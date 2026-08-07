/**
 * THE EXPORT MODULE (T15) — report rows in, a spreadsheet file out.
 *
 * Pure. No DOM, no React, no client. Everything in here is a string→string
 * function, which is the only reason the paisa question below is testable at
 * all: the bytes that reach the file are the bytes this module returns.
 *
 * ───────────────────────────────────────────────────────────────────────────
 * ⛔⛔ THE ONE THING THIS MODULE EXISTS TO GET RIGHT: EXCEL EATS MONEY.
 *
 * Money is fixed-4 decimal STRINGS on the wire ("1234.5600"). Write that into a
 * CSV cell as-is and Excel does not store the string — it parses it into an IEEE
 * double and re-renders it. Three things are lost, in ascending order of how
 * expensive they are:
 *
 *   1. "1234.5600" comes back "1234.56". The two trailing zeros are the SCALE,
 *      and the scale is part of the value in a fixed-point system.
 *   2. "0042" (a voucher number) comes back 42, and "2026-06-01" comes back as
 *      whatever the machine's locale thinks a date looks like. Identifiers and
 *      dates are not quantities and must not be arithmetic.
 *   3. "123456789012345.6789" does not fit in float64's ~15 significant digits.
 *      It comes back a DIFFERENT NUMBER. That is the one that hides for a year:
 *      it looks like money, it foots to itself, and it disagrees with the books.
 *
 * So money — and every other value whose exact characters matter — is emitted
 * TEXT-FORCED, using the `="…"` idiom: the cell body is an Excel formula whose
 * value is a string LITERAL. Excel evaluates it, gets the characters back
 * unchanged, and never sees a number to coerce. LibreOffice does the same.
 *
 * ⚠️ WHAT THIS COSTS, STATED PLAINLY: a text-forced column cannot be SUM()ed in
 * Excel without a VALUE() around it. That is a deliberate trade — an operator
 * who needs to total a column can do it in one step, whereas an operator whose
 * closing balance quietly moved in the fifteenth digit finds out from a
 * chartered accountant. Exactness first; convenience is recoverable, precision
 * is not.
 *
 * ⚠️ AND ITS FAILURE MODE IS THE SAFE ONE. If a spreadsheet somewhere does NOT
 * evaluate `="1234.5600"`, the operator sees the literal text `="1234.5600"` —
 * ugly, obvious, and LOSSLESS. The alternative failure mode (a plain numeric
 * cell) is invisible and lossy. When a choice has to be made between a visible
 * wart and a silent wrong number, this repository takes the wart.
 *
 * ───────────────────────────────────────────────────────────────────────────
 * ⛔ AND THE SECOND THING: A CSV CELL IS AN EXECUTION VECTOR.
 *
 * A cell whose text begins with `=`, `+`, `-`, `@`, TAB or CR is a FORMULA to
 * Excel, and quoting the CSV field does not change that (this is the standard
 * CSV-injection finding, and every published payload is written inside quotes).
 * A ledger named `=cmd|'/C calc'!A0` — or, far more plausibly, a narration
 * pasted from somewhere — would run on the accountant's machine when they open
 * our export. That is a security defect, not a formatting one.
 *
 * The neutralisation used here is the SAME `="…"` wrapper: the dangerous text
 * becomes a string literal, so Excel evaluates it to exactly those characters
 * and executes nothing. Nothing is stripped, nothing is renamed, and the cell
 * still reads as what the report said. An apostrophe prefix was rejected because
 * it CHANGES THE DATA (`'=SUM(A1)` is a different string from `=SUM(A1)`), and an
 * export that silently edits a ledger name is its own bug.
 */

/**
 * How a cell must survive the trip into a spreadsheet.
 *
 *  - `text`   — prose (a ledger name, a narration, a party). Coercion is not a
 *               risk for prose, so it is written plainly, EXCEPT when it starts
 *               with a formula-trigger character, which is neutralised.
 *  - `exact`  — the characters matter, byte for byte: money, quantities, rates,
 *               dates, voucher numbers, GSTINs, HSN codes. Always text-forced.
 */
export type ExportCellKind = "text" | "exact";

export interface ExportCell {
  readonly kind: ExportCellKind;
  /** The value, exactly as it should come back out of the spreadsheet. */
  readonly value: string;
}

/** A row is a cell per column, in column order. */
export type ExportRow = readonly ExportCell[];

/** One report, ready to be written to a file. */
export interface ExportSheet {
  /** Report title; becomes the file name and the worksheet name. */
  readonly title: string;
  readonly headers: readonly string[];
  readonly rows: readonly ExportRow[];
}

/** The formats this build can actually write. See {@link UNBUILT_FORMATS}. */
export type ExportFormat = "csv" | "excel-xml";

/**
 * Tally's own Alt+E also offers PDF, JPEG, HTML and "e-mail this document".
 * NONE of them is built here, and they are listed rather than omitted for the
 * reason the keymap lists its `unavailable` actions: an operator cannot tell a
 * missing feature from a broken one. (Printed PDFs DO exist — that is Ctrl+P,
 * a server-rendered statement, and a different thing from exporting the rows
 * on screen.)
 */
export const UNBUILT_FORMATS: readonly string[] = [
  "PDF",
  "JPEG",
  "HTML",
  "e-mail this document",
];

/** File extension for a format. */
export function extensionFor(format: ExportFormat): string {
  return format === "excel-xml" ? "xml" : "csv";
}

// ---------------------------------------------------------------------------
// Cell constructors. Screens use THESE rather than building the object literal,
// because which constructor a column uses is the whole safety decision and it
// should read as a word at the call site.
// ---------------------------------------------------------------------------

function str(value: string | number | null | undefined): string {
  if (value === null || value === undefined) return "";
  return String(value);
}

/** Prose: a ledger name, a party, a narration, a section heading. */
export function textCell(value: string | null | undefined): ExportCell {
  return { kind: "text", value: str(value) };
}

/**
 * MONEY. A fixed-4 decimal string straight off the wire — never a formatted
 * one, and never a `number`. Pass `e.debit`, not `formatMoney(e.debit)`: the
 * display form is Indian-grouped ("12,34,567.89") and truncated to two
 * decimals, so exporting it would ship a comma into a comma-separated file AND
 * throw away two digits of scale before Excel ever got a chance to.
 */
export function moneyCell(value: string | null | undefined): ExportCell {
  return exactCell(value);
}

/**
 * Anything else whose characters must survive verbatim: quantities, rates,
 * percentages, ISO dates, voucher numbers, GSTINs, HSN codes, section codes.
 * Same treatment as money — the reasons in the header apply to all of them.
 */
export function exactCell(value: string | null | undefined): ExportCell {
  return { kind: "exact", value: str(value) };
}

/** Build a row of plain text cells (headers, and rows that carry no figures). */
export function textRow(values: readonly (string | null | undefined)[]): ExportRow {
  return values.map(textCell);
}

// ---------------------------------------------------------------------------
// Shape
// ---------------------------------------------------------------------------

/**
 * Thrown when a sheet's rows do not all have one cell per header.
 *
 * ⛔ THIS THROWS RATHER THAN PADDING. A short row silently padded puts every
 * figure after the gap under the WRONG HEADING — a credit column read as a
 * debit — and the file still opens and still foots. There is no tolerant
 * behaviour here that is not a wrong number, so the export refuses and says
 * which row. The caller turns that into a status line.
 */
export class ExportShapeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ExportShapeError";
  }
}

function assertRectangular(sheet: ExportSheet): void {
  const width = sheet.headers.length;
  if (width === 0) {
    throw new ExportShapeError(
      `Cannot export "${sheet.title}": it has no columns.`,
    );
  }
  sheet.rows.forEach((row, i) => {
    if (row.length !== width) {
      throw new ExportShapeError(
        `Cannot export "${sheet.title}": row ${i + 1} has ${row.length} ` +
          `cell(s) but the report has ${width} column(s). Exporting it would ` +
          `put figures under the wrong heading.`,
      );
    }
  });
}

// ---------------------------------------------------------------------------
// CSV
// ---------------------------------------------------------------------------

/**
 * Characters that make Excel read a cell as a formula rather than as text.
 * TAB and CR are here because Excel strips leading whitespace before deciding,
 * so "\t=1+1" is a formula too.
 */
const FORMULA_TRIGGERS = ["=", "+", "-", "@", "\t", "\r"];

function isFormulaDangerous(value: string): boolean {
  return FORMULA_TRIGGERS.some((c) => value.startsWith(c));
}

/**
 * Wrap a value as an Excel string-literal formula: `="…"`. Inside a formula a
 * double quote is escaped by doubling it — which is coincidentally the same
 * rule RFC 4180 uses one layer out, and the two layers are applied
 * independently (here, then in {@link csvField}).
 */
function forceText(value: string): string {
  return `="${value.replace(/"/g, '""')}"`;
}

/** The literal characters a cell contributes to the file, before CSV quoting. */
export function csvCellBody(cell: ExportCell): string {
  // An EMPTY exact cell stays empty. `=""` would render as a blank Excel cell
  // anyway, but it makes every no-figure cell in the file look like a formula,
  // and a reader scanning the raw CSV should be able to see that nothing is
  // there.
  if (cell.value === "") return "";
  if (cell.kind === "exact") return forceText(cell.value);
  return isFormulaDangerous(cell.value) ? forceText(cell.value) : cell.value;
}

/**
 * RFC 4180 field encoding. Every field is quoted — unconditionally, rather than
 * only when it contains a delimiter — because a conditional rule is one more
 * thing to get wrong on the one row that has a comma in a party name, and a
 * uniformly quoted file is the easiest thing to read in a diff.
 */
function csvField(body: string): string {
  return `"${body.replace(/"/g, '""')}"`;
}

/**
 * Render a sheet as RFC 4180 CSV with CRLF line endings (which is what the
 * spec says and what Excel prefers).
 *
 * The returned string carries NO byte-order mark. The BOM belongs to the FILE,
 * not to the CSV — see `download.ts`, which prepends it so Excel reads the file
 * as UTF-8 instead of the machine's ANSI code page and turns ₹ into three
 * mojibake characters.
 */
export function toCsv(sheet: ExportSheet): string {
  assertRectangular(sheet);
  const lines: string[] = [];
  lines.push(sheet.headers.map((h) => csvField(csvCellBody(textCell(h)))).join(","));
  for (const row of sheet.rows) {
    lines.push(row.map((c) => csvField(csvCellBody(c))).join(","));
  }
  // A trailing CRLF: the last record of a file MAY have one (RFC 4180 §2.2) and
  // tools that split on newlines behave better when it is there.
  return lines.join("\r\n") + "\r\n";
}

/**
 * Parse CSV back into the VALUES that were exported — the inverse of
 * {@link toCsv}, `="…"` unwrapping included.
 *
 * This is not a convenience: it is how the round-trip test can assert that a
 * paisa survived, and it is what any future re-import would use. It handles
 * quoted fields, doubled quotes, embedded commas and embedded newlines.
 */
export function parseCsv(text: string): string[][] {
  const withoutBom = text.startsWith("\uFEFF") ? text.slice(1) : text;
  const rows: string[][] = [];
  let row: string[] = [];
  let field = "";
  let inQuotes = false;
  let i = 0;
  let sawAnyChar = false;

  const endField = () => {
    row.push(unwrapForcedText(field));
    field = "";
  };
  const endRow = () => {
    endField();
    rows.push(row);
    row = [];
  };

  while (i < withoutBom.length) {
    const ch = withoutBom[i]!;
    sawAnyChar = true;
    if (inQuotes) {
      if (ch === '"') {
        if (withoutBom[i + 1] === '"') {
          field += '"';
          i += 2;
          continue;
        }
        inQuotes = false;
        i += 1;
        continue;
      }
      field += ch;
      i += 1;
      continue;
    }
    if (ch === '"') {
      inQuotes = true;
      i += 1;
      continue;
    }
    if (ch === ",") {
      endField();
      i += 1;
      continue;
    }
    if (ch === "\r" && withoutBom[i + 1] === "\n") {
      endRow();
      i += 2;
      continue;
    }
    if (ch === "\n") {
      endRow();
      i += 1;
      continue;
    }
    field += ch;
    i += 1;
  }
  if (field !== "" || row.length > 0 || (sawAnyChar && rows.length === 0)) {
    endRow();
  }
  return rows;
}

/**
 * Reverse the `="…"` text-forcing wrapper. A body that is not exactly a single
 * string literal is returned untouched — a cell whose text HAPPENS to start
 * `="` and end `"` but contains an unescaped quote in between was not written
 * by us and must not be reinterpreted.
 */
export function unwrapForcedText(body: string): string {
  if (body.length < 3 || !body.startsWith('="') || !body.endsWith('"')) {
    return body;
  }
  const inner = body.slice(2, -1);
  // Every quote inside a string literal must be part of a doubled pair;
  // otherwise this is not a literal we wrote.
  let i = 0;
  let out = "";
  while (i < inner.length) {
    const ch = inner[i]!;
    if (ch === '"') {
      if (inner[i + 1] !== '"') return body;
      out += '"';
      i += 2;
      continue;
    }
    out += ch;
    i += 1;
  }
  return out;
}

// ---------------------------------------------------------------------------
// SpreadsheetML (Excel 2003 XML) — the second format
// ---------------------------------------------------------------------------

/**
 * Excel 2003 SpreadsheetML. Chosen over a real `.xlsx` deliberately: xlsx is a
 * ZIP of parts and would mean pulling a binary-writing dependency into a
 * frontend that currently has three runtime dependencies. SpreadsheetML is a
 * single XML file, Excel opens it natively, and — the actual reason — its cells
 * carry a DECLARED TYPE. `ss:Type="String"` is a standards-level statement that
 * this cell is text, so the `="…"` idiom is not needed here at all: exactness
 * is asserted in the format instead of coaxed out of the parser.
 *
 * Every cell is written as String, including money, for the reason the module
 * header gives. The trade (no SUM() without VALUE()) is the same one.
 */
export function toSpreadsheetML(sheet: ExportSheet): string {
  assertRectangular(sheet);
  const cell = (value: string) =>
    `    <Cell><Data ss:Type="String">${xmlEscape(value)}</Data></Cell>`;
  const rowXml = (cells: readonly string[]) =>
    ["   <Row>", ...cells.map(cell), "   </Row>"].join("\n");

  const body = [
    rowXml(sheet.headers),
    ...sheet.rows.map((r) => rowXml(r.map((c) => c.value))),
  ].join("\n");

  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<?mso-application progid="Excel.Sheet"?>',
    '<Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet"',
    '          xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet">',
    ` <Worksheet ss:Name="${xmlEscape(worksheetName(sheet.title))}">`,
    "  <Table>",
    body,
    "  </Table>",
    " </Worksheet>",
    "</Workbook>",
    "",
  ].join("\n");
}

/**
 * Excel worksheet names may not exceed 31 characters and may not contain
 * : \ / ? * [ ]. An invalid name makes the whole workbook unopenable, which is
 * a worse outcome than a shortened tab label.
 */
export function worksheetName(title: string): string {
  const cleaned = title.replace(/[:\\/?*[\]]/g, "-").trim();
  return (cleaned || "Report").slice(0, 31);
}

function xmlEscape(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&apos;");
}

// ---------------------------------------------------------------------------
// Rendering + naming
// ---------------------------------------------------------------------------

/** Render a sheet in the requested format. */
export function renderSheet(sheet: ExportSheet, format: ExportFormat): string {
  return format === "excel-xml" ? toSpreadsheetML(sheet) : toCsv(sheet);
}

/** The MIME type the browser should be handed for a format. */
export function mimeTypeFor(format: ExportFormat): string {
  return format === "excel-xml"
    ? "application/vnd.ms-excel;charset=utf-8"
    : "text/csv;charset=utf-8";
}

/**
 * A safe download file name for a report title, e.g. "Profit & Loss A/c" ->
 * "Profit-Loss-A-c.csv". Slashes matter: a title carrying one would otherwise
 * look like a path to some browsers.
 */
export function exportFilename(title: string, format: ExportFormat): string {
  const stem =
    title
      .replace(/[^\w]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 80) || "Report";
  return `${stem}.${extensionFor(format)}`;
}
