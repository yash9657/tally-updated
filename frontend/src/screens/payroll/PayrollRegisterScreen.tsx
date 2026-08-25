import { useCallback, useEffect, useState } from "react";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import type { ScreenComponentProps } from "../../app/screenRegistry";
import { ApiError } from "../../api";
import type {
  PayrollRegisterResponse,
  PayrollRegisterRow,
  StatutoryFileDownload,
} from "../../api";
import { EMPLOYEE_ROW_PREFIX } from "../../keystroke/navMachine";
import { useExportAction } from "../../export/useExportAction";
import { moneyCell, textCell, type ExportSheet } from "../../export/sheet";
import { formatMoney } from "../reports/money";
import { usePrintAction } from "../usePrint";
import { isWageMonth, WAGE_MONTH_HINT } from "./wageMonth";
import { saveStatutoryArchive } from "./statutoryDownload";

/**
 * THE PAYROLL REGISTER (Track 3) — one wage month, every employee, and the two
 * statutory files that month produces.
 *
 * ===========================================================================
 * ⛔ ITS DIMENSION IS A MONTH TYPED ON ITS FACE, NOT AN F2 PERIOD
 * ===========================================================================
 * `payroll-register` IS a report name on the server and it PRINTS (there is a
 * real layout, not a deferred 501). It is still not a `report` frame here,
 * because a report frame carries a From/To period and this report has no answer
 * for one: contributions, the ECR, the ESI return and every whole-month test are
 * functions of a WAGE MONTH, so a range would be asking for a span no statutory
 * scheme has an answer for. The month is asked for the way the TDS challan
 * register asks for a tax year — and F2 says so rather than opening a dialog
 * that changes nothing.
 *
 * The month lives on the FRAME (`config.wageMonth`) rather than in local state
 * for one reason: it must TRAVEL WITH THE DRILL into a payslip. Without that the
 * primary path — read the register, press Enter on a row — lands on "type a
 * month", and an operator who re-keys it slightly differently reads one
 * person's July payslip under a register for June.
 *
 * ===========================================================================
 * THE RULESET VERSIONS ARE PART OF THE REPORT
 * ===========================================================================
 * They are rendered because a payroll figure with no statement of which law
 * produced it cannot be checked by anybody who was not present when it was run.
 * The server puts them on the JSON and on the printed page; dropping them here is
 * the only place that chain could break.
 */
export function PayrollRegisterScreen({
  snapshot,
  onSelect,
  onConfig,
}: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();

  const frameMonth = snapshot.config?.wageMonth ?? "";
  const [monthText, setMonthText] = useState(frameMonth);
  /** The month the DATA on screen is for — never the one being typed. */
  const [loadedMonth, setLoadedMonth] = useState("");
  const [data, setData] = useState<PayrollRegisterResponse | null>(null);
  const [denied, setDenied] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [filesOpen, setFilesOpen] = useState(false);
  const [fileNotice, setFileNotice] = useState("");

  const highlighted = (snapshot.selectedItem ?? "").startsWith(
    EMPLOYEE_ROW_PREFIX,
  )
    ? (snapshot.selectedItem ?? "").slice(EMPLOYEE_ROW_PREFIX.length)
    : "";

  const load = useCallback(
    async (month: string): Promise<string> => {
      if (!isWageMonth(month)) {
        const message = `Type the wage month first. ${WAGE_MONTH_HINT}`;
        setError(message);
        return message;
      }
      setBusy(true);
      setError("");
      try {
        const res = await client.payrollRegister(companyID, month);
        setData(res);
        setLoadedMonth(month);
        setDenied(false);
        // ⛔ PUBLISH THE LOADED MONTH ONTO THE FRAME. This is the ONLY way Enter
        // on a row can open THAT month's payslip: the month is typed on this
        // screen's own face (a wage month is not an F2 period), so it exists
        // nowhere the pure machine can see it, and a drill from a frame without
        // it lands on "open this from the register" — from the register.
        //
        // ⚠️ THE LOADED MONTH AND NEVER THE TYPED ONE, for the reason Ctrl+P
        // sends the loaded one: a month typed but never fetched has produced
        // nothing on screen, and a drill from it opens a payslip that does not
        // match the page it came from.
        //
        // ⛔ MEASURED IN A BROWSER, NOT REVIEWED. The first version of this
        // screen never called this, and every unit test still passed: they drive
        // the screen with a frame the TEST built, so they were handing it the
        // month it was supposed to be publishing. Only pressing Enter on a real
        // register showed the payslip arriving with nothing.
        onConfig?.({ wageMonth: month });
        return `Payroll register for ${month}.`;
      } catch (e: unknown) {
        // ⛔ THE DATA AND THE LOADED MONTH GO TOGETHER. Leaving the previous
        // month's rows on screen under a heading naming the new one is the whole
        // class of defect this screen has to avoid.
        setData(null);
        setLoadedMonth("");
        if (e instanceof ApiError && e.status === 403) {
          setDenied(true);
          return "Refused.";
        }
        const message = e instanceof Error ? e.message : String(e);
        setError(message);
        return message;
      } finally {
        setBusy(false);
      }
    },
    [client, companyID, onConfig],
  );

  // Open on the frame's month when a drill or a re-entry carried one.
  useEffect(() => {
    if (frameMonth !== "") {
      setMonthText(frameMonth);
      void load(frameMonth);
    }
  }, [frameMonth, load]);

  // Ctrl+P. THIS SCREEN GENUINELY PRINTS — `printPayrollRegister` is a real
  // layout with the ruleset versions laid out on the page — so the month must
  // travel with the request. It is the LOADED month and not the typed one: a
  // month typed but not fetched has produced nothing on screen, and printing it
  // would put a document on paper that does not match the page it came from.
  usePrintAction({
    voucher: { voucherID: null, payrollMonth: loadedMonth },
  });

  // Alt+E. Writes exactly what is displayed.
  useExportAction(
    useCallback(() => {
      if (denied) {
        return "You do not have permission to read the payroll register, so there is nothing to export.";
      }
      if (data === null) {
        return `Type a wage month first — the register has no rows until it is told which month to cover. ${WAGE_MONTH_HINT}`;
      }
      return registerSheet(data);
    }, [data, denied]),
  );

  // Alt+S — the statutory files panel.
  useRegisterScreenAction(
    "payrollStatutoryFiles",
    useCallback((): string => {
      if (data === null) {
        // Refuses BEFORE opening rather than opening onto nothing: both files
        // are FOR a month, and a panel with no month could only ask for one.
        return "Show a wage month first — the ECR and the ESI return are both produced FOR one month, from the pay run that was posted for it.";
      }
      setFilesOpen(true);
      setFileNotice("");
      return `Statutory files for ${loadedMonth}.`;
    }, [data, loadedMonth]),
  );

  if (denied) {
    return (
      <section
        data-testid="payroll-register-denied"
        className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field"
      >
        <h1 className="text-lg font-bold text-tally-title">Payroll Register</h1>
        <p className="mt-2 max-w-3xl text-sm">
          You do not have permission to read this company&apos;s payroll
          register. It lists every employee&apos;s gross, deductions and net pay,
          so it is refused to the Data Entry level by default — that is a
          decision, not a fault. Each payroll report is its own permission (the
          register, the payslip, and each of the three statutory files), so a
          level can be allowed to produce the monthly ECR and still be refused
          the register. An owner can grant this one by adding an{" "}
          <span className="text-tally-title">
            allow display report:payroll-register
          </span>{" "}
          rule in Users &amp; Roles.
        </p>
      </section>
    );
  }

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">Payroll Register</h1>

      <div className="mt-3 flex flex-wrap items-end gap-2 text-sm">
        <label className="flex flex-col">
          <span className="text-xs text-tally-title">Wage month (YYYY-MM)</span>
          <input
            aria-label="Wage month"
            data-testid="payroll-register-month"
            className="w-32 bg-tally-field px-1 text-tally-fieldText outline-none"
            value={monthText}
            onChange={(e) => setMonthText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void load(monthText.trim());
            }}
          />
        </label>
        <button
          type="button"
          className="border border-tally-fieldText px-2 py-0.5 text-xs"
          onClick={() => void load(monthText.trim())}
        >
          Show the register
        </button>
        {busy ? <span className="text-xs">working…</span> : null}
      </div>

      {error !== "" ? (
        <p
          data-testid="payroll-register-error"
          className="mt-3 max-w-3xl text-sm text-tally-bar"
        >
          {error}
        </p>
      ) : null}

      {data === null && error === "" ? (
        <p
          data-testid="payroll-register-prompt"
          className="mt-3 max-w-3xl text-sm"
        >
          Enter the wage month to see its register. {WAGE_MONTH_HINT} It is not
          defaulted from today&apos;s date: payroll for a month is normally run
          after that month has ended, so &ldquo;this month&rdquo; is usually the
          wrong answer.
        </p>
      ) : null}

      {filesOpen && data !== null ? (
        <StatutoryFilesPanel
          month={loadedMonth}
          notice={fileNotice}
          onClose={() => setFilesOpen(false)}
          onNotice={setFileNotice}
          onECR={() => client.payrollPFECR(companyID, loadedMonth)}
          onESI={(reasons) =>
            client.payrollESIReturn(companyID, loadedMonth, reasons)
          }
        />
      ) : null}

      {data !== null ? (
        <>
          <p className="mt-3 text-xs" data-testid="payroll-register-heading">
            Wage month {data.wage_month} · {data.status} · pay run{" "}
            {data.pay_run_id}
          </p>
          {/* ⛔ THE LAW THAT PRODUCED THESE FIGURES, ON THE REPORT. A payroll
              number with no statement of which ruleset computed it cannot be
              checked by anybody who was not present when it was run — and a
              register is exactly the document somebody checks a year later. */}
          <p className="text-xs" data-testid="payroll-register-rulesets">
            Computed under social-security ruleset{" "}
            <span className="text-tally-title">{data.ss_ruleset_version}</span>{" "}
            and income-tax ruleset{" "}
            <span className="text-tally-title">{data.it_ruleset_version}</span>.
          </p>

          {data.rows.length === 0 ? (
            <p data-testid="payroll-register-empty" className="mt-3 text-sm">
              This pay run has no payslips.
            </p>
          ) : (
            <table
              className="mt-3 w-full text-sm"
              data-testid="payroll-register-table"
            >
              <thead>
                <tr className="border-b border-tally-panel text-left text-xs">
                  <th className="py-1 pr-2">Code</th>
                  <th className="py-1 pr-2">Employee</th>
                  <th className="py-1 pr-2 text-right">Days</th>
                  <th className="py-1 pr-2 text-right">Gross</th>
                  <th className="py-1 pr-2 text-right">EPF (empl.)</th>
                  <th className="py-1 pr-2 text-right">ESI (empl.)</th>
                  <th className="py-1 pr-2 text-right">TDS</th>
                  <th className="py-1 pr-2 text-right">Deductions</th>
                  <th className="py-1 pr-2 text-right">Net</th>
                  <th className="py-1 pr-2 text-right">EPF (employer)</th>
                  <th className="py-1 pr-2 text-right">EPS</th>
                  <th className="py-1 pr-2 text-right">ESI (employer)</th>
                </tr>
              </thead>
              <tbody>
                {data.rows.map((row) => (
                  <tr
                    key={row.employee_id ?? row.employee_code}
                    data-testid={`payroll-register-row:${row.employee_code}`}
                    className={
                      highlighted === row.employee_code
                        ? "bg-tally-panel"
                        : undefined
                    }
                    onClick={() =>
                      onSelect(`${EMPLOYEE_ROW_PREFIX}${row.employee_code}`)
                    }
                  >
                    <td className="py-0.5 pr-2">{row.employee_code}</td>
                    <td className="py-0.5 pr-2">{row.employee_name}</td>
                    <MoneyCells row={row} />
                  </tr>
                ))}
                <tr
                  className="border-t border-tally-panel font-bold"
                  data-testid="payroll-register-totals"
                >
                  <td className="py-0.5 pr-2" />
                  <td className="py-0.5 pr-2">Total</td>
                  <MoneyCells row={data.totals} />
                </tr>
              </tbody>
            </table>
          )}

          <p className="mt-3 max-w-3xl text-xs">
            Enter on a row opens that person&apos;s payslip. The employer columns
            (EPF, EPS, ESI) are the company&apos;s own cost and are NOT deducted
            from anybody — they are shown because they are what the ECR and the
            ESI return will carry.{" "}
            <strong>Alt+S produces those two files</strong> for this month.
          </p>
        </>
      ) : null}
    </section>
  );
}

/** The nine money cells shared by an employee row and the totals row. */
function MoneyCells({ row }: { row: PayrollRegisterRow }) {
  return (
    <>
      {/* Rendered as received, at the storage scale. Nothing here is parsed,
          re-rounded or summed in the browser — the totals row is the SERVER'S. */}
      <td className="py-0.5 pr-2 text-right">{row.days_paid}</td>
      <td className="py-0.5 pr-2 text-right">{formatMoney(row.gross)}</td>
      <td className="py-0.5 pr-2 text-right">{formatMoney(row.epf_employee)}</td>
      <td className="py-0.5 pr-2 text-right">{formatMoney(row.esi_employee)}</td>
      <td className="py-0.5 pr-2 text-right">{formatMoney(row.tds)}</td>
      <td className="py-0.5 pr-2 text-right">{formatMoney(row.deductions)}</td>
      <td className="py-0.5 pr-2 text-right">{formatMoney(row.net)}</td>
      <td className="py-0.5 pr-2 text-right">{formatMoney(row.epf_employer)}</td>
      <td className="py-0.5 pr-2 text-right">{formatMoney(row.eps_employer)}</td>
      <td className="py-0.5 pr-2 text-right">{formatMoney(row.esi_employer)}</td>
    </>
  );
}

/**
 * ALT+S — the two statutory files this month produces.
 *
 * ⛔ THE ESI RETURN ASKS FOR A LEAVING REASON, AND THAT IS NOT A FORM FIELD IT
 * COULD SKIP. The master records a date of leaving and NOT why, and "Retired",
 * "Expired" and "Employee has left service" are three different filings. The
 * server refuses a leaver with no reason — a 422 naming the employee CODE —
 * rather than filing a blank one, which the portal accepts and reads as empty. So
 * the panel takes them, keyed by code, and shows the server's refusal verbatim
 * when one is missing: that message names exactly who needs a reason.
 *
 * ⚠️ FORM 138 IS DELIBERATELY NOT HERE. It is QUARTERLY and needs a TAN, a
 * deductor PAN, a deductor name and every challan's counterfoil, none of which is
 * in the database. A button beside two one-click downloads would imply it takes
 * one click.
 */
function StatutoryFilesPanel({
  month,
  notice,
  onClose,
  onNotice,
  onECR,
  onESI,
}: {
  month: string;
  notice: string;
  onClose: () => void;
  onNotice: (message: string) => void;
  onECR: () => Promise<StatutoryFileDownload>;
  onESI: (
    reasons: Record<string, string>,
  ) => Promise<StatutoryFileDownload>;
}) {
  const [reasonsText, setReasonsText] = useState("");
  const [busy, setBusy] = useState(false);

  const run = useCallback(
    async (what: string, fetcher: () => Promise<StatutoryFileDownload>) => {
      setBusy(true);
      try {
        const dl = await fetcher();
        saveStatutoryArchive(dl);
        // ⛔ THE SERVER'S DISCLOSURE, VERBATIM AND IN FRONT OF THE OPERATOR.
        // Nothing this product emits has been through the FVU, the EPFO portal
        // or the ESIC portal — none of them can run in this environment — and the
        // archive carries a README saying so that the file cannot be saved
        // apart from. Repeating it here is what stops the download reading as a
        // finished filing.
        onNotice(
          `${what} for ${month} downloaded as ${dl.filename ?? "an archive"}. ` +
            `Inside it: ${dl.statutoryFile ?? "the statutory file"} and a README. ` +
            `${dl.disclosure ?? "It has not been through the portal's own validator."} ` +
            `Do not treat this as a filed return.`,
        );
      } catch (e: unknown) {
        // Verbatim. The server's refusals here NAME the employee and the field —
        // "employee E-0007 has no UAN", "employee E-0012 left and no leaving
        // reason was supplied" — and those sentences are the entire remedy.
        onNotice(
          `${what} for ${month} was refused: ${e instanceof Error ? e.message : String(e)}`,
        );
      } finally {
        setBusy(false);
      }
    },
    [month, onNotice],
  );

  return (
    <div
      data-testid="statutory-files-panel"
      className="mt-3 max-w-3xl border border-tally-fieldText p-3 text-sm"
    >
      <h2 className="text-sm font-bold text-tally-title">
        Statutory files for {month}
      </h2>
      <p className="mt-1 text-xs">
        ⛔ Nothing here has been through the EPFO or ESIC portal — neither can be
        reached from this deployment — so each download is an archive containing
        the file AND a README stating exactly that. They travel together on
        purpose: none of these formats permits a comment inside the file, so the
        disclosure can only sit beside it, and a disclosure the download does not
        carry is one nobody reads.
      </p>

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <button
          type="button"
          data-testid="download-ecr"
          className="border border-tally-fieldText px-2 py-0.5 text-xs"
          disabled={busy}
          onClick={() => void run("The EPFO ECR", onECR)}
        >
          EPFO ECR (provident fund)
        </button>
        <button
          type="button"
          data-testid="download-esi"
          className="border border-tally-fieldText px-2 py-0.5 text-xs"
          disabled={busy}
          onClick={() =>
            void run("The ESIC return", () => onESI(parseReasons(reasonsText)))
          }
        >
          ESIC monthly return
        </button>
        {busy ? <span className="text-xs">working…</span> : null}
      </div>

      <label className="mt-3 flex flex-col">
        <span className="text-xs text-tally-title">
          Leaving reasons for the ESIC return — one per line, CODE=Reason
        </span>
        <textarea
          aria-label="Leaving reasons"
          data-testid="esi-leaving-reasons"
          rows={3}
          className="bg-tally-field px-1 font-mono text-tally-fieldText outline-none"
          value={reasonsText}
          onChange={(e) => setReasonsText(e.target.value)}
          placeholder="E-0007=Employee has left service"
        />
      </label>
      <p className="mt-1 text-xs">
        Needed only for people who LEFT during this month, and only because the
        reason is not in the books: the master records the date somebody left and
        not why, and &ldquo;Retired&rdquo;, &ldquo;Expired&rdquo; and
        &ldquo;Employee has left service&rdquo; are three different filings. A
        leaver with no reason is refused by name rather than filed blank — the
        portal reads a blank one as accepted-and-empty.
      </p>

      {notice !== "" ? (
        <p
          data-testid="statutory-files-notice"
          className="mt-3 border border-tally-fieldText p-2 text-sm"
        >
          {notice}
        </p>
      ) : null}

      <button
        type="button"
        className="mt-3 border border-tally-fieldText px-2 py-0.5 text-xs"
        onClick={onClose}
      >
        Close
      </button>
    </div>
  );
}

/**
 * Parse the leaving-reason box: one `CODE=Reason` per line.
 *
 * Splits on the FIRST '=' only, so a reason may contain one. A line with no '='
 * is DROPPED rather than guessed at — a reason with no employee attached cannot
 * be filed against anybody, and the server's refusal will name whoever is still
 * missing one, which is a better message than anything invented here.
 */
function parseReasons(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const at = line.indexOf("=");
    if (at <= 0) continue;
    const code = line.slice(0, at).trim();
    const reason = line.slice(at + 1).trim();
    if (code !== "" && reason !== "") out[code] = reason;
  }
  return out;
}

/**
 * The Alt+E sheet: the rows on screen, at the storage scale.
 *
 * ⛔ EVERY FIGURE GOES THROUGH `moneyCell`, WHICH FORCES IT TO TEXT. A payroll
 * export opened in a spreadsheet that helpfully re-formats "1800.0000" as a
 * number is a file whose paisa are gone, and it is the file somebody reconciles
 * the bank transfer against.
 */
function registerSheet(data: PayrollRegisterResponse): ExportSheet {
  const row = (r: PayrollRegisterRow, label: string) => [
    textCell(label),
    textCell(r.employee_name ?? ""),
    moneyCell(r.days_paid),
    moneyCell(r.gross),
    moneyCell(r.epf_employee),
    moneyCell(r.esi_employee),
    moneyCell(r.tds),
    moneyCell(r.deductions),
    moneyCell(r.net),
    moneyCell(r.epf_employer),
    moneyCell(r.eps_employer),
    moneyCell(r.esi_employer),
  ];
  return {
    // The MONTH and the RULESETS are in the file name and the first row: a
    // payroll export read under the wrong month, or checked against the wrong
    // year's rates, is exactly the mistake the on-screen heading prevents.
    title: `Payroll-Register-${data.wage_month}`,
    headers: [
      "Code",
      "Employee",
      "Days",
      "Gross",
      "EPF (employee)",
      "ESI (employee)",
      "TDS",
      "Deductions",
      "Net",
      "EPF (employer)",
      "EPS (employer)",
      "ESI (employer)",
    ],
    rows: [
      ...data.rows.map((r) => row(r, r.employee_code ?? "")),
      row(data.totals, "TOTAL"),
      [
        textCell(
          `Computed under social-security ruleset ${data.ss_ruleset_version} and income-tax ruleset ${data.it_ruleset_version}. Wage month ${data.wage_month}.`,
        ),
      ],
    ],
  };
}
