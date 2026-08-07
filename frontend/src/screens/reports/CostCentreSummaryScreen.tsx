import { useCallback, useEffect, useMemo, useState } from "react";

import type { ScreenComponentProps } from "../../app/screenRegistry";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import { COST_CATEGORY_ROW_PREFIX } from "../../keystroke/navMachine";
import { ApiError } from "../../api";
import type {
  CostCategory,
  CostCentreNode,
  CostCentreSummaryResponse,
} from "../../api";
import { formatMoney } from "./money";
import { useExportAction } from "../../export/useExportAction";
import {
  moneyCell,
  textCell,
  type ExportRow,
  type ExportSheet,
} from "../../export/sheet";
import { SelectionList } from "../SelectionList";
import { usePrintAction } from "../usePrint";

/**
 * THE COST CENTRE SUMMARY (T2) — what each centre on ONE axis cost over a period.
 *
 * ⛔⛔ ONE AXIS, ALWAYS, AND THERE IS NO "ALL CENTRES" MODE.
 *
 * A cost CATEGORY is an AXIS of analysis (Department, Branch, Project) and a cost
 * CENTRE is a POSITION on it. Several axes allocate the SAME money in parallel:
 * one Rs 1,00,000 salary is allocated in full across Departments (Sales 60,000 /
 * Admin 40,000) AND AGAIN in full across Branches (Mumbai 70,000 / Delhi 30,000).
 * Both allocations are complete and both are correct. So a report that put centres
 * from two axes in one column would show 2,00,000 of cost against 1,00,000 borne,
 * and every figure in it would be individually right.
 *
 * The API makes that impossible — `costCategorySummary` refuses without an axis —
 * and this screen carries the same rule into the UI: the axis is CHOSEN (F4, or a
 * picker on the way in, or drilled from the Cost Category Summary), it is printed
 * in the heading FROM THE RESPONSE, and `total_*` is labelled as this axis's total
 * and nothing else. The server's `warnings` array says the same thing in its own
 * words and is rendered verbatim.
 *
 * WHAT THIS SCREEN REFUSES TO DO
 *
 *  1. IT NEVER TOUCHES A NUMBER. Every amount is a fixed-point STRING, rendered
 *     through formatMoney (pure string work). There is no parseFloat, no Number(),
 *     no toFixed and no arithmetic on money in this file — not even to check that
 *     the roll-up adds up. `reconciles`, `difference_debit` and
 *     `difference_credit` are the SERVER'S verdict on that, published so it cannot
 *     be hidden, and they are shown rather than recomputed.
 *
 *  2. IT NEVER SHOWS `total_*` ALONE. Every row carries its OWN allocations
 *     beside the TOTAL rolled up through its children, because NEITHER IS
 *     DERIVABLE FROM THE OTHER: a parent may hold allocations of its own AND have
 *     children. Only the pair tells an operator whether "Sales 60,000" was keyed
 *     against Sales itself or against its sub-teams — and rendering only the total
 *     is the mistake api/types.ts names as the common one.
 *
 *  3. IT NEVER DROPS `unattached`. Those are centres the roll-up could not place
 *     (a parent chain that loops, one deeper than the walk's bound, a parent
 *     outside the fact set). A centre that silently vanished from a cost report
 *     would take its money with it AND THE TOTALS WOULD STILL LOOK CONSISTENT,
 *     which is the worst shape a wrong report can have. They are rendered loudly
 *     whenever the array is non-empty.
 *
 *  4. IT NEVER DECIDES WHETHER TO SHOW A WARNING. `warnings` travels WITH the
 *     numbers. This screen styles them; it does not filter them.
 *
 *  5. IT NEVER DEFAULTS THE PERIOD. Both bounds are required (F2 sets them), and
 *     a cost summary is a FLOW between two dates rather than a position as at one:
 *     "what did Sales cost as at 31 March" is not a question, and answering it
 *     with everything since the company was formed would look exactly like a
 *     year's figure. FrameOverlays classifies this screen as FULL PERIOD so the F2
 *     panel says the same.
 *
 *  6. IT NEVER GUESSES THE AXIS. With more than one axis and none chosen it shows
 *     a picker; with exactly one it opens that one (making an operator pick from a
 *     list of one is friction, and one axis is the common case at first). A
 *     defaulted axis would render a real, correct Department report under a
 *     question that was about Branches.
 *
 *  7. IT TREATS A 403 AS A DECISION. Migration 0072 refuses
 *     `report:cost-centre-summary` to the seeded Data Entry level — this report is
 *     the company's cost structure stated outright, a sharper disclosure than the
 *     Profit &amp; Loss, which shows the same money with the analysis removed. So a
 *     403 is the NORMAL answer for that level and is rendered as the decision it
 *     is, not as an error and not as an empty table.
 */

/** Flatten the served tree for rendering, preserving parent-before-child order. */
function flatten(
  nodes: CostCentreNode[],
  depth = 0,
): { node: CostCentreNode; depth: number }[] {
  const out: { node: CostCentreNode; depth: number }[] = [];
  for (const node of nodes) {
    out.push({ node, depth });
    out.push(...flatten(node.children, depth + 1));
  }
  return out;
}

export function CostCentreSummaryScreen({ snapshot }: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();

  // F2 sets the period. BOTH bounds — see (5).
  const from = snapshot.config?.periodFrom;
  const to = snapshot.config?.periodTo;

  /**
   * The axis NAME the frame was drilled with, if any.
   *
   * The Cost Category Summary publishes `cost-category:<name>` as its row key and
   * Enter pushes this screen with it. The pure machine can only carry a name — it
   * has no database — so the name is resolved against the SERVED axis list here,
   * exactly as the Bank Reconciliation screen resolves a drilled ledger name. No
   * UUID ever travels through a frame.
   */
  const drilledName = useMemo(() => {
    const detail = snapshot.detailOf ?? "";
    return detail.startsWith(COST_CATEGORY_ROW_PREFIX)
      ? detail.slice(COST_CATEGORY_ROW_PREFIX.length)
      : null;
  }, [snapshot.detailOf]);

  const [axes, setAxes] = useState<CostCategory[] | null>(null);
  const [axisID, setAxisID] = useState<string | null>(null);
  const [picking, setPicking] = useState(false);
  const [data, setData] = useState<CostCentreSummaryResponse | null>(null);
  const [error, setError] = useState("");
  const [denied, setDenied] = useState(false);
  // ⛔ SEPARATE FROM `axes.length === 0`, and the distinction is the whole point.
  // A 403 on the axis list used to set axes to [], which made this screen state as a
  // FACT that the company has no cost categories — and then send the operator to
  // create one on a screen that refuses them for the SAME token. "There are none"
  // and "you may not see them" are different sentences about different subjects,
  // and only one of them is about the books.
  const [axesDenied, setAxesDenied] = useState(false);
  const [busy, setBusy] = useState(false);

  // ---- The axes this company has. SERVED, so nothing here asks for an id.
  useEffect(() => {
    let live = true;
    client
      .listCostCategories(companyID)
      .then((res) => {
        if (!live) return;
        const found = res.cost_categories;
        setAxes(found);
        if (drilledName !== null) {
          const match = found.find((a) => a.name === drilledName);
          if (match) {
            setAxisID(match.id);
          } else {
            // The drill named an axis that is no longer served (renamed, or
            // deleted between the two reads). Stated, never silently replaced with
            // another axis — that is the one substitution this report must not make.
            setError(
              `“${drilledName}” is not a cost category on this company any more, so ` +
                "no summary was drawn for it. Nothing was substituted: another " +
                "axis's figures under this heading would be a correct report " +
                "answering a different question. Press F4 to choose an axis.",
            );
            setPicking(found.length > 0);
          }
        } else if (found.length === 1) {
          setAxisID(found[0]!.id);
        } else if (found.length > 1) {
          setPicking(true);
        }
      })
      .catch((e: unknown) => {
        if (!live) return;
        // A 403 on the AXIS list is the masters' own decision (the bare
        // `cost-centre` token), and it is a different refusal from the report's:
        // a level may be allowed this report and refused the masters behind it.
        if (e instanceof ApiError && e.status === 403) {
          // NOT setAxes([]) — see axesDenied. Leaving it null keeps the
          // "this company has no cost categories" sentence off the page, because
          // this screen does not know whether it has any.
          setAxesDenied(true);
          setError(
            "You are not permitted to read this company's cost categories, so no " +
              "axis can be chosen and this report cannot be drawn. That is the COST " +
              "MASTERS decision (the `cost-centre` token), not this report's — an " +
              "owner can grant it with an `allow cost-centre` rule in Users & Roles.",
          );
        } else {
          setAxes([]);
          setError(e instanceof Error ? e.message : String(e));
        }
      });
    return () => {
      live = false;
    };
  }, [client, companyID, drilledName]);

  // ---- The report itself. Nothing is requested until the axis AND both dates
  // are known: each is a fact the operator states, and a request missing one
  // would be answered 400 or (worse) answered for a question nobody asked.
  useEffect(() => {
    let live = true;
    if (!axisID || !from || !to) {
      setData(null);
      return;
    }
    setBusy(true);
    client
      .costCentreSummary(companyID, axisID, from, to)
      .then((res) => {
        if (!live) return;
        setData(res);
        setError("");
        setDenied(false);
      })
      .catch((e: unknown) => {
        if (!live) return;
        setData(null);
        // A 403 here is a DECISION — see (7).
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
  }, [axisID, client, companyID, from, to]);

  // Ctrl+P. There is no printed layout for this report — the print route is a flat
  // literal that carries no axis, so the server answers 501 — and registering the
  // handler is what turns a dead key into that sentence. See printTarget.ts.
  usePrintAction({ voucher: { voucherID: null } });

  // F4 — CHOOSE THE AXIS. Provenance of the combo is on the `selectCostCategory`
  // action in keystroke/types.ts: it is OURS, placed where RESEARCH.md:879 puts the
  // analogous account picker, and scoped to this screen.
  useRegisterScreenAction(
    "selectCostCategory",
    useCallback((): string => {
      if (!axes || axes.length === 0) {
        return (
          "This company has no cost categories, so there is no axis to report on. " +
          "Create one on the Cost Centres screen (Gateway > Cost Centres, Alt+C) — " +
          "a cost centre cannot exist without an axis to sit on."
        );
      }
      if (axes.length === 1) {
        return `${axes[0]!.name} is this company's only cost category.`;
      }
      // ⛔ THE DENIED PAGE RETURNS EARLY, BEFORE THE PICKER CAN RENDER. Setting
      // picking here and reporting success would put a sentence in the status line
      // announcing a chooser that cannot appear — the operator presses F4, is told
      // it worked, and nothing happens. Say what is actually true instead.
      if (denied) {
        return (
          "The axis cannot be changed while this report is refused: you are not " +
          "permitted to read it at this level, so there is nothing to redraw. " +
          "That is the report's own decision (report:cost-centre-summary)."
        );
      }
      setPicking(true);
      return "Choose the cost category (the axis) this report is for.";
    }, [axes, denied]),
  );

  // Alt+E. This screen owns its own table (the axis-scoped roll-up is a shape no
  // generic report screen renders), so Export was dead on it.
  //
  // ⛔ THE AXIS TRAVELS IN THE TITLE. Several axes allocate the SAME money in
  // parallel, so a file called "Cost Centre Summary.csv" with no axis on it is
  // a set of figures whose subject cannot be recovered — and adding two such
  // files together double-counts the business. The axis name is in the file
  // name and in the title, not only on the screen it came from.
  useExportAction(
    useCallback(() => {
      if (denied) {
        return "You are not permitted to read this report (report:cost-centre-summary), so there is nothing to export.";
      }
      if (!axisID) {
        return "Choose the cost category (the axis) with F4 first — every figure on this report is scoped to one axis, so there is nothing to export until one is chosen.";
      }
      if (!from || !to) {
        return "Set the period with F2 first — this report is a flow over a range, so it has no rows until both dates are given.";
      }
      if (error !== "") return `Cost Centre Summary unavailable, so there is nothing to export. ${error}`;
      if (data === null) return "Cost Centre Summary is still loading — nothing to export yet.";
      if (data.centres.length === 0 && data.unattached.length === 0) {
        return "This axis has no cost centres with figures in the period, so there is nothing to export.";
      }
      return costCentreSummarySheet(data);
    }, [axisID, data, denied, error, from, to]),
  );

  if (denied) {
    return (
      <section
        data-testid="cost-centre-summary-denied"
        className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field"
      >
        <h1 className="text-lg font-bold text-tally-title">
          Cost Centre Summary
        </h1>
        <p className="mt-2 max-w-3xl text-sm">
          You do not have permission to read this report. That is a decision, not a
          fault: this level is not allowed to see departmental profitability. The
          report states the company&apos;s cost structure outright — which team is
          expensive, which branch loses money — and it is a sharper disclosure than
          the Profit &amp; Loss, which shows the same money with the analysis
          removed. So migration 0072 refuses it to the Data Entry level by default.
          An owner can grant it by adding an{" "}
          <span className="text-tally-title">
            allow display report:cost-centre-summary
          </span>{" "}
          rule in Users &amp; Roles. Note that it is a SEPARATE rule from the Cost
          Category Summary&apos;s, on purpose: &quot;may see WHICH analysis is
          incomplete but not what each department cost&quot; is a real
          configuration.
        </p>
      </section>
    );
  }

  // ---- THE AXIS PICKER. Shown rather than guessed — see (6).
  if (picking && axes && axes.length > 0) {
    return (
      <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
        <h1 className="mb-2 text-center text-lg font-bold text-tally-title">
          Cost Centre Summary
        </h1>
        <p className="mx-auto mb-3 max-w-xl text-center text-xs">
          Choose ONE axis. Centres on different axes describe the SAME money from
          different directions — a salary allocated across Departments is the same
          rupees allocated across Branches — so there is no &quot;all centres&quot;
          view, and no figure on this report ever spans two axes.
        </p>
        <div className="mx-auto w-96 bg-tally-panel p-3">
          <SelectionList
            label="SELECT A COST CATEGORY"
            options={axes.map((a) => a.name)}
            onSelect={(name) => {
              const match = axes.find((a) => a.name === name);
              if (match) {
                setAxisID(match.id);
                setPicking(false);
                setError("");
              }
            }}
            autoFocus
          />
        </div>
        <p className="mt-3 text-center text-xs">
          F4 reopens this list. Esc goes back.
        </p>
        {error !== "" ? (
          <p
            data-testid="cost-centre-summary-error"
            className="mx-auto mt-3 max-w-3xl text-sm text-tally-bar"
          >
            {error}
          </p>
        ) : null}
      </section>
    );
  }

  const chosenAxis = axes?.find((a) => a.id === axisID);

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      {/* THE HEADING NAMES THE AXIS FROM THE RESPONSE, not from local state: the
          server echoes cost_category_id and cost_category precisely so a screen
          cannot render one axis's numbers under another's heading. Before the
          numbers arrive it names the axis the request was made FOR, which is the
          only honest thing to say at that point. */}
      <h1 className="text-lg font-bold text-tally-title">
        Cost Centre Summary
        {data !== null
          ? ` — ${data.cost_category}`
          : chosenAxis
            ? ` — ${chosenAxis.name}`
            : ""}
      </h1>
      <p className="mt-1 max-w-3xl text-xs">
        ONE axis of analysis, over a period. F4 changes the axis, F2 the period.
        {busy ? " · working…" : ""}
      </p>

      {axes !== null && axes.length === 0 && !axesDenied ? (
        <p
          data-testid="cost-centre-summary-no-axes"
          className="mt-3 max-w-3xl text-sm"
        >
          This company has no cost categories, so there is no axis to report on —
          and until one exists, nothing can be allocated to anything. Create one on
          the Cost Centres screen (Gateway &gt; Cost Centres, then Alt+C). This is a
          statement about the masters, not about the cost: the money is being spent,
          it is simply not analysed.
        </p>
      ) : null}

      {error !== "" ? (
        <p
          data-testid="cost-centre-summary-error"
          className="mt-3 max-w-3xl text-sm text-tally-bar"
        >
          {error}
        </p>
      ) : null}

      {axisID && (!from || !to) ? (
        <p
          data-testid="cost-centre-summary-period-prompt"
          className="mt-3 max-w-3xl text-sm"
        >
          Press F2 to set the period. BOTH dates are needed and neither is
          defaulted: this report sums the allocations of postings dated in the
          range, so it is a FLOW and not a position — &quot;what did Sales cost as
          at 31 March&quot; is not a question, and a defaulted opening date would
          answer with everything since the company was formed while looking exactly
          like the year you asked for.
        </p>
      ) : null}

      {data !== null ? (
        <>
          <p className="mt-2 text-sm">
            {data.from} to {data.to} ·{" "}
            <span className="text-tally-title">{data.cost_category}</span> ·{" "}
            {data.allocate_non_revenue
              ? "this axis may carry balance-sheet ledgers as well as P&L ones"
              : "revenue and expense ledgers only"}
          </p>

          {/* ---- THE WARNINGS. They travel WITH the numbers — see (4). ----- */}
          {data.warnings.length > 0 ? (
            <ul
              data-testid="cost-centre-summary-warnings"
              className="mt-3 max-w-3xl list-disc border border-tally-fieldText p-2 pl-6 text-xs"
            >
              {data.warnings.map((w) => (
                <li key={w} className="mt-1">
                  {w}
                </li>
              ))}
            </ul>
          ) : null}

          {/* ---- THE CENTRES. OWN beside TOTAL, always — see (2). --------- */}
          {/*
            ⛔ `centres` EMPTY IS NOT THE SAME AS NOTHING ALLOCATED, and conflating
            the two put two contradictory sentences on one page. When every centre
            carrying allocations has a broken parent chain, the server returns
            centres: [] and puts them ALL in `unattached` — so this branch printed
            "Nothing was allocated on the Department axis in this period" directly
            above a panel listing 1,00,000 of that axis's allocations, with the flat
            oracle beside it also reading 1,00,000.

            So the claim is now made only when BOTH are empty. When the roll-up is
            empty but unattached is not, the unattached panel below IS the whole
            report, and this says so instead.
          */}
          {data.centres.length === 0 && data.unattached.length === 0 ? (
            <p
              data-testid="cost-centre-summary-empty"
              className="mt-3 max-w-3xl text-sm"
            >
              Nothing was allocated on the {data.cost_category} axis in this
              period. That is a statement about ALLOCATION, not about cost: the
              expense may be in the Profit &amp; Loss with nothing attached to it on
              this axis. The Cost Category Summary reports exactly how much — it is
              the report that measures the gap.
            </p>
          ) : data.centres.length === 0 ? (
            <p
              data-testid="cost-centre-summary-all-unattached"
              className="mt-3 max-w-3xl text-sm font-semibold"
            >
              ⛔ EVERY cost centre on the {data.cost_category} axis that carries
              allocations has a broken parent chain, so this axis has no roll-up at
              all — the figures below are the whole report. Money HAS been allocated
              on this axis; none of it can be placed in a hierarchy. Fix the parent
              of each centre listed below before treating this axis as readable.
            </p>
          ) : (
            <table className="mt-3 w-full max-w-5xl text-sm">
              <thead>
                <tr className="text-left text-tally-title">
                  <th className="pr-3 font-normal">Cost centre</th>
                  {/* OWN and TOTAL are separate column pairs on purpose. A single
                      "amount" column would be the report's one fatal
                      simplification. */}
                  <th className="pr-3 text-right font-normal">Own Dr</th>
                  <th className="pr-3 text-right font-normal">Own Cr</th>
                  <th className="pr-3 text-right font-normal">Total Dr</th>
                  <th className="pr-3 text-right font-normal">Total Cr</th>
                  <th className="pr-3 text-right font-normal">Total net</th>
                </tr>
              </thead>
              <tbody>
                {flatten(data.centres).map(({ node, depth }) => (
                  <tr
                    key={node.cost_centre_id}
                    data-testid={`cost-centre-row:${node.name}`}
                  >
                    <td
                      className="pr-3"
                      style={{ paddingLeft: `${depth * 1.25}rem` }}
                    >
                      {node.name}
                      {node.children.length > 0 && !isZeroPair(node) ? (
                        /* The one place the OWN/TOTAL distinction is spelled out
                           in words, because it is the sentence that stops a parent
                           being read as its sub-teams' sum. */
                        <span className="ml-2 text-xs text-tally-field">
                          (has allocations of its own AND children)
                        </span>
                      ) : null}
                    </td>
                    <td
                      className="pr-3 text-right"
                      data-testid={`cost-centre-own:${node.name}`}
                    >
                      {formatMoney(node.own_debit)}
                    </td>
                    <td className="pr-3 text-right">
                      {formatMoney(node.own_credit)}
                    </td>
                    <td
                      className="pr-3 text-right"
                      data-testid={`cost-centre-total:${node.name}`}
                    >
                      {formatMoney(node.total_debit)}
                    </td>
                    <td className="pr-3 text-right">
                      {formatMoney(node.total_credit)}
                    </td>
                    <td className="pr-3 text-right">
                      {formatMoney(node.total_net)}
                    </td>
                  </tr>
                ))}
                {/* THE AXIS'S TOTAL, labelled as exactly that. It is meaningful —
                    this axis's allocations are complete by the database's own
                    trigger — and it must never be added to another axis's, which is
                    what the label and the warnings above both say. */}
                <tr className="text-tally-title">
                  <td className="pr-3 pt-2">
                    Total for the {data.cost_category} axis ONLY
                  </td>
                  <td />
                  <td />
                  <td
                    className="pr-3 pt-2 text-right"
                    data-testid="cost-centre-summary-total-debit"
                  >
                    {formatMoney(data.total_debit)}
                  </td>
                  <td className="pr-3 pt-2 text-right">
                    {formatMoney(data.total_credit)}
                  </td>
                  <td className="pr-3 pt-2 text-right">
                    {formatMoney(data.total_net)}
                  </td>
                </tr>
              </tbody>
            </table>
          )}

          {/* ---- UNATTACHED. A FIELD, NOT AN OMISSION — see (3). ---------- */}
          {data.unattached.length > 0 ? (
            <div
              data-testid="cost-centre-summary-unattached"
              className="mt-4 max-w-4xl border border-tally-bar p-2"
            >
              <h2 className="font-bold text-tally-bar">
                {data.unattached.length} centre(s) COULD NOT BE PLACED IN THE
                ROLL-UP, and their money is here
              </h2>
              <p className="mt-1 text-xs">
                The walk could not attach these to a parent: a parent chain that
                loops, one deeper than the walk&apos;s bound, or a parent outside
                the period&apos;s facts. They are shown because the alternative is
                worse than an ugly report — a centre that disappeared would take its
                allocations with it and every total above would still look
                internally consistent. Fix the parentage on the Cost Centres screen;
                until then read the rolled-up totals as EXCLUDING everything listed
                here, and add the two yourself if you need the axis whole.
              </p>
              <table className="mt-2 w-full text-xs">
                <thead>
                  <tr className="text-left text-tally-title">
                    <th className="pr-3 font-normal">Cost centre</th>
                    <th className="pr-3 text-right font-normal">Own Dr</th>
                    <th className="pr-3 text-right font-normal">Own Cr</th>
                  </tr>
                </thead>
                <tbody>
                  {data.unattached.map((n) => (
                    <tr
                      key={n.cost_centre_id}
                      data-testid={`cost-centre-unattached:${n.name}`}
                    >
                      <td className="pr-3">{n.name}</td>
                      <td className="pr-3 text-right">
                        {formatMoney(n.own_debit)}
                      </td>
                      <td className="pr-3 text-right">
                        {formatMoney(n.own_credit)}
                      </td>
                    </tr>
                  ))}
                  <tr className="text-tally-title">
                    <td className="pr-3 pt-1">Unattached total</td>
                    <td
                      className="pr-3 pt-1 text-right"
                      data-testid="cost-centre-summary-unattached-debit"
                    >
                      {formatMoney(data.unattached_debit)}
                    </td>
                    <td className="pr-3 pt-1 text-right">
                      {formatMoney(data.unattached_credit)}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          ) : null}

          {/* ---- THE SERVER'S OWN CHECK ON ITSELF. Published so it cannot be
                  hidden: `allocated_*` is the flat sum of every centre's own
                  figures — an independent oracle — and `difference_*` is the
                  residual against the roll-up. NOTHING HERE IS RECOMPUTED. */}
          <div className="mt-4 max-w-3xl text-xs">
            <h2 className="font-bold text-tally-title">
              Does this report reconcile with itself?
            </h2>
            <p className="mt-1">
              Flat sum of every centre&apos;s OWN allocations:{" "}
              <span data-testid="cost-centre-summary-allocated">
                {formatMoney(data.allocated_debit)}
              </span>{" "}
              Dr / {formatMoney(data.allocated_credit)} Cr · residual against the
              roll-up:{" "}
              <span data-testid="cost-centre-summary-difference">
                {formatMoney(data.difference_debit)}
              </span>{" "}
              Dr / {formatMoney(data.difference_credit)} Cr ·{" "}
              <span
                className={data.reconciles ? undefined : "font-bold text-tally-bar"}
                data-testid="cost-centre-summary-reconciles"
              >
                {data.reconciles ? "reconciles" : "DOES NOT RECONCILE"}
              </span>
            </p>
            {!data.reconciles ? (
              <p className="mt-1 border border-tally-bar p-2 text-tally-bar">
                The roll-up and the flat sum of the same allocations disagree, which
                should be impossible. Treat every figure above as unsafe: the
                difference is published rather than absorbed precisely so that a
                report which cannot add up cannot look as though it did.
              </p>
            ) : null}
            <p className="mt-2">
              This total belongs to the {data.cost_category} axis and to no other.
              Another axis over the same period allocates the SAME money a second
              time, in full, from a different direction — so adding two axes&apos;
              totals gives twice the cost actually borne. Nothing in this product
              offers that sum, and this report is the reason why.
            </p>
          </div>
        </>
      ) : null}
    </section>
  );
}

/**
 * Does this node hold allocations of its OWN?
 *
 * ⛔ A STRING TEST, NOT ARITHMETIC. Money is a fixed-point string and is never
 * parsed here (see (1) in the header); "is this exactly zero" is answerable by
 * looking at the digits, which is all this needs to decide whether to print the
 * "has allocations of its own AND children" note.
 */
function isZeroPair(node: CostCentreNode): boolean {
  const zero = (v: string) => /^-?0*(\.0*)?$/.test(v.trim());
  return zero(node.own_debit) && zero(node.own_credit);
}

/**
 * The axis's roll-up as a spreadsheet.
 *
 * ⛔ OWN AND TOTAL ARE FOUR COLUMNS, NOT TWO, for the same reason the screen
 * shows four: a parent may hold allocations of its OWN and have children, and
 * neither pair is derivable from the other. Collapsing them into one "amount"
 * column would be this report's one fatal simplification, and it would be far
 * easier to make in a flat file than on a screen.
 *
 * ⛔ AND THE UNATTACHED CENTRES ARE EXPORTED, flagged, below the totals. They
 * are the centres the roll-up could not place; a file that dropped them would
 * take their money with it and still foot, which is precisely the failure the
 * response's own comment warns about.
 */
export function costCentreSummarySheet(
  data: CostCentreSummaryResponse,
): ExportSheet {
  const rows: ExportRow[] = flatten(data.centres).map(
    ({ node, depth }): ExportRow => [
      // The indent is carried as leading spaces, because a flat file has no
      // other way to say "this centre sits under that one" and the hierarchy is
      // what makes OWN vs TOTAL readable.
      textCell(`${"  ".repeat(depth)}${node.name}`),
      moneyCell(node.own_debit),
      moneyCell(node.own_credit),
      moneyCell(node.total_debit),
      moneyCell(node.total_credit),
      moneyCell(node.total_net),
    ],
  );
  rows.push([
    textCell("Total"),
    textCell(""),
    textCell(""),
    moneyCell(data.total_debit),
    moneyCell(data.total_credit),
    moneyCell(data.total_net),
  ]);
  for (const n of data.unattached) {
    rows.push([
      textCell(`UNATTACHED - ${n.name}`),
      moneyCell(n.own_debit),
      moneyCell(n.own_credit),
      textCell(""),
      textCell(""),
      textCell(""),
    ]);
  }
  if (data.unattached.length > 0) {
    rows.push([
      textCell("UNATTACHED total (NOT included in Total above)"),
      moneyCell(data.unattached_debit),
      moneyCell(data.unattached_credit),
      textCell(""),
      textCell(""),
      textCell(""),
    ]);
  }
  return {
    title: `Cost Centre Summary ${data.cost_category} ${data.from} to ${data.to}`,
    headers: [
      "Cost centre",
      "Own Dr",
      "Own Cr",
      "Total Dr",
      "Total Cr",
      "Total net",
    ],
    rows,
  };
}
