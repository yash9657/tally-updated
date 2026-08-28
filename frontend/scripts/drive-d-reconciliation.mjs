/**
 * TRACK D — THE RECONCILIATION QUEUE, driven in a real browser.
 *
 * ⛔ WHAT THIS PROVES THAT THE VITEST SUITE CANNOT. Every frontend test runs
 * against a stub client: the entries, the parked payload and the live voucher are
 * all objects this repository wrote. Here the CONFLICT is REAL — produced by
 * committing a stale `base_version` through the actual cloud sequencer — and the
 * two panes are filled by the actual server, so a screen that renders a shape the
 * server never produces fails here and passes there.
 *
 * ⛔⛔ THE CONFLICT IS NOT STUBBED, WHICH IS THE HANDOFF'S EXPLICIT REQUIREMENT
 * FOR THIS TRACK (§3.2). Three commits through POST /sync/commit: a CREATE (head
 * v1), a well-based ALTER (head v2), then an ALTER still based on v1. The
 * sequencer refuses the third and parks it, exactly as it would for an operator
 * whose laptop was closed while a colleague edited the same voucher.
 *
 * THE ORACLES, each chosen so a wrong answer FAILS rather than looks plausible:
 *
 *   1. ⛔ ALT+R AND THE GATEWAY DOOR BOTH LAND ON THE SCREEN. "Reconciliation" is
 *      NOT a report name, and `canonicalReportName` falls back to the raw label —
 *      so a missing navMachine match pushes a `report` frame reading "not
 *      available in this build" over a screen that works. From the operator's
 *      chair that is indistinguishable from the feature not existing. The trap is
 *      sharper here than for track C's label because a bank-reconciliation report
 *      really does exist.
 *
 *   2. ⛔⛔ THE PARKED PAYLOAD REACHES THE PANE. The keyed narration and the
 *      provisional number are on the page. Until this track, the ONLY caller of
 *      the payload reader was a Go fuzz test — so "the row exists" proves nothing
 *      an operator would notice.
 *
 *   3. ⛔⛔ THE TWO PANES SPELL MONEY THE SAME WAY. The stored JSONB renders the
 *      amount as "1180" and the live voucher as "1180.0000"; a compare screen
 *      showing both would invite an operator to discard their work over a
 *      difference that does not exist. Asserted on the REAL bytes from the REAL
 *      server, which is the only place the stored form and the rendered form can
 *      actually disagree.
 *
 *   4. ⛔⛔ RESOLVING EMPTIES THE QUEUE IN POSTGRES. Not "the row left the page" —
 *      the DATABASE. `resolved_at` had NO writer anywhere in this codebase before
 *      this track, so a resolve that silently did nothing would look identical
 *      from the browser.
 *
 *   5. ⛔ A FROZEN ENTRY OFFERS NO RETRY. Asserted as a PROPERTY over every button
 *      on the page, so a helpfully-worded addition later cannot slip past.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY OF ITS OWN, NOT ON Demo Traders, AND TEARS IT
 * DOWN with the catalog-derived sweep. Residue drops `verify:demo` below 34/34,
 * and a hand-rolled `DELETE FROM company` breaks /v1/me for every session
 * (HANDOFF §0.1 item 7).
 *
 * RUN:  node frontend/scripts/drive-d-reconciliation.mjs
 *       (needs cored :8087 and vite :5173 — the port matters, WebAuthn origin
 *        matching is exact. It creates its own company.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Reconciliation Drive ${stamp}`;

/**
 * ⚠️ SINGLE TOKENS WITH NO SPACES (HANDOFF §0.1 item 6). Not for PDF wrapping
 * here, but for the same reason in a DOM: the narration is rendered inside a <dd>
 * beside other text, and asserting a whole sentence against `body()` fails on
 * whitespace collapsing rather than on anything real.
 */
const KEYED_NARRATION = "ZZDRIVEKEYEDOFFLINE";
const WINNER_NARRATION = "ZZDRIVEWINNINGVERSION";

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
// The tenant, two ledgers, and a REAL parked conflict — all over HTTP. Keying
// the voucher through the grid would make a queue failure indistinguishable from
// a voucher-entry failure.
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
    `drive-d: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
const base = `/v1/companies/${CO}`;

const mkLedger = async (name, group) => {
  // ⚠️ `group_name`, NOT `group` — the route answers 422 "ledger group is
  // required" for the latter, which reads like a missing value rather than a
  // wrong field name. drive-c-print-templates.mjs is the copy-me.
  const r = await j("POST", `${base}/ledgers`, {
    token: TOK,
    body: { name, group_name: group, created_by: "drive-d" },
  });
  if (r.status !== 201 && r.status !== 200) {
    throw new Error(`drive-d: ledger ${name}: ${r.status} ${JSON.stringify(r.body)}`);
  }
  return r.body.ledger_id ?? r.body.id;
};
const DR = await mkLedger("Drive Dr", "Current Assets");
const CR = await mkLedger("Drive Cr", "Current Liabilities");

const CVID = crypto.randomUUID();
const intent = (key, op, baseVersion, wall, amount, narration) => ({
  idempotency_key: key,
  client_voucher_id: CVID,
  op,
  class: "Journal",
  node_id: "driveNodeA",
  provisional_no: `INV/2026-27/PROV-driveNodeA-${key}`,
  hlc: { wall_millis: wall, counter: 0, node_id: "driveNodeA" },
  base_version: baseVersion,
  payload: {
    voucher_date: "2026-05-20",
    narration,
    created_by: "driveNodeA",
    postings: [
      { ledger_id: DR, dr_cr: "D", amount },
      { ledger_id: CR, dr_cr: "C", amount },
    ],
  },
});

const commit = async (body) => {
  const r = await j("POST", `${base}/sync/commit`, { token: TOK, body: { intents: [body] } });
  if (r.status !== 200) {
    throw new Error(`drive-d: sync/commit ${r.status} ${JSON.stringify(r.body)}`);
  }
  return r.body.outcomes[0];
};

const oCreate = await commit(intent("c1", "CREATE", 0, 100, "1180.0000", "first version"));
rec(
  "a CREATE commits through the real cloud sequencer",
  oCreate.status === "COMMITTED" && !!oCreate.final_voucher_no,
  `${oCreate.status} no=${oCreate.final_voucher_no}`,
);

const oWin = await commit(intent("a1", "ALTER", 1, 200, "2360.0000", WINNER_NARRATION));
rec(
  "a well-based ALTER moves the head to version 2",
  oWin.status === "COMMITTED" && oWin.version_no === 2,
  `${oWin.status} v=${oWin.version_no}`,
);

// ⛔ THE REAL CONFLICT. Still based on version 1, which the commit above moved
// past. Nothing here is stubbed and no status name is invented.
const oLose = await commit(intent("a2", "ALTER", 1, 300, "1180.0000", KEYED_NARRATION));
rec(
  "a stale-base ALTER is REFUSED and parked, with a reconciliation id",
  oLose.status === "CONFLICT" && !!oLose.reconciliation_id,
  `${oLose.status} recon=${oLose.reconciliation_id}`,
);
const RECON = oLose.reconciliation_id;

// The stored form of the money, straight from Postgres. This is the value the
// compare pane must NOT show, and it is why the server re-formats.
const storedAmount = await sql(
  `SELECT payload->'Payload'->'Postings'->0->>'Amount'
     FROM reconciliation_queue WHERE id = ${q(RECON)}`,
);
rec(
  "the PARKED payload stores money bare, which is what makes re-formatting necessary",
  storedAmount === "1180",
  `reconciliation_queue.payload …Amount = ${JSON.stringify(storedAmount)} (a pane showing this beside the live "1180.0000" would invent a difference)`,
);

// ---------------------------------------------------------------------------
const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, toGateway, altMac, openFromGateway } = app;

// ===========================================================================
// ⛔ EVERY PROBE BELOW RUNS INSIDE ONE try, SO A THROW STILL REACHES THE SWEEP.
//
// Added 2026-08-27 by the U1-U7 audit lane, and NOT speculatively: adding two
// assertions to this file threw on a Postgres precedence error (`a || b->>'k'`
// parses as `(a || b) ->> 'k'`) and the drive died between `openApp` and the
// teardown TWICE, leaking a tenant each time. That is `drive-d-offline-queue`'s
// lesson exactly, and this file did not have the guard.
//
// ⚠️ THE BLOCK IS DELIBERATELY NOT RE-INDENTED, for the reason that file gives:
// indenting 290 lines to add a guard buries the change that matters in a
// whitespace diff.
// ===========================================================================
let driveError = null;
try {

/**
 * EVERY FAILED RESPONSE, WITH ITS URL **AND ITS BODY**.
 *
 * ⚠️ THE BODY IS NOT OPTIONAL HERE. A 403 on a write in this product is very
 * often STEPUP_REQUIRED — the passkey challenge the app answers and retries — and
 * a probe that treats the challenge as a failure reports a defect that is not
 * one (HANDOFF rule 5). Only the body can tell the two apart.
 */
const failedRequests = [];
page.on("response", async (r) => {
  if (r.status() < 400) return;
  let body = "";
  try {
    body = (await r.text()).slice(0, 200);
  } catch {
    body = "(unreadable)";
  }
  failedRequests.push({ status: r.status(), url: r.url(), body });
});

const btnTexts = async () =>
  await page.$$eval("button", (bs) => bs.map((b) => (b.textContent ?? "").trim()));

// ---- 1. THE TWO DOORS -------------------------------------------------------
await altMac("r", 1200);
let txt = await body();
rec(
  "Alt+R opens the reconciliation queue",
  /Reconciliation/i.test(txt) && !/not available in this build/i.test(txt),
  txt.slice(0, 160).replace(/\s+/g, " "),
);
rec(
  "⛔ Alt+R did NOT land on a report frame reading 'not available in this build'",
  !/not available in this build/i.test(txt),
  "the navMachine matched the screen, not canonicalReportName's fallback",
);

await toGateway();
await openFromGateway("Reconciliation Queue");
txt = await body();
rec(
  "Gateway of Tally > Reconciliation Queue opens the same screen",
  /Reconciliation Queue/i.test(txt) && !/not available in this build/i.test(txt),
  txt.slice(0, 160).replace(/\s+/g, " "),
);
/**
 * ⛔⛔ THE PROBE THAT CAUGHT THE COLLISION, AND THE ONE THE FIRST VERSION OF THIS
 * FILE GOT WRONG. The original assertion was /Reconciliation/i — which "Bank
 * Reconciliation" satisfies, so the door landing on the WRONG SHIPPED SCREEN
 * passed. Nothing errored; the operator just arrived at "this company has no bank
 * accounts", which reads like an empty queue. An oracle that cannot see the
 * defect is not an oracle (HANDOFF rule 4).
 */
rec(
  "⛔ and it is NOT the Bank Reconciliation screen, which contains the same word",
  !/no bank accounts|As on/i.test(txt),
  txt.slice(0, 200).replace(/\s+/g, " "),
);

// ---- 2. THE ENTRY IS LISTED -------------------------------------------------
rec(
  "the parked conflict is on the list, described in words rather than as a code",
  /changed this voucher first/i.test(txt),
  txt.slice(0, 300).replace(/\s+/g, " "),
);

// ---- 3. BOTH PANES ----------------------------------------------------------
const compare = page.getByRole("button", { name: /compare/i }).first();
await compare.click();
await page.waitForTimeout(1200);
txt = await body();

rec(
  "⛔ the KEYED version reaches the pane — the parked payload had no HTTP reader before this track",
  txt.includes(KEYED_NARRATION),
  `looking for ${KEYED_NARRATION}: ${txt.includes(KEYED_NARRATION)}`,
);
rec(
  "the LIVE version is shown beside it",
  txt.includes(WINNER_NARRATION),
  `looking for ${WINNER_NARRATION}: ${txt.includes(WINNER_NARRATION)}`,
);
rec(
  "the provisional number is shown and LABELLED provisional, not as a voucher number",
  /provisional/i.test(txt) && txt.includes("PROV-driveNodeA-a2"),
  "a PROV- label in the field a real number occupies is how one gets read to a customer",
);
rec(
  "the pane says which version the refused edit was based on",
  /based on version/i.test(txt),
  "the stale base is the whole explanation for the refusal",
);

// ⛔⛔ THE FORMATTING ORACLE, ON REAL BYTES.
rec(
  "⛔ both panes spell money the same way (fixed-4), so no false difference is shown",
  txt.includes("1180.0000") && txt.includes("2360.0000") && !/[^.\d]1180\b(?!\.)/.test(txt),
  `stored form is ${JSON.stringify(storedAmount)}; the page shows 1180.0000=${txt.includes("1180.0000")} 2360.0000=${txt.includes("2360.0000")}`,
);

// ---- 4. NO FORCE AFFORDANCE (a property over every button) ------------------
const forbidden = /\b(retry|re-?try|force|re-?apply|apply anyway|overwrite|re-?submit|push mine)\b/i;
const offending = (await btnTexts()).filter((t) => forbidden.test(t));
rec(
  "⛔ the screen offers NO way to force a stale version through",
  offending.length === 0,
  offending.length ? `offending buttons: ${JSON.stringify(offending)}` : "no retry/force/re-apply button anywhere",
);
rec(
  "and it says keeping your version means entering it again as a NEW voucher",
  /new/i.test(txt) && /superseded|refuse/i.test(txt),
  "a button that cannot work is worse than none: the operator stops re-keying",
);

// ---- 5. RESOLVE, AND THE DATABASE IS THE ORACLE ------------------------------
const openBefore = await sql(
  `SELECT count(*) FROM reconciliation_queue WHERE company_id = ${q(CO)} AND resolved_at IS NULL`,
);
rec("Postgres holds exactly one OPEN entry before the resolve", openBefore === "1", `open=${openBefore}`);

// ⛔⛔ THE BOOKS, PHOTOGRAPHED BEFORE THE RESOLVE. Added by the U1-U7 audit lane
// 2026-08-27 for the row's clause "CLEARING AN ENTRY RECORDS THAT SOMEBODY DEALT
// WITH IT AND RE-APPLIES NOTHING; the refused change stays refused."
//
// Everything below already checked that against the SCREEN ("does NOT claim to
// have re-applied anything") and against `resolved_at`. Neither can see the
// failure the clause is about: a resolve that ALSO replayed the parked intent
// would stamp `resolved_at`, would render the same sentence, and would leave the
// operator's books carrying an edit the sequencer refused — at version 3, with
// the losing amount, and nothing on any screen saying so. The oracle has to be
// the voucher.
const booksBefore = await sql(`
SELECT vh.version_no || '|' || (vv.payload->>'narration')
  FROM voucher_head vh
  JOIN voucher_version vv ON vv.id = vh.current_version_id
 WHERE vh.company_id = ${q(CO)}`);
rec(
  "control: the books are at the WINNING version before the resolve",
  booksBefore.startsWith("2|") && booksBefore.includes(WINNER_NARRATION),
  `head = ${booksBefore}`,
);

await page.getByRole("button", { name: /discard my version/i }).first().click();
await page.waitForTimeout(1500);
txt = await body();

const openAfter = await sql(
  `SELECT count(*) FROM reconciliation_queue WHERE company_id = ${q(CO)} AND resolved_at IS NULL`,
);
const stampedAfter = await sql(
  `SELECT count(*) FROM reconciliation_queue WHERE company_id = ${q(CO)} AND resolved_at IS NOT NULL`,
);
rec(
  "⛔⛔ resolving STAMPED resolved_at in Postgres — the column had no writer at all before this track",
  openAfter === "0" && stampedAfter === "1",
  `open=${openAfter} resolved=${stampedAfter}`,
);
rec(
  "the screen re-read the queue rather than splicing the row out of its own state",
  /nothing is waiting/i.test(txt),
  txt.slice(0, 200).replace(/\s+/g, " "),
);
rec(
  "⚠️ and it does NOT claim to have re-applied anything",
  /still refused/i.test(txt),
  "nothing anywhere replays a parked intent; saying otherwise promises what no route performs",
);

// ⛔⛔ AND THE BOOKS SAY THE SAME THING. This is the arm that can actually fail:
// the two above read the screen and a timestamp column.
const booksAfter = await sql(`
SELECT vh.version_no || '|' || (vv.payload->>'narration')
  FROM voucher_head vh
  JOIN voucher_version vv ON vv.id = vh.current_version_id
 WHERE vh.company_id = ${q(CO)}`);
rec(
  "⛔⛔ CLEARING RE-APPLIED NOTHING — the voucher is byte-for-byte where it was",
  booksAfter === booksBefore,
  `head before: ${booksBefore} · after: ${booksAfter}`,
);
rec(
  "⛔ and the KEYED version never reached the books at any version",
  !(await sql(
    `SELECT count(*) FROM voucher_version WHERE company_id = ${q(CO)} AND payload->>'narration' LIKE ${q("%" + KEYED_NARRATION + "%")}`,
  )).startsWith("1"),
  "a parked intent that got replayed on clear would be here, and only here would show it",
);

// ---- 6. A FROZEN ENTRY IS EXPLAINED, NOT RETRIED ----------------------------
// Pin an IRN on the live voucher, then try to delete it: the LEDGER raises the
// freeze row (sync.parkConflict is not involved, so it carries NO payload).
const vId = CVID;
const pin = await j("POST", `${base}/vouchers/${vId}/external-reference`, {
  token: TOK,
  body: { kind: "IRN", ref_value: "IRN-DRIVE-1", created_by: "drive" },
});
rec("an IRN pins the voucher (the shipped freeze seam)", pin.status === 201 || pin.status === 200, `${pin.status}`);

const del = await j("DELETE", `${base}/vouchers/${vId}`, { token: TOK });
rec(
  "deleting a frozen voucher is REFUSED, raising a freeze row",
  del.status >= 400,
  `DELETE answered ${del.status}`,
);

// ⚠️ THE FULL LABEL AGAIN. Passing the short "Reconciliation" here lands on Bank
// Reconciliation exactly as it did above — and the symptom is a missing "Explain"
// button, which reads as a broken screen rather than as a wrong door.
await toGateway();
await openFromGateway("Reconciliation Queue");
await page.waitForTimeout(800);

// ⛔ ASSERT THE ROW EXISTS IN POSTGRES BEFORE LOOKING FOR ITS BUTTON, so "the
// freeze rule raised nothing" and "the screen did not render the row" fail
// SEPARATELY. Without this, one message covers two very different defects.
const freezeRows = await sql(
  `SELECT count(*) FROM reconciliation_queue
    WHERE company_id = ${q(CO)} AND resolved_at IS NULL AND kind = 'IRN'`,
);
rec(
  "the ledger raised an IRN freeze row (kind='IRN', payload NULL — parkConflict is not involved)",
  freezeRows === "1",
  `open IRN rows = ${freezeRows}`,
);

await page.getByRole("button", { name: /explain/i }).first().click();
await page.waitForTimeout(1200);
txt = await body();

rec(
  "⛔ the freeze row NAMES what pinned the voucher, not merely that it is frozen",
  /e-invoiced/i.test(txt),
  "'frozen' without 'because an IRN is registered against it' sends an operator looking for a setting",
);
rec(
  "it names the way FORWARD — a credit note, not an edit",
  /credit note/i.test(txt),
  txt.slice(0, 240).replace(/\s+/g, " "),
);
rec(
  "⚠️ it says WHERE the keyed version is, because a freeze row never parked one",
  /only on the node that keyed it/i.test(txt),
  "has_payload=false is a real answer; an empty pane would read as a fault",
);
const frozenButtons = (await btnTexts()).filter((t) => forbidden.test(t));
rec(
  "⛔ and a FROZEN entry offers no retry either",
  frozenButtons.length === 0,
  frozenButtons.length ? JSON.stringify(frozenButtons) : "explained, not retried",
);

// ---- console / network hygiene ----------------------------------------------
console.log("\n--- failed requests ---");
for (const f of failedRequests) console.log(`  ${f.status} ${f.url}\n      body: ${f.body}`);

/**
 * ⛔ THE TWO ANSWERS THAT ARE EXPECTED, NAMED EXPLICITLY RATHER THAN TOLERATED AS
 * A COUNT. Being specific is what keeps this probe able to fail:
 *
 *   403 STEPUP_REQUIRED on the resolve — `reconciliation` is an Alter, so a level
 *     requiring WebAuthn is challenged; the app answers the ceremony and retries,
 *     and the retry is what stamped resolved_at (proved in Postgres above).
 *   404 on the payload of an entry that is NO LONGER OPEN — the screen re-reads
 *     after a resolve, and a row it had open a moment ago is legitimately gone.
 *     The contract is deliberately scoped to OPEN rows.
 *
 * Anything else is a real failure.
 */
const unexpected = failedRequests.filter((f) => {
  const stepUp = f.status === 403 && /STEPUP_REQUIRED|step-up/i.test(f.body);
  const staleEntry = f.status === 404 && /\/payload$/.test(f.url) && /no open reconciliation entry/i.test(f.body);
  return !stepUp && !staleEntry;
});
rec(
  "no request this screen made failed for a reason other than a passkey challenge or a resolved entry",
  unexpected.length === 0,
  unexpected.length
    ? unexpected.map((f) => `${f.status} ${f.url} :: ${f.body}`).join(" | ")
    : `${failedRequests.length} expected non-2xx (step-up challenge / stale entry), 0 unexplained`,
);
rec(
  "⚠️ the 403 on the resolve really is a STEP-UP CHALLENGE and not a denial",
  failedRequests.every((f) => f.status !== 403 || /STEPUP_REQUIRED|step-up/i.test(f.body)),
  failedRequests.filter((f) => f.status === 403).map((f) => f.body).join(" | ") || "no 403 at all",
);

} catch (e) {
  driveError = e;
  rec("the drive ran to the end without throwing", false, String(e).slice(0, 400));
}

await app.browser.close();
if (driveError) console.log("\n--- the throw ---\n" + String(driveError).slice(0, 1200));

// ---- teardown: the CATALOG-DERIVED sweep (HANDOFF §0.1 item 7) ---------------
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
  const recons = await sql(`SELECT count(*) FROM reconciliation_queue WHERE company_id = ${q(CO)}`);
  console.log(
    `\ntorn down ${COMPANY_NAME}: ${left === "0" && recons === "0" ? "clean" : `LEAKED (company=${left} reconciliation_queue=${recons})`}`,
  );
} catch (e) {
  console.log(`\n⚠️ teardown FAILED for ${CO}: ${e}`);
}

process.exit(summary());
