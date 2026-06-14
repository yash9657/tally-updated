import { describe, it, expect } from "vitest";
import { createMockClient } from "./mock";
import { createRestClient } from "./rest";

/**
 * Edit-loop fidelity over the CoredClient seam. The mock must honour the same
 * invariants the live backend does so screens (and the e2e harness's client
 * code path) behave identically without a server:
 *   - ALTER restates (400 -> 650 -> 650), never accumulates to 1050.
 *   - ALTER preserves the logical voucher_id and bumps version_no.
 *   - DELETE drops the voucher's accounting effect and day-book visibility.
 */
describe("edit loop on the mock CoredClient", () => {
  async function seedSales() {
    const client = createMockClient({ seed: false });
    const { company_id } = await client.createCompany({
      name: "Acme",
      fy_start_date: "2025-04-01",
      base_currency: "INR",
    });
    const debtor = await client.createLedger(company_id, {
      name: "Acme Corp",
      group_name: "Sundry Debtors",
    });
    const sales = await client.createLedger(company_id, {
      name: "Domestic Sales",
      group_name: "Sales Accounts",
    });
    const posted = await client.postSalesVoucher(company_id, {
      party_ledger_id: debtor.ledger_id,
      voucher_no: "INV-1",
      voucher_date: "2025-04-10",
      postings: [
        { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "400.0000" },
        { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "400.0000" },
      ],
    });
    return { client, company_id, debtor, sales, posted };
  }

  it("alter restates the amount (400 -> 650 -> 650, not 1050) and preserves identity", async () => {
    const { client, company_id, debtor, sales, posted } = await seedSales();
    expect(posted.version_no).toBe(1);

    const tb0 = await client.getTrialBalance(company_id);
    expect(tb0.total_debit).toBe("400.0000");

    const altered = await client.alterSalesVoucher(
      company_id,
      posted.voucher_id,
      {
        voucher_no: "INV-1",
        voucher_date: "2025-04-10",
        postings: [
          { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "650.0000" },
          { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "650.0000" },
        ],
      },
    );
    expect(altered.voucher_id).toBe(posted.voucher_id);
    expect(altered.version_no).toBe(2);

    const tb1 = await client.getTrialBalance(company_id);
    expect(tb1.total_debit).toBe("650.0000");
    expect(tb1.balanced).toBe(true);

    // Day Book shows ONE entry (no CREATE+ALTER dupes), number/date preserved.
    const day = await client.getDayBook(company_id);
    const rows = day.entries.filter((e) => e.voucher_id === posted.voucher_id);
    expect(rows).toHaveLength(1);
    expect(rows[0]?.voucher_no).toBe("INV-1");
    expect(rows[0]?.date).toBe("2025-04-10");
    expect(rows[0]?.debit).toBe("650.0000");
    expect(rows[0]?.op).toBe("ALTER");
  });

  it("delete drops the voucher from the trial balance and the day book", async () => {
    const { client, company_id, posted } = await seedSales();
    const del = await client.deleteVoucher(company_id, posted.voucher_id);
    expect(del.op).toBe("DELETE");
    expect(del.voucher_id).toBe(posted.voucher_id);

    const tb = await client.getTrialBalance(company_id);
    expect(tb.total_debit).toBe("0.0000");

    const day = await client.getDayBook(company_id);
    expect(
      day.entries.filter((e) => e.voucher_id === posted.voucher_id),
    ).toHaveLength(0);
  });

  it("alter/delete on an unknown voucher id reject", async () => {
    const client = createMockClient({ seed: false });
    const { company_id } = await client.createCompany({
      name: "Acme",
      fy_start_date: "2025-04-01",
      base_currency: "INR",
    });
    await expect(
      client.alterSalesVoucher(company_id, "voucher-missing", { postings: [] }),
    ).rejects.toThrow();
    await expect(
      client.deleteVoucher(company_id, "voucher-missing"),
    ).rejects.toThrow();
  });
});

describe("REST client alter/delete routing over a stub fetch", () => {
  it("PUTs the class-scoped alter route with the voucher id and JSON body", async () => {
    const calls: { url: string; method?: string }[] = [];
    const fetchImpl = (async (url: string | URL, init?: RequestInit) => {
      calls.push({ url: String(url), method: init?.method });
      return new Response(
        JSON.stringify({
          voucher_id: "v1",
          voucher_version_id: "vv2",
          version_no: 2,
          cgst: "0.0000",
          sgst: "0.0000",
          igst: "0.0000",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    }) as unknown as typeof fetch;
    const client = createRestClient({ baseUrl: "http://api.test", fetchImpl });

    const res = await client.alterSalesVoucher("co-1", "v1", {
      voucher_no: "INV-1",
      postings: [{ ledger_id: "l1", dr_cr: "DR", amount: "650.0000" }],
    });
    expect(res.version_no).toBe(2);
    expect(calls[0]?.url).toBe(
      "http://api.test/v1/companies/co-1/vouchers/sales/v1",
    );
    expect(calls[0]?.method).toBe("PUT");
  });

  it("DELETEs the class-agnostic route and forwards created_by", async () => {
    const calls: { url: string; method?: string }[] = [];
    const fetchImpl = (async (url: string | URL, init?: RequestInit) => {
      calls.push({ url: String(url), method: init?.method });
      return new Response(
        JSON.stringify({
          voucher_id: "v1",
          voucher_version_id: "vv3",
          version_no: 3,
          op: "DELETE",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    }) as unknown as typeof fetch;
    const client = createRestClient({ baseUrl: "http://api.test", fetchImpl });

    const res = await client.deleteVoucher("co-1", "v1", "auditor@x");
    expect(res.op).toBe("DELETE");
    expect(calls[0]?.url).toBe(
      "http://api.test/v1/companies/co-1/vouchers/v1?created_by=auditor%40x",
    );
    expect(calls[0]?.method).toBe("DELETE");
  });
});
