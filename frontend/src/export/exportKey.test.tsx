import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, act } from "@testing-library/react";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api/mock";
import { ApiError } from "../api/http";
import type { CoredClient, StockSummaryResponse } from "../api";
import { parseCsv } from "./sheet";

/**
 * ⛔⛔ ALT+E, PRESSED ON THE REAL SHELL. THIS IS THE FILE THAT SAYS THE FEATURE
 * IS REACHABLE.
 *
 * Every test here renders `<AppShell/>`, walks to a report the way an operator
 * walks to it, and presses Alt+E. Nothing calls the screen-action dispatcher
 * directly, and nothing calls the export module directly (sheet.test.ts does
 * that, and it is a different question).
 *
 * WHY, in this repository's own words: a feature is not shipped when its tests
 * pass, it is shipped when a human can reach it. `SCREEN_ACTIONS` in
 * AppShell.tsx is the registration point that ships a key COMPLETELY DEAD — no
 * error, no status line — and it is invisible to every other kind of test:
 * unit tests bypass it, and buttons.test.ts only checks that the combo RESOLVES.
 * Delete `exportScreen: "export"` from that map and the first test below fails;
 * nothing else in the repository does.
 *
 * ⚠️ AND THE POINT OF THE SECOND HALF OF THIS FILE: four report screens
 * (Stock Summary, the TDS / TCS Register, and the two order books) render their
 * own tables instead of going through `useReportView`, so Alt+E was dead on all
 * four WHILE THE REPORT BUTTON BAR OVER THEM ADVERTISED "Alt+E Export". A bar
 * button that does nothing is worse than no button, because an operator reaches
 * for it by position from then on.
 *
 * HOW THE DOWNLOAD IS OBSERVED. jsdom has no Blob URLs and no navigation, so
 * `URL.createObjectURL` is stubbed and the anchor's own `click` is replaced.
 * Both stubs CAPTURE rather than fake: the Blob is the real Blob the real code
 * built, and its text is asserted. Nothing about the encoding is mocked.
 */

const COMPANY = "company-demo";

interface CapturedDownload {
  filename: string;
  type: string;
  text: string;
}

let downloads: CapturedDownload[];
let restore: (() => void)[];

beforeEach(() => {
  downloads = [];
  restore = [];

  const blobs = new Map<string, Blob>();
  let n = 0;
  const createObjectURL = (blob: Blob) => {
    const url = `blob:mock/${(n += 1)}`;
    blobs.set(url, blob);
    return url;
  };
  const prevCreate = (URL as unknown as { createObjectURL?: unknown })
    .createObjectURL;
  const prevRevoke = (URL as unknown as { revokeObjectURL?: unknown })
    .revokeObjectURL;
  (URL as unknown as { createObjectURL: unknown }).createObjectURL =
    createObjectURL;
  (URL as unknown as { revokeObjectURL: unknown }).revokeObjectURL = () => {};
  restore.push(() => {
    (URL as unknown as { createObjectURL: unknown }).createObjectURL =
      prevCreate;
    (URL as unknown as { revokeObjectURL: unknown }).revokeObjectURL =
      prevRevoke;
  });

  // Capture the click on the anchor the download helper builds, and read the
  // Blob it points at. Replacing click also stops jsdom complaining about
  // navigation it does not implement.
  const proto = HTMLAnchorElement.prototype;
  const prevClick = proto.click;
  proto.click = function patchedClick(this: HTMLAnchorElement) {
    const href = this.getAttribute("href") ?? "";
    const blob = blobs.get(href);
    if (blob) {
      downloads.push({
        filename: this.download,
        type: blob.type,
        // The Blob is built synchronously from a string, so its bytes are
        // already here; reading them synchronously keeps the assertion in the
        // same tick as the key press.
        text: (blob as Blob & { __text?: string }).__text ?? "",
      });
    }
  };
  restore.push(() => {
    proto.click = prevClick;
  });

  // Blob in jsdom exposes no synchronous reader, and the download path is
  // synchronous by design (a key press must either write a file or say why in
  // the same breath). So the Blob constructor is wrapped to remember the text
  // it was handed — the REAL Blob is still constructed and still handed to the
  // real code; this only keeps a copy of its input.
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
 * A mock client whose seeded company has ONE posted sales voucher, so the Day
 * Book and the Trial Balance are not empty.
 *
 * ⛔ THE PARTY NAME CARRIES A COMMA AND THE AMOUNT CARRIES SUB-PAISA DIGITS ON
 * PURPOSE. "Acme, Inc." is the field that breaks a naive comma-separated writer,
 * and 1234.5678 is the amount that distinguishes an exact export from one that
 * writes what is on screen — the screen shows "1,234.56", because formatMoney
 * groups and truncates.
 */
async function seededClient() {
  const client = createMockClient();
  const { ledgers } = await client.listLedgers(COMPANY);
  const cash = ledgers.find((l) => l.name === "Cash")!;
  const sales = ledgers.find((l) => l.name === "Sales")!;
  await client.postSalesVoucher(COMPANY, {
    voucher_date: "2026-06-01",
    party: "Acme, Inc.",
    postings: [
      { ledger_id: cash.id, dr_cr: "DR", amount: "1234.5678" },
      { ledger_id: sales.id, dr_cr: "CR", amount: "1234.5678" },
    ],
  });
  return client;
}

function renderShell(client = createMockClient()) {
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
  return screen.getByRole("application");
}

/** Alt+G, pick a report by its name in the Go To list, Enter. */
function goTo(app: HTMLElement, reportName: string) {
  fireEvent.keyDown(app, { key: "g", altKey: true });
  fireEvent.mouseDown(screen.getByText(reportName));
  fireEvent.keyDown(app, { key: "Enter" });
}

/**
 * The two cost summaries are ScreenKinds reached from the GATEWAY menu, not
 * report names in the Alt+G list — so {@link goTo} cannot open them.
 */
function openFromGateway(app: HTMLElement, name: string) {
  fireEvent.mouseDown(screen.getByText(name));
  fireEvent.keyDown(app, { key: "Enter" });
}

function pressAltE(app: HTMLElement) {
  fireEvent.keyDown(app, { key: "e", altKey: true });
}

function statusLine(): string {
  return screen.getByTestId("status-line").textContent ?? "";
}

/** The last file written, parsed back into the values it carried. */
function lastCsvGrid(): string[][] {
  const last = downloads[downloads.length - 1]!;
  return parseCsv(last.text);
}

// ---------------------------------------------------------------------------

describe("Alt+E on the real shell", () => {
  it("writes a file from the Day Book, and did not write one before the key", async () => {
    const app = renderShell(await seededClient());
    goTo(app, "Day Book");
    await screen.findByRole("grid", { name: "Day Book entries" });

    // ⛔ THE CONTROL. Without it, a test that asserted only "a download exists"
    // would pass against a screen that downloaded on mount.
    expect(downloads).toHaveLength(0);

    pressAltE(app);

    // THE LINE THAT FAILS IF `exportScreen: "export"` LEAVES SCREEN_ACTIONS.
    expect(downloads).toHaveLength(1);
    expect(downloads[0]!.filename).toBe("Day-Book.csv");
    expect(downloads[0]!.type).toBe("text/csv;charset=utf-8");
    expect(statusLine()).toMatch(/Exported \d+ rows? to Day-Book\.csv\./);
  });

  it("⛔ the file keeps the paisa the SCREEN does not show", async () => {
    const app = renderShell(await seededClient());
    goTo(app, "Day Book");
    await screen.findByRole("grid", { name: "Day Book entries" });

    // What the operator SEES: Indian-grouped, two decimals. formatMoney groups
    // and truncates, so the two low-order digits of the fixed-4 amount are not
    // on the screen at all.
    expect(screen.getAllByText("1,234.56").length).toBeGreaterThan(0);
    expect(screen.queryByText("1234.5678")).toBeNull();

    pressAltE(app);

    // What the FILE carries: all four decimals, no grouping comma, and the
    // party name's own comma survived the comma-separated format.
    const grid = lastCsvGrid();
    const row = grid.find((r) => r[2] === "Acme, Inc.");
    expect(row).toBeTruthy();
    expect(row![0]).toBe("2026-06-01");
    expect(row![3]).toBe("1234.5678");
    expect(row![4]).toBe("1234.5678");

    // ⛔ AND THE CELL IS NOT A NUMBER LITERAL. This is the assertion the whole
    // track exists for: if the amount were written plainly, a spreadsheet would
    // parse it into a float64 and hand back 1234.5678 -> 1234.5678 today and a
    // different value on a large one. The raw bytes must be text-forced.
    const raw = downloads[downloads.length - 1]!.text;
    expect(raw).toContain('"=""1234.5678"""');
    expect(raw).not.toMatch(/,"1234\.5678",/);
  });

  it("the Alt+E BAR BUTTON writes the same file the key does", async () => {
    const app = renderShell(await seededClient());
    goTo(app, "Day Book");
    await screen.findByRole("grid", { name: "Day Book entries" });

    fireEvent.click(screen.getByRole("button", { name: /Alt\+E Export/ }));
    expect(downloads).toHaveLength(1);
    expect(downloads[0]!.filename).toBe("Day-Book.csv");
  });

  it("puts the WIRE amount in the file, not the formatted one on screen", async () => {
    const app = renderShell(await seededClient());
    goTo(app, "Trial Balance");
    await screen.findByRole("grid", { name: "Trial Balance rows" });
    pressAltE(app);

    const grid = lastCsvGrid();
    expect(grid[0]).toEqual(["Ledger", "Debit", "Credit"]);
    const body = grid.slice(1);
    expect(body.length).toBeGreaterThan(0);

    // ⛔ THE ASSERTION THAT DISTINGUISHES THIS BUILD FROM THE PREVIOUS ONE. The
    // export used to write `values` — the DISPLAY strings, which are Indian-
    // grouped and truncated to two decimals by formatMoney. So the old file's
    // amounts carried thousands commas and exactly two decimals; this one's are
    // the four-decimal wire strings with no grouping at all.
    const amounts = body.flatMap((r) => [r[1]!, r[2]!]).filter((v) => v !== "");
    expect(amounts.length).toBeGreaterThan(0);
    for (const a of amounts) {
      expect(a).toMatch(/^-?\d+\.\d{4}$/);
      expect(a).not.toContain(",");
    }

    // And the file's own bytes force text, so a spreadsheet cannot round them.
    const raw = downloads[downloads.length - 1]!.text;
    expect(raw).toContain('"=""');
  });

  it("carries a UTF-8 BOM so Excel does not mangle a non-ASCII party name", async () => {
    const app = renderShell(await seededClient());
    goTo(app, "Day Book");
    await screen.findByRole("grid", { name: "Day Book entries" });
    pressAltE(app);
    expect(downloads[0]!.text.startsWith("﻿")).toBe(true);
  });

  it("writes Excel XML instead when F12 says so — and only then", async () => {
    const app = renderShell(await seededClient());
    goTo(app, "Day Book");
    await screen.findByRole("grid", { name: "Day Book entries" });

    // CONTROL: the default is CSV.
    pressAltE(app);
    expect(downloads[0]!.filename).toBe("Day-Book.csv");

    fireEvent.keyDown(app, { key: "F12" });
    fireEvent.click(screen.getByTestId("cfg-export-excel-xml"));
    fireEvent.keyDown(app, { key: "Escape" });
    pressAltE(app);

    const xml = downloads[downloads.length - 1]!;
    expect(xml.filename).toBe("Day-Book.xml");
    expect(xml.type).toBe("application/vnd.ms-excel;charset=utf-8");
    expect(xml.text).toContain('ss:Type="String"');
    // No BOM on the XML: it declares its own encoding.
    expect(xml.text.startsWith("<?xml")).toBe(true);
  });

  it("writes the Ctrl+F FILTER, because Alt+E writes what is on screen", async () => {
    const app = renderShell(await seededClient());
    goTo(app, "Trial Balance");
    await screen.findByRole("grid", { name: "Trial Balance rows" });

    // CONTROL: unfiltered, the file has every seeded ledger in it.
    pressAltE(app);
    const unfiltered = lastCsvGrid();
    expect(unfiltered.length).toBeGreaterThan(3);
    expect(unfiltered.map((r) => r[0])).toContain("Sales");

    fireEvent.keyDown(app, { key: "f", ctrlKey: true });
    fireEvent.change(screen.getByTestId("filter-text"), {
      target: { value: "Cash" },
    });
    fireEvent.keyDown(app, { key: "Escape" });
    pressAltE(app);

    const filtered = lastCsvGrid();
    // ⛔ THE TOTAL ROW SURVIVES A FILTER — applyReportView keeps non-drillable
    // rows on purpose, and the export writes what survived rather than
    // re-deriving anything. Header, Cash, Total.
    expect(filtered.map((r) => r[0])).toEqual(["Ledger", "Cash", "Total"]);
    // …and the TOTAL is still the WHOLE statement's total, not the filter's sum.
    // The export must never do arithmetic; it writes the server's figure.
    expect(filtered[2]![1]).toBe(unfiltered[unfiltered.length - 1]![1]);
  });

  it("says so rather than writing an empty file when the report has no rows", async () => {
    // The unseeded demo company has no vouchers, so the Day Book is empty.
    const app = renderShell();
    goTo(app, "Day Book");
    await screen.findByText("No vouchers in this period.");

    pressAltE(app);

    expect(downloads).toHaveLength(0);
    expect(statusLine()).toMatch(/Nothing to export — Day Book has no rows on screen/);
  });
});

// ---------------------------------------------------------------------------

describe("the report screens whose Alt+E used to be dead", () => {
  /**
   * Stock Summary, the TDS / TCS Register and the two order books all render
   * their own tables instead of going through `useReportView`, which held the
   * frontend's only export handler — so Alt+E was DEAD on all four, silently,
   * while the report button bar over them advertised "Alt+E Export".
   *
   * ⛔ THE CONTROL IN EVERY TEST BELOW IS THE SHELL'S OWN DEAD-KEY SENTENCE.
   * When no screen has registered `export`, AppShell reports
   *   "Alt+E — Export the rows on screen (…) does not apply on this screen."
   * That string is what these screens used to answer, so asserting merely "the
   * status line is non-empty" would pass against the bug. Each test asserts the
   * screen's OWN sentence AND asserts the dead-key sentence is absent. Delete the
   * `useExportAction` call from any of these screens and its test fails with the
   * dead-key sentence back in the status line.
   */
  const DEAD_KEY = /does not apply on this screen/;

  it("Stock Summary answers for itself instead of falling through to the shell", async () => {
    const app = renderShell();
    goTo(app, "Stock Summary");
    await screen.findByTestId("stock-summary-empty");

    pressAltE(app);

    expect(statusLine()).not.toMatch(DEAD_KEY);
    expect(statusLine()).toMatch(/No stock movements in this company yet/);
  });

  it("the Sales Order Book answers for itself", async () => {
    const app = renderShell();
    goTo(app, "Sales Order Book");
    await screen.findByTestId("sales-order-book-empty");

    pressAltE(app);

    expect(statusLine()).not.toMatch(DEAD_KEY);
    expect(statusLine()).toMatch(/Nothing is outstanding in the Sales Order Book/);
  });

  it("the Purchase Order Book answers for itself, and names ITS OWN direction", async () => {
    // Two registers, two authorities, two report names — and the sentence must
    // not be the sales one, or an operator cannot tell which book refused.
    const app = renderShell();
    goTo(app, "Purchase Order Book");
    await screen.findByTestId("purchase-order-book-empty");

    pressAltE(app);

    expect(statusLine()).not.toMatch(DEAD_KEY);
    expect(statusLine()).toMatch(/Purchase Order Book/);
    expect(statusLine()).not.toMatch(/Sales Order Book/);
  });

  it("the TDS / TCS Register says the period is missing rather than nothing", async () => {
    const app = renderShell();
    goTo(app, "TDS / TCS Register");

    pressAltE(app);

    expect(downloads).toHaveLength(0);
    expect(statusLine()).not.toMatch(DEAD_KEY);
    expect(statusLine()).toMatch(/Set the period with F2 first/);
  });

  /**
   * ⛔ THE TWO COST SUMMARIES ARE HERE BECAUSE THIS TRACK PUT "Alt+E Export" ON
   * THEIR BUTTON BARS. buttonBar.ts's own rule — the one BANK_RECON_BAR states
   * for Alt+O — is that a key joins a bar the day it becomes real, and nothing
   * in this repository can tell a bar button that works from one that does not
   * except a test that PRESSES it on the shell. Both bars now advertise it, so
   * both get pressed here.
   */

  it("the Cost Category Summary answers for itself, in ITS OWN words", async () => {
    const app = renderShell();
    openFromGateway(app, "Cost Category Summary");

    pressAltE(app);

    expect(downloads).toHaveLength(0);
    expect(statusLine()).not.toMatch(DEAD_KEY);
    // ⛔ THE DISTINGUISHING HALF. Both cost reports refuse an export before they
    // are configured, and they refuse for DIFFERENT reasons — this one wants a
    // period, the centre summary wants an axis. Asserting only "it said
    // something" would pass if the two screens' handlers were swapped.
    expect(statusLine()).toMatch(/measures a period's coverage/);
    expect(statusLine()).not.toMatch(/cost category \(the axis\)/);
  });

  it("the Cost Centre Summary wants the AXIS first, not the period", async () => {
    const app = renderShell();
    openFromGateway(app, "Cost Centre Summary");
    // Two axes on the seeded company, so the screen opens on its picker.
    await screen.findByLabelText("SELECT A COST CATEGORY");

    pressAltE(app);

    expect(downloads).toHaveLength(0);
    expect(statusLine()).not.toMatch(DEAD_KEY);
    expect(statusLine()).toMatch(/Choose the cost category \(the axis\) with F4 first/);
  });

  it("⛔ and once the axis and the period are given, it writes a file whose NAME carries the axis", async () => {
    const app = renderShell();
    openFromGateway(app, "Cost Centre Summary");
    await screen.findByLabelText("SELECT A COST CATEGORY");
    await act(async () => {
      fireEvent.mouseDown(screen.getByText("Department"));
    });
    await screen.findByText(/Cost Centre Summary — Department/);

    fireEvent.keyDown(app, { key: "F2" });
    await act(async () => {
      fireEvent.change(screen.getByTestId("period-from"), {
        target: { value: "2026-04-01" },
      });
      fireEvent.change(screen.getByTestId("period-to"), {
        target: { value: "2026-06-30" },
      });
    });
    fireEvent.keyDown(app, { key: "Escape" });
    await screen.findByTestId("cost-centre-row:Sales");

    // CONTROL: still nothing written before the key.
    expect(downloads).toHaveLength(0);
    pressAltE(app);
    expect(downloads).toHaveLength(1);

    // ⛔⛔ THE AXIS IS IN THE FILE NAME. Several axes allocate the SAME money in
    // parallel, so two files both called "Cost-Centre-Summary.csv" are two sets
    // of figures whose subject cannot be recovered — and adding them together
    // double-counts the business. The period is in there for the same reason.
    expect(downloads[0]!.filename).toBe(
      "Cost-Centre-Summary-Department-2026-04-01-to-2026-06-30.csv",
    );

    const g = lastCsvGrid();
    // The DEPARTMENT axis's centres, at wire scale — the screen shows
    // "60,000.00" and the file must not.
    const sales = g.find((r) => r[0] === "Sales")!;
    expect(sales[1]).toBe("60000.0000");
    expect(sales[3]).toBe("60000.0000");
    expect(g.find((r) => r[0] === "Total")![3]).toBe("100000.0000");
    // ⛔ AND THE OTHER AXIS'S CENTRES ARE NOT IN THE FILE. A cross-axis export
    // would read 2,00,000 against 1,00,000 of cost actually borne, out of
    // figures that are each individually correct.
    expect(g.map((r) => r[0])).not.toContain("Mumbai");
    // Nothing on screen carried the grouping comma into the file.
    for (const row of g.slice(1)) {
      for (const cell of row) expect(cell).not.toMatch(/\d,\d/);
    }
  });
});

// ---------------------------------------------------------------------------

/**
 * ⛔⛔ THE REFUSAL PROMISE, FOR THE FIVE REPORTS IT WAS FALSE FOR.
 *
 * The track claims "a level refused a report is told exactly that when it
 * presses the key". That held only for the five SCREEN-OWNED reports (Stock
 * Summary, the TDS register, the two order books, the cost summaries), each of
 * which catches its own fetch error. The five statement screens went through
 * `useReportView`, and NONE of them had a `.catch` at all — so on a 403:
 *
 *   - the Trial Balance and the Outstandings pushed their total row anyway, so
 *     Alt+E WROTE A FILE: "Total, 0.0000, 0.0000". A refused report exported as
 *     a balanced set of books with nothing in them.
 *   - the Day Book, Balance Sheet and P&L answered "Nothing to export — X has no
 *     rows on screen", which states a fact about the COMPANY'S BOOKS when the
 *     truth is a fact about the OPERATOR'S AUTHORITY. An operator reading it
 *     goes looking for the missing vouchers.
 *
 * A 403 on a report is a NORMAL answer in this system — migration 0011 seeds a
 * coarse `allow display report` and later migrations take individual reports
 * back off the Data Entry level — so this is the everyday path, not an edge.
 *
 * EACH TEST BELOW CARRIES THREE ASSERTIONS AND ALL THREE ARE LOAD-BEARING:
 *   1. the refusal is SAID (and names the report, so the operator knows which);
 *   2. NO FILE WAS WRITTEN — the assertion that fails against the old Trial
 *      Balance and Outstandings, where the sentence alone would not;
 *   3. the OLD WRONG SENTENCE is absent — the assertion that fails against the
 *      old Day Book / Balance Sheet / P&L, where (1) and (2) would not.
 */
describe("a report the LEVEL is refused says so when Alt+E is pressed", () => {
  /** The mock client, with one report route refusing exactly as the PDP does. */
  function refusing(method: keyof CoredClient, resource: string): CoredClient {
    const base = createMockClient();
    return {
      ...base,
      [method]: () =>
        Promise.reject(
          new ApiError(403, `forbidden: display ${resource} is not allowed`),
        ),
    } as CoredClient;
  }

  const CASES: {
    report: string;
    method: keyof CoredClient;
    resource: string;
    /** How the refusal names the report — the SCREEN's own heading. */
    label: string;
  }[] = [
    {
      report: "Day Book",
      method: "getDayBook",
      resource: "report:day-book",
      label: "Day Book",
    },
    {
      report: "Trial Balance",
      method: "getTrialBalance",
      resource: "report:trial-balance",
      label: "Trial Balance",
    },
    {
      report: "Balance Sheet",
      method: "getBalanceSheet",
      resource: "report:balance-sheet",
      label: "Balance Sheet",
    },
    {
      report: "Profit & Loss A/c",
      method: "getProfitLoss",
      resource: "report:profit-loss",
      label: "Profit & Loss A/c",
    },
    {
      report: "Outstandings",
      method: "getOutstandings",
      resource: "report:outstandings",
      label: "Outstandings",
    },
  ];

  for (const c of CASES) {
    it(`${c.report}: Alt+E names the security level, writes nothing, and does not blame the books`, async () => {
      const app = renderShell(refusing(c.method, c.resource));
      goTo(app, c.report);

      // The SCREEN refuses too — Alt+E's whole promise is that it agrees with
      // what is displayed, so a status line saying "refused" over a rendered
      // statement would be its own defect.
      await screen.findByTestId("report-denied");

      // CONTROL: nothing has been written before the key, so the length
      // assertion below cannot pass for the wrong reason.
      expect(downloads).toHaveLength(0);

      pressAltE(app);

      // (1) THE PROMISE.
      expect(statusLine()).toMatch(
        new RegExp(
          `Your security level may not open the ${c.label.replace(/[&/]/g, "\\$&")}`,
        ),
      );
      // (2) THE ASSERTION THAT FAILS AGAINST THE OLD TRIAL BALANCE AND
      // OUTSTANDINGS: they exported a file of zeroes off their unconditional
      // total row.
      expect(downloads).toHaveLength(0);
      // (3) THE ASSERTION THAT FAILS AGAINST THE OLD DAY BOOK / BALANCE SHEET /
      // P&L: "no rows on screen" is a claim about the books.
      expect(statusLine()).not.toMatch(/has no rows on screen/);
      expect(statusLine()).not.toMatch(/does not apply on this screen/);
    });
  }

  /**
   * ⛔ THE CONTROL FOR THE WHOLE BLOCK, and without it every test above would
   * pass against a build that answered "you may not open this" to EVERYTHING.
   * Same shell, same key, same report — nothing refused — writes the file.
   */
  it("CONTROL: the same key on the same report, not refused, still writes the file", async () => {
    const app = renderShell(await seededClient());
    goTo(app, "Trial Balance");
    await screen.findByRole("grid", { name: "Trial Balance rows" });

    expect(screen.queryByTestId("report-denied")).toBeNull();
    pressAltE(app);

    expect(downloads).toHaveLength(1);
    expect(downloads[0]!.filename).toBe("Trial-Balance.csv");
    expect(statusLine()).not.toMatch(/security level/);
  });

  /**
   * A NON-403 IS NOT DRESSED UP AS A REFUSAL. A 500 is an outage and a 403 is a
   * decision; telling an operator their level is at fault when the server fell
   * over sends them to their Owner for a rule that does not exist.
   */
  it("a 500 is reported as an outage, quoting the server, not as a security refusal", async () => {
    const base = createMockClient();
    const client = {
      ...base,
      getDayBook: () =>
        Promise.reject(new ApiError(500, "reporting: replay failed")),
    } as CoredClient;

    const app = renderShell(client);
    goTo(app, "Day Book");
    await screen.findByTestId("report-error");

    pressAltE(app);

    expect(downloads).toHaveLength(0);
    expect(statusLine()).toMatch(/Day Book unavailable \(500\)/);
    expect(statusLine()).toMatch(/reporting: replay failed/);
    expect(statusLine()).not.toMatch(/security level/);
  });
});

// ---------------------------------------------------------------------------

/**
 * ⛔ ALT+E ON A DRILL-DOWN. `ReportDetailScreen` is a report screen like any
 * other — it goes through `useReportView`, so it has had a working Alt+E all
 * along — and nothing in the repository had ever PRESSED the key on it. That is
 * the same gap that shipped Alt+O dead: a registration that looks present in
 * five places and is only proven by a key press on the real shell.
 *
 * The drill is also where the export means the most: a statement line exports
 * five figures, and its drill-down exports the vouchers those figures are made
 * of, which is the thing an accountant actually takes into a spreadsheet.
 */
describe("Alt+E on a drill-down", () => {
  it("writes the DRILLED level — its own file name, its own rows, at wire scale", async () => {
    const app = renderShell(await seededClient());
    goTo(app, "Trial Balance");
    await screen.findByRole("grid", { name: "Trial Balance rows" });

    // Highlight the Cash ledger and drill into it. The highlight publishes the
    // row key; the Enter the shell then handles pushes the detail frame.
    fireEvent.mouseDown(screen.getByText("Cash"));
    fireEvent.keyDown(app, { key: "Enter" });
    await screen.findByRole("grid", { name: "Cash detail rows" });

    // CONTROL: the drill wrote nothing on the way in.
    expect(downloads).toHaveLength(0);

    pressAltE(app);

    // ⛔ THE FILE IS THE DRILL'S, NOT THE PARENT'S. A drill-down that exported
    // under "Trial-Balance.csv" would overwrite the statement's own file with
    // one ledger's vouchers — same name, different contents, and nothing inside
    // it saying which it is.
    expect(downloads).toHaveLength(1);
    expect(downloads[0]!.filename).toBe("Trial-Balance-Cash.csv");

    const grid = lastCsvGrid();
    expect(grid[0]).toEqual(["Date", "No.", "Particulars", "Debit", "Credit"]);
    const row = grid.find((r) => r[2] === "Acme, Inc.");
    expect(row).toBeTruthy();
    // The voucher that posted to Cash, at FOUR decimals — the screen shows
    // "1,234.56" and the drill's file must not.
    expect(row![3]).toBe("1234.5678");
    expect(statusLine()).toMatch(/Exported 1 row to Trial-Balance-Cash\.csv\./);
  });

  /**
   * ⛔ AND A REFUSED DRILL-DOWN ANSWERS THE REFUSAL — which the happy-path test
   * above cannot show.
   *
   * ⚠️ THIS TEST EXISTS BECAUSE ITS ABSENCE WAS PROVED, not suspected. Deleting
   * `availability` from `ReportDetailScreen`'s `useReportView` meta — i.e.
   * reintroducing on the drill exactly the lie this track removed from the five
   * statements — left the ENTIRE suite green (188/188). The behaviour was
   * already correct; it was simply undetectable, which is the state every
   * defect in this repository has been in the day before it was found.
   *
   * The coarser mutation the fix was checked against (ReportDetailScreen stops
   * calling `useReportView` at all) IS caught by the happy path — which is
   * precisely how the finer one slipped through. HANDOFF §9.1a item 11: mutate
   * against the case that DISTINGUISHES the implementations, not a
   * representative one.
   */
  it("a REFUSED drill-down exports nothing and says why", async () => {
    const base = createMockClient();
    const client = {
      ...base,
      getDayBook: () =>
        Promise.reject(new ApiError(403, "authz: display report:day-book denied")),
    } as CoredClient;

    const app = renderShell(client);
    goTo(app, "Trial Balance");
    await screen.findByRole("grid", { name: "Trial Balance rows" });
    fireEvent.mouseDown(screen.getByText("Cash"));
    fireEvent.keyDown(app, { key: "Enter" });

    // The screen states the refusal — the CONTROL that the drill really is in
    // the refused state and not merely still loading.
    await screen.findByTestId("report-denied");

    pressAltE(app);

    // ⛔ NO FILE. A drill that wrote a file of whatever it had managed to load
    // would put rows on disk that the operator was refused on screen.
    expect(downloads).toHaveLength(0);
    // ...and the key ANSWERS, rather than doing nothing. A silent no-op reads
    // as "Alt+E is broken" and sends the operator to the wrong person.
    expect(statusLine()).toMatch(/security level/i);
  });
});

// ---------------------------------------------------------------------------

/**
 * ⛔⛔ ALT+E AND THE SCREEN READ THE SAME FIELD ON THE STOCK SUMMARY.
 *
 * The response carries the positions twice — `rows` flat, `items` folded into
 * the item tree the table is drawn from. The screen showed its "no stock" line
 * on `rows.length === 0`; Alt+E refused on `items.length === 0`; the TABLE
 * renders `items`. Three readers, two fields, and only the server's fold kept
 * them agreeing.
 *
 * ⛔ THE INPUTS BELOW ARE THE ONES THAT DISTINGUISH RIGHT FROM WRONG, not
 * representative ones: each fills exactly ONE of the two fields, which is the
 * only shape under which the old pair of predicates disagree. (The first is not
 * hypothetical — `export/reportSheets.test.ts`'s own stock fixture is written
 * with `rows: []` and a populated `items`.) Under the old code the first case
 * showed "no stock movements in this company yet" on screen while Alt+E wrote a
 * file full of stock, and the second showed a table while Alt+E said there was
 * nothing to export. Both are the one thing Alt+E promises never to do.
 */
describe("Alt+E agrees with the Stock Summary screen about emptiness", () => {
  const POSITION = {
    stock_item_id: "i1",
    item: "Widget",
    unit: "Nos",
    godown_id: "g1",
    godown: "Main Store",
    batch_id: "",
    batch: "",
    valuation_method: "AVERAGE",
    opening_qty: "0.0000",
    opening_value: "0.0000",
    inward_qty: "100.0000",
    inward_value: "5000.0000",
    outward_qty: "40.0000",
    outward_value: "2000.5678",
    closing_qty: "60.0000",
    closing_value: "2999.4322",
    closing_rate: "49.9905",
  };

  function stockClient(data: StockSummaryResponse): CoredClient {
    return {
      ...createMockClient(),
      getStockSummary: () => Promise.resolve(data),
    } as CoredClient;
  }

  it("ITEMS but no ROWS: the table is on screen, so the key writes it", async () => {
    const app = renderShell(
      stockClient({
        as_on: "2026-06-30",
        total_value: "2999.4322",
        rows: [],
        items: [
          {
            stock_item_id: "i1",
            item: "Widget",
            unit: "Nos",
            opening_qty: "0.0000",
            opening_value: "0.0000",
            inward_qty: "100.0000",
            inward_value: "5000.0000",
            outward_qty: "40.0000",
            outward_value: "2000.5678",
            closing_qty: "60.0000",
            closing_value: "2999.4322",
            positions: [POSITION],
          },
        ],
      }),
    );
    goTo(app, "Stock Summary");
    await screen.findByTestId("stock-summary-table");

    // ⛔ THE SCREEN'S HALF: with the old `rows.length === 0` predicate this
    // company was declared to have no stock at all — over a rendered table.
    expect(screen.queryByTestId("stock-summary-empty")).toBeNull();
    expect(screen.getByTestId("stock-item:Widget")).toBeTruthy();

    pressAltE(app);

    // THE KEY'S HALF: it writes what is displayed.
    expect(downloads).toHaveLength(1);
    const g = lastCsvGrid();
    expect(g.map((r) => r[0])).toContain("Widget");
    // Indexed by HEADER NAME: the sheet was widened to put a Value beside every
    // Qty (the inward money figure had no column at all), and a hardcoded 6
    // would silently follow the shift onto a different column.
    expect(
      g.find((r) => r[0] === "Widget")![g[0]!.indexOf("Closing Value")],
    ).toBe("2999.4322");
  });

  it("ROWS but no ITEMS: the table is still on screen, so the key still writes it", async () => {
    const app = renderShell(
      stockClient({
        as_on: "2026-06-30",
        total_value: "2999.4322",
        rows: [POSITION],
        items: [],
      }),
    );
    goTo(app, "Stock Summary");
    await screen.findByTestId("stock-summary-table");

    // The old export predicate (`items.length === 0`) refused HERE, while the
    // screen rendered the table it is looking at.
    expect(screen.queryByTestId("stock-summary-empty")).toBeNull();

    pressAltE(app);

    expect(statusLine()).not.toMatch(/nothing to export/i);
    expect(downloads).toHaveLength(1);
    // What is on screen is the Stock-in-Hand line and nothing else, and that is
    // exactly what the file carries — agreement is the assertion, not the count.
    const g = lastCsvGrid();
    expect(g.map((r) => r[0])).toEqual(["Item / Location", "Stock-in-Hand"]);
    expect(g[1]![g[0]!.indexOf("Closing Value")]).toBe("2999.4322");
  });

  it("CONTROL: genuinely empty — BOTH fields empty — still refuses, in the screen's words", async () => {
    const app = renderShell(
      stockClient({ as_on: "", total_value: "0.0000", rows: [], items: [] }),
    );
    goTo(app, "Stock Summary");
    // The empty state, and NOT the table: without this the two tests above
    // would pass against a predicate that simply never reports emptiness.
    await screen.findByTestId("stock-summary-empty");
    expect(screen.queryByTestId("stock-summary-table")).toBeNull();

    pressAltE(app);

    expect(downloads).toHaveLength(0);
    expect(statusLine()).toMatch(/No stock movements in this company yet/);
  });
});

// ---------------------------------------------------------------------------

/**
 * ⛔ THE TWO SURFACES THAT SELL A KEY MUST SELL THE SAME KEY.
 *
 * `keystroke/buttonBar.ts` put "Alt+E Export" on both cost-summary bars when the
 * screens registered real handlers; `AppShell`'s `hintsFor` was not updated, so
 * the bottom strip listed F2/F4 and nothing else. Both are hand-maintained lists
 * with no compiler tying them together, and an operator reads the hint strip as
 * the definitive list of what this screen does — a working export that only one
 * surface mentions is a working export nobody uses.
 *
 * The test presses the key as well as reading both lists, so it cannot pass by
 * advertising something that does not answer.
 */
describe("the cost summaries advertise Alt+E on BOTH surfaces", () => {
  function hints(): string {
    return screen.getByLabelText("Shortcut hints").textContent ?? "";
  }

  it("Cost Category Summary: bar button, hint strip, and a key that answers", async () => {
    const app = renderShell();
    openFromGateway(app, "Cost Category Summary");

    expect(screen.getByRole("button", { name: /Alt\+E Export/ })).toBeTruthy();
    expect(hints()).toContain("Alt+E: Export");
    // CONTROL: the strip is the SCREEN's, not a global list — it still carries
    // this screen's own keys, so the assertion above is not matching some
    // unrelated always-present footer.
    expect(hints()).toContain("Enter: Open this axis");

    pressAltE(app);
    expect(statusLine()).not.toMatch(/does not apply on this screen/);
  });

  it("Cost Centre Summary: bar button, hint strip, and a key that answers", async () => {
    const app = renderShell();
    openFromGateway(app, "Cost Centre Summary");
    await screen.findByLabelText("SELECT A COST CATEGORY");

    expect(screen.getByRole("button", { name: /Alt\+E Export/ })).toBeTruthy();
    expect(hints()).toContain("Alt+E: Export");
    expect(hints()).toContain("F4: Cost Category");

    pressAltE(app);
    expect(statusLine()).not.toMatch(/does not apply on this screen/);
  });

  /**
   * ⛔ AND THE CONTROL FOR THE STRIP ITSELF: a screen with no export must not
   * list one. Without this, "add Alt+E to every hint list" would pass both tests
   * above and put the key on screens where it answers "does not apply".
   */
  it("CONTROL: the Gateway does not advertise Alt+E, because it has no export", () => {
    renderShell();
    expect(hints()).not.toContain("Alt+E");
  });
});
