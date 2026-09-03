import type { LedgerListItem, VoucherClass } from "../../api";
import { BillAllocationPicker } from "./BillAllocationPicker";
import { CostAllocationPicker } from "./CostAllocationPicker";
import {
  type DoubleEntryLine,
  doubleEntryForexRefusal,
  doubleEntryRefusal,
  doubleEntryTotals,
  emptyDoubleEntryLine,
} from "./doubleEntry";
import { normalizeMoney } from "./money";
// ⚠️ THE FIXED-4 PAIR LIVES IN `itemInvoice.ts`, which is where the money parser
// and formatter happen to be exported from — and `parseFixed4`'s own comment says
// it TRUNCATES. That is correct for money and wrong for a rate, which is why the
// rate pair is imported separately below and the two are never mixed.
import { formatFixed4, parseFixed4 } from "./itemInvoice";
// ⛔ THE RATE HELPERS, NEVER THE MONEY ONES — see api/rate.ts's header.
import { foreignToBase, parseRate } from "../../api/rate";

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
  currencies = [],
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
  /**
   * ⭐⭐ T8 STEP 8 — THE COMPANY'S FOREIGN CURRENCIES.
   *
   * ⛔ AN EMPTY LIST HIDES THE THREE FIELDS ENTIRELY, and that is the whole reason
   * the list is passed in rather than the fields being rendered unconditionally.
   * Every company that has never defined a currency — which is every company in
   * the field today — sees the grid exactly as it is now. Three permanently blank
   * inputs on every Journal line would be noise on the form an operator uses
   * most, and noise on a form is how a real field stops being read.
   *
   * ⚠️ IT IS THE COMPANY'S FOREIGN CURRENCIES AND NEVER ITS OWN. Migration 0080's
   * trigger refuses a master for the base currency, because a base amount is
   * recorded with NO currency at all — so "the company's own" is the empty
   * option below and not a row in this list.
   */
  currencies?: readonly { id: string; code: string }[];
}) {
  const rows = lines.length > 0 ? lines : [emptyDoubleEntryLine()];
  const totals = doubleEntryTotals(rows);
  // ⛔ BOTH REFUSALS, AND THE BALANCE ONE FIRST. An unbalanced grid is the error
  // an operator is most likely to be in the middle of, and a half-keyed currency
  // triple on line 4 is not worth burying it under.
  const refusal = doubleEntryRefusal(rows) || doubleEntryForexRefusal(rows);

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
            {/* ⭐⭐ T8 STEP 8 — THE CURRENCY, THE FOREIGN AMOUNT AND THE RATE, on
                the row the operator is looking at.
                ⛔ RENDERED ONLY WHEN THE COMPANY HAS A CURRENCY — see the
                `currencies` prop's own comment.
                ⚠️ AND THE AMOUNT FIELD TO THE LEFT STAYS AUTHORITATIVE. Q2 of the
                design memo: the BASE amount is the stored, authoritative figure
                and the one the double-entry check sums, so a multi-currency
                Journal balances in the company's own currency and need not
                balance in any foreign one. The base is NOT recomputed from the
                foreign amount and the rate — `foreignToBase` offers a
                SUGGESTION, and an operator who overrides it is entitled to be
                right: the rupee figure on the invoice is what the books carry. */}
            {currencies.length > 0 ? (
              <>
                <select
                  aria-label={`Line ${i + 1} currency`}
                  data-testid={`de-currency:${i}`}
                  className="bg-tally-field px-1 text-tally-fieldText outline-none"
                  value={l.currencyId ?? ""}
                  onChange={(e) => {
                    const id = e.target.value;
                    // ⛔ CLEARING THE CURRENCY CLEARS ALL THREE. A row left with a
                    // foreign amount and no currency is refused by the form, by
                    // the server and by a DB CHECK — and leaving the operator to
                    // clear two more fields to undo one choice is how a
                    // two-of-three row gets sent.
                    if (id === "") {
                      setLine(i, {
                        currencyId: undefined,
                        currencyCode: undefined,
                        foreignAmount: undefined,
                        rate: undefined,
                      });
                      return;
                    }
                    setLine(i, {
                      currencyId: id,
                      currencyCode:
                        currencies.find((c) => c.id === id)?.code ?? "",
                    });
                  }}
                >
                  <option value="">₹ own</option>
                  {currencies.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.code}
                    </option>
                  ))}
                </select>
                {(l.currencyId ?? "") !== "" ? (
                  <>
                    <input
                      aria-label={`Line ${i + 1} foreign amount`}
                      data-testid={`de-foreign:${i}`}
                      className="w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                      value={l.foreignAmount ?? ""}
                      placeholder={l.currencyCode ?? "fgn"}
                      onChange={(e) =>
                        setLine(i, { foreignAmount: e.target.value })
                      }
                    />
                    <input
                      aria-label={`Line ${i + 1} exchange rate`}
                      data-testid={`de-rate:${i}`}
                      // ⛔ WIDER THAN THE MONEY FIELDS, DELIBERATELY. A rate is
                      // ten decimals and the low ones ARE the precision; a box
                      // that visibly truncates what was typed invites an operator
                      // to type fewer digits.
                      className="w-36 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                      value={l.rate ?? ""}
                      placeholder="rate / 1"
                      onChange={(e) => setLine(i, { rate: e.target.value })}
                    />
                    {/* ⭐ THE SUGGESTION, AND IT IS A BUTTON RATHER THAN AN
                        AUTOMATIC FILL. Q2 makes the base authoritative, so the
                        form must never quietly overwrite what the operator keyed
                        from the invoice in front of them — but working
                        83.6512345678 × 1000 out by hand is exactly the arithmetic
                        a form should do. Pressing it states the product; not
                        pressing it leaves the invoice's own figure alone. */}
                    <button
                      type="button"
                      data-testid={`de-base-from-forex:${i}`}
                      className="px-1 text-xs underline"
                      title="Set the base amount to foreign × rate"
                      onClick={() =>
                        setLine(i, {
                          // ⛔ THE MONEY PARSER FOR THE MONEY, THE RATE PARSER
                          // FOR THE RATE, AND THEY ARE NOT INTERCHANGEABLE.
                          // `parseFixed4` truncates the fifth decimal onward, so
                          // putting the rate through it would compute the base
                          // from 83.6512 instead of 83.6512345678 — a plausible
                          // figure, wrong for ever. That is the exact confusion
                          // api/rate.ts exists to make impossible.
                          amount: formatFixed4(
                            foreignToBase(
                              parseFixed4(l.foreignAmount ?? ""),
                              parseRate(l.rate ?? ""),
                            ),
                          ),
                        })
                      }
                    >
                      = fgn × rate
                    </button>
                  </>
                ) : null}
              </>
            ) : null}
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
                  /* ⭐ THE LEG'S OWN CURRENCY AND RATE, so a reference cannot
                     name a different one from the posting it belongs to. */
                  forex={
                    (l.currencyId ?? "") === ""
                      ? undefined
                      : {
                          currencyId: l.currencyId ?? "",
                          currencyCode: l.currencyCode ?? "",
                          rate: l.rate ?? "",
                        }
                  }
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
