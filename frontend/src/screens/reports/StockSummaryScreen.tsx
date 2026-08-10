import { Fragment, useCallback, useEffect, useState } from "react";
import type { NavSnapshot } from "../../keystroke/navMachine";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { formatMoney } from "./money";
import { ApiError } from "../../api";
import type { StockSummaryResponse } from "../../api";
import { usePrintAction } from "../usePrint";
import { useExportAction } from "../../export/useExportAction";
import {
  exactCell,
  moneyCell,
  textCell,
  type ExportRow,
} from "../../export/sheet";

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
  // F2 sets the report period; a POSITION reads only its upper bound. This is
  // what was ASKED FOR — what the server actually bounded the replay at comes
  // back on the response as `as_on`, and it is that value every caption below
  // is written from. See stockSummaryScopeLine.
  const asOn = snapshot.config?.periodTo;

  // Ctrl+P / Alt+P. Registered here rather than inherited, because this screen
  // does NOT go through useReportView (it renders its own godown/batch grid), so
  // it would otherwise be the one report whose Print key silently did nothing.
  usePrintAction();

  const [data, setData] = useState<StockSummaryResponse | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(
    null,
  );

  // Alt+E. Registered for exactly the reason Ctrl+P above is: this screen does
  // not go through useReportView, so its Export key was DEAD — while the report
  // button bar over it advertised "Alt+E Export", which is worse than no button,
  // because an operator reaches for it by position from then on.
  //
  // ⚠️ THE REFUSAL AND THE LOADING STATE ARE EXPORT ANSWERS TOO. A 403 here is
  // the ACL working (migration 0026 denies this report to Data Entry — it prints
  // cost prices), and an operator who presses Alt+E on it must be told that,
  // not handed an empty file that looks like "no stock".
  useExportAction(
    useCallback(() => {
      if (error) {
        return error.status === 403
          ? "Your security level may not open the Stock Summary, so there is nothing to export."
          : `Stock Summary unavailable${error.status ? ` (${error.status})` : ""}, so there is nothing to export. ${error.message}`;
      }
      if (data === null) return "Stock Summary is still loading — nothing to export yet.";
      if (stockSummaryIsEmpty(data)) {
        return "No stock movements in this company yet, so the Stock Summary has nothing to export.";
      }
      // ⛔ THE SERVER'S `as_on`, NOT THE F2 BOX. The file names the date the
      // REPLAY STOPPED AT, which is the only date the figures in it are true
      // as at. Titling the file from the request would put an operator's
      // intention on a file full of the server's answer — and when the two
      // differ (no period set: the request says nothing, the report is
      // unbounded) that is exactly the lie this screen used to print.
      return stockSummarySheet(data, data.as_on);
    }, [data, error]),
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
      <p className="mt-1 text-xs" data-testid="stock-summary-scope">
        {stockSummaryScopeLine(asOn, data?.as_on)} · valued by replaying the
        movement ledger — no stock value is stored.
      </p>

      <div className="mt-3 bg-tally-panel p-3">
        {error ? (
          <StockSummaryError status={error.status} message={error.message} />
        ) : data === null ? (
          <p data-testid="stock-summary-loading">Loading…</p>
        ) : stockSummaryIsEmpty(data) ? (
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
 * ⛔ WHAT THE REPORT ACTUALLY COVERS — READ OFF THE SERVER'S ANSWER, NEVER OFF
 * THE F2 BOX. THIS SENTENCE USED TO BE FALSE, AND IT WAS MEASURED.
 *
 * With no period set this line read "As at today (everything posted)", and both
 * halves of that could not be true at once: `as_on` is a REPLAY BOUNDARY, and
 * an absent one means NO boundary — the replay runs over every voucher in the
 * table, INCLUDING ONES DATED AFTER TODAY. Driven on 2026-08-07 against the
 * demo books: the screen printed 72,837.15 under the words "As at today", while
 * the position as at that actual day was 96,168.18. A post-dated issue had
 * already been taken out of a warehouse that, today, still holds the goods —
 * and the caption asserted the opposite.
 *
 * The fix is not to bound the replay at today. That would MOVE THE BALANCE
 * SHEET (the Closing Stock asset comes off the same derivation) and it would
 * silently drop a legitimately post-dated voucher an operator entered on
 * purpose. The fix is that the report states the boundary it ACTUALLY applied:
 *
 *   - `resolved` is the server's echo — the date the replay stopped at.
 *   - "" from the server means UNBOUNDED, and the sentence says so, in the
 *     operator's words, with the key that changes it.
 *   - Before the response lands there is no server answer yet, so the REQUEST
 *     stands in. It is the same value the server will resolve, and the caption
 *     never has to blink between two claims.
 *
 * A `null`/`undefined` `resolved` is "no answer yet"; an EMPTY STRING is an
 * answer, and a different one. That distinction is why this takes two arguments
 * and why `??` (which falls through only on null/undefined) is the right
 * operator and `||` is not.
 */
export function stockSummaryScopeLine(
  requested: string | undefined,
  resolved: string | null | undefined,
): string {
  const bound = (resolved ?? requested ?? "").trim();
  if (bound !== "") return `As at ${bound}`;
  return (
    "NO DATE LIMIT — every voucher ever posted, including any dated AFTER " +
    "today. Press F2 and set a To date to value the stock as at a day."
  );
}

/**
 * ⛔ THE ONE EMPTINESS PREDICATE — Alt+E AND THE SCREEN READ IT, AND NOTHING
 * ELSE DECIDES THE QUESTION.
 *
 * The response carries the same positions TWICE: `rows` is the flat list and
 * `items` is the tree folded from it. Until now the screen showed its "no stock"
 * line on `rows.length === 0` while Alt+E refused on `items.length === 0`, and
 * the table itself is drawn from `items`. Two fields, three readers, and the
 * only thing holding them together was the server folding one from the other.
 * Any divergence — a fold that drops an item, an item with no positions, a
 * fixture written with one field filled (`reportSheets.test.ts`'s own stock
 * fixture has `rows: []` and a populated `items`) — makes Alt+E disagree with
 * what is on screen, which is the single thing Alt+E promises not to do: the
 * operator either reads "no stock movements" and gets a file full of stock, or
 * reads a table of stock and is told there is nothing to export.
 *
 * IT IS `&&` AND NOT `||`, DELIBERATELY. "Empty" has to mean "there is nothing
 * here at all", because the message it gates is a claim about the COMPANY ("no
 * stock movements in this company yet") and not about a field. If either field
 * carries something, that claim is false — so the table renders (with whatever
 * it has) and the export writes the same table.
 */
export function stockSummaryIsEmpty(data: StockSummaryResponse): boolean {
  return data.items.length === 0 && data.rows.length === 0;
}

/**
 * ⛔ WHAT A REFUSAL MAY SAY, AND WHY IT MAY NOT NAME A LEVEL.
 *
 * This branch used to open with the words "so the Data Entry level is denied it
 * explicitly" — a HARDCODED level name on a screen that is never told which
 * level the operator holds. `CompanyInfo` carries an id, a name and a state
 * code and no security level at all, and the 403 body is one field:
 * `{"error": "<the PDP's reason>"}`. So for the holder of ANY other level — a
 * custom "Warehouse" level, a level whose default_access is none, or a user who
 * is not a member of the company at all — the sentence named a level they do
 * not hold as the one refusing them, and then pointed them at that level's
 * rules. An Owner sent to edit Data Entry cannot fix a Warehouse denial, and
 * the operator has no way to know the screen guessed.
 *
 * SO THE MESSAGE IS DERIVED FROM WHAT THE SERVER SAID, and never from what the
 * client assumes:
 *
 *   - "disallowed by rule"  -> a rule ON THE OPERATOR'S OWN LEVEL denies it.
 *                              The Owner's move is to REMOVE that rule.
 *   - "default access: none"-> no rule matched at all and the level grants
 *                              nothing by default. The Owner's move is to ADD
 *                              an allow — there is no rule to delete, and
 *                              hunting for one is the wrong afternoon.
 *   - anything else (not a member, inactive account, unmapped route) is NOT a
 *     report-permission answer, so it must not be dressed as one.
 *
 * The server's own words are quoted verbatim in every case, for the reason
 * reportView's exportUnavailableMessage gives: rewriting a refusal is how a
 * refusal starts reading like an outage.
 *
 * WHY THE REPORT IS RESTRICTED AT ALL still gets said — that part IS a property
 * of the report and not of the level: it prints the company's own COST prices.
 */
export function stockSummaryDenialAdvice(message: string): string {
  const reason = message.trim().toLowerCase();
  if (reason.includes("disallowed by rule")) {
    return (
      "A rule on your own security level denies this report by name. An Owner " +
      "can lift it in Gateway > Users & Roles by removing that level's " +
      '"disallow display report:stock-summary" rule.'
    );
  }
  if (reason.includes("default access")) {
    return (
      "No rule on your security level grants this report, and the level allows " +
      "nothing by default — so there is no rule to delete. An Owner has to ADD " +
      '"allow display report:stock-summary" to your level in Gateway > Users & ' +
      "Roles."
    );
  }
  return (
    "The server refused this request before any report rule was reached, so " +
    "this is not a matter of which reports your level may open. An Owner can " +
    "check your membership and your level in Gateway > Users & Roles."
  );
}

/**
 * A refusal, stated. A 403 here is the ACL working, not a fault, so it says
 * what the server said and who can change it — an operator who is told "you may
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
          valuation rate is what the business paid — so it is one of the reports
          a security level can be refused. Quantities and values share one
          report, so there is no quantity-only view to fall back to yet.
        </p>
        <p className="mt-1 text-xs" data-testid="stock-summary-denied-advice">
          {stockSummaryDenialAdvice(message)}
        </p>
        <p className="mt-1 text-xs opacity-80">
          The server&rsquo;s own words: &ldquo;{message}&rdquo;
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

/**
 * ⛔ THE COLUMNS, AND WHY THERE ARE ELEVEN OF THEM RATHER THAN SEVEN.
 *
 * ONE LIST, USED BY THE TABLE AND BY Alt+E, so the screen and the file cannot
 * describe different reports — which is precisely what they did.
 *
 * TWO DEFECTS LIVED IN THE OLD SEVEN, and both were measured:
 *
 *  1. A QUANTITY AND A MONEY FIGURE SAT SIDE BY SIDE UNDER HEADINGS THAT DID
 *     NOT SAY WHICH WAS WHICH. The third column read "Inward" and held
 *     `inward_qty` (100.0000 pieces); the fourth read "Outward (at cost)" and
 *     held `outward_value` (2,000.57 rupees). Two adjacent columns, one a count
 *     and one an amount, neither headed with its unit — and in the CSV, where
 *     there is no styling to separate them, a reader summing across the row got
 *     a number with no meaning. The inward MONEY (`inward_value`, 7,860.00 on
 *     the demo books) was returned by the API and written down NOWHERE: the
 *     cost of everything that came in was invisible in the product.
 *  2. THE PRINTED REPORT WAS A DIFFERENT REPORT. Ctrl+P renders ten columns
 *     (Opening/Inward/Outward/Closing, each a Qty and a Value pair) from the
 *     SAME response this screen had reduced to seven. An operator comparing the
 *     paper against the screen found figures on the paper with no column on the
 *     screen to check them against.
 *
 * So every quantity now has its Value beside it, every heading says WHICH of
 * the two it is, and the set is the printed report's ten plus `Rate` — which is
 * on the screen and NOT on the paper (reported to the printing lane; it is
 * their file). Widening this list means widening `stockSummaryRow` below and
 * nothing else — `toCsv` REFUSES a row whose width disagrees with the headers,
 * so a half-done widening fails loudly at export rather than shifting every
 * figure after the gap under the wrong heading.
 */
const STOCK_SUMMARY_HEADERS = [
  "Item / Location",
  "Unit",
  "Opening Qty",
  "Opening Value",
  "Inward Qty",
  "Inward Value",
  "Outward Qty",
  "Outward Value (at cost)",
  "Closing Qty",
  "Rate",
  "Closing Value",
] as const;

/** The item tree: one block per stock item, with its per-location positions. */
function StockRows({ data }: { data: StockSummaryResponse }) {
  return (
    <table className="w-full text-sm" data-testid="stock-summary-table">
      <thead>
        <tr className="text-left text-xs text-tally-title">
          {STOCK_SUMMARY_HEADERS.map((h, i) => (
            <th key={h} className={i < 2 ? "pr-3" : "pr-3 text-right"}>
              {h}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {data.items.map((it) => (
          <Fragment key={it.stock_item_id}>
            <tr data-testid={`stock-item:${it.item}`}>
              <td className="pr-3 font-bold text-tally-title">{it.item}</td>
              <td className="pr-3">{it.unit}</td>
              <td className="pr-3 text-right">{it.opening_qty}</td>
              <td className="pr-3 text-right">{formatMoney(it.opening_value)}</td>
              <td className="pr-3 text-right">{it.inward_qty}</td>
              <td className="pr-3 text-right">{formatMoney(it.inward_value)}</td>
              <td className="pr-3 text-right">{it.outward_qty}</td>
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
                <td className="pr-3 text-right">{row.opening_qty}</td>
                <td className="pr-3 text-right">{formatMoney(row.opening_value)}</td>
                <td className="pr-3 text-right">{row.inward_qty}</td>
                <td className="pr-3 text-right">{formatMoney(row.inward_value)}</td>
                <td className="pr-3 text-right">{row.outward_qty}</td>
                <td className="pr-3 text-right">{formatMoney(row.outward_value)}</td>
                <td className="pr-3 text-right">{row.closing_qty}</td>
                <td className="pr-3 text-right">{formatMoney(row.closing_rate)}</td>
                <td className="pr-3 text-right">{formatMoney(row.closing_value)}</td>
              </tr>
            ))}
          </Fragment>
        ))}
        <tr className="border-t border-tally-field font-bold text-tally-title">
          {/* The footer states the ONE total the server states: the closing
              value of the whole inventory. The opening/inward/outward columns
              are left blank rather than summed here — the response carries no
              such totals, and a client-side fold would be this screen asserting
              a figure the server never derived. */}
          <td className="pr-3" colSpan={STOCK_SUMMARY_HEADERS.length - 1}>
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

/**
 * The Stock Summary as a spreadsheet: one row per ITEM total, each followed by
 * its godown/batch positions — the same shape the screen renders, because Alt+E
 * writes what is on screen.
 *
 * ⛔ EVERY FIGURE IS `moneyCell`/`exactCell` AND NONE IS `formatMoney`. Both
 * quantities and values are fixed-4 wire strings here; a spreadsheet would round
 * "12.5000" pieces to 12.5 and — worse — re-derive nothing, while `closing_rate`
 * is a 4-dp presentation figure that must never be multiplied back out (see the
 * type's own comment). Exported verbatim, it stays a figure to read rather than
 * one to compute with.
 */
export function stockSummarySheet(
  data: StockSummaryResponse,
  asOn: string | undefined,
): { title: string; headers: readonly string[]; rows: readonly ExportRow[] } {
  const rows: ExportRow[] = [];
  for (const it of data.items) {
    rows.push(
      stockSummaryRow(it.item, it, {
        // No rate on an item total: two godowns of the same item carry two
        // different average costs, and the average of the averages is not the
        // average. The screen shows an em dash; the file leaves it EMPTY rather
        // than exporting a punctuation mark into a numeric column.
        rate: null,
      }),
    );
    for (const pos of it.positions) {
      rows.push(
        stockSummaryRow(
          `${pos.godown || "(no location)"}${pos.batch ? ` \u00b7 ${pos.batch}` : ""}`,
          pos,
          { rate: pos.closing_rate },
        ),
      );
    }
  }
  // The footer states the ONE total the server states (see the table's footer).
  rows.push([
    textCell("Stock-in-Hand"),
    ...STOCK_SUMMARY_HEADERS.slice(1, -1).map(() => textCell("")),
    moneyCell(data.total_value),
  ]);
  return {
    // \u26d4 THE TITLE IS THE FILE NAME, AND IT MUST STATE THE REPORT'S SCOPE. A
    // file called "Stock-Summary.csv" with no date on it is indistinguishable
    // from one taken at a year end, and this report's most dangerous scope is
    // the one with no date in it at all \u2014 see stockSummaryScopeLine.
    title: `Stock Summary${asOn ? ` as at ${asOn}` : " (no date limit)"}`,
    headers: STOCK_SUMMARY_HEADERS,
    rows,
  };
}

/**
 * One export row, in the ONE column order the headers declare.
 *
 * Item totals and positions differ in exactly one cell (an item total has no
 * meaningful rate), so they are built here rather than twice \u2014 a second copy is
 * how a column gets added to one of them and not the other, and `toCsv` would
 * then refuse the whole export on a width mismatch the author never saw.
 *
 * Quantities are `exactCell` and money is `moneyCell` for the reason the
 * function above gives; every one of them is a fixed-4 wire string, and which
 * of the two a column is comes from its position in this list and nowhere else.
 */
function stockSummaryRow(
  label: string,
  f: {
    unit: string;
    opening_qty: string;
    opening_value: string;
    inward_qty: string;
    inward_value: string;
    outward_qty: string;
    outward_value: string;
    closing_qty: string;
    closing_value: string;
  },
  opts: { rate: string | null },
): ExportRow {
  return [
    textCell(label),
    textCell(f.unit),
    exactCell(f.opening_qty),
    moneyCell(f.opening_value),
    exactCell(f.inward_qty),
    moneyCell(f.inward_value),
    exactCell(f.outward_qty),
    moneyCell(f.outward_value),
    exactCell(f.closing_qty),
    opts.rate === null ? textCell("") : moneyCell(opts.rate),
    moneyCell(f.closing_value),
  ];
}
