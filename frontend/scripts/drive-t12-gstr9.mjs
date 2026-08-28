/**
 * T12 — THE GSTR-9 ANNUAL RECONCILIATION, driven end to end against a live cored.
 *
 * ⛔⛔ THIS PROBE MAKES ITS OWN TENANT AND REAPS IT IN A `finally` (the D91
 * pattern). It does NOT touch the demo tenant: it needs a Data Entry member and
 * the demo tenant has none, and provisioning one there would leave a user and a
 * membership behind that `verify:demo` does not tear down.
 *
 * ===========================================================================
 * ⭐ THE ONE ARM THAT MATTERS, AND THE FIXTURE THAT MAKES IT POSSIBLE
 * ===========================================================================
 * An annual return is twelve months added up, and addition CANCELS. A company
 * that is 5,000 wrong in one direction in May and 5,000 wrong the other way in
 * September has an ANNUAL NET OF ZERO — every figure foots, every total is
 * correct, and the return is silently wrong about the one thing it exists to say.
 *
 * So this probe builds exactly that year and asserts the report REFUSES to call
 * it clean: net zero, GROSS 10,000, `offsetting` true, and both months NAMED.
 *
 * ⛔ THE BREAK IS REAL AND NOT A STUB. cored's GSTR-1 query carries
 * `AND vv.taxable_value IS NOT NULL`, and GSTR-3B reads the same denormalised GST
 * columns — so a SALES voucher keyed WITHOUT a `gst` block is invisible to both
 * RETURNS. PostingTaxTotals has no such filter and counts its postings on the
 * output-tax ledgers. A tax adjustment keyed on a sales voucher with no GST block
 * therefore lands in the BOOKS and in neither RETURN, which is an ordinary
 * bookkeeping mistake with an extraordinary consequence.
 *
 * ⚠️ AND THE PARTY LEG IS ALWAYS A DEBIT. cored REFUSES a Sales voucher whose
 * party posting is a credit — it would turn a receivable into a payable and it
 * would BALANCE, so no total would ever disagree with it. The direction is
 * carried by the SALES leg instead.
 *
 * ===========================================================================
 * ⛔ THE CONTROL PROBES, AND WHAT EACH ONE DISPROVES
 * ===========================================================================
 *  1. THE CLERK IS REALLY A MEMBER. An invite never CLAIMED via GET /v1/me
 *     yields a token that is 403 on EVERY company route — satisfying every
 *     refusal below while proving nothing. That mistake has been made here
 *     before. `the clerk still reads the Trial Balance` disproves it.
 *  2. ⛔ THE CLERK CAN READ THE MONTHLY GSTR-1. This is the control that makes
 *     the probe about MIGRATION 0087 rather than about GST reads in general. No
 *     migration ever refused `report:gstr1`, and 0011's coarse grant reaches it
 *     exactly as it reaches `report:gstr9`. Without this arm, the 403 on /gstr9
 *     would be equally consistent with GST being closed off wholesale.
 *  3. THE OWNER'S REFUSALS ARE NOT THE CLERK'S. The Owner is refused the bare
 *     report too — with 400s about the WINDOW and the LEDGERS, never a 403 — so
 *     the clerk's 403 is the ACL and not a malformed request.
 *  4. ⭐ A CLEAN YEAR IS REPORTED AS CLEAN. The second financial year carries an
 *     ordinary GST invoice and no adjustment, and must come back agreeing. A
 *     detector that fires on everything is as useless as one that never fires,
 *     and without this arm the offsetting assertion could pass on a report that
 *     always says "offsetting".
 *
 * ⚠️ THE `print` ARM IS LABELLED AS PRE-EXISTING. Data Entry was ALREADY 403 on
 * the print form before migration 0087: 0038 seeds no `allow print report:`,
 * 0011's coarse grant is action 'display' only, and the level's default_access is
 * 'none'. It passes with or without 0087 and proves nothing about it on its own.
 * Reporting it as an equal win would be half a lie.
 *
 * ⛔ AND THE OWNER'S PRINT ANSWER IS 501, NOT 200. Printing the annual return is
 * unimplemented on purpose — on screen its ten unconditional disclosures sit
 * beside the figures, and on paper a tidy annual return on company stationery
 * with nowhere for ten caveats is a document somebody hands to a tax officer.
 * Asserting 200 would fail on correct code.
 *
 * RUN:  node frontend/scripts/drive-t12-gstr9.mjs      (takes NO CO — it makes one)
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

const stamp = Math.random().toString(36).slice(2, 8);
const CO = crypto.randomUUID();
const OWNER_SUB = `t12-owner-${stamp}`;
const CLERK = `t12-clerk-${stamp}`;
const CLERK_PW = "t12-clerk-pw-2026";
const COMPANY_NAME = `T12 GSTR9 Co ${stamp}`;
const base = `/v1/companies/${CO}`;

// THE TWO YEARS. The first carries the offsetting break; the second is the
// CONTROL and carries an ordinary invoice and nothing wrong.
const FY1 = { from: "2026-04-01", to: "2027-03-31", label: "2026-27" };
const FY2 = { from: "2027-04-01", to: "2028-03-31", label: "2027-28" };

const OWNER = await mint(OWNER_SUB);

{
  const r = await j("POST", "/v1/companies", {
    token: OWNER,
    body: {
      id: CO,
      name: COMPANY_NAME,
      fy_start_date: FY1.from,
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
      "T12: ⛔ CONTROL — the clerk IS a member and still reads the Trial Balance",
      r.status === 200,
      `trial-balance = ${r.status} (403 here would mean the invite was never ` +
        `claimed and every refusal below is vacuous)`,
    );
  }

  // =========================================================================
  // CONTROL 2 — ⛔ THE ONE THAT MAKES THIS PROBE ABOUT 0087.
  // =========================================================================
  {
    const r = await j("GET", `${base}/gstr1?from=2026-07-01&to=2026-07-31`, {
      token: CLERK_TOK,
    });
    rec(
      "T12: ⛔ CONTROL — the clerk reads the MONTHLY GSTR-1, so GST is not closed wholesale",
      r.status === 200,
      `gstr1 = ${r.status} (a 403 here would make the /gstr9 refusal below ` +
        `attributable to GST reads in general rather than to migration 0087)`,
    );
  }

  // =========================================================================
  // THE MASTERS AND THE BOOKS.
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
  L.ocgst = await mkLedger("Output CGST", "Duties & Taxes");
  L.osgst = await mkLedger("Output SGST", "Duties & Taxes");
  L.oigst = await mkLedger("Output IGST", "Duties & Taxes");
  L.icgst = await mkLedger("Input CGST", "Duties & Taxes");
  L.isgst = await mkLedger("Input SGST", "Duties & Taxes");
  L.iigst = await mkLedger("Input IGST", "Duties & Taxes");
  L.sales = await mkLedger("Domestic Sales", "Sales Accounts");
  L.customer = await mkLedger("T12 Customer", "Sundry Debtors");

  const ledgerQuery =
    `cgst_ledger_id=${L.ocgst}&sgst_ledger_id=${L.osgst}&igst_ledger_id=${L.oigst}` +
    `&input_cgst_ledger_id=${L.icgst}&input_sgst_ledger_id=${L.isgst}` +
    `&input_igst_ledger_id=${L.iigst}`;

  /** An ORDINARY intra-state GST invoice: 18% on `taxable`, seen by both returns. */
  const gstSale = async (no, date, taxable) => {
    const half = String(Math.round(Number(taxable) * 0.09));
    const total = String(Number(taxable) + Number(half) * 2);
    const r = await j("POST", `${base}/vouchers/sales`, {
      token: OWNER,
      body: {
        voucher_no: no,
        voucher_date: date,
        created_by: OWNER_SUB,
        party: "T12 Customer",
        party_ledger_id: L.customer,
        postings: [
          { ledger_id: L.customer, dr_cr: "D", amount: total },
          { ledger_id: L.sales, dr_cr: "C", amount: taxable },
          { ledger_id: L.ocgst, dr_cr: "C", amount: half },
          { ledger_id: L.osgst, dr_cr: "C", amount: half },
        ],
        gst: {
          supplier_state_code: "29",
          place_of_supply_state_code: "29",
          lines: [{ taxable_value: taxable, gst_rate_pct: "18", hsn: "8471" }],
          cgst_ledger_id: L.ocgst,
          sgst_ledger_id: L.osgst,
          igst_ledger_id: L.oigst,
        },
      },
    });
    if (r.status !== 201)
      throw new Error(`gst sale ${no}: ${r.status} ${r.text}`);
  };

  /**
   * A SALES voucher with NO `gst` block that moves the output-tax ledger.
   * Invisible to both returns, visible to the books — see the file header.
   */
  const untaxedAdjustment = async (no, date, amount, credit) => {
    const party = "10000";
    const postings = [{ ledger_id: L.customer, dr_cr: "D", amount: party }];
    if (credit) {
      postings.push(
        {
          ledger_id: L.sales,
          dr_cr: "C",
          amount: String(Number(party) - Number(amount)),
        },
        { ledger_id: L.ocgst, dr_cr: "C", amount },
      );
    } else {
      postings.push(
        { ledger_id: L.ocgst, dr_cr: "D", amount },
        {
          ledger_id: L.sales,
          dr_cr: "C",
          amount: String(Number(party) + Number(amount)),
        },
      );
    }
    const r = await j("POST", `${base}/vouchers/sales`, {
      token: OWNER,
      body: {
        voucher_no: no,
        voucher_date: date,
        created_by: OWNER_SUB,
        party: "T12 Customer",
        party_ledger_id: L.customer,
        postings,
      },
    });
    if (r.status !== 201)
      throw new Error(`untaxed adjustment ${no}: ${r.status} ${r.text}`);
  };

  // FY 2026-27 — the OFFSETTING year. One ordinary invoice so the year is not
  // empty, then the two adjustments that cancel across May and September.
  await gstSale("T12/1", "2026-05-05", "100000");
  await untaxedAdjustment("T12/ADJ-MAY", "2026-05-28", "5000", true);
  await untaxedAdjustment("T12/ADJ-SEP", "2026-09-18", "5000", false);

  // FY 2027-28 — the CONTROL year. An ordinary invoice and nothing wrong.
  await gstSale("T12/2", "2027-06-10", "50000");

  // =========================================================================
  // ⛔ THE ARM THAT GOES RED-TO-GREEN ON MIGRATION 0087.
  // =========================================================================
  {
    const r = await j("GET", `${base}/gstr9?from=${FY1.from}&to=${FY1.to}`, {
      token: CLERK_TOK,
    });
    rec(
      "T12: ⛔ the seeded Data Entry clerk is REFUSED the annual return (0087)",
      r.status === 403,
      `gstr9 = ${r.status} — a 400 here means the clerk PASSED the PDP and ` +
        `reached the handler, which is what the missing rule looks like`,
    );
  }

  // =========================================================================
  // CONTROL 3 — the Owner's refusals are about the REQUEST, never permission.
  // =========================================================================
  {
    const r = await j("GET", `${base}/gstr9?from=2026-06-01&to=2026-08-31&${ledgerQuery}`, {
      token: OWNER,
    });
    rec(
      "T12: the Owner is refused a NON-financial-year window, with a 400 not a 403",
      r.status === 400,
      `quarter window = ${r.status}: there is no GSTR-9 for June to August, and ` +
        `one that existed would be a statutory-looking document about nothing`,
    );
    rec(
      "T12: …and the refusal says what to read INSTEAD (the monthly GSTR-3B)",
      /GSTR-3B/i.test(r.text),
      (r.text.match(/.{0,50}GSTR-3B.{0,60}/i) ?? ["(no alternative named)"])[0],
    );
  }
  {
    const r = await j("GET", `${base}/gstr9?from=${FY1.from}&to=${FY1.to}`, {
      token: OWNER,
    });
    rec(
      "T12: ⛔ the Owner is refused a GSTR-9 with NO tax ledgers, and all six are named",
      r.status === 400 &&
        ["cgst_ledger_id", "sgst_ledger_id", "igst_ledger_id",
         "input_cgst_ledger_id", "input_sgst_ledger_id", "input_igst_ledger_id"]
          .every((p) => r.text.includes(p)),
      `= ${r.status}; without them the books column is ZERO for all twelve ` +
        `months and every period reads as a finding`,
    );
  }

  // =========================================================================
  // ⭐⭐ THE ORACLE — TWO MONTHS THAT CANCEL ARE NOT REPORTED AS AGREEMENT.
  // =========================================================================
  const annual = async (fy) => {
    const r = await j("GET", `${base}/gstr9?from=${fy.from}&to=${fy.to}&${ledgerQuery}`, {
      token: OWNER,
    });
    if (r.status !== 200)
      throw new Error(`gstr9 ${fy.label}: ${r.status} ${r.text}`);
    return r.body;
  };

  const bad = await annual(FY1);
  {
    const d = bad.gstr9.outward_r3b_vs_books;
    rec(
      "T12: the offsetting year's ANNUAL NET is zero — the fixture cancels, as designed",
      Number(d.net.camt) === 0,
      `net CGST = ${d.net.camt} (if this is non-zero the fixture is wrong and ` +
        `the arm below proves nothing)`,
    );
    rec(
      "T12: ⭐⭐ …and the GROSS divergence is 10,000, so the year is NOT reported as clean",
      Number(d.gross.camt) === 10000,
      `gross CGST = ${d.gross.camt} — a gross reading ZERO here means every ` +
        `offsetting pair of errors in a taxpayer's year is invisible, and this ` +
        `report certifies a clean year over 10,000 of disagreement`,
    );
    rec(
      "T12: …and `offsetting` is TRUE, which is what makes the screen print the months",
      d.offsetting === true,
      `offsetting = ${d.offsetting}`,
    );
    rec(
      "T12: …and BOTH disagreeing months are NAMED, so a reader can go and look",
      (d.months ?? []).join(",") === "052026,092026",
      `months = ${JSON.stringify(d.months ?? [])}`,
    );
    rec(
      "T12: …and `agrees` is FALSE — agreement is per-month and never read off the net",
      d.agrees === false,
      `agrees = ${d.agrees}`,
    );
    rec(
      "T12: ⛔ …and the envelope's `reconciled` is FALSE",
      bad.reconciled === false,
      `reconciled = ${bad.reconciled}`,
    );
    rec(
      "T12: …and the `monthly-differences-offset` disclosure fired",
      (bad.gstr9.disclosures ?? []).includes("monthly-differences-offset"),
      `disclosures = ${(bad.gstr9.disclosures ?? []).length}`,
    );
    // ⚠️ AND THE TWO RETURNS STILL AGREE WITH EACH OTHER. That is what makes the
    // finding ACTIONABLE: the problem is in the books, not in the filing, and a
    // report that flagged all three comparisons would send an accountant to
    // re-check two returns that are fine.
    rec(
      "T12: …while GSTR-1 and GSTR-3B still AGREE — the break is in the books, and it says so",
      bad.gstr9.outward_r1_vs_r3b.agrees === true,
      `r1-vs-3b agrees = ${bad.gstr9.outward_r1_vs_r3b.agrees}`,
    );
  }

  // =========================================================================
  // CONTROL 4 — ⭐ a CLEAN year with real trading in it is reported as clean.
  // =========================================================================
  const good = await annual(FY2);
  {
    const d = good.gstr9.outward_r3b_vs_books;
    rec(
      "T12: ⛔ CONTROL — a year with a real invoice and no adjustment RECONCILES",
      good.reconciled === true && d.agrees === true && !d.offsetting,
      `reconciled=${good.reconciled} agrees=${d.agrees} offsetting=${d.offsetting} ` +
        `— a detector that fires on every year is as useless as one that never fires`,
    );
    const turnover = good.gstr9.months.reduce(
      (t, m) => t + Number(m.r1_tax.camt),
      0,
    );
    rec(
      "T12: ⛔ CONTROL — …and that clean year is NOT clean merely by being empty",
      turnover > 0,
      `annual GSTR-1 CGST = ${turnover} (an empty year reconciles perfectly and ` +
        `would satisfy the arm above while proving nothing)`,
    );
    rec(
      "T12: …and it is STILL not filing-ready — reconciling is not the same as filable",
      (good.gstr9.disclosures ?? []).includes("not-filing-ready"),
      `disclosures = ${JSON.stringify(good.gstr9.disclosures ?? [])}`,
    );
  }

  // =========================================================================
  // THE HONESTY RULES ON THE TABLES.
  // =========================================================================
  {
    const t = (no) => (bad.gstr9.tables ?? []).find((x) => x.no === no);
    const t7 = t("7");
    rec(
      "T12: ⛔ table 7 (ITC reversed) is NOT_DERIVED and carries NO rows",
      Boolean(t7) && t7.status === "NOT_DERIVED" && !(t7.rows ?? []).length,
      `status=${t7?.status} rows=${(t7?.rows ?? []).length} — a ZERO there would ` +
        `assert the taxpayer reversed NOTHING, when the truth is nothing was checked`,
    );
    rec(
      "T12: …and it NAMES the missing input rather than leaving a blank",
      /inward statement/i.test(t7?.reason ?? ""),
      (t7?.reason ?? "(no reason)").slice(0, 110),
    );
    const t4 = t("4");
    rec(
      "T12: ⛔ table 4's TOTAL is suppressed, with the reason printed in its place",
      Boolean(t4) && t4.total_suppressed === true &&
        (t4.total_suppressed_reason ?? "").length > 0,
      `suppressed=${t4?.total_suppressed} reason=${(t4?.total_suppressed_reason ?? "").slice(0, 70)}…`,
    );
    rec(
      "T12: …and it names the sub-rows it could not produce",
      (t4?.omitted ?? []).length >= 4 &&
        (t4?.omitted ?? []).every((o) => (o.reason ?? "").length > 0),
      `omitted rows = ${(t4?.omitted ?? []).length}, each with a reason`,
    );
    const notDerived = (bad.gstr9.tables ?? []).filter(
      (x) => x.status === "NOT_DERIVED",
    );
    rec(
      "T12: every NOT_DERIVED table states WHY, and none of them smuggles in a row",
      notDerived.length >= 10 &&
        notDerived.every((x) => (x.reason ?? "").length > 0 && !(x.rows ?? []).length),
      `${notDerived.length} of ${(bad.gstr9.tables ?? []).length} tables are not derived`,
    );
  }

  // =========================================================================
  // THE DISCLOSURES — ALL of them, not a subset.
  // =========================================================================
  {
    const want = [
      "advances-not-modelled",
      "amendments-not-modelled",
      "annual-table-structure-unverified",
      "books-carry-no-taxable-value",
      "exempt-and-zero-rated-not-marked",
      "itc-reversals-not-computed",
      "no-annual-2a",
      "no-cash-or-credit-ledger",
      "not-filing-ready",
      "portal-schema-unverified",
    ];
    const got = bad.gstr9.disclosures ?? [];
    const missing = want.filter((w) => !got.includes(w));
    rec(
      "T12: ⛔ all TEN unconditional disclosures are on every response",
      missing.length === 0,
      missing.length ? `missing: ${missing.join(", ")}` : `${got.length} tokens`,
    );
    rec(
      "T12: …including the one that says the table structure was never verified",
      got.includes("annual-table-structure-unverified"),
      `RESEARCH.md has no GSTR-9 material and the portal is geo-fenced from here`,
    );
  }

  // =========================================================================
  // THE TWELVE-MONTH GRID.
  // =========================================================================
  {
    const fps = (bad.gstr9.months ?? []).map((m) => m.fp).join(",");
    rec(
      "T12: the grid is TWELVE months, April to March, including the empty ones",
      fps ===
        "042026,052026,062026,072026,082026,092026,102026,112026,122026,012027,022027,032027",
      `periods = ${fps || "(none)"} — a month missing from a list of twelve is a ` +
        `month nobody checked`,
    );
    const broken = (bad.gstr9.months ?? []).filter((m) => !m.r3b_matches_books);
    rec(
      "T12: …and the per-month verdicts agree with the annual divergence",
      broken.map((m) => m.fp).join(",") === "052026,092026",
      `months flagged on the grid = ${broken.map((m) => m.fp).join(",") || "(none)"}`,
    );
  }

  // =========================================================================
  // PRINT. ⚠️ The clerk arm is PRE-EXISTING; only the Owner's 501 is news.
  // =========================================================================
  {
    const c = await j("GET", `${base}/print/reports/gstr9`, { token: CLERK_TOK });
    rec(
      "T12: ⚠️ PRE-EXISTING — the clerk is 403 on the print form (true before 0087 too)",
      c.status === 403,
      `print = ${c.status}; 0038 seeds no allow-print rule, so this passes with ` +
        `or without 0087 and proves nothing about it on its own`,
    );
    const o = await j("GET", `${base}/print/reports/gstr9`, { token: OWNER });
    rec(
      "T12: ⛔ the OWNER gets 501 on the print form, and it NAMES the report",
      o.status === 501 && /gstr9/.test(o.text),
      `print = ${o.status}: an annual return on paper has nowhere to put ten ` +
        `disclosures, and a blank page that reads as filed is worse than a refusal`,
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
      await page.keyboard.type("GSTR-9");
      await page.waitForTimeout(800);
      await press("Enter", 4000);

      const h = await heading();
      const seen = (await body()).replace(/\s+/g, " ");
      rec(
        "T12: the clerk REACHES GSTR-9 from the keyboard (Alt+G)",
        h === "GSTR-9 Annual Return",
        `h1="${h}"`,
      );
      rec(
        "T12: ⛔ …and the refusal is a DECISION naming the permission",
        (await page.getByTestId("gstr9-denied").count()) > 0 &&
          /report:gstr9/.test(seen),
        (seen.match(/.{0,40}report:gstr9.{0,80}/) ?? [
          "(no decision panel — it rendered something else)",
        ])[0],
      );
      rec(
        "T12: …and it says reversing it means DELETING the disallow, not adding an allow",
        /DELETING/.test(seen),
        (seen.match(/.{0,30}DELETING.{0,80}/) ?? ["(absent)"])[0],
      );
      rec(
        "T12: …and NOT an empty grid that reads as “a year in which nothing happened”",
        (await page.getByTestId("gstr9-months").count()) === 0 &&
          !/\bNaN\b/.test(seen),
        "no month grid drawn under the refusal, no NaN",
      );
    } finally {
      await app.browser?.close?.();
    }
  }
} finally {
  // =========================================================================
  // ⛔ REAP, IN A `finally`. §5.14 — a probe that creates must clean up, and must
  // ASSERT it worked. This tenant HAS books, so the delete list is the long one.
  // =========================================================================
  const q = (s) => `'${String(s).replace(/'/g, "''")}'`;
  psql(
    `begin;
     -- THE TRIGGERS MUST BE DISARMED FOR THE DURATION, and this is not a
     -- shortcut: posting_immutable RAISES on DELETE, so a tenant WITH BOOKS
     -- cannot be reaped without it. internal/platform/testdb does exactly this
     -- for the same reason, and only a superuser may set it -- which is why this
     -- reap connects as postgres and not as tally_app.
     --
     -- ⚠️ SET LOCAL, so it lasts only to COMMIT. A session-wide SET would leave
     -- the shared dev database with immutability disarmed for whatever ran next
     -- on that connection, which is far worse than a leaked tenant.
     set local session_replication_role = replica;
     delete from budget_line          where company_id = ${q(CO)};
     delete from budget               where company_id = ${q(CO)};
     delete from posting_cost_allocation where company_id = ${q(CO)};
     delete from cost_centre          where company_id = ${q(CO)};
     delete from cost_category        where company_id = ${q(CO)};
     delete from voucher_gst_line     where company_id = ${q(CO)};
     delete from bill_allocation      where company_id = ${q(CO)};
     delete from posting              where company_id = ${q(CO)};
     -- ⛔ HEAD BEFORE VERSION, AND THE OBVIOUS ALTERNATIVE DOES NOT WORK.
     -- voucher_head.current_version_id is NOT NULL and points at voucher_version,
     -- so "null it out, then delete the versions" fails on the not-null
     -- constraint -- which session_replication_role does NOT suppress (it disarms
     -- TRIGGERS, not CHECK/NOT NULL). Deleting the head first satisfies the FK
     -- because there is then nothing left pointing at the versions.
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
    "T12: ⛔ the probe reaped its own tenant, and asserted it worked",
    left === "0",
    `companies matching ${CO.slice(0, 8)}…: ${left}`,
  );
}

process.exit(summary());
