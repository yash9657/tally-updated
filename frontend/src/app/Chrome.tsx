import type { ReactNode } from "react";
import type { ButtonBarItem } from "../keystroke/buttonBar";
import { ButtonBar } from "../screens/ButtonBar";

/**
 * The persistent Tally chrome: a top product/company bar, the main screen area,
 * the fixed right-side button bar, a status line, and the bottom keyboard-hint
 * strip. The chrome is screen-agnostic — it lays out whatever the router renders
 * into `children` and shows the button bar the active screen supplies.
 */
export function Chrome({
  companyName,
  screenTitle,
  buttonBar,
  hints,
  status,
  onButton,
  children,
}: {
  companyName: string;
  /** Surfaced to assistive tech via the top bar's aria-label, not duplicated as
   * visible text (the active screen body already renders its own heading). */
  screenTitle: string;
  buttonBar: readonly ButtonBarItem[];
  hints: readonly string[];
  /** What the last key press did (or why it did nothing). Null hides the line. */
  status?: string | null;
  /** Fire a button-bar item as if its combo had been typed. */
  onButton?: (combo: string) => void;
  children: ReactNode;
}) {
  return (
    <div className="flex h-full w-full flex-col bg-tally-bg font-mono text-tally-field">
      <TopBar companyName={companyName} screenTitle={screenTitle} />
      <div className="flex min-h-0 flex-1">
        <main className="min-w-0 flex-1 overflow-auto" aria-label="Screen">
          {children}
        </main>
        <ButtonBar items={buttonBar} onPress={onButton} />
      </div>
      <StatusLine status={status ?? null} />
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

/**
 * The status line: the shell's answer to "what did that key just do?". Every
 * bound key writes here — including keys whose feature is not built and keys
 * that do not apply on the active screen — so no keystroke is ever silently
 * swallowed. It is a live region so assistive tech announces it too.
 */
function StatusLine({ status }: { status: string | null }) {
  return (
    <div
      aria-live="polite"
      data-testid="status-line"
      className="min-h-[1.25rem] bg-tally-bg px-3 py-0.5 text-xs text-tally-title"
    >
      {status}
    </div>
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
