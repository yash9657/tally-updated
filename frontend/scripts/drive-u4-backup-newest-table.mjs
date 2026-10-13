/**
 * U4 — BACKUP / RESTORE (Alt+Y): IS THE ARCHIVE STILL TENANT-EXACT *TODAY*?
 *
 * ⛔ WHAT `drive-t14-data.mjs` ALREADY PROVES, SO THIS DOES NOT REPEAT IT
 * ===========================================================================
 * 25/25, re-run 2026-08-27: Alt+Y through `altMac`, the screen's three
 * disclosures, a real gzip download named by the server, the header, the
 * trailer, a truncated archive refused FOR BEING TRUNCATED and leaving nothing
 * behind, a restore beside its own company refused 409, the restore recreating
 * the company through the screen, `company_membership` holding only the
 * restoring user, and the restored voucher's HASH CHAIN verifying under its
 * original id.
 *
 * ⛔⛔ AND ONE OF ITS PROBES IS STRUCTURALLY BLIND TO THIS ROW'S WORST FAILURE.
 * "the restored company matches the archived one on every table" compares the
 * RESTORE against the ARCHIVE — so a table missing from the ENUMERATION is
 * missing from BOTH, and the comparison is green while the data is gone. The
 * row's claim is not "restore matches archive"; it is:
 *
 *   "covering every table the schema itself says holds this company's data —
 *    the enumeration is DERIVED from the live catalog and shared with the
 *    test-data teardown, so A TABLE ADDED BY TOMORROW'S MIGRATION IS ARCHIVED
 *    WITHOUT ANYONE REMEMBERING TO ADD IT."
 *
 * ⭐ THAT CLAIM IS TESTABLE TODAY AND IT WAS NOT TESTABLE LAST WEEK, because
 * the T8 lane landed **migration 0080** yesterday — `currency` and
 * `currency_rate`, two company-scoped tables that did not exist when the backup
 * service was written and that no line of backup code mentions. If the
 * enumeration is genuinely catalog-derived, a currency and a rate written
 * through T8's own routes survive a backup and a restore with nobody having
 * touched `internal/backup`. If it is a list anyone maintains by hand, they do
 * not — and a company would restore with its exchange rates silently gone,
 * which is precisely the failure the row calls the worst defect class in this
 * product: *discovered only when a restore is needed*.
 *
 * ⛔ THE ORACLE IS POSTGRES ON BOTH SIDES, ROW BY ROW. Not the archive, not the
 * screen. The archive is the thing under test, so it cannot also be the oracle.
 *
 * ⚠️ AND THE T8 WRITE IS A CONTROL, NOT AN ASSUMPTION. If the currency routes
 * refuse, every assertion below would compare zero rows against zero rows and
 * pass. The drive asserts the rows exist BEFORE the backup and records a
 * non-run rather than a pass if they do not.
 *
 * ⚠️ THIS LANE DOES NOT OWN THE T8 PATHS AND EDITS NONE OF THEM. It calls their
 * HTTP routes exactly as an operator's browser would.
 *
 * RUN:  node frontend/scripts/drive-u4-backup-newest-table.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `audit-u4 Backup ${stamp}`;

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

/**
 * Row counts for one company across EVERY company-scoped table, derived from
 * the live catalog rather than listed.
 *
 * ⛔ IT IS CATALOG-DERIVED ON THIS SIDE TOO, ON PURPOSE. A probe with its own
 * hand-written table list would go stale exactly the way the thing it is
 * testing might, and would then agree with a broken archive for the same
 * reason. `information_schema` is the only list either side is allowed.
 */
const census = async (co) => {
  const out = await sql(`
SELECT string_agg(format('%s=%s', t, n), E'\\n' ORDER BY t) FROM (
  SELECT c.table_name AS t,
         (xpath('/row/c/text()',
                query_to_xml(format('SELECT count(*) AS c FROM %I WHERE company_id::text = %L',
                                    c.table_name, ${q(co)}), false, true, '')))[1]::text::int AS n
    FROM information_schema.columns c
    JOIN information_schema.tables tb USING (table_schema, table_name)
   WHERE c.table_schema = 'public' AND c.column_name = 'company_id'
     AND tb.table_type = 'BASE TABLE' AND c.table_name <> 'company'
) s WHERE n > 0`);
  const m = new Map();
  for (const line of (out || "").split("\n").filter(Boolean)) {
    const [t, n] = line.split("=");
    m.set(t, Number(n));
  }
  return m;
};

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
    `drive-u4: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
const base = `/v1/companies/${CO}`;
console.log(`company ${COMPANY_NAME} = ${CO}`);

/** Every tenant this drive mints — the original and whatever a restore creates. */
const MINTED = [CO];

// ---------------------------------------------------------------------------
// ⛔ WRITE INTO THE NEWEST TABLES IN THE SCHEMA, THROUGH THE ROUTES THAT OWN
// THEM. Nothing here reaches into Postgres to plant a row: a hand-planted row
// would prove the archive can copy a row, which was never in doubt. What is in
// doubt is whether the ENUMERATION knows the table exists.
// ---------------------------------------------------------------------------
const cur = await j("POST", `${base}/currencies`, {
  token: TOK,
  body: {
    code: "USD",
    symbol: "$",
    formal_name: "US Dollar",
    decimal_places: 2,
    created_by: "drive-u4",
  },
});
const rate = await j("POST", `${base}/currencies/rates`, {
  token: TOK,
  body: { code: "USD", effective_date: "2026-08-01", rate: "87.1234567890", created_by: "drive-u4" },
});

let driveError = null;
let app = null;
try {
  // ⚠️ THE CONTROL. Without these rows every count below is 0 == 0.
  const curBefore = Number(await sql(`SELECT count(*) FROM currency WHERE company_id = ${q(CO)}`));
  const rateBefore = Number(
    await sql(`SELECT count(*) FROM currency_rate WHERE company_id = ${q(CO)}`),
  );
  rec(
    "control: a currency and a rate really were written into migration 0080's tables",
    curBefore === 1 && rateBefore === 1,
    `currency=${curBefore} currency_rate=${rateBefore} · POST currencies -> ${cur.status} ` +
      `${JSON.stringify(cur.body).slice(0, 120)} · POST rates -> ${rate.status} ` +
      `${JSON.stringify(rate.body).slice(0, 120)}`,
  );

  // ⭐ THE RATE'S EXACT VALUE IS CARRIED THROUGH, not just its row count. A
  // restore that rounded 87.1234567890 to 87.12 would keep every count green
  // and lose the thing the T8 lane's commit message says the column exists for.
  const rateValueBefore = await sql(
    `SELECT rate::text FROM currency_rate WHERE company_id = ${q(CO)}`,
  );

  app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
  const { page, body, altMac } = app;
  const testid = (t) => page.getByTestId(t);
  const seen = async (t) => (await testid(t).count()) > 0;

  await altMac("y", 1500);
  rec("Alt+Y opens the Data screen", await seen("data-screen") || /Data/.test(await body()));

  // -------------------------------------------------------------------------
  // BACK UP THROUGH THE SCREEN. The download is intercepted so the bytes this
  // drive restores are the bytes the operator's browser really received.
  // -------------------------------------------------------------------------
  const dl = page.waitForEvent("download", { timeout: 120_000 });
  await page.getByRole("button", { name: /back ?up/i }).first().click();
  const download = await dl;
  // ⛔ SAVE THE BYTES BEFORE THE BROWSER CLOSES. Playwright deletes its
  // artifacts directory on `browser.close()`, so `download.path()` read after
  // the close is an ENOENT — which cost this file one run and looked like a
  // backup that produced no file.
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");
  const archivePath = join(tmpdir(), `audit-u4-${stamp}.tallybak.jsonl.gz`);
  await download.saveAs(archivePath);
  rec(
    "the backup downloads through the screen",
    !!archivePath,
    `${download.suggestedFilename()} -> ${archivePath}`,
  );

  // ⛔ THE BEFORE PICTURE, TAKEN WHILE THE ORIGINAL IS STILL HERE.
  const before = await census(CO);

  // -------------------------------------------------------------------------
  // ⛔ DROP THE ORIGINAL. A restore REFUSES an archive whose company is still
  // here (409, proved by drive-t14), so the tenant has to go before the archive
  // can come back — which is also the only way the comparison below is honest:
  // the restored rows cannot be the originals.
  // -------------------------------------------------------------------------
  await app.browser.close();
  app = null;
  await sql(`
DO $$
DECLARE t text;
BEGIN
  SET LOCAL session_replication_role = replica;
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
  rec(
    "control: the original company is GONE, so nothing below can be reading it",
    (await sql(`SELECT count(*) FROM currency WHERE company_id = ${q(CO)}`)) === "0",
  );

  // -------------------------------------------------------------------------
  // RESTORE, and read the newest tables back.
  // -------------------------------------------------------------------------
  const { readFileSync } = await import("node:fs");
  const bytes = readFileSync(archivePath);
  const restore = await fetch(`${CORED}/v1/companies/restore`, {  // nosemgrep: typescript.react.security.react-insecure-request.react-insecure-request -- drive script against the local dev stack (CORED is http://localhost)
    method: "POST",
    headers: {
      Authorization: `Bearer ${TOK}`,
      "Content-Type": "application/gzip",
    },
    body: bytes,
  });
  const restored = await restore.json().catch(() => ({}));
  rec(
    "the archive restores",
    restore.status === 201 || restore.status === 200,
    `${restore.status} ${JSON.stringify(restored).slice(0, 200)}`,
  );
  const NEW = restored.company_id ?? restored.id ?? CO;
  if (NEW && NEW !== CO) MINTED.push(NEW);

  const curAfter = Number(await sql(`SELECT count(*) FROM currency WHERE company_id = ${q(NEW)}`));
  const rateAfter = Number(
    await sql(`SELECT count(*) FROM currency_rate WHERE company_id = ${q(NEW)}`),
  );

  // =========================================================================
  // ⛔⛔ THE ASSERTION THIS FILE EXISTS FOR.
  //
  // `currency` and `currency_rate` were created by migration 0080 on
  // 2026-08-27. No line of the backup service names either of them. If they
  // come back, the enumeration really is derived from the live catalog and the
  // row's anti-rot claim is TRUE OF CODE THAT SHIPPED AFTER IT WAS WRITTEN.
  // =========================================================================
  rec(
    "⛔⛔ A TABLE MIGRATION 0080 ADDED YESTERDAY SURVIVES BACKUP AND RESTORE — the enumeration really is catalog-derived",
    curAfter === 1 && rateAfter === 1,
    `currency ${curBefore}->${curAfter} · currency_rate ${rateBefore}->${rateAfter}. ` +
      `A zero here is the row's own worst case: an archive that looks complete and is not, ` +
      `discovered only when a restore is needed`,
  );
  const restoredCensus = await census(NEW);
  const rateValueAfter = await sql(
    `SELECT rate::text FROM currency_rate WHERE company_id = ${q(NEW)}`,
  );
  rec(
    "⛔ and the rate came back to the LAST DECIMAL — an archive that rounds is an archive that lies",
    rateValueAfter === rateValueBefore && /\.\d{10}/.test(rateValueAfter),
    `before ${rateValueBefore} · after ${rateValueAfter}`,
  );

  // -------------------------------------------------------------------------
  // ⭐ AND THE GENERAL FORM OF THE SAME QUESTION, so this file keeps working
  // when 0081 lands: EVERY company-scoped table that held a row before the
  // backup must hold the same number after the restore.
  //
  // ⚠️ ONE TABLE IS EXPECTED TO DIFFER AND IT IS NAMED RATHER THAN IGNORED.
  // `company_membership` is the access the row says an archive deliberately
  // does NOT re-grant: the restoring user becomes Owner and everyone else is
  // listed by name to be re-granted. A sweep that quietly skipped it would also
  // quietly skip the next table somebody decided not to restore.
  // -------------------------------------------------------------------------
  const NOT_RESTORED = new Set(["company_membership"]);
  const missing = [];
  const shrunk = [];
  for (const [table, n] of before) {
    if (NOT_RESTORED.has(table)) continue;
    const after = restoredCensus.get(table) ?? 0;
    if (after === 0) missing.push(`${table} (${n} rows archived, NONE restored)`);
    else if (after !== n) shrunk.push(`${table} ${n}->${after}`);
  }
  rec(
    "⛔⛔ NO company-scoped table that held rows came back EMPTY — the whole-table failure",
    missing.length === 0,
    missing.length
      ? `LOST ENTIRELY: ${missing.join(" · ")}`
      : `${before.size} tables held rows; every one came back`,
  );
  rec(
    "⛔ and no table came back with FEWER rows than were archived",
    shrunk.length === 0,
    shrunk.length ? `SHORT: ${shrunk.join(" · ")}` : "row counts match table for table",
  );
  rec(
    "⚠️ company_membership is the ONE table that deliberately differs, and it holds only the restorer",
    (restoredCensus.get("company_membership") ?? 0) === 1,
    `company_membership = ${restoredCensus.get("company_membership") ?? 0} ` +
      `(the archive carried ${before.get("company_membership") ?? 0}; re-granting access from a ` +
      `file somebody handed you is a privilege-escalation vector)`,
  );
} catch (e) {
  driveError = e;
  rec("the drive ran to the end without throwing", false, String(e).slice(0, 400));
} finally {
  if (app) await app.browser.close().catch(() => {});
  for (const id of MINTED) {
    try {
      await sql(`
DO $$
DECLARE t text;
BEGIN
  SET LOCAL session_replication_role = replica;
  EXECUTE format('DELETE FROM statutory_step WHERE workflow_id LIKE %L', 'irn:' || ${q(id)} || ':%');
  FOR t IN
    SELECT c.table_name FROM information_schema.columns c
      JOIN information_schema.tables tb USING (table_schema, table_name)
     WHERE c.table_schema='public' AND c.column_name='company_id'
       AND tb.table_type='BASE TABLE' AND c.table_name <> 'company'
  LOOP
    EXECUTE format('DELETE FROM %I WHERE company_id::text = %L', t, ${q(id)});
  END LOOP;
  DELETE FROM company WHERE id = ${q(id)};
END $$;`);
      const left = await sql(`SELECT count(*) FROM company WHERE id = ${q(id)}`);
      console.log(`torn down ${id}: ${left === "0" ? "clean" : `LEAKED (${left})`}`);
    } catch (e) {
      console.log(`⚠️ teardown FAILED for ${id}: ${e}`);
    }
  }
  // ⛔ THE ARCHIVE IS A WHOLE COMPANY IN A FILE. It does not stay on disk.
  try {
    const { rmSync } = await import("node:fs");
    const { tmpdir } = await import("node:os");
    const { join } = await import("node:path");
    rmSync(join(tmpdir(), `audit-u4-${stamp}.tallybak.jsonl.gz`), { force: true });
  } catch {
    /* nothing to remove */
  }
  if (driveError) console.log("\n--- the throw ---\n" + String(driveError).slice(0, 1200));
}
process.exit(summary() > 0 ? 1 : 0);
