import { useCallback, useEffect, useRef, useState } from "react";

import { useClient, useCompanyID } from "../app/CompanyContext";
import { useRegisterScreenAction } from "../app/ScreenActions";
import { ApiError } from "../api";
import type { DayBookEntry, EInvoiceRaised, EInvoicePinned } from "../api";
import { usePrintAction } from "./usePrint";

/**
 * THE e-INVOICE / IRN (§1.4 B). cored PROXIES to statutoryd.
 *
 * ===========================================================================
 * ⛔⛔ THIS SCREEN'S KEY IS THE ONLY IRREVERSIBLE ONE IN THE PRODUCT, AND
 * SAYING SO BEFORE IT IS PRESSED IS THIS SCREEN'S PRIMARY JOB
 * ===========================================================================
 * Every other write here is correctable: a voucher can be altered, a voucher
 * can be deleted, an e-way bill can be re-raised with a corrected distance.
 * Raising an e-invoice FREEZES the voucher — from that moment it cannot be
 * altered and cannot be deleted, by anybody, and there is NO RESET PATH
 * ANYWHERE IN THE PRODUCT. The only remedy is a credit note against it.
 *
 * ⛔ SO THE FREEZE IS STATED ON THE FORM, ABOVE THE KEY, BEFORE ANYTHING IS
 * RAISED — not on the result panel. An operator who reads it on the result
 * panel has already frozen the invoice. This is the same argument the e-way
 * bill screen makes for its standing notice, one step more serious: that screen
 * warns about what an artefact IS NOT, and this one warns about what a keypress
 * CANNOT BE UNDONE.
 *
 * ===========================================================================
 * ⛔ AND WHAT COMES BACK IS NOT A GOVERNMENT-ISSUED IRN
 * ===========================================================================
 * No IRP credentials exist in this deployment (HANDOFF §10 item 4), so
 * `FakeProvider` mints the reference. The disclosure is permanent and is never
 * collapsed behind a toggle, for the reason the e-way bill screen states: a
 * reader who has to click to find out what an identifier is not has already
 * decided to trust it.
 *
 * ⚠️ AND THE FAKE IS MORE PERMISSIVE THAN THE THING IT STANDS IN FOR, which is
 * the harder half to see. A real IRP derives IRN = hash(SupplierGSTIN, DocType,
 * DocNo, FY) and REJECTS a document that hashes the same as one already filed.
 * FakeProvider hashes the whole payload and cannot reject anything. So the
 * duplicate-IRN failure mode is INVISIBLE from here, and the class gate on the
 * server — Sales only — is what stands in for it. That gate is the server's,
 * and this screen does not restate its reasoning; it renders the refusal.
 *
 * ===========================================================================
 * WHY A VOUCHER PICKER RATHER THAN A BUTTON ON THE VOUCHER FORM
 * ===========================================================================
 * The same structural reason the e-way bill screen gives: an e-invoice names a
 * voucher, so the voucher must already EXIST, and while the operator is keying
 * one it has no id yet. Tally reaches the same conclusion from the other end —
 * RESEARCH.md:698 records that IRN operations MOVED OUT of the voucher into an
 * e-Invoice REPORT in TallyPrime 4.x, with a "Pending for IRN generation" list.
 * This screen is that list.
 *
 * ⚠️ THE PICKER'S CLASS FILTER IS A CONVENIENCE AND NOT THE RULE — and here
 * that distinction has teeth. The server refuses anything whose
 * `voucher_type.parent_class` is not SALES, BEFORE any line is read, because a
 * Purchase e-invoiced from these books would declare that the company SOLD the
 * goods to its own supplier. That refusal is the authority. This screen narrows
 * the list so an operator is not scrolling past payments, and shows the
 * server's refusal verbatim rather than keeping a second copy of the rule that
 * would drift.
 */

/**
 * The only class an e-invoice can be raised against. A CONVENIENCE filter — the
 * server's `parent_class = 'SALES'` gate is the rule. See the header.
 *
 * ⚠️ Credit and Debit notes are LEGITIMATELY e-invoiceable in India as CRN and
 * DBN, so their absence here is a GAP and not a rule. They are left out because
 * the server refuses them by name: this build models neither the NIC document
 * type nor the reference to the original invoice that must travel with one.
 * Listing them would offer an operator a document this build cannot file.
 */
const E_INVOICEABLE_CLASSES = new Set(["sales"]);

export function EInvoiceScreen() {
  const client = useClient();
  const companyID = useCompanyID();

  const [entries, setEntries] = useState<DayBookEntry[]>([]);
  const [listError, setListError] = useState("");
  const [denied, setDenied] = useState(false);
  const [busy, setBusy] = useState(false);

  const [selectedID, setSelectedID] = useState("");

  /**
   * ⛔ TWO PIECES OF STATE FOR ONE ARTEFACT, AND THEY ARE NOT INTERCHANGEABLE.
   *
   * `raised` is what the RAISE answered and is the ONLY place an acknowledgement
   * number ever exists. `pinned` is what the GET answers, and
   * `voucher_external_reference` (migration 0009) has NO COLUMN for the ack
   * number — so a voucher whose IRN is read back on a later visit genuinely has
   * no ack number to show.
   *
   * Merging them into one object would mean rendering an empty ack field, which
   * reads as "the IRP issued no acknowledgement" — a claim about the IRP rather
   * than about this schema. So they are kept apart and the screen says which
   * answer it is looking at.
   */
  const [raised, setRaised] = useState<EInvoiceRaised | null>(null);
  const [pinned, setPinned] = useState<EInvoicePinned | null>(null);

  /**
   * Which asynchronous answer is allowed to write the artefact state.
   *
   * ⛔ THE RACE IS REAL AND IT WAS CAUGHT BY A TEST, NOT BY REVIEW — the e-way
   * bill screen carries the identical guard and its header records the
   * reproduction. The obvious `let live = true` cleanup flag is NOT enough:
   *
   *   1. the operator selects a voucher; the effect fires GET .../{voucher};
   *   2. before it answers they press Ctrl+A, and the RAISE answers first;
   *   3. the GET's 404 lands — `live` is still true, nothing unmounted and no
   *      dependency changed — and its catch clears the artefact.
   *
   * The operator sees the key do nothing. Here that is materially worse than on
   * the e-way bill screen: the IRN WAS minted and the voucher IS frozen, and the
   * screen has just wiped the only copy of the ack number off the page. So every
   * asynchronous write carries the generation it was issued under and a stale
   * one is DISCARDED.
   */
  const generationRef = useRef(0);

  /**
   * Which voucher the artefact/refusal on screen BELONGS TO.
   *
   * ⛔ NOT the same question `generationRef` answers. That one orders concurrent
   * ANSWERS; this one decides whether a re-run of the load effect is a NEW
   * SELECTION (reset the panel) or the SAME selection re-read because something
   * upstream changed identity (keep it). See the long note in the effect: a
   * step-up replaces the client, and treating that as a new selection throws
   * away the reason the write was refused — or the ack number, which is worse.
   */
  const shownForRef = useRef("");

  /**
   * The generation of the RAISE — a THIRD question, and the one that survived
   * this screen's first fix.
   *
   * ⛔⛔ `shownForRef` STOPS THE LOAD EFFECT WIPING WHAT THE RAISE SET. It does
   * NOTHING about the raise never setting it in the first place, which is the
   * ordering that actually happens: `generationRef` is bumped by the load effect
   * AND by its cleanup, so a step-up's client swap bumps it twice while the
   * retry is still in flight — and the raise then re-checks its generation,
   * finds it moved, and DISCARDS ITS OWN RESULT.
   *
   * ⛔ ON THIS SCREEN THAT LOSS IS UNRECOVERABLE. `raised` is the ONLY place the
   * acknowledgement number ever exists; the schema has no column for it. A
   * discarded success is an IRN that was minted, charged against the voucher,
   * and never shown to anybody.
   *
   * Confirmed on the SIBLING screen in a browser on 2026-08-07 (`EWayBillScreen`
   * — same shape, same ordering, refusal invisible on the press that carried the
   * ceremony and visible on the one that did not), and fixed here at the same
   * time because §1.3's rule is that fixing the reported call site is not fixing
   * the class.
   *
   * A raise is superseded by a NEWER RAISE or by a DIFFERENT DOCUMENT, and by
   * nothing else. A re-render caused by the session token changing is not a
   * change of intent.
   */
  const raiseGenRef = useRef(0);

  /** The server's refusal, rendered verbatim, plus what the operator can do. */
  const [refusal, setRefusal] = useState("");
  /**
   * ⛔ WHETHER THE REFUSAL IS RETRYABLE, WHICH IS THE WHOLE POINT OF THE 422/503
   * SPLIT. They are fixed by different people and collapsing them helps neither:
   * a 422 means THE VOUCHER is wrong and the accountant fixes it, and retrying
   * unchanged cannot help; a 503 means something is down and the operator should
   * simply try again.
   */
  const [retryable, setRetryable] = useState(false);
  const [writeDenied, setWriteDenied] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const res = await client.getDayBook(companyID);
      setEntries(
        res.entries.filter((e) =>
          E_INVOICEABLE_CLASSES.has((e.class ?? "").trim().toLowerCase()),
        ),
      );
      setListError("");
      setDenied(false);
    } catch (e) {
      setEntries([]);
      if (e instanceof ApiError && e.status === 403) {
        setDenied(true);
        setListError("");
      } else {
        setDenied(false);
        setListError(e instanceof Error ? e.message : String(e));
      }
    } finally {
      setBusy(false);
    }
  }, [client, companyID]);

  useEffect(() => {
    void load();
  }, [load]);

  /**
   * Load any ALREADY-RAISED IRN for the highlighted voucher.
   *
   * ⚠️ A 404 IS THE NORMAL ANSWER and must not surface as an error: almost every
   * voucher has no e-invoice and never will.
   */
  useEffect(() => {
    if (selectedID === "") {
      setRaised(null);
      setPinned(null);
      return;
    }
    const mine = ++generationRef.current;
    const current = () => generationRef.current === mine;

    // ⛔⛔ THE RESET IS KEYED ON THE SELECTION CHANGING, NOT ON THIS EFFECT
    // RUNNING — AND THE DIFFERENCE WAS FOUND IN A BROWSER, NOT BY REVIEW.
    //
    // This effect's deps include `client`, and `client` IS REPLACED BY THE FIRST
    // WRITE OF EVERY SESSION. `App.tsx`'s `resolvedClient` memo depends on
    // `session`, a step-up replaces the session's token, and so a fresh
    // `CoredClient` is handed to every screen the moment the passkey ceremony
    // completes. The sequence that exposed it, driven against real cored:
    //
    //   POST .../e-invoices          -> 403 STEPUP_REQUIRED
    //   webauthn assert begin/finish -> 200   (client identity changes HERE)
    //   POST .../e-invoices (retry)  -> 422   setRefusal(the server's reason)
    //   ...this effect re-runs, because `client` is a new object...
    //   -> the unconditional setRefusal("") below WIPED IT
    //
    // The operator pressed the key and NOTHING happened: no result, no error,
    // no status. The invoice was genuinely refused and the reason was genuinely
    // received and thrown away one render later. It reproduces on the FIRST
    // raise of every session, which is the only one most operators ever do.
    //
    // ⛔ AND THE SUCCESS PATH IS WORSE THAN THE REFUSAL PATH. On a successful
    // first raise the same re-run would clear `raised`, and `raised` is the ONLY
    // place the acknowledgement number ever exists — the schema has no column
    // for it. That is an unrecoverable loss, not a cosmetic one.
    const selectionChanged = shownForRef.current !== selectedID;
    shownForRef.current = selectedID;
    if (selectionChanged) {
      setRefusal("");
      setRetryable(false);
      setWriteDenied(false);
      setRaised(null);
      setPinned(null);
      // A DIFFERENT document genuinely supersedes an in-flight raise; a client
      // swap does not. See raiseGenRef.
      raiseGenRef.current++;
    }

    client
      .getEInvoice(companyID, selectedID)
      .then((p) => {
        if (!current()) return;
        setPinned(p);
      })
      .catch((e: unknown) => {
        if (!current()) return;
        setPinned(null);
        if (e instanceof ApiError && e.status === 404) return;
        setRefusal(e instanceof Error ? e.message : String(e));
      });
    // ⛔ THE CLEANUP BUMPS THE GENERATION rather than setting a local flag, so an
    // in-flight GET is invalidated by an unmount, a dependency change AND a
    // raise — one mechanism covering all three.
    return () => {
      generationRef.current++;
    };
  }, [client, companyID, selectedID]);

  usePrintAction({ voucher: { voucherID: null } });

  // ---- Ctrl+A — RAISE THE e-INVOICE. IRREVERSIBLE.
  useRegisterScreenAction(
    "accept",
    useCallback((): string => {
      if (selectedID === "") {
        return "Select the invoice to report first — an e-invoice names a document.";
      }
      if (busy) return "Still working.";
      // ⚠️ NOT A REFUSAL, AND DELIBERATELY SO. Re-raising is SAFE: the workflow's
      // `generate` step is memoized, so the same IRN comes back rather than a
      // second one being minted. Refusing here would teach the operator that the
      // artefact is fragile, and would hide the one property that makes a retry
      // after a transient fault safe to advise.
      if (pinned || raised) {
        return "This invoice already carries an IRN — re-raising returns the same one, never a new one.";
      }
      setBusy(true);
      setRefusal("");
      setRetryable(false);
      setWriteDenied(false);
      // ⛔ CLAIM THE GENERATION. Any GET issued before this point is now stale
      // and its answer discarded — including the 404 that would otherwise land
      // after this raise and wipe the ack number off the screen.
      ++generationRef.current;
      // ⛔ AND CLAIM THE RAISE GENERATION SEPARATELY — see raiseGenRef. Sharing
      // one counter is what let the load effect throw away an ack number.
      const mine = ++raiseGenRef.current;
      void (async () => {
        try {
          const res = await client.raiseEInvoice(companyID, {
            voucher_id: selectedID,
          });
          if (raiseGenRef.current !== mine) return;
          setRaised(res);
          setPinned(null);
        } catch (e) {
          if (raiseGenRef.current !== mine) return;
          setRaised(null);
          if (e instanceof ApiError) {
            if (e.status === 403) {
              setWriteDenied(true);
              setRefusal("");
            } else {
              setRefusal(e.message);
              // 503 is the retryable family. See `retryable`'s note.
              setRetryable(e.status === 503);
            }
          } else {
            setRefusal(e instanceof Error ? e.message : String(e));
          }
        } finally {
          setBusy(false);
        }
      })();
      return "Reporting the invoice to the IRP…";
    }, [busy, client, companyID, pinned, raised, selectedID]),
  );

  if (denied) {
    return (
      <section aria-label="e-Invoice">
        <h2>e-Invoice</h2>
        <p>
          You are not permitted to see this company&apos;s vouchers, so no
          invoice can be selected.
        </p>
      </section>
    );
  }

  const irn = raised?.irn ?? pinned?.irn ?? "";
  const pinnedNo = raised?.pinned_voucher_no ?? pinned?.pinned_voucher_no ?? "";
  const voucherNo = raised?.voucher_no ?? pinned?.voucher_no ?? "";
  /**
   * ⛔ `raised` WINS OUTRIGHT — the GET's answer is not merged into it.
   *
   * After a successful raise the load effect re-reads the voucher and the GET
   * now succeeds, so BOTH pieces of state are populated for the same document.
   * They agree on the reference and the pinned number, and they disagree on
   * exactly one thing: `pinned` has no acknowledgement number and never can.
   * Rendering both blocks would print "here is the ack number" directly above
   * "no ack number is shown, it is returned only at the moment of reporting" —
   * about the same document, in the same panel, in the same breath.
   */
  const readBack = raised === null ? pinned : null;

  return (
    <section aria-label="e-Invoice">
      <h2>e-Invoice</h2>

      {/*
        ⛔ THE STANDING NOTICE, on the screen BEFORE anything is raised and still
        there afterwards. See the file header.
      */}
      <p role="note">
        ⛔ This build has no IRP credentials, so nothing here is submitted to the
        Invoice Registration Portal and nothing here is a government-issued IRN.
        What it produces is the payload that would be reported, and a locally
        generated reference.
      </p>

      {listError !== "" && <p role="alert">{listError}</p>}

      <h3>The invoice to report</h3>
      {entries.length === 0 ? (
        <p>
          No sales invoice in this period, so there is nothing to report. An
          e-invoice is the outward document a seller issues, so it can only be
          raised against a Sales invoice.
        </p>
      ) : (
        <ul>
          {entries.map((e) => (
            <li key={e.voucher_id}>
              <label>
                <input
                  type="radio"
                  name="einv-voucher"
                  value={e.voucher_id}
                  checked={selectedID === e.voucher_id}
                  onChange={() => setSelectedID(e.voucher_id)}
                />
                {e.date} · {e.voucher_type} {e.voucher_no} · {e.party}
              </label>
            </li>
          ))}
        </ul>
      )}

      {/*
        ⛔⛔ THE FREEZE WARNING. ABOVE THE KEY, NOT ON THE RESULT PANEL. This is
        the whole reason this screen reads differently from the e-way bill's.
      */}
      <h3>What Ctrl+A does, and it cannot be undone</h3>
      <p role="note">
        ⛔ Raising an e-invoice FREEZES this voucher. From that moment it cannot
        be altered and it cannot be deleted — by anyone, including an Owner — and
        there is no undo anywhere in this product. Check the invoice before you
        press the key. If it turns out to be wrong afterwards, the remedy is a
        credit note against it, not a correction to it.
      </p>
      <p>
        Nothing else is asked of you here: an e-invoice restates the invoice, and
        every figure on it — both parties&apos; GSTIN, legal name and state code,
        and every item line with its HSN, unit and rate — is read from the books
        by the server. There is nothing to key, and so nothing to mistype onto a
        document that is about to be frozen.
      </p>

      <p>Ctrl+A reports the invoice.</p>

      {writeDenied && (
        <p role="alert">
          You are not permitted to raise an e-invoice on this company.
        </p>
      )}
      {refusal !== "" && (
        <div role="alert">
          <p>{refusal}</p>
          {/*
            ⛔ THE TWO FAMILIES GET DIFFERENT ADVICE BECAUSE DIFFERENT PEOPLE FIX
            THEM. Telling an accountant to "try again" when their voucher is
            wrong wastes their afternoon; telling them to fix a voucher that is
            correct, when statutoryd is simply down, sends them editing a good
            invoice.
          */}
          {retryable ? (
            <p>
              Nothing is wrong with this invoice — a part of the filing stack did
              not answer. Try again. A retry cannot mint a second IRN: if the
              first attempt did reach the portal, the same reference comes back.
            </p>
          ) : (
            <p>
              This invoice was NOT reported and it has NOT been frozen. Correct
              it and raise it again — retrying it unchanged will be refused
              identically.
            </p>
          )}
        </div>
      )}

      {irn !== "" && (
        <section aria-label="The reported e-invoice">
          <h3>Reported</h3>

          {/*
            ⛔ THE FREEZE IS STATED FIRST, ABOVE THE IDENTIFIER. The handler that
            returns `pinned_voucher_no` says why in as many words: a screen that
            does not show it leaves the operator to discover the freeze by being
            refused an edit later, with nothing connecting that refusal to this
            action.
          */}
          <p role="alert">
            ⛔ Voucher {pinnedNo} is now FROZEN. It can no longer be altered or
            deleted. Issue a credit note against it if it needs to change.
          </p>

          <dl>
            <dt>Reference</dt>
            <dd>{irn}</dd>
            <dt>Document</dt>
            <dd>{voucherNo}</dd>
            {raised && (
              <>
                <dt>Acknowledgement number</dt>
                <dd>{raised.ack_no}</dd>
                <dt>Status</dt>
                <dd>{raised.status}</dd>
              </>
            )}
            {readBack && (
              <>
                <dt>Reported by</dt>
                <dd>{readBack.created_by}</dd>
                <dt>Reported at</dt>
                <dd>{readBack.created_at}</dd>
              </>
            )}
          </dl>

          {/*
            ⛔ NOT "IRN". The label above says "Reference" and this sentence says
            what it is not, in the same block, because a reader who saw only the
            label would reasonably assume otherwise.
          */}
          <p role="note">
            This reference is NOT a government-issued IRN. A real IRN is returned
            by the Invoice Registration Portal, which this build cannot reach;
            this one was generated locally and was never submitted. The
            acknowledgement number and status shown alongside it are the stand-in
            provider&apos;s too.
          </p>

          {/*
            ⚠️ THE ACK NUMBER EXISTS ON THIS ANSWER AND NOWHERE ELSE. Say so
            while it is on screen, rather than letting the operator find out by
            coming back to a panel that no longer has it.
          */}
          {raised && (
            <p role="note">
              The acknowledgement number is shown here and is not stored: this
              product records the reference and the frozen voucher number, and
              has no field for it. Copy it now if it is needed.
            </p>
          )}
          {readBack && (
            <p role="note">
              This e-invoice was reported earlier, so no acknowledgement number
              is shown — it is returned only at the moment of reporting and this
              product has no field to store it.
            </p>
          )}
        </section>
      )}
    </section>
  );
}
