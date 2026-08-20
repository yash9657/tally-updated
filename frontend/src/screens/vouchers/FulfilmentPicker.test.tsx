import { describe, it, expect, afterEach } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor,
} from "@testing-library/react";

import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import { createMockClient } from "../../api/mock";
import type { CoredClient } from "../../api";
import {
  buildFulfilments,
  fulfilmentWarning,
  fulfilsDirection,
} from "./fulfilment";
import { byCommitmentOrder, type Candidate } from "./FulfilmentPicker";

/**
 * THE FULFILMENT PICKER (T3) — can an operator actually link a delivery to an order?
 *
 * The answer was NO for the whole life of the feature: `voucherReq.fulfilments` was on
 * the wire, `FulfilmentInput` was in types.ts, over-fulfilment was refused by a
 * deferred trigger and both order books read the links back — and no screen could
 * create one. The order book therefore reported every order as fully outstanding
 * forever, correctly, because nothing could discharge one.
 *
 * ⛔ THE TWO ASSERTIONS THAT MATTER MOST HERE ARE THE ONES ABOUT ARITHMETIC, not about
 * the DOM:
 *
 *  1. THE LINK QTY IS NOT THE MOVEMENT QTY. Shipping 105 against a 100-unit order is
 *     legal and must stay saveable: a form that pre-filled the movement quantity and
 *     locked it would make a real over-delivery impossible to record. There is a test
 *     below that keys exactly that case.
 *
 *  2. `line_no` IS 1-BASED AND IS COUNTED OVER THE *KEYED* LINES. The grid is filtered
 *     before it becomes `inventory`, so a blank row above a keyed one shifts every
 *     number. The pure test for this is the one that would have caught the whole class
 *     of defect, and it is first.
 */

const COMPANY = "company-demo";

afterEach(cleanup);

// ---------------------------------------------------------------------------
// THE PURE HALF. These need no DOM and they are the assertions with teeth.
// ---------------------------------------------------------------------------

/** A minimal line, so these tests state only what they are about. */
function line(
  stockItemId: string,
  qty: string,
  fulfilments: { orderLineId: string; qty: string }[] = [],
) {
  return {
    stockItemId,
    qty,
    fulfilments: fulfilments.map((f) => ({
      orderLineId: f.orderLineId,
      qty: f.qty,
      orderNo: "",
      itemName: "",
      outstandingAtPick: "",
    })),
  };
}

/**
 * ⛔⛔ "OLDEST COMMITMENT FIRST" — AND IT USED TO OFFER THE NEWEST.
 *
 * The candidates were sorted with `a.orderNo.localeCompare(b.orderNo)` under a
 * comment reading "Oldest commitment first". Two faults, and the cases below are
 * chosen so that fixing only one of them still fails:
 *
 *  1. an order NUMBER is not a date — a series that restarts each year, or is
 *     prefixed by branch, sorts into an order unrelated to when anything was
 *     promised;
 *  2. a RAW STRING compare is not numeric — bare integers with an independent
 *     counter make "10" sort above "9", the same lexical hazard
 *     HANDOFF-DEFECT-BURNDOWN §4.1 records for voucher numbers.
 *
 * Asserted on the comparator directly rather than through a rendered <select>,
 * because the numeric case needs nine orders to show itself and a DOM fixture
 * that seeds nine would be testing the fixture.
 */
describe("the fulfilment picker offers the OLDEST commitment first", () => {
  const cand = (
    orderNo: string,
    orderDate: string,
    orderLineId = `line-${orderNo}`,
  ): Candidate => ({
    orderLineId,
    orderNo,
    orderDate,
    itemName: "MS Angle",
    outstanding: "20.0000",
    qty: "20.0000",
    dueDate: "",
  });

  const sortedNos = (cs: Candidate[]) =>
    [...cs].sort(byCommitmentOrder).map((c) => c.orderNo);

  it("⛔ orders by the ORDER DATE, so the number's shape cannot decide it", () => {
    // SO-100 was placed in April; SO-2 in June. The April commitment has been
    // waiting two months longer and is the one a shipment today discharges.
    const got = sortedNos([
      cand("SO-2", "2026-06-01"),
      cand("SO-100", "2026-04-05"),
      cand("SO-9", "2026-05-20"),
    ]);
    expect(got).toEqual(["SO-100", "SO-9", "SO-2"]);
    // The specific regression: a plain string sort would have put SO-100 first
    // here by accident, and SO-2 first if the numbers had been the other way up.
    expect(got[0]).not.toBe("SO-2");
  });

  it("⛔ breaks a same-day tie NUMERICALLY, so \"9\" precedes \"10\"", () => {
    // The lexical trap: as raw strings "SO-10" < "SO-9", so the tenth order would
    // be offered ahead of the ninth. Every one of these was placed the same day,
    // which is exactly when the number is all there is to go on.
    expect(
      sortedNos([
        cand("SO-10", "2026-06-01"),
        cand("SO-9", "2026-06-01"),
        cand("SO-1", "2026-06-01"),
        cand("SO-2", "2026-06-01"),
      ]),
    ).toEqual(["SO-1", "SO-2", "SO-9", "SO-10"]);

    // And bare integers, which is the shape §4.1 names.
    expect(
      sortedNos([cand("10", "2026-06-01"), cand("9", "2026-06-01")]),
    ).toEqual(["9", "10"]);
  });

  it("is a TOTAL order — two identical orders never swap between renders", () => {
    // The final tie-break is the order LINE id, and those are UUIDv7 (minted in
    // creation order), so it is a stable time ordering rather than a coin toss.
    // Without it the operator's selection appears to move on its own when the
    // picker re-renders after an unrelated keystroke.
    const a = cand("SO-1", "2026-06-01", "0198-aaaa");
    const b = cand("SO-1", "2026-06-01", "0198-bbbb");
    expect(byCommitmentOrder(a, b)).toBeLessThan(0);
    expect(byCommitmentOrder(b, a)).toBeGreaterThan(0);
    expect(byCommitmentOrder(a, a)).toBe(0);
  });
});

describe("buildFulfilments numbers links from the array it is GIVEN", () => {
  it("is 1-BASED, not 0-based", () => {
    // stock_movement.line_no has been 1-based since migration 0025, and
    // CostAllocationInput.posting_seq on the SAME request body is 0-based. A client
    // that sends an array index here is exactly one out.
    const out = buildFulfilments([line("item-1", "5", [{ orderLineId: "ol-1", qty: "5" }])]);
    expect(out).toEqual([{ line_no: 1, order_line_id: "ol-1", qty: "5.0000" }]);
  });

  it("⛔ counts over the FILTERED lines, so a blank row above cannot shift it", () => {
    // THE DEFECT THIS PINS. buildItemInvoiceRequest builds `inventory` from the KEYED
    // lines, so a grid whose first row is blank sends the second row as inventory line
    // ONE. Numbering from the grid position would name a line that either does not
    // exist or holds different goods — and the server would either 422 or, worse,
    // discharge the wrong order.
    //
    // The design that prevents it is that the links travel ON the line, so the caller
    // passes the same filtered array it built inventory from. This asserts the
    // contract that makes that safe: whatever array arrives, the numbering is its own
    // positions.
    const keyed = [
      // (the blank row has already been filtered out by the caller)
      line("item-2", "8", [{ orderLineId: "ol-2", qty: "8" }]),
      line("item-3", "3", [{ orderLineId: "ol-3", qty: "3" }]),
    ];
    expect(buildFulfilments(keyed).map((f) => f.line_no)).toEqual([1, 2]);
  });

  it("drops half-picked and zero links rather than sending them", () => {
    const out = buildFulfilments([
      line("item-1", "5", [
        { orderLineId: "", qty: "5" }, // no order chosen
        { orderLineId: "ol-1", qty: "" }, // nothing typed
        { orderLineId: "ol-2", qty: "0" }, // explicitly nothing
        { orderLineId: "ol-3", qty: "2" }, // the only real one
      ]),
    ]);
    expect(out).toEqual([{ line_no: 1, order_line_id: "ol-3", qty: "2.0000" }]);
  });

  it("carries several links on ONE line, which is ordinary", () => {
    // A delivery of 30 legitimately discharges a 20-unit order and 10 of a second.
    const out = buildFulfilments([
      line("item-1", "30", [
        { orderLineId: "ol-1", qty: "20" },
        { orderLineId: "ol-2", qty: "10" },
      ]),
    ]);
    expect(out).toHaveLength(2);
    expect(out.every((f) => f.line_no === 1)).toBe(true);
  });
});

describe("fulfilsDirection — which classes can discharge a commitment", () => {
  it("maps goods OUT to sales orders and goods IN to purchase orders", () => {
    // Decided by WHICH WAY THE GOODS MOVE. Offering the wrong family would let an
    // operator record that a customer's order was satisfied by goods we RECEIVED.
    expect(fulfilsDirection("delivery-note")).toBe("SALES");
    expect(fulfilsDirection("sales")).toBe("SALES");
    expect(fulfilsDirection("receipt-note")).toBe("PURCHASE");
    expect(fulfilsDirection("purchase")).toBe("PURCHASE");
  });

  it("⛔ refuses the classes that discharge NOTHING, each for its own reason", () => {
    // A transfer never leaves the business; a count is not a movement anyone ordered;
    // a return UNDOES a commitment rather than discharging one, and there is no
    // negative-link path. And the six accounting classes move no goods at all.
    for (const t of [
      "stock-journal",
      "physical-stock",
      "credit-note",
      "debit-note",
      "journal",
      "payment",
      "receipt",
      "contra",
    ] as const) {
      expect(fulfilsDirection(t), `${t} must discharge no order`).toBeNull();
    }
  });
});

describe("fulfilmentWarning warns, and never in the wrong direction", () => {
  it("⛔ says nothing at all when the operator has linked NOTHING", () => {
    // Most vouchers discharge no order. A form that nagged about it would train the
    // operator to ignore the one case that matters.
    expect(fulfilmentWarning(line("i", "10"))).toBe("");
  });

  it("says the server will refuse OVER-linking", () => {
    const w = fulfilmentWarning(
      line("i", "10", [{ orderLineId: "ol-1", qty: "40" }]),
    );
    expect(w).toMatch(/will refuse/);
    expect(w).toMatch(/forty units of promise/);
  });

  it("⛔ EXPLAINS under-linking and does NOT call it a problem", () => {
    // THE OVER-DELIVERY CASE, from the other side. 105 shipped against a 100-unit
    // order links 100 and moves 105 — the extra 5 consumed no order because nobody
    // ordered it. A warning that read as an error here would push the operator to
    // enter a number that is wrong.
    const w = fulfilmentWarning(
      line("i", "105", [{ orderLineId: "ol-1", qty: "100" }]),
    );
    expect(w).toMatch(/allowed/);
    expect(w).toMatch(/consumed no order/);
    expect(w).not.toMatch(/refuse|error|invalid/i);
  });

  it("is silent when the link exactly matches the movement", () => {
    expect(
      fulfilmentWarning(line("i", "10", [{ orderLineId: "ol-1", qty: "10" }])),
    ).toBe("");
  });
});

// ---------------------------------------------------------------------------
// THROUGH THE REAL SHELL. The only tests that prove an operator can reach it.
// ---------------------------------------------------------------------------

function renderShell(client: CoredClient) {
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

/** A 20-unit sales order for Widgets, created the way a client would. */
async function seedSalesOrder(client: CoredClient, no = "SO-1", qty = "20") {
  await client.createSalesOrder(COMPANY, {
    order_no: no,
    order_date: "2026-06-01",
    party_ledger_id: "ledger-acme",
    created_by: "ui",
    lines: [{ stock_item_id: "stock-widget", qty, rate: "500" }],
  });
}

/** Open Alt+F8 Delivery Note and key one Widget line of `qty`. */
async function openDeliveryNote(app: HTMLElement, qty: string) {
  fireEvent.keyDown(app, { key: "F8", altKey: true });
  await screen.findByTestId("stock-grid");
  const item = (await screen.findByLabelText("Item 1")) as HTMLSelectElement;
  fireEvent.change(item, {
    target: {
      value: [...item.options].find((o) => o.textContent === "Widget")?.value,
    },
  });
  fireEvent.change(screen.getByLabelText("Quantity 1"), {
    target: { value: qty },
  });
  fireEvent.change(screen.getByLabelText("Rate 1"), {
    target: { value: "500" },
  });
  // ⛔ A DELIVERY NOTE NOW REQUIRES A CONSIGNEE — see buildStockVoucherRequest.
  // Keyed in the HELPER because it is a precondition of keying this class at
  // all, and is the subject of no test in this file (they are about the ORDER LINK).
  // Found by driving D30: printing refuses any printable voucher with no other
  // party, so a delivery note saved without one could never be printed.
  fireEvent.change(screen.getByTestId("stock-party"), {
    target: { value: "Acme Traders" },
  });
}

describe("an operator can link a delivery note to a sales order", () => {
  it("the picker offers the OPEN order for the same item, by NUMBER not by id", async () => {
    const client = createMockClient();
    await seedSalesOrder(client);
    const app = renderShell(client);
    await openDeliveryNote(app, "8");

    // The link cell is on the row the operator is already looking at.
    const cell = await screen.findByTestId("stock-fulfilment:0");
    expect(cell).toBeTruthy();
    fireEvent.click(await screen.findByLabelText("Link line 1 to an order"));

    const picker = (await screen.findByLabelText(
      "Order for line 1, link 1",
    )) as HTMLSelectElement;
    // ⛔ THE OPTION IS HUMAN-READABLE. An operator cannot pick an order_line_id, so
    // the label carries the order NUMBER, the item and what is outstanding.
    const labels = [...picker.options].map((o) => o.textContent ?? "");
    expect(labels.some((l) => l.includes("SO-1"))).toBe(true);
    expect(labels.some((l) => l.includes("Widget"))).toBe(true);
    expect(labels.some((l) => l.includes("20.0000 outstanding"))).toBe(true);
    // And no field anywhere asks for a line number.
    expect(screen.queryByLabelText(/line_no|line number/i)).toBeNull();
  });

  it("⛔ does NOT pre-fill the link quantity when the order is picked", async () => {
    // THE RULE THIS FILE EXISTS FOR. Picking the order does not decide how much of it
    // this delivery discharges, and pre-filling-and-locking would make a legitimate
    // over-delivery unsaveable.
    const client = createMockClient();
    await seedSalesOrder(client);
    const app = renderShell(client);
    await openDeliveryNote(app, "8");
    fireEvent.click(await screen.findByLabelText("Link line 1 to an order"));

    const picker = (await screen.findByLabelText(
      "Order for line 1, link 1",
    )) as HTMLSelectElement;
    fireEvent.change(picker, { target: { value: picker.options[1]!.value } });

    const qty = screen.getByLabelText(
      "Quantity linked on line 1, link 1",
    ) as HTMLInputElement;
    expect(qty.value).toBe("");
    expect(qty.readOnly).toBe(false);
    expect(qty.disabled).toBe(false);
  });

  it("saves the link and the ORDER BOOK moves — wire to report", async () => {
    // The assertion spans the grid -> the built request -> the mock's validation ->
    // the derived fulfilment -> the order book. Nothing in between is stubbed.
    const client = createMockClient();
    await seedSalesOrder(client);
    const app = renderShell(client);
    await openDeliveryNote(app, "8");

    // CONTROL: fully outstanding before anything is linked.
    let book = await client.salesOrderBook(COMPANY);
    expect(book.rows[0]!.outstanding_qty).toBe("20.0000");
    expect(book.rows[0]!.fulfilled_qty).toBe("0.0000");

    fireEvent.click(await screen.findByLabelText("Link line 1 to an order"));
    const picker = (await screen.findByLabelText(
      "Order for line 1, link 1",
    )) as HTMLSelectElement;
    fireEvent.change(picker, { target: { value: picker.options[1]!.value } });
    // The one-click affordance: link the whole outstanding balance. A BUTTON the
    // operator presses, not a default.
    fireEvent.click(
      screen.getByLabelText(
        "Link the whole outstanding balance on line 1, link 1",
      ),
    );
    fireEvent.change(
      screen.getByLabelText("Quantity linked on line 1, link 1"),
      { target: { value: "8" } },
    );

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(async () => {
      book = await client.salesOrderBook(COMPANY);
      expect(book.rows[0]!.fulfilled_qty).toBe("8.0000");
    });
    expect(book.rows[0]!.outstanding_qty).toBe("12.0000");
  });

  it("⛔ an UNLINKED delivery of the same goods leaves the order untouched", async () => {
    // THE CONTROL THAT MAKES THE TEST ABOVE MEAN SOMETHING. An order book that
    // inferred fulfilment from stock movements — matching on item, say — would pass
    // that test and be wrong in the one way that matters: it would discharge
    // commitments nobody keyed. An order is a committed quantity and a movement is a
    // movement; only an explicit link joins them.
    const client = createMockClient();
    await seedSalesOrder(client);
    const app = renderShell(client);
    await openDeliveryNote(app, "8");

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(screen.getByTestId("stock-status").textContent).toMatch(/Saved/),
    );
    const book = await client.salesOrderBook(COMPANY);
    expect(book.rows[0]!.fulfilled_qty).toBe("0.0000");
    expect(book.rows[0]!.outstanding_qty).toBe("20.0000");
  });

  it("⛔ KEEPS AN OVER-DELIVERY SAVEABLE: 25 moved, 20 linked", async () => {
    // The case the whole no-pre-fill rule protects. The goods really left, so closing
    // stock must record 25; only 20 of it consumed an order, because only 20 were
    // ordered. A form that locked the link to the movement would refuse to save this
    // at all, and the operator would have no way to record what happened.
    const client = createMockClient();
    await seedSalesOrder(client, "SO-BIG", "20");
    const app = renderShell(client);
    await openDeliveryNote(app, "25");

    fireEvent.click(await screen.findByLabelText("Link line 1 to an order"));
    const picker = (await screen.findByLabelText(
      "Order for line 1, link 1",
    )) as HTMLSelectElement;
    fireEvent.change(picker, { target: { value: picker.options[1]!.value } });
    fireEvent.change(
      screen.getByLabelText("Quantity linked on line 1, link 1"),
      { target: { value: "20" } },
    );

    // The screen EXPLAINS the difference rather than calling it an error.
    const warning = screen.getByTestId("stock-fulfilment:0-warning");
    expect(warning.textContent).toMatch(/consumed no order/);
    expect(warning.textContent).not.toMatch(/refuse/);

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(screen.getByTestId("stock-status").textContent).toMatch(/Saved/),
    );
    // The order is fully discharged and NOT over-discharged.
    const book = await client.salesOrderBook(COMPANY);
    expect(book.rows).toHaveLength(0); // nothing outstanding -> excluded by design
    const order = await client.listSalesOrders(COMPANY, false);
    const full = await client.getSalesOrder(COMPANY, order.orders[0]!.order_id);
    expect(full.lines[0]!.fulfilled_qty).toBe("20.0000");
    expect(full.lines[0]!.outstanding_qty).toBe("0.0000");
  });

  it("the picker is ABSENT on a Stock Journal, which discharges nothing", async () => {
    // A transfer never leaves the business, so there is no commitment to discharge and
    // the column is absent rather than present-and-empty — an empty column invites the
    // question "why can I not link this?".
    const client = createMockClient();
    await seedSalesOrder(client);
    const app = renderShell(client);
    fireEvent.keyDown(app, { key: "F7", altKey: true });
    await screen.findByTestId("stock-grid");
    expect(screen.queryByText("Against order")).toBeNull();
    expect(screen.queryByTestId("stock-fulfilment:0")).toBeNull();
  });
});

describe("the ALTER round trip — the link must survive an unrelated edit", () => {
  it("⛔ altering the narration does NOT re-open the delivered order", async () => {
    // THE DEFECT THIS PINS, and it is data loss rather than a missing feature.
    // order_fulfilment keys on (voucher_version_id, line_no), so an ALTER RE-STATES
    // the links: whatever the body omits is gone. The voucher GET therefore has to
    // return them and the form has to re-hydrate from that — and if either half is
    // missing, fixing a typo silently re-opens an order the company has delivered.
    // Nothing refuses it, because zero links is a legal state.
    const client = createMockClient();
    await seedSalesOrder(client);

    // Post a linked delivery note the way the screen does.
    const orders = await client.listSalesOrders(COMPANY, true);
    const full = await client.getSalesOrder(COMPANY, orders.orders[0]!.order_id);
    const orderLineID = full.lines[0]!.order_line_id;
    const posted = await client.postDeliveryNoteVoucher(COMPANY, {
      voucher_date: "2026-06-10",
      narration: "first",
      created_by: "ui",
      inventory: [
        {
          stock_item_id: "stock-widget",
          godown_id: "",
          direction: "OUT",
          qty: "8.0000",
          rate: "500.0000",
          amount: "4000.0000",
        },
      ],
      fulfilments: [{ line_no: 1, order_line_id: orderLineID, qty: "8.0000" }],
    });

    // CONTROL: the link landed.
    let book = await client.salesOrderBook(COMPANY);
    expect(book.rows[0]!.fulfilled_qty).toBe("8.0000");

    // ⛔ THE READ-BACK. Without `fulfilments` on this body a form cannot restate them.
    const view = await client.getVoucher(COMPANY, posted.voucher_id);
    expect(
      view.fulfilments,
      "GET /vouchers/{id} must carry the order links, or an alter rebuilt from it " +
        "silently clears them",
    ).toBeDefined();
    expect(view.fulfilments).toHaveLength(1);
    expect(view.fulfilments![0]!.line_no).toBe(1);
    expect(view.fulfilments![0]!.qty).toBe("8.0000");
    // The DISPLAY fields, so a rebuilt form shows a number and not a uuid.
    expect(view.fulfilments![0]!.order_no).toBe("SO-1");
    expect(view.fulfilments![0]!.stock_item).toBe("Widget");

    // The round trip: restate from what the GET gave, changing only the narration.
    await client.alterDeliveryNoteVoucher(COMPANY, posted.voucher_id, {
      voucher_date: view.voucher_date,
      narration: "typo fixed",
      created_by: "ui",
      inventory: [
        {
          stock_item_id: "stock-widget",
          godown_id: "",
          direction: "OUT",
          qty: "8.0000",
          rate: "500.0000",
          amount: "4000.0000",
        },
      ],
      fulfilments: view.fulfilments!.map((f) => ({
        line_no: f.line_no,
        order_line_id: f.order_line_id,
        qty: f.qty,
      })),
    });
    book = await client.salesOrderBook(COMPANY);
    expect(
      book.rows[0]!.fulfilled_qty,
      "the link did not survive a round trip through the GET",
    ).toBe("8.0000");

    // ---- THE CONTROL: an alter that OMITS the links MUST re-open the order.
    // Without this, the assertion above would pass even if links were immune to an
    // alter — and this whole test would be inert while reading as a guard.
    await client.alterDeliveryNoteVoucher(COMPANY, posted.voucher_id, {
      voucher_date: view.voucher_date,
      narration: "links dropped",
      created_by: "ui",
      inventory: [
        {
          stock_item_id: "stock-widget",
          godown_id: "",
          direction: "OUT",
          qty: "8.0000",
          rate: "500.0000",
          amount: "4000.0000",
        },
      ],
    });
    book = await client.salesOrderBook(COMPANY);
    expect(
      book.rows[0]!.fulfilled_qty,
      "an alter that omits the links must CLEAR them — if it does not, the " +
        "round-trip assertion above proves nothing",
    ).toBe("0.0000");
  });
});
