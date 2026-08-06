import { useCallback, useEffect, useRef, useState } from "react";

import { useClient, useCompanyID } from "../app/CompanyContext";
import { useRegisterScreenAction } from "../app/ScreenActions";
import { ApiError } from "../api";
import type { DayBookEntry, EWayBillResponse } from "../api";
import { usePrintAction } from "./usePrint";

/**
 * THE e-WAY BILL (T5, Rule 138, migration 0047).
 *
 * ===========================================================================
 * ⛔⛔ WHAT THIS SCREEN SHOWS IS NOT A GOVERNMENT-ISSUED e-WAY BILL, AND SAYING
 * SO IS THIS SCREEN'S PRIMARY JOB — NOT A FOOTNOTE ON IT
 * ===========================================================================
 * The NIC e-Way Bill portal is unreachable from this deployment. Nothing here
 * has been submitted to anybody. What the server produces is the PAYLOAD that
 * WOULD be submitted plus a deterministic LOCAL REFERENCE, and an EBN is
 * something only NIC issues.
 *
 * ⛔ SO THE IDENTIFIER IS NEVER LABELLED "e-Way Bill Number", "EBN" or
 * "Number" anywhere on this screen. It is labelled "Local reference" and it
 * carries the sentence that says what it is not, immediately, in the same
 * block. A driver stopped at a checkpost with a sheet carrying a
 * government-looking number that no government issued is worse off than a
 * driver carrying nothing: the sheet tells them they are covered.
 *
 * ⛔ AND `disclosures` IS RENDERED IN FULL, ALWAYS, NEVER COLLAPSED BEHIND A
 * TOGGLE AND NEVER TRUNCATED. It is the whole disclosure discipline: it states
 * that no submission was made, that the Rule 138 figures are unverified
 * secondary material, and — when Part B is outstanding — that the goods may
 * not move yet. Every one of those sentences is one somebody could act on
 * wrongly if it were hidden one click away.
 *
 * ===========================================================================
 * WHY THE SCREEN HAS A VOUCHER PICKER RATHER THAN LIVING ON THE VOUCHER FORM
 * ===========================================================================
 * Tally raises an e-way bill FROM the sales voucher (RESEARCH.md:284). It
 * cannot be done that way here and the reason is structural rather than a
 * shortcut: a bill names a voucher, so the voucher must already EXIST — and at
 * the moment the operator is keying one it does not have an id yet. Tally
 * solves this by raising the bill after the voucher is accepted, which is
 * exactly what this screen does, one key away.
 *
 * ⚠️ THE PICKER'S CLASS FILTER IS A CONVENIENCE AND NOT THE RULE. The server
 * refuses any voucher with no stock lines ("an e-way bill covers a movement of
 * goods"), and that refusal is the authority. This screen narrows the list to
 * the classes that CAN move goods so an operator is not scrolling past
 * payments — but a Sales voucher with no items passes the filter and is
 * refused by the server, and the screen shows that refusal verbatim rather
 * than pre-empting it with a second copy of the rule that would drift.
 *
 * ===========================================================================
 * THE THREE THINGS THE FORM MUST NOT DEFAULT
 * ===========================================================================
 *  1. DISTANCE. Blank is not zero and zero is not "nearby": the server refuses
 *     to compute a validity from an unstated distance rather than granting the
 *     one-day minimum, because a bill whose expiry the system guessed expires
 *     without warning. The field starts EMPTY and the refusal, with its
 *     reasoning, is shown when it stays that way.
 *  2. OVER-DIMENSIONAL CARGO. Nothing in a voucher says whether a load is
 *     over-dimensional and assuming ordinary grants TEN TIMES the validity the
 *     rule allows. It is an explicit checkbox the operator ticks.
 *  3. THE ROUTE. From/to default to the company's and the party's master
 *     addresses, which is right for the common case and wrong whenever goods
 *     ship from a warehouse. The overrides are on the form and the screen says
 *     what the blank ones will use.
 *
 * ⛔ AND NO RULE 138 FIGURE IS RESTATED ANYWHERE IN THIS FILE. Not the
 * threshold, not the 50 km relaxation, not the km-per-day rates. The screen
 * renders the server's conclusions and the server's sentences; a number copied
 * onto a screen is a number that disagrees with the rule the first time the
 * rule is corrected.
 */

/**
 * The voucher classes that can move goods. See the header: a CONVENIENCE
 * filter, never the rule.
 */
const GOODS_CLASSES = new Set([
  "sales",
  "purchase",
  "delivery note",
  "receipt note",
  "stock journal",
]);

/** The four modes NIC codes. Mirrors rules.TransportMode. */
const MODES = ["ROAD", "RAIL", "AIR", "SHIP"] as const;

export function EWayBillScreen() {
  const client = useClient();
  const companyID = useCompanyID();

  const [entries, setEntries] = useState<DayBookEntry[]>([]);
  const [listError, setListError] = useState("");
  const [denied, setDenied] = useState(false);
  const [busy, setBusy] = useState(false);

  const [selectedID, setSelectedID] = useState("");
  const [bill, setBill] = useState<EWayBillResponse | null>(null);

  /**
   * Which asynchronous answer is allowed to write `bill`. See the long note on
   * the load effect below: without it, a GET that 404s AFTER a successful raise
   * silently wipes the bill off the screen, with no error anywhere.
   *
   * A ref and not state, deliberately — bumping it must NOT cause a render, and
   * it must be readable by callbacks that closed over an earlier render.
   */
  const generationRef = useRef(0);

  // ---- The consignment, as the operator states it.
  const [distance, setDistance] = useState("");
  const [vehicle, setVehicle] = useState("");
  const [mode, setMode] = useState<string>("ROAD");
  const [transporterID, setTransporterID] = useState("");
  const [transporterName, setTransporterName] = useState("");
  const [odc, setOdc] = useState(false);
  const [fromPincode, setFromPincode] = useState("");
  const [toPincode, setToPincode] = useState("");

  /**
   * The server's refusal, kept as its own state and rendered verbatim.
   *
   * ⛔ `refusalDetail` HOLDS THE LIST — Assess and ValidateEWayBill both return
   * EVERY problem rather than the first, deliberately, so an operator fixing a
   * consignment gets one round of corrections instead of five. Flattening them
   * into the single `refusal` line would throw that away at the last hop.
   */
  const [refusal, setRefusal] = useState("");
  const [refusalDetail, setRefusalDetail] = useState<string[]>([]);
  const [writeDenied, setWriteDenied] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const res = await client.getDayBook(companyID);
      setEntries(
        res.entries.filter((e) =>
          GOODS_CLASSES.has((e.class ?? "").trim().toLowerCase()),
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
   * Load any ALREADY-RAISED bill for the highlighted voucher.
   *
   * ⚠️ A 404 IS THE NORMAL ANSWER and must not surface as an error: most
   * vouchers have no e-way bill and never will. Only a non-404 is a problem
   * worth telling the operator about.
   *
   * ===========================================================================
   * ⛔⛔ `generation` IS A RACE GUARD, AND THE RACE IS REAL — IT WAS CAUGHT BY
   * THE REAL-SHELL TEST IN EWayBillScreen.test.tsx, NOT BY REVIEW
   * ===========================================================================
   * The obvious `let live = true` cleanup flag is NOT enough here, and the
   * reason is worth stating because the bug it hides is silent:
   *
   *   1. the operator selects a voucher; this effect fires GET .../{voucher};
   *   2. before that answers, they key the consignment and press Ctrl+A, and
   *      the RAISE answers first with a real bill (setBill(res));
   *   3. the GET's 404 then lands — `live` is still true, because nothing
   *      unmounted and no dependency changed — and its catch clears `bill`.
   *
   * The operator sees the key do nothing. The bill WAS raised and IS in the
   * database; the screen just wiped it off the page. There is no error, no
   * status line and nothing in any log. Reloading the screen shows the bill,
   * which makes it look like a fluke.
   *
   * So every asynchronous write to `bill` carries the generation it was issued
   * under, and a stale generation is DISCARDED rather than applied. The raise
   * bumps the generation precisely so a GET issued before it can never
   * overwrite it.
   */
  useEffect(() => {
    if (selectedID === "") {
      setBill(null);
      return;
    }
    const mine = ++generationRef.current;
    const current = () => generationRef.current === mine;
    setRefusal("");
    setRefusalDetail([]);
    client
      .getEWayBill(companyID, selectedID)
      .then((b) => {
        if (!current()) return;
        setBill(b);
        // Pre-fill from what was raised, so a REGENERATION starts from the
        // stated consignment rather than from a blank form. Correcting one
        // field should not mean re-keying the other six.
        setDistance(String(b.distance_km));
        setVehicle(b.vehicle_number);
        setMode(b.transport_mode || "ROAD");
        setTransporterID(b.transporter_id);
        setTransporterName(b.transporter_name);
        setOdc(b.over_dimensional_cargo);
        setFromPincode(b.from_pincode);
        setToPincode(b.to_pincode);
      })
      .catch((e: unknown) => {
        if (!current()) return;
        setBill(null);
        if (e instanceof ApiError && e.status === 404) return;
        setRefusal(e instanceof Error ? e.message : String(e));
      });
    // ⛔ THE CLEANUP BUMPS THE GENERATION rather than setting a local flag, so
    // an in-flight GET is invalidated by an unmount, a dependency change AND a
    // raise — one mechanism covering all three, instead of a `live` flag that
    // covers only the first two.
    return () => {
      generationRef.current++;
    };
  }, [client, companyID, selectedID]);

  usePrintAction({ voucher: { voucherID: null } });

  // ---- Ctrl+A — RAISE THE BILL.
  useRegisterScreenAction(
    "accept",
    useCallback((): string => {
      if (selectedID === "") {
        return "Select the voucher the goods move against first — an e-way bill names a document.";
      }
      if (busy) return "Still working.";
      setBusy(true);
      setRefusal("");
      setRefusalDetail([]);
      setWriteDenied(false);
      // ⛔ CLAIM THE GENERATION. Any GET issued before this point is now stale
      // and its answer will be discarded — including the 404 that would
      // otherwise land after this raise and wipe the bill off the screen.
      const mine = ++generationRef.current;
      void (async () => {
        try {
          const res = await client.raiseEWayBill(companyID, {
            voucher_id: selectedID,
            transporter_id: transporterID.trim(),
            transporter_name: transporterName.trim(),
            vehicle_number: vehicle.trim(),
            transport_mode: mode as (typeof MODES)[number],
            // ⛔ AN EMPTY FIELD SENDS 0, WHICH THE SERVER READS AS "NOT STATED"
            // AND REFUSES. It is deliberately not omitted-and-defaulted here:
            // the refusal, with Assess's reasoning, is the correct outcome and
            // the operator needs to see it.
            distance_km: Number(distance.trim() || "0") || 0,
            over_dimensional_cargo: odc,
            from_pincode: fromPincode.trim(),
            to_pincode: toPincode.trim(),
            created_by: "operator",
          });
          if (generationRef.current !== mine) return;
          setBill(res);
        } catch (e) {
          // A raise superseded by a NEWER raise (the operator pressed Ctrl+A
          // twice) must not report the older attempt's outcome over the newer
          // one's — the same discipline as the load effect, in the other
          // direction.
          if (generationRef.current !== mine) return;
          setBill(null);
          if (e instanceof ApiError) {
            if (e.status === 403) {
              setWriteDenied(true);
              setRefusal("");
            } else {
              setRefusal(e.message);
              // The server returns `blockers` / `basis` / `violations`
              // alongside the message; each is a separate correctable problem.
              const body = (e as unknown as { body?: Record<string, unknown> })
                .body;
              const pick = (k: string) =>
                Array.isArray(body?.[k]) ? (body[k] as string[]) : [];
              setRefusalDetail([
                ...pick("blockers"),
                ...pick("violations"),
                ...pick("basis"),
              ]);
            }
          } else {
            setRefusal(e instanceof Error ? e.message : String(e));
          }
        } finally {
          setBusy(false);
        }
      })();
      return bill
        ? "Re-raising the e-way bill for this consignment…"
        : "Raising the e-way bill…";
    }, [
      bill,
      busy,
      client,
      companyID,
      distance,
      fromPincode,
      mode,
      odc,
      selectedID,
      toPincode,
      transporterID,
      transporterName,
      vehicle,
    ]),
  );

  if (denied) {
    return (
      <section aria-label="e-Way Bill">
        <h2>e-Way Bill</h2>
        <p>
          You are not permitted to see this company&apos;s vouchers, so no
          consignment can be selected.
        </p>
      </section>
    );
  }

  return (
    <section aria-label="e-Way Bill">
      <h2>e-Way Bill</h2>

      {/*
        ⛔ THE STANDING NOTICE. It is on the screen BEFORE anything is raised
        and stays there afterwards, because an operator who reads it only on
        the result panel has already decided to print by then.
      */}
      <p role="note">
        ⛔ This build cannot reach the NIC e-Way Bill portal, so nothing here is
        submitted and nothing here is an EBN. What it produces is the payload
        that would be submitted, and a local reference.
      </p>

      {listError !== "" && <p role="alert">{listError}</p>}

      <h3>The consignment&apos;s document</h3>
      {entries.length === 0 ? (
        <p>
          No voucher in this period moves goods, so there is nothing to raise a
          bill against. An e-way bill covers a movement of goods.
        </p>
      ) : (
        <ul>
          {entries.map((e) => (
            <li key={e.voucher_id}>
              <label>
                <input
                  type="radio"
                  name="ewb-voucher"
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

      <h3>The movement</h3>
      <p>
        These are facts about the world that no ledger holds: a voucher records
        that goods were sold, not which lorry they left in.
      </p>
      <label>
        Approximate distance (km)
        <input
          aria-label="Approximate distance (km)"
          value={distance}
          onChange={(ev) => setDistance(ev.target.value)}
        />
      </label>
      <p>
        Required. Left blank, the server refuses rather than assuming one day —
        a bill whose expiry was guessed expires without warning.
      </p>
      <label>
        Vehicle number (Part B)
        <input
          aria-label="Vehicle number (Part B)"
          value={vehicle}
          onChange={(ev) => setVehicle(ev.target.value)}
        />
      </label>
      <p>
        May be left blank: the bill is still raised, and it says on its face
        that the goods may not move until a vehicle is added.
      </p>
      <label>
        Transport mode
        <select
          aria-label="Transport mode"
          value={mode}
          onChange={(ev) => setMode(ev.target.value)}
        >
          {MODES.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
      </label>
      <label>
        Transporter ID
        <input
          aria-label="Transporter ID"
          value={transporterID}
          onChange={(ev) => setTransporterID(ev.target.value)}
        />
      </label>
      <label>
        Transporter name
        <input
          aria-label="Transporter name"
          value={transporterName}
          onChange={(ev) => setTransporterName(ev.target.value)}
        />
      </label>
      <label>
        <input
          type="checkbox"
          aria-label="Over-dimensional cargo"
          checked={odc}
          onChange={(ev) => setOdc(ev.target.checked)}
        />
        Over-dimensional cargo
      </label>
      <p>
        Tick only if the operator has confirmed it. It is never inferred:
        nothing in a voucher says whether a load is over-dimensional, and
        assuming ordinary cargo grants ten times the validity the rule allows.
      </p>
      <label>
        From pincode
        <input
          aria-label="From pincode"
          value={fromPincode}
          onChange={(ev) => setFromPincode(ev.target.value)}
        />
      </label>
      <label>
        To pincode
        <input
          aria-label="To pincode"
          value={toPincode}
          onChange={(ev) => setToPincode(ev.target.value)}
        />
      </label>
      <p>
        Blank uses the company&apos;s and the party&apos;s registered addresses.
        Override them whenever the goods start or end somewhere else — a
        warehouse dispatch is the ordinary case, not the exception.
      </p>

      <p>Ctrl+A raises the bill.</p>

      {writeDenied && (
        <p role="alert">
          You are not permitted to raise an e-way bill on this company.
        </p>
      )}
      {refusal !== "" && (
        <div role="alert">
          <p>{refusal}</p>
          {refusalDetail.length > 0 && (
            <ul>
              {refusalDetail.map((d, i) => (
                <li key={i}>{d}</li>
              ))}
            </ul>
          )}
        </div>
      )}

      {bill && (
        <section aria-label="The raised e-way bill">
          <h3>Raised</h3>

          {/*
            ⛔ STALENESS IS FIRST, ABOVE THE IDENTIFIER. If the invoice has
            changed since the bill was raised, everything below describes goods
            the voucher no longer states — and nothing else in the system will
            ever say so.
          */}
          {bill.stale && (
            <p role="alert">
              ⛔ This invoice has been ALTERED since this bill was raised, so the
              payload below no longer describes it. Raise it again before
              anything is printed or handed to a driver.
            </p>
          )}

          <dl>
            <dt>Local reference</dt>
            <dd>{bill.local_reference}</dd>
            <dt>Generated by</dt>
            <dd>{bill.provider}</dd>
            <dt>Valid until</dt>
            <dd>
              {bill.valid_until} ({bill.validity_days} day
              {bill.validity_days === 1 ? "" : "s"})
            </dd>
            <dt>Document</dt>
            <dd>
              {bill.voucher_no} dated {bill.voucher_date}
            </dd>
          </dl>

          {/*
            ⛔ NOT "e-Way Bill Number". The label above says "Local reference"
            and this sentence says what it is not, in the same block, because a
            reader who saw only the label would reasonably assume otherwise.
          */}
          <p role="note">
            The local reference is NOT an e-Way Bill Number. An EBN is issued by
            NIC; this identifier was generated locally and was never submitted.
          </p>

          {bill.generation_count > 1 && (
            <p role="note">
              This is generation {bill.generation_count} for this document. An
              earlier bill was raised and has been replaced — its payload is not
              kept.
            </p>
          )}

          {!bill.assessed_required && (
            <p role="note">
              The consignment value did not reach the threshold, so a bill was
              not required on value alone. It has been raised anyway; the
              reasoning is below.
            </p>
          )}

          {bill.blockers.length > 0 && (
            <div role="alert">
              <p>The goods may not move on this bill yet:</p>
              <ul>
                {bill.blockers.map((b, i) => (
                  <li key={i}>{b}</li>
                ))}
              </ul>
            </div>
          )}

          {/*
            ⛔ THE DISCLOSURE BLOCK, IN FULL, NEVER COLLAPSED. See the file
            header. This is the artefact's honesty and it is not decoration.
          */}
          <h4>Disclosures</h4>
          <ul aria-label="Disclosures">
            {bill.disclosures.map((d, i) => (
              <li key={i}>{d}</li>
            ))}
          </ul>

          <h4>The payload that would be submitted</h4>
          <pre>{JSON.stringify(bill.payload, null, 2)}</pre>
        </section>
      )}
    </section>
  );
}
