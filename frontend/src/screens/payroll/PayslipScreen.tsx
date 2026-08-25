import { useEffect, useState } from "react";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import type { ScreenComponentProps } from "../../app/screenRegistry";
import { ApiError } from "../../api";
import type { PayslipResponse } from "../../api";
import { EMPLOYEE_ROW_PREFIX } from "../../keystroke/navMachine";
import { formatMoney } from "../reports/money";
import { usePrintAction } from "../usePrint";
import { WAGE_MONTH_HINT } from "./wageMonth";

/**
 * ONE EMPLOYEE'S PAYSLIP (Track 3) — reached with Enter from the register.
 *
 * ===========================================================================
 * ⛔ IT RESOLVES A CODE, NOT AN ID, AND THAT IS THE SAFETY PROPERTY
 * ===========================================================================
 * The pure navigation machine has no database, so the drill carries the
 * employee's CODE (`EMPLOYEE_ROW_PREFIX`) and this screen resolves it against
 * the served register — exactly as the Bank Reconciliation screen resolves a
 * drilled ledger name and the Cost Centre Summary resolves an axis name.
 *
 * The CODE and not the NAME, which is the one place this differs from those two:
 * `employee.code` is unique in the company (the server refuses a duplicate) and a
 * NAME is not. Two people called the same thing would make this open whichever
 * the resolver met first — ONE PERSON'S ENTIRE PAY UNDER ANOTHER PERSON'S
 * HEADING, correctly rendered, with nothing on the page saying so. That is the
 * worst rendering error this screen can make and it is the reason for the prefix.
 *
 * ===========================================================================
 * THE DEFERRALS ARE PART OF THE PAYSLIP
 * ===========================================================================
 * A component the engine did not compute is rendered with the REASON beside it,
 * because a blank where a number should be is a question the employee will ask,
 * and the honest answer is "the rate has not been notified", not "zero". The
 * server puts them on the response for that reason; dropping them here would
 * turn an explained absence into an unexplained one.
 */
export function PayslipScreen({ snapshot }: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();

  const month = snapshot.config?.wageMonth ?? "";
  const code = (snapshot.detailOf ?? "").startsWith(EMPLOYEE_ROW_PREFIX)
    ? (snapshot.detailOf ?? "").slice(EMPLOYEE_ROW_PREFIX.length)
    : "";

  const [slip, setSlip] = useState<PayslipResponse | null>(null);
  const [denied, setDenied] = useState(false);
  const [error, setError] = useState("");
  /** The employee id resolved from the code, kept so Ctrl+P can send it. */
  const [employeeID, setEmployeeID] = useState("");

  useEffect(() => {
    if (month === "" || code === "") return;
    let live = true;
    // The register is the RESOLVER: it is the served list that maps a code to an
    // id for this month, and it is the same list the operator was looking at when
    // they pressed Enter. Reading it again rather than smuggling an id through the
    // frame is what keeps the machine pure — and it is also the check: a code that
    // is not in THIS month's register has no payslip in it, which is a different
    // answer from "that person does not exist".
    client
      .payrollRegister(companyID, month)
      .then((reg) => {
        if (!live) return;
        const row = reg.rows.find((r) => r.employee_code === code);
        if (!row?.employee_id) {
          setError(
            `${code} has no payslip in the ${month} pay run. They may have joined after the month ended, left before it began, or not been included in the run — the register for ${month} lists everybody who was.`,
          );
          return;
        }
        setEmployeeID(row.employee_id);
        return client.payslip(companyID, month, row.employee_id).then((v) => {
          if (live) {
            setSlip(v);
            setError("");
          }
        });
      })
      .catch((e: unknown) => {
        if (!live) return;
        if (e instanceof ApiError && e.status === 403) {
          setDenied(true);
          return;
        }
        setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      live = false;
    };
  }, [client, code, companyID, month]);

  // Ctrl+P. THIS SCREEN GENUINELY PRINTS — `printPayrollPayslip` is a real
  // layout — and the printed page is the document the employee is HANDED, which
  // is why the name travels with the request: the status line names who went to
  // paper, so handing one person another person's salary is visible without
  // opening the PDF.
  //
  // Both the month and the id come from what was actually LOADED. A month typed
  // but not fetched has produced nothing on screen; printing it would put a
  // document on paper that does not match the page it came from.
  usePrintAction({
    voucher: {
      voucherID: null,
      payrollMonth: slip ? slip.wage_month : null,
      payrollEmployeeID: slip ? employeeID : null,
      payrollEmployeeName: slip ? slip.employee_name : null,
    },
  });

  if (denied) {
    return (
      <section
        data-testid="payslip-denied"
        className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field"
      >
        <h1 className="text-lg font-bold text-tally-title">Payslip</h1>
        <p className="mt-2 max-w-3xl text-sm">
          You do not have permission to read this payslip. A payslip is ONE
          person&apos;s pay in full — every component, every deduction and the
          tax — so it is its own permission, separate from the register: a level
          can be allowed to see the month&apos;s totals and refused any
          individual&apos;s detail. It is refused to the Data Entry level by
          default. An owner can grant it with an{" "}
          <span className="text-tally-title">
            allow display report:payroll-payslip
          </span>{" "}
          rule in Users &amp; Roles.
        </p>
      </section>
    );
  }

  if (month === "" || code === "") {
    return (
      <section
        data-testid="payslip-no-context"
        className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field"
      >
        <h1 className="text-lg font-bold text-tally-title">Payslip</h1>
        <p className="mt-2 max-w-3xl text-sm">
          A payslip is one person&apos;s pay for one wage month, and this screen
          was opened without both. Open it from the Payroll Register (Alt+G &gt;
          Payroll Register) — show a month, highlight a row and press Enter, and
          both come with you. {WAGE_MONTH_HINT}
        </p>
      </section>
    );
  }

  if (error !== "") {
    return (
      <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
        <h1 className="text-lg font-bold text-tally-title">Payslip</h1>
        <p data-testid="payslip-error" className="mt-2 max-w-3xl text-sm">
          {error}
        </p>
      </section>
    );
  }

  if (slip === null) {
    return (
      <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
        <h1 className="text-lg font-bold text-tally-title">Payslip</h1>
        <p className="mt-2 text-sm">Loading…</p>
      </section>
    );
  }

  const earnings = slip.lines.filter((l) => l.direction !== "DEDUCTION");
  const deductions = slip.lines.filter((l) => l.direction === "DEDUCTION");

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      {/* WHO AND WHEN, FROM THE RESPONSE THE FIGURES CAME IN — never from the
          frame. A heading built from the drill would name whoever was
          highlighted, and the figures below come from whatever the server
          resolved; the two must be the same person or the page is a lie. */}
      <h1 className="text-lg font-bold text-tally-title" data-testid="payslip-who">
        Payslip — {slip.employee_code} {slip.employee_name}, {slip.wage_month}
      </h1>
      <p className="mt-1 text-xs">
        Days paid {slip.days_paid} · loss of pay {slip.lop_days} · pay run{" "}
        {slip.pay_run_id}
      </p>
      <p className="text-xs" data-testid="payslip-rulesets">
        Computed under social-security ruleset{" "}
        <span className="text-tally-title">{slip.ss_ruleset_version}</span> and
        income-tax ruleset{" "}
        <span className="text-tally-title">{slip.it_ruleset_version}</span>.
      </p>

      <div className="mt-4 grid max-w-4xl grid-cols-1 gap-6 md:grid-cols-2">
        <LineTable
          title="Earnings"
          testid="payslip-earnings"
          lines={earnings}
          empty="No earnings on this payslip."
        />
        <LineTable
          title="Deductions"
          testid="payslip-deductions"
          lines={deductions}
          empty="Nothing was deducted."
        />
      </div>

      <table className="mt-4 w-full max-w-md text-sm">
        <tbody>
          <tr>
            <td className="py-0.5">Gross</td>
            <td className="py-0.5 text-right" data-testid="payslip-gross">
              {formatMoney(slip.gross)}
            </td>
          </tr>
          <tr>
            <td className="py-0.5">Deductions</td>
            <td className="py-0.5 text-right" data-testid="payslip-deducted">
              {formatMoney(slip.deductions)}
            </td>
          </tr>
          <tr className="border-t border-tally-panel font-bold">
            <td className="py-0.5">Net pay</td>
            <td className="py-0.5 text-right" data-testid="payslip-net">
              {formatMoney(slip.net)}
            </td>
          </tr>
        </tbody>
      </table>

      {/* ⛔ WHAT WAS NOT COMPUTED, AND WHY — ON THE PAYSLIP, not in a log. A
          blank where a number should be is a question the employee will ask, and
          the honest answer is "the rate has not been notified", never "zero". */}
      {slip.deferrals.length > 0 ? (
        <div
          data-testid="payslip-deferrals"
          className="mt-4 max-w-3xl border border-tally-active bg-tally-panel p-2 text-sm"
        >
          <h2 className="text-xs font-bold text-tally-title">
            Not computed on this payslip
          </h2>
          <ul className="mt-1 space-y-1 text-xs">
            {slip.deferrals.map((d) => (
              <li key={`${d.component}:${d.citation}`}>
                <span className="font-bold">{d.component}</span> — {d.reason}
                {d.citation ? ` (${d.citation})` : ""}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      <p className="mt-4 max-w-3xl text-xs">
        Ctrl+P prints the payslip the employee is handed — the server&apos;s own
        document, not this page. Esc returns to the register, which is where the
        month is chosen, so the two can never disagree about which month you are
        reading.
      </p>
    </section>
  );
}

function LineTable({
  title,
  testid,
  lines,
  empty,
}: {
  title: string;
  testid: string;
  lines: PayslipResponse["lines"];
  empty: string;
}) {
  return (
    <div>
      <h2 className="text-sm font-bold text-tally-title">{title}</h2>
      {lines.length === 0 ? (
        <p data-testid={`${testid}-empty`} className="mt-1 text-xs">
          {empty}
        </p>
      ) : (
        <table className="mt-1 w-full text-sm" data-testid={testid}>
          <tbody>
            {lines.map((l) => (
              <tr key={`${l.code}:${l.type}`} data-testid={`${testid}:${l.code}`}>
                <td className="py-0.5 pr-2">{l.code}</td>
                {/* The TAG is the server's classification of the component (what
                    it counts towards), rendered as received. It is not derived
                    here: which components are wages under s.2(88) decides the
                    contribution, and a browser's opinion about it is worthless. */}
                <td className="py-0.5 pr-2 text-xs">{l.tag}</td>
                <td className="py-0.5 text-right">{formatMoney(l.amount)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
