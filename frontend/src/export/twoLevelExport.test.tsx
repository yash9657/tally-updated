import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";

import { CompanyProvider } from "../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
} from "../app/ScreenActions";
import { createMockClient } from "../api/mock";
import { OutstandingsScreen } from "../screens/reports/ReportScreens";
import { stockSummarySheet } from "../screens/reports/StockSummaryScreen";
import { parseCsv, toCsv } from "./sheet";
import type { CoredClient, StockSummaryResponse } from "../api";
import type { NavSnapshot } from "../keystroke/navMachine";

/**
 * ⛔⛔ D109 — A TWO-LEVEL REPORT MUST DECLARE ITS LEVELS IN THE FILE, BECAUSE
 * THE FILE'S WHOLE PURPOSE IS TO BE SUMMED.
 *
 * Stock Summary and Outstandings each render a PARENT row (the item total, the
 * party total) followed by its CHILDREN (the godown positions, the open bills).
 * On screen the two are told apart by CSS and by nothing else — the parent's
 * `td` is `font-bold text-tally-title`, the child's is `pl-4` — and CSS does
 * not survive Alt+E. So the exported file carried a money column in which every
 * amount appeared TWICE, once on a parent and once on its child, with nothing
 * in the file saying so.
 *
 * ⭐ THE MEASUREMENT THAT REGISTERED IT (2026-08-31, by opening the Alt+E output
 * rather than the API): Stock Summary's four data rows summed to 177,037.0908
 * against the file's own `Stock-in-Hand` row of 88,518.5454 — a ratio of exactly
 * 2.000000 — and Outstandings summed to 555,040 against its own `Total` of
 * 277,520. The fixtures below reproduce that shape rather than quoting it.
 *
 * ⛔ WHAT THE FIX IS AND WHAT IT IS NOT. The duplication is CORRECT: an item
 * total and its locations are both facts, and the screen shows both on purpose.
 * What was missing is the DECLARATION — a Level column naming each row's kind —
 * so a spreadsheet can sum one level and get the right answer. The naive
 * whole-column sum is STILL 2x after the fix, and the control below asserts
 * exactly that, because a fix that changed it would have deleted a row the
 * report deliberately shows.
 *
 * ⚠️ EVERY SUM HERE IS IN INTEGER PAISE (BigInt), never a JS float. Summing a
 * money column with `+` is how a test about exactness stops being one.
 */

// ---------------------------------------------------------------------------
// Exact money, so the assertions are about the file and not about float64.

/** "21843.5454" -> 218435454n (fixed-4 wire scale). Refuses anything else. */
function units(s: string): bigint {
  const m = /^(-?)(\d+)\.(\d{4})$/.exec(s.trim());
  if (!m) throw new Error(`not a fixed-4 money string: ${JSON.stringify(s)}`);
  return BigInt(`${m[1]}${m[2]}${m[3]}`);
}

function sumUnits(values: readonly string[]): bigint {
  return values.reduce((acc, v) => acc + units(v), 0n);
}

/** The grid an operator opens, from the bytes the export actually writes. */
function gridOf(sheet: Parameters<typeof toCsv>[0]): string[][] {
  return parseCsv(toCsv(sheet));
}

/**
 * Read a column by HEADER NAME. Never by index: this suite exists because a
 * column was ADDED, and a test that hardcodes positions cannot survive that —
 * which is the same blindness as the defect.
 */
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

/** The rows of `grid` whose Level cell is `level`, as their money strings. */
function moneyAtLevel(
  grid: string[][],
  moneyHeader: string,
  level: string,
): string[] {
  const levels = column(grid, "Level");
  const money = column(grid, moneyHeader);
  return money.filter((_, i) => levels[i] === level);
}

// ---------------------------------------------------------------------------

describe("D109 — Stock Summary exports its two levels declared", () => {
  /**
   * Two items, each with ONE location holding the whole of it — the shape the
   * register measured, where every parent is duplicated by exactly one child.
   */
  const twoItems: StockSummaryResponse = {
    as_on: "2026-07-31",
    groups: [],
    rows: [],
    total_value: "88518.5454",
    items: [
      {
        stock_item_id: "i1",
        item: "MS Angle 50x50",
        unit: "Kgs",
        valuation_method: "WEIGHTED_AVG",
        stock_group: "",
        opening_qty: "0.0000",
        opening_value: "0.0000",
        inward_qty: "505.0000",
        inward_value: "21843.5454",
        outward_qty: "0.0000",
        outward_value: "0.0000",
        closing_qty: "505.0000",
        closing_value: "21843.5454",
        base_unit: "",
        base_qty: "",
        positions: [
          {
            stock_item_id: "i1",
            item: "MS Angle 50x50",
            unit: "Kgs",
            godown_id: "g1",
            godown: "Main Location",
            batch_id: "",
            batch: "",
            valuation_method: "WEIGHTED_AVG",
            stock_group: "",
            opening_qty: "0.0000",
            opening_value: "0.0000",
            inward_qty: "505.0000",
            inward_value: "21843.5454",
            outward_qty: "0.0000",
            outward_value: "0.0000",
            closing_qty: "505.0000",
            closing_value: "21843.5454",
            closing_rate: "43.2545",
          },
        ],
      },
      {
        stock_item_id: "i2",
        item: "MS Plate 10mm",
        unit: "Kgs",
        valuation_method: "WEIGHTED_AVG",
        stock_group: "",
        opening_qty: "0.0000",
        opening_value: "0.0000",
        inward_qty: "1200.0000",
        inward_value: "66675.0000",
        outward_qty: "0.0000",
        outward_value: "0.0000",
        closing_qty: "1200.0000",
        closing_value: "66675.0000",
        base_unit: "",
        base_qty: "",
        positions: [
          {
            stock_item_id: "i2",
            item: "MS Plate 10mm",
            unit: "Kgs",
            godown_id: "g1",
            godown: "Main Location",
            batch_id: "",
            batch: "",
            valuation_method: "WEIGHTED_AVG",
            stock_group: "",
            opening_qty: "0.0000",
            opening_value: "0.0000",
            inward_qty: "1200.0000",
            inward_value: "66675.0000",
            outward_qty: "0.0000",
            outward_value: "0.0000",
            closing_qty: "1200.0000",
            closing_value: "66675.0000",
            closing_rate: "55.5625",
          },
        ],
      },
    ],
  };

  it("⛔ names the KIND of every row, so the money column can be summed", () => {
    const g = gridOf(stockSummarySheet(twoItems, "2026-07-31"));
    const levels = column(g, "Level");

    // Not one blank. A row with no level is a row a spreadsheet cannot filter
    // out, which is the whole defect one row at a time.
    expect(levels.every((l) => l !== "")).toBe(true);
    expect(levels).toEqual(["Item", "Location", "Item", "Location", "Total"]);
  });

  it("⛔⛔ summing ONE level equals the file's own total, to the paisa", () => {
    const g = gridOf(stockSummarySheet(twoItems, "2026-07-31"));
    const stated = moneyAtLevel(g, "Closing Value", "Total");
    expect(stated).toEqual(["88518.5454"]);

    // The item level foots to the stated total…
    expect(sumUnits(moneyAtLevel(g, "Closing Value", "Item"))).toBe(
      units("88518.5454"),
    );
    // …and so does the location level. Two answers, one figure: that is what
    // makes the file summable instead of merely printable.
    expect(sumUnits(moneyAtLevel(g, "Closing Value", "Location"))).toBe(
      units("88518.5454"),
    );
  });

  it("⛔ names the ITEM each location belongs to (D118's Parent column)", () => {
    const g = gridOf(stockSummarySheet(twoItems, "2026-07-31"));
    const parents = column(g, "Parent");
    const levels = column(g, "Level");
    // A location's parent is its item; an item total and the footer are
    // nobody's child.
    expect(parents).toEqual([
      "",
      "MS Angle 50x50",
      "",
      "MS Plate 10mm",
      "",
    ]);
    // ⛔ AND THE PAIR IS WHAT MAKES ONE ITEM CHECKABLE ON ITS OWN. Summing the
    // locations under an item equals that item's own line — the question `Level`
    // alone could not answer.
    const closing = column(g, "Closing Value");
    const underAngle = closing.filter(
      (_, i) => parents[i] === "MS Angle 50x50" && levels[i] === "Location",
    );
    expect(sumUnits(underAngle)).toBe(units("21843.5454"));
  });

  it("⚠️ CONTROL: the whole column is still 2x, and that is correct", () => {
    // The duplication is the REPORT, not the bug: an item total and its
    // locations are both facts the screen shows on purpose. A "fix" that made
    // this sum equal the total would have deleted one of the two levels.
    const g = gridOf(stockSummarySheet(twoItems, "2026-07-31"));
    const data = column(g, "Closing Value").filter((_, i) => {
      const levels = column(g, "Level");
      return levels[i] !== "Total";
    });
    expect(sumUnits(data)).toBe(units("88518.5454") * 2n);
  });
});

// ---------------------------------------------------------------------------

const SNAP: NavSnapshot = {
  screen: "report",
  reportName: "Outstandings",
  screenStackDepth: 2,
  canReturnToVoucher: false,
};

/**
 * Render a report screen and press its Alt+E handler the way AppShell does,
 * capturing the BYTES the download helper wrote. Nothing about the encoding is
 * mocked — the Blob is the real Blob the real code built.
 */
let downloads: { filename: string; text: string }[];
let restore: (() => void)[];

beforeEach(() => {
  downloads = [];
  restore = [];
  const blobs = new Map<string, Blob>();
  let n = 0;
  const prevCreate = (URL as unknown as { createObjectURL?: unknown })
    .createObjectURL;
  const prevRevoke = (URL as unknown as { revokeObjectURL?: unknown })
    .revokeObjectURL;
  (URL as unknown as { createObjectURL: unknown }).createObjectURL = (
    blob: Blob,
  ) => {
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

describe("D109 — Outstandings exports its two levels declared", () => {
  /**
   * Two parties, each with two bills that net to the party's own total: the
   * measured shape, where the party row is duplicated by exactly its children.
   */
  const twoParties = () =>
    vi.fn(async () => ({
      kind: "RECEIVABLE" as const,
      as_on: "2026-06-30",
      aged_as_on: "2026-06-30",
      parties: [
        {
          ledger_id: "led-acme",
          ledger: "Acme",
          group: "Sundry Debtors",
          pending: "177520.0000",
          bills: [
            {
              ref_name: "Sales/2026-27/4",
              pending: "165720.0000",
              due_date: "2026-05-20",
              overdue_days: 41,
              slab: "0-45 days",
              on_account: false,
              currency_code: "",
              foreign_pending: "0.0000",
              currency_conflict: false,
            },
            {
              // ⛔ THE BILL WITH NO DUE DATE. Its empty second cell is exactly
              // where the old CONTENT discriminator (a group name on a party
              // row, a date on a bill row) failed outright.
              ref_name: "Sales/2026-27/9",
              pending: "11800.0000",
              due_date: "",
              overdue_days: 0,
              slab: "0-45 days",
              on_account: false,
              currency_code: "",
              foreign_pending: "0.0000",
              currency_conflict: false,
            },
          ],
        },
        {
          ledger_id: "led-borax",
          ledger: "Borax Traders",
          group: "Sundry Debtors",
          pending: "100000.0000",
          bills: [
            {
              ref_name: "Sales/2026-27/11",
              pending: "100000.0000",
              due_date: "2026-06-01",
              overdue_days: 29,
              slab: "0-45 days",
              on_account: false,
              currency_code: "",
              foreign_pending: "0.0000",
              currency_conflict: false,
            },
          ],
        },
      ],
      slab_totals: [],
      total: "277520.0000",
      on_account_total: "0.0000",
    }));

  async function exportOutstandings(): Promise<string[][]> {
    const base = createMockClient({ seed: false });
    const client = {
      ...base,
      getOutstandings: twoParties(),
    } as unknown as CoredClient;

    let fire: () => string | Promise<string> = () => "";
    function Harness() {
      const dispatch = useScreenActionDispatcher();
      fire = () => {
        const res = dispatch("export", SNAP);
        return res.pending ? (res.pending as Promise<string>) : (res.message ?? "");
      };
      return <OutstandingsScreen snapshot={SNAP} onSelect={() => {}} />;
    }

    render(
      <CompanyProvider
        client={client}
        initialCompany={{ id: "co-1", name: "Test Co" }}
      >
        <ScreenActionsProvider>
          <Harness />
        </ScreenActionsProvider>
      </CompanyProvider>,
    );
    await waitFor(() => expect(screen.getByText("Acme")).toBeTruthy());

    // ⛔ THE CONTROL: nothing was written before the key.
    expect(downloads).toHaveLength(0);
    await fire();
    expect(downloads).toHaveLength(1);
    return parseCsv(downloads[0]!.text);
  }

  it("⛔ names the KIND of every row, including the bill with no due date", async () => {
    const g = await exportOutstandings();
    expect(column(g, "Level")).toEqual([
      "Party",
      "Bill",
      "Bill",
      "Party",
      "Bill",
      "Total",
    ]);
  });

  it("⛔⛔ summing ONE level equals the file's own total, to the paisa", async () => {
    const g = await exportOutstandings();
    expect(moneyAtLevel(g, "Pending", "Total")).toEqual(["277520.0000"]);
    expect(sumUnits(moneyAtLevel(g, "Pending", "Party"))).toBe(
      units("277520.0000"),
    );
    expect(sumUnits(moneyAtLevel(g, "Pending", "Bill"))).toBe(
      units("277520.0000"),
    );
  });

  it("⛔ names the PARTY each bill belongs to (D118's Parent column)", async () => {
    const g = await exportOutstandings();
    expect(column(g, "Parent")).toEqual([
      "",
      "Acme",
      "Acme",
      "",
      "Borax Traders",
      "",
    ]);
    // ⭐ AND ONE PARTY'S BILLS FOOT AGAINST THAT PARTY'S OWN LINE — the question
    // `Level` alone cannot answer, and the one an operator chasing a single
    // customer actually asks.
    const parents = column(g, "Parent");
    const pending = column(g, "Pending");
    const acme = pending.filter((_, i) => parents[i] === "Acme");
    expect(sumUnits(acme)).toBe(units("177520.0000"));
  });

  it("⚠️ CONTROL: the whole column is still 2x, and that is correct", async () => {
    const g = await exportOutstandings();
    const levels = column(g, "Level");
    const data = column(g, "Pending").filter((_, i) => levels[i] !== "Total");
    expect(sumUnits(data)).toBe(units("277520.0000") * 2n);
  });
});
