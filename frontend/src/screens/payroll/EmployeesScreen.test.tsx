import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, act, fireEvent } from "@testing-library/react";

import { EmployeesScreen } from "./EmployeesScreen";
import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
  type ScreenActionKind,
} from "../../app/ScreenActions";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type { CoredClient } from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";
import { EMPLOYEE_ROW_PREFIX } from "../../keystroke/navMachine";

/**
 * THE EMPLOYEE MASTER — and the one property that is the reason this screen
 * exists in the shape it does.
 *
 * ⛔ THE PROPERTY: A PERSON WHO CANNOT BE PAID MUST SAY SO, HERE, BY NAME.
 *
 * An employee created with `POST /employees` and nothing else makes cored refuse
 * the ENTIRE pay run — not their payslip, the whole month — with "no salary
 * structure is in force" / "no statutory election is in force". That refusal
 * arrives on the RUN, days later, at the one moment an operator cannot afford to
 * be blocked; until this wave neither child even had an HTTP route, so it was
 * unavoidable from any client.
 *
 * So the assertions below are about the SENTENCE the list shows, not about the
 * requests it makes. A list that renders a complete-looking row for an unpayable
 * person is the defect, whatever it fetched.
 */

afterEach(cleanup);

const COMPANY = "company-demo";

function snap(over: Partial<NavSnapshot> = {}): NavSnapshot {
  return {
    screen: "employees",
    screenStackDepth: 2,
    canReturnToVoucher: false,
    ...over,
  } as unknown as NavSnapshot;
}

function renderScreen(client: CoredClient, s: NavSnapshot = snap()) {
  let fire: (kind: ScreenActionKind) => Promise<string | void> = async () => {};
  function Harness() {
    const dispatch = useScreenActionDispatcher();
    fire = async (kind) => {
      const res = dispatch(kind, s);
      if (res.pending) return await res.pending;
      return res.message;
    };
    return <EmployeesScreen snapshot={s} onSelect={() => {}} />;
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
    press: async (kind: ScreenActionKind) => {
      let out: string | void = undefined;
      await act(async () => {
        out = await fire(kind);
      });
      return out as string | undefined;
    },
  };
}

/** A mock client with one employee who has neither child — the default state. */
async function clientWithUnpayableEmployee(): Promise<CoredClient> {
  const c = createMockClient({ seed: false });
  await c.createEmployee(COMPANY, { code: "E-0007", name: "Asha Rao" });
  return c;
}

describe("an employee who cannot be paid is named as such", () => {
  it("says NO and names BOTH missing halves", async () => {
    const client = await clientWithUnpayableEmployee();
    renderScreen(client);
    const cell = await screen.findByTestId("employee-unpayable:E-0007");
    // ⛔ IT NAMES WHICH HALF. "Not payable" alone sends an operator to look at
    // both, and they are set in different parts of the same panel.
    expect(cell.textContent).toMatch(/salary structure/i);
    expect(cell.textContent).toMatch(/statutory election/i);
    expect(screen.queryByTestId("employee-payable:E-0007")).toBeNull();
  });

  it("flips to YES once BOTH children exist, and not before", async () => {
    // ⛔ THE CONTROL FOR THE ASSERTION ABOVE. A cell that said "NO" for every
    // employee would pass the first test and be useless; this is the measurement
    // that it responds to the thing it claims to measure — and it checks the
    // HALFWAY state too, because one child is still an unpayable person.
    const client = createMockClient({ seed: false });
    const { employee_id } = await client.createEmployee(COMPANY, {
      code: "E-0008",
      name: "Bhavna Iyer",
    });

    await client.setSalaryComponent(COMPANY, employee_id, {
      effective_from: "2026-04-01",
      code: "Basic",
      type: "BASIC",
      amount: "18000",
    });
    const half = renderScreen(client);
    const halfCell = await screen.findByTestId("employee-unpayable:E-0008");
    // One child down: still NO, and now naming only the half that is left.
    expect(halfCell.textContent).toMatch(/statutory election/i);
    expect(halfCell.textContent).not.toMatch(/salary structure/i);
    void half;
    cleanup();

    await client.setStatutoryElection(COMPANY, employee_id, {
      effective_from: "2026-04-01",
      pf_base: "CEILING",
      eps_member: true,
      esi_applicable: true,
    });
    renderScreen(client);
    expect(await screen.findByTestId("employee-payable:E-0008")).toBeTruthy();
    expect(screen.queryByTestId("employee-unpayable:E-0008")).toBeNull();
  });

  it("says 'could not check' rather than NO when the children fail to load", async () => {
    // ⚠️ AN UNKNOWN IS NOT A NO. An operator who re-creates an employee over a
    // transient read failure has lost that person's history — and on this screen
    // "NO" is an instruction to go and re-key a salary.
    const base = await clientWithUnpayableEmployee();
    const client = {
      ...base,
      listSalaryComponents: vi.fn(async () => {
        throw new Error("network");
      }),
    } as unknown as CoredClient;
    renderScreen(client);
    expect(
      await screen.findByTestId("employee-payable-unknown:E-0007"),
    ).toBeTruthy();
  });
});

describe("creating an employee says the record is not finished", () => {
  it("the confirmation tells the operator they cannot be paid yet", async () => {
    // ⛔ A BARE "created" IS TRUE AND MISLEADING. The operator leaves believing
    // somebody is on the payroll, and finds out otherwise when the pay run
    // refuses the whole month.
    const client = createMockClient({ seed: false });
    const { press } = renderScreen(client);
    await press("createEmployee");
    fireEvent.change(await screen.findByTestId("employee-code"), {
      target: { value: "E-0009" },
    });
    fireEvent.change(screen.getByTestId("employee-name"), {
      target: { value: "Chandra Nair" },
    });
    await press("accept");

    const notice = await screen.findByTestId("employees-notice");
    expect(notice.textContent).toMatch(/cannot be paid yet/i);
    expect(notice.textContent).toMatch(/Alt\+S/);
  });

  it("Alt+S refuses BEFORE opening when no employee is highlighted", async () => {
    // Refusing before opening rather than opening onto nothing: a pay panel with
    // no employee could only ask which one, and the list is right there.
    const client = await clientWithUnpayableEmployee();
    const { press } = renderScreen(client);
    const message = await press("setEmployeePay");
    expect(message).toMatch(/highlight an employee/i);
    expect(screen.queryByTestId("employee-pay-panel")).toBeNull();
  });

  it("Alt+S opens the panel for the HIGHLIGHTED employee", async () => {
    const client = await clientWithUnpayableEmployee();
    const { press } = renderScreen(
      client,
      snap({ selectedItem: `${EMPLOYEE_ROW_PREFIX}E-0007` }),
    );
    await screen.findByTestId("employee-row:E-0007");
    await press("setEmployeePay");
    const panel = await screen.findByTestId("employee-pay-panel");
    expect(panel.textContent).toContain("E-0007");
    // Both halves say, in as many words, that this person cannot be paid.
    expect(screen.getByTestId("no-salary-components").textContent).toMatch(
      /cannot be paid/i,
    );
    expect(screen.getByTestId("no-statutory-elections").textContent).toMatch(
      /cannot be paid/i,
    );
  });
});

describe("the pay panel does not invent an answer the server refuses to default", () => {
  async function openPanel() {
    const client = await clientWithUnpayableEmployee();
    const h = renderScreen(
      client,
      snap({ selectedItem: `${EMPLOYEE_ROW_PREFIX}E-0007` }),
    );
    await screen.findByTestId("employee-row:E-0007");
    await h.press("setEmployeePay");
    await screen.findByTestId("employee-pay-panel");
    return { client, ...h };
  }

  it("pre-selects NO PF base — the two differ above the wage ceiling", async () => {
    await openPanel();
    const select = (await screen.findByTestId(
      "election-pf-base",
    )) as HTMLSelectElement;
    // ⛔ A DEFAULT HERE IS A CONTRIBUTION FIGURE NOBODY CHOSE. CEILING and ACTUAL
    // differ for anybody paid above the EPF wage ceiling, which is most of the
    // people an employer worries about — the server refuses an unstated base for
    // exactly that reason, and a screen that pre-picked one would be answering
    // for the operator.
    expect(select.value).toBe("");
  });

  it("offers 'no election' as the regime, and does NOT pre-select the default", async () => {
    await openPanel();
    const select = (await screen.findByTestId(
      "election-regime",
    )) as HTMLSelectElement;
    // "" is a REAL, RECORDABLE ANSWER — most employees make no election — and it
    // is a different fact from electing the default, which the server stores as
    // given precisely so the two stay distinguishable.
    expect(select.value).toBe("");
    expect(select.textContent).toMatch(/no election/i);
  });

  it("builds the component-type picker from the SERVED vocabulary", async () => {
    await openPanel();
    const select = (await screen.findByTestId(
      "component-type",
    )) as HTMLSelectElement;
    const labels = [...select.options].map((o) => o.textContent ?? "");
    // ⛔ AND IT CARRIES THE WAGES CLASSIFICATION, which is the reason the list is
    // fetched rather than written here: whether PF is contributed on a component
    // is a LEGAL question (s.2(88), and the holding that a universally paid
    // special allowance IS wages), and a second copy of that classification in
    // TypeScript is the drift that under-contributes for a whole service.
    expect(labels.some((l) => /BASIC.*wages/.test(l))).toBe(true);
    expect(labels.some((l) => /HRA.*excluded/.test(l))).toBe(true);
  });

  it("says so, and offers nothing, when the vocabulary cannot be read", async () => {
    // An empty picker with an explanation beats a picker built from a guess:
    // a guessed list can offer a type the server refuses, and `is_wages` decides
    // somebody's contributions.
    const base = await clientWithUnpayableEmployee();
    const client = {
      ...base,
      payrollVocabulary: vi.fn(async () => {
        throw new Error("down");
      }),
    } as unknown as CoredClient;
    const h = renderScreen(
      client,
      snap({ selectedItem: `${EMPLOYEE_ROW_PREFIX}E-0007` }),
    );
    await screen.findByTestId("employee-row:E-0007");
    await h.press("setEmployeePay");
    expect(await screen.findByTestId("vocabulary-unavailable")).toBeTruthy();
    const select = screen.getByTestId("component-type") as HTMLSelectElement;
    expect(select.options.length).toBe(1); // just "— choose —"
  });

  it("both statutory tickboxes start UNTICKED", async () => {
    await openPanel();
    // Each is a POSITIVE assertion about one person and neither is safe to
    // assume: assuming membership contributes on somebody who is not a member,
    // and assuming coverage withholds from somebody who is not covered.
    expect((screen.getByTestId("election-eps") as HTMLInputElement).checked).toBe(
      false,
    );
    expect((screen.getByTestId("election-esi") as HTMLInputElement).checked).toBe(
      false,
    );
  });
});

describe("a 403 is rendered as the decision it is", () => {
  it("explains WHY the employee master is its own permission", async () => {
    // Migration 0035 refuses `employee` to the seeded Data Entry level in every
    // tenant, so this is a NORMAL answer — not a fault, and not an empty table.
    const base = createMockClient({ seed: false });
    const client = {
      ...base,
      listEmployees: vi.fn(async () => {
        throw new ApiError(403, "not authorized");
      }),
    } as unknown as CoredClient;
    renderScreen(client);
    const panel = await screen.findByTestId("employees-denied");
    expect(panel.textContent).toMatch(/PAN/);
    expect(panel.textContent).toMatch(/Users & Roles/);
  });
});

// ---------------------------------------------------------------------------
// ⛔ THE REGISTRATION POINT THAT SHIPPED ALT+O COMPLETELY DEAD.
//
// Everything can be present and correct — the keymap binding, the KeymapAction,
// the ScreenActionKind, the navMachine case, the button-bar item and the
// screen's own useRegisterScreenAction — and the key still does NOTHING,
// silently, because `AppShell`'s SCREEN_ACTIONS map has no entry: the shell
// resolves the combo, finds no screen action, and forwards it to a machine that
// deliberately does not move. There is no error and no status line.
//
// ⚠️ THE SUITES ABOVE CANNOT CATCH IT. They call the dispatcher directly and
// bypass that map entirely — which is exactly how Alt+O passed every unit test
// it had. Only a test that renders the REAL SHELL and presses the REAL KEY does.
//
// And of the two keys here, Alt+S is the worse one to lose: it is the ONLY key
// that gives an employee a salary structure and a statutory election, without
// which cored refuses the whole pay run — days later, naming a person. A
// silently dead Alt+S looks exactly like a complete employee master right up to
// pay day.
// ---------------------------------------------------------------------------
describe("the shell actually ROUTES Alt+C and Alt+S on the employee master", () => {
  async function openEmployees(client: CoredClient) {
    render(
      <CompanyProvider
        client={client}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");
    fireEvent.mouseDown(screen.getByText("Employees"));
    fireEvent.keyDown(app, { key: "Enter" });
    await screen.findByRole("heading", { name: "Employees" });
    return app;
  }

  it("Alt+C opens the create form through the real key path", async () => {
    const client = createMockClient({ seed: false });
    const app = await openEmployees(client);
    // THE CONTROL: the form is NOT up.
    expect(screen.queryByTestId("employee-form")).toBeNull();
    await act(async () => {
      fireEvent.keyDown(app, { key: "c", altKey: true });
    });
    expect(await screen.findByTestId("employee-form")).toBeTruthy();
  });

  it("Alt+S opens the pay panel for the row the operator highlighted", async () => {
    const client = await clientWithUnpayableEmployee();
    const app = await openEmployees(client);
    // Highlight the row the way a click does — through the machine's SELECT.
    fireEvent.click(await screen.findByTestId("employee-row:E-0007"));
    // THE CONTROL: the panel is NOT up.
    expect(screen.queryByTestId("employee-pay-panel")).toBeNull();
    await act(async () => {
      fireEvent.keyDown(app, { key: "s", altKey: true });
    });
    const panel = await screen.findByTestId("employee-pay-panel");
    expect(panel.textContent).toContain("E-0007");
  });

  it("the button bar over this screen advertises both keys", async () => {
    // Without a `case "employees"` in buttonBarFor the screen falls through to
    // GATEWAY_BAR, WHICH NO TEST CATCHES: the bar would offer F3 Company and F11
    // Features over a half-keyed employee and say nothing about either write key.
    const client = createMockClient({ seed: false });
    await openEmployees(client);
    expect(screen.getByRole("button", { name: /Alt\+C New Employee/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Alt\+S Set Pay/ })).toBeTruthy();
  });
});
