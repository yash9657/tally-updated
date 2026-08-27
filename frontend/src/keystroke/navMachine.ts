import { assign, setup } from "xstate";
import { LEDGER_WISE_DEFAULT, REPORT_NAMES } from "./types";
import type {
  EntryMode,
  FrameConfig,
  FrameOverlay,
  KeymapAction,
  MasterKind,
  ScreenKind,
  VoucherMode,
  VoucherType,
} from "./types";
import { resolveAction, resolveKeymap } from "./keymaps/tallyprime";
import { parseVoucherRowKey } from "./voucherClass";

/**
 * navMachine — a deterministic XState v5 model of Tally's SCREEN STACK
 * (PLAN.md B.1, RESEARCH.md sec 4).
 *
 * The load-bearing semantics:
 *  - Enter   drills down / advances the field cursor (context-scoped).
 *  - Esc     pops one frame off the stack.
 *  - Ctrl+A  accepts/saves the active voucher and re-opens a fresh form.
 *  - F-keys  switch the voucher type (replacing the active voucher frame).
 *  - Alt+G   opens a `goto-overlay` child that PRESERVES the prior screen and
 *            returns to it on Esc (Go To never destroys the parent).
 *  - Ctrl+G  (Switch To) REPLACES the current context — Esc cannot return to it.
 *  - Ctrl+H  opens the Change Mode picker (in vouchers).
 *
 * We hold the screen stack in `context` (an explicit array of frames) and route
 * every key through the active frame's resolver. This keeps the stack
 * semantics — the exact thing a CA notices when wrong — declarative and
 * unit-testable rather than emergent from scattered handlers.
 */

/** Ordered field cursor inside a voucher form (RESEARCH.md sec 4). */
export const VOUCHER_FIELDS = [
  "date",
  "party-ledger",
  "ledger",
  "amount",
  "narration",
] as const;
export type VoucherField = (typeof VOUCHER_FIELDS)[number];

/** Field used while the Go To overlay is open. */
export type OverlayField = "goto-search";

/** Ordered field cursor inside a master-create screen (default: ledger). */
export const MASTER_FIELDS = ["name", "group", "details"] as const;
export type MasterField = (typeof MASTER_FIELDS)[number];

/**
 * Reports whose rows are individual vouchers (Enter drills straight into the
 * underlying voucher in alteration mode). The Day Book is the canonical
 * example. Statement reports (Balance Sheet, P&L, Trial Balance) instead drill
 * into a report-detail level first.
 */
const VOUCHER_DRILL_REPORTS = new Set<string>(["Day Book"]);

/**
 * Aliases mapping common Gateway / Go To labels to the canonical Phase 1 report
 * names. Keyed by lower-cased label. Returns undefined for a non-report entry
 * (so the caller can fall through, e.g. "Vouchers").
 */
const REPORT_ALIASES: Readonly<Record<string, string>> = {
  "balance sheet": "Balance Sheet",
  "profit & loss a/c": "Profit & Loss A/c",
  "profit and loss a/c": "Profit & Loss A/c",
  "profit & loss": "Profit & Loss A/c",
  "profit and loss": "Profit & Loss A/c",
  "p&l": "Profit & Loss A/c",
  "trial balance": "Trial Balance",
  "day book": "Day Book",
  daybook: "Day Book",
  outstandings: "Outstandings",
  "bills outstanding": "Outstandings",
  "bills receivable": "Outstandings",
  // ⛔⛔ THIS ALIAS POINTED AT "Outstandings" — THE **RECEIVABLE** REPORT — AND HAD
  // SHIPPED THAT WAY. An operator typing the words for what the company OWES was
  // shown what it is OWED, under a heading that did not say so: the same class of
  // wrong answer as an alias that silently answers a different question, which L16
  // deliberately refused to add for "cash flow statement" (§6.5). It could not be
  // fixed before D94, because there was no payables report to point it at.
  "bills payable": "Bills Payable",
  payables: "Bills Payable",
  "accounts payable": "Bills Payable",
  "creditors ageing": "Bills Payable",
  // The register answers to the words an operator actually types. "tds" alone
  // is the common one; the rest are what a Tally user calls the same screen.
  "tds / tcs register": "TDS / TCS Register",
  "tds register": "TDS / TCS Register",
  "tcs register": "TDS / TCS Register",
  tds: "TDS / TCS Register",
  tcs: "TDS / TCS Register",
  // The forecast plane. Spelling variants only, and note what is NOT here.
  cashflow: "Cash Flow",
  "cash-flow": "Cash Flow",
  "cash flow projection": "Cash Flow",
  anomalies: "Anomalies",
  anomaly: "Anomalies",
  "anomaly detection": "Anomalies",
  // ⛔ "cash flow statement" AND "funds flow" ARE DELIBERATELY ABSENT, AND THAT
  // IS NOT AN OVERSIGHT. In Tally those name a HISTORIC statement of cash that
  // actually moved in a period. This report is a FORWARD PROJECTION of when open
  // bills are guessed to settle — a different question with a different answer,
  // and every date in it is invented. An operator who typed "cash flow statement"
  // and landed here would read projected figures as recorded ones, which is
  // exactly the confusion the screen's whole disclosure block exists to prevent.
  // An alias that silently answers a different question is worse than no alias:
  // the picker leaves the entry unmatched and the operator looks again.
};

/**
 * Resolve a selected menu/picker label to a canonical Phase 1 report name.
 * Falls back to the raw label for any unrecognised report-shaped entry, and to
 * undefined for the "Vouchers" entry and other non-report drills.
 */
function canonicalReportName(label: string | undefined): string | undefined {
  if (!label) return undefined;
  const key = label.trim().toLowerCase();
  if (key === "vouchers") return undefined;
  return REPORT_ALIASES[key] ?? label;
}

/** How a Go To-style overlay resolves when its selection is confirmed. */
export type GotoMode = "goto" | "switchTo";

/**
 * ⛔⛔ THE MASTER-CREATE ENTRIES OF THE Alt+G / Ctrl+G PICKER — the door row 20
 * proved did not exist anywhere in the build.
 *
 * DRIVEN IN A BROWSER 2026-08-07, and this is what it found: `Alt+C` renders
 * "Create ledger" from the Gateway AND from the item-invoice Item cell; the
 * Gateway menu carries no masters entry; and this picker offered exactly
 * `["Vouchers", ...REPORT_NAMES]`. So NO keystroke in the product reached
 * `masterKind` "stock-item" or "unit" — **a company could never be given a stock
 * item or a unit from the keyboard at all** — and MasterCreateScreen's two arms
 * were dead code that every unit test exercised by calling the component
 * directly.
 *
 * ⛔ THE ROUTE IS DOCUMENTED, NOT INVENTED. RESEARCH.md:568: *"All are reached
 * via Gateway of Tally > Create (single) or via **Alt+G (Go To) > Create Master /
 * Alter Master**, and during any voucher/master you can create a sub-master on
 * the fly with Alt+C."* A Go To entry is the cheapest faithful door, and it needs
 * NO new ScreenKind: `master-create` is already carried by all fifteen
 * registration points (HANDOFF.md §7.2), including `corpora/validate.py`'s
 * `KNOWN_SCREENS` outside `frontend/` — which is why this fix touches neither.
 *
 * ⚠️ AND THE KEYMAP IS THE WRONG PLACE TO START, which is why `Alt+C` is
 * deliberately left alone above. `resolveAction` is keyed on `ScreenKind` ONLY,
 * and `VOUCHER_FIELDS` is `["date","party-ledger","ledger","amount","narration"]`
 * — the machine has no notion of the item grid, so a field-scoped `Alt+C` has
 * nothing to key on today. Alt+C stays "create a LEDGER inline, on the fly";
 * these five entries are how every kind, including ledger, is reached
 * STANDALONE.
 *
 * ⚠️ THE LABELS ARE Tally's OWN `Gateway > Create <X>` NAMES, because the picker
 * is a substring type-ahead: typing "create" narrows to all five, typing "stock"
 * reaches the stock item, and typing "unit" reaches the unit. A label of the form
 * "Stock Item" alone would be indistinguishable from a report name to a reader of
 * the list, which is the confusion the "Sales Orders" / "Sales Order Book" pair
 * already costs this build once.
 */
export const MASTER_GOTO_ENTRIES: Readonly<Record<string, MasterKind>> = {
  "Create Ledger": "ledger",
  "Create Group": "group",
  "Create Stock Item": "stock-item",
  // T10. Listed AFTER "Create Stock Item" so typing "stock" offers the item
  // first — the master an operator reaches for forty times a day — and the group
  // second.
  "Create Stock Group": "stock-group",
  "Create Unit": "unit",
  "Create Voucher Type": "voucher-type",
};

/**
 * Everything the Alt+G / Ctrl+G picker offers, in the order it offers it.
 *
 * ⛔ IT LIVES HERE, NOT IN THE PICKER COMPONENT, for the reason every
 * `*_LABEL` above lives here: the MACHINE owns what a selection DOES, so the
 * machine must own the list of what can be selected. A component-local list is
 * how the five master entries could be added to the picker while
 * `resolveGotoSelection` still routed them to `{screen:"report"}` — a Go To entry
 * that lands on "not available in this build" is worse than an absent one.
 */
export const GOTO_ENTRIES: readonly string[] = [
  "Vouchers",
  ...Object.keys(MASTER_GOTO_ENTRIES),
  ...REPORT_NAMES,
];

/** Case-insensitive index of {@link MASTER_GOTO_ENTRIES}, built once. */
const MASTER_GOTO_BY_LABEL: ReadonlyMap<string, MasterKind> = new Map(
  Object.entries(MASTER_GOTO_ENTRIES).map(([label, kind]) => [
    label.toLowerCase(),
    kind,
  ]),
);

/**
 * The {@link MasterKind} a Go To label creates, or undefined for anything else.
 * Matched case-insensitively, exactly as every Gateway label above is.
 */
export function masterKindForGotoLabel(
  label: string | undefined,
): MasterKind | undefined {
  if (!label) return undefined;
  return MASTER_GOTO_BY_LABEL.get(label.trim().toLowerCase());
}

/**
 * The Gateway entry label that drills into the F9 Bill Inbox. Matched
 * case-insensitively in the gateway Enter/SELECT branches.
 */
export const BILL_INBOX_LABEL = "Bill Inbox";

/**
 * The Gateway entry label that drills into the "Ask your books" query screen.
 * Matched case-insensitively in the gateway Enter/SELECT branches. Like
 * {@link BILL_INBOX_LABEL} it is NEITHER "Vouchers" nor a report name, so it
 * needs its own highlightable branch (and an Enter branch placed BEFORE the
 * canonicalReportName fallback, else it would be mistaken for a report name).
 */
export const QUERY_LABEL = "Query";

/**
 * Pinned sentinel the {@link BillInboxScreen} feeds through `onSelect` (a SELECT
 * event) AFTER it has set the LoadedDraft context. reduceSelect treats this as
 * "open the chosen draft in the F9 Purchase voucher": it pushes a purchase
 * create frame IMMEDIATELY (no follow-up Enter), exactly mirroring the F9 push.
 * The draft id is NEVER smuggled through this sentinel — the screen resolves the
 * draft and calls setLoaded({draft}) before firing it, and VoucherScreen reads
 * the draft from the LoadedDraft context (isDraftMode).
 */
export const OPEN_PURCHASE_DRAFT_SENTINEL = "__open-purchase-draft__";

/**
 * The Gateway entry that opens the second-level report menu (Tally's "Display
 * More Reports"), which is where the Trial Balance and Outstandings live.
 */
export const MORE_REPORTS_LABEL = "Display More Reports";

/**
 * The Gateway entry that opens Users & Roles (the browser half of the admin
 * API: members, pending invites and security levels). Like BILL_INBOX_LABEL it
 * is neither "Vouchers" nor a report name, so it needs its own branch placed
 * BEFORE the canonicalReportName fallback — otherwise it would push a report
 * frame literally named "Users & Roles".
 */
export const USERS_ROLES_LABEL = "Users & Roles";

/**
 * The Gateway entry that opens BANK RECONCILIATION. Same ordering requirement as
 * BILL_INBOX_LABEL — it must be matched BEFORE the canonicalReportName fallback,
 * or it pushes a report frame literally named "Bank Reconciliation".
 *
 * That trap is not hypothetical here: "bank-reconciliation" IS a real name in
 * authz.KnownReportNames (migration 0046 needs it so a security level can be
 * refused the company's bank position), so a fallback-matched frame would look
 * plausible all the way to a 501 from the print route.
 *
 * Reached this way the screen opens in PICKER mode (choose a bank account);
 * reached by F5 from a ledger drill it opens straight on that ledger.
 */
export const BANK_RECON_LABEL = "Bank Reconciliation";

/**
 * The Gateway entry that opens the TDS CHALLAN register (T6).
 *
 * Same ordering requirement as BILL_INBOX_LABEL and BANK_RECON_LABEL: it MUST be
 * matched BEFORE the canonicalReportName fallback, which would otherwise push a
 * report frame literally named "TDS Challans". That mistake would look plausible
 * for a long time here, because "TDS / TCS Register" IS a real report name and
 * REPORT_ALIASES already maps four spellings of "tds" onto it.
 */
export const TDS_CHALLANS_LABEL = "TDS Challans";

/**
 * The Gateway entry that opens the MIGRATE-FROM-TALLY import screen (T32).
 *
 * Tally's own path is `Gateway of Tally > Import` (RESEARCH.md:1221), so this
 * door is the documented one and not a convenience. The screen also has Tally's
 * documented KEY (Alt+O), and it has both because Tally has both — unlike the
 * Data screen (Alt+Y), which deliberately has no Gateway entry because Alt+Y is
 * global in Tally too.
 *
 * ⚠️ Alt+G Go To IS NOT A THIRD DOOR AND CANNOT BE MADE ONE HERE. That picker is
 * hard-wired to ["Vouchers", ...REPORT_NAMES], so every screen that is not a
 * report name is absent from it — which is exactly why the Gateway entry is
 * needed rather than optional.
 *
 * Same ordering requirement as every label above: it MUST be matched BEFORE the
 * canonicalReportName fallback. The trap is milder than Bank Reconciliation's
 * ("Import from Tally" is not a report name) but the fallback would still push a
 * report frame with that literal name, which renders as a screen politely
 * announcing that a built, reachable importer is "not available in this build".
 */
export const TALLY_IMPORT_LABEL = "Import from Tally";

/**
 * The Gateway entry that opens the RECONCILIATION QUEUE (track D).
 *
 * ⛔ IT MUST BE MATCHED BEFORE THE canonicalReportName FALLBACK, or selecting it
 * pushes a `report` frame of that literal name, which renders as a screen
 * politely announcing that a built, reachable queue is "not available in this
 * build".
 *
 * ⛔⛔ AND IT IS "Reconciliation QUEUE" RATHER THAN "Reconciliation" BECAUSE THE
 * SHORT NAME WAS A COLLISION, FOUND IN A REAL BROWSER. The Gateway is reached by
 * typing into its search box, and "Bank Reconciliation" — a screen that has
 * shipped for as long as T4 has — CONTAINS the word. Typing "Reconciliation" and
 * pressing Enter opened the BANK screen, every time. Nothing errored: the
 * operator simply arrived somewhere else, at a screen that says "this company has
 * no bank accounts", which reads as an empty queue rather than as a wrong turn.
 *
 * ⚠️ THE DRIVE'S FIRST VERSION OF THIS PROBE COULD NOT SEE IT EITHER: it asserted
 * /Reconciliation/i against the page, and "Bank Reconciliation" matches that
 * happily. An oracle that cannot see the defect is not an oracle (HANDOFF rule
 * 4); the probe now asserts it is NOT the bank screen.
 */
export const RECONCILIATION_LABEL = "Reconciliation Queue";

/**
 * The Gateway entry that opens the PRINT TEMPLATES screen (track C).
 *
 * ⛔ IT MUST BE MATCHED BEFORE THE canonicalReportName FALLBACK, which falls back
 * to the RAW LABEL for anything it does not recognise — so without the match
 * below this label pushes a `report` frame named "Print Templates", which renders
 * "not available in this build" over a screen that exists and works.
 *
 * ⚠️ IT IS NOT IN Alt+G GO TO AND CANNOT BE: that picker is generated from
 * ["Vouchers", ...REPORT_NAMES], and this is not a report name. The Gateway entry
 * and the F11 Company Features row are its two doors.
 */
export const PRINT_TEMPLATES_LABEL = "Print Templates";

/**
 * The Gateway entries that open the three T2 COST screens.
 *
 * ⛔ SAME ORDERING REQUIREMENT AS EVERY LABEL ABOVE, and it bites harder here
 * than anywhere else: all three of these strings are report-SHAPED, and two of
 * them ARE real report names on the server (`report:cost-centre-summary` and
 * `report:cost-category-summary` are in authz.KnownReportNames, refused to the
 * seeded Data Entry level by migration 0072). So a frame pushed by the
 * canonicalReportName fallback would be a `report` frame named "Cost Centre
 * Summary" — which resolves to the UnwiredReportScreen, i.e. a screen politely
 * announcing that a built, ACL-gated report is "not available in this build".
 *
 * WHY THE GATEWAY AND NOT "Display More Reports": Tally files these under Display
 * More Reports > Statements of Accounts > Cost Centres (RESEARCH.md:810), but this
 * build's second-level menu is generated from REPORT_NAMES — which these three
 * deliberately are not, because none of them is reachable as a reportName frame.
 * A Gateway entry is the same door, and it is the door Bank Reconciliation and the
 * TDS Challan register already use.
 */
export const COST_CENTRES_LABEL = "Cost Centres";
export const COST_CENTRE_SUMMARY_LABEL = "Cost Centre Summary";
export const COST_CATEGORY_SUMMARY_LABEL = "Cost Category Summary";

/**
 * The prefix the Cost CATEGORY Summary puts on the row keys it publishes through
 * `onSelect`, so Enter can open THAT axis's Cost Centre Summary — "no report is a
 * dead end" (RESEARCH sec 8), applied to the one drill this pair of reports has.
 *
 * ⚠️ IT CARRIES THE AXIS'S NAME AND NEVER ITS ID. The machine stays pure: it has
 * no database, so it cannot know a cost_category_id, and smuggling one through a
 * frame would be the first step to a screen asking an operator for a UUID. The
 * Cost Centre Summary resolves the name against the SERVED list of axes, exactly
 * as the Bank Reconciliation screen resolves a drilled ledger name.
 */
export const COST_CATEGORY_ROW_PREFIX = "cost-category:";

/**
 * The Gateway entries that open the two ORDER REGISTERS and the BOM screen (T3).
 *
 * ⛔ SAME ORDERING REQUIREMENT AS EVERY LABEL ABOVE — and note that these three
 * strings are the SAFEST of the set with respect to the report fallback (none of
 * them is a report name or an alias of one), which is exactly why the ordering has
 * to be kept anyway: the two names that ARE report-shaped and adjacent to these,
 * "Sales Order Book" and "Purchase Order Book", genuinely ARE report names and are
 * reached as `report` frames on purpose. The pair that must not be confused is
 * REGISTER (a list of orders, a ScreenKind, reached by these labels and by
 * Ctrl+F8/F9) versus BOOK (what is outstanding as at a date, a report name). One
 * is where an order is created; the other is where the commitments are read.
 */
/**
 * The Gateway entries that open the FOUR PAYROLL screens (Track 3).
 *
 * ⛔ SAME ORDERING REQUIREMENT AS EVERY LABEL ABOVE, and two of these are the
 * most dangerous strings in the whole set: "Payroll Register" and "Payslip" are
 * report-SHAPED, and `payroll-register` and `payroll-payslip` genuinely ARE names
 * in authz.KnownReportNames (migration 0035 refuses all five payroll reports to
 * the seeded Data Entry level, which needs a name to refuse). So a frame pushed
 * by the canonicalReportName fallback would be a `report` frame named "Payroll
 * Register" — resolving to UnwiredReportScreen, i.e. a screen politely announcing
 * that a built, ACL-gated, PRINTABLE report is "not available in this build".
 *
 * WHY THE GATEWAY AND NOT "Display More Reports": that menu is generated from
 * REPORT_NAMES, and none of these four is reachable as a reportName frame. The
 * register's dimension is a wage MONTH and the payslip's is a month plus an
 * EMPLOYEE — neither is the period a `report` frame carries. Same door Bank
 * Reconciliation, the TDS challan register and the three cost screens use.
 *
 * ⚠️ "Employees" IS FIRST, and that ordering is the same argument "Cost Centres"
 * comes before the two cost reports: until an employee exists with a salary
 * structure and a statutory election, a pay run refuses outright and both reports
 * below can only say so.
 */
export const EMPLOYEES_LABEL = "Employees";
export const PAY_RUN_LABEL = "Pay Run";
export const PAYROLL_REGISTER_LABEL = "Payroll Register";
export const FORM_138_LABEL = "Form 138 (Salary TDS)";

/**
 * The prefix the PAYROLL REGISTER puts on the row keys it publishes through
 * `onSelect`, so Enter opens THAT employee's payslip — "no report is a dead end"
 * (RESEARCH sec 8), applied to the one drill this report has.
 *
 * ⚠️ IT CARRIES THE EMPLOYEE'S CODE AND NEVER THEIR ID. The machine stays pure:
 * it has no database, so it cannot know an employee uuid, and smuggling one
 * through a frame would be the first step to a screen asking an operator for a
 * UUID. The payslip screen resolves the code against the SERVED register,
 * exactly as the Bank Reconciliation screen resolves a drilled ledger name and
 * the Cost Centre Summary resolves an axis name.
 *
 * ⛔ THE CODE AND NOT THE NAME, which is the one place this differs from those
 * two. `employee.code` is UNIQUE in the company (the server refuses a duplicate);
 * a NAME is not, and two people called the same thing would make the drill open
 * whichever the resolver met first — one person's whole pay under another
 * person's heading.
 */
export const EMPLOYEE_ROW_PREFIX = "employee:";

export const SALES_ORDERS_LABEL = "Sales Orders";
export const PURCHASE_ORDERS_LABEL = "Purchase Orders";
export const BOMS_LABEL = "Bills of Materials";

/**
 * The Ctrl+H Change Mode labels the machine acts on. Kept here (not in the
 * screen) because the MACHINE owns what a selection does; the screen owns which
 * options a voucher class offers. Any other label — including the "not built"
 * ones — simply dismisses the overlay.
 *
 * The machine does NOT check that the chosen mode makes sense for the active
 * class: a class only ever OFFERS the modes it supports, and
 * voucherConfig.effectiveEntryMode clamps anything else on the way out. Putting
 * the per-class table here instead would make the pure machine import from
 * screens/, inverting the layering.
 */
const ENTRY_MODE_LABELS: Readonly<Record<string, EntryMode>> = {
  "Single Entry": "single",
  "Double Entry": "double",
  "Item Invoice": "item-invoice",
  "Accounting Invoice": "accounting-invoice",
};

/**
 * Pinned sentinel the CompanySelectScreen feeds through `onSelect` AFTER it has
 * re-scoped the session (setCompany) to the chosen company. The machine's only
 * job then is to LEAVE the picker — the company id is never smuggled through
 * this sentinel, exactly as with OPEN_PURCHASE_DRAFT_SENTINEL: the screen owns
 * the context write, the machine owns navigation.
 *
 * Why pop back to the ROOT rather than just popping one frame: the frames
 * underneath were rendered for the OLD company (a half-entered voucher, a drill
 * into last month's Day Book), and silently re-pointing them at a different set
 * of books is how a user posts an entry into the wrong company. Tally itself
 * returns you to the Gateway when you switch.
 */
export const COMPANY_SWITCHED_SENTINEL = "__company-switched__";

/**
 * Row-kind prefixes a report screen puts on the keys it publishes through
 * `onSelect`, so the machine knows HOW DEEP a drill goes without knowing
 * anything about accounting data:
 *
 *   "group:Sundry Debtors"  -> Enter opens another report-detail (its ledgers)
 *   "ledger:Acme Traders"   -> Enter opens the ledger's vouchers
 *   anything else           -> Enter opens the underlying voucher
 *
 * This is what makes "no report is a dead end" (RESEARCH sec 8) work with a
 * PURE machine: the drill depth is data on the event, not a hardcoded rule.
 */
export const GROUP_ROW_PREFIX = "group:";
export const LEDGER_ROW_PREFIX = "ledger:";

/** A single frame on Tally's screen stack. */
export interface ScreenFrame {
  readonly screen: ScreenKind;
  voucherType?: VoucherType;
  voucherMode?: VoucherMode;
  field?: VoucherField | OverlayField | MasterField;
  reportName?: string;
  /** For master-create frames: the kind of master being created. */
  masterKind?: MasterKind;
  /** For report-detail frames: the selected row of the parent report. */
  detailOf?: string;
  /** True once this voucher frame has been accepted at least once (Ctrl+A). */
  saved?: boolean;
  /**
   * Active in-frame overlay (the Ctrl+H Change Mode picker, F12 Configure, F2
   * Period, Ctrl+F Filter, Ctrl+N Calculator). An overlay PRESERVES its frame:
   * Esc closes the overlay and leaves you exactly where you were — it does NOT
   * pop the screen. Getting this wrong is instantly noticeable to a Tally user.
   */
  overlay?: FrameOverlay;
  /** Per-frame view configuration mutated by F12 / Ctrl+H / Alt+F1 / F5 / F2. */
  config?: FrameConfig;
  /** When true the frame was reached via Switch To and is NOT returnable-to. */
  replaced?: boolean;
  /** For goto-overlay frames: whether Enter does a Go To or a Switch To. */
  gotoMode?: GotoMode;
  /** For goto-overlay frames: the currently highlighted selection-list item. */
  selectedItem?: string;
}

export interface NavContext {
  /** The screen stack; index 0 is the root (gateway), last item is active. */
  stack: ScreenFrame[];
  keymapProfile: string;
  /**
   * The entry mode the operator last chose, PER VOUCHER TYPE.
   *
   * This cannot live on the frame. Ctrl+A accepts a voucher and re-opens a
   * fresh form for bulk entry, and a fresh frame carries no config — so an
   * operator keying a run of double-entry payments was silently reverted to
   * single entry after EVERY save. Switching F5 -> F6 -> F5 lost it too.
   * Tally remembers the choice per voucher type, so the map is keyed by type
   * rather than held as one global: choosing double entry for Payment says
   * nothing about how you want to key a Contra.
   *
   * Absent key = that type's class default (voucherConfig's `entry`).
   */
  entryModeByType: Partial<Record<VoucherType, EntryMode>>;
}

/** Events accepted by the machine. Mirrors corpus event shapes. */
export type NavEvent =
  | { type: "KEY"; combo: string }
  | { type: "SELECT"; menuItem: string }
  /**
   * A view-configuration patch for the ACTIVE frame, sent by the F12 Configure
   * / F2 Period / Ctrl+F Filter overlays when the user changes a setting. The
   * config lives on the frame so it survives re-render, is projected into the
   * snapshot every screen already reads, and stays unit-testable.
   */
  | { type: "CONFIG"; patch: FrameConfig };

/** Options used to construct an initial machine context. */
export interface NavMachineOptions {
  startScreen?: ScreenKind;
  keymapProfile?: string;
  /** Last-used voucher type, used when drilling Gateway > Vouchers. */
  lastVoucherType?: VoucherType;
}

const DEFAULT_LAST_VOUCHER: VoucherType = "sales";

function newVoucherFrame(
  voucherType: VoucherType,
  mode: VoucherMode = "create",
  /**
   * The remembered entry mode for this type, from NavContext.entryModeByType.
   * Omitted leaves the frame with no config, i.e. the class default — which is
   * what a frame built outside any context (rootFrame) must do.
   */
  entryMode?: EntryMode,
): ScreenFrame {
  return {
    screen: "voucher",
    voucherType,
    voucherMode: mode,
    field: "date",
    saved: false,
    ...(entryMode ? { config: { entryMode } } : {}),
  };
}

function rootFrame(screen: ScreenKind): ScreenFrame {
  if (screen === "voucher") return newVoucherFrame(DEFAULT_LAST_VOUCHER);
  return { screen };
}

/** Build the initial context for the machine from options. */
export function makeInitialContext(opts: NavMachineOptions = {}): NavContext {
  const startScreen = opts.startScreen ?? "gateway";
  return {
    stack: [rootFrame(startScreen)],
    keymapProfile: opts.keymapProfile ?? "tallyprime",
    entryModeByType: {},
  };
}

function top(stack: readonly ScreenFrame[]): ScreenFrame {
  // Stack always has at least the root frame.
  return stack[stack.length - 1] as ScreenFrame;
}

/** Advance the voucher field cursor; returns the next field (clamped). */
function nextVoucherField(
  field: VoucherField | OverlayField | MasterField | undefined,
): VoucherField {
  const idx = VOUCHER_FIELDS.indexOf(field as VoucherField);
  if (idx < 0) return "party-ledger"; // from date, advance to party-ledger
  const next = Math.min(idx + 1, VOUCHER_FIELDS.length - 1);
  return VOUCHER_FIELDS[next] as VoucherField;
}

/** Step the voucher field cursor back one (Shift+Tab); clamped at the first. */
function prevVoucherField(
  field: VoucherField | OverlayField | MasterField | undefined,
): VoucherField {
  const idx = VOUCHER_FIELDS.indexOf(field as VoucherField);
  if (idx <= 0) return VOUCHER_FIELDS[0] as VoucherField;
  return VOUCHER_FIELDS[idx - 1] as VoucherField;
}

/** Step the master-create field cursor back one (Shift+Tab); clamped. */
function prevMasterField(
  field: VoucherField | OverlayField | MasterField | undefined,
): MasterField {
  const idx = MASTER_FIELDS.indexOf(field as MasterField);
  if (idx <= 0) return MASTER_FIELDS[0] as MasterField;
  return MASTER_FIELDS[idx - 1] as MasterField;
}

/** Advance the master-create field cursor; returns the next field (clamped). */
function nextMasterField(
  field: VoucherField | OverlayField | MasterField | undefined,
): MasterField {
  const idx = MASTER_FIELDS.indexOf(field as MasterField);
  if (idx < 0) return "group"; // from name, advance to group
  const next = Math.min(idx + 1, MASTER_FIELDS.length - 1);
  return MASTER_FIELDS[next] as MasterField;
}

/**
 * Resolve a confirmed goto-overlay selection. A `goto` opens the chosen report
 * as a fresh screen rooted at the gateway (the overlay is dismissed). A
 * `switchTo` replaces the entire context with the chosen report rooted at the
 * gateway and marks it non-returnable. Both produce a depth-2 stack.
 */
function resolveGotoSelection(
  baseFrame: ScreenFrame,
  overlay: ScreenFrame,
): ScreenFrame[] {
  // ⛔ THE MASTER ENTRIES ARE MATCHED FIRST, AND THE ORDER IS LOAD-BEARING —
  // the same ordering rule every Gateway label in this file carries, for the
  // same reason. `canonicalReportName` FALLS BACK TO THE RAW LABEL for anything
  // it does not recognise, so "Create Stock Item" reaching the line below would
  // push `{screen:"report", reportName:"Create Stock Item"}` — a frame that
  // renders `UnwiredReportScreen`, i.e. a screen politely announcing that the
  // thing the operator just asked to create "is not available in this build".
  // That is exactly the shape of failure the three cost screens' labels were
  // written up to prevent.
  //
  // ⛔ AND "VOUCHERS" IS THE SAME CLAUSE (D80). It was the FIRST entry this
  // picker ever offered and the only one that never worked: `canonicalReportName`
  // returns undefined for it *on purpose* (see the `key === "vouchers"` line),
  // the `??` below then falls back to the RAW LABEL, and the operator lands on
  // `{screen:"report", reportName:"Vouchers"}` — `UnwiredReportScreen`, politely
  // announcing that vouchers are "not available in this build". The Gateway's own
  // drill has always done the right thing for the identical label; this arm
  // mirrors it, so the two doors land on the same frame.
  const masterKind = masterKindForGotoLabel(overlay.selectedItem);
  if (masterKind) {
    // `field: "name"` because the create form's cursor starts on the name, the
    // same way a fresh voucher frame starts on the date. Without it the screen
    // mounts with nothing focused and the operator's first keystroke goes
    // nowhere — a keystroke product's version of a dead screen.
    const create: ScreenFrame = {
      screen: "master-create",
      masterKind,
      field: "name",
    };
    if (overlay.gotoMode === "switchTo") create.replaced = true;
    return [baseFrame, create];
  }
  // ⛔ THE VOUCHER DOOR. `RESEARCH.md:903` names it: *"Alt+G > **Create
  // Voucher** > Ctrl+F4 (Payroll)"* — Tally's Alt+G voucher entry is a
  // CREATE-VOUCHER FORM, not the Day Book. Mirrors the gateway arm exactly
  // (`newVoucherFrame(DEFAULT_LAST_VOUCHER)`, no entry mode, so the class
  // default applies) because a Go To and a Gateway drill on one label must land
  // on one frame.
  //
  // ⛔⛔ `replaced` IS DELIBERATELY **NOT** SET HERE, AND THAT IS A DECISION.
  // Every other arm in this function sets it under `switchTo`, so its absence
  // would otherwise read as an oversight. The flag is read in exactly one place
  // — `canReturnToVoucher`, which asks whether any frame BELOW the top is a
  // voucher that a Switch To destroyed. A voucher frame this arm creates is not
  // a destroyed voucher; it is the one the operator just asked for. Marking it
  // `replaced` is invisible while it is the top frame, and becomes a LIE the
  // moment anything is pushed over it (Alt+C from a voucher field): the
  // projection would report `canReturnToVoucher: false` while Esc pops
  // unconditionally and really does return to it. Pinned in masterGoto.test.ts.
  if (overlay.selectedItem?.trim().toLowerCase() === "vouchers") {
    return [baseFrame, newVoucherFrame(DEFAULT_LAST_VOUCHER)];
  }
  const reportName =
    canonicalReportName(overlay.selectedItem) ?? overlay.selectedItem ?? "";
  const report: ScreenFrame = { screen: "report", reportName };
  if (overlay.gotoMode === "switchTo") report.replaced = true;
  return [baseFrame, report];
}

/**
 * Pure reducer for a KEY event. Returns a partial context (a new stack) — the
 * machine action just assigns the result. Keeping this pure makes the stack
 * semantics directly testable.
 */
function reduceKey(ctx: NavContext, combo: string): Partial<NavContext> {
  const keymap = resolveKeymap(ctx.keymapProfile);
  const activeScreen = top(ctx.stack).screen;
  const action: KeymapAction | undefined = resolveAction(
    keymap,
    activeScreen,
    combo,
  );
  if (!action) return {};

  const stack = ctx.stack.slice();
  const active = { ...top(stack) };

  /** Replace the active frame with `frame`. */
  const replaceActive = (frame: ScreenFrame): Partial<NavContext> => {
    stack[stack.length - 1] = frame;
    return { stack };
  };

  /** Merge a view-config patch into the active frame (F12 / Ctrl+H / Alt+F1). */
  const patchConfig = (patch: FrameConfig): Partial<NavContext> =>
    replaceActive({ ...active, config: { ...active.config, ...patch } });

  /** Open an in-frame overlay (Esc closes it, the frame survives). */
  const openOverlay = (overlay: FrameOverlay): Partial<NavContext> =>
    replaceActive({ ...active, overlay });

  /**
   * Push a screen unless it is already the active one (F1 twice = one Help).
   *
   * `extra` carries frame fields the destination needs (e.g. the drilled ledger
   * for bank reconciliation). It is compared as part of the "already active"
   * test: pressing F5 on a DIFFERENT ledger must open that ledger's statement,
   * not be swallowed as a repeat.
   */
  const pushOnce = (
    screen: ScreenKind,
    extra?: Partial<ScreenFrame>,
  ): Partial<NavContext> => {
    if (active.screen === screen) {
      const same =
        !extra ||
        Object.entries(extra).every(
          ([k, v]) => active[k as keyof ScreenFrame] === v,
        );
      if (same) return {};
    }
    stack.push({ screen, ...extra });
    return { stack };
  };

  const isReportScreen =
    active.screen === "report" || active.screen === "report-detail";

  switch (action.kind) {
    case "voucher": {
      // F-key switches voucher type. If the active frame is a voucher, replace
      // it in place; otherwise push a new voucher frame onto the stack.
      const frame = newVoucherFrame(
        action.voucherType,
        "create",
        ctx.entryModeByType[action.voucherType],
      );
      if (active.screen === "voucher") {
        stack[stack.length - 1] = frame;
      } else {
        stack.push(frame);
      }
      return { stack };
    }

    case "enter": {
      // Confirming ANY in-frame overlay closes it and leaves the frame intact
      // (Change Mode, Configure, Period, Filter, Calculator all behave this
      // way in Tally — the sub-screen accepts, the screen underneath stays).
      if (active.overlay) {
        const closed = { ...active };
        delete closed.overlay;
        return replaceActive(closed);
      }
      if (active.screen === "goto-overlay") {
        // Confirm the highlighted selection in the Go To / Switch To overlay.
        if (active.selectedItem) {
          return {
            stack: resolveGotoSelection(stack[0] as ScreenFrame, active),
          };
        }
        return {};
      }
      if (active.screen === "voucher") {
        // (An open overlay was already handled above, for every screen.)
        active.field = nextVoucherField(active.field);
        stack[stack.length - 1] = active;
        return { stack };
      }
      if (active.screen === "report") {
        // Universal drill. For a voucher-bearing report (e.g. Day Book) a row is
        // a voucher, so Enter drills straight into it in alteration mode. For a
        // statement report (Balance Sheet / P&L / Trial Balance) Enter first
        // drills into a report-detail level (a group/ledger expansion), from
        // which a further Enter reaches the voucher.
        if (VOUCHER_DRILL_REPORTS.has(active.reportName ?? "")) {
          // ⛔ THE CLASS COMES FROM THE ROW, and a row that does not carry one is
          // NOT drilled. This branch used to push
          // `newVoucherFrame(DEFAULT_LAST_VOUCHER, "alter")` unconditionally, and
          // both halves of that were wrong:
          //
          //  - the DEFAULT: every non-Sales voucher opened a SALES frame, and
          //    `acceptVoucher` routes the PUT by the frame's type, so drilling a
          //    Receipt and pressing Ctrl+A called `alterSalesVoucher` and
          //    RE-CLASSED the document. See keystroke/voucherClass.ts.
          //  - the UNCONDITIONAL: on an empty Day Book `DrillRows` publishes no
          //    row key at all, so Enter pushed an alteration frame with NO loaded
          //    voucher — and Ctrl+A on that POSTS a new Sales voucher from a frame
          //    titled "(Alteration)". A no-op is the same answer the statement
          //    drill below already gives an empty selection.
          const row = parseVoucherRowKey(active.selectedItem);
          if (!row) return {};
          stack.push(newVoucherFrame(row.voucherType, "alter"));
          return { stack };
        }
        // A statement drill needs a highlighted line to drill INTO. With none
        // (e.g. an empty Balance Sheet), Enter is a no-op — pushing a detail
        // frame for "nothing" would open a screen describing no account.
        if (!active.selectedItem) return {};
        const detail: ScreenFrame = {
          screen: "report-detail",
          reportName: active.reportName,
          detailOf: active.selectedItem,
        };
        stack.push(detail);
        return { stack };
      }
      if (active.screen === "report-detail") {
        // Drill one more level when the highlighted row is a GROUP or a LEDGER
        // (group > ledger > voucher, Tally's statement drill path); otherwise
        // the row IS a voucher, so open it in alteration mode.
        const sel = active.selectedItem ?? "";
        if (
          sel.startsWith(GROUP_ROW_PREFIX) ||
          sel.startsWith(LEDGER_ROW_PREFIX)
        ) {
          stack.push({
            screen: "report-detail",
            reportName: active.reportName,
            detailOf: sel,
          });
          return { stack };
        }
        // The row IS a voucher, and it names its own class — same rule, same
        // reason, as the Day Book branch above. A statement drill reaches this
        // level through group > ledger > voucher, so an unprefixed key here is a
        // voucher whose class has no entry screen; it is not drilled.
        const row = parseVoucherRowKey(sel);
        if (!row) return {};
        stack.push(newVoucherFrame(row.voucherType, "alter"));
        return { stack };
      }
      if (active.screen === "cost-category-summary") {
        // ENTER OPENS THE HIGHLIGHTED AXIS — the one drill this pair of reports
        // has, and the reason the Category Summary is not a dead end: it says
        // WHICH analysis is incomplete, and this is how you get from that to the
        // centres on it.
        //
        // The row key carries the axis's NAME (see COST_CATEGORY_ROW_PREFIX);
        // the screen resolves it against the served list. With no row highlighted
        // Enter is a no-op rather than a frame for "some axis" — the same rule the
        // statement reports follow for an empty selection.
        const sel = active.selectedItem ?? "";
        if (!sel.startsWith(COST_CATEGORY_ROW_PREFIX)) return {};
        // ⛔ THE PERIOD TRAVELS WITH THE DRILL. Pushing a bare frame discarded it,
        // so the PRIMARY path into the Cost Centre Summary — read "Department:
        // 20,000 unallocated", press Enter to see which centres — always landed on
        // "press F2 to set the period", and the operator had to re-key the range
        // they had just chosen. Worse, re-keying it slightly differently makes the
        // two reports disagree about the same axis with nothing on either page
        // saying why.
        //
        // ONLY the period is copied — not the whole config, and not by spreading
        // `active`. The rest of that frame (its selected row, its own detailOf, its
        // F12/Ctrl+F toggles) belongs to the report being drilled FROM, and carrying
        // any of it would make the pushed frame claim state it does not have.
        stack.push({
          screen: "cost-centre-summary",
          detailOf: sel,
          config: {
            periodFrom: active.config?.periodFrom,
            periodTo: active.config?.periodTo,
          },
        });
        return { stack };
      }
      if (active.screen === "payroll-register") {
        // ENTER OPENS THE HIGHLIGHTED EMPLOYEE'S PAYSLIP — the one drill the
        // register has, and the reason it is not a dead end: the register says
        // what each person was paid in total, and this is how you get from that
        // to WHICH COMPONENTS made it up and what was deducted.
        //
        // The row key carries the employee's CODE (see EMPLOYEE_ROW_PREFIX) and
        // never their id: the machine has no database. With no row highlighted
        // Enter is a no-op rather than a frame for "some employee" — the same
        // rule every statement report follows for an empty selection, and it
        // matters more here, because a payslip opened for the wrong person is one
        // individual's entire pay under another individual's name.
        const sel = active.selectedItem ?? "";
        if (!sel.startsWith(EMPLOYEE_ROW_PREFIX)) return {};
        // ⛔ THE WAGE MONTH TRAVELS WITH THE DRILL, for the reason the period
        // travels with the cost-category drill: a payslip frame without one lands
        // on "type a month", and the operator re-keys the month they just chose —
        // or, worse, re-keys it slightly differently and reads one person's July
        // payslip under a register for June with nothing on either page saying so.
        //
        // ONLY the month is copied, never the rest of the frame: the register's
        // own highlighted row and its toggles belong to the register.
        stack.push({
          screen: "payroll-payslip",
          detailOf: sel,
          config: { wageMonth: active.config?.wageMonth },
        });
        return { stack };
      }
      if (active.screen === "master-create") {
        // Enter advances the master-create field cursor.
        active.field = nextMasterField(active.field);
        stack[stack.length - 1] = active;
        return { stack };
      }
      if (active.screen === "gateway" || active.screen === "menu") {
        // Drill into the highlighted gateway entry. "Vouchers" opens the
        // last-used voucher type, defaulting to the date field.
        if (active.selectedItem?.toLowerCase() === "vouchers") {
          stack.push(newVoucherFrame(DEFAULT_LAST_VOUCHER));
          return { stack };
        }
        // "Bill Inbox" drills into the F9 Bill Inbox screen. This MUST be
        // checked BEFORE the canonicalReportName fallback below, which would
        // otherwise treat the unknown report-shaped label as a report name and
        // wrongly push a report frame called "Bill Inbox".
        if (
          active.selectedItem?.toLowerCase() === BILL_INBOX_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "bill-inbox" });
          return { stack };
        }
        // "Query" drills into the "Ask your books" query screen. Like Bill
        // Inbox this MUST be checked BEFORE the canonicalReportName fallback,
        // which would otherwise treat the label as an unknown report name and
        // wrongly push a report frame called "Query".
        if (active.selectedItem?.toLowerCase() === QUERY_LABEL.toLowerCase()) {
          stack.push({ screen: "query" });
          return { stack };
        }
        // "Users & Roles" drills into the members/security-levels screen. Same
        // ordering requirement as Bill Inbox: BEFORE the report fallback.
        if (
          active.selectedItem?.toLowerCase() === USERS_ROLES_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "members" });
          return { stack };
        }
        // "Bank Reconciliation" drills into the T4 reconciliation screen, in
        // PICKER mode (no ledger chosen yet). Ordering as above: this MUST come
        // before the canonicalReportName fallback, which would otherwise push a
        // plausible-looking report frame — "bank-reconciliation" really is a
        // KnownReportName, so the mistake would survive as far as a 501.
        if (
          active.selectedItem?.toLowerCase() === BANK_RECON_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "bank-reconciliation" });
          return { stack };
        }
        // "TDS Challans" drills into the T6 challan register. Ordering as
        // above: this MUST be checked BEFORE the canonicalReportName fallback,
        // which would otherwise push a report frame named "TDS Challans".
        if (
          active.selectedItem?.toLowerCase() ===
          TDS_CHALLANS_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "tds-challans" });
          return { stack };
        }
        // "Import from Tally" drills into the T32 migration import screen.
        // Ordering as above: BEFORE the canonicalReportName fallback, which
        // would otherwise push a report frame named "Import from Tally".
        if (
          active.selectedItem?.toLowerCase() ===
          TALLY_IMPORT_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "tally-import" });
          return { stack };
        }
        // "Reconciliation" drills into the refused-changes queue (track D).
        // Ordering as above, and it matters MORE here: the canonicalReportName
        // fallback would resolve this to something report-shaped rather than the
        // screen, because a bank-reconciliation report really does exist.
        if (
          active.selectedItem?.toLowerCase() ===
          RECONCILIATION_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "reconciliation" });
          return { stack };
        }
        // "Print Templates" drills into the per-company document layouts (track
        // C). Ordering as above: BEFORE the canonicalReportName fallback, which
        // would otherwise push a report frame named "Print Templates".
        //
        // ⛔ THIS IS ONE OF THE THREE POINTS THAT SHIP THE SCREEN DEAD. Without
        // it the label falls through to the fallback and the operator gets a
        // report frame that says the report is not available — over a screen that
        // is built.
        if (
          active.selectedItem?.toLowerCase() ===
          PRINT_TEMPLATES_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "print-templates" });
          return { stack };
        }
        // The three T2 COST screens. Ordering as above, and load-bearing: two of
        // these labels ARE report names on the server, so the fallback would push
        // a `report` frame that renders "not available in this build" over a
        // built, ACL-gated report. See COST_CENTRES_LABEL.
        if (
          active.selectedItem?.toLowerCase() ===
          COST_CENTRES_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "cost-centres" });
          return { stack };
        }
        if (
          active.selectedItem?.toLowerCase() ===
          COST_CENTRE_SUMMARY_LABEL.toLowerCase()
        ) {
          // No axis on the frame: the screen opens its PICKER (or resolves the
          // company's only axis), exactly as the reconciliation screen does with
          // bank accounts. The machine cannot choose an axis — it has no database.
          stack.push({ screen: "cost-centre-summary" });
          return { stack };
        }
        if (
          active.selectedItem?.toLowerCase() ===
          COST_CATEGORY_SUMMARY_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "cost-category-summary" });
          return { stack };
        }
        // The two ORDER REGISTERS and the BOM screen (T3). Ordering as above.
        // These are the screens Ctrl+F8 / Ctrl+F9 reach; the Gateway is the other
        // door, and it is the only door to the BOM screen (RESEARCH pins no key
        // for reaching a bill of materials — in Tally it is a page of the stock
        // item's own master).
        if (
          active.selectedItem?.toLowerCase() === SALES_ORDERS_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "sales-orders" });
          return { stack };
        }
        if (
          active.selectedItem?.toLowerCase() ===
          PURCHASE_ORDERS_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "purchase-orders" });
          return { stack };
        }
        // ---- THE FOUR PAYROLL SCREENS (Track 3). Ordering as above, and it is
        // load-bearing for two of them: "Payroll Register" and the payslip are
        // report-SHAPED and both ARE real report names on the server, so the
        // fallback would push a `report` frame that renders "not available in
        // this build" over a built, ACL-gated, printable report. See
        // EMPLOYEES_LABEL.
        if (
          active.selectedItem?.toLowerCase() === EMPLOYEES_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "employees" });
          return { stack };
        }
        if (active.selectedItem?.toLowerCase() === PAY_RUN_LABEL.toLowerCase()) {
          // The same frame Ctrl+F4 pushes. A Gateway drill and a key on one
          // destination must land on ONE frame, which is the rule the "Vouchers"
          // entry's own arm exists to keep.
          stack.push({ screen: "payroll-run" });
          return { stack };
        }
        if (
          active.selectedItem?.toLowerCase() ===
          PAYROLL_REGISTER_LABEL.toLowerCase()
        ) {
          // No month on the frame: the screen asks for it on its own face, the
          // way the TDS challan register asks for a tax year. The machine cannot
          // choose a wage month, and defaulting one would answer a question
          // nobody asked while looking like the one they did.
          stack.push({ screen: "payroll-register" });
          return { stack };
        }
        if (active.selectedItem?.toLowerCase() === FORM_138_LABEL.toLowerCase()) {
          stack.push({ screen: "payroll-form-138" });
          return { stack };
        }
        if (active.selectedItem?.toLowerCase() === BOMS_LABEL.toLowerCase()) {
          stack.push({ screen: "boms" });
          return { stack };
        }
        // "Display More Reports" opens the second-level report MENU (Tally's
        // own structure: the Trial Balance and Outstandings live one level in).
        // Checked before the report fallback, which would otherwise push a
        // report frame literally named "Display More Reports".
        if (
          active.selectedItem?.toLowerCase() ===
          MORE_REPORTS_LABEL.toLowerCase()
        ) {
          stack.push({ screen: "menu" });
          return { stack };
        }
        // A selected report entry drills into that report (depth +1), the same
        // destination the Alt+G Go To picker reaches.
        const report = canonicalReportName(active.selectedItem);
        if (report) {
          stack.push({ screen: "report", reportName: report });
          return { stack };
        }
        return {};
      }
      return {};
    }

    case "escape": {
      // An open overlay (Change Mode / Configure / Period / Filter /
      // Calculator) is what Esc cancels FIRST — the frame underneath survives.
      // Only when no overlay is open does Esc pop the screen stack.
      if (active.overlay) {
        const closed = { ...active };
        delete closed.overlay;
        return replaceActive(closed);
      }
      if (stack.length > 1) {
        stack.pop();
        return { stack };
      }
      return {}; // cannot pop past the root
    }

    case "quit": {
      // Ctrl+Q — quit the screen WITHOUT saving. Unlike Esc it abandons the
      // whole screen even when an overlay is open (Tally's "quit screen").
      if (stack.length > 1) {
        stack.pop();
        return { stack };
      }
      if (active.overlay) {
        const closed = { ...active };
        delete closed.overlay;
        return replaceActive(closed);
      }
      return {};
    }

    case "nextField": {
      // Tab — same field advance as Enter, but never drills/opens.
      if (active.screen === "voucher" && !active.overlay) {
        active.field = nextVoucherField(active.field);
        return replaceActive(active);
      }
      if (active.screen === "master-create") {
        active.field = nextMasterField(active.field);
        return replaceActive(active);
      }
      return {};
    }

    case "prevField": {
      // Shift+Tab — step the field cursor back one.
      if (active.screen === "voucher" && !active.overlay) {
        active.field = prevVoucherField(active.field);
        return replaceActive(active);
      }
      if (active.screen === "master-create") {
        active.field = prevMasterField(active.field);
        return replaceActive(active);
      }
      return {};
    }

    case "help": {
      return pushOnce("help");
    }

    case "features": {
      return pushOnce("features");
    }

    case "tallyImport": {
      // Alt+O — MIGRATE FROM TALLY (T32). pushOnce so pressing it twice does not
      // stack two copies, the same treatment Help, Features and the Data screen
      // get.
      //
      // ⛔ THIS CASE IS ONE OF THE THREE REGISTRATION POINTS THAT SHIP THE KEY
      // COMPLETELY DEAD WITH NO ERROR AND NO STATUS LINE (the keymap binding and
      // the screen-registry entry are the others). It is mutation-proved
      // separately in TallyImportScreen.test.tsx for that reason.
      //
      // ⚠️ IT IS REACHABLE FROM ANY SCREEN, and unlike the voucher keys it is NOT
      // held back on the create forms — Esc brings the operator straight back to
      // a half-keyed voucher, and an import is a thing decided rather than
      // navigated to. The narrower `importBankStatement` still owns Alt+O on the
      // reconciliation screen; resolveAction gives screen bindings priority, so
      // this global never reaches it.
      return pushOnce("tally-import");
    }

    case "reconciliationQueue": {
      // Alt+R — the refused-changes queue. pushOnce so pressing it twice does not
      // stack two frames, the same treatment every other reader screen gets.
      return pushOnce("reconciliation");
    }

    case "dataScreen": {
      // Alt+Y — DATA (backup / restore). pushOnce so pressing it twice does not
      // stack two of them, the same treatment Help, Features and the company
      // picker get.
      //
      // It is reachable from ANY screen, deliberately: a backup is a thing an
      // operator decides to take, not a thing they navigate to, and Tally's own
      // Alt+Y is global too. The voucher-key hold-back that guards the create
      // forms does not apply in reverse — leaving a half-keyed voucher to take a
      // backup is the operator's call, and Esc brings them straight back.
      return pushOnce("data");
    }

    case "switchCompany": {
      // F3 — the company picker. pushOnce so pressing F3 twice does not stack
      // two pickers (the same treatment Help and Features get).
      return pushOnce("company-select");
    }

    case "editLog": {
      // Alt+L on a voucher — its EDIT LOG (every hash-chained version plus a
      // verification of the chain).
      //
      // Only from a VOUCHER frame, and only in ALTERATION mode: a voucher being
      // entered has no history yet, and pushing an empty log would read as a
      // broken screen rather than as "there is nothing here". The machine stays
      // PURE — no voucher id travels through it. The screen reads the drilled
      // voucher from LoadedVoucher, exactly as the alter form already does.
      if (active.screen !== "voucher" || active.voucherMode !== "alter")
        return {};
      return pushOnce("edit-log");
    }

    case "configure": {
      return openOverlay("configure");
    }

    case "calculator": {
      return openOverlay("calculator");
    }

    case "period": {
      // F2 (report) / Alt+F2 — the From/To picker. Reports have a period, and so
      // does the Bank Reconciliation Statement: it is a position AS AT a date
      // (only the upper bound applies), and reconciling in arrears is the normal
      // case rather than an edge one.
      //
      // AND SO DO THE TWO COST SUMMARIES, which are ScreenKinds rather than
      // `report` frames: without them here F2 opens NOTHING on a screen whose
      // heading asks for a period, and both refuse to render without one. They are
      // FULL-period reports (a flow between two dates, not a position as at one) —
      // FrameOverlays' FULL_PERIOD_REPORTS says so, and periodClassification.test
      // keeps the panel's note and the screens' behaviour from drifting apart.
      if (
        isReportScreen ||
        active.screen === "bank-reconciliation" ||
        active.screen === "cost-centre-summary" ||
        active.screen === "cost-category-summary"
      ) {
        return openOverlay("period");
      }
      return {};
    }

    case "filter": {
      if (isReportScreen) return openOverlay("filter");
      return {};
    }

    case "detailed": {
      if (!isReportScreen) return {};
      return patchConfig({ detailed: !active.config?.detailed });
    }

    case "ledgerWise": {
      if (!isReportScreen) return {};
      return patchConfig({
        ledgerWise: !(active.config?.ledgerWise ?? LEDGER_WISE_DEFAULT),
      });
    }

    case "changeView": {
      if (!isReportScreen) return {};
      return patchConfig({ vertical: !active.config?.vertical });
    }

    case "cancelVoucher": {
      // Alt+X — abandon the voucher being entered. In CREATE mode the frame is
      // reset to a pristine one (the screen clears its form off the same key);
      // a SAVED voucher cannot be "cancelled" in this build (the ledger is
      // append-only: Alt+D writes a delete tombstone instead), so the key
      // reports that through the shell's status line rather than pretending.
      if (active.screen === "voucher" && active.voucherMode !== "alter") {
        return replaceActive(
          newVoucherFrame(active.voucherType ?? DEFAULT_LAST_VOUCHER),
        );
      }
      return {};
    }

    case "duplicate": {
      // Alt+2 — duplicate the loaded voucher: same lines, a NEW voucher. The
      // machine flips the frame to create mode (so Ctrl+A posts rather than
      // alters); the screen keeps the form and clears the voucher number.
      if (active.screen === "voucher" && active.voucherMode === "alter") {
        return replaceActive({
          ...active,
          voucherMode: "create",
          field: "date",
          saved: false,
        });
      }
      return {};
    }

    case "prevVoucher":
    case "nextVoucher":
    case "exportScreen":
    case "print": {
      // Pure side effects owned by the active screen (browse saved vouchers,
      // write a CSV, fetch a PDF). The machine deliberately does not move the
      // stack.
      //
      // PRINT belongs here and not in a frame transition because printing does
      // not navigate: an operator who prints an invoice is still on that
      // invoice. It also keeps the machine PURE — resolving what to print needs
      // the loaded voucher id, and no voucher id travels through this machine
      // (the same rule `editLog` established). screens/printTarget.ts decides,
      // and the screen fires it through the ScreenActions bridge.
      return {};
    }

    case "importBankStatement": {
      // NO NAVIGATION. Importing happens in a panel ON the reconciliation
      // screen, because slice 2b's matching is inherently a two-column view of
      // statement rows beside book legs — splitting them across screens would
      // make that slice worse. The screen owns the panel and fires it through
      // the ScreenActions bridge, exactly as F4 owns its picker.
      return {};
    }

    case "allocateTDSChallan":
    case "recordTDSChallan": {
      // NO NAVIGATION. The deposit form lives ON the challan register, because
      // the register is the context that says which tax year is being paid for
      // and what has already been recorded against it. The screen owns the form
      // and fires it through the ScreenActions bridge, exactly as Alt+O owns its
      // import panel.
      return {};
    }

    case "createCostMaster":
    case "selectCostCategory": {
      // NO NAVIGATION, for the reason `recordTDSChallan` above gives. The create
      // panel lives ON the cost masters screen (the list beside it is what tells
      // an operator the name is not taken, and on which axis it is not taken), and
      // the axis picker lives ON the Cost Centre Summary (choosing an axis does
      // not leave the report — it IS the report's subject). Both are screen-owned
      // side effects fired through the ScreenActions bridge.
      //
      // ⚠️ A `default: return {}` below would already produce this answer. The
      // cases are written out because the shell reports "does not apply on this
      // screen" for a key that neither ran a screen action nor moved the stack —
      // so an absent SCREEN_ACTIONS entry is indistinguishable from a key that is
      // meant to be inert, and writing the inertness down is what makes the
      // difference reviewable.
      return {};
    }

    case "orderRegister": {
      // Ctrl+F8 / Ctrl+F9 — the two order REGISTERS (T3).
      //
      // Reachable from ANYWHERE, unlike `bankReconciliation` above which is
      // gateway-only: these are documented TallyPrime voucher-family keys
      // (RESEARCH.md:400, :614, :616), and an operator presses a voucher-family
      // key from wherever they are. That is also why the two registers hold the
      // twelve OTHER voucher keys back in the keymap but deliberately leave this
      // pair live — moving between the two registers is the one navigation these
      // screens should not block.
      //
      // pushOnce, so pressing Ctrl+F8 twice on the sales register does not stack a
      // second identical frame. The key then changes nothing, which is the honest
      // answer ("you are already here") — and `orderRegister` is in
      // ALWAYS_MEANINGFUL in AppShell so the shell does not additionally report it
      // as a key that does not apply on this screen.
      return pushOnce(
        action.direction === "sales" ? "sales-orders" : "purchase-orders",
      );
    }

    case "eWayBill": {
      // Alt+Z — the e-WAY BILL screen (T5).
      //
      // Reachable from ANYWHERE, like the two order registers above and for the
      // same reason: it is a statutory act an operator performs immediately after
      // accepting the invoice, from wherever they happen to be.
      //
      // pushOnce, so pressing Alt+Z twice does not stack a second identical
      // frame — the key then changes nothing, which is the honest answer, and
      // `eWayBill` is in ALWAYS_MEANINGFUL in AppShell so the shell does not
      // additionally report it as a key that does not apply here.
      return pushOnce("e-way-bill");
    }

    case "eInvoice": {
      // Alt+I — the e-INVOICE / IRN screen (§1.4 B).
      //
      // Reachable from ANYWHERE, for the reason the e-way bill above gives: it
      // is a statutory act an operator performs immediately after accepting the
      // invoice, from wherever they happen to be. TallyPrime agrees from the
      // other direction — RESEARCH.md:698 records that 4.x moved IRN operations
      // out of the voucher into a standalone e-Invoice report.
      //
      // pushOnce, so pressing Alt+I twice does not stack a second identical
      // frame; `eInvoice` is in ALWAYS_MEANINGFUL in AppShell so the shell does
      // not additionally report it as a key that does not apply here.
      return pushOnce("e-invoice");
    }

    case "createOrder":
    case "closeOrder":
    case "createBom":
    case "explodeBom": {
      // NO NAVIGATION — the same answer, and for the same reasons, that
      // `createCostMaster` and `selectCostCategory` give above. All four are
      // screen-owned side effects fired through the ScreenActions bridge:
      //
      //  - the create FORMS live on the register / the BOM list, because the list
      //    beside them is what tells an operator the order number or the recipe
      //    name is already taken;
      //  - Alt+W closes the order HIGHLIGHTED in the list, which is a fact only
      //    the screen holds;
      //  - Alt+X explodes the highlighted recipe, which is a server round-trip
      //    against a bom_id the pure machine has never seen.
      //
      // ⚠️ Written out rather than left to a `default: return {}` for the reason
      // the cost cases give: the shell reports "does not apply on this screen" for
      // a key that neither ran a screen action nor moved the stack, so a MISSING
      // SCREEN_ACTIONS entry looks exactly like a key meant to be inert. Writing
      // the inertness down is what makes the difference reviewable — and this is
      // the registration point that shipped Alt+O completely dead.
      return {};
    }

    case "payRun": {
      // Ctrl+F4 — THE PAY RUN (RESEARCH.md:903).
      //
      // pushOnce, so pressing it twice does not stack a second identical frame;
      // `payRun` is in ALWAYS_MEANINGFUL in AppShell so the shell does not
      // additionally report it as a key that does not apply here.
      //
      // ⛔ IT PUSHES A SCREEN AND NOT A VOUCHER FRAME, and the difference is not
      // cosmetic: VoucherScreen resolves its layout through configFor(voucherType)
      // and there is no payroll VoucherConfig, so a voucher frame would take the
      // operator to an undefined config. A pay run has no particulars to key —
      // its lines are computed from the masters for a whole WAGE MONTH.
      return pushOnce("payroll-run");
    }

    case "setValuationMethod":
      // NO NAVIGATION, and here that is the whole point: the operator is looking
      // at the figure that is about to change, and the panel opens OVER the
      // report so the table redraws underneath it. Navigating anywhere would
      // take away the only evidence that the switch did anything.
      //
      // ⚠️ Written out rather than left to a `default: return {}` for the reason
      // the payroll block below gives: the shell reports "does not apply on this
      // screen" for a key that neither ran a screen action nor moved the stack,
      // so a MISSING entry here is indistinguishable from a key meant to be
      // inert — which is how Alt+O once shipped completely dead.
      return {};

    case "createEmployee":
    case "setEmployeePay":
    case "payrollStatutoryFiles": {
      // NO NAVIGATION — the same answer, and the same reasons, as
      // `createCostMaster` and `createOrder` above:
      //
      //  - the create FORM lives on the employee list, because the list beside it
      //    is what tells an operator the code is already taken;
      //  - Alt+S sets the pay of the employee HIGHLIGHTED in that list, which is a
      //    fact only the screen holds;
      //  - Alt+S on the register opens a panel for the month ON SCREEN, and the
      //    ESIC return additionally needs a LEAVING REASON per leaver — a question
      //    only that panel can ask, and one the server refuses to guess (a leaver
      //    with a blank reason is a row the portal accepts and reads as empty).
      //
      // ⚠️ Written out rather than left to a `default: return {}` for the reason
      // every block above gives: the shell reports "does not apply on this screen"
      // for a key that neither ran a screen action nor moved the stack, so a
      // MISSING SCREEN_ACTIONS entry is indistinguishable from a key meant to be
      // inert — which is how Alt+O once shipped completely dead.
      return {};
    }

    case "bankReconciliation": {
      // The Gateway / menu entry opens the statement — TallyPrime's own route to
      // it is Alt+G > Banking Activities (RESEARCH.md:873), so a Gateway entry
      // and the Go To picker are the faithful ways in.
      if (active.screen === "gateway" || active.screen === "menu") {
        return pushOnce("bank-reconciliation");
      }
      // F4 on the statement itself — "select bank ledger" (RESEARCH.md:879).
      // The machine does NOT move: which accounts exist, and which of them are
      // bank accounts, is an ltree ancestry fact that lives in the database.
      // The screen owns the picker and fires it through the ScreenActions
      // bridge, the same way Alt+E owns its export.
      return {};
    }

    case "accept": {
      // Ctrl+A on a master-create screen accepts the master and POPS back to the
      // originating frame (typically a voucher), where the freshly-created
      // master is now available for selection. This mirrors Tally's inline
      // Alt+C create-and-return flow.
      if (active.screen === "master-create" && stack.length > 1) {
        stack.pop();
        const parent = { ...top(stack) };
        // Surface the just-created master as the parent's pending selection so
        // the voucher screen can pre-fill the field that triggered the create.
        //
        // ⛔ ONLY ONTO A VOUCHER, and that guard ships WITH the Go To route
        // above rather than after it. `selectedItem` means something DIFFERENT
        // on every other frame: on the Gateway and on a report it is the row
        // Enter drills into. Stamping a freshly created master's name there
        // made the Gateway's pending selection "Acme Traders", and the very next
        // Enter pushed `{screen:"report", reportName:"Acme Traders"}` —
        // `canonicalReportName` falls back to the raw label — so accepting a
        // ledger left the operator one keypress away from a report that does not
        // exist. Harmless-looking before this commit because the ONLY door to a
        // master-create was Alt+C from a voucher; the Go To entries make the
        // Gateway the PRIMARY parent, so the guard is part of the same fix.
        if (active.detailOf && parent.screen === "voucher") {
          parent.selectedItem = active.detailOf;
        }
        stack[stack.length - 1] = parent;
        return { stack };
      }
      // Ctrl+A accepts the voucher and re-opens a fresh form for bulk entry.
      //
      // ⛔⛔ ONLY A **CREATE** FRAME RE-OPENS FRESH. AN **ALTER** FRAME KEEPS ITS
      // IDENTITY, AND THAT IS A DUPLICATE-INVOICE GUARD, NOT A NAVIGATION
      // PREFERENCE (D89, found by the D82 drive).
      //
      // This reducer is pure, so it cannot know whether the write it is
      // reacting to SUCCEEDED — the screen's onAccept fires first (the
      // ScreenActions bridge) and its request resolves long after. Replacing an
      // alter frame with a fresh `create` frame therefore dropped the loaded
      // `voucher_id` on a REFUSED alter too, while the form kept the loaded
      // voucher's values on screen and nothing said the frame had changed
      // class. Driven in a browser: a legacy sales invoice with no HSN was
      // drilled from the Day Book, Ctrl+A was refused CLIENT-SIDE by
      // `gstRefusal` (nothing sent), the operator keyed the HSN the refusal
      // asked for, pressed Ctrl+A again — and the screen POSTed a **new**
      // voucher (201), said "Saved", and left the original invoice's `hsn`
      // empty. One tax invoice in, two on the books, and the remedy the refusal
      // itself prescribed is what minted the second.
      //
      // Staying put is what `delete` below already does for an alter frame
      // (it pops, because the voucher it was altering is gone). Here the
      // voucher still exists, so the frame that names it must survive: a second
      // Ctrl+A re-PUTs the same `voucher_id` (harmless) instead of POSTing a
      // twin, and Esc returns to the report the operator drilled from.
      if (active.screen === "voucher" && active.voucherMode === "alter") {
        return {};
      }
      if (active.screen === "voucher") {
        const type = active.voucherType ?? DEFAULT_LAST_VOUCHER;
        const fresh = newVoucherFrame(
          type,
          "create",
          ctx.entryModeByType[type],
        );
        fresh.saved = true;
        stack[stack.length - 1] = fresh;
        return { stack };
      }
      return {};
    }

    case "delete": {
      // Alt+D deletes the loaded voucher. It is only meaningful in an
      // alteration-mode voucher frame (reached by drilling from the Day Book /
      // a report). The pure machine just pops back to the originating screen —
      // exactly like Esc — once the voucher is gone; the side-effecting
      // deleteVoucher() client call lives in the screen's onDelete handler,
      // keyed off snapshot.mode === "alter". A create-mode voucher (nothing
      // persisted yet) has nothing to delete, so the key is a no-op.
      if (
        active.screen === "voucher" &&
        active.voucherMode === "alter" &&
        stack.length > 1
      ) {
        stack.pop();
        return { stack };
      }
      return {};
    }

    case "createMaster": {
      // Alt+C pushes an inline master-create screen that PRESERVES its parent
      // (the voucher) and returns to it on Ctrl+A (accept) or Esc (cancel).
      const masterFrame: ScreenFrame = {
        screen: "master-create",
        masterKind: action.masterKind,
        field: "name",
      };
      stack.push(masterFrame);
      return { stack };
    }

    case "goto": {
      // Go To overlay: push a child that PRESERVES its parent and returns to it
      // on Esc. The selection is confirmed with Enter.
      const overlay: ScreenFrame = {
        screen: "goto-overlay",
        field: "goto-search",
        gotoMode: "goto",
      };
      stack.push(overlay);
      return { stack };
    }

    case "switchTo": {
      // Switch To overlay: same picker UX, but confirming REPLACES the context
      // (the prior screen cannot be returned to).
      const overlay: ScreenFrame = {
        screen: "goto-overlay",
        field: "goto-search",
        gotoMode: "switchTo",
      };
      stack.push(overlay);
      return { stack };
    }

    case "changeMode": {
      if (active.screen === "voucher") {
        active.overlay = "change-mode";
        stack[stack.length - 1] = active;
        return { stack };
      }
      return {};
    }

    case "date": {
      // F2 focuses the date field of the active voucher.
      if (active.screen === "voucher") {
        active.field = "date";
        stack[stack.length - 1] = active;
        return { stack };
      }
      return {};
    }

    default:
      return {};
  }
}

/** Pure reducer for a SELECT (menu / list pick) event. */
function reduceSelect(ctx: NavContext, menuItem: string): Partial<NavContext> {
  const stack = ctx.stack.slice();
  const active = { ...top(stack) };

  // CTRL+H CHANGE MODE: picking a mode applies it and closes the overlay.
  //
  // Unlike the Go To pick-list below, this needs no confirming Enter — Tally's
  // Change Mode takes effect the moment you choose, and the overlay was
  // previously a dead end: it rendered three labels that did nothing at all.
  //
  // A "not built" option (the invoice modes on Sales/Purchase) is ACCEPTED as a
  // dismissal rather than silently ignored: the overlay closes, the mode is
  // unchanged, and the label already said why.
  if (active.screen === "voucher" && active.overlay === "change-mode") {
    // The machine interprets the two CANONICAL labels and nothing else. Which
    // options a class OFFERS is the screen's business (voucherConfig's
    // entryModesFor) — putting that table here would make the pure machine
    // import from screens/, inverting the layering. Anything else, including
    // the "not built" invoice modes, falls through as a dismissal.
    //
    // A double-entry-only class cannot be pushed into single mode this way
    // either: it never offers the label, and effectiveEntryMode() forces
    // "double" for those classes regardless of what the config says.
    const mode = ENTRY_MODE_LABELS[menuItem];
    active.overlay = undefined;
    if (mode) {
      active.config = { ...(active.config ?? {}), entryMode: mode };
      stack[stack.length - 1] = active;
      // Remember it for this voucher TYPE, not just this frame: Ctrl+A replaces
      // the frame after every save, so a frame-only choice lasts one voucher.
      const type = active.voucherType;
      if (type) {
        return {
          stack,
          entryModeByType: { ...ctx.entryModeByType, [type]: mode },
        };
      }
    }
    stack[stack.length - 1] = active;
    return { stack };
  }

  // ⛔ A STANDING ALTERATION FRAME RE-CLASSES ITSELF WHEN THE LOADED VOUCHER
  // CHANGES UNDER IT.
  //
  // Page Up / Page Down browse the SAVED vouchers around the drilled one in Day
  // Book order — which is chronological and therefore MIXED-CLASS. VoucherScreen's
  // `stepVoucher` loads the neighbour into the LoadedVoucher slot and publishes
  // its row key here, exactly as a report row does; without this branch the frame
  // kept the class it was drilled with, so paging from a Sales alter onto a
  // Receipt re-classed the Receipt on Ctrl+A with no drill involved at all.
  //
  // The screen sets the LoadedVoucher context BEFORE firing, and the voucher id
  // is NOT smuggled through this event — the screen reads it back out of that
  // context. Same contract as OPEN_PURCHASE_DRAFT_SENTINEL.
  //
  // ⛔ ALTER MODE ONLY. A CREATE frame's class is the operator's own F-key choice
  // and nothing may overwrite it; there is nothing loaded to page through either.
  if (
    active.screen === "voucher" &&
    active.voucherMode === "alter" &&
    !active.overlay
  ) {
    const row = parseVoucherRowKey(menuItem);
    if (!row) return {};
    // The FIELD CURSOR is deliberately reset to the date: the operator is now
    // looking at a different document, and leaving the cursor on (say) the
    // fourth particular of the voucher they just left points it at a row this
    // one may not have.
    active.voucherType = row.voucherType;
    active.field = "date";
    stack[stack.length - 1] = active;
    return { stack };
  }

  // In a Go To / Switch To overlay, a selection highlights the item; Enter
  // confirms it (matching Tally's type-ahead pick-list then Enter).
  if (active.screen === "goto-overlay") {
    active.selectedItem = menuItem;
    stack[stack.length - 1] = active;
    return { stack };
  }

  // Gateway/menu selection: choosing "Vouchers", "Bill Inbox" or a report
  // highlights it; Enter then drills (into the voucher form, the Bill Inbox, or
  // the report respectively). "Bill Inbox" is neither "vouchers" nor a report
  // name, so it needs its own highlightable branch.
  if (active.screen === "gateway" || active.screen === "menu") {
    if (
      menuItem.toLowerCase() === "vouchers" ||
      menuItem.toLowerCase() === BILL_INBOX_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === QUERY_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === USERS_ROLES_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === MORE_REPORTS_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === TDS_CHALLANS_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === COST_CENTRES_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === COST_CENTRE_SUMMARY_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === COST_CATEGORY_SUMMARY_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === SALES_ORDERS_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === PURCHASE_ORDERS_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === BOMS_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === EMPLOYEES_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === PAY_RUN_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === PAYROLL_REGISTER_LABEL.toLowerCase() ||
      menuItem.toLowerCase() === FORM_138_LABEL.toLowerCase() ||
      canonicalReportName(menuItem) !== undefined
    ) {
      active.selectedItem = menuItem;
      stack[stack.length - 1] = active;
      return { stack };
    }
    return {};
  }

  // Bill Inbox: the screen sets the LoadedDraft context, then fires the pinned
  // sentinel through onSelect. Push the F9 Purchase create frame IMMEDIATELY so
  // VoucherScreen (purchase && !alter && loadedDraft != null) hydrates from the
  // draft — exactly mirroring the F9 voucher push. No follow-up Enter needed
  // because the call is programmatic.
  if (active.screen === "bill-inbox") {
    if (menuItem === OPEN_PURCHASE_DRAFT_SENTINEL) {
      stack.push(newVoucherFrame("purchase", "create"));
      return { stack };
    }
    return {};
  }

  // Company switch: the screen has ALREADY re-scoped the session (setCompany)
  // and now asks the machine to leave the picker. Unwind to the ROOT frame —
  // the frames below were rendered against the previous company's books.
  if (active.screen === "company-select") {
    if (menuItem === COMPANY_SWITCHED_SENTINEL) {
      return { stack: [stack[0] as ScreenFrame] };
    }
    return {};
  }

  // THE PAYROLL REGISTER: a selection highlights the EMPLOYEE Enter will open the
  // payslip for. Its own branch rather than the report branch below, because this
  // screen is a ScreenKind — see keystroke/types.ts for why the register is not a
  // `report` frame despite `payroll-register` being a real report NAME.
  if (active.screen === "payroll-register") {
    active.selectedItem = menuItem;
    stack[stack.length - 1] = active;
    return { stack };
  }

  // THE EMPLOYEE MASTER: a selection highlights the employee that Alt+S sets the
  // salary structure and the statutory election of. Enter does NOT navigate from
  // here — there is nothing below an employee to drill into — so this branch only
  // records the highlight, which is what the screen's own write key acts on.
  if (active.screen === "employees") {
    active.selectedItem = menuItem;
    stack[stack.length - 1] = active;
    return { stack };
  }

  // The Cost CATEGORY Summary: a selection highlights the AXIS Enter will open.
  // Its own branch rather than being folded into the report branch below, because
  // this screen is a ScreenKind — see the ScreenKind doc in keystroke/types.ts for
  // why neither cost summary is a `report` frame.
  if (active.screen === "cost-category-summary") {
    active.selectedItem = menuItem;
    stack[stack.length - 1] = active;
    return { stack };
  }

  // Inside a report, a selection highlights the row Enter will drill into.
  if (active.screen === "report" || active.screen === "report-detail") {
    active.selectedItem = menuItem;
    stack[stack.length - 1] = active;
    return { stack };
  }

  // Inside a master-create screen, a selection fills the active field (e.g.
  // picking the parent group) AND is remembered as the master's name so the
  // accept handler can return it to the originating voucher field. The masters
  // agent owns the field-level form; the machine only tracks the pending pick.
  if (active.screen === "master-create") {
    active.detailOf = menuItem;
    stack[stack.length - 1] = active;
    return { stack };
  }

  return {};
}

/**
 * Would this combo change the screen stack? A PURE preview of {@link reduceKey}
 * used by the shell to tell the user when a key did nothing ON THIS SCREEN
 * (e.g. F5 Ledger-wise pressed inside a voucher). Sharing the real reducer is
 * the point: a preview that re-implemented the rules could disagree with them.
 */
export function keyChangesStack(ctx: NavContext, combo: string): boolean {
  const next = reduceKey(ctx, combo);
  if (!next.stack) return false;
  return JSON.stringify(next.stack) !== JSON.stringify(ctx.stack);
}

/**
 * The navigation machine. A single `running` state; all stack transitions are
 * expressed as context updates so the screen-stack invariant lives in one
 * place. Construct context via {@link makeInitialContext} when interpreting.
 */
export const navMachine = setup({
  types: {
    context: {} as NavContext,
    events: {} as NavEvent,
    input: {} as NavMachineOptions,
  },
  actions: {
    applyKey: assign(({ context, event }) => {
      if (event.type !== "KEY") return {};
      return reduceKey(context, event.combo);
    }),
    applySelect: assign(({ context, event }) => {
      if (event.type !== "SELECT") return {};
      return reduceSelect(context, event.menuItem);
    }),
    applyConfig: assign(({ context, event }) => {
      if (event.type !== "CONFIG") return {};
      const stack = context.stack.slice();
      const active = { ...top(stack) };
      active.config = { ...active.config, ...event.patch };
      stack[stack.length - 1] = active;
      return { stack };
    }),
  },
}).createMachine({
  id: "nav",
  context: ({ input }) => makeInitialContext(input),
  initial: "running",
  states: {
    running: {
      on: {
        KEY: { actions: "applyKey" },
        SELECT: { actions: "applySelect" },
        CONFIG: { actions: "applyConfig" },
      },
    },
  },
});

/**
 * A flat, corpus-shaped projection of the current navigation state. This is the
 * exact shape the keystroke golden corpora assert against.
 */
export interface NavSnapshot {
  screen: ScreenKind;
  voucherType?: VoucherType;
  field?: VoucherField | OverlayField | MasterField;
  mode?: VoucherMode;
  reportName?: string;
  masterKind?: MasterKind;
  detailOf?: string;
  /** The active frame's pending selection (e.g. a Go To pick, a created master). */
  selectedItem?: string;
  saved?: boolean;
  overlay?: FrameOverlay;
  /** The frame's view configuration (F12 / Ctrl+H / Alt+F1 / F5 / F2 state). */
  config?: FrameConfig;
  screenStackDepth: number;
  parentScreen?: ScreenKind;
  parentVoucherType?: VoucherType;
  parentReportName?: string;
  canReturnToVoucher: boolean;
}

/** Project a {@link NavContext} into a flat, assertable snapshot. */
export function projectSnapshot(ctx: NavContext): NavSnapshot {
  const active = top(ctx.stack);
  const parent =
    ctx.stack.length >= 2
      ? (ctx.stack[ctx.stack.length - 2] as ScreenFrame)
      : undefined;

  // Can we Esc back to a voucher? True only if some lower frame is a voucher
  // that was not destroyed by a Switch To replacement.
  const canReturnToVoucher = ctx.stack
    .slice(0, ctx.stack.length - 1)
    .some((f) => f.screen === "voucher" && !f.replaced);

  const snap: NavSnapshot = {
    screen: active.screen,
    screenStackDepth: ctx.stack.length,
    canReturnToVoucher,
  };
  if (active.voucherType !== undefined) snap.voucherType = active.voucherType;
  if (active.field !== undefined) snap.field = active.field;
  if (active.voucherMode !== undefined) snap.mode = active.voucherMode;
  if (active.reportName !== undefined) snap.reportName = active.reportName;
  if (active.masterKind !== undefined) snap.masterKind = active.masterKind;
  if (active.detailOf !== undefined) snap.detailOf = active.detailOf;
  if (active.selectedItem !== undefined)
    snap.selectedItem = active.selectedItem;
  if (active.saved) snap.saved = true;
  if (active.overlay !== undefined) snap.overlay = active.overlay;
  if (active.config !== undefined) snap.config = active.config;
  if (parent) {
    snap.parentScreen = parent.screen;
    if (parent.voucherType !== undefined)
      snap.parentVoucherType = parent.voucherType;
    if (parent.reportName !== undefined)
      snap.parentReportName = parent.reportName;
  }
  return snap;
}
