import { useCallback, useEffect, useState } from "react";

import { ApiError } from "../api";
import type {
  PrintTemplateField,
  PrintTemplateRequest,
  PrintTemplateResponse,
} from "../api";
import { useClient, useCompanyContext, useCompanyID } from "../app/CompanyContext";
import {
  PRINT_TEMPLATE_FIELD_LABELS,
  PRINT_TEMPLATE_KINDS,
  printTemplateKindLabel,
} from "./printTemplateSurface";

/**
 * PRINT TEMPLATES — what this company's own documents say (track C).
 *
 * Reached from `Gateway of Tally > Print Templates`, and named on the F11
 * Company Features row that used to promise this.
 *
 * ⛔ THE ENGINE WAS COMPLETE AND HAD NO FRONT DOOR. `printingdb.SaveOverrides` —
 * a real upsert keyed on (company_id, doc_kind), with `updated_at` maintained on
 * both paths so "when did our invoice layout last change?" is answerable from the
 * row — has been implemented AND TESTED since migration 0037. The READ side was
 * wired the whole time too: both document paths and the report letterhead call
 * `LoadOverrides` on every print. Nothing could WRITE one. No route reached the
 * store and `print_template` held ZERO ROWS in the entire database, so changing
 * what a company's own invoice says was not possible from anywhere in the
 * product. That is HANDOFF.md §1.15's rule again: a route table is not a surface.
 *
 * ⛔ THE FORM IS BUILT FROM A GENERATED ARTEFACT, NOT FROM A LIST IN THIS FILE.
 * The eight document kinds do NOT all honour the same fields: `RenderReport`
 * reads three of the seven and ignores the rest, because printing "Authorised
 * Signatory" under a Trial Balance would imply a certification nobody gave. A
 * control offered for a field its renderer ignores is the worst outcome available
 * — the operator types a declaration, the screen accepts it, the row stores it,
 * and no printed statement ever carries it. Nothing errors and nothing logs. So
 * `honours` decides which controls exist, and it is MEASURED: the Go generator
 * renders each kind twice per field and compares the PDF bytes. See
 * `printTemplateSurface.ts`.
 *
 * ⛔ AND IT SHOWS WHAT WILL ACTUALLY PRINT, NOT BLANK BOXES. An absent override
 * is not "this company has no declaration" — it is "the built-in declaration
 * prints", and the built-in is real text a company may be perfectly happy with.
 * Every empty box therefore carries the actual default as its placeholder. A form
 * of empty fields would read as a company that had deleted its own declaration.
 *
 * ⚠️ WHY THERE IS NO LOGO CONTROL, SINCE THE FEATURE ROW USED TO PROMISE ONE.
 * There is no image storage anywhere in this product — no column, no object
 * store, no upload route — so a logo is not one field short of shipping, it is a
 * different feature. The row has been renamed to drop the word and says so, which
 * is the whole point of the FeaturesScreen: shipping four of five and leaving the
 * fifth advertised is the failure that screen exists to prevent.
 */
export function PrintTemplateScreen(): JSX.Element {
  const client = useClient();
  const companyID = useCompanyID();
  const { company } = useCompanyContext();

  // The first kind in the artefact's order, which is the server's own — the two
  // invoice kinds first, because a tax invoice is the document nearly every
  // company issues and the one they ask to change first.
  const [docKind, setDocKind] = useState<string>(
    PRINT_TEMPLATE_KINDS[0]?.doc_kind ?? "tax-invoice",
  );
  const [loaded, setLoaded] = useState<PrintTemplateResponse | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);

  const load = useCallback(
    async (kind: string) => {
      setBusy(true);
      setError(null);
      setSaved(null);
      try {
        const res = await client.getPrintTemplate(companyID, kind);
        setLoaded(res);
        setForm(formFrom(res));
      } catch (e) {
        // ⛔ THE FORM IS CLEARED ON A FAILED LOAD. Leaving the previous kind's
        // values on screen under the new kind's heading invites the operator to
        // press Save and copy one document's declaration onto another.
        setLoaded(null);
        setForm(emptyForm());
        setError(messageOf(e));
      } finally {
        setBusy(false);
      }
    },
    [client, companyID],
  );

  useEffect(() => {
    void load(docKind);
  }, [docKind, load]);

  const save = useCallback(async () => {
    if (!loaded) return;
    setBusy(true);
    setError(null);
    setSaved(null);
    try {
      const res = await client.savePrintTemplate(
        companyID,
        docKind,
        // ⛔ ONLY THE HONOURED FIELDS ARE SENT. The server refuses a field this
        // kind cannot print, with a 400 naming it — correctly, because storing
        // it would be a lie. Sending only what is on the form means a correct
        // screen never meets that refusal, and a screen that drifts from the
        // artefact meets it loudly instead of silently storing dead values.
        requestFrom(form, loaded.honours),
      );
      // ⛔ RENDER THE STORED ROW, NEVER THE REQUEST. `savePrintTemplate`
      // answers with what the database holds after the write. A screen that
      // re-rendered its own form state would be UNABLE to detect a column the
      // server dropped — a resolved promise looks identical either way, which
      // is the trap register row D43 set for every caller of POST /v1/companies.
      setLoaded(res);
      setForm(formFrom(res));
      setSaved("Saved. This is what documents of this kind will print.");
    } catch (e) {
      setError(messageOf(e));
    } finally {
      setBusy(false);
    }
  }, [client, companyID, docKind, form, loaded]);

  const clear = useCallback(async () => {
    setBusy(true);
    setError(null);
    setSaved(null);
    try {
      const res = await client.clearPrintTemplate(companyID, docKind);
      setLoaded(res);
      setForm(formFrom(res));
      setSaved(
        res.cleared === false
          ? "There was no customisation to clear; this document was already printing the built-in layout."
          : "Cleared. This document is back to the built-in layout.",
      );
    } catch (e) {
      setError(messageOf(e));
    } finally {
      setBusy(false);
    }
  }, [client, companyID, docKind]);

  const honours = loaded?.honours ?? [];
  const ignored = PRINT_TEMPLATE_KINDS.find(
    (k) => k.doc_kind === docKind,
  )?.ignores;

  return (
    <div className="print-template-screen" data-testid="print-template-screen">
      <h1>Print Templates</h1>

      <p data-testid="print-template-target">
        These are the documents <strong>{company?.name ?? companyID}</strong>{" "}
        prints. A field left empty prints the built-in default shown beneath it,
        so a company that changes nothing still prints a correct document.
      </p>

      {/*
        ⚠️ AN HONEST DISCLOSURE AN OPERATOR NEEDS, AND IT IS NOT OBVIOUS. A
        printed document is rendered from the books at the moment it is printed,
        so changing the letterhead changes what a REPRINT of an old invoice looks
        like too. That is deliberate rather than overlooked — the alternative,
        pinning every voucher to the layout in force on its date, is a different
        feature and nothing in the schema supports it (print_template holds
        exactly one row per company and document kind). An operator who expects
        April's reprint to carry April's address should know before they save.
      */}
      <p data-testid="print-template-reprint-note">
        A document is laid out when it is printed, not when it was entered — so a
        change here also changes how already-entered documents look when they are
        printed again. There is no per-period layout history.
      </p>

      <label htmlFor="print-template-kind">Document</label>
      <select
        id="print-template-kind"
        value={docKind}
        disabled={busy}
        data-testid="print-template-kind"
        onChange={(ev) => setDocKind(ev.target.value)}
      >
        {PRINT_TEMPLATE_KINDS.map((k) => (
          <option key={k.doc_kind} value={k.doc_kind}>
            {printTemplateKindLabel(k.doc_kind)}
          </option>
        ))}
      </select>

      {loaded !== null && (
        <p data-testid="print-template-state">
          {loaded.customised
            ? "This company has customised this document."
            : "This company has not customised this document — it prints the built-in layout."}
        </p>
      )}

      {loaded !== null && (
        <section className="print-template-form">
          {honours.includes("title") && (
            <TextField
              field="title"
              value={form.title}
              placeholder={
                // ⚠️ THE ONE KIND WHOSE DEFAULT IS NOT A FIXED STRING. A report's
                // built-in title is whichever of the thirty statements is being
                // printed, so there is nothing to show — and a blank placeholder
                // here would read as "this company has no title".
                loaded.default_title_is_the_documents_own_name
                  ? "the report's own name (Trial Balance, Day Book, …)"
                  : loaded.defaults.title
              }
              onChange={(v) => setForm({ ...form, title: v })}
              disabled={busy}
            />
          )}

          {honours.includes("header_lines") && (
            <LinesField
              field="header_lines"
              value={form.header_lines}
              hint="One line per row, printed under the company block. On a report these repeat on every page."
              onChange={(v) => setForm({ ...form, header_lines: v })}
              disabled={busy}
            />
          )}

          {honours.includes("declaration") && (
            <AreaField
              field="declaration"
              value={form.declaration}
              placeholder={loaded.defaults.declaration}
              // ⚠️ THREE KINDS PRINT NO DECLARATION UNLESS GIVEN ONE, which is
              // different from a kind that ignores the field. The empty default
              // is said out loud so an empty box is not read as a missing one.
              hint={
                loaded.defaults.declaration === ""
                  ? "This document prints no declaration unless you give it one."
                  : undefined
              }
              onChange={(v) => setForm({ ...form, declaration: v })}
              disabled={busy}
            />
          )}

          {honours.includes("terms") && (
            <AreaField
              field="terms"
              value={form.terms}
              hint="Printed only when you set it; there is no built-in text."
              onChange={(v) => setForm({ ...form, terms: v })}
              disabled={busy}
            />
          )}

          {honours.includes("signature_label") && (
            <TextField
              field="signature_label"
              value={form.signature_label}
              placeholder={loaded.defaults.signature_label}
              onChange={(v) => setForm({ ...form, signature_label: v })}
              disabled={busy}
            />
          )}

          {honours.includes("footer_lines") && (
            <LinesField
              field="footer_lines"
              value={form.footer_lines}
              hint="One line per row, printed at the foot. On a report these print on the last page only."
              onChange={(v) => setForm({ ...form, footer_lines: v })}
              disabled={busy}
            />
          )}

          {honours.includes("bank_details") && (
            <fieldset data-testid="print-template-field-bank_details">
              <legend>{PRINT_TEMPLATE_FIELD_LABELS.bank_details}</legend>
              {/*
                ⛔ ONE CONTROL OVER TWO COLUMNS, AND THE CHECKBOX IS NOT
                DECORATIVE. Every renderer tests `show_bank_details && bank_lines
                is non-empty`, so lines with the box unticked print NOTHING. An
                operator who typed their account number and saved would have done
                everything the screen asked and got a document with no bank
                block, so the dependency is stated here rather than discovered.
              */}
              <label>
                <input
                  type="checkbox"
                  checked={form.show_bank_details}
                  disabled={busy}
                  data-testid="print-template-show-bank"
                  onChange={(ev) =>
                    setForm({ ...form, show_bank_details: ev.target.checked })
                  }
                />
                Print bank details on this document
              </label>
              <textarea
                value={form.bank_lines}
                disabled={busy}
                data-testid="print-template-input-bank_lines"
                aria-label="Bank detail lines"
                onChange={(ev) =>
                  setForm({ ...form, bank_lines: ev.target.value })
                }
              />
              <p className="print-template-hint">
                One line per row. Nothing prints unless the box above is ticked
                AND there is at least one line.
              </p>
            </fieldset>
          )}

          {/*
            ⛔ THE FIELDS THIS DOCUMENT IGNORES ARE NAMED RATHER THAN SILENTLY
            ABSENT. An operator who set a declaration on the invoice and then
            picked Report would otherwise find the box simply gone, and conclude
            the screen was broken. Saying which fields this document does not
            print, and that the reason is the document rather than a permission,
            is the difference between an answer and a support ticket.
          */}
          {ignored !== undefined && ignored.length > 0 && (
            <p data-testid="print-template-ignored">
              A {printTemplateKindLabel(docKind).toLowerCase()} does not print{" "}
              {ignored
                .map((f) => PRINT_TEMPLATE_FIELD_LABELS[f].toLowerCase())
                .join(", ")}
              , so those are not offered here — a statement carries no
              certification and no bank block, and printing one would imply
              something nobody signed.
            </p>
          )}

          <button
            type="button"
            onClick={() => void save()}
            disabled={busy}
            data-testid="print-template-save"
          >
            {busy ? "Saving…" : "Save this layout"}
          </button>

          {/*
            ⛔ THE WAY BACK. Without this a company that sets a declaration once
            can never return to the built-in default: an all-blank save stores a
            row that still counts as a customisation. A one-way door is not a
            setting. Offered only when there is something to clear, so the button
            never lies about what it will do.
          */}
          {loaded.customised && (
            <button
              type="button"
              onClick={() => void clear()}
              disabled={busy}
              data-testid="print-template-clear"
            >
              Clear this customisation
            </button>
          )}
        </section>
      )}

      {saved !== null && (
        <p className="print-template-saved" data-testid="print-template-saved">
          {saved}
        </p>
      )}

      {error !== null && (
        <p
          className="print-template-error"
          role="alert"
          data-testid="print-template-error"
        >
          {error}
        </p>
      )}
    </div>
  );
}

/**
 * The form's own state, with the line lists held as TEXT.
 *
 * ⚠️ TEXT AND NOT string[], DELIBERATELY. A textarea is the editing surface, and
 * splitting on every keystroke would drop a blank line the operator is still
 * typing into — the caret jumps and a line cannot be inserted between two
 * others. The split happens once, at submit.
 */
interface FormState {
  title: string;
  header_lines: string;
  footer_lines: string;
  declaration: string;
  terms: string;
  signature_label: string;
  show_bank_details: boolean;
  bank_lines: string;
}

function emptyForm(): FormState {
  return {
    title: "",
    header_lines: "",
    footer_lines: "",
    declaration: "",
    terms: "",
    signature_label: "",
    show_bank_details: false,
    bank_lines: "",
  };
}

function formFrom(res: PrintTemplateResponse): FormState {
  const o = res.overrides;
  return {
    title: o.title ?? "",
    header_lines: (o.header_lines ?? []).join("\n"),
    footer_lines: (o.footer_lines ?? []).join("\n"),
    declaration: o.declaration ?? "",
    terms: o.terms ?? "",
    signature_label: o.signature_label ?? "",
    show_bank_details: o.show_bank_details ?? false,
    bank_lines: (o.bank_lines ?? []).join("\n"),
  };
}

/**
 * Split a textarea into stored lines.
 *
 * ⚠️ BLANK LINES ARE DROPPED. A trailing newline is what a textarea has after
 * anybody presses Enter, and storing it would print an empty line on every
 * document. An operator who genuinely wants vertical space has no way to ask for
 * it here, which is the right trade: a stray blank line on every invoice is a
 * defect, and a missing one is a preference.
 */
export function splitTemplateLines(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

/**
 * Build the PUT body from the form, carrying ONLY the honoured fields.
 *
 * Exported so it can be tested without mounting the screen: this function is
 * where a drift between the form and the artefact would become a 400, and it is
 * the one piece of the screen with a genuinely interesting contract.
 */
export function requestFrom(
  form: FormState,
  honours: readonly PrintTemplateField[],
): PrintTemplateRequest {
  const req: PrintTemplateRequest = {};
  const has = (f: PrintTemplateField) => honours.includes(f);
  if (has("title")) req.title = form.title;
  if (has("header_lines")) req.header_lines = splitTemplateLines(form.header_lines);
  if (has("footer_lines")) req.footer_lines = splitTemplateLines(form.footer_lines);
  if (has("declaration")) req.declaration = form.declaration;
  if (has("terms")) req.terms = form.terms;
  if (has("signature_label")) req.signature_label = form.signature_label;
  if (has("bank_details")) {
    req.show_bank_details = form.show_bank_details;
    req.bank_lines = splitTemplateLines(form.bank_lines);
  }
  return req;
}

function TextField({
  field,
  value,
  placeholder,
  onChange,
  disabled,
}: {
  field: PrintTemplateField;
  value: string;
  placeholder?: string;
  onChange: (v: string) => void;
  disabled: boolean;
}): JSX.Element {
  return (
    <div data-testid={`print-template-field-${field}`}>
      <label htmlFor={`print-template-input-${field}`}>
        {PRINT_TEMPLATE_FIELD_LABELS[field]}
      </label>
      <input
        id={`print-template-input-${field}`}
        type="text"
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        data-testid={`print-template-input-${field}`}
        onChange={(ev) => onChange(ev.target.value)}
      />
      {placeholder !== undefined && placeholder !== "" && (
        <p className="print-template-hint" data-testid={`print-template-default-${field}`}>
          Leave empty to print: {placeholder}
        </p>
      )}
    </div>
  );
}

function AreaField({
  field,
  value,
  placeholder,
  hint,
  onChange,
  disabled,
}: {
  field: PrintTemplateField;
  value: string;
  placeholder?: string;
  hint?: string;
  onChange: (v: string) => void;
  disabled: boolean;
}): JSX.Element {
  return (
    <div data-testid={`print-template-field-${field}`}>
      <label htmlFor={`print-template-input-${field}`}>
        {PRINT_TEMPLATE_FIELD_LABELS[field]}
      </label>
      <textarea
        id={`print-template-input-${field}`}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        data-testid={`print-template-input-${field}`}
        onChange={(ev) => onChange(ev.target.value)}
      />
      {placeholder !== undefined && placeholder !== "" && (
        <p className="print-template-hint" data-testid={`print-template-default-${field}`}>
          Leave empty to print: {placeholder}
        </p>
      )}
      {hint !== undefined && <p className="print-template-hint">{hint}</p>}
    </div>
  );
}

function LinesField({
  field,
  value,
  hint,
  onChange,
  disabled,
}: {
  field: PrintTemplateField;
  value: string;
  hint: string;
  onChange: (v: string) => void;
  disabled: boolean;
}): JSX.Element {
  return (
    <div data-testid={`print-template-field-${field}`}>
      <label htmlFor={`print-template-input-${field}`}>
        {PRINT_TEMPLATE_FIELD_LABELS[field]}
      </label>
      <textarea
        id={`print-template-input-${field}`}
        value={value}
        disabled={disabled}
        data-testid={`print-template-input-${field}`}
        onChange={(ev) => onChange(ev.target.value)}
      />
      <p className="print-template-hint">{hint}</p>
    </div>
  );
}

function messageOf(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return String(e);
}
