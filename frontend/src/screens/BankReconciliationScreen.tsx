import { useCallback, useEffect, useMemo, useState } from "react";
import { useClient, useCompanyID } from "../app/CompanyContext";
import { screenHandled, useRegisterScreenAction } from "../app/ScreenActions";
import { LEDGER_ROW_PREFIX } from "../keystroke/navMachine";
import type { NavSnapshot } from "../keystroke/navMachine";
import type { ScreenComponentProps } from "../app/screenRegistry";
import { ApiError } from "../api";
import type {
  BankReconciliationResponse,
  BankReconLine,
  LedgerListItem,
} from "../api";
import { formatMoney } from "./reports/money";
import { SelectionList } from "./SelectionList";
import { BankStatementImportPanel } from "./BankStatementImportPanel";
import { BankMatchPanel, panelOps } from "./BankMatchPanel";
import { usePrintAction } from "./usePrint";

/**
 * THE BANK RECONCILIATION STATEMENT (track T4) — Tally's manual reconciliation
 * report, with the one editable column that is the whole feature.
 *
 * Design memo: docs/design/t1t4-ui-model.md §1.4. Keys from RESEARCH.md:879.
 *
 * T4 shipped the server half — migration 0045, the statement, the tick-off loop —
 * reachable over HTTP only. `FeaturesScreen` has been saying so in capitals:
 * "BUILT ON THE SERVER, NOT REACHABLE FROM THIS APP … an operator cannot use any
 * of it without calling the server directly." This screen is the other half.
 *
 * WHAT THIS SCREEN WILL NOT DO:
 *
 *  - It does not compute money. Every amount and all four balances are fixed-4
 *    decimal STRINGS rendered as received, and `balanced` is the SERVER'S verdict
 *    rather than a string comparison done here. A reconciliation that disagreed
 *    with the Trial Balance by a paisa because a browser parsed a float would be
 *    the worst possible bug in this feature.
 *  - It does not compute `age_days`. That is server-derived; a clock-skewed
 *    browser would show a different age for the same row.
 *  - It does not default a bank date. A reconciliation is an assertion copied off
 *    a bank statement, and defaulting it to today would invent the one fact the
 *    feature exists to record. The server refuses an empty one.
 *  - It does not compute a match FOR YOU. Alt+O imports the bank's own CSV (T4
 *    slice 2a) and shows what the bank said; Alt+S opens the matcher (slice 2b,
 *    shipped 2026-08-28), which PROPOSES matches and ticks nothing. Every Bank
 *    Date on this screen is still an operator's assertion — accepting a
 *    proposal is how one gets made, not an exception to it.
 *
 * The account number and IFSC ARE shown now — migration 0064 stores them — and
 * they are rendered as the words "not recorded" when absent, because a blank
 * labelled field reads as "this bank has no IFSC" (invariant I-11).
 */

/**
 * A ledger the operator may reconcile.
 *
 * The bank identity rides along because the import panel needs it to say
 * whether a file could be checked against this account at all — and because
 * showing "not recorded" is what stops a blank field reading as "this bank has
 * no IFSC" (I-11).
 */
type BankLedger = Pick<
  LedgerListItem,
  "id" | "name" | "group_name" | "bank_account_number" | "bank_ifsc"
>;

export function BankReconciliationScreen({ snapshot }: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();

  // F2 sets the statement date. The BRS is a POSITION as at a date, so only the
  // upper bound applies — the period overlay says so on its face.
  const asOn = snapshot.config?.periodTo;

  /**
   * The chosen bank ledger's NAME, when the frame carried one.
   *
   * The pure navigation machine can only carry a name (it has no database), so
   * this screen resolves name -> id against the ledger list. That indirection is
   * also the guard: the list is the only place `is_bank` is known.
   */
  const drilledName = useMemo(() => {
    const detail = snapshot.detailOf ?? "";
    return detail.startsWith(LEDGER_ROW_PREFIX)
      ? detail.slice(LEDGER_ROW_PREFIX.length)
      : null;
  }, [snapshot.detailOf]);

  const [banks, setBanks] = useState<BankLedger[] | null>(null);
  const [ledgerID, setLedgerID] = useState<string | null>(null);
  const [picking, setPicking] = useState(false);
  // Alt+O — the import panel. A panel ON this screen rather than a screen of its
  // own, because slice 2b's matching is inherently a two-column view of
  // statement rows beside book legs; splitting them would make that slice worse.
  const [importing, setImporting] = useState(false);
  // Alt+S — the MATCHING panel (T4 slice 2b). A panel on this screen for the
  // reason the import panel is one, and the reason slice 2a predicted: matching
  // is inherently a two-column view of statement rows beside book legs.
  const [matching, setMatching] = useState(false);
  const [matchView, setMatchView] = useState<"match" | "exceptions" | "basis">(
    "match",
  );
  /** The bank row F8 / F9 / Alt+F8 act on. */
  const [matchRow, setMatchRow] = useState<string | null>(null);
  const [brs, setBrs] = useState<BankReconciliationResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  /**
   * Bumped after EVERY completed write attempt, success or failure, and folded
   * into each Bank Date input's React `key` so the cell remounts and re-reads
   * the SERVER's value.
   *
   * ⛔ THE CELL IS AN UNCONTROLLED `defaultValue` INPUT, WHICH IS MOUNT-ONLY.
   * Without this the browser keeps showing whatever was typed even when the
   * server stored nothing, and both failure modes were driven and confirmed:
   *   - a date the 0045 trigger REFUSED (a cheque cannot clear before it is
   *     written) stayed in the cell beside the error;
   *   - a date the server ACCEPTED but which falls after the statement's own
   *     as-at date came back as un-cleared, and the cell went on displaying it
   *     on a row filed under "Un-cleared".
   * Both leave an operator looking at a date the books do not have.
   */
  const [writeGen, setWriteGen] = useState(0);
  /**
   * Set when a tick SUCCEEDED but the bank date is after the statement date, so
   * the item is correctly un-cleared IN THIS STATEMENT (trap 1 of bankrecon.go).
   * Without this the screen is byte-identical after a 200 and the operator has
   * no way to tell a stored date from a lost one.
   */
  const [outOfWindow, setOutOfWindow] = useState<string | null>(null);

  // ---- The bank accounts this company has.
  //
  // is_bank is SERVER-DERIVED and must not be second-guessed from group_name:
  // bank-ness is ltree ancestry, group_name is the immediate group, so filtering
  // on the two seeded names here would silently drop a bank account kept in a
  // user-created sub-group. That bug is why the field exists.
  useEffect(() => {
    let live = true;
    client
      .listLedgers(companyID)
      .then((res) => {
        if (!live) return;
        const found = res.ledgers
          .filter((l) => l.is_bank)
          .map((l) => ({
            id: l.id,
            name: l.name,
            group_name: l.group_name,
            bank_account_number: l.bank_account_number,
            bank_ifsc: l.bank_ifsc,
          }));
        setBanks(found);
        // Resolve the drilled name, or fall through to the picker.
        if (drilledName) {
          const match = found.find((b) => b.name === drilledName);
          if (match) {
            setLedgerID(match.id);
          } else {
            setError(
              `“${drilledName}” is not a bank account, so it has no bank reconciliation statement. A statement can only be drawn for a ledger under Bank Accounts or Bank OD A/c.`,
            );
          }
        } else if (found.length === 1) {
          // One bank account: open it. Making the operator pick from a list of
          // one is friction, and this is the common case for a small business.
          setLedgerID(found[0]!.id);
        } else if (found.length > 1) {
          setPicking(true);
        }
      })
      .catch((err: unknown) => {
        if (live) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      live = false;
    };
  }, [client, companyID, drilledName]);

  // ---- The statement itself.
  const load = useCallback(
    (id: string) => {
      setBusy(true);
      client
        .bankReconciliation(companyID, id, asOn)
        .then((res) => {
          setBrs(res);
          setError(null);
        })
        .catch((err: unknown) => {
          setBrs(null);
          // The server NAMES the problem (a non-bank ledger, a missing one), and
          // that message is more useful than anything this screen could invent.
          setError(
            err instanceof ApiError
              ? err.message
              : err instanceof Error
                ? err.message
                : String(err),
          );
        })
        .finally(() => setBusy(false));
    },
    [asOn, client, companyID],
  );

  useEffect(() => {
    if (ledgerID) load(ledgerID);
  }, [ledgerID, load]);

  /**
   * Closing each panel, ONCE — because it now happens from two places: the
   * panel's own Close button and Esc (D114). Two copies of "which state to
   * reset" is how one of them keeps the matcher's selected row alive behind a
   * closed panel, and F9/Alt+F8 then act on a row nobody can see.
   */
  const closeImportPanel = useCallback(() => {
    setImporting(false);
    // Re-read the statement on the way out. An import writes no bank dates by
    // itself, but the rows it adds are what Alt+S then proposes matches from,
    // and a stale statement behind a fresh import is the kind of thing nobody
    // thinks to re-check.
    if (ledgerID) load(ledgerID);
  }, [ledgerID, load]);

  const closeMatchPanel = useCallback(() => {
    setMatching(false);
    setMatchRow(null);
    setMatchView("match");
    if (ledgerID) load(ledgerID);
  }, [ledgerID, load]);

  // Ctrl+P. This screen prints since HANDOFF-PARITY-SPRINT.md §3.4 took the
  // route-shape decision: /print/reports/bank-reconciliation?ledger_id=<uuid>.
  //
  // THE SELECTED LEDGER IS THE WHOLE POINT OF PASSING IT. Without it the target
  // resolves to a refusal rather than to a guess — a statement for an account
  // the operator did not choose is a real document, correctly rendered, for the
  // wrong bank, and nothing on the page says so. Invariant I-10, now closed in
  // the direction of printing rather than refusing.
  //
  // ⛔ AND THE ACCOUNT'S NAME AND THE ACCOUNT COUNT TRAVEL WITH IT. The refusal
  // above cannot be reached on a company with exactly ONE bank account, because
  // the effect above opens it without asking — which is the right call (a
  // pick-list of one is friction) and also means Ctrl+P as the very first key on
  // this screen prints an account the operator never chose. So the confirmation
  // NAMES the account: the convenience stays, and it stops being silent. The
  // count is what lets "you have no bank accounts" be a different sentence from
  // "you have not chosen one yet" — the first cannot be fixed by choosing.
  usePrintAction({
    voucher: {
      voucherID: null,
      bankLedgerID: ledgerID,
      bankLedgerName: banks?.find((b) => b.id === ledgerID)?.name ?? null,
      ...(banks ? { bankAccountCount: banks.length } : {}),
    },
  });

  /**
   * ⛔ Ctrl+A — ADVERTISED ON THIS BAR AND OWNED BY NOTHING, until now.
   *
   * BANK_RECON_BAR carries "Ctrl+A Accept", this component registered no
   * `accept` handler, and the pure machine does not move on Ctrl+A from a
   * bank-reconciliation frame (measured) — so the key answered the shell's
   * generic "Ctrl+A — Accept does not apply on this screen". That is the same
   * class as the four stock screens' dead Ctrl+P: the strip sells a key, the key
   * says the strip is wrong, and the operator stops believing the strip.
   *
   * ⚠️ AND THE HONEST ANSWER IS NOT A SAVE. There is nothing here to accept.
   * Every tick in the Bank Date column is written the moment it is typed (the
   * cell PUTs, and clearing it DELETEs) — deliberately, because a reconciliation
   * half-done and lost on a page change is worse than one done a row at a time.
   * An operator who believes Ctrl+A is what commits their work would leave the
   * screen ASSUMING the ticks were saved on some other key, and on the day one
   * of those PUTs was refused they would never find out.
   *
   * So it states what is already true, and states the SERVER'S verdict on the
   * difference rather than comparing figures here — the same rule the Difference
   * row follows.
   */
  useRegisterScreenAction(
    "accept",
    useCallback((): string => {
      if (!ledgerID || !brs) {
        return "Nothing to accept — select a bank account first (F4). Bank dates are saved as you type them; this screen has no separate save.";
      }
      const account = banks?.find((b) => b.id === ledgerID)?.name ?? "this account";
      return (
        `Nothing to accept — every bank date on ${account} was saved the moment it was typed. ` +
        (brs.balanced
          ? "The statement balances: the difference is zero to the paisa."
          : `The statement does NOT balance — the difference is ${formatMoney(brs.difference)}. Ctrl+A cannot close that; an un-cleared item or a missing voucher can.`)
      );
    }, [banks, brs, ledgerID]),
  );

  // F4 — "select bank ledger" (RESEARCH.md:879).
  useRegisterScreenAction(
    "bankReconciliation",
    useCallback((): string => {
      if (!banks || banks.length === 0) {
        return "This company has no bank accounts. Create a ledger under Bank Accounts (or Bank OD A/c) first.";
      }
      if (banks.length === 1) {
        return `${banks[0]!.name} is this company's only bank account.`;
      }
      setPicking(true);
      return "Select a bank account.";
    }, [banks]),
  );

  // Alt+O — "Import" (RESEARCH.md:361 "O: Import", :415, :1221). Refuses
  // BEFORE opening rather than opening onto nothing: an import needs an account,
  // and a panel that opened with no account selected could only ask again.
  useRegisterScreenAction(
    "importBankStatement",
    useCallback((): string => {
      if (!ledgerID) {
        return "Select a bank account first (F4) — a statement always belongs to one account, and importing it against the wrong one reconciles nothing.";
      }
      setImporting(true);
      return "Import a bank statement (CSV).";
    }, [ledgerID]),
  );

  /**
   * ⛔⛔ Esc — CLOSE THE OPEN PANEL, AND ONLY THEN (D114).
   *
   * Both panels below are rendered by RETURNING them instead of this screen's
   * body, out of React state the navigation machine cannot see. The machine
   * therefore did the only thing it could with Esc — pop the one frame on the
   * stack — and an operator who pressed Esc in the import panel landed on the
   * `Gateway of Tally`, two screens from where that panel's own footer says
   * they will be ("Esc goes back to the reconciliation statement"). Driven
   * 2026-08-31, three times, one keypress each.
   *
   * ⚠️ IT COSTS MORE THAN A KEYSTROKE. Bank Reconciliation is not in the Alt+G
   * Go To list (§2.2), so an operator who has just imported a statement and
   * wants to tick against it has to know to find the Gateway entry again.
   *
   * ⛔ THE ORDER IS MATCHING, THEN IMPORT, THEN NOTHING — and the last arm is
   * the load-bearing one: with no panel open this returns `undefined`, the key
   * is forwarded, and Esc goes on popping the frame exactly as before. An
   * OVERLAY (F12 Configure, the F2 period) also wins over both panels, because
   * that is the machine's own precedence and a panel closing underneath an open
   * dialog would leave the operator holding a dialog for a screen they can no
   * longer see.
   */
  useRegisterScreenAction(
    "closePanel",
    useCallback(
      (snap: NavSnapshot) => {
        if (snap.overlay) return undefined;
        if (matching) {
          closeMatchPanel();
          return screenHandled(
            "Closed the matching panel. Nothing was accepted that you did not accept.",
          );
        }
        if (importing) {
          closeImportPanel();
          return screenHandled("Back to the reconciliation statement.");
        }
        return undefined;
      },
      [closeImportPanel, closeMatchPanel, importing, matching],
    ),
  );

  // ---- T4 SLICE 2b — the five matching keys (RESEARCH.md:879).
  //
  // ⛔ EVERY ONE OF THEM REFUSES BEFORE OPENING ONTO NOTHING, and the refusals
  // name the key that fixes them. A panel that opened with no account selected
  // could only ask again, and a row-scoped key with no row selected would
  // otherwise act on whichever row happened to be first — which for a WITHDRAW
  // is a real reconciliation undone by accident.

  // Alt+S — "potential matches" (:879). Its slice-2a refusal promised the §0
  // rule; the panel now keeps that promise.
  useRegisterScreenAction(
    "matchBankRows",
    useCallback((): string => {
      if (!ledgerID) {
        return "Select a bank account first (F4) — matching compares ONE account's imported rows against the vouchers on that account.";
      }
      setMatchView("match");
      setMatching(true);
      return "Proposed matches. NOTHING here is reconciled until you accept it.";
    }, [ledgerID]),
  );

  // Ctrl+J — the exception report. It is a VIEW over the proposals already
  // loaded, not a second endpoint: two routes answering "what is outstanding?"
  // would be two answers that can disagree.
  useRegisterScreenAction(
    "bankExceptionReport",
    useCallback((): string => {
      if (!ledgerID) {
        return "Select a bank account first (F4).";
      }
      setMatchView("exceptions");
      setMatching(true);
      return "Exception report — what is still unmatched, and why.";
    }, [ledgerID]),
  );

  // Ctrl+B — basis of values. What the comparison actually did: zero tolerance,
  // the search window, and what each tier rests on.
  useRegisterScreenAction(
    "bankBasisOfValues",
    useCallback((): string => {
      if (!ledgerID) {
        return "Select a bank account first (F4).";
      }
      setMatchView("basis");
      setMatching(true);
      return "Basis of values — how these proposals were arrived at.";
    }, [ledgerID]),
  );

  // F9 — "set status manually" (:879). In this build the status a person can
  // set by hand is SET ASIDE: this bank row matches nothing in the books.
  //
  // ⛔ IT IS NOT A WEAKER "MATCHED". It writes no bank date and moves no
  // balance, and the panel words it so it cannot be read as reconciled.
  useRegisterScreenAction(
    "setAsideBankRow",
    useCallback((): string => {
      if (!matching || !panelOps.setAside) {
        return "Open the matching panel first (Alt+S) — F9 sets the status of an imported BANK ROW, and there are none on screen.";
      }
      if (!matchRow) {
        return "Select a bank row first. F9 records that THAT row matches nothing in the books, and it must not guess which one you mean.";
      }
      void panelOps.setAside(matchRow);
      return "Set aside: this bank row matches nothing in the books. Nothing was reconciled.";
    }, [matching, matchRow]),
  );

  // Alt+F8 — "unlink a match" (:879).
  //
  // ⛔ IT WITHDRAWS THE CLEARANCE, and says so before it does. Un-linking a
  // match and un-ticking a bank date are the SAME operation (migration 0088's
  // cascade), so a key that said only "unlink" would understate what it does.
  useRegisterScreenAction(
    "unlinkBankMatch",
    useCallback((): string => {
      if (!matching || !panelOps.unlink) {
        return "Open the matching panel first (Alt+S) — Alt+F8 withdraws a MATCH, and the matches are listed there.";
      }
      const resolved = panelOps.data?.resolved ?? [];
      const target = resolved.find(
        (r) => r.line_id === matchRow && r.status === "matched",
      );
      if (!target || !target.voucher_id) {
        return "Select a MATCHED bank row first. Alt+F8 withdraws a match and the bank date it recorded, so it must not guess which.";
      }
      void panelOps.unlink(target.voucher_id, target.leg_no ?? 0);
      return `Withdrawing the match to ${target.class ?? ""} ${target.voucher_no ?? ""} — the voucher becomes un-cleared again.`;
    }, [matching, matchRow]),
  );

  // F8 — "modify match" (:879). Re-matching IS withdraw-then-choose, and it says
  // so rather than pretending to be an edit: there is no way to re-point a
  // clearance without first withdrawing the one that exists.
  useRegisterScreenAction(
    "modifyBankMatch",
    useCallback((): string => {
      if (!matching || !panelOps.unlink) {
        return "Open the matching panel first (Alt+S).";
      }
      const resolved = panelOps.data?.resolved ?? [];
      const target = resolved.find(
        (r) => r.line_id === matchRow && r.status === "matched",
      );
      if (!target || !target.voucher_id) {
        return "Select a MATCHED bank row first — F8 re-matches one, which means withdrawing the match it has now.";
      }
      void panelOps.unlink(target.voucher_id, target.leg_no ?? 0);
      return `Match withdrawn and the bank date with it. The row is back among the proposals — choose the voucher it should be.`;
    }, [matching, matchRow]),
  );

  /** Tick an item off, or un-tick it. The ONE write this screen makes. */
  const setBankDate = useCallback(
    (line: BankReconLine, value: string) => {
      if (!ledgerID) return;
      setBusy(true);
      setError(null);
      setOutOfWindow(null);
      const done = () => {
        // The write landed. If the date the operator entered is AFTER this
        // statement's as-at date the item is still un-cleared HERE — correct,
        // and invisible without saying so, because every figure on the page is
        // unchanged and the line does not move. See outOfWindow's doc.
        if (value !== "" && asOn && value > asOn) {
          setOutOfWindow(
            `Recorded: ${line.class || "voucher"} ${line.voucher_no} cleared on ${value}. ` +
              `That is AFTER this statement's date of ${asOn}, so it stays under Un-cleared here ` +
              `and none of the balances above move — a statement as at ${asOn} cannot know about it. ` +
              `Press F2 and set the date to ${value} or later to see it cleared.`,
          );
        }
        setWriteGen((g) => g + 1);
        load(ledgerID);
      };
      const fail = (err: unknown) => {
        // Migration 0045's constraint-trigger refusals arrive here (a bank date
        // before the voucher date, a non-bank ledger). They name the field, so
        // they are shown as they are.
        setError(err instanceof Error ? err.message : String(err));
        setBusy(false);
        // ⛔ AND PUT THE SERVER'S VALUE BACK IN THE CELL. Showing the refused
        // date beside the refusal reads as "stored, with a warning" — the one
        // reading that is false.
        setWriteGen((g) => g + 1);
      };
      if (value === "") {
        client
          .clearBankDate(companyID, ledgerID, line.voucher_id, line.leg_no)
          .then(done)
          .catch(fail);
        return;
      }
      client
        .setBankDate(companyID, ledgerID, line.voucher_id, line.leg_no, {
          bank_date: value,
          created_by: "ui",
        })
        .then(done)
        .catch(fail);
    },
    [client, companyID, ledgerID, load, asOn],
  );

  // ---- The matching panel (Alt+S, Ctrl+J, Ctrl+B).
  if (matching && ledgerID) {
    const bank = banks?.find((b) => b.id === ledgerID);
    return (
      <BankMatchPanel
        ledgerID={ledgerID}
        ledgerName={bank?.name ?? brs?.ledger_name ?? ""}
        to={asOn}
        view={matchView}
        onView={setMatchView}
        selected={matchRow}
        onSelect={setMatchRow}
        onWrote={() => {
          // A match writes a bank date, so the statement behind this panel is
          // now stale. Re-reading it here is what stops the operator closing the
          // panel onto figures from before their own work.
          setWriteGen((g) => g + 1);
          load(ledgerID);
        }}
        onClose={closeMatchPanel}
      />
    );
  }

  // ---- The import panel (Alt+O).
  if (importing && ledgerID) {
    const bank = banks?.find((b) => b.id === ledgerID);
    return (
      <BankStatementImportPanel
        ledgerID={ledgerID}
        ledgerName={bank?.name ?? brs?.ledger_name ?? ""}
        bankAccountNumber={bank?.bank_account_number ?? ""}
        bankIFSC={bank?.bank_ifsc ?? ""}
        onClose={closeImportPanel}
      />
    );
  }

  // ---- The bank-account picker.
  if (picking && banks && banks.length > 0) {
    return (
      <section className="flex h-full flex-col bg-tally-bg p-4 text-tally-field">
        <h1 className="mb-4 text-center font-mono text-lg font-bold text-tally-title">
          Bank Reconciliation
        </h1>
        <div className="mx-auto w-96 bg-tally-panel p-3">
          <SelectionList
            label="SELECT A BANK ACCOUNT"
            options={banks.map((b) => b.name)}
            onSelect={(name) => {
              const match = banks.find((b) => b.name === name);
              if (match) {
                setLedgerID(match.id);
                setPicking(false);
                setError(null);
              }
            }}
            autoFocus
          />
        </div>
        <p className="mt-3 text-center text-xs">
          F4 reopens this list. Esc goes back.
        </p>
      </section>
    );
  }

  if (error && !brs) {
    return (
      <section className="flex h-full flex-col bg-tally-bg p-4 text-tally-field">
        <h1 className="mb-2 text-lg font-bold text-tally-title">
          Bank Reconciliation
        </h1>
        <p data-testid="brs-error" className="max-w-2xl text-sm">
          {error}
        </p>
      </section>
    );
  }

  if (banks && banks.length === 0) {
    return (
      <section className="flex h-full flex-col bg-tally-bg p-4 text-tally-field">
        <h1 className="mb-2 text-lg font-bold text-tally-title">
          Bank Reconciliation
        </h1>
        {/* An explicit state, not a blank grid: the operator must be able to tell
            "you have no bank accounts" from "the screen is broken". */}
        <p data-testid="brs-no-banks" className="max-w-2xl text-sm">
          This company has no bank accounts, so there is nothing to reconcile.
          Create a ledger under <strong>Bank Accounts</strong> (or{" "}
          <strong>Bank OD A/c</strong> for an overdraft) and its postings will
          appear here.
        </p>
      </section>
    );
  }

  if (!brs) {
    return (
      <section className="flex h-full flex-col bg-tally-bg p-4 text-tally-field">
        <h1 className="mb-2 text-lg font-bold text-tally-title">
          Bank Reconciliation
        </h1>
        <p className="text-sm">Loading…</p>
      </section>
    );
  }

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 text-tally-field">
      <header className="mb-3">
        <h1 className="text-lg font-bold text-tally-title">
          Bank Reconciliation — {brs.ledger_name}
        </h1>
        <p className="text-xs" data-testid="brs-as-on">
          As at {brs.as_on}
          {busy ? " · working…" : ""}
        </p>
      </header>

      {/* THE BROKEN-RECONCILIATION WARNING, above the rows.
          Non-zero broken_count means somebody ALTERED a voucher after it was
          reconciled. The server treats such an item as un-cleared so the
          difference cannot silently absorb the edit; a client that rendered it as
          an ordinary row would defeat the one invariant T4 was built around. */}
      {brs.broken_count > 0 ? (
        <p
          data-testid="brs-broken-warning"
          className="mb-3 border border-tally-active bg-tally-panel p-2 text-sm font-bold"
        >
          {brs.broken_count}{" "}
          {brs.broken_count === 1 ? "item was" : "items were"} altered AFTER
          being reconciled. {brs.broken_count === 1 ? "It is" : "They are"}{" "}
          treated as un-cleared and marked below, with what was originally
          cleared shown beside the current figure. Re-tick{" "}
          {brs.broken_count === 1 ? "it" : "them"} against the bank statement.
        </p>
      ) : null}

      {/* The four balance lines — the point of the statement. */}
      <table className="mb-4 w-full max-w-xl text-sm">
        <tbody>
          <BalanceRow
            label="Balance as per company books"
            value={brs.balance_as_per_books}
            testid="brs-books"
          />
          <BalanceRow
            label="Amounts not reflected in bank"
            value={brs.uncleared_net}
            testid="brs-uncleared-net"
          />
          <BalanceRow
            label="Balance as per bank"
            value={brs.balance_as_per_bank}
            testid="brs-per-bank"
            bold
          />
          <tr>
            <td className="py-0.5">Difference</td>
            <td className="py-0.5 text-right font-mono" data-testid="brs-difference">
              {formatMoney(brs.difference)}
            </td>
            <td className="pl-3 text-xs" data-testid="brs-balanced">
              {/* The SERVER's verdict, not a string comparison here. */}
              {brs.balanced ? "balanced" : "NOT BALANCED"}
            </td>
          </tr>
        </tbody>
      </table>

      <LineTable
        title={`Un-cleared (${brs.uncleared.length})`}
        emptyNote="Nothing outstanding — every posting on this account has cleared as at this date."
        lines={brs.uncleared}
        testid="brs-uncleared"
        onSetBankDate={setBankDate}
        writeGen={writeGen}
      />
      <LineTable
        title={`Cleared (${brs.cleared.length})`}
        emptyNote="Nothing has been ticked off yet. Enter the date the bank cleared each item in the Bank Date column above."
        lines={brs.cleared}
        testid="brs-cleared"
        onSetBankDate={setBankDate}
        writeGen={writeGen}
      />

      {error ? (
        <p data-testid="brs-row-error" className="mt-3 text-sm font-bold">
          {error}
        </p>
      ) : null}

      {/* THE STORED-BUT-NOT-HERE DISCLOSURE. A tick whose bank date is after
          this statement's as-at date is correctly un-cleared in THIS statement
          (bankrecon.go trap 1) — so the write succeeds and the page does not
          move by a single paisa. Saying nothing is what made a confirmed 200
          indistinguishable from a lost keystroke. */}
      {outOfWindow ? (
        <p
          data-testid="brs-out-of-window"
          className="mt-3 border border-tally-active bg-tally-panel p-2 text-sm"
        >
          {outOfWindow}
        </p>
      ) : null}

      <p className="mt-4 max-w-3xl text-xs" data-testid="brs-import-note">
        Reconciling never touches a voucher: the bank date is recorded beside it,
        outside the hash chain, because it is a fact about the bank and not about
        the voucher.{" "}
        {/* ⛔⛔ D109's SIBLING, AND THE REASON THIS COMMENT IS LONGER THAN THE
            SENTENCE. This paragraph used to end "Matching those rows to
            vouchers is not built yet". That was TRUE when it was written and
            stopped being true on 2026-08-28, when Alt+S shipped a complete
            matcher ON THIS SCREEN — and nothing brought the sentence back.
            Measured 2026-08-31: an operator read the denial here, hand-ticked a
            whole statement a Bank Date at a time, and never pressed the key,
            while the import report two screens away said the opposite in
            writing ("Matching now works: press Alt+S").

            ⭐ THE HALF IT GOT RIGHT IS KEPT, and it is the half that matters:
            this paragraph exists to stop an operator believing the ticks above
            are automatic. THEY ARE NOT, AND THE MATCHER DOES NOT TICK EITHER —
            it PROPOSES, and accepting a proposal is the operator's assertion.
            That is what the match screen says on its own face
            ("NOTHING ON THIS SCREEN HAS BEEN RECONCILED"), so the two now agree
            rather than contradicting each other in two directions. */}
        <strong>Alt+O imports your bank&apos;s CSV statement</strong> and shows
        what the bank said. <strong>Alt+S matches those rows against your
        vouchers</strong> — it only ever PROPOSES a match, and accepting one
        writes the bank&apos;s own value date into the Bank Date column above.
        Nothing on this screen is ticked for you, by hand or by Alt+S.
      </p>
    </section>
  );
}

function BalanceRow({
  label,
  value,
  testid,
  bold,
}: {
  label: string;
  value: string;
  testid: string;
  bold?: boolean;
}) {
  return (
    <tr className={bold ? "font-bold" : undefined}>
      <td className="py-0.5">{label}</td>
      <td className="py-0.5 text-right font-mono" data-testid={testid}>
        {formatMoney(value)}
      </td>
      <td />
    </tr>
  );
}

function LineTable({
  title,
  emptyNote,
  lines,
  testid,
  onSetBankDate,
  writeGen,
}: {
  title: string;
  emptyNote: string;
  lines: BankReconLine[];
  testid: string;
  onSetBankDate: (line: BankReconLine, value: string) => void;
  /** See the parent's `writeGen` doc — it forces the date cells to remount. */
  writeGen: number;
}) {
  return (
    <div className="mb-5">
      <h2 className="mb-1 text-sm font-bold text-tally-title">{title}</h2>
      {lines.length === 0 ? (
        <p data-testid={`${testid}-empty`} className="text-xs">
          {emptyNote}
        </p>
      ) : (
        <table className="w-full text-sm" data-testid={testid}>
          <thead>
            <tr className="border-b border-tally-panel text-left text-xs">
              <th className="py-1 pr-2">Date</th>
              <th className="py-1 pr-2">Particulars</th>
              {/* CLASS BEFORE NUMBER. Numbering is per (class, FY), so the
                  number alone repeats down this column — the demo's HDFC Bank
                  shows four rows numbered "1". Without the class an operator
                  cannot tell which voucher a row is. */}
              <th className="py-1 pr-2">Type</th>
              <th className="py-1 pr-2">Vch No</th>
              <th className="py-1 pr-2 text-right">Amount</th>
              <th className="py-1 pr-2">Bank Date</th>
              <th className="py-1 pr-2 text-right">Age</th>
            </tr>
          </thead>
          <tbody>
            {lines.map((l) => (
              <tr
                key={`${l.voucher_id}:${l.leg_no}`}
                data-testid={`brs-line:${l.voucher_id}:${l.leg_no}`}
                className={l.broken_by_alter ? "font-bold" : undefined}
              >
                <td className="py-0.5 pr-2 font-mono">{l.voucher_date}</td>
                <td className="py-0.5 pr-2">
                  {l.party || l.narration || "—"}
                  {l.broken_by_alter ? (
                    <span
                      className="ml-2 text-xs"
                      data-testid={`brs-broken:${l.voucher_id}:${l.leg_no}`}
                    >
                      {/* The EVIDENCE of what changed: what was cleared, beside
                          what the voucher now says. */}
                      ALTERED AFTER RECONCILING — cleared as{" "}
                      {formatMoney(l.cleared_amount ?? "0.0000")}{" "}
                      {l.cleared_dr_cr}, now {formatMoney(l.amount)} {l.dr_cr}
                    </span>
                  ) : null}
                </td>
                <td className="py-0.5 pr-2">{l.class || "—"}</td>
                <td className="py-0.5 pr-2 font-mono">{l.voucher_no}</td>
                <td className="py-0.5 pr-2 text-right font-mono">
                  {formatMoney(l.amount)} {l.dr_cr}
                </td>
                <td className="py-0.5 pr-2">
                  {/* THE ONE EDITABLE CELL. Clearing it un-reconciles (idempotent
                      on the server), which is how a mis-keyed date is undone. */}
                  <input
                    // ⛔ THE KEY CARRIES THE SERVER'S ANSWER AND THE WRITE
                    // GENERATION. `defaultValue` is applied at MOUNT only, so
                    // without a changing key this cell keeps displaying a date
                    // the server refused — or one it accepted but filtered out
                    // of this statement's window — on a row filed under
                    // Un-cleared. Both were driven and confirmed.
                    key={`${l.bank_date}:${writeGen}`}
                    type="date"
                    // Named by CLASS and DATE, not by number alone: several legs
                    // on one bank ledger share a voucher number.
                    aria-label={`Bank date for ${l.class || "voucher"} ${l.voucher_no} dated ${l.voucher_date}`}
                    data-testid={`brs-bankdate:${l.voucher_id}:${l.leg_no}`}
                    className="bg-tally-field px-1 font-mono text-tally-fieldText outline-none"
                    defaultValue={l.bank_date}
                    min={l.voucher_date}
                    onBlur={(e) => {
                      if (e.target.value !== l.bank_date) {
                        onSetBankDate(l, e.target.value);
                      }
                    }}
                  />
                </td>
                <td className="py-0.5 pr-2 text-right font-mono">
                  {/* Server-derived. Never recomputed here. */}
                  {l.bank_date ? "" : `${l.age_days}d`}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
