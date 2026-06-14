/**
 * Voucher edit-loop side effects, keyed off the navigation snapshot's mode.
 *
 * The navMachine stays pure (it only moves the screen stack); the actual
 * backend calls live here, in the screen layer, so they are independently
 * unit-testable against the in-memory mock client. The AppShell/voucher screen
 * invokes {@link acceptVoucher} on Ctrl+A and {@link deleteLoadedVoucher} on
 * Alt+D, passing the loaded voucher id (when the frame is an alteration of an
 * existing voucher).
 *
 * Fidelity rules encoded here:
 *  - ALTER preserves identity: when mode === "alter" and a loaded voucher id is
 *    present, we call the per-class ALTER method (PUT carries {voucherID} and
 *    never mints a new id). Otherwise we POST a fresh voucher.
 *  - ALTER preserves numbering: the caller is responsible for pre-filling the
 *    request with the loaded voucher_no/voucher_date so they are RESENT
 *    unchanged. This module forwards the body verbatim — it never regenerates a
 *    number — so as long as the loaded body is passed in, numbering is stable.
 *  - ALTER is a FULL restatement: the request must carry the complete set of
 *    postings/bill-allocations/GST (this module does not patch a delta).
 *  - DELETE is class-agnostic: the tombstone needs only the logical voucher id.
 */

import type {
  CoredClient,
  DeleteVoucherResponse,
  VoucherRequest,
  VoucherResponse,
} from "../../api";
import type { VoucherMode, VoucherType } from "../../keystroke/types";
import { configFor } from "./voucherConfig";

/** The state the screen layer holds for a voucher frame's edit context. */
export interface VoucherEditContext {
  /** The accounting class fixed by the active voucher frame. */
  readonly type: VoucherType;
  /** "create" for a fresh voucher, "alter" when editing a loaded one. */
  readonly mode: VoucherMode;
  /**
   * The loaded logical voucher id, present only in alteration mode. Required to
   * route an alter/delete to the existing voucher; absent => treat as create.
   */
  readonly voucherID?: string;
}

/**
 * Ctrl+A Accept. In create mode (or alter mode with no loaded id) this POSTs a
 * new voucher; in alter mode with a loaded id it PUTs a full restatement under
 * the same logical voucher id. The request body is forwarded verbatim — the
 * caller must have pre-filled the loaded voucher_no/voucher_date on alter so
 * they are resent unchanged (numbering-preservation rule).
 */
export function acceptVoucher(
  client: CoredClient,
  companyID: string,
  ctx: VoucherEditContext,
  req: VoucherRequest,
): Promise<VoucherResponse> {
  const cfg = configFor(ctx.type);
  if (ctx.mode === "alter" && ctx.voucherID) {
    return cfg.alter(client, companyID, ctx.voucherID, req);
  }
  return cfg.post(client, companyID, req);
}

/**
 * Alt+D Delete. Class-agnostic — appends an OpDelete tombstone that removes the
 * voucher's accounting effect and default report/day-book visibility while the
 * version stays in the audit log. Throws if there is no loaded voucher id
 * (nothing persisted to delete, e.g. a create-mode frame).
 */
export function deleteLoadedVoucher(
  client: CoredClient,
  companyID: string,
  ctx: VoucherEditContext,
  createdBy?: string,
): Promise<DeleteVoucherResponse> {
  if (!ctx.voucherID) {
    return Promise.reject(
      new Error("cannot delete: no loaded voucher in this frame"),
    );
  }
  return client.deleteVoucher(companyID, ctx.voucherID, createdBy);
}
