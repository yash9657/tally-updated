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
 *
 * ⛔⛔ THE `continue` BELOW IS HALF OF A PAIR. {@link axisRemainders} MAKES THE SAME
 * TEST and must go on making it: what the picker calls "allocated" has to be what
 * this function actually sends, or the screen reports an axis complete while the
 * request is short of it. The two drifted apart once — axisRemainders counted a
 * centre-less split and this one dropped it — and the result was a form showing
 * "allocated in full" over a 422. If you change what is dropped here, change what
 * is counted there in the same edit.
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
  /**
   * What this axis has been allocated so far, fixed-4.
   *
   * ⛔ COUNTED FROM THE SPLITS THAT WILL ACTUALLY BE SENT, i.e. exactly the ones
   * {@link buildCostAllocations} keeps. See {@link unassigned}.
   */
  allocated: string;
  /** posting amount − allocated, fixed-4. Zero means this axis is complete. */
  remainder: string;
  /**
   * Money keyed against this axis on a split with NO cost centre chosen, fixed-4.
   * Normally "0.0000".
   *
   * ⛔ THIS FIELD EXISTS BECAUSE THE NUMBER IT HOLDS IS INVISIBLE EVERYWHERE ELSE.
   * {@link buildCostAllocations} DROPS a centre-less split, so it is typed, shown
   * on its own row, and then silently absent from the request. Reporting it here
   * is what lets {@link allocationRefusal} say so before Ctrl+A rather than
   * letting the server say it afterwards, in a sentence about a posting ordinal.
   */
  unassigned: string;
  /**
   * True when the axis totals the posting exactly AND every one of its splits is
   * sendable — the only saveable state.
   *
   * ⛔ `unassigned` MUST BE ZERO FOR THIS TO BE TRUE, and that is not implied by
   * the arithmetic. A posting of 1000 with a centre-picked 1000 AND a centre-less
   * 400 beside it sums to exactly 1000 on the wire: the server would take it, and
   * the 400 the operator keyed would simply cease to exist with nothing anywhere
   * saying so.
   */
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
 *
 * ============================================================================
 * ⛔⛔ THE REMAINDER DESCRIBES THE **WIRE**, NOT THE FORM. THAT IS THE INVARIANT.
 * ============================================================================
 * `allocated` counts EXACTLY the splits {@link buildCostAllocations} will send,
 * and nothing else. The two functions are one statement made twice, and the
 * property that ties them together is:
 *
 *     remainder === posting − (what buildCostAllocations sends for this axis)
 *
 * This line used to add a centre-less split into `sum`, defending it with "money
 * the operator has committed to this axis, and hiding it from the remainder would
 * show the axis as complete while it cannot be sent". ⛔ IT PRODUCED THE EXACT
 * OUTCOME IT CLAIMED TO PREVENT. A posting of 1,000 with one split carrying 1,000
 * and no centre read `allocated 1000.0000 / remainder 0.0000 / complete`, the
 * picker rendered **"allocated in full"**, `buildCostAllocations` then dropped the
 * split, and Ctrl+A sent an axis with NO allocations at all — or, with a second
 * centre-picked split beside it, a SHORT axis, which the server refuses with a 422
 * that costs the operator the whole form. The screen and the request disagreed
 * about what had been allocated, and the screen was the flattering one.
 *
 * Counting only sendable splits makes the shortfall VISIBLE on the remainder row
 * (600.0000 of 1000 — 400.0000 left) instead of hiding it behind a green "in
 * full", and the 400 that will not be sent is reported separately as
 * {@link AxisRemainder.unassigned} so {@link allocationRefusal} can name it.
 */
export function axisRemainders(line: AllocatableLine): AxisRemainder[] {
  const posting = parseFixed4(line.amount);
  const byAxis = new Map<
    string,
    { name: string; sum: bigint; unassigned: bigint }
  >();
  for (const a of line.allocations) {
    if (a.costCategoryId.trim() === "") continue;
    const cur = byAxis.get(a.costCategoryId) ?? {
      name: a.categoryName,
      sum: 0n,
      unassigned: 0n,
    };
    // The NAME is taken from whichever split carries one, so a row picked before the
    // masters loaded does not blank an axis heading that another split can name.
    cur.name = cur.name || a.categoryName;
    const amount = parseFixed4(a.amount);
    // ⛔ THE SAME TEST buildCostAllocations MAKES, and it must stay the same test.
    // A centre-less split is not sent, so it is not allocated; it is set aside as
    // `unassigned` so that the one thing nothing else can see — money typed into a
    // row that will evaporate — has somewhere to be reported from.
    if (a.costCentreId.trim() === "") cur.unassigned += amount;
    else cur.sum += amount;
    byAxis.set(a.costCategoryId, cur);
  }
  const out: AxisRemainder[] = [];
  for (const [id, { name, sum, unassigned }] of byAxis) {
    out.push({
      costCategoryId: id,
      categoryName: name,
      allocated: formatFixed4(sum),
      remainder: formatFixed4(posting - sum),
      unassigned: formatFixed4(unassigned),
      // ⛔ `unassigned === 0n` IS NOT REDUNDANT. It is the over-keyed case: a
      // sendable 1000 against a posting of 1000 with a centre-less 400 beside it
      // sums correctly, and without this guard the form reads saveable while 400
      // of the operator's typing is discarded in silence.
      complete: sum === posting && posting !== 0n && unassigned === 0n,
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
 *
 * ⛔ THE UNASSIGNED SPLIT IS REPORTED **BEFORE** THE SHORTFALL, and the order is
 * load-bearing rather than stylistic. Both sentences can be true of one axis at
 * once (400 keyed with no centre, 300 short as well), and the shortfall is ALREADY
 * on screen — it is the remainder row, in the alarm colour, one line above. The
 * unassigned amount is on screen too but reads as allocated, so it is the fact the
 * operator cannot deduce; saying "short by 700" first would send them to add
 * another split when the fix is to finish the one they have.
 *
 * ⚠️ AND IT IS WHY THIS LOOP CANNOT SIMPLY TRUST `complete`. With `unassigned`
 * non-zero and the sendable splits totalling the posting exactly, the axis is NOT
 * complete and its remainder is `0.0000` — so the generic branch would emit "short
 * by 0.0000", a sentence that is arithmetically consistent and completely useless.
 */
export function allocationRefusal(line: AllocatableLine): string {
  const posting = formatFixed4(parseFixed4(line.amount));
  for (const r of axisRemainders(line)) {
    if (r.complete) continue;
    const axis = r.categoryName || "chosen";
    if (parseFixed4(r.unassigned) !== 0n) {
      return (
        `A split on the ${axis} axis has ${r.unassigned} against no cost centre. ` +
        `A split with no centre is NOT SENT — it is dropped on the way to the ` +
        `server — so of the ${formatFixed4(parseFixed4(r.allocated) + parseFixed4(r.unassigned))} ` +
        `keyed on this axis only ${r.allocated} would reach the books, against a ` +
        `posting of ${posting}. Choose a centre, or clear the amount.`
      );
    }
    const over = parseFixed4(r.remainder) < 0n;
    return over
      ? `The ${axis} axis is over-allocated by ` +
          `${formatFixed4(-parseFixed4(r.remainder))} — its splits total ` +
          `${r.allocated} against a posting of ${posting}. ` +
          `An axis must total the posting EXACTLY, to the paisa; there is no tolerance.`
      : `The ${axis} axis is short by ${r.remainder} — its ` +
          `splits total ${r.allocated} against a posting of ` +
          `${posting}. An axis is complete or it is ` +
          `not: a partial split is refused by the server AND by the database, so ` +
          `nothing would be saved. Allocate the remainder, or remove the axis.`;
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
