import { useState } from "react";
import type { NavSnapshot } from "../../keystroke/navMachine";
import type { FrameConfig } from "../../keystroke/types";

/**
 * The in-frame overlays behind Tally's F12 Configure, F2/Alt+F2 Period, Ctrl+F
 * Filter and Ctrl+N Calculator buttons.
 *
 * They are rendered by the shell ABOVE the active screen and are dismissed with
 * Esc (the navigation machine closes the overlay and keeps the frame, exactly
 * like Tally's sub-screens). Every setting they change is written to the FRAME's
 * config through `onConfig`, so the report screens below re-render from the
 * snapshot they already receive — no parallel store to keep in sync.
 *
 * The Ctrl+H Change Mode picker is NOT here: it belongs to the voucher screen
 * (it changes the voucher's entry mode, not a view setting).
 */
export function FrameOverlayHost({
  snapshot,
  onConfig,
  onClose,
}: {
  snapshot: NavSnapshot;
  onConfig: (patch: FrameConfig) => void;
  onClose: () => void;
}) {
  switch (snapshot.overlay) {
    case "configure":
      return (
        <ConfigureOverlay
          snapshot={snapshot}
          onConfig={onConfig}
          onClose={onClose}
        />
      );
    case "period":
      return (
        <PeriodOverlay
          snapshot={snapshot}
          onConfig={onConfig}
          onClose={onClose}
        />
      );
    case "filter":
      return (
        <FilterOverlay
          snapshot={snapshot}
          onConfig={onConfig}
          onClose={onClose}
        />
      );
    case "calculator":
      return <CalculatorOverlay onClose={onClose} />;
    default:
      return null;
  }
}

/** Shared modal frame: Tally renders sub-screens as a panel over the screen. */
function OverlayPanel({
  title,
  children,
  footer,
}: {
  title: string;
  children: React.ReactNode;
  footer?: string;
}) {
  return (
    <div className="pointer-events-none fixed inset-0 z-20 flex items-start justify-center pt-24">
      <div
        role="dialog"
        aria-label={title}
        className="pointer-events-auto w-96 border border-tally-accent bg-tally-panel p-3 font-mono text-sm text-tally-field shadow-lg"
      >
        <h2 className="mb-2 font-bold text-tally-title">{title}</h2>
        {children}
        <p className="mt-3 text-xs text-tally-field">
          {footer ?? "Enter: accept   Esc: cancel"}
        </p>
      </div>
    </div>
  );
}

/** One F12 toggle row: label + Yes/No, flipped by click (or by its own key). */
function ToggleRow({
  label,
  value,
  onToggle,
  testId,
}: {
  label: string;
  value: boolean;
  onToggle: () => void;
  testId: string;
}) {
  return (
    <button
      type="button"
      data-testid={testId}
      aria-pressed={value}
      onMouseDown={(e) => e.preventDefault()}
      onClick={onToggle}
      className="flex w-full items-center justify-between px-1 py-0.5 text-left hover:bg-tally-accent"
    >
      <span>{label}</span>
      <span className="font-bold text-tally-title">{value ? "Yes" : "No"}</span>
    </button>
  );
}

/**
 * F12 Configure. The options are the ones this build can honestly honour — each
 * is read by the report screens (see ReportScreens.tsx applyConfig) rather than
 * being a decorative switch.
 */
function ConfigureOverlay({
  snapshot,
  onConfig,
  onClose,
}: {
  snapshot: NavSnapshot;
  onConfig: (patch: FrameConfig) => void;
  onClose: () => void;
}) {
  const cfg = snapshot.config ?? {};
  const isReport =
    snapshot.screen === "report" || snapshot.screen === "report-detail";

  if (!isReport) {
    return (
      <OverlayPanel title="Configure" footer="Esc: close">
        <p className="text-xs">
          This screen has no configuration options in this build. F12 configures
          reports (detail level, zero balances, narration, view).
        </p>
      </OverlayPanel>
    );
  }

  return (
    <OverlayPanel title={`Configure — ${snapshot.reportName ?? "Report"}`}>
      <div className="flex flex-col gap-px">
        <ToggleRow
          testId="cfg-detailed"
          label="Expand all levels (Detailed)"
          value={Boolean(cfg.detailed)}
          onToggle={() => onConfig({ detailed: !cfg.detailed })}
        />
        <ToggleRow
          testId="cfg-hide-zero"
          label="Exclude rows with zero closing balance"
          value={Boolean(cfg.hideZeroBalances)}
          onToggle={() => onConfig({ hideZeroBalances: !cfg.hideZeroBalances })}
        />
        <ToggleRow
          testId="cfg-narration"
          label="Show narration (Day Book)"
          value={Boolean(cfg.showNarration)}
          onToggle={() => onConfig({ showNarration: !cfg.showNarration })}
        />
        <ToggleRow
          testId="cfg-ledger-wise"
          label="Ledger-wise (Trial Balance)"
          value={Boolean(cfg.ledgerWise)}
          onToggle={() => onConfig({ ledgerWise: !cfg.ledgerWise })}
        />
        <ToggleRow
          testId="cfg-vertical"
          label="Vertical view (Balance Sheet / P&L)"
          value={Boolean(cfg.vertical)}
          onToggle={() => onConfig({ vertical: !cfg.vertical })}
        />
      </div>
      <button
        type="button"
        className="mt-2 bg-tally-active px-2 py-0.5 text-xs text-white"
        onMouseDown={(e) => e.preventDefault()}
        onClick={onClose}
      >
        Close
      </button>
    </OverlayPanel>
  );
}

/**
 * F2 / Alt+F2 Period, applied SERVER-SIDE by every report that has a date
 * dimension. Which bounds a report uses depends on what kind of statement it is,
 * and the panel says so rather than implying a range that two of them cannot
 * honour:
 *
 *  - Day Book and Profit & Loss are FOR A PERIOD — both bounds apply.
 *  - Trial Balance, Balance Sheet and Outstandings are POSITIONS AS AT a date —
 *    only "To" applies. Bounding a position below would drop the entity's
 *    earlier history and still foot, which is a wrong answer that looks right.
 */
function PeriodOverlay({
  snapshot,
  onConfig,
  onClose,
}: {
  snapshot: NavSnapshot;
  onConfig: (patch: FrameConfig) => void;
  onClose: () => void;
}) {
  const cfg = snapshot.config ?? {};
  const [from, setFrom] = useState(cfg.periodFrom ?? "");
  const [to, setTo] = useState(cfg.periodTo ?? "");
  // Which reports read only the upper bound (they are positions, not periods).
  const asAtOnly =
    snapshot.reportName === "Trial Balance" ||
    snapshot.reportName === "Balance Sheet" ||
    snapshot.reportName === "Outstandings";
  const periodAware =
    asAtOnly ||
    snapshot.reportName === "Day Book" ||
    snapshot.reportName === "Profit & Loss A/c";

  return (
    <OverlayPanel title="Period">
      <label className="mb-1 flex items-center gap-2">
        <span className="w-12">From</span>
        <input
          // eslint-disable-next-line jsx-a11y/no-autofocus
          autoFocus
          type="date"
          aria-label="Period from"
          data-testid="period-from"
          className="bg-tally-field px-1 text-tally-fieldText outline-none"
          value={from}
          // Applied as it changes (not only on the Apply button), so pressing
          // Enter — which closes the overlay — never discards what was typed.
          onChange={(e) => {
            setFrom(e.target.value);
            onConfig({ periodFrom: e.target.value });
          }}
        />
      </label>
      <label className="flex items-center gap-2">
        <span className="w-12">To</span>
        <input
          type="date"
          aria-label="Period to"
          data-testid="period-to"
          className="bg-tally-field px-1 text-tally-fieldText outline-none"
          value={to}
          onChange={(e) => {
            setTo(e.target.value);
            onConfig({ periodTo: e.target.value });
          }}
        />
      </label>
      {asAtOnly ? (
        <p data-testid="period-as-at-note" className="mt-2 text-xs">
          Note: {snapshot.reportName} is a POSITION as at a date, so only “To”
          applies to it. “From” is ignored here on purpose — bounding a position
          below would drop earlier history and still balance.
        </p>
      ) : !periodAware ? (
        <p className="mt-2 text-xs">
          Note: {snapshot.reportName ?? "This report"} has no date dimension in
          this build, so a period is not applied to it.
        </p>
      ) : null}
      <button
        type="button"
        data-testid="period-apply"
        className="mt-2 bg-tally-active px-2 py-0.5 text-xs text-white"
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => {
          onConfig({ periodFrom: from, periodTo: to });
          onClose();
        }}
      >
        Apply
      </button>
    </OverlayPanel>
  );
}

/** Ctrl+F Filter — a case-insensitive substring filter over the report rows. */
function FilterOverlay({
  snapshot,
  onConfig,
  onClose,
}: {
  snapshot: NavSnapshot;
  onConfig: (patch: FrameConfig) => void;
  onClose: () => void;
}) {
  const [text, setText] = useState(snapshot.config?.filterText ?? "");
  return (
    <OverlayPanel title="Filter rows">
      <input
        // eslint-disable-next-line jsx-a11y/no-autofocus
        autoFocus
        aria-label="Filter rows"
        data-testid="filter-text"
        className="w-full bg-tally-field px-1 text-tally-fieldText outline-none"
        value={text}
        placeholder="e.g. Cash, Sundry, INV/"
        onChange={(e) => {
          setText(e.target.value);
          // Apply as you type — Tally filters incrementally.
          onConfig({ filterText: e.target.value });
        }}
      />
      <button
        type="button"
        data-testid="filter-clear"
        className="mt-2 bg-tally-active px-2 py-0.5 text-xs text-white"
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => {
          setText("");
          onConfig({ filterText: "" });
          onClose();
        }}
      >
        Clear
      </button>
    </OverlayPanel>
  );
}

/**
 * Ctrl+N Calculator. Evaluates + - * / and parentheses with a tiny
 * recursive-descent parser — deliberately NOT `eval` (an accounting app must
 * never execute user text) and deliberately not float-formatted beyond 4dp,
 * matching the money precision used everywhere else.
 */
function CalculatorOverlay({ onClose }: { onClose: () => void }) {
  const [expr, setExpr] = useState("");
  const result = expr.trim() ? safeEvaluate(expr) : null;
  return (
    <OverlayPanel title="Calculator" footer="Esc: close">
      <input
        // eslint-disable-next-line jsx-a11y/no-autofocus
        autoFocus
        aria-label="Calculator expression"
        data-testid="calc-input"
        className="w-full bg-tally-field px-1 text-tally-fieldText outline-none"
        value={expr}
        placeholder="e.g. 118000/1.18"
        onChange={(e) => setExpr(e.target.value)}
      />
      <p data-testid="calc-result" className="mt-2 text-tally-title">
        {result === null ? "" : result}
      </p>
      <button
        type="button"
        className="mt-2 bg-tally-active px-2 py-0.5 text-xs text-white"
        onMouseDown={(e) => e.preventDefault()}
        onClick={onClose}
      >
        Close
      </button>
    </OverlayPanel>
  );
}

/**
 * Evaluate a decimal arithmetic expression (+ - * / and parentheses) without
 * `eval`. Returns the value fixed to 4 decimals, or an error string. Exported
 * for direct unit testing.
 */
export function safeEvaluate(input: string): string {
  const tokens = input.match(/\d+(?:\.\d+)?|[()+\-*/]/g);
  if (!tokens || tokens.join("").replace(/\s/g, "") !== input.replace(/\s/g, "")) {
    return "invalid expression";
  }
  let pos = 0;
  const peek = () => tokens[pos];
  const eat = (t: string) => {
    if (peek() === t) {
      pos += 1;
      return true;
    }
    return false;
  };

  // expr := term (('+'|'-') term)* ; term := factor (('*'|'/') factor)* ;
  // factor := '-'? ( number | '(' expr ')' )
  const parseExpr = (): number => {
    let value = parseTerm();
    for (;;) {
      if (eat("+")) value += parseTerm();
      else if (eat("-")) value -= parseTerm();
      else return value;
    }
  };
  const parseTerm = (): number => {
    let value = parseFactor();
    for (;;) {
      if (eat("*")) value *= parseFactor();
      else if (eat("/")) {
        const d = parseFactor();
        if (d === 0) throw new Error("divide by zero");
        value /= d;
      } else return value;
    }
  };
  const parseFactor = (): number => {
    if (eat("-")) return -parseFactor();
    if (eat("(")) {
      const v = parseExpr();
      if (!eat(")")) throw new Error("unbalanced");
      return v;
    }
    const t = peek();
    if (t === undefined || !/^\d/.test(t)) throw new Error("expected number");
    pos += 1;
    return Number(t);
  };

  try {
    const value = parseExpr();
    if (pos !== tokens.length) return "invalid expression";
    if (!Number.isFinite(value)) return "invalid expression";
    return value.toFixed(4);
  } catch (err) {
    return err instanceof Error && err.message === "divide by zero"
      ? "divide by zero"
      : "invalid expression";
  }
}
