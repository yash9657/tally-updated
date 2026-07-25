import { useEffect, useState } from "react";
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
import { useReportView } from "./reportView";
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
  children,
}: {
  title: string;
  /** When given, the active view settings are shown under the heading. */
  snapshot?: NavSnapshot;
  children: React.ReactNode;
}) {
  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">{title}</h1>
      {snapshot ? <ViewBanner snapshot={snapshot} /> : null}
      <div className="mt-3 bg-tally-panel p-3">{children}</div>
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
  if (cfg.periodFrom || cfg.periodTo) {
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
  const [data, setData] = useState<DayBookResponse | null>(null);
  // F2 / Alt+F2 period — the Day Book is period-aware SERVER-side, so a change
  // refetches with from/to rather than filtering what was already loaded.
  const from = snapshot.config?.periodFrom;
  const to = snapshot.config?.periodTo;
  const showNarration = Boolean(snapshot.config?.showNarration);

  useEffect(() => {
    let cancelled = false;
    const query: { from?: string; to?: string } = {};
    if (from) query.from = from;
    if (to) query.to = to;
    client.getDayBook(companyID, query).then((res) => {
      if (!cancelled) setData(res);
    });
    return () => {
      cancelled = true;
    };
  }, [client, companyID, from, to]);

  const entries = data?.entries ?? [];
  const rows: DrillRow[] = entries.map((e) => ({
    rowKey: e.voucher_id,
    values: [
      e.date,
      e.voucher_no,
      e.party || e.narration,
      formatMoney(e.debit),
      formatMoney(e.credit),
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
    const entry = entries.find((e) => e.voucher_id === rowKey);
    if (entry) setLoaded({ entry });
    onSelect(rowKey);
  }

  const columns = "grid-cols-[7rem_8rem_1fr_8rem_8rem]";
  const headers = ["Date", "No.", "Particulars", "Debit", "Credit"];
  const visible = useReportView(rows, snapshot, { title: "Day Book", headers });

  return (
    <ReportFrame title="Day Book" snapshot={snapshot}>
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
  const [data, setData] = useState<TrialBalanceResponse | null>(null);

  useEffect(() => {
    let cancelled = false;
    client.getTrialBalance(companyID).then((res) => {
      if (!cancelled) setData(res);
    });
    return () => {
      cancelled = true;
    };
  }, [client, companyID]);

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
  });
  return (
    <ReportFrame title="Trial Balance" snapshot={snapshot}>
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
  const [data, setData] = useState<BalanceSheetResponse | null>(null);

  useEffect(() => {
    let cancelled = false;
    client.getBalanceSheet(companyID).then((res) => {
      if (!cancelled) setData(res);
    });
    return () => {
      cancelled = true;
    };
  }, [client, companyID]);

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
  });

  return (
    <ReportFrame title="Balance Sheet" snapshot={snapshot}>
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
  const [data, setData] = useState<ProfitLossResponse | null>(null);

  useEffect(() => {
    let cancelled = false;
    client.getProfitLoss(companyID).then((res) => {
      if (!cancelled) setData(res);
    });
    return () => {
      cancelled = true;
    };
  }, [client, companyID]);

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
  });

  return (
    <ReportFrame title="Profit & Loss A/c" snapshot={snapshot}>
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

export function OutstandingsScreen({ snapshot, onSelect }: ReportProps) {
  const client = useClient();
  const companyID = useCompanyID();
  const [data, setData] = useState<OutstandingsResponse | null>(null);
  // F2 period: Outstandings is an AS-ON report, so the period's "To" date is
  // what it honours (server-side as_on) — the From date does not apply to it.
  const asOn = snapshot.config?.periodTo;

  useEffect(() => {
    let cancelled = false;
    const query: { as_on?: string } = {};
    if (asOn) query.as_on = asOn;
    client.getOutstandings(companyID, query).then((res) => {
      if (!cancelled) setData(res);
    });
    return () => {
      cancelled = true;
    };
  }, [client, companyID, asOn]);

  const columns = "grid-cols-[1fr_8rem_8rem]";
  const rows: DrillRow[] = (data?.parties ?? []).map((p) => ({
    rowKey: `${LEDGER_ROW_PREFIX}${p.ledger}`,
    values: [p.ledger, p.group, formatMoney(p.pending)],
    cells: (
      <>
        <span>{p.ledger}</span>
        <span>{p.group}</span>
        <span className="text-right">{formatMoney(p.pending)}</span>
      </>
    ),
  }));
  rows.push({
    rowKey: "__total__",
    drillable: false,
    values: ["Total", "", formatMoney(data?.total)],
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
    headers: ["Party", "Group", "Pending"],
  });

  return (
    <ReportFrame title="Outstandings" snapshot={snapshot}>
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
      {data?.as_on ? (
        <p className="mt-2 text-xs" data-testid="os-as-on">
          As on {data.as_on}
        </p>
      ) : null}
    </ReportFrame>
  );
}
