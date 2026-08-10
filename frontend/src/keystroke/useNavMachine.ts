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
      // Prevent the browser from hijacking Tally's chords: Ctrl+A select-all,
      // Ctrl+P print, the F-key browser menus. Plain printable keys are never
      // intercepted, so typing into a field still works normally.
      //
      // =====================================================================
      // ⛔⛔ TAB AND SHIFT+TAB ARE **NOT** IN THIS LIST, AND THAT IS THE WHOLE
      // POINT. THEY USED TO BE, AND IT MADE THE PRODUCT UNKEYABLE.
      // =====================================================================
      // The removed clause read `combo === "Tab" || combo === "Shift+Tab"`, and
      // the comment defending it said "Tab's focus walk — the field cursor is
      // ours". BOTH HALVES WERE FALSE, and the cost was the largest keyboard
      // defect this product has had.
      //
      //  1. THE FIELD CURSOR IS NOT A FOCUS CURSOR. `navMachine`'s
      //     VOUCHER_FIELDS is five strings — date, party-ledger, ledger, amount,
      //     narration — and advancing it does nothing but change which row
      //     VoucherScreen paints with `bg-tally-active`. Grep the screen: the
      //     ONLY place `snapshot.field` touches the DOM is an `autoFocus` prop,
      //     which React applies at MOUNT and never again. So the machine took
      //     the browser's focus walk away and put NOTHING in its place.
      //
      //  2. THE VOUCHER IS NOT FIVE FIELDS. One particulars row alone carries a
      //     ledger box, an amount box, a remove button, the whole cost-split
      //     picker (an add button, an axis select, a centre select, an amount, a
      //     "rest" button, a remove, "+ split") and a bill-reference picker —
      //     and none of them is one of the five. Every one of those controls was
      //     MOUSE-ONLY on a keystroke-first product, while the hint strip on
      //     that very screen advertised "Tab / Shift+Tab: Field". Tab was bound
      //     GLOBALLY in the keymap, so the same was true of every other screen:
      //     `nextField` returns {} for anything that is not a voucher or a
      //     master-create, i.e. Tab was swallowed and then discarded on reports,
      //     the Gateway, Users & Roles and the Bill Inbox alike.
      //
      // THE RULE THIS ESTABLISHES, which is the general form of the bug:
      // ⛔ NEVER preventDefault A KEY WHOSE HANDLER DOES NOT REPLACE WHAT THE
      // BROWSER WOULD HAVE DONE. Sequential focus navigation is a browser
      // primitive — it knows about disabled controls, `tabindex`, shadow roots,
      // scroll containers and the platform's own conventions. Re-implementing it
      // here would be the same mistake in a new place (see HANDOFF §7.2: an
      // authorization peek re-implementing a rule that lives in encoding/json),
      // so we hand it BACK rather than reimplement it. It is the identical
      // reading to the ⌘A note in normalize.ts: the browser's own behaviour is
      // what the operator asked for, and taking it away without substituting
      // anything is a silent, total loss of a capability.
      //
      // ⚠️ THE KEY IS STILL SENT TO THE MACHINE. `corpora/keystroke/
      // voucher-tab-walks-fields.json` pins the five-field cursor to Tab, and
      // the highlight it drives is a real (if coarse) affordance. So Tab now
      // does BOTH: the browser moves real focus, and the machine advances the
      // highlight. They agree for the first few presses and then the highlight
      // saturates at "narration" while focus walks on into the grid — the
      // highlight is DECORATION, the focus ring is the truth, and a lagging
      // decoration is not a reason to keep the operator out of the form. Making
      // the highlight follow real focus needs an edit in VoucherScreen.tsx,
      // which this lane does not own; it is reported, not attempted.
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
