import { useCallback, useEffect, useRef, useState } from "react";

/**
 * A keyboard-navigable list of drillable report rows — the shared interaction
 * primitive behind every report screen (RESEARCH.md sec 8: "no report is a dead
 * end", every line drills with Enter).
 *
 * Division of responsibility with the navigation machine:
 *  - The AppShell owns global Enter/Esc. Enter on a report frame pushes the
 *    child screen (report-detail or the underlying voucher); Esc pops.
 *  - This component owns ONLY the in-screen ROW HIGHLIGHT. Whenever the
 *    highlighted row changes (arrow keys, mouse) it calls `onSelect(rowKey)`,
 *    which the machine records as the frame's `selectedItem`. The subsequent
 *    Enter the shell receives then drills into THAT row (the machine reads
 *    `selectedItem` to set the child's `detailOf`).
 *  - Crucially this primitive does NOT swallow Enter (no stopPropagation): the
 *    keydown bubbles to the shell's root handler so the machine performs the
 *    drill. Arrow keys are consumed locally.
 *
 * Each row exposes `rowKey` (fed to onSelect) separately from its rendered
 * cells, so the drill target can be a stable group/ledger/party/voucher
 * identifier while the row still renders a rich columnar layout.
 */

export interface DrillRow {
  /** Stable key fed to onSelect (becomes the machine's selectedItem). */
  readonly rowKey: string;
  /** Optional indent depth for tree-shaped statements (group > ledger). */
  readonly depth?: number;
  /** Whether this row is drillable (false = a total/computed line). */
  readonly drillable?: boolean;
  /** The rendered cells, laid out by the parent's column template. */
  readonly cells: React.ReactNode;
}

export function DrillRows({
  rows,
  onSelect,
  ariaLabel,
  columnsClassName,
}: {
  rows: readonly DrillRow[];
  onSelect: (rowKey: string) => void;
  ariaLabel: string;
  /** Tailwind grid-template-columns class shared by header + every row. */
  columnsClassName: string;
}) {
  const drillable = rows.filter((r) => r.drillable !== false);
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);

  // Whenever the active drillable row changes, publish it to the machine so a
  // subsequent Enter (handled by the shell) drills into the right row.
  const activeKey = drillable[active]?.rowKey;
  useEffect(() => {
    if (activeKey !== undefined) onSelect(activeKey);
    // onSelect identity is stable (useCallback in the shell); we only want to
    // re-publish when the highlighted row actually changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeKey]);

  const move = useCallback(
    (delta: number) => {
      setActive((a) =>
        Math.min(Math.max(a + delta, 0), Math.max(drillable.length - 1, 0)),
      );
    },
    [drillable.length],
  );

  function handleKeyDown(ev: React.KeyboardEvent<HTMLDivElement>) {
    // Consume vertical navigation locally; let Enter/Esc bubble to the shell.
    if (ev.key === "ArrowDown") {
      ev.preventDefault();
      move(1);
    } else if (ev.key === "ArrowUp") {
      ev.preventDefault();
      move(-1);
    }
  }

  let drillIndex = -1;
  return (
    <div
      ref={listRef}
      role="grid"
      aria-label={ariaLabel}
      tabIndex={0}
      onKeyDown={handleKeyDown}
      className="outline-none"
    >
      {rows.map((row) => {
        const isDrillable = row.drillable !== false;
        if (isDrillable) drillIndex += 1;
        const isActive = isDrillable && drillIndex === active;
        const depth = row.depth ?? 0;
        return (
          <div
            key={`${row.rowKey}:${drillIndex}`}
            role="row"
            aria-selected={isActive}
            data-rowkey={row.rowKey}
            data-drillable={isDrillable}
            onMouseEnter={
              isDrillable
                ? (() => {
                    const idx = drillIndex;
                    return () => setActive(idx);
                  })()
                : undefined
            }
            onMouseDown={
              isDrillable
                ? (e) => {
                    e.preventDefault();
                    onSelect(row.rowKey);
                  }
                : undefined
            }
            className={[
              "grid items-center gap-2 px-2 py-0.5",
              columnsClassName,
              isActive ? "bg-tally-active text-white" : "text-tally-fieldText",
              isDrillable ? "cursor-pointer" : "font-bold text-tally-title",
            ].join(" ")}
            style={depth ? { paddingLeft: `${0.5 + depth * 1.25}rem` } : undefined}
          >
            {row.cells}
          </div>
        );
      })}
    </div>
  );
}

/**
 * Column header strip sharing the row grid template. Right-aligned numeric
 * headers match Tally's ledger columns.
 */
export function ColumnHeader({
  labels,
  columnsClassName,
}: {
  labels: readonly { text: string; numeric?: boolean }[];
  columnsClassName: string;
}) {
  return (
    <div
      role="row"
      className={`grid items-center gap-2 border-b border-tally-accent px-2 py-0.5 font-bold text-tally-title ${columnsClassName}`}
    >
      {labels.map((l, i) => (
        <span
          key={`${l.text}:${i}`}
          role="columnheader"
          className={l.numeric ? "text-right" : ""}
        >
          {l.text}
        </span>
      ))}
    </div>
  );
}
