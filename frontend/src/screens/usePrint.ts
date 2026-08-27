import { useCallback } from "react";
import type { NavSnapshot } from "../keystroke/navMachine";
import { useRegisterScreenAction } from "../app/ScreenActions";
import { useCompanyContext } from "../app/CompanyContext";
import { ApiError } from "../api/http";
import { downloadBlob } from "../export/download";
import { useEmailDocumentAction } from "../export/useEmailAction";
import {
  resolvePrintTarget,
  type PrintTarget,
  type PrintVoucherContext,
} from "./printTarget";

/**
 * Ctrl+P / Alt+P, wired. The screen-owned half of printing (track T1).
 *
 * The decision — what does this screen print, or why can it not — is
 * screens/printTarget.ts and is pure. This hook is the effect: it registers the
 * `print` screen action, fetches the PDF, and hands it to the browser.
 *
 * TWO THINGS IT MUST NEVER DO, both of them stated as tests in printTarget.test.ts
 * and in the memo's invariants:
 *
 *  - It must not turn a REFUSAL into an HTTP call (I-5). A refusal that still hits
 *    the network is a 404 in somebody's log and, on the print routes, a 404 that
 *    looks exactly like a probe.
 *  - It must not rewrite the server's message. A class mismatch answers 404 with
 *    a deliberately class-neutral body, because a 403 there would confirm that a
 *    voucher with that id exists in this company; and a report that does not print
 *    answers 501. Both are shown verbatim.
 */

/** How a fetched PDF reaches the user. Injectable so tests need no DOM plumbing. */
export type PdfSink = (blob: Blob, filename: string | null) => void;

/**
 * The default sink: hand the PDF to the browser in a new tab, where the built-in
 * viewer's own print command takes over.
 *
 * A web page cannot silently drive a printer, and it should not try: the operator
 * chooses the tray, the copies and the paper. What matters is that the bytes they
 * print are the SERVER'S document — laid out against Rule 46 — and not a
 * screenshot of this screen, which is what Ctrl+P did before it was bound.
 */
export function openPdfInBrowser(blob: Blob, filename: string | null): void {
  const url = URL.createObjectURL(blob);
  const win = window.open(url, "_blank");
  if (win) {
    // Revoke late: revoking before the viewer has read the blob shows a blank tab.
    window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
    return;
  }
  // Pop-up blocked: fall back to a download so the document is not simply lost.
  //
  // ⛔ THROUGH THE SHARED ANCHOR (export/download.ts), which is now the only one
  // in the frontend. This was one of three near-copies that disagreed about when
  // to revoke the object URL; see downloadBlob's header.
  URL.revokeObjectURL(url);
  downloadBlob(blob, filename ?? "document.pdf");
}

export interface UsePrintOptions {
  /**
   * What the screen knows about the voucher on it. Voucher screens pass the
   * loaded (or just-saved) id; report screens pass nothing.
   */
  voucher?: PrintVoucherContext;
  /** Override the PDF sink (tests). */
  sink?: PdfSink;
}

/**
 * Register Ctrl+P for the active screen. Returns nothing: the key is the API.
 *
 * The handler is async and returns its message, which the shell awaits — so a
 * refusal that only the server knows about still reaches the status line.
 */
export function usePrintAction(options: UsePrintOptions = {}): void {
  const { voucher, sink = openPdfInBrowser } = options;
  const { company, client } = useCompanyContext();

  // ⛔ ALT+M IS REGISTERED HERE, FROM THE SAME OPTIONS, AND THAT IS A DESIGN
  // DECISION RATHER THAN A SHORTCUT.
  //
  // There are 23 call sites of this hook. Wiring Alt+M into each of them would be
  // 23 chances to forget one — and a forgotten one is SILENT: the key resolves,
  // the shell finds no handler for that screen, and nothing happens. Registering
  // both here makes "a screen that can PRINT its document can also E-MAIL it" a
  // structural invariant instead of a checklist, which is the right invariant:
  // Alt+M is "print it, but hand it to your mail client instead of the printer",
  // and both keys ask the SAME `resolvePrintTarget` what the document is. A screen
  // where printing is refused refuses e-mail with the identical sentence.
  //
  // ⚠️ SO THIS HOOK BINDS TWO KEYS DESPITE ITS NAME. It is not renamed because
  // renaming would touch all 23 sites for no behavioural gain; the coupling is
  // asserted by a test instead (`emailDocument.test.tsx` mounts a screen and
  // presses both).
  useEmailDocumentAction({ voucher });

  const handler = useCallback(
    async (snapshot: NavSnapshot): Promise<string> => {
      const target: PrintTarget = resolvePrintTarget(snapshot, voucher);
      // A refusal is final and LOCAL. No request is made.
      if (target.kind === "refusal") return target.reason;

      if (!company) return "Select a company first.";

      try {
        const { blob, filename } =
          target.kind === "voucher"
            ? await client.printVoucher(
                company.id,
                target.voucherClass,
                target.voucherID,
              )
            : await client.printReport(company.id, target.slug, target.query);
        sink(blob, filename);
        return `Printed ${target.label}${filename ? ` — ${filename}` : ""}`;
      } catch (err) {
        // The server's own sentence, unaltered. See this file's header for why
        // "helpfully" rewriting a 404 here would leak what it protects.
        if (err instanceof ApiError) {
          return `Could not print ${target.label}: ${err.message}`;
        }
        return `Could not print ${target.label}: ${
          err instanceof Error ? err.message : String(err)
        }`;
      }
    },
    [client, company, sink, voucher],
  );

  useRegisterScreenAction("print", handler);
}
