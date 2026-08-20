import { describe, it, expect, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { AppShell } from "./AppShell";
import { CompanyProvider } from "./CompanyContext";
import { createDefaultRegistry } from "./defaultScreens";
import { createMockClient } from "../api";
import type { ScreenComponentProps } from "./screenRegistry";

afterEach(cleanup);

function renderShell(registry = createDefaultRegistry()) {
  return render(
    <CompanyProvider
      client={createMockClient()}
      initialCompany={{ id: "company-demo", name: "Demo Traders" }}
    >
      <AppShell registry={registry} />
    </CompanyProvider>,
  );
}

/**
 * ⛔ D38's SECOND HALF, AND IT HAD NO TEST — FOUND BY AN EXECUTED MUTATION PROOF.
 *
 * D38 was two defects wearing one number. `useNavMachine` used to
 * preventDefault Tab (fixed, and pinned by three tests in
 * useNavMachine.test.tsx), and the SHELL used to report the key it had just let
 * through as inapplicable. Removing `"nextField"`/`"prevField"` from
 * ALWAYS_MEANINGFUL left every suite in this repository GREEN — 36 app tests,
 * 195 keystroke tests, the TDS/buttons/export suites that mention the phrase —
 * while the operator was told "Tab — Next field does not apply on this screen"
 * as the focus ring visibly moved.
 *
 * `nextField` returns `{}` off a voucher, so `effect.ran` is false and
 * `machineActs` is false, and the else-branch fires. ALWAYS_MEANINGFUL is what
 * makes `machineActs` true. That is the whole mechanism, and this is the test
 * that would have caught its removal.
 */
describe("⛔ the status line may not deny a key the browser just honoured (D38)", () => {
  const statusOf = () => screen.getByTestId("status-line").textContent ?? "";

  it("says nothing about Tab NOT APPLYING on the Gateway", () => {
    renderShell();
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "Tab" });
    expect(statusOf()).not.toMatch(/does not apply/i);
  });

  it("nor about Shift+Tab", () => {
    renderShell();
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "Tab", shiftKey: true });
    expect(statusOf()).not.toMatch(/does not apply/i);
  });

  it("nor on a REPORT, where nextField genuinely returns {}", () => {
    renderShell();
    const app = screen.getByRole("application");
    // Alt+G > Trial Balance: a report frame, not a voucher.
    fireEvent.keyDown(app, { key: "F1", altKey: true });
    fireEvent.keyDown(app, { key: "Tab" });
    expect(statusOf()).not.toMatch(/does not apply/i);
  });

  it("⛔ CONTROL: a key that really does nothing here IS still reported", () => {
    renderShell();
    const app = screen.getByRole("application");
    // Alt+D Delete on the Gateway — no voucher to delete, and the shell must
    // still say so. Without this control the assertions above would pass just as
    // well over a status line that had been silenced altogether.
    fireEvent.keyDown(app, { key: "d", altKey: true });
    expect(statusOf()).toMatch(/does not apply/i);
  });
});

describe("AppShell routing + chrome", () => {
  it("renders the company in the top bar and the gateway body", () => {
    renderShell();
    expect(screen.getByText("Demo Traders")).toBeTruthy();
    expect(screen.getByText("Gateway of Tally")).toBeTruthy();
  });

  it("routes F-keys to the voucher screen and Alt+G to the Go To overlay", () => {
    renderShell();
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F9" });
    expect(screen.getByText(/Purchase Voucher/)).toBeTruthy();
    fireEvent.keyDown(app, { key: "g", altKey: true });
    expect(screen.getByRole("navigation", { name: "Button bar" })).toBeTruthy();
  });

  it("renders the bottom shortcut hints", () => {
    renderShell();
    expect(screen.getByLabelText("Shortcut hints")).toBeTruthy();
  });

  it("lets an agent register a custom report screen by name", () => {
    const CustomDayBook = ({ snapshot }: ScreenComponentProps) => (
      <div>CUSTOM {snapshot.reportName}</div>
    );
    const registry = createDefaultRegistry().register(
      "report:Day Book",
      CustomDayBook,
    );
    renderShell(registry);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "g", altKey: true });
    // The Go To overlay is open; the SelectionList is autofocused. Pick Day Book.
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    expect(screen.getByText("CUSTOM Day Book")).toBeTruthy();
  });
});

/**
 * ⚠️ THE THIRTEENTH REGISTRATION POINT — `SCREEN_ACTIONS` in AppShell.
 *
 * `HANDOFF-FEATURE-LOCK.md` §5.2 lists twelve places a new screen or resource
 * must be registered. It does not list this map, and Alt+O shipped broken
 * because of it: the keymap binding, the ScreenActionKind, the navMachine case,
 * the button-bar item and the screen's own useRegisterScreenAction were ALL
 * present, and the key still did nothing.
 *
 * WHY EVERY EXISTING TEST MISSED IT, which is the part worth keeping:
 *
 *   - the panel's own suite calls the screen-action dispatcher DIRECTLY, so it
 *     bypasses this map entirely;
 *   - buttons.test.ts asserts the combo RESOLVES to an action and that the
 *     action is not an `unavailable` placeholder — both true here;
 *   - nothing rendered the real shell and pressed the real key.
 *
 * It was found by driving the app in a browser. These tests are the cheap
 * version of that, and they go through `AppShell` on purpose: a screen action
 * that is not routed by the shell is invisible to every other kind of test.
 */
describe("a screen-owned key is actually ROUTED by the shell", () => {
  /**
   * Walk from the Gateway to the Bank Reconciliation screen — via the GATEWAY's
   * own list, which is how a user reaches it (the Alt+G Go To list does not
   * carry it). The mock's seeded company has exactly one bank ledger, in a
   * SUB-group of "Bank Accounts", so the screen resolves it and opens directly
   * instead of showing the account picker.
   */
  async function openBankReconciliation() {
    renderShell();
    const app = screen.getByRole("application");
    fireEvent.mouseDown(screen.getByText("Bank Reconciliation"));
    fireEvent.keyDown(app, { key: "Enter" });
    // AWAIT THE ACCOUNT LOAD. The ledger list is fetched, so for a moment the
    // screen has no account selected — and Alt+O then correctly answers "select
    // a bank account first" instead of opening a panel with nothing to import
    // into. Pressing the key before this point tests the refusal, not the route.
    await screen.findByText(/Bank Reconciliation — HDFC Bank/);
    return app;
  }

  it("Alt+O opens the statement-import panel on the reconciliation screen", async () => {
    const app = await openBankReconciliation();
    // THE CONTROL: we are on the reconciliation screen and the panel is NOT up.
    // Without this the assertion below could pass on a screen that happened to
    // render the panel from the start.
    expect(screen.queryByTestId("stmt-import-panel")).toBeNull();
    expect(screen.getByRole("button", { name: /Alt\+O Import/ })).toBeTruthy();

    fireEvent.keyDown(app, { key: "o", altKey: true });

    // If SCREEN_ACTIONS is missing its `importBankStatement` entry, this is the
    // line that fails — and it is the ONLY kind of test that does.
    expect(screen.getByTestId("stmt-import-panel")).toBeTruthy();
  });

  it("the Alt+O BUTTON does the same thing as the key", async () => {
    // buttonBar clicks dispatch the same combo the keyboard sends, so a bar
    // button and its key must never diverge. This is the cheap check that they
    // go through the same routing.
    await openBankReconciliation();
    fireEvent.click(screen.getByRole("button", { name: /Alt\+O Import/ }));
    expect(screen.getByTestId("stmt-import-panel")).toBeTruthy();
  });
});
