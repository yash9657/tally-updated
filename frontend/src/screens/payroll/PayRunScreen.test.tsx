import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, act, fireEvent } from "@testing-library/react";

import { PayRunScreen } from "./PayRunScreen";
import { CompanyProvider } from "../../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
  type ScreenActionKind,
} from "../../app/ScreenActions";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type { CoredClient, PayRunResponse } from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";

/**
 * THE PAY RUN — three properties, and the first is the one that costs a pay day.
 *
 * ⛔ (1) A 403 MEANS TWO DIFFERENT THINGS AND THE SCREEN MUST SAY WHICH.
 * The route is under the back-dated cutoff THROUGH the wage month: cored derives
 * the voucher date with `payrolldb.LastDayOf` and judges it. So a refusal is
 * either "your level may not run payroll" or "that month is CLOSED" — and told
 * the first, an operator who already has the permission goes and asks for it
 * again, and never learns the period is locked. Worse here than on any voucher
 * screen: there is no date field on this page at all, so nothing on it hints
 * that a date is what was judged.
 *
 * ⚠️ IT IS DISTINGUISHED ON THE SERVER'S OWN SENTENCE and not on a code we
 * invent — every cutoff refusal in internal/authz carries "back-dated cutoff"
 * and no ACL denial does. These tests drive BOTH refusals, because a screen that
 * called everything a period lock would pass a test for the first alone.
 *
 * ⛔ (2) `already_posted` IS NEITHER A FAILURE NOR A FRESH POSTING. Read as a
 * failure the operator runs it again; read as a success they believe a
 * correction they just keyed took effect, when the stored run is the earlier one.
 *
 * ⛔ (3) A 422 IS SHOWN VERBATIM. Every payroll refusal names the person and what
 * is missing, and those sentences are the whole remedy.
 */

afterEach(cleanup);

const COMPANY = "company-demo";

function snap(): NavSnapshot {
  return {
    screen: "payroll-run",
    screenStackDepth: 2,
    canReturnToVoucher: false,
  } as unknown as NavSnapshot;
}

function renderScreen(client: CoredClient) {
  const s = snap();
  let fire: (kind: ScreenActionKind) => Promise<string | void> = async () => {};
  function Harness() {
    const dispatch = useScreenActionDispatcher();
    fire = async (kind) => {
      const res = dispatch(kind, s);
      if (res.pending) return await res.pending;
      return res.message;
    };
    return <PayRunScreen snapshot={s} onSelect={() => {}} />;
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

/** Type a month and press Ctrl+A. */
async function runMonth(
  h: { press: (k: ScreenActionKind) => Promise<string | undefined> },
  month: string,
) {
  fireEvent.change(await screen.findByTestId("pay-run-month"), {
    target: { value: month },
  });
  return h.press("accept");
}

function clientRefusing(error: unknown): CoredClient {
  const base = createMockClient({ seed: false });
  return {
    ...base,
    runPayroll: vi.fn(async () => {
      throw error;
    }),
  } as unknown as CoredClient;
}

describe("a 403 says WHICH of the two refusals it is", () => {
  it("a back-dated cutoff reads as a CLOSED PERIOD and names the derived date", async () => {
    const client = clientRefusing(
      new ApiError(
        403,
        "voucher date 2026-07-31 is before the back-dated cutoff of 2026-08-01",
      ),
    );
    const h = renderScreen(client);
    await runMonth(h, "2026-07");

    const panel = await screen.findByTestId("pay-run-period-locked");
    // The server's sentence, verbatim — it names the cutoff date, which is the
    // one fact the operator needs.
    expect(panel.textContent).toContain("back-dated cutoff of 2026-08-01");
    expect(panel.textContent).toMatch(/CLOSED PERIOD/);
    // ⛔ AND IT EXPLAINS WHICH DATE WAS JUDGED. This screen has no date field, so
    // without this sentence the operator has nothing to connect the refusal to.
    expect(panel.textContent).toMatch(/derives the voucher's date from the wage month/i);
    expect(screen.queryByTestId("pay-run-refusal")).toBeNull();
  });

  it("an ACL denial does NOT read as a closed period", async () => {
    // ⛔ THE CONTROL. Without it a screen that called every 403 a period lock
    // would pass the test above — and would send an operator who genuinely lacks
    // the permission hunting for a cutoff that is not there.
    const client = clientRefusing(
      new ApiError(403, "disallowed: create payroll"),
    );
    const h = renderScreen(client);
    await runMonth(h, "2026-07");

    const panel = await screen.findByTestId("pay-run-refusal");
    expect(panel.textContent).toMatch(/own permission/i);
    expect(panel.textContent).not.toMatch(/CLOSED PERIOD/);
    expect(screen.queryByTestId("pay-run-period-locked")).toBeNull();
  });
});

describe("already_posted is reported as neither a failure nor a fresh posting", () => {
  it("says the month was already posted and that nothing was written now", async () => {
    const base = createMockClient({ seed: false });
    const client = {
      ...base,
      runPayroll: vi.fn(
        async (): Promise<PayRunResponse> => ({
          pay_run_id: "run-1",
          voucher_id: "v-1",
          voucher_no: "PR-1",
          version_no: 1,
          status: "POSTED",
          already_posted: true,
        }),
      ),
    } as unknown as CoredClient;
    const h = renderScreen(client);
    const message = await runMonth(h, "2026-07");

    expect(message).toMatch(/ALREADY POSTED/);
    expect(message).toMatch(/Nothing was written now/);
    // ⛔ AND NO "posted" CONFIRMATION PANEL. That panel is the screen saying "your
    // run went in", which is exactly the false reading this flag exists to
    // prevent: the stored run is the EARLIER one, without anything keyed since.
    expect(screen.queryByTestId("pay-run-posted")).toBeNull();
  });

  it("a genuinely fresh run DOES show the posted panel", async () => {
    // The control for the assertion above.
    const base = createMockClient({ seed: false });
    const client = {
      ...base,
      runPayroll: vi.fn(
        async (): Promise<PayRunResponse> => ({
          pay_run_id: "run-2",
          voucher_id: "v-2",
          voucher_no: "PR-2",
          version_no: 1,
          status: "POSTED",
          already_posted: false,
        }),
      ),
    } as unknown as CoredClient;
    const h = renderScreen(client);
    await runMonth(h, "2026-07");
    const panel = await screen.findByTestId("pay-run-posted");
    expect(panel.textContent).toContain("PR-2");
  });
});

describe("the wage month is refused locally, with the reason", () => {
  it("refuses 2026-7 and 2026-13 WITHOUT making a request", async () => {
    // ⛔ THE STRICTNESS IS THE SERVER'S AND THE REASON IS REAL: every
    // social-security scheme keys off the wage month, and one silently moved to
    // its neighbour computes somebody's contributions under the wrong window.
    // Refusing here avoids a 400 in somebody's log for a shape the screen knows.
    const base = createMockClient({ seed: false });
    const runPayroll = vi.fn();
    const client = { ...base, runPayroll } as unknown as CoredClient;
    const h = renderScreen(client);

    for (const bad of ["2026-7", "2026-13", "July 2026", ""]) {
      const message = await runMonth(h, bad);
      expect(message, `"${bad}" was accepted`).toMatch(/wage month/i);
    }
    expect(runPayroll).not.toHaveBeenCalled();
  });
});

describe("a 422 reaches the operator unaltered", () => {
  it("shows the server's sentence, which names the person and the gap", async () => {
    // "Could not run payroll" would leave an operator with a blocked pay day and
    // nothing to act on. The server's own message IS the remedy.
    const client = clientRefusing(
      new ApiError(
        422,
        "payrolldb: compute payslip for employee E-0007: no statutory election is in force for wage month 2026-07",
      ),
    );
    const h = renderScreen(client);
    await runMonth(h, "2026-07");
    const panel = await screen.findByTestId("pay-run-refusal");
    expect(panel.textContent).toContain("E-0007");
    expect(panel.textContent).toContain("no statutory election is in force");
  });
});

describe("the screen does not claim to know who will be paid", () => {
  it("labels its list as who is on the payroll NOW, not who the run covers", async () => {
    // ⛔ ELIGIBILITY IS A FUNCTION OF THE MONTH — joined on or before it ended,
    // did not leave before it began — and the SERVER evaluates it. A list here
    // claiming to predict the run would be wrong for every joiner and every
    // leaver, which are the months anybody actually checks.
    const client = createMockClient({ seed: false });
    await client.createEmployee(COMPANY, { code: "E-0007", name: "Asha Rao" });
    renderScreen(client);
    expect(await screen.findByTestId("pay-run-employees")).toBeTruthy();
    expect(screen.getByText(/On the payroll now/)).toBeTruthy();
    expect(
      screen.getByText(/Who is actually paid is decided by the SERVER/),
    ).toBeTruthy();
  });

  it("says a company with no employees has nobody to pay", async () => {
    const client = createMockClient({ seed: false });
    renderScreen(client);
    const empty = await screen.findByTestId("pay-run-no-employees");
    expect(empty.textContent).toMatch(/nobody to pay/i);
  });
});
