import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  type ReactNode,
} from "react";
import type { NavSnapshot } from "../keystroke/navMachine";

/**
 * The side-effect bridge between the (pure) navigation machine and the screen
 * components that own the LIVE client calls.
 *
 * The navMachine stays byte-for-byte pure: its `accept` / `delete` /
 * `createMaster` reducers only move the screen stack. But Ctrl+A must POST a
 * voucher, Alt+D must DELETE one, and Ctrl+A on a master-create must
 * createLedger/createGroup/… — all of which need the CoredClient + the screen's
 * live form state. Those effects live in the screen components.
 *
 * The problem: the AppShell owns the single root `onKeyDown` and the machine's
 * `accept` reducer immediately resets the voucher frame (wiping the screen's
 * captured number). So the screen must run its effect at the moment the action
 * fires, reading the SNAPSHOT as it was BEFORE the machine reset.
 *
 * The bridge solves this with a mutable registry of per-action handlers. The
 * active screen registers its `onAccept` / `onDelete` (via {@link
 * useRegisterScreenAction}) in an effect; the AppShell, in its keydown wrapper,
 * resolves the combo to an action and, for accept/delete, invokes the
 * registered handler with the PRE-transition snapshot before forwarding the key
 * to the machine. AppShell never imports the client — it only fires the hook.
 */

/**
 * The screen-owned side effects a key can fire. `accept`/`delete` post and
 * tombstone vouchers; `export` writes the active screen's rows to CSV;
 * `cancelVoucher`/`duplicate` reshape the in-progress entry; `prevVoucher`/
 * `nextVoucher` browse the saved vouchers around the loaded one (Page Up/Down).
 */
export type ScreenActionKind =
  /**
   * Alt+C on the TDS Challans screen — open the DEPOSIT FORM (T6). A screen
   * action rather than a machine transition because the form's state (and the
   * client that posts it) belong to the screen, and recording a deposit does not
   * navigate: the operator is still looking at the register they are adding to.
   */
  | "recordTDSChallan"
  /**
   * Alt+A on the TDS Challans screen — ATTACH deductions to a deposit already
   * recorded (D1). A screen action for the same reason `recordTDSChallan` is,
   * and one more: the challan being added to is one the operator picked out of
   * the register that is on screen, so the action has no meaning off it.
   */
  | "allocateTDSChallan"
  /**
   * Alt+C on the COST MASTERS screen — open the create panel for a cost CATEGORY
   * (an axis) or a cost CENTRE (a position on one). A screen action rather than a
   * machine transition for the same reasons `recordTDSChallan` is: the panel's
   * state and the client that posts it belong to the screen, and creating a master
   * here does not navigate — the operator is still looking at the list they are
   * adding to, which is the list that tells them the name is not taken already.
   */
  | "createCostMaster"
  /**
   * F4 on the COST CENTRE SUMMARY — choose the AXIS (cost category) the report is
   * for. The screen owns it because the axes are a SERVED list, and because the
   * report is meaningless without one: every figure on it is scoped to one axis,
   * since several axes allocate the same money in parallel.
   */
  | "selectCostCategory"
  /**
   * Alt+C on an ORDER REGISTER — open the form that records a new sales or
   * purchase order (T3). A screen action rather than a machine transition for the
   * reasons `recordTDSChallan` gives: the form's state and the client that posts it
   * belong to the screen, and recording an order does not navigate — the operator
   * is still looking at the register they are adding to, which is the list that
   * tells them the order number is not already taken.
   *
   * ⚠️ IT CARRIES NO DIRECTION, and neither does the keymap action behind it. The
   * screen IS one direction; a direction on the action would be a second source of
   * truth for the same fact.
   */
  | "createOrder"
  /**
   * Alt+W on an ORDER REGISTER — PRE-CLOSE (abandon) the highlighted order, with a
   * required reason. The screen owns it because the order to close is the one
   * HIGHLIGHTED in its list, and because re-reading the list afterwards is what
   * proves the close landed.
   */
  | "closeOrder"
  /**
   * Alt+C on the BILLS OF MATERIALS screen — open the form that defines a recipe
   * (T3). Screen-owned for the same reasons as `createOrder`; additionally, the
   * finished good and every component are PICKED from the served stock-item list,
   * which the screen holds and the machine cannot.
   */
  | "createBom"
  /**
   * Alt+X on the BILLS OF MATERIALS screen — explode the highlighted recipe for a
   * wanted quantity and show what a Stock Journal's lines would be. Screen-owned
   * because it is a SERVER round-trip against the highlighted bom_id: the scaling
   * is computed once at full precision server-side, deliberately not in the client
   * (see BomComponentInput — a pre-divided per-unit figure shorts every batch
   * forever at fixed-4).
   */
  | "explodeBom"
  | "accept"
  | "delete"
  | "export"
  | "cancelVoucher"
  | "duplicate"
  | "prevVoucher"
  | "nextVoucher"
  /**
   * Ctrl+P / Alt+P — fetch the active document as a PDF and hand it to the
   * browser. A screen action rather than a machine transition for two reasons:
   * printing does not navigate (an operator who prints an invoice is still on
   * that invoice), and resolving WHAT to print needs the loaded voucher id, which
   * deliberately never travels through the pure navigation machine.
   */
  | "print"
  /**
   * F4 on the Bank Reconciliation Statement — "select bank ledger"
   * (RESEARCH.md:879). The screen owns it because which ledgers are BANK ledgers
   * is an ltree ancestry fact that lives in the database.
   */
  | "bankReconciliation"
  /**
   * Alt+O on the Bank Reconciliation Statement — import a bank statement
   * (T4 slice 2a, RESEARCH.md:361 "O: Import"). A screen action for the same
   * reason F4 above is one: the import belongs to the SELECTED bank account,
   * and which account that is never travels through the pure machine.
   */
  | "importBankStatement";

/**
 * A handler the active screen registers for a screen action. It may return a
 * short status message ("Exported 42 rows to Day Book.csv"), which the shell
 * shows in its status line — so a key press always produces visible feedback.
 *
 * It may also return a PROMISE of one, for an action whose outcome is only known
 * after a round trip: printing cannot say "Printed Invoice INV-0042" until the
 * server has answered, and the message that matters most is the REFUSAL (a 403
 * from the ACL, a 501 for a report that does not print). The shell awaits it and
 * shows whatever arrives, so an async failure can never be silent.
 */
export type ScreenActionHandler = (
  snapshot: NavSnapshot,
) => void | string | ScreenRefusal | Promise<void | string>;

interface ScreenActionsValue {
  /** Mutable registry of the active screen's action handlers. */
  readonly handlers: React.MutableRefObject<
    Partial<Record<ScreenActionKind, ScreenActionHandler>>
  >;
}

const ScreenActionsContext = createContext<ScreenActionsValue | null>(null);

export function ScreenActionsProvider({ children }: { children: ReactNode }) {
  const handlers = useRef<
    Partial<Record<ScreenActionKind, ScreenActionHandler>>
  >({});
  const value = useMemo<ScreenActionsValue>(() => ({ handlers }), []);
  return (
    <ScreenActionsContext.Provider value={value}>
      {children}
    </ScreenActionsContext.Provider>
  );
}

function useScreenActions(): ScreenActionsValue {
  const ctx = useContext(ScreenActionsContext);
  if (!ctx) {
    throw new Error(
      "useScreenActions must be used within a ScreenActionsProvider",
    );
  }
  return ctx;
}

/** The outcome of dispatching a screen action (used for the status line). */
export interface ScreenActionResult {
  /** True when the active screen had a handler registered for the action. */
  ran: boolean;
  /** Optional message the handler wants shown in the shell's status line. */
  message?: string;
  /**
   * Set when the handler is still working. The shell awaits it and shows the
   * result, so an async refusal (a 403, a 501) reaches the operator instead of
   * being dropped on the floor.
   */
  pending?: Promise<void | string>;
  /**
   * Set when the handler REFUSED the action outright — the operator's input was
   * rejected and nothing was done. The shell then shows the message and does
   * NOT forward the key to the navigation machine (register row D92).
   *
   * ⛔ WHY THIS EXISTS. The machine's `accept` reducer POPS a master-create
   * frame unconditionally, because for its whole life the only way to reach it
   * was a successful create. A `build()` refusal — no group, a bill-wise
   * breakup that does not add up — therefore threw the operator back to the
   * Gateway and DISCARDED EVERYTHING THEY HAD KEYED, then showed them the
   * reason on a screen they could no longer act on. Measured in a browser
   * 2026-08-22: name a ledger, leave the group blank, Ctrl+A, and the form is
   * gone along with the name.
   *
   * That was survivable while the refusals were rare. D87 makes one of them
   * COMMON — every party keyed without a state code — so a refusal that costs
   * the operator their work would make that change a net harm.
   *
   * ⚠️ IT IS NOT A GENERAL "SWALLOW THE KEY". Only a handler that returns
   * {@link screenRefusal} sets it, so every existing handler is untouched and
   * the machine still moves for every action that is not an outright refusal.
   */
  refused?: boolean;
}

/**
 * The value a screen handler returns to say "I refused this; do not navigate".
 *
 * A branded object rather than a thrown error or a magic string: a throw would
 * have to be caught by every dispatch site and would be indistinguishable from
 * a genuine bug, and a string is what a SUCCESS already returns.
 */
export interface ScreenRefusal {
  readonly __screenRefusal: true;
  readonly message: string;
}

/** Build the refusal marker a handler returns instead of a plain message. */
export function screenRefusal(message: string): ScreenRefusal {
  return { __screenRefusal: true, message };
}

/** Narrow a handler's return value to a refusal. */
export function isScreenRefusal(v: unknown): v is ScreenRefusal {
  return (
    typeof v === "object" &&
    v !== null &&
    (v as ScreenRefusal).__screenRefusal === true
  );
}

/**
 * Fire the registered handler for an action with the pre-transition snapshot.
 * Reports whether a handler ran (and any message it returned) so the shell can
 * tell the user when a key did nothing HERE. Used by the AppShell keydown wrap.
 */
export function useScreenActionDispatcher(): (
  kind: ScreenActionKind,
  snapshot: NavSnapshot,
) => ScreenActionResult {
  const { handlers } = useScreenActions();
  return (kind, snapshot) => {
    const fn = handlers.current[kind];
    if (!fn) return { ran: false };
    const out = fn(snapshot);
    // A REFUSAL, checked before the string arm: it carries a message too, but
    // it must also stop the machine from moving. See ScreenActionResult.refused.
    if (isScreenRefusal(out)) {
      return { ran: true, message: out.message, refused: true };
    }
    if (typeof out === "string") return { ran: true, message: out };
    // A thenable means the handler is still working (a print fetch). Hand it back
    // so the shell can await it rather than reporting "nothing happened".
    if (out && typeof (out as Promise<void | string>).then === "function") {
      return { ran: true, pending: out as Promise<void | string> };
    }
    return { ran: true };
  };
}

/**
 * Register (and on unmount, clear) the active screen's handler for an action.
 * The screen passes a STABLE callback (it captures the live form state through a
 * ref it updates each render) so we don't churn the registry on every keystroke.
 */
export function useRegisterScreenAction(
  kind: ScreenActionKind,
  handler: ScreenActionHandler | undefined,
): void {
  const { handlers } = useScreenActions();
  // Keep the registry pointing at the latest handler each render via a ref, and
  // register a stable trampoline once (cleared on unmount). This way the screen
  // can pass a fresh closure over its live form state every render without
  // re-registering, and the next keydown always sees the current closure.
  const latest = useRef<ScreenActionHandler | undefined>(handler);
  latest.current = handler;
  useEffect(() => {
    const trampoline: ScreenActionHandler = (snap) => latest.current?.(snap);
    handlers.current[kind] = trampoline;
    return () => {
      if (handlers.current[kind] === trampoline) {
        delete handlers.current[kind];
      }
    };
  }, [handlers, kind]);
}
