import type { StatutoryFileDownload } from "../../api";

/**
 * Hand a statutory payroll ARCHIVE to the browser.
 *
 * ⛔ IT SAVES THE ZIP AND NEVER UNWRAPS IT. cored serves the ECR, the ESI return
 * and Form 138 as an archive holding exactly two entries — the statutory file
 * under its statutory filename, and `<name>.README.txt` carrying the disclosure
 * that NOTHING here has been through the NSDL/Protean FVU, the EPFO Unified
 * Portal or the ESIC employer portal, none of which can run in this environment.
 *
 * The archive exists because none of the three formats permits an in-file
 * comment: the ECR is a bare member list the portal parses line by line, the
 * Form 138 file must begin with its FH record, and the ESI CSV must begin with
 * the ESIC column headers. So the disclosure can only travel BESIDE the file —
 * and a client that opened the zip and handed over only the file would be
 * separating the two, which is the one thing the zip exists to prevent.
 *
 * ⛔ THEREFORE: DO NOT ADD AN "extract the file for me" CONVENIENCE HERE. It
 * would be a one-line feature that quietly removes the only statement anybody
 * ever reads about whether the file has been validated.
 *
 * Written as its own module, beside `export/download.ts` rather than inside it,
 * because that module's whole job is RENDERING a sheet and this one must not
 * touch the bytes at all: they came from the server and any transformation
 * applied here would be applied to a statutory record.
 */

/** What saving did, so the caller can say something true about it. */
export interface SaveOutcome {
  readonly ok: boolean;
  readonly filename: string;
  /** Set when `ok` is false: why nothing was written. */
  readonly reason?: string;
}

/**
 * Save the archive under the server's own download name.
 *
 * Returns an outcome rather than throwing: in a headless environment (jsdom, an
 * SSR pass) there is no Blob URL to make, and a key press that quietly did
 * nothing is the failure this repository keeps shipping.
 */
export function saveStatutoryArchive(
  download: StatutoryFileDownload,
): SaveOutcome {
  // The SERVER'S name. It is derived from the statutory filename and the period,
  // so honouring it is the difference between saving "ECR-2026-07.zip" and
  // saving "download.zip" — and on a folder holding twelve months of returns,
  // the second one is a file nobody can identify a week later.
  const filename = download.filename ?? "statutory-file.zip";
  if (
    typeof window === "undefined" ||
    typeof document === "undefined" ||
    typeof URL === "undefined" ||
    typeof URL.createObjectURL !== "function"
  ) {
    return {
      ok: false,
      filename,
      reason:
        "This environment cannot save a file (no download support), so nothing was written.",
    };
  }
  const url = URL.createObjectURL(download.blob);
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    // Hidden, and in the document: Firefox ignores a click on a detached anchor.
    a.style.display = "none";
    document.body.appendChild(a);
    a.click();
    a.remove();
  } finally {
    URL.revokeObjectURL(url);
  }
  return { ok: true, filename };
}
