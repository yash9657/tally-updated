import type { BillAllocationRequest, BillAllocationView, DrCr, RefType } from "../../api";
// The EXACT BigInt-scaled helpers, not money.ts's parseFloat-based ones. The
// sum-to-posting invariant is checked TO THE PAISA server-side with no tolerance,
// so a comparison done on two doubles can disagree with the server about whether
// a bill is fully allocated. Same reasoning as costAllocation.ts.
import { formatFixed4, parseFixed4 } from "./itemInvoice";

/**
 * BILL-WISE REFERENCES — the pure half. The picker UI is in
 * BillAllocationPicker.tsx.
 *
 * ⛔⛔ WHAT THIS MODULE EXISTS TO FIX: for the whole life of this product NO
 * SCREEN ON ANY VOUCHER PATH BUILT A BILL REFERENCE. `ledger.go` writes them from
 * `in.BillAllocations` — the CLIENT supplies them on every path — and the only
 * thing in the repository that ever constructed one was `seed-demo.mts`. So a
 * receivable keyed in a browser sat in the Balance Sheet's Sundry Debtors and was
 * ABSENT from the Outstandings report: money that never ages, never appears on a
 * statement, and that nobody can chase. Every gate stayed green for the entire
 * time, because the demo data was hand-written by the seed script.
 *
 * ⛔ THE REFERENCE NAME IS THE LOAD-BEARING FIELD, AND IT IS NOT THE TYPE.
 * `reporting.Outstandings` groups by **(ledger_id, ref_name)** and IGNORES
 * `ref_type` entirely — the type is documentary. A settlement therefore nets a
 * bill only if its name matches EXACTLY. An AGAINST carrying the wrong name does
 * not fail: it opens a second bill and leaves the first fully outstanding, with
 * both figures wrong and the report still footing.
 *
 * ⛔ AND A NEW REFERENCE IS DELIBERATELY LEFT UNNAMED HERE. The voucher number is
 * allocated by `allocateVoucherNo` INSIDE the post transaction, so at the moment
 * this code runs the name Tally would use does not exist yet. `post()` names any
 * reference that arrives unnamed — Tally's own `New Ref` default — and REFUSES an
 * unnamed AGAINST for the reason above. Do not "fix" the blank by inventing a
 * name here: a client-side guess (a timestamp, a uuid, the party name) would be a
 * reference no human can tie to a voucher, and it would be permanent.
 *
 * ⚠️ THE NAME THE SERVER GENERATES IS NOT THE BARE VOUCHER NUMBER, and no client
 * may assume it is. Every voucher type has its OWN number series per financial
 * year, so "7" names one voucher per type per year — a Receipt "7" once netted
 * against a Sales invoice "7" on the same party. `ledger.go`'s `stampBillRefNames`
 * therefore builds "<class>/<financial year>/<number>", with an index when one
 * voucher opens several bills on one ledger, and files ON_ACCOUNT money in a
 * single per-party "On Account" bucket instead of naming it after a voucher at
 * all. READ THE NAME BACK — from the GET, or from the open-bills list — and never
 * reconstruct it.
 */

/** One reference an operator has keyed (or the form has defaulted). */
export interface DraftBillRef {
  /**
   * NEW opens a bill · AGAINST settles a named one · ADVANCE is money before the
   * invoice · ON_ACCOUNT is money tied to no bill at all.
   */
  refType: RefType;
  /**
   * ⛔ "" IS LEGAL AND IS THE NORMAL CASE for NEW / ADVANCE / ON_ACCOUNT — the
   * server names it (see the header: NOT with the bare voucher number). It is
   * NEVER legal for AGAINST, which must match a name that already exists.
   */
  refName: string;
  /** fixed-4 on the wire; free-typed here. */
  amount: string;
  /** ISO date; "" when the bill carries no due date (then it never ages). */
  dueDate: string;
  /**
   * T8's three forex values, CARRIED THROUGH THE FORM RATHER THAN EDITED IN IT.
   *
   * ⛔⛔ THEY ARE HERE BECAUSE AN ALTER RE-STATES, AND THIS TYPE IS THE ONLY
   * THING STANDING BETWEEN A FETCHED FOREIGN BILL AND THE REQUEST THAT REPLACES
   * IT. `billRefsFromView` reads the view into this shape and
   * `buildBillAllocations` writes this shape back onto the wire; a field absent
   * HERE is a field the round trip drops, no matter how correct the GET and the
   * POST both are. That is the SEVENTH occurrence of the defect this file's own
   * header describes one paragraph up — and the first where what is lost is
   * three values on a surviving row rather than the row itself, so nothing
   * stops adding up and no report goes blank.
   *
   * ⚠️ NO FORM FIELD EDITS THEM YET, and that is deliberate rather than
   * unfinished. There is no foreign voucher ENTRY UI in this step: the build
   * order (design memo §5) puts operator exposure last, after settlement,
   * Outstandings and the unrealised report exist, on the principle that a
   * toggle with nothing behind it is worse than no toggle. What must be true
   * TODAY is that a foreign voucher which arrives by any other door — the
   * importer, a sync commit from an offline node, the HTTP API — is not
   * destroyed by an operator opening it and fixing a typo.
   *
   * ⚠️ `rate` IS A TEN-DECIMAL RATE, NOT MONEY. Never round-trip it through
   * `parseFixed4`/`formatFixed4` like `amount` above; see api/rate.ts.
   */
  currencyId?: string;
  /** Display only — the currency's code ("USD"). Never sent back. */
  currencyCode?: string;
  foreignAmount?: string;
  rate?: string;
  /**
   * TRUE while this reference is the form's own default and the operator has not
   * touched its amount.
   *
   * ⛔ IT EXISTS BECAUSE THE INVOICE TOTAL IS NOT KNOWN WHEN THE PARTY IS PICKED,
   * and that ordering is the normal one: an operator chooses the customer and
   * THEN keys the value. A default captured at party-pick time is 0.0000 forever,
   * so the form would refuse every invoice it had itself defaulted — which is
   * worse than not defaulting at all. An `auto` reference re-tracks the gross
   * until the operator states an amount, and stops the instant they do.
   *
   * ⚠️ DRAFT-ONLY. It is not on the wire and {@link buildBillAllocations} ignores
   * it; the server has no idea a reference was defaulted.
   */
  auto?: boolean;
}

/**
 * The posting the references attach to.
 *
 * ⛔ THE CALLER STATES THIS; IT IS NEVER INFERRED, and that is deliberate for the
 * same reason `buildCostAllocations` takes `primaryLegCount`. The bill-bearing
 * leg is in a DIFFERENT PLACE depending on the voucher:
 *
 *   - SALES / PURCHASE (GST accounting mode, and the item invoice) — it is the
 *     `party_ledger_id` leg: the Sundry Debtor or Creditor, for the invoice total
 *     INCLUDING tax.
 *   - PAYMENT / RECEIPT (single entry) — it is a **PARTICULAR**, not the party
 *     field. In single entry `postings[0]` is the BANK, and `party_ledger_id`
 *     names the bank too; the debtor being settled is one of the grid rows. A
 *     reference attached to the bank leg would be refused by the server (that
 *     ledger's postings would not total), which is the good outcome — but only
 *     because the caller is forced to say which leg it means.
 */
export interface BillBearingLeg {
  ledgerId: string;
  drCr: DrCr;
  /** The leg's FULL amount — what the references must total, exactly. */
  amount: string;
}

/** A blank reference of the given type, ready to key. */
export function emptyBillRef(refType: RefType = "NEW"): DraftBillRef {
  return { refType, refName: "", amount: "", dueDate: "" };
}

/**
 * The reference a SALE or PURCHASE defaults to: one NEW ref for the whole leg.
 *
 * ⚠️ Unlike `emptyAllocation`, this DOES pre-fill the amount, and the difference
 * is not an inconsistency. A cost split defaults to blank because splitting is
 * the whole point of the feature and the one-centre case is the rare one. A bill
 * reference is the opposite: one bill for the whole invoice is what virtually
 * every sale is, and making the operator retype the invoice total to get the
 * ordinary outcome is how a feature ends up switched off.
 */
export function defaultNewRef(leg: BillBearingLeg, dueDate = ""): DraftBillRef[] {
  return [
    {
      refType: "NEW",
      refName: "",
      amount: formatFixed4(parseFixed4(leg.amount)),
      dueDate,
      auto: true,
    },
  ];
}

/**
 * Keep an untouched default reference equal to the invoice total.
 *
 * Returns `refs` unchanged when there is nothing to re-track, so it is safe to
 * call on every render. Anything the operator has keyed — a second reference, an
 * edited amount, a removed row — takes the form out of `auto` and is never
 * overwritten.
 */
export function retrackAutoRef(
  refs: readonly DraftBillRef[],
  leg: BillBearingLeg,
): DraftBillRef[] | null {
  if (refs.length !== 1) return null;
  const only = refs[0]!;
  if (!only.auto) return null;
  const want = formatFixed4(parseFixed4(leg.amount));
  if (only.amount === want) return null;
  return [{ ...only, amount: want }];
}

/**
 * Build the wire array for one leg.
 *
 * Half-keyed references are DROPPED: a zero amount is not a reference, and an
 * AGAINST with no bill chosen is an operator who has not finished, not a
 * settlement of nothing. Both are reported by {@link billRefusal} instead, so
 * nothing is silently discarded on the way to the server.
 */
export function buildBillAllocations(
  leg: BillBearingLeg,
  refs: readonly DraftBillRef[],
): BillAllocationRequest[] {
  const out: BillAllocationRequest[] = [];
  for (const r of refs) {
    const amount = parseFixed4(r.amount);
    if (amount === 0n) continue;
    if (r.refType === "AGAINST" && r.refName.trim() === "") continue;
    const alloc: BillAllocationRequest = {
      ledger_id: leg.ledgerId,
      ref_name: r.refName.trim(),
      ref_type: r.refType,
      dr_cr: leg.drCr,
      amount: formatFixed4(amount),
    };
    if (r.dueDate) alloc.due_date = r.dueDate;
    // ⛔ ALL THREE OR NONE, and keyed off the CURRENCY rather than off each
    // field independently. The server refuses a two-of-three triple (migration
    // 0080's all-or-none CHECK), so emitting `foreign_amount` beside an absent
    // currency would 422 a request that looks complete on this side.
    if (r.currencyId) {
      alloc.currency_id = r.currencyId;
      alloc.foreign_amount = r.foreignAmount ?? "";
      alloc.rate = r.rate ?? "";
    }
    out.push(alloc);
  }
  return out;
}

/** What the references have accounted for so far, fixed-4. */
export function billAllocated(refs: readonly DraftBillRef[]): string {
  let sum = 0n;
  for (const r of refs) sum += parseFixed4(r.amount);
  return formatFixed4(sum);
}

/**
 * leg amount − allocated, fixed-4. Zero means the leg is fully referenced.
 *
 * This is the single most useful number on the form: on a receipt it is "how
 * much of this money is not yet applied to a bill", which is exactly the figure
 * that decides whether the remainder goes on account.
 */
export function billRemainder(leg: BillBearingLeg, refs: readonly DraftBillRef[]): string {
  return formatFixed4(parseFixed4(leg.amount) - parseFixed4(billAllocated(refs)));
}

/**
 * A REFUSAL for one leg, or "" when it is saveable.
 *
 * ⛔ A REFUSAL, NOT A WARNING — the same call `costAllocation.ts` makes and for
 * the same reason. The server enforces sum-to-posting exactly, so sending a
 * short reference costs the operator their whole form to a 422. Telling them
 * here is strictly kinder.
 *
 * ⚠️ NO REFERENCES AT ALL IS NOT AN ERROR. A party that does not maintain
 * bill-by-bill has none, and that is the normal case for most ledgers.
 */
export function billRefusal(leg: BillBearingLeg, refs: readonly DraftBillRef[]): string {
  if (refs.length === 0) return "";
  // An AGAINST with an amount but no bill chosen is dropped by the builder, so
  // the arithmetic below would read as complete while the reference cannot be
  // sent. Checked FIRST, for the same reason the cost module checks its
  // half-picked splits: the sum includes it.
  for (const r of refs) {
    if (r.refType === "AGAINST" && r.refName.trim() === "" && parseFixed4(r.amount) !== 0n) {
      return (
        `A settlement of ${formatFixed4(parseFixed4(r.amount))} does not say which bill it ` +
        `settles. It is counted in the total, so this leg looks complete while the ` +
        `reference cannot be sent — choose a bill, or change it to On Account.`
      );
    }
  }
  const remainder = parseFixed4(billRemainder(leg, refs));
  if (remainder === 0n) return "";
  const legAmount = formatFixed4(parseFixed4(leg.amount));
  return remainder < 0n
    ? `The bill references are over-allocated by ${formatFixed4(-remainder)} — they total ` +
        `${billAllocated(refs)} against a line of ${legAmount}. References must total the ` +
        `line EXACTLY, to the paisa; there is no tolerance.`
    : `The bill references are short by ${formatFixed4(remainder)} — they total ` +
        `${billAllocated(refs)} against a line of ${legAmount}. Unreferenced money would sit ` +
        `in Sundry Debtors and never reach the Outstandings report, so the server refuses ` +
        `it. Apply the remainder to a bill, or put it On Account.`;
}

/**
 * Re-hydrate one ledger's references from a fetched voucher.
 *
 * ⛔⛔ OMITTING THIS IS DATA LOSS, NOT A MISSING FEATURE, AND IT WAS LIVE.
 * `bill_allocation` is keyed on `voucher_version_id`, so AN ALTER RE-STATES:
 * whatever the body omits is gone. `voucherFullJSON` has always RETURNED these
 * rows — the §4.3 read-back audit checked exactly that and ticked them off — but
 * no client ever sent them back, so editing the narration of any voucher that
 * carried a bill reference DELETED it. The invoice stayed in Sundry Debtors on
 * the Balance Sheet and vanished from Outstandings, which is the same silent
 * shape as the order-fulfilment defect and reads to a business as a customer who
 * stopped owing them money.
 *
 * ⚠️ THE AUDIT WAS HALF AN AUDIT, and that is the lesson worth keeping: a GET
 * that emits the rows is NECESSARY and not SUFFICIENT. The round trip is only
 * closed when a form rebuilt from that response sends them back.
 *
 * ⚠️ Filtered BY LEDGER because a voucher may carry references for more than one
 * party, and each form panel owns exactly one leg.
 */
export function billRefsFromView(
  views: readonly BillAllocationView[],
  ledgerId: string,
): DraftBillRef[] {
  return views
    .filter((v) => v.ledger_id === ledgerId)
    .map((v) => ({
      // The stored type is a plain string on the view; anything the server did
      // not recognise could not have been stored, so this narrowing is safe.
      refType: v.ref_type as RefType,
      refName: v.ref_name,
      amount: v.amount,
      dueDate: v.due_date,
      // Carried verbatim. The view omits all three on a base bill, so these
      // stay undefined and `buildBillAllocations` emits nothing — which is the
      // only spelling the server accepts for "this bill is in rupees".
      currencyId: v.currency_id,
      currencyCode: v.currency,
      foreignAmount: v.foreign_amount,
      rate: v.rate,
    }));
}
