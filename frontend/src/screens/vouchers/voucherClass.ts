import {
  VOUCHER_CLASS_LABELS,
  type DayBookEntry,
  type VoucherClass,
} from "../../api";
import { voucherRowKey } from "../../keystroke/voucherClass";
import type { VoucherType } from "../../keystroke/types";

/**
 * Map the server's voucher `class` LABEL onto the voucher frame that can render
 * it — the one thing that lets a Day Book row be drilled into the RIGHT form.
 *
 * This lives in `screens/` because it is the only layer allowed to know both
 * halves: `VOUCHER_CLASS_LABELS` is a wire fact from `api/`, and `VoucherType` is
 * a frame fact from `keystroke/`. Those two directories do not import each other
 * (deliberately — see the note in navMachine's Change Mode branch about not
 * inverting the layering), so the join belongs here, beside the voucher configs
 * it feeds.
 */

/**
 * `VoucherClass` (the api slug) and `VoucherType` (the frame's class) are the
 * SAME twelve slugs declared in two layers, and this Record is what makes that a
 * COMPILE-TIME fact rather than a coincidence: tsc requires every `VoucherClass`
 * key, and requires each value to be a real `VoucherType`. Add a class on one
 * side only and this stops compiling.
 *
 * ⚠️ It is not `identity` written as a loop for a reason: a loop would happily
 * cast an api slug that no longer exists as a frame type, which is the drift the
 * table exists to catch.
 */
const AS_VOUCHER_TYPE: Readonly<Record<VoucherClass, VoucherType>> = {
  sales: "sales",
  purchase: "purchase",
  receipt: "receipt",
  payment: "payment",
  contra: "contra",
  journal: "journal",
  "credit-note": "credit-note",
  "debit-note": "debit-note",
  "delivery-note": "delivery-note",
  "receipt-note": "receipt-note",
  "stock-journal": "stock-journal",
  "physical-stock": "physical-stock",
};

/**
 * The REVERSE join: a frame's `VoucherType` back to the api `VoucherClass`.
 *
 * ⛔ IT IS A SECOND EXPLICIT TABLE, NOT A CAST AND NOT AN INVERSION LOOP, for the
 * same reason `AS_VOUCHER_TYPE` above is not `identity` written as a loop: tsc
 * requires every `VoucherType` key and requires each value to be a real
 * `VoucherClass`, so a slug that exists on one side only stops compiling. An
 * inverted loop, or a `type as VoucherClass`, would happily hand a frame slug
 * that no api class matches straight into a URL path.
 *
 * ⚠️ IT IS LOAD-BEARING FOR AN ACL DECISION (D97), which is why it is worth a
 * table. The class goes into the PATH of `/vouchers/{class}/open-bills/{ledgerID}`
 * and cored's PDP decides the bill-wise picker's read on it; a slug the server
 * does not recognise as a voucher class is default-DENIED by routeAction, so a
 * silent mis-cast here would take the picker away from a clerk who is entitled to
 * it and land their settlement ON ACCOUNT.
 */
const AS_VOUCHER_CLASS: Readonly<Record<VoucherType, VoucherClass>> = {
  sales: "sales",
  purchase: "purchase",
  receipt: "receipt",
  payment: "payment",
  contra: "contra",
  journal: "journal",
  "credit-note": "credit-note",
  "debit-note": "debit-note",
  "delivery-note": "delivery-note",
  "receipt-note": "receipt-note",
  "stock-journal": "stock-journal",
  "physical-stock": "physical-stock",
};

/** The api `VoucherClass` a voucher frame keys. Total: every frame has one. */
export function voucherClassForType(type: VoucherType): VoucherClass {
  return AS_VOUCHER_CLASS[type];
}

/** label -> frame type, built once from the two tables above. */
const BY_LABEL: ReadonlyMap<string, VoucherType> = new Map(
  (Object.keys(AS_VOUCHER_TYPE) as VoucherClass[]).map((cls) => [
    VOUCHER_CLASS_LABELS[cls].toLowerCase(),
    AS_VOUCHER_TYPE[cls],
  ]),
);

/**
 * The voucher frame that renders a voucher of this server `class`, or `undefined`
 * when this build has no entry screen for it.
 *
 * ⛔ `undefined` IS A REAL ANSWER AND MUST NOT BE DEFAULTED. Three server classes
 * reach no voucher screen: **Payroll** (postable, but a pay run has no entry form
 * here — `internal/ledger/payroll_class.go`), and **Sales Order** / **Purchase
 * Order** (named but not postable, so they never appear in the Day Book; the
 * registers are Ctrl+F8 / Ctrl+F9, which are not voucher frames). Substituting
 * "sales" for any of them is precisely the defect this function exists to end:
 * a pay run opened in a Sales form and saved becomes a sales invoice.
 *
 * ⚠️ Matched case-INSENSITIVELY, and that is a fidelity concession rather than
 * politeness. Hand-built `DayBookEntry` fixtures in this repo carried
 * `class: "SALES"` because the in-memory mock used to write `cls.toUpperCase()`
 * (see mock.ts's classLabel). The mock is fixed and the fixtures are corrected,
 * but a case-sensitive match would make any surviving one open NOTHING, with no
 * error — the silent-no-op failure mode. The CASE is not load-bearing anywhere;
 * the words and the spaces are.
 */
export function voucherTypeForClass(
  classLabel: string | undefined,
): VoucherType | undefined {
  if (!classLabel) return undefined;
  return BY_LABEL.get(classLabel.trim().toLowerCase());
}

/**
 * The drill row key for a Day Book entry: `voucher:<type>:<id>` when this build
 * can render the class, and the BARE id when it cannot.
 *
 * ⛔ THE BARE-ID FALLBACK IS NOT A CONVENIENCE — it is what makes an
 * un-renderable voucher a NO-OP instead of a corruption. `parseVoucherRowKey`
 * refuses it, so Enter does nothing and the shell says the key does not apply,
 * where the previous behaviour opened a Sales form over the document and
 * re-classed it on save. The row stays visible, selectable and printable; only
 * the drill declines.
 *
 * Used by BOTH voucher-bearing reports (the Day Book and a statement's ledger
 * drill) and by Page Up / Page Down, so all three agree on one encoding.
 */
export function dayBookRowKey(entry: DayBookEntry): string {
  const type = voucherTypeForClass(entry.class);
  return type ? voucherRowKey(entry.voucher_id, type) : entry.voucher_id;
}
