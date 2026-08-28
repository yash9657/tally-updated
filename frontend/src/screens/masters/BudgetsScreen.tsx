import { useCallback, useEffect, useMemo, useState } from "react";

import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import { ApiError } from "../../api";
import type {
  AccountGroupListItem,
  Budget,
  BudgetBasis,
  BudgetDimension,
  BudgetLine,
  CostCategory,
  CostCentre,
  LedgerListItem,
} from "../../api";
import { formatMoney } from "../reports/money";
import { usePrintAction } from "../usePrint";

/**
 * BUDGETS — the T11 master, and the screen that makes the whole feature usable.
 *
 * Until a plan and a line exist, the Budget Variance report has nothing in its
 * second column and can only say so. Tally reaches the same master through
 * Gateway > Accounts Info > Budgets; this is that door.
 *
 * ⛔⛔ THE TWO THINGS TO UNDERSTAND BEFORE READING ANY OF IT.
 *
 * 1. A BUDGET POSTS NOTHING. No voucher, no posting, no balance, no hash chain. A
 *    budget is the COMPARISON DIMENSION a report measures an actual against — it
 *    is why the feature cannot be faked by keying journals, and why nothing on
 *    this screen affects a single statement in the product. Creating a plan
 *    changes no number anywhere; it creates a second column for one report.
 *
 * 2. THE BASIS IS NOT A DETAIL, IT IS HALF THE MEANING. Every line is measured on
 *    either a CLOSING BALANCE (a POSITION as at the plan's end date, opening
 *    balance folded in) or NETT TRANSACTIONS (MOVEMENT inside the plan's period,
 *    opening balance excluded). "Sundry Debtors must not exceed 40 lakh at year
 *    end" is the first; "travel spend must not exceed 5 lakh this year" is the
 *    second. THE SAME Rs 10,00,000 MEANS DIFFERENT THINGS UNDER EACH. The server
 *    gives the column no default for exactly that reason, and this screen shows
 *    the basis IN WORDS on every line — a token alone would let an operator set a
 *    position ceiling believing they had set a spend cap.
 *
 * WHAT THIS SCREEN REFUSES TO DO
 *
 *  1. IT NEVER ASKS FOR AN ID. Every target is picked from a SERVED list — groups,
 *     ledgers, cost centres. A uuid is a thing no screen in this product displays,
 *     so a box for one could not be filled by a human: the defect the TDS challan
 *     form shipped and had to be rebuilt for.
 *
 *  2. IT NEVER OFFERS A CLOSING BALANCE FOR A COST CENTRE, and the reason is a
 *     modelling fact rather than tidiness. A cost centre is not an account: it
 *     holds no opening balance and carries no position, only allocations against
 *     dated postings. Summing every allocation it has ever received would be the
 *     total cost booked to that department since the books began — a number no
 *     business plans against, and one that would make a year's budget look almost
 *     entirely unspent in year one and several times overspent in year four. The
 *     server refuses it, a CHECK constraint refuses it behind that, and the basis
 *     control here does not offer it.
 *
 *  3. IT NEVER SILENTLY DEFAULTS THE SIDE FOR A COST CENTRE. A group or a ledger
 *     has a NATURE (debit for asset/expense, credit for liability/income) and the
 *     server takes the side from it, so an operator never types a debit indicator
 *     for the ordinary case. A cost centre has no nature, so the side is asked
 *     for — a coin-flip default would turn every rupee of departmental cost into a
 *     negative actual against the plan.
 *
 *  4. IT NEVER HIDES THAT A PLAN IS EMPTY. An empty budget is a live trap: the
 *     variance report over it is a page of unbudgeted actuals, which reads as
 *     "nothing was planned" and is indistinguishable from "the plan was never
 *     keyed". The line count is on every plan row, and an empty one says so.
 *
 *  5. IT NEVER PRETENDS A REVISION IS HISTORY. Altering a line overwrites the
 *     previous figure: it survives only in the master edit log, not as a
 *     reportable column. So "the original budget, the revised budget, and the
 *     actual" — three columns a real business asks for — is NOT available, and
 *     this screen says so where an operator is about to revise one.
 *
 *  6. IT RENDERS NO ACTUALS AND NO VARIANCE. Those are the Budget Variance
 *     report, at a grain the operator chooses there. There is no arithmetic on
 *     money in this file at all: every amount is a fixed-point string rendered
 *     through formatMoney.
 *
 * IT TREATS A 403 AS A DECISION. The server serves every route here under the BARE
 * `budget` token, so the seeded Data Entry level is refused all of them with NO
 * ACL rule written — its default_access is 'none' and every rule it carries names
 * a specific token. A budget states what the owner INTENDS and setting one is not
 * clerical work, so a refusal is the normal answer for that level and is rendered
 * as the decision it is.
 */

const BASIS_WORDS: Record<BudgetBasis, string> = {
  CLOSING_BALANCE:
    "a POSITION as at the plan's end date, with the opening balance folded in",
  NETT_TRANSACTIONS:
    "MOVEMENT inside the plan's period, with the opening balance excluded",
};

type TargetKind = BudgetDimension;

export function BudgetsScreen() {
  const client = useClient();
  const companyID = useCompanyID();

  const [plans, setPlans] = useState<Budget[] | null>(null);
  const [denied, setDenied] = useState(false);
  const [listError, setListError] = useState("");

  const [openPlan, setOpenPlan] = useState<string | null>(null);
  const [lines, setLines] = useState<BudgetLine[] | null>(null);
  const [linesError, setLinesError] = useState("");

  // The create panel (Alt+C) — one panel that creates either a PLAN or a LINE.
  const [creating, setCreating] = useState(false);
  const [kind, setKind] = useState<"" | "plan" | "line">("");
  const [notice, setNotice] = useState("");
  const [refusal, setRefusal] = useState("");
  const [busy, setBusy] = useState(false);

  // Plan fields.
  const [name, setName] = useState("");
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");

  // Line fields.
  const [dimension, setDimension] = useState<TargetKind>("LEDGER");
  const [targetID, setTargetID] = useState("");
  const [basis, setBasis] = useState<BudgetBasis>("NETT_TRANSACTIONS");
  const [drCr, setDrCr] = useState("");
  const [amount, setAmount] = useState("");
  const [note, setNote] = useState("");

  // The SERVED target lists — see (1). Loaded lazily per grain so a refusal on
  // one master plane does not block budgeting against another.
  const [groups, setGroups] = useState<AccountGroupListItem[] | null>(null);
  const [ledgers, setLedgers] = useState<LedgerListItem[] | null>(null);
  const [centres, setCentres] = useState<CostCentre[] | null>(null);
  const [axes, setAxes] = useState<CostCategory[] | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await client.listBudgets(companyID);
      setPlans(res.budgets);
      setDenied(false);
      setListError("");
    } catch (e: unknown) {
      if (e instanceof ApiError && e.status === 403) {
        // ⛔ NOT setPlans([]). That would state as a FACT that this company has no
        // budgets, when what is true is that this level may not see them. "There
        // are none" and "you may not see them" are different sentences about
        // different subjects, and only one of them is about the books.
        setDenied(true);
        setPlans(null);
      } else {
        setPlans([]);
        setListError(e instanceof Error ? e.message : String(e));
      }
    }
  }, [client, companyID]);

  useEffect(() => {
    void load();
  }, [load]);

  // ---- The open plan's lines.
  useEffect(() => {
    let live = true;
    if (!openPlan) {
      setLines(null);
      return;
    }
    client
      .listBudgetLines(companyID, openPlan)
      .then((res) => {
        if (!live) return;
        setLines(res.lines);
        setLinesError("");
      })
      .catch((e: unknown) => {
        if (!live) return;
        setLines(null);
        setLinesError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      live = false;
    };
  }, [client, companyID, openPlan]);

  // ---- The target lists, per grain.
  useEffect(() => {
    let live = true;
    if (!creating || kind !== "line") return;
    if (dimension === "GROUP" && groups === null) {
      client
        .listGroups(companyID)
        .then((r) => live && setGroups(r.groups))
        .catch(() => live && setGroups([]));
    }
    if (dimension === "LEDGER" && ledgers === null) {
      client
        .listLedgers(companyID)
        .then((r) => live && setLedgers(r.ledgers))
        .catch(() => live && setLedgers([]));
    }
    if (dimension === "COST_CENTRE" && centres === null) {
      client
        .listCostCentres(companyID)
        .then((r) => {
          if (!live) return;
          setCentres(r.cost_centres);
          setAxes(r.cost_categories);
        })
        .catch(() => {
          if (!live) return;
          setCentres([]);
          setAxes([]);
        });
    }
    return () => {
      live = false;
    };
  }, [centres, client, companyID, creating, dimension, groups, kind, ledgers]);

  // ⛔ (2) A COST CENTRE MAY ONLY BE BUDGETED ON NETT TRANSACTIONS, so switching
  // the grain to COST_CENTRE forces the basis. Done here rather than only in the
  // control's options so that a basis chosen BEFORE the grain cannot survive the
  // switch — which would post a request the server rejects for a reason the
  // operator cannot see on the form.
  useEffect(() => {
    if (dimension === "COST_CENTRE" && basis !== "NETT_TRANSACTIONS") {
      setBasis("NETT_TRANSACTIONS");
    }
  }, [basis, dimension]);

  usePrintAction({ voucher: { voucherID: null } });

  // Alt+C — OPEN THE CREATE PANEL.
  useRegisterScreenAction(
    "createBudget",
    useCallback((): string => {
      if (denied) {
        return (
          "You are not permitted to read or set this company's budgets at this " +
          "security level, so there is nothing to add to. A budget states what the " +
          "owner intends; an owner can grant it with an `allow budget` rule in " +
          "Users & Roles."
        );
      }
      if (creating) return "The create panel is already open. Ctrl+A accepts it.";
      setCreating(true);
      setKind("");
      setName("");
      setFromDate("");
      setToDate("");
      setTargetID("");
      setAmount("");
      setNote("");
      setDrCr("");
      setRefusal("");
      setNotice("");
      return "Create a budget, or add a line to the open one. Ctrl+A accepts it.";
    }, [creating, denied]),
  );

  // Ctrl+A — ACCEPT.
  const submit = useCallback(async (): Promise<string> => {
    if (!creating) {
      return "Nothing to accept here. Alt+C creates a budget or adds a line.";
    }
    if (kind === "") {
      // THE SCREEN'S OWN REFUSAL, and it is not duplicated validation: there is no
      // request to send until this is known, because the two are different routes.
      return (
        "Say which you are creating first. A BUDGET is a named plan over a period; " +
        "a LINE is one budgeted figure on it. They are different routes, so " +
        "nothing was sent."
      );
    }
    if (kind === "line" && !openPlan) {
      return (
        "Open a budget first — a line belongs to a plan, and there is no default " +
        "plan for it to attach to. Nothing was sent."
      );
    }
    if (kind === "line" && targetID === "") {
      return (
        "Choose the thing being budgeted. A line with no target could not be " +
        "compared against anything, so nothing was sent."
      );
    }
    if (kind === "line" && dimension === "COST_CENTRE" && drCr === "") {
      // ⛔ (3) NO COIN-FLIP DEFAULT. See the file header.
      return (
        'Say which side this cost centre budget is expected on ("D" for a cost, ' +
        'which is the ordinary case). A group or a ledger has a NATURE to take the ' +
        "side from; a cost centre is an axis of analysis and has none, so there is " +
        "nothing to derive it from. Nothing was sent."
      );
    }
    setRefusal("");
    setNotice("");
    setBusy(true);
    try {
      if (kind === "plan") {
        const created = await client.createBudget(companyID, {
          name: name.trim(),
          from_date: fromDate.trim(),
          to_date: toDate.trim(),
          // ⛔ REQUIRED. Omitting it is a 400 AFTER the step-up has already
          // succeeded, which reads as "the passkey failed" and is not.
          created_by: "ui",
        });
        setCreating(false);
        await load();
        setOpenPlan(created.id);
        const message =
          `Created “${created.name}” for ${created.from_date} to ${created.to_date}. ` +
          "It has NO LINES yet, so a variance report over it would be a page of " +
          "unbudgeted actuals — Alt+C again, this time for a LINE.";
        setNotice(message);
        return message;
      }
      await client.addBudgetLine(companyID, openPlan!, {
        dimension,
        target_id: targetID,
        basis,
        // Omitted for a group or ledger so the server takes the natural side. Sent
        // for a cost centre, which has no nature — see (3).
        ...(drCr === "" ? {} : { dr_cr: drCr }),
        amount: amount.trim(),
        note: note.trim(),
        created_by: "ui",
      });
      setCreating(false);
      setTargetID("");
      setAmount("");
      setNote("");
      const res = await client.listBudgetLines(companyID, openPlan!);
      setLines(res.lines);
      const message =
        `Added the line, measured on ${basis} — ${BASIS_WORDS[basis]}. It changes ` +
        "no balance and no statement: a budget posts nothing. It is the second " +
        "column the Budget Variance report compares an actual against.";
      setNotice(message);
      return message;
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      if (e instanceof ApiError && e.status === 403) {
        setRefusal(
          "You are not permitted to set budgets at this security level. That is " +
            "the `budget` token — a budget states what the owner intends, and it is " +
            "refused to the seeded Data Entry level with no rule written.",
        );
        return "Refused: you may not set a budget at this security level.";
      }
      // The server's sentence, verbatim. Every refusal it raises names the
      // CONSEQUENCE — which report goes wrong, and how — rather than a constraint,
      // so paraphrasing it here would lose the only useful part.
      setRefusal(message);
      return message;
    } finally {
      setBusy(false);
    }
  }, [
    amount,
    basis,
    client,
    companyID,
    creating,
    dimension,
    drCr,
    fromDate,
    kind,
    load,
    name,
    note,
    openPlan,
    targetID,
    toDate,
  ]);

  useRegisterScreenAction("accept", submit);

  const targetOptions = useMemo(() => {
    if (dimension === "GROUP") {
      return (groups ?? []).map((g) => ({ id: g.id, label: g.name }));
    }
    if (dimension === "LEDGER") {
      return (ledgers ?? []).map((l) => ({ id: l.id, label: l.name }));
    }
    // ⚠️ THE AXIS TRAVELS WITH A CENTRE'S NAME. "Mumbai" may legitimately be both
    // a Branch and a Project, and a list of bare centre names spanning axes is
    // ambiguous — an operator picking from it picks the wrong one. That is why the
    // server puts cost_category_name on every centre row.
    return (centres ?? []).map((c) => ({
      id: c.id,
      label: `${c.name} (${c.cost_category_name})`,
    }));
  }, [centres, dimension, groups, ledgers]);

  return (
    <section data-testid="budgets" className="p-3 text-sm">
      <h1 className="text-base font-bold text-tally-title">Budgets</h1>

      {denied ? (
        <div
          data-testid="budgets-denied"
          className="mt-3 max-w-3xl border border-tally-bar p-3 text-xs"
        >
          <p className="font-bold text-tally-bar">
            You may not see or set this company&apos;s budgets at this security
            level.
          </p>
          <p className="mt-2">
            This is a DECISION and not a fault, and it costs no permission rule at
            all: <span className="text-tally-title">budget</span> is a bare token,
            and a level whose default access is &ldquo;none&rdquo; is refused every
            token no rule names. A budget states what the owner INTENDS — the one
            figure in this product that no voucher contains — and setting one is not
            clerical work. An owner can grant it with an{" "}
            <span className="text-tally-title">allow display budget</span> rule in
            Users &amp; Roles.
          </p>
        </div>
      ) : null}

      {listError !== "" ? (
        <p
          data-testid="budgets-error"
          className="mt-3 max-w-3xl border border-tally-bar p-2 text-xs text-tally-bar"
        >
          {listError}
        </p>
      ) : null}

      {notice !== "" ? (
        <p data-testid="budgets-notice" className="mt-3 max-w-3xl text-xs">
          {notice}
        </p>
      ) : null}
      {refusal !== "" ? (
        <p
          data-testid="budgets-refusal"
          className="mt-3 max-w-3xl border border-tally-bar p-2 text-xs text-tally-bar"
        >
          {refusal}
        </p>
      ) : null}

      {/* ---- THE PLANS. */}
      {plans !== null ? (
        plans.length === 0 ? (
          <p data-testid="budgets-empty" className="mt-3 max-w-3xl text-xs">
            This company has no budgets. Until one exists the Budget Variance report
            has nothing in its second column and can only say so — Alt+C creates
            one. A budget posts nothing: it changes no voucher and no balance, which
            is exactly why it cannot be faked by keying journals.
          </p>
        ) : (
          <table className="mt-3 max-w-3xl text-xs">
            <thead>
              <tr className="text-left text-tally-field">
                <th>Plan</th>
                <th>Period</th>
                <th className="text-right">Lines</th>
                <th>By</th>
              </tr>
            </thead>
            <tbody>
              {plans.map((p) => (
                <tr key={p.id} data-testid={`budget:${p.name}`}>
                  <td>
                    <button
                      type="button"
                      onClick={() => setOpenPlan(p.id)}
                      className={
                        p.id === openPlan
                          ? "font-bold text-tally-title underline"
                          : ""
                      }
                    >
                      {p.name}
                    </button>
                  </td>
                  <td>
                    {p.from_date} to {p.to_date}
                  </td>
                  <td className="text-right">
                    {/* ⛔ (4) AN EMPTY PLAN SAYS SO. */}
                    {p.line_count === 0 ? (
                      <span
                        data-testid={`budget-empty:${p.name}`}
                        className="text-tally-bar"
                      >
                        NONE
                      </span>
                    ) : (
                      p.line_count
                    )}
                  </td>
                  <td>{p.created_by}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )
      ) : denied ? null : (
        <p className="mt-3 text-xs">Loading…</p>
      )}

      {/* ---- THE OPEN PLAN'S LINES. */}
      {openPlan !== null ? (
        <div className="mt-4 max-w-4xl" data-testid="budget-lines">
          <h2 className="font-bold text-tally-title">
            Lines on{" "}
            {plans?.find((p) => p.id === openPlan)?.name ?? "the open budget"}
          </h2>
          {linesError !== "" ? (
            <p className="mt-1 text-xs text-tally-bar">{linesError}</p>
          ) : null}
          {lines !== null && lines.length === 0 ? (
            <p
              data-testid="budget-lines-empty"
              className="mt-2 border border-tally-bar p-2 text-xs"
            >
              THIS PLAN HAS NO LINES. A variance report over it is a page of
              unbudgeted actuals, which reads as &ldquo;nothing was planned&rdquo;
              and is indistinguishable from &ldquo;the plan was never keyed&rdquo;.
              Alt+C adds a line.
            </p>
          ) : null}
          {lines !== null && lines.length > 0 ? (
            <table className="mt-2 w-full text-xs">
              <thead>
                <tr className="text-left text-tally-field">
                  <th>Grain</th>
                  <th>Target</th>
                  <th>Side</th>
                  <th className="text-right">Budget</th>
                  <th>Measured on</th>
                </tr>
              </thead>
              <tbody>
                {lines.map((l) => (
                  <tr key={l.id} data-testid={`budget-line:${l.target_name}`}>
                    <td>{l.dimension}</td>
                    <td>
                      {l.target_name}
                      {l.cost_category_name ? (
                        <span className="ml-1 text-tally-field">
                          ({l.cost_category_name})
                        </span>
                      ) : null}
                      {l.nature ? (
                        <span className="ml-1 text-tally-field">
                          · {l.nature.toLowerCase()}
                        </span>
                      ) : null}
                    </td>
                    <td>{l.dr_cr === "C" ? "credit" : "debit"}</td>
                    <td className="text-right">{formatMoney(l.amount)}</td>
                    <td>
                      {/* ⛔ THE BASIS IN WORDS, ON EVERY LINE. Half the meaning of
                          the figure beside it — see the file header. `basis_means`
                          is served for exactly this and is rendered rather than
                          re-derived. */}
                      <span className="text-tally-title">{l.basis}</span> —{" "}
                      {l.basis_means || BASIS_WORDS[l.basis]}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : null}
          {lines !== null && lines.length > 0 ? (
            <p className="mt-2 max-w-3xl text-xs text-tally-field">
              {/* ⛔ (5) A REVISION IS NOT HISTORY, and this is where an operator is
                  about to make one. */}
              Altering a figure OVERWRITES it. The previous figure survives only in
              the master edit log, not as a reportable column — so &ldquo;the
              original budget, the revised budget, and the actual&rdquo; is not
              available as three columns. If that matters for a plan, keep the
              original as its own budget rather than revising this one.
            </p>
          ) : null}
        </div>
      ) : null}

      {/* ---- THE CREATE PANEL. */}
      {creating ? (
        <div
          data-testid="budget-create"
          className="mt-4 max-w-3xl border border-tally-field p-3 text-xs"
        >
          <p>
            <label className="mr-3">
              <input
                type="radio"
                name="budget-kind"
                data-testid="budget-kind-plan"
                checked={kind === "plan"}
                onChange={() => setKind("plan")}
              />{" "}
              a BUDGET (a named plan over a period)
            </label>
            <label>
              <input
                type="radio"
                name="budget-kind"
                data-testid="budget-kind-line"
                checked={kind === "line"}
                onChange={() => setKind("line")}
              />{" "}
              a LINE on the open budget (one budgeted figure)
            </label>
          </p>

          {kind === "plan" ? (
            <div className="mt-2">
              <label className="block">
                Name{" "}
                <input
                  data-testid="budget-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  className="border border-tally-field bg-transparent"
                />
              </label>
              <label className="mt-1 block">
                From{" "}
                <input
                  data-testid="budget-from"
                  placeholder="YYYY-MM-DD"
                  value={fromDate}
                  onChange={(e) => setFromDate(e.target.value)}
                  className="border border-tally-field bg-transparent"
                />
              </label>
              <label className="mt-1 block">
                To{" "}
                <input
                  data-testid="budget-to"
                  placeholder="YYYY-MM-DD"
                  value={toDate}
                  onChange={(e) => setToDate(e.target.value)}
                  className="border border-tally-field bg-transparent"
                />
              </label>
              <p className="mt-2 text-tally-field">
                BOTH BOUNDS ARE REQUIRED, unlike every report period in this
                product. A plan for no stated period cannot be compared against
                anything: the closing-balance basis would have no date to be
                measured as at. The period is also NOT narrowable on the report —
                comparing a full-period budget against a shorter window needs a
                spread rule nobody has chosen — so key the period you actually mean
                to be held to.
              </p>
            </div>
          ) : null}

          {kind === "line" ? (
            <div className="mt-2">
              <label className="block">
                Grain{" "}
                <select
                  data-testid="budget-line-dimension"
                  value={dimension}
                  onChange={(e) => {
                    setDimension(e.target.value as TargetKind);
                    setTargetID("");
                  }}
                  className="border border-tally-field bg-transparent"
                >
                  <option value="GROUP">a GROUP (every ledger under it)</option>
                  <option value="LEDGER">a LEDGER</option>
                  <option value="COST_CENTRE">a COST CENTRE</option>
                </select>
              </label>

              <label className="mt-1 block">
                Target{" "}
                <select
                  data-testid="budget-line-target"
                  value={targetID}
                  onChange={(e) => setTargetID(e.target.value)}
                  className="border border-tally-field bg-transparent"
                >
                  <option value="">— choose —</option>
                  {targetOptions.map((o) => (
                    <option key={o.id} value={o.id}>
                      {o.label}
                    </option>
                  ))}
                </select>
              </label>
              {dimension === "COST_CENTRE" &&
              centres !== null &&
              centres.length === 0 ? (
                <p
                  data-testid="budget-no-centres"
                  className="mt-1 text-tally-bar"
                >
                  No cost centre is readable at this level, so nothing can be
                  budgeted at this grain. A centre sits on an axis — create both on
                  the Cost Centres screen, or ask an owner for the{" "}
                  <span className="text-tally-title">cost-centre</span> token.
                  {axes !== null && axes.length === 0
                    ? " This company has no cost categories either, and a centre cannot exist without one."
                    : ""}
                </p>
              ) : null}

              <label className="mt-1 block">
                Measured on{" "}
                <select
                  data-testid="budget-line-basis"
                  value={basis}
                  onChange={(e) => setBasis(e.target.value as BudgetBasis)}
                  className="border border-tally-field bg-transparent"
                >
                  <option value="NETT_TRANSACTIONS">
                    NETT TRANSACTIONS — {BASIS_WORDS.NETT_TRANSACTIONS}
                  </option>
                  {/* ⛔ (2) NOT OFFERED FOR A COST CENTRE. A centre holds no
                      position. */}
                  {dimension === "COST_CENTRE" ? null : (
                    <option value="CLOSING_BALANCE">
                      CLOSING BALANCE — {BASIS_WORDS.CLOSING_BALANCE}
                    </option>
                  )}
                </select>
              </label>
              {dimension === "COST_CENTRE" ? (
                <p
                  data-testid="budget-centre-basis-note"
                  className="mt-1 text-tally-field"
                >
                  A COST CENTRE CAN ONLY BE BUDGETED ON NETT TRANSACTIONS, and that
                  is a modelling fact rather than a restriction. A cost centre is
                  not an account: it holds no opening balance and carries no
                  position, only allocations against dated postings. Its
                  &ldquo;closing balance&rdquo; would be every rupee booked to that
                  department since the books began — a number that would make a
                  year&apos;s budget look almost entirely unspent in year one and
                  several times overspent in year four.
                </p>
              ) : null}

              <label className="mt-1 block">
                Amount{" "}
                <input
                  data-testid="budget-line-amount"
                  placeholder="500000.0000"
                  value={amount}
                  onChange={(e) => setAmount(e.target.value)}
                  className="border border-tally-field bg-transparent"
                />
              </label>

              <label className="mt-1 block">
                Side{" "}
                <select
                  data-testid="budget-line-drcr"
                  value={drCr}
                  onChange={(e) => setDrCr(e.target.value)}
                  className="border border-tally-field bg-transparent"
                >
                  {/* ⛔ (3) THE EMPTY OPTION IS THE ORDINARY CASE for a group or a
                      ledger and IS NOT OFFERED for a cost centre. */}
                  {dimension === "COST_CENTRE" ? null : (
                    <option value="">
                      the target&apos;s natural side (the ordinary case)
                    </option>
                  )}
                  <option value="D">debit — what is spent or owned</option>
                  <option value="C">credit — what is earned or owed</option>
                </select>
              </label>
              <p className="mt-1 text-tally-field">
                {dimension === "COST_CENTRE"
                  ? 'A cost centre has no NATURE to take a side from — it is an axis of analysis, not an account — so the side must be stated. A cost budget is "D".'
                  : "Leave this as the natural side unless you mean a CONTRA expectation (a bank account budgeted to sit overdrawn is a credit balance on an asset ledger). The side is taken from the target's nature: debit for what is spent or owned, credit for what is earned or owed."}
              </p>

              <label className="mt-1 block">
                Note{" "}
                <input
                  data-testid="budget-line-note"
                  placeholder="assumes 3 new hires in Q2"
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  className="border border-tally-field bg-transparent"
                />
              </label>
            </div>
          ) : null}

          <p className="mt-2 text-tally-field">
            {busy ? "Working…" : "Ctrl+A accepts. Esc closes the panel."}
          </p>
        </div>
      ) : null}

      {plans !== null && !denied ? (
        <p className="mt-4 max-w-3xl text-xs">
          NOTHING ON THIS SCREEN MOVES A BALANCE. A budget posts no voucher and no
          posting, appears in no Trial Balance, and is in no hash chain — which is
          why the feature cannot be faked by keying journals, and why a plan can be
          revised freely where a voucher cannot. What a plan is worth is the{" "}
          <span className="text-tally-title">Budget Variance</span> report, which
          takes a plan and a GRAIN (group, ledger or cost centre) and shows the
          actual beside it.
        </p>
      ) : null}
    </section>
  );
}
