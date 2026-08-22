/**
 * L15d — D87: a keyed party had no GSTIN, no state code and no registration type.
 *
 * ⛔⛔ TWO DOORS, AND BOTH ARE DRIVEN. `MasterCreateScreen` is reached from
 * `Alt+G > Create Ledger` AND from `Alt+C` on a voucher's party field. The
 * second is the one that matters most for this row: it is the D19 path — a
 * party created inline, mid-invoice, is exactly the party whose missing state
 * code made an intra-state bill derive as IGST — and it is also the door where a
 * NEW refusal is most disruptive, because the operator is in the middle of a
 * voucher. A probe that drove only the masters screen would say nothing about it.
 *
 * ⛔ THE ORACLE IS THE BOOKS, THROUGH A SURFACE THE FORM DOES NOT OWN. The
 * status line saying "Created ledger" looks identical whether the five fields
 * persisted or were dropped — which is precisely what `credit_days` did on this
 * very route until this lane (accepted at 201, written nowhere). So every field
 * is read back out of Postgres AND through `GET /ledgers`, and the two must
 * agree.
 *
 * ⛔ THE REFUSAL'S ORACLE IS ZERO NETWORK REQUESTS, NOT A MESSAGE. The server
 * answers 201 to a party with no state code — `masters.CreateLedger` creates it
 * and then has nothing left to refuse with — so a refusal that let the request
 * go would be refusing a party that already exists. The probe counts the
 * `POST /ledgers` calls on the wire.
 *
 * ⛔ IT REAPS EVERY MASTER IT CREATES (D91). A party left behind has no state
 * code by definition on the refusal arms, and fails `verify:demo`'s party checks
 * by construction. The reap refuses to run on a ledger that has postings.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15d-d87.mjs
 */
import { execFileSync } from "node:child_process";

import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO;
const CORED = "http://localhost:8087";
if (!CO) throw new Error("drive-l15d-d87: CO is required (the seeded company id)");

const stamp = Math.random().toString(36).slice(2, 7);
const PARTY_GOTO = `D87 Goto Party ${stamp}`;
const PARTY_INLINE = `D87 Inline Party ${stamp}`;
const PARTY_REFUSED = `D87 Refused Party ${stamp}`;
const created = [PARTY_GOTO, PARTY_INLINE, PARTY_REFUSED];

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
  try { parsed = text ? JSON.parse(text) : {}; } catch { parsed = { raw: text }; }
  return { status: r.status, body: parsed };
};
const TOK = (await j("POST", "/auth/token", {
  body: { subject: "browser-dev-owner", amr: ["webauthn"] },
})).body.token;

const psql = (sql) =>
  execFileSync("psql", [
    "-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql,
  ], { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" }).trim();
const q = (s) => `'${String(s).replace(/'/g, "''")}'`;

/**
 * The party block straight out of Postgres. ⛔ IT THROWS ON A MISSING ROW rather
 * than returning blanks — a party that was never created must not read as a
 * party with empty fields, which is the difference between "the form dropped
 * the values" and "the form refused". §4.6, three times over this session.
 */
const rowOf = (name) => {
  const raw = psql(
    `SELECT coalesce(gstin,'<null>')||'|'||coalesce(state_code,'<null>')||'|'||
            registration_type::text||'|'||coalesce(pan,'<null>')||'|'||
            coalesce(credit_days::text,'<null>')
       FROM ledger WHERE company_id=${q(CO)} AND name=${q(name)}`,
  );
  if (raw === "") throw new Error(`no ledger named ${JSON.stringify(name)} — it was never created`);
  const [gstin, stateCode, regType, pan, creditDays] = raw.split("|");
  return { gstin, stateCode, regType, pan, creditDays };
};
const ledgerExists = (name) =>
  psql(`SELECT count(*) FROM ledger WHERE company_id=${q(CO)} AND name=${q(name)}`) !== "0";

const app = await openApp({ company: CO, headless: true });
const { page, body, press, altMac, toGateway } = app;

const statusLine = async () =>
  (await page.getByTestId("status-line").innerText().catch(() => "")).replace(/\n/g, " ");
const setField = async (testId, value) => {
  const el = page.getByTestId(testId);
  if ((await el.count()) === 0) throw new Error(`${testId} is not on the page`);
  await el.fill(value);
  await page.waitForTimeout(120);
};
const setSelect = async (testId, value) => {
  const el = page.getByTestId(testId);
  if ((await el.count()) === 0) throw new Error(`${testId} is not on the page`);
  await el.selectOption(value);
  await page.waitForTimeout(120);
};
const pickGroup = async (group) => {
  const picker = page.getByRole("combobox").first();
  await picker.click();
  await picker.fill(group);
  await page.waitForTimeout(250);
  await press("Enter", 300);
};

/** Count POST /ledgers calls across a block — the refusal's real oracle. */
let posts = 0;
page.on("request", (r) => {
  if (r.method() === "POST" && /\/ledgers$/.test(new URL(r.url()).pathname)) posts += 1;
});

try {
  // =========================================================================
  // DOOR 1 — Alt+G > Create Ledger (the masters screen)
  // =========================================================================
  await toGateway();
  await altMac("g");
  await page.waitForTimeout(300);
  await page.keyboard.type("Create Ledger");
  await page.waitForTimeout(400);
  await press("Enter", 1500);

  rec(
    "D87: the ledger form RENDERS the party block at all",
    (await page.getByTestId("master-party-block").count()) === 1,
    "before this row it rendered none of the five fields",
  );

  // ---- the REFUSAL, on the common path. ----------------------------------
  const before = posts;
  await setField("master-name", PARTY_REFUSED);
  await pickGroup("Sundry Debtors");
  // No state code.
  await press("Control+a", 1500);

  const refusalMsg = await statusLine();
  rec(
    "⛔ D87 ORACLE: a party with no state code is REFUSED, naming the reason",
    /needs a STATE CODE/i.test(refusalMsg),
    refusalMsg.slice(0, 160),
  );
  rec(
    "⛔⛔ D87 ORACLE: and NOTHING WAS SENT — zero POST /ledgers",
    posts === before,
    `${posts - before} POST(s). The server answers 201 to this body, so a refusal ` +
      `that let the request go would be refusing a party that already exists.`,
  );
  rec(
    "…and no such ledger exists in the books",
    !ledgerExists(PARTY_REFUSED),
    `${PARTY_REFUSED} must not be in the ledger table`,
  );
  // D92, on the door that made it matter.
  rec(
    "D92 holds here: the form is still up and the keying survived the refusal",
    (await page.getByTestId("master-name").inputValue()) === PARTY_REFUSED,
    `master-name = ${JSON.stringify(await page.getByTestId("master-name").inputValue())}`,
  );

  // ---- §4.4 THE REMEDY. The refusal names a missing state code; supply it. --
  await setSelect("master-state-code", "27");
  await setSelect("master-registration-type", "Regular");
  await setField("master-gstin", "27AACCM9012M1Z7");
  await setField("master-pan", "AACCM9012M");
  await setField("master-credit-days", "45");
  // Rename so this run's fixture is the one the oracle reads.
  await setField("master-name", PARTY_GOTO);
  await press("Control+a", 2500);

  const okMsg = await statusLine();
  rec(
    "§4.4: the remedy the refusal invites works from the SAME frame",
    /Created ledger/i.test(okMsg),
    okMsg.slice(0, 140),
  );

  // ⛔ THE BOOKS. Not the message.
  const goto = rowOf(PARTY_GOTO);
  rec(
    "⛔⛔ D87 ORACLE: all FIVE fields reached the ledger row",
    goto.gstin === "27AACCM9012M1Z7" &&
      goto.stateCode === "27" &&
      goto.regType === "REGULAR" &&
      goto.pan === "AACCM9012M" &&
      goto.creditDays === "45",
    `gstin=${goto.gstin} state=${goto.stateCode} type=${goto.regType} pan=${goto.pan} credit=${goto.creditDays} ` +
      `(before this row a keyed party read <null>|<null>|UNKNOWN)`,
  );

  // ...and through the PRODUCT's own reader, which had to be extended for pan
  // and credit_days — they were writable and not readable.
  const listed = (await j("GET", `/v1/companies/${CO}/ledgers`, { token: TOK })).body.ledgers
    .find((l) => l.name === PARTY_GOTO);
  if (!listed) throw new Error(`GET /ledgers does not carry ${PARTY_GOTO}`);
  rec(
    "⛔ D87 ORACLE: and GET /ledgers reads all five back",
    listed.gstin === "27AACCM9012M1Z7" &&
      listed.state_code === "27" &&
      listed.registration_type === "REGULAR" &&
      listed.pan === "AACCM9012M" &&
      listed.credit_days === 45,
    `pan=${JSON.stringify(listed.pan)} credit_days=${JSON.stringify(listed.credit_days)} — ` +
      `both were WRITABLE and NOT READABLE before this lane`,
  );

  // =========================================================================
  // DOOR 2 — Alt+C from a voucher's party field (the D19 path)
  // =========================================================================
  await toGateway();
  await press("F8", 1200); // Sales
  rec("a voucher screen is open to create from", (await body()).includes("Sales"));
  await altMac("c");
  await page.waitForTimeout(700);

  rec(
    "⛔ D87: the INLINE Alt+C door renders the party block too",
    (await page.getByTestId("master-party-block").count()) === 1,
    "the recon calls this the D19 path — a party created mid-invoice",
  );

  const beforeInline = posts;
  await setField("master-name", PARTY_INLINE);
  await pickGroup("Sundry Creditors");
  await press("Control+a", 1500);
  rec(
    "⛔ D87 ORACLE (inline): the refusal fires on THIS door too, and sends nothing",
    /needs a STATE CODE/i.test(await statusLine()) && posts === beforeInline,
    `${posts - beforeInline} POST(s) — ${(await statusLine()).slice(0, 120)}`,
  );

  await setSelect("master-state-code", "29");
  await setField("master-credit-days", "30");
  await press("Control+a", 2500);
  const inline = rowOf(PARTY_INLINE);
  rec(
    "⛔⛔ D87 ORACLE (inline): the party lands with a real state code and term",
    inline.stateCode === "29" && inline.creditDays === "30" && inline.regType === "UNREGISTERED",
    `state=${inline.stateCode} credit=${inline.creditDays} type=${inline.regType} — ` +
      `UNREGISTERED is a STATEMENT; UNKNOWN (the old landing value) is the absence of one`,
  );
} finally {
  await app.browser.close();

  // ---- THE REAP (D91). Asserted, and refused on anything posted to. --------
  for (const name of created) {
    const posted = psql(
      `SELECT count(*) FROM posting p JOIN ledger l ON l.id = p.ledger_id
        WHERE l.company_id = ${q(CO)} AND l.name = ${q(name)}`,
    );
    if (posted !== "0") {
      throw new Error(`refusing to reap ${JSON.stringify(name)}: it has ${posted} posting(s)`);
    }
    psql(`DELETE FROM ledger WHERE company_id = ${q(CO)} AND name = ${q(name)}`);
  }
  const left = created.filter(ledgerExists);
  rec(
    "cleanup (D91): every party this probe created is gone from the demo tenant",
    left.length === 0,
    left.length === 0 ? "0 left" : `still there: ${left.join(", ")}`,
  );
}

summary();
