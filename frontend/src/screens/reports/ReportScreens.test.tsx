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
  BillsPayableScreen,
  outstandingsScopeLine,
  onAccountLine,
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
      aged_as_on: "2026-06-30",
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
      on_account_total: "0.0000",
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
      aged_as_on: "2026-06-30",
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
              on_account: false,
              currency_code: "",
              foreign_pending: "0.0000",
              currency_conflict: false,
            },
            {
              ref_name: "INV/0008",
              pending: "5900.0000",
              due_date: "2026-07-20",
              overdue_days: 0,
              slab: "0-45 days",
              on_account: false,
              currency_code: "",
              foreign_pending: "0.0000",
              currency_conflict: false,
            },
          ],
        },
      ],
      slab_totals: [],
      total: "11800.0000",
      on_account_total: "0.0000",
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
      aged_as_on: "2026-06-30",
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
              on_account: false,
              currency_code: "",
              foreign_pending: "0.0000",
              currency_conflict: false,
            },
          ],
        },
      ],
      slab_totals: [],
      total: "1000.0000",
      on_account_total: "0.0000",
    }));
    renderReport(OutstandingsScreen, { getOutstandings });
    await waitFor(() => expect(screen.getByText("OPEN-1")).toBeTruthy());
    expect(screen.getByText(/no due date recorded/)).toBeTruthy();
    expect(screen.getByText("no due date")).toBeTruthy();
  });
});


// ---------------------------------------------------------------------------
// D86 — what the ageing report actually covers, and what its caption claims
// ---------------------------------------------------------------------------

describe("outstandingsScopeLine — the scope the ageing report actually has", () => {
  it("says NO DATE LIMIT, and names post-dated bills, when the server bounded nothing", () => {
    const line = outstandingsScopeLine("", "2026-08-24");
    // ⛔ THE MUTATION THIS ARM EXISTS FOR: restore `as_on ? "As on " + as_on : null`
    // — the caption either claims a boundary the query no longer applies, or (with a
    // falsy `as_on`) vanishes entirely and the operator cannot tell an unbounded
    // report from a caption that failed to render. Both fail here.
    expect(line).toMatch(/no date limit/i);
    expect(line).toMatch(/after today/i);
    expect(line).not.toMatch(/^As on /);
    // ⛔ AND IT STILL STATES THE AGEING DAY. Dropping that would trade a false
    // caption for a missing one: the slab labels mean nothing without it.
    expect(line).toContain("2026-08-24");
    // It names the key that changes the answer, so the sentence is actionable.
    expect(line).toMatch(/F2/);
  });

  it("says As on <date> when the caller DID name one — the bound is conditional, not gone", () => {
    // ⛔ THE CONTROL. "Remove the caption" and "always say NO DATE LIMIT" both pass
    // the arm above; only this one refuses them.
    expect(outstandingsScopeLine("2026-04-30", "2026-04-30")).toBe("As on 2026-04-30");
    expect(outstandingsScopeLine("2026-04-30", "2026-04-30")).not.toMatch(/no date limit/i);
  });

  it("names the ageing day separately when it differs from the bound", () => {
    // Two facts, two statements. One field could not carry both, which is why the
    // response has `as_on` AND `aged_as_on`.
    expect(outstandingsScopeLine("2026-04-30", "2026-08-24")).toBe(
      "As on 2026-04-30 · aged against 2026-08-24",
    );
  });
});


// ---------------------------------------------------------------------------
// D94 — the payables ageing has its own door, and therefore its own RULE
// ---------------------------------------------------------------------------

describe("BillsPayableScreen — the door is the authorisation", () => {
  it("⛔⛔ calls getBillsPayable (its own ROUTE), never getOutstandings with a kind", async () => {
    // ⛔ THIS IS THE ONE ARM THAT CATCHES THE WRONG BUILD, AND IT IS NOT ABOUT
    // RENDERING. A payables screen written as
    // `getOutstandings(co, {kind: "PAYABLE"})` renders IDENTICALLY — same parties,
    // same bills, same totals — and puts the payables book back under
    // `report:outstandings`, because cored maps the FLAT LITERAL PATH to the PDP
    // resource and a query parameter never reaches it. The screen would look right
    // and the authorisation would be wrong, which is exactly the state D94 exists
    // to leave. Nothing visual can tell the two apart; only the call can.
    const client = createMockClient({ seed: false });
    const payable = vi.spyOn(client, "getBillsPayable");
    const receivable = vi.spyOn(client, "getOutstandings");

    renderReport(BillsPayableScreen, client);
    await waitFor(() => expect(payable).toHaveBeenCalled());
    expect(receivable).not.toHaveBeenCalled();
    // And it sends NO kind: the route IS the kind, and honouring one would hand
    // back the receivables list from under the payables rule.
    const [, query] = payable.mock.calls[0]!;
    expect(query ?? {}).not.toHaveProperty("kind");
  });

  it("⛔ CONTROL: OutstandingsScreen still calls getOutstandings, so the split is real", async () => {
    // Without this, "point both screens at getBillsPayable" passes the arm above.
    const client = createMockClient({ seed: false });
    const payable = vi.spyOn(client, "getBillsPayable");
    const receivable = vi.spyOn(client, "getOutstandings");

    renderReport(OutstandingsScreen, client);
    await waitFor(() => expect(receivable).toHaveBeenCalled());
    expect(payable).not.toHaveBeenCalled();
  });
});

/**
 * ⛔⛔ D106 — THE ON-ACCOUNT ROW MUST NOT READ LIKE A BILL WITH A MISSING DUE DATE.
 *
 * The row is the part of a party's balance that names no reference. Rendered
 * through the ordinary branch it comes out as "no due date recorded" / "no due
 * date", which invites an operator to go and record one — there is nothing to
 * record, and the absence IS the fact being reported.
 *
 * ⚠️ AND THE SLABS ARE SHORT OF THE TOTAL BY EXACTLY THIS ROW, which is the shape
 * a reader checks by adding up. Tally leaves that silent; this screen says it.
 */
describe("Outstandings states its on-account money (D106)", () => {
  const withOnAccount = () =>
    vi.fn(async () => ({
      kind: "RECEIVABLE" as const,
      as_on: "",
      aged_as_on: "2026-08-30",
      parties: [
        {
          ledger_id: "led-acme",
          ledger: "Acme",
          group: "Sundry Debtors",
          pending: "17000.0000",
          bills: [
            {
              ref_name: "INV/0007",
              pending: "24000.0000",
              due_date: "2026-05-12",
              overdue_days: 110,
              slab: "90-180 days",
              on_account: false,
              currency_code: "",
              foreign_pending: "0.0000",
              currency_conflict: false,
            },
            {
              ref_name: "On Account",
              pending: "-7000.0000",
              due_date: "",
              overdue_days: 0,
              slab: "",
              on_account: true,
              currency_code: "",
              foreign_pending: "0.0000",
              currency_conflict: false,
            },
          ],
        },
      ],
      slab_totals: [{ label: "90-180 days", amount: "24000.0000" }],
      total: "17000.0000",
      on_account_total: "-7000.0000",
    }));

  it("says the money is unapplied rather than that a due date is missing", async () => {
    renderReport(OutstandingsScreen, { getOutstandings: withOnAccount() });

    // THE CONTROL FIRST: the real bill still reads as a bill, with its ageing.
    await waitFor(() => expect(screen.getByText("INV/0007")).toBeTruthy());
    expect(screen.getByText(/due 2026-05-12/)).toBeTruthy();
    expect(screen.getByText("110 days overdue")).toBeTruthy();

    // ⛔ AND THE ON-ACCOUNT ROW SAYS WHAT IT IS.
    expect(screen.getByText("On Account")).toBeTruthy();
    expect(screen.getByText(/received, not applied to any bill/)).toBeTruthy();
    expect(screen.getByText("not aged — in no slab")).toBeTruthy();
    // The wording that would be WRONG here — it invites a correction that cannot
    // be made, because there is no bill whose due date could be filled in.
    expect(screen.queryByText(/no due date recorded/)).toBeNull();
  });

  it("discloses that the slabs are short of the total by exactly that much", async () => {
    renderReport(OutstandingsScreen, { getOutstandings: withOnAccount() });
    await waitFor(() =>
      expect(screen.getByTestId("os-on-account")).toBeTruthy(),
    );
    const said = screen.getByTestId("os-on-account").textContent ?? "";
    expect(said).toMatch(/ON ACCOUNT/);
    expect(said).toMatch(/NOT in any ageing slab/);
  });

  it("says nothing at all when there is no on-account money", async () => {
    // ⛔ THE CONTROL ON THE DISCLOSURE ITSELF. A caption about a case that does
    // not apply is noise, and noise is how a real disclosure stops being read.
    renderReport(OutstandingsScreen, { getOutstandings: twoBillsNoOnAccount() });
    await waitFor(() => expect(screen.getByText("INV/0007")).toBeTruthy());
    expect(screen.queryByTestId("os-on-account")).toBeNull();
  });

  const twoBillsNoOnAccount = () =>
    vi.fn(async () => ({
      kind: "RECEIVABLE" as const,
      as_on: "",
      aged_as_on: "2026-08-30",
      parties: [
        {
          ledger_id: "led-acme",
          ledger: "Acme",
          group: "Sundry Debtors",
          pending: "24000.0000",
          bills: [
            {
              ref_name: "INV/0007",
              pending: "24000.0000",
              due_date: "2026-05-12",
              overdue_days: 110,
              slab: "90-180 days",
              on_account: false,
              currency_code: "",
              foreign_pending: "0.0000",
              currency_conflict: false,
            },
          ],
        },
      ],
      slab_totals: [{ label: "90-180 days", amount: "24000.0000" }],
      total: "24000.0000",
      on_account_total: "0.0000",
    }));
});

/**
 * The sentence, held to its rule directly — cheaper than a rendered screen, and
 * the reason `onAccountLine` is exported at all.
 */
describe("onAccountLine", () => {
  it("reads money HELD as received-and-unapplied", () => {
    const said = onAccountLine("-7000.0000");
    expect(said).toMatch(/received and not yet applied/);
    expect(said).toMatch(/NOT in any ageing slab/);
    // ⛔ IT TELLS THE OPERATOR WHAT TO DO. A disclosure that names a problem and
    // no remedy just teaches people to scroll past it.
    expect(said).toMatch(/alter the voucher/);
  });

  it("reads the other direction as an amount with no invoice raised", () => {
    // ⚠️ THE SIGN IS NOT COSMETIC. A positive on-account figure is money the party
    // owes that no invoice covers — the opposite bookkeeping situation from an
    // unapplied receipt, and calling it "received" would be plainly false.
    expect(onAccountLine("7000.0000")).toMatch(/no invoice has been raised/);
  });
});

/**
 * ⭐⭐ T8 STEP 6 — OUTSTANDINGS SHOWS A FOREIGN BILL AS FOREIGN (Q5).
 *
 * Before step 6 `Bill` carried ONE money column and every bill landed in it, so an
 * exporter with $1,000 outstanding read a rupee figure that was correct, ageing that
 * was correct, and could not tell from any screen in this product that the debt was
 * in dollars at all.
 *
 * ⚠️ EVERY TEST HERE CARRIES ITS BASE CONTROL. A screen that stamped a currency on
 * every row, or that rendered the column for nobody, would satisfy half of these on
 * its own.
 */
describe("Outstandings reports the foreign half of a foreign bill", () => {
  const oneOfEach = () =>
    vi.fn(async () => ({
      kind: "RECEIVABLE" as const,
      as_on: "",
      aged_as_on: "2026-06-30",
      parties: [
        {
          ledger_id: "led-fx",
          ledger: "Foreign Exports Ltd",
          group: "Sundry Debtors",
          pending: "66920.9840",
          bills: [
            {
              ref_name: "EXP-1",
              pending: "66920.9840",
              due_date: "2026-05-20",
              overdue_days: 41,
              slab: "0-45 days",
              on_account: false,
              currency_code: "USD",
              foreign_pending: "800.0000",
              currency_conflict: false,
            },
          ],
        },
        {
          ledger_id: "led-inr",
          ledger: "Domestic Traders",
          group: "Sundry Debtors",
          pending: "30000.0000",
          bills: [
            {
              ref_name: "DOM-1",
              pending: "30000.0000",
              due_date: "2026-06-30",
              overdue_days: 0,
              slab: "0-45 days",
              on_account: false,
              currency_code: "",
              foreign_pending: "0.0000",
              currency_conflict: false,
            },
          ],
        },
      ],
      slab_totals: [{ label: "0-45 days", amount: "96920.9840" }],
      total: "96920.9840",
      on_account_total: "0.0000",
    }));

  it("prints the currency and the foreign amount, and leaves a rupee bill blank", async () => {
    renderReport(OutstandingsScreen, { getOutstandings: oneOfEach() });

    await waitFor(() =>
      expect(screen.getByText("Foreign Exports Ltd")).toBeTruthy(),
    );

    // THE SUBJECT: the dollar figure is on the page, named as dollars.
    expect(screen.getByText("USD 800.00")).toBeTruthy();
    // ⛔ AND THE RUPEE FIGURE IS STILL THERE AND UNCHANGED. It is the figure that
    // ties to Sundry Debtors, and the foreign column is beside it, never instead
    // of it.
    // ⚠️ getAllByText: this party holds exactly one bill, so the party total and
    // the bill row carry the same figure — which is the report working correctly.
    expect(screen.getAllByText("66,920.98").length).toBe(2);

    // ⭐ THE CONTROL: the base bill renders no currency at all. A screen that put
    // "INR 30,000.00" here would be inventing a currency master that 0080's own
    // trigger refuses to let exist.
    expect(screen.getByText("DOM-1")).toBeTruthy();
    expect(screen.queryByText(/INR/)).toBeNull();
    expect(screen.queryByText(/^ 0.00$/)).toBeNull();

    // ⚠️ THE GRAND TOTAL IS BASE-ONLY (Q5). Not 96,920.98 + 800.
    expect(screen.getByTestId("os-total").textContent).toBe("96,920.98");
  });

  it("marks BOTH rows of a reference that is open under two currencies", async () => {
    // ⛔ THE CASE STEP 5 DELIBERATELY DOES NOT REFUSE: a rupee receipt allocated
    // against a dollar bill. The server returns two rows under one bill number,
    // because netting them would be a sum of two currencies; unmarked, that reads
    // as a customer who numbered two invoices alike.
    const conflicted = vi.fn(async () => ({
      kind: "RECEIVABLE" as const,
      as_on: "",
      aged_as_on: "2026-06-30",
      parties: [
        {
          ledger_id: "led-fx",
          ledger: "Foreign Exports Ltd",
          group: "Sundry Debtors",
          pending: "0.0000",
          bills: [
            {
              ref_name: "EXP-1",
              pending: "83651.2300",
              due_date: "2026-05-20",
              overdue_days: 41,
              slab: "0-45 days",
              on_account: false,
              currency_code: "USD",
              foreign_pending: "1000.0000",
              currency_conflict: true,
            },
            {
              ref_name: "EXP-1",
              pending: "-83651.2300",
              due_date: "",
              overdue_days: 0,
              slab: "0-45 days",
              on_account: false,
              currency_code: "",
              foreign_pending: "0.0000",
              currency_conflict: true,
            },
          ],
        },
      ],
      slab_totals: [{ label: "0-45 days", amount: "0.0000" }],
      total: "0.0000",
      on_account_total: "0.0000",
    }));
    renderReport(OutstandingsScreen, { getOutstandings: conflicted });

    await waitFor(() =>
      expect(screen.getByText("Foreign Exports Ltd")).toBeTruthy(),
    );

    // ⛔⛔ BOTH ROWS ARE PRESENT AND BOTH ARE MARKED. A row key that ignored the
    // currency would collide and DrillRows would drop the second row — hiding
    // exactly the half of the disclosure the marker exists to show.
    const marked = screen.getAllByText(/EXP-1 ⚠ two currencies/);
    expect(marked.length).toBe(2);
    // The dollar exposure that was never settled is on the page.
    expect(screen.getByText("USD 1,000.00")).toBeTruthy();
    // And so is the negative rupee row that says a rupee receipt cleared it.
    expect(screen.getByText("-83,651.23")).toBeTruthy();
  });

  it("does not mark an ordinary foreign bill (the inverse control)", async () => {
    renderReport(OutstandingsScreen, { getOutstandings: oneOfEach() });
    await waitFor(() =>
      expect(screen.getByText("Foreign Exports Ltd")).toBeTruthy(),
    );
    // A disclosure that fires on the normal case is noise, and noise is how a real
    // disclosure stops being read.
    expect(screen.queryByText(/two currencies/)).toBeNull();
  });
});
