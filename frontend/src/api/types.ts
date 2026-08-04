/**
 * Typed request/response shapes for the Phase 1 `cored` backend.
 *
 * These mirror the EXACT JSON the Go HTTP surface accepts and emits
 * (backend/internal/server/server.go). Two non-negotiable conventions:
 *
 *  - Money / quantities / rates are fixed-4 decimal STRINGS on the wire. They
 *    are typed as `Money` (a branded-ish alias of string) and MUST be parsed as
 *    strings — never coerced to a JS `number`, which would silently lose paisa.
 *  - Every write is scoped to a company by the `{companyID}` path segment; the
 *    company id is therefore a first argument to every scoped call, not a field.
 */

/** A fixed-4 decimal value transported as a string (e.g. "1234.5600"). */
export type Money = string;

/**
 * The voucher classes with a POST route (path segment + F-key mapping).
 *
 * The first eight are ACCOUNTING classes: they must state balanced double-entry
 * postings. The last four are the NON-ACCOUNTING STOCK classes — they move
 * goods and post ZERO accounting lines. That split is a compile-time closed set
 * in Go (`ledger.accountingClasses`), never a DB flag, and it is a closed union
 * here for the same reason.
 */
export type VoucherClass =
  | "sales"
  | "purchase"
  | "receipt"
  | "payment"
  | "contra"
  | "journal"
  /** Sales return / downward price correction. GSTR-1 table 9B (cdnr). */
  | "credit-note"
  /** Purchase return / upward price correction raised on a supplier. */
  | "debit-note"
  /** Goods out before/without the invoice. Stock only, no postings. */
  | "delivery-note"
  /** Goods in before the supplier's bill. Stock only, no postings. */
  | "receipt-note"
  /** A transfer between godowns: OUT of one and IN to another. */
  | "stock-journal"
  /** The counted figure; the server derives the correction. */
  | "physical-stock";

/** Debit / Credit marker on a posting or bill allocation. */
// "D"/"C" are the backend's canonical wire values; "DR"/"CR" are the legible
// frontend domain form. The REST adapter normalizes the latter to the former.
export type DrCr = "D" | "C" | "DR" | "CR" | "Dr" | "Cr";

/** Bill reference lifecycle type for bill-by-bill allocation. */
export type RefType = "NEW" | "AGAINST" | "ADVANCE" | "ON_ACCOUNT";

// ---------------------------------------------------------------------------
// Company
// ---------------------------------------------------------------------------

export interface CreateCompanyRequest {
  /** Optional client-supplied UUID; the server generates one when omitted. */
  id?: string;
  name: string;
  fy_start_date: string;
  base_currency: string;
  /**
   * The company's home/registered GST state (2-digit code, e.g. "29" Karnataka,
   * "27" Maharashtra). Persisted to `company.state_code` and used server-side as
   * the DEFAULT SupplierStateCode when a Sales/Purchase post leaves it empty —
   * i.e. it is what decides intra-state (CGST+SGST) vs inter-state (IGST).
   * Omitted / "" is accepted (the column defaults to '').
   */
  state_code?: string;
  /** The company's own (filer's) GSTIN. Optional; "" is the safe default. */
  gstin?: string;

  // --- Mailing block + country (migration 0015). All optional. -------------
  /** Name printed on invoices; "" means "use `name`". */
  mailing_name?: string;
  /** Principal place of business, newline-separated free text. */
  address?: string;
  city?: string;
  pincode?: string;
  /** Defaults to "India" when omitted. */
  country?: string;

  /**
   * The date this company's books BEGIN here — and therefore the date every
   * opening balance is stated as at. DISTINCT from `fy_start_date`, which is
   * when the financial YEAR begins: they are equal for a company starting fresh
   * at the top of its year, and differ for a mid-year migration (year starts
   * 1 April, books begin 1 October). Defaults to `fy_start_date` when omitted;
   * it may never precede it (enforced by a DB CHECK).
   */
  books_beginning_from?: string;

  // --- Base-currency presentation. Storage stays NUMERIC(19,4). -----------
  /** Defaults to "₹". */
  currency_symbol?: string;
  /** Defaults to "Indian Rupees". */
  currency_formal_name?: string;
  /** Digits after the decimal point, 0-4. Defaults to 2. Display only. */
  decimal_places?: number;
}

export interface CreateCompanyResponse {
  company_id: string;
}

/**
 * The company master as returned by GET /v1/companies/{companyID}. Field names
 * MIRROR {@link CreateCompanyRequest} exactly, so create → read round-trips
 * without a second mapping. `state_code` is the value the frontend hydrates into
 * `CompanyInfo.homeStateCode`, which is what makes the in-browser GST split
 * agree with the server's derivation for non-Karnataka companies.
 */
export interface CompanyView {
  id: string;
  name: string;
  fy_start_date: string;
  base_currency: string;
  /** 2-digit home state code; "" when the company has none recorded. */
  state_code: string;
  /** "" when not set. */
  gstin: string;

  mailing_name: string;
  address: string;
  city: string;
  pincode: string;
  country: string;
  /** See {@link CreateCompanyRequest.books_beginning_from}. */
  books_beginning_from: string;
  currency_symbol: string;
  currency_formal_name: string;
  decimal_places: number;
}

// ---------------------------------------------------------------------------
// Masters
// ---------------------------------------------------------------------------

export interface CreateGroupRequest {
  name: string;
  parent_name?: string;
  nature?: string;
  is_pl?: boolean;
  behaves_as_subledger?: boolean;
  created_by?: string;
}
export interface CreateGroupResponse {
  group_id: string;
}

/** One row of the chart of accounts, as returned by GET .../ledgers. */
export interface LedgerListItem {
  id: string;
  name: string;
  group_name: string;
  /** Fixed-4 decimal string, like every money value on the wire. */
  opening_balance: Money;
  opening_dc: string;
  maintain_bill_by_bill: boolean;
  gstin: string;
  state_code: string;
  registration_type: string;
  /**
   * The party's POSTAL BLOCK (migration 0039). Absent reads back as "" — the
   * columns are nullable and a cash party legitimately has none. `address` is
   * a NEWLINE-SEPARATED BLOCK.
   */
  address: string;
  city: string;
  pincode: string;
  /**
   * The company's OWN bank details for this ledger (migration 0064).
   *
   * "" MEANS NOT RECORDED — render those words, never an empty field, because a
   * blank labelled field reads as "this bank has no IFSC" (invariant I-11).
   */
  bank_account_number: string;
  bank_ifsc: string;
  /** True for ledgers seeded with the company (Cash, Profit & Loss A/c). */
  is_system: boolean;
  /**
   * True when a Bank Reconciliation Statement can be drawn for this ledger.
   *
   * SERVER-DERIVED, AND DO NOT TRY TO RECOMPUTE IT FROM `group_name`. Bank-ness
   * is ltree ANCESTRY under 'Bank Accounts' / 'Bank OD A/c'
   * (reporting.BankLedgerPredicate), while `group_name` is the ledger's
   * IMMEDIATE group — so a company keeping its accounts in a user-created
   * "Current Accounts" sub-group reads group_name = "Current Accounts", and a
   * client filtering on the two seeded names silently drops real bank accounts
   * from its reconciliation picker. Silently: a shorter list is not an error.
   *
   * An OD account is a bank account AND a liability; this says "a statement can
   * be drawn", not "this is an asset".
   */
  is_bank: boolean;
}

export interface ListLedgersResponse {
  ledgers: LedgerListItem[];
}

// ---------------------------------------------------------------------------
// BANK RECONCILIATION (T4). Wire shapes of GET /bank-reconciliation/{ledgerID}.
// ---------------------------------------------------------------------------

/**
 * One line of the statement. Money is a fixed-4 decimal STRING — rendered as
 * received, never parsed into a number (docs/design/t1t4-ui-model.md §3).
 */
export interface BankReconLine {
  voucher_id: string;
  /** Ordinal of the bank leg; non-zero only when one voucher touches the bank twice. */
  leg_no: number;
  voucher_no: string;
  voucher_date: string;
  narration: string;
  party: string;
  amount: Money;
  dr_cr: string;
  /** "" when the item has not cleared AS AT the statement date. */
  bank_date: string;
  /** SERVER-derived. Never recomputed here: a clock-skewed browser would disagree. */
  age_days: number;
  /**
   * True when a reconciliation exists for this leg but the voucher's current
   * version no longer matches what was cleared — i.e. somebody ALTERED a voucher
   * after reconciling it. The item is treated as un-cleared for the arithmetic
   * and must be surfaced prominently; it is not a detail.
   */
  broken_by_alter: boolean;
  /** What the reconciliation recorded. Present ONLY when broken — the evidence. */
  cleared_amount?: Money;
  cleared_dr_cr?: string;
}

/** The Bank Reconciliation Statement as at a date. */
export interface BankReconciliationResponse {
  ledger_id: string;
  ledger_name: string;
  as_on: string;
  balance_as_per_books: Money;
  uncleared_net: Money;
  balance_as_per_bank: Money;
  difference: Money;
  /** The SERVER's verdict. Rendered rather than re-derived by comparing strings. */
  balanced: boolean;
  /** Non-zero means a voucher was altered after being reconciled. */
  broken_count: number;
  uncleared: BankReconLine[];
  cleared: BankReconLine[];
}

export interface SetBankDateRequest {
  /**
   * ISO yyyy-mm-dd, REQUIRED. There is deliberately no "as of today" default:
   * a reconciliation is an assertion copied off a bank statement, and defaulting
   * it would invent the one fact the feature exists to record.
   */
  bank_date: string;
  created_by: string;
}

export interface SetBankDateResponse {
  status: string;
}

// ---------------------------------------------------------------------------
// BANK STATEMENT IMPORT (T4 slice 2a). Wire shapes of the three
// /bank-statement/{ledgerID} routes.
//
// THE PREVIEW STEP IS NOT A CONVENIENCE. This build ships NO built-in per-bank
// column layouts: a layout recalled from memory that is wrong by one column
// reads the running balance as the amount, and every figure is then a real
// number off a real statement that was never a transaction. So the mapping is
// PROPOSED from the file's own header row and CONFIRMED by the operator.
// See docs/design/t4-slice2-model.md §8.2.
// ---------------------------------------------------------------------------

/** How a file says which way money moved. */
export type StatementDirectionEncoding =
  | "two-columns"
  | "signed-amount"
  | "amount-and-flag";

/**
 * The operator-confirmed reading of one file. Every field except `encoding` and
 * `date_layout` is a HEADER NAME as it appears in the file; "" means the file
 * has no such column.
 */
export interface StatementColumnMapping {
  encoding: StatementDirectionEncoding | "";
  /** A NAMED layout, e.g. "dd/mm/yyyy" — never a raw format string. */
  date_layout: string;
  value_date: string;
  txn_date: string;
  description: string;
  deposit: string;
  withdrawal: string;
  amount: string;
  direction: string;
  running_balance: string;
  bank_reference: string;
  cheque_number: string;
}

export interface PreviewStatementRequest {
  file_name: string;
  /** The file, verbatim. */
  content: string;
}

/** What the operator confirms before a single row is stored. */
export interface PreviewStatementResponse {
  file_name: string;
  /** The file's own header row, in order — the list the operator picks from. */
  header: string[];
  mapping: StatementColumnMapping;
  /**
   * ⚠️ THE POINT OF THE PREVIEW. Each entry is a question only the operator can
   * answer, and a client MUST show them rather than confirming past them. The
   * commonest is an ambiguous dd/mm vs mm/dd date column: 03/04/2026 is the 3rd
   * of April or the 4th of March, the file cannot say which, and choosing
   * wrongly moves the whole statement by up to eleven months while every amount
   * in it stays perfectly plausible.
   */
  unresolved: string[];
  /** Observations that do not block — e.g. a column nothing was mapped to. */
  notes: string[];
  known_date_layouts: string[];
  known_encodings: StatementDirectionEncoding[];
}

export interface ImportStatementRequest {
  file_name: string;
  content: string;
  mapping: StatementColumnMapping;
  /**
   * What the FILE says the account is, if the operator read it off the
   * statement. When this and the ledger's recorded number are both present they
   * MUST agree or the import is refused — a statement imported against the
   * wrong ledger reconciles nothing and leaves two accounts wrong, silently,
   * because every line in it is real and both accounts still balance.
   */
  statement_account_number?: string;
  created_by: string;
}

export interface ImportStatementResponse {
  import_id: string;
  /**
   * True when this exact file was already imported into this account. A SUCCESS
   * and a no-op, not an error: the operator's intent is satisfied either way.
   */
  already_imported: boolean;
  lines_in_file: number;
  new_lines: number;
  /** Rows already present from an overlapping statement. Ordinary, not an error. */
  duplicate_lines: number;
  opening_balance: Money;
  closing_balance: Money;
  /**
   * FALSE MEANS THE DROPPED-ROW CHECK NEVER RAN — the file carried no running
   * balance. A client must NOT render this the same way as a check that passed:
   * a check that did not run and a check that succeeded are the same shape from
   * the outside, and only one of them is safe.
   */
  balance_walked: boolean;
  period_from: string;
  period_to: string;
  /** What this import could not verify, in the operator's own words. */
  disclosures: string[];
}

/** One transaction as the BANK reported it. */
export interface BankStatementLine {
  id: string;
  import_id: string;
  /** 1-based position in the file, so a refusal can be found in a spreadsheet. */
  line_no: number;
  value_date: string;
  /** "" when the file carried only one date. NOT defaulted to the value date. */
  txn_date: string;
  description: string;
  amount: Money;
  /**
   * Direction IN OUR BOOKS, which INVERTS the word on the statement: a DEPOSIT
   * is "D" because our bank asset rose, though the bank calls it a credit. Do
   * not "correct" this when labelling a column — render "D" as money IN.
   */
  dr_cr: DrCr;
  /** Absent when the file carried no balance, so it is distinguishable from zero. */
  running_balance?: Money;
  bank_reference: string;
  cheque_number: string;
}

/** One imported file, as a header over its lines. */
export interface BankStatementImport {
  import_id: string;
  file_name: string;
  imported_by: string;
  imported_at: string;
  period_from: string;
  period_to: string;
  line_count: number;
  /** False when this file carried no running balance to walk. */
  balance_walked: boolean;
}

/** GET /bank-statement/{ledgerID} — what the bank said, over a period. */
export interface ImportedStatementResponse {
  ledger_id: string;
  ledger_name: string;
  /** "" means NOT RECORDED — render those words, never an empty field (I-11). */
  bank_account_number: string;
  bank_ifsc: string;
  from: string;
  to: string;
  imports: BankStatementImport[];
  lines: BankStatementLine[];
  total_in: Money;
  total_out: Money;
  /**
   * PART OF THE REPORT, not decoration. The first always says that MATCHING IS
   * NOT BUILT YET — rows of bank movements with no status column otherwise read
   * as "none of these matched", which is a different and false statement.
   */
  disclosures: string[];
}

/**
 * One bill of a ledger's BILL-WISE OPENING BREAKUP — the individual unpaid
 * invoices that make up its opening balance, as at `books_beginning_from`.
 * The server REQUIRES these to sum to `opening_balance` when any are supplied
 * (Tally refuses a breakup with a difference, and so do we).
 */
export interface OpeningBillRequest {
  ref_name: string;
  /** NEW (default) / ADVANCE / ON_ACCOUNT. */
  ref_type?: RefType;
  /** Defaults to the ledger's `opening_dc`. */
  dr_cr?: DrCr;
  amount: Money;
  due_date?: string;
}

export interface CreateLedgerRequest {
  name: string;
  group_name: string;
  opening_balance?: Money;
  opening_dc?: string;
  /** Bill-wise breakup of `opening_balance`; must sum to it. */
  opening_bills?: OpeningBillRequest[];
  maintain_bill_by_bill?: boolean;
  gstin?: string;
  state_code?: string;
  registration_type?: string;
  pan?: string;
  /**
   * The party's POSTAL BLOCK (migration 0039). `address` is a
   * NEWLINE-SEPARATED BLOCK, not a single line — the same shape the company
   * profile uses, so one form serves both.
   *
   * All three are optional here on purpose. Rule 46(d) makes the recipient's
   * address mandatory ON A TAX INVOICE and that is enforced server-side at
   * print time, not at master-create time: a walk-in cash customer below the
   * Rule 46(e) threshold legitimately has no address.
   */
  address?: string;
  city?: string;
  pincode?: string;
  /**
   * The company's OWN account details for a BANK ledger (migration 0064).
   *
   * They ship WITH the columns, not after them — invariant I-11: a blank
   * labelled field reads as "this bank has no IFSC" rather than "nobody has
   * typed it yet". They are also how a statement import knows a file belongs to
   * this account: with both recorded, an import whose file names a different
   * account is REFUSED instead of quietly reconciling the wrong books.
   *
   * `bank_ifsc` is four letters, '0', then six alphanumerics; the server
   * refuses anything else by CHECK, because the column exists to be compared
   * against a bank's own file and a typo that merely fails to match produces a
   * refusal nobody can debug.
   */
  bank_account_number?: string;
  bank_ifsc?: string;
  created_by?: string;
}
export interface CreateLedgerResponse {
  ledger_id: string;
}

/**
 * Body of PUT .../ledgers/{id}/opening — set a ledger's opening balance and its
 * bill-wise opening breakup. The only way to give an opening balance to a
 * ledger seeded WITH the company (Cash, Profit & Loss A/c), which no client can
 * pass fields to at creation time.
 */
export interface SetLedgerOpeningRequest {
  opening_balance: Money;
  /** "D" or "C". Defaults to "D". */
  opening_dc?: string;
  /** Must sum to `opening_balance` when present. */
  opening_bills?: OpeningBillRequest[];
  created_by?: string;
}

export interface SetLedgerOpeningResponse {
  ledger_id: string;
}

export interface CreateVoucherTypeRequest {
  name: string;
  parent_class: string;
  numbering_method?: string;
  affects_stock?: boolean;
  is_accounting?: boolean;
  created_by?: string;
}
export interface CreateVoucherTypeResponse {
  voucher_type_id: string;
}

export interface CreateUnitRequest {
  symbol: string;
  formal_name?: string;
  decimal_places?: number;
  created_by?: string;
}
export interface CreateUnitResponse {
  unit_id: string;
}

export interface CreateStockItemRequest {
  name: string;
  unit_symbol: string;
  hsn?: string;
  gst_rate_pct?: Money;
  opening_qty?: Money;
  opening_rate?: Money;
  created_by?: string;
}
export interface CreateStockItemResponse {
  stock_item_id: string;
}

// ---------------------------------------------------------------------------
// Vouchers
// ---------------------------------------------------------------------------

export interface PostingRequest {
  ledger_id: string;
  dr_cr: DrCr;
  amount: Money;
}

export interface BillAllocationRequest {
  ledger_id: string;
  ref_name: string;
  ref_type: RefType;
  dr_cr: DrCr;
  amount: Money;
  due_date?: string;
}

export interface GSTLineRequest {
  taxable_value: Money;
  gst_rate_pct: Money;
  hsn?: string;
}

export interface GSTRequest {
  ruleset_version?: string;
  supplier_state_code?: string;
  place_of_supply_state_code?: string;
  reverse_charge?: boolean;
  exempt?: boolean;
  lines?: GSTLineRequest[];
  cgst_ledger_id?: string;
  sgst_ledger_id?: string;
  igst_ledger_id?: string;
}

/**
 * ONE LINE OF THE ITEM GRID (server.go's `inventoryLineReq`).
 *
 * Every numeric field is a fixed-4 decimal STRING for the reason every money
 * field on this surface is: a JSON number is a float64, and a float quantity
 * makes a closing stock balance depend on the order the lines were entered.
 * Quantities are money here.
 *
 * `godown_id` / `batch_id` are OPTIONAL: "" means "not tracked on this line"
 * and is stored as SQL NULL, so a company that never enables multi-location or
 * batches is not forced to invent master rows.
 *
 * There is deliberately NO gst_rate_pct or hsn: the SERVER reads those from the
 * stock item master. A client that could state a line's tax rate could tax a
 * sale at whatever it liked.
 *
 * `direction` is "IN" or "OUT" and carries the sign; `qty` is always POSITIVE.
 * `amount` is taken as entered and is never recomputed server-side from
 * qty × rate, because Tally lets a user override a line total and silently
 * "correcting" it would make the printed invoice disagree with the stored one.
 */
export interface InventoryLineRequest {
  stock_item_id: string;
  /** "" when the line is not tracked to a location. */
  godown_id?: string;
  /** "" when the line is not tracked to a batch/lot. */
  batch_id?: string;
  direction: "IN" | "OUT";
  qty: Money;
  rate?: Money;
  discount_pct?: Money;
  amount?: Money;
}

/**
 * ONE COUNTED POSITION of a Physical Stock voucher: "there are Qty of this
 * item, in this godown, from this batch, on the shelf" (server.go's
 * physicalCountLineReq).
 *
 * IT HAS NO `direction` AND THAT IS THE POINT. A count carries no sign — the
 * sign of the correction is DERIVED server-side by differencing the count
 * against the book position, inside the transaction that writes the movement.
 * A client that stated the delta would be differencing against a book it read
 * earlier, and a stock-take is exactly the moment two readers race.
 *
 * `qty` may legitimately be "0" ("the shelf is empty" is a count, and the one
 * an auditor most wants recorded). `rate` is required (non-zero) only when the
 * count EXCEEDS the books: a surplus brought in at zero cost writes the item's
 * weighted average down, so the server refuses it rather than silently
 * devaluing the warehouse.
 */
export interface PhysicalCountLineRequest {
  stock_item_id: string;
  /** "" when the count is not tracked to a location. */
  godown_id?: string;
  /** "" when the count is not tracked to a batch/lot. */
  batch_id?: string;
  qty: Money;
  rate?: Money;
}

/** The generic voucher body posted to /vouchers/{class}. */
export interface VoucherRequest {
  voucher_type_id?: string;
  party_ledger_id?: string;
  voucher_no?: string;
  voucher_date?: string;
  narration?: string;
  party?: string;
  created_by?: string;
  postings?: PostingRequest[];
  bill_allocations?: BillAllocationRequest[];
  /**
   * The ITEM GRID. When it is present ALONGSIDE a `gst` block on a GST-bearing
   * class, the voucher is an ITEM INVOICE and the server GENERATES both the GST
   * lines and the accounting postings from these lines plus the stock item
   * masters. The body must therefore NOT also carry `gst.lines` or `postings`:
   * either one is a 422, not a merge, because silently preferring one input
   * over the other is how 100 units of stock get billed as a Rs 1 invoice with
   * every layer reporting success.
   *
   * What the body still carries is what the grid cannot know: the party ledger,
   * `item_ledger_id`, the place of supply and the three tax ledger ids.
   */
  inventory?: InventoryLineRequest[];
  /**
   * The Physical Stock voucher's body: the COUNTED positions, not movements.
   *
   * It sits BESIDE `inventory` rather than replacing it because the two are
   * mutually exclusive per class and the LEDGER owns that rule — a Physical
   * Stock carrying `inventory` is refused, and any other class carrying
   * `physical_count` is refused too. Deciding it here as well would be a second
   * copy of the rule, and the drifted copy always wins for whichever caller
   * reaches it first.
   */
  physical_count?: PhysicalCountLineRequest[];
  /**
   * The revenue/expense ledger an ITEM INVOICE books the value of the goods to
   * (a Sales Accounts ledger for F8, a Purchase Accounts ledger for F9).
   *
   * Meaningful ONLY on an item invoice, where the server generates the
   * postings and therefore needs to be told which ledger the operator meant.
   * Every other voucher states its own postings and leaves this unset.
   */
  item_ledger_id?: string;
  gst?: GSTRequest | null;
}

export interface VoucherResponse {
  voucher_id: string;
  voucher_version_id: string;
  version_no: number;
  /**
   * The server-resolved display number for this voucher (prefix + zero-padded,
   * FY-scoped running counter + suffix). ALWAYS present and non-empty for an
   * accounting voucher type: auto-allocated when the client sent it empty, or
   * the exact value the client supplied (manual / override). On ALTER it is the
   * carried-forward number, identical to the create's value (numbering is never
   * re-allocated on edit).
   */
  voucher_no: string;
  cgst: Money;
  sgst: Money;
  igst: Money;
}

/**
 * Response of the class-agnostic DELETE /vouchers/{voucherID} tombstone path.
 * The backend appends an OpDelete version (the voucher is removed from reports
 * and the day book but stays in the audit log); the response returns the new
 * tombstone version so the client can confirm and update its edit-log view.
 */
export interface DeleteVoucherResponse {
  voucher_id: string;
  voucher_version_id: string;
  version_no: number;
  op: string;
  /** The deleted voucher's display number, carried through for symmetry. */
  voucher_no: string;
}

// ---------------------------------------------------------------------------
// Voucher fetch (GET /vouchers/{voucherID}) — the CURRENT head version of one
// logical voucher. This is the source of truth for hydrating an ALTER form:
// unlike a Day Book row (which carries ledger NAMEs), each posting here carries
// the REAL ledger_id uuid AND its display name, so a full-restatement alter
// resends valid uuids (buildVoucherInput's uuid.Parse succeeds). Money is
// fixed-4 strings; dr_cr is the canonical "D"/"C".
// ---------------------------------------------------------------------------

export interface PostingView {
  /** The REAL posting ledger id (uuid) — never a name. */
  ledger_id: string;
  /** The ledger's display name (for the form's read-only view). */
  ledger: string;
  dr_cr: DrCr;
  amount: Money;
}

export interface BillAllocationView {
  ledger_id: string;
  ref_name: string;
  ref_type: string;
  dr_cr: DrCr;
  amount: Money;
  /** "" when no due date is recorded. */
  due_date: string;
}

export interface VoucherView {
  voucher_id: string;
  voucher_no: string;
  voucher_date: string;
  /** The accounting class ("Sales", "Purchase", ...) from the version payload. */
  class: string;
  /** "" when not set. */
  voucher_type_id: string;
  party: string;
  /** "" when the head version has no party ledger. */
  party_ledger_id: string;
  narration: string;
  version_no: number;
  op: string;
  /** 2-digit state code, "" when not set. */
  place_of_supply: string;
  reverse_charge: boolean;
  /** Persisted GST attributes; "0.0000" when null/absent. */
  taxable_value: Money;
  cgst: Money;
  sgst: Money;
  igst: Money;
  postings: PostingView[];
  bill_allocations: BillAllocationView[];
  /**
   * The item grid as stored. Field names MIRROR {@link InventoryLineRequest}
   * where they overlap so an ALTER can re-POST what it read; the extra
   * `item`/`godown`/`batch` names are display-only. THE UUIDS ARE THE
   * LOAD-BEARING PART — re-resolving an item by name on an alter would silently
   * drop a line whose item had been renamed, turning an edit of the narration
   * into an inventory write-off.
   *
   * Optional in the type only because vouchers fetched from an older server
   * (or a fixture written before the grid existed) carry no such array; treat
   * an absent array as "no stock on this voucher".
   */
  inventory?: InventoryLineView[];
}

/** One stored item-grid line, as returned by GET /vouchers/{id}. */
export interface InventoryLineView {
  line_no: number;
  stock_item_id: string;
  /** Display name of the stock item. */
  item: string;
  /** "" when the line is not tracked to a location. */
  godown_id: string;
  godown: string;
  /** "" when the line is not tracked to a batch/lot. */
  batch_id: string;
  batch: string;
  direction: "IN" | "OUT";
  qty: Money;
  rate: Money;
  discount_pct: Money;
  amount: Money;
}

// ---------------------------------------------------------------------------
// Inventory masters (GET .../stock-items, .../godowns, .../batches)
// ---------------------------------------------------------------------------

/**
 * One stock item master. THERE ARE NO OPENING FIGURES HERE and their absence is
 * the point: an opening position belongs to an (item, godown, batch) triple,
 * not to the item — the same item legitimately opens with different quantities
 * at different rates in two locations. Ask the Stock Summary for a position.
 */
export interface StockItemListItem {
  id: string;
  name: string;
  /** "" for an item with no unit set. */
  unit_symbol: string;
  hsn: string;
  /** Fixed-4 decimal string. The rate the SERVER taxes this item at. */
  gst_rate_pct: Money;
  /** Names the valuer that replays this item's movements, e.g. "WEIGHTED_AVG". */
  valuation_method: string;
  is_system: boolean;
}
export interface ListStockItemsResponse {
  stock_items: StockItemListItem[];
}

/** One stock location. `is_system` marks the seeded 'Main Location'. */
export interface GodownListItem {
  id: string;
  name: string;
  is_system: boolean;
}
export interface ListGodownsResponse {
  godowns: GodownListItem[];
}

/**
 * One batch/lot. Batch names are ITEM-scoped, not company-scoped: two different
 * items may each carry a batch called 'B-01' and they are unrelated, so the
 * item id travels with every batch.
 */
export interface StockBatchListItem {
  id: string;
  stock_item_id: string;
  stock_item_name: string;
  name: string;
  /** ISO date, or "" when the batch carries none. */
  mfg_date: string;
  expiry_date: string;
}
export interface ListStockBatchesResponse {
  batches: StockBatchListItem[];
}

// ---------------------------------------------------------------------------
// Stock Summary (GET .../stock-summary)
// ---------------------------------------------------------------------------

/**
 * The position of one (item, godown, batch) triple as at a date.
 *
 * NOTHING HERE IS STORED. Closing quantity is a signed sum over the movement
 * ledger and closing VALUE is derived by replaying that ledger through the
 * server's valuer, because valuation is order-dependent — a back-dated purchase
 * re-prices every issue that already happened after it. `as_on` is a REPLAY
 * BOUNDARY, not a filter on a balances table.
 */
export interface StockPositionRow {
  stock_item_id: string;
  item: string;
  unit: string;
  /** "" when the dimension is untracked (the server's uuid.Nil). */
  godown_id: string;
  godown: string;
  batch_id: string;
  batch: string;
  valuation_method: string;
  opening_qty: Money;
  opening_value: Money;
  inward_qty: Money;
  inward_value: Money;
  outward_qty: Money;
  /** COST CONSUMED, never what the goods sold for. */
  outward_value: Money;
  closing_qty: Money;
  closing_value: Money;
  /**
   * PRESENTATION ONLY, 4 dp. Never multiply it back out by the closing quantity
   * to re-derive the value: the running average carries more precision than the
   * rounded rate can reproduce.
   */
  closing_rate: Money;
}

/**
 * One item's total across its locations/batches. There is deliberately no
 * `closing_rate` on an item total: two godowns of the same item carry two
 * different average costs, and the average of the averages is not the average.
 */
export interface StockSummaryItem {
  stock_item_id: string;
  item: string;
  unit: string;
  opening_qty: Money;
  opening_value: Money;
  inward_qty: Money;
  inward_value: Money;
  outward_qty: Money;
  outward_value: Money;
  closing_qty: Money;
  closing_value: Money;
  positions: StockPositionRow[];
}

export interface StockSummaryResponse {
  /** The as-at date the replay stopped at; "" when unbounded. */
  as_on: string;
  items: StockSummaryItem[];
  /** The same positions, flat — the item tree is folded from these. */
  rows: StockPositionRow[];
  /** What a Stock-in-Hand line on the Balance Sheet is worth on that date. */
  total_value: Money;
}

// ---------------------------------------------------------------------------
// Reports
// ---------------------------------------------------------------------------

export interface TrialBalanceRow {
  ledger: string;
  group: string;
  debit: Money;
  credit: Money;
}
export interface TrialBalanceResponse {
  rows: TrialBalanceRow[];
  total_debit: Money;
  total_credit: Money;
  balanced: boolean;
}

/** Node kind in a statement tree (balance sheet / P&L). */
export type NodeKind = "GROUP" | "LEDGER" | "COMPUTED";

export interface StatementNode {
  kind: NodeKind;
  name: string;
  amount: Money;
  /** Present for drillable group/ledger nodes (a UUID). */
  id?: string;
  children?: StatementNode[];
}

export interface StatementSection {
  title: string;
  nodes: StatementNode[];
  total: Money;
}

export interface BalanceSheetResponse {
  assets: StatementSection;
  liabilities: StatementSection;
  net_profit: Money;
  balanced: boolean;
}

export interface ProfitLossResponse {
  income: StatementSection;
  expenses: StatementSection;
  net_profit: Money;
}

export interface DayBookPosting {
  ledger: string;
  dr_cr: string;
  amount: Money;
}

export interface DayBookEntry {
  voucher_id: string;
  version_no: number;
  op: string;
  class: string;
  voucher_type: string;
  voucher_no: string;
  date: string;
  narration: string;
  party: string;
  debit: Money;
  credit: Money;
  postings: DayBookPosting[];
}

export interface DayBookResponse {
  from: string;
  to: string;
  entries: DayBookEntry[];
}

export type OutstandingKind = "RECEIVABLE" | "PAYABLE";

export interface OutstandingBill {
  ref_name: string;
  pending: Money;
  due_date: string;
  overdue_days: number;
  slab: string;
}

export interface OutstandingParty {
  ledger: string;
  group: string;
  pending: Money;
  bills: OutstandingBill[];
}

export interface OutstandingSlabTotal {
  label: string;
  amount: Money;
}

export interface OutstandingsResponse {
  kind: OutstandingKind;
  as_on: string;
  parties: OutstandingParty[];
  slab_totals: OutstandingSlabTotal[];
  total: Money;
}

/**
 * Query params for the report endpoints that accept a period / as-on date.
 * The index signature lets these pass directly to the http client's query
 * serializer (which keys arbitrary string→value maps).
 */
export interface DayBookQuery {
  from?: string;
  to?: string;
  [key: string]: string | undefined;
}
export interface OutstandingsQuery {
  kind?: OutstandingKind;
  as_on?: string;
  [key: string]: string | undefined;
}

// ---------------------------------------------------------------------------
// GSTR-1
// ---------------------------------------------------------------------------

export interface GSTR1Query {
  from: string;
  to: string;
  gstin?: string;
  cgst_ledger_id?: string;
  sgst_ledger_id?: string;
  igst_ledger_id?: string;
  [key: string]: string | undefined;
}

/**
 * The GSTR-1 JSON section (government schema; deeply nested). We expose the top
 * level and treat the section trees as opaque structured JSON — the reports
 * agent renders them generically. When any output-tax ledger is supplied the
 * server wraps the return in a reconciliation envelope.
 */
export interface GSTR1 {
  gstin?: string;
  fp: string;
  b2b?: unknown[];
  b2cl?: unknown[];
  b2cs?: unknown[];
  hsn: unknown;
  [key: string]: unknown;
}

export interface GSTR1Reconciled {
  gstr1: GSTR1;
  reconciliation_ok: boolean;
}

/** GSTR-1 is either the bare return or the reconciliation envelope. */
export type GSTR1Response = GSTR1 | GSTR1Reconciled;

export function isGSTR1Reconciled(r: GSTR1Response): r is GSTR1Reconciled {
  return (
    typeof r === "object" &&
    r !== null &&
    "gstr1" in r &&
    "reconciliation_ok" in r
  );
}

// ---------------------------------------------------------------------------
// Migration import (Tally-XML ingress)
// ---------------------------------------------------------------------------

export interface ImportTallyXMLQuery {
  created_by?: string;
  [key: string]: string | undefined;
}

export interface ImportedVoucher {
  voucher_id: string;
  voucher_version_id: string;
  version_no: number;
}

export interface ImportTallyXMLResponse {
  source_company: string;
  vouchers_posted: number;
  ledgers_ensured: number;
  posted: ImportedVoucher[];
}

// ---------------------------------------------------------------------------
// The EDIT LOG (GET .../vouchers/{voucherID}/history)
// ---------------------------------------------------------------------------

/**
 * One immutable entry of a voucher's edit log, as RECORDED — the postings come
 * from the hashed payload, not from the posting table, so what is displayed is
 * exactly what is signed.
 */
export interface VoucherVersionView {
  version_no: number;
  /** CREATE | ALTER | DELETE. */
  op: string;
  created_by: string;
  created_at: string;
  node_id: string;
  /** BLAKE3 over (canonical payload || prev_hash), hex. */
  hash: string;
  /** "" on version 1, which begins the chain. */
  prev_hash: string;
  class: string;
  voucher_no: string;
  voucher_date: string;
  narration: string;
  party: string;
  postings: { ledger_id: string; dr_cr: string; amount: string }[];
  bill_allocations?: {
    ledger_id: string;
    ref_name: string;
    ref_type: string;
    dr_cr: string;
    amount: string;
    due_date: string;
  }[];
}

/**
 * The server's answer to "has this record been tampered with?".
 *
 * `ok` is true only when every version recomputes to its recorded hash, each
 * links to its predecessor, and the numbering has no gaps. This is the half of
 * the edit log that makes it evidence rather than a claim, so a client MUST
 * render it — a viewer that showed the versions and dropped the verification
 * would be strictly more misleading than showing nothing.
 */
export interface ChainVerification {
  ok: boolean;
  versions_checked: number;
  /** The version_no where verification first failed, or 0 when ok. */
  broken_at_version: number;
  reason: string;
}

export interface VoucherHistoryResponse {
  voucher_id: string;
  /** True when the head is a tombstone. A deleted voucher still HAS a history. */
  is_deleted: boolean;
  versions: VoucherVersionView[];
  verification: ChainVerification;
}

// ---------------------------------------------------------------------------
// TDS DEDUCTOR STATUS (Item Zero step 2). Tally's F11 > Enable TDS >
// "TDS Deductor Details" (RESEARCH.md:692).
//
// Whether this company is required to deduct tax at source, per tax year. The
// TDS post path REFUSES every voucher for a company with no row, so this is the
// screen that makes TDS usable at all.
// ---------------------------------------------------------------------------

/** One recorded tax year's deductor status. */
export interface DeductorStatus {
  /** The calendar year the tax year BEGINS in: 2025 = 2025-26. */
  tax_year_start: number;
  /** "2025-26" — pre-rendered by the server so the client never does this sum. */
  tax_year_label: string;
  /** Tally's "deductor type": one of the s.402(11)(a)-(l) categories. */
  entity_category: string;
  /**
   * ⚠️ THE YEAR THIS ROW ACTUALLY DECIDES, which is NOT tax_year_start.
   * s.402(37)(b) tests turnover "during the tax year immediately preceding",
   * so the 2025-26 row governs payments made in 2026-27. The server derives and
   * sends both so a screen can state it instead of an operator guessing.
   */
  governs_payments_in: number;
  governs_payments_in_label: string;
  /**
   * EMPTY when the turnover test does not apply to this category — which is a
   * different statement from "0". A COMPANY is a specified and designated
   * person whatever it turned over, so it records no figure at all.
   */
  prior_year_turnover_rupees: string;
  /** "BUSINESS" or "PROFESSION"; empty when not applicable. */
  prior_year_activity: string;
  recorded_by: string;
  recorded_at: string;
}

export interface DeductorStatusListResponse {
  deductor_statuses: DeductorStatus[];
  /**
   * The category vocabulary, SERVED rather than hard-coded here. A fourth copy
   * of a closed list the migration owns would drift, and a picker offering a
   * category the database refuses fails only on save.
   */
  entity_categories: string[];
  prior_year_activity: string[];
}

export interface SetDeductorStatusRequest {
  entity_category: string;
  /** Omit or send "" when the turnover test does not apply. Never "0". */
  prior_year_turnover_rupees?: string;
  prior_year_activity?: string;
  recorded_by: string;
}

export interface SetDeductorStatusResponse {
  tax_year_start: number;
  tax_year_label: string;
  governs_payments_in: number;
  governs_payments_in_label: string;
}

// ---------------------------------------------------------------------------
// s.394(2) MANUFACTURING DECLARATIONS (Item Zero step 3).
//
// A resident buyer's written statement that goods are for manufacturing and NOT
// for trading, which exempts collection at source on Table Sl. Nos. 1-5. The
// post path READS these when deriving TCS on a sale, so filing one here is what
// makes the next sale to that buyer exempt.
// ---------------------------------------------------------------------------

export interface TCSDeclaration {
  buyer_ledger_id: string;
  buyer_name: string;
  section: string;
  tax_year_start: number;
  tax_year_label: string;
  /** The date ON the document. */
  declaration_date: string;
  /** When the collector received it — s.394(3)'s deadline runs off this. */
  received_date: string;
  /**
   * s.394(3): a copy must reach the Commissioner "on or before the seventh day
   * of the month following the month of receipt". DERIVED by the server and
   * shown, because storing declarations without surfacing the deadline they
   * create would build a compliance trap.
   */
  forward_due_date: string;
  /** Empty when none was furnished — a meaningful state, see pan_valid. */
  buyer_pan: string;
  /**
   * ⚠️ FALSE DOES NOT MEAN "NO DECLARATION". s.397(2)(f)(i) makes a PAN-less
   * declaration INVALID, and (g) then requires the PENAL rate — so the buyer
   * pays 5% instead of 2%, which is WORSE for them than furnishing nothing.
   */
  pan_valid: boolean;
  recorded_by: string;
}

export interface TCSDeclarationListResponse {
  tcs_declarations: TCSDeclaration[];
  /** The sections s.394(2) reaches. Motor vehicles (Sl. No. 6) are not among them. */
  eligible_sections: string[];
}

export interface CreateTCSDeclarationRequest {
  buyer_ledger_id: string;
  section: string;
  tax_year_start: number;
  declaration_date: string;
  received_date: string;
  /** May be EMPTY — that is the s.397(2)(f)(i) invalid state, not a missing field. */
  buyer_pan?: string;
  recorded_by: string;
}

export interface CreateTCSDeclarationResponse {
  buyer_ledger_id: string;
  section: string;
  tax_year_start: number;
  tax_year_label: string;
}

// ---------------------------------------------------------------------------
// THE TDS / TCS REGISTER (Item Zero step 4). GET /tds-register?from=&to=
//
// Everything Item Zero derived, refused and stored was invisible until this.
// Denied to the seeded Data Entry level by migration 0068 — it is every supplier
// paid and every customer collected from, with amounts.
// ---------------------------------------------------------------------------

export interface TDSRegisterDeduction {
  date: string;
  voucher_no: string;
  voucher_id: string;
  deductee: string;
  section: string;
  deductee_type: string;
  /** Form 140 Annexure 2 payment code, decided at derivation time. */
  return_code: string;
  base: Money;
  rate_pct: string;
  tds: Money;
  pan_available: boolean;
}

export interface TDSRegisterCollection {
  date: string;
  voucher_no: string;
  voucher_id: string;
  buyer: string;
  section: string;
  base: Money;
  rate_pct: string;
  tcs: Money;
  /**
   * NONE | EXEMPT | INVALID_NO_PAN — the only field that distinguishes the three
   * different meanings a zero in `tcs` can carry.
   */
  declaration: string;
  pan_available: boolean;
}

/** One thing that must be obtained before a return file can be emitted. */
export interface ReturnFileBlocker {
  what: string;
  where: string;
  why: string;
}

/**
 * The machine-readable refusal (invariant I-7). A client must branch on
 * `available`, never on the presence of the other fields.
 */
export interface ReturnFileStatus {
  available: boolean;
  /**
   * The prescribed file-format version, read from a primary source ("1.0").
   *
   * ⚠️ ITS MEANING CHANGED. It used to be empty, and its emptiness was I-7 in
   * one field. The format has since been read and implemented, so an empty
   * value still means "no primary source" but a non-empty one does NOT mean
   * "ready to file". Branch on `available`, never on this.
   */
  format_version: string;
  /** The document `format_version` was read from — a version with no document
   * behind it is not a citation. */
  format_source: string;
  summary: string;
  /** ALWAYS false: the FVU needs a JRE that does not exist in this environment. */
  validated: boolean;
  validation_note: string;
  blockers: ReturnFileBlocker[];
  what_you_can_do_instead: string;
}

export interface TDSRegisterResponse {
  from: string;
  to: string;
  /** 0 when the period spans two tax years — no single return covers it. */
  tax_year_start: number;
  deductions: TDSRegisterDeduction[];
  collections: TDSRegisterCollection[];
  total_base_deducted: Money;
  total_tds: Money;
  total_base_collected: Money;
  total_tcs: Money;
  sub_threshold_deductions: number;
  exempt_collections: number;
  /** What this register CANNOT tell you. Rendered, never suppressed. */
  disclosures: string[];
  return_file: ReturnFileStatus;
}
