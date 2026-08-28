/**
 * U2 — OFFLINE OUTBOX (Alt+Q): THE FOUR CLAIMS THE EXISTING DRIVE NEVER REACHES.
 *
 * ⛔ WHAT `drive-d-offline-queue.mjs` ALREADY PROVES, SO THIS DOES NOT REPEAT IT
 * ===========================================================================
 * That file is thorough and it is green (27/27, re-run 2026-08-27): Alt+O… Alt+Q
 * through `altMac`, both doors, the PROV literal, the "Provisional" column
 * heading, the unencrypted-store disclosure, a REAL IndexedDB that survives a
 * reload with the node id intact, a 422 reported-and-not-queued, one commit
 * through the real cloud sequencer with POSTGRES as the oracle, the server
 * number allocated, the PROV→real mapping, and no provisional ever in the Day
 * Book.
 *
 * ⛔⛔ AND IT DRIVES EXACTLY ONE VOUCHER, so four of the row's claims — every one
 * of them a claim about a QUEUE rather than about an entry — have never been
 * driven at all:
 *
 *   1. "commits the whole queue as ONE write, OLDEST FIRST". With one row there
 *      is no order to get wrong and no batch to be a batch.
 *
 *   2. ⛔ "A WHOLE-BATCH REFUSAL IS SHOWN AS A BANNER AND NEVER AS A PER-VOUCHER
 *      STATUS." The row argues this at length: a screen that said "these 12
 *      failed" would invite somebody to re-key twelve invoices that are sitting
 *      intact below — and then commit both copies. Nothing has ever produced a
 *      whole-batch refusal. This drive produces a REAL one by setting the
 *      company's back-dated cutoff over HTTP, which is the mechanism the row
 *      itself names.
 *
 *   3. "and can discard a row". Discard is not a convenience — the screen's own
 *      comment says one voucher dated inside a closed period refuses the ENTIRE
 *      batch, so without discard the whole queue is un-committable. It has never
 *      been pressed.
 *
 *   4. "cleared on sign-out" (the FeaturesScreen row) / "anything not committed
 *      by sign-out is lost" (the screen). That is a DATA claim about a shared
 *      machine: if it were false, one operator's un-posted books would be
 *      readable by the next person to sign in on the same browser profile.
 *
 * ⛔ THE ORACLE FOR EVERY "NOT IN THE BOOKS" CLAIM IS POSTGRES, never the page.
 * A screen that renders an empty queue while the rows are still on disk, or one
 * that renders a refusal after writing the batch, looks identical from the
 * operator's chair.
 *
 * ⚠️ AND THE CUTOFF ARM CAN FAIL TO RUN RATHER THAN FAIL. If the commit is NOT
 * refused, the batch-refusal probes would pass vacuously against a screen that
 * never rendered a banner, so the drive records that it could not run instead —
 * a check that did not run must never look like a check that passed.
 *
 * RUN:  node frontend/scripts/drive-u2-outbox-batch.mjs
 *       (needs cored :8087 and vite :5173 — the port matters, WebAuthn origin
 *        matching is exact. It creates its own company and reaps it.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `audit-u2 Outbox ${stamp}`;

/** ⚠️ SINGLE TOKENS, NO SPACES — asserted against innerText and a JSON payload. */
const V1 = "ZZU2FIRSTQUEUED";
const V2 = "ZZU2SECONDQUEUED";
const V3 = "ZZU2THIRDQUEUED";
const V4 = "ZZU2SIGNOUTVICTIM";

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
    `drive-u2: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
const base = `/v1/companies/${CO}`;
console.log(`company ${COMPANY_NAME} = ${CO}`);

const mkLedger = async (name, group) => {
  // ⚠️ `group_name`, NOT `group` — the route answers 422 "ledger group is
  // required" for the latter, which reads like a missing value.
  const r = await j("POST", `${base}/ledgers`, {
    token: TOK,
    body: { name, group_name: group, created_by: "drive-u2" },
  });
  if (r.status !== 201 && r.status !== 200) {
    throw new Error(`drive-u2: ledger ${name}: ${r.status} ${JSON.stringify(r.body)}`);
  }
};
const DR_NAME = "Drive Dr";
const CR_NAME = "Drive Cr";
await mkLedger(DR_NAME, "Current Assets");
await mkLedger(CR_NAME, "Current Liabilities");

/**
 * ⛔ THE BATCH REFUSAL IS PRODUCED BY THE REAL CONTROL, NOT BY A ROUTE STUB.
 * A `page.route(... 422)` would prove the banner RENDERS and nothing about
 * whether the cutoff judges a batch as a unit — which is the claim. The cutoff
 * lives on the SECURITY LEVEL (`security_level.backdated_cutoff`), so it is set
 * through the admin route the product itself exposes.
 */
const ownerLevelID = async () => {
  const r = await j("GET", `${base}/security-levels`, { token: TOK });
  const levels = r.body.levels ?? r.body.security_levels ?? r.body ?? [];
  const list = Array.isArray(levels) ? levels : [];
  const owner = list.find((l) => l.is_owner) ?? list[0];
  return owner?.id ?? owner?.level_id ?? null;
};
/**
 * ⛔ CLEARING IS THE EMPTY STRING, NOT `null`. `levelReq.BackdatedCutoff` is a
 * `*string` so that ABSENT and PRESENT are distinguishable, and `null` decodes
 * to absent — i.e. "leave the cutoff alone". A probe that cleared with `null`
 * gets 200 and a cutoff that is still set, and the NEXT commit is refused for a
 * reason the probe is no longer looking for. That happened on the first run of
 * this file: the ordered-commit arm read 0 vouchers and looked like a product
 * failure. `parseCutoff("")` is the clear.
 */
const setCutoff = async (levelID, date) =>
  await j("PUT", `${base}/security-levels/${levelID}`, {
    token: TOK,
    body: { backdated_cutoff: date ?? "" },
  });

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, toGateway, altMac } = app;

const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const rows = async () => await testid("offline-outbox-row").count();
const text = async (t) =>
  (await seen(t)) ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim() : "";

/** Vouchers actually in the books for this tenant. Never the page. */
const inBooks = async () =>
  Number(await sql(`SELECT count(*) FROM voucher_head WHERE company_id = ${q(CO)}`));

/** The journal POST this drive aborts to simulate a dropped connection. */
const JOURNAL_POST = `**/v1/companies/${CO}/vouchers/journal`;

const keyJournal = async (narration) => {
  await toGateway();
  await press("F7", 900);
  const set = async (t, value) => {
    const el = page.locator(`[data-testid="${t}"]`);
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
};

const toOutbox = async () => {
  await toGateway();
  await altMac("q", 1400);
  for (let i = 0; i < 20; i++) {
    if (await seen("offline-outbox-screen")) return true;
    await page.waitForTimeout(250);
  }
  return false;
};

// ===========================================================================
// ⛔ ONE try FOR THE WHOLE DRIVE. A drive that died after 19 green probes once
// leaked a tenant, and residue drops `verify:demo` below 34/34.
// ===========================================================================
let driveError = null;
try {
  // =========================================================================
  // 1. QUEUE TWO VOUCHERS. Every claim below is about a QUEUE, and one row is
  //    not a queue.
  // =========================================================================
  await page.route(JOURNAL_POST, (route) => route.abort("failed"));
  await keyJournal(V1);
  await keyJournal(V2);

  rec("Alt+Q opens the Offline Outbox", await toOutbox());
  rec(
    "⛔ BOTH dropped vouchers are queued — two rows, not one",
    (await rows()) === 2,
    `${await rows()} row(s)`,
  );
  const commitLabel = await text("offline-outbox-commit");
  rec(
    "the commit button counts the whole queue, so the operator knows what one press sends",
    /Commit all 2/.test(commitLabel),
    commitLabel,
  );
  rec(
    "⛔ the screen states the queue is sent as ONE write, oldest first",
    /single write, oldest first/i.test((await body()).replace(/\s+/g, " ")),
  );
  rec(
    "control: neither queued voucher is in the books",
    (await inBooks()) === 0,
    `${await inBooks()} in voucher_head`,
  );

  // =========================================================================
  // 2. ⛔⛔ THE WHOLE-BATCH REFUSAL. A REAL one, from the back-dated cutoff.
  //
  // Both queued vouchers are dated today; a cutoff of tomorrow makes every one
  // of them back-dated, so the batch is refused as a unit — which is the exact
  // mechanism the row cites as the reason discard has to exist.
  // =========================================================================
  const levelID = await ownerLevelID();
  const tomorrow = new Date(Date.now() + 86400000).toISOString().slice(0, 10);
  const set = levelID ? await setCutoff(levelID, tomorrow) : { status: 0 };
  rec(
    "control: the back-dated cutoff was set on this tenant, so the refusal below is REAL",
    set.status === 200 || set.status === 204,
    `PUT security-levels/${levelID} -> ${set.status} ${JSON.stringify(set.body).slice(0, 160)}`,
  );

  await page.reload({ waitUntil: "networkidle" });
  await page.waitForTimeout(1200);
  await toOutbox();
  await testid("offline-outbox-commit").click();
  for (let i = 0; i < 60; i++) {
    if ((await seen("offline-outbox-refusal")) || (await inBooks()) > 0) break;
    await page.waitForTimeout(500);
  }
  const refused = await seen("offline-outbox-refusal");
  const booksAfterRefusal = await inBooks();

  if (!refused && booksAfterRefusal > 0) {
    // ⚠️ THE ARM DID NOT RUN. Recorded as a failure that says so, never as a
    // pass: a check that did not run must not look like a check that passed.
    rec(
      "⛔ the batch-refusal arm could RUN at all (the cutoff refused the commit)",
      false,
      `the commit SUCCEEDED — ${booksAfterRefusal} voucher(s) in the books. The cutoff did not ` +
        `deny this level, so every banner assertion below would have been vacuous`,
    );
  } else {
    rec(
      "⛔⛔ a whole-batch refusal is shown as a BANNER (role=alert), not as a per-voucher status",
      refused,
      await text("offline-outbox-refusal"),
    );
    rec(
      "⛔ the banner says the queue is INTACT before it says why — the anti-re-key sentence",
      /still queued exactly as you keyed it/i.test(await text("offline-outbox-refusal")),
      (await text("offline-outbox-refusal")).slice(0, 200),
    );
    rec(
      "⛔ the SERVER'S own sentence is carried, so the operator knows which control refused",
      /cutoff|back-?dated|closed/i.test(await text("offline-outbox-refusal-message")),
      await text("offline-outbox-refusal-message"),
    );
    rec(
      "⛔⛔ EVERY ROW IS STILL QUEUED — a refusal that dropped one is the failure that loses work",
      (await rows()) === 2,
      `${await rows()} row(s) after the refusal`,
    );
    rec(
      "⛔ and NOTHING was written — the batch is judged as a unit, not voucher by voucher",
      booksAfterRefusal === 0,
      `${booksAfterRefusal} in voucher_head`,
    );
    rec(
      "⛔ no per-voucher failure status appears anywhere on the screen",
      !/failed/i.test((await body()).replace(/\s+/g, " ")),
      "a per-voucher 'failed' invites re-keying vouchers that are sitting intact below",
    );
  }

  // Lift the cutoff so the rest of the drive is about the queue and not about it.
  if (levelID) await setCutoff(levelID, null);
  await page.reload({ waitUntil: "networkidle" });
  await page.waitForTimeout(1200);
  await toOutbox();

  // =========================================================================
  // 3. DISCARD. Not a convenience — it is the only escape from a queue one
  //    row has made un-committable.
  // =========================================================================
  const before = await rows();
  const secondRow = testid("offline-outbox-row").nth(1);
  const discardedNarration = (await secondRow.innerText()).includes(V2) ? V2 : V1;
  await secondRow.getByRole("button", { name: /discard/i }).click();
  await page.waitForTimeout(1200);
  rec(
    "⛔ Discard removes exactly ONE row",
    (await rows()) === before - 1,
    `${before} -> ${await rows()}`,
  );
  rec(
    "⛔ and the discarded voucher is the one that was discarded, not a neighbour",
    !(await body()).includes(discardedNarration),
    `discarded ${discardedNarration}`,
  );
  rec(
    "⛔ discarding touches NO books — it removes a thing that was never there",
    (await inBooks()) === 0,
    `${await inBooks()} in voucher_head`,
  );

  // =========================================================================
  // 4. ⛔ OLDEST FIRST. Queue a third so there are two again, then commit and
  //    read the ORDER off the numbers the server allocated.
  //
  // ⛔⛔ THE ORACLE IS THE SERVER'S OWN NUMBERING, not the row order on screen.
  // The sequencer allocates in the order it receives, so the voucher queued
  // FIRST must carry the LOWER number. A screen that listed them oldest-first
  // and sent them newest-first would look identical.
  // =========================================================================
  await keyJournal(V3);
  await toOutbox();
  rec("two rows are queued again, ready for an ordered commit", (await rows()) === 2);

  await testid("offline-outbox-commit").click();
  for (let i = 0; i < 80; i++) {
    if ((await inBooks()) >= 2) break;
    await page.waitForTimeout(500);
  }
  rec(
    "⛔ the batch commits — both vouchers are in POSTGRES",
    (await inBooks()) === 2,
    `${await inBooks()} in voucher_head`,
  );
  const order = await sql(`
SELECT string_agg(n, ',' ORDER BY num)
FROM (
  SELECT vv.voucher_no::int AS num,
         CASE WHEN vv.payload->>'narration' LIKE '%${V1}%' THEN 'V1'
              WHEN vv.payload->>'narration' LIKE '%${V3}%' THEN 'V3'
              ELSE 'other' END AS n
    FROM voucher_version vv
   WHERE vv.company_id = ${q(CO)}
) s`);
  rec(
    "⛔⛔ OLDEST FIRST — the voucher queued first carries the LOWER server number",
    order === "V1,V3",
    `server numbering order was: ${order} (V1 was queued before V3)`,
  );
  rec(
    "⛔ and the DISCARDED voucher is in neither the queue nor the books",
    !(await sql(`SELECT count(*) FROM voucher_version WHERE company_id = ${q(CO)} AND payload->>'narration' LIKE '%${V2}%'`)).startsWith("1"),
    "a discard that merely hid a row would leave it here",
  );

  // =========================================================================
  // 5. ⛔ SIGN-OUT CLEARS THE QUEUE. The row says so, and the screen says
  //    "anything not committed by sign-out is lost". Both are claims about a
  //    SHARED MACHINE: an un-posted voucher readable by the next person to sign
  //    in on this profile is somebody else's books left on the desk.
  // =========================================================================
  await keyJournal(V4);
  await toOutbox();
  rec("a fourth voucher is queued, ready to be abandoned by a sign-out", (await rows()) === 1);

  await page.getByRole("button", { name: /sign out/i }).click();
  await page.waitForTimeout(2500);
  rec(
    "control: the sign-out really happened (the login screen is up)",
    (await page.getByTestId("login-screen").count()) > 0,
    (await body()).replace(/\s+/g, " ").slice(0, 120),
  );

  // Sign back in on the SAME browser profile — the shared-machine scenario.
  await page.keyboard.type("browser-dev-owner");
  await page.keyboard.press("Tab");
  await page.waitForTimeout(100);
  await page.keyboard.type("tally-demo-2026");
  await page.keyboard.press("Enter");
  for (let i = 0; i < 60; i++) {
    if ((await page.getByTestId("login-screen").count()) === 0) break;
    await page.waitForTimeout(250);
  }
  await page.waitForTimeout(1500);
  await toOutbox();
  rec(
    "⛔ the queue is EMPTY after a sign-out — an un-posted voucher does not survive for the next operator",
    (await seen("offline-outbox-empty")) && (await rows()) === 0,
    `${await rows()} row(s); empty-state shown: ${await seen("offline-outbox-empty")}`,
  );
  rec(
    "⛔ and the abandoned voucher reached no books either — lost is lost, not silently posted",
    !(await sql(`SELECT count(*) FROM voucher_version WHERE company_id = ${q(CO)} AND payload->>'narration' LIKE '%${V4}%'`)).startsWith("1"),
    "the screen says it is lost; a silent post would be a different and worse answer",
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
    console.log(`\ntorn down ${COMPANY_NAME}: ${left === "0" ? "clean" : `LEAKED (${left} rows)`}`);
  } catch (e) {
    console.log(`\n⚠️ teardown FAILED for ${CO}: ${e}`);
  }
  console.log("\n--- non-GET browser requests (status last) ---");
  for (const r of app.net) console.log("  " + r);
  if (driveError) console.log("\n--- the throw ---\n" + String(driveError).slice(0, 1200));
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);
