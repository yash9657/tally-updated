import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
} from "@testing-library/react";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api/mock";
import { ApiError } from "../api/http";
import type { CoredClient } from "../api";
import printedLayouts from "../api/goldens/printed-report-layouts.json";
import {
  SLUGS_THAT_PRINT,
  SLUGS_THAT_DO_NOT_PRINT,
  printsALayout,
} from "../screens/printTarget";
import { exportTargetFor, frameCanExportPdf } from "./useExportAction";
import type { NavSnapshot } from "../keystroke/navMachine";

/**
 * B1 — ALT+E CAN WRITE THE SERVER'S PDF.
 *
 * ⛔ WHAT THIS TRACK ACTUALLY WAS. The PDF renderer, ten report layouts, the two
 * print routes and `usePrint.ts` all existed and were driven. What did not exist
 * was any way to SAVE one: Ctrl+P opens a tab, and Alt+E offered "csv" and
 * "excel-xml" and nothing else. `grep -rn pdf src/export/` was empty.
 *
 * ⛔⛔ AND THE ONE THING THAT MAKES THIS MORE THAN PLUMBING: A PDF IS NOT THE ROWS
 * ON SCREEN. Alt+E's whole identity is "write what is displayed, after Ctrl+F and
 * F12 have been applied"; the print route returns the WHOLE statement. They are
 * two different documents under one key, so every place that offers PDF says so
 * and the status line names the narrowing that was not honoured. An operator who
 * filters to one party and exports must not send a customer the whole ledger
 * believing it was filtered.
 */

const COMPANY = "company-demo";

interface CapturedDownload {
  filename: string;
  type: string;
  size: number;
}

let downloads: CapturedDownload[];
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
      downloads.push({ filename: this.download, type: blob.type, size: blob.size });
    }
  };
  restore.push(() => {
    proto.click = prevClick;
  });
});

afterEach(() => {
  cleanup();
  for (const undo of restore.reverse()) undo();
  vi.restoreAllMocks();
});

// ===========================================================================
// THE GOLDEN — the counterweight to a Go test that skips without Postgres
// ===========================================================================

/**
 * ⛔⛔ THIS BLOCK EXISTS BECAUSE ITS GENERATOR CAN SILENTLY STOP RUNNING.
 *
 * `printed-report-layouts.json` is emitted by `internal/server`'s
 * `TestPrintedReportLayoutsGolden`, which needs Postgres and calls `t.Skip`
 * without it — and a skip is indistinguishable from a pass in `go test`'s exit
 * code. `internal/reporting`'s golden dodged that by building its fixture
 * synthetically; this one cannot, because deciding whether a report prints means
 * running the handler.
 *
 * So the invariants are asserted HERE, in a suite that always runs. None of this
 * re-states the list — that would just be the drift the golden exists to stop —
 * it states the SHAPE the list must have for anything downstream to mean
 * anything.
 */
describe("the printed-layouts golden is intact", () => {
  it("partitions every report name, with none in both lists and none in neither", () => {
    const prints = new Set(printedLayouts.prints_a_layout);
    const deferred = new Set(printedLayouts.answers_501);
    expect(prints.size + deferred.size).toBe(printedLayouts.total_report_names);
    for (const name of prints) {
      expect(deferred.has(name), `${name} is in BOTH lists`).toBe(false);
    }
  });

  it("is not empty, and not everything — either would silently switch the gate off", () => {
    // ⛔ AN EMPTY `prints_a_layout` LOOKS EXACTLY LIKE THE FEATURE BEING TURNED
    // OFF: no F12 row anywhere, no failures, nothing to notice. A full one is
    // worse — PDF offered on twenty reports that answer 501 after the operator
    // chooses.
    expect(SLUGS_THAT_PRINT.length).toBeGreaterThan(0);
    expect(SLUGS_THAT_DO_NOT_PRINT.length).toBeGreaterThan(0);
  });

  it("names the statements a business prints, and not the ones with no layout", () => {
    // A handful of anchors, chosen because each would be a REAL regression: the
    // three primary statements, and the payables ageing that D94 added.
    for (const slug of ["trial-balance", "balance-sheet", "profit-and-loss", "bills-payable"]) {
      expect(printsALayout(slug), `${slug} should print`).toBe(true);
    }
    // And the deferred ones whose printed form is a DIFFERENT document from the
    // screen the slug names — the reason they are deferred rather than missing.
    for (const slug of ["gstr1", "sales-order-book", "bank-statement", "tds-register"]) {
      expect(printsALayout(slug), `${slug} should NOT print`).toBe(false);
    }
  });
});

// ===========================================================================
// THE GATE — a format that fails after being chosen is a menu item that lies
// ===========================================================================

function reportFrame(name: string, config: NavSnapshot["config"] = {}): NavSnapshot {
  return { screen: "report", reportName: name, config } as NavSnapshot;
}

describe("PDF is offered only where the server has a layout", () => {
  it("is available on a report that prints", () => {
    expect(frameCanExportPdf(reportFrame("Trial Balance"))).toBe(true);
  });

  it("⛔ is NOT available on a report that answers 501", () => {
    // The two order books are mapped, governable and deliberately layout-less.
    expect(frameCanExportPdf(reportFrame("Sales Order Book"))).toBe(false);
    expect(frameCanExportPdf(reportFrame("TDS / TCS Register"))).toBe(false);
  });

  it("⛔ the flag alone does NOT arm PDF where there is no layout", () => {
    // TWO GATES, and this is the second. Frame config travels on the frame, so
    // the tempting future change — remembering an export preference across
    // screens — would otherwise silently arm a format that 501s.
    const armed = reportFrame("Sales Order Book", { exportPdf: true });
    expect(exportTargetFor(armed)).toBe("csv");
    // ...while the same flag on a printing report does arm it.
    expect(exportTargetFor(reportFrame("Trial Balance", { exportPdf: true }))).toBe("pdf");
  });

  it("PDF wins over the Excel XML flag, and both unset is still CSV", () => {
    // Three states out of two booleans, decided in ONE place. Both set resolves
    // to PDF, because a PDF is a different document rather than a different
    // encoding of the same rows.
    const both = reportFrame("Trial Balance", { exportPdf: true, exportExcelXml: true });
    expect(exportTargetFor(both)).toBe("pdf");
    expect(exportTargetFor(reportFrame("Trial Balance", { exportExcelXml: true }))).toBe(
      "excel-xml",
    );
    expect(exportTargetFor(reportFrame("Trial Balance"))).toBe("csv");
  });
});

// ===========================================================================
// ALT+E, PRESSED ON THE REAL SHELL
// ===========================================================================

function renderShell(client: CoredClient) {
  render(
    <CompanyProvider client={client} initialCompany={{ id: COMPANY, name: "Demo Traders" }}>
      <AppShell />
    </CompanyProvider>,
  );
  return screen.getByRole("application");
}

function goTo(app: HTMLElement, reportName: string) {
  fireEvent.keyDown(app, { key: "g", altKey: true });
  fireEvent.mouseDown(screen.getByText(reportName));
  fireEvent.keyDown(app, { key: "Enter" });
}

function statusLine(): string {
  return screen.getByTestId("status-line").textContent ?? "";
}

/**
 * A client whose print route answers with a real Blob and records the call.
 *
 * ⛔ THE MOCK IS NOT TAUGHT TO RENDER A PDF. It returns bytes that begin `%PDF-`
 * so the shape is right and nothing downstream has to pretend; the LAYOUT is
 * `internal/printing`'s and is asserted in Go against the real renderer. What is
 * under test here is that Alt+E asks the print route at all.
 */
async function printingClient(err?: Error) {
  const calls: { slug: string; query: unknown }[] = [];
  const base = createMockClient();
  // ⛔ ONE POSTED VOUCHER, AND ONLY THE SHEET FORMATS NEED IT. An empty Trial
  // Balance exports as PDF perfectly well — the document is the server's — while
  // CSV correctly answers "nothing to export". The first draft of this file
  // seeded nothing and the CSV control failed for exactly that reason, which is
  // the difference between the two paths stated as a fact rather than a comment.
  const { ledgers } = await base.listLedgers(COMPANY);
  const cash = ledgers.find((l) => l.name === "Cash")!;
  const sales = ledgers.find((l) => l.name === "Sales")!;
  await base.postSalesVoucher(COMPANY, {
    voucher_date: "2026-06-01",
    party: "Acme, Inc.",
    postings: [
      { ledger_id: cash.id, dr_cr: "DR", amount: "1234.5678" },
      { ledger_id: sales.id, dr_cr: "CR", amount: "1234.5678" },
    ],
  });
  const client: CoredClient = {
    ...base,
    async printReport(_companyID: string, slug: string, query?: Record<string, string>) {
      calls.push({ slug, query });
      if (err) throw err;
      return {
        blob: new Blob([new TextEncoder().encode("%PDF-1.4 fake")], {
          type: "application/pdf",
        }),
        filename: `${slug}.pdf`,
      };
    },
  };
  return { client, calls };
}

/** Turn the F12 PDF toggle on through the panel, the way an operator would. */
async function armPdfViaF12(app: HTMLElement) {
  fireEvent.keyDown(app, { key: "F12" });
  const row = await screen.findByTestId("cfg-export-pdf");
  fireEvent.click(row);
  fireEvent.keyDown(app, { key: "Escape" });
}

describe("Alt+E writes the server's PDF", () => {
  it("asks the PRINT route and never the sheet writer", async () => {
    const { client, calls } = await printingClient();
    const app = renderShell(client);
    goTo(app, "Trial Balance");
    await screen.findByRole("grid", { name: "Trial Balance rows" });
    await armPdfViaF12(app);

    // ⛔ THE CONTROL: nothing has been written, and the print route is untouched.
    expect(downloads).toHaveLength(0);
    expect(calls).toHaveLength(0);

    fireEvent.keyDown(app, { key: "e", altKey: true });

    await waitFor(() => expect(downloads).toHaveLength(1));
    // The route was asked — this is the line that fails if the pdf branch ever
    // falls through to sheet.ts.
    expect(calls).toHaveLength(1);
    expect(calls[0]!.slug).toBe("trial-balance");
    // ...and what was saved is the SERVER'S bytes, not a rendered sheet.
    expect(downloads[0]!.type).toBe("application/pdf");
    expect(downloads[0]!.filename).toBe("trial-balance.pdf");
  });

  it("⛔ SAYS the file is the whole statement, not the rows on screen", async () => {
    const { client } = await printingClient();
    const app = renderShell(client);
    goTo(app, "Trial Balance");
    await armPdfViaF12(app);
    fireEvent.keyDown(app, { key: "e", altKey: true });

    await waitFor(() => expect(downloads).toHaveLength(1));
    // With nothing narrowed the sentence says so plainly rather than hedging —
    // a warning an operator learns to skip protects nobody.
    expect(statusLine()).toMatch(/whole statement, not the rows on screen/i);
    expect(statusLine()).toMatch(/nothing is narrowed on screen/i);
  });

  it("⛔⛔ NAMES THE ACTIVE Ctrl+F FILTER THAT WAS NOT APPLIED", async () => {
    const { client } = await printingClient();
    const app = renderShell(client);
    goTo(app, "Trial Balance");
    await armPdfViaF12(app);

    // Ctrl+F, then a filter the operator can see working on screen.
    fireEvent.keyDown(app, { key: "f", ctrlKey: true });
    const box = await screen.findByTestId("filter-text");
    fireEvent.change(box, { target: { value: "Cash" } });
    fireEvent.keyDown(app, { key: "Enter" });

    fireEvent.keyDown(app, { key: "e", altKey: true });
    await waitFor(() => expect(downloads).toHaveLength(1));

    // ⛔ THE FILTER TEXT ITSELF, IN THE SENTENCE. This is the assertion that
    // stops somebody filtering to one party, exporting, and sending a customer
    // the whole ledger believing it was filtered.
    expect(statusLine()).toMatch(/Ctrl\+F filter "Cash"/);
    expect(statusLine()).toMatch(/NOT applied/);
  });

  it("renders the server's own sentence when the print route refuses", async () => {
    const { client } = await printingClient(
      new ApiError(403, "this security level is not allowed to print report:trial-balance"),
    );
    const app = renderShell(client);
    goTo(app, "Trial Balance");
    await armPdfViaF12(app);
    fireEvent.keyDown(app, { key: "e", altKey: true });

    await waitFor(() =>
      expect(statusLine()).toMatch(/not allowed to print report:trial-balance/),
    );
    // And nothing was written: a refusal must not leave a file behind.
    expect(downloads).toHaveLength(0);
  });

  it("still writes CSV when the PDF flag is off — the default did not move", async () => {
    const { client, calls } = await printingClient();
    const app = renderShell(client);
    goTo(app, "Trial Balance");
    // ⚠️ AWAIT THE ROWS. Alt+E on a report still loading exports nothing and says
    // so, correctly — the other tests here happen to await the F12 panel, which
    // hid this. A control that fails because the screen was not ready proves
    // nothing about the default format.
    await screen.findByRole("grid", { name: "Trial Balance rows" });
    fireEvent.keyDown(app, { key: "e", altKey: true });

    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(downloads[0]!.type).toBe("text/csv;charset=utf-8");
    expect(calls, "the print route must not be touched for a sheet export").toHaveLength(0);
  });
});

// ===========================================================================
// THE F12 PANEL — the row, and the sentence where the row is absent
// ===========================================================================

describe("F12 offers PDF only where it works, and says why where it does not", () => {
  it("offers the row on a report that prints", async () => {
    const { client } = await printingClient();
    const app = renderShell(client);
    goTo(app, "Trial Balance");
    fireEvent.keyDown(app, { key: "F12" });
    expect(await screen.findByTestId("cfg-export-pdf")).toBeTruthy();
    // ⛔ AND THE LABEL CARRIES THE DIFFERENCE. A row reading only "as PDF" would
    // let an operator assume it is the filtered rows in another wrapper.
    expect(screen.getByTestId("cfg-export-pdf").textContent).toMatch(
      /whole statement, not the rows on screen/i,
    );
  });

  it("⛔ withholds the row on a report with no layout, and NAMES the report", async () => {
    const { client } = await printingClient();
    const app = renderShell(client);
    goTo(app, "Sales Order Book");
    fireEvent.keyDown(app, { key: "F12" });
    await screen.findByTestId("cfg-export-note");

    expect(screen.queryByTestId("cfg-export-pdf")).toBeNull();
    // ⛔ AND IT SAYS SO. An absent control is indistinguishable from one that
    // failed to render, which is the argument the keymap makes for listing its
    // `unavailable` combos rather than omitting them.
    const note = screen.getByTestId("cfg-export-pdf-note").textContent ?? "";
    expect(note).toMatch(/no printed layout/i);
    expect(note).toMatch(/501/);
    expect(note, "the sheet formats still work and the note must not imply otherwise").toMatch(
      /CSV and Excel XML/,
    );
  });
});
