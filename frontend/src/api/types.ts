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

/**
 * The `class` LABEL the server stamps into a voucher version's payload, per
 * class. This is a WIRE FACT, not a display choice: it is the exact string
 * `payload->>'class'` yields, which is what `DayBookEntry.class` and
 * `VoucherView.class` carry.
 *
 * Recorded from the Go constants, which are the authority:
 * `backend/internal/ledger/ledger.go` (the eight accounting classes) and
 * `backend/internal/ledger/nonaccounting.go` (the four stock classes).
 * `TestClassLabelsAreTheWireContract` pins each one on the Go side and names this
 * table, so a rename there fails a test rather than silently changing a string
 * two layers away.
 *
 * ⛔⛔ IT IS NOT DERIVABLE FROM THE SLUG, and assuming it was is how the mock
 * diverged from the server for the whole life of the field. The mock wrote
 * `cls.toUpperCase()` ("SALES") into the Day Book and a naive title-case
 * ("Credit-note") into the GET view, where the server writes "Sales" and
 * "Credit Note". Nothing caught it because no screen RENDERED `class` — until the
 * Day Book drill began routing on it, at which point a slugify-based guess would
 * have silently refused to open half the voucher classes.
 *
 * The Record is exhaustive over {@link VoucherClass}, so tsc refuses a new class
 * that does not state its label.
 */
export const VOUCHER_CLASS_LABELS: Readonly<Record<VoucherClass, string>> = {
  sales: "Sales",
  purchase: "Purchase",
  receipt: "Receipt",
  payment: "Payment",
  contra: "Contra",
  journal: "Journal",
  "credit-note": "Credit Note",
  "debit-note": "Debit Note",
  "delivery-note": "Delivery Note",
  "receipt-note": "Receipt Note",
  "stock-journal": "Stock Journal",
  "physical-stock": "Physical Stock",
};

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

  /**
   * Allow a voucher to drive a stock position BELOW ZERO (`company.allow_
   * negative_stock`, migration 0025). Defaults to `false` — refuse — when
   * omitted, and that default is deliberate: an escape hatch nobody asked for
   * must be shut.
   *
   * ⛔ THIS KEY WAS ACCEPTED AND SILENTLY DISCARDED BY THE LIVE ROUTE until
   * register row D43 was closed. `decodeJSON` server-side does not reject
   * unknown fields, so a create that asked for `true` still answered 201 while
   * the column stayed `false` — the caller was told a setting had been applied
   * that never was. Do not assert this from the 201; read it back through
   * {@link CoredClient.getCompany}.
   */
  allow_negative_stock?: boolean;
}

export interface CreateCompanyResponse {
  company_id: string;
}

/**
 * The body of PUT /v1/companies/{companyID} — the company FEATURE settings.
 *
 * Every field is OPTIONAL and an omitted field LEAVES THE SETTING ALONE. It is
 * not a full restatement of the company: a PUT that reset the settings it was
 * not told about would be a worse defect than the one this route closes.
 *
 * Narrow by design. The name, the financial year, the state code and the
 * currency block are identity that posted books depend on and are NOT writable
 * here — moving `fy_start_date` under existing vouchers would re-file them into
 * a different year.
 */
export interface CompanyFeaturesRequest {
  /** See {@link CreateCompanyRequest.allow_negative_stock}. */
  allow_negative_stock?: boolean;
}

/**
 * The 200 body of PUT /v1/companies/{companyID}.
 *
 * ⛔ THIS IS THE ROW, NOT AN ECHO OF THE REQUEST. The server re-reads the
 * column after the UPDATE and answers with what it holds, because an echo is
 * structurally incapable of distinguishing "applied" from "accepted and thrown
 * away" — which is precisely what D43 was.
 */
export interface CompanyFeaturesResponse {
  id: string;
  allow_negative_stock: boolean;
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
  /**
   * See {@link CreateCompanyRequest.allow_negative_stock}. On the READ so a
   * control can show its own current state: both stock enforcers have acted on
   * this column since migration 0025, and until it appeared here no client
   * could learn the value of a setting it was already subject to.
   */
  allow_negative_stock: boolean;
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
   * PAN and credit days, completing this type's own contract — "a client can
   * read a ledger and re-send it". Both were WRITABLE and NOT READABLE until
   * 2026-08-22, so a party's PAN could be stated and never seen again through
   * HTTP. `credit_days` is `null` when no term was agreed, which is genuinely
   * different from `0` ("due immediately").
   */
  pan: string;
  credit_days: number | null;
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
  /**
   * The voucher CLASS ("Receipt", "Payment", "Contra", …).
   *
   * ⛔ NOT DECORATION — it is what makes a row identifiable. Numbering is per
   * (class, financial year), so `voucher_no` alone is NOT unique on one bank
   * ledger: the seeded demo shows FOUR legs on HDFC Bank all reading "1".
   * Rendering the number without the class gives an operator four identical
   * rows and no way to know which voucher they just ticked.
   */
  class: string;
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
  "two-columns" | "signed-amount" | "amount-and-flag";

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
   * The party's agreed payment term in days — what makes an Outstandings
   * ageing bucket mean anything for a bill with no explicit due date.
   *
   * OPTIONAL, and `undefined` is a REAL state distinct from `0`: "no term
   * agreed" is not "due immediately", and collapsing them would make every
   * party that never stated a term look like a cash party.
   *
   * ⛔ THE WIRE ACCEPTED THIS KEY AND DISCARDED IT until 2026-08-22 — D43's
   * shape on a second route. `masters.Ledger.CreditDays` existed and the INSERT
   * bound it, but `ledgerReq` had no field to fill it from, so it was nil on
   * every create. Measured, not inferred: POST with `credit_days: 45` -> 201,
   * `SELECT credit_days` -> NULL.
   */
  credit_days?: number;
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
  /**
   * A COMPOUND unit (T10): 1 <symbol> = <conversion_factor> <base_unit_symbol>,
   * e.g. 1 BOX = 12 Nos.
   *
   * ⛔ BOTH OR NEITHER — the server refuses half of one, because a base with no
   * factor cannot be converted and a factor with no base converts to nothing.
   * ⛔ AND THE FACTOR IS A DECIMAL STRING, like every other number on this wire:
   * a JSON number is a float, and a float conversion factor is how one BOX
   * quietly becomes 11.999999 pieces.
   */
  base_unit_symbol?: string;
  conversion_factor?: Money;
  created_by?: string;
}

/**
 * One unit of measure as stored. `decimal_places` is what tells a grid how many
 * decimals a quantity in this unit may carry.
 *
 * A COMPOUND unit carries `base_unit_symbol` and `conversion_factor`; both are ""
 * for a simple one.
 *
 * ⚠️ DO NOT MULTIPLY A QUANTITY BY THIS FACTOR IN A CLIENT. Quantities are stored
 * in the item's own unit and RESTATED by the server (the Stock Summary's
 * base_qty); a client that converted as well would convert twice.
 */
export interface UnitListItem {
  id: string;
  symbol: string;
  formal_name: string;
  decimal_places: number;
  is_system: boolean;
  base_unit_symbol: string;
  conversion_factor: Money | "";
}
export interface ListUnitsResponse {
  units: UnitListItem[];
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
  /**
   * THE COSTING METHOD (T9): "WEIGHTED_AVG" (the default), "FIFO" or "LIFO".
   *
   * ⛔ OMITTED MEANS THE DEFAULT, and it is optional so that every caller
   * written before T9 keeps creating weighted-average items. Do not send a
   * method the server does not offer: GET .../inventory-vocabulary is the list,
   * and the server refuses anything else at the MASTER rather than letting the
   * Stock Summary and the Balance Sheet fail later for the whole company.
   */
  valuation_method?: string;
  /**
   * The item's STOCK GROUP (T10). Absent leaves it UNGROUPED.
   *
   * ⚠️ THE GROUP MUST ALREADY EXIST — the server refuses an unknown one rather
   * than creating it, because a group minted from a misspelling subtotals half a
   * company's stock under a name nobody meant.
   */
  stock_group_name?: string;
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
 * THE `tds` BLOCK ON A VOUCHER BODY (server.go's `tdsReq`) — tax DEDUCTED from a
 * payment this company made.
 *
 * ⛔ THIS TYPE'S ABSENCE WAS REGISTER ROW D3, AND IT IS THE SAME DEFECT
 * {@link VoucherRequest.cost_allocations} carries a warning about, for the third
 * time in this file: the Go wire has carried `tds` and `tcs` since T6 was wired,
 * the ledger derives and PERSISTS the deduction, the TDS/TCS Register reads it
 * and the challan mechanism depends on it — and no client type could express it,
 * so on any books a real operator keyed there were no deductions at all and the
 * challan's allocation picker was empty. A FEATURE IS NOT REACHABLE UNTIL THE
 * FIELD EXISTS ON EVERY WIRE BETWEEN THE OPERATOR AND THE TABLE: Go struct, TS
 * type, AND the form that fills it.
 *
 * ⚠️ THE FORM IS STILL MISSING. This type makes the request EXPRESSIBLE; it does
 * not put a TDS panel on the voucher screen, and until that panel exists a
 * deduction still cannot be keyed. Do not read the presence of this interface as
 * the feature being reachable.
 *
 * ⚠️ TWO SERVER-SIDE PRECONDITIONS, both 422s, because they are facts about the
 * company rather than about the body:
 *   - `deductee_ledger_id` is REQUIRED — who was paid is half the key of the
 *     statutory aggregate and the whole of the return's deductee annexure;
 *   - the company must have a deductor status recorded for the tax year
 *     PRECEDING the voucher's, or the post is refused outright. Whether there is
 *     a duty to deduct is not a question this software guesses at.
 */
export interface TDSRequest {
  /** The nature-of-payment key, e.g. "194C", "194J-P", "194I-B". */
  section: string;
  /** REQUIRED. The ledger of the party being paid. */
  deductee_ledger_id: string;
  /** "IND_HUF" or "OTHER"; empty means "OTHER". */
  deductee_type?: string;
  /** False triggers the statutory no-PAN floor — it RAISES the rate. */
  pan_available?: boolean;
  /** Fixed-point decimal STRING. The amount the deduction applies to. */
  taxable_amount: Money;
  /**
   * OPTIONAL. When given, the server cross-checks the posted TDS leg against the
   * amount its own engine derives and REFUSES a voucher whose leg contradicts it.
   * When empty the deduction is still derived and stored; no leg is verified.
   * ⛔ The server never injects a posting of its own.
   */
  tds_ledger_id?: string;
  /** Selects the effective-dated rate table; empty means the ledger's default. */
  ruleset_version?: string;
}

/**
 * THE `tcs` BLOCK ON A SALES VOUCHER (server.go's `tcsReq`) — tax COLLECTED from
 * a buyer. The mirror of {@link TDSRequest}, and register row D3's other half.
 *
 * ⛔ THERE IS NO EXEMPTION FIELD HERE AND THERE MUST NEVER BE ONE. A s.394(2)
 * exemption rests on a WRITTEN DECLARATION the collector holds and must forward
 * to the Commissioner; the post path LOOKS IT UP by (buyer, section, tax year)
 * from the declarations table. A `declaration: true` flag on this body would make
 * the exemption an entry-time opinion — an operator could zero the tax on any
 * sale by typing a boolean, with no document behind it.
 *
 * `pan_available` IS here, because that is a fact about the buyer master rather
 * than a claim about a document, and getting it wrong raises the rate rather than
 * removing the tax.
 */
export interface TCSRequest {
  /** The collection key: "206C(1)-SCRAP" or "206C(1F)-MV". */
  section: string;
  /** REQUIRED. The declaration's lookup key and the return's collectee. */
  buyer_ledger_id: string;
  /** False triggers the penal rate — NOT the deduction side's flat floor. */
  pan_available?: boolean;
  /** Fixed-point decimal STRING. The sale consideration collected on. */
  taxable_amount: Money;
  /** OPTIONAL; when given the posted TCS leg is verified against the engine. */
  tcs_ledger_id?: string;
  /** Selects the effective-dated rate table; empty means the ledger's default. */
  ruleset_version?: string;
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
  /**
   * HOW THIS VOUCHER'S POSTINGS WERE SPLIT ACROSS COST CENTRES (T2, migration
   * 0040). See {@link CostAllocationInput} — and read its warning about
   * `posting_seq` before building any UI that sets this.
   *
   * ⛔ THIS FIELD EXISTS BECAUSE ITS ABSENCE WAS THE ENTIRE T2 REACHABILITY
   * DEFECT, ONE LAYER UP. The Go wire (`voucherReq.cost_allocations`) was fixed
   * late in the session that built T2, and this type was not — so the server
   * accepted allocations that no browser could construct, both cost reports
   * answered 200, and they were CORRECT: they accurately reported that nothing
   * had been allocated, because nothing could be. `item_ledger_id` above carries
   * a comment describing the identical defect from an earlier feature. That makes
   * three occurrences of one shape, so state it as a rule: A FEATURE IS NOT
   * REACHABLE UNTIL THE FIELD EXISTS ON EVERY WIRE BETWEEN THE OPERATOR AND THE
   * TABLE — Go struct, TS type, and the form that fills it.
   *
   * ⚠️ REFUSED OUTRIGHT ON AN ITEM INVOICE (inventory + gst on a GST-bearing
   * class): its postings are generated inside the server's transaction and index
   * 0 is the PARTY leg, so a client-supplied posting number would attribute the
   * cost to a receivable. Allocate on a Journal, Payment, Receipt or Contra, or
   * on a voucher with stated postings.
   */
  cost_allocations?: CostAllocationInput[];
  /**
   * WHICH ORDER LINES THIS VOUCHER'S INVENTORY CONSUMED (T3, migration 0042).
   * See {@link FulfilmentInput}.
   *
   * ⚠️ HONOURED ON AN ITEM INVOICE, unlike `cost_allocations` directly above,
   * and the asymmetry is worth knowing because it looks like an inconsistency.
   * An allocation is refused there because the POSTINGS are server-generated; a
   * fulfilment names an INVENTORY line, which is the operator's own grid on
   * every class. An invoice that fulfils an order directly, with no separate
   * delivery note, is one of the two ordinary ways an order is closed.
   */
  fulfilments?: FulfilmentInput[];
  gst?: GSTRequest | null;
  /**
   * TAX DEDUCTED AT SOURCE on this voucher. See {@link TDSRequest} — including
   * the warning that this field being expressible is NOT the same as a deduction
   * being keyable, because no screen renders a TDS panel yet.
   *
   * Honoured on any class that states its own postings; the deduction is derived,
   * cross-checked against the leg the body posts, and PERSISTED, which is what
   * the TDS/TCS Register and the whole challan mechanism read.
   */
  tds?: TDSRequest | null;
  /**
   * TAX COLLECTED AT SOURCE. Honoured on SALES only — a collection is made by
   * the seller. See {@link TCSRequest}.
   */
  tcs?: TCSRequest | null;
  /**
   * THE insightd BILL DRAFT THIS POST IS ACCEPTING (§7.3 B, migration 0077).
   * Unset on every hand-keyed voucher, which is nearly all of them.
   *
   * ⛔⛔ IT IS WHAT MAKES "ACCEPT THIS AI BILL ONCE" A GUARANTEE INSTEAD OF A
   * CLAIM, AND THE REASON IS THAT THE SERVER ENFORCES IT IN THE VOUCHER'S OWN
   * TRANSACTION. Accepting a draft is two writes to two services: this POST, and
   * then telling insightd the draft is `accepted`. When that second write is lost
   * — tab closed, network dropped, laptop asleep — insightd keeps offering the
   * bill as `ready`. Every guard that READS a status (the session memo, the
   * durable memo, the localStorage retry queue, the day-book lookup, the claim
   * lease) is a check-then-act, and all of them live in ONE BROWSER: a second
   * machine inside that window walked straight through, which is why
   * draftAccept.test.ts used to pin a test asserting TWO real vouchers.
   *
   * Sending it here puts the decision under a unique index on
   * (company_id, source_draft_id): the loser rolls back with no voucher, no
   * numbering advance and no input tax, and gets a 409 whose `detail.code` is
   * `draft_already_accepted` — the SAME code and fields insightd's own claim
   * route mints, so {@link acceptPurchaseDraft} translates one refusal shape and
   * not two.
   *
   * ⚠️ IT IS AN OPAQUE insightd IDENTIFIER, NOT A UUID (its corpora use ids like
   * "CO-DEMO/bill-3"). The only property the server needs is that two accepts of
   * one draft spell it identically.
   */
  draft_id?: string;
}

/**
 * ONE inventory line of this voucher consuming ONE order line — "line 1 of this
 * delivery note ships 5 of the 20 that sales order SO-14 committed" (T3,
 * migration 0042).
 *
 * ⛔⛔ `qty` IS A LINK, NOT A MOVEMENT, and this is the one thing a UI here can
 * get wrong in a way nothing catches. It is how much of the ORDER was consumed;
 * {@link InventoryLineRequest.qty} is how much STOCK moved. They are usually
 * equal and they are different facts — closing stock reads the movement, the
 * order book reads this, and neither reads the other.
 *
 * The case where they legitimately differ is the reason the distinction is
 * written down: shipping 105 against a 100-unit order records a movement of 105
 * (the goods really left, closing stock is right) and a link of 100. The extra 5
 * consumed no order, because nobody ordered them. So a form must NOT default this
 * to the line quantity and lock it — over-fulfilment is refused by the server and
 * by a deferred DB trigger, and an operator who genuinely over-shipped would be
 * unable to save at all.
 *
 * ⚠️ `line_no` IS 1-BASED, where {@link CostAllocationInput.posting_seq} on the
 * same request body is 0-BASED. Inherited, not chosen: `stock_movement.line_no`
 * has been a 1-based column since migration 0025. A client that sends an array
 * index here will be exactly one out, and the server says so in the error rather
 * than surfacing a foreign-key name.
 */
export interface FulfilmentInput {
  /** The voucher's own 1-BASED inventory line. Not an array index. */
  line_no: number;
  /** The order LINE, never the order: fulfilment is per item. */
  order_line_id: string;
  /**
   * fixed-4 string. How much of the ORDER this line consumed — see the interface
   * note. Must not exceed what the line moved, and the sum across every order one
   * line names must not either: forty units of promise cannot be discharged by
   * twenty units of goods, and no single order would look over-fulfilled.
   */
  qty: Money;
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
  /**
   * WHICH ORDER LINES THIS VOUCHER'S INVENTORY DISCHARGED, as stored (T3).
   *
   * ⛔⛔ AN ALTER RE-STATES THESE, SO A FORM THAT REBUILDS A VOUCHER WITHOUT THEM
   * DELETES THEM. `order_fulfilment` keys on (voucher_version_id, line_no): whatever
   * the alter body carries becomes the current version's links and whatever it omits
   * is simply not written. So the ordinary round trip — open a delivered delivery
   * note, fix a typo in the narration, save — silently RE-OPENS the order. Nothing
   * refuses it, because zero links is a legal state: no trigger fires, no total stops
   * adding up, and the only trace is an order book that starts reporting a commitment
   * the company already met.
   *
   * This field, its Go counterpart and the `fulfilments` key on the wire were ALL
   * added together, because that is the rule this defect keeps teaching: a feature is
   * not reachable — and here, not SAFE — until the field exists on every wire between
   * the operator and the table.
   *
   * ⚠️ ALWAYS PRESENT (possibly empty) from a current server. It is optional in the
   * type only for a voucher fetched from a build that predates it, where an absent
   * array and an empty one mean different things: "this endpoint does not tell me
   * about links" versus "this voucher has none". A form that cannot tell them apart
   * must not silently restate none.
   */
  fulfilments?: FulfilmentView[];
  /**
   * HOW THIS VOUCHER'S POSTINGS WERE SPLIT ACROSS COST CENTRES, as stored (T2).
   *
   * ⛔⛔ AN ALTER RE-STATES THESE, SO A FORM THAT REBUILDS A VOUCHER WITHOUT THEM
   * DESTROYS A DEPARTMENTAL SPLIT OF A COST THAT IS STILL IN THE PROFIT & LOSS.
   * Nothing refuses it, because zero allocations is a legal state: the sum trigger has
   * nothing to fire on, and the only trace is a Cost Centre Summary that quietly stops
   * adding up to the P&L. `ledger.VoucherView` has carried this since T2 with a doc
   * comment saying exactly that — and `voucherFullJSON` DROPPED it, so every client
   * rebuilding an alter from this response cleared the split on an unrelated edit. The
   * Go struct was right and the wire was not.
   *
   * ⚠️ ALWAYS PRESENT (possibly empty) from a current server; optional in the type
   * only for a voucher fetched from a build that predates the key, where absent and
   * empty mean different things.
   */
  cost_allocations?: CostAllocationView[];
  /**
   * THE PER-LINE GST DETAIL THIS VOUCHER'S TAX WAS DERIVED FROM (migration 0016).
   *
   * ⛔⛔ AN ALTER RE-STATES THESE, AND THIS IS THE WORST MEMBER OF THAT CLASS,
   * BECAUSE THE MISSING FIELD IS AN INPUT AND NOT A RECORD. `taxable_value`,
   * `cgst`, `sgst` and `igst` above are what the derivation PRODUCED. The RATE it
   * was derived AT — and the HSN it was stated under — live only here, and no rate
   * is recoverable from the amounts (an exempt line and a nil-value line both
   * produce zero tax).
   *
   * So while this key did not exist, a form rebuilt from GET /vouchers/{id} had no
   * rate to put back in its `gst` block, and the ordinary round trip on an
   * ACCOUNTING-mode GST invoice — open it, fix the narration, save — re-derived the
   * tax at 0%: the party leg fell by the whole tax (11,800 becomes 10,000), the
   * version's GST columns went to zero, and the supply left both the GSTR-1 b2b
   * section and the table-12 HSN summary. Nothing refused it, because a supply
   * with no tax is a legal state.
   *
   * ⚠️ AN ITEM INVOICE IS NOT EXPOSED TO THAT: its lines are generated server-side
   * from the stock-item master, so the rate is re-derived from something the round
   * trip cannot lose. It is the accounting-mode voucher — a taxable value and a
   * typed rate, no stock — where the rate exists nowhere but the request that
   * carried it.
   *
   * ⚠️ FIELD NAMES MIRROR {@link GSTLineRequest} where they overlap
   * (`taxable_value` / `gst_rate_pct` / `hsn`) so these objects can be lifted
   * STRAIGHT back into `gst.lines`; the three tax amounts are extra and are
   * ignored by the server's decoder, which re-derives them. A client that stated
   * them would be offering a second opinion about the tax.
   *
   * ⚠️ ALWAYS PRESENT (possibly empty) from a current server; optional in the type
   * only for a voucher fetched from a build that predates the key, where absent and
   * empty mean different things — "this endpoint does not tell me about GST" versus
   * "this voucher has none", and only the second is safe to restate as nothing.
   */
  gst_lines?: GSTLineView[];
  /**
   * THE STATUTORY DEDUCTION / COLLECTION THIS VOUCHER RECORDED — register row
   * D88. `null` (or absent) when it carried neither.
   *
   * ⛔⛔ IT IS HERE SO AN ALTER CAN RE-STATE IT. `tds_deduction` is deleted and
   * re-derived from the request body on EVERY alter, so a form that cannot read
   * the block cannot send it back — and the ordinary round trip (open a voucher
   * that withheld tax, fix the narration, save) DELETES the deduction while the
   * withheld credit stays posted. The books look untouched; the return has lost
   * it, and the challan that paid it keeps an allocation matching no row.
   *
   * ⚠️ `null` AND `undefined` MEAN DIFFERENT THINGS here, as they do for
   * `gst_lines` above: null is "this voucher recorded none", undefined is "this
   * source does not tell me". Only the first is safe to restate as nothing.
   */
  tds?: TDSDeductionView | null;
  tcs?: TCSCollectionView | null;
}

/**
 * The stored deduction, shaped so a client can send it straight back as the
 * request's `tds` block.
 *
 * ⛔ THE DERIVED FIELDS ARE FOR READING, NEVER FOR RESENDING. `rate_pct` and
 * `tds_amount` are what the SERVER's rate table produced; echoing them into a
 * request would state a second opinion about a table the client does not own,
 * and the decoder ignores them anyway.
 *
 * ⚠️ THEY ARE OPTIONAL BECAUSE THE MOCK DOES NOT DERIVE THEM. `api/mock.ts`
 * echoes what the operator stated and omits the derivation rather than
 * implementing a second copy of `internal/compliance` — so a test that asserts a
 * rate against the mock fails LOUDLY on `undefined` instead of quietly reading a
 * plausible zero. That looseness is deliberate and is named here rather than
 * left to be discovered (cf. register row D84).
 *
 * ⚠️ AND THERE IS NO LIABILITY LEG. `tds_ledger_id` is an INPUT to the server's
 * cross-check and is not stored — a client re-stating a deduction finds the leg
 * among the postings in this same response, and sending none is legal and is
 * DISCLOSED rather than refused.
 */
export interface TDSDeductionView {
  leg_seq: number;
  section: string;
  deductee_ledger_id: string;
  deductee_type: string;
  pan_available: boolean;
  /** The base the deduction applied to — an OPERATOR-stated fact, so resendable. */
  taxable_amount: Money;
  rate_pct?: Money;
  tds_amount?: Money;
  deduction_date?: string;
  tax_year_start?: number;
  return_code?: string;
}

/** The collection-side mirror. See {@link TDSDeductionView} for every caveat. */
export interface TCSCollectionView {
  leg_seq: number;
  section: string;
  buyer_ledger_id: string;
  pan_available: boolean;
  taxable_amount: Money;
  rate_pct?: Money;
  tcs_amount?: Money;
  collection_date?: string;
  tax_year_start?: number;
  /**
   * What the s.394(2) declaration lookup CONCLUDED at post time.
   *
   * ⛔ DELIBERATELY NOT PART OF THE REQUEST BLOCK. An exemption rests on a
   * document the collector holds; letting a client send the outcome back would
   * make it an entry-time opinion. The post path looks it up again.
   */
  declaration_outcome?: string;
}

/**
 * One stored `voucher_gst_line` row, as returned by GET /vouchers/{id}: the INPUT
 * to the tax derivation beside the OUTPUT it produced.
 *
 * ⚠️ `line_no` IS 1-BASED and STORED, like {@link FulfilmentView.line_no} and
 * unlike {@link CostAllocationView.posting_seq}. It indexes nothing else on the
 * body — an accounting-mode GST voucher has GST lines and no inventory at all, so
 * pairing the two by position would be a guess.
 */
export interface GSTLineView {
  line_no: number;
  /** "" when the client stated no code — legal, and it takes the supply out of the GSTR-1 table-12 summary. */
  hsn: string;
  taxable_value: Money;
  /** ⛔ THE FIELD THE ROUND TRIP EXISTED WITHOUT. e.g. "18.0000". */
  gst_rate_pct: Money;
  /** Derived, not stated. Present so a form can display the split it read. */
  cgst: Money;
  sgst: Money;
  igst: Money;
}

/**
 * One stored cost-centre split, as returned by GET /vouchers/{id}.
 *
 * ⛔ `posting_seq` IS 0-BASED AND IT INDEXES **THIS RESPONSE'S** `postings` ARRAY — it
 * is RE-DERIVED on every read, because `posting` has no ordinal column. The response
 * orders postings by (dr_cr DESC, amount DESC), which is NOT the order a rebuilt
 * request will send them in, so this number may be used to MATCH a split to a posting
 * and must never be carried back onto the wire. Recompute it from the new array.
 *
 * ⚠️ It is 0-BASED where {@link FulfilmentView.line_no} on this same body is 1-BASED
 * and STORED. Both are inherited; neither is negotiable.
 */
export interface CostAllocationView {
  /** ⚠️ NOT FOR DISPLAY, and not for sending back. See the interface note. */
  posting_seq: number;
  cost_category_id: string;
  cost_category_name: string;
  cost_centre_id: string;
  /** The centre's name as stored. */
  cost_centre_name: string;
  amount: Money;
}

/**
 * One stored order link, as returned by GET /vouchers/{id}.
 *
 * ⚠️ `line_no` IS 1-BASED and indexes {@link VoucherView.inventory} at `line_no - 1`.
 * Unlike a cost allocation's `posting_seq` — which is 0-based and RE-DERIVED, because
 * `posting` has no ordinal column — this is the stored `order_fulfilment.line_no`, so
 * a client sends back exactly the number it was given.
 */
export interface FulfilmentView {
  line_no: number;
  /** "" when the link's order cannot be resolved (the join is LEFT). */
  order_id: string;
  order_line_id: string;
  /** Display: the order's human number, e.g. "SO-14". */
  order_no: string;
  /** Display: the item the ORDER line names, so a mis-link is visible. */
  stock_item: string;
  /**
   * How much of the ORDER this line consumed. ⛔ NOT how much stock moved — that is
   * the `qty` on the matching `inventory` entry. They are usually equal and they are
   * different facts: closing stock reads the movement, the order book reads this.
   */
  qty: Money;
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
  /**
   * The item's STOCK GROUP (T10), or "" for an UNGROUPED item — a real state
   * (Tally's "under Primary") and not a missing value: every item created before
   * T10 is ungrouped, so a picker that hid them would hide most of a company's
   * stock.
   */
  stock_group: string;
}

/**
 * One node of the item hierarchy (T10), as served in TREE ORDER: a parent always
 * precedes its children, and `depth` is what a client indents by.
 *
 * ⚠️ RE-SORTING THE LIST BREAKS THE TREE. The order is the server's ltree path
 * order; sorting by name would put a child above its parent and the indentation
 * would describe a hierarchy that does not exist.
 */
export interface StockGroupListItem {
  id: string;
  name: string;
  /** "" for a top-level group. */
  parent_name: string;
  depth: number;
  /** How many items sit DIRECTLY in this group (not in its descendants). */
  item_count: number;
}
export interface ListStockGroupsResponse {
  stock_groups: StockGroupListItem[];
}
/**
 * A foreign currency as the server stores it (T8).
 *
 * ⛔ `latest_rate` IS A STRING AND MAY BE EMPTY, AND EMPTY IS NOT ZERO. A
 * currency with no rate on file cannot be used to key a voucher; rendering "0"
 * would offer the choice anyway and value the whole invoice at nothing. The
 * rate carries TEN decimals — more than `Money` — so it must never be parsed
 * with parseFixed4, which truncates the fifth.
 */
export interface CurrencyListItem {
  id: string;
  code: string;
  symbol: string;
  formal_name: string;
  decimal_places: number;
  /** "" when no rate has ever been entered. */
  latest_rate: string;
  /** "" when no rate has ever been entered. */
  latest_rate_date: string;
}
export interface ListCurrenciesResponse {
  currencies: CurrencyListItem[];
}
export interface CreateCurrencyRequest {
  code: string;
  symbol: string;
  formal_name: string;
  decimal_places: number;
  created_by?: string;
}
export interface CreateCurrencyResponse {
  currency_id: string;
}
export interface SetCurrencyRateRequest {
  code: string;
  effective_date: string;
  /** Base units per ONE foreign unit, as a STRING. Never a number. */
  rate: string;
  created_by?: string;
}

export interface CreateStockGroupRequest {
  name: string;
  /** An EXISTING group, or absent for a top-level one. */
  parent_name?: string;
  created_by?: string;
}
export interface CreateStockGroupResponse {
  stock_group_id: string;
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

/**
 * One costing method the SERVER offers, with Tally's own name for it and one
 * line on what the closing stock ends up carried at.
 *
 * ⛔ SERVED, NEVER HARDCODED. A picker built from a client-side list can offer a
 * method the server refuses, and the operator meets that refusal after keying
 * the whole master — and the list will grow (Tally ships eight methods, this
 * build implements three).
 */
export interface ValuationMethodOption {
  method: string;
  label: string;
  basis: string;
}
export interface InventoryVocabularyResponse {
  valuation_methods: ValuationMethodOption[];
  /** The method an item gets when none is asked for. */
  default: string;
}

export interface SetValuationMethodRequest {
  valuation_method: string;
  created_by?: string;
}
export interface SetValuationMethodResponse {
  stock_item_id: string;
  name: string;
  /** THE STORED value, never an echo of the request. */
  valuation_method: string;
  /**
   * The server's own disclosure that the item's WHOLE history has been
   * re-valued — including periods already reported. Show it: a client that says
   * only "saved" lets an operator walk away not knowing a filed figure moved.
   */
  restates: string;
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
  /** The item's stock group (T10), carried on the flat row so an export can group by it. */
  stock_group: string;
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
  /** The item's STOCK GROUP (T10), "" for an ungrouped item. */
  stock_group: string;
  /**
   * The closing quantity RESTATED in the unit's base unit when the item's unit
   * is COMPOUND: an item in "BOX of 12 Nos" closing at 4 BOX reads base_qty
   * "48.0000" in base_unit "Nos".
   *
   * ⛔ BOTH ARE "" FOR A SIMPLE UNIT — render NOTHING, never "0.0000": a unit
   * with no base restates into nothing, and a zero would read as a claim about
   * the goods. And `closing_qty` is the STORED figure: nothing converts a
   * quantity on the way in, so a report that showed base_qty as the quantity
   * would disagree with the invoice about what was keyed.
   */
  base_unit: string;
  base_qty: Money;
  /**
   * The BASIS every figure on this line was derived on — "WEIGHTED_AVG",
   * "FIFO" or "LIFO". It is on the ITEM and not on a position because that is
   * where stock_item.valuation_method lives: one item is valued one way in
   * every godown it sits in.
   *
   * ⛔ A SCREEN THAT PRINTS THESE VALUES MUST PRINT THIS TOO. One company can
   * mix all three, so two item totals on the same page can mean different
   * things; without the basis beside them neither can be reproduced or compared.
   */
  valuation_method: string;
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

/**
 * One STOCK GROUP's subtotal on the Stock Summary (T10) — the whole BRANCH: a
 * group's own items plus every item in every group beneath it.
 *
 * ⛔ THESE DO NOT SUM TO `total_value`, AND ADDING THEM UP IS A BUG. An item
 * counts in every ancestor of its group, which is what makes a branch total worth
 * reading. The company's closing stock is `total_value`, which comes off the
 * POSITIONS.
 *
 * `group` is "" for the UNGROUPED bucket, which is a real bucket holding every
 * item that predates T10 — label it, never skip it.
 */
export interface StockGroupSubtotal {
  group: string;
  depth: number;
  item_count: number;
  opening_value: Money;
  inward_value: Money;
  outward_value: Money;
  closing_value: Money;
}

export interface StockSummaryResponse {
  /** The as-at date the replay stopped at; "" when unbounded. */
  as_on: string;
  /** Group subtotals in TREE ORDER, ungrouped last. See StockGroupSubtotal. */
  groups: StockGroupSubtotal[];
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
  /**
   * The party ledger's uuid. Present so a bill-wise settlement picker can list
   * THIS party's open bills without matching on the display name — which would
   * work only by accident of name uniqueness and would fail silently on a rename.
   */
  ledger_id: string;
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
  /**
   * The date the ROWS are limited to, or `""` when they are not limited at all.
   *
   * ⛔⛔ EMPTY IS AN ANSWER, NOT A MISSING FIELD (D86), and it is the same
   * convention `/stock-summary` already uses. The server used to echo the
   * RESOLVED date here — today — on a report whose rows were no longer limited to
   * today, so the screen's "As on <date>" caption was present, well-formed and
   * false. Read it with `??`, never `||`: a bounded report's date and an
   * unbounded report's `""` are two different answers, and `||` collapses them.
   */
  as_on: string;
  /**
   * The day every bill's ageing is measured against. ALWAYS populated, including
   * on an unbounded read — the slabs mean nothing without it. ⚠️ It is NOT a claim
   * about which bills are listed; `as_on` is.
   */
  aged_as_on: string;
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

/**
 * ONE PARTY's open bills, for the bill-wise settlement picker (D97).
 *
 * ⛔⛔ IT IS A DIFFERENT SHAPE FROM `OutstandingsResponse` AND THE DIFFERENCE IS
 * THE POINT. No slab totals, no grand total, no other party. The picker used to
 * fetch the whole ageing report and filter it client-side to one `ledger_id`, so
 * to render one supplier's list it received every party's bills and the ageing
 * summary — and it fetched them from `/outstandings?kind=PAYABLE`, the
 * RECEIVABLES report's door, which is how a level refused
 * `report:bills-payable` could still read every figure in the payables book.
 *
 * ⚠️ `found` DISTINGUISHES "no such party in this ageing" FROM "this party has
 * nothing open", which an empty `bills` array cannot. Both render as an empty
 * picker, but only one of them is a bug worth seeing in a drive.
 */
export interface PartyOpenBillsResponse {
  kind: OutstandingKind;
  ledger_id: string;
  ledger: string;
  group: string;
  pending: Money;
  bills: OutstandingBill[];
  found: boolean;
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
  vouchers_altered: number;
  vouchers_deleted: number;
  vouchers_skipped: number;
  /**
   * D101 — MONEY THE SOURCE BOOKS EXCLUDE. Vouchers marked ISCANCELLED or
   * ISOPTIONAL in Tally, which Tally itself keeps out of the books, and which
   * this importer therefore does NOT post.
   *
   * ⛔ THIS IS NOT `vouchers_skipped`. That figure means "already imported under
   * this GUID". Rendering the two together would tell an operator their voided
   * sales were duplicates.
   */
  vouchers_voided: number;
  /** One line per excluded voucher: ordinal, type, number, date and reason. */
  voided_vouchers: string[];
  /** LEDGER masters marked ISDELETED in the export, and so not created. */
  ledgers_tombstoned: string[];
  /** Account groups CREATED from the export's GROUP masters (Tally's own built-ins resolve instead). */
  groups_ensured: string[];
  /** Every ledger name the import touched. It is a list of NAMES, not a count. */
  ledgers_ensured: string[];
  /**
   * What the import carried on past rather than aborting over — an opening
   * bill-wise breakup that does not sum to its own balance, a ledger whose Tally
   * group was not in the file. An empty array is the only clean import; a
   * non-empty one MUST be shown to the operator, because the totals will look
   * right either way.
   */
  warnings: string[];
  posted: ImportedVoucher[];
  /**
   * THE GOODS — the number of stock MOVEMENTS the import wrote across every
   * voucher, and the number of OPENING POSITIONS it wrote from STOCKITEM
   * masters.
   *
   * ⛔ THESE ARE THE ONLY FIGURES IN THIS RESPONSE THAT CAN TELL AN OPERATOR
   * WHETHER THEIR STOCK ARRIVED. Quantity is invisible in a trial balance, so an
   * import that reads every stock line and drops it answers 200 with a report
   * that looks complete — books that balance, and a Stock Summary with nothing
   * in it. `stock_lines: 0` on a file full of invoices is the tell.
   *
   * ⚠️ `opening_positions: 0` alongside a non-zero `stock_lines` is a WARNING and
   * not a clean result: the export carried no opening stock (a Day Book does
   * not), so every position starts at nil and the first issue of older goods was
   * either refused or consumed nothing at all.
   */
  stock_lines: number;
  opening_positions: number;
  /** The inventory masters CREATED (not merely referenced), sorted and unique. */
  stock_items_ensured: string[];
  units_ensured: string[];
  godowns_ensured: string[];
  /** "ITEM/BATCH", because a batch name is scoped to its item. */
  batches_ensured: string[];
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

// ---------------------------------------------------------------------------
// THE TDS CHALLAN (T6, migration 0069).
//   GET  /v1/companies/{id}/tds-challans?tax_year_start=2026
//   POST /v1/companies/{id}/tds-challans
//
// The deposit slip: what was paid to the Government, when, through which bank
// branch, and — the half that makes a return buildable at all — WHICH DEDUCTIONS
// IT PAID. Deductee Detail field 5 of the prescribed return is a challan
// reference, so a deduction with no allocation cannot be filed.
//
// Denied to the seeded Data Entry level BY DESIGN (`tds-challan` is a bare
// resource token and that level's default_access is 'none'), so a 403 on either
// route is a NORMAL answer and must read as the decision it is.
// ---------------------------------------------------------------------------

/**
 * One Annexure 7 minor head, SERVED by the GET.
 *
 * ⛔ NEVER hard-code the three codes in TypeScript. The list is migration 0069's
 * CHECK and the server hands it over with its descriptions precisely so no copy
 * exists here to drift from it — a client copy could offer a fourth value that
 * fails only on save.
 */
export interface MinorHead {
  /** "200", "400" or "100" — what is stored. */
  code: string;
  /**
   * What the code MEANS. Rendered beside it because the operator has to choose
   * one and there is NO DEFAULT; a picker of three bare numbers invites
   * whichever is listed first.
   */
  description: string;
}

/**
 * One allocation read back, with the LIVE deduction beside it so a screen can
 * show an allocation the books have outgrown.
 */
export interface TDSChallanAllocation {
  /** The deduction's NATURAL key is (voucher_id, section, leg_seq). */
  voucher_id: string;
  voucher_no: string;
  section: string;
  /** 0-BASED. Leg 0 is a real leg. */
  leg_seq: number;
  /**
   * Which side of the statute this line pays: a DEDUCTION (migration 0050) or a
   * COLLECTION (migration 0067). It changes the caption, not the arithmetic — on
   * a "TCS" line `deductee_name` is the BUYER the tax was collected FROM, and
   * calling them a deductee names the wrong side of the transaction.
   *
   * ⚠️ OPTIONAL ONLY BECAUSE OF ITS AGE, NOT BECAUSE THE SERVER MAY OMIT IT: the
   * real server always sends it, and an ABSENT value means "TDS" — the meaning
   * every allocation recorded before the collection side existed carries.
   */
  kind?: TaxAllocationKind;
  /** Empty when `deduction_on_record` is false — the deduction is gone, so there is nobody to name. */
  deductee_name: string;
  allocated_rupees: Money;
  /**
   * False means an ALTER removed the leg. The allocation SURVIVES that
   * (migration 0069 carries no foreign key to tds_deduction, by design) and is
   * shown rather than silently dropped: it records a real payment.
   */
  deduction_on_record: boolean;
  deduction_tax_rupees: Money;
  /**
   * ⚠️ ADVISORY ONLY, and sent so the operator meets it before filing day.
   * `reporting.Form140StatementFor` applies the same test and REFUSES the whole
   * quarter (ErrAllocationBrokenByAlter); that refusal remains the authority. A
   * screen must not present a false flag here as "the return is fine".
   */
  broken_by_alter: boolean;
}

/** One challan read back, with what it paid for. */
export interface TDSChallan {
  id: string;
  /**
   * The seven-digit code of the bank branch that received the deposit (or, for a
   * book-entry deposit, the seven-digit Form 24G receipt number).
   *
   * ⚠️ TEXT, AND LEADING ZEROS ARE PART OF IT: 0004329 is a valid code and 4329
   * is a different branch. A client that ever holds it in a number type has
   * already lost the information.
   */
  bsr_code: string;
  /** yyyy-mm-dd: the date the tax was PAID TO THE GOVERNMENT. Not the voucher date. */
  deposit_date: string;
  /** The 1-5 digit serial the bank printed on the counterfoil. TEXT: 05122 is not 5122. */
  challan_no: string;
  /** Book adjustment (a Government transfer voucher) rather than a bank challan. */
  by_book_entry: boolean;

  /**
   * The four amounts as the challan itself splits them — FOUR SEPARATE FACTS,
   * not a total with a breakdown. Only the TAX component discharges a deduction
   * liability, which is why an interest-only challan allocates to nothing.
   *
   * All four are WHOLE RUPEES (the format forbids paise in a challan box), so
   * they read "5000.0000".
   */
  tax_rupees: Money;
  interest_rupees: Money;
  fee_rupees: Money;
  penalty_rupees: Money;
  /** DERIVED from the four by the server and never stored. */
  total_deposited_rupees: Money;

  minor_head: string;
  /** Travels with the code so a row can read "400 — a demand raised on assessment". */
  minor_head_description: string;
  /** The year the DEPOSIT BELONGS TO (2026 = 2026-27). Recorded, never computed from the date. */
  tax_year_start: number;
  tax_year_label: string;

  allocation_count: number;
  allocated_total_rupees: Money;
  /**
   * ⚠️ THE ONE FIELD HERE THAT IS NOT WHOLE RUPEES, AND IT CAN BE NEGATIVE.
   * A deduction's tax is a 4-dp figure (₹33,333 at 10% is ₹3,333.30) while the
   * deposit against it must be a whole number, so a difference of a few paise
   * per allocation is the ORDINARY case and not an error. Report it; do not
   * refuse on it.
   */
  unallocated_tax_rupees: Money;

  allocations: TDSChallanAllocation[];

  recorded_by: string;
  recorded_at: string;
}

/**
 * Which side of the statute an allocation pays.
 *
 * "TDS" is tax DEDUCTED from a supplier the company paid; "TCS" is tax COLLECTED
 * from a buyer the company sold to. ⛔ The server treats an ABSENT kind as "TDS"
 * so that a client written before the collection side existed stays correct, but
 * a client that renders a picker must send it: the two are filed on separate
 * returns and may not share a challan, and a screen that captioned a collection
 * as a deduction would name the wrong party as the one taxed.
 */
export type TaxAllocationKind = "TDS" | "TCS";

/**
 * One deduction a new challan could pay for — SERVED with the register, and the
 * only reason the deposit form is completable by a human at all.
 *
 * ⛔ WHY THIS EXISTS (the blocker it fixes). An allocation is keyed on
 * (voucher_id, section, leg_seq): a UUID and a 0-BASED ORDINAL. NO SCREEN IN THIS
 * PRODUCT DISPLAYS EITHER — the TDS/TCS Register renders `voucher_no` and its
 * payload carries no leg field at all. The first deposit form therefore asked the
 * operator to TYPE two identifiers they had no way to obtain, and typing the one
 * thing they could see ("P1") earned a bare "allocation 1: invalid voucher_id".
 * It passed every test, because the tests keyed MOCK fixture ids that the real
 * server rejects as non-UUIDs.
 *
 * The fix is not a better message: it is to STOP ASKING. These rows travel to the
 * client so it can send the identifiers back, never so a human reads them.
 */
export interface AllocatableDeduction {
  /**
   * ⚠️ NOT FOR DISPLAY. A UUID on the real server; here only to be sent back
   * unchanged in {@link CreateTDSChallanAllocation.voucher_id}.
   */
  voucher_id: string;
  /** ⚠️ NOT FOR DISPLAY, and 0-BASED. Sent back unchanged; never defaulted. */
  leg_seq: number;
  /**
   * DEDUCTION or COLLECTION — sent back unchanged on the allocation, like the
   * ids, because it is how the server knows which table to verify the triple
   * against.
   *
   * ⛔ ITS ABSENCE WAS THE WHOLE TCS HALF OF THIS FEATURE (register row D4). The
   * picker offered deductions only, so a live collection under 206C(1)-SCRAP
   * appeared nowhere and a collector could never record that it had deposited
   * what it collected. A screen must label the two differently: on a "TCS" row
   * `deductee_name` is the BUYER.
   *
   * ⚠️ OPTIONAL ONLY BECAUSE OF ITS AGE — the real server always sends it, and an
   * absent value means "TDS". A screen may therefore read `d.kind ?? "TDS"`, but
   * it must SEND back what it was served rather than that default.
   */
  kind?: TaxAllocationKind;
  /** What a human actually recognises. */
  voucher_no: string;
  deductee_name: string;
  section: string;
  /**
   * yyyy-mm-dd — the date the liability arose: the DEDUCTION date, or on a "TCS"
   * row the COLLECTION date. Never the date of any deposit.
   */
  deduction_date: string;
  /**
   * What was deducted, and therefore EXACTLY what may be allocated: the write
   * path refuses an amount that is not equal to it. So the amount is not an input
   * either — a screen sends this figure back verbatim. 4-dp: ₹33,333 at 10% is
   * "3333.3000".
   */
  tax_rupees: Money;
  /**
   * A challan already covers this leg. SERVED RATHER THAN FILTERED, on purpose:
   * an operator who cannot find a deduction has to be able to tell "not deducted
   * in this year" from "already deposited". A screen must show such a row and
   * must NOT let it be selected — the write would be a 409.
   */
  already_paid: boolean;
}

export interface TDSChallanListResponse {
  tax_year_start: number;
  tax_year_label: string;
  /** Ordered by (deposit_date, challan_no) — the server's order, not the client's. */
  challans: TDSChallan[];
  /** The vocabulary, SERVED. See {@link MinorHead}. */
  minor_heads: MinorHead[];
  /**
   * What a new challan could pay for, in (deduction_date, voucher_no, leg_seq)
   * order. ZERO-TAX DEDUCTIONS ARE ALREADY EXCLUDED SERVER-SIDE — nothing can be
   * allocated to a sub-threshold payment, and offering one would invite a refusal
   * for a thing that is not an error. An EMPTY array is a statement in itself: no
   * deduction of this tax year carries tax, so there is nothing a challan could
   * pay for. See {@link AllocatableDeduction}.
   */
  allocatable_deductions: AllocatableDeduction[];
  /**
   * Present ONLY when that read failed. It does not fail the request: the
   * register is still worth showing to an operator who came to look at recorded
   * deposits, so a screen says the picker is unavailable AND WHY, and renders the
   * register anyway.
   */
  allocatable_error?: string;
}

/** One deduction this deposit paid. */
export interface CreateTDSChallanAllocation {
  /** tds_deduction's NATURAL key — never a surrogate id, which an ALTER regenerates. */
  voucher_id: string;
  section: string;
  /**
   * ⚠️ REQUIRED AND 0-BASED, AND THE SERVER DOES NOT DEFAULT IT. Leg 0 is a real
   * leg, so an absent field cannot mean "the first one": it is refused by name.
   * Never send `undefined` to make the type quiet.
   */
  leg_seq: number;
  /**
   * Which table the triple names. OPTIONAL, and an absent value means "TDS" — so
   * every caller written before the collection side existed stays correct. Send
   * the `kind` the picker served rather than omitting it: a wrong kind cannot
   * match the other side's row (the two section vocabularies are disjoint) and
   * is refused by name, but an omitted one on a collection is refused as "nothing
   * was deducted on that voucher", which reads like the books are wrong.
   */
  kind?: TaxAllocationKind;
  /**
   * The tax the deduction shows, EXACTLY. The server refuses any inequality,
   * because the stored amount is what makes a later voucher ALTER detectable —
   * a deliberately partial allocation would surface months later as a refusal of
   * the entire quarter, blaming a change nobody made.
   *
   * ⚠️ AND THE SUM OF A CHALLAN'S ALLOCATIONS MAY NOT EXCEED ITS `tax_rupees` by
   * a rupee or more per line (register row D2): a ₹1 deposit was once accepted as
   * having paid ₹27,000 across two deductees, which marked both as deposited and
   * made the real deposit unallocatable. Under-allocation is still fine — that is
   * what {@link AllocateToChallanRequest} is for.
   */
  amount_rupees: Money;
}

export interface CreateTDSChallanRequest {
  bsr_code: string;
  deposit_date: string;
  challan_no: string;
  by_book_entry: boolean;
  /**
   * Whole rupees as a fixed-point STRING. Positive tax REQUIRES at least one
   * allocation; zero tax FORBIDS them (only tax discharges a deduction).
   */
  tax_rupees: Money;
  /**
   * The other three boxes. Omitted or "" is ZERO here — on a challan a blank box
   * and a zero in it are the same statement. (That is the opposite call from
   * /deductor-status's turnover, where "" means "the test does not apply".)
   * A challan on which all four are zero is REFUSED: a nil challan is derived by
   * the return emitter and never stored.
   */
  interest_rupees?: Money;
  fee_rupees?: Money;
  penalty_rupees?: Money;
  /** MANDATORY, with NO DEFAULT. See {@link MinorHead}. */
  minor_head: string;
  /**
   * ASKED FOR, never computed from `deposit_date`: a challan paid 7 April 2026 is
   * normally for March of 2025-26 and the date alone cannot tell you which.
   */
  tax_year_start: number;
  allocations: CreateTDSChallanAllocation[];
  recorded_by: string;
}

export interface CreateTDSChallanResponse {
  id: string;
  tax_year_start: number;
  tax_year_label: string;
  /**
   * The challan as stored, read back so the operator meets
   * `unallocated_tax_rupees` at the moment they record the deposit rather than
   * on filing day.
   *
   * ⚠️ NULL DOES NOT MEAN THE WRITE FAILED. The challan is committed before the
   * read-back runs, so a failed read is reported in `read_back_error` and the
   * status stays 201. A screen that treats null as failure will invite the
   * operator to key the deposit again, and the CIN conflict that follows reads
   * like a bug.
   */
  challan: TDSChallan | null;
  read_back_error?: string;
}

/**
 * ONE STATUTORY SECTION, as the server describes it — GET
 * `/v1/companies/{id}/tds-sections`.
 *
 * ⛔ THE LIST IS SERVED SO NO CLIENT COPIES THE FINANCE ACT. Register row D3 is
 * that a deduction cannot be keyed at all; the form that fixes it has to offer
 * the sections, and a TypeScript array of "194C", "194J-P", … would be a second
 * copy of a table that changes with every Finance Act — the drift bug the challan
 * screen's rule (3) records for three minor heads, on a list with rates attached.
 *
 * Rates and the threshold are STRINGS at the engine's own precision: a JSON
 * number is a float64.
 */
export interface TaxSectionDoc {
  /** "194C", "194J-P", "206C(1)-SCRAP". */
  section: string;
  /** Only 194C splits by deductee type in the shipped ruleset; both always carry. */
  rate_ind_huf_pct: string;
  rate_other_pct: string;
  /**
   * The limit the amount must EXCEED — an amount exactly equal to it attracts
   * NO deduction ("exceeding" in the statute).
   */
  threshold_rupees: Money;
  /** "PER_TXN" | "ANNUAL" | "MONTHLY" | "NIL". See the disclosure below. */
  basis: string;
}

/**
 * The statutory vocabulary a voucher's `tds` / `tcs` block may name.
 *
 * ⛔ THE TWO NO-PAN RULES ARE DIFFERENT SHAPES, and a caller that assumed
 * otherwise would print 20% where the answer is 5%:
 *   deduction — max(rate, `tds_no_pan_floor_pct`)
 *   collection — min(max(`tcs_no_pan_multiplier` × rate, `tcs_no_pan_floor_pct`),
 *                    `tcs_no_pan_ceiling_pct`)
 *
 * ⚠️ `threshold_basis_is_approximated` IS NOT DECORATION. v1 applies EVERY basis
 * per transaction, so an ANNUAL or MONTHLY limit under-deducts when a payment is
 * split. A screen showing a threshold must not present it as the law.
 */
export interface TaxSectionsResponse {
  tds_ruleset_version: string;
  tcs_ruleset_version: string;
  tds_sections: TaxSectionDoc[];
  tcs_sections: TaxSectionDoc[];
  tds_no_pan_floor_pct: string;
  tcs_no_pan_multiplier: string;
  tcs_no_pan_floor_pct: string;
  tcs_no_pan_ceiling_pct: string;
  threshold_basis_is_approximated: boolean;
}

/**
 * ATTACH FURTHER DEDUCTIONS TO A DEPOSIT THAT IS ALREADY RECORDED.
 *
 *   POST /v1/companies/{id}/tds-challans/{challanID}/allocations
 *
 * ⛔ THIS ROUTE EXISTS BECAUSE ITS ABSENCE WAS A TRAP AN OPERATOR COULD NOT GET
 * OUT OF (register row D1). A challan may be recorded naming fewer deductions
 * than it actually paid for — nothing requires the allocations to cover the tax —
 * and until this route there was NO way to attach the rest: the create is the
 * only writer and the same CIN cannot be entered twice. The audit tried seven
 * doors in the browser and six HTTP methods, and challan 05130's unallocated
 * balance stayed at exactly 12000.0000 while the return refused the unattached
 * deduction as never deposited.
 *
 * ⚠️ IT IS AN ADD, NOT AN EDIT. It cannot move an allocation off another challan
 * (that is a 409 naming the challan that has it), cannot change an amount and
 * cannot remove anything.
 *
 * ⚠️ AND IT CAN BE REFUSED BY THE BACK-DATED CUTOFF WITH NO DATE IN THE BODY.
 * The act adopts the challan's own `deposit_date`, so a deposit made inside a
 * period an Owner has locked cannot have deductions attached to it afterwards —
 * the refusal names "deposit date" so the sentence is about a field the operator
 * can see, even though they typed none.
 */
export interface AllocateToChallanRequest {
  /**
   * The lines to add, taken from
   * {@link TDSChallanListResponse.allocatable_deductions} exactly as the create
   * takes them. An EMPTY array is refused rather than treated as a no-op: it
   * means the operator ticked nothing, and answering "done" would tell them the
   * deposit now covers deductions it does not.
   */
  allocations: CreateTDSChallanAllocation[];
  recorded_by: string;
}

/**
 * The challan after the allocations were attached.
 *
 * ⛔ READ `challan.unallocated_tax_rupees` AND SHOW IT. It is the whole oracle of
 * this route — the number that could never fall — and a screen that reports
 * success from the status code alone would look identical whether the balance
 * moved or not.
 */
export interface AllocateToChallanResponse {
  id: string;
  tax_year_start: number;
  tax_year_label: string;
  /** Null ONLY when the read-back failed; the allocations are committed either way. */
  challan: TDSChallan | null;
  read_back_error?: string;
}

// ---------------------------------------------------------------------------
// THE TDS DEDUCTOR RETURN PROFILE (T6, migration 0070).
//   GET /v1/companies/{id}/tds-profile
//   PUT /v1/companies/{id}/tds-profile
//
// The deductor block of the return. The sourcing layer refuses a return with
// ErrNoDeductorProfile before it looks at anything else, so this is what makes a
// return buildable at all. The row carries A NAMED INDIVIDUAL'S PAN — Data Entry
// is denied both halves by design, so a 403 here is a normal answer.
// ---------------------------------------------------------------------------

/**
 * One statutory address block as the RETURN numbers it: five separately numbered
 * lines, a country/region, a two-digit Annexure 1 CODE and a pincode.
 *
 * It is not `company.address` and it is not a ledger's address — splitting a
 * free-text blob into numbered lines is a guess, and a guess that truncates a
 * deductor's address on a statutory filing.
 */
export interface TDSAddressBody {
  /** Mandatory — the flat/door/block number. */
  line1: string;
  line2?: string;
  line3?: string;
  line4?: string;
  line5?: string;
  /** Mandatory. The column's 'India' DEFAULT is unreachable on this path (whole-row upsert). */
  country: string;
  /**
   * ⚠️ THE TWO-DIGIT ANNEXURE 1 CODE, NEVER A STATE NAME.
   *
   * And the route checks the SHAPE ONLY: the list of 37 codes lives in
   * `internal/compliance/form140`, which neither masters nor server may import.
   * So a screen MUST NOT tell an operator a state code is valid — "saved" is all
   * this route earns. Whether a return can be built is what the TDS register's
   * `return_file` status answers.
   */
  state_code: string;
  pincode: string;
}

/**
 * The person named on the return as answerable for the deduction. Their PAN is
 * their OWN — a different field from the deductor's (BH field 59 against 15),
 * and the PANNOTREQD literal is NOT accepted for it.
 */
export interface TDSResponsiblePerson {
  name: string;
  designation: string;
  pan: string;
  address: TDSAddressBody;
  /** May be empty IF the deductor's is present: the format makes each conditional on the other. */
  email?: string;
  /** This format version SPLITS the calling code from the number; both are mandatory. */
  contact_country_code: string;
  contact_number: string;
}

/**
 * The whole profile row, in and out.
 *
 * THERE IS NOT ONE MONEY FIELD IN IT — the deductor block is identity, address
 * and contact — so nothing here is a number at all.
 */
export interface TDSProfile {
  /** AAAA99999A. It is NOT the GSTIN and is not derivable from it. Stored upper-cased. */
  tan: string;
  /** The DEDUCTOR's own PAN, or the literal served as `pan_not_required_literal`. */
  pan: string;
  /** The name held against the TAN, which must match the .csi file from TIN — a different fact from the company's display name. */
  deductor_name: string;
  /** An Annexure 4 code. NOT the s.402(11) entity category on /deductor-status. */
  deductor_category: string;
  address: TDSAddressBody;
  email?: string;
  contact_country_code: string;
  contact_number: string;
  /** Annexure 5 code — MANDATORY for the categories flagged `state_name_code_required` and FORBIDDEN for every other. Clear it when the category changes. */
  state_name_code?: string;
  /** Annexure 3 code. Mandatory / allowed / forbidden per the category's flags. */
  ministry_code?: string;
  /** Names the ministry EXACTLY when `ministry_code` is "99" ("Other") — an equivalence, both ways. */
  ministry_other?: string;
  /** Account Office Identification Number — Government deductors paying by book entry only. */
  ain?: string;
  /** Optional even though the company holds one: the return's copy is the one that was filed. */
  gstin?: string;
  responsible: TDSResponsiblePerson;
  /** Required on the way in, echoed on the way out. */
  recorded_by: string;
  /** Set by the database; IGNORED on input. */
  recorded_at?: string;
}

/**
 * One Annexure 4 deductor category, SERVED with the two conditional rules that
 * hang off it.
 *
 * ⛔ NEVER hard-code these sixteen codes, and never re-derive the rules. A client
 * copy could offer a letter the CHECK refuses on save, or hide a field the
 * return makes mandatory and then report a Postgres constraint name to the
 * operator.
 */
export interface DeductorCategory {
  /** The single letter that goes in BH field 32. */
  code: string;
  description: string;
  /**
   * True for the State-Government categories. Means BOTH "show the Annexure 5
   * state code field" AND "for every other category, CLEAR it" — the CHECK is
   * conditional in both directions.
   */
  state_name_code_required: boolean;
  ministry_required: boolean;
  /** False means a ministry code is FORBIDDEN, not merely unnecessary. */
  ministry_allowed: boolean;
}

export interface TDSProfileResponse {
  /**
   * NULL is the ordinary state of a company that has not filled the form in —
   * and it arrives as 200, NEVER 404. The response still carries the vocabulary
   * an empty form is rendered from, which is exactly why it is not a 404.
   */
  profile: TDSProfile | null;
  /**
   * Stated rather than inferred from the null: this is the refusal the return
   * sourcing layer makes FIRST, before it looks at a challan or a deduction.
   */
  profile_recorded: boolean;
  deductor_categories: DeductorCategory[];
  /** The prescribed literal for a deductor not required to hold a PAN. Served so no client spells it itself. */
  pan_not_required_literal: string;
}

/**
 * The PUT body: THE WHOLE ROW, and PUT rather than PATCH is not stylistic.
 * Migration 0070 carries three CROSS-FIELD CHECKs (the Annexure 5 state code and
 * the Annexure 3 ministry are each mandatory for some categories and FORBIDDEN
 * for the rest), so a partial body is not checkable and can leave a stale
 * conditional value behind.
 *
 * There is no key in the path because the company IS the key.
 */
export type SetTDSProfileRequest = TDSProfile;

/**
 * The PUT answer. It carries NO vocabulary — the caller already has it from the
 * GET — and the profile is READ BACK rather than echoed, so it shows the
 * normalisation (a TAN keyed in lower case comes back in capitals) and the audit
 * timestamp.
 */
export interface SetTDSProfileResponse {
  profile: TDSProfile;
  profile_recorded: boolean;
}

// ===========================================================================
// T2 — COST CENTRES AND COST CATEGORIES
// ===========================================================================

/**
 * A cost CATEGORY is an AXIS of analysis (Department, Branch, Project); a cost
 * CENTRE is a POSITION on that axis.
 *
 * ⛔⛔ READ THIS BEFORE WRITING ANY COST SCREEN OR ANY COST TOTAL.
 *
 * Multiple categories exist in order to allocate the SAME money IN PARALLEL. One
 * ₹1,00,000 salary posting is allocated in full across Departments (Sales 60,000
 * / Admin 40,000) AND AGAIN IN FULL across Branches (Mumbai 70,000 / Delhi
 * 30,000). Both allocations are complete and both are correct — they describe the
 * same money from two directions.
 *
 * Therefore A UI THAT SUMS ACROSS CATEGORIES DOUBLE-COUNTS. There is no
 * legitimate "total allocated" figure spanning axes: for two axes it is 2× the
 * cost. Every cost figure a screen renders must be scoped to ONE category, or
 * grouped by category with no grand total. The backend's per-(posting, category)
 * constraint trigger guarantees each axis is complete; nothing guarantees — or
 * should suggest — that axes add up.
 */
export interface CostCategory {
  id: string;
  name: string;
  /**
   * Whether this axis may be used on a balance-sheet ledger as well as a P&L one
   * (Tally's "Allocate Non-Revenue Items"). Defaults false; departmental analysis
   * of revenue and expense is the ordinary case.
   *
   * It also decides the DENOMINATOR of the Cost Category Summary's coverage figure,
   * so a screen showing coverage must show this beside it.
   */
  allocate_non_revenue: boolean;
  centre_count: number;
  created_by: string;
  created_at: string;
}

export interface CostCentre {
  id: string;
  name: string;
  cost_category_id: string;
  /**
   * The axis's name, on EVERY row on purpose: a list of centre names spanning axes
   * is ambiguous — "Mumbai" may legitimately be both a Branch and a Project — and a
   * screen rendering the bare name invites an operator to pick the wrong one.
   */
  cost_category_name: string;
  /** "" at the top of the axis, and parent_name is then "" too. Never the nil uuid. */
  parent_id: string;
  parent_name: string;
  /**
   * DERIVED from the list, never stored. path is "Mumbai / Andheri", so a flat list
   * renders as a tree without the client walking parent pointers.
   *
   * ⚠️ Do NOT indent by depth in the order received: the server orders centres by
   * (axis name, centre name), not parent-before-child, so indenting the received
   * order puts children under the wrong parent. Sort by path, or group by axis and
   * build the tree from parent_id.
   */
  depth: number;
  path: string;
  /**
   * Should never be true. It says the STORED rows contain a parent cycle — which the
   * write path refuses to create and which no constraint catches beyond a one-hop
   * self-reference. A client must NOT walk path as a tree when this is set.
   */
  ancestry_truncated: boolean;
  created_by: string;
  created_at: string;
}

export interface CostCategoryListResponse {
  cost_categories: CostCategory[];
}

export interface CostCentreListResponse {
  /** Filtered to one axis when the request named one; otherwise every centre. */
  cost_centres: CostCentre[];
  /**
   * The axes, SERVED, so a create form never asks an operator to type an id.
   *
   * ⚠️ NOT A CONSISTENT SNAPSHOT with cost_centres: the server reads the two in
   * separate transactions, so an axis created between them yields centres whose
   * cost_category_id is absent from this array. Tolerate it; do not assume.
   */
  cost_categories: CostCategory[];
  /** Echoed only when the request named an axis. */
  cost_category_id?: string;
}

/**
 * The create/alter reply for a cost CENTRE.
 *
 * ⛔ THE VIEW IS NESTED, and typing this envelope AS a flat CostCentre is a lie the
 * compiler cannot catch — every field reads `undefined` at runtime while the code
 * type-checks. That shipped: the masters screen confirmed a successful create with
 * "Created undefined on the undefined axis, under undefined", with 597 frontend
 * tests green, because the MOCK returned a flat object and only the real server
 * returns the envelope.
 *
 * `cost_centre` is null when the read-back could not be performed — the row IS
 * written either way, so a caller must treat null as "created, but I cannot show you
 * what it became" and never as a failure. read_back_error says why.
 */
export interface CostCentreWriteResponse {
  id: string;
  cost_centre: CostCentre | null;
  read_back_error?: string;
}

export interface CreateCostCategoryRequest {
  name: string;
  allocate_non_revenue?: boolean;
  /**
   * ⛔ REQUIRED BY THE SERVER, and omitting it is a 400 — not a defaulted row.
   *
   * This field was missing from this type when it was first written, so the masters
   * screen had no way to know it was needed and every create came back 400 AFTER a
   * successful WebAuthn step-up. The step-up worked; the retry was malformed. The
   * only sign was a console 400, because the screen reported the failure as an
   * ordinary error.
   *
   * ⚠️ It is CLIENT-SUPPLIED, which means the audit actor is a value the client
   * chooses. The rest of this codebase does the same (`created_by: "ui"` in the bank
   * screens), so this matches the house pattern rather than fixing it — but the
   * server holds the authenticated subject and ought to be the one deciding. Noted
   * as a real gap, not endorsed.
   */
  created_by: string;
}

export interface CreateCostCentreRequest {
  /** Required. See {@link CostCentre}. */
  cost_category_id: string;
  name: string;
  parent_id?: string | null;
  /** ⛔ REQUIRED. See {@link CreateCostCategoryRequest.created_by}. */
  created_by: string;
}

/**
 * ONE allocation of ONE posting, as the voucher write path accepts it.
 *
 * ⛔⛔ `posting_seq` IS A 0-BASED ORDINAL THAT NO SCREEN DISPLAYS, AND THAT IS THE
 * TRAP THIS FEATURE IS MOST LIKELY TO SHIP.
 *
 * This is the same defect T6's challan form shipped and had to be rebuilt for:
 * it asked the operator to type a voucher UUID and a 0-based leg ordinal —
 * identifiers no screen in this product renders — and it passed every test,
 * because the tests keyed mock fixture ids the real server rejects. See
 * {@link AllocatableDeduction}, which exists solely to stop that being asked.
 *
 * The rule that follows: A HUMAN MUST NEVER TYPE OR COUNT A posting_seq. The
 * allocation UI is attached to a posting ROW the operator is already looking at
 * (a ledger name and an amount), and the client fills this field in from that
 * row's index. If a screen ever renders this number, or asks for it, the feature
 * is not built however green the suite is.
 */
export interface CostAllocationInput {
  /** ⚠️ NOT FOR DISPLAY. The index of the posting in the voucher's own postings array. */
  posting_seq: number;
  /** The AXIS. Required — an allocation belongs to both a centre and its category. */
  cost_category_id: string;
  cost_centre_id: string;
  /**
   * fixed-4 string. ⛔ The allocations for ONE (posting, category) must total the
   * posting's amount EXACTLY, to the paisa. There is no tolerance: the server
   * refuses, and the DB's deferred constraint trigger refuses behind it. A screen
   * should show the running remainder so the operator can see the gap close —
   * that remainder is the single most useful number on an allocation form.
   */
  amount: Money;
}

/**
 * One centre in the Cost Centre Summary tree.
 *
 * NESTED, not a flat list with a depth column. The roll-up IS the report, and a
 * flat list leaves the client to re-derive which figure contains which — the one
 * derivation most likely to be got wrong twice. (An earlier draft of these types
 * assumed flat+depth; the server does not order rows parent-before-child, so
 * indenting a flat list by depth in the order received renders children under the
 * wrong parent.)
 */
export interface CostCentreNode {
  cost_centre_id: string;
  name: string;
  /** "" for a top-level centre — never the nil uuid spelled out. */
  parent_id: string;
  depth: number;
  /**
   * OWN and TOTAL are both served and NEITHER is derivable from the other: a
   * parent may hold allocations of its own AND have children. Only the pair tells
   * an operator whether "Sales 60,000" was keyed against Sales itself or against
   * its sub-teams. Rendering only total is the common mistake.
   */
  own_debit: Money;
  own_credit: Money;
  own_net: Money;
  total_debit: Money;
  total_credit: Money;
  total_net: Money;
  children: CostCentreNode[];
}

/**
 * THE COST CENTRE SUMMARY — for ONE axis.
 *
 * It takes a cost category and has no "all centres" mode, for the reason
 * {@link CostCategory} gives: centres from different axes describe the same money,
 * so summing them double-counts. `total_*` here is the total for THIS axis and is
 * therefore meaningful; it must never be added to another axis's.
 */
export interface CostCentreSummaryResponse {
  /** Echoed so a screen cannot render one axis's numbers under another's heading. */
  cost_category_id: string;
  cost_category: string;
  /** The axis's own rule — what kind of ledger it may carry. */
  allocate_non_revenue: boolean;
  from: string;
  to: string;
  centres: CostCentreNode[];
  /**
   * Centres the roll-up could not place: a parent chain that loops, or one deeper
   * than the walk's bound, or a parent outside the fact set.
   *
   * ⛔ A FIELD, NOT AN OMISSION, and a screen MUST render it when non-empty. A cost
   * centre that silently vanished from a cost report would take its money with it,
   * and the totals would still look internally consistent.
   */
  unattached: CostCentreNode[];
  total_debit: Money;
  total_credit: Money;
  total_net: Money;
  unattached_debit: Money;
  unattached_credit: Money;
  /** The flat sum of every centre's own figures — the report's independent oracle. */
  allocated_debit: Money;
  allocated_credit: Money;
  /** The residual between the oracle and the roll-up. Published so it cannot be hidden. */
  difference_debit: Money;
  difference_credit: Money;
  reconciles: boolean;
  /**
   * Caveats that travel WITH the numbers, including the one saying this total must
   * never be added to another axis's. A screen may style them; it may not decide
   * whether to show them.
   */
  warnings: string[];
}

/** One axis's coverage of the period's allocable cost. */
export interface CostCategorySummaryRow {
  cost_category_id: string;
  cost_category: string;
  allocate_non_revenue: boolean;
  /**
   * THE DENOMINATOR — which postings this axis was measured against, which depends
   * on allocate_non_revenue. A coverage figure whose denominator is unstated is a
   * percentage of nothing in particular, so it is served beside the numerator.
   */
  allocable_debit: Money;
  allocable_credit: Money;
  allocable_postings: number;
  allocated_debit: Money;
  allocated_credit: Money;
  allocated_postings: number;
  /**
   * THE GAP — cost that is in the Profit & Loss and attached to nothing on this
   * axis.
   *
   * ⛔ THIS IS WHAT THE REPORT IS FOR, not a footnote. No database constraint can
   * see it: the sum trigger fires on allocations, and where there are none there is
   * nothing to fire on. So an axis can be perfectly consistent and still describe a
   * fraction of the business. A screen rendering allocated_* without this beside it
   * answers a question nobody asked.
   */
  unallocated_debit: Money;
  unallocated_credit: Money;
  unallocated_postings: number;
  fully_allocated: boolean;
  /**
   * Two faults that should be impossible and are reported rather than absorbed: a
   * part-allocated posting (the deferred trigger forbids it, so a non-zero here
   * means that guarantee is gone) and an allocation on a ledger this axis may not
   * carry (the database does not enforce allocate_non_revenue; only this report
   * sees it). Both are non-zero only when something is wrong — render them loudly.
   */
  partly_allocated_postings: number;
  partly_allocated_shortfall: Money;
  off_axis_debit: Money;
  off_axis_credit: Money;
  off_axis_postings: number;
  difference_debit: Money;
  difference_credit: Money;
  reconciles: boolean;
  notes: string[];
}

export interface CostCategorySummaryResponse {
  from: string;
  to: string;
  /**
   * ⛔ `rows` AND NO TOTAL, and none may be added. Each row is a different axis
   * through the SAME money, so a total across them is a multiple of the cost
   * actually borne. `notes` carries that statement onto the artefact itself,
   * because the absence of a field only informs a programmer.
   */
  rows: CostCategorySummaryRow[];
  reconciles: boolean;
  notes: string[];
}

// ===========================================================================
// T3 — SALES / PURCHASE ORDERS AND THE BILL OF MATERIALS (migrations 0042, 0043)
// ===========================================================================

/**
 * ⛔⛔ AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT. Read this before writing
 * any screen in this block.
 *
 * Nothing has moved when a customer orders 20 units: the goods are still on our
 * shelf, still ours, and still in closing stock at full value. Orders therefore
 * live in their own tables and affect REPORTS (the order book) and never
 * BALANCES — no stock movement, no posting, no effect on the trial balance,
 * the P&L or the balance sheet.
 *
 * A UI consequence that is easy to miss: an order screen must NOT show or imply a
 * stock effect, and an order must never appear in the Day Book beside vouchers.
 * It is a promise, not an entry.
 */
export type OrderDirection = "SALES" | "PURCHASE";

/**
 * One committed item quantity on an order.
 *
 * ⛔ NO `line_no`. The SERVER assigns it from the position of this entry in the
 * `lines` array (the first line is 1), exactly as it does for a voucher's inventory
 * grid. A client-supplied line number would be a second source of truth for the
 * same ordinal, and the two would part company the first time a form let an
 * operator delete a middle line. It comes BACK on {@link OrderLineView}, which is
 * where a fulfilment or a printed order refers to it.
 */
export interface OrderLineInput {
  stock_item_id: string;
  /** fixed-4. The COMMITTED quantity — always positive; the order's direction carries the sense. */
  qty: Money;
  /**
   * The agreed rate. An INPUT to the order book's value column and never a
   * valuation: the invoice that fulfils this line may lawfully bill a different
   * rate (a price revision agreed later), and that difference is a finding to
   * show — not a disagreement to resolve by overwriting either figure.
   */
  rate?: Money;
  /**
   * ISO date, or omitted. ⚠️ OMITTED MEANS "NO DATE AGREED", WHICH IS NOT
   * OVERDUE. A screen that renders a missing due date as today's date, or sorts
   * it as if it were, invents an overdue commitment.
   */
  due_date?: string;
}

export interface CreateOrderRequest {
  /**
   * ⛔ THERE IS NO `direction` FIELD, AND ITS ABSENCE IS LOAD-BEARING. The ROUTE
   * carries the direction (`/sales-orders` vs `/purchase-orders`), because the
   * server's PDP reads the PATH to decide whether this request is `sales-order` or
   * `purchase-order` authority. A body field would let a caller authorised only for
   * sales orders POST a purchase commitment to the sales route and have it
   * accepted — the PDP would have said yes to the wrong object.
   *
   * Pick the method, not a field: {@link CoredClient.createSalesOrder} or
   * {@link CoredClient.createPurchaseOrder}.
   */
  order_no: string;
  order_date: string;
  /** The customer (SALES) or the supplier (PURCHASE). A party LEDGER id. */
  party_ledger_id: string;
  narration?: string;
  created_by?: string;
  lines: OrderLineInput[];
}

/**
 * One order line as READ BACK, carrying its derived position.
 *
 * ⛔ `fulfilled_qty` AND `outstanding_qty` ARE DERIVED SERVER-SIDE AND ARE NOT
 * STORED ANYWHERE. There is no such column, deliberately: a stored figure goes
 * stale undetectably the first time a fulfilling voucher is ALTERED, because an
 * alter writes a NEW voucher version and re-states its links without visiting the
 * order at all. The server sums `order_fulfilment` across CURRENT, non-deleted
 * voucher versions on every read. A client must not cache these across a voucher
 * edit.
 */
export interface OrderLineView extends OrderLineInput {
  order_line_id: string;
  /**
   * 1-BASED position within the order, assigned by the server — see
   * {@link OrderLineInput}. It is what an operator reads off the printed order when
   * they ask "which line is short".
   */
  line_no: number;
  stock_item: string;
  /**
   * ⛔ REQUIRED ON THE WAY BACK, though {@link OrderLineInput.rate} is optional on
   * the way in. The server defaults an omitted rate to zero and always sends a
   * fixed-4 string, so inheriting the input's optionality would be a type that
   * LIES ABOUT THE RESPONSE — the exact shape of defect that shipped in T2, where
   * `rest.ts` typed a response envelope as the flat view it wished existed and
   * every field read `undefined` at runtime while the code type-checked.
   */
  rate: Money;
  fulfilled_qty: Money;
  /** qty - fulfilled_qty. Never negative: over-fulfilment is refused. */
  outstanding_qty: Money;
}

export interface OrderView {
  order_id: string;
  direction: OrderDirection;
  order_no: string;
  order_date: string;
  party_ledger_id: string;
  party: string;
  narration: string;
  /**
   * ⚠️ MANUAL ABANDONMENT ("the customer cancelled"), NOT A FULFILMENT STATE.
   * Nothing can compute this — an order nobody ever delivered and one that was
   * cancelled have identical fulfilment histories — which is exactly why it is
   * the one order field that IS stored. A closed order carries a reason; an open
   * one must not.
   */
  is_closed: boolean;
  closed_reason: string;
  lines: OrderLineView[];
}

/**
 * THE ORDER BOOK — outstanding commitments, for ONE direction.
 *
 * ⛔ ONE DIRECTION PER REPORT, AND THAT IS AN AUTHORISATION FACT RATHER THAN A
 * REPORTING ONE. The server serves `sales-order-book` and `purchase-order-book`
 * as two separate report names because the PDP decides on the report NAME and a
 * query parameter is invisible to it — so a single report with `?direction=` would
 * make "the salesperson may see our sales pipeline but not what we have committed
 * to pay suppliers" an unwritable rule. Do not "simplify" the two screens into one
 * with a toggle: the toggle would be reaching a resource the operator may be
 * denied.
 */
export interface OrderBookRow {
  order_id: string;
  order_no: string;
  order_date: string;
  party: string;
  order_line_id: string;
  stock_item: string;
  qty: Money;
  fulfilled_qty: Money;
  outstanding_qty: Money;
  rate: Money;
  /** outstanding_qty * rate, at the ORDER's agreed rate. */
  outstanding_value: Money;
  /** "" when no date was agreed — see {@link OrderLineInput.due_date}. */
  due_date: string;
  /**
   * True only when a due date EXISTS and has passed. A line with no agreed date
   * is never overdue, however old.
   */
  is_overdue: boolean;
}

export interface OrderBookResponse {
  direction: OrderDirection;
  as_on: string;
  rows: OrderBookRow[];
  total_outstanding_value: Money;
  notes: string[];
}

// ===========================================================================
// THE FORECAST PLANE (wave-2 WS5a) — GET /cash-flow and GET /anomalies
// ===========================================================================
//
// ⛔⛔ EVERY FIELD BELOW WAS TRANSCRIBED FROM A LIVE RESPONSE, NOT FROM THE
// HANDLER AND NOT FROM A HANDOFF. A report route's JSON shape is not guessable —
// two earlier attempts at this screen died on `'str' has no attribute 'get'` —
// so the shape was measured against a running cored (`GET /cash-flow` on a
// seeded tenant) and the handler was then read to confirm the TYPES rather than
// the other way round. Two things a reader would have got wrong:
//
//  1. `beyond_horizon` is an OBJECT, not an array. It is the tail of the
//     projection past the horizon, not another bucket.
//  2. `overdue_folded_into_first_bucket` EXISTS TWICE WITH TWO DIFFERENT TYPES —
//     a `boolean` at the top level (did the folding happen?) and a
//     {@link CashFlowBasisCount} inside `basis` (how much was folded). They are
//     not the same field and neither is redundant.
//
// And every money value is a fixed-4 STRING. `Number()` on any of them loses
// paisa; they are formatted, compared and exported as strings.

/**
 * One side-split count inside the projection's basis: how many bills got their
 * settlement date from this source, and how much money that was, per side.
 */
export interface CashFlowBasisCount {
  bills: number;
  inflow: Money;
  outflow: Money;
}

/**
 * WHERE THE PROJECTION'S DATES CAME FROM — the disclosure that makes the numbers
 * readable as an estimate instead of a balance.
 *
 * ⛔ THE THREE `assumed_settlement_date` SOURCES ARE NOT INTERCHANGEABLE AND THE
 * SCREEN MUST NOT TOTAL THEM INTO ONE FIGURE. `from_due_date` is OBSERVED — the
 * bill carried an explicit due date. `from_party_credit_days` is assumed from
 * something the tenant configured. `from_default_credit_days` is assumed from a
 * CONSTANT COMPILED INTO THIS BUILD, which no tenant ever agreed to — it is the
 * one an operator most needs to see, and it is the reason this block exists at
 * all. A single "assumed" total would hide exactly the distinction being
 * disclosed.
 */
export interface CashFlowBasis {
  what_is_projected: string;
  assumed_settlement_date: {
    from_due_date: CashFlowBasisCount;
    from_party_credit_days: CashFlowBasisCount;
    from_default_credit_days: CashFlowBasisCount;
    /** The build's convention, in days, when a bill has neither a due date nor a party credit period. */
    default_credit_days: number;
  };
  overdue_folded_into_first_bucket: CashFlowBasisCount;
  /** What the projection does NOT model. Rendered verbatim; never summarised. */
  not_modelled: string[];
}

/** One bucket of the projection. `from` is inclusive, `to` exclusive. */
export interface CashFlowBucket {
  from: string;
  to: string;
  inflow: Money;
  outflow: Money;
  nett: Money;
  opening_cash: Money;
  closing_cash: Money;
}

/**
 * A CASH-FLOW PROJECTION — `report:cash-flow` on the server.
 *
 * ⛔⛔ `estimate`, `method` AND `basis` ARE REQUIRED, NOT OPTIONAL, AND THAT IS
 * THE WHOLE POINT OF THE TYPE. This response used to carry seven keys, not one
 * of which contained the words projection, estimate, basis, assumed or method,
 * while every figure in it was a fixed-4 string that reads exactly like a posted
 * ledger balance — and the engine INVENTS a settlement date for every unpaid
 * bill. That was register defect D23, and the server half of it is closed.
 *
 * Declaring these three OPTIONAL would re-open it from the client side: a screen
 * written against `basis?` compiles perfectly while rendering nothing, and a
 * projection displayed without its basis is a guess printed as a balance. If the
 * server ever stops sending them, this type must fail to compile — which is why
 * there is no `?` anywhere in this interface.
 */
export interface CashFlowResponse {
  as_on: string;
  horizon_days: number;
  bucket_days: number;
  opening_cash: Money;
  buckets: CashFlowBucket[];
  /** The tail PAST the horizon. An OBJECT, not a bucket — it has no dates. */
  beyond_horizon: {
    inflow: Money;
    outflow: Money;
    nett: Money;
  };
  /** Did overdue bills get folded into bucket 1? A BOOLEAN here; a count in `basis`. */
  overdue_folded_into_first_bucket: boolean;
  /** Always true. A plain bool so no reader has to parse prose to find it. */
  estimate: boolean;
  method: string;
  basis: CashFlowBasis;
}

/**
 * One anomaly finding. `severity` and `kind` are the server's own vocabulary and
 * are rendered as they arrive — a client that re-ranked them would be inventing
 * an opinion the audit trail does not have.
 */
export interface AnomalyFinding {
  kind: string;
  voucher_id: string;
  voucher_no: string;
  severity: string;
  detail: string;
}

/**
 * THE ANOMALY LIST — `report:anomalies` on the server.
 *
 * ⚠️ `from`/`to` COME BACK AS THE EMPTY STRING WHEN NONE WAS SENT, and that
 * means UNBOUNDED rather than "today". The screen says so instead of
 * interpolating a blank into a heading — the same defect the order books shipped
 * and had to fix.
 *
 * ⛔ AND THE WINDOW KEYS ON `created_at`, NOT ON THE VOUCHER DATE. This is the
 * "when was it ENTERED" audit axis, which is the only axis on which "entered
 * after hours" or "backdated" means anything. A screen that labelled it with the
 * voucher period would be describing a different question.
 */
export interface AnomaliesResponse {
  from: string;
  to: string;
  findings: AnomalyFinding[];
}

/**
 * A BILL OF MATERIALS — a recipe, and a MASTER (migration 0043). It moves
 * nothing, commits nothing and balances nothing; it becomes real only when
 * EXPLODED into the inventory lines of a Stock Journal.
 *
 * ⛔⛔ `output_qty` IS THE YIELD AND THE COMPONENTS ARE STORED AGAINST IT, NEVER
 * PRE-DIVIDED PER UNIT. Recipes are stated as "10 kg of resin makes 3 mouldings".
 * A UI that helpfully normalises that to 3.3333 kg per moulding loses material at
 * fixed-4: exploding it for a batch of 3 gives 9.9999 kg, the consumption is short
 * by a tenth of a gram every batch forever, and it accumulates in closing stock as
 * phantom material that reconciles against nothing. Show a per-unit figure if it
 * helps an operator, but SEND the recipe as written and let the server scale by
 * (wanted / output_qty).
 */
export interface BomComponentInput {
  stock_item_id: string;
  /** fixed-4. Consumed to yield `output_qty` of the finished good — NOT per unit of it. */
  qty: Money;
}

/**
 * A recipe component as READ BACK, carrying the `line_no` the server assigned from
 * its position — the same asymmetry {@link OrderLineInput} records, and for the same
 * reason: a client-supplied ordinal would be a second source of truth for the
 * position, and the two part company the first time a form deletes a middle line.
 */
export interface BomComponentView extends BomComponentInput {
  line_no: number;
  stock_item: string;
}

export interface CreateBomRequest {
  /** The FINISHED GOOD. ⚠️ May not also appear as a component: the server refuses it. */
  stock_item_id: string;
  /**
   * An item may have several recipes (a standard build and a substitute build),
   * so a BOM is named and the name is what an operator picks at explosion time.
   * Unique within its item, case-insensitively.
   */
  name: string;
  output_qty: Money;
  /** At most one default per item; the server refuses a second. */
  is_default?: boolean;
  created_by?: string;
  components: BomComponentInput[];
}

export interface BomView {
  bom_id: string;
  stock_item_id: string;
  stock_item: string;
  name: string;
  output_qty: Money;
  is_default: boolean;
  components: BomComponentView[];
}

/**
 * The explosion of a BOM for a wanted quantity — what a Stock Journal's lines
 * would be.
 *
 * ⚠️ SINGLE-LEVEL. A component that itself has a BOM is NOT exploded
 * recursively: a Table explodes to 4 Legs, and if a Leg has its own recipe the
 * operator issues a second Stock Journal. That is Tally's own default and it is
 * what keeps work-in-progress visible as stock instead of vanishing inside one
 * entry. A screen must not imply the explosion is complete to the raw materials.
 */
export interface BomExplosionResponse {
  bom_id: string;
  stock_item: string;
  /** What the operator asked to make. */
  wanted_qty: Money;
  /** The recipe's own yield, echoed so a screen can show the ratio it scaled by. */
  output_qty: Money;
  components: {
    stock_item_id: string;
    stock_item: string;
    /** wanted_qty / output_qty * component qty, computed once at full precision. */
    qty: Money;
  }[];
  notes: string[];
}

// ===========================================================================
// T5 — e-WAY BILL (Rule 138)
// ===========================================================================

/**
 * ⛔⛔ NOTHING HERE IS A GOVERNMENT-ISSUED e-WAY BILL, AND A SCREEN MUST SAY SO
 * ON THE ARTEFACT'S OWN FACE.
 *
 * The NIC e-Way Bill API is unreachable from this environment, so what this
 * product generates is the PAYLOAD and a deterministic local number — never a
 * real EBN. Printing something that looks like a validated e-way bill tells a
 * driver at a checkpost that the consignment is covered when it is not, which is
 * a worse outcome than printing nothing. The notice is conditioned on the
 * artefact being the KIND of thing the gap applies to, following
 * `invQRNotPrintedNotice` in internal/printing/invoice.go.
 *
 * ⚠️ AND THE THRESHOLDS ARE SECONDARY MATERIAL. As of 2026-08-05 CBIC's own rule
 * repository fails TLS chain verification from here, so the ₹50,000 consignment
 * value, the under-50 km Part B relaxation and the one-day-per-200-km validity are
 * corroborated across secondary sources and NOT verified against the bare Rule.
 * They live in ONE Go location carrying that caveat, deliberately not in a
 * migration CHECK, so re-verification is a code change rather than a schema
 * change. A screen must not restate the numbers: read them from the server.
 */
export interface EWayBillRequest {
  voucher_id: string;
  /** The transporter's GSTIN or enrolment id. */
  transporter_id?: string;
  transporter_name?: string;
  /** Part B. Absent for a movement under the distance threshold the server states. */
  vehicle_number?: string;
  transport_mode?: "ROAD" | "RAIL" | "AIR" | "SHIP";
  /**
   * Approximate distance in km — drives the validity period the server computes.
   *
   * ⛔ OMITTING IT (or sending 0) IS A 422, NOT A DEFAULT. The server refuses to
   * compute a validity from an unstated distance rather than granting the
   * one-day minimum by accident: a bill whose expiry the system guessed is a
   * bill that expires without warning, and an expired e-way bill on a live
   * consignment is a detained vehicle. The refusal carries `blockers` and
   * `basis` so a screen can show WHY.
   */
  distance_km?: number;
  /**
   * ⛔ ASSERTED BY THE OPERATOR AND NEVER INFERRED. Nothing in a voucher says
   * whether a consignment is over-dimensional, and assuming ordinary cargo grants
   * TEN TIMES the validity the rule allows. A screen must offer this as an
   * explicit choice, never derive it.
   */
  over_dimensional_cargo?: boolean;
  /**
   * Route overrides. Omit to use the company's and the party's master addresses.
   *
   * ⚠️ THE DEFAULT IS OFTEN WRONG AND THE OVERRIDE IS WHY THESE EXIST: NIC keys
   * the route on where the goods actually start and end, and a consignment
   * routinely ships from a warehouse that is neither party's registered address.
   */
  from_pincode?: string;
  from_state_code?: string;
  to_pincode?: string;
  to_state_code?: string;
  created_by?: string;
}

export interface EWayBillResponse {
  voucher_id: string;
  voucher_no: string;
  voucher_date: string;
  /**
   * ⛔ A LOCAL, DETERMINISTIC IDENTIFIER — NOT AN EBN. Never label this "e-Way
   * Bill Number" in the UI.
   */
  local_reference: string;
  /**
   * Which adapter answered — "fake-nic-ewaybill" in this deployment. Rendered so a
   * reader learns it was a stand-in; the disclosure block names it too.
   */
  provider: string;
  /** The exact JSON that WOULD be submitted, for inspection and for a later real submission. */
  payload: Record<string, unknown>;
  valid_until: string;
  /** How many days were granted. The REASONING is in `disclosures`, not here. */
  validity_days: number;
  /**
   * ⛔ RENDER THIS. It states that no submission was made, that the thresholds
   * are unverified secondary material, and — when Part B is outstanding — that
   * the goods may not move yet. It is not decoration and it is not optional: the
   * whole disclosure discipline rests on it reaching the paper.
   *
   * ⚠️ THE VALIDITY REASONING IS IN HERE, which is why there is no separate
   * `validity_basis` field. An earlier draft of this interface had one; it was
   * removed rather than implemented, because the km-per-day rule applied and the
   * cargo class assumed are already two of the sentences in this array, and a
   * second copy would be a second thing to keep true.
   */
  disclosures: string[];
  /** Whether the consignment reached the threshold. False is legal: a bill may be raised anyway. */
  assessed_required: boolean;
  /** Whether the vehicle number is required for this movement. */
  part_b_required: boolean;
  /**
   * Statutory problems that do NOT prevent the bill being raised but DO prevent
   * the goods moving on it — in practice, a missing vehicle on a Part B movement.
   *
   * ⚠️ A NON-EMPTY `blockers` ON A RAISED BILL IS THE PART-A SLIP CASE and is
   * legitimate: an operator raises Part A while the vehicle is still being
   * arranged. Render it as a warning, not an error. It is also inside
   * `disclosures`, so it reaches paper either way.
   */
  blockers: string[];

  transporter_id: string;
  transporter_name: string;
  vehicle_number: string;
  transport_mode: string;
  distance_km: number;
  over_dimensional_cargo: boolean;
  from_pincode: string;
  to_pincode: string;
  from_state_code: string;
  to_state_code: string;

  /** 1 on the first raise. Above 1 means an earlier generation was replaced. */
  generation_count: number;
  generated_at: string;
  /**
   * ⛔ THE INVOICE HAS BEEN ALTERED SINCE THIS BILL WAS RAISED, so the stored
   * payload no longer describes the document it names. Derived on every read from
   * the voucher's current version, never stored.
   *
   * A screen MUST surface this. Nothing else in the system will ever say it, and
   * the failure mode is a driver carrying a sheet that describes different goods.
   */
  stale: boolean;
}

/**
 * THE e-INVOICE / IRN (§1.4 B). cored PROXIES to statutoryd.
 *
 * ===========================================================================
 * ⛔ THE REQUEST CARRIES ONLY THE VOUCHER, AND THAT IS THE WHOLE POINT
 * ===========================================================================
 * statutoryd's OWN request demands seller and buyer GSTIN, legal name and state
 * code plus the entire NIC item list, because statutoryd cannot read cored's
 * masters. cored CAN — every one of those fields is derived from the `company`
 * row, the party `ledger` row and the voucher's own GST and stock lines.
 *
 * So a browser must NOT be given fields for them. A browser that had to supply
 * them would be re-typing the books, and a browser that mistyped one would put a
 * wrong GSTIN on a document carrying a government reference number — which is
 * then FROZEN and cannot be corrected. Compare {@link EWayBillRequest}, which
 * legitimately carries seven operator-stated fields because they are facts about
 * the world (which lorry, how far) that no ledger holds. An e-invoice has none:
 * it is entirely a restatement of the invoice.
 */
export interface EInvoiceRaiseRequest {
  voucher_id: string;
}

/**
 * A raised e-invoice, as the RAISE answers it.
 *
 * ⚠️ THIS IS THE ONLY PLACE `ack_no` EVER EXISTS — see {@link EInvoicePinned}.
 */
export interface EInvoiceRaised {
  voucher_id: string;
  voucher_no: string;

  /**
   * ⛔ NOT A GOVERNMENT-ISSUED IRN IN THIS DEPLOYMENT, and a screen must say so
   * on the artefact's own face. No IRP credentials exist here (HANDOFF §10
   * item 4), so `FakeProvider` mints this from a hash of the payload. A real
   * IRP derives IRN = hash(SupplierGSTIN, DocType, DocNo, FY) and REJECTS a
   * duplicate; the fake hashes the whole payload and cannot, which is why no
   * test in this stack can show a duplicate-IRN defect.
   */
  irn: string;
  /** The NIC acknowledgement number. Available on the raise and NEVER AGAIN. */
  ack_no: string;
  /** "ACT" from the fake provider. A real IRP also emits CNL for a cancelled IRN. */
  status: string;

  /**
   * ⛔ THE VOUCHER IS NOW FROZEN AND THIS IS THE NUMBER THE IRN REFERS TO.
   *
   * The freeze hook has just made this voucher un-alterable and un-deletable,
   * with NO UNDO ANYWHERE IN THE PRODUCT. A screen that does not show this
   * leaves the operator to discover the freeze by being refused an edit later,
   * with nothing connecting the refusal to this action.
   */
  pinned_voucher_no: string;
}

/**
 * An IRN read back off a voucher. 404 when none has been raised, which is the
 * NORMAL answer for almost every voucher.
 *
 * ⛔ NO `ack_no` KEY, DELIBERATELY, AND ITS ABSENCE IS THE HONEST ANSWER.
 * `voucher_external_reference` (migration 0009) has no column for the NIC
 * acknowledgement number — it holds the kind, the ref and the pinned number.
 * A key carrying "" would read as "this invoice has no ack number", which is a
 * claim about the IRP rather than about this schema. The ack number is available
 * on the RAISE and never again, so a screen that wants to show it must show it
 * then.
 */
export interface EInvoicePinned {
  voucher_id: string;
  voucher_no: string;
  irn: string;
  pinned_voucher_no: string;
  created_by: string;
  created_at: string;
}

/** The order list: heads only, WITHOUT their lines — see {@link OrderView}. */
export interface OrderListResponse {
  direction: OrderDirection;
  orders: Omit<OrderView, "lines">[];
  /**
   * Echoed so a screen cannot render a filtered list under an unfiltered heading.
   * "No orders" and "no OPEN orders" are different answers to an operator.
   */
  open_only: boolean;
}

/** The recipe list: heads only, WITHOUT their components. */
export interface BomListResponse {
  boms: Omit<BomView, "components">[];
}

/**
 * ABANDON an order, with a required reason — NOT "mark it fulfilled".
 *
 * `is_closed: false` re-opens it and clears the reason together, because the
 * server refuses a reason on an open order: a reason attached to something that
 * has not happened is a claim about nothing.
 */
export interface CloseOrderRequest {
  is_closed: boolean;
  /** Required when closing. Omit (or send empty) when re-opening. */
  reason?: string;
}

// ===========================================================================
// PAYROLL (Track 3, migrations 0034-0036 · backend/internal/payroll,
// internal/server/handler_payroll.go).
// ===========================================================================
//
// The eight routes below have been mounted, PDP-mapped and tested since Track 3
// landed, and NOTHING IN THIS APP HAS EVER CALLED ONE OF THEM. The engine, the
// statutory rates, the two reports and the three statutory FILES were all built
// server-side; `frontend/src/screens/` contained no payroll screen at all, so an
// operator could not create an employee, run a wage month, or produce an ECR
// without calling the server directly. That is the same shape of gap T4 (bank
// reconciliation) and T6 (the challan register) each had, and it is closed the
// same way: a client method per route, and a screen per authority.
//
// ⛔ EVERY MONEY FIELD ON EVERY TYPE BELOW IS A STRING, AND IT IS THE SERVER'S
// OWN `StringFixed(4)`. There is no `number` anywhere in this section and there
// must never be one: a JSON number arrives as a float64 and re-prices somebody's
// salary in the last paisa. The screens render these strings and never parse
// them; the ONE place a decimal is built is an operator's own typed input on the
// way OUT, where it travels as a string too (`monthInputReq` on the server takes
// strings for exactly this reason).

/**
 * One employee master row.
 *
 * ⚠️ THE IDENTIFIERS ARE HERE — PAN, UAN, ESIC IP number, bank IFSC — and that
 * is a deliberate server decision, not an oversight to be filtered in a screen:
 * an operator who may reach the master must be able to SEE and correct a wrong
 * PAN, because a malformed one is a Form 138 deductee record the department
 * rejects. WHO may reach it is decided by the ACL and not by this projection —
 * `employees` is served under its OWN resource token `employee` (never folded
 * into `master`, which migration 0022 grants to every seeded Data Entry level),
 * and migration 0035 disallows create/alter/display of it there by default. So a
 * 403 on the employee screen is a NORMAL answer and is rendered as the decision
 * it is.
 */
export interface EmployeeView {
  id: string;
  code: string;
  name: string;
  /** Date of joining, YYYY-MM-DD. "" when not recorded. */
  doj: string;
  /** Date of leaving, YYYY-MM-DD. "" for a serving employee — the usual case. */
  dol: string;
  designation: string;
  pan: string;
  uan: string;
  esic_ip_number: string;
  bank_ifsc: string;
  /**
   * The LAST FOUR DIGITS of the bank account and nothing else. The column is
   * `bank_account_last4` on the server too: a payroll master is the highest-value
   * row in the database for anybody who reaches it, and a full account number
   * buys nothing the payslip or the ECR needs.
   */
  bank_account_last4: string;
  is_active: boolean;
  /** Absent when the employee has no dedicated payable ledger. */
  payable_ledger_id?: string;
}

export interface ListEmployeesResponse {
  employees: EmployeeView[];
}

/**
 * The POST /employees body.
 *
 * ⛔ `is_active` IS OPTIONAL AND ITS ABSENCE MEANS TRUE — the server's field is a
 * `*bool` precisely so a client that does not know about the flag cannot create
 * an INACTIVE employee. An inactive employee is silently excluded from the pay
 * run, which is a missing salary discovered on pay day, so this client never
 * sends `false` unless somebody chose it.
 */
export interface CreateEmployeeRequest {
  code: string;
  name: string;
  doj?: string;
  dol?: string;
  designation?: string;
  pan?: string;
  uan?: string;
  esic_ip_number?: string;
  bank_ifsc?: string;
  bank_account_last4?: string;
  payable_ledger_id?: string;
  is_active?: boolean;
}

export interface CreateEmployeeResponse {
  employee_id: string;
}

/**
 * One employee's non-derivable facts for a wage month.
 *
 * EVERY FIELD IS A STRING for the reason this section's header states. `paid_days`
 * and `lop_days` are decimals too — a half-day of loss of pay is real, and
 * `parseFloat` on the wire is how it stops being exactly a half.
 *
 * Omitted fields are omitted, not sent as "0": the server skips an empty string
 * and leaves the engine's own zero value, so sending "0" for a declaration the
 * employee never made is the client asserting something it was not told.
 */
export interface PayRunMonthInput {
  employee_id: string;
  paid_days?: string;
  lop_days?: string;
  /** YYYY-MM-DD — first date this person was EVER a PF member (s.PF continuity). */
  first_pf_member_on?: string;
  prior_employer_taxable?: string;
  prior_employer_tds?: string;
  house_property_loss?: string;
  chapter_via?: string;
  other_income_declared?: string;
  non_resident?: boolean;
  /** The income-tax age band; the engine's own vocabulary, passed through. */
  age_band?: string;
}

/**
 * POST /vouchers/payroll — compute and post a whole wage month.
 *
 * ⛔ ITS DATE DIMENSION IS `wage_month`, NOT A VOUCHER DATE, AND THE BACK-DATED
 * CUTOFF STILL APPLIES TO IT. The server derives the voucher date from the wage
 * month with `payrolldb.LastDayOf` and runs the SAME period lock every invoice
 * meets (`peekPayrollVoucherDate`, `auth_payroll_cutoff_test.go`). So a pay run
 * for a closed month is a 403 that means "that month is locked" — never a
 * permission problem with payroll — and the screen must say which.
 */
export interface PayRunRequest {
  /** YYYY-MM. Strictly parsed server-side: "2026-13" and "2026-1" are refused. */
  wage_month: string;
  created_by?: string;
  narration?: string;
  inputs: PayRunMonthInput[];
}

export interface PayRunResponse {
  pay_run_id: string;
  voucher_id: string;
  voucher_no: string;
  version_no: number;
  status: string;
  /**
   * TRUE when this month had already been posted and nothing new was written.
   *
   * ⛔ IT IS NOT A FAILURE AND IT IS NOT A FRESH POSTING, and a screen that
   * renders it as either is wrong in a way that costs money: read as a failure,
   * the operator runs it again; read as a success, they believe a correction they
   * just keyed took effect when the stored run is the earlier one.
   */
  already_posted: boolean;
}

/** One line of the payroll register — an employee, or the TOTALS row. */
export interface PayrollRegisterRow {
  /** Absent on the totals row, which is how the totals row is identified. */
  employee_id?: string;
  employee_code?: string;
  employee_name?: string;
  days_paid: string;
  gross: string;
  epf_employee: string;
  esi_employee: string;
  tds: string;
  deductions: string;
  net: string;
  epf_employer: string;
  eps_employer: string;
  esi_employer: string;
}

/**
 * GET /payroll-register?month=YYYY-MM.
 *
 * ⚠️ THE RULESET VERSIONS ARE PART OF THE REPORT AND MUST BE RENDERED. A payroll
 * figure with no statement of which law produced it cannot be checked by anybody
 * who was not present when it was run — which is the whole audit problem with
 * every payroll system. The server puts them on the JSON and on the printed page;
 * a screen that drops them is the only place the chain breaks.
 */
export interface PayrollRegisterResponse {
  pay_run_id: string;
  wage_month: string;
  status: string;
  ss_ruleset_version: string;
  it_ruleset_version: string;
  rows: PayrollRegisterRow[];
  totals: PayrollRegisterRow;
}

/** One component line of a payslip. */
export interface PayslipLine {
  code: string;
  type: string;
  tag: string;
  /** "EARNING" or "DEDUCTION" — the server's own word, never inferred here. */
  direction: string;
  amount: string;
}

/**
 * A component the engine DID NOT compute, and why.
 *
 * ⛔ IT BELONGS ON THE PAYSLIP, NOT IN A LOG. A blank where a number should be is
 * a question the employee will ask, and the honest answer is "the rate has not
 * been notified", not "zero". The server puts these on the payslip for that
 * reason; the screen renders them beside the components rather than swallowing
 * them.
 */
export interface PayslipDeferral {
  component: string;
  reason: string;
  citation: string;
}

/** GET /payroll-payslip?month=YYYY-MM&employee_id=UUID. */
export interface PayslipResponse {
  pay_run_id: string;
  wage_month: string;
  employee_id: string;
  employee_code: string;
  employee_name: string;
  days_paid: string;
  lop_days: string;
  gross: string;
  deductions: string;
  net: string;
  lines: PayslipLine[];
  deferrals: PayslipDeferral[];
  ss_ruleset_version: string;
  it_ruleset_version: string;
}

/**
 * One challan on a Form 138 Annexure I statement.
 *
 * `total_tds_deposited` is OPTIONAL and omitting it is the better answer: the
 * server DERIVES the challan total from the month's payslips (what the books say
 * was deducted). A supplied amount that does not equal the deductee sum is
 * REFUSED — that reconciliation is the whole point of the statement, so this is
 * not a convenience field but a cross-check the operator can opt into.
 */
export interface Form138Challan {
  /** YYYY-MM — the wage month the challan pays for. */
  month: string;
  bsr_code: string;
  /** YYYY-MM-DD. */
  date_of_deposit: string;
  challan_serial_number: number;
  total_tds_deposited?: string;
  /** A government book-entry transfer rather than a bank deposit. */
  book_entry?: boolean;
}

/**
 * GET /payroll-form-138 — the quarterly salary-TDS statement (ex-24Q Annexure I).
 *
 * ⛔ NOT ONE OF THESE PARTICULARS IS IN THE DATABASE. The company master carries
 * no TAN and no challan, so every field here is supplied by the operator and an
 * absent one is a 400. Nothing is defaulted into a FILED RECORD — that is the
 * rule this whole shape exists to keep, and it is why the screen behind it is a
 * form rather than a download button.
 *
 * ⚠️ Q4 IS REFUSED BY THE SERVER (`ErrQ4FormatNotPublished`): the Q4 / Annexure
 * II record structure is not published, and guessing it would mis-file every
 * employee's whole-year salary detail.
 */
export interface Form138Request {
  /** "2026-27" or "2026" — the tax year beginning 1 April of the leading year. */
  tax_year: string;
  /** "Q1" | "Q2" | "Q3" (Q4 is refused — see above). */
  quarter: string;
  tan: string;
  deductor_pan: string;
  deductor_name: string;
  /** "24Q". */
  form_number: string;
  /** YYYY-MM-DD. */
  file_creation_date: string;
  /** Defaults to 1 server-side. Unique across every file this deductor has sent. */
  file_sequence?: number;
  challans: Form138Challan[];
}

/**
 * A statutory file download: the file, its statutory NAME, and the disclosure.
 *
 * ⛔ THE SERVER SERVES A ZIP, ON PURPOSE, AND THIS CLIENT NEVER UNWRAPS IT. None
 * of the three formats permits an in-file comment — the ECR is a bare member list
 * the portal parses line by line, the Form 138 file must begin with its FH record,
 * and the ESI CSV must begin with the ESIC column headers — so the "this has NOT
 * been through the validator" disclosure can only travel BESIDE the file. The
 * archive holds exactly two entries: the statutory file under its statutory
 * filename, and `<name>.README.txt`. A client that opened the zip and handed over
 * only the file would be separating the file from the statement of what is not
 * verified about it, which is the one thing the zip exists to prevent.
 */
export interface StatutoryFileDownload {
  /** The .zip itself, exactly as served. */
  blob: Blob;
  /** The download name from Content-Disposition, or null. */
  filename: string | null;
  /**
   * `X-Statutory-File` — the name of the statutory file INSIDE the archive.
   * Shown so an operator knows what the portal will be given, not just what
   * landed in their downloads folder.
   */
  statutoryFile: string | null;
  /**
   * `X-Statutory-Disclosure` — the server's own one-line warning that nothing
   * here has been through the FVU, the EPFO portal or the ESIC portal, none of
   * which can run in this environment. Rendered VERBATIM; never summarised.
   */
  disclosure: string | null;
}

/**
 * One effective-dated salary component.
 *
 * ⛔ THE EMPLOYEE MASTER ALONE CANNOT BE PAID, AND THAT IS WHY THIS TYPE EXISTS.
 * `payrolldb.SetSalaryComponent` and `SetStatutoryElection` shipped with Track 3
 * and had NO ROUTE, so an employee created through `POST /employees` met
 * "no salary structure is in force on <date>; there is nothing to pay and
 * nothing to contribute on" — a 422 on the PAY RUN, long after the master was
 * created, with nothing anywhere saying it was incomplete. Both children are now
 * served under the same `employee` authority as the master itself.
 *
 * The window is HALF-OPEN [effective_from, effective_to): with an inclusive
 * upper bound the day of a revision belongs to both the old row and the new one,
 * and a pay run for that month would have two answers to "what is the basic".
 * An empty `effective_to` is an open window — the usual case.
 */
export interface SalaryComponentView {
  id: string;
  effective_from: string;
  /** "" = still in force. */
  effective_to: string;
  /** The operator's own name for it — "Basic", "HRA", "Site allowance". */
  code: string;
  /** A member of the served component-type vocabulary; never free text. */
  type: string;
  amount: string;
  /**
   * Whether PF and ESI are contributed on this component (Code on Social
   * Security s.2(88)).
   *
   * ⛔ IT TRAVELS WITH THE ROW rather than being looked up by this client against
   * a vocabulary it fetched at some other moment. This is the single most
   * consequential fact about a component — a type that silently fell through to
   * "excluded" under-contributes PF for the whole of an employee's service with
   * no error anywhere — and a join between two responses is a join that can be
   * stale.
   */
  is_wages: boolean;
  /**
   * Whether this component is one of s.2(88) clauses (a)-(i), the only sum
   * proviso 1 measures against the one-half floor.
   *
   * A screen that showed only wages/not-wages would be telling an operator that
   * an excluded component never affects the contribution, and for these it does:
   * paid above one half of all remuneration, the EXCESS is deemed back into
   * wages. (j) gratuity and (k) retrenchment compensation are outside it.
   */
  in_deeming_test: boolean;
}

export interface ListSalaryComponentsResponse {
  salary_components: SalaryComponentView[];
}

/** The POST body for one effective-dated salary component. */
export interface SetSalaryComponentRequest {
  effective_from: string;
  effective_to?: string;
  code: string;
  type: string;
  /** A decimal STRING. Never a JSON number — see this section's header. */
  amount: string;
}

export interface SetSalaryComponentResponse {
  salary_component_id: string;
}

/**
 * One effective-dated statutory election: the PF contribution base, the pension
 * scheme, ESI coverage and the income-tax regime.
 *
 * ⛔ `regime` MAY BE "" AND THAT IS A DIFFERENT FACT FROM "NEW". An empty regime
 * is "no election was made", which the server resolves to the s.202 default when
 * it computes and STORES AS GIVEN so a later reader can still tell one from the
 * other. A screen that rendered "" as "New regime" would be reporting an
 * election nobody made.
 */
export interface StatutoryElectionView {
  id: string;
  effective_from: string;
  effective_to: string;
  /** "CEILING" | "ACTUAL" — no default; the two differ above the wage ceiling. */
  pf_base: string;
  pf_voluntary_base: string;
  employer_matches_voluntary: boolean;
  eps_member: boolean;
  esi_applicable: boolean;
  /** "" = no election; see the interface doc. */
  regime: string;
}

export interface ListStatutoryElectionsResponse {
  statutory_elections: StatutoryElectionView[];
}

/**
 * The POST body for one election.
 *
 * The three booleans are POSITIVE ASSERTIONS about one person and an omitted one
 * is FALSE, unlike {@link CreateEmployeeRequest}'s `is_active`: there is no
 * answer that is safe to assume — assuming membership contributes on somebody who
 * is not a member, and assuming coverage withholds from somebody who is not
 * covered — so the form asks for all three rather than letting a default decide.
 */
export interface SetStatutoryElectionRequest {
  effective_from: string;
  effective_to?: string;
  pf_base: string;
  pf_voluntary_base?: string;
  employer_matches_voluntary?: boolean;
  eps_member?: boolean;
  esi_applicable?: boolean;
  regime?: string;
}

export interface SetStatutoryElectionResponse {
  statutory_election_id: string;
}

/** One entry of the served salary-component vocabulary. */
export interface PayrollComponentType {
  code: string;
  is_wages: boolean;
  in_deeming_test: boolean;
}

/**
 * The closed sets a payroll form chooses from.
 *
 * ⛔ FETCHED AND NEVER HARDCODED, for the reason {@link SecurityVocabulary} is:
 * a form built from a list this client owns can compose a value the server
 * answers 400, and the operator meets the refusal after keying the whole thing.
 * Here it is worse than inconvenient — `is_wages` is a LEGAL classification (it
 * carries the Vivekananda Vidyamandir holding that a universally paid special
 * allowance IS wages), and a second copy of it in TypeScript, updated by
 * somebody else, is the drift that under-contributes PF with no error anywhere.
 */
export interface PayrollVocabularyResponse {
  component_types: PayrollComponentType[];
  pf_bases: string[];
  regimes: string[];
  /**
   * What an UNSTATED regime resolves to (s.202: the new regime).
   *
   * Served so a form can SAY so rather than pre-selecting it — pre-selecting
   * would record an election nobody made, which is exactly the distinction
   * {@link StatutoryElectionView.regime} keeps.
   */
  default_regime: string;
  age_bands: string[];
}

// ---------------------------------------------------------------------------
// PER-COMPANY PRINT TEMPLATES (track C)
// ---------------------------------------------------------------------------

/**
 * The seven fields a company may override on a printed document.
 *
 * ⛔ THE STRINGS ARE THE SERVER'S, and the vocabulary is CONSUMED FROM A
 * GENERATED GOLDEN rather than declared here — see `printTemplateSurface.ts`.
 * `bank_details` is deliberately ONE field covering TWO columns
 * (`show_bank_details` and `bank_lines`): every renderer tests them together, so
 * lines with the flag off print nothing, and offering them separately lets an
 * operator fill in their account number, save, and print a document with no bank
 * block having done everything the screen asked.
 */
export type PrintTemplateField =
  | "title"
  | "header_lines"
  | "footer_lines"
  | "declaration"
  | "terms"
  | "signature_label"
  | "bank_details";

/** What a company has overridden. Every field is optional; blank means "use the built-in default". */
export interface PrintTemplateOverrides {
  title: string;
  header_lines: string[] | null;
  footer_lines: string[] | null;
  declaration: string;
  terms: string;
  signature_label: string;
  show_bank_details: boolean;
  bank_lines: string[] | null;
}

/**
 * GET/PUT/DELETE .../print-templates/{docKind}.
 *
 * ⛔ `customised` AND THE BLANKNESS OF `overrides` ARE DIFFERENT FACTS and the
 * screen needs both. An absent row and a row of empty strings print the same
 * document but are not the same sentence: the first is "this company has never
 * been asked", the second is "this company deliberately prints nothing there".
 * Without `customised` a form cannot offer to CLEAR an override, because it does
 * not know there is one.
 *
 * ⚠️ THERE IS NO SERVER-COMPUTED "EFFECTIVE TEMPLATE" AND ASKING FOR ONE WAS
 * CONSIDERED AND REFUSED. It would be a third implementation of "blank falls back
 * to the built-in", and the two that exist in the renderers do not agree about
 * whitespace. `defaults` is what prints when a box is empty, so the screen shows
 * it as the placeholder — which tells the operator what will print without
 * anybody re-deriving it.
 */
export interface PrintTemplateResponse {
  doc_kind: string;
  /** Whether a print_template row exists at all. */
  customised: boolean;
  /** The fields THIS document's renderer actually reads. A form must offer no others. */
  honours: PrintTemplateField[];
  /** What prints when the corresponding box is blank. */
  defaults: {
    title: string;
    declaration: string;
    signature_label: string;
  };
  /**
   * This kind honours a title override but has no fixed default, because the
   * default is the document's own name. True only for `report`, whose title is
   * whichever of the thirty statements is being printed.
   */
  default_title_is_the_documents_own_name: boolean;
  overrides: PrintTemplateOverrides;
  /** Present only on a DELETE: whether there was anything to clear. */
  cleared?: boolean;
}

/** The PUT body. A FULL restatement: an omitted field is stored as empty, not left alone. */
export interface PrintTemplateRequest {
  title?: string;
  header_lines?: string[];
  footer_lines?: string[];
  declaration?: string;
  terms?: string;
  signature_label?: string;
  show_bank_details?: boolean;
  bank_lines?: string[];
}

// ---------------------------------------------------------------------------
// OFFLINE SYNC (track D) — the cloud sequencer's wire contract.
//
// ⛔ `SyncIntent.payload` IS A `VoucherRequest`, THE SAME TYPE THE ONLINE PATH
// POSTS, and that is load-bearing rather than tidy. The server decodes it with
// `buildVoucherInput`, the single translator every voucher route shares, so
// anything the online path carries the offline path carries for free — including
// the once-only AI-bill `draft_id`. `server.go` warns three times against a
// parallel type and records two silent-drop defects from ignoring it.
// ---------------------------------------------------------------------------

/** A hybrid-logical-clock reading on the wire. Mirrors `sync.HLCTimestamp`. */
export interface SyncHLC {
  wall_millis: number;
  counter: number;
  node_id: string;
}

/** CREATE / ALTER / DELETE — the change kinds an offline node records. */
export type SyncOp = "CREATE" | "ALTER" | "DELETE";

/**
 * One outbox entry: a single recorded voucher change awaiting commit.
 *
 * ⚠️ `provisional_no` IS NEVER SENT AS `payload.voucher_no`. The server allocates
 * the real sequential number; the provisional exists so the offline UI can show
 * something, and so the loser of a conflict can be surfaced with the label the
 * operator actually saw. `OfflineNode` forces `payload.voucher_no` empty.
 */
export interface SyncIntent {
  idempotency_key: string;
  client_voucher_id: string;
  op: SyncOp;
  class: string;
  provisional_no: string;
  hlc: SyncHLC;
  node_id: string;
  /** The head version an ALTER/DELETE was based on; 0 for a CREATE. */
  base_version: number;
  payload: VoucherRequest;
}

/**
 * The four TERMINAL dispositions of one committed intent.
 *
 * ⛔ THERE IS NO NON-TERMINAL STATUS ON THIS WIRE, and a probe must not invent
 * one (HANDOFF's standing rule). A commit either produces one of these four per
 * intent, or fails as a WHOLE REQUEST — see `SyncBatchRefusal`.
 */
export type SyncOutcomeStatus = "COMMITTED" | "CONFLICT" | "FROZEN" | "DUPLICATE";

/** The sequencer's answer for one intent, in submission order. */
export interface SyncOutcome {
  idempotency_key: string;
  client_voucher_id: string;
  status: SyncOutcomeStatus;
  /** The server-assigned number on a COMMITTED CREATE; "" otherwise. */
  final_voucher_no: string;
  version_no: number;
  /** Set on CONFLICT (the parked loser) and on FROZEN (the freeze row). */
  reconciliation_id: string;
  detail: string;
}

/** `POST /sync/commit` — one outcome per submitted intent, in caller order. */
export interface SyncCommitResponse {
  outcomes: SyncOutcome[];
}

/**
 * A WHOLE-BATCH refusal, which is NOT one of the four statuses above.
 *
 * ⛔⛔ THE OUTBOX IS INTACT WHEN THIS HAPPENS AND MUST BE LEFT THAT WAY. Two
 * request-level failures return no outcomes at all:
 *
 *   cutoff   the back-dated cutoff refuses the LOT if ONE intent is pre-cutoff
 *            ("a batch is only as permitted as its most-refused member" —
 *            auth_backdated_key_binding_test.go, cutoff round 2)
 *   stepup   403 STEPUP_REQUIRED: any write at a level requiring WebAuthn
 *
 * Rendering either as "these 12 vouchers failed" is wrong in the dangerous
 * direction — nothing was rejected, the batch was never judged intent by intent,
 * and a screen that says otherwise invites an operator to re-key work that is
 * still queued. Because the commit is ONE batched write, the whole outbox costs
 * exactly one step-up, which is the argument for draining it as a batch.
 */
export interface SyncBatchRefusal {
  /**
   * ⚠️ `offline` IS THE THIRD ONE, and it is the commonest of the three on this
   * track: the drain was attempted with no network, so the request never got an
   * answer. It belongs in this union rather than under `other` because the screen
   * must say something different — "you are still offline, nothing was sent"
   * rather than "the server refused the batch" — while the invariant it shares
   * with the other two is the important part: THE OUTBOX IS UNTOUCHED.
   */
  kind: "cutoff" | "stepup" | "offline" | "other";
  status: number;
  message: string;
}

/** One OPEN reconciliation-queue row (`GET /reconciliation-queue`). */
export interface ReconciliationEntry {
  id: string;
  voucher_id: string;
  attempted_op: string;
  /** "CONFLICT" for a parked sync loser; "IRN" / "FILED_GSTR1" for a freeze. */
  kind: string;
  ref_value: string;
  frozen_voucher_no: string;
  detail: string;
  created_at: string;
}

/** `GET /reconciliation-queue` */
export interface ReconciliationQueueResponse {
  entries: ReconciliationEntry[];
}

/** The parked voucher body, formatted the way the LIVE voucher route formats it. */
export interface ParkedVoucher {
  class: string;
  voucher_type_id: string;
  party_ledger_id: string;
  item_ledger_id: string;
  voucher_no: string;
  voucher_date: string;
  narration: string;
  party: string;
  created_by: string;
  source_guid: string;
  source_draft_id: string;
  postings: { ledger_id: string; dr_cr: string; amount: string }[];
  bill_allocations: { ledger_id: string; ref_name: string; ref_type: string; amount: string }[];
  inventory: {
    stock_item_id: string;
    godown_id: string;
    batch_id: string;
    direction: string;
    qty: string;
    rate: string;
    discount_pct: string;
    amount: string;
    rate_unstated: boolean;
  }[];
  physical_count: {
    stock_item_id: string;
    godown_id: string;
    batch_id: string;
    qty: string;
    rate: string;
  }[];
  /**
   * Blocks a two-pane diff has no room for, NAMED rather than dropped (e.g.
   * "GST parameters"). A field that is neither rendered above nor named here
   * fails `TestParkedVoucherAccountsForEveryVoucherInputField` server-side.
   */
  also_carries: string[];
}

/** The parked intent, with the entry metadata the compare screen needs. */
export interface ReconciliationPayloadResponse {
  reconciliation_id: string;
  voucher_id: string;
  attempted_op: string;
  kind: string;
  ref_value: string;
  frozen_voucher_no: string;
  detail: string;
  created_at: string;
  /**
   * ⚠️ FALSE IS A REAL ANSWER, NOT AN ERROR. Only a parked CONFLICT preserves the
   * keyed payload; a FROZEN row is raised by the ledger's freeze rule and never
   * held one. When this is false the operator's own version exists ONLY in the
   * local outbox on the machine that keyed it — read it from there, and say so
   * when it is not on this machine.
   */
  has_payload: boolean;
  payload_absent_because?: string;
  intent?: {
    idempotency_key: string;
    client_voucher_id: string;
    op: SyncOp;
    class: string;
    provisional_no: string;
    node_id: string;
    base_version: number;
    hlc: SyncHLC;
    payload: ParkedVoucher;
  };
}

/** `POST /reconciliation-queue/{id}/resolve` */
export interface ResolveReconciliationResponse {
  reconciliation_id: string;
  resolved: boolean;
}
