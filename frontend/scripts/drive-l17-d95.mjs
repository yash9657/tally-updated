/**
 * L17 — D95: the forecast plane had no ACL counterweight, so the clerk could read
 * the control surface that watches them.
 *
 * ⛔⛔ THIS PROBE MAKES ITS OWN TENANT AND REAPS IT (the D91 pattern). It does NOT
 * touch the demo tenant, for two reasons: the demo tenant has NO Data Entry member
 * (measured — `browser-dev-owner`/Owner is its only membership), and provisioning
 * one there would leave a user and a membership behind that `verify:demo` does not
 * tear down. A probe that needs a second identity should bring its own books.
 *
 * ⛔ THE TWO ARMS THAT MATTER ARE THE **DISPLAY** ARMS, AND THE PRINT ARMS ARE
 * DELIBERATELY LABELLED AS PRE-EXISTING. Data Entry was ALREADY 403 on both print
 * forms before migration 0076: 0038 seeds no `allow print report:`, 0011's coarse
 * grant is action 'display' only, and the level's default_access is 'none'. So the
 * print arms pass with or without 0076 and prove nothing about it on their own.
 * Reporting "4/4 green" without saying so would be half a lie — see §5.19: a
 * refusal can be fully present and prove nothing.
 *
 * ⛔ AND THE OWNER'S PRINT ANSWER IS **501**, NOT 200. Printing these two is
 * unimplemented. The driver originally said "an Owner still reads all four", which
 * is wrong and would have failed on correct code. The Owner arm asserts 501 because
 * the Owner's refusal is a MISSING LAYOUT and the clerk's is a POLICY DECISION, and
 * a probe that blurred them would hide the day one became the other.
 *
 * ⚠️ THE CONTROL ARM CARRIES MORE WEIGHT THAN THE REFUSALS. An invite that is never
 * claimed via GET /v1/me yields a token that is 403 "not a member" on EVERY company
 * route — which satisfies every refusal assertion below while proving nothing. That
 * mistake has been made in this repo before. `Data Entry reads the Trial Balance`
 * is what disproves it.
 *
 * RUN:  node frontend/scripts/drive-l17-d95.mjs        (takes NO CO — it makes one)
 */
import { execFileSync } from "node:child_process";

import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://localhost:8087";

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
const mint = async (subject) =>
  (await j("POST", "/auth/token", { body: { subject, amr: ["webauthn"] } })).body.token;

const psql = (sql) =>
  execFileSync("psql", [
    "-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql,
  ], { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" }).trim();

// A stamp so a re-run never collides. The invite POST answers 409 for a subject
// that is already a member, and a fixed name would make the second run fail in a
// way that looks like a defect.
const stamp = Math.random().toString(36).slice(2, 8);
const CO = crypto.randomUUID();
const CLERK = `d95-clerk-${stamp}`;
const CLERK_PW = "d95-clerk-pw-2026";
const COMPANY_NAME = `D95 Probe Co ${stamp}`;

const OWNER = await mint(`d95-owner-${stamp}`);

// ---- its own tenant, through the product's own route so the SEEDER runs.
{
  const r = await j("POST", "/v1/companies", {
    token: OWNER,
    body: {
      id: CO, name: COMPANY_NAME, fy_start_date: "2026-04-01",
      base_currency: "INR", state_code: "29",
    },
  });
  if (r.status !== 201) throw new Error(`create company: ${r.status} ${r.text}`);
}

// ---- a REAL Data Entry member: mint -> set a first password -> invite -> CLAIM.
const CLERK_TOK = await (async () => {
  const tok = await mint(CLERK);
  // There is no production route that sets a FIRST password; this dev one exists
  // so a probe can sign the browser in as this subject later.
  const pw = await j("POST", "/auth/password", {
    token: tok, body: { current_password: "", new_password: CLERK_PW },
  });
  if (pw.status !== 204) throw new Error(`set clerk password: ${pw.status} ${pw.text}`);

  const inv = await j("POST", `/v1/companies/${CO}/members/invites`, {
    token: OWNER, body: { subject: CLERK, security_level_name: "Data Entry" },
  });
  if (inv.status !== 201) throw new Error(`invite: ${inv.status} ${inv.text}`);

  // ⛔ THE CLAIM. Without this the token is 403 on everything and every refusal
  // below would pass while proving nothing.
  const me = await j("GET", "/v1/me", { token: tok });
  if (me.status !== 200) throw new Error(`claim via /v1/me: ${me.status} ${me.text}`);
  const claimed = (me.body.claimed_invites ?? []).length;
  if (claimed !== 1) throw new Error(`claimed_invites = ${claimed}, want 1`);
  return tok;
})();

const base = `/v1/companies/${CO}`;
console.log(`\n── tenant ${CO}  ·  clerk ${CLERK} ─────────────`);

// ===========================================================================
// THE CONTROL — proves the token is a MEMBER and the coarse grant is real.
// ===========================================================================
{
  const r = await j("GET", `${base}/trial-balance?as_on=2026-07-31`, { token: CLERK_TOK });
  rec("D95: ⛔ CONTROL — the clerk IS a member and still reads the Trial Balance",
      r.status === 200,
      `trial-balance = ${r.status} (403 here would mean the invite was never claimed ` +
        `and every refusal below is vacuous)`);
}

// ===========================================================================
// ⛔⛔ THE TWO ARMS THAT GO RED-TO-GREEN ON MIGRATION 0076
// ===========================================================================
for (const [path, what] of [
  ["/cash-flow", "the forward cash position"],
  ["/anomalies", "the control surface naming this level's own irregular entries"],
]) {
  const clerk = await j("GET", base + path, { token: CLERK_TOK });
  rec(`D95: ⛔ DISPLAY — the clerk is REFUSED ${path} (${what})`,
      clerk.status === 403,
      `${path} as Data Entry = ${clerk.status} ${JSON.stringify(clerk.body).slice(0, 80)}`);

  const owner = await j("GET", base + path, { token: OWNER });
  rec(`D95: …and the OWNER still reads ${path} — 0076 narrows the clerk, not the owner`,
      owner.status === 200,
      `${path} as Owner = ${owner.status}`);
}

// ===========================================================================
// THE PRINT ARMS — labelled as pre-existing, and the Owner's answer is 501
// ===========================================================================
for (const name of ["cash-flow", "anomalies"]) {
  const path = `${base}/print/reports/${name}`;
  const clerk = await j("GET", path, { token: CLERK_TOK });
  rec(`D95: PRINT — the clerk is refused print ${name} ⚠️ (TRUE BEFORE 0076 TOO — durability, not a fix)`,
      clerk.status === 403,
      `print/${name} as Data Entry = ${clerk.status}`);

  const owner = await j("GET", path, { token: OWNER });
  rec(`D95: ⛔ …and the OWNER gets 501, NOT 200 — a missing LAYOUT, not a policy refusal`,
      owner.status === 501 && owner.text.includes(name),
      `print/${name} as Owner = ${owner.status} ${owner.text.slice(0, 90)}`);
}

// ===========================================================================
// THE OUT-OF-BAND ORACLE — the rules themselves, as an administrator sees them
// ===========================================================================
{
  const r = await j("GET", `${base}/security-levels`, { token: OWNER });
  const de = (r.body.security_levels ?? []).find((l) => l.name === "Data Entry");
  if (!de) throw new Error("no Data Entry level in the admin listing");
  const forecast = (de.rules ?? [])
    .filter((x) => x.resource === "report:cash-flow" || x.resource === "report:anomalies")
    .map((x) => `${x.effect} ${x.action} ${x.resource}`)
    .sort();
  // ⛔ A 403 can be produced by a dozen things — a stale membership, a wrong token,
  // a company that does not exist. Only the ROWS prove WHICH rule answered.
  rec("D95: ⛔ the four rules are on the level, and they are all `disallow`",
      forecast.length === 4 && forecast.every((f) => f.startsWith("disallow")),
      forecast.join(" | ") || "(none)");
  rec("D95: …and NO allow was seeded for either token (the user's decision was deny both)",
      !forecast.some((f) => f.startsWith("allow")),
      `${forecast.length} rows, 0 allows`);
}

// ===========================================================================
// THE BROWSER ARM — the 403-as-a-decision panel, which L16 built and which has
// never been exercised against a REAL refusal until now.
// ===========================================================================
{
  const app = await openApp({
    company: CO,
    headless: true,
    expectCompany: COMPANY_NAME,
    subject: CLERK,
    password: CLERK_PW,
  });
  const { page, body, press, altMac, toGateway } = app;
  const heading = async () =>
    (await page.locator("h1").first().innerText().catch(() => "")).trim();

  for (const [typed, title, testid] of [
    ["Cash Flow", "Cash Flow", "cash-flow-denied"],
    ["Anomalies", "Anomalies", "anomalies-denied"],
  ]) {
    await toGateway();
    await altMac("g", 1200);
    await page.keyboard.type(typed);
    await page.waitForTimeout(800);
    await press("Enter", 4000);

    const h = await heading();
    const seenBody = (await body()).replace(/\s+/g, " ");
    rec(`D95: the clerk still REACHES ${title} from the keyboard`,
        h === title, `h1="${h}"`);
    // ⛔ THE REFUSAL IS RENDERED AS A DECISION, NOT AS A BLANK OR AN ERROR. This is
    // the panel L16 shipped on speculation; this is its first real refusal.
    rec(`D95: ⛔ …and the refusal is rendered as a DECISION naming the report`,
        (await page.getByTestId(testid).count()) > 0 &&
          /may not open/i.test(seenBody),
        (seenBody.match(/.{0,40}may not open.{0,90}/i) ?? ["(no decision panel — it rendered something else)"])[0]);
    rec(`D95: …and it says printing is a SEPARATE permission from reading`,
        /separate permission/i.test(seenBody),
        (seenBody.match(/.{0,30}separate permission.{0,60}/i) ?? ["(absent)"])[0]);
    // And it is NOT a crash or an empty table pretending to be zero data.
    rec(`D95: …and NOT an empty table that reads as "no data"`,
        !/Loading…$/.test(seenBody) && !/\bNaN\b/.test(seenBody),
        "no spinner-stuck / NaN state");
  }
  await app.browser?.close?.();
}

// ===========================================================================
// ⛔ REAP. §5.14 — a probe that creates must clean up, and must ASSERT it worked.
// ===========================================================================
{
  const q = (s) => `'${String(s).replace(/'/g, "''")}'`;
  const vouchers = psql(
    `select count(*) from voucher_version where company_id = ${q(CO)}`,
  );
  // Refuse to reap a tenant that somehow has postings — better to leave it and
  // say so than to delete books.
  if (vouchers !== "0") {
    rec("D95: REAP SKIPPED — the probe tenant has postings, leaving it alone",
        false, `${vouchers} voucher versions; tenant ${CO} left in place`);
  } else {
    // ⛔ THE ORDER IS NOT GUESSWORK AND THE SHORT VERSION DOES NOT WORK. `company`
    // has FIFTEEN non-cascading FKs pointing at it, and a four-table delete fails
    // on `notification_outbox_company_id_fkey` — company creation writes an outbox
    // row. This list is every table that actually holds rows for a freshly created,
    // BOOK-LESS tenant, measured rather than assumed:
    //
    //   account_group 28 · acl_rule 96 · company_invite 1 · company_membership 2
    //   godown 1 · ledger 9 · notification_outbox 1 · security_level 2 · unit 1
    //   voucher_type 13 · company 1
    //
    // ⚠️ `account_group` self-references via parent_id, and the single-statement
    // DELETE handles that fine — Postgres does not need the leaves removed first.
    // ⚠️ A TENANT WITH BOOKS NEEDS MORE THAN THIS (vouchers, postings, stock,
    // allocations), which is why the guard above refuses to reap one that has any.
    psql(
      `begin;
       delete from acl_rule           where company_id = ${q(CO)};
       delete from company_membership where company_id = ${q(CO)};
       delete from company_invite     where company_id = ${q(CO)};
       delete from notification_outbox where company_id = ${q(CO)};
       delete from ledger             where company_id = ${q(CO)};
       delete from godown             where company_id = ${q(CO)};
       delete from unit               where company_id = ${q(CO)};
       delete from voucher_type       where company_id = ${q(CO)};
       delete from account_group      where company_id = ${q(CO)};
       delete from security_level     where company_id = ${q(CO)};
       delete from company            where id = ${q(CO)};
       commit;`,
    );
    const left = psql(`select count(*) from company where id = ${q(CO)}`);
    rec("D95: ⛔ the probe reaped its own tenant, and asserted it worked",
        left === "0", `companies matching ${CO.slice(0, 8)}…: ${left}`);
  }
}

summary();
