import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient, ApiError } from "../api";
import type { CoredClient, InventoryLineRequest, VoucherRequest } from "../api";
import { FeaturesScreen } from "./FeaturesScreen";
import {
  buildStockVoucherRequest,
  refuseWrongWayGoods,
  stockAlterRefusal,
  stockShapeFor,
  type StockVoucherLine,
} from "./vouchers/stockVoucher";
import { configFor } from "./vouchers/voucherConfig";
import { defaultGodownOf } from "./vouchers/stockVoucher";

afterEach(cleanup);

/**
 * THE STOCK VOUCHER SCREENS — Alt+F8 Delivery Note, Alt+F9 Receipt Note,
 * Alt+F7 Stock Journal, Alt+F10 Physical Stock.
 *
 * These four documents move goods and post NOTHING to the ledger, which makes
 * the usual accounting assertions useless here: there are no debits to sum, no
 * balance to check and no tax to re-derive. Every property worth pinning is
 * therefore about the REQUEST SHAPE and the resulting stock POSITION —
 *
 *   - a body carrying postings is a 422, so "it saved" proves nothing; the
 *     absence of a money side has to be asserted on the request itself;
 *   - a Receipt Note with an outward line balances perfectly (there is nothing
 *     to balance) and removes stock the company just received;
 *   - a Stock Journal with two same-way lines is a phantom creation or a silent
 *     write-off, and no total anywhere disagrees with it;
 *   - a Physical Stock voucher that sent the DELTA instead of the COUNT would
 *     be differencing against a book it read earlier, and one that sent the
 *     count as a MOVEMENT would leave the books asserting 23 on a shelf a human
 *     just counted 13 of — while reporting success at every layer.
 */

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

/** Seed the stock masters these vouchers are keyed against. */
async function seedStock(base: CoredClient): Promise<void> {
  await base.createStockItem("co-1", {
    name: "Widget",
    unit_symbol: "Nos",
    hsn: "8481",
    gst_rate_pct: "18.0000",
  });
  await base.createStockItem("co-1", {
    name: "Gasket",
    unit_symbol: "Nos",
    hsn: "4016",
    gst_rate_pct: "18.0000",
  });
}

function spyClient(base: CoredClient): {
  client: CoredClient;
  spies: Record<string, ReturnType<typeof vi.fn>>;
} {
  const spies: Record<string, ReturnType<typeof vi.fn>> = {};
  const client = {} as Record<string, unknown>;
  for (const key of Object.keys(base) as (keyof CoredClient)[]) {
    const orig = base[key] as unknown as (...a: unknown[]) => unknown;
    const fn = vi.fn((...args: unknown[]) => orig.apply(base, args));
    spies[key] = fn;
    client[key] = fn;
  }
  return { client: client as unknown as CoredClient, spies };
}

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

/** The combos that open each stock voucher, per keymaps/tallyprime.ts. */
const OPEN_KEY: Record<string, { key: string; altKey: true }> = {
  "delivery-note": { key: "F8", altKey: true },
  "receipt-note": { key: "F9", altKey: true },
  "stock-journal": { key: "F7", altKey: true },
  "physical-stock": { key: "F10", altKey: true },
};

/**
 * Open a stock voucher screen through its REAL key, delivered to the document
 * the way the app delivers it — never to a located element. Two shipped bugs in
 * this project hid behind `locator.press`, which dispatches to the element
 * rather than through the shell's single root handler.
 */
async function openStockVoucher(
  client: CoredClient,
  type: keyof typeof OPEN_KEY,
): Promise<HTMLElement> {
  renderShell(client);
  const app = screen.getByRole("application");
  fireEvent.keyDown(app, OPEN_KEY[type]!);
  await screen.findByTestId("stock-voucher-screen");
  // The masters load asynchronously; the item picker is useless until they do.
  await waitFor(() => expect(screen.getByTestId("stock-item:0")).toBeTruthy());
  // ⛔ A DELIVERY NOTE NOW REQUIRES A CONSIGNEE (see buildStockVoucherRequest).
  // Keyed here rather than in each test because it is a PRECONDITION of keying
  // this class at all, not the subject of any test below — those are about where
  // the stock went. Without it the form refuses before any request is made, and
  // three tests whose subject is the movement failed on a rule they never meant
  // to exercise.
  if (type === "delivery-note") {
    fireEvent.change(screen.getByTestId("stock-party"), {
      target: { value: "Acme Traders" },
    });
  }
  return app;
}

/** Resolve a seeded stock item's id by name. */
async function itemID(base: CoredClient, name: string): Promise<string> {
  const { stock_items } = await base.listStockItems("co-1");
  return stock_items.find((s) => s.name === name)!.id;
}

/**
 * The seeded system godown ('Main Location'), which is WHERE STOCK ACTUALLY IS.
 *
 * ⛔ FIXTURES MUST PUT THEIR OPENING STOCK HERE, and that is not tidiness. Three
 * tests in this file used to receive their stock with no godown at all — the
 * NULL position — and then count or despatch through a grid that also defaulted
 * to NULL. Both sides agreed, so all three passed. The real app disagreed:
 * seed-demo puts every unit at Main Location while the grid defaulted to
 * "— not tracked —", so a Physical Stock count differenced against a book of
 * ZERO and posted a phantom surplus (measured in a browser 2026-08-07: counting
 * 13 took the item from 195 to 208 and left Main Location untouched).
 *
 * A fixture that stores its stock where the product never stores it cannot see
 * that class of defect at all.
 */
async function mainGodown(base: CoredClient): Promise<string> {
  const { godowns } = await base.listGodowns("co-1");
  return godowns.find((g) => g.is_system)!.id;
}

/** Key one grid row (adding it first when it is not row 0). */
async function keyRow(
  i: number,
  values: { item: string; qty: string; rate?: string; godown?: string; toGodown?: string },
): Promise<void> {
  if (i > 0) {
    fireEvent.click(screen.getByTestId("stock-add-line"));
    await screen.findByTestId(`stock-item:${i}`);
  }
  fireEvent.change(screen.getByTestId(`stock-item:${i}`), {
    target: { value: values.item },
  });
  if (values.godown !== undefined) {
    fireEvent.change(screen.getByTestId(`stock-godown:${i}`), {
      target: { value: values.godown },
    });
  }
  if (values.toGodown !== undefined) {
    fireEvent.change(screen.getByTestId(`stock-to-godown:${i}`), {
      target: { value: values.toGodown },
    });
  }
  fireEvent.change(screen.getByTestId(`stock-qty:${i}`), {
    target: { value: values.qty },
  });
  if (values.rate !== undefined) {
    fireEvent.change(screen.getByTestId(`stock-rate:${i}`), {
      target: { value: values.rate },
    });
  }
}

/** The closing quantity of one (item, godown) position, from the Stock Summary. */
async function closingQty(
  base: CoredClient,
  item: string,
  godownID: string,
): Promise<string> {
  const summary = await base.getStockSummary("co-1");
  const row = summary.rows.find(
    (r) => r.item === item && r.godown_id === godownID,
  );
  return row?.closing_qty ?? "absent";
}

// ---------------------------------------------------------------------------
// 1. The Delivery Note: two lines, all OUT, and NO money side at all
// ---------------------------------------------------------------------------

describe("Alt+F8 Delivery Note — goods leave, nothing is posted", () => {
  it("posts two OUTWARD stock lines and NO postings, no gst, no bill allocations", async () => {
    // THE MUTATION THIS TEST EXISTS FOR: make buildStockVoucherRequest emit a
    // party leg (a `postings` array) "so the document has an accounting side".
    // The server REFUSES it — a non-accounting class states no postings at all
    // — so the test asserts the REQUEST SHAPE, not merely that the save
    // succeeded: under the mutation the POST is still made, and only the body
    // says what went wrong.
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const { client, spies } = spyClient(base);
    const app = await openStockVoucher(client, "delivery-note");

    const widget = await itemID(base, "Widget");
    const gasket = await itemID(base, "Gasket");
    await keyRow(0, { item: widget, qty: "2", rate: "150.5000" });
    await keyRow(1, { item: gasket, qty: "3", rate: "99.9900" });

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.postDeliveryNoteVoucher).toHaveBeenCalledTimes(1),
    );
    const [, req] = spies.postDeliveryNoteVoucher!.mock.calls[0]! as [
      string,
      VoucherRequest,
    ];

    const inv = req.inventory as InventoryLineRequest[];
    expect(
      inv.length,
      "a two-line delivery note must post TWO stock movements — a grid that silently drops a line is an unrecorded despatch",
    ).toBe(2);
    expect(
      inv.map((l) => l.direction),
      "every line of a Delivery Note is OUTWARD; an inward line would ADD stock under a document that says the goods left",
    ).toEqual(["OUT", "OUT"]);
    expect(inv.map((l) => l.qty)).toEqual(["2.0000", "3.0000"]);

    expect(
      req.postings,
      "a Delivery Note posts NO accounting lines at all — the server refuses a body that states one, so a 'helpful' party leg makes every despatch a 422",
    ).toBeUndefined();
    expect(
      req.gst,
      "a Delivery Note carries no tax: GST is charged by the INVOICE that follows, and deriving it here would file the same supply twice",
    ).toBeUndefined();
    expect(req.bill_allocations).toBeUndefined();
    expect(
      req.physical_count,
      "only a Physical Stock voucher carries counted quantities",
    ).toBeUndefined();

    // The screen says so too, on the same screen the operator is looking at.
    expect(screen.getByTestId("stock-no-postings").textContent).toMatch(
      /No accounting entry/i,
    );
  });

  it("...and the stock really left: the closing position falls by what was despatched", async () => {
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const widget = await itemID(base, "Widget");
    const main = await mainGodown(base);
    // Receive 10 first, so the despatch is not a negative-stock question — AT
    // MAIN LOCATION, which is where a real company's stock sits (see mainGodown).
    await base.postReceiptNoteVoucher("co-1", {
      voucher_date: "2026-07-01",
      inventory: [
        {
          stock_item_id: widget,
          godown_id: main,
          direction: "IN",
          qty: "10.0000",
          rate: "100.0000",
        },
      ],
    });

    const app = await openStockVoucher(base, "delivery-note");
    // The godown is deliberately NOT keyed: the grid must default to the place
    // the goods are. Despatching from the NULL position instead would be a
    // negative-stock refusal at best, and at worst a second phantom position.
    await keyRow(0, { item: widget, qty: "4", rate: "100.0000" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByTestId("voucher-no");

    expect(await closingQty(base, "Widget", main)).toBe("6.0000");
    expect(
      await closingQty(base, "Widget", ""),
      "the despatch must come off the shelf that held the goods, not open a second untracked position",
    ).toBe("absent");
  });
});

// ---------------------------------------------------------------------------
// 2. The Receipt Note refuses an outward line before the wire
// ---------------------------------------------------------------------------

describe("Alt+F9 Receipt Note — goods arrive, and cannot be made to leave", () => {
  it("posts every line INWARD and offers the operator no direction to get wrong", async () => {
    // THE MUTATION: drop the fixed direction from STOCK_VOUCHER_SHAPES (make
    // receipt-note a "transfer", or flip its direction to OUT). An operator
    // could then ship goods out on a goods-inward document: the voucher saves,
    // nothing is unbalanced (there is nothing to balance), and the warehouse
    // is short by the quantity of every receipt ever keyed.
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const { client, spies } = spyClient(base);
    const app = await openStockVoucher(client, "receipt-note");

    const widget = await itemID(base, "Widget");
    await keyRow(0, { item: widget, qty: "7", rate: "88.8800" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(spies.postReceiptNoteVoucher).toHaveBeenCalledTimes(1),
    );
    const [, req] = spies.postReceiptNoteVoucher!.mock.calls[0]! as [
      string,
      VoucherRequest,
    ];
    expect(
      (req.inventory as InventoryLineRequest[]).map((l) => l.direction),
      "a Receipt Note is a goods-INWARD document; an OUT line takes stock the company just received back out of the warehouse",
    ).toEqual(["IN"]);

    // There is no direction control on this form at all. A fixed-direction
    // class that RENDERED one would be inviting the mistake and then refusing
    // it, which is worse than not offering the choice.
    expect(screen.queryByTestId("stock-direction:0")).toBeNull();
    expect(screen.queryByTestId("stock-to-godown:0")).toBeNull();
  });

  it("refuses an outward line BEFORE it reaches the server, by name", () => {
    // The builder emits the class's direction, so the reachable route to a
    // wrong-way line is the ALTER path, where the grid was rebuilt from a
    // voucher fetched BACK from the server and the directions came from stored
    // movements rather than from this class's shape. refuseWrongWayGoods checks
    // the FINISHED body, which is what covers both paths with one rule.
    const body: VoucherRequest = {
      inventory: [
        { stock_item_id: "s1", direction: "OUT", qty: "7.0000", rate: "88.8800" },
      ],
    };
    expect(() => refuseWrongWayGoods("receipt-note", body)).toThrow(
      /Receipt Note moves stock IN, not OUT/,
    );
    // ...and the mirror, so the rule is not simply "refuse everything".
    expect(() =>
      refuseWrongWayGoods("receipt-note", {
        inventory: [
          { stock_item_id: "s1", direction: "IN", qty: "7.0000", rate: "88.8800" },
        ],
      }),
    ).not.toThrow();
    expect(() => refuseWrongWayGoods("delivery-note", body)).not.toThrow();
  });

  it("refuses a money side on any of the four, with the reason", () => {
    const withPostings: VoucherRequest = {
      inventory: [{ stock_item_id: "s1", direction: "IN", qty: "1.0000" }],
      postings: [{ ledger_id: "l1", dr_cr: "DR", amount: "100.0000" }],
    };
    expect(() => refuseWrongWayGoods("receipt-note", withPostings)).toThrow(
      /posts no accounting lines at all/,
    );
    expect(() =>
      refuseWrongWayGoods("delivery-note", {
        inventory: [{ stock_item_id: "s1", direction: "OUT", qty: "1.0000" }],
        gst: { place_of_supply_state_code: "29" },
      }),
    ).toThrow(/carries no tax/);
  });
});

// ---------------------------------------------------------------------------
// 3. The Stock Journal is a TRANSFER: one OUT, one IN, holding unchanged
// ---------------------------------------------------------------------------

describe("Alt+F7 Stock Journal — a transfer, not a creation", () => {
  it("posts each keyed move as an OUT/IN PAIR and leaves the item total unchanged", async () => {
    // THE MUTATION: emit both legs in the same direction (make the IN leg "OUT",
    // or the OUT leg "IN"). The transfer then becomes a phantom stock creation
    // or a silent write-off of the moved quantity — and NOTHING else in the
    // system disagrees, because a stock journal has no total to check, no
    // postings to balance and no tax to re-derive.
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const widget = await itemID(base, "Widget");
    const { godowns } = await base.listGodowns("co-1");
    const main = godowns.find((g) => g.name === "Main Location")!.id;

    // Receive 10 into the UNTRACKED position first, so the transfer moves real
    // goods. (The mock seeds exactly one named location, so "untracked -> Main
    // Location" is the transfer available to it; both are ordinary positions to
    // the movement ledger, which is all this test needs.)
    await base.postReceiptNoteVoucher("co-1", {
      voucher_date: "2026-07-01",
      inventory: [
        { stock_item_id: widget, godown_id: "", direction: "IN", qty: "10.0000", rate: "100.0000" },
      ],
    });

    const { client, spies } = spyClient(base);
    const app = await openStockVoucher(client, "stock-journal");
    await keyRow(0, {
      item: widget,
      godown: "",
      toGodown: main,
      qty: "4",
      rate: "100.0000",
    });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(spies.postStockJournalVoucher).toHaveBeenCalledTimes(1),
    );
    const [, req] = spies.postStockJournalVoucher!.mock.calls[0]! as [
      string,
      VoucherRequest,
    ];
    const inv = req.inventory as InventoryLineRequest[];
    expect(
      inv.map((l) => l.direction),
      "a transfer is an OUT and an IN on the SAME voucher; two lines the same way create or destroy stock that nobody moved",
    ).toEqual(["OUT", "IN"]);
    expect(inv[0]!.godown_id, "the OUT leg leaves the FROM godown").toBe("");
    expect(inv[1]!.godown_id, "the IN leg arrives in the TO godown").toBe(main);
    expect(inv.map((l) => l.qty)).toEqual(["4.0000", "4.0000"]);
    expect(req.postings, "a Stock Journal posts no accounting lines").toBeUndefined();

    // THE PROPERTY, not the shape: the company still holds ten Widgets. Under
    // the mutation this reads 14.0000 (both legs IN) or 2.0000 (both OUT).
    const summary = await base.getStockSummary("co-1");
    const widgetTotal = summary.items.find((i) => i.item === "Widget")!;
    expect(
      widgetTotal.closing_qty,
      "a transfer moves goods between locations; the company's holding of the item cannot change",
    ).toBe("10.0000");
    // ...and it moved: six left where they were, four are in Main Location.
    expect(await closingQty(base, "Widget", "")).toBe("6.0000");
    expect(await closingQty(base, "Widget", main)).toBe("4.0000");
  });

  it("refuses a transfer whose From and To are the same place", () => {
    // Two movements that net to zero, written to the ledger, describing nothing
    // that happened. The operator meant to pick a destination and did not.
    const line: StockVoucherLine = {
      stockItemId: "s1",
      itemName: "Widget",
      godownId: "g1",
      toGodownId: "g1",
      batchId: "",
      // A transfer can carry no order link at all — see fulfilsDirection, and the
      // guard in buildStockVoucherRequest: one keyed transfer row becomes TWO
      // movements, so a link's line number could not be derived from the row.
      fulfilments: [],
      qty: "4",
      rate: "10",
      unit: "Nos",
    };
    expect(() =>
      buildStockVoucherRequest({ type: "stock-journal", lines: [line] }),
    ).toThrow(/From and To godowns are the same/);
  });

  /**
   * ⛔ A DELIVERY NOTE WITH NO CONSIGNEE — FOUND BY DRIVING D30, AND IT IS D60'S
   * CLASS ON THIS SCREEN.
   *
   * D30 restored Ctrl+P here. The key then reached the server and answered 422
   * on every delivery note in the demo books: `validateVoucherDoc` requires "the
   * other party" on every printable kind, and nothing on this side required it —
   * so the class D30 had just made printable could not print one document. All
   * four seeded delivery notes carry a NULL party_ledger_id.
   *
   * The refusal is scoped to `delivery-note` deliberately. Receipt Note, Stock
   * Journal and Physical Stock are NOT printable, and a stock journal has no
   * other party by construction — requiring one there would be this form
   * inventing a rule the printing layer does not state.
   */
  const deliverableLine = (): StockVoucherLine => ({
    stockItemId: "s1",
    itemName: "Widget",
    godownId: "g1",
    toGodownId: "",
    batchId: "",
    fulfilments: [],
    qty: "5",
    rate: "10",
    unit: "Nos",
  });

  it("⛔ refuses a Delivery Note with no consignee — it could never be printed", () => {
    expect(() =>
      buildStockVoucherRequest({ type: "delivery-note", lines: [deliverableLine()] }),
    ).toThrow(/consignee/i);
    expect(() =>
      buildStockVoucherRequest({ type: "delivery-note", lines: [deliverableLine()], party: "   " }),
    ).toThrow(/consignee/i);
  });

  it("names the CONSEQUENCE and quotes the server, so the refusal is actionable", () => {
    let msg = "";
    try {
      buildStockVoucherRequest({ type: "delivery-note", lines: [deliverableLine()] });
    } catch (e) {
      msg = e instanceof Error ? e.message : String(e);
    }
    expect(msg).toMatch(/cannot be printed at all/);
    expect(msg).toMatch(/goods in transit have no consignee/);
    expect(msg).toMatch(/Party A\/c Name/);
  });

  it("⛔ CONTROL: a Delivery Note WITH a consignee is built, and carries it", () => {
    const req = buildStockVoucherRequest({
      type: "delivery-note",
      lines: [deliverableLine()],
      party: "Acme Traders",
    });
    expect(req.party).toBe("Acme Traders");
    expect(req.inventory).toHaveLength(1);
  });

  it("⛔ CONTROL: the three NON-printable classes still need no party", () => {
    // A stock journal has no other party by construction; a receipt note is the
    // supplier's despatch document; a physical stock count has nobody on the
    // other side. If any of these starts throwing, the scope has leaked.
    const transfer: StockVoucherLine = { ...deliverableLine(), toGodownId: "g2" };
    expect(() =>
      buildStockVoucherRequest({ type: "stock-journal", lines: [transfer] }),
    ).not.toThrow();
    expect(() =>
      buildStockVoucherRequest({ type: "receipt-note", lines: [deliverableLine()] }),
    ).not.toThrow();
  });

  it("refuses a transfer whose Rate was left blank, instead of writing stock value off", () => {
    // ⛔ THE ROW IS "KEYED": isKeyedStockLine asks only for a positive quantity,
    // so an empty Rate box is a complete line as far as the grid is concerned,
    // and parseFixed4("") is 0n — so the wire carries rate "0.0000", a STATED
    // zero the server's RateUnstated backstop is blind to by design.
    //
    // What that cost, measured in a browser on 2026-08-07: transferring 10 units
    // carried at 889 took 8,890 off the Balance Sheet's Closing Stock line while
    // the quantity stayed at 75, under a caption reading "no value moves".
    const line: StockVoucherLine = {
      stockItemId: "s1",
      itemName: "Widget",
      godownId: "g1",
      toGodownId: "g2",
      batchId: "",
      fulfilments: [],
      qty: "10",
      rate: "",
      unit: "Nos",
    };
    expect(() =>
      buildStockVoucherRequest({ type: "stock-journal", lines: [line] }),
    ).toThrow(/state the Rate/i);

    // THE CONTROL: the same transfer with a rate must build normally, and both
    // legs must carry it. A check that refused every transfer would pass the
    // assertion above just as well.
    const priced = buildStockVoucherRequest({
      type: "stock-journal",
      lines: [{ ...line, rate: "889" }],
    });
    expect(priced.inventory?.map((l) => [l.direction, l.rate])).toEqual([
      ["OUT", "889.0000"],
      ["IN", "889.0000"],
    ]);
  });

  it("a free sample on a Receipt Note still arrives at 0.0000 — the rule is about TRANSFERS", () => {
    // ⛔ THE CONTROL THE SERVER'S READ-BACK AUDIT FORCED. The first draft refused a
    // zero-rate inward on every stock-only class, which would have made free
    // samples unkeyable — and cored accepts them deliberately, so the client would
    // have been refusing what the API allows (§7.4). Goods arriving from OUTSIDE
    // can genuinely cost nothing; goods coming off our own shelf cannot, because
    // the outward leg already valued them.
    const line: StockVoucherLine = {
      stockItemId: "s1",
      itemName: "Widget",
      godownId: "g1",
      toGodownId: "",
      batchId: "",
      fulfilments: [],
      qty: "4",
      rate: "",
      unit: "Nos",
    };
    const req = buildStockVoucherRequest({ type: "receipt-note", lines: [line] });
    expect(req.inventory?.[0]?.direction).toBe("IN");
    expect(req.inventory?.[0]?.rate).toBe("0.0000");
  });

  it("a Delivery Note with no rate still ships — an outward's rate is ignored by the valuer", () => {
    // The mirror of the case above, and it is the reason the refusal is scoped to
    // INWARD rather than to "any zero". An outward is consumed at the position's
    // running weighted-average cost; its stated rate moves no figure anybody
    // reads. Refusing it would block a warehouse from despatching goods it holds
    // because nobody had priced them yet — which is the entire point of the class.
    const line: StockVoucherLine = {
      stockItemId: "s1",
      itemName: "Widget",
      godownId: "g1",
      toGodownId: "",
      batchId: "",
      fulfilments: [],
      qty: "4",
      rate: "",
      unit: "Nos",
    };
    // ⚠️ `party` IS A PRECONDITION OF THIS CLASS NOW, not part of this test's
    // subject — which is that a blank RATE on an outward still ships, because
    // the valuer ignores an outward's rate.
    const req = buildStockVoucherRequest({
      type: "delivery-note",
      lines: [line],
      party: "Acme Traders",
    });
    expect(req.inventory).toHaveLength(1);
    expect(req.inventory?.[0]?.direction).toBe("OUT");
  });
});

// ---------------------------------------------------------------------------
// 4. Physical Stock sends the COUNT, never the delta
// ---------------------------------------------------------------------------

describe("Alt+F10 Physical Stock — the count is stated, the correction is derived", () => {
  it("sends physical_count with the COUNTED figure, and no inventory at all", async () => {
    // THE MUTATION: send the delta instead — i.e. put the number in
    // `inventory` as a movement (or difference it client-side against a book
    // the client read earlier). The wire field is asserted BY NAME so the two
    // shapes stay distinguishable, and the closing position is asserted too,
    // because that is where the confusion actually bites: 13 sent as a MOVEMENT
    // on top of the 10 already there leaves the books asserting 23 on a shelf a
    // human has just counted 13 of, with every layer reporting success.
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const widget = await itemID(base, "Widget");
    const main = await mainGodown(base);
    await base.postReceiptNoteVoucher("co-1", {
      voucher_date: "2026-07-01",
      inventory: [
        {
          stock_item_id: widget,
          godown_id: main,
          direction: "IN",
          qty: "10.0000",
          rate: "100.0000",
        },
      ],
    });

    const { client, spies } = spyClient(base);
    const app = await openStockVoucher(client, "physical-stock");
    // "There are 13 on the shelf." The rate is what the three EXTRA units are
    // carried at; a surplus at zero cost is refused, because it would write the
    // item's weighted average down.
    //
    // ⛔ THE GODOWN IS LEFT UNTOUCHED ON PURPOSE — that is the defect this test
    // now also guards. The count must be differenced against the shelf that
    // holds the 10, not against an empty NULL position that would turn a
    // stock-take into an invention of 13 new units.
    await keyRow(0, { item: widget, qty: "13", rate: "100.0000" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() =>
      expect(spies.postPhysicalStockVoucher).toHaveBeenCalledTimes(1),
    );
    const [, req] = spies.postPhysicalStockVoucher!.mock.calls[0]! as [
      string,
      VoucherRequest,
    ];
    expect(
      req.physical_count,
      "a Physical Stock voucher's body is `physical_count` — the field the server differences against the books",
    ).toBeDefined();
    expect(
      req.physical_count!.map((c) => c.qty),
      "the wire must carry the COUNT (13), not the correction (3): a client-computed delta is measured against a book the client read earlier, and a stock-take is exactly when two readers race",
    ).toEqual(["13.0000"]);
    expect(
      req.inventory,
      "a Physical Stock voucher states what was COUNTED, not what moved; 13 sent as a movement on top of 10 leaves the books asserting 23",
    ).toBeUndefined();

    // The count must name the position it was made at, or the server differences
    // it against the wrong shelf.
    expect(req.physical_count!.map((c) => c.godown_id)).toEqual([main]);

    // The consequence, which is what makes the two shapes distinguishable at
    // all: the closing position IS the count, by construction.
    expect(await closingQty(base, "Widget", main)).toBe("13.0000");
    expect(
      await closingQty(base, "Widget", ""),
      "a stock-take corrects the shelf it counted; it must not open a phantom untracked position beside it",
    ).toBe("absent");
  });

  it("a shortage is not negative stock: the position becomes exactly the count", async () => {
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const widget = await itemID(base, "Widget");
    const main = await mainGodown(base);
    await base.postReceiptNoteVoucher("co-1", {
      voucher_date: "2026-07-01",
      inventory: [
        {
          stock_item_id: widget,
          godown_id: main,
          direction: "IN",
          qty: "10.0000",
          rate: "100.0000",
        },
      ],
    });

    const app = await openStockVoucher(base, "physical-stock");
    // A shortage needs no rate: the outward is consumed at the position's own
    // running weighted-average cost and its stated rate is ignored.
    await keyRow(0, { item: widget, qty: "6" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByTestId("voucher-no");

    expect(
      await closingQty(base, "Widget", main),
      "the count is a FACT about the shelf, not a withdrawal request — it commits, and the position it leaves is the counted quantity",
    ).toBe("6.0000");
  });

  it("counts against the shelf the stock is ON, not against a phantom untracked position", async () => {
    // ⛔ THE DEFECT, MEASURED IN A BROWSER 2026-08-07, AND ITS SYMPTOM READS AS
    // SUCCESS. The Godown select defaulted to "— not tracked —" (NULL) while all
    // the stock sat at Main Location. Counting 13 without touching it differenced
    // 13 against a book of ZERO on the NULL position and posted a SURPLUS INWARD
    // of 13: the item went from 195 to 208, Main Location kept its 195, and the
    // operator — who counted 13 and now sees 13 — has no way to tell. A
    // stock-take is the control that makes the books match the shelf; this made
    // them disagree, and INVENTED stock while doing it.
    //
    // The assertion is on the CORRECTION, not on the total: a delta of -187
    // against 195 is a stock-take, a delta of +13 against 0 is an invention, and
    // only one of them is a document about the shelf that was counted.
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const widget = await itemID(base, "Widget");
    const main = await mainGodown(base);
    await base.postReceiptNoteVoucher("co-1", {
      voucher_date: "2026-07-01",
      inventory: [
        {
          stock_item_id: widget,
          godown_id: main,
          direction: "IN",
          qty: "195.0000",
          rate: "31.6008",
        },
      ],
    });

    const app = await openStockVoucher(base, "physical-stock");
    await keyRow(0, { item: widget, qty: "13", rate: "31.6008" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByTestId("voucher-no");

    expect(
      await closingQty(base, "Widget", main),
      "the counted shelf must END at the count: 13 is what the human saw",
    ).toBe("13.0000");
    expect(
      await closingQty(base, "Widget", ""),
      "a phantom position holding the count is the defect itself — the stock-take added 13 units instead of correcting 195 down to 13",
    ).toBe("absent");
  });

  it("leaves the operator the untracked position when the company keeps no godowns", () => {
    // THE CONTROL for the rule above (§9.3). "— not tracked —" is a REAL answer
    // for a company that never enabled locations, so the fix must default to a
    // godown only when there is one to default to — never invent a position, and
    // never force a choice that does not exist.
    expect(defaultGodownOf([])).toBe("");
    expect(defaultGodownOf([{ id: "g1", name: "Main Location", is_system: true }])).toBe("g1");
    expect(defaultGodownOf([{ id: "g9", name: "Shed", is_system: false }])).toBe("g9");
    // Two user godowns and no system one: there is nothing to infer, and
    // guessing would put the goods somewhere nobody chose.
    expect(
      defaultGodownOf([
        { id: "g1", name: "Shed", is_system: false },
        { id: "g2", name: "Yard", is_system: false },
      ]),
    ).toBe("");
  });

  it("states the REASON when the security level refuses it, rather than a blank form", async () => {
    // Migration 0031 explicitly disallows `create physical-stock` to the seeded
    // Data Entry level, so a 403 is a POLICY answer, not a fault. An operator
    // shown a silent no-op files a bug; an operator told which rule refused
    // them asks their Owner.
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const widget = await itemID(base, "Widget");
    const client: CoredClient = {
      ...base,
      postPhysicalStockVoucher: async () => {
        throw new ApiError(403, "forbidden by security level", {});
      },
    };
    const app = await openStockVoucher(client, "physical-stock");
    await keyRow(0, { item: widget, qty: "13", rate: "100.0000" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const denied = await screen.findByTestId("stock-denied");
    expect(denied.textContent).toMatch(/security level/i);
    expect(denied.textContent).toMatch(/physical-stock/);
  });

  it("refuses to ALTER a fetched physical stock voucher instead of corrupting it", () => {
    // GET /vouchers/{id} projects the DELTA movements — the correction, not the
    // count, which is not stored (the closing position immediately after the
    // voucher IS the count). Re-posting that form verbatim would restate "there
    // are 3" on a shelf holding 13. Refusing loudly beats an alter that
    // silently corrupts the position it exists to correct.
    const refusal = stockAlterRefusal("physical-stock");
    expect(
      refusal,
      "a fetched Physical Stock voucher carries the DELTA the server derived, not the count; allowing this alter restates 'there are 3' on a shelf holding 13",
    ).not.toBeNull();
    expect(refusal ?? "").toMatch(/cannot be altered here/);
    expect(refusal ?? "").toMatch(/correction/i);
    // The other three restate normally: their stored movements ARE what was
    // keyed, so there is nothing to reconstruct.
    expect(stockAlterRefusal("delivery-note")).toBeNull();
    expect(stockAlterRefusal("receipt-note")).toBeNull();
    expect(stockAlterRefusal("stock-journal")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// The config, and the F11 honesty surface
// ---------------------------------------------------------------------------

describe("the four classes are configured, not merely typed", () => {
  const types = [
    "delivery-note",
    "receipt-note",
    "stock-journal",
    "physical-stock",
  ] as const;

  it("each has a real config with a real post and alter", () => {
    for (const t of types) {
      const cfg = configFor(t);
      expect(cfg.type).toBe(t);
      expect(typeof cfg.post).toBe("function");
      expect(typeof cfg.alter).toBe("function");
      expect(
        cfg.showGst,
        `${t} must not surface a GST panel: the server refuses a gst block on it`,
      ).toBe(false);
      expect(cfg.itemInvoice).toBe(false);
    }
  });

  it("the two fixed-direction classes are genuinely OPPOSITE", () => {
    // A guard on the guard: if someone gave both notes one hardcoded direction,
    // each class's own test would still assert its own expected value on a
    // one-line voucher. Pin that the SHAPES actually differ.
    const dn = stockShapeFor("delivery-note");
    const rn = stockShapeFor("receipt-note");
    expect(dn.kind).toBe("fixed-direction");
    expect(rn.kind).toBe("fixed-direction");
    if (dn.kind === "fixed-direction" && rn.kind === "fixed-direction") {
      expect(dn.direction).not.toBe(rn.direction);
      expect(dn.direction).toBe("OUT");
    }
  });

  it("each post method routes to its OWN class endpoint", async () => {
    // The configs are four near-identical objects, and a copy-paste that left
    // receipt-note calling postDeliveryNoteVoucher would post goods-in
    // documents as goods-out ones — which saves, and reverses the warehouse.
    const base = createMockClient({ seed: false });
    const { client, spies } = spyClient(base);
    const body: VoucherRequest = { inventory: [] };
    const expected: [(typeof types)[number], string][] = [
      ["delivery-note", "postDeliveryNoteVoucher"],
      ["receipt-note", "postReceiptNoteVoucher"],
      ["stock-journal", "postStockJournalVoucher"],
      ["physical-stock", "postPhysicalStockVoucher"],
    ];
    for (const [t, method] of expected) {
      await configFor(t).post(client, "co-1", body).catch(() => undefined);
      expect(spies[method], `${t} must post through ${method}`).toHaveBeenCalled();
    }
  });
});

describe("F11 Company Features tells the truth about the stock vouchers", () => {
  function renderFeatures() {
    const client = createMockClient({ seed: false });
    return render(
      <CompanyProvider
        client={client}
        initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
      >
        <FeaturesScreen />
      </CompanyProvider>,
    );
  }

  function badgeFor(namePattern: RegExp): string {
    const cell = screen.getByText(namePattern);
    const row = cell.closest("tr");
    return row?.firstElementChild?.textContent?.trim() ?? "";
  }

  it("the inventory-voucher row reads 'On', not 'Not built'", () => {
    // THE MUTATION: ship the four screens and leave the row saying "Not built".
    // An honesty surface that lags the product is worse than no surface — it
    // teaches people not to trust it — so the claim is pinned by a test in the
    // SAME change that makes it true.
    renderFeatures();
    expect(
      badgeFor(/^Inventory vouchers \(Delivery Note/),
      "F11 must say the inventory vouchers are On in the same change that gives an operator a screen to key one",
    ).toBe("On");
  });

  it("...and its note no longer says an operator cannot key one", () => {
    renderFeatures();
    const row = screen.getByText(/^Inventory vouchers \(Delivery Note/).closest("tr")!;
    const note = row.lastElementChild?.textContent ?? "";
    expect(
      note,
      "the note claimed 'there is no voucher screen ... so you cannot key one'; that sentence is now false and must be gone",
    ).not.toMatch(/cannot key one|no voucher screen/i);
    expect(note, "the keys that reach it must be named").toMatch(/Alt\+F8/);
    // The refusal is part of the truth, not a footnote: the seeded Data Entry
    // level cannot key a Physical Stock.
    expect(note).toMatch(/Data Entry/);
  });

  it("still says NOT BUILT for what is not built", () => {
    // The other half of honesty. If every row said "On" the screen would be
    // decoration.
    //
    // ⚠️ ORDER PROCESSING USED TO BE ASSERTED HERE and it has MOVED to the positive
    // test below. That is this assertion working as intended rather than being
    // weakened: the row was "Not built" while its whole backend shipped, and it flipped
    // in the change that finally gave an operator a screen (Ctrl+F8 / Ctrl+F9, the BOM
    // screen and both order books).
    // ⚠️ FIFO / LIFO MOVED TO THE POSITIVE TEST TOO (T9: a costing-method picker
    // on the master form and Alt+V on the Stock Summary), and now STOCK GROUPS AND
    // COMPOUND UNITS has as well — T10 gives an operator Alt+G > Create Stock Group
    // and a base unit + conversion factor on Alt+G > Create Unit, and the flip is
    // pinned by its own test in VoucherScreen.itemInvoice.test.tsx.
    //
    // ⛔ THERE IS NO "Not built" ROW LEFT UNDER INVENTORY AT ALL — all ten read On —
    // so this assertion had to leave the group rather than be deleted. It is now
    // pointed at the nearest genuinely absent thing to a DOCUMENT this file keys:
    // a delivery note prints (Ctrl+P), and there is still no way to E-MAIL it.
    //
    // ⚠️ THIS ASSERTION HAS NOW BEEN RE-POINTED TWICE, AND THAT IS IT WORKING.
    // It named order processing, then FIFO/LIFO, then stock groups, then
    // "Export: e-mail a document, and export to PDF" — each time because the row
    // it named FLIPPED in the change that finally gave an operator a screen or a
    // key. B1 split the joint export row; B2 then shipped the e-mail half as
    // Alt+M (it saves the PDF and opens the operator's own mail client), so that
    // row now reads On and this assertion had to move again.
    //
    // ⛔ IT IS NOW POINTED AT THE Rule 48(4) QR CODE, chosen deliberately over the
    // other six absent rows: it is the nearest genuinely absent thing to a printed
    // DOCUMENT, which is what this file keys — a delivery note prints, and what a
    // registered e-invoice must also carry is a QR code that this build cannot
    // produce. It is also blocked on a HUMAN (IRP sandbox credentials, HANDOFF.md
    // §10 item 4) rather than on work, so it will not flip out from under this
    // test the way three screen rows already have.
    //
    // ⛔ WHOEVER MAKES THAT ROW TRUE MUST RE-POINT THIS ASSERTION IN THE SAME
    // CHANGE. Find another genuinely absent row rather than deleting the test, and
    // keep the plural property below.
    renderFeatures();
    expect(badgeFor(/^Rule 48\(4\) QR code/)).toBe("Not built");
    // And the screen-wide property, so this cannot pass vacuously the day that
    // row is built: F11 still carries real gaps, plural.
    expect(screen.getAllByText("Not built").length).toBeGreaterThan(1);
  });

  it("says ON for order processing, and ONLY because a human can now reach it", () => {
    // ⛔ THE RULE THIS PINS, from FeaturesScreen's own doc comment: a row flips in the
    // change that gives the operator a SCREEN, not the change that gave them a route.
    // T3's tables, fulfilment links, over-fulfilment trigger, both order books, ACL
    // migration and fourteen client methods all shipped while this row correctly said
    // "Not built" — because a keyboard-only operator could not create an order at all.
    renderFeatures();
    expect(badgeFor(/^Order processing \(Sales \/ Purchase Orders\)/)).toBe("On");
    const note =
      screen
        .getByText(/^Order processing \(Sales \/ Purchase Orders\)/)
        .closest("tr")?.textContent ?? "";
    // The KEYS must be named, or the row claims a feature with no door.
    expect(note).toMatch(/Ctrl\+F8/);
    expect(note).toMatch(/Ctrl\+F9/);
    // And the gaps stay on the row: this is not a victory lap.
    expect(note).toMatch(/SINGLE-LEVEL/);
    expect(note).toMatch(/WHAT IS NOT HERE/);
  });
});
