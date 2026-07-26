import { useCallback, useEffect, useRef, useState } from "react";
import { useNavMachine } from "../keystroke/useNavMachine";
import { buttonBarFor } from "../keystroke/buttonBar";
import type { NavSnapshot } from "../keystroke/navMachine";
import {
  actionLabel,
  resolveAction,
  resolveKeymap,
} from "../keystroke/keymaps/tallyprime";
import { LEDGER_WISE_DEFAULT, type KeymapAction } from "../keystroke/types";
import { comboFromEvent } from "../keystroke/normalize";
import { useCompanyContext } from "./CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
  type ScreenActionKind,
} from "./ScreenActions";
import { LoadedVoucherProvider } from "./LoadedVoucher";
import { VOUCHER_CONFIGS } from "../screens/vouchers/voucherConfig";
import { LoadedDraftProvider } from "./LoadedDraft";
import { Chrome } from "./Chrome";
import { ScreenRegistry } from "./screenRegistry";
import { createDefaultRegistry } from "./defaultScreens";
import { FrameOverlayHost } from "../screens/overlays/FrameOverlays";

/** Screens that require an active company scope (useCompanyID would throw). */
const COMPANY_SCOPED_SCREENS = new Set<NavSnapshot["screen"]>([
  "voucher",
  "report",
  "report-detail",
  "master-create",
]);

/**
 * Keymap actions whose real work belongs to the ACTIVE SCREEN (it owns the
 * client + the form state), not to the pure navigation machine. The shell fires
 * the registered handler with the PRE-transition snapshot, then lets the
 * machine move the stack.
 */
const SCREEN_ACTIONS: Partial<Record<KeymapAction["kind"], ScreenActionKind>> = {
  accept: "accept",
  delete: "delete",
  exportScreen: "export",
  cancelVoucher: "cancelVoucher",
  duplicate: "duplicate",
  prevVoucher: "prevVoucher",
  nextVoucher: "nextVoucher",
};

/** How long a status-line message stays up before it fades (ms). */
const STATUS_TTL_MS = 6000;

/**
 * Actions that are meaningful even when the machine's stack comes out
 * IDENTICAL. Pressing F8 inside a Sales voucher rebuilds the same frame — a
 * byte-identical stack — but it did the Tally thing (a fresh Sales voucher), so
 * it must not be reported as "does not apply on this screen".
 */
const ALWAYS_MEANINGFUL = new Set<KeymapAction["kind"]>([
  "voucher",
  "date",
  "help",
  "features",
  "configure",
  "calculator",
]);

/**
 * The application shell. Provides the screen-action bridge and the loaded-voucher
 * store (so it is renderable standalone in tests), then delegates to the inner
 * shell which owns the navigation machine + chrome. The providers wrap the inner
 * shell because the inner shell CONSUMES the screen-action dispatcher.
 */
export function AppShell({
  registry = createDefaultRegistry(),
}: {
  registry?: ScreenRegistry;
}) {
  return (
    <ScreenActionsProvider>
      <LoadedVoucherProvider>
        <LoadedDraftProvider>
          <AppShellInner registry={registry} />
        </LoadedDraftProvider>
      </LoadedVoucherProvider>
    </ScreenActionsProvider>
  );
}

function AppShellInner({
  registry = createDefaultRegistry(),
}: {
  registry?: ScreenRegistry;
}) {
  const { company } = useCompanyContext();
  const { snapshot, onKeyDown, onSelect, onConfig, stackSignature } = useNavMachine({
    startScreen: "gateway",
    keymapProfile: "tallyprime",
  });
  const rootRef = useRef<HTMLDivElement>(null);
  const dispatchScreenAction = useScreenActionDispatcher();
  /** The transient status line: what the last key press actually did. */
  const [status, setStatus] = useState<string | null>(null);

  // Keep the live snapshot in a ref so the DOCUMENT-level key listener (bound
  // once) always reads the current frame without being re-bound per keystroke.
  const snapshotRef = useRef(snapshot);
  snapshotRef.current = snapshot;

  const showStatus = useCallback((message: string | null) => {
    setStatus(message);
  }, []);

  // Auto-expire the status line so it reads as feedback, not as chrome.
  useEffect(() => {
    if (status === null) return;
    const t = window.setTimeout(() => setStatus(null), STATUS_TTL_MS);
    return () => window.clearTimeout(t);
  }, [status]);

  /**
   * The single key handler. It (1) resolves the combo against the keymap FOR
   * THE ACTIVE SCREEN, (2) fires the screen-owned side effect (if any) with the
   * pre-transition snapshot — before the machine's reducer resets the voucher
   * frame and wipes the captured number — (3) forwards the key to the machine,
   * and (4) makes sure the user is TOLD what happened: an unbuilt feature says
   * so, and a key that does nothing on this screen says that too. A silently
   * dead key is the thing a Tally operator cannot forgive.
   */
  const handleKeyDown = useCallback(
    (ev: React.KeyboardEvent | KeyboardEvent) => {
      const snap = snapshotRef.current;
      const combo = comboFromEvent({
        key: ev.key,
        ctrlKey: ev.ctrlKey,
        altKey: ev.altKey,
        shiftKey: ev.shiftKey,
        metaKey: ev.metaKey,
      });
      const action = resolveAction(
        resolveKeymap("tallyprime"),
        snap.screen,
        combo,
      );

      // Unbound key (ordinary typing, arrows inside a list, …): not ours.
      if (!action) return;

      if (action.kind === "unavailable") {
        ev.preventDefault();
        showStatus(`${action.feature} — ${action.note}`);
        return;
      }

      const screenActionKind = SCREEN_ACTIONS[action.kind];
      const effect = screenActionKind
        ? dispatchScreenAction(screenActionKind, snap)
        : { ran: false as const };

      // Did the key move the machine? Measured across the send against the
      // machine's LIVE stack (not the rendered snapshot, which is a tick behind
      // when a SelectionList fires SELECT and KEY in the same keypress).
      const before = stackSignature();
      onKeyDown(ev);
      const machineActs =
        stackSignature() !== before || ALWAYS_MEANINGFUL.has(action.kind);

      // ---- Feedback. Priority: what the screen said > what the key toggled >
      // "this key does nothing here".
      if (effect.message) {
        showStatus(effect.message);
      } else if (effect.ran || machineActs) {
        showStatus(toggleMessage(action, snap));
      } else {
        showStatus(
          `${combo} — ${actionLabel(action)} does not apply on this screen.`,
        );
      }
    },
    [dispatchScreenAction, onKeyDown, showStatus, stackSignature],
  );

  // THE key delivery path. It is bound to the DOCUMENT, not to the root div,
  // because focus is not guaranteed to live inside the app: every screen change
  // unmounts the previously focused control (e.g. the Gateway's autofocused
  // combobox), at which point the browser moves focus to <body> — which is NOT
  // a descendant of the root div, so a React onKeyDown there would never fire.
  // That is exactly what made Esc (and every other shortcut) dead after drilling
  // into a report. A document listener is immune to where focus happens to be.
  useEffect(() => {
    const listener = (ev: KeyboardEvent) => handleKeyDown(ev);
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, [handleKeyDown]);

  // Belt-and-braces focus retention: if a screen change left focus on <body>
  // (no autofocused control on the new screen), pull it back into the app root
  // so screen readers follow the navigation and in-screen arrow keys land.
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const active = document.activeElement;
    if (!active || active === document.body || !root.contains(active)) {
      root.focus({ preventScroll: true });
    }
  }, [snapshot.screen, snapshot.reportName, snapshot.overlay]);

  /** Button-bar clicks dispatch the SAME combo the keyboard would send. */
  const pressCombo = useCallback(
    (combo: string) => {
      const synthetic = syntheticEventFor(combo);
      if (synthetic) handleKeyDown(synthetic);
    },
    [handleKeyDown],
  );

  const bar = buttonBarFor(snapshot.screen, snapshot.voucherType);
  const Screen = registry.resolve(snapshot);
  const needsCompany = COMPANY_SCOPED_SCREENS.has(snapshot.screen);
  const companyReady = company !== null;

  return (
    <div
      ref={rootRef}
      tabIndex={0}
      role="application"
      aria-label="Tally-compatible keystroke engine"
      className="h-full w-full outline-none"
    >
      <Chrome
        companyName={company?.name ?? "No Company"}
        screenTitle={screenTitle(snapshot)}
        buttonBar={bar}
        hints={hintsFor(snapshot)}
        status={status}
        onButton={pressCombo}
      >
        {needsCompany && !companyReady ? (
          <CompanyLoadingScreen />
        ) : Screen ? (
          <Screen snapshot={snapshot} onSelect={onSelect} />
        ) : (
          <UnregisteredScreen snapshot={snapshot} />
        )}
        <FrameOverlayHost
          snapshot={snapshot}
          onConfig={onConfig}
          onClose={() => pressCombo("Esc")}
        />
      </Chrome>
    </div>
  );
}

/**
 * A confirmation for keys that toggle a per-frame view setting, so the user can
 * see the state they just flipped (Tally shows the same thing in its button
 * bar). Returns null for actions whose effect is self-evident on screen.
 */
function toggleMessage(action: KeymapAction, before: NavSnapshot): string | null {
  const on = (v: boolean | undefined) => (v ? "off" : "on"); // pre-toggle value
  switch (action.kind) {
    case "detailed":
      return `Detailed: ${on(before.config?.detailed)}`;
    case "ledgerWise":
      return `Ledger-wise: ${on(before.config?.ledgerWise ?? LEDGER_WISE_DEFAULT)}`;
    case "changeView":
      return `View: ${before.config?.vertical ? "horizontal" : "vertical"}`;
    default:
      return null;
  }
}

/**
 * Build a KeyboardEvent-shaped object for a canonical combo ("Alt+G", "F9",
 * "Ctrl+A") so a mouse click on the button bar takes the exact same code path
 * as the keystroke — one implementation, no drift between mouse and keyboard.
 */
function syntheticEventFor(combo: string): KeyboardEvent | null {
  const parts = combo.split("+");
  const base = parts[parts.length - 1] ?? "";
  const key =
    base === "Esc" ? "Escape" : base === "Space" ? " " : base;
  return {
    key,
    ctrlKey: parts.includes("Ctrl"),
    altKey: parts.includes("Alt"),
    shiftKey: parts.includes("Shift"),
    metaKey: false,
    preventDefault: () => {},
    stopPropagation: () => {},
  } as unknown as KeyboardEvent;
}

/** Placeholder shown while the company bootstrap is in flight (gated screens). */
function CompanyLoadingScreen() {
  return (
    <section
      role="status"
      className="flex h-full items-center justify-center bg-tally-bg p-4 font-mono text-tally-field"
    >
      <p>Preparing company…</p>
    </section>
  );
}

/** Human title for the chrome's title bar, derived from the active frame. */
function screenTitle(snap: NavSnapshot): string {
  switch (snap.screen) {
    case "gateway":
    case "menu":
      return "Gateway of Tally";
    case "voucher": {
      // Title from the voucher CONFIG, not by upper-casing the slug: the slug
      // form rendered "Credit-note Voucher" for the hyphenated types.
      const t = snap.voucherType;
      const mode = snap.mode === "alter" ? " (Alteration)" : "";
      const title = t ? VOUCHER_CONFIGS[t].title : "Voucher";
      return `${title} Voucher${mode}`;
    }
    case "report":
      return snap.reportName ?? "Report";
    case "report-detail":
      return `${snap.reportName ?? "Report"} — ${snap.detailOf ?? "Detail"}`;
    case "master-create":
      return `Create ${(snap.masterKind ?? "ledger").replace("-", " ")}`;
    case "goto-overlay":
      return "Go To";
    case "bill-inbox":
      return "Bill Inbox";
    case "query":
      return "Ask your books";
    case "help":
      return "Help — Keyboard";
    case "features":
      return "Company Features";
    default:
      return "Tally";
  }
}

/** Bottom-strip shortcut hints, scoped to the active screen. */
function hintsFor(snap: NavSnapshot): string[] {
  const common = ["Esc: Back", "Alt+G: Go To", "Ctrl+G: Switch To", "F1: Help"];
  switch (snap.screen) {
    case "voucher":
      return [
        "Enter: Next field",
        "Tab / Shift+Tab: Field",
        "Alt+C: Create Master",
        "Ctrl+H: Change Mode",
        "Ctrl+A: Accept",
        "Ctrl+Q: Quit",
        ...common,
      ];
    case "report":
    case "report-detail":
      return [
        "Enter: Drill down",
        "F2: Period",
        "F12: Configure",
        "Ctrl+F: Filter",
        "Alt+E: Export",
        ...common,
      ];
    case "master-create":
      return ["Enter: Next field", "Ctrl+A: Accept", "Esc: Cancel", "F1: Help"];
    case "goto-overlay":
      return ["Type to search", "Enter: Open", "Esc: Cancel"];
    case "help":
      return ["Esc: Back"];
    case "features":
      return ["Esc: Back", "F1: Help"];
    default:
      return ["Enter: Open", ...common];
  }
}

function UnregisteredScreen({ snapshot }: { snapshot: NavSnapshot }) {
  return (
    <section className="flex h-full items-center justify-center bg-tally-bg p-4 font-mono text-tally-field">
      <p>No screen registered for "{snapshot.screen}".</p>
    </section>
  );
}
