/**
 * T14 — BACKUP / RESTORE, driven from the keyboard in a real browser.
 *
 * ⛔ WHAT THIS PROVES, AND WHY IT NEEDED A BROWSER
 * ===========================================================================
 * internal/backup, the census extraction, the round-trip test that drops the
 * original and the three report oracles can ALL be green while no operator can
 * take a backup — which is exactly what happened to order processing (whole
 * backend, no screen) and to payroll (complete engine, two missing writers). So
 * this probe presses Alt+Y, clicks the button ON THE PAGE, and reads the archive
 * the BROWSER actually downloaded.
 *
 * ⛔ AND ALT+Y IS EXACTLY THE KEY THAT DIES ON macOS. Option composes Y into
 * "¥", so a probe that pressed "Alt+y" would resolve nothing and prove nothing
 * on the machines this is developed on. It is pressed with the physical `code`
 * through the harness's altMac("y"), which is what a Mac really sends.
 *
 * THE ORACLES, each chosen so the wrong answer fails rather than looks plausible:
 *
 *   1. THE FILE THE BROWSER SAVED IS A REAL ARCHIVE. It is gunzipped and parsed
 *      here — header first, trailer last — and the trailer's per-table counts are
 *      compared against the DATABASE, table by table, using the same census the
 *      product derives. A backup that downloaded a plausible-looking file with a
 *      table missing passes a smoke test and fails this.
 *
 *   2. A RESTORE OF IT IS REFUSED, 409, BECAUSE THE COMPANY IS STILL HERE. That
 *      is the D2 consequence — preserving primary keys is what keeps the audit
 *      chain verifiable, so an archive cannot be restored beside itself — and it
 *      is the single most surprising thing about this feature. The screen says so
 *      before the operator picks a file; this checks the server agrees.
 *
 *   3. A TRUNCATED ARCHIVE IS REFUSED. A cut-off download is still valid gzip and
 *      still parses as JSON Lines, so without the trailer check it would restore
 *      as most of a company.
 *
 *   4. THE SCREEN SAYS THE THREE THINGS. Restore creates rather than replaces;
 *      access is not re-granted; Split Company Data is absent ON PURPOSE with its
 *      reason. Read OFF THE PAGE, not from the source.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY OF ITS OWN, NOT ON Demo Traders. It posts
 * vouchers and it RESTORES a company; residue in the demo tenant drops
 * `verify:demo` below 34/34.
 *
 * ⛔ SYMBOL-CHECK THE RUNNING `cored` BEFORE BELIEVING A GREEN RUN. `make
 * run-cored` is `go run`, so a stale process is indistinguishable from a fix
 * that does not work:
 *
 *   strings "$(ps -o command= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)" \
 *     | awk '{print $1}')" | grep -c tally-company-archive
 *
 * RUN:  node frontend/scripts/drive-t14-data.mjs
 *       (needs cored :8087 and vite :5173 — the port matters, WebAuthn origin
 *        matching is exact. It creates its own company.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";
import { gunzipSync } from "node:zlib";
import { readFileSync } from "node:fs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Backup Drive ${stamp}`;

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

// ---- The tenant and some real books, over HTTP as the SAME subject the browser
// signs in as.
//
// ⚠️ THE VOUCHERS ARE POSTED OVER HTTP ON PURPOSE. They are not what this probe
// is about, and keying them through the grid would make a backup failure
// indistinguishable from a voucher-entry failure. THE BACKUP AND THE RESTORE ARE
// DRIVEN IN THE BROWSER, because they are the feature.
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
    `drive-t14: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
console.log(`company ${COMPANY_NAME} = ${CO}`);

const base = `/v1/companies/${CO}`;
const ledger = async (name, group) =>
  (
    await j("POST", `${base}/ledgers`, {
      token: TOK,
      body: { name, group_name: group, created_by: "drive" },
    })
  ).body.ledger_id;
const DEBTOR = await ledger("Drive Debtor", "Sundry Debtors");
const SALES = await ledger("Drive Sales", "Sales Accounts");
const posted = await j("POST", `${base}/vouchers/sales`, {
  token: TOK,
  body: {
    voucher_no: "BK-1",
    voucher_date: "2026-04-10",
    party: "Drive Debtor",
    created_by: "drive",
    postings: [
      { ledger_id: DEBTOR, dr_cr: "D", amount: "1500.0000" },
      { ledger_id: SALES, dr_cr: "C", amount: "1500.0000" },
    ],
  },
});
if (posted.status !== 201) {
  throw new Error(`drive-t14: seed voucher failed: ${posted.status} ${JSON.stringify(posted.body)}`);
}

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, altMac, net } = app;

const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const text = async (t) =>
  (await seen(t))
    ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim()
    : "";

let archivePath = null;

try {
  // =========================================================================
  // 1. ALT+Y OPENS THE DATA SCREEN — the macOS-composed key, not "Alt+y".
  // =========================================================================
  rec(
    "control: the Data screen is ABSENT before the key",
    !(await seen("data-screen")),
    "",
  );
  await altMac("y", 900);
  rec("Alt+Y (macOS Option+Y) opens the Data screen", await seen("data-screen"), await body().then((b) => b.slice(0, 120)));
  rec(
    "the frame is titled Data, not Backup",
    (await body()).includes("Data"),
    "",
  );

  // =========================================================================
  // 2. THE SCREEN SAYS THE THREE THINGS, read OFF THE PAGE.
  // =========================================================================
  const restoreWarning = await text("data-restore-warning");
  rec(
    "the screen says a restore CREATES a company and never replaces one",
    /creates a new company/i.test(restoreWarning) && /never replaces/i.test(restoreWarning),
    restoreWarning.slice(0, 140),
  );
  const memberWarning = await text("data-restore-memberships-warning");
  rec(
    "...and that access rights are NOT re-granted from the archive",
    /not restored/i.test(memberWarning) && /owner/i.test(memberWarning),
    memberWarning.slice(0, 140),
  );
  const split = await text("data-split-absent");
  rec(
    "...and that Split Company Data is absent ON PURPOSE, with the reason",
    /deliberately/i.test(split) && /(memory|RAM)/i.test(split) && !/coming soon/i.test(split),
    split.slice(0, 160),
  );

  // =========================================================================
  // 3. THE BUTTON DOWNLOADS A REAL ARCHIVE.
  // =========================================================================
  const download = page.waitForEvent("download", { timeout: 30_000 });
  await testid("data-backup-button").first().click();
  const dl = await download;
  archivePath = await dl.path();
  const suggested = dl.suggestedFilename();
  rec(
    "clicking Back up downloads a file named by the SERVER",
    /\.tallybak\.jsonl\.gz$/.test(suggested) && suggested.includes("backup-drive"),
    suggested,
  );
  rec(
    "the screen confirms what it saved",
    (await text("data-backup-saved")).includes(suggested),
    await text("data-backup-saved"),
  );

  const raw = readFileSync(archivePath);
  let lines = [];
  let gunzipOK = true;
  try {
    lines = gunzipSync(raw).toString("utf8").split("\n").filter((l) => l.trim() !== "");
  } catch (e) {
    gunzipOK = false;
    lines = [];
  }
  rec("the downloaded file is real gzip", gunzipOK, `${raw.length} bytes`);

  const records = lines.map((l) => JSON.parse(l));
  const header = records[0];
  const trailer = records[records.length - 1];
  rec(
    "the archive begins with a header naming this company",
    header?.kind === "header" &&
      header.format === "tally-company-archive" &&
      header.source_company_id === CO,
    JSON.stringify({ kind: header?.kind, id: header?.source_company_id }),
  );
  rec(
    "the header states what it carries and what a restore will not recreate",
    header?.contents?.insightd_drafts === true &&
      header?.contents?.statutory_steps === true &&
      (header?.contents?.not_restored ?? []).includes("company_membership") &&
      (header?.contents?.membership_names ?? []).length > 0,
    JSON.stringify(header?.contents ?? {}),
  );
  rec(
    "the archive ends with a trailer — the ONLY thing that can tell a whole file from most of one",
    trailer?.kind === "trailer" && typeof trailer.row_count === "number",
    JSON.stringify({ kind: trailer?.kind, rows: trailer?.row_count }),
  );

  // ---- ORACLE 1: the trailer against the DATABASE, table by table, using the
  // same census the product derives. Nothing here is a list of table names.
  const censusSQL = `
SELECT string_agg(format('SELECT %L t, count(*) n FROM %I WHERE company_id::text = %L',
         table_name, table_name, ${q(CO)}), ' UNION ALL ')
  FROM information_schema.columns c
  JOIN information_schema.tables t USING (table_schema, table_name)
 WHERE c.table_schema='public' AND c.column_name='company_id' AND t.table_type='BASE TABLE'`;
  const unionSQL = await sql(censusSQL);
  const dbCounts = Object.fromEntries(
    (await sql(`SELECT t || '=' || n FROM (${unionSQL}) x WHERE n > 0`))
      .split("\n")
      .filter(Boolean)
      .map((r) => {
        const [t, n] = r.split("=");
        return [t, Number(n)];
      }),
  );
  const mismatched = Object.entries(dbCounts).filter(
    ([t, n]) => (trailer?.counts?.[t] ?? 0) !== n,
  );
  rec(
    `the archive's trailer matches the database on every non-empty table (${Object.keys(dbCounts).length} of them)`,
    mismatched.length === 0 && Object.keys(dbCounts).length >= 8,
    mismatched.length
      ? mismatched.map(([t, n]) => `${t}: db ${n}, archive ${trailer?.counts?.[t] ?? 0}`).join("; ")
      : `${Object.keys(dbCounts).length} tables agree`,
  );
  const rowRecords = records.filter((r) => r.kind === "row");
  rec(
    "the archive actually carries the rows the trailer claims",
    rowRecords.length === trailer?.row_count && rowRecords.length > 0,
    `${rowRecords.length} row records, trailer says ${trailer?.row_count}`,
  );

  // =========================================================================
  // 4. RESTORING IT IS REFUSED, 409 — the company is still here.
  // =========================================================================
  const conflict = await fetch(`${CORED}/v1/companies/restore`, {  // nosemgrep: typescript.react.security.react-insecure-request.react-insecure-request -- drive script against the local dev stack (CORED is http://localhost)
    method: "POST",
    headers: { Authorization: `Bearer ${TOK}`, "Content-Type": "application/gzip" },
    body: raw,
  });
  const conflictBody = await conflict.text();
  rec(
    "restoring an archive beside its own company is REFUSED with 409",
    conflict.status === 409,
    `${conflict.status} ${conflictBody.slice(0, 120)}`,
  );
  rec(
    "...and the refusal explains itself (primary keys, the audit chain)",
    /already/i.test(conflictBody) && /primary key/i.test(conflictBody),
    conflictBody.slice(0, 200),
  );

  // =========================================================================
  // 5. DROP THE COMPANY. Everything after this is a restore of a company that
  //    IS NOT THERE — which is the only way any of it proves anything.
  //
  // ⛔ AN EARLIER VERSION OF THIS PROBE TESTED THE TRUNCATED ARCHIVE HERE AND
  // GOT A GREEN 409 FOR THE WRONG REASON: the company still existed, so the
  // existence check answered before the trailer check ever ran, and the
  // assertion `status !== 201` passed while proving nothing at all about
  // truncation. An oracle that cannot see the defect is not an oracle.
  //
  // The delete is DERIVED from the live catalog exactly as the product's census
  // is — every base table with a company_id, plus statutory_step, whose tenancy
  // lives inside its TEXT primary key. session_replication_role = replica is
  // what makes it possible at all: eight BEFORE UPDATE OR DELETE triggers refuse
  // a delete for every role, superuser included, which is the same immutability
  // that makes "restore over a company" impossible in the product.
  // =========================================================================
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
  const gone = await sql(`SELECT count(*) FROM company WHERE id = ${q(CO)}`);
  rec(
    "the company is DROPPED — every restore below is of a company that is not there",
    gone === "0",
    `company rows: ${gone}`,
  );

  // =========================================================================
  // 6. NOW the truncated archive, with nothing else able to refuse it first.
  // =========================================================================
  const short = raw.subarray(0, Math.floor(raw.length * 0.7));
  const truncated = await fetch(`${CORED}/v1/companies/restore`, {  // nosemgrep: typescript.react.security.react-insecure-request.react-insecure-request -- drive script against the local dev stack (CORED is http://localhost)
    method: "POST",
    headers: { Authorization: `Bearer ${TOK}`, "Content-Type": "application/gzip" },
    body: short,
  });
  const truncatedBody = await truncated.text();
  rec(
    "a TRUNCATED archive is refused — and refused FOR BEING TRUNCATED",
    truncated.status !== 201 && /(incomplete|cut short|trailer)/i.test(truncatedBody),
    `${truncated.status} ${truncatedBody.slice(0, 160)}`,
  );
  const afterShort = await sql(`SELECT count(*) FROM company WHERE id = ${q(CO)}`);
  rec(
    "...and it left NOTHING behind — a restore is one transaction",
    afterShort === "0",
    `company rows: ${afterShort}`,
  );

  // =========================================================================
  // 7. THE WHOLE ARCHIVE, THROUGH THE SCREEN. This is the round trip an
  //    operator actually performs, on the file the BROWSER downloaded.
  // =========================================================================
  await testid("data-restore-file").first().setInputFiles(archivePath);
  await page.waitForTimeout(4000);
  const result = await text("data-restore-result");
  rec(
    "restoring the downloaded archive through the SCREEN recreates the company",
    /Restored/i.test(result) && result.includes(COMPANY_NAME),
    result.slice(0, 200),
  );
  rec(
    "the screen names the people it did NOT re-grant access to",
    (await text("data-restore-memberships")).includes("browser-dev-owner"),
    await text("data-restore-memberships"),
  );
  rec(
    "no error was shown alongside the success",
    (await text("data-error")) === "",
    await text("data-error"),
  );

  // ---- THE ROUND-TRIP ORACLE: the restored company matches the archive,
  // table by table, against the same derived census as before.
  const restoredCounts = Object.fromEntries(
    (await sql(`SELECT t || '=' || n FROM (${unionSQL}) x WHERE n > 0`))
      .split("\n")
      .filter(Boolean)
      .map((r) => {
        const [t, n] = r.split("=");
        return [t, Number(n)];
      }),
  );
  // company_membership is the ONE deliberate difference: the archive carries it
  // and the restore refuses to act on it, seeding the restoring user instead.
  const bad = Object.entries(dbCounts)
    .filter(([t]) => t !== "company_membership")
    .filter(([t, n]) => restoredCounts[t] !== n);
  rec(
    "the restored company matches the archived one on every table but the one it declines to recreate",
    bad.length === 0,
    bad.length
      ? bad.map(([t, n]) => `${t}: was ${n}, now ${restoredCounts[t] ?? 0}`).join("; ")
      : `${Object.keys(dbCounts).length - 1} tables agree`,
  );
  rec(
    "...and company_membership holds exactly the restoring user, not the archive's rows",
    restoredCounts["company_membership"] === 1,
    `company_membership rows: ${restoredCounts["company_membership"] ?? 0}`,
  );

  // ⛔ THE CHAIN. Preserving primary keys is the whole reason this restore looks
  // the way it does; the edit log is what says it worked.
  const voucherID = posted.body.voucher_id;
  const history = await j("GET", `${base}/vouchers/${voucherID}/history`, { token: TOK });
  rec(
    "the restored voucher's HASH CHAIN still verifies, under its original id",
    history.status === 200 && history.body?.verification?.ok === true,
    `${history.status} ${JSON.stringify(history.body?.verification ?? {})}`,
  );

} finally {
  // ⛔ THE THROWAWAY COMPANY IS DROPPED, INCLUDING THE ONE THE RESTORE CREATED.
  // This probe is the only one in the repo that CREATES a second company as part
  // of its assertion, so it is the only one that can leak one — and a restored
  // company looks exactly like a real one to every other lane. The delete is the
  // same catalog-derived sweep as step 5.
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
    console.log(`\ntorn down ${COMPANY_NAME}: ${left === "0" ? "clean" : `LEAKED (${left} rows)`}`);
  } catch (e) {
    console.log(`\n⚠️ teardown FAILED for ${CO}: ${e}`);
  }

  // ⛔ EVERY non-GET THE BROWSER SENT IS PRINTED. A silent 403 on a screen that
  // rendered fine is how a refusal gets mistaken for a feature.
  console.log("\n--- non-GET browser requests (status last) ---");
  for (const r of net) console.log("  " + r);
  console.log("\n--- console errors ---");
  for (const e of app.consoleErrors.slice(0, 8)) console.log("  " + e);
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);
