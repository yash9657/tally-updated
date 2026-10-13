import { useCallback } from "react";

import type { NavSnapshot } from "../keystroke/navMachine";
import { useRegisterScreenAction } from "../app/ScreenActions";
import { useCompanyContext } from "../app/CompanyContext";
import { ApiError } from "../api/http";
import { downloadBlob } from "./download";
import {
  resolvePrintTarget,
  type PrintTarget,
  type PrintVoucherContext,
} from "../screens/printTarget";
import { buildMailtoURL, draftFor, emailHandoffMessage } from "./emailDocument";

/**
 * ALT+M — E-MAIL A DOCUMENT (B2). The effect half.
 *
 * ⛔⛔ THE ORDER IS THE WHOLE SAFETY PROPERTY: DOWNLOAD, THEN DRAFT. A draft that
 * opens with no file saved is the failure this feature exists around — the
 * operator types an address, sends, and the customer gets an e-mail about an
 * invoice with no invoice on it. So the PDF is fetched and written to disk FIRST,
 * and if either step fails **the draft does not open at all** and the message says
 * why. `emailDocument.test.ts` asserts the ORDERING, not merely that both
 * happened, and a mutation that opens the draft first turns it red.
 *
 * ⛔ IT IS AN ACTION AND NOT A FOURTH `ExportTarget`, AND THAT WAS A DELIBERATE
 * DECISION rather than a convenience:
 *
 *   1. **It is Tally's own key.** TallyPrime's top menu is `E: Export` and
 *      `M: E-mail` — two separate entries (RESEARCH.md:361), Alt+M is documented
 *      as its own global (:415), the report key table lists it (:774), and the
 *      export page says "Alt+M e-mails the report" alongside — not among — Alt+E's
 *      format list (:817). Four independent places, all agreeing that e-mail is not
 *      a format.
 *   2. **Every `ExportTarget` writes a file and stops.** This writes a file and
 *      then LEAVES THE APPLICATION, handing the operator a manual step. A fourth
 *      "format" that silently opens a mail client is a surprise.
 *   3. `"e-mail this document"` sitting in `UNBUILT_FORMATS` was itself the drift
 *      it caused; it is removed in the same change, because Tally never treated it
 *      as a format either.
 *
 * ✅ IT REUSES `resolvePrintTarget` WHOLESALE, so Alt+M, Ctrl+P and Alt+E-as-PDF
 * can never disagree about what the document on this screen is. A refusal is
 * FINAL AND LOCAL: no request, no download, no draft — just the sentence.
 */

/** How the draft is opened. Injectable so tests need no navigation. */
export type DraftSink = (url: string) => void;

/**
 * The default sink: hand the `mailto:` URL to the browser, which hands it to the
 * operating system's mail client.
 *
 * ⚠️ `location.href` AND NOT `window.open`. A pop-up blocker silently swallows
 * `window.open`, and this call happens after an await — so it is no longer inside
 * the user gesture that blockers require, and it is exactly the case they block.
 * Assigning `location.href` to a `mailto:` does not navigate the page: the browser
 * recognises the scheme, hands it off, and leaves the document where it is.
 */
export function openMailDraft(url: string): void {
  window.location.href = url;
}

export interface UseEmailActionOptions {
  /** What the screen knows about the voucher on it, exactly as Ctrl+P takes it. */
  voucher?: PrintVoucherContext;
  /** Override the draft sink (tests). */
  draftSink?: DraftSink;
  /**
   * Override the file sink (tests).
   *
   * Defaults to the ONE shared download anchor. ⛔ Do not write another: there
   * were three near-copies and they disagreed about when to revoke the object URL
   * (two revoked late and said why, one revoked synchronously in a `finally` and
   * produced empty files). `downloadBlob` is the survivor.
   */
  fileSink?: (blob: Blob, filename: string) => { ok: boolean; filename: string; reason?: string };
}

/** Register Alt+M for the active screen. Returns nothing: the key is the API. */
export function useEmailDocumentAction(
  options: UseEmailActionOptions = {},
): void {
  const {
    voucher,
    draftSink = openMailDraft,
    fileSink = downloadBlob,
  } = options;
  const { company, client } = useCompanyContext();

  const handler = useCallback(
    async (snapshot: NavSnapshot): Promise<string> => {
      const target: PrintTarget = resolvePrintTarget(snapshot, voucher);
      // ⛔ A REFUSAL IS FINAL AND LOCAL — no fetch, no file, no draft. The same
      // invariant Ctrl+P holds (I-5): a refusal that still hits the network is a
      // 404 in somebody's log, and here it would also open an empty draft about a
      // document that does not exist.
      if (target.kind === "refusal") return target.reason;

      if (!company) return "Select a company first.";

      let blob: Blob;
      let serverFilename: string | null;
      try {
        const got =
          target.kind === "voucher"
            ? await client.printVoucher(
                company.id,
                target.voucherClass,
                target.voucherID,
              )
            : await client.printReport(company.id, target.slug, target.query);
        blob = got.blob;
        serverFilename = got.filename;
      } catch (err) {
        // The server's own sentence, unaltered — the rule usePrint and
        // exportAsPdf both follow. ⛔ AND NO DRAFT: there is nothing to attach,
        // so opening one would invite exactly the empty-invoice e-mail this
        // feature is shaped to prevent.
        if (err instanceof ApiError) {
          return `Could not e-mail ${target.label}: ${err.message}`;
        }
        return `Could not e-mail ${target.label}: ${
          err instanceof Error ? err.message : String(err)
        }`;
      }

      // The SERVER names the file (Content-Disposition); the fallback only covers
      // a server that sent no header.
      const name =
        serverFilename ??
        `${target.kind === "report" ? target.slug : target.voucherClass}.pdf`;

      // ---- STEP 1: THE FILE. Before the draft, always.
      //
      // ⚠️ WRAPPED, BECAUSE A THROW HERE USED TO ESCAPE. `downloadBlob` returns an
      // outcome for the environment it EXPECTS to fail in (headless, no Blob
      // URLs), but `URL.createObjectURL` can also throw outright — and an
      // exception on this line left the key doing nothing at all: no file, no
      // draft, and no status line either, because the rejection escaped the
      // handler. Found by a probe that simulated the failure as a throw.
      let outcome: { ok: boolean; filename: string; reason?: string };
      try {
        outcome = fileSink(blob, name);
      } catch (err) {
        outcome = {
          ok: false,
          filename: name,
          reason: err instanceof Error ? err.message : String(err),
        };
      }
      if (!outcome.ok) {
        // ⛔ NO DRAFT ON A FAILED SAVE. This is the branch that matters: an
        // operator handed a draft with nothing on disk has been set up to send an
        // empty invoice, and they would have no way to know.
        return (
          `Could not e-mail ${target.label}: the PDF could not be saved, so no ` +
          `draft was opened. ${outcome.reason ?? ""}`
        ).trim();
      }

      // ---- STEP 2: THE DRAFT.
      const { url, draft } = buildMailtoURL(
        draftFor(target, company.name ?? ""),
      );
      draftSink(url);

      return emailHandoffMessage(target.label, outcome.filename, draft.trimmed);
    },
    [client, company, draftSink, fileSink, voucher],
  );

  useRegisterScreenAction("emailDocument", handler);
}
