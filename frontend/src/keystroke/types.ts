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
 *  - "bank-reconciliation" — Tally's F5 view on a bank ledger: the Bank
 *                          Reconciliation Statement, with the ONE editable
 *                          column (Bank Date) that ticks an item off against the
 *                          bank's own statement. Reconciling never touches the
 *                          voucher, so an ALTER after clearing is reported as
 *                          BROKEN rather than silently absorbed.
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
  | "features"
  | "bank-reconciliation";

/**
 * An in-frame overlay: a sub-screen that keeps its parent frame alive and is
 * dismissed by Esc (Tally's F12 Configure, F2 period, Ctrl+F filter, Ctrl+N
 * calculator and Ctrl+H Change Mode all behave this way).
 */
export type FrameOverlay =
  "change-mode" | "configure" | "period" | "filter" | "calculator";

/**
 * The data-capture forms Ctrl+H (Change Mode) can put a voucher into.
 *
 * It lives HERE rather than in voucherConfig.ts because the pure navigation
 * machine records the operator's choice ({@link FrameConfig.entryMode}, and
 * NavContext.entryModeByType) and the machine may not import from `screens/` —
 * that edge would invert the layering. Which modes a CLASS offers, and what
 * each one renders, stays voucherConfig's business.
 *
 *  - "single" / "double" — the classic entry styles (Payment / Receipt /
 *    Contra offer both; Journal is double only).
 *  - "accounting-invoice" — Sales / Purchase keyed as a TAXABLE VALUE against a
 *    Sales/Purchases ledger. No stock moves.
 *  - "item-invoice" — Sales / Purchase keyed as GOODS: item, godown, batch,
 *    quantity, rate. The accounting AND the GST are derived from the grid, and
 *    stock actually moves. Tally's default for a trading company.
 *  - "stock" — the ONLY mode the four non-accounting stock vouchers have: a
 *    grid of goods with NO ledger column at all, because these documents post
 *    no accounting lines. It is listed as a mode so Ctrl+H answers honestly on
 *    those screens (naming the one form that exists) rather than offering
 *    Single/Double entry, which would imply a money side that does not exist.
 */
export type EntryMode =
  "single" | "double" | "accounting-invoice" | "item-invoice" | "stock";

/**
 * Per-frame VIEW configuration — the state Tally's F12 Configure / Ctrl+H /
 * Alt+F1 / F5 buttons mutate. It lives on the frame (not in a screen's local
 * state) so it survives re-renders, is projected into the snapshot every screen
 * already receives, and is unit-testable in the pure machine.
 */
export interface FrameConfig {
  /**
   * Ctrl+H on a voucher: which DATA-CAPTURE FORM this voucher is keyed through.
   *
   * Tally defaults Payment / Receipt / Contra to single entry — "one account on
   * one side, multiple on the other" (RESEARCH.md §"Voucher entry") — which is
   * why bulk cash work is fast there, and offers Sales / Purchase the INVOICE
   * modes instead. Undefined means "the class's default"
   * (voucherConfig's `entry`), so the config only ever records a DEVIATION the
   * user chose, and changing a class default does not fight a stale frame.
   *
   * This is a DATA-ENTRY affordance, not a different accounting model: the
   * posted voucher is balanced double-entry in every mode. Only the form
   * differs — and, for an item invoice, what the server derives the accounting
   * FROM (goods rather than a typed taxable value).
   */
  entryMode?: EntryMode;
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
  "ledger" | "group" | "voucher-type" | "unit" | "stock-item";

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
  /**
   * Stock Summary — the inventory position as at a date. It was NOT listed here
   * while it had no data plane, because a Go To entry that lands on "this is
   * not built" is worse than an absent one; it is listed now that the movement
   * ledger exists and the screen reads it.
   *
   * Note it is DENIED to the seeded Data Entry level (migration 0026: it prints
   * the company's own cost prices), so this entry is reachable-but-refused for
   * some operators. The screen states that reason rather than showing a blank.
   */
  "Stock Summary",
  /**
   * TDS / TCS Register — everything deducted and collected in a period.
   *
   * Listed here for the same reason Stock Summary is, and with the same caveat:
   * it is DENIED to the seeded Data Entry level (migration 0068 — it is every
   * supplier paid and every customer collected from, with amounts), so this
   * entry is reachable-but-refused for some operators. The screen states that
   * reason rather than showing a blank.
   */
  "TDS / TCS Register",
] as const;
export type ReportName = (typeof REPORT_NAMES)[number];

/**
 * The voucher classes an operator can key.
 *
 * The first six are Phase 0's core accounting vouchers (RESEARCH.md sec 22);
 * the notes came with the GST wave. The last four are the NON-ACCOUNTING STOCK
 * VOUCHERS: they move goods and post ZERO accounting lines (see
 * internal/ledger/nonaccounting.go and screens/vouchers/stockVoucher.ts).
 *
 * GROWING THIS UNION IS DELIBERATELY EXPENSIVE. VOUCHER_CONFIGS is a
 * `Readonly<Record<VoucherType, VoucherConfig>>`, so tsc refuses to compile
 * until every new member has a real config with a real post/alter method —
 * which is what stops a keymap binding reaching a screen with no configuration
 * behind it. corpora/validate.py's KNOWN_VOUCHER_TYPES is checked against this
 * union by check_vocabulary_matches_typescript(), for the same reason.
 */
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
  | "debit-note"
  /** Alt+F8 — goods LEAVE against a customer, before or without the invoice. */
  | "delivery-note"
  /** Alt+F9 — goods ARRIVE from a supplier, before the bill. */
  | "receipt-note"
  /** Alt+F7 — a transfer between godowns: OUT of one, IN to another. */
  | "stock-journal"
  /**
   * Alt+F10 — the counted figure. The operator states what was COUNTED and the
   * SERVER derives the correction. Refused to the seeded Data Entry level by
   * migration 0031: it is the only document that makes the books agree with a
   * number a human asserts, so it is a supervisor's key.
   */
  | "physical-stock";

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
   * Ctrl+P / Alt+P — PRINT the active voucher or report.
   *
   * COMBO PROVENANCE (RESEARCH.md:2165, the ERP 9 / TallyPrime comparison table;
   * corroborated for Prime at :361 and :415): in ERP 9 print is Alt+P, and in
   * TallyPrime it is "Ctrl+P (voucher) / Alt+P (print menu)". So BOTH are live
   * TallyPrime bindings and they are NOT the same action — Ctrl+P prints this
   * voucher, Alt+P opens a print OPTIONS menu.
   *
   * We have no options menu. `viaOptionsMenu` records which key was pressed so
   * Alt+P can say that out loud and state that it printed with defaults. Binding
   * both silently to one direct print would let an operator believe they had
   * reached an options screen and chosen settings they never saw — the same class
   * of quiet wrongness this keymap's coverage rule exists to prevent.
   *
   * Note that binding these combos at all is what stops the BROWSER print dialog
   * opening: AppShell returns early on an unresolved combo, so before this action
   * existed Ctrl+P printed a screenshot of the DOM — no Rule 46 particulars, no
   * GSTIN. See docs/design/t1t4-ui-model.md §0.1.
   */
  | { kind: "print"; viaOptionsMenu?: boolean }
  /**
   * F5 on a bank ledger / the Gateway entry — the BANK RECONCILIATION screen.
   *
   * Tally reaches this by pressing F5 inside a bank ledger's voucher report, and
   * F5 is already `ledgerWise` on report screens, so the override is scoped
   * rather than global (see the keymap's screenBindings).
   */
  | { kind: "bankReconciliation" }
  /**
   * Alt+O on the Bank Reconciliation screen — IMPORT A BANK STATEMENT
   * (T4 slice 2a).
   *
   * Alt+O is Tally's Import, from the top horizontal menu ("O: Import"), cited
   * at RESEARCH.md:361, :415 and :1221 — a documented global, not a combo
   * recalled from memory. HANDOFF-PARITY-SPRINT.md §0.2 records that two
   * keystrokes were once invented from memory and both were wrong.
   *
   * It is bound PER SCREEN rather than globally because importing is
   * account-specific: a statement always belongs to one bank account, and the
   * screen is the only place that knows which is selected. A global Alt+O would
   * have to guess, and importing against the wrong account reconciles nothing
   * while leaving two accounts wrong.
   */
  | { kind: "importBankStatement" }
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
