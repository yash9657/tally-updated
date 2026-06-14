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

/** The six accounting voucher classes (path segment + F-key mapping). */
export type VoucherClass =
  | "sales"
  | "purchase"
  | "receipt"
  | "payment"
  | "contra"
  | "journal";

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
}

export interface CreateCompanyResponse {
  company_id: string;
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

export interface CreateLedgerRequest {
  name: string;
  group_name: string;
  opening_balance?: Money;
  opening_dc?: string;
  maintain_bill_by_bill?: boolean;
  gstin?: string;
  state_code?: string;
  registration_type?: string;
  pan?: string;
  created_by?: string;
}
export interface CreateLedgerResponse {
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
