import type { FulfilmentInput, OrderDirection } from "../../api";
import type { VoucherType } from "../../keystroke/types";
// The EXACT BigInt-scaled helpers, not money.ts's parseFloat-based ones. A
// fulfilment qty is compared against the movement qty, and a comparison done on
// two doubles can disagree with the server's decimal by a ten-thousandth — which
// on this field is the difference between a saved voucher and a 422.
import { formatFixed4, parseFixed4 } from "./itemInvoice";

/**
 * LINKING A VOUCHER'S INVENTORY TO THE ORDER LINES IT CONSUMED (T3, migration
 * 0042) — the pure half. The picker UI is in FulfilmentPicker.tsx.
 *
 * ⛔⛔ A FULFILMENT QTY IS A LINK, NOT A MOVEMENT, and this is the one thing a UI
 * here can get wrong in a way nothing catches.
 *
 * It is how much of the ORDER was consumed. The inventory line's own `qty` is how
 * much STOCK moved. They are usually equal and they are DIFFERENT FACTS: closing
 * stock reads the movement, the order book reads this, and neither reads the other.
 *
 * The case where they legitimately differ is the reason the distinction is written
 * down. Shipping 105 against a 100-unit order records a movement of 105 (the goods
 * really left, closing stock is right) and a link of 100. The extra 5 consumed no
 * order, because nobody ordered them. So the form must NOT default the link to the
 * line quantity and lock it — over-fulfilment is refused by the server AND by a
 * deferred database trigger, and an operator who genuinely over-shipped would be
 * unable to save at all.
 *
 * ⛔ AND THE OTHER TRAP, WHICH IS AN OFF-BY-ONE THAT LOOKS LIKE NOTHING.
 *
 * `line_no` is the voucher's own 1-BASED inventory line, and `stock_movement.line_no`
 * has been 1-based since migration 0025. Meanwhile `CostAllocationInput.posting_seq`
 * on the SAME request body is 0-BASED. Both are inherited and neither is negotiable.
 *
 * Worse than the base: the grid an operator keys is NOT the inventory array that is
 * sent. `buildItemInvoiceRequest` filters to KEYED lines, so a grid whose first row
 * is blank and whose second row is keyed sends that second row as inventory line
 * ONE. A fulfilment numbered from the grid position would name a line that either
 * does not exist or belongs to different goods.
 *
 * {@link buildFulfilments} is the only place that number is produced, and it takes
 * the ALREADY-FILTERED lines for exactly this reason: the link travels ON the line
 * object, so the position it is numbered from is by construction the position the
 * line is sent at. There is no second array to keep in step.
 */

/**
 * One link an operator has made on one grid row, carrying the display fields the
 * row needs so it can be rendered without re-fetching the order.
 *
 * ⚠️ `orderNo`, `itemName` and `outstandingAtPick` are DISPLAY ONLY and none of them
 * is sent. `outstandingAtPick` is what the order line had outstanding at the moment
 * it was picked, kept so the row can say what the operator was looking at — it is
 * NOT re-checked against, and must never be used as a limit: it is derived
 * server-side on every read, and altering some other delivery note changes it
 * without touching this voucher.
 */
export interface DraftFulfilment {
  /** The order LINE, never the order: fulfilment is per item. */
  orderLineId: string;
  /** How much of the ORDER this line consumed. Free-typed; never locked. */
  qty: string;
  /** Display: the order's human number, e.g. "SO-14". */
  orderNo: string;
  /** Display: the item the ORDER line names, so a mismatch is visible. */
  itemName: string;
  /** Display: what was outstanding when this was picked. See the note above. */
  outstandingAtPick: string;
}

/** A grid line that can carry links. Both voucher line types satisfy this. */
export interface FulfillableLine {
  stockItemId: string;
  qty: string;
  fulfilments: readonly DraftFulfilment[];
}

/**
 * WHICH ORDER FAMILY A VOUCHER CLASS CAN DISCHARGE, and `null` for the classes that
 * can discharge none.
 *
 * ⛔ IT IS DECIDED BY WHICH WAY THE GOODS MOVE, not by which family exists. Goods
 * leaving discharge a SALES order (a customer's promise to buy); goods arriving
 * discharge a PURCHASE order (our promise to buy). Offering the wrong family would
 * let an operator record that a customer's order was satisfied by goods we RECEIVED.
 *
 * `null` for the rest, and each for a stated reason rather than by omission:
 *
 *  - `stock-journal` — an internal transfer or a manufacture. The goods never leave
 *    the business, so no external commitment is discharged. A recipe explosion is
 *    not a delivery.
 *  - `physical-stock` — a COUNT. It restates what is on the shelf against what the
 *    records say; it is not a movement anyone ordered, and linking a stock
 *    correction to a customer's order would report a discrepancy as a delivery.
 *  - `credit-note` / `debit-note` — a RETURN or a price correction. Goods coming
 *    back do not discharge a commitment, they undo one, and this build has no
 *    negative-link path: un-fulfilling is done by altering the delivery that made
 *    the link. Offering the picker here would produce a link that says the return
 *    delivered the order.
 *  - the six accounting classes that move no goods at all — there is no inventory
 *    line to attach a link to.
 */
export function fulfilsDirection(type: VoucherType): OrderDirection | null {
  switch (type) {
    // Goods OUT against a customer: the two ordinary ways a sales order is
    // discharged (RESEARCH.md:622 — "SO -> Delivery Note -> Sales Invoice, or SO ->
    // direct invoice"). BOTH are offered, because both really happen.
    case "delivery-note":
    case "sales":
      return "SALES";
    // Goods IN from a supplier: the same pair on the purchase side.
    case "receipt-note":
    case "purchase":
      return "PURCHASE";
    default:
      return null;
  }
}

/**
 * Build the wire array from the ALREADY-FILTERED inventory lines.
 *
 * ⛔ `lines` MUST BE THE EXACT ARRAY, IN THE EXACT ORDER, THAT BECOMES THE REQUEST'S
 * `inventory`. The 1-based `line_no` is derived from this array's own indices, so
 * passing the unfiltered grid produces links that name the wrong goods — see the
 * module header. Callers pass the result of the same `filter` they build inventory
 * from, on the line after they build it.
 *
 * Links with no order line chosen are dropped: a half-picked row is an operator who
 * changed their mind, not a link to nothing. A zero or blank qty is dropped for the
 * same reason — a link that consumes nothing of an order is not a link, and the
 * server would refuse it.
 */
export function buildFulfilments(
  lines: readonly FulfillableLine[],
): FulfilmentInput[] {
  const out: FulfilmentInput[] = [];
  lines.forEach((line, idx) => {
    for (const f of line.fulfilments) {
      if (f.orderLineId.trim() === "") continue;
      const qty = parseFixed4(f.qty);
      if (qty <= 0n) continue;
      out.push({
        // 1-BASED, and derived from THIS array's position. Not the grid's.
        line_no: idx + 1,
        order_line_id: f.orderLineId,
        qty: formatFixed4(qty),
      });
    }
  });
  return out;
}

/** An empty link, ready to pick. `qty` starts BLANK — see the module header. */
export function emptyFulfilment(): DraftFulfilment {
  return {
    orderLineId: "",
    // ⛔ NOT the line quantity, and not zero-and-locked. Blank, because the operator
    // has not said yet — and because pre-filling the movement quantity is exactly
    // the mistake that makes a legitimate over-delivery unsaveable.
    qty: "",
    orderNo: "",
    itemName: "",
    outstandingAtPick: "",
  };
}

/**
 * A WARNING for one line's links, or "" when there is nothing to say.
 *
 * ⚠️ THIS IS A WARNING AND NEVER A REFUSAL, and the difference is the whole point.
 * Linking MORE than the line moved is a real error the server refuses — forty units
 * of promise cannot be discharged by twenty units of goods — so saying so early is a
 * kindness. But linking LESS is completely legitimate: it is the over-delivery case
 * (105 shipped, 100 linked, and the extra 5 consumed no order), and a form that
 * refused it, or nagged about it, would be pressing the operator to enter a number
 * that is wrong.
 *
 * So: over-linking gets a sentence saying the server will refuse it. Under-linking
 * gets a sentence EXPLAINING it, and no suggestion that anything is wrong.
 */
export function fulfilmentWarning(line: FulfillableLine): string {
  const linked = line.fulfilments.reduce(
    (acc, f) => (f.orderLineId.trim() === "" ? acc : acc + parseFixed4(f.qty)),
    0n,
  );
  if (linked === 0n) return "";
  const moved = parseFixed4(line.qty);
  if (linked > moved) {
    return (
      `This line links ${formatFixed4(linked)} to orders but only moves ` +
      `${formatFixed4(moved)}. The server will refuse it: forty units of promise ` +
      `cannot be discharged by twenty units of goods, and no single order would ` +
      `look over-fulfilled while it happened.`
    );
  }
  if (linked < moved) {
    return (
      `This line moves ${formatFixed4(moved)} and links ${formatFixed4(linked)} of ` +
      `it to orders. That is allowed and is sometimes exactly right — the ` +
      `unlinked ${formatFixed4(moved - linked)} consumed no order, because nobody ` +
      `ordered it. Stock still moves in full either way.`
    );
  }
  return "";
}
