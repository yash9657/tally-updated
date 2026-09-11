import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";

import { CompanyProvider } from "../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
} from "../app/ScreenActions";
import { createMockClient } from "../api/mock";
import {
  BalanceSheetScreen,
  ProfitLossScreen,
  TrialBalanceScreen,
} from "../screens/reports/ReportScreens";
import { parseCsv } from "./sheet";
import type { CoredClient } from "../api";
import type { NavSnapshot } from "../keystroke/navMachine";

/**
 * ⛔⛔ D118 — A STATEMENT TREE MUST SAY WHOSE TOTAL EACH ROW IS PART OF.
 *
 * D109 closed the two-level reports (Stock Summary, Outstandings) with a `Level`
 * column. The STATEMENT trees have the same disease and a worse version of it,
 * measured 2026-09-04 by opening the files Alt+E writes on the demo book:
 *
 *   Balance-Sheet.csv    Σ Amount = 3,135,449.4546  vs a section total of 746,314.9091
 *                        — a ratio of 4.2013…, because the tree is three deep
 *   Profit-and-Loss.csv  Σ Amount =   837,168.7273  vs Income 224,594.9091 + Expenses 163,384.0000
 *   Trial-Balance.csv    Σ Debit  = 1,708,730.2400  = 2 × its own Total row, which is unmarked
 *
 * ⛔ WORSE IN THE ONE WAY THAT MATTERS: D109's exact 2.000000 is at least
 * suspicious to a reader. 4.2 is not — and the Balance Sheet has NO total row of
 * its own (its section headers ARE the totals, unmarked), so unlike D109's two
 * reports the file carries no stated figure to contradict itself with.
 *
 * ⭐ THE FIX IS TWO COLUMNS AND THE PAIR IS THE ANSWER (see
 * docs/design/d118-statement-tree-export.md): `Level` says WHAT a row is, so
 * headers and totals can be excluded; `Parent` says WHOSE TOTAL its amount is
 * part of, so a group at any depth can be summed without knowing the tree.
 *
 * ⚠️ EVERY SUM HERE IS IN INTEGER PAISE (BigInt). Summing a money column with
 * `+` is how a test about exactness stops being one.
 */

// ---------------------------------------------------------------------------

function units(s: string): bigint {
  const m = /^(-?)(\d+)\.(\d{4})$/.exec(s.trim());
  if (!m) throw new Error(`not a fixed-4 money string: ${JSON.stringify(s)}`);
  return BigInt(`${m[1]}${m[2]}${m[3]}`);
}
const sumUnits = (v: readonly string[]) => v.reduce((a, x) => a + units(x), 0n);

function column(grid: string[][], header: string): string[] {
  const i = grid[0]!.indexOf(header);
  if (i < 0) {
    throw new Error(
      `no ${JSON.stringify(header)} column in the export; headers are ` +
        JSON.stringify(grid[0]),
    );
  }
  return grid.slice(1).map((r) => r[i] ?? "");
}

/** The rows of `grid` whose Parent cell is `parent`, as their money strings. */
function moneyUnder(grid: string[][], moneyHeader: string, parent: string) {
  const parents = column(grid, "Parent");
  return column(grid, moneyHeader).filter((_, i) => parents[i] === parent);
}

/** The money on the ONE row with this label. */
function rowAmount(grid: string[][], label: string, moneyHeader: string) {
  const labels = column(grid, grid[0]![0]!);
  const i = labels.indexOf(label);
  if (i < 0) throw new Error(`no row labelled ${JSON.stringify(label)}`);
  return column(grid, moneyHeader)[i]!;
}

// ---------------------------------------------------------------------------
// The download-capturing harness: the bytes the real code wrote.

let downloads: { filename: string; text: string }[];
let restore: (() => void)[];

beforeEach(() => {
  downloads = [];
  restore = [];
  const blobs = new Map<string, Blob>();
  let n = 0;
  const prevCreate = (URL as unknown as { createObjectURL?: unknown }).createObjectURL;
  const prevRevoke = (URL as unknown as { revokeObjectURL?: unknown }).revokeObjectURL;
  (URL as unknown as { createObjectURL: unknown }).createObjectURL = (blob: Blob) => {
    const url = `blob:mock/${(n += 1)}`;
    blobs.set(url, blob);
    return url;
  };
  (URL as unknown as { revokeObjectURL: unknown }).revokeObjectURL = () => {};
  restore.push(() => {
    (URL as unknown as { createObjectURL: unknown }).createObjectURL = prevCreate;
    (URL as unknown as { revokeObjectURL: unknown }).revokeObjectURL = prevRevoke;
  });

  const proto = HTMLAnchorElement.prototype;
  const prevClick = proto.click;
  proto.click = function patchedClick(this: HTMLAnchorElement) {
    const blob = blobs.get(this.getAttribute("href") ?? "");
    if (blob) {
      downloads.push({
        filename: this.download,
        text: (blob as Blob & { __text?: string }).__text ?? "",
      });
    }
  };
  restore.push(() => {
    proto.click = prevClick;
  });

  const RealBlob = globalThis.Blob;
  class RecordingBlob extends RealBlob {
    __text: string;
    constructor(parts: BlobPart[] = [], options?: BlobPropertyBag) {
      super(parts, options);
      this.__text = parts.map((p) => String(p)).join("");
    }
  }
  (globalThis as unknown as { Blob: unknown }).Blob = RecordingBlob;
  restore.push(() => {
    (globalThis as unknown as { Blob: unknown }).Blob = RealBlob;
  });
});

afterEach(() => {
  cleanup();
  for (const undo of restore.reverse()) undo();
  vi.restoreAllMocks();
});

/**
 * A three-deep Balance Sheet whose Current Assets branch reproduces the exact
 * shape that makes the condensed view PARTIAL: three drillable groups that the
 * condensed view drops, plus a `Stock-in-Hand` group kept only because the
 * COMPUTED `Closing Stock` under it must never be dropped.
 */
const BALANCE_SHEET = {
  as_on: "2026-07-31",
  balanced: true,
  net_profit: "61210.9091",
  liabilities: {
    title: "Liabilities",
    total: "746314.9091",
    nodes: [
      {
        kind: "GROUP" as const,
        name: "Capital Account",
        amount: "502000.0000",
        children: [
          { kind: "LEDGER" as const, name: "Capital A/c", amount: "502000.0000" },
        ],
      },
      {
        kind: "GROUP" as const,
        name: "Current Liabilities",
        amount: "183104.0000",
        children: [
          { kind: "GROUP" as const, name: "Duties & Taxes", amount: "5558.8800" },
          { kind: "GROUP" as const, name: "Sundry Creditors", amount: "177545.1200" },
        ],
      },
      {
        kind: "COMPUTED" as const,
        name: "Profit & Loss A/c (cumulative to the latest date posted)",
        amount: "61210.9091",
      },
    ],
  },
  assets: {
    title: "Assets",
    total: "746314.9091",
    nodes: [
      {
        kind: "GROUP" as const,
        name: "Current Assets",
        amount: "746314.9091",
        children: [
          { kind: "GROUP" as const, name: "Bank Accounts", amount: "370000.0000" },
          { kind: "GROUP" as const, name: "Cash-in-Hand", amount: "41400.0000" },
          {
            kind: "GROUP" as const,
            name: "Stock-in-Hand",
            amount: "75094.9091",
            children: [
              { kind: "COMPUTED" as const, name: "Closing Stock", amount: "75094.9091" },
            ],
          },
          { kind: "GROUP" as const, name: "Sundry Debtors", amount: "259820.0000" },
        ],
      },
    ],
  },
};

const PROFIT_LOSS = {
  from: "2026-04-01",
  to: "2026-07-31",
  net_profit: "61210.9091",
  expenses: {
    title: "Expenses",
    total: "163384.0000",
    nodes: [
      { kind: "GROUP" as const, name: "Indirect Expenses", amount: "53600.0000" },
      { kind: "GROUP" as const, name: "Purchase Accounts", amount: "109784.0000" },
    ],
  },
  income: {
    title: "Income",
    total: "224594.9091",
    nodes: [
      { kind: "GROUP" as const, name: "Sales Accounts", amount: "149500.0000" },
      { kind: "COMPUTED" as const, name: "Closing Stock", amount: "75094.9091" },
    ],
  },
};

const TRIAL_BALANCE = {
  as_on: "2026-07-31",
  total_debit: "854365.1200",
  total_credit: "854365.1200",
  rows: [
    { ledger: "HDFC Bank", group: "Bank Accounts", debit: "370000.0000", credit: "0.0000" },
    { ledger: "Capital A/c", group: "Capital Account", debit: "0.0000", credit: "502000.0000" },
    { ledger: "Acme Traders", group: "Sundry Debtors", debit: "484365.1200", credit: "0.0000" },
    { ledger: "Steel Suppliers", group: "Sundry Creditors", debit: "0.0000", credit: "352365.1200" },
  ],
};

// ---------------------------------------------------------------------------

const SNAP: NavSnapshot = {
  screen: "report",
  reportName: "Report",
  screenStackDepth: 2,
  canReturnToVoucher: false,
};

async function exportOf(
  Comp: React.ComponentType<{
    snapshot: NavSnapshot;
    onSelect: (s: string) => void;
  }>,
  over: Partial<CoredClient>,
  waitFor1: string,
  snapshot: NavSnapshot = SNAP,
): Promise<{ filename: string; grid: string[][] }> {
  const base = createMockClient({ seed: false });
  const client = { ...base, ...over } as CoredClient;

  let fire: () => string | Promise<string> = () => "";
  function Harness() {
    const dispatch = useScreenActionDispatcher();
    fire = () => {
      const res = dispatch("export", snapshot);
      return res.pending ? (res.pending as Promise<string>) : (res.message ?? "");
    };
    return <Comp snapshot={snapshot} onSelect={() => {}} />;
  }
  render(
    <CompanyProvider client={client} initialCompany={{ id: "co-1", name: "Test Co" }}>
      <ScreenActionsProvider>
        <Harness />
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
  await waitFor(() => expect(screen.getByText(waitFor1)).toBeTruthy());

  // ⛔ THE CONTROL: nothing was written before the key.
  expect(downloads).toHaveLength(0);
  await fire();
  expect(downloads).toHaveLength(1);
  return {
    filename: downloads[0]!.filename,
    grid: parseCsv(downloads[0]!.text),
  };
}

const balanceSheet = (snapshot?: NavSnapshot) =>
  exportOf(
    BalanceSheetScreen,
    { getBalanceSheet: vi.fn(async () => BALANCE_SHEET) } as unknown as Partial<CoredClient>,
    "Current Assets",
    snapshot,
  );

// ===========================================================================

describe("D118 — the Balance Sheet declares its levels and its parents", () => {
  it("⛔ every row says WHAT it is", async () => {
    const { grid } = await balanceSheet();
    const levels = column(grid, "Level");
    expect(levels.every((l) => l !== "")).toBe(true);
    // Section headers, the groups under them, and the COMPUTED rows that are
    // neither a group nor a ledger.
    expect(new Set(levels)).toEqual(new Set(["Section", "Group", "Computed"]));
  });

  it("⛔⛔ SUMIF(Parent, <section>) equals that section's own row, to the paisa", async () => {
    const { grid } = await balanceSheet();

    // ⭐ THE SUM THIS COLUMN EXISTS FOR. Before it, the only sum a reader could
    // take over this file was the whole column — 4.2× the answer.
    expect(sumUnits(moneyUnder(grid, "Amount", "Liabilities"))).toBe(
      units(rowAmount(grid, "Liabilities", "Amount")),
    );
    expect(sumUnits(moneyUnder(grid, "Amount", "Assets"))).toBe(
      units(rowAmount(grid, "Assets", "Amount")),
    );
    // The section rows themselves are at the top and belong to nothing.
    const parents = column(grid, "Parent");
    const levels = column(grid, "Level");
    levels.forEach((l, i) => {
      if (l === "Section") expect(parents[i]).toBe("");
    });
  });

  it("⚠️ CONTROL: the whole money column is still 4.2x, and that is correct", async () => {
    // The nesting IS the statement. A fix that made the naive whole-column sum
    // foot would have deleted a level of it — the same control D109 carries.
    const { grid } = await balanceSheet();
    const all = sumUnits(column(grid, "Amount"));
    const section = units(rowAmount(grid, "Liabilities", "Amount"));
    expect(all).toBeGreaterThan(section * 4n);
    expect(all).toBeLessThan(section * 5n);
  });

  it("⛔⛔ the FILENAME says which view produced the file", async () => {
    // A condensed file and a detailed file are different documents, and "no
    // suffix" is exactly the silence that makes a condensed one look complete.
    const { filename } = await balanceSheet();
    expect(filename).toBe("Balance-Sheet-condensed.csv");
  });

  it("⛔⛔ …and the DETAILED view is a different file whose deep sums also foot", async () => {
    const detailed: NavSnapshot = { ...SNAP, config: { detailed: true } };
    const { filename, grid } = await balanceSheet(detailed);
    expect(filename).toBe("Balance-Sheet-detailed.csv");

    // ⭐ THE MEASUREMENT THE SUFFIX EXISTS FOR. In the CONDENSED file the rows
    // under "Current Assets" are only `Stock-in-Hand` — the three drillable
    // siblings are dropped, and `Stock-in-Hand` survives only as the ancestor of
    // the COMPUTED `Closing Stock`, which must never be dropped. So a per-group
    // sum there is PARTIAL. In the detailed file it is complete.
    expect(sumUnits(moneyUnder(grid, "Amount", "Current Assets"))).toBe(
      units(rowAmount(grid, "Current Assets", "Amount")),
    );
    // …and a LEDGER row is now in the file, which the condensed view had dropped.
    expect(column(grid, "Level")).toContain("Ledger");
  });

  it("⚠️ the partial branch in the CONDENSED file is real, and is why the suffix is", async () => {
    // Stated as a measurement rather than left implicit: this is the number a
    // reader would get if they summed a deep group out of a condensed file.
    const { grid } = await balanceSheet();
    const under = sumUnits(moneyUnder(grid, "Amount", "Current Assets"));
    expect(under).toBe(units("75094.9091"));
    expect(under).not.toBe(units(rowAmount(grid, "Current Assets", "Amount")));
    // The top level is complete in BOTH views, which is what makes the section
    // sum above trustworthy in a condensed file.
    expect(sumUnits(moneyUnder(grid, "Amount", "Assets"))).toBe(
      units(rowAmount(grid, "Assets", "Amount")),
    );
  });
});

describe("D118 — the Profit & Loss declares its levels and its parents", () => {
  const pl = () =>
    exportOf(
      ProfitLossScreen,
      { getProfitLoss: vi.fn(async () => PROFIT_LOSS) } as unknown as Partial<CoredClient>,
      "Sales Accounts",
    );

  it("⛔ names Net Profit as COMPUTED, not as a section", async () => {
    const { grid } = await pl();
    const labels = column(grid, grid[0]![0]!);
    const levels = column(grid, "Level");
    // ⛔ IT IS NOT THE SUM OF ANYTHING IN THIS FILE — it is Income minus
    // Expenses — so calling it a Section or a Total would invite exactly the
    // sum that is wrong.
    expect(levels[labels.indexOf("Net Profit")]).toBe("Computed");
    expect(levels[labels.indexOf("Expenses")]).toBe("Section");
    expect(levels[labels.indexOf("Income")]).toBe("Section");
  });

  it("⛔⛔ each section foots against its own row", async () => {
    const { grid } = await pl();
    expect(sumUnits(moneyUnder(grid, "Amount", "Expenses"))).toBe(
      units(rowAmount(grid, "Expenses", "Amount")),
    );
    expect(sumUnits(moneyUnder(grid, "Amount", "Income"))).toBe(
      units(rowAmount(grid, "Income", "Amount")),
    );
  });
});

describe("D118 — the Trial Balance marks its Total row", () => {
  const tb = () =>
    exportOf(
      TrialBalanceScreen,
      { getTrialBalance: vi.fn(async () => TRIAL_BALANCE) } as unknown as Partial<CoredClient>,
      "HDFC Bank",
    );

  it("⛔ the Total row is no longer indistinguishable from a ledger named Total", async () => {
    const { grid } = await tb();
    const levels = column(grid, "Level");
    expect(new Set(levels)).toEqual(new Set(["Ledger", "Total"]));
    // The level agrees with the column header, which F5 already flips.
    expect(grid[0]![0]).toBe("Ledger");
  });

  it("⛔⛔ summing the ledger rows equals the file's own Total, to the paisa", async () => {
    const { grid } = await tb();
    // ⛔ THE FILTER IS `!== "Total"` AND NOT `=== "Ledger"`, DELIBERATELY. It is
    // how an operator would write it — exclude the total, sum the rest — and it
    // is the only form that catches an UNMARKED total: a total whose Level is
    // blank passes an `=== "Ledger"` filter by being excluded for the wrong
    // reason, and this suite measured that while mutation-proving.
    const levels = column(grid, "Level");
    const debit = column(grid, "Debit").filter((_, i) => levels[i] !== "Total");
    const credit = column(grid, "Credit").filter((_, i) => levels[i] !== "Total");
    expect(sumUnits(debit)).toBe(units("854365.1200"));
    expect(sumUnits(credit)).toBe(units("854365.1200"));
  });

  it("⚠️ CONTROL: the whole Debit column is still 2x, and that is correct", async () => {
    const { grid } = await tb();
    expect(sumUnits(column(grid, "Debit"))).toBe(units("854365.1200") * 2n);
  });

  it("⛔ a FLAT report's Parent column is empty, not invented", async () => {
    // Nothing on a Trial Balance is anybody's child. A Parent column that named
    // the group would be a different report — and would make the sum above
    // double-count the moment a group row appeared.
    const { grid } = await tb();
    expect(column(grid, "Parent").every((p) => p === "")).toBe(true);
  });
});
