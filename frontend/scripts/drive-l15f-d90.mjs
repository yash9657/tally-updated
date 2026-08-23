/**
 * L15f — D90: master names collided BYTE-EXACTLY, so two masters an operator
 * cannot tell apart both landed.
 *
 * ⛔⛔ D90's PREMISE WAS WRONG IN BOTH HALVES AND ITS CONSEQUENCE WAS RIGHT.
 * The row said "a duplicate STOCK ITEM name is accepted while a duplicate LEDGER
 * name is refused". Re-measured on the wire before any code moved: BOTH are
 * refused, and `UNIQUE (company_id, name)` has been on `stock_item` since
 * migration 0005. The measurement that raised D90 (D84's) was simply wrong, and
 * the recon it "overturned" had been right all along.
 *
 * What was real is the mechanism nobody had measured: uniqueness was BYTE-EXACT,
 * so "Acme Traders", "acme traders" and "Acme Traders " were three masters —
 * indistinguishable in every picker — and that is exactly the damage D90
 * described. It applied to LEDGERS and UNITS too, and the ledger case is the
 * worst of the three: a party keyed twice with different capitalisation splits
 * one supplier's outstandings across two accounts.
 *
 * ⛔ THE ORACLE IS THE PICKER, NOT THE REFUSAL. A refusal message proves the
 * server said no; it does not prove the operator can no longer end up with two
 * indistinguishable rows. So this probe reads the ITEM PICKER and the LEDGER
 * count back, and requires ONE row where two creates were attempted.
 *
 * ⚠️ AND THE MESSAGE MUST NAME THE STORED MASTER, NOT THE ONE THE CALLER SENT.
 * An operator who types "acme traders" and is told "you already have a ledger
 * named 'acme traders'" can see that is false — there is no such row in the
 * picker. The first implementation did exactly that, because it read the stored
 * name AFTER the failed INSERT, inside a transaction Postgres had already
 * aborted.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15f-d90.mjs
 */
import { execFileSync } from "node:child_process";

import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO;
const CORED = "http://localhost:8087";
if (!CO) throw new Error("drive-l15f-d90: CO is required (the seeded company id)");

const stamp = Math.random().toString(36).slice(2, 7);
const PARTY = `D90 Party ${stamp}`;
const PARTY_LOWER = `  d90 party ${stamp} `;

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
  return { status: r.status, body: parsed, text };
};
const TOK = (await j("POST", "/auth/token", {
  body: { subject: "browser-dev-owner", amr: ["webauthn"] },
})).body.token;

const psql = (sql) =>
  execFileSync("psql", [
    "-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql,
  ], { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" }).trim();
const q = (s) => `'${String(s).replace(/'/g, "''")}'`;

const app = await openApp({ company: CO, headless: true });
const { page, press, altMac, toGateway } = app;

const statusLine = async () =>
  (await page.getByTestId("status-line").innerText().catch(() => "")).replace(/\n/g, " ");
const setField = async (testId, value) => {
  const el = page.getByTestId(testId);
  if ((await el.count()) === 0) throw new Error(`${testId} is not on the page`);
  await el.fill(value);
  await page.waitForTimeout(120);
};
const openCreateLedger = async () => {
  await toGateway();
  await altMac("g");
  await page.waitForTimeout(300);
  await page.keyboard.type("Create Ledger");
  await page.waitForTimeout(400);
  await press("Enter", 1500);
};
const partyRows = () =>
  Number(psql(`SELECT count(*) FROM ledger WHERE company_id=${q(CO)} AND lower(btrim(name)) = lower(btrim(${q(PARTY)}))`));

try {
  // ---- 1. Create the party from the keyboard. -----------------------------
  await openCreateLedger();
  await setField("master-name", PARTY);
  const picker = page.getByRole("combobox").first();
  await picker.click();
  await picker.fill("Sundry Debtors");
  await page.waitForTimeout(250);
  await press("Enter", 300);
  await page.getByTestId("master-state-code").selectOption("29");
  await page.waitForTimeout(150);
  await press("Control+a", 2500);
  rec(
    "the party is created from the keyboard",
    /Created ledger/i.test(await statusLine()) && partyRows() === 1,
    `${partyRows()} row(s)`,
  );

  // ---- 2. Key it AGAIN, differing only by case and surrounding space. -----
  // ⛔ THIS IS THE WHOLE ROW. Before migration 0075 this landed a SECOND party.
  await openCreateLedger();
  await setField("master-name", PARTY_LOWER);
  const picker2 = page.getByRole("combobox").first();
  await picker2.click();
  await picker2.fill("Sundry Debtors");
  await page.waitForTimeout(250);
  await press("Enter", 300);
  await page.getByTestId("master-state-code").selectOption("29");
  await page.waitForTimeout(150);
  await press("Control+a", 2500);

  const msg = await statusLine();
  rec(
    "⛔ D90 ORACLE: a name differing only by CASE and SPACE is REFUSED",
    /already has a ledger named/i.test(msg),
    msg.slice(0, 190),
  );
  rec(
    "⛔⛔ D90 ORACLE: and the refusal names the STORED master, not what was typed",
    msg.includes(PARTY) && /apart from capitalisation or surrounding spaces/.test(msg),
    `must quote ${JSON.stringify(PARTY)} — an operator told they already have ` +
      `${JSON.stringify(PARTY_LOWER.trim())} can see that is false`,
  );
  rec(
    "⛔⛔ D90 ORACLE: the BOOKS hold ONE party, not two",
    partyRows() === 1,
    `${partyRows()} row(s) under the normalised name — two would be ` +
      `indistinguishable in every picker and would split this supplier's outstandings`,
  );
  // ⚠️ D92 DOES **NOT** HOLD HERE, AND THAT IS A REAL AND KNOWN ASYMMETRY.
  // D92 made a CLIENT-SIDE `build()` refusal non-destructive. This refusal comes
  // from CORED, so `onAccept` returns a Promise, the machine has already popped
  // by the time the 422 arrives, and the operator is back on the Gateway with
  // everything they keyed gone. That is precisely why register row D47 exists —
  // it is the row that made the message survive the screen being unmounted.
  //
  // ⛔ SO THE PRODUCT NOW REFUSES IN TWO DIFFERENT WAYS: a missing state code
  // (client-side) keeps the form; a duplicate name (server-side) does not.
  // Recorded here rather than fixed: the server-refusal half is register row
  // D81's territory (it is the same seam — a server refusal that leaves the
  // operator holding nothing), and pulling it into D90 would mean two rows in
  // one commit with one oracle between them.
  const poppedToGateway = (await page.getByTestId("master-name").count()) === 0;
  rec(
    "⚠️ KNOWN (D81's seam): a SERVER refusal still pops the frame, unlike a client-side one",
    poppedToGateway,
    poppedToGateway
      ? "popped, as D47 documents — the message survives, the keying does not"
      : "the form stayed up: the server-refusal path now behaves like the client one, " +
        "which would mean D81's half has been closed and this note is stale",
  );

  // ---- 3. THE CONTROL. A genuinely different name still goes through. -----
  // ⛔ WITHOUT THIS, "refuse every second ledger" passes everything above.
  await openCreateLedger();
  await setField("master-name", `${PARTY} Two`);
  const picker3 = page.getByRole("combobox").first();
  await picker3.click();
  await picker3.fill("Sundry Debtors");
  await page.waitForTimeout(250);
  await press("Enter", 300);
  await page.getByTestId("master-state-code").selectOption("29");
  await page.waitForTimeout(150);
  await press("Control+a", 2500);
  rec(
    "⛔ CONTROL: a genuinely different name is still ACCEPTED",
    /Created ledger/i.test(await statusLine()),
    (await statusLine()).slice(0, 120),
  );
  // ⚠️ AND AN INNER DOUBLE SPACE IS STILL A DIFFERENT NAME, deliberately: it is
  // VISIBLE in a picker, unlike a trailing space or a capital letter.
  const inner = await j("POST", `/v1/companies/${CO}/ledgers`, {
    token: TOK,
    body: { name: PARTY.replace(" Party ", " Party  "), group_name: "Sundry Debtors", state_code: "29" },
  });
  rec(
    "⛔ CONTROL: an INNER double space is still a distinct name",
    inner.status === 201,
    `${inner.status} — btrim, not a whitespace fold: an inner double space is visible in a picker`,
  );
} finally {
  await app.browser.close();
  for (const name of [PARTY, `${PARTY} Two`, PARTY.replace(" Party ", " Party  ")]) {
    const posted = psql(
      `SELECT count(*) FROM posting p JOIN ledger l ON l.id = p.ledger_id
        WHERE l.company_id = ${q(CO)} AND lower(btrim(l.name)) = lower(btrim(${q(name)}))`,
    );
    if (posted !== "0") throw new Error(`refusing to reap ${JSON.stringify(name)}: ${posted} posting(s)`);
    psql(`DELETE FROM ledger WHERE company_id = ${q(CO)} AND lower(btrim(name)) = lower(btrim(${q(name)}))`);
  }
  rec(
    "cleanup (D91): every party this probe created is gone",
    partyRows() === 0,
    `${partyRows()} left`,
  );
}

summary();
