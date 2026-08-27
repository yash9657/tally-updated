import { useCallback } from "react";
import { useRegisterScreenAction } from "../app/ScreenActions";
import { useCompanyContext } from "../app/CompanyContext";
import { ApiError } from "../api/http";
import type { CoredClient } from "../api/client";
import type { NavSnapshot } from "../keystroke/navMachine";
import { resolvePrintTarget, printsALayout } from "../screens/printTarget";
import { downloadBlob, downloadSheet } from "./download";
import {
  ExportShapeError,
  UNBUILT_FORMATS,
  type ExportFormat,
  type ExportSheet,
  type ExportTarget,
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
 * What Alt+E will produce on this frame — the SHEET format, or the server's PDF.
 *
 * ⛔ THE ONE PLACE THREE STATES ARE DECIDED FROM TWO BOOLEANS. `exportPdf` wins
 * over `exportExcelXml`, because a PDF is a different document rather than a
 * different encoding of the same rows, and there is no sensible way to honour
 * both.
 *
 * ⛔ AND PDF IS REFUSED HERE IF THE FRAME HAS NO PRINTED LAYOUT, even though the
 * F12 panel does not offer the row on such a screen. Two gates, on purpose: frame
 * config is carried on the frame, and the tempting future change — remembering an
 * operator's export preference across screens — would otherwise silently arm a
 * format that 501s. The panel decides what to SHOW; this decides what HAPPENS.
 */
export function exportTargetFor(snapshot: NavSnapshot): ExportTarget {
  if (snapshot.config?.exportPdf && frameCanExportPdf(snapshot)) return "pdf";
  return exportFormatFor(snapshot);
}

/**
 * Whether PDF is honestly available on this frame.
 *
 * It asks `resolvePrintTarget` — the same pure resolver Ctrl+P uses — so the two
 * keys can never disagree about what this screen's document is. A voucher frame
 * qualifies when its class produces a document; a report frame qualifies when the
 * server has a layout for its slug.
 *
 * ⚠️ A REFUSAL FROM `resolvePrintTarget` IS A "NO" HERE, NOT A MESSAGE. Ctrl+P
 * shows that sentence because the operator asked to print; Alt+E must not offer a
 * format it would then have to explain away.
 */
export function frameCanExportPdf(snapshot: NavSnapshot): boolean {
  const target = resolvePrintTarget(snapshot);
  if (target.kind === "refusal") return false;
  if (target.kind === "voucher") return true;
  return printsALayout(target.slug);
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
  const { company, client } = useCompanyContext();

  useRegisterScreenAction(
    "export",
    useCallback(
      // ⛔ `string | Promise<string>` AND NOT `async`, DELIBERATELY. Only the PDF
      // branch needs a round trip; making the whole handler async would turn every
      // sheet export's message into a promise. `ScreenActionHandler` already
      // admits both and the shell awaits whatever arrives, so the asynchrony is
      // confined to the one branch that has a reason for it — and eighteen
      // existing assertions that read the message synchronously stay meaningful
      // rather than being rewritten to await something that never suspends.
      (snapshot: NavSnapshot): string | Promise<string> => {
        // ⛔ THE PDF BRANCH COMES FIRST AND NEVER TOUCHES `source`. A PDF is the
        // SERVER'S statement, so the screen's rows are not consulted at all —
        // asking `source` here and ignoring its answer would make an empty screen
        // report "nothing to export" for a document that exports perfectly well.
        if (exportTargetFor(snapshot) === "pdf") {
          return exportAsPdf(snapshot, company, client);
        }

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
      [client, company, source],
    ),
  );
}

/**
 * Alt+E as PDF: SAVE the server's document to a file.
 *
 * ⛔ IT SAVES AND DOES NOT OPEN A TAB, which is the one behavioural difference
 * from Ctrl+P and it is deliberate. Ctrl+P means "put this on paper", so it opens
 * the browser's viewer where the print command lives. Alt+E means "give me the
 * file"; opening a tab instead would leave an operator who pressed the export key
 * with nothing saved. Same bytes, same route, different verb — and one shared
 * anchor underneath both.
 *
 * ⛔ AND THE STATUS LINE SAYS WHAT THE FILE CONTAINS. This is the only Alt+E
 * format whose contents differ from the screen: the filter and the F12 settings
 * are not applied to it. Saying so once, here, is what stops an operator who
 * filtered to one party from sending a customer the whole ledger.
 */
async function exportAsPdf(
  snapshot: NavSnapshot,
  company: { id: string } | null,
  client: CoredClient,
): Promise<string> {
  const target = resolvePrintTarget(snapshot);
  // Unreachable while `exportTargetFor` gates on the same resolver — kept because
  // the two could drift, and a thrown TypeError on a key press is worse than a
  // sentence. See frameCanExportPdf.
  if (target.kind === "refusal") return target.reason;
  if (!company) return "Select a company first.";

  try {
    const { blob, filename } =
      target.kind === "voucher"
        ? await client.printVoucher(company.id, target.voucherClass, target.voucherID)
        : await client.printReport(company.id, target.slug, target.query);

    // The SERVER names the file (Content-Disposition); the fallback only covers a
    // server that sent no header. `target.slug` exists on a report target only.
    const name =
      filename ?? `${target.kind === "report" ? target.slug : target.voucherClass}.pdf`;
    const outcome = downloadBlob(blob, name);
    if (!outcome.ok) return `Export failed. ${outcome.reason ?? ""}`.trim();

    return (
      `Exported ${target.label} to ${outcome.filename}. ` +
      `⚠️ A PDF is the SERVER'S whole statement, not the rows on screen: ` +
      `${describeUnappliedView(snapshot)}`
    );
  } catch (err) {
    // The server's own sentence, unaltered — the same rule usePrint follows. A
    // 501 here would mean the layout gate has drifted from the golden, and that
    // is worth reading verbatim rather than smoothing over.
    if (err instanceof ApiError) {
      return `Could not export ${target.label} as PDF: ${err.message}`;
    }
    return `Could not export ${target.label} as PDF: ${
      err instanceof Error ? err.message : String(err)
    }`;
  }
}

/**
 * Name the on-screen narrowing the PDF did NOT honour.
 *
 * ⛔ IT NAMES THE ACTIVE ONES RATHER THAN RECITING THE LIST. "a filter may not be
 * applied" is noise an operator learns to skip; "your Ctrl+F filter \"ACME\" was
 * NOT applied" is a fact they will act on. With nothing narrowed there is nothing
 * to warn about, and the sentence says so plainly instead of hedging.
 */
function describeUnappliedView(snapshot: NavSnapshot): string {
  const cfg = snapshot.config ?? {};
  const active: string[] = [];
  const filter = (cfg.filterText ?? "").trim();
  if (filter !== "") active.push(`your Ctrl+F filter "${filter}"`);
  if (cfg.hideZeroBalances) active.push("the F12 exclude-zero-balances setting");
  if (cfg.detailed) active.push("the F12 Detailed setting");
  if (active.length === 0) {
    return "nothing is narrowed on screen, so the two agree here.";
  }
  const list =
    active.length === 1
      ? active[0]
      : `${active.slice(0, -1).join(", ")} and ${active[active.length - 1]}`;
  return `${list} ${active.length === 1 ? "was" : "were"} NOT applied to it.`;
}

