/**
 * Turn an insightd {@link PurchaseVoucherDraft} body into the EXISTING cored
 * write path. This is the structural human-in-the-loop boundary: ACCEPT is the
 * only write, and there is no insightd ledger-write path.
 *
 * The draft `body.postings` are ALREADY fully balanced (purchase/expense DEBITs
 * + Input CGST/SGST/IGST DEBITs + party CREDIT = invoice_total), so accept POSTs
 * the body ESSENTIALLY VERBATIM to POST /companies/{id}/vouchers/purchase — NOT
 * the buildGstVoucherRequest form-rebuild path. dr_cr stays the wire "D"/"C"
 * verbatim (both rest.ts's wireDrCr and the mock's summarize canonicalize via
 * startsWith("D"), so "D"/"C" pass cleanly without translation).
 */

import type {
  CoredClient,
  GSTDraft,
  GSTRequest,
  PostingDraft,
  PostingRequest,
  VoucherDraftBody,
  VoucherRequest,
  VoucherResponse,
} from "../../api";
import { ensureLedger } from "./gstVoucher";
import { deriveGstSplit } from "./gstDerive";
import { addMoney, isZeroMoney, normalizeMoney } from "./money";

/**
 * PURE: convert a (fully-resolved) draft body into a cored {@link VoucherRequest}.
 *
 * Every posting MUST already have a non-null `ledger_id` — call
 * {@link resolveDraftLedgers} first if any are null. Throws if a null id remains.
 *
 * Mapping rules:
 *  - PostingDraft -> PostingRequest: drop `ledger_name`; `dr_cr` ("D"/"C") and
 *    `amount` (fixed-4 string) pass through verbatim.
 *  - GSTDraft -> GSTRequest: null -> undefined; carry exempt, ruleset_version,
 *    state codes, lines, and the three tax-ledger ids.
 *  - voucher_no/voucher_date/narration/party/party_ledger_id from the body
 *    (null or empty string -> omitted).
 */
export function draftBodyToVoucherRequest(
  body: VoucherDraftBody,
): VoucherRequest {
  const postings: PostingRequest[] = body.postings.map((p) => {
    if (p.ledger_id == null) {
      throw new Error(
        `draftBodyToVoucherRequest: posting "${p.ledger_name}" still has a null ledger_id — resolve ledgers before converting`,
      );
    }
    // Keep dr_cr ("D"/"C") and amount (fixed-4 string) verbatim; drop ledger_name.
    return { ledger_id: p.ledger_id, dr_cr: p.dr_cr, amount: p.amount };
  });

  const req: VoucherRequest = { postings };

  // Header scalars: omit null / empty.
  if (body.voucher_no) req.voucher_no = body.voucher_no;
  if (body.voucher_date) req.voucher_date = body.voucher_date;
  if (body.narration) req.narration = body.narration;
  if (body.party) req.party = body.party;
  if (body.party_ledger_id) req.party_ledger_id = body.party_ledger_id;
  if (body.voucher_type_id) req.voucher_type_id = body.voucher_type_id;

  if (body.gst) {
    const g = body.gst;
    const gst: GSTRequest = {
      ruleset_version: g.ruleset_version,
      reverse_charge: g.reverse_charge,
      exempt: g.exempt,
      lines: g.lines.map((l) => ({
        taxable_value: l.taxable_value,
        gst_rate_pct: l.gst_rate_pct,
        ...(l.hsn ? { hsn: l.hsn } : {}),
      })),
    };
    // null -> undefined (omit) for every optional code / ledger id.
    if (g.supplier_state_code) gst.supplier_state_code = g.supplier_state_code;
    if (g.place_of_supply_state_code)
      gst.place_of_supply_state_code = g.place_of_supply_state_code;
    if (g.cgst_ledger_id) gst.cgst_ledger_id = g.cgst_ledger_id;
    if (g.sgst_ledger_id) gst.sgst_ledger_id = g.sgst_ledger_id;
    if (g.igst_ledger_id) gst.igst_ledger_id = g.igst_ledger_id;
    req.gst = gst;
  }

  return req;
}

// ---------------------------------------------------------------------------
// Per-field edit-through (WS8b). A DIRTY accept rebuilds the POSTed body from
// the user's edits, keeping the draft's PURCHASE orientation (expense Dr +
// Input-tax Dr + party Cr). It deliberately does NOT route through
// buildGstVoucherRequest — that helper is SALES-shaped (party Dr / income Cr /
// output-tax Cr) and would post a REVERSED purchase that still passes the
// server's dr_cr-blind GST verify and a balanced TB. So the rebuild is a local,
// orientation-preserving function that reuses only the pure split derivation.
// ---------------------------------------------------------------------------

/** The subset of a prefilled draft a human may edit in v1. Absent = unchanged. */
export interface DraftEdits {
  voucherDate?: string;
  narration?: string;
  taxableValue?: string;
  gstRatePct?: string;
  placeOfSupply?: string;
}

/** Thrown when a draft cannot be safely rebuilt after an edit (guarded shapes). */
export class DraftRebuildError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "DraftRebuildError";
  }
}

/**
 * A draft is rebuildable ONLY when it is the simple, single-line intra/inter GST
 * purchase the split derivation models: a GST block, exactly one line, no
 * reverse-charge and no exemption (neither is modelled by deriveGstSplit), and
 * exactly one NON-tax debit posting (the expense leg). Multi-line drafts, RCM
 * and exempt drafts are DEFERRED (disclosed) — their edits keep the pristine
 * body. This mirrors the guard the plan's own e2e assumes.
 */
export function canRebuildDraft(body: VoucherDraftBody): boolean {
  const g = body.gst;
  if (!g) return false;
  if (g.reverse_charge || g.exempt) return false;
  if (g.lines.length !== 1) return false;
  const taxIds = taxLedgerIds(g);
  const nonTaxDebits = body.postings.filter(
    (p) => p.dr_cr === "D" && (p.ledger_id == null || !taxIds.has(p.ledger_id)),
  );
  return nonTaxDebits.length === 1;
}

/** The set of NON-null input-tax ledger ids declared on the draft's gst block. */
function taxLedgerIds(g: GSTDraft): Set<string> {
  return new Set(
    [g.cgst_ledger_id, g.sgst_ledger_id, g.igst_ledger_id].filter(
      (x): x is string => !!x,
    ),
  );
}

/**
 * PURE: apply the human's edits to a prefilled draft body and return a NEW,
 * balanced body that PRESERVES the purchase orientation. Intra/inter is derived
 * from the draft's supplier_state_code (the VENDOR's, never the home state) vs
 * the possibly-edited place of supply — NOT from any home-state code. The three
 * tax-ledger ids are carried from the draft's gst block verbatim (Input CGST/…,
 * never the Output CGST buildGstVoucherRequest would create). Throws
 * DraftRebuildError when the shape is not rebuildable or a needed tax ledger id
 * is absent — the caller must NOT post in that case (HITL preserved).
 */
export function applyDraftEdits(
  body: VoucherDraftBody,
  edits: DraftEdits,
): VoucherDraftBody {
  if (!canRebuildDraft(body)) {
    throw new DraftRebuildError(
      "this draft cannot be edited in v1 (multi-line, reverse-charge or exempt) — accept as-is",
    );
  }
  const g = body.gst as GSTDraft;
  const line = g.lines[0]!;

  const taxable = normalizeMoney(edits.taxableValue ?? line.taxable_value);
  const rate = edits.gstRatePct ?? line.gst_rate_pct;
  const supplierState = g.supplier_state_code ?? "";
  const placeOfSupply = edits.placeOfSupply ?? g.place_of_supply_state_code ?? "";

  const split = deriveGstSplit(taxable, rate, supplierState, placeOfSupply);

  // Identify the existing legs (orientation-preserving; ledger ids reused).
  const taxIds = taxLedgerIds(g);
  const expense = body.postings.find(
    (p) => p.dr_cr === "D" && (p.ledger_id == null || !taxIds.has(p.ledger_id)),
  );
  if (!expense) throw new DraftRebuildError("no expense debit posting found");
  const party =
    body.postings.find(
      (p) => p.dr_cr === "C" && p.ledger_id === body.party_ledger_id,
    ) ?? body.postings.find((p) => p.dr_cr === "C");
  if (!party) throw new DraftRebuildError("no party credit posting found");

  // Rebuild the tax DEBIT legs from the derived split (non-zero heads only),
  // using the draft's own input-tax ledger ids. A needed-but-missing id is a
  // hard error, not a silent create-under-the-wrong-group.
  const taxPostings: PostingDraft[] = [];
  const pushTax = (
    amount: string,
    ledgerId: string | null | undefined,
    name: string,
  ) => {
    if (isZeroMoney(amount)) return;
    if (!ledgerId) {
      throw new DraftRebuildError(
        `edited GST needs the ${name} ledger id, which the draft does not carry`,
      );
    }
    taxPostings.push({ ledger_id: ledgerId, ledger_name: name, dr_cr: "D", amount });
  };
  if (split.intra) {
    pushTax(split.cgst, g.cgst_ledger_id, "Input CGST");
    pushTax(split.sgst, g.sgst_ledger_id, "Input SGST");
  } else {
    pushTax(split.igst, g.igst_ledger_id, "Input IGST");
  }

  // invoice total = taxable + whichever heads are non-zero.
  const invoiceTotal = addMoney(
    addMoney(taxable, split.cgst),
    addMoney(split.sgst, split.igst),
  );

  const postings: PostingDraft[] = [
    { ...expense, dr_cr: "D", amount: taxable },
    ...taxPostings,
    { ...party, dr_cr: "C", amount: invoiceTotal },
  ];

  const gst: GSTDraft = {
    ...g,
    supplier_state_code: g.supplier_state_code,
    place_of_supply_state_code: placeOfSupply || g.place_of_supply_state_code,
    lines: [{ ...line, taxable_value: taxable, gst_rate_pct: rate }],
  };

  return {
    ...body,
    voucher_date: edits.voucherDate ?? body.voucher_date,
    narration: edits.narration ?? body.narration,
    postings,
    gst,
  };
}

/**
 * ASYNC: apply edits, resolve any null ledgers, and POST the rebuilt body via the
 * SAME proven path as a pristine accept (resolveDraftLedgers +
 * draftBodyToVoucherRequest). The only difference from acceptPurchaseDraft is
 * the edit-through rebuild in front of it.
 */
export async function acceptEditedPurchaseDraft(
  client: CoredClient,
  companyID: string,
  body: VoucherDraftBody,
  edits: DraftEdits,
): Promise<VoucherResponse> {
  const edited = applyDraftEdits(body, edits);
  const resolved = await resolveDraftLedgers(client, companyID, edited);
  return client.postPurchaseVoucher(
    companyID,
    draftBodyToVoucherRequest(resolved),
  );
}

/**
 * Resolve every null `ledger_id` in a draft body via Alt+C create-on-the-fly
 * (memoized {@link ensureLedger}), returning a NEW body with all ids filled.
 * The input body is not mutated.
 *
 * Resolution rules (deterministic):
 *  - party_ledger_id null -> ensureLedger under "Sundry Creditors", named
 *    body.party (fallback "Sundry Creditor"). ALSO patch the matching party
 *    CREDIT posting's null id to the resolved party id: the party posting is the
 *    one whose ledger_name === body.party, or — failing that — the single CREDIT
 *    posting. (Both are documented & deterministic; the fallback covers a body
 *    whose party posting name was relabelled.)
 *  - any other posting.ledger_id null -> ensureLedger under "Purchase Accounts",
 *    named by the posting's ledger_name.
 */
export async function resolveDraftLedgers(
  client: CoredClient,
  companyID: string,
  body: VoucherDraftBody,
): Promise<VoucherDraftBody> {
  // Resolve the party id (if missing) up front so we can patch its posting.
  let partyLedgerId = body.party_ledger_id ?? null;
  if (partyLedgerId == null) {
    partyLedgerId = await ensureLedger(client, companyID, {
      name: body.party ?? "Sundry Creditor",
      groupName: "Sundry Creditors",
    });
  }

  // Identify the party CREDIT posting index to patch when its id is null:
  // first a name match against body.party, else the single CREDIT posting.
  const partyName = body.party ?? null;
  let partyPostingIdx = partyName
    ? body.postings.findIndex(
        (p) => p.dr_cr === "C" && p.ledger_name === partyName,
      )
    : -1;
  if (partyPostingIdx < 0) {
    const creditIdxs = body.postings
      .map((p, i) => (p.dr_cr === "C" ? i : -1))
      .filter((i) => i >= 0);
    if (creditIdxs.length === 1) partyPostingIdx = creditIdxs[0] as number;
  }

  const postings = await Promise.all(
    body.postings.map(async (p, i) => {
      if (p.ledger_id != null) return p;
      if (i === partyPostingIdx) {
        // The party credit leg: use the resolved party id (already ensured).
        return { ...p, ledger_id: partyLedgerId as string };
      }
      // A proposed purchase/expense master (Alt+C) under Purchase Accounts.
      const id = await ensureLedger(client, companyID, {
        name: p.ledger_name,
        groupName: "Purchase Accounts",
      });
      return { ...p, ledger_id: id };
    }),
  );

  return { ...body, party_ledger_id: partyLedgerId, postings };
}

/**
 * ASYNC convenience used by the screen AND the e2e: resolve null ledgers, then
 * POST the converted (balanced, fixed-4-string) body to the existing cored
 * purchase voucher endpoint. This is the ONLY write path for a draft.
 */
export async function acceptPurchaseDraft(
  client: CoredClient,
  companyID: string,
  body: VoucherDraftBody,
): Promise<VoucherResponse> {
  const resolved = await resolveDraftLedgers(client, companyID, body);
  return client.postPurchaseVoucher(
    companyID,
    draftBodyToVoucherRequest(resolved),
  );
}
