import { useCallback, useMemo, useRef, useState } from "react";
import { useClient, useCompanyID } from "../app/CompanyContext";
import { ApiError } from "../api";
import type {
  ImportedStatementResponse,
  ImportStatementResponse,
  PreviewStatementResponse,
  StatementColumnMapping,
  StatementDirectionEncoding,
} from "../api";
import { formatMoney } from "./reports/money";

/**
 * BANK STATEMENT IMPORT (T4 slice 2a) — the panel Alt+O opens on the Bank
 * Reconciliation screen.
 *
 * Design memo: docs/design/t4-slice2-model.md §8. Key: RESEARCH.md:361 ("O:
 * Import"), :415, :1221.
 *
 * WHY THIS IS A THREE-STEP FLOW AND NOT A FILE PICKER
 * --------------------------------------------------
 * There are NO built-in per-bank column layouts in this build, and that is a
 * decision rather than a shortcut (memo §8.2). HDFC, ICICI and SBI all differ,
 * no real export exists in this repository to check a layout against, and a
 * layout wrong by one column reads the RUNNING BALANCE as the amount — at which
 * point every figure on screen is a real number off a real statement that was
 * never a transaction. Nothing raises. Nobody notices.
 *
 * So: choose the file, CONFIRM how it is read, then import. The middle step is
 * the feature.
 *
 * WHAT THIS PANEL WILL NOT DO
 * ---------------------------
 *  - It does not compute money. Amounts are fixed-4 decimal strings rendered by
 *    formatMoney, never parsed into a JS number.
 *  - It does not pre-confirm past `unresolved`. Each entry is a question only the
 *    operator can answer, and the commonest — an ambiguous dd/mm vs mm/dd date
 *    column — moves a whole statement by up to eleven months while every amount
 *    stays plausible. The Import button stays disabled until they are answered.
 *  - It does not say a statement row is matched, cleared or reconciled. Matching
 *    is slice 2b. The server's `disclosures` say so and this panel renders them.
 */

/** The fields an operator maps, in the order the form shows them. */
const MAPPABLE_FIELDS: readonly {
  key: keyof StatementColumnMapping;
  label: string;
  /** Which encodings need this column; empty means "always shown". */
  onlyFor?: readonly StatementDirectionEncoding[];
  required?: boolean;
  hint?: string;
}[] = [
  { key: "value_date", label: "Value date", required: true, hint: "The bank's own date. This is what becomes the Bank Date on a match." },
  { key: "txn_date", label: "Transaction date", hint: "Kept separately from the value date, never merged — a cheque can be presented one day and credited another." },
  { key: "description", label: "Description / narration", required: true },
  { key: "deposit", label: "Deposit amount", onlyFor: ["two-columns"], required: true },
  { key: "withdrawal", label: "Withdrawal amount", onlyFor: ["two-columns"], required: true },
  { key: "amount", label: "Amount", onlyFor: ["signed-amount", "amount-and-flag"], required: true },
  { key: "direction", label: "Credit / debit flag", onlyFor: ["amount-and-flag"], required: true, hint: "The column holding C or D, in the bank's own wording." },
  { key: "running_balance", label: "Running balance", hint: "Only used to CHECK the file: it is how a dropped or duplicated row is detected. Without it, key the statement's opening and closing balance below." },
  { key: "balance_sign", label: "Balance Cr / Dr column", hint: "Only for files that print the balance unsigned beside its own Cr/Dr. Dr there means overdrawn." },
  { key: "bank_reference", label: "Bank reference / UTR" },
  { key: "cheque_number", label: "Cheque number" },
];

const ENCODING_LABELS: Record<StatementDirectionEncoding, string> = {
  "two-columns": "Two columns — separate Deposit and Withdrawal",
  "signed-amount": "One amount column, sign carries the direction",
  "amount-and-flag": "One amount column plus a C/D flag column",
};

export interface BankStatementImportPanelProps {
  ledgerID: string;
  ledgerName: string;
  /** "" when NOT RECORDED — the panel says so rather than showing a blank. */
  bankAccountNumber: string;
  bankIFSC: string;
  onClose: () => void;
}

export function BankStatementImportPanel({
  ledgerID,
  ledgerName,
  bankAccountNumber,
  bankIFSC,
  onClose,
}: BankStatementImportPanelProps) {
  const client = useClient();
  const companyID = useCompanyID();

  const [fileName, setFileName] = useState("");
  const [content, setContent] = useState("");
  const [preview, setPreview] = useState<PreviewStatementResponse | null>(null);
  const [mapping, setMapping] = useState<StatementColumnMapping | null>(null);
  const [statementAccount, setStatementAccount] = useState("");
  // W5 — the balances printed on the statement. Strings, never numbers: they go
  // to the server as decimal strings and the proof is computed there.
  const [declaredOpening, setDeclaredOpening] = useState("");
  const [declaredClosing, setDeclaredClosing] = useState("");
  const [result, setResult] = useState<ImportStatementResponse | null>(null);
  const [imported, setImported] = useState<ImportedStatementResponse | null>(
    null,
  );
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const fail = useCallback((err: unknown) => {
    // The server NAMES every refusal — the row, the column, the account. That
    // message is more useful than anything this panel could invent, so it is
    // shown as it is.
    setError(
      err instanceof ApiError
        ? err.message
        : err instanceof Error
          ? err.message
          : String(err),
    );
    setBusy(false);
  }, []);

  const runPreview = useCallback(
    (name: string, text: string) => {
      setBusy(true);
      setError(null);
      setResult(null);
      client
        .previewBankStatement(companyID, ledgerID, {
          file_name: name,
          content: text,
        })
        .then((res) => {
          setPreview(res);
          setMapping(res.mapping);
          setBusy(false);
        })
        .catch(fail);
    },
    [client, companyID, fail, ledgerID],
  );

  const onFile = useCallback(
    async (file: File) => {
      const text = await file.text();
      setFileName(file.name);
      setContent(text);
      runPreview(file.name, text);
    },
    [runPreview],
  );

  /**
   * Which required columns are still unmapped, for THIS encoding.
   *
   * Computed here as well as server-side on purpose: the server is the
   * authority and refuses regardless, but a disabled button with a named reason
   * beats a round trip that comes back "a required column is not mapped".
   */
  const missing = useMemo(() => {
    if (!mapping) return [];
    const out: string[] = [];
    if (!mapping.encoding) out.push("how the file says which way money moved");
    if (!mapping.date_layout) out.push("the date layout");
    for (const f of MAPPABLE_FIELDS) {
      if (!f.required) continue;
      if (f.onlyFor && !f.onlyFor.includes(mapping.encoding as StatementDirectionEncoding)) {
        continue;
      }
      if (!mapping[f.key]) out.push(f.label.toLowerCase());
    }
    return out;
  }, [mapping]);

  const runImport = useCallback(() => {
    if (!mapping) return;
    setBusy(true);
    setError(null);
    client
      .importBankStatement(companyID, ledgerID, {
        file_name: fileName,
        content,
        mapping,
        statement_account_number: statementAccount || undefined,
        declared_opening_balance: declaredOpening.trim() || undefined,
        declared_closing_balance: declaredClosing.trim() || undefined,
        created_by: "ui",
      })
      .then((res) => {
        setResult(res);
        setBusy(false);
        return client.importedStatement(companyID, ledgerID);
      })
      .then((st) => setImported(st ?? null))
      .catch(fail);
  }, [client, companyID, content, declaredClosing, declaredOpening, fail, fileName, ledgerID, mapping, statementAccount]);

  const showImported = useCallback(() => {
    setBusy(true);
    setError(null);
    client
      .importedStatement(companyID, ledgerID)
      .then((st) => {
        setImported(st);
        setBusy(false);
      })
      .catch(fail);
  }, [client, companyID, fail, ledgerID]);

  const setField = (key: keyof StatementColumnMapping, value: string) =>
    setMapping((m) => (m ? { ...m, [key]: value } : m));

  return (
    <section
      data-testid="stmt-import-panel"
      className="flex h-full flex-col overflow-auto bg-tally-bg p-4 text-tally-field"
    >
      <header className="mb-3">
        <h1 className="text-lg font-bold text-tally-title">
          Import Bank Statement — {ledgerName}
        </h1>
        {/* I-11: the account identity, or the WORDS "not recorded". A blank
            labelled field reads as "this bank has no IFSC". */}
        <p className="text-xs" data-testid="stmt-account-identity">
          {bankAccountNumber || bankIFSC ? (
            <>
              A/c {bankAccountNumber || "not recorded"} · IFSC{" "}
              {bankIFSC || "not recorded"}
            </>
          ) : (
            <>
              This account has <strong>no account number or IFSC recorded</strong>
              , so a file cannot be checked against it. Record them on the ledger
              to have future imports verified.
            </>
          )}
          {busy ? " · working…" : ""}
        </p>
      </header>

      {error ? (
        <p
          data-testid="stmt-error"
          className="mb-3 border border-tally-active bg-tally-panel p-2 text-sm"
        >
          {error}
        </p>
      ) : null}

      {/* ---- Step 1: the file. */}
      <div className="mb-4 max-w-3xl bg-tally-panel p-3">
        <label className="block text-xs font-bold">
          1 — CHOOSE THE BANK&apos;S CSV FILE
        </label>
        <input
          ref={fileInput}
          data-testid="stmt-file"
          type="file"
          accept=".csv,text/csv"
          className="mt-2 text-sm"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) void onFile(file);
          }}
        />
        <p className="mt-2 text-xs">
          CSV only in this build. MT940 and XLSX are refused by name rather than
          read on a best-effort basis — a half-understood format would fill in
          plausible values and reconcile to nothing.
        </p>
        {/* The paste path exists because a bank portal that will not download,
            or a file on another machine, is an ordinary Tuesday. */}
        <details className="mt-2 text-xs">
          <summary className="cursor-pointer">or paste the file</summary>
          <textarea
            data-testid="stmt-paste"
            className="mt-2 h-24 w-full bg-tally-bg p-1 font-mono text-xs"
            value={content}
            onChange={(e) => setContent(e.target.value)}
            onBlur={() => {
              if (content.trim()) {
                // ⛔ NO INVENTED NAME HERE ANY MORE (D111). This call used to
                // send "pasted.csv" while the IMPORT ninety-nine lines up sent
                // the bare (empty) name — one of the two calls that needed a
                // fallback got one, which is how every pasted statement came to
                // be stored nameless. The fallback now lives in ONE place, the
                // server, where the file's own period and row count are known
                // and can tell two pastes apart; a constant here could not.
                runPreview(fileName, content);
              }
            }}
          />
        </details>
      </div>

      {/* ---- Step 2: confirm the reading. THE STEP THAT MATTERS. */}
      {preview && mapping ? (
        <div className="mb-4 max-w-3xl bg-tally-panel p-3">
          <label className="block text-xs font-bold">
            2 — CONFIRM HOW THIS FILE IS READ
          </label>
          <p className="mt-1 text-xs">
            This build has no built-in layout for any bank. The suggestions below
            come from <strong>this file&apos;s own header row</strong>; a layout
            remembered instead would silently read the wrong column.
          </p>

          {preview.unresolved.length > 0 ? (
            <ul
              data-testid="stmt-unresolved"
              className="mt-2 border border-tally-active p-2 text-xs"
            >
              {preview.unresolved.map((u) => (
                <li key={u} className="mb-1">
                  ⚠ {u}
                </li>
              ))}
            </ul>
          ) : null}

          {preview.notes.length > 0 ? (
            <ul data-testid="stmt-notes" className="mt-2 text-xs">
              {preview.notes.map((n) => (
                <li key={n}>· {n}</li>
              ))}
            </ul>
          ) : null}

          <div className="mt-3 grid grid-cols-2 gap-2 text-xs">
            <label className="flex flex-col">
              <span className="font-bold">Direction encoding</span>
              <select
                data-testid="stmt-encoding"
                className="mt-1 bg-tally-bg p-1"
                value={mapping.encoding}
                onChange={(e) => setField("encoding", e.target.value)}
              >
                <option value="">— choose —</option>
                {preview.known_encodings.map((enc) => (
                  <option key={enc} value={enc}>
                    {ENCODING_LABELS[enc] ?? enc}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col">
              <span className="font-bold">Date layout</span>
              <select
                data-testid="stmt-date-layout"
                className="mt-1 bg-tally-bg p-1"
                value={mapping.date_layout}
                onChange={(e) => setField("date_layout", e.target.value)}
              >
                <option value="">— choose —</option>
                {preview.known_date_layouts.map((l) => (
                  <option key={l} value={l}>
                    {l}
                  </option>
                ))}
              </select>
            </label>

            {MAPPABLE_FIELDS.filter(
              (f) =>
                !f.onlyFor ||
                f.onlyFor.includes(mapping.encoding as StatementDirectionEncoding),
            ).map((f) => (
              <label key={f.key} className="flex flex-col">
                <span className={f.required ? "font-bold" : ""}>
                  {f.label}
                  {f.required ? " *" : ""}
                </span>
                <select
                  data-testid={`stmt-map-${f.key}`}
                  className="mt-1 bg-tally-bg p-1"
                  value={String(mapping[f.key] ?? "")}
                  onChange={(e) => setField(f.key, e.target.value)}
                >
                  <option value="">— none —</option>
                  {preview.header.map((h) => (
                    <option key={h} value={h}>
                      {h}
                    </option>
                  ))}
                </select>
                {f.hint ? (
                  <span className="mt-0.5 text-[10px] leading-tight opacity-80">
                    {f.hint}
                  </span>
                ) : null}
              </label>
            ))}

            <label className="flex flex-col">
              <span>Account number printed on the statement</span>
              <input
                data-testid="stmt-account-number"
                className="mt-1 bg-tally-bg p-1"
                value={statementAccount}
                onChange={(e) => setStatementAccount(e.target.value)}
              />
              <span className="mt-0.5 text-[10px] leading-tight opacity-80">
                Optional, and worth typing: if it disagrees with this
                ledger&apos;s recorded number the import is REFUSED. A statement
                imported against the wrong account reconciles nothing and leaves
                two accounts wrong.
              </span>
            </label>

            <label className="flex flex-col">
              <span>Opening balance printed on the statement</span>
              <input
                data-testid="stmt-declared-opening"
                inputMode="decimal"
                className="mt-1 bg-tally-bg p-1"
                value={declaredOpening}
                onChange={(e) => setDeclaredOpening(e.target.value)}
              />
            </label>
            <label className="flex flex-col">
              <span>Closing balance printed on the statement</span>
              <input
                data-testid="stmt-declared-closing"
                inputMode="decimal"
                className="mt-1 bg-tally-bg p-1"
                value={declaredClosing}
                onChange={(e) => setDeclaredClosing(e.target.value)}
              />
              <span className="mt-0.5 text-[10px] leading-tight opacity-80">
                Required when the file has no running balance: the import is
                REFUSED unless opening + deposits − withdrawals = closing can be
                checked. If the file prints them too, they must agree.
              </span>
            </label>
          </div>

          {missing.length > 0 ? (
            <p data-testid="stmt-missing" className="mt-3 text-xs">
              Still needed: {missing.join(", ")}.
            </p>
          ) : null}

          <button
            data-testid="stmt-import"
            type="button"
            disabled={busy || missing.length > 0}
            className="mt-3 bg-tally-active px-3 py-1 text-sm font-bold text-tally-bg disabled:opacity-50"
            onClick={runImport}
          >
            Import
          </button>
        </div>
      ) : null}

      {/* ---- Step 3: what happened. */}
      {result ? (
        <div
          data-testid="stmt-result"
          className="mb-4 max-w-3xl bg-tally-panel p-3 text-sm"
        >
          <p className="font-bold">
            {result.already_imported
              ? "This file has already been imported into this account — nothing was added."
              : `Imported ${result.new_lines} of ${result.lines_in_file} rows.`}
          </p>
          {result.duplicate_lines > 0 && !result.already_imported ? (
            <p className="mt-1 text-xs">
              {result.duplicate_lines} row
              {result.duplicate_lines === 1 ? " was" : "s were"} already on this
              account from an earlier statement and{" "}
              {result.duplicate_lines === 1 ? "was" : "were"} not added again.
            </p>
          ) : null}
          <p className="mt-1 text-xs">
            {result.period_from} to {result.period_to}
          </p>
          {/* ⛔ THE NAME IT WAS STORED UNDER, AND IT IS NOT ALWAYS THE ONE SENT
              (D111). A statement PASTED rather than chosen has no file name, so
              the server derives one from the file's own period, row count and
              fingerprint. Shown here because the disclosures on the combined
              import report QUOTE this string — an operator who has pasted twice
              cannot otherwise tell which of the two a warning is about. */}
          <p className="mt-1 text-xs" data-testid="stmt-stored-name">
            Stored as <strong>{result.file_name}</strong>
            {fileName.trim() === "" ? (
              <>
                {" "}
                — this statement was pasted, so it had no file name of its own
                and one was made from its contents.
              </>
            ) : null}
          </p>

          {/* ⚠️ A CHECK THAT DID NOT RUN MUST NOT LOOK LIKE A CHECK THAT
              PASSED. This is the difference between "your file is verified" and
              "nobody looked". */}
          <p
            data-testid="stmt-walk"
            className={
              result.balance_walked
                ? "mt-2 text-xs"
                : "mt-2 border border-tally-active p-1 text-xs font-bold"
            }
          >
            {result.balance_walked
              ? `Balance check PASSED: the rows walk from ${formatMoney(result.opening_balance)} to ${formatMoney(result.closing_balance)} to the paisa, so no row is missing or duplicated.`
              : result.balance_proof === "declared"
                ? `Balance PROVED by totals, not row by row: ${formatMoney(result.opening_balance)} + deposits − withdrawals = ${formatMoney(result.closing_balance)}. One missing or duplicated row would have been refused; two errors of equal and opposite amount would not.`
                : "Balance check DID NOT RUN — this file carries no running balance. A row dropped or duplicated by the export would NOT have been detected."}
          </p>

          {result.disclosures.length > 0 ? (
            <ul data-testid="stmt-disclosures" className="mt-2 text-xs">
              {result.disclosures.map((d) => (
                <li key={d} className="mb-1">
                  · {d}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      {/* ---- What the bank said, as stored. */}
      <div className="max-w-5xl">
        <button
          data-testid="stmt-show-imported"
          type="button"
          className="mb-2 text-xs underline"
          onClick={showImported}
        >
          Show everything imported for this account
        </button>

        {imported ? (
          <>
            {imported.disclosures.length > 0 ? (
              <ul
                data-testid="stmt-report-disclosures"
                className="mb-2 border border-tally-active bg-tally-panel p-2 text-xs"
              >
                {imported.disclosures.map((d) => (
                  <li key={d} className="mb-1">
                    · {d}
                  </li>
                ))}
              </ul>
            ) : null}

            {imported.lines.length === 0 ? (
              /* An explicit state: "nothing imported" must be distinguishable
                 from "the panel is broken". */
              <p data-testid="stmt-none" className="text-sm">
                Nothing has been imported for this account yet.
              </p>
            ) : (
              <table className="w-full text-xs" data-testid="stmt-lines">
                <thead>
                  <tr className="border-b border-tally-active text-left">
                    <th className="py-1 pr-2">Value date</th>
                    <th className="py-1 pr-2">Description</th>
                    <th className="py-1 pr-2">Cheque / ref</th>
                    {/* IN and OUT rather than Dr and Cr. The statement's own
                        wording is the opposite of our dr_cr — the bank calls a
                        deposit a credit because in ITS books we are a liability
                        — so labelling these columns Dr/Cr would look wrong to
                        every operator holding the paper statement. */}
                    <th className="py-1 pr-2 text-right">In</th>
                    <th className="py-1 pr-2 text-right">Out</th>
                    <th className="py-1 text-right">Balance</th>
                  </tr>
                </thead>
                <tbody>
                  {imported.lines.map((l) => (
                    <tr key={l.id} className="border-b border-tally-panel">
                      <td className="py-0.5 pr-2 font-mono">{l.value_date}</td>
                      <td className="py-0.5 pr-2">{l.description}</td>
                      <td className="py-0.5 pr-2 font-mono">
                        {l.cheque_number || l.bank_reference}
                      </td>
                      <td className="py-0.5 pr-2 text-right font-mono">
                        {l.dr_cr === "D" ? formatMoney(l.amount) : ""}
                      </td>
                      <td className="py-0.5 pr-2 text-right font-mono">
                        {l.dr_cr === "C" ? formatMoney(l.amount) : ""}
                      </td>
                      <td className="py-0.5 text-right font-mono">
                        {/* Absent, not zero, when the file carried none. */}
                        {l.running_balance
                          ? formatMoney(l.running_balance)
                          : "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr className="font-bold">
                    <td className="py-1" colSpan={3}>
                      Total
                    </td>
                    <td className="py-1 pr-2 text-right font-mono">
                      {formatMoney(imported.total_in)}
                    </td>
                    <td className="py-1 pr-2 text-right font-mono">
                      {formatMoney(imported.total_out)}
                    </td>
                    <td />
                  </tr>
                </tfoot>
              </table>
            )}
          </>
        ) : null}
      </div>

      <p className="mt-4 text-xs">
        Esc goes back to the reconciliation statement.{" "}
        <button type="button" className="underline" onClick={onClose}>
          Close
        </button>
      </p>
    </section>
  );
}
