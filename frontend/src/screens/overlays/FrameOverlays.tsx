import { useState } from "react";
import type { NavSnapshot } from "../../keystroke/navMachine";
import type { FrameConfig } from "../../keystroke/types";
import { EXPORT_NOT_BUILT_NOTE } from "../../export/useExportAction";

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
        {/* Alt+E's format. A toggle rather than a picker because there are two
            formats and only two: see EXPORT_NOT_BUILT_NOTE for the ones Tally
            offers that this build does not. */}
        <ToggleRow
          testId="cfg-export-excel-xml"
          label="Export (Alt+E) as Excel XML instead of CSV"
          value={Boolean(cfg.exportExcelXml)}
          onToggle={() => onConfig({ exportExcelXml: !cfg.exportExcelXml })}
        />
      </div>
      <p className="mt-2 text-xs opacity-80" data-testid="cfg-export-note">
        Alt+E writes the rows on screen, with every amount as TEXT so a
        spreadsheet cannot round the paisa off it. {EXPORT_NOT_BUILT_NOTE}
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
 * F2 / Alt+F2 Period, applied SERVER-SIDE by every report that has a date
 * dimension. Which bounds a report uses depends on what kind of statement it is,
 * and the panel says so rather than implying a range that two of them cannot
 * honour:
 *
 *  - Day Book, Profit & Loss and the TDS / TCS Register are FOR A PERIOD — both
 *    bounds apply.
 *  - Trial Balance, Balance Sheet, Outstandings and Stock Summary are POSITIONS
 *    AS AT a date — only "To" applies. Bounding a position below would drop the
 *    entity's earlier history and still foot, which is a wrong answer that looks
 *    right.
 *
 * ⚠️ THESE ARE TWO ALLOWLISTS, AND A REPORT MISSING FROM BOTH IS TOLD ITS
 * PERIOD DOES NOTHING. That is the failure mode, and it shipped twice: the TDS /
 * TCS Register and the Stock Summary both fell through to "has no date dimension
 * in this build, so a period is not applied to it" while reading the period and
 * acting on it. On the register it was flatly self-contradictory — the screen
 * behind the dialog reads "Press F2 to set the period. Both dates are needed",
 * and the dialog on top of it said the period would be ignored.
 *
 * Found by driving the app, not by reading it, and not by any of the 461 tests.
 * TestEveryPeriodReadingScreenIsClassified is the guard: the CLASSIFICATION is
 * derived from what each screen actually reads — periodFrom AND periodTo means a
 * period, periodTo alone means a position — so a new report cannot be silently
 * told its dates do nothing.
 */
/**
 * Reports that are a POSITION AS AT a date — only the upper bound applies.
 * Each one reads `config.periodTo` and nothing else; that is the test.
 */
export const AS_AT_ONLY_REPORTS = [
  "Trial Balance",
  "Balance Sheet",
  "Outstandings",
  // Closing stock is a position, not a flow: StockSummaryScreen reads
  // `periodTo` as `asOn` and never reads `periodFrom`. It was in NEITHER list
  // and was therefore told its period did nothing.
  "Stock Summary",
  // ---- THE TWO ORDER BOOKS (T3). Both are POSITIONS as at a date.
  //
  // An outstanding commitment either still stands on the date asked about or it
  // does not, so only the upper bound is meaningful and both screens read
  // `periodTo` alone as `as_on`. Bounding one BELOW would be actively wrong rather
  // than merely useless: it would drop the orders PLACED before the window while
  // leaving their deliveries inside it, so a long-open order would read as fully
  // delivered — the outstanding balance is derived from both halves, and dropping
  // only one half of a subtraction produces a confident wrong answer. That is the
  // same trap AS_AT_ONLY_REPORTS exists for ("Bounding a position below would drop
  // the entity's earlier history and still foot").
  "Sales Order Book",
  "Purchase Order Book",
];

/**
 * Reports that are FOR A PERIOD — both bounds apply. Each one reads
 * `config.periodFrom` AND `config.periodTo`.
 */
export const FULL_PERIOD_REPORTS = [
  "Day Book",
  "Profit & Loss A/c",
  // The register is filed per quarter and refuses to render without BOTH dates
  // — it is the most period-driven report in the product, and it was the one
  // being told its period was ignored.
  "TDS / TCS Register",
  // ---- THE TWO T2 COST SUMMARIES.
  //
  // Both are FLOWS between two dates and neither is a position: a cost summary
  // sums the ALLOCATIONS of postings dated in the range, so bounding it below is
  // not merely allowed but required — "what did Sales cost as at 31 March" is not
  // a question, and answering it with everything since the company was formed
  // would look exactly like a year's figure. Both screens read `periodFrom` AND
  // `periodTo` and refuse to render without both, which is the test
  // periodClassification.test.ts applies to the source.
  //
  // ⚠️ THEY ARE ScreenKinds, NOT `report` FRAMES, so `snapshot.reportName` is
  // undefined on them and matching these strings needs the screen->label table
  // below. Without it both would fall into the "has no date dimension in this
  // build" branch — the exact defect that shipped twice, on screens whose own
  // headings ask for a period.
  "Cost Centre Summary",
  "Cost Category Summary",
];

/**
 * ScreenKind -> the name that screen is classified under above.
 *
 * The three allowlists are keyed by REPORT NAME because every report that had a
 * period was once a `report` frame. The two cost summaries are ScreenKinds (see
 * the ScreenKind doc in keystroke/types.ts for why), so they would match nothing
 * and be told their dates do nothing while reading them.
 *
 * Exported so periodClassification.test.ts can assert the same closure over
 * SCREENS that it asserts over registered report names: a period-reading screen
 * that is in this table but in none of the three lists fails there.
 */
export const PERIOD_SCREEN_LABELS: Partial<Record<string, string>> = {
  "cost-centre-summary": "Cost Centre Summary",
  "cost-category-summary": "Cost Category Summary",
};

/**
 * Reports that genuinely have no date dimension. Being on THIS list is a
 * decision; being on no list at all was the bug.
 */
export const NO_DATE_DIMENSION_REPORTS: string[] = [];

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
  // Which screens read only the upper bound (they are positions, not periods).
  // The Bank Reconciliation Statement is one: it answers "what had cleared AS AT
  // this date", and an item cleared later is un-cleared as at the statement date
  // (docs/design/t4-model.md's first trap).
  const isBankRecon = snapshot.screen === "bank-reconciliation";
  // A ScreenKind report (the two cost summaries) carries no reportName, so the
  // name it is CLASSIFIED under comes from the screen. Without this the classifier
  // sees "" and reports "no date dimension" over a screen that refuses to render
  // without both dates.
  const classifiedAs =
    PERIOD_SCREEN_LABELS[snapshot.screen] ?? snapshot.reportName ?? "";
  const asAtOnly = isBankRecon || AS_AT_ONLY_REPORTS.includes(classifiedAs);
  const periodAware = asAtOnly || FULL_PERIOD_REPORTS.includes(classifiedAs);
  const asAtLabel = isBankRecon
    ? "The Bank Reconciliation Statement"
    : classifiedAs;

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
          Note: {asAtLabel} is a POSITION as at a date, so only “To”
          applies to it. “From” is ignored here on purpose — bounding a position
          below would drop earlier history and still balance.
        </p>
      ) : !periodAware ? (
        <p className="mt-2 text-xs">
          Note: {classifiedAs === "" ? "This report" : classifiedAs} has no date
          dimension in this build, so a period is not applied to it.
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
