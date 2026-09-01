/**
 * PAYROLL — the whole module, driven from the keyboard, end to end.
 *
 * ⛔ WHAT THIS PROVES, AND WHY IT NEEDED A BROWSER
 * ===========================================================================
 * Before this wave the biggest module in the product was INVISIBLE: eight
 * payroll routes had been mounted and PDP-mapped since Track 3, every one of
 * them was tested, and `frontend/src/screens/` contained no payroll screen at
 * all. And two routes did not exist AT ALL — the salary structure and the
 * statutory election — so an employee created over HTTP could never be paid by
 * anybody: `POST /vouchers/payroll` answered 422 ("no statutory election is in
 * force") for every caller that had not reached into the Go service directly.
 * Every payroll test in `internal/server` does reach in, which is precisely why
 * a green suite never noticed.
 *
 * So this probe performs the ENTIRE task an operator performs, with nothing but
 * keystrokes and clicks in a real browser:
 *
 *   create an employee -> give them a salary and an election -> post a pay run
 *   -> read the register -> drill into a payslip -> download the ECR, the ESI
 *   return and Form 138.
 *
 * ⛔ THE ORACLE IS POSTGRES, WHICH SHARES NO CODE WITH THE SCREENS. A screen
 * that reported "posted" from a status line looks identical whether a voucher
 * exists or not — the D5/D43 shape, where `allow_negative_stock` was accepted at
 * 201 and thrown away. So every claim the UI makes here is checked against the
 * `payslip`, `pay_run` and `voucher_version` rows directly.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY OF ITS OWN, NOT ON Demo Traders. A pay run
 * posts a real balanced voucher, which MOVES THE TRIAL BALANCE — and residue in
 * the demo tenant reads as a broken Balance Sheet to the next lane and drops
 * `verify:demo` below 34/34 (the lesson `drive-l13-masters.mjs` learned the hard
 * way). Creating a tenant costs one request and leaves the demo books untouched,
 * so there is nothing to tear down and nothing to get wrong in the teardown.
 *
 * RUN:  node frontend/scripts/drive-payroll.mjs
 *       (needs cored :8087 and vite :5173. It creates its own company.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Payroll Drive ${stamp}`;
const CODE = `E-${stamp.toUpperCase()}`;
const NAME = "Asha Rao";
/** A month in the past, so nothing depends on today's date. */
const MONTH = "2026-07";
// A month this drive deliberately never runs payroll for, so the register has
// an honest empty case to answer. See section 6b (D112).
const UNRUN_MONTH = "2026-09";

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
  return { status: r.status, body: parsed };
};

const sql = async (query) => {
  const { execFile } = await import("node:child_process");
  return new Promise((res, rej) => {
    execFile(
      "psql",
      ["-h", "127.0.0.1", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", query],
      { env: { ...process.env, PGPASSWORD: "postgres" } },
      (err, stdout) => (err ? rej(err) : res(stdout.trim())),
    );
  });
};
const q = (s) => `'${String(s).replace(/'/g, "''")}'`;

// ---- The tenant. Created over the API as the SAME subject the browser signs
// in as, so the browser is an Owner of it.
const TOK = (
  await j("POST", "/auth/token", {
    body: { subject: "browser-dev-owner", amr: ["webauthn"] },
  })
).body.token;
const created = await j("POST", "/v1/companies", {
  token: TOK,
  body: { name: COMPANY_NAME, fy_start_date: "2026-04-01", state_code: "29" },
});
if (created.status !== 201 && created.status !== 200) {
  throw new Error(
    `drive-payroll: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
if (!CO) throw new Error(`drive-payroll: no company id in ${JSON.stringify(created.body)}`);
console.log(`company ${COMPANY_NAME} = ${CO}`);

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, altMac, toGateway, openFromGateway, net } = app;

const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const text = async (t) =>
  (await seen(t)) ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim() : "";
const type = async (t, v) => {
  const el = testid(t).first();
  await el.click();
  await el.fill(v);
};
const choose = async (t, v) => {
  await testid(t).first().selectOption(v);
  await page.waitForTimeout(150);
};

/** Every file the browser was actually handed. The download oracle. */
const downloads = [];
page.on("download", (d) => downloads.push(d.suggestedFilename()));

try {
  // =========================================================================
  // 1. THE EMPLOYEE MASTER — created from the keyboard.
  // =========================================================================
  await openFromGateway("Employees");
  rec(
    "Gateway > Employees opens the employee master (and NOT an 'unwired report')",
    (await body()).includes("The people on the payroll") &&
      !(await body()).includes("not available in this build"),
  );

  await altMac("c");
  rec("Alt+C opens the create form in a real browser", await seen("employee-form"));

  await type("employee-code", CODE);
  await type("employee-name", NAME);
  await type("employee-doj", "2025-06-01");
  await type("employee-pan", "ABCPD1234E");
  await type("employee-uan", "100200300455");
  await type("employee-ip", "1234567855");
  await press("Control+a", 1200);

  const dbEmployee = await sql(
    `SELECT id::text FROM employee WHERE company_id = ${q(CO)} AND code = ${q(CODE)}`,
  );
  // ⛔ THE ORACLE. The status line said "created"; this is the row.
  rec("the employee is in the DATABASE, not just on the status line", dbEmployee !== "", dbEmployee);

  const notice = await text("employees-notice");
  rec(
    "the confirmation says the employee CANNOT BE PAID YET",
    /cannot be paid yet/i.test(notice) && /Alt\+S/.test(notice),
    notice.slice(0, 120),
  );
  rec(
    "the list marks them unpayable and NAMES both missing halves",
    /salary structure/i.test(await text(`employee-unpayable:${CODE}`)) &&
      /statutory election/i.test(await text(`employee-unpayable:${CODE}`)),
    await text(`employee-unpayable:${CODE}`),
  );

  // =========================================================================
  // 2. THE TWO CHILDREN — the routes that did not exist before this wave.
  // =========================================================================
  await testid(`employee-row:${CODE}`).first().click();
  await page.waitForTimeout(200);
  await altMac("s");
  rec("Alt+S opens the pay panel for the highlighted employee", await seen("employee-pay-panel"));

  await type("component-from", "2026-04-01");
  await type("component-code", "Basic");
  await choose("component-type", "BASIC");
  await type("component-amount", "18000");
  await page.getByRole("button", { name: "Add this component" }).click();
  await page.waitForTimeout(1200);

  const dbComponent = await sql(
    `SELECT amount::text FROM employee_salary_structure
      WHERE company_id = ${q(CO)} AND employee_id = ${q(dbEmployee)}`,
  );
  rec(
    "the salary component reached employee_salary_structure",
    dbComponent.startsWith("18000"),
    dbComponent,
  );

  // The panel re-reads after a save, so re-open it for the election.
  if (!(await seen("election-pf-base"))) {
    await testid(`employee-row:${CODE}`).first().click();
    await page.waitForTimeout(200);
    await altMac("s");
  }
  await type("election-from", "2026-04-01");
  await choose("election-pf-base", "CEILING");
  await choose("election-regime", "NEW");
  await testid("election-eps").first().check();
  await testid("election-esi").first().check();
  await page.getByRole("button", { name: "Record this election" }).click();
  await page.waitForTimeout(1200);

  const dbElection = await sql(
    `SELECT pf_base FROM employee_statutory_election
      WHERE company_id = ${q(CO)} AND employee_id = ${q(dbEmployee)}`,
  );
  rec(
    "the statutory election reached employee_statutory_election",
    dbElection === "CEILING",
    dbElection,
  );

  rec(
    "the list now says the employee CAN be paid",
    await seen(`employee-payable:${CODE}`),
    await text(`employee-payable:${CODE}`),
  );

  // ---- A SECOND EMPLOYEE, OVER THE API, AND HERE IS WHY.
  //
  // ⛔ FORM 138 CANNOT EXIST FOR THE EMPLOYEE ABOVE, AND THAT IS THE SERVER
  // BEING RIGHT. On 18,000 a month the rebate takes the tax to nil, so a challan
  // for that month has NO DEDUCTEE — and cored refuses the statement: "a challan
  // with no deductee is a statement the department cannot reconcile". Driving
  // Form 138's SUCCESS path therefore needs somebody who actually pays salary
  // tax, and that is what this employee is for.
  //
  // ⚠️ IT USES THE API AND NOT THE KEYBOARD, DELIBERATELY AND ONLY HERE. The
  // keyboard path for all three writes is proven above, against the database;
  // repeating it buys nothing, and this person exists solely so the quarter has
  // tax in it. Every ASSERTION below still reads a screen or the books.
  const HIGH = `${CODE}-HI`;
  const hi = await j("POST", `/v1/companies/${CO}/employees`, {
    token: TOK,
    // ⛔ THE UAN IS NOT OPTIONAL FOR THE ECR AND THAT IS THE SERVER BEING RIGHT
    // AGAIN. Without it cored refuses the whole file, naming the employee —
    // "employee <code> has no UAN" — because the EPFO portal parses the member
    // list line by line and a line with no member number credits nobody's
    // account. Found by driving it: adding this taxpayer without a UAN broke the
    // ECR arm, which is exactly the refusal a real operator meets first.
    body: {
      code: HIGH, name: "Vikram Shah", doj: "2025-06-01",
      pan: "ABCPD9999F", uan: "100200300477", esic_ip_number: "1234567877",
    },
  });
  const HI_ID = hi.body.employee_id;
  const hiComp = await j("POST", `/v1/companies/${CO}/employees/${HI_ID}/salary-components`, {
    token: TOK,
    body: { effective_from: "2026-04-01", code: "Basic", type: "BASIC", amount: "300000" },
  });
  const hiElect = await j("POST", `/v1/companies/${CO}/employees/${HI_ID}/statutory-elections`, {
    token: TOK,
    body: {
      effective_from: "2026-04-01",
      pf_base: "CEILING",
      eps_member: true,
      esi_applicable: false,
      regime: "NEW",
    },
  });
  rec(
    "a second, TAX-PAYING employee exists for the Form 138 arm",
    hi.status === 201 && hiComp.status === 201 && hiElect.status === 201,
    `${hi.status}/${hiComp.status}/${hiElect.status} ${JSON.stringify(hiComp.body)}`,
  );

  // =========================================================================
  // 3. THE PAY RUN — Ctrl+F4, the key that said "not built yet" for the whole
  //    life of the project while the engine and eight routes were shipped.
  // =========================================================================
  await toGateway();
  await press("Control+F4", 1200);
  rec("Ctrl+F4 opens the Pay Run screen", (await body()).includes("Pay Run"));

  await type("pay-run-month", MONTH);
  await press("Control+a", 2500);
  rec("the pay run posted", await seen("pay-run-posted"), await text("pay-run-posted"));

  // ⛔ THE ORACLE, AND IT IS THE VOUCHER RATHER THAN THE PAY RUN ROW. A pay run
  // that recorded itself and posted nothing is the whole class of silent defect
  // here: the screen would look identical and the books would be short by a
  // month's payroll.
  const dbNet = await sql(
    `SELECT p.net_pay::text FROM payslip p
      WHERE p.company_id = ${q(CO)} AND p.employee_id = ${q(dbEmployee)}`,
  );
  rec(
    "a payslip row exists with the hand-derived net of 16065",
    dbNet.startsWith("16065"),
    `net_pay=${dbNet} (18,000 gross less EPF 1,800 and ESI 135 — derived from the published rates, not read off the screen)`,
  );
  // ⛔ AND SOMEBODY MUST ACTUALLY HAVE PAID TAX, or the Form 138 arm below would
  // be asserting against a quarter with no deductee — which the server refuses,
  // correctly, and which would make that arm's failure unreadable.
  const dbTDS = await sql(
    `SELECT COALESCE(SUM(tds), 0)::text FROM payslip WHERE company_id = ${q(CO)}`,
  );
  rec("the run deducted salary TDS from somebody", Number(dbTDS) > 0, `sum(tds)=${dbTDS}`);

  // The voucher itself, joined to its TYPE by name: `voucher_version` carries a
  // voucher_type_id and not a class string, so the class comes from the type
  // master — which is also the check that the pay run posted under the payroll
  // type rather than under whatever type happened to resolve.
  const dbVoucher = await sql(
    `SELECT vv.voucher_no || ' / ' || vt.name
       FROM voucher_version vv
       JOIN voucher_type vt ON vt.id = vv.voucher_type_id
      WHERE vv.company_id = ${q(CO)}
      ORDER BY vv.created_at DESC LIMIT 1`,
  );
  rec(
    "the pay run POSTED a real voucher, under the PAYROLL voucher type",
    /payroll/i.test(dbVoucher),
    dbVoucher,
  );
  // ⛔ AND IT IS BALANCED. A pay run that recorded itself and posted an
  // unbalanced entry is the whole class of silent defect here: the screen looks
  // identical and the Trial Balance is short by a month's payroll.
  const dbBalance = await sql(
    `SELECT COALESCE(SUM(CASE WHEN dr_cr = 'D' THEN amount ELSE -amount END), 0)::text
       FROM posting WHERE company_id = ${q(CO)}`,
  );
  rec(
    "the posted payroll voucher BALANCES to the paisa",
    Number(dbBalance) === 0,
    `sum(Dr) - sum(Cr) = ${dbBalance}`,
  );

  // =========================================================================
  // 4. THE REGISTER, AND THE PAYSLIP BEHIND IT.
  // =========================================================================
  await openFromGateway("Payroll Register");
  rec(
    "Gateway > Payroll Register opens the register, NOT an unwired report frame",
    !(await body()).includes("not available in this build"),
  );
  await type("payroll-register-month", MONTH);
  await page.getByRole("button", { name: "Show the register" }).click();
  await page.waitForTimeout(1500);

  rec(
    "the register shows this employee's row",
    await seen(`payroll-register-row:${CODE}`),
    await text(`payroll-register-row:${CODE}`),
  );
  const rulesets = await text("payroll-register-rulesets");
  // ⛔ A PAYROLL FIGURE WITH NO STATEMENT OF WHICH LAW PRODUCED IT cannot be
  // checked by anybody who was not present when it was run.
  rec(
    "the register names the rulesets that produced the figures",
    /ss-\d{4}-\d{2}/.test(rulesets) && /it-[\w-]*\d{4}/.test(rulesets),
    rulesets,
  );

  await testid(`payroll-register-row:${CODE}`).first().click();
  await page.waitForTimeout(200);
  await press("Enter", 1800);
  const who = await text("payslip-who");
  rec("Enter on the row opens THAT employee's payslip", who.includes(CODE) && who.includes(MONTH), who);
  rec(
    "the payslip's net matches the books",
    (await text("payslip-net")).replace(/[^\d.]/g, "").startsWith("16065"),
    await text("payslip-net"),
  );

  // =========================================================================
  // 5. THE STATUTORY FILES — and the disclosure they cannot be separated from.
  // =========================================================================
  await press("Escape", 800);
  await altMac("s");
  rec("Alt+S opens the statutory files panel", await seen("statutory-files-panel"));

  await testid("download-ecr").first().click();
  await page.waitForTimeout(2000);
  const ecrNotice = await text("statutory-files-notice");
  rec(
    "the EPFO ECR downloaded",
    downloads.some((f) => /\.zip$/i.test(f)),
    downloads.join(", "),
  );
  // ⛔ THE DISCLOSURE IS THE SERVER'S OWN AND IT REACHES THE OPERATOR. Nothing
  // this product emits has been through the EPFO portal, and a download that
  // reads as a finished filing is the one outcome this must not have.
  rec(
    "the ECR download carries the NOT-VALIDATED disclosure",
    /NOT validator-verified/i.test(ecrNotice) && /not treat this as a filed return/i.test(ecrNotice),
    ecrNotice.slice(0, 160),
  );

  const before = downloads.length;
  await testid("download-esi").first().click();
  await page.waitForTimeout(2000);
  const esiNotice = await text("statutory-files-notice");
  rec(
    "the ESIC monthly return downloaded",
    downloads.length > before,
    `${downloads.length - before} file(s): ${downloads.slice(before).join(", ")}`,
  );
  rec("the ESI download carries its disclosure too", /NOT validator-verified/i.test(esiNotice), esiNotice.slice(0, 120));

  // =========================================================================
  // 6. FORM 138 — a FORM, because not one of its particulars is in the books.
  // =========================================================================
  await openFromGateway("Form 138 (Salary TDS)");
  rec("Gateway > Form 138 opens the statement form", await seen("form138-tan"));

  // First: press Ctrl+A with nothing keyed. It must refuse LOCALLY and name the
  // fields — nothing is defaulted into a filed record.
  await press("Control+a", 900);
  const bare = await text("form138-notice");
  rec(
    "an empty Form 138 is refused, naming what is missing",
    /tax year/i.test(bare) && /TAN/.test(bare) && /nothing is defaulted/i.test(bare),
    bare.slice(0, 140),
  );

  await type("form138-tax-year", "2026-27");
  await choose("form138-quarter", "Q2");
  await type("form138-tan", "BLRA12345B");
  await type("form138-deductor-pan", "AAACP1234A");
  await type("form138-deductor-name", COMPANY_NAME);
  await type("form138-file-date", "2026-10-10");

  // ⛔ FIRST, WITH THE DEDUCTOR COMPLETE AND NO CHALLAN. The server refuses it —
  // "a statement with no challan credits nobody", because Deductee Detail field
  // 5 of the return IS a challan reference — and the screen must say so BEFORE
  // the round trip. It did not, until this drive found it: the form was
  // otherwise complete and the only refusal came back from cored.
  await press("Control+a", 900);
  const noChallan = await text("form138-notice");
  rec(
    "a Form 138 with no challan is refused LOCALLY, with the reason",
    /at least one challan/i.test(noChallan) && /credits nobody/i.test(noChallan),
    noChallan.slice(0, 140),
  );

  await type("challan-month-0", "2026-07");
  await type("challan-bsr-0", "0221826");
  await type("challan-date-0", "2026-08-07");
  await type("challan-serial-0", "12345");
  const before138 = downloads.length;
  await press("Control+a", 2500);
  const f138 = await text("form138-notice");
  rec(
    "Form 138 (Q2) downloaded with its disclosure",
    downloads.length > before138 && /NOT a filed return/i.test(f138),
    f138.slice(0, 160),
  );

  // ⛔ AND Q4 IS REFUSED BY THE SERVER, WITH A REAL REASON. The Annexure II
  // record structure is not published; guessing it would mis-file every
  // employee's whole-year salary detail. Driven, because a refusal nobody has
  // seen is a refusal nobody knows the wording of.
  await choose("form138-quarter", "Q4");
  await press("Control+a", 2500);
  const q4 = await text("form138-notice");
  rec("Q4 is refused rather than guessed at", /refused/i.test(q4), q4.slice(0, 160));

  // =========================================================================
  // 6b. AN UNRUN MONTH IS AN EMPTY STATE, NOT A CRASH  (D112)
  // =========================================================================
  // ⛔ THIS IS THE COMMONEST THING THIS SCREEN HAS TO SAY and it was the one
  // case left to the server's sentence. Before the fix the operator read:
  //
  //   payrolldb: payroll register for 2026-09: payrolldb: pay run not found:
  //   no live pay run for 2026-09
  //
  // — the Go package twice, the sentinel wrapped in its own text, and nothing
  // they could act on. It is driven HERE, last, because everything above needs
  // the register loaded and this deliberately unloads it.
  await openFromGateway("Payroll Register");
  await type("payroll-register-month", UNRUN_MONTH);
  await page.getByRole("button", { name: "Show the register" }).click();
  await page.waitForTimeout(1500);

  rec(
    "an unrun month draws NO error box",
    !(await seen("payroll-register-error")),
    (await seen("payroll-register-error")) ? await text("payroll-register-error") : "no error box",
  );
  const empty = (await seen("payroll-register-empty")) ? await text("payroll-register-empty") : "";
  rec(
    "…it draws an empty state naming the month and where to go next",
    empty.includes(UNRUN_MONTH) && /pay run/i.test(empty),
    empty.slice(0, 160),
  );
  // ⛔ THE ORACLE FOR "NO INTERNALS LEAKED" IS THE RENDERED TEXT, not the
  // absence of a testid: a screen that printed the raw sentence into the empty
  // state would pass every check above.
  rec(
    "…and it leaks no package name or sentinel wording",
    empty !== "" && !/payrolldb|not found/i.test(empty),
    empty.slice(0, 160),
  );
  // The CONTROL: the register still works for the month that WAS run, from the
  // same screen, right after. An empty state that stuck would be a new defect.
  await type("payroll-register-month", MONTH);
  await page.getByRole("button", { name: "Show the register" }).click();
  await page.waitForTimeout(1500);
  rec(
    "…and the month that WAS run still renders from the same screen",
    (await seen(`payroll-register-row:${CODE}`)) && !(await seen("payroll-register-empty")),
    await text(`payroll-register-row:${CODE}`),
  );

  // =========================================================================
  // 7. NOTHING WENT WRONG QUIETLY.
  // =========================================================================
  const bad = net.filter((n) => / 5\d\d$/.test(n));
  rec("no request answered 5xx during the drive", bad.length === 0, bad.join(" | "));

  // ⛔ THE NON-2xx WRITES ARE ENUMERATED RATHER THAN FILTERED OUT, because
  // "ignore the failures" is how a probe stops measuring anything. Exactly two
  // shapes are expected here and both are DESIGNED behaviour:
  //
  //   403 on the FIRST employee POST — the WebAuthn step-up ceremony. Every
  //       first write of a session is refused, the browser completes the
  //       ceremony, and the write is retried. The 201 immediately after it in
  //       the log is that retry.
  //   422 on payroll-form-138 — the refusals this probe ASSERTS ON: the
  //       no-challan statement and Q4.
  //
  // Anything else is a finding.
  const unexpected = net.filter((n) => {
    if (/ 2\d\d$/.test(n)) return false;
    if (/\/employees 403$/.test(n)) return false; // the step-up ceremony
    if (/payroll-form-138 4\d\d$/.test(n)) return false; // asserted above
    return true;
  });
  rec(
    "every non-2xx write is one of the two EXPECTED refusals",
    unexpected.length === 0,
    unexpected.join(" | "),
  );

  // JS errors only. A browser logs "Failed to load resource" for the two
  // statuses above, and those are answers the UI handled and this probe already
  // asserted — an exception thrown by the app is a different thing entirely.
  const errs = app.consoleErrors.filter(
    (e) => !/favicon|Download the React|Failed to load resource/i.test(e),
  );
  rec("no javascript errors in the console", errs.length === 0, errs.slice(0, 2).join(" | "));

  console.log(`\nnon-GET wire traffic:\n  ${net.join("\n  ")}`);
} finally {
  await app.browser.close();
}

process.exit(summary() > 0 ? 1 : 0);
