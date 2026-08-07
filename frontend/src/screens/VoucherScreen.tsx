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
} from "./vouchers/voucherConfig";
import { dayBookRowKey, voucherTypeForClass } from "./vouchers/voucherClass";
import {
  emptyGstFormState,
  ensureLedger,
  ensureGstTaxLedgers,
  gstOrientationFor,
  buildGstVoucherRequest,
  gstVoucherGross,
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
  buildBillAllocations,
  defaultNewRef,
  retrackAutoRef,
  type BillBearingLeg,
  type DraftBillRef,
} from "./vouchers/billAllocation";
import {
  allocationsFromView,
  buildCostAllocations,
  type DraftAllocation,
} from "./vouchers/costAllocation";
import { DoubleEntryPanel } from "./vouchers/DoubleEntryPanel";
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
import { acceptEditedPurchaseDraft, canRebuildDraft } from "./vouchers/draftAccept";
import { StockVoucherScreen } from "./vouchers/StockVoucherScreen";
import { isStockVoucherType } from "./vouchers/stockVoucher";
import { acceptPurchaseDraft, ApiError } from "../api";
import type {
  DayBookEntry,
  FieldConfidence,
  GodownListItem,
  LedgerListItem,
  PostingRequest,
  PurchaseVoucherDraft,
  StockItemListItem,
  VoucherDraftBody,
  VoucherRequest,
  VoucherResponse,
  VoucherView,
} from "../api";
import type { EntryMode } from "../keystroke/types";

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
}

function emptyForm(): VoucherForm {
  return {
    date: today(),
    party: "",
    partyLedgerId: "",
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
  };
}

/**
 * Seed the form from a fetched VoucherView (the GET hydration path). All alter
 * seeding flows through here so buildGstVoucherRequest / the verbatim send path
 * carry REAL posting uuids and persisted GST attrs (place_of_supply, taxable,
 * reverse_charge), and the loaded voucher_no/date preserve numbering.
 */
function formFromView(view: VoucherView): VoucherForm {
  return {
    date: view.voucher_date,
    party: view.party,
    partyLedgerId: view.party_ledger_id,
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
    date: body.voucher_date ?? today(),
    party: body.party ?? "",
    partyLedgerId: body.party_ledger_id ?? "",
    partyStateCode: "",
    narration: body.narration ?? "",
    postings: [],
    voucherNo: body.voucher_no ?? "",
    gst: {
      placeOfSupply: body.gst?.place_of_supply_state_code ?? "",
      gstRatePct: line?.gst_rate_pct ?? "",
      reverseCharge: body.gst?.reverse_charge ?? false,
      hsn: line?.hsn ?? "",
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
  const entryMode = effectiveEntryMode(type, snapshot.config?.entryMode);

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
      const rows = f.lines.filter(
        (l) => l.ledger.trim() !== "" && parseFloat(l.amount || "0") > 0,
      );
      if (rows.length === 0) {
        throw new Error("Add at least one particular with an amount.");
      }
      const accountName = f.party.trim();
      if (accountName === "") {
        throw new Error(`${cfg.partyLabel} is required.`);
      }

      // The single side takes the SUM, computed in paise so a two-line entry
      // cannot drift by a rounding cent.
      const paise = rows.reduce(
        (sum, l) => sum + Math.round(parseFloat(l.amount || "0") * 10000),
        0,
      );
      const total = (paise / 10000).toFixed(4);

      const accountID =
        f.partyLedgerId ||
        (await ensureLedger(client, companyID, {
          name: accountName,
          groupName: "Bank Accounts",
        }));
      const manySide: "DR" | "CR" = cfg.primarySide === "DR" ? "CR" : "DR";

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

      const req: VoucherRequest = {
        voucher_date: f.date,
        narration: f.narration,
        party: accountName,
        postings,
      };
      if (f.partyLedgerId) req.party_ledger_id = f.partyLedgerId;
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
      const allocations = buildCostAllocations(rows, 1);
      if (allocations.length > 0) req.cost_allocations = allocations;

      // ---- THE BILL REFERENCES (what this voucher settles).
      //
      // ⛔ THE LEG IS A PARTICULAR, NOT THE PARTY FIELD, and that is the whole
      // hazard here — the mirror image of the cost-split offset above. In single
      // entry `postings[0]` is the BANK and `party_ledger_id` names the bank too;
      // the customer whose invoice is being settled is one of these ROWS. A
      // reference attached to the party field would credit the bank's own bill.
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
    },
    [client, companyID, cfg.primarySide, cfg.partyLabel],
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
      // ---- REFUSE FIRST. Nothing below this line may run on a bad grid.
      const refusal = doubleEntryRefusal(f.drCrLines);
      if (refusal !== "") throw new Error(refusal);

      // ⚠️ THE SAME `keyedLines` THE REFUSAL USED, and the same array the cost
      // splits are indexed against. Two independently-written filters that
      // agree today are how an allocation lands on the row above the one it
      // was keyed against.
      const rows = keyedLines(f.drCrLines);

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
      : gstVoucherGross(
          normalizeMoney(form.taxableValue),
          form.gst.gstRatePct,
          homeStateCode,
          form.gst.placeOfSupply,
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
      // ⛔ REFUSE A VOUCHER THIS FORM CANNOT FAITHFULLY RESTATE.
      //
      // The accounting-mode GST form holds ONE taxable value, ONE rate and ONE
      // HSN, and `buildGstVoucherRequest` sends exactly one `gst.lines` entry.
      // A loaded voucher with TWO rates therefore cannot survive this form: it
      // would be rebuilt as a single line at whichever rate was hydrated,
      // DELETING the others and re-deriving the whole tax from what remained.
      //
      // That is the same defect the `gst_lines` hydration above fixes, one
      // level along — not "the rate is lost" but "every line after the first
      // is". And it has the same signature: the voucher still balances, the
      // server still derives a consistent tax, and nothing refuses it, because
      // a one-line supply is a legal state.
      //
      // ⚠️ REFUSING IS THE HONEST ANSWER RATHER THAN THE LAZY ONE. Building a
      // multi-rate editor here would be a real feature; silently collapsing the
      // voucher is a data-loss bug wearing a feature's clothes. The message
      // names the count and points at the mode that CAN hold it.
      //
      // ⚠️ It is checked BEFORE `ensureOutputTaxLedgers`, for the reason the
      // Journal grid states: an `ensure*` helper CREATES, so validating after
      // it would leave ledgers behind on every refused save.
      if (f.gstLineCount > 1) {
        throw new Error(
          `This voucher has ${f.gstLineCount} GST lines at different rates, and ` +
            `the accounting-mode form holds only one rate — saving it here would ` +
            `keep the first line and DELETE the rest, re-deriving the tax from ` +
            `what was left. Alter it as an Item Invoice (Ctrl+H), where each line ` +
            `carries its own rate from the stock master.`,
        );
      }
      // ⛔⛔ EVERY ONE OF THE FOUR RESOLUTIONS BELOW IS CLASS-DEPENDENT, AND ALL
      // FOUR USED TO BE HARD-CODED TO THE SALES ANSWER. See GST_ORIENTATION in
      // gstVoucher.ts for what that cost: a purchase booked as a receivable
      // carrying output tax the company does not owe.
      const orientation = gstOrientationFor(type);
      if (!orientation) {
        throw new Error(
          `This build has no posting orientation for a "${type}" GST voucher, ` +
            `so it will not guess which way the money goes.`,
        );
      }
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
      const incomeLedgerId = await ensureLedger(client, companyID, {
        name: orientation.baseLedger.name,
        groupName: orientation.baseLedger.group,
      });
      return buildGstVoucherRequest({
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
      const orientation = gstOrientationFor(type);
      if (!orientation) {
        throw new Error(
          `This build has no posting orientation for a "${type}" item invoice.`,
        );
      }
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
      const accept =
        dirty.size > 0 && canRebuildDraft(draftBody)
          ? acceptEditedPurchaseDraft(client, companyID, draftBody, {
              voucherDate: f.date,
              narration: f.narration,
              taxableValue: f.taxableValue,
              gstRatePct: f.gst.gstRatePct,
              placeOfSupply: f.gst.placeOfSupply,
            })
          : acceptPurchaseDraft(client, companyID, draftBody);
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
    const reqPromise =
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
    reqPromise
      .then((req) => acceptVoucher(client, companyID, ctx, req))
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
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
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
        <FieldRow
          name="Date"
          active={snapshot.field === "date"}
          value={form.date}
          editable={draftRebuildable}
          onChange={(v) => setForm((f) => ({ ...f, date: v }))}
          testId={isDraftMode ? "draft-date" : undefined}
        />
        <FieldRow
          name="No."
          active={false}
          value={displayNo || "auto"}
          testId="voucher-no-field"
        />
        <div className={snapshot.field === "party-ledger" ? "" : "opacity-70"}>
          <SelectionList
            label={cfg.partyLabel}
            options={partyNames}
            onSelect={handleSelectParty}
            autoFocus={snapshot.field === "party-ledger"}
            // ⛔ THE HYDRATED PARTY, so an alteration does not render a blank
            // Account field over a voucher that has one. Pre-existing on every
            // class — see SelectionList's `value` doc for why it read as data
            // loss without ever being any.
            value={form.party}
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
        <FieldRow
          name="Narration"
          active={snapshot.field === "narration"}
          value={form.narration}
          editable={draftRebuildable}
          onChange={(v) => setForm((f) => ({ ...f, narration: v }))}
          testId={isDraftMode ? "draft-narration" : undefined}
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
        <GstPanel
          form={form}
          setForm={setForm}
          tax={taxResult}
        />
      ) : entryMode === "single" ? (
        <SingleEntryPanel
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
        <DoubleEntryPanel
          lines={form.drCrLines}
          setLines={(next) =>
            setForm((f) => ({ ...f, drCrLines: next(f.drCrLines) }))
          }
          parties={partyNames}
          ledgerByName={ledgerByName}
          kindForGroup={kindForGroup}
        />
      ) : null}

      {/* ⛔ THE BILL THIS INVOICE OPENS. Rendered only when the chosen party
          MAINTAINS BILL-BY-BILL — that flag is on the ledger master, so the form
          reads it from the chart it already loaded rather than asking. A party
          that does not maintain it has no bills, and showing the panel would
          invite a reference the report would never use. */}
      {raiseParty !== undefined && raiseParty.maintain_bill_by_bill ? (
        <div className="mt-3 max-w-2xl bg-tally-panel p-3 text-sm" data-testid="bill-refs-panel">
          <BillAllocationPicker
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
  active,
  value,
  testId,
  editable,
  onChange,
}: {
  name: string;
  active: boolean;
  value: string;
  testId?: string;
  /** Draft mode: render an editable input while keeping the active highlight. */
  editable?: boolean;
  onChange?: (v: string) => void;
}) {
  return (
    <div className="flex items-center gap-2 py-0.5">
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
      {tax ? <TaxPanel tax={tax} /> : null}
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
  form,
  setForm,
  accountLabel,
  primarySide,
  parties,
  ledgerByName,
  kindForGroup,
}: {
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
            <input
              aria-label={`Particular ${i + 1} ledger`}
              data-testid={`single-ledger:${i}`}
              list="single-entry-ledgers"
              className="bg-tally-field px-1 text-tally-fieldText outline-none"
              value={l.ledger}
              placeholder="ledger"
              onChange={(e) => setLine(i, { ledger: e.target.value })}
            />
            <input
              aria-label={`Particular ${i + 1} amount`}
              data-testid={`single-amount:${i}`}
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

      {stockError ? (
        <p data-testid="stock-masters-error" className="mb-2 text-xs text-red-400">
          {stockError}
        </p>
      ) : stockItems.length === 0 ? (
        <p data-testid="stock-items-empty" className="mb-2 text-xs">
          This company has no stock items yet — create one (Alt+C from Gateway
          &gt; Masters) before keying an item invoice.
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
          setForm((f) => ({ ...f, items: [...f.items, emptyItemLine()] }))
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
