import { useCallback, useEffect, useState } from "react";

import { useClient, useCompanyID } from "../../app/CompanyContext";
import { ApiError } from "../../api";
import type { OutstandingBill, OutstandingKind, RefType } from "../../api";
import { formatFixed4, parseFixed4 } from "./itemInvoice";
import {
  billAllocated,
  billRefusal,
  billRemainder,
  emptyBillRef,
  type BillBearingLeg,
  type DraftBillRef,
} from "./billAllocation";

/**
 * THE BILL-WISE REFERENCE PICKER — how an operator says which bill a voucher
 * raises or settles.
 *
 * ⛔⛔ WHAT IT FIXES: no screen on any voucher path had ever built a bill
 * reference, so a receivable keyed in a browser sat in Sundry Debtors and was
 * ABSENT from the Outstandings report. The seed script hand-wrote the demo's
 * allocations, which is the only reason the feature ever looked alive.
 *
 * ⛔ TWO MODES, AND THEY ARE OPPOSITE OBLIGATIONS.
 *
 *  - **raise** (Sales, Purchase) — the voucher OPENS a bill. The form defaults
 *    one NEW reference for the whole invoice, left UNNAMED so the server can
 *    stamp the number it allocates. This is the ordinary case and must cost the
 *    operator nothing; a due date is the only thing worth asking for, and it is
 *    the field the entire ageing report depends on.
 *  - **settle** (Receipt, Payment) — the voucher CLOSES one. The operator picks
 *    from the party's OPEN BILLS, which this component fetches. A settlement
 *    that does not name its bill is refused, because Outstandings matches on the
 *    NAME and a defaulted one would open a second bill rather than settle
 *    anything.
 *
 * ⚠️ IT IS RENDERED AGAINST A LEG THE CALLER NAMES, never one inferred here. On a
 * Sales invoice the bill-bearing leg is `party_ledger_id`; in SINGLE ENTRY the
 * party field is the BANK and the debtor is a particular row. Inferring it would
 * put the reference on the bank leg — which the server now refuses, but only
 * because the caller was made to be explicit. Same discipline, same reason, as
 * `buildCostAllocations`'s `primaryLegCount`.
 */

const REF_TYPE_LABEL: Record<RefType, string> = {
  NEW: "New Ref",
  AGAINST: "Agst Ref",
  ADVANCE: "Advance",
  ON_ACCOUNT: "On Account",
};

/** The party's open bills, for the settle mode's picker. */
function useOpenBills(
  ledgerId: string,
  kind: OutstandingKind,
  enabled: boolean,
): { bills: OutstandingBill[]; error: string; denied: boolean; loading: boolean } {
  const client = useClient();
  const companyID = useCompanyID();
  const [bills, setBills] = useState<OutstandingBill[]>([]);
  const [error, setError] = useState("");
  const [denied, setDenied] = useState(false);
  const [loading, setLoading] = useState(enabled);

  useEffect(() => {
    if (!enabled || ledgerId === "") {
      setBills([]);
      setLoading(false);
      return;
    }
    let live = true;
    setLoading(true);
    client
      .getOutstandings(companyID, { kind })
      .then((res) => {
        if (!live) return;
        // ⛔ MATCHED ON THE LEDGER ID, not the display name. A rename would
        // silently show a customer with several open bills as having none.
        const party = res.parties.find((p) => p.ledger_id === ledgerId);
        setBills(party?.bills ?? []);
        setError("");
        setDenied(false);
      })
      .catch((e: unknown) => {
        if (!live) return;
        setBills([]);
        // A 403 is a normal answer for a level that may not read the ageing
        // report, and it is not this form's business to shout about it.
        setDenied(e instanceof ApiError && e.status === 403);
        setError(
          e instanceof ApiError && e.status === 403
            ? ""
            : e instanceof Error
              ? e.message
              : String(e),
        );
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [client, companyID, ledgerId, kind, enabled]);

  return { bills, error, denied, loading };
}

export function BillAllocationPicker({
  leg,
  refs,
  mode,
  kind,
  label,
  onChange,
}: {
  leg: BillBearingLeg;
  refs: readonly DraftBillRef[];
  /** "raise" opens bills (Sales/Purchase); "settle" closes them (Receipt/Payment). */
  mode: "raise" | "settle";
  /** Which ageing report the open bills come from. Receivable for a customer. */
  kind: OutstandingKind;
  /** What to call this leg in labels, e.g. the party's name. */
  label: string;
  onChange: (refs: DraftBillRef[]) => void;
}) {
  const { bills, error, denied, loading } = useOpenBills(
    leg.ledgerId,
    kind,
    mode === "settle",
  );
  const refusal = billRefusal(leg, refs);
  const remainder = billRemainder(leg, refs);
  const id = "billrefs";

  const patch = useCallback(
    (idx: number, p: Partial<DraftBillRef>) =>
      onChange(refs.map((r, i) => (i === idx ? { ...r, ...p } : r))),
    [refs, onChange],
  );

  return (
    <div data-testid={id} className="mt-1 text-xs">
      <div className="text-tally-field">
        Bill-wise references for {label} ({formatFixed4(parseFixed4(leg.amount))}{" "}
        {leg.drCr})
      </div>

      {mode === "settle" && loading ? (
        <span data-testid={`${id}-loading`}>reading open bills…</span>
      ) : null}
      {mode === "settle" && denied ? (
        <span data-testid={`${id}-denied`} className="text-tally-field">
          This level may not read the ageing report, so the open bills cannot be
          listed. A reference can still be keyed by hand.
        </span>
      ) : null}
      {mode === "settle" && error !== "" ? (
        <span data-testid={`${id}-error`} className="text-tally-bar">
          The open bills could not be read: {error}
        </span>
      ) : null}

      {refs.map((r, i) => {
        const isAgainst = r.refType === "AGAINST";
        return (
          <div key={i} className="mt-0.5 flex flex-wrap items-center gap-1">
            <select
              aria-label={`Reference type ${i + 1}`}
              data-testid={`${id}-type:${i}`}
              className="bg-tally-field px-1 text-tally-fieldText"
              value={r.refType}
              onChange={(e) =>
                patch(i, {
                  refType: e.target.value as RefType,
                  // ⛔ THE NAME IS CLEARED WHEN THE TYPE LEAVES AGAINST. A name
                  // left over from a settlement would be stamped onto a NEW
                  // reference, silently merging this voucher into a bill it has
                  // nothing to do with.
                  refName: e.target.value === "AGAINST" ? r.refName : "",
                })
              }
            >
              {(Object.keys(REF_TYPE_LABEL) as RefType[]).map((t) => (
                <option key={t} value={t}>
                  {REF_TYPE_LABEL[t]}
                </option>
              ))}
            </select>

            {isAgainst ? (
              <select
                aria-label={`Bill settled by reference ${i + 1}`}
                data-testid={`${id}-bill:${i}`}
                className="bg-tally-field px-1 text-tally-fieldText"
                value={r.refName}
                onChange={(e) => {
                  const b = bills.find((x) => x.ref_name === e.target.value);
                  // ⛔ CAPPED AT WHAT IS LEFT ON THIS LINE, not the bill's whole
                  // pending. Pre-filling the full outstanding over-allocates
                  // every PARTIAL payment — which is most of them — and the
                  // operator's first sight of the feature is a refusal about
                  // money they never typed. Found by driving it: a 5,000 receipt
                  // against an 11,800 invoice pre-filled 11,800 and refused.
                  const left = parseFixed4(
                    billRemainder(leg, refs.filter((_, idx) => idx !== i)),
                  );
                  const owed = b ? parseFixed4(b.pending) : null;
                  patch(i, {
                    refName: e.target.value,
                    amount:
                      owed === null
                        ? r.amount
                        : formatFixed4(owed < left ? owed : left),
                    auto: false,
                  });
                }}
              >
                <option value="">— bill —</option>
                {bills.map((b) => (
                  <option key={b.ref_name} value={b.ref_name}>
                    {b.ref_name} ({b.pending}
                    {b.overdue_days > 0 ? `, ${b.overdue_days}d overdue` : ""})
                  </option>
                ))}
              </select>
            ) : (
              /* ⛔ NOT AN INPUT, AND DELIBERATELY SO. A reference that OPENS a
                 bill is named by the server with the number it allocates, which
                 does not exist yet. Offering a box here would invite a name that
                 ties to no voucher — and it would be permanent. */
              <span
                data-testid={`${id}-autoname:${i}`}
                className="text-tally-field"
                title="Named with this voucher's number when it is saved — the number is allocated by the server, so it does not exist yet."
              >
                (numbered on save)
              </span>
            )}

            <input
              aria-label={`Amount for reference ${i + 1}`}
              data-testid={`${id}-amount:${i}`}
              className="w-28 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
              value={r.amount}
              placeholder="0.0000"
              // ⛔ CLEARS `auto`. The moment an operator states an amount the
              // form must stop re-tracking the invoice total over the top of it.
              onChange={(e) => patch(i, { amount: e.target.value, auto: false })}
            />

            {/* A due date is what the ENTIRE ageing report runs on: a bill with
                none never becomes overdue and sits in the first slab forever.
                Only meaningful on a reference that opens a bill. */}
            {r.refType === "NEW" || r.refType === "ADVANCE" ? (
              <input
                type="date"
                aria-label={`Due date for reference ${i + 1}`}
                data-testid={`${id}-due:${i}`}
                className="bg-tally-field px-1 text-tally-fieldText outline-none"
                value={r.dueDate}
                onChange={(e) => patch(i, { dueDate: e.target.value })}
              />
            ) : null}

            <button
              type="button"
              data-testid={`${id}-rest:${i}`}
              aria-label={`Give the rest of this line to reference ${i + 1}`}
              className="underline"
              onClick={() => {
                // "The rest" EXCLUDING this row, so the button means what it says
                // rather than "the rest after counting what I already typed here".
                const others = refs.filter((_, idx) => idx !== i);
                patch(i, { amount: billRemainder(leg, others), auto: false });
              }}
            >
              rest
            </button>

            <button
              type="button"
              data-testid={`${id}-remove:${i}`}
              aria-label={`Remove reference ${i + 1}`}
              className="underline"
              onClick={() => onChange(refs.filter((_, idx) => idx !== i))}
            >
              ×
            </button>
          </div>
        );
      })}

      <button
        type="button"
        data-testid={`${id}-add`}
        aria-label="Add a bill reference"
        className="mt-0.5 underline"
        onClick={() =>
          onChange([...refs, emptyBillRef(mode === "settle" ? "AGAINST" : "NEW")])
        }
      >
        + reference
      </button>

      {refs.length > 0 ? (
        <p
          data-testid={`${id}-remainder`}
          className={parseFixed4(remainder) === 0n ? "text-tally-field" : "text-tally-bar"}
        >
          {parseFixed4(remainder) === 0n
            ? `referenced in full (${billAllocated(refs)})`
            : `${billAllocated(refs)} of ${formatFixed4(parseFixed4(leg.amount))} — ${remainder} left`}
        </p>
      ) : null}

      {refusal !== "" ? (
        /* A REFUSAL, not a warning. The server enforces sum-to-posting exactly,
           so sending a short reference costs the operator the whole form. */
        <p data-testid={`${id}-refusal`} className="mt-0.5 max-w-md text-tally-bar">
          {refusal}
        </p>
      ) : null}
    </div>
  );
}
