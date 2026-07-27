import { assign, setup } from "xstate";
import { LEDGER_WISE_DEFAULT } from "./types";
import type {
  FrameConfig,
  FrameOverlay,
  KeymapAction,
  MasterKind,
  ScreenKind,
  VoucherMode,
  VoucherType,
} from "./types";
import { resolveAction, resolveKeymap } from "./keymaps/tallyprime";

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
  "bills payable": "Outstandings",
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
): ScreenFrame {
  return {
    screen: "voucher",
    voucherType,
    voucherMode: mode,
    field: "date",
    saved: false,
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

  /** Push a screen unless it is already the active one (F1 twice = one Help). */
  const pushOnce = (screen: ScreenKind): Partial<NavContext> => {
    if (active.screen === screen) return {};
    stack.push({ screen });
    return { stack };
  };

  const isReportScreen =
    active.screen === "report" || active.screen === "report-detail";

  switch (action.kind) {
    case "voucher": {
      // F-key switches voucher type. If the active frame is a voucher, replace
      // it in place; otherwise push a new voucher frame onto the stack.
      const frame = newVoucherFrame(action.voucherType);
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
          stack.push(newVoucherFrame(DEFAULT_LAST_VOUCHER, "alter"));
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
        stack.push(newVoucherFrame(DEFAULT_LAST_VOUCHER, "alter"));
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
        // "Display More Reports" opens the second-level report MENU (Tally's
        // own structure: the Trial Balance and Outstandings live one level in).
        // Checked before the report fallback, which would otherwise push a
        // report frame literally named "Display More Reports".
        if (
          active.selectedItem?.toLowerCase() === MORE_REPORTS_LABEL.toLowerCase()
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
      if (active.screen !== "voucher" || active.voucherMode !== "alter") return {};
      return pushOnce("edit-log");
    }

    case "configure": {
      return openOverlay("configure");
    }

    case "calculator": {
      return openOverlay("calculator");
    }

    case "period": {
      // F2 (report) / Alt+F2 — the From/To picker. Only reports have a period.
      if (isReportScreen) return openOverlay("period");
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
    case "exportScreen": {
      // Pure side effects owned by the active screen (browse saved vouchers,
      // write a CSV). The machine deliberately does not move the stack.
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
        if (active.detailOf) parent.selectedItem = active.detailOf;
        stack[stack.length - 1] = parent;
        return { stack };
      }
      // Ctrl+A accepts the voucher and re-opens a fresh form for bulk entry.
      if (active.screen === "voucher") {
        const fresh = newVoucherFrame(
          active.voucherType ?? DEFAULT_LAST_VOUCHER,
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
  if (active.selectedItem !== undefined) snap.selectedItem = active.selectedItem;
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
