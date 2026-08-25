import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";

import { StockSummaryScreen } from "./StockSummaryScreen";
import { CompanyProvider } from "../../app/CompanyContext";
import { ScreenActionsProvider } from "../../app/ScreenActions";
import { createMockClient } from "../../api";
import type { CoredClient } from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";

/**
 * StockSummaryScreen.groups.test.tsx — FEATURE T10 ON THE SCREEN: the STOCK
 * GROUP subtotals and the COMPOUND-UNIT restatement.
 *
 * ⛔⛔ NOTHING HERE IS A STUBBED RESPONSE, for the reason its sibling
 * StockSummaryScreen.valuation.test.tsx gives at length: a fixture cannot tell
 * you whether a number was DERIVED. These tests create the groups, create the
 * compound unit, create the items, post the purchases and read the report —
 * every figure below is the mock's own replay, and the mock is a SECOND
 * IMPLEMENTATION of the server's two rules (`replayStock` walks the parent chain
 * for the branch rollup, and multiplies the conversion factor after the item
 * fold). If the two implementations disagree, one of them is wrong, and the
 * browser's is the other one.
 *
 * THE TWO ORACLES, both chosen so that no assertion is shared by the right and
 * the wrong answer:
 *
 *  1. A GROUP SUBTOTAL IS THE WHOLE BRANCH. "Provisions" holds Sugar (250) and
 *     contains "Provisions > Grains", which holds Rice (1,000). The branch total
 *     is 1,250 and the DIRECT-ONLY total is 250 — a parent printing SMALLER than
 *     its own child, which is the mutation §1.2 names.
 *  2. A COMPOUND QUANTITY IS STORED IN THE ITEM'S OWN UNIT. Four boxes of twelve
 *     close at 4.0000 with 48.0000 Nos restated beside them. The mutation that
 *     assigns the restatement back over the stored figure reads 48 in the Qty
 *     column where 4 was keyed — so the closing quantity is asserted too, and
 *     not only the derived column.
 */

afterEach(cleanup);

const PROVISIONS = "Provisions";
const GRAINS = "Grains";

/**
 * A company holding three items: one in a CHILD group, one in its PARENT, and
 * one in no group at all — plus a compound unit, so both T10 derivations are on
 * the same page.
 *
 * Rice   1,000.0000 in Provisions > Grains   (simple unit, Kg-like "Nos")
 * Sugar    250.0000 in Provisions            (simple unit)
 * Widget 1,200.0000 ungrouped                (4 BOX, 1 BOX = 12 Nos)
 */
async function groupedCompany(client: CoredClient): Promise<string> {
  const co = await client.createCompany({
    name: "Grouped Co",
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

  // The hierarchy, parent first — a child cannot name a parent that does not
  // exist yet, which is the server's rule and the mock's.
  await client.createStockGroup(companyID, { name: PROVISIONS });
  await client.createStockGroup(companyID, {
    name: GRAINS,
    parent_name: PROVISIONS,
  });
  // 1 BOX = 12 Nos. "Nos" is the company's own seeded unit; a base unit has to
  // exist before something can be measured in it.
  await client.createUnit(companyID, {
    symbol: "BOX",
    formal_name: "Boxes",
    decimal_places: 0,
    base_unit_symbol: "Nos",
    conversion_factor: "12.0000",
  });

  const rice = await client.createStockItem(companyID, {
    name: "Rice",
    unit_symbol: "Nos",
    stock_group_name: GRAINS,
  });
  const sugar = await client.createStockItem(companyID, {
    name: "Sugar",
    unit_symbol: "Nos",
    stock_group_name: PROVISIONS,
  });
  const widget = await client.createStockItem(companyID, {
    name: "Widget",
    unit_symbol: "BOX",
  });

  const godowns = await client.listGodowns(companyID);
  const godownID = godowns.godowns[0]!.id;

  let no = 0;
  for (const [itemID, qty, rate, amount] of [
    [rice.stock_item_id, "10.0000", "100.0000", "1000.0000"],
    [sugar.stock_item_id, "5.0000", "50.0000", "250.0000"],
    [widget.stock_item_id, "4.0000", "300.0000", "1200.0000"],
  ] as const) {
    no += 1;
    await client.postPurchaseVoucher(companyID, {
      voucher_no: `PUR-${String(no)}`,
      voucher_date: "2026-04-01",
      postings: [
        { ledger_id: purchases.ledger_id, dr_cr: "D", amount },
        { ledger_id: supplier.ledger_id, dr_cr: "C", amount },
      ],
      inventory: [
        {
          stock_item_id: itemID,
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
  return companyID;
}

function renderStockSummary(client: CoredClient, companyID: string) {
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
        name: "Grouped Co",
        homeStateCode: "29",
      }}
    >
      <ScreenActionsProvider>
        <StockSummaryScreen snapshot={snapshot} />
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
}

/** The value cell of one subtotal row (Group | Items | Closing Value). */
function groupValue(group: string): string {
  return (
    screen.getByTestId(`stock-group:${group}`).lastElementChild?.textContent ??
    ""
  );
}

/** The item-count cell of one subtotal row. */
function groupItemCount(group: string): string {
  const cells = screen.getByTestId(`stock-group:${group}`).children;
  return cells[1]?.textContent ?? "";
}

describe("the Stock Summary subtotals by stock group", () => {
  it("prints one line per group in tree order, with the ungrouped bucket named", async () => {
    const client = createMockClient({ seed: false });
    const companyID = await groupedCompany(client);
    renderStockSummary(client, companyID);

    await waitFor(() => {
      expect(screen.getByTestId("stock-group-subtotals")).toBeTruthy();
    });
    // Tree order: the parent above its own child, and the ungrouped bucket LAST.
    // A report that dropped that bucket would show a company holding less stock
    // than it does.
    const rows = [
      ...screen
        .getByTestId("stock-group-subtotals")
        .querySelectorAll("tbody tr"),
    ].map((r) => r.getAttribute("data-testid"));
    expect(rows).toEqual([
      `stock-group:${PROVISIONS}`,
      `stock-group:${GRAINS}`,
      "stock-group:",
    ]);
    expect(screen.getByTestId("stock-group:").textContent).toContain(
      "(ungrouped)",
    );
  });

  it("⛔ a group total is the WHOLE BRANCH — the parent CONTAINS its child, and says so in the figure", async () => {
    const client = createMockClient({ seed: false });
    const companyID = await groupedCompany(client);
    renderStockSummary(client, companyID);

    await waitFor(() => {
      expect(screen.getByTestId("stock-group-subtotals")).toBeTruthy();
    });
    // ⛔ THE ORACLE. Provisions holds Sugar (250) DIRECTLY and Grains (1,000)
    // BENEATH it. The branch is 1,250; the direct-only fold — the mutation
    // `for i := len(labels)-1; i < len(labels); i++` in rollupStockGroups —
    // prints 250 here, which is a parent SMALLER than its own child with nothing
    // on the page to explain the gap.
    expect(groupValue(PROVISIONS)).toBe("1,250.00");
    expect(groupValue(GRAINS)).toBe("1,000.00");
    expect(groupValue("")).toBe("1,200.00");
    // The item COUNT is the branch too, or the two columns of one line would
    // disagree about what the line covers.
    expect(groupItemCount(PROVISIONS)).toBe("2");
    expect(groupItemCount(GRAINS)).toBe("1");

    // ...and the whole company is 2,450 — which is NOT the sum of the three
    // subtotal lines (3,450). That gap IS the feature, and it is why the next
    // assertion exists.
    expect(screen.getByTestId("stock-total-value").textContent).toBe("2,450.00");
  });

  it("says the rows must not be added, where a reader would otherwise add them", async () => {
    const client = createMockClient({ seed: false });
    const companyID = await groupedCompany(client);
    renderStockSummary(client, companyID);

    await waitFor(() => {
      expect(screen.getByTestId("stock-group-subtotals")).toBeTruthy();
    });
    // A column of money with no total invites a calculator. The reason is stated
    // in the table's own footer rather than in a release note nobody reading this
    // screen has open.
    const footer =
      screen.getByTestId("stock-group-subtotals").querySelector("tfoot")
        ?.textContent ?? "";
    expect(footer).toMatch(/No total/i);
    expect(footer).toMatch(/double-count|overlap/i);
    expect(footer).toMatch(/Stock-in-Hand/);
  });

  it("⚠️ a company with NO groups renders no subtotal table at all", async () => {
    // Every item that predates T10 is ungrouped, so a table whose only line is
    // "(ungrouped)" says nothing the item list does not — and a heading over it
    // would imply a feature is missing rather than unused.
    const client = createMockClient({ seed: false });
    const co = await client.createCompany({
      name: "Flat Co",
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
    const item = await client.createStockItem(companyID, {
      name: "Rice",
      unit_symbol: "Nos",
    });
    const godowns = await client.listGodowns(companyID);
    await client.postPurchaseVoucher(companyID, {
      voucher_no: "PUR-1",
      voucher_date: "2026-04-01",
      postings: [
        { ledger_id: purchases.ledger_id, dr_cr: "D", amount: "1000.0000" },
        { ledger_id: supplier.ledger_id, dr_cr: "C", amount: "1000.0000" },
      ],
      inventory: [
        {
          stock_item_id: item.stock_item_id,
          godown_id: godowns.godowns[0]!.id,
          batch_id: "",
          direction: "IN",
          qty: "10.0000",
          rate: "100.0000",
          amount: "1000.0000",
        },
      ],
    });
    renderStockSummary(client, companyID);

    await waitFor(() => {
      expect(screen.getByTestId("stock-summary-table")).toBeTruthy();
    });
    expect(screen.queryByTestId("stock-group-subtotals")).toBeNull();
  });
});

describe("the Stock Summary restates a compound quantity in its base unit", () => {
  it("⛔ shows 4 BOX as 4.0000 with 48.0000 Nos BESIDE it, never instead of it", async () => {
    const client = createMockClient({ seed: false });
    const companyID = await groupedCompany(client);
    renderStockSummary(client, companyID);

    await waitFor(() => {
      expect(screen.getByTestId("stock-summary-table")).toBeTruthy();
    });
    // The restated column.
    expect(screen.getByTestId("stock-base-qty:Widget").textContent).toBe(
      "48.0000 Nos",
    );
    // ⛔ AND THE STORED FIGURE IS UNTOUCHED, which is the half the mutation
    // breaks: `out[i].ClosingQty = out[i].ClosingBaseQty` reads 48.0000 here,
    // and a Stock Summary claiming forty-eight boxes disagrees with the purchase
    // invoice about what was bought.
    const widgetRow = screen.getByTestId("stock-item:Widget");
    const cells = [...widgetRow.children].map((c) => c.textContent);
    expect(cells).toContain("4.0000");
    expect(cells).not.toContain("48.0000");
  });

  it("prints an em dash for a SIMPLE unit — there is no base to restate into", async () => {
    // Not "0.0000". A zero in a quantity column is a claim about the goods, and
    // an item measured in Nos has no second unit to be wrong about.
    const client = createMockClient({ seed: false });
    const companyID = await groupedCompany(client);
    renderStockSummary(client, companyID);

    await waitFor(() => {
      expect(screen.getByTestId("stock-summary-table")).toBeTruthy();
    });
    expect(screen.getByTestId("stock-base-qty:Rice").textContent).toBe("—");
    expect(screen.getByTestId("stock-base-qty:Sugar").textContent).toBe("—");
  });
});
