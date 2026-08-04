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
  BankReconLine,
  SetBankDateRequest,
  SetBankDateResponse,
  VoucherVersionView,
} from "./types";
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
export function createMockClient(opts: MockClientOptions = {}): CoredClient {
  const companies = new Map<string, MockCompany>();

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
        views: new Map(),
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
      views: new Map(),
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

  /** Map a request's bill allocations into BillAllocationViews (canonical D/C). */
  function billViews(req: VoucherRequest): BillAllocationView[] {
    return (req.bill_allocations ?? []).map((b) => ({
      ledger_id: b.ledger_id,
      ref_name: b.ref_name,
      ref_type: b.ref_type,
      dr_cr: b.dr_cr.trim().toUpperCase().startsWith("D") ? "D" : "C",
      amount: parseFloat(b.amount || "0").toFixed(4),
      due_date: b.due_date ?? "",
    }));
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

  /** Title-case class label ("sales" -> "Sales") matching the GET payload. */
  function classLabel(cls: VoucherClass): string {
    return cls.charAt(0).toUpperCase() + cls.slice(1);
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
      class: cls.toUpperCase(),
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
      bill_allocations: billViews(req),
      inventory: inventoryViews(c, req),
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
    entry.class = cls.toUpperCase();
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
      bill_allocations: billViews(req),
      inventory: inventoryViews(c, req),
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

    async getOutstandings(
      companyID,
      query?: OutstandingsQuery,
    ): Promise<OutstandingsResponse> {
      ensure(companyID);
      return {
        kind: query?.kind ?? "RECEIVABLE",
        as_on: query?.as_on ?? "",
        parties: [],
        slab_totals: [],
        total: "0.0000",
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
  };
}
