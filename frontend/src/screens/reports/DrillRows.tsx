import { useCallback, useEffect, useRef, useState } from "react";
import type { ExportRow } from "../../export/sheet";

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
  /**
   * WHAT KIND OF ROW THIS IS, in the report's own words — `Section`, `Group`,
   * `Ledger`, `Computed`, `Total`, `Party`, `Bill` (D109, D118).
   *
   * ⛔ IT IS FOR THE EXPORT AND NOT FOR THE SCREEN. On screen a row's kind is in
   * its indentation and its CSS — the section header is bold, a child is `pl-4`
   * — and CSS does not survive Alt+E, which is the whole defect: a file whose
   * parents and children look alike sums to a multiple of its own total.
   *
   * ⛔ DECLARED, NOT INFERRED. It could be read off the `rowKey` prefix, and
   * that inference would be wrong the first time a screen picked a prefix for a
   * NAVIGATION reason — which is what those prefixes are for. It is one word at
   * the site that already knows the answer.
   *
   * A screen supplies it on EVERY row or on none; `useReportView`'s `levels`
   * flag is what turns the column on. See reportView.ts.
   */
  readonly level?: string;
  /** The rendered cells, laid out by the parent's column template. */
  readonly cells: React.ReactNode;
  /**
   * The row's cells as PLAIN TEXT, in column order — the DISPLAY text, exactly
   * as rendered. Filtering (Ctrl+F) and the zero-balance test (F12) read this
   * instead of trying to parse rendered React nodes, so an operator filtering on
   * "12,34,567" matches what their eyes matched.
   */
  readonly values?: readonly string[];
  /**
   * The row's cells for EXPORT (Alt+E), in column order.
   *
   * ⛔ SEPARATE FROM `values`, AND IT HAS TO BE. `values` is what is on screen:
   * money in it has been through `formatMoney`, which groups it with commas and
   * truncates it to two decimals. Writing that into a comma-separated file is
   * two defects in one cell — a delimiter inside a field, and two lost digits of
   * a fixed-4 amount, neither recoverable by the reader. `exportCells` carries
   * the wire values with a declared kind (see export/sheet.ts), so a spreadsheet
   * gets the exact characters the server sent.
   *
   * A row that omits it exports its `values` as plain text. That is correct only
   * for rows with no figures on them.
   */
  readonly exportCells?: ExportRow;
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

  // Take focus when the report opens, so Arrow Up/Down (handled locally, below)
  // reach this grid instead of falling on <body>. Global keys (Enter/Esc/F-keys)
  // are handled on the document by the shell and work regardless — this is only
  // about the in-screen row cursor. Focus is taken ONLY if nothing inside the
  // app has it already, so it can never steal the caret from a form field.
  useEffect(() => {
    const el = listRef.current;
    if (!el) return;
    const active = document.activeElement;
    const idle =
      !active ||
      active === document.body ||
      active.getAttribute?.("role") === "application";
    if (idle) el.focus({ preventScroll: true });
  }, []);

  /**
   * ⛔ THE CURSOR IS CLAMPED TO THE ROWS THAT ARE ACTUALLY THERE.
   *
   * `active` is an INDEX into a list this component does not own, and the list
   * changes under it: F2 narrows the period, Ctrl+F filters, F12 hides zero
   * balances, and the same mounted component is re-used when the shell renders
   * another frame of the same kind. Nothing reset the index, so it could point
   * PAST THE END — at which point `drillable[active]` is undefined, the publish
   * below was skipped entirely, and the machine went on holding the row key of a
   * voucher from the PREVIOUS view. On the Day Book that is the whole defect:
   * Enter (and therefore Ctrl+P on the frame it pushes) opened, printed or
   * refused a DIFFERENT DOCUMENT than the one the operator was looking at.
   *
   * Clamping is not enough on its own — see the Enter handler below.
   */
  const activeIndex = drillable.length === 0 ? 0 : Math.min(active, drillable.length - 1);
  const activeKey = drillable[activeIndex]?.rowKey;

  /**
   * The identity of the ROW SET, so a different list can be told from a
   * re-render of the same one. Cheap (rows are a screenful, not a book) and it
   * is the only thing this component can see that changes when the report does.
   */
  // The separator is an ESCAPE, not a literal control character in the source.
  // A raw NUL byte here made git classify this .tsx as BINARY, so every diff of
  // this file read "Binary files differ" and the change that introduced it could
  // not be reviewed at all.
  const rowSignature = drillable.map((r) => r.rowKey).join("\u0000");

  // Whenever the active drillable row — or the SET it is an index into —
  // changes, publish it to the machine so a subsequent Enter (handled by the
  // shell) drills into the right row.
  //
  // ⚠️ `rowSignature` IS IN THE DEPENDENCIES AND IT IS LOAD-BEARING. Keyed on
  // [activeKey] alone, a report that came back with the SAME key at the cursor
  // (a re-opened Day Book, a refetch of the same period) published nothing at
  // all, because from this effect's point of view nothing had changed — while
  // the frame underneath was new and its `selectedItem` was empty.
  useEffect(() => {
    if (activeKey !== undefined) onSelect(activeKey);
    // onSelect identity is stable (useCallback in the shell); we only want to
    // re-publish when the highlighted row, or the report under it, changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeKey, rowSignature]);

  const move = useCallback(
    (delta: number) => {
      setActive((a) => {
        // From the CLAMPED position, not the stored one: after the list shrank,
        // one Arrow Up from a stale index 12 in a 3-row report must land on row
        // 1, not on row 11 of a report that has three.
        const last = Math.max(drillable.length - 1, 0);
        const cur = Math.min(a, last);
        return Math.min(Math.max(cur + delta, 0), last);
      });
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
    } else if (ev.key === "Enter") {
      // ⛔⛔ THE DRILL TARGET IS PUBLISHED AT THE MOMENT OF THE DRILL, and this
      // is the line that makes a stale target impossible rather than unlikely.
      //
      // The effect above publishes when the highlight MOVES. Everything that can
      // make it not fire — a re-used component instance under a freshly pushed
      // frame, a list that changed to one carrying the same key at the cursor, a
      // cursor that was out of range — leaves the machine's `selectedItem`
      // holding some other row, and the shell's Enter reads exactly that field to
      // decide which voucher to open. Re-publishing here means the row the
      // operator can SEE highlighted is the row that is drilled, whatever the
      // frame's history.
      //
      // ⚠️ ORDERING, AND IT IS WHY THIS IS NOT REDUNDANT. Enter is deliberately
      // NOT consumed (no preventDefault, no stopPropagation): it goes on bubbling
      // to the shell's document-level listener, which is where the drill happens.
      // React's own handlers run at the app root, BELOW `document`, so this
      // SELECT reaches the machine before that KEY does — the machine processes
      // events synchronously, so the frame's `selectedItem` is already correct
      // when Enter is reduced.
      if (activeKey !== undefined) onSelect(activeKey);
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
        // The CLAMPED index: the highlight has to be on the row that will be
        // published, or the operator is reading one row and drilling another.
        const isActive = isDrillable && drillIndex === activeIndex;
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
