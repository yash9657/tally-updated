/**
 * commitRefusal.ts — ⛔ WHEN A DRAIN FAILS AS A WHOLE REQUEST, WHAT DID THAT MEAN?
 *
 * `SyncCommitResponse` carries one outcome per intent, and those four statuses
 * (COMMITTED / CONFLICT / FROZEN / DUPLICATE) are per-intent verdicts the screen
 * renders in a status column. ⛔⛔ THE FAILURES CLASSIFIED HERE ARE NOT THAT.
 * They return NO outcomes at all: the sequencer never judged the intents one by
 * one, so **every intent is still queued and still valid**.
 *
 * ⛔ RENDERING ONE OF THESE IN THE STATUS COLUMN IS THE DEFECT THIS MODULE
 * EXISTS TO PREVENT. "These 12 vouchers failed" is wrong in the direction that
 * costs work: the operator re-keys twelve invoices that are sitting intact in the
 * outbox, and then commits both copies. The refusal is a property of the REQUEST
 * and must be rendered as a banner over the whole queue, never per row.
 *
 * The three, and why they are told apart:
 *
 *   `cutoff`   ⭐ ONE pre-cutoff intent refuses the LOT — "a batch is only as
 *              permitted as its most-refused member" (cutoff round 2). The
 *              operator's move is to find and DROP or re-date that one row, so the
 *              banner must carry the server's own sentence, which names the date.
 *   `stepup`   the level requires WebAuthn and the ceremony did not complete.
 *              `bearerFetch` normally runs it and replays invisibly, so reaching
 *              here means it was cancelled or failed. The move is to try again.
 *   `offline`  there was no answer at all. The move is to wait for a network.
 *   `other`    anything else the server said, shown verbatim — including the
 *              mock's honest 501 ("the mock has no cloud sequencer").
 *
 * ⛔ IT IS DISTINGUISHED ON THE SERVER'S OWN SENTENCE, NOT ON A CODE WE INVENT —
 * the same rule `StockVoucherScreen` follows for its 403. Every cutoff refusal in
 * `internal/authz` contains "back-dated cutoff" and no ACL denial does.
 */

import { ApiError, NetworkError } from "../api/http";
import type { SyncBatchRefusal } from "../api/types";

/** Is this 403 the PDP demanding a passkey rather than denying the act? */
function isStepUp(err: ApiError): boolean {
  const body = err.body;
  if (body && typeof body === "object" && "code" in (body as Record<string, unknown>)) {
    if ((body as Record<string, unknown>).code === "STEPUP_REQUIRED") return true;
  }
  return /step-?up/i.test(err.message);
}

/**
 * Classify a whole-batch failure. ⛔ EVERY RETURN VALUE MEANS "THE OUTBOX IS
 * INTACT" — there is no kind here that authorises calling `OfflineNode.settle`.
 */
export function classifyBatchRefusal(err: unknown): SyncBatchRefusal {
  if (err instanceof NetworkError) {
    return {
      kind: "offline",
      status: 0,
      message:
        "The server could not be reached, so nothing was sent. Every voucher below is still queued exactly as you keyed it.",
    };
  }

  if (err instanceof ApiError) {
    // ⛔ CUTOFF IS TESTED BEFORE STEP-UP AND BOTH BEFORE THE STATUS. Both arrive
    // as 403 and only the sentence tells them apart.
    if (/back-dated cutoff/i.test(err.message)) {
      return { kind: "cutoff", status: err.status, message: err.message };
    }
    if (err.status === 403 && isStepUp(err)) {
      return { kind: "stepup", status: err.status, message: err.message };
    }
    return { kind: "other", status: err.status, message: err.message };
  }

  return {
    kind: "other",
    status: 0,
    message: err instanceof Error ? err.message : String(err),
  };
}

/**
 * What the operator should DO about it. Kept beside the classifier so a new kind
 * cannot be added without answering the question — the switch is exhaustive.
 */
export function refusalRemedy(r: SyncBatchRefusal): string {
  switch (r.kind) {
    case "cutoff":
      return "This is a CLOSED PERIOD refusing the whole batch, not a rejection of any one voucher: the date named above falls inside a period your level may no longer write to, and one such voucher refuses the lot. Find that row below and either change its date or discard it, then commit again.";
    case "stepup":
      return "This write needs your passkey and the ceremony did not complete. Commit again and answer the prompt — the whole queue costs exactly one step-up, because it is sent as a single write.";
    case "offline":
      return "Nothing was sent. Commit again when you have a connection; the queue survives a reload on this machine.";
    case "other":
      return "Nothing in the queue was judged, so nothing has changed. The server's own message is above.";
  }
}
