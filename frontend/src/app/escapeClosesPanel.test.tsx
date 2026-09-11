import { describe, it, expect, afterEach, beforeEach } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";

import { AppShell } from "./AppShell";
import { CompanyProvider } from "./CompanyContext";
import { createMockClient } from "../api/mock";

/**
 * ⛔⛔ D114 — Esc IN AN IN-SCREEN PANEL MUST CLOSE THE PANEL, NOT THE SCREEN.
 *
 * The statement-import panel's own footer says "Esc goes back to the
 * reconciliation statement" and the matching panel's says "Esc closes." Both
 * were false: each panel is rendered by the Bank Reconciliation screen
 * RETURNING it instead of its own body, out of React state the navigation
 * machine cannot see — so Esc did the only thing the machine could do with one
 * frame on the stack and popped it. Driven 2026-08-31, three times, one
 * keypress each: `Bank Reconciliation — HDFC Bank` → Alt+O → `Import Bank
 * Statement` → ONE Esc → `Gateway of Tally`.
 *
 * ⚠️ IT COSTS MORE THAN A KEYSTROKE, which is why it is not cosmetic: Bank
 * Reconciliation is NOT in the Alt+G Go To list, so an operator who has just
 * imported a statement and wants to tick against it has to know to find the
 * Gateway entry again.
 *
 * ⛔⛔ AND THIS SUITE IS ON THE REAL SHELL FOR THE REASON exportKey.test.tsx
 * gives: `SCREEN_ACTIONS` in AppShell.tsx is a registration point that ships a
 * key COMPLETELY DEAD, invisibly to every unit test, because unit tests call
 * the dispatcher directly and bypass the map. Delete `escape: "closePanel"`
 * from it and the first test below fails; nothing else in the repository does.
 */

const COMPANY = "company-demo";

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

/** Pick an entry off the Gateway menu, the way an operator does. */
function openFromGateway(app: HTMLElement, name: string) {
  fireEvent.mouseDown(screen.getByText(name));
  fireEvent.keyDown(app, { key: "Enter" });
}

const onGateway = () => screen.queryByText("Gateway of Tally") !== null;

beforeEach(() => {
  // The reconciliation screen auto-selects when the company has exactly one
  // bank account; the mock's demo book does. If that ever changes the picker
  // appears and the waitFor below fails LOUDLY rather than silently testing
  // the picker's Esc instead of the panel's.
});

afterEach(cleanup);

describe("D114 — Esc closes the open panel and leaves the screen standing", () => {
  it("⛔ Alt+O then Esc returns to the statement, NOT to the Gateway", async () => {
    const app = renderShell();
    openFromGateway(app, "Bank Reconciliation");
    await screen.findByTestId("brs-as-on");

    fireEvent.keyDown(app, { key: "o", altKey: true });
    await screen.findByTestId("stmt-import-panel");

    fireEvent.keyDown(app, { key: "Escape", code: "Escape" });

    // The panel is gone…
    await waitFor(() =>
      expect(screen.queryByTestId("stmt-import-panel")).toBeNull(),
    );
    // …the statement is back — this is the line that was false…
    expect(screen.getByTestId("brs-as-on")).toBeTruthy();
    // …and the frame survived, which is the half a status message cannot fake.
    expect(onGateway()).toBe(false);
  });

  it("⛔ Alt+S then Esc does the same for the MATCHING panel", async () => {
    const app = renderShell();
    openFromGateway(app, "Bank Reconciliation");
    await screen.findByTestId("brs-as-on");

    fireEvent.keyDown(app, { key: "s", altKey: true });
    await screen.findByTestId("bank-match-panel");

    fireEvent.keyDown(app, { key: "Escape", code: "Escape" });

    await waitFor(() =>
      expect(screen.queryByTestId("bank-match-panel")).toBeNull(),
    );
    expect(screen.getByTestId("brs-as-on")).toBeTruthy();
    expect(onGateway()).toBe(false);
  });

  it("⭐⭐ CONTROL: with NO panel open, Esc still leaves the screen", async () => {
    // The arm that keeps Esc being Esc. A handler that consumed the key
    // unconditionally would pass both tests above and strand an operator on a
    // screen they can no longer leave — which is a worse defect than the one
    // being fixed.
    const app = renderShell();
    openFromGateway(app, "Bank Reconciliation");
    await screen.findByTestId("brs-as-on");

    fireEvent.keyDown(app, { key: "Escape", code: "Escape" });
    await waitFor(() => expect(onGateway()).toBe(true));
  });

  it("⭐ CONTROL: two Escapes from a panel reach the Gateway", async () => {
    // The panel first, THEN the frame. This is what proves the fix pops one
    // level rather than swallowing the key: an operator's second Esc must still
    // leave, and must not need a third.
    const app = renderShell();
    openFromGateway(app, "Bank Reconciliation");
    await screen.findByTestId("brs-as-on");

    fireEvent.keyDown(app, { key: "o", altKey: true });
    await screen.findByTestId("stmt-import-panel");

    fireEvent.keyDown(app, { key: "Escape", code: "Escape" });
    await waitFor(() =>
      expect(screen.queryByTestId("stmt-import-panel")).toBeNull(),
    );
    fireEvent.keyDown(app, { key: "Escape", code: "Escape" });
    await waitFor(() => expect(onGateway()).toBe(true));
  });

  it("⭐ CONTROL: an OVERLAY still wins over the panel", async () => {
    // The machine cancels an open overlay before it pops a frame, and the panel
    // must not jump that queue: a panel closing underneath an open dialog leaves
    // the operator holding a dialog for a screen they can no longer see.
    const app = renderShell();
    openFromGateway(app, "Bank Reconciliation");
    await screen.findByTestId("brs-as-on");

    fireEvent.keyDown(app, { key: "o", altKey: true });
    await screen.findByTestId("stmt-import-panel");

    // F2 — the period overlay, over the panel.
    fireEvent.keyDown(app, { key: "F2" });
    await screen.findByTestId("period-from");

    fireEvent.keyDown(app, { key: "Escape", code: "Escape" });

    // The overlay closed; THE PANEL IS STILL OPEN.
    await waitFor(() =>
      expect(screen.queryByTestId("period-from")).toBeNull(),
    );
    expect(screen.getByTestId("stmt-import-panel")).toBeTruthy();
    expect(onGateway()).toBe(false);
  });
});
