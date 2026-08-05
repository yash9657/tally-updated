/**
 * ITEM INVOICE (Ctrl+H > Item Invoice on F8 Sales / F9 Purchase).
 *
 * Tally's item invoice is the mode where you key GOODS — item, godown, batch,
 * quantity, rate — and the accounting falls out of them. This module is the
 * client half of that: it turns a keyed grid into the `inventory` array the
 * backend's voucher wire accepts (server.go's `inventoryLineReq`), plus the
 * balanced accounting postings that must accompany it.
 *
 * TWO PROPERTIES THIS FILE EXISTS TO GUARANTEE
 *
 * 1. QUANTITY IS MONEY. Every qty / rate / discount / amount is a fixed-4
 *    decimal STRING and every multiplication happens on BigInt-scaled integers.
 *    Never `Number(qty) * Number(rate)`: an IEEE-754 double cannot represent
 *    1.005 or 2.675, so a line of 1.005 kg at 33.33 comes out as 33.4966 in
 *    floating point where the backend's decimal.Decimal (and any accountant
 *    with a calculator) says 33.4967. One paisa per line, silently, on the
 *    document the customer is invoiced from. The backend stores quantities as
 *    NUMERIC and derives stock VALUE by replaying them, so a drifted quantity
 *    is not merely a display bug — it perturbs the closing valuation of every
 *    later issue of that item.
 *
 *    JS numbers are also the reason the scaling is BigInt rather than `number`:
 *    qty and rate are each scaled by 1e4, so their product lives at 1e8 and a
 *    modest 1e8-unit quantity would exceed Number.MAX_SAFE_INTEGER before the
 *    rounding step ever ran.
 *
 * 2. THE CLIENT STATES NO AMOUNTS AT ALL. On an item invoice the SERVER
 *    generates the GST lines AND the accounting postings from the stock lines
 *    (internal/ledger/item_invoice.go). A body carrying `inventory` + `gst`
 *    together with `gst.lines` or with `postings` is REFUSED — 422, not a
 *    merge, because silently preferring one input over the other is how a
 *    100-unit despatch gets billed as a Rs 1 invoice with every layer reporting
 *    success. So this module sends: the grid, the party ledger, the item
 *    (sales/purchase) ledger, and the tax ledger ids. Nothing else.
 *
 *    What the client CAN still get wrong is WHICH LEDGERS IT NAMES — booking a
 *    purchase to the Domestic Sales ledger balances perfectly and reports the
 *    company's costs as revenue. That is why the acceptance test resolves the
 *    posted ids back to NAMES: this project has already shipped a fully
 *    reversed purchase once (WS8b) and it survived every totals check there is.
 *
 * ARITHMETIC PARITY IS LOAD-BEARING, NOT COSMETIC. The server derives each
 * line's taxable value itself and REFUSES a non-zero `amount` that disagrees
 * with it (it does not silently correct one). So {@link lineAmount} reproduces
 * `ledger.InventoryLine.TaxableValue()` exactly — including rounding ONCE, at
 * the end, on the unrounded product — or every discounted line is a 422.
 */

import type {
  InventoryLineRequest,
  GSTRequest,
  VoucherRequest,
} from "../../api";
import type { OutputTaxLedgers } from "./gstVoucher";
import { deriveGstSplit } from "./gstDerive";
import { addMoney } from "./money";
import { buildFulfilments, type DraftFulfilment } from "./fulfilment";

// ---------------------------------------------------------------------------
// Exact fixed-4 decimal arithmetic on BigInt-scaled integers
// ---------------------------------------------------------------------------

/** Fixed-point scale: every value is carried as integer ten-thousandths. */
const SCALE = 10_000n;

/**
 * Parse a decimal STRING into integer ten-thousandths, EXACTLY.
 *
 * Deliberately not `Number.parseFloat(...) * 10000`: that route rounds the
 * input to the nearest double before we ever get to scale it, which is where
 * 1.005 becomes 1.00499999999999989. We read the digits instead, so what the
 * operator typed is what we compute with.
 *
 * A partially-typed or junk value ("", "-", "12.") parses as 0 rather than
 * throwing, matching money.ts: the grid re-renders on every keystroke and a
 * half-typed quantity must not blow up the form. Digits beyond the fourth are
 * TRUNCATED, not rounded — a quantity carries 4 dp on the wire and inventing a
 * fifth here would make the client and the column disagree.
 */
export function parseFixed4(value: string): bigint {
  const raw = (value ?? "").trim();
  if (raw === "") return 0n;
  const m = /^([+-]?)(\d*)(?:\.(\d*))?$/.exec(raw);
  if (!m) return 0n;
  const sign = m[1] === "-" ? -1n : 1n;
  const whole = m[2] ?? "";
  const frac = (m[3] ?? "").slice(0, 4).padEnd(4, "0");
  if (whole === "" && (m[3] ?? "") === "") return 0n;
  return sign * BigInt((whole === "" ? "0" : whole) + frac);
}

/** Render integer ten-thousandths back as a fixed-4 decimal string. */
export function formatFixed4(units: bigint): string {
  const neg = units < 0n;
  const abs = (neg ? -units : units).toString().padStart(5, "0");
  return `${neg ? "-" : ""}${abs.slice(0, -4)}.${abs.slice(-4)}`;
}

/**
 * Divide `n` by `d` rounding HALF-AWAY-FROM-ZERO, which is what
 * shopspring/decimal's Round does and therefore what the backend produces.
 * Banker's rounding here would disagree with the server on every exact .00005
 * and the voucher would come back 422 "GST verification failed" with no hint
 * as to why.
 */
function divRoundHalfUp(n: bigint, d: bigint): bigint {
  if (n >= 0n) return (n + d / 2n) / d;
  return -((-n + d / 2n) / d);
}

/**
 * Multiply two fixed-4 decimal strings, returning a fixed-4 decimal string
 * rounded half-away-from-zero — the qty × rate of one invoice line.
 *
 * Both operands are scaled by 1e4, so their product is scaled by 1e8 and has to
 * come back down by 1e4. That intermediate is why this is BigInt: 1.005 × 33.33
 * is 10_050 × 333_300 = 3_349_665_000, and a realistic bulk quantity times a
 * realistic rate passes 2^53 well before anyone notices.
 */
export function mulFixed4(a: string, b: string): string {
  return formatFixed4(divRoundHalfUp(parseFixed4(a) * parseFixed4(b), SCALE));
}

/**
 * qty × rate less `discountPct`, rounded half-away-from-zero EXACTLY ONCE, at
 * the end, on the UNROUNDED product.
 *
 * The single rounding is not a style choice — it is the server's formula
 * (`ledger.InventoryLine.TaxableValue()`), and the server REFUSES a line whose
 * `amount` disagrees with what it derives rather than quietly correcting it. So
 * every plausible-looking alternative is a 422 on some line:
 *
 *   round(gross) then discount        — rounds twice; drifts on 1.005 @ 33.33
 *   gross − round(gross × d / 100)    — two roundings that can BOTH round up on
 *                                       an exact half-paisa, differing by 0.0001
 *
 * The intermediate lives at 1e12 (qty and rate at 1e4 each, times a percentage
 * at 1e4), which is another reason this is BigInt rather than `number`.
 */
export function taxableValueOf(
  qty: string,
  rate: string,
  discountPct: string,
): string {
  const hundred = SCALE * 100n; // 100, at the fixed-4 scale
  const disc = parseFixed4(discountPct);
  if (disc === 0n) return mulFixed4(qty, rate);
  // qty × rate at scale 1e8, then × (100 − d) / 100, brought down to 1e4 in ONE
  // division so the result is rounded exactly once.
  const gross = parseFixed4(qty) * parseFixed4(rate);
  return formatFixed4(divRoundHalfUp(gross * (hundred - disc), hundred * SCALE));
}

/** True when a fixed-4 string is exactly zero (never `parseFloat(x) === 0`). */
export function isZeroFixed4(value: string): boolean {
  return parseFixed4(value) === 0n;
}

// ---------------------------------------------------------------------------
// The grid
// ---------------------------------------------------------------------------

/**
 * One keyed line of the item grid, joined with the bits of the stock-item
 * master the client needs to build a verifiable body.
 *
 * `gstRatePct` and `hsn` are NOT sent on the inventory line — the wire has no
 * field for them, because the SERVER reads them from the stock item master. We
 * carry them here only to derive the tax postings the server will verify: if a
 * client's idea of an item's rate disagreed with the master's, the post is
 * refused rather than silently taxed at the client's number.
 */
export interface ItemInvoiceLine {
  stockItemId: string;
  itemName: string;
  /** "" = this line is not tracked to a location (posts as SQL NULL). */
  godownId: string;
  /** "" = this line is not tracked to a batch/lot. */
  batchId: string;
  qty: string;
  rate: string;
  discountPct: string;
  /** From the stock item master — the rate the server will derive tax at. */
  gstRatePct: string;
  /** From the stock item master; groups the derived GST lines. */
  hsn: string;
  /** Display only (the master's unit symbol). */
  unit: string;
  /**
   * WHICH ORDER LINES THIS LINE'S GOODS DISCHARGED (T3).
   *
   * ⛔ IT LIVES ON THE LINE, AND THAT IS THE WHOLE DESIGN. The wire's
   * `FulfilmentInput.line_no` is the 1-based position of the line in the request's
   * `inventory` array — which is the KEYED lines, not the grid's rows. Carrying the
   * links in a parallel structure keyed by grid position would put that number one
   * out the first time an operator left a blank row above a keyed one, and it would
   * name either a line that does not exist or one holding different goods. Because
   * the links travel ON the line, the position they are numbered from is by
   * construction the position the line is sent at.
   *
   * ⚠️ An empty array is the normal case: most invoices discharge no order.
   */
  fulfilments: readonly DraftFulfilment[];
}

/** An empty grid row, ready to key. */
export function emptyItemLine(): ItemInvoiceLine {
  return {
    stockItemId: "",
    itemName: "",
    godownId: "",
    batchId: "",
    qty: "",
    rate: "",
    discountPct: "",
    gstRatePct: "",
    hsn: "",
    unit: "",
    fulfilments: [],
  };
}

/** A row counts as keyed once it names an item AND a non-zero quantity. */
export function isKeyedLine(l: ItemInvoiceLine): boolean {
  return l.stockItemId.trim() !== "" && parseFixed4(l.qty) > 0n;
}

/**
 * The line's net amount — and, exactly, the number the server will derive.
 *
 * It is the SAME number three times over: the movement's stored `amount`, the
 * GST line's `taxable_value`, and what the valuer carries the stock in at.
 * Migration 0027's deferred trigger refuses a commit where those ever differ,
 * so a client formula that drifts by a paisa is not a display bug — it is a
 * rejected voucher.
 */
export function lineAmount(l: ItemInvoiceLine): string {
  return taxableValueOf(l.qty, l.rate, l.discountPct);
}

/** The invoice's taxable base: the sum of the keyed lines' net amounts. */
export function invoiceTaxable(lines: readonly ItemInvoiceLine[]): string {
  let total = "0.0000";
  for (const l of lines) {
    if (!isKeyedLine(l)) continue;
    total = addMoney(total, lineAmount(l));
  }
  return total;
}

/**
 * The taxable base per (GST rate, HSN) — one derived GST line each.
 *
 * The server derives one gst line per rate/HSN group and sums the heads, so the
 * client must group identically or the verification fails on the rounding: two
 * lines of 100.005 at 18% round to 18.0009 each (36.0018) when grouped
 * separately but to 36.0018 when summed first — they agree here, and they do
 * NOT agree for every base, which is exactly why the grouping has to match
 * rather than merely look reasonable.
 */
export function taxableByRate(
  lines: readonly ItemInvoiceLine[],
): { gstRatePct: string; hsn: string; taxable: string }[] {
  const groups = new Map<string, { gstRatePct: string; hsn: string; taxable: string }>();
  for (const l of lines) {
    if (!isKeyedLine(l)) continue;
    const rate = l.gstRatePct.trim() === "" ? "0.0000" : l.gstRatePct;
    const key = `${parseFixed4(rate).toString()}|${l.hsn}`;
    const existing = groups.get(key);
    if (existing) {
      existing.taxable = addMoney(existing.taxable, lineAmount(l));
    } else {
      groups.set(key, { gstRatePct: rate, hsn: l.hsn, taxable: lineAmount(l) });
    }
  }
  return [...groups.values()];
}

/** The derived tax heads for a whole item invoice (all rate groups summed). */
export interface ItemInvoiceTax {
  intra: boolean;
  cgst: string;
  sgst: string;
  igst: string;
  /** cgst + sgst + igst. */
  total: string;
}

/**
 * Derive the invoice's tax heads by summing the per-rate-group splits.
 *
 * Intra vs inter is a property of the two STATE CODES, not of a line, so it is
 * decided once and every group follows it — a single invoice cannot be half
 * IGST and half CGST.
 */
export function deriveItemInvoiceTax(
  lines: readonly ItemInvoiceLine[],
  supplierStateCode: string,
  placeOfSupplyStateCode: string,
): ItemInvoiceTax {
  let cgst = "0.0000";
  let sgst = "0.0000";
  let igst = "0.0000";
  let intra = false;
  for (const g of taxableByRate(lines)) {
    const split = deriveGstSplit(
      g.taxable,
      g.gstRatePct,
      supplierStateCode,
      placeOfSupplyStateCode,
    );
    intra = split.intra;
    cgst = addMoney(cgst, split.cgst);
    sgst = addMoney(sgst, split.sgst);
    igst = addMoney(igst, split.igst);
  }
  return {
    intra,
    cgst,
    sgst,
    igst,
    total: addMoney(addMoney(cgst, sgst), igst),
  };
}

// ---------------------------------------------------------------------------
// Building the request
// ---------------------------------------------------------------------------

/** Everything the builder needs beyond the grid itself. */
export interface ItemInvoiceInputs {
  /**
   * Which way the goods physically move. Mirrors ledger.expectedStockDirection,
   * and the server REFUSES a line moving the other way: a Sales invoice with an
   * INWARD line would credit revenue while ADDING stock to the warehouse, so
   * the sale would inflate both the P&L and the closing inventory and no later
   * report could untangle the two.
   */
  direction: "IN" | "OUT";
  /** The debtor/creditor the invoice is billed to. REQUIRED by the server. */
  partyLedgerId: string;
  /**
   * The revenue/expense ledger the VALUE OF THE GOODS is booked to: a Sales
   * Accounts ledger for F8, a Purchase Accounts ledger for F9.
   *
   * THIS IS THE ONE ACCOUNTING DECISION LEFT TO THE CLIENT, and it is the one
   * that still cannot be caught by any totals check: naming Domestic Sales on a
   * purchase produces a perfectly balanced voucher that reports the company's
   * costs as revenue. Server-side generation removed every other way to get the
   * accounting wrong; it could not remove this one, because only the client
   * knows which ledger the operator meant.
   */
  itemLedgerId: string;
  outputTax: OutputTaxLedgers;
  supplierStateCode: string;
  placeOfSupplyStateCode: string;
  lines: readonly ItemInvoiceLine[];
  voucherNo?: string;
  voucherDate?: string;
  narration?: string;
  party?: string;
}

/**
 * Build the POST body for an item invoice: the grid, the two ledgers that
 * cannot be inferred, and a `gst` block WITHOUT lines.
 *
 * IT DELIBERATELY SENDS NO POSTINGS AND NO gst.lines. The server generates both
 * from the stock lines and refuses a body that also states them (422) rather
 * than merging — see internal/ledger/item_invoice.go. A client that "helpfully"
 * added its own amounts here would not be belt-and-braces; it would be a second
 * source of truth that the server has to pick between.
 *
 * Throws when nothing is keyed. An empty grid posted as an item invoice would
 * burn a real voucher number on a document that says nothing, so it is refused
 * in the form rather than sent.
 */
export function buildItemInvoiceRequest(
  inputs: ItemInvoiceInputs,
): VoucherRequest {
  const keyed = inputs.lines.filter(isKeyedLine);
  if (keyed.length === 0) {
    throw new Error(
      "An item invoice needs at least one line with an item and a quantity.",
    );
  }
  if (!inputs.itemLedgerId) {
    throw new Error("An item invoice needs a sales/purchase ledger to book the goods to.");
  }

  const inventory: InventoryLineRequest[] = keyed.map((l) => ({
    stock_item_id: l.stockItemId,
    godown_id: l.godownId,
    batch_id: l.batchId,
    direction: inputs.direction,
    qty: formatFixed4(parseFixed4(l.qty)),
    rate: formatFixed4(parseFixed4(l.rate)),
    discount_pct: formatFixed4(parseFixed4(l.discountPct)),
    // Sent, though the server derives it too — and the server REFUSES a
    // disagreement instead of correcting it, which is exactly why sending it is
    // worth doing: it turns a client-side arithmetic drift into a loud 422 on
    // the first discounted line rather than a silent paisa on every invoice.
    amount: lineAmount(l),
  }));

  // NO `lines` HERE, ON PURPOSE, and no reverse_charge: an RCM item invoice
  // needs self-assessed output AND input legs the generator does not derive, so
  // the server refuses one outright rather than filing an understated 3B.
  const gst: GSTRequest = {
    place_of_supply_state_code: inputs.placeOfSupplyStateCode,
    reverse_charge: false,
    cgst_ledger_id: inputs.outputTax.cgstId,
    sgst_ledger_id: inputs.outputTax.sgstId,
    igst_ledger_id: inputs.outputTax.igstId,
  };
  // Omitted (not "") when unknown, so the server's company.state_code default
  // fires explicitly — same idiom, and same reasoning, as buildGstVoucherRequest.
  if (inputs.supplierStateCode) gst.supplier_state_code = inputs.supplierStateCode;

  const req: VoucherRequest = {
    voucher_date: inputs.voucherDate,
    narration: inputs.narration,
    party: inputs.party,
    party_ledger_id: inputs.partyLedgerId,
    item_ledger_id: inputs.itemLedgerId,
    inventory,
    gst,
  };
  if (inputs.voucherNo) req.voucher_no = inputs.voucherNo;
  // ⛔ FROM `keyed`, THE SAME FILTERED ARRAY `inventory` WAS BUILT FROM, so the
  // 1-based line_no each link carries is by construction the position its line is
  // sent at. Passing `inputs.lines` here would be the off-by-one described on
  // ItemInvoiceLine.fulfilments: a blank row above a keyed one shifts every number.
  //
  // ⚠️ OMITTED WHEN EMPTY, not sent as []. Most invoices discharge no order, and an
  // empty array is a statement where absence is silence.
  const fulfilments = buildFulfilments(keyed);
  if (fulfilments.length > 0) req.fulfilments = fulfilments;
  return req;
}
