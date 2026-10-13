import { describe, it, expect, afterEach } from "vitest";
import {
  render,
  screen,
  cleanup,
  waitFor,
  fireEvent,
} from "@testing-library/react";

import { StockSummaryScreen } from "./StockSummaryScreen";
import { CompanyProvider } from "../../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
  type ScreenActionKind,
  type ScreenActionResult,
} from "../../app/ScreenActions";
import { createMockClient } from "../../api";
import type { CoredClient } from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";

/**
 * StockSummaryScreen.valuation.test.tsx — FEATURE T9 ON THE SCREEN.
 *
 * ⛔⛔ NOTHING HERE IS A STUBBED RESPONSE, AND THAT IS THE WHOLE DESIGN OF THIS
 * FILE. Its sibling StockSummaryScreen.test.tsx hands the screen a fixed
 * `getStockSummary`, which is right for testing captions and columns and USELESS
 * here: a fixture cannot tell you whether the number moved because the costing
 * method changed. So these tests drive the MOCK CLIENT end to end — create the
 * item, post two purchases and a sale, read the report, switch the method, read
 * it again — and the figures are the mock's own replay.
 *
 * That also means these tests fail if the mock LABELS a method it does not
 * REPLAY (it used to hardcode `valuation_method: "WEIGHTED_AVG"` on every row),
 * which is register row D84's rule and the most dangerous divergence available
 * here: both numbers look right, and the browser's is the other one.
 *
 * THE ORACLE IS THAT THE THREE METHODS DISAGREE. Buy 10 @ 100, buy 10 @ 120,
 * issue 5: closing 1650.0000 at Avg. Cost, 1700.0000 at FIFO, 1600.0000 at LIFO.
 * No assertion below is shared by two methods.
 */

afterEach(cleanup);

let fire: ((kind: ScreenActionKind) => ScreenActionResult) | null = null;

function ActionProbe() {
  const dispatch = useScreenActionDispatcher();
  fire = (kind) => dispatch(kind, {} as NavSnapshot);
  return null;
}

/** Create the item, then buy 10 @ 100, buy 10 @ 120 and sell 5 at 200. */
async function tradingCompany(
  client: CoredClient,
  valuationMethod?: string,
): Promise<string> {
  const co = await client.createCompany({
    name: "Valuation Co",
    state_code: "29",
    fy_start_date: "2026-04-01",
    base_currency: "INR",
  });
  const companyID = co.company_id;
  const supplier = await client.createLedger(companyID, {
    name: "Supplier",
    group_name: "Sundry Creditors",
  });
  const purchases = await client.createLedger(companyID, {
    name: "Purchases",
    group_name: "Purchase Accounts",
  });
  const customer = await client.createLedger(companyID, {
    name: "Customer",
    group_name: "Sundry Debtors",
  });
  const sales = await client.createLedger(companyID, {
    name: "Sales",
    group_name: "Sales Accounts",
  });
  const item = await client.createStockItem(companyID, {
    name: "Widget",
    unit_symbol: "Nos",
    ...(valuationMethod ? { valuation_method: valuationMethod } : {}),
  });
  const godowns = await client.listGodowns(companyID);
  const godownID = godowns.godowns[0]!.id;

  for (const [no, date, qty, rate, amount] of [
    ["PUR-1", "2026-04-01", "10.0000", "100.0000", "1000.0000"],
    ["PUR-2", "2026-04-05", "10.0000", "120.0000", "1200.0000"],
  ] as const) {
    await client.postPurchaseVoucher(companyID, {
      voucher_no: no,
      voucher_date: date,
      postings: [
        { ledger_id: purchases.ledger_id, dr_cr: "D", amount },
        { ledger_id: supplier.ledger_id, dr_cr: "C", amount },
      ],
      inventory: [
        {
          stock_item_id: item.stock_item_id,
          godown_id: godownID,
          batch_id: "",
          direction: "IN",
          qty,
          rate,
          amount,
        },
      ],
    });
  }
  await client.postSalesVoucher(companyID, {
    voucher_no: "INV-1",
    voucher_date: "2026-04-10",
    postings: [
      { ledger_id: customer.ledger_id, dr_cr: "D", amount: "1000.0000" },
      { ledger_id: sales.ledger_id, dr_cr: "C", amount: "1000.0000" },
    ],
    inventory: [
      {
        stock_item_id: item.stock_item_id,
        godown_id: godownID,
        batch_id: "",
        direction: "OUT",
        qty: "5.0000",
        rate: "200.0000",
        amount: "1000.0000",
      },
    ],
  });
  return companyID;
}

function renderStockSummary(client: CoredClient, companyID: string) {
  fire = null;
  const snapshot = {
    screen: "report",
    reportName: "Stock Summary",
    screenStackDepth: 2,
    canReturnToVoucher: false,
    config: { periodTo: "2026-04-30" },
  } as NavSnapshot;
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{
        id: companyID,
        name: "Valuation Co",
        homeStateCode: "29",
      }}
    >
      <ScreenActionsProvider>
        <ActionProbe />
        <StockSummaryScreen snapshot={snapshot} />
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
}

describe("the Stock Summary states the basis its figures were derived on", () => {
  it("prints the item's costing method in a Basis column, per item", async () => {
    const client = createMockClient({ seed: false });
    const companyID = await tradingCompany(client, "FIFO");
    renderStockSummary(client, companyID);

    await waitFor(() => {
      expect(screen.getByTestId("stock-summary-table")).toBeTruthy();
    });
    // ⛔ THE COLUMN IS FOUND BY ITS HEADER, and the value read out of the ITEM
    // line. A Stock Summary that prints values without the basis is a report
    // whose numbers cannot be compared with the line above them once one
    // company can hold three methods.
    expect(screen.getByRole("columnheader", { name: "Basis" })).toBeTruthy();
    expect(screen.getByTestId("stock-basis:Widget").textContent).toBe("FIFO");
  });

  it("values a FIFO item as FIFO and a LIFO item as LIFO — the mock REPLAYS the method rather than labelling it", async () => {
    // Both companies see the same three movements. If the mock folded a
    // weighted average and merely printed the label, both would close at
    // 1650.0000 and this test is the one that catches it.
    for (const [method, closing] of [
      ["FIFO", "1,700.00"],
      ["LIFO", "1,600.00"],
      [undefined, "1,650.00"],
    ] as const) {
      const client = createMockClient({ seed: false });
      const companyID = await tradingCompany(client, method);
      const view = renderStockSummary(client, companyID);
      await waitFor(() => {
        expect(screen.getByTestId("stock-total-value").textContent).toBe(
          closing,
        );
      });
      view.unmount();
      cleanup();
    }
  });
});

describe("Alt+V — changing an item's costing method from the report", () => {
  it("switches the method and the FIGURE ON SCREEN moves", async () => {
    const client = createMockClient({ seed: false });
    const companyID = await tradingCompany(client); // the default basis
    renderStockSummary(client, companyID);

    await waitFor(() => {
      expect(screen.getByTestId("stock-total-value").textContent).toBe(
        "1,650.00",
      );
    });
    expect(screen.getByTestId("stock-basis:Widget").textContent).toBe(
      "WEIGHTED_AVG",
    );

    // Alt+V. One item on the report, so it is pre-picked: an operator looking
    // at one item should not be asked which one they mean.
    const result = fire!("setValuationMethod");
    expect(result.ran).toBe(true);
    await waitFor(() => {
      expect(screen.getByTestId("stock-valuation-panel")).toBeTruthy();
    });
    expect(screen.getByTestId("stock-valuation-item").textContent).toMatch(
      /Widget — currently WEIGHTED_AVG/,
    );

    // The methods are the SERVER's list, not this screen's. All three are
    // described, because "FIFO" alone tells an operator nothing about which to
    // choose.
    await waitFor(() => {
      expect(screen.getByTestId("stock-valuation-option:FIFO")).toBeTruthy();
    });
    expect(
      screen.getByTestId("stock-valuation-option:LIFO").textContent,
    ).toMatch(/OLDEST purchases cost/);

    pickMethod("FIFO");

    // ⛔ THE ORACLE IS THE TABLE, NOT THE MESSAGE. The report must REDRAW: the
    // whole point of the switch is that the figure moves, and a screen that
    // reported success over a table still showing 1,650.00 would be the same
    // class of lie as answering a write with the request body.
    await waitFor(() => {
      expect(screen.getByTestId("stock-total-value").textContent).toBe(
        "1,700.00",
      );
    });
    expect(screen.getByTestId("stock-basis:Widget").textContent).toBe("FIFO");
    // And the disclosure the server sent travels to the operator: they have
    // just re-valued periods that may already have been reported.
    expect(screen.getByTestId("stock-valuation-status").textContent).toMatch(
      /WHOLE history/i,
    );
  });

  it("refuses to open on a company with no stock, instead of offering an empty picker", async () => {
    const client = createMockClient({ seed: false });
    const co = await client.createCompany({
      name: "Empty Co",
      state_code: "29",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
    });
    renderStockSummary(client, co.company_id);
    await waitFor(() => {
      expect(screen.getByTestId("stock-summary-empty")).toBeTruthy();
    });
    const result = fire!("setValuationMethod");
    expect(result.ran).toBe(true);
    expect(result.message).toMatch(/no item to re-value/i);
    expect(screen.queryByTestId("stock-valuation-panel")).toBeNull();
  });

  it("says so, and changes nothing, when the item already uses that method", async () => {
    const client = createMockClient({ seed: false });
    const companyID = await tradingCompany(client, "FIFO");
    renderStockSummary(client, companyID);
    await waitFor(() => {
      expect(screen.getByTestId("stock-total-value").textContent).toBe(
        "1,700.00",
      );
    });

    fire!("setValuationMethod");
    await waitFor(() => {
      expect(screen.getByTestId("stock-valuation-option:FIFO")).toBeTruthy();
    });
    pickMethod("FIFO");

    await waitFor(() => {
      expect(screen.getByTestId("stock-valuation-status").textContent).toMatch(
        /already valued at FIFO/i,
      );
    });
    // The figure did not move, because nothing was changed.
    expect(screen.getByTestId("stock-total-value").textContent).toBe(
      "1,700.00",
    );
  });
});

/**
 * Choose a method in the Alt+V panel: filter the as-you-type combobox to the
 * one method, then press Enter. That is how an operator uses a SelectionList,
 * and it is how CompanySelectScreen's own test drives one.
 */
function pickMethod(method: string) {
  const combo = screen.getByRole("combobox", { name: /New method/i });
  fireEvent.change(combo, { target: { value: method } });
  fireEvent.keyDown(combo, { key: "Enter" });
}
