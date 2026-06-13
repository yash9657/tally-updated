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
 */
const VOUCHER_BAR: readonly ButtonBarItem[] = [
  item("F4", "Contra"),
  item("F5", "Payment"),
  item("F6", "Receipt"),
  item("F7", "Journal"),
  item("F8", "Sales"),
  item("F9", "Purchase"),
  item("Ctrl+H", "Change Mode"),
  item("Ctrl+A", "Accept"),
];

const GATEWAY_BAR: readonly ButtonBarItem[] = [
  item("F1", "Help"),
  item("Alt+G", "Go To"),
  item("Ctrl+G", "Switch To"),
];

const REPORT_BAR: readonly ButtonBarItem[] = [
  item("F2", "Period"),
  item("F12", "Configure"),
  item("Ctrl+H", "Change View"),
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
      return REPORT_BAR;
    case "gateway":
    case "menu":
      return GATEWAY_BAR;
    case "goto-overlay":
      return GATEWAY_BAR;
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
