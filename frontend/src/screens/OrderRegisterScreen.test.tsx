import { describe, it, expect, afterEach } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor,
} from "@testing-library/react";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api/mock";
import { ApiError } from "../api";
import type { CoredClient } from "../api";

/**
 * THE ORDER REGISTERS (T3) — and specifically, whether an OPERATOR can reach them.
 *
 * ⛔⛔ WHY EVERY TEST IN THIS FILE DRIVES THE REAL SHELL AND PRESSES THE REAL KEY.
 *
 * T3's entire backend — the order tables, the fulfilment links, the deferred
 * over-fulfilment trigger, both order books, the ACL migration and all fourteen
 * client methods — shipped, was reviewed, and was covered by unit AND live-Postgres
 * tests while the F11 Features row said `off`, because there was no screen. A
 * keyboard-only operator could not create an order at all. That is the third
 * occurrence of one shape in this repository's history, so this file is written to
 * assert the thing the other two occurrences were missing:
 *
 *   A FEATURE IS NOT SHIPPED WHEN ITS TESTS PASS. IT IS SHIPPED WHEN A HUMAN CAN
 *   REACH IT.
 *
 * So there are no tests here that call the screen-action dispatcher directly. Every
 * one renders `<AppShell/>`, presses the combo an operator would press, and asserts
 * what appears. That is the only kind of test that exercises AppShell's
 * `SCREEN_ACTIONS` map — the registration point that shipped Alt+O completely dead
 * with its keymap binding, its ScreenActionKind, its navMachine case, its bar item
 * and its screen handler ALL present and correct. Delete
 * `createOrder: "createOrder"` from that map and the Alt+C tests below fail;
 * nothing else in the repo does.
 *
 * ⚠️ HOW REFUSAL TEXT IS ASSERTED. No server sentence is retyped here — a hand copy
 * is what went stale for two days on the TDS register. Each test CAPTURES the
 * `ApiError` the mock client throws for the same request and looks for THAT message
 * in the DOM. A wording change moves both together, and the only way to fail is to
 * stop rendering the server's sentence.
 */

const COMPANY = "company-demo";

afterEach(cleanup);

function renderShell(client: CoredClient = createMockClient()) {
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
  return screen.getByRole("application");
}

/** Press Ctrl+F8 / Ctrl+F9 and wait for that register to be up. */
async function openRegister(
  app: HTMLElement,
  direction: "sales" | "purchase",
): Promise<void> {
  fireEvent.keyDown(app, {
    key: direction === "sales" ? "F8" : "F9",
    ctrlKey: true,
  });
  await screen.findByTestId(`${direction}-orders-empty`);
}

/** Fill the create form for one single-line order. */
function keyAnOrder(opts: {
  no: string;
  date?: string;
  party?: string;
  item?: string;
  qty?: string;
  rate?: string;
  due?: string;
}) {
  fireEvent.change(screen.getByLabelText("Order number"), {
    target: { value: opts.no },
  });
  fireEvent.change(screen.getByLabelText("Order date"), {
    target: { value: opts.date ?? "2026-06-01" },
  });
  // ⛔ PICKED BY LABEL, NEVER BY ID. The <option> values ARE uuids, but nothing a
  // test types here is one — which is the same guarantee the operator has.
  const party = screen.getByLabelText(/Customer|Supplier/) as HTMLSelectElement;
  const partyOption = [...party.options].find(
    (o) => o.textContent === (opts.party ?? "Acme Traders"),
  );
  fireEvent.change(party, { target: { value: partyOption?.value } });

  const item = screen.getByLabelText("Item on line 1") as HTMLSelectElement;
  const itemOption = [...item.options].find(
    (o) => o.textContent === (opts.item ?? "Widget"),
  );
  fireEvent.change(item, { target: { value: itemOption?.value } });

  fireEvent.change(screen.getByLabelText("Quantity on line 1"), {
    target: { value: opts.qty ?? "20" },
  });
  if (opts.rate !== undefined) {
    fireEvent.change(screen.getByLabelText("Rate on line 1"), {
      target: { value: opts.rate },
    });
  }
  if (opts.due !== undefined) {
    fireEvent.change(screen.getByLabelText("Due date on line 1"), {
      target: { value: opts.due },
    });
  }
}

describe("Ctrl+F8 and Ctrl+F9 reach TWO SEPARATE registers", () => {
  it("Ctrl+F8 opens the SALES register and Ctrl+F9 the PURCHASE one", async () => {
    // ⛔ THE ASSERTION THAT KEEPS THE AUTHORITY SPLIT REAL. `sales-order` and
    // `purchase-order` are separate tokens on separate routes and the PDP reads the
    // PATH, so one screen serving both directions would be reaching a resource the
    // operator may be denied — a 403 nobody can attribute. If the two were ever
    // collapsed into one screen with a toggle, one of these two halves could not
    // hold.
    const app = renderShell();
    await openRegister(app, "sales");
    expect(screen.getByText("Sales Orders")).toBeTruthy();
    // THE CONTROL: the purchase register is NOT what we are looking at.
    expect(screen.queryByTestId("purchase-orders-empty")).toBeNull();

    fireEvent.keyDown(app, { key: "F9", ctrlKey: true });
    await screen.findByTestId("purchase-orders-empty");
    expect(screen.getByText("Purchase Orders")).toBeTruthy();
  });

  it("each register names its own party noun, not a generic one", async () => {
    // The two screens look nearly identical, and a purchase commitment read as a
    // customer's promise is the whole reason the direction is on the face of it.
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    expect(screen.getByLabelText("Customer")).toBeTruthy();
    expect(screen.queryByLabelText("Supplier")).toBeNull();

    cleanup();
    const app2 = renderShell();
    await openRegister(app2, "purchase");
    fireEvent.keyDown(app2, { key: "c", altKey: true });
    expect(screen.getByLabelText("Supplier")).toBeTruthy();
    expect(screen.queryByLabelText("Customer")).toBeNull();
  });

  it("pressing Ctrl+F8 twice does not stack a second identical frame", async () => {
    // pushOnce. Pressing the key you are already on is honest ("you are here") and
    // must not grow the stack — otherwise Esc would need pressing twice to leave a
    // screen entered once.
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "F8", ctrlKey: true });
    await screen.findByTestId("sales-orders-empty");
    fireEvent.keyDown(app, { key: "Escape" });
    // ONE Esc is enough to be back at the Gateway.
    expect(await screen.findByText("Gateway of Tally")).toBeTruthy();
  });
});

describe("Alt+C on a register — the key is ROUTED by the shell", () => {
  it("Alt+C opens the order form, which is ABSENT until it is pressed", async () => {
    const app = renderShell();
    await openRegister(app, "sales");

    // THE CONTROL: we are on the register and the form is NOT up. Without this the
    // test would pass on a screen that renders the form from the start.
    expect(screen.queryByTestId("sales-orders-panel")).toBeNull();
    expect(screen.getByRole("button", { name: /Alt\+C New Order/ })).toBeTruthy();

    fireEvent.keyDown(app, { key: "c", altKey: true });

    // If SCREEN_ACTIONS is missing its `createOrder` entry, THIS is the line that
    // fails — and it is the only kind of test that does. Note what else would still
    // be green: Alt+C resolves, the action has a label, the button is on the bar,
    // and the screen's handler is registered.
    expect(screen.getByTestId("sales-orders-panel")).toBeTruthy();
  });

  it("the Alt+C BUTTON does the same thing as the key", async () => {
    const app = renderShell();
    await openRegister(app, "sales");
    expect(screen.queryByTestId("sales-orders-panel")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Alt\+C New Order/ }));
    expect(screen.getByTestId("sales-orders-panel")).toBeTruthy();
  });

  it("the form NEVER asks for an id, and NEVER asks for a line number", async () => {
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });

    // The party and the item are PICKED from served lists — a `party_ledger_id` or
    // a `stock_item_id` is a UUID no screen in this product displays, so a box for
    // one could not be filled by a human. This is the defect the TDS deposit form
    // shipped with and had to be rebuilt for.
    expect(
      (screen.getByLabelText("Customer") as HTMLSelectElement).tagName,
    ).toBe("SELECT");
    expect(
      (screen.getByLabelText("Item on line 1") as HTMLSelectElement).tagName,
    ).toBe("SELECT");
    // And the seeded company's real masters are in them.
    expect(screen.getByRole("option", { name: "Acme Traders" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "Widget" })).toBeTruthy();

    // ⛔ NO LINE-NUMBER FIELD. The server assigns `line_no` from the array's
    // position; a client-supplied ordinal is a second source of truth that parts
    // company the moment a middle line is deleted.
    expect(screen.queryByLabelText(/line number/i)).toBeNull();
    expect(screen.queryByLabelText(/line_no/i)).toBeNull();
  });

  it("F9 does NOT open a Purchase voucher over a half-keyed order", async () => {
    // ⛔ AN UNBOUND KEY IS NOT INERT. Without the register's own binding the GLOBAL
    // F9 pushes a Purchase VOUCHER on top of the form and the frame holding the
    // operator's lines goes with it — not a missing feature but a destructive one.
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyAnOrder({ no: "SO-KEEP" });

    fireEvent.keyDown(app, { key: "F9" });

    expect(screen.queryByText(/Purchase Voucher/)).toBeNull();
    // Still on the form, with what was keyed still there.
    expect(screen.getByTestId("sales-orders-panel")).toBeTruthy();
    expect((screen.getByLabelText("Order number") as HTMLInputElement).value).toBe(
      "SO-KEEP",
    );
  });
});

describe("recording an order through the shell", () => {
  it("Ctrl+A saves it and the register reads it back with SERVER line numbers", async () => {
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyAnOrder({ no: "SO-1", qty: "20", rate: "500", due: "2026-06-30" });

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // The order is in the register…
    expect(await screen.findByTestId("sales-orders-row:SO-1")).toBeTruthy();
    // …and its lines came back, carrying the line_no the SERVER assigned. That
    // number is the one an operator quotes when they ask which line is short, and
    // this is the only place it is displayed.
    expect(
      await screen.findByTestId("sales-orders-line-row:SO-1:1"),
    ).toBeTruthy();
    // Nothing moved: the notice says so, because an order is a commitment.
    expect(screen.getByTestId("sales-orders-notice").textContent).toMatch(
      /Nothing has moved/,
    );
  });

  it("a fresh order is outstanding IN FULL — delivered is derived, and it is zero", async () => {
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyAnOrder({ no: "SO-2", qty: "20" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const row = await screen.findByTestId("sales-orders-line-row:SO-2:1");
    const cells = [...row.querySelectorAll("td")].map((c) => c.textContent);
    // ordered / delivered / outstanding, as fixed-4 strings. Both derived figures
    // are the server's; nothing here subtracts one from the other.
    expect(cells).toContain("20.0000");
    expect(cells).toContain("0.0000");
  });

  it("an omitted due date reads as 'none agreed' and NEVER as a date", async () => {
    // ⛔ THE TRAP THIS ASSERTS AGAINST: an omitted due date means no delivery date
    // was AGREED, which is not overdue however old the order. A screen that
    // defaulted it to today, or sorted it as if it were a date, would invent an
    // overdue commitment out of an ordinary open one.
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyAnOrder({ no: "SO-NODATE", qty: "5" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const row = await screen.findByTestId("sales-orders-line-row:SO-NODATE:1");
    expect(row.textContent).toMatch(/none agreed/);
  });

  it("renders the SERVER'S sentence on a duplicate order number", async () => {
    const client = createMockClient();
    // Capture the real refusal for the SAME request rather than retyping it.
    const probe = createMockClient();
    await probe.createSalesOrder(COMPANY, {
      order_no: "SO-DUP",
      order_date: "2026-06-01",
      party_ledger_id: "ledger-acme",
      created_by: "ui",
      lines: [{ stock_item_id: "stock-widget", qty: "1" }],
    });
    let expected = "";
    try {
      await probe.createSalesOrder(COMPANY, {
        order_no: "SO-DUP",
        order_date: "2026-06-01",
        party_ledger_id: "ledger-acme",
        created_by: "ui",
        lines: [{ stock_item_id: "stock-widget", qty: "1" }],
      });
    } catch (e) {
      expect(e).toBeInstanceOf(ApiError);
      expect((e as ApiError).status).toBe(409);
      expected = (e as Error).message;
    }
    expect(expected.length).toBeGreaterThan(0);

    const app = renderShell(client);
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyAnOrder({ no: "SO-DUP" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByTestId("sales-orders-row:SO-DUP");

    // Key the same number again.
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyAnOrder({ no: "SO-DUP" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const refusal = await screen.findByTestId("sales-orders-refusal");
    expect(refusal.textContent).toContain(expected);
  });

  it("refuses to send an order with no line, and says nothing was sent", async () => {
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    // Party chosen, no item picked.
    const party = screen.getByLabelText("Customer") as HTMLSelectElement;
    const opt = [...party.options].find((o) => o.textContent === "Acme Traders");
    fireEvent.change(party, { target: { value: opt?.value } });

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // The form is still open with the operator's work in it, and the status line
    // said why. Nothing reached the server: the register is still empty.
    expect(screen.getByTestId("sales-orders-panel")).toBeTruthy();
    await waitFor(() =>
      expect(screen.getByTestId("status-line").textContent).toMatch(
        /commits nothing at all|nothing was sent/i,
      ),
    );
  });
});

describe("Alt+W pre-closes — which ABANDONS an order, and is not 'delivered'", () => {
  async function withOneOrder(): Promise<HTMLElement> {
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyAnOrder({ no: "SO-W", qty: "20" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByTestId("sales-orders-row:SO-W");
    return app;
  }

  it("Alt+W opens the panel on the HIGHLIGHTED order, and is routed by the shell", async () => {
    const app = await withOneOrder();
    // THE CONTROL: the panel is not up.
    expect(screen.queryByTestId("sales-orders-close-panel")).toBeNull();
    expect(screen.getByRole("button", { name: /Alt\+W Pre-close/ })).toBeTruthy();

    fireEvent.keyDown(app, { key: "w", altKey: true });

    // Fails if SCREEN_ACTIONS has no `closeOrder` entry.
    expect(screen.getByTestId("sales-orders-close-panel")).toBeTruthy();
  });

  it("Alt+W with NOTHING highlighted opens no panel and says so", async () => {
    // The panel's render condition and the key's answer must agree. A key that
    // claims it worked while nothing appears is the Alt+O signature.
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "w", altKey: true });
    expect(screen.queryByTestId("sales-orders-close-panel")).toBeNull();
    await waitFor(() =>
      expect(screen.getByTestId("status-line").textContent).toMatch(
        /Highlight the order first/,
      ),
    );
  });

  it("REFUSES to close with no reason — an abandoned commitment must say why", async () => {
    const app = await withOneOrder();
    fireEvent.keyDown(app, { key: "w", altKey: true });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // Still open, nothing sent, and the order is still open in the list.
    expect(screen.getByTestId("sales-orders-close-panel")).toBeTruthy();
    await waitFor(() =>
      expect(screen.getByTestId("status-line").textContent).toMatch(
        /cannot be told apart from a mistake/,
      ),
    );
    expect(screen.getByTestId("sales-orders-row:SO-W").textContent).toMatch(
      /open/,
    );
  });

  it("closes with a reason, SHOWS the reason, and re-opening clears it", async () => {
    const app = await withOneOrder();
    // Untick open-only FIRST, so the closed order stays visible in this list. That
    // it would otherwise vanish is correct behaviour and is asserted separately —
    // it is only in the way here, where the reason on the row is what matters.
    fireEvent.click(
      screen.getByLabelText("Show only orders with something outstanding"),
    );
    await screen.findByTestId("sales-orders-row:SO-W");
    fireEvent.click(
      screen.getByRole("button", { name: "Select order SO-W" }),
    );
    fireEvent.keyDown(app, { key: "w", altKey: true });
    fireEvent.change(
      screen.getByLabelText("Reason for pre-closing this order"),
      { target: { value: "customer cancelled" } },
    );
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // ⛔ THE REASON IS ON THE FACE OF THE LIST. A closed order whose reason is not
    // shown is a commitment that vanished for no stated cause.
    await waitFor(() =>
      expect(screen.getByTestId("sales-orders-row:SO-W").textContent).toMatch(
        /pre-closed — customer cancelled/,
      ),
    );

    // Re-open it. The server refuses a reason on an OPEN order, so accepting here
    // must clear it rather than resend it — which is the only reading under which
    // "I closed the wrong order" is correctable.
    fireEvent.keyDown(app, { key: "w", altKey: true });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(screen.getByTestId("sales-orders-row:SO-W").textContent).toMatch(
        /open/,
      ),
    );
    expect(screen.getByTestId("sales-orders-row:SO-W").textContent).not.toMatch(
      /customer cancelled/,
    );
  });

  it("the panel says it ABANDONS the order and does not mean 'delivered'", async () => {
    // The wording is the feature here. An operator who reads "close" as "mark it
    // delivered" would use this key to tidy up a finished order and would be
    // recording that the balance was abandoned instead.
    const app = await withOneOrder();
    fireEvent.keyDown(app, { key: "w", altKey: true });
    const panel = screen.getByTestId("sales-orders-close-panel");
    expect(panel.textContent).toMatch(/ABANDONS THE ORDER/);
    expect(panel.textContent).toMatch(/does NOT mean/);
    expect(panel.textContent).toMatch(/DERIVED/);
  });
});

describe("the open-only filter, and the heading that must not lie about it", () => {
  it("renders the heading from the RESPONSE's echo, not from the checkbox", async () => {
    // "No orders" and "no OPEN orders" are different answers to an operator, and a
    // filtered list under an unfiltered heading is how the second gets read as the
    // first. The server echoes the filter for exactly this reason.
    const app = renderShell();
    await openRegister(app, "sales");
    expect(screen.getByText(/Orders with something still outstanding/)).toBeTruthy();

    fireEvent.click(
      screen.getByLabelText("Show only orders with something outstanding"),
    );
    expect(
      await screen.findByText(/Every order on this register, outstanding or not/),
    ).toBeTruthy();
  });

  it("a pre-closed order leaves the open-only list but is visible unfiltered", async () => {
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyAnOrder({ no: "SO-GONE", qty: "3" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByTestId("sales-orders-row:SO-GONE");

    fireEvent.keyDown(app, { key: "w", altKey: true });
    fireEvent.change(
      screen.getByLabelText("Reason for pre-closing this order"),
      { target: { value: "cancelled" } },
    );
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // Gone from the open list…
    await waitFor(() =>
      expect(screen.queryByTestId("sales-orders-row:SO-GONE")).toBeNull(),
    );
    // …and NOT gone from the register. An order that disappears entirely is how an
    // operator concludes it was never recorded and records it again.
    fireEvent.click(
      screen.getByLabelText("Show only orders with something outstanding"),
    );
    expect(await screen.findByTestId("sales-orders-row:SO-GONE")).toBeTruthy();
  });
});

/**
 * ⛔⛔ WHO MAY BE THE PARTY — D6, and the highest-severity defect on this screen.
 *
 * The picker offered the ENTIRE chart of accounts, so a sales order could be
 * committed to a BANK ledger and a purchase order to a customer, and nothing on
 * the client, in the server's Validate, or in the database said no. Both order
 * books GROUP BY COUNTERPARTY to answer "who is owed what", so such an order
 * names — in a report an operator acts on — an account that can never be invoiced
 * and can never deliver.
 *
 * ⚠️ THE SEEDED DEMO COMPANY IS THE FIXTURE THIS NEEDS AND IT IS NOT AN ACCIDENT:
 * it carries Cash (Cash-in-Hand), Sales (Sales Accounts), HDFC Bank (a Bank
 * Accounts SUB-GROUP) and Acme Traders (Sundry Debtors). Exactly one of the four
 * is a customer.
 */
describe("the party is a COUNTERPARTY, not any ledger on the chart", () => {
  /** The option LABELS the party picker is offering, in order. */
  function partyOptions(): string[] {
    const select = screen.getByLabelText(/Customer|Supplier/) as HTMLSelectElement;
    return [...select.options].map((o) => o.textContent ?? "");
  }

  it("⛔ a SALES order does not offer the bank, the cash or an income ledger", async () => {
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });

    const offered = partyOptions();
    // THE CONTROL: the one real customer IS offered. Without it, a picker that
    // offered nothing at all would pass every assertion below.
    expect(offered).toContain("Acme Traders");
    // …and nothing else on the chart is.
    expect(offered).not.toContain("HDFC Bank");
    expect(offered).not.toContain("Cash");
    expect(offered).not.toContain("Sales");

    // The screen SAYS why the list is short, rather than leaving a silently
    // shorter list — which is not an error and so is never noticed.
    const note = screen.getByTestId("sales-orders-party-group-note");
    expect(note.textContent).toMatch(/Sundry Debtors/);
    expect(note.textContent).toMatch(/groups\s+by counterparty/);
  });

  it("⛔ a PURCHASE order asks for a SUPPLIER, and this company has none", async () => {
    // The mirror of the defect: a purchase commitment made to a customer. The
    // seeded company has no Sundry Creditor at all, so the correct behaviour is an
    // empty picker WITH A SENTENCE — not a picker quietly offering the debtor.
    const app = renderShell();
    await openRegister(app, "purchase");
    fireEvent.keyDown(app, { key: "c", altKey: true });

    expect(partyOptions()).not.toContain("Acme Traders");
    const note = screen.getByTestId("purchase-orders-party-group-note");
    expect(note.textContent).toMatch(/Sundry Creditors/);
    expect(note.textContent).toMatch(/no ledger under Sundry Creditors yet/);
  });

  it("offers a supplier once one exists, and still not the customer", async () => {
    const client = createMockClient();
    await client.createLedger(COMPANY, {
      name: "Steel Suppliers",
      group_name: "Sundry Creditors",
    });
    const app = renderShell(client);
    await openRegister(app, "purchase");
    fireEvent.keyDown(app, { key: "c", altKey: true });

    const offered = partyOptions();
    expect(offered).toContain("Steel Suppliers");
    expect(offered).not.toContain("Acme Traders");
    expect(offered).not.toContain("HDFC Bank");
  });

  it("⛔ the way out is DELIBERATE, and narrowing back drops the choice", async () => {
    // The client filter compares each ledger's IMMEDIATE group_name, while the
    // server walks ltree ANCESTRY — so a customer kept in a sub-group of Sundry
    // Debtors is legitimate and invisible here. The escape hatch exists for that
    // case and defaults to OFF, because the wrong party must be reachable only on
    // purpose. HDFC Bank stands in for "a ledger this list will not offer".
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });

    const widen = screen.getByLabelText(
      "Offer every ledger, not only this direction's party group",
    );
    fireEvent.click(widen);
    expect(partyOptions().join("|")).toMatch(/HDFC Bank/);

    // Pick the bank, then narrow back. The request carries the ID, not the picked
    // option, so a selection left behind would be SENT — and the operator would
    // meet a 422 naming a party the form no longer shows them.
    const select = screen.getByLabelText("Customer") as HTMLSelectElement;
    const bank = [...select.options].find((o) =>
      (o.textContent ?? "").startsWith("HDFC Bank"),
    );
    fireEvent.change(select, { target: { value: bank?.value } });
    expect(select.value).toBe(bank?.value);

    fireEvent.click(widen);
    expect(
      (screen.getByLabelText("Customer") as HTMLSelectElement).value,
    ).toBe("");
    expect(partyOptions()).not.toContain("HDFC Bank");
  });

  it("a customer picked from the narrowed list still records the order", async () => {
    // ⛔ THE CONTROL FOR THE WHOLE SECTION. Every assertion above is about what is
    // NOT offered, and all of them would pass on a screen that had stopped being
    // able to record an order at all.
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyAnOrder({ no: "SO-PARTY", qty: "20", rate: "500" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const row = await screen.findByTestId("sales-orders-row:SO-PARTY");
    // The PARTY and the QUANTITY, never a status code: what an order records is
    // who it commits us to and how much.
    expect(row.textContent).toMatch(/Acme Traders/);
    const line = await screen.findByTestId(
      "sales-orders-line-row:SO-PARTY:1",
    );
    expect(line.textContent).toMatch(/20\.0000/);
  });
});

/**
 * ⛔⛔ D70 — AFTER Alt+W, THE DETAIL PANEL MUST NOT GO ON SAYING "OUTSTANDING".
 *
 * Pre-closing drops the order from the open-only list, and the line panel below
 * that list went on showing its balance under a column headed "Outstanding", with
 * no pre-closed marker anywhere — while the row it came from had vanished from
 * the table above. An operator was left reading a live-looking commitment
 * belonging to an order that no longer appeared to exist, and chasing goods
 * nobody is going to send.
 */
describe("a pre-closed order's detail says so, and stops calling it outstanding", () => {
  it("marks the panel PRE-CLOSED with its reason, after the row has gone", async () => {
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyAnOrder({ no: "SO-ABANDON", qty: "12" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByTestId("sales-orders-row:SO-ABANDON");

    // THE CONTROL, BEFORE: the panel calls the balance Outstanding, correctly.
    const linesBefore = await screen.findByTestId("sales-orders-lines");
    expect(linesBefore.textContent).toMatch(/Outstanding/);
    expect(screen.queryByTestId("sales-orders-detail-preclosed")).toBeNull();

    fireEvent.keyDown(app, { key: "w", altKey: true });
    fireEvent.change(
      screen.getByLabelText("Reason for pre-closing this order"),
      { target: { value: "customer cancelled the site" } },
    );
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // The row has gone from the open-only list — that part always worked.
    await waitFor(() =>
      expect(screen.queryByTestId("sales-orders-row:SO-ABANDON")).toBeNull(),
    );

    // ⛔ AND THE PANEL BELOW IT NOW SAYS WHAT HAPPENED, with the reason. This is
    // the assertion that fails on the defect: the heading was just
    // "SO-ABANDON · Acme Traders", indistinguishable from a live order.
    const marker = await screen.findByTestId("sales-orders-detail-preclosed");
    expect(marker.textContent).toMatch(/PRE-CLOSED/);
    expect(marker.textContent).toMatch(/customer cancelled the site/);

    const note = screen.getByTestId("sales-orders-detail-preclosed-note");
    expect(note.textContent).toMatch(/ABANDONED, NOT DELIVERED/);
    // It explains where the row went AND how to get it back — otherwise
    // re-opening the order is unreachable from the state Alt+W leaves behind.
    expect(note.textContent).toMatch(/Only orders with something outstanding/);

    // ⛔ THE COLUMN IS RENAMED, AND THE FIGURE IS STILL THERE. The 12 is what was
    // still owed when the commitment was abandoned — it is the number an auditor
    // asks about and must not be blanked. What must go is the WORD.
    const lines = screen.getByTestId("sales-orders-lines");
    expect(lines.textContent).toMatch(/Abandoned/);
    expect(lines.textContent).not.toMatch(/Outstanding/);
    expect(
      screen.getByTestId("sales-orders-line-row:SO-ABANDON:1").textContent,
    ).toMatch(/12\.0000/);
  });

  it("re-opening puts the word Outstanding back — the marker is not sticky", async () => {
    // The control for the test above. A panel permanently stuck on "Abandoned"
    // would pass every assertion there and would be a worse defect.
    const app = renderShell();
    await openRegister(app, "sales");
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyAnOrder({ no: "SO-BACK", qty: "4" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByTestId("sales-orders-row:SO-BACK");

    fireEvent.keyDown(app, { key: "w", altKey: true });
    fireEvent.change(
      screen.getByLabelText("Reason for pre-closing this order"),
      { target: { value: "keyed twice" } },
    );
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByTestId("sales-orders-detail-preclosed");

    // Untick the filter so the order is back in the list, then Alt+W re-opens it —
    // the exact route the panel's own sentence points at.
    fireEvent.click(
      screen.getByLabelText("Show only orders with something outstanding"),
    );
    // ⚠️ AWAITED. Unticking re-reads the list AND drops the highlight (an order
    // that has just left the list must not stay selected), so the row is not in
    // the DOM on the next tick and a synchronous getByRole races the reload.
    fireEvent.click(
      await screen.findByRole("button", { name: "Select order SO-BACK" }),
    );
    fireEvent.keyDown(app, { key: "w", altKey: true });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(screen.queryByTestId("sales-orders-detail-preclosed")).toBeNull(),
    );
    expect(screen.getByTestId("sales-orders-lines").textContent).toMatch(
      /Outstanding/,
    );
  });
});

describe("a 403 on a register is a DECISION, and it names the direction", () => {
  it("says which direction was refused and why the split exists", async () => {
    const client = createMockClient();
    const denying: CoredClient = {
      ...client,
      async listPurchaseOrders() {
        throw new ApiError(403, "forbidden", "forbidden");
      },
    };
    const app = renderShell(denying);
    fireEvent.keyDown(app, { key: "F9", ctrlKey: true });

    const panel = await screen.findByTestId("purchase-orders-denied");
    // It NAMES the direction. Being allowed one register and refused the other is
    // the configuration two authorities exist to make possible, so an operator who
    // sees the sales register work needs to know this refusal is deliberate.
    expect(panel.textContent).toMatch(/purchase orders/i);
    expect(panel.textContent).toMatch(/allow purchase-order/);
    // And it explains that the split is what makes the rule writable at all.
    expect(panel.textContent).toMatch(/cannot see a query parameter/);
    // The SALES register is unaffected — that is the whole point.
    fireEvent.keyDown(app, { key: "F8", ctrlKey: true });
    expect(await screen.findByTestId("sales-orders-empty")).toBeTruthy();
  });
});
