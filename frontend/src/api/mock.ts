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
  ListLedgersResponse,
  SetLedgerOpeningRequest,
  SetLedgerOpeningResponse,
  VoucherHistoryResponse,
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

let idCounter = 0;
function id(prefix: string): string {
  idCounter += 1;
  // Deterministic, UUID-shaped-enough id for tests.
  return `${prefix}-${idCounter.toString().padStart(12, "0")}`;
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
    stateCode?: string;
    gstin?: string;
    registrationType?: string;
    maintainBillByBill?: boolean;
    openingBalance?: Money;
    openingDC?: string;
    isSystem?: boolean;
  }[];
  vouchers: DayBookEntry[];
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
        vouchers: [],
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
        {
          id: "ledger-acme",
          name: "Acme Traders",
          group: "Sundry Debtors",
          stateCode: "29",
        },
      ],
      vouchers: [],
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
  function summarize(c: MockCompany, req: VoucherRequest): {
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
  function deriveGst(c: MockCompany, req: VoucherRequest): {
    taxable: Money;
    cgst: Money;
    sgst: Money;
    igst: Money;
  } {
    const gst = req.gst;
    const line = gst?.lines?.[0];
    if (!gst || !line) {
      return { taxable: "0.0000", cgst: "0.0000", sgst: "0.0000", igst: "0.0000" };
    }
    const supplierState =
      (gst.supplier_state_code ?? "").trim() !== ""
        ? (gst.supplier_state_code as string)
        : c.stateCode;
    const split = deriveGstSplit(
      line.taxable_value,
      line.gst_rate_pct,
      supplierState,
      gst.place_of_supply_state_code ?? "",
    );
    return {
      taxable: parseFloat(line.taxable_value || "0").toFixed(4),
      cgst: split.cgst,
      sgst: split.sgst,
      igst: split.igst,
    };
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
  function allocateNumber(c: MockCompany, cls: string, finYear: number): number {
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
    const { postings, postingViews, debit, credit } = summarize(c, req);
    const gst = deriveGst(c, req);
    const voucherID = id("voucher");
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
    async createCompany(req: CreateCompanyRequest): Promise<CreateCompanyResponse> {
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

    async createGroup(_c, _req: CreateGroupRequest): Promise<CreateGroupResponse> {
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
          gstin: l.gstin ?? "",
          state_code: l.stateCode ?? "",
          registration_type: l.registrationType ?? "UNKNOWN",
          is_system: l.isSystem ?? false,
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
      _c,
      _req: CreateStockItemRequest,
    ): Promise<CreateStockItemResponse> {
      return { stock_item_id: id("stock") };
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

    async getVoucher(companyID, voucherID): Promise<VoucherView> {
      const c = ensure(companyID);
      const view = c.views.get(voucherID);
      if (!view) {
        // Mirror the live GET: unknown id OR a deleted head row -> 404.
        throw new ApiError(404, "voucher not found", { error: "voucher not found" });
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

    async getTrialBalance(companyID): Promise<TrialBalanceResponse> {
      const c = ensure(companyID);
      const acc = new Map<string, { debit: Money; credit: Money; group: string }>();
      for (const l of c.ledgers) {
        acc.set(l.name, { debit: "0.0000", credit: "0.0000", group: l.group });
      }
      for (const v of c.vouchers) {
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

    async getBalanceSheet(companyID): Promise<BalanceSheetResponse> {
      ensure(companyID);
      return {
        assets: { title: "Assets", nodes: [], total: "0.0000" },
        liabilities: { title: "Liabilities", nodes: [], total: "0.0000" },
        net_profit: "0.0000",
        balanced: true,
      };
    },

    async getProfitLoss(companyID): Promise<ProfitLossResponse> {
      ensure(companyID);
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

    async generateGSTR1(
      companyID,
      query: GSTR1Query,
    ): Promise<GSTR1Response> {
      ensure(companyID);
      const fp = query.from.slice(0, 7).replace("-", "");
      const base = { gstin: query.gstin, fp, hsn: { data: [] } };
      if (query.cgst_ledger_id || query.sgst_ledger_id || query.igst_ledger_id) {
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
      const level = resolveLevel(c, req.security_level_id, req.security_level_name);
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
        if (m.security_level_id === level.id) m.security_level_name = level.name;
      }
      for (const iv of c.invites) {
        if (iv.security_level_id === level.id) iv.security_level_name = level.name;
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
