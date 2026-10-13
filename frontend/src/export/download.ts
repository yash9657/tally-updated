import {
  exportFilename,
  mimeTypeFor,
  renderSheet,
  type ExportFormat,
  type ExportSheet,
} from "./sheet";

/**
 * The one place in the frontend that hands a file to the browser.
 *
 * Kept apart from `sheet.ts` so the encoding stays pure and testable — the
 * paisa question is decided in there, and nothing in here may touch a value.
 */

/** What a download attempt did, so the caller can say something true. */
export interface DownloadOutcome {
  readonly ok: boolean;
  readonly filename: string;
  /** Set when `ok` is false: why nothing was written. */
  readonly reason?: string;
}

/**
 * ⛔ THE BYTE-ORDER MARK IS NOT DECORATION.
 *
 * Excel on Windows opens a .csv with the machine's ANSI code page unless the
 * file starts with a UTF-8 BOM. Without it, a party named "Śrī Enterprises" or
 * any amount written with ₹ arrives as mojibake — and unlike a rounding error
 * that is at least visible, so it gets reported. It is still wrong, and it is
 * three bytes to fix.
 *
 * The BOM belongs to the FILE and not to the CSV text: `toCsv` stays clean so
 * its output can be compared byte-for-byte in tests, and `parseCsv` skips a BOM
 * on the way back in.
 */
const UTF8_BOM = "\uFEFF";

/**
 * ⛔ THE ONE ANCHOR. Hand a Blob to the browser as a saved file.
 *
 * There were THREE copies of this before B1 — here (inline in `downloadSheet`),
 * in `usePrint.ts`'s pop-up-blocked fallback, and in `DataScreen.tsx`, whose own
 * comment said it was "deliberately not a third variant" while being one. The
 * fourth was about to be written for Alt+E-as-PDF, so they are now one function.
 *
 * ⚠️ AND THE THREE COPIES DID NOT AGREE ON THE ONE THING THAT MATTERS. Both the
 * print fallback and the Data screen revoke the object URL on a 60-second timer
 * and say why — *"revoking before the browser has read the blob produces an
 * empty file"*. `downloadSheet` revoked it SYNCHRONOUSLY, in a `finally`, right
 * after `a.click()`. That is the hazard the other two document, and it survived
 * because a small CSV is usually snapshotted before the revoke lands. A large
 * PDF is a much better bet to lose. This function revokes LATE, and all three
 * call sites now inherit that.
 *
 * Returns an outcome rather than throwing: in a headless environment (jsdom, an
 * SSR pass) there is no Blob URL to make, and a key press that quietly did
 * nothing is the failure this repository keeps shipping.
 */
export function downloadBlob(blob: Blob, filename: string): DownloadOutcome {
  if (!canSaveAFile()) {
    return {
      ok: false,
      filename,
      reason:
        "This environment cannot save a file (no download support), so nothing was written.",
    };
  }
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.rel = "noopener";
  // Hidden, and IN the document: Firefox ignores a click on a detached anchor.
  a.style.display = "none";
  document.body.appendChild(a);
  a.click();
  a.remove();
  // See the header: late, never in a `finally`.
  window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
  return { ok: true, filename };
}

/** Whether this environment can hand a file to a user at all. */
export function canSaveAFile(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof document !== "undefined" &&
    typeof Blob !== "undefined" &&
    typeof URL !== "undefined" &&
    typeof URL.createObjectURL === "function"
  );
}

/**
 * Render a sheet and hand it to the browser as a download.
 *
 * Returns an outcome rather than throwing or silently doing nothing: in a
 * headless environment (jsdom, an SSR pass) there is no Blob URL to make, and a
 * key press that quietly did nothing is the failure this repository keeps
 * shipping. The caller turns the outcome into a status line.
 */
export function downloadSheet(
  sheet: ExportSheet,
  format: ExportFormat,
): DownloadOutcome {
  const filename = exportFilename(sheet.title, format);
  if (!canSaveAFile()) {
    return {
      ok: false,
      filename,
      reason:
        "This environment cannot save a file (no download support), so nothing was written.",
    };
  }

  // renderSheet may throw ExportShapeError. It is NOT caught here — a misshapen
  // sheet is a bug in the calling screen, and the hook above turns it into a
  // sentence naming the row. Swallowing it here would write a file with figures
  // under the wrong headings.
  //
  // ⛔ AND IT IS CALLED BEFORE downloadBlob, NOT INSIDE IT. A throw must happen
  // before anything is handed to the browser, or a misshapen sheet leaves a
  // half-written file behind and still reports the error.
  const body = renderSheet(sheet, format);
  const text = format === "csv" ? UTF8_BOM + body : body;

  return downloadBlob(new Blob([text], { type: mimeTypeFor(format) }), filename);
}
