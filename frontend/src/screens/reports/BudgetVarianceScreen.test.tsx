import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, act } from "@testing-library/react";

import { BudgetVarianceScreen } from "./BudgetVarianceScreen";
import { CompanyProvider } from "../../app/CompanyContext";
import { ScreenActionsProvider } from "../../app/ScreenActions";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type {
  BudgetVarianceResponse,
  BudgetVarianceRow,
  CoredClient,
} from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";

/**
 * BUDGET VS ACTUAL (T11) — the screen, and the four ways it could lie.
 *
 * ⛔ EVERY TEST HERE IS ABOUT A FAILURE THAT WOULD STILL LOOK RIGHT ON SCREEN.
 * None of them is about a crash or a blank page: a variance report's failure mode
 * is a page of plausible numbers, and this repository's register is full of
 * exactly that (D100, D105). So each test asserts the presence of an honest
 * refusal or an honest label, and the failure messages name the CONSEQUENCE.
 *
 * The four:
 *
 *  1. A grand total drawn over MIXED BASES. A closing balance is a POSITION and
 *     nett transactions are a FLOW; their sum is not a quantity. The server sends
 *     `totals_are_addable: false` with three ZERO grand totals, so a screen that
 *     drew the footer unconditionally would print 0.00 and be believed.
 *  2. A percentage rendered where the server sent `null`. There is no budget to
 *     be a percentage of, and any number is one a reader will act on.
 *  3. UNSPENT shown as underspend, or UNBUDGETED shown as 100% over.
 *  4. `unbudgeted` folded into the plan's rows, which would make a plan that was
 *     never set look like a plan that was exceeded.
 */

afterEach(cleanup);

const COMPANY = "company-demo";

function snap(): NavSnapshot {
  return {
    screen: "budget-variance",
    screenStackDepth: 2,
    canReturnToVoucher: false,
    config: {},
  } as unknown as NavSnapshot;
}

function row(over: Partial<BudgetVarianceRow> = {}): BudgetVarianceRow {
  return {
    dimension: "LEDGER",
    target_id: "ledger-travel",
    target_name: "Travel Expenses",
    nature: "EXPENSE",
    basis: "NETT_TRANSACTIONS",
    dr_cr: "D",
    budget: "500000.0000",
    actual_debit: "620000.0000",
    actual_credit: "0.0000",
    actual: "620000.0000",
    variance: "120000.0000",
    variance_percent: "24.00",
    status: "OVER",
    note: "",
    notes: [],
    ...over,
  };
}

function view(over: Partial<BudgetVarianceResponse> = {}): BudgetVarianceResponse {
  return {
    budget_id: "budget-fy26",
    budget_name: "FY 2026-27 Plan",
    dimension: "LEDGER",
    from_date: "2026-04-01",
    to_date: "2027-03-31",
    rows: [row()],
    unbudgeted: [],
    unbudgeted_count: 0,
    unbudgeted_actual: "0.0000",
    totals: [
      {
        basis: "NETT_TRANSACTIONS",
        rows: 1,
        budget: "500000.0000",
        actual: "620000.0000",
        variance: "120000.0000",
      },
    ],
    totals_are_addable: true,
    total_budget: "500000.0000",
    total_actual: "620000.0000",
    total_variance: "120000.0000",
    status_counts: { OVER: 1 },
    cost_category_id: "",
    cost_category_name: "",
    notes: [],
    ...over,
  };
}

/** A client serving one hand-built report for whichever plan is asked for. */
function varianceClient(
  res: BudgetVarianceResponse | Error,
  plans = [
    {
      id: "budget-fy26",
      name: "FY 2026-27 Plan",
      from_date: "2026-04-01",
      to_date: "2027-03-31",
      line_count: 1,
      created_by: "seed",
      created_at: "2026-04-01T00:00:00Z",
    },
  ],
): CoredClient {
  const base = createMockClient({ seed: false });
  return {
    ...base,
    listBudgets: vi.fn(async () => ({ budgets: plans })),
    budgetVariance: vi.fn(async () => {
      if (res instanceof Error) throw res;
      return res;
    }),
  } as unknown as CoredClient;
}

async function renderScreen(client: CoredClient) {
  await act(async () => {
    render(
      <CompanyProvider
        client={client}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <ScreenActionsProvider>
          <BudgetVarianceScreen snapshot={snap()} onSelect={() => {}} />
        </ScreenActionsProvider>
      </CompanyProvider>,
    );
  });
}

describe("Budget Variance — the totals", () => {
  it("draws a grand total when every line shares one basis", async () => {
    await renderScreen(varianceClient(view()));
    expect(screen.queryByTestId("budget-grand-total")).not.toBeNull();
    expect(screen.queryByTestId("budget-no-grand-total")).toBeNull();
  });

  it("⛔ REFUSES a grand total when the plan mixes a POSITION with a FLOW", async () => {
    const closing = row({
      target_id: "group-debtors",
      target_name: "Sundry Debtors",
      nature: "ASSET",
      basis: "CLOSING_BALANCE",
      budget: "4000000.0000",
      actual: "3500000.0000",
      actual_debit: "3500000.0000",
      variance: "-500000.0000",
      variance_percent: "-12.50",
      status: "UNDER",
    });
    await renderScreen(
      varianceClient(
        view({
          rows: [row(), closing],
          totals: [
            {
              basis: "CLOSING_BALANCE",
              rows: 1,
              budget: "4000000.0000",
              actual: "3500000.0000",
              variance: "-500000.0000",
            },
            {
              basis: "NETT_TRANSACTIONS",
              rows: 1,
              budget: "500000.0000",
              actual: "620000.0000",
              variance: "120000.0000",
            },
          ],
          // The server's own answer: no addable total, and three zeros.
          totals_are_addable: false,
          total_budget: "0.0000",
          total_actual: "0.0000",
          total_variance: "0.0000",
        }),
      ),
    );

    // ⛔ THE FOOTER MUST BE ABSENT. Drawn, it would read 0.00 — a figure that
    // foots, ties to nothing, and is believed.
    expect(
      screen.queryByTestId("budget-grand-total"),
      "a grand total was drawn over a plan mixing a closing balance with nett " +
        "transactions. The server sent three ZEROS with totals_are_addable=false, " +
        "so the footer would read 0.00 against 4.5 lakh of budget — the most " +
        "believable wrong number this report can print",
    ).toBeNull();

    // And the ABSENCE must be explained in place, or the next person "fixes" it.
    const why = screen.getByTestId("budget-no-grand-total").textContent ?? "";
    expect(
      why,
      "the suppressed total is not explained on the page. A missing footer with " +
        "no reason reads as a bug, and the obvious repair is to add the two bases " +
        "together — which is the error the suppression exists to prevent",
    ).toMatch(/NO GRAND TOTAL/i);
    expect(why).toMatch(/POSITION/);
    expect(why).toMatch(/MOVEMENT/);

    // The per-basis subtotals are the replacement and must both survive.
    expect(screen.queryByTestId("budget-total:CLOSING_BALANCE")).not.toBeNull();
    expect(screen.queryByTestId("budget-total:NETT_TRANSACTIONS")).not.toBeNull();
  });
});

describe("Budget Variance — the four statuses that are easy to misread", () => {
  it("⛔ shows UNSPENT as untouched and never as ordinary underspend", async () => {
    await renderScreen(
      varianceClient(
        view({
          rows: [
            row({
              target_name: "Office Rent",
              actual: "0.0000",
              actual_debit: "0.0000",
              actual_credit: "0.0000",
              variance: "-500000.0000",
              variance_percent: "-100.00",
              status: "UNSPENT",
              notes: ["Nothing was posted against this at all in the period."],
            }),
          ],
          status_counts: { UNSPENT: 1 },
        }),
      ),
    );
    const text = screen.getByTestId("budget-row:Office Rent").textContent ?? "";
    expect(
      text,
      "an UNSPENT budget is not labelled as untouched. Shown as ordinary " +
        "underspend it hides that the plan may be wrong, or that nobody is keying " +
        "the invoices — both of which are the reason to look at this report",
    ).toMatch(/UNSPENT/);
    expect(text).toMatch(/nothing at all was posted/i);
  });

  it("⛔ shows NETTED_TO_ZERO with BOTH raw sides, so it cannot read as untouched", async () => {
    await renderScreen(
      varianceClient(
        view({
          rows: [
            row({
              target_name: "Suspense",
              actual: "0.0000",
              actual_debit: "300000.0000",
              actual_credit: "300000.0000",
              variance: "-500000.0000",
              variance_percent: "-100.00",
              status: "NETTED_TO_ZERO",
            }),
          ],
        }),
      ),
    );
    const text = screen.getByTestId("budget-row:Suspense").textContent ?? "";
    expect(
      text,
      "a netted-to-zero row does not show the debit and credit that cancelled. " +
        "Its nett is identically zero, so without the raw sides it is " +
        "indistinguishable from a budget nobody touched — and the 3,00,000 that " +
        "moved each way is invisible on the report entirely",
    ).toMatch(/Dr\s*3,?00,?000/);
    expect(text).toMatch(/NETTED TO ZERO/i);
  });

  it("⛔ never prints a percentage where the server sent null", async () => {
    await renderScreen(
      varianceClient(
        view({
          rows: [],
          totals: [],
          totals_are_addable: true,
          total_budget: "0.0000",
          total_actual: "0.0000",
          total_variance: "0.0000",
          unbudgeted: [
            row({
              target_id: "ledger-stationery",
              target_name: "Stationery",
              budget: "0.0000",
              actual: "83000.0000",
              actual_debit: "83000.0000",
              variance: "83000.0000",
              variance_percent: null,
              variance_percent_absent_because:
                "no budget was set for this target",
              status: "UNBUDGETED",
              notes: ["No budget line covers this."],
            }),
          ],
          unbudgeted_count: 1,
          unbudgeted_actual: "83000.0000",
          status_counts: { UNBUDGETED: 1 },
        }),
      ),
    );
    // ⛔ ASSERTED ON THE CELL, NOT THE ROW. The row legitimately contains the
    // words "NOT 100% over budget" — the honest explanation of what UNBUDGETED
    // means — so a row-wide scan for a digit-then-percent matches the EXPLANATION
    // and fails on correct code. That is exactly what the first draft of this test
    // did.
    const pct = screen.getByTestId("budget-pct:Stationery").textContent ?? "";
    expect(
      pct,
      "the percentage cell for an unbudgeted row is not empty. There is no budget " +
        "for it to be a percentage OF — any number here (0, 100, or an infinity) " +
        "is a figure a reader will believe and act on",
    ).toBe("—");

    const text = screen.getByTestId("budget-row:Stationery").textContent ?? "";
    // And the budget column must not read "0.00", which is a budget of nothing
    // rather than no budget at all.
    expect(
      text,
      "the unbudgeted row's budget column shows a figure rather than saying no " +
        "budget was set",
    ).toMatch(/no budget set/i);
    expect(text).toMatch(/UNBUDGETED/);
  });
});

describe("Budget Variance — the plan and its gaps stay apart", () => {
  it("⛔ renders unbudgeted actuals in their OWN section, outside the plan's totals", async () => {
    await renderScreen(
      varianceClient(
        view({
          unbudgeted: [
            row({
              target_id: "ledger-stationery",
              target_name: "Stationery",
              budget: "0.0000",
              actual: "83000.0000",
              actual_debit: "83000.0000",
              variance: "83000.0000",
              variance_percent: null,
              status: "UNBUDGETED",
            }),
          ],
          unbudgeted_count: 1,
          unbudgeted_actual: "83000.0000",
          status_counts: { OVER: 1, UNBUDGETED: 1 },
        }),
      ),
    );

    const gaps = screen.getByTestId("budget-variance-unbudgeted").textContent ?? "";
    expect(
      gaps,
      "the unbudgeted section does not say it is excluded from the totals above. " +
        "A reader who adds it in is double-counting the page; one who assumes it " +
        "is already included is understating the spend nobody planned",
    ).toMatch(/NOT in the totals above/i);
    expect(gaps).toMatch(/Stationery/);

    // ⛔ AND IT MUST NOT BE IN THE PLAN'S TABLE.
    const plan = screen.getByTestId("budget-variance-rows").textContent ?? "";
    expect(
      plan,
      "an unbudgeted actual appears in the PLAN's table. `rows` is the plan: a " +
        "target with a budget of zero in it makes the budget column carry a figure " +
        "nobody set, and the plan's totals stop meaning anything",
    ).not.toMatch(/Stationery/);
  });

  it("says so when a plan has no lines, rather than looking met", async () => {
    await renderScreen(
      varianceClient(
        view({
          rows: [],
          totals: [],
          total_budget: "0.0000",
          total_actual: "0.0000",
          total_variance: "0.0000",
          status_counts: {},
          notes: ["This budget has no lines at this grain."],
        }),
      ),
    );
    const text =
      screen.getByTestId("budget-variance-rows-empty").textContent ?? "";
    expect(
      text,
      "a plan with no lines renders an empty table with no explanation. That " +
        "reads as “nothing was planned”, which is indistinguishable from “the plan " +
        "was never keyed” — and the second is a data-entry emergency",
    ).toMatch(/ABSENCE of a plan/i);
  });
});

describe("Budget Variance — the refusal", () => {
  it("⛔ renders a 403 as a DECISION, not as an error or an empty table", async () => {
    await renderScreen(
      varianceClient(new ApiError(403, "forbidden") as unknown as Error),
    );
    const panel = screen.getByTestId("budget-variance-denied").textContent ?? "";
    expect(
      panel,
      "a refused report does not name the permission that refused it, so an " +
        "operator cannot tell an owner what to grant",
    ).toMatch(/report:budget-variance/);
    expect(
      panel,
      "the refusal does not say that printing is a separate permission",
    ).toMatch(/separate permission/i);
    // ⛔ AND NOT AN EMPTY TABLE PRETENDING TO BE ZERO DATA.
    expect(screen.queryByTestId("budget-variance-rows")).toBeNull();
  });
});

describe("Budget Variance — the caveats travel", () => {
  it("renders the server's notes verbatim rather than filtering them", async () => {
    const notes = [
      "The budget column is this plan's WHOLE period and is not apportioned.",
      "OVER and UNDER are statements about arithmetic, not about good and bad.",
    ];
    await renderScreen(varianceClient(view({ notes })));
    const rendered =
      screen.getByTestId("budget-variance-notes").textContent ?? "";
    for (const n of notes) {
      expect(
        rendered,
        `the note “${n}” was dropped. A caveat that does not travel with the ` +
          "numbers is a caveat nobody reads, and this screen may style them but " +
          "may not decide whether to show them",
      ).toContain(n);
    }
  });
});
