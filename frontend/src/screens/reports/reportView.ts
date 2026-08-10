import { useCallback, useEffect, useMemo, useState } from "react";
import type { NavSnapshot } from "../../keystroke/navMachine";
import type { FrameConfig } from "../../keystroke/types";
import { usePrintAction } from "../usePrint";
import { useExportAction } from "../../export/useExportAction";
import { textCell, type ExportRow, type ExportSheet } from "../../export/sheet";
import { ApiError } from "../../api";
import type { DrillRow } from "./DrillRows";

/**
 * The shared report VIEW layer: the one place Tally's report buttons actually
 * take effect, so every report honours them identically.
 *
 *   Ctrl+F Filter   -> keep only rows whose text matches (totals always stay)
 *   F12  Configure  -> exclude zero-balance rows, expand all levels
 *   Alt+F1 Detailed -> show every level of a statement tree (else top levels)
 *   Alt+E Export    -> write exactly the ROWS ON SCREEN to a spreadsheet file
 *
 * Rows carry a plain-text `values` array alongside their rendered cells; that
 * is what filtering and the zero-balance test read, so neither has to parse
 * React nodes.
 *
 * ⛔ AND THE EXPORT DOES **NOT** READ `values`. It reads `exportCells`, and the
 * difference is the whole T15 track. `values` holds the DISPLAY strings — the
 * output of `formatMoney`, which is Indian-grouped ("12,34,567.89") and
 * truncated to two decimals. Exporting those shipped a thousands comma into a
 * comma-separated file and threw away two digits of a fixed-4 amount before the
 * spreadsheet had a chance to. A row that omits `exportCells` falls back to
 * `values` as plain text, which is correct for a report with no figures on it
 * and wrong for one with figures — so every screen that shows money supplies
 * them. See export/sheet.ts for what "exact" costs and buys.
 */

/**
 * WHETHER THERE IS A REPORT UNDER THE KEY AT ALL — and it is not a detail.
 *
 * ⛔ THE DEFECT THIS TYPE EXISTS TO CLOSE. Until now the five statement screens
 * fetched with a bare `.then()` and no `.catch()`, so a 403 from the PDP left
 * `data` at null forever. What that produced was not an error: the Trial Balance
 * and the Outstandings both PUSH a total row unconditionally, so a refused
 * report rendered a tidy "Total 0.00 / 0.00" and Alt+E cheerfully wrote it to a
 * file — a file of zeroes, correctly formatted, that an operator would read as
 * "the books are empty" rather than "you were not allowed to see this". The
 * Day Book, Balance Sheet and P&L were only marginally better: they answered
 * "Nothing to export — Day Book has no rows on screen", which states a fact
 * about the BOOKS when the truth is a fact about the OPERATOR.
 *
 * A refusal is a normal answer in this system (migration 0011 seeds a coarse
 * `allow display report`, and later migrations take individual reports back
 * off Data Entry), so it gets a first-class state rather than being inferred
 * from an absence. The screen renders it and Alt+E says it, from this one
 * value, which is the only way the two cannot drift.
 */
export type ReportAvailability =
  | { readonly kind: "ready" }
  | { readonly kind: "loading" }
  | { readonly kind: "failed"; readonly status: number; readonly message: string };

/** Normalize a rejected report fetch. A non-ApiError carries status 0. */
export function reportFailure(err: unknown): ReportAvailability & {
  kind: "failed";
} {
  if (err instanceof ApiError) {
    return { kind: "failed", status: err.status, message: err.message };
  }
  return {
    kind: "failed",
    status: 0,
    message: err instanceof Error ? err.message : String(err),
  };
}

/**
 * The sentence Alt+E answers when the report itself never arrived.
 *
 * A 403 is named as a 403 — "your security level may not open this" — because
 * the operator's next move is to ask an Owner, and "nothing to export" sends
 * them to look for missing vouchers instead. Anything else quotes the server
 * verbatim, for the reason usePrint gives: rewriting a status here is how a
 * refusal starts reading like an outage.
 */
export function exportUnavailableMessage(
  label: string,
  availability: ReportAvailability,
): string | null {
  if (availability.kind === "loading") {
    return `${label} is still loading — nothing to export yet.`;
  }
  if (availability.kind !== "failed") return null;
  if (availability.status === 403) {
    return (
      `Your security level may not open the ${label}, so there is nothing to ` +
      `export. An Owner can grant it in Gateway > Users & Roles.`
    );
  }
  return (
    `${label} unavailable${availability.status ? ` (${availability.status})` : ""}, ` +
    `so there is nothing to export. ${availability.message}`
  ).trim();
}

/**
 * Fetch a report, keeping BOTH answers the screen has to be able to give.
 *
 * The success path is byte-for-byte what the five screens did before (the
 * previous response stays on screen while a period change refetches, rather
 * than blinking through a loading state); the addition is that a rejection is
 * CAUGHT and published instead of becoming an unhandled promise rejection that
 * leaves the screen looking empty.
 *
 * `load` must be a stable callback — wrap it in useCallback with the query's
 * own dependencies, exactly as the useEffect it replaces was keyed.
 */
export function useReportFetch<T>(load: () => Promise<T>): {
  data: T | null;
  availability: ReportAvailability;
} {
  const [data, setData] = useState<T | null>(null);
  const [failure, setFailure] = useState<
    (ReportAvailability & { kind: "failed" }) | null
  >(null);

  useEffect(() => {
    let cancelled = false;
    load()
      .then((res) => {
        if (cancelled) return;
        setData(res);
        setFailure(null);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setFailure(reportFailure(err));
      });
    return () => {
      cancelled = true;
    };
  }, [load]);

  // A failure WINS over stale rows: a refetch that was refused must not leave
  // the previous period's figures on screen under the new period's banner.
  const availability: ReportAvailability = failure
    ? failure
    : data === null
      ? { kind: "loading" }
      : { kind: "ready" };
  return { data, availability };
}

/** Apply the frame's view config to a report's rows. Pure — directly testable. */
export function applyReportView(
  rows: readonly DrillRow[],
  config: FrameConfig | undefined,
): DrillRow[] {
  const cfg = config ?? {};
  let out = rows.slice();

  // Condensed (the default) shows the top levels of a statement tree; Detailed
  // expands every level. Flat reports (depth undefined) are unaffected.
  if (!cfg.detailed) {
    out = condense(out);
  }

  if (cfg.hideZeroBalances) {
    out = out.filter((r) => r.drillable === false || !isAllZero(r.values));
  }

  const needle = cfg.filterText?.trim().toLowerCase();
  if (needle) {
    out = out.filter(
      (r) =>
        r.drillable === false ||
        (r.values ?? []).join(" ").toLowerCase().includes(needle),
    );
  }
  return out;
}

/**
 * The condensed (default) view of a statement tree.
 *
 * ⛔ THE RULE IS NOT "DEPTH <= 1". IT IS "ONLY HIDE WHAT THE OPERATOR CAN STILL
 * REACH ANOTHER WAY" — AND THE DIFFERENCE WAS DRIVEN IN A BROWSER.
 *
 * This used to be a bare `filter((r) => (r.depth ?? 0) <= 1)`, and on the
 * Balance Sheet that made the **Closing Stock** line — the single largest asset
 * a trading company owns — INVISIBLE in the view every operator gets by
 * default, and (because Alt+E writes the rows on screen) ABSENT from the export
 * as well. Measured on 2026-08-07: a sheet whose Current Assets subtotal
 * included 72,837.15 of stock that appeared on no line of the statement or of
 * its CSV, while the row with the SAME NAME on the Profit & Loss — where the
 * Trading-account terms are appended at section level, i.e. depth 1 — was
 * always visible. One figure, two statements, one of them silently short a row.
 *
 * TWO PROPERTIES, AND THE SECOND IS THE ONE THAT WAS MISSING.
 *
 *  1. A row is REACHABLE if it can be drilled to. A group or a ledger at depth
 *     2 is hidden here and still reachable: Enter on its parent pushes a detail
 *     screen that lists it. Hiding it condenses the statement — which is what
 *     Tally's Condensed/Detailed pair is FOR.
 *  2. A row with `drillable === false` is a COMPUTED or TOTAL line. Nothing
 *     drills to it, no detail screen lists it, and no other screen states it. If
 *     the condensed view drops it, it exists NOWHERE in the product for an
 *     operator who never presses Alt+F1 — a figure inside a subtotal with no
 *     line of its own. So a non-drillable row is kept at EVERY depth.
 *
 * Note that the other two filters below already carve out exactly this case
 * ("totals always stay"): this one was written without it, which is the whole
 * defect.
 *
 * ANCESTORS COME BACK WITH IT. A kept deep row is re-parented onto its chain of
 * enclosing groups, so the tree stays well formed — "Closing Stock" prints under
 * "Stock-in-Hand" under "Current Assets", and the subtotal that contains it is
 * on screen next to it. Without that the row appears indented beneath a group
 * that is NOT its parent, which reads as a mis-stated statement, and the export
 * writes a child whose parent is missing.
 */
function condense(rows: readonly DrillRow[]): DrillRow[] {
  const keep = rows.map(() => false);
  for (let i = 0; i < rows.length; i++) {
    const row = rows[i]!;
    const depth = row.depth ?? 0;
    if (depth > 1 && row.drillable !== false) continue;
    keep[i] = true;
    // Walk BACK to the enclosing groups: the nearest preceding row that is
    // shallower than the level we are looking for is this row's parent, and so
    // on up to the section. Rows arrive in tree order (flattenNodes emits a
    // node then its children), which is what makes this correct.
    let wanted = depth;
    for (let j = i - 1; j >= 0 && wanted > 0; j--) {
      const above = rows[j]!.depth ?? 0;
      if (above < wanted) {
        keep[j] = true;
        wanted = above;
      }
    }
  }
  return rows.filter((_, i) => keep[i]!);
}

/** True when every numeric value on the row is zero (and there is at least one). */
function isAllZero(values: readonly string[] | undefined): boolean {
  const numbers = (values ?? []).filter((v) => /^-?[\d,]+(\.\d+)?$/.test(v.trim()));
  if (numbers.length === 0) return false;
  return numbers.every((v) => Number(v.replace(/,/g, "")) === 0);
}

/**
 * The export cells for one row: what the screen declared, or its display text
 * as a last resort. The fallback is deliberately NOT clever — it does not try
 * to detect a money column and un-format it, because un-formatting "12,34,567.89"
 * cannot recover the two decimals `formatMoney` already dropped. A screen that
 * needs exact figures says so by supplying `exportCells`.
 */
export function exportRowFor(row: DrillRow): ExportRow {
  return row.exportCells ?? (row.values ?? []).map(textCell);
}

/** The default availability: a screen that fetches nothing is always ready. */
const READY: ReportAvailability = { kind: "ready" };

/** Build the sheet Alt+E writes for a report's visible rows. */
export function reportSheet(
  title: string,
  headers: readonly string[],
  rows: readonly DrillRow[],
): ExportSheet {
  return { title, headers, rows: rows.map(exportRowFor) };
}

/**
 * Hook used by every report screen: applies the view config to its rows and
 * registers the Alt+E export handler for exactly what is on screen.
 */
export function useReportView(
  rows: readonly DrillRow[],
  snapshot: NavSnapshot,
  meta: {
    title: string;
    headers: readonly string[];
    /**
     * Whether the report arrived. Omitted means "ready", which is what a screen
     * that builds its rows locally (no fetch) should say.
     */
    availability?: ReportAvailability;
    /**
     * What to CALL the report in a refusal sentence, when that differs from the
     * export title. `title` becomes the FILE NAME ("Profit-and-Loss.csv"), so
     * the P&L cannot use its screen heading there — and a status line reading
     * "Profit and Loss" over a screen headed "Profit & Loss A/c" is the same
     * quiet mismatch the view banner exists to prevent.
     */
    label?: string;
  },
): DrillRow[] {
  const visible = useMemo(
    () => applyReportView(rows, snapshot.config),
    [rows, snapshot.config],
  );

  const { title, headers } = meta;
  const availability = meta.availability ?? READY;
  const label = meta.label ?? title;
  useExportAction(
    useCallback(() => {
      // ⛔ THE REFUSAL IS AN EXPORT ANSWER. Checked BEFORE the sheet is built,
      // because a refused Trial Balance still has a total row in `rows` and
      // would otherwise export as a file of zeroes.
      const unavailable = exportUnavailableMessage(label, availability);
      if (unavailable !== null) return unavailable;
      return reportSheet(title, headers, visible);
    }, [availability, headers, label, title, visible]),
  );

  // Ctrl+P / Alt+P. Registered HERE, in the hook every report screen already
  // calls, rather than once per screen — six registrations would be six chances
  // to forget one, and a report whose Print key silently did nothing is exactly
  // the class of gap this change exists to close.
  //
  // Note the ASYMMETRY with export above, and that it is deliberate: export
  // writes the rows on screen (after the F12/Ctrl+F view is applied), while print
  // asks the SERVER for the statement. So a filtered screen exports the filter and
  // prints the whole statement. The status line says "the whole statement" on a
  // drill-down for the same reason.
  usePrintAction();

  return visible;
}
