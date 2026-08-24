/**
 * L20 — D49: insightd authenticated nobody, and vite handed the browser a
 * same-origin door to it.
 *
 * WHAT WAS MEASURED BEFORE A LINE WAS WRITTEN (2026-08-24, on the wire):
 *
 *   curl :8088/companies/<any-uuid>/drafts  -> 200, 15 drafts, in full
 *                                              "Pune Fabricators | 21240.0000"
 *   curl :8087/v1/companies/<same-uuid>     -> 401
 *
 * Twelve routes — six draft routes, /healthz, /benchmark/last and FOUR auto-doc
 * routes nobody had counted — all anonymous. `/openapi.json` reported
 * `securitySchemes: None`. The Postgres RLS underneath was never a second layer:
 * pg_store set the tenant GUC from the same path parameter, so it enforced
 * whichever scope the caller chose.
 *
 * ⛔⛔ THE ARM THIS PROBE EXISTS FOR IS #17, AND "THE ACCEPT STILL WORKS" IS THE
 * WRONG ORACLE. All four insightd-backed layers of the duplicate-bill guard fail
 * OPEN on any non-409: claimDraftForPost warns and returns null,
 * draftAlreadyAcceptedInsightd returns null, releaseClaimAfterFailure never
 * throws, recordAccepted is fire-and-forget. So if insightd starts refusing
 * before the clients learn to send a credential, the five-door guard silently
 * collapses onto its client-side layers, the accept STILL SUCCEEDS, and the only
 * evidence is a console.warn. A gate that checked "Ctrl+A still saves" would be
 * green while a duplicate-supplier-bill defect was re-opened. So this asserts the
 * CLAIM POST REACHED insightd (200 on .../claim) and that the 409 refusal still
 * carries detail.code through the proxy.
 *
 * ⛔ IT MAKES ITS OWN TWO TENANTS AND REAPS THEM (the D91 pattern). Two, because
 * the cross-tenant arm needs a second identity that is not a member — and the
 * demo tenant must not be touched: it has one Owner membership and `verify:demo`
 * tears nothing down.
 *
 * ⚠️ THE BROWSER ARM IS READ-ONLY ON PURPOSE (§5.14/§5.15). It opens the Bill
 * Inbox on the demo tenant and asserts the row renders and the LIST request went
 * to cored's path with a 200 — it never presses Ctrl+A, because accepting the
 * seeded draft would post a voucher into the demo tenant and move `verify:demo`.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l20-d49.mjs
 *       (CO is used by the BROWSER arm only; the wire arms bring their own books)
 */
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://localhost:8087";
const INSIGHTD = "http://localhost:8088";
// The dev credential the Makefile gives BOTH processes. The probe needs it for
// exactly one arm (#12): a caller who HOLDS the credential still cannot read a
// company cored did not authorize for them.
const SERVICE_TOKEN = process.env.INSIGHTD_SERVICE_TOKEN ?? "dev-insightd-service-token";

const here = dirname(fileURLToPath(import.meta.url));
const FIXTURE = JSON.parse(
  readFileSync(
    resolve(here, "../../insightd/insightd/benchmark/corpus/intra-state-18-1line-clean.json"),
    "utf8",
  ),
);
/** The supplier name the fixture's OCR produces. The cross-tenant oracle is this
 *  STRING being absent from the other tenant's answer — not the status code. */
const SUPPLIER = "Acme Components Pvt Ltd";

const req = async (base, method, path, { token, body, headers = {} } = {}) => {
  const r = await fetch(`${base}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...headers,
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const text = await r.text();
  let parsed;
  try { parsed = text ? JSON.parse(text) : {}; } catch { parsed = { raw: text }; }
  return { status: r.status, body: parsed, text };
};
const j = (method, path, opts) => req(CORED, method, path, opts);
const direct = (method, path, opts) => req(INSIGHTD, method, path, opts);

const mint = async (subject) =>
  (await j("POST", "/auth/token", { body: { subject, amr: ["webauthn"] } })).body.token;

const psql = (sql) =>
  execFileSync("psql", [
    "-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql,
  ], { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" }).trim();

const stamp = Math.random().toString(36).slice(2, 8);
const CO_B = crypto.randomUUID(); // holds the draft
const CO_A = crypto.randomUUID(); // the outsider's own books
const OWNER_B = await mint(`d49-owner-b-${stamp}`);
const OWNER_A = await mint(`d49-owner-a-${stamp}`);
const CLERK = `d49-clerk-${stamp}`;
const CLERK_PW = "d49-clerk-pw-2026";

for (const [co, tok, name] of [
  [CO_B, OWNER_B, `D49 Probe B ${stamp}`],
  [CO_A, OWNER_A, `D49 Probe A ${stamp}`],
]) {
  const r = await j("POST", "/v1/companies", {
    token: tok,
    body: { id: co, name, fy_start_date: "2026-04-01", base_currency: "INR", state_code: "27" },
  });
  if (r.status !== 201) throw new Error(`create company ${name}: ${r.status} ${r.text}`);
}
const B = `/v1/companies/${CO_B}`;
console.log(`\n── B ${CO_B} (holds the draft)  ·  A ${CO_A} (the outsider) ──\n`);

// ===========================================================================
// SEED THE DRAFT — through cored's proxy, which is itself the first proof that
// the door exists at all.
// ===========================================================================
const DRAFT_ID = await (async () => {
  const tenant = { ...FIXTURE.input.tenant, company_id: CO_B };
  const r = await j("POST", `${B}/drafts`, {
    token: OWNER_B,
    body: { source: "upload", tenant, fixture: FIXTURE },
  });
  if (r.status !== 201) throw new Error(`create draft through cored: ${r.status} ${r.text}`);
  rec("D49: ⛔ CONTROL — a draft can be CREATED through cored's proxy (201)",
      r.status === 201 && !!r.body.draft_id,
      `draft ${String(r.body.draft_id).slice(0, 8)}… status=${r.body.status}`);
  rec("D49: …and the draft insightd built names the fixture's supplier",
      r.text.includes(SUPPLIER),
      `"${SUPPLIER}" present in the created draft`);
  return r.body.draft_id;
})();

// ===========================================================================
// 1–6. EVERY COMPANY-SCOPED insightd ROUTE REFUSES AN ANONYMOUS CALLER.
// ⛔ Six refusals, route by route — not one and an argument that the rest are the
// same shape. That argument is exactly how /benchmark/last and the four auto-doc
// routes stayed uncounted through three written scopings of this row.
// ===========================================================================
for (const [method, path, body] of [
  ["GET", `/companies/${CO_B}/drafts`, undefined],
  ["GET", `/companies/${CO_B}/drafts/${DRAFT_ID}`, undefined],
  ["POST", `/companies/${CO_B}/drafts`, { source: "upload", tenant: { company_id: CO_B }, fixture: FIXTURE }],
  ["POST", `/companies/${CO_B}/drafts/${DRAFT_ID}/claim`, { claim_token: "x", claimed_by: "anon" }],
  ["POST", `/companies/${CO_B}/drafts/${DRAFT_ID}/claim/release`, { claim_token: "x" }],
  ["POST", `/companies/${CO_B}/drafts/${DRAFT_ID}/accepted`, { voucher_id: "V", voucher_no: "1" }],
]) {
  const r = await direct(method, path, { body });
  const shown = path.replace(CO_B, "{B}").replace(DRAFT_ID, "{draft}");
  rec(`D49: ⛔ ANONYMOUS ${method} ${shown} is REFUSED (401)`,
      r.status === 401,
      `${r.status} ${String(r.body.detail ?? r.text).slice(0, 70)}`);
  // ⛔ AND THE PAYLOAD IS THE ORACLE, NOT THE STATUS. A 401 that still shipped the
  // body would be the same leak with a different number on it.
  rec(`D49: …and its answer does NOT carry the supplier name`,
      !r.text.includes(SUPPLIER),
      `"${SUPPLIER}" absent from ${r.text.length}-byte body`);
}

// ===========================================================================
// 7. /benchmark/last — the eighth route, uncounted twice because it is not
// company-scoped. There is no company to assert, so only the credential applies.
// ===========================================================================
{
  const anon = await direct("GET", "/benchmark/last");
  rec("D49: ⛔ ANONYMOUS GET /benchmark/last is REFUSED (401)", anon.status === 401, `${anon.status}`);
  const withTok = await direct("GET", "/benchmark/last", {
    headers: { "X-Insightd-Service-Token": SERVICE_TOKEN },
  });
  rec("D49: …and the SAME route answers 200 with the service credential",
      withTok.status === 200,
      `${withTok.status} (a refusal here would mean the arm above proves nothing about the credential)`);
}

// ===========================================================================
// 8–11. THE FOUR AUTO-DOC ROUTES. All 200 before, all publishing the whole
// schema, and all absent from openapi.paths — which is why every openapi-based
// count of this service's exposure missed them.
// ===========================================================================
for (const path of ["/openapi.json", "/docs", "/redoc", "/docs/oauth2-redirect"]) {
  const r = await direct("GET", path);
  rec(`D49: ⛔ ${path} no longer publishes the schema (404)`, r.status === 404, `${r.status}`);
}

// ===========================================================================
// 12. ⛔⛔ THE ARM THAT RETIRES "caller-supplied company_id in the path".
// A caller who HOLDS the service credential — an insider, a compromised sidecar,
// anything on the host — still cannot read a company cored did not authorize for
// them. Holding the credential is not holding a tenant.
// ===========================================================================
{
  const r = await direct("GET", `/companies/${CO_B}/drafts`, {
    headers: {
      "X-Insightd-Service-Token": SERVICE_TOKEN,
      "X-Insightd-Company": CO_A, // authorized for A, asking about B
    },
  });
  rec("D49: ⛔ a VALID service credential + the wrong authorized company is REFUSED (403)",
      r.status === 403, `${r.status} ${String(r.body.detail ?? "").slice(0, 60)}`);
  rec("D49: …and that refusal carries no supplier name either",
      !r.text.includes(SUPPLIER), `"${SUPPLIER}" absent`);
  const missing = await direct("GET", `/companies/${CO_B}/drafts`, {
    headers: { "X-Insightd-Service-Token": SERVICE_TOKEN },
  });
  rec("D49: ⛔ and with NO authorized company at all it is REFUSED (403)",
      missing.status === 403, `${missing.status}`);
}

// ===========================================================================
// 13. /healthz — the ONE anonymous route, and its disclosure had to move WITH
// the mechanism. The old value was pinned by a test on purpose.
// ===========================================================================
{
  const h = await direct("GET", "/healthz");
  rec("D49: /healthz still answers anonymously (a probe a deployer can read)",
      h.status === 200, `${h.status}`);
  rec("D49: ⛔ and it no longer says authentication: \"none\"",
      h.body.authentication === "cored-proxy service token",
      `authentication = ${JSON.stringify(h.body.authentication)}`);
  rec("D49: ⛔ and the tenant scope is no longer the caller's own path value",
      String(h.body.tenant_scope).includes("authorized by cored's PDP"),
      `tenant_scope = ${JSON.stringify(h.body.tenant_scope)}`);
}

// ===========================================================================
// 14–16. THROUGH CORED: the door works, and it is the PDP's door.
// ===========================================================================
{
  const anon = await j("GET", `${B}/drafts`);
  rec("D49: ⛔ cored's own draft route refuses an anonymous caller (401)",
      anon.status === 401, `${anon.status} ${String(anon.body.error ?? "").slice(0, 50)}`);

  // ⛔ THE CONTROL, AND IT CARRIES MORE WEIGHT THAN THE REFUSALS. Without it every
  // refusal above is satisfied by a door that is simply bricked up.
  const owner = await j("GET", `${B}/drafts`, { token: OWNER_B });
  rec("D49: ⛔ CONTROL — B's owner LISTS B's drafts through cored (200)",
      owner.status === 200 && Array.isArray(owner.body) && owner.body.length === 1,
      `${owner.status}, ${Array.isArray(owner.body) ? owner.body.length : "?"} draft(s)`);
  rec("D49: …and the supplier name IS in that answer (the door is not bricked up)",
      owner.text.includes(SUPPLIER), `"${SUPPLIER}" present`);

  // ⛔ CROSS-TENANT. A's bearer naming B. The defect was never the status code —
  // it was that string appearing in an A-scoped response.
  const cross = await j("GET", `${B}/drafts`, { token: OWNER_A });
  rec("D49: ⛔ A's bearer asking for B's drafts is REFUSED (403 not a member)",
      cross.status === 403, `${cross.status} ${String(cross.body.error ?? "").slice(0, 50)}`);
  rec("D49: ⛔⛔ and B's SUPPLIER NAME appears nowhere in A's answer",
      !cross.text.includes(SUPPLIER),
      `"${SUPPLIER}" absent from ${cross.text.length}-byte body`);
  const crossOne = await j("GET", `${B}/drafts/${DRAFT_ID}`, { token: OWNER_A });
  rec("D49: ⛔ …nor when A names the draft id directly",
      crossOne.status === 403 && !crossOne.text.includes(SUPPLIER),
      `${crossOne.status}, supplier absent=${!crossOne.text.includes(SUPPLIER)}`);
}

// ===========================================================================
// 17–18. ⛔⛔ THE ANTI-FAIL-OPEN ARMS. See the header: a gate that asserted "the
// accept still works" would be GREEN while the money defect was re-opened.
// ===========================================================================
{
  const claim = await j("POST", `${B}/drafts/${DRAFT_ID}/claim`, {
    token: OWNER_B, body: { claim_token: "probe-token-1", claimed_by: "L20 probe" },
  });
  rec("D49: ⛔⛔ the CLAIM POST REACHES insightd through the proxy (200)",
      claim.status === 200,
      `${claim.status} — a 401/403 here collapses the five-door duplicate guard onto ` +
        `its client-side layers, silently, and the accept would still succeed`);
  rec("D49: …and insightd actually recorded the claim (not an echo)",
      psql(`select (body->>'claim_token') from insightd_draft where company_id = '${CO_B}' and draft_id = '${DRAFT_ID}'`) === "probe-token-1",
      "insightd_draft.body.claim_token read back OUT OF BAND, from the database");

  // ⛔ THE 409 CONTRACT. conflictCode reads err.body.detail.code and ONLY for 409.
  // A proxy that reshaped the body into cored's {"error": …} form, or normalised
  // the status, turns three specific refusals into the fail-open branch.
  const second = await j("POST", `${B}/drafts/${DRAFT_ID}/claim`, {
    token: OWNER_B, body: { claim_token: "probe-token-2", claimed_by: "another tab" },
  });
  rec("D49: ⛔ a SECOND claimant is refused 409 THROUGH the proxy",
      second.status === 409, `${second.status}`);
  rec("D49: ⛔⛔ and the 409 still carries detail.code — the body was NOT reshaped",
      typeof second.body?.detail?.code === "string" && second.body.detail.code.length > 0,
      `detail.code = ${JSON.stringify(second.body?.detail?.code)} (cored's own errors are ` +
        `{"error": …}; if this is undefined the guard's conflictCode reads nothing and ` +
        `fails OPEN)`);

  const release = await j("POST", `${B}/drafts/${DRAFT_ID}/claim/release`, {
    token: OWNER_B, body: { claim_token: "probe-token-1" },
  });
  rec("D49: the holder can RELEASE its claim through the proxy (200)",
      release.status === 200, `${release.status}`);
}

// ===========================================================================
// 19–20. ⛔⛔ THE ARMS THAT PROVE THE TOKEN CHOICE WAS UNDERSTOOD RATHER THAN
// MERELY CHOSEN. The six routes map onto the `purchase` voucher class, so an
// administrator can write two DIFFERENT rules — and they must bite differently.
// ===========================================================================
{
  // A real Data Entry member of B: mint -> first password -> invite -> CLAIM.
  // ⚠️ Without the /v1/me claim the token is 403 on EVERYTHING and every refusal
  // below would pass while proving nothing. That mistake has been made here before.
  const clerkTok = await mint(CLERK);
  const pw = await j("POST", "/auth/password", {
    token: clerkTok, body: { current_password: "", new_password: CLERK_PW },
  });
  if (pw.status !== 204) throw new Error(`set clerk password: ${pw.status} ${pw.text}`);
  const inv = await j("POST", `${B}/members/invites`, {
    token: OWNER_B, body: { subject: CLERK, security_level_name: "Data Entry" },
  });
  if (inv.status !== 201) throw new Error(`invite: ${inv.status} ${inv.text}`);
  const me = await j("GET", "/v1/me", { token: clerkTok });
  if ((me.body.claimed_invites ?? []).length !== 1) {
    throw new Error(`invite not claimed: ${me.status} ${me.text}`);
  }

  // ⛔ THE POSITIVE ARM FIRST (§5.23: can it even succeed?). Migration 0011 seeds
  // Data Entry `allow create|alter|display purchase`, so the level that actually
  // keys supplier bills reads the inbox with NO migration of its own. If this were
  // 403, the token choice would have shipped an inbox nobody can open.
  const clerkList = await j("GET", `${B}/drafts`, { token: clerkTok });
  rec("D49: ⛔ a DATA ENTRY clerk reads the Bill Inbox (200) — no migration needed",
      clerkList.status === 200 && Array.isArray(clerkList.body) && clerkList.body.length === 1,
      `${clerkList.status}, ${Array.isArray(clerkList.body) ? clerkList.body.length : "?"} draft(s) ` +
        `— 0011 already grants this level display+create on 'purchase'`);
  const clerkClaim = await j("POST", `${B}/drafts/${DRAFT_ID}/claim`, {
    token: clerkTok, body: { claim_token: "clerk-token", claimed_by: CLERK },
  });
  rec("D49: …and may CLAIM one (200) — the same authority that lets them post it",
      clerkClaim.status === 200, `${clerkClaim.status}`);
  await j("POST", `${B}/drafts/${DRAFT_ID}/claim/release`, {
    token: clerkTok, body: { claim_token: "clerk-token" },
  });

  // ⛔ NOW THE LEVER. `disallow display purchase` on the clerk's level must hide
  // the inbox — while leaving what they may still read untouched.
  // ⚠️ RULES ARE WRITTEN BY PUTTING THE WHOLE LEVEL. There is no
  // POST .../rules route: `PUT /security-levels/{id}` REPLACES the rule set when
  // `rules` is present (and `"rules": []` clears them), so the existing rows have
  // to be read and carried forward or this arm would silently revoke 0011's
  // grants and every refusal below would pass for the wrong reason.
  const levels = await j("GET", `${B}/security-levels`, { token: OWNER_B });
  const de = (levels.body.security_levels ?? []).find((l) => l.name === "Data Entry");
  if (!de) throw new Error(`no Data Entry level in the admin listing: ${levels.text.slice(0, 300)}`);
  const baseRules = (de.rules ?? []).map(({ effect, action, resource }) => ({ effect, action, resource }));

  const setRules = async (extra) =>
    j("PUT", `${B}/security-levels/${de.id}`, {
      token: OWNER_B, body: { rules: [...baseRules, ...extra] },
    });

  // ---- 19. `disallow display purchase` hides the inbox ---------------------
  const wroteDisplay = await setRules([{ effect: "disallow", action: "display", resource: "purchase" }]);
  if (wroteDisplay.status !== 200) {
    rec("D49: (could not write `disallow display purchase` — arm 19 SKIPPED)", false,
        `${wroteDisplay.status} ${wroteDisplay.text.slice(0, 200)}`);
  } else {
    // ⛔ THE OUT-OF-BAND ORACLE FIRST. A 403 can be produced by a dozen things —
    // a stale membership, a wrong token, a company that does not exist. Only the
    // ROW proves WHICH rule answered.
    const after = await j("GET", `${B}/security-levels`, { token: OWNER_B });
    const rows = ((after.body.security_levels ?? []).find((l) => l.id === de.id)?.rules ?? [])
      .filter((x) => x.resource === "purchase")
      .map((x) => `${x.effect} ${x.action}`)
      .sort();
    rec("D49: the level now carries `disallow display purchase` beside 0011's grants",
        rows.includes("disallow display") && rows.includes("allow create"),
        rows.join(" | "));

    const hidden = await j("GET", `${B}/drafts`, { token: clerkTok });
    rec("D49: ⛔⛔ `disallow display purchase` HIDES the Bill Inbox (403)",
        hidden.status === 403, `${hidden.status}`);
    rec("D49: …and the supplier name is not in that refusal",
        !hidden.text.includes(SUPPLIER), `"${SUPPLIER}" absent`);
    // ⛔ THE COUNTERWEIGHT: a rule that denied EVERYTHING would satisfy the arm
    // above and mean nothing. The clerk must still read what nobody denied.
    const tb = await j("GET", `${B}/trial-balance?as_on=2026-07-31`, { token: clerkTok });
    rec("D49: ⛔ …WHILE the same clerk still reads the Trial Balance (200)",
        tb.status === 200,
        `${tb.status} — this is what makes the refusal above a RULE and not a bricked door`);
  }

  // ---- 20. ⛔⛔ THE SHARPEST ARM: the two actions are SEPARABLE -------------
  // `disallow create purchase` alone must refuse the CLAIM — the first write of
  // the whole flow, before any master or voucher exists — while the inbox stays
  // READABLE. A mapping that lumped all six routes onto one action could never
  // pass this, and it is what proves the token choice was understood rather than
  // merely chosen.
  const wroteCreate = await setRules([{ effect: "disallow", action: "create", resource: "purchase" }]);
  if (wroteCreate.status !== 200) {
    rec("D49: (could not write `disallow create purchase` — arm 20 SKIPPED)", false,
        `${wroteCreate.status} ${wroteCreate.text.slice(0, 200)}`);
  } else {
    const stillReads = await j("GET", `${B}/drafts`, { token: clerkTok });
    rec("D49: ⛔⛔ `disallow create purchase` leaves the inbox READABLE (200)",
        stillReads.status === 200, `${stillReads.status}`);
    const refusedClaim = await j("POST", `${B}/drafts/${DRAFT_ID}/claim`, {
      token: clerkTok, body: { claim_token: "should-never-land", claimed_by: CLERK },
    });
    rec("D49: ⛔⛔ …and REFUSES the claim (403) — the two authorities are separable",
        refusedClaim.status === 403, `${refusedClaim.status}`);
    // ⛔ AND NOTHING WAS RESERVED. A refusal that still took the lease would strand
    // the bill for the TTL — which is the failure the release route exists for.
    const held = psql(
      `select coalesce(body->>'claim_token','(none)') from insightd_draft ` +
      `where company_id = '${CO_B}' and draft_id = '${DRAFT_ID}'`,
    );
    rec("D49: ⛔ …and no claim was recorded — the refusal happened BEFORE the write",
        held !== "should-never-land",
        `insightd_draft.body.claim_token = ${held} (read out of band)`);
  }
}

// ===========================================================================
// 21. THE BROWSER. Read-only, on the demo tenant, so it cannot move verify:demo.
// ⛔ It asserts the LIST went to CORED's path — a client still pointed at :8088
// would 401 and the inbox would render "no drafts", which looks like a seeding
// problem rather than a broken transport.
// ===========================================================================
const CO_DEMO = process.env.CO;
if (!CO_DEMO) {
  rec("D49: BROWSER ARM SKIPPED — no CO in the environment", false,
      'pass CO="$(cat /tmp/drive-company-id)"');
} else {
  const app = await openApp({ company: CO_DEMO });
  const seen = [];
  app.page.on("response", (r) => {
    const u = new global.URL(r.url());
    if (u.pathname.includes("/drafts")) {
      seen.push(`${r.request().method()} ${u.pathname} ${r.status()}`);
    }
  });
  await app.openFromGateway("Bill Inbox", 2500);
  const body = await app.page.locator("body").innerText();

  rec("D49: the Bill Inbox opens in a real browser", /Bill Inbox/i.test(body), "");
  const listCalls = seen.filter((s) => s.startsWith("GET"));
  rec("D49: ⛔ the LIST went to CORED's path, under /v1, and answered 200",
      listCalls.some((s) => /^GET \/v1\/companies\/[0-9a-f-]+\/drafts 200$/.test(s)),
      listCalls.join(" | ") || "(no /drafts request was made at all)");
  rec("D49: ⛔ and NOTHING in the browser addressed insightd's own prefix",
      !seen.some((s) => / \/companies\//.test(s)),
      seen.join(" | ") || "(none)");
  // ⛔ AND THE ROW RENDERED. A 200 on an empty array would satisfy the arm above.
  rec("D49: ⛔ the seeded draft RENDERS — party and total on the screen",
      /Pune Fabricators/.test(body) && /21240|18000|21,240/.test(body),
      (body.match(/.{0,40}Pune Fabricators.{0,60}/) ?? ["(the party is not on the screen)"])[0]
        .replace(/\s+/g, " "));
  await app.browser?.close?.();
}

// ===========================================================================
// ⛔ REAP. §5.14 — a probe that creates must clean up, and must ASSERT it worked.
// ⚠️ AND IT MUST REAP THE DRAFTS TOO. `insightd_draft.company_id` is TEXT, not a
// uuid with an FK, so nothing in the schema would ever have told us they were
// left behind — they would simply accumulate under a company that no longer
// exists (61 stray `co-1` rows were found exactly that way once).
// ===========================================================================
{
  const q = (s) => `'${String(s).replace(/'/g, "''")}'`;
  for (const co of [CO_B, CO_A]) {
    const vouchers = psql(`select count(*) from voucher_version where company_id = ${q(co)}`);
    if (vouchers !== "0") {
      rec(`D49: REAP SKIPPED for ${co.slice(0, 8)}… — it has postings`, false,
          `${vouchers} voucher versions; left in place rather than deleting books`);
      continue;
    }
    psql(
      `begin;
       delete from insightd_draft     where company_id = ${q(co)};
       delete from acl_rule           where company_id = ${q(co)};
       delete from company_membership where company_id = ${q(co)};
       delete from company_invite     where company_id = ${q(co)};
       delete from notification_outbox where company_id = ${q(co)};
       delete from ledger             where company_id = ${q(co)};
       delete from godown             where company_id = ${q(co)};
       delete from unit               where company_id = ${q(co)};
       delete from voucher_type       where company_id = ${q(co)};
       delete from account_group      where company_id = ${q(co)};
       delete from security_level     where company_id = ${q(co)};
       delete from company            where id = ${q(co)};
       commit;`,
    );
    const left = psql(`select count(*) from company where id = ${q(co)}`);
    const drafts = psql(`select count(*) from insightd_draft where company_id = ${q(co)}`);
    rec(`D49: ⛔ reaped ${co.slice(0, 8)}… and asserted it worked`,
        left === "0" && drafts === "0", `company rows ${left}, draft rows ${drafts}`);
  }
}

summary();
