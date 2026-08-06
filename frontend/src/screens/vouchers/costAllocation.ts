import type { CostAllocationInput, CostAllocationView } from "../../api";
// The EXACT BigInt-scaled helpers, not money.ts's parseFloat-based ones. The
// sum-to-posting invariant is checked TO THE PAISA with no tolerance, on both the
// server and a deferred DB trigger, so a comparison done on two doubles can disagree
// with the server about whether a split is complete.
import { formatFixed4, parseFixed4 } from "./itemInvoice";

/**
 * SPLITTING ONE POSTING ACROSS COST CENTRES (T2, migration 0040) — the pure half.
 * The picker UI is in CostAllocationPicker.tsx.
 *
 * ⛔⛔ A COST CATEGORY IS AN AXIS AND A COST CENTRE IS A POSITION ON IT, and the
 * consequence is the one thing this module exists to get right.
 *
 * SEVERAL AXES ALLOCATE THE SAME MONEY IN PARALLEL. One ₹1,00,000 salary is
 * allocated in full across Departments (Sales 60,000 / Admin 40,000) **and again in
 * full** across Branches (Mumbai 70,000 / Delhi 30,000). Both allocations are
 * complete and both are correct.
 *
 * Therefore:
 *
 *  - **The sum-to-posting invariant is per (posting, CATEGORY), never per posting.**
 *    {@link axisRemainders} returns one remainder PER AXIS for exactly this reason.
 *  - **NOTHING may total across axes.** For two axes the sum is 2× the cost borne.
 *    There is deliberately no function in this module that adds allocations up
 *    across categories, and there must never be one.
 *
 * ⛔ AND THE OTHER TRAP, WHICH IS THE ONE THE TYPES WARN ABOUT LOUDEST.
 *
 * `posting_seq` is a **0-BASED ordinal that no screen displays**. A human must never
 * type or count one. It is the index of the posting in the REQUEST's own `postings`
 * array — and in single-entry mode that array is `[primary, ...particulars]`, so a
 * particular the operator sees at row 0 is posting **1**. The off-by-one is real, it
 * is invisible, and it would attribute a departmental cost to the bank account.
 *
 * {@link buildCostAllocations} is the ONLY place that number is produced, and it
 * takes `primaryLegCount` so the offset is stated once, at the call site that knows
 * how the postings array was assembled — never inferred here and never carried on a
 * row. This mirrors the fulfilment module's design decision for the same reason: the
 * ordinal is derived from the array construction, not from a UI position.
 *
 * ⚠️ IT IS ALSO 0-BASED WHERE `FulfilmentInput.line_no` ON THE SAME REQUEST BODY IS
 * 1-BASED. Both are inherited and neither is negotiable. And `posting_seq` is
 * RE-DERIVED on every read (a posting has no ordinal column) where `line_no` is
 * STORED — so an allocation's seq indexes the postings of *that same response* and
 * must be recomputed, never carried, across a rebuild.
 */

/**
 * One split an operator has keyed, carrying the display fields the row needs so it
 * can be rendered without re-reading the masters.
 *
 * ⚠️ `categoryName` and `centrePath` are DISPLAY ONLY and neither is sent.
 */
export interface DraftAllocation {
  /** The AXIS. Required — an allocation belongs to both a centre and its category. */
  costCategoryId: string;
  costCentreId: string;
  /** fixed-4 on the wire; free-typed here. */
  amount: string;
  /** Display: the axis's name, e.g. "Department". */
  categoryName: string;
  /** Display: the centre's PATH, so two centres named alike are distinguishable. */
  centrePath: string;
}

/** A particulars row that can carry splits. */
export interface AllocatableLine {
  /** The posting's own amount — what each axis's splits must total, exactly. */
  amount: string;
  allocations: readonly DraftAllocation[];
}

/** An empty split, ready to pick. Nothing is defaulted — see `axisRemainders`. */
export function emptyAllocation(): DraftAllocation {
  return {
    costCategoryId: "",
    costCentreId: "",
    // ⛔ NOT the posting amount, and not the remainder. Blank, because the operator
    // has not said yet. Pre-filling the whole amount would make the ONE-centre case
    // effortless and the two-centre case — which is the entire point of the feature —
    // require deleting a number first.
    amount: "",
    categoryName: "",
    centrePath: "",
  };
}

/**
 * Build the wire array for ONE voucher's particulars.
 *
 * ⛔ `primaryLegCount` IS THE OFFSET AND THE CALLER MUST GET IT RIGHT. It is how many
 * postings precede these rows in the request's `postings` array:
 *
 *   - SINGLE ENTRY: **1** — `postings[0]` is the account being paid from / into, and
 *     the particulars follow it. Passing 0 here would allocate the bank leg.
 *   - a postings array the rows map to one-for-one from index 0: **0**.
 *
 * It is a required parameter rather than a default precisely so that a new call site
 * has to think about it. There is no sensible default: the two cases differ by one,
 * and the wrong one is silent.
 *
 * Half-picked splits are dropped (no axis or no centre chosen is an operator who
 * changed their mind, not a split to nowhere), and so are zero amounts — the server
 * refuses a zero allocation, and a split that moves nothing is not a split.
 */
export function buildCostAllocations(
  lines: readonly AllocatableLine[],
  primaryLegCount: number,
): CostAllocationInput[] {
  const out: CostAllocationInput[] = [];
  lines.forEach((line, idx) => {
    for (const a of line.allocations) {
      if (a.costCategoryId.trim() === "" || a.costCentreId.trim() === "") continue;
      const amount = parseFixed4(a.amount);
      if (amount === 0n) continue;
      out.push({
        // 0-BASED, and offset by the legs that precede these rows. See above.
        posting_seq: idx + primaryLegCount,
        cost_category_id: a.costCategoryId,
        cost_centre_id: a.costCentreId,
        amount: formatFixed4(amount),
      });
    }
  });
  return out;
}

/** One axis's progress on one posting. */
export interface AxisRemainder {
  costCategoryId: string;
  categoryName: string;
  /** What this axis has been allocated so far, fixed-4. */
  allocated: string;
  /** posting amount − allocated, fixed-4. Zero means this axis is complete. */
  remainder: string;
  /** True when the axis totals the posting exactly — the only saveable state. */
  complete: boolean;
}

/**
 * The remainder PER AXIS for one posting — "the single most useful number on an
 * allocation form", in the words of the type that defines the wire field.
 *
 * ⛔ ONE ROW PER AXIS, NEVER ONE FIGURE FOR THE POSTING. A posting split across two
 * axes has two independent invariants, and a single combined remainder would read as
 * "50% allocated" on a posting that is in fact fully allocated twice over.
 *
 * Axes with no splits at all are ABSENT from the result rather than reported as
 * 100% outstanding: not allocating a posting on an axis is the normal case (most
 * postings are analysed on no axis at all), and listing every axis as incomplete
 * would make the one axis that IS incomplete invisible.
 */
export function axisRemainders(line: AllocatableLine): AxisRemainder[] {
  const posting = parseFixed4(line.amount);
  const byAxis = new Map<string, { name: string; sum: bigint }>();
  for (const a of line.allocations) {
    if (a.costCategoryId.trim() === "") continue;
    const cur = byAxis.get(a.costCategoryId) ?? { name: a.categoryName, sum: 0n };
    // The NAME is taken from whichever split carries one, so a row picked before the
    // masters loaded does not blank an axis heading that another split can name.
    cur.name = cur.name || a.categoryName;
    // ⚠️ SUMMED EVEN WHEN THE CENTRE IS UNPICKED. A half-keyed split with an amount
    // is money the operator has committed to this axis, and hiding it from the
    // remainder would show the axis as complete while it cannot be sent.
    cur.sum += parseFixed4(a.amount);
    byAxis.set(a.costCategoryId, cur);
  }
  const out: AxisRemainder[] = [];
  for (const [id, { name, sum }] of byAxis) {
    out.push({
      costCategoryId: id,
      categoryName: name,
      allocated: formatFixed4(sum),
      remainder: formatFixed4(posting - sum),
      complete: sum === posting && posting !== 0n,
    });
  }
  // Stable, by axis name, so two renders of one posting do not reorder.
  out.sort((a, b) => a.categoryName.localeCompare(b.categoryName));
  return out;
}

/**
 * A REFUSAL for one line, or "" when it is saveable.
 *
 * ⛔ THIS IS A REFUSAL, NOT A WARNING, and that is the opposite call from the
 * fulfilment module's — for a stated reason. An under-linked fulfilment is
 * legitimate (the goods that consumed no order). An under-allocated AXIS is not
 * legitimate at all: the server refuses it and a deferred DB trigger refuses behind
 * it, so sending it produces a 422 that costs the operator their whole form. Telling
 * them before the send is strictly kinder.
 *
 * ⚠️ AN AXIS WITH NO SPLITS IS NOT AN ERROR. Only a PARTIAL axis is.
 */
export function allocationRefusal(line: AllocatableLine): string {
  for (const r of axisRemainders(line)) {
    if (r.complete) continue;
    const over = parseFixed4(r.remainder) < 0n;
    return over
      ? `The ${r.categoryName || "chosen"} axis is over-allocated by ` +
          `${formatFixed4(-parseFixed4(r.remainder))} — its splits total ` +
          `${r.allocated} against a posting of ${formatFixed4(parseFixed4(line.amount))}. ` +
          `An axis must total the posting EXACTLY, to the paisa; there is no tolerance.`
      : `The ${r.categoryName || "chosen"} axis is short by ${r.remainder} — its ` +
          `splits total ${r.allocated} against a posting of ` +
          `${formatFixed4(parseFixed4(line.amount))}. An axis is complete or it is ` +
          `not: a partial split is refused by the server AND by the database, so ` +
          `nothing would be saved. Allocate the remainder, or remove the axis.`;
  }
  // A split with an amount but no centre chosen cannot be sent either, and the axis
  // arithmetic above cannot see it — the sum includes it, so the axis reads complete.
  for (const a of line.allocations) {
    if (a.costCategoryId.trim() === "") continue;
    if (a.costCentreId.trim() === "" && parseFixed4(a.amount) !== 0n) {
      return (
        `A split on the ${a.categoryName || "chosen"} axis has an amount but no cost ` +
        `centre. It is counted in that axis's total, so the axis looks complete while ` +
        `the split cannot be sent — choose a centre, or clear the amount.`
      );
    }
  }
  return "";
}

/**
 * Re-hydrate one posting's splits from a fetched voucher.
 *
 * ⛔⛔ OMITTING THIS IS DATA LOSS, NOT A MISSING FEATURE — the same defect the
 * fulfilment links had, and the reason `cost_allocations` is on the voucher GET at
 * all. An ALTER RE-STATES: `posting_cost_allocation` rows are written against the new
 * version's postings, so a form that rebuilt a voucher without the splits would send
 * none and **silently destroy the departmental split of a cost that is still in the
 * Profit & Loss.** Nothing refuses it, because zero allocations is a legal state: the
 * sum trigger has nothing to fire on, and the only trace is a Cost Centre Summary
 * that quietly stops adding up to the P&L.
 *
 * ⚠️ `seq` IS THE INDEX INTO THE RESPONSE'S OWN `postings` ARRAY, and the response
 * orders postings by (dr_cr DESC, amount DESC) — which is NOT the order the rebuilt
 * request will send them in. So the seq is used ONLY to match here, and
 * {@link buildCostAllocations} recomputes it from the new array. Carrying it through
 * would re-attach the split to whichever posting happened to land at that index.
 */
export function allocationsFromView(
  views: readonly CostAllocationView[],
  seq: number,
): DraftAllocation[] {
  return views
    .filter((v) => v.posting_seq === seq)
    .map((v) => ({
      costCategoryId: v.cost_category_id,
      costCentreId: v.cost_centre_id,
      amount: v.amount,
      categoryName: v.cost_category_name,
      centrePath: v.cost_centre_name,
    }));
}
