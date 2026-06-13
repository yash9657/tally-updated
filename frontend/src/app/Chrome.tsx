import type { ReactNode } from "react";
import type { ButtonBarItem } from "../keystroke/buttonBar";
import { ButtonBar } from "../screens/ButtonBar";

/**
 * The persistent Tally chrome: a top product/company bar, the main screen area,
 * the fixed right-side button bar, and the bottom keyboard-shortcut hint strip.
 * The chrome is screen-agnostic — it lays out whatever the router renders into
 * `children` and shows the button bar the active screen supplies.
 */
export function Chrome({
  companyName,
  screenTitle,
  buttonBar,
  hints,
  children,
}: {
  companyName: string;
  /** Surfaced to assistive tech via the top bar's aria-label, not duplicated as
   * visible text (the active screen body already renders its own heading). */
  screenTitle: string;
  buttonBar: readonly ButtonBarItem[];
  hints: readonly string[];
  children: ReactNode;
}) {
  return (
    <div className="flex h-full w-full flex-col bg-tally-bg font-mono text-tally-field">
      <TopBar companyName={companyName} screenTitle={screenTitle} />
      <div className="flex min-h-0 flex-1">
        <main className="min-w-0 flex-1 overflow-auto" aria-label="Screen">
          {children}
        </main>
        <ButtonBar items={buttonBar} />
      </div>
      <BottomHints hints={hints} />
    </div>
  );
}

/**
 * The product/company top bar. Tally's bar shows the product on the left and
 * the active company on the right; the current screen's name lives in the
 * screen body (so the chrome never duplicates the body heading text). The
 * screen title is exposed to assistive tech via the bar's aria-label.
 */
function TopBar({
  companyName,
  screenTitle,
}: {
  companyName: string;
  screenTitle: string;
}) {
  return (
    <header
      className="flex items-center justify-between bg-tally-panel px-3 py-1 text-sm"
      aria-label={`TallyPrime — ${screenTitle}`}
    >
      <span className="font-bold text-tally-title">TallyPrime</span>
      <span className="text-tally-title">{companyName}</span>
    </header>
  );
}

function BottomHints({ hints }: { hints: readonly string[] }) {
  return (
    <footer
      className="flex flex-wrap gap-x-4 gap-y-0.5 bg-tally-panel px-3 py-1 text-xs text-tally-field"
      aria-label="Shortcut hints"
    >
      {hints.map((h) => (
        <span key={h}>{h}</span>
      ))}
    </footer>
  );
}
