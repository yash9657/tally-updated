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
  GSTRequest,
  PostingRequest,
  VoucherDraftBody,
  VoucherRequest,
  VoucherResponse,
} from "../../api";
import { ensureLedger } from "./gstVoucher";

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
