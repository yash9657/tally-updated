import { useCallback, useEffect, useState } from "react";

import { ApiError } from "../api";
import type {
  ReconciliationEntry,
  ReconciliationPayloadResponse,
  VoucherView,
} from "../api";
import { useClient, useCompanyID } from "../app/CompanyContext";

/**
 * RECONCILIATION QUEUE — every alter or delete this company's books refused, and
 * what to do about each one (track D).
 *
 * Reached from `Gateway of Tally > Reconciliation Queue` and from Alt+R.
 *
 * ⛔ THIS SCREEN FIXES A PRESENT-TENSE DEFECT, NOT ONLY AN OFFLINE ONE. The
 * ledger's freeze rule has been raising queue rows since migration 0009 — every
 * time somebody tries to alter or delete a voucher pinned by an IRN or a filed
 * GSTR-1 — and `GET /reconciliation-queue` has served them the whole time. NOTHING
 * IN THIS APP EVER CALLED IT (`/usr/bin/grep -rn "reconciliation-queue"
 * frontend/src/` found only an ACL token and a 501 report name), so those rows
 * have never been shown to anybody. An operator's edit was refused and the
 * explanation was written to a table only Postgres could read.
 *
 * ⛔ THE TWO KINDS ON THIS LIST ARE NOT INTERCHANGEABLE AND THE SCREEN MUST NOT
 * BLUR THEM:
 *
 *   CONFLICT  an offline intent that lost a race. The loser's payload was
 *             PRESERVED, so both sides can be shown and the operator chooses.
 *   IRN /     the freeze rule. The voucher can NEVER be superseded, so there is
 *   FILED_    nothing to choose — it is explained, not retried — and no payload
 *   GSTR1     was ever parked, because the ledger raised the row rather than
 *             sync.parkConflict.
 *
 * ⛔⛔ THERE ARE EXACTLY TWO CHOICES ON A CONFLICT AND "FORCE MY VERSION" IS NOT
 * ONE OF THEM. The base version is stale PERMANENTLY — the head has moved and
 * will not move back — so a retried ALTER would be refused again by construction.
 * Offering it would be a button that cannot work, and the failure mode is worse
 * than a dead button: an operator who believes their version can still win does
 * not re-key it, and the work is lost quietly. The honest options are DISCARD
 * MINE and RE-KEY AS A NEW VOUCHER, and this screen says so in those words.
 *
 * ⚠️ RESOLVING DOES NOT REPLAY ANYTHING, AND NOTHING ANYWHERE DOES. It records
 * that a human dealt with the item. The screen therefore never uses the word
 * "retry" for it: the ways back into the books are a fresh voucher or a credit
 * note, both ordinary writes judged on their own dates.
 */
export function ReconciliationScreen(): JSX.Element {
  const client = useClient();
  const companyID = useCompanyID();

  const [entries, setEntries] = useState<ReconciliationEntry[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [openID, setOpenID] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);

  const load = useCallback(async () => {
    setListError(null);
    try {
      const res = await client.reconciliationQueue(companyID);
      setEntries(res.entries);
    } catch (e) {
      setEntries([]);
      setListError(
        e instanceof ApiError
          ? `The reconciliation queue could not be read (${e.status}): ${e.message}`
          : String(e),
      );
    }
  }, [client, companyID]);

  useEffect(() => {
    void load();
  }, [load]);

  const onResolved = useCallback(
    async (entry: ReconciliationEntry) => {
      setOpenID(null);
      // ⛔ RE-READ THE LIST RATHER THAN SPLICING THE ROW OUT LOCALLY. The queue is
      // the oracle for whether resolved_at was actually stamped; a screen that
      // removed the row from its own state would look identical whether the write
      // landed or did nothing at all.
      await load();
      setStatus(
        `Entry cleared. The refused ${entry.attempted_op.toLowerCase()} is still refused — ` +
          `resolving records that somebody dealt with it, it does not re-apply anything.`,
      );
    },
    [load],
  );

  if (entries === null) {
    return (
      <div className="screen" data-testid="reconciliation-loading">
        Reading the reconciliation queue…
      </div>
    );
  }

  const open = entries.find((e) => e.id === openID) ?? null;

  return (
    <div className="screen reconciliation-screen" data-testid="reconciliation-screen">
      <h2>Reconciliation Queue</h2>
      <p className="muted">
        Every alter or delete these books refused, and why. Nothing here has been applied.
      </p>

      {listError ? <p role="alert">{listError}</p> : null}
      {status ? <p role="status">{status}</p> : null}

      {entries.length === 0 ? (
        <p data-testid="reconciliation-empty">
          Nothing is waiting. No alter or delete has been refused — a queue that is empty is
          the ordinary state, not a sign that something is missing.
        </p>
      ) : (
        <table>
          <caption>{entries.length} open {entries.length === 1 ? "entry" : "entries"}</caption>
          <thead>
            <tr>
              <th scope="col">Refused</th>
              <th scope="col">Why</th>
              <th scope="col">Reference</th>
              <th scope="col">Voucher</th>
              <th scope="col">Raised</th>
              <th scope="col" />
            </tr>
          </thead>
          <tbody>
            {entries.map((e) => (
              <tr key={e.id}>
                <td>{e.attempted_op}</td>
                <td>{reasonLabel(e)}</td>
                <td>{e.ref_value || "—"}</td>
                {/*
                  The FROZEN number is a real voucher number and is safe to show.
                  A conflict row carries "" here, and an empty cell is correct: the
                  loser never had a final number.
                */}
                <td>{e.frozen_voucher_no || "—"}</td>
                <td>{e.created_at}</td>
                <td>
                  <button type="button" onClick={() => setOpenID(e.id)}>
                    {e.kind === "CONFLICT" ? "Compare" : "Explain"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {open ? (
        <ReconciliationDetail
          key={open.id}
          entry={open}
          onClose={() => setOpenID(null)}
          onResolved={() => void onResolved(open)}
        />
      ) : null}
    </div>
  );
}

/**
 * Why an entry is on the list, in words an operator can act on.
 *
 * ⛔ "FROZEN" ALONE IS UNACTIONABLE. The operator's next step depends entirely on
 * WHAT pinned the voucher: an IRN means the invoice is e-invoiced and the fix is a
 * credit note; a filed GSTR-1 means the return for that period is already with the
 * portal. Naming only the state sends them to look for a setting to turn off.
 */
function reasonLabel(e: ReconciliationEntry): string {
  switch (e.kind) {
    case "CONFLICT":
      return "Someone else changed this voucher first";
    case "IRN":
      return "E-invoiced: an IRN is registered against it";
    case "FILED_GSTR1":
      return "The GSTR-1 covering it has been filed";
    default:
      // An unknown kind is shown verbatim rather than mapped to a guess. A new
      // freeze reason added server-side must not read as one of the above.
      return e.kind;
  }
}

/** One entry, opened: the comparison (or the explanation) and the one write. */
function ReconciliationDetail({
  entry,
  onClose,
  onResolved,
}: {
  entry: ReconciliationEntry;
  onClose: () => void;
  onResolved: () => void;
}): JSX.Element {
  const client = useClient();
  const companyID = useCompanyID();

  const [parked, setParked] = useState<ReconciliationPayloadResponse | null>(null);
  const [live, setLive] = useState<VoucherView | null>(null);
  const [liveGone, setLiveGone] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [resolving, setResolving] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const p = await client.reconciliationPayload(companyID, entry.id);
        if (!cancelled) setParked(p);
      } catch (e) {
        if (!cancelled) {
          setError(
            e instanceof ApiError
              ? `The keyed version could not be read (${e.status}): ${e.message}`
              : String(e),
          );
        }
      }
      try {
        const v = await client.getVoucher(companyID, entry.voucher_id);
        if (!cancelled) setLive(v);
      } catch (e) {
        // ⚠️ A 404 HERE IS INFORMATION, NOT A FAULT. The live head is gone when
        // the winning change was a DELETE — which is precisely the case where an
        // operator most needs to be told, because their edit lost to a deletion
        // and re-keying it would resurrect a voucher somebody removed on purpose.
        if (!cancelled && e instanceof ApiError && e.status === 404) setLiveGone(true);
        else if (!cancelled) setError(String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [client, companyID, entry.id, entry.voucher_id]);

  const resolve = useCallback(async () => {
    setResolving(true);
    setError(null);
    try {
      await client.resolveReconciliation(companyID, entry.id);
      onResolved();
    } catch (e) {
      setResolving(false);
      setError(
        e instanceof ApiError && e.status === 404
          ? "That entry is no longer open — somebody else cleared it. Reloading the list."
          : `The entry could not be cleared: ${String(e)}`,
      );
    }
  }, [client, companyID, entry.id, onResolved]);

  const isConflict = entry.kind === "CONFLICT";

  return (
    <section
      className="reconciliation-detail"
      aria-label={`Entry ${entry.id}`}
      data-testid="reconciliation-detail"
    >
      <h3>{isConflict ? "Two versions of this voucher" : "This voucher cannot be changed"}</h3>
      <p>{entry.detail}</p>
      {error ? <p role="alert">{error}</p> : null}

      {isConflict ? (
        <div className="compare" data-testid="reconciliation-compare">
          <div className="pane" data-testid="pane-live">
            <h4>What the books hold now</h4>
            {liveGone ? (
              <p>
                Nothing — the change that won was a <strong>deletion</strong>. This voucher is
                no longer in the books, so re-keying your version would bring back something
                somebody removed deliberately.
              </p>
            ) : live ? (
              <LiveVoucherPane voucher={live} />
            ) : (
              <p>Reading…</p>
            )}
          </div>
          <div className="pane" data-testid="pane-parked">
            <h4>What was keyed</h4>
            {parked?.has_payload && parked.intent ? (
              <ParkedVoucherPane parked={parked} />
            ) : parked ? (
              <p>
                {parked.payload_absent_because ??
                  "No keyed version was preserved for this entry."}
              </p>
            ) : (
              <p>Reading…</p>
            )}
          </div>
        </div>
      ) : (
        <>
          <p>
            <strong>{reasonLabel(entry)}.</strong> A voucher pinned this way can never be
            superseded — that is the point of the pin, and there is no setting that lifts it.
            {entry.frozen_voucher_no ? ` The voucher is ${entry.frozen_voucher_no}.` : ""}
          </p>
          <p>
            To correct what it says, issue a <strong>credit note</strong> (or a fresh voucher)
            rather than editing this one. That is an ordinary entry, dated today, and it leaves
            the filed document exactly as it was filed.
          </p>
          {/*
            ⚠️ The freeze row never carried a payload, so there is nothing to show
            on the right. Saying WHERE the operator's own version is beats an empty
            pane: it is on the machine that keyed it, and nowhere else.
          */}
          {parked && !parked.has_payload && parked.payload_absent_because ? (
            <p className="muted">{parked.payload_absent_because}.</p>
          ) : null}
        </>
      )}

      <div className="actions">
        {isConflict ? (
          <>
            {/*
              ⛔ TWO BUTTONS, AND NEITHER OF THEM RE-APPLIES THE EDIT. "Discard
              mine" clears the entry. Re-keying is a NEW voucher the operator
              enters themselves — deliberately not automated here, because a fresh
              CREATE from a stale payload is a decision (the winning version may
              already say what they wanted) and not a mechanical step.
            */}
            <button type="button" onClick={() => void resolve()} disabled={resolving}>
              Discard my version and clear this entry
            </button>
            <p className="muted">
              Keeping your version means entering it again as a <strong>new</strong> voucher.
              It cannot be applied on top of this one: the version it was based on has already
              been superseded, so the books would refuse it again.
            </p>
          </>
        ) : (
          <button type="button" onClick={() => void resolve()} disabled={resolving}>
            I have dealt with this — clear the entry
          </button>
        )}
        <button type="button" onClick={onClose}>
          Close
        </button>
      </div>
    </section>
  );
}

/** The live head, as the books hold it. */
function LiveVoucherPane({ voucher }: { voucher: VoucherView }): JSX.Element {
  return (
    <dl>
      <dt>Number</dt>
      <dd>{voucher.voucher_no}</dd>
      <dt>Date</dt>
      <dd>{voucher.voucher_date}</dd>
      <dt>Party</dt>
      <dd>{voucher.party || "—"}</dd>
      <dt>Narration</dt>
      <dd>{voucher.narration || "—"}</dd>
      <dt>Postings</dt>
      <dd>
        <ul>
          {voucher.postings.map((p, i) => (
            <li key={`${p.ledger_id}-${i}`}>
              {p.ledger} {p.dr_cr} {p.amount}
            </li>
          ))}
        </ul>
      </dd>
    </dl>
  );
}

/**
 * The parked version, as the operator keyed it.
 *
 * ⛔ THE AMOUNTS ARE PRINTED EXACTLY AS THE SERVER SENT THEM AND ARE NOT
 * RE-FORMATTED HERE. The server already renders them the way the live pane's
 * amounts are rendered (fixed-4), precisely so the two columns are comparable;
 * re-formatting one side is how "1180" ends up beside "1180.0000" and an operator
 * discards their work over a difference that does not exist.
 *
 * ⚠️ THE PROVISIONAL NUMBER IS LABELLED "Provisional", NEVER "Number". It is not
 * a voucher number and never was — showing it in the field a real number occupies
 * is how somebody reads a PROV- label down a phone line to a customer.
 */
function ParkedVoucherPane({ parked }: { parked: ReconciliationPayloadResponse }): JSX.Element {
  const intent = parked.intent;
  if (!intent) return <p>No keyed version was preserved.</p>;
  const v = intent.payload;
  return (
    <dl>
      <dt>Provisional</dt>
      <dd>{intent.provisional_no || "—"}</dd>
      <dt>Keyed on</dt>
      <dd>{intent.node_id}</dd>
      <dt>Based on version</dt>
      <dd>{intent.base_version}</dd>
      <dt>Date</dt>
      <dd>{v.voucher_date}</dd>
      <dt>Party</dt>
      <dd>{v.party || "—"}</dd>
      <dt>Narration</dt>
      <dd>{v.narration || "—"}</dd>
      <dt>Postings</dt>
      <dd>
        <ul>
          {v.postings.map((p, i) => (
            <li key={`${p.ledger_id}-${i}`}>
              {p.ledger_id} {p.dr_cr} {p.amount}
            </li>
          ))}
        </ul>
      </dd>
      {v.also_carries.length > 0 ? (
        <>
          <dt>Also carries</dt>
          {/*
            ⛔ NAMED, NOT DROPPED. These are blocks a two-column diff has no room
            for (GST parameters, inventory dimensions, cost splits). Omitting them
            silently is how two panes look identical and differ.
          */}
          <dd>{v.also_carries.join(", ")}</dd>
        </>
      ) : null}
    </dl>
  );
}
