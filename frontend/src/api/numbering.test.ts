import { describe, it, expect } from "vitest";
import { createMockClient, finYearOf, formatVoucherNo } from "./mock";

/**
 * The mock client's faithful per-(type, FY) running-number allocator. It mirrors
 * the backend numbering model so component/e2e assertions about FORMAT and
 * SEQUENCE hold without a live server.
 */
describe("finYearOf (Indian FY, Apr-Mar)", () => {
  it("derives the start-year from the voucher date, not today", () => {
    expect(finYearOf("2026-03-31")).toBe(2025); // Mar -> prior FY
    expect(finYearOf("2026-04-01")).toBe(2026); // Apr -> new FY
    expect(finYearOf("2026-07-15")).toBe(2026);
    expect(finYearOf("2027-02-01")).toBe(2026); // still FY 2026-27
  });
});

describe("formatVoucherNo", () => {
  it("zero-pads to width when set, else bare integer", () => {
    expect(formatVoucherNo({ prefix: "INV/", suffix: "", width: 4 }, 42)).toBe(
      "INV/0042",
    );
    expect(formatVoucherNo({ prefix: "", suffix: "", width: null }, 7)).toBe("7");
    expect(
      formatVoucherNo({ prefix: "A/", suffix: "/26", width: 2 }, 3),
    ).toBe("A/03/26");
  });
});

describe("mock allocator on the CoredClient", () => {
  async function fresh() {
    const client = createMockClient({ seed: false });
    const { company_id } = await client.createCompany({
      name: "Acme",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
    });
    return { client, company_id };
  }

  it("auto-allocates consecutive numbers within an FY (empty voucher_no)", async () => {
    const { client, company_id } = await fresh();
    const a = await client.postSalesVoucher(company_id, {
      voucher_date: "2026-04-10",
      postings: [],
    });
    const b = await client.postSalesVoucher(company_id, {
      voucher_date: "2026-05-01",
      postings: [],
    });
    expect(a.voucher_no).toBe("INV/0001");
    expect(b.voucher_no).toBe("INV/0002");
  });

  it("honors a manual override verbatim and does NOT bump the counter", async () => {
    const { client, company_id } = await fresh();
    const over = await client.postSalesVoucher(company_id, {
      voucher_no: "OVERRIDE/7",
      voucher_date: "2026-04-10",
      postings: [],
    });
    expect(over.voucher_no).toBe("OVERRIDE/7");
    // The next AUTO create gets 0001 (not 8): override left the counter untouched.
    const next = await client.postSalesVoucher(company_id, {
      voucher_date: "2026-04-11",
      postings: [],
    });
    expect(next.voucher_no).toBe("INV/0001");
  });

  it("restarts numbering in a new financial year", async () => {
    const { client, company_id } = await fresh();
    const inFy = await client.postSalesVoucher(company_id, {
      voucher_date: "2027-02-01", // FY 2026-27
      postings: [],
    });
    const newFy = await client.postSalesVoucher(company_id, {
      voucher_date: "2027-05-01", // FY 2027-28
      postings: [],
    });
    expect(inFy.voucher_no).toBe("INV/0001");
    expect(newFy.voucher_no).toBe("INV/0001"); // new FY restarts at 1
  });

  it("alter carries the number forward (never re-allocates) when voucher_no empty", async () => {
    const { client, company_id } = await fresh();
    const created = await client.postSalesVoucher(company_id, {
      voucher_date: "2026-04-10",
      postings: [],
    });
    expect(created.voucher_no).toBe("INV/0001");
    const altered = await client.alterSalesVoucher(
      company_id,
      created.voucher_id,
      { voucher_date: "2026-04-10", postings: [] }, // empty voucher_no
    );
    expect(altered.voucher_no).toBe("INV/0001");
    // A subsequent auto create still gets 0002 (alter did not consume a number).
    const next = await client.postSalesVoucher(company_id, {
      voucher_date: "2026-04-12",
      postings: [],
    });
    expect(next.voucher_no).toBe("INV/0002");
  });

  it("delete leaves a gap (counter never reused)", async () => {
    const { client, company_id } = await fresh();
    const a = await client.postSalesVoucher(company_id, {
      voucher_date: "2026-04-10",
      postings: [],
    });
    const b = await client.postSalesVoucher(company_id, {
      voucher_date: "2026-04-11",
      postings: [],
    });
    await client.postSalesVoucher(company_id, {
      voucher_date: "2026-04-12",
      postings: [],
    });
    expect(a.voucher_no).toBe("INV/0001");
    expect(b.voucher_no).toBe("INV/0002");
    const del = await client.deleteVoucher(company_id, b.voucher_id);
    expect(del.voucher_no).toBe("INV/0002");
    const d = await client.postSalesVoucher(company_id, {
      voucher_date: "2026-04-13",
      postings: [],
    });
    expect(d.voucher_no).toBe("INV/0004"); // 0002 is NOT reused
  });
});
