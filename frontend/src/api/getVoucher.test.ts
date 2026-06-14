import { describe, it, expect } from "vitest";
import { createMockClient } from "./mock";
import { createRestClient } from "./rest";
import { ApiError } from "./http";

/**
 * GET /vouchers/{voucherID} — the head-version fetch that hydrates an ALTER
 * form. The defining property: postings carry the REAL ledger_id uuids (NOT the
 * day-book names), so a full-restatement alter resends valid uuids. Also covers
 * GST attribute round-trip and the 404 on unknown/deleted ids.
 */
describe("mock getVoucher", () => {
  async function seed() {
    const client = createMockClient({ seed: false });
    const { company_id } = await client.createCompany({
      name: "Acme",
      fy_start_date: "2025-04-01",
      base_currency: "INR",
    });
    const debtor = await client.createLedger(company_id, {
      name: "Acme Corp",
      group_name: "Sundry Debtors",
      state_code: "29",
    });
    const sales = await client.createLedger(company_id, {
      name: "Domestic Sales",
      group_name: "Sales Accounts",
    });
    return { client, company_id, debtor, sales };
  }

  it("returns postings whose ledger_id are the REAL recorded uuids (not names)", async () => {
    const { client, company_id, debtor, sales } = await seed();
    const posted = await client.postSalesVoucher(company_id, {
      voucher_no: "INV/0001",
      voucher_date: "2025-05-10",
      party: "Acme Corp",
      postings: [
        { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "400.0000" },
        { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "400.0000" },
      ],
    });

    const view = await client.getVoucher(company_id, posted.voucher_id);
    expect(view.voucher_no).toBe("INV/0001");
    expect(view.op).toBe("CREATE");
    expect(view.version_no).toBe(1);
    // The postings carry the REAL ledger ids as a SET (order-independent), and
    // each id is non-empty (would pass a uuid.Parse) — NOT a ledger name.
    const ids = new Set(view.postings.map((p) => p.ledger_id));
    expect(ids).toEqual(new Set([debtor.ledger_id, sales.ledger_id]));
    expect(view.postings.map((p) => p.ledger)).toEqual([
      "Acme Corp",
      "Domestic Sales",
    ]);
    // dr_cr is the canonical single-letter wire form.
    expect(view.postings.map((p) => p.dr_cr).sort()).toEqual(["C", "D"]);
  });

  it("round-trips GST attributes for an intra-state GST sale", async () => {
    const { client, company_id, debtor, sales } = await seed();
    const out = await Promise.all([
      client.createLedger(company_id, { name: "Output CGST", group_name: "Duties & Taxes" }),
      client.createLedger(company_id, { name: "Output SGST", group_name: "Duties & Taxes" }),
      client.createLedger(company_id, { name: "Output IGST", group_name: "Duties & Taxes" }),
    ]);
    const posted = await client.postSalesVoucher(company_id, {
      voucher_no: "INV/0002",
      voucher_date: "2025-05-10",
      party: "Acme Corp",
      postings: [
        { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "11800.0000" },
        { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "10000.0000" },
        { ledger_id: out[0].ledger_id, dr_cr: "CR", amount: "900.0000" },
        { ledger_id: out[1].ledger_id, dr_cr: "CR", amount: "900.0000" },
      ],
      gst: {
        supplier_state_code: "29",
        place_of_supply_state_code: "29",
        lines: [{ taxable_value: "10000.0000", gst_rate_pct: "18", hsn: "8471" }],
        cgst_ledger_id: out[0].ledger_id,
        sgst_ledger_id: out[1].ledger_id,
        igst_ledger_id: out[2].ledger_id,
      },
    });
    expect(posted.cgst).toBe("900.0000");
    expect(posted.sgst).toBe("900.0000");

    const view = await client.getVoucher(company_id, posted.voucher_id);
    expect(view.place_of_supply).toBe("29");
    expect(view.taxable_value).toBe("10000.0000");
    expect(view.cgst).toBe("900.0000");
    expect(view.sgst).toBe("900.0000");
    expect(view.igst).toBe("0.0000");
  });

  it("404s (rejects with ApiError) for an unknown voucher id", async () => {
    const { client, company_id } = await seed();
    await expect(
      client.getVoucher(company_id, "00000000-0000-0000-0000-000000000000"),
    ).rejects.toBeInstanceOf(ApiError);
  });

  it("404s after the voucher is deleted (head row excluded)", async () => {
    const { client, company_id, debtor, sales } = await seed();
    const posted = await client.postSalesVoucher(company_id, {
      voucher_no: "INV/0003",
      voucher_date: "2025-05-10",
      postings: [
        { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "1.0000" },
        { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "1.0000" },
      ],
    });
    await client.deleteVoucher(company_id, posted.voucher_id);
    await expect(
      client.getVoucher(company_id, posted.voucher_id),
    ).rejects.toBeInstanceOf(ApiError);
  });
});

describe("REST getVoucher", () => {
  it("targets GET /v1/companies/{id}/vouchers/{voucherID}", async () => {
    const calls: { url: string; method?: string }[] = [];
    const fetchImpl = (async (url: string | URL, init?: RequestInit) => {
      calls.push({ url: String(url), method: init?.method });
      return new Response(
        JSON.stringify({
          voucher_id: "v1",
          voucher_no: "INV/0001",
          voucher_date: "2025-05-10",
          class: "Sales",
          voucher_type_id: "",
          party: "Acme Corp",
          party_ledger_id: "",
          narration: "",
          version_no: 1,
          op: "CREATE",
          place_of_supply: "29",
          reverse_charge: false,
          taxable_value: "10000.0000",
          cgst: "900.0000",
          sgst: "900.0000",
          igst: "0.0000",
          postings: [],
          bill_allocations: [],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    }) as unknown as typeof fetch;

    const client = createRestClient({ baseUrl: "http://api.test", fetchImpl });
    const view = await client.getVoucher("co-1", "v1");
    expect(view.taxable_value).toBe("10000.0000");
    expect(calls).toHaveLength(1);
    expect(calls[0]?.url).toBe(
      "http://api.test/v1/companies/co-1/vouchers/v1",
    );
    expect(calls[0]?.method).toBe("GET");
  });
});
