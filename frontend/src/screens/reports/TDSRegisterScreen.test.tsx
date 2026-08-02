import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";

import { TDSRegisterScreen } from "./TDSRegisterScreen";
import { CompanyProvider } from "../../app/CompanyContext";
import { createMockClient, ApiError } from "../../api";
import type { CoredClient } from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";

/**
 * THE TDS/TCS REGISTER SCREEN — Item Zero step 4, layer 5.
 *
 * The mock client derives no tax, so it returns an EMPTY register. That is the
 * right shape for these tests anyway: what only exists in the client is how it
 * behaves around the data — the refusal, the disclosures, the missing period —
 * and the numbers themselves are covered server-side by
 * TestTDSRegisterShowsBothSides.
 */

afterEach(cleanup);

function snap(from?: string, to?: string): NavSnapshot {
  return {
    screen: "report",
    reportName: "TDS / TCS Register",
    config: { periodFrom: from, periodTo: to },
  } as unknown as NavSnapshot;
}

function renderRegister(
  snapshot: NavSnapshot,
  client: CoredClient = createMockClient(),
) {
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "company-demo", name: "Demo Traders" }}
    >
      <TDSRegisterScreen snapshot={snapshot} />
    </CompanyProvider>,
  );
}

describe("TDS / TCS Register screen", () => {
  it("asks for a period rather than defaulting one", () => {
    renderRegister(snap());
    // A defaulted quarter would answer a question the operator did not ask
    // while looking exactly like the one they did — the server refuses too.
    expect(screen.getByText(/Press F2 to set the period/)).toBeTruthy();
  });

  it("states an empty period instead of inventing rows", async () => {
    renderRegister(snap("2026-04-01", "2026-06-30"));
    expect(
      await screen.findByText("Nothing deducted in this period."),
    ).toBeTruthy();
    expect(screen.getByText("Nothing collected in this period.")).toBeTruthy();
  });

  it("ALWAYS renders the disclosures — they are data, not decoration", async () => {
    renderRegister(snap("2026-04-01", "2026-06-30"));
    // The one that matters: none of these figures is tax PAID, and it is the
    // deposit that stops interest running under s.396.
    expect(await screen.findByText(/DEPOSIT IS NOT TRACKED/)).toBeTruthy();
  });

  it("says a return cannot be filed, and NAMES what is missing", async () => {
    renderRegister(snap("2026-04-01", "2026-06-30"));
    // An absent "file return" button would leave an operator hunting for a
    // feature that is deliberately not there. The screen says so instead, and
    // lists the blockers.
    expect(
      await screen.findByText(/Filing the return — not available/),
    ).toBeTruthy();
    // Matched on the blocker's own WHY rather than on "Form 140 layout", which
    // also appears in the disclosures above it — a substring shared by two
    // elements asserts neither of them.
    expect(
      screen.getByText(/fixes the record order and the field widths/),
    ).toBeTruthy();
    // And it never lets "generated" read as "validated".
    expect(
      screen.getByText(
        /has been validated by the department's File Validation Utility/,
      ),
    ).toBeTruthy();
  });

  it("renders a 403 as the DECISION it is, not as a blank screen", async () => {
    const denied = {
      ...createMockClient(),
      tdsRegister: vi.fn().mockRejectedValue(new ApiError(403, "forbidden")),
    } as unknown as CoredClient;
    renderRegister(snap("2026-04-01", "2026-06-30"), denied);

    // Migration 0068 refuses this register to Data Entry on purpose. A blank
    // table would read as "you have no deductions", which is a false statement
    // about the company's tax position.
    await waitFor(() =>
      expect(
        screen.getByText(/do not have permission to read this register/),
      ).toBeTruthy(),
    );
    expect(screen.getByText(/allow display report:tds-register/)).toBeTruthy();
  });
});
