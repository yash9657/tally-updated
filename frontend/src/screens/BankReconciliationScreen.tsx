import { useCallback, useEffect, useMemo, useState } from "react";
import { useClient, useCompanyID } from "../app/CompanyContext";
import { useRegisterScreenAction } from "../app/ScreenActions";
import { LEDGER_ROW_PREFIX } from "../keystroke/navMachine";
import type { ScreenComponentProps } from "../app/screenRegistry";
import { ApiError } from "../api";
import type {
  BankReconciliationResponse,
  BankReconLine,
  LedgerListItem,
} from "../api";
import { formatMoney } from "./reports/money";
import { SelectionList } from "./SelectionList";
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
 *  - It does not show a bank account number or IFSC. Those are not stored yet
 *    (docs/design/t4-model.md §2 deferred them to slice 2), and a blank labelled
 *    field reads as "this bank has no IFSC".
 */

/** A ledger the operator may reconcile. */
type BankLedger = Pick<LedgerListItem, "id" | "name" | "group_name">;

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
  const [brs, setBrs] = useState<BankReconciliationResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

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
          .map((l) => ({ id: l.id, name: l.name, group_name: l.group_name }));
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

  // Ctrl+P. This screen CANNOT print — the print route carries no ledger id, so
  // a company with three bank accounts has no way to say which one to print
  // (docs/design/t4-model.md §1.3, an open product decision). But the key must
  // still be registered, or the shell falls through to its generic "Print does
  // not apply on this screen", which tells the operator nothing about WHY and
  // reads like a missing feature rather than a deliberate refusal.
  //
  // A browser gate caught exactly that: printTarget.ts had the reasoned refusal
  // and no screen was wired to deliver it. Invariant I-10.
  usePrintAction();

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

  /** Tick an item off, or un-tick it. The ONE write this screen makes. */
  const setBankDate = useCallback(
    (line: BankReconLine, value: string) => {
      if (!ledgerID) return;
      setBusy(true);
      const done = () => {
        load(ledgerID);
      };
      const fail = (err: unknown) => {
        // Migration 0045's constraint-trigger refusals arrive here (a bank date
        // before the voucher date, a non-bank ledger). They name the field, so
        // they are shown as they are.
        setError(err instanceof Error ? err.message : String(err));
        setBusy(false);
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
    [client, companyID, ledgerID, load],
  );

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
      />
      <LineTable
        title={`Cleared (${brs.cleared.length})`}
        emptyNote="Nothing has been ticked off yet. Enter the date the bank cleared each item in the Bank Date column above."
        lines={brs.cleared}
        testid="brs-cleared"
        onSetBankDate={setBankDate}
      />

      {error ? (
        <p data-testid="brs-row-error" className="mt-3 text-sm font-bold">
          {error}
        </p>
      ) : null}

      <p className="mt-4 max-w-3xl text-xs">
        Reconciling never touches a voucher: the bank date is recorded beside it,
        outside the hash chain, because it is a fact about the bank and not about
        the voucher. Statement import and automatic matching are not built yet.
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
}: {
  title: string;
  emptyNote: string;
  lines: BankReconLine[];
  testid: string;
  onSetBankDate: (line: BankReconLine, value: string) => void;
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
                data-testid={`brs-line:${l.voucher_no}:${l.leg_no}`}
                className={l.broken_by_alter ? "font-bold" : undefined}
              >
                <td className="py-0.5 pr-2 font-mono">{l.voucher_date}</td>
                <td className="py-0.5 pr-2">
                  {l.party || l.narration || "—"}
                  {l.broken_by_alter ? (
                    <span
                      className="ml-2 text-xs"
                      data-testid={`brs-broken:${l.voucher_no}:${l.leg_no}`}
                    >
                      {/* The EVIDENCE of what changed: what was cleared, beside
                          what the voucher now says. */}
                      ALTERED AFTER RECONCILING — cleared as{" "}
                      {formatMoney(l.cleared_amount ?? "0.0000")}{" "}
                      {l.cleared_dr_cr}, now {formatMoney(l.amount)} {l.dr_cr}
                    </span>
                  ) : null}
                </td>
                <td className="py-0.5 pr-2 font-mono">{l.voucher_no}</td>
                <td className="py-0.5 pr-2 text-right font-mono">
                  {formatMoney(l.amount)} {l.dr_cr}
                </td>
                <td className="py-0.5 pr-2">
                  {/* THE ONE EDITABLE CELL. Clearing it un-reconciles (idempotent
                      on the server), which is how a mis-keyed date is undone. */}
                  <input
                    type="date"
                    aria-label={`Bank date for voucher ${l.voucher_no}`}
                    data-testid={`brs-bankdate:${l.voucher_no}:${l.leg_no}`}
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
