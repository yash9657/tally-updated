import { useRef } from "react";
import { useNavMachine } from "./keystroke/useNavMachine";
import { buttonBarFor } from "./keystroke/buttonBar";
import { ButtonBar } from "./screens/ButtonBar";
import { GatewayScreen } from "./screens/GatewayScreen";
import { VoucherScreen } from "./screens/VoucherScreen";
import { SelectionList } from "./screens/SelectionList";

/**
 * Application shell. Owns the navigation machine, captures real keyboard events
 * at the root, and routes to the screen for the active stack frame. The fixed
 * right button bar is always present and context-scoped to the active screen.
 */
export function App() {
  const { snapshot, onKeyDown, onSelect } = useNavMachine({
    startScreen: "gateway",
    keymapProfile: "tallyprime",
  });
  const rootRef = useRef<HTMLDivElement>(null);

  const bar = buttonBarFor(snapshot.screen, snapshot.voucherType);

  return (
    <div
      ref={rootRef}
      tabIndex={0}
      role="application"
      aria-label="Tally-compatible keystroke engine"
      onKeyDown={onKeyDown}
      className="flex h-full w-full bg-tally-bg outline-none"
    >
      <main className="flex-1 overflow-auto">
        {snapshot.screen === "voucher" ? (
          <VoucherScreen snapshot={snapshot} onSelect={onSelect} />
        ) : snapshot.screen === "report" ? (
          <ReportScreen name={snapshot.reportName ?? "Report"} />
        ) : snapshot.screen === "goto-overlay" ? (
          <GoToOverlay onSelect={onSelect} />
        ) : (
          <GatewayScreen onSelect={onSelect} />
        )}
      </main>
      <ButtonBar items={bar} />
    </div>
  );
}

function ReportScreen({ name }: { name: string }) {
  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">{name}</h1>
      <p className="mt-2 text-sm">
        Press Enter on a line to drill into the underlying voucher; Esc to go back.
      </p>
    </section>
  );
}

function GoToOverlay({ onSelect }: { onSelect: (item: string) => void }) {
  return (
    <section className="flex h-full items-start justify-center bg-tally-bg/80 p-8">
      <div className="w-80 bg-tally-panel p-3 font-mono text-tally-field">
        <h2 className="mb-2 text-tally-title">Go To</h2>
        <SelectionList
          label="Search"
          options={[
            "Day Book",
            "Balance Sheet",
            "Profit & Loss A/c",
            "Stock Summary",
          ]}
          onSelect={onSelect}
          autoFocus
        />
      </div>
    </section>
  );
}
