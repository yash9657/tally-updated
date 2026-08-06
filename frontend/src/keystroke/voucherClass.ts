import { isVoucherType, type VoucherType } from "./types";

/**
 * THE VOUCHER ROW KEY — how a voucher-bearing report tells the navigation
 * machine WHICH voucher a row is *and what class it is*.
 *
 * ## Why this exists
 *
 * `navMachine` is pure: the only thing it knows about a highlighted report row
 * is the row key the screen published through `onSelect`. Until this module, a
 * Day Book row published its bare `voucher_id`, so the Enter drill pushed
 * `newVoucherFrame(DEFAULT_LAST_VOUCHER, "alter")` — and `DEFAULT_LAST_VOUCHER`
 * is `"sales"` — for EVERY voucher, while `acceptVoucher` routes the PUT by the
 * SCREEN's type rather than by the loaded voucher's class. Drilling a
 * Receipt from the Day Book and pressing Ctrl+A therefore called
 * `alterSalesVoucher`: **a corrupted document, silently re-classed**, over a
 * frame that titled itself "Sales Voucher (Alteration)" above `RCPT/1`.
 *
 * ## Why the class travels in the ROW KEY and not somewhere nicer
 *
 * Three fixes were possible and only one of them is whole:
 *
 *  1. Route `acceptVoucher` by the LOADED voucher's class. Smallest, and it does
 *     stop the re-classing — but the frame still renders the Sales GST form over
 *     a single-entry voucher (so the particulars are not editable), and
 *     `AppShell.screenTitle` reads `snap.voucherType` off the FRAME, so the
 *     title bar keeps lying. Nothing short of the frame carrying the class fixes
 *     the title.
 *  2. Let the screen correct the frame after `getVoucher` resolves. That is one
 *     render of the wrong form, a wasted masters/voucher fetch, an unmount and
 *     remount for the stock classes — and a SECOND source of truth for the class.
 *  3. Put the class in the row key, which is the machine's only channel into the
 *     drill. The frame is right on its FIRST render, the machine stays pure, and
 *     the encoding is the idiom this codebase already uses for exactly this
 *     purpose: `GROUP_ROW_PREFIX` / `LEDGER_ROW_PREFIX` publish a statement row's
 *     KIND so Enter knows how deep to drill, and `COST_CATEGORY_ROW_PREFIX`
 *     publishes an axis.
 *
 * ## The class is a property of the LOADED VOUCHER, not of the drill
 *
 * ⛔ This is the part that is easy to get wrong, and the reason the encoding is
 * exported rather than inlined into the report screens. `VoucherScreen`'s Page Up
 * / Page Down (`stepVoucher`) swaps the loaded voucher UNDERNEATH a standing
 * frame, so paging from a Sales alter onto a Receipt re-classed it in exactly the
 * same way with no drill involved at all. It publishes the SAME row key through
 * the same `onSelect`, and `reduceSelect` re-classes the standing frame from it.
 * One encoding, one parser, both paths.
 */

/**
 * Prefix marking a row key as "a voucher of a known class". Rows whose class has
 * no entry screen in this build (Sales Order / Purchase Order / Payroll) publish
 * their bare id instead, which parses to `undefined` and makes the drill a no-op
 * — see {@link parseVoucherRowKey}.
 */
export const VOUCHER_ROW_PREFIX = "voucher:";

/** Encode a drillable voucher row as `voucher:<type>:<id>`. */
export function voucherRowKey(voucherID: string, type: VoucherType): string {
  return `${VOUCHER_ROW_PREFIX}${type}:${voucherID}`;
}

/**
 * Decode a voucher row key, or `undefined` when the key is not one.
 *
 * ⛔ RETURNING `undefined` RATHER THAN DEFAULTING IS THE WHOLE POINT, and the
 * callers must not paper over it. Three kinds of key arrive here and each has a
 * different right answer:
 *
 *  - `voucher:receipt:<id>` — a drillable voucher of a class this build renders.
 *  - a bare id, or nothing at all — a Day Book row whose class has NO entry
 *    screen, or an EMPTY report (`DrillRows` publishes no key when there is not
 *    one drillable row). Both must be a no-op. The second used to push an
 *    alteration frame with NO loaded voucher, so Ctrl+A **POSTED a brand-new
 *    Sales voucher** from a frame titled "(Alteration)".
 *  - `voucher:<something-else>:<id>` — impossible from this build, and refused
 *    rather than trusted: `voucherType` is used as a bare index into
 *    `VOUCHER_CONFIGS`, so an unvalidated slug crashes the chrome.
 */
export function parseVoucherRowKey(
  rowKey: string | undefined,
): { voucherType: VoucherType; voucherID: string } | undefined {
  if (rowKey === undefined || !rowKey.startsWith(VOUCHER_ROW_PREFIX)) {
    return undefined;
  }
  const rest = rowKey.slice(VOUCHER_ROW_PREFIX.length);
  const sep = rest.indexOf(":");
  if (sep <= 0) return undefined;
  const type = rest.slice(0, sep);
  const voucherID = rest.slice(sep + 1);
  // A uuid contains no colon, so an id is never split. An EMPTY id is refused:
  // "voucher:receipt:" would otherwise open an alteration frame for no voucher,
  // which is the create-in-disguise hazard described above.
  if (voucherID === "" || !isVoucherType(type)) return undefined;
  return { voucherType: type, voucherID };
}

/**
 * The voucher id a row key names, whichever form it takes. Report screens hold
 * their entries by `voucher_id` and must be able to resolve the row they just
 * published back to its entry — including the unprefixed rows above.
 */
export function voucherIDFromRowKey(rowKey: string): string {
  return parseVoucherRowKey(rowKey)?.voucherID ?? rowKey;
}
