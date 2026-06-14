import { useCallback, useRef } from "react";
import { useNavMachine } from "../keystroke/useNavMachine";
import { buttonBarFor } from "../keystroke/buttonBar";
import type { NavSnapshot } from "../keystroke/navMachine";
import { resolveKeymap } from "../keystroke/keymaps/tallyprime";
import { normalizeCombo, comboFromEvent } from "../keystroke/normalize";
import { useCompanyContext } from "./CompanyContext";
import { ScreenActionsProvider, useScreenActionDispatcher } from "./ScreenActions";
import { LoadedVoucherProvider } from "./LoadedVoucher";
import { LoadedDraftProvider } from "./LoadedDraft";
import { Chrome } from "./Chrome";
import { ScreenRegistry } from "./screenRegistry";
import { createDefaultRegistry } from "./defaultScreens";

/** Screens that require an active company scope (useCompanyID would throw). */
const COMPANY_SCOPED_SCREENS = new Set<NavSnapshot["screen"]>([
  "voucher",
  "report",
  "report-detail",
  "master-create",
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
  const { snapshot, onKeyDown, onSelect } = useNavMachine({
    startScreen: "gateway",
    keymapProfile: "tallyprime",
  });
  const rootRef = useRef<HTMLDivElement>(null);
  const dispatchScreenAction = useScreenActionDispatcher();

  // Wrap the machine's keydown so that an `accept` (Ctrl+A) or `delete` (Alt+D)
  // fires the active screen's registered side-effect handler with the snapshot
  // AS IT IS NOW — before the machine's reducer resets the voucher frame and
  // wipes the captured number. The machine transition still runs (navMachine
  // stays pure); the client call is the screen's responsibility.
  const handleKeyDown = useCallback(
    (ev: React.KeyboardEvent) => {
      const combo = comboFromEvent({
        key: ev.key,
        ctrlKey: ev.ctrlKey,
        altKey: ev.altKey,
        shiftKey: ev.shiftKey,
        metaKey: ev.metaKey,
      });
      const action = resolveKeymap("tallyprime").bindings[normalizeCombo(combo)];
      if (action?.kind === "accept") {
        dispatchScreenAction("accept", snapshot);
      } else if (action?.kind === "delete") {
        dispatchScreenAction("delete", snapshot);
      }
      onKeyDown(ev);
    },
    [dispatchScreenAction, onKeyDown, snapshot],
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
      onKeyDown={handleKeyDown}
      className="h-full w-full outline-none"
    >
      <Chrome
        companyName={company?.name ?? "No Company"}
        screenTitle={screenTitle(snapshot)}
        buttonBar={bar}
        hints={hintsFor(snapshot)}
      >
        {needsCompany && !companyReady ? (
          <CompanyLoadingScreen />
        ) : Screen ? (
          <Screen snapshot={snapshot} onSelect={onSelect} />
        ) : (
          <UnregisteredScreen snapshot={snapshot} />
        )}
      </Chrome>
    </div>
  );
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
      const t = snap.voucherType ?? "voucher";
      const mode = snap.mode === "alter" ? " (Alteration)" : "";
      return `${t[0]?.toUpperCase()}${t.slice(1)} Voucher${mode}`;
    }
    case "report":
      return snap.reportName ?? "Report";
    case "report-detail":
      return `${snap.reportName ?? "Report"} — ${snap.detailOf ?? "Detail"}`;
    case "master-create":
      return `Create ${(snap.masterKind ?? "ledger").replace("-", " ")}`;
    case "goto-overlay":
      return "Go To";
    default:
      return "Tally";
  }
}

/** Bottom-strip shortcut hints, scoped to the active screen. */
function hintsFor(snap: NavSnapshot): string[] {
  const common = ["Esc: Back", "Alt+G: Go To", "Ctrl+G: Switch To"];
  switch (snap.screen) {
    case "voucher":
      return [
        "Enter: Next field",
        "Alt+C: Create Master",
        "Ctrl+H: Change Mode",
        "Ctrl+A: Accept",
        ...common,
      ];
    case "report":
    case "report-detail":
      return ["Enter: Drill down", "F2: Period", ...common];
    case "master-create":
      return ["Enter: Next field", "Ctrl+A: Accept", "Esc: Cancel"];
    case "goto-overlay":
      return ["Type to search", "Enter: Open", "Esc: Cancel"];
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
