/**
 * T32 — MIGRATE FROM TALLY, driven from the keyboard in a real browser.
 *
 * ⛔ WHAT THIS PROVES THAT NOTHING ELSE CAN
 * ===========================================================================
 * `POST /import/tally-xml` has been green in `internal/migration`,
 * `internal/server` and `frontend/scripts/drive-tally-import.mjs` for a whole
 * lane, and until this screen shipped NO OPERATOR COULD REACH IT: migrating
 * meant hand-rolling a curl with the right content type. That is HANDOFF.md
 * §1.15's rule — a route table is not a surface — and it is the same defect
 * order processing, payroll and T14's census each shipped with.
 *
 * ⛔⛔ AND IT IS THE ONLY PLACE THE UTF-16 PROOF CAN BE COMPLETED.
 *
 * Every real TallyPrime export is UTF-16LE with a BOM. The trap is that
 * `File.text()` decodes as UTF-8 unconditionally, so the obvious screen code
 * destroys the only file shape a customer will ever send. The vitest suite pins
 * the property ("the bytes that left the browser are the bytes on disk") with a
 * stub client — but jsdom has NO `File.text()`, NO `File.arrayBuffer()` and no
 * real `fetch` body encoding at all, so the unit test can only assert what the
 * screen HANDED the client. This probe is what proves the bytes survive a real
 * Chromium `fetch`, a real multi-hop HTTP body and the server's own BOM sniffer.
 *
 * ⚠️ AND `FileReader.readAsText` IS NOT THE SAME FUNCTION — it BOM-sniffs and
 * decodes UTF-16 correctly. Measured in the unit suite. So a developer who
 * checked this with a FileReader would have concluded strings were fine.
 *
 * THE ORACLES, each chosen so a wrong answer FAILS rather than looks plausible:
 *
 *   1. THE FILE GOES IN THROUGH THE SCREEN — `setInputFiles` on the real input,
 *      then the button on the page. Not a fetch from this script.
 *
 *   2. THE FIGURES ARE READ OFF THE PAGE and compared against numbers MEASURED
 *      from the same fixture beforehand: 2 vouchers, 4 stock movements, 3
 *      opening positions. ⛔ `stock_lines` and `opening_positions` are the only
 *      figures that distinguish "imported the accounting" from "imported the
 *      trade" — a trial balance cannot see quantity, so an importer that read
 *      every stock line and dropped it answers 200 with books that balance
 *      perfectly and an empty Stock Summary.
 *
 *   3. AND THEY ARE CROSS-CHECKED AGAINST THE DATABASE. A screen that rendered
 *      the right numbers from a response the server never acted on would pass
 *      oracle 2 and fail this one.
 *
 *   4. THE WARNINGS ARE ON THE PAGE, IN FULL. Fixture 07 produces two real ones
 *      (a COMPOUND unit, and Tally's location tree flattened). An operator told
 *      only "2 vouchers posted" would believe the migration was lossless.
 *
 *   5. BOTH DOORS OPEN IT — Tally's own key (Alt+O) and Tally's own path
 *      (Gateway of Tally > Import). ⚠️ Alt+G Go To is NOT a door and cannot be:
 *      that picker is generated from ["Vouchers", ...REPORT_NAMES].
 *
 *   6. THE GLOBAL Alt+O DOES NOT SHADOW THE BANK-STATEMENT Alt+O. The narrower
 *      screen binding must still win on the reconciliation screen.
 *
 * ⛔ ALT+O IS EXACTLY THE KEY THAT DIES ON macOS. Option composes O into "ø", so
 * a probe pressing "Alt+o" would prove the binding works on machines nobody
 * develops on. It goes through the harness's altMac("o"), which sends the
 * composed character with the physical `code` — what a Mac really delivers.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY OF ITS OWN, NOT ON Demo Traders, AND TEARS IT
 * DOWN. An import moves a trial balance; residue drops `verify:demo` below 34/34.
 *
 * ⛔ SYMBOL-CHECK THE RUNNING `cored` BEFORE BELIEVING A GREEN RUN. `make
 * run-cored` is `go run`, so a stale process is indistinguishable from a fix that
 * does not work. This track changes NO Go code, so the check is that it is
 * recent:
 *
 *   ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"
 *
 * RUN:  node frontend/scripts/drive-t32-import-screen.mjs
 *       (needs cored :8087 and vite :5173 — the port matters, WebAuthn origin
 *        matching is exact. It creates its own company.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Migrate Drive ${stamp}`;

const HERE = dirname(fileURLToPath(import.meta.url));
const FIXTURE = join(
  HERE,
  "..",
  "..",
  "corpora",
  "migration",
  "07-tallyprime-inventory-shapes.xml",
);

/**
 * ⛔ MEASURED FROM THE FIXTURE, NOT GUESSED. Taken by posting
 * `07-tallyprime-inventory-shapes.xml` to the route directly and reading the
 * response, so a change in the importer shows up here as a FAILURE rather than
 * as an assertion quietly rewritten to match.
 *
 * ⚠️ IF ONE OF THESE MOVES, DO NOT EDIT THE NUMBER UNTIL YOU KNOW WHY. The whole
 * point of `stock_lines` is that a drop to 0 looks like a clean import.
 */
const EXPECT = {
  sourceCompany: "Goods Trading Co",
  vouchers: 2,
  stockLines: 4,
  openingPositions: 3,
  stockItems: ["PACKETS", "RICE", "WHEAT"],
  godowns: ["WAREHOUSE A", "WAREHOUSE B"],
  batches: ["RICE/R-1", "RICE/R-2", "WHEAT/W-1"],
};

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

// ---------------------------------------------------------------------------
// THE FIXTURE'S ENCODING IS CHECKED HERE, BEFORE ANY BROWSER IS OPENED.
//
// ⛔ If the corpus file were ever re-saved as UTF-8, every UTF-16 assertion
// downstream would pass vacuously — the file would round-trip through the bug
// unharmed and this probe would be green against a screen that destroys real
// exports. An oracle that cannot see the defect is not an oracle.
// ---------------------------------------------------------------------------
const FIXTURE_BYTES = readFileSync(FIXTURE);
rec(
  "the fixture really is UTF-16LE with a BOM — the control on the oracle itself",
  FIXTURE_BYTES[0] === 0xff && FIXTURE_BYTES[1] === 0xfe,
  `first bytes: ${FIXTURE_BYTES[0]?.toString(16)} ${FIXTURE_BYTES[1]?.toString(16)}, ${FIXTURE_BYTES.length} bytes`,
);

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
    `drive-t32: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
console.log(`company ${COMPANY_NAME} = ${CO}`);

// ⛔ ONE BANK LEDGER, SEEDED OVER HTTP, PURELY SO STEP 8 IS NOT VACUOUS. The
// reconciliation screen resolves a company's ONLY bank account and opens on it;
// with none it shows `brs-no-banks` and Alt+O answers "select a bank account
// first", which would make the precedence check pass for the wrong reason.
const bank = await j("POST", `/v1/companies/${CO}/ledgers`, {
  token: TOK,
  body: {
    name: "Drive Bank",
    group_name: "Bank Accounts",
    created_by: "drive-t32",
  },
});
if (bank.status !== 201) {
  console.log(
    `⚠️ drive-t32: the bank ledger was NOT created (${bank.status} ${JSON.stringify(bank.body)}) — step 8 will report that it could not run`,
  );
}

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, altMac } = app;

const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const text = async (t) =>
  (await seen(t))
    ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim()
    : "";

try {
  // =========================================================================
  // 1. ALT+O OPENS THE SCREEN — the macOS-composed key, not "Alt+o".
  // =========================================================================
  rec(
    "control: the import screen is ABSENT before the key",
    !(await seen("tally-import-screen")),
    "",
  );
  await altMac("o", 900);
  rec(
    "Alt+O (macOS Option+O) opens the MIGRATE-FROM-TALLY screen",
    await seen("tally-import-screen"),
    (await body()).slice(0, 120),
  );
  rec(
    "the frame is titled Import from Tally, not the default 'Tally'",
    (await body()).includes("Import from Tally"),
    "",
  );

  // =========================================================================
  // 2. THE CONSEQUENCES ARE STATED BEFORE THE OPERATOR ACTS, read OFF THE PAGE.
  // =========================================================================
  const target = await text("tally-import-target");
  rec(
    "the screen names the company the export will be posted INTO",
    target.includes(COMPANY_NAME),
    target.slice(0, 160),
  );
  rec(
    "...and says an import cannot be undone",
    /no way to undo/i.test(target),
    target.slice(0, 160),
  );
  rec(
    "no import is offered until a file has been chosen",
    await testid("tally-import-button").first().isDisabled(),
    "",
  );

  // =========================================================================
  // 3. THE FILE GOES IN THROUGH THE SCREEN — and the BYTES SURVIVE.
  //
  // ⛔ THIS IS THE HALF OF THE UTF-16 PROOF THAT jsdom CANNOT DO. A real
  // Chromium reads the File, a real fetch encodes the body, a real HTTP hop
  // carries it and the server's own sniffer decodes it. If the screen had used
  // `await file.text()`, THIS is where it would fail — and it would fail with a
  // parse error naming the envelope, not with anything about encodings.
  // =========================================================================
  await testid("tally-import-file").first().setInputFiles(FIXTURE);
  const chosen = await text("tally-import-chosen");
  rec(
    "the screen shows the file it is holding, with its size in bytes",
    chosen.includes("07-tallyprime-inventory-shapes.xml") &&
      // ⚠️ COMMAS STRIPPED. The screen renders the size with toLocaleString, so
      // the page says "15,328 bytes" — the first version of this probe compared
      // against "15328" and failed on the SCREEN BEING RIGHT. Worth the line:
      // asserting a raw number against a localised one is a probe that fails
      // for the wrong reason, which is the same class of bug as a vacuous pass.
      chosen.replace(/,/g, "").includes(String(FIXTURE_BYTES.length)),
    chosen,
  );

  await testid("tally-import-button").first().click();
  await page.waitForTimeout(2500);

  const err = await text("tally-import-error");
  rec(
    "the UTF-16LE export IMPORTS through the screen — no refusal at all",
    err === "" && (await seen("tally-import-report")),
    err === "" ? "no error shown" : `REFUSED: ${err.slice(0, 240)}`,
  );

  // =========================================================================
  // 4. THE FIGURES, READ OFF THE PAGE, against numbers measured from the file.
  // =========================================================================
  const report = (await text("tally-import-report")).replace(/\s+/g, " ");
  rec(
    "the report names the SOURCE company out of the export's own header",
    report.includes(EXPECT.sourceCompany),
    report.slice(0, 140),
  );
  const postedTxt = await text("tally-import-posted");
  rec(
    `the voucher count is on the page (${EXPECT.vouchers})`,
    new RegExp(`\\b${EXPECT.vouchers}\\b`).test(postedTxt),
    postedTxt,
  );
  const stockTxt = await text("tally-import-stock-lines");
  rec(
    `⛔ THE GOODS: stock movements written is on the page (${EXPECT.stockLines})`,
    new RegExp(`\\b${EXPECT.stockLines}\\b`).test(stockTxt),
    stockTxt,
  );
  const openingTxt = await text("tally-import-opening-positions");
  rec(
    `⛔ THE GOODS: opening positions written is on the page (${EXPECT.openingPositions})`,
    new RegExp(`\\b${EXPECT.openingPositions}\\b`).test(openingTxt),
    openingTxt,
  );
  // This file HAS opening stock and HAS movement, so NEITHER warning belongs.
  rec(
    "neither goods warning is shown for a file that carries both",
    !(await seen("tally-import-no-stock")) &&
      !(await seen("tally-import-opening-gap")),
    `no-stock: ${await seen("tally-import-no-stock")}, opening-gap: ${await seen("tally-import-opening-gap")}`,
  );

  // =========================================================================
  // 5. THE WARNINGS ARE ON THE PAGE, IN FULL — and this file produces two.
  // =========================================================================
  const warnings = await text("tally-import-warnings");
  rec(
    "the COMPOUND-unit warning is rendered in full, not counted",
    /COMPOUND/i.test(warnings) && /BOX/.test(warnings) && /BAG/.test(warnings),
    warnings.slice(0, 200),
  );
  rec(
    "...and the flattened-location warning too",
    /WAREHOUSE B/.test(warnings) && /(FLAT|flat)/.test(warnings),
    warnings.slice(0, 260),
  );
  const caveat = await text("tally-import-caveat");
  rec(
    "the screen says what a CLEAN-looking report still does not prove",
    /empty warning list/i.test(caveat) &&
      /balanced trial balance/i.test(caveat) &&
      /stock summary/i.test(caveat),
    caveat.slice(0, 200),
  );
  const items = await text("tally-import-stock-items");
  rec(
    "the inventory masters it CREATED are named on the page",
    EXPECT.stockItems.every((n) => items.includes(n)),
    items.slice(0, 140),
  );

  // =========================================================================
  // 6. AND THE DATABASE AGREES.
  //
  // ⛔ A screen that rendered a response the server never acted on passes every
  // assertion above. These are the rows.
  // =========================================================================
  const dbVouchers = await sql(
    `SELECT count(*) FROM voucher_head WHERE company_id = ${q(CO)}`,
  );
  rec(
    "the vouchers are REALLY in the books, not just in a response",
    Number(dbVouchers) === EXPECT.vouchers,
    `voucher_head rows: ${dbVouchers} (expected ${EXPECT.vouchers})`,
  );
  const dbItems = await sql(
    `SELECT count(*) FROM stock_item WHERE company_id = ${q(CO)}`,
  );
  rec(
    "the stock ITEMS are really there",
    Number(dbItems) === EXPECT.stockItems.length,
    `stock_item rows: ${dbItems} (expected ${EXPECT.stockItems.length})`,
  );
  // ⛔ THE ONE THAT WOULD HAVE CAUGHT THE ORIGINAL "trial balance is perfect and
  // the Stock Summary is empty" defect. Counted from the catalog rather than a
  // hardcoded table name, so a renamed movement table fails loudly here instead
  // of silently reporting zero.
  const movementTable = await sql(
    `SELECT table_name FROM information_schema.columns
      WHERE table_schema='public' AND column_name='company_id'
        AND table_name IN ('stock_movement','stock_ledger_entry','inventory_movement')
      LIMIT 1`,
  );
  if (movementTable) {
    const dbMoves = await sql(
      `SELECT count(*) FROM ${movementTable} WHERE company_id = ${q(CO)}`,
    );
    rec(
      `the stock MOVEMENTS are really there (${movementTable})`,
      Number(dbMoves) >= EXPECT.stockLines,
      `${movementTable} rows: ${dbMoves} (page said ${EXPECT.stockLines})`,
    );
  } else {
    rec(
      "the stock movement table could be located in the catalog",
      false,
      "no known movement table found — the DB cross-check for THE GOODS did not run",
    );
  }

  // =========================================================================
  // 7. THE SECOND DOOR — Gateway of Tally > Import, which is Tally's own path.
  //
  // ⚠️ VIA THE HARNESS'S openFromGateway, NOT BY CLICKING THE LABEL. The first
  // version of this probe clicked the text and pressed Enter, which left the
  // SelectionList's selection on its default and opened a SALES VOUCHER — and
  // that is exactly why openFromGateway exists (two audit rows once named the
  // wrong key and were logged as unreachable). It drives the Gateway's own
  // search box, which is how a keyboard operator gets there.
  // =========================================================================
  await app.toGateway();
  const gatewayBody = await body();
  rec(
    "the Gateway carries an 'Import from Tally' entry",
    gatewayBody.includes("Import from Tally"),
    gatewayBody.includes("Gateway of Tally") ? "on the Gateway" : "NOT on the Gateway",
  );
  await app.openFromGateway("Import from Tally", 1200);
  rec(
    "selecting it opens the import screen (not a report frame of that name)",
    await seen("tally-import-screen"),
    (await body()).slice(0, 120),
  );

  // =========================================================================
  // 8. THE GLOBAL Alt+O DOES NOT SHADOW THE BANK-STATEMENT IMPORT.
  //
  // ⛔ Alt+O was ALREADY BOUND, screen-scoped, on bank-reconciliation. Adding a
  // global is safe only because resolveAction consults screen bindings FIRST.
  // That is a precedence claim, and this is the browser check on it: if it ever
  // inverts, Alt+O on the reconciliation screen opens a company-wide XML
  // importer over a bank statement — and both are called "import".
  //
  // ⛔⛔ AND IT NEEDS A REAL BANK ACCOUNT OR IT IS VACUOUS. This tenant is created
  // empty and the export carries no bank ledger, so without the account seeded
  // over HTTP above the screen shows `brs-no-banks` and Alt+O correctly answers
  // "select a bank account first" — a refusal for the WRONG REASON, which would
  // have been logged as a pass. `brs-as-on` is the control that the statement is
  // really up before the key is pressed.
  // =========================================================================
  await app.openFromGateway("Bank Reconciliation", 2000);
  await page.waitForTimeout(1200);
  const statementUp = await seen("brs-as-on");
  rec(
    "control: the reconciliation STATEMENT is up, with a resolved bank account",
    statementUp,
    statementUp
      ? "brs-as-on present"
      : `no statement — no-banks: ${await seen("brs-no-banks")}, body: ${(await body()).slice(0, 100)}`,
  );
  if (statementUp) {
    await altMac("o", 1200);
    const panel = await seen("stmt-import-panel");
    const tally = await seen("tally-import-screen");
    rec(
      "Alt+O on the reconciliation screen still means the BANK STATEMENT import",
      panel && !tally,
      `stmt panel: ${panel}, tally importer: ${tally}`,
    );
  } else {
    rec(
      "Alt+O precedence on the reconciliation screen could be checked at all",
      false,
      "the statement never came up, so pressing Alt+O would have proved nothing",
    );
  }
} finally {
  // ⛔ THE THROWAWAY COMPANY IS DROPPED. An import moves a trial balance, and
  // this one posts vouchers AND stock into a fresh tenant; residue drops
  // verify:demo below 34/34. Catalog-derived, so a table added by tomorrow's
  // migration is swept without anyone remembering to add it.
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
  for (const r of app.net) console.log("  " + r);
  console.log("\n--- console errors ---");
  for (const e of app.consoleErrors.slice(0, 8)) console.log("  " + e);
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);
