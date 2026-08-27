import { useCallback, useState } from "react";

import type { SyncBatchRefusal, SyncIntent, SyncOutcome } from "../api";
import { refusalRemedy } from "../offline/commitRefusal";
import { useOfflineOutbox } from "../offline/OfflineOutbox";

/** One committed intent: the server's verdict, beside the label it was queued under. */
interface CommittedRow {
  readonly outcome: SyncOutcome;
  /** The provisional number the operator SAW, captured before the commit. */
  readonly provisional: string;
}

/**
 * OFFLINE OUTBOX — the vouchers this machine is holding because the server could
 * not be reached, and the one control that sends them (track D).
 *
 * Reached from `Gateway of Tally > Offline Outbox` and from Alt+Q.
 *
 * ⛔⛔ NOTHING ON THIS SCREEN IS IN THE BOOKS. That is the single fact it exists
 * to communicate, and every column, caption and sentence here is chosen to keep
 * it impossible to misread:
 *
 *  - the number column is headed **Provisional**, never "No.". The value itself
 *    carries the literal `PROV` and the node id (`INV/2025-26/PROV-<node>-3`), so
 *    a number read down a phone line to a customer is self-evidently not a
 *    document number. ⛔ A PROVISIONAL MUST NEVER APPEAR IN A FIELD A FINAL
 *    NUMBER ALSO APPEARS IN — which is why this is a screen of its own and why a
 *    queued intent is in no register, no report and not in the Day Book. It
 *    cannot be: it is not on the server.
 *  - on a COMMITTED outcome the mapping `PROV-… → INV/2026-27/42` is kept on
 *    screen for the rest of the session even though the row has left the queue.
 *    The operator may already have quoted the provisional; the mapping is the
 *    only thing that lets them correct it.
 *
 * ⛔ A WHOLE-BATCH REFUSAL IS A BANNER, NEVER A STATUS COLUMN. `SyncBatchRefusal`
 * spells out why: a cutoff or a step-up returns NO per-intent outcomes, every
 * voucher is still queued and still valid, and rendering "these 12 failed" invites
 * an operator to re-key twelve invoices that are sitting intact below — and then
 * commit both copies. See `classifyBatchRefusal` and `refusalRemedy`.
 *
 * ⛔ THE TWO DISCLOSURES ARE PART OF THE FEATURE, NOT SMALL PRINT.
 *   1. The queue is UNENCRYPTED. Encrypting client-side with a key the client
 *      also holds is theatre, so the honest thing is to store it in the clear and
 *      SAY SO — somebody keying invoices on a shared back-office PC is entitled
 *      to know that the books sit in this browser's storage until they commit.
 *   2. On a browser with no IndexedDB (private mode, a locked-down profile) the
 *      queue is memory-only and will NOT survive a reload. A queue that quietly
 *      forgets is worse than no queue at all, because the operator has already
 *      trusted it with the work — so `durable === false` is stated in the loudest
 *      terms this screen has.
 *
 * ⚠️ AND THE AMBIGUITY THIS SCREEN OWNS. A connection that dropped AFTER the
 * server committed but BEFORE the response arrived is indistinguishable from one
 * that never arrived — nothing on the wire separates them — so a queued voucher
 * MAY already be in the books. That is disclosed here with the place to check,
 * because the alternative is an operator committing a duplicate invoice believing
 * the queue could not have been wrong.
 */
export function OfflineOutboxScreen(): JSX.Element {
  const outbox = useOfflineOutbox();

  // The commit's answer, kept for the session. COMMITTED rows have left the
  // queue by the time this renders, so it is the only place their final numbers
  // are visible.
  //
  // ⛔ THE PROVISIONAL IS PAIRED IN HERE AND NOT LOOKED UP LATER. `SyncOutcome`
  // carries the FINAL number and the client voucher id; the provisional travels
  // on the INTENT, which has left the queue by then. Capturing the pair at commit
  // time is the only way `PROV-… → INV/2026-27/42` can be shown at all — and that
  // mapping is what lets an operator correct a number they already quoted.
  const [results, setResults] = useState<CommittedRow[]>([]);
  const [refusal, setRefusal] = useState<SyncBatchRefusal | null>(null);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<string | null>(null);

  const onCommit = useCallback(async () => {
    if (!outbox || busy) return;
    setBusy(true);
    setStatus(null);
    // Snapshot BEFORE the commit: settled intents are gone from `pending` the
    // moment it returns.
    const sent = new Map(outbox.pending.map((i) => [i.idempotency_key, i.provisional_no]));
    try {
      const res = await outbox.commit();
      if (res.kind === "empty") {
        setStatus("Nothing to commit.");
        return;
      }
      if (res.kind === "refusal") {
        // ⛔ THE QUEUE IS UNTOUCHED AND THE PREVIOUS OUTCOMES ARE NOT. Clearing
        // the outcome list here would erase the PROV -> final mapping from an
        // EARLIER successful commit, which is the one record of numbers the
        // operator may have quoted.
        setRefusal(res.refusal);
        return;
      }
      setRefusal(null);
      setResults((prev) => [
        ...prev,
        ...res.outcomes.map((o) => ({
          outcome: o,
          // "—" only when the pairing genuinely is not available. ⛔ A WRONG
          // provisional here is worse than a missing one: it is the number the
          // operator will read out to correct what they quoted.
          provisional: sent.get(o.idempotency_key) ?? "—",
        })),
      ]);
      const committed = res.outcomes.filter((o) => o.status === "COMMITTED").length;
      const parked = res.outcomes.filter(
        (o) => o.status === "CONFLICT" || o.status === "FROZEN",
      ).length;
      setStatus(
        `${committed} of ${res.outcomes.length} committed to the books.` +
          (parked > 0
            ? ` ${parked} was refused and parked — Alt+R (Reconciliation Queue) is where those are handled; nothing about them is decided here.`
            : ""),
      );
    } finally {
      setBusy(false);
    }
  }, [busy, outbox]);

  const onDiscard = useCallback(
    async (intent: SyncIntent) => {
      if (!outbox) return;
      await outbox.discard(intent.idempotency_key);
      setStatus(
        `Discarded ${intent.provisional_no}. It was never posted, so nothing in the books changed — and it is gone from this machine.`,
      );
    },
    [outbox],
  );

  // ⛔ NO PROVIDER MEANS NO QUEUE, AND IT MUST SAY THAT RATHER THAN RENDER AN
  // EMPTY TABLE. An empty table reads as "nothing is waiting", which is the one
  // wrong answer when the truth is "this screen cannot see the queue".
  if (!outbox) {
    return (
      <div className="screen" data-testid="offline-outbox-unavailable">
        <h2>Offline Outbox</h2>
        <p role="alert">
          The offline queue is not available in this session, so this screen cannot say what is
          waiting. Nothing has been lost and nothing has been posted.
        </p>
      </div>
    );
  }

  if (!outbox.ready) {
    return (
      <div className="screen" data-testid="offline-outbox-loading">
        Reading the offline queue held on this machine…
      </div>
    );
  }

  const pending = outbox.pending;

  return (
    <div className="screen offline-outbox-screen" data-testid="offline-outbox-screen">
      <h2>Offline Outbox</h2>
      <p className="muted">
        Vouchers keyed on this machine that the server could not be reached to post.{" "}
        <strong>Nothing here is in the books.</strong> They are not in the Day Book, in any
        register or in any report, and they carry no voucher number until they commit.
      </p>

      {/* ⛔ DISCLOSURE 1 — always, not only when something is queued. */}
      <p data-testid="offline-outbox-encryption-notice">
        <strong>This queue is stored unencrypted on this machine.</strong> Anyone who can use this
        browser profile can read the vouchers below. It is cleared when you sign out.
      </p>

      {/* ⛔ DISCLOSURE 2 — the loudest thing on the screen when it applies. */}
      {!outbox.durable ? (
        <p role="alert" data-testid="offline-outbox-not-durable">
          <strong>
            This browser has no durable storage, so this queue will NOT survive a reload or a
            closed tab.
          </strong>{" "}
          Commit before you leave this page. (Private-browsing and locked-down profiles do this.)
        </p>
      ) : (
        <p className="muted" data-testid="offline-outbox-durable">
          This queue survives a reload on this machine. It does not travel to another machine, and
          it is not a filing cabinet — anything not committed by sign-out is lost.
        </p>
      )}

      {refusal ? (
        <div role="alert" data-testid="offline-outbox-refusal">
          {/*
            ⛔ THE BANNER, AND THE WORDS THAT MATTER MOST ON THIS SCREEN. It says
            the queue is intact FIRST, before the server's sentence, because the
            operator's next instinct on seeing a failure is to re-key.
          */}
          <p>
            <strong>
              The whole batch was refused, so nothing was judged voucher by voucher and every row
              below is still queued exactly as you keyed it.
            </strong>
          </p>
          <p data-testid="offline-outbox-refusal-message">{refusal.message}</p>
          <p>{refusalRemedy(refusal)}</p>
        </div>
      ) : null}

      {status ? <p role="status">{status}</p> : null}

      {pending.length === 0 ? (
        <p data-testid="offline-outbox-empty">
          Nothing is waiting. An empty outbox is the ordinary state — a voucher only lands here
          when the server could not be reached at all. A voucher the books <em>refused</em> is
          never queued: it is reported on the voucher screen, where the reason is.
        </p>
      ) : (
        <>
          <table data-testid="offline-outbox-table">
            <caption>
              {pending.length} voucher{pending.length === 1 ? "" : "s"} waiting on this machine
              (node {outbox.nodeID})
            </caption>
            <thead>
              <tr>
                {/*
                  ⛔ "Provisional", NEVER "No.". The header is half of what stops a
                  provisional being quoted as a document number; the PROV literal
                  in the value is the other half.
                */}
                <th scope="col">Provisional</th>
                <th scope="col">Change</th>
                <th scope="col">Class</th>
                <th scope="col">Date</th>
                <th scope="col">Narration</th>
                <th scope="col" />
              </tr>
            </thead>
            <tbody>
              {pending.map((i) => (
                <tr key={i.idempotency_key} data-testid="offline-outbox-row">
                  <td>{i.provisional_no}</td>
                  <td>{i.op}</td>
                  <td>{i.class}</td>
                  <td>{i.payload.voucher_date ?? "—"}</td>
                  <td>{i.payload.narration ?? ""}</td>
                  <td>
                    {/*
                      ⛔ DISCARD IS REQUIRED, NOT A CONVENIENCE. One voucher dated
                      inside a closed period refuses the ENTIRE batch (a batch is
                      only as permitted as its most-refused member), so without a
                      way to remove that row the whole queue would be
                      un-committable and the operator's only escape would be
                      signing out — which destroys all of it.
                    */}
                    <button type="button" onClick={() => void onDiscard(i)} disabled={busy}>
                      Discard
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          <p>
            <button
              type="button"
              onClick={() => void onCommit()}
              disabled={busy}
              data-testid="offline-outbox-commit"
            >
              {busy ? "Committing…" : `Commit all ${pending.length} to the books`}
            </button>
          </p>
          {/*
            ⛔ ONE WRITE FOR THE WHOLE QUEUE, AND THE REASON IS STATED because it
            looks like a missing feature otherwise ("why can I not send just
            this one?"). The back-dated cutoff judges the batch as a unit and a
            passkey step-up is charged once per write, so draining row by row is
            not a smaller version of this — it is a worse one.
          */}
          <p className="muted">
            The queue is sent as a single write, oldest first: that is what makes the whole
            outbox cost exactly one passkey step-up instead of one per voucher.
          </p>
          <p className="muted" data-testid="offline-outbox-midflight-notice">
            If a connection dropped <em>while</em> a voucher was being sent, that voucher may
            already have reached the books — nothing the browser can see tells the two apart.
            Check the Day Book for the date before committing if you suspect it.
          </p>
        </>
      )}

      {results.length > 0 ? (
        <>
          <h3>Committed in this session</h3>
          {/*
            ⛔ THE MAPPING SURVIVES THE ROW. A COMMITTED intent leaves the queue
            immediately (the server has it), but the operator may have quoted the
            provisional number to a customer already — so PROV -> final is the one
            thing that lets them correct it, and it stays until the session ends.
          */}
          <table data-testid="offline-outbox-outcomes">
            <thead>
              <tr>
                <th scope="col">Was (provisional)</th>
                <th scope="col">Is now</th>
                <th scope="col">Result</th>
                <th scope="col">Detail</th>
              </tr>
            </thead>
            <tbody>
              {results.map((r) => (
                <tr
                  key={r.outcome.idempotency_key}
                  data-testid="offline-outbox-outcome-row"
                >
                  <td>{r.provisional}</td>
                  <td>{r.outcome.final_voucher_no || "—"}</td>
                  <td>{outcomeLabel(r.outcome)}</td>
                  <td>{r.outcome.detail || ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      ) : null}
    </div>
  );
}

/**
 * What an outcome means, in words an operator can act on.
 *
 * ⛔ "CONFLICT" AND "FROZEN" ARE NOT DEAD ENDS AND MUST NOT READ AS ONE. Both
 * parked a reconciliation row, and that row is the way forward — so both name the
 * screen that holds it. "FROZEN" alone sends an operator looking for a setting to
 * turn off; the pin (an IRN, a filed GSTR-1) is what tells them the answer is a
 * credit note.
 */
function outcomeLabel(o: SyncOutcome): string {
  switch (o.status) {
    case "COMMITTED":
      return "In the books";
    case "DUPLICATE":
      return "Already committed earlier — sent twice, applied once";
    case "CONFLICT":
      return "Refused: this voucher changed on the server first. Handle it in the Reconciliation Queue (Alt+R)";
    case "FROZEN":
      return "Refused: the voucher is pinned and can never be superseded. The Reconciliation Queue (Alt+R) names what pinned it";
    default:
      return o.status;
  }
}

