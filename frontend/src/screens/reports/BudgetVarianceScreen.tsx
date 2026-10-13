import { useCallback, useEffect, useMemo, useState } from "react";

import type { ScreenComponentProps } from "../../app/screenRegistry";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import { ApiError } from "../../api";
import type {
  Budget,
  BudgetBasisTotal,
  BudgetDimension,
  BudgetStatus,
  BudgetVarianceResponse,
  BudgetVarianceRow,
  CostCategory,
} from "../../api";
import { formatMoney } from "./money";
import { SelectionList } from "../SelectionList";
import { usePrintAction } from "../usePrint";

/**
 * BUDGET VS ACTUAL (T11) — the only report in this product whose left-hand column
 * is not derived from a voucher.
 *
 * Every other statement here answers "what happened". This one sets that beside
 * "what was meant to happen", and the second column comes from a budget, which
 * POSTS NOTHING: no voucher, no posting, no balance effect, no hash chain. That is
 * exactly why it cannot be faked by keying journals — there is no second column
 * for any other report to compare an actual against.
 *
 * ⛔⛔ THE FIVE THINGS THIS SCREEN REFUSES TO DO. Every one of them is a way a
 * variance report lies while FOOTING PERFECTLY, which is the defect shape this
 * repository's register is full of.
 *
 *  1. IT NEVER DRAWS A GRAND TOTAL WITHOUT CHECKING `totals_are_addable`.
 *     A CLOSING_BALANCE budget is a POSITION as at the period end; a
 *     NETT_TRANSACTIONS budget is MOVEMENT within the period. Their sum is not a
 *     quantity. A plan legitimately holds both (a year-end receivables ceiling
 *     AND a travel spend cap), and when it does the server sends
 *     `totals_are_addable: false` with three ZERO grand totals — so a footer drawn
 *     unconditionally would read 0.00 and be believed, or, worse, a screen that
 *     added the subtotals itself would print the single most believable wrong
 *     number on the page. The per-basis subtotals are rendered instead, always,
 *     and the footer is drawn ONLY when the flag says it means something.
 *
 *  2. IT NEVER RENDERS A PERCENTAGE THE SERVER SENT AS `null`. `variance_percent`
 *     is null whenever there is no budget to be a percentage OF — every UNBUDGETED
 *     row. "no budget set" is the truth; a 0, a 100 or an infinity is a figure a
 *     reader will act on. The server also sends
 *     `variance_percent_absent_because`, which is rendered as the reason.
 *
 *  3. IT NEVER MERGES THE SEVEN STATUSES, and it never colours OVER red.
 *     ⛔ SPENDING more than budget is bad; EARNING more than budget is good; BOTH
 *     ARE `OVER`. The server refuses to say "favourable" because it would guess
 *     wrong for a whole class of ledgers, and it sends `nature` on every row so a
 *     reader can tell which. This screen therefore styles by nature-and-status
 *     TOGETHER (see {@link toneFor}) and labels every status in words. UNSPENT is
 *     never shown as underspend and NETTED_TO_ZERO is never shown as unspent:
 *     "nothing was posted" and "movement happened and cancelled" are different
 *     facts, and the second one usually means a reversal or a misposting.
 *
 *  4. IT NEVER MIXES `unbudgeted` INTO `rows`. Those are actuals no budget line
 *     covers — money leaving the business that no plan accounted for, and the rows
 *     a finance person reads first. They are NOT variances against the plan and
 *     are NOT in its totals, so they are rendered as their own section under their
 *     own heading with their own total. Folding them in would make a plan that was
 *     never set look like a plan that was exceeded.
 *
 *  5. IT NEVER TOUCHES A NUMBER. Every amount is a fixed-point STRING rendered
 *     through formatMoney (pure string work). There is no parseFloat, no Number(),
 *     no toFixed and no arithmetic on money in this file — not even to check a
 *     total. The subtotals and the addability verdict are the SERVER'S, shown
 *     rather than recomputed.
 *
 * AND TWO MORE THAT ARE ABOUT WHAT IT ASKS FOR RATHER THAN WHAT IT SHOWS:
 *
 *  6. IT NEVER GUESSES THE PLAN OR THE GRAIN. Both are required by the server and
 *     neither has a default. An empty report for a plan nobody chose would read as
 *     "nothing was planned and nothing was spent" — a real and reassuring claim
 *     about a company, and a lie. With one plan it opens that one (making an
 *     operator pick from a list of one is friction); with several it shows a
 *     picker.
 *
 *  7. IT NEVER NARROWS THE PERIOD, and F2 does not apply to it. The period is the
 *     PLAN'S OWN and is printed from the response. Comparing a full-year budget
 *     against four months of actuals needs a spread rule — straight-line, monthly,
 *     seasonal — that Tally makes the operator choose and that these books give no
 *     basis to invent. Apportioning silently by day count would be confidently
 *     wrong every October for any festival-weighted business. The server says so in
 *     `notes` and this screen renders it.
 *
 * IT TREATS A 403 AS A DECISION. Migration 0086 refuses
 * `report:budget-variance` to the seeded Data Entry level — this report carries
 * what the owner PLANNED, which no voucher contains, plus every line the business
 * is failing against. So a 403 is the NORMAL answer for that level and is rendered
 * as the decision it is, not as an error and not as an empty table.
 */

/** The three grains, with the question each one answers. */
const DIMENSIONS: { value: BudgetDimension; label: string; asks: string }[] = [
  {
    value: "GROUP",
    label: "By group",
    asks: "every ledger under a budgeted group, rolled up",
  },
  {
    value: "LEDGER",
    label: "By ledger",
    asks: "one budgeted ledger per row",
  },
  {
    value: "COST_CENTRE",
    label: "By cost centre",
    asks:
      "one budgeted centre per row, rolled up through its children — ONE axis only",
  },
];

/**
 * What each status MEANS, in words, on the row.
 *
 * ⛔ THE WORDS ARE THE POINT. A bare token tells a reader nothing, and the four
 * dangerous statuses are dangerous precisely because a reader assumes they mean
 * something simpler than they do.
 */
const STATUS_WORDS: Record<BudgetStatus, string> = {
  ON_TARGET: "on target — the actual equals the budget to the paisa",
  OVER: "over — more than planned on the budgeted side",
  UNDER: "under — some activity, less than the budget",
  UNSPENT:
    "UNSPENT — nothing at all was posted against this. Not a variance of zero: the whole budget is untouched, which is a fact about the plan or about what has been keyed",
  NETTED_TO_ZERO:
    "NETTED TO ZERO — movement happened on both sides and cancelled exactly. NOT untouched: look for a reversal or a misposting",
  CONTRA:
    "CONTRA — the actual is on the OPPOSITE side from the budget. Not “comfortably under”: usually a reversal, a refund, or a posting on the wrong side",
  UNBUDGETED:
    "UNBUDGETED — no budget line covers this. NOT 100% over budget: the plan is silent about it rather than exceeded",
};

/**
 * The visual tone for a row.
 *
 * ⛔⛔ NATURE AND STATUS TOGETHER, NEVER STATUS ALONE. `OVER` on an EXPENSE is bad
 * news; `OVER` on INCOME is the best news on the page. A screen that keyed colour
 * off the status would paint a record sales quarter in the colour of a crisis —
 * which is the interpretation the server deliberately refuses to make on the
 * client's behalf, sending `nature` instead.
 *
 * A cost centre has NO nature (it is an axis of analysis, not an account), so it
 * is treated as a cost — which is what a cost centre budget is.
 */
function toneFor(row: BudgetVarianceRow): "bad" | "good" | "warn" | "plain" {
  const earning = row.nature === "INCOME";
  switch (row.status) {
    case "OVER":
      return earning ? "good" : "bad";
    case "UNDER":
      return earning ? "bad" : "plain";
    case "UNSPENT":
    case "NETTED_TO_ZERO":
    case "CONTRA":
    case "UNBUDGETED":
      // ⚠️ ALL FOUR ARE `warn` AND NONE IS `plain`. Each says something happened
      // that the plan does not describe, and none of them is good or bad news on
      // its own — they are news that the report cannot be read at face value.
      return "warn";
    default:
      return "plain";
  }
}

const TONE_CLASS: Record<string, string> = {
  bad: "text-tally-bar",
  good: "text-tally-title",
  warn: "text-tally-bar",
  plain: "",
};

/**
 * The text one budget shows in the picker.
 *
 * ⛔ IT IS A FUNCTION AND NOT AN INLINE TEMPLATE because SelectionList selects by
 * STRING: the label built for the list and the label matched on selection must be
 * the same expression, or picking a plan silently selects nothing. Two copies of
 * a format string is exactly how that drifts.
 */
function budgetOptionLabel(p: Budget): string {
  const lines =
    p.line_count === 0
      ? "NO LINES YET — a report over it compares against nothing"
      : `${p.line_count} line(s)`;
  return `${p.name} · ${p.from_date} to ${p.to_date} · ${lines}`;
}

export function BudgetVarianceScreen({ snapshot }: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();
  void snapshot;

  const [plans, setPlans] = useState<Budget[] | null>(null);
  const [plansDenied, setPlansDenied] = useState(false);
  const [planID, setPlanID] = useState<string | null>(null);
  const [picking, setPicking] = useState(false);

  const [dimension, setDimension] = useState<BudgetDimension>("LEDGER");

  const [axes, setAxes] = useState<CostCategory[] | null>(null);
  const [axisID, setAxisID] = useState<string | null>(null);
  const [pickingAxis, setPickingAxis] = useState(false);

  const [data, setData] = useState<BudgetVarianceResponse | null>(null);
  const [error, setError] = useState("");
  const [denied, setDenied] = useState(false);
  const [busy, setBusy] = useState(false);

  // ---- The plans this company has. SERVED, so nothing here asks for a uuid.
  useEffect(() => {
    let live = true;
    client
      .listBudgets(companyID)
      .then((res) => {
        if (!live) return;
        setPlans(res.budgets);
        if (res.budgets.length === 1) {
          setPlanID(res.budgets[0]!.id);
        } else if (res.budgets.length > 1) {
          setPicking(true);
        }
      })
      .catch((e: unknown) => {
        if (!live) return;
        // ⛔ A 403 ON THE PLAN LIST IS THE *MASTERS* DECISION (the bare `budget`
        // token), and it is a DIFFERENT refusal from this report's. A level may
        // legitimately hold the report and be refused the master list, or the
        // reverse — so the two are never collapsed into one sentence.
        //
        // ⚠️ AND `plans` IS LEFT null RATHER THAN SET TO []. Setting it to [] would
        // make this screen state as a FACT that the company has no budgets, and
        // then send the operator to create one on a screen that refuses them for
        // the same token. "There are none" and "you may not see them" are
        // different sentences about different subjects.
        if (e instanceof ApiError && e.status === 403) {
          setPlansDenied(true);
          setError(
            "You are not permitted to read this company's budgets, so no plan can " +
              "be chosen and this report cannot be drawn. That is the BUDGET MASTER " +
              "decision (the `budget` token), not this report's — an owner can grant " +
              "it with an `allow display budget` rule in Users & Roles.",
          );
        } else {
          setPlans([]);
          setError(e instanceof Error ? e.message : String(e));
        }
      });
    return () => {
      live = false;
    };
  }, [client, companyID]);

  // ---- ⛔ THE REPORT'S OWN PERMISSION, PROBED WHEN THE PLAN LIST IS REFUSED.
  //
  // A level can be refused the budget MASTER (the bare `budget` token) and the
  // REPORT (`report:budget-variance`) independently, and the seeded Data Entry
  // level is refused BOTH. Without this probe such an operator sees only the
  // master's refusal — which is accurate, but implies that getting the `budget`
  // token would let them read this report. It would not, and sending someone to
  // ask an owner for the wrong permission is worse than saying nothing.
  //
  // ⚠️ IT SENDS AN EMPTY budget_id DELIBERATELY, and the two answers are the whole
  // point: the PDP runs BEFORE the handler, so a refused level gets 403 while a
  // permitted one gets 400 about the missing parameter. That is exactly the
  // discriminator this screen needs, and it costs one request that only ever runs
  // on the refused path.
  useEffect(() => {
    let live = true;
    if (!plansDenied) return;
    client
      .budgetVariance(companyID, "", dimension)
      .catch((e: unknown) => {
        if (!live) return;
        if (e instanceof ApiError && e.status === 403) setDenied(true);
        // A 400 means the report itself is PERMITTED and only the plan list is
        // not — the master-only sentence already on screen is then the whole and
        // correct story, so nothing more is said.
      });
    return () => {
      live = false;
    };
  }, [client, companyID, dimension, plansDenied]);

  // ---- The axes, needed only for the COST_CENTRE grain. Fetched lazily so a
  // ledger-level report is not blocked by a refusal on the cost masters.
  useEffect(() => {
    let live = true;
    if (dimension !== "COST_CENTRE" || axes !== null) return;
    client
      .listCostCategories(companyID)
      .then((res) => {
        if (!live) return;
        setAxes(res.cost_categories);
        if (res.cost_categories.length === 1) {
          setAxisID(res.cost_categories[0]!.id);
        } else if (res.cost_categories.length > 1) {
          setPickingAxis(true);
        }
      })
      .catch(() => {
        if (!live) return;
        // Left as [] here deliberately, unlike the plan list: the sentence rendered
        // for an empty axis list on this screen is about the COST_CENTRE grain being
        // unavailable, which is true either way, and the report itself will refuse
        // with the server's own message naming the axes it can see.
        setAxes([]);
      });
    return () => {
      live = false;
    };
  }, [axes, client, companyID, dimension]);

  // ---- The report. Nothing is requested until every fact the operator states is
  // known: a request missing one would be answered 400, or — worse — answered for
  // a question nobody asked.
  useEffect(() => {
    let live = true;
    if (!planID) {
      setData(null);
      return;
    }
    if (dimension === "COST_CENTRE" && !axisID) {
      setData(null);
      return;
    }
    setBusy(true);
    client
      .budgetVariance(
        companyID,
        planID,
        dimension,
        dimension === "COST_CENTRE" ? (axisID ?? undefined) : undefined,
      )
      .then((res) => {
        if (!live) return;
        setData(res);
        setError("");
        setDenied(false);
      })
      .catch((e: unknown) => {
        if (!live) return;
        setData(null);
        if (e instanceof ApiError && e.status === 403) {
          setDenied(true);
          setError("");
        } else {
          setDenied(false);
          setError(e instanceof Error ? e.message : String(e));
        }
      })
      .finally(() => {
        if (live) setBusy(false);
      });
    return () => {
      live = false;
    };
  }, [axisID, client, companyID, dimension, planID]);

  // Ctrl+P. There is no printed layout for this report and there is a REASON
  // rather than a backlog item: the screen legitimately suppresses a grand total
  // when a plan mixes bases and explains why in a note beside the gap, and paper
  // has no affordance for that — a footer is either there or it is not. The server
  // answers 501, and registering the handler is what turns a dead key into that
  // sentence.
  usePrintAction({ voucher: { voucherID: null } });

  const chosenPlan = useMemo(
    () => (plans ?? []).find((p) => p.id === planID) ?? null,
    [planID, plans],
  );

  // F4 — CHOOSE THE PLAN.
  useRegisterScreenAction(
    "selectBudget",
    useCallback((): string => {
      if (plansDenied) {
        return (
          "You are not permitted to read this company's budgets, so there is no " +
          "list to choose from. That is the `budget` master token, not this report."
        );
      }
      if (!plans || plans.length === 0) {
        return (
          "This company has no budgets, so there is nothing to compare against. " +
          "Create one on the Budgets screen — a variance report needs a plan, and " +
          "there is no default plan."
        );
      }
      if (plans.length === 1) {
        return `${plans[0]!.name} is this company's only budget.`;
      }
      if (denied) {
        return (
          "The plan cannot be changed while this report is refused: you are not " +
          "permitted to read it at this level, so there is nothing to redraw. That " +
          "is the report's own decision (report:budget-variance)."
        );
      }
      setPicking(true);
      return "Choose the budget this report compares against.";
    }, [denied, plans, plansDenied]),
  );

  return (
    <section data-testid="budget-variance" className="p-3 text-sm">
      <h1 className="text-base font-bold text-tally-title">Budget Variance</h1>

      {/* ---- THE REFUSAL, RENDERED AS A DECISION. */}
      {denied ? (
        <div
          data-testid="budget-variance-denied"
          className="mt-3 max-w-3xl border border-tally-bar p-3 text-xs"
        >
          <p className="font-bold text-tally-bar">
            You may not open the Budget Variance report at this security level.
          </p>
          <p className="mt-2">
            This is a DECISION and not a fault. A budget states what the owner
            INTENDS — the one figure in this product that no voucher contains — and
            this report sets it beside the actual, so one read gives the plan, every
            line the business is failing against, and (through a budget on a payroll
            group) the payroll total. Your level is refused{" "}
            <span className="text-tally-title">report:budget-variance</span>.
          </p>
          <p className="mt-2">
            PRINTING IS A SEPARATE PERMISSION from reading, and this level is
            refused both. An owner can grant either in Users &amp; Roles — note that
            granting the read means DELETING the disallow rather than adding an
            allow, because Disallow beats Allow at equal specificity.
          </p>
        </div>
      ) : null}

      {denied && plansDenied ? (
        <p
          data-testid="budget-variance-denied-both"
          className="mt-3 max-w-3xl text-xs"
        >
          ⚠️ BOTH permissions are refused at this level, and they are separate: the
          report above (<span className="text-tally-title">report:budget-variance</span>
          ) and the budget MASTER (<span className="text-tally-title">budget</span>),
          which is why the plan list is empty too. Granting one does NOT grant the
          other — an owner would have to grant both for this screen to work.
        </p>
      ) : null}

      {error !== "" && !denied ? (
        <p
          data-testid="budget-variance-error"
          className="mt-3 max-w-3xl border border-tally-bar p-2 text-xs text-tally-bar"
        >
          {error}
        </p>
      ) : null}

      {/* ---- THE GRAIN. Always visible, because the three answer different
          questions and a reader must be able to see which one is on screen. */}
      {!denied && !plansDenied ? (
        <div className="mt-3 max-w-3xl text-xs">
          <span className="text-tally-field">Grain: </span>
          {DIMENSIONS.map((d) => (
            <button
              key={d.value}
              type="button"
              data-testid={`budget-dimension:${d.value}`}
              onClick={() => setDimension(d.value)}
              className={
                d.value === dimension
                  ? "mr-3 font-bold text-tally-title underline"
                  : "mr-3"
              }
            >
              {d.label}
            </button>
          ))}
          <span className="ml-1 text-tally-field">
            — {DIMENSIONS.find((d) => d.value === dimension)?.asks}
          </span>
        </div>
      ) : null}

      {/* ---- THE PLAN PICKER. */}
      {picking && plans && plans.length > 0 ? (
        <div className="mt-3 max-w-2xl" data-testid="budget-picker">
          <p className="text-xs">
            Choose the budget to compare against. There is no default plan: an empty
            variance report would read as &ldquo;nothing was planned and nothing was
            spent&rdquo;, which is a claim about this company rather than about a
            missing selection.
          </p>
          <SelectionList
            label="SELECT A BUDGET"
            /* ⚠️ THE LINE COUNT AND THE PERIOD ARE IN THE OPTION TEXT. An EMPTY
               plan produces a page of unbudgeted actuals, which is
               indistinguishable from "the plan was never keyed" unless the
               operator is told BEFORE they pick it. And two plans can share every
               visible attribute but their period, so the dates are what make the
               list pickable at all. */
            options={plans.map((p) => budgetOptionLabel(p))}
            onSelect={(chosen) => {
              const match = plans.find((p) => budgetOptionLabel(p) === chosen);
              if (match) {
                setPlanID(match.id);
                setPicking(false);
              }
            }}
          />
        </div>
      ) : null}

      {/* ---- THE AXIS PICKER, for the cost-centre grain only. */}
      {dimension === "COST_CENTRE" && pickingAxis && axes && axes.length > 0 ? (
        <div className="mt-3 max-w-2xl" data-testid="budget-axis-picker">
          <p className="text-xs">
            Choose the cost category (the AXIS) this report is for. The same money is
            allocated in full on EVERY axis, so a report spanning two would total a
            multiple of the cost actually borne — which is why there is no
            &ldquo;all axes&rdquo; mode and no default.
          </p>
          <SelectionList
            label="SELECT A COST CATEGORY"
            options={axes.map((a) => a.name)}
            onSelect={(name) => {
              const match = axes.find((a) => a.name === name);
              if (match) {
                setAxisID(match.id);
                setPickingAxis(false);
              }
            }}
          />
        </div>
      ) : null}

      {dimension === "COST_CENTRE" && axes !== null && axes.length === 0 ? (
        <p
          data-testid="budget-no-axes"
          className="mt-3 max-w-3xl border border-tally-bar p-2 text-xs"
        >
          No cost category is readable at this level, so the cost-centre grain
          cannot be drawn. A cost centre sits on an axis and a report over one axis
          is the only kind that can be totalled — choose another grain, or ask an
          owner for the <span className="text-tally-title">cost-centre</span> token.
        </p>
      ) : null}

      {busy ? <p className="mt-3 text-xs">Loading…</p> : null}

      {data !== null ? (
        <>
          <p className="mt-3 max-w-3xl text-xs">
            <span className="text-tally-title">{data.budget_name}</span> ·{" "}
            {data.from_date} to {data.to_date} ·{" "}
            {DIMENSIONS.find((d) => d.value === data.dimension)?.label ??
              data.dimension}
            {data.cost_category_name !== "" ? (
              <> · axis: {data.cost_category_name}</>
            ) : null}
            {chosenPlan && chosenPlan.line_count === 0 ? (
              <span className="ml-2 text-tally-bar">
                — THIS PLAN HAS NO LINES
              </span>
            ) : null}
          </p>

          {/* ---- (4) THE PLAN. */}
          <VarianceTable
            testid="budget-variance-rows"
            heading="The plan"
            rows={data.rows}
            empty={
              "This budget has no lines at this grain, so there is nothing to " +
              "compare. That is the ABSENCE of a plan, not a plan that was met."
            }
          />

          {/* ---- (1) THE TOTALS. Per basis always; a grand total only if addable. */}
          <BasisTotals data={data} />

          {/* ---- (4) THE GAPS, in their own section with their own total. */}
          {data.unbudgeted.length > 0 ? (
            <VarianceTable
              testid="budget-variance-unbudgeted"
              heading={
                `Covered by no budget line (${data.unbudgeted_count}) — total ` +
                `${formatMoney(data.unbudgeted_actual)}`
              }
              subheading={
                "These are the plan's GAPS, not variances against it, so they are " +
                "NOT in the totals above. Money leaving the business that no plan " +
                "accounted for is usually the first thing worth chasing. The list is " +
                "complete — nothing is capped or sampled."
              }
              rows={data.unbudgeted}
              empty=""
            />
          ) : null}

          {/* ---- THE STATUS HEADLINE. */}
          {Object.keys(data.status_counts).length > 0 ? (
            <p
              data-testid="budget-status-counts"
              className="mt-3 max-w-3xl text-xs"
            >
              {(Object.entries(data.status_counts) as [BudgetStatus, number][])
                .map(([status, n]) => `${n} ${status}`)
                .join(" · ")}
            </p>
          ) : null}

          {/* ---- THE CAVEATS. Rendered verbatim; this screen styles them and does
              not decide whether to show them. */}
          {data.notes.length > 0 ? (
            <ul
              data-testid="budget-variance-notes"
              className="mt-3 max-w-3xl list-disc pl-5 text-xs"
            >
              {data.notes.map((n) => (
                <li key={n} className="mt-1">
                  {n}
                </li>
              ))}
            </ul>
          ) : null}
        </>
      ) : null}
    </section>
  );
}

/**
 * ⛔ (1) THE TOTALS BLOCK — the one piece of this screen that must never be
 * simplified into a single footer.
 *
 * The per-basis subtotals are ALWAYS rendered. The grand total is rendered ONLY
 * when the server says the bases are addable, and when they are not, the reason is
 * printed IN PLACE OF the footer rather than beside it — so there is no gap for a
 * reader to fill in with their own addition.
 */
function BasisTotals({ data }: { data: BudgetVarianceResponse }) {
  if (data.totals.length === 0) return null;
  return (
    <div data-testid="budget-variance-totals" className="mt-3 max-w-3xl text-xs">
      <table className="w-full">
        <thead>
          <tr className="text-left text-tally-field">
            <th>Basis</th>
            <th className="text-right">Lines</th>
            <th className="text-right">Budget</th>
            <th className="text-right">Actual</th>
            <th className="text-right">Variance</th>
          </tr>
        </thead>
        <tbody>
          {data.totals.map((t: BudgetBasisTotal) => (
            <tr key={t.basis} data-testid={`budget-total:${t.basis}`}>
              <td>{t.basis}</td>
              <td className="text-right">{t.rows}</td>
              <td className="text-right">{formatMoney(t.budget)}</td>
              <td className="text-right">{formatMoney(t.actual)}</td>
              <td className="text-right">{formatMoney(t.variance)}</td>
            </tr>
          ))}
          {data.totals_are_addable ? (
            <tr
              data-testid="budget-grand-total"
              className="border-t border-tally-bar font-bold"
            >
              <td>Total</td>
              <td />
              <td className="text-right">{formatMoney(data.total_budget)}</td>
              <td className="text-right">{formatMoney(data.total_actual)}</td>
              <td className="text-right">{formatMoney(data.total_variance)}</td>
            </tr>
          ) : null}
        </tbody>
      </table>
      {!data.totals_are_addable ? (
        <p
          data-testid="budget-no-grand-total"
          className="mt-2 border border-tally-bar p-2 text-tally-bar"
        >
          NO GRAND TOTAL, DELIBERATELY. This plan mixes both bases: a
          closing-balance budget is a POSITION as at {data.to_date} and a
          nett-transactions budget is MOVEMENT within the period. Adding them would
          not produce a quantity — so the subtotals above are the only totals this
          report has, and adding them together by hand is the same mistake with no
          caveat attached.
        </p>
      ) : null}
    </div>
  );
}

/** One table of variance rows. Used for the plan AND for the gaps. */
function VarianceTable({
  testid,
  heading,
  subheading,
  rows,
  empty,
}: {
  testid: string;
  heading: string;
  subheading?: string;
  rows: BudgetVarianceRow[];
  empty: string;
}) {
  return (
    <div className="mt-4 max-w-4xl" data-testid={testid}>
      <h2 className="font-bold text-tally-title">{heading}</h2>
      {subheading ? (
        <p className="mt-1 text-xs text-tally-field">{subheading}</p>
      ) : null}

      {rows.length === 0 ? (
        empty === "" ? null : (
          <p
            data-testid={`${testid}-empty`}
            className="mt-2 border border-tally-bar p-2 text-xs"
          >
            {empty}
          </p>
        )
      ) : (
        <table className="mt-2 w-full text-xs">
          <thead>
            <tr className="text-left text-tally-field">
              <th>Target</th>
              <th>Basis</th>
              <th className="text-right">Budget</th>
              <th className="text-right">Actual</th>
              <th className="text-right">Variance</th>
              <th className="text-right">%</th>
              <th>What it is</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const tone = toneFor(row);
              return (
                <tr
                  key={`${row.dimension}:${row.target_id}`}
                  data-testid={`budget-row:${row.target_name}`}
                >
                  <td className={TONE_CLASS[tone]}>
                    {row.target_name}
                    {row.cost_category_name ? (
                      // ⚠️ THE AXIS TRAVELS WITH A CENTRE'S NAME. "Mumbai" may
                      // legitimately be both a Branch and a Project, and a bare
                      // name invites a reader to attribute the figure to the wrong
                      // one.
                      <span className="ml-1 text-tally-field">
                        ({row.cost_category_name})
                      </span>
                    ) : null}
                    {row.nature ? (
                      <span className="ml-1 text-tally-field">
                        · {row.nature.toLowerCase()}
                      </span>
                    ) : null}
                  </td>
                  <td>{row.basis === "CLOSING_BALANCE" ? "closing" : "nett"}</td>
                  <td className="text-right">
                    {/* An UNBUDGETED row's budget is zero, and showing "0.00"
                        would read as a budget of nothing rather than as no budget
                        at all. */}
                    {row.status === "UNBUDGETED"
                      ? "no budget set"
                      : formatMoney(row.budget)}
                  </td>
                  <td className="text-right">
                    {formatMoney(row.actual)}
                    {/* ⛔ (3) THE RAW SIDES, SHOWN WHENEVER THE NETT HIDES THEM.
                        A nett of zero with movement on both sides is the case a
                        reader would otherwise take for an untouched budget. */}
                    {row.status === "NETTED_TO_ZERO" ? (
                      <span className="ml-1 text-tally-field">
                        (Dr {formatMoney(row.actual_debit)} / Cr{" "}
                        {formatMoney(row.actual_credit)})
                      </span>
                    ) : null}
                  </td>
                  <td className={`text-right ${TONE_CLASS[tone]}`}>
                    {formatMoney(row.variance)}
                  </td>
                  <td
                    className="text-right"
                    /* Its own testid so a probe can assert on the CELL rather
                       than on the row's text. The row also carries the words
                       "NOT 100% over budget" — the honest explanation of what
                       UNBUDGETED means — and a test scanning the whole row for a
                       digit-then-percent would match that sentence and fail on
                       correct code. Found exactly that way. */
                    data-testid={`budget-pct:${row.target_name}`}
                  >
                    {/* ⛔⛔ (2) null IS RENDERED AS WORDS AND NEVER AS A NUMBER. */}
                    {row.variance_percent === null
                      ? "—"
                      : `${row.variance_percent}%`}
                  </td>
                  <td className={TONE_CLASS[tone]}>
                    {STATUS_WORDS[row.status] ?? row.status}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}

      {/* The per-row caveats and the operator's own notes, below the table so the
          numbers stay readable but never dropped. */}
      {rows.some(
        (r) =>
          r.notes.length > 0 ||
          r.note !== "" ||
          r.variance_percent_absent_because,
      ) ? (
        <ul className="mt-2 list-disc pl-5 text-xs">
          {rows.map((r) =>
            [
              ...r.notes,
              ...(r.variance_percent_absent_because
                ? [
                    `No percentage for ${r.target_name}: ${r.variance_percent_absent_because}.`,
                  ]
                : []),
              ...(r.note !== "" ? [`${r.target_name} — note: ${r.note}`] : []),
            ].map((text) => (
              <li key={`${r.target_id}:${text}`} className="mt-1">
                <span className="text-tally-title">{r.target_name}</span> —{" "}
                {text.startsWith(r.target_name) ? text.slice(r.target_name.length + 3) : text}
              </li>
            )),
          )}
        </ul>
      ) : null}
    </div>
  );
}
