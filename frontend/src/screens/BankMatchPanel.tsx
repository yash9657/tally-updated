import { useCallback, useEffect, useMemo, useState } from "react";
import { useClient, useCompanyID } from "../app/CompanyContext";
import { ApiError } from "../api";
import type {
  BankMatchCandidate,
  BankMatchesResponse,
  BankMatchProposal,
} from "../api";
import { formatMoney } from "./reports/money";

/**
 * BANK STATEMENT MATCHING (T4 slice 2b) — the panel Alt+S opens on the Bank
 * Reconciliation screen.
 *
 * Design memo: docs/design/t4-slice2-model.md §0, §1.2, §5, §6.
 * Keys: RESEARCH.md:879.
 *
 * ===========================================================================
 * ⛔ THE ONE RULE THIS PANEL EXISTS TO RENDER: A PROPOSAL IS NOT A
 *    RECONCILIATION, AND THE TWO NEVER LOOK THE SAME
 * ===========================================================================
 * Everything in the PROPOSED column is the machine's opinion. Everything in the
 * RESOLVED column is something a person asserted. They are drawn as two lists
 * with different headings and different words — never one list with a status
 * chip — because the whole value of a bank reconciliation is that a human said
 * "this line is that voucher", and a screen that blurs the two produces a
 * reconciliation that attests to nothing while footing perfectly.
 *
 * WHAT THIS PANEL WILL NOT DO
 * ---------------------------
 *  - It does not compute money. Every amount is a fixed-4 decimal STRING
 *    rendered by formatMoney, never parsed into a JS number.
 *  - It does not decide a tier, an ambiguity or a bulk-acceptability. All three
 *    are the SERVER's verdict, read off the response. A browser that re-derived
 *    `ambiguous` from the candidate count would miss the CONTESTED case — one
 *    voucher wanted by two bank rows — where each row has exactly one candidate
 *    and looks perfectly unambiguous on its own.
 *  - ⛔ IT NEVER AUTO-ACCEPTS. Not on load, not on a single exact match, not
 *    ever. "Accept all" is a key the operator presses, and it accepts only rows
 *    the server marked `acceptable_in_bulk`.
 *  - It does not hide what it could not do. The server's `disclosures` are
 *    rendered at the top, and the first of them says in those words that nothing
 *    here has been reconciled.
 */

type Props = {
  ledgerID: string;
  ledgerName: string;
  /** The statement date from F2, so proposals cover the period on screen. */
  to?: string;
  onClose: () => void;
  /** Called after any write, so the parent can re-read the BRS. */
  onWrote: () => void;
  /** Which sub-view Ctrl+J / Ctrl+B asked for. */
  view: "match" | "exceptions" | "basis";
  onView: (v: "match" | "exceptions" | "basis") => void;
  /** The row the parent's F8/F9/Alt+F8 keys act on. */
  selected: string | null;
  onSelect: (lineID: string | null) => void;
};

const TIER_WORDS: Record<string, string> = {
  exact: "EXACT",
  potential: "POTENTIAL",
  partial: "PARTIAL",
  manual: "MANUAL",
};

/**
 * How each tier is EXPLAINED, in the operator's words rather than the wire's.
 *
 * ⛔ "EXACT" IS NOT SPELLED AS CERTAINTY. It is the strongest thing the matcher
 * found, and the sentence says what that rests on, because an operator who reads
 * "exact" as "verified" will accept without looking — which is the one habit
 * this feature must not create.
 */
const TIER_MEANING: Record<string, string> = {
  exact:
    "the amount and direction agree to the paisa AND the dates agree (or the bank's own reference is on the voucher). It is the strongest match this build can find — it is not a verification.",
  potential:
    "the amount and direction agree to the paisa, but the dates differ. A cheque written on one day and cleared on another is the ORDINARY case, so this is a normal outcome and not a warning.",
  partial:
    "the amounts do not agree one-for-one: several bank rows make up one voucher, or one bank row nets several. Shown so you can go and look; never offered for bulk acceptance.",
  manual: "a person's say-so. The matcher offered this leg at no tier.",
};

export function BankMatchPanel({
  ledgerID,
  ledgerName,
  to,
  onClose,
  onWrote,
  view,
  onView,
  selected,
  onSelect,
}: Props) {
  const client = useClient();
  const companyID = useCompanyID();

  const [data, setData] = useState<BankMatchesResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  /**
   * The window the operator asked for, or null for "the build's own".
   *
   * ⚠️ IT IS SENT ONLY WHEN CHOSEN. The response states the window actually
   * used, and the panel renders THAT rather than this — so a widened window can
   * never be displayed as though it were the default, or the reverse.
   */
  const [windowDays, setWindowDays] = useState<number | null>(null);
  const [gen, setGen] = useState(0);

  const load = useCallback(() => {
    setBusy(true);
    setError(null);
    client
      .bankStatementMatches(companyID, ledgerID, {
        to,
        ...(windowDays ? { windowDays } : {}),
      })
      .then((res) => {
        setData(res);
        setBusy(false);
      })
      .catch((err: unknown) => {
        setError(err instanceof Error ? err.message : String(err));
        setBusy(false);
      });
  }, [client, companyID, ledgerID, to, windowDays]);

  useEffect(load, [load, gen]);

  const proposals = data?.proposals ?? [];

  /** The rows a bulk accept would touch — the SERVER's flag, never re-derived. */
  const bulk = useMemo(
    () => proposals.filter((p) => p.acceptable_in_bulk),
    [proposals],
  );

  /**
   * Accept ONE proposal: assert that this bank row IS that voucher leg.
   *
   * ⛔ IT SENDS THE BANK ROW'S OWN VALUE DATE, and the server refuses any other.
   * The date is also what the back-dated cutoff judges, which is why it travels
   * in the body rather than being looked up server-side from the line.
   */
  const accept = useCallback(
    async (p: BankMatchProposal, c: BankMatchCandidate) => {
      setBusy(true);
      setError(null);
      setNote(null);
      try {
        await client.setBankDate(companyID, ledgerID, c.voucher_id, c.leg_no, {
          bank_date: p.value_date,
          created_by: "ui",
          statement_line_id: p.line_id,
        });
        setNote(
          `Recorded: the bank row of ${formatMoney(p.amount)} on ${p.value_date} ` +
            `is ${c.class} ${c.voucher_no}, cleared on ${p.value_date}.`,
        );
        onWrote();
        setGen((g) => g + 1);
      } catch (err) {
        setError(err instanceof ApiError ? err.message : String(err));
        setBusy(false);
      }
    },
    [client, companyID, ledgerID, onWrote],
  );

  /** Accept every unambiguous exact match, one call each. */
  const acceptAll = useCallback(async () => {
    setBusy(true);
    setError(null);
    setNote(null);
    let done = 0;
    try {
      for (const p of bulk) {
        const c = p.candidates[0];
        if (!c) continue;
        await client.setBankDate(companyID, ledgerID, c.voucher_id, c.leg_no, {
          bank_date: p.value_date,
          created_by: "ui",
          statement_line_id: p.line_id,
        });
        done++;
      }
      setNote(
        `${done} bank row(s) accepted — each one is now asserted to BE the voucher ` +
          `named beside it, with the bank's own date. Anything ambiguous was left for you.`,
      );
      onWrote();
      setGen((g) => g + 1);
    } catch (err) {
      // ⚠️ A PARTIAL RUN IS REPORTED AS ONE. Each acceptance is its own write, so
      // a failure part-way leaves the earlier ones asserted — saying "failed"
      // without the count would send an operator looking for work already done.
      setError(
        `${done} row(s) were accepted before this stopped: ` +
          (err instanceof Error ? err.message : String(err)),
      );
      setBusy(false);
      onWrote();
      setGen((g) => g + 1);
    }
  }, [bulk, client, companyID, ledgerID, onWrote]);

  const setAside = useCallback(
    async (lineID: string) => {
      setBusy(true);
      setError(null);
      setNote(null);
      try {
        await client.setAsideStatementLine(companyID, ledgerID, lineID, {
          note: "no voucher in the books for this row",
          created_by: "ui",
        });
        setNote(
          "Set aside. This bank row is recorded as matching NOTHING in the books — " +
            "it is not reconciled, no bank date was written, and no balance moved.",
        );
        setGen((g) => g + 1);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
        setBusy(false);
      }
    },
    [client, companyID, ledgerID],
  );

  const restore = useCallback(
    async (lineID: string) => {
      setBusy(true);
      setError(null);
      setNote(null);
      try {
        await client.restoreStatementLine(companyID, ledgerID, lineID);
        setNote("Returned to the unmatched list.");
        setGen((g) => g + 1);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
        setBusy(false);
      }
    },
    [client, companyID, ledgerID],
  );

  /**
   * Withdraw a match — Alt+F8, and F8's first half.
   *
   * ⛔ IT UN-TICKS THE BANK DATE, which is the same write the Bank Date column's
   * DELETE makes, and the match row goes with it by migration 0088's cascade.
   * There is no separate "unlink": a match that outlived its clearance would
   * leave the leg un-ticked while the bank row still read as matched.
   */
  const unlink = useCallback(
    async (voucherID: string, legNo: number) => {
      setBusy(true);
      setError(null);
      setNote(null);
      try {
        await client.clearBankDate(companyID, ledgerID, voucherID, legNo);
        setNote(
          "Match withdrawn, and the bank date it recorded with it — the voucher is " +
            "un-cleared again and the bank row is back among the proposals.",
        );
        onWrote();
        setGen((g) => g + 1);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
        setBusy(false);
      }
    },
    [client, companyID, ledgerID, onWrote],
  );

  // Expose the row-scoped operations to the parent's F8 / F9 / Alt+F8.
  useEffect(() => {
    panelOps.setAside = setAside;
    panelOps.restore = restore;
    panelOps.unlink = unlink;
    panelOps.acceptAll = acceptAll;
    panelOps.data = data;
    return () => {
      panelOps.setAside = null;
      panelOps.restore = null;
      panelOps.unlink = null;
      panelOps.acceptAll = null;
      panelOps.data = null;
    };
  }, [setAside, restore, unlink, acceptAll, data]);

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 text-tally-field">
      <h1 className="mb-1 text-center font-mono text-lg font-bold text-tally-title">
        Match Bank Statement — {ledgerName}
      </h1>
      <p className="mb-3 text-center text-xs">
        Esc closes. Ctrl+J exception report · Ctrl+B basis of values · F9 set a
        row aside · Alt+F8 withdraw a match
      </p>

      {/* ⛔ THE DISCLOSURES COME FIRST AND ARE NOT COLLAPSIBLE. The first one
          says nothing here has been reconciled, which is this panel's most
          dangerous possible misreading. */}
      {data && data.disclosures.length > 0 && (
        <ul
          data-testid="match-disclosures"
          className="mb-3 space-y-1 border border-tally-title bg-tally-panel p-2 text-xs"
        >
          {data.disclosures.map((d) => (
            <li key={d}>⚠️ {d}</li>
          ))}
        </ul>
      )}

      {error && (
        <p data-testid="match-error" className="mb-2 text-sm text-tally-title">
          {error}
        </p>
      )}
      {note && (
        <p data-testid="match-note" className="mb-2 text-sm">
          {note}
        </p>
      )}

      {view === "basis" && data && (
        <div data-testid="match-basis" className="mb-3 bg-tally-panel p-3 text-xs">
          <h2 className="mb-2 font-bold text-tally-title">
            BASIS OF VALUES — how these proposals were arrived at
          </h2>
          <p className="mb-1">
            Amounts are compared <strong>to the paisa, with zero tolerance</strong>.
            Direction is compared directly: a deposit can only match a receipt.
          </p>
          <p className="mb-1">
            Candidates were sought within{" "}
            <strong>{data.date_window_days} days</strong> either side of the
            bank's date. That is this build's <em>search width</em>, not a rule
            from anywhere — widening it shows more proposals and clears nothing.
          </p>
          <p className="mb-1">
            A voucher leg that has <strong>already been cleared</strong> — by
            matching or by typing a Bank Date — is not offered as a candidate, so
            nothing can be cleared twice.
          </p>
          <ul className="mt-2 space-y-1">
            {Object.entries(TIER_MEANING).map(([tier, meaning]) => (
              <li key={tier}>
                <strong>{TIER_WORDS[tier]}</strong> — {meaning}
              </li>
            ))}
          </ul>
          <label className="mt-3 flex items-center gap-2">
            Search window (days):
            <input
              type="number"
              min={0}
              className="w-20 bg-tally-bg px-1"
              defaultValue={data.date_window_days}
              onBlur={(e) => {
                const v = Number(e.target.value);
                setWindowDays(Number.isFinite(v) && v >= 0 ? v : null);
              }}
            />
          </label>
        </div>
      )}

      {view === "exceptions" && data && (
        <div
          data-testid="match-exceptions"
          className="mb-3 bg-tally-panel p-3 text-xs"
        >
          <h2 className="mb-2 font-bold text-tally-title">
            EXCEPTION REPORT — what is still outstanding, and why
          </h2>
          {/* Every count is the SERVER's, over the same payload the list above
              renders. Two sources for "what is outstanding?" would be two
              answers that can disagree. */}
          <ul className="space-y-1">
            <li>
              <strong>{data.counts.no_candidate}</strong> bank row(s) match{" "}
              <strong>nothing</strong> in the books. Some are real —
              bank charges, standing orders, drawings — and F9 sets those aside.
            </li>
            <li>
              <strong>{data.counts.ambiguous}</strong> bank row(s) are{" "}
              <strong>ambiguous</strong>: they fit more than one voucher, or
              another bank row wants the same voucher. None has been chosen for
              you.
            </li>
            <li>
              <strong>{data.counts.potential}</strong> at POTENTIAL — the amount
              agrees but the date does not.
            </li>
            <li>
              <strong>{data.counts.partial}</strong> at PARTIAL — part payments
              or netted rows.
            </li>
            <li>
              <strong>{data.counts.unmatched_legs}</strong> voucher leg(s) that
              no bank row matches — <em>books-only unreconciled</em>. A cheque
              written and not yet presented is the ordinary member of this list.
            </li>
            <li>
              <strong>{data.counts.exact}</strong> at EXACT, of which{" "}
              <strong>{data.counts.acceptable_in_bulk}</strong> are unambiguous
              and could be accepted together.
            </li>
          </ul>
          {data.resolved.some((r) => r.bank_date_differs) && (
            <p className="mt-2">
              ⚠️ A matched row's bank date has been changed since it was matched,
              so the tick and the statement row no longer agree about{" "}
              <em>when</em>. See the resolved list below.
            </p>
          )}
        </div>
      )}

      <div className="mb-2 flex items-center gap-3 text-xs">
        <button
          type="button"
          data-testid="accept-all"
          disabled={busy || bulk.length === 0}
          onClick={acceptAll}
          className="border border-tally-title px-2 py-1 disabled:opacity-40"
        >
          Accept {bulk.length} unambiguous exact match
          {bulk.length === 1 ? "" : "es"}
        </button>
        {/* ⛔ THE SENTENCE BESIDE THE BUTTON IS PART OF THE FEATURE. Accepting
            is an ASSERTION, and a button that saved keystrokes without saying
            what it asserts is how an operator ends up attesting to something
            they never read. */}
        <span>
          Accepting asserts that each of those bank rows <strong>is</strong> the
          voucher named beside it. Nothing else is touched.
        </span>
      </div>

      <button
        type="button"
        onClick={() => onView(view === "match" ? "exceptions" : "match")}
        className="sr-only"
      >
        toggle view
      </button>

      <h2 className="mt-2 text-sm font-bold text-tally-title">
        PROPOSED — the machine's opinion. Nothing here is reconciled.
      </h2>
      {proposals.length === 0 && !busy && (
        <p data-testid="no-proposals" className="text-xs">
          Every imported bank row on this account has been decided. Import a
          statement with Alt+O, or widen the period with F2.
        </p>
      )}
      <table className="w-full border-collapse text-xs">
        <tbody>
          {proposals.map((p) => (
            <tr
              key={p.line_id}
              data-testid={`proposal-${p.line_no}`}
              onClick={() => onSelect(p.line_id)}
              className={
                selected === p.line_id
                  ? "bg-tally-panel align-top"
                  : "align-top"
              }
            >
              <td className="w-64 border-t border-tally-panel py-1 pr-2">
                <div>
                  {p.value_date} · {p.dr_cr === "D" ? "IN" : "OUT"}{" "}
                  {formatMoney(p.amount)}
                </div>
                <div className="opacity-70">{p.description}</div>
                {p.cheque_number && <div>cheque {p.cheque_number}</div>}
              </td>
              <td className="border-t border-tally-panel py-1">
                {p.ambiguous && (
                  <div
                    data-testid={`ambiguous-${p.line_no}`}
                    className="font-bold text-tally-title"
                  >
                    AMBIGUOUS — not accepted, and not chosen for you.
                    {p.contested_by && p.contested_by.length > 0 && (
                      <>
                        {" "}
                        File line{p.contested_by.length > 1 ? "s" : ""}{" "}
                        {p.contested_by.join(", ")} want the same voucher.
                      </>
                    )}
                  </div>
                )}
                {p.candidates.length === 0 && (
                  <div>
                    No voucher matches this row.{" "}
                    <button
                      type="button"
                      data-testid={`set-aside-${p.line_no}`}
                      disabled={busy}
                      onClick={() => setAside(p.line_id)}
                      className="underline"
                    >
                      Set it aside (F9)
                    </button>{" "}
                    if the books have nothing for it.
                  </div>
                )}
                {p.candidates.map((c) => (
                  <div
                    key={`${c.voucher_id}:${c.leg_no}`}
                    className="mb-1 flex items-start gap-2"
                  >
                    <span className="w-20 shrink-0 font-bold">
                      {TIER_WORDS[c.tier] ?? c.tier}
                    </span>
                    <span className="flex-1">
                      {c.class} {c.voucher_no} · {c.voucher_date} ·{" "}
                      {formatMoney(c.amount)}
                      {c.party ? ` · ${c.party}` : ""}
                      <div className="opacity-70">{c.why}</div>
                    </span>
                    <button
                      type="button"
                      data-testid={`accept-${p.line_no}-${c.voucher_no}`}
                      disabled={busy}
                      onClick={() => accept(p, c)}
                      className="border border-tally-title px-1 disabled:opacity-40"
                    >
                      This is it
                    </button>
                  </div>
                ))}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <h2 className="mt-4 text-sm font-bold text-tally-title">
        RESOLVED — what a person asserted.
      </h2>
      {data && data.resolved.length === 0 && (
        <p className="text-xs">Nothing has been resolved on this account yet.</p>
      )}
      <ul className="text-xs">
        {(data?.resolved ?? []).map((r) => (
          <li
            key={r.line_id}
            data-testid={`resolved-${r.line_id}`}
            // ⛔ A RESOLVED ROW IS SELECTABLE TOO, and it has to be: F8 and
            // Alt+F8 act on "the selected bank row", and the row they act on is
            // always one that has ALREADY been decided. Without this the two
            // keys could never find a target and would refuse forever — while
            // the buttons beside each row went on working, so nothing on screen
            // would look wrong.
            onClick={() => onSelect(r.line_id)}
            className={
              selected === r.line_id
                ? "border-t border-tally-panel bg-tally-panel py-1"
                : "border-t border-tally-panel py-1"
            }
          >
            {r.status === "matched" ? (
              <>
                <strong>MATCHED</strong> to {r.class} {r.voucher_no}, cleared{" "}
                {r.bank_date} ({TIER_WORDS[r.tier] ?? r.tier}, by {r.matched_by})
                {r.bank_date_differs && (
                  <div className="font-bold text-tally-title">
                    ⚠️ The bank date has been changed since this was matched: the
                    statement row is dated {r.line_value_date} and the tick says{" "}
                    {r.bank_date}.
                  </div>
                )}{" "}
                <button
                  type="button"
                  data-testid={`unlink-${r.line_id}`}
                  disabled={busy}
                  onClick={() =>
                    r.voucher_id && unlink(r.voucher_id, r.leg_no ?? 0)
                  }
                  className="underline"
                >
                  Withdraw (Alt+F8)
                </button>
              </>
            ) : (
              <>
                {/* ⛔ SET ASIDE IS WORDED SO IT CANNOT BE READ AS RECONCILED. */}
                <strong>SET ASIDE</strong> — this bank row matches nothing in the
                books. <em>Not reconciled; no bank date written.</em>
                {r.note ? ` (${r.note})` : ""}{" "}
                <button
                  type="button"
                  data-testid={`restore-${r.line_id}`}
                  disabled={busy}
                  onClick={() => restore(r.line_id)}
                  className="underline"
                >
                  Return it to the list
                </button>
              </>
            )}
          </li>
        ))}
      </ul>

      <button
        type="button"
        onClick={onClose}
        className="mt-4 self-start border border-tally-title px-2 py-1 text-xs"
      >
        Close (Esc)
      </button>
    </section>
  );
}

/**
 * The row-scoped operations, published for the PARENT's keys.
 *
 * ⚠️ WHY A MODULE OBJECT RATHER THAN PROPS. F8, F9 and Alt+F8 are registered by
 * the SCREEN (that is where useRegisterScreenAction lives and where the keymap
 * points), but they act on the panel's loaded data and the panel's selected row.
 * Threading five callbacks up through props would mean the screen holding state
 * it does not own; this keeps the ownership where the data is. The panel clears
 * every field on unmount, so a key pressed after it closes finds nothing rather
 * than a stale closure.
 */
export const panelOps: {
  setAside: ((lineID: string) => Promise<void>) | null;
  restore: ((lineID: string) => Promise<void>) | null;
  unlink: ((voucherID: string, legNo: number) => Promise<void>) | null;
  acceptAll: (() => Promise<void>) | null;
  data: BankMatchesResponse | null;
} = {
  setAside: null,
  restore: null,
  unlink: null,
  acceptAll: null,
  data: null,
};
