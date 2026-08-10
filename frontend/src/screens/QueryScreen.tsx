import { useCallback, useState } from "react";
import { ApiError } from "../api";
import type { Query, QueryResult } from "../api";
import { useClient, useCompanyContext, useCompanyID } from "../app/CompanyContext";
import type { ScreenComponentProps } from "../app/screenRegistry";

/**
 * The "Ask your books" query screen — the AI-native ask-your-data entry point.
 *
 * The operator types an English question (or picks an example chip); on submit
 * the screen calls `client.query(companyID, { nl })`. The backend translates the
 * NL to a CLOSED typed DSL, compiles it to a FIXED whitelisted skeleton (all
 * user values bound as $N params), and executes it in a READ-ONLY,
 * tenant-scoped transaction — so this screen can NEVER write or leak across
 * tenants. The typed answer (columns + string rows) is rendered verbatim; a
 * collapsible "how this was answered" panel surfaces the echoed DSL and the
 * sql_preview (placeholders only) for transparency. Money stays a string end to
 * end — rows are rendered exactly as the server returned them (no parseFloat).
 *
 * ⛔ TWO CAPTIONS ARE ALWAYS VISIBLE AND ARE NOT DECORATION: the SCOPE line
 * (which company this answer is for) and the BASIS block (which period, and
 * what was measured). Both exist because an answer that is plausible and
 * unlabelled is worse than no answer — see the DISCLOSURE note below.
 *
 * On a 422 (untranslatable NL or an invalid DSL) the server returns a helpful
 * message plus a list of example questions; both are shown so the operator can
 * rephrase. The screen never writes; it is purely read-only.
 */

/** Starter questions surfaced as one-click chips (also a discoverability hint). */
const EXAMPLE_QUESTIONS = [
  "trial balance",
  "sales this month",
  // "sales last month" is a CHIP, not just a supported phrase: it used to answer
  // for the CURRENT month (D21) and the only way an operator could have noticed
  // was to open the collapsed DSL panel. One click now reaches the fixed path
  // and the answered window is printed above the table.
  "sales last month",
  "top debtors",
  "total purchases",
] as const;

/** Extract the example-question list a 422 body may carry (else fall back). */
function examplesFromError(err: ApiError): string[] {
  const body = err.body;
  if (body && typeof body === "object" && "examples" in body) {
    const ex = (body as { examples?: unknown }).examples;
    if (Array.isArray(ex)) return ex.filter((e): e is string => typeof e === "string");
  }
  return [...EXAMPLE_QUESTIONS];
}

/* --------------------------------------------------------------------------
 * DISCLOSURE. Read this before deleting anything below it.
 *
 * The audit drove this screen and found the same failure twice, with two faces:
 *
 *   D21 — "sales last month" / "last quarter" / "last year" answered for the
 *         CURRENT period, and NOTHING ON SCREEN SAID WHICH WINDOW WAS USED.
 *         "last quarter" printed "No rows." for a quarter that held 1,56,500.00.
 *         The DSL that would have revealed the window existed — behind a
 *         collapsed-by-default "how this was answered" panel that an operator
 *         has no reason to open when the answer looks plausible.
 *
 *   D51 — "total sales" answered a figure matching NO report (taxable value of
 *         Sales-typed vouchers: no GST, no credit notes), and the screen said
 *         nothing about what the number was.
 *
 *   D50 — a question naming ANOTHER COMPANY (by name, by UUID, or "every
 *         company") returned 200 with the caller's OWN table and no statement
 *         that the named company had been ignored.
 *
 * The server-side halves are fixed in internal/query. THIS is the half that
 * makes them legible, and both pieces below are deliberately UNCONDITIONAL:
 *
 *   - the SCOPE line renders on every answer, every error, and before the first
 *     question. It needs no detection of what the operator typed, so there is
 *     no pattern list here to drift out of step with the server's.
 *   - the BASIS line is derived from the DSL THE SERVER ECHOED BACK, never from
 *     the question. It therefore cannot claim a period or a measure that the
 *     answer was not actually computed with — the failure mode a hand-written
 *     caption would have.
 * ------------------------------------------------------------------------ */

/** Render the answered date window from the echoed DSL, in words. */
function periodBasis(dsl: Query | undefined): string {
  const dr = dsl?.date_range;
  if (!dr || (!dr.from && !dr.to)) {
    return "Period: ALL DATES — the question named no period, so nothing was filtered out by date.";
  }
  if (dr.from && dr.to) return `Period: ${dr.from} to ${dr.to} (on ${dr.column}), inclusive.`;
  if (dr.from) return `Period: ${dr.from} onwards (on ${dr.column}).`;
  return `Period: up to and including ${dr.to} (on ${dr.column}).`;
}

/**
 * Describe WHAT WAS MEASURED, keyed off the metric column the server actually
 * used. An unknown column falls through to a literal restatement of the metric
 * rather than to silence — an undisclosed figure is the defect.
 */
function measureBasis(dsl: Query | undefined): string[] {
  const out: string[] = [];
  for (const m of dsl?.metrics ?? []) {
    switch (m.column) {
      case "natural_net":
        out.push(
          "Measure: posted movement on the ledgers rolling up into this Profit & Loss group, " +
            "signed to each ledger's natural side — the SAME figure the Profit & Loss shows. " +
            "It is NET of credit/debit notes (a sales return debits Sales and subtracts itself) " +
            "and EXCLUDES GST, which posts to Duties & Taxes.",
        );
        break;
      case "pending":
        out.push(
          "Measure: bill-wise pending AS AT TODAY, from both sources the Outstandings report " +
            "reads — references raised by vouchers dated on or before today, plus the opening " +
            "bill-wise breakup carried on the ledger master. Bills dated in the future are not " +
            "outstanding yet and are excluded.",
        );
        break;
      case "closing_net":
        out.push(
          "Measure: closing position per ledger — opening balance folded in, so it reconciles " +
            "with the Trial Balance.",
        );
        break;
      case "debit":
      case "credit":
      case "net":
        out.push(
          `Measure: POSTED MOVEMENT only (${m.func}(${m.column})); opening balances are NOT ` +
            "included, so this differs from the Trial Balance by exactly the openings.",
        );
        break;
      case "taxable_value":
      case "cgst":
      case "sgst":
      case "igst":
        out.push(
          `Measure: ${m.func}(${m.column}) stamped on the voucher by GST derivation. It is ` +
            "blank on vouchers posted without GST, and it counts only the voucher types this " +
            "query filtered to — it is NOT a Profit & Loss figure.",
        );
        break;
      default:
        out.push(`Measure: ${m.func}(${m.column || "*"}) as "${m.as || m.column}".`);
    }
  }
  return out;
}

export function QueryScreen(_props: ScreenComponentProps) {
  const companyID = useCompanyID();
  const client = useClient();
  // The company NAME, so the scope statement names the books it answered for.
  // useCompanyID already threw if there is none, so `company` is set here.
  const { company } = useCompanyContext();
  const companyName = company?.name ?? companyID;

  const [nl, setNl] = useState("");
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<QueryResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [examples, setExamples] = useState<string[]>([]);
  const [retryable, setRetryable] = useState(false);
  const [showHow, setShowHow] = useState(false);

  const run = useCallback(
    async (question: string) => {
      const q = question.trim();
      if (q.length === 0) return;
      setLoading(true);
      setError(null);
      setExamples([]);
      setRetryable(false);
      try {
        // Send ONLY the NL string. The server owns translation + compilation +
        // read-only execution; the frontend never builds SQL.
        const res = await client.query(companyID, { nl: q });
        setResult(res);
        setShowHow(false);
      } catch (e) {
        // A 422 carries a helpful message + example questions; surface both so
        // the operator can rephrase. Other errors show their message verbatim.
        setResult(null);
        if (e instanceof ApiError) {
          setError(e.message);
          // A 503 is an OPERATOR fault (the real-Claude translator upstream is
          // down/misconfigured), NOT a bad question. Show a retry hint, and do
          // NOT fall back to the canned example list — that would wrongly imply
          // the question needs rephrasing.
          if (e.status === 503) {
            setExamples([]);
            setRetryable(true);
          } else {
            setExamples(examplesFromError(e));
          }
        } else {
          setError(e instanceof Error ? e.message : String(e));
        }
      } finally {
        setLoading(false);
      }
    },
    [client, companyID],
  );

  const onSubmit = useCallback(
    (ev: React.FormEvent) => {
      ev.preventDefault();
      void run(nl);
    },
    [run, nl],
  );

  const pickExample = useCallback(
    (q: string) => {
      setNl(q);
      void run(q);
    },
    [run],
  );

  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="mb-2 text-lg font-bold text-tally-title">Ask your books</h1>
      <p className="mb-3 text-sm">
        Ask a question in plain English; the answer is computed by the safe
        read-only query core. Esc to go back.
      </p>

      <form onSubmit={onSubmit} className="mb-3 max-w-xl">
        <input
          aria-label="Ask your books"
          data-testid="query-input"
          className="w-full bg-tally-field px-1 text-tally-fieldText outline-none"
          value={nl}
          onChange={(e) => setNl(e.target.value)}
          placeholder="Ask your books… e.g. sales this month"
        />
        <button
          type="submit"
          data-testid="query-submit"
          disabled={loading || nl.trim().length === 0}
          className="mt-2 bg-tally-active px-2 py-1 text-xs text-white disabled:opacity-50"
        >
          {loading ? "Asking…" : "Ask"}
        </button>
      </form>

      <div className="mb-3 flex flex-wrap gap-2">
        {EXAMPLE_QUESTIONS.map((q) => (
          <button
            key={q}
            type="button"
            data-testid={`query-example-${q}`}
            className="bg-tally-panel px-2 py-1 text-xs text-tally-fieldText hover:bg-tally-accent"
            onClick={() => pickExample(q)}
          >
            {q}
          </button>
        ))}
      </div>

      {/*
        THE SCOPE STATEMENT — UNCONDITIONAL, AND THAT IS THE POINT (D50).
        It renders before the first question, with every answer, and with every
        refusal. A cross-tenant probe that names a company by a NAME nobody
        enumerated still lands under this caption, so the operator can never be
        shown their own books as though they were somebody else's. Do not make
        it conditional on the question text: a caption that only appears when a
        list matches is a caption that is missing exactly when the list is wrong.
      */}
      <p data-testid="query-scope" className="mb-3 max-w-xl bg-tally-panel p-2 text-xs">
        Scope: <span className="text-tally-title">{companyName}</span>, and only
        this company. Answers are computed inside this company&rsquo;s books;
        this screen cannot read another company&rsquo;s, and a question naming
        one is refused rather than answered for this one.
      </p>

      {error ? (
        <div data-testid="query-error" className="mb-3 max-w-xl bg-tally-panel p-3">
          <p className="text-sm text-red-400">{error}</p>
          {retryable ? (
            <p data-testid="query-retry-hint" className="mt-2 text-xs text-tally-field">
              The AI translator is temporarily unavailable. Retry in a moment, or
              type a typed query (DSL) directly.
            </p>
          ) : null}
          {examples.length > 0 ? (
            <div className="mt-2 text-xs">
              <span className="text-tally-field">Try one of these: </span>
              <ul className="mt-1 list-disc pl-5">
                {examples.map((ex) => (
                  <li key={ex} data-testid="query-error-example">
                    {ex}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
      ) : null}

      {result ? (
        <div className="max-w-3xl">
          {/*
            THE BASIS OF THIS ANSWER — ABOVE THE TABLE, NOT BEHIND A TOGGLE
            (D21, D51). Every line is derived from `result.dsl`, i.e. from what
            the SERVER says it computed, so it cannot describe a window or a
            measure the answer did not use. The window in particular has to be
            visible without opening anything: "No rows." for a period the
            operator did not ask about is indistinguishable from "no business
            that period" until the window is on screen.
          */}
          <div data-testid="query-basis" className="mb-2 bg-tally-panel p-2 text-xs">
            <p data-testid="query-basis-period" className="text-tally-fieldText">
              {periodBasis(result.dsl)}
            </p>
            {measureBasis(result.dsl).map((line) => (
              <p key={line} data-testid="query-basis-measure" className="mt-1 text-tally-fieldText">
                {line}
              </p>
            ))}
          </div>

          <div className="overflow-auto bg-tally-panel p-3">
            <table data-testid="query-table" className="text-sm">
              <thead>
                <tr>
                  {result.columns.map((c) => (
                    <th
                      key={c.name}
                      className="px-2 py-1 text-left text-tally-title"
                      data-testid="query-col"
                    >
                      {c.name}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {result.rows.length === 0 ? (
                  <tr>
                    <td
                      data-testid="query-empty"
                      colSpan={Math.max(result.columns.length, 1)}
                      className="px-2 py-1 text-tally-fieldText"
                    >
                      No rows.
                    </td>
                  </tr>
                ) : (
                  result.rows.map((row, i) => (
                    <tr key={i} data-testid="query-row">
                      {row.map((cell, j) => (
                        // Render the cell STRING verbatim — money/dates are
                        // already strings from the server; never parseFloat.
                        <td
                          key={j}
                          className="px-2 py-1 text-tally-fieldText"
                        >
                          {cell}
                        </td>
                      ))}
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>

          <button
            type="button"
            data-testid="query-how-toggle"
            className="mt-2 text-xs text-tally-field underline"
            onClick={() => setShowHow((s) => !s)}
          >
            {showHow ? "Hide" : "Show"} how this was answered
          </button>
          {showHow ? (
            <div data-testid="query-how" className="mt-1 bg-tally-panel p-3 text-xs">
              <p className="text-tally-title">SQL preview (placeholders only)</p>
              <pre
                data-testid="query-sql-preview"
                className="mt-1 overflow-auto whitespace-pre-wrap text-tally-fieldText"
              >
                {result.sql_preview}
              </pre>
              <p className="mt-2 text-tally-title">Typed query (DSL)</p>
              <pre
                data-testid="query-dsl"
                className="mt-1 overflow-auto whitespace-pre-wrap text-tally-fieldText"
              >
                {JSON.stringify(result.dsl, null, 2)}
              </pre>
            </div>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
