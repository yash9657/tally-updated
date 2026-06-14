/**
 * Shared vocabulary for the keystroke/navigation engine.
 *
 * These string unions are the contract shared with the golden keystroke corpora
 * (`/corpora/keystroke/*.json`). Screen names, voucher types, field names, and
 * button-bar labels MUST stay in lock-step with the corpus `expect` blocks.
 */

/**
 * A logical screen on Tally's screen stack.
 *
 *  - "gateway" / "menu"  — the Gateway of Tally and submenus.
 *  - "voucher"           — any of the six voucher entry/alteration screens.
 *  - "report"            — any of the five Phase 1 financial reports.
 *  - "report-detail"     — a drill-down level INSIDE a report (e.g. a group or
 *                          ledger row expanded, or a party's bills) reached by
 *                          Enter on a report row that is not itself a voucher.
 *  - "goto-overlay"      — the Alt+G / Ctrl+G picker (preserves or replaces).
 *  - "master-create"     — an inline master-creation screen (Alt+C), e.g.
 *                          creating a ledger from a voucher field and returning.
 */
export type ScreenKind =
  | "gateway"
  | "menu"
  | "voucher"
  | "report"
  | "report-detail"
  | "goto-overlay"
  | "master-create";

/**
 * The kind of master a "master-create" screen is creating. Mirrors the masters
 * the backend exposes; the masters agent renders the matching form.
 */
export type MasterKind =
  | "ledger"
  | "group"
  | "voucher-type"
  | "unit"
  | "stock-item";

/**
 * The canonical Phase 1 report names. These strings are the `reportName` on a
 * "report" frame and the Go To picker entries. The reports agent keys its
 * screen components off these EXACT strings.
 */
export const REPORT_NAMES = [
  "Balance Sheet",
  "Profit & Loss A/c",
  "Trial Balance",
  "Day Book",
  "Outstandings",
] as const;
export type ReportName = (typeof REPORT_NAMES)[number];

/** The six core voucher types reproduced in Phase 0 (RESEARCH.md sec 22). */
export type VoucherType =
  | "contra"
  | "payment"
  | "receipt"
  | "journal"
  | "sales"
  | "purchase";

/** Voucher entry mode (TallyPrime Ctrl+H Change Mode three-state enum). */
export type VoucherMode = "create" | "alter";

/**
 * A normalized key combination, e.g. "F8", "Enter", "Esc", "Ctrl+A", "Alt+G".
 * Modifiers are canonicalized (see `normalizeCombo`) so a keymap is a plain
 * lookup table — keymaps are data, not code (PLAN.md B.1).
 */
export type KeyCombo = string;

/**
 * A keymap action. The navigation machine interprets these; the keymap table
 * only maps a `KeyCombo` to one of these abstract actions, so swapping the
 * TallyPrime profile for an ERP 9 profile is a pure data change.
 */
export type KeymapAction =
  | { kind: "voucher"; voucherType: VoucherType }
  | { kind: "enter" }
  | { kind: "escape" }
  | { kind: "accept" } // Ctrl+A
  | { kind: "delete" } // Alt+D — delete the loaded voucher (alteration mode)
  | { kind: "goto" } // Alt+G — overlay, preserves prior screen
  | { kind: "switchTo" } // Ctrl+G — replaces the stack
  | { kind: "changeMode" } // Ctrl+H — Change Mode picker (vouchers)
  | { kind: "date" } // F2
  | { kind: "createMaster"; masterKind: MasterKind }; // Alt+C — inline create

/** A keymap profile: a named, data-driven combo → action table. */
export interface Keymap {
  readonly profile: string;
  readonly bindings: Readonly<Record<KeyCombo, KeymapAction>>;
}

/**
 * A normalized description of a key event, decoupled from the DOM so the
 * machine can be driven by corpus JSON or by real `KeyboardEvent`s identically.
 */
export interface KeyEventLike {
  readonly key: string;
  readonly ctrlKey?: boolean;
  readonly altKey?: boolean;
  readonly shiftKey?: boolean;
  readonly metaKey?: boolean;
}
