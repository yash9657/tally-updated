import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  act,
} from "@testing-library/react";

import { CostCentreSummaryScreen } from "./CostCentreSummaryScreen";
import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
} from "../../app/ScreenActions";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type {
  CoredClient,
  CostCentreNode,
  CostCentreSummaryResponse,
} from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";

/**
 * THE COST CENTRE SUMMARY (T2) — ONE axis, over a period.
 *
 * ⛔ THE INVARIANT EVERY TEST HERE IS ULTIMATELY ABOUT: several axes allocate the
 * SAME money in parallel, so no figure this screen renders may span two of them.
 * The mock fixture is built to expose a violation rather than hide it — the same
 * Rs 1,00,000 is allocated in full across Departments (60,000 / 40,000) AND again
 * in full across Branches (70,000 / 30,000) — so a screen that summed across axes
 * would print 2,00,000 against 1,00,000 of cost and these tests would show the
 * wrong number instead of a plausible one.
 *
 * ⚠️ The suite at the bottom renders the REAL SHELL and presses the real keys. It
 * is the only one that proves F4 is routed (AppShell's SCREEN_ACTIONS map) and that
 * F2 actually opens the period overlay on a ScreenKind report.
 */

afterEach(cleanup);

const COMPANY = "company-demo";

/** A snapshot for this screen, with the period F2 would have written. */
function snap(over: Partial<NavSnapshot> = {}): NavSnapshot {
  return {
    screen: "cost-centre-summary",
    screenStackDepth: 2,
    canReturnToVoucher: false,
    config: { periodFrom: "2026-04-01", periodTo: "2026-06-30" },
    ...over,
  } as unknown as NavSnapshot;
}

function node(over: Partial<CostCentreNode> = {}): CostCentreNode {
  return {
    cost_centre_id: "c-sales",
    name: "Sales",
    parent_id: "",
    depth: 0,
    own_debit: "60000.0000",
    own_credit: "0.0000",
    own_net: "60000.0000",
    total_debit: "60000.0000",
    total_credit: "0.0000",
    total_net: "60000.0000",
    children: [],
    ...over,
  };
}

function summaryView(
  over: Partial<CostCentreSummaryResponse> = {},
): CostCentreSummaryResponse {
  return {
    cost_category_id: "cost-cat-department",
    cost_category: "Department",
    allocate_non_revenue: false,
    from: "2026-04-01",
    to: "2026-06-30",
    centres: [node()],
    unattached: [],
    total_debit: "60000.0000",
    total_credit: "0.0000",
    total_net: "60000.0000",
    unattached_debit: "0.0000",
    unattached_credit: "0.0000",
    allocated_debit: "60000.0000",
    allocated_credit: "0.0000",
    difference_debit: "0.0000",
    difference_credit: "0.0000",
    reconciles: true,
    warnings: ["This total is for the Department axis only."],
    ...over,
  };
}

/** A client serving one hand-built summary for whichever axis is asked for. */
function summaryClient(res: CostCentreSummaryResponse): CoredClient {
  const base = createMockClient({ seed: false });
  return {
    ...base,
    costCentreSummary: vi.fn(async () => res),
  } as unknown as CoredClient;
}

function renderScreen(client: CoredClient, s: NavSnapshot = snap()) {
  let fire: (kind: "selectCostCategory") => Promise<string | void> =
    async () => {};
  function Harness() {
    const dispatch = useScreenActionDispatcher();
    fire = async (kind) => {
      const res = dispatch(kind, s);
      if (res.pending) return await res.pending;
      return res.message;
    };
    return <CostCentreSummaryScreen snapshot={s} onSelect={() => {}} />;
  }
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <ScreenActionsProvider>
        <Harness />
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
  return {
    lastMessage: async (kind: "selectCostCategory") => {
      let out: string | void = undefined;
      await act(async () => {
        out = await fire(kind);
      });
      return out as string | undefined;
    },
  };
}

/**
 * Pick an axis out of the picker the way an operator does.
 *
 * It AWAITS the picker first: the axes are fetched, so a test that clicked
 * synchronously would be clicking before the list exists — and would then pass or
 * fail for a reason that has nothing to do with what it is asserting.
 */
async function pickAxis(name: string) {
  await screen.findByLabelText("SELECT A COST CATEGORY");
  await act(async () => {
    fireEvent.mouseDown(screen.getByText(name));
  });
}

describe("the axis is CHOSEN, never guessed", () => {
  it("offers a picker when the company has more than one axis", async () => {
    renderScreen(createMockClient({ seed: false }));
    // The mock has TWO axes on purpose. Defaulting to the first would render a
    // real, correct Department report under a question that was about Branches.
    await screen.findByLabelText("SELECT A COST CATEGORY");
    const why = screen.getByText(/no .all centres. view/i).textContent ?? "";
    expect(why).toMatch(/SAME money/);
  });

  it("opens the only axis a company has, without asking", async () => {
    const client = {
      ...createMockClient({ seed: false }),
      listCostCategories: vi.fn(async () => ({
        cost_categories: [
          {
            id: "cost-cat-department",
            name: "Department",
            allocate_non_revenue: false,
            centre_count: 2,
            created_by: "seed",
            created_at: "2026-04-01T00:00:00Z",
          },
        ],
      })),
    } as unknown as CoredClient;
    renderScreen(client);
    // Making an operator pick from a list of one is friction, and one axis is the
    // common case at first.
    await screen.findByText(/Cost Centre Summary — Department/);
    expect(screen.queryByLabelText("SELECT A COST CATEGORY")).toBeNull();
  });

  it("names the axis FROM THE RESPONSE, not from what was asked for", async () => {
    // The server echoes cost_category_id and cost_category precisely so a screen
    // cannot render one axis's numbers under another's heading. If this screen
    // titled itself from local state, a stale title over fresh figures would be
    // invisible.
    const client = summaryClient(
      summaryView({ cost_category: "Branch", cost_category_id: "cost-cat-branch" }),
    );
    renderScreen(client);
    await pickAxis("Department");
    await screen.findByText(/Cost Centre Summary — Branch/);
    expect(screen.queryByText(/Cost Centre Summary — Department/)).toBeNull();
  });

  it("F4 reopens the picker, and says so when there is nothing to pick", async () => {
    const h = renderScreen(createMockClient({ seed: false }));
    await screen.findByLabelText("SELECT A COST CATEGORY");
    await pickAxis("Department");
    await screen.findByText(/Cost Centre Summary — Department/);
    // CONTROL: the picker is gone.
    expect(screen.queryByLabelText("SELECT A COST CATEGORY")).toBeNull();
    const msg = await h.lastMessage("selectCostCategory");
    expect(msg).toMatch(/Choose the cost category/);
    expect(screen.getByLabelText("SELECT A COST CATEGORY")).toBeTruthy();
  });

  it("says what to do when the company has no axis at all", async () => {
    const client = {
      ...createMockClient({ seed: false }),
      listCostCategories: vi.fn(async () => ({ cost_categories: [] })),
    } as unknown as CoredClient;
    const h = renderScreen(client);
    const said = (await screen.findByTestId("cost-centre-summary-no-axes"))
      .textContent ?? "";
    expect(said).toMatch(/Gateway > Cost Centres/);
    // The distinction that matters: this is a statement about the MASTERS.
    expect(said).toMatch(/the money is being spent, it is simply not analysed/);
    const msg = await h.lastMessage("selectCostCategory");
    expect(msg).toMatch(/no cost categories/);
  });

  it("refuses to substitute another axis when the drilled one is gone", async () => {
    // Reached by Enter on the Cost Category Summary, for an axis that has since
    // been renamed. Another axis's figures under this heading would be a correct
    // report answering a different question.
    renderScreen(
      createMockClient({ seed: false }),
      snap({ detailOf: "cost-category:Project" }),
    );
    const said = (await screen.findByTestId("cost-centre-summary-error"))
      .textContent ?? "";
    expect(said).toMatch(/not a cost category on this company any more/);
    expect(said).toMatch(/Nothing was substituted/);
  });
});

describe("the period is asked for, and both bounds are meant", () => {
  it("requests nothing and explains why until BOTH dates are set", async () => {
    const client = summaryClient(summaryView());
    renderScreen(client, snap({ config: { periodTo: "2026-06-30" } }));
    await pickAxis("Department");
    const said = (await screen.findByTestId("cost-centre-summary-period-prompt"))
      .textContent ?? "";
    // A cost summary is a FLOW, so an opening bound is required rather than
    // optional — and a defaulted one would answer with everything since the
    // company was formed while looking like the range you asked for.
    expect(said).toMatch(/BOTH dates are needed/);
    expect(said).toMatch(/not a position/);
    expect(client.costCentreSummary).not.toHaveBeenCalled();
  });

  it("asks the server for exactly the axis and the dates it was given", async () => {
    const client = summaryClient(summaryView());
    renderScreen(client);
    await pickAxis("Branch");
    await screen.findByText(/2026-04-01 to 2026-06-30/);
    expect(client.costCentreSummary).toHaveBeenCalledWith(
      COMPANY,
      "cost-cat-branch",
      "2026-04-01",
      "2026-06-30",
    );
  });
});

describe("OWN and TOTAL are both rendered, always", () => {
  /**
   * ⛔ NEITHER IS DERIVABLE FROM THE OTHER: a parent may hold allocations of its
   * OWN and have children too. Only the pair tells an operator whether "Sales
   * 60,000" was keyed against Sales itself or against its sub-teams, and rendering
   * only the total is the mistake api/types.ts names as the common one.
   */
  const withChild = summaryView({
    centres: [
      node({
        name: "Sales",
        own_debit: "10000.0000",
        own_net: "10000.0000",
        total_debit: "60000.0000",
        total_net: "60000.0000",
        children: [
          node({
            cost_centre_id: "c-inside",
            name: "Inside Sales",
            parent_id: "c-sales",
            depth: 1,
            own_debit: "50000.0000",
            own_net: "50000.0000",
            total_debit: "50000.0000",
            total_net: "50000.0000",
          }),
        ],
      }),
    ],
    total_debit: "60000.0000",
    allocated_debit: "60000.0000",
  });

  it("shows a parent's own 10,000 beside its rolled-up 60,000", async () => {
    renderScreen(summaryClient(withChild));
    await pickAxis("Department");
    await screen.findByTestId("cost-centre-row:Sales");
    // The two figures are DIFFERENT and both are on the row. A single "amount"
    // column would have to pick one, and either choice is a wrong answer to a
    // question an operator asks constantly.
    expect(screen.getByTestId("cost-centre-own:Sales").textContent).toBe(
      "10,000.00",
    );
    expect(screen.getByTestId("cost-centre-total:Sales").textContent).toBe(
      "60,000.00",
    );
    // And it says so in words, because the distinction is the one that misleads.
    expect(screen.getByTestId("cost-centre-row:Sales").textContent).toMatch(
      /has allocations of its own AND children/,
    );
  });

  it("renders a child UNDER its parent, from the served nesting", async () => {
    renderScreen(summaryClient(withChild));
    await pickAxis("Department");
    await screen.findByTestId("cost-centre-row:Inside Sales");
    // The response is NESTED (not flat + depth) precisely so the client never
    // re-derives which figure contains which. Parent first, child after, indented.
    const rows = [...document.querySelectorAll("tr[data-testid^='cost-centre-row:']")];
    expect(rows.map((r) => r.getAttribute("data-testid"))).toEqual([
      "cost-centre-row:Sales",
      "cost-centre-row:Inside Sales",
    ]);
    const cell = screen
      .getByTestId("cost-centre-row:Inside Sales")
      .querySelector("td");
    expect(cell?.getAttribute("style")).toContain("padding-left");
  });

  it("does not print the own/children note for a leaf", async () => {
    // The control for the note above: it must appear only where it is true.
    renderScreen(summaryClient(summaryView()));
    await pickAxis("Department");
    await screen.findByTestId("cost-centre-row:Sales");
    expect(screen.getByTestId("cost-centre-row:Sales").textContent).not.toMatch(
      /has allocations of its own AND children/,
    );
  });
});

describe("nothing is hidden: unattached, warnings, and the server's own check", () => {
  it("renders `unattached` LOUDLY when it is non-empty", async () => {
    // ⛔ A FIELD, NOT AN OMISSION. A centre that silently vanished would take its
    // money with it AND the totals would still look internally consistent.
    renderScreen(
      summaryClient(
        summaryView({
          unattached: [
            node({
              cost_centre_id: "c-loop",
              name: "Orphaned Branch",
              own_debit: "5000.0000",
            }),
          ],
          unattached_debit: "5000.0000",
        }),
      ),
    );
    await pickAxis("Department");
    const panel = await screen.findByTestId("cost-centre-summary-unattached");
    expect(panel.textContent).toMatch(/COULD NOT BE PLACED IN THE ROLL-UP/);
    expect(panel.textContent).toMatch(/would still look internally consistent/);
    expect(
      screen.getByTestId("cost-centre-unattached:Orphaned Branch").textContent,
    ).toContain("5,000.00");
    expect(
      screen.getByTestId("cost-centre-summary-unattached-debit").textContent,
    ).toBe("5,000.00");
  });

  it("does not render the unattached panel when there is nothing in it", async () => {
    // The control: an alarm that is always on is not an alarm.
    renderScreen(summaryClient(summaryView()));
    await pickAxis("Department");
    await screen.findByTestId("cost-centre-row:Sales");
    expect(screen.queryByTestId("cost-centre-summary-unattached")).toBeNull();
  });

  it("renders every served warning, verbatim", async () => {
    // `warnings` travels WITH the numbers. A screen may style them; it may not
    // decide whether to show them.
    renderScreen(
      summaryClient(
        summaryView({
          warnings: [
            "This total is for the Department axis only.",
            "A second sentence the server chose to send.",
          ],
        }),
      ),
    );
    await pickAxis("Department");
    const list = await screen.findByTestId("cost-centre-summary-warnings");
    expect(list.textContent).toContain("This total is for the Department axis only.");
    expect(list.textContent).toContain(
      "A second sentence the server chose to send.",
    );
  });

  it("shows the SERVER's reconciliation verdict and shouts when it fails", async () => {
    renderScreen(
      summaryClient(
        summaryView({
          allocated_debit: "59000.0000",
          difference_debit: "1000.0000",
          reconciles: false,
        }),
      ),
    );
    await pickAxis("Department");
    // `reconciles` is the server's verdict on its own arithmetic — this screen does
    // no arithmetic at all, so it could not form an opinion if it wanted one.
    expect(
      (await screen.findByTestId("cost-centre-summary-reconciles")).textContent,
    ).toBe("DOES NOT RECONCILE");
    expect(
      screen.getByTestId("cost-centre-summary-difference").textContent,
    ).toBe("1,000.00");
    expect(screen.getByText(/Treat every figure above as unsafe/)).toBeTruthy();
  });

  it("labels the total as THIS axis's, and says why it may not be added", async () => {
    renderScreen(summaryClient(summaryView()));
    await pickAxis("Department");
    await screen.findByTestId("cost-centre-summary-total-debit");
    expect(
      screen.getByText(/Total for the Department axis ONLY/),
    ).toBeTruthy();
    expect(
      screen.getByText(/twice the cost actually borne/),
    ).toBeTruthy();
  });

  it("states an axis with no allocations instead of implying no cost", async () => {
    renderScreen(
      summaryClient(
        summaryView({
          centres: [],
          total_debit: "0.0000",
          total_net: "0.0000",
          allocated_debit: "0.0000",
        }),
      ),
    );
    await pickAxis("Department");
    const said = (await screen.findByTestId("cost-centre-summary-empty"))
      .textContent ?? "";
    expect(said).toMatch(/statement about ALLOCATION, not about cost/);
    // And it names the report that measures the gap.
    expect(said).toMatch(/Cost Category Summary/);
  });
});

describe("a 403 reads as a decision, and the two 403s are different decisions", () => {
  it("renders the report's own 403 as the decision it is", async () => {
    const client = {
      ...createMockClient({ seed: false }),
      costCentreSummary: vi.fn().mockRejectedValue(new ApiError(403, "forbidden")),
    } as unknown as CoredClient;
    renderScreen(client);
    await pickAxis("Department");
    const panel = await screen.findByTestId("cost-centre-summary-denied");
    expect(panel.textContent).toMatch(
      /not allowed to see departmental profitability/,
    );
    expect(panel.textContent).toMatch(
      /allow display report:cost-centre-summary/,
    );
    // Not an error and not an empty table.
    expect(screen.queryByTestId("cost-centre-summary-error")).toBeNull();
    expect(screen.queryByTestId("cost-centre-summary-empty")).toBeNull();
  });

  it("distinguishes a 403 on the AXIS LIST from a 403 on the report", async () => {
    // Different tokens, different decisions: the axes are cost MASTERS (the bare
    // `cost-centre` token) and the report is `report:cost-centre-summary`. A level
    // can hold one and not the other, and telling an operator to ask for the wrong
    // rule wastes an owner's time.
    const client = {
      ...createMockClient({ seed: false }),
      listCostCategories: vi
        .fn()
        .mockRejectedValue(new ApiError(403, "forbidden")),
    } as unknown as CoredClient;
    renderScreen(client);
    const said = (await screen.findByTestId("cost-centre-summary-error"))
      .textContent ?? "";
    expect(said).toMatch(/COST MASTERS decision/);
    expect(said).toMatch(/allow cost-centre/);
    expect(said).not.toMatch(/report:cost-centre-summary/);
  });
});

// ---------------------------------------------------------------------------
// THROUGH THE REAL SHELL — the only tests here that prove the KEYS work.
//
// F4 goes through AppShell's SCREEN_ACTIONS map (the registration point Alt+O
// shipped without) and F2 goes through the navMachine's `period` case plus the
// FrameOverlays classification — the pair that told the TDS Register and the Stock
// Summary their dates did nothing while both were reading them.
// ---------------------------------------------------------------------------
describe("the screen's keys are actually ROUTED by the shell", () => {
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

  /** Gateway -> Cost Centre Summary, with the Department axis chosen. */
  async function openOnDepartment() {
    const app = renderShell();
    fireEvent.mouseDown(screen.getByText("Cost Centre Summary"));
    fireEvent.keyDown(app, { key: "Enter" });
    // The picker, because the seeded company has two axes.
    await screen.findByLabelText("SELECT A COST CATEGORY");
    await pickAxis("Department");
    await screen.findByText(/Cost Centre Summary — Department/);
    return app;
  }

  it("F4 reopens the axis picker on the real shell", async () => {
    const app = await openOnDepartment();
    // THE CONTROL: the picker is NOT up.
    expect(screen.queryByLabelText("SELECT A COST CATEGORY")).toBeNull();
    expect(
      screen.getByRole("button", { name: /F4 Cost Category/ }),
    ).toBeTruthy();

    fireEvent.keyDown(app, { key: "F4" });

    // If SCREEN_ACTIONS is missing its `selectCostCategory` entry, THIS is the line
    // that fails. Everything else would still be green: F4 resolves on this screen,
    // the action has a label, the button is on the bar, and the handler is
    // registered.
    expect(screen.getByLabelText("SELECT A COST CATEGORY")).toBeTruthy();
  });

  it("F4 does NOT open a Contra voucher (the global binding)", async () => {
    const app = await openOnDepartment();
    fireEvent.keyDown(app, { key: "F4" });
    expect(screen.queryByText(/Contra Voucher/)).toBeNull();
  });

  it("the F4 BUTTON does the same thing as the key", async () => {
    await openOnDepartment();
    expect(screen.queryByLabelText("SELECT A COST CATEGORY")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /F4 Cost Category/ }));
    expect(screen.getByLabelText("SELECT A COST CATEGORY")).toBeTruthy();
  });

  it("F2 opens the period overlay AND applies the dates to the report", async () => {
    const app = await openOnDepartment();
    // CONTROL: no overlay, and the screen is asking for the period.
    expect(screen.queryByRole("dialog", { name: "Period" })).toBeNull();
    expect(
      screen.getByTestId("cost-centre-summary-period-prompt"),
    ).toBeTruthy();

    fireEvent.keyDown(app, { key: "F2" });
    expect(screen.getByRole("dialog", { name: "Period" })).toBeTruthy();

    // ⛔ AND THE OVERLAY MUST NOT SAY THE PERIOD DOES NOTHING. That default branch
    // shipped twice — to the TDS Register and the Stock Summary — over screens that
    // were reading the dates. This screen is a ScreenKind, so it needs the
    // screen->label mapping in FrameOverlays, and this is the assertion that has it.
    const dialog = screen.getByRole("dialog", { name: "Period" });
    expect(dialog.textContent).not.toMatch(/has no date dimension/);
    // Nor is it an as-at position: both bounds apply to a flow.
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

    // THE FIGURES ARRIVE — and they are the DEPARTMENT axis's 60,000 / 40,000, not
    // a cross-axis 1,00,000 + 1,00,000. The mock's fixture is built so that a
    // screen summing across axes shows 2,00,000 here.
    await screen.findByTestId("cost-centre-row:Sales");
    expect(screen.getByTestId("cost-centre-total:Sales").textContent).toBe(
      "60,000.00",
    );
    expect(screen.getByTestId("cost-centre-total:Admin").textContent).toBe(
      "40,000.00",
    );
    expect(
      screen.getByTestId("cost-centre-summary-total-debit").textContent,
    ).toBe("1,00,000.00");
    // The other axis's centres are NOT on this report at all.
    expect(screen.queryByTestId("cost-centre-row:Mumbai")).toBeNull();
  });

  it("the bar is the cost summary's, not the Gateway's", async () => {
    // Without a case in buttonBarFor this screen falls through to GATEWAY_BAR and
    // no other test catches it: the bar would advertise F11 Features and say
    // nothing about the F4 the report is useless without.
    await openOnDepartment();
    expect(screen.getByRole("button", { name: /F2 Period/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /F4 Cost Category/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /F11 Features/ })).toBeNull();
    // And it does NOT advertise a key that can only explain itself.
    expect(screen.queryByRole("button", { name: /Ctrl\+P Print/ })).toBeNull();
  });

  it("Ctrl+P answers with the reason, and fetches nothing", async () => {
    const app = await openOnDepartment();
    fireEvent.keyDown(app, { key: "p", ctrlKey: true });
    // The status line carries the refusal — a bound key that explains itself,
    // rather than the browser's own print dialog rendering a screenshot of a dark
    // terminal emulation.
    const status = await screen.findByTestId("status-line");
    expect(status.textContent).toMatch(/ONE cost category/);
  });
});
