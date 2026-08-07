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
 *  - "tds-challans"      — the TDS CHALLAN register (T6, migration 0069): the
 *                          deposits made to the Government for one tax year and
 *                          WHICH DEDUCTIONS each one paid, plus the form that
 *                          records a new one. A ScreenKind and NOT a
 *                          "report:NAME": a report name is reached by migration
 *                          0011's coarse `allow display report`, whereas the
 *                          server serves this as the bare resource token
 *                          `tds-challan` (refused to the seeded Data Entry level
 *                          by default_access 'none'). As a ScreenKind it also
 *                          needs no FrameOverlays period classification — the F2
 *                          overlay opens only for report screens, and this
 *                          screen's dimension is a TAX YEAR asked for on its
 *                          face, never derived from a deposit date.
 *  - "e-way-bill"       — THE e-WAY BILL (T5, Rule 138). Pick the voucher the
 *                          goods move against, state the movement (distance,
 *                          vehicle, transporter, cargo class), and Ctrl+A
 *                          raises the payload NIC would be sent. ⛔ NOTHING IT
 *                          SHOWS IS AN EBN — the portal is unreachable from
 *                          this deployment, so the identifier is a LOCAL
 *                          reference and the screen says so on its face.
 *                          A ScreenKind and not a "report:NAME" for the reason
 *                          cost-centres is: the server serves it under the BARE
 *                          token `e-way-bill`, which a coarse `allow display
 *                          report` can never match — and it carries a FORM, so
 *                          it needs no period and is exempt from the
 *                          FrameOverlays classification.
 *  - "e-invoice"        — THE e-INVOICE / IRN (§1.4 B). Pick the sales invoice
 *                          to report and Ctrl+A raises it through cored's proxy
 *                          into statutoryd. ⛔ NOTHING IT SHOWS IS A
 *                          GOVERNMENT-ISSUED IRN — no IRP credentials exist in
 *                          this deployment, so the reference is minted by a fake
 *                          provider and the screen says so on its face.
 *                          ⛔⛔ AND ITS Ctrl+A IS THE ONLY IRREVERSIBLE KEY IN
 *                          THE PRODUCT: raising an e-invoice FREEZES the
 *                          voucher against alteration and deletion, with no
 *                          undo anywhere. The screen states that ABOVE the key
 *                          rather than on the result panel.
 *                          The form carries NO operator-stated fields at all,
 *                          which is what distinguishes it from "e-way-bill":
 *                          a consignment has facts no ledger holds (which
 *                          lorry, how far), and an e-invoice has none — it is
 *                          entirely a restatement of the invoice, so the server
 *                          derives every figure and there is nothing to
 *                          mistype onto a document about to be frozen.
 *                          A ScreenKind and not a "report:NAME" for the reason
 *                          e-way-bill is: the server serves it under the BARE
 *                          token `e-invoice`, which a coarse `allow display
 *                          report` can never match — and it carries a FORM, so
 *                          it needs no period and is exempt from the
 *                          FrameOverlays classification.
 *  - "cost-centres"      — the COST MASTERS (T2): the axes of analysis (cost
 *                          CATEGORIES — Department, Branch, Project) and the
 *                          positions on them (cost CENTRES), plus the one form
 *                          that creates either. Tally reaches the same masters
 *                          through Alt+G > Create Master > Cost Centre
 *                          (RESEARCH.md:525). A ScreenKind and not a
 *                          "report:NAME" for the same reason tds-challans is:
 *                          the server serves these routes under the BARE token
 *                          `cost-centre` (internal/authz/vocabulary.go), which a
 *                          coarse `allow display report` can never match — and
 *                          because a master list is not a report. It carries a
 *                          create form and therefore needs NO period, so it is
 *                          exempt from the FrameOverlays classification (the F2
 *                          overlay opens only for reports).
 *  - "cost-centre-summary" — THE COST CENTRE SUMMARY (T2): what each centre on
 *                          ONE axis cost over a period, rolled up through the
 *                          centre hierarchy. ⛔ ONE AXIS, ALWAYS: centres on
 *                          different axes describe the SAME money (see the
 *                          CostCategory doc in api/types.ts), so the screen
 *                          takes an axis and has no "all centres" mode.
 *  - "cost-category-summary" — THE COST CATEGORY SUMMARY (T2): one row per axis
 *                          over a period — how much of the period's allocable
 *                          cost each axis accounts for, and the UNALLOCATED
 *                          remainder that is the report's whole purpose. ⛔ It
 *                          has NO grand total and may never grow one.
 *
 *  - "sales-orders" / "purchase-orders" — THE ORDER REGISTERS (T3, migration
 *                          0042): the commitments made to customers and to
 *                          suppliers, the form that records one, and the
 *                          pre-closure that abandons one. ⛔ TWO ScreenKinds AND
 *                          NOT ONE WITH A DIRECTION TOGGLE, for the same reason
 *                          the two order BOOKS are two report names: the server
 *                          serves them on two routes under two bare tokens
 *                          (`sales-order`, `purchase-order`), so the PDP decides
 *                          on the PATH. A toggle would be reaching a resource
 *                          the operator may be denied — "this salesperson may
 *                          commit us to customers but may not commit us to
 *                          suppliers" is a real rule, and one screen makes it
 *                          unwritable. Tally has two KEYS for the same reason
 *                          (Ctrl+F8 / Ctrl+F9, RESEARCH.md:400, :614, :616).
 *                          Neither needs a FrameOverlays classification: an
 *                          order register is a list of masters with a form on
 *                          it, and what is OUTSTANDING as at a date is the order
 *                          book, which is a report and is classified there.
 *  - "boms"              — BILLS OF MATERIALS (T3, migration 0043): the recipes
 *                          defined on a finished good, and the explosion that
 *                          shows what a Stock Journal's lines WOULD be. A master
 *                          list with a create form, like "cost-centres", and a
 *                          bare `bom` token on the server rather than a report
 *                          name. ⚠️ It renders no money and has none: a recipe
 *                          is quantities, and what those quantities COST is the
 *                          valuation plane.
 *
 * ⚠️ Both cost SUMMARIES are ScreenKinds AND report names on the server
 * (`report:cost-centre-summary`, `report:cost-category-summary`, refused to the
 * seeded Data Entry level by migration 0072). They are ScreenKinds here because
 * neither is reached by a reportName frame: one needs an AXIS the pure machine
 * cannot know, and both carry a shape no generic report screen renders. Being
 * ScreenKinds rather than "report" frames is exactly why each needs its own
 * FrameOverlays period classification — see FULL_PERIOD_REPORTS.
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
  | "tds-challans"
  | "cost-centres"
  | "cost-centre-summary"
  | "cost-category-summary"
  | "sales-orders"
  | "purchase-orders"
  | "boms"
  | "e-way-bill"
  | "e-invoice"
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
  /**
   * F12 (Alt+E export): write Excel 2003 XML (SpreadsheetML) instead of CSV.
   *
   * Both formats carry the SAME values — money is exact in both — but they fail
   * differently in a spreadsheet that surprises us: the CSV forces text with the
   * `="…"` idiom, while the XML DECLARES `ss:Type="String"`, which is a
   * standards-level statement rather than a parser trick. The XML is the safer
   * one; CSV is the default because it is what every other tool can read.
   */
  exportExcelXml?: boolean;
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
  /**
   * ---- THE TWO ORDER BOOKS (T3). What is still OUTSTANDING as at a date.
   *
   * ⛔⛔ TWO NAMES AND NEVER ONE "Order Book" WITH A DIRECTION TOGGLE, AND THIS
   * IS AN AUTHORISATION FACT RATHER THAN A REPORTING ONE. The server serves
   * `report:sales-order-book` and `report:purchase-order-book` as two separate
   * names because the PDP decides on the report NAME and a query parameter is
   * INVISIBLE to it. One report with `?direction=` would make "the salesperson
   * may see our sales pipeline but not what we have committed to pay suppliers"
   * an unwritable rule, and the toggle would be reaching a resource the operator
   * may be denied — producing a 403 nobody can attribute. Migration 0073 writes
   * the refusals, and it writes `disallow display` AND `disallow print` for each:
   * a report that cannot be displayed but CAN be printed is a leak with a paper
   * trail.
   *
   * Both are POSITIONS AS AT A DATE, not flows, and FrameOverlays classifies
   * them so — an outstanding commitment either still stands on the date asked
   * about or it does not, and bounding it below would drop the orders placed
   * before the window while their deliveries stayed in it.
   */
  "Sales Order Book",
  "Purchase Order Book",
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

/**
 * Every member of {@link VoucherType}, as a runtime set.
 *
 * ⛔ IT IS A `Readonly<Record<VoucherType, true>>` AND NOT A `readonly
 * VoucherType[]` ON PURPOSE: the Record is EXHAUSTIVE — tsc refuses to compile
 * until a newly added member appears here — where an array typed as
 * `readonly VoucherType[]` would accept eleven of the twelve silently. A guard
 * that is quietly missing a class does not reject a bad value; it rejects a
 * GOOD one, which is the failure mode corpora/validate.py's own vocabulary
 * checks exist because of.
 *
 * ⚠️ The union above stays a hand-written `export type` rather than being derived
 * from these keys, because corpora/validate.py parses the union's members out of
 * this file textually (`_ts_union_members`) to cross-check KNOWN_VOUCHER_TYPES.
 * Rewriting it as `(typeof …)[number]` makes that parser return None and the
 * validator reports that it "has stopped checking" — a green gate that checks
 * nothing.
 */
const VOUCHER_TYPE_MEMBERS: Readonly<Record<VoucherType, true>> = {
  contra: true,
  payment: true,
  receipt: true,
  journal: true,
  sales: true,
  purchase: true,
  "credit-note": true,
  "debit-note": true,
  "delivery-note": true,
  "receipt-note": true,
  "stock-journal": true,
  "physical-stock": true,
};

/**
 * Whether an arbitrary string is a {@link VoucherType}.
 *
 * ⛔ THIS IS LOAD-BEARING, NOT DEFENSIVE. A voucher frame's `voucherType` is used
 * as a key into `VOUCHER_CONFIGS` — including by AppShell's `screenTitle`, which
 * reads `VOUCHER_CONFIGS[t].title` with no guard. An unvalidated slug reaching a
 * frame is therefore a crash of the whole chrome, not a wrong label.
 */
export function isVoucherType(s: string | undefined): s is VoucherType {
  return s !== undefined && Object.hasOwn(VOUCHER_TYPE_MEMBERS, s);
}

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
  | { kind: "exportScreen" } // Alt+E — export the rows on screen (CSV / Excel XML)
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
   * Alt+C on the TDS Challans screen — RECORD A DEPOSIT (T6).
   *
   * THE COMBO is Tally's documented create-inline key (RESEARCH.md:405, "Alt+C =
   * create a master on the fly from within a field"); the SCOPE is ours, and it
   * is scoped rather than claimed globally for two reasons. A challan is not a
   * master, so the global `createMaster` action would push a LEDGER-CREATE frame
   * over the form; and inside a report Alt+C is Tally's New Column
   * (RESEARCH.md:771), which this screen is not. Leaving the global binding in
   * place would be worse than an unbound key: it would navigate away from a
   * half-keyed counterfoil.
   */
  | { kind: "recordTDSChallan" }
  /**
   * Alt+C on the COST MASTERS screen — create a cost CATEGORY (an axis) or a cost
   * CENTRE (a position on one).
   *
   * THE COMBO is Tally's documented create-a-master-on-the-fly key
   * (RESEARCH.md:405), and it is the right one twice over: RESEARCH.md:525 says a
   * cost centre is created through "Alt+G > Create Master > Cost Centre", so these
   * ARE masters in Tally's own vocabulary.
   *
   * THE SCOPE is ours. The global `createMaster` action carries a
   * {@link MasterKind}, and cost masters are deliberately NOT members of that
   * union: MasterCreateScreen renders a ledger/group/unit/stock-item form, so the
   * global binding would push a LEDGER-CREATE frame over this screen. Leaving it
   * in place would be worse than an unbound key — it would navigate away from a
   * half-keyed centre.
   *
   * ⚠️ ONE KEY CREATES BOTH, and that is a decision. RESEARCH pins no second
   * combo for a cost CATEGORY, and inventing one would be inventing a keystroke;
   * so the panel Alt+C opens asks WHICH of the two is being created, with the axis
   * for a centre PICKED from the served list rather than typed.
   */
  | { kind: "createCostMaster" }
  /**
   * F4 on the COST CENTRE SUMMARY — choose the AXIS (cost category) the report is
   * for.
   *
   * ⚠️ THE COMBO IS OURS, AND IS SAID SO OUT LOUD. RESEARCH.md:810–811 documents
   * how Tally reaches these reports ("Display More Reports > Statements of
   * Accounts > Cost Centres", where the cost centre / category is chosen from a
   * pick-list on the way in) but pins NO key for re-choosing it. So F4 is not
   * presented as a documented TallyPrime binding: it is placed at F4 for
   * consistency with the one picker this product already has — RESEARCH.md:879's
   * "F4 (select bank ledger)" on the reconciliation screen, which is the same
   * gesture ("pick the thing this report is about"). It is scoped to this screen
   * and never claimed globally, exactly as `editLog`'s Alt+L is.
   *
   * BINDING IT IS ALSO PROTECTIVE: the global F4 is a CONTRA VOUCHER, so an
   * operator reaching for the picker they used a moment ago on the reconciliation
   * screen would otherwise open a voucher over the report.
   *
   * There is deliberately no such key on the Cost CATEGORY Summary — that report
   * IS the list of axes, and Enter on a row opens that axis here.
   */
  | { kind: "selectCostCategory" }
  /**
   * Ctrl+F8 / Ctrl+F9 — THE ORDER REGISTERS (T3). `direction` is what decides
   * which, and it is on the ACTION rather than being read off the active screen
   * because these are two authorities, not one screen in two states — see the
   * "sales-orders" note on {@link ScreenKind}.
   *
   * ⚠️ THE COMBOS ARE DOCUMENTED TallyPrime, and they are the two keys ERP 9 used
   * for Credit and Debit Note — Tally itself re-mapped them onto the freed
   * Ctrl+F8/F9 slots when it added order vouchers (RESEARCH.md:400 "Ctrl+F8 Sales
   * Order; Ctrl+F9 Purchase Order (orders are TallyPrime additions)", the voucher
   * table at :614/:616, and the migration table at :2145-2146 which marks both as
   * CHANGED). RESEARCH.md:2181 names this as "the single most user-visible
   * regression risk": an ERP 9 migrant pressing Ctrl+F8 in Prime gets a Sales
   * Order and not a Credit Note. This profile is the PRIME profile, so it
   * replicates Prime's mapping — and Credit/Debit Note keep Prime's Alt+F6 /
   * Alt+F5, which are bound to the real note screens.
   *
   * Both combos were `unavailable` ("Order processing is not built yet") until the
   * screens existed. They are real actions now, which is what buttons.test.ts's
   * label assertion is checking.
   */
  | { kind: "orderRegister"; direction: "sales" | "purchase" }
  /**
   * Alt+Z — THE e-WAY BILL (T5).
   *
   * TallyPrime puts e-way bill actions under its "Z: Exchange" menu
   * (RESEARCH.md:361) and raises the bill FROM the sales voucher
   * (RESEARCH.md:284). This build has no Exchange MENU, so Alt+Z goes straight
   * to the one thing that menu would offer here.
   *
   * ⛔ IT IS A SCREEN AND NOT A VOUCHER KEY. A bill NAMES a voucher, so the
   * voucher must already exist — at the moment an operator is keying one it has
   * no id yet. Tally raises the bill after the voucher is accepted, and so does
   * this.
   */
  | { kind: "eWayBill" }
  /**
   * Alt+I — THE e-INVOICE / IRN SCREEN.
   *
   * TallyPrime files e-invoice actions under its "Z: Exchange" top menu
   * (RESEARCH.md:698, "Alt+Z (Exchange) > Send for e-Invoicing") — the SAME menu
   * Alt+Z was bound for on T5's behalf. That menu therefore now has TWO
   * destinations here and this build still has no Exchange MENU, so rather than
   * re-purposing a browser-verified key into a picker, the second destination
   * gets its own combo and Alt+Z's comment names it.
   *
   * ⚠️ Alt+I WAS NOT PREVIOUSLY BOUND TO `unavailable` OR TO ANYTHING ELSE, in
   * the global table or in any screen table — checked, because §7.3 is explicit
   * that the key you want is often already bound with a claim that has rotted.
   *
   * ⛔ IT IS A SCREEN AND NOT A VOUCHER KEY, for the reason eWayBill states and
   * one more: RESEARCH.md:698 records that TallyPrime 4.x MOVED IRN operations
   * out of the voucher into an e-Invoice REPORT with a "Pending for IRN
   * generation" list. Tally arrived at this shape from the other direction.
   */
  | { kind: "eInvoice" }
  /**
   * Alt+C on an ORDER REGISTER — record a new order (T3).
   *
   * THE COMBO is Tally's documented create-inline key (RESEARCH.md:405), scoped
   * for the same reason `recordTDSChallan` and `createCostMaster` are: an order is
   * not a {@link MasterKind}, so the global `createMaster` would push a
   * LEDGER-CREATE frame over a half-keyed order and take the lines with it.
   *
   * ⚠️ IT CARRIES NO DIRECTION. The screen it fires on already is one direction —
   * that is the whole point of there being two — so a direction here would be a
   * second source of truth for it, and the two would part company the moment a
   * screen was reached by a route the key did not set.
   */
  | { kind: "createOrder" }
  /**
   * Alt+W on an ORDER REGISTER — PRE-CLOSE the highlighted order (T3).
   *
   * THE COMBO IS DOCUMENTED: RESEARCH.md:622 — "Alt+W pre-closes outstanding
   * orders with reason/date". It is one of the few order keys RESEARCH pins
   * exactly, so it is used rather than invented.
   *
   * ⛔ PRE-CLOSING IS ABANDONMENT ("the customer cancelled"), NOT "mark it
   * delivered", and the label says so. Nothing can compute it — an order nobody
   * ever delivered and one that was cancelled have identical fulfilment histories,
   * which is exactly why `is_closed` is the one order field that is STORED while
   * `fulfilled_qty` is derived on every read. The reason is REQUIRED, because a
   * closed order that does not say why is a commitment that vanished.
   */
  | { kind: "closeOrder" }
  /**
   * Alt+C on the BILLS OF MATERIALS screen — define a recipe (T3).
   *
   * Same combo, same scoping argument as `createOrder`. A BOM genuinely IS a
   * master in Tally's vocabulary (it is defined on the stock item's own master,
   * RESEARCH.md:626), which is why the create key is the create-master key — but
   * it is still not a {@link MasterKind} here, because MasterCreateScreen renders
   * a ledger/group/unit/stock-item form and knows nothing about components.
   */
  | { kind: "createBom" }
  /**
   * Alt+X on the BILLS OF MATERIALS screen — EXPLODE the highlighted recipe for a
   * wanted quantity (T3).
   *
   * ⚠️ THE COMBO IS OURS AND IS SAID SO OUT LOUD. RESEARCH.md:628 documents how
   * Tally reaches the same arithmetic — a Manufacturing Journal, where "components
   * auto-fill (BoM qty × finished qty)" — but pins NO key for exploding a recipe
   * on its own, because in Tally there is nothing to press: the explosion only
   * ever happens inside that voucher. Alt+X is chosen because the global Alt+X is
   * `cancelVoucher`, which is meaningless on a master list, so scoping it here
   * takes nothing away from an operator; it is NOT presented as documented Tally.
   *
   * ⛔ THE EXPLOSION IS A PREVIEW AND MOVES NOTHING. It is what a Stock Journal's
   * lines would be, and it is SINGLE-LEVEL: a component that itself has a recipe
   * is not exploded recursively (Tally's own default, and what keeps
   * work-in-progress visible as stock instead of vanishing inside one entry).
   */
  | { kind: "explodeBom" }
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
