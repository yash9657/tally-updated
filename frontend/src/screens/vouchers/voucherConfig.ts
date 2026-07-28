import type { CoredClient, VoucherRequest, VoucherResponse } from "../../api";
import type { EntryMode, VoucherType } from "../../keystroke/types";
import { isStockVoucherType } from "./stockVoucher";

/**
 * Per-voucher-type presentation + posting configuration. The six voucher
 * screens are a single config-driven component (RESEARCH.md sec 5): they differ
 * only in title, entry mode (single vs double), the party-field label, whether
 * they surface a GST split (Sales/Purchase), and which {@link CoredClient} post
 * method they call on Ctrl+A Accept.
 *
 *  - Single Entry (Payment / Receipt / Contra): one account on one side, the
 *    other side is a single party/bank/cash ledger. Tally's faster data-entry
 *    mode. The "primary" ledger sits on the natural side; the counter-lines sit
 *    opposite and must sum to it.
 *  - Double Entry (Journal / Sales / Purchase here in As-Voucher mode): explicit
 *    Dr/Cr lines that must balance.
 */

/** Which posting method on the client a voucher type maps to. */
export type PostFn = (
  client: CoredClient,
  companyID: string,
  req: VoucherRequest,
) => Promise<VoucherResponse>;

/**
 * Which alteration method on the client a voucher type maps to. Mirrors
 * {@link PostFn} but carries the existing logical voucher id — the alter path
 * NEVER mints a new id and resends the loaded voucher_no/voucher_date unchanged
 * (the numbering-preservation rule), so the screen passes the full restated
 * body it loaded plus the original id.
 */
export type AlterFn = (
  client: CoredClient,
  companyID: string,
  voucherID: string,
  req: VoucherRequest,
) => Promise<VoucherResponse>;

export interface VoucherConfig {
  readonly type: VoucherType;
  /** Voucher title shown in the form header (matches AppShell chrome casing). */
  readonly title: string;
  /** Entry style. Single-entry shows one primary ledger + counter lines. */
  readonly entry: "single" | "double";
  /** Label for the party/primary-ledger field (e.g. "Account", "Party A/c Name"). */
  readonly partyLabel: string;
  /**
   * The natural Dr/Cr side of this class's PRIMARY ledger.
   *
   *  - Single entry (Payment / Receipt / Contra): the side the one named
   *    account sits on; the particulars sit opposite and sum to it.
   *  - Invoice classes (Sales / Purchase): the side the GOODS and the TAX sit
   *    on; the party takes the opposite side for the gross. A sale credits
   *    sales + output tax and debits the party; a purchase does the exact
   *    reverse.
   *
   * Both orientations BALANCE, so a reversed voucher passes every totals check
   * the server makes — which is why this is one config value pinned by a
   * name-resolving test rather than an orientation written out per class.
   */
  readonly primarySide: "DR" | "CR";
  /** Whether the form surfaces a GST split (Sales / Purchase). */
  readonly showGst: boolean;
  /**
   * Whether Ctrl+H offers this class the ITEM INVOICE (a stock grid whose lines
   * generate the accounting and the GST).
   *
   * TRUE for Sales and Purchase only. Credit and Debit Notes are GST vouchers
   * with a stock effect in Tally, but their accounting REVERSES an invoice
   * (a credit note debits sales returns and CREDITS the party), and this
   * build's GST builder posts every showGst class sales-shaped. Offering an
   * item grid there would move stock correctly and book the reversal backwards,
   * which balances and is therefore invisible. Not built rather than built
   * wrong; the entry-mode list says so out loud.
   */
  readonly itemInvoice: boolean;
  /** Short guidance shown under the header (Tally's contextual hint). */
  readonly hint: string;
  /** The CoredClient method this voucher type posts through on Ctrl+A. */
  readonly post: PostFn;
  /**
   * The CoredClient method this voucher type ALTERS through on Ctrl+A when the
   * frame is in alteration mode (snapshot.mode === "alter"). The screen dispatches
   * to this instead of {@link post} when editing a loaded voucher.
   */
  readonly alter: AlterFn;
}

export const VOUCHER_CONFIGS: Readonly<Record<VoucherType, VoucherConfig>> = {
  contra: {
    type: "contra",
    title: "Contra",
    entry: "single",
    partyLabel: "Account",
    primarySide: "CR",
    showGst: false,
    itemInvoice: false,
    hint: "Cash ↔ Bank / Bank ↔ Bank transfers only.",
    post: (c, id, req) => c.postContraVoucher(id, req),
    alter: (c, id, vid, req) => c.alterContraVoucher(id, vid, req),
  },
  payment: {
    type: "payment",
    title: "Payment",
    entry: "single",
    partyLabel: "Account",
    primarySide: "CR", // money out: credit the cash/bank account paid from
    showGst: false,
    itemInvoice: false,
    hint: "Money out. Credit the bank/cash, debit the expense or party.",
    post: (c, id, req) => c.postPaymentVoucher(id, req),
    alter: (c, id, vid, req) => c.alterPaymentVoucher(id, vid, req),
  },
  receipt: {
    type: "receipt",
    title: "Receipt",
    entry: "single",
    partyLabel: "Account",
    primarySide: "DR", // money in: debit the cash/bank account received into
    showGst: false,
    itemInvoice: false,
    hint: "Money in. Debit the bank/cash, credit the party or income.",
    post: (c, id, req) => c.postReceiptVoucher(id, req),
    alter: (c, id, vid, req) => c.alterReceiptVoucher(id, vid, req),
  },
  journal: {
    type: "journal",
    title: "Journal",
    entry: "double",
    partyLabel: "Dr/Cr Ledger",
    primarySide: "DR",
    showGst: false,
    itemInvoice: false,
    hint: "Adjustments / non-cash entries. Debits must equal credits.",
    post: (c, id, req) => c.postJournalVoucher(id, req),
    alter: (c, id, vid, req) => c.alterJournalVoucher(id, vid, req),
  },
  sales: {
    type: "sales",
    title: "Sales",
    entry: "double",
    partyLabel: "Party A/c Name",
    primarySide: "CR",
    showGst: true,
    itemInvoice: true,
    hint: "Sales invoice. Party debited; sales + GST credited.",
    post: (c, id, req) => c.postSalesVoucher(id, req),
    alter: (c, id, vid, req) => c.alterSalesVoucher(id, vid, req),
  },
  purchase: {
    type: "purchase",
    title: "Purchase",
    entry: "double",
    partyLabel: "Party A/c Name",
    primarySide: "DR",
    showGst: true,
    itemInvoice: true,
    hint: "Purchase invoice. Party credited; purchase + GST debited.",
    post: (c, id, req) => c.postPurchaseVoucher(id, req),
    alter: (c, id, vid, req) => c.alterPurchaseVoucher(id, vid, req),
  },
  "credit-note": {
    type: "credit-note",
    title: "Credit Note",
    // Double entry: a return restates the sale, the output tax AND the party
    // in explicit lines, exactly as the invoice it reverses did.
    entry: "double",
    partyLabel: "Party A/c Name",
    primarySide: "CR",
    showGst: true,
    itemInvoice: false,
    hint: "Sales return / downward price correction. Its OWN number series — use this instead of altering the original invoice.",
    post: (c, id, req) => c.postCreditNoteVoucher(id, req),
    alter: (c, id, vid, req) => c.alterCreditNoteVoucher(id, vid, req),
  },
  "debit-note": {
    type: "debit-note",
    title: "Debit Note",
    entry: "double",
    partyLabel: "Party A/c Name",
    primarySide: "DR",
    showGst: true,
    itemInvoice: false,
    hint: "Purchase return / price correction raised on a supplier. Reverses the input credit claimed on those goods.",
    post: (c, id, req) => c.postDebitNoteVoucher(id, req),
    alter: (c, id, vid, req) => c.alterDebitNoteVoucher(id, vid, req),
  },

  // -------------------------------------------------------------------------
  // THE NON-ACCOUNTING STOCK VOUCHERS. They move goods and post ZERO
  // accounting lines, so `primarySide` and `partyLabel` describe the DOCUMENT,
  // not a posting: nothing on these screens resolves a ledger, and the server
  // refuses a body that states one.
  //
  // primarySide is set to the side the goods would eventually be booked on IF
  // an invoice followed — it is never read for these classes (StockVoucherScreen
  // builds the body through stockVoucher.ts, which sends no postings at all) and
  // is present only because VoucherConfig is one shape for every class. If it
  // ever starts being read here, that is the bug: a stock voucher with a
  // posting is a 422 by design.
  // -------------------------------------------------------------------------
  "delivery-note": {
    type: "delivery-note",
    title: "Delivery Note",
    entry: "double",
    partyLabel: "Party A/c Name",
    primarySide: "CR",
    showGst: false,
    itemInvoice: false,
    hint: "Goods OUT against a customer, before or without the invoice. Stock moves; NOTHING is posted to the ledger — the invoice that follows carries the revenue, the tax and the bill.",
    post: (c, id, req) => c.postDeliveryNoteVoucher(id, req),
    alter: (c, id, vid, req) => c.alterDeliveryNoteVoucher(id, vid, req),
  },
  "receipt-note": {
    type: "receipt-note",
    title: "Receipt Note",
    entry: "double",
    partyLabel: "Party A/c Name",
    primarySide: "DR",
    showGst: false,
    itemInvoice: false,
    hint: "Goods IN from a supplier, before the bill. Stock moves; NOTHING is posted to the ledger — the purchase invoice that follows books the cost and the input credit.",
    post: (c, id, req) => c.postReceiptNoteVoucher(id, req),
    alter: (c, id, vid, req) => c.alterReceiptNoteVoucher(id, vid, req),
  },
  "stock-journal": {
    type: "stock-journal",
    title: "Stock Journal",
    entry: "double",
    partyLabel: "Reference",
    primarySide: "DR",
    showGst: false,
    itemInvoice: false,
    hint: "A transfer: goods leave one godown and arrive in another on the SAME voucher. The company still owns them, so no value moves and nothing is posted.",
    post: (c, id, req) => c.postStockJournalVoucher(id, req),
    alter: (c, id, vid, req) => c.alterStockJournalVoucher(id, vid, req),
  },
  "physical-stock": {
    type: "physical-stock",
    title: "Physical Stock",
    entry: "double",
    partyLabel: "Counted by",
    primarySide: "DR",
    showGst: false,
    itemInvoice: false,
    hint: "State what was COUNTED on the shelf; the SERVER derives the correction against the books. A supervisor's key — the seeded Data Entry level is refused this one (migration 0031).",
    post: (c, id, req) => c.postPhysicalStockVoucher(id, req),
    alter: (c, id, vid, req) => c.alterPhysicalStockVoucher(id, vid, req),
  },
};

export function configFor(type: VoucherType): VoucherConfig {
  return VOUCHER_CONFIGS[type];
}


/**
 * The entry modes Ctrl+H offers for a voucher class, in Tally's own order.
 *
 * RESEARCH.md §"Voucher entry": Ctrl+H (Change Mode) toggles Single Entry
 * (Payment / Receipt / Contra — one account on one side, multiple on the other)
 * against Double Entry (classic Dr/Cr). Sales and Purchase instead toggle the
 * INVOICE modes: Item Invoice (goods — item, godown, quantity, rate — with the
 * accounting and the GST derived from them) and Accounting Invoice (a taxable
 * value keyed straight against a Sales/Purchases ledger, no stock).
 *
 * A mode this edition does not implement is still LISTED, with an honest
 * "not built" marker, on the same principle as the keymap's `unavailable`
 * actions: a key that silently does nothing is worse than one that says why.
 */
export interface EntryModeOption {
  label: string;
  /** The FrameConfig.entryMode this option selects, or null when not built. */
  mode: EntryMode | null;
}

export function entryModesFor(type: VoucherType): EntryModeOption[] {
  const cfg = configFor(type);
  // A stock voucher has ONE form and no money side. Offering "Single Entry" /
  // "Double Entry" here would name two modes that both imply ledger lines this
  // class is REFUSED (validateStockVoucher rejects any posting outright), so
  // Ctrl+H states the one form that exists instead of listing choices that
  // would be a 422 if they did anything.
  if (isStockVoucherType(type)) {
    return [{ label: "Stock Entry", mode: "stock" }];
  }
  if (cfg.showGst) {
    // Tally's invoice-mode family, in Tally's order. "As Voucher" (keying an
    // invoice as raw Dr/Cr lines) is genuinely not built.
    return [
      cfg.itemInvoice
        ? { label: "Item Invoice", mode: "item-invoice" }
        : // Credit / Debit Notes: see VoucherConfig.itemInvoice — the grid
          // would move stock correctly and book the reversal backwards.
          { label: "Item Invoice — not built", mode: null },
      { label: "Accounting Invoice", mode: "accounting-invoice" },
      { label: "As Voucher — not built", mode: null },
    ];
  }
  if (cfg.entry === "double") {
    // Journal is double-entry only in Tally too: there is no single side to
    // infer when both sides are the point.
    return [{ label: "Double Entry", mode: "double" }];
  }
  return [
    { label: "Single Entry", mode: "single" },
    { label: "Double Entry", mode: "double" },
  ];
}

/**
 * The entry mode in force for a frame: the user's Ctrl+H choice if they made
 * one, else the class default. Single-capable classes default to SINGLE, which
 * is what a Tally operator expects when they press F5; the GST classes default
 * to the ACCOUNTING invoice, which is the form this build has always shown, so
 * shipping the item grid does not silently change what F8 opens into.
 *
 * It also CLAMPS: a mode that this class does not offer is ignored rather than
 * honoured. Nothing in the UI can put a Payment into "item-invoice", but the
 * mode is remembered per voucher type and read back from a frame, so a stale or
 * hand-constructed config must not be able to render a stock grid on a class
 * whose post path knows nothing about stock.
 */
export function effectiveEntryMode(
  type: VoucherType,
  chosen: EntryMode | undefined,
): EntryMode {
  const cfg = configFor(type);
  // A stock class is ALWAYS "stock", whatever a stale or hand-constructed frame
  // config says. This is the clamp that matters most: the entry mode is
  // remembered per voucher type and read back from a frame, so without it a
  // Ctrl+H choice made on Sales could put a Delivery Note into "item-invoice"
  // and hand it to a build path that sends a party ledger and a GST block —
  // both of which this class refuses.
  if (isStockVoucherType(type)) return "stock";
  if (cfg.showGst) {
    if (chosen === "item-invoice" && cfg.itemInvoice) return "item-invoice";
    return "accounting-invoice";
  }
  if (cfg.entry === "double") return "double"; // never offer single where Tally does not
  if (chosen === "single" || chosen === "double") return chosen;
  return cfg.entry;
}
