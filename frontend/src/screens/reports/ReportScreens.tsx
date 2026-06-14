import { useEffect, useState } from "react";
import type { NavSnapshot } from "../../keystroke/navMachine";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useLoadedVoucher } from "../../app/LoadedVoucher";
import { DrillRows, ColumnHeader, type DrillRow } from "./DrillRows";
import { formatMoney } from "./money";
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
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">{title}</h1>
      <div className="mt-3 bg-tally-panel p-3">{children}</div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Day Book (voucher-bearing; drill loads the real voucher).
// ---------------------------------------------------------------------------

export function DayBookScreen({ onSelect }: ReportProps) {
  const client = useClient();
  const companyID = useCompanyID();
  const { setLoaded } = useLoadedVoucher();
  const [data, setData] = useState<DayBookResponse | null>(null);

  useEffect(() => {
    let cancelled = false;
    client.getDayBook(companyID).then((res) => {
      if (!cancelled) setData(res);
    });
    return () => {
      cancelled = true;
    };
  }, [client, companyID]);

  const entries = data?.entries ?? [];
  const rows: DrillRow[] = entries.map((e) => ({
    rowKey: e.voucher_id,
    cells: (
      <>
        <span>{e.date}</span>
        <span>{e.voucher_no}</span>
        <span>{e.party || e.narration}</span>
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

  return (
    <ReportFrame title="Day Book">
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
      {rows.length === 0 ? (
        <p className="px-2 py-1 text-xs" role="status">
          No vouchers in this period.
        </p>
      ) : (
        <DrillRows
          rows={rows}
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

export function TrialBalanceScreen({ onSelect }: ReportProps) {
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

  const rows: DrillRow[] = (data?.rows ?? []).map((r) => ({
    rowKey: r.ledger,
    cells: (
      <>
        <span>{r.ledger}</span>
        <span className="text-right">{formatMoney(r.debit)}</span>
        <span className="text-right">{formatMoney(r.credit)}</span>
      </>
    ),
  }));
  rows.push({
    rowKey: "__total__",
    drillable: false,
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
  return (
    <ReportFrame title="Trial Balance">
      <ColumnHeader
        columnsClassName={columns}
        labels={[
          { text: "Ledger" },
          { text: "Debit", numeric: true },
          { text: "Credit", numeric: true },
        ]}
      />
      <DrillRows
        rows={rows}
        onSelect={onSelect}
        ariaLabel="Trial Balance rows"
        columnsClassName={columns}
      />
    </ReportFrame>
  );
}

// ---------------------------------------------------------------------------
// Balance Sheet + Profit & Loss (statement trees).
// ---------------------------------------------------------------------------

function flattenNodes(nodes: readonly StatementNode[], depth = 0): DrillRow[] {
  const out: DrillRow[] = [];
  for (const n of nodes) {
    out.push({
      rowKey: n.id ?? n.name,
      depth,
      drillable: n.kind !== "COMPUTED",
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

export function BalanceSheetScreen({ onSelect }: ReportProps) {
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
  const rows: DrillRow[] = [];
  if (data) {
    rows.push({
      rowKey: "__liabilities__",
      drillable: false,
      cells: (
        <>
          <span>{data.liabilities.title}</span>
          <span className="text-right" data-testid="bs-liabilities-total">
            {formatMoney(data.liabilities.total)}
          </span>
        </>
      ),
    });
    rows.push(...flattenNodes(data.liabilities.nodes, 1));
    rows.push({
      rowKey: "__assets__",
      drillable: false,
      cells: (
        <>
          <span>{data.assets.title}</span>
          <span className="text-right" data-testid="bs-assets-total">
            {formatMoney(data.assets.total)}
          </span>
        </>
      ),
    });
    rows.push(...flattenNodes(data.assets.nodes, 1));
  }

  return (
    <ReportFrame title="Balance Sheet">
      <ColumnHeader
        columnsClassName={columns}
        labels={[{ text: "Particulars" }, { text: "Amount", numeric: true }]}
      />
      <DrillRows
        rows={rows}
        onSelect={onSelect}
        ariaLabel="Balance Sheet rows"
        columnsClassName={columns}
      />
    </ReportFrame>
  );
}

export function ProfitLossScreen({ onSelect }: ReportProps) {
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
    rows.push({
      rowKey: "__expenses__",
      drillable: false,
      cells: (
        <>
          <span>{data.expenses.title}</span>
          <span className="text-right" data-testid="pl-expenses-total">
            {formatMoney(data.expenses.total)}
          </span>
        </>
      ),
    });
    rows.push(...flattenNodes(data.expenses.nodes, 1));
    rows.push({
      rowKey: "__income__",
      drillable: false,
      cells: (
        <>
          <span>{data.income.title}</span>
          <span className="text-right" data-testid="pl-income-total">
            {formatMoney(data.income.total)}
          </span>
        </>
      ),
    });
    rows.push(...flattenNodes(data.income.nodes, 1));
    rows.push({
      rowKey: "__net__",
      drillable: false,
      cells: (
        <>
          <span>Net Profit</span>
          <span className="text-right" data-testid="pl-net-profit">
            {formatMoney(data.net_profit)}
          </span>
        </>
      ),
    });
  }

  return (
    <ReportFrame title="Profit & Loss A/c">
      <ColumnHeader
        columnsClassName={columns}
        labels={[{ text: "Particulars" }, { text: "Amount", numeric: true }]}
      />
      <DrillRows
        rows={rows}
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

export function OutstandingsScreen({ onSelect }: ReportProps) {
  const client = useClient();
  const companyID = useCompanyID();
  const [data, setData] = useState<OutstandingsResponse | null>(null);

  useEffect(() => {
    let cancelled = false;
    client.getOutstandings(companyID).then((res) => {
      if (!cancelled) setData(res);
    });
    return () => {
      cancelled = true;
    };
  }, [client, companyID]);

  const columns = "grid-cols-[1fr_8rem_8rem]";
  const rows: DrillRow[] = (data?.parties ?? []).map((p) => ({
    rowKey: p.ledger,
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

  return (
    <ReportFrame title="Outstandings">
      <ColumnHeader
        columnsClassName={columns}
        labels={[
          { text: "Party" },
          { text: "Group" },
          { text: "Pending", numeric: true },
        ]}
      />
      <DrillRows
        rows={rows}
        onSelect={onSelect}
        ariaLabel="Outstandings rows"
        columnsClassName={columns}
      />
    </ReportFrame>
  );
}
