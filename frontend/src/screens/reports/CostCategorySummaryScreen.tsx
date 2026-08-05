import { useEffect, useState } from "react";

import type { ScreenComponentProps } from "../../app/screenRegistry";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { COST_CATEGORY_ROW_PREFIX } from "../../keystroke/navMachine";
import { ApiError } from "../../api";
import type {
  CostCategorySummaryResponse,
  CostCategorySummaryRow,
} from "../../api";
import { formatMoney } from "./money";
import { DrillRows, type DrillRow } from "./DrillRows";
import { usePrintAction } from "../usePrint";

/**
 * THE COST CATEGORY SUMMARY (T2) — one row per AXIS, and the GAP.
 *
 * ⛔⛔ THERE IS NO GRAND TOTAL ON THIS REPORT AND THERE MUST NEVER BE ONE.
 *
 * Each row is a different AXIS through the SAME money. One Rs 1,00,000 salary is
 * allocated in full across Departments AND AGAIN in full across Branches; both
 * allocations are complete and both are correct. So a footer summing the
 * `allocated_debit` column would read 2,00,000 against 1,00,000 of cost actually
 * borne — twice the answer, from figures that are each individually right, on the
 * one report where the mistake is easiest to make because the rows are sitting in
 * a column. The response deliberately carries NO total field, and its `notes`
 * array says so in the server's own words; both are rendered below, because the
 * ABSENCE of a field only informs a programmer.
 *
 * ⛔ WHAT THE REPORT IS FOR: `unallocated_*`.
 *
 * It is cost that is in the Profit &amp; Loss and attached to NOTHING on this axis.
 * No database constraint can see it — the sum trigger fires on allocations, and
 * where there are none there is nothing to fire on — so an axis can be perfectly
 * consistent and still describe a fraction of the business. A screen that rendered
 * `allocated_*` without this beside it would answer a question nobody asked, so
 * here the gap gets its own emphasised column AND a sentence per row, and it is
 * never smaller on the page than what WAS allocated.
 *
 * FOUR MORE THINGS THIS SCREEN REFUSES TO DO
 *
 *  1. IT NEVER TOUCHES A NUMBER. Fixed-point strings, rendered through
 *     formatMoney (pure string work). No parseFloat, no Number(), no toFixed, no
 *     arithmetic — including no percentage: coverage is shown as the pair
 *     (allocated, allocable) it was served as, because dividing two money strings
 *     here would be the one place a float crept into this feature.
 *
 *  2. IT NEVER SHOWS A COVERAGE FIGURE WITHOUT ITS DENOMINATOR. Which postings
 *     an axis was measured AGAINST depends on `allocate_non_revenue`, so the base
 *     and that flag are printed beside the coverage. A percentage whose
 *     denominator is unstated is a percentage of nothing in particular.
 *
 *  3. IT RENDERS `partly_allocated_postings` AND `off_axis_*` LOUDLY. Both are
 *     non-zero ONLY when something is wrong: a part-allocated posting means the
 *     deferred trigger's guarantee is gone, and an off-axis allocation is on a
 *     ledger this axis may not carry — which the database does not enforce and
 *     only this report sees. They are not ordinary columns and are not rendered as
 *     ordinary columns.
 *
 *  4. IT TREATS A 403 AS A DECISION. Migration 0072 refuses
 *     `report:cost-category-summary` to the seeded Data Entry level, and it is a
 *     SEPARATE name from the centre summary's on purpose: chasing the unallocated
 *     gap is exactly the clerical job an owner delegates, so "a clerk may see WHICH
 *     analysis is incomplete but not what each department cost" has to be a
 *     writable rule.
 */
export function CostCategorySummaryScreen({
  snapshot,
  onSelect,
}: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();

  // F2 sets the period. BOTH bounds: this report measures a period's allocable
  // cost against what was allocated in it, which is a flow and not a position.
  const from = snapshot.config?.periodFrom;
  const to = snapshot.config?.periodTo;

  const [data, setData] = useState<CostCategorySummaryResponse | null>(null);
  const [error, setError] = useState("");
  const [denied, setDenied] = useState(false);

  useEffect(() => {
    let live = true;
    if (!companyID || !from || !to) {
      setData(null);
      setError("");
      setDenied(false);
      return;
    }
    void (async () => {
      try {
        const res = await client.costCategorySummary(companyID, from, to);
        if (!live) return;
        setData(res);
        setError("");
        setDenied(false);
      } catch (e) {
        if (!live) return;
        setData(null);
        // A 403 here is a DECISION — see (4).
        if (e instanceof ApiError && e.status === 403) {
          setDenied(true);
          setError("");
        } else {
          setDenied(false);
          setError(e instanceof Error ? e.message : String(e));
        }
      }
    })();
    return () => {
      live = false;
    };
  }, [client, companyID, from, to]);

  // Ctrl+P — no printed layout (the server answers 501). Registering the handler
  // is what turns a dead key into that sentence; see printTarget.ts.
  usePrintAction({ voucher: { voucherID: null } });

  if (denied) {
    return (
      <section
        data-testid="cost-category-summary-denied"
        className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field"
      >
        <h1 className="text-lg font-bold text-tally-title">
          Cost Category Summary
        </h1>
        <p className="mt-2 max-w-3xl text-sm">
          You do not have permission to read this report. That is a decision, not a
          fault: this level is not allowed to see how much of the company&apos;s
          cost each axis of analysis accounts for. Migration 0072 refuses it to the
          Data Entry level by default. An owner can grant it by adding an{" "}
          <span className="text-tally-title">
            allow display report:cost-category-summary
          </span>{" "}
          rule in Users &amp; Roles — and it is deliberately a SEPARATE rule from
          the Cost Centre Summary&apos;s, because chasing the unallocated gap is
          exactly the job an owner would delegate to somebody who may not see what
          each department cost.
        </p>
      </section>
    );
  }

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">
        Cost Category Summary
      </h1>
      <p className="mt-1 max-w-3xl text-xs">
        One row per AXIS of analysis, and how much of the period&apos;s allocable
        cost each one accounts for. The column that matters is UNALLOCATED: cost
        that is in the Profit &amp; Loss with nothing attached to it on that axis.
        Press Enter on a row to open that axis&apos;s Cost Centre Summary.
      </p>

      {!from || !to ? (
        <p
          data-testid="cost-category-summary-period-prompt"
          className="mt-3 max-w-3xl text-sm"
        >
          Press F2 to set the period. BOTH dates are needed and neither is
          defaulted: this report compares what COULD have been allocated in a period
          against what was, so a defaulted opening date would measure coverage over
          a range you did not choose and report a gap that belongs to a different
          question.
        </p>
      ) : error !== "" ? (
        <p
          data-testid="cost-category-summary-error"
          className="mt-3 max-w-3xl text-sm text-tally-bar"
        >
          {error}
        </p>
      ) : data === null ? (
        <p className="mt-3 text-sm">Loading…</p>
      ) : (
        <>
          <p className="mt-2 text-sm">
            {data.from} to {data.to}
          </p>

          {/* ---- THE NOTES. They travel WITH the numbers and carry the
                  no-total rule onto the artefact itself. A screen may style them;
                  it may not decide whether to show them. */}
          {data.notes.length > 0 ? (
            <ul
              data-testid="cost-category-summary-notes"
              className="mt-3 max-w-3xl list-disc border border-tally-fieldText p-2 pl-6 text-xs"
            >
              {data.notes.map((n) => (
                <li key={n} className="mt-1">
                  {n}
                </li>
              ))}
            </ul>
          ) : null}

          {data.rows.length === 0 ? (
            <p
              data-testid="cost-category-summary-empty"
              className="mt-3 max-w-3xl text-sm"
            >
              This company has no cost categories, so there is no axis to measure
              and no coverage to report. That is NOT a statement that the
              period&apos;s cost is fully analysed — it is a statement that none of
              it is. Create an axis on the Cost Centres screen (Gateway &gt; Cost
              Centres, then Alt+C).
            </p>
          ) : (
            <>
              {/* A CLICK SELECTS, ENTER OPENS. The row key carries the axis's NAME
                  and never its id (see COST_CATEGORY_ROW_PREFIX): the pure machine
                  has no database, and the Cost Centre Summary resolves the name
                  against its own served list. */}
              <div className="mt-3 max-w-6xl">
                <div className="grid grid-cols-[minmax(9rem,1.4fr)_repeat(4,minmax(6rem,1fr))] text-xs text-tally-title">
                  <div className="pr-3">Cost category (axis)</div>
                  <div className="pr-3 text-right">Allocable base</div>
                  <div className="pr-3 text-right">Allocated</div>
                  {/* THE POINT OF THE REPORT, and it is not a footnote. */}
                  <div className="pr-3 text-right font-bold">UNALLOCATED</div>
                  <div className="pr-3 text-right">Coverage</div>
                </div>
                <DrillRows
                  ariaLabel="Cost categories"
                  columnsClassName="grid-cols-[minmax(9rem,1.4fr)_repeat(4,minmax(6rem,1fr))]"
                  onSelect={onSelect}
                  rows={data.rows.map((r) => axisRow(r))}
                />
              </div>

              {/* ---- NO FOOTER TOTAL, AND THE ABSENCE IS STATED. --------- */}
              <p
                data-testid="cost-category-summary-no-total"
                className="mt-3 max-w-3xl border border-tally-fieldText p-2 text-xs"
              >
                <span className="text-tally-title">
                  These rows are not added up, and they must not be.
                </span>{" "}
                Every axis above allocates the SAME money, in full, from a different
                direction: one salary analysed by Department is the same rupees
                analysed by Branch. So a total down any of these columns is a
                multiple of the cost actually borne — for two axes, twice it — even
                though every figure in the column is correct. There is no field for
                such a total in the response and there is no row for it here.
              </p>

              {/* ---- PER-AXIS DETAIL: the notes, and the two faults. ------ */}
              {data.rows.map((r) => (
                <AxisDetail key={r.cost_category_id} row={r} />
              ))}

              <div className="mt-4 max-w-3xl text-xs">
                <p>
                  <span className="text-tally-title">
                    Does this report reconcile with itself?
                  </span>{" "}
                  <span data-testid="cost-category-summary-reconciles">
                    {data.reconciles ? "reconciles" : "DOES NOT RECONCILE"}
                  </span>
                  . That is the SERVER&apos;S verdict on each axis&apos;s own
                  arithmetic (allocable = allocated + unallocated, per axis), not a
                  check across axes — there is no such check, because there is no
                  such relationship.
                </p>
                <p className="mt-2">
                  A non-zero UNALLOCATED figure is not a bug and not necessarily a
                  mistake: an axis may be meant to cover only part of the business.
                  It is simply the one thing no constraint can tell you, which is
                  why it is the reason this report exists.
                </p>
              </div>
            </>
          )}
        </>
      )}
    </section>
  );
}

/** One axis as a drillable row. Enter opens its Cost Centre Summary. */
function axisRow(r: CostCategorySummaryRow): DrillRow {
  const flagged = r.partly_allocated_postings > 0 || r.off_axis_postings > 0;
  return {
    rowKey: `${COST_CATEGORY_ROW_PREFIX}${r.cost_category}`,
    values: [
      r.cost_category,
      r.allocable_debit,
      r.allocated_debit,
      r.unallocated_debit,
      `${r.allocated_postings}/${r.allocable_postings}`,
    ],
    cells: (
      <>
        <div className="pr-3" data-testid={`cost-category-row:${r.cost_category}`}>
          {r.cost_category}
          {flagged ? (
            <span className="ml-2 text-tally-bar">⚠</span>
          ) : null}
        </div>
        <div className="pr-3 text-right">{formatMoney(r.allocable_debit)}</div>
        <div
          className="pr-3 text-right"
          data-testid={`cost-category-allocated:${r.cost_category}`}
        >
          {formatMoney(r.allocated_debit)}
        </div>
        {/* THE GAP, in bold, in its own column, AT LEAST as prominent as what was
            allocated. `fully_allocated` is the server's verdict and is shown as
            words rather than inferred from a zero. */}
        <div
          className={`pr-3 text-right ${
            r.fully_allocated ? "" : "font-bold text-tally-bar"
          }`}
          data-testid={`cost-category-unallocated:${r.cost_category}`}
        >
          {formatMoney(r.unallocated_debit)}
        </div>
        <div className="pr-3 text-right">
          {/* THE PAIR, never a percentage: dividing two money strings would need
              the arithmetic this feature does not do in a browser. The denominator
              is on the row for the reason (2) in the header gives. */}
          {r.allocated_postings}/{r.allocable_postings} postings
        </div>
      </>
    ),
  };
}

/**
 * One axis's own sentences — the served notes, and the two faults that should be
 * impossible.
 *
 * These are per-ROW rather than pooled at the bottom because each names its own
 * axis, and a pooled list of "20,000 is unallocated" lines with no axis attached
 * is the shape that invites adding them together.
 */
function AxisDetail({ row: r }: { row: CostCategorySummaryRow }) {
  const flagged = r.partly_allocated_postings > 0 || r.off_axis_postings > 0;
  return (
    <div
      data-testid={`cost-category-detail:${r.cost_category}`}
      className="mt-3 max-w-3xl border border-tally-fieldText p-2 text-xs"
    >
      <h2 className="font-bold text-tally-title">{r.cost_category}</h2>
      <p className="mt-1">
        Measured against{" "}
        <span data-testid={`cost-category-base:${r.cost_category}`}>
          {formatMoney(r.allocable_debit)}
        </span>{" "}
        Dr / {formatMoney(r.allocable_credit)} Cr over {r.allocable_postings}{" "}
        posting(s) —{" "}
        {r.allocate_non_revenue
          ? "this axis MAY be used on balance-sheet ledgers, so the base includes them"
          : "revenue and expense postings only, because this axis may not carry balance-sheet ledgers"}
        . That flag IS the denominator: turning it on widens the base, and the same
        allocations then cover a smaller share of it.
      </p>
      <p className="mt-1">
        <span className="text-tally-title">Allocated</span>{" "}
        {formatMoney(r.allocated_debit)} Dr / {formatMoney(r.allocated_credit)} Cr
        over {r.allocated_postings} posting(s) ·{" "}
        <span
          className={r.fully_allocated ? undefined : "font-bold text-tally-bar"}
        >
          UNALLOCATED {formatMoney(r.unallocated_debit)} Dr /{" "}
          {formatMoney(r.unallocated_credit)} Cr over {r.unallocated_postings}{" "}
          posting(s)
        </span>{" "}
        ·{" "}
        {r.fully_allocated
          ? "every allocable posting carries an allocation on this axis"
          : "the gap is real cost with no analysis attached to it on this axis"}
      </p>

      {/* THE SERVER'S OWN SENTENCES about this axis, rendered verbatim. */}
      {r.notes.length > 0 ? (
        <ul
          data-testid={`cost-category-notes:${r.cost_category}`}
          className="mt-2 list-disc pl-5"
        >
          {r.notes.map((n) => (
            <li key={n} className="mt-1">
              {n}
            </li>
          ))}
        </ul>
      ) : null}

      {/* ---- THE TWO FAULTS THAT SHOULD BE IMPOSSIBLE — see (3). ---------- */}
      {flagged ? (
        <div
          data-testid={`cost-category-faults:${r.cost_category}`}
          className="mt-2 border border-tally-bar p-2 text-tally-bar"
        >
          <p className="font-bold">
            SOMETHING IS WRONG ON THIS AXIS, and neither fault is an ordinary
            column:
          </p>
          {r.partly_allocated_postings > 0 ? (
            <p className="mt-1">
              {r.partly_allocated_postings} posting(s) are PART-allocated, short by{" "}
              <span
                data-testid={`cost-category-shortfall:${r.cost_category}`}
              >
                {formatMoney(r.partly_allocated_shortfall)}
              </span>
              . A deferred constraint trigger forbids this: the allocations of one
              posting for one category must total that posting EXACTLY, to the
              paisa. A non-zero figure here means that guarantee is gone, so every
              other number on this axis is suspect — not merely incomplete.
            </p>
          ) : null}
          {r.off_axis_postings > 0 ? (
            <p className="mt-1">
              {r.off_axis_postings} posting(s) carry {formatMoney(r.off_axis_debit)}{" "}
              Dr / {formatMoney(r.off_axis_credit)} Cr of allocation on a ledger this
              axis may NOT be used on. The database does not enforce
              allocate_non_revenue — only this report sees it — so this is the only
              place the fault is visible at all. It also means the allocable base
              above and the allocations do not describe the same population.
            </p>
          ) : null}
        </div>
      ) : null}

      {/* The axis's own residual, published by the server so it cannot be hidden. */}
      <p className="mt-2">
        Residual on this axis: {formatMoney(r.difference_debit)} Dr /{" "}
        {formatMoney(r.difference_credit)} Cr ·{" "}
        <span
          className={r.reconciles ? undefined : "font-bold text-tally-bar"}
          data-testid={`cost-category-reconciles:${r.cost_category}`}
        >
          {r.reconciles ? "reconciles" : "DOES NOT RECONCILE"}
        </span>
      </p>
    </div>
  );
}
