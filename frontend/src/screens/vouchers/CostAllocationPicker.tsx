import { useCallback, useEffect, useMemo, useState } from "react";

import { useClient, useCompanyID } from "../../app/CompanyContext";
import { ApiError } from "../../api";
import type { CostCategory, CostCentre } from "../../api";
import { formatFixed4, parseFixed4 } from "./itemInvoice";
import {
  allocationRefusal,
  axisRemainders,
  emptyAllocation,
  type AllocatableLine,
  type DraftAllocation,
} from "./costAllocation";

/**
 * THE COST ALLOCATION PICKER — how an operator splits one posting across cost
 * centres (T2, migration 0040).
 *
 * Until this existed, the F11 Features row for this feature said **`server`**: the
 * validation, the hashed payload entry, the row insert, both reports and the ACL
 * migration were all built and tested, `voucherReq.cost_allocations` was on the wire
 * and `CostAllocationInput` was in types.ts — and **no screen could construct one.**
 * Both cost reports answered 200 and were *correct*: they accurately reported that
 * nothing had been allocated, because nothing could be.
 *
 * ⛔⛔ THE THREE RULES THIS COMPONENT EXISTS TO OBEY.
 *
 *  1. **IT NEVER ASKS FOR, DISPLAYS, OR COUNTS A `posting_seq`.** It is a 0-based
 *     ordinal no screen in this product renders. The picker is attached to a posting
 *     ROW the operator is already looking at — a ledger name and an amount — and the
 *     ordinal is computed from that row's position by `buildCostAllocations`, with the
 *     single-entry primary-leg offset stated at the call site. If this screen ever
 *     renders that number, the feature is not built however green the suite is.
 *
 *  2. **THE REMAINDER IS PER AXIS, AND IT IS THE POINT OF THE FORM.** Several axes
 *     allocate the SAME money in parallel: one ₹1,00,000 salary is allocated in full
 *     across Departments *and again in full* across Branches, and both are complete
 *     and correct. So this renders one remainder per axis and there is deliberately
 *     **no combined figure** — a single "50% allocated" on a posting that is fully
 *     allocated twice over would be a lie in the most expensive direction.
 *
 *  3. **AN AXIS IS COMPLETE OR IT IS NOT.** A partial axis is refused by the server
 *     AND by a deferred database trigger, to the paisa, with no tolerance. So the
 *     shortfall is a REFUSAL shown before the send, not a warning — unlike the
 *     fulfilment picker, where under-linking is legitimate. The two components look
 *     alike and make opposite calls here, for stated reasons.
 *
 * ⚠️ WHERE IT DOES *NOT* APPEAR, and why that is not an inconsistency. An ITEM
 * INVOICE refuses allocations outright: its postings are generated inside the
 * server's transaction and index 0 is the PARTY leg, so a client-supplied ordinal
 * would attribute a departmental cost to a receivable. A fulfilment names an
 * INVENTORY line — the operator's own grid — which is why that one IS honoured there.
 */

/** The masters, loaded once and shared by every row on the voucher. */
function useCostMasters(): {
  axes: CostCategory[];
  centresByAxis: Map<string, CostCentre[]>;
  error: string;
  denied: boolean;
  loading: boolean;
} {
  const client = useClient();
  const companyID = useCompanyID();
  const [axes, setAxes] = useState<CostCategory[]>([]);
  const [centres, setCentres] = useState<CostCentre[]>([]);
  const [error, setError] = useState("");
  const [denied, setDenied] = useState(false);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let live = true;
    setLoading(true);
    client
      .listCostCentres(companyID)
      .then((res) => {
        if (!live) return;
        setAxes(res.cost_categories);
        setCentres(res.cost_centres);
        setError("");
        setDenied(false);
      })
      .catch((e: unknown) => {
        if (!live) return;
        setAxes([]);
        setCentres([]);
        // ⚠️ A 403 IS A NORMAL ANSWER AND NOT AN ERROR TO SHOUT ABOUT. The seeded
        // Data Entry level is refused the cost masters deliberately (creating an axis
        // decides how every future allocation is GROUPED). Such an operator can still
        // key the voucher — it simply saves with no analysis, which is the truth
        // rather than a fault.
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
  }, [client, companyID]);

  const centresByAxis = useMemo(() => {
    const m = new Map<string, CostCentre[]>();
    for (const c of centres) {
      const list = m.get(c.cost_category_id) ?? [];
      list.push(c);
      m.set(c.cost_category_id, list);
    }
    // ⛔ BY PATH, NOT BY NAME. Two centres on one axis can share a leaf name under
    // different parents, and the path is what tells them apart — which is also why
    // the option label is the path.
    for (const list of m.values()) {
      list.sort((a, b) => a.path.localeCompare(b.path));
    }
    return m;
  }, [centres]);

  return { axes, centresByAxis, error, denied, loading };
}

/**
 * The per-row picker, rendered inside the particulars list on the row the operator is
 * already looking at — not in a modal, because a separate panel would have to make
 * them re-identify the posting, and the only handle for that is the ordinal this
 * component refuses to show.
 */
export function CostAllocationPicker({
  line,
  lineIndex,
  onChange,
}: {
  line: AllocatableLine;
  /** The row's position in the particulars list. Labels and test ids ONLY. */
  lineIndex: number;
  onChange: (allocations: DraftAllocation[]) => void;
}) {
  const { axes, centresByAxis, error, denied, loading } = useCostMasters();
  const links = line.allocations;
  const remainders = axisRemainders(line);
  const refusal = allocationRefusal(line);

  const patch = useCallback(
    (idx: number, p: Partial<DraftAllocation>) =>
      onChange(links.map((a, i) => (i === idx ? { ...a, ...p } : a))),
    [links, onChange],
  );

  const id = `alloc:${lineIndex}`;

  // A company with no axes cannot allocate anything, and that is a statement about
  // its MASTERS rather than about this voucher. Saying so beats an empty control.
  if (denied) {
    return (
      <span data-testid={`${id}-denied`} className="text-xs text-tally-field">
        no cost access
      </span>
    );
  }
  if (loading) {
    return (
      <span data-testid={`${id}-loading`} className="text-xs">
        …
      </span>
    );
  }
  if (axes.length === 0) {
    return (
      <span
        data-testid={`${id}-noaxes`}
        className="text-xs text-tally-field"
        title={
          error !== ""
            ? `The cost masters could not be read: ${error}`
            : "This company has no cost categories, so there is no axis to analyse this cost on. Create one from Gateway > Cost Centres."
        }
      >
        {error !== "" ? "cost masters unreadable" : "no axis"}
      </span>
    );
  }

  return (
    <div data-testid={id} className="text-xs">
      {links.length === 0 ? (
        <button
          type="button"
          data-testid={`${id}-add`}
          aria-label={`Allocate particular ${lineIndex + 1} to cost centres`}
          className="underline"
          onClick={() => onChange([...links, emptyAllocation()])}
        >
          allocate
        </button>
      ) : null}

      {links.map((a, i) => {
        const centres = centresByAxis.get(a.costCategoryId) ?? [];
        return (
          <div key={i} className="mt-0.5 flex flex-wrap items-center gap-1">
            {/* THE AXIS — picked from the served list. */}
            <select
              aria-label={`Cost category for particular ${lineIndex + 1}, split ${i + 1}`}
              data-testid={`${id}-axis:${i}`}
              className="bg-tally-field px-1 text-tally-fieldText"
              value={a.costCategoryId}
              onChange={(e) => {
                const axis = axes.find((x) => x.id === e.target.value);
                patch(i, {
                  costCategoryId: e.target.value,
                  categoryName: axis?.name ?? "",
                  // ⛔ THE CENTRE IS CLEARED WITH THE AXIS. A centre from the previous
                  // axis is refused with a 422 ("one axis would roll up into the
                  // other and the Category Summary would double-count"), and keeping
                  // it would leave a stale name in a picker whose list just changed.
                  costCentreId: "",
                  centrePath: "",
                });
              }}
            >
              <option value="">— axis —</option>
              {axes.map((x) => (
                <option key={x.id} value={x.id}>
                  {x.name}
                </option>
              ))}
            </select>

            {/* THE CENTRE — only this axis's, by PATH. */}
            <select
              aria-label={`Cost centre for particular ${lineIndex + 1}, split ${i + 1}`}
              data-testid={`${id}-centre:${i}`}
              className="bg-tally-field px-1 text-tally-fieldText"
              value={a.costCentreId}
              disabled={a.costCategoryId === ""}
              onChange={(e) => {
                const c = centres.find((x) => x.id === e.target.value);
                patch(i, {
                  costCentreId: e.target.value,
                  centrePath: c?.path ?? "",
                });
              }}
            >
              <option value="">— centre —</option>
              {/* ⛔ ONLY THIS AXIS'S CENTRES. Offering another axis's would be
                  offering the operator a mistake the server refuses. */}
              {centres.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.path}
                </option>
              ))}
            </select>

            <input
              aria-label={`Amount for particular ${lineIndex + 1}, split ${i + 1}`}
              data-testid={`${id}-amount:${i}`}
              className="w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
              value={a.amount}
              placeholder="0.0000"
              onChange={(e) => patch(i, { amount: e.target.value })}
            />

            {/* THE AFFORDANCE: take up this axis's remaining balance. A BUTTON, not a
                default — see emptyAllocation. It is what makes the ordinary
                "everything to one centre" case one click while leaving the split case
                the natural thing to key. */}
            {a.costCategoryId !== "" ? (
              <button
                type="button"
                data-testid={`${id}-rest:${i}`}
                aria-label={`Give the rest of this axis to particular ${lineIndex + 1}, split ${i + 1}`}
                className="underline"
                onClick={() => {
                  const r = axisRemainders({
                    ...line,
                    // Exclude THIS split, so the button means "the rest" rather than
                    // "the rest after counting what I already typed here".
                    allocations: links.filter((_, idx) => idx !== i),
                  }).find((x) => x.costCategoryId === a.costCategoryId);
                  // ⚠️ CANONICALISED ON BOTH BRANCHES. With no other split on this
                  // axis there is no remainder to read, and the whole posting is the
                  // answer — but `line.amount` is the operator's RAW keystrokes
                  // ("1000"), while a remainder is already fixed-4 ("1000.0000").
                  // Writing the raw string here would put two different spellings of
                  // one number into the same field depending on which path filled it,
                  // and the field is compared against a paisa-exact invariant.
                  patch(i, {
                    amount: r
                      ? r.remainder
                      : formatFixed4(parseFixed4(line.amount)),
                  });
                }}
              >
                rest
              </button>
            ) : null}

            <button
              type="button"
              data-testid={`${id}-remove:${i}`}
              aria-label={`Remove split ${i + 1} on particular ${lineIndex + 1}`}
              className="underline"
              onClick={() => onChange(links.filter((_, idx) => idx !== i))}
            >
              ×
            </button>
          </div>
        );
      })}

      {links.length > 0 ? (
        <button
          type="button"
          data-testid={`${id}-more`}
          aria-label={`Add another split on particular ${lineIndex + 1}`}
          className="mt-0.5 underline"
          onClick={() => onChange([...links, emptyAllocation()])}
        >
          + split
        </button>
      ) : null}

      {/* ⛔ ONE ROW PER AXIS, AND NO COMBINED TOTAL — rule 2. */}
      {remainders.length > 0 ? (
        <ul data-testid={`${id}-remainders`} className="mt-0.5 list-none">
          {remainders.map((r) => (
            <li
              key={r.costCategoryId}
              data-testid={`${id}-remainder:${r.categoryName}`}
              className={r.complete ? "text-tally-field" : "text-tally-bar"}
            >
              {r.categoryName || "(axis)"}:{" "}
              {r.complete
                ? `allocated in full (${r.allocated})`
                : `${r.allocated} of ${line.amount || "0.0000"} — ${r.remainder} left`}
            </li>
          ))}
        </ul>
      ) : null}

      {refusal !== "" ? (
        /* A REFUSAL, not a warning — rule 3. The server and a deferred DB trigger
           both refuse a partial axis to the paisa, so saying it here saves the
           operator a 422 that would cost them the form. */
        <p data-testid={`${id}-refusal`} className="mt-0.5 max-w-md text-tally-bar">
          {refusal}
        </p>
      ) : null}

      {remainders.length > 1 ? (
        <p data-testid={`${id}-parallel`} className="mt-0.5 max-w-md text-tally-field">
          This posting is analysed on {remainders.length} axes IN PARALLEL. Each one
          must total the posting on its own — the same money is described twice from
          different directions, so the two are never added together.
        </p>
      ) : null}
    </div>
  );
}
