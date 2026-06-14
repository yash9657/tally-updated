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

export type ScreenActionKind = "accept" | "delete";

/** A handler the active screen registers for a screen action. */
export type ScreenActionHandler = (snapshot: NavSnapshot) => void;

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

/**
 * Fire the registered handler for an action with the pre-transition snapshot.
 * Returns true when a handler ran. Used by the AppShell keydown wrapper.
 */
export function useScreenActionDispatcher(): (
  kind: ScreenActionKind,
  snapshot: NavSnapshot,
) => boolean {
  const { handlers } = useScreenActions();
  return (kind, snapshot) => {
    const fn = handlers.current[kind];
    if (fn) {
      fn(snapshot);
      return true;
    }
    return false;
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
