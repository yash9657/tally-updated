import { useCallback, useEffect, useState } from "react";

import type { NavSnapshot } from "../../keystroke/navMachine";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { formatMoney } from "./money";
import { ApiError } from "../../api";
import type {
  AnomaliesResponse,
  CashFlowBasisCount,
  CashFlowResponse,
} from "../../api";
import { usePrintAction } from "../usePrint";
import { useExportAction } from "../../export/useExportAction";
import {
  moneyCell,
  textCell,
  type ExportRow,
  type ExportSheet,
} from "../../export/sheet";

/**
 * THE FORECAST PLANE (wave-2 WS5a) — the cash-flow projection and the anomaly
 * list, as `report` frames.
 *
 * ⛔⛔ THESE TWO SCREENS ARE THE SECOND HALF OF A DEFECT WHOSE FIRST HALF SHIPPED
 * A WHOLE WAVE EARLIER, AND THE GAP IS THE LESSON. cored has served
 * `GET /cash-flow` and `GET /anomalies` since wave 2. Both were routed, both were
 * ACL-mapped, both were in the PDP vocabulary, and `handler_forecast.go` carries a
 * long comment explaining that the cash-flow response was given `estimate`,
 * `method` and `basis` so that "the screen has something true to render the moment
 * it exists". The screen did not exist. There was no Gateway entry, no Go To entry,
 * no client method and no component — while F11 Company Features advertised
 * "Cash-flow projection and anomaly detection" as `on`.
 *
 * So the product had a disclosure with no renderer (register defect D22, and D23's
 * client half), and a capability list that overstated what an operator could reach
 * (D52). ⛔ A DISCLOSURE WITH NO RENDERER IS NOT A DISCLOSURE — it is a comment in
 * a Go file. That is why these two screens land in ONE commit with the report
 * names, the client methods and the corrected feature rows: any one of those
 * without the others leaves the product claiming something that is not true.
 *
 * WHY `report` FRAMES AND NOT NEW ScreenKinds. Both are pure readers with a date
 * dimension and no form, which is exactly what a `report` frame is; registering
 * them by NAME buys the report keymap, F2, the report button bar, and a place in
 * Go To and Display More Reports for free. It also puts them where the PDP already
 * expects them: cored serves them as `report:cash-flow` and `report:anomalies`.
 *
 * ⚠️ AND THEY ARE TWO NAMES, NOT ONE "Forecast" SCREEN WITH TWO TABS. The reason
 * is the order books' reason (see OrderBookScreens.tsx): the PDP decides on the
 * report NAME, so a tab is invisible to it. A cash-flow projection is a management
 * report about money; the anomaly list is a CONTROL SURFACE naming operators'
 * irregular entries. "The accountant may project cash but may not audit who
 * backdated what" is an ordinary rule, and one screen with two tabs would make it
 * unwritable.
 *
 * ⛔ WHAT NEITHER SCREEN DOES: re-derive anything. Every figure on both pages is
 * rendered as it arrived. The cash-flow screen does not total the three basis
 * sources into one "assumed" figure (that would hide the distinction being
 * disclosed), does not re-add the buckets to check the closing balance, and does
 * not compute a bucket from a date. The anomaly screen does not re-rank or re-group
 * findings by severity, because the ranking is the server's opinion and a client
 * that sorted them would be inventing one.
 */

// ===========================================================================
// CASH FLOW
// ===========================================================================

/**
 * How the heading names the date the projection was run for.
 *
 * ⚠️ UNLIKE THE ORDER BOOKS, `as_on` IS NEVER EMPTY HERE. cored's `parseAsOn`
 * substitutes today when none is sent and returns the date it used, so there is no
 * "every bill ever" case to word. What there IS is the window before the response
 * arrives, and that gets the operator's requested date clearly marked as the
 * request rather than the answer — the same three-case shape the order books
 * needed, with the middle case collapsed.
 *
 * Exported so a test can pin it without rendering the screen.
 */
export function cashFlowPeriodLabel(
  served: string | undefined,
  requested: string | undefined,
): string {
  if (served !== undefined && served !== "") return `Projected from ${served}`;
  return requested ? `Loading, from ${requested}` : "Loading";
}

/**
 * A basis line: how many bills took their settlement date from this source, and
 * how much money that was per side.
 *
 * ⛔ THE BILL COUNT IS RENDERED EVEN WHEN IT IS ZERO, and that is deliberate. A
 * zero on `from_default_credit_days` is the most reassuring number on the page —
 * it says none of the projection rests on a constant nobody agreed to — and a line
 * that vanished when it was zero would leave a reader unable to tell "none" from
 * "not disclosed".
 */
function BasisLine({
  label,
  observed,
  count,
  testid,
}: {
  label: string;
  observed: boolean;
  count: CashFlowBasisCount;
  testid: string;
}) {
  return (
    <tr data-testid={testid}>
      <td className="pr-3">
        {label}{" "}
        <span className="text-tally-dim">
          {observed ? "(observed)" : "(assumed)"}
        </span>
      </td>
      <td className="pr-3 text-right" data-testid={`${testid}-bills`}>
        {count.bills}
      </td>
      <td className="pr-3 text-right">{formatMoney(count.inflow)}</td>
      <td className="pr-3 text-right">{formatMoney(count.outflow)}</td>
    </tr>
  );
}

/**
 * THE DISCLOSURE. This block is not decoration and it is not collapsible.
 *
 * ⛔ EVERY FIGURE IN THE TABLE ABOVE IT IS A FIXED-4 STRING THAT READS EXACTLY
 * LIKE A POSTED LEDGER BALANCE, AND THE ENGINE INVENTED THE DATE OF EVERY ONE OF
 * THEM. `opening_cash` is a real position and the bill AMOUNTS are real pending
 * balances — what is projected is WHEN each lands. Without this block a reader
 * cannot tell which part of the answer was observed and which part is a 30-day
 * convention compiled into the build, and a closing balance three months out is
 * not a fact. That was register defect D23; the server closed its half by putting
 * `estimate`, `method` and `basis` on the wire, and this is the other half.
 */
function CashFlowBasisPanel({ data }: { data: CashFlowResponse }) {
  const b = data.basis;
  const a = b.assumed_settlement_date;
  return (
    <section className="mt-4 max-w-4xl bg-tally-panel p-3" data-testid="cash-flow-basis">
      <h2 className="text-sm font-bold text-tally-title">
        {/* The word "ESTIMATE" is on the page in its own right, driven by the
            server's boolean rather than by prose this screen composes. */}
        {data.estimate ? "THIS IS AN ESTIMATE" : "Basis"}
      </h2>
      <p className="mt-1 text-xs" data-testid="cash-flow-method">
        Method: {data.method}
      </p>
      <p className="mt-1 max-w-3xl text-xs" data-testid="cash-flow-projected">
        {b.what_is_projected}
      </p>

      <table className="mt-3 w-full text-xs">
        <thead>
          <tr className="text-tally-dim">
            <th className="pr-3 text-left font-normal">
              Settlement date taken from
            </th>
            <th className="pr-3 text-right font-normal">Bills</th>
            <th className="pr-3 text-right font-normal">Inflow</th>
            <th className="pr-3 text-right font-normal">Outflow</th>
          </tr>
        </thead>
        <tbody>
          <BasisLine
            label="the bill's own due date"
            observed
            count={a.from_due_date}
            testid="cash-flow-basis-due"
          />
          <BasisLine
            label="the party's credit period"
            observed={false}
            count={a.from_party_credit_days}
            testid="cash-flow-basis-party"
          />
          <BasisLine
            /* ⛔ THIS LINE NAMES THE NUMBER OF DAYS. "assumed" alone does not tell
               an operator that the assumption is a constant in the software rather
               than anything their business agreed to, and this is the line most
               worth doubting. */
            label={`this build's ${a.default_credit_days}-day convention — no party or bill said so`}
            observed={false}
            count={a.from_default_credit_days}
            testid="cash-flow-basis-default"
          />
        </tbody>
      </table>

      {/* Overdue folding, stated with its money. The top-level flag says WHETHER
          it happened; this count says how much — they are two different fields
          with the same name on the wire and neither is redundant. */}
      <p className="mt-3 text-xs" data-testid="cash-flow-overdue">
        {data.overdue_folded_into_first_bucket
          ? `Already overdue and folded into the first bucket: ` +
            `${b.overdue_folded_into_first_bucket.bills} bill(s), ` +
            `${formatMoney(b.overdue_folded_into_first_bucket.inflow)} in, ` +
            `${formatMoney(b.overdue_folded_into_first_bucket.outflow)} out. ` +
            `They are shown as arriving in the first bucket because they are owed now — ` +
            `not because a date says they will arrive then.`
          : "Nothing is overdue, so nothing was folded into the first bucket."}
      </p>

      {/* ⛔ VERBATIM, AND NEVER SUMMARISED. These are the server's own sentences
          about what the projection cannot see. A screen that paraphrased them
          would drift from the engine silently, and a test pinning the paraphrase
          would go green while the real disclosure changed. */}
      <div className="mt-3">
        <p className="text-xs text-tally-dim">Not modelled:</p>
        <ul className="list-none text-xs" data-testid="cash-flow-not-modelled">
          {b.not_modelled.map((n) => (
            <li key={n} className="mt-1">
              · {n}
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}

/** The bucket table. Dates and money exactly as they arrived. */
function CashFlowBuckets({ data }: { data: CashFlowResponse }) {
  return (
    <table className="w-full text-sm" data-testid="cash-flow-buckets">
      <thead>
        <tr className="text-tally-dim">
          <th className="pr-3 text-left font-normal">From</th>
          <th className="pr-3 text-left font-normal">To</th>
          <th className="pr-3 text-right font-normal">Inflow</th>
          <th className="pr-3 text-right font-normal">Outflow</th>
          <th className="pr-3 text-right font-normal">Nett</th>
          <th className="pr-3 text-right font-normal">Closing cash</th>
        </tr>
      </thead>
      <tbody>
        {data.buckets.map((k) => (
          <tr key={`${k.from}-${k.to}`} data-testid={`cash-flow-bucket-${k.from}`}>
            <td className="pr-3">{k.from}</td>
            <td className="pr-3">{k.to}</td>
            <td className="pr-3 text-right">{formatMoney(k.inflow)}</td>
            <td className="pr-3 text-right">{formatMoney(k.outflow)}</td>
            <td className="pr-3 text-right">{formatMoney(k.nett)}</td>
            <td
              className="pr-3 text-right"
              data-testid={`cash-flow-closing-${k.from}`}
            >
              {formatMoney(k.closing_cash)}
            </td>
          </tr>
        ))}
        {/* BEYOND THE HORIZON IS A ROW WITH NO DATES, because it is an OBJECT on
            the wire and not a bucket. Rendering it as a bucket with invented dates
            would be inventing data; leaving it out would silently drop money the
            server took the trouble to report. */}
        <tr className="border-t border-tally-dim" data-testid="cash-flow-beyond">
          <td className="pr-3" colSpan={2}>
            Beyond {data.horizon_days} days (no dates — the tail)
          </td>
          <td className="pr-3 text-right">
            {formatMoney(data.beyond_horizon.inflow)}
          </td>
          <td className="pr-3 text-right">
            {formatMoney(data.beyond_horizon.outflow)}
          </td>
          <td className="pr-3 text-right">
            {formatMoney(data.beyond_horizon.nett)}
          </td>
          <td className="pr-3 text-right text-tally-dim">—</td>
        </tr>
      </tbody>
    </table>
  );
}

/**
 * A refusal, stated as the decision it is.
 *
 * ⚠️ UNLIKE THE ORDER BOOKS, NO MIGRATION REFUSES THESE TWO TO ANYBODY TODAY —
 * migration 0011's coarse `allow display report` matches every `report:NAME` at
 * specificity 1, and there is no counterweight for `report:cash-flow` or
 * `report:anomalies`. So a 403 here means an administrator wrote a rule
 * deliberately, which makes it MORE worth naming rather than less: the operator
 * needs to know the refusal is policy, not breakage.
 */
function ForecastError({
  status,
  message,
  title,
  testid,
}: {
  status: number;
  message: string;
  title: string;
  testid: string;
}) {
  if (status === 403) {
    return (
      <div data-testid={`${testid}-denied`} className="max-w-3xl text-sm">
        <p className="font-bold">
          Your security level may not open the {title}.
        </p>
        <p className="mt-1 text-xs">
          This is a decision recorded against your level, not a failure. Printing
          it is a separate permission from reading it, so being refused here does
          not mean the printed copy was granted.
        </p>
      </div>
    );
  }
  return (
    <div data-testid={`${testid}-error`} className="max-w-3xl text-sm">
      <p className="font-bold">
        {title} unavailable{status ? ` (${status})` : ""}.
      </p>
      <p className="mt-1 text-xs">{message}</p>
    </div>
  );
}

/**
 * THE CASH-FLOW PROJECTION — `report:cash-flow`.
 *
 * ⛔ IT READS `periodTo` AND NEVER `periodFrom`. A projection runs FORWARD from
 * one date; bounding it below would drop the already-overdue bills, which are
 * folded into the first bucket precisely because they are the most certain cash in
 * the answer. periodClassification.test.ts asserts that against this source.
 */
export function CashFlowScreen({ snapshot }: { snapshot: NavSnapshot }) {
  const client = useClient();
  const companyID = useCompanyID();
  const asOn = snapshot.config?.periodTo;

  usePrintAction();

  const [data, setData] = useState<CashFlowResponse | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(
    null,
  );

  useEffect(() => {
    let live = true;
    setData(null);
    setError(null);
    client
      .cashFlow(companyID, asOn)
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

  // Alt+E. Registered here rather than inherited, for the same reason the order
  // books register it: REPORT_BAR advertises "Alt+E Export" over every `report`
  // frame, but this screen renders its own table instead of going through
  // useReportView, so without this the advertised key would be dead.
  useExportAction(
    useCallback(() => {
      if (error) {
        return error.status === 403
          ? "Your security level may not open the Cash Flow projection, so there is nothing to export."
          : `Cash Flow unavailable${error.status ? ` (${error.status})` : ""}, so there is nothing to export. ${error.message}`;
      }
      if (data === null) return "Cash Flow is still loading — nothing to export yet.";
      return cashFlowSheet(data);
    }, [data, error]),
  );

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">Cash Flow</h1>
      <p className="mt-1 max-w-4xl text-xs">
        {cashFlowPeriodLabel(data?.as_on, asOn)}
        {data
          ? ` · ${data.horizon_days} days ahead in ${data.bucket_days}-day buckets · opening cash ${formatMoney(data.opening_cash)}`
          : ""}
      </p>
      {/* ⚠️ THE HORIZON AND BUCKET WIDTH ARE THE SERVER'S CHOICE AND ARE LABELLED
          AS SUCH. F2 gives an operator a date and nothing else, so presenting
          these as settings would be a control with no keyboard behind it. */}
      <p className="mt-1 max-w-4xl text-xs text-tally-dim">
        F2 sets the date this is projected from. The horizon and bucket width are
        the server's defaults — there is no keyboard control for them.
      </p>

      <div className="mt-3 bg-tally-panel p-3">
        {error ? (
          <ForecastError
            status={error.status}
            message={error.message}
            title="Cash Flow projection"
            testid="cash-flow"
          />
        ) : data === null ? (
          <p data-testid="cash-flow-loading">Loading…</p>
        ) : (
          <CashFlowBuckets data={data} />
        )}
      </div>

      {/* ⛔ THE BASIS RENDERS WHENEVER THERE IS DATA, INCLUDING WHEN EVERY BUCKET
          IS ZERO. An all-zero projection still rests on assumptions, and an empty
          table with no disclosure is exactly the state that reads as fact. */}
      {data ? <CashFlowBasisPanel data={data} /> : null}
    </section>
  );
}

/**
 * The projection as a spreadsheet.
 *
 * ⛔ THE DISCLOSURE TRAVELS WITH IT. A file of fixed-4 figures read away from the
 * screen that produced it is the single most likely place for a projection to be
 * mistaken for a statement of account — so the sheet's title says ESTIMATE, and
 * the method, the three basis counts and every `not_modelled` line are written
 * into the rows. An export that dropped them would be the D23 defect with a
 * longer half-life, because a spreadsheet outlives the session that made it.
 */
export function cashFlowSheet(data: CashFlowResponse): ExportSheet {
  const rows: ExportRow[] = data.buckets.map((k) => [
    textCell(k.from),
    textCell(k.to),
    moneyCell(k.inflow),
    moneyCell(k.outflow),
    moneyCell(k.nett),
    moneyCell(k.closing_cash),
  ]);
  rows.push([
    textCell(`Beyond ${data.horizon_days} days`),
    textCell(""),
    moneyCell(data.beyond_horizon.inflow),
    moneyCell(data.beyond_horizon.outflow),
    moneyCell(data.beyond_horizon.nett),
    textCell(""),
  ]);
  const a = data.basis.assumed_settlement_date;
  const basisRow = (label: string, c: CashFlowBasisCount): ExportRow => [
    textCell(label),
    textCell(`${c.bills} bill(s)`),
    moneyCell(c.inflow),
    moneyCell(c.outflow),
    textCell(""),
    textCell(""),
  ];
  rows.push([textCell(""), textCell(""), textCell(""), textCell(""), textCell(""), textCell("")]);
  rows.push([
    textCell(`METHOD: ${data.method}`),
    textCell(""),
    textCell(""),
    textCell(""),
    textCell(""),
    textCell(""),
  ]);
  rows.push([
    textCell(data.basis.what_is_projected),
    textCell(""),
    textCell(""),
    textCell(""),
    textCell(""),
    textCell(""),
  ]);
  rows.push(basisRow("Settlement date from the bill's own due date (observed)", a.from_due_date));
  rows.push(basisRow("Settlement date from the party's credit period (assumed)", a.from_party_credit_days));
  rows.push(
    basisRow(
      `Settlement date from this build's ${a.default_credit_days}-day convention (assumed — no party or bill said so)`,
      a.from_default_credit_days,
    ),
  );
  rows.push(
    basisRow(
      "Already overdue, folded into the first bucket",
      data.basis.overdue_folded_into_first_bucket,
    ),
  );
  for (const n of data.basis.not_modelled) {
    rows.push([
      textCell(`NOT MODELLED: ${n}`),
      textCell(""),
      textCell(""),
      textCell(""),
      textCell(""),
      textCell(""),
    ]);
  }
  return {
    title: `Cash Flow — ESTIMATE, projected from ${data.as_on}`,
    headers: ["From", "To", "Inflow", "Outflow", "Nett", "Closing cash"],
    rows,
  };
}

// ===========================================================================
// ANOMALIES
// ===========================================================================

/**
 * How the heading names the window.
 *
 * ⚠️ BOTH BOUNDS COME BACK AS "" WHEN NONE WAS SENT, AND THAT MEANS UNBOUNDED,
 * NOT TODAY. Interpolating a blank produced "Entered between  and " on the order
 * books' equivalent — a heading that reads as a field which failed to load — so
 * the unbounded case gets its own sentence.
 *
 * Exported so a test can pin all four combinations without rendering the screen.
 */
export function anomaliesWindowLabel(from: string, to: string): string {
  if (from === "" && to === "") {
    return "Every finding in the books — F2 sets a window on when vouchers were ENTERED";
  }
  if (from === "") return `Everything entered up to ${to}`;
  if (to === "") return `Everything entered from ${from} onwards`;
  return `Entered between ${from} and ${to}`;
}

/**
 * THE ANOMALY LIST — `report:anomalies`.
 *
 * ⛔ THIS IS A CONTROL SURFACE, NOT A REPORT ABOUT MONEY, AND THE PAGE SAYS SO.
 * It names vouchers whose ENTRY looks irregular — backdated, keyed outside
 * business hours, edited three times in a day, or written for a suspiciously round
 * taxable value. A finding is not an accusation and not an error: the whole point
 * is that a human reads it. So the screen never uses the word "error", never
 * offers a remedy, and renders the server's own `kind`, `severity` and `detail`
 * verbatim rather than re-wording them into something that sounds like a verdict.
 *
 * ⛔ AND IT READS BOTH BOUNDS, BOUNDING `created_at`. See the classification note
 * in FrameOverlays.tsx: the entry axis is the only one on which any of these rules
 * means anything.
 */
export function AnomaliesScreen({ snapshot }: { snapshot: NavSnapshot }) {
  const client = useClient();
  const companyID = useCompanyID();
  const from = snapshot.config?.periodFrom;
  const to = snapshot.config?.periodTo;

  usePrintAction();

  const [data, setData] = useState<AnomaliesResponse | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(
    null,
  );

  useEffect(() => {
    let live = true;
    setData(null);
    setError(null);
    client
      .anomalies(companyID, from, to)
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
  }, [client, companyID, from, to]);

  useExportAction(
    useCallback(() => {
      if (error) {
        return error.status === 403
          ? "Your security level may not open the anomaly list, so there is nothing to export."
          : `Anomalies unavailable${error.status ? ` (${error.status})` : ""}, so there is nothing to export. ${error.message}`;
      }
      if (data === null) return "Anomalies is still loading — nothing to export yet.";
      if (data.findings.length === 0) {
        return "No findings in this window, so there is nothing to export.";
      }
      return anomaliesSheet(data);
    }, [data, error]),
  );

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">Anomalies</h1>
      <p className="mt-1 max-w-4xl text-xs">
        {data
          ? anomaliesWindowLabel(data.from, data.to)
          : from || to
            ? `Loading, ${anomaliesWindowLabel(from ?? "", to ?? "").toLowerCase()}`
            : "Loading"}
      </p>
      {/* ⛔ THE AXIS, ON THE PAGE. An operator who read this as a window over
          voucher DATES would conclude the list was wrong the first time a
          backdated voucher failed to appear for its own period. */}
      <p className="mt-1 max-w-4xl text-xs text-tally-dim">
        The window is on when each voucher was ENTERED, not the period it accounts
        for — that is the only axis on which "backdated" means anything. A finding
        is something for a human to look at, not an error and not an accusation.
      </p>

      <div className="mt-3 bg-tally-panel p-3">
        {error ? (
          <ForecastError
            status={error.status}
            message={error.message}
            title="anomaly list"
            testid="anomalies"
          />
        ) : data === null ? (
          <p data-testid="anomalies-loading">Loading…</p>
        ) : data.findings.length === 0 ? (
          <p data-testid="anomalies-empty" className="max-w-3xl text-sm">
            No findings in this window. That is the normal state of a clean set of
            books — it is not an empty screen waiting for data.
          </p>
        ) : (
          <table className="w-full text-sm" data-testid="anomalies-findings">
            <thead>
              <tr className="text-tally-dim">
                <th className="pr-3 text-left font-normal">Finding</th>
                <th className="pr-3 text-left font-normal">Severity</th>
                <th className="pr-3 text-left font-normal">Voucher</th>
                <th className="pr-3 text-left font-normal">Detail</th>
              </tr>
            </thead>
            <tbody>
              {data.findings.map((f, i) => (
                <tr
                  /* ⛔ THE INDEX IS PART OF THE KEY ON PURPOSE. One voucher can
                     carry SEVERAL findings of different kinds — a backdated entry
                     keyed at midnight for a round amount produces three — so
                     keying on voucher_id alone would collapse them, and keying on
                     kind+voucher would still collide if the server ever emitted
                     two of a kind. */
                  key={`${f.voucher_id}-${f.kind}-${i}`}
                  data-testid={`anomalies-row-${f.kind}-${f.voucher_no || f.voucher_id}`}
                >
                  <td className="pr-3">{f.kind}</td>
                  <td className="pr-3">{f.severity}</td>
                  <td className="pr-3">
                    {/* A voucher with no number is a real state (a draft
                        numbering scheme, or a version keyed before numbering) and
                        the id is all there is to name it by. */}
                    {f.voucher_no || f.voucher_id}
                  </td>
                  <td className="pr-3">{f.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  );
}

/** The findings as a spreadsheet. Every column is text — none of it is money. */
export function anomaliesSheet(data: AnomaliesResponse): ExportSheet {
  return {
    title: `Anomalies — ${anomaliesWindowLabel(data.from, data.to)}`,
    headers: ["Finding", "Severity", "Voucher", "Voucher ID", "Detail"],
    rows: data.findings.map((f) => [
      textCell(f.kind),
      textCell(f.severity),
      textCell(f.voucher_no),
      // ⛔ THE ID IS ITS OWN COLUMN IN THE EXPORT, unlike on screen. A spreadsheet
      // is read away from the app and often filtered or joined; a row identified
      // only by a number that may be blank cannot be looked up again.
      textCell(f.voucher_id),
      textCell(f.detail),
    ]),
  };
}
