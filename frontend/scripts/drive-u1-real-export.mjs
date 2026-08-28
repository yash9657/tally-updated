/**
 * U1 — MIGRATE FROM TALLY, AGAINST A **REAL** TALLYPRIME EXPORT.
 *
 * ⛔ THIS IS THE ARM `HANDOFF.md` §10 ITEM 1 CALLS THE COMMERCIAL BLOCKER: a
 * business cannot switch if it cannot bring its books, and until 2026-08-27 this
 * importer had only ever met files this repository wrote itself. The user
 * supplied a genuine pair exported from TallyPrime — `Master.xml` (84 MB
 * UTF-16LE, 3,706 LEDGERs, 37 GROUPs) and `Transactions.xml` (121 VOUCHERs, 226
 * stock lines) — of a real trading company's 2019-20 books.
 *
 * ⛔⛔ THE FILES ARE **NOT** IN THIS REPOSITORY AND MUST NOT BE. They are one
 * business's whole ledger — every customer it sells to, by name, with what they
 * owe. They are read from a path given by `TALLY_MASTERS` / `TALLY_TXNS`
 * (defaulting to `~/Downloads`), and this drive SKIPS ITSELF, loudly, when they
 * are not there. ⚠️ A skip is recorded as a probe that did not run, never as a
 * pass: everything below would be vacuously green against two absent files.
 *
 * WHAT A REAL MIGRATION ACTUALLY LOOKS LIKE, AND IT IS NOT THE HAPPY PATH
 * ===========================================================================
 * Driven on the wire first, so the browser arm knows what it is looking at:
 *
 *   1. `Master.xml` imports CLEANLY — 3,706 ledgers, 9 groups, and **19 real
 *      warnings**, every one of them a fact an accountant has to act on
 *      (opening bill-wise breakups that do not sum to their own balance; an
 *      opening bill on the opposite side to its balance). ⭐ The warnings are
 *      the product working: an operator told only "3,706 ledgers" would believe
 *      the migration was lossless.
 *
 *   2. ⛔⛔ `Transactions.xml` IS THEN REFUSED ENTIRELY — the whole-import abort
 *      on the FIRST voucher, because the export carries no opening stock and
 *      a sale of RICE cannot issue goods that are not there. The message names
 *      two ways forward and refuses to choose.
 *
 *   3. ⛔⛔ AND REMEDY (1) — "import a Masters.xml carrying the items with their
 *      opening balances FIRST" — **IS NOT AVAILABLE TO THIS OPERATOR.** Their
 *      `Master.xml` IS the "All Masters" export (its own REPORTNAME says so) and
 *      it contains **zero STOCKITEM, zero UNIT, zero GODOWN, zero STOCKGROUP** —
 *      measured by counting the TALLYMESSAGE children: LEDGER 3706, GROUP 37,
 *      CURRENCY 2, COMPANY 1, and nothing else. So the only door actually open
 *      is remedy (2), which the message itself says understates the cost of
 *      goods sold. That is registered as **D100**.
 *
 *   4. With `allow_negative_stock` ON: 121 vouchers posted, 226 stock lines,
 *      **0 warnings**, 72 ledgers the masters export did not contain, 4 stock
 *      items, 71 batches — and a trial balance that BALANCES at
 *      25,986,501.8800 on both sides across 3,712 rows.
 *
 * ⛔ THE BROWSER IS WHAT CLOSES THE ROW, and what it adds over the wire is the
 * only thing that matters to an operator: WHAT THEY SEE. The abort in step 2 is
 * a wall of text on a screen; the 19 warnings in step 1 are either rendered in
 * full or summarised into a number; and the report's own closing sentence tells
 * them to check the Stock Summary, so this drive checks it too.
 *
 * ⛔ THROWAWAY TENANT, REAPED. Someone's real books do not stay on this machine
 * a minute longer than the drive.
 *
 * RUN:  node frontend/scripts/drive-u1-real-export.mjs
 *       TALLY_MASTERS=/path/Master.xml TALLY_TXNS=/path/Transactions.xml node …
 */
import { openApp, rec, summary } from "./audit-harness.mjs";
import { existsSync, readFileSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `audit-u1 REAL ${stamp}`;

const MASTERS = process.env.TALLY_MASTERS ?? join(homedir(), "Downloads", "Master.xml");
const TXNS = process.env.TALLY_TXNS ?? join(homedir(), "Downloads", "Transactions.xml");

// ---------------------------------------------------------------------------
// ⛔ THE SKIP IS A RECORDED NON-RUN, NOT A SILENT ONE. Two missing files would
// make every assertion below vacuous, and this repository's own rule is that a
// check that did not run must never look like a check that passed.
// ---------------------------------------------------------------------------
if (!existsSync(MASTERS) || !existsSync(TXNS)) {
  rec(
    "the real TallyPrime export pair is present — WITHOUT IT THIS DRIVE PROVES NOTHING",
    false,
    `missing: ${[MASTERS, TXNS].filter((p) => !existsSync(p)).join(", ")}. ` +
      `Set TALLY_MASTERS / TALLY_TXNS. This is a SKIP, deliberately recorded as a failure.`,
  );
  summary();
  process.exit(1);
}

// ⛔ THE CONTROL ON THE ORACLE: a real TallyPrime export is UTF-16LE with a BOM.
// If these were ever replaced with ASCII stand-ins, the encoding half of this
// row would pass against files that cannot exercise it.
const bom = (p) => {
  const b = readFileSync(p, { length: 2 });
  return b[0] === 0xff && b[1] === 0xfe;
};
rec(
  "control: both real exports are UTF-16LE with a BOM, the only shape a customer sends",
  bom(MASTERS) && bom(TXNS),
  `${MASTERS} ${statSync(MASTERS).size} bytes · ${TXNS} ${statSync(TXNS).size} bytes`,
);

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
      { env: { ...process.env, PGPASSWORD: "postgres" }, maxBuffer: 32 * 1024 * 1024 },
      (err, stdout) => (err ? rej(err) : res(stdout.trim())),
    );
  });
};
const q = (s) => `'${String(s).replace(/'/g, "''")}'`;

const TOK = (
  await j("POST", "/auth/token", {
    body: { subject: "browser-dev-owner", amr: ["webauthn"] },
  })
).body.token;

// ⚠️ THE FINANCIAL YEAR MATCHES THE EXPORT'S OWN. The file's SVCURRENTCOMPANY
// says "from 1-Apr-2020"; a tenant opened on a different FY would refuse or
// mis-file vouchers for a reason that has nothing to do with the importer.
const created = await j("POST", "/v1/companies", {
  token: TOK,
  body: { name: COMPANY_NAME, fy_start_date: "2020-04-01", state_code: "27" },
});
if (created.status !== 201 && created.status !== 200) {
  throw new Error(
    `drive-u1-real: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
console.log(`company ${COMPANY_NAME} = ${CO}`);

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, toGateway, altMac } = app;

const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const text = async (t) =>
  (await seen(t)) ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim() : "";

/**
 * Put a real export through the SCREEN and poll for a verdict.
 *
 * ⛔ IT POLLS AND NEVER SLEEPS. An 84 MB UTF-16 upload plus 3,706 ledger
 * upserts measured 43 s on this machine; a fixed wait tuned to that is a flake
 * that would report DATA LOSS the first time the machine was busy.
 */
const importThroughScreen = async (path, budgetMs = 600_000) => {
  await testid("tally-import-file").setInputFiles(path);
  await page.waitForTimeout(300);
  await testid("tally-import-button").click();
  const deadline = Date.now() + budgetMs;
  while (Date.now() < deadline) {
    if ((await seen("tally-import-error")) || (await seen("tally-import-report"))) {
      return {
        error: await text("tally-import-error"),
        report: await text("tally-import-report"),
      };
    }
    await page.waitForTimeout(500);
  }
  return { error: "", report: "", timedOut: true };
};

let driveError = null;
try {
  await altMac("o", 1200);
  rec("Alt+O opens the importer on a tenant with a real financial year", await seen("tally-import-screen"));

  // =========================================================================
  // 1. THE MASTERS. 84 MB of UTF-16, 3,706 ledgers, through a real file input.
  // =========================================================================
  const m = await importThroughScreen(MASTERS);
  rec(
    "⛔ a REAL 84 MB TallyPrime masters export imports through the screen",
    !m.timedOut && m.report !== "" && m.error === "",
    m.timedOut ? "TIMED OUT" : m.error.slice(0, 300) || m.report.slice(0, 160),
  );
  rec(
    "the report names the SOURCE company off the file, not the tenant it landed in",
    /RAJKUMAR/i.test(m.report),
    m.report.slice(0, 160),
  );
  const ledgersShown = await text("tally-import-ledgers");
  rec(
    "⛔ the ledgers it created are NAMED, not counted — a migration is reconciled by name",
    /Ledgers touched \(3706\)/.test(ledgersShown) || /\(37\d\d\)/.test(ledgersShown),
    ledgersShown.slice(0, 140),
  );

  // ⛔⛔ THE WARNINGS. Nineteen of them, every one a real accounting fact.
  const warnCount = await page
    .getByTestId("tally-import-warnings")
    .locator("li")
    .count()
    .catch(() => 0);
  rec(
    "⛔⛔ all 19 warnings are on the page IN FULL, never summarised to a count",
    warnCount === 19,
    `${warnCount} <li> under the warnings heading`,
  );
  const warnText = await text("tally-import-warnings");
  rec(
    "⛔ and a warning says WHICH ledger and BY HOW MUCH, so it can be acted on",
    /opening bill-wise breakup sums to [\d.]+ but its opening balance is [\d.]+/.test(warnText),
    warnText.slice(0, 220),
  );

  // =========================================================================
  // 2. ⛔⛔ THE TRANSACTIONS ARE REFUSED — the whole-import abort, on screen.
  //    This is the FIRST thing a real migrating customer sees.
  // =========================================================================
  const t1 = await importThroughScreen(TXNS);
  rec(
    "⛔⛔ the real transactions export is REFUSED ENTIRELY on a company with no opening stock",
    !t1.timedOut && t1.error !== "" && t1.report === "",
    t1.timedOut ? "TIMED OUT" : t1.error.slice(0, 200),
  );
  rec(
    "the refusal names the ITEM, the godown and the batch that would go negative",
    /RICE/.test(t1.error) && /godown/i.test(t1.error) && /batch/i.test(t1.error),
    t1.error.slice(0, 220),
  );
  rec(
    "⛔ and it names BOTH ways forward rather than choosing for the operator",
    /Two ways forward/i.test(t1.error) && /negative stock/i.test(t1.error),
    t1.error.slice(0, 300),
  );
  rec(
    "⛔ NOTHING was written by the refused transactions import — it aborted on voucher 1",
    (await sql(`SELECT count(*) FROM voucher_head WHERE company_id = ${q(CO)}`)) === "0",
    "an abort on the FIRST voucher is the one case where D98's partial commit cannot bite",
  );

  // =========================================================================
  // 3. REMEDY (2), FROM THE KEYBOARD. F11 Features > Allow Negative Stock.
  //    Remedy (1) is unavailable to this operator — see D100 and the header.
  // =========================================================================
  await toGateway();
  await press("F11", 1500);
  rec("F11 opens Company Features, where the negative-stock control lives", await seen("negative-stock-panel"));
  rec(
    "control: the setting starts REFUSED, which is why the import above failed",
    /REFUSED/.test(await text("negative-stock-state")),
    await text("negative-stock-state"),
  );
  await testid("negative-stock-allow").click();
  await page.waitForTimeout(1500);
  rec(
    "⛔ the operator can switch it ON from the keyboard, and the screen says so",
    /ALLOWED/.test(await text("negative-stock-state")),
    await text("negative-stock-state"),
  );
  rec(
    "⚠️ and the panel warns that allowing it does NOT make the position harmless",
    /no COST to consume|carries zero value/i.test((await text("negative-stock-panel")) || ""),
    "an issue with no stock to consume has no cost to consume either",
  );

  // =========================================================================
  // 4. THE REAL BOOKS ARRIVE.
  // =========================================================================
  await toGateway();
  await altMac("o", 1200);
  const t2 = await importThroughScreen(TXNS);
  rec(
    "⛔ with negative stock allowed, the real 121-voucher export imports",
    !t2.timedOut && t2.report !== "" && t2.error === "",
    t2.timedOut ? "TIMED OUT" : t2.error.slice(0, 200),
  );
  rec(
    "the report shows 121 vouchers posted",
    /Vouchers posted: 121/.test(t2.report),
    (t2.report.match(/Vouchers posted: \d+/) ?? [""])[0],
  );
  rec(
    "⛔⛔ AND THE GOODS ARRIVED — 226 stock movements, the figure a trial balance cannot see",
    /Stock movements written: 226/.test(t2.report),
    (t2.report.match(/Stock movements written: \d+/) ?? [""])[0],
  );
  rec(
    "⛔ opening positions are ZERO and that is rendered as a WARNING, not a clean result",
    await seen("tally-import-opening-gap"),
    await text("tally-import-opening-gap"),
  );
  rec(
    "⚠️ 72 ledgers the MASTERS export did not contain were created by the transactions",
    /Ledgers touched \(72\)/.test(await text("tally-import-ledgers")),
    `${(await text("tally-import-ledgers")).slice(0, 100)} — an "All Masters" export that does ` +
      `not name every ledger its own vouchers post to`,
  );

  // =========================================================================
  // 5. THE BOOKS THEMSELVES. The report's own closing sentence says never to
  //    trust a balanced trial balance — so check BOTH, and check the one it
  //    names.
  // =========================================================================
  const tb = await j("GET", `/v1/companies/${CO}/trial-balance?as_on=2027-03-31`, { token: TOK });
  rec(
    "⛔ the migrated books BALANCE — debits equal credits to the paisa",
    tb.body.balanced === true && tb.body.total_debit === tb.body.total_credit,
    `Dr ${tb.body.total_debit} · Cr ${tb.body.total_credit} · ${(tb.body.rows ?? []).length} rows`,
  );

  await toGateway();
  await altMac("g", 700);
  await page.keyboard.type("Stock Summary");
  await page.waitForTimeout(400);
  await page.keyboard.press("Enter");
  await page.waitForTimeout(3000);
  const stock = (await body()).replace(/\s+/g, " ");
  rec(
    "⛔⛔ THE STOCK SUMMARY THE REPORT TOLD THEM TO CHECK SHOWS THE REAL ITEMS",
    /RICE/i.test(stock) && /WHEAT|JOWAR|BAJARI|POHA/i.test(stock),
    stock.slice(0, 300),
  );
} catch (e) {
  driveError = e;
  rec("the drive ran to the end without throwing", false, String(e).slice(0, 400));
} finally {
  // ⛔⛔ SOMEONE'S REAL BOOKS DO NOT STAY ON THIS MACHINE. Catalog-derived, and
  // never a bare DELETE FROM company (that strands ACL rows and /v1/me then
  // answers 500 for every session).
  try {
    await sql(`
DO $$
DECLARE t text;
BEGIN
  SET LOCAL session_replication_role = replica;
  EXECUTE format('DELETE FROM statutory_step WHERE workflow_id LIKE %L', 'irn:' || ${q(CO)} || ':%');
  FOR t IN
    SELECT c.table_name FROM information_schema.columns c
      JOIN information_schema.tables tb USING (table_schema, table_name)
     WHERE c.table_schema='public' AND c.column_name='company_id'
       AND tb.table_type='BASE TABLE' AND c.table_name <> 'company'
  LOOP
    EXECUTE format('DELETE FROM %I WHERE company_id::text = %L', t, ${q(CO)});
  END LOOP;
  DELETE FROM company WHERE id = ${q(CO)};
END $$;`);
    const left = await sql(`SELECT count(*) FROM company WHERE id = ${q(CO)}`);
    const led = await sql(`SELECT count(*) FROM ledger WHERE company_id = ${q(CO)}`);
    console.log(
      `\ntorn down ${COMPANY_NAME}: ${left === "0" && led === "0" ? "clean" : `LEAKED (company=${left} ledger=${led})`}`,
    );
  } catch (e) {
    console.log(`\n⚠️ teardown FAILED for ${CO}: ${e}`);
  }
  if (driveError) console.log("\n--- the throw ---\n" + String(driveError).slice(0, 1200));
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);
