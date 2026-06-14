import { describe, it, expect, beforeEach, vi } from "vitest";
import { createMockClient } from "../../api";
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
