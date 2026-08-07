import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";

import { TDSRegisterScreen } from "./TDSRegisterScreen";
import { CompanyProvider } from "../../app/CompanyContext";
import { ScreenActionsProvider } from "../../app/ScreenActions";
import { createMockClient, ApiError } from "../../api";
import type { CoredClient } from "../../api";
import registerProse from "../../api/goldens/tds-register-prose.json";
import type { NavSnapshot } from "../../keystroke/navMachine";

/** Escapes a golden string so it can be matched as a literal. */
function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

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
  // ScreenActionsProvider is required now that the register registers Alt+E
  // (T15). Every other report-screen test wraps in it for the same reason — this
  // screen was the last one whose keys were all dead, which is exactly why it
  // did not need the provider before.
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "company-demo", name: "Demo Traders" }}
    >
      <ScreenActionsProvider>
        <TDSRegisterScreen snapshot={snapshot} />
      </ScreenActionsProvider>
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
    // ⛔ ASSERTED AGAINST THE GENERATED GOLDEN, NOT AGAINST A STRING TYPED HERE.
    //
    // This test used to match /DEPOSIT IS NOT TRACKED/, and it FAILED the moment
    // the mock started serving the server's real prose — because deposit IS
    // tracked now (migration 0069) and the server had stopped saying that. That
    // failure is the feature: the old arrangement would have kept this test green
    // against a sentence the product no longer produced.
    //
    // So the expected text comes from the same golden the mock serves. A wording
    // change on the Go side now moves the assertion and the fixture together,
    // and the only way to break this test is to stop RENDERING the disclosures —
    // which is precisely what it exists to catch.
    for (const line of registerProse.disclosures) {
      // Matched on a distinctive head of each line rather than the whole
      // paragraph, because the DOM splits long text across nodes.
      //
      // ⚠️ `?? line` AND NOT `?? ""`. Under noUncheckedIndexedAccess the [0] is
      // `string | undefined`, and the tempting fallback is the empty string —
      // which compiles, and turns the pattern into one that matches ANYTHING, so
      // the disclosure assertion would silently stop asserting. Falling back to
      // the whole line keeps a real pattern in every branch.
      const head = line.split(".")[0] ?? line;
      expect(await screen.findByText(new RegExp(escapeRegExp(head)))).toBeTruthy();
    }
    // And the count, so a disclosure silently disappearing is not a pass.
    expect(registerProse.disclosures.length).toBeGreaterThanOrEqual(2);
  });

  it("says a return cannot be filed, and NAMES what is missing", async () => {
    renderRegister(snap("2026-04-01", "2026-06-30"));
    // An absent "file return" button would leave an operator hunting for a
    // feature that is deliberately not there. The screen says so instead, and
    // lists the blockers.
    expect(
      await screen.findByText(/Filing the return — not available/),
    ).toBeTruthy();
    // Matched on the blocker's own WHY rather than on a phrase that also
    // appears in the disclosures above it — a substring shared by two elements
    // asserts neither of them.
    //
    // ⚠️ THIS ASSERTION CHANGED ON 2026-08-04. It used to match "fixes the
    // record order and the field widths", the WHY of a blocker that no longer
    // exists: the prescribed file format has been read and implemented, and the
    // remaining blockers are a missing challan register and a missing TAN.
    expect(
      screen.getByText(
        /no deductee row can exist without one|points at the challan/,
      ),
    ).toBeTruthy();
    // And it never lets "generated" read as "validated".
    expect(
      screen.getByText(
        /has been validated by the department's File Validation Utility/,
      ),
    ).toBeTruthy();
    // The citation is on the artefact's own face, so the panel cannot be read
    // as "nobody has looked at the format yet".
    expect(screen.getByText(/form-140-file-format\.xlsx/)).toBeTruthy();
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
