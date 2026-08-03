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
  item("Ctrl+A", "Accept"),
  item("Ctrl+Q", "Quit"),
  item("F1", "Help"),
];

const MASTER_BAR: readonly ButtonBarItem[] = [
  item("Ctrl+A", "Accept"),
  item("Esc", "Cancel"),
  item("F1", "Help"),
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
  item("Ctrl+A", "Accept"),
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

/** The AI-native screens (Bill Inbox, Ask your books). */
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
