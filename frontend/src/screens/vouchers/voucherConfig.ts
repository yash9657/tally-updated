import type { CoredClient, VoucherRequest, VoucherResponse } from "../../api";
import type { VoucherType } from "../../keystroke/types";

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
  /** The natural Dr/Cr side of the single-entry primary ledger. */
  readonly primarySide: "DR" | "CR";
  /** Whether the form surfaces a GST split (Sales / Purchase). */
  readonly showGst: boolean;
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
    hint: "Purchase return / price correction raised on a supplier. Reverses the input credit claimed on those goods.",
    post: (c, id, req) => c.postDebitNoteVoucher(id, req),
    alter: (c, id, vid, req) => c.alterDebitNoteVoucher(id, vid, req),
  },
};

export function configFor(type: VoucherType): VoucherConfig {
  return VOUCHER_CONFIGS[type];
}
