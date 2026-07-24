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
} from "./types";
import type { QueryRequest, QueryResult } from "./query";
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
  ledgers: { id: string; name: string; group: string; stateCode?: string }[];
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
        ledgers: [],
        vouchers: [],
        views: new Map(),
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
      };
    },

    async createGroup(_c, _req: CreateGroupRequest): Promise<CreateGroupResponse> {
      return { group_id: id("group") };
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
