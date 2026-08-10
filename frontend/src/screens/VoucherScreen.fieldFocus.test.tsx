import { describe, it, expect, afterEach, beforeEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
  act,
} from "@testing-library/react";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api";
import type { CoredClient } from "../api";
import { MASTER_GOTO_ENTRIES } from "../keystroke/navMachine";
import {
  _resetOutputTaxLedgerCache,
  _resetLedgerCache,
} from "./vouchers/gstVoucher";

afterEach(cleanup);
beforeEach(() => {
  _resetOutputTaxLedgerCache();
  _resetLedgerCache();
});

/**
 * ===========================================================================
 * TWO AFFORDANCES THAT LIED TO THE OPERATOR, AND NEITHER WAS A CRASH.
 * ===========================================================================
 *
 *  1. THE FIELD HIGHLIGHT DID NOT FOLLOW FOCUS. Tab is a browser primitive
 *     again (the global `preventDefault` that swallowed it on every screen was
 *     removed), so real focus now walks the whole form — but the highlight was
 *     painted from `navMachine`'s FIVE-STRING cursor, which saturates on
 *     `narration` and stops. The operator's ring was in the particulars grid
 *     while the screen said they were in the narration box. Nothing was
 *     unreachable; the decoration contradicted the truth, which teaches an
 *     operator to stop believing the screen.
 *
 *  2. THE EMPTY-ITEM-GRID SIGNPOST NAMED A MENU THAT DOES NOT EXIST. It read
 *     "create one (Alt+C from Gateway > Masters)". There is no
 *     `Gateway > Masters` in this product, and Alt+C on this screen creates a
 *     LEDGER inline — it has never reached a stock item. The route that exists
 *     is `Alt+G > Create Stock Item`.
 *
 * ⚠️ THE SIGNPOST TEST DRIVES THE ROUTE IT ADVERTISES rather than matching a
 * string. A test that only asserted the new words would pass just as happily on
 * the next dead signpost, which is the whole failure mode being closed here.
 */

function renderShell(client: CoredClient) {
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
}

/**
 * Move REAL focus, the way the browser's own Tab walk does.
 *
 * ⚠️ `el.focus()` — not a synthetic focus event. The highlight is derived from
 * what is actually focused, so a test that only dispatched an event would prove
 * the handler runs and not that it agrees with `document.activeElement`. It is
 * wrapped in `act` because the resulting React state update is not otherwise
 * flushed before the assertion reads the DOM.
 */
async function focus(el: HTMLElement): Promise<void> {
  await act(async () => {
    el.focus();
  });
  expect(document.activeElement).toBe(el);
}

/** Is this control painted with the active-field highlight? */
function lit(testId: string): boolean {
  return screen.getByTestId(testId).className.includes("bg-tally-active");
}

describe("⛔ the field highlight follows REAL focus, not the machine's cursor", () => {
  async function openPayment(): Promise<HTMLElement> {
    const base = createMockClient({ seed: false });
    await base.createLedger("co-1", {
      name: "HDFC Bank",
      group_name: "Bank Accounts",
    });
    renderShell(base);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F5" }); // Payment — a single-entry grid
    await screen.findByTestId("single-entry-panel");
    return app;
  }

  it("paints the machine's field while NOTHING in the form has focus", async () => {
    // The baseline the fix must not break: a fresh frame starts on the Date, and
    // nothing is focused yet, so the coarse cursor is still the best thing
    // available and it is what decorates.
    await openPayment();
    expect(lit("voucher-date")).toBe(true);
    expect(lit("voucher-narration")).toBe(false);
  });

  it("⛔ does NOT paint `narration` while focus is out in the particulars grid", async () => {
    // ⛔ THIS IS THE REPORTED SYMPTOM, REPRODUCED. Four Tabs walk the machine's
    // cursor to its LAST field and it stops there; the browser's focus walk does
    // not stop, because one particulars row alone carries a ledger box, an
    // amount box, a remove button and two pickers. The highlight used to
    // saturate on the narration box while the ring was in the grid.
    const app = await openPayment();
    for (let i = 0; i < 4; i++) fireEvent.keyDown(app, { key: "Tab" });
    // The machine really is saturated — the premise of the defect, asserted
    // rather than assumed. (Nothing has focus yet, so it is still decorating.)
    expect(lit("voucher-narration")).toBe(true);

    await focus(screen.getByTestId("single-amount:0"));

    // The ring is in the grid, so NOTHING in the header is painted. "" — focus
    // is inside the form but on none of the five named fields — is the honest
    // state; painting a field the operator has left is not.
    expect(lit("voucher-narration")).toBe(false);
    expect(lit("voucher-date")).toBe(false);
  });

  it("paints the field that is ACTUALLY focused, even when the machine disagrees", async () => {
    // The machine's cursor is on `date` (a fresh frame, no Tab pressed). Focus
    // goes to the narration box. The highlight follows the FOCUS.
    await openPayment();
    expect(lit("voucher-date")).toBe(true);

    await focus(screen.getByTestId("voucher-narration"));

    expect(lit("voucher-narration")).toBe(true);
    expect(lit("voucher-date")).toBe(false);
  });

  it("hands the highlight BACK to the machine when focus leaves the form", async () => {
    // ⚠️ THE CONTROL FOR THE WHOLE MECHANISM. Deriving from focus only works if
    // "no focus" is distinguishable from "focus on something unnamed" — if they
    // collapsed, a voucher screen nobody had clicked into would show no cursor
    // at all, which is worse than a coarse one.
    await openPayment();
    const narration = screen.getByTestId("voucher-narration");
    await focus(narration);
    expect(lit("voucher-narration")).toBe(true);

    await act(async () => narration.blur());

    expect(lit("voucher-date")).toBe(true);
    expect(lit("voucher-narration")).toBe(false);
  });
});

describe("⛔ the empty item grid signposts a route that EXISTS", () => {
  async function openEmptyItemInvoice(): Promise<HTMLElement> {
    const base = createMockClient({ seed: false });
    await base.createLedger("co-1", {
      name: "Acme Traders",
      group_name: "Sundry Debtors",
      state_code: "29",
    });
    renderShell(base);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.keyDown(app, { key: "h", ctrlKey: true });
    fireEvent.mouseDown(await screen.findByText("Item Invoice"));
    await screen.findByTestId("item-invoice-panel");
    return app;
  }

  it("names Alt+G > Create Stock Item, and that label is one the machine routes", async () => {
    await openEmptyItemInvoice();
    const note = await screen.findByTestId("stock-items-empty");
    expect(note.textContent).toMatch(/Alt\+G/);
    // ⛔ NOT A STRING MATCH ON THE LABEL — the label is looked UP in the
    // machine's own table, so renaming the Go To entry without fixing this note
    // fails here instead of shipping another dead signpost.
    const label = Object.keys(MASTER_GOTO_ENTRIES).find(
      (k) => MASTER_GOTO_ENTRIES[k] === "stock-item",
    )!;
    expect(note.textContent).toContain(label);
    // The menu it used to point at does not exist and never did.
    expect(note.textContent).not.toMatch(/Gateway/);
  });

  it("⛔ and the keystroke it names actually opens the stock-item form", async () => {
    // RULE 3, as a test: a signpost is only as good as the door it names. This
    // presses the door.
    const app = await openEmptyItemInvoice();
    await screen.findByTestId("stock-items-empty");

    // ⛔ THE PHYSICAL KEY. Option COMPOSES on macOS — a real Mac sends key:"©"
    // for Alt+G and never "g" — and `comboFromEvent` reads `code` for exactly
    // that reason. Pressing "g" here would pass while the product was dead on
    // every Mac, which is how every Alt binding in this build was once broken.
    fireEvent.keyDown(app, { key: "©", code: "KeyG", altKey: true });
    fireEvent.mouseDown(screen.getByText("Create Stock Item"));
    fireEvent.keyDown(app, { key: "Enter" });

    await waitFor(() =>
      expect(screen.getByTestId("master-create:stock-item")).toBeTruthy(),
    );
  });
});
