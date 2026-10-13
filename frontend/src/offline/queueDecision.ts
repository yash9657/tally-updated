/**
 * queueDecision.ts — ⛔⛔ THE ONE JUDGEMENT ON THE PRODUCT'S MAIN WRITE PATH.
 *
 * When a voucher fails to post, was it the NETWORK — in which case the work is
 * the operator's and belongs in the outbox — or did the BOOKS REFUSE IT, in
 * which case queueing it is the worst thing this application can do?
 *
 * ⛔ THE TWO ERRORS ARE NOT SYMMETRIC, AND THAT ASYMMETRY IS THE WHOLE DESIGN.
 *
 *   REFUSED, RENDERED AS QUEUED  — the operator is told their invoice is safe.
 *       They close the laptop and walk away. The invoice does not exist, nobody
 *       is billed, and the queue they trusted will re-offer a voucher the books
 *       have already rejected once. This direction LOSES MONEY SILENTLY.
 *
 *   QUEUED, RENDERED AS REFUSED  — the operator sees an error and re-keys.
 *       Annoying. Nothing is lost, and they know the state they are in.
 *
 * So the rule is NOT "queue unless it looks like a refusal". It is: ⭐ **queue
 * only when the client can PROVE the books never saw the write**, and treat
 * every other failure — including every failure whose meaning is unclear — as a
 * refusal the operator must read.
 *
 * ⛔ WHICH IS WHY THIS ASKS FOR `NetworkError` AND NOT `!(err instanceof
 * ApiError)`. A decision by exclusion also catches every bug in the client: a
 * TypeError from a null deref inside the accept chain, a builder that throws, a
 * `.then` that blows up while reading the response. Each of those would have
 * been filed as "the network was down" and the operator told their voucher was
 * queued because THIS APPLICATION crashed. `http.ts` raises `NetworkError` from
 * the `fetch` call and from nowhere else, so the test here is positive: the
 * transport itself says no answer was produced.
 *
 * ## The statuses, and why 5xx is on the REFUSED side
 *
 * | What arrived | Verdict | Why |
 * |---|---|---|
 * | `NetworkError` | QUEUE | no answer was produced; the books cannot have applied it |
 * | `422` | refused | the books judged the body and said no. This is the case that must never queue |
 * | `403` | refused | the PDP, or a closed period. Both are decisions, not outages. (A `403 STEPUP_REQUIRED` never reaches here at all — `bearerFetch` runs the ceremony and replays the request) |
 * | `409` | refused | a duplicate voucher number is a decision about the books' contents |
 * | `500` | refused | ⭐ THE INTERESTING ONE. The request REACHED the books. It may have applied and then failed while answering. A queued re-commit mints a NEW client voucher id and a NEW idempotency key, so the server cannot dedupe it against the first attempt — queueing a 500 is how ONE invoice becomes TWO. The operator seeing an error is strictly better |
 * | `502` / `503` / `504` | refused | a gateway that answered is a server that was reached by SOMETHING. The client cannot tell an app-level 503 (maintenance, rate limit) from a proxy that never got through, and the ambiguous direction is the losing one |
 * | anything else | refused | a client bug, and it must look like one |
 *
 * ⚠️ THE RESIDUAL AMBIGUITY, WRITTEN DOWN RATHER THAN PAPERED OVER. A connection
 * reset AFTER the server committed but BEFORE the response could be read raises
 * `NetworkError` too, so this rule can queue an intent that already landed. That
 * is inherent — nothing observable on the wire distinguishes it — and it is the
 * price of offline entry at all. What is NOT acceptable is hiding it: the outbox
 * screen says a queued voucher may already have reached the books if the
 * connection dropped mid-send, and names the Day Book as the place to check.
 *
 * ⚠️ AND WHAT IS DELIBERATELY NOT SOLVED HERE: this app sets no request timeout
 * anywhere (`/usr/bin/grep -rn "AbortController\|AbortSignal" src/` -> EMPTY), so
 * a STALLED connection hangs rather than failing, and nothing is queued because
 * nothing ever rejects. Adding a timeout would trade that stall for exactly the
 * committed-then-abandoned ambiguity above, on a deadline of our choosing — so it
 * is a separate decision, not a detail of this one.
 */

import { ApiError, NetworkError } from "../api/http";

/**
 * The verdict. `why` is written for the OPERATOR and is rendered verbatim on the
 * accept path — it is not a log line.
 */
export interface QueueVerdict {
  /** Append this voucher to the offline outbox? */
  readonly queue: boolean;
  /** What to tell the operator. Complete sentences; shown on the screen. */
  readonly why: string;
}

/**
 * ⛔ The decision. See the file header for why the test is `NetworkError` and
 * not `!ApiError`, and why a 500 is on the refused side.
 */
export function shouldQueueOffline(err: unknown): QueueVerdict {
  if (err instanceof NetworkError) {
    return {
      queue: true,
      why: "The server could not be reached, so nothing was posted. This voucher is QUEUED on this machine and is NOT in the books yet.",
    };
  }

  if (err instanceof ApiError) {
    // ⛔ EVERY status the server answered with, INCLUDING 5xx. The books were
    // reached; only they know what they did with the body. Nothing is queued and
    // the caller shows the server's own sentence — see the header's table.
    return {
      queue: false,
      why: `The server answered ${err.status} and REFUSED this voucher. It has not been queued — there is nothing to retry until the reason is fixed.`,
    };
  }

  // Not the transport and not the server: this is a fault in the client. It must
  // not be dressed up as an outage, because the voucher is not queued and the
  // operator would otherwise believe it was.
  return {
    queue: false,
    why: "This voucher was not posted and was NOT queued: the failure came from the application itself, not from the network. Nothing is safe to walk away from — report this.",
  };
}
