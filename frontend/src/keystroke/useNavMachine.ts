import { useMachine } from "@xstate/react";
import { useCallback, useMemo } from "react";
import { navMachine, projectSnapshot } from "./navMachine";
import type { NavMachineOptions, NavSnapshot } from "./navMachine";
import { comboFromEvent } from "./normalize";

/**
 * React binding for the navigation machine. Returns the current corpus-shaped
 * {@link NavSnapshot}, plus handlers to feed it raw DOM key events and
 * selection-list picks. The machine is the single source of truth; React just
 * renders its projection.
 */
export function useNavMachine(options: NavMachineOptions = {}): {
  snapshot: NavSnapshot;
  onKeyDown: (ev: React.KeyboardEvent | KeyboardEvent) => void;
  onSelect: (menuItem: string) => void;
} {
  const [state, send] = useMachine(navMachine, { input: options });

  const snapshot = useMemo(
    () => projectSnapshot(state.context),
    [state.context],
  );

  const onKeyDown = useCallback(
    (ev: React.KeyboardEvent | KeyboardEvent) => {
      const combo = comboFromEvent({
        key: ev.key,
        ctrlKey: ev.ctrlKey,
        altKey: ev.altKey,
        shiftKey: ev.shiftKey,
        metaKey: ev.metaKey,
      });
      // Prevent the browser from hijacking Tally's chords (e.g. Ctrl+A select-all).
      if (ev.ctrlKey || ev.altKey || /^F\d{1,2}$/.test(combo)) {
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

  return { snapshot, onKeyDown, onSelect };
}
