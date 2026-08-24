import type { LedgerListItem, VoucherClass } from "../../api";
import { BillAllocationPicker } from "./BillAllocationPicker";
import { CostAllocationPicker } from "./CostAllocationPicker";
import {
  type DoubleEntryLine,
  doubleEntryRefusal,
  doubleEntryTotals,
  emptyDoubleEntryLine,
} from "./doubleEntry";
import { normalizeMoney } from "./money";

/**
 * THE DOUBLE-ENTRY POSTING GRID — the Dr/Cr lines of a Journal (F7), and of a
 * Payment / Receipt / Contra switched to Double Entry with Ctrl+H.
 *
 * The pure arithmetic and every refusal message live in `doubleEntry.ts`; this
 * file is presentation. See that module's header for why this is NOT modelled
 * on the single-entry panel: single entry infers one leg and puts the SUM on
 * it, and a double-entry voucher states both sides — the opposite operation.
 *
 * ⛔ THE DIFFERENCE IS SHOWN CONTINUOUSLY, NOT ONLY ON ACCEPT. An operator
 * keying a six-line reclass finds out the entry is 200 short while they are
 * still looking at the lines, not after a round trip that costs them the form.
 * The accept path refuses independently (`buildDoubleEntryRequest` calls the
 * same `doubleEntryRefusal`), so the guard is not load-bearing on this
 * component rendering — that is deliberate defence in depth, and both halves
 * are pinned by their own tests.
 */
export function DoubleEntryPanel({
  voucherClass,
  lines,
  setLines,
  parties,
  ledgerByName,
  kindForGroup,
}: {
  /**
   * The class being keyed — the AUTHORITY the row's open-bill list is read under
   * (D97). Passed down rather than inferred: this grid is reached from F5/F6/F4
   * and from the Journal, and each is a different `display <class>` rule.
   */
  voucherClass: VoucherClass;
  lines: readonly DoubleEntryLine[];
  setLines: (
    next: (prev: readonly DoubleEntryLine[]) => DoubleEntryLine[],
  ) => void;
  /** The company's real accounts, offered as completions. */
  parties: readonly string[];
  ledgerByName: (name: string) => LedgerListItem | undefined;
  kindForGroup: (group: string) => "RECEIVABLE" | "PAYABLE";
}) {
  const rows = lines.length > 0 ? lines : [emptyDoubleEntryLine()];
  const totals = doubleEntryTotals(rows);
  const refusal = doubleEntryRefusal(rows);

  const setLine = (i: number, patch: Partial<DoubleEntryLine>) =>
    setLines((prev) => prev.map((l, idx) => (idx === i ? { ...l, ...patch } : l)));

  return (
    <div
      className="mt-3 max-w-3xl bg-tally-panel p-3 text-sm"
      data-testid="double-entry-panel"
    >
      <h2 className="mb-1 text-tally-title">Particulars (Dr/Cr)</h2>
      <p className="mb-2 text-xs">
        Double entry: every line states its OWN side and its own amount.
        Nothing is inferred and no total is filled in for you — debits must
        equal credits exactly, to the paisa.
      </p>

      <ul>
        {rows.map((l, i) => (
          <li key={i} className="mb-1 flex flex-wrap items-center gap-2">
            <select
              aria-label={`Line ${i + 1} side`}
              data-testid={`de-side:${i}`}
              className="bg-tally-field px-1 text-tally-fieldText outline-none"
              value={l.side}
              onChange={(e) =>
                setLine(i, { side: e.target.value === "CR" ? "CR" : "DR" })
              }
            >
              <option value="DR">Dr</option>
              <option value="CR">Cr</option>
            </select>
            <input
              aria-label={`Line ${i + 1} ledger`}
              data-testid={`de-ledger:${i}`}
              list="double-entry-ledgers"
              className="bg-tally-field px-1 text-tally-fieldText outline-none"
              value={l.ledger}
              placeholder="ledger"
              onChange={(e) => setLine(i, { ledger: e.target.value })}
            />
            <input
              aria-label={`Line ${i + 1} amount`}
              data-testid={`de-amount:${i}`}
              className="w-32 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
              value={l.amount}
              placeholder="0.0000"
              onChange={(e) => setLine(i, { amount: e.target.value })}
            />
            {rows.length > 1 ? (
              <button
                type="button"
                data-testid={`de-remove:${i}`}
                className="px-1 text-xs underline"
                onClick={() =>
                  setLines((prev) => prev.filter((_, idx) => idx !== i))
                }
              >
                remove
              </button>
            ) : null}

            {/* ⛔ THE COST SPLITS, ON THE ROW THE OPERATOR IS LOOKING AT. The
                0-based posting_seq is this row's own position — and on THIS
                grid the rows map to the postings array one-for-one from index
                0, which is why buildDoubleEntryRequest passes 0 where single
                entry passes 1. Allocating a Journal was previously impossible
                from any screen: it had to arrive over HTTP. */}
            <CostAllocationPicker
              line={l}
              lineIndex={i}
              onChange={(allocations) => setLine(i, { allocations })}
            />

            {/* ⛔ THE BILL THIS LINE OPENS OR SETTLES. On the ROW, because a
                Journal's party field names no ledger — a write-off credits the
                debtor on one of these lines, and that line is the bill-bearing
                leg. The side comes from the row, so a Journal that reverses a
                credit note references on the debit side without a special
                case. */}
            {(() => {
              const led = ledgerByName(l.ledger);
              if (led === undefined || !led.maintain_bill_by_bill) return null;
              return (
                <BillAllocationPicker
                  voucherClass={voucherClass}
                  leg={{
                    ledgerId: led.id,
                    drCr: l.side,
                    amount: normalizeMoney(l.amount),
                  }}
                  refs={l.billRefs}
                  // A Journal can do either — write a bill off (settle) or
                  // raise one (an opening balance, a provision billed later).
                  // "settle" offers the party's real open bills, which is the
                  // case an operator cannot key by hand; "NEW" is still
                  // reachable from inside the picker.
                  mode="settle"
                  kind={kindForGroup(led.group_name)}
                  label={led.name}
                  onChange={(billRefs) => setLine(i, { billRefs })}
                />
              );
            })()}
          </li>
        ))}
      </ul>

      {/* The company's real accounts as completions rather than a closed list:
          a ledger that does not exist yet is created on accept, exactly as the
          party field and the single-entry particulars do. */}
      <datalist id="double-entry-ledgers">
        {parties.map((p) => (
          <option key={p} value={p} />
        ))}
      </datalist>

      <button
        type="button"
        data-testid="de-add-line"
        className="mt-1 px-2 py-0.5 text-xs underline"
        onClick={() => setLines((prev) => [...prev, emptyDoubleEntryLine()])}
      >
        + Add line
      </button>

      <div className="mt-2 border-t border-tally-field pt-2">
        <p data-testid="de-totals">
          Dr {totals.debit} · Cr {totals.credit} ·{" "}
          <span
            data-testid="de-difference"
            className={totals.balanced ? "text-tally-active" : "text-red-400"}
          >
            {totals.balanced
              ? "balanced"
              : `difference ${totals.difference}`}
          </span>
        </p>
        {/* ⛔ THE REFUSAL, IN THE FORM. A 422 from the server names a database
            constraint after a round trip; this names both figures and what is
            between them, while the operator is still on the line that is
            wrong. */}
        {refusal ? (
          <p
            data-testid="de-refusal"
            role="alert"
            className="mt-1 text-xs text-red-400"
          >
            {refusal}
          </p>
        ) : null}
      </div>
    </div>
  );
}
