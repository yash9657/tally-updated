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
 * ⛔⛔ THE MODE SAYS WHAT THE VOUCHER USUALLY DOES — IT DOES NOT SAY WHICH
 * REFERENCE TYPES ARE AVAILABLE, AND CONFLATING THE TWO MADE A WHOLE DOCUMENT
 * CLASS UNPRINTABLE. The type selector offers all four types in BOTH modes, but
 * the open-bill list used to load only in `settle`. So on a CREDIT NOTE — mode
 * "raise", because it is keyed on the invoice screens — an operator who chose
 * "Agst Ref" got a picker whose ONLY option was the empty "— bill —", and there
 * was no way to type a name either. A credit note that names no invoice is
 * REFUSED BY THE PRINTER (CGST s.34 read with rule 53(1A): the original invoice's
 * number and date are required particulars), so a credit note keyed in this app
 * could never be printed at all. Driven: 422, with no reachable escape hatch.
 *
 * The list therefore loads whenever an AGAINST reference EXISTS on the panel, in
 * either mode, and the name is an INPUT with the open bills as suggestions rather
 * than a closed <select>. Free text is not a nicety here: the invoice a credit
 * note adjusts is very often NOT outstanding (it was paid, or it predates
 * bill-wise tracking), and a bill that nets to zero is not in the ageing report
 * at all. A closed list cannot name it, and the "keyed by hand" the denial
 * message has always promised did not exist.
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
      // ⛔ NO `as_on`, AND THAT IS NOW A DECISION RATHER THAN AN OMISSION (D86).
      // Since the ageing report stopped bounding an unnamed date at today, this
      // picker lists FUTURE-DATED open bills too — and it should. The question this
      // list answers is "which open bill is this payment for", and a customer paying
      // an advance or proforma invoice early is the ordinary case; excluding it would
      // make an allocable bill unallocable and push the operator onto ON ACCOUNT,
      // which is the balance nobody can chase later. A bill that exists and is unpaid
      // is a valid target whatever its date.
      // ⚠️ Passing today's date here to "restore" the old list would be the wrong
      // fix twice over: it would re-open the same disagreement with the ledger
      // balance, on a surface where the consequence is a mis-allocated receipt.
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
  // ⛔ NOT `mode === "settle"`. The list is needed wherever a SETTLEMENT is being
  // keyed, and a settlement is keyed on a raise-mode screen every time a credit
  // note adjusts an invoice — see the header. It stays lazy: a plain sale carries
  // one NEW reference and never touches the ageing report.
  const wantsOpenBills =
    mode === "settle" || refs.some((r) => r.refType === "AGAINST");
  const { bills, error, denied, loading } = useOpenBills(
    leg.ledgerId,
    kind,
    wantsOpenBills,
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

      {wantsOpenBills && loading ? (
        <span data-testid={`${id}-loading`}>reading open bills…</span>
      ) : null}
      {wantsOpenBills && denied ? (
        <span data-testid={`${id}-denied`} className="text-tally-field">
          This level may not read the ageing report, so the open bills cannot be
          listed. A reference can still be keyed by hand.
        </span>
      ) : null}
      {wantsOpenBills && error !== "" ? (
        <span data-testid={`${id}-error`} className="text-tally-bar">
          The open bills could not be read: {error}
        </span>
      ) : null}

      {/* The suggestions behind the name box. ONE list for every row: they are
          all references on the same leg, so they all settle bills of the same
          party. */}
      <datalist id={`${id}-openbills`} data-testid={`${id}-openbills`}>
        {bills.map((b) => (
          <option key={b.ref_name} value={b.ref_name}>
            {b.pending}
            {b.overdue_days > 0 ? `, ${b.overdue_days}d overdue` : ""}
          </option>
        ))}
      </datalist>

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
              /* ⛔ AN INPUT WITH SUGGESTIONS, NOT A CLOSED <select>. The list can
                 only ever hold bills that are still OUTSTANDING — a reference
                 that nets to zero is dropped by the ageing report — so a closed
                 list cannot name a PAID invoice, which is exactly what a credit
                 note usually adjusts. It also cannot name anything at all when
                 the caller's level may not read the report (the 403 below), and
                 that message has always promised hand-keying. */
              <input
                list={`${id}-openbills`}
                aria-label={`Bill settled by reference ${i + 1}`}
                data-testid={`${id}-bill:${i}`}
                className="w-44 bg-tally-field px-1 text-tally-fieldText outline-none"
                placeholder="bill / invoice no."
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
              />
            ) : (
              /* ⛔ NOT AN INPUT, AND DELIBERATELY SO. A reference that OPENS a
                 bill is named by the SERVER, from the number it allocates plus
                 the class and the financial year that make that number an
                 identity (see ledger.go's stampBillRefNames). None of it exists
                 yet. Offering a box here would invite a name that ties to no
                 voucher — and it would be permanent. */
              <span
                data-testid={`${id}-autoname:${i}`}
                className="text-tally-field"
                /* ⛔ THE TOOLTIP USED TO PROMISE "this voucher's number", and
                   that promise is now false in a way that matters: the server
                   names the bill "<class>/<financial year>/<number>", because a
                   bare number is allocated per voucher TYPE per YEAR and so
                   named one bill per party per year rather than one bill. An
                   operator told to expect "7" and shown "Sales/2026-27/7" on the
                   ageing report has been misled by this form. */
                title="Named on save after the voucher that opens it — class, financial year and number, e.g. Sales/2026-27/7. The number is allocated by the server, so none of it exists yet."
              >
                (named on save)
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

            {/* ⛔ THE PRICE OF FREE TEXT, PAID HERE. Outstandings matches a
                settlement on the NAME and ignores the type, so an AGAINST whose
                name matches nothing does not fail — it OPENS a second bill and
                leaves the original fully outstanding beside it, both figures
                wrong and the report still footing. The closed <select> made that
                impossible; an input makes it a typo. So the mismatch is SHOWN,
                and shown only when there is a list to be checked against: on a
                403, or before the fetch lands, silence is the honest answer. It
                is a NOTE and not a refusal, because naming an already-settled
                invoice is exactly what a credit note legitimately does. */}
            {isAgainst &&
            r.refName.trim() !== "" &&
            bills.length > 0 &&
            !bills.some((b) => b.ref_name === r.refName.trim()) ? (
              <span
                data-testid={`${id}-unlisted:${i}`}
                className="basis-full text-tally-bar"
              >
                “{r.refName.trim()}” is not among {label}’s open bills. If it is
                an invoice that is already settled — what a credit note usually
                adjusts — this is right. Otherwise check the name: a settlement
                that matches nothing OPENS a bill instead of closing one.
              </span>
            ) : null}
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
