/**
 * In-memory implementation of {@link CoredClient} for hermetic component tests
 * and Storybook-style local development WITHOUT a live `cored` server.
 *
 * It is deliberately faithful where it cheaply can be (it accumulates created
 * masters and posted vouchers, derives a day book and a trial balance from the
 * postings, and keeps money as fixed-4 strings), and seeds a small demo company
 * so reports render non-empty out of the box. It is NOT an accounting engine —
 * GST is echoed as zero and statement trees are minimal. The contract (shapes,
 * money-as-string, company scoping) matches REST exactly.
 */

import type { CoredClient } from "./client";
// GENERATED, NOT AUTHORED. internal/reporting's TestReturnFileStatusGolden emits
// this from the Go that the real server runs, so the mock cannot drift from it.
// See the note at the tdsRegister mock below for the two occasions it did.
import registerProse from "./goldens/tds-register-prose.json";
import { ApiError } from "./http";
import type {
  BalanceSheetResponse,
  CompanyView,
  CreateCompanyRequest,
  CreateCompanyResponse,
  CreateGroupRequest,
  CreateGroupResponse,
  CreateLedgerRequest,
  CreateLedgerResponse,
  CreateStockItemRequest,
  CreateStockItemResponse,
  CreateUnitRequest,
  CreateUnitResponse,
  CreateVoucherTypeRequest,
  CreateVoucherTypeResponse,
  DayBookEntry,
  DayBookPosting,
  DayBookQuery,
  DeleteVoucherResponse,
  DayBookResponse,
  GSTR1Query,
  GSTR1Response,
  ImportTallyXMLQuery,
  ImportTallyXMLResponse,
  Money,
  OutstandingParty,
  OutstandingsQuery,
  OutstandingsResponse,
  ProfitLossResponse,
  TrialBalanceResponse,
  VoucherClass,
  VoucherRequest,
  VoucherResponse,
  VoucherView,
  PostingView,
  BillAllocationView,
  GodownListItem,
  GSTLineRequest,
  InventoryLineRequest,
  CostAllocationView,
  FulfilmentView,
  InventoryLineView,
  PhysicalCountLineRequest,
  PostingRequest,
  ListGodownsResponse,
  ListStockBatchesResponse,
  ListStockItemsResponse,
  StockBatchListItem,
  StockItemListItem,
  StockPositionRow,
  StockSummaryItem,
  StockSummaryResponse,
  ListLedgersResponse,
  SetLedgerOpeningRequest,
  SetLedgerOpeningResponse,
  VoucherHistoryResponse,
  BankReconciliationResponse,
  BankStatementImport,
  DrCr,
  BankStatementLine,
  ImportedStatementResponse,
  ImportStatementRequest,
  ImportStatementResponse,
  PreviewStatementRequest,
  PreviewStatementResponse,
  StatementColumnMapping,
  DeductorStatus,
  TDSRegisterResponse,
  TCSDeclaration,
  DeductorStatusListResponse,
  TCSDeclarationListResponse,
  CreateTCSDeclarationRequest,
  CreateTCSDeclarationResponse,
  SetDeductorStatusRequest,
  SetDeductorStatusResponse,
  MinorHead,
  DeductorCategory,
  TDSChallan,
  TDSChallanAllocation,
  CostCategory,
  CostCategoryListResponse,
  CostCategorySummaryResponse,
  CreateOrderRequest,
  CloseOrderRequest,
  OrderDirection,
  OrderListResponse,
  OrderView,
  OrderBookRow,
  OrderBookResponse,
  CreateBomRequest,
  BomView,
  EWayBillRequest,
  EWayBillResponse,
  EInvoiceRaiseRequest,
  EInvoiceRaised,
  CostCentre,
  CostCentreListResponse,
  CostCentreWriteResponse,
  CostCentreSummaryResponse,
  CreateCostCategoryRequest,
  CreateCostCentreRequest,
  TDSChallanListResponse,
  CreateTDSChallanRequest,
  CreateTDSChallanResponse,
  TDSProfile,
  TDSProfileResponse,
  SetTDSProfileRequest,
  SetTDSProfileResponse,
  BankReconLine,
  SetBankDateRequest,
  SetBankDateResponse,
  VoucherVersionView,
  GSTLineView,
} from "./types";
// A VALUE, not a type: the recorded `payload.class` labels the server stamps.
// The mock must agree with them exactly — see classLabel().
import { VOUCHER_CLASS_LABELS } from "./types";
import type { QueryRequest, QueryResult } from "./query";
import type {
  CreateSecurityLevelResponse,
  DeleteSecurityLevelResponse,
  InviteMemberRequest,
  InviteMemberResponse,
  InviteView,
  MeResponse,
  MemberView,
  MembersResponse,
  RemoveMemberResponse,
  RevokeInviteResponse,
  SecurityLevelRequest,
  SecurityLevelRule,
  SecurityLevelsResponse,
  SecurityLevelView,
  SecurityVocabulary,
  UpdateMemberRequest,
  UpdateMemberResponse,
  UpdateSecurityLevelResponse,
} from "./admin";
import { deriveGstSplit } from "../screens/vouchers/gstDerive";
import { taxableValueOf } from "../screens/vouchers/itemInvoice";

/**
 * WHICH SIDE each leg of an item invoice lands on, mirroring
 * ledger.orientationFor. One row per class, so a reversed purchase is a visible
 * diff rather than an emergent property of where four literals were typed.
 * The notes REVERSE their parent document, which is why a credit note reads
 * like a purchase here and a debit note like a sale.
 */
const ITEM_INVOICE_ORIENTATION: Readonly<
  Record<string, { party: "D" | "C"; goods: "D" | "C"; tax: "D" | "C" }>
> = {
  sales: { party: "D", goods: "C", tax: "C" },
  purchase: { party: "C", goods: "D", tax: "D" },
  "credit-note": { party: "C", goods: "D", tax: "D" },
  "debit-note": { party: "D", goods: "C", tax: "C" },
};

/** Which way the goods physically move, mirroring ledger.expectedStockDirection. */
const ITEM_INVOICE_DIRECTION: Readonly<Record<string, "IN" | "OUT">> = {
  sales: "OUT",
  "debit-note": "OUT",
  purchase: "IN",
  "credit-note": "IN",
};

/**
 * The NON-ACCOUNTING classes, mirroring ledger.stockOnlyClasses. Kept as its
 * own literal set rather than derived from "not in ITEM_INVOICE_ORIENTATION",
 * for the same reason the Go side does: a class added without being classified
 * either way must be a visible omission, not a silent grant of the
 * no-postings-required behaviour.
 */
const STOCK_ONLY_CLASSES: Readonly<Record<string, true>> = {
  "delivery-note": true,
  "receipt-note": true,
  "stock-journal": true,
  "physical-stock": true,
};

/**
 * The ONE direction a class's lines must all take, mirroring
 * ledger.fixedStockDirection. A Stock Journal has none (a transfer is an OUT
 * and an IN); Physical Stock has none either, because its movement is DERIVED.
 */
const STOCK_FIXED_DIRECTION: Readonly<Record<string, "IN" | "OUT">> = {
  "delivery-note": "OUT",
  "receipt-note": "IN",
};

let idCounter = 0;
function id(prefix: string): string {
  idCounter += 1;
  // Deterministic, UUID-shaped-enough id for tests.
  return `${prefix}-${idCounter.toString().padStart(12, "0")}`;
}

/**
 * The system 'Main Location' every company is seeded with (migration 0025's
 * seed_inventory_defaults). The id is derived from the company id so it is
 * stable across calls without a counter.
 */
function mainLocation(companyID: string): GodownListItem {
  return {
    id: `${companyID}:godown-main`,
    name: "Main Location",
    is_system: true,
  };
}

// ---------------------------------------------------------------------------
// Stock replay (the mock's GET /stock-summary)
// ---------------------------------------------------------------------------
//
// STOCK VALUE IS NEVER STORED — not on the server and not here. The position is
// replayed from the movement lines held on the HEAD version of each voucher,
// which is the mock's equivalent of the server's current_version_id join: an
// altered voucher restates its view in place and a deleted one is removed from
// `views`, so neither can double-count.
//
// The valuation mirrors internal/inventory.WeightedAverage arm for arm,
// including the "an issue that empties the position takes the whole remaining
// value with it" rule — without it a sub-paisa residue survives at zero
// quantity and the next inward blends it into its cost. It is still a MOCK: it
// has no stock_opening (nothing here can create one) and the server's valuer
// stays authoritative. Quantities and values are BigInt-scaled integers, never
// floats, for the same reason the wire is strings.

/** Scale of a fixed-4 quantity/value, as a BigInt. */
const QSCALE = 10_000n;
/** Working precision of the running weighted-average cost rate (inventory.rateScale). */
const RATE_SCALE = 10n ** 16n;

/** Exact fixed-4 string -> integer ten-thousandths (no Number in the path). */
function q4(value: string): bigint {
  const m = /^([+-]?)(\d*)(?:\.(\d*))?$/.exec((value ?? "").trim());
  if (!m) return 0n;
  const whole = m[2] ?? "";
  const frac = (m[3] ?? "").slice(0, 4).padEnd(4, "0");
  if (whole === "" && (m[3] ?? "") === "") return 0n;
  return (
    (m[1] === "-" ? -1n : 1n) * BigInt((whole === "" ? "0" : whole) + frac)
  );
}

/** Integer ten-thousandths -> fixed-4 string. */
function q4str(units: bigint): string {
  const neg = units < 0n;
  const abs = (neg ? -units : units).toString().padStart(5, "0");
  return `${neg ? "-" : ""}${abs.slice(0, -4)}.${abs.slice(-4)}`;
}

/** Divide rounding half-away-from-zero (shopspring/decimal's Round). */
function q4div(n: bigint, d: bigint): bigint {
  return n >= 0n ? (n + d / 2n) / d : -((-n + d / 2n) / d);
}

/** One position's running totals during a replay. */
interface MockPosition {
  key: { itemID: string; godownID: string; batchID: string };
  item: string;
  unit: string;
  godown: string;
  batch: string;
  inwardQty: bigint;
  inwardValue: bigint;
  outwardQty: bigint;
  outwardValue: bigint;
  qty: bigint;
  value: bigint;
}

function replayStock(c: MockCompany, asOn?: string): StockSummaryResponse {
  // Flatten the head versions into movements, keeping the voucher's date and
  // number so the replay can be ordered the way the server orders it. Weighted
  // average is ORDER-DEPENDENT, so an unordered fold would return a different,
  // equally confident closing value depending on insertion order.
  const moves: {
    date: string;
    no: string;
    lineNo: number;
    line: InventoryLineView;
  }[] = [];
  for (const v of c.views.values()) {
    if (asOn && v.voucher_date > asOn) continue;
    for (const line of v.inventory ?? []) {
      moves.push({
        date: v.voucher_date,
        no: v.voucher_no,
        lineNo: line.line_no,
        line,
      });
    }
  }
  moves.sort(
    (a, b) =>
      a.date.localeCompare(b.date) ||
      a.no.localeCompare(b.no) ||
      a.lineNo - b.lineNo,
  );

  const positions = new Map<string, MockPosition>();
  for (const m of moves) {
    const l = m.line;
    const key = `${l.stock_item_id}|${l.godown_id}|${l.batch_id}`;
    let p = positions.get(key);
    if (!p) {
      p = {
        key: {
          itemID: l.stock_item_id,
          godownID: l.godown_id,
          batchID: l.batch_id,
        },
        item: l.item,
        unit:
          c.stockItems.find((s) => s.id === l.stock_item_id)?.unit_symbol ?? "",
        godown: l.godown,
        batch: l.batch,
        inwardQty: 0n,
        inwardValue: 0n,
        outwardQty: 0n,
        outwardValue: 0n,
        qty: 0n,
        value: 0n,
      };
      positions.set(key, p);
    }
    const qty = q4(l.qty);
    // The line's value is RECOMPUTED from qty/rate/discount, exactly as
    // inventory.Movement.Value() does — the stored `amount` is the invoice
    // figure and the valuer never reads it.
    const gross = q4div(qty * q4(l.rate), QSCALE);
    const disc = q4(l.discount_pct);
    const value =
      disc === 0n
        ? gross
        : q4div(gross * (100n * QSCALE - disc), 100n * QSCALE);

    if (l.direction === "IN") {
      p.inwardQty += qty;
      p.inwardValue += value;
      p.qty += qty;
      p.value += value;
      continue;
    }
    const nextQty = p.qty - qty;
    let consumed: bigint;
    if (nextQty === 0n) {
      consumed = p.value; // empty the position: take the whole remaining value
      p.qty = 0n;
      p.value = 0n;
    } else {
      const rate = p.qty === 0n ? 0n : q4div(p.value * RATE_SCALE, p.qty);
      consumed = q4div(qty * rate, RATE_SCALE);
      p.qty = nextQty;
      p.value -= consumed;
    }
    p.outwardQty += qty;
    p.outwardValue += consumed;
  }

  const rows: StockPositionRow[] = [...positions.values()].map((p) => ({
    stock_item_id: p.key.itemID,
    item: p.item,
    unit: p.unit,
    godown_id: p.key.godownID,
    godown: p.godown,
    batch_id: p.key.batchID,
    batch: p.batch,
    valuation_method: "WEIGHTED_AVG",
    opening_qty: "0.0000",
    opening_value: "0.0000",
    inward_qty: q4str(p.inwardQty),
    inward_value: q4str(p.inwardValue),
    outward_qty: q4str(p.outwardQty),
    outward_value: q4str(p.outwardValue),
    closing_qty: q4str(p.qty),
    closing_value: q4str(p.value),
    closing_rate: q4str(p.qty === 0n ? 0n : q4div(p.value * QSCALE, p.qty)),
  }));
  rows.sort(
    (a, b) => a.item.localeCompare(b.item) || a.godown.localeCompare(b.godown),
  );

  // Fold the flat rows into the item tree, exactly as the server does — one set
  // of numbers presented two ways, never two computations that can disagree.
  const items: StockSummaryItem[] = [];
  for (const row of rows) {
    let it = items.find((x) => x.stock_item_id === row.stock_item_id);
    if (!it) {
      it = {
        stock_item_id: row.stock_item_id,
        item: row.item,
        unit: row.unit,
        opening_qty: "0.0000",
        opening_value: "0.0000",
        inward_qty: "0.0000",
        inward_value: "0.0000",
        outward_qty: "0.0000",
        outward_value: "0.0000",
        closing_qty: "0.0000",
        closing_value: "0.0000",
        positions: [],
      };
      items.push(it);
    }
    it.positions.push(row);
    it.inward_qty = addMoney(it.inward_qty, row.inward_qty);
    it.inward_value = addMoney(it.inward_value, row.inward_value);
    it.outward_qty = addMoney(it.outward_qty, row.outward_qty);
    it.outward_value = addMoney(it.outward_value, row.outward_value);
    it.closing_qty = addMoney(it.closing_qty, row.closing_qty);
    it.closing_value = addMoney(it.closing_value, row.closing_value);
  }

  let total = "0.0000";
  for (const row of rows) total = addMoney(total, row.closing_value);
  return { as_on: asOn ?? "", items, rows, total_value: total };
}

/**
 * Mirror of the backend numbering model (WORKSTREAM B spec / numbering.go), kept
 * deliberately aligned so component + e2e-style assertions about FORMAT and
 * SEQUENCE hold against the mock exactly as they would against the live server:
 *
 *  - METHOD: AUTOMATIC — the server (here, the mock) allocates the next number
 *    when the client sends voucher_no empty. A non-empty client value is an
 *    OVERRIDE: honored verbatim and the counter is NOT bumped (next auto stays
 *    prior-max+1). MANUAL with empty voucher_no would be an error, but the mock
 *    seeds AUTOMATIC types only.
 *  - FORMAT: prefix + zero-pad(n, width) + suffix, with a per-(type, FY) running
 *    counter starting at 1.
 *  - FY SCOPE: Indian FY (1-Apr..31-Mar) derived from the VOUCHER DATE, not
 *    today. month>=4 => start-year = year, else year-1. The counter restarts at
 *    1 for each (class, fin_year), so a new FY restarts numbering.
 *  - GAP-ON-DELETE / NO-REALLOC-ON-ALTER: the counter is monotonic; deletes
 *    leave gaps and alters never allocate (the loaded number is resent).
 */

/** Per-class numbering format. Aligned with the server's seeded voucher types. */
interface NumberFormat {
  prefix: string;
  suffix: string;
  /** Zero-pad width; null/undefined => bare integer (Tally default). */
  width: number | null;
}

/**
 * Seeded per-class formats. The server seeds a Sales voucher type; the mock
 * gives each class a faithful AUTOMATIC format so every voucher screen's
 * Ctrl+A surfaces a formatted number. Sales uses a 4-wide zero-padded counter
 * ("INV/0001"); the others use bare integers (Tally's no-padding default).
 */
const CLASS_NUMBER_FORMATS: Readonly<Record<string, NumberFormat>> = {
  sales: { prefix: "INV/", suffix: "", width: 4 },
  purchase: { prefix: "BILL/", suffix: "", width: 4 },
  receipt: { prefix: "RCPT/", suffix: "", width: null },
  payment: { prefix: "PYMT/", suffix: "", width: null },
  contra: { prefix: "CTRA/", suffix: "", width: null },
  journal: { prefix: "JRNL/", suffix: "", width: null },
  // The stock vouchers carry their OWN series, as they do in Tally: a delivery
  // note and the invoice that follows it are two documents and are counted
  // separately, so a warehouse that ships 40 times a month does not push the
  // invoice numbers forward by 40.
  "delivery-note": { prefix: "DN/", suffix: "", width: 4 },
  "receipt-note": { prefix: "RN/", suffix: "", width: 4 },
  "stock-journal": { prefix: "SJ/", suffix: "", width: 4 },
  "physical-stock": { prefix: "PS/", suffix: "", width: 4 },
};

/** Derive the Indian FY start-year from a YYYY-MM-DD voucher date. */
export function finYearOf(voucherDate: string): number {
  const d = (voucherDate || "").slice(0, 10);
  const [yStr, mStr] = d.split("-");
  const year = parseInt(yStr ?? "", 10);
  const month = parseInt(mStr ?? "", 10);
  if (!Number.isFinite(year) || !Number.isFinite(month)) {
    // Fall back to the current date's FY (server defaults empty date to today).
    const now = new Date();
    const m = now.getMonth() + 1;
    return m >= 4 ? now.getFullYear() : now.getFullYear() - 1;
  }
  return month >= 4 ? year : year - 1;
}

/** prefix + zero-pad(n, width) + suffix — the formatVoucherNo contract. */
export function formatVoucherNo(fmt: NumberFormat, n: number): string {
  const body =
    fmt.width != null ? String(n).padStart(fmt.width, "0") : String(n);
  return `${fmt.prefix}${body}${fmt.suffix}`;
}

/** Fixed-4 string add of two decimal strings (string-safe, no float drift). */
function addMoney(a: Money, b: Money): Money {
  const scale = 10000;
  const ai = Math.round(parseFloat(a || "0") * scale);
  const bi = Math.round(parseFloat(b || "0") * scale);
  return ((ai + bi) / scale).toFixed(4);
}

// The fifteen s.402(11)(a)-(l) categories the server serves from
// compliance.KnownEntityCategories. Duplicated here ONLY because the mock has no
// server to ask; the real client renders whatever the API returns, so this list
// never reaches a real screen.
const MOCK_ENTITY_CATEGORIES = [
  "GOVERNMENT",
  "LOCAL_AUTHORITY",
  "STATUTORY_CORPORATION",
  "COMPANY",
  "COOPERATIVE_SOCIETY",
  "HOUSING_AUTHORITY",
  "REGISTERED_SOCIETY",
  "TRUST",
  "UNIVERSITY",
  "FOREIGN_ENTITY",
  "FIRM",
  "INDIVIDUAL",
  "HUF",
  "AOP",
  "BOI",
];

// ---------------------------------------------------------------------------
// T6 — THE TDS CHALLAN AND THE DEDUCTOR PROFILE (migrations 0069 / 0070).
//
// ⚠️ EVERY SENTENCE THIS MOCK REFUSES WITH IS A HAND COPY OF THE SERVER'S, AND A
// HAND COPY IS THE THING THAT WENT WRONG ON THE REGISTER (see the tdsRegister
// mock below, whose prose is now GENERATED because two hand copies went stale for
// two days while every test stayed green).
//
// There is no generator for these yet, so the rule for whoever writes the screen
// tests is: ASSERT THAT THE MESSAGE THE CLIENT THREW IS THE MESSAGE THAT REACHED
// THE SCREEN — capture `err.message` from the rejected promise and look for THAT
// string in the DOM. Never retype one of these sentences into a test as a
// literal: a retyped sentence pins the mock's wording, and the mock's wording is
// not the product's.
// ---------------------------------------------------------------------------

/**
 * The three Annexure 7 minor heads the server serves from
 * masters.TDSChallanMinorHeads(), IN THE CHECK'S ORDER.
 *
 * Duplicated here ONLY because the mock has no server to ask — exactly as
 * MOCK_ENTITY_CATEGORIES is. A real client renders whatever
 * `minor_heads` carries, so this list never reaches a real screen.
 */
const MOCK_MINOR_HEADS: MinorHead[] = [
  { code: "200", description: "tax the deductor pays over of its own motion" },
  { code: "400", description: "a demand raised on assessment" },
  { code: "100", description: "advance tax" },
];

/**
 * Annexure 4's sixteen deductor categories in the workbook's printed order, with
 * the two conditional rules attached — the mirror of masters.DeductorCategories().
 *
 * The RULES are here because the screen needs them to know that choosing "State
 * Government" makes the Annexure 5 state code mandatory and that choosing
 * "Company" FORBIDS it. Same caveat as above: served, never a client's own copy.
 */
const MOCK_DEDUCTOR_CATEGORIES: DeductorCategory[] = [
  { code: "A", description: "Central Government", state_name_code_required: false, ministry_required: true, ministry_allowed: true },
  { code: "S", description: "State Government", state_name_code_required: true, ministry_required: false, ministry_allowed: false },
  { code: "D", description: "Statutory body (Central Govt.)", state_name_code_required: false, ministry_required: true, ministry_allowed: true },
  { code: "E", description: "Statutory body (State Govt.)", state_name_code_required: true, ministry_required: false, ministry_allowed: true },
  { code: "G", description: "Autonomous body (Central Govt.)", state_name_code_required: false, ministry_required: true, ministry_allowed: true },
  { code: "H", description: "Autonomous body (State Govt.)", state_name_code_required: true, ministry_required: false, ministry_allowed: true },
  { code: "L", description: "Local Authority (Central Govt.)", state_name_code_required: false, ministry_required: false, ministry_allowed: true },
  { code: "N", description: "Local Authority (State Govt.)", state_name_code_required: true, ministry_required: false, ministry_allowed: true },
  { code: "K", description: "Company", state_name_code_required: false, ministry_required: false, ministry_allowed: false },
  { code: "M", description: "Branch / Division of Company", state_name_code_required: false, ministry_required: false, ministry_allowed: false },
  { code: "P", description: "Association of Person (AOP)", state_name_code_required: false, ministry_required: false, ministry_allowed: false },
  { code: "T", description: "Association of Person (Trust)", state_name_code_required: false, ministry_required: false, ministry_allowed: false },
  { code: "J", description: "Artificial Juridical Person", state_name_code_required: false, ministry_required: false, ministry_allowed: false },
  { code: "B", description: "Body of Individuals", state_name_code_required: false, ministry_required: false, ministry_allowed: false },
  { code: "Q", description: "Individual/HUF", state_name_code_required: false, ministry_required: false, ministry_allowed: false },
  { code: "F", description: "Firm", state_name_code_required: false, ministry_required: false, ministry_allowed: false },
];

/** The format's literal for a deductor not required to hold a PAN (masters.PANNotRequired). */
const MOCK_PAN_NOT_REQUIRED = "PANNOTREQD";

/** Annexure 3's "Other" code, which the ministry_other field exists to expand. */
const MOCK_MINISTRY_OTHER = "99";

/**
 * ONE FIXTURE DEDUCTION — the mock's stand-in for a `tds_deduction` row.
 *
 * ⛔ THE MOCK DERIVES NO TAX AND KEEPS NO DEDUCTIONS. It cannot: TDS derivation
 * is a server-side engine and nothing here posts through it. So without a fixture
 * table an allocation could not be checked against ANYTHING, and the two refusals
 * that matter most on the challan screen — "that is not what was deducted" and
 * "another challan already paid that" — would be unreachable, which would teach
 * the screen that refusals do not happen.
 *
 * These rows are therefore FIXTURES, not facts. What follows from that:
 *
 *   - They are IDENTICAL IN EVERY MOCK COMPANY, so a screen test does not have
 *     to seed anything, and there is no company where an allocation is silently
 *     unrefusable.
 *   - THEY DO NOT APPEAR IN `tdsRegister`, which serves an empty register. The
 *     mock's register and its challan path do not agree about what was deducted,
 *     and nothing here can make them: the register's prose is generated from the
 *     Go and this table is invented. That does NOT stop the challan screen: these
 *     rows are served as `allocatable_deductions` on `listTDSChallans`, exactly as
 *     the server serves its own, so the picker has something to offer.
 *   - ⛔ THE IDS ARE REAL UUIDS, AND THAT IS A DEFECT THIS FILE ALREADY HAD. They
 *     used to be the mock's own shape ("voucher-tds-fixture-p1"), and the real
 *     server refuses a voucher_id that is not a uuid. So the screen tests keyed
 *     ids that cored rejects with "allocation 1: invalid voucher_id" and passed
 *     green while the feature was unusable on the real stack. A fixture whose
 *     shape the server would refuse is not a fixture, it is a way of not testing.
 */
export interface MockTDSDeduction {
  /**
   * ⚠️ A UUID, because the real server parses it as one. See the note above: the
   * previous non-uuid ids let every screen test pass against a form no operator
   * could complete against cored.
   */
  voucher_id: string;
  voucher_no: string;
  section: string;
  /** 0-BASED, as the natural key is. */
  leg_seq: number;
  deductee_name: string;
  /** yyyy-mm-dd — the date of the DEDUCTION. Shown in the allocation picker. */
  deduction_date: string;
  /** Fixed-point string. NOT whole rupees — see the 3,333.30 row. */
  tax_rupees: Money;
  /** The year the deduction belongs to. An allocation across years is refused. */
  tax_year_start: number;
}

/**
 * The four fixture deductions every mock company knows about. Exported so a
 * screen test (and a developer driving the browser) has real triples to allocate
 * against — and, deliberately, one of each interesting shape:
 *
 *   1. P1 — a plain whole-rupee deduction on LEG 0. The happy path.
 *   2. P2 — ₹33,333 at 10% = ₹3,333.30, which is NOT whole rupees. A challan's
 *      tax must be, so paying this one leaves a few paise in
 *      `unallocated_tax_rupees` — the ordinary case the field exists for, and
 *      the one a screen must NOT present as an error.
 *   3. P3 — a SUB-THRESHOLD deduction of zero. Nothing can be allocated to it:
 *      it is still reported, under the return's derived nil challan. It is
 *      therefore EXCLUDED from `allocatable_deductions` (as the server excludes
 *      it) but kept here, so the "no tax was deducted" refusal stays reachable
 *      for a caller that posts the triple directly.
 *   4. P4 — LEG 1 WITH TAX ON IT, which exists for one reason: a picker that
 *      quietly sent leg 0 for every row would still pass on P1, P2 and P3. This
 *      is the row that fails such a picker.
 *
 * ⛔ EVERY voucher_id HERE IS A UUID. See {@link MockTDSDeduction}.
 */
export const MOCK_TDS_DEDUCTIONS: MockTDSDeduction[] = [
  {
    voucher_id: "3f6b1c2e-0a4d-4c8b-9e11-7a2d5f0b1001",
    voucher_no: "P1",
    section: "194J",
    leg_seq: 0,
    deductee_name: "Chitra Consulting",
    deduction_date: "2026-05-05",
    tax_rupees: "20000.0000",
    tax_year_start: 2026,
  },
  {
    voucher_id: "3f6b1c2e-0a4d-4c8b-9e11-7a2d5f0b1002",
    voucher_no: "P2",
    section: "194C",
    leg_seq: 0,
    deductee_name: "Rao Contractors",
    deduction_date: "2026-06-10",
    tax_rupees: "3333.3000",
    tax_year_start: 2026,
  },
  {
    voucher_id: "3f6b1c2e-0a4d-4c8b-9e11-7a2d5f0b1003",
    voucher_no: "P3",
    section: "194C",
    leg_seq: 1,
    deductee_name: "Rao Contractors",
    deduction_date: "2026-06-10",
    tax_rupees: "0.0000",
    tax_year_start: 2026,
  },
  {
    voucher_id: "3f6b1c2e-0a4d-4c8b-9e11-7a2d5f0b1004",
    voucher_no: "P4",
    section: "194H",
    leg_seq: 1,
    deductee_name: "Iyer Brokers",
    deduction_date: "2026-07-01",
    tax_rupees: "5000.0000",
    tax_year_start: 2026,
  },
];

/** The natural key of a deduction / an allocation: (voucher, section, leg). */
function allocationTriple(
  voucherID: string,
  section: string,
  legSeq: number,
): string {
  return `${voucherID}|${section}|${legSeq}`;
}

function mockDeductionFixtures(): Map<string, MockTDSDeduction> {
  return new Map(
    MOCK_TDS_DEDUCTIONS.map((d) => [
      allocationTriple(d.voucher_id, d.section, d.leg_seq),
      d,
    ]),
  );
}

/**
 * A DEEP copy of a profile, handed out on every read and kept on every write.
 *
 * Not tidiness. A profile screen holds the fetched row AS ITS FORM STATE and
 * edits it in place — so a shallow copy would leave `address` and `responsible`
 * shared with the mock's store, and keying into the form would "save" the change
 * with no PUT at all. The next GET would then confirm it, and the screen would
 * pass a test that proves nothing.
 */
function cloneTDSProfile(p: TDSProfile): TDSProfile {
  return {
    ...p,
    address: { ...p.address },
    responsible: { ...p.responsible, address: { ...p.responsible.address } },
  };
}

/** The CIN — (branch, date, serial) — which is how the department identifies a payment. */
function mockCIN(bsr: string, depositDate: string, challanNo: string): string {
  return `${bsr}:${depositDate}:${challanNo}`;
}

/**
 * A fixed-4 string with its trailing zeros trimmed, for a SENTENCE only.
 * shopspring's `.String()` renders "20000" where StringFixed(4) renders
 * "20000.0000", and the server's refusals use the former. Never used on a value
 * that goes back on the wire.
 */
function mockPlainAmount(fixed4: Money): string {
  if (!fixed4.includes(".")) return fixed4;
  return fixed4.replace(/\.?0+$/, "") || "0";
}

/** "2025-26" from 2025, mirroring the server's taxYearLabel. */
function mockTaxYearLabel(start: number): string {
  return `${start}-${String((start + 1) % 100).padStart(2, "0")}`;
}

interface MockCompany {
  id: string;
  name: string;
  /**
   * The company's home GST state (2-digit code) — the mirror of
   * `company.state_code`. Mirrors the SERVER contract twice: it is echoed by
   * {@link CoredClient.getCompany}, and it is the fallback supplier state used
   * by `deriveGst` when a posted `gst` block leaves `supplier_state_code`
   * absent/empty (exactly what internal/ledger/ledger.go does inside the post
   * transaction). Without that fallback the mock would derive IGST for a
   * server-defaulted intra-state post and silently disagree with cored.
   */
  stateCode: string;
  /** The company's own GSTIN ("" when unset), mirroring `company.gstin`. */
  gstin: string;
  fyStartDate: string;
  baseCurrency: string;
  /**
   * The 0015 company-profile block. Grouped rather than splayed across the
   * type so a new profile field is one edit here and one in `defaultProfile`.
   * `booksBeginningFrom` is the load-bearing one: distinct from fyStartDate,
   * it is the date opening balances are stated as at.
   */
  profile: CompanyProfile;
  ledgers: {
    id: string;
    name: string;
    group: string;
    /**
     * The seeded PRIMARY group this ledger's group descends from, when that is
     * not `group` itself. The real server decides bank-ness by ltree ancestry
     * (reporting.BankLedgerPredicate); this mock has no group hierarchy at all,
     * so this field stands in for the ancestor the ltree would have walked to.
     *
     * It exists so the mock can represent the case that matters — a bank account
     * in a user-created sub-group, whose `group_name` is neither 'Bank Accounts'
     * nor 'Bank OD A/c' — instead of only the easy case where the immediate group
     * name happens to be the answer.
     */
    bankParentGroup?: string;
    stateCode?: string;
    gstin?: string;
    registrationType?: string;
    maintainBillByBill?: boolean;
    openingBalance?: Money;
    openingDC?: string;
    /**
     * The party's postal block (migration 0039). Held as real fields for the
     * same reason the rest of this row is: a mock that accepted an address on
     * create and dropped it would let a round-trip test pass against a store
     * that never kept it.
     */
    address?: string;
    city?: string;
    pincode?: string;
    /**
     * The company's OWN bank account identity (migration 0064). STORED here for
     * the same reason the address row above is: a mock that accepted them on
     * create and dropped them would let a round-trip test pass against a store
     * that never kept them — and the imported-statement report reads them back
     * to decide whether it can say the account was verified.
     */
    bankAccountNumber?: string;
    bankIFSC?: string;
    isSystem?: boolean;
  }[];
  vouchers: DayBookEntry[];
  /**
   * Bank reconciliations, keyed `${ledgerID}:${voucherID}:${legNo}`.
   *
   * OUTSIDE the voucher, exactly as migration 0045 puts it outside the hash
   * chain: a bank date is a fact about the BANK, not about the voucher. Storing
   * the amount and side that were cleared is what makes a later alter DETECTABLE
   * rather than silently absorbed — the whole point of T4's broken-by-alter flag.
   */
  bankDates: Map<
    string,
    { bankDate: string; clearedAmount: Money; clearedDrCr: string }
  >;
  /**
   * TDS deductor status, keyed by tax_year_start (Item Zero step 2).
   *
   * The mock does not derive TDS at all, so nothing here REFUSES a voucher the
   * way the real post path does. It exists so the F11 panel has somewhere to
   * read and write against — and the gap is worth naming: a screen that looks
   * right against this mock is not evidence the refusal works.
   */
  deductorStatuses: Map<number, DeductorStatus>;
  /**
   * Imported bank statements (T4 slice 2a), keyed by bank ledger id.
   *
   * ⚠️ WHAT THIS MOCK DOES NOT DO, AND THE GAPS MATTER MORE HERE THAN USUAL.
   * The real import is a parser with six named refusals; this is a reader that
   * takes a confirmed mapping at its word. Specifically it does NOT:
   *
   *   - walk the running balances (invariant B-4), so a file with a dropped row
   *     imports cleanly here and is REFUSED by the server;
   *   - deduplicate by transaction (B-3) — only an identical FILE is caught, so
   *     an overlapping statement doubles here and does not on the server;
   *   - number identical transactions by occurrence;
   *   - check the file's account number against the ledger's.
   *
   * So a screen that looks right against this mock is NOT evidence that any of
   * those refusals work. It exists so the import panel has somewhere to read
   * and write against, exactly as `deductorStatuses` does for the F11 panel.
   *
   * ONE THING IT DOES COPY EXACTLY, because a screen test would otherwise pin
   * the wrong behaviour: THE DIRECTION INVERSION. A deposit becomes "D".
   */
  bankStatements: Map<
    string,
    { imports: BankStatementImport[]; lines: BankStatementLine[] }
  >;
  /** s.394(2) declarations, keyed `${buyer}:${section}:${taxYear}`. */
  tcsDeclarations: Map<string, TCSDeclaration>;
  /**
   * TDS challans (T6, migration 0069), keyed by CIN — `${bsr}:${date}:${serial}`.
   *
   * KEYED BY CIN AND NOT BY ID, because the CIN is what UNIQUENESS is written on:
   * the department identifies a payment by (branch, date, serial), so keying by
   * the surrogate id would make the duplicate a mock cannot refuse — and the
   * duplicate is the refusal an operator meets most.
   */
  tdsChallans: Map<string, TDSChallan>;
  /**
   * Which challan paid which deduction, keyed by the allocation triple
   * `${voucher}|${section}|${leg}` -> CIN of the challan that paid it.
   *
   * A DEDUCTION IS PAID ONCE. Deductee Detail field 5 is a single challan
   * reference, so this map is what lets the mock answer 409 with the challan that
   * already paid — the same shape as the server's unique index.
   */
  tdsAllocations: Map<string, string>;
  /**
   * The mock's stand-in for `tds_deduction`. See {@link MockTDSDeduction}: these
   * are FIXTURES, identical in every company, and they are what makes an
   * allocation checkable at all here.
   */
  tdsDeductions: Map<string, MockTDSDeduction>;
  /**
   * The deductor return profile (0070). `undefined` is the ORDINARY state of a
   * company that has not filled the form in, and it is reported as
   * `profile_recorded: false` on a 200 — never a 404.
   */
  tdsProfile?: TDSProfile;
  /**
   * The INVENTORY MASTERS. Held as real rows (not synthesized on read) for the
   * same reason `ledgers` is: the item grid resolves an item's gst_rate_pct and
   * unit from this list, so a mock that invented items on demand would let a
   * screen test pass against a master it never created.
   */
  stockItems: StockItemListItem[];
  godowns: GodownListItem[];
  batches: StockBatchListItem[];
  /**
   * Head VoucherView per logical voucher_id — the GET /vouchers/{id} source.
   * Unlike the day-book DayBookEntry (which keeps ledger NAMEs for display),
   * this RETAINS the real posting ledger_id uuids + persisted GST attributes so
   * the alter-hydration path resends valid uuids (the name-as-uuid fix).
   */
  views: Map<string, VoucherView>;
  /**
   * Raised e-way bills, keyed by voucher_id — mirroring migration 0047's
   * UNIQUE (company_id, voucher_id), so the mock can only hold one per voucher
   * exactly as the table can.
   *
   * `generatedFromVersion` is what makes `stale` derivable here as it is on the
   * server: it is compared to the voucher's CURRENT version_no on every read,
   * never stored as a flag.
   */
  eWayBills: Map<
    string,
    { view: EWayBillResponse; generatedFromVersion: number }
  >;
  /**
   * THE RAISED e-INVOICES, keyed by voucher_id.
   *
   * ⛔ THERE IS NO `generatedFromVersion` HERE AND THAT ASYMMETRY WITH
   * `eWayBills` IS THE WHOLE POINT. An e-way bill can go stale because the
   * invoice under it can still be ALTERED. An e-invoice cannot: raising one
   * FREEZES the voucher, so no later version can exist to be compared against.
   * Staleness is not a state this artefact can reach, and a `stale` field here
   * would be a field that is always false — which reads as "checked, and fine".
   */
  eInvoices: Map<string, EInvoiceRaised>;
  /**
   * Per-(class, fin_year) NEXT free number. Mirrors voucher_number_counter:
   * monotonic, advanced ONLY when WE allocate (empty client voucher_no on a
   * CREATE). Keyed "class:finYear".
   */
  counters: Map<string, number>;
  /**
   * The SaaS admin state, mirroring the server's shape closely enough to drive
   * the screens: every company is created with the two SEEDED levels and its
   * creator as an Owner member, and an invite stays PENDING (it is not a
   * membership) until the invitee claims it.
   */
  members: MemberView[];
  invites: InviteView[];
  levels: SecurityLevelView[];
  /**
   * The EDIT LOG, keyed by logical voucher_id: every create/alter/delete, in
   * order. The rest of the mock RESTATES a voucher in place (that is what makes
   * the derived reports right), so without this log a version history would be
   * unrecoverable — the mock would have to fabricate one, which on an audit-trail
   * screen is the worst possible thing to fake.
   *
   * What it deliberately does NOT do is hash. The chain is BLAKE3 over the
   * canonical payload and only the server computes it, so every entry here
   * carries an empty hash and `getVoucherHistory` returns a verification that
   * SAYS no chain was checked. A mock that answered `ok: true` would assert the
   * one property this feature exists to prove.
   */
  history: Map<string, VoucherVersionView[]>;
}

/** The migration-0015 company profile, mirrored by the mock. */
interface CompanyProfile {
  mailingName: string;
  address: string;
  city: string;
  pincode: string;
  country: string;
  booksBeginningFrom: string;
  currencySymbol: string;
  currencyFormalName: string;
  decimalPlaces: number;
}

/**
 * Server-side defaults from identity.CreateCompany, mirrored exactly so the
 * mock and cored agree on what an omitted field becomes. `booksBeginningFrom`
 * defaults to the company's fy start, never to a hardcoded date.
 */
/**
 * The two seeded primary groups that make a ledger a BANK ledger, mirroring
 * reporting.BankLedgerPredicate. 'Bank OD A/c' is a LIABILITY and still a bank
 * account — the flag says "a statement can be drawn", not "this is an asset".
 */
const BANK_PRIMARY_GROUPS = ["Bank Accounts", "Bank OD A/c"];

/**
 * The six voucher classes that produce a printable document, mirroring the
 * server's printingdb.docKindForClass. The mock refuses the other seven for the
 * same reason it refuses a non-bank ledger: a mock that printed a Contra would
 * let a broken client-side refusal pass its tests.
 */
const MOCK_PRINTABLE_CLASSES = [
  "sales",
  "credit-note",
  "debit-note",
  "receipt",
  "payment",
  "delivery-note",
];

/** Today as ISO yyyy-mm-dd — the server's default `as_on`. */
function todayISO(): string {
  return new Date().toISOString().slice(0, 10);
}

/**
 * Whole days from `from` to `to` (both ISO dates). Used only for the mock's
 * age_days; the REAL age is server-derived, and a client must never recompute it
 * — a clock-skewed browser would show a different age for the same row.
 */
function daysBetween(from: string, to: string): number {
  const a = Date.parse(`${from}T00:00:00Z`);
  const b = Date.parse(`${to}T00:00:00Z`);
  if (Number.isNaN(a) || Number.isNaN(b)) return 0;
  return Math.max(0, Math.round((b - a) / 86_400_000));
}

/**
 * Is this mock ledger a bank ledger?
 *
 * The real server walks an ltree: `g.path <@ bg.path` for either seeded primary
 * group, so a ledger in ANY descendant group qualifies. This mock has no group
 * hierarchy, so it checks the immediate group name OR the declared
 * `bankParentGroup` ancestor — which is what lets the mock represent a bank
 * account in a user-created sub-group, the case a naive group_name filter gets
 * wrong.
 */
function isBankLedger(l: { group: string; bankParentGroup?: string }): boolean {
  return (
    BANK_PRIMARY_GROUPS.includes(l.group) ||
    BANK_PRIMARY_GROUPS.includes(l.bankParentGroup ?? "")
  );
}

// ---------------------------------------------------------------------------
// BANK STATEMENT IMPORT (T4 slice 2a) — mock helpers.
//
// ⚠️ READ THE `bankStatements` COMMENT ON THE COMPANY STATE FIRST. These
// functions are a READER, not the server's parser: no balance walk, no
// transaction-level dedup, no occurrence ordinals, no account check. What they
// DO copy exactly is the direction inversion, because a screen test would
// otherwise pin the wrong behaviour and pass.
// ---------------------------------------------------------------------------

/** The layouts the server reads, for the mock's picker. */
const MOCK_DATE_LAYOUTS = [
  "dd-Mon-yyyy",
  "dd-mm-yyyy",
  "dd/mm/yy",
  "dd/mm/yyyy",
  "mm/dd/yyyy",
  "yyyy-mm-dd",
];

/** Said on every mock import, so nobody mistakes this for the real thing. */
const MOCK_IMPORT_DISCLOSURE =
  "This is the MOCK client: the file was read but NOT balance-walked, NOT " +
  "deduplicated row by row, and NOT checked against this account's number. " +
  "The server does all three and refuses files this accepts.";

function mockEmptyMapping(): StatementColumnMapping {
  return {
    encoding: "",
    date_layout: "",
    value_date: "",
    txn_date: "",
    description: "",
    deposit: "",
    withdrawal: "",
    amount: "",
    direction: "",
    running_balance: "",
    bank_reference: "",
    cheque_number: "",
  };
}

/**
 * Mirrors the server's diagnostic refusal: NAME the ledger rather than hand back
 * an id or an empty statement that reads as "nothing imported".
 */
function mockRequireBankLedger(
  c: { ledgers: { id: string; name: string; group: string; bankParentGroup?: string; bankAccountNumber?: string; bankIFSC?: string }[] },
  ledgerID: string,
) {
  const ledger = c.ledgers.find((l) => l.id === ledgerID);
  if (!ledger) {
    throw new ApiError(400, `reporting: ledger ${ledgerID} not found`);
  }
  if (!isBankLedger(ledger)) {
    throw new ApiError(
      400,
      `reporting: ledger "${ledger.name}" is not under a bank group; only a bank account has a statement to import`,
    );
  }
  return ledger;
}

/** Split a CSV line, honouring double quotes around a field. */
function mockCSVSplit(line: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quoted = false;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i]!;
    if (quoted) {
      if (ch === '"') {
        if (line[i + 1] === '"') {
          cur += '"';
          i++;
        } else quoted = false;
      } else cur += ch;
    } else if (ch === '"') quoted = true;
    else if (ch === ",") {
      out.push(cur.trim());
      cur = "";
    } else cur += ch;
  }
  out.push(cur.trim());
  return out;
}

function mockCSVRows(content: string): string[][] {
  return content
    .split(/\r?\n/)
    .filter((l) => l.trim() !== "")
    .map(mockCSVSplit);
}

function mockCSVHeader(content: string): string[] {
  return mockCSVRows(content)[0] ?? [];
}

/** "1,25,000.00" -> 1250000000n (ten-thousandths). NEVER parseFloat. */
function mockMoneyToUnits(v: Money | string): bigint {
  const cleaned = (v || "0").replace(/[,\s₹]/g, "");
  const neg = cleaned.startsWith("-") || /^\(.*\)$/.test(cleaned);
  const bare = cleaned.replace(/^[-(]|\)$/g, "").replace(/(CR|DR)$/i, "");
  const [whole = "0", frac = ""] = bare.split(".");
  const units = BigInt((whole || "0") + frac.padEnd(4, "0").slice(0, 4));
  return neg ? -units : units;
}

function mockUnitsToMoney(u: bigint): Money {
  const neg = u < 0n;
  const abs = neg ? -u : u;
  const whole = abs / 10000n;
  const frac = (abs % 10000n).toString().padStart(4, "0");
  return `${neg ? "-" : ""}${whole}.${frac}`;
}

/** Read a date cell under a NAMED layout, to ISO. Unreadable -> a 400. */
function mockParseDate(raw: string, layout: string): string {
  const digits = raw.split(/[/\-.]/).map((p) => p.trim());
  const months = [
    "jan", "feb", "mar", "apr", "may", "jun",
    "jul", "aug", "sep", "oct", "nov", "dec",
  ];
  const pad = (n: string) => n.padStart(2, "0");
  const fail = () => {
    throw new ApiError(
      400,
      `bankstatement: date does not match the declared layout: ${raw} is not ${layout}`,
    );
  };
  if (digits.length !== 3) fail();
  const [a = "", b = "", c = ""] = digits;
  switch (layout) {
    case "yyyy-mm-dd":
      return `${a}-${pad(b)}-${pad(c)}`;
    case "dd/mm/yyyy":
    case "dd-mm-yyyy":
      return `${c}-${pad(b)}-${pad(a)}`;
    case "mm/dd/yyyy":
      return `${c}-${pad(a)}-${pad(b)}`;
    case "dd/mm/yy":
      return `20${pad(c)}-${pad(b)}-${pad(a)}`;
    case "dd-Mon-yyyy": {
      const m = months.indexOf(b.toLowerCase().slice(0, 3));
      if (m < 0) fail();
      return `${c}-${pad(String(m + 1))}-${pad(a)}`;
    }
    default:
      return fail() as never;
  }
}

/**
 * Read the rows under a CONFIRMED mapping.
 *
 * ⚠️ THE DIRECTION INVERSION IS COPIED EXACTLY FROM THE SERVER and is the one
 * thing in this file that must not drift: a DEPOSIT becomes "D", because in our
 * books the bank asset rose, though the bank's own statement calls it a credit.
 */
function mockParseStatement(
  content: string,
  m: StatementColumnMapping,
  importID: string,
): BankStatementLine[] {
  const rows = mockCSVRows(content);
  const header = rows[0] ?? [];
  const at = (name: string) => header.indexOf(name);
  const cell = (r: string[], name: string) =>
    name === "" ? "" : (r[at(name)] ?? "").trim();

  const out: BankStatementLine[] = [];
  rows.slice(1).forEach((r, i) => {
    let amount = "0";
    let drCr: DrCr = "D";
    if (m.encoding === "two-columns") {
      const dep = cell(r, m.deposit);
      const wdr = cell(r, m.withdrawal);
      const depEmpty = dep === "" || mockMoneyToUnits(dep) === 0n;
      if (!depEmpty) {
        amount = dep;
        drCr = "D"; // a DEPOSIT is a DEBIT in our books
      } else {
        amount = wdr;
        drCr = "C";
      }
    } else if (m.encoding === "signed-amount") {
      const raw = cell(r, m.amount);
      amount = raw;
      drCr = mockMoneyToUnits(raw) < 0n ? "C" : "D";
    } else {
      const raw = cell(r, m.amount);
      const flag = cell(r, m.direction).toUpperCase();
      amount = raw;
      // The bank's C (a credit to us) is OUR debit.
      drCr = flag.startsWith("C") ? "D" : "C";
    }
    const units = mockMoneyToUnits(amount);
    const bal = cell(r, m.running_balance);
    const line: BankStatementLine = {
      id: `${importID}-line-${i + 1}`,
      import_id: importID,
      line_no: i + 2,
      value_date: mockParseDate(cell(r, m.value_date), m.date_layout),
      txn_date:
        m.txn_date && cell(r, m.txn_date)
          ? mockParseDate(cell(r, m.txn_date), m.date_layout)
          : "",
      description: cell(r, m.description),
      amount: mockUnitsToMoney(units < 0n ? -units : units),
      dr_cr: drCr,
      bank_reference: cell(r, m.bank_reference),
      cheque_number: cell(r, m.cheque_number),
    };
    if (bal !== "") line.running_balance = mockUnitsToMoney(mockMoneyToUnits(bal));
    out.push(line);
  });
  return out;
}

function defaultProfile(fyStartDate: string): CompanyProfile {
  return {
    mailingName: "",
    address: "",
    city: "",
    pincode: "",
    country: "India",
    booksBeginningFrom: fyStartDate,
    currencySymbol: "₹",
    currencyFormalName: "Indian Rupees",
    decimalPlaces: 2,
  };
}

/** The single identity the mock authenticates as (mirrors one dev subject). */
const MOCK_USER_ID = "mock-user-0001";
const MOCK_SUBJECT = "browser-dev-owner";

/**
 * The two levels seed_company_auth_defaults creates with every company. Rules
 * are abridged to the shape the screens render (effect/action/resource); the
 * PDP itself is server-side, so the mock never evaluates them.
 */
function seededLevels(companyID: string): SecurityLevelView[] {
  return [
    {
      id: `${companyID}:owner`,
      name: "Owner",
      is_owner: true,
      default_access: "full",
      requires_webauthn: true,
      backdated_cutoff: "",
      member_count: 1,
      rules: [],
    },
    {
      id: `${companyID}:data-entry`,
      name: "Data Entry",
      is_owner: false,
      default_access: "none",
      requires_webauthn: false,
      backdated_cutoff: "",
      member_count: 0,
      rules: [
        { effect: "allow", action: "create", resource: "sales" },
        { effect: "allow", action: "display", resource: "report" },
      ],
    },
  ];
}

/**
 * The closed rule vocabulary, mirroring internal/authz/vocabulary.go.
 *
 * THE MOCK'S COPY IS NOT THE PRODUCT'S SOURCE OF TRUTH — the live client fetches
 * it from GET .../security-levels/vocabulary precisely so no TypeScript copy can
 * drift from the PDP. This one exists only so hermetic component tests have
 * something to render, and it is the mock's job to be a faithful stand-in.
 * If it ever disagrees with the server, the LIVE gates (`verify:tenancy`,
 * `e2e:ui`) are authoritative — a fake that models a promise the real store
 * breaks is exactly how this project shipped a lockout bug before.
 */
const MOCK_VOCABULARY: SecurityVocabulary = {
  effects: ["allow", "disallow"],
  actions: ["create", "alter", "delete", "display", "print", "export", "*"],
  resources: [
    "master",
    "voucher",
    "report",
    "company",
    "import",
    "sync",
    "query",
    "security",
    "sales",
    "purchase",
    "receipt",
    "payment",
    "contra",
    "journal",
    "credit-note",
    "debit-note",
    "report:trial-balance",
    "report:balance-sheet",
    "report:profit-and-loss",
    "report:day-book",
    "report:outstandings",
    "report:cash-flow",
    "report:anomalies",
    "report:gstr1",
    "report:gstr3b",
    "report:reconciliation-queue",
    "*",
  ],
  default_accesses: ["full", "none"],
};

/** How many Owner-level members a company has (the last-Owner guard's input). */
function ownerCount(c: MockCompany): number {
  return c.members.filter((m) => m.is_owner).length;
}

/** Re-derive each level's member_count after a membership change. */
function recount(c: MockCompany): void {
  for (const l of c.levels) {
    l.member_count = c.members.filter(
      (m) => m.security_level_id === l.id,
    ).length;
  }
}

/**
 * Resolve a level by id OR name, mirroring the server's resolveLevel: supplying
 * both is a 400 (the server refuses to pick one), supplying neither likewise.
 */
function resolveLevel(
  c: MockCompany,
  levelID?: string,
  levelName?: string,
): SecurityLevelView {
  if (levelID && levelName) {
    throw new ApiError(
      400,
      "supply security_level_id or security_level_name, not both",
      "supply security_level_id or security_level_name, not both",
    );
  }
  if (!levelID && !levelName) {
    throw new ApiError(
      400,
      "security_level_id or security_level_name is required",
      "security_level_id or security_level_name is required",
    );
  }
  const level = levelID
    ? c.levels.find((l) => l.id === levelID)
    : c.levels.find((l) => l.name === levelName);
  if (!level) {
    throw new ApiError(
      404,
      "security level not found",
      "security level not found",
    );
  }
  return level;
}

/** 422 on a token outside the closed vocabulary, exactly as parseRules does. */
function validateRule(r: SecurityLevelRule): SecurityLevelRule {
  const effect = r.effect.trim().toLowerCase();
  const action = r.action.trim().toLowerCase();
  const resource = r.resource.trim().toLowerCase();
  if (!MOCK_VOCABULARY.effects.includes(effect)) {
    throw new ApiError(
      422,
      `invalid effect "${r.effect}": want allow or disallow`,
      `invalid effect "${r.effect}"`,
    );
  }
  if (!MOCK_VOCABULARY.actions.includes(action)) {
    throw new ApiError(
      422,
      `unknown action "${r.action}"`,
      `unknown action "${r.action}"`,
    );
  }
  if (!MOCK_VOCABULARY.resources.includes(resource)) {
    throw new ApiError(
      422,
      `unknown resource "${r.resource}"`,
      `unknown resource "${r.resource}"`,
    );
  }
  return { effect, action, resource };
}

function validateDefaultAccess(da: string): void {
  if (!MOCK_VOCABULARY.default_accesses.includes(da)) {
    throw new ApiError(
      422,
      "default_access must be full or none",
      "default_access must be full or none",
    );
  }
}

export interface MockClientOptions {
  /** Seed a demo company so reports are non-empty. Default true. */
  seed?: boolean;
}

/** Construct a fresh in-memory CoredClient. State is isolated per instance. */
// ---------------------------------------------------------------------------
// T2 cost-centre fixtures. TWO AXES over ONE posting — see the note on
// listCostCategories for why a single-axis fixture cannot express the model.
// ---------------------------------------------------------------------------
function mockCostCategories(): CostCategory[] {
  return [
    {
      id: "cost-cat-department",
      name: "Department",
      allocate_non_revenue: false,
      centre_count: 2,
      created_by: "seed",
      created_at: "2026-04-01T00:00:00Z",
    },
    {
      id: "cost-cat-branch",
      name: "Branch",
      allocate_non_revenue: false,
      centre_count: 2,
      created_by: "seed",
      created_at: "2026-04-01T00:00:00Z",
    },
  ];
}

function mockCostCentres(): CostCentre[] {
  const on = (
    id: string,
    name: string,
    axisID: string,
    axisName: string,
  ): CostCentre => ({
    id,
    name,
    cost_category_id: axisID,
    cost_category_name: axisName,
    parent_id: "",
    parent_name: "",
    depth: 0,
    path: name,
    ancestry_truncated: false,
    created_by: "seed",
    created_at: "2026-04-01T00:00:00Z",
  });
  return [
    on("cost-ctr-sales", "Sales", "cost-cat-department", "Department"),
    on("cost-ctr-admin", "Admin", "cost-cat-department", "Department"),
    // NOTE "Mumbai" is a Branch here. The same name is legal on another axis —
    // cost_centre_name_unique is per (company, CATEGORY, name).
    on("cost-ctr-mumbai", "Mumbai", "cost-cat-branch", "Branch"),
    on("cost-ctr-delhi", "Delhi", "cost-cat-branch", "Branch"),
  ];
}

// Departments 60/40 and Branches 70/30 — each axis totalling the SAME 1,00,000.
function mockAllocationFor(centreID: string): Money {
  switch (centreID) {
    case "cost-ctr-sales":
      return "60000.0000";
    case "cost-ctr-admin":
      return "40000.0000";
    case "cost-ctr-mumbai":
      return "70000.0000";
    case "cost-ctr-delhi":
      return "30000.0000";
    default:
      return "0.0000";
  }
}

export function createMockClient(opts: MockClientOptions = {}): CoredClient {
  const companies = new Map<string, MockCompany>();

  // =========================================================================
  // T3 mock state and helpers
  // =========================================================================
  // MockOrder is an OrderView whose closure fields are mutable, because closing is
  // the one thing about an order a caller can change. Everything else on an order
  // is immutable here: the mock has no revise-line route because the server's
  // ReviseLineQty is not exposed over HTTP yet, and inventing one would let a screen
  // be built against a route that does not exist.
  type MockOrder = Omit<OrderView, "is_closed" | "closed_reason"> & {
    is_closed: boolean;
    closed_reason: string;
  };

  // Module-scoped arrays, matching how the rest of this file holds mutable mock
  // state. ⚠️ THE FIXTURE STARTS EMPTY, deliberately: an order book that came
  // pre-populated would let a screen render rows it never fetched, and the FIRST
  // thing a T3 screen has to get right is the empty state — "nothing outstanding"
  // is a real and common answer that reads very differently from "not loaded".
  const mockOrders: MockOrder[] = [];
  const mockBoms: BomView[] = [];

  /** fixed4 is how the server sends every quantity and rate. A JSON number is a
   * float64 and a commitment is not a thing to send through one. */
  function fixed4(v: string): string {
    const n = Number(v);
    return Number.isFinite(n) ? n.toFixed(4) : "0.0000";
  }

  // ⛔ EVERY ONE OF THESE HELPERS IS `async`, AND THAT IS NOT A STYLE CHOICE.
  //
  // A method typed as returning a Promise that THROWS SYNCHRONOUSLY does not
  // reject — the exception escapes before a promise exists, so `client.create(...)
  // .catch(h)` never runs `h` and the error surfaces as an unhandled throw at the
  // call site. The real server always answers with a rejected promise, so a
  // synchronous throw here is a behavioural difference between the mock and the
  // wire, of exactly the kind this file's own test suite exists to catch. It DID
  // catch it: the first run of orders.test.ts failed with the ApiError escaping
  // `expect(...).rejects`, which is the symptom a screen would see as a crash
  // rather than a handled 422.
  async function mockCreateOrder(
    companyID: string,
    direction: OrderDirection,
    req: CreateOrderRequest,
  ): Promise<{ order_id: string }> {
    const c = ensure(companyID);
    const no = (req.order_no ?? "").trim();
    if (no === "") {
      throw new ApiError(422, "orders: an order needs a number");
    }
    if (!req.order_date) {
      throw new ApiError(422, "orders: an order needs a date");
    }
    if (!req.party_ledger_id) {
      throw new ApiError(
        422,
        `orders: a ${direction.toLowerCase()} order needs a party ledger — without one the order book cannot group by counterparty`,
      );
    }
    if (!req.lines || req.lines.length === 0) {
      throw new ApiError(
        422,
        "orders: an order with no lines commits nothing — there is no quantity to be outstanding",
      );
    }
    for (const l of req.lines) {
      if (!l.stock_item_id) {
        throw new ApiError(422, "orders: every line must name a stock item");
      }
      if (!l.qty || Number(l.qty) <= 0) {
        throw new ApiError(
          422,
          "orders: the committed quantity must be more than zero — a commitment is a MAGNITUDE and the order's direction carries the sense",
        );
      }
    }
    // ⛔ UNIQUE PER DIRECTION, CASE-INSENSITIVELY — and a sales and a purchase order
    // MAY share a number, which is why the filter includes the direction. Matching
    // company-wide would refuse a legitimate PO-1 beside SO-1.
    if (
      mockOrders.some(
        (o) =>
          o.direction === direction &&
          o.order_no.trim().toLowerCase() === no.toLowerCase(),
      )
    ) {
      throw new ApiError(
        409,
        `orders: this company already has a ${direction.toLowerCase()} order numbered "${no}" — order numbers are unique per direction and are matched case-insensitively`,
      );
    }
    const orderID = id("order");
    mockOrders.push({
      order_id: orderID,
      direction,
      order_no: no,
      order_date: req.order_date,
      party_ledger_id: req.party_ledger_id,
      // ⛔ THE DISPLAY NAME, RESOLVED — NOT THE ID AGAIN.
      //
      // This returned `req.party_ledger_id` (and `stock_item` returned the item id)
      // until a screen was actually built against it, and it is the T2 defect
      // exactly: a SHAPE the mock got wrong, which no type can catch because both
      // fields are `string`. The server LEFT JOINs the master and returns
      // COALESCE(name, '') for each — internal/orders/bom.go:242 and the order
      // queries beside it — so a screen built against the id-returning mock renders
      // a UUID where the operator expects "Acme Traders", every test stays green,
      // and the defect only appears in a browser.
      //
      // ⚠️ AN UNRESOLVABLE ID READS BACK AS "", matching the server's COALESCE
      // rather than falling back to the id. The empty string is the honest answer —
      // it says the join found nothing — and a fallback to the id would put the UUID
      // back on the screen in exactly the case the operator can least explain.
      party: c.ledgers.find((l) => l.id === req.party_ledger_id)?.name ?? "",
      narration: req.narration ?? "",
      is_closed: false,
      closed_reason: "",
      lines: req.lines.map((l, i) => ({
        order_line_id: id("order-line"),
        line_no: i + 1,
        stock_item_id: l.stock_item_id,
        stock_item:
          c.stockItems.find((s) => s.id === l.stock_item_id)?.name ?? "",
        qty: fixed4(l.qty),
        rate: fixed4(l.rate ?? "0"),
        due_date: l.due_date,
        // DERIVED on the server and zero on a fresh order. The mock keeps the pair
        // rather than one figure, because a screen needs both: 5 outstanding reads
        // completely differently against a commitment of 6 than against 500.
        fulfilled_qty: "0.0000",
        outstanding_qty: fixed4(l.qty),
      })),
    });
    return { order_id: orderID };
  }

  /** mockFindOrder enforces the DIRECTION GUARD, which is the single most important
   * behaviour in this block.
   *
   * ⛔ A 404 AND NOT A 403. Both directions live in one table on the server and
   * routeAction admits a caller to /sales-orders/{any-id}, so the handler refuses an
   * id of the other direction — otherwise a caller holding only `sales-order` reads
   * the company's purchase commitments. "Forbidden" would confirm the id names
   * something real; "not found" is the honest answer for this path. A mock that
   * served it would let a screen be built against a leak. */
  function mockFindOrder(direction: OrderDirection, orderID: string): MockOrder {
    const o = mockOrders.find((x) => x.order_id === orderID);
    if (!o || o.direction !== direction) {
      throw new ApiError(404, `no such ${direction.toLowerCase()} order`);
    }
    return o;
  }

  /**
   * ONE ORDER WITH ITS FULFILMENT DERIVED FRESH, which is the only correct way to
   * hand an order to a caller.
   *
   * ⛔ `fulfilled_qty` AND `outstanding_qty` ARE COMPUTED HERE ON EVERY READ, not
   * stored on the line. The stored fields exist only as the shape the rest of this
   * mock was written against; they are OVERWRITTEN by this function and no caller
   * should ever see them. That is the server's behaviour and migration 0042's header
   * gives the reason: a stored figure goes stale undetectably the first time a
   * fulfilling voucher is ALTERED, because an alter writes a NEW version and re-states
   * its links without ever visiting the order.
   *
   * The sum runs over `c.views`, which holds only the CURRENT version of each voucher
   * — the mock's equivalent of the `voucher_head.current_version_id` join every reader
   * of order_fulfilment owes. Without it, a delivery corrected from 5 to 8 counts as
   * 13.
   */
  function orderWithDerivedFulfilment(c: MockCompany, o: MockOrder): OrderView {
    return {
      ...o,
      lines: o.lines.map((l) => {
        const done = fulfilledFor(c, l.order_line_id, null);
        const ordered = Number.parseFloat(l.qty);
        return {
          ...l,
          fulfilled_qty: done.toFixed(4),
          // Never negative: over-fulfilment is refused on the way in, so the clamp is
          // belt-and-braces on a figure an operator reads rather than a licence to
          // accept a link that would need it.
          outstanding_qty: Math.max(0, ordered - done).toFixed(4),
        };
      }),
    };
  }

  async function mockListOrders(
    companyID: string,
    direction: OrderDirection,
    openOnly?: boolean,
  ): Promise<OrderListResponse> {
    const c = ensure(companyID);
    const rows = mockOrders.filter((o) => {
      if (o.direction !== direction) return false;
      if (!openOnly) return true;
      // A closed order is not outstanding however little was delivered, and neither
      // is a fully delivered one. DERIVED, not read off the line — see
      // orderWithDerivedFulfilment.
      return (
        !o.is_closed &&
        orderWithDerivedFulfilment(c, o).lines.some(
          (l) => Number(l.outstanding_qty) > 0,
        )
      );
    });
    return {
      direction,
      orders: rows.map(({ lines: _lines, ...head }) => head),
      open_only: openOnly ?? false,
    };
  }

  async function mockGetOrder(
    companyID: string,
    direction: OrderDirection,
    orderID: string,
  ): Promise<OrderView> {
    const c = ensure(companyID);
    // DERIVED on every read, never the stored pair — see orderWithDerivedFulfilment.
    return orderWithDerivedFulfilment(c, mockFindOrder(direction, orderID));
  }

  async function mockCloseOrder(
    companyID: string,
    direction: OrderDirection,
    orderID: string,
    req: CloseOrderRequest,
  ): Promise<{ order_id: string; is_closed: boolean }> {
    ensure(companyID);
    const o = mockFindOrder(direction, orderID);
    const reason = (req.reason ?? "").trim();
    if (req.is_closed && reason === "") {
      // ⛔ The server refuses this (0042 CHECKs it): an abandoned commitment with no
      // stated cause is the row an auditor asks about, and it cannot be told apart
      // from a mistake.
      throw new ApiError(
        422,
        "orders: closing an order needs a reason — an abandoned commitment with no stated cause cannot be told apart from a mistake",
      );
    }
    o.is_closed = req.is_closed;
    // Cleared TOGETHER with the flag, because the server refuses a reason on an open
    // order: a reason attached to something that has not happened is a claim about
    // nothing.
    o.closed_reason = req.is_closed ? reason : "";
    return { order_id: orderID, is_closed: o.is_closed };
  }

  async function mockOrderBook(
    companyID: string,
    direction: OrderDirection,
    asOn?: string,
  ): Promise<OrderBookResponse> {
    const c = ensure(companyID);
    const rows: OrderBookRow[] = [];
    let total = 0;
    for (const stored of mockOrders) {
      if (stored.direction !== direction || stored.is_closed) continue;
      // DERIVED, so the book moves when a delivery is linked and moves BACK when that
      // delivery is altered — which is the whole behaviour the report exists for.
      const o = orderWithDerivedFulfilment(c, stored);
      for (const l of o.lines) {
        const out = Number(l.outstanding_qty);
        // A line with nothing outstanding is EXCLUDED, matching the server: an order
        // book is a list of what is still owed, and a delivered line on it is a row
        // an operator has to read and dismiss.
        if (out <= 0) continue;
        const value = out * Number(l.rate);
        total += value;
        rows.push({
          order_id: o.order_id,
          order_no: o.order_no,
          order_date: o.order_date,
          party: o.party,
          order_line_id: l.order_line_id,
          stock_item: l.stock_item,
          qty: l.qty,
          fulfilled_qty: l.fulfilled_qty,
          outstanding_qty: l.outstanding_qty,
          rate: l.rate,
          outstanding_value: value.toFixed(4),
          due_date: l.due_date ?? "",
          // ⚠️ A LINE WITH NO AGREED DUE DATE IS NEVER OVERDUE, however old.
          // Treating an absent date as today's would invent a commitment the
          // business never made.
          is_overdue: Boolean(l.due_date && asOn && l.due_date < asOn),
        });
      }
    }
    return {
      direction,
      as_on: asOn ?? "",
      rows,
      total_outstanding_value: total.toFixed(4),
      notes: [
        "Outstanding quantities and value at the ORDER's agreed rate. An order is a committed quantity and not a movement, so nothing on this report affects closing stock, the Profit & Loss or the Balance Sheet.",
      ],
    };
  }

  function ensure(companyID: string): MockCompany {
    let c = companies.get(companyID);
    if (!c) {
      c = {
        id: companyID,
        name: "Mock Co",
        // Unknown until createCompany/getCompany says otherwise. "" mirrors the
        // column default, and makes deriveGst fall through to inter-state (IGST)
        // exactly as cored would for a company with no registered state.
        stateCode: "",
        gstin: "",
        fyStartDate: "2026-04-01",
        baseCurrency: "INR",
        profile: defaultProfile("2026-04-01"),
        ledgers: [],
        // Every company is seeded with exactly one system location, mirroring
        // migration 0025's seed_inventory_defaults. Without it the item grid's
        // godown picker would be empty on a fresh company and every line would
        // post untracked — a silent downgrade of a feature that IS available.
        godowns: [mainLocation(companyID)],
        stockItems: [],
        batches: [],
        vouchers: [],
        bankDates: new Map(),
        deductorStatuses: new Map(),
        bankStatements: new Map(),
        tcsDeclarations: new Map(),
        tdsChallans: new Map(),
        tdsAllocations: new Map(),
        // The fixture deductions are seeded into EVERY company, not just the demo
        // one, so there is no company in which an allocation is unrefusable.
        tdsDeductions: mockDeductionFixtures(),
        views: new Map(),
        eWayBills: new Map(),
        eInvoices: new Map(),
        members: [
          {
            user_id: MOCK_USER_ID,
            subject: MOCK_SUBJECT,
            email: "",
            display_name: "",
            security_level_id: `${companyID}:owner`,
            security_level_name: "Owner",
            is_owner: true,
            is_self: true,
            created_at: "2026-04-01T00:00:00Z",
          },
        ],
        invites: [],
        levels: seededLevels(companyID),
        history: new Map(),
        counters: new Map(),
      };
      companies.set(companyID, c);
    }
    return c;
  }

  if (opts.seed !== false) {
    const demoID = "company-demo";
    const demo: MockCompany = {
      id: demoID,
      name: "Demo Traders",
      // The seeded demo tenant is a Karnataka company, matching the golden
      // corpora / e2e fixtures (party "Acme Traders" is also state 29).
      stateCode: "29",
      gstin: "",
      fyStartDate: "2026-04-01",
      baseCurrency: "INR",
      profile: defaultProfile("2026-04-01"),
      ledgers: [
        { id: "ledger-cash", name: "Cash", group: "Cash-in-Hand" },
        { id: "ledger-sales", name: "Sales", group: "Sales Accounts" },
        // A BANK ledger, so the demo company can actually exercise bank
        // reconciliation (T4). Without one the reconciliation screen has nothing
        // to offer and the picker's empty state would be the only path a
        // developer ever saw.
        //
        // It sits in a SUB-GROUP of "Bank Accounts" on purpose: that is the exact
        // shape that made a client-side group_name filter wrong (a real bank
        // account whose immediate group is neither seeded name), so the default
        // demo data exercises the case rather than the easy one.
        {
          id: "ledger-hdfc",
          name: "HDFC Bank",
          group: "Current Accounts",
          bankParentGroup: "Bank Accounts",
        },
        {
          id: "ledger-acme",
          name: "Acme Traders",
          group: "Sundry Debtors",
          stateCode: "29",
        },
      ],
      godowns: [mainLocation(demoID)],
      // Two demo items with DIFFERENT GST rates, so an item invoice keyed
      // against the seeded company exercises the multi-rate grouping rather
      // than the single-rate happy path.
      stockItems: [
        {
          id: "stock-widget",
          name: "Widget",
          unit_symbol: "Nos",
          hsn: "8481",
          gst_rate_pct: "18.0000",
          valuation_method: "WEIGHTED_AVG",
          is_system: false,
        },
        {
          id: "stock-gasket",
          name: "Gasket",
          unit_symbol: "Nos",
          hsn: "4016",
          gst_rate_pct: "12.0000",
          valuation_method: "WEIGHTED_AVG",
          is_system: false,
        },
      ],
      batches: [],
      vouchers: [],
      bankDates: new Map(),
      deductorStatuses: new Map(),
      bankStatements: new Map(),
      tcsDeclarations: new Map(),
      tdsChallans: new Map(),
      tdsAllocations: new Map(),
      tdsDeductions: mockDeductionFixtures(),
      views: new Map(),
      eWayBills: new Map(),
      eInvoices: new Map(),
      counters: new Map(),
      // The seeded demo tenant carries the same admin defaults every company
      // gets: its two seeded levels, and the mock identity as its Owner.
      members: [
        {
          user_id: MOCK_USER_ID,
          subject: MOCK_SUBJECT,
          email: "",
          display_name: "",
          security_level_id: `${demoID}:owner`,
          security_level_name: "Owner",
          is_owner: true,
          is_self: true,
          created_at: "2026-04-01T00:00:00Z",
        },
      ],
      invites: [],
      levels: seededLevels(demoID),
      history: new Map(),
    };
    companies.set(demoID, demo);
  }

  /**
   * Map a request's postings into day-book postings (NAMES, for display) +
   * Dr/Cr totals AND a parallel set of REAL-ledger_id PostingViews (for the
   * GET /vouchers/{id} hydration path). Canonicalize dr_cr to "D"/"C".
   */
  function summarize(
    c: MockCompany,
    req: VoucherRequest,
  ): {
    postings: DayBookPosting[];
    postingViews: PostingView[];
    debit: Money;
    credit: Money;
  } {
    let debit: Money = "0.0000";
    let credit: Money = "0.0000";
    const postings: DayBookPosting[] = [];
    const postingViews: PostingView[] = [];
    for (const p of req.postings ?? []) {
      // Accept both the wire ("D"/"C") and domain ("DR"/"CR") conventions;
      // canonicalize to single-letter "D"/"C" as the live GET does.
      const dc = p.dr_cr.trim().toUpperCase().startsWith("D") ? "D" : "C";
      if (dc === "D") debit = addMoney(debit, p.amount);
      else credit = addMoney(credit, p.amount);
      const ledger = c.ledgers.find((l) => l.id === p.ledger_id);
      const amount = parseFloat(p.amount || "0").toFixed(4);
      postings.push({ ledger: ledger?.name ?? p.ledger_id, dr_cr: dc, amount });
      postingViews.push({
        // RETAIN the real ledger_id uuid (the day book maps to NAME above; the
        // view keeps the id so getVoucher round-trips real ids).
        ledger_id: p.ledger_id,
        ledger: ledger?.name ?? p.ledger_id,
        dr_cr: dc,
        amount,
      });
    }
    return { postings, postingViews, debit, credit };
  }

  /**
   * Echo the server-derived GST split for a posted voucher: when the request
   * carries a `gst` block with a single line, mirror the canonical engine via
   * the shared {@link deriveGstSplit} helper (intra: CGST+SGST; inter: IGST),
   * else all zeros. Keeps the mock aligned with the live server so component
   * tests can assert a non-zero split without a backend.
   *
   * SUPPLIER-STATE DEFAULT (mirrors internal/ledger/ledger.go): when the posted
   * `gst` block omits `supplier_state_code` (or sends ""), cored fills it from
   * `company.state_code` INSIDE the post transaction, before deriving. The mock
   * does the same from {@link MockCompany.stateCode}; otherwise a draft-accept
   * or state-hydrated post would derive IGST here and CGST+SGST on the real
   * server, and the mock would be a false oracle.
   */
  function deriveGst(
    c: MockCompany,
    req: VoucherRequest,
  ): {
    taxable: Money;
    cgst: Money;
    sgst: Money;
    igst: Money;
  } {
    const gst = req.gst;
    if (!gst) {
      return {
        taxable: "0.0000",
        cgst: "0.0000",
        sgst: "0.0000",
        igst: "0.0000",
      };
    }
    const supplierState =
      (gst.supplier_state_code ?? "").trim() !== ""
        ? (gst.supplier_state_code as string)
        : c.stateCode;
    const pos = gst.place_of_supply_state_code ?? "";

    // Sum over EVERY gst line, not just the first. A hand-keyed accounting
    // invoice has exactly one, but an ITEM INVOICE has one per (rate, HSN)
    // group — and reading only lines[0] would report the tax on the first
    // group as the tax on the whole invoice, which for a mixed-rate voucher is
    // a plausible-looking understatement.
    let taxable: Money = "0.0000";
    let cgst: Money = "0.0000";
    let sgst: Money = "0.0000";
    let igst: Money = "0.0000";
    for (const line of gst.lines ?? []) {
      const split = deriveGstSplit(
        line.taxable_value,
        line.gst_rate_pct,
        supplierState,
        pos,
      );
      taxable = addMoney(taxable, line.taxable_value);
      cgst = addMoney(cgst, split.cgst);
      sgst = addMoney(sgst, split.sgst);
      igst = addMoney(igst, split.igst);
    }
    return { taxable, cgst, sgst, igst };
  }

  /**
   * The PER-LINE GST detail as GET /vouchers/{id} returns it — the mock's mirror
   * of `voucher_gst_line` (migration 0016) and of voucherFullJSON's `gst_lines`.
   *
   * ⛔⛔ IT IS RE-DERIVED FROM THE REQUEST ON EVERY WRITE, AND THAT IS THE POINT.
   * The rows key on voucher_version_id server-side, so an ALTER RE-STATES them:
   * whatever the body carries becomes the current version's lines and whatever it
   * omits is GONE. A mock that carried the previous version's lines forward would
   * be MORE FORGIVING THAN THE SERVER (§9.4's first kind) and would hide the exact
   * defect this projection exists to close — that a form rebuilt from a GET which
   * had no rate on it re-derived the tax at 0% and silently stripped an invoice of
   * its GST.
   *
   * Derived through the SAME {@link deriveGstSplit} the aggregate above uses, so
   * the per-line amounts and the voucher total can never disagree here in a way
   * they could not disagree on the server.
   */
  function gstLineViews(c: MockCompany, req: VoucherRequest): GSTLineView[] {
    const gst = req.gst;
    if (!gst) return [];
    const supplierState =
      (gst.supplier_state_code ?? "").trim() !== ""
        ? (gst.supplier_state_code as string)
        : c.stateCode;
    const pos = gst.place_of_supply_state_code ?? "";
    return (gst.lines ?? []).map((line, i) => {
      const split = deriveGstSplit(
        line.taxable_value,
        line.gst_rate_pct,
        supplierState,
        pos,
      );
      return {
        // 1-BASED and STORED, matching the server's line_no column.
        line_no: i + 1,
        hsn: line.hsn ?? "",
        // Fixed-4 through the shared string adder, never a float format: the
        // server hands these out as StringFixed(4) and a client that echoes them
        // straight back into `gst.lines` must be echoing the same characters.
        taxable_value: addMoney("0.0000", line.taxable_value),
        gst_rate_pct: addMoney("0.0000", line.gst_rate_pct),
        cgst: split.cgst,
        sgst: split.sgst,
        igst: split.igst,
      };
    });
  }

  /**
   * THE ITEM INVOICE, GENERATED — the mock's mirror of
   * internal/ledger/item_invoice.go.
   *
   * When a GST-bearing voucher carries stock lines AND a `gst` block, the
   * SERVER generates the GST lines and every posting from the goods. A mock
   * that just echoed whatever the client sent would be a false oracle for the
   * one property this whole feature exists to guarantee: that the books and the
   * warehouse cannot tell different stories. So this reproduces the real thing,
   * refusals included —
   *
   *   - `gst.lines` or `postings` alongside inventory: 422, NOT a merge.
   *     Choosing which to believe is how 100 units of stock get billed as a
   *     Rs 1 invoice with every layer reporting success.
   *   - a line moving the wrong way for the class: 422. A sale that ADDED stock
   *     would inflate the P&L and the closing inventory at once.
   *   - reverse charge: 422 (the self-assessed legs are not generated).
   *   - a line `amount` that contradicts qty x rate less discount: 422, never a
   *     silent correction — the printed invoice would then disagree with the
   *     stored one and neither party would know which is real.
   *
   * HSN and the tax RATE come from the item MASTER, never from the request:
   * they are the tax identity of the goods, and identity belongs to the item,
   * not to the document that mentions it.
   *
   * Returns the request REWRITTEN with the generated gst.lines + postings, or
   * null when this is not an item invoice (the ordinary path is untouched).
   */
  function generateItemInvoice(
    c: MockCompany,
    cls: VoucherClass,
    req: VoucherRequest,
  ): VoucherRequest | null {
    const orient = ITEM_INVOICE_ORIENTATION[cls];
    if (!orient || !req.inventory?.length || !req.gst) return null;

    const refuse = (msg: string): never => {
      throw new ApiError(422, msg, { error: msg });
    };
    if (req.gst.lines?.length) {
      refuse(
        "an item invoice derives its GST lines from the stock lines; send inventory OR gst.lines, not both",
      );
    }
    if (req.postings?.length) {
      refuse(
        "an item invoice derives its postings from the stock lines; do not send postings with inventory + gst",
      );
    }
    if (!req.party_ledger_id)
      refuse("an item invoice needs a party ledger to bill");
    if (!req.item_ledger_id) {
      refuse(
        "an item invoice needs an item ledger (sales/purchase account) to book the goods to",
      );
    }
    if (req.gst.reverse_charge) {
      refuse("reverse-charge item invoices are not derived in v1");
    }

    const wantDirection = ITEM_INVOICE_DIRECTION[cls];
    const lines: InventoryLineRequest[] = [];
    const gstLines: GSTLineRequest[] = [];
    let taxableTotal: Money = "0.0000";
    for (const [i, l] of req.inventory.entries()) {
      if (l.direction !== wantDirection) {
        refuse(
          `inventory line ${i + 1}: a ${cls} item invoice moves stock ${wantDirection}, not ${l.direction}`,
        );
      }
      const master = c.stockItems.find((s) => s.id === l.stock_item_id);
      if (!master) {
        refuse(`item invoice references unknown stock item ${l.stock_item_id}`);
      }
      const taxable = taxableValueOf(
        l.qty,
        l.rate ?? "0",
        l.discount_pct ?? "0",
      );
      if (l.amount && l.amount !== "0.0000" && l.amount !== taxable) {
        refuse(
          `inventory line ${i + 1} (${master!.name}): amount ${l.amount} contradicts qty x rate less discount = ${taxable}`,
        );
      }
      lines.push({ ...l, amount: taxable });
      gstLines.push({
        taxable_value: taxable,
        gst_rate_pct: master!.gst_rate_pct,
        hsn: master!.hsn,
      });
      taxableTotal = addMoney(taxableTotal, taxable);
    }

    const withLines: VoucherRequest = {
      ...req,
      gst: { ...req.gst, lines: gstLines },
    };
    const tax = deriveGst(c, withLines);
    const totalTax = addMoney(addMoney(tax.cgst, tax.sgst), tax.igst);

    // Party leg = goods + tax; goods leg = the taxable base ONLY. Booking the
    // gross to the sales ledger is how a P&L ends up overstated by the whole
    // GST liability.
    const postings: PostingRequest[] = [
      {
        ledger_id: req.party_ledger_id!,
        dr_cr: orient.party,
        amount: addMoney(taxableTotal, totalTax),
      },
      {
        ledger_id: req.item_ledger_id!,
        dr_cr: orient.goods,
        amount: taxableTotal,
      },
    ];
    // A zero component gets NO posting: an intra-state invoice has no IGST at
    // all, and a 0.0000 row is noise in every printout of that tax account.
    const taxLeg = (
      amount: Money,
      ledgerID: string | undefined,
      name: string,
    ) => {
      if (amount === "0.0000") return;
      if (!ledgerID)
        refuse(
          `item invoice derives ${name} of ${amount} but no ${name} ledger was named`,
        );
      postings.push({ ledger_id: ledgerID!, dr_cr: orient.tax, amount });
    };
    taxLeg(tax.cgst, req.gst.cgst_ledger_id, "CGST");
    taxLeg(tax.sgst, req.gst.sgst_ledger_id, "SGST");
    taxLeg(tax.igst, req.gst.igst_ledger_id, "IGST");

    return { ...withLines, inventory: lines, postings };
  }

  /**
   * THE NON-ACCOUNTING STOCK VOUCHERS — the mock's mirror of
   * internal/ledger/nonaccounting.go.
   *
   * A mock that merely echoed these bodies back would be a false oracle for the
   * two properties they exist to guarantee, so it reproduces the refusals:
   *
   *   - postings / bill_allocations / gst on ANY of the four: 422. Skipping the
   *     double-entry check is only safe when paired with this; without it a
   *     Delivery Note would be the one document permitted to state UNBALANCED
   *     ledger lines, and posting_balanced (a trigger on the posting TABLE)
   *     never fires for a voucher that writes no posting rows.
   *   - a line moving the wrong way for a fixed-direction class: 422. Inward
   *     goods on a Delivery Note would ADD stock under a document that says
   *     they left.
   *   - `inventory` on a Physical Stock, or `physical_count` on anything else:
   *     422. The two shapes are mutually exclusive per class.
   *
   * ...and, critically, it DERIVES THE PHYSICAL STOCK CORRECTION rather than
   * trusting a delta: see {@link physicalStockDeltas}.
   *
   * Returns the request REWRITTEN with the derived movements (Physical Stock)
   * or unchanged (the other three), or null when this is not a stock class.
   */
  function generateStockVoucher(
    c: MockCompany,
    cls: VoucherClass,
    voucherID: string,
    req: VoucherRequest,
  ): VoucherRequest | null {
    if (!STOCK_ONLY_CLASSES[cls]) return null;
    const refuse = (msg: string): never => {
      throw new ApiError(422, msg, { error: msg });
    };
    if (req.postings?.length) {
      refuse(
        `a ${cls} posts no accounting lines at all (it records the movement of goods, not money); remove the ${req.postings.length} posting(s)`,
      );
    }
    if (req.bill_allocations?.length) {
      refuse(
        `a ${cls} creates no outstanding bill: there is no party leg for an allocation to reference`,
      );
    }
    if (req.gst) {
      refuse(
        `a ${cls} carries no tax: GST is charged by the INVOICE, and deriving it here would file a supply twice`,
      );
    }

    if (cls === "physical-stock") {
      if (req.inventory?.length) {
        refuse(
          "a Physical Stock voucher states what was COUNTED, not what moved; send physical_count lines and let the server derive the correction",
        );
      }
      if (!req.physical_count?.length) {
        refuse(
          "a Physical Stock voucher records at least one counted position",
        );
      }
      return {
        ...req,
        inventory: physicalStockDeltas(
          c,
          voucherID,
          req.physical_count!,
          refuse,
        ),
      };
    }

    if (req.physical_count?.length) {
      refuse(
        `only a Physical Stock voucher carries counted quantities; a ${cls} states the movement itself`,
      );
    }
    if (!req.inventory?.length) {
      refuse(
        `a ${cls} exists to record goods moving; it has no inventory lines`,
      );
    }
    const want = STOCK_FIXED_DIRECTION[cls];
    if (want) {
      for (const [i, l] of req.inventory!.entries()) {
        if (l.direction !== want) {
          refuse(
            `inventory line ${i + 1}: a ${cls} moves stock ${want}, not ${l.direction} (goods keyed the wrong way would be added to the warehouse by a document that says they left)`,
          );
        }
      }
    }
    return req;
  }

  /**
   * The BOOK quantity of one position as the mock's books stand, EXCLUDING the
   * voucher being posted — the mirror of ledger.bookQtyExcludingVoucher.
   *
   * THE EXCLUSION IS WHAT MAKES AN ALTER CORRECT. Without it, re-counting the
   * same shelf on an existing Physical Stock voucher would difference the count
   * against a book that already contains that voucher's own previous
   * correction, and the adjustment would be applied twice.
   */
  function bookQtyExcluding(
    c: MockCompany,
    voucherID: string,
    key: { item: string; godown: string; batch: string },
  ): bigint {
    let qty = 0n;
    for (const [vid, v] of c.views.entries()) {
      if (vid === voucherID) continue;
      for (const l of v.inventory ?? []) {
        if (
          l.stock_item_id !== key.item ||
          (l.godown_id ?? "") !== key.godown ||
          (l.batch_id ?? "") !== key.batch
        ) {
          continue;
        }
        qty += l.direction === "IN" ? q4(l.qty) : -q4(l.qty);
      }
    }
    return qty;
  }

  /**
   * Turn counted positions into the movements that make the books agree with
   * the shelf — counted MINUS book, as ordinary movements.
   *
   * A position whose count already agrees with the books produces NO movement:
   * stock_movement's CHECK (qty > 0) forbids a zero row, and a zero-quantity
   * correction is not a thing that happened.
   *
   * A SURPLUS AT ZERO COST IS REFUSED. The inward is INVENTED here — nobody
   * keyed a supplier's price for it — so defaulting the rate to zero would
   * bring units in at no cost and silently write the position's weighted
   * average DOWN, understating closing stock and overstating the cost of every
   * later issue. A misstated balance sheet produced BY a stock-take is the
   * opposite of what a stock-take is for.
   *
   * A SHORTAGE IS NOT NEGATIVE STOCK: it posts an outward delta and the
   * position it leaves behind is exactly the counted quantity, which cannot be
   * negative. The count is a FACT about the shelf, not a withdrawal request.
   */
  function physicalStockDeltas(
    c: MockCompany,
    voucherID: string,
    counts: PhysicalCountLineRequest[],
    refuse: (msg: string) => never,
  ): InventoryLineRequest[] {
    const seen = new Set<string>();
    const out: InventoryLineRequest[] = [];
    for (const [i, count] of counts.entries()) {
      const key = {
        item: count.stock_item_id,
        godown: count.godown_id ?? "",
        batch: count.batch_id ?? "",
      };
      const k = `${key.item}|${key.godown}|${key.batch}`;
      // Two counts of the same shelf would each be differenced against the same
      // book figure and BOTH deltas written, doubling the correction — and
      // there is no sane reading of two different answers to "how many are
      // there".
      if (seen.has(k)) {
        refuse(
          `physical count line ${i + 1}: this (item, godown, batch) position is counted twice on the same voucher; a shelf has one count`,
        );
      }
      seen.add(k);
      const counted = q4(count.qty);
      if (counted < 0n) {
        refuse(
          `physical count line ${i + 1}: a counted quantity cannot be negative, got ${count.qty} (a count of 0 says the shelf is empty)`,
        );
      }
      const delta = counted - bookQtyExcluding(c, voucherID, key);
      if (delta === 0n) continue;
      const rate = q4(count.rate ?? "0");
      if (delta > 0n && rate === 0n) {
        const name =
          c.stockItems.find((s) => s.id === count.stock_item_id)?.name ??
          count.stock_item_id;
        refuse(
          `physical count line ${i + 1} (${name}): the count is ${q4str(delta)} above the books, and bringing a surplus in at zero cost would write the item's weighted-average value down; state the rate the extra units are carried at`,
        );
      }
      out.push({
        stock_item_id: count.stock_item_id,
        godown_id: key.godown,
        batch_id: key.batch,
        direction: delta > 0n ? "IN" : "OUT",
        qty: q4str(delta > 0n ? delta : -delta),
        rate: q4str(rate),
        amount: q4str(q4div((delta > 0n ? delta : -delta) * rate, QSCALE)),
      });
    }
    return out;
  }

  /**
   * Project a posted request's item grid into the stored {@link InventoryLineView}
   * shape, resolving the master NAMES for display while RETAINING the uuids.
   * The uuids are what makes an alter round-trip preserve stock: re-resolving an
   * item by name would silently drop a line whose item had been renamed.
   */
  function inventoryViews(
    c: MockCompany,
    req: VoucherRequest,
  ): InventoryLineView[] {
    return (req.inventory ?? []).map((l, i) => ({
      line_no: i + 1,
      stock_item_id: l.stock_item_id,
      item: c.stockItems.find((s) => s.id === l.stock_item_id)?.name ?? "",
      godown_id: l.godown_id ?? "",
      godown: c.godowns.find((g) => g.id === l.godown_id)?.name ?? "",
      batch_id: l.batch_id ?? "",
      batch: c.batches.find((b) => b.id === l.batch_id)?.name ?? "",
      direction: l.direction,
      qty: l.qty,
      rate: l.rate ?? "0.0000",
      discount_pct: l.discount_pct ?? "0.0000",
      amount: l.amount ?? "0.0000",
    }));
  }

  /**
   * VALIDATE AND PROJECT A REQUEST'S ORDER LINKS (T3), the way the server does.
   *
   * ⛔⛔ THE MOCK IGNORED `fulfilments` ENTIRELY UNTIL THE PICKER WAS BUILT, and that
   * is worse than it sounds. It did not reject them — it silently dropped them, so a
   * screen keyed a delivery against an order, the post returned 201, and the order
   * book went on reporting the commitment as fully outstanding. A developer driving
   * the mock would have concluded their own code was broken; a test suite built on it
   * would have gone green over a feature that did nothing. It is the same shape as the
   * T2 defect this file's own test suite exists to catch, one field along.
   *
   * The refusals below are diffed against internal/ledger/fulfilment.go, NOT against
   * the shape a screen would prefer:
   *
   *  - a `line_no` naming no inventory line is a 422. It is 1-BASED, so 0 is always
   *    wrong and the message says so — a client sending an array index is exactly one
   *    out, and that is the single most likely defect on this field.
   *  - linking MORE of an order than the line MOVED is a 422: forty units of promise
   *    cannot be discharged by twenty units of goods.
   *  - linking more than the order still has OUTSTANDING is a 422 — the deferred
   *    trigger's job on the real thing, and the refusal that keeps an outstanding
   *    balance from going negative.
   *  - an unknown order_line_id is a 422 rather than a 404: the voucher is the
   *    resource being created, and the bad reference is a fact about its body.
   */
  /**
   * VALIDATE AND PROJECT A REQUEST'S COST SPLITS (T2), the way the server does.
   *
   * ⛔⛔ THE MOCK IGNORED `cost_allocations` ENTIRELY until the picker was built, which
   * is the same silent-drop defect `fulfilments` had: a screen keyed a split, the post
   * returned 201, and both cost reports went on reporting the cost as unallocated.
   * Every assertion about amounts stayed green.
   *
   * The refusals below are diffed against internal/ledger/cost_allocation.go's three
   * passes, NOT against the shape a screen would prefer:
   *
   *  1. per allocation — an out-of-range `posting_seq` (0-BASED), a missing axis, a
   *     missing centre, a non-positive amount;
   *  2. the same centre twice on one (posting, axis) — refused because the same share
   *     keyed twice can only make the axis total correctly by making the other entry
   *     wrong;
   *  3. ⛔ THE PER-(POSTING, CATEGORY) SUM, to the paisa, with NO tolerance. Checked
   *     per AXIS and never per posting: several axes allocate the same money in
   *     parallel, so a posting-wide total would refuse correct parallel allocation and
   *     no total at all would let cost go silently unattributed.
   */
  function costAllocationViews(
    req: VoucherRequest,
  ): CostAllocationView[] {
    const as = req.cost_allocations ?? [];
    if (as.length === 0) return [];
    const postings = req.postings ?? [];
    // ⛔ REFUSED OUTRIGHT ON AN ITEM INVOICE, before anything else. Its postings are
    // generated inside the server's transaction and index 0 is the PARTY leg, so a
    // client-supplied ordinal would attribute a departmental cost to a receivable.
    if ((req.inventory ?? []).length > 0) {
      throw new ApiError(
        422,
        "ledger: an item invoice generates its own postings inside the write transaction, so a client-supplied posting number cannot be honoured — allocate on a Journal, Payment, Receipt or Contra, or on a voucher with stated postings",
      );
    }
    const cats = mockCostCategories();
    const centres = mockCostCentres();
    const out: CostAllocationView[] = [];
    for (const a of as) {
      if (!Number.isInteger(a.posting_seq) || a.posting_seq < 0 || a.posting_seq >= postings.length) {
        throw new ApiError(
          422,
          postings.length === 0
            ? `ledger: a cost allocation names posting ${a.posting_seq} but this voucher posts no accounting lines at all, so there is no cost to attribute to a department`
            : `ledger: a cost allocation names posting ${a.posting_seq} but this voucher has ${postings.length} posting(s), numbered 0 to ${postings.length - 1} — a cost cannot be attributed to a line that is not being posted`,
        );
      }
      if (!a.cost_category_id) {
        throw new ApiError(
          422,
          `ledger: the cost allocation on posting ${a.posting_seq} names no cost category; without the axis it belongs to it would be added to every other allocation of the same posting, and two axes that each allocate the full amount would come to twice the cost`,
        );
      }
      if (!a.cost_centre_id) {
        throw new ApiError(
          422,
          `ledger: the cost allocation on posting ${a.posting_seq} names cost category ${a.cost_category_id} but no cost centre, so nothing is recorded about which department bore the cost`,
        );
      }
      if (!(Number.parseFloat(a.amount || "0") > 0)) {
        throw new ApiError(
          422,
          `ledger: the cost allocation of ${a.amount} to cost centre ${a.cost_centre_id} on posting ${a.posting_seq} must be more than zero — an allocation is a MAGNITUDE and the posting's own Dr/Cr carries the direction`,
        );
      }
      const centre = centres.find((c) => c.id === a.cost_centre_id);
      // ⚠️ A CENTRE ON ANOTHER AXIS IS REFUSED. Honouring it would make one axis roll
      // up into the other and the Category Summary would double-count.
      if (centre && centre.cost_category_id !== a.cost_category_id) {
        throw new ApiError(
          422,
          `ledger: cost centre ${centre.path} is on the ${centre.cost_category_name} axis, not the one named — one axis would roll up into the other and the Cost Category Summary would double-count`,
        );
      }
      out.push({
        posting_seq: a.posting_seq,
        cost_category_id: a.cost_category_id,
        cost_category_name: cats.find((c) => c.id === a.cost_category_id)?.name ?? "",
        cost_centre_id: a.cost_centre_id,
        cost_centre_name: centre?.path ?? "",
        amount: Number.parseFloat(a.amount).toFixed(4),
      });
    }
    // Pass 2 — the same centre twice on one (posting, axis).
    const seen = new Set<string>();
    for (const a of out) {
      const k = `${a.posting_seq}|${a.cost_category_id}|${a.cost_centre_id}`;
      if (seen.has(k)) {
        throw new ApiError(
          422,
          `ledger: cost centre ${a.cost_centre_id} appears twice on posting ${a.posting_seq} for cost category ${a.cost_category_id}; the same share keyed twice can only make the axis total correctly by making the other entry wrong`,
        );
      }
      seen.add(k);
    }
    // ⛔ Pass 3 — THE PER-(POSTING, CATEGORY) SUM, in integer ten-thousandths so the
    // comparison is exact. A tolerance is how a real misallocation hides for a year.
    const totals = new Map<string, { seq: number; cat: string; sum: number }>();
    const order: string[] = [];
    for (const a of out) {
      const k = `${a.posting_seq}|${a.cost_category_id}`;
      if (!totals.has(k)) {
        order.push(k);
        totals.set(k, { seq: a.posting_seq, cat: a.cost_category_id, sum: 0 });
      }
      const t = totals.get(k)!;
      t.sum += Math.round(Number.parseFloat(a.amount) * 10000);
    }
    for (const k of order) {
      const t = totals.get(k)!;
      const want = Math.round(
        Number.parseFloat(postings[t.seq]?.amount ?? "0") * 10000,
      );
      if (t.sum === want) continue;
      const allocated = (t.sum / 10000).toFixed(4);
      const posting = (want / 10000).toFixed(4);
      const short = (want - t.sum) / 10000;
      throw new ApiError(
        422,
        short < 0
          ? `ledger: cost allocation on posting ${t.seq} totals ${allocated} on cost category ${t.cat} but the posting is only ${posting} — ${(-short).toFixed(4)} more has been attributed to departments than was actually spent, so the Cost Centre Summary would overstate cost against the Profit & Loss by that amount`
          : `ledger: cost allocation on posting ${t.seq} totals ${allocated} on cost category ${t.cat} but the posting is ${posting} — ${short.toFixed(4)} is unattributed, so the Cost Centre Summary would disagree with the Profit & Loss by that amount`,
      );
    }
    return out;
  }

  function fulfilmentViews(
    c: MockCompany,
    req: VoucherRequest,
    /** The voucher being ALTERED, whose own existing links do not count against it. */
    alteringVoucherID: string | null,
  ): FulfilmentView[] {
    const links = req.fulfilments ?? [];
    if (links.length === 0) return [];
    const inventory = req.inventory ?? [];
    const out: FulfilmentView[] = [];
    // How much this request links per order line, so the total can be checked
    // against what the order still wants.
    const wantedByLine = new Map<string, number>();
    for (const f of links) {
      if (!Number.isInteger(f.line_no) || f.line_no < 1) {
        throw new ApiError(
          422,
          `orders: fulfilment line_no ${f.line_no} is not a line number — it is 1-BASED (the first inventory line is 1), and a client sending an array index will be exactly one out`,
        );
      }
      const line = inventory[f.line_no - 1];
      if (!line) {
        throw new ApiError(
          422,
          `orders: fulfilment names inventory line ${f.line_no}, but this voucher has ${inventory.length} inventory line(s)`,
        );
      }
      const orderLine = mockOrders
        .flatMap((o) => o.lines.map((l) => ({ order: o, line: l })))
        .find((x) => x.line.order_line_id === f.order_line_id);
      if (!orderLine) {
        throw new ApiError(
          422,
          `orders: fulfilment names order line ${f.order_line_id}, which does not exist on this company`,
        );
      }
      // ⛔ THE ITEM MUST MATCH. A link between a delivery of Widgets and an order for
      // Gaskets would discharge a commitment nobody met.
      if (orderLine.line.stock_item_id !== line.stock_item_id) {
        throw new ApiError(
          422,
          `orders: inventory line ${f.line_no} moves a different item from the order line it names — a link between goods that are not the same goods would discharge a commitment nobody met`,
        );
      }
      const linkQty = Number.parseFloat(f.qty || "0");
      if (!(linkQty > 0)) {
        throw new ApiError(
          422,
          "orders: a fulfilment must consume more than zero of the order — a link that discharges nothing is not a link",
        );
      }
      wantedByLine.set(
        f.order_line_id,
        (wantedByLine.get(f.order_line_id) ?? 0) + linkQty,
      );
      out.push({
        line_no: f.line_no,
        order_id: orderLine.order.order_id,
        order_line_id: f.order_line_id,
        order_no: orderLine.order.order_no,
        stock_item: orderLine.line.stock_item,
        qty: Number.parseFloat(f.qty).toFixed(4),
      });
    }
    // ⛔ THE SUM PER INVENTORY LINE MUST NOT EXCEED WHAT THAT LINE MOVED. Checked per
    // line and not per link, because one delivery of 30 legitimately discharges a
    // 20-unit order and 10 of a second.
    const linkedByLineNo = new Map<number, number>();
    for (const f of out) {
      linkedByLineNo.set(
        f.line_no,
        (linkedByLineNo.get(f.line_no) ?? 0) + Number.parseFloat(f.qty),
      );
    }
    for (const [lineNo, linked] of linkedByLineNo) {
      const moved = Number.parseFloat(inventory[lineNo - 1]?.qty ?? "0");
      if (linked > moved + 1e-9) {
        throw new ApiError(
          422,
          `orders: inventory line ${lineNo} moves ${moved.toFixed(4)} but links ${linked.toFixed(4)} to orders — forty units of promise cannot be discharged by twenty units of goods`,
        );
      }
    }
    // ⛔ AND NOT MORE THAN THE ORDER STILL HAS OUTSTANDING. This is the deferred
    // trigger's job on the real thing. `alteringVoucherID` is excluded because an
    // ALTER RE-STATES: the version being replaced must not count against its own
    // replacement, or correcting a delivery from 5 to 8 would be refused as though 13
    // had been delivered.
    for (const [orderLineID, wanted] of wantedByLine) {
      const already = fulfilledFor(c, orderLineID, alteringVoucherID);
      const ordered = Number.parseFloat(
        mockOrders
          .flatMap((o) => o.lines)
          .find((l) => l.order_line_id === orderLineID)?.qty ?? "0",
      );
      if (already + wanted > ordered + 1e-9) {
        throw new ApiError(
          422,
          `orders: this would discharge ${(already + wanted).toFixed(4)} of an order line committing only ${ordered.toFixed(4)} — the outstanding balance would go negative, and "the customer owes us -5 units of promise" is not a fact about anything`,
        );
      }
    }
    return out;
  }

  /**
   * How much of ONE order line has been discharged, summed across CURRENT voucher
   * versions.
   *
   * ⛔ DERIVED ON EVERY READ AND STORED NOWHERE, exactly as the server does it and for
   * the reason migration 0042's header gives: a stored `fulfilled_qty` goes stale
   * undetectably the first time a fulfilling voucher is ALTERED, because an alter
   * writes a NEW version and re-states its links without ever visiting the order.
   *
   * `c.views` holds only the CURRENT version of each voucher (an alter replaces the
   * entry and a delete removes it), so summing over it is the mock's equivalent of the
   * `voucher_head.current_version_id` join every reader of this table owes. Without
   * that property a delivery corrected from 5 to 8 would count as 13.
   */
  function fulfilledFor(
    c: MockCompany,
    orderLineID: string,
    excludeVoucherID: string | null,
  ): number {
    let total = 0;
    for (const [voucherID, view] of c.views) {
      if (voucherID === excludeVoucherID) continue;
      for (const f of view.fulfilments ?? []) {
        if (f.order_line_id === orderLineID) total += Number.parseFloat(f.qty);
      }
    }
    return total;
  }

  /**
   * Map a request's bill allocations into BillAllocationViews (canonical D/C),
   * MAKING THE SERVER'S REFUSALS AND THE SERVER'S SUBSTITUTION.
   *
   * ⛔ THIS IS §9.4's THIRD KIND OF MOCK DEFECT — the one no test written against
   * the mock can catch, because a mock that is more PERMISSIVE than the server
   * simply never fails. Until this function did the three things below it accepted
   * bodies cored refuses outright and dropped the substitution cored performs, so
   * a screen could look correct against the mock and be unable to save a voucher
   * in a browser.
   *
   *  1. AN UNNAMED `AGAINST` IS REFUSED. Left to default it would open a SECOND
   *     bill under this voucher's own number instead of settling the one it meant
   *     to — both figures wrong, report still footing.
   *  2. AN UNNAMED REFERENCE OF ANY OTHER TYPE IS STAMPED with the allocated
   *     voucher number. The client CANNOT do this itself: the number is minted
   *     inside the post transaction. A mock that stored the blank would make every
   *     round-trip test assert the wrong name.
   *  3. THE REFERENCES ON A LEDGER MUST TOTAL WHAT THAT LEDGER POSTS, signed and
   *     exact to the paisa. Otherwise Outstandings and the Balance Sheet's Sundry
   *     Debtors disagree, and nothing reconciles them.
   */
  function billViews(
    req: VoucherRequest,
    voucherNo: string,
    postings: readonly { ledger_id: string; dr_cr: string; amount: string }[],
  ): BillAllocationView[] {
    const bs = req.bill_allocations ?? [];
    if (bs.length === 0) return [];
    const canonDC = (dc: string) => (dc.trim().toUpperCase().startsWith("D") ? "D" : "C");
    // ⚠️ The SHARED scaled-integer helpers, never a local copy and never a float.
    // The invariant is exact to the paisa, and a second implementation of money
    // parsing is precisely how two places in one file come to disagree.
    const units = mockMoneyToUnits;
    const out: BillAllocationView[] = [];
    const signedByLedger = new Map<string, bigint>();
    for (const b of bs) {
      if (b.ref_name.trim() === "" && b.ref_type === "AGAINST") {
        throw new ApiError(
          422,
          "ledger: an AGAINST bill allocation must name the bill it settles",
        );
      }
      const dc = canonDC(b.dr_cr);
      const signed = dc === "C" ? -units(b.amount) : units(b.amount);
      signedByLedger.set(b.ledger_id, (signedByLedger.get(b.ledger_id) ?? 0n) + signed);
      out.push({
        ledger_id: b.ledger_id,
        // THE SUBSTITUTION. An empty name is the normal case for a reference that
        // OPENS a bill — see the header.
        ref_name: b.ref_name.trim() === "" ? voucherNo : b.ref_name.trim(),
        ref_type: b.ref_type,
        dr_cr: dc,
        amount: mockUnitsToMoney(units(b.amount)),
        due_date: b.due_date ?? "",
      });
    }
    const postedByLedger = new Map<string, bigint>();
    for (const p of postings) {
      const signed = canonDC(p.dr_cr) === "C" ? -units(p.amount) : units(p.amount);
      postedByLedger.set(p.ledger_id, (postedByLedger.get(p.ledger_id) ?? 0n) + signed);
    }
    for (const [lid, got] of signedByLedger) {
      const want = postedByLedger.get(lid) ?? 0n;
      if (got === want) continue;
      throw new ApiError(
        422,
        want === 0n
          ? `ledger: bill references total ${mockUnitsToMoney(got)} against a ledger this voucher does not post to at all — a bill cannot be raised or settled on a line that is not in the entry`
          : `ledger: bill references on this ledger total ${mockUnitsToMoney(got)} but it posts ${mockUnitsToMoney(want)} — the difference is ${mockUnitsToMoney(want - got)}, and leaving it would make the Outstandings report disagree with Sundry Debtors on the Balance Sheet`,
      );
    }
    return out;
  }

  /**
   * Atomic per-(class, fin_year) allocate: returns the consumed number and bumps
   * the stored NEXT-free counter (the voucher_number_counter contract). Counters
   * start at 1; concurrent allocations would serialize on the row, so two creates
   * get distinct consecutive numbers — modelled here as a synchronous bump.
   */
  function allocateNumber(
    c: MockCompany,
    cls: string,
    finYear: number,
  ): number {
    const key = `${cls}:${finYear}`;
    const next = c.counters.get(key) ?? 1;
    c.counters.set(key, next + 1);
    return next;
  }

  /**
   * Resolve the display voucher_no on a CREATE: honor a non-empty client value
   * verbatim (override — counter untouched), else AUTOMATIC-allocate the next
   * FY-scoped running number and format it.
   */
  function resolveCreateNumber(
    c: MockCompany,
    cls: string,
    req: VoucherRequest,
  ): string {
    const supplied = (req.voucher_no ?? "").trim();
    if (supplied !== "") return supplied; // override: do NOT bump the counter
    const fmt = CLASS_NUMBER_FORMATS[cls] ?? {
      prefix: "",
      suffix: "",
      width: null,
    };
    const finYear = finYearOf(req.voucher_date ?? "");
    const n = allocateNumber(c, cls, finYear);
    return formatVoucherNo(fmt, n);
  }

  /**
   * The `class` label the SERVER stamps, from the recorded wire table.
   *
   * ⛔⛔ THIS USED TO BE `cls.charAt(0).toUpperCase() + cls.slice(1)`, AND IT WAS
   * A §9.4-CLASS FIDELITY DEFECT — a shape the mock got wrong, invisible to every
   * test written against it because no screen rendered `class`:
   *
   *  - it produced "Credit-note" / "Delivery-note" / "Physical-stock" where the
   *    server writes "Credit Note" / "Delivery Note" / "Physical Stock";
   *  - and the DAY BOOK entry did not use even that — it wrote
   *    `cls.toUpperCase()`, i.e. "SALES", so the mock disagreed with the server
   *    AND with its own GET view.
   *
   * The moment the Day Book drill started routing on `class`, a guess would have
   * quietly refused to open the multiword classes. Pinned by
   * `mock.classLabel.test.ts` against {@link VOUCHER_CLASS_LABELS}.
   */
  function classLabel(cls: VoucherClass): string {
    return VOUCHER_CLASS_LABELS[cls];
  }

  /**
   * Append one entry to a voucher's EDIT LOG. Called on every create, alter and
   * delete, because the rest of the mock restates in place and would otherwise
   * have no history to show.
   *
   * `hash`/`prev_hash` are left EMPTY on purpose: the chain is BLAKE3 over the
   * server's canonical payload, and inventing hashes here would let a screen
   * test pass against a chain nobody verified.
   */
  function logVersion(
    c: MockCompany,
    voucherID: string,
    e: {
      op: string;
      cls: string;
      voucherNo: string;
      date: string;
      narration: string;
      party: string;
      createdBy: string;
      postings: { ledger_id: string; dr_cr: string; amount: Money }[];
    },
  ): void {
    const log = c.history.get(voucherID) ?? [];
    log.push({
      version_no: log.length + 1,
      op: e.op,
      created_by: e.createdBy,
      created_at: "2026-07-27T00:00:00Z",
      node_id: "mock-0",
      hash: "",
      prev_hash: "",
      class: e.cls,
      voucher_no: e.voucherNo,
      voucher_date: e.date,
      narration: e.narration,
      party: e.party,
      postings: e.postings,
    });
    c.history.set(voucherID, log);
  }

  function recordVoucher(
    companyID: string,
    cls: VoucherClass,
    req: VoucherRequest,
  ): VoucherResponse {
    const c = ensure(companyID);
    const voucherID = id("voucher");
    // STOCK-ONLY CLASSES first: they are refused a money side outright, and a
    // Physical Stock has its correction DERIVED here (against the books as they
    // stand, excluding this voucher) exactly as post()'s transaction does.
    req = generateStockVoucher(c, cls, voucherID, req) ?? req;
    // ITEM INVOICE: the server GENERATES the gst lines and every posting from
    // the stock lines (or refuses the body). Everything below then works on the
    // generated request, exactly as cored's post() does.
    req = generateItemInvoice(c, cls, req) ?? req;
    const { postings, postingViews, debit, credit } = summarize(c, req);
    const gst = deriveGst(c, req);
    const voucherNo = resolveCreateNumber(c, cls, req);
    const date = req.voucher_date ?? "1970-01-01";
    const entry: DayBookEntry = {
      voucher_id: voucherID,
      version_no: 1,
      op: "CREATE",
      // The SERVER's label, not an upper-cased slug — the Day Book drill routes
      // on this field, so a mock that disagreed here would refuse to open the
      // very classes the drill exists to get right. See classLabel above.
      class: classLabel(cls),
      voucher_type: cls,
      voucher_no: voucherNo,
      date,
      narration: req.narration ?? "",
      party: req.party ?? "",
      debit,
      credit,
      postings,
    };
    c.vouchers.push(entry);
    logVersion(c, voucherID, {
      op: "CREATE",
      cls: classLabel(cls),
      voucherNo,
      date,
      narration: req.narration ?? "",
      party: req.party ?? "",
      createdBy: req.created_by ?? "",
      postings: req.postings ?? [],
    });
    // RETAIN real ledger_id uuids + GST attrs for the GET hydration path.
    c.views.set(voucherID, {
      voucher_id: voucherID,
      voucher_no: voucherNo,
      voucher_date: date,
      class: classLabel(cls),
      voucher_type_id: req.voucher_type_id ?? "",
      party: req.party ?? "",
      party_ledger_id: req.party_ledger_id ?? "",
      narration: req.narration ?? "",
      version_no: 1,
      op: "CREATE",
      place_of_supply: req.gst?.place_of_supply_state_code ?? "",
      reverse_charge: req.gst?.reverse_charge ?? false,
      taxable_value: gst.taxable,
      cgst: gst.cgst,
      sgst: gst.sgst,
      igst: gst.igst,
      postings: postingViews,
      // ⚠️ Passed the FINAL posting list and the ALLOCATED number, because both
      // are things only the server knows: an item invoice's postings were
      // generated here, and the number was minted here.
      bill_allocations: billViews(req, voucherNo, postingViews),
      inventory: inventoryViews(c, req),
      // The ORDER LINKS. Validated first (fulfilmentViews throws the server's own
      // refusals), so an over-fulfilment never reaches the store. `null` because a
      // CREATE has no prior version of itself to exclude.
      fulfilments: fulfilmentViews(c, req, null),
      // The COST SPLITS. Validated first, so a partial axis never reaches the store.
      cost_allocations: costAllocationViews(req),
      // ⛔ THE PER-LINE GST DETAIL. RE-STATED from this body, never carried
      // forward: the rows key on voucher_version_id server-side, so an alter that
      // omits `gst` must lose them here too or the mock is more forgiving than the
      // server about the one thing this projection exists to make safe.
      gst_lines: gstLineViews(c, req),
    });
    return {
      voucher_id: voucherID,
      voucher_version_id: id("vv"),
      version_no: 1,
      voucher_no: voucherNo,
      cgst: gst.cgst,
      sgst: gst.sgst,
      igst: gst.igst,
    };
  }

  /**
   * Alter = full restatement. Find the live entry by voucher_id and REPLACE its
   * postings/amounts/narration in place (never accumulate), bump version_no, and
   * stamp op=ALTER so the derived trial balance / day book reflect the NEW
   * amount. The voucher_id is preserved (identity is stable across versions).
   */
  /**
   * ⛔⛔ THE FREEZE. A voucher carrying an IRN CANNOT BE ALTERED OR DELETED, by
   * anybody, ever — there is no undo and no reset path anywhere in the product.
   *
   * The server enforces this in the freeze hook's `voucher_external_reference`
   * row (kind `IRN`) and answers 409. It is modelled here because §7.4's rule is
   * that the mock must refuse what the server refuses, and because this
   * particular refusal is the entire risk of the feature: a screen developed
   * against a lenient mock would offer an Alter that works in every test and
   * 409s the first time an operator uses it on a real invoice — at which point
   * the invoice is already frozen and the operator's correction is impossible.
   */
  function frozenByIRN(c: MockCompany, voucherID: string): void {
    const irn = c.eInvoices.get(voucherID);
    if (!irn) return;
    throw new ApiError(
      409,
      `voucher frozen by IRN ${irn.irn} (no ${irn.pinned_voucher_no}) — an e-invoice has been ` +
        "reported to the IRP against this document, so it can no longer be altered or deleted. " +
        "Issue a credit note against it instead",
    );
  }

  function alterVoucher(
    companyID: string,
    cls: VoucherClass,
    voucherID: string,
    req: VoucherRequest,
  ): VoucherResponse {
    const c = ensure(companyID);
    const entry = c.vouchers.find((v) => v.voucher_id === voucherID);
    if (!entry) {
      throw new Error("voucher not found");
    }
    frozenByIRN(c, voucherID);
    // An ALTER of a stock voucher is a full restatement of the GOODS, and a
    // Physical Stock's correction is re-derived against a book that EXCLUDES
    // this voucher's own superseded movements — otherwise re-counting the same
    // shelf would apply the adjustment twice.
    req = generateStockVoucher(c, cls, voucherID, req) ?? req;
    // An ALTER of an item invoice is a full restatement of the GOODS: resend
    // inventory + gst + item ledger, never the postings GetVoucher projected.
    req = generateItemInvoice(c, cls, req) ?? req;
    const { postings, postingViews, debit, credit } = summarize(c, req);
    const gst = deriveGst(c, req);
    const nextVersion = entry.version_no + 1;
    entry.version_no = nextVersion;
    entry.op = "ALTER";
    entry.class = classLabel(cls);
    entry.voucher_type = cls;
    // NO-REALLOC-ON-ALTER: an empty voucher_no carries forward the prior number;
    // a supplied value is honored verbatim. The counter is NEVER touched here.
    const suppliedNo = (req.voucher_no ?? "").trim();
    if (suppliedNo !== "") entry.voucher_no = suppliedNo;
    if (req.voucher_date !== undefined && req.voucher_date !== "")
      entry.date = req.voucher_date;
    entry.narration = req.narration ?? "";
    entry.party = req.party ?? "";
    entry.postings = postings;
    entry.debit = debit;
    entry.credit = credit;
    logVersion(c, voucherID, {
      op: "ALTER",
      cls: classLabel(cls),
      voucherNo: entry.voucher_no,
      date: entry.date,
      narration: req.narration ?? "",
      party: req.party ?? "",
      createdBy: req.created_by ?? "",
      postings: req.postings ?? [],
    });
    // Restate the head view with the new postings/GST (real ledger_id uuids).
    c.views.set(voucherID, {
      voucher_id: voucherID,
      voucher_no: entry.voucher_no,
      voucher_date: entry.date,
      class: classLabel(cls),
      voucher_type_id: req.voucher_type_id ?? "",
      party: req.party ?? "",
      party_ledger_id: req.party_ledger_id ?? "",
      narration: req.narration ?? "",
      version_no: nextVersion,
      op: "ALTER",
      place_of_supply: req.gst?.place_of_supply_state_code ?? "",
      reverse_charge: req.gst?.reverse_charge ?? false,
      taxable_value: gst.taxable,
      cgst: gst.cgst,
      sgst: gst.sgst,
      igst: gst.igst,
      postings: postingViews,
      // An ALTER keeps the voucher's existing number, so that is what an unnamed
      // reference is stamped with — never a fresh one.
      bill_allocations: billViews(req, entry.voucher_no, postingViews),
      inventory: inventoryViews(c, req),
      // ⛔ AN ALTER RE-STATES THE LINKS: whatever this body carries becomes the
      // current version's links, and whatever it omits is GONE. That is why the
      // voucher GET has to return them (it does) and why a form must send back what
      // it read — otherwise fixing a typo in the narration silently re-opens a
      // delivered order.
      //
      // `voucherID` is passed as the exclusion so this voucher's OWN superseded links
      // do not count against its replacement: correcting a delivery from 5 to 8 must
      // not be refused as though 13 had been delivered.
      fulfilments: fulfilmentViews(c, req, voucherID),
      // ⛔ AN ALTER RE-STATES THESE TOO: whatever this body carries becomes the
      // current version's splits, and whatever it omits is GONE — which is why the
      // voucher GET has to return them and a form must send back what it read.
      cost_allocations: costAllocationViews(req),
      // ⛔ THE PER-LINE GST DETAIL. RE-STATED from this body, never carried
      // forward: the rows key on voucher_version_id server-side, so an alter that
      // omits `gst` must lose them here too or the mock is more forgiving than the
      // server about the one thing this projection exists to make safe.
      gst_lines: gstLineViews(c, req),
    });
    return {
      voucher_id: voucherID,
      voucher_version_id: id("vv"),
      version_no: nextVersion,
      voucher_no: entry.voucher_no,
      cgst: gst.cgst,
      sgst: gst.sgst,
      igst: gst.igst,
    };
  }

  return {
    async createCompany(
      req: CreateCompanyRequest,
    ): Promise<CreateCompanyResponse> {
      const companyID = req.id ?? id("company");
      const c = ensure(companyID);
      c.name = req.name;
      // PERSIST the full master (previously only `name` survived). state_code in
      // particular must round-trip: getCompany is what the shell hydrates
      // homeStateCode from, and deriveGst falls back to it.
      if (req.fy_start_date) c.fyStartDate = req.fy_start_date;
      if (req.base_currency) c.baseCurrency = req.base_currency;
      c.stateCode = req.state_code ?? "";
      c.gstin = req.gstin ?? "";
      // 0015 profile. Start from the defaults for THIS company's fy start so an
      // omitted books_beginning_from tracks fy_start_date, as cored does.
      const p = defaultProfile(c.fyStartDate);
      c.profile = {
        mailingName: req.mailing_name ?? p.mailingName,
        address: req.address ?? p.address,
        city: req.city ?? p.city,
        pincode: req.pincode ?? p.pincode,
        country: req.country || p.country,
        booksBeginningFrom: req.books_beginning_from || p.booksBeginningFrom,
        currencySymbol: req.currency_symbol || p.currencySymbol,
        currencyFormalName: req.currency_formal_name || p.currencyFormalName,
        // ?? not ||: an explicit 0 (whole-rupee books) is a real choice.
        decimalPlaces: req.decimal_places ?? p.decimalPlaces,
      };
      return { company_id: companyID };
    },

    async getCompany(companyID: string): Promise<CompanyView> {
      // Unknown ids 404 like the live route rather than materialising a blank
      // company — a hydrate against a stale persisted id must be observable.
      const c = companies.get(companyID);
      if (!c) {
        throw new ApiError(404, "company not found", "company not found");
      }
      return {
        id: c.id,
        name: c.name,
        fy_start_date: c.fyStartDate,
        base_currency: c.baseCurrency,
        state_code: c.stateCode,
        gstin: c.gstin,
        mailing_name: c.profile.mailingName,
        address: c.profile.address,
        city: c.profile.city,
        pincode: c.profile.pincode,
        country: c.profile.country,
        books_beginning_from: c.profile.booksBeginningFrom,
        currency_symbol: c.profile.currencySymbol,
        currency_formal_name: c.profile.currencyFormalName,
        decimal_places: c.profile.decimalPlaces,
      };
    },

    async createGroup(
      _c,
      _req: CreateGroupRequest,
    ): Promise<CreateGroupResponse> {
      return { group_id: id("group") };
    },
    async listLedgers(companyID): Promise<ListLedgersResponse> {
      return {
        ledgers: ensure(companyID).ledgers.map((l) => ({
          id: l.id,
          name: l.name,
          group_name: l.group,
          // Server-side column defaults, mirrored so mock and cored agree on
          // what an unset field reads back as.
          opening_balance: l.openingBalance ?? "0.0000",
          opening_dc: l.openingDC ?? "D",
          maintain_bill_by_bill: l.maintainBillByBill ?? false,
          // "" is NOT RECORDED, mirroring the server's COALESCE. The mock must
          // agree, or a screen test would see `undefined` where the real client
          // sees "" and the "not recorded" branch would never be exercised.
          bank_account_number: l.bankAccountNumber ?? "",
          bank_ifsc: l.bankIFSC ?? "",
          gstin: l.gstin ?? "",
          state_code: l.stateCode ?? "",
          registration_type: l.registrationType ?? "UNKNOWN",
          address: l.address ?? "",
          city: l.city ?? "",
          pincode: l.pincode ?? "",
          is_system: l.isSystem ?? false,
          is_bank: isBankLedger(l),
        })),
      };
    },
    async setLedgerOpening(
      companyID,
      ledgerID,
      req: SetLedgerOpeningRequest,
    ): Promise<SetLedgerOpeningResponse> {
      const c = ensure(companyID);
      const l = c.ledgers.find((x) => x.id === ledgerID);
      if (!l) throw new ApiError(404, "ledger not found", "ledger not found");
      // Mirror the SERVER invariant: a breakup must account for the whole
      // opening balance, or the Outstandings total silently stops tying to the
      // Balance Sheet.
      const bills = req.opening_bills ?? [];
      if (bills.length > 0) {
        const sum = bills.reduce((a, b) => addMoney(a, b.amount), "0.0000");
        if (sum !== parseFloat(req.opening_balance).toFixed(4)) {
          throw new ApiError(
            422,
            `opening bill-wise breakup sums to ${sum} but the opening balance is ${req.opening_balance}`,
            "unprocessable",
          );
        }
      }
      l.openingBalance = req.opening_balance;
      l.openingDC = req.opening_dc ?? "D";
      return { ledger_id: ledgerID };
    },
    async createLedger(
      companyID,
      req: CreateLedgerRequest,
    ): Promise<CreateLedgerResponse> {
      const ledgerID = id("ledger");
      ensure(companyID).ledgers.push({
        id: ledgerID,
        name: req.name,
        group: req.group_name,
        stateCode: req.state_code,
        gstin: req.gstin,
        registrationType: req.registration_type,
        maintainBillByBill: req.maintain_bill_by_bill,
        openingBalance: req.opening_balance,
        openingDC: req.opening_dc,
        address: req.address,
        city: req.city,
        pincode: req.pincode,
        bankAccountNumber: req.bank_account_number,
        bankIFSC: req.bank_ifsc,
        isSystem: false,
      });
      return { ledger_id: ledgerID };
    },
    async createVoucherType(
      _c,
      _req: CreateVoucherTypeRequest,
    ): Promise<CreateVoucherTypeResponse> {
      return { voucher_type_id: id("vtype") };
    },
    async createUnit(_c, _req: CreateUnitRequest): Promise<CreateUnitResponse> {
      return { unit_id: id("unit") };
    },
    async createStockItem(
      companyID,
      req: CreateStockItemRequest,
    ): Promise<CreateStockItemResponse> {
      // STORE it. This used to mint an id and throw the master away, which made
      // listStockItems impossible and would have let an item-grid test pass
      // against an item nobody created. Create-or-select by NAME mirrors
      // masters.EnsureStockItem, so re-creating resolves the same id.
      const c = ensure(companyID);
      const existing = c.stockItems.find((s) => s.name === req.name);
      if (existing) return { stock_item_id: existing.id };
      const stockItemID = id("stock");
      c.stockItems.push({
        id: stockItemID,
        name: req.name,
        unit_symbol: req.unit_symbol,
        hsn: req.hsn ?? "",
        gst_rate_pct: req.gst_rate_pct ?? "0.0000",
        valuation_method: "WEIGHTED_AVG",
        is_system: false,
      });
      return { stock_item_id: stockItemID };
    },
    async listStockItems(companyID): Promise<ListStockItemsResponse> {
      return {
        stock_items: [...ensure(companyID).stockItems].sort((a, b) =>
          a.name.localeCompare(b.name),
        ),
      };
    },
    async listGodowns(companyID): Promise<ListGodownsResponse> {
      return {
        godowns: [...ensure(companyID).godowns].sort((a, b) =>
          a.name.localeCompare(b.name),
        ),
      };
    },
    async listStockBatches(companyID): Promise<ListStockBatchesResponse> {
      return {
        batches: [...ensure(companyID).batches].sort(
          (a, b) =>
            a.stock_item_name.localeCompare(b.stock_item_name) ||
            a.name.localeCompare(b.name),
        ),
      };
    },
    async getStockSummary(
      companyID,
      asOn?: string,
    ): Promise<StockSummaryResponse> {
      return replayStock(ensure(companyID), asOn);
    },

    async postSalesVoucher(companyID, req) {
      return recordVoucher(companyID, "sales", req);
    },
    async postPurchaseVoucher(companyID, req) {
      return recordVoucher(companyID, "purchase", req);
    },
    async postReceiptVoucher(companyID, req) {
      return recordVoucher(companyID, "receipt", req);
    },
    async postPaymentVoucher(companyID, req) {
      return recordVoucher(companyID, "payment", req);
    },
    async postContraVoucher(companyID, req) {
      return recordVoucher(companyID, "contra", req);
    },
    async postJournalVoucher(companyID, req) {
      return recordVoucher(companyID, "journal", req);
    },
    async postCreditNoteVoucher(companyID, req) {
      return recordVoucher(companyID, "credit-note", req);
    },
    async postDebitNoteVoucher(companyID, req) {
      return recordVoucher(companyID, "debit-note", req);
    },
    async postDeliveryNoteVoucher(companyID, req) {
      return recordVoucher(companyID, "delivery-note", req);
    },
    async postReceiptNoteVoucher(companyID, req) {
      return recordVoucher(companyID, "receipt-note", req);
    },
    async postStockJournalVoucher(companyID, req) {
      return recordVoucher(companyID, "stock-journal", req);
    },
    async postPhysicalStockVoucher(companyID, req) {
      return recordVoucher(companyID, "physical-stock", req);
    },

    async alterSalesVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "sales", voucherID, req);
    },
    async alterPurchaseVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "purchase", voucherID, req);
    },
    async alterReceiptVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "receipt", voucherID, req);
    },
    async alterPaymentVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "payment", voucherID, req);
    },
    async alterContraVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "contra", voucherID, req);
    },
    async alterJournalVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "journal", voucherID, req);
    },
    async alterCreditNoteVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "credit-note", voucherID, req);
    },
    async alterDebitNoteVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "debit-note", voucherID, req);
    },
    async alterDeliveryNoteVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "delivery-note", voucherID, req);
    },
    async alterReceiptNoteVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "receipt-note", voucherID, req);
    },
    async alterStockJournalVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "stock-journal", voucherID, req);
    },
    async alterPhysicalStockVoucher(companyID, voucherID, req) {
      return alterVoucher(companyID, "physical-stock", voucherID, req);
    },

    async getVoucher(companyID, voucherID): Promise<VoucherView> {
      const c = ensure(companyID);
      const view = c.views.get(voucherID);
      if (!view) {
        // Mirror the live GET: unknown id OR a deleted head row -> 404.
        throw new ApiError(404, "voucher not found", {
          error: "voucher not found",
        });
      }
      // Return a structural clone so callers can't mutate the store.
      return {
        ...view,
        postings: view.postings.map((p) => ({ ...p })),
        bill_allocations: view.bill_allocations.map((b) => ({ ...b })),
      };
    },

    async deleteVoucher(
      companyID,
      voucherID,
      _createdBy?: string,
    ): Promise<DeleteVoucherResponse> {
      void _createdBy;
      const c = ensure(companyID);
      const idx = c.vouchers.findIndex((v) => v.voucher_id === voucherID);
      if (idx < 0) {
        throw new Error("voucher not found");
      }
      frozenByIRN(c, voucherID);
      const prior = c.vouchers[idx] as DayBookEntry;
      const nextVersion = prior.version_no + 1;
      // Tombstone: drop the voucher's accounting effect and default day-book
      // visibility (it leaves the in-memory store entirely), matching the
      // backend's reverse-prior-then-add-nothing net-zero delete. The head view
      // is removed too, so a subsequent getVoucher 404s (rejects) like the live
      // GET (which excludes is_deleted=true head rows).
      logVersion(c, voucherID, {
        op: "DELETE",
        cls: classLabel(prior.voucher_type as VoucherClass),
        voucherNo: prior.voucher_no,
        date: prior.date,
        narration: prior.narration,
        party: prior.party,
        createdBy: _createdBy ?? "",
        postings: [],
      });
      c.vouchers.splice(idx, 1);
      c.views.delete(voucherID);
      return {
        voucher_id: voucherID,
        voucher_version_id: id("vv"),
        version_no: nextVersion,
        op: "DELETE",
        voucher_no: prior.voucher_no,
      };
    },

    async getVoucherHistory(
      companyID: string,
      voucherID: string,
    ): Promise<VoucherHistoryResponse> {
      const c = ensure(companyID);
      const versions = c.history.get(voucherID);
      if (!versions || versions.length === 0) {
        throw new ApiError(404, "voucher not found", "voucher not found");
      }
      return {
        voucher_id: voucherID,
        // The mock removes a deleted voucher from the live store, so a head view
        // that is gone while a log exists IS the tombstone.
        is_deleted: !c.views.has(voucherID),
        versions: versions.map((v) => ({ ...v })),
        // NOT `ok: true`. The mock computes no hashes (see MockCompany.history),
        // so claiming a verified chain would assert the single property this
        // whole feature exists to prove. It reports honestly that it checked
        // nothing, and the screen renders that as the unknown state it is.
        verification: {
          ok: false,
          versions_checked: 0,
          broken_at_version: 0,
          reason:
            "mock client: no hash chain is computed in memory — only the server verifies the log",
        },
      };
    },

    // AS AT a date: every voucher up to and including `asOn`. The mock honours
    // it for real rather than ignoring the argument, so a screen test that
    // passes a period observes the same SHAPE of behaviour the server gives.
    async getTrialBalance(
      companyID,
      asOn?: string,
    ): Promise<TrialBalanceResponse> {
      const c = ensure(companyID);
      const acc = new Map<
        string,
        { debit: Money; credit: Money; group: string }
      >();
      for (const l of c.ledgers) {
        acc.set(l.name, { debit: "0.0000", credit: "0.0000", group: l.group });
      }
      for (const v of c.vouchers) {
        if (asOn && v.date > asOn) continue;
        for (const p of v.postings) {
          const row =
            acc.get(p.ledger) ??
            (() => {
              const r = { debit: "0.0000", credit: "0.0000", group: "" };
              acc.set(p.ledger, r);
              return r;
            })();
          if (p.dr_cr.trim().toUpperCase().startsWith("D"))
            row.debit = addMoney(row.debit, p.amount);
          else row.credit = addMoney(row.credit, p.amount);
        }
      }
      let totalDebit: Money = "0.0000";
      let totalCredit: Money = "0.0000";
      const rows = [...acc.entries()].map(([ledger, r]) => {
        totalDebit = addMoney(totalDebit, r.debit);
        totalCredit = addMoney(totalCredit, r.credit);
        return { ledger, group: r.group, debit: r.debit, credit: r.credit };
      });
      return {
        rows,
        total_debit: totalDebit,
        total_credit: totalCredit,
        balanced: totalDebit === totalCredit,
      };
    },

    async getBalanceSheet(
      companyID,
      asOn?: string,
    ): Promise<BalanceSheetResponse> {
      ensure(companyID);
      void asOn; // the mock builds no statement tree, so there is nothing to bound
      return {
        assets: { title: "Assets", nodes: [], total: "0.0000" },
        liabilities: { title: "Liabilities", nodes: [], total: "0.0000" },
        net_profit: "0.0000",
        balanced: true,
      };
    },

    async getProfitLoss(
      companyID,
      from?: string,
      to?: string,
    ): Promise<ProfitLossResponse> {
      ensure(companyID);
      void from;
      void to;
      return {
        income: { title: "Income", nodes: [], total: "0.0000" },
        expenses: { title: "Expenses", nodes: [], total: "0.0000" },
        net_profit: "0.0000",
      };
    },

    async getDayBook(
      companyID,
      query?: DayBookQuery,
    ): Promise<DayBookResponse> {
      const c = ensure(companyID);
      return {
        from: query?.from ?? "",
        to: query?.to ?? "",
        entries: c.vouchers.slice(),
      };
    },

    // ---- PRINTING (T1). The mock cannot generate a PDF — the real writer lives
    // in the Go backend (base-14 fonts, uncompressed streams so the bytes are
    // diffable). It returns a clearly-labelled placeholder so a screen's plumbing
    // is exercisable offline, and it REFUSES the same classes the server refuses,
    // because a mock that printed a Contra would let a broken refusal pass.
    async printVoucher(companyID, voucherClass, voucherID) {
      ensure(companyID);
      if (!MOCK_PRINTABLE_CLASSES.includes(voucherClass)) {
        throw new ApiError(
          404,
          "no printable voucher with that id in this company",
        );
      }
      return {
        blob: new Blob(
          [
            `MOCK PDF — not a real document.\n` +
              `${voucherClass} ${voucherID}\n` +
              `The PDF writer is server-side (internal/printing); this offline ` +
              `mock only proves the client plumbing.\n`,
          ],
          { type: "application/pdf" },
        ),
        filename: `mock-${voucherClass}-${voucherID}.pdf`,
      };
    },
    async printReport(companyID, slug, query) {
      ensure(companyID);
      // The scope goes in the mock body too. A mock that silently drops
      // ledger_id would let the "which bank account?" bug pass every test that
      // runs against it, which is the one thing this report must not do.
      const scope = query
        ? Object.entries(query)
            .map(([k, v]) => `${k}: ${v}\n`)
            .join("")
        : "";
      return {
        blob: new Blob(
          [`MOCK PDF — not a real document.\nreport: ${slug}\n${scope}`],
          { type: "application/pdf" },
        ),
        filename: `mock-${slug}.pdf`,
      };
    },

    // ---- THE TDS/TCS REGISTER (Item Zero step 4).
    //
    // The mock derives neither TDS nor TCS, so this is always EMPTY. That is
    // deliberate and it is stated: a mock that synthesised plausible register
    // rows would let a screen look finished against data no engine produced —
    // exactly the "sample rows on an accounting screen" the report screens
    // refuse. The disclosures and the I-7 refusal ARE returned, because those
    // are the parts of the response the screen must render correctly whether or
    // not there are rows.
    async tdsRegister(
      companyID,
      from: string,
      to: string,
    ): Promise<TDSRegisterResponse> {
      ensure(companyID);
      return {
        from,
        to,
        tax_year_start: 0,
        deductions: [],
        collections: [],
        total_base_deducted: "0.0000",
        total_tds: "0.0000",
        total_base_collected: "0.0000",
        total_tcs: "0.0000",
        sub_threshold_deductions: 0,
        exempt_collections: 0,
        // ⛔ THESE TWO ARE NOT WRITTEN HERE, AND THAT IS THE POINT.
        //
        // They used to be hand-retyped from reporting.ReturnFileRefusal(), with
        // a comment asking whoever changed the server to remember to change them
        // too. Nobody did, twice: this mock told every screen the Form 140 layout
        // "has not been read from a primary source" for two days after the format
        // was committed, and it carried TWO blockers while the server emitted
        // THREE.
        //
        // The failure mode is the worst available. TDSRegisterScreen.test.tsx
        // renders against createMockClient() and matches strings that live in
        // THIS FILE — so when the server's wording moves and the mock's does not,
        // the whole frontend suite stays GREEN against a refusal the product no
        // longer makes. Every test passes and the screen lies.
        //
        // So the prose is now GENERATED from the Go that produces it, by
        // internal/reporting's TestReturnFileStatusGolden, and imported. There is
        // no copy here to go stale. If that test fails, the wording changed on
        // purpose or by accident — read the diff, then regenerate:
        //   cd backend && UPDATE_GOLDENS=1 go test ./internal/reporting/ \
        //     -run TestReturnFileStatusGolden
        disclosures: registerProse.disclosures,
        return_file: registerProse.return_file,
      };
    },

    // ---- s.394(2) DECLARATIONS (Item Zero step 3).
    //
    // The mock does not derive TCS, so filing one here exempts nothing. It
    // exists so the F11 panel has somewhere to read and write against, and the
    // gap is worth naming: a screen that looks right against this mock is not
    // evidence the exemption works. TestFilingADeclarationExemptsTheNextSale
    // covers that server-side.
    async listTCSDeclarations(companyID): Promise<TCSDeclarationListResponse> {
      const c = ensure(companyID);
      return {
        tcs_declarations: [...c.tcsDeclarations.values()].sort((a, b) =>
          b.received_date.localeCompare(a.received_date),
        ),
        eligible_sections: ["206C(1)-SCRAP"],
      };
    },
    async createTCSDeclaration(
      companyID,
      req: CreateTCSDeclarationRequest,
    ): Promise<CreateTCSDeclarationResponse> {
      const c = ensure(companyID);
      // s.394(2) reaches Table Sl. Nos. 1-5. Motor vehicles are Sl. No. 6, and
      // refusing here mirrors the server: an operator who files one believes
      // their buyer is exempt.
      if (req.section !== "206C(1)-SCRAP") {
        throw new ApiError(
          400,
          `masters: invalid s.394(2) declaration: s.394(2) reaches only Table Sl. Nos. 1 to 5, and ${JSON.stringify(req.section)} is not one of them`,
        );
      }
      if (req.received_date < req.declaration_date) {
        throw new ApiError(
          400,
          "masters: invalid s.394(2) declaration: received date is before the declaration date",
        );
      }
      const buyer = c.ledgers.find((l) => l.id === req.buyer_ledger_id);
      const pan = (req.buyer_pan ?? "").trim();
      const received = new Date(req.received_date + "T00:00:00Z");
      const due = new Date(
        Date.UTC(received.getUTCFullYear(), received.getUTCMonth() + 1, 7),
      );
      c.tcsDeclarations.set(
        `${req.buyer_ledger_id}:${req.section}:${req.tax_year_start}`,
        {
          buyer_ledger_id: req.buyer_ledger_id,
          buyer_name: buyer?.name ?? "",
          section: req.section,
          tax_year_start: req.tax_year_start,
          tax_year_label: mockTaxYearLabel(req.tax_year_start),
          declaration_date: req.declaration_date,
          received_date: req.received_date,
          forward_due_date: due.toISOString().slice(0, 10),
          buyer_pan: pan,
          // ⚠️ NOT "is there a declaration" — a false here means the penal rate.
          pan_valid: pan !== "",
          recorded_by: req.recorded_by,
        },
      );
      return {
        buyer_ledger_id: req.buyer_ledger_id,
        section: req.section,
        tax_year_start: req.tax_year_start,
        tax_year_label: mockTaxYearLabel(req.tax_year_start),
      };
    },

    // ---- TDS DEDUCTOR STATUS (Item Zero step 2).
    async listDeductorStatus(companyID): Promise<DeductorStatusListResponse> {
      const c = ensure(companyID);
      return {
        deductor_statuses: [...c.deductorStatuses.values()].sort(
          (a, b) => b.tax_year_start - a.tax_year_start,
        ),
        entity_categories: MOCK_ENTITY_CATEGORIES,
        prior_year_activity: ["BUSINESS", "PROFESSION"],
      };
    },
    async setDeductorStatus(
      companyID,
      taxYearStart: number,
      req: SetDeductorStatusRequest,
    ): Promise<SetDeductorStatusResponse> {
      const c = ensure(companyID);
      if (!MOCK_ENTITY_CATEGORIES.includes(req.entity_category)) {
        throw new ApiError(
          400,
          `masters: invalid deductor status: ${JSON.stringify(req.entity_category)} is not one of the s.402(11)(a)–(l) deductor categories`,
        );
      }
      // The turnover pair, mirroring masters.validateDeductorStatus. Kept here
      // rather than skipped because the F11 panel's conditional fields are
      // exactly what this rule drives, and a mock that accepted anything would
      // let a broken form pass its own test.
      const natural = ["INDIVIDUAL", "HUF", "AOP", "BOI"].includes(
        req.entity_category,
      );
      const hasTurnover = (req.prior_year_turnover_rupees ?? "") !== "";
      if (natural && !hasTurnover) {
        throw new ApiError(
          400,
          `masters: invalid deductor status: ${req.entity_category} is subject to the turnover test, so the prior year's turnover and whether it was business or profession are both required`,
        );
      }
      if (!natural && hasTurnover) {
        throw new ApiError(
          400,
          `masters: invalid deductor status: ${req.entity_category} is a specified and designated person WHATEVER its turnover, so no turnover may be recorded against it`,
        );
      }
      c.deductorStatuses.set(taxYearStart, {
        tax_year_start: taxYearStart,
        tax_year_label: mockTaxYearLabel(taxYearStart),
        entity_category: req.entity_category,
        governs_payments_in: taxYearStart + 1,
        governs_payments_in_label: mockTaxYearLabel(taxYearStart + 1),
        prior_year_turnover_rupees: hasTurnover
          ? req.prior_year_turnover_rupees!
          : "",
        prior_year_activity: hasTurnover ? (req.prior_year_activity ?? "") : "",
        recorded_by: req.recorded_by,
        recorded_at: new Date().toISOString(),
      });
      return {
        tax_year_start: taxYearStart,
        tax_year_label: mockTaxYearLabel(taxYearStart),
        governs_payments_in: taxYearStart + 1,
        governs_payments_in_label: mockTaxYearLabel(taxYearStart + 1),
      };
    },

    // ---- TDS CHALLANS (T6, migration 0069).
    //
    // WHAT THIS HALF OF THE MOCK DOES NOT MODEL, stated first because the gaps
    // decide what a screen test may claim:
    //
    //   - IT DERIVES NO TAX AND KEEPS NO DEDUCTIONS. Allocations are checked
    //     against the FIXTURE table (MOCK_TDS_DEDUCTIONS) and nothing else, so an
    //     allocation cannot really be checked against a voucher here: the real
    //     server reads the live `tds_deduction` row inside the write transaction.
    //   - IT NEVER GOES BROKEN-BY-ALTER. `deduction_on_record` is always true and
    //     `broken_by_alter` always false, because no voucher in this mock can
    //     remove a deduction leg. A screen that renders those two flags must be
    //     tested with a hand-built response, NOT against this mock.
    //   - IT CANNOT FAIL A READ-BACK, so `challan` is never null and
    //     `read_back_error` is never set. The 201-with-null-challan branch — the
    //     one that must NOT be reported to the operator as a failed write — is
    //     unreachable here and needs its own fixture.
    //   - IT KNOWS NOTHING ABOUT FORM 140. Minor head 100's extra rules (the
    //     deposit date must fall inside the quarter being filed) are refused
    //     MONTHS LATER by the emitter, not here — and not by the server either.
    async listTDSChallans(
      companyID,
      taxYearStart: number,
    ): Promise<TDSChallanListResponse> {
      const c = ensure(companyID);
      // THE YEAR IS REQUIRED AND NOTHING DEFAULTS IT — refused here as well as on
      // the server, because a screen that forgot to ask would otherwise render a
      // plausible empty register against this mock and a 400 against cored.
      //
      // ⚠️ AND ONLY WHAT THE SERVER ACTUALLY REFUSES. This used to refuse
      // `taxYearStart === 0` too, with the missing-param sentence — A REFUSAL THE
      // SERVER DOES NOT MAKE: the handler rejects an EMPTY or non-numeric param,
      // `strconv.Atoi("0")` succeeds, and ListTDSChallans has no range check at
      // all (the 2025–2200 rule is on the WRITE path). So a screen that sent 0 for
      // an unparseable box got an invented 400 here and a 200 with zero rows on
      // the real stack, which reads as "no deposit has been recorded against
      // 0-01". A mock that invents a refusal certifies behaviour that does not
      // exist, and a test pinned to it is worse than no test.
      if (!Number.isInteger(taxYearStart)) {
        throw new ApiError(
          400,
          "tax_year_start must be the calendar year the tax year begins in, e.g. 2026 for 2026-27",
        );
      }
      return {
        tax_year_start: taxYearStart,
        tax_year_label: mockTaxYearLabel(taxYearStart),
        challans: [...c.tdsChallans.values()]
          .filter((ch) => ch.tax_year_start === taxYearStart)
          // The server's ORDER BY deposit_date, challan_no. A register ordered
          // any other way is a different document.
          .sort(
            (a, b) =>
              a.deposit_date.localeCompare(b.deposit_date) ||
              a.challan_no.localeCompare(b.challan_no),
          ),
        // A COPY, so a caller that sorts the picker in place cannot rewrite the
        // vocabulary for every later call (masters.TDSChallanMinorHeads copies for
        // the same reason).
        minor_heads: MOCK_MINOR_HEADS.map((h) => ({ ...h })),
        // ---- WHAT CAN BE ALLOCATED, served exactly as the server serves it.
        //
        // ⛔ THIS IS WHAT MAKES THE DEPOSIT FORM COMPLETABLE, and its absence was
        // the blocker: the form asked the operator to TYPE voucher_id (a UUID) and
        // leg_seq (a 0-based ordinal), and NO SCREEN IN THIS PRODUCT SHOWS EITHER.
        // Every test passed anyway because they keyed these fixture ids — which,
        // until this commit, were not uuids and so were refused by cored with
        // "allocation 1: invalid voucher_id". The ids are uuids now precisely so a
        // screen test CANNOT pass with an id the real server would reject.
        //
        // ZERO-TAX ROWS ARE EXCLUDED, as ListAllocatableDeductions excludes them:
        // nothing can be deposited against a sub-threshold payment, and offering
        // one would invite a refusal for a thing that is not an error.
        // ALREADY-PAID ROWS ARE NOT excluded — an operator who cannot find a
        // deduction has to be able to tell "not deducted in this year" from
        // "already deposited".
        allocatable_deductions: [...c.tdsDeductions.values()]
          .filter(
            (d) => d.tax_year_start === taxYearStart && q4(d.tax_rupees) > 0n,
          )
          // The server's ORDER BY deduction_date, voucher_no, leg_seq.
          .sort(
            (a, b) =>
              a.deduction_date.localeCompare(b.deduction_date) ||
              a.voucher_no.localeCompare(b.voucher_no) ||
              a.leg_seq - b.leg_seq,
          )
          .map((d) => ({
            voucher_id: d.voucher_id,
            leg_seq: d.leg_seq,
            voucher_no: d.voucher_no,
            deductee_name: d.deductee_name,
            section: d.section,
            deduction_date: d.deduction_date,
            tax_rupees: d.tax_rupees,
            // A fact about this mock's OWN stored challans, so it is modelled
            // exactly rather than approximated — the same LEFT JOIN the server
            // does, on the same natural key.
            already_paid: c.tdsAllocations.has(
              allocationTriple(d.voucher_id, d.section, d.leg_seq),
            ),
          })),
      };
    },

    async createTDSChallan(
      companyID,
      req: CreateTDSChallanRequest,
    ): Promise<CreateTDSChallanResponse> {
      const c = ensure(companyID);
      const invalid = (why: string) =>
        new ApiError(400, `masters: invalid TDS challan: ${why}`);

      const bsr = (req.bsr_code ?? "").trim();
      const challanNo = (req.challan_no ?? "").trim();
      const depositDate = (req.deposit_date ?? "").trim();
      const minorHead = (req.minor_head ?? "").trim();

      // ---- The identity fields. LEADING ZEROS ARE PART OF BOTH, which is why
      // the shape is checked on the STRING and nothing here parses either.
      if (!/^[0-9]{7}$/.test(bsr)) {
        const why =
          bsr.length === 7
            ? "it is seven characters but not all of them are digits"
            : `it is ${bsr.length} characters long`;
        throw invalid(
          `the BSR code is EXACTLY SEVEN DIGITS and "${bsr}" fails that — ${why}. ` +
            "It is the code of the bank branch that received the deposit (or, for a book-entry " +
            "deposit, the seven-digit Form 24G receipt number). LEADING ZEROS ARE PART OF IT: " +
            "0004329 is a valid code and 4329 is a different branch",
        );
      }
      if (!/^[0-9]{1,5}$/.test(challanNo)) {
        throw invalid(
          `the challan serial number is ONE TO FIVE DIGITS and "${challanNo}" is not — ` +
            "it is the serial the bank printed on the counterfoil. LEADING ZEROS ARE PART OF IT: " +
            "05122 is not 5122",
        );
      }
      if (!/^\d{4}-\d{2}-\d{2}$/.test(depositDate)) {
        throw invalid(
          `the deposit date "${depositDate}" is not yyyy-mm-dd. It is the date the tax ` +
            "was PAID TO THE GOVERNMENT — not the date of the voucher and not the date of the " +
            "deduction",
        );
      }

      // ---- The minor head. NO DEFAULT, and the empty case has its own sentence
      // because it is the one an operator meets by leaving the picker alone.
      if (minorHead === "") {
        throw invalid(
          "the minor head is required and there is NO DEFAULT. 200 is tax " +
            "paid over of the deductor's own motion, 400 is a demand raised on assessment, 100 is " +
            "advance tax. Defaulting to 200 would assert on this company's behalf that the deposit " +
            "was voluntary rather than demanded, which is a statement about its assessment history " +
            "that this software has no business making — migration 0069 forbids it explicitly",
        );
      }
      const head = MOCK_MINOR_HEADS.find((h) => h.code === minorHead);
      if (!head) {
        throw invalid(
          `"${minorHead}" is not an Annexure 7 minor head; it is one of 200 (paid of ` +
            "the deductor's own motion), 400 (a demand raised on assessment) or 100 (advance tax)",
        );
      }

      // ---- The tax year. RECORDED, never derived from the deposit date.
      if (
        !Number.isInteger(req.tax_year_start) ||
        req.tax_year_start < 2025 ||
        req.tax_year_start > 2200
      ) {
        throw invalid(
          `tax year ${req.tax_year_start} is outside 2025–2200. This is the year the DEPOSIT ` +
            "BELONGS TO, which is not always the year the deposit date falls in — a challan paid on " +
            "7 April 2026 is normally for March of 2025-26, so it is recorded and never computed " +
            "from the date",
        );
      }

      // ---- The four amounts. STRINGS all the way down: parsed to scaled BigInt
      // by q4 and rendered back by q4str, never through a float.
      const amounts: { name: string; raw: Money; units: bigint }[] = [];
      for (const [name, raw] of [
        ["tax", req.tax_rupees],
        ["interest", req.interest_rupees],
        ["fee", req.fee_rupees],
        ["penalty", req.penalty_rupees],
      ] as const) {
        // "" AND ABSENT ARE BOTH ZERO here — on a challan a blank box and a zero
        // in it are the same statement. (On /deductor-status's turnover they are
        // not, which is why that field is checked the opposite way.)
        const text = (raw ?? "").trim();
        if (text !== "" && !/^[+-]?(\d+(\.\d*)?|\.\d+)$/.test(text)) {
          throw new ApiError(
            400,
            `invalid ${name}_rupees: money is a fixed-point decimal STRING such as "5000" — ` +
              "never a JSON number, which cannot carry paise exactly",
          );
        }
        amounts.push({ name, raw: text, units: text === "" ? 0n : q4(text) });
      }
      for (const a of amounts) {
        if (a.units < 0n) {
          throw invalid(
            `the ${a.name} on the challan is ${mockPlainAmount(a.raw)}. These are MAGNITUDES — a ` +
              "deposit is never negative, and a refund is not a challan",
          );
        }
        if (a.units % QSCALE !== 0n) {
          throw invalid(
            `the ${a.name} on the challan is ${mockPlainAmount(a.raw)}, and all five challan amount ` +
              'fields are WHOLE RUPEES: the prescribed format says "no fractional portion is ' +
              "allowed in this field (value should be integer), i.e. value '1000.50' will not be " +
              "allowed, whereas value '1000.00' will be considered to be valid value\". The " +
              "column is NUMERIC(19,4) so the TYPE accepts paise; the rule is on the VALUE",
          );
        }
      }
      const taxUnits = amounts[0]!.units;
      const total = amounts.reduce((sum, a) => sum + a.units, 0n);
      if (total <= 0n) {
        throw invalid(
          "nothing was deposited on this challan — tax, interest, fee and " +
            "penalty are all zero. A challan on which nothing was paid is a NIL challan, and a nil " +
            "challan is not a deposit: it is how the return carries deductee rows that have no tax, " +
            "it is derived from those deductions, and the emitter synthesises it. Storing one would " +
            "put a fiction in the same table as facts and the first report to SUM this table would " +
            "count it",
        );
      }

      // ---- Tax and allocations imply each other, in both directions.
      const allocations = req.allocations ?? [];
      if (taxUnits > 0n && allocations.length === 0) {
        throw invalid(
          `this challan deposited ${mockPlainAmount(q4str(taxUnits))} of TAX and names no deduction. ` +
            "Deductee Detail field 5 of the return is a challan reference, so a deduction with no " +
            "allocation cannot be filed at all — and this slice has no allocate-later route, so a " +
            "challan stored without its allocations could not be completed afterwards",
        );
      }
      if (taxUnits <= 0n && allocations.length > 0) {
        throw invalid(
          "this challan deposited NO TAX (only interest, fee or penalty) " +
            `but allocates to ${allocations.length} deduction(s). Only the tax component discharges a deduction ` +
            "liability, so there is nothing here to allocate; record the interest challan on its " +
            "own",
        );
      }

      // ---- PASS 1 over the allocations: the checks that need nothing but the
      // request itself. Separated from pass 2 so the ORDER OF REFUSALS matches the
      // server's, which matters for one case in particular: re-posting an
      // identical slip must answer "this CIN is already recorded" and not "that
      // deduction is already paid". Both are 409, but only the first names what
      // the operator is actually holding.
      const seen = new Set<string>();
      const parsed: {
        voucherID: string;
        section: string;
        legSeq: number;
        rawAmount: Money;
        amountUnits: bigint;
        triple: string;
      }[] = [];
      for (const a of allocations) {
        const voucherID = (a.voucher_id ?? "").trim();
        const section = (a.section ?? "").trim();
        if (voucherID === "") {
          throw invalid(
            "an allocation must name the voucher whose deduction was paid",
          );
        }
        if (section === "") {
          throw invalid(
            `allocation for voucher ${voucherID} names no section. The deduction ` +
              "key is (voucher, section, leg) — one voucher can carry deductions under more than " +
              "one section",
          );
        }
        // 0 IS A REAL LEG, so an absent leg_seq is refused BY NAME and never
        // defaulted. The type makes it required; a JS caller can still omit it,
        // and the silent default is exactly what must not happen.
        if (typeof a.leg_seq !== "number" || !Number.isInteger(a.leg_seq)) {
          throw new ApiError(
            400,
            `allocation ${parsed.length + 1}: leg_seq is required and is NOT defaulted. The deduction key is ` +
              "(voucher, section, leg) and the leg ordinal is 0-based, so leg 0 is a real " +
              "leg — an omitted field would silently mean the first one",
          );
        }
        if (a.leg_seq < 0) {
          throw invalid(
            `allocation for voucher ${voucherID} under ${section} has leg ${a.leg_seq}; the leg ` +
              "ordinal is 0-based and never negative",
          );
        }
        const rawAmount = (a.amount_rupees ?? "").trim();
        if (!/^[+-]?(\d+(\.\d*)?|\.\d+)$/.test(rawAmount)) {
          throw new ApiError(
            400,
            `allocation ${parsed.length + 1}: amount_rupees must be a fixed-point decimal string, and it must be the ` +
              "tax that was deducted on that leg — it is what makes a later voucher ALTER " +
              "detectable, so it is stated rather than derived",
          );
        }
        const amountUnits = q4(rawAmount);
        if (amountUnits < 0n) {
          throw invalid(
            `allocation for voucher ${voucherID} under ${section} is ${mockPlainAmount(rawAmount)}. An allocation is ` +
              "a magnitude",
          );
        }
        const triple = allocationTriple(voucherID, section, a.leg_seq);
        if (seen.has(triple)) {
          throw invalid(
            `voucher ${voucherID} under ${section} leg ${a.leg_seq} appears twice on this challan. ` +
              "One challan pays a deduction ONCE — Deductee Detail field 5 is a single challan " +
              "reference, so a deduction split across two lines is not expressible in the return",
          );
        }
        seen.add(triple);
        parsed.push({
          voucherID,
          section,
          legSeq: a.leg_seq,
          rawAmount,
          amountUnits,
          triple,
        });
      }

      // ---- recorded_by, which the server validates in the same pure pass.
      if ((req.recorded_by ?? "").trim() === "") {
        throw invalid("recorded_by is required");
      }

      // ---- THE DUPLICATE CIN, and it is checked BEFORE the allocations because
      // the server checks it first inside the transaction. That order is the
      // feature: re-posting an identical slip must answer "this CIN is already
      // recorded" — not "that deduction is already paid", which would describe the
      // consequence instead of the thing in the operator's hand.
      //
      // NEVER AN UPSERT. The second post of one CIN is either a double entry or a
      // second real deposit whose serial was mistyped, and overwriting either
      // loses money.
      const cin = mockCIN(bsr, depositDate, challanNo);
      const existing = c.tdsChallans.get(cin);
      if (existing) {
        throw new ApiError(
          409,
          `masters: challan ${challanNo} of ${depositDate} at BSR branch ${bsr} is already recorded, with ${mockPlainAmount(existing.tax_rupees)} ` +
            `of tax on it, entered by ${existing.recorded_by} on ${existing.recorded_at.slice(0, 10)}. The CIN (branch, date, serial) is how the ` +
            "department identifies a payment, so this is the same deposit entered twice — which " +
            "would over-claim it in the return's batch total and be failed by the FVU against " +
            "the .csi file at the portal. If the first entry is wrong it must be corrected, not " +
            "duplicated",
        );
      }

      // ---- PASS 2: each allocation against the FIXTURE deduction. On the server
      // this reads the live row inside the transaction; here it reads a table that
      // was invented. The REFUSALS are the same shape; the evidence behind them is
      // not, which is the gap named at the top of this section.
      const views: TDSChallanAllocation[] = [];
      let allocatedUnits = 0n;
      for (const {
        voucherID,
        section,
        legSeq,
        rawAmount,
        amountUnits,
        triple,
      } of parsed) {
        const deduction = c.tdsDeductions.get(triple);
        if (!deduction) {
          throw new ApiError(
            400,
            `masters: nothing was deducted on voucher ${voucherID} under ${section} at leg ${legSeq}, so there is ` +
              "nothing on it to deposit. Migration 0069 carries no foreign key to tds_deduction (its " +
              "rows are deleted and re-derived on every voucher ALTER), so nothing in the database " +
              "would have stopped this — it would have been stored as a deposit against no deduction, " +
              "visible in no register and reconciling to no deductee",
          );
        }
        const deductionUnits = q4(deduction.tax_rupees);
        if (deductionUnits === 0n) {
          throw new ApiError(
            400,
            `masters: voucher ${deduction.voucher_no} paid ${deduction.deductee_name} under ${section} and deducted NO tax (the payment is ` +
              "below the threshold), so no deposit can be allocated to it. It is still reported — " +
              "under the return's nil challan, which is derived and never stored",
          );
        }
        if (amountUnits !== deductionUnits) {
          throw new ApiError(
            400,
            `masters: voucher ${deduction.voucher_no} deducted ${mockPlainAmount(deduction.tax_rupees)} under ${section} from ` +
              `${deduction.deductee_name}, but ${mockPlainAmount(rawAmount)} is being allocated ` +
              "to it. The two must be EQUAL: the amount stored on the allocation is what makes a " +
              "later voucher ALTER detectable, and reporting treats ANY inequality as an allocation " +
              "broken by an alter — so a deliberately partial allocation would refuse the entire " +
              "quarter at filing time with a message about a change nobody made",
          );
        }
        if (deduction.tax_year_start !== req.tax_year_start) {
          throw new ApiError(
            400,
            `masters: the deduction on voucher ${deduction.voucher_no} under ${section} belongs to tax year ${deduction.tax_year_start}, and ` +
              `this challan is recorded against ${req.tax_year_start}. A challan belongs to ONE tax year — that is why ` +
              "the year is stored on it rather than computed from the deposit date (a challan paid on " +
              "7 April is normally for March of the year before). Record the deposit against the year " +
              "whose tax it paid",
          );
        }
        // ---- ALREADY PAID. This one the mock models exactly, because it is a
        // fact about ITS OWN stored challans and not about a derivation: 409, and
        // the sentence NAMES the challan that paid it, because the operator's next
        // question is "then what is this second slip in my hand".
        const paidBy = c.tdsAllocations.get(triple);
        if (paidBy !== undefined) {
          const other = c.tdsChallans.get(paidBy);
          throw new ApiError(
            409,
            `masters: the ${mockPlainAmount(deduction.tax_rupees)} deducted on voucher ${deduction.voucher_no} under ${section} from ${deduction.deductee_name} was already ` +
              `deposited on challan ${other?.challan_no ?? ""} of ${other?.deposit_date ?? ""} at BSR branch ${other?.bsr_code ?? ""}. Deductee Detail field 5 is a SINGLE ` +
              "challan reference, so a deduction cannot be paid by two challans — if this deposit is " +
              "the right one, the earlier allocation is the thing to correct",
          );
        }

        allocatedUnits += amountUnits;
        views.push({
          voucher_id: voucherID,
          voucher_no: deduction.voucher_no,
          section,
          leg_seq: legSeq,
          deductee_name: deduction.deductee_name,
          allocated_rupees: q4str(amountUnits),
          // Always true / always false here — see the gap list above.
          deduction_on_record: true,
          deduction_tax_rupees: q4str(deductionUnits),
          broken_by_alter: false,
        });
      }

      const challan: TDSChallan = {
        id: id("tds-challan"),
        bsr_code: bsr,
        deposit_date: depositDate,
        challan_no: challanNo,
        by_book_entry: req.by_book_entry === true,
        tax_rupees: q4str(taxUnits),
        interest_rupees: q4str(amounts[1]!.units),
        fee_rupees: q4str(amounts[2]!.units),
        penalty_rupees: q4str(amounts[3]!.units),
        // DERIVED, never stored — a stored total is a column that disagrees with
        // its parts after the first correction.
        total_deposited_rupees: q4str(total),
        minor_head: head.code,
        minor_head_description: head.description,
        tax_year_start: req.tax_year_start,
        tax_year_label: mockTaxYearLabel(req.tax_year_start),
        allocation_count: views.length,
        allocated_total_rupees: q4str(allocatedUnits),
        // CAN BE NEGATIVE, and a few paise either way is the ordinary case: the
        // deposit is whole rupees and a deduction is a 4-dp figure. Reported so a
        // human can read it, never refused on.
        unallocated_tax_rupees: q4str(taxUnits - allocatedUnits),
        // The server's ORDER BY voucher_no, section, leg_seq within a challan.
        allocations: views.sort(
          (x, y) =>
            x.voucher_no.localeCompare(y.voucher_no) ||
            x.section.localeCompare(y.section) ||
            x.leg_seq - y.leg_seq,
        ),
        recorded_by: req.recorded_by,
        recorded_at: new Date().toISOString(),
      };
      c.tdsChallans.set(cin, challan);
      for (const v of views) {
        c.tdsAllocations.set(
          allocationTriple(v.voucher_id, v.section, v.leg_seq),
          cin,
        );
      }
      return {
        id: challan.id,
        tax_year_start: challan.tax_year_start,
        tax_year_label: challan.tax_year_label,
        // The mock's read-back cannot fail, so this is never null and
        // `read_back_error` is never set. See the gap list above.
        challan,
      };
    },

    // ---- THE TDS DEDUCTOR PROFILE (T6, migration 0070).
    //
    // WHAT IT DOES NOT MODEL: the Annexure 1 list of 37 STATE CODES, exactly as
    // the server does not. Both check the two-digit SHAPE only — the list lives in
    // internal/compliance/form140, which neither masters nor server may import —
    // so a screen must NOT tell an operator a state code is valid. "Saved" is all
    // this earns, and whether a return can be built is the register's answer.
    async getTDSProfile(companyID): Promise<TDSProfileResponse> {
      const c = ensure(companyID);
      // A COMPANY WITH NO PROFILE IS 200 WITH A NULL PROFILE, NEVER 404: the
      // absence of a row is the ordinary state of a company that has not filled
      // the form in, and the response still has to carry the vocabulary the empty
      // form is rendered from.
      return {
        profile: c.tdsProfile ? cloneTDSProfile(c.tdsProfile) : null,
        profile_recorded: c.tdsProfile !== undefined,
        deductor_categories: MOCK_DEDUCTOR_CATEGORIES.map((k) => ({ ...k })),
        pan_not_required_literal: MOCK_PAN_NOT_REQUIRED,
      };
    },

    // ---- T2: cost centres and cost categories.
    //
    // ⛔ THE FIXTURE IS DELIBERATELY TWO AXES OVER ONE POSTING, because a
    // single-axis fixture cannot express the model or catch the defect this feature
    // is most likely to ship. Rs 1,00,000 of salary is allocated in full across
    // Departments (Sales 60,000 / Admin 40,000) AND AGAIN in full across Branches
    // (Mumbai 70,000 / Delhi 30,000). Both are complete and both are correct.
    //
    // So a screen that sums across axes reads 2,00,000 against 1,00,000 of cost, and
    // any test asserting a cross-axis total against THIS fixture will show the wrong
    // number rather than a plausible one. That is the point of the numbers.
    async listCostCategories(companyID): Promise<CostCategoryListResponse> {
      ensure(companyID);
      return { cost_categories: mockCostCategories() };
    },
    async createCostCategory(
      companyID,
      req: CreateCostCategoryRequest,
    ): Promise<CostCategory> {
      ensure(companyID);
      const name = (req.name ?? "").trim();
      if (name === "") {
        throw new ApiError(400, "masters: invalid cost master: a cost category needs a name");
      }
      // ⛔ THE MOCK MUST REFUSE WHAT THE SERVER REFUSES, and this check exists
      // because it did not. The screen originally sent no created_by; the server
      // answered 400, the mock answered 201, and all 597 frontend tests passed
      // against a create path that could not work in the browser — the failure
      // surfaced only as a console 400 AFTER a successful WebAuthn step-up, which
      // reads as "the passkey failed" and is not. A mock more permissive than the
      // server does not model the server; it hides it.
      if ((req.created_by ?? "").trim() === "") {
        throw new ApiError(400, "masters: invalid cost master: created_by is required");
      }
      // Case- AND whitespace-insensitive, matching the server's
      // lower(btrim(name)) unique index. A Go check that normalised differently
      // would accept a name the database then rejects.
      const clash = mockCostCategories().find(
        (c) => c.name.trim().toLowerCase() === name.toLowerCase(),
      );
      if (clash) {
        throw new ApiError(
          409,
          `masters: a cost category called "${clash.name}" already exists for this company; two axes of the same name would split one allocation set in half and both halves would look complete`,
        );
      }
      return {
        id: `cost-cat-${name.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`,
        name,
        allocate_non_revenue: req.allocate_non_revenue ?? false,
        centre_count: 0,
        created_by: "browser-dev-owner",
        created_at: "2026-08-05T00:00:00Z",
      };
    },
    async renameCostCategory(
      companyID,
      categoryID: string,
      req: CreateCostCategoryRequest,
    ): Promise<CostCategory> {
      ensure(companyID);
      const existing = mockCostCategories().find((c) => c.id === categoryID);
      if (!existing) {
        throw new ApiError(404, "masters: there is no cost category with that id for this company");
      }
      return { ...existing, name: (req.name ?? "").trim() || existing.name };
    },
    async listCostCentres(
      companyID,
      costCategoryID?: string,
    ): Promise<CostCentreListResponse> {
      ensure(companyID);
      const cats = mockCostCategories();
      if (costCategoryID && !cats.some((c) => c.id === costCategoryID)) {
        // A 404 and NOT an empty list. "There is no such axis" and "this axis has
        // no centres" are different answers, and a screen that renders them the
        // same leaves an operator unable to tell a wrong id from unkeyed data.
        throw new ApiError(
          404,
          'masters: there is no cost category with that id for this company, so there are no cost centres to list on it. This is not the same answer as "the axis is empty"',
        );
      }
      const all = mockCostCentres();
      return {
        cost_centres: costCategoryID
          ? all.filter((c) => c.cost_category_id === costCategoryID)
          : all,
        cost_categories: cats,
        ...(costCategoryID ? { cost_category_id: costCategoryID } : {}),
      };
    },
    async createCostCentre(
      companyID,
      req: CreateCostCentreRequest,
    ): Promise<CostCentreWriteResponse> {
      ensure(companyID);
      const cats = mockCostCategories();
      const axis = cats.find((c) => c.id === req.cost_category_id);
      if (!axis) {
        throw new ApiError(
          400,
          "masters: a cost centre must name the cost category it sits on — there is no uncategorised centre, because a centre with no axis cannot be grouped",
        );
      }
      const name = (req.name ?? "").trim();
      if (name === "") {
        throw new ApiError(400, "masters: invalid cost master: a cost centre needs a name");
      }
      // See createCostCategory: the mock refuses what the server refuses.
      if ((req.created_by ?? "").trim() === "") {
        throw new ApiError(400, "masters: invalid cost master: created_by is required");
      }
      if (req.parent_id === "00000000-0000-0000-0000-000000000000") {
        throw new ApiError(
          400,
          "parent_id is all zeros, which is not a cost centre: send the field EMPTY to put this centre at the top of its axis",
        );
      }
      // ⛔ UNIQUE WITHIN THE AXIS, NOT THE COMPANY. The same name on a different
      // axis is legal — "Mumbai" may be both a Branch and a Project — so the
      // message NAMES THE AXIS. Without that it reads as a bug to an operator who
      // can see the other Mumbai is a different kind of thing.
      const clash = mockCostCentres().find(
        (c) =>
          c.cost_category_id === req.cost_category_id &&
          c.name.trim().toLowerCase() === name.toLowerCase(),
      );
      if (clash) {
        throw new ApiError(
          409,
          `masters: "${clash.name}" is already a cost centre on the ${axis.name} axis. The same name on a DIFFERENT axis is allowed`,
        );
      }
      const parent = req.parent_id
        ? mockCostCentres().find((c) => c.id === req.parent_id)
        : undefined;
      if (req.parent_id && !parent) {
        throw new ApiError(404, "masters: there is no cost centre with that id to sit under");
      }
      if (parent && parent.cost_category_id !== req.cost_category_id) {
        throw new ApiError(
          422,
          `masters: ${parent.name} sits on the ${parent.cost_category_name} axis, so a ${axis.name} centre cannot sit under it — one axis would roll up into the other and the Category Summary would double-count`,
        );
      }
      const id = `cost-ctr-${name.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`;
      // ⛔ THE ENVELOPE, because that is what the server sends. This mock used to
      // return the view FLAT, which typed-checked against a lying declaration and
      // hid a real defect: the masters screen read created.path and got undefined,
      // confirming a successful create with "Created undefined on the undefined
      // axis". 597 tests passed. A mock whose SHAPE differs from the server's does
      // not model the server, it conceals it.
      return {
        id,
        cost_centre: {
          id,
          name,
          cost_category_id: axis.id,
          cost_category_name: axis.name,
          parent_id: parent ? parent.id : "",
          parent_name: parent ? parent.name : "",
          depth: parent ? parent.depth + 1 : 0,
          path: parent ? `${parent.path} / ${name}` : name,
          ancestry_truncated: false,
          created_by: "browser-dev-owner",
          created_at: "2026-08-05T00:00:00Z",
        },
      };
    },
    async alterCostCentre(
      companyID,
      centreID: string,
      req: CreateCostCentreRequest,
    ): Promise<CostCentreWriteResponse> {
      ensure(companyID);
      const existing = mockCostCentres().find((c) => c.id === centreID);
      if (!existing) {
        throw new ApiError(404, "masters: there is no cost centre with that id for this company");
      }
      if (req.parent_id === "00000000-0000-0000-0000-000000000000") {
        throw new ApiError(
          400,
          "parent_id is all zeros, which is not a cost centre: send the field EMPTY to put this centre at the top of its axis — the all-zeros uuid would silently move it to the top and change which totals it rolls into",
        );
      }
      return {
        id: existing.id,
        cost_centre: { ...existing, name: (req.name ?? "").trim() || existing.name },
      };
    },

    async costCentreSummary(
      companyID,
      costCategoryID: string,
      from: string,
      to: string,
    ): Promise<CostCentreSummaryResponse> {
      ensure(companyID);
      const axis = mockCostCategories().find((c) => c.id === costCategoryID);
      if (!costCategoryID) {
        throw new ApiError(
          400,
          "cost_category_id is required: a Cost Centre Summary is always for ONE axis, because centres on different axes describe the same money and summing them double-counts",
        );
      }
      if (!axis) {
        throw new ApiError(422, "there is no cost category with that id for this company");
      }
      const centres = mockCostCentres().filter((c) => c.cost_category_id === costCategoryID);
      const nodes = centres.map((c) => {
        const own = mockAllocationFor(c.id);
        return {
          cost_centre_id: c.id,
          name: c.name,
          parent_id: c.parent_id,
          depth: c.depth,
          own_debit: own,
          own_credit: "0.0000",
          own_net: own,
          total_debit: own,
          total_credit: "0.0000",
          total_net: own,
          children: [],
        };
      });
      const total = nodes
        .reduce((acc, n) => acc + Math.round(Number(n.total_debit) * 10000), 0)
        .toString();
      const totalFixed = (Number(total) / 10000).toFixed(4);
      return {
        cost_category_id: axis.id,
        cost_category: axis.name,
        allocate_non_revenue: axis.allocate_non_revenue,
        from,
        to,
        centres: nodes,
        unattached: [],
        total_debit: totalFixed,
        total_credit: "0.0000",
        total_net: totalFixed,
        unattached_debit: "0.0000",
        unattached_credit: "0.0000",
        allocated_debit: totalFixed,
        allocated_credit: "0.0000",
        difference_debit: "0.0000",
        difference_credit: "0.0000",
        reconciles: true,
        warnings: [
          `This total is for the ${axis.name} axis only. It must never be added to another axis's total: every axis allocates the SAME money, so a cross-axis sum is a multiple of the cost actually borne.`,
        ],
      };
    },
    async costCategorySummary(
      companyID,
      from: string,
      to: string,
    ): Promise<CostCategorySummaryResponse> {
      ensure(companyID);
      // Both axes allocate the SAME Rs 1,00,000 in full, and the period's allocable
      // base is Rs 1,20,000 — so each axis independently leaves Rs 20,000
      // unallocated. THAT GAP IS THE REPORT'S WHOLE PURPOSE: the sum trigger
      // guarantees an allocated posting is allocated in full, but nothing forces a
      // posting to be allocated at all.
      const rows = mockCostCategories().map((c) => ({
        cost_category_id: c.id,
        cost_category: c.name,
        allocate_non_revenue: c.allocate_non_revenue,
        allocable_debit: "120000.0000",
        allocable_credit: "0.0000",
        allocable_postings: 2,
        allocated_debit: "100000.0000",
        allocated_credit: "0.0000",
        allocated_postings: 1,
        unallocated_debit: "20000.0000",
        unallocated_credit: "0.0000",
        unallocated_postings: 1,
        fully_allocated: false,
        partly_allocated_postings: 0,
        partly_allocated_shortfall: "0.0000",
        off_axis_debit: "0.0000",
        off_axis_credit: "0.0000",
        off_axis_postings: 0,
        difference_debit: "0.0000",
        difference_credit: "0.0000",
        reconciles: true,
        notes: [
          `20000.0000 of debit and 0.0000 of credit — 1 of the 2 posting(s) this axis covers — carry NO ${c.name} allocation at all. That money is real cost with no ${c.name} attached to it: it is in the Profit & Loss and it is missing from every ${c.name} report.`,
        ],
      }));
      return {
        from,
        to,
        rows,
        reconciles: true,
        notes: [
          "These rows must NOT be added together. Each is a different axis through the SAME money, so a total across them is a multiple of the cost actually borne.",
        ],
      };
    },

    async setTDSProfile(
      companyID,
      req: SetTDSProfileRequest,
    ): Promise<SetTDSProfileResponse> {
      const c = ensure(companyID);
      const invalid = (why: string) =>
        new ApiError(400, `masters: invalid TDS deductor profile: ${why}`);
      const up = (s: string | undefined) => (s ?? "").trim().toUpperCase();
      const tr = (s: string | undefined) => (s ?? "").trim();

      // NORMALISED FIRST, then validated — mirroring the server, so a TAN keyed
      // in lower case is stored in capitals and the read-back SHOWS that.
      const tan = up(req.tan);
      const pan = up(req.pan);
      const category = up(req.deductor_category);
      const stateNameCode = tr(req.state_name_code);
      const ministryCode = tr(req.ministry_code);
      const ministryOther = tr(req.ministry_other);

      if (!/^[A-Z]{4}[0-9]{5}[A-Z]$/.test(tan)) {
        throw invalid(
          `"${tan}" is not a TAN. A Tax Deduction and Collection Account ` +
            "Number is four letters, five digits and a letter (AAAA99999A) — it is NOT " +
            "the GSTIN and cannot be derived from it, and a wrong one is rejected against " +
            "the .csi challan file after the operator believes they have filed",
        );
      }
      const panShape = /^[A-Z]{5}[0-9]{4}[A-Z]$/;
      if (!panShape.test(pan) && pan !== MOCK_PAN_NOT_REQUIRED) {
        throw invalid(
          `the deductor's PAN "${pan}" is not five letters, four digits and a ` +
            `letter (AAAAA9999A). Where a deductor is not required to hold one the format ` +
            `prescribes the literal ${MOCK_PAN_NOT_REQUIRED} — a real value with a meaning, never a blank`,
        );
      }
      // NO PANNOTREQD ALTERNATIVE for the responsible person: they are a named
      // human and the constraint gives them no exemption.
      const responsiblePAN = up(req.responsible?.pan);
      if (!panShape.test(responsiblePAN)) {
        throw invalid(
          `the responsible person's own PAN "${responsiblePAN}" is not five letters, four ` +
            "digits and a letter. It is a DIFFERENT field from the deductor's PAN (BH field " +
            `59 against field 15) and ${MOCK_PAN_NOT_REQUIRED} is not accepted for it`,
        );
      }
      const cat = MOCK_DEDUCTOR_CATEGORIES.find((k) => k.code === category);
      if (!cat) {
        throw invalid(
          `"${category}" is not one of Annexure 4's sixteen deductor category codes ` +
            `(${MOCK_DEDUCTOR_CATEGORIES.map((k) => k.code).join(", ")}). It is a single letter that goes in BH field 32, and it is NOT the ` +
            "s.402(11) entity category recorded against the tax year — mapping between the " +
            "two is a decision, not a derivation",
        );
      }
      // "Contains no letter or digit" — the format refuses a name that is only
      // blanks, dots or special characters, which is a different (and weaker)
      // test than "is not empty".
      const alnum = /[A-Za-z0-9]/;
      for (const [what, value] of [
        ["the deductor name (BH field 6)", tr(req.deductor_name)],
        [
          "the responsible person's name (BH field 33)",
          tr(req.responsible?.name),
        ],
        [
          "the responsible person's designation (BH field 34)",
          tr(req.responsible?.designation),
        ],
      ] as const) {
        if (!alnum.test(value)) {
          throw invalid(
            `${what} is "${value}", which contains no letter or digit. The format ` +
              "refuses a name that is only blanks, dots or special characters",
          );
        }
      }
      for (const [what, value] of [
        [
          "the deductor's contact country code (BH field 29)",
          tr(req.contact_country_code),
        ],
        ["the deductor's contact number (BH field 30)", tr(req.contact_number)],
        [
          "the responsible person's contact country code (BH field 44)",
          tr(req.responsible?.contact_country_code),
        ],
        [
          "the responsible person's contact number (BH field 45)",
          tr(req.responsible?.contact_number),
        ],
      ] as const) {
        if (value === "") {
          throw invalid(
            `${what} is mandatory; this format version splits the calling ` +
              "code from the number, so both are required",
          );
        }
      }
      // ONE e-mail is enough and NONE is not: the format prints both as mandatory
      // and then makes each conditional on the other.
      if (tr(req.email) === "" && tr(req.responsible?.email) === "") {
        throw invalid(
          "an e-mail address is required — the format asks for the " +
            "deductor's (BH field 28) OR the responsible person's (BH field 42), each " +
            "mandatory only if the other is absent, and neither was given",
        );
      }
      for (const [whose, addr] of [
        ["the deductor", req.address],
        ["the responsible person", req.responsible?.address],
      ] as const) {
        if (tr(addr?.line1) === "") {
          throw invalid(
            `${whose}'s address line 1 is mandatory. The return wants five ` +
              "separately numbered lines rather than one block of text, and line 1 is the " +
              "flat/door/block number",
          );
        }
        if (tr(addr?.country) === "") {
          throw invalid(`${whose}'s country/region is mandatory`);
        }
        // SHAPE ONLY — see the note above the method.
        if (!/^[0-9]{2}$/.test(tr(addr?.state_code))) {
          throw invalid(
            `${whose}'s state code "${tr(addr?.state_code)}" is not two digits. The field carries the ` +
              "two-digit Annexure 1 CODE, not the name of the state",
          );
        }
        if (!/^[0-9]{6}$/.test(tr(addr?.pincode))) {
          throw invalid(
            `${whose}'s pincode "${tr(addr?.pincode)}" is not six digits`,
          );
        }
      }
      // ---- The two CATEGORY-CONDITIONAL rules, and each is conditional in BOTH
      // directions: a value is mandatory for some categories and FORBIDDEN for the
      // rest, so emptiness is tested as well as presence. This is the pair a
      // client that hard-coded the category list would get wrong.
      if (cat.state_name_code_required) {
        if (!/^[0-9]{2}$/.test(stateNameCode)) {
          throw invalid(
            `deductor category ${cat.code} (${cat.description}) requires the Annexure 5 state ` +
              `code in BH field 54, and "${stateNameCode}" is not two digits`,
          );
        }
      } else if (stateNameCode !== "") {
        throw invalid(
          `BH field 54 (State Name) carries "${stateNameCode}", but the format allows a ` +
            `value only for the four State-Government categories S, E, H and N — not ${cat.code} ` +
            `(${cat.description}). Clear it rather than choosing a code that will be refused`,
        );
      }
      if (cat.ministry_required) {
        if (ministryCode === "") {
          throw invalid(
            `deductor category ${cat.code} (${cat.description}) requires an Annexure 3 ministry ` +
              "code in BH field 57",
          );
        }
      } else if (!cat.ministry_allowed && ministryCode !== "") {
        throw invalid(
          `BH field 57 (Ministry Name) carries "${ministryCode}", but the format allows ` +
            `no ministry for deductor category ${cat.code} (${cat.description})`,
        );
      }
      // An EQUIVALENCE: the code is 99 exactly when field 58 names the ministry.
      if (ministryCode === MOCK_MINISTRY_OTHER && ministryOther === "") {
        throw invalid(
          `ministry code ${MOCK_MINISTRY_OTHER} is "Other", so BH field 58 must name the ministry`,
        );
      }
      if (ministryCode !== MOCK_MINISTRY_OTHER && ministryOther !== "") {
        throw invalid(
          `BH field 58 names the ministry "${ministryOther}", but field 57 is "${ministryCode}" rather ` +
            `than the code ${MOCK_MINISTRY_OTHER} ("Other") that field 58 exists to expand`,
        );
      }
      if (tr(req.recorded_by) === "") {
        throw invalid("recorded_by is required");
      }

      const stored: TDSProfile = {
        tan,
        pan,
        deductor_name: tr(req.deductor_name),
        deductor_category: category,
        address: {
          line1: tr(req.address?.line1),
          line2: tr(req.address?.line2),
          line3: tr(req.address?.line3),
          line4: tr(req.address?.line4),
          line5: tr(req.address?.line5),
          country: tr(req.address?.country),
          state_code: tr(req.address?.state_code),
          pincode: tr(req.address?.pincode),
        },
        email: tr(req.email),
        contact_country_code: tr(req.contact_country_code),
        contact_number: tr(req.contact_number),
        state_name_code: stateNameCode,
        ministry_code: ministryCode,
        ministry_other: ministryOther,
        ain: tr(req.ain),
        gstin: up(req.gstin),
        responsible: {
          name: tr(req.responsible?.name),
          designation: tr(req.responsible?.designation),
          pan: responsiblePAN,
          address: {
            line1: tr(req.responsible?.address?.line1),
            line2: tr(req.responsible?.address?.line2),
            line3: tr(req.responsible?.address?.line3),
            line4: tr(req.responsible?.address?.line4),
            line5: tr(req.responsible?.address?.line5),
            country: tr(req.responsible?.address?.country),
            state_code: tr(req.responsible?.address?.state_code),
            pincode: tr(req.responsible?.address?.pincode),
          },
          email: tr(req.responsible?.email),
          contact_country_code: tr(req.responsible?.contact_country_code),
          contact_number: tr(req.responsible?.contact_number),
        },
        recorded_by: tr(req.recorded_by),
        // Set HERE and not taken from the request, exactly as the column is: the
        // audit timestamp is the database's statement, not the caller's.
        recorded_at: new Date().toISOString(),
      };
      // A WHOLE-ROW UPSERT. There is one profile per company (the company IS the
      // primary key), so a second PUT restates it and never merges into it —
      // which is what makes the conditional fields checkable at all.
      c.tdsProfile = stored;
      return { profile: cloneTDSProfile(stored), profile_recorded: true };
    },

    // ---- BANK RECONCILIATION (T4).
    async bankReconciliation(
      companyID,
      ledgerID,
      asOn?,
    ): Promise<BankReconciliationResponse> {
      const c = ensure(companyID);
      const ledger = c.ledgers.find((l) => l.id === ledgerID);
      if (!ledger) {
        throw new ApiError(400, `reporting: ledger ${ledgerID} not found`);
      }
      if (!isBankLedger(ledger)) {
        // Mirrors the server's diagnostic refusal: NAME the problem rather than
        // return an empty statement that reads as "nothing to reconcile".
        throw new ApiError(
          400,
          `reporting: ledger "${ledger.name}" is not under a bank group; a bank reconciliation statement needs a bank ledger`,
        );
      }
      const asOnDate = asOn && asOn !== "" ? asOn : todayISO();

      const uncleared: BankReconLine[] = [];
      const cleared: BankReconLine[] = [];
      let unclearedNet = 0n;
      let brokenCount = 0;

      for (const v of c.vouchers) {
        if (v.date > asOnDate) continue;
        // One line per POSTING on this bank ledger — leg_no is the ordinal, so a
        // voucher touching the bank twice yields two independently tickable rows.
        let legNo = 0;
        for (const p of v.postings) {
          if (p.ledger !== ledger.name) continue;
          const key = `${ledgerID}:${v.voucher_id}:${legNo}`;
          const rec = c.bankDates.get(key);
          const signed = p.dr_cr === "D" ? q4(p.amount) : -q4(p.amount);

          // A reconciliation is only in force if it matches what was cleared AND
          // the bank date is on or before the statement date. An item cleared
          // LATER is un-cleared as at this date — t4-model.md's first trap.
          const broken =
            !!rec &&
            (rec.clearedAmount !== p.amount || rec.clearedDrCr !== p.dr_cr);
          const inForce = !!rec && !broken && rec.bankDate <= asOnDate;

          const line: BankReconLine = {
            voucher_id: v.voucher_id,
            leg_no: legNo,
            voucher_no: v.voucher_no,
            voucher_date: v.date,
            narration: v.narration,
            party: v.party,
            amount: p.amount,
            dr_cr: p.dr_cr,
            bank_date: inForce ? rec!.bankDate : "",
            age_days: daysBetween(v.date, asOnDate),
            broken_by_alter: broken,
          };
          if (broken) {
            brokenCount++;
            line.cleared_amount = rec!.clearedAmount;
            line.cleared_dr_cr = rec!.clearedDrCr;
          }

          if (inForce) {
            cleared.push(line);
          } else {
            uncleared.push(line);
            unclearedNet += signed;
          }
          legNo++;
        }
      }

      // Oldest first: an item outstanding for 90+ days is a stale cheque, and
      // surfacing it is the most useful thing this statement does.
      uncleared.sort((a, b) => a.voucher_date.localeCompare(b.voucher_date));
      cleared.sort((a, b) => a.bank_date.localeCompare(b.bank_date));

      // Orientation follows the ledger's NATURE. A Bank OD A/c is a LIABILITY, so
      // its book balance is a credit — t4-model.md §3.1's sign trap.
      const isOD = (ledger.bankParentGroup ?? ledger.group) === "Bank OD A/c";
      let books = 0n;
      for (const v of c.vouchers) {
        if (v.date > asOnDate) continue;
        for (const p of v.postings) {
          if (p.ledger !== ledger.name) continue;
          books += p.dr_cr === "D" ? q4(p.amount) : -q4(p.amount);
        }
      }
      if (isOD) {
        books = -books;
        unclearedNet = -unclearedNet;
      }
      const perBank = books - unclearedNet;

      return {
        ledger_id: ledgerID,
        ledger_name: ledger.name,
        as_on: asOnDate,
        balance_as_per_books: q4str(books),
        uncleared_net: q4str(unclearedNet),
        balance_as_per_bank: q4str(perBank),
        // Zero by construction: the statement asserts its own arithmetic rather
        // than assuming it.
        difference: q4str(books - unclearedNet - perBank),
        balanced: books - unclearedNet - perBank === 0n,
        broken_count: brokenCount,
        uncleared,
        cleared,
      };
    },

    async setBankDate(
      companyID,
      ledgerID,
      voucherID,
      legNo,
      req: SetBankDateRequest,
    ): Promise<SetBankDateResponse> {
      const c = ensure(companyID);
      const ledger = c.ledgers.find((l) => l.id === ledgerID);
      if (!ledger || !isBankLedger(ledger)) {
        throw new ApiError(400, "banking: not a bank ledger");
      }
      if (!req.bank_date) {
        throw new ApiError(400, "bank_date is required");
      }
      const v = c.vouchers.find((e) => e.voucher_id === voucherID);
      // The leg must be on the voucher's CURRENT version. 404, not 403: a 403
      // would confirm that some other leg exists.
      const legs = v?.postings.filter((p) => p.ledger === ledger.name) ?? [];
      const leg = legs[legNo];
      if (!v || !leg) {
        throw new ApiError(
          404,
          "banking: leg not found on this voucher version",
        );
      }
      // Migration 0045's trigger: the bank cannot clear an item before it exists.
      if (req.bank_date < v.date) {
        throw new ApiError(
          400,
          `banking: bank_date ${req.bank_date} is before the voucher date ${v.date}`,
        );
      }
      c.bankDates.set(`${ledgerID}:${voucherID}:${legNo}`, {
        bankDate: req.bank_date,
        clearedAmount: leg.amount,
        clearedDrCr: leg.dr_cr,
      });
      return { status: "reconciled" };
    },

    async clearBankDate(
      companyID,
      ledgerID,
      voucherID,
      legNo,
    ): Promise<SetBankDateResponse> {
      const c = ensure(companyID);
      // IDEMPOTENT: clearing an already-unreconciled leg is a 200. The caller's
      // intent is satisfied either way, and making a double-click an error is
      // friction with no safety gain.
      c.bankDates.delete(`${ledgerID}:${voucherID}:${legNo}`);
      return { status: "unreconciled" };
    },

    // ---- BANK STATEMENT IMPORT (T4 slice 2a).
    //
    // See `bankStatements` on the company state for the list of what this mock
    // deliberately does NOT check. The short version: it is a reader, not the
    // parser, and four of the server's refusals do not exist here.
    async previewBankStatement(
      companyID,
      ledgerID,
      req: PreviewStatementRequest,
    ): Promise<PreviewStatementResponse> {
      const c = ensure(companyID);
      mockRequireBankLedger(c, ledgerID);
      const header = mockCSVHeader(req.content);

      // ⚠️ THE MOCK PROPOSES NOTHING, ON PURPOSE. The server proposes from a
      // synonym list; copying that list here would be a second copy free to
      // drift, and the drift would be invisible — a screen test would pin the
      // mock's guess and pass while the server proposed something else.
      //
      // Returning an EMPTY mapping with an unresolved entry is also the safer
      // shape for the screen: it exercises the path where the operator must
      // choose, which is the path the design rests on.
      return {
        file_name: req.file_name,
        header,
        mapping: mockEmptyMapping(),
        unresolved: [
          "the mock does not propose mappings — every column must be chosen here. " +
            "The server proposes from the file's own header row; see docs/design/t4-slice2-model.md §8.2.",
        ],
        notes: [],
        known_date_layouts: MOCK_DATE_LAYOUTS,
        known_encodings: ["two-columns", "signed-amount", "amount-and-flag"],
      };
    },

    async importBankStatement(
      companyID,
      ledgerID,
      req: ImportStatementRequest,
    ): Promise<ImportStatementResponse> {
      const c = ensure(companyID);
      mockRequireBankLedger(c, ledgerID);
      const store = c.bankStatements.get(ledgerID) ?? {
        imports: [],
        lines: [],
      };
      c.bankStatements.set(ledgerID, store);

      // Only an identical FILE is caught, unlike the server, which dedups by
      // TRANSACTION. Named in the state comment.
      const already = store.imports.find((i) => i.file_name === req.file_name);
      if (already) {
        return {
          import_id: already.import_id,
          already_imported: true,
          lines_in_file: already.line_count,
          new_lines: 0,
          duplicate_lines: already.line_count,
          opening_balance: "0.0000",
          closing_balance: "0.0000",
          balance_walked: false,
          period_from: already.period_from,
          period_to: already.period_to,
          disclosures: [MOCK_IMPORT_DISCLOSURE],
        };
      }

      const importID = `mock-import-${store.imports.length + 1}`;
      const parsed = mockParseStatement(req.content, req.mapping, importID);
      if (parsed.length === 0) {
        throw new ApiError(
          400,
          "bankstatement: the file has no transaction rows: it has a header row and nothing under it",
        );
      }
      store.lines.push(...parsed);
      const dates = parsed.map((l) => l.value_date).sort();
      const im: BankStatementImport = {
        import_id: importID,
        file_name: req.file_name,
        imported_by: req.created_by,
        imported_at: new Date().toISOString(),
        period_from: dates[0]!,
        period_to: dates[dates.length - 1]!,
        line_count: parsed.length,
        // FALSE ALWAYS: this mock does not walk balances. Reporting true would
        // make a screen render "checked" over a check that never ran.
        balance_walked: false,
      };
      store.imports.unshift(im);

      return {
        import_id: importID,
        already_imported: false,
        lines_in_file: parsed.length,
        new_lines: parsed.length,
        duplicate_lines: 0,
        opening_balance: "0.0000",
        closing_balance: "0.0000",
        balance_walked: false,
        period_from: im.period_from,
        period_to: im.period_to,
        disclosures: [MOCK_IMPORT_DISCLOSURE],
      };
    },

    async importedStatement(
      companyID,
      ledgerID,
      from?,
      to?,
    ): Promise<ImportedStatementResponse> {
      const c = ensure(companyID);
      const ledger = mockRequireBankLedger(c, ledgerID);
      const store = c.bankStatements.get(ledgerID) ?? {
        imports: [],
        lines: [],
      };
      const inPeriod = (d: string) =>
        (!from || d >= from) && (!to || d <= to);
      const lines = store.lines.filter((l) => inPeriod(l.value_date));

      // Money is summed as PAISE INTEGERS, never parseFloat: every money value
      // in this product is a fixed-4 decimal string and a float would round.
      let inPaise = 0n;
      let outPaise = 0n;
      for (const l of lines) {
        const v = mockMoneyToUnits(l.amount);
        if (l.dr_cr === "D") inPaise += v;
        else outPaise += v;
      }

      const disclosures = [
        "MATCHING IS NOT BUILT YET (T4 slice 2b). These are the bank's own rows; nothing here " +
          "says whether a row has been matched to a voucher or cleared. Reconciling is still done on the " +
          "Bank Reconciliation screen by entering a Bank Date against each voucher.",
      ];
      for (const im of store.imports) {
        if (!im.balance_walked) {
          disclosures.push(
            `"${im.file_name}" carried no running balance, so its rows were NOT balance-walked: a row dropped or ` +
              "duplicated by the export would not have been detected.",
          );
        }
      }
      if (!ledger.bankAccountNumber || !ledger.bankIFSC) {
        disclosures.push(
          "This ledger has no account number or IFSC recorded, so imports against it cannot be checked " +
            "against the file's own account. Record them on the ledger.",
        );
      }

      return {
        ledger_id: ledgerID,
        ledger_name: ledger.name,
        bank_account_number: ledger.bankAccountNumber ?? "",
        bank_ifsc: ledger.bankIFSC ?? "",
        from: from ?? "",
        to: to ?? "",
        imports: store.imports.filter(
          (i) => inPeriod(i.period_from) || inPeriod(i.period_to),
        ),
        lines,
        total_in: mockUnitsToMoney(inPaise),
        total_out: mockUnitsToMoney(outPaise),
        disclosures,
      };
    },

    /**
     * THE AGEING REPORT, netted from the stored bill references.
     *
     * ⛔ THIS USED TO RETURN AN EMPTY PARTY LIST UNCONDITIONALLY, and that is
     * §9.4's FIRST kind of mock defect: not a wrong answer but a SILENTLY DROPPED
     * one. The bill-wise settlement picker lists a party's open bills from this
     * call, so against the old mock it showed "no open bills" for every customer —
     * and a developer driving the mock would conclude their own picker was broken
     * rather than that the mock had never implemented the report.
     *
     * ⚠️ It nets by **(ledger_id, ref_name)** and ignores `ref_type`, because that
     * is exactly what reporting.Outstandings does. A settlement matches its bill
     * by NAME or it does not match at all; a mock that netted by type would hide
     * the single most likely defect on this path.
     *
     * ⚠️ THE SIMPLIFICATION, stated rather than hidden: the server splits
     * receivable from payable on the account GROUP'S NATURE (ASSET vs LIABILITY),
     * joined from account_group. This store keeps a group NAME and no nature, so
     * it classifies on the two seeded party groups. A company whose debtors sit
     * under a custom group is reported correctly by cored and not by this mock —
     * which is a fidelity gap in the direction that shows too little, never too
     * much.
     */
    async getOutstandings(
      companyID,
      query?: OutstandingsQuery,
    ): Promise<OutstandingsResponse> {
      const c = ensure(companyID);
      const kind = query?.kind ?? "RECEIVABLE";
      const wantGroup = kind === "PAYABLE" ? "Sundry Creditors" : "Sundry Debtors";
      const asOn = query?.as_on || new Date().toISOString().slice(0, 10);

      // Net every reference on a CURRENT, non-deleted voucher. c.views holds
      // exactly those: an ALTER restates the view in place and a DELETE removes it.
      const net = new Map<string, { ledgerID: string; ref: string; units: bigint; due: string }>();
      for (const view of c.views.values()) {
        for (const b of view.bill_allocations) {
          const ledger = c.ledgers.find((l) => l.id === b.ledger_id);
          if (!ledger || ledger.group !== wantGroup) continue;
          // Positive on the party's natural side: debit for a receivable.
          const signed =
            b.dr_cr === "D"
              ? mockMoneyToUnits(b.amount)
              : -mockMoneyToUnits(b.amount);
          const key = `${b.ledger_id} ${b.ref_name}`;
          const cur = net.get(key) ?? {
            ledgerID: b.ledger_id,
            ref: b.ref_name,
            units: 0n,
            due: "",
          };
          cur.units += kind === "PAYABLE" ? -signed : signed;
          // The EARLIEST due date on the reference, as the server takes MIN().
          if (b.due_date && (cur.due === "" || b.due_date < cur.due)) cur.due = b.due_date;
          net.set(key, cur);
        }
      }

      const SLABS: [string, number][] = [
        ["0-45 days", 45],
        ["45-90 days", 90],
        ["90-180 days", 180],
        ["180+ days", 0],
      ];
      const slabTotals = new Map<string, bigint>();
      const byParty = new Map<string, OutstandingParty>();
      let total = 0n;

      for (const { ledgerID, ref, units, due } of [...net.values()].sort((a, b) =>
        a.ref.localeCompare(b.ref),
      )) {
        // A fully-settled bill nets to zero and is not outstanding.
        if (units === 0n) continue;
        const ledger = c.ledgers.find((l) => l.id === ledgerID);
        if (!ledger) continue;
        const overdue =
          due === ""
            ? 0
            : Math.max(
                0,
                Math.round(
                  (Date.parse(asOn) - Date.parse(due)) / 86_400_000,
                ),
              );
        const slab = SLABS.find(([, upto]) => upto !== 0 && overdue <= upto)?.[0] ?? "180+ days";
        const party =
          byParty.get(ledgerID) ??
          ({
            ledger_id: ledgerID,
            ledger: ledger.name,
            group: ledger.group,
            pending: "0.0000",
            bills: [],
          } satisfies OutstandingParty);
        party.bills.push({
          ref_name: ref,
          pending: mockUnitsToMoney(units),
          due_date: due,
          overdue_days: overdue,
          slab,
        });
        party.pending = mockUnitsToMoney(mockMoneyToUnits(party.pending) + units);
        byParty.set(ledgerID, party);
        slabTotals.set(slab, (slabTotals.get(slab) ?? 0n) + units);
        total += units;
      }

      return {
        kind,
        as_on: asOn,
        parties: [...byParty.values()].sort((a, b) => a.ledger.localeCompare(b.ledger)),
        slab_totals: SLABS.filter(([l]) => slabTotals.has(l)).map(([label]) => ({
          label,
          amount: mockUnitsToMoney(slabTotals.get(label) ?? 0n),
        })),
        total: mockUnitsToMoney(total),
      };
    },

    async generateGSTR1(companyID, query: GSTR1Query): Promise<GSTR1Response> {
      ensure(companyID);
      const fp = query.from.slice(0, 7).replace("-", "");
      const base = { gstin: query.gstin, fp, hsn: { data: [] } };
      if (
        query.cgst_ledger_id ||
        query.sgst_ledger_id ||
        query.igst_ledger_id
      ) {
        return { gstr1: base, reconciliation_ok: true };
      }
      return base;
    },

    async query(companyID, body: QueryRequest): Promise<QueryResult> {
      ensure(companyID);
      // The mock does not run the deterministic DSL->SQL compiler; it returns a
      // small, contract-shaped canned answer so screens that depend on the
      // CoredClient interface still type-check and render. Tests that exercise
      // QueryScreen inject their own client returning a richer QueryResult.
      const dsl: QueryResult["dsl"] = body.dsl ?? {
        entity: "ledger_balances",
        metrics: [{ func: "sum", column: "debit", as: "total_debit" }],
        dimensions: ["group_name"],
        filters: [],
        date_range: null,
        order_by: [],
        limit: 100,
      };
      return {
        dsl,
        sql_preview: "SELECT ... FROM ... WHERE ... -- mock (no compiler)",
        columns: [{ name: "result", type: "text" }],
        rows: [],
      };
    },

    // ---- SaaS admin surface -------------------------------------------
    async getMe(): Promise<MeResponse> {
      const mine = [...companies.values()].filter((c) =>
        c.members.some((m) => m.user_id === MOCK_USER_ID),
      );
      return {
        user_id: MOCK_USER_ID,
        subject: MOCK_SUBJECT,
        companies: mine.map((c) => {
          const m = c.members.find((x) => x.user_id === MOCK_USER_ID);
          return {
            company_id: c.id,
            name: c.name,
            security_level_id: m?.security_level_id ?? "",
            security_level_name: m?.security_level_name ?? "",
            is_owner: m?.is_owner ?? false,
          };
        }),
        // The mock has one identity, so there is never a pending invite for it.
        claimed_invites: [],
      };
    },

    async listMembers(companyID: string): Promise<MembersResponse> {
      const c = ensure(companyID);
      return {
        members: [...c.members],
        invites: c.invites.filter((i) => i.status === "pending"),
      };
    },

    async inviteMember(
      companyID: string,
      req: InviteMemberRequest,
    ): Promise<InviteMemberResponse> {
      const c = ensure(companyID);
      const subject = (req.subject ?? "").trim();
      if (!subject) {
        throw new ApiError(400, "subject is required", "subject is required");
      }
      if (c.members.some((m) => m.subject === subject)) {
        throw new ApiError(
          409,
          "already a member of this company",
          "already a member of this company",
        );
      }
      const level =
        c.levels.find((l) => l.id === req.security_level_id) ??
        c.levels.find((l) => l.name === req.security_level_name);
      if (!level) {
        throw new ApiError(
          404,
          "security level not found",
          "security level not found",
        );
      }
      // Re-inviting a pending subject updates that invite (the server upserts on
      // the partial unique index) rather than stacking a second one.
      const existing = c.invites.find(
        (i) => i.subject === subject && i.status === "pending",
      );
      const invite: InviteView = existing ?? {
        id: id("invite"),
        subject,
        email: req.email ?? "",
        display_name: req.display_name ?? "",
        security_level_id: level.id,
        security_level_name: level.name,
        status: "pending",
        created_at: "2026-07-26T00:00:00Z",
        // The mock has no dispatcher, so an invite it creates is enqueued and
        // never delivered — which is the honest in-memory analogue and keeps a
        // screen test from asserting a delivery nothing performed.
        notification_status: "pending",
        notification_attempts: 0,
      };
      invite.security_level_id = level.id;
      invite.security_level_name = level.name;
      if (!existing) c.invites.push(invite);
      return { invite_id: invite.id, subject, status: "pending" };
    },

    async revokeInvite(
      companyID: string,
      inviteID: string,
    ): Promise<RevokeInviteResponse> {
      const c = ensure(companyID);
      const iv = c.invites.find((i) => i.id === inviteID);
      if (!iv) {
        throw new ApiError(404, "invite not found", "invite not found");
      }
      // Revoked, never deleted: the audit trail of who was offered access
      // survives, and listMembers filters to `pending` so it leaves the screen.
      iv.status = "revoked";
      return { invite_id: inviteID, status: "revoked" };
    },

    async updateMember(
      companyID: string,
      userID: string,
      req: UpdateMemberRequest,
    ): Promise<UpdateMemberResponse> {
      const c = ensure(companyID);
      const m = c.members.find((x) => x.user_id === userID);
      if (!m) {
        throw new ApiError(404, "membership not found", "membership not found");
      }
      const level = resolveLevel(
        c,
        req.security_level_id,
        req.security_level_name,
      );
      // THE LAST-OWNER GUARD (authz.Store.SetMembershipLevel): demoting the only
      // Owner would leave the company unadministrable, so it is refused.
      if (m.is_owner && !level.is_owner && ownerCount(c) <= 1) {
        throw new ApiError(
          409,
          "the last Owner cannot be demoted",
          "the last Owner cannot be demoted",
        );
      }
      m.security_level_id = level.id;
      m.security_level_name = level.name;
      m.is_owner = level.is_owner;
      recount(c);
      return { user_id: userID, security_level_id: level.id };
    },

    async removeMember(
      companyID: string,
      userID: string,
    ): Promise<RemoveMemberResponse> {
      const c = ensure(companyID);
      const m = c.members.find((x) => x.user_id === userID);
      if (!m) {
        throw new ApiError(404, "membership not found", "membership not found");
      }
      if (m.is_owner && ownerCount(c) <= 1) {
        throw new ApiError(
          409,
          "the last Owner cannot be removed",
          "the last Owner cannot be removed",
        );
      }
      c.members = c.members.filter((x) => x.user_id !== userID);
      recount(c);
      return { user_id: userID, status: "removed" };
    },

    async listSecurityLevels(
      companyID: string,
    ): Promise<SecurityLevelsResponse> {
      return { security_levels: [...ensure(companyID).levels] };
    },

    async getSecurityVocabulary(
      companyID: string,
    ): Promise<SecurityVocabulary> {
      ensure(companyID);
      return {
        effects: [...MOCK_VOCABULARY.effects],
        actions: [...MOCK_VOCABULARY.actions],
        resources: [...MOCK_VOCABULARY.resources],
        default_accesses: [...MOCK_VOCABULARY.default_accesses],
      };
    },

    async createSecurityLevel(
      companyID: string,
      req: SecurityLevelRequest,
    ): Promise<CreateSecurityLevelResponse> {
      const c = ensure(companyID);
      const name = (req.name ?? "").trim();
      if (!name) {
        throw new ApiError(400, "name is required", "name is required");
      }
      if (c.levels.some((l) => l.name === name)) {
        throw new ApiError(
          409,
          `security level "${name}" already exists`,
          `security level "${name}" already exists`,
        );
      }
      // Default-DENY for a level created with no explicit default, mirroring
      // handleCreateSecurityLevel: a new level must not silently grant all.
      const defaultAccess = req.default_access || "none";
      validateDefaultAccess(defaultAccess);
      const rules = (req.rules ?? []).map(validateRule);
      const level: SecurityLevelView = {
        id: id("level"),
        name,
        is_owner: false,
        default_access: defaultAccess,
        requires_webauthn: req.requires_webauthn ?? false,
        backdated_cutoff: req.backdated_cutoff ?? "",
        member_count: 0,
        rules,
      };
      c.levels.push(level);
      return { security_level_id: level.id, name };
    },

    async updateSecurityLevel(
      companyID: string,
      levelID: string,
      req: SecurityLevelRequest,
    ): Promise<UpdateSecurityLevelResponse> {
      const c = ensure(companyID);
      const level = c.levels.find((l) => l.id === levelID);
      if (!level) {
        throw new ApiError(
          404,
          "security level not found",
          "security level not found",
        );
      }
      const touchesMoreThanCutoff =
        (req.name ?? "").trim() !== "" ||
        (req.default_access ?? "") !== "" ||
        req.requires_webauthn !== undefined ||
        req.rules !== undefined;
      // THE OWNER LEVEL IS IMMUTABLE except its back-dated cutoff (Tally's own
      // behaviour, and the cutoff binds Owners too). Rewriting its
      // requires_webauthn or default_access would silently rewrite the whole
      // company's security posture.
      if (level.is_owner && touchesMoreThanCutoff) {
        throw new ApiError(
          422,
          "the Owner level cannot be modified except its back-dated cutoff",
          "the Owner level cannot be modified except its back-dated cutoff",
        );
      }
      const newName = (req.name ?? "").trim();
      if (newName && newName !== level.name) {
        if (c.levels.some((l) => l.name === newName)) {
          throw new ApiError(
            409,
            `security level "${newName}" already exists`,
            `security level "${newName}" already exists`,
          );
        }
        level.name = newName;
      }
      if (req.default_access) {
        validateDefaultAccess(req.default_access);
        level.default_access = req.default_access;
      }
      if (req.requires_webauthn !== undefined) {
        level.requires_webauthn = req.requires_webauthn;
      }
      if (req.backdated_cutoff !== undefined) {
        level.backdated_cutoff = req.backdated_cutoff;
      }
      // Rules are replaced WHOLESALE when the key is present — a PUT of the
      // whole set, not a patch. `[]` therefore clears every rule.
      if (req.rules !== undefined) {
        level.rules = req.rules.map(validateRule);
      }
      // Members carry the level NAME, so a rename must follow through or the
      // member list would show a level that no longer exists.
      for (const m of c.members) {
        if (m.security_level_id === level.id)
          m.security_level_name = level.name;
      }
      for (const iv of c.invites) {
        if (iv.security_level_id === level.id)
          iv.security_level_name = level.name;
      }
      return { security_level_id: levelID };
    },

    async deleteSecurityLevel(
      companyID: string,
      levelID: string,
    ): Promise<DeleteSecurityLevelResponse> {
      const c = ensure(companyID);
      const level = c.levels.find((l) => l.id === levelID);
      if (!level) {
        throw new ApiError(
          404,
          "security level not found",
          "security level not found",
        );
      }
      if (level.is_owner) {
        throw new ApiError(
          422,
          "the Owner level cannot be deleted",
          "the Owner level cannot be deleted",
        );
      }
      // IN USE counts PENDING INVITES as well as members (authz.Store.DeleteLevel):
      // deleting a level somebody has been invited at would leave that invite
      // pointing at nothing.
      const inUse =
        c.members.filter((m) => m.security_level_id === levelID).length +
        c.invites.filter(
          (i) => i.security_level_id === levelID && i.status === "pending",
        ).length;
      if (inUse > 0) {
        throw new ApiError(
          409,
          `${inUse} member(s)/invite(s) still use it`,
          `${inUse} member(s)/invite(s) still use it`,
        );
      }
      c.levels = c.levels.filter((l) => l.id !== levelID);
      return { security_level_id: levelID, status: "deleted" };
    },

    async importTallyXML(
      companyID,
      _xml,
      query?: ImportTallyXMLQuery,
    ): Promise<ImportTallyXMLResponse> {
      void query;
      return {
        source_company: ensure(companyID).name,
        vouchers_posted: 0,
        ledgers_ensured: 0,
        posted: [],
      };
    },

    // =======================================================================
    // T3 — ORDERS, THE BILL OF MATERIALS, AND THE ORDER BOOKS
    // =======================================================================
    // ⛔⛔ THIS MOCK REFUSES WHAT THE SERVER REFUSES, AND ITS RESPONSE SHAPES ARE
    // THE SERVER'S. That is not tidiness — it is the one rule this file exists to
    // follow, and it was broken TWICE in the session that built T2: once for a
    // missing created_by (server 400, mock 201) and once for a response SHAPE (the
    // server nests a view under `cost_centre` and may send it null; the mock
    // returned it flat, rest.ts typed the envelope AS the flat view, and every
    // field read undefined at runtime while the code type-checked).
    //
    // 597 green frontend tests sat on top of a create path that could not work in a
    // browser. So each refusal below is DIFFED AGAINST THE HANDLER, not against the
    // type I would like to exist:
    //
    //	duplicate order number, case-insensitive, PER DIRECTION        -> 409
    //	closing with no reason                                         -> 422
    //	an id of the OTHER direction on this direction's route          -> 404
    //	a second DEFAULT recipe for one item                            -> 409
    //	a recipe naming its own finished good as a component            -> 422
    //	an all-zeros stock_item_id filter (uuid.Nil is "absent")        -> 400
    //	explode with no qty                                             -> 400
    createSalesOrder(companyID, req: CreateOrderRequest) {
      return mockCreateOrder(companyID, "SALES", req);
    },
    createPurchaseOrder(companyID, req: CreateOrderRequest) {
      return mockCreateOrder(companyID, "PURCHASE", req);
    },
    async listSalesOrders(companyID, openOnly?: boolean) {
      return mockListOrders(companyID, "SALES", openOnly);
    },
    async listPurchaseOrders(companyID, openOnly?: boolean) {
      return mockListOrders(companyID, "PURCHASE", openOnly);
    },
    async getSalesOrder(companyID, orderID: string) {
      return mockGetOrder(companyID, "SALES", orderID);
    },
    async getPurchaseOrder(companyID, orderID: string) {
      return mockGetOrder(companyID, "PURCHASE", orderID);
    },
    async closeSalesOrder(companyID, orderID: string, req: CloseOrderRequest) {
      return mockCloseOrder(companyID, "SALES", orderID, req);
    },
    async closePurchaseOrder(companyID, orderID: string, req: CloseOrderRequest) {
      return mockCloseOrder(companyID, "PURCHASE", orderID, req);
    },
    async createBom(companyID, req: CreateBomRequest) {
      const co = ensure(companyID);
      const name = (req.name ?? "").trim();
      if (!req.stock_item_id) {
        throw new ApiError(422, "orders: a bill of materials must name the finished good it produces");
      }
      if (name === "") {
        throw new ApiError(422, "orders: a bill of materials needs a name");
      }
      if (!req.output_qty || Number(req.output_qty) <= 0) {
        throw new ApiError(
          422,
          "orders: the yield must be more than zero — the explosion scales the components by (wanted / yield)",
        );
      }
      if (!req.components || req.components.length === 0) {
        throw new ApiError(
          422,
          "orders: a recipe with no components consumes nothing — exploding it would produce a finished good out of thin air",
        );
      }
      for (const c of req.components) {
        if (c.stock_item_id === req.stock_item_id) {
          throw new ApiError(
            422,
            "orders: the finished good cannot also be a component of its own recipe — exploding it would consume the very thing it produces",
          );
        }
      }
      const seen = new Set<string>();
      for (const c of req.components) {
        if (seen.has(c.stock_item_id)) {
          throw new ApiError(422, "orders: the same item appears twice in this recipe");
        }
        seen.add(c.stock_item_id);
      }
      if (
        mockBoms.some(
          (b) =>
            b.stock_item_id === req.stock_item_id &&
            b.name.trim().toLowerCase() === name.toLowerCase(),
        )
      ) {
        throw new ApiError(409, `orders: this item already has a recipe named "${name}"`);
      }
      if (req.is_default && mockBoms.some((b) => b.stock_item_id === req.stock_item_id && b.is_default)) {
        // ⛔ Two defaults is a silent coin flip over which recipe a manufacture used,
        // and the two consume different materials.
        throw new ApiError(
          409,
          "orders: this item already has a DEFAULT recipe — clear the other default first, or add this one as a non-default alternative",
        );
      }
      const bomID = id("bom");
      mockBoms.push({
        bom_id: bomID,
        stock_item_id: req.stock_item_id,
        // THE RESOLVED NAME, not the id — see the note on the order's `party` field.
        // internal/orders/bom.go:242 LEFT JOINs stock_item and returns
        // COALESCE(si.name, ''), so "" is the honest answer for an id that resolves
        // to nothing and the id itself is never a fallback.
        stock_item:
          co.stockItems.find((s) => s.id === req.stock_item_id)?.name ?? "",
        name,
        output_qty: fixed4(req.output_qty),
        is_default: req.is_default ?? false,
        components: req.components.map((c, i) => ({
          line_no: i + 1,
          stock_item_id: c.stock_item_id,
          stock_item:
            co.stockItems.find((s) => s.id === c.stock_item_id)?.name ?? "",
          qty: fixed4(c.qty),
        })),
      });
      return { bom_id: bomID };
    },
    async listBoms(companyID, stockItemID?: string) {
      ensure(companyID);
      // ⛔ THE ALL-ZEROS UUID IS REFUSED, not treated as "no filter". uuid.Nil is the
      // server's absent-sentinel, so an explicit zero would silently return EVERY
      // recipe with a 200 and a truthful-looking body — the defect that shipped in
      // T2's cost-centre filter.
      if (stockItemID === "00000000-0000-0000-0000-000000000000") {
        throw new ApiError(
          400,
          "stock_item_id must be the uuid of a stock item; omit it entirely to list every recipe",
        );
      }
      const rows = stockItemID
        ? mockBoms.filter((b) => b.stock_item_id === stockItemID)
        : mockBoms;
      return {
        boms: rows.map(({ components: _components, ...head }) => head),
      };
    },
    async getBom(companyID, bomID: string) {
      ensure(companyID);
      const b = mockBoms.find((x) => x.bom_id === bomID);
      if (!b) {
        throw new ApiError(404, "orders: no such bill of materials in this company");
      }
      return b;
    },
    async explodeBom(companyID, bomID: string, qty: string) {
      ensure(companyID);
      if (!qty || qty.trim() === "") {
        throw new ApiError(
          400,
          "qty is required — there is no sensible default: guessing 1 would quietly answer a question about a different batch size",
        );
      }
      const wanted = Number(qty);
      if (!Number.isFinite(wanted) || wanted <= 0) {
        throw new ApiError(422, "orders: the quantity to make must be more than zero");
      }
      const b = mockBoms.find((x) => x.bom_id === bomID);
      if (!b) {
        throw new ApiError(404, "orders: no such bill of materials in this company");
      }
      const output = Number(b.output_qty);
      return {
        bom_id: b.bom_id,
        stock_item: b.stock_item,
        wanted_qty: fixed4(qty),
        output_qty: b.output_qty,
        // ⛔ MULTIPLY THEN DIVIDE, matching orders.ScaleComponent exactly. Computing
        // the ratio first shorts every non-whole-multiple batch, always downward. The
        // mock reproducing the server's arithmetic is the point: a screen developed
        // against a mock that rounded differently would show one number here and
        // another in production.
        components: b.components.map((c) => ({
          stock_item_id: c.stock_item_id,
          stock_item: c.stock_item,
          qty: fixed4(String((Number(c.qty) * wanted) / output)),
        })),
        notes: [
          `Scaled from the recipe as written: ${b.output_qty} of ${b.stock_item} yields ${b.output_qty}.`,
          "SINGLE-LEVEL explosion: a component that has its own bill of materials is NOT exploded further.",
          "Nothing has moved. This is what a Stock Journal's lines WOULD be.",
        ],
      };
    },
    // -----------------------------------------------------------------------
    // T5 — THE e-WAY BILL.
    //
    // ⛔ THIS MOCK MAKES THE SERVER'S REFUSALS, AND THAT IS THE WHOLE JOB.
    // HANDOFF §9.4 names three kinds of mock-fidelity defect and this feature
    // could carry all three:
    //   1. a refusal the mock does not make — a screen keys a consignment with
    //      no distance, gets a 201 against the mock, and 422s in a browser;
    //   2. a shape the mock gets wrong — `disclosures` absent or empty, so the
    //      screen renders nothing and looks correct until production;
    //   3. a mock MORE PERMISSIVE than the server — the ensureLedger class,
    //      invisible by construction.
    //
    // So every refusal below is the server's, in the server's own words, with
    // the server's status code.
    //
    // ⚠️ THE RULE 138 FIGURES ARE NOT RESTATED HERE EITHER. The mock cannot
    // reach rule138.go, so it applies the same ARITHMETIC over constants that
    // are declared, once, right beside the code that uses them — and it says
    // out loud that they are a mirror. If the server's figures are corrected
    // and these are not, the divergence shows up as a mock and a server
    // disagreeing about a validity, which is exactly the kind of thing
    // orders.test.ts exists to catch.
    async raiseEWayBill(companyID, req: EWayBillRequest) {
      const co = ensure(companyID);
      const voucherID = (req.voucher_id ?? "").trim();
      const view = voucherID ? co.views.get(voucherID) : undefined;
      if (!view) {
        throw new ApiError(
          404,
          "ewaybill: no such voucher or e-way bill in this company",
        );
      }

      // ---- REFUSAL 1: no goods. An e-way bill covers a MOVEMENT OF GOODS.
      const lines = view.inventory ?? [];
      if (lines.length === 0) {
        throw new ApiError(
          422,
          "ewaybill: this voucher moves no goods, so no e-way bill can be raised for it — " +
            "an e-way bill covers a movement of goods, and a voucher with no stock lines is not one",
        );
      }

      const distance = req.distance_km ?? 0;
      const odc = req.over_dimensional_cargo === true;
      const fromState = (req.from_state_code ?? co.stateCode ?? "").trim();
      const toState = (req.to_state_code ?? "").trim() || fromState;

      // The three Rule 138 figures, MIRRORED from rule138.go. See the note above.
      const THRESHOLD = 50000;
      const PART_B_EXEMPT_KM = 50;
      const KM_PER_DAY = odc ? 20 : 200;

      const total =
        Number(view.taxable_value ?? 0) +
        Number(view.cgst ?? 0) +
        Number(view.sgst ?? 0) +
        Number(view.igst ?? 0);
      const required = total >= THRESHOLD;

      const intraState = fromState !== "" && fromState === toState;
      const shortHop = distance > 0 && distance <= PART_B_EXEMPT_KM;
      const partBRequired = !(intraState && shortHop);

      const blockers: string[] = [];
      if (partBRequired && (req.vehicle_number ?? "").trim() === "") {
        blockers.push(
          "Part B is required for this movement and no vehicle number was supplied. " +
            "The goods may not move on this bill until one is added.",
        );
      }

      // ---- REFUSAL 2: no distance. ⛔ AND ONLY THIS ONE REFUSES.
      //
      // The Part B blocker above must NOT refuse: raising Part A while the
      // vehicle is still being arranged is ordinary. Getting this wrong in the
      // mock would teach a screen author the opposite of the server's rule,
      // which is worse than no mock at all.
      if (distance <= 0) {
        throw new ApiError(
          422,
          "ewaybill: the e-way bill cannot be raised as stated: No distance was stated, so " +
            "the validity period cannot be computed. It is NOT assumed to be one day: a bill " +
            "whose expiry the system guessed is a bill that expires without warning, and an " +
            "expired e-way bill on a live consignment is a detained vehicle.",
        );
      }

      const validityDays = Math.max(1, Math.ceil(distance / KM_PER_DAY));
      const generatedAt = new Date();
      const validUntil = new Date(generatedAt);
      validUntil.setDate(validUntil.getDate() + validityDays);

      const provider = "fake-nic-ewaybill";
      const disclosures = [
        "⛔ THIS IS NOT A GOVERNMENT-ISSUED e-WAY BILL. No submission was made to the NIC " +
          "e-Way Bill portal, which is unreachable from this deployment. The identifier on " +
          `this document is a LOCAL REFERENCE generated by "${provider}" and is NOT an EBN. ` +
          "Presenting it as one would tell a checkpost that this consignment is covered when it is not.",
        "⚠️ THE e-WAY BILL RULES APPLIED HERE ARE SECONDARY MATERIAL. Not verified against the " +
          "published rule: CBIC's rule repository failed TLS chain verification from this " +
          "environment on 2026-08-05.",
        ...blockers.map((b) => "BLOCKER: " + b),
        required
          ? `Consignment value ${total.toFixed(2)} is at or above the Rs ${THRESHOLD} threshold, so an e-way bill is required.`
          : `Consignment value ${total.toFixed(2)} is below the Rs ${THRESHOLD} threshold, so no e-way bill is required on value alone.`,
        `Validity ${validityDays} day(s): ${distance} km at one day per ${KM_PER_DAY} km, rounded UP.`,
      ];

      const existing = co.eWayBills.get(voucherID);
      const built: EWayBillResponse = {
        voucher_id: voucherID,
        voucher_no: view.voucher_no,
        voucher_date: view.voucher_date,
        // ⛔ "LOCAL-EWB-" AND NOT 12 DIGITS, matching FakeEWayBillProvider. A mock
        // that minted an EBN-shaped value would let a screen ship a label the
        // server's value could never justify.
        local_reference: `LOCAL-EWB-${view.voucher_no || "NODOC"}-${(view.place_of_supply || "NOGST").slice(0, 6)}`,
        provider,
        payload: {
          version: "1.0.0421",
          supplyType: "O",
          subSupplyType: "1",
          docType: "INV",
          docNo: view.voucher_no,
          docDate: view.voucher_date,
          transDistance: String(distance),
          totInvValue: total.toFixed(2),
          itemList: lines.map((l) => ({
            productName: l.item,
            hsnCode: "",
            quantity: l.qty,
            qtyUnit: "",
            taxableAmount: Number(l.amount ?? 0).toFixed(2),
          })),
        },
        valid_until: validUntil.toISOString().slice(0, 10),
        validity_days: validityDays,
        disclosures,
        assessed_required: required,
        part_b_required: partBRequired,
        blockers,
        transporter_id: (req.transporter_id ?? "").trim(),
        transporter_name: (req.transporter_name ?? "").trim(),
        vehicle_number: (req.vehicle_number ?? "").trim(),
        transport_mode: (req.transport_mode ?? "").trim(),
        distance_km: distance,
        over_dimensional_cargo: odc,
        from_pincode: (req.from_pincode ?? "").trim(),
        to_pincode: (req.to_pincode ?? "").trim(),
        from_state_code: fromState,
        to_state_code: toState,
        // Counted, never silent — migration 0047's rule.
        generation_count: (existing?.view.generation_count ?? 0) + 1,
        generated_at: generatedAt.toISOString(),
        stale: false,
      };
      co.eWayBills.set(voucherID, {
        view: built,
        generatedFromVersion: view.version_no,
      });
      return built;
    },
    async getEWayBill(companyID, voucherID: string) {
      const co = ensure(companyID);
      const held = co.eWayBills.get((voucherID ?? "").trim());
      if (!held) {
        throw new ApiError(
          404,
          "ewaybill: no such voucher or e-way bill in this company",
        );
      }
      // ⛔ `stale` IS DERIVED ON READ, exactly as the server derives it from
      // voucher_head.current_version_id. A mock that returned the stored `false`
      // would let a screen ship with no staleness handling and look correct.
      const current = co.views.get(voucherID)?.version_no;
      return {
        ...held.view,
        stale:
          current !== undefined && current !== held.generatedFromVersion,
      };
    },
    /**
     * RAISE THE e-INVOICE. §7.4: THE MOCK MUST REFUSE WHAT THE SERVER REFUSES,
     * and here that is not a nicety — every refusal below guards a document that
     * carries a government reference number and then FREEZES the voucher against
     * it, with no undo anywhere in the product. A lenient mock would teach a
     * screen author that these cases do not happen.
     *
     * The refusals mirror internal/einvoice's sentinels, in the server's order.
     */
    async raiseEInvoice(companyID, req: EInvoiceRaiseRequest) {
      const co = ensure(companyID);
      const voucherID = (req.voucher_id ?? "").trim();
      const view = voucherID ? co.views.get(voucherID) : undefined;
      if (!view) {
        throw new ApiError(
          404,
          "einvoice: no such voucher, or no e-invoice has been raised for it in this company",
        );
      }

      // ---- IDEMPOTENCY, AND IT IS CHECKED BEFORE EVERY REFUSAL BELOW.
      //
      // ⛔ THE SAME IRN COMES BACK, NOT A NEW ONE. This is the single property
      // whose failure mints a DUPLICATE against a real government API, and it is
      // the entire reason statutoryd's workflow is durable and its `generate`
      // step memoized. It is checked first because a settled workflow is never
      // re-validated by the server either: once an IRN exists the voucher is
      // frozen, so re-deriving the refusals could only ever answer a question
      // about a document that can no longer change.
      const settled = co.eInvoices.get(voucherID);
      if (settled) return settled;

      // ---- REFUSAL 1: THE WRONG DOCUMENT. This is the one that shipped as a
      // critical defect and was caught only by RUNNING it: a Purchase was
      // e-invoiced, 201, seller = the company's own GSTIN and buyer = its
      // SUPPLIER, and the freeze then made a legitimate purchase record
      // un-alterable and un-deletable forever.
      //
      // ⚠️ Credit and Debit notes are LEGITIMATELY e-invoiceable in India as CRN
      // and DBN. Refusing them is a real gap, not a rule — this build models
      // neither the document type nor the reference to the original invoice that
      // NIC requires alongside it, and a credit note submitted as "INV" is a
      // false document. So they are refused BY NAME with the gap stated.
      if (view.class !== "Sales") {
        throw new ApiError(
          422,
          "einvoice: an e-invoice is the OUTWARD document a seller issues, so it can only be " +
            "raised against a SALES invoice. Supported: Sales (NIC document type INV). NOT " +
            "supported: Purchase, Credit Note and Debit Note — a purchase e-invoiced from these " +
            "books would declare that this company SOLD the goods to its own supplier, and a " +
            "credit or debit note is a different NIC document type (CRN/DBN, with a reference to " +
            "the original invoice) that this version does not build. Nothing else — receipt, " +
            "payment, contra, journal, delivery note, payroll — is an invoice at all",
        );
      }

      // ---- REFUSAL 2: no tax. An e-invoice reports a taxable supply.
      const tax =
        Number(view.cgst ?? 0) + Number(view.sgst ?? 0) + Number(view.igst ?? 0);
      if (Number(view.taxable_value ?? 0) <= 0 && tax <= 0) {
        throw new ApiError(
          422,
          "einvoice: this invoice carries no GST lines, and an e-invoice reports a taxable " +
            "outward supply — there is nothing to report",
        );
      }

      // ---- REFUSAL 3: NIC's ItemList requires at least one item.
      //
      // ⛔ THIS IS THE REFUSAL THAT SETTLED THE ARCHITECTURE. statutoryd's own
      // GetVoucher maps five amount fields and NO LINES, so an e-invoice raised
      // without a cored-side line-item builder fails "[ItemList: 0 items,
      // minimum 1]" every time. Only cored holds the inventory rows, the stock
      // masters, the HSN and the rate — which is why cored proxies rather than
      // the browser calling statutoryd directly.
      const lines = view.inventory ?? [];
      if (lines.length === 0) {
        throw new ApiError(
          422,
          "einvoice: this invoice has no item lines, and NIC's schema requires at least one",
        );
      }

      // ⛔ A HASH OF THE PAYLOAD, MATCHING FakeProvider — AND THAT IS ITSELF A
      // DISCLOSURE. A real IRP derives IRN = hash(SupplierGSTIN, DocType, DocNo,
      // FY) and REJECTS the second document that hashes the same. FakeProvider
      // hashes the WHOLE PAYLOAD, so Sales #1, Purchase #1 and Credit Note #1
      // would all mint DISTINCT IRNs where NIC would reject two of them. No test
      // written against this fake can fail that way, which is exactly why the
      // class refusal above is a hard gate rather than something left to the
      // provider to notice.
      let h = 0;
      const seed = `${companyID}:${voucherID}:${view.voucher_no}:${view.version_no}`;
      for (let i = 0; i < seed.length; i++) {
        h = (h * 31 + seed.charCodeAt(i)) >>> 0;
      }
      const hex = h.toString(16).padStart(8, "0");

      const raised: EInvoiceRaised = {
        voucher_id: voucherID,
        voucher_no: view.voucher_no,
        irn: `${hex}${hex}${hex}${hex}`,
        ack_no: String(112000000000000 + (h % 1000000)),
        status: "ACT",
        // The freeze hook pins the number the IRN refers to. From here the
        // voucher cannot be altered or deleted — see alterVoucher/deleteVoucher.
        pinned_voucher_no: view.voucher_no,
      };
      co.eInvoices.set(voucherID, raised);
      return raised;
    },
    async getEInvoice(companyID, voucherID: string) {
      const co = ensure(companyID);
      const held = co.eInvoices.get((voucherID ?? "").trim());
      if (!held) {
        // ⚠️ THE NORMAL ANSWER FOR ALMOST EVERY VOUCHER. A screen must not treat
        // it as an error.
        throw new ApiError(
          404,
          "einvoice: no such voucher, or no e-invoice has been raised for it in this company",
        );
      }
      // ⛔ NO `ack_no` KEY, matching the server and the schema: there is no
      // column for it in voucher_external_reference. Spreading `held` here would
      // hand a screen an ack number the real GET never carries, and the screen
      // would render it in a panel that is blank against a real server.
      return {
        voucher_id: held.voucher_id,
        voucher_no: held.voucher_no,
        irn: held.irn,
        pinned_voucher_no: held.pinned_voucher_no,
        created_by: MOCK_SUBJECT,
        created_at: new Date().toISOString(),
      };
    },
    async salesOrderBook(companyID, asOn?: string) {
      return mockOrderBook(companyID, "SALES", asOn);
    },
    async purchaseOrderBook(companyID, asOn?: string) {
      return mockOrderBook(companyID, "PURCHASE", asOn);
    },
  };
}
