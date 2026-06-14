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
import type {
  BalanceSheetResponse,
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
} from "./types";

let idCounter = 0;
function id(prefix: string): string {
  idCounter += 1;
  // Deterministic, UUID-shaped-enough id for tests.
  return `${prefix}-${idCounter.toString().padStart(12, "0")}`;
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
  ledgers: { id: string; name: string; group: string }[];
  vouchers: DayBookEntry[];
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
      c = { id: companyID, name: "Mock Co", ledgers: [], vouchers: [] };
      companies.set(companyID, c);
    }
    return c;
  }

  if (opts.seed !== false) {
    const demoID = "company-demo";
    const demo: MockCompany = {
      id: demoID,
      name: "Demo Traders",
      ledgers: [
        { id: "ledger-cash", name: "Cash", group: "Cash-in-Hand" },
        { id: "ledger-sales", name: "Sales", group: "Sales Accounts" },
        { id: "ledger-acme", name: "Acme Traders", group: "Sundry Debtors" },
      ],
      vouchers: [],
    };
    companies.set(demoID, demo);
  }

  /** Map a request's postings into day-book postings + Dr/Cr totals. */
  function summarize(c: MockCompany, req: VoucherRequest): {
    postings: DayBookPosting[];
    debit: Money;
    credit: Money;
  } {
    let debit: Money = "0.0000";
    let credit: Money = "0.0000";
    const postings = (req.postings ?? []).map((p) => {
      // Accept both the wire ("D"/"C") and domain ("DR"/"CR") conventions.
      const dc = p.dr_cr.trim().toUpperCase();
      if (dc.startsWith("D")) debit = addMoney(debit, p.amount);
      else credit = addMoney(credit, p.amount);
      const ledger = c.ledgers.find((l) => l.id === p.ledger_id);
      return {
        ledger: ledger?.name ?? p.ledger_id,
        dr_cr: dc,
        amount: parseFloat(p.amount || "0").toFixed(4),
      };
    });
    return { postings, debit, credit };
  }

  function recordVoucher(
    companyID: string,
    cls: VoucherClass,
    req: VoucherRequest,
  ): VoucherResponse {
    const c = ensure(companyID);
    const { postings, debit, credit } = summarize(c, req);
    const voucherID = id("voucher");
    const entry: DayBookEntry = {
      voucher_id: voucherID,
      version_no: 1,
      op: "CREATE",
      class: cls.toUpperCase(),
      voucher_type: cls,
      voucher_no: req.voucher_no ?? String(c.vouchers.length + 1),
      date: req.voucher_date ?? "1970-01-01",
      narration: req.narration ?? "",
      party: req.party ?? "",
      debit,
      credit,
      postings,
    };
    c.vouchers.push(entry);
    return {
      voucher_id: voucherID,
      voucher_version_id: id("vv"),
      version_no: 1,
      cgst: "0.0000",
      sgst: "0.0000",
      igst: "0.0000",
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
    const { postings, debit, credit } = summarize(c, req);
    const nextVersion = entry.version_no + 1;
    entry.version_no = nextVersion;
    entry.op = "ALTER";
    entry.class = cls.toUpperCase();
    entry.voucher_type = cls;
    if (req.voucher_no !== undefined) entry.voucher_no = req.voucher_no;
    if (req.voucher_date !== undefined) entry.date = req.voucher_date;
    entry.narration = req.narration ?? "";
    entry.party = req.party ?? "";
    entry.postings = postings;
    entry.debit = debit;
    entry.credit = credit;
    return {
      voucher_id: voucherID,
      voucher_version_id: id("vv"),
      version_no: nextVersion,
      cgst: "0.0000",
      sgst: "0.0000",
      igst: "0.0000",
    };
  }

  return {
    async createCompany(req: CreateCompanyRequest): Promise<CreateCompanyResponse> {
      const companyID = req.id ?? id("company");
      const c = ensure(companyID);
      c.name = req.name;
      return { company_id: companyID };
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
      // backend's reverse-prior-then-add-nothing net-zero delete.
      c.vouchers.splice(idx, 1);
      return {
        voucher_id: voucherID,
        voucher_version_id: id("vv"),
        version_no: nextVersion,
        op: "DELETE",
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
