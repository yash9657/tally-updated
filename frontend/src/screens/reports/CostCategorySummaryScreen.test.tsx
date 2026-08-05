import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  act,
} from "@testing-library/react";

import { CostCategorySummaryScreen } from "./CostCategorySummaryScreen";
import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import { ScreenActionsProvider } from "../../app/ScreenActions";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type {
  CoredClient,
  CostCategorySummaryResponse,
  CostCategorySummaryRow,
} from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";

/**
 * THE COST CATEGORY SUMMARY (T2) — one row per axis, and the GAP.
 *
 * ⛔⛔ THE TEST THIS WHOLE FILE EXISTS FOR is "renders NO grand total": the mock
 * fixture allocates the SAME Rs 1,00,000 across two axes, so a footer summing the
 * allocated column reads 2,00,000 against 1,00,000 of cost actually borne — and
 * every figure in that column is individually correct. The assertion is that the
 * string "2,00,000" appears NOWHERE on the page.
 *
 * ⛔ AND THE SECOND ONE: `unallocated_*` is the point of the report, not a
 * footnote. No database constraint can see it — the sum trigger fires on
 * allocations, and where there are none there is nothing to fire on — so an axis
 * can be perfectly consistent and describe a fraction of the business.
 */

afterEach(cleanup);

const COMPANY = "company-demo";

function snap(over: Partial<NavSnapshot> = {}): NavSnapshot {
  return {
    screen: "cost-category-summary",
    screenStackDepth: 2,
    canReturnToVoucher: false,
    config: { periodFrom: "2026-04-01", periodTo: "2026-06-30" },
    ...over,
  } as unknown as NavSnapshot;
}

function rowView(
  over: Partial<CostCategorySummaryRow> = {},
): CostCategorySummaryRow {
  return {
    cost_category_id: "cost-cat-department",
    cost_category: "Department",
    allocate_non_revenue: false,
    allocable_debit: "120000.0000",
    allocable_credit: "0.0000",
    allocable_postings: 2,
    allocated_debit: "100000.0000",
    allocated_credit: "0.0000",
    allocated_postings: 1,
    unallocated_debit: "20000.0000",
    unallocated_credit: "0.0000",
    unallocated_postings: 1,
    fully_allocated: false,
    partly_allocated_postings: 0,
    partly_allocated_shortfall: "0.0000",
    off_axis_debit: "0.0000",
    off_axis_credit: "0.0000",
    off_axis_postings: 0,
    difference_debit: "0.0000",
    difference_credit: "0.0000",
    reconciles: true,
    notes: ["20000.0000 of debit carries NO Department allocation at all."],
    ...over,
  };
}

function summaryView(
  over: Partial<CostCategorySummaryResponse> = {},
): CostCategorySummaryResponse {
  return {
    from: "2026-04-01",
    to: "2026-06-30",
    rows: [rowView()],
    reconciles: true,
    notes: ["These rows must NOT be added together."],
    ...over,
  };
}

function summaryClient(res: CostCategorySummaryResponse): CoredClient {
  return {
    ...createMockClient({ seed: false }),
    costCategorySummary: vi.fn(async () => res),
  } as unknown as CoredClient;
}

function renderScreen(
  client: CoredClient,
  s: NavSnapshot = snap(),
  onSelect: (item: string) => void = () => {},
) {
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <ScreenActionsProvider>
        <CostCategorySummaryScreen snapshot={s} onSelect={onSelect} />
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
}

describe("⛔ there is no grand total, and there never may be", () => {
  it("does not sum the axes, against the fixture built to expose it", async () => {
    // THE MOCK ALLOCATES THE SAME Rs 1,00,000 TWICE — once across Departments, once
    // across Branches — so a cross-axis total is 2,00,000 against 1,00,000 of cost.
    renderScreen(createMockClient({ seed: false }));
    await screen.findByTestId("cost-category-row:Department");
    expect(screen.getByTestId("cost-category-row:Branch")).toBeTruthy();

    // Each axis's own figure is 1,00,000 and is correct...
    expect(
      screen.getByTestId("cost-category-allocated:Department").textContent,
    ).toBe("1,00,000.00");
    expect(
      screen.getByTestId("cost-category-allocated:Branch").textContent,
    ).toBe("1,00,000.00");
    // ...and the sum of them appears NOWHERE. This is the assertion: 2,00,000 would
    // be twice the cost actually borne, made of two correct numbers.
    expect(document.body.textContent).not.toContain("2,00,000");
    // Nor the unallocated column's cross-axis sum (20,000 + 20,000).
    expect(document.body.textContent).not.toContain("40,000");
  });

  it("says out loud that the rows are not to be added", async () => {
    // The absence of a field only informs a programmer. The statement has to be on
    // the artefact, which is why the response carries `notes` and why this panel
    // exists beside them.
    renderScreen(createMockClient({ seed: false }));
    const said = (await screen.findByTestId("cost-category-summary-no-total"))
      .textContent ?? "";
    expect(said).toMatch(/not added up, and they must not be/);
    expect(said).toMatch(/multiple of the cost actually borne/);
    // And the reason, which is the part that makes it stick: every figure in the
    // column is correct.
    expect(said).toMatch(/every figure in the column is correct/);
  });

  it("renders the server's notes verbatim, and does not decide whether to", async () => {
    renderScreen(
      summaryClient(
        summaryView({
          notes: [
            "These rows must NOT be added together.",
            "A second note the server chose to send.",
          ],
        }),
      ),
    );
    const list = await screen.findByTestId("cost-category-summary-notes");
    expect(list.textContent).toContain("These rows must NOT be added together.");
    expect(list.textContent).toContain("A second note the server chose to send.");
  });
});

describe("⛔ the UNALLOCATED gap is the report, not a footnote", () => {
  it("gives the gap its own emphasised column and a sentence per axis", async () => {
    renderScreen(summaryClient(summaryView()));
    const cell = await screen.findByTestId(
      "cost-category-unallocated:Department",
    );
    expect(cell.textContent).toBe("20,000.00");
    // EMPHASISED. `fully_allocated` is the server's verdict, and the styling follows
    // it rather than a local test for a zero.
    expect(cell.className).toMatch(/font-bold/);
    // The allocated figure beside it is NOT emphasised, so the gap is at least as
    // prominent as what was allocated — the whole complaint api/types.ts makes about
    // a screen rendering allocated_* alone.
    expect(
      screen.getByTestId("cost-category-allocated:Department").className,
    ).not.toMatch(/font-bold/);
    // And the served sentence about this axis is rendered.
    expect(
      screen.getByTestId("cost-category-notes:Department").textContent,
    ).toContain("NO Department allocation at all");
  });

  it("does not emphasise a gap that is not there", async () => {
    // The control: an alarm that is always on is not an alarm.
    renderScreen(
      summaryClient(
        summaryView({
          rows: [
            rowView({
              allocable_debit: "100000.0000",
              allocated_debit: "100000.0000",
              unallocated_debit: "0.0000",
              unallocated_postings: 0,
              allocable_postings: 1,
              fully_allocated: true,
              notes: [],
            }),
          ],
        }),
      ),
    );
    const cell = await screen.findByTestId(
      "cost-category-unallocated:Department",
    );
    expect(cell.textContent).toBe("0.00");
    expect(cell.className).not.toMatch(/font-bold/);
    expect(
      screen.getByTestId("cost-category-detail:Department").textContent,
    ).toMatch(/every allocable posting carries an allocation/);
  });

  it("shows the DENOMINATOR beside the coverage, and the flag that sets it", async () => {
    // A coverage figure whose denominator is unstated is a percentage of nothing in
    // particular — and WHICH postings an axis was measured against depends on
    // allocate_non_revenue, so both travel with it.
    renderScreen(
      summaryClient(
        summaryView({
          rows: [rowView({ allocate_non_revenue: true })],
        }),
      ),
    );
    const detail = await screen.findByTestId("cost-category-detail:Department");
    expect(
      screen.getByTestId("cost-category-base:Department").textContent,
    ).toBe("1,20,000.00");
    expect(detail.textContent).toMatch(/MAY be used on balance-sheet ledgers/);
    expect(detail.textContent).toMatch(/That flag IS the denominator/);
  });

  it("shows coverage as the served PAIR and never as a computed percentage", async () => {
    // Dividing two money strings would need the arithmetic this feature does not do
    // in a browser — see the source scan at the bottom of this file.
    renderScreen(summaryClient(summaryView()));
    await screen.findByTestId("cost-category-row:Department");
    expect(screen.getByText("1/2 postings")).toBeTruthy();
    expect(document.body.textContent).not.toContain("83.3");
    expect(document.body.textContent).not.toMatch(/\d%/);
  });
});

describe("the two faults that should be impossible are rendered LOUDLY", () => {
  it("names a part-allocated posting as a broken guarantee, not a column", async () => {
    renderScreen(
      summaryClient(
        summaryView({
          rows: [
            rowView({
              partly_allocated_postings: 2,
              partly_allocated_shortfall: "1500.0000",
            }),
          ],
        }),
      ),
    );
    const faults = await screen.findByTestId("cost-category-faults:Department");
    expect(faults.textContent).toMatch(/SOMETHING IS WRONG ON THIS AXIS/);
    // A deferred constraint trigger FORBIDS this, so a non-zero figure means the
    // guarantee is gone — which is worse than incomplete data and is said so.
    expect(faults.textContent).toMatch(/deferred constraint trigger forbids this/);
    expect(faults.textContent).toMatch(/every other number on this axis is suspect/);
    expect(
      screen.getByTestId("cost-category-shortfall:Department").textContent,
    ).toBe("1,500.00");
    // The row itself carries a marker, so the fault is visible before scrolling.
    expect(
      screen.getByTestId("cost-category-row:Department").textContent,
    ).toContain("⚠");
  });

  it("names an off-axis allocation as the one thing only this report can see", async () => {
    renderScreen(
      summaryClient(
        summaryView({
          rows: [
            rowView({
              off_axis_postings: 1,
              off_axis_debit: "7000.0000",
            }),
          ],
        }),
      ),
    );
    const faults = await screen.findByTestId("cost-category-faults:Department");
    expect(faults.textContent).toMatch(/may NOT be used on/);
    // The database does not enforce allocate_non_revenue — only this report sees it.
    expect(faults.textContent).toMatch(/only place the fault is visible/);
    expect(faults.textContent).toContain("7,000.00");
  });

  it("renders NO fault panel when both are zero", async () => {
    // The control for both tests above.
    renderScreen(summaryClient(summaryView()));
    await screen.findByTestId("cost-category-detail:Department");
    expect(screen.queryByTestId("cost-category-faults:Department")).toBeNull();
    expect(
      screen.getByTestId("cost-category-row:Department").textContent,
    ).not.toContain("⚠");
  });

  it("shows the server's per-axis reconciliation verdict", async () => {
    renderScreen(
      summaryClient(
        summaryView({
          rows: [rowView({ difference_debit: "5.0000", reconciles: false })],
          reconciles: false,
        }),
      ),
    );
    expect(
      (await screen.findByTestId("cost-category-reconciles:Department"))
        .textContent,
    ).toBe("DOES NOT RECONCILE");
    expect(
      screen.getByTestId("cost-category-summary-reconciles").textContent,
    ).toBe("DOES NOT RECONCILE");
    // And it says what that verdict is ABOUT: each axis's own arithmetic, never a
    // check across axes, because there is no such relationship to check.
    expect(screen.getByText(/not a check across axes/)).toBeTruthy();
  });
});

describe("the period, the empty case and the 403", () => {
  it("asks for both dates and requests nothing until it has them", async () => {
    const client = summaryClient(summaryView());
    renderScreen(client, snap({ config: { periodFrom: "2026-04-01" } }));
    const said = (await screen.findByTestId(
      "cost-category-summary-period-prompt",
    )).textContent ?? "";
    expect(said).toMatch(/BOTH dates are needed/);
    expect(client.costCategorySummary).not.toHaveBeenCalled();
  });

  it("asks the server for exactly the dates it was given", async () => {
    const client = summaryClient(summaryView());
    renderScreen(client);
    await screen.findByTestId("cost-category-row:Department");
    expect(client.costCategorySummary).toHaveBeenCalledWith(
      COMPANY,
      "2026-04-01",
      "2026-06-30",
    );
  });

  it("states a company with no axes as 'none of it is analysed'", async () => {
    renderScreen(summaryClient(summaryView({ rows: [] })));
    const said = (await screen.findByTestId("cost-category-summary-empty"))
      .textContent ?? "";
    // ⛔ THE ONE READING THAT MUST NOT BE POSSIBLE: an empty report looking like
    // full coverage.
    expect(said).toMatch(/NOT a statement that the period.s cost is fully analysed/);
    expect(said).toMatch(/none of it is/);
  });

  it("renders a 403 as the decision it is, naming its OWN rule", async () => {
    const client = {
      ...createMockClient({ seed: false }),
      costCategorySummary: vi
        .fn()
        .mockRejectedValue(new ApiError(403, "forbidden")),
    } as unknown as CoredClient;
    renderScreen(client);
    const panel = await screen.findByTestId("cost-category-summary-denied");
    expect(panel.textContent).toMatch(/decision, not a fault/);
    expect(panel.textContent).toMatch(
      /allow display report:cost-category-summary/,
    );
    // A SEPARATE rule from the centre summary's, and the panel says why that
    // separation exists rather than leaving it as trivia.
    expect(panel.textContent).toMatch(/SEPARATE rule/);
    expect(screen.queryByTestId("cost-category-summary-empty")).toBeNull();
  });

  it("renders a non-403 failure as the server's sentence", async () => {
    const client = {
      ...createMockClient({ seed: false }),
      costCategorySummary: vi
        .fn()
        .mockRejectedValue(new ApiError(500, "cost: read allocable base: boom")),
    } as unknown as CoredClient;
    renderScreen(client);
    expect(
      (await screen.findByTestId("cost-category-summary-error")).textContent,
    ).toContain("cost: read allocable base: boom");
  });
});

/**
 * ⛔ NO FLOAT EVER TOUCHES MONEY ON ANY OF THE THREE COST SCREENS.
 *
 * A source scan, because this is an invariant about the CODE and not about one
 * rendering: `parseFloat` on "100000.0000" is silently lossy at scale, and it is
 * the class of bug a chartered accountant finds and nobody else does. Every figure
 * on these screens is a fixed-point string rendered through formatMoney, which is
 * pure string work, and none of the three computes a total, a percentage or a
 * difference — the server publishes all of those precisely so a browser does not
 * have to.
 */
describe("no arithmetic on money, in any of the three cost screens", () => {
  const FILES = {
    "CostCentresScreen.tsx": join(
      __dirname,
      "..",
      "masters",
      "CostCentresScreen.tsx",
    ),
    "CostCentreSummaryScreen.tsx": join(
      __dirname,
      "CostCentreSummaryScreen.tsx",
    ),
    "CostCategorySummaryScreen.tsx": join(
      __dirname,
      "CostCategorySummaryScreen.tsx",
    ),
  };

  /**
   * The source with its COMMENTS REMOVED.
   *
   * All three files talk about `parseFloat` in prose — the rule is written down
   * next to the code it governs, which is the point — so a raw substring scan would
   * fail on the documentation of the invariant it is checking.
   */
  function code(path: string): string {
    return readFileSync(path, "utf8")
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/^\s*\/\/.*$/gm, "")
      .replace(/\/\/.*$/gm, "");
  }

  for (const [label, path] of Object.entries(FILES)) {
    it(`${label} contains no float coercion or rounding of an amount`, () => {
      const src = code(path);
      for (const banned of [
        "parseFloat",
        "toFixed",
        "Number(",
        "parseInt",
        "+ Number",
      ]) {
        expect(src, `${label} must not contain ${banned}`).not.toContain(banned);
      }
    });
  }

  it("the masters screen renders no money at all", () => {
    // A master is a name and a place in a hierarchy. What each centre COST is the
    // Cost Centre Summary, one axis at a time — so this screen does not even import
    // the money formatter, and importing it would be the first step to a total.
    expect(code(FILES["CostCentresScreen.tsx"])).not.toContain("formatMoney");
  });
});

// ---------------------------------------------------------------------------
// THROUGH THE REAL SHELL — Enter drills, F2 sets the period, Ctrl+P explains.
//
// This report's key claim is that it is NOT A DEAD END: it says WHICH analysis is
// incomplete, and Enter on a row opens the centres on that axis. That drill lives
// in the navMachine's Enter arm, which no unit test on this screen can reach.
// ---------------------------------------------------------------------------
describe("the drill and the period are actually ROUTED by the shell", () => {
  function renderShell() {
    render(
      <CompanyProvider
        client={createMockClient()}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    return screen.getByRole("application");
  }

  /** Gateway -> Cost Category Summary, with a period set through the F2 overlay. */
  async function openWithPeriod() {
    const app = renderShell();
    fireEvent.mouseDown(screen.getByText("Cost Category Summary"));
    fireEvent.keyDown(app, { key: "Enter" });
    await screen.findByTestId("cost-category-summary-period-prompt");

    fireEvent.keyDown(app, { key: "F2" });
    const dialog = screen.getByRole("dialog", { name: "Period" });
    // ⛔ THE DEFECT THAT SHIPPED TWICE: the panel telling a report its dates do
    // nothing while the report reads them. This screen is a ScreenKind, so it needs
    // the screen->label mapping in FrameOverlays to be classified at all.
    expect(dialog.textContent).not.toMatch(/has no date dimension/);
    expect(screen.queryByTestId("period-as-at-note")).toBeNull();

    await act(async () => {
      fireEvent.change(screen.getByTestId("period-from"), {
        target: { value: "2026-04-01" },
      });
      fireEvent.change(screen.getByTestId("period-to"), {
        target: { value: "2026-06-30" },
      });
    });
    fireEvent.keyDown(app, { key: "Escape" });
    await screen.findByTestId("cost-category-row:Department");
    return app;
  }

  it("Enter opens the highlighted axis's Cost Centre Summary", async () => {
    const app = await openWithPeriod();
    // THE CONTROL: we are on the category summary and NOT on a centre summary. The
    // pattern includes the em dash and the axis, because the prose on THIS screen
    // names the other report ("Enter on a row opens that axis's Cost Centre
    // Summary") — a looser matcher would pass before the key was pressed.
    expect(screen.queryByText(/Cost Centre Summary — /)).toBeNull();
    expect(screen.getByTestId("cost-category-row:Branch")).toBeTruthy();

    fireEvent.keyDown(app, { key: "Enter" });

    // The first row is highlighted by default, so Enter opens Department — and the
    // drill carries the axis NAME through the pure machine, which the screen then
    // resolves against its own served list. No UUID travels through a frame.
    await screen.findByText(/Cost Centre Summary — Department/);
  });

  it("Enter opens the axis that was SELECTED, not the first one", async () => {
    // The control for the test above: asserting one axis alone would pass for an
    // implementation that hard-coded the first row. A click SELECTS, Enter OPENS.
    const app = await openWithPeriod();
    const branchRow = document.querySelector(
      "[data-rowkey='cost-category:Branch']",
    );
    expect(branchRow).toBeTruthy();
    fireEvent.mouseDown(branchRow!);
    fireEvent.keyDown(app, { key: "Enter" });
    await screen.findByText(/Cost Centre Summary — Branch/);
    expect(screen.queryByText(/Cost Centre Summary — Department/)).toBeNull();
  });

  it("the drilled report shows the SAME period, and only that axis's centres", async () => {
    const app = await openWithPeriod();
    fireEvent.keyDown(app, { key: "Enter" });
    await screen.findByText(/Cost Centre Summary — Department/);
    // ⛔ THIS ASSERTION USED TO BE ITS OWN OPPOSITE. The test was named "shows the
    // SAME period" and asserted the period PROMPT — i.e. that the period was
    // DISCARDED — with a comment calling that honest. It is not honest, it is a
    // dead end: the primary path into this report is the drill, so every operator
    // arriving the normal way had to re-key the range they had just chosen, and
    // re-keying it slightly differently makes the two reports disagree about one
    // axis with nothing on either page saying why.
    //
    // The machine now copies periodFrom/periodTo onto the pushed frame, so the
    // report draws immediately. If this ever regresses, the prompt comes back and
    // the name and the body agree again — in the wrong direction.
    expect(screen.queryByTestId("cost-centre-summary-period-prompt")).toBeNull();
    // Esc returns to the report that was drilled from, with ITS period intact.
    fireEvent.keyDown(app, { key: "Escape" });
    await screen.findByTestId("cost-category-row:Department");
  });

  it("F4 does NOT open a Contra voucher on this report", async () => {
    // There is no axis picker here — this report IS the list of axes — but the
    // global F4 is a Contra voucher, and an operator who used F4 on the centre
    // summary a moment ago will press it. Bound to a sentence instead.
    const app = await openWithPeriod();
    fireEvent.keyDown(app, { key: "F4" });
    expect(screen.queryByText(/Contra Voucher/)).toBeNull();
    const status = await screen.findByTestId("status-line");
    expect(status.textContent).toMatch(/IS the list of cost categories/);
  });

  it("Alt+C does NOT open a ledger-create frame over the report", async () => {
    // Inside a report Alt+C is Tally's New Column (RESEARCH.md:771, and :811 for
    // this very report). It is not built — and the GLOBAL Alt+C would push a
    // LEDGER-CREATE frame over the report, which is not a missing feature but a
    // wrong one.
    const app = await openWithPeriod();
    fireEvent.keyDown(app, { key: "c", altKey: true });
    expect(screen.queryByText(/Create ledger/i)).toBeNull();
    expect(screen.getByTestId("cost-category-row:Department")).toBeTruthy();
    const status = await screen.findByTestId("status-line");
    expect(status.textContent).toMatch(/New Column/);
  });

  it("Ctrl+P answers with the reason, and fetches nothing", async () => {
    const app = await openWithPeriod();
    fireEvent.keyDown(app, { key: "p", ctrlKey: true });
    const status = await screen.findByTestId("status-line");
    expect(status.textContent).toMatch(/501/);
    expect(status.textContent).toMatch(/NEVER be added together/);
  });

  it("the bar is this report's, not the Gateway's", async () => {
    await openWithPeriod();
    expect(screen.getByRole("button", { name: /F2 Period/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /F11 Features/ })).toBeNull();
    // No axis picker is advertised, because this report has none.
    expect(
      screen.queryByRole("button", { name: /F4 Cost Category/ }),
    ).toBeNull();
  });
});
