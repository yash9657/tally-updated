/**
 * demoLinks — THE TWO ORDINALS THE DEMO SEED MUST NOT GET WRONG, and the
 * arithmetic that must not be done in floating point.
 *
 * `frontend/scripts/seed-demo.mts` attaches two kinds of side-car row to
 * vouchers it posts:
 *
 *   - a COST ALLOCATION, which names a posting by a 0-BASED `posting_seq`, and
 *   - a FULFILMENT, which names an inventory line by a 1-BASED `line_no`.
 *
 * Both are index-into-an-array-the-server-never-sees-again, both are silent
 * when wrong, and they differ from each other by one. This module exists so the
 * seed never writes either number down.
 *
 * ===========================================================================
 * ⛔⛔ WHY A WRONG `posting_seq` IS INVISIBLE, WHICH IS WHY THIS IS A MODULE
 * ===========================================================================
 * HANDOFF §1.2: the T2 allocation UI nearly attached a departmental split to
 * the BANK leg of a payment instead of to the EXPENSE leg. The voucher balances
 * either way. The per-(posting, category) sum trigger is satisfied either way —
 * on a two-leg payment BOTH legs carry the same amount, so an amount check
 * cannot tell them apart. The Cost Centre Summary foots either way. Nothing
 * downstream can detect it: the only difference is that the report answers a
 * question about a bank account instead of about rent.
 *
 * ⛔ Therefore the DISTINGUISHING input is the LEDGER, never the amount.
 * {@link costSplitOn} takes the ledger the cost belongs to and DERIVES the
 * sequence, so the caller states a fact it can check by reading ("split the
 * RENT posting") instead of a number it cannot ("split posting 0").
 *
 * ===========================================================================
 * ⛔ MONEY AND QUANTITY ARE FIXED-4 STRINGS, COMPARED AS SCALED INTEGERS
 * ===========================================================================
 * Every total here is computed in `bigint` ten-thousandths. Never
 * Number()/parseFloat/+: the sum-to-posting invariant is EXACT to the paisa
 * (there is no tolerance, in this layer or in the deferred constraint trigger
 * behind it), and 0.1 + 0.2 !== 0.3 is the mechanism by which a real
 * misallocation hides for a year.
 */

import type {
  CostAllocationInput,
  FulfilmentInput,
  InventoryLineRequest,
  PostingRequest,
} from "../api";

/**
 * A fixed-point decimal STRING, as ten-thousandths.
 *
 * Refuses anything that is not a plain decimal literal with at most four
 * fractional digits — including the three spellings a float parser accepts and
 * a ledger must not: "NaN", "Infinity" and "1e4". `decimal.NewFromString`
 * server-side is equally strict about the first two, and a fifth decimal place
 * is a figure that CANNOT round-trip through the fixed-4 wire, so a caller that
 * produced one has already lost data by the time it reaches here.
 */
export function scaled4(v: string): bigint {
  const m = /^(-?)(\d+)(?:\.(\d{1,4}))?$/.exec(v);
  if (!m) {
    throw new Error(
      `demoLinks: ${JSON.stringify(v)} is not a fixed-point decimal string ` +
        `with at most 4 decimal places — money and quantity travel as fixed-4 ` +
        `strings, and a value a float parser would accept ("NaN", "1e4", ` +
        `"0.12345") either has no value or has already lost a digit`,
    );
  }
  const [, sign, whole, frac = ""] = m;
  const digits = `${whole}${frac.padEnd(4, "0")}`;
  const n = BigInt(digits);
  return sign === "-" ? -n : n;
}

/** The inverse, so an error message quotes the same spelling the wire carries. */
function unscaled4(n: bigint): string {
  const neg = n < 0n;
  const abs = neg ? -n : n;
  const whole = abs / 10000n;
  const frac = (abs % 10000n).toString().padStart(4, "0");
  return `${neg ? "-" : ""}${whole}.${frac}`;
}

/** One centre's share of one posting, on one axis. */
export interface CostShare {
  costCentreID: string;
  /** fixed-4 string. A MAGNITUDE — the posting's own Dr/Cr carries the sense. */
  amount: string;
}

/**
 * Build the cost allocations that split ONE posting — named by its LEDGER —
 * across the centres of ONE axis.
 *
 * ⛔ THE AXIS IS A PARAMETER AND THE FUNCTION IS CALLED ONCE PER AXIS. The
 * sum-to-posting invariant is per (posting, CATEGORY) and never per posting
 * (HANDOFF §8.1): the same ₹45,000 is allocated in full across Departments AND
 * AGAIN in full across Branches, and both are correct. A version of this
 * function that took several axes at once would have to total across them, and
 * that total is a multiple of the cost actually borne.
 *
 * Refuses, with a sentence rather than a constraint name:
 *   - a ledger that appears on no posting, or on MORE THAN ONE (the sequence
 *     would then be a coin flip between two legs the report cannot distinguish);
 *   - an empty split, a non-positive share, or the same centre twice;
 *   - a total that is not EXACTLY the posting's amount, to the paisa.
 */
export function costSplitOn(
  postings: readonly PostingRequest[],
  onLedgerID: string,
  costCategoryID: string,
  splits: readonly CostShare[],
): CostAllocationInput[] {
  const hits: number[] = [];
  postings.forEach((p, i) => {
    if (p.ledger_id === onLedgerID) hits.push(i);
  });
  if (hits.length === 0) {
    throw new Error(
      `demoLinks: no posting on this voucher is against ledger ${onLedgerID}, ` +
        `so there is no cost on it to attribute to a department`,
    );
  }
  if (hits.length > 1) {
    throw new Error(
      `demoLinks: ledger ${onLedgerID} appears on ${hits.length} postings ` +
        `(${hits.join(", ")}) — which of them bore the cost is not decidable ` +
        `from the ledger alone, and picking one would attach the split to a leg ` +
        `nothing downstream can tell apart from the other`,
    );
  }
  const seq = hits[0]!;

  if (splits.length === 0) {
    throw new Error(
      `demoLinks: the split of posting ${seq} names no cost centre, so the ` +
        `whole posting would be unattributed while looking allocated`,
    );
  }
  const seen = new Set<string>();
  let total = 0n;
  for (const s of splits) {
    const amount = scaled4(s.amount);
    if (amount <= 0n) {
      throw new Error(
        `demoLinks: the share of ${s.amount} to cost centre ${s.costCentreID} ` +
          `on posting ${seq} must be more than zero — an allocation is a ` +
          `MAGNITUDE and the posting's own Dr/Cr carries the direction`,
      );
    }
    if (seen.has(s.costCentreID)) {
      throw new Error(
        `demoLinks: cost centre ${s.costCentreID} appears twice on posting ` +
          `${seq} for cost category ${costCategoryID}; the same share keyed ` +
          `twice can only make the axis total correctly by making the other ` +
          `entry wrong`,
      );
    }
    seen.add(s.costCentreID);
    total += amount;
  }

  const want = scaled4(postings[seq]!.amount);
  if (total !== want) {
    const short = want - total;
    throw new Error(
      `demoLinks: the split of posting ${seq} on cost category ` +
        `${costCategoryID} totals ${unscaled4(total)} but the posting is ` +
        `${unscaled4(want)} — ${unscaled4(short)} unattributed. There is no ` +
        `tolerance: the Cost Centre Summary would disagree with the Profit & ` +
        `Loss by that amount`,
    );
  }

  return splits.map((s) => ({
    posting_seq: seq,
    cost_category_id: costCategoryID,
    cost_centre_id: s.costCentreID,
    amount: s.amount,
  }));
}

/**
 * Build the fulfilment link that says which ORDER LINE the inventory line for
 * `stockItemID` discharged.
 *
 * ⛔ `line_no` IS 1-BASED, where {@link CostAllocationInput.posting_seq} above
 * is 0-BASED. Inherited from `stock_movement.line_no`, not chosen. Derived here
 * from the position of the item's line so the seed never spells either out.
 *
 * ⛔ `qty` IS A LINK, NOT A MOVEMENT. It is how much of the ORDER was consumed;
 * the inventory line's own `qty` is how much STOCK moved. They are usually equal
 * and they are different facts, so this takes the link quantity explicitly
 * rather than defaulting it to the movement — over-delivery (105 shipped
 * against a 100-unit order: a movement of 105 and a link of 100) is an ordinary
 * case, and a helper that defaulted-and-locked would make it unrecordable.
 *
 * What it DOES refuse is a link LARGER than the movement, because that is the
 * one direction the goods cannot support: a line cannot discharge more
 * commitment than it moved. The server refuses it too; this refuses it at the
 * call site where the mistake is legible.
 */
export function fulfilmentOn(
  inventory: readonly InventoryLineRequest[],
  stockItemID: string,
  orderLineID: string,
  qty: string,
): FulfilmentInput {
  const hits: number[] = [];
  inventory.forEach((l, i) => {
    if (l.stock_item_id === stockItemID) hits.push(i);
  });
  if (hits.length === 0) {
    throw new Error(
      `demoLinks: this voucher moves no ${stockItemID}, so none of its lines ` +
        `could have satisfied an order for it — an order is fulfilled by goods`,
    );
  }
  if (hits.length > 1) {
    throw new Error(
      `demoLinks: ${stockItemID} moves on ${hits.length} lines of this voucher ` +
        `(1-based ${hits.map((i) => i + 1).join(", ")}); which of them ` +
        `discharged the order is a decision, not a derivation`,
    );
  }
  // ⛔ +1: stock_movement.line_no is 1-BASED and has been since migration 0025.
  const lineNo = hits[0]! + 1;

  const link = scaled4(qty);
  if (link <= 0n) {
    throw new Error(
      `demoLinks: the fulfilment of ${qty} against order line ${orderLineID} ` +
        `on inventory line ${lineNo} must be more than zero — a zero or ` +
        `negative link either records that an order was not fulfilled at all ` +
        `or un-delivers goods that went out`,
    );
  }
  const moved = scaled4(inventory[hits[0]!]!.qty);
  if (link > moved) {
    throw new Error(
      `demoLinks: inventory line ${lineNo} moved ${unscaled4(moved)} but the ` +
        `link claims ${unscaled4(link)} of order line ${orderLineID} was ` +
        `discharged — a line cannot discharge more commitment than it moved, ` +
        `and the order's outstanding balance would go negative`,
    );
  }

  return { line_no: lineNo, order_line_id: orderLineID, qty };
}
