/**
 * T8 — THE CURRENCY MASTER, driven in a real browser.
 *
 * ⛔ WHAT THIS PROVES THAT NOTHING ELSE IN THE REPOSITORY CAN:
 *
 *   1. ⛔⛔ THE ROUTE IS ACTUALLY MAPPED. `routeAction` DEFAULT-DENIES an
 *      unmapped {companyID} route — the Owner gets 403 "unmapped route: access
 *      denied" — so a new route with no isMasterPath entry compiles, passes every
 *      Go test (they call the SERVICE, not the route) and is 403 for everybody.
 *      Only an HTTP call through the real middleware can tell.
 *
 *   2. ⛔⛔ THE MasterKind REACHES A FORM FROM THE KEYBOARD. Register row 20 was
 *      exactly this defect for five kinds at once: a master form that exists and
 *      cannot be opened. `masterGoto.test.ts` guards the MAP; only the browser
 *      proves the door.
 *
 *   3. ⛔ THE BASE-CURRENCY REFUSAL IS RENDERED, NOT JUST RAISED. The refusal
 *      starts as a plpgsql trigger, is translated in masters/currency.go, and has
 *      to survive two more layers to reach a human. Every layer is a place it can
 *      become "422 Unprocessable Entity".
 *
 *   4. ⛔ THE RATE KEEPS TEN DECIMALS THROUGH JSON. It is NUMERIC(19,10) in the
 *      database and a STRING on the wire precisely so nothing rounds in transit;
 *      a client that made it a JSON number would lose the digits that matter and
 *      every derived figure would stay plausible.
 *
 * ⛔ THE ORACLE IS POSTGRES, NOT THE PAGE. A screen that says "Created currency"
 * without writing a row looks identical from the operator's chair.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY AND TEARS IT DOWN with the catalog-derived
 * sweep. Residue drops `verify:demo` below 34/34, and a hand-rolled
 * `DELETE FROM company` strands ACL rows and breaks /v1/me for every session.
 *
 * RUN:  node frontend/scripts/drive-t8-currency.mjs
 *       (needs cored :8087 and vite :5173 — the port matters, WebAuthn origin
 *        matching is exact. It creates its own company.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Currency Drive ${stamp}`;

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
    `drive-t8: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
const base = `/v1/companies/${CO}`;

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, toGateway, altMac } = app;

const text = async (tid) =>
  (await page.locator(`[data-testid="${tid}"]`).first().innerText().catch(() => "")).trim();
const statusLine = () => text("status-line");
const seen = async (tid) => (await page.locator(`[data-testid="${tid}"]`).count()) > 0;
const type = async (tid, v) => {
  const el = page.locator(`[data-testid="${tid}"]`).first();
  await el.click();
  await el.fill(v);
};
const goTo = async (label) => {
  await toGateway();
  await altMac("g");
  await page.keyboard.type(label);
  await page.waitForTimeout(300);
  await press("Enter", 900);
};

try {

// ===========================================================================
// 1. THE ROUTE IS MAPPED — over HTTP, through the real middleware.
// ===========================================================================
console.log(`\n── the wire ──`);
{
  const r = await j("POST", `${base}/currencies`, {
    token: TOK,
    body: {
      code: "USD", symbol: "$", formal_name: "US Dollars",
      decimal_places: 2, created_by: "drive-t8",
    },
  });
  rec("⛔ POST /currencies is MAPPED (not 403 'unmapped route: access denied')",
      r.status === 201, `${r.status} ${JSON.stringify(r.body).slice(0, 120)}`);
}

// ⛔⛔ THE BASE CURRENCY IS REFUSED, AND THE SENTENCE SURVIVES THE TRIP.
{
  const r = await j("POST", `${base}/currencies`, {
    token: TOK,
    body: {
      code: "INR", symbol: "₹", formal_name: "Indian Rupees",
      decimal_places: 2, created_by: "drive-t8",
    },
  });
  const msg = String(r.body?.error ?? "");
  rec("⛔⛔ the company's OWN base currency is refused, 422",
      r.status === 422, `${r.status} ${msg.slice(0, 90)}`);
  rec("…and the refusal explains itself in words an operator can act on",
      /base currency/i.test(msg) && /already kept in it/i.test(msg), msg.slice(0, 140));
}

// ⛔ THE RATE KEEPS TEN DECIMALS THROUGH JSON.
{
  const RATE = "83.6512345678";
  const set = await j("POST", `${base}/currencies/rates`, {
    token: TOK,
    body: { code: "USD", effective_date: "2026-08-27", rate: RATE, created_by: "drive-t8" },
  });
  rec("POST /currencies/rates records a rate", set.status === 201, String(set.status));

  const list = await j("GET", `${base}/currencies`, { token: TOK });
  const usd = (list.body?.currencies ?? []).find((c) => c.code === "USD");
  rec("⛔ the rate survives JSON with ALL TEN decimals (a number would not)",
      usd?.latest_rate === RATE, `latest_rate=${String(usd?.latest_rate)} want ${RATE}`);
  rec("…and it is a STRING on the wire, never a JSON number",
      typeof usd?.latest_rate === "string", typeof usd?.latest_rate);

  // The database is the oracle, not the response.
  const stored = await sql(
    `select rate::text from currency_rate cr join currency c on c.id=cr.currency_id
      where cr.company_id=${q(CO)} and c.code='USD'`);
  rec("…and POSTGRES holds exactly that, to the tenth decimal", stored === RATE, stored);
}

// ⛔ A currency with NO rate reports an EMPTY rate, not "0".
{
  await j("POST", `${base}/currencies`, {
    token: TOK,
    body: { code: "AED", symbol: "د.إ", formal_name: "UAE Dirham", decimal_places: 2 },
  });
  const list = await j("GET", `${base}/currencies`, { token: TOK });
  const aed = (list.body?.currencies ?? []).find((c) => c.code === "AED");
  rec("⛔ a currency with no rate yet reports \"\" — NOT \"0\", which would value it at nothing",
      aed !== undefined && aed.latest_rate === "",
      `present=${String(aed !== undefined)} latest_rate=${JSON.stringify(aed?.latest_rate)}`);
}

// ===========================================================================
// 2. THE KEYBOARD DOOR — register row 20's defect class.
// ===========================================================================
console.log(`\n── the keyboard ──`);
const CODE = `Z${stamp.slice(0, 2).toUpperCase()}`;

await goTo("Create Currency");
rec("⛔⛔ Alt+G > Create Currency reaches masterKind \"currency\"",
    await seen("master-create:currency"), (await body()).split("\n")[0]);

{
  await type("master-name", CODE);
  // A currency has no second field, so the cursor advances straight to details.
  await press("Tab", 300);
  rec("the currency's DETAILS cursor lands on a real control (the symbol)",
      await page.evaluate(() =>
        document.activeElement?.getAttribute("data-testid") === "master-currency-symbol"),
      await page.evaluate(() => document.activeElement?.getAttribute("data-testid") ?? "(none)"));

  await type("master-currency-symbol", "¤");
  await type("master-currency-formal-name", `Drive Dollars ${stamp}`);
  await press("Control+a", 1500);
  rec(`the currency "${CODE}" is created and the status line says so`,
      /Created currency/i.test(await statusLine()), await statusLine());
}

rec("⛔ …and it is REALLY IN THE BOOKS (Postgres, not the page)",
    (await sql(`select count(*) from currency where company_id=${q(CO)} and code=${q(CODE)}`)) === "1");

rec("…with the symbol and decimals as keyed",
    (await sql(`select symbol||'|'||decimal_places from currency
                 where company_id=${q(CO)} and code=${q(CODE)}`)) === "¤|2");

// ⛔⛔ THE REFUSAL IS RENDERED ON THE SCREEN, not merely raised on the server.
await goTo("Create Currency");
{
  await type("master-name", "INR");
  await press("Tab", 300);
  await type("master-currency-symbol", "₹");
  await type("master-currency-formal-name", "Indian Rupees");
  await press("Control+a", 1500);
  const line = await statusLine();
  const page_ = await body();
  rec("⛔⛔ keying the BASE currency is refused ON SCREEN, in words",
      /base currency/i.test(line + page_), line.slice(0, 160));
  rec("…and NOTHING was created — the refusal is not cosmetic",
      (await sql(`select count(*) from currency where company_id=${q(CO)} and lower(code)='inr'`)) === "0");
}

} catch (driveError) {
  rec("the drive ran to the end without throwing", false, String(driveError).slice(0, 400));
}

await app.browser.close();

// ---- teardown: the CATALOG-DERIVED sweep -------------------------------------
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
  const cur = await sql(`SELECT count(*) FROM currency WHERE company_id = ${q(CO)}`);
  console.log(
    `\ntorn down ${COMPANY_NAME}: ${left === "0" && cur === "0" ? "clean" : `LEAKED (company=${left} currency=${cur})`}`,
  );
} catch (e) {
  console.log(`\n⚠️ teardown FAILED for ${CO}: ${e}`);
}

process.exit(summary());
