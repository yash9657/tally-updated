import { Fragment, useCallback, useEffect, useState } from "react";
import type { NavSnapshot } from "../../keystroke/navMachine";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import { SelectionList } from "../SelectionList";
import { formatMoney } from "./money";
import { ApiError } from "../../api";
import type { StockSummaryResponse, ValuationMethodOption } from "../../api";
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
  const [error, setError] = useState<{
    status: number;
    message: string;
  } | null>(null);
  // ---- Alt+V: THE COSTING METHOD (T9).
  //
  // ⛔ WHY THE SWITCH LIVES ON THIS SCREEN AND NOT ON A MASTER FORM. The method
  // can be chosen when an item is created (Alt+C > Stock Item), but every item
  // created before T9 — which is every item in every existing company — could
  // otherwise never be switched from a keyboard at all, and "the route exists
  // and nothing calls it" is the shape this repo has shipped three times. This
  // is also the honest place for it: the operator is looking at the very figure
  // that is about to change, and the report redraws under the panel when it does.
  //
  // ⚠️ `reload` is what makes that redraw real. Without it the panel would report
  // a success over a table still showing the OLD basis, which is the same class
  // of lie as answering a write with the request body.
  const [switching, setSwitching] = useState(false);
  const [reload, setReload] = useState(0);
  const [methods, setMethods] = useState<ValuationMethodOption[] | null>(null);
  const [pickedItem, setPickedItem] = useState<string>("");
  const [switchStatus, setSwitchStatus] = useState<string>("");

  useRegisterScreenAction(
    "setValuationMethod",
    useCallback((): string => {
      if (error) {
        return error.status === 403
          ? "Your security level may not open the Stock Summary, so there is no item here to re-value."
          : `Stock Summary unavailable${error.status ? ` (${error.status})` : ""}, so there is nothing to re-value. ${error.message}`;
      }
      if (data === null) return "Stock Summary is still loading.";
      if (data.items.length === 0) {
        return "No stock has moved in this company yet, so there is no item to re-value. A costing method only changes figures that exist.";
      }
      setSwitchStatus("");
      setPickedItem(
        data.items.length === 1 ? data.items[0]!.stock_item_id : "",
      );
      setSwitching(true);
      return "Choose the item whose costing method you want to change.";
    }, [data, error]),
  );

  // The vocabulary is SERVED, so it is fetched — never hardcoded here. A picker
  // built from a client-side list can offer a method the server refuses, and the
  // operator would meet that refusal after choosing it.
  useEffect(() => {
    if (!switching || methods !== null) return;
    let live = true;
    client
      .inventoryVocabulary(companyID)
      .then((v) => {
        if (live) setMethods(v.valuation_methods);
      })
      .catch((e: unknown) => {
        if (live) {
          setSwitchStatus(
            `The costing methods could not be read from the server, so nothing is offered rather than a guessed list. ${e instanceof Error ? e.message : String(e)}`,
          );
        }
      });
    return () => {
      live = false;
    };
  }, [client, companyID, switching, methods]);

  const applyMethod = useCallback(
    (method: string) => {
      const item = data?.items.find((i) => i.stock_item_id === pickedItem);
      if (!item) {
        setSwitchStatus("Choose an item first.");
        return;
      }
      setSwitchStatus(`Re-valuing ${item.item} on the ${method} basis…`);
      client
        .setStockItemValuationMethod(companyID, item.stock_item_id, {
          valuation_method: method,
        })
        .then((res) => {
          // The STORED method, from the answer — not the one that was asked for.
          setSwitchStatus(
            `${res.name} is now valued at ${res.valuation_method}. ${res.restates}`,
          );
          setSwitching(false);
          setReload((n) => n + 1);
        })
        .catch((e: unknown) => {
          const status = e instanceof ApiError ? e.status : 0;
          const message = e instanceof Error ? e.message : String(e);
          setSwitchStatus(
            status === 409
              ? `${item.item} is already valued at ${method}, so nothing was changed.`
              : status === 403
                ? `Refused: ${message} Changing a costing method re-values periods that may be CLOSED, so it is a write like any other — the back-dated cutoff and your security level both apply.`
                : `Not changed (${status || "no status"}): ${message}`,
          );
        });
    },
    [client, companyID, data, pickedItem],
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
      if (data === null)
        return "Stock Summary is still loading — nothing to export yet.";
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
            : {
                status: 0,
                message: e instanceof Error ? e.message : String(e),
              },
        );
      });
    return () => {
      live = false;
    };
  }, [client, companyID, asOn, reload]);

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">Stock Summary</h1>
      <p className="mt-1 text-xs" data-testid="stock-summary-scope">
        {stockSummaryScopeLine(asOn, data?.as_on)} · valued by replaying the
        movement ledger — no stock value is stored.
      </p>

      {/* ⛔ THE SCREEN ADVERTISES Alt+V ITSELF. The button bar and the hint strip
          are per SCREEN KIND ("report"), so neither can name a key that is
          meaningful on this report and inert on the other twelve — and a key
          nothing mentions is a feature nobody finds. This line is the surface
          that can be specific. */}
      <p className="mt-1 text-xs" data-testid="stock-summary-basis-hint">
        Alt+V changes an item&rsquo;s costing method (Avg. Cost / FIFO / LIFO).
        The Basis column says which one every figure on that line was derived on
        — and changing it re-values the item&rsquo;s WHOLE history, including
        periods already reported.
      </p>

      {switching ? (
        <ValuationSwitchPanel
          items={
            data?.items.map((i) => ({
              id: i.stock_item_id,
              name: i.item,
              method: i.valuation_method,
            })) ?? []
          }
          methods={methods}
          picked={pickedItem}
          onPickItem={setPickedItem}
          onPickMethod={applyMethod}
          onCancel={() => {
            setSwitching(false);
          }}
          status={switchStatus}
        />
      ) : switchStatus ? (
        <p
          className="mt-2 text-xs text-tally-active"
          role="status"
          data-testid="stock-valuation-status"
        >
          {switchStatus}
        </p>
      ) : null}

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
          <>
            <StockGroupSubtotals data={data} />
            <StockRows data={data} />
          </>
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
  // ⛔ THE BASIS IS A COLUMN, NOT A CAPTION (T9). Costing method is a property
  // of the ITEM, so one company's Stock Summary can mix WEIGHTED_AVG, FIFO and
  // LIFO — and two item totals on the same page then mean different things.
  // Every figure here is a REPLAY through one of those valuers and none of them
  // is stored, so a value printed without its basis cannot be reproduced or
  // compared with the line above it.
  //
  // ⚠️ IT IS THE THIRD COLUMN AND NOT THE LAST, deliberately: `Closing Value`
  // must stay last, because both this table's footer and the export's footer put
  // the one total the server states in the FINAL cell (colSpan / slice(1, -1)).
  // Appending a column after it would silently file the company's closing stock
  // under "Basis".
  "Basis",
  "Opening Qty",
  "Opening Value",
  "Inward Qty",
  "Inward Value",
  "Outward Qty",
  "Outward Value (at cost)",
  "Closing Qty",
  // T10 — the closing quantity RESTATED in the unit's base unit ("48.0000" for
  // four boxes of twelve). ⛔ ITS OWN COLUMN AND NOT PARENTHESES INSIDE
  // "Closing Qty": the screen can afford "4.0000 (= 48.0000 Nos)" in one cell
  // and a spreadsheet cannot — a numeric column with prose in it is a column
  // nobody can sum, and this file's whole purpose is to be summed. It is BEFORE
  // Closing Value because that one must stay LAST (both footers put the one
  // total the server states in the final cell).
  "Closing Qty (base unit)",
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
            <th key={h} className={i < 3 ? "pr-3" : "pr-3 text-right"}>
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
              <td className="pr-3" data-testid={`stock-basis:${it.item}`}>
                {it.valuation_method}
              </td>
              <td className="pr-3 text-right">{it.opening_qty}</td>
              <td className="pr-3 text-right">
                {formatMoney(it.opening_value)}
              </td>
              <td className="pr-3 text-right">{it.inward_qty}</td>
              <td className="pr-3 text-right">
                {formatMoney(it.inward_value)}
              </td>
              <td className="pr-3 text-right">{it.outward_qty}</td>
              <td className="pr-3 text-right">
                {formatMoney(it.outward_value)}
              </td>
              <td className="pr-3 text-right">{it.closing_qty}</td>
              {/* ⛔ THE RESTATEMENT IS ITS OWN COLUMN, BESIDE THE STORED FIGURE
                  AND NEVER INSTEAD OF IT (T10). The quantity that was keyed is
                  the one in the item's own unit; the base-unit figure is DERIVED
                  by the server. A screen that showed only "48 Nos" for four
                  boxes would disagree with the invoice about what was entered.
                  A simple unit renders an em dash: there is no base to restate
                  into, and a zero would be a claim about the goods. */}
              <td
                className="pr-3 text-right"
                data-testid={`stock-base-qty:${it.item}`}
              >
                {it.base_unit && it.base_qty
                  ? `${it.base_qty} ${it.base_unit}`
                  : "—"}
              </td>
              {/* No rate on an item total: two godowns of the same item carry
                  two different average costs, and the average of the averages
                  is not the average. */}
              <td className="pr-3 text-right">—</td>
              <td className="pr-3 text-right">
                {formatMoney(it.closing_value)}
              </td>
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
                {/* The basis belongs to the ITEM and is stated on its line; a
                    position cannot carry a different one, so repeating it on
                    every location would be noise that implies it could. */}
                <td className="pr-3" />
                <td className="pr-3 text-right">{row.opening_qty}</td>
                <td className="pr-3 text-right">
                  {formatMoney(row.opening_value)}
                </td>
                <td className="pr-3 text-right">{row.inward_qty}</td>
                <td className="pr-3 text-right">
                  {formatMoney(row.inward_value)}
                </td>
                <td className="pr-3 text-right">{row.outward_qty}</td>
                <td className="pr-3 text-right">
                  {formatMoney(row.outward_value)}
                </td>
                <td className="pr-3 text-right">{row.closing_qty}</td>
                {/* Empty: the restatement belongs to the ITEM (one unit, one
                    conversion), and repeating it per location would imply a
                    location could have a conversion of its own. */}
                <td className="pr-3" />
                <td className="pr-3 text-right">
                  {formatMoney(row.closing_rate)}
                </td>
                <td className="pr-3 text-right">
                  {formatMoney(row.closing_value)}
                </td>
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
          { ...pos, valuation_method: "", base_unit: "", base_qty: "" },
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
    valuation_method?: string;
    base_unit?: string;
    base_qty?: string;
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
    // The basis, on item lines only — the same rule the table follows.
    textCell(f.valuation_method ?? ""),
    exactCell(f.opening_qty),
    moneyCell(f.opening_value),
    exactCell(f.inward_qty),
    moneyCell(f.inward_value),
    exactCell(f.outward_qty),
    moneyCell(f.outward_value),
    exactCell(f.closing_qty),
    // Empty for a simple unit AND for every position line (the restatement is a
    // property of the ITEM, stated on its own line), rather than "0.0000":
    // a zero in a quantity column is a claim about the goods.
    f.base_qty && f.base_unit ? exactCell(f.base_qty) : textCell(""),
    opts.rate === null ? textCell("") : moneyCell(opts.rate),
    moneyCell(f.closing_value),
  ];
}

/**
 * Alt+V — CHANGE AN ITEM'S COSTING METHOD.
 *
 * Two picks and no Ctrl+A: the item, then the method, and choosing the method
 * IS the commit. That is deliberate — there is no draft here to accept, and an
 * intermediate "accept" step would leave the operator holding a form whose only
 * content is a choice they have already made.
 *
 * ⛔ IT STATES THE CONSEQUENCE BEFORE THE CHOICE, NOT AFTER IT. Nothing about
 * this switch is stored, so the item's WHOLE history is re-valued — a period
 * that has been printed, read and filed will come back a different number. An
 * operator who learns that from the confirmation has already done it.
 */
function ValuationSwitchPanel({
  items,
  methods,
  picked,
  onPickItem,
  onPickMethod,
  onCancel,
  status,
}: {
  items: readonly { id: string; name: string; method: string }[];
  methods: readonly ValuationMethodOption[] | null;
  picked: string;
  onPickItem: (id: string) => void;
  onPickMethod: (method: string) => void;
  onCancel: () => void;
  status: string;
}) {
  const item = items.find((i) => i.id === picked);
  return (
    <div
      className="mt-3 border border-tally-field/50 bg-tally-panel p-3 text-sm"
      data-testid="stock-valuation-panel"
    >
      <p className="text-tally-title">Costing method</p>
      <p className="mt-1 text-xs">
        Stock value is never stored — it is derived by replaying the movement
        ledger through a valuer. So changing the method changes no data and
        every figure: this item&rsquo;s closing stock, its cost of goods sold,
        and the Balance Sheet and Profit &amp; Loss of every period it has
        traded in. Switching back reproduces the earlier figures exactly.
      </p>

      {item ? (
        <p className="mt-2 text-xs" data-testid="stock-valuation-item">
          {item.name} — currently {item.method}
        </p>
      ) : (
        <div className="mt-2">
          <SelectionList
            label="Item"
            options={items.map((i) => i.name)}
            autoFocus
            onSelect={(name) => {
              const hit = items.find((i) => i.name === name);
              if (hit) onPickItem(hit.id);
            }}
          />
        </div>
      )}

      {item ? (
        methods === null ? (
          <p className="mt-2 text-xs" data-testid="stock-valuation-loading">
            Reading the costing methods this server offers…
          </p>
        ) : (
          <div className="mt-2">
            <SelectionList
              label="New method"
              options={methods.map((m) => m.method)}
              autoFocus
              value={item.method}
              onSelect={onPickMethod}
            />
            <ul className="mt-2 space-y-1 text-xs">
              {methods.map((m) => (
                <li
                  key={m.method}
                  data-testid={`stock-valuation-option:${m.method}`}
                >
                  <span className="text-tally-title">{m.method}</span> (
                  {m.label}) — {m.basis}
                </li>
              ))}
            </ul>
          </div>
        )
      ) : null}

      {status ? (
        <p
          className="mt-2 text-xs text-tally-active"
          role="status"
          data-testid="stock-valuation-status"
        >
          {status}
        </p>
      ) : null}
      <p className="mt-2 text-xs">
        <button
          type="button"
          className="underline"
          onClick={onCancel}
          data-testid="stock-valuation-cancel"
        >
          Esc to cancel
        </button>
      </p>
    </div>
  );
}

/**
 * THE STOCK GROUP SUBTOTALS (T10) — one line per group, in tree order, above the
 * item table.
 *
 * ⛔⛔ IT PRINTS "NO TOTAL" AND SAYS WHY. The group figures DO NOT SUM to the
 * company's closing stock: an item counts in every ancestor of its group, which
 * is exactly what makes a branch total worth reading ("Provisions" includes
 * "Provisions > Grains") and exactly what makes adding the column up wrong. A
 * table of numbers with no total invites the reader to add them, so the reason is
 * stated where they would otherwise do it.
 *
 * ⚠️ A COMPANY WITH NO GROUPS RENDERS NOTHING AT ALL — not an empty box, not a
 * heading. Every item that predates T10 is ungrouped, so the one-bucket case
 * (just "(ungrouped)") is a table that says nothing the item list does not, and
 * a heading over it would imply a feature is missing rather than unused.
 */
function StockGroupSubtotals({ data }: { data: StockSummaryResponse }) {
  const grouped = data.groups.filter((g) => g.group !== "");
  if (grouped.length === 0) return null;
  return (
    <table className="mb-4 w-full text-sm" data-testid="stock-group-subtotals">
      <thead>
        <tr className="text-left text-xs text-tally-title">
          <th className="pr-3">Stock group</th>
          <th className="pr-3 text-right">Items</th>
          <th className="pr-3 text-right">Closing Value</th>
        </tr>
      </thead>
      <tbody>
        {data.groups.map((g) => (
          <tr
            key={g.group || "(ungrouped)"}
            data-testid={`stock-group:${g.group}`}
          >
            <td className="pr-3">
              {/* The indent IS the hierarchy, and `depth` comes from the server
                  (an ltree path) rather than from anything this screen derives. */}
              <span style={{ paddingLeft: `${String(g.depth)}rem` }}>
                {g.group === "" ? "(ungrouped)" : g.group}
              </span>
            </td>
            <td className="pr-3 text-right">{g.item_count}</td>
            <td className="pr-3 text-right">{formatMoney(g.closing_value)}</td>
          </tr>
        ))}
      </tbody>
      <tfoot>
        <tr>
          <td colSpan={3} className="pt-1 text-xs">
            No total: an item counts in EVERY group above it, so these lines
            overlap and adding them up double-counts every nested branch. The
            company&rsquo;s closing stock is the Stock-in-Hand line below.
          </td>
        </tr>
      </tfoot>
    </table>
  );
}
