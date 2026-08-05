/**
 * THE NON-ACCOUNTING STOCK VOUCHERS, CLIENT SIDE (Alt+F8 / Alt+F9 / Alt+F7 /
 * Alt+F10).
 *
 * Delivery Note, Receipt Note, Stock Journal and Physical Stock move goods and
 * post ZERO accounting lines. The server half has existed since bc84519
 * (internal/ledger/nonaccounting.go); this module is the other half — the one
 * that decides what an operator's keystrokes turn into on the wire.
 *
 * ===========================================================================
 * WHAT THIS FILE EXISTS TO GUARANTEE
 * ===========================================================================
 *
 * 1. NO MONEY SIDE, EVER. `validateStockVoucher` refuses `postings`,
 *    `bill_allocations`, `gst` and `tds` on these classes outright, and the
 *    refusal is what makes skipping the double-entry check safe: without it a
 *    Delivery Note would be the one document in the system permitted to state
 *    UNBALANCED ledger lines. So this builder sends the goods and nothing else.
 *    It does not send a "helpful" party leg — that is a 422, not a merge.
 *
 * 2. GOODS MOVE THE WAY THE DOCUMENT SAYS THEY DO. A Receipt Note is a
 *    goods-INWARD document. An outward line on one would take stock out of the
 *    warehouse under a piece of paper that says it arrived, and no later report
 *    could untangle the two — the movement ledger is a plain sum, so the
 *    quantity is simply wrong from then on. {@link STOCK_VOUCHER_SHAPES} is the
 *    single place the direction of each class is decided, and
 *    {@link refuseWrongWayGoods} re-checks the FINISHED body against it so an
 *    alter that rebuilt its lines from a fetched voucher is checked too.
 *
 * 3. A PHYSICAL STOCK VOUCHER SENDS THE COUNT, NEVER THE DELTA. "There are 13
 *    on the shelf" is an ABSOLUTE, and the server turns it into a correction
 *    inside the post transaction by differencing it against the book position
 *    (nonaccounting.go, buildPhysicalStockDeltas). A client that computed the
 *    delta itself would be differencing against a book it read EARLIER — and a
 *    stock-take is exactly the moment two readers race. Worse, a client that
 *    sent the count in `inventory` instead of `physical_count` would have the
 *    server ADD 13 to the 10 already there and leave the books asserting 23 on
 *    a shelf a human has just counted 13 of. So `physical_count` is the only
 *    field this builder ever puts a count in, and the wire field is asserted by
 *    name in the tests: the two shapes must stay distinguishable.
 *
 * QUANTITY IS MONEY, here as everywhere. Every qty/rate is a fixed-4 decimal
 * STRING routed through itemInvoice.ts's BigInt-scaled parse/format — never
 * `Number(qty) * Number(rate)`, which turns 1.005 kg into 1.00499999999999989
 * and makes a closing stock valuation depend on arithmetic nobody chose.
 */

import type { InventoryLineRequest, PhysicalCountLineRequest, VoucherRequest } from "../../api";
import type { VoucherType } from "../../keystroke/types";
import { formatFixed4, mulFixed4, parseFixed4 } from "./itemInvoice";
import { buildFulfilments, type DraftFulfilment } from "./fulfilment";

/** The four classes that move goods and post no accounting lines. */
export type StockVoucherType =
  | "delivery-note"
  | "receipt-note"
  | "stock-journal"
  | "physical-stock";

/**
 * How a stock class's keyed rows become movements.
 *
 *  - "fixed-direction" — every line moves ONE way and the operator is never
 *    offered the choice (a Receipt Note cannot ship goods out).
 *  - "transfer" — the Stock Journal: each keyed row is a MOVE, and becomes two
 *    movements, OUT of the source godown and IN to the destination. It is the
 *    one class that carries both directions on the same voucher, which is why
 *    migration 0025 made stock_movement_not_negative DEFERRABLE: checked per
 *    statement, the OUT would raise before the IN had been written.
 *  - "count" — Physical Stock: the rows are COUNTED positions, not movements.
 *    They carry no direction because a count carries no sign.
 */
export type StockVoucherShape =
  | { readonly kind: "fixed-direction"; readonly direction: "IN" | "OUT" }
  | { readonly kind: "transfer" }
  | { readonly kind: "count" };

/**
 * THE SINGLE PLACE EACH CLASS'S SHAPE IS DECIDED.
 *
 * The form reads it to decide which columns to render; the builder reads it to
 * decide what to emit; {@link refuseWrongWayGoods} reads it to re-check the
 * finished body. One table, three readers — so a class cannot render one way
 * and post another, which is the failure mode that produces a document whose
 * heading and whose stock effect disagree.
 */
export const STOCK_VOUCHER_SHAPES: Readonly<
  Record<StockVoucherType, StockVoucherShape>
> = {
  // Goods LEAVE against a customer, before or without the invoice.
  "delivery-note": { kind: "fixed-direction", direction: "OUT" },
  // Goods ARRIVE from a supplier, before the bill.
  "receipt-note": { kind: "fixed-direction", direction: "IN" },
  "stock-journal": { kind: "transfer" },
  "physical-stock": { kind: "count" },
};

const STOCK_VOUCHER_TYPES = Object.keys(STOCK_VOUCHER_SHAPES) as StockVoucherType[];

/** Whether a voucher class is one of the four stock-only classes. */
export function isStockVoucherType(type: VoucherType): type is StockVoucherType {
  return (STOCK_VOUCHER_TYPES as string[]).includes(type);
}

/** The shape of a stock class. */
export function stockShapeFor(type: StockVoucherType): StockVoucherShape {
  return STOCK_VOUCHER_SHAPES[type];
}

/**
 * One keyed row of a stock voucher's grid.
 *
 * There is NO ledger column and no amount column, because there is no money
 * side: `rate` exists to VALUE the movement (the weighted-average valuer reads
 * qty and rate; nothing reads a stated amount), not to price a sale.
 */
export interface StockVoucherLine {
  stockItemId: string;
  /** Display only, joined from the stock item master. */
  itemName: string;
  /**
   * The location the goods move at. On a TRANSFER this is the SOURCE godown.
   * "" is a real answer, not a missing one: it posts as SQL NULL, which is what
   * a company that never enabled multi-location wants.
   */
  godownId: string;
  /** TRANSFER only: the location the goods arrive INTO. Unused elsewhere. */
  toGodownId: string;
  /** "" = this line is not tracked to a batch/lot. */
  batchId: string;
  qty: string;
  rate: string;
  /** Display only (the master's unit symbol). */
  unit: string;
  /**
   * WHICH ORDER LINES THIS LINE'S GOODS DISCHARGED (T3). Populated only on a
   * Delivery Note or a Receipt Note — see `fulfilsDirection`, and the guard in
   * {@link buildStockVoucherRequest} explaining why a TRANSFER cannot carry these
   * even in principle.
   *
   * It lives ON the line so that the 1-based `line_no` each link is sent with is by
   * construction the position the line itself is sent at. See the same field on
   * ItemInvoiceLine for the off-by-one this avoids.
   */
  fulfilments: readonly DraftFulfilment[];
}

/** An empty grid row, ready to key. */
export function emptyStockLine(): StockVoucherLine {
  return {
    stockItemId: "",
    itemName: "",
    godownId: "",
    toGodownId: "",
    batchId: "",
    qty: "",
    rate: "",
    unit: "",
    fulfilments: [],
  };
}

/**
 * Whether a row has been keyed, which is SHAPE-DEPENDENT.
 *
 * A movement needs a positive quantity — a zero-quantity movement is not a
 * thing that happened, and stock_movement's CHECK (qty > 0) refuses one. A
 * COUNT of zero is the opposite: "the shelf is empty" is a real observation,
 * and it is the one an auditor most wants recorded, so a counted row is keyed
 * as soon as it names an item.
 */
export function isKeyedStockLine(
  line: StockVoucherLine,
  shape: StockVoucherShape,
): boolean {
  if (line.stockItemId.trim() === "") return false;
  if (shape.kind === "count") return true;
  return parseFixed4(line.qty) > 0n;
}

/** Everything the builder needs beyond the grid. */
export interface StockVoucherInputs {
  type: StockVoucherType;
  lines: readonly StockVoucherLine[];
  voucherNo?: string;
  voucherDate?: string;
  narration?: string;
  /** The customer/supplier the document names. Descriptive: there is no leg. */
  party?: string;
}

/**
 * The value of one movement at the stated rate, exactly: qty x rate on
 * BigInt-scaled integers.
 *
 * It is informational on these classes — nothing ties it to a taxable base and
 * the valuer recomputes value from qty and rate itself — but it is written so
 * the movement row and the hashed version payload state what the goods that
 * moved were worth, which is what a warehouse dispute is argued from.
 */
function movementAmount(qty: string, rate: string): string {
  return mulFixed4(qty, rate);
}

/**
 * Build the POST body for a stock voucher: the goods, and NOTHING ELSE.
 *
 * NO postings, NO bill_allocations, NO gst. Not as an omission but as the
 * contract: `validateStockVoucher` refuses each of them with its own domain
 * message, because a document with no money side that nonetheless states one is
 * a mistake about what the operator is keying, not a shortcut. Sending a party
 * leg here would not be belt-and-braces — it would be a 422 on every Delivery
 * Note the warehouse ever tried to key.
 *
 * Throws when nothing is keyed: an empty stock voucher would burn a real
 * voucher number on a document that says nothing.
 */
export function buildStockVoucherRequest(
  inputs: StockVoucherInputs,
): VoucherRequest {
  const shape = stockShapeFor(inputs.type);
  const keyed = inputs.lines.filter((l) => isKeyedStockLine(l, shape));
  if (keyed.length === 0) {
    throw new Error(
      shape.kind === "count"
        ? "A Physical Stock voucher records at least one counted position — name the item that was counted."
        : `A ${stockVoucherTitle(inputs.type)} exists to record goods moving; key at least one item and quantity.`,
    );
  }

  const req: VoucherRequest = {
    voucher_date: inputs.voucherDate,
    narration: inputs.narration,
    party: inputs.party,
  };
  if (inputs.voucherNo) req.voucher_no = inputs.voucherNo;

  if (shape.kind === "count") {
    // THE COUNT, VERBATIM. Never a delta: see this file's header — the server
    // differences it against the book position inside the post transaction, and
    // a client-computed delta would be measured against a book it read earlier.
    const counts: PhysicalCountLineRequest[] = keyed.map((l) => ({
      stock_item_id: l.stockItemId,
      godown_id: l.godownId,
      batch_id: l.batchId,
      qty: formatFixed4(parseFixed4(l.qty)),
      // Required (non-zero) only when the count EXCEEDS the books: a surplus
      // brought in at zero cost writes the item's weighted average down, so the
      // server refuses one rather than silently devaluing the warehouse.
      rate: formatFixed4(parseFixed4(l.rate)),
    }));
    req.physical_count = counts;
    return refuseWrongWayGoods(inputs.type, req);
  }

  const inventory: InventoryLineRequest[] = [];
  for (const [i, l] of keyed.entries()) {
    const qty = formatFixed4(parseFixed4(l.qty));
    const rate = formatFixed4(parseFixed4(l.rate));
    const amount = movementAmount(l.qty, l.rate);
    if (shape.kind === "transfer") {
      // A TRANSFER IS TWO MOVEMENTS, and they are generated as a PAIR from one
      // keyed row so they cannot come out unbalanced. Two rows keyed
      // independently would let an operator record 10 out of Main and 8 into
      // Godown B — a two-unit stock write-off with no document saying so, and
      // one that every totals check in the system passes, because a stock
      // journal has no total to check.
      if (l.godownId === l.toGodownId) {
        throw new Error(
          `Line ${i + 1}: a stock journal moves goods BETWEEN locations — the From and To godowns are the same, so nothing would move.`,
        );
      }
      inventory.push({
        stock_item_id: l.stockItemId,
        godown_id: l.godownId,
        batch_id: l.batchId,
        direction: "OUT",
        qty,
        rate,
        amount,
      });
      inventory.push({
        stock_item_id: l.stockItemId,
        godown_id: l.toGodownId,
        batch_id: l.batchId,
        direction: "IN",
        qty,
        rate,
        amount,
      });
      continue;
    }
    inventory.push({
      stock_item_id: l.stockItemId,
      godown_id: l.godownId,
      batch_id: l.batchId,
      direction: shape.direction,
      qty,
      rate,
      amount,
    });
  }
  req.inventory = inventory;
  // ---- THE ORDER LINKS (T3).
  //
  // ⛔ ONLY FOR A `fixed-direction` CLASS, AND THE GUARD IS LOAD-BEARING ARITHMETIC
  // RATHER THAN CAUTION. `buildFulfilments` numbers each link from its line's
  // position in the array it is given, which is only the position that line is SENT
  // at when the mapping is one keyed row to one inventory line. On a TRANSFER it is
  // not: one keyed row pushes TWO movements (the OUT and the IN), so row 2 is
  // inventory line 3 and every link past the first would name the wrong movement.
  //
  // Nothing is lost by excluding them, because neither class can discharge a
  // commitment anyway — `fulfilsDirection` returns null for both, so the picker is
  // never rendered on either. A transfer moves goods between our own locations, and a
  // count restates what is on the shelf; neither is a delivery anybody ordered. This
  // guard is what makes those two facts one fact instead of two that could drift.
  if (shape.kind === "fixed-direction") {
    const fulfilments = buildFulfilments(keyed);
    if (fulfilments.length > 0) req.fulfilments = fulfilments;
  }
  return refuseWrongWayGoods(inputs.type, req);
}

/**
 * Re-check a FINISHED stock voucher body against its class, and refuse it here
 * rather than letting the server refuse it (or, worse, accept it).
 *
 * WHY THIS IS NOT A TAUTOLOGY ON THE BUILDER ABOVE. It also runs on the ALTER
 * path, where the lines were rebuilt from a voucher fetched back from the
 * server — so the directions came from stored movements, not from this class's
 * shape. That is the case where a wrong-way line is actually reachable: a
 * document whose stored movement disagrees with its heading would be re-posted
 * verbatim and the disagreement made permanent. Checking the body, not the
 * inputs, is what covers both paths with one rule.
 *
 * Returns the request so it can be used as the last expression of a build.
 */
export function refuseWrongWayGoods(
  type: StockVoucherType,
  req: VoucherRequest,
): VoucherRequest {
  const shape = stockShapeFor(type);
  const title = stockVoucherTitle(type);

  // The money side, refused for the same reason validateStockVoucher refuses
  // it: skipping the double-entry check is only safe when paired with this.
  if (req.postings?.length) {
    throw new Error(
      `A ${title} posts no accounting lines at all — it records the movement of goods, not money. Remove the ${req.postings.length} posting(s).`,
    );
  }
  if (req.bill_allocations?.length) {
    throw new Error(
      `A ${title} creates no outstanding bill: there is no party leg for an allocation to reference (the invoice that follows carries the bill).`,
    );
  }
  if (req.gst) {
    throw new Error(
      `A ${title} carries no tax: GST is charged by the INVOICE, and deriving it here would file the same supply twice.`,
    );
  }

  if (shape.kind === "count") {
    if (req.inventory?.length) {
      throw new Error(
        "A Physical Stock voucher states what was COUNTED, not what moved. Send the counted quantities and let the server derive the correction.",
      );
    }
    if (!req.physical_count?.length) {
      throw new Error("A Physical Stock voucher records at least one counted position.");
    }
    return req;
  }

  if (req.physical_count?.length) {
    throw new Error(
      `Only a Physical Stock voucher carries counted quantities; a ${title} states the movement itself.`,
    );
  }
  if (!req.inventory?.length) {
    throw new Error(`A ${title} exists to record goods moving; it has no stock lines.`);
  }
  if (shape.kind === "fixed-direction") {
    for (const [i, l] of req.inventory.entries()) {
      if (l.direction !== shape.direction) {
        throw new Error(
          `Line ${i + 1}: a ${title} moves stock ${shape.direction}, not ${l.direction} — goods keyed the wrong way would be added to the warehouse by a document that says they left.`,
        );
      }
    }
  }
  return req;
}

/**
 * Whether a fetched stock voucher of this class can be ALTERED through this
 * screen, and if not, why.
 *
 * GET /vouchers/{id} projects a Physical Stock voucher's DELTA movements — the
 * correction the server derived, not the count the operator keyed, because the
 * count is not stored (the closing position immediately after the voucher IS
 * the count, by construction). So a fetched physical stock voucher cannot be
 * re-posted verbatim: its `inventory` is refused on the class, and its counts
 * are gone. Re-keying the delta as a count would restate "there are 3" on a
 * shelf that holds 13.
 *
 * The honest answer is to refuse LOUDLY and say what to do instead, rather than
 * to let an alter silently corrupt the position it was meant to correct.
 * Returns null when the class alters normally.
 */
export function stockAlterRefusal(type: StockVoucherType): string | null {
  if (stockShapeFor(type).kind !== "count") return null;
  return (
    "A Physical Stock voucher cannot be altered here: what is stored is the CORRECTION the server derived, not the count you keyed, so re-posting this form would restate the correction as if it were the count. Key a fresh Physical Stock voucher with the true count (the server differences it against the books), or Alt+D to delete this one."
  );
}

/** The document title of a stock class (matches VoucherConfig.title). */
export function stockVoucherTitle(type: StockVoucherType): string {
  switch (type) {
    case "delivery-note":
      return "Delivery Note";
    case "receipt-note":
      return "Receipt Note";
    case "stock-journal":
      return "Stock Journal";
    case "physical-stock":
      return "Physical Stock";
  }
}
