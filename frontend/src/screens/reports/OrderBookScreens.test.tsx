import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";

import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type { CoredClient } from "../../api";
import {
  AS_AT_ONLY_REPORTS,
  FULL_PERIOD_REPORTS,
} from "../overlays/FrameOverlays";

/**
 * THE TWO ORDER BOOKS (T3) — what is still outstanding, as at a date.
 *
 * ⛔⛔ THE ONE STRUCTURAL FACT THIS FILE GUARDS: THERE ARE TWO OF THEM.
 *
 * `report:sales-order-book` and `report:purchase-order-book` are two separate report
 * names on the server because THE PDP DECIDES ON THE REPORT NAME AND A QUERY
 * PARAMETER IS INVISIBLE TO IT. One report with `?direction=` would make "the
 * salesperson may see our sales pipeline but not what we have committed to pay
 * suppliers" an unwritable rule, and the toggle would be reaching a resource the
 * operator may be denied — a 403 nobody can attribute to anything they did.
 * Migration 0073 writes both refusals, each as a `disallow display` AND a `disallow
 * print`, because a report that cannot be displayed but CAN be printed is a leak
 * with a paper trail.
 *
 * The tests below are written so that COLLAPSING THE TWO INTO ONE cannot pass them.
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

/** Gateway -> Display More Reports -> the named book. */
async function openBook(
  app: HTMLElement,
  name: "Sales Order Book" | "Purchase Order Book",
): Promise<void> {
  fireEvent.mouseDown(screen.getByText("Display More Reports"));
  fireEvent.keyDown(app, { key: "Enter" });
  fireEvent.mouseDown(await screen.findByText(name));
  fireEvent.keyDown(app, { key: "Enter" });
}

/** Record one sales order so the book has something outstanding to report. */
async function seedOrder(client: CoredClient, no = "SO-1", qty = "20") {
  await client.createSalesOrder(COMPANY, {
    order_no: no,
    order_date: "2026-06-01",
    party_ledger_id: "ledger-acme",
    created_by: "ui",
    lines: [{ stock_item_id: "stock-widget", qty, rate: "500" }],
  });
}

describe("both books are REACHABLE, and they are two different screens", () => {
  it("each opens from the report menu and renders its OWN direction", async () => {
    // ⛔ IF THE TWO WERE EVER COLLAPSED INTO ONE SCREEN WITH A TOGGLE, one of these
    // two halves could not hold: each asserts its own testid, which is derived from
    // the direction fixed at REGISTRATION and not from any control on the screen.
    const client = createMockClient();
    await seedOrder(client);
    const app = renderShell(client);

    await openBook(app, "Sales Order Book");
    expect(await screen.findByTestId("sales-order-book-table")).toBeTruthy();
    expect(screen.queryByTestId("purchase-order-book-table")).toBeNull();
    // The party column is named for the direction: a customer promises US.
    expect(screen.getByText("Customer")).toBeTruthy();

    fireEvent.keyDown(app, { key: "Escape" });
    fireEvent.keyDown(app, { key: "Escape" });
    await openBook(app, "Purchase Order Book");
    // The purchase book is EMPTY — the seeded order was a SALES order, and if one
    // screen served both directions this assertion would see the sales row.
    expect(await screen.findByTestId("purchase-order-book-empty")).toBeTruthy();
    expect(screen.queryByTestId("sales-order-book-table")).toBeNull();
    expect(screen.getByText(/Supplier|committed to buy from suppliers/)).toBeTruthy();
  });

  it("neither screen offers a direction control — that IS the design", async () => {
    // The rule is about authority, not pixels: one COMPONENT renders both books, and
    // the direction is fixed at registration. This asserts the thing that would break
    // it — a control that lets the operator change direction in place, which would
    // reach the other report name without the PDP ever seeing a different path.
    const client = createMockClient();
    await seedOrder(client);
    const app = renderShell(client);
    await openBook(app, "Sales Order Book");
    await screen.findByTestId("sales-order-book-table");

    expect(screen.queryByLabelText(/direction/i)).toBeNull();
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.queryByText(/purchase/i)).toBeNull();
  });
});

describe("the figures, and the four things these screens must not do", () => {
  it("shows ordered, fulfilled and outstanding — and totals only the VALUE", async () => {
    const client = createMockClient();
    await seedOrder(client, "SO-1", "20");
    const app = renderShell(client);
    await openBook(app, "Sales Order Book");

    const row = await screen.findByTestId("sales-order-book-row:SO-1:Widget");
    expect(row.textContent).toMatch(/Acme Traders/);
    expect(row.textContent).toMatch(/20\.0000/); // ordered
    expect(row.textContent).toMatch(/0\.0000/); // nothing delivered yet

    // ⛔ THE TOTAL IS THE SERVER'S OWN FIGURE, and it is a VALUE. Quantities are
    // never totalled: two lines of different items are in different units, so their
    // sum is not a quantity at all.
    const total = screen.getByTestId("sales-order-book-total");
    expect(total.textContent).toMatch(/10,000/); // 20 @ 500, Indian grouping
    // And nothing on the screen totals a quantity column.
    expect(screen.queryByTestId("sales-order-book-qty-total")).toBeNull();
  });

  it("⛔ renders a missing due date as 'none agreed' and NEVER as overdue", async () => {
    // An omitted due date means no delivery date was AGREED, which is not a late
    // commitment however old the order. A client that compared "" against today
    // would mark every undated line overdue and invent a collections problem. The
    // server computes `is_overdue` and it is false here; the screen renders the flag
    // and never re-derives it.
    const client = createMockClient();
    await seedOrder(client, "SO-NODATE", "5"); // seedOrder sends no due_date
    const app = renderShell(client);
    await openBook(app, "Sales Order Book");

    const row = await screen.findByTestId(
      "sales-order-book-row:SO-NODATE:Widget",
    );
    expect(row.textContent).toMatch(/none agreed/);
    expect(row.textContent).not.toMatch(/OVERDUE/);
    expect(
      screen.queryByTestId("sales-order-book-overdue:SO-NODATE:Widget"),
    ).toBeNull();
  });

  it("distinguishes 'no orders' from 'nothing outstanding'", async () => {
    // These are different answers to an operator: one says the company has made no
    // commitments, the other that every commitment has been met. The empty state says
    // the second and points at the register, which lists them all.
    const app = renderShell();
    await openBook(app, "Sales Order Book");
    const empty = await screen.findByTestId("sales-order-book-empty");
    expect(empty.textContent).toMatch(/not the same as having no\s+orders/);
    expect(empty.textContent).toMatch(/Ctrl\+F8/);
  });

  it("renders the server's NOTES, including on an empty book", async () => {
    // On an empty book the notes may be the only thing that explains the emptiness —
    // e.g. that no as_on was given, so the position is from every order ever placed.
    const app = renderShell();
    await openBook(app, "Sales Order Book");
    await screen.findByTestId("sales-order-book-empty");
    const notes = screen.queryByTestId("sales-order-book-notes");
    // The mock does send notes here; if it ever stops, this must not silently pass.
    expect(notes).not.toBeNull();
    expect((notes as HTMLElement).textContent?.length ?? 0).toBeGreaterThan(0);
  });
});

describe("both books are POSITIONS as at a date, not flows", () => {
  it("is classified AS-AT-ONLY, and the classification matches what it reads", () => {
    // ⛔ THE F2 DIALOG TOLD TWO REPORTS THEIR DATES DID NOTHING WHILE THEY READ THEM,
    // and it shipped twice. A report in none of the three allowlists gets that
    // default. periodClassification.test.ts asserts the closure over registered
    // names; this asserts the DIRECTION of the classification for these two, because
    // being in the WRONG list is the other half of the same bug.
    //
    // Outstanding-as-at-a-date is a POSITION. Bounding it BELOW would be actively
    // wrong rather than merely useless: it would drop the orders PLACED before the
    // window while leaving their deliveries inside it, so a long-open order would
    // read as fully delivered. The outstanding balance is a subtraction, and dropping
    // one side of it produces a confident wrong answer.
    for (const name of ["Sales Order Book", "Purchase Order Book"]) {
      expect(AS_AT_ONLY_REPORTS).toContain(name);
      expect(FULL_PERIOD_REPORTS).not.toContain(name);
    }
  });

  it("labels the heading with the date the SERVER answered for", async () => {
    // Not the date on the frame. The server resolves an omitted as_on to its own
    // today, and a heading reading "As at (our today)" over figures computed for a
    // different day would be a report labelled with a date it was not run for.
    const client = createMockClient();
    await seedOrder(client);
    const app = renderShell(client);
    await openBook(app, "Sales Order Book");
    await screen.findByTestId("sales-order-book-table");
    const book = await client.salesOrderBook(COMPANY);
    expect(screen.getByText(new RegExp(`As at ${book.as_on}`))).toBeTruthy();
  });
});

describe("a 403 on one book is a DECISION, and the other still works", () => {
  it("names the direction refused and explains why the split exists", async () => {
    const client = createMockClient();
    await seedOrder(client);
    const denying: CoredClient = {
      ...client,
      async purchaseOrderBook() {
        throw new ApiError(403, "forbidden", "forbidden");
      },
    };
    const app = renderShell(denying);

    await openBook(app, "Purchase Order Book");
    const panel = await screen.findByTestId("purchase-order-book-denied");
    expect(panel.textContent).toMatch(/PURCHASE order book/);
    // It explains the split, and names BOTH rules — the print one included, because
    // a report that cannot be displayed but can be printed is a leak with a paper
    // trail.
    expect(panel.textContent).toMatch(/cannot see a query parameter/);
    expect(panel.textContent).toMatch(
      /disallow display report:purchase-order-book/,
    );
    expect(panel.textContent).toMatch(/disallow print/);

    // ⛔ AND THE SALES BOOK STILL WORKS. That is the entire purpose of two names:
    // one granted and one refused is a configuration, not a fault.
    fireEvent.keyDown(app, { key: "Escape" });
    fireEvent.keyDown(app, { key: "Escape" });
    await openBook(app, "Sales Order Book");
    expect(await screen.findByTestId("sales-order-book-table")).toBeTruthy();
  });
});
