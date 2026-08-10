import { describe, it, expect } from "vitest";

import { applyReportView, reportSheet } from "./reportView";
import type { DrillRow } from "./DrillRows";
import { moneyCell, textCell, toCsv, parseCsv } from "../../export/sheet";

/**
 * reportView.test.ts — THE CONDENSED VIEW, AND THE ROW IT USED TO DELETE.
 *
 * ⛔ WHAT WAS DRIVEN, AND WHY THESE FIXTURES LOOK THE WAY THEY DO.
 *
 * On 2026-08-07 a Balance Sheet was opened in a browser with no F12 and no
 * Alt+F1 — the view every operator gets by default — and its **Closing Stock**
 * line was not on the screen. Nor was the Stock-in-Hand group above it. Alt+E
 * wrote the rows on screen, so the CSV was short the same line: a statement
 * whose Current Assets subtotal contained 72,837.15 of stock that appeared on no
 * row of the report or of its export. The row with the SAME NAME on the Profit
 * & Loss — appended at section level, so depth 1 — was visible the whole time.
 *
 * The fixtures below are those two trees, cut down to the shape that matters:
 *
 *   Balance Sheet          Profit & Loss
 *     Assets        d0       Income          d0
 *       Current A.  d1         Sales         d1
 *         Stock-in  d2         Closing Stock d1   <- COMPUTED, always visible
 *           Closing d3  <- COMPUTED, was invisible
 *
 * ⛔ RULE 4, HERE. Every assertion below reads a VALUE and a NAME, never a row
 * count and never a bare "the report is not empty": a condensed view that
 * dropped the amount and kept the label would satisfy a count, and the figure
 * is the entire reason the row exists.
 */

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

/**
 * One statement row. `cells` is null because nothing here renders — the view
 * layer is pure, which is why it can be tested without a DOM at all.
 */
function row(
  rowKey: string,
  depth: number,
  name: string,
  amount: string,
  opts: { computed?: boolean; wire?: string } = {},
): DrillRow {
  return {
    rowKey,
    depth,
    drillable: opts.computed ? false : undefined,
    // `values` is the DISPLAY text (Indian-grouped, two decimals) and
    // `exportCells` the WIRE figure (fixed-4). Keeping them different in the
    // fixture is deliberate: it is the only way an assertion can tell which of
    // the two a code path actually read.
    values: [name, amount],
    exportCells: [
      textCell(name),
      moneyCell(opts.wire ?? amount.replace(/,/g, "") + "00"),
    ],
    cells: null,
  };
}

/** The Balance Sheet's Assets side, down to the derived Closing Stock leaf. */
const BALANCE_SHEET: DrillRow[] = [
  row("__assets__", 0, "Assets", "3,00,000.00"),
  row("group:Current Assets", 1, "Current Assets", "1,00,000.00"),
  row("group:Stock-in-Hand", 2, "Stock-in-Hand", "72,837.15"),
  row("computed:Closing Stock", 3, "Closing Stock", "72,837.15", {
    computed: true,
    wire: "72837.1541",
  }),
  row("group:Bank Accounts", 2, "Bank Accounts", "27,162.85"),
  row("ledger:HDFC Current", 3, "HDFC Current", "27,162.85"),
];

/** The P&L's Income side, where the identically named row lives at depth 1. */
const PROFIT_AND_LOSS: DrillRow[] = [
  row("__income__", 0, "Income", "5,00,000.00"),
  row("group:Sales Accounts", 1, "Sales Accounts", "4,27,162.85"),
  row("ledger:Sales", 2, "Sales", "4,27,162.85"),
  row("computed:Closing Stock", 1, "Closing Stock", "72,837.15", {
    computed: true,
  }),
];

/** The row named, with its amount — or null when the view dropped it. */
function find(rows: readonly DrillRow[], name: string): string | null {
  const hit = rows.find((r) => (r.values ?? [])[0] === name);
  return hit ? ((hit.values ?? [])[1] ?? null) : null;
}

// ---------------------------------------------------------------------------

describe("applyReportView — the condensed (default) view", () => {
  it("keeps the Balance Sheet's derived Closing Stock, WITH ITS AMOUNT, at depth 3", () => {
    // ⛔ THE MUTATION PROOF LIVES HERE. Restore the old
    // `out.filter((r) => (r.depth ?? 0) <= 1)` and this line fails with
    // "expected null to be 72,837.15" — which is exactly what the browser
    // showed: the figure was gone, not merely un-indented.
    const visible = applyReportView(BALANCE_SHEET, {});
    expect(find(visible, "Closing Stock")).toBe("72,837.15");
  });

  it("brings the group that SUBTOTALS it back with it, so the tree still reads", () => {
    // A kept leaf under a dropped parent prints beneath a group that is not its
    // parent — the reader then attributes the stock to Current Assets directly,
    // and the subtotal that contains it is nowhere to be compared against.
    const visible = applyReportView(BALANCE_SHEET, {});
    expect(find(visible, "Stock-in-Hand")).toBe("72,837.15");
    expect(find(visible, "Current Assets")).toBe("1,00,000.00");
    expect(find(visible, "Assets")).toBe("3,00,000.00");
    // ...and in TREE ORDER, parent before child.
    const names = visible.map((r) => (r.values ?? [])[0]);
    expect(names.indexOf("Stock-in-Hand")).toBeLessThan(
      names.indexOf("Closing Stock"),
    );
  });

  it("STILL CONDENSES a drillable row deeper than 1 — the control", () => {
    // Without this, "condensed" could have been fixed by showing everything,
    // which is indistinguishable from a working condense until an operator
    // opens a 400-ledger chart. A ledger at depth 3 is reachable by drilling
    // its group; the computed row above is reachable by nothing.
    const visible = applyReportView(BALANCE_SHEET, {});
    expect(find(visible, "HDFC Current")).toBeNull();
    // Its parent group is at depth 2 and is kept only because the ancestor walk
    // does not reach it — nothing under it survived. Assert that directly.
    expect(find(visible, "Bank Accounts")).toBeNull();
  });

  it("shows the SAME NAMED ROW on both statements in the default view — the class", () => {
    // D9's class in one assertion: a figure stated on two statements must not
    // be visible on one and absent on the other by an accident of tree depth.
    const bs = applyReportView(BALANCE_SHEET, {});
    const pl = applyReportView(PROFIT_AND_LOSS, {});
    expect(find(bs, "Closing Stock")).toBe("72,837.15");
    expect(find(pl, "Closing Stock")).toBe("72,837.15");
    expect(find(bs, "Closing Stock")).toBe(find(pl, "Closing Stock"));
  });

  it("Alt+E writes the row too, with the EXACT wire figure", () => {
    // The export writes the rows on screen, so the invisible row was also an
    // absent CSV row. Read the file back rather than the sheet object: the
    // encoder is what an operator actually receives.
    const visible = applyReportView(BALANCE_SHEET, {});
    const csv = parseCsv(
      toCsv(reportSheet("Balance Sheet", ["Particulars", "Amount"], visible)),
    );
    const line = csv.find((r) => r[0] === "Closing Stock");
    expect(line).toBeDefined();
    // The FIXED-4 wire figure, not the screen's grouped-and-truncated one: an
    // export that wrote "72,837.15" into a comma-separated file would have
    // shipped a delimiter into a field and thrown away two decimals.
    expect(line![1]).toBe("72837.1541");
  });

  it("Alt+F1 Detailed still expands EVERY level", () => {
    const visible = applyReportView(BALANCE_SHEET, { detailed: true });
    expect(find(visible, "HDFC Current")).toBe("27,162.85");
    expect(visible.length).toBe(BALANCE_SHEET.length);
  });
});

describe("applyReportView — the other two view buttons still hold", () => {
  it("F12 exclude-zero drops a zero GROUP and keeps a zero computed line", () => {
    const rows: DrillRow[] = [
      row("group:Empty", 1, "Suspense", "0.00"),
      row("computed:Difference", 1, "Difference in Opening Balances", "0.00", {
        computed: true,
      }),
    ];
    const visible = applyReportView(rows, { hideZeroBalances: true });
    expect(find(visible, "Suspense")).toBeNull();
    // A computed/total line states a FACT ABOUT THE REPORT and survives every
    // filter — the same carve-out the condensed view now makes.
    expect(find(visible, "Difference in Opening Balances")).toBe("0.00");
  });

  it("Ctrl+F filters drillable rows and keeps the computed ones", () => {
    const visible = applyReportView(BALANCE_SHEET, { filterText: "bank" });
    expect(find(visible, "Current Assets")).toBeNull();
    expect(find(visible, "Closing Stock")).toBe("72,837.15");
  });
});
