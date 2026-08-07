import { useMachine } from "@xstate/react";
import { useCallback, useMemo } from "react";
import { navMachine, projectSnapshot } from "./navMachine";
import type { NavMachineOptions, NavSnapshot } from "./navMachine";
import type { FrameConfig } from "./types";
import { comboFromEvent } from "./normalize";

/**
 * React binding for the navigation machine. Returns the current corpus-shaped
 * {@link NavSnapshot}, plus handlers to feed it raw DOM key events, selection-
 * list picks, and view-config patches (F12 Configure / F2 Period / Ctrl+F).
 * The machine is the single source of truth; React just renders its projection.
 */
export function useNavMachine(options: NavMachineOptions = {}): {
  snapshot: NavSnapshot;
  onKeyDown: (ev: React.KeyboardEvent | KeyboardEvent) => void;
  onSelect: (menuItem: string) => void;
  onConfig: (patch: FrameConfig) => void;
  /**
   * The machine's LIVE stack, serialized. Read it before and after sending a
   * key to know whether that key actually did anything — the rendered React
   * `state` is one tick behind (a SelectionList Enter fires SELECT and KEY in
   * the same tick), so it is the wrong oracle for "did that key work?".
   */
  stackSignature: () => string;
} {
  const [state, send, actorRef] = useMachine(navMachine, { input: options });

  const snapshot = useMemo(
    () => projectSnapshot(state.context),
    [state.context],
  );

  const onKeyDown = useCallback(
    (ev: React.KeyboardEvent | KeyboardEvent) => {
      const combo = comboFromEvent({
        key: ev.key,
        // See the identical line in AppShell, and KeyEventLike.code: without it
        // Option+<letter> on macOS resolves to a combo no keymap contains.
        code: ev.code,
        ctrlKey: ev.ctrlKey,
        altKey: ev.altKey,
        shiftKey: ev.shiftKey,
        metaKey: ev.metaKey,
      });
      // Prevent the browser from hijacking Tally's chords (e.g. Ctrl+A
      // select-all, F-key browser menus, Tab's focus walk — the field cursor is
      // ours). Plain printable keys are never intercepted, so typing into a
      // field still works normally.
      if (
        ev.ctrlKey ||
        ev.altKey ||
        /^F\d{1,2}$/.test(combo) ||
        combo === "Tab" ||
        combo === "Shift+Tab"
      ) {
        ev.preventDefault();
      }
      send({ type: "KEY", combo });
    },
    [send],
  );

  const onSelect = useCallback(
    (menuItem: string) => send({ type: "SELECT", menuItem }),
    [send],
  );

  const onConfig = useCallback(
    (patch: FrameConfig) => send({ type: "CONFIG", patch }),
    [send],
  );

  const stackSignature = useCallback(
    () => JSON.stringify(actorRef.getSnapshot().context.stack),
    [actorRef],
  );

  return { snapshot, onKeyDown, onSelect, onConfig, stackSignature };
}
