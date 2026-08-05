import { useCallback, useEffect, useRef, useState } from "react";

import { useClient, useCompanyID } from "../app/CompanyContext";
import { useRegisterScreenAction } from "../app/ScreenActions";
import { ApiError } from "../api";
import type {
  AllocatableDeduction,
  CreateTDSChallanAllocation,
  Money,
  TDSChallan,
  TDSChallanAllocation,
  TDSChallanListResponse,
} from "../api";
import { formatMoney } from "./reports/money";
import { usePrintAction } from "./usePrint";

/**
 * THE TDS CHALLAN REGISTER (T6, migration 0069) — the deposits this company made
 * to the Government, and the one screen that records a new one.
 *
 * The server half shipped first: GET/POST /v1/companies/{id}/tds-challans.
 * Everything it stores was unreachable from this app, which is the parity gap
 * this screen closes — and the gap MATTERS more here than usual, because
 * Deductee Detail field 5 of the prescribed return is a challan reference, so a
 * deduction with no allocation cannot be filed at all.
 *
 * EIGHT THINGS THIS SCREEN REFUSES TO DO
 *
 *  1. IT NEVER TOUCHES A NUMBER. Every amount is a fixed-point STRING in and
 *     out — there is not one parseFloat, Number(), toFixed or arithmetic
 *     operator on money in this file, and no total is computed here. The server
 *     derives `total_deposited_rupees` and `unallocated_tax_rupees`; this screen
 *     renders what arrived. `bsr_code` and `challan_no` are TEXT for the same
 *     reason in the other direction: 0004329 is a valid branch code and 4329 is
 *     a different branch, 05122 is not 5122.
 *
 *  2. IT NEVER PRE-SELECTS A MINOR HEAD. The picker is built from the SERVED
 *     `minor_heads` and opens with nothing chosen. Defaulting to 200 would
 *     assert on this company's behalf that the deposit was made of its own
 *     motion rather than against a demand — a statement about its assessment
 *     history that no software should make for it. Migration 0069 forbids the
 *     default explicitly and the route refuses an empty head with its own
 *     sentence.
 *
 *  3. IT HARD-CODES NO VOCABULARY. The three minor heads live in the migration's
 *     CHECK and arrive with their descriptions on every GET. A TypeScript copy
 *     of a list the migration owns is the drift bug this feature has already had
 *     three times: a client copy can offer a fourth value that fails only on
 *     save.
 *
 *  4. IT NEVER COMPUTES THE TAX YEAR FROM THE DEPOSIT DATE. A challan paid on
 *     7 April 2026 is normally for March of 2025-26, so the year is ASKED FOR —
 *     for the register (the GET is refused without it) and again on the form,
 *     carried from what the operator already typed and editable.
 *
 *  5. IT RENDERS THE SERVER'S SENTENCE. Every refusal on these routes names the
 *     fact that is missing or wrong: 400 for the operator's input or an
 *     allocation that is not what was deducted, 409 for a duplicate CIN or a
 *     deduction another challan already paid (RETRYING IS THE MISTAKE there),
 *     403 when the level is not permitted. None of them is replaced with a
 *     generic message, and the 403 is not swallowed — the seeded Data Entry
 *     level is refused these routes BY DESIGN (`tds-challan` is a bare resource
 *     token and that level's default_access is 'none'), so a 403 is a NORMAL
 *     answer rendered as the decision it is, ON BOTH ROUTES: Display and Create
 *     are separate ACL actions on one token, so a level allowed to READ this
 *     register and not to add to it is a real configuration and gets the same
 *     framing after keying a whole counterfoil.
 *
 *  6. IT NEVER SAYS THE RETURN IS FINE. `broken_by_alter` is ADVISORY: the
 *     reporting layer applies the same test and REFUSES THE WHOLE QUARTER
 *     (ErrAllocationBrokenByAlter), and that refusal is the authority. So a
 *     flagged allocation is shown, an unflagged one is NOT presented as a
 *     clearance, and `deduction_on_record: false` — an ALTER removed the leg
 *     while the payment stands — is shown rather than hidden.
 *
 *  7. ⛔ IT ASKS FOR NO IDENTIFIER NOTHING DISPLAYS. An allocation is keyed on
 *     (voucher_id, section, leg_seq) — a UUID and a 0-BASED ORDINAL — and NO
 *     SCREEN IN THIS PRODUCT SHOWS EITHER: the TDS/TCS Register renders
 *     `voucher_no` and its payload has no leg field at all. The first version of
 *     this form had three free-text boxes for voucher_id, leg_seq and the
 *     amount, so a human could not complete it — typing the one thing they could
 *     see ("P1") earned "allocation 1: invalid voucher_id" — and it passed every
 *     test, because the tests keyed mock fixture ids the real server rejects as
 *     non-UUIDs. The fix was not a better message: the identifiers are now
 *     PICKED from the served `allocatable_deductions` and travel back untouched.
 *     THE AMOUNT IS NOT AN INPUT EITHER — RecordTDSChallan refuses an allocation
 *     that is not EXACTLY the deduction's tax, so there is nothing for a human to
 *     decide, and the served figure is sent verbatim.
 *
 *  8. IT SENDS NO YEAR IT ALREADY KNOWS IS NOT ONE. This is the ONLY refusal
 *     this screen makes instead of the server, and it is made because THE GET
 *     WILL NOT MAKE IT: `handleListTDSChallans` refuses an empty or non-numeric
 *     param and nothing else — `strconv.Atoi("0")` succeeds and ListTDSChallans
 *     has no range check at all (the 2025–2200 rule is on the WRITE path). So
 *     sending 0 for an unparseable box answers 200 with zero rows, and this
 *     screen would then print "No deposit has been recorded against 0-01": a
 *     confident, correctly formatted answer to a question nobody asked, which is
 *     the exact failure this file refuses everywhere else.
 *
 * ⚠️ A 201 WITH A NULL `challan` IS NOT A FAILED WRITE. The row is committed
 * before the read-back runs, so a failed read arrives as `read_back_error` with
 * the status still 201. Reporting that as a failure would invite the operator to
 * key the deposit a second time, and the CIN conflict that follows would read
 * like a bug in this screen rather than the protection it is.
 */

/**
 * The natural key of a deduction: (voucher, section, leg). Used ONLY to remember
 * which served rows the operator ticked — never parsed, never shown.
 */
function allocationKey(d: AllocatableDeduction): string {
  return `${d.voucher_id}|${d.section}|${d.leg_seq}`;
}

/**
 * THE SCREEN'S OWN REFUSAL of a tax year — see (8) in the header for why this one
 * cannot be left to the server.
 *
 * It names the reason, and it names WHOSE rule it is: 2025–2200 is migration
 * 0069's CHECK on the challan row, so no deposit can exist outside it and a
 * register for 1999 could only ever be empty. The GET itself would answer 200 and
 * an empty list, which is why refusing here is not duplicated validation.
 */
function yearRefusalFor(text: string): string {
  if (!/^[0-9]+$/.test(text)) {
    return (
      `"${text}" is not a tax year, so nothing was asked of the server. Type the ` +
      "calendar year the tax year BEGINS in — 2026 for 2026-27. (This refusal is " +
      "this screen's own: the register route accepts any number, so a year of 0 " +
      "would come back as an empty register for “0-01” and read like an answer.)"
    );
  }
  const year = Number.parseInt(text, 10);
  if (year < 2025 || year > 2200) {
    return (
      `${year} is outside 2025–2200, which is the range a challan can be recorded ` +
      "against (migration 0069's CHECK), so no deposit could exist against it and " +
      "the register could only come back empty. Type the calendar year the tax year " +
      "BEGINS in — 2026 for 2026-27."
    );
  }
  return "";
}

export function TDSChallansScreen() {
  const client = useClient();
  const companyID = useCompanyID();

  // ---- THE TAX YEAR, ASKED FOR. The GET is refused without it and nothing here
  // defaults it: a correctly rendered list of the wrong year's deposits reads
  // exactly like the answer that was asked for.
  const [yearText, setYearText] = useState("");
  /** This screen's own refusal of the typed year. See {@link yearRefusalFor}. */
  const [yearRefusal, setYearRefusal] = useState("");

  const [data, setData] = useState<TDSChallanListResponse | null>(null);
  const [listError, setListError] = useState("");
  const [denied, setDenied] = useState(false);
  const [busy, setBusy] = useState(false);

  // ---- The deposit form (Alt+C). Closed until asked for, because this screen is
  // read far more often than it is written to.
  const [recording, setRecording] = useState(false);
  const [depositDate, setDepositDate] = useState("");
  const [bsrCode, setBsrCode] = useState("");
  const [challanNo, setChallanNo] = useState("");
  const [byBookEntry, setByBookEntry] = useState(false);
  const [tax, setTax] = useState("");
  const [interest, setInterest] = useState("");
  const [fee, setFee] = useState("");
  const [penalty, setPenalty] = useState("");
  /** NO DEFAULT, and never pre-selected. "" means "the operator has not chosen". */
  const [minorHead, setMinorHead] = useState("");
  const [formYear, setFormYear] = useState("");
  const [recordedBy, setRecordedBy] = useState("");
  /**
   * WHICH SERVED DEDUCTIONS ARE TICKED, by natural key. Not amounts, not
   * identifiers an operator typed: the row itself supplies all three fields on the
   * wire — see (7) in the header. Several may be ticked, and that is the NORMAL
   * case: one deposit usually covers a month of deductions.
   */
  const [chosen, setChosen] = useState<string[]>([]);
  /** THE SERVER'S SENTENCE, verbatim. Never replaced, never summarised. */
  const [refusal, setRefusal] = useState("");
  /** A 403 on the WRITE. The sentence above is still rendered; this adds the framing. */
  const [createDenied, setCreateDenied] = useState(false);
  /** A 201 that could not be read back: a NOTICE, never an error. */
  const [notice, setNotice] = useState("");
  /**
   * The form was open and the register went away under it. Said out loud, because
   * a form that vanishes with a half-keyed counterfoil in it and no sentence is
   * the same class of defect as a status line that claims a key worked.
   */
  const [formLostWithRegister, setFormLostWithRegister] = useState(false);

  /**
   * ⚠️ THE ONE PLACE `data` BECOMES NULL, and it exists so that two states cannot
   * disagree.
   *
   * The form renders on `recording && data !== null` and Alt+C used to gate on a
   * SEPARATE `loadedYear`, which a failed load did not clear. So: load 2026 fine,
   * retype 2027, meet a transient 500 — and Alt+C answered the SUCCESS line
   * "Record a TDS deposit (challan)…" while no form appeared. That is the Alt+O
   * signature this whole slice is written against: the status line claims the key
   * worked and nothing happened. `loadedYear` is gone; `data` is the single fact,
   * and the Alt+C gate is the form's render condition verbatim.
   */
  const recordingRef = useRef(false);
  useEffect(() => {
    recordingRef.current = recording;
  }, [recording]);
  const dropRegister = useCallback(() => {
    setData(null);
    setFormLostWithRegister(recordingRef.current);
    setRecording(false);
  }, []);

  const load = useCallback(
    async (year: number) => {
      setBusy(true);
      try {
        const res = await client.listTDSChallans(companyID, year);
        setData(res);
        setListError("");
        setDenied(false);
        setFormLostWithRegister(false);
      } catch (e) {
        dropRegister();
        // A 403 is a DECISION here, not a fault — see (5) in the header.
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
    },
    [client, companyID, dropRegister],
  );

  /** Ask for the register. The year is the operator's statement, so it is theirs to make. */
  const askForYear = useCallback(() => {
    const text = yearText.trim();
    if (text === "") {
      setYearRefusal("");
      setListError("");
      dropRegister();
      return;
    }
    // NOT money: a calendar year is an ordinal, and it is sent as the integer the
    // route takes — but ONLY once this screen knows it is a year. See (8).
    const why = yearRefusalFor(text);
    if (why !== "") {
      setYearRefusal(why);
      setListError("");
      dropRegister();
      return;
    }
    setYearRefusal("");
    setFormYear((prev) => (prev === "" ? text : prev));
    void load(Number.parseInt(text, 10));
  }, [dropRegister, load, yearText]);

  // Ctrl+P. There is no printed layout for a challan register, so the target
  // resolver answers with a SENTENCE and makes no request. Registering the
  // handler is what turns "Ctrl+P — Print does not apply on this screen" into
  // that sentence; an unregistered key would read as a key that quietly failed.
  usePrintAction({ voucher: { voucherID: null } });

  // ---- Alt+C — RECORD A DEPOSIT.
  //
  // The register must be loaded first, and that is not friction: the form's tax
  // year is carried from the year the operator asked for, the minor heads and the
  // allocatable deductions BOTH arrive in that same response, and a form opened
  // over no register would have to ask for the same year twice.
  useRegisterScreenAction(
    "recordTDSChallan",
    useCallback((): string => {
      if (denied) {
        return "You are not permitted to record a deposit on this company.";
      }
      // ⚠️ THE FORM'S RENDER CONDITION, VERBATIM. Anything else here can claim
      // the key worked while nothing appears — see {@link dropRegister}.
      if (data === null) {
        return (
          "There is no register on screen to record against. Ask for the tax year " +
          "first — a challan belongs to one tax year, and it is recorded rather " +
          "than computed from the deposit date. If a refusal is showing above, " +
          "that is why the register is not up."
        );
      }
      if (recording) {
        return "The deposit form is already open. Ctrl+A records it.";
      }
      setRecording(true);
      setRefusal("");
      setCreateDenied(false);
      setNotice("");
      setChosen([]);
      return "Record a TDS deposit (challan). Ctrl+A accepts it.";
    }, [data, denied, recording]),
  );

  // ---- Ctrl+A — ACCEPT. Async, so the shell awaits it and the REFUSAL reaches
  // the status line as well as the screen.
  //
  // The handler is registered through a ref-backed trampoline (see
  // useRegisterScreenAction), so this closure may read the live form state.
  const submit = useCallback(async (): Promise<string> => {
    if (!recording) {
      return "Nothing to accept here. Alt+C records a deposit.";
    }
    // The same single fact the form renders on and Alt+C gates on. Unreachable
    // while the form is up (see {@link dropRegister}), and stated rather than
    // asserted away: a non-null assertion here would be the place a later change
    // reintroduces a form with no register behind it.
    if (data === null) {
      return "The register is no longer on screen, so there is nothing to record against. Nothing was sent.";
    }
    setRefusal("");
    setCreateDenied(false);
    setNotice("");
    setBusy(true);
    try {
      // ---- THE ALLOCATIONS, TAKEN FROM THE SERVED ROWS AND NOT FROM A KEYBOARD.
      // All three fields travel exactly as they arrived: the UUID and the 0-based
      // leg because no screen displays them, and the amount because the write path
      // refuses anything that is not equal to the deduction's tax. Nothing here
      // trims, parses or rounds any of it.
      //
      // An already-paid row cannot be ticked, and is filtered again here so a
      // stale tick from a re-read cannot be sent — that would be a 409.
      const wire: CreateTDSChallanAllocation[] = data.allocatable_deductions
        .filter((d) => !d.already_paid && chosen.includes(allocationKey(d)))
        .map((d) => ({
          voucher_id: d.voucher_id,
          section: d.section,
          leg_seq: d.leg_seq,
          amount_rupees: d.tax_rupees,
        }));
      // NOTHING IS REFUSED HERE FOR AN EMPTY SELECTION. A challan with tax on it
      // and no allocation is refused by the route with a sentence that explains
      // why it cannot be filed; a challan with NO tax and no allocation is valid
      // (interest, fee or penalty on its own). Only the server knows which.
      const res = await client.createTDSChallan(companyID, {
        bsr_code: bsrCode.trim(),
        deposit_date: depositDate.trim(),
        challan_no: challanNo.trim(),
        by_book_entry: byBookEntry,
        tax_rupees: tax.trim() as Money,
        interest_rupees: interest.trim() as Money,
        fee_rupees: fee.trim() as Money,
        penalty_rupees: penalty.trim() as Money,
        minor_head: minorHead,
        tax_year_start: Number.parseInt(formYear.trim(), 10),
        allocations: wire,
        recorded_by: recordedBy.trim(),
      });

      // ---- THE WRITE SUCCEEDED. Close the form and re-read the register.
      setRecording(false);
      setBsrCode("");
      setChallanNo("");
      setDepositDate("");
      setByBookEntry(false);
      setTax("");
      setInterest("");
      setFee("");
      setPenalty("");
      setMinorHead("");
      setRecordedBy("");
      setChosen([]);
      // Re-read the year the challan was RECORDED AGAINST — which may not be the
      // year the register was showing (a deposit made in April usually belongs to
      // the year that just ended), so the operator sees what they actually
      // stored rather than a register that appears not to have changed. The year
      // came back from the server, so it needs no local check.
      setYearText(String(res.tax_year_start));
      void load(res.tax_year_start);

      if (res.challan === null) {
        // ⚠️ NOT A FAILURE. The row is committed; only the read-back failed.
        const why = res.read_back_error ?? "";
        const message =
          `Challan recorded (${res.tax_year_label}). It could not be read back` +
          (why === "" ? "" : `: ${why}`) +
          ". The deposit IS stored — do not enter it again, or the CIN will be refused as a duplicate.";
        setNotice(message);
        return message;
      }
      const ch = res.challan;
      const message =
        `Recorded challan ${ch.challan_no} of ${ch.deposit_date} at BSR branch ${ch.bsr_code} — ` +
        `${ch.total_deposited_rupees} deposited, ${ch.allocation_count} deduction(s) paid, ` +
        `${ch.unallocated_tax_rupees} of tax unallocated.`;
      setNotice(message);
      return message;
    } catch (e) {
      // THE SERVER'S OWN SENTENCE, on the screen and in the status line. It names
      // the fact that is missing or wrong; nothing here can say it better.
      const message = e instanceof Error ? e.message : String(e);
      setRefusal(message);
      // A 403 on the WRITE is a DECISION and is framed as one. Display and Create
      // are separate ACL actions on ONE token, so "allow display tds-challan"
      // without create is a real configuration — and an operator who has just
      // keyed a whole counterfoil is owed more than a bare ACL sentence.
      setCreateDenied(e instanceof ApiError && e.status === 403);
      return message;
    } finally {
      setBusy(false);
    }
  }, [
    bsrCode,
    byBookEntry,
    challanNo,
    chosen,
    client,
    companyID,
    data,
    depositDate,
    fee,
    formYear,
    interest,
    load,
    minorHead,
    penalty,
    recordedBy,
    recording,
    tax,
  ]);

  useRegisterScreenAction("accept", submit);

  // Autofocus the first field when the form opens, so Alt+C leaves the operator
  // keying rather than reaching for a mouse.
  const firstFieldRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (recording) firstFieldRef.current?.focus();
  }, [recording]);

  if (denied) {
    return (
      <section
        data-testid="tds-challans-denied"
        className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field"
      >
        <h1 className="text-lg font-bold text-tally-title">TDS Challans</h1>
        <p className="mt-2 max-w-3xl text-sm">
          You do not have permission to read or record this company&apos;s TDS
          deposits. The register names every deduction that has been paid over,
          the bank branch it was paid at and the person who recorded it, so it is
          refused to the Data Entry level by default — that is a decision, not a
          fault. An owner can grant it by adding an{" "}
          <span className="text-tally-title">allow tds-challan</span> rule in
          Users &amp; Roles.
        </p>
      </section>
    );
  }

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">TDS Challans</h1>
      <p className="mt-1 max-w-3xl text-xs">
        What was paid to the Government, when, through which bank branch — and
        which deductions it paid. A deduction with no challan behind it cannot be
        filed: Deductee Detail field 5 of the return is a challan reference.
      </p>

      {/* ---- THE TAX YEAR. Asked for, never derived from a date. ------------ */}
      <div className="mt-3 flex flex-wrap items-end gap-2 text-sm">
        <label className="flex flex-col">
          <span className="text-xs text-tally-title">
            Tax year begins in (2026 = 2026-27)
          </span>
          <input
            aria-label="Tax year begins in"
            data-testid="tds-challans-year"
            inputMode="numeric"
            className="w-32 bg-tally-field px-1 text-tally-fieldText outline-none"
            value={yearText}
            onChange={(e) => setYearText(e.target.value)}
            onBlur={askForYear}
            onKeyDown={(e) => {
              if (e.key === "Enter") askForYear();
            }}
          />
        </label>
        <button
          type="button"
          className="border border-tally-fieldText px-2 py-0.5 text-xs"
          onClick={askForYear}
        >
          Show the register
        </button>
        {busy ? <span className="text-xs">working…</span> : null}
      </div>

      {yearRefusal !== "" ? (
        /* THIS SCREEN'S OWN REFUSAL, and the only one it makes. Nothing was sent:
           the route would have answered 200 with an empty register. See (8). */
        <p
          data-testid="tds-challans-year-refusal"
          className="mt-3 max-w-3xl border border-tally-bar p-2 text-sm text-tally-bar"
        >
          {yearRefusal}
        </p>
      ) : null}

      {data === null && yearRefusal === "" && listError === "" ? (
        <p
          data-testid="tds-challans-year-prompt"
          className="mt-3 max-w-3xl text-sm"
        >
          Enter the tax year to see its deposits. It is not defaulted from
          today&apos;s date: a challan paid on 7 April 2026 is normally for March
          of 2025-26, which is why the year is recorded on the challan rather
          than computed from it.
        </p>
      ) : null}

      {listError !== "" ? (
        <p
          data-testid="tds-challans-error"
          className="mt-3 max-w-3xl text-sm text-tally-bar"
        >
          {listError}
        </p>
      ) : null}

      {formLostWithRegister ? (
        /* The form was open when the register went away. Said out loud rather
           than left as a form that silently vanished. */
        <p
          data-testid="tds-challan-form-lost"
          className="mt-2 max-w-3xl text-sm text-tally-bar"
        >
          The deposit form was open and has been closed: it is built from the
          register&apos;s response — the minor heads and the deductions that can
          be paid both arrive in it — so it cannot stand without one. Nothing was
          sent. Ask for the year again and re-key the counterfoil.
        </p>
      ) : null}

      {notice !== "" ? (
        <p
          data-testid="tds-challans-notice"
          className="mt-3 max-w-3xl border border-tally-fieldText p-2 text-sm"
        >
          {notice}
        </p>
      ) : null}

      {/* ---- THE PICKER IS UNAVAILABLE, but the register is not. ------------ */}
      {data !== null && data.allocatable_error !== undefined ? (
        <p
          data-testid="tds-allocatable-error"
          className="mt-3 max-w-3xl border border-tally-bar p-2 text-sm text-tally-bar"
        >
          The list of deductions a deposit can pay for could not be read, so no
          allocation can be chosen and a challan carrying tax cannot be recorded
          right now. The deposits below are unaffected — this failed on its own
          and the register was still served. The server said:{" "}
          <span className="text-tally-title">{data.allocatable_error}</span>
        </p>
      ) : null}

      {/* ---- THE FORM (Alt+C). --------------------------------------------- */}
      {recording && data !== null ? (
        <form
          data-testid="tds-challan-form"
          className="mt-4 max-w-4xl border border-tally-fieldText p-3 text-sm"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <h2 className="font-bold text-tally-title">Record a deposit</h2>
          <p className="mt-1 text-xs">
            Copy the counterfoil. Ctrl+A accepts it; Esc leaves this screen and
            abandons what you have keyed.
          </p>

          <div className="mt-2 flex flex-wrap gap-3">
            <Field
              label="Deposit date (yyyy-mm-dd) — the date the tax was PAID"
              ariaLabel="Deposit date"
              value={depositDate}
              onChange={setDepositDate}
              inputRef={firstFieldRef}
              width="w-40"
            />
            <Field
              label="BSR code — 7 digits, leading zeros included"
              ariaLabel="BSR code"
              value={bsrCode}
              onChange={setBsrCode}
              width="w-32"
            />
            <Field
              label="Challan serial — 1 to 5 digits (05122 is not 5122)"
              ariaLabel="Challan serial number"
              value={challanNo}
              onChange={setChallanNo}
              width="w-28"
            />
            <label className="flex flex-col text-xs">
              <span className="text-tally-title">Book entry?</span>
              <input
                type="checkbox"
                aria-label="Deposited by book entry"
                className="mt-1 h-4 w-4"
                checked={byBookEntry}
                onChange={(e) => setByBookEntry(e.target.checked)}
              />
            </label>
          </div>

          <p className="mt-3 text-xs text-tally-title">
            The four boxes on the challan — WHOLE RUPEES, as the format requires.
            Blank means zero.
          </p>
          <div className="mt-1 flex flex-wrap gap-3">
            <Field
              label="Tax"
              ariaLabel="Tax"
              value={tax}
              onChange={setTax}
              width="w-28"
            />
            <Field
              label="Interest"
              ariaLabel="Interest"
              value={interest}
              onChange={setInterest}
              width="w-28"
            />
            <Field
              label="Fee"
              ariaLabel="Fee"
              value={fee}
              onChange={setFee}
              width="w-28"
            />
            <Field
              label="Penalty"
              ariaLabel="Penalty"
              value={penalty}
              onChange={setPenalty}
              width="w-28"
            />
          </div>

          {/* THE MINOR HEAD — served, and NOT pre-selected. */}
          <fieldset className="mt-3">
            <legend className="text-xs text-tally-title">
              Minor head — choose one. There is no default.
            </legend>
            {data.minor_heads.map((h) => (
              <label
                key={h.code}
                className="mt-1 flex items-start gap-2 text-xs"
              >
                <input
                  type="radio"
                  name="minor_head"
                  aria-label={`Minor head ${h.code}`}
                  value={h.code}
                  checked={minorHead === h.code}
                  onChange={() => setMinorHead(h.code)}
                />
                <span>
                  <span className="text-tally-title">{h.code}</span> —{" "}
                  {h.description}
                </span>
              </label>
            ))}
          </fieldset>

          <div className="mt-3 flex flex-wrap gap-3">
            <Field
              label="Tax year this deposit belongs to"
              ariaLabel="Tax year this deposit belongs to"
              value={formYear}
              onChange={setFormYear}
              width="w-32"
            />
            <Field
              label="Recorded by"
              ariaLabel="Recorded by"
              value={recordedBy}
              onChange={setRecordedBy}
              width="w-40"
            />
          </div>
          <p className="mt-1 max-w-3xl text-xs">
            The year is carried from the register you asked for and stays
            editable: a deposit made in April usually belongs to the year that
            ended days earlier, and only you know which.
          </p>

          {/* ---- WHAT THIS DEPOSIT PAID — CHOSEN, never keyed. See (7). ---- */}
          <h3 className="mt-4 text-xs font-bold text-tally-title">
            What this deposit paid
          </h3>
          <AllocationPicker
            rows={data.allocatable_deductions}
            unavailable={data.allocatable_error !== undefined}
            taxYearLabel={data.tax_year_label}
            chosen={chosen}
            onToggle={(key) =>
              setChosen((keys) =>
                keys.includes(key)
                  ? keys.filter((k) => k !== key)
                  : [...keys, key],
              )
            }
          />

          <div className="mt-3 flex items-center gap-3">
            <button
              type="submit"
              className="border border-tally-fieldText px-2 py-0.5 text-xs"
            >
              Ctrl+A — Accept
            </button>
            <button
              type="button"
              className="border border-tally-fieldText px-2 py-0.5 text-xs"
              onClick={() => {
                setRecording(false);
                setRefusal("");
                setCreateDenied(false);
              }}
            >
              Close the form
            </button>
          </div>

          {refusal !== "" ? (
            /* THE SERVER'S SENTENCE, verbatim. A duplicate CIN and an
               already-paid deduction are both 409s that must NOT be retried. */
            <p
              data-testid="tds-challan-refusal"
              className="mt-3 max-w-3xl border border-tally-bar p-2 text-sm text-tally-bar"
            >
              {refusal}
            </p>
          ) : null}

          {createDenied ? (
            /* A 403 ON THE WRITE, framed as the DECISION it is — the same framing
               the read gets. Display and Create are separate ACL actions on one
               `tds-challan` token, so a level allowed to READ this register and
               not to add to it is a real configuration, not a broken one. */
            <p
              data-testid="tds-challan-create-denied"
              className="mt-2 max-w-3xl border border-tally-fieldText p-2 text-sm"
            >
              That is a decision, not a fault. Displaying this register and
              creating a deposit on it are SEPARATE actions on one{" "}
              <span className="text-tally-title">tds-challan</span> token, so a
              level that may read what has been deposited and may not add to it
              is a real configuration — and you are in it. Nothing you keyed has
              been lost or sent. An owner can grant it by adding an{" "}
              <span className="text-tally-title">allow create tds-challan</span>{" "}
              rule in Users &amp; Roles.
            </p>
          ) : null}
        </form>
      ) : null}

      {/* ---- THE REGISTER. ------------------------------------------------- */}
      {data !== null ? (
        <>
          <h2 className="mt-5 font-bold text-tally-title">
            Deposits recorded for {data.tax_year_label}
          </h2>
          {!recording ? (
            <p className="mt-1 text-xs">Alt+C records a deposit.</p>
          ) : null}
          {data.challans.length === 0 ? (
            <p data-testid="tds-challans-empty" className="mt-1 text-sm">
              No deposit has been recorded against {data.tax_year_label}. That is
              a statement about this register only — it does not mean no tax was
              deducted, and it does not mean none was paid.
            </p>
          ) : (
            <ul className="mt-2 max-w-5xl list-none">
              {data.challans.map((ch) => (
                <ChallanCard key={ch.id} challan={ch} />
              ))}
            </ul>
          )}
          <p className="mt-4 max-w-3xl text-xs">
            This register does not tell you a return can be filed. Nothing here
            checks the Form 140 quarter rules, and an allocation flagged below is
            refused by the reporting layer for the WHOLE quarter — the TDS / TCS
            Register&apos;s filing status is the answer to &quot;can this be
            filed&quot;.
          </p>
        </>
      ) : null}
    </section>
  );
}

/**
 * THE ALLOCATION PICKER — the deductions this deposit can pay, CHOSEN.
 *
 * ⛔ THERE IS NOTHING TO TYPE HERE, and that is the point (see (7) in the header).
 * The voucher UUID and the 0-based leg are never shown; the amount is not offered
 * because it can only be one figure. The operator recognises a deduction by
 * voucher number, party, section, date and tax, ticks it, and the served row
 * supplies the wire.
 */
function AllocationPicker({
  rows,
  unavailable,
  taxYearLabel,
  chosen,
  onToggle,
}: {
  rows: AllocatableDeduction[];
  unavailable: boolean;
  taxYearLabel: string;
  chosen: string[];
  onToggle: (key: string) => void;
}) {
  if (unavailable) {
    return (
      <p data-testid="tds-allocatable-unavailable" className="mt-1 text-xs">
        The deductions this deposit could pay could not be read — the reason is
        above the form. A challan that deposits TAX must name at least one
        deduction, so it cannot be recorded until that read succeeds; a challan of
        interest, fee or penalty only names none and can still be accepted.
      </p>
    );
  }
  if (rows.length === 0) {
    return (
      /* ⛔ NEVER AN EMPTY PICKER WITH NO EXPLANATION. An empty list is a
         STATEMENT, and which statement matters: no deduction of this year
         carries tax, so there is nothing a challan could pay for. */
      <p data-testid="tds-allocatable-empty" className="mt-1 max-w-3xl text-xs">
        No deduction of {taxYearLabel} carries any tax, so there is nothing a
        deposit could pay for. Deductions that deducted NOTHING — a payment below
        the s.393(1) threshold — are not listed, because nothing can be deposited
        against them: they are reported under the return&apos;s nil challan, which
        is derived and never stored. If you are holding a counterfoil for this
        year, then the deduction behind it is not in these books yet — key the
        voucher first, and the deduction it derives will appear here. You can
        still record a deposit of interest, fee or penalty ONLY, which pays no
        deduction.
      </p>
    );
  }
  return (
    <>
      <p className="mt-1 max-w-3xl text-xs">
        Tick every deduction this one deposit paid — one deposit usually covers a
        month of them. THERE IS NOTHING TO TYPE: the amount allocated to a
        deduction must equal the tax that was deducted on it EXACTLY (the server
        refuses any inequality, because that stored amount is what makes a later
        voucher ALTER detectable), so it is taken from the row rather than asked
        for.
      </p>
      <table className="mt-2 w-full text-xs">
        <thead>
          <tr className="text-left text-tally-title">
            <th className="pr-2 font-normal">Pay?</th>
            <th className="pr-2 font-normal">Voucher</th>
            <th className="pr-2 font-normal">Deducted on</th>
            <th className="pr-2 font-normal">Deductee</th>
            <th className="pr-2 font-normal">Section</th>
            <th className="pr-2 text-right font-normal">Tax deducted</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((d) => {
            const key = allocationKey(d);
            const id = `${d.voucher_no}:${d.section}:${d.leg_seq}`;
            return (
              <tr
                key={key}
                data-testid={`tds-allocatable:${id}`}
                className={d.already_paid ? "text-tally-bar" : undefined}
              >
                <td className="pr-2">
                  {d.already_paid ? (
                    /* SHOWN AND NOT SELECTABLE. It is served rather than
                       filtered out so an operator who cannot find a deduction
                       learns WHICH reason applies — not deducted in this year, or
                       already deposited. Ticking it could only earn a 409. */
                    <span data-testid={`tds-allocatable-paid:${id}`}>
                      already deposited
                    </span>
                  ) : (
                    /* THE ACCESSIBLE NAME CARRIES ALL FIVE FACTS an operator
                       recognises a deduction by, and NONE of the two they
                       cannot: the voucher UUID and the leg ordinal are absent
                       here as they are absent from the row. Two legs of one
                       voucher, under one section, to one party, on one date, for
                       one amount would still read alike — that ambiguity is in
                       the books, and inventing a leg number to break it would be
                       showing an identifier no other screen shows. */
                    <input
                      type="checkbox"
                      aria-label={`Pay the ${d.tax_rupees} deducted on voucher ${d.voucher_no} under ${d.section} from ${d.deductee_name} on ${d.deduction_date}`}
                      checked={chosen.includes(key)}
                      onChange={() => onToggle(key)}
                    />
                  )}
                </td>
                <td className="pr-2">{d.voucher_no}</td>
                <td className="pr-2">{d.deduction_date}</td>
                <td className="pr-2">{d.deductee_name}</td>
                <td className="pr-2">{d.section}</td>
                {/* RAW, NOT formatMoney: this is the 4-dp figure that must be
                    allocated EXACTLY, and it is what will be sent. ₹33,333 at 10%
                    is 3333.3000, and showing "3,333.30" here would show two
                    decimals of a four-decimal fact the server compares for
                    equality. */}
                <td className="pr-2 text-right">{d.tax_rupees}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <p data-testid="tds-allocation-chosen" className="mt-2 text-xs">
        {chosen.length === 0
          ? "Nothing ticked. A deposit of TAX must name at least one deduction — the server refuses one that names none, because Deductee Detail field 5 of the return is a challan reference."
          : `${chosen.length} deduction(s) ticked. Their amounts are sent exactly as shown above; the challan's own tax box is what you copied from the counterfoil, and any difference is reported as unallocated rather than refused.`}
      </p>
    </>
  );
}

/** One challan, with its CIN parts, its four boxes and what it paid. */
function ChallanCard({ challan: ch }: { challan: TDSChallan }) {
  return (
    <li
      data-testid={`tds-challan:${ch.challan_no}`}
      className="mt-3 border border-tally-fieldText p-2 text-sm"
    >
      {/* THE CIN — (branch, date, serial) — which is how the department
          identifies a payment. All three are TEXT. */}
      <p>
        <span className="text-tally-title">BSR</span> {ch.bsr_code} ·{" "}
        <span className="text-tally-title">paid</span> {ch.deposit_date} ·{" "}
        <span className="text-tally-title">serial</span> {ch.challan_no}
        {ch.by_book_entry ? " · by book entry (Form 24G)" : ""}
      </p>
      <p className="mt-1">
        <span className="text-tally-title">Deposited</span>{" "}
        <span data-testid={`tds-challan-total:${ch.challan_no}`}>
          {formatMoney(ch.total_deposited_rupees)}
        </span>{" "}
        = tax {formatMoney(ch.tax_rupees)} + interest{" "}
        {formatMoney(ch.interest_rupees)} + fee {formatMoney(ch.fee_rupees)} +
        penalty {formatMoney(ch.penalty_rupees)}
      </p>
      {/* The minor head WITH THE SERVED DESCRIPTION: three bare numbers would
          invite whichever is listed first. */}
      <p className="mt-1">
        <span className="text-tally-title">Minor head</span> {ch.minor_head} —{" "}
        {ch.minor_head_description} ·{" "}
        <span className="text-tally-title">tax year</span> {ch.tax_year_label}
      </p>
      <p className="mt-1 text-xs">
        Recorded by {ch.recorded_by} on {ch.recorded_at.slice(0, 10)}
      </p>

      {ch.allocations.length === 0 ? (
        <p className="mt-2 text-xs">
          This challan pays no deduction — only interest, fee or penalty. Only
          the tax component discharges a deduction liability, so there is nothing
          to allocate.
        </p>
      ) : (
        <table className="mt-2 w-full text-xs">
          <thead>
            <tr className="text-left text-tally-title">
              <th className="pr-2 font-normal">Voucher</th>
              <th className="pr-2 font-normal">Section</th>
              <th className="pr-2 font-normal">Leg</th>
              <th className="pr-2 font-normal">Deductee</th>
              <th className="pr-2 text-right font-normal">Allocated</th>
              <th className="pr-2 text-right font-normal">Deducted</th>
            </tr>
          </thead>
          <tbody>
            {ch.allocations.map((a) => (
              <AllocationRow
                key={`${a.voucher_id}:${a.section}:${a.leg_seq}`}
                allocation={a}
              />
            ))}
          </tbody>
        </table>
      )}

      {/* THE PAISE REMAINDER. A deduction's tax is a 4-dp figure and a deposit is
          whole rupees, so a few paise either way is the ORDINARY case — and it
          can be negative. Reported, never refused on, and never dressed up as an
          error.

          WHICH FIGURES ARE FORMATTED AND WHICH ARE RAW, because this card used to
          print ONE figure BOTH WAYS — `tax_rupees` as "20,000.00" above and
          "20000.0000" here, four lines apart, which reads as two different
          numbers. The challan's four boxes are WHOLE RUPEES by the format's rule,
          so they are formatted, here as well as above. `allocated_total_rupees`
          and `unallocated_tax_rupees` are NOT: they are 4-dp sums whose paise are
          the entire subject of this sentence, and a two-decimal rendering of the
          thing being reported as a remainder would hide it. */}
      <p className="mt-2 text-xs">
        <span className="text-tally-title">Tax on this challan</span>{" "}
        {formatMoney(ch.tax_rupees)} ·{" "}
        <span className="text-tally-title">allocated</span>{" "}
        {ch.allocated_total_rupees} across {ch.allocation_count} deduction(s) ·{" "}
        <span className="text-tally-title">unallocated</span>{" "}
        <span data-testid={`tds-challan-unallocated:${ch.challan_no}`}>
          {ch.unallocated_tax_rupees}
        </span>
      </p>
      <p className="mt-1 text-xs">
        A non-zero unallocated figure is not an error: a challan is whole rupees
        and a deduction is not, so paying ₹3,333.30 of tax with a ₹3,334 deposit
        leaves ₹0.70 here. It can also be negative.
      </p>
    </li>
  );
}

function AllocationRow({
  allocation: a,
}: {
  allocation: TDSChallanAllocation;
}) {
  const flagged = a.broken_by_alter || !a.deduction_on_record;
  return (
    <>
      <tr data-testid={`tds-allocation:${a.voucher_no}:${a.section}:${a.leg_seq}`}>
        <td className="pr-2">{a.voucher_no}</td>
        <td className="pr-2">{a.section}</td>
        <td className="pr-2">{a.leg_seq}</td>
        <td className="pr-2">
          {a.deduction_on_record ? (
            a.deductee_name
          ) : (
            /* Empty by design when the deduction is gone: there is nobody left
               to name. Never rendered as a blank cell, which would read as an
               unnamed deductee. */
            <span className="text-tally-bar">deduction no longer on record</span>
          )}
        </td>
        {/* RAW, and deliberately: these are the 4-dp figures whose EQUALITY is
            what makes a later ALTER detectable. Two decimals would make an
            inequality of paise invisible on the one screen that shows it. */}
        <td className="pr-2 text-right">{a.allocated_rupees}</td>
        <td className="pr-2 text-right">{a.deduction_tax_rupees}</td>
      </tr>
      {flagged ? (
        <tr
          data-testid={`tds-allocation-flag:${a.voucher_no}:${a.section}:${a.leg_seq}`}
        >
          <td colSpan={6} className="pb-1 pr-2 text-tally-bar">
            {!a.deduction_on_record ? (
              <>
                A voucher ALTER removed this deduction, and the deposit stands.
                The allocation is kept — it records a real payment — and it is
                shown rather than dropped.{" "}
              </>
            ) : null}
            {a.broken_by_alter ? (
              <>
                ADVISORY: this allocation no longer matches the deduction it
                paid. Reporting applies the same test and REFUSES THE WHOLE
                QUARTER, and that refusal is the authority — this flag is only
                the early warning. The absence of a flag is not a clearance.
              </>
            ) : null}
          </td>
        </tr>
      ) : null}
    </>
  );
}

function Field({
  label,
  ariaLabel,
  value,
  onChange,
  width,
  inputRef,
}: {
  label: string;
  ariaLabel: string;
  value: string;
  onChange: (v: string) => void;
  width: string;
  inputRef?: React.RefObject<HTMLInputElement>;
}) {
  return (
    <label className="flex flex-col text-xs">
      <span className="text-tally-title">{label}</span>
      <input
        ref={inputRef}
        aria-label={ariaLabel}
        className={`${width} mt-1 bg-tally-field px-1 text-tally-fieldText outline-none`}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  );
}
