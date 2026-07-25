import { useCallback, useState } from "react";
import { ApiError } from "../api";
import type { QueryResult } from "../api";
import { useClient, useCompanyID } from "../app/CompanyContext";
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
 * On a 422 (untranslatable NL or an invalid DSL) the server returns a helpful
 * message plus a list of example questions; both are shown so the operator can
 * rephrase. The screen never writes; it is purely read-only.
 */

/** Starter questions surfaced as one-click chips (also a discoverability hint). */
const EXAMPLE_QUESTIONS = [
  "trial balance",
  "sales this month",
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

export function QueryScreen(_props: ScreenComponentProps) {
  const companyID = useCompanyID();
  const client = useClient();

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
