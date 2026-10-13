import { describe, it, expect, afterEach, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";

import {
  StockSummaryScreen,
  stockSummaryScopeLine,
  stockSummaryDenialAdvice,
  stockSummarySheet,
} from "./StockSummaryScreen";
import { CompanyProvider } from "../../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
  type ScreenActionResult,
} from "../../app/ScreenActions";
import { createMockClient, ApiError } from "../../api";
import type { CoredClient, StockSummaryResponse } from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";
import { toCsv, parseCsv } from "../../export/sheet";

/**
 * StockSummaryScreen.test.tsx — THE THREE THINGS THIS SCREEN USED TO SAY THAT
 * WERE NOT TRUE.
 *
 *  1. "As at today (everything posted)" over a report with NO upper bound at
 *     all, which therefore includes vouchers dated after today (D45). Measured:
 *     72,837.15 under that caption where the position as at that day was
 *     96,168.18.
 *  2. "so the Data Entry level is denied it explicitly", on a screen that is
 *     never told which level the operator holds (D53).
 *  3. Seven columns with a QUANTITY under "Inward" beside MONEY under "Outward
 *     (at cost)" — and `inward_value` written down nowhere, on screen or in the
 *     export (D64), while the PRINTED report of the same response carried ten
 *     columns (D62).
 *
 * ⛔ RULE 4 GOVERNS EVERY FIXTURE BELOW. This repository has been burned by a
 * QUANTITY-ONLY oracle: a check that read counts stayed green while 1,000 of
 * stock VALUE was written off. So the fixtures come in PAIRS THAT AGREE IN
 * EVERY QUANTITY AND DIFFER IN VALUE, and the assertions read money, name the
 * column they read it from BY ITS HEADER, and never assert a bare total.
 */

afterEach(cleanup);

/**
 * jsdom has no Blob URLs and no navigation, so the download is OBSERVED rather
 * than faked: `URL.createObjectURL` keeps the real Blob the real code built and
 * the anchor's own click captures it. Nothing about the encoding is mocked —
 * this is the same harness exportKey.test.tsx uses, for the same reason.
 */
interface CapturedDownload {
  filename: string;
  text: string;
}
let downloads: CapturedDownload[] = [];
let restoreDownload: (() => void)[] = [];

beforeEach(() => {
  downloads = [];
  restoreDownload = [];
  const blobs = new Map<string, Blob>();
  let n = 0;
  const url = URL as unknown as Record<string, unknown>;
  const prevCreate = url.createObjectURL;
  const prevRevoke = url.revokeObjectURL;
  url.createObjectURL = (blob: Blob) => {
    const href = `blob:mock/${(n += 1)}`;
    blobs.set(href, blob);
    return href;
  };
  url.revokeObjectURL = () => {};
  restoreDownload.push(() => {
    url.createObjectURL = prevCreate;
    url.revokeObjectURL = prevRevoke;
  });

  const proto = HTMLAnchorElement.prototype;
  const prevClick = proto.click;
  const texts = new Map<Blob, string>();
  const prevBlobText = Blob.prototype.text;
  // The Blob is built synchronously from a string; keep that string so the
  // assertion can stay synchronous with the keypress.
  const prevBlob = globalThis.Blob;
  class CapturingBlob extends prevBlob {
    constructor(parts?: BlobPart[], options?: BlobPropertyBag) {
      super(parts, options);
      texts.set(this, (parts ?? []).map(String).join(""));
    }
  }
  globalThis.Blob = CapturingBlob as unknown as typeof Blob;
  proto.click = function patchedClick(this: HTMLAnchorElement) {
    const blob = blobs.get(this.getAttribute("href") ?? "");
    if (blob) {
      downloads.push({ filename: this.download, text: texts.get(blob) ?? "" });
    }
  };
  restoreDownload.push(() => {
    proto.click = prevClick;
    globalThis.Blob = prevBlob;
    Blob.prototype.text = prevBlobText;
  });
});

afterEach(() => {
  for (const undo of restoreDownload) undo();
});

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

/**
 * One item in one location. Every one of the eight figures is distinct, so an
 * assertion that reads the wrong cell reads a wrong number rather than a number
 * that happens to match.
 */
const WIDGET: StockSummaryResponse = {
  as_on: "2026-07-31",
  groups: [],
  total_value: "1200.0000",
  rows: [],
  items: [
    {
      stock_item_id: "s1",
      item: "Widget",
      unit: "Nos",
      valuation_method: "WEIGHTED_AVG",
      stock_group: "",
      opening_qty: "2.0000",
      opening_value: "300.0000",
      inward_qty: "10.0000",
      inward_value: "7860.0000",
      outward_qty: "4.0000",
      outward_value: "800.0000",
      closing_qty: "8.0000",
      closing_value: "1200.0000",
      base_unit: "",
      base_qty: "",
      positions: [
        {
          stock_item_id: "s1",
          item: "Widget",
          unit: "Nos",
          godown_id: "g1",
          godown: "Main Location",
          batch_id: "",
          batch: "",
          valuation_method: "WEIGHTED_AVG",
          stock_group: "",
          opening_qty: "2.0000",
          opening_value: "300.0000",
          inward_qty: "10.0000",
          inward_value: "7860.0000",
          outward_qty: "4.0000",
          outward_value: "800.0000",
          closing_qty: "8.0000",
          closing_value: "1200.0000",
          closing_rate: "150.0000",
        },
      ],
    },
  ],
};

/** The same response with no upper bound — the shape the caption lied about. */
const UNBOUNDED: StockSummaryResponse = { ...WIDGET, as_on: "" };

/** Deep-clone a response so a mutation in one test cannot leak into another. */
function clone(r: StockSummaryResponse): StockSummaryResponse {
  return JSON.parse(JSON.stringify(r)) as StockSummaryResponse;
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

/** Captures the dispatcher so a test can press Alt+E on the mounted screen. */
let fire: ((kind: "export") => ScreenActionResult) | null = null;

function ActionProbe() {
  const dispatch = useScreenActionDispatcher();
  fire = (kind) => dispatch(kind, {} as NavSnapshot);
  return null;
}

function renderStockSummary(
  getStockSummary: CoredClient["getStockSummary"],
  config?: NavSnapshot["config"],
) {
  fire = null;
  const base = createMockClient({ seed: false });
  const client = { ...base, getStockSummary } as CoredClient;
  const snapshot = {
    screen: "report",
    reportName: "Stock Summary",
    screenStackDepth: 2,
    canReturnToVoucher: false,
    config,
  } as NavSnapshot;
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <ScreenActionsProvider>
        <ActionProbe />
        <StockSummaryScreen snapshot={snapshot} />
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
}

/** The header index of a named column — assertions index BY NAME, never by 3. */
function col(gridRows: string[][], header: string): number {
  const i = gridRows[0]!.indexOf(header);
  expect(i, `no column headed "${header}" in [${gridRows[0]!.join(" | ")}]`)
    .toBeGreaterThanOrEqual(0);
  return i;
}

// ---------------------------------------------------------------------------
// D45 — the scope the report actually has
// ---------------------------------------------------------------------------

describe("stockSummaryScopeLine — what the report actually covers", () => {
  it("says NO DATE LIMIT, and names post-dated vouchers, when the server bounded nothing", () => {
    const line = stockSummaryScopeLine(undefined, "");
    // ⛔ THE MUTATION: restore `asOn ? \`As at ${asOn}\` : "As at today
    // (everything posted)"` and this fails on the first assertion — the screen
    // goes back to claiming a boundary the replay never applied.
    expect(line).toMatch(/no date limit/i);
    expect(line).toMatch(/after today/i);
    expect(line).not.toMatch(/as at today/i);
  });

  it("follows the SERVER's resolved date, not the F2 box", () => {
    // The property, not the example: whatever was ASKED FOR, the caption states
    // the boundary the REPLAY STOPPED AT. If the two ever diverge, the figures
    // belong to the server's date and so must the sentence over them.
    expect(stockSummaryScopeLine("2026-08-31", "2026-07-31")).toBe(
      "As at 2026-07-31",
    );
    // "" is an ANSWER (unbounded); undefined is "no answer yet", and only then
    // does the request stand in — so the caption never blinks between claims.
    expect(stockSummaryScopeLine("2026-08-31", undefined)).toBe(
      "As at 2026-08-31",
    );
    expect(stockSummaryScopeLine("2026-08-31", "")).toMatch(/no date limit/i);
  });

  it("renders that sentence on the screen, over the figures it describes", async () => {
    renderStockSummary(async () => UNBOUNDED);
    await screen.findByTestId("stock-item:Widget");
    const scope = screen.getByTestId("stock-summary-scope");
    expect(scope.textContent).toMatch(/no date limit/i);
    expect(scope.textContent).not.toMatch(/as at today/i);
    // The control: the same screen with a bounded response states the date.
    cleanup();
    renderStockSummary(async () => WIDGET);
    await screen.findByTestId("stock-item:Widget");
    expect(screen.getByTestId("stock-summary-scope").textContent).toContain(
      "As at 2026-07-31",
    );
  });

  it("Alt+E names the scope in the FILE NAME, from the server's answer", async () => {
    // RULE 3: the sentence above is only worth writing if a key reaches it. The
    // shell's routing of Alt+E to this screen is pinned in exportKey.test.tsx;
    // what is pinned here is that the registered handler builds its title from
    // `data.as_on` — the F2 box below says 2026-08-31 and the server says
    // 2026-07-31, so a title naming August would mean the file was named after
    // an intention rather than after the figures inside it.
    renderStockSummary(async () => WIDGET, { periodTo: "2026-08-31" });
    await screen.findByTestId("stock-item:Widget");
    fire!("export");
    expect(downloads).toHaveLength(1);
    expect(downloads[0]!.filename).toBe("Stock-Summary-as-at-2026-07-31.csv");
    expect(downloads[0]!.filename).not.toContain("2026-08-31");
    // The file itself carries the figures, so the name is not the only claim.
    expect(downloads[0]!.text).toContain("7860.0000");
  });

  it("...and says so on the unbounded file too, rather than leaving it undated", async () => {
    renderStockSummary(async () => UNBOUNDED);
    await screen.findByTestId("stock-item:Widget");
    fire!("export");
    expect(downloads).toHaveLength(1);
    expect(downloads[0]!.filename).toBe("Stock-Summary-no-date-limit.csv");
  });
});

// ---------------------------------------------------------------------------
// D53 — a refusal that names no level it was not told about
// ---------------------------------------------------------------------------

describe("stockSummaryDenialAdvice — derived from what the PDP said", () => {
  it("names a RULE to remove when the server says a rule denied it", () => {
    const advice = stockSummaryDenialAdvice("disallowed by rule");
    expect(advice).toMatch(/your own security level/i);
    expect(advice).toMatch(/removing/i);
    expect(advice).toContain("disallow display report:stock-summary");
  });

  it("says there is NO rule to delete when the level simply grants nothing", () => {
    // The two PDP refusals need opposite moves from an Owner. Told to delete a
    // rule that does not exist, an Owner searches a level that has no such row
    // and concludes the product is broken.
    const advice = stockSummaryDenialAdvice("default access: none");
    expect(advice).toMatch(/ADD/);
    expect(advice).toContain("allow display report:stock-summary");
    expect(advice).not.toMatch(/removing that level/i);
  });

  it("does NOT dress a membership refusal as a report permission", () => {
    const advice = stockSummaryDenialAdvice("not a member of this company");
    expect(advice).toMatch(/before any report rule/i);
    expect(advice).not.toContain("report:stock-summary");
  });

  it("NAMES NO SECURITY LEVEL, on any of the doors", () => {
    // ⛔ D53 IN ONE ASSERTION. The screen is never told which level the operator
    // holds — `CompanyInfo` carries id, name and state code and nothing else,
    // and the 403 body is one string. So no level name may appear in any
    // branch. Restore the hardcoded "the Data Entry level" and this fails.
    for (const reason of [
      "disallowed by rule",
      "default access: none",
      "not a member of this company",
      "account is inactive",
    ]) {
      expect(stockSummaryDenialAdvice(reason)).not.toMatch(/data entry/i);
      expect(stockSummaryDenialAdvice(reason)).not.toMatch(/owner level/i);
    }
  });

  it("the 403 panel quotes the server verbatim and states the derived advice", async () => {
    renderStockSummary(async () => {
      throw new ApiError(403, "default access: none", {});
    });
    const denied = await screen.findByTestId("stock-summary-denied");
    expect(denied.textContent).not.toMatch(/data entry/i);
    // Verbatim, for the reason reportView gives: a rewritten refusal reads like
    // an outage, and the operator's Owner needs the server's own words.
    expect(denied.textContent).toContain("default access: none");
    expect(
      screen.getByTestId("stock-summary-denied-advice").textContent,
    ).toMatch(/ADD/);
    // Still a policy answer and not a fault, and still no invented table.
    expect(screen.queryByTestId("stock-summary-error")).toBeNull();
    expect(screen.queryByTestId("stock-summary-table")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// D62 (screen half) + D64 — every quantity has its value beside it
// ---------------------------------------------------------------------------

describe("the Stock Summary columns", () => {
  it("puts Inward Qty and Inward Value in SEPARATE, LABELLED columns on screen", async () => {
    renderStockSummary(async () => WIDGET);
    const item = await screen.findByTestId("stock-item:Widget");
    const cells = [...item.querySelectorAll("td")].map((td) => td.textContent);
    const heads = [
      ...screen
        .getByTestId("stock-summary-table")
        .querySelectorAll("thead th"),
    ].map((th) => th.textContent);

    // The cost of everything that came in — 7,860.00 — was returned by the API
    // and displayed NOWHERE before this.
    expect(cells[heads.indexOf("Inward Value")]).toBe("7,860.00");
    expect(cells[heads.indexOf("Inward Qty")]).toBe("10.0000");
    // And the pair that used to sit adjacent with no unit between them.
    expect(cells[heads.indexOf("Outward Qty")]).toBe("4.0000");
    expect(cells[heads.indexOf("Outward Value (at cost)")]).toBe("800.00");
    expect(cells[heads.indexOf("Opening Value")]).toBe("300.00");
    expect(cells[heads.indexOf("Closing Value")]).toBe("1,200.00");
    // No blended rate on an item total: two godowns of one item carry two
    // average costs, and the average of the averages is not the average.
    expect(cells[heads.indexOf("Rate")]).toBe("—");
  });

  it("carries every one of the PRINTED report's ten columns", async () => {
    // D62: Ctrl+P renders Opening/Inward/Outward/Closing as Qty+Value pairs
    // from the SAME response. An operator holding the paper beside the screen
    // could not check six of those figures against anything.
    renderStockSummary(async () => WIDGET);
    await screen.findByTestId("stock-item:Widget");
    const heads = [
      ...screen
        .getByTestId("stock-summary-table")
        .querySelectorAll("thead th"),
    ].map((th) => th.textContent);
    for (const printed of [
      "Item / Location",
      "Unit",
      "Opening Qty",
      "Opening Value",
      "Inward Qty",
      "Inward Value",
      "Outward Qty",
      "Closing Qty",
      "Closing Value",
    ]) {
      expect(heads, `the printed report has a "${printed}" column`).toContain(
        printed,
      );
    }
    // The printed "Outward Value" is headed with its basis here, which the
    // paper does not say — reported to the printing lane, whose file it is.
    expect(heads).toContain("Outward Value (at cost)");
  });

  it("exports every figure under a heading that says WHICH it is", () => {
    const g = parseCsv(toCsv(stockSummarySheet(WIDGET, "2026-07-31")));
    const item = g.find((r) => r[0] === "Widget")!;
    const pos = g.find((r) => r[0] === "Main Location")!;

    // Fixed-4 wire strings, indexed BY HEADER NAME.
    expect(item[col(g, "Inward Qty")]).toBe("10.0000");
    expect(item[col(g, "Inward Value")]).toBe("7860.0000");
    expect(item[col(g, "Outward Qty")]).toBe("4.0000");
    expect(item[col(g, "Outward Value (at cost)")]).toBe("800.0000");
    expect(item[col(g, "Opening Qty")]).toBe("2.0000");
    expect(item[col(g, "Opening Value")]).toBe("300.0000");
    expect(item[col(g, "Closing Qty")]).toBe("8.0000");
    expect(item[col(g, "Closing Value")]).toBe("1200.0000");
    // An item total has no rate; the file leaves the cell EMPTY rather than
    // exporting an em dash into a numeric column.
    expect(item[col(g, "Rate")]).toBe("");
    // The position DOES carry one, presentation-only at 4 dp, verbatim.
    expect(pos[col(g, "Rate")]).toBe("150.0000");
    // The footer states the one total the server states, under Closing Value.
    const footer = g.find((r) => r[0] === "Stock-in-Hand")!;
    expect(footer[col(g, "Closing Value")]).toBe("1200.0000");
    expect(footer[col(g, "Inward Value")]).toBe("");
  });

  it("⛔ RULE 4: a write-off that moves VALUE and no QUANTITY changes the file", () => {
    // THE ORACLE THIS REPOSITORY WAS BURNED BY. A Stock Journal that destroys
    // value while leaving every count untouched moves `inward_value` /
    // `outward_value` and nothing else. Under the old seven columns the two
    // responses below exported BYTE-IDENTICAL files: 1,000 of stock value
    // written off, and not one character of the export different.
    const before = clone(WIDGET);
    const after = clone(WIDGET);
    after.items[0]!.inward_value = "6860.0000";
    after.items[0]!.positions[0]!.inward_value = "6860.0000";

    // Every QUANTITY is identical — this is what makes the assertion a value
    // assertion and not a restatement of the fixture.
    for (const k of ["opening_qty", "inward_qty", "outward_qty", "closing_qty"] as const) {
      expect(after.items[0]![k]).toBe(before.items[0]![k]);
    }

    const csvBefore = toCsv(stockSummarySheet(before, "2026-07-31"));
    const csvAfter = toCsv(stockSummarySheet(after, "2026-07-31"));
    expect(csvAfter).not.toBe(csvBefore);
    const g = parseCsv(csvAfter);
    expect(g.find((r) => r[0] === "Widget")![col(g, "Inward Value")]).toBe(
      "6860.0000",
    );
  });

  it("still refuses to invent a row, and still answers Alt+E when there is none", async () => {
    // The control for all of the above: widening the columns must not make an
    // empty response render a wide empty table.
    renderStockSummary(async () => ({
      as_on: "",
      items: [],
      rows: [],
      groups: [],
      total_value: "0.0000",
    }));
    await waitFor(() =>
      expect(screen.queryByTestId("stock-summary-loading")).toBeNull(),
    );
    expect(screen.queryByTestId("stock-summary-table")).toBeNull();
    expect(screen.getByTestId("stock-summary-empty")).toBeTruthy();
    expect(fire!("export").message).toMatch(/No stock movements/);
  });
});
