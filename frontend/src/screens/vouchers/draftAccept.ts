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
 *
 * ⛔ "ESSENTIALLY VERBATIM" IS NOT "ONLY THE POSTINGS", AND READING IT THAT WAY
 * COST THIS MODULE THREE DEFECTS THAT WERE ALL DRIVEN IN A BROWSER:
 *
 *  1. ACCEPT WAS NOT ONCE-ONLY. It POSTed and returned, and nothing — here, on
 *     the screen, or on the server — stopped the same supplier bill being posted
 *     twice: a duplicated payable and duplicated input tax. ⛔ AND THE FIRST FIX
 *     FOR IT WAS INSUFFICIENT: after a session memo plus a day-book lookup were
 *     added, A BROWSER RE-DRIVE STILL PRODUCED TWO REAL VOUCHERS from one bill,
 *     because a draft whose invoice number the OCR never read gives the day book
 *     nothing to match on and a page reload empties the memo. The guard is now
 *     FOUR layers ending in a TERMINAL SERVER-SIDE DRAFT STATE — insightd's
 *     `status: "accepted"` — so the truth is no longer only in the client. See
 *     {@link acceptPurchaseDraft} for the layers and what each one alone misses.
 *  2. THE BILL-WISE REFERENCE THE SCREEN CERTIFIED WAS DROPPED. The panel renders
 *     "referenced in full (…)" for a bill-by-bill party and this converter emitted
 *     no `bill_allocations` key at all, so the accepted payable never reached the
 *     Outstandings report and could never be settled. See
 *     {@link draftBillAllocations} — the wire shape is billAllocation.ts's, reused
 *     rather than re-derived.
 *  3. A REFUSED ACCEPT STILL WROTE MASTERS. Ledger creation necessarily precedes
 *     the voucher POST (the voucher needs the ids), and there is NO route that
 *     deletes a ledger, so the orphan cannot be undone. What it can be is
 *     VISIBLE: see {@link VoucherAcceptFailedError}.
 */

import type {
  BillAllocationRequest,
  CoredClient,
  DayBookEntry,
  GSTDraft,
  GSTRequest,
  LedgerListItem,
  PostingDraft,
  PostingRequest,
  VoucherDraftBody,
  VoucherRequest,
  VoucherResponse,
} from "../../api";
// insightd, NOT cored: the draft LIFECYCLE lives on the service that owns drafts.
// Imported from the concrete modules rather than from ../../api because that
// barrel re-exports THIS file (api/index.ts), and a value import through it would
// close a runtime import cycle.
import { ApiError, createHttpClient, type HttpClient } from "../../api/http";
import { createInsightdClient, type InsightdClient } from "../../api/insightd";
import {
  buildBillAllocations,
  defaultNewRef,
  type BillBearingLeg,
  type DraftBillRef,
} from "./billAllocation";
import { ensureLedger } from "./gstVoucher";
import { deriveGstSplit } from "./gstDerive";
// The EXACT BigInt-scaled helpers, not money.ts's parseFloat-based ones. Both the
// server and the mock check the bill-reference sum against the posting TO THE
// PAISA with no tolerance, so a total computed on two doubles can disagree with
// them about whether a bill is fully referenced. Same reasoning as
// billAllocation.ts and costAllocation.ts.
import { formatFixed4, parseFixed4 } from "./itemInvoice";
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
 *  - `billAllocations` (the SECOND argument) -> `bill_allocations`, omitted when
 *    empty. It is an argument and NOT derived from the body because
 *    {@link VoucherDraftBody} has no bill-reference field: insightd's contract
 *    does not model one, so the references are the SCREEN's (or this module's
 *    default — {@link draftBillAllocations}), never the OCR pipeline's.
 */
export function draftBodyToVoucherRequest(
  body: VoucherDraftBody,
  billAllocations: readonly BillAllocationRequest[] = [],
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

  // ⛔⛔ THE BILL-WISE REFERENCES, AND OMITTING THEM WAS THE LIVE DEFECT.
  // `ledger.go` writes bill references from `in.BillAllocations` — the CLIENT
  // supplies them on EVERY path — so a body without this key posts a payable that
  // is in Sundry Creditors on the Balance Sheet and ABSENT from the Outstandings
  // report: money nobody can age, chase or settle against. The draft panel had
  // already rendered "referenced in full (…)" by the time this ran, so the
  // operator was shown a referenced bill and the books got an unreferenced one.
  // Empty stays OMITTED (not `[]`): no references at all is the legal, normal
  // state for a party that does not maintain bill-by-bill.
  if (billAllocations.length > 0) req.bill_allocations = [...billAllocations];

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
// BILL-WISE REFERENCES ON AN ACCEPTED DRAFT (defect 2).
//
// ⛔ THE WIRE SHAPE IS billAllocation.ts's, NOT A SECOND ONE. `buildBillAllocations`
// is the builder every other voucher path uses; the reference name is left EMPTY on
// purpose (post() stamps the number it allocates inside the transaction — the client
// cannot know it) and the sum-to-posting invariant is exact to the paisa. A local
// re-derivation here would be the drifted second copy that always wins for whichever
// caller reaches it first.
// ---------------------------------------------------------------------------

/**
 * The party's BILL-BEARING LEG on a draft body, or null when there is none.
 *
 * Signed net over EVERY posting on `party_ledger_id` (debits positive), so a
 * purchase — which credits the creditor — is referenced on the credit side with
 * no special case, exactly as VoucherScreen's own `partyLeg` does it. null when
 * the draft has no party ledger yet (call {@link resolveDraftLedgers} first) or
 * when the party nets to zero, because a reference for nothing is not a
 * reference and the server would refuse it.
 */
export function draftPartyBillLeg(
  body: VoucherDraftBody,
): BillBearingLeg | null {
  const partyID = body.party_ledger_id;
  if (!partyID) return null;
  let units = 0n;
  for (const p of body.postings) {
    if (p.ledger_id !== partyID) continue;
    const amt = parseFixed4(p.amount);
    units += p.dr_cr.toUpperCase().startsWith("D") ? amt : -amt;
  }
  if (units === 0n) return null;
  return {
    ledgerId: partyID,
    drCr: units < 0n ? "C" : "D",
    amount: formatFixed4(units < 0n ? -units : units),
  };
}

/**
 * The references an accepted draft defaults to: ONE `NEW` reference for the whole
 * party leg — byte-for-byte what the panel shows and certifies as
 * "referenced in full (…)".
 *
 * ⛔ GATED ON THE PARTY'S OWN `maintain_bill_by_bill` FLAG, and the gate is the
 * point. The panel renders only when the chosen party maintains bill-by-bill
 * (VoucherScreen: `raiseParty.maintain_bill_by_bill`), so that is exactly the
 * condition under which a reference was certified to the operator. Defaulting one
 * for a party who does not maintain bill-wise balances would invent a
 * certification nobody made and list bills for a ledger that opted out of having
 * any. `chart` is the company's chart of accounts as read for THIS accept.
 */
export function defaultDraftBillRefs(
  body: VoucherDraftBody,
  chart: readonly LedgerListItem[],
): DraftBillRef[] {
  const leg = draftPartyBillLeg(body);
  if (leg === null) return [];
  const party = chart.find((l) => l.id === leg.ledgerId);
  if (party?.maintain_bill_by_bill !== true) return [];
  return defaultNewRef(leg);
}

/**
 * PURE: the `bill_allocations` array for a (resolved) draft body and a set of
 * references. Empty in, empty out — and empty out means the key is omitted.
 */
export function draftBillAllocations(
  body: VoucherDraftBody,
  refs: readonly DraftBillRef[],
): BillAllocationRequest[] {
  if (refs.length === 0) return [];
  const leg = draftPartyBillLeg(body);
  if (leg === null) return [];
  return buildBillAllocations(leg, refs);
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
 *
 * ⛔ THE ONCE-ONLY GUARD IS KEYED ON THE **PRISTINE** BODY, NOT THE EDITED ONE.
 * Accepting a draft pristine and then re-opening it, changing the date and
 * accepting again is the SAME supplier bill posted twice — and keying the guard on
 * what is posted would let it through, because the edit changed the key. The
 * document's identity is the draft, and the draft is `body`.
 */
export function acceptEditedPurchaseDraft(
  client: CoredClient,
  companyID: string,
  body: VoucherDraftBody,
  edits: DraftEdits,
  opts: AcceptDraftOptions = {},
): Promise<VoucherResponse> {
  return guardAcceptOnce(companyID, body, opts, () => {
    // applyDraftEdits is PURE and throws before anything is written, which is the
    // order defect 3 asks for: a body that cannot be rebuilt must not have created
    // a ledger master on its way to failing.
    const edited = applyDraftEdits(body, edits);
    // ⛔ THE PRISTINE `body` IS PASSED AS THE IDENTITY, AND OMITTING IT WAS A REAL
    // BYPASS. Without it, postDraftBody asked the day book about the EDITED bill, so
    // editing the date bought a second post of the same numbered supplier bill after
    // a reload — the same "keyed on the pristine draft" rule the memo above obeys,
    // which the books check did not. See postDraftBody.
    return postDraftBody(client, companyID, edited, opts, body);
  });
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
 *
 * ⛔⛔ IT USED TO BE POSSIBLE TO ACCEPT THE SAME DRAFT TWICE, AND THAT WAS THE
 * MONEY DEFECT. This function POSTed and returned; it marked nothing consumed. The
 * draft stayed `status: "ready"` and stayed in the Bill Inbox, so re-opening it and
 * pressing Ctrl+A again posted the SAME supplier bill a second time — a duplicated
 * payable and duplicated input tax, with every layer reporting success.
 *
 * ⛔⛔ AND THE FIRST FIX FOR IT WAS NOT ENOUGH — A BROWSER RE-DRIVE PRODUCED TWO
 * REAL VOUCHERS AFTERWARDS. That fix was two client-side layers: the day-book
 * lookup below and a page-lifetime session memo. Both hold for a bill that HAS a
 * supplier invoice number and a date. The bill whose number the OCR never read
 * (`voucher_no: ""`) was accepted, THE PAGE WAS RELOADED, and it was accepted
 * again — purchase 3 and purchase 4, same party, same date, same 5000.0000 +
 * 900.0000 IGST. The day book had nothing to match on and the reload had emptied
 * the memo. A guard whose only copy of the truth is in the page is defeated by
 * reloading the page.
 *
 * ⛔⛔ AND A SECOND RE-DRIVE DEFEATED ALL FOUR OF THOSE LAYERS BY RACING TWO SEPARATE
 * BROWSERS, which is why there is now a FIFTH that is not a check at all. Every one of
 * layers 1–4 asks "has this draft been accepted?" — and asks it BEFORE this attempt has
 * posted, when the honest answer is still "no". Two browsers on one numberless draft
 * were both told "no", both posted (vouchers 11 and 12, 27000.0000 + 4860.0000 IGST
 * each), and only the second MARKING was refused, after the money had moved. Layer 5
 * changes the ORDERING instead of adding a check: the poster RESERVES the draft first,
 * so the loser is refused before it writes anything at all. See {@link postDraftBody}
 * step 4 and {@link claimDraftForPost}.
 *
 * So the guard is now FIVE layers, ordered cheapest-and-most-local first. None of
 * them may be removed: each covers a case the others cannot see.
 *
 *   1. {@link guardAcceptOnce}'s per-(company, draft) SESSION memo. Also
 *      coalesces a double-press of Ctrl+A into one voucher.
 *   2. {@link assertNotAlreadyOnTheBooks} asks THE BOOKS: a purchase already
 *      posted for this party, on this date, under this supplier invoice number is
 *      this bill. Survives a reload, another tab AND another machine — but only
 *      for a draft that carries a number and a date. It is asked about the
 *      PRISTINE draft as well as about what is being posted; see
 *      {@link postDraftBody} for why an edited date used to slip past it.
 *   3. The DURABLE memo, {@link durableAcceptLookup} — layer 1's key written to
 *      `localStorage`. This is what closes the driven case above without needing
 *      any other component's help: same key, same refusal, but it survives F5.
 *      ⚠️ It is per-browser-profile: another browser or another machine does not
 *      see it. That is what layer 4 is for.
 *   4. insightd's own TERMINAL DRAFT STATE, {@link draftLifecyclePort}. A draft
 *      that has become a voucher is moved to `status: "accepted"` carrying the
 *      voucher id, server-side, so the truth is not in the client at all and a
 *      reload cannot defeat it. This is the ROOT fix; the other three are defence
 *      in depth around it. It also makes the Bill Inbox honest — the row used to
 *      still read "…· ready · 95%" after a successful accept, which is an
 *      invitation to post the bill a second time.
 *   5. ⛔ THE POSTING CLAIM, {@link claimDraftForPost} — the only layer that is an
 *      ORDERING and not a question. It reserves the draft server-side (an atomic
 *      SELECT … FOR UPDATE compare-and-set) BEFORE the voucher POST and before any
 *      ledger master is created, so a CONCURRENT poster is refused while refusing it
 *      is still free. Layers 1–4 cannot do this at all: each of them can only report
 *      on a write that has already happened. Its cost is that a claim can outlive the
 *      attempt that took it, which is why a failed post releases it immediately and
 *      why the lease expires — see {@link releaseClaimAfterFailure}.
 *
 * ⚠️ cored itself still does not enforce unique purchase voucher numbers
 * (`INSERT INTO voucher_version` has no such constraint), and a purchase number is
 * the SUPPLIER's reference, so it could not be globally unique anyway. Nothing
 * here can be delegated to a database constraint.
 *
 * `opts.draftId` is the authoritative identity when the caller has it (the
 * `PurchaseVoucherDraft.draft_id` it fetched) and should be passed; without it the
 * memo falls back to the draft body's own content (documented on
 * {@link draftAcceptKey}) and layer 4 resolves the draft by EXACT body match
 * ({@link draftLifecyclePort}).
 */
export function acceptPurchaseDraft(
  client: CoredClient,
  companyID: string,
  body: VoucherDraftBody,
  opts: AcceptDraftOptions = {},
): Promise<VoucherResponse> {
  return guardAcceptOnce(companyID, body, opts, () =>
    postDraftBody(client, companyID, body, opts),
  );
}

/** Caller-supplied context for an accept. Every field is optional. */
export interface AcceptDraftOptions {
  /**
   * The `PurchaseVoucherDraft.draft_id` this body came from. When present it is
   * the once-only key, which is strictly better than the content fallback: two
   * genuinely different bills that happen to look identical get different keys.
   *
   * ⚠️ IT IS ALSO THE ADDRESS insightd's terminal `accepted` state is recorded
   * against, so passing it saves the body-match lookup in
   * {@link insightdDraftLifecycle} (one list + a few gets per accept). The F9
   * screen does NOT pass it yet — VoucherScreen.tsx has
   * `loadedDraft.draft.draft_id` right there and calls
   * `acceptPurchaseDraft(client, companyID, draftBody)` — and wiring it through is
   * a one-line change in a file this round could not edit. Layer 4 works without
   * it, by exact body match; with it, it is both cheaper and exact by construction.
   */
  draftId?: string;
  /**
   * The bill-wise references the OPERATOR keyed in the panel. Omitted =
   * {@link defaultDraftBillRefs} (one NEW reference for the whole party leg, which
   * is what the panel defaults to and certifies). Pass `[]` to post none.
   */
  billRefs?: readonly DraftBillRef[];
}

/** Thrown when a draft is refused BEFORE anything is written. */
export class DraftNotPostableError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "DraftNotPostableError";
  }
}

/**
 * Thrown INSTEAD OF POSTING when this supplier bill is already on the books, or
 * when this draft was already accepted in this session.
 *
 * It is a REFUSAL and not a silent replay of the first response on purpose: an
 * operator who is told "Saved, PUR-42" twice cannot tell a duplicate that was
 * blocked from a duplicate that was written, and this is the one place in the flow
 * where those two must not look alike.
 */
export class DraftAlreadyAcceptedError extends Error {
  /** The voucher the earlier accept produced, when it is known. */
  readonly voucherNo: string;
  readonly voucherID: string;
  constructor(message: string, voucherNo: string, voucherID: string) {
    super(message);
    this.name = "DraftAlreadyAcceptedError";
    this.voucherNo = voucherNo;
    this.voucherID = voucherID;
  }
}

/**
 * Thrown INSTEAD OF POSTING when ANOTHER poster holds a live claim on this draft.
 *
 * ⛔ THE WHOLE VALUE OF THIS ERROR IS THAT NOTHING HAS BEEN WRITTEN WHEN IT IS
 * THROWN — not a voucher, not a ledger master. It is raised by the CLAIM, which runs
 * before the first write of any kind, which is the only moment at which a losing
 * racer can still be stopped for free. Its predecessor state of the world was: both
 * browsers posted, and the loser found out afterwards.
 *
 * ⚠️ IT IS NOT {@link DraftAlreadyAcceptedError} AND THE DIFFERENCE MATTERS TO THE
 * OPERATOR. "Already accepted" is TERMINAL — the bill is a voucher, stop. This one is
 * TRANSIENT: the other attempt may yet fail, in which case the claim is released (or
 * lapses) and the bill becomes postable again. So the message says "already being
 * posted" and names when the lease expires, rather than telling someone their bill is
 * done when it may not be.
 */
export class DraftBeingPostedError extends Error {
  /** ISO-8601, when the other poster's lease lapses. "" when unknown. */
  readonly claimExpiresAt: string;
  constructor(message: string, claimExpiresAt: string) {
    super(message);
    this.name = "DraftBeingPostedError";
    this.claimExpiresAt = claimExpiresAt;
  }
}

/**
 * A refused voucher POST that had already created ledger masters (defect 3, and
 * register row D81 for the ordinary — non-draft — accept path).
 *
 * ⚠️ IT IS NOT DRAFT-SPECIFIC, which is why it is not called `DraftAccept…`. It was,
 * for as long as the draft accept was the only path that disclosed its orphans; the
 * five builders in VoucherScreen mint up to five masters on one Ctrl+A and named
 * none of them. Both paths now raise THIS error, through {@link voucherAcceptFailure}.
 *
 * ⛔ THE ORPHAN CANNOT BE UNDONE, AND PRETENDING OTHERWISE WOULD BE WORSE. The
 * voucher needs real ledger ids, so the masters must exist before the POST that
 * may be refused; and cored exposes POST /ledgers, GET /ledgers and
 * PUT /ledgers/{id}/opening — there is NO DELETE. So the honest remedy is to make
 * the orphan VISIBLE and HARMLESS rather than invisible: the operator is told
 * exactly which masters now exist, and told that a retry REUSES them (ensureLedger
 * resolves-then-creates against the live chart, so it will not mint a second one).
 *
 * ⚠️ `message` keeps the server's own refusal FIRST and unaltered — that is the
 * sentence the operator needs in order to fix the voucher.
 */
export class VoucherAcceptFailedError extends Error {
  readonly createdLedgers: readonly { id: string; name: string }[];
  constructor(
    message: string,
    cause: unknown,
    createdLedgers: readonly { id: string; name: string }[],
  ) {
    super(message);
    this.name = "VoucherAcceptFailedError";
    this.cause = cause;
    this.createdLedgers = createdLedgers;
  }
}

/**
 * The one place the orphan sentence is WRITTEN — used by the draft accept below
 * AND by the ordinary voucher accept in VoucherScreen (register row D81).
 *
 * ⛔ IT IS A FUNCTION, NOT A SECOND COPY, AND THAT IS THE POINT. This module's own
 * §1.3 lesson is that a second copy is exactly how a defect gets fixed once and
 * shipped twice: `ensureLedger` was corrected in one caller and left broken in the
 * next, and a browser found it. The draft path had this disclosure from the day it
 * was written; the five builders on the ordinary voucher screen mint up to five
 * masters and said nothing at all. One sentence, two callers.
 *
 * ⚠️ THE SERVER'S OWN REFUSAL STAYS FIRST AND UNALTERED. That is the sentence the
 * operator needs in order to fix the voucher; the orphan note is what they need
 * afterwards, and putting it first would bury the cause.
 */
export function voucherAcceptFailure(
  cause: unknown,
  minted: readonly { id: string; name: string }[],
): VoucherAcceptFailedError {
  const names = minted.map((m) => m.name).join(", ");
  const one = minted.length === 1;
  return new VoucherAcceptFailedError(
    `${cause instanceof Error ? cause.message : String(cause)} — this accept had already created ` +
      `${minted.length} new ledger master${one ? "" : "s"} (${names}), which now ` +
      `${one ? "has" : "have"} no voucher against ${one ? "it" : "them"}. ` +
      `Retrying this accept REUSES ${one ? "it" : "them"} rather than creating more. ` +
      `There is no route that deletes a ledger, so ${one ? "it" : "they"} cannot be undone automatically.`,
    cause,
    minted,
  );
}

/**
 * PURE PRE-FLIGHT, run BEFORE any write (defect 3).
 *
 * Everything here is something the server would refuse anyway — but it would
 * refuse it AFTER `resolveDraftLedgers` had already created the party and every
 * proposed expense master, leaving orphans behind. Checking it here costs nothing
 * and moves those refusals to before the first write.
 *
 *  - NO POSTINGS is the "processing" placeholder insightd stores while real OCR
 *    runs (routes_drafts.py `_processing_placeholder`: an empty body, on purpose,
 *    so "nothing downstream can post it"). Nothing downstream now does.
 *  - UNBALANCED contradicts this module's own premise ("the draft body.postings
 *    are ALREADY fully balanced"), so it is a broken draft, not an entry to fix
 *    server-side.
 */
export function assertPostableDraft(body: VoucherDraftBody): void {
  if (body.postings.length === 0) {
    throw new DraftNotPostableError(
      "this draft carries no postings — it has not finished processing, so there is nothing to accept",
    );
  }
  let debits = 0n;
  let credits = 0n;
  for (const p of body.postings) {
    const amt = parseFixed4(p.amount);
    if (amt < 0n) {
      throw new DraftNotPostableError(
        `this draft cannot be accepted: posting "${p.ledger_name}" carries a negative amount (${p.amount}) — reverse the side instead`,
      );
    }
    if (p.dr_cr.toUpperCase().startsWith("D")) debits += amt;
    else credits += amt;
  }
  if (debits !== credits) {
    throw new DraftNotPostableError(
      `this draft does not balance: debits total ${formatFixed4(debits)} against credits ` +
        `${formatFixed4(credits)}, a difference of ${formatFixed4(debits - credits)}. ` +
        `Accepting it would be refused by the server after creating its ledger masters, so it is refused here first.`,
    );
  }
}

// ---------------------------------------------------------------------------
// ONCE-ONLY ACCEPT
// ---------------------------------------------------------------------------

interface AcceptRecord {
  promise: Promise<VoucherResponse>;
  /** The response, once it has arrived. null while still in flight. */
  done: VoucherResponse | null;
}

const ACCEPTED_DRAFTS = new Map<string, AcceptRecord>();

/**
 * Test-only: forget every draft this session has accepted.
 *
 * ⛔ THIS IS "A PAGE RELOAD" IN THE TESTS, AND IT DELIBERATELY DOES **NOT** TOUCH
 * THE DURABLE MEMO. Clearing both together would make every "…still refuses after
 * a reload" assertion in draftAccept.test.ts vacuous — a reload empties this map
 * and nothing else. Use {@link _resetDurableAcceptRecords} for the durable store,
 * which models a different, larger event (a new browser profile / new machine).
 */
export function _resetAcceptedDraftCache(): void {
  ACCEPTED_DRAFTS.clear();
}

// ---------------------------------------------------------------------------
// LAYER 3 — THE DURABLE MEMO (localStorage)
//
// ⛔ WHY A SECOND MEMO EXISTS AT ALL: A BROWSER RE-DRIVE DEFEATED THE FIRST ONE
// BY PRESSING F5. Layer 1 lives in a module-level Map, so a reload empties it, and
// layer 2 (the day book) cannot see a bill whose supplier invoice number the OCR
// never read. Accept → reload → accept produced TWO real purchase vouchers for one
// supplier bill. This is layer 1's key, written down where a reload cannot reach
// it: the SAME key, the SAME refusal, so it is not a new heuristic — it is the
// existing one made durable.
//
// ⚠️ WHAT IT IS NOT. localStorage is per-browser-profile and per-origin: another
// browser, another machine or a cleared profile does not see it. It is therefore
// NOT the fix — insightd's terminal draft state (layer 4) is — but it is the layer
// that needs no other component's cooperation, so it holds even when insightd is
// unreachable or the draft has aged out of it.
//
// ⚠️ EVERY ACCESS IS GUARDED. Safari in private mode throws on `localStorage`
// access, a quota-full profile throws on write, and a hand-edited value can be any
// JSON at all. None of that may break an accept: a storage that cannot be read
// degrades to "no opinion" (the other three layers still run) and a write that
// fails is swallowed — losing a memo entry costs a duplicate-detection chance,
// while throwing here would cost the operator the voucher they are trying to post.
// ---------------------------------------------------------------------------

/** The one localStorage key this module owns. Versioned so a shape change is a new key. */
const DURABLE_ACCEPTS_KEY = "tally.vouchers.acceptedDrafts.v1";

/**
 * How many accepted drafts to remember. A bookkeeper posts a few hundred purchase
 * bills a month, so this is months of cover; the cap only exists so a long-lived
 * profile cannot grow the entry without bound. Eviction is OLDEST-FIRST, which is
 * the right direction: a bill accepted last year is a bill nobody is about to
 * re-accept by mistake, and layer 2 still covers it if it carries a number.
 */
const DURABLE_ACCEPTS_MAX = 500;

interface DurableAcceptRecord {
  voucherNo: string;
  voucherID: string;
  /** ISO-8601, when the accept succeeded. Used for eviction and for the message. */
  at: string;
}

/** localStorage, or null when there is none / access throws. Never throws. */
function durableStore(): Storage | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    // Safari private mode throws on the property access itself.
    return null;
  }
}

/** The whole memo, or `{}` for absent / unreadable / malformed storage. */
function readDurableAccepts(): Record<string, DurableAcceptRecord> {
  const store = durableStore();
  if (store === null) return {};
  try {
    const raw = store.getItem(DURABLE_ACCEPTS_KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      return {};
    }
    return parsed as Record<string, DurableAcceptRecord>;
  } catch {
    return {};
  }
}

/**
 * What this browser profile remembers accepting under `key`, or null.
 *
 * A record missing its voucher ids is treated as ABSENT rather than as a refusal:
 * refusing an accept on the strength of a value this module cannot describe to the
 * operator ("already accepted as …what?") is worse than falling through to the
 * other three layers.
 */
function durableAcceptLookup(key: string): DurableAcceptRecord | null {
  const rec = readDurableAccepts()[key];
  if (rec === undefined || typeof rec !== "object" || rec === null) return null;
  const voucherID = typeof rec.voucherID === "string" ? rec.voucherID : "";
  const voucherNo = typeof rec.voucherNo === "string" ? rec.voucherNo : "";
  if (voucherID === "" && voucherNo === "") return null;
  return { voucherID, voucherNo, at: typeof rec.at === "string" ? rec.at : "" };
}

/** Write one accepted draft down. Best-effort: a failure is swallowed by design. */
function rememberDurableAccept(key: string, rec: DurableAcceptRecord): void {
  const store = durableStore();
  if (store === null) return;
  try {
    const all = readDurableAccepts();
    all[key] = rec;
    const keys = Object.keys(all);
    if (keys.length > DURABLE_ACCEPTS_MAX) {
      // Oldest-first eviction. `at` is ISO-8601, so a lexical sort IS a time sort.
      keys
        .sort((a, b) => (all[a]?.at ?? "").localeCompare(all[b]?.at ?? ""))
        .slice(0, keys.length - DURABLE_ACCEPTS_MAX)
        .forEach((k) => delete all[k]);
    }
    store.setItem(DURABLE_ACCEPTS_KEY, JSON.stringify(all));
  } catch {
    // Quota exceeded / private mode. See the header comment: never break an accept
    // because a memo could not be written.
  }
}

/**
 * Test-only: forget what this BROWSER PROFILE remembers accepting.
 *
 * Separate from {@link _resetAcceptedDraftCache} on purpose — that one models a
 * page reload, this one models a different browser or a wiped profile, and a test
 * that conflates them cannot tell the two layers apart.
 */
export function _resetDurableAcceptRecords(): void {
  const store = durableStore();
  if (store === null) return;
  try {
    store.removeItem(DURABLE_ACCEPTS_KEY);
  } catch {
    /* nothing to do; the store is unusable and reads already return {} */
  }
}

/**
 * Does a voucher number carry insightd's PERSISTENCE-boundary redaction tag?
 *
 * insightd removes PII from every value that persists and writes a tag in its
 * place — `[PAN REDACTED 6442]` — because a PAN read out of an invoice number
 * would otherwise become an immutable `voucher_version.voucher_no`. The trailing
 * group is an UNSALTED fingerprint of the value that was removed, which is what
 * makes the day-book check below correct for a redacted number at all: without it
 * every PII-shaped invoice number collapsed to one string and a supplier's SECOND
 * bill on one date was refused as a duplicate of the first (D85).
 * `insightd/insightd/redaction/persistence.py` is the authority for this shape.
 *
 * ⚠️ IT IS A SECOND COPY OF A SHAPE THAT LIVES IN PYTHON, so it can drift — and it
 * is used ONLY to choose which SENTENCE the operator reads, never to decide whether
 * to refuse. If it drifts the advice gets less specific and nothing is mis-posted.
 * `drive-l21-d85.mjs` asserts the two still agree, against a real draft.
 */
const REDACTION_TAG_RE = /\[[A-Z][A-Z_]* REDACTED [0-9a-f]+\]/;

/**
 * What to tell the operator who is holding a bill this refusal will not post.
 *
 * ⛔ THE ADVICE HAS TO MATCH THE KEY, AND THAT IS THE LESSON OF D85 RATHER THAN AN
 * AESTHETIC. The two memo layers key on {@link draftAcceptKey}: the DRAFT id when
 * the caller passed one, and otherwise the body's own CONTENT. Those two keys have
 * two different remedies, and a sentence that names the wrong one sends the
 * operator down a dead end while reading as helpful — the refusal is fully present
 * and fully wrong. ⚠️ Note which branch the product actually takes today: the F9
 * screen does not pass `draftId` (see {@link AcceptDraftOptions}), so the live path
 * is the content one.
 */
function secondBillAdvice(draftId: string | undefined): string {
  return draftId
    ? `This refusal is keyed on the DRAFT, so retyping the invoice number does not ` +
        `make it a different bill: if this is genuinely a second bill, upload it as ` +
        `its own document so it gets its own draft.`
    : `This refusal is keyed on the draft's own CONTENT — party, supplier invoice ` +
        `number, date, narration and every leg — so if this is genuinely a second ` +
        `bill, key its own supplier invoice number.`;
}

/**
 * The once-only key for a draft.
 *
 * `draftId` wins whenever the caller has it. The fallback is the draft body's
 * CONTENT, and its one failure mode is worth stating plainly: two genuinely
 * different supplier bills that agree on party, supplier invoice number, date,
 * narration and every posting would share a key, and the second would be refused.
 * For a purchase that is not a coincidence — a supplier does not issue two bills
 * under one invoice number — and the refusal names the number so the operator can
 * key the real one. The opposite mistake (posting the payable twice) is the one
 * that costs money.
 */
function draftAcceptKey(
  companyID: string,
  body: VoucherDraftBody,
  draftId?: string,
): string {
  if (draftId) return `${companyID}::draft::${draftId}`;
  const legs = body.postings
    .map(
      (p) =>
        `${p.ledger_id ?? `new:${p.ledger_name}`}|${p.dr_cr}|${p.amount}`,
    )
    .join(";");
  const lines = (body.gst?.lines ?? [])
    .map((l) => `${l.taxable_value}|${l.gst_rate_pct}|${l.hsn ?? ""}`)
    .join(";");
  return [
    companyID,
    "body",
    body.party_ledger_id ?? "",
    body.party ?? "",
    body.voucher_no ?? "",
    body.voucher_date ?? "",
    body.narration ?? "",
    legs,
    lines,
  ].join("::");
}

/**
 * Run `post` at most once per (company, draft) for the life of this page.
 *
 * ⚠️ A CALL THAT ARRIVES WHILE THE FIRST IS STILL IN FLIGHT GETS THE SAME PROMISE
 * rather than a refusal: that is a double-press of Ctrl+A, one intent, and the
 * right answer is one voucher and one "Saved" — not an error beside a success.
 * A call that arrives AFTER the first succeeded is a second accept and is refused.
 *
 * ⚠️ A FAILED ACCEPT IS FORGOTTEN. The operator must be able to fix a 422 and try
 * again, and a memo that remembered the failure would lock them out of their own
 * bill. Same policy as gstVoucher.ts's LEDGER_CACHE.
 *
 * The memo is registered SYNCHRONOUSLY, before `post` awaits anything, so two
 * calls in the same tick cannot both get past it.
 */
function guardAcceptOnce(
  companyID: string,
  pristine: VoucherDraftBody,
  opts: AcceptDraftOptions,
  post: () => Promise<VoucherResponse>,
): Promise<VoucherResponse> {
  const key = draftAcceptKey(companyID, pristine, opts.draftId);
  const seen = ACCEPTED_DRAFTS.get(key);
  if (seen !== undefined) {
    if (seen.done === null) return seen.promise;
    return Promise.reject(
      new DraftAlreadyAcceptedError(
        `This draft has already been accepted in this session as voucher ` +
          `${seen.done.voucher_no || seen.done.voucher_id}. It has NOT been posted again — ` +
          `accepting the same supplier bill twice duplicates both the payable and the input tax. ` +
          `${secondBillAdvice(opts.draftId)}`,
        seen.done.voucher_no,
        seen.done.voucher_id,
      ),
    );
  }
  // ⛔ LAYER 3, AND IT IS READ HERE RATHER THAN IN postDraftBody FOR A REASON: this
  // is the ONE funnel both accepts (pristine and edited) pass through with the
  // PRISTINE body in hand, and localStorage is synchronous, so the refusal costs no
  // round trip and happens before any I/O at all. The session memo above is empty
  // after a reload; this is the same key, written down. A BROWSER RE-DRIVE beat the
  // session memo by pressing F5, which is why this exists.
  const remembered = durableAcceptLookup(key);
  if (remembered !== null) {
    return Promise.reject(
      new DraftAlreadyAcceptedError(
        `This draft was already accepted on this machine as voucher ` +
          `${remembered.voucherNo || remembered.voucherID}` +
          `${remembered.at ? ` (${remembered.at})` : ""}. It has NOT been posted again — ` +
          `accepting the same supplier bill twice duplicates both the payable and the input tax. ` +
          `${secondBillAdvice(opts.draftId)}`,
        remembered.voucherNo,
        remembered.voucherID,
      ),
    );
  }
  const rec: AcceptRecord = {
    // Filled in below, once `post()` has been started. Nothing can observe the
    // gap: the only reader is the `seen` branch above, and reaching it requires
    // another call, which cannot happen before this synchronous block finishes.
    promise: undefined as unknown as Promise<VoucherResponse>,
    done: null,
  };
  ACCEPTED_DRAFTS.set(key, rec);
  // ⛔ `post` MAY THROW SYNCHRONOUSLY — applyDraftEdits does, on a draft whose
  // shape is not rebuildable — and letting that escape would break two things at
  // once. The caller (VoucherScreen's onAccept) chains .then/.catch/.finally on the
  // returned promise, so a synchronous throw would skip the `finally` that clears
  // its in-flight latch and Ctrl+A would be dead for the rest of the session; and
  // the memo entry set above would be left with no promise on it, so the NEXT
  // accept would return `undefined` and blow up on `.then`. Both are converted
  // here: the memo is dropped and the throw becomes a rejection, which is what
  // this function returned when it was simply `async`.
  let started: Promise<VoucherResponse>;
  try {
    started = post();
  } catch (err) {
    ACCEPTED_DRAFTS.delete(key);
    return Promise.reject(err);
  }
  rec.promise = started.then(
    (res) => {
      rec.done = res;
      return res;
    },
    (err: unknown) => {
      ACCEPTED_DRAFTS.delete(key);
      throw err;
    },
  );
  return rec.promise;
}

// ---------------------------------------------------------------------------
// LAYER 4 — insightd's TERMINAL DRAFT STATE (the root fix)
//
// ⛔ THIS IS THE LAYER THE OTHER THREE WERE STANDING IN FOR. A draft that has
// become a cored voucher is moved SERVER-SIDE to `status: "accepted"` carrying the
// voucher id (`POST /companies/{c}/drafts/{id}/accepted`, added in
// insightd/insightd/api/routes_drafts.py). Because the record is on the server:
//   - a page reload cannot defeat it — that is what beat layers 1 and 3;
//   - a second browser tab / another machine cannot defeat it either;
//   - the Bill Inbox can finally be HONEST. After a successful accept the row used
//     to still read "Pune Fabricators · 21240.0000 · ready · 95%", which is an
//     invitation to post the same bill again; now it reads accepted and names the
//     voucher, because an operator who cannot see what happened re-does it.
//
// ⚠️ HOW DURABLE, EXACTLY: insightd's default store is IN-MEMORY
// (insightd/api/store.py) and only the PG-backed store (INSIGHTD_DATABASE_URL,
// insightd/api/pg_store.py) survives an insightd restart. So the guarantee is
// "survives a page reload, another tab and another machine" always, and "survives
// an insightd restart" only in the durable deployment. Layer 3 is deliberately kept
// underneath it for exactly that reason.
//
// ⛔ THE PORT IS AN INTERFACE, NOT A DIRECT CALL, for two reasons that are not
// tidiness: unit tests must be able to drive this layer without a network, and the
// default implementation has to reach insightd — a DIFFERENT service from the
// `CoredClient` every other function here takes — so the dependency has to enter
// this module somewhere explicit.
// ---------------------------------------------------------------------------

/** What insightd currently records about the draft an accept is posting. */
export interface DraftLifecycleRecord {
  /** insightd's own `draft_id` — the address the acceptance is recorded against. */
  draftId: string;
  /** `"ready"` | `"low_confidence_manual"` | `"accepted"` | `"processing"`. */
  status: string;
  /** The cored voucher this draft already became. "" unless status is "accepted". */
  acceptedVoucherID: string;
  acceptedVoucherNo: string;
}

/**
 * The two things this module needs from insightd's draft lifecycle. Injectable so
 * tests can drive layer 4 with no network, and so the transport lives in ONE place.
 */
export interface DraftLifecyclePort {
  /**
   * The insightd draft this body came from, or null when insightd knows of none.
   *
   * `draftId` is used directly when the caller has it. Without it the draft is
   * found by EXACT body match (see {@link insightdDraftLifecycle}) — not by a
   * fuzzy resemblance, and not by party/amount alone.
   */
  find(
    companyID: string,
    body: VoucherDraftBody,
    draftId?: string,
  ): Promise<DraftLifecycleRecord | null>;
  /**
   * STEP 1 — RESERVE this draft for THIS poster, before anything is written.
   *
   * ⛔ RESOLVING MEANS "YOU MAY POST". Rejecting with an `ApiError` 409 means
   * somebody else is posting it and THIS CALLER MUST POST NOTHING AT ALL — see
   * {@link claimDraftForPost}, which is the only caller and translates the three
   * documented 409 codes into refusals the operator can act on.
   *
   * Idempotent for the same `token`: a retry from the page that already holds the
   * claim renews the lease rather than being locked out by itself.
   */
  claim(
    companyID: string,
    draftId: string,
    token: string,
    claimedBy: string,
  ): Promise<void>;
  /**
   * Give the claim back because the voucher POST did NOT happen.
   *
   * ⛔ THIS IS THE CONTROL THAT STOPS THE CLAIM BECOMING A WORSE BUG THAN THE
   * DUPLICATE. A draft left claimed after a failed post is a bill the operator can
   * neither post nor release; the TTL is only the backstop for a browser that died.
   */
  releaseClaim(companyID: string, draftId: string, token: string): Promise<void>;
  /** STEP 3 — record which cored voucher a draft became (also clears the claim). */
  recordAccepted(
    companyID: string,
    draftId: string,
    voucher: { id: string; no: string },
  ): Promise<void>;
}

let LIFECYCLE_PORT: DraftLifecyclePort | null = null;

/**
 * Override the lifecycle port (tests). Pass null to restore the insightd-backed
 * default. Mirrors BillInboxScreen's `_setInsightdClientFactory` idiom.
 */
export function _setDraftLifecyclePort(port: DraftLifecyclePort | null): void {
  LIFECYCLE_PORT = port;
}

/**
 * A port that never has an opinion and records nothing — layer 4 switched off.
 *
 * ⚠️ Its `claim` RESOLVES, which means "no opinion", not "you hold a lease". That is
 * the honest behaviour for a port that is not talking to insightd at all: refusing
 * would block every accept, and pretending to reserve would be a lie. It is why the
 * claim is a guard against a CONCURRENT poster only when insightd is reachable, and
 * why the four recorded layers stay underneath it.
 */
export const INERT_DRAFT_LIFECYCLE: DraftLifecyclePort = {
  find: () => Promise.resolve(null),
  claim: () => Promise.resolve(),
  releaseClaim: () => Promise.resolve(),
  recordAccepted: () => Promise.resolve(),
};

/** The active port, building the insightd-backed default on first use. */
function draftLifecyclePort(): DraftLifecyclePort {
  if (LIFECYCLE_PORT === null) LIFECYCLE_PORT = insightdDraftLifecycle();
  return LIFECYCLE_PORT;
}

/** A deep, key-SORTED JSON rendering, so two bodies compare by VALUE not by key order.
 *
 * `null` and absent are treated as the same thing: pydantic emits every optional
 * field explicitly (`"voucher_no": null`) while a hand-built or narrowed TS body may
 * simply omit it, and those two are the same draft. Money stays a string — nothing
 * here parses a number, so no fixed-4 value can be reformatted by the comparison.
 */
function canonicalJson(value: unknown): string {
  if (value === null || value === undefined) return "null";
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(",")}]`;
  if (typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([, v]) => v !== null && v !== undefined)
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
      .map(([k, v]) => `${JSON.stringify(k)}:${canonicalJson(v)}`);
    return `{${entries.join(",")}}`;
  }
  return JSON.stringify(value);
}

/**
 * The default port: insightd's EXISTING GET routes plus the ONE new POST.
 *
 * ⛔ FINDING THE DRAFT WITHOUT A `draftId` IS AN EXACT MATCH, NOT A GUESS. The F9
 * screen currently calls {@link acceptPurchaseDraft} with the draft BODY and no id
 * (VoucherScreen.tsx passes `loadedDraft.draft.body`; wiring the id through is a
 * one-line change in a file this round could not touch), so without a fallback this
 * whole layer would be dead code in the app — which is precisely the "the fix was
 * never actually reachable" failure this round exists to avoid. The fallback lists
 * the company's drafts, narrows to the ones whose party matches, and then compares
 * the FULL body of each candidate with {@link canonicalJson}. The narrowing is only
 * an optimisation; the decision is total-body equality. Both bodies come from the
 * same insightd JSON, so equality is exact rather than approximate.
 *
 * An ACCEPTED match wins over a ready one when several bodies are identical (the
 * same bill uploaded twice): the accepted one is the answer that stops a duplicate.
 */
export function insightdDraftLifecycle(deps: {
  /** Injected insightd reader (tests). Defaults to the env-configured client. */
  insightd?: Pick<InsightdClient, "getDraft" | "listDrafts">;
  /** Injected transport for the ONE write (tests). Defaults to insightd's base URL. */
  http?: HttpClient;
} = {}): DraftLifecyclePort {
  const insightd = deps.insightd ?? createInsightdClient();
  // ✅ THE DUPLICATED BASE-URL RESOLVER IS GONE (register row D49). It existed
  // because both readings of VITE_INSIGHTD_BASE_URL had to stay identical or the
  // Bill Inbox and the accept path would talk to two different insightds; there is
  // now no variable to keep in step, because neither of them addresses insightd at
  // all. Both build a cored client with no arguments: same-origin base, and the
  // session transport the auth gate installed, so the bearer arrives on its own.
  const http = deps.http ?? createHttpClient();

  const toRecord = (d: {
    draft_id: string;
    status: string;
    accepted_voucher_id?: string | null;
    accepted_voucher_no?: string | null;
  }): DraftLifecycleRecord => ({
    draftId: d.draft_id,
    status: d.status,
    acceptedVoucherID: d.accepted_voucher_id ?? "",
    acceptedVoucherNo: d.accepted_voucher_no ?? "",
  });

  return {
    async find(companyID, body, draftId) {
      if (draftId) {
        try {
          return toRecord(await insightd.getDraft(companyID, draftId));
        } catch (err) {
          // 404 is a DEFINITE answer ("insightd has no such draft"), not a failed
          // read: fall through to no-opinion. Anything else is a real transport
          // failure and is re-thrown for the caller to classify.
          if (err instanceof ApiError && err.status === 404) return null;
          throw err;
        }
      }
      const party = (body.party ?? "").trim().toLowerCase();
      const wanted = canonicalJson(body);
      const candidates = (await insightd.listDrafts(companyID)).filter(
        (s) =>
          // "processing" carries an EMPTY body by construction, so it can never
          // match; skipping it saves a fetch and can never skip the answer.
          s.status !== "processing" && s.party.trim().toLowerCase() === party,
      );
      let match: DraftLifecycleRecord | null = null;
      for (const c of candidates) {
        const full = await insightd.getDraft(companyID, c.draft_id);
        if (canonicalJson(full.body) !== wanted) continue;
        const rec = toRecord(full);
        if (rec.status === "accepted") return rec; // the answer that matters
        match ??= rec;
      }
      return match;
    },

    async claim(companyID, draftId, token, claimedBy) {
      await http.post(`${draftPath(companyID, draftId)}/claim`, {
        claim_token: token,
        claimed_by: claimedBy,
      });
    },

    async releaseClaim(companyID, draftId, token) {
      await http.post(`${draftPath(companyID, draftId)}/claim/release`, {
        claim_token: token,
      });
    },

    async recordAccepted(companyID, draftId, voucher) {
      await http.post(`${draftPath(companyID, draftId)}/accepted`, {
        voucher_id: voucher.id,
        voucher_no: voucher.no,
      });
    },
  };
}

/** The one place the draft URL is built, so claim / release / accepted cannot drift.
 *
 * ⛔ UNDER CORED (D49). `/v1/companies/{id}/drafts/{draftId}` — cored authenticates
 * the caller, checks membership, evaluates (Create, "purchase") and forwards to
 * insightd with the company its PDP authorized. ⚠️ NOT `/companies/...`: vite used
 * to route that whole prefix to insightd, so a cored path written that way would
 * be delivered to the wrong process while every unit test passed.
 */
function draftPath(companyID: string, draftId: string): string {
  return `/v1/companies/${encodeURIComponent(companyID)}/drafts/${encodeURIComponent(draftId)}`;
}

/**
 * Ask insightd whether this draft has ALREADY been accepted, and refuse if it has.
 *
 * Returns the draft's lifecycle record (so the caller can record the acceptance
 * against the same id afterwards) or null when insightd has no opinion.
 *
 * ⚠️ A FAILED READ DOES **NOT** REFUSE HERE, AND THAT IS A DELIBERATE DEPARTURE
 * from {@link assertNotAlreadyOnTheBooks} one screen down. The difference is that
 * this layer is not alone: the session memo, the durable memo and the day book have
 * all already answered by the time it runs. Refusing on an insightd outage would
 * mean an insightd hiccup blocks every accept — including a FIRST accept of a bill
 * that is not a duplicate at all — and losing the human's only write path is a
 * worse failure than losing the fourth of four duplicate checks. The failure is
 * reported rather than swallowed silently.
 */
async function draftAlreadyAcceptedInsightd(
  companyID: string,
  identity: VoucherDraftBody,
  opts: AcceptDraftOptions,
): Promise<DraftLifecycleRecord | null> {
  let rec: DraftLifecycleRecord | null;
  try {
    rec = await draftLifecyclePort().find(companyID, identity, opts.draftId);
  } catch (err) {
    console.warn(
      "draftAccept: insightd could not be asked whether this draft was already " +
        "accepted, so the once-only guard is running on its client-side layers " +
        "only (session memo, durable memo, day book):",
      err,
    );
    return null;
  }
  if (rec !== null && rec.status === "accepted") {
    throw new DraftAlreadyAcceptedError(
      `This draft has already been accepted: insightd records it as voucher ` +
        `${rec.acceptedVoucherNo || rec.acceptedVoucherID}. It has NOT been posted again — ` +
        `accepting the same supplier bill twice duplicates both the payable and the input tax. ` +
        `If this is genuinely a second bill, upload it as its own document.`,
      rec.acceptedVoucherNo,
      rec.acceptedVoucherID,
    );
  }
  return rec;
}

// ---------------------------------------------------------------------------
// LAYER 5 — THE POSTING CLAIM. RESERVE BEFORE POSTING, NOT CHECK BEFORE POSTING.
//
// ⛔⛔ WHY A FIFTH LAYER, WHEN THE FOUR ABOVE ARE ALL CORRECT AND ALL HELD. Because
// every one of them answers the SAME QUESTION AT THE SAME WRONG MOMENT: "has this
// draft been accepted yet?", asked before this attempt has posted. Two SEPARATE
// browsers (separate localStorage, no shared session memo) were raced on ONE
// numberless draft — nothing for the day book to match on — and BOTH POSTED:
//
//   BROWSER A: Saved / No: 11  POST …/vouchers/purchase 201  POST …/accepted 200
//   BROWSER B: Saved / No: 12  POST …/vouchers/purchase 201  POST …/accepted 409
//
// 27000.0000 + 4860.0000 IGST, twice. Layer 4's compare-and-set did exactly what it
// was built to do — it refused the second MARKING — but it necessarily runs AFTER the
// voucher exists. Both browsers were TRUTHFULLY told "not accepted", because neither
// had posted yet. A FIFTH CHECK AT THAT SAME MOMENT WOULD LOSE THE SAME RACE; the
// ordering is the defect, not the number of checks.
//
// So the poster now RESERVES the draft first and the loser posts NOTHING:
//   1. CLAIM   — atomic, server-side (SELECT … FOR UPDATE); loser gets 409
//   2. POST    — the voucher, in cored, by the human's client
//   3. CONFIRM — layer 4's terminal `accepted` state, unchanged
//
// ⛔ AND THE CLAIM IS TAKEN BEFORE `resolveDraftLedgers`, NOT AFTER. That function
// creates ledger MASTERS, and cored has no DELETE for a ledger, so a loser that
// claimed late would leave orphan masters behind for a bill it never posted. An
// earlier round fixed exactly this class of ordering bug for the day-book check; this
// is the same rule applied to the claim.
// ---------------------------------------------------------------------------

/**
 * This PAGE's claim token. Opaque, and deliberately per-page rather than per-profile
 * or per-company: TWO TABS OF ONE BROWSER ARE TWO RACERS, and a token shared between
 * them would let each renew the other's claim and both post — reintroducing the
 * defect inside a single browser. A reload mints a new one, which is correct: the page
 * that held the old claim is gone, and its lease lapses on the server.
 */
const PAGE_CLAIM_TOKEN = ((): string => {
  const c = (globalThis as { crypto?: { randomUUID?: () => string } }).crypto;
  if (typeof c?.randomUUID === "function") return `f9-${c.randomUUID()}`;
  // jsdom/older browsers: uniqueness only has to hold across concurrent POSTERS, and
  // a losing collision is a false "already being posted" that lapses, never a
  // duplicate voucher. Time + entropy is sufficient for that.
  return `f9-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
})();

/** Display-only label, so a refused operator is told WHAT holds the claim. */
const CLAIM_LABEL = "the F9 bill-accept screen";

/** Test-only: the token this page claims under (asserted by the concurrency tests). */
export function _pageClaimToken(): string {
  return PAGE_CLAIM_TOKEN;
}

/** A claim this attempt is holding, and therefore owes a release or a confirm. */
interface HeldClaim {
  draftId: string;
  token: string;
}

/** The `detail.code` insightd returned on a 409, or "" when there is none. */
function conflictCode(err: unknown): string {
  if (!(err instanceof ApiError) || err.status !== 409) return "";
  const detail = (err.body as { detail?: unknown } | null | undefined)?.detail;
  const code = (detail as { code?: unknown } | null | undefined)?.code;
  return typeof code === "string" ? code : "";
}

/** A string field off a 409 detail body, or "" — never throws on a shape surprise. */
function conflictField(err: unknown, key: string): string {
  if (!(err instanceof ApiError)) return "";
  const detail = (err.body as { detail?: unknown } | null | undefined)?.detail;
  const v = (detail as Record<string, unknown> | null | undefined)?.[key];
  return typeof v === "string" ? v : "";
}

/**
 * STEP 1: reserve the draft, or refuse the accept WITHOUT WRITING ANYTHING.
 *
 * Returns the claim to release later, or `null` when there was no claim to take
 * (insightd does not know this draft, or could not be reached — see below).
 *
 * ⛔ A 409 IS A DEFINITE ANSWER AND ALWAYS REFUSES. It is the entire point of the
 * layer: the caller must not post. The three codes are translated rather than passed
 * through raw because each one means something different to the operator —
 * `draft_being_posted` is transient, `draft_already_accepted` is terminal and names
 * the voucher, `draft_still_processing` means there is nothing to post at all.
 *
 * ⚠️ A TRANSPORT FAILURE DOES NOT REFUSE, and that is the same deliberate trade
 * {@link draftAlreadyAcceptedInsightd} makes one screen up. insightd is a SEPARATE
 * service from cored, and cored is where the human's money write goes; letting an
 * insightd hiccup block every accept would take away the operator's only write path,
 * including the first accept of a bill that is not a duplicate at all. The four
 * recorded layers still stand. ⛔ The residual is real and is stated on
 * {@link postDraftBody}: with insightd unreachable, two browsers can still race.
 */
async function claimDraftForPost(
  companyID: string,
  draftId: string,
): Promise<HeldClaim | null> {
  try {
    await draftLifecyclePort().claim(
      companyID,
      draftId,
      PAGE_CLAIM_TOKEN,
      CLAIM_LABEL,
    );
    return { draftId, token: PAGE_CLAIM_TOKEN };
  } catch (err) {
    const code = conflictCode(err);
    if (code === "draft_being_posted") {
      const expires = conflictField(err, "claim_expires_at");
      const by = conflictField(err, "claimed_by");
      throw new DraftBeingPostedError(
        `This bill is already being posted${by ? ` by ${by}` : ""}, in another tab or on ` +
          `another machine. NOTHING has been posted from this attempt — no voucher and no ` +
          `ledger masters — because the reservation is taken before any write. If that other ` +
          `attempt fails or is abandoned, the reservation lapses` +
          `${expires ? ` at ${expires}` : " shortly"} and this bill can be accepted again. ` +
          `Do NOT post it manually in the meantime: that is how one supplier bill becomes two ` +
          `vouchers.`,
        expires,
      );
    }
    if (code === "draft_already_accepted") {
      // A TIGHTER WINDOW THAN LAYER 4's READ, and worth having: the other browser can
      // finish between our `find` and this call, and only this answer sees that.
      throw new DraftAlreadyAcceptedError(
        `This draft has already been accepted: insightd records it as voucher ` +
          `${conflictField(err, "accepted_voucher_no") || conflictField(err, "accepted_voucher_id")}. ` +
          `It has NOT been posted again — accepting the same supplier bill twice duplicates both ` +
          `the payable and the input tax.`,
        conflictField(err, "accepted_voucher_no"),
        conflictField(err, "accepted_voucher_id"),
      );
    }
    if (code === "draft_still_processing") {
      throw new DraftNotPostableError(
        `insightd is still processing this draft, so it carries no postings and there is ` +
          `nothing to accept yet. Nothing has been posted.`,
      );
    }
    if (err instanceof ApiError && err.status === 409) {
      // A 409 whose code this client does not recognise is STILL a refusal. Treating
      // an unknown conflict as "go ahead and post" would be exactly the check-then-act
      // reasoning this layer exists to remove.
      throw new DraftBeingPostedError(
        `insightd refused to reserve this bill for posting (${err.message}). Nothing has been ` +
          `posted. Re-open the bill from the inbox and try again.`,
        conflictField(err, "claim_expires_at"),
      );
    }
    console.warn(
      "draftAccept: insightd could not be asked to RESERVE this draft, so the accept is " +
        "proceeding on its four recorded layers only (session memo, day book, durable memo, " +
        "terminal draft state). Two browsers racing this exact bill could both post:",
      err,
    );
    return null;
  }
}

/**
 * Give a claim back after a failure, WITHOUT ever replacing the failure that caused it.
 *
 * ⛔ THIS IS THE CONTROL THAT KEEPS THE CLAIM FROM BEING A WORSE DEFECT THAN THE
 * DUPLICATE IT PREVENTS. Every path between the claim and a successful POST ends here:
 * a refused voucher (a back-dated cutoff, an unbalanced entry), a failed chart read, a
 * ledger create that 500s, a bill-allocation sum that will not certify. Without it the
 * operator who just fixed the problem would be told THEIR OWN BILL is "already being
 * posted" until the lease lapsed.
 *
 * ⚠️ IT NEVER THROWS. The caller is already unwinding with the real error, and that
 * error is the sentence the operator needs; a failed release would replace it with a
 * confusing one and still not release the claim. A release that could not be delivered
 * degrades to the TTL, which is precisely what the TTL is for.
 */
async function releaseClaimAfterFailure(
  companyID: string,
  claim: HeldClaim | null,
): Promise<void> {
  if (claim === null) return;
  try {
    await draftLifecyclePort().releaseClaim(
      companyID,
      claim.draftId,
      claim.token,
    );
  } catch (err) {
    console.warn(
      `draftAccept: this accept failed and insightd could not be told to RELEASE its ` +
        `reservation on draft ${claim.draftId}. No voucher was posted. The reservation ` +
        `lapses on its own (insightd's CLAIM_TTL_SECONDS), after which the bill can be ` +
        `accepted again; a retry from THIS page is not blocked, because it re-claims under ` +
        `the same token:`,
      err,
    );
  }
}

/**
 * Ask THE BOOKS whether this supplier bill is already posted, and refuse if it is.
 *
 * ⛔ THIS IS THE ONLY LAYER OF THE ONCE-ONLY GUARD THAT SURVIVES A PAGE RELOAD ON
 * A MACHINE THAT HAS NEVER SEEN THE DRAFT, which is close to how the defect was
 * driven: accept, go back to the Bill Inbox, re-open the same still-"ready" draft,
 * accept again.
 *
 * The match is deliberately tight — same class, same DATE, same supplier invoice
 * number, same party — because a purchase `voucher_no` is the SUPPLIER's reference
 * and two different suppliers may both number a bill "001". A tombstoned voucher
 * does not match: the real Day Book already filters `vh.is_deleted = false`, and
 * the `op` check makes that explicit so this agrees with the mock too. The date is
 * re-compared in JS rather than trusted to the query, because the mock's
 * `getDayBook` ignores from/to and returns every voucher.
 *
 * ⛔⛔ AND WHAT IT MATCHES ON IS THE POST-REDACTION NUMBER, WHICH ONCE MADE IT
 * REFUSE REAL BILLS (D85, driven). The day book stores only `voucher_no`, and for a
 * bill whose invoice number insightd had to redact that string is a TAG, not the
 * supplier's number. While the tag was a constant, two DIFFERENT PAN-shaped numbers
 * from one supplier on one date were the same bill here and the second accept was
 * refused. The tag now ends in an unsalted fingerprint of the removed value
 * (`insightd/insightd/redaction/persistence.py`), so this check is exact again for
 * redacted numbers too — and the refusal below says which case it is in, because
 * the old sentence advised a remedy that could not work. ⛔ The fingerprint must
 * stay UNSALTED: a per-request salt would make a re-upload of the SAME bill miss
 * here, which is this layer's whole job.
 *
 * ⛔ IT CANNOT SEE A BILL WITH NO NUMBER OR NO DATE, AND THAT GAP IS WHAT A BROWSER
 * RE-DRIVE WALKED THROUGH. A draft whose supplier invoice number the OCR never read
 * has nothing to match on, so this returns without an opinion — and once a reload
 * had emptied the session memo, nothing was left. Two real vouchers came out of one
 * bill. Layers 3 and 4 exist because of that; this function is unchanged in what it
 * matches on, because tightening it would only trade a missed duplicate for a
 * refused first accept.
 *
 * ⚠️ A FAILED READ REFUSES THE ACCEPT rather than assuming "not a duplicate", and
 * it does so BEFORE any ledger is created, so nothing is left behind. Precedent:
 * gstVoucher.ts's ledgerIndex refuses to swallow a failed chart read into an empty
 * index for the same reason — a read that did not happen is not evidence.
 */
async function assertNotAlreadyOnTheBooks(
  client: CoredClient,
  companyID: string,
  body: VoucherDraftBody,
): Promise<void> {
  const no = (body.voucher_no ?? "").trim();
  const date = (body.voucher_date ?? "").trim();
  if (no === "" || date === "") return;
  const party = (body.party ?? "").trim().toLowerCase();
  const day = await client.getDayBook(companyID, { from: date, to: date });
  const isThisBill = (e: DayBookEntry): boolean =>
    e.op.trim().toUpperCase() !== "DELETE" &&
    e.class.trim().toLowerCase() === "purchase" &&
    e.date === date &&
    e.voucher_no.trim() === no &&
    e.party.trim().toLowerCase() === party;
  const hit = day.entries.find(isThisBill);
  if (hit === undefined) return;
  // ⛔ D85 — THE ADVICE DEPENDS ON WHAT THE NUMBER IS. For an ordinary number,
  // "key its own number" is the remedy. For a number insightd had to REDACT it
  // used to be a dead end: every PII-shaped number collapsed to one token, so the
  // operator keyed a different real number and was refused again. The token now
  // carries a fingerprint of the value that was removed, so reaching this branch
  // means the SAME supplier number — and saying so is the difference between a
  // refusal that helps and one that is fully present and fully wrong.
  const redacted = REDACTION_TAG_RE.test(hit.voucher_no);
  throw new DraftAlreadyAcceptedError(
    `This supplier bill is already on the books: purchase ${hit.voucher_no} dated ${hit.date}` +
      `${hit.party ? ` for ${hit.party}` : ""}. It has NOT been posted again — accepting the same ` +
      `bill twice duplicates both the payable and the input tax. ` +
      (redacted
        ? `That number was removed as PII before it could reach the books, and the tag ends in a ` +
          `fingerprint of the removed number itself — so this is the SAME supplier number, not ` +
          `merely another redacted one. A genuinely different bill carries a different number, ` +
          `hence a different fingerprint, and would not have been stopped here; retype the real ` +
          `number in the invoice-number box if this bill is one the books do not have.`
        : `If this is genuinely a second bill, key its own supplier invoice number.`),
    hit.voucher_no,
    hit.voucher_id,
  );
}

/**
 * The ids this accept MINTED, judged against the chart as it stood moments before.
 *
 * By ID and not by name, and only at the positions the resolver actually filled: a
 * draft may legitimately carry an id the chart read did not return, and blaming
 * this accept for it would put a stranger's ledger in the operator's error message.
 *
 * ⚠️ It is used ONLY to REPORT. A concurrent create in another tab could in
 * principle be misattributed here, which costs one slightly wrong sentence — and is
 * precisely why nothing is deleted on the strength of it. Deleting a master that
 * existed before this accept would be far worse than the orphan.
 */
function ledgersMintedByThisAccept(
  input: VoucherDraftBody,
  resolved: VoucherDraftBody,
  before: readonly LedgerListItem[],
): { id: string; name: string }[] {
  const known = new Set(before.map((l) => l.id));
  const minted = new Map<string, string>();
  if (
    input.party_ledger_id == null &&
    resolved.party_ledger_id != null &&
    !known.has(resolved.party_ledger_id)
  ) {
    minted.set(resolved.party_ledger_id, input.party ?? "Sundry Creditor");
  }
  input.postings.forEach((p, i) => {
    if (p.ledger_id != null) return;
    const id = resolved.postings[i]?.ledger_id;
    if (id != null && !known.has(id)) minted.set(id, p.ledger_name);
  });
  return [...minted].map(([id, name]) => ({ id, name }));
}

/** The (party, supplier invoice number, date) triple the day-book check matches on. */
function billIdentity(body: VoucherDraftBody): string {
  return [
    (body.party ?? "").trim().toLowerCase(),
    (body.voucher_no ?? "").trim(),
    (body.voucher_date ?? "").trim(),
  ].join("|");
}

/**
 * The shared write path behind both accepts, in the order defect 3 demands:
 * every check that can refuse WITHOUT writing runs first, and only then are
 * masters created.
 *
 *   1. pure pre-flight (no I/O, no writes)
 *   2. the duplicate check against the books — for the PRISTINE draft AND, when an
 *      edit moved it, for what is actually being posted (two reads)
 *   3. the duplicate check against insightd's terminal draft state (a read)
 *   4. ⛔ THE CLAIM — reserve the draft in insightd, atomically. A caller refused
 *      here posts NOTHING. This is the LAST step before the first write and the
 *      FIRST step that can stop a concurrent poster.
 *   5. the chart of accounts (a read) — needed for the party's bill-by-bill flag
 *      and to know afterwards what this accept minted
 *   6. ledger creation (the first write, and unavoidably before the POST)
 *   7. the voucher POST; on refusal, name the masters step 6 left behind
 *   8. ⛔ ON ANY FAILURE IN 5–7: RELEASE THE CLAIM. No voucher exists, so the
 *      reservation is owed back at once.
 *   9. on success ONLY: write the durable memo and tell insightd which voucher this
 *      draft became (which also clears the claim, server-side)
 *
 * ⛔ STEP 4 IS BEFORE STEP 6, NOT AFTER, AND THAT IS DELIBERATE. Step 6 creates ledger
 * MASTERS and cored has no DELETE for a ledger. A claim taken after it would mean the
 * loser of a race still left orphan masters behind for a bill it never posted — the
 * same ordering mistake an earlier round already had to fix for the day-book check.
 *
 * ⚠️ THE RESIDUAL WINDOW, STATED IN FULL, because this layer narrows the race and does
 * not close it absolutely:
 *   - insightd UNREACHABLE at step 4: the claim is skipped with a warning rather than
 *     refusing the accept (insightd is not where the money write goes, and blocking on
 *     it would cost the operator their only write path). Two browsers racing during an
 *     insightd outage on a NUMBERLESS bill from two profiles can therefore still both
 *     post — the same case layers 1–4 cannot see either.
 *   - NO DRAFT ID resolvable at step 4 (insightd holds no matching draft — a body
 *     built by hand, or a draft from another company): there is nothing to reserve, and
 *     the accept proceeds. Passing `opts.draftId` removes this case entirely.
 *   - A POST THAT OUTLIVES THE LEASE (insightd's CLAIM_TTL_SECONDS, 120 s): another
 *     poster may take the draft over mid-flight. The TTL is set far above any voucher
 *     POST for exactly this reason, and step 8 releases immediately instead of waiting.
 *   - AN AMBIGUOUS FAILURE — a timeout at step 7 where the voucher actually landed —
 *     releases the claim, so a retry is not blocked here. The day-book layer catches
 *     that for a numbered bill; for a numberless one the durable memo does not (the
 *     post failed, so nothing was remembered). This is unchanged by the claim: the
 *     alternative, holding the claim after an ambiguous failure, strands the bill.
 *
 * ⛔ `identity` IS THE PRISTINE DRAFT AND `body` IS WHAT GETS POSTED, AND CONFLATING
 * THEM WAS A LIVE BYPASS OF STEP 2 — a third door, found by re-reading this path
 * after a browser re-drive showed the first fix was not enough. On an EDITED accept
 * the caller hands `body` = the rebuilt body, so asking the books about `body` asks
 * about the EDITED date. Accept a draft pristine (posting it at 2026-07-12), reload,
 * re-open it, change the date to 2026-07-13 and accept again: the session memo is
 * cold, the day book is queried for the 13th, finds nothing, and the same numbered
 * bill is posted twice. So the books are asked about the DOCUMENT's identity first —
 * the pristine draft, exactly as the once-only memo keys on it — and then about the
 * edited body too, when the edit changed the triple. Both, because either alone
 * leaves a duplicate reachable.
 *
 * ⚠️ Step 4 is NOT memoized. gstVoucher.ts caches the chart for the life of the
 * page, which is right for resolving a name to an id; it is wrong here, because
 * "what existed before this accept" must be true of THIS accept or the orphan
 * report names the wrong ledgers.
 */
async function postDraftBody(
  client: CoredClient,
  companyID: string,
  body: VoucherDraftBody,
  opts: AcceptDraftOptions,
  identity: VoucherDraftBody = body,
): Promise<VoucherResponse> {
  assertPostableDraft(body);
  await assertNotAlreadyOnTheBooks(client, companyID, identity);
  if (billIdentity(body) !== billIdentity(identity)) {
    // An edit moved the bill's identity. Both are worth asking about: the pristine
    // one catches "already posted from this draft", this one catches "already
    // posted under the number/date being keyed now".
    await assertNotAlreadyOnTheBooks(client, companyID, body);
  }
  // LAYER 4 — the server's own record. Keyed on the PRISTINE draft for the same
  // reason as everything above: the document's identity is the draft.
  const lifecycle = await draftAlreadyAcceptedInsightd(companyID, identity, opts);

  // ⛔⛔ LAYER 5 — RESERVE THE DRAFT. THIS IS THE ONLY LINE IN THIS FUNCTION THAT CAN
  // STOP A CONCURRENT POSTER, AND ITS POSITION IS THE FIX. Everything above only READ;
  // everything below WRITES. A loser refused here has created nothing at all — not a
  // voucher, and (because this precedes `resolveDraftLedgers` by two lines that do no
  // writing) not an undeletable ledger master either.
  //
  // The claim needs insightd's own draft id: the caller's when it has one, else the id
  // layer 4's body-match just resolved. With NEITHER, insightd does not know this draft
  // and there is nothing to reserve — see the residual note below.
  const claimDraftId = opts.draftId ?? lifecycle?.draftId ?? null;
  const claim =
    claimDraftId === null
      ? null
      : await claimDraftForPost(companyID, claimDraftId);

  let res: VoucherResponse;
  // ⛔ EVERY FAILURE FROM HERE TO A POSTED VOUCHER MUST RELEASE THE CLAIM, WHICH IS WHY
  // THE try STARTS HERE AND NOT AT THE POST. The chart read, the ledger creates and the
  // bill-allocation build can all fail, and each of them would otherwise leave the
  // draft reserved by a page that is no longer posting it — a bill the operator can
  // neither post nor release until the lease lapses. That is worse than the duplicate.
  try {
    const chart = (await client.listLedgers(companyID)).ledgers;

    const resolved = await resolveDraftLedgers(client, companyID, body);
    const minted = ledgersMintedByThisAccept(body, resolved, chart);

    const refs = opts.billRefs ?? defaultDraftBillRefs(resolved, chart);
    const req = draftBodyToVoucherRequest(
      resolved,
      draftBillAllocations(resolved, refs),
    );
    try {
      res = await client.postPurchaseVoucher(companyID, req);
    } catch (err) {
      if (minted.length === 0) throw err;
      throw voucherAcceptFailure(err, minted);
    }
  } catch (err) {
    // No voucher was posted, so the reservation is owed back IMMEDIATELY rather than
    // being left to expire. `releaseClaimAfterFailure` never throws: the error below is
    // the one the operator has to read.
    await releaseClaimAfterFailure(companyID, claim);
    throw err;
  }
  // ⛔ THE VOUCHER EXISTS FROM HERE ON, AND THAT IS WHY THIS IS OUTSIDE THE try.
  // Everything below is bookkeeping ABOUT a write that already happened, and none of
  // it may turn a success into a failure: an operator told "Error" after the payable
  // was posted retries, and posts the bill twice — the exact defect, arriving
  // through the error path instead of the happy one. It is also why this must not be
  // inside the catch above, where a throw would be dressed up as a refused POST.
  recordAcceptance(companyID, identity, opts, lifecycle, res);
  return res;
}

/**
 * After a SUCCESSFUL post: write layer 3's durable memo and move insightd's draft to
 * its terminal `accepted` state (layer 4). Best-effort, and never throws.
 *
 * ⛔ ORDER MATTERS. The durable memo is written FIRST and synchronously, because it
 * is the layer that needs nobody's cooperation: if insightd is unreachable at this
 * instant, the reload-then-accept-again case is still refused. The insightd call is
 * deliberately NOT awaited by the accept — the operator's "Saved" must not wait on a
 * second service — but a failure is REPORTED rather than swallowed, because a draft
 * that stayed `ready` after a successful accept is exactly the state that showed
 * "Pune Fabricators · 21240.0000 · ready · 95%" in the inbox and invited the second
 * post. A 409 is NOT a failure here: it means insightd already had the record.
 */
function recordAcceptance(
  companyID: string,
  identity: VoucherDraftBody,
  opts: AcceptDraftOptions,
  lifecycle: DraftLifecycleRecord | null,
  res: VoucherResponse,
): void {
  try {
    rememberDurableAccept(draftAcceptKey(companyID, identity, opts.draftId), {
      voucherNo: res.voucher_no,
      voucherID: res.voucher_id,
      at: new Date().toISOString(),
    });
  } catch {
    // rememberDurableAccept already swallows storage failures; this is belt and
    // braces so a surprise here cannot reach the caller.
  }
  // Prefer the id the caller gave us; else the one the pre-flight resolved. With
  // neither, insightd does not know of this draft and there is nothing to mark.
  const draftId = opts.draftId ?? lifecycle?.draftId;
  if (!draftId) return;
  const report = (err: unknown) => {
    if (err instanceof ApiError && err.status === 409) return; // already recorded
    console.warn(
      `draftAccept: posted purchase ${res.voucher_no || res.voucher_id} but could not ` +
        `mark insightd draft ${draftId} accepted, so the Bill Inbox may still offer it ` +
        `as ready. The accept itself succeeded; the duplicate guard falls back to its ` +
        `client-side layers:`,
      err,
    );
  };
  // ⛔ THE try IS NOT DECORATION. A port implementation is free to be a plain
  // function that throws rather than one that rejects (and `draftLifecyclePort()`
  // itself constructs a client), and a synchronous throw here would escape past the
  // successful POST above and be reported to the operator as a failed accept — who
  // would then retry and post the bill twice. That is the defect arriving through
  // the error path, so it is caught in BOTH shapes.
  try {
    void draftLifecyclePort()
      .recordAccepted(companyID, draftId, {
        id: res.voucher_id,
        no: res.voucher_no,
      })
      .catch(report);
  } catch (err) {
    report(err);
  }
}
