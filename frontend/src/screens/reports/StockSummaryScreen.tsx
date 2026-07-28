import { Fragment, useEffect, useState } from "react";
import type { NavSnapshot } from "../../keystroke/navMachine";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { formatMoney } from "./money";
import { ApiError } from "../../api";
import type { StockSummaryResponse } from "../../api";

/**
 * STOCK SUMMARY — the inventory position as at a date.
 *
 * Wired to GET /stock-summary. Nothing on this screen is stored anywhere:
 * closing quantity is a signed sum over the movement ledger and closing VALUE
 * is derived by replaying that ledger through the server's valuer, because
 * valuation is order-dependent (a back-dated purchase re-prices every issue
 * that already happened after it). The `as on` date is a REPLAY BOUNDARY, not a
 * filter on a balances table — which is also why there is no From date: a
 * position has no lower bound, and applying one would drop the stock the
 * company already owned while still rendering a tidy-looking table.
 *
 * THREE THINGS THIS SCREEN REFUSES TO DO
 *
 *  1. It NEVER invents a row. An empty response renders as a stated "no stock"
 *     line and nothing else. Sample rows on an accounting screen are worse than
 *     a missing screen, and a placeholder that ships looking wired is how a
 *     report nobody built ends up in a board pack.
 *  2. It NEVER shows a blank on a 403. Migration 0026 explicitly DISALLOWS this
 *     report to the seeded Data Entry level — the closing valuation rate is
 *     what the business PAID for its stock, and the coarse `allow display
 *     report` those operators carry would otherwise have handed every tenant's
 *     cost book to every operator by inheritance. A denial is a normal answer
 *     here, so it is rendered as the reason it is, with who can lift it.
 *  3. It NEVER re-derives a value from a rate. `closing_rate` is presentation
 *     only at 4 dp; the running average carries far more precision and
 *     multiplying the rounded rate back out does not reproduce the value.
 */
export function StockSummaryScreen({ snapshot }: { snapshot: NavSnapshot }) {
  const client = useClient();
  const companyID = useCompanyID();
  // F2 sets the report period; a POSITION reads only its upper bound.
  const asOn = snapshot.config?.periodTo;

  const [data, setData] = useState<StockSummaryResponse | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(
    null,
  );

  useEffect(() => {
    let live = true;
    setData(null);
    setError(null);
    client
      .getStockSummary(companyID, asOn)
      .then((res) => {
        if (live) setData(res);
      })
      .catch((e: unknown) => {
        if (!live) return;
        setError(
          e instanceof ApiError
            ? { status: e.status, message: e.message }
            : { status: 0, message: e instanceof Error ? e.message : String(e) },
        );
      });
    return () => {
      live = false;
    };
  }, [client, companyID, asOn]);

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">Stock Summary</h1>
      <p className="mt-1 text-xs">
        {asOn ? `As at ${asOn}` : "As at today (everything posted)"} · valued by
        replaying the movement ledger — no stock value is stored.
      </p>

      <div className="mt-3 bg-tally-panel p-3">
        {error ? (
          <StockSummaryError status={error.status} message={error.message} />
        ) : data === null ? (
          <p data-testid="stock-summary-loading">Loading…</p>
        ) : data.rows.length === 0 ? (
          <p data-testid="stock-summary-empty">
            No stock movements in this company yet, so there is nothing to
            value. Key an Item Invoice (Ctrl+H on F8 Sales / F9 Purchase) and
            this report fills in.
          </p>
        ) : (
          <StockRows data={data} />
        )}
      </div>
    </section>
  );
}

/**
 * A refusal, stated. A 403 here is the ACL working, not a fault, so it says
 * which rule refused and who can change it — an operator who is told "you may
 * not see cost prices" stops filing bug reports about an empty screen.
 */
function StockSummaryError({
  status,
  message,
}: {
  status: number;
  message: string;
}) {
  if (status === 403) {
    return (
      <div data-testid="stock-summary-denied" className="text-sm">
        <p className="text-tally-title">
          Your security level may not open the Stock Summary.
        </p>
        <p className="mt-1 text-xs">
          This report prints the company&rsquo;s own COST prices — the closing
          valuation rate is what the business paid — so the Data Entry level is
          denied it explicitly. An Owner can lift that in Gateway &gt; Users
          &amp; Roles by removing the &ldquo;disallow display
          report:stock-summary&rdquo; rule. Quantities and values share one
          report, so there is no quantity-only view to fall back to yet.
        </p>
      </div>
    );
  }
  return (
    <p data-testid="stock-summary-error" className="text-sm text-red-400">
      Stock Summary unavailable{status ? ` (${status})` : ""}. {message}
    </p>
  );
}

/** The item tree: one block per stock item, with its per-location positions. */
function StockRows({ data }: { data: StockSummaryResponse }) {
  return (
    <table className="w-full max-w-5xl text-sm" data-testid="stock-summary-table">
      <thead>
        <tr className="text-left text-xs text-tally-title">
          <th className="pr-3">Item / Location</th>
          <th className="pr-3">Unit</th>
          <th className="pr-3 text-right">Inward</th>
          <th className="pr-3 text-right">Outward (at cost)</th>
          <th className="pr-3 text-right">Closing Qty</th>
          <th className="pr-3 text-right">Rate</th>
          <th className="pr-3 text-right">Closing Value</th>
        </tr>
      </thead>
      <tbody>
        {data.items.map((it) => (
          <Fragment key={it.stock_item_id}>
            <tr data-testid={`stock-item:${it.item}`}>
              <td className="pr-3 font-bold text-tally-title">{it.item}</td>
              <td className="pr-3">{it.unit}</td>
              <td className="pr-3 text-right">{it.inward_qty}</td>
              <td className="pr-3 text-right">{formatMoney(it.outward_value)}</td>
              <td className="pr-3 text-right">{it.closing_qty}</td>
              {/* No rate on an item total: two godowns of the same item carry
                  two different average costs, and the average of the averages
                  is not the average. */}
              <td className="pr-3 text-right">—</td>
              <td className="pr-3 text-right">{formatMoney(it.closing_value)}</td>
            </tr>
            {it.positions.map((row) => (
              <tr
                key={`${it.stock_item_id}:${row.godown_id}:${row.batch_id}`}
                data-testid={`stock-row:${row.item}:${row.godown || "-"}`}
                className="text-xs"
              >
                <td className="pr-3 pl-4">
                  {row.godown || "(no location)"}
                  {row.batch ? ` · ${row.batch}` : ""}
                </td>
                <td className="pr-3">{row.unit}</td>
                <td className="pr-3 text-right">{row.inward_qty}</td>
                <td className="pr-3 text-right">{formatMoney(row.outward_value)}</td>
                <td className="pr-3 text-right">{row.closing_qty}</td>
                <td className="pr-3 text-right">{formatMoney(row.closing_rate)}</td>
                <td className="pr-3 text-right">{formatMoney(row.closing_value)}</td>
              </tr>
            ))}
          </Fragment>
        ))}
        <tr className="border-t border-tally-field font-bold text-tally-title">
          <td className="pr-3" colSpan={6}>
            Stock-in-Hand
          </td>
          <td className="pr-3 text-right" data-testid="stock-total-value">
            {formatMoney(data.total_value)}
          </td>
        </tr>
      </tbody>
    </table>
  );
}
