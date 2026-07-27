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
 *  - "bill-inbox"        — the F9 Bill Inbox: lists pending OCR drafts and opens
 *                          one into the Purchase (F9) voucher in draft-prefill
 *                          mode (the AI-native upload-to-voucher entry point).
 *  - "query"             — the "Ask your books" conversational query screen: a
 *                          natural-language question is sent to the safe
 *                          read-only query core and the typed answer is rendered
 *                          (the AI-native ask-your-data entry point).
 *  - "company-select"    — F3 Switch Company: the companies THIS USER may open
 *                          (GET /v1/me), with the security level they hold in
 *                          each. Selecting one re-scopes the whole session.
 *  - "members"           — Users & Roles: the company's members, its pending
 *                          invites, and its security levels. The browser-visible
 *                          half of the admin API.
 *  - "help"              — F1 Help: the context-aware keystroke reference, built
 *                          from the SAME keymap the engine dispatches, so it can
 *                          never drift from what the keys actually do.
 *  - "features"          — F11 Company Features: what this edition has enabled,
 *                          stated honestly (including what is not built yet).
 */
export type ScreenKind =
  | "gateway"
  | "menu"
  | "voucher"
  | "report"
  | "report-detail"
  | "goto-overlay"
  | "master-create"
  | "bill-inbox"
  | "query"
  | "company-select"
  | "members"
  | "edit-log"
  | "help"
  | "features";

/**
 * An in-frame overlay: a sub-screen that keeps its parent frame alive and is
 * dismissed by Esc (Tally's F12 Configure, F2 period, Ctrl+F filter, Ctrl+N
 * calculator and Ctrl+H Change Mode all behave this way).
 */
export type FrameOverlay =
  | "change-mode"
  | "configure"
  | "period"
  | "filter"
  | "calculator";

/**
 * Per-frame VIEW configuration — the state Tally's F12 Configure / Ctrl+H /
 * Alt+F1 / F5 buttons mutate. It lives on the frame (not in a screen's local
 * state) so it survives re-renders, is projected into the snapshot every screen
 * already receives, and is unit-testable in the pure machine.
 */
export interface FrameConfig {
  /** Alt+F1: expand every level of a statement report. */
  detailed?: boolean;
  /** F5 (Trial Balance): ledger-wise (default) instead of group-wise. */
  ledgerWise?: boolean;
  /** Ctrl+H (Balance Sheet): vertical instead of horizontal. */
  vertical?: boolean;
  /** F12: hide rows whose closing balance is zero. */
  hideZeroBalances?: boolean;
  /** F12 (Day Book): show the narration line under each entry. */
  showNarration?: boolean;
  /** Ctrl+F: case-insensitive substring filter over the report rows. */
  filterText?: string;
  /** F2 / Alt+F2: report period (ISO yyyy-mm-dd). */
  periodFrom?: string;
  periodTo?: string;
}

/**
 * The value `ledgerWise` has when a report frame has never been toggled. This
 * build's Trial Balance opens LEDGER-wise, and F5 switches to group-wise — the
 * machine, the status line and the Trial Balance screen all read this constant
 * so the toggle, the message and the rendering can never disagree.
 */
export const LEDGER_WISE_DEFAULT = true;

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
  | "purchase"
  /** Alt+F6 — sales return / downward price correction. */
  | "credit-note"
  /** Alt+F5 — purchase return / price correction raised on a supplier. */
  | "debit-note";

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
  | { kind: "quit" } // Ctrl+Q — quit the screen WITHOUT saving
  | { kind: "delete" } // Alt+D — delete the loaded voucher (alteration mode)
  | { kind: "cancelVoucher" } // Alt+X — cancel/clear the voucher being entered
  | { kind: "duplicate" } // Alt+2 — duplicate the loaded voucher into a new one
  | { kind: "nextField" } // Tab
  | { kind: "prevField" } // Shift+Tab
  | { kind: "prevVoucher" } // Page Up — previous saved voucher (alteration)
  | { kind: "nextVoucher" } // Page Down — next saved voucher (alteration)
  | { kind: "goto" } // Alt+G — overlay, preserves prior screen
  | { kind: "switchTo" } // Ctrl+G — replaces the stack
  | { kind: "changeMode" } // Ctrl+H — Change Mode picker (vouchers)
  | { kind: "changeView" } // Ctrl+H on a report — horizontal/vertical view
  | { kind: "date" } // F2 — voucher date
  | { kind: "period" } // F2 / Alt+F2 on a report — period (From/To)
  | { kind: "detailed" } // Alt+F1 / Alt+F5 — detailed (expand all levels)
  | { kind: "ledgerWise" } // F5 on a report — group-wise <-> ledger-wise
  | { kind: "filter" } // Ctrl+F — filter report rows
  | { kind: "exportScreen" } // Alt+E — export the active screen (CSV)
  | { kind: "calculator" } // Ctrl+N — calculator panel
  | { kind: "help" } // F1 / Ctrl+F1 — Help (the keystroke reference)
  | { kind: "features" } // F11 — Company Features
  | { kind: "configure" } // F12 — screen/report Configure
  | { kind: "createMaster"; masterKind: MasterKind } // Alt+C — inline create
  /**
   * F3 — Switch Company. Opens the list of companies this USER is a member of
   * (GET /v1/me). It was bound to `unavailable` for as long as a session could
   * only ever hold one company; the admin API made a second membership
   * reachable, so the key now does what a Tally operator expects.
   */
  | { kind: "switchCompany" }
  /**
   * Alt+L on a voucher — open its EDIT LOG (every hash-chained version, with a
   * verification of the chain).
   *
   * PLACEMENT is from RESEARCH.md sec 6: Tally surfaces the Edit Log from the
   * voucher alteration screen and the Day Book. The COMBO is not pinned there —
   * RESEARCH records Alt+L for other log-ish actions (Get IRN Info, Update
   * Opening BRS) but never for opening a voucher's log — so Alt+L is OUR choice,
   * and it is scoped to the voucher screen rather than claimed globally.
   */
  | { kind: "editLog" }
  /**
   * A combo that a real TallyPrime user WILL press but whose feature is not
   * built in this edition (e.g. payroll, order processing). It is bound on
   * purpose: pressing it must say so out loud rather than do nothing, which is
   * what makes a keyboard user distrust the app. `feature` is the human name,
   * `note` the one-line explanation shown in the status line.
   */
  | { kind: "unavailable"; feature: string; note: string };

/**
 * A keymap profile: a named, data-driven combo → action table.
 *
 * `bindings` is the global table. `screenBindings` holds per-screen OVERRIDES,
 * because Tally itself re-uses F-keys per context (F5 creates a Payment voucher
 * from the Gateway, but toggles ledger-wise inside the Trial Balance; F2 is the
 * voucher date but the report period). Resolution is: screen override first,
 * then the global table — see `resolveAction`.
 */
export interface Keymap {
  readonly profile: string;
  readonly bindings: Readonly<Record<KeyCombo, KeymapAction>>;
  readonly screenBindings?: Readonly<
    Partial<Record<ScreenKind, Readonly<Record<KeyCombo, KeymapAction>>>>
  >;
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
