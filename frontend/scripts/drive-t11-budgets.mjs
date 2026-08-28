/**
 * T11 — BUDGETS AND BUDGET-VS-ACTUAL, driven end to end against a live cored.
 *
 * ⛔⛔ THIS PROBE MAKES ITS OWN TENANT AND REAPS IT IN A `finally` (the D91
 * pattern). It does NOT touch the demo tenant: it needs a Data Entry member and
 * the demo tenant has none, and provisioning one there would leave a user and a
 * membership behind that `verify:demo` does not tear down.
 *
 * ===========================================================================
 * ⛔ WHAT THE ASSERTIONS ARE PHRASED AGAINST, AND WHY IT MATTERS HERE
 * ===========================================================================
 * Every arm below is written so that the CORRECT behaviour is the PASS. That is
 * unusually load-bearing for this feature, because a budget report's failure mode
 * is not an error — it is a page of plausible, correctly-footed numbers. Three of
 * the probes assert the ABSENCE of a figure (a grand total, a percentage), which
 * is the only shape in which those defects are visible at all.
 *
 * ===========================================================================
 * ⛔ THE CONTROL PROBES, AND WHAT EACH ONE DISPROVES
 * ===========================================================================
 * A refusal-shaped result can be produced by a dozen things that have nothing to
 * do with the rule under test, so each refusal here has a control beside it:
 *
 *  1. THE CLERK IS REALLY A MEMBER. An invite that is never CLAIMED via GET
 *     /v1/me yields a token that is 403 on EVERY company route — which satisfies
 *     every refusal below while proving nothing. That mistake has been made in
 *     this repo before. `the clerk still reads the Trial Balance` disproves it.
 *  2. THE OWNER'S 403 IS NOT THE SAME AS THE CLERK'S. The Owner is refused the
 *     bare report too — with a 400 about a MISSING PARAMETER, not a 403 — so the
 *     clerk's 403 is the ACL and not a malformed request. Asserting only "the
 *     clerk gets 403" would pass on a route that 403s for everybody.
 *  3. THE REPORT REALLY HAS DATA. Every "this figure is absent / is labelled
 *     UNSPENT" arm sits beside one asserting a figure that IS present, so an
 *     empty report cannot satisfy the honesty checks by having nothing in it.
 *
 * ⚠️ THE `print` ARM IS LABELLED AS PRE-EXISTING. Data Entry was ALREADY 403 on
 * the print form before migration 0086: 0038 seeds no `allow print report:`,
 * 0011's coarse grant is action 'display' only, and the level's default_access is
 * 'none'. It passes with or without 0086 and proves nothing about it on its own.
 * Reporting it as an equal win would be half a lie.
 *
 * ⛔ AND THE OWNER'S PRINT ANSWER IS 501, NOT 200. Printing a variance statement
 * is unimplemented on purpose — the screen suppresses a grand total when a plan
 * mixes bases and explains why beside the gap, and paper has no affordance for
 * that. Asserting 200 would fail on correct code.
 *
 * RUN:  node frontend/scripts/drive-t11-budgets.mjs      (takes NO CO — it makes one)
 */
import { execFileSync } from "node:child_process";

import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://localhost:8087";

const j = async (method, path, { token, body } = {}) => {
  const r = await fetch(`${CORED}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const text = await r.text();
  let parsed;
  try {
    parsed = text ? JSON.parse(text) : {};
  } catch {
    parsed = { raw: text };
  }
  return { status: r.status, body: parsed, text };
};
const mint = async (subject) =>
  (await j("POST", "/auth/token", { body: { subject, amr: ["webauthn"] } })).body
    .token;

const psql = (sql) =>
  execFileSync(
    "psql",
    ["-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql],
    { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" },
  ).trim();

// A stamp so a re-run never collides: the invite POST answers 409 for a subject
// that is already a member, and a fixed name would make the second run fail in a
// way that looks like a defect.
const stamp = Math.random().toString(36).slice(2, 8);
const CO = crypto.randomUUID();
const OWNER_SUB = `t11-owner-${stamp}`;
const CLERK = `t11-clerk-${stamp}`;
const CLERK_PW = "t11-clerk-pw-2026";
const COMPANY_NAME = `T11 Budget Co ${stamp}`;
const base = `/v1/companies/${CO}`;

const OWNER = await mint(OWNER_SUB);

// ⛔ EVERYTHING AFTER THE TENANT EXISTS RUNS INSIDE try/finally, so the reap
// happens even when an assertion throws. A probe that creates and dies leaves a
// tenant behind, and the next run's census is one company wronger.
{
  const r = await j("POST", "/v1/companies", {
    token: OWNER,
    body: {
      id: CO,
      name: COMPANY_NAME,
      fy_start_date: "2026-04-01",
      base_currency: "INR",
      state_code: "29",
    },
  });
  if (r.status !== 201) throw new Error(`create company: ${r.status} ${r.text}`);
}

try {
  // -------------------------------------------------------------------------
  // A REAL Data Entry member: mint -> set a first password -> invite -> CLAIM.
  // -------------------------------------------------------------------------
  const CLERK_TOK = await (async () => {
    const tok = await mint(CLERK);
    const pw = await j("POST", "/auth/password", {
      token: tok,
      body: { current_password: "", new_password: CLERK_PW },
    });
    if (pw.status !== 204)
      throw new Error(`set clerk password: ${pw.status} ${pw.text}`);
    const inv = await j("POST", `${base}/members/invites`, {
      token: OWNER,
      body: { subject: CLERK, security_level_name: "Data Entry" },
    });
    if (inv.status !== 201) throw new Error(`invite: ${inv.status} ${inv.text}`);
    // ⛔ THE CLAIM. Without this the token is 403 on everything and every refusal
    // below would pass while proving nothing.
    const me = await j("GET", "/v1/me", { token: tok });
    if (me.status !== 200) throw new Error(`claim: ${me.status} ${me.text}`);
    if ((me.body.claimed_invites ?? []).length !== 1)
      throw new Error("the invite was not claimed");
    return tok;
  })();

  console.log(`\n── tenant ${CO}  ·  clerk ${CLERK} ─────────────`);

  // =========================================================================
  // CONTROL 1 — the clerk IS a member and the coarse report grant is real.
  // =========================================================================
  {
    const r = await j("GET", `${base}/trial-balance?as_on=2026-07-31`, {
      token: CLERK_TOK,
    });
    rec(
      "T11: ⛔ CONTROL — the clerk IS a member and still reads the Trial Balance",
      r.status === 200,
      `trial-balance = ${r.status} (403 here would mean the invite was never ` +
        `claimed and every refusal below is vacuous)`,
    );
  }

  // =========================================================================
  // THE BOOKS. ⛔ THE 2026-04-05 POSTING IS THE WHOLE POINT OF THE FIXTURE:
  // it is inside the financial year and OUTSIDE the budget period, which is the
  // only arrangement under which the two bases give different answers — and
  // therefore the only one that can prove the basis is honoured rather than
  // ignored.
  // =========================================================================
  const mkLedger = async (name, group) => {
    const r = await j("POST", `${base}/ledgers`, {
      token: OWNER,
      body: { name, group_name: group, state_code: "29" },
    });
    if (r.status !== 201)
      throw new Error(`create ledger ${name}: ${r.status} ${r.text}`);
    return r.body.ledger_id ?? r.body.id;
  };
  const L = {};
  L.travel = await mkLedger("T11 Travel", "Indirect Expenses");
  L.stationery = await mkLedger("T11 Stationery", "Indirect Expenses");
  L.rent = await mkLedger("T11 Office Rent", "Indirect Expenses");
  L.sales = await mkLedger("T11 Sales", "Sales Accounts");
  L.customer = await mkLedger("T11 Customer", "Sundry Debtors");

  const postJournal = async (date, debit, credit, amount) => {
    const r = await j("POST", `${base}/vouchers/journal`, {
      token: OWNER,
      body: {
        voucher_date: date,
        created_by: OWNER_SUB,
        postings: [
          { ledger_id: debit, dr_cr: "D", amount },
          { ledger_id: credit, dr_cr: "C", amount },
        ],
      },
    });
    if (r.status !== 201)
      throw new Error(`post journal ${amount} on ${date}: ${r.status} ${r.text}`);
  };
  //                   date          Dr             Cr             amount
  await postJournal("2026-04-05", L.travel, L.customer, "40000"); // BEFORE the plan
  await postJournal("2026-08-10", L.travel, L.customer, "300000");
  await postJournal("2026-08-12", L.stationery, L.customer, "25000"); // never budgeted
  await postJournal("2026-09-01", L.customer, L.sales, "500000");

  // =========================================================================
  // THE PLANS. Two, because one target gets one line per plan — which is itself
  // a rule worth proving (see the duplicate arm below).
  // =========================================================================
  const mkBudget = async (name) => {
    const r = await j("POST", `${base}/budgets`, {
      token: OWNER,
      body: {
        name,
        from_date: "2026-07-01",
        to_date: "2027-03-31",
        created_by: OWNER_SUB,
      },
    });
    if (r.status !== 201) throw new Error(`create budget: ${r.status} ${r.text}`);
    return r.body.id;
  };
  const addLine = async (budgetID, body, want = 201) => {
    const r = await j("POST", `${base}/budgets/${budgetID}/lines`, {
      token: OWNER,
      body: { created_by: OWNER_SUB, ...body },
    });
    if (want !== null && r.status !== want)
      throw new Error(`add line: ${r.status} ${r.text}`);
    return r;
  };

  const nettPlan = await mkBudget(`T11 Nett Plan ${stamp}`);
  await addLine(nettPlan, {
    dimension: "LEDGER",
    target_id: L.travel,
    basis: "NETT_TRANSACTIONS",
    amount: "500000",
  });
  // Budgeted and never posted to — the UNSPENT row.
  await addLine(nettPlan, {
    dimension: "LEDGER",
    target_id: L.rent,
    basis: "NETT_TRANSACTIONS",
    amount: "120000",
  });

  const closingPlan = await mkBudget(`T11 Closing Plan ${stamp}`);
  await addLine(closingPlan, {
    dimension: "LEDGER",
    target_id: L.travel,
    basis: "CLOSING_BALANCE",
    amount: "500000",
  });

  const variance = async (budgetID, dimension, token = OWNER, extra = "") =>
    j("GET", `${base}/budget-variance?budget_id=${budgetID}&dimension=${dimension}${extra}`, {
      token,
    });

  // =========================================================================
  // ⛔⛔ THE ORACLE T11 LIVES OR DIES ON: THE TWO BASES ARE TWO DIFFERENT QUERIES
  // =========================================================================
  {
    const nett = await variance(nettPlan, "LEDGER");
    const closing = await variance(closingPlan, "LEDGER");
    if (nett.status !== 200 || closing.status !== 200)
      throw new Error(`variance: ${nett.status} / ${closing.status}`);

    const findRow = (res, id) =>
      (res.body.rows ?? []).find((r) => r.target_id === id);
    const n = findRow(nett, L.travel);
    const c = findRow(closing, L.travel);

    // CONTROL 3 — the report HAS data, so the honesty arms below cannot pass by
    // being empty.
    rec(
      "T11: ⛔ CONTROL — the report has a real row for the budgeted ledger",
      Boolean(n) && Boolean(c),
      `nett rows=${(nett.body.rows ?? []).length} closing rows=${(closing.body.rows ?? []).length}`,
    );

    rec(
      "T11: ⛔ NETT_TRANSACTIONS excludes movement from BEFORE the budget period",
      n?.actual === "300000.0000",
      `actual = ${n?.actual} (want 300000.0000 — the 40,000 posted 2026-04-05 is ` +
        `outside the plan's period and counting it would overstate the overspend)`,
    );
    rec(
      "T11: ⛔ CLOSING_BALANCE includes every movement up to the period END",
      c?.actual === "340000.0000",
      `actual = ${c?.actual} (want 340000.0000 — a position as at 2027-03-31 ` +
        `includes the 40,000, and dropping it reports a ceiling as if the entity ` +
        `had no history)`,
    );
    // ⛔ THE INVARIANT, STATED AS ITSELF. Both equalities above could be wrong in
    // the same direction; this cannot.
    const gap =
      Number(c?.actual ?? 0) - Number(n?.actual ?? 0);
    rec(
      "T11: ⛔⛔ …so the two bases DIFFER by exactly the pre-period movement",
      gap === 40000,
      `closing - nett = ${gap} (want 40000. ZERO here means "basis" is IGNORED ` +
        `and every closing-balance line is silently measured as movement — a ` +
        `real, correctly-footed, confidently wrong number)`,
    );
    // And each report names its basis on its own face.
    rec(
      "T11: …and each report NAMES the basis it used, in its notes",
      (nett.body.notes ?? []).join(" ").includes("NETT TRANSACTIONS") &&
        (closing.body.notes ?? []).join(" ").includes("CLOSING BALANCE"),
      "a variance whose basis is unstated is a variance a reader cannot interpret",
    );
  }

  // =========================================================================
  // §5.3 — THE THREE WAYS A VARIANCE COLUMN LIES WHILE FOOTING
  // =========================================================================
  {
    const res = await variance(nettPlan, "LEDGER");
    const rows = res.body.rows ?? [];
    const unbudgeted = res.body.unbudgeted ?? [];

    const rent = rows.find((r) => r.target_id === L.rent);
    rec(
      "T11: ⛔ a budget with NOTHING posted is UNSPENT, not a variance of zero",
      rent?.status === "UNSPENT",
      `Office Rent status = ${rent?.status} (an untouched budget is a fact about ` +
        `the plan or about what is being keyed; shown as ordinary underspend it ` +
        `hides both)`,
    );

    const stat = unbudgeted.find((r) => r.target_id === L.stationery);
    rec(
      "T11: ⛔ an actual with NO budget is UNBUDGETED, in its own array",
      stat?.status === "UNBUDGETED" && stat?.actual === "25000.0000",
      `Stationery: status=${stat?.status} actual=${stat?.actual} in ` +
        `unbudgeted[${unbudgeted.length}] (money leaving the business that no ` +
        `plan accounted for is the row a finance person reads first)`,
    );
    rec(
      "T11: ⛔ …and it carries NO percentage — null, never 0 or 100 or an infinity",
      stat !== undefined && stat.variance_percent === null,
      `variance_percent = ${JSON.stringify(stat?.variance_percent)} (there is no ` +
        `budget for it to be a percentage OF; any number is one a reader acts on)`,
    );
    rec(
      "T11: ⛔ …and it is NOT in the plan's rows or the plan's totals",
      !rows.some((r) => r.target_id === L.stationery) &&
        res.body.total_actual === "300000.0000",
      `total_actual = ${res.body.total_actual} (want 300000.0000: folding the ` +
        `25,000 in would make a plan that never covered it look exceeded)`,
    );
  }

  // =========================================================================
  // §5.2 — A POSITION AND A FLOW ARE NEVER ADDED
  // =========================================================================
  {
    const mixed = await mkBudget(`T11 Mixed Plan ${stamp}`);
    await addLine(mixed, {
      dimension: "LEDGER",
      target_id: L.travel,
      basis: "NETT_TRANSACTIONS",
      amount: "500000",
    });
    await addLine(mixed, {
      dimension: "LEDGER",
      target_id: L.sales,
      basis: "CLOSING_BALANCE",
      amount: "1000000",
    });
    const res = await variance(mixed, "LEDGER");

    rec(
      "T11: ⛔⛔ a plan mixing a POSITION and a FLOW is REFUSED a grand total",
      res.body.totals_are_addable === false,
      `totals_are_addable = ${res.body.totals_are_addable} (a closing balance is ` +
        `a position and nett transactions are a flow; their sum is not a quantity)`,
    );
    rec(
      "T11: …and the grand totals are ZEROS, not a plausible figure",
      res.body.total_budget === "0.0000" && res.body.total_actual === "0.0000",
      `total_budget=${res.body.total_budget} total_actual=${res.body.total_actual} ` +
        `(the flag alone is not enough: a client that ignores it must find zeros)`,
    );
    rec(
      "T11: …and BOTH per-basis subtotals survive, as the only totals it may state",
      (res.body.totals ?? []).length === 2,
      `${(res.body.totals ?? []).length} subtotals`,
    );
    rec(
      "T11: …and the page SAYS there is no grand total, so nobody 'fixes' it",
      (res.body.notes ?? []).join(" ").includes("NO GRAND TOTAL"),
      "a suppressed number with no explanation reads as a bug",
    );

    // The INCOME line's side must be derived as credit, or its actual is negative.
    const salesRow = (res.body.rows ?? []).find((r) => r.target_id === L.sales);
    rec(
      "T11: ⛔ an INCOME budget takes the CREDIT side from its group's nature",
      salesRow?.dr_cr === "C" && salesRow?.actual === "500000.0000",
      `dr_cr=${salesRow?.dr_cr} actual=${salesRow?.actual} (the debit side would ` +
        `turn every invoice raised into a negative actual and report a record ` +
        `quarter as a collapse)`,
    );
  }

  // =========================================================================
  // THE MASTER'S OWN REFUSALS
  // =========================================================================
  {
    const dup = await addLine(
      nettPlan,
      {
        dimension: "LEDGER",
        target_id: L.travel,
        basis: "NETT_TRANSACTIONS",
        amount: "700000",
      },
      null,
    );
    rec(
      "T11: ⛔ a SECOND line for one target in one plan is refused (409)",
      dup.status === 409,
      `= ${dup.status} — two would make the report show that ledger twice with ` +
        `the actual counted in full against each, and the budget column would ` +
        `total more than anyone set`,
    );

    const reversed = await j("POST", `${base}/budgets`, {
      token: OWNER,
      body: {
        name: `T11 Reversed ${stamp}`,
        from_date: "2027-03-31",
        to_date: "2026-07-01",
        created_by: OWNER_SUB,
      },
    });
    rec(
      "T11: ⛔ a plan whose period ends before it begins is refused (400)",
      reversed.status === 400 && /unspent/i.test(reversed.text),
      `= ${reversed.status}; the message must say what would have happened — ` +
        `every line would match no movement and report as unspent, which reads ` +
        `as a business fact rather than a typo`,
    );

    const zero = await addLine(
      nettPlan,
      {
        dimension: "LEDGER",
        target_id: L.sales,
        basis: "NETT_TRANSACTIONS",
        amount: "0",
      },
      null,
    );
    rec(
      "T11: ⛔ a budget of ZERO is refused — it is the ABSENCE of a budget",
      zero.status === 400,
      `= ${zero.status}; the report already has a distinct answer for a target ` +
        `nobody budgeted (UNBUDGETED), and two ways to spell one thing is how ` +
        `one of them stops being read`,
    );
  }

  // =========================================================================
  // THE COST-CENTRE ARM: an axis is required, and a closing balance is refused
  // =========================================================================
  {
    const cat = await j("POST", `${base}/cost-categories`, {
      token: OWNER,
      body: { name: "T11 Department", created_by: OWNER_SUB },
    });
    if (cat.status !== 201)
      throw new Error(`cost category: ${cat.status} ${cat.text}`);
    const centre = await j("POST", `${base}/cost-centres`, {
      token: OWNER,
      body: {
        name: "T11 Sales Team",
        cost_category_id: cat.body.id,
        created_by: OWNER_SUB,
      },
    });
    if (centre.status !== 201)
      throw new Error(`cost centre: ${centre.status} ${centre.text}`);
    const centreID = centre.body.id ?? centre.body.cost_centre?.id;

    const ccPlan = await mkBudget(`T11 Centre Plan ${stamp}`);
    const badBasis = await addLine(
      ccPlan,
      {
        dimension: "COST_CENTRE",
        target_id: centreID,
        basis: "CLOSING_BALANCE",
        dr_cr: "D",
        amount: "100000",
      },
      null,
    );
    rec(
      "T11: ⛔ a COST CENTRE on a CLOSING_BALANCE is refused, and the refusal explains",
      badBasis.status === 400 && /holds no opening balance/i.test(badBasis.text),
      `= ${badBasis.status}; a centre holds no position, only allocations against ` +
        `dated postings — its "closing balance" would be every rupee booked to ` +
        `that department since the books began`,
    );

    const noSide = await addLine(
      ccPlan,
      {
        dimension: "COST_CENTRE",
        target_id: centreID,
        basis: "NETT_TRANSACTIONS",
        amount: "100000",
      },
      null,
    );
    rec(
      "T11: ⛔ a COST CENTRE with no stated side is refused — it has no nature",
      noSide.status === 400,
      `= ${noSide.status}; a group or ledger takes its side from the group's ` +
        `nature, a cost centre has none, and the wrong default turns every rupee ` +
        `of departmental cost into a negative actual`,
    );

    await addLine(ccPlan, {
      dimension: "COST_CENTRE",
      target_id: centreID,
      basis: "NETT_TRANSACTIONS",
      dr_cr: "D",
      amount: "100000",
    });

    const noAxis = await variance(ccPlan, "COST_CENTRE");
    rec(
      "T11: ⛔ a cost-centre variance with NO axis is refused, naming the axes",
      noAxis.status === 400 && noAxis.text.includes("T11 Department"),
      `= ${noAxis.status}; the same money is allocated in full on every axis, so ` +
        `picking one silently reports an axis the operator did not choose`,
    );
    const withAxis = await variance(
      ccPlan,
      "COST_CENTRE",
      OWNER,
      `&cost_category_id=${cat.body.id}`,
    );
    rec(
      "T11: …and WITH the axis it is served, carrying the cross-axis caveat",
      withAxis.status === 200 &&
        (withAxis.body.notes ?? []).join(" ").includes("axis ONLY"),
      `= ${withAxis.status}; the caveat travels with the data because the reader ` +
        `most likely to add two of these together is holding two printouts`,
    );
  }

  // =========================================================================
  // ⛔⛔ THE ACL — migration 0086. THE DISPLAY ARM IS THE ONE THAT MATTERS.
  // =========================================================================
  {
    const clerk = await variance(nettPlan, "LEDGER", CLERK_TOK);
    rec(
      "T11: ⛔ DISPLAY — the clerk is REFUSED the budget variance report",
      clerk.status === 403,
      `= ${clerk.status}; it is what the owner PLANNED set beside the actual — ` +
        `the strategy, every line the business is failing against, and the ` +
        `payroll total by inference`,
    );
    // CONTROL 2 — the Owner is stopped by a MISSING PARAMETER, not by permission,
    // so the clerk's 403 is the ACL and not a malformed request.
    const ownerBare = await j("GET", `${base}/budget-variance`, { token: OWNER });
    rec(
      "T11: ⛔ CONTROL — the OWNER on the same bare path gets 400, not 403",
      ownerBare.status === 400,
      `= ${ownerBare.status}; if the Owner were 403 too, the arm above would be ` +
        `proving the route is broken rather than that the rule works`,
    );
    const ownerFull = await variance(nettPlan, "LEDGER");
    rec(
      "T11: …and the OWNER reads the well-formed report — 0086 narrows the clerk only",
      ownerFull.status === 200,
      `= ${ownerFull.status}`,
    );

    // THE MASTER: a BARE token, refused with NO acl_rule at all.
    const clerkMaster = await j("GET", `${base}/budgets`, { token: CLERK_TOK });
    rec(
      "T11: ⛔ the clerk is refused the budget MASTER too — and it costs no rule",
      clerkMaster.status === 403,
      `GET /budgets as Data Entry = ${clerkMaster.status}; "budget" is a BARE ` +
        `token, so default_access 'none' refuses it with nothing written`,
    );

    // PRINT. ⚠️ TRUE BEFORE 0086 TOO — durability, not a fix.
    const clerkPrint = await j("GET", `${base}/print/reports/budget-variance`, {
      token: CLERK_TOK,
    });
    rec(
      "T11: PRINT — the clerk is refused print ⚠️ (TRUE BEFORE 0086 — durability, not a fix)",
      clerkPrint.status === 403,
      `= ${clerkPrint.status}`,
    );
    const ownerPrint = await j("GET", `${base}/print/reports/budget-variance`, {
      token: OWNER,
    });
    rec(
      "T11: ⛔ …and the OWNER gets 501, NOT 200 — a missing LAYOUT, not a policy refusal",
      ownerPrint.status === 501 && ownerPrint.text.includes("budget-variance"),
      `= ${ownerPrint.status} ${ownerPrint.text.slice(0, 90)}`,
    );
  }

  // =========================================================================
  // THE OUT-OF-BAND ORACLE — the RULES themselves, as an administrator sees them
  // =========================================================================
  {
    const r = await j("GET", `${base}/security-levels`, { token: OWNER });
    const de = (r.body.security_levels ?? []).find((l) => l.name === "Data Entry");
    if (!de) throw new Error("no Data Entry level in the admin listing");
    const rules = (de.rules ?? []).filter((x) =>
      String(x.resource).startsWith("report:budget-variance"),
    );
    const shown = rules.map((x) => `${x.effect} ${x.action} ${x.resource}`).sort();
    // ⛔ A 403 can be produced by a dozen things. Only the ROWS prove WHICH rule
    // answered.
    rec(
      "T11: ⛔ the two rules are on the level, and both are `disallow`",
      shown.length === 2 && shown.every((s) => s.startsWith("disallow")),
      shown.join(" | ") || "(none)",
    );
    rec(
      "T11: …and NO allow was seeded (reversing this means DELETING the disallow)",
      !shown.some((s) => s.startsWith("allow")),
      `${shown.length} rows, 0 allows — an allow would TIE at specificity 3 and ` +
        `Disallow beats Allow at equal specificity, so it would silently lose`,
    );
    // ⛔ AND THE MASTER TOKEN CARRIES NONE. Adding rows for a bare token is how a
    // reader concludes bare tokens need them.
    const masterRules = (de.rules ?? []).filter((x) => x.resource === "budget");
    rec(
      "T11: ⛔ the BARE `budget` token carries ZERO rules, and that is the design",
      masterRules.length === 0,
      `${masterRules.length} rows on "budget" (default_access 'none' already ` +
        `refuses every token no rule names)`,
    );
  }

  // =========================================================================
  // THE BROWSER ARM — reachable from the keyboard, and the refusal is a DECISION
  // =========================================================================
  {
    const app = await openApp({
      company: CO,
      headless: true,
      expectCompany: COMPANY_NAME,
      subject: CLERK,
      password: CLERK_PW,
    });
    const { page, body, press, altMac, toGateway } = app;
    try {
      const heading = async () =>
        (await page.locator("h1").first().innerText().catch(() => "")).trim();

      await toGateway();
      await altMac("g", 1200);
      await page.keyboard.type("Budget Variance");
      await page.waitForTimeout(800);
      await press("Enter", 4000);

      const h = await heading();
      const seen = (await body()).replace(/\s+/g, " ");
      rec(
        "T11: the clerk REACHES Budget Variance from the keyboard (Alt+G)",
        h === "Budget Variance",
        `h1="${h}"`,
      );
      // ⛔ THE REFUSAL IS RENDERED AS A DECISION, not a blank and not an error.
      rec(
        "T11: ⛔ …and the refusal is a DECISION naming the permission",
        (await page.getByTestId("budget-variance-denied").count()) > 0 &&
          /may not open/i.test(seen),
        (seen.match(/.{0,40}may not open.{0,90}/i) ?? [
          "(no decision panel — it rendered something else)",
        ])[0],
      );
      rec(
        "T11: …and it says printing is a SEPARATE permission from reading",
        /separate permission/i.test(seen),
        (seen.match(/.{0,30}separate permission.{0,60}/i) ?? ["(absent)"])[0],
      );
      rec(
        "T11: …and NOT an empty table that reads as “no data”",
        !/Loading…$/.test(seen) && !/\bNaN\b/.test(seen),
        "no spinner-stuck / NaN state",
      );
    } finally {
      await app.browser?.close?.();
    }
  }
} finally {
  // =========================================================================
  // ⛔ REAP, IN A `finally`. §5.14 — a probe that creates must clean up, and must
  // ASSERT it worked. This tenant HAS books, so the delete list is longer than
  // the book-less one drive-l17 uses: vouchers, postings and the budget tables
  // all hold rows.
  // =========================================================================
  const q = (s) => `'${String(s).replace(/'/g, "''")}'`;
  psql(
    `begin;
     -- THE TRIGGERS MUST BE DISARMED FOR THE DURATION, and this is not a
     -- shortcut: posting_immutable RAISES on DELETE, so a tenant WITH BOOKS
     -- cannot be reaped without it. internal/platform/testdb does exactly this
     -- (SET LOCAL session_replication_role = replica, testdb.go:284) for the
     -- same reason, and notes that only a superuser may set it -- which is why
     -- this reap connects as postgres and not as tally_app.
     --
     -- ⚠️ SET LOCAL, so it lasts only to COMMIT. A session-wide SET here would
     -- leave the shared dev database with immutability disarmed for whatever ran
     -- next on that connection, which is a far worse outcome than a leaked tenant.
     set local session_replication_role = replica;
     delete from budget_line          where company_id = ${q(CO)};
     delete from budget               where company_id = ${q(CO)};
     delete from posting_cost_allocation where company_id = ${q(CO)};
     delete from cost_centre          where company_id = ${q(CO)};
     delete from cost_category        where company_id = ${q(CO)};
     delete from bill_allocation      where company_id = ${q(CO)};
     delete from posting              where company_id = ${q(CO)};
     -- ⛔ HEAD BEFORE VERSION, AND THE OBVIOUS ALTERNATIVE DOES NOT WORK.
     -- voucher_head.current_version_id is NOT NULL and points at voucher_version,
     -- so "null it out, then delete the versions" fails on the not-null
     -- constraint -- which is not suppressed by session_replication_role (that
     -- disarms TRIGGERS, not CHECK/NOT NULL). Deleting the head first satisfies
     -- the FK because there is then nothing left pointing at the versions.
     delete from voucher_head         where company_id = ${q(CO)};
     delete from voucher_version      where company_id = ${q(CO)};
     delete from ledger_balance       where company_id = ${q(CO)};
     delete from acl_rule             where company_id = ${q(CO)};
     delete from company_membership   where company_id = ${q(CO)};
     delete from company_invite       where company_id = ${q(CO)};
     delete from notification_outbox  where company_id = ${q(CO)};
     delete from master_audit         where company_id = ${q(CO)};
     delete from ledger               where company_id = ${q(CO)};
     delete from godown               where company_id = ${q(CO)};
     delete from unit                 where company_id = ${q(CO)};
     delete from voucher_type         where company_id = ${q(CO)};
     delete from account_group        where company_id = ${q(CO)};
     delete from security_level       where company_id = ${q(CO)};
     delete from company              where id = ${q(CO)};
     commit;`,
  );
  const left = psql(`select count(*) from company where id = ${q(CO)}`);
  rec(
    "T11: ⛔ the probe reaped its own tenant, and asserted it worked",
    left === "0",
    `companies matching ${CO.slice(0, 8)}…: ${left}`,
  );
}

process.exit(summary());
