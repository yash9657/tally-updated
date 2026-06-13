import { describe, it, expect } from "vitest";
import { createMockClient } from "./mock";
import { createRestClient } from "./rest";
import { ApiError, createHttpClient } from "./http";
import { isGSTR1Reconciled } from "./types";

describe("mock CoredClient", () => {
  it("creates a company and echoes the id", async () => {
    const client = createMockClient({ seed: false });
    const res = await client.createCompany({
      name: "Acme",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
    });
    expect(typeof res.company_id).toBe("string");
    expect(res.company_id.length).toBeGreaterThan(0);
  });

  it("posts a sales voucher and reflects it in the day book and trial balance", async () => {
    const client = createMockClient({ seed: false });
    const { company_id } = await client.createCompany({
      name: "Acme",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
    });
    const cash = await client.createLedger(company_id, {
      name: "Cash",
      group_name: "Cash-in-Hand",
    });
    const sales = await client.createLedger(company_id, {
      name: "Sales",
      group_name: "Sales Accounts",
    });
    const v = await client.postSalesVoucher(company_id, {
      voucher_date: "2026-04-02",
      party: "Walk-in",
      postings: [
        { ledger_id: cash.ledger_id, dr_cr: "DR", amount: "1180.0000" },
        { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "1180.0000" },
      ],
    });
    expect(v.voucher_id).toBeTruthy();
    // Money stays a fixed-4 string, never a number.
    expect(v.cgst).toBe("0.0000");

    const day = await client.getDayBook(company_id);
    expect(day.entries).toHaveLength(1);
    expect(day.entries[0]?.debit).toBe("1180.0000");
    expect(day.entries[0]?.credit).toBe("1180.0000");

    const tb = await client.getTrialBalance(company_id);
    expect(tb.total_debit).toBe("1180.0000");
    expect(tb.total_credit).toBe("1180.0000");
    expect(tb.balanced).toBe(true);
    // Values are strings throughout.
    expect(typeof tb.total_debit).toBe("string");
  });

  it("returns a reconciliation envelope when an output-tax ledger is supplied", async () => {
    const client = createMockClient({ seed: false });
    const { company_id } = await client.createCompany({
      name: "Acme",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
    });
    const plain = await client.generateGSTR1(company_id, {
      from: "2026-04-01",
      to: "2026-04-30",
    });
    expect(isGSTR1Reconciled(plain)).toBe(false);

    const reconciled = await client.generateGSTR1(company_id, {
      from: "2026-04-01",
      to: "2026-04-30",
      cgst_ledger_id: "ledger-cgst",
    });
    expect(isGSTR1Reconciled(reconciled)).toBe(true);
  });
});

describe("REST CoredClient over a stub fetch", () => {
  it("targets the company-scoped sales voucher route with a JSON body", async () => {
    const calls: { url: string; init?: RequestInit }[] = [];
    const fetchImpl = (async (url: string | URL, init?: RequestInit) => {
      calls.push({ url: String(url), init });
      return new Response(
        JSON.stringify({
          voucher_id: "v1",
          voucher_version_id: "vv1",
          version_no: 1,
          cgst: "0.0000",
          sgst: "0.0000",
          igst: "0.0000",
        }),
        { status: 201, headers: { "Content-Type": "application/json" } },
      );
    }) as unknown as typeof fetch;

    const client = createRestClient({
      baseUrl: "http://api.test",
      fetchImpl,
    });
    const res = await client.postSalesVoucher("co-1", {
      voucher_date: "2026-04-02",
      postings: [{ ledger_id: "l1", dr_cr: "DR", amount: "10.0000" }],
    });
    expect(res.voucher_id).toBe("v1");
    expect(calls).toHaveLength(1);
    expect(calls[0]?.url).toBe(
      "http://api.test/v1/companies/co-1/vouchers/sales",
    );
    expect(calls[0]?.init?.method).toBe("POST");
  });

  it("appends report query params and surfaces server errors as ApiError", async () => {
    const seen: string[] = [];
    const fetchImpl = (async (url: string | URL) => {
      seen.push(String(url));
      return new Response(JSON.stringify({ error: "bad period" }), {
        status: 400,
      });
    }) as unknown as typeof fetch;
    const client = createRestClient({ baseUrl: "", fetchImpl });
    await expect(
      client.getDayBook("co-1", { from: "2026-04-01", to: "2026-04-30" }),
    ).rejects.toBeInstanceOf(ApiError);
    expect(seen[0]).toContain(
      "/v1/companies/co-1/day-book?from=2026-04-01&to=2026-04-30",
    );
  });
});

describe("http base URL handling", () => {
  it("strips a trailing slash from the configured base", async () => {
    let captured = "";
    const fetchImpl = (async (url: string | URL) => {
      captured = String(url);
      return new Response("{}", { status: 200 });
    }) as unknown as typeof fetch;
    const http = createHttpClient({ baseUrl: "http://x/", fetchImpl });
    await http.get("/healthz");
    expect(captured).toBe("http://x/healthz");
  });
});
