import { describe, it, expect } from "vitest";
import { createMockClient } from "./mock";
import { ApiError } from "./http";

/**
 * orders.test.ts — DOES THE MOCK REFUSE WHAT THE SERVER REFUSES?
 *
 * ⛔⛔ THIS FILE EXISTS BECAUSE THE ANSWER WAS "NO" TWICE IN ONE SESSION, AND 597
 * GREEN TESTS SAT ON TOP OF A CREATE PATH THAT COULD NOT WORK IN A BROWSER.
 *
 * The two failures, both from T2, are worth restating because they are different
 * shapes and only one of them is catchable by a type:
 *
 *   1. A REFUSAL the mock did not make — the server 400s a missing `created_by`
 *      and the mock returned 201. A screen was built, tested and shipped against
 *      a create that always succeeded.
 *   2. A SHAPE the mock got wrong — the server nests a view under `cost_centre`
 *      and may send it null; the mock returned it flat, and `rest.ts` typed the
 *      envelope AS the flat view. That is a lie the compiler CANNOT catch, so
 *      every field read `undefined` at runtime while `tsc` was clean.
 *
 * So these tests assert the mock's REFUSALS and its ARITHMETIC, not its happy
 * paths — the happy paths are what a screen test exercises anyway. Each expectation
 * is diffed against the Go handler, not against the type I would like to exist.
 *
 * ⚠️ THE STATUS CODES ARE PART OF THE CONTRACT. A 409 tells an operator "pick
 * another name" and a 422 tells them "the books refuse this"; a screen that
 * rendered both as "something went wrong" would make the difference invisible, so
 * the codes are asserted rather than just the failure.
 */

async function company(client: ReturnType<typeof createMockClient>) {
  const { company_id } = await client.createCompany({
    name: "Order Co",
    fy_start_date: "2026-04-01",
    base_currency: "INR",
  });
  return company_id;
}

const line = (item = "item-1", qty = "20") => ({
  stock_item_id: item,
  qty,
  rate: "500",
  due_date: "2026-06-30",
});

const order = (no: string, ...lines: ReturnType<typeof line>[]) => ({
  order_no: no,
  order_date: "2026-06-01",
  party_ledger_id: "ledger-1",
  created_by: "owner",
  lines: lines.length > 0 ? lines : [line()],
});

describe("the mock order API refuses what the server refuses", () => {
  it("accepts a well-formed sales order — THE CONTROL for every refusal below", async () => {
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    const res = await c.createSalesOrder(cid, order("SO-1"));
    expect(typeof res.order_id).toBe("string");
    expect(res.order_id.length).toBeGreaterThan(0);
  });

  it("refuses a duplicate order number in the same direction, case-insensitively", async () => {
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    await c.createSalesOrder(cid, order("SO-1"));
    // "SO-1" and "so-1" are one order to an operator, and two of them is how half a
    // commitment goes missing while both halves look complete.
    await expect(c.createSalesOrder(cid, order("so-1"))).rejects.toMatchObject({
      status: 409,
    });
  });

  it("ALLOWS a purchase order to share a sales order's number", async () => {
    // ⛔ The mirror of the test above, and it must not be "tidied" into consistency:
    // uniqueness is per DIRECTION because a business numbers its own sales orders and
    // receives supplier numbers it does not control. SO-1 colliding with PO-1 is not
    // a collision an operator would accept being told about.
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    await c.createSalesOrder(cid, order("SO-1"));
    const res = await c.createPurchaseOrder(cid, order("SO-1"));
    expect(res.order_id.length).toBeGreaterThan(0);
  });

  it("refuses an order with no lines, no party, or a zero quantity", async () => {
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    await expect(
      c.createSalesOrder(cid, { ...order("SO-A"), lines: [] }),
    ).rejects.toMatchObject({ status: 422 });
    await expect(
      c.createSalesOrder(cid, { ...order("SO-B"), party_ledger_id: "" }),
    ).rejects.toMatchObject({ status: 422 });
    await expect(
      c.createSalesOrder(cid, order("SO-C", line("item-1", "0"))),
    ).rejects.toMatchObject({ status: 422 });
  });

  it("⛔ will not serve a purchase order through the sales route, or the reverse", async () => {
    // THE MOST IMPORTANT TEST IN THIS FILE. Both directions live in ONE table on the
    // server and routeAction admits a caller to /sales-orders/{any-id}, so the
    // handler refuses an id of the other direction — otherwise a caller holding only
    // `sales-order` reads the company's purchase commitments, with the PDP having
    // said yes to the wrong object. A mock that served it would let a screen be built
    // against a leak.
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    const sales = await c.createSalesOrder(cid, order("SO-1"));
    const purch = await c.createPurchaseOrder(cid, order("PO-1"));

    // CONTROLS: each id reads from its own route.
    await expect(c.getSalesOrder(cid, sales.order_id)).resolves.toBeTruthy();
    await expect(c.getPurchaseOrder(cid, purch.order_id)).resolves.toBeTruthy();

    // A 404 and NOT a 403: "forbidden" would confirm the id names something real.
    await expect(c.getSalesOrder(cid, purch.order_id)).rejects.toMatchObject({
      status: 404,
    });
    await expect(c.getPurchaseOrder(cid, sales.order_id)).rejects.toMatchObject({
      status: 404,
    });
    // And on the WRITE, which would be worse: a salesperson abandoning a purchase
    // commitment.
    await expect(
      c.closeSalesOrder(cid, purch.order_id, { is_closed: true, reason: "x" }),
    ).rejects.toMatchObject({ status: 404 });
  });

  it("refuses closing an order with no reason, and clears the reason on re-opening", async () => {
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    const { order_id } = await c.createSalesOrder(cid, order("SO-1"));

    await expect(
      c.closeSalesOrder(cid, order_id, { is_closed: true }),
    ).rejects.toMatchObject({ status: 422 });

    await c.closeSalesOrder(cid, order_id, {
      is_closed: true,
      reason: "the customer cancelled",
    });
    let view = await c.getSalesOrder(cid, order_id);
    expect(view.is_closed).toBe(true);
    expect(view.closed_reason).toBe("the customer cancelled");

    // Reversible, and the reason goes WITH the flag — the server refuses a reason on
    // an open order, because a reason attached to something that has not happened is
    // a claim about nothing.
    await c.closeSalesOrder(cid, order_id, { is_closed: false });
    view = await c.getSalesOrder(cid, order_id);
    expect(view.is_closed).toBe(false);
    expect(view.closed_reason).toBe("");
  });

  it("serves fulfilled AND outstanding on every line, and both as fixed-4 strings", async () => {
    // Both, never one: an operator's question is not "how much is left" alone but
    // "how much of what we promised" — 5 outstanding reads completely differently
    // against a commitment of 6 than against 500. And fixed-4 STRINGS, because a
    // JSON number is a float64 and a commitment is not a thing to send through one.
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    const { order_id } = await c.createSalesOrder(cid, order("SO-1"));
    const view = await c.getSalesOrder(cid, order_id);
    expect(view.lines).toHaveLength(1);
    expect(view.lines[0]!.qty).toBe("20.0000");
    expect(view.lines[0]!.fulfilled_qty).toBe("0.0000");
    expect(view.lines[0]!.outstanding_qty).toBe("20.0000");
    expect(view.lines[0]!.rate).toBe("500.0000");
  });

  it("⛔ resolves DISPLAY NAMES on the way back, and never echoes the id as the name", async () => {
    // THE THIRD OCCURRENCE OF T2'S SHAPE DEFECT, caught the same way as the first
    // two — by building a screen against the mock and looking at it.
    //
    // `party` and `stock_item` returned the ID that was sent in. Both fields are
    // `string`, so tsc is clean and every assertion about quantities and refusals
    // stays green; what breaks is only visible to an operator, who is shown a UUID
    // where the register should say "Acme Traders". The server LEFT JOINs the master
    // and returns COALESCE(name, '') for each (internal/orders/bom.go:242 and the
    // order queries beside it), so this is what the wire actually does.
    //
    // ⚠️ THE CONTROL IS THE SECOND HALF: an id that resolves to NOTHING must come
    // back as "", not as the id. Falling back to the id would put the UUID on the
    // screen in exactly the case an operator can least explain, and it would make
    // the first half of this test pass while the defect survived for unknown
    // masters.
    const c = createMockClient();
    const seeded = "company-demo";
    const { order_id } = await c.createSalesOrder(seeded, {
      order_no: "SO-NAMES",
      order_date: "2026-06-01",
      party_ledger_id: "ledger-acme",
      created_by: "owner",
      lines: [{ stock_item_id: "stock-widget", qty: "2" }],
    });
    const view = await c.getSalesOrder(seeded, order_id);
    expect(view.party).toBe("Acme Traders");
    expect(view.party).not.toBe("ledger-acme");
    expect(view.lines[0]!.stock_item).toBe("Widget");
    expect(view.lines[0]!.stock_item).not.toBe("stock-widget");
    // The BOM side, same shape, same server join.
    const { bom_id } = await c.createBom(seeded, {
      stock_item_id: "stock-widget",
      name: "Standard",
      output_qty: "3",
      components: [{ stock_item_id: "stock-gasket", qty: "10" }],
      created_by: "owner",
    });
    const bom = await c.getBom(seeded, bom_id);
    expect(bom.stock_item).toBe("Widget");
    expect(bom.components[0]!.stock_item).toBe("Gasket");

    // THE CONTROL: an unknown master resolves to "", never to the id.
    const unknown = await c.createSalesOrder(seeded, {
      order_no: "SO-UNKNOWN",
      order_date: "2026-06-01",
      party_ledger_id: "ledger-does-not-exist",
      created_by: "owner",
      lines: [{ stock_item_id: "stock-does-not-exist", qty: "1" }],
    });
    const orphan = await c.getSalesOrder(seeded, unknown.order_id);
    expect(orphan.party).toBe("");
    expect(orphan.lines[0]!.stock_item).toBe("");
  });

  it("drops closed orders from the order book and from open_only, and echoes the filter", async () => {
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    const open = await c.createSalesOrder(cid, order("SO-OPEN"));
    const shut = await c.createSalesOrder(cid, order("SO-SHUT"));

    // CONTROL: both are on the book before either is closed.
    let book = await c.salesOrderBook(cid, "2026-07-31");
    expect(book.rows).toHaveLength(2);
    expect(book.total_outstanding_value).toBe("20000.0000"); // 2 x 20 x 500

    await c.closeSalesOrder(cid, shut.order_id, {
      is_closed: true,
      reason: "cancelled",
    });
    book = await c.salesOrderBook(cid, "2026-07-31");
    expect(book.rows).toHaveLength(1);
    expect(book.rows[0]!.order_id).toBe(open.order_id);
    expect(book.total_outstanding_value).toBe("10000.0000");

    const list = await c.listSalesOrders(cid, true);
    expect(list.orders).toHaveLength(1);
    // Echoed, so a screen cannot render a filtered list under an unfiltered heading.
    expect(list.open_only).toBe(true);
    // The list carries NO lines, matching the server: loading every line of every
    // order to render a picker is how a list of a thousand becomes a timeout.
    expect("lines" in list.orders[0]!).toBe(false);
  });

  it("marks a line overdue only when a due date EXISTS and has passed", async () => {
    // ⚠️ A line with no agreed due date is NEVER overdue, however old. Treating an
    // absent date as today's would invent a commitment the business never made, and
    // a salesperson would chase nothing.
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    await c.createSalesOrder(cid, {
      ...order("SO-1"),
      lines: [
        { stock_item_id: "item-1", qty: "5", rate: "10", due_date: "2026-06-30" },
        { stock_item_id: "item-2", qty: "5", rate: "10" }, // no date agreed
      ],
    });
    const book = await c.salesOrderBook(cid, "2026-07-31");
    expect(book.rows).toHaveLength(2);
    const dated = book.rows.find((r) => r.due_date === "2026-06-30");
    const undated = book.rows.find((r) => r.due_date === "");
    expect(dated?.is_overdue).toBe(true);
    expect(undated?.is_overdue).toBe(false);
  });
});

describe("the mock BOM API matches the server's refusals and its arithmetic", () => {
  const recipe = (over: Record<string, unknown> = {}) => ({
    stock_item_id: "table",
    name: "Standard",
    output_qty: "3",
    created_by: "owner",
    components: [{ stock_item_id: "leg", qty: "10" }],
    ...over,
  });

  it("accepts a well-formed recipe — THE CONTROL", async () => {
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    const res = await c.createBom(cid, recipe());
    expect(res.bom_id.length).toBeGreaterThan(0);
  });

  it("refuses an item as a component of its own recipe", async () => {
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    await expect(
      c.createBom(
        cid,
        recipe({
          components: [
            { stock_item_id: "leg", qty: "4" },
            { stock_item_id: "table", qty: "1" },
          ],
        }),
      ),
    ).rejects.toMatchObject({ status: 422 });
  });

  it("refuses a second DEFAULT recipe for one item but allows a non-default alternative", async () => {
    // Two defaults is a silent coin flip over which recipe a manufacture used, and
    // the two consume different materials.
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    await c.createBom(cid, recipe({ is_default: true }));
    await expect(
      c.createBom(cid, recipe({ name: "Substitute", is_default: true })),
    ).rejects.toMatchObject({ status: 409 });
    await expect(
      c.createBom(cid, recipe({ name: "Substitute" })),
    ).resolves.toBeTruthy();
  });

  it("refuses a duplicate recipe name on the same item, and a zero yield", async () => {
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    await c.createBom(cid, recipe());
    await expect(c.createBom(cid, recipe())).rejects.toMatchObject({ status: 409 });
    await expect(
      c.createBom(cid, recipe({ name: "Zero", output_qty: "0" })),
    ).rejects.toMatchObject({ status: 422 });
  });

  it("⛔ refuses an all-zeros stock_item_id filter rather than treating it as absent", async () => {
    // uuid.Nil is the server's "no filter" sentinel, so an explicit zero would
    // silently return EVERY recipe with a 200 and a truthful-looking body. That exact
    // defect shipped in T2's cost-centre filter.
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    await c.createBom(cid, recipe());
    // CONTROL: omitting the filter lists it.
    expect((await c.listBoms(cid)).boms).toHaveLength(1);
    await expect(
      c.listBoms(cid, "00000000-0000-0000-0000-000000000000"),
    ).rejects.toMatchObject({ status: 400 });
  });

  it("⛔ explodes by multiplying BEFORE dividing, matching orders.ScaleComponent", async () => {
    // THE ARITHMETIC IS PART OF THE CONTRACT. A screen developed against a mock that
    // rounded differently would show one number here and another in production.
    //
    // The cases are chosen to tell the two implementations apart: a WHOLE-MULTIPLE
    // batch is exact under both `qty*wanted/output` and the wrong
    // `qty*(wanted/output)`, so only the non-representable ratios can see the
    // difference. 10-makes-3 exploded for ONE is 3.3333, and 3.3330 is what
    // computing the ratio first produces — short by a tenth of a gram, every batch,
    // always downward, accumulating in closing stock as phantom material.
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    const { bom_id } = await c.createBom(cid, recipe());

    const one = await c.explodeBom(cid, bom_id, "1");
    expect(one.components[0]!.qty).toBe("3.3333");

    const seven = await c.explodeBom(cid, bom_id, "7");
    expect(seven.components[0]!.qty).toBe("23.3333");

    // THE CONTROLS: exact under any implementation, so they prove the fixture rather
    // than the arithmetic.
    expect((await c.explodeBom(cid, bom_id, "3")).components[0]!.qty).toBe("10.0000");
    expect((await c.explodeBom(cid, bom_id, "6")).components[0]!.qty).toBe("20.0000");

    // The yield is echoed so a screen can show the ratio it scaled by, rather than
    // leaving an operator to wonder why 3 units consumed 10 kg.
    expect(one.output_qty).toBe("3.0000");
    expect(one.wanted_qty).toBe("1.0000");
    // And the notes must state that the explosion is SINGLE-LEVEL and that nothing
    // has moved — a screen that implied otherwise would have an operator issuing
    // components they do not hold.
    expect(one.notes.join(" ")).toContain("SINGLE-LEVEL");
    expect(one.notes.join(" ").toLowerCase()).toContain("nothing has moved");
  });

  it("requires qty on an explosion rather than defaulting to 1", async () => {
    // Guessing would answer a question about a different batch size.
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    const { bom_id } = await c.createBom(cid, recipe());
    await expect(c.explodeBom(cid, bom_id, "")).rejects.toMatchObject({ status: 400 });
    await expect(c.explodeBom(cid, bom_id, "0")).rejects.toMatchObject({ status: 422 });
  });

  it("404s an unknown bom id, as an ApiError a caller can branch on", async () => {
    const c = createMockClient({ seed: false });
    const cid = await company(c);
    const err = await c.getBom(cid, "bom-nope").catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(404);
  });
});
