import { useRef } from "react";
import { useNavMachine } from "../keystroke/useNavMachine";
import { buttonBarFor } from "../keystroke/buttonBar";
import type { NavSnapshot } from "../keystroke/navMachine";
import { useCompanyContext } from "./CompanyContext";
import { Chrome } from "./Chrome";
import { ScreenRegistry } from "./screenRegistry";
import { createDefaultRegistry } from "./defaultScreens";

/**
 * The application shell. Owns the navigation machine, captures REAL DOM key
 * events at the root and feeds them to the machine, then routes the active
 * stack frame to its registered screen component inside the persistent Tally
 * chrome (top bar + right button bar + bottom hints).
 *
 * The shell is screen-content-agnostic: it knows the machine and the chrome,
 * and delegates rendering to the {@link ScreenRegistry}. Agents add screens by
 * registering components; they never edit this file.
 */
export function AppShell({
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

  const bar = buttonBarFor(snapshot.screen, snapshot.voucherType);
  const Screen = registry.resolve(snapshot);

  return (
    <div
      ref={rootRef}
      tabIndex={0}
      role="application"
      aria-label="Tally-compatible keystroke engine"
      onKeyDown={onKeyDown}
      className="h-full w-full outline-none"
    >
      <Chrome
        companyName={company?.name ?? "No Company"}
        screenTitle={screenTitle(snapshot)}
        buttonBar={bar}
        hints={hintsFor(snapshot)}
      >
        {Screen ? (
          <Screen snapshot={snapshot} onSelect={onSelect} />
        ) : (
          <UnregisteredScreen snapshot={snapshot} />
        )}
      </Chrome>
    </div>
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
