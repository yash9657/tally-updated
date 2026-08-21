/**
 * L9 — USERS + SECURITY. D25 (signing in never claimed a pending invite),
 * D55 (no way to unlock an account, and no way to change your own password),
 * D56 (locking an account did not revoke the sessions it already held),
 * D57 (the lockout policy was hardcoded), D58 (the lock message was raw UTC),
 * D73 (attempts during a lock window were never audited), D74
 * (failed_attempts was not a running counter).
 *
 * ⛔⛔ LOCKOUT IS KEYED ON **SUBJECT**, SO THIS FILE NEVER TOUCHES
 * `browser-dev-owner`. One wrong password for the demo owner locks the account
 * every other probe in this directory signs in as, and the next lane's failures
 * then look like the defects it was written to test. Two THROWAWAY subjects are
 * minted per run instead, and they are kept apart on purpose:
 *
 *   U_PW    the invite + password-change drives. NEVER given a wrong password.
 *   U_LOCK  the lockout drives. Deliberately locked, then unlocked.
 *
 * Mixing them would mean a lock from D56 lands in the middle of D55's password
 * change and the 423 reads as a broken form.
 *
 * ⛔ D57 RESTARTS CORED with AUTH_LOCKOUT_MAX_FAILURES=3 and puts it back
 * afterwards. A configuration knob cannot be driven without changing the
 * configuration, and asserting the constant from the source would be reading
 * the code rather than the product.
 */
import { spawn } from "node:child_process";
import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO;
const CORED = "http://localhost:8087";
const PW_OLD = "l9-old-password-2026";
const PW_NEW = "l9-new-password-2026";
const stamp = Math.random().toString(36).slice(2, 8);
const U_PW = `l9-pw-${stamp}`;
const U_LOCK = `l9-lock-${stamp}`;

// ---------------------------------------------------------------------------
// Wire helpers. Everything here is cored's own surface — no mock, no client.
// ---------------------------------------------------------------------------
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

/** A dev token for any subject. AUTH_DEV_TOKEN_ENABLED gates this route. */
const devToken = async (subject, amr = ["webauthn"]) =>
  (await j("POST", "/auth/token", { body: { subject, amr } })).body.token;

/** Sign in for real, the way the browser does. */
const login = (subject, password) =>
  j("POST", "/auth/login", { body: { subject, password } });

const sql = async (query) => {
  const { execFile } = await import("node:child_process");
  return new Promise((res, rej) => {
    execFile(
      "psql",
      ["-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", query],
      { env: { ...process.env, PGPASSWORD: "postgres" } },
      (err, stdout) => (err ? rej(err) : res(stdout.trim())),
    );
  });
};

const OWNER = await devToken("browser-dev-owner");

// ---------------------------------------------------------------------------
// SETUP. Both subjects get a first password (the dev token path — there is no
// production route that sets a FIRST password, which auth_login.go discloses)
// and an invitation to the company.
// ---------------------------------------------------------------------------
for (const subject of [U_PW, U_LOCK]) {
  const t = await devToken(subject);
  const set = await j("POST", "/auth/password", {
    token: t,
    body: { current_password: "", new_password: PW_OLD },
  });
  if (set.status !== 204) {
    throw new Error(`setup: could not set ${subject}'s first password: ${set.status} ${JSON.stringify(set.body)}`);
  }
  const inv = await j("POST", `/v1/companies/${CO}/members/invites`, {
    token: OWNER,
    body: { subject, email: `${subject}@example.test`, security_level_name: "Data Entry" },
  });
  if (inv.status !== 201 && inv.status !== 200) {
    throw new Error(`setup: could not invite ${subject}: ${inv.status} ${JSON.stringify(inv.body)}`);
  }
  // ⛔ U_LOCK CLAIMS ITS INVITE HERE; U_PW DELIBERATELY DOES NOT.
  //
  // An invite is not a membership: GET /v1/me is the only seam that converts one
  // (that IS D25), so a subject that never calls it is refused 403 "not a member"
  // by every company route. The first run of this file skipped this and read
  // those 403s as a broken D56 control and a missing member row.
  //
  // U_PW is left UNCLAIMED because D25's assertion is that SIGNING IN claims it.
  // Claiming it here would satisfy the assertion in setup and the probe could
  // never fail.
  if (subject === U_LOCK) {
    const me = await j("GET", "/v1/me", { token: await devToken(subject) });
    if (me.status !== 200) {
      throw new Error(`setup: ${subject} could not claim its invite: ${me.status}`);
    }
  }
}

// ===========================================================================
// D25 — SIGNING IN CLAIMS THE PENDING INVITE AND LANDS IN THAT COMPANY.
//
// ⛔ NO `?company=` ON PURPOSE, AND THE ASSERTION DEPENDS ON IT. The fix
// (App.useSessionBootstrap -> adoptCompanyIfNoneChosen) fills the company in
// ONLY when the operator has expressed no preference: nothing persisted and
// nothing in the URL. A probe that passed `?company=` would disable the code it
// is testing and report D25 fixed whatever the app did.
// ===========================================================================
{
  const app = await openApp({
    allowNoCompany: true,
    expectCompany: "Demo Traders",
    headless: true,
    // ⛔ AS THE INVITED USER. The default is the demo OWNER, who is already a
    // member of everything — the first run of this file signed in as the owner
    // and its "landed in the invited company" assertion was vacuous.
    subject: U_PW,
    password: PW_OLD,
  });
  // openApp already threw if the chrome did not name Demo Traders, so reaching
  // here IS the result — but say so explicitly rather than leaving a silent pass.
  const shown = await app.body();
  rec(
    "D25: ⛔ an INVITED user signing in with no company in the URL lands in the INVITED company",
    /Demo Traders/.test(shown) && !/My Company/.test(shown),
    (shown.split("\n").find((l) => /Traders|My Company/.test(l)) ?? "<no company on the bar>").trim(),
  );
  rec(
    "D25: and the claim happened over GET /v1/me (the only seam that converts an invite)",
    (await sql(
      `select count(*) from company_membership cm join app_user u on u.id = cm.user_id
       where u.subject = '${U_PW}' and cm.company_id = '${CO}'`,
    )) === "1",
    `company_membership rows for ${U_PW}: ${await sql(
      `select count(*) from company_membership cm join app_user u on u.id = cm.user_id
       where u.subject = '${U_PW}' and cm.company_id = '${CO}'`,
    )}`,
  );
  await app.browser.close();
}

// ===========================================================================
// D55 (a) — THE SIGNED-IN USER CHANGES THEIR OWN PASSWORD, IN THE CHROME.
// The oracle is not the 204. It is that the OLD password stops working and the
// NEW one starts: a screen reporting success from a status code looks identical
// whether it changed anything or not.
// ===========================================================================
{
  const app = await openApp({
    company: CO,
    headless: true,
    // ⛔ AS U_PW, NEVER THE OWNER. This form sends a current_password, and a
    // wrong one is a password GUESS that counts toward that subject's lockout —
    // so driving it as `browser-dev-owner` aims the demo owner's own lockout
    // budget at the probe. The first run of this file did exactly that.
    subject: U_PW,
    password: PW_OLD,
  });
  const { page } = app;
  const before = app.net.length;

  await page.getByTestId("change-password-open").click();
  await page.waitForTimeout(300);
  rec(
    "D55: the chrome offers a Change Password form at all (it had NO caller before)",
    (await page.getByTestId("change-password-form").count()) === 1,
    `form present: ${(await page.getByTestId("change-password-form").count()) === 1}`,
  );
  await page.getByTestId("change-password-current").fill(PW_OLD);
  await page.getByTestId("change-password-new").fill(PW_NEW);
  await page.getByTestId("change-password-confirm").fill(PW_NEW);
  await page.getByTestId("change-password-submit").click();
  await page.waitForTimeout(2000);

  const wire = app.net.slice(before);
  rec(
    "D55: ⛔ the browser really reached POST /auth/password and it answered 204",
    wire.some((w) => /POST \/auth\/password 204/.test(w)),
    wire.join(" · ") || "<nothing went on the wire>",
  );
  rec(
    "D55: and the session SURVIVES the change (the operator is not signed out)",
    (await page.getByTestId("login-screen").count()) === 0 &&
      (await page.getByTestId("signed-in-as").count()) === 1,
    `login screen back: ${(await page.getByTestId("login-screen").count()) > 0}`,
  );
  await app.browser.close();

  // ⛔ THE REAL ORACLE, ON THE WIRE, NOT IN THE UI.
  const oldTry = await login(U_PW, PW_OLD);
  const newTry = await login(U_PW, PW_NEW);
  rec(
    "D55: ⛔ the OLD password is now REFUSED",
    oldTry.status === 401,
    `login with the old password: ${oldTry.status}`,
  );
  rec(
    "D55: ⛔ and the NEW password SIGNS IN",
    newTry.status === 200 && typeof newTry.body.token === "string",
    `login with the new password: ${newTry.status}`,
  );
}

// ===========================================================================
// D56 — LOCKING AN ACCOUNT REVOKES THE SESSION IT ALREADY HOLDS.
// The token is taken BEFORE the lock and proved to work, so a refusal after the
// lock cannot be a token that was never valid.
// ===========================================================================
const liveToken = (await login(U_LOCK, PW_OLD)).body.token;
{
  const readBefore = await j("GET", `/v1/companies/${CO}/trial-balance?as_on=2026-08-31`, {
    token: liveToken,
  });
  rec(
    "D56: CONTROL — the bearer WORKS before the lock (else the refusal below proves nothing)",
    readBefore.status === 200,
    `GET /trial-balance with the pre-lock bearer: ${readBefore.status}`,
  );
}

// Drive the lock the way an attacker would: wrong passwords on /auth/login.
let lockedAt = null;
const attempts = [];
for (let i = 1; i <= 6; i++) {
  const r = await login(U_LOCK, `wrong-password-${i}`);
  attempts.push(`${i}:${r.status}`);
  // D74's oracle has to be read BETWEEN attempts — after the lock trips the
  // counter is no longer "partway".
  const counter = await sql(`select failed_attempts from app_user where subject = '${U_LOCK}'`);
  attempts[attempts.length - 1] += `(fa=${counter})`;
  if (r.status === 423 && lockedAt === null) lockedAt = i;
}
rec(
  "the lock trips on /auth/login, at the configured threshold",
  // ⛔ THE LOCK TRIPS **ON** THE 5th FAILURE AND THAT SAME RESPONSE IS THE 423 —
  // it is not "5 failures, then a 6th attempt refused". This probe predicted 6
  // and read the correct behaviour as a defect.
  lockedAt === 5,
  `attempt:status(failed_attempts) -> ${attempts.join(" ")} · first 423 at ${lockedAt} (want 5 = the default threshold)`,
);

rec(
  "D74: ⛔ failed_attempts is a RUNNING counter, not 0 until the lock trips",
  /1:401\(fa=1\)/.test(attempts.join(" ")) && /2:401\(fa=2\)/.test(attempts.join(" ")),
  `after failure 1: fa=${(attempts[0] ?? "").match(/fa=(\d+)/)?.[1]} · after failure 2: fa=${(attempts[1] ?? "").match(/fa=(\d+)/)?.[1]}`,
);

{
  const readAfter = await j("GET", `/v1/companies/${CO}/trial-balance?as_on=2026-08-31`, {
    token: liveToken,
  });
  const writeAfter = await j("POST", `/v1/companies/${CO}/vouchers/receipt`, {
    token: liveToken,
    body: { voucher_date: "2026-08-10", party: "Acme Industries", amount: "1.0000" },
  });
  rec(
    "D56: ⛔ the SAME bearer is now refused a READ",
    readAfter.status === 401 || readAfter.status === 403,
    `GET /trial-balance with the pre-lock bearer AFTER the lock: ${readAfter.status} ${JSON.stringify(readAfter.body).slice(0, 90)}`,
  );
  rec(
    "D56: ⛔ and refused a WRITE (this was the driven defect — it used to answer 201)",
    writeAfter.status === 401 || writeAfter.status === 403,
    `POST /vouchers/receipt with the pre-lock bearer AFTER the lock: ${writeAfter.status}`,
  );
}

// ===========================================================================
// D73 — THE KNOCK IS AUDITED. Attempt 6 above was refused 423 without the
// password ever being checked; that attempt must still leave a trace.
// ===========================================================================
{
  const locked = await sql(
    `select count(*) from auth_login_attempt where subject = '${U_LOCK}' and outcome = 'locked'`,
  );
  const failures = await sql(
    `select count(*) from auth_login_attempt where subject = '${U_LOCK}' and outcome = 'failure'`,
  );
  rec(
    "D73: ⛔ an attempt made DURING the lock window is audited, with its own outcome",
    Number(locked) >= 1,
    `auth_login_attempt for ${U_LOCK}: outcome=locked ${locked}, outcome=failure ${failures}`,
  );
  rec(
    "D73: and it is NOT counted as a failure (a lock must not renew itself off its own refusals)",
    Number(failures) === 5,
    `outcome=failure rows: ${failures} (want exactly the 5 real password guesses)`,
  );
}

// ===========================================================================
// D58 — THE LOCK MESSAGE, IN A BROWSER, IN THE OPERATOR'S OWN CLOCK.
// ===========================================================================
{
  // ⛔ RAW PLAYWRIGHT, NOT openApp, AND THIS IS THE ONE PLACE THAT IS RIGHT.
  // openApp asserts it got PAST the login screen; here the whole point is to be
  // REFUSED at it and to read what the refusal says. Every other leg imports the
  // harness.
  const { chromium } = await import("playwright");
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  await page.goto(`http://localhost:5173/?company=${CO}`, { waitUntil: "networkidle" });
  await page.waitForTimeout(800);
  await page.keyboard.type(U_LOCK);
  await page.keyboard.press("Tab");
  await page.keyboard.type(PW_OLD); // the CORRECT password — the lock must still refuse it
  await page.keyboard.press("Enter");
  await page.waitForTimeout(2500);
  const err = await page.locator('[data-testid="login-error"]').innerText().catch(() => "");
  rec(
    "D58: the login screen reports the lock (and refuses even the CORRECT password)",
    /lock/i.test(err),
    err.replace(/\n/g, " ").slice(0, 150) || "<no login error rendered>",
  );
  rec(
    "D58: ⛔ it does NOT hand the operator a raw UTC RFC3339 instant",
    !/\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z/.test(err),
    (err.match(/\d{4}-\d{2}-\d{2}T[^\s]*/) ?? ["no ISO instant in the message"])[0],
  );
  rec(
    "D58: ⛔ and it states HOW LONG — the only question being asked",
    /minute|hour|second/i.test(err),
    (err.match(/[^.]*\b(minute|hour|second)[^.]*/i) ?? ["<no duration in the message>"])[0].trim().slice(0, 120),
  );
  await browser.close();
}

// ===========================================================================
// D55 (b) — THE ADMINISTRATIVE UNLOCK. There was no route, no key and no
// screen; the lock was also INVISIBLE on Users & Roles.
// ===========================================================================
{
  const members = await j("GET", `/v1/companies/${CO}/members`, { token: OWNER });
  const mine = (members.body.members ?? []).find((m) => m.subject === U_LOCK);
  rec(
    "D55: ⛔ Users & Roles can SEE the lock (it rendered no lock state at all before)",
    Boolean(mine?.locked_until),
    mine ? `locked_until = ${mine.locked_until ?? "<absent>"}` : `${U_LOCK} is not in the member list`,
  );

  const unlock = await j("PUT", `/v1/companies/${CO}/members/${mine?.user_id}`, {
    token: OWNER,
    body: { unlock: true },
  });
  rec(
    "D55: the administrative unlock route answers",
    unlock.status === 200 || unlock.status === 204,
    `PUT /members/{userID} {"unlock":true}: ${unlock.status} ${JSON.stringify(unlock.body).slice(0, 80)}`,
  );

  const after = await login(U_LOCK, PW_OLD);
  rec(
    "D55: ⛔ and the account can SIGN IN again — the unlock actually cleared the lock",
    after.status === 200,
    `login after the unlock: ${after.status}`,
  );
  const audited = await sql(
    `select count(*) from auth_login_attempt where subject = '${U_LOCK}' and outcome = 'unlocked'`,
  );
  rec(
    "D55: and the unlock itself is audited",
    Number(audited) >= 1,
    `auth_login_attempt outcome=unlocked rows: ${audited}`,
  );
}

// ===========================================================================
// D56 (second clause) — THE SESSIONS A LOCK KILLS STAY DEAD AFTER THE UNLOCK.
// Otherwise a lock is a pause button and a stolen bearer simply waits.
// ===========================================================================
{
  const readAfterUnlock = await j("GET", `/v1/companies/${CO}/trial-balance?as_on=2026-08-31`, {
    token: liveToken,
  });
  rec(
    "D56: ⛔ the pre-lock bearer stays DEAD even after the account is unlocked",
    readAfterUnlock.status === 401 || readAfterUnlock.status === 403,
    `the pre-lock bearer after the unlock: ${readAfterUnlock.status}`,
  );
}

// ===========================================================================
// D57 — THE THRESHOLD IS CONFIGURED, NOT COMPILED IN.
// Restart cored with AUTH_LOCKOUT_MAX_FAILURES=3 and show the lock trips at 3.
// ⛔ A CONFIGURATION KNOB CANNOT BE DRIVEN WITHOUT CHANGING THE CONFIGURATION.
// ===========================================================================
const restartCored = async (env) => {
  const owner = await new Promise((res) => {
    const p = spawn("bash", ["-lc", "lsof -nP -iTCP:8087 -sTCP:LISTEN | tail -1 | awk '{print $2}'"]);
    let out = "";
    p.stdout.on("data", (d) => (out += d));
    p.on("close", () => res(out.trim()));
  });
  if (owner) process.kill(Number(owner), "SIGTERM");
  await new Promise((r) => setTimeout(r, 2500));
  spawn("bash", ["-lc", "cd /Users/yashbhalgat/code/tally-updated && make run-cored > /tmp/cored.log 2>&1"], {
    detached: true,
    stdio: "ignore",
    env: { ...process.env, ...env },
  }).unref();
  for (let i = 0; i < 60; i++) {
    await new Promise((r) => setTimeout(r, 1000));
    try {
      const r = await fetch(`${CORED}/healthz`);
      if (r.ok) return true;
    } catch {
      /* not up yet */
    }
  }
  return false;
};

{
  const up = await restartCored({ AUTH_LOCKOUT_MAX_FAILURES: "3" });
  rec("D57: cored restarts with AUTH_LOCKOUT_MAX_FAILURES=3", up, up ? "healthz 200" : "cored did not come back");

  if (up) {
    const U_CFG = `l9-cfg-${stamp}`;
    const t = await devToken(U_CFG);
    await j("POST", "/auth/password", {
      token: t,
      body: { current_password: "", new_password: PW_OLD },
    });
    let firstLock = null;
    const seq = [];
    for (let i = 1; i <= 5 && firstLock === null; i++) {
      const r = await login(U_CFG, `wrong-${i}`);
      seq.push(`${i}:${r.status}`);
      if (r.status === 423) firstLock = i;
    }
    rec(
      "D57: ⛔ the threshold is the CONFIGURED 3, not the compiled-in 5",
      // Same off-by-one as above: with the knob at 3 the THIRD failure is the
      // one refused 423, where the default deployment took five.
      firstLock === 3,
      `attempt:status -> ${seq.join(" ")} · first 423 at ${firstLock} (3 failures then the refusal)`,
    );
  }
}

// ⛔ PUT THE DEPLOYMENT BACK. A probe that leaves cored running with a
// three-strike lockout would make the NEXT lane's first typo lock an account.
{
  const back = await restartCored({});
  rec(
    "D57: and cored is restored to its default policy for the next lane",
    back,
    back ? "healthz 200 with no AUTH_LOCKOUT_* in the environment" : "⛔ CORED DID NOT COME BACK — restart it by hand",
  );
}

console.log(
  "\nSUBJECTS USED (never browser-dev-owner):",
  [U_PW, U_LOCK, `l9-cfg-${stamp}`].join(" · "),
);
process.exit(summary() > 0 ? 1 : 0);
