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
import {
  PRINT_TEMPLATE_KINDS,
  printTemplateSurfaceFor,
} from "../screens/printTemplateSurface";
import type {
  BooksClosure,
  BalanceSheetResponse,
  CompanyFeaturesRequest,
  CompanyFeaturesResponse,
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
  SetValuationMethodRequest,
  SetValuationMethodResponse,
  OutstandingKind,
  OutstandingParty,
  OutstandingsQuery,
  OutstandingsResponse,
  UnadjustedForexResponse,
  UnadjustedForexBill,
  PartyOpenBillsResponse,
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
  CreateStockGroupRequest,
  CreateCurrencyRequest,
  CreateCurrencyResponse,
  CurrencyListItem,
  ListCurrenciesResponse,
  ForexSettingsResponse,
  SetForexSettingsRequest,
  SetCurrencyRateRequest,
  CreateStockGroupResponse,
  InventoryVocabularyResponse,
  ListStockGroupsResponse,
  ListUnitsResponse,
  StockGroupListItem,
  StockGroupSubtotal,
  UnitListItem,
  ListStockItemsResponse,
  StockBatchListItem,
  StockItemListItem,
  StockPositionRow,
  StockSummaryItem,
  StockSummaryResponse,
  ListGroupsResponse,
  ListLedgersResponse,
  SetLedgerOpeningRequest,
  SetLedgerOpeningResponse,
  VoucherHistoryResponse,
  BankMatchCandidate,
  BankMatchCounts,
  BankMatchesResponse,
  BankMatchProposal,
  BankMatchResolution,
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
  AnomaliesResponse,
  CashFlowResponse,
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
  EmployeeView,
  ListEmployeesResponse,
  CreateEmployeeRequest,
  CreateEmployeeResponse,
  PayRunRequest,
  PayRunResponse,
  PayrollRegisterResponse,
  PayrollRegisterRow,
  PayslipResponse,
  PayslipDeferral,
  Form138Request,
  StatutoryFileDownload,
  SalaryComponentView,
  ListSalaryComponentsResponse,
  SetSalaryComponentRequest,
  SetSalaryComponentResponse,
  StatutoryElectionView,
  ListStatutoryElectionsResponse,
  SetStatutoryElectionRequest,
  SetStatutoryElectionResponse,
  PayrollVocabularyResponse,
  TDSChallanListResponse,
  AllocateToChallanRequest,
  AllocateToChallanResponse,
  TaxSectionDoc,
  TaxSectionsResponse,
  CreateTDSChallanRequest,
  CreateTDSChallanResponse,
  TDSProfile,
  TDSProfileResponse,
  SetTDSProfileRequest,
  SetTDSProfileResponse,
  BankReconLine,
  SetAsideRequest,
  SetAsideResponse,
  SetBankDateRequest,
  SetBankDateResponse,
  VoucherVersionView,
  GSTLineView,
  TDSRequest,
  TCSRequest,
  TaxAllocationKind,
  TDSDeductionView,
  TCSCollectionView,
  PrintTemplateField,
  PrintTemplateOverrides,
  PrintTemplateRequest,
  PrintTemplateResponse,
  AlterBudgetResponse,
  Budget,
  BudgetBasis,
  BudgetDimension,
  BudgetLine,
  BudgetLineListResponse,
  BudgetListResponse,
  BudgetStatus,
  BudgetVarianceResponse,
  GSTR9Envelope,
  GSTR9Month,
  GSTR9Divergence,
  GSTR9Table,
  GSTR9TaxLedgers,
  GSTTax,
  BudgetVarianceRow,
  CreateBudgetLineRequest,
  CreateBudgetLineResponse,
  CreateBudgetRequest,
  CreateBudgetResponse,
  DeleteBudgetResponse,
} from "./types";
// A VALUE, not a type: the recorded `payload.class` labels the server stamps.
// The mock must agree with them exactly — see classLabel().
import { VOUCHER_CLASS_LABELS } from "./types";
// ⛔ THE RATE HELPERS, NEVER THE MONEY ONES. `api/rate.ts`'s header states the
// reason at length: money is fixed-4 in this product and `parseFixed4` TRUNCATES
// the fifth decimal onward, so a rate put through it silently becomes a different
// rate and every figure derived from it is plausible. The mock has to be exactly as
// careful as the screen, or the double is the one place the bug is invisible.
import { RATE_DECIMALS, parseRate, formatRate } from "./rate";

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
import {
  AGENT_UNCONFIGURED,
  runFinished,
  type AgentApproval,
  type AgentAutonomyRow,
  type AgentDecisionRequest,
  type AgentEvent,
  type AgentRun,
  type SetAgentAutonomyRequest,
  type StartAgentRunRequest,
} from "./agent";
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

/** A seeded SIMPLE unit — every company gets "Nos", as migration 0005 does. */
function seedUnit(companyID: string, symbol: string, formalName: string): UnitListItem {
  return {
    id: `${companyID}:unit-${symbol.toLowerCase()}`,
    symbol,
    formal_name: formalName,
    decimal_places: 0,
    is_system: true,
    // Simple: no base, and "" rather than "0.0000" — a unit with no base
    // restates into nothing, and a zero factor would read as "one of these holds
    // none of those".
    base_unit_symbol: "",
    conversion_factor: "",
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
/**
 * The ONE bucket a party's unreferenced balance is reported under (D106).
 *
 * ⛔ IT MUST MATCH cored's `reporting.OnAccountRefName` AND `ledger.OnAccountRefName`
 * EXACTLY. The report groups by (ledger, ref_name), so a mock that spelled it
 * differently would put an explicit ON_ACCOUNT reference and the derived residue in
 * two rows here and one row against the real server — and the screen tests would be
 * proving a layout the product never renders. That is D84's failure (this double
 * being looser than cored) wearing a different hat.
 */
// Derived from `api/rate.ts`'s RATE_DECIMALS rather than written as a literal, so
// the mock cannot end up on a different scale from the parser it uses.
const MOCK_RATE_SCALE = 10n ** BigInt(RATE_DECIMALS);

const ON_ACCOUNT_REF = "On Account";

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
  /**
   * The ITEM's costing method (T9), carried on the position because that is
   * what the replay needs at each movement.
   */
  method: string;
  /** The ITEM's stock group (T10), for the group subtotals. */
  group: string;
  /**
   * THE COSTED LAYERS, oldest first — empty for a weighted-average position,
   * which has one blended rate and needs no layers.
   *
   * ⛔ THE MOCK REPLAYS THE METHOD, IT DOES NOT LABEL ONE. Hardcoding
   * `valuation_method: "WEIGHTED_AVG"` on the row (which is what this did) while
   * folding a weighted average was harmless while that was the only method; the
   * moment an item can be FIFO, a mock that folds an average and prints "FIFO"
   * beside it makes every screen test PASS against figures the browser will not
   * reproduce — the exact class of divergence register row D84 exists to stop,
   * and the worst kind, because both numbers look right.
   */
  lots: { qty: bigint; value: bigint }[];
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
        method:
          c.stockItems.find((s) => s.id === l.stock_item_id)
            ?.valuation_method ?? "WEIGHTED_AVG",
        group:
          c.stockItems.find((s) => s.id === l.stock_item_id)?.stock_group ?? "",
        lots: [],
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

    const layered = p.method === "FIFO" || p.method === "LIFO";

    if (l.direction === "IN") {
      p.inwardQty += qty;
      p.inwardValue += value;
      p.qty += qty;
      p.value += value;
      if (layered) {
        // An empty queue is a fresh position, one issued flat, or one that has
        // gone NEGATIVE and lost its layers; all three become ONE blended layer
        // of whatever is now held, and a still-negative position holds none.
        if (p.lots.length === 0) {
          if (p.qty > 0n) p.lots = [{ qty: p.qty, value: p.value }];
        } else {
          p.lots.push({ qty, value });
        }
      }
      continue;
    }

    const nextQty = p.qty - qty;
    let consumed: bigint;
    if (layered && nextQty > 0n) {
      // Walk the queue from the end the method names. FIFO eats the oldest
      // layer, LIFO the newest; the SURVIVORS stay in arrival order either way,
      // or the next issue would eat them in the wrong sequence.
      let remaining = qty;
      consumed = 0n;
      const kept: { qty: bigint; value: bigint }[] = [];
      const queue =
        p.lots.length > 0 ? p.lots : [{ qty: p.qty, value: p.value }];
      for (let i = 0; i < queue.length; i++) {
        const lot = queue[p.method === "LIFO" ? queue.length - 1 - i : i]!;
        if (remaining === 0n) {
          kept.push(lot);
        } else if (lot.qty <= remaining) {
          consumed += lot.value;
          remaining -= lot.qty;
        } else {
          // A partial issue out of one layer, at the LAYER's own rate carried
          // at RATE_SCALE — rounding the rate to 4 dp first loses value on
          // every issue and leaves a layer holding nothing but a paisa.
          const rate = q4div(lot.value * RATE_SCALE, lot.qty);
          const take = q4div(remaining * rate, RATE_SCALE);
          consumed += take;
          kept.push({ qty: lot.qty - remaining, value: lot.value - take });
          remaining = 0n;
        }
      }
      if (p.method === "LIFO") kept.reverse();
      p.lots = kept;
      p.qty = nextQty;
      p.value -= consumed;
    } else if (nextQty === 0n || (layered && nextQty < 0n)) {
      // The position is emptied (or driven past empty): take everything the
      // layers held and NOT ONE PAISA MORE — stock never received has no cost
      // to consume, and valuing the shortfall at the SALE rate is the classic
      // inventory bug. Weighted average keeps its own long-standing behaviour
      // for a negative overshoot (it consumes qty * the running rate), which is
      // why this arm is `layered &&` for the negative case only.
      consumed = p.value > 0n ? p.value : 0n;
      p.qty = nextQty;
      p.value -= consumed;
      p.lots = [];
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
    valuation_method: p.method,
    stock_group: p.group,
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
        // The basis, from the position that arrived first: every position of one
        // item shares the item's method, exactly as the server folds it.
        valuation_method: row.valuation_method,
        stock_group: row.stock_group,
        // Filled after the fold: the restatement is a function of the item's
        // FINAL closing quantity, and doing it per position would restate each
        // one separately and then add the roundings up.
        base_unit: "",
        base_qty: "",
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

  // ---- T10a: THE COMPOUND-UNIT RESTATEMENT, once per item after the fold.
  for (const it of items) {
    const unit = c.units.find(
      (u) => u.symbol.toLowerCase() === it.unit.toLowerCase(),
    );
    if (!unit || unit.base_unit_symbol === "" || unit.conversion_factor === "") {
      continue;
    }
    it.base_unit = unit.base_unit_symbol;
    // Fixed-4 throughout, like every other quantity here. ⛔ closing_qty is NOT
    // touched: the stored quantity is the one that was keyed.
    it.base_qty = q4str(q4div(q4(it.closing_qty) * q4(unit.conversion_factor), QSCALE));
  }

  // ---- T10b: THE GROUP SUBTOTALS — the whole BRANCH, so an item counts in
  // every ANCESTOR of its group. The mock walks the parent chain rather than an
  // ltree path (it has no ltree), which is a second implementation of the same
  // rule and therefore able to disagree with the server — which is the point of
  // having it: a screen test that renders these is rendering something derived.
  const ancestors = (group: string): string[] => {
    const chain: string[] = [];
    let cursor = group;
    // A cycle is impossible through the API (the server refuses one) but a
    // hand-built fixture could hold one; the bound stops an infinite loop from
    // hanging a test suite instead of failing it.
    for (let hops = 0; cursor !== "" && hops < 32; hops++) {
      chain.push(cursor);
      cursor = c.stockGroups.find((g) => g.name === cursor)?.parent_name ?? "";
    }
    return chain;
  };
  const subtotals = new Map<string, StockGroupSubtotal>();
  const ungrouped: StockGroupSubtotal = {
    group: "",
    depth: 0,
    item_count: 0,
    opening_value: "0.0000",
    inward_value: "0.0000",
    outward_value: "0.0000",
    closing_value: "0.0000",
  };
  const fold = (into: StockGroupSubtotal, it: StockSummaryItem) => {
    into.item_count += 1;
    into.opening_value = addMoney(into.opening_value, it.opening_value);
    into.inward_value = addMoney(into.inward_value, it.inward_value);
    into.outward_value = addMoney(into.outward_value, it.outward_value);
    into.closing_value = addMoney(into.closing_value, it.closing_value);
  };
  for (const it of items) {
    if (it.stock_group === "") {
      fold(ungrouped, it);
      continue;
    }
    for (const name of ancestors(it.stock_group)) {
      let sub = subtotals.get(name);
      if (!sub) {
        sub = {
          group: name,
          depth: c.stockGroups.find((g) => g.name === name)?.depth ?? 0,
          item_count: 0,
          opening_value: "0.0000",
          inward_value: "0.0000",
          outward_value: "0.0000",
          closing_value: "0.0000",
        };
        subtotals.set(name, sub);
      }
      fold(sub, it);
    }
  }
  // Tree order: by depth, then by name — the server sorts by ltree path, which
  // is the same order for a tree whose siblings are named uniquely.
  const groups = [...subtotals.values()].sort(
    (a, b) => a.depth - b.depth || a.group.localeCompare(b.group),
  );
  if (ungrouped.item_count > 0) groups.push(ungrouped);

  let total = "0.0000";
  for (const row of rows) total = addMoney(total, row.closing_value);
  return { as_on: asOn ?? "", groups, items, rows, total_value: total };
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
  {
    code: "A",
    description: "Central Government",
    state_name_code_required: false,
    ministry_required: true,
    ministry_allowed: true,
  },
  {
    code: "S",
    description: "State Government",
    state_name_code_required: true,
    ministry_required: false,
    ministry_allowed: false,
  },
  {
    code: "D",
    description: "Statutory body (Central Govt.)",
    state_name_code_required: false,
    ministry_required: true,
    ministry_allowed: true,
  },
  {
    code: "E",
    description: "Statutory body (State Govt.)",
    state_name_code_required: true,
    ministry_required: false,
    ministry_allowed: true,
  },
  {
    code: "G",
    description: "Autonomous body (Central Govt.)",
    state_name_code_required: false,
    ministry_required: true,
    ministry_allowed: true,
  },
  {
    code: "H",
    description: "Autonomous body (State Govt.)",
    state_name_code_required: true,
    ministry_required: false,
    ministry_allowed: true,
  },
  {
    code: "L",
    description: "Local Authority (Central Govt.)",
    state_name_code_required: false,
    ministry_required: false,
    ministry_allowed: true,
  },
  {
    code: "N",
    description: "Local Authority (State Govt.)",
    state_name_code_required: true,
    ministry_required: false,
    ministry_allowed: true,
  },
  {
    code: "K",
    description: "Company",
    state_name_code_required: false,
    ministry_required: false,
    ministry_allowed: false,
  },
  {
    code: "M",
    description: "Branch / Division of Company",
    state_name_code_required: false,
    ministry_required: false,
    ministry_allowed: false,
  },
  {
    code: "P",
    description: "Association of Person (AOP)",
    state_name_code_required: false,
    ministry_required: false,
    ministry_allowed: false,
  },
  {
    code: "T",
    description: "Association of Person (Trust)",
    state_name_code_required: false,
    ministry_required: false,
    ministry_allowed: false,
  },
  {
    code: "J",
    description: "Artificial Juridical Person",
    state_name_code_required: false,
    ministry_required: false,
    ministry_allowed: false,
  },
  {
    code: "B",
    description: "Body of Individuals",
    state_name_code_required: false,
    ministry_required: false,
    ministry_allowed: false,
  },
  {
    code: "Q",
    description: "Individual/HUF",
    state_name_code_required: false,
    ministry_required: false,
    ministry_allowed: false,
  },
  {
    code: "F",
    description: "Firm",
    state_name_code_required: false,
    ministry_required: false,
    ministry_allowed: false,
  },
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
  /**
   * DEDUCTION ("TDS") or COLLECTION ("TCS"). Absent means TDS, mirroring the
   * wire.
   *
   * ⛔⛔ ITS ABSENCE HERE IS WHY 40 GREEN SCREEN TESTS MISSED REGISTER ROW D4.
   * Every fixture was a deduction, so no test ever sent an allocation whose kind
   * mattered — and the screen's two wire mappings both DROPPED the field. On the
   * real server that means "look this triple up in tds_deduction", so a live
   * collection came back as "nothing was deducted on voucher <uuid> under
   * 206C(1)-SCRAP at leg 0", which reads like the books are wrong rather than
   * like the request is. Found in a browser on 2026-08-21; the collection
   * fixture below is what would have caught it here.
   */
  kind?: TaxAllocationKind;
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
/**
 * The shipped `tds-2026-04` / `tcs-2026-04` section tables, copied from
 * internal/compliance. THE ONLY COPY OF THEM OUTSIDE GO, and pinned against the
 * server's own catalog by a test — see the note on `taxSections`.
 */
export const MOCK_TDS_SECTIONS: TaxSectionDoc[] = [
  {
    section: "194C",
    rate_ind_huf_pct: "1",
    rate_other_pct: "2",
    threshold_rupees: "30000.0000",
    basis: "PER_TXN",
  },
  {
    section: "194H",
    rate_ind_huf_pct: "2",
    rate_other_pct: "2",
    threshold_rupees: "20000.0000",
    basis: "ANNUAL",
  },
  {
    section: "194I-A",
    rate_ind_huf_pct: "2",
    rate_other_pct: "2",
    threshold_rupees: "50000.0000",
    basis: "MONTHLY",
  },
  {
    section: "194I-B",
    rate_ind_huf_pct: "10",
    rate_other_pct: "10",
    threshold_rupees: "50000.0000",
    basis: "MONTHLY",
  },
  {
    section: "194J-D",
    rate_ind_huf_pct: "10",
    rate_other_pct: "10",
    threshold_rupees: "0.0000",
    basis: "NIL",
  },
  {
    section: "194J-P",
    rate_ind_huf_pct: "10",
    rate_other_pct: "10",
    threshold_rupees: "50000.0000",
    basis: "ANNUAL",
  },
  {
    section: "194J-T",
    rate_ind_huf_pct: "2",
    rate_other_pct: "2",
    threshold_rupees: "50000.0000",
    basis: "ANNUAL",
  },
];

export const MOCK_TCS_SECTIONS: TaxSectionDoc[] = [
  {
    section: "206C(1)-SCRAP",
    rate_ind_huf_pct: "2",
    rate_other_pct: "2",
    threshold_rupees: "0.0000",
    basis: "PER_TXN",
  },
  {
    section: "206C(1F)-MV",
    rate_ind_huf_pct: "1",
    rate_other_pct: "1",
    threshold_rupees: "1000000.0000",
    basis: "PER_TXN",
  },
];

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
  /**
   * 5. C1 — A COLLECTION, and the row this table existed four fixtures without.
   *
   * ⛔ Its section is from the TCS vocabulary, which is DISJOINT from the TDS one
   * by a database CHECK — that disjointness is exactly what makes a dropped
   * `kind` refusable rather than silently wrong, and it is why one fixture of
   * this shape is enough. `deductee_name` is the BUYER on a collection: the two
   * are opposite acts and a picker that labels them alike says the company
   * withheld from someone it in fact charged.
   */
  {
    voucher_id: "3f6b1c2e-0a4d-4c8b-9e11-7a2d5f0b1005",
    voucher_no: "S1",
    section: "206C(1)-SCRAP",
    leg_seq: 0,
    deductee_name: "Vikram Scrap Traders",
    deduction_date: "2026-07-15",
    tax_rupees: "2000.0000",
    tax_year_start: 2026,
    kind: "TCS",
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
  /**
   * Mirrors `company.allow_negative_stock`. STORED, not synthesized, for the
   * reason the address row below is stored: a mock that accepted the key on
   * create and dropped it would let a round-trip test pass against a store that
   * never kept it — which is D43 re-created inside the mock.
   *
   * ⚠️ AND IT SWITCHES NOTHING HERE, WHICH IS THE HONEST STATE. This mock has
   * no negative-stock enforcement at all: `post` never computes a closing
   * position, so there is no refusal for the flag to lift. It round-trips and
   * that is the whole of its behaviour. A screen that looks right against this
   * mock is NOT evidence the escape hatch works — see the `company-features`
   * entry in `corpora/mock-vs-cored/masters.json`.
   */
  allowNegativeStock: boolean;
  fyStartDate: string;
  baseCurrency: string;
  /**
   * WHERE REALISED EXCHANGE DIFFERENCES GO (T8 step 5, migration 0080). "" is "not
   * chosen", which covers BOTH of the server's encodings of that (no
   * company_forex_settings row at all, or a row with a NULL column) — the two are
   * deliberately indistinguishable to a caller, so one empty string is the faithful
   * mirror rather than a simplification.
   */
  forexGainLossLedgerID: string;
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
    pan?: string;
    /** `undefined` is "no term agreed" — distinct from 0, "due immediately". */
    creditDays?: number;
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
    /** Born blocked (0095): an agent-created ledger no voucher may post to. */
    blocked?: boolean;
    blockedReason?: string;
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
   * WHICH VOUCHER EACH insightd DRAFT PRODUCED — the mock's stand-in for
   * migration 0077's partial unique index on
   * `voucher_head (company_id, source_draft_id)`.
   *
   * ⛔ IT IS NOT A CONVENIENCE. cored enforces once-only accept inside the
   * voucher's OWN transaction, and that guarantee is the entire point of §7.3 B:
   * a second machine, with none of this browser's memos or queues, must be unable
   * to post the same supplier bill twice. A mock that let the second POST through
   * would make every test of that guarantee green for the wrong reason — the
   * `false negative` this whole row exists to end. Kept a Map rather than derived
   * from `vouchers` because acceptance is TERMINAL ACROSS A DELETE, exactly as it
   * is on the server.
   */
  acceptedDrafts: Map<string, { voucher_id: string; voucher_no: string }>;
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
  /**
   * T4 slice 2b — RESOLUTIONS ONLY, keyed `${ledgerID}:${lineID}`.
   *
   * ⭐ THERE IS NO CANDIDATE STORE, AND THAT MIRRORS THE SERVER RATHER THAN
   * SIMPLIFYING IT. Proposals are DERIVED on every read in both places, because
   * a stored candidate goes stale silently: a voucher altered, deleted or
   * cleared between the proposal and the acceptance leaves the operator choosing
   * from a list that no longer describes the books.
   */
  bankMatches: Map<string, BankMatchResolution>;
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
  /**
   * THE PAYROLL MASTERS AND RUNS (Track 3).
   *
   * ⛔⛔ READ `mockPayrollRegister` BEFORE PINNING ANY FIGURE THAT COMES OUT OF
   * HERE. This store holds employees, their salary structure and their
   * elections as REAL ROWS — a screen must be able to read back exactly what it
   * wrote — and it COMPUTES NOTHING STATUTORY. Every PF, EPS, ESI and TDS figure
   * it serves is 0.0000, and those zeros are an ABSTENTION, not a computation.
   *
   * Reproducing the engine here would be a second implementation of Indian
   * payroll law in another language: the wage classification alone carries
   * s.2(88)'s exhaustive exclusion list and the Vivekananda Vidyamandir holding,
   * and a screen test pinning a number this file invented would be pinning a
   * payroll engine nobody reviewed. That is the D84 lesson in its sharpest form
   * — a mock that is DIFFERENT from cored is a source of false green, and
   * "different" includes being cleverer.
   *
   * So the payslip carries every statutory component as a DEFERRAL with the
   * reason written on it (the same channel cored uses for a rate that has not
   * been notified), and ⛔ THE ORACLE FOR ANY PAYROLL FIGURE IS THE BROWSER
   * AGAINST CORED — never this arm.
   */
  employees: EmployeeView[];
  /** Salary components by employee id, in insertion order. */
  salaryComponents: Map<string, SalaryComponentView[]>;
  /** Statutory elections by employee id, in insertion order. */
  statutoryElections: Map<string, StatutoryElectionView[]>;
  /**
   * Posted pay runs, keyed by wage month "YYYY-MM".
   *
   * KEYED BY MONTH because that is what uniqueness is written on: a wage month
   * is posted once, and a second run of a posted month answers `already_posted`
   * with the FIRST run's identifiers and writes nothing. A screen that read that
   * as a fresh posting would tell an operator a correction took effect when the
   * stored run is the earlier one.
   */
  payRuns: Map<
    string,
    { payRunID: string; voucherID: string; voucherNo: string }
  >;
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
   * T10 — the ITEM HIERARCHY and the company's own UNITS.
   *
   * ⛔ THE UNITS ARE HELD AS ROWS, not synthesized, because a COMPOUND unit's
   * conversion factor is what the Stock Summary restates a quantity with. A mock
   * that invented units on read could not hold a factor, so a screen test would
   * render a base quantity of zero and pass — the mock LABELLING a feature it
   * does not compute, which is register row D84 exactly (it happened once
   * already this lane, with the valuation method).
   */
  stockGroups: StockGroupListItem[];
  /**
   * T8 — the company's FOREIGN currencies. ⛔ The base currency is deliberately
   * never in here, and createCurrency below refuses it, because the SERVER
   * refuses it with a database trigger. A mock that allowed it would let a
   * screen test create "INR" on an INR company and pass — D84 exactly.
   */
  currencies: CurrencyListItem[];
  /**
   * ⭐⭐ THE RATE HISTORY, AND IT EXISTS BECAUSE T8 STEP 7 MADE `latest_rate` TOO
   * LOOSE TO BE A DOUBLE. `CurrencyListItem` carries only the LATEST rate, which is
   * all the currency-list screen needs. The unadjusted-forex report asks a
   * different question — "the latest rate ON OR BEFORE this date" — and its whole
   * Decision 3 turns on whether the answer EXISTS. A double that answered with the
   * latest rate whatever date it was asked for would price a report from a rate
   * entered after the date it claims to be as at, and would never produce the
   * unpriced row this report has to disclose. That is D84's class exactly.
   *
   * ⚠️ IT IS NOT ON THE WIRE. cored has no rate-history route; this mirrors the
   * `currency_rate` TABLE, which the server queries directly.
   */
  currencyRates: { currencyID: string; on: string; rate: string }[];
  units: UnitListItem[];
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
 * `n` whole days after an ISO date, as an ISO date.
 *
 * ⛔ ANCHORED AT MIDNIGHT UTC, LIKE {@link daysBetween}, AND THAT IS NOT
 * COSMETIC. `new Date("2026-08-23")` parses as UTC but `.toISOString()` on a
 * date built from local parts does not, so an operator west of Greenwich would
 * see every projected bucket boundary land a day early — the bug would appear
 * only on some machines, which is the worst kind.
 */
function mockAddDays(iso: string, n: number): string {
  const ms = Date.parse(`${iso}T00:00:00Z`);
  if (Number.isNaN(ms)) return iso;
  return new Date(ms + n * 86_400_000).toISOString().slice(0, 10);
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

/**
 * D111 — the name a statement is stored under when the operator PASTED it
 * rather than choosing a file.
 *
 * ⛔ IT MIRRORS THE SERVER'S SHAPE AND NOT ITS BYTES, and that is stated rather
 * than implied: cored derives the same sentence (`name.go`), but its fingerprint
 * is the first eight characters of the file's SHA-256 and this one is a 32-bit
 * FNV-1a — there is no synchronous SHA-256 in a browser. So the two names AGREE
 * IN FORM and will differ in the parenthesis, and nothing may compare a mock
 * name with a cored name expecting equality. What they do share is the property
 * the defect is about: two different pastes get two different names.
 *
 * The alternative — leaving the mock naming pastes `""` — is worse than a
 * divergence: it is the mock agreeing with the BUG, which is exactly how a
 * screen's handling of a defect passes here and fails against the real server.
 */
function mockStatementName(
  supplied: string,
  periodFrom: string,
  periodTo: string,
  lines: number,
  content: string,
): string {
  const name = (supplied ?? "").trim();
  if (name !== "") return name;
  const when = periodTo === periodFrom ? periodFrom : `${periodFrom} to ${periodTo}`;
  let h = 0x811c9dc5;
  for (let i = 0; i < content.length; i++) {
    h ^= content.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  const fingerprint = h.toString(16).padStart(8, "0");
  return `pasted statement ${when}, ${lines} ${lines === 1 ? "row" : "rows"} (${fingerprint})`;
}

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
  c: {
    ledgers: {
      id: string;
      name: string;
      group: string;
      bankParentGroup?: string;
      bankAccountNumber?: string;
      bankIFSC?: string;
    }[];
  },
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
    "jan",
    "feb",
    "mar",
    "apr",
    "may",
    "jun",
    "jul",
    "aug",
    "sep",
    "oct",
    "nov",
    "dec",
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

// ---------------------------------------------------------------------------
// T4 SLICE 2b — the mock's matcher.
//
// ⚠️ READ THIS BEFORE PINNING A TIER IN A SCREEN TEST. This is a NARROWER
// matcher than the server's, and the narrowing is deliberate and listed, in the
// same spirit as `previewBankStatement`'s refusal to copy the synonym list:
//
//   * NO PARTIAL TIER. The server finds a bank row that is the sum of two
//     vouchers, and the reverse. This does not, so a line that would be
//     `partial` on the server is `""` (no candidate) here.
//   * NO STRONG-KEY TIER. The server promotes a match to `exact` when a cheque
//     number or UTR appears on the voucher. This looks only at amount, direction
//     and date.
//   * THE WINDOW IS FIXED. The server takes it as a parameter; this uses the
//     same default (30 — Tally's own documented "Find Potential Matches within"
//     default) so a screen test never pins a number the server would not send.
//
// ⭐ WHAT IT DOES REPRODUCE FAITHFULLY IS THE PART A SCREEN CAN GET WRONG:
// amounts compared to the PAISE with zero tolerance, direction compared
// directly, and AMBIGUITY IN BOTH DIRECTIONS — one row with two candidates, and
// one voucher claimed by two rows. Those are invariant B-1, which is the rule
// the whole slice exists to honour, and a screen that mis-renders it is the
// failure that matters.
// ---------------------------------------------------------------------------
const MOCK_MATCH_WINDOW_DAYS = 30;

function mockDaysApart(a: string, b: string): number {
  const ms = Date.parse(a + "T00:00:00Z") - Date.parse(b + "T00:00:00Z");
  return Math.abs(Math.round(ms / 86400000));
}

/** Every un-cleared, un-matched bank leg of one ledger, as a candidate shape. */
function mockCandidateLegs(
  c: MockCompany,
  ledgerID: string,
  ledgerName: string,
): BankMatchCandidate[] {
  const out: BankMatchCandidate[] = [];
  for (const v of c.vouchers) {
    const legs = v.postings.filter((p) => p.ledger === ledgerName);
    legs.forEach((leg, legNo) => {
      // A leg with ANY clearance is not a candidate — not "un-cleared as at a
      // date", which is a different question. Offering a cleared leg would let a
      // second clearance be asserted over the first.
      if (c.bankDates.has(`${ledgerID}:${v.voucher_id}:${legNo}`)) return;
      out.push({
        voucher_id: v.voucher_id,
        leg_no: legNo,
        voucher_no: v.voucher_no,
        class: v.class,
        voucher_date: v.date,
        narration: v.narration,
        party: v.party,
        amount: leg.amount,
        dr_cr: leg.dr_cr as DrCr,
        tier: "",
        why: "",
      });
    });
  }
  return out;
}

function mockProposeMatches(
  lines: BankStatementLine[],
  legs: BankMatchCandidate[],
): BankMatchProposal[] {
  const proposals: BankMatchProposal[] = lines.map((ln) => {
    const candidates: BankMatchCandidate[] = [];
    for (const leg of legs) {
      // ZERO TOLERANCE, compared as PAISE INTEGERS. A one-paisa difference is a
      // bank charge or a different transaction, and a tolerance is how a real
      // difference hides for a year.
      if (mockMoneyToUnits(leg.amount) !== mockMoneyToUnits(ln.amount)) continue;
      if (leg.dr_cr !== ln.dr_cr) continue;
      const apart = mockDaysApart(ln.value_date, leg.voucher_date);
      if (apart === 0) {
        candidates.push({
          ...leg,
          tier: "exact",
          why: "amount, direction and date all agree to the paisa and the day",
        });
      } else if (apart <= MOCK_MATCH_WINDOW_DAYS) {
        candidates.push({
          ...leg,
          tier: "potential",
          why:
            `amount and direction agree to the paisa; the voucher is dated ${leg.voucher_date} ` +
            `and the bank ${ln.value_date} — a cheque clearing late is ordinary`,
        });
      }
    }
    candidates.sort((a, b) => (a.tier === "exact" ? 0 : 1) - (b.tier === "exact" ? 0 : 1));
    const bestTier = candidates[0]?.tier ?? "";
    const atBest = candidates.filter((x) => x.tier === bestTier).length;
    return {
      line_id: ln.id,
      line_no: ln.line_no,
      value_date: ln.value_date,
      description: ln.description,
      amount: ln.amount,
      dr_cr: ln.dr_cr,
      bank_reference: ln.bank_reference || undefined,
      cheque_number: ln.cheque_number || undefined,
      best_tier: bestTier,
      candidates,
      ambiguous: atBest > 1,
      acceptable_in_bulk: atBest === 1 && bestTier === "exact",
    };
  });

  // ⛔ THE MIRROR AMBIGUITY: one leg claimed by more than one bank row. Each of
  // those rows looks perfectly unambiguous ON ITS OWN, which is why this pass
  // exists — without it both would be offered for bulk acceptance and the second
  // would collide after the first had already asserted a clearance.
  const claimants = new Map<string, number[]>();
  proposals.forEach((p, i) => {
    for (const cnd of p.candidates) {
      if (cnd.tier !== p.best_tier) continue;
      const k = `${cnd.voucher_id}:${cnd.leg_no}`;
      claimants.set(k, [...(claimants.get(k) ?? []), i]);
    }
  });
  for (const idxs of claimants.values()) {
    if (idxs.length < 2) continue;
    for (const i of idxs) {
      proposals[i]!.ambiguous = true;
      proposals[i]!.acceptable_in_bulk = false;
      proposals[i]!.contested_by = idxs
        .filter((o) => o !== i)
        .map((o) => proposals[o]!.line_no)
        .sort((a, b) => a - b);
    }
  }
  return proposals;
}

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
    if (bal !== "")
      line.running_balance = mockUnitsToMoney(mockMoneyToUnits(bal));
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
    "report:gstr9",
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
  /**
   * THE AGENT PLANE's tables, pre-filled (W2). The mock has no agent loop, so a
   * test that wants something in the approval inbox or the side panel seeds it
   * here. `model: true` makes startAgentRun record a run instead of answering
   * 503 AGENT_UNCONFIGURED (the shipped server default, and the mock's).
   */
  agent?: {
    model?: boolean;
    runs?: AgentRun[];
    approvals?: AgentApproval[];
    events?: Record<string, AgentEvent[]>;
    /** W2 breadth: pre-set autonomy rows. */
    autonomy?: AgentAutonomyRow[];
    /** Whether the signed-in member is an Owner (default true). */
    owner?: boolean;
  };
}

/** What a mock client holds of the agent plane — per client, like `companies`. */
interface MockAgentState {
  model: boolean;
  runs: AgentRun[];
  approvals: AgentApproval[];
  events: Map<string, AgentEvent[]>;
  decisions: ({ approvalID: string } & AgentDecisionRequest)[];
  /** W2 breadth: the autonomy table, and whether the caller is an Owner. */
  autonomy: AgentAutonomyRow[];
  owner: boolean;
  /** Set by createMockClientWith: marks one of its ledgers blocked (tests). */
  blockLedger?: (companyID: string, ledgerID: string, reason: string) => void;
}

/**
 * The keys the mock offers for autonomy. A SECOND COPY of the server's
 * autonomyKeys(), trimmed to the ones a screen test needs; the live client
 * reads the server's list, which is the authority.
 */
const MOCK_AUTONOMY_KEYS = [
  "alter bank-reconciliation",
  "alter payment",
  "create journal",
  "create master",
  "create payment",
  "create receipt",
  "create sales",
];

const mockAgentStates = new WeakMap<CoredClient, MockAgentState>();

/**
 * Every decision a mock client has recorded, in order (tests only).
 *
 * It exists so a test can assert what was DECIDED — the one thing a screen in
 * this plane sends that matters — and, as importantly, what was NOT: a prefilled
 * voucher whose Ctrl+A posted through createVoucher instead of through here
 * would leave the approval pending and put a second, unapproved voucher on the
 * books the moment somebody approved the first.
 */
export function mockAgentDecisions(
  client: CoredClient,
): readonly ({ approvalID: string } & AgentDecisionRequest)[] {
  return mockAgentStates.get(client)?.decisions ?? [];
}

/**
 * Add an approval to a mock client's inbox after construction (tests only) —
 * for a fixture whose ledger ids exist only once the test has created the
 * ledgers through the same client.
 */
export function mockAddAgentApproval(client: CoredClient, a: AgentApproval): void {
  const st = mockAgentStates.get(client);
  if (!st) throw new Error("mockAddAgentApproval: not a mock client");
  st.approvals.push(JSON.parse(JSON.stringify(a)) as AgentApproval);
}

function newMockAgentState(opts: MockClientOptions): MockAgentState {
  // Deep-copied so a decision never mutates a test's own fixture object — a
  // fixture reused by a second test would otherwise arrive already decided.
  return {
    model: opts.agent?.model ?? false,
    runs: (opts.agent?.runs ?? []).map((r) => JSON.parse(JSON.stringify(r)) as AgentRun),
    approvals: (opts.agent?.approvals ?? []).map(
      (a) => JSON.parse(JSON.stringify(a)) as AgentApproval,
    ),
    events: new Map(
      Object.entries(opts.agent?.events ?? {}).map(([k, v]) => [k, v.slice()]),
    ),
    decisions: [],
    autonomy: (opts.agent?.autonomy ?? []).map((r) => ({ ...r })),
    owner: opts.agent?.owner ?? true,
  };
}

/**
 * Mark a mock ledger BORN BLOCKED, as an agent's commit would (tests only):
 * the mock has no agent loop to create one.
 */
export function mockBlockLedger(client: CoredClient, companyID: string, ledgerID: string, reason: string): void {
  const block = mockAgentStates.get(client)?.blockLedger;
  if (!block) throw new Error("mockBlockLedger: not a mock client");
  block(companyID, ledgerID, reason);
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

// ---------------------------------------------------------------------------
// T11 — BUDGETS (migrations 0085/0086)
// ---------------------------------------------------------------------------
//
// ⛔ THE FIXTURE DELIBERATELY MIXES THE TWO BASES, because the mixed case is the
// one a screen gets wrong. A plan holding a CLOSING_BALANCE line and a
// NETT_TRANSACTIONS line has NO addable grand total — a position plus a flow is
// not a quantity — and `totals_are_addable: false` with three ZERO grand totals
// is the shape every consumer must handle. A single-basis fixture would let a
// screen draw a footer unconditionally and pass every test.
//
// ⛔ AND IT CARRIES ONE ROW OF EACH AWKWARD STATUS: an UNSPENT budget (nothing
// posted), an UNBUDGETED actual in its own array with `variance_percent: null`,
// and an ordinary OVER. A fixture of well-behaved rows is how a screen ships with
// no "no budget set" rendering at all and a divide-by-zero in production.

function mockBudgets(): Budget[] {
  return [
    {
      id: "budget-fy26",
      name: "FY 2026-27 Plan",
      from_date: "2026-04-01",
      to_date: "2027-03-31",
      line_count: 3,
      created_by: "seed",
      created_at: "2026-04-01T00:00:00Z",
    },
    {
      // An EMPTY plan, on purpose: the variance report over it is a page of
      // unbudgeted actuals, which reads as "nothing was planned" and is
      // indistinguishable from "the plan was never keyed". A screen must be able
      // to show that a plan has no lines BEFORE drawing a report from it.
      id: "budget-empty",
      name: "Draft Plan (no lines yet)",
      from_date: "2026-04-01",
      to_date: "2027-03-31",
      line_count: 0,
      created_by: "seed",
      created_at: "2026-04-02T00:00:00Z",
    },
  ];
}

function mockBudgetLines(budgetID: string): BudgetLine[] {
  if (budgetID !== "budget-fy26") return [];
  const line = (
    id: string,
    dimension: BudgetDimension,
    targetID: string,
    targetName: string,
    nature: string,
    basis: BudgetBasis,
    drCr: string,
    amount: string,
    note = "",
  ): BudgetLine => ({
    id,
    budget_id: budgetID,
    dimension,
    target_id: targetID,
    target_name: targetName,
    cost_category_id: "",
    cost_category_name: "",
    nature,
    basis,
    dr_cr: drCr,
    amount,
    note,
    basis_means:
      basis === "CLOSING_BALANCE"
        ? "a POSITION as at the budget period's end date, with the opening balance folded in"
        : "MOVEMENT inside the budget period, with the opening balance excluded",
    created_at: "2026-04-01T00:00:00Z",
  });
  return [
    line(
      "budget-line-travel",
      "LEDGER",
      "ledger-travel",
      "Travel Expenses",
      "EXPENSE",
      "NETT_TRANSACTIONS",
      "D",
      "500000.0000",
      "assumes two regional trips per quarter",
    ),
    line(
      "budget-line-rent",
      "LEDGER",
      "ledger-rent",
      "Office Rent",
      "EXPENSE",
      "NETT_TRANSACTIONS",
      "D",
      "120000.0000",
    ),
    // The CLOSING_BALANCE line — this is what makes the plan mixed-basis.
    line(
      "budget-line-debtors",
      "GROUP",
      "group-sundry-debtors",
      "Sundry Debtors",
      "ASSET",
      "CLOSING_BALANCE",
      "D",
      "4000000.0000",
      "year-end receivables ceiling",
    ),
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

/**
 * The mock's year-end lock. Module-level so a screen test can close, re-open
 * and close again through one client and see the state move — a mock that
 * forgot between calls would let a screen pass while doing nothing.
 */
let mockClosedThrough: string | null = null;
let mockClosedAt: string | null = null;
let mockClosedBy: string | null = null;

function mockClosure(): BooksClosure {
  return {
    closed: mockClosedThrough !== null,
    closedThrough: mockClosedThrough,
    closedAt: mockClosedAt,
    closedBy: mockClosedBy,
  };
}

/** Reset the mock's year-end lock between tests. */
export function resetMockBooksClosure(): void {
  mockClosedThrough = null;
  mockClosedAt = null;
  mockClosedBy = null;
}

export function createMockClient(opts: MockClientOptions = {}): CoredClient {
  const agentState = newMockAgentState(opts);
  const client = createMockClientWith(opts, agentState);
  mockAgentStates.set(client, agentState);
  return client;
}

function createMockClientWith(
  opts: MockClientOptions,
  agentState: MockAgentState,
): CoredClient {
  const companies = new Map<string, MockCompany>();

  /**
   * PRINT TEMPLATES (track C): one row per (company, doc_kind), which is exactly
   * `print_template`'s UNIQUE constraint.
   *
   * ⛔ PER-CLIENT AND NOT MODULE-SCOPED, and this was a real defect before it was
   * one line. A module-scoped map is shared by every `createMockClient()` in a
   * process, so one test's save leaked into the next test's "this company has
   * customised nothing" — which failed loudly here, but in a suite ordered
   * differently would have made a test pass for the wrong reason. `companies`
   * above is per-client for the same reason; state that models a DATABASE belongs
   * to the client that pretends to be one.
   */
  const printTemplates = new Map<string, PrintTemplateOverrides>();

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
  function mockFindOrder(
    direction: OrderDirection,
    orderID: string,
  ): MockOrder {
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

  /**
   * ⛔ THE ONE IMPLEMENTATION BEHIND BOTH AGEING METHODS (D94), mirroring cored's
   * `writeOutstandings`, which is the single writer behind its two routes.
   *
   * ⛔⛔ NEITHER PUBLIC METHOD CALLS THE OTHER. Delegating `getBillsPayable` through
   * `this.getOutstandings` meant a test that spied on the receivables method also
   * observed — and could stub — the payables one: a mock coupling two surfaces the
   * server keeps independent, which is D84's class of false green pointed at the very
   * split D94 exists to make. Caught by the arm asserting the payables SCREEN never
   * touches `getOutstandings`.
   */
  async function outstandingsOf(
    companyID: string,
    kind: OutstandingKind,
    asOn?: string,
  ): Promise<OutstandingsResponse> {
    const c = ensure(companyID);
    const wantGroup =
      kind === "PAYABLE" ? "Sundry Creditors" : "Sundry Debtors";
    // ⛔⛔ TWO QUESTIONS, TWO VARIABLES — AND THIS MOCK USED TO CONFLATE THEM
    // EXACTLY AS CORED DID (D86). `bound` is the date the ROWS are limited to
    // ("" = not limited); `agedOn` is the day the ageing is measured against and
    // is always a real date. Echoing the resolved date as `as_on` while listing
    // every bill whatever its date is a caption that is present and false, and it
    // is what made `verify:demo`'s and the screen's vitest arms green either way.
    //
    // ⛔ AND THE BOUND IS NOW APPLIED, WHICH IT NEVER WAS HERE. The loop below had
    // no date predicate at all, so the mock was UNBOUNDED in both cases — looser
    // than cored in the direction that hides a bug (D84's class). With `as_on`
    // supplied it now excludes later vouchers, exactly as cored does, so a vitest
    // that asks "as at April" gets April's answer here too.
    const bound = (asOn ?? "").trim();
    const agedOn = bound || new Date().toISOString().slice(0, 10);

    // Net every reference on a CURRENT, non-deleted voucher. c.views holds
    // exactly those: an ALTER restates the view in place and a DELETE removes it.
    // ⭐ THE KEY IS `ledger \0 ref \0 currency-code` SINCE T8 STEP 6, and `code` is
    // on the VALUE as well so the emitted row does not have to re-split the key.
    const net = new Map<
      string,
      {
        ledgerID: string;
        ref: string;
        code: string;
        units: bigint;
        foreign: bigint;
        due: string;
      }
    >();
    for (const view of c.views.values()) {
      if (bound !== "" && (view.voucher_date ?? "") > bound) continue;
      for (const b of view.bill_allocations) {
        const ledger = c.ledgers.find((l) => l.id === b.ledger_id);
        if (!ledger || ledger.group !== wantGroup) continue;
        // Positive on the party's natural side: debit for a receivable.
        const signed =
          b.dr_cr === "D"
            ? mockMoneyToUnits(b.amount)
            : -mockMoneyToUnits(b.amount);
        // ⭐⭐ T8 STEP 6 — THE CURRENCY IS PART OF THE NETTING KEY, exactly as
        // cored's GROUP BY is (Q5: "a ref_name that appears under two different
        // currencies is a data error, not a sum"). ⛔ A DOUBLE THAT NETTED THEM
        // TOGETHER WOULD BE LOOSER THAN THE SERVER, which is D84 and the reason
        // this whole residue block exists: 61 test files build books through here.
        //
        // ⚠️ `On Account` IS COLLAPSED TO BASE, on BOTH sides, because cored
        // collapses it: the residue is a difference of sums over many rates and
        // has no single currency, and an explicit ON_ACCOUNT allocation carrying
        // one would otherwise land in a different bucket from the derived residue
        // that cancels it — two rows that each look right.
        const code = b.ref_name === ON_ACCOUNT_REF ? "" : (b.currency ?? "");
        const key = `${b.ledger_id}\u0000${b.ref_name}\u0000${code}`;
        const cur = net.get(key) ?? {
          ledgerID: b.ledger_id,
          ref: b.ref_name,
          code,
          units: 0n,
          foreign: 0n,
          due: "",
        };
        cur.units += kind === "PAYABLE" ? -signed : signed;
        // ⛔ THE FOREIGN COLUMN IS SIGNED THE SAME WAY AND ZERO IN THE On Account
        // BUCKET. `foreign_amount` is absent on a base row, which is 0 here — not
        // NaN, and not the base amount.
        if (b.ref_name !== ON_ACCOUNT_REF && b.foreign_amount) {
          const sf =
            b.dr_cr === "D"
              ? mockMoneyToUnits(b.foreign_amount)
              : -mockMoneyToUnits(b.foreign_amount);
          cur.foreign += kind === "PAYABLE" ? -sf : sf;
        }
        // The EARLIEST due date on the reference, as the server takes MIN().
        if (b.due_date && (cur.due === "" || b.due_date < cur.due))
          cur.due = b.due_date;
        net.set(key, cur);
      }
    }

    // ⛔⛔ THE ON-ACCOUNT RESIDUE (D106), MODELLED HERE BECAUSE A MOCK THAT IS
    // LOOSER THAN CORED IS A SOURCE OF FALSE GREEN — which is D84, and three lanes
    // hit it. cored reports the part of a party's balance that names no reference
    // as one un-aged `On Account` row, via the identity
    //
    //     On Account = the ledger's whole balance − every named reference
    //
    // so the report reconciles to Sundry Debtors/Creditors by construction. If
    // this double kept reporting only references, 61 test files would go on
    // building books whose two reports silently disagree, and the screen tests
    // would prove the old behaviour.
    //
    // ⚠️ IT IS THE SAME TWO PASSES AS THE SERVER'S: add every party POSTING, then
    // take every named reference back out. A fully referenced voucher contributes
    // +X and −X and vanishes, which is why an ordinary book grows no such row.
    // ⚠️ THE On Account KEY CARRIES AN EMPTY CURRENCY, and it must be spelled the
    // same way the bill-allocation key spells base, or the collapse silently
    // becomes two buckets that never meet.
    const onAcctKey = (ledgerID: string) =>
      `${ledgerID}\u0000${ON_ACCOUNT_REF}\u0000`;
    for (const view of c.views.values()) {
      if (bound !== "" && (view.voucher_date ?? "") > bound) continue;
      const bump = (ledgerID: string, units: bigint) => {
        const ledger = c.ledgers.find((l) => l.id === ledgerID);
        if (!ledger || ledger.group !== wantGroup) return;
        const key = onAcctKey(ledgerID);
        const cur = net.get(key) ?? {
          ledgerID,
          ref: ON_ACCOUNT_REF,
          code: "",
          units: 0n,
          foreign: 0n,
          due: "",
        };
        // ⛔ NO FOREIGN FIGURE ON THE RESIDUE, matching cored's residue branches:
        // `posting` DOES carry a currency and it is deliberately not read, because
        // these two passes compute a DIFFERENCE OF SUMS whose halves are keyed on
        // different tables. Base-only keeps D106's identity true by construction.
        cur.units += kind === "PAYABLE" ? -units : units;
        net.set(key, cur);
      };
      for (const p of view.postings ?? []) {
        bump(
          p.ledger_id,
          p.dr_cr === "D" ? mockMoneyToUnits(p.amount) : -mockMoneyToUnits(p.amount),
        );
      }
      for (const b of view.bill_allocations) {
        bump(
          b.ledger_id,
          b.dr_cr === "D" ? -mockMoneyToUnits(b.amount) : mockMoneyToUnits(b.amount),
        );
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
    let onAccountUnits = 0n;

    // ⛔⛔ T8 STEP 6 — THE CONFLICT COUNT IS TAKEN BEFORE THE ROWS ARE BUILT, over
    // the groups that SURVIVE the zero skip. A reference is in conflict only once
    // every one of its currency groups has been seen, so it cannot be decided while
    // the rows are being emitted — and counting closed groups too would flag a
    // correctly settled bill for ever. cored does the same in a second pass.
    const survives = (u: bigint, f: bigint) => u !== 0n || f !== 0n;
    const groupsPerRef = new Map<string, number>();
    for (const g of net.values()) {
      if (!survives(g.units, g.foreign)) continue;
      const rk = `${g.ledgerID}\u0000${g.ref}`;
      groupsPerRef.set(rk, (groupsPerRef.get(rk) ?? 0) + 1);
    }

    for (const { ledgerID, ref, code, units, foreign, due } of [
      ...net.values(),
    ].sort((a, b) => a.ref.localeCompare(b.ref) || a.code.localeCompare(b.code))) {
      // A fully-settled bill nets to zero and is not outstanding.
      //
      // ⛔ SINCE STEP 6 THAT IS TWO QUESTIONS. A bill with nothing left in rupees
      // and $400 left in dollars is the row an exporter has to see, and a
      // base-only skip drops it off the report entirely.
      if (!survives(units, foreign)) continue;
      const ledger = c.ledgers.find((l) => l.id === ledgerID);
      if (!ledger) continue;
      // ⛔ THE ON-ACCOUNT ROW IS UN-AGED AND IN NO SLAB, exactly as cored leaves
      // it: money with no bill has no due date to be overdue against, and ageing
      // it would file it in the FIRST slab as the freshest debt on the party.
      const onAccount = ref === ON_ACCOUNT_REF;
      const overdue =
        onAccount || due === ""
          ? 0
          : Math.max(
              0,
              Math.round((Date.parse(agedOn) - Date.parse(due)) / 86_400_000),
            );
      const slab = onAccount
        ? ""
        : (SLABS.find(([, upto]) => upto !== 0 && overdue <= upto)?.[0] ??
          "180+ days");
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
        on_account: onAccount,
        currency_code: code,
        foreign_pending: mockUnitsToMoney(foreign),
        currency_conflict:
          (groupsPerRef.get(`${ledgerID}\u0000${ref}`) ?? 0) > 1,
      });
      party.pending = mockUnitsToMoney(mockMoneyToUnits(party.pending) + units);
      byParty.set(ledgerID, party);
      // ⛔ ON-ACCOUNT MONEY IS IN NO SLAB, so it is accumulated separately and the
      // slab column is deliberately SHORT of the total by exactly this much.
      if (onAccount) onAccountUnits += units;
      else slabTotals.set(slab, (slabTotals.get(slab) ?? 0n) + units);
      total += units;
    }

    return {
      kind,
      // ⛔ "" WHEN NOTHING WAS ASKED FOR — the same convention cored and
      // /stock-summary use, and the string `outstandingsScopeLine` turns into the
      // NO DATE LIMIT sentence. Echoing `agedOn` here is the defect.
      as_on: bound,
      aged_as_on: agedOn,
      on_account_total: mockUnitsToMoney(onAccountUnits),
      parties: [...byParty.values()].sort((a, b) =>
        a.ledger.localeCompare(b.ledger),
      ),
      slab_totals: SLABS.filter(([l]) => slabTotals.has(l)).map(([label]) => ({
        label,
        amount: mockUnitsToMoney(slabTotals.get(label) ?? 0n),
      })),
      total: mockUnitsToMoney(total),
    };
  }

  agentState.blockLedger = (companyID, ledgerID, reason) => {
    const l = ensure(companyID).ledgers.find((x) => x.id === ledgerID);
    if (!l) throw new Error(`mockBlockLedger: no ledger ${ledgerID}`);
    l.blocked = true;
    l.blockedReason = reason;
  };

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
        // Mirrors the column default. FALSE is "refuse", and a mock whose
        // default were the permissive one would make the escape hatch look like
        // the shipped behaviour.
        allowNegativeStock: false,
        fyStartDate: "2026-04-01",
        baseCurrency: "INR",
        // "" is "not chosen", the state every company starts in.
        forexGainLossLedgerID: "",
        profile: defaultProfile("2026-04-01"),
        ledgers: [],
        // Every company is seeded with exactly one system location, mirroring
        // migration 0025's seed_inventory_defaults. Without it the item grid's
        // godown picker would be empty on a fresh company and every line would
        // post untracked — a silent downgrade of a feature that IS available.
        godowns: [mainLocation(companyID)],
        stockItems: [],
        batches: [],
        stockGroups: [],
        currencies: [],
        currencyRates: [],
        // "Nos" is seeded with every company by migration 0005's
        // seed_phase1_voucher_types, and the item form's unit picker reads this
        // list — a fresh company with an empty one could not create an item at
        // all (the form refuses an item with no unit, deliberately).
        units: [seedUnit(companyID, "Nos", "Numbers")],
        vouchers: [],
        acceptedDrafts: new Map(),
        bankDates: new Map(),
        deductorStatuses: new Map(),
        bankStatements: new Map(),
        bankMatches: new Map(),
        tcsDeclarations: new Map(),
        employees: [],
        salaryComponents: new Map(),
        statutoryElections: new Map(),
        payRuns: new Map(),
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
      // The demo tenant REFUSES negative stock, matching a freshly created
      // cored company. The seed must not hand the browser a permissive
      // default that no real tenant starts with.
      allowNegativeStock: false,
      fyStartDate: "2026-04-01",
      baseCurrency: "INR",
      // "" is "not chosen", the state every company starts in.
      forexGainLossLedgerID: "",
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
      stockGroups: [],
      currencies: [],
      currencyRates: [],
      units: [seedUnit(demoID, "Nos", "Numbers")],
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
          // Ungrouped, like every item that predates T10 — a real state, and the
          // one the Stock Summary's ungrouped bucket exists to report.
          stock_group: "",
        },
        {
          id: "stock-gasket",
          name: "Gasket",
          unit_symbol: "Nos",
          hsn: "4016",
          gst_rate_pct: "12.0000",
          valuation_method: "WEIGHTED_AVG",
          is_system: false,
          stock_group: "",
        },
      ],
      batches: [],
      vouchers: [],
      acceptedDrafts: new Map(),
      bankDates: new Map(),
      deductorStatuses: new Map(),
      bankStatements: new Map(),
      bankMatches: new Map(),
      tcsDeclarations: new Map(),
      employees: [],
      salaryComponents: new Map(),
      statutoryElections: new Map(),
      payRuns: new Map(),
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
  /**
   * Echo T8's three forex values onto a posting or bill-allocation VIEW —
   * present only when the row is foreign, exactly as the live GET does
   * (server.go's addForexJSON).
   *
   * ⛔⛔ ABSENT ON A BASE ROW, NEVER EMPTY-STRINGED. `currency_id: ""` is not
   * "no currency": it is a two-of-three triple, which the live server refuses.
   * A mock that emitted empty keys would let a client that echoes the GET back
   * verbatim pass every offline test and 422 against cored on the first base
   * voucher — the exact class of divergence the mock/cored contract test
   * exists to catch.
   *
   * ⚠️ THE RATE IS PASSED THROUGH UNCHANGED, NOT RE-FORMATTED. It is
   * NUMERIC(19,10) on the server and every money helper in this file is
   * fixed-4; running it through `mockUnitsToMoney` would hand back 83.6512 and
   * make the mock the one place where the bug is invisible.
   */
  function mockForex(
    c: MockCompany,
    row: { currency_id?: string; foreign_amount?: string; rate?: string },
  ): {
    currency_id?: string;
    currency?: string;
    foreign_amount?: string;
    rate?: string;
  } {
    if (!row.currency_id) return {};
    const cur = (c.currencies ?? []).find((x) => x.id === row.currency_id);
    return {
      currency_id: row.currency_id,
      currency: cur?.code ?? "",
      foreign_amount: row.foreign_amount ?? "",
      rate: row.rate ?? "",
    };
  }

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
        // ⛔ AND RETAIN T8's THREE FOREX VALUES, for exactly the reason the
        // ledger_id is retained one line up. A mock that dropped them would
        // make every offline and component-test round trip of a foreign
        // voucher look CORRECT while the live one lost the currency — and the
        // mock is the oracle those tests trust. See mockForex.
        ...mockForex(c, p),
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
  /**
   * Project a request's `tds`/`tcs` block onto the voucher view (register row
   * D88) — echoing what the OPERATOR stated and deriving nothing.
   *
   * ⛔⛔ THE DERIVED FIGURES ARE OMITTED ON PURPOSE, AND THIS IS ONE PLACE THIS
   * MOCK IS DELIBERATELY LESS CAPABLE THAN THE SERVER RATHER THAN MORE
   * FORGIVING. Deriving `rate_pct` and the amount would put a second copy of
   * `internal/compliance`'s effective-dated tables and its two no-PAN rules in
   * the browser bundle, and the copy that drifts always wins for whichever
   * caller reaches it first. Omitting them makes a test that reads one fail on
   * `undefined` instead of quietly agreeing with a plausible zero.
   *
   * ⚠️ WHAT IT DOES MIRROR IS THE PART A ROUND TRIP DEPENDS ON: the operator's
   * own statements come back out unchanged, so a form rebuilt from this view
   * sends the same block the server would have stored.
   */
  function tdsView(
    block: TDSRequest | null | undefined,
  ): TDSDeductionView | null {
    if (!block) return null;
    return {
      leg_seq: 0,
      section: block.section,
      deductee_ledger_id: block.deductee_ledger_id,
      deductee_type: block.deductee_type ?? "OTHER",
      // ⚠️ ABSENT MEANS FALSE, which is what the Go decoder does with a missing
      // bool — and getting it the other way would hand back a PAN-on-record
      // claim the request never made, which LOWERS the rate.
      pan_available: block.pan_available ?? false,
      taxable_amount: block.taxable_amount,
    };
  }

  function tcsView(
    block: TCSRequest | null | undefined,
  ): TCSCollectionView | null {
    if (!block) return null;
    return {
      leg_seq: 0,
      section: block.section,
      buyer_ledger_id: block.buyer_ledger_id,
      pan_available: block.pan_available ?? false,
      taxable_amount: block.taxable_amount,
    };
  }

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
          `physical count line ${i + 1} (${name}): the count is ${q4str(delta)} above the books, and bringing a surplus in at zero cost would carry the extra units at nothing and write the item's stock value down; state the rate the extra units are carried at`,
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
  function costAllocationViews(req: VoucherRequest): CostAllocationView[] {
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
      if (
        !Number.isInteger(a.posting_seq) ||
        a.posting_seq < 0 ||
        a.posting_seq >= postings.length
      ) {
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
        cost_category_name:
          cats.find((c) => c.id === a.cost_category_id)?.name ?? "",
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
    company: MockCompany,
    req: VoucherRequest,
    voucherNo: string,
    postings: readonly { ledger_id: string; dr_cr: string; amount: string }[],
  ): BillAllocationView[] {
    const bs = req.bill_allocations ?? [];
    if (bs.length === 0) return [];
    const canonDC = (dc: string) =>
      dc.trim().toUpperCase().startsWith("D") ? "D" : "C";
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
      signedByLedger.set(
        b.ledger_id,
        (signedByLedger.get(b.ledger_id) ?? 0n) + signed,
      );
      out.push({
        ledger_id: b.ledger_id,
        // THE SUBSTITUTION. An empty name is the normal case for a reference that
        // OPENS a bill — see the header.
        ref_name: b.ref_name.trim() === "" ? voucherNo : b.ref_name.trim(),
        ref_type: b.ref_type,
        dr_cr: dc,
        amount: mockUnitsToMoney(units(b.amount)),
        due_date: b.due_date ?? "",
        ...mockForex(company, b),
      });
    }
    const postedByLedger = new Map<string, bigint>();
    for (const p of postings) {
      const signed =
        canonDC(p.dr_cr) === "C" ? -units(p.amount) : units(p.amount);
      postedByLedger.set(
        p.ledger_id,
        (postedByLedger.get(p.ledger_id) ?? 0n) + signed,
      );
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
    // ⛔⛔ §7.3 B — THE ONCE-ONLY ACCEPT, AND IT IS FIRST FOR THE REASON THE
    // SERVER'S IS INSIDE THE TRANSACTION: the loser must write NOTHING. Not a
    // voucher, not a number, not a day-book row. A mock that refused after
    // pushing the entry would still be "correct" about the status code and would
    // hide the only failure that matters.
    //
    // Mirrors migration 0077 exactly: keyed on the draft id alone (never on the
    // body — a second machine can send a different amount for the same bill), and
    // TERMINAL, so deleting the voucher does not put the bill back in the inbox.
    const draftID = req.draft_id ?? "";
    if (draftID !== "") {
      const won = c.acceptedDrafts.get(draftID);
      if (won) {
        // The refusal shape is insightd's, because that is the one shape
        // draftAccept.ts parses — `detail.code` picks the sentence, and
        // `accepted_voucher_no` is the fact the operator needs. `error` is carried
        // too because http.ts reads THAT key and nothing else to build
        // ApiError.message. Both, exactly as cored's writeDraftAlreadyAccepted does.
        const message =
          `ledger: draft ${draftID} was already accepted as voucher ` +
          `${won.voucher_no || won.voucher_id}; nothing was posted`;
        throw new ApiError(409, message, {
          error: message,
          detail: {
            code: "draft_already_accepted",
            draft_id: draftID,
            accepted_voucher_no: won.voucher_no,
            accepted_voucher_id: won.voucher_id,
          },
        });
      }
    }
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
      bill_allocations: billViews(c, req, voucherNo, postingViews),
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
      // ⛔ THE STATUTORY BLOCK, RE-STATED FROM THIS BODY (register row D88). Not
      // carried forward: `tds_deduction` is deleted and re-derived on every
      // alter server-side, so a mock that kept a stale one would be MORE
      // forgiving than the server about the exact thing this projection exists
      // to make safe.
      tds: tdsView(req.tds),
      tcs: tcsView(req.tcs),
    });
    // The draft is SPENT, and only now — after every write this call makes has
    // landed. Recorded on CREATE only: an alter or a delete restating a draft id
    // would repoint provenance and consume a draft whose bill never reached the
    // books, which is why the server strips it for any op but CREATE.
    if (draftID !== "") {
      c.acceptedDrafts.set(draftID, {
        voucher_id: voucherID,
        voucher_no: voucherNo,
      });
    }
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
      bill_allocations: billViews(c, req, entry.voucher_no, postingViews),
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
      // ⛔ THE STATUTORY BLOCK, RE-STATED FROM THIS BODY (register row D88). Not
      // carried forward: `tds_deduction` is deleted and re-derived on every
      // alter server-side, so a mock that kept a stale one would be MORE
      // forgiving than the server about the exact thing this projection exists
      // to make safe.
      tds: tdsView(req.tds),
      tcs: tcsView(req.tcs),
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

  /**
   * A master name's IDENTITY, as migration 0075 defines it: case-folded and
   * with surrounding whitespace removed.
   *
   * ⚠️ NOT a full whitespace fold. "Acme  Traders" (two inner spaces) stays
   * distinct from "Acme Traders", deliberately: an inner double space is
   * VISIBLE in a picker, while a trailing space and a capital letter are not,
   * and the rule is aimed at the collisions an operator cannot see.
   */
  /**
 * The seeded PRIMARY groups whose `account_nature` is INCOME or EXPENSE — migration
 * 0003's six Profit & Loss groups, and the only groups a realised exchange difference
 * may be booked under.
 *
 * ⛔ A NAME LIST HERE STANDS IN FOR A COLUMN THE MOCK DOES NOT HAVE, exactly as
 * `bankParentGroup` stands in for the ltree ancestry `reporting.BankLedgerPredicate`
 * walks. The server tests `account_group.nature IN ('INCOME','EXPENSE')`, which is
 * the primitive; this mock has no group hierarchy and no nature column, so it matches
 * the immediate group name against the seeded set and against the ancestor a
 * user-created sub-group records.
 *
 * ⚠️ SO A LEDGER IN A USER-CREATED SUB-GROUP OF, SAY, "Indirect Expenses" IS ACCEPTED
 * BY THE SERVER AND MAY BE REFUSED HERE. That divergence is real and is recorded in
 * `corpora/mock-vs-cored/masters.json` rather than papered over: a screen that looks
 * right against this mock is not evidence the setter accepts a given ledger.
 */
const PL_PRIMARY_GROUPS = new Set([
  "sales accounts",
  "purchase accounts",
  "direct incomes",
  "indirect incomes",
  "direct expenses",
  "indirect expenses",
]);

function forexLedgerIsSuitable(
  _c: MockCompany,
  led: { group: string; bankParentGroup?: string },
): boolean {
  const names = [led.group, led.bankParentGroup ?? ""];
  return names.some((n) => PL_PRIMARY_GROUPS.has(normalisedName(n)));
}

function normalisedName(v: string): string {
    return v.trim().toLowerCase();
  }

  /** The refusal sentence, mirroring masters.MasterNameTaken exactly. */
  function duplicateMasterMessage(
    kind: string,
    sent: string,
    stored: string,
  ): string {
    if (stored !== "" && stored !== sent) {
      return (
        `masters: this company already has a ${kind} named ${JSON.stringify(stored)}, ` +
        `which is the same name as ${JSON.stringify(sent)} apart from capitalisation ` +
        `or surrounding spaces — use the existing one`
      );
    }
    return `masters: this company already has a ${kind} named ${JSON.stringify(sent)}`;
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
      // ?? not ||: `false` is a real choice, not an absence — and cored takes
      // the column default (false) for an omitted key, so the two agree.
      c.allowNegativeStock = req.allow_negative_stock ?? false;
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
        allow_negative_stock: c.allowNegativeStock,
      };
    },

    async setCompanyFeatures(
      companyID: string,
      req: CompanyFeaturesRequest,
    ): Promise<CompanyFeaturesResponse> {
      // 404 on an unknown id, like getCompany and like the live route. `ensure`
      // would materialise a blank company and hand back a cheerful 200 for a
      // tenant that does not exist.
      const c = companies.get(companyID);
      if (!c) {
        throw new ApiError(404, "company not found", "company not found");
      }
      // An ABSENT field leaves the setting alone; it is not a full restatement.
      if (req.allow_negative_stock !== undefined) {
        c.allowNegativeStock = req.allow_negative_stock;
      }
      // Answer from the STORE, never from `req`. The same discipline the server
      // follows, and for the same reason: an echo cannot tell "applied" from
      // "accepted and discarded", which is exactly what D43 was.
      return { id: c.id, allow_negative_stock: c.allowNegativeStock };
    },

    async createGroup(
      _c,
      req: CreateGroupRequest,
    ): Promise<CreateGroupResponse> {
      // D84: mirrors masters.CreateGroup's required-name refusal.
      if ((req.name ?? "").trim() === "") {
        throw new ApiError(422, "masters: group name is required");
      }
      return { group_id: id("group") };
    },
    async listGroups(companyID): Promise<ListGroupsResponse> {
      // DERIVED FROM THE LEDGERS' OWN GROUP NAMES rather than from a second
      // fixture, so the mock cannot offer a group no ledger in it belongs to — a
      // picker entry that resolves to nothing is exactly the kind of fixture-only
      // state a screen test would pass on and a browser drive would fail on.
      //
      // ⚠️ THE NATURE IS INFERRED FROM THE NAME here and READ FROM THE COLUMN by
      // cored, which is a real difference and is stated rather than hidden: the
      // mock has no account_group table to read a nature out of. A screen must
      // therefore not treat the mock's nature as authoritative for anything but
      // rendering — the SIDE a budget line ends up on is derived server-side from
      // the real column, which is why the field is served at all.
      const seen = new Map<string, string>();
      for (const l of ensure(companyID).ledgers) {
        if (!seen.has(l.group)) seen.set(l.group, l.group);
      }
      const natureOf = (name: string): string => {
        const n = name.toLowerCase();
        if (n.includes("sales") || n.includes("income")) return "INCOME";
        if (n.includes("purchase") || n.includes("expense")) return "EXPENSE";
        if (n.includes("creditor") || n.includes("duties") || n.includes("capital"))
          return "LIABILITY";
        return "ASSET";
      };
      return {
        groups: Array.from(seen.keys())
          .sort()
          .map((name) => ({
            id: `group-${name.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`,
            name,
            parent_name: "",
            nature: natureOf(name),
            is_pl: natureOf(name) === "INCOME" || natureOf(name) === "EXPENSE",
          })),
      };
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
          pan: l.pan ?? "",
          // ⛔ `?? null`, NEVER `?? 0`. NULL is "no term agreed" and 0 is "due
          // immediately" — two different facts, and collapsing them here would
          // let a screen test pass against a mock that cannot represent the
          // distinction the ageing report depends on.
          credit_days: l.creditDays ?? null,
          address: l.address ?? "",
          city: l.city ?? "",
          pincode: l.pincode ?? "",
          is_system: l.isSystem ?? false,
          is_bank: isBankLedger(l),
          blocked: l.blocked ?? false,
          ...(l.blocked ? { blocked_reason: l.blockedReason ?? "" } : {}),
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
      // ⛔⛔ D84 — THESE REFUSALS MIRROR `masters.CreateLedger`, SENTENCE FOR
      // SENTENCE, AND THE SENTENCES ARE THE POINT. This method used to validate
      // NOTHING: 61 test files build their masters through this mock, and
      // App.tsx's dev-client switch hands it to a real operator, so every one of
      // them was measured against an engine that accepted bodies cored answers
      // 422 to. Pinned by `corpora/mock-vs-cored/masters.json` and its two
      // runners — the sentence, not just the status, because a status-code-only
      // gate is never an oracle.
      const c = ensure(companyID);
      if ((req.name ?? "").trim() === "") {
        throw new ApiError(422, "masters: ledger name is required");
      }
      if ((req.group_name ?? "").trim() === "") {
        throw new ApiError(422, "masters: ledger group is required");
      }
      // ⚠️ EMPTY DEFAULTS TO "D" BEFORE THE CHECK, exactly as masters.go:350-353
      // does. Refusing "" here would refuse the commonest body in the product.
      const openingDC = (req.opening_dc ?? "") === "" ? "D" : req.opening_dc;
      if (openingDC !== "D" && openingDC !== "C") {
        throw new ApiError(
          422,
          `masters: invalid opening dr/cr ${JSON.stringify(openingDC)}`,
        );
      }
      // ⛔ THE DUPLICATE RULE IS NORMALISED — case-insensitive and
      // whitespace-trimmed — mirroring migration 0075 (register row D90).
      //
      // ⚠️ THIS COMMENT USED TO SAY THE RULE WAS CASE-SENSITIVE "because that
      // is what the wire showed". It was, and the wire showing it was the
      // DEFECT: 'Acme Traders' and 'acme traders' were two parties, splitting
      // one supplier's outstandings across two accounts with nothing on screen
      // to tell them apart. An observation faithfully mirrored is still only as
      // good as the behaviour it observed.
      const taken = c.ledgers.find(
        (l) => normalisedName(l.name) === normalisedName(req.name),
      );
      if (taken) {
        throw new ApiError(
          422,
          duplicateMasterMessage("ledger", req.name, taken.name),
        );
      }
      const ledgerID = id("ledger");
      c.ledgers.push({
        id: ledgerID,
        name: req.name,
        group: req.group_name,
        stateCode: req.state_code,
        gstin: req.gstin,
        registrationType: req.registration_type,
        pan: req.pan,
        creditDays: req.credit_days,
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
    async createUnit(companyID, req: CreateUnitRequest): Promise<CreateUnitResponse> {
      // D84: mirrors masters.CreateUnit's required-symbol refusal.
      const symbol = (req.symbol ?? "").trim();
      if (symbol === "") {
        throw new ApiError(422, "masters: unit symbol is required");
      }
      const c = ensure(companyID);
      const dup = c.units.find(
        (u) => normalisedName(u.symbol) === normalisedName(symbol),
      );
      if (dup) {
        throw new ApiError(422, duplicateMasterMessage("unit", symbol, dup.symbol));
      }
      // ---- T10: A COMPOUND UNIT, and the four refusals the server makes.
      //
      // ⛔ IT STORES THE ROW NOW, where it used to mint an id and throw the
      // master away. That was survivable while a unit was a bare symbol; a
      // compound unit carries the CONVERSION FACTOR the Stock Summary restates
      // quantities with, so a discarded unit means every base quantity in a
      // screen test reads empty and the test passes anyway.
      const base = (req.base_unit_symbol ?? "").trim();
      const factor = (req.conversion_factor ?? "").trim();
      if ((base === "") !== (factor === "")) {
        throw new ApiError(
          422,
          `masters: unit ${JSON.stringify(symbol)} states only half a conversion — a base unit ` +
            `with no factor cannot be converted and a factor with no base converts to nothing`,
        );
      }
      let baseRow: UnitListItem | undefined;
      if (base !== "") {
        if (!/^-?\d+(\.\d+)?$/.test(factor) || Number(factor) <= 0) {
          throw new ApiError(
            422,
            `masters: unit ${JSON.stringify(symbol)} needs a conversion factor greater than zero`,
          );
        }
        baseRow = c.units.find(
          (u) => normalisedName(u.symbol) === normalisedName(base),
        );
        if (!baseRow) {
          throw new ApiError(404, `masters: not found: base unit "${base}"`);
        }
        // A CHAIN is refused rather than half-resolved: this build converts one
        // level, and resolving the first of two factors is wrong by the second.
        if (baseRow.base_unit_symbol !== "") {
          throw new ApiError(
            422,
            `masters: unit ${JSON.stringify(symbol)} would be measured in ${JSON.stringify(base)}, ` +
              `which is itself compound; this build converts ONE level only`,
          );
        }
      }
      const unitID = id("unit");
      c.units.push({
        id: unitID,
        symbol,
        formal_name: (req.formal_name ?? "").trim(),
        decimal_places: req.decimal_places ?? 0,
        is_system: false,
        base_unit_symbol: baseRow ? baseRow.symbol : "",
        conversion_factor: base === "" ? "" : factor,
      });
      return { unit_id: unitID };
    },
    async createStockItem(
      companyID,
      req: CreateStockItemRequest,
    ): Promise<CreateStockItemResponse> {
      // STORE it. This used to mint an id and throw the master away, which made
      // listStockItems impossible and would have let an item-grid test pass
      // against an item nobody created.
      //
      // ⛔ IT NO LONGER CREATE-OR-SELECTS, AND THE OLD COMMENT NAMED A FUNCTION
      // THAT DOES NOT EXIST. It said "create-or-select by NAME mirrors
      // masters.EnsureStockItem" — there is no `masters.EnsureStockItem`, and
      // cored has REFUSED a duplicate stock item name since migration 0005.
      // The mock was the loose one, which is precisely the class of divergence
      // D84 exists to stop; it survived because the corpus recorded it as a
      // DELIBERATE divergence on the strength of a measurement (D84's) that was
      // itself wrong. Measured again 2026-08-22: cored answers 422.
      const c = ensure(companyID);
      // D84: mirrors masters.CreateStockItem's required-name refusal…
      if ((req.name ?? "").trim() === "") {
        throw new ApiError(422, "masters: stock item name is required");
      }
      // …and the HTTP layer's decimal bind, which answers 400 (NOT 422) because
      // it fails before `masters` is reached. Measured on the wire; the two
      // status codes on one call are why the corpus records a status per row
      // instead of a single "refused" class.
      if (
        req.gst_rate_pct !== undefined &&
        req.gst_rate_pct !== "" &&
        !/^-?\d+(\.\d+)?$/.test(String(req.gst_rate_pct).trim())
      ) {
        throw new ApiError(400, "invalid gst_rate_pct");
      }
      // ⛔ THE DUPLICATE CHECK COMES LAST, mirroring the wire: cored's own
      // required-name and decimal-bind refusals fire BEFORE the INSERT that
      // trips the unique index, so a body that is both blank and duplicated
      // must report the blank name — not the collision.
      const dup = c.stockItems.find(
        (s) => normalisedName(s.name) === normalisedName(req.name ?? ""),
      );
      if (dup) {
        throw new ApiError(
          422,
          duplicateMasterMessage("stock item", req.name ?? "", dup.name),
        );
      }

      // T10: the STOCK GROUP must already exist, exactly as on the wire — a
      // group minted from a misspelling subtotals half a company's stock under a
      // name nobody meant.
      const groupName = (req.stock_group_name ?? "").trim();
      if (groupName !== "") {
        const g = c.stockGroups.find(
          (x) => normalisedName(x.name) === normalisedName(groupName),
        );
        if (!g) {
          throw new ApiError(404, `masters: not found: stock group "${groupName}"`);
        }
        g.item_count += 1;
      }
      const stockItemID = id("stock");
      c.stockItems.push({
        id: stockItemID,
        name: req.name,
        unit_symbol: req.unit_symbol,
        stock_group: groupName,
        hsn: req.hsn ?? "",
        gst_rate_pct: req.gst_rate_pct ?? "0.0000",
        // T9: the method ASKED FOR, defaulting to weighted average when the
        // body names none. ⛔ It was hardcoded to WEIGHTED_AVG here, which is
        // exactly the defect this mock exists to catch on the real wire (a body
        // key accepted and discarded, register row D43) — a screen test would
        // have passed against a mock that could not express the feature.
        valuation_method: (req.valuation_method ?? "").trim() || "WEIGHTED_AVG",
        is_system: false,
      });
      return { stock_item_id: stockItemID };
    },
    async listUnits(companyID): Promise<ListUnitsResponse> {
      return {
        units: [...ensure(companyID).units].sort((a, b) =>
          a.symbol.localeCompare(b.symbol),
        ),
      };
    },
    async listCurrencies(companyID): Promise<ListCurrenciesResponse> {
      return {
        currencies: [...ensure(companyID).currencies].sort((a, b) =>
          a.code.localeCompare(b.code),
        ),
      };
    },
    async createCurrency(
      companyID,
      req: CreateCurrencyRequest,
    ): Promise<CreateCurrencyResponse> {
      const c = ensure(companyID);
      const code = (req.code ?? "").trim();
      if (code === "") {
        throw new ApiError(422, "masters: currency code is required");
      }
      // D84: the mock refuses what the server refuses, IN THE SERVER'S ORDER.
      // ⛔ THE BASE-CURRENCY REFUSAL IS FIRST because the server's trigger fires
      // before the unique index can: on an INR company, "INR" is refused as the
      // base currency and never as a duplicate.
      if (
        normalisedName(code) === normalisedName(c.baseCurrency) &&
        c.baseCurrency !== ""
      ) {
        throw new ApiError(
          422,
          `${code}: that is this company's base currency, and the books are already kept in it. ` +
            `Add a currency only for money you receive or pay in something else`,
        );
      }
      const dup = c.currencies.find(
        (x) => normalisedName(x.code) === normalisedName(code),
      );
      if (dup) {
        throw new ApiError(422, duplicateMasterMessage("currency", code, dup.code));
      }
      const dp = req.decimal_places;
      if (!Number.isInteger(dp) || dp < 0 || dp > 4) {
        throw new ApiError(
          422,
          `masters: ${String(dp)} decimal places: money is carried to 4 decimals in this product, so a currency may have between 0 and 4`,
        );
      }
      const id = `cur-${String(c.currencies.length + 1)}`;
      c.currencies.push({
        id,
        code,
        symbol: req.symbol,
        formal_name: req.formal_name,
        decimal_places: dp,
        // ⛔ EMPTY, NOT "0". A currency starts with NO rate on file, and empty is
        // the state the client must render as "no rate yet".
        latest_rate: "",
        latest_rate_date: "",
      });
      return { currency_id: id };
    },
    async forexSettings(companyID): Promise<ForexSettingsResponse> {
      const c = ensure(companyID);
      const id = c.forexGainLossLedgerID ?? "";
      if (id === "") {
        // ⛔ A 200 WITH AN EMPTY FIELD, NEVER A 404. "No forex ledger chosen" is the
        // ordinary state of every company that never trades in a foreign currency,
        // so reporting it the way a mistyped URL is reported would make the F11
        // screen render an error for the common case.
        return {};
      }
      const led = c.ledgers.find((l) => l.id === id);
      return {
        gain_loss_ledger_id: id,
        gain_loss_ledger_name: led ? led.name : "",
      };
    },
    async setForexSettings(companyID, req: SetForexSettingsRequest) {
      const c = ensure(companyID);
      const id = (req.gain_loss_ledger_id ?? "").trim();
      if (id === "") {
        // CLEARS the choice. Not a delete: the setting goes back to "not chosen",
        // and every difference-realising settlement starts refusing again.
        c.forexGainLossLedgerID = "";
        return;
      }
      // D84: the mock refuses what the server refuses, IN THE SERVER'S ORDER —
      // existence first (the server's SELECT finds no row), then suitability.
      const led = c.ledgers.find((l) => l.id === id);
      if (!led) {
        throw new ApiError(422, `masters: no such ledger: ${id}`);
      }
      // ⛔ THE SUITABILITY REFUSAL IS THE POINT OF THIS SETTER, and the mock has to
      // carry it or a screen built against the mock would offer every ledger in the
      // chart of accounts and only discover the refusal in production. A realised
      // gain credited to a Sundry Debtors ledger lands on a customer's own balance
      // and makes a fully settled bill show a permanent residue.
      if (!forexLedgerIsSuitable(c, led)) {
        throw new ApiError(
          422,
          "that ledger cannot hold a realised exchange difference. Choose a revenue " +
            "or expense ledger — a gain is credited to it and a loss debited, so a " +
            "party, bank or cash ledger would move a balance that belongs to " +
            "somebody else",
        );
      }
      c.forexGainLossLedgerID = id;
    },
    async setCurrencyRate(companyID, req: SetCurrencyRateRequest) {
      const c = ensure(companyID);
      const cur = c.currencies.find(
        (x) => normalisedName(x.code) === normalisedName(req.code),
      );
      if (!cur) {
        throw new ApiError(404, `masters: not found: currency "${req.code}"`);
      }
      if (!(Number(req.rate) > 0)) {
        throw new ApiError(
          422,
          `masters: rate ${req.rate}: an exchange rate must be greater than zero`,
        );
      }
      // Replaces the rate for that date, exactly as the server's upsert does.
      if (req.effective_date >= cur.latest_rate_date) {
        cur.latest_rate = req.rate;
        cur.latest_rate_date = req.effective_date;
      }
      // ⭐ AND THE HISTORY, which is what the unadjusted-forex report reads. ONE
      // ROW PER (currency, day), replaced rather than appended — the server has a
      // unique index on exactly that pair, and its comment says why: a second row
      // for the same date is not extra information, it is an ambiguity.
      const prior = c.currencyRates.findIndex(
        (x) => x.currencyID === cur.id && x.on === req.effective_date,
      );
      if (prior >= 0) {
        c.currencyRates[prior] = {
          currencyID: cur.id,
          on: req.effective_date,
          rate: req.rate,
        };
      } else {
        c.currencyRates.push({
          currencyID: cur.id,
          on: req.effective_date,
          rate: req.rate,
        });
      }
    },
    async listStockGroups(companyID): Promise<ListStockGroupsResponse> {
      // ⚠️ TREE ORDER (parents before children), which the server produces from
      // its ltree path. The mock keeps insertion order and only sorts by depth,
      // because a parent must exist before a child can name it — so insertion
      // order is already topological.
      return {
        stock_groups: [...ensure(companyID).stockGroups].sort(
          (a, b) => a.depth - b.depth,
        ),
      };
    },
    async createStockGroup(
      companyID,
      req: CreateStockGroupRequest,
    ): Promise<CreateStockGroupResponse> {
      const c = ensure(companyID);
      const name = (req.name ?? "").trim();
      if (name === "") {
        throw new ApiError(422, "masters: stock group name is required");
      }
      // D84: the mock refuses what the server refuses, in the server's order.
      const dup = c.stockGroups.find(
        (g) => normalisedName(g.name) === normalisedName(name),
      );
      if (dup) {
        throw new ApiError(
          422,
          duplicateMasterMessage("stock group", name, dup.name),
        );
      }
      const parent = (req.parent_name ?? "").trim();
      let depth = 0;
      if (parent !== "") {
        const p = c.stockGroups.find(
          (g) => normalisedName(g.name) === normalisedName(parent),
        );
        if (!p) {
          throw new ApiError(404, `masters: not found: parent stock group "${parent}"`);
        }
        depth = p.depth + 1;
      }
      const groupID = id("stock-group");
      c.stockGroups.push({
        id: groupID,
        name,
        parent_name: parent === "" ? "" : parent,
        depth,
        item_count: 0,
      });
      return { stock_group_id: groupID };
    },
    async inventoryVocabulary(
      _companyID,
    ): Promise<InventoryVocabularyResponse> {
      // The three methods cored implements, with the server's own labels. The
      // mock mirrors the WIRE and not a convenience: a screen that renders a
      // picker from this in a test must be rendering the same set the browser
      // gets, or the test proves nothing about the picker.
      return {
        valuation_methods: [
          {
            method: "WEIGHTED_AVG",
            label: "Avg. Cost (default)",
            basis:
              "One blended rate for the whole position: every receipt re-prices " +
              "the stock already on hand, so a late purchase changes what earlier issues cost.",
          },
          {
            method: "FIFO",
            label: "FIFO",
            basis:
              "The oldest receipt is issued first, so closing stock is carried at " +
              "what the MOST RECENT purchases cost — higher than average in a rising market.",
          },
          {
            method: "LIFO",
            label: "LIFO (Perpetual)",
            basis:
              "The newest receipt at the time of each issue is consumed first, so closing " +
              "stock is carried at what the OLDEST purchases cost. Not LIFO Annual, which " +
              "cannot be stated until the year closes.",
          },
        ],
        default: "WEIGHTED_AVG",
      };
    },
    async setStockItemValuationMethod(
      companyID,
      stockItemID,
      req: SetValuationMethodRequest,
    ): Promise<SetValuationMethodResponse> {
      const c = ensure(companyID);
      const item = c.stockItems.find((s) => s.id === stockItemID);
      if (!item) {
        throw new ApiError(
          404,
          `masters: not found: stock item ${stockItemID}`,
        );
      }
      const method = (req.valuation_method ?? "").trim();
      // Mirrors the wire's two refusals, in the server's order: a method no
      // valuer implements is 422 at the MASTER (never a failing report later),
      // and a no-op is 409 so the audit trail of restatements holds only real
      // ones. D84's rule — the mock is not allowed to be the loose one.
      if (!["WEIGHTED_AVG", "FIFO", "LIFO"].includes(method)) {
        throw new ApiError(
          422,
          `masters: valuation method ${JSON.stringify(method)} is not one this build can value stock with (WEIGHTED_AVG, FIFO, LIFO)`,
        );
      }
      if (item.valuation_method === method) {
        throw new ApiError(
          409,
          `masters: the stock item already uses that valuation method: ${item.name} is already ${method}`,
        );
      }
      item.valuation_method = method;
      return {
        stock_item_id: item.id,
        name: item.name,
        valuation_method: method,
        restates:
          "Stock value is derived at report time, so this item's WHOLE history is " +
          "now valued on the new basis — including periods that have already been reported. " +
          "Switching back reproduces the earlier figures exactly.",
      };
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
            // Served on EVERY row, as the server serves it — including on a
            // deduction, so a client cannot pass by only ever seeing it absent.
            kind: d.kind ?? ("TDS" as TaxAllocationKind),
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
            "allocation cannot be filed at all. (An allocate-later route DOES exist now — see " +
            "allocateToTDSChallan — but it can only ADD to a challan that is already correct in " +
            "every other respect, so a tax deposit is still refused here if it names nothing)",
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
        /** The kind AS SENT — `undefined` means the caller named none. */
        sentKind?: TaxAllocationKind;
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
        // ⛔ THE KIND AS SENT — never defaulted here. The DEFAULT (absent means
        // TDS) is applied at the comparison, so a request that named NO kind can
        // be told apart from one that named the WRONG kind and the refusal can
        // say which mistake was made.
        parsed.push({
          voucherID,
          section,
          legSeq: a.leg_seq,
          rawAmount,
          amountUnits,
          triple,
          sentKind: a.kind,
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
      /** Which kinds this challan ends up paying. See the refusal below. */
      const kindsOnThisChallan = new Set<TaxAllocationKind>();
      for (const {
        voucherID,
        section,
        legSeq,
        rawAmount,
        amountUnits,
        triple,
        sentKind,
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
        // ⛔ THE KIND DECIDES WHICH TABLE THE TRIPLE IS LOOKED UP IN, and an ABSENT
        // kind means TDS — so a collection sent without one finds no deduction on
        // the server and comes back as "nothing was deducted", which reads like
        // the books are wrong rather than like the request is. That is register
        // row D4's client half, and it shipped green past 40 screen tests because
        // every fixture in this file was a deduction.
        const wantKind: TaxAllocationKind = deduction.kind ?? "TDS";
        if ((sentKind ?? "TDS") !== wantKind) {
          throw new ApiError(
            400,
            `masters: nothing was ${wantKind === "TCS" ? "deducted" : "collected"} on voucher ${voucherID} under ` +
              `${section} at leg ${legSeq}. That triple is a ${wantKind === "TCS" ? "COLLECTION" : "DEDUCTION"}, and this ` +
              `allocation ${sentKind ? `says it is a ${sentKind}` : "names no kind at all, which means TDS"}. The two ` +
              "section vocabularies are disjoint, so send back the `kind` the picker served",
          );
        }
        // ⛔⛔ ONE CHALLAN CANNOT PAY BOTH — the return files a deduction and a
        // collection as different statements, so a challan naming both is
        // UNFILABLE. The server refuses it by name (ErrMixedChallanKinds); this
        // mock ACCEPTED it, which is exactly the class of divergence register row
        // D84 is about — the looser side is the one every screen test measures.
        if (kindsOnThisChallan.size > 0 && !kindsOnThisChallan.has(wantKind)) {
          throw new ApiError(
            400,
            "masters: one challan cannot pay both TDS deductions and TCS collections. " +
              `This deposit already pays ${[...kindsOnThisChallan].join(" and ")} and voucher ${voucherID} under ` +
              `${section} is a ${wantKind}. The return reports them as different statements, so a ` +
              "challan naming both cannot be filed — record the collection's deposit on its own challan",
          );
        }
        kindsOnThisChallan.add(wantKind);
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

    // ---- ATTACH FURTHER DEDUCTIONS TO A RECORDED CHALLAN (D1).
    //
    // ⛔ THIS MOCK MUST NOT BE LOOSER THAN CORED. D84 records that `api/mock.ts`
    // being looser than the server is a SOURCE OF FALSE GREEN and that three
    // lanes hit it, so every refusal `AllocateToRecordedChallan` makes is made
    // here, in the SERVER'S ORDER — the order is the feature, exactly as it is on
    // the create path: a request that trips two rules must name the same one the
    // real stack names, or a screen test certifies a sentence no operator will
    // ever see.
    //
    // WHAT IT CANNOT MODEL, stated rather than approximated: the server takes
    // `FOR UPDATE` on the challan so two concurrent attaches cannot each pass an
    // over-allocation cap their sum breaks. There is no concurrency here, so that
    // guard is untestable against this mock and must be driven on the wire.
    async allocateToTDSChallan(
      companyID,
      challanID: string,
      req: AllocateToChallanRequest,
    ): Promise<AllocateToChallanResponse> {
      const c = ensure(companyID);
      const invalid = (why: string) =>
        new ApiError(400, `masters: invalid TDS challan: ${why}`);

      if ((challanID ?? "").trim() === "") {
        throw invalid("this allocation names no challan");
      }
      const allocations = req.allocations ?? [];
      if (allocations.length === 0) {
        // A REFUSAL, NOT A NO-OP 200. An operator who ticked nothing and pressed
        // accept must not be told the deposit now covers deductions it does not —
        // and the register would not have changed to contradict it.
        throw invalid(
          "this request names no deduction to allocate. A deposit already recorded is only " +
            "changed by saying WHAT it paid for",
        );
      }
      if ((req.recorded_by ?? "").trim() === "") {
        throw invalid("recorded_by is required");
      }

      // ---- PASS 1, the request alone. Same shape checks the create makes, and
      // the same reasons: 0 is a real leg, the amount is a string, a triple may
      // not repeat.
      const seen = new Set<string>();
      const parsed: {
        voucherID: string;
        section: string;
        legSeq: number;
        rawAmount: Money;
        amountUnits: bigint;
        triple: string;
        /** The kind AS SENT — `undefined` means the caller named none. */
        sentKind?: TaxAllocationKind;
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
            `allocation for voucher ${voucherID} names no section. The deduction key is ` +
              "(voucher, section, leg) — one voucher can carry deductions under more than one section",
          );
        }
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
            `allocation for voucher ${voucherID} under ${section} is ${mockPlainAmount(rawAmount)}. An allocation is a magnitude`,
          );
        }
        const triple = allocationTriple(voucherID, section, a.leg_seq);
        if (seen.has(triple)) {
          throw invalid(
            `voucher ${voucherID} under ${section} leg ${a.leg_seq} appears twice on this request. ` +
              "One challan pays a deduction ONCE",
          );
        }
        seen.add(triple);
        // ⛔ THE KIND AS SENT — never defaulted here. The DEFAULT (absent means
        // TDS) is applied at the comparison, so a request that named NO kind can
        // be told apart from one that named the WRONG kind and the refusal can
        // say which mistake was made.
        parsed.push({
          voucherID,
          section,
          legSeq: a.leg_seq,
          rawAmount,
          amountUnits,
          triple,
          sentKind: a.kind,
        });
      }

      // ---- THE CHALLAN. 404 and not 400: the request is well formed and names a
      // challan this company does not have. ⚠️ RLS makes ANOTHER TENANT'S challan
      // look exactly like a missing one on the real server, and that is the
      // intended answer — the alternative discloses whether a CIN exists
      // elsewhere. This mock has no tenants to confuse, so it simply must not
      // answer anything friendlier.
      let cin = "";
      let challan: TDSChallan | undefined;
      for (const [key, ch] of c.tdsChallans) {
        if (ch.id === challanID) {
          cin = key;
          challan = ch;
          break;
        }
      }
      if (!challan) {
        throw new ApiError(
          404,
          `masters: not found: no challan ${challanID} is recorded for this company, so there is ` +
            "no deposit to allocate against",
        );
      }
      const taxUnits = q4(challan.tax_rupees);
      if (taxUnits <= 0n) {
        throw invalid(
          `challan ${challan.challan_no} of ${challan.deposit_date} at BSR branch ${challan.bsr_code} deposited NO TAX ` +
            "(only interest, fee or penalty), so there is nothing on it to allocate. Only the tax " +
            "component discharges a deduction liability",
        );
      }

      // ---- PASS 2, each allocation against the deduction it names.
      const added: TDSChallanAllocation[] = [];
      let addedUnits = 0n;
      // ⛔ SEEDED FROM WHAT THE CHALLAN ALREADY PAYS, not from this request. The
      // mixed pair is assembled ACROSS two requests — a deposit recorded against
      // a collection, then a deduction attached later — and checking only within
      // the request would let exactly that through, which is the whole reason
      // the allocate-later route needed its own cap for D2 as well.
      const kindsOnThisChallan = new Set<TaxAllocationKind>(
        (challan.allocations ?? []).map(
          (a) =>
            c.tdsDeductions.get(
              allocationTriple(a.voucher_id, a.section, a.leg_seq),
            )?.kind ?? "TDS",
        ),
      );
      for (const {
        voucherID,
        section,
        legSeq,
        rawAmount,
        amountUnits,
        triple,
        sentKind,
      } of parsed) {
        const deduction = c.tdsDeductions.get(triple);
        if (!deduction) {
          throw new ApiError(
            400,
            `masters: nothing was deducted on voucher ${voucherID} under ${section} at leg ${legSeq}, so there is ` +
              "nothing on it to deposit",
          );
        }
        // ⛔ THE KIND DECIDES WHICH TABLE THE TRIPLE IS LOOKED UP IN, and an ABSENT
        // kind means TDS — so a collection sent without one finds no deduction on
        // the server and comes back as "nothing was deducted", which reads like
        // the books are wrong rather than like the request is. That is register
        // row D4's client half, and it shipped green past 40 screen tests because
        // every fixture in this file was a deduction.
        const wantKind: TaxAllocationKind = deduction.kind ?? "TDS";
        if ((sentKind ?? "TDS") !== wantKind) {
          throw new ApiError(
            400,
            `masters: nothing was ${wantKind === "TCS" ? "deducted" : "collected"} on voucher ${voucherID} under ` +
              `${section} at leg ${legSeq}. That triple is a ${wantKind === "TCS" ? "COLLECTION" : "DEDUCTION"}, and this ` +
              `allocation ${sentKind ? `says it is a ${sentKind}` : "names no kind at all, which means TDS"}. The two ` +
              "section vocabularies are disjoint, so send back the `kind` the picker served",
          );
        }
        // ⛔⛔ ONE CHALLAN CANNOT PAY BOTH — the return files a deduction and a
        // collection as different statements, so a challan naming both is
        // UNFILABLE. The server refuses it by name (ErrMixedChallanKinds); this
        // mock ACCEPTED it, which is exactly the class of divergence register row
        // D84 is about — the looser side is the one every screen test measures.
        if (kindsOnThisChallan.size > 0 && !kindsOnThisChallan.has(wantKind)) {
          throw new ApiError(
            400,
            "masters: one challan cannot pay both TDS deductions and TCS collections. " +
              `This deposit already pays ${[...kindsOnThisChallan].join(" and ")} and voucher ${voucherID} under ` +
              `${section} is a ${wantKind}. The return reports them as different statements, so a ` +
              "challan naming both cannot be filed — record the collection's deposit on its own challan",
          );
        }
        kindsOnThisChallan.add(wantKind);
        const deductionUnits = q4(deduction.tax_rupees);
        if (deductionUnits === 0n) {
          throw new ApiError(
            400,
            `masters: voucher ${deduction.voucher_no} paid ${deduction.deductee_name} under ${section} and deducted NO tax (the payment is ` +
              "below the threshold), so no deposit can be allocated to it",
          );
        }
        if (amountUnits !== deductionUnits) {
          throw new ApiError(
            400,
            `masters: voucher ${deduction.voucher_no} deducted ${mockPlainAmount(deduction.tax_rupees)} under ${section} from ` +
              `${deduction.deductee_name}, but ${mockPlainAmount(rawAmount)} is being allocated to it. The two must be EQUAL`,
          );
        }
        if (deduction.tax_year_start !== challan.tax_year_start) {
          throw new ApiError(
            400,
            `masters: the deduction on voucher ${deduction.voucher_no} under ${section} belongs to tax year ${deduction.tax_year_start}, and ` +
              `this challan is recorded against ${challan.tax_year_start}. A challan belongs to ONE tax year`,
          );
        }
        const paidBy = c.tdsAllocations.get(triple);
        if (paidBy !== undefined) {
          const other = c.tdsChallans.get(paidBy);
          throw new ApiError(
            409,
            `masters: the ${mockPlainAmount(deduction.tax_rupees)} deducted on voucher ${deduction.voucher_no} under ${section} from ${deduction.deductee_name} was already ` +
              `deposited on challan ${other?.challan_no ?? ""} of ${other?.deposit_date ?? ""} at BSR branch ${other?.bsr_code ?? ""}. Deductee Detail field 5 is a SINGLE ` +
              "challan reference, so a deduction cannot be paid by two challans",
          );
        }
        addedUnits += amountUnits;
        added.push({
          voucher_id: voucherID,
          voucher_no: deduction.voucher_no,
          section,
          leg_seq: legSeq,
          deductee_name: deduction.deductee_name,
          allocated_rupees: q4str(amountUnits),
          deduction_on_record: true,
          deduction_tax_rupees: q4str(deductionUnits),
          broken_by_alter: false,
        });
      }

      // ---- THE OVER-ALLOCATION CAP (D2), which this route needs as much as the
      // create does: attaching one deduction at a time must not be a way past a
      // rule the create enforces in one shot. The tolerance is ONE RUPEE PER
      // ALLOCATION, not an equality — a deposit is whole rupees and a deduction is
      // a 4-dp figure, so a few paise either way is the ordinary case.
      const storedUnits = q4(challan.allocated_total_rupees);
      const totalAfter = storedUnits + addedUnits;
      const count = BigInt(challan.allocation_count + added.length);
      if (totalAfter - taxUnits >= count * QSCALE) {
        throw new ApiError(
          409,
          `masters: this challan's allocations claim more tax than it deposited: challan ` +
            `${challan.challan_no} of ${challan.deposit_date} at BSR branch ${challan.bsr_code} deposited ${mockPlainAmount(challan.tax_rupees)} of tax, and after ` +
            `this request its allocations would claim ${q4str(totalAfter)} across ${count} deduction(s)`,
        );
      }

      // ---- COMMIT. The stored challan is REPLACED by a new object rather than
      // mutated: `listTDSChallans` hands out the stored rows themselves, so
      // mutating in place would rewrite a response a caller may still be holding.
      const merged = [...challan.allocations, ...added].sort(
        (x, y) =>
          x.voucher_no.localeCompare(y.voucher_no) ||
          x.section.localeCompare(y.section) ||
          x.leg_seq - y.leg_seq,
      );
      const updated: TDSChallan = {
        ...challan,
        allocation_count: merged.length,
        allocated_total_rupees: q4str(totalAfter),
        // ⛔ THE ORACLE OF THIS WHOLE ROUTE. It must FALL.
        unallocated_tax_rupees: q4str(taxUnits - totalAfter),
        allocations: merged,
      };
      c.tdsChallans.set(cin, updated);
      for (const v of added) {
        c.tdsAllocations.set(
          allocationTriple(v.voucher_id, v.section, v.leg_seq),
          cin,
        );
      }
      return {
        id: updated.id,
        tax_year_start: updated.tax_year_start,
        tax_year_label: updated.tax_year_label,
        // The mock's read-back cannot fail, so this is never null.
        challan: updated,
      };
    },

    // ---- THE STATUTORY SECTION VOCABULARY (D3).
    //
    // ⛔ THE FIGURES BELOW ARE A HAND COPY OF internal/compliance's ruleset, and
    // that is exactly the drift this route exists to prevent everywhere ELSE.
    // It is tolerable HERE and nowhere else because a mock has no server to ask,
    // and it is bounded: these two tables are the only copy in this codebase
    // outside Go, and a test compares them against what cored's own catalog
    // serves. ⚠️ IF A SECTION IS ADDED TO THE RULESET AND NOT HERE, that test
    // fails — the fix is to add it here, never to relax the test.
    async taxSections(companyID): Promise<TaxSectionsResponse> {
      ensure(companyID);
      return {
        tds_ruleset_version: "tds-2026-04",
        tcs_ruleset_version: "tcs-2026-04",
        tds_sections: MOCK_TDS_SECTIONS.map((r) => ({ ...r })),
        tcs_sections: MOCK_TCS_SECTIONS.map((r) => ({ ...r })),
        tds_no_pan_floor_pct: "20",
        // ⛔ NOT THE SAME SHAPE as the line above — see TaxSectionsResponse.
        tcs_no_pan_multiplier: "2",
        tcs_no_pan_floor_pct: "5",
        tcs_no_pan_ceiling_pct: "20",
        threshold_basis_is_approximated: true,
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
        throw new ApiError(
          400,
          "masters: invalid cost master: a cost category needs a name",
        );
      }
      // ⛔ THE MOCK MUST REFUSE WHAT THE SERVER REFUSES, and this check exists
      // because it did not. The screen originally sent no created_by; the server
      // answered 400, the mock answered 201, and all 597 frontend tests passed
      // against a create path that could not work in the browser — the failure
      // surfaced only as a console 400 AFTER a successful WebAuthn step-up, which
      // reads as "the passkey failed" and is not. A mock more permissive than the
      // server does not model the server; it hides it.
      if ((req.created_by ?? "").trim() === "") {
        throw new ApiError(
          400,
          "masters: invalid cost master: created_by is required",
        );
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
        throw new ApiError(
          404,
          "masters: there is no cost category with that id for this company",
        );
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
        throw new ApiError(
          400,
          "masters: invalid cost master: a cost centre needs a name",
        );
      }
      // See createCostCategory: the mock refuses what the server refuses.
      if ((req.created_by ?? "").trim() === "") {
        throw new ApiError(
          400,
          "masters: invalid cost master: created_by is required",
        );
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
        throw new ApiError(
          404,
          "masters: there is no cost centre with that id to sit under",
        );
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
        throw new ApiError(
          404,
          "masters: there is no cost centre with that id for this company",
        );
      }
      if (req.parent_id === "00000000-0000-0000-0000-000000000000") {
        throw new ApiError(
          400,
          "parent_id is all zeros, which is not a cost centre: send the field EMPTY to put this centre at the top of its axis — the all-zeros uuid would silently move it to the top and change which totals it rolls into",
        );
      }
      return {
        id: existing.id,
        cost_centre: {
          ...existing,
          name: (req.name ?? "").trim() || existing.name,
        },
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
        throw new ApiError(
          422,
          "there is no cost category with that id for this company",
        );
      }
      const centres = mockCostCentres().filter(
        (c) => c.cost_category_id === costCategoryID,
      );
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

    // ---- T11: budgets and budget vs actual (migrations 0085/0086).
    async listBudgets(companyID): Promise<BudgetListResponse> {
      ensure(companyID);
      return { budgets: mockBudgets() };
    },
    async createBudget(
      companyID,
      req: CreateBudgetRequest,
    ): Promise<CreateBudgetResponse> {
      ensure(companyID);
      const name = (req.name ?? "").trim();
      if (name === "") {
        throw new ApiError(
          400,
          "a budget needs a name — a plan nobody can name is a plan nobody can be asked about",
        );
      }
      if (!req.from_date || !req.to_date) {
        throw new ApiError(
          400,
          "a budget needs both a from_date and a to_date (YYYY-MM-DD). A plan for no stated period cannot be compared against anything: the closing-balance basis would have no date to be measured as at",
        );
      }
      if (req.to_date < req.from_date) {
        throw new ApiError(
          400,
          "the budget period ends before it begins. Reversed bounds would match no movement at all and report the entire budget as unspent, which reads as a business fact rather than as a typo",
        );
      }
      if (
        mockBudgets().some(
          (b) => b.name.trim().toLowerCase() === name.toLowerCase(),
        )
      ) {
        throw new ApiError(
          409,
          `a budget called "${name}" already exists for this company (names are compared ignoring case and surrounding spaces)`,
        );
      }
      return {
        id: `budget-${name.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`,
        name,
        from_date: req.from_date,
        to_date: req.to_date,
      };
    },
    async alterBudget(
      companyID,
      budgetID: string,
      req: CreateBudgetRequest,
    ): Promise<AlterBudgetResponse> {
      ensure(companyID);
      const existing = mockBudgets().find((b) => b.id === budgetID);
      if (!existing) {
        throw new ApiError(404, `no budget ${budgetID} in this company`);
      }
      if (req.to_date < req.from_date) {
        throw new ApiError(
          400,
          "the budget period ends before it begins",
        );
      }
      return {
        id: existing.id,
        budget: {
          ...existing,
          name: (req.name ?? "").trim() || existing.name,
          from_date: req.from_date || existing.from_date,
          to_date: req.to_date || existing.to_date,
        },
      };
    },
    async deleteBudget(
      companyID,
      budgetID: string,
      deletedBy: string,
    ): Promise<DeleteBudgetResponse> {
      ensure(companyID);
      const existing = mockBudgets().find((b) => b.id === budgetID);
      if (!existing) {
        throw new ApiError(404, `no budget ${budgetID} in this company`);
      }
      if (!deletedBy) {
        throw new ApiError(
          400,
          "deleted_by is required (?deleted_by=…) — a plan deleted by nobody in particular is a plan nobody can be asked about",
        );
      }
      // ⚠️ THE COUNT IS SERVED because deleting a plan cascades to its lines, and
      // "I deleted a budget" and "I deleted 3 budgeted figures" are different acts.
      return {
        id: existing.id,
        deleted: true,
        lines_deleted_by_cascade: existing.line_count,
      };
    },
    async listBudgetLines(
      companyID,
      budgetID: string,
    ): Promise<BudgetLineListResponse> {
      ensure(companyID);
      if (!mockBudgets().some((b) => b.id === budgetID)) {
        // ⛔ 404 AND NOT AN EMPTY LIST. An empty line list for a plan that does not
        // exist reads as "this plan has no figures", which is a different and more
        // reassuring claim than "there is no such plan".
        throw new ApiError(404, `no budget ${budgetID} in this company`);
      }
      return { budget_id: budgetID, lines: mockBudgetLines(budgetID) };
    },
    async addBudgetLine(
      companyID,
      budgetID: string,
      req: CreateBudgetLineRequest,
    ): Promise<CreateBudgetLineResponse> {
      ensure(companyID);
      if (!mockBudgets().some((b) => b.id === budgetID)) {
        throw new ApiError(404, `no budget ${budgetID} in this company`);
      }
      if (!req.target_id) {
        throw new ApiError(
          400,
          "target_id is required — the thing being budgeted. A line with no target could not be compared against anything",
        );
      }
      // ⛔ A COST CENTRE HOLDS NO POSITION, so it may not be budgeted on one.
      if (
        req.dimension === "COST_CENTRE" &&
        req.basis !== "NETT_TRANSACTIONS"
      ) {
        throw new ApiError(
          400,
          "a cost centre cannot be budgeted on a CLOSING_BALANCE. A cost centre is not an account: it holds no opening balance and carries no position, only allocations against dated postings. Use NETT_TRANSACTIONS",
        );
      }
      // ⛔ AND IT HAS NO NATURE, so it cannot take a side by default.
      if (req.dimension === "COST_CENTRE" && !req.dr_cr) {
        throw new ApiError(
          400,
          'a cost centre budget must state its side (dr_cr "D" or "C"). A group or a ledger has a NATURE to take the side from; a cost centre has none. A cost budget is "D"',
        );
      }
      if (!req.amount || Number(req.amount) <= 0) {
        throw new ApiError(
          400,
          "a budget amount must be greater than zero. A budget of zero is the ABSENCE of a budget, and the report already has a distinct answer for a target nobody budgeted (UNBUDGETED). To plan to spend nothing here, set no line at all",
        );
      }
      if (
        mockBudgetLines(budgetID).some((l) => l.target_id === req.target_id)
      ) {
        throw new ApiError(
          409,
          "this budget already has a line for that target. One target gets one figure per budget: two would make the variance report show it twice, with the actual counted in full against each. Alter the existing line instead",
        );
      }
      return { id: `budget-line-${req.target_id}`, budget_id: budgetID };
    },
    async alterBudgetLine(
      companyID,
      budgetID: string,
      lineID: string,
      req: CreateBudgetLineRequest,
    ): Promise<{ id: string; altered: boolean }> {
      ensure(companyID);
      const line = mockBudgetLines(budgetID).find((l) => l.id === lineID);
      if (!line) {
        throw new ApiError(404, `no budget line ${lineID} in this company`);
      }
      // ⛔ THE DIMENSION AND TARGET ARE IMMUTABLE. Re-filing a figure would leave
      // the original target reading as unbudgeted and the new one as having been
      // planned all along — a change of meaning dressed as a correction.
      if (req.dimension && req.dimension !== line.dimension) {
        throw new ApiError(
          400,
          `a budget line's dimension cannot be changed (it is ${line.dimension}). Delete this line and add the right one`,
        );
      }
      if (req.target_id && req.target_id !== line.target_id) {
        throw new ApiError(
          400,
          "a budget line's target cannot be changed. Delete this line and add one against the target you mean — that is two audited acts, which is what actually happened",
        );
      }
      if (line.dimension === "COST_CENTRE" && req.basis !== "NETT_TRANSACTIONS") {
        throw new ApiError(
          400,
          "a cost centre cannot be budgeted on a CLOSING_BALANCE — it holds no position, only allocations against dated postings",
        );
      }
      return { id: lineID, altered: true };
    },
    async deleteBudgetLine(
      companyID,
      budgetID: string,
      lineID: string,
      deletedBy: string,
    ): Promise<{ id: string; deleted: boolean }> {
      ensure(companyID);
      if (!mockBudgetLines(budgetID).some((l) => l.id === lineID)) {
        throw new ApiError(404, `no budget line ${lineID} in this company`);
      }
      if (!deletedBy) {
        throw new ApiError(400, "deleted_by is required (?deleted_by=…)");
      }
      return { id: lineID, deleted: true };
    },
    async gstr9(
      companyID,
      from: string,
      to: string,
      ledgers: GSTR9TaxLedgers,
      gstin?: string,
    ): Promise<GSTR9Envelope> {
      const c = ensure(companyID);

      // ⛔ THE TWO REFUSALS, IN THE ORDER cored CHECKS THEM. The window is a pure
      // check and runs first; the ledgers need no lookup but are checked second
      // because that is the order handleGSTR9 checks them in. A mock that refused
      // in a different order lets a screen's error handling pass here and fail
      // against the real server.
      const fy = financialYearOf(from, to);
      if (!fy) {
        throw new ApiError(
          400,
          "GSTR-9 covers an Indian financial year (1 April to 31 March), not " +
            `${from} to ${to}. For a shorter window read the monthly GSTR-3B ` +
            "instead — an annual return for a period the law does not recognise " +
            "is a statutory-looking document about nothing",
        );
      }
      const missing = (
        [
          ["cgst_ledger_id", ledgers.cgst],
          ["sgst_ledger_id", ledgers.sgst],
          ["igst_ledger_id", ledgers.igst],
          ["input_cgst_ledger_id", ledgers.inputCGST],
          ["input_sgst_ledger_id", ledgers.inputSGST],
          ["input_igst_ledger_id", ledgers.inputIGST],
        ] as [string, string][]
      )
        .filter(([, v]) => !v)
        .map(([k]) => k);
      if (missing.length > 0) {
        throw new ApiError(
          400,
          `GSTR-9 is a reconciliation TO the books and needs the tax ledger ids: ${missing.join(", ")}. ` +
            "Without them the books column is ZERO for every month and every period appears to " +
            "disagree with the returns by exactly the tax charged, which is a wrong number that " +
            "looks like a finding",
        );
      }

      // Resolve the six ids to LEDGER NAMES, because a mock posting records the
      // ledger by name and not by id.
      const nameOf = (id: string) =>
        c.ledgers.find((l) => l.id === id)?.name ?? "";
      const outNames = [ledgers.cgst, ledgers.sgst, ledgers.igst].map(nameOf);
      const inNames = [
        ledgers.inputCGST,
        ledgers.inputSGST,
        ledgers.inputIGST,
      ].map(nameOf);

      const months: GSTR9Month[] = [];
      for (let n = 0; n < 12; n++) {
        const w = monthWindowOf(fy, n);
        const books = (names: string[], cls: string): GSTTax => {
          const head = (name: string) => {
            let net = 0;
            for (const v of c.vouchers) {
              if (v.class.toUpperCase() !== cls) continue;
              if (v.date < w.from || v.date > w.to) continue;
              for (const p of v.postings) {
                if (p.ledger !== name) continue;
                const amt = Number(p.amount);
                net += p.dr_cr === "C" ? amt : -amt;
              }
            }
            return net.toFixed(4);
          };
          const [cg = "", sg = "", ig = ""] = names;
          return { camt: head(cg), samt: head(sg), iamt: head(ig) };
        };
        const outward = books(outNames, "SALES");
        const itc = books(inNames, "PURCHASE");
        // ⛔⛔ THE RETURNS SIDE IS SET EQUAL TO THE BOOKS, AND THAT IS A LIMITATION
        // OF THIS MOCK RATHER THAN A CLAIM ABOUT ANY REAL COMPANY. The server's
        // three columns come from three INDEPENDENT computations — GSTR-1's
        // section classifier, GSTR-3B's own SQL, and the postings — and this mock
        // has none of the first two: it stores postings and never derives a GST
        // return from anything.
        //
        // So THE MOCK CAN NEVER PRODUCE A DIVERGENCE, and a screen test that needs
        // one MUST build the response itself rather than driving this method. A
        // mock that faked a divergence would be worse: it would let a screen's
        // offsetting rendering pass here while the real wire shape went untested.
        months.push({
          fp: w.fp,
          from: w.from,
          to: w.to,
          r1_txval: "0.0000",
          r1_tax: outward,
          r3b_txval: "0.0000",
          r3b_tax: outward,
          books_outward_tax: outward,
          r3b_itc_available: itc,
          r3b_itc_ineligible: zeroTax9(),
          books_itc_tax: itc,
          hsn_txval: "0.0000",
          hsn_tax: outward,
          r1_matches_r3b: true,
          r3b_matches_books: true,
          itc_matches_books: true,
          hsn_foots_to_r1: true,
        });
      }

      const agreed = (a: string, b: string): GSTR9Divergence => ({
        a,
        b,
        net: zeroTax9(),
        gross: zeroTax9(),
        offsetting: false,
        agrees: true,
      });

      return {
        gstr9: {
          gstin: gstin ?? c.gstin,
          fy: `${fy}-${String((fy + 1) % 100).padStart(2, "0")}`,
          from,
          to,
          months,
          outward_r1_vs_r3b: agreed("gstr1", "gstr3b"),
          outward_r3b_vs_books: agreed("gstr3b", "books"),
          outward_r1_vs_books: agreed("gstr1", "books"),
          itc_vs_books: agreed("gstr3b-4a-plus-4d", "books"),
          hsn_vs_r1: agreed("gstr1-table-12", "gstr1-sections"),
          taxable_r1_vs_r3b: {
            a: "gstr1",
            b: "gstr3b",
            net: "0.0000",
            gross: "0.0000",
            offsetting: false,
            agrees: true,
          },
          tables: gstr9Tables(),
          // The TEN unconditional tokens, in the sorted order cored emits them.
          // ⛔ NOT A SUBSET. A mock that disclosed less than the server would let a
          // screen ship without rendering a limitation the real product always has.
          disclosures: [
            "advances-not-modelled",
            "amendments-not-modelled",
            "annual-table-structure-unverified",
            "books-carry-no-taxable-value",
            "exempt-and-zero-rated-not-marked",
            "itc-reversals-not-computed",
            "no-annual-2a",
            "no-cash-or-credit-ledger",
            "not-filing-ready",
            "portal-schema-unverified",
          ],
        },
        reconciled: true,
      };
    },
    async budgetVariance(
      companyID,
      budgetID: string,
      dimension: BudgetDimension,
      costCategoryID?: string,
    ): Promise<BudgetVarianceResponse> {
      ensure(companyID);
      // ⛔ THE THREE REFUSALS, IN THE ORDER cored CHECKS THEM. The dimension is a
      // pure check and runs first; the plan needs a lookup and runs second. A mock
      // that refused in a different order would let a screen's error handling pass
      // here and fail against the real server.
      if (!dimension) {
        throw new ApiError(
          400,
          "a dimension must be named (dimension): a budget is compared at ONE grain, and the three answer different questions — GROUP, LEDGER, COST_CENTRE",
        );
      }
      if (!["GROUP", "LEDGER", "COST_CENTRE"].includes(dimension)) {
        throw new ApiError(
          400,
          `"${dimension}" is not a budget dimension — want one of GROUP, LEDGER, COST_CENTRE`,
        );
      }
      const plan = mockBudgets().find((b) => b.id === budgetID);
      if (!budgetID) {
        throw new ApiError(
          400,
          `a budget must be named (budget_id): a variance report is a comparison, and there is no default plan to compare against. This company has: ${mockBudgets()
            .map((b) => `"${b.name}" (${b.from_date} to ${b.to_date}, ${b.id})`)
            .join("; ")}`,
        );
      }
      if (!plan) {
        throw new ApiError(422, `no budget ${budgetID} in these books`);
      }
      // ⛔ THE COST-CENTRE ARM DEMANDS AN AXIS. The same money is allocated in full
      // on every axis, so a report spanning two would total a multiple of the cost
      // borne — the identical rule costCentreSummary enforces.
      if (dimension === "COST_CENTRE" && !costCategoryID) {
        throw new ApiError(
          400,
          "cost_category_id is required: a cost-centre variance is always for ONE axis, because centres on different axes describe the same money and summing them double-counts",
        );
      }

      const lines = mockBudgetLines(budgetID).filter(
        (l) => l.dimension === dimension,
      );
      const row = (
        l: BudgetLine,
        actualDebit: string,
        status: BudgetStatus,
        variance: string,
        variancePercent: string | null,
        notes: string[] = [],
      ): BudgetVarianceRow => ({
        dimension: l.dimension,
        target_id: l.target_id,
        target_name: l.target_name,
        nature: l.nature,
        basis: l.basis,
        dr_cr: l.dr_cr,
        budget: l.amount,
        actual_debit: actualDebit,
        actual_credit: "0.0000",
        actual: actualDebit,
        variance,
        variance_percent: variancePercent,
        status,
        note: l.note,
        notes,
      });

      const rows: BudgetVarianceRow[] = lines.map((l) => {
        if (l.id === "budget-line-rent") {
          // ⛔ UNSPENT, NOT ZERO VARIANCE. Nothing was posted at all.
          return row(l, "0.0000", "UNSPENT", "-120000.0000", "-100.00", [
            "Nothing was posted against this at all in the period. That is UNSPENT, not a variance of zero — the whole budget is untouched, which is a fact about the plan or about what has been keyed, not a sign of accuracy.",
          ]);
        }
        if (l.id === "budget-line-debtors") {
          return row(l, "3500000.0000", "UNDER", "-500000.0000", "-12.50");
        }
        return row(l, "620000.0000", "OVER", "120000.0000", "24.00");
      });

      // ⛔ UNBUDGETED IN ITS OWN ARRAY, WITH variance_percent NULL. A screen that
      // has never seen this row ships with no "no budget set" rendering.
      const unbudgeted: BudgetVarianceRow[] =
        dimension === "COST_CENTRE"
          ? []
          : [
              {
                dimension,
                target_id: "ledger-stationery",
                target_name: "Stationery",
                nature: "EXPENSE",
                basis: "NETT_TRANSACTIONS",
                dr_cr: "D",
                budget: "0.0000",
                actual_debit: "83000.0000",
                actual_credit: "0.0000",
                actual: "83000.0000",
                variance: "83000.0000",
                variance_percent: null,
                variance_percent_absent_because:
                  "no budget was set for this target, so there is nothing for the actual to be a percentage of",
                status: "UNBUDGETED",
                note: "",
                notes: [
                  "No budget line covers this. It is UNBUDGETED, not 100% over budget: the plan is silent about it rather than exceeded, and no percentage of a budget that was never set can be stated.",
                ],
              },
            ];

      const bases = Array.from(new Set(rows.map((r) => r.basis))).sort();
      const totals = bases.map((basis) => {
        const mine = rows.filter((r) => r.basis === basis);
        const sum = (pick: (r: BudgetVarianceRow) => string) =>
          (
            mine.reduce((acc, r) => acc + Number(pick(r)), 0) as number
          ).toFixed(4);
        return {
          basis,
          rows: mine.length,
          budget: sum((r) => r.budget),
          actual: sum((r) => r.actual),
          variance: sum((r) => r.variance),
        };
      });
      // ⛔⛔ A GRAND TOTAL ONLY WHEN EVERY ROW SHARES ONE BASIS. A position plus a
      // flow is not a quantity, and a client that ignores the flag must find zeros.
      const addable = totals.length <= 1;
      const notes: string[] = [];
      if (!addable) {
        notes.push(
          `⛔ THIS PLAN MIXES BOTH BASES, SO THERE IS NO GRAND TOTAL. A closing-balance budget is a POSITION as at ${plan.to_date} and a nett-transactions budget is MOVEMENT within the period; adding them would not produce a quantity. Use the per-basis subtotals.`,
        );
      }
      if (rows.length === 0) {
        notes.push(
          "This budget has no lines at this grain, so there is nothing to compare. Every actual below is listed as unbudgeted — that is the absence of a plan, not a plan that was met.",
        );
      }
      notes.push(
        `The budget column is this plan's WHOLE period (${plan.from_date} to ${plan.to_date}) and is not apportioned to any shorter window. Comparing a full-period budget against part-period actuals needs a spread rule (straight-line, monthly or seasonal) that nobody has chosen, so this report does not narrow the period rather than inventing one.`,
      );
      notes.push(
        "OVER and UNDER are statements about arithmetic, not about good and bad. Spending more than budget and EARNING more than budget are both OVER. Each row carries the target's nature so a reader can tell which it is.",
      );

      const statusCounts: Partial<Record<BudgetStatus, number>> = {};
      for (const r of [...rows, ...unbudgeted]) {
        statusCounts[r.status] = (statusCounts[r.status] ?? 0) + 1;
      }

      const axis = mockCostCategories().find((c) => c.id === costCategoryID);
      return {
        budget_id: plan.id,
        budget_name: plan.name,
        dimension,
        from_date: plan.from_date,
        to_date: plan.to_date,
        rows,
        unbudgeted,
        unbudgeted_count: unbudgeted.length,
        unbudgeted_actual: unbudgeted
          .reduce((acc, r) => acc + Number(r.actual), 0)
          .toFixed(4),
        totals,
        totals_are_addable: addable,
        total_budget: addable ? (totals[0]?.budget ?? "0.0000") : "0.0000",
        total_actual: addable ? (totals[0]?.actual ?? "0.0000") : "0.0000",
        total_variance: addable ? (totals[0]?.variance ?? "0.0000") : "0.0000",
        status_counts: statusCounts,
        cost_category_id: axis?.id ?? "",
        cost_category_name: axis?.name ?? "",
        notes,
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
            // The SAME label the Day Book carries (classLabel), because the
            // server reads both off the same `payload.class`. A second
            // derivation here is exactly how the mock diverged last time.
            class: v.class,
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
      // T4 slice 2b. A statement_line_id makes this the ACCEPTANCE of a
      // proposed match — the same write, additionally recording which imported
      // bank row the tick came from. There is no separate resolve route, here or
      // on the server.
      if (req.statement_line_id) {
        const store = c.bankStatements.get(ledgerID);
        const line = store?.lines.find((l) => l.id === req.statement_line_id);
        if (!line) {
          throw new ApiError(
            404,
            "bankstatement: that statement line is not on this bank account",
          );
        }
        // ⛔ THE ASSERTED DATE MUST BE THE ROW'S OWN VALUE DATE, refused naming
        // BOTH rather than resolved in either direction — silently preferring
        // one would be invisible afterwards, because every figure would be real.
        if (line.value_date !== req.bank_date) {
          throw new ApiError(
            400,
            `bankstatement: this bank row is dated ${line.value_date} on the statement ` +
              `but the request asserts ${req.bank_date}; a match records the bank's own date and nothing else`,
          );
        }
        if (c.bankMatches.has(`${ledgerID}:${req.statement_line_id}`)) {
          throw new ApiError(
            409,
            "bankstatement: that statement line has already been resolved",
          );
        }
        // The TIER IS THE MOCK'S OWN VERDICT, never the caller's — there is no
        // field by which a caller could describe its own evidence.
        const apart = mockDaysApart(line.value_date, v.date);
        const sameAmount =
          mockMoneyToUnits(leg.amount) === mockMoneyToUnits(line.amount) &&
          leg.dr_cr === line.dr_cr;
        c.bankMatches.set(`${ledgerID}:${req.statement_line_id}`, {
          line_id: req.statement_line_id,
          status: "matched",
          tier: !sameAmount
            ? "manual"
            : apart === 0
              ? "exact"
              : apart <= MOCK_MATCH_WINDOW_DAYS
                ? "potential"
                : "manual",
          matched_by: req.created_by,
          matched_at: new Date().toISOString(),
          voucher_id: voucherID,
          leg_no: legNo,
          voucher_no: v.voucher_no,
          class: v.class,
          bank_date: req.bank_date,
          line_value_date: line.value_date,
        });
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
      // ⭐ AND THE MATCH GOES WITH IT — migration 0088's ON DELETE CASCADE, which
      // is what makes un-linking a match (Alt+F8) and un-ticking a bank date the
      // SAME operation rather than two that can drift apart. A match outliving
      // its clearance would leave the leg un-ticked while the bank row still read
      // as matched.
      for (const [k, m] of c.bankMatches) {
        if (
          k.startsWith(`${ledgerID}:`) &&
          m.status === "matched" &&
          m.voucher_id === voucherID &&
          m.leg_no === legNo
        ) {
          c.bankMatches.delete(k);
        }
      }
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

      // ⚠️ PARSE BEFORE DEDUPLICATING, which is the order cored uses too ("PARSE
      // BEFORE OPENING THE TRANSACTION"). It matters since D111: the name a
      // PASTED statement is stored under is derived from the file's own period
      // and row count, so it does not exist until the file has been read — and
      // the dedup below is by name.
      const importID = `mock-import-${store.imports.length + 1}`;
      const parsed = mockParseStatement(req.content, req.mapping, importID);
      if (parsed.length === 0) {
        throw new ApiError(
          400,
          "bankstatement: the file has no transaction rows: it has a header row and nothing under it",
        );
      }
      const dates = parsed.map((l) => l.value_date).sort();
      const fileName = mockStatementName(
        req.file_name,
        dates[0]!,
        dates[dates.length - 1]!,
        parsed.length,
        req.content,
      );

      // Only an identical FILE is caught, unlike the server, which dedups by
      // TRANSACTION. Named in the state comment.
      //
      // ⛔ IT IS THE RESOLVED NAME THAT IS COMPARED, NOT THE ONE THE CALLER
      // SENT. Comparing `req.file_name` meant every PASTED statement matched
      // every other pasted statement — both are "" — so the second paste on an
      // account came back `already_imported` having stored nothing. That is
      // D111 wearing the mock's clothes.
      const already = store.imports.find((i) => i.file_name === fileName);
      if (already) {
        return {
          import_id: already.import_id,
          already_imported: true,
          file_name: already.file_name,
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

      store.lines.push(...parsed);
      const im: BankStatementImport = {
        import_id: importID,
        file_name: fileName,
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
        file_name: fileName,
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

    // ---- BANK STATEMENT MATCHING (T4 slice 2b).
    //
    // ⚠️ See mockProposeMatches for what this matcher deliberately does NOT do
    // (no partial tier, no strong-key tier, a fixed window). What it DOES
    // reproduce faithfully is ambiguity in both directions, which is invariant
    // B-1 and the thing a screen can get dangerously wrong.
    async bankStatementMatches(
      companyID,
      ledgerID,
      opts?,
    ): Promise<BankMatchesResponse> {
      const c = ensure(companyID);
      const ledger = mockRequireBankLedger(c, ledgerID);
      const store = c.bankStatements.get(ledgerID) ?? { imports: [], lines: [] };
      const from = opts?.from;
      const to = opts?.to;
      const inPeriod = (d: string) => (!from || d >= from) && (!to || d <= to);

      const resolvedKeys = new Set(
        [...c.bankMatches.keys()]
          .filter((k) => k.startsWith(`${ledgerID}:`))
          .map((k) => k.slice(ledgerID.length + 1)),
      );
      const open = store.lines.filter(
        (l) => inPeriod(l.value_date) && !resolvedKeys.has(l.id),
      );
      const legs = mockCandidateLegs(c, ledgerID, ledger.name);
      const proposals = mockProposeMatches(open, legs);

      const proposed = new Set<string>();
      for (const p of proposals) {
        for (const cnd of p.candidates) proposed.add(`${cnd.voucher_id}:${cnd.leg_no}`);
      }
      const unmatchedLegs = legs.filter(
        (l) => !proposed.has(`${l.voucher_id}:${l.leg_no}`),
      );

      const resolved = [...c.bankMatches.entries()]
        .filter(([k]) => k.startsWith(`${ledgerID}:`))
        .map(([, m]) => ({
          ...m,
          // Derived on READ, never stored: true when somebody retyped the Bank
          // Date cell after matching, so the tick and the statement row no
          // longer agree about WHEN.
          bank_date_differs:
            m.status === "matched" &&
            !!m.bank_date &&
            !!m.line_value_date &&
            m.bank_date !== m.line_value_date,
        }))
        .sort((a, b) => (a.matched_at < b.matched_at ? 1 : -1));

      const counts: BankMatchCounts = {
        lines: proposals.length,
        exact: proposals.filter((p) => p.best_tier === "exact").length,
        potential: proposals.filter((p) => p.best_tier === "potential").length,
        partial: proposals.filter((p) => p.best_tier === "partial").length,
        ambiguous: proposals.filter((p) => p.ambiguous).length,
        no_candidate: proposals.filter((p) => p.best_tier === "").length,
        acceptable_in_bulk: proposals.filter((p) => p.acceptable_in_bulk).length,
        unmatched_legs: unmatchedLegs.length,
      };

      const disclosures = [
        "NOTHING ON THIS SCREEN HAS BEEN RECONCILED. Every row below is a PROPOSAL; " +
          "a bank date is written only when you accept one, and accepting is the assertion " +
          "that this bank row IS that voucher.",
        `Candidates were sought within ${MOCK_MATCH_WINDOW_DAYS} days either side of the bank's date. ` +
          "That window is this build's search width, not a rule from anywhere — widening it only " +
          "shows you more proposals, because none of them clears itself.",
        "Amounts must agree TO THE PAISA. A row that is one paisa out is not shown as a match to anything, " +
          "deliberately: it is a bank charge or a different transaction, and absorbing it into a tolerance " +
          "is how a real difference hides for a year.",
      ];
      if (counts.ambiguous > 0) {
        disclosures.push(
          `${counts.ambiguous} row(s) fit more than one voucher, or compete with another bank row for the same voucher. ` +
            "None of them can be accepted in bulk and none has been chosen for you — " +
            "two payments of the same amount on the same day are ordinary, and picking one is right half the time.",
        );
      }

      return {
        ledger_id: ledgerID,
        ledger_name: ledger.name,
        from: from ?? "",
        to: to ?? "",
        date_window_days: MOCK_MATCH_WINDOW_DAYS,
        proposals,
        unmatched_legs: unmatchedLegs,
        resolved,
        counts,
        disclosures,
      };
    },

    async setAsideStatementLine(
      companyID,
      ledgerID,
      lineID,
      req: SetAsideRequest,
    ): Promise<SetAsideResponse> {
      const c = ensure(companyID);
      mockRequireBankLedger(c, ledgerID);
      const store = c.bankStatements.get(ledgerID) ?? { imports: [], lines: [] };
      const line = store.lines.find((l) => l.id === lineID);
      if (!line) {
        throw new ApiError(
          404,
          "bankstatement: that statement line is not on this bank account",
        );
      }
      if (c.bankMatches.has(`${ledgerID}:${lineID}`)) {
        throw new ApiError(
          409,
          "bankstatement: that statement line has already been resolved",
        );
      }
      // ⛔ NO CLEARANCE IS WRITTEN. A set-aside asserts that this bank row
      // corresponds to nothing in the books; it is not a weaker kind of match,
      // and it moves no balance — c.bankDates is untouched.
      c.bankMatches.set(`${ledgerID}:${lineID}`, {
        line_id: lineID,
        status: "set-aside",
        tier: "manual",
        note: req.note,
        matched_by: req.created_by ?? "ui",
        matched_at: new Date().toISOString(),
        line_value_date: line.value_date,
      });
      return { status: "set-aside" };
    },

    async restoreStatementLine(
      companyID,
      ledgerID,
      lineID,
    ): Promise<SetAsideResponse> {
      const c = ensure(companyID);
      mockRequireBankLedger(c, ledgerID);
      const store = c.bankStatements.get(ledgerID) ?? { imports: [], lines: [] };
      if (!store.lines.some((l) => l.id === lineID)) {
        throw new ApiError(
          404,
          "bankstatement: that statement line is not on this bank account",
        );
      }
      const existing = c.bankMatches.get(`${ledgerID}:${lineID}`);
      if (existing?.status === "matched") {
        // ⛔ REFUSED, and the refusal is the feature. Deleting a matched row here
        // would leave the clearance behind with nothing pointing at it: the leg
        // would stay ticked while the bank row returned to the unmatched list,
        // and the same row could be reconciled a second time against a second
        // voucher. Both would look right.
        throw new ApiError(
          400,
          "bankstatement: that line is MATCHED to a voucher, not set aside; " +
            "un-tick the bank date on the reconciliation screen to undo it, " +
            "which withdraws the clearance and the match together",
        );
      }
      // Idempotent otherwise: restoring a line that was never set aside is a
      // success, because the caller's intent is satisfied either way.
      c.bankMatches.delete(`${ledgerID}:${lineID}`);
      return { status: "unmatched" };
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
      const inPeriod = (d: string) => (!from || d >= from) && (!to || d <= to);
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
        "THIS REPORT SHOWS NO MATCH STATUS (T4 slice 2b). These are the bank's own rows, and a row " +
          "here may already be matched to a voucher, set aside as matching nothing, or untouched — " +
          "nothing on this report says which. Matching now works: press Alt+S on the Bank " +
          "Reconciliation screen. Reconciling by hand, a Bank Date at a time, still works too.",
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
      return outstandingsOf(
        companyID,
        query?.kind ?? "RECEIVABLE",
        query?.as_on,
      );
    },

    /**
     * THE PAYABLES AGEING under its own name (D94).
     *
     * ⛔ IT DOES NOT CALL `this.getOutstandings`. Both public methods are thin
     * wrappers over ONE local implementation, mirroring cored, where
     * `writeOutstandings` is the single writer behind both doors. Delegating
     * through the sibling METHOD would mean a test that stubs or spies on
     * `getOutstandings` silently changes what this one does — a mock that couples
     * two surfaces the server keeps independent, which is D84's class of false
     * green pointed at the very split this row exists to make.
     *
     * ⚠️ WHAT THE MOCK CANNOT MODEL is the only thing that actually differs: the
     * PDP sees a different resource for a different PATH. There is no ACL in this
     * store, so a screen's 403 for `report:bills-payable` can only be driven
     * against real cored — `drive-l24-d94.mjs` is where that arm lives.
     */
    async getBillsPayable(companyID, query?: { as_on?: string }) {
      return outstandingsOf(companyID, "PAYABLE", query?.as_on);
    },

    /**
     * ONE PARTY's open bills, for the settlement picker (D97).
     *
     * ⛔ A THIRD THIN WRAPPER OVER THE SAME LOCAL IMPLEMENTATION, never a call to
     * a sibling METHOD, for the reason `getBillsPayable` records above: cored has
     * one writer behind these doors, and a mock whose surfaces delegate through
     * each other lets a test that stubs one silently change another.
     *
     * ⛔ IT NARROWS TO THE PARTY HERE, and that is fidelity rather than tidiness:
     * cored emits ONE party and no totals from this route, so a mock that handed
     * back the whole report would let a screen bug that reads another party's
     * bills — or the ageing total — pass green and fail on the wire.
     *
     * ⚠️ NO `as_on` IS ACCEPTED OR APPLIED, matching the route (D86): "which open
     * bill is this payment for" is answered by a future-dated bill too.
     *
     * ⚠️ WHAT THE MOCK CANNOT MODEL is the whole reason the route exists: there is
     * no PDP in this store, so `voucherClass` changes nothing here. That it is the
     * AUTHORITY can only be driven against real cored — see drive-l25-d97.mjs.
     */
    async getUnadjustedForex(
      companyID,
      query?: { as_on?: string },
    ): Promise<UnadjustedForexResponse> {
      // ⛔⛔ MODELLED, NOT STUBBED, AND D84 IS THE REASON. A double that returned an
      // empty list would let every screen test prove that a report with no rows
      // renders — which is the one case this screen must NOT be tuned for. It nets
      // the same open-bill universe cored does, prices it from the same rate
      // history, and reproduces Decision 3's unpriced row.
      //
      // ⚠️ IT DOES **NOT** MODEL `ledger_opening_bill`, and that gap PRE-DATES this
      // method: `setLedgerOpening` and `createLedger` store the opening BALANCE and
      // never the breakup, so the mock's Outstandings has never carried an opening
      // bill either. It is named here rather than quietly inherited — a foreign
      // OPENING bill is exactly §0.2's trap, and the coverage for it lives in the Go
      // test and in the drive's psql oracle.
      const c = ensure(companyID);
      const bound = (query?.as_on ?? "").trim();
      const pricedOn = bound || new Date().toISOString().slice(0, 10);

      type Grp = {
        ledgerID: string;
        ref: string;
        refType: string;
        code: string;
        currencyID: string;
        units: bigint;
        foreign: bigint;
        billRate: string;
      };
      const net = new Map<string, Grp>();
      for (const view of c.views.values()) {
        if (bound !== "" && (view.voucher_date ?? "") > bound) continue;
        for (const b of view.bill_allocations) {
          // FOREIGN references only, and never the On Account bucket — see the
          // server's own comment on why the exclusion is by NAME.
          if (!b.currency_id || b.ref_name === ON_ACCOUNT_REF) continue;
          const ledger = c.ledgers.find((l) => l.id === b.ledger_id);
          if (!ledger) continue;
          if (
            ledger.group !== "Sundry Debtors" &&
            ledger.group !== "Sundry Creditors"
          )
            continue;
          const key = `${b.ledger_id}\u0000${b.ref_name}\u0000${b.currency_id}`;
          const cur = net.get(key) ?? {
            ledgerID: b.ledger_id,
            ref: b.ref_name,
            refType: "NEW",
            code: b.currency ?? "",
            currencyID: b.currency_id,
            units: 0n,
            foreign: 0n,
            billRate: "0",
          };
          // Debit-positive here, and oriented to the party's natural side below —
          // the same two steps cored takes, in the same order.
          const sgn = b.dr_cr === "D" ? 1n : -1n;
          cur.units += sgn * mockMoneyToUnits(b.amount);
          cur.foreign += sgn * mockMoneyToUnits(b.foreign_amount ?? "0");
          // ⛔ THE OPENER'S RATE, NEVER AN `AGAINST` ROW'S. An AGAINST carries the
          // SETTLEMENT-date rate, and taking it would report a plausible rate off
          // the wrong document.
          if (b.ref_type !== "AGAINST") {
            cur.refType = b.ref_type;
            cur.billRate = b.rate ?? "0";
          }
          net.set(key, cur);
        }
      }

      // The latest rate ON OR BEFORE the date, or nothing at all.
      const rateAsOn = (currencyID: string): string | null => {
        let best: { on: string; rate: string } | null = null;
        for (const r of c.currencyRates) {
          if (r.currencyID !== currencyID) continue;
          if (r.on > pricedOn) continue;
          if (!best || r.on > best.on) best = { on: r.on, rate: r.rate };
        }
        return best ? best.rate : null;
      };

      const bills: UnadjustedForexBill[] = [];
      let total = 0n;
      let unrated = 0;
      for (const g of net.values()) {
        const ledger = c.ledgers.find((l) => l.id === g.ledgerID);
        if (!ledger) continue;
        const payable = ledger.group === "Sundry Creditors";
        const book = payable ? -g.units : g.units;
        const fgn = payable ? -g.foreign : g.foreign;
        // Step 6's TWO-QUESTION skip: both columns zero means the reference is
        // closed and its difference has been REALISED.
        if (book === 0n && fgn === 0n) continue;

        const r = rateAsOn(g.currencyID);
        let difference = 0n;
        if (r !== null) {
          // round(foreign × rate, 4), ONE rounding on the PRODUCT — and the rate is
          // scaled by 1e10, never quantised to money first (§6.3).
          // ⛔ ONE ROUNDING, ON THE PRODUCT, AND THE RATE IS NEVER QUANTISED TO
          // MONEY FIRST (§6.3). `fgn` is money in units of 1e-4 and `parseRate`
          // returns units of 1e-10, so the product is in 1e-14 and dividing by
          // 1e10 lands back on money. HALF-UP, to match decimal.Decimal.Round.
          const scaled = fgn * parseRate(r);
          const half = MOCK_RATE_SCALE / 2n;
          const rounded =
            scaled >= 0n
              ? (scaled + half) / MOCK_RATE_SCALE
              : -((-scaled + half) / MOCK_RATE_SCALE);
          difference = rounded - book;
          if (payable) difference = -difference;
          total += difference;
        } else {
          unrated++;
        }
        bills.push({
          ledger_id: g.ledgerID,
          ledger: ledger.name,
          group: ledger.group,
          ref_name: g.ref,
          ref_type: g.refType,
          currency_code: g.code,
          foreign_pending: mockUnitsToMoney(fgn),
          book_value: mockUnitsToMoney(book),
          difference: r === null ? "0.0000" : mockUnitsToMoney(difference),
          bill_rate: formatRate(parseRate(g.billRate)),
          rate_as_on: formatRate(parseRate(r ?? "0")),
          has_rate_as_on: r !== null,
        });
      }
      // Priced rows first, then by the ABSOLUTE difference — a loss of 50,000 is as
      // interesting as a gain of 50,000, so sorting signed would bury every loss.
      bills.sort((a, b) => {
        if (a.has_rate_as_on !== b.has_rate_as_on)
          return a.has_rate_as_on ? -1 : 1;
        const aa = Math.abs(Number(a.difference));
        const bb = Math.abs(Number(b.difference));
        if (aa !== bb) return bb - aa;
        return (
          a.ledger.localeCompare(b.ledger) || a.ref_name.localeCompare(b.ref_name)
        );
      });
      return {
        as_on: bound,
        priced_as_on: pricedOn,
        bills,
        total_difference: mockUnitsToMoney(total),
        unrated_count: unrated,
        posts_nothing: true,
      };
    },

    async getPartyOpenBills(
      companyID,
      _voucherClass,
      ledgerID,
      kind,
    ): Promise<PartyOpenBillsResponse> {
      const out = await outstandingsOf(companyID, kind, undefined);
      const party = out.parties.find((p) => p.ledger_id === ledgerID);
      return {
        kind,
        ledger_id: ledgerID,
        ledger: party?.ledger ?? "",
        group: party?.group ?? "",
        pending: party?.pending ?? "0.0000",
        bills: party?.bills ?? [],
        found: party !== undefined,
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
        vouchers_altered: 0,
        vouchers_deleted: 0,
        vouchers_skipped: 0,
        vouchers_voided: 0,
        voided_vouchers: [],
        ledgers_tombstoned: [],
        groups_ensured: [],
        ledgers_ensured: [],
        warnings: [],
        posted: [],
        // ⚠️ ZEROES, AND HONEST ONES: this mock does not parse Tally XML at all
        // (the real importer is 1,500 lines of Go), so it imports nothing and
        // says so. The fields are present because the SHAPE is the server's —
        // register row D84's rule — and a client that reads
        // `res.stock_lines` must not get `undefined` here while type-checking.
        stock_lines: 0,
        opening_positions: 0,
        stock_items_ensured: [],
        units_ensured: [],
        godowns_ensured: [],
        batches_ensured: [],
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
    async closePurchaseOrder(
      companyID,
      orderID: string,
      req: CloseOrderRequest,
    ) {
      return mockCloseOrder(companyID, "PURCHASE", orderID, req);
    },
    async createBom(companyID, req: CreateBomRequest) {
      const co = ensure(companyID);
      const name = (req.name ?? "").trim();
      if (!req.stock_item_id) {
        throw new ApiError(
          422,
          "orders: a bill of materials must name the finished good it produces",
        );
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
          throw new ApiError(
            422,
            "orders: the same item appears twice in this recipe",
          );
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
        throw new ApiError(
          409,
          `orders: this item already has a recipe named "${name}"`,
        );
      }
      if (
        req.is_default &&
        mockBoms.some(
          (b) => b.stock_item_id === req.stock_item_id && b.is_default,
        )
      ) {
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
        throw new ApiError(
          404,
          "orders: no such bill of materials in this company",
        );
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
        throw new ApiError(
          422,
          "orders: the quantity to make must be more than zero",
        );
      }
      const b = mockBoms.find((x) => x.bom_id === bomID);
      if (!b) {
        throw new ApiError(
          404,
          "orders: no such bill of materials in this company",
        );
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
        stale: current !== undefined && current !== held.generatedFromVersion,
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
        Number(view.cgst ?? 0) +
        Number(view.sgst ?? 0) +
        Number(view.igst ?? 0);
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

    /**
     * THE CASH-FLOW PROJECTION.
     *
     * ⛔⛔ THIS ARM EXISTS BECAUSE AN UNDISCLOSED PROJECTION MUST NOT BE
     * REACHABLE FROM A HERMETIC TEST. `estimate`, `method` and `basis` are
     * REQUIRED on the wire (register defect D23), and if the mock returned the
     * buckets alone then every vitest in this repo would render invented
     * settlement dates as if they were posted balances and pass — the screen
     * would look finished and the disclosure would only be missing where nobody
     * was watching. So the counts below are DERIVED from this store's own bills
     * rather than stubbed, and `from_default_credit_days.bills` is non-zero on
     * the seeded data on purpose: it is the figure that says how much of the
     * answer is a constant compiled into the build talking.
     *
     * ⚠️ THE SIMPLIFICATIONS, STATED RATHER THAN HIDDEN — all in the direction
     * of showing LESS than cored, never more (the D84 rule):
     *   - `opening_cash` sums postings on Cash-in-Hand and bank-group ledgers up
     *     to `as_on`. cored reads its own balance query; a company whose cash
     *     sits in a custom group is right in cored and low here.
     *   - the horizon and bucket width are cored's DEFAULTS (90 days, weekly),
     *     because the client never sends anything else.
     *   - `not_modelled` and `what_is_projected` are cored's sentences verbatim.
     *     They are copied, not paraphrased: a screen test that pins a paraphrase
     *     would go green while the real disclosure changed underneath it.
     */
    async cashFlow(companyID, asOn?: string): Promise<CashFlowResponse> {
      const c = ensure(companyID);
      const asOnDate = asOn || new Date().toISOString().slice(0, 10);
      const DEFAULT_CREDIT_DAYS = 30;
      const HORIZON = 90;
      const BUCKET = 7;

      // ---- opening cash, from the postings this store actually holds.
      const cashLedgers = new Set(
        c.ledgers
          .filter(
            (l) =>
              l.group === "Cash-in-Hand" ||
              BANK_PRIMARY_GROUPS.includes(l.group) ||
              l.bankParentGroup != null,
          )
          .map((l) => l.name),
      );
      let openingUnits = 0n;
      for (const v of c.vouchers) {
        if (v.date > asOnDate) continue;
        for (const pl of v.postings) {
          if (!cashLedgers.has(pl.ledger)) continue;
          const amt = mockMoneyToUnits(pl.amount);
          openingUnits += pl.dr_cr.trim().toUpperCase().startsWith("D")
            ? amt
            : -amt;
        }
      }

      // ---- the open bills, netted exactly as getOutstandings nets them:
      // by (ledger_id, ref_name), ignoring ref_type, because that is what
      // reporting.Outstandings does.
      interface ProjBill {
        units: bigint;
        inflow: boolean;
        settles: string;
        source: "due" | "party" | "default";
      }
      const net = new Map<
        string,
        { ledgerID: string; units: bigint; due: string; inflow: boolean }
      >();
      for (const view of c.views.values()) {
        for (const b of view.bill_allocations) {
          const ledger = c.ledgers.find((l) => l.id === b.ledger_id);
          if (!ledger) continue;
          const inflow = ledger.group === "Sundry Debtors";
          const outflow = ledger.group === "Sundry Creditors";
          if (!inflow && !outflow) continue;
          const signed =
            b.dr_cr === "D"
              ? mockMoneyToUnits(b.amount)
              : -mockMoneyToUnits(b.amount);
          const key = `${b.ledger_id} ${b.ref_name}`;
          const cur = net.get(key) ?? {
            ledgerID: b.ledger_id,
            units: 0n,
            due: "",
            inflow,
          };
          // Positive on the party's natural side, per direction.
          cur.units += inflow ? signed : -signed;
          if (b.due_date && (cur.due === "" || b.due_date < cur.due)) {
            cur.due = b.due_date;
          }
          net.set(key, cur);
        }
      }

      const bills: ProjBill[] = [];
      for (const { ledgerID, units, due, inflow } of net.values()) {
        if (units === 0n) continue; // a settled bill is not outstanding
        const ledger = c.ledgers.find((l) => l.id === ledgerID);
        // ⛔ THE THREE SOURCES, IN CORED'S OWN PRECEDENCE: an observed due date,
        // else the party's configured credit period, else the build's constant.
        // Collapsing the last two would hide exactly what `basis` discloses.
        let settles: string;
        let source: ProjBill["source"];
        if (due !== "") {
          settles = due;
          source = "due";
        } else if (ledger?.creditDays != null) {
          settles = mockAddDays(asOnDate, ledger.creditDays);
          source = "party";
        } else {
          settles = mockAddDays(asOnDate, DEFAULT_CREDIT_DAYS);
          source = "default";
        }
        bills.push({ units, inflow, settles, source });
      }

      // ---- the buckets. `from` inclusive, `to` exclusive, weekly.
      const zero = () => ({ in: 0n, out: 0n });
      // ⛔ `ceil`, NOT a plain divide. 90/7 is 12.857 and `Array.from` floors a
      // fractional length to TWELVE buckets, silently dropping the last partial
      // week — cored answers THIRTEEN (measured: 13 buckets for the default
      // horizon), so a floor here would make the mock and the server disagree
      // about how far the projection reaches, in the direction that hides money.
      const buckets = Array.from(
        { length: Math.ceil(HORIZON / BUCKET) },
        (_, i) => ({
          from: mockAddDays(asOnDate, i * BUCKET),
          to: mockAddDays(asOnDate, (i + 1) * BUCKET),
          ...zero(),
        }),
      );
      const beyond = zero();
      const basisCounts = {
        due: { bills: 0, ...zero() },
        party: { bills: 0, ...zero() },
        default: { bills: 0, ...zero() },
      };
      const overdue = { bills: 0, ...zero() };
      let anyOverdue = false;

      for (const b of bills) {
        const abs = b.units < 0n ? -b.units : b.units;
        const add = (t: { in: bigint; out: bigint }) => {
          if (b.inflow) t.in += abs;
          else t.out += abs;
        };
        const bc = basisCounts[b.source];
        bc.bills += 1;
        add(bc);
        // OVERDUE FOLDS INTO BUCKET 1, exactly as cored does — a bill already
        // late is cash the business is waiting on now, not cash that never
        // arrives. Dropping it would flatter the projection.
        if (b.settles < asOnDate) {
          anyOverdue = true;
          overdue.bills += 1;
          add(overdue);
          // `first` is only undefined for a zero-bucket horizon, which cannot
          // arise from the constants above — but it is checked rather than
          // asserted, because a `!` here would crash the screen instead of
          // showing a short projection if the horizon ever became configurable.
          const first = buckets[0];
          if (first) add(first);
          continue;
        }
        const target = buckets.find(
          (k) => b.settles >= k.from && b.settles < k.to,
        );
        if (target) add(target);
        else add(beyond);
      }

      let running = openingUnits;
      const wireBuckets = buckets.map((k) => {
        const open = running;
        const nett = k.in - k.out;
        running = open + nett;
        return {
          from: k.from,
          to: k.to,
          inflow: mockUnitsToMoney(k.in),
          outflow: mockUnitsToMoney(k.out),
          nett: mockUnitsToMoney(nett),
          opening_cash: mockUnitsToMoney(open),
          closing_cash: mockUnitsToMoney(running),
        };
      });
      const count = (x: { bills: number; in: bigint; out: bigint }) => ({
        bills: x.bills,
        inflow: mockUnitsToMoney(x.in),
        outflow: mockUnitsToMoney(x.out),
      });

      return {
        as_on: asOnDate,
        horizon_days: HORIZON,
        bucket_days: BUCKET,
        opening_cash: mockUnitsToMoney(openingUnits),
        buckets: wireBuckets,
        beyond_horizon: {
          inflow: mockUnitsToMoney(beyond.in),
          outflow: mockUnitsToMoney(beyond.out),
          nett: mockUnitsToMoney(beyond.in - beyond.out),
        },
        overdue_folded_into_first_bucket: anyOverdue,
        estimate: true,
        method:
          "open-bill settlement projection (deterministic; no statistics, no model)",
        basis: {
          what_is_projected:
            "the DATE each open bill settles, not its amount. Amounts are the " +
            "pending balances the Outstandings report shows; the bucket a bill " +
            "falls into is derived.",
          assumed_settlement_date: {
            from_due_date: count(basisCounts.due),
            from_party_credit_days: count(basisCounts.party),
            from_default_credit_days: count(basisCounts.default),
            default_credit_days: DEFAULT_CREDIT_DAYS,
          },
          overdue_folded_into_first_bucket: count(overdue),
          not_modelled: [
            "vouchers not yet entered \u2014 this reads the books, not the business",
            "part payments arriving earlier or later than the projected date",
            "bills raised after as_on",
            "cash movements with no bill behind them (salaries, rent, taxes, drawings)",
          ],
        },
      };
    },

    /**
     * THE ANOMALY LIST — and it answers EMPTY, deliberately, with the reason
     * written down.
     *
     * ⛔ ALL FOUR OF CORED'S RULES NEED SOMETHING THIS STORE DOES NOT KEEP, AND
     * INVENTING A FIFTH RULE HERE WOULD BE WORSE THAN ANSWERING NOTHING.
     * BACKDATED, AFTER_HOURS and EDIT_BURST all key on `created_at` per voucher
     * VERSION — the "when was it entered" axis — and this store keeps no version
     * chain with entry timestamps. ROUND_AMOUNT keys on `taxable_value`, which is
     * the GST base and NOT the voucher total; substituting the total would flag a
     * different set of vouchers and a hermetic test pinning it would be pinning a
     * rule cored does not have. That is the D84 defect exactly: a mock that is
     * DIFFERENT from cored is a source of false green, and "different" includes
     * being cleverer.
     *
     * So: `from`/`to` are echoed (the screen's heading reads them, and empty
     * means UNBOUNDED, not today), the list is empty, and a screen test that
     * needs findings injects them through a stub client. ⛔ THE ORACLE FOR A
     * POPULATED LIST IS THE BROWSER AGAINST CORED — never this arm.
     */
    async anomalies(
      companyID,
      from?: string,
      to?: string,
    ): Promise<AnomaliesResponse> {
      ensure(companyID);
      return { from: from ?? "", to: to ?? "", findings: [] };
    },

    // =======================================================================
    // PAYROLL (Track 3). ⛔ READ THE `employees` FIELD'S DOC ON MockCompany
    // BEFORE PINNING A FIGURE: this store computes NOTHING statutory, and every
    // PF/EPS/ESI/TDS number it serves is an abstention rather than a
    // computation. The oracle for a payroll figure is the browser against
    // cored.
    // =======================================================================

    async listEmployees(
      companyID,
      includeInactive?: boolean,
    ): Promise<ListEmployeesResponse> {
      const c = ensure(companyID);
      // Active first then by code — the server's own ORDER BY. A mock that
      // returned insertion order would let a screen test pass while the live
      // list is sorted differently, which is exactly the kind of difference
      // nobody re-checks.
      const rows = c.employees
        .filter((e) => includeInactive || e.is_active)
        .slice()
        .sort((a, b) =>
          a.is_active === b.is_active
            ? a.code.localeCompare(b.code)
            : a.is_active
              ? -1
              : 1,
        );
      return { employees: rows };
    },

    async createEmployee(
      companyID,
      req: CreateEmployeeRequest,
    ): Promise<CreateEmployeeResponse> {
      const c = ensure(companyID);
      const code = (req.code ?? "").trim();
      if (code === "") {
        throw new ApiError(400, "payrolldb: employee code is required");
      }
      if (
        c.employees.some((e) => e.code.toLowerCase() === code.toLowerCase())
      ) {
        throw new ApiError(
          422,
          `payrolldb: this company already has an employee with code ${JSON.stringify(code)}`,
        );
      }
      const employeeID = id("employee");
      c.employees.push({
        id: employeeID,
        code,
        name: req.name ?? "",
        doj: req.doj ?? "",
        dol: req.dol ?? "",
        designation: req.designation ?? "",
        pan: req.pan ?? "",
        uan: req.uan ?? "",
        esic_ip_number: req.esic_ip_number ?? "",
        bank_ifsc: req.bank_ifsc ?? "",
        bank_account_last4: req.bank_account_last4 ?? "",
        // ⛔ AN OMITTED FLAG IS TRUE, mirroring the server's *bool exactly. A
        // mock that defaulted it to false would make every screen test pass
        // against employees who are silently excluded from the pay run — which
        // is a missing salary, discovered on pay day.
        is_active: req.is_active === undefined ? true : req.is_active,
        ...(req.payable_ledger_id
          ? { payable_ledger_id: req.payable_ledger_id }
          : {}),
      });
      return { employee_id: employeeID };
    },

    async listSalaryComponents(
      companyID,
      employeeID: string,
    ): Promise<ListSalaryComponentsResponse> {
      const c = ensure(companyID);
      return { salary_components: c.salaryComponents.get(employeeID) ?? [] };
    },

    async setSalaryComponent(
      companyID,
      employeeID: string,
      req: SetSalaryComponentRequest,
    ): Promise<SetSalaryComponentResponse> {
      const c = ensure(companyID);
      if (!c.employees.some((e) => e.id === employeeID)) {
        throw new ApiError(404, "payrolldb: no such employee");
      }
      if ((req.effective_from ?? "").trim() === "") {
        throw new ApiError(
          400,
          "effective_from is required and must be YYYY-MM-DD: a salary component is effective-dated, and the pay run reads the window in force on the first day of the wage month",
        );
      }
      const type = (req.type ?? "").trim();
      const known = MOCK_PAYROLL_COMPONENT_TYPES.find((t) => t.code === type);
      if (!known) {
        throw new ApiError(
          400,
          `payroll: unknown salary component type: ${JSON.stringify(type)} — the type decides whether PF and ESI are contributed on this component (Code on Social Security s.2(88)), so it is a closed set`,
        );
      }
      const componentID = id("salary-component");
      const rows = c.salaryComponents.get(employeeID) ?? [];
      rows.push({
        id: componentID,
        effective_from: req.effective_from,
        effective_to: req.effective_to ?? "",
        code: req.code,
        type,
        amount: mockFixed4(req.amount),
        // From the SAME table the vocabulary is served from, so the flag on the
        // row and the flag in the picker cannot disagree here either.
        is_wages: known.is_wages,
        in_deeming_test: known.in_deeming_test,
      });
      c.salaryComponents.set(employeeID, rows);
      return { salary_component_id: componentID };
    },

    async listStatutoryElections(
      companyID,
      employeeID: string,
    ): Promise<ListStatutoryElectionsResponse> {
      const c = ensure(companyID);
      return {
        statutory_elections: c.statutoryElections.get(employeeID) ?? [],
      };
    },

    async setStatutoryElection(
      companyID,
      employeeID: string,
      req: SetStatutoryElectionRequest,
    ): Promise<SetStatutoryElectionResponse> {
      const c = ensure(companyID);
      if (!c.employees.some((e) => e.id === employeeID)) {
        throw new ApiError(404, "payrolldb: no such employee");
      }
      if ((req.effective_from ?? "").trim() === "") {
        throw new ApiError(
          400,
          "effective_from is required and must be YYYY-MM-DD: an election is effective-dated, and the pay run reads the one in force on the first day of the wage month",
        );
      }
      if ((req.pf_base ?? "").trim() === "") {
        // The one refusal in this whole payroll arm that is a REAL rule and not
        // a shape check: CEILING and ACTUAL differ for anybody paid above the
        // EPF wage ceiling, so an unstated base is a contribution figure nobody
        // chose. The mock refuses it because the server does.
        throw new ApiError(
          400,
          "pf_base is required and must be CEILING or ACTUAL: the two differ for anybody paid above the EPF wage ceiling, so there is no safe default",
        );
      }
      const electionID = id("statutory-election");
      const rows = c.statutoryElections.get(employeeID) ?? [];
      rows.push({
        id: electionID,
        effective_from: req.effective_from,
        effective_to: req.effective_to ?? "",
        pf_base: req.pf_base,
        pf_voluntary_base: mockFixed4(req.pf_voluntary_base ?? "0"),
        employer_matches_voluntary: req.employer_matches_voluntary ?? false,
        eps_member: req.eps_member ?? false,
        esi_applicable: req.esi_applicable ?? false,
        // STORED AS GIVEN. "" is "no election", which is a different fact from
        // "NEW" — see StatutoryElectionView.regime.
        regime: req.regime ?? "",
      });
      c.statutoryElections.set(employeeID, rows);
      return { statutory_election_id: electionID };
    },

    async payrollVocabulary(companyID): Promise<PayrollVocabularyResponse> {
      ensure(companyID);
      return {
        component_types: MOCK_PAYROLL_COMPONENT_TYPES.map((t) => ({ ...t })),
        pf_bases: ["CEILING", "ACTUAL"],
        regimes: ["NEW", "OLD"],
        default_regime: "NEW",
        age_bands: ["BELOW_60", "SENIOR", "SUPER_SENIOR"],
      };
    },

    async runPayroll(companyID, req: PayRunRequest): Promise<PayRunResponse> {
      const c = ensure(companyID);
      const month = (req.wage_month ?? "").trim();
      if (!/^\d{4}-(0[1-9]|1[0-2])$/.test(month)) {
        throw new ApiError(
          400,
          `month ${JSON.stringify(month)} is not a wage month; it must be YYYY-MM (e.g. 2026-07)`,
        );
      }
      const already = c.payRuns.get(month);
      if (already) {
        // ⛔ NOT AN ERROR AND NOT A FRESH POSTING. The server answers 201 with
        // `already_posted: true` and the FIRST run's identifiers, and the mock
        // must too — a screen that renders this as a new run tells an operator a
        // correction took effect when the stored run is the earlier one.
        return {
          pay_run_id: already.payRunID,
          voucher_id: already.voucherID,
          voucher_no: already.voucherNo,
          version_no: 1,
          status: "POSTED",
          already_posted: true,
        };
      }
      // Eligibility mirrors the server's: ACTIVE employees only, and each must
      // have a salary structure AND an election — cored refuses the whole run
      // otherwise, by name, and a mock that posted anyway would hide the single
      // most common payroll failure from every screen test.
      const eligible = c.employees.filter((e) => e.is_active);
      if (eligible.length === 0) {
        throw new ApiError(
          422,
          `payrolldb: no employee is eligible for the ${month} pay run (active, joined on or before the month ended, and not left before it began); a run with no payslips computes nothing and would post an empty voucher`,
        );
      }
      for (const e of eligible) {
        if ((c.statutoryElections.get(e.id) ?? []).length === 0) {
          throw new ApiError(
            422,
            `payrolldb: compute payslip for employee ${e.code}, wage month ${month}: no statutory election is in force for wage month ${month}; the PF contribution base and the tax regime are elections and there is no safe default for the first of them (CEILING and ACTUAL differ for anyone paid above the wage ceiling)`,
          );
        }
        if ((c.salaryComponents.get(e.id) ?? []).length === 0) {
          throw new ApiError(
            422,
            `payrolldb: compute payslip for employee ${e.code}, wage month ${month}: no salary structure is in force; there is nothing to pay and nothing to contribute on`,
          );
        }
      }
      const payRunID = id("pay-run");
      const voucherID = id("voucher");
      const voucherNo = String(c.payRuns.size + 1);
      c.payRuns.set(month, { payRunID, voucherID, voucherNo });
      return {
        pay_run_id: payRunID,
        voucher_id: voucherID,
        voucher_no: voucherNo,
        version_no: 1,
        status: "POSTED",
        already_posted: false,
      };
    },

    async payrollRegister(
      companyID,
      month: string,
    ): Promise<PayrollRegisterResponse> {
      const c = ensure(companyID);
      const run = c.payRuns.get(month);
      if (!run) {
        throw new ApiError(
          404,
          `payrolldb: no pay run for wage month ${month}`,
        );
      }
      const rows = c.employees
        .filter((e) => e.is_active)
        .map((e) => mockRegisterRow(e, c.salaryComponents.get(e.id) ?? []));
      return {
        pay_run_id: run.payRunID,
        wage_month: month,
        status: "POSTED",
        // ⛔ NAMED SO NOBODY MISTAKES THE MOCK'S ZEROS FOR A COMPUTATION. cored
        // serves the real ruleset ids here ("ss-2026-07"), and the versions are
        // ON the report because a payroll figure with no statement of which law
        // produced it cannot be checked by anybody who was not present.
        ss_ruleset_version: "mock-no-statutory-engine",
        it_ruleset_version: "mock-no-statutory-engine",
        rows,
        totals: mockRegisterTotals(rows),
      };
    },

    async payslip(
      companyID,
      month: string,
      employeeID: string,
    ): Promise<PayslipResponse> {
      const c = ensure(companyID);
      const run = c.payRuns.get(month);
      if (!run) {
        throw new ApiError(
          404,
          `payrolldb: no pay run for wage month ${month}`,
        );
      }
      const e = c.employees.find((x) => x.id === employeeID);
      if (!e) {
        // The SAME answer cored gives for another tenant's employee id, which
        // resolves through RLS to no rows: 404, and never that person's figures.
        throw new ApiError(404, "payrolldb: no payslip for that employee");
      }
      const components = c.salaryComponents.get(employeeID) ?? [];
      const gross = components.reduce(
        (sum, comp) => sum + Number(comp.amount),
        0,
      );
      return {
        pay_run_id: run.payRunID,
        wage_month: month,
        employee_id: e.id,
        employee_code: e.code,
        employee_name: e.name,
        days_paid: "0.0000",
        lop_days: "0.0000",
        gross: mockFixed4(String(gross)),
        deductions: "0.0000",
        net: mockFixed4(String(gross)),
        lines: components.map((comp) => ({
          code: comp.code,
          type: comp.type,
          tag: comp.is_wages ? "WAGES" : "EXCLUDED",
          direction: "EARNING",
          amount: comp.amount,
        })),
        // ⛔ THE ABSTENTION, SAID OUT LOUD ON THE PAYSLIP ITSELF. This is the
        // channel cored uses for a component whose rate has not been notified,
        // and it is the right one here: a blank where a number should be is a
        // question somebody will ask, and the honest answer is "this was not
        // computed", not "zero".
        deferrals: MOCK_PAYROLL_DEFERRALS.map((d) => ({ ...d })),
        ss_ruleset_version: "mock-no-statutory-engine",
        it_ruleset_version: "mock-no-statutory-engine",
      };
    },

    // ---- The three statutory FILES. The mock serves a NOTE, not a file.
    //
    // ⛔ IT MUST NOT PRODUCE ECR/ESI/Form-138 BYTES. Those layouts are the
    // portals' own and cored's payrollfile package is where they live, spelled
    // out record by record; a second hand-rolled copy here would be bytes a
    // screen test could pin and nobody validated — and the one thing worse than
    // an unvalidated statutory file is two of them that differ. What IS
    // exercised, which is all a screen can be asked to get right, is that the
    // download path carries the archive AND the disclosure headers.
    async payrollPFECR(companyID, month: string) {
      return mockStatutoryDownload(ensure(companyID), month, "ECR", "ECR.txt");
    },
    async payrollESIReturn(companyID, month: string) {
      return mockStatutoryDownload(
        ensure(companyID),
        month,
        "ESI monthly contribution return",
        "ESI.csv",
      );
    },
    async payrollForm138(companyID, req: Form138Request) {
      return mockStatutoryDownload(
        ensure(companyID),
        `${req.tax_year} ${req.quarter}`,
        "Form 138 Annexure I",
        "Form138.txt",
      );
    },

    // ---- T14: DATA (Alt+Y) — backup and restore.
    //
    // ⛔ THE MOCK DOES NOT FABRICATE AN ARCHIVE, and that is the honest shape
    // rather than a gap. A real archive is 62 tables of rows that Postgres
    // itself serialised with to_jsonb and that a restore feeds back through
    // jsonb_populate_record; a hand-rolled imitation here would be bytes no
    // restore has ever read, and a screen test pinned against it would prove
    // that the mock and the screen agree with each other and nothing more.
    //
    // What it DOES do is answer honestly, so the screen's failure path is the
    // thing under test: a backup hands back a plain-text note under the real
    // filename, and a restore REJECTS it — with the sentence the operator would
    // see — because the mock has no database to create a company in.
    async backupCompany(companyID) {
      const c = ensure(companyID);
      const note =
        `This is the in-memory MOCK client, not cored. No archive of ${c.name} ` +
        `(${c.id}) was produced: an archive is every row of 62 tables as ` +
        `Postgres itself serialised them, and a hand-rolled imitation here ` +
        `would be bytes no restore has ever read.`;
      return {
        blob: new Blob([note], { type: "text/plain" }),
        filename: "mock-company.tallybak.jsonl.gz",
        header: () => null,
      };
    },
    // ---- PER-COMPANY PRINT TEMPLATES (track C).
    //
    // ⛔ THIS ONE IS IMPLEMENTED RATHER THAN REFUSED, AND THE LINE IS WORTH
    // STATING. The mock's rule is HONEST, NOT HELPFUL: it replays domain logic
    // where the logic is genuinely reproducible (FIFO, group rollups) and refuses
    // everywhere else, and it must never fake a payload a real backend has never
    // produced. An override store is the reproducible case — it is one row per
    // (company, doc_kind), keyed by a unique constraint, with no derivation at
    // all — so an in-memory map behaves exactly as the server does.
    //
    // ✅ AND THE PARTS IT COULD NOT INVENT ARE READ FROM THE SERVER'S OWN
    // GENERATED ARTEFACT. `honours` and `defaults` are decisions belonging to the
    // Go renderers, and `print-template-surface.json` is emitted by
    // TestPrintTemplateSurfaceGolden by RENDERING each kind twice per field. So
    // these are not invented values that resemble the server's — they are the
    // server's, measured.
    //
    // ⛔ IT ALSO REFUSES WHAT THE SERVER REFUSES, which is the part a lazier mock
    // would skip. A mock more PERMISSIVE than the real backend is worse than no
    // mock: a screen developed against it offers a control that works in every
    // test and 400s in production. So an unknown kind is refused, and so is a
    // field the kind's renderer does not read.
    async getPrintTemplate(
      companyID: string,
      docKind: string,
    ): Promise<PrintTemplateResponse> {
      ensure(companyID);
      return mockPrintTemplateBody(
        docKind,
        printTemplates.get(mockPrintTemplateKey(companyID, docKind)),
      );
    },
    async savePrintTemplate(
      companyID: string,
      docKind: string,
      req: PrintTemplateRequest,
    ): Promise<PrintTemplateResponse> {
      ensure(companyID);
      const kind = mockPrintTemplateKind(docKind);
      mockPrintTemplateRefuseUnhonoured(kind, req);
      // A FULL RESTATEMENT, matching the route: an omitted field is stored as
      // empty, which means "fall back to the built-in default". Merging would
      // make an emptied box un-emptiable.
      const stored: PrintTemplateOverrides = {
        title: req.title ?? "",
        header_lines: req.header_lines ?? null,
        footer_lines: req.footer_lines ?? null,
        declaration: req.declaration ?? "",
        terms: req.terms ?? "",
        signature_label: req.signature_label ?? "",
        show_bank_details: req.show_bank_details ?? false,
        bank_lines: req.bank_lines ?? null,
      };
      printTemplates.set(mockPrintTemplateKey(companyID, docKind), stored);
      return mockPrintTemplateBody(docKind, stored);
    },
    async clearPrintTemplate(
      companyID: string,
      docKind: string,
    ): Promise<PrintTemplateResponse> {
      ensure(companyID);
      mockPrintTemplateKind(docKind);
      const key = mockPrintTemplateKey(companyID, docKind);
      const cleared = printTemplates.delete(key);
      // Idempotent, and honest about having found nothing — the server's contract.
      return { ...mockPrintTemplateBody(docKind, undefined), cleared };
    },

    async restoreCompany(_archive: Blob) {
      throw new ApiError(
        422,
        "This is the in-memory mock client. A restore CREATES a company from an " +
          "archive, and the mock has no database to create one in — run against " +
          "cored to restore.",
      );
    },

    // ---- OFFLINE SYNC + THE RECONCILIATION QUEUE (track D).
    //
    // ⛔ THE MOCK IS NOT TAUGHT TO SEQUENCE. Faking syncCommit would mean a
    // SECOND IMPLEMENTATION of the subtlest thing on the server — the atomic
    // per-(company, type, FY) numbering allocator, the (HLC, node) total order
    // that picks a conflict winner, per-intent idempotency, and the freeze rule —
    // and a test could then pin bytes no real backend ever produced. The one
    // honest answer is a refusal that says which gate to run instead.
    async syncCommit() {
      throw new ApiError(
        501,
        "the mock has no cloud sequencer: committing an outbox needs the real " +
          "numbering allocator, the (HLC, node) conflict order and the freeze rule, " +
          "and a second copy of those in TypeScript would be unvalidated. Run " +
          "against cored (pnpm verify:demo / the track-D drive) to exercise a commit.",
      );
    },

    // ⚠️ AN EMPTY QUEUE HERE IS ACCURATE, NOT A STAND-IN. The mock never refuses
    // an alter or a delete — it models no freeze rule and no stale-base conflict —
    // so nothing in this world has ever been parked, and reporting nothing is the
    // truth about it rather than a convenient blank. A test that needs entries
    // injects them by spreading this client and overriding the method, which is
    // the pattern TallyImportScreen.test.tsx documents.
    async reconciliationQueue() {
      return { entries: [] };
    },

    // No row can exist here, so both of these answer the way the server answers
    // for an id that names no open entry. ⛔ RETURNING A FABRICATED PARKED
    // VOUCHER WOULD BE THE ONE THING THE RULE FORBIDS — inventing a payload a
    // real backend never produced, which a screen test could then pin.
    async reconciliationPayload(_companyID: string, reconID: string) {
      throw new ApiError(
        404,
        `no open reconciliation entry with that id (${reconID}); the mock parks none ` +
          "because it refuses no alter or delete",
      );
    },
    // ---- YEAR-END CLOSURE (0089).
    //
    // ⛔ THE REFUSALS MIRROR THE SERVER'S, INCLUDING THEIR CODES. D116 closed a
    // defect that existed only because this mock and cored disagreed about the
    // TEXT of a refusal and nothing compared them; the screens were built
    // against the mock and shown something else. Both 409s here carry the same
    // `code` the server sends, because DataScreen switches on the code.
    async booksClosure(_companyID: string): Promise<BooksClosure> {
      return mockClosure();
    },
    async closeBooks(
      _companyID: string,
      closedThrough: string,
    ): Promise<BooksClosure> {
      if (!/^\d{4}-\d{2}-\d{2}$/.test(closedThrough)) {
        throw new ApiError(
          400,
          "closed_through must be a date as YYYY-MM-DD, and it is the LAST day of the " +
            "period being closed — for the financial year 2026-27 that is 2027-03-31, " +
            "never 2027-04-01",
        );
      }
      if (mockClosedThrough !== null && closedThrough <= mockClosedThrough) {
        throw new ApiError(
          409,
          `the books are already closed through a later date (${mockClosedThrough}). ` +
            "Closing only ever moves the lock FORWARD; to unlock a period that is " +
            "already closed, re-open the books first — that is a separate, deliberate act",
          { code: "BOOKS_ALREADY_CLOSED_LATER" },
        );
      }
      mockClosedThrough = closedThrough;
      mockClosedAt = "2026-09-01T00:00:00Z";
      mockClosedBy = "mock-owner";
      return mockClosure();
    },
    async reopenBooks(_companyID: string): Promise<BooksClosure> {
      if (mockClosedThrough === null) {
        throw new ApiError(409, "no period is closed, so there is nothing to re-open", {
          code: "NOTHING_CLOSED",
        });
      }
      mockClosedThrough = null;
      return mockClosure();
    },

    async resolveReconciliation(_companyID: string, reconID: string) {
      throw new ApiError(
        404,
        `no open reconciliation entry with that id (${reconID}); the mock parks none ` +
          "because it refuses no alter or delete",
      );
    },

    // ---- THE AGENT PLANE (W2, D121 §8) --------------------------------
    //
    // ⚠️ THE MOCK HAS NO AGENT, ONLY THE AGENT'S TABLES. It cannot run a loop
    // (there is no model and no preview here), so a run started against it
    // either answers 503 AGENT_UNCONFIGURED — the shipped server default — or,
    // with `agent.model: true`, records a `running` run and its run_started
    // event and then sits there. What it DOES model faithfully is the decision
    // contract the inbox is built on: one decision per approval (409), the
    // 72-hour window (409 APPROVAL_EXPIRED, recorded as expired), ask_back
    // without a note and an edit beside a reject (422). It never posts a
    // voucher on approve, because the real server does not either: the commit
    // is the agent poller's, afterwards.
    async startAgentRun(_companyID: string, req: StartAgentRunRequest) {
      const msg = "no agent model is configured on this server (set LLM_PROVIDER and friends)";
      if (!agentState.model) {
        throw new ApiError(503, msg, { code: AGENT_UNCONFIGURED, error: msg });
      }
      if (!(req.instruction ?? "").trim()) {
        throw new ApiError(422, "agent: invalid request: an instruction is required");
      }
      const now = new Date().toISOString();
      const run: AgentRun = {
        id: `mock-run-${agentState.runs.length + 1}`,
        created_by: "mock-user",
        agent_id: "khata-agent",
        channel: "panel",
        instruction: req.instruction,
        inputs: req.inputs ?? [],
        delegation: {
          acl_keys: req.delegate ?? [],
          ...(req.max_amount_paise !== undefined ? { max_amount_paise: req.max_amount_paise } : {}),
          expires_at: now,
          step_up: false,
        },
        status: "running",
        attempts: 0,
        created_at: now,
        updated_at: now,
      };
      agentState.runs.unshift(run);
      agentState.events.set(run.id, [
        {
          seq: 1,
          type: "RUN_STARTED",
          kind: "run_started",
          at: now,
          actor_kind: "user",
          actor_id: "mock-user",
          data: { channel: "panel" },
          hash: "0".repeat(64),
        },
      ]);
      return { run };
    },
    async listAgentRuns(_companyID: string, limit?: number) {
      const n = limit && limit > 0 ? limit : 50;
      return { runs: agentState.runs.slice(0, n).map((r) => ({ ...r })) };
    },
    async getAgentRun(_companyID: string, runID: string) {
      const run = agentState.runs.find((r) => r.id === runID);
      if (!run) throw new ApiError(404, "not found");
      return {
        run: {
          ...run,
          approvals: agentState.approvals.filter((a) => a.run_id === runID).map((a) => ({ ...a })),
        },
      };
    },
    async agentRunEvents(_companyID: string, runID: string, after?: number) {
      const run = agentState.runs.find((r) => r.id === runID);
      if (!run) throw new ApiError(404, "not found");
      const all = agentState.events.get(runID) ?? [];
      const events = all.filter((e) => e.seq > (after ?? 0));
      const last = events[events.length - 1];
      return {
        run_id: runID,
        status: run.status,
        finished: runFinished(run.status),
        next_after: last ? last.seq : (after ?? 0),
        events,
      };
    },
    async cancelAgentRun(_companyID: string, runID: string) {
      const run = agentState.runs.find((r) => r.id === runID);
      if (!run) throw new ApiError(404, "not found");
      if (!runFinished(run.status)) {
        run.status = "cancelled";
        run.finished_at = new Date().toISOString();
      }
      return { run: { ...run } };
    },
    async listAgentApprovals(
      _companyID: string,
      query?: { state?: "pending" | "all"; run_id?: string },
    ) {
      const rows = agentState.approvals.filter(
        (a) =>
          (query?.state !== "pending" || a.state === "pending") &&
          (!query?.run_id || a.run_id === query.run_id),
      );
      return { approvals: rows.map((a) => ({ ...a })) };
    },
    async getAgentApproval(_companyID: string, approvalID: string) {
      const a = agentState.approvals.find((x) => x.id === approvalID);
      if (!a) throw new ApiError(404, "not found");
      return { approval: { ...a } };
    },
    async decideAgentApproval(
      _companyID: string,
      approvalID: string,
      req: AgentDecisionRequest,
    ) {
      const a = agentState.approvals.find((x) => x.id === approvalID);
      if (!a) throw new ApiError(404, "not found");
      if (req.decision !== "approve" && req.decision !== "reject" && req.decision !== "ask_back") {
        throw new ApiError(422, `agent: invalid request: unknown decision ${String(req.decision)}`);
      }
      if (req.decision === "ask_back" && !(req.note ?? "").trim()) {
        throw new ApiError(422, "agent: invalid request: ask_back needs a note");
      }
      if (req.edited_body !== undefined && req.decision !== "approve") {
        throw new ApiError(422, "agent: invalid request: an edited body is only meaningful with approve");
      }
      if (a.state !== "pending") {
        throw new ApiError(409, "this approval has already been decided", {
          code: "ALREADY_DECIDED",
          approval: { ...a },
        });
      }
      const now = new Date();
      const expired = now.getTime() > Date.parse(a.deadline_at);
      agentState.decisions.push({ approvalID, ...req });
      a.decision = {
        id: `mock-decision-${approvalID}`,
        decision: expired ? "expired" : req.decision,
        ...(req.edited_body && !expired ? { edited_args: req.edited_body } : {}),
        ...(req.note && !expired ? { note: req.note } : {}),
        decided_webauthn: false,
        decided_at: now.toISOString(),
      };
      if (expired) {
        a.state = "expired";
        throw new ApiError(
          409,
          "the approval window closed before this decision; it has been recorded as expired",
          { code: "APPROVAL_EXPIRED", approval: { ...a } },
        );
      }
      a.state =
        req.decision === "approve" ? "approved" : req.decision === "reject" ? "rejected" : "ask_back";
      return { approval: { ...a } };
    },

    // ---- W2 breadth ------------------------------------------------------
    async previewAgentEdit(_companyID: string, approvalID: string, _editedBody: Record<string, unknown>) {
      // ⚠️ THE MOCK HAS NO HANDLER TO DRY-RUN, so it answers with the approval's
      // own recorded preview — never an effect computed here (the "no money on
      // this side" rule). A screen test that needs a refusal seeds one.
      const a = agentState.approvals.find((x) => x.id === approvalID);
      if (!a) throw new ApiError(404, "not found");
      if (a.state !== "pending") {
        throw new ApiError(409, "this approval has already been decided", { code: "ALREADY_DECIDED" });
      }
      return { preview: { status: a.preview?.status ?? 201, effect: a.preview?.effect ?? null } };
    },
    async listAgentAutonomy(_companyID: string) {
      if (!agentState.owner) {
        throw new ApiError(403, "only an Owner may read or change the agent's autonomy");
      }
      return { rows: agentState.autonomy.map((r) => ({ ...r })), default_level: 1, keys: MOCK_AUTONOMY_KEYS.slice() };
    },
    async setAgentAutonomy(_companyID: string, req: SetAgentAutonomyRequest) {
      if (!agentState.owner) {
        throw new ApiError(403, "only an Owner may read or change the agent's autonomy");
      }
      if (!MOCK_AUTONOMY_KEYS.includes(req.acl_key)) {
        throw new ApiError(422, `agent: invalid request: "${req.acl_key}" is not an action an agent can ever commit`);
      }
      if (!Number.isInteger(req.level) || req.level < 0 || req.level > 3) {
        throw new ApiError(422, "agent: invalid request: level must be 0..3");
      }
      const same = (r: AgentAutonomyRow) =>
        r.acl_key === req.acl_key && (r.user_id ?? "") === (req.user_id ?? "") &&
        (r.amount_max_paise ?? null) === (req.amount_max_paise ?? null);
      const now = new Date().toISOString();
      const existing = agentState.autonomy.find(same);
      if (existing) {
        existing.level = req.level;
        existing.updated_at = now;
      } else {
        agentState.autonomy.push({
          id: `mock-autonomy-${agentState.autonomy.length + 1}`,
          acl_key: req.acl_key,
          level: req.level,
          ...(req.amount_max_paise !== undefined ? { amount_max_paise: req.amount_max_paise } : {}),
          ...(req.user_id ? { user_id: req.user_id } : {}),
          set_by: "mock-owner",
          updated_at: now,
        });
      }
      return { rows: agentState.autonomy.map((r) => ({ ...r })), default_level: 1, keys: MOCK_AUTONOMY_KEYS.slice() };
    },
    async verifyAgentRun(_companyID: string, runID: string) {
      const evs = agentState.events.get(runID) ?? [];
      return { run_id: runID, events: evs.length, ok: true };
    },
    async verifyAgentAnchors(_companyID: string) {
      return { anchors: 0, ok: true };
    },
    async unblockLedger(companyID: string, ledgerID: string) {
      const l = ensure(companyID).ledgers.find((x) => x.id === ledgerID);
      if (!l) throw new ApiError(404, "ledger not found");
      if (!l.blocked) {
        throw new ApiError(409, "this ledger is not blocked", { code: "NOT_BLOCKED" });
      }
      l.blocked = false;
      l.blockedReason = undefined;
      return { ledger_id: ledgerID, blocked: false as const };
    },
  };
}

// ---------------------------------------------------------------------------
// The payroll mock's helpers. See MockCompany.employees for why none of them
// computes a statutory figure.
// ---------------------------------------------------------------------------

/**
 * The salary-component vocabulary, mirroring `payroll.ComponentTypes()` and its
 * s.2(88) classification.
 *
 * ⚠️ THIS IS A SECOND COPY OF A LEGAL CLASSIFICATION AND THE RISK IS REAL — it
 * is here only because a mock has no way to call Go. It is the reason the LIVE
 * client fetches the vocabulary from the server instead of holding a table like
 * this one: a form built from a hardcoded list can offer a type the server
 * refuses, and `is_wages` decides whether PF is contributed at all. If cored's
 * vocabulary changes, THIS is the copy that goes stale, and the symptom is a
 * hermetic test that passes while the browser does not.
 */
const MOCK_PAYROLL_COMPONENT_TYPES: readonly {
  code: string;
  is_wages: boolean;
  in_deeming_test: boolean;
}[] = [
  { code: "BASIC", is_wages: true, in_deeming_test: false },
  { code: "DA", is_wages: true, in_deeming_test: false },
  { code: "RETAINING_ALLOWANCE", is_wages: true, in_deeming_test: false },
  { code: "SPECIAL_ALLOWANCE", is_wages: true, in_deeming_test: false },
  { code: "OTHER_ALLOWANCE", is_wages: true, in_deeming_test: false },
  { code: "BONUS", is_wages: false, in_deeming_test: true },
  { code: "HOUSE_ACCOMMODATION_VALUE", is_wages: false, in_deeming_test: true },
  { code: "EMPLOYER_PF_CONTRIBUTION", is_wages: false, in_deeming_test: true },
  { code: "CONVEYANCE_ALLOWANCE", is_wages: false, in_deeming_test: true },
  { code: "SPECIAL_EXPENSES", is_wages: false, in_deeming_test: true },
  { code: "HRA", is_wages: false, in_deeming_test: true },
  { code: "AWARD_OR_SETTLEMENT", is_wages: false, in_deeming_test: true },
  { code: "OVERTIME_ALLOWANCE", is_wages: false, in_deeming_test: true },
  { code: "COMMISSION", is_wages: false, in_deeming_test: true },
  { code: "GRATUITY", is_wages: false, in_deeming_test: false },
  {
    code: "RETRENCHMENT_COMPENSATION",
    is_wages: false,
    in_deeming_test: false,
  },
  { code: "EX_GRATIA", is_wages: false, in_deeming_test: false },
];

/** What this mock deliberately did not compute, on the payslip that lacks it. */
const MOCK_PAYROLL_DEFERRALS: readonly PayslipDeferral[] = [
  {
    component: "EPF / EPS / ESI / TDS",
    reason:
      "This is the in-memory mock client, which implements no statutory engine. " +
      "Every contribution and tax figure here is 0.0000 as an ABSTENTION, not as a " +
      "computation — the real figures come from cored's internal/payroll, whose " +
      "rules carry the wage ceilings, the s.2(88) classification and s.392.",
    citation: "frontend/src/api/mock.ts — see MockCompany.employees",
  },
];

/** Render a decimal-ish string at the storage scale, as every payroll field is. */
function mockFixed4(value: string): string {
  const n = Number(value);
  return Number.isFinite(n) ? n.toFixed(4) : "0.0000";
}

/** One register row: gross from the stored components, everything else zero. */
function mockRegisterRow(
  e: EmployeeView,
  components: readonly SalaryComponentView[],
): PayrollRegisterRow {
  const gross = components.reduce((sum, c) => sum + Number(c.amount), 0);
  return {
    employee_id: e.id,
    employee_code: e.code,
    employee_name: e.name,
    days_paid: "0.0000",
    gross: mockFixed4(String(gross)),
    epf_employee: "0.0000",
    esi_employee: "0.0000",
    tds: "0.0000",
    deductions: "0.0000",
    net: mockFixed4(String(gross)),
    epf_employer: "0.0000",
    eps_employer: "0.0000",
    esi_employer: "0.0000",
  };
}

/** The totals row — a SUM of the rows above and nothing else. */
function mockRegisterTotals(
  rows: readonly PayrollRegisterRow[],
): PayrollRegisterRow {
  const sum = (pick: (r: PayrollRegisterRow) => string): string =>
    mockFixed4(String(rows.reduce((t, r) => t + Number(pick(r)), 0)));
  return {
    days_paid: sum((r) => r.days_paid),
    gross: sum((r) => r.gross),
    epf_employee: sum((r) => r.epf_employee),
    esi_employee: sum((r) => r.esi_employee),
    tds: sum((r) => r.tds),
    deductions: sum((r) => r.deductions),
    net: sum((r) => r.net),
    epf_employer: sum((r) => r.epf_employer),
    eps_employer: sum((r) => r.eps_employer),
    esi_employer: sum((r) => r.esi_employer),
  };
}

/**
 * A stand-in statutory download: a note, under the real filename, WITH both
 * disclosure headers.
 *
 * The headers are the part that matters. cored sets `X-Statutory-File` and
 * `X-Statutory-Disclosure` on every one of these routes, and a screen's only job
 * is to put them in front of the operator — so the mock carries them, and a
 * screen test that forgets to render the disclosure fails here rather than in
 * production.
 */
function mockStatutoryDownload(
  c: MockCompany,
  period: string,
  what: string,
  fileName: string,
): StatutoryFileDownload {
  const note =
    `This is the in-memory MOCK client, not cored. No ${what} was produced for ` +
    `${period} (company ${c.id}): the layout lives in cored's payrollfile package, ` +
    `record by record, and a second hand-rolled copy here would be unvalidated ` +
    `bytes a test could pin.`;
  return {
    blob: new Blob([note], { type: "text/plain" }),
    filename: fileName.replace(/\.[^.]+$/, "") + ".zip",
    statutoryFile: fileName,
    disclosure:
      "NOT validator-verified; see the .README.txt inside this archive",
  };
}

// ---------------------------------------------------------------------------
// PER-COMPANY PRINT TEMPLATES (track C) — the mock's override store.
// ---------------------------------------------------------------------------

function mockPrintTemplateKey(companyID: string, docKind: string): string {
  return `${companyID} ${docKind}`;
}

/**
 * Refuse a document kind the server does not print, with the server's own 400.
 *
 * ⛔ THE MOCK MUST NOT BE MORE PERMISSIVE THAN THE BACKEND. A screen developed
 * against a mock that accepted "invoice" (the plausible wrong answer — everybody
 * calls it that, and the stored vocabulary spells it "tax-invoice") would pass
 * every test and 400 in production.
 */
function mockPrintTemplateKind(docKind: string): string {
  if (!printTemplateSurfaceFor(docKind)) {
    throw new ApiError(
      400,
      `unknown document kind ${docKind}: this system prints ` +
        PRINT_TEMPLATE_KINDS.map((k) => k.doc_kind).join(", "),
    );
  }
  return docKind;
}

/**
 * Refuse a field this kind's renderer does not read, when it carries CONTENT.
 *
 * ⛔ WHY REFUSING IS THE HONEST BEHAVIOUR. `RenderReport` reads three of the
 * seven overridable fields. Storing a declaration for the `report` kind would be
 * accepted, stored and never printed — the operator types a certification, the
 * screen says saved, and no statement ever carries it. Nothing errors.
 *
 * ⚠️ PRESENCE IS NOT CONTENT. A form that submits its whole shape with the unused
 * boxes empty is behaving correctly and must not be refused; only a typed value
 * is.
 */
function mockPrintTemplateRefuseUnhonoured(
  docKind: string,
  req: PrintTemplateRequest,
): void {
  const surface = printTemplateSurfaceFor(docKind);
  if (!surface) return;
  const honours = (f: PrintTemplateField) => surface.honours.includes(f);
  const refuse = (name: string): never => {
    throw new ApiError(
      400,
      `the ${docKind} layout does not print ${name}, so this would be stored ` +
        `and never appear. Fields this document honours: ${surface.honours.join(", ")}`,
    );
  };
  const texts: [PrintTemplateField, string, string | undefined][] = [
    ["title", "title", req.title],
    ["declaration", "declaration", req.declaration],
    ["terms", "terms", req.terms],
    ["signature_label", "signature_label", req.signature_label],
  ];
  for (const [field, name, value] of texts) {
    if ((value ?? "").trim() !== "" && !honours(field)) refuse(name);
  }
  const lines: [PrintTemplateField, string, string[] | undefined][] = [
    ["header_lines", "header_lines", req.header_lines],
    ["footer_lines", "footer_lines", req.footer_lines],
    ["bank_details", "bank_lines", req.bank_lines],
  ];
  for (const [field, name, value] of lines) {
    if ((value?.length ?? 0) > 0 && !honours(field)) refuse(name);
  }
  if (req.show_bank_details && !honours("bank_details")) refuse("bank details");
}

/** The zero value, which is what an ABSENT row reads as. */
function mockPrintTemplateZero(): PrintTemplateOverrides {
  return {
    title: "",
    header_lines: null,
    footer_lines: null,
    declaration: "",
    terms: "",
    signature_label: "",
    show_bank_details: false,
    bank_lines: null,
  };
}

/**
 * Assemble the response the same way the Go handler does.
 *
 * `honours` and `defaults` come from the GENERATED artefact rather than from
 * anything this file decides — they are the Go renderers' answers, measured by
 * rendering, so the mock cannot drift from them.
 */
function mockPrintTemplateBody(
  docKind: string,
  stored: PrintTemplateOverrides | undefined,
): PrintTemplateResponse {
  const kind = mockPrintTemplateKind(docKind);
  const surface = printTemplateSurfaceFor(kind);
  if (!surface) {
    throw new ApiError(400, `unknown document kind ${docKind}`);
  }
  return {
    doc_kind: kind,
    customised: stored !== undefined,
    honours: surface.honours,
    defaults: surface.defaults,
    default_title_is_the_documents_own_name:
      surface.default_title_is_the_documents_own_name,
    // Blank everywhere means "the built-in default prints", and that is the
    // ordinary case for most companies forever.
    overrides: stored ?? mockPrintTemplateZero(),
  };
}

// ---------------------------------------------------------------------------
// T12 — GSTR-9 helpers
// ---------------------------------------------------------------------------

/** An all-zero tax triple, in the fixed-4 spelling every money value uses. */
function zeroTax9(): GSTTax {
  return { camt: "0.0000", samt: "0.0000", iamt: "0.0000" };
}

/**
 * The calendar year an Indian financial year STARTS in, or null when [from, to]
 * is not one.
 *
 * ⛔ IT IS STRICT ON PURPOSE, exactly as cored's financialYear() is. There is no
 * GSTR-9 for an arbitrary window, and a mock that accepted one would let a screen
 * ship a control the server refuses.
 */
function financialYearOf(from: string, to: string): number | null {
  const m = /^(\d{4})-04-01$/.exec(from);
  if (!m) return null;
  const y = Number(m[1]);
  return to === `${y + 1}-03-31` ? y : null;
}

/** The inclusive bounds and FP of month `n` (0-based) of the year from April. */
function monthWindowOf(
  startYear: number,
  n: number,
): { from: string; to: string; fp: string } {
  // Month arithmetic in UTC so a developer machine's timezone cannot shift a
  // boundary — a month that starts a day early silently moves vouchers between
  // periods, which is the one thing this grid must never do.
  const start = new Date(Date.UTC(startYear, 3 + n, 1));
  const end = new Date(Date.UTC(start.getUTCFullYear(), start.getUTCMonth() + 1, 0));
  const iso = (d: Date) => d.toISOString().slice(0, 10);
  const mm = String(start.getUTCMonth() + 1).padStart(2, "0");
  return { from: iso(start), to: iso(end), fp: `${mm}${start.getUTCFullYear()}` };
}

/**
 * The statutory table list, with the statuses cored gives them.
 *
 * ⚠️ ELEVEN OF THE FOURTEEN ARE NOT_DERIVED, and the mock says so rather than
 * emitting empty derived tables: a screen built against a mock where everything
 * derives would have no rendering for the case that is actually normal.
 */
function gstr9Tables(): GSTR9Table[] {
  const nd = (no: string, title: string, reason: string): GSTR9Table => ({
    no,
    title,
    status: "NOT_DERIVED",
    reason,
  });
  return [
    {
      no: "4",
      title: "Details of advances and outward supplies on which tax is payable",
      status: "PARTIAL",
      reason:
        "The B2B and B2C rows come from the year's twelve GSTR-1 returns and are net of " +
        "credit and debit notes. Four statutory rows below cannot be produced from these books.",
      rows: [
        {
          label: "4A-4B",
          title:
            "Supplies to unregistered and registered persons (B2C and B2B), net of credit and debit notes",
          txval: "0.0000",
          tax: zeroTax9(),
        },
      ],
      omitted: [
        {
          label: "4C",
          title: "Exports on payment of tax",
          reason: "these books carry no export, SEZ or deemed-export marker on a supply.",
        },
      ],
      total_suppressed: true,
      total_suppressed_reason:
        "4N totals rows 4A to 4L, and five of them are not derived here. A total over a set " +
        "with known-missing members is not a total — it is an UNDERSTATEMENT that foots.",
    },
    nd(
      "5",
      "Outward supplies on which tax is not payable",
      "these books carry no export, SEZ, deemed-export, nil-rated, exempt or non-GST marker.",
    ),
    {
      no: "6",
      title: "Details of ITC availed as declared in returns filed during the year",
      status: "PARTIAL",
      reason:
        "6A is the year's GSTR-3B table 4(A). The statutory breakdown below it cannot be reproduced.",
      rows: [
        { label: "6A", title: "Total ITC availed as per GSTR-3B table 4(A)", tax: zeroTax9() },
      ],
      omitted: [
        {
          label: "6B",
          title: "Inputs, capital goods and input services, separately",
          reason:
            "the input / capital-goods / input-services split is not modelled.",
        },
      ],
      total_suppressed: true,
      total_suppressed_reason:
        "6J is meant to be ZERO on a correct return. Emitting it from a breakdown that is not " +
        "derived would print a zero meaning 'nothing was checked' in the one cell whose zero " +
        "means 'everything ties'.",
    },
    nd(
      "7",
      "ITC reversed and ineligible ITC",
      "the twelve GSTR-3Bs behind this year were generated with NO inward statement, so table " +
        "4(B) is empty in every one of them. A zero here would say the taxpayer reversed nothing.",
    ),
    nd(
      "8",
      "Other ITC-related information (GSTR-2A comparison)",
      "the department's own annual GSTR-2A is not held by this build.",
    ),
    nd(
      "9",
      "Tax paid as declared in returns filed during the year",
      "there is no electronic cash or credit ledger and no GST challan register here.",
    ),
    nd(
      "10 & 11",
      "Prior-year supplies declared in the next year's April-September returns",
      "those returns are outside the window asked for, and amendments are not modelled.",
    ),
    nd(
      "12 & 13",
      "Prior-year ITC reversed or availed in the next year's April-September returns",
      "those returns are outside the window asked for, and amendments are not modelled.",
    ),
    nd(
      "14",
      "Differential tax paid on account of tables 10 and 11",
      "those returns are outside the window asked for, and what was actually PAID is not known.",
    ),
    nd(
      "15",
      "Particulars of demands and refunds",
      "demands, refunds and their status are departmental data that never enter these books.",
    ),
    nd(
      "16",
      "Composition supplies, deemed supply under s.143, goods sent on approval",
      "there is no job-work register and no approval-basis dispatch tracking.",
    ),
    { no: "17", title: "HSN-wise summary of outward supplies", status: "DERIVED", rows: [] },
    nd(
      "18",
      "HSN-wise summary of inward supplies",
      "per-line GST detail IS recorded for purchases, but nothing rolls it up today.",
    ),
    nd(
      "19",
      "Late fee payable and paid",
      "nothing here records WHEN a return was filed, and late fee is a function of the delay.",
    ),
  ];
}
