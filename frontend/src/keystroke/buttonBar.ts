import type { ScreenKind, VoucherType } from "./types";

/**
 * The fixed-position right-side button bar (RESEARCH.md sec 4 & 22). In Tally
 * the bar's button positions are stable for muscle memory; the visible set is
 * context-scoped by the active screen. Labels are "<Key> <Action>" exactly as
 * the keystroke corpora assert via `buttonBarContains`.
 */
export interface ButtonBarItem {
  /** The displayed key, e.g. "F8", "Ctrl+A". */
  readonly key: string;
  /** The plain action label, e.g. "Sales". */
  readonly label: string;
  /** The full "<Key> <Action>" string used for corpus assertions. */
  readonly full: string;
}

function item(key: string, label: string): ButtonBarItem {
  return { key, label, full: `${key} ${label}` };
}

/**
 * The voucher button bar. The six core voucher F-keys are always present (their
 * fixed positions are part of the muscle memory contract), plus the universal
 * accept/navigation buttons.
 *
 * THE STOCK VOUCHERS (Alt+F7 Stock Journal, Alt+F8 Delivery Note, Alt+F9
 * Receipt Note, Alt+F10 Physical Stock) EARNED THEIR PLACE HERE only once they
 * had an entry screen. buttons.test.ts fails the build if a bar advertises an
 * `unavailable` action, and that rule was never relaxed to get them on early: a
 * button a user can SEE and PRESS that answers "not built" is worse than no
 * button, because the operator reaches for it by position from then on. They
 * are listed AFTER the six accounting keys so the fixed positions of F4–F9 —
 * the part that is muscle memory — do not move.
 */
const VOUCHER_BAR: readonly ButtonBarItem[] = [
  item("F4", "Contra"),
  item("F5", "Payment"),
  item("F6", "Receipt"),
  item("F7", "Journal"),
  item("F8", "Sales"),
  item("F9", "Purchase"),
  // ⛔ THE TWO NOTES WERE BOUND, LIVE, AND INVISIBLE. Alt+F5 and Alt+F6 open the
  // real Debit and Credit Note screens (keymaps/tallyprime.ts) and always have;
  // neither this bar nor hintsFor's `voucher` case named them. By this repo's own
  // standing rule — an operator reads the strip as the list of what works here —
  // a working feature that nothing advertises is an unreachable one, so the
  // parity ledger was counting two rows nobody could find. Found by reading on
  // 2026-08-07; this bar and the hint strip are the whole fix.
  item("Alt+F5", "Debit Note"),
  item("Alt+F6", "Credit Note"),
  item("Alt+F7", "Stock Journal"),
  item("Alt+F8", "Delivery Note"),
  item("Alt+F9", "Receipt Note"),
  item("Alt+F10", "Physical Stock"),
  item("F2", "Date"),
  item("Alt+C", "Create Master"),
  item("Ctrl+H", "Change Mode"),
  item("Alt+D", "Delete"),
  // Ctrl+P is TallyPrime's print-this-voucher key (RESEARCH.md:2165). It is on
  // the bar because the whole point of the change that added it is that an
  // operator could not print the invoice from the screen they keyed it on. It
  // prints a SAVED voucher — including one saved a moment ago by Ctrl+A — and
  // says so when there is nothing to print yet.
  item("Ctrl+P", "Print"),
  item("Alt+M", "E-Mail"),
  item("Ctrl+A", "Accept"),
  item("Ctrl+Q", "Quit"),
  item("F1", "Help"),
];

const MASTER_BAR: readonly ButtonBarItem[] = [
  item("Ctrl+A", "Accept"),
  item("Esc", "Cancel"),
  item("F1", "Help"),
];

// T5. Ctrl+A is the only screen-owned key here: the consignment form is on the
// face of the screen rather than behind an Alt+C panel, because there is exactly
// one thing to key and hiding it behind a key would be a step for its own sake.
const E_WAY_BILL_BAR: readonly ButtonBarItem[] = [
  item("F1", "Help"),
  item("Ctrl+A", "Raise"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
];

// ⛔ "Report" AND NOT "Raise" OR "Generate IRN". The e-way bill's Ctrl+A is
// re-raisable and its label can afford to be breezy; this one FREEZES the
// voucher for good. "Report" is the statutory verb (an invoice is REPORTED to
// the IRP) and it is the one that does not promise a number this build cannot
// issue — the bar has no room for the disclosure the screen carries.
const E_INVOICE_BAR: readonly ButtonBarItem[] = [
  item("F1", "Help"),
  item("Ctrl+A", "Report"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
];

const GATEWAY_BAR: readonly ButtonBarItem[] = [
  item("F1", "Help"),
  // F3 is Tally's company button. It only earned a place on the bar once a
  // session could actually hold more than one company (the admin API + /v1/me);
  // buttons.test.ts fails the build if an advertised key is decorative.
  item("F3", "Company"),
  item("F11", "Features"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("Ctrl+N", "Calculator"),
];

const REPORT_BAR: readonly ButtonBarItem[] = [
  item("F2", "Period"),
  item("F5", "Ledger-wise"),
  item("F12", "Configure"),
  item("Alt+F1", "Detailed"),
  item("Ctrl+H", "Change View"),
  item("Ctrl+F", "Filter"),
  item("Alt+E", "Export"),
  item("Ctrl+P", "Print"),
  item("Alt+M", "E-Mail"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * The Bank Reconciliation Statement bar, from RESEARCH.md:879's list of the keys
 * that live inside Tally's reconciliation screen.
 *
 * F4 picks the bank account, F2 dates the statement, and Alt+O imports the
 * bank's own CSV (T4 slice 2a; RESEARCH.md:361 "O: Import").
 *
 * Only the keys that are BUILT are advertised: :879 also documents F5 (amount
 * view), F8/Alt+F8 (modify and unlink a match), F9 (set status), Alt+S
 * (potential matches), Ctrl+J (exception report) and Ctrl+B (basis of values),
 * and all of those belong to MATCHING — T4 slice 2b. They are bound in the
 * keymap so they answer honestly, but buttons.test.ts fails the build if a BAR
 * advertises an `unavailable` action, and rightly: an operator reaches for a bar
 * button by position from then on.
 *
 * Alt+O JOINED THE BAR THE DAY IT BECAME REAL, which is the same rule read from
 * the other side — an import an operator cannot find is an import that reaches
 * nobody.
 *
 * Ctrl+P is here for that reason too, and it was overdue: this bar carried a
 * paragraph explaining that a BRS could not be printed because the print route
 * carried no ledger id. HANDOFF-PARITY-SPRINT.md §3.4 closed that by taking
 * `?ledger_id=`, and printTarget has resolved this screen ever since — so the
 * comment was describing a refusal that no longer existed while REPORT_BAR
 * advertised the same key one screen over.
 */
const BANK_RECON_BAR: readonly ButtonBarItem[] = [
  item("F2", "As on"),
  item("F4", "Bank"),
  item("Alt+O", "Import"),
  item("Ctrl+P", "Print"),
  item("Alt+M", "E-Mail"),
  item("Ctrl+A", "Accept"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * The TDS CHALLAN register bar (T6).
 *
 * Alt+C RECORDS a deposit (see the KeymapAction comment in keystroke/types.ts
 * for the combo's provenance and why it is screen-scoped) and Ctrl+A accepts the
 * form — Tally's own accept key doing exactly what it always does.
 *
 * Ctrl+P is deliberately NOT here. There is no printed layout for a challan
 * register, so the print target resolves to a sentence; this file's rule read
 * from the other side is that an operator reaches for a bar button by position
 * from then on, so advertising a key that can only explain itself is worse than
 * not advertising it. The key stays bound globally and still answers.
 */
const TDS_CHALLAN_BAR: readonly ButtonBarItem[] = [
  item("Alt+C", "Record Challan"),
  // ⛔ ADVERTISED, because a key nobody can find is the same as no key. D1's whole
  // subject is that an operator could not complete a challan recorded with too
  // few allocations; a fix reachable only by someone who read the keymap would
  // leave them exactly where they were.
  item("Alt+A", "Allocate More"),
  item("Ctrl+A", "Accept"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * THE COST MASTERS bar (T2) — the axes of analysis and their centres.
 *
 * Alt+C creates either one (see the KeymapAction comment in keystroke/types.ts:
 * Tally's own route to these masters is Alt+G > Create Master > Cost Centre,
 * RESEARCH.md:525) and Ctrl+A accepts the panel.
 *
 * F2 is NOT here. A master list has no period — the two cost REPORTS do — and the
 * keymap binds F2 on this screen to a refusal saying exactly that, which
 * buttons.test.ts would refuse to see advertised anyway.
 *
 * Ctrl+P is not here either, for the reason TDS_CHALLAN_BAR gives: there is no
 * printed layout for a list of cost masters, so the key can only explain itself,
 * and an operator reaches for a bar button by position from then on.
 */
const COST_MASTERS_BAR: readonly ButtonBarItem[] = [
  item("Alt+C", "Create"),
  item("Ctrl+A", "Accept"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * THE COST CENTRE SUMMARY bar (T2) — ONE axis, over a period.
 *
 * F2 is the period (both bounds: this is a flow over a range, not a position as
 * at a date) and F4 chooses the AXIS. F4's provenance is stated in full on the
 * `selectCostCategory` action in keystroke/types.ts: the combo is OURS, placed
 * where RESEARCH.md:879 puts the analogous account picker, and never claimed as a
 * documented TallyPrime binding.
 *
 * NOT ADVERTISED, each for a reason rather than an oversight: F5 (ledger-wise) is
 * meaningless when the AXIS is the grouping; Alt+F1 (detailed) has nothing to
 * expand, because every row already carries its OWN figures beside its rolled-up
 * TOTAL; Alt+C is Tally's New Column inside a report (RESEARCH.md:771, and :811
 * for this very report) and is not built; and Ctrl+P cannot print, because the
 * print route is a flat literal that carries no axis. All four are BOUND — they
 * answer — they are simply not sold from the bar.
 */
const COST_CENTRE_SUMMARY_BAR: readonly ButtonBarItem[] = [
  item("F2", "Period"),
  item("F4", "Cost Category"),
  // Alt+E JOINED THIS BAR THE DAY IT BECAME REAL, which is the rule the Alt+O
  // note on BANK_RECON_BAR states from the other side: a report an operator
  // cannot get out of the screen is a dead end, and an export nobody can find is
  // an export that reaches nobody. The screen registers a real handler (T15) and
  // the axis travels in the file name, so the file cannot be read under the
  // wrong axis's heading.
  item("Alt+E", "Export"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * THE COST CATEGORY SUMMARY bar (T2) — one row per axis, and the GAP.
 *
 * There is deliberately no axis picker: this report IS the list of axes, and
 * Enter on a row opens that axis's Cost Centre Summary (no report is a dead end —
 * RESEARCH.md sec 8). F2 is the period.
 */
const COST_CATEGORY_SUMMARY_BAR: readonly ButtonBarItem[] = [
  item("F2", "Period"),
  // T15, for the reason on COST_CENTRE_SUMMARY_BAR above. The exported file
  // carries the "these rows must not be added up" sentence as a row of its own,
  // because a flat file has no bordered panel to put it in and a total down any
  // of these columns is a multiple of the cost actually borne.
  item("Alt+E", "Export"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * THE ORDER REGISTER bar (T3) — one bar, used by BOTH registers.
 *
 * ⚠️ ONE BAR AND TWO SCREENS IS NOT THE THING THE ORDER FEATURE MUST NOT DO. The
 * rule that forces `sales-orders` and `purchase-orders` apart is about AUTHORITY:
 * the two are separate tokens on separate routes and the PDP reads the path, so a
 * screen serving both directions, or ONE REPORT NAME serving both, would be
 * reaching a resource the operator may be denied. A shared BUTTON BAR reaches
 * nothing — the keys on it (Alt+C, Alt+W, Ctrl+A) mean "create", "pre-close" and
 * "accept" on whichever register is active, and each register's own screen sends
 * them to its own route. Sharing the bar is what keeps the two labelled
 * identically, which is right: the direction is on the TITLE and on the screen's
 * own heading, where an operator reads it.
 *
 * Alt+W is Tally's documented pre-close key (RESEARCH.md:622). It is labelled
 * "Pre-close" — Tally's own word — and NOT "Close", because "close" reads as "mark
 * it delivered", which is the one thing it does not do: it ABANDONS the outstanding
 * balance and demands a reason.
 *
 * Ctrl+P is NOT here, for the reason TDS_CHALLAN_BAR gives: neither order book has
 * a specified printed layout (handler_orderbook.go says so), and a register of
 * orders has none at all, so the key could only explain itself — and an operator
 * reaches for a bar button by position from then on. It stays bound globally.
 */
const ORDER_REGISTER_BAR: readonly ButtonBarItem[] = [
  item("Alt+C", "New Order"),
  item("Alt+W", "Pre-close"),
  item("Ctrl+A", "Accept"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * THE BILLS OF MATERIALS bar (T3) — the recipes, and the explosion preview.
 *
 * Alt+C defines a recipe; Alt+X explodes the highlighted one for a quantity.
 * Alt+X's provenance is stated in full on the `explodeBom` action in
 * keystroke/types.ts: the combo is OURS, because in Tally the explosion only ever
 * happens inside a Manufacturing Journal (RESEARCH.md:628) and there is no key to
 * press on its own. It is labelled "Explode" and the key's own label adds that
 * nothing moves — the explosion is a PREVIEW of what a Stock Journal's lines would
 * be, and an operator who believed it had produced something would be looking for
 * stock that is not there.
 *
 * F2 is not here (a recipe has no period — it is a master) and neither is Ctrl+P
 * (no printed layout for a recipe list).
 */
const BOM_BAR: readonly ButtonBarItem[] = [
  item("Alt+C", "New Recipe"),
  item("Alt+X", "Explode"),
  item("Ctrl+A", "Accept"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * THE EMPLOYEE MASTER bar (Track 3).
 *
 * ⛔ TWO WRITE KEYS, AND THE SECOND ONE IS NOT A REFINEMENT. Alt+C creates the
 * person; Alt+S sets their salary structure and statutory election, WITHOUT WHICH
 * cored refuses the entire pay run — "no salary structure is in force", "no
 * statutory election is in force" — and refuses it on the RUN, long after the
 * operator left this screen believing the record was complete. A bar that
 * advertised only Alt+C would be advertising half a task.
 *
 * Ctrl+P is NOT here, for the reason TDS_CHALLAN_BAR gives: there is no printed
 * layout for a list of employees, so the key could only explain itself — and an
 * operator reaches for a bar button by position from then on. It stays bound
 * globally, and printTarget answers it with a sentence.
 */
const EMPLOYEES_BAR: readonly ButtonBarItem[] = [
  item("Alt+C", "New Employee"),
  item("Alt+S", "Set Pay"),
  item("Ctrl+A", "Accept"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * THE PAY RUN bar (Track 3).
 *
 * Ctrl+A is the only write, and it writes a whole month at once. F2 is NOT here:
 * a pay run has no date to set — the server DERIVES the voucher date from the
 * wage month (the last day of it), which is also the date the back-dated cutoff
 * judges, so a date on this bar would advertise a choice the server overwrites.
 */
const PAY_RUN_BAR: readonly ButtonBarItem[] = [
  item("Ctrl+A", "Post Pay Run"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * THE PAYROLL REGISTER bar (Track 3).
 *
 * Ctrl+P IS here, unlike on every other screen added in this wave, and that is a
 * fact and not a preference: `payroll-register` has a REAL printed layout on the
 * server (handler_print_reports.go's `printPayrollRegister`, with the ruleset
 * versions laid out on the page), so the key produces a document rather than a
 * sentence. Alt+E writes the rows on screen to a spreadsheet.
 *
 * Alt+S opens the statutory files panel — the EPFO ECR and the ESIC monthly
 * return for the month on screen. ⚠️ Form 138 is deliberately NOT on this bar: it
 * is QUARTERLY and needs a TAN, a deductor PAN and every challan's counterfoil,
 * none of which is in the database, so it is a FORM on its own screen (Alt+G >
 * Form 138). A button beside two one-click downloads would imply it takes one
 * click, and the operator would meet four 400s discovering otherwise.
 *
 * F2 is NOT here: this register's dimension is a WAGE MONTH typed on its own
 * face, never a From/To range — see the keymap's note on F2 for why a range is a
 * span no statutory scheme has an answer for.
 */
const PAYROLL_REGISTER_BAR: readonly ButtonBarItem[] = [
  item("Alt+S", "Statutory Files"),
  item("Ctrl+P", "Print"),
  item("Alt+M", "E-Mail"),
  item("Alt+E", "Export"),
  item("Enter", "Payslip"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * THE PAYSLIP bar (Track 3). A pure reader with a real printed layout
 * (`printPayrollPayslip`), so Ctrl+P produces the document an employee is handed.
 *
 * There is no Alt+E: a payslip is one person's document, not a table of rows, and
 * a CSV of it invites the one thing a payslip must not become — a column in
 * somebody's spreadsheet of everybody's pay. The register is where rows are
 * exported, and it is ACL'd as its own report name for exactly that reason.
 */
const PAYSLIP_BAR: readonly ButtonBarItem[] = [
  item("Ctrl+P", "Print"),
  item("Alt+M", "E-Mail"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * THE FORM 138 bar (Track 3). Ctrl+A builds and downloads the statement.
 *
 * ⛔ "Download" AND NOT "File". Nothing this screen produces has been through the
 * department's FVU — it cannot run in this environment — and the archive says so
 * on its own face, in a README the download cannot be separated from. A button
 * labelled "File Return" would be a claim about a filing that has not happened.
 */
const FORM_138_BAR: readonly ButtonBarItem[] = [
  item("Ctrl+A", "Download"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/** Screens that are pure readers (Help, Features): Esc out, nothing to save. */
const READER_BAR: readonly ButtonBarItem[] = [
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
];

/**
 * THE DATA SCREEN (Alt+Y) — backup and restore.
 *
 * Ctrl+A is not offered and that is deliberate: this screen has nothing to
 * "accept". Its two actions are a download and an upload, each with its own
 * consequence, and a generic Accept would have to pick one of them for the
 * operator.
 */
const DATA_BAR: readonly ButtonBarItem[] = [
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * MIGRATE FROM TALLY (Alt+O) — the TallyPrime XML import screen (T32).
 *
 * Ctrl+A is not offered, deliberately and for the same reason the Data screen
 * does not offer it: this screen has nothing to "accept". Its one action is an
 * import that is not undoable, and it is behind a named button next to the file
 * that was chosen — a generic "Accept (save) the screen" would fire an
 * irreversible write from a key an operator presses reflexively on every form in
 * the product.
 */
const TALLY_IMPORT_BAR: readonly ButtonBarItem[] = [
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/**
 * PRINT TEMPLATES (track C) — the per-company document layouts.
 *
 * Ctrl+A is not offered, for the reason the Data and import screens do not offer
 * it: this screen has TWO named actions with different consequences — save a
 * layout, and clear a customisation — and a generic "Accept (save) the screen"
 * cannot say which it would do. Both are behind named buttons, and the clear
 * button appears only when there is something to clear.
 */
const PRINT_TEMPLATES_BAR: readonly ButtonBarItem[] = [
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
  item("F1", "Help"),
];

/** The AI-native screens (Bill Inbox, Ask your books). */
/**
 * ⛔ ALT+M SITS BESIDE Ctrl+P ON EVERY BAR THAT HAS ONE, AND NOWHERE ELSE (B2).
 *
 * That is not a layout preference, it is the same invariant `usePrintAction`
 * enforces in code: Alt+M asks `resolvePrintTarget` the identical question Ctrl+P
 * does, so a screen that can print its document can e-mail it and a screen that
 * cannot refuses both with the same sentence. A bar offering one without the other
 * would advertise a difference that does not exist — and the bars that omit Ctrl+P
 * (the TDS challan register, both order books, the BOM screen) omit Alt+M for the
 * reason they already state: there is no printed layout to send.
 *
 * ⚠️ THE LABEL IS "E-Mail" AND THE FULL SENTENCE IS IN `actionLabel()`. There is
 * no room on a bar for "saves the PDF, opens your mail client", and F1's keyboard
 * help is where an operator reads what a key actually does.
 */
const AI_BAR: readonly ButtonBarItem[] = [
  item("F1", "Help"),
  item("Esc", "Back"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
];

/**
 * Resolve the visible button bar for a given screen. Voucher type is accepted
 * for forward-compatibility (e.g. enabling Credit/Debit Note buttons) though
 * the core six are constant across voucher types in Phase 0.
 */
export function buttonBarFor(
  screen: ScreenKind,
  _voucherType?: VoucherType,
): readonly ButtonBarItem[] {
  switch (screen) {
    case "voucher":
      return VOUCHER_BAR;
    case "report":
    case "report-detail":
      return REPORT_BAR;
    case "bank-reconciliation":
      return BANK_RECON_BAR;
    // WITHOUT THIS CASE THE SCREEN FALLS THROUGH TO GATEWAY_BAR AND NO TEST
    // CATCHES IT: the bar would advertise F3 Company and F11 Features over a
    // half-keyed challan form and say nothing at all about Alt+C.
    case "tds-challans":
      return TDS_CHALLAN_BAR;
    // The three T2 COST screens, for the same reason: without these cases they
    // fall through to GATEWAY_BAR, WHICH NO TEST CATCHES. The bar would advertise
    // F3 Company and F11 Features over a half-keyed cost centre and say nothing
    // at all about Alt+C, or about F4 on the report that is useless without it.
    // T5, for the reason every case here exists: without it the screen falls
    // through to GATEWAY_BAR, WHICH NO TEST CATCHES. The bar would advertise F3
    // Company and F11 Features over a half-stated consignment and say nothing at
    // all about Ctrl+A, which is the only way to raise the bill.
    case "e-way-bill":
      return E_WAY_BILL_BAR;
    // The e-invoice, for the same reason: without this case it falls through to
    // GATEWAY_BAR, WHICH NO TEST CATCHES — the bar would advertise F3 Company and
    // F11 Features and say nothing at all about Ctrl+A, which is the only way to
    // report the invoice.
    case "e-invoice":
      return E_INVOICE_BAR;
    case "cost-centres":
      return COST_MASTERS_BAR;
    case "cost-centre-summary":
      return COST_CENTRE_SUMMARY_BAR;
    case "cost-category-summary":
      return COST_CATEGORY_SUMMARY_BAR;
    // The three T3 screens, for the reason every case above exists: without them
    // they fall through to GATEWAY_BAR, WHICH NO TEST CATCHES. The bar would
    // advertise F3 Company and F11 Features over a half-keyed order and say nothing
    // at all about Alt+C — or about Alt+W, which is the only way to abandon a
    // commitment that will otherwise sit in the order book forever.
    case "sales-orders":
    case "purchase-orders":
      return ORDER_REGISTER_BAR;
    case "boms":
      return BOM_BAR;
    // The five PAYROLL screens, for the reason every case here exists: without
    // them they fall through to GATEWAY_BAR, WHICH NO TEST CATCHES. The bar would
    // advertise F3 Company and F11 Features over a half-keyed employee and say
    // nothing about Alt+C — or about Alt+S, which is the only key that makes a
    // person payable at all, and whose absence cored reports only on the pay run.
    case "employees":
      return EMPLOYEES_BAR;
    case "payroll-run":
      return PAY_RUN_BAR;
    case "payroll-register":
      return PAYROLL_REGISTER_BAR;
    case "payroll-payslip":
      return PAYSLIP_BAR;
    case "payroll-form-138":
      return FORM_138_BAR;
    case "master-create":
      return MASTER_BAR;
    case "gateway":
    case "menu":
      return GATEWAY_BAR;
    case "goto-overlay":
      return GATEWAY_BAR;
    case "help":
    case "features":
      return READER_BAR;
    case "data":
      return DATA_BAR;
    case "tally-import":
      return TALLY_IMPORT_BAR;
    case "print-templates":
      return PRINT_TEMPLATES_BAR;
    case "bill-inbox":
    case "query":
      return AI_BAR;
    // The SaaS screens: pure readers with a pick-list, like the AI screens.
    case "company-select":
    case "members":
      return AI_BAR;
    // The Edit Log is a pure reader: nothing on it is editable, by design.
    case "edit-log":
      return READER_BAR;
    default:
      return GATEWAY_BAR;
  }
}

/** The list of full "<Key> <Action>" labels for the screen (corpus shape). */
export function buttonBarLabels(
  screen: ScreenKind,
  voucherType?: VoucherType,
): string[] {
  return buttonBarFor(screen, voucherType).map((b) => b.full);
}
