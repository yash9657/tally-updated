import { useCallback } from "react";
import type { NavSnapshot } from "../../keystroke/navMachine";
import {
  GROUP_ROW_PREFIX,
  LEDGER_ROW_PREFIX,
} from "../../keystroke/navMachine";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useLoadedVoucher } from "../../app/LoadedVoucher";
import { DrillRows, ColumnHeader, type DrillRow } from "./DrillRows";
import { formatMoney } from "./money";
import { addMoney } from "../vouchers/money";
import { dayBookRowKey } from "../vouchers/voucherClass";
import { voucherIDFromRowKey } from "../../keystroke/voucherClass";
import {
  useReportFetch,
  useReportView,
  type ReportAvailability,
} from "./reportView";
import { ReportUnavailable } from "./ReportUnavailable";
import { exactCell, moneyCell, textCell } from "../../export/sheet";
import { LEDGER_WISE_DEFAULT } from "../../keystroke/types";
import type {
  BalanceSheetResponse,
  DayBookResponse,
  OutstandingsResponse,
  ProfitLossResponse,
  StatementNode,
  TrialBalanceResponse,
} from "../../api";

/**
 * The Phase-1 report screens, wired to the LIVE client. Each fetches its report
 * on mount (keyed by companyID) and renders the rows from the response. Money is
 * kept as fixed-4 STRINGS throughout (formatted for display via formatMoney,
 * never coerced to a JS number).
 *
 * Drill: the Day Book is voucher-bearing — Enter on a row drills straight into
 * the underlying voucher in alteration mode (the navMachine pushes the alter
 * frame; the AppShell handles the key). To make that drill load the REAL voucher
 * the Day Book screen stashes the highlighted entry into the {@link
 * useLoadedVoucher} store whenever the selection changes, so by the time Enter
 * fires the loaded slot already holds the right voucher (number/date/postings).
 */

type ReportProps = {
  snapshot: NavSnapshot;
  onSelect: (item: string) => void;
};

/** Shared loading/empty wrapper for hermetic tests. */
function ReportFrame({
  title,
  snapshot,
  availability,
  children,
}: {
  title: string;
  /** When given, the active view settings are shown under the heading. */
  snapshot?: NavSnapshot;
  /**
   * Whether the report arrived. A FAILED one replaces the table entirely rather
   * than rendering beside it: a Trial Balance that was refused still has a total
   * row to draw, and "Total 0.00" under a refusal notice is the worse of the two
   * readings on offer. Alt+E answers with the same fact — see reportView.ts.
   */
  availability?: ReportAvailability;
  children: React.ReactNode;
}) {
  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">{title}</h1>
      {snapshot ? <ViewBanner snapshot={snapshot} /> : null}
      <div className="mt-3 bg-tally-panel p-3">
        {availability?.kind === "failed" ? (
          <ReportUnavailable label={title} availability={availability} />
        ) : (
          children
        )}
      </div>
    </section>
  );
}

/**
 * The active view settings, spelled out under the report heading. Tally shows
 * the same thing in its report header; without it a user cannot tell whether a
 * short report is short because of a filter or because the books are empty.
 */
function ViewBanner({ snapshot }: { snapshot: NavSnapshot }) {
  const cfg = snapshot.config ?? {};
  const bits: string[] = [];
  // A POSITION report applies only the upper bound, so it must not print a
  // range it did not use. Saying "Period: X to Y" on a Balance Sheet that
  // ignored X is exactly the kind of quiet mismatch this banner exists to
  // prevent — the report would be right and the label wrong.
  const asAtOnly =
    snapshot.reportName === "Trial Balance" ||
    snapshot.reportName === "Balance Sheet";
  if (asAtOnly && cfg.periodTo) {
    bits.push(`As on ${cfg.periodTo}`);
  } else if (asAtOnly && cfg.periodFrom) {
    bits.push(`As on latest (a ${snapshot.reportName} is a position; set "To" to date it)`);
  } else if (cfg.periodFrom || cfg.periodTo) {
    bits.push(`Period: ${cfg.periodFrom || "…"} to ${cfg.periodTo || "…"}`);
  }
  if (cfg.detailed) bits.push("Detailed");
  if (cfg.hideZeroBalances) bits.push("Zero balances hidden");
  if (cfg.filterText) bits.push(`Filter: "${cfg.filterText}"`);
  if (cfg.vertical) bits.push("Vertical view");
  if (bits.length === 0) return null;
  return (
    <p data-testid="report-view-banner" className="mt-1 text-xs text-tally-title">
      {bits.join("   ·   ")}
    </p>
  );
}

// ---------------------------------------------------------------------------
// Day Book (voucher-bearing; drill loads the real voucher).
// ---------------------------------------------------------------------------

export function DayBookScreen({ snapshot, onSelect }: ReportProps) {
  const client = useClient();
  const companyID = useCompanyID();
  const { setLoaded } = useLoadedVoucher();
  // F2 / Alt+F2 period — the Day Book is period-aware SERVER-side, so a change
  // refetches with from/to rather than filtering what was already loaded.
  const from = snapshot.config?.periodFrom;
  const to = snapshot.config?.periodTo;
  const showNarration = Boolean(snapshot.config?.showNarration);

  const { data, availability } = useReportFetch<DayBookResponse>(
    useCallback(() => {
      const query: { from?: string; to?: string } = {};
      if (from) query.from = from;
      if (to) query.to = to;
      return client.getDayBook(companyID, query);
    }, [client, companyID, from, to]),
  );

  const entries = data?.entries ?? [];
  const rows: DrillRow[] = entries.map((e) => ({
    // ⛔ THE ROW KEY CARRIES THE CLASS, and it has to: the navigation machine's
    // only knowledge of a highlighted row is this string, so a bare voucher_id
    // left it drilling every voucher into a SALES frame — which then re-classed
    // Receipts, Payments, Contras and Journals on Ctrl+A. A class this build has
    // no entry screen for (Payroll) publishes the bare id and is not drilled.
    // See keystroke/voucherClass.ts for why the key and not something nicer.
    rowKey: dayBookRowKey(e),
    values: [
      e.date,
      e.voucher_no,
      e.party || e.narration,
      formatMoney(e.debit),
      formatMoney(e.credit),
    ],
    // Alt+E. The DATE and the VOUCHER NUMBER are `exactCell` and not text: a
    // spreadsheet turns "2026-06-01" into a locale-formatted serial date and
    // "0042" into 42. The two amounts are the WIRE strings, not formatMoney's.
    exportCells: [
      exactCell(e.date),
      exactCell(e.voucher_no),
      textCell(e.party || e.narration),
      moneyCell(e.debit),
      moneyCell(e.credit),
    ],
    cells: (
      <>
        <span>{e.date}</span>
        <span>{e.voucher_no}</span>
        <span>
          {e.party || e.narration}
          {showNarration && e.party && e.narration ? (
            <em className="ml-2 not-italic text-xs opacity-80">{e.narration}</em>
          ) : null}
        </span>
        <span className="text-right">{formatMoney(e.debit)}</span>
        <span className="text-right">{formatMoney(e.credit)}</span>
      </>
    ),
  }));

  // Whenever the highlighted row changes the machine records it as selectedItem
  // (via onSelect). We mirror that into the loaded-voucher store so a subsequent
  // Enter (which pushes the alter frame) finds the right voucher pre-loaded.
  function handleSelect(rowKey: string) {
    // The key is `voucher:<class>:<id>` (or a bare id for a class with no entry
    // screen), so the id has to be decoded rather than compared — matching on the
    // whole key would find nothing and the loaded slot would stay stale, leaving
    // the drill to alter whichever voucher was drilled BEFORE this one.
    const id = voucherIDFromRowKey(rowKey);
    const entry = entries.find((e) => e.voucher_id === id);
    if (entry) setLoaded({ entry });
    onSelect(rowKey);
  }

  const columns = "grid-cols-[7rem_8rem_1fr_8rem_8rem]";
  const headers = ["Date", "No.", "Particulars", "Debit", "Credit"];
  const visible = useReportView(rows, snapshot, {
    title: "Day Book",
    headers,
    availability,
  });

  return (
    <ReportFrame title="Day Book" snapshot={snapshot} availability={availability}>
      <ColumnHeader
        columnsClassName={columns}
        labels={[
          { text: "Date" },
          { text: "No." },
          { text: "Particulars" },
          { text: "Debit", numeric: true },
          { text: "Credit", numeric: true },
        ]}
      />
      {visible.length === 0 ? (
        <p className="px-2 py-1 text-xs" role="status">
          No vouchers in this period.
        </p>
      ) : (
        <DrillRows
          rows={visible}
          onSelect={handleSelect}
          ariaLabel="Day Book entries"
          columnsClassName={columns}
        />
      )}
    </ReportFrame>
  );
}

// ---------------------------------------------------------------------------
// Trial Balance.
// ---------------------------------------------------------------------------

export function TrialBalanceScreen({ snapshot, onSelect }: ReportProps) {
  const client = useClient();
  const companyID = useCompanyID();
  // A Trial Balance is a POSITION, so the F2 period's "To" becomes `as on` and
  // the "From" is deliberately not sent — bounding it below would drop the
  // entity's earlier history and still foot. The banner says "as on <date>" so
  // the screen never claims a range it did not ask for.
  const asOn = snapshot.config?.periodTo;
  const { data, availability } = useReportFetch<TrialBalanceResponse>(
    useCallback(
      () => client.getTrialBalance(companyID, asOn),
      [client, companyID, asOn],
    ),
  );

  // F5 — ledger-wise (the default here) ⇄ group-wise, which sums each ledger's
  // debit/credit into its parent group exactly as Tally's group-wise Trial
  // Balance does. Money is summed as fixed-4 STRINGS via addMoney, never floats.
  const ledgerWise = snapshot.config?.ledgerWise ?? LEDGER_WISE_DEFAULT;
  const source = data?.rows ?? [];
  const lines = ledgerWise
    ? source.map((r) => ({ label: r.ledger, debit: r.debit, credit: r.credit }))
    : groupWise(source);

  const rows: DrillRow[] = lines.map((r) => ({
    // Ledger-wise rows drill into that ledger's vouchers; group-wise rows drill
    // into the group's ledgers first (Tally's group > ledger > voucher path).
    rowKey: `${ledgerWise ? LEDGER_ROW_PREFIX : GROUP_ROW_PREFIX}${r.label}`,
    values: [r.label, formatMoney(r.debit), formatMoney(r.credit)],
    exportCells: [textCell(r.label), moneyCell(r.debit), moneyCell(r.credit)],
    cells: (
      <>
        <span>{r.label}</span>
        <span className="text-right">{formatMoney(r.debit)}</span>
        <span className="text-right">{formatMoney(r.credit)}</span>
      </>
    ),
  }));
  rows.push({
    rowKey: "__total__",
    drillable: false,
    values: ["Total", formatMoney(data?.total_debit), formatMoney(data?.total_credit)],
    exportCells: [
      textCell("Total"),
      moneyCell(data?.total_debit),
      moneyCell(data?.total_credit),
    ],
    cells: (
      <>
        <span>Total</span>
        <span className="text-right" data-testid="tb-total-debit">
          {formatMoney(data?.total_debit)}
        </span>
        <span className="text-right" data-testid="tb-total-credit">
          {formatMoney(data?.total_credit)}
        </span>
      </>
    ),
  });

  const columns = "grid-cols-[1fr_8rem_8rem]";
  const visible = useReportView(rows, snapshot, {
    title: "Trial Balance",
    headers: [ledgerWise ? "Ledger" : "Group", "Debit", "Credit"],
    availability,
  });
  return (
    <ReportFrame
      title="Trial Balance"
      snapshot={snapshot}
      availability={availability}
    >
      <ColumnHeader
        columnsClassName={columns}
        labels={[
          { text: ledgerWise ? "Ledger" : "Group" },
          { text: "Debit", numeric: true },
          { text: "Credit", numeric: true },
        ]}
      />
      <DrillRows
        rows={visible}
        onSelect={onSelect}
        ariaLabel="Trial Balance rows"
        columnsClassName={columns}
      />
    </ReportFrame>
  );
}

/** Aggregate trial-balance ledger rows into their parent groups (F5 off). */
function groupWise(
  rows: readonly { ledger: string; group: string; debit: string; credit: string }[],
): { label: string; debit: string; credit: string }[] {
  const byGroup = new Map<string, { debit: string; credit: string }>();
  for (const r of rows) {
    const key = r.group || "(ungrouped)";
    const acc = byGroup.get(key) ?? { debit: "0.0000", credit: "0.0000" };
    byGroup.set(key, {
      debit: addMoney(acc.debit, r.debit),
      credit: addMoney(acc.credit, r.credit),
    });
  }
  return [...byGroup.entries()].map(([label, v]) => ({ label, ...v }));
}

// ---------------------------------------------------------------------------
// Balance Sheet + Profit & Loss (statement trees).
// ---------------------------------------------------------------------------

function flattenNodes(nodes: readonly StatementNode[], depth = 0): DrillRow[] {
  const out: DrillRow[] = [];
  for (const n of nodes) {
    out.push({
      // The row key carries the row's KIND (group vs ledger), which is how the
      // machine knows a group drills one more level than a ledger. Names — not
      // uuids — because the drill screen resolves against the Trial Balance and
      // the Day Book, which speak in names.
      rowKey:
        n.kind === "GROUP"
          ? `${GROUP_ROW_PREFIX}${n.name}`
          : `${LEDGER_ROW_PREFIX}${n.name}`,
      depth,
      drillable: n.kind !== "COMPUTED",
      values: [n.name, formatMoney(n.amount)],
      exportCells: [textCell(n.name), moneyCell(n.amount)],
      cells: (
        <>
          <span>{n.name}</span>
          <span className="text-right">{formatMoney(n.amount)}</span>
        </>
      ),
    });
    if (n.children?.length) out.push(...flattenNodes(n.children, depth + 1));
  }
  return out;
}

export function BalanceSheetScreen({ snapshot, onSelect }: ReportProps) {
  const client = useClient();
  const companyID = useCompanyID();
  // Same as the Trial Balance: a Balance Sheet is a position AS AT a date.
  const asOn = snapshot.config?.periodTo;
  const { data, availability } = useReportFetch<BalanceSheetResponse>(
    useCallback(
      () => client.getBalanceSheet(companyID, asOn),
      [client, companyID, asOn],
    ),
  );

  const columns = "grid-cols-[1fr_10rem]";
  // Ctrl+H Change View: Tally's horizontal Balance Sheet puts Liabilities on
  // the left and Assets on the right; the vertical view stacks Sources of Funds
  // above Application of Funds. We render one column either way (a terminal
  // grid), so the view flips the SECTION ORDER and their titles, which is the
  // part a user is actually reading.
  const vertical = Boolean(snapshot.config?.vertical);
  const rows: DrillRow[] = [];
  if (data) {
    rows.push(
      sectionHeaderRow(
        "__liabilities__",
        vertical ? "Sources of Funds" : data.liabilities.title,
        data.liabilities.total,
        "bs-liabilities-total",
      ),
      ...flattenNodes(data.liabilities.nodes, 1),
      sectionHeaderRow(
        "__assets__",
        vertical ? "Application of Funds" : data.assets.title,
        data.assets.total,
        "bs-assets-total",
      ),
      ...flattenNodes(data.assets.nodes, 1),
    );
  }

  const visible = useReportView(rows, snapshot, {
    title: "Balance Sheet",
    headers: ["Particulars", "Amount"],
    availability,
  });

  return (
    <ReportFrame
      title="Balance Sheet"
      snapshot={snapshot}
      availability={availability}
    >
      <ColumnHeader
        columnsClassName={columns}
        labels={[
          { text: vertical ? "Particulars (vertical)" : "Particulars" },
          { text: "Amount", numeric: true },
        ]}
      />
      <DrillRows
        rows={visible}
        onSelect={onSelect}
        ariaLabel="Balance Sheet rows"
        columnsClassName={columns}
      />
    </ReportFrame>
  );
}

/** A non-drillable section heading row carrying the section total. */
function sectionHeaderRow(
  key: string,
  title: string,
  total: string,
  testId: string,
): DrillRow {
  return {
    rowKey: key,
    drillable: false,
    values: [title, formatMoney(total)],
    exportCells: [textCell(title), moneyCell(total)],
    cells: (
      <>
        <span>{title}</span>
        <span className="text-right" data-testid={testId}>
          {formatMoney(total)}
        </span>
      </>
    ),
  };
}

export function ProfitLossScreen({ snapshot, onSelect }: ReportProps) {
  const client = useClient();
  const companyID = useCompanyID();
  // The ONE statement of the three for which a lower bound is meaningful: a P&L
  // is a statement FOR A PERIOD, so both F2 bounds are sent.
  const from = snapshot.config?.periodFrom;
  const to = snapshot.config?.periodTo;
  const { data, availability } = useReportFetch<ProfitLossResponse>(
    useCallback(
      () => client.getProfitLoss(companyID, from, to),
      [client, companyID, from, to],
    ),
  );

  const columns = "grid-cols-[1fr_10rem]";
  const rows: DrillRow[] = [];
  if (data) {
    rows.push(
      sectionHeaderRow(
        "__expenses__",
        data.expenses.title,
        data.expenses.total,
        "pl-expenses-total",
      ),
      ...flattenNodes(data.expenses.nodes, 1),
      sectionHeaderRow(
        "__income__",
        data.income.title,
        data.income.total,
        "pl-income-total",
      ),
      ...flattenNodes(data.income.nodes, 1),
      sectionHeaderRow(
        "__net__",
        "Net Profit",
        data.net_profit,
        "pl-net-profit",
      ),
    );
  }

  const visible = useReportView(rows, snapshot, {
    title: "Profit and Loss",
    headers: ["Particulars", "Amount"],
    availability,
    // The FILE is "Profit-and-Loss.csv"; the SCREEN is headed "Profit & Loss
    // A/c". A refusal names the screen, because that is what the operator is
    // looking at when they read the status line.
    label: "Profit & Loss A/c",
  });

  return (
    <ReportFrame
      title="Profit & Loss A/c"
      snapshot={snapshot}
      availability={availability}
    >
      <ColumnHeader
        columnsClassName={columns}
        labels={[{ text: "Particulars" }, { text: "Amount", numeric: true }]}
      />
      <DrillRows
        rows={visible}
        onSelect={onSelect}
        ariaLabel="Profit and Loss rows"
        columnsClassName={columns}
      />
    </ReportFrame>
  );
}

// ---------------------------------------------------------------------------
// Outstandings.
// ---------------------------------------------------------------------------

/**
 * The ONE writing of the Outstandings scope sentence.
 *
 * ⛔⛔ THE OLD CAPTION WAS `data.as_on ? "As on " + data.as_on : null`, AND D86 IS
 * WHY THAT IS NOT ENOUGH. The server echoed the RESOLVED as-on — today — even when
 * the caller had named no date, so the screen said "As on 2026-08-24" over a table
 * that included next month's invoices. The sentence was present, well-worded and
 * false, which is the class of defect that survives longest because a caption that
 * reads well is never re-read.
 *
 * Two things had to change together: the server now sends `as_on: ""` for an
 * unbounded read (the convention `/stock-summary` already uses), and this function
 * turns that empty string into a sentence that says so and names the key that
 * changes it. ⚠️ A falsy-but-absent `as_on` would also have hidden the caption
 * entirely, which is quieter but no more honest — an operator cannot tell an
 * unbounded report from one whose caption failed to render.
 *
 * ⚠️ `aged_as_on` IS A DIFFERENT FACT and is stated in both branches: the ageing
 * slabs are meaningless without the day they were measured against, and dropping it
 * would trade a false caption for a missing one.
 */
export function outstandingsScopeLine(asOn: string, agedAsOn: string): string {
  const bound = (asOn ?? "").trim();
  const aged = (agedAsOn ?? "").trim();
  if (bound !== "") {
    return `As on ${bound}` + (aged && aged !== bound ? ` · aged against ${aged}` : "");
  }
  return (
    "NO DATE LIMIT — every bill on these books, including any dated AFTER today, " +
    "so this agrees with Sundry Debtors/Creditors on the Balance Sheet" +
    (aged ? `. Ageing measured against ${aged}` : "") +
    ". Press F2 and set a To date to read it as at a day."
  );
}

export function OutstandingsScreen({ snapshot, onSelect }: ReportProps) {
  const client = useClient();
  const companyID = useCompanyID();
  // F2 period: Outstandings is an AS-ON report, so the period's "To" date is
  // what it honours (server-side as_on) — the From date does not apply to it.
  const asOn = snapshot.config?.periodTo;

  const { data, availability } = useReportFetch<OutstandingsResponse>(
    useCallback(() => {
      const query: { as_on?: string } = {};
      if (asOn) query.as_on = asOn;
      return client.getOutstandings(companyID, query);
    }, [client, companyID, asOn]),
  );

  const columns = "grid-cols-[1fr_8rem_8rem]";
  // ⛔ THE BILLS ARE THE REPORT. This screen rendered ONE ROW PER PARTY and
  // dropped `p.bills` on the floor, which made it an ageing report with no
  // ageing in it: a party's total is the one figure a Balance Sheet already
  // gives, so nothing on any screen in this product could show WHICH invoice is
  // open, when it fell due, or how far overdue it is — and therefore nothing
  // could verify a bill-wise reference at all. The server has always sent the
  // per-bill array; the client simply never read it.
  //
  // ⚠️ DEPTH 1, WHICH IS WHAT KEEPS THEM VISIBLE. reportView's condensed
  // (default) view keeps rows at depth <= 1 and drops deeper ones, so a bill at
  // depth 1 shows without an operator having to know about F12 — the mistake the
  // Balance Sheet's Closing Stock row makes at depth 2.
  //
  // The bill row is NOT drillable: its rowKey names no ledger, and DrillRows'
  // Enter handler would otherwise open whatever the party prefix resolved to.
  const rows: DrillRow[] = [];
  for (const p of data?.parties ?? []) {
    rows.push({
      rowKey: `${LEDGER_ROW_PREFIX}${p.ledger}`,
      values: [p.ledger, p.group, formatMoney(p.pending)],
      exportCells: [textCell(p.ledger), textCell(p.group), moneyCell(p.pending)],
      cells: (
        <>
          <span>{p.ledger}</span>
          <span>{p.group}</span>
          <span className="text-right">{formatMoney(p.pending)}</span>
        </>
      ),
    });
    for (const b of p.bills ?? []) {
      // The ageing, in the operator's words. "due 2026-05-20 · 47 days overdue"
      // beats a bare slab label, because the slab is a bucket and the days are
      // the fact — and a bill with no due date says so rather than reading as
      // "not overdue", which is what an empty cell would imply.
      const age =
        b.overdue_days > 0
          ? `${b.overdue_days} days overdue`
          : b.due_date
            ? "not yet due"
            : "no due date";
      const when = b.due_date ? `due ${b.due_date}` : "no due date recorded";
      rows.push({
        rowKey: `${LEDGER_ROW_PREFIX}${p.ledger}/${b.ref_name}`,
        depth: 1,
        drillable: false,
        values: [`${b.ref_name} — ${when}`, age, formatMoney(b.pending)],
        exportCells: [
          textCell(b.ref_name),
          textCell(b.due_date),
          moneyCell(b.pending),
        ],
        cells: (
          <>
            <span className="pl-4">
              {b.ref_name}
              <span className="text-tally-field"> — {when}</span>
            </span>
            <span className="text-tally-field">{age}</span>
            <span className="text-right">{formatMoney(b.pending)}</span>
          </>
        ),
      });
    }
  }
  rows.push({
    rowKey: "__total__",
    drillable: false,
    values: ["Total", "", formatMoney(data?.total)],
    exportCells: [textCell("Total"), textCell(""), moneyCell(data?.total)],
    cells: (
      <>
        <span>Total</span>
        <span />
        <span className="text-right" data-testid="os-total">
          {formatMoney(data?.total)}
        </span>
      </>
    ),
  });

  const visible = useReportView(rows, snapshot, {
    title: "Outstandings",
    // ⚠️ THE HEADERS NAME BOTH ROW KINDS, because the sheet now carries both. A
    // header reading "Group" over a column that holds a due date on every second
    // row is the Stock Summary export's defect (a quantity under a header the
    // next column's money contradicts) — declared here instead.
    headers: ["Party / Bill ref", "Group / Due date", "Pending"],
    availability,
  });

  return (
    <ReportFrame
      title="Outstandings"
      snapshot={snapshot}
      availability={availability}
    >
      <ColumnHeader
        columnsClassName={columns}
        labels={[
          { text: "Party" },
          { text: "Group" },
          { text: "Pending", numeric: true },
        ]}
      />
      <DrillRows
        rows={visible}
        onSelect={onSelect}
        ariaLabel="Outstandings rows"
        columnsClassName={columns}
      />
      {data ? (
        <p className="mt-2 text-xs" data-testid="os-as-on">
          {outstandingsScopeLine(data.as_on, data.aged_as_on)}
        </p>
      ) : null}
    </ReportFrame>
  );
}
