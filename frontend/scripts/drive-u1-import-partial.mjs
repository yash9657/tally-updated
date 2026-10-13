/**
 * U1 — MIGRATE FROM TALLY: WHAT AN OPERATOR SEES WHEN ONE AMOUNT IS DECORATED.
 *
 * ⛔ WHAT THIS DRIVES THAT `drive-t32-import-screen.mjs` DOES NOT
 * ===========================================================================
 * That probe drives the HAPPY PATH end to end — Alt+O, a UTF-16 fixture, the
 * report, the database cross-check, both doors. Every fixture in
 * `corpora/migration/` parses, so nothing in this repository has ever driven
 * the REFUSAL path through the screen. This one does, and it exists because
 * `docs/design/t8-multi-currency-model.md` §6 measured (against the unchanged
 * build) that `TallyLedgerEntry.Magnitude()` hard-ERRORS on `$ 1000.00`,
 * `1000.00 USD`, `1000.00/USD` and on `83,650.00`.
 *
 * ⛔⛔ THE QUESTION IS NOT "DOES IT REFUSE". It is WHAT THE BOOKS LOOK LIKE
 * AFTERWARDS, and that cannot be answered anywhere but here:
 *
 *   - `migration.ImportEnvelope` posts each voucher through
 *     `im.ledger.PostVoucher` in document order and returns `res, err` on the
 *     first failure. There is NO enclosing transaction (grep the package: no
 *     BeginTx, no Rollback), so every voucher BEFORE the bad one is already
 *     committed through the immutable ledger path.
 *   - `server.go:3783` then throws `res` away and writes
 *     `422 {error: <sentence>}`. The count of vouchers that DID land is on the
 *     floor of that handler.
 *   - `TallyImportScreen` renders the server's sentence and nothing else, and
 *     its own copy says "There is no way to undo an import".
 *
 * So the hypothesis under test is: an operator is shown a refusal, and the
 * books are NOT as they were before it. If that holds, the row is LYING by
 * omission and it is a §7.1 registration.
 *
 * ⚠️ AND THE RECOVERY IS DRIVEN TOO, because the refusal only matters through
 * what the operator does next: fix the file, import it again. Whether that
 * doubles the vouchers that already landed turns on ONE line of
 * `migration.go` — "A GUID-less voucher is never deduped — it always posts."
 * A real TallyPrime export carries `<GUID>` on every voucher; a hand-built or
 * partial one may not. BOTH ARE DRIVEN so the finding can state which is which
 * instead of guessing at severity.
 *
 * THE ORACLES
 *
 *   1. THE SCREEN — the file goes in through the real `input[type=file]` and
 *      the real button, and the refusal is read off `tally-import-error`.
 *      Not a fetch from this script.
 *   2. THE DATABASE — `SELECT count(*) FROM voucher WHERE company_id = …`,
 *      which is the fact the screen is silent about.
 *   3. THE OPERATOR'S OWN BOOKS — the Day Book, opened from the keyboard in
 *      the same session. A count in psql that no screen shows is a fact about
 *      the schema; a voucher visible in the Day Book after a refusal is the
 *      finding.
 *
 * ⛔ THROWAWAY COMPANIES, ONE PER SCENARIO, ALL TORN DOWN. Each scenario needs
 * virgin books — a partial import in company A would make company B's counts
 * unreadable. The teardown is catalog-derived (copied from drive-t32) and the
 * whole drive is in ONE try so a throw still reaches it.
 *
 * RUN:  node frontend/scripts/drive-u1-import-partial.mjs
 *       (needs cored :8087 and vite :5173. It creates its own companies.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const CORED = "http://127.0.0.1:8087";
const HERE = dirname(fileURLToPath(import.meta.url));
// ⛔ THE FIXTURES LIVE IN THE CORPUS, NOT IN A TEMP DIRECTORY. They are the
// only files in `corpora/migration/` that do NOT parse, deliberately, and
// `corpora/validate.py` never sees them: it discovers cases by globbing
// `*.json`, and these five carry none (06 and 07 are the same shape).
const FIXTURES = join(HERE, "..", "..", "corpora", "migration");
const fx = (n) => join(FIXTURES, n);

// ⛔ THE CONTROL ON THE ORACLE ITSELF. If a fixture were ever regenerated
// without its decoration, every assertion below would pass vacuously against a
// file that simply imports. Assert the bytes carry the thing under test.
const carries = (name, needle) => {
  const s = readFileSync(fx(name), "utf8");
  return s.includes(needle);
};
rec(
  "control: the decorated fixture really contains a $-prefixed AMOUNT",
  carries("08-refusal-decorated-amount.xml", "<AMOUNT>$ 1000.00</AMOUNT>"),
);
rec(
  "control: the comma fixture really contains a thousands-grouped AMOUNT",
  carries("09-refusal-grouped-amount.xml", "<AMOUNT>83,650.00</AMOUNT>"),
);
rec(
  "control: the clean fixture is the SAME file with only that amount changed",
  carries("08-refusal-corrected.xml", "<AMOUNT>8000.00</AMOUNT>") &&
    !carries("08-refusal-corrected.xml", "$"),
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
      { env: { ...process.env, PGPASSWORD: "postgres" } },
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

const stamp = Math.random().toString(36).slice(2, 7);
/** Every tenant this drive mints, so the finally block can sweep all of them. */
const MINTED = [];

const newCompany = async (label) => {
  const name = `audit-u1 ${label} ${stamp}`;
  const created = await j("POST", "/v1/companies", {
    token: TOK,
    body: { name, fy_start_date: "2026-04-01", state_code: "29" },
  });
  if (created.status !== 201 && created.status !== 200) {
    throw new Error(
      `drive-u1: could not create throwaway company ${name}: ${created.status} ${JSON.stringify(created.body)}`,
    );
  }
  const id = created.body.company_id ?? created.body.id;
  MINTED.push({ id, name });
  console.log(`company ${name} = ${id}`);
  return { id, name };
};

/** Vouchers actually committed into a company. The fact the screen never shows. */
const voucherCount = async (co) =>
  Number(
    await sql(
      // ⛔ voucher_head, not "voucher" — and NOT is_deleted, because a
      // tombstoned voucher is still a row this import wrote. The count is of
      // what the import PUT THERE, which is the fact under test.
      `SELECT count(*) FROM voucher_head WHERE company_id = ${q(co)}`,
    ),
  );

let app = null;
try {
  // =========================================================================
  // SCENARIO A — A DECORATED AMOUNT ON THE THIRD OF THREE VOUCHERS.
  //
  // Vouchers 1 and 2 are ordinary and parse; voucher 3 carries "$ 1000.00".
  // If the import were atomic the company would hold ZERO vouchers afterwards.
  // =========================================================================
  const A = await newCompany("A");
  rec(
    "control: company A is virgin — no vouchers before the import",
    (await voucherCount(A.id)) === 0,
  );

  app = await openApp({ company: A.id, expectCompany: A.name });
  const { page, body, altMac } = app;
  const testid = (t) => page.getByTestId(t);
  const seen = async (t) => (await testid(t).count()) > 0;
  const text = async (t) =>
    (await seen(t))
      ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim()
      : "";

  /**
   * Put a file through the SCREEN and wait for an answer.
   *
   * ⛔ IT POLLS FOR THE VERDICT AND NEVER SLEEPS IN FRONT OF ONE. §4 trap 3:
   * two probes in this repo reported false DATA-LOSS claims from a fixed sleep
   * that expired before the answer arrived. The loop ends on the report, the
   * error, or a timeout that is itself reported as a failure.
   */
  const importThroughScreen = async (fixture) => {
    await testid("tally-import-file").setInputFiles(fx(fixture));
    await page.waitForTimeout(200);
    await testid("tally-import-button").click();
    for (let i = 0; i < 240; i++) {
      if ((await seen("tally-import-error")) || (await seen("tally-import-report"))) {
        return {
          error: await text("tally-import-error"),
          report: await text("tally-import-report"),
        };
      }
      await page.waitForTimeout(250);
    }
    return { error: "", report: "", timedOut: true };
  };

  await altMac("o", 900);
  rec("Alt+O opens the MIGRATE-FROM-TALLY screen", await seen("tally-import-screen"));

  const a = await importThroughScreen("08-refusal-decorated-amount.xml");
  rec(
    "a decorated AMOUNT ($ 1000.00) is REFUSED — the screen shows an error, not a report",
    !a.timedOut && a.error !== "" && a.report === "",
    a.timedOut ? "TIMED OUT waiting for a verdict" : a.error.slice(0, 200),
  );
  rec(
    "the refusal quotes the offending value, so the operator can find it in the file",
    /\$ 1000\.00/.test(a.error),
    a.error.slice(0, 220),
  );
  rec(
    "the refusal names WHICH voucher (an ordinal), and the ledger on it",
    /voucher 3\b/.test(a.error) && /Rent|Cash/i.test(a.error),
    a.error.slice(0, 220),
  );

  // -------------------------------------------------------------------------
  // ⛔⛔ THE ASSERTION THIS WHOLE PROBE EXISTS FOR.
  //
  // The screen has said the import failed. The claim under test is that the
  // books moved anyway. This assertion is written so that ATOMIC is the PASS:
  // if a future change wraps the import in a transaction, this goes green and
  // the finding below is retired by the same run that proves it.
  // -------------------------------------------------------------------------
  const afterA = await voucherCount(A.id);
  rec(
    "⛔ a REFUSED import left the books UNCHANGED (0 vouchers) — atomic",
    afterA === 0,
    `voucher table holds ${afterA} row(s) for this company after a refusal the operator was shown`,
  );
  rec(
    "...and if it is not atomic, the refusal SAYS how many vouchers landed",
    afterA === 0 || /\b2\b|landed|posted|committed|already/i.test(a.error),
    `${afterA} committed; the sentence the operator got was: ${a.error.slice(0, 220)}`,
  );

  // -------------------------------------------------------------------------
  // THE OPERATOR'S OWN ORACLE. A psql count is a fact about the schema; a
  // voucher visible in the operator's Day Book after they were told the import
  // failed is the finding. Alt+G Go To > Day Book, from the keyboard.
  // -------------------------------------------------------------------------
  await app.toGateway();
  await altMac("g", 700);
  await page.keyboard.type("Day Book");
  await page.waitForTimeout(400);
  await page.keyboard.press("Enter");
  await page.waitForTimeout(2000);
  const dayBook = (await body()).replace(/\s+/g, " ");
  rec(
    "control: the Day Book really opened (so its emptiness would mean something)",
    /Day Book/.test(dayBook),
    dayBook.slice(0, 120),
  );
  rec(
    "⛔ the Day Book shows NOTHING after the refused import",
    !/50,?000|30,?000/.test(dayBook),
    dayBook.slice(0, 400),
  );

  await app.browser.close();
  app = null;

  // =========================================================================
  // SCENARIO B — THE COMMA. Same file, same position, "83,650.00".
  //
  // ⚠️ THIS ONE IS NOT ABOUT CURRENCY AT ALL. `SplitQtyUnit` strips thousands
  // separators ON PURPOSE and its comment says "Commas are thousands
  // separators in Tally's own output" — so the codebase itself states Tally
  // emits them, while the three money readers refuse them. Driven separately
  // because it is a DIFFERENT defect with a different fix.
  // =========================================================================
  const B = await newCompany("B");
  app = await openApp({ company: B.id, expectCompany: B.name });
  {
    const { page: p2, altMac: alt2 } = app;
    const seen2 = async (t) => (await p2.getByTestId(t).count()) > 0;
    const text2 = async (t) =>
      (await seen2(t))
        ? (await p2.getByTestId(t).first().innerText()).replace(/\s+/g, " ").trim()
        : "";
    await alt2("o", 900);
    await p2.getByTestId("tally-import-file").setInputFiles(fx("09-refusal-grouped-amount.xml"));
    await p2.waitForTimeout(200);
    await p2.getByTestId("tally-import-button").click();
    let verdict = { error: "", report: "", timedOut: true };
    for (let i = 0; i < 240; i++) {
      if ((await seen2("tally-import-error")) || (await seen2("tally-import-report"))) {
        verdict = {
          error: await text2("tally-import-error"),
          report: await text2("tally-import-report"),
          timedOut: false,
        };
        break;
      }
      await p2.waitForTimeout(250);
    }
    rec(
      "a comma-grouped AMOUNT (83,650.00) is ACCEPTED — Tally's own output shape",
      !verdict.timedOut && verdict.report !== "" && verdict.error === "",
      verdict.timedOut ? "TIMED OUT" : verdict.error.slice(0, 200),
    );
    const afterB = await voucherCount(B.id);
    rec(
      "⛔ a comma refusal, if it refuses, still left the books UNCHANGED",
      verdict.error === "" ? afterB === 3 : afterB === 0,
      `${afterB} voucher(s) committed`,
    );
  }
  await app.browser.close();
  app = null;

  // =========================================================================
  // SCENARIO C — THE RECOVERY, GUID-LESS. What the operator does next.
  //
  // Import the decorated file (refused), then fix the amount and import again.
  // If the first import left 2 vouchers behind and the corrected file re-posts
  // all 3, the company ends holding FIVE vouchers — two of them duplicate
  // entries of real money, with no undo path anywhere in this product.
  // =========================================================================
  const C = await newCompany("C");
  app = await openApp({ company: C.id, expectCompany: C.name });
  {
    const { page: p3, altMac: alt3 } = app;
    const seen3 = async (t) => (await p3.getByTestId(t).count()) > 0;
    const put = async (fixture) => {
      await p3.getByTestId("tally-import-file").setInputFiles(fx(fixture));
      await p3.waitForTimeout(200);
      await p3.getByTestId("tally-import-button").click();
      for (let i = 0; i < 240; i++) {
        if ((await seen3("tally-import-error")) || (await seen3("tally-import-report"))) return;
        await p3.waitForTimeout(250);
      }
    };
    await alt3("o", 900);
    await put("08-refusal-decorated-amount.xml");
    const partial = await voucherCount(C.id);
    await put("08-refusal-corrected.xml");
    const total = await voucherCount(C.id);
    rec(
      "⛔ re-importing the CORRECTED file does not double the vouchers that already landed",
      total === 3,
      `${partial} landed under the refusal, ${total} in the books after the corrected re-import ` +
        `(3 = clean recovery, 5 = two duplicated vouchers of real money)`,
    );
  }
  await app.browser.close();
  app = null;

  // =========================================================================
  // SCENARIO D — THE RECOVERY, WITH GUIDs. The shape a REAL export has.
  //
  // ⚠️ THIS IS THE CONTROL THAT KEEPS SCENARIO C HONEST. Every TallyPrime
  // export stamps <GUID> on each voucher and `migration.go`'s Create arm skips
  // a GUID already imported. If D is clean and C is not, the finding is about
  // GUID-LESS files specifically, and saying so is the difference between a
  // registration a reader can act on and one that overstates its own blast
  // radius.
  // =========================================================================
  const D = await newCompany("D");
  app = await openApp({ company: D.id, expectCompany: D.name });
  {
    const { page: p4, altMac: alt4 } = app;
    const seen4 = async (t) => (await p4.getByTestId(t).count()) > 0;
    const put = async (fixture) => {
      await p4.getByTestId("tally-import-file").setInputFiles(fx(fixture));
      await p4.waitForTimeout(200);
      await p4.getByTestId("tally-import-button").click();
      for (let i = 0; i < 240; i++) {
        if ((await seen4("tally-import-error")) || (await seen4("tally-import-report"))) return;
        await p4.waitForTimeout(250);
      }
    };
    await alt4("o", 900);
    await put("08-refusal-decorated-amount-guid.xml");
    const partial = await voucherCount(D.id);
    await put("08-refusal-corrected-guid.xml");
    const total = await voucherCount(D.id);
    rec(
      "a GUID-bearing export recovers cleanly — the already-landed vouchers are SKIPPED",
      total === 3,
      `${partial} landed under the refusal, ${total} after the corrected re-import`,
    );
  }
  await app.browser.close();
  app = null;
} finally {
  if (app) {
    console.log("\n--- non-GET browser requests (status last) ---");
    for (const r of app.net) console.log("  " + r);
    await app.browser.close().catch(() => {});
  }
  // ⛔ EVERY MINTED TENANT IS SWEPT, catalog-derived, and NEVER by
  // `DELETE FROM company` alone — that strands ACL rows and /v1/me then answers
  // 500 for every session.
  for (const { id, name } of MINTED) {
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
      console.log(`torn down ${name}: ${left === "0" ? "clean" : `LEAKED (${left} rows)`}`);
    } catch (e) {
      console.log(`⚠️ teardown FAILED for ${name} (${id}): ${e}`);
    }
  }
}
process.exit(summary() > 0 ? 1 : 0);
