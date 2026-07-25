import { useEffect, useMemo, useState } from "react";
import type { NavSnapshot } from "../../keystroke/navMachine";
import {
  GROUP_ROW_PREFIX,
  LEDGER_ROW_PREFIX,
} from "../../keystroke/navMachine";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useLoadedVoucher } from "../../app/LoadedVoucher";
import { ColumnHeader, DrillRows, type DrillRow } from "./DrillRows";
import { formatMoney } from "./money";
import { useReportView } from "./reportView";
import type {
  DayBookResponse,
  StatementNode,
  TrialBalanceResponse,
} from "../../api";

/**
 * The report DRILL level — Tally's "no report is a dead end" (RESEARCH sec 8):
 * every statement line opens what it is made of, all the way down to the
 * voucher, without a mouse.
 *
 *   group  ->  its ledgers (with their debit/credit)      [Enter drills again]
 *   ledger ->  the vouchers that posted to it             [Enter opens it]
 *
 * Which level this frame is showing is carried by `detailOf`, prefixed by the
 * report row that produced it ("group:" / "ledger:"). Everything rendered here
 * is REAL data: the ledger⇄group map comes from the Trial Balance and the
 * vouchers from the Day Book (whose entries carry their postings), so there is
 * no placeholder row anywhere in the drill path.
 */
export function ReportDetailScreen({
  snapshot,
  onSelect,
}: {
  snapshot: NavSnapshot;
  onSelect: (item: string) => void;
}) {
  const client = useClient();
  const companyID = useCompanyID();
  const { setLoaded } = useLoadedVoucher();
  const [tb, setTb] = useState<TrialBalanceResponse | null>(null);
  const [dayBook, setDayBook] = useState<DayBookResponse | null>(null);
  /** The statement tree this drill came from (Balance Sheet / P&L), if any. */
  const [tree, setTree] = useState<StatementNode[] | null>(null);

  const target = parseDetailTarget(snapshot.detailOf);
  const reportName = snapshot.reportName;

  useEffect(() => {
    let cancelled = false;
    void Promise.all([
      client.getTrialBalance(companyID),
      client.getDayBook(companyID, {}),
      // A group drill expands the SAME tree the parent report drew, which is
      // where the real hierarchy lives (Current Assets > Sundry Debtors >
      // ledger). Resolving groups off the flat Trial Balance would only ever
      // find leaf-level parents and show an empty screen for the outer groups.
      reportName === "Balance Sheet"
        ? client.getBalanceSheet(companyID)
        : reportName === "Profit & Loss A/c"
          ? client.getProfitLoss(companyID)
          : Promise.resolve(null),
    ]).then(([t, d, statement]) => {
      if (cancelled) return;
      setTb(t);
      setDayBook(d);
      if (statement && "assets" in statement) {
        setTree([...statement.liabilities.nodes, ...statement.assets.nodes]);
      } else if (statement && "income" in statement) {
        setTree([...statement.expenses.nodes, ...statement.income.nodes]);
      } else {
        setTree(null);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [client, companyID, reportName]);

  const columns =
    target.kind === "group"
      ? "grid-cols-[1fr_8rem_8rem]"
      : "grid-cols-[7rem_8rem_1fr_8rem_8rem]";

  const { rows, headers, empty } = useMemo(() => {
    if (target.kind === "group") {
      // Children of this group: from the statement tree when the drill came
      // from one (it carries the real nesting), else from the Trial Balance's
      // ledger→group map (the group-wise Trial Balance drill).
      const node = tree ? findNode(tree, target.name) : undefined;
      const children: { name: string; isGroup: boolean; amounts: string[] }[] =
        node?.children?.length
          ? node.children.map((c) => ({
              name: c.name,
              isGroup: c.kind === "GROUP",
              amounts: [formatMoney(c.amount), ""],
            }))
          : (tb?.rows ?? [])
              .filter((r) => r.group === target.name)
              .map((r) => ({
                name: r.ledger,
                isGroup: false,
                amounts: [formatMoney(r.debit), formatMoney(r.credit)],
              }));
      return {
        headers: ["Particulars", "Debit", "Credit"],
        empty: `Nothing under ${target.name}.`,
        rows: children.map<DrillRow>((c) => ({
          // Publish each child with its KIND, so Enter drills one more level
          // for a group and to the vouchers for a ledger.
          rowKey: `${c.isGroup ? GROUP_ROW_PREFIX : LEDGER_ROW_PREFIX}${c.name}`,
          values: [c.name, c.amounts[0] ?? "", c.amounts[1] ?? ""],
          cells: (
            <>
              <span>{c.name}</span>
              <span className="text-right">{c.amounts[0]}</span>
              <span className="text-right">{c.amounts[1]}</span>
            </>
          ),
        })),
      };
    }
    // Ledger level: every voucher that posted to this ledger, in Day Book order.
    const entries = (dayBook?.entries ?? []).filter((e) =>
      e.postings.some((p) => p.ledger === target.name),
    );
    return {
      headers: ["Date", "No.", "Particulars", "Debit", "Credit"],
      empty: `No vouchers have posted to ${target.name} in this period.`,
      rows: entries.map<DrillRow>((e) => {
        const mine = e.postings.filter((p) => p.ledger === target.name);
        const debit = mine.find((p) => p.dr_cr.toUpperCase().startsWith("D"));
        const credit = mine.find((p) => p.dr_cr.toUpperCase().startsWith("C"));
        return {
          rowKey: e.voucher_id,
          values: [
            e.date,
            e.voucher_no,
            e.party || e.narration,
            formatMoney(debit?.amount),
            formatMoney(credit?.amount),
          ],
          cells: (
            <>
              <span>{e.date}</span>
              <span>{e.voucher_no}</span>
              <span>{e.party || e.narration}</span>
              <span className="text-right">{formatMoney(debit?.amount)}</span>
              <span className="text-right">{formatMoney(credit?.amount)}</span>
            </>
          ),
        };
      }),
    };
  }, [dayBook, target.kind, target.name, tb, tree]);

  const visible = useReportView(rows, snapshot, {
    title: `${snapshot.reportName ?? "Report"} - ${target.name}`,
    headers,
  });

  /**
   * Highlighting a VOUCHER row stashes it in the loaded-voucher slot, so the
   * Enter the shell then handles (which pushes an alteration frame) finds the
   * real voucher already loaded — the same contract the Day Book uses.
   */
  function handleSelect(rowKey: string) {
    if (target.kind === "ledger") {
      const entry = (dayBook?.entries ?? []).find((e) => e.voucher_id === rowKey);
      if (entry) setLoaded({ entry });
    }
    onSelect(rowKey);
  }

  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">
        {snapshot.reportName ?? "Report"} — {target.name}
      </h1>
      <p className="mt-1 text-xs">
        {target.kind === "group"
          ? "Ledgers under this group. Enter opens a ledger's vouchers; Esc goes back."
          : "Vouchers that posted to this ledger. Enter opens one for alteration; Esc goes back."}
      </p>
      <div className="mt-3 bg-tally-panel p-3">
        <ColumnHeader
          columnsClassName={columns}
          labels={headers.map((text, i) => ({
            text,
            numeric: i >= headers.length - 2,
          }))}
        />
        {visible.length === 0 ? (
          <p className="px-2 py-1 text-xs" role="status">
            {tb === null && dayBook === null ? "Loading…" : empty}
          </p>
        ) : (
          <DrillRows
            rows={visible}
            onSelect={handleSelect}
            ariaLabel={`${target.name} detail rows`}
            columnsClassName={columns}
          />
        )}
      </div>
    </section>
  );
}

/** Depth-first search for a node by name in a statement tree. */
function findNode(
  nodes: readonly StatementNode[],
  name: string,
): StatementNode | undefined {
  for (const n of nodes) {
    if (n.name === name) return n;
    const hit = n.children ? findNode(n.children, name) : undefined;
    if (hit) return hit;
  }
  return undefined;
}

/** Split a "group:Name" / "ledger:Name" detail key into its kind and name. */
export function parseDetailTarget(detailOf: string | undefined): {
  kind: "group" | "ledger";
  name: string;
} {
  const raw = detailOf ?? "";
  if (raw.startsWith(GROUP_ROW_PREFIX)) {
    return { kind: "group", name: raw.slice(GROUP_ROW_PREFIX.length) };
  }
  if (raw.startsWith(LEDGER_ROW_PREFIX)) {
    return { kind: "ledger", name: raw.slice(LEDGER_ROW_PREFIX.length) };
  }
  // An unprefixed row key (older corpora / a plain name) is treated as a ledger:
  // the deeper of the two levels, which is what a statement leaf row is.
  return { kind: "ledger", name: raw };
}
