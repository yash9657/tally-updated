import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { CompanyProvider } from "../../app/CompanyContext";
import { LoadedVoucherProvider } from "../../app/LoadedVoucher";
import { ScreenActionsProvider } from "../../app/ScreenActions";
import {
  DayBookScreen,
  TrialBalanceScreen,
  BalanceSheetScreen,
  ProfitLossScreen,
  OutstandingsScreen,
} from "./ReportScreens";
import { createMockClient } from "../../api";
import type { CoredClient } from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";

afterEach(cleanup);

const SNAP: NavSnapshot = {
  screen: "report",
  reportName: "Report",
  screenStackDepth: 2,
  canReturnToVoucher: false,
};

function renderReport(
  Comp: React.ComponentType<{
    snapshot: NavSnapshot;
    onSelect: (s: string) => void;
  }>,
  client: Partial<CoredClient>,
) {
  return render(
    <CompanyProvider
      client={client as CoredClient}
      initialCompany={{ id: "co-1", name: "Test Co" }}
    >
      <LoadedVoucherProvider>
        {/* Reports register their Alt+E export handler on the screen-action
            bridge, exactly as they do inside the shell. */}
        <ScreenActionsProvider>
          <Comp snapshot={SNAP} onSelect={() => {}} />
        </ScreenActionsProvider>
      </LoadedVoucherProvider>
    </CompanyProvider>,
  );
}

describe("report screens fetch from the live client on mount", () => {
  it("Trial Balance calls getTrialBalance(companyID) and renders the total string verbatim", async () => {
    const getTrialBalance = vi.fn(async () => ({
      rows: [
        { ledger: "Cash", group: "Cash-in-Hand", debit: "1180.0000", credit: "0.0000" },
      ],
      total_debit: "1180.0000",
      total_credit: "1180.0000",
      balanced: true,
    }));
    renderReport(TrialBalanceScreen, { getTrialBalance });
    await waitFor(() =>
      expect(getTrialBalance).toHaveBeenCalledWith("co-1", undefined),
    );
    // Money rendered with Indian grouping, never coerced: 1180 -> "1,180.00".
    expect(screen.getByTestId("tb-total-debit").textContent).toBe("1,180.00");
  });

  it("Day Book calls getDayBook(companyID) and renders the entry's number", async () => {
    const getDayBook = vi.fn(async () => ({
      from: "",
      to: "",
      entries: [
        {
          voucher_id: "v1",
          version_no: 1,
          op: "CREATE",
          // The SERVER's label ("Sales"), not the mock's old upper-cased slug —
          // the Day Book drill routes on this field.
          class: "Sales",
          voucher_type: "sales",
          voucher_no: "INV/0007",
          date: "2026-04-10",
          narration: "",
          party: "Acme",
          debit: "400.0000",
          credit: "400.0000",
          postings: [],
        },
      ],
    }));
    renderReport(DayBookScreen, { getDayBook });
    // Await the RENDER, not just the call — the same hazard the Outstandings
    // test below already guards against. A spy records its call on invocation,
    // so waiting on it can return before the response has been applied to state.
    await waitFor(() => expect(screen.getByText("INV/0007")).toBeTruthy());
    expect(getDayBook).toHaveBeenCalledWith("co-1", {});
  });

  it("Balance Sheet calls getBalanceSheet(companyID)", async () => {
    const getBalanceSheet = vi.fn(async () => ({
      assets: { title: "Assets", nodes: [], total: "5000.0000" },
      liabilities: { title: "Liabilities", nodes: [], total: "5000.0000" },
      net_profit: "0.0000",
      balanced: true,
    }));
    renderReport(BalanceSheetScreen, { getBalanceSheet });
    await waitFor(() =>
      expect(getBalanceSheet).toHaveBeenCalledWith("co-1", undefined),
    );
    expect(screen.getByTestId("bs-assets-total").textContent).toBe("5,000.00");
  });

  it("Profit & Loss calls getProfitLoss(companyID)", async () => {
    const getProfitLoss = vi.fn(async () => ({
      income: { title: "Income", nodes: [], total: "900.0000" },
      expenses: { title: "Expenses", nodes: [], total: "300.0000" },
      net_profit: "600.0000",
    }));
    renderReport(ProfitLossScreen, { getProfitLoss });
    await waitFor(() =>
      expect(getProfitLoss).toHaveBeenCalledWith("co-1", undefined, undefined),
    );
    expect(screen.getByTestId("pl-net-profit").textContent).toBe("600.00");
  });

  it("Outstandings calls getOutstandings(companyID)", async () => {
    const getOutstandings = vi.fn(async () => ({
      kind: "RECEIVABLE" as const,
      as_on: "",
      parties: [
        {
          ledger_id: "led-acme",
          ledger: "Acme",
          group: "Sundry Debtors",
          pending: "400.0000",
          bills: [],
        },
      ],
      slab_totals: [],
      total: "400.0000",
    }));
    renderReport(OutstandingsScreen, { getOutstandings });
    await waitFor(() => expect(getOutstandings).toHaveBeenCalledWith("co-1", {}));
    // Await the RENDER, not just the call: the total only exists once the
    // response has been applied to state.
    await waitFor(() =>
      expect(screen.getByTestId("os-total").textContent).toBe("400.00"),
    );
  });
});

/**
 * PERIOD SEMANTICS reach the wire correctly, per statement type.
 *
 * The three statements do NOT take the same period, and the whole risk of this
 * feature is that they silently agree to disagree: a Balance Sheet handed a
 * lower bound would drop the entity's earlier history and STILL FOOT, so the
 * screen would show a plausible, wrong position with a label claiming a range.
 * These tests assert what each screen actually sends.
 */
describe("the F2 period reaches each statement with the RIGHT bounds", () => {
  const withPeriod = (reportName: string): NavSnapshot => ({
    ...SNAP,
    reportName,
    config: { periodFrom: "2026-04-01", periodTo: "2026-06-30" },
  });

  function renderWithSnapshot(
    Comp: React.ComponentType<{
      snapshot: NavSnapshot;
      onSelect: (s: string) => void;
    }>,
    snapshot: NavSnapshot,
    over: Partial<CoredClient>,
  ) {
    const client = { ...createMockClient({ seed: false }), ...over } as CoredClient;
    return render(
      <CompanyProvider
        client={client}
        initialCompany={{ id: "co-1", name: "Demo", homeStateCode: "29" }}
      >
        <LoadedVoucherProvider>
          <ScreenActionsProvider>
            <Comp snapshot={snapshot} onSelect={() => {}} />
          </ScreenActionsProvider>
        </LoadedVoucherProvider>
      </CompanyProvider>,
    );
  }

  it("Trial Balance sends only AS ON (it is a position, not a range)", async () => {
    const getTrialBalance = vi.fn(async () => ({
      rows: [], total_debit: "0.0000", total_credit: "0.0000", balanced: true,
    }));
    renderWithSnapshot(TrialBalanceScreen, withPeriod("Trial Balance"), {
      getTrialBalance,
    });
    await waitFor(() =>
      expect(getTrialBalance).toHaveBeenCalledWith("co-1", "2026-06-30"),
    );
    // The lower bound is NOT among the arguments — not merely unused.
    expect(JSON.stringify(getTrialBalance.mock.calls[0])).not.toContain(
      "2026-04-01",
    );
    // ...and the banner says "As on", never a range it did not apply.
    const banner = screen.getByTestId("report-view-banner").textContent ?? "";
    expect(banner).toContain("As on 2026-06-30");
    expect(banner).not.toContain("2026-04-01");
  });

  it("Balance Sheet sends only AS ON", async () => {
    const getBalanceSheet = vi.fn(async () => ({
      assets: { title: "Assets", nodes: [], total: "0.0000" },
      liabilities: { title: "Liabilities", nodes: [], total: "0.0000" },
      net_profit: "0.0000",
      balanced: true,
    }));
    renderWithSnapshot(BalanceSheetScreen, withPeriod("Balance Sheet"), {
      getBalanceSheet,
    });
    await waitFor(() =>
      expect(getBalanceSheet).toHaveBeenCalledWith("co-1", "2026-06-30"),
    );
    expect(JSON.stringify(getBalanceSheet.mock.calls[0])).not.toContain(
      "2026-04-01",
    );
  });

  it("Profit & Loss sends BOTH bounds (it is the one period statement)", async () => {
    const getProfitLoss = vi.fn(async () => ({
      income: { title: "Income", nodes: [], total: "0.0000" },
      expenses: { title: "Expenses", nodes: [], total: "0.0000" },
      net_profit: "0.0000",
    }));
    renderWithSnapshot(ProfitLossScreen, withPeriod("Profit & Loss A/c"), {
      getProfitLoss,
    });
    await waitFor(() =>
      expect(getProfitLoss).toHaveBeenCalledWith(
        "co-1",
        "2026-04-01",
        "2026-06-30",
      ),
    );
  });

  it("re-fetches when the period changes, rather than showing a stale figure", async () => {
    const getTrialBalance = vi.fn(async () => ({
      rows: [], total_debit: "0.0000", total_credit: "0.0000", balanced: true,
    }));
    const { rerender } = renderWithSnapshot(
      TrialBalanceScreen,
      { ...SNAP, reportName: "Trial Balance", config: { periodTo: "2026-06-30" } },
      { getTrialBalance },
    );
    await waitFor(() =>
      expect(getTrialBalance).toHaveBeenCalledWith("co-1", "2026-06-30"),
    );
    void rerender;
    // A second mount with a different period must issue a NEW request; a screen
    // that fetched once on mount would keep showing June's position under a
    // September heading.
    cleanup();
    renderWithSnapshot(
      TrialBalanceScreen,
      { ...SNAP, reportName: "Trial Balance", config: { periodTo: "2026-09-30" } },
      { getTrialBalance },
    );
    await waitFor(() =>
      expect(getTrialBalance).toHaveBeenCalledWith("co-1", "2026-09-30"),
    );
  });
});

/**
 * ⛔ THE AGEING REPORT HAD NO AGEING IN IT.
 *
 * OutstandingsScreen rendered one row per party and dropped `p.bills` — the
 * per-bill array the server has always sent. A party's total is the one figure
 * the Balance Sheet already gives, so with the bills missing NOTHING on ANY
 * screen in this product could show which invoice is open, when it fell due, or
 * how overdue it is. That is not a missing nicety: it means no on-screen surface
 * could verify a bill-wise reference at all, which is why a whole class of
 * bill-wise defects reached a browser undetected.
 *
 * The control is the party row: a change that rendered ONLY bills would satisfy
 * every assertion about bills and would have deleted the report's own summary.
 */
describe("Outstandings shows the BILLS, not just the party totals", () => {
  const twoBills = () =>
    vi.fn(async () => ({
      kind: "RECEIVABLE" as const,
      as_on: "2026-06-30",
      parties: [
        {
          ledger_id: "led-acme",
          ledger: "Acme",
          group: "Sundry Debtors",
          pending: "11800.0000",
          bills: [
            {
              ref_name: "INV/0007",
              pending: "5900.0000",
              due_date: "2026-05-20",
              overdue_days: 41,
              slab: "0-45 days",
            },
            {
              ref_name: "INV/0008",
              pending: "5900.0000",
              due_date: "2026-07-20",
              overdue_days: 0,
              slab: "0-45 days",
            },
          ],
        },
      ],
      slab_totals: [],
      total: "11800.0000",
    }));

  it("renders each bill with its reference, its due date and its ageing", async () => {
    renderReport(OutstandingsScreen, { getOutstandings: twoBills() });

    // THE CONTROL FIRST: the party summary is still there.
    await waitFor(() => expect(screen.getByText("Acme")).toBeTruthy());
    expect(screen.getByTestId("os-total").textContent).toBe("11,800.00");

    // Both bills, each named. Before the fix the screen showed one row reading
    // "Acme … 11,800.00" and an operator could not tell it was two invoices.
    expect(screen.getByText("INV/0007")).toBeTruthy();
    expect(screen.getByText("INV/0008")).toBeTruthy();

    // The due date is ON the row, and the ageing is stated in days rather than
    // only as a slab — the slab is a bucket, the days are the fact.
    expect(screen.getByText(/due 2026-05-20/)).toBeTruthy();
    expect(screen.getByText("41 days overdue")).toBeTruthy();

    // ⚠️ AND A BILL THAT IS NOT YET DUE SAYS SO. An empty cell here would read
    // as "not overdue" for a bill with no due date at all, which is a different
    // fact and the one that hides a bill nobody is chasing.
    expect(screen.getByText("not yet due")).toBeTruthy();
  });

  it("says so when a bill carries no due date, rather than leaving it blank", async () => {
    const getOutstandings = vi.fn(async () => ({
      kind: "RECEIVABLE" as const,
      as_on: "2026-06-30",
      parties: [
        {
          ledger_id: "led-x",
          ledger: "No Terms Ltd",
          group: "Sundry Debtors",
          pending: "1000.0000",
          bills: [
            {
              ref_name: "OPEN-1",
              pending: "1000.0000",
              due_date: "",
              overdue_days: 0,
              slab: "0-45 days",
            },
          ],
        },
      ],
      slab_totals: [],
      total: "1000.0000",
    }));
    renderReport(OutstandingsScreen, { getOutstandings });
    await waitFor(() => expect(screen.getByText("OPEN-1")).toBeTruthy());
    expect(screen.getByText(/no due date recorded/)).toBeTruthy();
    expect(screen.getByText("no due date")).toBeTruthy();
  });
});
