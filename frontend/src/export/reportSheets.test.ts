import { describe, it, expect } from "vitest";

import { toCsv, parseCsv, type ExportSheet } from "./sheet";
import { stockSummarySheet } from "../screens/reports/StockSummaryScreen";
import { tdsRegisterSheet } from "../screens/reports/TDSRegisterScreen";
import { orderBookSheet } from "../screens/reports/OrderBookScreens";
import { costCentreSummarySheet } from "../screens/reports/CostCentreSummaryScreen";
import { costCategorySummarySheet } from "../screens/reports/CostCategorySummaryScreen";
import type {
  CostCategorySummaryResponse,
  CostCentreSummaryResponse,
  OrderBookResponse,
  StockSummaryResponse,
  TDSRegisterResponse,
} from "../api";

/**
 * THE FIVE SCREEN-OWNED SHEET BUILDERS.
 *
 * ⛔ WHY THESE ARE TESTED HERE AND NOT THROUGH THE SHELL. The shell tests in
 * exportKey.test.tsx press the real key on the real AppShell, which is the only
 * way to know Alt+E is ROUTED — but the mock client's seeded company has no
 * stock, no orders, no cost masters and no TDS, so those screens can only be
 * driven to their (correct, asserted) "nothing to export" sentences. The
 * builders themselves are pure functions, so they are exercised here against
 * fixtures instead of being left uncovered.
 *
 * ⛔ AND THE ASSERTION THAT MATTERS MOST IN THIS FILE IS THE WIDTH. `toCsv`
 * REFUSES a row whose cell count does not match the headers, because a padded
 * row puts every figure after the gap under the wrong heading — a credit read as
 * a debit. That refusal is a runtime one, so without a test per builder the
 * first person to find a mis-widthed row is an operator whose export failed.
 * Every test below runs its sheet through toCsv, which is what enforces it.
 */

/** Export the sheet and read it back as the grid an operator would see. */
function grid(sheet: ExportSheet): string[][] {
  return parseCsv(toCsv(sheet));
}

/** Every row has one cell per header — asserted through the encoder itself. */
function expectRectangular(sheet: ExportSheet) {
  expect(() => toCsv(sheet)).not.toThrow();
  for (const row of sheet.rows) {
    expect(row.length).toBe(sheet.headers.length);
  }
}

// ---------------------------------------------------------------------------

describe("stockSummarySheet", () => {
  const data: StockSummaryResponse = {
    as_on: "2026-06-30",
    groups: [],
    total_value: "9999.1234",
    rows: [],
    items: [
      {
        stock_item_id: "i1",
        item: "Widget",
        unit: "Nos",
        valuation_method: "AVERAGE",
        stock_group: "",
        opening_qty: "0.0000",
        opening_value: "0.0000",
        inward_qty: "100.0000",
        inward_value: "5000.0000",
        outward_qty: "40.0000",
        outward_value: "2000.5678",
        closing_qty: "60.0000",
        closing_value: "2999.4322",
        base_unit: "",
        base_qty: "",
        positions: [
          {
            stock_item_id: "i1",
            item: "Widget",
            unit: "Nos",
            godown_id: "g1",
            godown: "Main Store",
            batch_id: "",
            batch: "",
            valuation_method: "AVERAGE",
            stock_group: "",
            opening_qty: "0.0000",
            opening_value: "0.0000",
            inward_qty: "100.0000",
            inward_value: "5000.0000",
            outward_qty: "40.0000",
            outward_value: "2000.5678",
            closing_qty: "60.0000",
            closing_value: "2999.4322",
            closing_rate: "49.9905",
          },
        ],
      },
    ],
  };

  it("is rectangular and carries the as-at date in its title", () => {
    const sheet = stockSummarySheet(data, "2026-06-30");
    expectRectangular(sheet);
    expect(sheet.title).toContain("2026-06-30");
  });

  it("writes quantities and the presentation-only rate EXACTLY", () => {
    const g = grid(stockSummarySheet(data, undefined));
    // ⛔ INDEXED BY HEADER NAME, NEVER BY A LITERAL COLUMN NUMBER. This test
    // read `item[4]` and `item[5]`, and when the sheet was widened to give
    // every quantity its VALUE beside it (the inward money figure was written
    // down nowhere at all), those numbers silently pointed at different
    // columns. A test that hardcodes a position cannot notice a column moving —
    // which is the same class of blindness as the defect it is guarding.
    const at = (row: string[], header: string) => row[g[0]!.indexOf(header)];
    const item = g.find((r) => r[0] === "Widget")!;
    expect(at(item, "Closing Qty")).toBe("60.0000"); // all four decimals
    expect(at(item, "Rate")).toBe(""); // no rate on an item total
    expect(at(item, "Closing Value")).toBe("2999.4322");
    // The inward MONEY: returned by the API, and absent from this file until
    // the columns were widened.
    expect(at(item, "Inward Value")).toBe("5000.0000");
    expect(at(item, "Inward Qty")).toBe("100.0000");

    const pos = g.find((r) => r[0] === "Main Store")!;
    // `closing_rate` is 4-dp presentation only. Exported verbatim, so nobody can
    // multiply a rounded rate back out and get a value that never existed.
    expect(at(pos, "Rate")).toBe("49.9905");

    const footer = g[g.length - 1]!;
    expect(footer[0]).toBe("Stock-in-Hand");
    expect(at(footer, "Closing Value")).toBe("9999.1234");
    // Every other cell of the footer is blank: the response carries no
    // opening/inward/outward totals, and a client-side fold would be this sheet
    // asserting a figure the server never derived.
    expect(footer.filter((c) => c !== "")).toEqual([
      "Stock-in-Hand",
      "9999.1234",
    ]);
  });

  it("labels an untracked location instead of exporting an empty cell", () => {
    const untracked: StockSummaryResponse = {
      ...data,
      items: [
        {
          ...data.items[0]!,
          base_unit: "",
          base_qty: "",
          positions: [
            { ...data.items[0]!.positions[0]!, godown_id: "", godown: "" },
          ],
        },
      ],
    };
    const g = grid(stockSummarySheet(untracked, undefined));
    expect(g.map((r) => r[0])).toContain("(no location)");
  });
});

// ---------------------------------------------------------------------------

describe("tdsRegisterSheet", () => {
  const data: TDSRegisterResponse = {
    from: "2026-04-01",
    to: "2026-06-30",
    tax_year_start: 2026,
    deductions: [
      {
        date: "2026-04-10",
        voucher_no: "0042",
        voucher_id: "v1",
        deductee: "Contractor A",
        section: "194C",
        deductee_type: "COMPANY",
        return_code: "94C",
        base: "100000.0000",
        rate_pct: "2.00",
        tds: "2000.0000",
        pan_available: true,
      },
    ],
    collections: [
      {
        date: "2026-05-11",
        voucher_no: "0100",
        voucher_id: "v2",
        buyer: "Buyer B",
        section: "206C(1H)",
        base: "5000000.0000",
        rate_pct: "0.10",
        tcs: "0.0000",
        declaration: "EXEMPT",
        pan_available: false,
      },
    ],
    total_base_deducted: "100000.0000",
    total_tds: "2000.0000",
    total_base_collected: "5000000.0000",
    total_tcs: "0.0000",
    sub_threshold_deductions: 0,
    exempt_collections: 1,
    disclosures: [],
    return_file: {
      available: false,
      format_version: "1.0",
      format_source: "",
      summary: "",
      validated: false,
      validation_note: "",
      blockers: [],
      what_you_can_do_instead: "",
    },
  };

  it("is rectangular across BOTH tables and their two totals", () => {
    expectRectangular(tdsRegisterSheet(data));
  });

  it("discriminates deductions from collections, so the Tax column cannot be summed blind", () => {
    const g = grid(tdsRegisterSheet(data));
    expect(g[0]![0]).toBe("Type");
    const kinds = g.slice(1).map((r) => r[0]);
    expect(kinds).toEqual([
      "Deduction",
      "Deduction total",
      "Collection",
      "Collection total",
    ]);
  });

  it("keeps the return code and the declaration in SEPARATE columns", () => {
    const g = grid(tdsRegisterSheet(data));
    const ded = g[1]!;
    const col = g[3]!;
    expect(ded[5]).toBe("94C"); // Return code
    expect(ded[6]).toBe(""); // no declaration on a deduction
    expect(col[5]).toBe(""); // no return code on a collection
    // The only field that tells three different zeroes apart — a TCS of zero can
    // mean below-threshold, exempt, or penal-rate-not-applied.
    expect(col[6]).toBe("EXEMPT");
    expect(col[9]).toBe("0.0000");
  });

  it("keeps a leading-zero voucher number and a two-decimal rate exact", () => {
    const g = grid(tdsRegisterSheet(data));
    expect(g[1]![2]).toBe("0042");
    expect(g[1]![8]).toBe("2.00");
    expect(g[3]![8]).toBe("0.10");
  });

  it("flags a missing PAN on the party, because the register does", () => {
    const g = grid(tdsRegisterSheet(data));
    expect(g[3]![3]).toBe("Buyer B (no PAN)");
    expect(g[1]![3]).toBe("Contractor A");
  });
});

// ---------------------------------------------------------------------------

describe("orderBookSheet", () => {
  const copy = { title: "Sales Order Book", partyHeading: "Customer" };
  const data: OrderBookResponse = {
    direction: "SALES",
    as_on: "2026-06-30",
    rows: [
      {
        order_line_id: "l1",
        order_id: "o1",
        order_no: "SO/0001",
        order_date: "2026-05-01",
        party: "Acme, Inc.",
        stock_item: "Widget",
        qty: "100.0000",
        fulfilled_qty: "40.0000",
        outstanding_qty: "60.0000",
        rate: "49.9905",
        outstanding_value: "2999.4300",
        due_date: "",
        is_overdue: false,
      },
    ],
    total_outstanding_value: "2999.4300",
  } as unknown as OrderBookResponse;

  it("is rectangular", () => {
    expectRectangular(orderBookSheet(data, copy as never, "2026-06-30"));
  });

  it("says 'none agreed' for an undated line and never calls it overdue", () => {
    const g = grid(orderBookSheet(data, copy as never, undefined));
    const row = g[1]!;
    expect(row[9]).toBe("none agreed");
    expect(row[10]).toBe("");
  });

  it("has NO quantity total, because two items are in different units", () => {
    const g = grid(orderBookSheet(data, copy as never, undefined));
    const total = g[g.length - 1]!;
    expect(total[0]).toBe("Total outstanding");
    expect(total[4]).toBe("");
    expect(total[5]).toBe("");
    expect(total[6]).toBe("");
    // Only the VALUE total, and it is the SERVER's figure.
    expect(total[8]).toBe("2999.4300");
  });

  it("names the party column after the DIRECTION, not generically", () => {
    const sheet = orderBookSheet(data, copy as never, undefined);
    expect(sheet.headers[2]).toBe("Customer");
    expect(sheet.title).toContain("Sales Order Book");
  });
});

// ---------------------------------------------------------------------------

describe("costCentreSummarySheet", () => {
  const data: CostCentreSummaryResponse = {
    cost_category_id: "c1",
    cost_category: "Department",
    allocate_non_revenue: false,
    from: "2026-04-01",
    to: "2026-06-30",
    centres: [
      {
        cost_centre_id: "cc1",
        name: "Sales",
        parent_id: "",
        depth: 0,
        own_debit: "1000.0000",
        own_credit: "0.0000",
        own_net: "1000.0000",
        total_debit: "3000.0000",
        total_credit: "0.0000",
        total_net: "3000.0000",
        children: [
          {
            cost_centre_id: "cc2",
            name: "North",
            parent_id: "cc1",
            depth: 1,
            own_debit: "2000.0000",
            own_credit: "0.0000",
            own_net: "2000.0000",
            total_debit: "2000.0000",
            total_credit: "0.0000",
            total_net: "2000.0000",
            children: [],
          },
        ],
      },
    ],
    unattached: [
      {
        cost_centre_id: "cc9",
        name: "Orphan",
        parent_id: "missing",
        depth: 0,
        own_debit: "50.0000",
        own_credit: "0.0000",
        own_net: "50.0000",
        total_debit: "50.0000",
        total_credit: "0.0000",
        total_net: "50.0000",
        children: [],
      },
    ],
    total_debit: "3000.0000",
    total_credit: "0.0000",
    total_net: "3000.0000",
    unattached_debit: "50.0000",
    unattached_credit: "0.0000",
    allocated_debit: "3050.0000",
    allocated_credit: "0.0000",
  } as unknown as CostCentreSummaryResponse;

  it("is rectangular and names the AXIS in the title", () => {
    const sheet = costCentreSummarySheet(data);
    expectRectangular(sheet);
    // ⛔ Several axes allocate the SAME money in parallel. A file with no axis on
    // it is a set of figures whose subject cannot be recovered.
    expect(sheet.title).toContain("Department");
  });

  it("keeps OWN and TOTAL as four separate columns", () => {
    const g = grid(costCentreSummarySheet(data));
    expect(g[0]).toEqual([
      "Cost centre",
      "Own Dr",
      "Own Cr",
      "Total Dr",
      "Total Cr",
      "Total net",
    ]);
    const sales = g[1]!;
    // The whole point: Sales holds 1,000 of its OWN and rolls up 3,000.
    expect(sales[1]).toBe("1000.0000");
    expect(sales[3]).toBe("3000.0000");
  });

  it("indents a child so the hierarchy survives a flat file", () => {
    const g = grid(costCentreSummarySheet(data));
    expect(g[1]![0]).toBe("Sales");
    expect(g[2]![0]).toBe("  North");
  });

  it("exports the UNATTACHED centres, flagged and excluded from the total", () => {
    const g = grid(costCentreSummarySheet(data));
    const names = g.map((r) => r[0]);
    expect(names).toContain("UNATTACHED - Orphan");
    expect(names).toContain("UNATTACHED total (NOT included in Total above)");
    const total = g.find((r) => r[0] === "Total")!;
    expect(total[3]).toBe("3000.0000"); // the server's roll-up, orphan excluded
  });
});

// ---------------------------------------------------------------------------

describe("costCategorySummarySheet", () => {
  const data: CostCategorySummaryResponse = {
    from: "2026-04-01",
    to: "2026-06-30",
    rows: [
      {
        cost_category: "Department",
        allocable_debit: "10000.0000",
        allocated_debit: "7500.0000",
        unallocated_debit: "2500.0000",
        allocable_postings: 8,
        allocated_postings: 6,
        partly_allocated_postings: 1,
        off_axis_postings: 0,
        fully_allocated: false,
      },
    ],
  } as unknown as CostCategorySummaryResponse;

  it("is rectangular", () => {
    expectRectangular(costCategorySummarySheet(data));
  });

  it("carries the GAP as its own column and the verdict in words", () => {
    const g = grid(costCategorySummarySheet(data));
    expect(g[0]![3]).toBe("UNALLOCATED");
    expect(g[1]![3]).toBe("2500.0000");
    expect(g[1]![5]).toBe("GAP");
    expect(g[1]![4]).toBe("6/8");
  });

  it("carries the DO-NOT-TOTAL warning INTO the file", () => {
    // ⛔ The screen states it in a bordered panel under the table. A file has no
    // panel, and a flat file is exactly where somebody drags a SUM() across the
    // bottom — so the sentence travels with the rows.
    const g = grid(costCategorySummarySheet(data));
    expect(g[g.length - 1]![0]).toMatch(/must NOT be added up/);
  });
});
