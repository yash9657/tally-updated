import type { PrintTemplateField } from "../api/types";
// GENERATED, NOT AUTHORED — see the block comment below. internal/printing's
// TestPrintTemplateSurfaceGolden emits this by RENDERING each document kind
// twice per field and comparing the PDF bytes, so the frontend cannot hold a
// stale opinion about which controls do anything.
import surface from "../api/goldens/print-template-surface.json";

/**
 * WHICH PRINT-TEMPLATE CONTROLS DO ANYTHING, PER DOCUMENT KIND — the pure
 * resolution, with no client and no React.
 *
 * ⛔ WHY THIS IS READ FROM A GOLDEN AND NOT TYPED HERE. `TemplateOverrides` has
 * seven overridable fields and there are eight document kinds, and the grid is
 * RAGGED: `RenderReport` reads three of the seven and ignores the rest, because
 * printing "Authorised Signatory" under a Trial Balance would imply a
 * certification nobody gave. A screen that held its own copy of that grid would
 * start drifting from the renderers the day after it shipped, and the failure is
 * silent in the worst direction — an operator types a declaration for the
 * `report` kind, the form accepts it, the row is stored, and no printed statement
 * ever shows it. Nothing errors and nothing logs.
 *
 * ✅ AND THE GENERATOR CANNOT SKIP, which is what makes this artefact stronger
 * than `printed-report-layouts.json`. That one needs Postgres to decide whether a
 * report prints, and a skipped Go test is indistinguishable from a passing one in
 * the exit code. Deciding whether a FIELD prints needs two renders and a byte
 * comparison, so this file is regenerated on every run of the Go suite on any
 * machine.
 *
 * ⚠️ THE COUNTERWEIGHT IS STILL WORTH HAVING and lives in
 * `printTemplateSurface.test.ts`: it asserts this artefact's SHAPE — eight kinds,
 * every kind honouring at least one field, at least one kind ignoring something —
 * in a suite that always runs. A golden that went empty or became uniform would
 * otherwise turn every consumer below into a silent no-op.
 */

/** One kind's row in the generated artefact. */
export interface PrintTemplateKindSurface {
  doc_kind: string;
  honours: PrintTemplateField[];
  ignores: PrintTemplateField[];
  defaults: { title: string; declaration: string; signature_label: string };
  default_title_is_the_documents_own_name: boolean;
}

/**
 * Every document kind this system prints, in the server's own stable order — the
 * two invoice kinds, the five voucher documents, then the statement.
 *
 * ⛔ NOT ALPHABETISED AND NOT RE-SORTED. The order is the declaration order in
 * `printing.AllDocKinds`, which is the order a form should offer them in: a
 * picker that put "bill-of-supply" first would lead with the document most
 * companies never issue.
 */
export const PRINT_TEMPLATE_KINDS: readonly PrintTemplateKindSurface[] =
  surface.kinds as PrintTemplateKindSurface[];

/** Every overridable field, in a stable order suitable for laying out a form top to bottom. */
export const PRINT_TEMPLATE_FIELDS: readonly PrintTemplateField[] =
  surface.fields as PrintTemplateField[];

const BY_KIND: ReadonlyMap<string, PrintTemplateKindSurface> = new Map(
  PRINT_TEMPLATE_KINDS.map((k) => [k.doc_kind, k]),
);

/**
 * The surface for one document kind, or `undefined` for a kind the server does
 * not print.
 *
 * ⚠️ `undefined` RATHER THAN A PERMISSIVE DEFAULT. A caller that mistypes a kind
 * must get a visibly broken form, not one offering all seven controls whose
 * values the server would refuse — or worse, accept and never print.
 */
export function printTemplateSurfaceFor(
  docKind: string,
): PrintTemplateKindSurface | undefined {
  return BY_KIND.get(docKind);
}

/** Whether this kind's renderer reads this field. */
export function printTemplateHonours(
  docKind: string,
  field: PrintTemplateField,
): boolean {
  return printTemplateSurfaceFor(docKind)?.honours.includes(field) ?? false;
}

/**
 * A human label for a document kind, for a picker.
 *
 * ⛔ DERIVED FROM THE SLUG, NEVER A SECOND TABLE. "tax-invoice" becomes "Tax
 * Invoice" by rule, so a document kind added on the server appears in the picker
 * with a reasonable name and no frontend change — rather than falling through a
 * lookup table to `undefined` and rendering an empty row in a list of eight.
 */
export function printTemplateKindLabel(docKind: string): string {
  return docKind
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

/**
 * A human label for an overridable field.
 *
 * These ARE authored, because the field names are a closed set of seven fixed by
 * the schema, and "bank_details" has to read as one control covering two columns.
 * A slug-derived label would render "Signature Label", which is the column name
 * rather than what an operator is choosing.
 */
export const PRINT_TEMPLATE_FIELD_LABELS: Readonly<
  Record<PrintTemplateField, string>
> = {
  title: "Document title",
  header_lines: "Letterhead lines (under the company block)",
  footer_lines: "Footer lines",
  declaration: "Declaration",
  terms: "Terms",
  signature_label: "Signature label",
  bank_details: "Bank details",
};
