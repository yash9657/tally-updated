import { useCallback } from "react";
import { useRegisterScreenAction } from "../app/ScreenActions";
import type { NavSnapshot } from "../keystroke/navMachine";
import { downloadSheet } from "./download";
import {
  ExportShapeError,
  UNBUILT_FORMATS,
  type ExportFormat,
  type ExportSheet,
} from "./sheet";

/**
 * Alt+E — THE ONE EXPORT HANDLER EVERY SCREEN USES.
 *
 * ⛔ ONE HELPER AND NOT A COPY PER SCREEN. Before this hook there was exactly
 * one export registration in the whole frontend — inside `useReportView` — and
 * the six report screens that do NOT go through that hook (Stock Summary, the
 * TDS / TCS Register, the two order books, the two cost summaries) had Alt+E
 * dead while the button bar over four of them advertised "Alt+E Export". This
 * repository's own lesson is that the fifth copy is where copies start to
 * drift, so the money encoding, the file naming, the format choice and every
 * status sentence live here, once.
 *
 * WHAT A SCREEN OWES: a `source` callback that returns either the sheet to
 * write, or a STRING saying why it cannot. The string form is not an error
 * path — it is the normal answer while a report is loading, when the ACL has
 * refused it, and when a filter has emptied it. Every one of those must produce
 * a sentence rather than a silent key, which is the whole reason the handler
 * returns a message at all.
 */
export type ExportSource = (snapshot: NavSnapshot) => ExportSheet | string;

/**
 * Which format Alt+E writes. Read from the FRAME's config so F12 Configure can
 * change it, the same way every other report view setting works — there is no
 * parallel store and no per-screen state to keep in sync.
 */
export function exportFormatFor(snapshot: NavSnapshot): ExportFormat {
  return snapshot.config?.exportExcelXml ? "excel-xml" : "csv";
}

/**
 * A one-line reminder of what Alt+E does NOT offer, appended to the success
 * message the first time is not worth tracking — so it is not appended at all.
 * It is exported for the F12 panel and the Features screen to state honestly.
 *
 * Tally's Alt+E also exports PDF/JPEG/HTML and can e-mail the document. None of
 * that is built. Saying so where the operator looks is the same discipline the
 * keymap follows for every unavailable combo.
 */
export const EXPORT_NOT_BUILT_NOTE = `Not built: ${UNBUILT_FORMATS.join(", ")}.`;

/** Register Alt+E for the active screen. */
export function useExportAction(source: ExportSource): void {
  useRegisterScreenAction(
    "export",
    useCallback(
      (snapshot: NavSnapshot): string => {
        const sheet = source(snapshot);
        if (typeof sheet === "string") return sheet;
        if (sheet.rows.length === 0) {
          return (
            `Nothing to export — ${sheet.title} has no rows on screen. ` +
            `(Alt+E writes exactly what is displayed, so a filter or F12 ` +
            `setting that hides everything exports nothing.)`
          );
        }
        const format = exportFormatFor(snapshot);
        try {
          const outcome = downloadSheet(sheet, format);
          if (!outcome.ok) {
            return `Export failed. ${outcome.reason ?? ""}`.trim();
          }
          const n = sheet.rows.length;
          return `Exported ${n} ${n === 1 ? "row" : "rows"} to ${outcome.filename}.`;
        } catch (err: unknown) {
          // A misshapen sheet. It names the offending row; do not soften it —
          // the alternative is a file whose credits are under the debit column.
          if (err instanceof ExportShapeError) return err.message;
          return `Export failed: ${err instanceof Error ? err.message : String(err)}`;
        }
      },
      [source],
    ),
  );
}
