import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { SelectionList } from "./SelectionList";
import type { NavSnapshot } from "../keystroke/navMachine";
import type { VoucherType } from "../keystroke/types";
import {
  useClient,
  useCompanyID,
  useHomeStateCode,
} from "../app/CompanyContext";
import { useRegisterScreenAction } from "../app/ScreenActions";
import { usePrintAction } from "./usePrint";
import { useLoadedVoucher } from "../app/LoadedVoucher";
import { useLoadedDraft } from "../app/LoadedDraft";
import {
  acceptVoucher,
  deleteLoadedVoucher,
  type VoucherEditContext,
} from "./vouchers/editActions";
import {
  configFor,
  effectiveEntryMode,
  entryModesFor,
  type VoucherConfig,
} from "./vouchers/voucherConfig";
import {
  dayBookRowKey,
  voucherClassForType,
  voucherTypeForClass,
} from "./vouchers/voucherClass";
import {
  emptyGstFormState,
  ensureLedger,
  ensureGstTaxLedgers,
  gstOrientationFor,
  gstTaxLedgerNames,
  buildGstVoucherRequest,
  gstVoucherPartyTotal,
  chargeLinesRefusal,
  keyedChargeLines,
  type GstChargeLine,
  type GstFormState,
} from "./vouchers/gstVoucher";
import { addMoney, normalizeMoney } from "./vouchers/money";
import { FulfilmentPicker } from "./vouchers/FulfilmentPicker";
import { fulfilsDirection } from "./vouchers/fulfilment";
import { CostAllocationPicker } from "./vouchers/CostAllocationPicker";
import { BillAllocationPicker } from "./vouchers/BillAllocationPicker";
import { formatFixed4, itemInvoiceGross, parseFixed4 } from "./vouchers/itemInvoice";
import {
  billRefsFromView,
  billRefusal,
  buildBillAllocations,
  defaultNewRef,
  retrackAutoRef,
  type BillBearingLeg,
  type DraftBillRef,
} from "./vouchers/billAllocation";
import {
  allocationRefusal,
  allocationsFromView,
  buildCostAllocations,
  type DraftAllocation,
} from "./vouchers/costAllocation";
import { DoubleEntryPanel } from "./vouchers/DoubleEntryPanel";
import { TaxDeductionPanel } from "./vouchers/TaxDeductionPanel";
import {
  buildTCSRequest,
  buildTDSRequest,
  emptyTaxDeductionForm,
  taxDeductionFromView,
  taxDeductionRefusal,
  taxLegRefusal,
  type TaxDeductionForm,
} from "./vouchers/taxDeduction";
import {
  doubleEntryLinesFromView,
  doubleEntryRefusal,
  emptyDoubleEntryLine,
  keyedLines,
  type DoubleEntryLine,
} from "./vouchers/doubleEntry";
import {
  buildItemInvoiceRequest,
  deriveItemInvoiceTax,
  emptyItemLine,
  invoiceTaxable,
  isKeyedLine,
  lineAmount,
  type ItemInvoiceLine,
} from "./vouchers/itemInvoice";
import {
  acceptEditedPurchaseDraft,
  canRebuildDraft,
  voucherAcceptFailure,
} from "./vouchers/draftAccept";
import { StockVoucherScreen } from "./vouchers/StockVoucherScreen";
import { defaultGodownOf, isStockVoucherType } from "./vouchers/stockVoucher";
import { acceptPurchaseDraft, ApiError } from "../api";
import type {
  DayBookEntry,
  FieldConfidence,
  GodownListItem,
  LedgerListItem,
  PostingRequest,
  PurchaseVoucherDraft,
  StockItemListItem,
  VoucherClass,
  VoucherDraftBody,
  VoucherRequest,
  VoucherResponse,
  VoucherView,
} from "../api";
import type { EntryMode } from "../keystroke/types";
import type { CoredClient } from "../api";

/**
 * The account groups a PARTY field offers. A voucher's party is a debtor, a
 * creditor, or the cash/bank account being moved — never an income or tax
 * ledger, and offering those would invite an unbalanced or nonsensical entry.
 */
const PARTY_GROUPS: readonly string[] = [
  "Sundry Debtors",
  "Sundry Creditors",
  "Cash-in-Hand",
  "Bank Accounts",
  "Bank OD A/c",
];

/**
 * One selectable party, resolved from the company's REAL chart of accounts.
 *
 * This replaced a hardcoded sample list ("Acme Traders", "Bharat Stores",
 * "Chandra & Co") that existed in no seeded company — so the picker offered
 * names that were not accounts, and accepting one CREATED a ledger as a side
 * effect of keying a voucher. Two consequences, both bad: an operator could
 * silently mint a near-duplicate of a real party by picking the sample with the
 * similar name, and a Data Entry operator — who may key vouchers but may NOT
 * create masters — got a 403 from the ledger POST rather than from anything
 * they meant to do.
 *
 * Carrying the real `id` also means accepting a voucher for an existing party
 * performs NO write to create it.
 */
interface PartyOption {
  id: string;
  name: string;
  stateCode: string;
}

// ⛔ `GST_INCOME_LEDGER` LIVED HERE AND IS GONE. It mapped only `sales` and
// `purchase` and fell back to Domestic Sales for anything else, which is how a
// Debit Note came to book a purchase return to REVENUE. Its replacement is
// GST_ORIENTATION in vouchers/gstVoucher.ts, which carries the base ledger
// alongside the three posting DIRECTIONS and the tax kind — because those four
// facts are per-class and were being answered by four separate hard-codings,
// three of which were the sales answer.

/** Today's date as YYYY-MM-DD (server default if the user leaves date empty). */
function today(): string {
  return new Date().toISOString().slice(0, 10);
}

/**
 * Map a fetched VoucherView's postings into request postings carrying the REAL
 * ledger_id uuids. This REPLACES the retired day-book `postingsFromEntry`, which
 * stuffed the ledger NAME into ledger_id (the documented name-as-uuid bug). The
 * GET endpoint returns canonical "D"/"C" dr_cr which we pass through verbatim.
 */
function postingsFromView(view: VoucherView): PostingRequest[] {
  return view.postings.map((p) => ({
    ledger_id: p.ledger_id,
    dr_cr: p.dr_cr,
    amount: p.amount,
  }));
}

/** The form state a voucher frame edits. */
interface VoucherForm {
  date: string;
  party: string;
  partyLedgerId: string;
  /**
   * SINGLE ENTRY ONLY: the ONE account (bank / cash) the money moved through —
   * what the operator keys in the "Account" field on F5 / F6 / F4.
   *
   * ⛔⛔ A SEPARATE FIELD FROM `party`, AND SPLITTING THEM IS THE WHOLE OF THE
   * D59 FIX. The two were one field, so a single-entry voucher shipped the BANK
   * as `party` / `party_ledger_id` — and printingdb builds the Party block from
   * `party_ledger_id`, so every printed receipt said "Received From: HDFC Bank"
   * and every payment "Paid To: HDFC Bank". The bank is not the counterparty; it
   * is the account the counterparty paid into. The counterparty is a PARTICULAR
   * (see {@link singleEntryCounterparty}), and the request now carries that.
   *
   * ⛔⛔ AND THE SPLIT IS NOT COSMETIC — COLLAPSING IT AGAIN DESTROYS VOUCHERS.
   * If the request's `party` names the counterparty while the form's Account
   * field reads `party` back on an ALTER, then hydrating a receipt puts ACME in
   * the Account field, and the next Ctrl+A posts `Dr Acme (total) / Cr Acme` —
   * the bank leg vanishes from a voucher that still BALANCES, so no total, no
   * trigger and no server check disagrees with it. The account is therefore
   * hydrated from the voucher's OWN primary-side posting
   * ({@link accountLegFromView}), never from the stored party label.
   *
   * ⚠️ Ctrl+H STAYS LOSSLESS, exactly as `lines` / `drCrLines` do: the two modes
   * keep their own field, so switching to double entry and back returns the form
   * as it was. A Journal or an invoice never reads this.
   */
  account: string;
  /** The Account field's resolved uuid, when it was PICKED rather than typed. */
  accountLedgerId: string;
  /**
   * ACCOUNTING-MODE GST ONLY: the income/expense head a HYDRATED voucher is
   * already booked to, so an alter preserves it instead of re-pointing the
   * voucher at the orientation's canonical base. "" on a fresh create, and ""
   * when the voucher has no single identifiable base leg — see
   * {@link baseLegFromView}, which explains the data loss this prevents.
   */
  baseLedgerId: string;
  /** The chosen party's GST state code (seeds place of supply). */
  partyStateCode: string;
  narration: string;
  /**
   * The full restated postings (real ledger_id uuids). For GST vouchers these
   * are rebuilt from the GST slice on accept; for hydrated alters they seed the
   * non-GST send path verbatim.
   */
  postings: PostingRequest[];
  /** Display number: empty (=> "auto") on a fresh create, loaded on alter. */
  voucherNo: string;
  /** GST inputs (used only when cfg.showGst). */
  gst: GstFormState;
  /**
   * HOW MANY `voucher_gst_line` ROWS THE LOADED VOUCHER HAD. 0 on a fresh create.
   *
   * ⛔ IT EXISTS TO MAKE A REFUSAL POSSIBLE, not to be displayed. The
   * accounting-mode GST form holds ONE taxable value, ONE rate and ONE HSN, so
   * it can only faithfully restate a ONE-LINE voucher. A voucher with two rates
   * — importable from Tally XML, and generated by the item path — would be
   * rebuilt by this form as a single line at whichever rate happened to be
   * hydrated, silently DELETING the others and re-deriving the whole tax.
   *
   * That is the same shape as the defect this field was added to fix, one level
   * along: not "the rate is lost" but "the rate of every line after the first
   * is lost". `buildGstRequest` refuses instead — see the guard there.
   */
  gstLineCount: number;
  /** The taxable base (income posting amount) for a GST voucher, fixed-4 string. */
  taxableValue: string;
  /**
   * SINGLE-ENTRY particulars: the ledgers on the MANY side, with amounts.
   *
   * In single entry the user names one account (the bank/cash) and then lists
   * what it paid for / was received against; the single side's amount is the
   * SUM and is never typed. Empty rows are ignored on accept, so the form can
   * always offer one more line without forcing the user to fill it.
   */
  lines: {
    ledger: string;
    amount: string;
    /**
     * HOW THIS PARTICULAR'S POSTING IS SPLIT ACROSS COST CENTRES (T2).
     *
     * ⛔ IT LIVES ON THE ROW so that the 0-based `posting_seq` it is sent with is
     * derived from the row's own position at the moment the postings array is
     * assembled — see buildCostAllocations, which takes the primary-leg offset
     * explicitly. Carrying the ordinal in a parallel structure is how a split gets
     * attributed to the bank leg instead of the expense.
     */
    allocations: readonly DraftAllocation[];
    /**
     * WHICH BILLS THIS PARTICULAR SETTLES (bill-wise references).
     *
     * ⛔ IT LIVES ON THE ROW, and for a sharper reason than the cost splits do.
     * In SINGLE ENTRY the party field is the BANK — `postings[0]` — and the
     * customer or supplier whose bill is being settled is one of these
     * particulars. So the bill-bearing leg is a ROW, and attaching a reference
     * anywhere else would put a receivable settlement on the bank account.
     */
    billRefs: readonly DraftBillRef[];
  }[];
  /**
   * DOUBLE-ENTRY Dr/Cr lines — the Journal grid (F7), and Payment / Receipt /
   * Contra switched to Double Entry with Ctrl+H.
   *
   * ⛔ A SEPARATE ARRAY FROM `lines`, DELIBERATELY, AND THE REASON IS THE WHOLE
   * DESIGN. Single entry INFERS the primary leg and puts the SUM on it; double
   * entry STATES both sides. Sharing one array would mean a Ctrl+H between the
   * two modes silently reinterprets what the operator keyed — the same rows
   * meaning "the many side of an inferred entry" in one mode and "the complete
   * entry" in the other, with no way to tell which was intended.
   *
   * ⚠️ Keeping them separate also makes Ctrl+H LOSSLESS in both directions:
   * each mode keeps its own lines, so switching to look at the other and back
   * returns the form exactly as it was. Journal itself never switches
   * (`entryModesFor` offers it Double Entry only), so this matters for the
   * three single-entry classes.
   */
  drCrLines: DoubleEntryLine[];
  /**
   * THE STATUTORY TDS / TCS BLOCK (register row D3).
   *
   * ⛔ IT LIVES ON THE DOUBLE-ENTRY FORM AND NOWHERE ELSE, and that is an
   * ACCOUNTING fact, not a layout choice. Withholding tax means the payment
   * splits three ways — Dr the expense in full, Cr the bank NET, Cr the TDS
   * liability — and single entry cannot express it: it puts the SUM of the
   * particulars on ONE account, so the withheld portion has nowhere to go but a
   * negative particular. Ctrl+H reaches this form from F5/F6/F4, and F7 is it.
   *
   * ⚠️ IT IS NOT HYDRATED ON AN ALTER, because the voucher READ path does not
   * serve it — registered as D88. See the notice the panel renders.
   */
  tax: TaxDeductionForm;
  /**
   * WHICH BILL THIS INVOICE OPENS — the PARTY leg's references, used by the GST
   * accounting and item-invoice paths where the bill-bearing leg IS
   * `partyLedgerId` (the Sundry Debtor or Creditor) rather than a particular.
   */
  billRefs: readonly DraftBillRef[];
  /**
   * ITEM-INVOICE grid rows (Ctrl+H > Item Invoice on Sales/Purchase). Empty
   * rows are ignored on accept, so the form can always offer one more line.
   */
  items: ItemInvoiceLine[];
  /**
   * TRUE when the LOADED voucher's credit legs could not be told apart — see
   * {@link gstLegsFromView}. It is a fact about what was hydrated, not a
   * validation result, which is why it lives on the form rather than being
   * recomputed in the refusal: the view is gone by the time Ctrl+A runs.
   */
  gstLegsAmbiguous: boolean;
}

function emptyForm(): VoucherForm {
  return {
    date: today(),
    party: "",
    partyLedgerId: "",
    account: "",
    accountLedgerId: "",
    // "" => a fresh voucher is booked to the orientation's canonical base.
    baseLedgerId: "",
    partyStateCode: "",
    narration: "",
    postings: [],
    voucherNo: "",
    gst: emptyGstFormState(),
    // A fresh voucher has no stored GST lines. See VoucherForm.gstLineCount.
    gstLineCount: 0,
    taxableValue: "0.0000",
    lines: [{ ledger: "", amount: "", allocations: [], billRefs: [] }],
    drCrLines: [emptyDoubleEntryLine()],
    billRefs: [],
    items: [emptyItemLine()],
    tax: emptyTaxDeductionForm(),
    // A fresh voucher was hydrated from nothing, so nothing was ambiguous.
    gstLegsAmbiguous: false,
  };
}

/**
 * Seed the form from a fetched VoucherView (the GET hydration path). All alter
 * seeding flows through here so buildGstVoucherRequest / the verbatim send path
 * carry REAL posting uuids and persisted GST attrs (place_of_supply, taxable,
 * reverse_charge), and the loaded voucher_no/date preserve numbering.
 */
function formFromView(view: VoucherView): VoucherForm {
  const account = accountLegFromView(view);
  const legs = gstLegsFromView(view);
  // ⛔ THE NAME COMES FROM THIS VOUCHER'S OWN POSTINGS, not from the chart. The
  // party of a deduction is usually a leg of the entry, and resolving it here
  // keeps `formFromView` pure — the chart is loaded asynchronously and is not
  // available at hydration time. When it is NOT a leg (a deductee paid through
  // a control account) the name is left blank and the panel shows the block with
  // an empty party, which the refusal then names rather than posting silently.
  const nameByID = (id: string): string =>
    view.postings.find((p) => p.ledger_id === id)?.ledger ?? "";
  return {
    gstLegsAmbiguous: legs.ambiguous,
    date: view.voucher_date,
    party: view.party,
    partyLedgerId: view.party_ledger_id,
    // ⛔⛔ THE ACCOUNT COMES FROM THE POSTINGS, NOT FROM THE STORED `party`.
    // See VoucherForm.account: reading it back from the party label is what
    // would post `Dr Acme / Cr Acme` and lose the bank leg on the first alter of
    // a receipt whose party is (correctly) the counterparty. "" when the voucher
    // has no single primary leg — the single-entry form then refuses rather than
    // silently merging two bank legs into one, which is what it did before.
    account: account?.name ?? "",
    accountLedgerId: account?.id ?? "",
    // ⛔ THE HEAD THIS VOUCHER IS BOOKED TO, carried so an alter cannot re-point
    // it to the orientation's canonical base. See baseLegFromView.
    baseLedgerId: baseLegFromView(view),
    partyStateCode: "",
    narration: view.narration,
    postings: postingsFromView(view),
    voucherNo: view.voucher_no,
    // ⛔⛔ THE RATE AND THE HSN COME FROM `gst_lines`, AND OMITTING THEM WAS
    // LIVE TAX LOSS ON EVERY ACCOUNTING-MODE GST ALTERATION.
    //
    // `taxable_value`, `cgst`, `sgst` and `igst` on the view are what the
    // derivation PRODUCED. The rate it was derived AT lives only in
    // `voucher_gst_line`, and no rate is recoverable from the amounts (an
    // exempt line and a nil-rated line both produce zero tax). So while these
    // two read `""`, the ordinary round trip — open an invoice, fix the
    // narration, save — re-derived the tax AT 0%: the party leg fell by the
    // whole tax (11,800 became 10,000), the version's GST columns went to zero,
    // and the supply left both the GSTR-1 b2b section and the table-12 HSN
    // summary. Nothing refused it, because a supply with no tax is legal.
    //
    // ⚠️ AN ITEM INVOICE WAS NEVER EXPOSED TO THIS: its lines are generated
    // server-side from the stock master, so its rate is re-derived from
    // something the round trip cannot lose. It is the ACCOUNTING-mode voucher —
    // a taxable value and a typed rate, no stock — where the rate exists
    // nowhere but the request that carried it.
    gst: {
      placeOfSupply: view.place_of_supply,
      gstRatePct: view.gst_lines?.[0]?.gst_rate_pct ?? "",
      reverseCharge: view.reverse_charge,
      hsn: view.gst_lines?.[0]?.hsn ?? "",
      // ⛔ RE-STATED, NOT REBUILT. An alter POSTs the whole voucher, so a charge
      // this form does not carry is a charge DELETED — the buyer's total falls
      // by it and, on a TCS invoice, the collected tax stops being posted while
      // the return still says it was collected. See gstLegsFromView.
      charges: legs.charges,
    },
    // ⚠️ `?? 0` MEANS "THIS SERVER DOES NOT TELL ME", NOT "THIS VOUCHER HAS
    // NONE", and the two must not be conflated in the refusing direction: a
    // voucher fetched from a build predating the key has no `gst_lines` at all,
    // and treating that as a multi-line voucher would refuse every alter.
    gstLineCount: view.gst_lines?.length ?? 0,
    taxableValue: view.taxable_value,
    // Seed the single-entry particulars from the loaded postings, so ALTERING a
    // payment opens a form the user can edit rather than an empty one over a
    // voucher that already has lines. Which side is "many" depends on the class
    // (a payment credits one bank and debits many expenses), so the rows are
    // whichever side is NOT the class's primary side.
    // The PARTY leg's own references (the raise path). `linesFromPostings`
    // claims the particulars' ones; this is the Sundry Debtor/Creditor leg that
    // a Sales or Purchase invoice opens its bill on.
    billRefs: billRefsFromView(view.bill_allocations ?? [], view.party_ledger_id),
    lines: linesFromPostings(view),
    // ⛔⛔ THE DOUBLE-ENTRY GRID, AND OMITTING THIS WOULD DESTROY THE WHOLE
    // VOUCHER — not a detail of it. `buildDoubleEntryRequest` now OWNS the
    // double-entry send path and builds `postings` from these rows and nothing
    // else, so an empty hydration would make "open a Journal, fix the
    // narration, save" post a version with NO POSTINGS: the entry silently
    // reverses out of every balance, and zero postings is a legal state so
    // nothing anywhere refuses it. See doubleEntry.ts's header, and
    // `TestAnAlteredJournalRebuiltFromTheGetKeepsItsPostings` with its control.
    //
    // ⚠️ BOTH SIDES, in the response's own order — unlike `linesFromPostings`,
    // which keeps only the many side because single entry synthesises the
    // other. Here there is nothing to synthesise.
    drCrLines: doubleEntryLinesFromView(
      view.postings,
      (seq) => allocationsFromView(view.cost_allocations ?? [], seq),
      (ledgerID) => billRefsFromView(view.bill_allocations ?? [], ledgerID),
    ),
    // THE ALTER ROUND TRIP FOR STOCK. The stored grid carries the real item /
    // godown / batch uuids; seeding from it (rather than re-resolving anything
    // by name) is what lets an edit of the narration re-post the SAME movements
    // instead of writing the voucher's inventory off to nothing.
    items: itemsFromView(view),
    // ⛔ RE-STATED FROM THE STORED ROW (register row D88, CLOSED). `tds_deduction`
    // is deleted and re-derived from the body on EVERY alter, so a form that
    // could not restate the block sent none and the server obliged — the
    // ordinary round trip (open a voucher that withheld tax, fix the narration,
    // save) DELETED the deduction. Nothing an operator looks at moved: the
    // withheld credit is a posting and the postings are restated, so only the
    // return changed, and the challan that paid it kept an allocation matching
    // no row. The voucher GET now serves the block; this is the half that sends
    // it back.
    //
    // ⚠️ AT MOST ONE OF THE TWO. A voucher cannot both withhold and collect —
    // the classes that offer each are disjoint — so whichever the server served
    // is the one to restate.
    tax: taxDeductionFromView(view.tds ?? view.tcs, view.postings, nameByID),
  };
}

/**
 * The stored item grid as editable rows. gstRatePct / hsn / unit are NOT stored
 * on a movement (the server reads them from the item master), so they are left
 * empty here and re-joined from the fetched masters when the grid renders — see
 * `joinMasters`. A voucher with no stock yields one blank row.
 */
function itemsFromView(view: VoucherView): ItemInvoiceLine[] {
  const rows = (view.inventory ?? []).map((l) => ({
    stockItemId: l.stock_item_id,
    itemName: l.item,
    godownId: l.godown_id,
    batchId: l.batch_id,
    qty: l.qty,
    rate: l.rate,
    discountPct: l.discount_pct,
    gstRatePct: "",
    hsn: "",
    unit: "",
    // ⛔⛔ THE ORDER LINKS, RE-HYDRATED — AND OMITTING THIS IS DATA LOSS, NOT A
    // MISSING FEATURE. An ALTER re-states the links (order_fulfilment keys on
    // voucher_version_id), so a form that rebuilt this grid without them would send
    // none and the next save of an unrelated field — a typo in the narration — would
    // silently RE-OPEN an order this voucher already delivered. Nothing refuses it:
    // zero links is a legal state, so no trigger fires and the only trace is an order
    // book reporting a commitment the company has met.
    //
    // Matched on the 1-BASED `line_no` against this line's own 1-based position.
    // `l.line_no` is the STORED number, so the match is against what the server
    // actually keyed rather than against this array's index — which matters because
    // the two can only agree if the projection is ordered, and relying on that
    // silently is how an off-by-one hides.
    fulfilments: (view.fulfilments ?? [])
      .filter((f) => f.line_no === l.line_no)
      .map((f) => ({
        orderLineId: f.order_line_id,
        qty: f.qty,
        orderNo: f.order_no,
        itemName: f.stock_item,
        // What the ORDER had outstanding is not on this body and must not be
        // guessed: it is derived across every current voucher version, so a figure
        // invented here would be a claim about other vouchers. The picker shows "" and
        // the operator re-picks if they want to change the link.
        outstandingAtPick: "",
      })),
  }));
  return rows.length > 0 ? rows : [emptyItemLine()];
}

/**
 * The MANY side of a loaded voucher, as single-entry particulars. Rows carry
 * the ledger NAME because that is what the picker selects on; the real uuid is
 * resolved on accept exactly as it is for a fresh entry.
 */
function linesFromPostings(
  view: VoucherView,
): {
  ledger: string;
  amount: string;
  allocations: readonly DraftAllocation[];
  billRefs: readonly DraftBillRef[];
}[] {
  const cfg = configFor(view.class.toLowerCase() as VoucherType);
  const manySide = cfg.primarySide === "DR" ? "CR" : "DR";
  // ⚠️ A ledger posted on more than one line would otherwise be given its
  // references TWICE — once per row — and the rebuilt voucher would claim double
  // what it settles. The references belong to the LEDGER, not to the row, so the
  // first row that carries it takes them.
  const claimed = new Set<string>();
  const rows = view.postings
    // ⛔ THE INDEX IS CAPTURED BEFORE THE FILTER, and that is load-bearing.
    // `cost_allocations[].posting_seq` indexes the response's FULL `postings` array,
    // and this function keeps only the many side — so matching on the position in the
    // FILTERED list would attach a split to whichever posting happened to land at
    // that index. Usually that is the row above the right one, and the split would
    // land on a real ledger with a plausible amount.
    .map((p, seq) => ({ p, seq }))
    .filter(({ p }) => p.dr_cr.toUpperCase().startsWith(manySide[0]!))
    .map(({ p, seq }) => ({
      ledger: p.ledger,
      amount: p.amount,
      // ⛔⛔ RE-HYDRATED, AND OMITTING THIS IS DATA LOSS. An ALTER RE-STATES: a form
      // that rebuilt this voucher without its splits would send none, and the
      // ordinary round trip (open a pay run, fix the narration, save) would silently
      // DESTROY the departmental split of a cost that is still in the Profit & Loss.
      // Nothing refuses it — zero allocations is a legal state, so the sum trigger
      // has nothing to fire on and the only trace is a Cost Centre Summary that
      // quietly stops adding up.
      //
      // ⚠️ The seq is used ONLY to match here. buildCostAllocations RECOMPUTES it
      // from the rebuilt array, because the response orders postings by
      // (dr_cr DESC, amount DESC) and the request will not.
      allocations: allocationsFromView(view.cost_allocations ?? [], seq),
      // ⛔⛔ RE-HYDRATED FOR THE SAME REASON, AND THIS ONE WAS LIVE.
      // `bill_allocation` is keyed on voucher_version_id, so an ALTER RE-STATES.
      // `voucherFullJSON` has always RETURNED these rows — the read-back audit
      // ticked them off — but no form ever sent them back, so editing the
      // narration of any voucher carrying a bill reference DELETED it: the
      // invoice stayed in Sundry Debtors and vanished from Outstandings. Emitting
      // on the GET is necessary and NOT sufficient; the round trip closes here.
      billRefs: (() => {
        if (claimed.has(p.ledger_id)) return [];
        claimed.add(p.ledger_id);
        return billRefsFromView(view.bill_allocations ?? [], p.ledger_id);
      })(),
    }));
  return rows.length > 0
    ? rows
    : [{ ledger: "", amount: "", allocations: [], billRefs: [] }];
}

/**
 * THE ONE ACCOUNT a single-entry voucher moved money through, read from the
 * voucher's OWN postings — the bank on a receipt or payment, the source account
 * on a contra.
 *
 * ⛔⛔ THIS EXISTS BECAUSE `view.party` IS NO LONGER THE ACCOUNT. Since D59 the
 * request's `party` / `party_ledger_id` name the COUNTERPARTY (the customer who
 * paid, the supplier who was paid), because that is what the printed document
 * states and what `printingdb` reads. So the Account field can no longer be
 * hydrated from the party label: doing that would put the counterparty in the
 * Account field, and the next Ctrl+A would post the counterparty on BOTH sides
 * and drop the bank from the voucher entirely. It balances, so nothing refuses
 * it — this repo's signature failure shape (RULE 4).
 *
 * ⚠️ NULL UNLESS THERE IS EXACTLY ONE PRIMARY-SIDE POSTING, and that is a
 * refusal in disguise rather than a missing case. A payment keyed in DOUBLE
 * entry may credit two banks; single entry cannot represent it (it puts the SUM
 * on one account), and the old form silently MERGED the two into whichever
 * ledger the party label named. Returning null blanks the Account field, and
 * {@link singleEntryRefusal} then refuses the save with a message the operator
 * can act on.
 *
 * ⚠️ Only for classes whose form IS single entry (`cfg.entry === "single"`:
 * Payment / Receipt / Contra). On a sale the sole primary-side posting is the
 * income ledger when the invoice bears no tax, and hydrating THAT into the party
 * field would rewrite the invoice's customer to "Domestic Sales".
 */
function accountLegFromView(view: VoucherView): { name: string; id: string } | null {
  const cfg = configFor(view.class.toLowerCase() as VoucherType);
  if (cfg === undefined || cfg.entry !== "single") return null;
  const legs = view.postings.filter((p) =>
    p.dr_cr.toUpperCase().startsWith(cfg.primarySide[0]!),
  );
  if (legs.length !== 1) return null;
  const leg = legs[0]!;
  return { name: leg.ledger, id: leg.ledger_id };
}

/**
 * THE INCOME / EXPENSE HEAD A STORED GST VOUCHER IS ACTUALLY BOOKED TO.
 *
 * ⛔⛔ THE DEFECT THIS CLOSES IS DATA LOSS ON EVERY ALTER, AND IT WAS DRIVEN.
 *
 * `buildGstRequest` always `ensureLedger`d the ORIENTATION'S CANONICAL base —
 * "Domestic Sales" for a sale, "Purchases" for a purchase — so opening ANY
 * accounting-mode GST voucher booked to some other head and pressing Ctrl+A
 * silently MOVED it. Driven on the demo books:
 *
 *     version 1:  Dr Raw Material Purchases  30000.0000
 *     version 2:  Dr Purchases               30000.0000     <- nothing was edited
 *
 * A "Purchases" ledger was created to receive it. The voucher still balances,
 * the tax is unchanged, every trigger and every totals check passes — and the
 * company's expense analysis has quietly changed, on a voucher somebody merely
 * LOOKED at. That is worse than an orphan master: an orphan is visible in the
 * chart of accounts, this is a restated P&L.
 *
 * THE RULE, and it refuses to guess for the same reason
 * {@link singleEntryCounterparty} does:
 *
 *  1. Take the legs on the orientation's BASE SIDE.
 *  2. Drop the party leg (by uuid) and the three tax legs (by NAME — they are
 *     the names `ensureGstTaxLedgers` creates, and the only three this form can
 *     ever have produced on that side).
 *  3. Exactly ONE left => that is the head this voucher is booked to, by uuid.
 *  4. Anything else => null, and the builder falls back to the canonical base.
 *     A multi-head voucher cannot be restated by a form holding one taxable
 *     value anyway, and the multi-rate guard already refuses those.
 *
 * ⚠️ ON A FRESH CREATE THERE IS NO VIEW, so the canonical base is still what a
 * new voucher gets. This changes only what an ALTER preserves.
 */
function baseLegFromView(view: VoucherView): string {
  return gstLegsFromView(view).baseLedgerId;
}

/**
 * THE BASE LEG AND THE ADDITIONAL CHARGES, told apart on the way back in.
 *
 * ⛔⛔ THIS EXISTS BECAUSE A CHARGE LINE SITS ON THE **SAME SIDE** AS THE BASE.
 * On a sale both are credits, so the moment `GstFormState.charges` was added the
 * old one-leg rule (`legs.length === 1`) stopped matching any invoice carrying a
 * charge — it returned "" and the alter re-pointed the goods to the canonical
 * `Domestic Sales`, which is precisely the defect `baseLegFromView`'s own
 * comment records having fixed once already.
 *
 * ⛔ AND THE CHARGES MUST COME BACK OUT, or the ordinary round trip — open the
 * invoice, fix the narration, Ctrl+A — REBUILDS it without them: the buyer's
 * total silently falls by the charge, and on a TCS invoice the collected tax
 * stops being posted while the return still says it was collected. That is
 * register row D88's shape exactly, one field over.
 *
 * THE DISCRIMINATOR IS THE TAXABLE VALUE, and it is the only honest one
 * available: `PostingView` carries no group, so nothing on the wire says which
 * credit is the sale. The base leg is the one posted for `view.taxable_value`.
 *
 * ⚠️ WHEN IT CANNOT TELL — no leg matches the taxable value, or several do — it
 * says so (`ambiguous`) and names NOTHING, and `gstRefusal` then refuses the
 * ALTER rather than guessing. Guessing here has exactly two outcomes and both
 * are silent: the goods booked to a charge account, or a charge dropped off the
 * invoice. A refusal that names the remedy is strictly better than either.
 */
export interface GstLegsFromView {
  /** The ledger the GOODS are booked to; "" when it could not be told. */
  baseLedgerId: string;
  /** The additional charges, as the form keys them. */
  charges: GstChargeLine[];
  /** True when the credit legs could not be told apart — see above. */
  ambiguous: boolean;
}

export function gstLegsFromView(view: VoucherView): GstLegsFromView {
  const empty: GstLegsFromView = {
    baseLedgerId: "",
    charges: [],
    ambiguous: false,
  };
  const orientation = gstOrientationFor(view.class.toLowerCase());
  if (orientation === undefined) return empty;
  const taxNames = new Set<string>(
    gstTaxLedgerNames(orientation.taxKind).map((n) => n.toLowerCase()),
  );
  const legs = view.postings.filter(
    (p) =>
      p.dr_cr.toUpperCase().startsWith(orientation.baseSide[0]!) &&
      p.ledger_id !== view.party_ledger_id &&
      !taxNames.has(p.ledger.toLowerCase()),
  );
  // The overwhelmingly common shape, and UNCHANGED: one credit, it is the sale.
  // ⚠️ Matched WITHOUT consulting the taxable value on purpose — a voucher
  // stored before `voucher_gst_line` existed hydrates `taxable_value` as
  // "0.0000", and requiring the match here would refuse every alter of one.
  if (legs.length <= 1) {
    return { ...empty, baseLedgerId: legs.length === 1 ? legs[0]!.ledger_id : "" };
  }
  const base = legs.filter((p) => sameMoney(p.amount, view.taxable_value));
  if (base.length !== 1) return { ...empty, ambiguous: true };
  return {
    baseLedgerId: base[0]!.ledger_id,
    charges: legs
      .filter((p) => p !== base[0])
      .map((p) => ({ ledger: p.ledger, amount: p.amount })),
    ambiguous: false,
  };
}

/** Two fixed-point money strings, compared as VALUES ("100" == "100.0000"). */
function sameMoney(a: string, b: string): boolean {
  return normalizeMoney(a) === normalizeMoney(b);
}

/**
 * WHO THE MONEY CAME FROM / WENT TO on a single-entry voucher — the voucher's
 * real counterparty, derived from the PARTICULARS.
 *
 * ⛔⛔ THE DEFECT THIS CLOSES (D59) WAS ON PAPER, WHICH IS WHERE IT COUNTS. The
 * form put the BANK in both `party` and `party_ledger_id`, and
 * `printingdb.loadHead` builds the Party block from `party_ledger_id` (falling
 * back to the `party` LABEL when there is no master behind it) — so every
 * receipt this product printed said "Received From: HDFC Bank" and every payment
 * "Paid To: HDFC Bank". A receipt that names the bank as the payer is not a
 * receipt: nobody's account can be credited from it, which is exactly the
 * consequence `vdPartyConsequence` states.
 *
 * THE RULE, AND WHY IT REFUSES TO GUESS:
 *
 *  1. Exactly ONE distinct party-shaped particular (a debtor, a creditor, or the
 *     cash/bank account on the other end of a contra — {@link PARTY_GROUPS},
 *     which is the same list the party PICKER offers) => that is the
 *     counterparty, name AND uuid. The uuid is what carries the printed block,
 *     the address and the GSTIN.
 *  2. No party-shaped particular but exactly ONE particular in all => its NAME
 *     is the counterparty label and NO uuid is sent. "Paid To: Electricity" is
 *     the truth about a payment booked straight to an expense head, and
 *     `party_ledger_id` stays what its contract says it is — a party master, not
 *     an expense head that some report will later join on and mis-classify.
 *  3. Anything else (several parties, or several expense heads) => NULL. One
 *     voucher settling three customers has three counterparties, and naming the
 *     first would be this file inventing the answer. The document then refuses
 *     to print, naming the missing particular — which is the honest outcome for
 *     a document that cannot say who it is for, and is strictly better than the
 *     confident lie it printed before.
 *
 * ⚠️ Rows are deduplicated BY LEDGER, not by name, because the same customer may
 * legitimately appear on two particulars (two bills settled by one cheque) and
 * that is one counterparty, not an ambiguity.
 */
function singleEntryCounterparty(
  rows: readonly { name: string; ledgerId: string }[],
  chart: readonly LedgerListItem[],
): { name: string; ledgerId: string } | null {
  const distinct = new Map<string, { name: string; ledgerId: string }>();
  for (const r of rows) if (!distinct.has(r.ledgerId)) distinct.set(r.ledgerId, r);
  const all = [...distinct.values()];
  const partyish = all.filter((r) => {
    const l = chart.find((c) => c.id === r.ledgerId);
    return l !== undefined && PARTY_GROUPS.includes(l.group_name);
  });
  if (partyish.length === 1) return partyish[0]!;
  if (partyish.length > 1) return null;
  // ⚠️ A ledger this accept is about to CREATE is not in the chart (which is read
  // once per company), so it can never be party-shaped here — and it genuinely is
  // not: a free-typed particular is created under Indirect Expenses / Direct
  // Incomes. It falls to rule 2 and contributes a label, which is right.
  if (all.length === 1) return { name: all[0]!.name, ledgerId: "" };
  return null;
}

/**
 * The PARTY leg as a bill-bearing line, read from the form's own postings.
 *
 * ⛔ DERIVED, NEVER ASSUMED. The verbatim alter path re-sends `f.postings` as they
 * were hydrated, so the reference must total whatever the PARTY ledger nets to in
 * THAT array — not what the class would normally do. A sales voucher whose party
 * leg was corrected to a different figure must re-reference the corrected one, and
 * the server refuses any other total to the paisa.
 *
 * Signed: debits positive. The side follows the sign, so a party that nets credit
 * (a purchase, or a sale that was reversed) is referenced on the credit side
 * without a special case.
 */
function partyLeg(f: VoucherForm): BillBearingLeg {
  let units = 0n;
  for (const p of f.postings) {
    if (p.ledger_id !== f.partyLedgerId) continue;
    const amt = parseFixed4(p.amount);
    units += p.dr_cr.toUpperCase().startsWith("D") ? amt : -amt;
  }
  return {
    ledgerId: f.partyLedgerId,
    drCr: units < 0n ? "CR" : "DR",
    amount: formatFixed4(units < 0n ? -units : units),
  };
}

/**
 * Seed the form from an OCR-derived {@link VoucherDraftBody} (the F9 draft-mode
 * prefill path). DISPLAY ONLY: the accept POSTs the ORIGINAL balanced body, not
 * a form rebuild, so this only populates the visible fields. Money/qty/rate stay
 * exact fixed-4 strings — never Number()-coerced.
 */
function formFromDraft(draft: PurchaseVoucherDraft): VoucherForm {
  const body = draft.body;
  const line = body.gst?.lines[0];
  return {
    // A draft is built by insightd from the bill, not hydrated from a stored
    // voucher, so there are no legs to have been ambiguous.
    gstLegsAmbiguous: false,
    date: body.voucher_date ?? today(),
    party: body.party ?? "",
    partyLedgerId: body.party_ledger_id ?? "",
    // ⚠️ "" DELIBERATELY. A draft is a NEW purchase, not an alteration of a
    // stored one, so there is no already-booked head to preserve — the
    // orientation's canonical base is the right answer here. (The accept POSTs
    // the original balanced body anyway; see this function's own note.)
    baseLedgerId: "",
    // A draft is an F9 PURCHASE — a GST form with no single-entry account. Blank
    // rather than derived, for the same reason `drCrLines` is: the accept POSTs
    // the ORIGINAL balanced body, so a field seeded here would display something
    // that is not what gets sent.
    account: "",
    accountLedgerId: "",
    partyStateCode: "",
    narration: body.narration ?? "",
    postings: [],
    voucherNo: body.voucher_no ?? "",
    gst: {
      placeOfSupply: body.gst?.place_of_supply_state_code ?? "",
      gstRatePct: line?.gst_rate_pct ?? "",
      reverseCharge: body.gst?.reverse_charge ?? false,
      hsn: line?.hsn ?? "",
      // An OCR draft is a PURCHASE and carries no additional charge: insightd
      // derives postings from the bill, and a charge is something the operator
      // decides to bill, not something a scan can infer.
      charges: [],
    },
    // An OCR draft is a CREATE, never an alter — there is no stored voucher
    // behind it and so no stored GST line to be lost.
    gstLineCount: 0,
    taxableValue: line?.taxable_value ?? "0.0000",
    // Draft mode is F9 Purchase, which is a GST (double-entry) form — the
    // single-entry particulars are unused there, but the shape must be whole.
    // ⚠️ No allocations, and that is not an oversight: an item invoice REFUSES
    // cost_allocations outright (its postings are server-generated and index 0 is the
    // party leg), so a draft-prefilled purchase has nowhere to put one.
    lines: [{ ledger: "", amount: "", allocations: [], billRefs: [] }],
    // Likewise unused: F9 draft mode is a GST form, and `cfg.showGst` wins over
    // the Dr/Cr grid in both the render chain and the accept dispatch. Present
    // because the shape must be whole, and BLANK rather than derived from the
    // draft body — the draft accept POSTs the ORIGINAL balanced body, not a
    // form rebuild, so a grid seeded here would display lines that are not the
    // ones being sent.
    drCrLines: [emptyDoubleEntryLine()],
    billRefs: [],
    // An OCR draft carries no stock: insightd reads a supplier's bill, and the
    // items on it are the SUPPLIER's descriptions, not this company's stock
    // masters. Matching them would be a guess, and a guess that moves stock.
    items: [emptyItemLine()],
    // A draft carries no statutory deduction either: insightd reads a supplier's
    // bill, and whether this company had a duty to deduct on it is not on the
    // paper.
    tax: emptyTaxDeductionForm(),
  };
}

/** The editable draft fields, and their OCR-confidence keys (for the amber-drop). */
const DRAFT_EDITABLE_FIELDS = ["date", "narration", "taxableValue", "gstRatePct", "placeOfSupply"] as const;
type DraftEditableField = (typeof DRAFT_EDITABLE_FIELDS)[number];

/** confidence key an edited form field invalidates (narration has none). */
const DRAFT_FIELD_CONF_KEY: Partial<Record<DraftEditableField, string>> = {
  date: "invoice_date",
  taxableValue: "totals",
  gstRatePct: "totals",
  placeOfSupply: "place_of_supply",
};

/** The set of editable draft fields whose live value differs from the seed. */
function draftDirtyFields(seed: VoucherForm | null, form: VoucherForm): Set<DraftEditableField> {
  const dirty = new Set<DraftEditableField>();
  if (!seed) return dirty;
  if (form.date !== seed.date) dirty.add("date");
  if (form.narration !== seed.narration) dirty.add("narration");
  if (form.taxableValue !== seed.taxableValue) dirty.add("taxableValue");
  if (form.gst.gstRatePct !== seed.gst.gstRatePct) dirty.add("gstRatePct");
  if (form.gst.placeOfSupply !== seed.gst.placeOfSupply) dirty.add("placeOfSupply");
  return dirty;
}

// ===========================================================================
// ⛔⛔ THE MASTER GATE — "NO LEDGER IS CREATED FOR A DOCUMENT THE CLIENT
// ALREADY KNOWS WILL BE REFUSED".
// ===========================================================================
//
// THE CLASS, not the two bugs it was reported as. D39 and D41 are the same
// defect on three different keystrokes:
//
//   D39 (DRIVEN, both grids): Ctrl+A on a voucher whose cost split is
//       incomplete on one axis created EVERY ledger the operator had named —
//       then the server refused the voucher. The chart of accounts gained
//       accounts with no voucher behind them, and nothing on screen said so.
//   D41 (DRIVEN, the alter path): opening a Purchase from the Day Book and
//       pressing Ctrl+A created a new "Purchases" ledger over the wire and THEN
//       failed the write with "invalid gst line gst_rate_pct". A REFUSED ALTER
//       LEFT A MASTER BEHIND.
//
// `ensureLedger` and `ensureGstTaxLedgers` CREATE. Every builder in this file
// used to resolve its ledgers first and validate afterwards (or not at all), so
// every refusal — client-side or server-side — landed after the writes. Press
// Ctrl+A five times on a form the server will refuse and the company silently
// gains five accounts and no voucher.
//
// ⛔ THE ORDER IS THE FIX, AND A COMMENT SAYING "VALIDATE FIRST" IS NOT. The
// Journal builder already carried exactly that comment and was still creating
// ledgers for a form with a short cost axis, because the check it ran first was
// only the BALANCE check. So the ordering is now STRUCTURAL: every `ensure*`
// call in this file sits inside a {@link resolveMasters} closure whose FIRST
// argument is the refusal, evaluated before the closure can run. A new resolver
// cannot be added without a refusal argument being written next to it.
//
// ⛔ WHAT THIS DOES **NOT** CLOSE, STATED SO IT IS NOT MISTAKEN FOR CLOSED: a
// refusal only the SERVER can make (a back-dated-period cutoff, an ACL denial, a
// duplicate voucher number, a negative-stock refusal) still lands after the
// masters exist, because a posting needs a real uuid and there is no
// DELETE /ledgers route to compensate with. The client's half is that it never
// contributes a refusal of its own to that window.
//
// ⚠️ AND WHAT NOW HAPPENS IN THAT WINDOW (D81, closed 2026-08-24). The orphan is
// still real — it cannot be otherwise — but it is no longer SILENT. `onAccept`
// reads the chart before the builder runs and, on a refused POST, names every
// master this accept minted and says that a retry reuses them. See
// {@link discloseOrphanedMasters}. Driven: a GST sales invoice refused for a
// closed period minted FOUR accounts and the operator was shown only
// "Error: period is closed".
// ===========================================================================

/**
 * Run `resolve` ONLY if the form has already been found saveable.
 *
 * ⛔ `refusal` IS EVALUATED BY THE CALLER BEFORE THE CLOSURE EXISTS. That is the
 * whole point: the refusal cannot accidentally be computed from something the
 * resolver produced, and a reader of any call site sees the check and the writes
 * in the same expression.
 */
async function resolveMasters<T>(
  refusal: string,
  resolve: () => Promise<T>,
): Promise<T> {
  if (refusal !== "") throw new Error(refusal);
  return resolve();
}

/**
 * Every ledger id a request body names, wherever it sits in that body.
 *
 * ⛔ A WALK, NOT A LIST OF FIELDS, AND THE DIFFERENCE IS THE WHOLE VALUE. The ids
 * live at `postings[].ledger_id`, `party_ledger_id`, `item_ledger_id`,
 * `bill_allocations[].ledger_id` and the three `gst.{c,s,i}gst_ledger_id` — and
 * `types.ts` carries `tds_ledger_id`, `deductee_ledger_id` and `buyer_ledger_id`
 * besides. An enumeration here would silently stop covering the body the day a
 * tenth field is added, and the failure mode is the quiet one: a master minted for
 * a refused voucher that this disclosure no longer mentions. The suffix is the
 * contract the server's own wire already keeps.
 */
function ledgerIdsIn(value: unknown, into: Set<string> = new Set()): Set<string> {
  if (Array.isArray(value)) {
    for (const v of value) ledgerIdsIn(v, into);
    return into;
  }
  if (value === null || typeof value !== "object") return into;
  for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
    if (typeof v === "string") {
      if (k === "ledger_id" || k.endsWith("_ledger_id")) into.add(v);
    } else {
      ledgerIdsIn(v, into);
    }
  }
  return into;
}

/**
 * A refused voucher whose accept had ALREADY minted masters says which ones
 * (register row D81).
 *
 * ⛔ THE ORPHAN CANNOT BE UNDONE AND NOTHING HERE PRETENDS OTHERWISE. cored exposes
 * POST /ledgers, GET /ledgers and PUT /ledgers/{id}/opening — there is NO DELETE
 * route (five DELETEs exist on the whole server and none of them is one), and the
 * voucher needs real ledger ids, so the masters must exist before the POST that may
 * be refused. The honest remedy is to make the orphan VISIBLE and HARMLESS: name it,
 * and say that a retry REUSES it.
 *
 * ⚠️ MINTED IS JUDGED BY ID against the chart as it stood moments earlier, and it is
 * used ONLY TO REPORT. A create in another tab between the snapshot and the build
 * could in principle be misattributed here, which costs one slightly wrong sentence
 * — and is precisely why nothing is deleted on the strength of it.
 *
 * ⛔ AND A DISCLOSURE THAT CANNOT BE MADE ACCURATELY IS NOT MADE AT ALL. If either
 * chart read failed we do not know which masters are new, so the operator gets the
 * server's own refusal unaltered rather than a sentence naming a guess. A disclosure
 * that is fully present and fully wrong is worse than none — the operator acts on it.
 */
async function discloseOrphanedMasters(
  client: CoredClient,
  companyID: string,
  req: VoucherRequest,
  known: Set<string> | null,
  err: unknown,
): Promise<never> {
  if (known === null) throw err; // no "before" picture — see the header
  const mintedIds = [...ledgerIdsIn(req)].filter((id) => !known.has(id));
  if (mintedIds.length === 0) throw err;
  // The names come from the chart the masters are now ON, which is the only
  // authority for them: the request carries ids, and the builders that minted them
  // do not report back.
  const after = await client
    .listLedgers(companyID)
    .then((r) => new Map(r.ledgers.map((l) => [l.id, l.name])))
    .catch(() => null);
  if (after === null) throw err;
  const minted = mintedIds
    .map((id) => ({ id, name: after.get(id) }))
    .filter((m): m is { id: string; name: string } => m.name !== undefined);
  if (minted.length === 0) throw err;
  throw voucherAcceptFailure(err, minted);
}

/**
 * The refusals that belong to a ROW of any grid — the two the pickers already
 * compute for display and that NOTHING consulted on the way to the server.
 *
 * ⛔ BOTH ARE SERVER REFUSALS THIS CLIENT CAN MAKE FIRST. `allocationRefusal`
 * and `billRefusal` were written as refusals ("telling them before the send is
 * strictly kinder", say both headers) and then wired only into their pickers —
 * and the cost-centre picker is MOUSE-ONLY on this screen (D38), so on the
 * keyboard path nothing checked them at all. That is how D39 reached a real
 * browser: a short axis is refused by the server AND by a deferred DB trigger,
 * after this file has already created the ledgers.
 *
 * The row is named in the message because the grids have no other handle on a
 * line — `posting_seq` is the 0-based ordinal no screen in this product may
 * display.
 */
function rowRefusal(
  label: string,
  row: { amount: string; allocations: readonly DraftAllocation[]; billRefs: readonly DraftBillRef[] },
  side: "DR" | "CR",
): string {
  const alloc = allocationRefusal(row);
  if (alloc !== "") return `${label}: ${alloc}`;
  // ⚠️ The leg's LEDGER is not known yet — that is the point of this gate, the
  // ledger may not exist — and `billRefusal` does not need it: it is arithmetic
  // against the line's own amount. Passing "" here rather than resolving one is
  // deliberate.
  const bill = billRefusal(
    { ledgerId: "", drCr: side, amount: normalizeMoney(row.amount) },
    row.billRefs,
  );
  return bill === "" ? "" : `${label}: ${bill}`;
}

/** The keyed particulars of a single-entry form (empty rows are not lines). */
function keyedParticulars(f: VoucherForm): VoucherForm["lines"] {
  return f.lines.filter(
    (l) => l.ledger.trim() !== "" && parseFloat(l.amount || "0") > 0,
  );
}

/**
 * EVERY refusal a SINGLE-ENTRY form can make, computed before anything is
 * resolved. "" means saveable.
 */
function singleEntryRefusal(f: VoucherForm, cfg: VoucherConfig): string {
  const rows = keyedParticulars(f);
  if (rows.length === 0) return "Add at least one particular with an amount.";
  if (f.account.trim() === "") {
    // ⚠️ ALSO THE MESSAGE A HYDRATION THAT COULD NOT NAME ONE ACCOUNT PRODUCES —
    // see accountLegFromView. A voucher with two primary legs is not keyable
    // here, and saying so beats merging them into one behind the operator.
    return (
      `${cfg.partyLabel} is required — single entry puts the TOTAL on one ` +
      `account and lists what it moved against. If this voucher moves money ` +
      `through more than one bank, key it in double entry (Ctrl+H).`
    );
  }
  const manySide: "DR" | "CR" = cfg.primarySide === "DR" ? "CR" : "DR";
  for (let i = 0; i < rows.length; i++) {
    const l = rows[i]!;
    const r = rowRefusal(`Particular ${i + 1} (${l.ledger.trim()})`, l, manySide);
    if (r !== "") return r;
  }
  return "";
}

/**
 * The two grids differ in shape and share this: a row that the server will
 * refuse must be refused BEFORE `ensureLedger` runs. D39 was reproduced on BOTH.
 */
function doubleEntryRowRefusal(
  rows: readonly { ledger: string; side: "DR" | "CR"; amount: string; allocations: readonly DraftAllocation[]; billRefs: readonly DraftBillRef[] }[],
): string {
  for (let i = 0; i < rows.length; i++) {
    const l = rows[i]!;
    const r = rowRefusal(`Line ${i + 1} (${l.ledger.trim()})`, l, l.side);
    if (r !== "") return r;
  }
  return "";
}

/**
 * Is this what `decimal.NewFromString` on the server will accept?
 *
 * ⛔ MIRRORS THE HANDLER, AND THE DIRECTION OF ERROR MATTERS. `buildGST` 400s
 * with "invalid gst line gst_rate_pct" on anything shortdec cannot parse —
 * including the EMPTY STRING, which is what an alter hydrates when the stored
 * voucher has no `gst_lines` (D41's exact wire error). A peek that is LOOSER
 * than the handler lets the orphan-master window back open; one that is TIGHTER
 * refuses a voucher the server would have taken. So: a plain optionally-signed
 * decimal, nothing clever.
 */
function isServerDecimal(s: string): boolean {
  return /^[+-]?(\d+(\.\d*)?|\.\d+)$/.test(s.trim());
}

/**
 * WHICH CLASSES MAY NOT BE SAVED WITHOUT AN HSN / SAC (D60).
 *
 * ⛔⛔ THE DEFECT WAS A DOCUMENT THAT COULD BE KEYED AND THEN NOT PRINTED. A
 * Sales invoice keyed through the GST panel saved happily and Ctrl+P answered
 * 400: `printing.rule46` treats the HSN as a Rule 46(g) MANDATORY PARTICULAR
 * (`items[0].hsn`) and refuses to render an invoice without one, while this form
 * left the field optional. So the operator learns at the counter, with the
 * customer waiting, that the invoice they saved cannot be handed over.
 *
 * ⛔ SALES ONLY, AND THE SCOPE IS THE POINT — a refusal that fired on every GST
 * class would "fix" D60 by making the feature unusable. Reasoned from what the
 * server actually requires, class by class:
 *
 *  - `sales`      → `printingdb` maps it to KindTaxInvoice (or KindBillOfSupply
 *                   when no tax is charged, where Rule 49 asks for the same
 *                   particular). Both refuse a blank HSN. REQUIRED HERE.
 *  - credit/debit note → rendered by `voucherdoc`, whose `validateVoucherDoc`
 *                   states no HSN requirement. Refusing here would be this file
 *                   inventing a statutory particular the printing layer does not
 *                   cite. NOT required.
 *  - `purchase`   → the supplier's document; `docKindForClass` refuses to print
 *                   one at all ("a purchase bill is the SUPPLIER'S document").
 *                   NOT required — and requiring it would also break the Bill
 *                   Inbox, where a scanned bill whose HSN did not survive OCR
 *                   must still be acceptable. gstVoucher.ts's own header makes
 *                   exactly that argument for why the REFUSAL DOES NOT LIVE
 *                   THERE; this is the screen where it can be scoped.
 *
 * ⚠️ THE FILING SIDE IS NOT THIS CHECK'S JOB and is already covered: the GSTR-1
 * builder refuses to emit a blank mandatory code and names the vouchers
 * responsible. This one is about the piece of paper.
 */
const HSN_REQUIRED_CLASSES: readonly VoucherType[] = ["sales"];

/**
 * THE INVOICE CLASSES MUST NAME SOMEBODY — and the reason is the master gate,
 * not tidiness.
 *
 * ⛔ THE SIMPLEST KEYSTROKE IN THE PRODUCT USED TO MINT THREE ACCOUNTS AND SAVE
 * NOTHING: press F8 and Ctrl+A on an untouched form. `f.party` is "" and
 * `f.partyLedgerId` is "", so the builder ran `ensureGstTaxLedgers` (creating
 * Output CGST / SGST / IGST) and then `ensureLedger({ name: "" })` — which cored
 * refuses outright ("masters: ledger name is required", masters.go:297). Three
 * real accounts, no voucher, and the operator shown one error about a name they
 * never typed.
 *
 * ⚠️ A FREE-TYPED NAME IS STILL FINE. This refuses only the EMPTY field; a party
 * the company does not have yet is created on accept, which is how the first
 * voucher of a brand-new company is keyed and is Tally's own behaviour.
 */
function partyRefusal(f: VoucherForm, type: VoucherType): string {
  if (f.partyLedgerId !== "" || f.party.trim() !== "") return "";
  return (
    `${configFor(type).partyLabel} is required — an invoice is raised ON ` +
    `somebody, and the document names them. Pick a party, or type a name to ` +
    `create one on accept.`
  );
}

/**
 * The PARTY leg a GST (accounting-mode) invoice will reference, computed from
 * the form alone so the refusal is measured against the body that will be sent.
 * Mirrors `raiseLeg` in the component — the hydrated postings win on an alter,
 * because a GET returns the tax but not the RATE it was derived at.
 */
function gstBillLeg(
  f: VoucherForm,
  type: VoucherType,
  homeStateCode: string,
): BillBearingLeg {
  // ⛔ THE PARTY TOTAL, NOT THE GROSS. A charge is money the party owes, and the
  // server checks the bill reference against the party POSTING to the paisa —
  // so measuring the refusal against the pre-charge figure certified an invoice
  // as referenced in full and then had it refused at Ctrl+A. See
  // gstVoucherPartyTotal, which is the one definition all three surfaces share.
  const amount = f.postings.some((p) => p.ledger_id === f.partyLedgerId)
    ? partyLeg(f).amount
    : gstVoucherPartyTotal(
        normalizeMoney(f.taxableValue),
        f.gst.gstRatePct,
        homeStateCode,
        f.gst.placeOfSupply,
        f.gst.charges,
      );
  return {
    ledgerId: f.partyLedgerId,
    drCr: type === "purchase" ? "CR" : "DR",
    amount,
  };
}

/**
 * EVERY refusal an ACCOUNTING-MODE GST voucher can make, before a single tax
 * ledger, party or income account is created.
 *
 * ⛔ THIS IS WHERE D41 LIVED. Opening a Purchase from the Day Book and pressing
 * Ctrl+A created a new "Purchases" ledger over the wire and THEN failed with the
 * server's "invalid gst line gst_rate_pct" — a refused ALTER that left a master
 * behind. The rate is a FIELD ON THIS FORM: the client can see it is unsendable
 * without asking anybody, and it hydrates empty whenever the stored voucher
 * carries no `voucher_gst_line` rows.
 */
function gstRefusal(f: VoucherForm, type: VoucherType, homeStateCode: string): string {
  if (f.gstLineCount > 1) {
    return (
      `This voucher has ${f.gstLineCount} GST lines at different rates, and ` +
      `the accounting-mode form holds only one rate — saving it here would ` +
      `keep the first line and DELETE the rest, re-deriving the tax from ` +
      `what was left. Alter it as an Item Invoice (Ctrl+H), where each line ` +
      `carries its own rate from the stock master.`
    );
  }
  if (!gstOrientationFor(type)) {
    return (
      `This build has no posting orientation for a "${type}" GST voucher, ` +
      `so it will not guess which way the money goes.`
    );
  }
  const partyGap = partyRefusal(f, type);
  if (partyGap !== "") return partyGap;
  if (!isServerDecimal(f.gst.gstRatePct)) {
    return (
      `GST Rate % is ${f.gst.gstRatePct.trim() === "" ? "empty" : `"${f.gst.gstRatePct.trim()}"`}, ` +
      `which the server cannot read as a rate — it answers "invalid gst line ` +
      `gst_rate_pct" and nothing is saved. Key a number; 0 is a real rate and ` +
      `is what an exempt or nil-rated supply carries. ⚠️ A voucher stored ` +
      `without GST lines hydrates this field EMPTY, so an alteration has to ` +
      `re-state the rate it was raised at.`
    );
  }
  if (HSN_REQUIRED_CLASSES.includes(type) && f.gst.hsn.trim() === "") {
    return (
      `HSN / SAC is required on a ${configFor(type).title.toLowerCase()} ` +
      `invoice. It is a MANDATORY particular of a tax invoice (CGST Rules ` +
      `2017, Rule 46(g)), so an invoice saved without one cannot be printed — ` +
      `Ctrl+P answers 400 naming items[0].hsn, at the counter, after the ` +
      `document has been keyed. Key the code for what was supplied.`
    );
  }
  // ⛔ THE AMBIGUOUS ALTER, REFUSED RATHER THAN GUESSED. See gstLegsFromView: a
  // stored voucher whose credit legs cannot be told apart would be rebuilt
  // either with the goods booked to a charge account or with the charge dropped,
  // and BOTH balance, so nothing downstream would report it.
  if (f.gstLegsAmbiguous) {
    return (
      "This voucher has more than one credit besides the party and the tax " +
      "heads, and none of them is for its taxable value — so this form cannot " +
      "tell the goods from the additional charges, and it will not guess: " +
      "booking the goods to a charge account and dropping a charge off the " +
      "invoice both BALANCE, so nothing would report either. Nothing was sent. " +
      "Alter it as an Item Invoice (Ctrl+H), where each line names itself."
    );
  }
  // ⛔ THE CHARGE LINES ARE PART OF THIS GATE, not checked later, for the reason
  // the whole function exists: the resolvers below CREATE accounts, so a refusal
  // after them leaves the charge's ledger behind on a voucher that never posted
  // (D39/D81).
  const chargeGap = chargeLinesRefusal(f.gst.charges);
  if (chargeGap !== "") return chargeGap;
  // ⛔ AND THE STATUTORY BLOCK, exactly as the Dr/Cr grid gates it (D3). The
  // leg it may name is a CHARGE LINE — that is the whole design: a collection
  // the buyer is really charged, named by the operator, verified by the server.
  const cfg = configFor(type);
  if (cfg.statutoryTax !== null) {
    const taxGap =
      taxDeductionRefusal(f.tax, cfg.statutoryTax) ||
      taxLegRefusal(
        f.tax,
        cfg.statutoryTax,
        keyedChargeLines(f.gst.charges).map((c) => c.ledger),
      );
    if (taxGap !== "") return taxGap;
  }
  return billRefusal(gstBillLeg(f, type, homeStateCode), f.billRefs);
}

/**
 * EVERY refusal an ITEM INVOICE can make, before any account is created.
 *
 * ⚠️ `rows` is the grid ALREADY JOINED to the stock masters, because that is
 * what the builder sends and what the bill reference must total.
 *
 * ⛔ THE MISSING HSN IS **NOT** REFUSED HERE, AND THAT IS DELIBERATE (RULE 3).
 * An item line's HSN comes off the STOCK MASTER, and this build has no keyboard
 * path to a stock item at all — Alt+C reaches ledgers only. A refusal whose
 * remedy cannot be reached from the keyboard would make item invoicing
 * unusable on any company with one incomplete master. It is DISCLOSED on the
 * panel instead, naming the item, so the operator learns before Ctrl+P rather
 * than at the counter. See {@link ItemInvoicePanel}.
 */
function itemInvoiceRefusal(
  f: VoucherForm,
  type: VoucherType,
  rows: readonly ItemInvoiceLine[],
  homeStateCode: string,
): string {
  if (!gstOrientationFor(type)) {
    return `This build has no posting orientation for a "${type}" item invoice.`;
  }
  // ⚠️ AND HERE THE SERVER AGREES EXPLICITLY: `item_invoice.go:241` refuses an
  // item invoice with no party at all, because the party leg is one of the
  // postings it GENERATES. The client would have created four masters first.
  const partyGap = partyRefusal(f, type);
  if (partyGap !== "") return partyGap;
  const keyed = rows.filter(isKeyedLine);
  if (keyed.length === 0) {
    return (
      "Add at least one item line with a quantity and a rate — an item invoice " +
      "with no goods on it states a total the server will not derive."
    );
  }
  const amount = f.postings.some((p) => p.ledger_id === f.partyLedgerId)
    ? partyLeg(f).amount
    : itemInvoiceGross(rows, homeStateCode, f.gst.placeOfSupply);
  return billRefusal(
    {
      ledgerId: f.partyLedgerId,
      drCr: type === "purchase" ? "CR" : "DR",
      amount,
    },
    f.billRefs,
  );
}

/**
 * The voucher screen the shell mounts — a two-way ROUTER, not a form.
 *
 * The four NON-ACCOUNTING stock classes (Alt+F8 Delivery Note, Alt+F9 Receipt
 * Note, Alt+F7 Stock Journal, Alt+F10 Physical Stock) get their own component.
 * They share almost nothing with an accounting voucher: no party ledger, no
 * particulars, no GST panel, no amount — the server REFUSES every one of those
 * on those classes (validateStockVoucher). Rendering the accounting form with
 * its money fields hidden would leave the state that feeds buildGstRequest /
 * buildSingleEntryRequest alive behind them, one stale frame config away from
 * being posted on a class that answers 422 to a posting.
 *
 * The split is a WRAPPER rather than an early return inside the accounting
 * component on purpose: an early return before the hooks would change the hook
 * COUNT of a mounted component whenever an F-key replaces the frame's voucher
 * type in place, which React refuses at runtime.
 *
 * ⛔ PAGE UP / PAGE DOWN LIVES HERE, ABOVE THE SPLIT, and that is a fix rather
 * than tidying — see {@link useVoucherPaging}.
 */
export function VoucherScreen(props: {
  snapshot: NavSnapshot;
  onSelect: (item: string) => void;
}) {
  const type = (props.snapshot.voucherType ?? "sales") as VoucherType;
  // Registered on the ROUTER so both forms have it. Hooks before the branch, so
  // the hook count is the same whichever component renders.
  useVoucherPaging(props.snapshot, props.onSelect);
  // The stock screen takes no `onSelect`: it has no pick-list that feeds the
  // navigation machine (its party field is free text describing the document,
  // not a ledger to resolve), and firing a SELECT per keystroke would push the
  // machine around from a field that names nothing it knows about.
  if (isStockVoucherType(type)) return <StockVoucherScreen snapshot={props.snapshot} />;
  return <AccountingVoucherScreen {...props} />;
}

/**
 * PAGE UP / PAGE DOWN — browse the SAVED vouchers around the loaded one, in Day
 * Book order (the same ordering the operator drilled from). The Day Book is
 * fetched once and cached in a ref; moving loads the neighbour into the
 * loaded-voucher slot, which re-hydrates the form from GET /vouchers/{id}.
 *
 * ⛔⛔ DAY BOOK ORDER IS CHRONOLOGICAL AND THEREFORE MIXED-CLASS, AND THAT WAS A
 * SECOND LIVE PATH INTO THE RE-CLASSING DEFECT — one with no drill in it at all.
 * Paging a Sales alteration onto a Receipt left the FRAME saying "sales", so
 * `acceptVoucher` routed the PUT to `alterSalesVoucher` and re-classed the
 * receipt. The neighbour's row key is therefore published through `onSelect`,
 * which re-classes the standing alteration frame (see `reduceSelect`); the loaded
 * slot is set FIRST, so the frame and the document can never disagree.
 *
 * ⛔ AND IT IS ON THE ROUTER RATHER THAN INSIDE THE ACCOUNTING FORM BECAUSE IT WAS
 * A DEAD END THERE — found by paging in a real browser, not by review. It used to
 * live in `AccountingVoucherScreen`, which was harmless only while the frame's
 * class never changed: paging always kept rendering the accounting form. Now that
 * the frame follows the document, paging onto a Delivery Note correctly mounts
 * `StockVoucherScreen` — which registered only `accept`, so Page Up and Page Down
 * both went dead and the operator was STRANDED on that voucher with Esc the only
 * way out. Browser-verified: Sales → Purchase → Receipt Note, then five presses
 * that did nothing at all.
 *
 * Paging between vouchers is a property of the voucher FRAME, not of either form
 * — neither reads a single piece of form state to do it — so one registration
 * above the split is both the smaller fix and the truer place. A second copy
 * inside the stock screen was the alternative, and §1.3's `ensureLedger` lesson is
 * that a second copy is exactly how a defect gets fixed once and shipped twice.
 */
function useVoucherPaging(
  snapshot: NavSnapshot,
  onSelect: (item: string) => void,
): void {
  const client = useClient();
  const companyID = useCompanyID();
  const { loaded, setLoaded } = useLoadedVoucher();
  const dayBookRef = useRef<DayBookEntry[] | null>(null);
  // Only an ALTER frame has a voucher to page from; a create frame has nothing
  // loaded and must not adopt whatever the previous drill left in the slot.
  const currentID =
    snapshot.mode === "alter" ? (loaded?.entry?.voucher_id ?? null) : null;

  const stepVoucher = useCallback(
    (delta: number): string => {
      if (!currentID) {
        return "Page Up / Page Down browse saved vouchers — open one from the Day Book first.";
      }
      const apply = (entries: DayBookEntry[]): string => {
        const idx = entries.findIndex((e) => e.voucher_id === currentID);
        if (idx < 0) return "This voucher is not in the current Day Book range.";
        // ⚠️ SKIP CLASSES WITH NO ENTRY SCREEN rather than landing on one. A
        // Payroll voucher in the Day Book has no voucher form in this build, so
        // stopping on it would render whatever the frame last was over a pay run
        // — the exact corruption this change removes. Stepping OVER it keeps
        // Page Up / Page Down monotonic; refusing to move would strand the
        // operator at that row with no way past it in either direction.
        let next: DayBookEntry | undefined;
        let skipped = 0;
        for (let i = idx + delta; i >= 0 && i < entries.length; i += delta) {
          const candidate = entries[i] as DayBookEntry;
          if (voucherTypeForClass(candidate.class)) {
            next = candidate;
            break;
          }
          skipped += 1;
        }
        if (!next) {
          return delta < 0
            ? "Already at the first voucher."
            : "Already at the last voucher.";
        }
        setLoaded({ entry: next });
        // Re-class the frame from the voucher now loaded. Fires AFTER setLoaded so
        // the screen never renders a class the loaded document is not.
        onSelect(dayBookRowKey(next));
        const note =
          skipped > 0
            ? ` — skipped ${skipped} voucher${skipped === 1 ? "" : "s"} with no entry screen`
            : "";
        return `Voucher ${next.voucher_no} (${next.date})${note}`;
      };
      if (dayBookRef.current) return apply(dayBookRef.current);
      client
        .getDayBook(companyID, {})
        .then((res) => {
          dayBookRef.current = res.entries;
          apply(res.entries);
        })
        .catch(() => {
          /* the status line already told the user we are loading */
        });
      return "Loading the Day Book to browse vouchers…";
    },
    [client, companyID, currentID, setLoaded, onSelect],
  );

  useRegisterScreenAction("prevVoucher", () => stepVoucher(-1));
  useRegisterScreenAction("nextVoucher", () => stepVoucher(1));
}

/**
 * The voucher entry/alteration screen. Holds the real form state, drives the
 * LIVE backend on Ctrl+A (Accept) and Alt+D (Delete) via {@link editActions},
 * and surfaces the server-resolved voucher_no + the server-DERIVED GST split.
 * The XState navMachine stays pure (it only moves the field cursor / screen
 * stack); ALL form state + side effects live here.
 */
function AccountingVoucherScreen({
  snapshot,
  onSelect,
}: {
  snapshot: NavSnapshot;
  onSelect: (item: string) => void;
}) {
  const client = useClient();
  const companyID = useCompanyID();
  const homeStateCode = useHomeStateCode();
  const { loaded, setLoaded } = useLoadedVoucher();
  const { loaded: loadedDraft, setLoaded: setLoadedDraft } = useLoadedDraft();

  const type = (snapshot.voucherType ?? "sales") as VoucherType;
  const mode = snapshot.mode ?? "create";
  const isAlter = mode === "alter";
  const cfg = configFor(type);

  // DRAFT MODE: the F9 Purchase create screen hydrated from an OCR draft. Active
  // only for a fresh (non-alter) purchase with a loaded draft. The POST on
  // accept sends the ORIGINAL balanced body (acceptPurchaseDraft), NOT a form
  // rebuild — the form prefill below is display-only.
  const isDraftMode =
    type === "purchase" && !isAlter && loadedDraft != null;

  // SINGLE vs DOUBLE entry (Ctrl+H). Undefined config means "the class default",
  // so Payment / Receipt / Contra open in SINGLE entry the way a Tally operator
  // expects, and a class that has no single form (Journal, and the GST vouchers)
  // is forced to double regardless of what a stale frame config says.

  // On an alter frame, the loaded report row gives us the logical voucher_id;
  // the FORM is hydrated from GET /vouchers/{id} (real posting uuids + GST
  // attrs), NOT the day-book names. The loaded voucher_id becomes ctx.voucherID
  // so Ctrl+A routes to the per-class ALTER (PUT), not a create.
  // THE PARTY PICKER'S OPTIONS, from the company's real chart of accounts.
  // Read once per company: the list is a master, not a transaction, and a
  // refetch on every keystroke would make the picker feel broken.
  const [parties, setParties] = useState<PartyOption[]>([]);
  const [partiesError, setPartiesError] = useState<string | null>(null);
  /**
   * THE WHOLE CHART, kept beside the filtered party list.
   *
   * ⛔ `parties` is filtered to PARTY_GROUPS and drops every field but three, and
   * neither is enough here. Bill-wise references need `maintain_bill_by_bill` and
   * the GROUP (which decides whether an open bill is a receivable or a payable),
   * and in SINGLE ENTRY the bill-bearing ledger is a free-typed PARTICULAR that
   * need not be in PARTY_GROUPS at all. Resolving it by name against the full
   * chart is the only way the picker can know a row is bill-by-bill.
   */
  const [chart, setChart] = useState<LedgerListItem[]>([]);
  useEffect(() => {
    let live = true;
    client
      .listLedgers(companyID)
      .then((res) => {
        if (!live) return;
        setChart(res.ledgers);
        setParties(
          res.ledgers
            .filter((l) => PARTY_GROUPS.includes(l.group_name))
            .map((l) => ({ id: l.id, name: l.name, stateCode: l.state_code }))
            .sort((a, b) => a.name.localeCompare(b.name)),
        );
        setPartiesError(null);
      })
      .catch((e: unknown) => {
        if (!live) return;
        // NOT silent. An empty picker with no explanation reads as "this
        // company has no customers", which is a different and much more
        // alarming statement than "the list could not be loaded".
        setPartiesError(
          e instanceof ApiError
            ? `Chart of accounts unavailable (${e.status}). ${e.message}`
            : e instanceof Error
              ? e.message
              : String(e),
        );
      });
    return () => {
      live = false;
    };
  }, [client, companyID]);
  const partyNames = parties.map((p) => p.name);

  // THE INVENTORY MASTERS the item grid picks from. Fetched only when this
  // class can key an item invoice, so a Payment screen never asks a Data Entry
  // operator's session for stock masters it will not render.
  //
  // A failure is SURFACED, never swallowed: an empty item picker with no
  // explanation reads as "this company sells nothing", which is a different and
  // much more alarming statement than "the list could not be loaded".
  const [stockItems, setStockItems] = useState<StockItemListItem[]>([]);
  const [godowns, setGodowns] = useState<GodownListItem[]>([]);
  const [stockError, setStockError] = useState<string | null>(null);
  const wantsStock = cfg.itemInvoice;
  useEffect(() => {
    if (!wantsStock) return;
    let live = true;
    Promise.all([client.listStockItems(companyID), client.listGodowns(companyID)])
      .then(([items, gd]) => {
        if (!live) return;
        setStockItems(items.stock_items);
        setGodowns(gd.godowns);
        // ⛔ THE ITEM GRID GETS THE SAME DEFAULT POSITION AS THE STOCK GRIDS, AND
        // HERE THE OLD DEFAULT WAS NOT SILENT — IT WAS FATAL.
        //
        // emptyItemLine() starts godownId "", which posts as SQL NULL: a real
        // position that, in a company with godowns, holds nothing. The
        // negative-stock check is per (item, godown, batch), so an F8 item sale
        // keyed without touching this select is measured against the NULL
        // position and REFUSED — measured in a browser 2026-08-07:
        //   "negative stock refused for item MS Angle 50x50 (godown -, batch -):
        //    closing balance would be -5.0000"
        // on a company holding 195 of that item at Main Location. An operator
        // cannot sell stock they own.
        //
        // ⚠️ AND ON F9 IT IS SILENT AGAIN: a PURCHASE brings goods IN, so there
        // is no negative-stock check to refuse it — the units simply land on the
        // phantom position and the warehouse is split in two.
        const def = defaultGodownOf(gd.godowns);
        if (def !== "") {
          setForm((f) => ({
            ...f,
            items: f.items.map((r) =>
              r.stockItemId === "" && r.qty === "" && r.godownId === ""
                ? { ...r, godownId: def }
                : r,
            ),
          }));
        }
        setStockError(null);
      })
      .catch((e: unknown) => {
        if (!live) return;
        setStockError(
          e instanceof ApiError
            ? `Stock masters unavailable (${e.status}). ${e.message}`
            : e instanceof Error
              ? e.message
              : String(e),
        );
      });
    return () => {
      live = false;
    };
  }, [client, companyID, wantsStock]);

  const loadedEntry = isAlter ? (loaded?.entry ?? null) : null;
  const loadedVoucherID = loadedEntry?.voucher_id ?? null;
  const seedRef = useRef<string | null>(null);
  const draftSeedRef = useRef<string | null>(null);
  const [form, setForm] = useState<VoucherForm>(emptyForm);
  /**
   * ⛔ DERIVED AFTER `form`, AND FROM THE VOUCHER'S OWN CONTENTS ON AN ALTER.
   *
   * A Day Book drill builds its frame with no entryMode, so this used to fall to
   * "accounting-invoice" on every GST class — the item grid was not rendered and
   * the accept sent no `inventory`, which on an ALTER means "this voucher moves
   * no goods" and DROPPED THE MOVEMENT. See effectiveEntryMode's third parameter
   * for the measurement (190 -> 195 units, silently, on a re-accept).
   *
   * `form.items` is hydrated from GET /vouchers/{id} by itemsFromView, so a
   * keyed row here means the STORED voucher carries stock.
   */
  const alterHasStock =
    isAlter && form.items.some((l) => l.stockItemId.trim() !== "");
  const entryMode = effectiveEntryMode(
    type,
    snapshot.config?.entryMode,
    alterHasStock,
  );
  /**
   * Whether the one master field at the top of the form means "the ACCOUNT the
   * money moved through" (single entry) or "the PARTY this document is for"
   * (everything else). They are different facts and, since D59, different form
   * fields — see VoucherForm.account.
   */
  const isSingleEntry = entryMode === "single";

  const [status, setStatus] = useState<string | null>(null);
  const [savedNo, setSavedNo] = useState<string | null>(null);
  /**
   * The voucher_id of a voucher JUST SAVED in create mode, so Ctrl+P can print
   * it. This is the whole of docs/design/t1t4-ui-model.md §2.1: the handoff's
   * §3.1 complaint is that "an operator cannot print the invoice from the screen
   * they keyed it on", and requiring an alteration first would have left that
   * standing. The create response already carries voucher_id — it was simply
   * being discarded.
   *
   * It is CLEARED wherever the form's identity is dropped (Alt+X cancel, Alt+2
   * duplicate), because a Ctrl+P that printed the previous voucher would be worse
   * than one that refuses. Invariant I-6 pins that, with a mutation proof.
   */
  const [savedVoucherID, setSavedVoucherID] = useState<string | null>(null);
  // The server-derived GST split from the last accept (display-only).
  const [taxResult, setTaxResult] = useState<{
    cgst: string;
    sgst: string;
    igst: string;
  } | null>(null);
  // Guard against a double-submit while an accept is in flight.
  const inFlight = useRef(false);

  // =========================================================================
  // ⛔⛔ RESET THE FORM WHEN A **CREATE** FRAME CHANGES CLASS.
  //
  // THIS IS A LIVE, PRE-EXISTING, MONEY-CORRUPTING DEFECT, and it was invisible
  // for exactly the reason the party field was blank.
  //
  // `AccountingVoucherScreen` is NOT remounted when the frame's voucher type
  // changes — F8 and F6 render the same component instance — and nothing ever
  // reset `form`. So `party` / `partyLedgerId` SURVIVED from one voucher into
  // the next. Proved by driving the shell:
  //
  //     F8, pick "Acme Traders", 10,000 @ 18%, Ctrl+A   -> Sales saved
  //     F6, key ONLY the particular (Electricity 2,500), Ctrl+A
  //     -> the RECEIPT posted  DR Acme Traders 2,500 / CR Electricity 2,500
  //
  // The receipt debited the previous voucher's CUSTOMER instead of a bank, on a
  // voucher that BALANCES and that no server check can refuse — `party` is a
  // free string and a debtor is a perfectly legal thing to debit. The operator
  // could not see it either, because `SelectionList` was uncontrolled and the
  // Account field rendered EMPTY over the stale value. Two invisibilities
  // compounding: the state leaked, and the one control that would have shown it
  // was blank.
  //
  // ⚠️ FOUND ONLY BECAUSE THE `value` PROP MADE IT VISIBLE. Adding the hydrated
  // party to the field turned three passing tests red — and they were red for a
  // real reason. HANDOFF §9.1a item 12's rule, inverted: when a fix makes state
  // visible, re-check what that state was doing while nobody could see it.
  //
  // ⚠️ THE `loadedVoucherID || isDraftMode` GUARD IS DEFENSIVE, AND SAYING SO
  // HONESTLY MATTERS MORE THAN THE GUARD DOES. A drill, a Page Up/Down step and
  // a draft prefill all change `type` while a document is loading, and their
  // own effects own the form — so resetting there COULD race a hydration and
  // blank a voucher the operator just opened.
  //
  // ⛔ BUT NO TEST CURRENTLY DISTINGUISHES IT, AND THAT WAS MEASURED, NOT
  // ASSUMED. Deleting this line leaves the whole suite green, including the
  // cross-class drill written specifically to catch it: on every path found so
  // far the reset runs first and the async `getVoucher` re-seeds the form
  // afterwards, so the end state is correct either way. The guard is kept
  // because "a document owns the form" is correct by construction and the cost
  // of being wrong is a blanked voucher — not because anything proves it fires.
  //
  // If you are tempted to treat the cross-class drill test as proof of this
  // line, it is not. It proves the drill hydrates; it does not reach the race.
  // =========================================================================
  const formClassRef = useRef(type);
  useEffect(() => {
    if (formClassRef.current === type) return;
    formClassRef.current = type;
    if (loadedVoucherID || isDraftMode) return; // a hydration owns the form
    setForm(emptyForm());
    setStatus(null);
    setSavedNo(null);
    setSavedVoucherID(null);
    setTaxResult(null);
  }, [type, loadedVoucherID, isDraftMode]);

  // Hydrate the alter form from the GET endpoint when a new voucher is drilled.
  //
  // The "already seeded" guard is stamped only AFTER the response is applied,
  // never before the fetch. Under React StrictMode every effect runs twice: the
  // first run's cleanup sets cancelled=true (so its response is dropped) and, if
  // the guard had already been stamped, the second run would return early — the
  // drilled voucher would then load into an EMPTY form, silently. (Ctrl+A on it
  // would then alter a real voucher into blank lines.) Stamping after the apply
  // costs one extra idempotent GET under StrictMode and makes the seed correct.
  useEffect(() => {
    if (!loadedVoucherID || seedRef.current === loadedVoucherID) return;
    let cancelled = false;
    client
      .getVoucher(companyID, loadedVoucherID)
      .then((view) => {
        if (cancelled) return;
        seedRef.current = loadedVoucherID;
        setForm(formFromView(view));
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setStatus(
            `Error loading voucher: ${err instanceof Error ? err.message : String(err)}`,
          );
        }
      });
    return () => {
      cancelled = true;
    };
  }, [client, companyID, loadedVoucherID]);

  // Seed the form from an OCR draft when entering draft mode (keyed by draft_id,
  // mirroring the alter seedRef guard so a re-render doesn't clobber edits and a
  // fresh draft re-seeds). Prefill is display-only; accept POSTs the orig body.
  // draftSeedFormRef holds the pristine form the draft seeded, so an edit is
  // detected by comparing the live form to it (no parallel dirty flags to keep
  // in sync). Cleared/re-set whenever a fresh draft seeds.
  const draftSeedFormRef = useRef<VoucherForm | null>(null);
  const draftID = isDraftMode ? loadedDraft.draft.draft_id : null;
  useEffect(() => {
    if (!draftID || draftSeedRef.current === draftID) return;
    draftSeedRef.current = draftID;
    if (loadedDraft) {
      const seeded = formFromDraft(loadedDraft.draft);
      draftSeedFormRef.current = seeded;
      setForm(seeded);
    }
  }, [draftID, loadedDraft]);

  // Keep the latest form/ctx in refs so the registered action handlers (stable
  // across renders) always read current state without re-registering.
  const formRef = useRef(form);
  formRef.current = form;

  const editCtx = useMemo<VoucherEditContext>(() => {
    const ctx: VoucherEditContext = { type, mode };
    if (isAlter && loadedVoucherID) {
      return { ...ctx, voucherID: loadedVoucherID };
    }
    return ctx;
  }, [type, mode, isAlter, loadedVoucherID]);
  const ctxRef = useRef(editCtx);
  ctxRef.current = editCtx;

  /**
   * SINGLE-ENTRY -> a balanced DOUBLE-ENTRY request.
   *
   * Single entry is a data-capture affordance, not a different accounting
   * model: what gets posted is an ordinary balanced voucher. The user names one
   * account and lists what it moved against; this puts the SUM on the single
   * side and each line on the other, oriented by the class —
   *
   *   Payment  (primarySide CR): Cr Bank (total), Dr each expense/party
   *   Receipt  (primarySide DR): Dr Bank (total), Cr each income/party
   *   Contra   (primarySide CR): Cr the account paid FROM, Dr the one paid INTO
   *
   * Getting that orientation backwards is the failure this project has already
   * shipped once (WS8b: a purchase posted through the sales-shaped builder came
   * out REVERSED and still passed the server's dr_cr-blind GST verify). Here the
   * server's double-entry constraint catches an unbalanced body, but NOT a
   * reversed one — so the orientation is pinned by test, not by the server.
   */
  const buildSingleEntryRequest = useCallback(
    async (f: VoucherForm): Promise<VoucherRequest> => {
      const rows = keyedParticulars(f);
      const accountName = f.account.trim();
      const manySide: "DR" | "CR" = cfg.primarySide === "DR" ? "CR" : "DR";

      // ⛔⛔ THE MASTER GATE. Nothing below this line may create a ledger for a
      // form this client already knows the server will refuse — see the gate's
      // header. D39 was DRIVEN on this grid: a cost axis short by any amount
      // left one new account per named particular behind, and the operator was
      // shown only the 422.
      return resolveMasters(singleEntryRefusal(f, cfg), async () => {
        // The single side takes the SUM, computed in paise so a two-line entry
        // cannot drift by a rounding cent.
        const paise = rows.reduce(
          (sum, l) => sum + Math.round(parseFloat(l.amount || "0") * 10000),
          0,
        );
        const total = (paise / 10000).toFixed(4);

        const accountID =
          f.accountLedgerId ||
          (await ensureLedger(client, companyID, {
            name: accountName,
            groupName: "Bank Accounts",
          }));

        const postings: PostingRequest[] = [
          { ledger_id: accountID, dr_cr: cfg.primarySide, amount: total },
        ];
        for (const l of rows) {
          const id = await ensureLedger(client, companyID, {
            name: l.ledger.trim(),
            // A payment's counter-ledgers are usually expenses, a receipt's
            // usually income. Both are create-or-select: an EXISTING ledger keeps
            // its own group and this is never consulted.
            groupName: cfg.primarySide === "CR" ? "Indirect Expenses" : "Direct Incomes",
          });
          postings.push({
            ledger_id: id,
            dr_cr: manySide,
            amount: normalizeMoney(l.amount),
          });
        }

        // ---- WHO THE DOCUMENT IS FOR (D59).
        //
        // ⛔ NOT THE ACCOUNT FIELD. `party` / `party_ledger_id` are what the
        // printed receipt and payment name — "Received From" / "Paid To" — and
        // naming the BANK there made every one of them state that the money came
        // from, or went to, the operator's own bank. The counterparty is a
        // PARTICULAR; see singleEntryCounterparty for the three cases and for why
        // it names nobody rather than guessing between several.
        //
        // ⚠️ The ids are the ones the loop above just resolved, in the same order,
        // so the counterparty is the ledger actually posted rather than a lookup
        // that could drift from it.
        const counterparty = singleEntryCounterparty(
          rows.map((l, idx) => ({
            name: l.ledger.trim(),
            ledgerId: postings[idx + 1]!.ledger_id,
          })),
          chart,
        );

        const req: VoucherRequest = {
          voucher_date: f.date,
          narration: f.narration,
          party: counterparty?.name ?? "",
          postings,
        };
        if (counterparty?.ledgerId) req.party_ledger_id = counterparty.ledgerId;
        if (f.voucherNo) req.voucher_no = f.voucherNo;
        // ---- THE COST SPLITS (T2).
        //
        // ⛔ THE OFFSET IS 1, AND IT IS THE WHOLE HAZARD OF THIS FEATURE. `postings`
        // above is built as [ the account being paid from/into, ...the particulars ],
        // so the particular the operator sees at row 0 is posting **1**. Passing 0 here
        // would allocate the BANK leg — a departmental cost attributed to a cash
        // account, with a voucher that balances and a Cost Centre Summary that foots.
        // Nothing downstream can detect it.
        //
        // It is passed from HERE, next to the array construction, rather than inferred
        // inside the builder, because this is the only place that knows the shape.
        //
        // ⚠️ `rows` is the FILTERED list (keyed particulars only) and it is the same
        // array the postings loop above iterated, so the positions agree by
        // construction — the same discipline the fulfilment links use.
        //
        // ⚠️ AND EVERY AXIS ON EVERY ROW HAS ALREADY BEEN FOUND COMPLETE, up in the
        // gate. `buildCostAllocations` DROPS a half-picked split silently, so a form
        // that reached here with a short axis would send one the server refuses —
        // after the ledgers above had been created. That is D39.
        const allocations = buildCostAllocations(rows, 1);
        if (allocations.length > 0) req.cost_allocations = allocations;

        // ---- THE BILL REFERENCES (what this voucher settles).
        //
        // ⛔ THE LEG IS A PARTICULAR, NOT THE PARTY FIELD, and that is the whole
        // hazard here — the mirror image of the cost-split offset above. In single
        // entry `postings[0]` is the BANK; the customer whose invoice is being
        // settled is one of these ROWS. A reference attached to the account leg
        // would credit the bank's own bill.
        //
        // ⚠️ The ids come from the SAME `ensureLedger` resolution the postings loop
        // performed, in the same order, so the reference and the posting it must
        // total against are the same ledger by construction rather than by lookup.
        const billAllocations = rows.flatMap((l, idx) =>
          buildBillAllocations(
            {
              ledgerId: postings[idx + 1]!.ledger_id,
              drCr: manySide,
              amount: normalizeMoney(l.amount),
            },
            l.billRefs,
          ),
        );
        if (billAllocations.length > 0) req.bill_allocations = billAllocations;
        return req;
      });
    },
    [client, companyID, cfg, chart],
  );

  /**
   * BUILD A DOUBLE-ENTRY REQUEST — the Journal (F7), and Payment / Receipt /
   * Contra under Ctrl+H > Double Entry.
   *
   * ⛔ THE OPPOSITE OPERATION TO `buildSingleEntryRequest`, AND NOT A VARIANT OF
   * IT. Single entry names one account and puts the SUM of the particulars on
   * it; here every line states its own side and its own amount and nothing is
   * synthesised. `postings` is therefore the keyed rows IN ORDER, one-for-one
   * from index 0.
   *
   * ⛔⛔ VALIDATE BEFORE RESOLVING, AND THE ORDER OF THESE TWO BLOCKS IS A
   * CORRECTNESS PROPERTY, NOT A STYLE CHOICE. `ensureLedger` CREATES a ledger
   * that does not exist yet. Validating after resolution would mean every
   * refused Ctrl+A on a misspelled or unbalanced grid leaves a real account
   * behind in the chart of accounts: press Ctrl+A five times on an entry that
   * is 200 short and the company silently gains five ledgers and no voucher,
   * with nothing on screen saying so. Pinned by
   * "an unbalanced grid creates no ledger".
   */
  const buildDoubleEntryRequest = useCallback(
    async (f: VoucherForm): Promise<VoucherRequest> => {
      // ⚠️ THE SAME `keyedLines` THE REFUSAL USES, and the same array the cost
      // splits are indexed against. Two independently-written filters that
      // agree today are how an allocation lands on the row above the one it
      // was keyed against.
      const rows = keyedLines(f.drCrLines);

      // ---- REFUSE FIRST. Nothing inside this closure may run on a bad grid.
      //
      // ⛔⛔ THE BALANCE CHECK WAS NOT THE WHOLE REFUSAL, AND THIS FILE ALREADY
      // CLAIMED IT WAS. `doubleEntryRefusal` alone stood here under a comment
      // saying an `ensure*` must never run before validation — and D39 was still
      // DRIVEN on this very grid, because a cost axis short by any amount is
      // refused by the server and by a deferred DB trigger and by NOTHING here.
      // The row-level refusals are now part of the same gate expression.
      return resolveMasters(
        doubleEntryRefusal(f.drCrLines) ||
          doubleEntryRowRefusal(rows) ||
          // ⛔ THE STATUTORY BLOCK IS PART OF THE SAME GATE (D3). A half-keyed
          // deduction is refused by the server AFTER `ensureLedger` has created
          // every named account — which is D39's shape exactly, and the reason
          // every refusal on this form lives in one expression rather than in
          // whichever check happened to be written first.
          (cfg.statutoryTax === null
            ? ""
            : taxDeductionRefusal(f.tax, cfg.statutoryTax) ||
              taxLegRefusal(f.tax, cfg.statutoryTax, rows.map((l) => l.ledger))),
        async () => {
        const postings: PostingRequest[] = [];
        for (const l of rows) {
          const id = await ensureLedger(client, companyID, {
            name: l.ledger.trim(),
            // ⚠️ A GUESS, AND IT ONLY EVER BITES ON A LEDGER THAT DOES NOT EXIST
            // YET — an EXISTING ledger keeps its own group and this is never
            // consulted (see `ensureLedger`, which resolves before it creates).
            // A Journal's counter-account is genuinely unknowable from its side:
            // a provision debits an expense and credits a LIABILITY, where this
            // guesses an income. The panel says so, and the honest answer for a
            // real new account is to create it on the masters screen with its
            // right group rather than to infer one from a keystroke.
            groupName: l.side === "DR" ? "Indirect Expenses" : "Indirect Incomes",
          });
          postings.push({
            ledger_id: id,
            dr_cr: l.side,
            amount: normalizeMoney(l.amount),
          });
        }

        const req: VoucherRequest = {
          voucher_date: f.date,
          narration: f.narration,
          // A Journal names no party — the field is informational and usually
          // blank. It is forwarded rather than dropped because the three
          // single-entry classes reach this builder through Ctrl+H and DO name
          // one, and because an alter must resend what it loaded.
          party: f.party,
          postings,
        };
        if (f.partyLedgerId) req.party_ledger_id = f.partyLedgerId;
        if (f.voucherNo) req.voucher_no = f.voucherNo;

        // ---- THE STATUTORY DEDUCTION / COLLECTION (D3).
        //
        // ⛔ THE LEG ID COMES FROM THE ARRAY JUST BUILT, BY POSITION — the same
        // discipline the cost-split offset follows, and for the same reason. The
        // server compares `tds_ledger_id` against the POSTED legs; an id from a
        // second lookup that resolved differently would match no posting, and
        // the verification loop would then sum zero and compare it against the
        // derived tax — a mismatch nobody can explain, or a silent pass that
        // verified nothing.
        const legIDForRow = (ledgerName: string): string => {
          const idx = rows.findIndex(
            (l) => l.ledger.trim().toLowerCase() === ledgerName.trim().toLowerCase(),
          );
          return idx < 0 ? "" : postings[idx]!.ledger_id;
        };
        // ⛔ A CLASS THAT CANNOT CARRY ONE SENDS NOTHING, even if the form state
        // somehow holds a block (a Ctrl+H between classes, a hydration). The
        // panel is not rendered for such a class, so a block reaching here would
        // be one nobody could see — and an invisible statutory declaration is
        // the undisclosed-figure defect, not a convenience.
        if (cfg.statutoryTax === "TDS") {
          const tds = buildTDSRequest(f.tax, legIDForRow);
          if (tds) req.tds = tds;
        } else if (cfg.statutoryTax === "TCS") {
          const tcs = buildTCSRequest(f.tax, legIDForRow);
          if (tcs) req.tcs = tcs;
        }

        // ---- THE COST SPLITS (T2).
        //
        // ⛔ THE OFFSET IS 0, AND THIS IS THE SECOND CALL SITE THAT PARAMETER WAS
        // ADDED FOR. `postings` above is exactly `rows`, in order, with no leg
        // in front of it — so the row the operator sees at index 0 IS posting 0.
        // Single entry passes 1 because its array is [the account paid from,
        // ...the particulars]. The two cases differ by one and the wrong one is
        // SILENT: the split would attach to a real ledger with a plausible
        // amount, on a voucher that balances perfectly and a Cost Centre Summary
        // that foots. Nothing downstream can detect it.
        //
        // ⚠️ This also closes the gap `CostCentresScreen` documents on its face:
        // allocating a Journal previously had to arrive over HTTP, because no
        // screen could key one at all.
        const allocations = buildCostAllocations(rows, 0);
        if (allocations.length > 0) req.cost_allocations = allocations;

        // ---- THE BILL REFERENCES.
        //
        // ⛔ THE LEG IS A ROW, exactly as in single entry and for the same
        // reason: a Journal's party field names no ledger, so the debtor being
        // written off is one of these lines. The side comes from the ROW rather
        // than from the class, because a Journal has no natural side — that is
        // what makes it a Journal.
        //
        // ⚠️ The ids come from the SAME `ensureLedger` resolution the loop above
        // performed, in the same order, so a reference and the posting it must
        // total against are the same ledger by construction rather than by
        // lookup. The server checks that total to the paisa, inside the
        // transaction, against the FINAL postings.
        const billAllocations = rows.flatMap((l, idx) =>
          buildBillAllocations(
            {
              ledgerId: postings[idx]!.ledger_id,
              drCr: l.side,
              amount: normalizeMoney(l.amount),
            },
            l.billRefs,
          ),
        );
        if (billAllocations.length > 0) req.bill_allocations = billAllocations;
        return req;
        },
      );
    },
    [client, companyID],
  );


  /**
   * The chart entry for a ledger the operator NAMED, or for a uuid the form holds.
   *
   * ⚠️ Resolved by NAME for a single-entry particular because that is all the row
   * carries until accept — `ensureLedger` mints the uuid at send time. A name
   * that matches nothing is a ledger about to be CREATED, and a brand-new ledger
   * is never bill-by-bill, so returning undefined is the right answer rather than
   * a missing case.
   */
  const ledgerByName = useCallback(
    (name: string): LedgerListItem | undefined =>
      chart.find((l) => l.name === name.trim()),
    [chart],
  );
  const ledgerById = useCallback(
    (id: string): LedgerListItem | undefined => chart.find((l) => l.id === id),
    [chart],
  );

  /**
   * Which ageing report a ledger's open bills come from.
   *
   * A Sundry Debtor's bills are RECEIVABLE; everything else party-shaped is
   * treated as PAYABLE. The distinction decides which list the settle picker
   * offers, and offering the wrong one shows an operator no bills at all.
   */
  const kindForGroup = (group: string): "RECEIVABLE" | "PAYABLE" =>
    group === "Sundry Creditors" ? "PAYABLE" : "RECEIVABLE";

  /**
   * THE PARTY WHOSE BILL THIS INVOICE OPENS — the raise path only.
   *
   * Undefined on every class that does not bill a party (Contra, the stock
   * vouchers) and on single entry, where the bill-bearing leg is a PARTICULAR and
   * the panel belongs on the row instead.
   */
  const raiseParty =
    entryMode === "item-invoice" || cfg.showGst
      ? ledgerById(form.partyLedgerId)
      : undefined;

  /**
   * The gross the party will be posted, through the SHARED helpers so the figure
   * shown here is the figure the builder sends. Never a local sum.
   */
  /** The party's net posting in a HYDRATED form, or null on a fresh create. */
  const partyLegAmountFromPostings = (f: VoucherForm): string | null =>
    f.postings.some((p) => p.ledger_id === f.partyLedgerId)
      ? partyLeg(f).amount
      : null;

  /** The gross a FRESH invoice comes to, through the shared helpers. */
  const deriveRaiseGross = (): string =>
    entryMode === "item-invoice"
      ? itemInvoiceGross(
          joinMasters(form.items, stockItems),
          homeStateCode,
          form.gst.placeOfSupply,
        )
      : // The DEFAULT reference tracks what the party owes, charges included —
        // the same definition the refusal above uses. An operator who keys a
        // charge must not have to notice that the reference stopped matching.
        gstVoucherPartyTotal(
          normalizeMoney(form.taxableValue),
          form.gst.gstRatePct,
          homeStateCode,
          form.gst.placeOfSupply,
          form.gst.charges,
        );

  const raiseLeg: BillBearingLeg = {
    ledgerId: form.partyLedgerId,
    // A purchase credits the creditor; everything else debits the debtor.
    drCr: type === "purchase" ? "CR" : "DR",
    // ⛔ THE HYDRATED POSTINGS WIN WHEN THERE ARE ANY, AND THAT IS AN ALTER.
    // A GET returns the voucher's taxable value and its derived tax but NOT the
    // RATE — the rate is not stored, only what it produced. So re-deriving the
    // gross on an alter reads the taxable base with no tax on it (10,000 against
    // an 11,800 invoice) until the operator happens to re-key the rate, and the
    // panel would tell them a correctly-hydrated reference was over-allocated by
    // exactly the tax. The party's posting is right there in the response and is
    // the figure the reference must total, so it is the one to use.
    amount: partyLegAmountFromPostings(form) ?? deriveRaiseGross(),
  };

  /**
   * DEFAULT ONE FULL-VALUE REFERENCE the moment a bill-by-bill party is chosen.
   *
   * ⛔ THE ORDINARY INVOICE MUST COST NOTHING. One bill for the whole invoice is
   * what virtually every sale is; making the operator open a panel and retype the
   * total to get it is how a feature ends up switched off, and switching it off
   * means the receivable never reaches Outstandings — which is the entire defect
   * this track exists to close.
   *
   * ⚠️ It fires only when there are NO references yet, so it can never overwrite a
   * split the operator keyed, and never re-adds one they deliberately removed
   * within the same edit. On an ALTER the hydrated references are already there,
   * so this is a no-op — which is what stops it from re-defaulting a voucher whose
   * real references were something else.
   */
  const hasRaiseParty = raiseParty?.maintain_bill_by_bill === true;
  const raiseLegAmount = raiseLeg.amount;
  const raiseLegId = raiseLeg.ledgerId;
  const raiseLegSide = raiseLeg.drCr;
  useEffect(() => {
    if (!hasRaiseParty) return;
    const leg: BillBearingLeg = {
      ledgerId: raiseLegId,
      drCr: raiseLegSide,
      amount: raiseLegAmount,
    };
    setForm((f) => {
      if (f.billRefs.length === 0) return { ...f, billRefs: defaultNewRef(leg) };
      // ⛔ AND RE-TRACK IT. The party is picked BEFORE the value is keyed, so the
      // default is created against a total of zero. Without this the form would
      // refuse every invoice it had itself defaulted — see DraftBillRef.auto.
      const retracked = retrackAutoRef(f.billRefs, leg);
      return retracked === null ? f : { ...f, billRefs: retracked };
    });
    // The leg object is rebuilt every render; its three FIELDS are the only
    // things the default depends on, and depending on the object would loop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasRaiseParty, raiseLegAmount, raiseLegId, raiseLegSide]);

  /** Build a plain (non-GST) request: forward the loaded/seeded postings verbatim. */
  const buildPlainRequest = useCallback((f: VoucherForm): VoucherRequest => {
    const req: VoucherRequest = {
      voucher_date: f.date,
      narration: f.narration,
      party: f.party,
      postings: f.postings,
    };
    if (f.partyLedgerId) req.party_ledger_id = f.partyLedgerId;
    if (f.voucherNo) req.voucher_no = f.voucherNo;
    // ⛔⛔ RE-SENT, AND OMITTING THIS IS THE DATA LOSS THIS TRACK EXISTS TO FIX.
    // This is the VERBATIM path a hydrated ALTER takes, and `bill_allocation` is
    // keyed on voucher_version_id — so a body without them DELETES the voucher's
    // bill references. Editing a narration would drop an invoice out of
    // Outstandings while leaving it in Sundry Debtors, and nothing anywhere
    // refuses it: zero references is a legal state.
    //
    // ⚠️ The leg is the PARTY, matching formFromView's hydration. A single-entry
    // voucher rebuilt through buildSingleEntryRequest carries its particulars'
    // references instead; the two paths are exclusive.
    const billAllocations = buildBillAllocations(
      partyLeg(f),
      f.billRefs,
    );
    if (billAllocations.length > 0) req.bill_allocations = billAllocations;
    return req;
  }, []);

  /**
   * Build the GST request: (1) ensure the three output-tax ledgers, (2) resolve
   * the real party + income ledger uuids, (3) assemble the balanced postings +
   * GSTRequest via the shared derive helper. The server re-derives and 422s on
   * any mismatch — the safety net keeping it authoritative.
   */
  const buildGstRequest = useCallback(
    async (f: VoucherForm): Promise<VoucherRequest> => {
      // ⛔⛔ THE MASTER GATE. {@link gstRefusal} holds every refusal this form can
      // make — the multi-rate voucher it cannot faithfully restate, the missing
      // orientation, the unsendable rate (D41), the missing Rule 46(g) HSN (D60)
      // and a short bill reference — and NOTHING below may create an account
      // until it comes back empty.
      //
      // ⚠️ THE MULTI-RATE CHECK ALREADY STOOD FIRST HERE, with a comment saying
      // exactly why ("an `ensure*` helper CREATES, so validating after it would
      // leave ledgers behind"), and D41 was STILL DRIVEN in a browser — because
      // the check that was first was not the check that fired. That is why the
      // refusals are one function and the resolvers are inside a closure that
      // cannot run without it.
      return resolveMasters(gstRefusal(f, type, homeStateCode), async () => {
        // ⛔⛔ EVERY ONE OF THE FOUR RESOLUTIONS BELOW IS CLASS-DEPENDENT, AND ALL
        // FOUR USED TO BE HARD-CODED TO THE SALES ANSWER. See GST_ORIENTATION in
        // gstVoucher.ts for what that cost: a purchase booked as a receivable
        // carrying output tax the company does not owe.
        //
        // ⚠️ Non-null by the gate above, which refuses a class with no orientation.
        const orientation = gstOrientationFor(type)!;
        const outputTax = await ensureGstTaxLedgers(
          client,
          companyID,
          orientation.taxKind,
        );
        // Resolve the party uuid: use the hydrated id if present, else ensure by name.
        const partyLedgerId =
          f.partyLedgerId ||
          (await ensureLedger(client, companyID, {
            name: f.party,
            // ⛔ NOT ALWAYS "Sundry Debtors". A supplier created as a DEBTOR is
            // ASSET-natured, and GSTR-1's note-side discriminator keys on the
            // party group's NATURE rather than the voucher class — so it would
            // report a purchase-return debit note as an outward supply.
            groupName: orientation.partyGroup,
            stateCode: f.partyStateCode || undefined,
          }));
        // ⛔⛔ THE HYDRATED HEAD WINS. Ensuring the canonical base here on every
        // accept is what silently moved `Raw Material Purchases` to `Purchases`
        // on an alter that edited nothing — see baseLegFromView. A fresh create
        // has no hydrated head and still gets the canonical one.
        const incomeLedgerId =
          f.baseLedgerId ||
          (await ensureLedger(client, companyID, {
            name: orientation.baseLedger.name,
            groupName: orientation.baseLedger.group,
          }));
        // ---- THE ADDITIONAL CHARGES (register row D3's collection half).
        //
        // ⛔ RESOLVED HERE AND PASSED IN BY ID, because `buildGstVoucherRequest`
        // is pure and creates nothing — the same contract `partyLedgerId` and
        // `incomeLedgerId` above are held to.
        //
        // ⚠️ THE GROUP IS A GUESS AND IT ONLY BITES ON A LEDGER THAT DOES NOT
        // EXIST YET (`ensureLedger` resolves before it creates). "Duties &
        // Taxes" is right for the case this field was built for — a TCS
        // liability — and wrong for a freight recovery, which is an income. The
        // panel says so, and the honest answer for a real new account is the
        // masters screen. It is the same call the Journal's grid makes, stated
        // in the same words.
        const charges = keyedChargeLines(f.gst.charges);
        const chargeIDs = await Promise.all(
          charges.map((c) =>
            ensureLedger(client, companyID, {
              name: c.ledger.trim(),
              groupName: "Duties & Taxes",
            }),
          ),
        );
        const req = buildGstVoucherRequest({
          voucherType: type,
          partyLedgerId,
          incomeLedgerId,
          taxableValue: normalizeMoney(f.taxableValue),
          supplierStateCode: homeStateCode,
          gst: f.gst,
          outputTax,
          voucherNo: f.voucherNo || undefined,
          voucherDate: f.date,
          narration: f.narration,
          party: f.party,
          billRefs: f.billRefs,
          charges: charges.map((c, i) => ({
            ledgerId: chargeIDs[i]!,
            amount: normalizeMoney(c.amount),
          })),
        });

        // ---- THE STATUTORY COLLECTION (D3's TCS half).
        //
        // ⛔ THE LEG ID COMES FROM THE ARRAY JUST RESOLVED, BY POSITION — the
        // identical discipline the Dr/Cr grid's `legIDForRow` follows. The
        // server compares `tcs_ledger_id` against the POSTED legs and requires
        // the leg to be a CREDIT of exactly the collection it derives; an id
        // from a second lookup would match no posting, and the verification loop
        // would sum zero and compare it against a positive derived figure.
        //
        // ⛔ A CLASS THAT CANNOT CARRY ONE SENDS NOTHING. Only `sales` sets
        // `statutoryTax: "TCS"` — a purchase does not collect tax from its own
        // supplier, and a credit note reversing a collection is a case this
        // build does not handle (FeaturesScreen says so).
        const cfg = configFor(type);
        if (cfg.statutoryTax === "TCS") {
          const tcs = buildTCSRequest(f.tax, (ledgerName) => {
            const idx = charges.findIndex(
              (c) =>
                c.ledger.trim().toLowerCase() === ledgerName.trim().toLowerCase(),
            );
            return idx < 0 ? "" : chargeIDs[idx]!;
          });
          if (tcs) req.tcs = tcs;
        }
        return req;
      });
    },
    [client, companyID, homeStateCode, type],
  );

  /**
   * Build the ITEM-INVOICE request: resolve the party ledger, the ITEM ledger
   * (the sales/purchase account the goods are booked to) and the three tax
   * ledgers, then hand the grid to {@link buildItemInvoiceRequest}.
   *
   * NOTHING here computes an amount. The server generates the GST lines and
   * every posting from the stock lines and refuses a body that also states them
   * — so the two ledger CHOICES below are the whole of the client's accounting
   * responsibility, and both are asserted BY NAME in the tests. Booking a
   * purchase to Domestic Sales would balance perfectly and report the company's
   * costs as revenue; no totals check anywhere can catch that.
   *
   * The grid rows are still joined to the fetched masters, but only so the
   * on-screen tax PREVIEW uses each item's real rate. The rate that is charged
   * comes from the master server-side, never from this request.
   */
  const buildItemRequest = useCallback(
    async (f: VoucherForm): Promise<VoucherRequest> => {
      const rows = joinMasters(f.items, stockItems);
      // ⛔⛔ THE MASTER GATE — the same one the other three builders sit behind.
      // An item invoice ensures FOUR accounts (three tax heads, the party, the
      // sales/purchase ledger) before it posts, so a body the server refuses used
      // to leave up to five masters behind on a single Ctrl+A.
      return resolveMasters(
        itemInvoiceRefusal(f, type, rows, homeStateCode),
        async () => {
          // ⛔ THE TAX KIND IS THE CLASS'S HERE TOO, AND THIS LINE READ
          // `ensureOutputTaxLedgers` UNCONDITIONALLY. Item mode escaped the
          // posting-DIRECTION defect — the server derives the postings from the grid
          // — but not the LEDGER half of it: an item-mode PURCHASE booked its input
          // credit into "Output CGST", i.e. against the company's output-tax
          // liability instead of its reclaimable input credit. The direction was
          // right, the account was wrong, and it balanced.
          //
          // ⚠️ This is the "fixing the bug is not fixing the class" lesson that
          // ensureOutputTaxLedgers' OWN header already tells about itself, one
          // caller away — so it is repeated here rather than left to be rediscovered.
          //
          // ⚠️ Non-null by the gate above, which refuses a class with no orientation.
          const orientation = gstOrientationFor(type)!;
          const outputTax = await ensureGstTaxLedgers(
            client,
            companyID,
            orientation.taxKind,
          );
          const partyLedgerId =
            f.partyLedgerId ||
            (await ensureLedger(client, companyID, {
              name: f.party,
              groupName: orientation.partyGroup,
              stateCode: f.partyStateCode || undefined,
            }));
          const itemLedgerId = await ensureLedger(client, companyID, {
            name: orientation.baseLedger.name,
            groupName: orientation.baseLedger.group,
          });
          return buildItemInvoiceRequest({
            // A sale takes goods OUT of the godown; a purchase brings them IN. The
            // server refuses the other way round: a sale that ADDED stock would
            // inflate the P&L and the closing inventory at the same time.
            direction: type === "purchase" ? "IN" : "OUT",
            partyLedgerId,
            itemLedgerId,
            outputTax,
            supplierStateCode: homeStateCode,
            placeOfSupplyStateCode: f.gst.placeOfSupply,
            lines: rows,
            voucherNo: f.voucherNo || undefined,
            voucherDate: f.date,
            narration: f.narration,
            party: f.party,
            billRefs: f.billRefs,
          });
        },
      );
    },
    [client, companyID, homeStateCode, type, stockItems],
  );

  // Ctrl+A Accept: POST (create) or PUT (alter) via editActions, then surface
  // the server-resolved voucher_no AND the server-derived GST split. Runs BEFORE
  // the machine resets the frame.
  // Keep the active draft body in a ref so the stable accept handler reads the
  // current draft without re-registering. null when not in draft mode.
  const draftBodyRef = useRef<VoucherDraftBody | null>(null);
  draftBodyRef.current = isDraftMode ? loadedDraft.draft.body : null;

  const onAccept = useCallback(() => {
    if (inFlight.current) return;
    inFlight.current = true;
    // DRAFT MODE: the ONLY write path is acceptPurchaseDraft, which resolves any
    // null ledger ids (Alt+C create-on-the-fly) then POSTs the ORIGINAL balanced
    // body verbatim to /vouchers/purchase — NOT a form rebuild. Accept is the
    // sole human-in-the-loop write; there is no insightd ledger-write path.
    const draftBody = draftBodyRef.current;
    if (draftBody) {
      // Pristine accept POSTs the balanced draft body byte-identical (the proven
      // path). A DIRTY accept (any editable field changed) rebuilds a balanced,
      // orientation-preserving body from the edits via acceptEditedPurchaseDraft
      // — NOT buildGstVoucherRequest (which is sales-shaped). Nothing posts before
      // this Ctrl+A; HITL is untouched.
      const seed = draftSeedFormRef.current;
      const f = formRef.current;
      const dirty = draftDirtyFields(seed, f);
      // Only rebuild when the shape is editable; a non-rebuildable draft always
      // posts pristine (the disclosed edit-deferred path), never silently errors.
      // ⛔ PASS THE DRAFT ID. Without it the accept-once guard has to identify the
      // draft by EXACT total-body equality, which works but is a heuristic; with it
      // the guard — and insightd's terminal `accepted` marking — are exact by
      // construction. Wired 2026-08-07 after a browser re-drive posted the SAME
      // supplier bill twice: a draft whose invoice number the OCR never read has no
      // number and no date for the day-book layer to match on, so it was accepted,
      // the page was reloaded, and it was accepted again — two real vouchers for one
      // bill (see draftAccept.ts's layer notes).
      const acceptOpts = draftID ? { draftId: draftID } : {};
      const accept =
        dirty.size > 0 && canRebuildDraft(draftBody)
          ? acceptEditedPurchaseDraft(
              client,
              companyID,
              draftBody,
              {
                voucherDate: f.date,
                narration: f.narration,
                taxableValue: f.taxableValue,
                gstRatePct: f.gst.gstRatePct,
                placeOfSupply: f.gst.placeOfSupply,
              },
              acceptOpts,
            )
          : acceptPurchaseDraft(client, companyID, draftBody, acceptOpts);
      accept
        .then((res: VoucherResponse) => {
          setSavedNo(res.voucher_no);
          setSavedVoucherID(res.voucher_id);
          setTaxResult({ cgst: res.cgst, sgst: res.sgst, igst: res.igst });
          setStatus("Saved");
          setLoadedDraft(null);
          draftSeedFormRef.current = null;
        })
        .catch((err: unknown) => {
          setStatus(
            `Error: ${err instanceof Error ? err.message : String(err)}`,
          );
        })
        .finally(() => {
          inFlight.current = false;
        });
      return;
    }
    const f = formRef.current;
    // Capture the edit context SYNCHRONOUSLY, before any async ledger-ensure /
    // GST-derive work: the machine may pop/reset the frame (create<->alter)
    // before the request promise resolves, so we must not re-read ctxRef late or
    // an alter would mis-route to a create (POST instead of PUT).
    const ctx = ctxRef.current;
    const buildRequest = (): Promise<VoucherRequest> =>
      entryMode === "item-invoice"
        ? buildItemRequest(f)
        : cfg.showGst
          ? buildGstRequest(f)
          : entryMode === "single"
            ? buildSingleEntryRequest(f)
            : entryMode === "double"
              ? // ⛔ THE JOURNAL PATH, AND IT REPLACES `buildPlainRequest` HERE.
                // `buildPlainRequest` forwards `f.postings`, which is only ever
                // populated by hydrating a loaded voucher — so on a CREATE it
                // sent an empty array and F7 could not produce a voucher at
                // all. The grid now owns both create and alter for this mode,
                // which is why `formFromView` MUST hydrate `drCrLines`: this
                // builder reads nothing else.
                buildDoubleEntryRequest(f)
              : // The stock classes, whose bodies carry no postings at all and
                // are built by StockVoucherScreen. Left on the verbatim path.
                Promise.resolve(buildPlainRequest(f));
    // =====================================================================
    // ⛔⛔ THE CHART AS IT STOOD BEFORE THIS ACCEPT — D81's HALF, AND IT MUST BE
    // READ BEFORE THE BUILDER RUNS.
    // =====================================================================
    // The gate above guarantees no master is created for a body this CLIENT can
    // refuse. It cannot guarantee the same for a refusal only the SERVER can make
    // (a back-dated cutoff, an ACL denial, a duplicate voucher number, negative
    // stock) — see the gate's own header, which says exactly this and calls it
    // NOT CLOSED. Driven: a GST sales invoice refused for a closed period left
    // "Output CGST", "Output SGST", "Output IGST" and "Domestic Sales" on the
    // chart, and the operator was shown `Error: period is closed`. Four accounts,
    // named by nothing.
    //
    // ⛔ A DIRECT client.listLedgers, NEVER gstVoucher's ledgerIndex. `ensureLedger`
    // writes each id it mints straight into that shared index (gstVoucher.ts's
    // `index.set(spec.name, r.ledger_id)`), so a "before" picture taken from the
    // cache already contains the masters this accept is about to create and the
    // diff comes back EMPTY — the disclosure would be silently absent, which is
    // indistinguishable from there being nothing to disclose.
    //
    // ⚠️ AND IT IS BEST-EFFORT, NEVER FATAL. A voucher must not become unpostable
    // because a read that exists only to word an error message failed. `null` here
    // means "no picture", and no picture means the plain server error — see
    // `discloseOrphanedMasters`.
    const chartBefore: Promise<Set<string> | null> = client
      .listLedgers(companyID)
      .then((r) => new Set(r.ledgers.map((l) => l.id)))
      .catch(() => null);
    chartBefore
      .then((known) =>
        buildRequest().then((req) =>
          acceptVoucher(client, companyID, ctx, req).catch((err: unknown) =>
            discloseOrphanedMasters(client, companyID, req, known, err),
          ),
        ),
      )
      .then((res: VoucherResponse) => {
        setSavedNo(res.voucher_no);
        setSavedVoucherID(res.voucher_id);
        // Display the server-derived split (never the JS-computed one).
        setTaxResult({ cgst: res.cgst, sgst: res.sgst, igst: res.igst });
        setStatus("Saved");
      })
      .catch((err: unknown) => {
        setStatus(`Error: ${err instanceof Error ? err.message : String(err)}`);
      })
      .finally(() => {
        inFlight.current = false;
      });
  }, [
    buildGstRequest,
    buildItemRequest,
    buildPlainRequest,
    buildDoubleEntryRequest,
    cfg.showGst,
    entryMode,
    buildSingleEntryRequest,
    client,
    companyID,
    setLoadedDraft,
  ]);

  // Alt+D Delete (alter mode only): tombstone the loaded voucher, then clear the
  // loaded slot. The machine pops back to the originating report.
  const onDelete = useCallback(() => {
    if (!ctxRef.current.voucherID) return; // create-mode: nothing to delete
    deleteLoadedVoucher(client, companyID, ctxRef.current)
      .then(() => {
        setLoaded(null);
        setStatus("Deleted");
      })
      .catch((err: unknown) => {
        setStatus(`Error: ${err instanceof Error ? err.message : String(err)}`);
      });
  }, [client, companyID, setLoaded]);

  /**
   * Alt+X Cancel. In CREATE mode it clears the form (the machine resets the
   * frame off the same key). A SAVED voucher cannot be "cancelled" here: the
   * ledger is append-only, so the honest answer is Alt+D, which writes a delete
   * tombstone as a new version rather than mutating history.
   */
  const onCancelVoucher = useCallback((): string => {
    if (ctxRef.current.voucherID) {
      return "A saved voucher cannot be cancelled in this build — Alt+D deletes it (appends a delete version; nothing is erased).";
    }
    setForm(emptyForm());
    setStatus(null);
    setSavedNo(null);
    setSavedVoucherID(null);
    setTaxResult(null);
    return "Voucher cancelled — the form is cleared.";
  }, []);

  /**
   * Alt+2 Duplicate. Keeps the loaded voucher's lines but drops its identity
   * (number + loaded slot) so Ctrl+A POSTS a NEW voucher instead of altering the
   * original. The machine flips the frame to create mode off the same key.
   */
  const onDuplicate = useCallback((): string => {
    if (!ctxRef.current.voucherID) {
      return "Nothing to duplicate — open a saved voucher (e.g. from the Day Book) first.";
    }
    setForm((f) => ({ ...f, voucherNo: "" }));
    setSavedNo(null);
    setSavedVoucherID(null);
    setLoaded(null);
    seedRef.current = null;
    return "Duplicated — same lines, new voucher. Ctrl+A to save it.";
  }, [setLoaded]);

  useRegisterScreenAction("accept", onAccept);
  useRegisterScreenAction("delete", isAlter ? onDelete : undefined);
  useRegisterScreenAction("cancelVoucher", onCancelVoucher);
  useRegisterScreenAction("duplicate", onDuplicate);
  // Ctrl+P / Alt+P. An ALTER frame prints the drilled voucher; a CREATE frame
  // prints the one just saved (§2.1) and otherwise refuses with "press Ctrl+A
  // first". The loaded id wins because on an alter frame it IS the voucher on
  // screen, whereas savedVoucherID may hold something keyed earlier in the
  // session.
  usePrintAction({
    voucher: loadedVoucherID
      ? { voucherID: loadedVoucherID }
      : { voucherID: savedVoucherID, justSaved: true },
  });

  // Title comes from the voucher CONFIG, which is the single source of truth for
  // per-type presentation. A second local map here silently fell behind when
  // credit-note/debit-note were added and rendered the raw slug
  // ("credit-note Voucher") in the header.
  const title = cfg.title;
  const displayNo = savedNo ?? form.voucherNo;

  // Draft-mode edit affordances: whether the draft's shape is rebuildable
  // (single-line, non-RCM, non-exempt), and which OCR-confidence chips to drop
  // because the human has overridden the underlying field.
  const draftRebuildable = isDraftMode ? canRebuildDraft(loadedDraft.draft.body) : false;
  const draftDirtyConfKeys = new Set<string>();
  if (isDraftMode) {
    for (const field of draftDirtyFields(draftSeedFormRef.current, form)) {
      const k = DRAFT_FIELD_CONF_KEY[field];
      if (k) draftDirtyConfKeys.add(k);
    }
  }

  // =========================================================================
  // ⛔⛔ THE FIELD HIGHLIGHT FOLLOWS **REAL FOCUS**, NOT THE MACHINE'S CURSOR.
  //
  // Tab is a browser primitive again (useNavMachine no longer preventDefaults
  // it — its removal note is the long version of this comment). The key is
  // still sent to the machine, so `snapshot.field` still walks the five strings
  // of VOUCHER_FIELDS; but the FORM is not five fields. One particulars row
  // alone carries a ledger box, an amount box, a remove button, the cost-split
  // picker and the bill-reference picker, so after four presses the machine's
  // cursor SATURATES on `narration` while the focus ring walks on into the
  // grid. Nothing is unreachable — the ring is the truth — but a decoration
  // that contradicts the ring is worse than no decoration: it tells the
  // operator their keystrokes are landing somewhere they are not.
  //
  // So the highlight is derived from what is actually focused (`e.target` in a
  // capture-phase focus handler IS `document.activeElement` at that instant,
  // without the timing hazard of reading the global), and the machine's cursor
  // is used ONLY while nothing inside the form has focus — the state a fresh
  // F8 frame is in before the operator has touched anything.
  //
  // THREE STATES, and the middle one is the point:
  //   null → nothing in this form has focus; the machine's cursor decorates.
  //   ""   → focus is INSIDE the form but on a control that is none of the five
  //          (a cost-centre select, "+ Add particular", a bill-ref row). Then
  //          NOTHING is highlighted, which is the honest statement — better
  //          than painting `narration` while the operator is in the grid.
  //   name → that field is focused, and it is the one painted.
  //
  // ⚠️ `data-vfield` IS TAGGED ONLY WHERE THE MAPPING IS REAL. The panels in
  // other files (the Dr/Cr grid, the pickers) are untagged and land in the ""
  // case on purpose; inventing a mapping for them here would put the decoration
  // back to guessing, which is the defect.
  // =========================================================================
  const [focusField, setFocusField] = useState<string | null>(null);
  const fieldActive = (name: string): boolean =>
    focusField === null ? snapshot.field === name : focusField === name;

  // Selecting a party fills the form name, carries its REAL ledger id, and seeds
  // the place of supply from that ledger's own state code (Tally defaults POS
  // from the party, and allows an override).
  //
  // Carrying `partyLedgerId` is what stops the accept path creating a ledger for
  // a party that already exists. A name typed FREE-HAND still falls through to
  // the create-or-select on accept, which is how a new party is added inline —
  // and which a Data Entry level is correctly refused.
  function handleSelectParty(item: string) {
    const party = parties.find((p) => p.name === item);
    // ⛔ IN SINGLE ENTRY THIS FIELD IS THE **ACCOUNT**, NOT THE PARTY — the bank
    // or cash the money moved through — so it fills `account`, and `party` is
    // DERIVED from the particulars on accept (D59: `party_ledger_id` is what the
    // printed receipt names, and naming the bank there made every receipt say
    // "Received From: HDFC Bank"). Writing both from here would put the bank back
    // on the paper.
    if (isSingleEntry) {
      setForm((f) => ({
        ...f,
        account: item,
        accountLedgerId: party?.id ?? "",
      }));
      onSelect(item);
      return;
    }
    setForm((f) => ({
      ...f,
      party: item,
      partyLedgerId: party?.id ?? "",
      partyStateCode: party?.stateCode ?? "",
      gst: {
        ...f.gst,
        // Seed POS from the party's state code (only if not already overridden).
        placeOfSupply: party?.stateCode || f.gst.placeOfSupply,
      },
    }));
    onSelect(item);
  }

  return (
    <section
      className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field"
      // CAPTURE phase: focus/blur do not bubble, so the section only hears them
      // this way — and hearing them at the section is what lets one handler
      // cover every control the form renders, including the ones in the panels
      // it composes from other files.
      onFocusCapture={(e) => {
        const owner = (e.target as HTMLElement).closest("[data-vfield]");
        setFocusField(owner?.getAttribute("data-vfield") ?? "");
      }}
      onBlurCapture={(e) => {
        // Only when focus leaves the FORM. Moving between two fields fires this
        // first, and clearing to the machine's cursor in between would make the
        // highlight flicker back to a field the operator has already left.
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
          setFocusField(null);
        }
      }}
    >
      <header className="mb-3 flex items-center justify-between">
        <h1 className="text-lg font-bold text-tally-title">
          {title} Voucher{isAlter ? " (Alteration)" : ""}
        </h1>
        <div className="flex items-center gap-3">
          {status ? (
            <span className="text-tally-active" role="status">
              {status}
            </span>
          ) : snapshot.saved ? (
            <span className="text-tally-active" role="status">
              Saved
            </span>
          ) : null}
          {savedNo ? (
            <span className="text-tally-title" data-testid="voucher-no">
              No: {savedNo}
            </span>
          ) : null}
        </div>
      </header>

      <p className="mb-2 text-xs text-tally-field">{cfg.hint}</p>

      <div className="bg-tally-panel p-3">
        {/*
          ⛔ ALWAYS EDITABLE. This read `editable={draftRebuildable}`, which is
          FALSE on every ordinary voucher, so the Date rendered as a read-only
          <span> on all six classes and the notes: an operator could not date a
          voucher at all, and every voucher keyed in a browser carried today().
          Verified in a browser 2026-08-07 — a Sales voucher's only inputs were
          the party and the GST fields.
          ⚠️ WHAT THAT COST IS NOT JUST TYPING. A business keys yesterday's
          invoices; more sharply, TWO PARITY ROWS WERE UNMEASURABLE BECAUSE OF IT
          — row 7's "the series must RESTART next financial year" (the counter is
          keyed off the voucher date) and row 37's back-dated-entry cutoff (which
          needs a voucher BEFORE the cutoff to refuse). Neither could be reached
          from the keyboard while the only date obtainable was today.
          The onChange was already wired; only the gate was wrong.
        */}
        <FieldRow
          name="Date"
          field="date"
          active={fieldActive("date")}
          value={form.date}
          editable
          onChange={(v) => setForm((f) => ({ ...f, date: v }))}
          testId={isDraftMode ? "draft-date" : "voucher-date"}
        />
        <FieldRow
          name="No."
          active={false}
          value={displayNo || "auto"}
          testId="voucher-no-field"
        />
        <div
          data-vfield="party-ledger"
          className={fieldActive("party-ledger") ? "" : "opacity-70"}
        >
          <SelectionList
            label={cfg.partyLabel}
            options={partyNames}
            onSelect={handleSelectParty}
            autoFocus={snapshot.field === "party-ledger"}
            // ⛔ THE HYDRATED PARTY, so an alteration does not render a blank
            // Account field over a voucher that has one. Pre-existing on every
            // class — see SelectionList's `value` doc for why it read as data
            // loss without ever being any.
            //
            // ⛔ IN SINGLE ENTRY IT IS THE **ACCOUNT**, which on an alter is read
            // back from the voucher's own primary-side POSTING rather than from
            // its stored party — see VoucherForm.account and accountLegFromView.
            // Rendering `form.party` here after D59 would show the customer in
            // the bank field, and the next Ctrl+A would post the customer on both
            // sides of a voucher that still balances.
            value={isSingleEntry ? form.account : form.party}
            // A party the company does not have yet is created on accept — the
            // only way to key the FIRST voucher of a brand-new company, and
            // Tally's own behaviour for a master field.
            allowCreate
          />
          {partiesError ? (
            <p data-testid="party-list-error" className="text-xs text-red-400">
              {partiesError} — type a name to create the party on accept
              (Alt+C also creates one inline).
            </p>
          ) : parties.length === 0 ? (
            <p data-testid="party-list-empty" className="text-xs">
              No debtor, creditor, cash or bank accounts in this company yet —
              type a name to create one on accept.
            </p>
          ) : null}
        </div>
        {/*
          ⛔ ALWAYS EDITABLE, for the same reason as the Date above: gated on
          `draftRebuildable`, this was a read-only <span> on every ordinary
          voucher, so EVERY browser-keyed voucher was posted with narration "".
          The narration is what a Day Book row shows and what an auditor reads to
          find out why an entry exists — the seeded vouchers all carry one, which
          is exactly why nothing looked wrong on screen.
        */}
        <FieldRow
          name="Narration"
          field="narration"
          active={fieldActive("narration")}
          value={form.narration}
          editable
          onChange={(v) => setForm((f) => ({ ...f, narration: v }))}
          testId={isDraftMode ? "draft-narration" : "voucher-narration"}
        />
      </div>

      {entryMode === "item-invoice" ? (
        <ItemInvoicePanel
          form={form}
          setForm={setForm}
          stockItems={stockItems}
          godowns={godowns}
          stockError={stockError}
          supplierStateCode={homeStateCode}
          voucherType={type}
          tax={taxResult}
        />
      ) : cfg.showGst ? (
        <>
          <GstPanel
            form={form}
            setForm={setForm}
            tax={taxResult}
          />
          {/* ⛔ THE STATUTORY BLOCK ON THE INVOICE (register row D3's collection
              half), and it is HERE — the ACCOUNTING-mode GST form — for the same
              reason the deduction block is on the Dr/Cr grid and not on single
              entry: it needs a line the operator keyed for it to NAME. That line
              is the additional charge above.

              ⚠️ THE LEG PICKER READS THE CHARGE LINES, not the chart. A leg the
              server cannot find among the postings is a verification that
              silently does not happen, so a name that is not on the invoice is
              REFUSED rather than sent. */}
          {cfg.statutoryTax !== null ? (
            <div className="mt-2 max-w-3xl">
              <TaxDeductionPanel
                kind={cfg.statutoryTax}
                value={form.tax}
                onChange={(tax) => setForm((f) => ({ ...f, tax }))}
                parties={partyNames}
                ledgerIdByName={(name) => ledgerByName(name)?.id ?? ""}
                ledgerNameByID={(id) => ledgerById(id)?.name ?? ""}
                gridLedgers={keyedChargeLines(form.gst.charges).map((c) =>
                  c.ledger.trim(),
                )}
              />
            </div>
          ) : null}
        </>
      ) : entryMode === "single" ? (
        <SingleEntryPanel
          voucherClass={voucherClassForType(type)}
          form={form}
          setForm={setForm}
          accountLabel={cfg.partyLabel}
          primarySide={cfg.primarySide}
          parties={partyNames}
          ledgerByName={ledgerByName}
          kindForGroup={kindForGroup}
        />
      ) : entryMode === "double" ? (
        /* ⛔ THIS BRANCH USED TO BE `null`, AND THAT WAS THE GAP. F7 rendered
           Date / No. / Party / Narration and then nothing — one input, no
           amount field, no second line, no side selector — on a FeaturesScreen
           row that read `on`. Ctrl+H > Double Entry on F5/F6/F4 reached the
           same dead end. See doubleEntry.ts's header. */
        <>
          <DoubleEntryPanel
            voucherClass={voucherClassForType(type)}
            lines={form.drCrLines}
            setLines={(next) =>
              setForm((f) => ({ ...f, drCrLines: next(f.drCrLines) }))
            }
            parties={partyNames}
            ledgerByName={ledgerByName}
            kindForGroup={kindForGroup}
          />
          {/* ⛔ THE STATUTORY BLOCK (register row D3), and it is HERE and not on
              the single-entry form because withholding splits the payment three
              ways — Dr the expense in full, Cr the bank NET, Cr the liability —
              and single entry puts the SUM of the particulars on ONE account,
              which leaves the withheld portion nowhere to go but a negative
              particular. Ctrl+H reaches this grid from F5/F6/F4.

              ⚠️ THE LEG PICKER READS THE GRID ABOVE IT, so the operator names the
              line they just keyed rather than a ledger from the whole chart —
              and so a leg the server cannot find is refused rather than posted
              as a verification that never runs. */}
          {cfg.statutoryTax !== null ? (
          <div className="mt-2 max-w-3xl">
            <TaxDeductionPanel
              kind={cfg.statutoryTax}
              value={form.tax}
              onChange={(tax) => setForm((f) => ({ ...f, tax }))}
              parties={partyNames}
              ledgerIdByName={(name) => ledgerByName(name)?.id ?? ""}
              ledgerNameByID={(id) => ledgerById(id)?.name ?? ""}
              gridLedgers={form.drCrLines
                .map((l) => l.ledger.trim())
                .filter((l) => l !== "")}
            />
          </div>
          ) : null}
        </>
      ) : null}

      {/* ⛔ THE BILL THIS INVOICE OPENS. Rendered only when the chosen party
          MAINTAINS BILL-BY-BILL — that flag is on the ledger master, so the form
          reads it from the chart it already loaded rather than asking. A party
          that does not maintain it has no bills, and showing the panel would
          invite a reference the report would never use. */}
      {raiseParty !== undefined && raiseParty.maintain_bill_by_bill ? (
        <div className="mt-3 max-w-2xl bg-tally-panel p-3 text-sm" data-testid="bill-refs-panel">
          <BillAllocationPicker
            voucherClass={voucherClassForType(type)}
            leg={raiseLeg}
            refs={form.billRefs}
            mode="raise"
            kind={kindForGroup(raiseParty.group_name)}
            label={raiseParty.name}
            onChange={(billRefs) => setForm((f) => ({ ...f, billRefs }))}
          />
        </div>
      ) : null}

      {isDraftMode ? (
        <DraftPrefillPanel
          draft={loadedDraft.draft}
          dirtyConfKeys={draftDirtyConfKeys}
          editable={draftRebuildable}
        />
      ) : null}

      {snapshot.overlay === "change-mode" ? (
        <ChangeModeOverlay type={type} current={entryMode} onSelect={onSelect} />
      ) : null}
    </section>
  );
}

function FieldRow({
  name,
  field,
  active,
  value,
  testId,
  editable,
  onChange,
}: {
  name: string;
  /**
   * WHICH of `VOUCHER_FIELDS` this row IS, so the highlight can follow real
   * focus — see the `fieldActive` note in AccountingVoucherScreen. Omitted on a
   * row that is not one of the five (the read-only "No."), which is exactly the
   * case that must NOT light up when focus passes over it.
   */
  field?: string;
  active: boolean;
  value: string;
  testId?: string;
  /** Draft mode: render an editable input while keeping the active highlight. */
  editable?: boolean;
  onChange?: (v: string) => void;
}) {
  return (
    <div data-vfield={field} className="flex items-center gap-2 py-0.5">
      <span className="w-32">{name}</span>
      {editable ? (
        <input
          data-testid={testId}
          className={
            active
              ? "bg-tally-active px-2 text-white outline-none"
              : "bg-tally-field px-2 text-tally-fieldText outline-none"
          }
          value={value}
          onChange={(e) => onChange?.(e.target.value)}
        />
      ) : (
        <span
          data-testid={testId}
          className={
            active
              ? "bg-tally-active px-2 text-white"
              : "bg-tally-field px-2 text-tally-fieldText"
          }
        >
          {value || " "}
        </span>
      )}
    </div>
  );
}

/**
 * The GST sub-form (F8/F9, cfg.showGst only): taxable base + rate + place of
 * supply + reverse charge, and a Tax panel showing the SERVER-derived split
 * (read back from the POST response — never computed in JS). Money is rendered
 * as exact fixed-4 strings.
 */
function GstPanel({
  form,
  setForm,
  tax,
}: {
  form: VoucherForm;
  setForm: React.Dispatch<React.SetStateAction<VoucherForm>>;
  tax: { cgst: string; sgst: string; igst: string } | null;
}) {
  return (
    <div className="mt-3 bg-tally-panel p-3" data-testid="gst-panel">
      <h2 className="mb-2 text-tally-title">GST</h2>
      <label className="flex items-center gap-2 py-0.5">
        <span className="w-32">Taxable Value</span>
        <input
          data-testid="gst-taxable"
          className="bg-tally-field px-2 text-tally-fieldText"
          value={form.taxableValue}
          onChange={(e) =>
            setForm((f) => ({ ...f, taxableValue: e.target.value }))
          }
        />
      </label>
      <label className="flex items-center gap-2 py-0.5">
        <span className="w-32">GST Rate %</span>
        <input
          data-testid="gst-rate"
          className="bg-tally-field px-2 text-tally-fieldText"
          value={form.gst.gstRatePct}
          onChange={(e) =>
            setForm((f) => ({ ...f, gst: { ...f.gst, gstRatePct: e.target.value } }))
          }
        />
      </label>
      <label className="flex items-center gap-2 py-0.5">
        <span className="w-32">Place of Supply</span>
        <input
          data-testid="gst-pos"
          className="bg-tally-field px-2 text-tally-fieldText"
          value={form.gst.placeOfSupply}
          onChange={(e) =>
            setForm((f) => ({ ...f, gst: { ...f.gst, placeOfSupply: e.target.value } }))
          }
        />
      </label>
      {/*
        ⛔ HSN — MANDATORY, AND UNTIL 2026-08-07 THERE WAS NO WAY TO KEY IT.
        `form.gst.hsn` already existed and gstVoucher.ts already put it on the
        wire; the accounting-mode panel simply had no input, so EVERY GST voucher
        keyed in a browser produced a GSTR-1 table-12 row with `"hsn_sc": ""`.
        Measured against real cored on 2026-08-07: two invoices, 50,000 of
        taxable value, one blank HSN summary row — a return the GSTN portal
        rejects. The ITEM path was never affected (HSN comes off the stock
        master), which is exactly why this hid: the demo seed supplies HSN
        explicitly and verify:demo therefore passed throughout.
      */}
      <label className="flex items-center gap-2 py-0.5">
        <span className="w-32">HSN / SAC</span>
        <input
          data-testid="gst-hsn"
          className="bg-tally-field px-2 text-tally-fieldText"
          value={form.gst.hsn}
          onChange={(e) =>
            setForm((f) => ({ ...f, gst: { ...f.gst, hsn: e.target.value } }))
          }
        />
      </label>
      <ChargeLines form={form} setForm={setForm} />
      {tax ? <TaxPanel tax={tax} /> : null}
    </div>
  );
}

/**
 * THE ADDITIONAL CHARGES — what the buyer is billed BEYOND the goods.
 *
 * ⛔⛔ THIS IS WHAT MAKES A TCS COLLECTION REAL (register row D3's collection
 * half). A `tcs` block on its own records the collection for the return and
 * POSTS NOTHING — the invoice would then charge the buyer for the goods and the
 * GST while the return says a further 1% was collected from them, which is money
 * the company must deposit and never billed. The panel below is the line that
 * charges it, and the TCS block underneath NAMES this line so the server can
 * verify the posting against its own derivation.
 *
 * ⚠️ IT IS NOT A TCS-ONLY FIELD. Freight, insurance and packing recovered from
 * the buyer are the same accounting act; the statutory block simply points at
 * one of these lines. Naming it "TCS" would have been the smaller lie.
 *
 * ⛔ IT IS NOT PART OF THE TAXABLE VALUE and the caption says so, because that
 * is the one mistake an operator can make here that the server cannot catch:
 * folding the charge into `Taxable Value` above raises the GST on it, and the
 * server's re-derivation would then agree with the client about a figure both
 * of them have wrong.
 */
function ChargeLines({
  form,
  setForm,
}: {
  form: VoucherForm;
  setForm: React.Dispatch<React.SetStateAction<VoucherForm>>;
}) {
  const rows: readonly GstChargeLine[] =
    form.gst.charges.length > 0 ? form.gst.charges : [{ ledger: "", amount: "" }];
  const setRows = (next: (prev: readonly GstChargeLine[]) => GstChargeLine[]) =>
    setForm((f) => ({ ...f, gst: { ...f.gst, charges: next(rows) } }));
  return (
    <div className="mt-3 border-t border-tally-field pt-2" data-testid="gst-charges">
      <h3 className="text-tally-title">Additional charges (billed to the party)</h3>
      <p className="mt-1 max-w-2xl text-xs">
        A line the party is charged that is NOT the goods — a statutory
        collection, freight, insurance, packing. It is added to what the party
        owes and is <span className="text-tally-title">not</span> part of the
        Taxable Value above: GST is charged on the supply, so folding a charge
        into the base would raise the tax on it.
      </p>
      <ul className="mt-2">
        {rows.map((c, i) => (
          <li key={i} className="mb-1 flex flex-wrap items-center gap-2 text-sm">
            <input
              aria-label={`Charge ${i + 1} ledger`}
              data-testid={`charge-ledger:${i}`}
              className="bg-tally-field px-1 text-tally-fieldText outline-none"
              placeholder="ledger (e.g. TCS Payable)"
              value={c.ledger}
              onChange={(e) =>
                setRows((prev) =>
                  prev.map((r, idx) =>
                    idx === i ? { ...r, ledger: e.target.value } : r,
                  ),
                )
              }
            />
            <input
              aria-label={`Charge ${i + 1} amount`}
              data-testid={`charge-amount:${i}`}
              className="w-32 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
              placeholder="0.0000"
              value={c.amount}
              onChange={(e) =>
                setRows((prev) =>
                  prev.map((r, idx) =>
                    idx === i ? { ...r, amount: e.target.value } : r,
                  ),
                )
              }
            />
            {rows.length > 1 ? (
              <button
                type="button"
                data-testid={`charge-remove:${i}`}
                className="px-1 text-xs underline"
                onClick={() =>
                  setRows((prev) => prev.filter((_, idx) => idx !== i))
                }
              >
                remove
              </button>
            ) : null}
          </li>
        ))}
      </ul>
      <button
        type="button"
        data-testid="charge-add-line"
        className="mt-1 px-2 py-0.5 text-xs underline"
        onClick={() => setRows((prev) => [...prev, { ledger: "", amount: "" }])}
      >
        + another charge
      </button>
    </div>
  );
}

/**
 * Render the server-returned tax split: CGST/SGST rows for intra-state, the
 * IGST row for inter-state (non-zero heads only), plus the invoice total. Proves
 * the user sees Tally's auto-derived split — money rendered as exact fixed-4
 * strings, never number-coerced.
 */
function TaxPanel({
  tax,
}: {
  tax: { cgst: string; sgst: string; igst: string };
}) {
  const isNonZero = (m: string) => Number.parseFloat(m || "0") !== 0;
  return (
    <div className="mt-2 border-t border-tally-field pt-2" data-testid="tax-panel">
      <h3 className="text-tally-title">Tax</h3>
      {isNonZero(tax.cgst) ? (
        <TaxRow label="CGST" amount={tax.cgst} testId="tax-cgst" />
      ) : null}
      {isNonZero(tax.sgst) ? (
        <TaxRow label="SGST" amount={tax.sgst} testId="tax-sgst" />
      ) : null}
      {isNonZero(tax.igst) ? (
        <TaxRow label="IGST" amount={tax.igst} testId="tax-igst" />
      ) : null}
    </div>
  );
}

function TaxRow({
  label,
  amount,
  testId,
}: {
  label: string;
  amount: string;
  testId: string;
}) {
  return (
    <div className="flex items-center gap-2 py-0.5">
      <span className="w-32">{label}</span>
      <span data-testid={testId} className="bg-tally-field px-2 text-tally-fieldText">
        {amount}
      </span>
    </div>
  );
}

/**
 * A header field is "amber" (needs review) when its confidence is below 0.80.
 * Missing confidence (undefined) is NOT amber: absent fields are surfaced via
 * the unresolved list instead, not flagged on present rows.
 */
function amber(conf?: FieldConfidence): boolean {
  return conf != null && conf.confidence < 0.8;
}

/** Inline amber color (tailwind warn class absent); applied to low-confidence rows. */
const AMBER_STYLE: React.CSSProperties = { color: "#d97706" };

/**
 * The OCR draft prefill panel (DRAFT MODE only). Surfaces, read-only:
 *  - a status banner (ready vs low_confidence_manual),
 *  - per-field amber confidence flags on the header fields,
 *  - the unresolved-field focus hints,
 *  - the redaction proof (PII counts),
 *  - and the derived GST split PREVIEW (server re-derives on submit).
 * Money is rendered as exact fixed-4 strings, never Number()-coerced.
 */
function DraftPrefillPanel({
  draft,
  dirtyConfKeys,
  editable,
}: {
  draft: PurchaseVoucherDraft;
  /** OCR-confidence keys whose field the human has edited (drop the amber chip). */
  dirtyConfKeys: Set<string>;
  /** Whether the draft is rebuildable; false => a disclosed edit-deferred note. */
  editable: boolean;
}) {
  const header = draft.confidence.header;
  const ready = draft.status === "ready";

  // The header confidence rows we surface, keyed to the contract's header map.
  const headerRows: readonly { key: string; label: string }[] = [
    { key: "invoice_no", label: "Invoice No." },
    { key: "invoice_date", label: "Invoice Date" },
    { key: "supplier", label: "Supplier" },
    { key: "place_of_supply", label: "Place of Supply" },
    { key: "totals", label: "Totals" },
  ];

  const red = draft.redaction;
  const redCounts = Object.entries(red.counts_by_type)
    .map(([k, n]) => `${k} ${n}`)
    .join(", ");
  const dg = draft.derived_gst;

  return (
    <div className="mt-3 bg-tally-panel p-3" data-testid="draft-panel">
      <div
        data-testid="draft-status"
        className="mb-2 font-bold"
        style={ready ? undefined : AMBER_STYLE}
      >
        {ready
          ? "Ready — press Ctrl+A to accept"
          : "Low confidence — review every field before accepting"}
      </div>

      {!editable ? (
        <div data-testid="draft-edit-disabled" className="mb-2 text-xs" style={AMBER_STYLE}>
          Editing is not available for this draft (multi-line, reverse-charge or
          exempt); accept as-is or fix it after posting.
        </div>
      ) : null}

      <div className="mb-2">
        {headerRows.map((row) => {
          const conf = header[row.key];
          // An EDITED field drops its amber confidence chip: the OCR confidence
          // described the machine's read, not the human's override.
          const edited = dirtyConfKeys.has(row.key);
          const isAmber = amber(conf) && !edited;
          return (
            <div
              key={row.key}
              data-testid={`conf-${row.key}`}
              className={
                "flex items-center gap-2 py-0.5" +
                (isAmber ? " text-tally-warn" : "")
              }
              style={isAmber ? AMBER_STYLE : undefined}
            >
              <span className="w-32">{row.label}</span>
              <span>
                {edited
                  ? "edited"
                  : conf
                    ? `${(conf.confidence * 100).toFixed(0)}% (${conf.source})`
                    : "—"}
              </span>
            </div>
          );
        })}
      </div>

      {draft.unresolved.length > 0 ? (
        <div data-testid="draft-unresolved" className="mb-2" style={AMBER_STYLE}>
          <span className="w-32">Review: </span>
          <span>{draft.unresolved.join(", ")}</span>
        </div>
      ) : null}

      <div data-testid="draft-redaction" className="mb-2 text-xs">
        PII redacted: {red.total}
        {redCounts ? ` (${redCounts})` : ""}
      </div>

      {dg ? (
        <div
          data-testid="draft-derived-gst"
          className="mt-2 border-t border-tally-field pt-2 text-xs"
        >
          <div className="text-tally-title">
            Derived GST (preview — server re-derives on submit)
          </div>
          <div>CGST {dg.cgst}</div>
          <div>SGST {dg.sgst}</div>
          <div>IGST {dg.igst}</div>
          <div>Total Tax {dg.total_tax}</div>
          <div>Invoice Total {dg.invoice_total}</div>
        </div>
      ) : null}
    </div>
  );
}

/** The Ctrl+H Change Mode picker (Item Invoice / Accounting Invoice / As Voucher). */
/**
 * Ctrl+H Change Mode.
 *
 * It used to offer three invoice labels on EVERY voucher and do nothing at all
 * when you picked one — a dead end on the key Tally uses to switch entry style.
 * It now offers the modes that apply to THIS class (RESEARCH.md: Ctrl+H toggles
 * Single vs Double entry for Payment / Receipt / Contra), marks the one in
 * force, and picking one takes effect immediately — Tally's behaviour, and no
 * confirming Enter.
 *
 * Sales/Purchase get the INVOICE family: Item Invoice (a stock grid whose lines
 * generate the accounting and the GST) and Accounting Invoice (a typed taxable
 * value). "As Voucher" is genuinely not built and still says so — the same
 * honesty the keymap applies to unavailable combos, since a key that silently
 * does nothing is what makes a keyboard user distrust the app.
 */
function ChangeModeOverlay({
  type,
  current,
  onSelect,
}: {
  type: VoucherType;
  current: EntryMode;
  onSelect: (item: string) => void;
}) {
  const options = entryModesFor(type);
  const label = (o: { label: string; mode: string | null }) =>
    o.mode === current ? `${o.label}  ← current` : o.label;
  return (
    <div
      role="dialog"
      aria-label="Change Mode"
      className="mt-3 mx-auto w-80 bg-tally-panel p-3"
      data-testid="change-mode-overlay"
    >
      <h2 className="mb-2 text-tally-title">Change Mode</h2>
      <SelectionList
        label="Mode"
        options={options.map(label)}
        // Strip the "← current" marker before handing the label to the machine,
        // which matches on the canonical label.
        onSelect={(item) => onSelect(item.replace(/\s+← current$/, ""))}
        autoFocus
      />
    </div>
  );
}

/**
 * The SINGLE-ENTRY particulars: one account on one side, many on the other.
 *
 * The single side's amount is DERIVED (the sum) and deliberately not editable —
 * that is the whole point of the mode, and an editable total would let the two
 * sides disagree in a form whose job is to make them agree.
 */
function SingleEntryPanel({
  voucherClass,
  form,
  setForm,
  accountLabel,
  primarySide,
  parties,
  ledgerByName,
  kindForGroup,
}: {
  /**
   * The class being keyed — the AUTHORITY a particular's open-bill list is read
   * under (D97). Threaded down rather than inferred here: the single-entry form
   * renders for Receipt, Payment and Contra alike, and each is a different
   * `display <class>` rule in cored's PDP.
   */
  voucherClass: VoucherClass;
  form: VoucherForm;
  setForm: React.Dispatch<React.SetStateAction<VoucherForm>>;
  accountLabel: string;
  primarySide: "DR" | "CR";
  parties: string[];
  /** Resolve a free-typed particular against the full chart. */
  ledgerByName: (name: string) => LedgerListItem | undefined;
  kindForGroup: (group: string) => "RECEIVABLE" | "PAYABLE";
}) {
  const rows = form.lines;
  const paise = rows.reduce(
    (sum, l) => sum + Math.round(parseFloat(l.amount || "0") * 10000),
    0,
  );
  const total = (paise / 10000).toFixed(4);
  const manySide = primarySide === "DR" ? "Cr" : "Dr";
  const oneSide = primarySide === "DR" ? "Dr" : "Cr";

  const setLine = (
    i: number,
    patch: Partial<{
      ledger: string;
      amount: string;
      allocations: readonly DraftAllocation[];
      billRefs: readonly DraftBillRef[];
    }>,
  ) =>
    setForm((f) => ({
      ...f,
      lines: f.lines.map((l, idx) => (idx === i ? { ...l, ...patch } : l)),
    }));

  return (
    <div className="mt-3 max-w-2xl bg-tally-panel p-3 text-sm" data-testid="single-entry-panel">
      <h2 className="mb-1 text-tally-title">
        Particulars ({manySide}) — {accountLabel} is {oneSide} {total}
      </h2>
      <p className="mb-2 text-xs">
        Single entry: name the account once, list what it moved against. The
        account&rsquo;s amount is the total and is not typed. Ctrl+H switches to
        double entry.
      </p>
      <ul>
        {rows.map((l, i) => (
          <li key={i} className="mb-1 flex flex-wrap items-center gap-2">
            <span className="w-6">{manySide}</span>
            {/* ⛔ `data-vfield` — THE TWO GRID BOXES THAT REALLY ARE TWO OF THE
                MACHINE'S FIVE FIELDS. RESEARCH sec 4's cursor is date → party →
                LEDGER → AMOUNT → narration, and in single entry these are that
                ledger and that amount. Tagging them is what makes the highlight
                keep up with the focus ring once Tab walks past the header. */}
            <input
              aria-label={`Particular ${i + 1} ledger`}
              data-testid={`single-ledger:${i}`}
              data-vfield="ledger"
              list="single-entry-ledgers"
              className="bg-tally-field px-1 text-tally-fieldText outline-none"
              value={l.ledger}
              placeholder="ledger"
              onChange={(e) => setLine(i, { ledger: e.target.value })}
            />
            <input
              aria-label={`Particular ${i + 1} amount`}
              data-testid={`single-amount:${i}`}
              data-vfield="amount"
              className="w-32 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
              value={l.amount}
              placeholder="0.0000"
              onChange={(e) => setLine(i, { amount: e.target.value })}
            />
            {rows.length > 1 ? (
              <button
                type="button"
                data-testid={`single-remove:${i}`}
                className="px-1 text-xs underline"
                onClick={() =>
                  setForm((f) => ({
                    ...f,
                    lines: f.lines.filter((_, idx) => idx !== i),
                  }))
                }
              >
                remove
              </button>
            ) : null}
            {/* ⛔ THE COST SPLITS, ON THE ROW THE OPERATOR IS ALREADY LOOKING AT.
                Not a modal: a separate panel would have to make them re-identify the
                posting, and the only handle for that is the 0-based ordinal this
                whole feature refuses to show. The ordinal is computed from this
                row's position when the postings array is assembled — see
                buildSingleEntryRequest, which states the primary-leg offset. */}
            <CostAllocationPicker
              line={l}
              lineIndex={i}
              onChange={(allocations) => setLine(i, { allocations })}
            />
            {/* ⛔ THE BILL THIS ROW SETTLES. It hangs off the PARTICULAR and not
                off the party field, because in single entry the party field is the
                BANK: `postings[0]` is the account the money moved through, and the
                customer or supplier being settled is this row. Putting it anywhere
                else would attach a receivable settlement to a bank account. */}
            {(() => {
              const led = ledgerByName(l.ledger);
              if (led === undefined || !led.maintain_bill_by_bill) return null;
              return (
                <BillAllocationPicker
                  voucherClass={voucherClass}
                  leg={{
                    ledgerId: led.id,
                    drCr: manySide === "Dr" ? "DR" : "CR",
                    amount: normalizeMoney(l.amount),
                  }}
                  refs={l.billRefs}
                  mode="settle"
                  kind={kindForGroup(led.group_name)}
                  label={led.name}
                  onChange={(billRefs) => setLine(i, { billRefs })}
                />
              );
            })()}
          </li>
        ))}
      </ul>
      {/* The company's real accounts, offered as completions rather than a
          closed list: a counter-ledger that does not exist yet is created on
          accept, exactly as the party field does. */}
      <datalist id="single-entry-ledgers">
        {parties.map((p) => (
          <option key={p} value={p} />
        ))}
      </datalist>
      <button
        type="button"
        data-testid="single-add-line"
        className="mt-1 px-2 py-0.5 text-xs underline"
        onClick={() =>
          setForm((f) => ({
            ...f,
            lines: [...f.lines, { ledger: "", amount: "", allocations: [], billRefs: [] }],
          }))
        }
      >
        + Add particular
      </button>
      <p className="mt-2" data-testid="single-total">
        {oneSide} {accountLabel}: {total}
      </p>
    </div>
  );
}

/**
 * Join keyed grid rows to the fetched stock masters.
 *
 * The tax RATE, the HSN and the unit live on the item master, never on the
 * movement — which is deliberate: a wire that let a client state a line's tax
 * rate would let a client tax a sale at whatever it liked. The server derives
 * GST from the master, so the client must derive its (verifiable) tax postings
 * from the same master or the post is refused.
 *
 * A row naming an item the master list does not contain keeps its keyed
 * quantity but carries a ZERO rate here. That is the honest failure: it makes
 * the derived tax visibly wrong on screen, and the server refuses the post,
 * rather than the client quietly inventing a rate that would have been accepted.
 */
function joinMasters(
  rows: readonly ItemInvoiceLine[],
  stockItems: readonly StockItemListItem[],
): ItemInvoiceLine[] {
  return rows.map((r) => {
    const master = stockItems.find((s) => s.id === r.stockItemId);
    if (!master) return r;
    return {
      ...r,
      itemName: master.name,
      gstRatePct: master.gst_rate_pct,
      hsn: master.hsn,
      unit: master.unit_symbol,
    };
  });
}

/**
 * THE ITEM INVOICE GRID (Ctrl+H > Item Invoice on F8 / F9).
 *
 * One row per item: what, from/into which godown, how many, at what rate, less
 * what discount. The line amount and the invoice totals are DERIVED from those
 * and are deliberately not typeable — that is the whole point of the mode, and
 * an editable amount would let the printed document disagree with the stock
 * movement it is supposed to describe.
 *
 * The tax shown before a save is a PREVIEW derived client-side purely so the
 * operator can see what they are about to post; the figures displayed AFTER a
 * save come from the server's response (`tax`), which is the only authority.
 */
function ItemInvoicePanel({
  form,
  setForm,
  stockItems,
  godowns,
  stockError,
  supplierStateCode,
  voucherType,
  tax,
}: {
  form: VoucherForm;
  setForm: React.Dispatch<React.SetStateAction<VoucherForm>>;
  stockItems: StockItemListItem[];
  godowns: GodownListItem[];
  stockError: string | null;
  supplierStateCode: string;
  voucherType: VoucherType;
  tax: { cgst: string; sgst: string; igst: string } | null;
}) {
  const rows = joinMasters(form.items, stockItems);
  const taxable = invoiceTaxable(rows);
  const preview = deriveItemInvoiceTax(
    rows,
    supplierStateCode,
    form.gst.placeOfSupply,
  );
  // WHICH WAY THE GOODS MOVE, said out loud on the form. The server refuses
  // the other direction, so this is a statement of what will happen, not a
  // second implementation of it.
  const inward = voucherType === "purchase";
  /**
   * Which order family this CLASS can discharge, or null.
   *
   * Non-null for Sales (goods out → a SALES order) and Purchase (goods in → a
   * PURCHASE order). ⛔ NULL for a Credit or Debit Note, and that is a decision
   * rather than an omission: goods coming back do not discharge a commitment, they
   * undo one, and this build has no negative-link path. Un-linking is done by
   * altering the delivery that made the link. Offering the picker on a return would
   * produce a link asserting that the return DELIVERED the order.
   */
  const fulfilsDir = fulfilsDirection(voucherType);

  const setRow = (i: number, patch: Partial<ItemInvoiceLine>) =>
    setForm((f) => ({
      ...f,
      items: f.items.map((l, idx) => (idx === i ? { ...l, ...patch } : l)),
    }));

  return (
    <div className="mt-3 bg-tally-panel p-3 text-sm" data-testid="item-invoice-panel">
      <h2 className="mb-1 text-tally-title">Item Invoice</h2>
      <p className="mb-2 text-xs">
        Key the GOODS; the SERVER derives the accounting and the GST from them,
        in the transaction that writes the stock movement — the client sends no
        amounts at all. Stock moves {inward ? "into" : "out of"} the godown on
        save. Ctrl+H switches back to the accounting invoice.
      </p>

      {/*
        ⛔ THE MISSING HSN, DISCLOSED BEFORE THE COUNTER (the item half of D60).
        The HSN is a MANDATORY particular of a tax invoice (CGST Rules 2017, Rule
        46(g)) and `printing.rule46` refuses to render one without it — so an
        item invoice keyed on a stock master that carries no code SAVES and then
        answers 400 on Ctrl+P, which the operator discovers with the customer
        waiting. The accounting-mode panel now REFUSES the save instead (see
        HSN_REQUIRED_CLASSES); this one cannot, because the code lives on the
        STOCK MASTER and this build has no keyboard path to one (RULE 3: never
        ship a refusal whose remedy cannot be reached). So it is said out loud,
        with the item named, and the save is allowed.
      */}
      {(() => {
        const noHsn = rows
          .filter((l) => isKeyedLine(l) && l.hsn.trim() === "")
          .map((l) => l.itemName || l.stockItemId);
        if (noHsn.length === 0) return null;
        return (
          <p data-testid="item-hsn-missing" className="mb-2 text-xs" style={AMBER_STYLE}>
            No HSN / SAC on the stock master for {noHsn.join(", ")}. This invoice
            will SAVE and then refuse to PRINT — the HSN is a mandatory particular
            of a tax invoice (Rule 46(g)), and Ctrl+P answers 400 naming the line.
            Put the code on the item master before you issue the document.
          </p>
        );
      })()}

      {/* ⛔ WHY THERE IS NO TCS BLOCK HERE, SAID OUT LOUD (register row D3).
          An item invoice's postings are GENERATED SERVER-SIDE from this grid and
          the server refuses a body that also states them — so there is no line
          an operator could key and no leg the collection could name. Offering a
          block that could only record-without-posting would produce an invoice
          that under-charges the buyer while the return says otherwise, which is
          exactly the state the charge line exists to prevent. A silent absence
          would read as "this build has no TCS"; it has one, one Ctrl+H away. */}
      {configFor(voucherType).statutoryTax !== null ? (
        <p data-testid="item-statutory-unavailable" className="mb-2 text-xs">
          Tax collected at source cannot be keyed on an item invoice: its
          postings are generated by the server from the lines below, so there is
          no charge line for the collection to be booked to. Ctrl+H &gt;
          Accounting Invoice keys the same document with an additional-charge
          line, and the TCS block is there.
        </p>
      ) : null}

      {stockError ? (
        <p data-testid="stock-masters-error" className="mb-2 text-xs text-red-400">
          {stockError}
        </p>
      ) : stockItems.length === 0 ? (
        /* ⛔ THE ROUTE THIS NAMES MUST BE ONE THAT EXISTS. It used to read
           "Alt+C from Gateway > Masters", and BOTH halves were wrong: Alt+C on
           this screen creates a LEDGER inline (it has never reached a stock item
           — `resolveAction` is keyed on ScreenKind and has no notion of the item
           grid), and there is no `Gateway > Masters` menu in this product at
           all. So the one empty state an operator meets on their first item
           invoice sent them to a door that is not there. The real route was
           built since: `MASTER_GOTO_ENTRIES` puts all five master kinds in the
           Go To picker, so Alt+G, type "stock", Enter reaches the create form.
           ⚠️ Alt+G on macOS is the PHYSICAL KeyG with Alt held — Option composes,
           so a real Mac sends key:"©". `comboFromEvent` reads `code`. */
        <p data-testid="stock-items-empty" className="mb-2 text-xs">
          This company has no stock items yet — create one with Alt+G &gt; Create
          Stock Item before keying an item invoice.
        </p>
      ) : null}

      <label className="mb-2 flex items-center gap-2 py-0.5">
        <span className="w-32">Place of Supply</span>
        <input
          data-testid="gst-pos"
          className="bg-tally-field px-2 text-tally-fieldText"
          value={form.gst.placeOfSupply}
          onChange={(e) =>
            setForm((f) => ({
              ...f,
              gst: { ...f.gst, placeOfSupply: e.target.value },
            }))
          }
        />
      </label>

      <table className="w-full max-w-4xl">
        <thead>
          <tr className="text-left text-xs text-tally-title">
            <th className="pr-2">Item</th>
            <th className="pr-2">Godown</th>
            <th className="pr-2 text-right">Qty</th>
            <th className="pr-2 text-right">Rate</th>
            <th className="pr-2 text-right">Disc %</th>
            <th className="pr-2 text-right">Amount</th>
            {/* THE ORDER LINK column. An invoice that fulfils an order directly,
                with no separate delivery note, is ONE OF THE TWO ORDINARY WAYS an
                order is discharged (RESEARCH.md:622), so it belongs here and not
                only on the Delivery Note.

                ⚠️ Note the asymmetry with cost_allocations, which an item invoice
                REFUSES: an allocation names a POSTING, and this class's postings are
                generated inside the server's transaction (index 0 is the party leg),
                so a client-supplied posting number would attribute the cost to a
                receivable. A fulfilment names an INVENTORY line — the operator's own
                grid, on every class — so it is honoured here. */}
            {fulfilsDir !== null ? <th className="pr-2">Against order</th> : null}
            <th />
          </tr>
        </thead>
        <tbody>
          {rows.map((l, i) => (
            <tr key={i} className="align-top">
              <td className="pr-2">
                <select
                  aria-label={`Item ${i + 1}`}
                  data-testid={`item-select:${i}`}
                  className="bg-tally-field px-1 text-tally-fieldText"
                  value={l.stockItemId}
                  onChange={(e) => setRow(i, { stockItemId: e.target.value })}
                >
                  <option value="">— select item —</option>
                  {stockItems.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </select>
                {l.unit ? (
                  <span className="ml-1 text-xs" data-testid={`item-unit:${i}`}>
                    {l.unit}
                  </span>
                ) : null}
              </td>
              <td className="pr-2">
                {/* "" is a REAL choice, not a missing one: an untracked line
                    posts a NULL godown, which is what a company that never
                    enabled multi-location wants. */}
                <select
                  aria-label={`Godown ${i + 1}`}
                  data-testid={`item-godown:${i}`}
                  className="bg-tally-field px-1 text-tally-fieldText"
                  value={l.godownId}
                  onChange={(e) => setRow(i, { godownId: e.target.value })}
                >
                  <option value="">— not tracked —</option>
                  {godowns.map((g) => (
                    <option key={g.id} value={g.id}>
                      {g.name}
                    </option>
                  ))}
                </select>
              </td>
              <td className="pr-2">
                <input
                  aria-label={`Quantity ${i + 1}`}
                  data-testid={`item-qty:${i}`}
                  className="w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                  value={l.qty}
                  placeholder="0.0000"
                  onChange={(e) => setRow(i, { qty: e.target.value })}
                />
              </td>
              <td className="pr-2">
                <input
                  aria-label={`Rate ${i + 1}`}
                  data-testid={`item-rate:${i}`}
                  className="w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                  value={l.rate}
                  placeholder="0.0000"
                  onChange={(e) => setRow(i, { rate: e.target.value })}
                />
              </td>
              <td className="pr-2">
                <input
                  aria-label={`Discount ${i + 1}`}
                  data-testid={`item-disc:${i}`}
                  className="w-16 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                  value={l.discountPct}
                  placeholder="0"
                  onChange={(e) => setRow(i, { discountPct: e.target.value })}
                />
              </td>
              {/* DERIVED, never typed — see the panel doc comment. */}
              <td className="pr-2 text-right" data-testid={`item-amount:${i}`}>
                {isKeyedLine(l) ? lineAmount(l) : "—"}
              </td>
              {/* ⛔ ON THE ROW, not in a modal — see FulfilmentPicker's header. */}
              {fulfilsDir !== null ? (
                <td className="pr-2">
                  <FulfilmentPicker
                    line={l}
                    lineIndex={i}
                    direction={fulfilsDir}
                    testIdPrefix="item"
                    onChange={(fulfilments) => setRow(i, { fulfilments })}
                  />
                </td>
              ) : null}
              <td>
                {form.items.length > 1 ? (
                  <button
                    type="button"
                    data-testid={`item-remove:${i}`}
                    className="px-1 text-xs underline"
                    onClick={() =>
                      setForm((f) => ({
                        ...f,
                        items: f.items.filter((_, idx) => idx !== i),
                      }))
                    }
                  >
                    remove
                  </button>
                ) : null}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <button
        type="button"
        data-testid="item-add-line"
        className="mt-1 px-2 py-0.5 text-xs underline"
        onClick={() =>
          setForm((f) => ({
            ...f,
            // The second row needs the default as much as the first.
            items: [...f.items, { ...emptyItemLine(), godownId: defaultGodownOf(godowns) }],
          }))
        }
      >
        + Add item
      </button>

      <div className="mt-2 border-t border-tally-field pt-2">
        <p data-testid="item-taxable">Goods (taxable): {taxable}</p>
        {tax ? (
          // AFTER a save: the server's own split, read back from the response.
          <TaxPanel tax={tax} />
        ) : (
          <p className="text-xs" data-testid="item-tax-preview">
            Tax preview — CGST {preview.cgst} / SGST {preview.sgst} / IGST{" "}
            {preview.igst}; the party is billed{" "}
            {addMoney(taxable, preview.total)}. PREVIEW ONLY: every figure on the
            saved voucher is the server&rsquo;s, derived from the grid above.
          </p>
        )}
      </div>
    </div>
  );
}
