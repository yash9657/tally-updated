import { useCallback, useEffect, useRef, useState } from "react";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import type { ScreenComponentProps } from "../../app/screenRegistry";
import { ApiError } from "../../api";
import type { Form138Challan } from "../../api";
import { usePrintAction } from "../usePrint";
import { isWageMonth } from "./wageMonth";
import { saveStatutoryArchive } from "./statutoryDownload";

/**
 * FORM 138 (ex-24Q) ANNEXURE I — the quarterly salary-TDS statement.
 *
 * ===========================================================================
 * ⛔ WHY THIS IS A FORM AND NOT A DOWNLOAD BUTTON
 * ===========================================================================
 * NOT ONE of the deductor or challan particulars is in the database. The company
 * master carries no TAN, no deductor category and no challan, so every field
 * below is supplied by the operator and an absent one is a 400 from the server.
 * That is the rule the whole shape exists to keep: NOTHING IS DEFAULTED INTO A
 * FILED RECORD. A screen that guessed at a TAN, or invented a file sequence
 * number, would be putting a false statement on a tax return — and the operator
 * would never see the guess.
 *
 * ⚠️ Q4 IS REFUSED BY THE SERVER, and the refusal is honest rather than
 * temporary: the Q4 / Annexure II record structure is not published, and guessing
 * it would mis-file every employee's whole-year salary detail. The picker below
 * offers Q4 anyway, with the reason on it, because an operator who cannot find
 * their quarter concludes the screen is broken.
 *
 * ===========================================================================
 * THE CHALLAN AMOUNT IS OPTIONAL, AND LEAVING IT BLANK IS THE BETTER ANSWER
 * ===========================================================================
 * Omitted, the server DERIVES the challan total from that month's payslips —
 * what the books say was deducted. A supplied amount that does not equal the
 * deductee sum is REFUSED, and that reconciliation is the entire point of the
 * statement: it is the check that what was deposited matches what was withheld.
 * So the field is offered as a cross-check an operator opts into, not as
 * something they must key.
 */

/** One challan row being keyed. Strings throughout — see `amount`. */
interface ChallanDraft {
  month: string;
  bsr: string;
  date: string;
  serial: string;
  /** Blank = derive from the month's payslips. A decimal STRING otherwise. */
  amount: string;
  bookEntry: boolean;
}

const EMPTY_CHALLAN: ChallanDraft = {
  month: "",
  bsr: "",
  date: "",
  serial: "",
  amount: "",
  bookEntry: false,
};

export function Form138Screen(_props: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();

  const [taxYear, setTaxYear] = useState("");
  const [quarter, setQuarter] = useState("");
  const [tan, setTan] = useState("");
  const [deductorPan, setDeductorPan] = useState("");
  const [deductorName, setDeductorName] = useState("");
  const [fileCreationDate, setFileCreationDate] = useState("");
  const [fileSequence, setFileSequence] = useState("1");
  const [challans, setChallans] = useState<ChallanDraft[]>([{ ...EMPTY_CHALLAN }]);
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);

  const firstFieldRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    firstFieldRef.current?.focus();
  }, []);

  // Ctrl+P — answered by printTarget with a sentence. A statutory file is
  // DOWNLOADED, not printed: it must begin with its FH record, and a PDF of it is
  // a document no portal can accept and no human is meant to read.
  usePrintAction();

  const download = useCallback(async (): Promise<string> => {
    // ⛔ REFUSED HERE, WITH NO REQUEST MADE, for the fields this screen can see
    // are empty. The server refuses each of them too — that is where the
    // authority lives — but a round trip to be told a field is blank is a 400 in
    // somebody's log that the screen already knows the answer to.
    const missing: string[] = [];
    if (taxYear.trim() === "") missing.push("the tax year");
    if (quarter === "") missing.push("the quarter");
    if (tan.trim() === "") missing.push("the TAN");
    if (deductorPan.trim() === "") missing.push("the deductor's PAN");
    if (deductorName.trim() === "") missing.push("the deductor's name");
    if (fileCreationDate.trim() === "") missing.push("the file creation date");
    if (missing.length > 0) {
      const message = `Form 138 needs ${missing.join(", ")}. None of them is in the books — the company master carries no TAN and no challan — and nothing is defaulted into a filed record.`;
      setNotice(message);
      return message;
    }

    const rows: Form138Challan[] = [];
    for (const [i, c] of challans.entries()) {
      const stated =
        c.month.trim() !== "" ||
        c.bsr.trim() !== "" ||
        c.date.trim() !== "" ||
        c.serial.trim() !== "";
      // A wholly blank row is the empty form, not an error: the operator has not
      // started it. A PARTLY filled one IS an error, and is refused by name.
      if (!stated) continue;
      if (!isWageMonth(c.month.trim())) {
        const message = `Challan ${i + 1}: the month must be YYYY-MM — it is the WAGE MONTH the deposit pays for, not the month it was paid in. Tax deducted in March is normally deposited in April.`;
        setNotice(message);
        return message;
      }
      if (c.bsr.trim() === "" || c.date.trim() === "" || c.serial.trim() === "") {
        const message = `Challan ${i + 1} is incomplete. A challan is identified by its BSR code, its date of deposit and its serial number together — the department finds a payment by all three, so a missing one cannot be looked past.`;
        setNotice(message);
        return message;
      }
      const serial = Number(c.serial.trim());
      if (!Number.isInteger(serial) || serial < 0) {
        const message = `Challan ${i + 1}: the serial number must be a whole number.`;
        setNotice(message);
        return message;
      }
      rows.push({
        month: c.month.trim(),
        bsr_code: c.bsr.trim(),
        date_of_deposit: c.date.trim(),
        challan_serial_number: serial,
        // Blank is OMITTED, not zero — omitted means "derive it from the
        // payslips", and zero would be a stated deposit of nothing, which the
        // reconciliation refuses.
        ...(c.amount.trim() === "" ? {} : { total_tds_deposited: c.amount.trim() }),
        ...(c.bookEntry ? { book_entry: true } : {}),
      });
    }

    if (rows.length === 0) {
      // ⛔ THE SERVER REFUSES A STATEMENT WITH NO CHALLAN — "a statement with no
      // challan credits nobody" — and it is right to: Deductee Detail field 5 of
      // the return is a challan reference, so a statement without one names tax
      // that was deducted from people and never traced to a payment. Checked
      // here as well because the operator is looking at an empty challan table
      // and can fix it without a round trip. Found by driving the screen: the
      // form was otherwise complete and the only refusal came back from cored.
      const message =
        "Form 138 needs at least one challan. A statement with no challan credits nobody — Deductee Detail field 5 of the return IS a challan reference, so tax deducted from people would be reported with no payment traced to it. Fill in the wage month, the BSR code, the date of deposit and the serial number of each deposit below.";
      setNotice(message);
      return message;
    }

    setBusy(true);
    try {
      const dl = await client.payrollForm138(companyID, {
        tax_year: taxYear.trim(),
        quarter,
        tan: tan.trim().toUpperCase(),
        deductor_pan: deductorPan.trim().toUpperCase(),
        deductor_name: deductorName.trim(),
        // The only field this screen supplies of its own: the form NUMBER is a
        // fact about what Annexure I is, not a choice.
        form_number: "24Q",
        file_creation_date: fileCreationDate.trim(),
        ...(fileSequence.trim() === "" ? {} : { file_sequence: Number(fileSequence.trim()) }),
        challans: rows,
      });
      saveStatutoryArchive(dl);
      // The server's disclosure, verbatim and in front of the operator.
      const message =
        `Form 138 for ${taxYear.trim()} ${quarter} downloaded as ${dl.filename ?? "an archive"}. ` +
        `Inside it: ${dl.statutoryFile ?? "the statement"} and a README. ` +
        `${dl.disclosure ?? "It has not been through the department's validator."} ` +
        `⛔ This is NOT a filed return — the department's FVU cannot run here, so nothing has checked it.`;
      setNotice(message);
      return message;
    } catch (e: unknown) {
      // Verbatim, always. The server's refusals here are the whole remedy: they
      // name the employee with no PAN, the challan whose stated amount does not
      // equal what was deducted, or the fact that Q4's format is not published.
      const message = `Form 138 was refused: ${e instanceof Error ? e.message : String(e)}`;
      setNotice(
        e instanceof ApiError && e.status === 403
          ? `${message}. Producing this statement is its own permission — it carries every employee's PAN, salary and tax — so it is refused to the Data Entry level by default. Ask an Owner for an "allow display report:payroll-form-138" rule in Users & Roles.`
          : message,
      );
      return message;
    } finally {
      setBusy(false);
    }
  }, [
    challans,
    client,
    companyID,
    deductorName,
    deductorPan,
    fileCreationDate,
    fileSequence,
    quarter,
    tan,
    taxYear,
  ]);

  useRegisterScreenAction("accept", download);

  const patchChallan = (i: number, patch: Partial<ChallanDraft>) =>
    setChallans((prev) =>
      prev.map((c, at) => (at === i ? { ...c, ...patch } : c)),
    );

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">
        Form 138 — Salary TDS (Annexure I)
      </h1>
      <p className="mt-1 max-w-3xl text-xs">
        The quarterly statement of tax deducted from salaries. ⛔ Every
        particular below is asked for because NONE of them is in the books — the
        company master carries no TAN and no challan — and nothing is defaulted
        into a filed record. Ctrl+A builds the file and downloads it.
      </p>

      <h2 className="mt-4 text-sm font-bold text-tally-title">The deductor</h2>
      <div className="mt-2 grid max-w-4xl grid-cols-2 gap-2 text-sm md:grid-cols-4">
        <Field
          label="Tax year (2026-27)"
          testid="form138-tax-year"
          value={taxYear}
          onChange={setTaxYear}
          inputRef={firstFieldRef}
        />
        <label className="flex flex-col">
          <span className="text-xs text-tally-title">Quarter</span>
          <select
            aria-label="Quarter"
            data-testid="form138-quarter"
            className="bg-tally-field px-1 text-tally-fieldText outline-none"
            value={quarter}
            onChange={(e) => setQuarter(e.target.value)}
          >
            <option value="">— choose —</option>
            <option value="Q1">Q1 (Apr–Jun)</option>
            <option value="Q2">Q2 (Jul–Sep)</option>
            <option value="Q3">Q3 (Oct–Dec)</option>
            {/* ⛔ OFFERED, AND IT WILL BE REFUSED. Hiding it would leave an
                operator hunting for their quarter and concluding the screen is
                broken; the server's own refusal names the reason, and it is a
                real one rather than a deferral: the Annexure II record structure
                is not published. */}
            <option value="Q4">Q4 (Jan–Mar) — refused, see below</option>
          </select>
        </label>
        <Field label="TAN" testid="form138-tan" value={tan} onChange={setTan} />
        <Field
          label="Deductor's PAN"
          testid="form138-deductor-pan"
          value={deductorPan}
          onChange={setDeductorPan}
        />
        <Field
          label="Deductor's name"
          testid="form138-deductor-name"
          value={deductorName}
          onChange={setDeductorName}
        />
        <Field
          label="File creation date (YYYY-MM-DD)"
          testid="form138-file-date"
          value={fileCreationDate}
          onChange={setFileCreationDate}
        />
        <Field
          label="File sequence number"
          testid="form138-file-sequence"
          value={fileSequence}
          onChange={setFileSequence}
        />
      </div>
      <p className="mt-1 max-w-3xl text-xs">
        The file sequence number must be unique across every file this deductor
        has ever sent — it is how the department tells a re-submission from a new
        statement, and only you know what you have already sent. Q4 is refused by
        the server: its record structure (Annexure II, the whole-year salary
        detail) is not published, and guessing it would mis-file every employee.
      </p>

      <h2 className="mt-5 text-sm font-bold text-tally-title">The challans</h2>
      <p className="mt-1 max-w-3xl text-xs">
        <strong>At least one is required</strong> — a statement with no challan
        credits nobody, because Deductee Detail field 5 of the return IS a
        challan reference. One row per deposit. The MONTH is the wage month the
        deposit pays for,
        not the month it was paid in — tax deducted in March is normally
        deposited in April. <strong>Leave the amount blank</strong> and the
        server derives it from that month&apos;s payslips, which is what the
        books say was deducted; state one and it is REFUSED unless it matches
        exactly, which is the reconciliation this statement exists to make.
      </p>
      <table className="mt-2 w-full max-w-5xl text-sm" data-testid="form138-challans">
        <thead>
          <tr className="border-b border-tally-panel text-left text-xs">
            <th className="py-1 pr-2">Wage month</th>
            <th className="py-1 pr-2">BSR code</th>
            <th className="py-1 pr-2">Date of deposit</th>
            <th className="py-1 pr-2">Serial</th>
            <th className="py-1 pr-2">Amount (optional)</th>
            <th className="py-1 pr-2">Book entry</th>
          </tr>
        </thead>
        <tbody>
          {challans.map((c, i) => (
            <tr key={i}>
              <Cell testid={`challan-month-${i}`} label={`Challan ${i + 1} wage month`} value={c.month} onChange={(v) => patchChallan(i, { month: v })} />
              <Cell testid={`challan-bsr-${i}`} label={`Challan ${i + 1} BSR code`} value={c.bsr} onChange={(v) => patchChallan(i, { bsr: v })} />
              <Cell testid={`challan-date-${i}`} label={`Challan ${i + 1} date of deposit`} value={c.date} onChange={(v) => patchChallan(i, { date: v })} />
              <Cell testid={`challan-serial-${i}`} label={`Challan ${i + 1} serial`} value={c.serial} onChange={(v) => patchChallan(i, { serial: v })} />
              <Cell testid={`challan-amount-${i}`} label={`Challan ${i + 1} amount`} value={c.amount} onChange={(v) => patchChallan(i, { amount: v })} />
              <td className="py-0.5 pr-2">
                <input
                  type="checkbox"
                  aria-label={`Challan ${i + 1} is a book entry`}
                  data-testid={`challan-book-${i}`}
                  checked={c.bookEntry}
                  onChange={(e) => patchChallan(i, { bookEntry: e.target.checked })}
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <button
        type="button"
        className="mt-2 border border-tally-fieldText px-2 py-0.5 text-xs"
        onClick={() => setChallans((prev) => [...prev, { ...EMPTY_CHALLAN }])}
      >
        Add another challan
      </button>
      <p className="mt-1 text-xs">
        A &ldquo;book entry&rdquo; is a government transfer rather than a bank
        deposit — tick it only for one, because the department reconciles the two
        against different records.
      </p>

      <div className="mt-4 flex gap-2">
        <button
          type="button"
          data-testid="form138-download"
          className="border border-tally-fieldText px-2 py-0.5 text-xs"
          disabled={busy}
          onClick={() => void download()}
        >
          Build and download (Ctrl+A)
        </button>
        {busy ? <span className="text-xs">working…</span> : null}
      </div>

      {notice !== "" ? (
        <p
          data-testid="form138-notice"
          className="mt-3 max-w-3xl border border-tally-fieldText p-2 text-sm"
        >
          {notice}
        </p>
      ) : null}

      <p className="mt-4 max-w-3xl text-xs">
        ⛔ Nothing this screen produces has been through the department&apos;s
        File Validation Utility — it needs a Java runtime this deployment does
        not have — so the download is an archive holding the statement AND a
        README saying exactly that. They travel together because the statement
        must begin with its FH record and cannot carry a comment of its own, and
        a disclosure the download does not carry is one nobody reads.
      </p>
    </section>
  );
}

function Field({
  label,
  testid,
  value,
  onChange,
  inputRef,
}: {
  label: string;
  testid: string;
  value: string;
  onChange: (v: string) => void;
  inputRef?: React.RefObject<HTMLInputElement>;
}) {
  return (
    <label className="flex flex-col">
      <span className="text-xs text-tally-title">{label}</span>
      <input
        ref={inputRef}
        aria-label={label}
        data-testid={testid}
        className="bg-tally-field px-1 text-tally-fieldText outline-none"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  );
}

function Cell({
  label,
  testid,
  value,
  onChange,
}: {
  label: string;
  testid: string;
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <td className="py-0.5 pr-2">
      <input
        aria-label={label}
        data-testid={testid}
        className="w-full bg-tally-field px-1 text-tally-fieldText outline-none"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </td>
  );
}
