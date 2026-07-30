import { useMemo } from "react";
import type { NavSnapshot } from "../../keystroke/navMachine";
import type { FrameConfig } from "../../keystroke/types";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import { usePrintAction } from "../usePrint";
import type { DrillRow } from "./DrillRows";

/**
 * The shared report VIEW layer: the one place Tally's report buttons actually
 * take effect, so every report honours them identically.
 *
 *   Ctrl+F Filter   -> keep only rows whose text matches (totals always stay)
 *   F12  Configure  -> exclude zero-balance rows, expand all levels
 *   Alt+F1 Detailed -> show every level of a statement tree (else top levels)
 *   Alt+E Export    -> write exactly the ROWS ON SCREEN to a CSV file
 *
 * Rows carry a plain-text `values` array alongside their rendered cells; that
 * is what filtering, the zero-balance test and the CSV export read, so none of
 * them has to parse React nodes.
 */

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
    out = out.filter((r) => (r.depth ?? 0) <= 1);
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

/** True when every numeric value on the row is zero (and there is at least one). */
function isAllZero(values: readonly string[] | undefined): boolean {
  const numbers = (values ?? []).filter((v) => /^-?[\d,]+(\.\d+)?$/.test(v.trim()));
  if (numbers.length === 0) return false;
  return numbers.every((v) => Number(v.replace(/,/g, "")) === 0);
}

/**
 * Hook used by every report screen: applies the view config to its rows and
 * registers the Alt+E export handler for exactly what is on screen.
 */
export function useReportView(
  rows: readonly DrillRow[],
  snapshot: NavSnapshot,
  meta: { title: string; headers: readonly string[] },
): DrillRow[] {
  const visible = useMemo(
    () => applyReportView(rows, snapshot.config),
    [rows, snapshot.config],
  );

  useRegisterScreenAction("export", () => {
    const csv = toCsv(meta.headers, visible);
    const name = `${meta.title.replace(/[^\w]+/g, "-")}.csv`;
    const ok = downloadCsv(name, csv);
    return ok
      ? `Exported ${visible.length} ${visible.length === 1 ? "row" : "rows"} to ${name}`
      : "Export is not available in this environment.";
  });

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

/** Render rows as RFC-4180-ish CSV (quotes doubled, fields always quoted). */
export function toCsv(
  headers: readonly string[],
  rows: readonly DrillRow[],
): string {
  const quote = (s: string) => `"${s.replace(/"/g, '""')}"`;
  const lines = [headers.map(quote).join(",")];
  for (const r of rows) {
    lines.push((r.values ?? []).map(quote).join(","));
  }
  return lines.join("\r\n");
}

/**
 * Trigger a browser download. Returns false where the environment has no Blob
 * URL support (e.g. jsdom in unit tests), so the caller can say so honestly
 * instead of silently claiming success.
 */
function downloadCsv(filename: string, csv: string): boolean {
  if (
    typeof window === "undefined" ||
    typeof URL === "undefined" ||
    typeof URL.createObjectURL !== "function"
  ) {
    return false;
  }
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
  return true;
}
