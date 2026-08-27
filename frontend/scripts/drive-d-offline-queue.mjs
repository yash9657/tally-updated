/**
 * TRACK D — THE OFFLINE QUEUE, driven in a real browser.
 *
 * ⛔ WHAT THIS PROVES THAT THE VITEST SUITE CANNOT, and there are four things:
 *
 *   1. ⛔⛔ THE TRANSPORT'S OWN FAILURE PATH. Every unit test hands the screen a
 *      hand-built `NetworkError`, because the mock client is not an HTTP client —
 *      there is no `fetch` under it to fail. Here the voucher POST is ABORTED at
 *      the network layer, so the rejection comes from Chrome, through the real
 *      `http.ts` try/catch, into the real judgement. If that try/catch were
 *      deleted the operator would get "Error: Failed to fetch" and lose the
 *      voucher, and every unit test would still pass.
 *
 *   2. ⛔⛔ INDEXEDDB. MEASURED: jsdom exposes no `indexedDB` at all and
 *      fake-indexeddb is not a dependency, so `store.ts`'s real adapter has NEVER
 *      run in a test — the suite exercises the memory fallback. This drive is the
 *      only place the durable store is exercised, and the assertion that matters
 *      is a RELOAD: a queue that forgets is worse than no queue, because the
 *      operator has already trusted it with the work.
 *
 *   3. ⛔⛔ A REAL COMMIT THROUGH THE REAL CLOUD SEQUENCER. The mock refuses to
 *      fake one on purpose (a second copy of the numbering allocator, the (HLC,
 *      node) order and the freeze rule would be unvalidated), so until this file
 *      nothing in this repository had ever committed an outbox FROM THE BROWSER.
 *      The oracle is POSTGRES, not the page: a commit that rendered a number
 *      without writing a voucher looks identical from the operator's chair.
 *
 *   4. ⛔ macOS Alt COMPOSITION. Alt+Q is driven with the harness's `altMac`,
 *      which sends the composed character a Mac really delivers.
 *
 * ⛔⛔ AND THE PROBE THAT MATTERS MOST IS THE NEGATIVE ONE. A 422 must NOT queue.
 * Getting that backwards tells an operator their invoice is safe when the books
 * REFUSED it — they walk away and nobody is billed. So the refusal half asserts
 * the QUEUE'S CONTENTS afterwards, not the status line: a build that queued the
 * voucher AND printed the error would satisfy a status-line assertion while being
 * the worst of both outcomes.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY OF ITS OWN, NOT ON Demo Traders, AND TEARS IT
 * DOWN with the catalog-derived sweep. Residue drops `verify:demo` below 34/34,
 * and a hand-rolled `DELETE FROM company` breaks /v1/me for every session.
 *
 * RUN:  node frontend/scripts/drive-d-offline-queue.mjs
 *       (needs cored :8087 and vite :5173 — the port matters, WebAuthn origin
 *        matching is exact. It creates its own company.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Offline Queue Drive ${stamp}`;

/**
 * ⚠️ SINGLE TOKENS WITH NO SPACES. The narration is asserted against the page's
 * innerText and against a Postgres payload; a sentence would fail on whitespace
 * collapsing rather than on anything real.
 */
const QUEUED_NARRATION = "ZZOFFLINEQUEUEDONE";
const REFUSED_NARRATION = "ZZOFFLINEREFUSEDTWO";

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

/**
 * Run `query` until it answers `want`, or give up.
 *
 * ⚠️ IT EXISTS FOR THE SAME REASON THE RELOAD PROBE POLLS: A FIXED SLEEP IN FRONT
 * OF AN ASSERTION IS A FLAKE. The commit's Postgres check first read the count
 * 3000ms after the click and got 0 — while the VERY NEXT query, a moment later,
 * found the voucher and its server-allocated number. A single read there does not
 * assert "the commit did not write"; it asserts "the commit had not written YET",
 * and it reports the first as if it were proof of the second.
 *
 * ⛔ STILL A REAL ORACLE: a commit that writes nothing never satisfies the poll,
 * so this times out and fails. The bound is on patience, not on what it can see.
 */
const sqlUntil = async (query, want, timeoutMs = 15000) => {
  const deadline = Date.now() + timeoutMs;
  let last = "";
  for (;;) {
    last = await sql(query);
    if (last === want || Date.now() > deadline) return last;
    await new Promise((r) => setTimeout(r, 400));
  }
};

// ---------------------------------------------------------------------------
// The tenant and two ledgers, over HTTP. They exist BEFORE the drive so that the
// on-demand ledger creation inside the accept path has nothing to do — a queue
// failure must not be confusable with a master-creation failure.
// ---------------------------------------------------------------------------
const TOK = (
  await j("POST", "/auth/token", {
    body: { subject: "browser-dev-owner", amr: ["webauthn"] },
  })
).body.token;

const created = await j("POST", "/v1/companies", {
  token: TOK,
  body: {
    name: COMPANY_NAME,
    fy_start_date: "2026-04-01",
    state_code: "29",
    gstin: "29AABCU9603R1ZM",
  },
});
if (created.status !== 201 && created.status !== 200) {
  throw new Error(
    `drive-offline: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
const base = `/v1/companies/${CO}`;

const mkLedger = async (name, group) => {
  // ⚠️ `group_name`, NOT `group` — the route answers 422 "ledger group is
  // required" for the latter, which reads like a missing value rather than a
  // wrong field name.
  const r = await j("POST", `${base}/ledgers`, {
    token: TOK,
    body: { name, group_name: group, created_by: "drive-offline" },
  });
  if (r.status !== 201 && r.status !== 200) {
    throw new Error(`drive-offline: ledger ${name}: ${r.status} ${JSON.stringify(r.body)}`);
  }
  return r.body.ledger_id ?? r.body.id;
};
const DR_NAME = "Drive Dr";
const CR_NAME = "Drive Cr";
await mkLedger(DR_NAME, "Current Assets");
await mkLedger(CR_NAME, "Current Liabilities");

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, toGateway, altMac, openFromGateway } = app;

const failedRequests = [];
page.on("response", async (r) => {
  if (r.status() < 400) return;
  let text = "";
  try {
    text = (await r.text()).slice(0, 200);
  } catch {
    text = "(unreadable)";
  }
  failedRequests.push({ status: r.status(), url: r.url(), body: text });
});

const btnTexts = async () =>
  await page.$$eval("button", (bs) => bs.map((b) => (b.textContent ?? "").trim()));

/** The journal POST for THIS company — the one request the drive interferes with. */
const JOURNAL_POST = `**/v1/companies/${CO}/vouchers/journal`;

/** Key a balanced two-line Journal with a stated narration, then Ctrl+A. */
const keyJournal = async (narration) => {
  await toGateway();
  await press("F7", 900);
  const set = async (testid, value) => {
    const el = page.locator(`[data-testid="${testid}"]`);
    await el.click();
    await el.fill(value);
  };
  await set("de-ledger:0", DR_NAME);
  await page.selectOption('[data-testid="de-side:0"]', "DR").catch(() => {});
  await set("de-amount:0", "1180");
  await page.click('[data-testid="de-add-line"]');
  await set("de-ledger:1", CR_NAME);
  await page.selectOption('[data-testid="de-side:1"]', "CR").catch(() => {});
  await set("de-amount:1", "1180");
  await set("voucher-narration", narration);
  await page.keyboard.press("Control+a");
  await page.waitForTimeout(1500);
  return await body();
};

// ===========================================================================
// ⛔ EVERY PROBE BELOW RUNS INSIDE ONE try, SO A THROW STILL REACHES THE SWEEP.
//
// The first run of this file died on a mistyped table name AFTER 19 green probes,
// never reached the teardown, and left a tenant behind — which then had to be
// swept by NAME before it could run again, and residue drops `verify:demo` below
// 34/34. A throw is now recorded as a FAILED PROBE (so the exit code is non-zero
// and the summary says which) and control falls through to the teardown.
//
// ⚠️ THE BLOCK IS DELIBERATELY NOT RE-INDENTED. Indenting 200 lines to add a
// guard would bury the change that matters in a whitespace diff.
// ===========================================================================
try {

// ---------------------------------------------------------------------------
// 1. ⛔⛔ A GENUINE TRANSPORT FAILURE QUEUES THE VOUCHER.
//
// `route.abort("failed")` is a real network abort: Chrome rejects the fetch, so
// this exercises http.ts's own try/catch and the NetworkError it raises. Only
// the voucher POST is aborted — everything else on the page keeps working, which
// is what a dropped connection mid-session actually looks like.
// ---------------------------------------------------------------------------
await page.route(JOURNAL_POST, (route) => route.abort("failed"));
let txt = await keyJournal(QUEUED_NARRATION);

rec(
  "⛔⛔ a dropped connection on Ctrl+A says QUEUED, NOT POSTED",
  /QUEUED, NOT POSTED/i.test(txt),
  txt.slice(0, 260).replace(/\s+/g, " "),
);
rec(
  "⛔ and it says the voucher is NOT in the books",
  /not in the books/i.test(txt),
  "the operator must not be left guessing whether it posted",
);
const provMatch = /INV\/[0-9-]+\/PROV-[A-Za-z0-9-]+/.exec(txt);
rec(
  "⛔ a PROVISIONAL number is shown, and it says PROV on its face",
  !!provMatch,
  provMatch ? provMatch[0] : txt.slice(0, 200).replace(/\s+/g, " "),
);
const PROVISIONAL = provMatch ? provMatch[0] : null;
rec(
  "⛔ the voucher screen did NOT report it as Saved",
  !/\bSaved\b/.test(txt),
  "a provisional number is not a voucher number; Ctrl+P and Alt+M read the saved fields",
);

// ---------------------------------------------------------------------------
// 2. THE TWO DOORS onto the outbox.
// ---------------------------------------------------------------------------
await altMac("q", 1200);
txt = await body();
rec(
  "Alt+Q opens the Offline Outbox",
  /Offline Outbox/i.test(txt) && !/not available in this build/i.test(txt),
  txt.slice(0, 160).replace(/\s+/g, " "),
);
rec(
  "⛔ Alt+Q did NOT land on a report frame reading 'not available in this build'",
  !/not available in this build/i.test(txt),
  "the navMachine matched the screen, not canonicalReportName's raw-label fallback",
);
rec(
  "⛔ the queued voucher is ON the outbox, with its provisional number",
  !!PROVISIONAL && txt.includes(PROVISIONAL),
  PROVISIONAL ?? "(no provisional captured)",
);
rec(
  "⛔ the number column is headed 'Provisional' and not like a voucher number",
  /Provisional/.test(txt) && !/\bNo\.\s*Change\b/.test(txt),
  "a provisional in a column read as a document number is how one gets quoted",
);
rec(
  "⛔ the screen SAYS the queue is unencrypted",
  /unencrypted/i.test(txt),
  "unencrypted books on a shared machine, disclosed rather than implied",
);

// ⛔ THE ONE PLACE THE REAL INDEXEDDB ADAPTER IS EXERCISED AT ALL. In jsdom the
// store falls back to memory and reports durable:false, so this assertion is the
// INVERSE of the unit test's and cannot be made anywhere else.
rec(
  "⛔⛔ a real browser reports the queue as DURABLE (IndexedDB, not the memory fallback)",
  /survives a reload/i.test(txt) && !/NOT survive a reload/i.test(txt),
  txt.slice(0, 200).replace(/\s+/g, " "),
);

await toGateway();
await openFromGateway("Offline Outbox");
txt = await body();
rec(
  "Gateway of Tally > Offline Outbox opens the same screen",
  /Offline Outbox/i.test(txt) && !/not available in this build/i.test(txt),
  txt.slice(0, 160).replace(/\s+/g, " "),
);
/**
 * ⛔ THE COLLISION CHECK. The Gateway is driven by typing into its search box, so
 * a label that is a SUBSTRING of a shipped one silently opens the shipped one —
 * which is exactly what happened to "Reconciliation" (it opened BANK
 * Reconciliation, every time, and the probe could not see it because it asserted
 * /Reconciliation/i). "Bill Inbox" is this label's nearest neighbour.
 */
rec(
  "⛔ and it is NOT the Bill Inbox, the nearest neighbouring label",
  !/Bill Inbox/i.test(txt) || /Offline Outbox/i.test(txt),
  txt.slice(0, 160).replace(/\s+/g, " "),
);

// ---------------------------------------------------------------------------
// 3. ⛔⛔ THE RELOAD. The assertion that cannot be made in jsdom.
// ---------------------------------------------------------------------------
await page.reload({ waitUntil: "domcontentloaded" });

/**
 * ⚠️ WAIT FOR THE SHELL, THEN POLL — NEVER A FIXED SLEEP IN FRONT OF A KEYSTROKE.
 * The first version of this probe slept 2500ms and pressed Alt+Q, and it passed
 * once and failed the next run with the page still on the GATEWAY: after a reload
 * the app re-authenticates and re-reads the company list, and that read is
 * O(tenants) — so on a database with a few drive tenants the sleep expires first
 * and the key is delivered to a shell that is not listening yet. It then reports
 * "THE QUEUE DID NOT SURVIVE A RELOAD", which is a data-loss claim, and would send
 * the next session hunting a storage bug that is not there.
 *
 * ⛔ IT IS STILL A REAL ORACLE. A queue that genuinely lost its contents never
 * shows the number, so this times out and fails — the poll bounds how long it
 * waits, not whether it can see the defect.
 */
await page.waitForFunction(
  () => document.body.innerText.includes("MASTERS / TRANSACTIONS"),
  { timeout: 30000 },
);
let survived = false;
for (let attempt = 0; attempt < 5 && !survived; attempt++) {
  await altMac("q", 1200);
  survived = await page
    .waitForFunction((prov) => document.body.innerText.includes(prov), PROVISIONAL, {
      timeout: 4000,
    })
    .then(() => true)
    .catch(() => false);
  if (!survived) await toGateway();
}
txt = await body();
rec(
  "⛔⛔ THE QUEUE SURVIVES A FULL PAGE RELOAD (real IndexedDB)",
  survived,
  `looking for ${PROVISIONAL} after reload: ${txt.slice(0, 200).replace(/\s+/g, " ")}`,
);
rec(
  "⛔ and the provisional number is the SAME one — the node id was not re-minted",
  survived,
  "a re-minted node id would orphan every number the operator has already quoted, and (worse) two profiles that both re-mint can collide on an idempotency key",
);

// ---------------------------------------------------------------------------
// 4. ⛔⛔ A REFUSAL IS NOT QUEUED. The direction that loses money.
//
// The abort is replaced by a real 422 response: it travels the same transport,
// is decoded by the same code, and arrives as an ApiError — the exact thing the
// judgement must NOT queue.
// ---------------------------------------------------------------------------
await page.unroute(JOURNAL_POST);
await page.route(JOURNAL_POST, (route) =>
  route.fulfill({
    status: 422,
    contentType: "application/json",
    body: JSON.stringify({ error: "debit total must equal credit total" }),
  }),
);
txt = await keyJournal(REFUSED_NARRATION);
rec(
  "⛔⛔ a 422 is REPORTED and not queued — the status line names the refusal",
  /Error:/.test(txt) && !/QUEUED, NOT POSTED/i.test(txt),
  txt.slice(0, 260).replace(/\s+/g, " "),
);
rec(
  "⛔ the server's own sentence survives to the operator",
  /debit total must equal credit total/i.test(txt),
  "a rewritten refusal is a refusal nobody can act on",
);

await altMac("q", 1200);
txt = await body();
/**
 * ⛔⛔ THE ORACLE THAT CAN ACTUALLY SEE THE DEFECT. Asserting only the status line
 * would pass for a build that queued the voucher AND printed the error — the worst
 * of both, because the refused intent would then be committed later behind the
 * operator's back. So the queue's CONTENTS are the assertion: still exactly one
 * row, and it is the network one.
 */
const rowCount = await page.locator('[data-testid="offline-outbox-row"]').count();
rec(
  "⛔⛔ THE REFUSED VOUCHER IS NOT IN THE QUEUE — still exactly one row",
  rowCount === 1,
  `${rowCount} row(s); expected the network-failed voucher only`,
);
rec(
  "⛔ and the row that IS there is the network one, not the refused one",
  txt.includes(QUEUED_NARRATION) && !txt.includes(REFUSED_NARRATION),
  txt.slice(0, 240).replace(/\s+/g, " "),
);

// ---------------------------------------------------------------------------
// 5. ⛔ NOTHING ON THIS SCREEN OFFERS TO PRETEND. A property over every button.
// ---------------------------------------------------------------------------
const buttons = await btnTexts();
rec(
  "⛔ no button on the outbox claims to POST or SAVE a single queued voucher",
  !buttons.some((b) => /^(post|save|accept)\b/i.test(b)),
  buttons.filter(Boolean).join(" | ").slice(0, 200),
);

// ---------------------------------------------------------------------------
// 6. ⛔⛔ A REAL COMMIT, AND POSTGRES IS THE ORACLE.
// ---------------------------------------------------------------------------
await page.unroute(JOURNAL_POST);
const before = await sql(
  // ⚠️ THE TABLE IS `voucher_version` AND IT CARRIES `company_id` ITSELF. There is
  // no `voucher` table in this schema — the head pointer is `voucher_head` — and
  // guessing one cost this drive a run (it threw after 19 green probes and leaked
  // the tenant, which then had to be swept by name before re-running).
  `SELECT count(*) FROM voucher_version
    WHERE company_id = ${q(CO)} AND payload->>'narration' = ${q(QUEUED_NARRATION)}`,
);
rec(
  "the control: the queued voucher is NOT in Postgres before the commit",
  before === "0",
  `count=${before}`,
);

await page.click('[data-testid="offline-outbox-commit"]');
await page.waitForTimeout(3000);
txt = await body();

const after = await sqlUntil(
  `SELECT count(*) FROM voucher_version
    WHERE company_id = ${q(CO)} AND payload->>'narration' = ${q(QUEUED_NARRATION)}`,
  "1",
);
rec(
  "⛔⛔ COMMITTING THE OUTBOX PUTS THE VOUCHER IN POSTGRES",
  after === "1",
  `count=${after} — a screen that rendered a number without writing a voucher looks identical from the browser`,
);

const finalNo = await sql(
  `SELECT voucher_no FROM voucher_version
    WHERE company_id = ${q(CO)} AND payload->>'narration' = ${q(QUEUED_NARRATION)} LIMIT 1`,
);
rec(
  "⛔ the SERVER allocated the real number (the provisional was never sent as voucher_no)",
  !!finalNo && !/PROV/.test(finalNo),
  `server number = ${finalNo || "(none)"}`,
);
rec(
  "⭐ and the screen shows BOTH numbers, so a quoted provisional can be corrected",
  !!PROVISIONAL && txt.includes(PROVISIONAL) && !!finalNo && txt.includes(finalNo),
  `${PROVISIONAL} -> ${finalNo}: ${txt.slice(0, 240).replace(/\s+/g, " ")}`,
);
rec(
  "⛔ the committed row has LEFT the queue",
  (await page.locator('[data-testid="offline-outbox-row"]').count()) === 0,
  "the server has it now; leaving it queued would invite a second commit",
);

// ---------------------------------------------------------------------------
// 7. ⛔ THE COMMITTED VOUCHER IS A REAL ONE — it shows up in the Day Book, which
// is the screen an operator would check. A queued intent must never appear there;
// a committed one must.
// ---------------------------------------------------------------------------
await toGateway();
await openFromGateway("Day Book");
await page.waitForTimeout(1500);
txt = await body();
rec(
  "⛔ the committed voucher is in the DAY BOOK under its server number",
  !!finalNo && txt.includes(finalNo),
  txt.slice(0, 240).replace(/\s+/g, " "),
);
rec(
  "⛔ and no PROVISIONAL number appears in the Day Book, ever",
  !/PROV-/.test(txt),
  "a provisional in a register is the failure the whole screen design prevents",
);

// ---------------------------------------------------------------------------
// Request hygiene: a 403 STEPUP_REQUIRED is the ceremony, not a failure, and the
// deliberate 422 above is ours. Anything else is a real problem.
// ---------------------------------------------------------------------------
const unexpected = failedRequests.filter(
  (f) => !/STEPUP_REQUIRED/.test(f.body) && !/debit total must equal/.test(f.body),
);
rec(
  "no unexpected failed requests (step-up challenges and the deliberate 422 excepted)",
  unexpected.length === 0,
  unexpected.map((f) => `${f.status} ${f.url}`).join(" | ").slice(0, 300),
);

} catch (driveError) {
  // ⛔ NOT A silent catch: it is a failed probe, so `summary()` reports it and the
  // exit code is non-zero. What it buys is the teardown below.
  rec("the drive ran to the end without throwing", false, String(driveError).slice(0, 400));
}

await app.browser.close();

// ---- teardown: the CATALOG-DERIVED sweep -------------------------------------
// ⛔ IT MUST RUN EVEN WHEN A PROBE ABOVE THREW. The first run of this file died on
// a mistyped table name AFTER 19 green probes, never reached here, and left a
// tenant behind — which then has to be swept by NAME before the next run, and
// residue drops `verify:demo` below 34/34. Everything above is therefore wrapped
// so that a throw is reported and still falls through to the sweep.
try {
  await sql(`DO $$
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
  const vouchers = await sql(`SELECT count(*) FROM voucher_head WHERE company_id = ${q(CO)}`);
  console.log(
    `\ntorn down ${COMPANY_NAME}: ${left === "0" && vouchers === "0" ? "clean" : `LEAKED (company=${left} voucher=${vouchers})`}`,
  );
} catch (e) {
  console.log(`\n⚠️ teardown FAILED for ${CO}: ${e}`);
}

process.exit(summary());
