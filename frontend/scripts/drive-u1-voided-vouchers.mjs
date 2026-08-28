/**
 * U1 — DOES THE IMPORTER POST VOUCHERS THAT TALLY KEEPS *OUT* OF THE BOOKS?
 *
 * ⛔ HOW THIS QUESTION AROSE: BY DIFFING A REAL EXPORT AGAINST THE PARSER.
 * ===========================================================================
 * The user's genuine TallyPrime export was enumerated element by element and
 * compared against every `xml:"…"` tag `internal/integration/tallyxml.go` binds.
 * `<VOUCHER>` has **207 distinct direct children in a real file and the parser
 * binds 15 of them.** Almost all of the other 192 are Tally internals nobody
 * should read — `USEFOREXCISE`, `LEDGERFBTCATEGORY`, `PERSISTEDVIEW`. Three are
 * not:
 *
 *   `<ISCANCELLED>`  — a CANCELLED voucher in Tally keeps its number and has NO
 *                      effect on the books. It is a void, not an entry.
 *   `<ISOPTIONAL>`   — an OPTIONAL voucher (Ctrl+L, RESEARCH.md:2173) is a
 *                      provisional entry deliberately EXCLUDED from the books;
 *                      RESEARCH.md:2225 records that only Cash Flow's "Include
 *                      Scenarios" pulls Memorandum, Reversing Journal and
 *                      Optional vouchers in.
 *   `<ISDELETED>`    — on a voucher, and on a LEDGER master.
 *
 * ⛔⛔ AND THE ASYMMETRY IS THE TELL, because this codebase already knows the
 * idea. `Deleted()` is defined and HONOURED on **five** master kinds — stock
 * group, unit, godown, stock item and account group (`inventory.go:299,363,405,423`
 * and `migration.go:384`). `TallyVoucher` binds none of the three. `TallyLedgerMaster`
 * binds no `ISDELETED` either. So the rule was understood and applied to the
 * masters that came last, and never reached the voucher or the ledger.
 *
 * ⚠️ WHAT THE REAL EXPORT COULD AND COULD NOT SETTLE, STATED PLAINLY:
 *
 *   - It CANNOT show the bug firing. All 121 vouchers carry `ISCANCELLED=No`,
 *     `ISOPTIONAL=No`, `ISDELETED=No` — this company simply has none.
 *   - It CAN show the export is not filtered: all five voucher-number series are
 *     CONTIGUOUS (Sales 7594-7641, Receipt 475-511, Payment 403-414, Journal
 *     60-68, Purchase 105-119 — **zero missing numbers in any of them**). A
 *     cancelled voucher in Tally RETAINS its number, so a filtering export would
 *     leave gaps. And Tally emits the flag on every voucher, which is what an
 *     export that carries them looks like rather than one that drops them.
 *
 * ⛔ SO THIS PROBE MEASURES THE IMPORTER RATHER THAN INFERRING FROM TALLY. The
 * fixture is what a cancelled and an optional voucher look like on the wire; the
 * only question it asks is what THIS PRODUCT does with them, which is knowable
 * exactly and is the half that would have to change either way.
 *
 * THE ORACLE IS THE BOOKS. A voucher visible in the operator's Day Book carrying
 * money that the source books voided is the whole finding, and no response body
 * can show it.
 *
 * ⭐ EVERY ASSERTION IS WRITTEN SO THAT **SKIPPING THE VOIDED ROWS IS THE PASS**.
 * If the importer is ever taught these three flags, this file goes green on the
 * same run that proves the fix, and D101 is retired by its own probe.
 *
 * RUN:  node frontend/scripts/drive-u1-voided-vouchers.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const CORED = "http://127.0.0.1:8087";
const HERE = dirname(fileURLToPath(import.meta.url));
const FIXTURE = join(HERE, "..", "..", "corpora", "migration", "10-voided-vouchers.xml");

// ⛔ THE CONTROL ON THE ORACLE ITSELF: if the fixture ever lost its flags, every
// assertion below would pass against a file with nothing to skip.
const RAW = readFileSync(FIXTURE, "utf8");
rec(
  "control: the fixture really marks one voucher CANCELLED and one OPTIONAL",
  /<ISCANCELLED>Yes<\/ISCANCELLED>/.test(RAW) && /<ISOPTIONAL>Yes<\/ISOPTIONAL>/.test(RAW),
);
rec(
  "control: ...and carries one ORDINARY voucher, so an empty result cannot be mistaken for a skip",
  /ZZLIVEVOUCHER/.test(RAW),
);
rec(
  "control: ...and a LEDGER master marked ISDELETED",
  /ZZDELETEDLEDGER[\s\S]{0,200}<ISDELETED>Yes<\/ISDELETED>/.test(RAW),
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
const COMPANY_NAME = `audit-u1 Voided ${stamp}`;
const created = await j("POST", "/v1/companies", {
  token: TOK,
  body: { name: COMPANY_NAME, fy_start_date: "2026-04-01", state_code: "29" },
});
if (created.status !== 201 && created.status !== 200) {
  throw new Error(
    `drive-u1-voided: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
console.log(`company ${COMPANY_NAME} = ${CO}`);

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, altMac } = app;
const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const text = async (t) =>
  (await seen(t)) ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim() : "";

const narrationCount = async (needle) =>
  Number(
    await sql(
      `SELECT count(*) FROM voucher_version WHERE company_id = ${q(CO)} AND payload->>'narration' LIKE ${q("%" + needle + "%")}`,
    ),
  );

let driveError = null;
try {
  await altMac("o", 1000);
  rec("Alt+O opens the importer", await seen("tally-import-screen"));

  await testid("tally-import-file").setInputFiles(FIXTURE);
  await page.waitForTimeout(300);
  await testid("tally-import-button").click();
  for (let i = 0; i < 240; i++) {
    if ((await seen("tally-import-error")) || (await seen("tally-import-report"))) break;
    await page.waitForTimeout(250);
  }
  const report = await text("tally-import-report");
  const error = await text("tally-import-error");
  rec(
    "the file imports (the voided rows are a semantic question, not a parse error)",
    report !== "" && error === "",
    error.slice(0, 200) || report.slice(0, 140),
  );

  // -------------------------------------------------------------------------
  // ⭐ THE CONTROL ARM. The ordinary voucher must be in the books, or a clean
  // result below would mean "the import did nothing" rather than "it skipped
  // the right rows".
  // -------------------------------------------------------------------------
  rec(
    "control: the ORDINARY voucher IS in the books, so a skip below means something",
    (await narrationCount("ZZLIVEVOUCHER")) === 1,
    `${await narrationCount("ZZLIVEVOUCHER")} row(s)`,
  );

  // =========================================================================
  // ⛔⛔ THE TWO ASSERTIONS THIS FILE EXISTS FOR. Skipping is the PASS.
  // =========================================================================
  const cancelled = await narrationCount("ZZCANCELLEDVOUCHER");
  rec(
    "⛔⛔ a voucher marked ISCANCELLED in Tally is NOT posted — a void is not an entry",
    cancelled === 0,
    `${cancelled} row(s) in the books. Tally retains a cancelled voucher's NUMBER and none of ` +
      `its effect; posting one puts 30,000.00 of sales in books the source shows as voided`,
  );
  const optional = await narrationCount("ZZOPTIONALVOUCHER");
  rec(
    "⛔⛔ a voucher marked ISOPTIONAL in Tally is NOT posted — it is provisional by definition",
    optional === 0,
    `${optional} row(s) in the books. RESEARCH.md:2225 — only Cash Flow's "Include Scenarios" ` +
      `pulls Optional vouchers in; everywhere else Tally keeps them out of the books`,
  );

  // -------------------------------------------------------------------------
  // AND THE LEDGER MASTER. `Deleted()` is honoured on five master kinds and
  // TallyLedgerMaster binds no ISDELETED at all.
  // -------------------------------------------------------------------------
  const deletedLedger = Number(
    await sql(`SELECT count(*) FROM ledger WHERE company_id = ${q(CO)} AND name = 'ZZDELETEDLEDGER'`),
  );
  rec(
    "⛔ a LEDGER master marked ISDELETED is NOT created — the rule five other master kinds honour",
    deletedLedger === 0,
    `${deletedLedger} row(s) in \`ledger\`. inventory.go honours Deleted() on stock groups, units, ` +
      `godowns and stock items; migration.go:384 on account groups; nothing on the ledger`,
  );

  // -------------------------------------------------------------------------
  // ⛔ THE OPERATOR'S OWN ORACLE. A psql count is a schema fact; voided money
  // in the Day Book is the finding.
  // -------------------------------------------------------------------------
  await app.toGateway();
  await altMac("g", 700);
  await page.keyboard.type("Day Book");
  await page.waitForTimeout(400);
  await page.keyboard.press("Enter");
  await page.waitForTimeout(2500);
  const dayBook = (await body()).replace(/\s+/g, " ");
  rec(
    "control: the Day Book opened and shows the ordinary voucher",
    /ZZLIVEVOUCHER/.test(dayBook),
    dayBook.slice(0, 200),
  );
  rec(
    "⛔⛔ NEITHER VOIDED VOUCHER APPEARS IN THE OPERATOR'S DAY BOOK",
    !/ZZCANCELLEDVOUCHER/.test(dayBook) && !/ZZOPTIONALVOUCHER/.test(dayBook),
    (dayBook.match(/ZZ\w+/g) ?? []).join(", "),
  );
  // ⚠️ WRITTEN SO THAT A WARNING IS THE PASS, like every other probe here. If
  // the decision is ever "post them but say so", this arm is what that fix has
  // to satisfy — and today it fails, because the report is silent.
  rec(
    "⚠️ ...and failing that, the report at least WARNS that voided vouchers were posted as live entries",
    /cancel|optional|void/i.test(report),
    `the report says "${(report.match(/Vouchers posted: \d+/) ?? [""])[0]}" and ` +
      `"${(report.match(/Warnings \(\d+\)/) ?? [""])[0]}" — silence is what makes this ` +
      `unrecoverable: nothing on any screen distinguishes the voided money from the real money`,
  );
} catch (e) {
  driveError = e;
  rec("the drive ran to the end without throwing", false, String(e).slice(0, 400));
} finally {
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
    console.log(`\ntorn down ${COMPANY_NAME}: ${left === "0" ? "clean" : `LEAKED (${left})`}`);
  } catch (e) {
    console.log(`\n⚠️ teardown FAILED for ${CO}: ${e}`);
  }
  if (driveError) console.log("\n--- the throw ---\n" + String(driveError).slice(0, 1200));
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);
