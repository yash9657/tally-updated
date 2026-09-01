import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, act, fireEvent } from "@testing-library/react";

import { PayrollRegisterScreen } from "./PayrollRegisterScreen";
import { PayslipScreen } from "./PayslipScreen";
import { CompanyProvider } from "../../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
  type ScreenActionKind,
} from "../../app/ScreenActions";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type {
  CoredClient,
  PayrollRegisterResponse,
  PayslipResponse,
} from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";
import { EMPLOYEE_ROW_PREFIX } from "../../keystroke/navMachine";
import { resolvePrintTarget } from "../printTarget";

/**
 * THE PAYROLL REGISTER AND THE PAYSLIP — four properties.
 *
 * ⛔ (1) THE RULESET VERSIONS ARE ON THE REPORT. A payroll figure with no
 * statement of which law produced it cannot be checked by anybody who was not
 * present when it was run — and a register is exactly the document somebody
 * checks a year later. The server puts them on the JSON and on the printed page;
 * this screen is the only place the chain could break.
 *
 * ⛔ (2) THE MONTH ON THE HEADING AND THE FIGURES UNDER IT ARE THE SAME MONTH.
 * A failed re-read must clear the rows, not leave the previous month's figures
 * beneath a heading naming the new one. That is the whole class of defect a
 * report with a typed dimension has.
 *
 * ⛔ (3) THE PAYSLIP RESOLVES A CODE AND OPENS THAT PERSON. `employee.code` is
 * unique in the company and a NAME is not, so a name-keyed drill would open
 * whichever the resolver met first — one individual's entire pay under another
 * individual's heading, correctly rendered, with nothing saying so.
 *
 * ⛔ (4) Ctrl+P SENDS THE LOADED MONTH — not the typed one. A month typed but
 * never fetched has produced nothing on screen, and printing it would put a
 * document on paper that does not match the page it came from.
 */

afterEach(cleanup);

const COMPANY = "company-demo";

function registerSnap(over: Partial<NavSnapshot> = {}): NavSnapshot {
  return {
    screen: "payroll-register",
    screenStackDepth: 2,
    canReturnToVoucher: false,
    ...over,
  } as unknown as NavSnapshot;
}

function view(over: Partial<PayrollRegisterResponse> = {}): PayrollRegisterResponse {
  const row = {
    employee_id: "emp-1",
    employee_code: "E-0007",
    employee_name: "Asha Rao",
    days_paid: "31.0000",
    gross: "18000.0000",
    epf_employee: "1800.0000",
    esi_employee: "135.0000",
    tds: "0.0000",
    deductions: "1935.0000",
    net: "16065.0000",
    epf_employer: "550.0000",
    eps_employer: "1250.0000",
    esi_employer: "585.0000",
  };
  return {
    pay_run_id: "run-1",
    wage_month: "2026-07",
    status: "POSTED",
    ss_ruleset_version: "ss-2026-07",
    it_ruleset_version: "it-2026-27",
    rows: [row],
    totals: { ...row, employee_id: undefined, employee_code: undefined, employee_name: undefined },
    ...over,
  };
}

function renderRegister(client: CoredClient, s: NavSnapshot = registerSnap()) {
  let fire: (kind: ScreenActionKind) => Promise<string | void> = async () => {};
  function Harness() {
    const dispatch = useScreenActionDispatcher();
    fire = async (kind) => {
      const res = dispatch(kind, s);
      if (res.pending) return await res.pending;
      return res.message;
    };
    return <PayrollRegisterScreen snapshot={s} onSelect={() => {}} />;
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

async function showMonth(month: string) {
  fireEvent.change(await screen.findByTestId("payroll-register-month"), {
    target: { value: month },
  });
  await act(async () => {
    fireEvent.click(screen.getByText("Show the register"));
  });
}

function registerClient(
  impl: (month: string) => Promise<PayrollRegisterResponse>,
): CoredClient {
  const base = createMockClient({ seed: false });
  return {
    ...base,
    payrollRegister: vi.fn(async (_c: string, month: string) => impl(month)),
  } as unknown as CoredClient;
}

describe("the register states which law produced its figures", () => {
  it("renders both ruleset versions", async () => {
    const client = registerClient(async () => view());
    renderRegister(client);
    await showMonth("2026-07");
    const line = await screen.findByTestId("payroll-register-rulesets");
    expect(line.textContent).toContain("ss-2026-07");
    expect(line.textContent).toContain("it-2026-27");
  });

  it("renders the SERVER's totals row and does not compute one", async () => {
    // Every figure is a fixed-4 string rendered as received. A browser that
    // summed the column would disagree with the server the first time rounding
    // mattered, and the disagreement would be invisible.
    const client = registerClient(async () =>
      view({ totals: { ...view().totals, net: "99999.0000" } }),
    );
    renderRegister(client);
    await showMonth("2026-07");
    const totals = await screen.findByTestId("payroll-register-totals");
    // 99999.0000 is deliberately NOT the sum of the rows: the screen must render
    // what it was given, so a mismatch shows up as the server's problem rather
    // than being papered over here.
    expect(totals.textContent).toContain("99,999.00");
  });
});

describe("the heading and the figures under it are always the same month", () => {
  it("clears the rows when a re-read fails", async () => {
    // ⛔ THE DEFECT THIS PREVENTS: July's figures left on screen under a heading
    // that now says August, because the second fetch failed. Every number would
    // be real, which is what makes it dangerous.
    let calls = 0;
    const client = registerClient(async () => {
      calls += 1;
      if (calls === 1) return view();
      // ⚠️ A GENUINE failure, not the no-pay-run one. This test's property is
      // "a failed re-read clears the rows"; since D112 the unrun month is an
      // EMPTY STATE with its own test above, which asserts the same clearing.
      throw new ApiError(500, "the payroll service is unavailable, reading 2026-08", {
        error: "the payroll service is unavailable, reading 2026-08",
      });
    });
    renderRegister(client);
    await showMonth("2026-07");
    expect(await screen.findByTestId("payroll-register-table")).toBeTruthy();

    await showMonth("2026-08");
    expect(screen.queryByTestId("payroll-register-table")).toBeNull();
    expect(screen.queryByTestId("payroll-register-rulesets")).toBeNull();
    expect(
      (await screen.findByTestId("payroll-register-error")).textContent,
    ).toContain("2026-08");
  });

  it("shows an EMPTY STATE for a month nobody has run, not an error", async () => {
    // ⛔ D112. "You have not run September payroll yet" is the COMMONEST state
    // on this screen — its whole purpose is a month that may not have been run
    // — and it was rendered as a red server error reading
    //
    //   payrolldb: payroll register for 2026-08: payrolldb: pay run not found:
    //   no live pay run for 2026-08
    //
    // measured on the wire at badea6f. The screen now switches on the server's
    // NO_PAY_RUN code, never on the prose.
    let calls = 0;
    const client = registerClient(async () => {
      calls += 1;
      if (calls === 1) return view();
      throw new ApiError(
        404,
        "pay run not found for wage month 2026-08 — no payroll has been run for it yet",
        { code: "NO_PAY_RUN", error: "pay run not found for wage month 2026-08" },
      );
    });
    renderRegister(client);
    await showMonth("2026-07");
    expect(await screen.findByTestId("payroll-register-table")).toBeTruthy();

    await showMonth("2026-08");

    // It is not an error.
    expect(screen.queryByTestId("payroll-register-error")).toBeNull();

    // It is an empty state that names the month and says what to do next.
    const empty = await screen.findByTestId("payroll-register-empty");
    expect(empty.textContent).toContain("2026-08");
    expect(empty.textContent).toMatch(/pay run/i);
    // ⛔ AND IT MUST NOT LEAK THE SERVER'S INTERNALS EITHER.
    expect(empty.textContent).not.toMatch(/payrolldb|not found/i);

    // ⛔ THE ROW-CLEARING PROPERTY STILL HOLDS ON THIS PATH. An empty state that
    // left July's figures under an August heading would be the SAME defect the
    // error path was written to avoid, one branch over.
    expect(screen.queryByTestId("payroll-register-table")).toBeNull();
    expect(screen.queryByTestId("payroll-register-rulesets")).toBeNull();
  });

  it("still shows the server's own words for a REAL failure", async () => {
    // The control. Only NO_PAY_RUN becomes an empty state; everything else
    // keeps the server's sentence, for the reason usePrint gives — a refusal
    // written to tell an operator what to fix must reach them intact.
    const client = registerClient(async () => {
      throw new ApiError(422, "employee E-0007 has no UAN", {
        error: "employee E-0007 has no UAN",
      });
    });
    renderRegister(client);
    await showMonth("2026-08");
    expect(screen.queryByTestId("payroll-register-empty")).toBeNull();
    expect(
      (await screen.findByTestId("payroll-register-error")).textContent,
    ).toContain("E-0007 has no UAN");
  });

  it("refuses a malformed month locally, and makes no request", async () => {
    const payrollRegister = vi.fn();
    const base = createMockClient({ seed: false });
    const client = { ...base, payrollRegister } as unknown as CoredClient;
    renderRegister(client);
    await showMonth("2026-7");
    expect(payrollRegister).not.toHaveBeenCalled();
    expect(
      (await screen.findByTestId("payroll-register-error")).textContent,
    ).toMatch(/wage month/i);
  });
});

describe("Alt+S — the statutory files", () => {
  it("refuses to open before a month is shown", async () => {
    // Refusing before opening rather than opening onto nothing: both files are
    // FOR a month, and a panel with no month could only ask for one.
    const client = registerClient(async () => view());
    const h = renderRegister(client);
    const message = await h.press("payrollStatutoryFiles");
    expect(message).toMatch(/wage month/i);
    expect(screen.queryByTestId("statutory-files-panel")).toBeNull();
  });

  it("shows the SERVER's disclosure verbatim after a download", async () => {
    // ⛔ NOTHING THIS PRODUCT EMITS HAS BEEN THROUGH THE EPFO OR ESIC PORTAL —
    // neither can be reached from here — and the disclosure travels ON the
    // response. Repeating it in front of the operator is what stops a download
    // reading as a finished filing.
    const base = registerClient(async () => view());
    const client = {
      ...base,
      payrollPFECR: vi.fn(async () => ({
        blob: new Blob(["x"]),
        filename: "ECR-2026-07.zip",
        statutoryFile: "ECR_2026_07.txt",
        disclosure: "NOT validator-verified; see the .README.txt inside this archive",
      })),
    } as unknown as CoredClient;
    const h = renderRegister(client);
    await showMonth("2026-07");
    await h.press("payrollStatutoryFiles");
    await act(async () => {
      fireEvent.click(screen.getByTestId("download-ecr"));
    });
    const notice = await screen.findByTestId("statutory-files-notice");
    expect(notice.textContent).toContain("NOT validator-verified");
    expect(notice.textContent).toContain("ECR_2026_07.txt");
    expect(notice.textContent).toMatch(/Do not treat this as a filed return/);
  });

  it("shows a refusal verbatim — it names who is missing a UAN", async () => {
    const base = registerClient(async () => view());
    const client = {
      ...base,
      payrollPFECR: vi.fn(async () => {
        throw new ApiError(422, "payrollfile: employee E-0007 has no UAN");
      }),
    } as unknown as CoredClient;
    const h = renderRegister(client);
    await showMonth("2026-07");
    await h.press("payrollStatutoryFiles");
    await act(async () => {
      fireEvent.click(screen.getByTestId("download-ecr"));
    });
    expect(
      (await screen.findByTestId("statutory-files-notice")).textContent,
    ).toContain("E-0007 has no UAN");
  });
});

describe("Ctrl+P sends what is on screen, not what was typed", () => {
  it("refuses with no month, and prints the LOADED month with it", () => {
    // Pure resolution — no screen, no fetch. `resolvePrintTarget` is where the
    // decision lives, and invariant I-5 is that a refusal never becomes a
    // request.
    const noMonth = resolvePrintTarget(registerSnap(), {
      voucherID: null,
      payrollMonth: null,
    });
    expect(noMonth.kind).toBe("refusal");

    const loaded = resolvePrintTarget(registerSnap(), {
      voucherID: null,
      payrollMonth: "2026-07",
    });
    expect(loaded.kind).toBe("report");
    if (loaded.kind === "report") {
      expect(loaded.slug).toBe("payroll-register");
      expect(loaded.query?.month).toBe("2026-07");
      // ⛔ THE LABEL NAMES THE MONTH. The operator reads what went to paper
      // without opening the PDF.
      expect(loaded.label).toContain("2026-07");
    }
  });

  it("a payslip print carries the employee AND names them", () => {
    const slipSnap = {
      screen: "payroll-payslip",
      screenStackDepth: 3,
      canReturnToVoucher: false,
    } as unknown as NavSnapshot;
    const target = resolvePrintTarget(slipSnap, {
      voucherID: null,
      payrollMonth: "2026-07",
      payrollEmployeeID: "emp-1",
      payrollEmployeeName: "Asha Rao",
    });
    expect(target.kind).toBe("report");
    if (target.kind === "report") {
      expect(target.query?.employee_id).toBe("emp-1");
      // ⛔ THE NAME ON THE STATUS LINE. On this screen more than any other in the
      // product, the failure it catches is handing one employee another
      // employee's salary.
      expect(target.label).toContain("Asha Rao");
    }

    // The control: without an employee it must REFUSE rather than print "some
    // payslip".
    const bare = resolvePrintTarget(slipSnap, {
      voucherID: null,
      payrollMonth: "2026-07",
    });
    expect(bare.kind).toBe("refusal");
  });
});

describe("the payslip opens the person the code names", () => {
  function slipSnap(code: string, month = "2026-07"): NavSnapshot {
    return {
      screen: "payroll-payslip",
      screenStackDepth: 3,
      canReturnToVoucher: false,
      detailOf: `${EMPLOYEE_ROW_PREFIX}${code}`,
      config: { wageMonth: month },
    } as unknown as NavSnapshot;
  }

  function slipClient(): CoredClient {
    const base = createMockClient({ seed: false });
    const reg = view({
      rows: [
        { ...view().rows[0]!, employee_id: "emp-1", employee_code: "E-0007", employee_name: "Asha Rao" },
        { ...view().rows[0]!, employee_id: "emp-2", employee_code: "E-0008", employee_name: "Asha Rao" },
      ],
    });
    return {
      ...base,
      payrollRegister: vi.fn(async () => reg),
      payslip: vi.fn(
        async (_c: string, month: string, employeeID: string): Promise<PayslipResponse> => ({
          pay_run_id: "run-1",
          wage_month: month,
          employee_id: employeeID,
          employee_code: employeeID === "emp-1" ? "E-0007" : "E-0008",
          employee_name: "Asha Rao",
          days_paid: "31.0000",
          lop_days: "0.0000",
          gross: employeeID === "emp-1" ? "18000.0000" : "42000.0000",
          deductions: "0.0000",
          net: employeeID === "emp-1" ? "18000.0000" : "42000.0000",
          lines: [],
          deferrals: [
            {
              component: "EDLI administrative charges",
              reason: "no rate has been notified under the 2026 Scheme",
              citation: "EPF Scheme 2026 para 29(2)",
            },
          ],
          ss_ruleset_version: "ss-2026-07",
          it_ruleset_version: "it-2026-27",
        }),
      ),
    } as unknown as CoredClient;
  }

  function renderSlip(client: CoredClient, s: NavSnapshot) {
    render(
      <CompanyProvider
        client={client}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <ScreenActionsProvider>
          <PayslipScreen snapshot={s} onSelect={() => {}} />
        </ScreenActionsProvider>
      </CompanyProvider>,
    );
  }

  it("opens the CODE's payslip even when two people share a name", async () => {
    // ⛔ THE REASON THE DRILL CARRIES A CODE. Two employees called "Asha Rao" —
    // legal, and common — and a name-keyed resolver opens whichever it met
    // first: one person's entire pay under another person's heading.
    renderSlip(slipClient(), slipSnap("E-0008"));
    const heading = await screen.findByTestId("payslip-who");
    expect(heading.textContent).toContain("E-0008");
    expect(await screen.findByTestId("payslip-gross")).toBeTruthy();
    expect(screen.getByTestId("payslip-gross").textContent).toContain("42,000");
  });

  it("says so when the code has no payslip in THIS month's run", async () => {
    // A different answer from "that person does not exist" — they may have
    // joined after the month ended or left before it began.
    renderSlip(slipClient(), slipSnap("E-9999"));
    expect(
      (await screen.findByTestId("payslip-error")).textContent,
    ).toMatch(/no payslip in the 2026-07 pay run/);
  });

  it("renders the DEFERRALS — what was not computed, and why", async () => {
    // ⛔ ON THE PAYSLIP, NOT IN A LOG. A blank where a number should be is a
    // question the employee will ask, and the honest answer is "the rate has not
    // been notified", never "zero".
    renderSlip(slipClient(), slipSnap("E-0007"));
    const panel = await screen.findByTestId("payslip-deferrals");
    expect(panel.textContent).toContain("EDLI administrative charges");
    expect(panel.textContent).toContain("no rate has been notified");
  });

  it("refuses to render without BOTH a month and an employee", async () => {
    const bare = {
      screen: "payroll-payslip",
      screenStackDepth: 3,
      canReturnToVoucher: false,
    } as unknown as NavSnapshot;
    renderSlip(slipClient(), bare);
    expect(await screen.findByTestId("payslip-no-context")).toBeTruthy();
  });
});
