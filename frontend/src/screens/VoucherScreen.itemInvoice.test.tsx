import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient, ApiError } from "../api";
import type {
  CoredClient,
  GSTRequest,
  InventoryLineRequest,
  PostingRequest,
  StockSummaryResponse,
  VoucherRequest,
} from "../api";
import {
  _resetOutputTaxLedgerCache,
  _resetLedgerCache,
} from "./vouchers/gstVoucher";
import {
  lineAmount,
  mulFixed4,
  parseFixed4,
  formatFixed4,
  taxableValueOf,
} from "./vouchers/itemInvoice";
import { StockSummaryScreen } from "./reports/StockSummaryScreen";
import { FeaturesScreen } from "./FeaturesScreen";
import type { NavSnapshot } from "../keystroke/navMachine";

afterEach(cleanup);
beforeEach(() => {
  _resetOutputTaxLedgerCache();
  _resetLedgerCache();
});

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

/** Seed the party ledgers and stock masters an item invoice is keyed against. */
async function seedBooks(base: CoredClient): Promise<void> {
  await base.createLedger("co-1", {
    name: "Acme Traders",
    group_name: "Sundry Debtors",
    state_code: "29", // same state as home -> intra-state (CGST + SGST)
  });
  await base.createLedger("co-1", {
    name: "Zenith Supplies",
    group_name: "Sundry Creditors",
    state_code: "29",
  });
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

/**
 * Resolve a ledger uuid the request names back to its NAME.
 *
 * THIS IS THE POINT OF THE WHOLE FILE. Ledgers travel as uuids, so an assertion
 * phrased in ids — or in counts of debits and credits — cannot tell a purchase
 * from a reversed purchase: both are two debits and two credits summing to the
 * same total. The last time this project reversed a voucher's orientation the
 * change passed 317 of 317 tests, because the assertion counted sides on a
 * ONE-LINE voucher. Only naming the ledgers fails.
 */
async function ledgerName(base: CoredClient, id: string | undefined): Promise<string> {
  const { ledgers } = await base.listLedgers("co-1");
  return ledgers.find((l) => l.id === id)?.name ?? `<unknown ${id}>`;
}

/**
 * The postings of a SAVED item invoice, resolved to (name, side, amount).
 *
 * Read back from the stored voucher rather than from the request, because on an
 * item invoice the request HAS no postings: the server generates them from the
 * goods and refuses a body that also states them. So the orientation this
 * asserts is the one that actually reached the books.
 */
async function storedPostingsByName(
  base: CoredClient,
  voucherID: string,
): Promise<{ name: string; side: string; amount: string }[]> {
  const view = await base.getVoucher("co-1", voucherID);
  return view.postings.map((p) => ({
    name: p.ledger,
    side: String(p.dr_cr).toUpperCase().startsWith("D") ? "DR" : "CR",
    amount: p.amount,
  }));
}

/** Drive the shell into a two-line item invoice and press Ctrl+A. */
async function keyTwoLineInvoice(
  fKey: "F8" | "F9",
  party: string,
): Promise<{ base: CoredClient; spies: Record<string, ReturnType<typeof vi.fn>> }> {
  const base = createMockClient({ seed: false });
  await seedBooks(base);
  const { client, spies } = spyClient(base);
  renderShell(client);
  const app = screen.getByRole("application");
  fireEvent.keyDown(app, { key: fKey });

  // Ctrl+H > Item Invoice. Keys go to the DOCUMENT, the way the real app
  // delivers them, never to a located element.
  fireEvent.keyDown(app, { key: "h", ctrlKey: true });
  fireEvent.mouseDown(await screen.findByText("Item Invoice"));
  await screen.findByTestId("item-invoice-panel");

  fireEvent.mouseDown(await screen.findByText(party));

  const items = await base.listStockItems("co-1");
  const widget = items.stock_items.find((s) => s.name === "Widget")!;
  const gasket = items.stock_items.find((s) => s.name === "Gasket")!;

  // Line 1: 2 @ 150.5000 = 301.0000
  fireEvent.change(screen.getByTestId("item-select:0"), {
    target: { value: widget.id },
  });
  fireEvent.change(screen.getByTestId("item-qty:0"), { target: { value: "2" } });
  fireEvent.change(screen.getByTestId("item-rate:0"), {
    target: { value: "150.5000" },
  });

  // Line 2: 3 @ 99.9900 = 299.9700
  fireEvent.click(screen.getByTestId("item-add-line"));
  fireEvent.change(await screen.findByTestId("item-select:1"), {
    target: { value: gasket.id },
  });
  fireEvent.change(screen.getByTestId("item-qty:1"), { target: { value: "3" } });
  fireEvent.change(screen.getByTestId("item-rate:1"), {
    target: { value: "99.9900" },
  });

  fireEvent.keyDown(app, { key: "a", ctrlKey: true });
  return { base, spies };
}

// ---------------------------------------------------------------------------
// The two-line item invoice, resolved BY LEDGER NAME
// ---------------------------------------------------------------------------

describe("Item Invoice (Ctrl+H) posts goods, and the accounting derived from them", () => {
  it("a SALES item invoice sends the grid + the two ledgers, and NO amounts", async () => {
    const { base, spies } = await keyTwoLineInvoice("F8", "Acme Traders");

    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const [, req] = spies.postSalesVoucher!.mock.calls[0]! as [string, VoucherRequest];

    // 1. The grid travelled. Two lines, OUT (goods leave on a sale), positive
    //    quantities with the direction carrying the sign.
    const inv = req.inventory as InventoryLineRequest[];
    expect(
      inv.length,
      "a two-line item invoice must post TWO stock movements — a grid that silently drops a line is an inventory write-off",
    ).toBe(2);
    expect(inv.map((l) => l.direction)).toEqual(["OUT", "OUT"]);
    expect(inv.map((l) => l.qty)).toEqual(["2.0000", "3.0000"]);
    expect(inv.map((l) => l.amount)).toEqual(["301.0000", "299.9700"]);

    // 2. NO gst.lines and NO postings. The server generates both from the goods
    //    and REFUSES a body that also states them — sending "helpful" copies is
    //    not belt-and-braces, it is a second source of truth to pick between.
    const gst = req.gst as GSTRequest;
    expect(
      gst.lines,
      "an item invoice must not send gst.lines: the server derives them from the stock lines and refuses a body carrying both",
    ).toBeUndefined();
    expect(
      req.postings,
      "an item invoice must not send postings: the server generates them from the goods and 422s a body carrying both",
    ).toBeUndefined();
    expect(gst.place_of_supply_state_code).toBe("29");
    expect(
      gst.reverse_charge,
      "an RCM item invoice is refused server-side (the self-assessed legs are not generated), so the form must never claim one",
    ).toBe(false);

    // 3. THE TWO LEDGERS THE CLIENT DOES CHOOSE, BY NAME. This is the only
    //    accounting decision server-side generation could not take away, and
    //    getting it wrong still balances: an invoice booked to the wrong ledger
    //    is a perfectly valid voucher about the wrong account.
    expect(await ledgerName(base, req.party_ledger_id)).toBe("Acme Traders");
    expect(
      await ledgerName(base, req.item_ledger_id),
      "a SALE must be booked to a Sales Accounts ledger",
    ).toBe("Domestic Sales");
  });

  it("...and the SAVED sale reads party-DR / sales-CR / tax-CR, by name", async () => {
    const { base, spies } = await keyTwoLineInvoice("F8", "Acme Traders");
    await waitFor(() => expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1));
    const res = await spies.postSalesVoucher!.mock.results[0]!.value;

    // Taxable 600.9700 at 18% intra-state: CGST = SGST = 54.0873,
    // party = 709.1446.
    expect(await storedPostingsByName(base, res.voucher_id)).toEqual([
      { name: "Acme Traders", side: "DR", amount: "709.1446" },
      { name: "Domestic Sales", side: "CR", amount: "600.9700" },
      { name: "Output CGST", side: "CR", amount: "54.0873" },
      { name: "Output SGST", side: "CR", amount: "54.0873" },
    ]);
  });

  it("a PURCHASE item invoice is the MIRROR: party-CR / purchases-DR / tax-DR, and stock comes IN", async () => {
    // THE MUTATION THIS TEST EXISTS FOR: make the purchase screen name the
    // Domestic Sales ledger (swap GST_INCOME_LEDGER's two entries). The posted
    // voucher still balances to the paisa, still has the same two debits and
    // two credits, still passes the server's double-entry check and its
    // dr_cr-blind GST verify — and books every supplier bill as revenue. Only
    // naming the ledgers catches it.
    const { base, spies } = await keyTwoLineInvoice("F9", "Zenith Supplies");

    await waitFor(() => expect(spies.postPurchaseVoucher).toHaveBeenCalledTimes(1));
    const [, req] = spies.postPurchaseVoucher!.mock.calls[0]! as [string, VoucherRequest];

    expect(await ledgerName(base, req.party_ledger_id)).toBe("Zenith Supplies");
    expect(
      await ledgerName(base, req.item_ledger_id),
      "a PURCHASE must be booked to a Purchase Accounts ledger — naming the sales ledger reports the company's costs as revenue, and it balances",
    ).toBe("Purchases");

    const res = await spies.postPurchaseVoucher!.mock.results[0]!.value;
    const posted = await storedPostingsByName(base, res.voucher_id);
    expect(posted).toEqual([
      { name: "Zenith Supplies", side: "CR", amount: "709.1446" },
      { name: "Purchases", side: "DR", amount: "600.9700" },
      { name: "Output CGST", side: "DR", amount: "54.0873" },
      { name: "Output SGST", side: "DR", amount: "54.0873" },
    ]);
    // Totals prove NOTHING about orientation: a reversed purchase balances to
    // the same paisa and has the same two debits and two credits.
    const sum = (side: string) =>
      posted.filter((p) => p.side === side).reduce((n, p) => n + Number(p.amount), 0);
    expect(sum("DR")).toBeCloseTo(sum("CR"), 4);

    const inv = req.inventory as InventoryLineRequest[];
    expect(
      inv.map((l) => l.direction),
      "a purchase brings goods IN; posting OUT would take stock the company just bought straight back out of the godown",
    ).toEqual(["IN", "IN"]);

    // Stated separately so the failure message is unambiguous: the PARTY is
    // credited on a purchase (we owe the supplier) and the goods are debited.
    const party = posted.find((p) => p.name === "Zenith Supplies")!;
    expect(
      party.side,
      "a purchase CREDITS the supplier — a DR here books the bill as if the supplier owed us the money",
    ).toBe("CR");
    const goods = posted.find((p) => p.name === "Purchases")!;
    expect(
      goods.side,
      "a purchase DEBITS the expense/stock ledger — a CR here reports the company's costs as income",
    ).toBe("DR");
  });

  it("the SERVER refuses a body that also states its own postings", async () => {
    // The refusal is what makes "one computation" structural rather than a
    // convention the next client can quietly break. Asserted against the mock,
    // which mirrors ledger.ErrInventoryAndPostings.
    const base = createMockClient({ seed: false });
    await seedBooks(base);
    const { ledgers } = await base.listLedgers("co-1");
    const party = ledgers.find((l) => l.name === "Acme Traders")!;
    const items = await base.listStockItems("co-1");
    const widget = items.stock_items[0]!;
    const body: VoucherRequest = {
      voucher_date: "2026-07-01",
      party_ledger_id: party.id,
      item_ledger_id: party.id,
      inventory: [
        { stock_item_id: widget.id, direction: "OUT", qty: "1.0000", rate: "100.0000" },
      ],
      gst: { place_of_supply_state_code: "29" },
      postings: [
        { ledger_id: party.id, dr_cr: "DR", amount: "118.0000" },
        { ledger_id: party.id, dr_cr: "CR", amount: "118.0000" },
      ] as PostingRequest[],
    };
    await expect(base.postSalesVoucher("co-1", body)).rejects.toThrow(
      /do not send postings with inventory/,
    );
  });

  it("the two orientations are genuinely opposite (not accidentally identical)", () => {
    // A guard on the guard: if someone made both classes use one hardcoded
    // orientation, the two tests above would each still assert their own
    // expected names... so pin that the CONFIG actually differs.
    // (Imported lazily to keep the config assertion next to its consumers.)
    return import("./vouchers/voucherConfig").then(({ configFor }) => {
      expect(configFor("sales").primarySide).not.toBe(
        configFor("purchase").primarySide,
      );
    });
  });
});

// ---------------------------------------------------------------------------
// Quantity x rate: scaled integers, never Number()
// ---------------------------------------------------------------------------

describe("item-invoice arithmetic is exact fixed-4 decimal, never floating point", () => {
  /**
   * Every expectation here was produced by an INDEPENDENT ORACLE — Go's
   * shopspring/decimal, the library the backend computes with — not by running
   * the code under test and writing down what it said:
   *
   *   1.005 * 33.33  = 33.49665 -> round4 33.4967   (JS float: 33.4966)
   *   0.15  * 2.675  = 0.40125  -> round4 0.4013    (JS float: 0.4012)
   *   12.345 * 33.33 = 411.4589                      (JS float: 411.4588)
   *
   * These are the values a double cannot represent: 1.005 is stored as
   * 1.00499999999999989, so the product lands a hair BELOW the half-paisa and
   * rounds down. One paisa per line, on the document the customer pays from,
   * and on the quantity the stock ledger is valued by.
   */
  const oracle: { qty: string; rate: string; want: string; float: string }[] = [
    { qty: "1.005", rate: "33.33", want: "33.4967", float: "33.4966" },
    { qty: "0.15", rate: "2.675", want: "0.4013", float: "0.4012" },
    { qty: "12.345", rate: "33.33", want: "411.4589", float: "411.4588" },
    // Non-drifting controls, so the test also proves the ordinary case works.
    { qty: "2", rate: "150.5000", want: "301.0000", float: "301.0000" },
    { qty: "3", rate: "99.9900", want: "299.9700", float: "299.9700" },
  ];

  for (const c of oracle) {
    it(`${c.qty} x ${c.rate} = ${c.want} (a float would say ${c.float})`, () => {
      expect(
        mulFixed4(c.qty, c.rate),
        `qty x rate must equal what decimal.Decimal computes; ${c.float} is the answer a Number(a)*Number(b) implementation gives, and it is wrong by a paisa on the printed invoice`,
      ).toBe(c.want);
    });
  }

  it("the float implementation really would differ — the fixtures are not decorative", () => {
    // A mutation test written down as a test: if `mulFixed4` were
    // `(Number(a) * Number(b)).toFixed(4)`, THIS is what it would return. Any
    // fixture where the two agree proves nothing, so at least one must differ.
    const drifting = oracle.filter(
      (c) => (Number(c.qty) * Number(c.rate)).toFixed(4) !== c.want,
    );
    expect(
      drifting.length,
      "no fixture in this table distinguishes exact decimal arithmetic from float arithmetic, so the mutation would pass and the table is decorative",
    ).toBeGreaterThan(0);
    for (const c of drifting) {
      expect((Number(c.qty) * Number(c.rate)).toFixed(4)).toBe(c.float);
    }
  });

  it("parses exactly, without a float in the path", () => {
    // parseFloat("1.005") * 10000 is 10049.999999999998; Math.round rescues
    // that one but not every value, and the fix is not to round harder.
    expect(parseFixed4("1.005")).toBe(10050n);
    expect(formatFixed4(10050n)).toBe("1.0050");
    // Truncates beyond 4 dp rather than inventing a 5th place the column has
    // no room for.
    expect(parseFixed4("1.00509")).toBe(10050n);
    // Partially-typed input is 0, not NaN — the grid re-renders per keystroke.
    expect(parseFixed4("")).toBe(0n);
    expect(parseFixed4("-")).toBe(0n);
    expect(parseFixed4("12.")).toBe(120000n);
  });

  it("the discount is ONE rounding, matching ledger.InventoryLine.TaxableValue()", () => {
    // The server derives the line total itself and REFUSES a client value that
    // disagrees (it does not silently correct one), so a client that rounded
    // the gross first, or that computed `gross - round(gross*d/100)`, would 422
    // on some discounted line. Oracle: Go decimal, 1.005 * 33.33 = 33.49665,
    // * 0.9 = 30.146985 -> round4 30.1470.
    expect(taxableValueOf("1.005", "33.33", "10")).toBe("30.1470");
    expect(taxableValueOf("3", "333.33", "7.5")).toBe("924.9908");

    // THE CASE THAT SEPARATES THE TWO FORMULAS. Neither line above does: they
    // agree whichever way you round, so on their own they would let a
    // round-the-gross-first implementation pass. 0.125 kg at 7.77 is 0.97125
    // exactly, which rounds UP to 0.9713 — and 0.9713 less 10% is 0.87417 ->
    // 0.8742, while the server's single rounding of 0.97125 x 90% = 0.874125
    // gives 0.8741. One paisa, and the server REFUSES the voucher for it.
    // Oracle: Go decimal says 0.8741.
    expect(
      taxableValueOf("0.125", "7.77", "10"),
      "rounding qty x rate BEFORE the discount disagrees with the server's single rounding, and the server refuses the line rather than correcting it",
    ).toBe("0.8741");
    expect(taxableValueOf("0.125", "7.77", "7.5")).toBe("0.8984");
    expect(
      lineAmount({
        stockItemId: "s1",
        itemName: "Widget",
        godownId: "",
        batchId: "",
        qty: "1.005",
        rate: "33.33",
        discountPct: "10",
        gstRatePct: "18.0000",
        hsn: "",
        unit: "Nos",
      }),
    ).toBe("30.1470");
  });
});

// ---------------------------------------------------------------------------
// Stock Summary screen
// ---------------------------------------------------------------------------

const EMPTY_SUMMARY: StockSummaryResponse = {
  as_on: "",
  items: [],
  rows: [],
  total_value: "0.0000",
};

const ONE_ROW_SUMMARY: StockSummaryResponse = {
  as_on: "2026-07-31",
  total_value: "1200.0000",
  rows: [],
  items: [
    {
      stock_item_id: "s1",
      item: "Widget",
      unit: "Nos",
      opening_qty: "0.0000",
      opening_value: "0.0000",
      inward_qty: "10.0000",
      inward_value: "2000.0000",
      outward_qty: "4.0000",
      outward_value: "800.0000",
      closing_qty: "6.0000",
      closing_value: "1200.0000",
      positions: [
        {
          stock_item_id: "s1",
          item: "Widget",
          unit: "Nos",
          godown_id: "g1",
          godown: "Main Location",
          batch_id: "",
          batch: "",
          valuation_method: "WEIGHTED_AVG",
          opening_qty: "0.0000",
          opening_value: "0.0000",
          inward_qty: "10.0000",
          inward_value: "2000.0000",
          outward_qty: "4.0000",
          outward_value: "800.0000",
          closing_qty: "6.0000",
          closing_value: "1200.0000",
          closing_rate: "200.0000",
        },
      ],
    },
  ],
};
ONE_ROW_SUMMARY.rows = ONE_ROW_SUMMARY.items[0]!.positions;

/** Render the Stock Summary against a client whose getStockSummary is stubbed. */
function renderStockSummary(getStockSummary: CoredClient["getStockSummary"]) {
  const base = createMockClient({ seed: false });
  const client = { ...base, getStockSummary } as CoredClient;
  const snapshot = {
    screen: "report",
    reportName: "Stock Summary",
    screenStackDepth: 2,
    canReturnToVoucher: false,
  } as NavSnapshot;
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <StockSummaryScreen snapshot={snapshot} />
    </CompanyProvider>,
  );
}

describe("Stock Summary screen", () => {
  it("renders the rows the client returns", async () => {
    renderStockSummary(async () => ONE_ROW_SUMMARY);
    expect(await screen.findByTestId("stock-item:Widget")).toBeTruthy();
    expect(screen.getByTestId("stock-row:Widget:Main Location")).toBeTruthy();
    expect(screen.getByTestId("stock-total-value").textContent).toContain("1,200");
  });

  it("renders NOTHING when the client returns an empty position", async () => {
    // THE MUTATION: seed this screen with demo rows on empty and this
    // assertion fails. A report that invents rows is exactly how a placeholder
    // ships looking wired, and on an accounting screen a fabricated closing
    // stock is worse than a missing one.
    renderStockSummary(async () => EMPTY_SUMMARY);
    // Wait for the fetch to settle on SOMETHING before asserting absence, or
    // "no rows" would pass merely because the screen is still loading.
    await waitFor(() =>
      expect(screen.queryByTestId("stock-summary-loading")).toBeNull(),
    );
    // The invention check comes FIRST so its message is the one a failure
    // reports: a screen that fills an empty response with demo rows is the
    // failure mode, and "the empty message is missing" describes it far worse
    // than "the screen rendered rows nobody posted".
    expect(
      screen.queryAllByTestId(/^stock-row:/),
      "an empty stock position rendered stock rows — those numbers were invented by the screen, and a fabricated closing stock is worse than a missing report",
    ).toHaveLength(0);
    expect(screen.queryAllByTestId(/^stock-item:/)).toHaveLength(0);
    expect(screen.queryByTestId("stock-summary-table")).toBeNull();
    expect(screen.getByTestId("stock-summary-empty")).toBeTruthy();
  });

  it("states the REASON on a 403 rather than showing a blank screen", async () => {
    // Migration 0026 denies this report to Data Entry (it prints cost prices),
    // so a refusal is a normal answer, not a fault. An operator shown an empty
    // table files a bug; an operator told which rule refused asks their Owner.
    renderStockSummary(async () => {
      throw new ApiError(403, "forbidden by security level", {});
    });
    const denied = await screen.findByTestId("stock-summary-denied");
    expect(denied.textContent).toMatch(/security level/i);
    expect(denied.textContent).toMatch(/cost/i);
    expect(screen.queryByTestId("stock-summary-table")).toBeNull();
    // Not the generic error path: a 403 is a policy answer, a 500 is a fault.
    expect(screen.queryByTestId("stock-summary-error")).toBeNull();
  });

  it("surfaces any OTHER failure as an error, still without inventing rows", async () => {
    renderStockSummary(async () => {
      throw new ApiError(500, "replay failed", {});
    });
    expect(await screen.findByTestId("stock-summary-error")).toBeTruthy();
    expect(screen.queryByTestId("stock-summary-table")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// F11 is an honesty surface
// ---------------------------------------------------------------------------

describe("F11 Company Features tells the truth about inventory", () => {
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

  /** The badge cell rendered next to a feature row's name. */
  function badgeFor(namePattern: RegExp): string {
    const cell = screen.getByText(namePattern);
    const row = cell.closest("tr");
    return row?.firstElementChild?.textContent?.trim() ?? "";
  }

  it("the Item Invoice row reads 'On', not 'Not built'", () => {
    // THE MUTATION: ship the grid and leave this row saying "Not built". An
    // honesty surface that lags the product is worse than no surface — it
    // teaches people not to trust it — so the claim is pinned by a test in the
    // same change that makes it true.
    renderFeatures();
    expect(
      badgeFor(/^Item Invoice \(Ctrl\+H/),
      "F11 must say the item invoice is On in the same change that ships it",
    ).toBe("On");
  });

  it("the Stock Summary and godown rows read 'On' too", () => {
    renderFeatures();
    expect(badgeFor(/^Stock Summary \(closing quantity/)).toBe("On");
    expect(badgeFor(/^Godowns \/ stock locations/)).toBe("On");
  });

  it("the closing-stock row reads 'On', because the Balance Sheet now carries the figure", () => {
    // THIS ASSERTION USED TO LIVE IN THE "not built" TEST BELOW AND SAID
    // "Not built". Closing stock was wired onto the Balance Sheet through the
    // same derivation the Stock Summary uses — one function, so the two report
    // paths cannot disagree — and the row was corrected to "On" while this
    // assertion was left behind, which turned the honesty gate RED against a
    // screen that was telling the truth. The claim is still pinned, in the
    // direction that is now true: if the Balance Sheet's Closing Stock line is
    // ever unwired, this fails, and if the row is left saying "Not built" after
    // it was wired, it fails too.
    renderFeatures();
    expect(
      badgeFor(/^Closing stock on the Balance Sheet/),
      "F11 must say closing stock reaches the Balance Sheet, because it does",
    ).toBe("On");
  });

  it("still says NOT BUILT for the inventory things that are not built", () => {
    // The other half of honesty. If every row said "On" the screen would be
    // decoration, so the gaps this slice did NOT close are asserted too.
    // Closing stock is NOT one of them any more — it has its own test above.
    renderFeatures();
    expect(badgeFor(/^FIFO \/ LIFO/)).toBe("Not built");
    // Was "Inventory vouchers (Stock Journal, ...)" until those four got an
    // entry screen; the still-missing half of that row is order processing,
    // which is asserted here and pinned On/Not-built in the stock-voucher
    // suite's own F11 test.
    expect(badgeFor(/^Order processing \(Sales \/ Purchase Orders\)/)).toBe("Not built");
  });
});
