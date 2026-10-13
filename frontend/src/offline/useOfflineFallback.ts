/**
 * useOfflineFallback.ts — the accept path's ONE decision point, shared by both
 * screens that post a voucher.
 *
 * ⛔⛔ IT IS ONE HOOK AND NOT TWO COPIES ON PURPOSE. `VoucherScreen` and
 * `StockVoucherScreen` are the whole funnel (`voucherConfig.ts`'s per-class
 * `post`, reached from `editActions.ts` and `StockVoucherScreen`), and the
 * judgement — queue a dropped connection, NEVER queue a refusal — is the kind
 * that drifts if it is written twice. A copy that queued a 422 on the stock
 * screen only would be invisible to every test the other screen has.
 *
 * ⛔ IT ALWAYS THROWS, which is what keeps it out of the success path. Either it
 * rethrows the ORIGINAL error (so the screen's existing, hard-won messages — the
 * back-dated-cutoff sentence, the orphaned-master disclosure — run exactly as
 * before), or it queues the voucher and throws {@link QueuedOfflineSignal}, which
 * the screen renders as "queued, NOT posted". ⛔ IT NEVER RETURNS A FAKE
 * `VoucherResponse`: setting `savedNo`/`savedVoucherID` from a voucher that was
 * only queued is precisely the conflation this whole track exists to prevent.
 */

import { useCallback } from "react";

import type { VoucherRequest } from "../api/types";

import { useOfflineOutbox } from "./OfflineOutbox";
import { shouldQueueOffline } from "./queueDecision";

/**
 * Not an error condition: the voucher IS safely queued. Thrown so the accept
 * chain's existing `.catch` is the one place that decides what the screen says.
 */
export class QueuedOfflineSignal extends Error {
  readonly provisionalNo: string;
  constructor(provisionalNo: string, why: string) {
    super(why);
    this.name = "QueuedOfflineSignal";
    this.provisionalNo = provisionalNo;
  }
}

/** What is being queued, if it is queued at all. */
export interface OfflineAttempt {
  /**
   * The WIRE class label — "Sales", "Credit Note" — from
   * `VOUCHER_CLASS_LABELS`, never the slug. The server does
   * `ledger.Class(ir.Class)` with no translation (`server.go:3886`), so a slug
   * here decodes to a class the ledger does not know.
   */
  readonly cls: string;
  readonly req: VoucherRequest;
  /** Present only when this was an ALTER of a loaded voucher. */
  readonly alter?: { readonly targetID: string; readonly baseVersion: number };
}

/**
 * ⛔ CALL THIS FROM THE `catch` THAT WRAPS THE POST ITSELF, NOT FROM AN OUTER
 * ONE. The outer catch on `VoucherScreen` also sees failures from BUILDING the
 * request — including the on-demand ledger creation that runs before the post.
 * A transport failure there means the ledger ids in this payload do NOT exist on
 * the server, so queueing it would store an intent that can only ever be refused
 * with "no such ledger". The narrow catch is what keeps the queue's contents
 * committable.
 */
export function useOfflineFallback(): (err: unknown, attempt: OfflineAttempt) => Promise<never> {
  const outbox = useOfflineOutbox();

  return useCallback(
    async (err: unknown, attempt: OfflineAttempt): Promise<never> => {
      const verdict = shouldQueueOffline(err);
      if (!verdict.queue) throw err;

      // ⛔ A QUEUE THAT IS NOT THERE MUST SAY SO. Falling back to the plain
      // network error would leave the operator reading "could not reach the
      // server" with no statement about whether their work was kept — and this
      // path is reached with no provider at all in standalone screen tests, so
      // it is not hypothetical.
      if (!outbox || !outbox.ready) {
        throw new Error(
          "The server could not be reached AND this voucher could not be queued on this machine, so nothing has been kept. Re-key it when the connection is back.",
        );
      }

      const provisional = attempt.alter
        ? await outbox.queueAlter(
            attempt.alter.targetID,
            attempt.alter.baseVersion,
            attempt.cls,
            attempt.req,
          )
        : await outbox.queueCreate(attempt.cls, attempt.req);

      throw new QueuedOfflineSignal(provisional, verdict.why);
    },
    [outbox],
  );
}

/**
 * The sentence the screen shows for a queued voucher.
 *
 * ⛔ THE PROVISIONAL NUMBER IS LABELLED AS PROVISIONAL EVERY TIME IT IS SHOWN.
 * `INV/2025-26/PROV-<node>-3` contains the literal `PROV` and the node id so that
 * a number read down a phone line is self-evidently not a document number — and
 * the sentence still names the screen that will commit it, because a queue the
 * operator cannot find is a queue they cannot trust.
 */
export function queuedStatus(signal: QueuedOfflineSignal): string {
  return `QUEUED, NOT POSTED — ${signal.message} Provisional number ${signal.provisionalNo} (provisional: the real voucher number is allocated when this commits). Alt+Q opens the Offline Outbox to commit it.`;
}
