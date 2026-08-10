import { useCallback, useEffect, useState } from "react";

import type { NavSnapshot } from "../../keystroke/navMachine";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { formatMoney } from "./money";
import { ApiError } from "../../api";
import type { OrderBookResponse, OrderDirection } from "../../api";
import { usePrintAction } from "../usePrint";
import { useExportAction } from "../../export/useExportAction";
import {
  exactCell,
  moneyCell,
  textCell,
  type ExportRow,
  type ExportSheet,
} from "../../export/sheet";

/**
 * THE TWO ORDER BOOKS (T3) — what is still OUTSTANDING as at a date.
 *
 * ⛔⛔ TWO REPORT NAMES, NEVER ONE WITH A DIRECTION TOGGLE, AND THIS IS AN
 * AUTHORISATION FACT RATHER THAN A REPORTING ONE.
 *
 * The server serves `report:sales-order-book` and `report:purchase-order-book` as
 * two separate names because the PDP decides on the report NAME and A QUERY
 * PARAMETER IS INVISIBLE TO IT. One report with `?direction=` would make "the
 * salesperson may see our sales pipeline but not what we have committed to pay
 * suppliers" an unwritable rule, and a toggle on one screen would be reaching a
 * resource the operator may be denied — producing a 403 nobody can attribute to
 * anything they did. Migration 0073 writes both refusals, and writes `disallow
 * display` AND `disallow print` for each: a report that cannot be displayed but
 * CAN be printed is a leak with a paper trail.
 *
 * ⚠️ SO WHY IS THERE ONE COMPONENT BELOW? Because the rule is about AUTHORITY and
 * not about pixels. `OrderBook` is instantiated twice, and each instance's
 * direction is fixed AT REGISTRATION (defaultScreens.tsx maps "report:Sales Order
 * Book" and "report:Purchase Order Book" to two different adapters) — it is never
 * read from operator input, there is no control on the screen that changes it, and
 * each instance calls exactly one client method against exactly one route. What the
 * rule forbids is a SINGLE REACHABLE SURFACE that serves both directions; what this
 * is, is two reachable surfaces that happen to share their rendering. If a `?dir=`
 * control ever appears on this screen, that is the defect the whole T3 design
 * exists to prevent.
 *
 * FOUR THINGS THESE SCREENS REFUSE TO DO
 *
 *  1. THEY NEVER TREAT A MISSING DUE DATE AS OVERDUE. An omitted due date means "no
 *     date was agreed", which is NOT a late commitment however old the order is.
 *     `is_overdue` is computed server-side and is true only when a date EXISTS and
 *     has passed; this screen renders that flag and never re-derives it from the
 *     date string, because a client that compared "" to today would mark every
 *     undated line overdue and invent a debt collection problem.
 *
 *  2. THEY NEVER ADD A QUANTITY COLUMN TO A VALUE COLUMN, or sum quantities across
 *     items. Two lines of different items have quantities in different units, so
 *     their sum is meaningless. Only `outstanding_value` is totalled, and the
 *     server totals it — `total_outstanding_value` comes off the response rather
 *     than being re-added here, so the figure on screen is the figure the server
 *     computed.
 *
 *  3. THEY NEVER SHOW A BLANK ON A 403. A denial is a NORMAL answer on both of
 *     these (migration 0073), so it is rendered as the decision it is, naming the
 *     direction that was refused — because being allowed one book and refused the
 *     other is the configuration the two names exist to make possible, and an
 *     operator who sees the sales book work needs to know the purchase refusal is
 *     deliberate rather than broken.
 *
 *  4. THEY NEVER RE-DERIVE `outstanding_qty` FROM qty − fulfilled_qty. Both figures
 *     arrive derived, summed server-side over CURRENT, non-deleted voucher versions
 *     — which is the whole reason no `fulfilled_qty` column exists: a stored figure
 *     goes stale undetectably the first time a fulfilling voucher is ALTERED,
 *     because an alter writes a NEW version and re-states its links without ever
 *     visiting the order. Subtracting them here would be arithmetic on two numbers
 *     that are already the answer.
 */

/**
 * How the heading names the date this book was run for.
 *
 * Three cases, and the middle one is the one that shipped wrong:
 *
 *  - `served` is a date        → say it. This is what the server actually answered for.
 *  - `served` is ""            → the server was given NO as_on and reports the position
 *                                from every order ever placed. That is a real answer
 *                                and it needs a sentence, not a blank: "As at " with
 *                                nothing after it reads as a field that failed to load.
 *  - nothing served yet        → fall back to what F2 asked for, clearly marked as the
 *                                request rather than the answer.
 *
 * Exported so a test can pin all three without rendering the screen.
 */
export function periodLabel(
  served: string | undefined,
  requested: string | undefined,
): string {
  if (served !== undefined) {
    return served === ""
      ? "Every order ever placed, less every delivery ever recorded — F2 sets an as-at date"
      : `As at ${served}`;
  }
  return requested ? `Loading, as at ${requested}` : "Loading";
}

/** What a direction is CALLED, in the two places an operator reads it. */
interface BookCopy {
  /** The report name — must match REPORT_NAMES and the registry key exactly. */
  title: string;
  /** The party column's heading. A customer promises us; we promise a supplier. */
  partyHeading: string;
  /**
   * What the FULFILLED quantity column is called on this direction.
   *
   * ⛔ IT IS NOT "Delivered" ON BOTH, AND HARD-CODING IT WAS THE DEFECT. On the
   * purchase book the figure counts goods a SUPPLIER owes and has handed over —
   * the company received them, it delivered nothing — and the purchase register
   * (OrderRegisterScreen's PURCHASE_COPY.fulfilledNoun) has always called the
   * identical figure "Received". Two names for one number across two screens an
   * operator moves between is how a reconciliation question gets asked about a
   * column that was never different.
   */
  fulfilledHeading: string;
  /** The sentence under the heading, saying what these commitments ARE. */
  blurb: string;
  /** The empty state. "No orders" and "nothing outstanding" are different facts. */
  empty: string;
  /** The 403 panel's opening sentence, naming the direction refused. */
  deniedLead: string;
  /** Where an order of this direction is created. */
  registerHint: string;
}

const SALES_COPY: BookCopy = {
  title: "Sales Order Book",
  partyHeading: "Customer",
  // We deliver to a customer, so "Delivered" is right here — and it is stated
  // rather than defaulted, so the purchase book cannot inherit it by accident.
  fulfilledHeading: "Delivered",
  blurb:
    "What customers have committed to buy and we have not yet delivered. An order is " +
    "a PROMISE, not an entry: nothing here has moved stock or posted to the ledger, " +
    "and none of it appears in the Balance Sheet.",
  empty:
    "Nothing is outstanding as at this date. That is not the same as having no " +
    "orders — every order this company has placed may simply have been delivered " +
    "in full. Ctrl+F8 opens the sales order register, which lists them all.",
  deniedLead: "Your security level may not open the SALES order book.",
  registerHint: "Ctrl+F8",
};

const PURCHASE_COPY: BookCopy = {
  title: "Purchase Order Book",
  partyHeading: "Supplier",
  // A supplier delivers to US. The company RECEIVES, which is exactly the word
  // the purchase register uses for the same figure.
  fulfilledHeading: "Received",
  blurb:
    "What we have committed to buy from suppliers and have not yet received. An " +
    "order is a PROMISE, not an entry: nothing here has moved stock or posted to " +
    "the ledger, and none of it is a liability in the Balance Sheet — a purchase " +
    "commitment becomes a creditor only when the goods or the bill arrive.",
  empty:
    "Nothing is outstanding as at this date. That is not the same as having no " +
    "orders — every order this company has placed may simply have been received in " +
    "full. Ctrl+F9 opens the purchase order register, which lists them all.",
  deniedLead: "Your security level may not open the PURCHASE order book.",
  registerHint: "Ctrl+F9",
};

function OrderBook({
  snapshot,
  direction,
  copy,
}: {
  snapshot: NavSnapshot;
  /** ⛔ FIXED AT REGISTRATION. Never operator input — see the header. */
  direction: OrderDirection;
  copy: BookCopy;
}) {
  const client = useClient();
  const companyID = useCompanyID();
  // F2 sets the report period; a POSITION reads only its upper bound. There is
  // deliberately no `periodFrom` here, and periodClassification.test.ts asserts
  // that against this source: bounding an outstanding balance BELOW would drop the
  // orders placed before the window while leaving their deliveries inside it, so a
  // long-open order would read as fully delivered.
  const asOn = snapshot.config?.periodTo;

  usePrintAction();

  const [data, setData] = useState<OrderBookResponse | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(
    null,
  );

  useEffect(() => {
    let live = true;
    setData(null);
    setError(null);
    // ⛔ THE METHOD IS CHOSEN BY THE FIXED DIRECTION, NOT BY A PARAMETER ON ONE
    // CALL. These are two routes and two authorities; `salesOrderBook` can only
    // ever reach the sales book.
    const fetch =
      direction === "SALES"
        ? client.salesOrderBook(companyID, asOn)
        : client.purchaseOrderBook(companyID, asOn);
    fetch
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
  }, [client, companyID, asOn, direction]);

  const testid = direction === "SALES" ? "sales-order-book" : "purchase-order-book";

  // Alt+E. Registered here rather than inherited: an order book is a `report`
  // frame — so the report button bar over it ADVERTISES "Alt+E Export" — but it
  // renders its own table instead of going through useReportView, so the key was
  // dead on both books.
  useExportAction(
    useCallback(() => {
      if (error) {
        return error.status === 403
          ? `Your security level may not open the ${copy.title}, so there is nothing to export.`
          : `${copy.title} unavailable${error.status ? ` (${error.status})` : ""}, so there is nothing to export. ${error.message}`;
      }
      if (data === null) return `${copy.title} is still loading — nothing to export yet.`;
      if (data.rows.length === 0) {
        return `Nothing is outstanding in the ${copy.title}, so there is nothing to export.`;
      }
      return orderBookSheet(data, copy, asOn);
    }, [asOn, copy, data, error]),
  );

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">{copy.title}</h1>
      <p className="mt-1 max-w-4xl text-xs">
        {/* ⛔ THE DATE COMES FROM THE RESPONSE once it has arrived, never from the
            frame. A heading that said "As at (the date we asked for)" while the
            server answered for a different day would be a report labelled with a
            date it was not run for.

            ⚠️ AND `as_on` COMES BACK EMPTY WHEN NONE WAS GIVEN — the server does NOT
            substitute its own today, it reports the position from every order ever
            placed and says so in `notes`. Interpolating that blank produced the
            heading "As at ·", which reads as a value that failed to load. FOUND BY
            DRIVING THE APP, NOT BY READING IT, and not by any of the 678 tests — the
            same way the F2 period defect and Alt+O's dead binding were found. So the
            empty case gets its own sentence rather than an empty slot. */}
        {periodLabel(data?.as_on, asOn)} · {copy.blurb}
      </p>

      <div className="mt-3 bg-tally-panel p-3">
        {error ? (
          <OrderBookError
            status={error.status}
            message={error.message}
            copy={copy}
            testid={testid}
          />
        ) : data === null ? (
          <p data-testid={`${testid}-loading`}>Loading…</p>
        ) : data.rows.length === 0 ? (
          <p data-testid={`${testid}-empty`} className="max-w-3xl text-sm">
            {copy.empty}
          </p>
        ) : (
          <OrderBookRows data={data} copy={copy} testid={testid} />
        )}

        {/* THE SERVER'S OWN NOTES, verbatim and always rendered when present —
            including on an empty book, where they may be the only thing that
            explains the emptiness. */}
        {data && data.notes.length > 0 ? (
          <ul
            data-testid={`${testid}-notes`}
            className="mt-3 max-w-3xl list-none text-xs"
          >
            {data.notes.map((n) => (
              <li key={n} className="mt-1">
                {n}
              </li>
            ))}
          </ul>
        ) : null}
      </div>
    </section>
  );
}

/**
 * A refusal, stated, NAMING THE DIRECTION.
 *
 * A 403 on one book and a working other book is not a bug — it is the exact
 * configuration two report names exist to make expressible. So the panel says which
 * one was refused and why the split exists, rather than "access denied".
 */
function OrderBookError({
  status,
  message,
  copy,
  testid,
}: {
  status: number;
  message: string;
  copy: BookCopy;
  testid: string;
}) {
  if (status === 403) {
    return (
      <div data-testid={`${testid}-denied`} className="max-w-3xl text-sm">
        <p className="text-tally-title">{copy.deniedLead}</p>
        <p className="mt-1 text-xs">
          That is a decision, not a fault, and it is why there are TWO order books
          rather than one report with a direction switch: the two are separate
          report names precisely so that a level can be allowed one and refused the
          other. A salesperson may need the sales pipeline and have no business
          reading what the company has committed to pay its suppliers — and with a
          single report that rule could not be written at all, because the PDP
          cannot see a query parameter. An Owner can lift this in Gateway &gt; Users
          &amp; Roles by removing the{" "}
          <span className="text-tally-title">
            disallow display report:{testid}
          </span>{" "}
          rule — note there is a matching{" "}
          <span className="text-tally-title">disallow print</span> rule, because a
          report that cannot be displayed but can be printed is a leak with a paper
          trail.
        </p>
      </div>
    );
  }
  return (
    <p data-testid={`${testid}-error`} className="text-sm text-red-400">
      {copy.title} unavailable{status ? ` (${status})` : ""}. {message}
    </p>
  );
}

/** One row per outstanding order LINE — fulfilment is per item, never per order. */
function OrderBookRows({
  data,
  copy,
  testid,
}: {
  data: OrderBookResponse;
  copy: BookCopy;
  testid: string;
}) {
  return (
    <table className="w-full max-w-6xl text-sm" data-testid={`${testid}-table`}>
      <thead>
        <tr className="text-left text-xs text-tally-title">
          <th className="pr-3">Order</th>
          <th className="pr-3">Date</th>
          <th className="pr-3">{copy.partyHeading}</th>
          <th className="pr-3">Item</th>
          <th className="pr-3 text-right">Ordered</th>
          {/* ⛔ FROM THE COPY, NEVER A LITERAL — see BookCopy.fulfilledHeading. */}
          <th className="pr-3 text-right">{copy.fulfilledHeading}</th>
          <th className="pr-3 text-right">Outstanding</th>
          <th className="pr-3 text-right">Rate</th>
          <th className="pr-3 text-right">Outstanding Value</th>
          <th className="pr-3">Due</th>
        </tr>
      </thead>
      <tbody>
        {data.rows.map((row) => (
          <tr
            key={row.order_line_id}
            data-testid={`${testid}-row:${row.order_no}:${row.stock_item}`}
          >
            <td className="pr-3">{row.order_no}</td>
            <td className="pr-3">{row.order_date}</td>
            <td className="pr-3">{row.party}</td>
            <td className="pr-3">{row.stock_item}</td>
            {/* Quantities render as they arrive (fixed-4 strings) and are never
                formatted as money: a quantity has a unit, not a currency, and
                formatMoney would put a thousands grouping on a count of goods. */}
            <td className="pr-3 text-right">{row.qty}</td>
            <td className="pr-3 text-right">{row.fulfilled_qty}</td>
            <td className="pr-3 text-right text-tally-title">
              {row.outstanding_qty}
            </td>
            <td className="pr-3 text-right">{formatMoney(row.rate)}</td>
            <td className="pr-3 text-right">
              {formatMoney(row.outstanding_value)}
            </td>
            {/* ⛔ THE SERVER'S FLAG, NEVER A COMPARISON DONE HERE. An empty due
                date is "no date agreed" and is NOT overdue, however old the order:
                a client that compared "" against today would mark every undated
                line late and invent a problem. */}
            <td
              className={`pr-3 ${row.is_overdue ? "text-tally-bar" : ""}`}
              data-testid={
                row.is_overdue
                  ? `${testid}-overdue:${row.order_no}:${row.stock_item}`
                  : undefined
              }
            >
              {row.due_date === "" ? (
                <span title="No delivery date was agreed on this line, so it cannot be overdue.">
                  none agreed
                </span>
              ) : (
                `${row.due_date}${row.is_overdue ? " — OVERDUE" : ""}`
              )}
            </td>
          </tr>
        ))}
        <tr className="border-t border-tally-field font-bold text-tally-title">
          {/* ⛔ NO QUANTITY TOTAL. Two lines of different items are in different
              units, so their sum is not a quantity. Only value totals, and the
              SERVER totals it — this cell prints the response's own figure rather
              than re-adding the column, so what is on screen is what was computed. */}
          <td className="pr-3" colSpan={8}>
            Total outstanding{" "}
            <span className="text-xs font-normal text-tally-field">
              (at each order&apos;s own agreed rate — the invoice that fulfils a
              line may lawfully bill a different one)
            </span>
          </td>
          <td
            className="pr-3 text-right"
            data-testid={`${testid}-total`}
          >
            {formatMoney(data.total_outstanding_value)}
          </td>
          <td />
        </tr>
      </tbody>
    </table>
  );
}

/** The SALES order book — `report:sales-order-book` on the server. */
export function SalesOrderBookScreen({ snapshot }: { snapshot: NavSnapshot }) {
  return (
    <OrderBook snapshot={snapshot} direction="SALES" copy={SALES_COPY} />
  );
}

/** The PURCHASE order book — `report:purchase-order-book` on the server. */
export function PurchaseOrderBookScreen({ snapshot }: { snapshot: NavSnapshot }) {
  return (
    <OrderBook snapshot={snapshot} direction="PURCHASE" copy={PURCHASE_COPY} />
  );
}

/**
 * An order book as a spreadsheet.
 *
 * ⛔ QUANTITIES ARE `exactCell`, NOT `moneyCell`-formatted AND NOT PLAIN TEXT.
 * They arrive as fixed-4 strings and the screen prints them raw for the reason
 * stated on the table itself — a quantity has a unit, not a currency — but a
 * spreadsheet would still eat "20.0000" and hand back 20. The unit is not
 * exported as a separate column because the response does not carry one per
 * line; the item name is the only place it is implied, and inventing a column
 * would be inventing data.
 *
 * ⛔ AND THERE IS NO QUANTITY TOTAL ROW, exactly as there is none on screen: two
 * lines of different items are in different units, so their sum is not a
 * quantity. Only the VALUE total is written, and it is the SERVER's figure
 * rather than a re-addition of the column.
 */
export function orderBookSheet(
  data: OrderBookResponse,
  // ⚠️ ONLY THE THREE HEADINGS, not the whole BookCopy. A function that asks for
  // more than it uses cannot be called from a test without inventing a blurb, a
  // 403 sentence and an empty state — and a header assertion that has to invent
  // four unrelated strings is one nobody writes.
  copy: Pick<BookCopy, "title" | "partyHeading" | "fulfilledHeading">,
  asOn: string | undefined,
): ExportSheet {
  const rows: ExportRow[] = data.rows.map((row): ExportRow => [
    exactCell(row.order_no),
    exactCell(row.order_date),
    textCell(row.party),
    textCell(row.stock_item),
    exactCell(row.qty),
    exactCell(row.fulfilled_qty),
    exactCell(row.outstanding_qty),
    moneyCell(row.rate),
    moneyCell(row.outstanding_value),
    // The DUE column carries the server's own overdue verdict, never a
    // comparison made here: an empty due date is "no date agreed" and is not
    // overdue however old the line is.
    exactCell(row.due_date === "" ? "none agreed" : row.due_date),
    textCell(row.is_overdue ? "OVERDUE" : ""),
  ]);
  rows.push([
    textCell("Total outstanding"),
    textCell(""),
    textCell(""),
    textCell(""),
    textCell(""),
    textCell(""),
    textCell(""),
    textCell(""),
    moneyCell(data.total_outstanding_value),
    textCell(""),
    textCell(""),
  ]);
  return {
    title: `${copy.title}${asOn ? ` as at ${asOn}` : ""}`,
    headers: [
      "Order",
      "Date",
      copy.partyHeading,
      "Item",
      "Ordered",
      // ⛔ THE EXPORT'S HEADER MUST MATCH THE SCREEN'S. A spreadsheet is read
      // away from the screen that produced it, so a column named "Delivered" in
      // a file of goods a supplier owes us is the same defect with a longer
      // half-life: nobody can check it against the report it came from.
      copy.fulfilledHeading,
      "Outstanding",
      "Rate",
      "Outstanding Value",
      "Due",
      "Overdue",
    ],
    rows,
  };
}
