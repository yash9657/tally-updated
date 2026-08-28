import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  act,
  fireEvent,
} from "@testing-library/react";

import { BudgetsScreen } from "./BudgetsScreen";
import { CompanyProvider } from "../../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
} from "../../app/ScreenActions";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type { Budget, CoredClient } from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";

/**
 * THE BUDGET MASTER (T11) — the two refusals that keep a plan honest, and the
 * one label that keeps a figure meaningful.
 *
 * ⛔ THE THINGS TESTED HERE ARE ALL ABOUT WHAT THE FORM WILL NOT LET AN OPERATOR
 * DO, because a budget master has no wrong answer that looks wrong. A cost centre
 * budgeted on a closing balance stores fine, reports fine, and is nonsense; a
 * side derived the wrong way turns every rupee of spend into a negative actual
 * and still foots.
 *
 *  1. A COST CENTRE IS NOT OFFERED A CLOSING BALANCE. It holds no position — only
 *     allocations against dated postings — so its "closing balance" would be
 *     every rupee booked to that department since the books began.
 *  2. A COST CENTRE IS NOT OFFERED A DEFAULTED SIDE. It has no nature to derive
 *     one from, and a coin-flip default inverts the whole comparison.
 *  3. EVERY LINE SHOWS ITS BASIS IN WORDS. The token alone would let an operator
 *     set a position ceiling believing they had set a spend cap.
 *  4. AN EMPTY PLAN SAYS SO. A variance report over it is a page of unbudgeted
 *     actuals, which is indistinguishable from "the plan was never keyed".
 */

afterEach(cleanup);

const COMPANY = "company-demo";

function snap(): NavSnapshot {
  return {
    screen: "budgets",
    screenStackDepth: 2,
    canReturnToVoucher: false,
    config: {},
  } as unknown as NavSnapshot;
}

const PLAN: Budget = {
  id: "budget-fy26",
  name: "FY 2026-27 Plan",
  from_date: "2026-04-01",
  to_date: "2027-03-31",
  line_count: 1,
  created_by: "seed",
  created_at: "2026-04-01T00:00:00Z",
};

function budgetsClient(over: Partial<Record<string, unknown>> = {}): CoredClient {
  const base = createMockClient({ seed: false });
  return {
    ...base,
    listBudgets: vi.fn(async () => ({ budgets: [PLAN] })),
    listBudgetLines: vi.fn(async () => ({
      budget_id: PLAN.id,
      lines: [
        {
          id: "line-1",
          budget_id: PLAN.id,
          dimension: "LEDGER" as const,
          target_id: "ledger-travel",
          target_name: "Travel Expenses",
          cost_category_id: "",
          cost_category_name: "",
          nature: "EXPENSE",
          basis: "NETT_TRANSACTIONS" as const,
          dr_cr: "D",
          amount: "500000.0000",
          note: "",
          basis_means:
            "MOVEMENT inside the budget period, with the opening balance excluded",
          created_at: "2026-04-01T00:00:00Z",
        },
      ],
    })),
    listCostCentres: vi.fn(async () => ({
      cost_centres: [
        {
          id: "centre-mumbai",
          name: "Mumbai",
          cost_category_id: "cost-cat-branch",
          cost_category_name: "Branch",
          parent_id: "",
          parent_name: "",
          depth: 0,
          path: "Mumbai",
          ancestry_truncated: false,
          created_by: "seed",
          created_at: "2026-04-01T00:00:00Z",
        },
      ],
      cost_categories: [],
    })),
    ...over,
  } as unknown as CoredClient;
}

async function renderScreen(client: CoredClient) {
  let fire: (kind: "createBudget") => Promise<string | void> = async () => {};
  function Harness() {
    const dispatch = useScreenActionDispatcher();
    fire = async (kind) => {
      const res = dispatch(kind, snap());
      if (res.pending) return await res.pending;
      return res.message;
    };
    return <BudgetsScreen />;
  }
  await act(async () => {
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
  });
  return {
    openCreate: async () => {
      await act(async () => {
        await fire("createBudget");
      });
    },
  };
}

describe("Budgets — the cost-centre refusals", () => {
  it("⛔ never offers a CLOSING BALANCE for a cost centre", async () => {
    const { openCreate } = await renderScreen(budgetsClient());
    await openCreate();
    await act(async () => {
      fireEvent.click(screen.getByTestId("budget-kind-line"));
    });
    await act(async () => {
      fireEvent.change(screen.getByTestId("budget-line-dimension"), {
        target: { value: "COST_CENTRE" },
      });
    });

    const basis = screen.getByTestId("budget-line-basis") as HTMLSelectElement;
    const offered = Array.from(basis.options).map((o) => o.value);
    expect(
      offered,
      "the form offers CLOSING_BALANCE for a cost centre. A cost centre is not an " +
        "account: it holds no opening balance and carries no position, only " +
        "allocations against dated postings — so its “closing balance” is every " +
        "rupee booked to that department since the books began, which would make a " +
        "year's budget look almost entirely unspent in year one and several times " +
        "overspent in year four",
    ).not.toContain("CLOSING_BALANCE");
    expect(offered).toContain("NETT_TRANSACTIONS");

    // …and the form says WHY, rather than just omitting the option.
    expect(
      screen.getByTestId("budget-centre-basis-note").textContent ?? "",
      "the missing option is not explained. An operator who cannot find the basis " +
        "they wanted will assume the form is broken",
    ).toMatch(/holds no opening balance/i);
  });

  it("⛔ never offers a DEFAULTED side for a cost centre, and does for a ledger", async () => {
    const { openCreate } = await renderScreen(budgetsClient());
    await openCreate();
    await act(async () => {
      fireEvent.click(screen.getByTestId("budget-kind-line"));
    });

    // A LEDGER: the empty option — "the target's natural side" — is the ordinary
    // case and an operator never types a debit indicator.
    const ledgerSides = Array.from(
      (screen.getByTestId("budget-line-drcr") as HTMLSelectElement).options,
    ).map((o) => o.value);
    expect(
      ledgerSides,
      "a LEDGER line does not offer the natural-side default, so every operator " +
        "would have to know what a debit is to budget anything",
    ).toContain("");

    // A COST CENTRE: no default, because there is no nature to derive one from.
    await act(async () => {
      fireEvent.change(screen.getByTestId("budget-line-dimension"), {
        target: { value: "COST_CENTRE" },
      });
    });
    const centreSides = Array.from(
      (screen.getByTestId("budget-line-drcr") as HTMLSelectElement).options,
    ).map((o) => o.value);
    expect(
      centreSides,
      "a COST CENTRE line offers a natural-side default. A cost centre is an axis " +
        "of analysis, not an account: it has no nature, so the default would be a " +
        "coin flip — and the wrong face turns every rupee of departmental cost " +
        "into a negative actual against the plan",
    ).not.toContain("");
    expect(centreSides).toEqual(["D", "C"]);
  });
});

describe("Budgets — the basis travels in words", () => {
  it("⛔ prints what the basis MEANS on every line, not just the token", async () => {
    await renderScreen(budgetsClient());
    // The lines render only once a plan is OPENED — the screen lists plans first
    // and drills into one, so the click is part of the path a reader takes.
    await act(async () => {
      fireEvent.click(screen.getByText(PLAN.name));
    });
    const line = screen.getByTestId("budget-line:Travel Expenses").textContent ?? "";
    expect(
      line,
      "a budget line shows its basis as a bare token. CLOSING_BALANCE and " +
        "NETT_TRANSACTIONS answer different questions and the same figure means " +
        "different things under each — a screen that renders only the token lets " +
        "an operator set a position ceiling believing they set a spend cap",
    ).toMatch(/MOVEMENT inside the budget period/i);
    expect(line).toMatch(/NETT_TRANSACTIONS/);
  });
});

describe("Budgets — an empty plan and a refusal", () => {
  it("⛔ says a plan has NO lines rather than showing a blank", async () => {
    await renderScreen(
      budgetsClient({
        listBudgets: vi.fn(async () => ({
          budgets: [{ ...PLAN, line_count: 0 }],
        })),
        listBudgetLines: vi.fn(async () => ({
          budget_id: PLAN.id,
          lines: [],
        })),
      }),
    );
    expect(
      screen.queryByTestId(`budget-empty:${PLAN.name}`),
      "an EMPTY plan is not flagged in the list. A variance report over it is a " +
        "page of unbudgeted actuals, which reads as “nothing was planned” and is " +
        "indistinguishable from “the plan was never keyed” — and the second is a " +
        "data-entry emergency",
    ).not.toBeNull();
  });

  it("⛔ renders a 403 as a DECISION that costs no permission rule", async () => {
    await renderScreen(
      budgetsClient({
        listBudgets: vi.fn(async () => {
          throw new ApiError(403, "forbidden");
        }),
      }),
    );
    const panel = screen.getByTestId("budgets-denied").textContent ?? "";
    expect(
      panel,
      "the refusal does not name the permission, so an operator cannot tell an " +
        "owner what to grant",
    ).toMatch(/allow display budget/);
    // ⛔ AND IT MUST NOT SAY THE COMPANY HAS NO BUDGETS. "There are none" and
    // "you may not see them" are different sentences about different subjects.
    expect(
      screen.queryByTestId("budgets-empty"),
      "a refused level is told this company has no budgets. That states as a FACT " +
        "something the screen does not know, and sends the operator to create a " +
        "plan on a form that refuses them for the same token",
    ).toBeNull();
  });
});
