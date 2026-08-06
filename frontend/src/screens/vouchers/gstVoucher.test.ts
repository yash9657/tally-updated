import { describe, it, expect, beforeEach, vi } from "vitest";
import { createMockClient, ApiError } from "../../api";
import type { CoredClient } from "../../api";
import {
  buildGstVoucherRequest,
  ensureOutputTaxLedgers,
  ensureLedger,
  emptyGstFormState,
  _resetOutputTaxLedgerCache,
  _resetLedgerCache,
  type OutputTaxLedgers,
} from "./gstVoucher";
import { acceptVoucher } from "./editActions";

beforeEach(() => {
  _resetOutputTaxLedgerCache();
  _resetLedgerCache();
});

const OUTPUT: OutputTaxLedgers = {
  cgstId: "led-cgst",
  sgstId: "led-sgst",
  igstId: "led-igst",
};

describe("buildGstVoucherRequest", () => {
  it("intra-state emits party Dr (gross) + income Cr + CGST/SGST Cr, no IGST line", () => {
    const req = buildGstVoucherRequest({
      partyLedgerId: "party-1",
      incomeLedgerId: "income-1",
      taxableValue: "10000.0000",
      supplierStateCode: "29",
      gst: { ...emptyGstFormState(), placeOfSupply: "29", gstRatePct: "18", hsn: "8471" },
      outputTax: OUTPUT,
      voucherNo: "INV/0001",
      voucherDate: "2025-05-10",
      party: "Acme Corp",
    });

    // GSTRequest is well-formed.
    expect(req.gst?.supplier_state_code).toBe("29");
    expect(req.gst?.place_of_supply_state_code).toBe("29");
    expect(req.gst?.lines?.[0]?.taxable_value).toBe("10000.0000");
    expect(req.gst?.lines?.[0]?.gst_rate_pct).toBe("18");
    expect(req.gst?.lines?.[0]?.hsn).toBe("8471");
    expect(req.gst?.cgst_ledger_id).toBe("led-cgst");
    expect(req.gst?.sgst_ledger_id).toBe("led-sgst");
    expect(req.gst?.igst_ledger_id).toBe("led-igst");

    // Balanced postings: party Dr 11800, income Cr 10000, CGST 900, SGST 900.
    const p = req.postings!;
    expect(p).toHaveLength(4);
    expect(p[0]).toMatchObject({ ledger_id: "party-1", dr_cr: "DR", amount: "11800.0000" });
    expect(p[1]).toMatchObject({ ledger_id: "income-1", dr_cr: "CR", amount: "10000.0000" });
    expect(p[2]).toMatchObject({ ledger_id: "led-cgst", dr_cr: "CR", amount: "900.0000" });
    expect(p[3]).toMatchObject({ ledger_id: "led-sgst", dr_cr: "CR", amount: "900.0000" });
    // No IGST line.
    expect(p.some((x) => x.ledger_id === "led-igst")).toBe(false);
    expect(req.voucher_no).toBe("INV/0001");
  });

  it("inter-state emits a single IGST line, no CGST/SGST", () => {
    const req = buildGstVoucherRequest({
      partyLedgerId: "party-1",
      incomeLedgerId: "income-1",
      taxableValue: "10000.0000",
      supplierStateCode: "29",
      gst: { ...emptyGstFormState(), placeOfSupply: "27", gstRatePct: "18" },
      outputTax: OUTPUT,
    });
    const p = req.postings!;
    expect(p).toHaveLength(3);
    expect(p[0]).toMatchObject({ ledger_id: "party-1", dr_cr: "DR", amount: "11800.0000" });
    expect(p[1]).toMatchObject({ ledger_id: "income-1", dr_cr: "CR", amount: "10000.0000" });
    expect(p[2]).toMatchObject({ ledger_id: "led-igst", dr_cr: "CR", amount: "1800.0000" });
    expect(p.some((x) => x.ledger_id === "led-cgst")).toBe(false);
    expect(p.some((x) => x.ledger_id === "led-sgst")).toBe(false);
  });

  // WS8a: the supplier (home) state is no longer a hardcoded constant, so it can
  // legitimately be unknown for one round trip. When it is, the field must be
  // OMITTED from the wire body rather than sent as "", so the server's
  // company.state_code default (internal/ledger/ledger.go, inside the post tx) is
  // the explicit, single source of truth for the supplier state.
  it("omits supplier_state_code entirely when the home state is unknown", () => {
    const req = buildGstVoucherRequest({
      partyLedgerId: "party-1",
      incomeLedgerId: "income-1",
      taxableValue: "10000.0000",
      supplierStateCode: "",
      gst: { ...emptyGstFormState(), placeOfSupply: "27", gstRatePct: "18" },
      outputTax: OUTPUT,
    });
    // Not merely undefined — the KEY must be absent (JSON.stringify drops it).
    expect("supplier_state_code" in (req.gst as object)).toBe(false);
    expect(JSON.stringify(req.gst)).not.toContain("supplier_state_code");
    // The other codes are untouched.
    expect(req.gst?.place_of_supply_state_code).toBe("27");

    // DOCUMENTED CONSEQUENCE: with no home state the client-side mirror cannot
    // classify the sale, so it derives inter-state (IGST) postings. The server
    // will fill the supplier state from company.state_code and re-derive; if the
    // company is in fact registered in 27 the posted legs will NOT match and the
    // post is REJECTED (422) — a loud failure, never a silently wrong tax. This
    // is why CompanyContext hydrates homeStateCode from the company master
    // before a gated screen renders instead of guessing a default.
    const p = req.postings!;
    expect(p).toHaveLength(3);
    expect(p[2]).toMatchObject({ ledger_id: "led-igst", dr_cr: "CR", amount: "1800.0000" });
  });

  it("sends supplier_state_code verbatim when the home state IS known", () => {
    const req = buildGstVoucherRequest({
      partyLedgerId: "party-1",
      incomeLedgerId: "income-1",
      taxableValue: "10000.0000",
      supplierStateCode: "27",
      gst: { ...emptyGstFormState(), placeOfSupply: "27", gstRatePct: "18" },
      outputTax: OUTPUT,
    });
    expect(req.gst?.supplier_state_code).toBe("27");
    // A Maharashtra company selling within Maharashtra is INTRA-state: the
    // hardcoded "29" this workstream removed would have produced IGST here.
    const p = req.postings!;
    expect(p).toHaveLength(4);
    expect(p[2]).toMatchObject({ ledger_id: "led-cgst", dr_cr: "CR", amount: "900.0000" });
    expect(p[3]).toMatchObject({ ledger_id: "led-sgst", dr_cr: "CR", amount: "900.0000" });
    expect(p.some((x) => x.ledger_id === "led-igst")).toBe(false);
  });
});

describe("acceptVoucher with a GST request body", () => {
  async function seed() {
    const client = createMockClient({ seed: false });
    const { company_id } = await client.createCompany({
      name: "Acme",
      fy_start_date: "2025-04-01",
      base_currency: "INR",
    });
    return { client, company_id };
  }

  it("create-mode Sales sends a well-formed gst block and the mock echoes the derived split", async () => {
    const { client, company_id } = await seed();
    const out = await ensureOutputTaxLedgers(client, company_id);
    const party = await ensureLedger(client, company_id, {
      name: "Acme Corp",
      groupName: "Sundry Debtors",
      stateCode: "29",
    });
    const income = await ensureLedger(client, company_id, {
      name: "Domestic Sales",
      groupName: "Sales Accounts",
    });
    const req = buildGstVoucherRequest({
      partyLedgerId: party,
      incomeLedgerId: income,
      taxableValue: "10000.0000",
      supplierStateCode: "29",
      gst: { ...emptyGstFormState(), placeOfSupply: "29", gstRatePct: "18", hsn: "8471" },
      outputTax: out,
    });
    const res = await acceptVoucher(
      client,
      company_id,
      { type: "sales", mode: "create" },
      req,
    );
    // The displayed split comes from the response (server-derived), echoed by mock.
    expect(res.cgst).toBe("900.0000");
    expect(res.sgst).toBe("900.0000");
    expect(res.igst).toBe("0.0000");
    expect(res.version_no).toBe(1);
  });
});

describe("ensureOutputTaxLedgers memoization", () => {
  it("creates the 3 Duties & Taxes ledgers once, then reuses cached ids", async () => {
    const base = createMockClient({ seed: false });
    const createLedger = vi.fn(base.createLedger.bind(base));
    const client = { ...base, createLedger } as unknown as CoredClient;

    const first = await ensureOutputTaxLedgers(client, "co-1");
    expect(createLedger).toHaveBeenCalledTimes(3);
    // Assert they went to Duties & Taxes.
    const groups = createLedger.mock.calls.map((c) => (c[1] as { group_name: string }).group_name);
    expect(groups).toEqual([
      "Duties & Taxes",
      "Duties & Taxes",
      "Duties & Taxes",
    ]);

    const second = await ensureOutputTaxLedgers(client, "co-1");
    // No new createLedger calls — cached ids reused.
    expect(createLedger).toHaveBeenCalledTimes(3);
    expect(second).toEqual(first);
  });
});

// ---------------------------------------------------------------------------
// ⛔⛔ ensureLedger AGAINST A LEDGER THAT ALREADY EXISTS.
//
// THE BUG THIS PINS, which was live and user-facing. `ensureLedger` called
// `createLedger` unconditionally, on the stated assumption — written in its own doc
// comment — that the route was idempotent by name. It is not: `INSERT INTO ledger`
// has no ON CONFLICT and `ledger_company_id_name_key` is a plain UNIQUE index, so
// creating an existing ledger is a raw Postgres unique-violation surfaced as
// "masters: insert ledger: ERROR: duplicate key value violates unique constraint".
//
// The consequence: on a FRESH PAGE LOAD the memo is empty, so keying a Payment /
// Receipt / Contra against any ledger the company already had — the normal case, and
// every case after the first day — failed with that SQL error. It appeared to work
// only when the memo was warm from an earlier post in the same session.
//
// ⚠️ NOT ONE TEST IN THE SUITE CAUGHT IT, because they all run against the mock,
// whose createLedger happily mints a fresh id per call. It was found by driving the
// app. So these tests assert against a client whose createLedger REFUSES a duplicate
// the way the server does — which is the only shape that could have caught it.
// ---------------------------------------------------------------------------
describe("ensureLedger resolves an EXISTING ledger instead of re-creating it", () => {
  /** A client that refuses a duplicate name, as the server's UNIQUE index does. */
  function strictClient(existing: { id: string; name: string }[]) {
    let creates = 0;
    const rows = [...existing];
    const client = {
      ...createMockClient(),
      async listLedgers() {
        return {
          ledgers: rows.map((r) => ({
            id: r.id,
            name: r.name,
            group_name: "Indirect Expenses",
            opening_balance: "0.0000",
            opening_dc: "D",
            maintain_bill_by_bill: false,
            gstin: "",
            state_code: "",
            registration_type: "unregistered",
          })),
        } as never;
      },
      async createLedger(_cid: string, req: { name: string }) {
        creates++;
        if (rows.some((r) => r.name === req.name)) {
          // EXACTLY what cored does, message and all.
          throw new ApiError(
            500,
            'masters: insert ledger: ERROR: duplicate key value violates unique constraint "ledger_company_id_name_key" (SQLSTATE 23505)',
          );
        }
        const created = { id: `new-${req.name}`, name: req.name };
        rows.push(created);
        return { ledger_id: created.id } as never;
      },
    } as unknown as CoredClient;
    return { client, creates: () => creates };
  }

  it("⛔ returns the existing id and NEVER calls createLedger", async () => {
    _resetLedgerCache();
    const { client, creates } = strictClient([
      { id: "led-electricity", name: "Electricity" },
    ]);
    const id = await ensureLedger(client, "c1", {
      name: "Electricity",
      groupName: "Indirect Expenses",
    });
    expect(id).toBe("led-electricity");
    // THE ASSERTION THAT WOULD HAVE CAUGHT THE BUG: not one create attempt.
    expect(creates()).toBe(0);
  });

  it("still CREATES a ledger the company genuinely does not have", async () => {
    // The control. Without it, "never create" would pass by never working at all.
    _resetLedgerCache();
    const { client, creates } = strictClient([
      { id: "led-electricity", name: "Electricity" },
    ]);
    const id = await ensureLedger(client, "c1", {
      name: "Stationery",
      groupName: "Indirect Expenses",
    });
    expect(id).toBe("new-Stationery");
    expect(creates()).toBe(1);
  });

  it("resolves two particulars naming the SAME new ledger to one id, with one create", async () => {
    _resetLedgerCache();
    const { client, creates } = strictClient([]);
    const a = await ensureLedger(client, "c1", {
      name: "Courier",
      groupName: "Indirect Expenses",
    });
    const b = await ensureLedger(client, "c1", {
      name: "Courier",
      groupName: "Indirect Expenses",
    });
    expect(a).toBe(b);
    expect(creates()).toBe(1);
  });

  it("matches the name EXACTLY, mirroring the UNIQUE index", async () => {
    // "electricity" and "Electricity" are two different ledgers to (company_id, name),
    // so resolving them to one here would make this helper disagree with the database
    // about what a duplicate is.
    _resetLedgerCache();
    const { client } = strictClient([{ id: "led-e", name: "Electricity" }]);
    const id = await ensureLedger(client, "c1", {
      name: "electricity",
      groupName: "Indirect Expenses",
    });
    expect(id).toBe("new-electricity");
  });
});
