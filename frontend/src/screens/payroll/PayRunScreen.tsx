import { useCallback, useEffect, useRef, useState } from "react";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import type { ScreenComponentProps } from "../../app/screenRegistry";
import { ApiError } from "../../api";
import type { EmployeeView, PayRunResponse } from "../../api";
import { usePrintAction } from "../usePrint";
import { isWageMonth, WAGE_MONTH_HINT } from "./wageMonth";

/**
 * THE PAY RUN (Ctrl+F4, Track 3) — one wage month for every eligible employee.
 *
 * ===========================================================================
 * ⛔ WHY THIS IS A SCREEN AND NOT A VOUCHER FRAME
 * ===========================================================================
 * It posts through `/vouchers/payroll`, exactly as an invoice posts through
 * `/vouchers/sales`, and that is deliberate on the server's side: it puts the pay
 * run under the SAME PDP mapping, the same back-dated cutoff and the same
 * WebAuthn step-up as any other write, through the same code.
 *
 * But there is nothing to KEY. A pay run has no particulars — its lines are
 * computed from each employee's salary structure and statutory election for a
 * whole WAGE MONTH — so `VoucherScreen`, which resolves its layout through
 * `configFor(voucherType)`, has no config for it and never could. Ctrl+F4 pushing
 * a voucher frame would not have "partly worked"; it would have taken the
 * operator to an undefined config.
 *
 * ===========================================================================
 * ⛔ THE 403 ON THIS SCREEN MEANS TWO DIFFERENT THINGS AND IT MUST SAY WHICH
 * ===========================================================================
 * The route is under the back-dated cutoff THROUGH THE WAGE MONTH: cored derives
 * the voucher date with `payrolldb.LastDayOf` and judges it. So a refusal may be
 * "your level may not run payroll" OR "that month is closed" — and the second one
 * is about a field this screen DOES have, while nothing on the page mentions a
 * voucher date at all. Told the first sentence, an operator who already has the
 * permission goes and asks for it again, and never learns the period is locked.
 *
 * It is distinguished on the SERVER'S OWN SENTENCE and not on a code we invent,
 * exactly as StockVoucherScreen does: every cutoff refusal in internal/authz
 * carries the words "back-dated cutoff", and no ACL denial does.
 * `auth_payroll_cutoff_test.go` is the contract.
 */
export function PayRunScreen(_props: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();

  const [month, setMonth] = useState("");
  const [narration, setNarration] = useState("");
  const [employees, setEmployees] = useState<EmployeeView[] | null>(null);
  const [result, setResult] = useState<PayRunResponse | null>(null);
  const [refusal, setRefusal] = useState("");
  const [periodLocked, setPeriodLocked] = useState(false);
  const [busy, setBusy] = useState(false);

  // Who the run will cover. Read for the operator's benefit only — the SELECTION
  // is the server's: an empty `inputs` means every eligible employee (active,
  // joined on or before the month ended, not left before it began), and eligibility
  // is a function of the MONTH, which this list cannot evaluate. So the list is
  // labelled as what it is: who is on the payroll, not who will be paid.
  useEffect(() => {
    let live = true;
    client
      .listEmployees(companyID)
      .then((res) => {
        if (live) setEmployees(res.employees);
      })
      .catch(() => {
        if (live) setEmployees(null);
      });
    return () => {
      live = false;
    };
  }, [client, companyID]);

  // Ctrl+P — before any conditional return, and answered by printTarget with a
  // sentence: a pay run is an ACT, not a document. What it produced is the
  // register, and that prints.
  usePrintAction();

  const monthRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    monthRef.current?.focus();
  }, []);

  const post = useCallback(async (): Promise<string> => {
    if (!isWageMonth(month)) {
      // ⛔ REFUSED HERE, WITH NO REQUEST MADE. The server refuses "2026-13" and
      // "2026-1" too — strictly, because a month coerced to a neighbouring one
      // computes somebody's contributions under the wrong statutory window — but
      // a round trip to be told the shape of a field is a 400 in somebody's log
      // that this screen already knows the answer to.
      const message = `Type the wage month first. ${WAGE_MONTH_HINT}`;
      setRefusal(message);
      setPeriodLocked(false);
      return message;
    }
    setBusy(true);
    setRefusal("");
    setPeriodLocked(false);
    try {
      const res = await client.runPayroll(companyID, {
        wage_month: month,
        created_by: "ui",
        narration: narration.trim(),
        // ⛔ NO `inputs`, AND THAT IS THE WHOLE SELECTION DECISION. An empty
        // array means EVERY ELIGIBLE EMPLOYEE with a zero-valued month input,
        // which the server normalises to a full month's paid days. A non-empty
        // one is also the SELECTION — an employee with no row is not in the run —
        // so a client that sent a row per employee it happened to have loaded
        // would silently exclude anybody the list had not reached. Attendance and
        // declarations are not built into this screen yet, and sending nothing is
        // what keeps that from becoming a wrong figure rather than a missing
        // feature.
        inputs: [],
      });
      setResult(res);
      if (res.already_posted) {
        // ⛔ NOT A FRESH POSTING AND NOT A FAILURE. Read as a failure the
        // operator runs it again; read as a success they believe a correction
        // they just keyed took effect, when the stored run is the earlier one.
        const message = `${month} was ALREADY POSTED — voucher ${res.voucher_no}. Nothing was written now, and nothing you have changed since is in it. To correct a posted month you must alter or reverse that voucher; re-running cannot replace it.`;
        setRefusal(message);
        return message;
      }
      const message = `Pay run for ${month} posted — voucher ${res.voucher_no}. Read it back on the Payroll Register (Alt+G > Payroll Register).`;
      return message;
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      if (e instanceof ApiError && e.status === 403) {
        // See this file's header. Distinguished on the server's own sentence.
        if (/back-dated cutoff/i.test(message)) {
          setPeriodLocked(true);
          const full = `Pay run refused: ${message}. This is a CLOSED PERIOD, not a missing permission. ⛔ Note WHICH date is being judged: a pay run has no date field — the server derives the voucher's date from the wage month you typed (its LAST day), and that is the date inside the lock. Run a later month, or ask an Owner to move the back-dated cutoff for your level (Users & Roles).`;
          setRefusal(full);
          return full;
        }
        const full = `Pay run refused: ${message}. Running payroll is its own permission — an employee record carries a PAN, a UAN and a bank IFSC, so it is refused to the Data Entry level by default. Ask an Owner to allow "create payroll" on your level (Users & Roles).`;
        setRefusal(full);
        return full;
      }
      // ⛔ 422s ARE SHOWN VERBATIM AND THEY ARE THE COMMON CASE. Every payroll
      // refusal names the person and what is missing — "no salary structure is in
      // force", "no statutory election is in force for wage month …" — and those
      // sentences are the whole remedy. Softening one into "could not run
      // payroll" would leave an operator with a blocked pay day and nothing to
      // act on.
      const full = `Pay run refused: ${message}`;
      setRefusal(full);
      return full;
    } finally {
      setBusy(false);
    }
  }, [client, companyID, month, narration]);

  useRegisterScreenAction("accept", post);

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">Pay Run</h1>
      <p className="mt-1 max-w-3xl text-xs">
        Computes and posts a whole wage month for every eligible employee at
        once. There is nothing to key: each person&apos;s pay comes from their
        salary structure and statutory election, which are set on the Employees
        screen (Alt+G &gt; Employees, then Alt+S).
      </p>

      <div className="mt-3 flex flex-wrap items-end gap-3 text-sm">
        <label className="flex flex-col">
          <span className="text-xs text-tally-title">Wage month (YYYY-MM)</span>
          <input
            ref={monthRef}
            aria-label="Wage month"
            data-testid="pay-run-month"
            className="w-32 bg-tally-field px-1 text-tally-fieldText outline-none"
            value={month}
            onChange={(e) => setMonth(e.target.value)}
          />
        </label>
        <label className="flex flex-1 flex-col">
          <span className="text-xs text-tally-title">Narration (optional)</span>
          <input
            aria-label="Narration"
            data-testid="pay-run-narration"
            className="w-full bg-tally-field px-1 text-tally-fieldText outline-none"
            value={narration}
            onChange={(e) => setNarration(e.target.value)}
          />
        </label>
        <button
          type="button"
          className="border border-tally-fieldText px-2 py-0.5 text-xs"
          disabled={busy}
          onClick={() => void post()}
        >
          Post pay run (Ctrl+A)
        </button>
        {busy ? <span className="text-xs">working…</span> : null}
      </div>

      <p className="mt-2 max-w-3xl text-xs">
        {WAGE_MONTH_HINT} The voucher&apos;s date is DERIVED from the month (its
        last day) and is not asked for — which is also the date the back-dated
        cutoff judges, so a closed month is refused here even though this screen
        has no date field.
      </p>

      {refusal !== "" ? (
        <p
          data-testid={periodLocked ? "pay-run-period-locked" : "pay-run-refusal"}
          className="mt-3 max-w-3xl border border-tally-bar p-2 text-sm text-tally-bar"
        >
          {refusal}
        </p>
      ) : null}

      {result && !result.already_posted ? (
        <div
          data-testid="pay-run-posted"
          className="mt-3 max-w-3xl border border-tally-fieldText p-2 text-sm"
        >
          <p className="font-bold">
            Posted — voucher {result.voucher_no} ({result.status}).
          </p>
          <p className="mt-1 text-xs">
            The figures are on the Payroll Register for {month}: Alt+G &gt;
            Payroll Register. Each person&apos;s own copy is their payslip, which
            opens with Enter on their row.
          </p>
        </div>
      ) : null}

      {/* WHO IS ON THE PAYROLL — labelled as exactly that, and not as "who will
          be paid". ELIGIBILITY IS A FUNCTION OF THE MONTH (joined on or before
          it ended, did not leave before it began) and the server evaluates it;
          a list here claiming to predict the run would be wrong for every
          joiner and every leaver, which are the months that matter most. */}
      <h2 className="mt-5 text-sm font-bold text-tally-title">
        On the payroll now
      </h2>
      {employees === null ? (
        <p className="mt-1 text-sm">
          The employee list could not be read. The pay run does not depend on it
          — the server selects who is eligible for the month — so this is
          information only.
        </p>
      ) : employees.length === 0 ? (
        <p data-testid="pay-run-no-employees" className="mt-1 max-w-3xl text-sm">
          This company has no employees, so there is nobody to pay. A run with no
          payslips computes nothing and would post an empty voucher, which the
          server refuses. Create somebody on the Employees screen first.
        </p>
      ) : (
        <ul className="mt-1 text-sm" data-testid="pay-run-employees">
          {employees.map((e) => (
            <li key={e.id}>
              {e.code} — {e.name}
              {e.dol ? ` (left ${e.dol})` : ""}
            </li>
          ))}
        </ul>
      )}
      <p className="mt-2 max-w-3xl text-xs">
        Who is actually paid is decided by the SERVER from the month: somebody
        who joined after it ended, or left before it began, is not in the run.
        This list is who is on the payroll today, which is a different question.
      </p>
    </section>
  );
}
