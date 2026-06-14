import { assign, setup } from "xstate";
import type {
  KeymapAction,
  MasterKind,
  ScreenKind,
  VoucherMode,
  VoucherType,
} from "./types";
import { resolveKeymap } from "./keymaps/tallyprime";
import { normalizeCombo } from "./normalize";

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
 * Pinned sentinel the {@link BillInboxScreen} feeds through `onSelect` (a SELECT
 * event) AFTER it has set the LoadedDraft context. reduceSelect treats this as
 * "open the chosen draft in the F9 Purchase voucher": it pushes a purchase
 * create frame IMMEDIATELY (no follow-up Enter), exactly mirroring the F9 push.
 * The draft id is NEVER smuggled through this sentinel — the screen resolves the
 * draft and calls setLoaded({draft}) before firing it, and VoucherScreen reads
 * the draft from the LoadedDraft context (isDraftMode).
 */
export const OPEN_PURCHASE_DRAFT_SENTINEL = "__open-purchase-draft__";

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
  /** Active in-frame overlay (e.g. the Ctrl+H Change Mode picker). */
  overlay?: "change-mode";
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
  | { type: "SELECT"; menuItem: string };

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
  const action: KeymapAction | undefined = keymap.bindings[normalizeCombo(combo)];
  if (!action) return {};

  const stack = ctx.stack.slice();
  const active = { ...top(stack) };

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
        if (active.overlay) {
          // Confirming a picker closes the overlay, leaving the field intact.
          active.overlay = undefined;
          stack[stack.length - 1] = active;
          return { stack };
        }
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
        const detail: ScreenFrame = {
          screen: "report-detail",
          reportName: active.reportName,
          detailOf: active.selectedItem,
        };
        stack.push(detail);
        return { stack };
      }
      if (active.screen === "report-detail") {
        // A report-detail row resolves to its underlying voucher in alteration
        // mode (the final drill level of a statement report).
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
      if (stack.length > 1) {
        stack.pop();
        return { stack };
      }
      return {}; // cannot pop past the root
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
  overlay?: "change-mode";
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
  if (parent) {
    snap.parentScreen = parent.screen;
    if (parent.voucherType !== undefined)
      snap.parentVoucherType = parent.voucherType;
    if (parent.reportName !== undefined)
      snap.parentReportName = parent.reportName;
  }
  return snap;
}
