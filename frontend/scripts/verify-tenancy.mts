/**
 * VERIFY EVERY ACCESS-CONTROL PATH — authn, the PDP, and tenant isolation.
 *
 *   cd frontend && pnpm verify:tenancy <companyA> <companyB>
 *
 * Where `verify:demo` proves the ACCOUNTING is right, this proves who is allowed
 * to do what. It drives the live HTTP surface with raw fetch (never the typed
 * client, which throws on non-2xx and would hide the status codes that ARE the
 * contract) and asserts the exact status of every branch in the policy engine.
 *
 * THE MODEL BEING TESTED (backend/internal/authz/authz.go), in precedence order:
 *
 *   1. service principal (subject "svc:...")  -> full access, no cutoff, no step-up
 *   2. level.is_owner                         -> allow, but cutoff + step-up STILL apply
 *   3. most-specific matching ACL rule        -> disallow beats allow at equal specificity
 *   4. no rule matched                        -> default_access: full -> allow, none -> deny
 *   5. back-dated cutoff on writes            -> voucher_date < cutoff -> deny
 *   6. requires_webauthn on writes            -> no webauthn AMR -> STEPUP_REQUIRED
 *
 * And the status contract the layers agree on:
 *   401  missing / malformed / expired token          (authn)
 *   403  authenticated but not permitted              (membership or PDP)
 *   403 + code STEPUP_REQUIRED  authenticated, permitted, but needs WebAuthn
 *   423 + code LOCKED           too many failed logins (POST /auth/login only)
 *
 * §9 additionally drives the FIRST-PARTY PASSWORD LOGIN end to end — set a
 * password, sign in, be refused, be locked out, and prove the lock is durable —
 * against a THROWAWAY `login-probe-<uuid>` identity that it deletes again. That
 * is a hard requirement, not tidiness: those checks deliberately lock the
 * account they run against, and locking one of the seeded demo identities would
 * break every later run.
 */

import { execFileSync } from "node:child_process";

const baseUrl = process.env.CORED_BASE_URL ?? "http://localhost:8087";
const PG_CONTAINER = process.env.PG_CONTAINER ?? "tally_pg";

function requireArg(v: string | undefined, usage: string): string {
  if (!v) {
    console.error(usage);
    process.exit(2);
  }
  return v;
}
const USAGE = "usage: pnpm verify:tenancy <companyA> <companyB>";
const companyA: string = requireArg(process.argv[2], USAGE);
const companyB: string = requireArg(process.argv[3], USAGE);

let failures = 0;
let checks = 0;
let section = "";

function head(name: string): void {
  section = name;
  console.log(`\n${name}`);
}

function check(name: string, ok: boolean, detail: string): void {
  checks += 1;
  if (!ok) failures += 1;
  console.log(`  ${ok ? "PASS" : "FAIL"}  ${name}\n          ${detail}`);
  void section;
}

async function mint(subject: string, amr: string[]): Promise<string> {
  const res = await fetch(`${baseUrl}/auth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject, amr }),
  });
  if (!res.ok) throw new Error(`mint ${subject}: ${res.status}`);
  return ((await res.json()) as { token: string }).token;
}

interface Res {
  status: number;
  /** The FULL response body. Truncating here broke JSON.parse for the caller. */
  body: string;
  code?: string;
}

/** Short form of a body, for log lines only — never for parsing. */
function brief(r: Res): string {
  return `${r.status} ${r.body.slice(0, 140)}`;
}

/** Raw request — returns the status instead of throwing, which is the point. */
async function call(
  method: string,
  path: string,
  tok: string | null,
  body?: unknown,
): Promise<Res> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (tok) headers.Authorization = `Bearer ${tok}`;
  const res = await fetch(`${baseUrl}${path}`, {
    method,
    headers,
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const text = await res.text();
  let code: string | undefined;
  try {
    code = (JSON.parse(text) as { code?: string }).code;
  } catch {
    /* not JSON */
  }
  return { status: res.status, body: text, code };
}

function sql(statement: string): string {
  return execFileSync(
    "docker",
    ["exec", PG_CONTAINER, "psql", "-U", "postgres", "-d", "tally", "-t", "-A", "-c", statement],
    { encoding: "utf8" },
  )
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "" && !/^(INSERT|UPDATE|DELETE|SELECT)\s+\d/.test(l))
    .join("\n");
}

/**
 * Every voucher this script creates, so it can be REVERSED at the end.
 *
 * Testing write permission means actually writing, and this runs against the
 * real accounting demo company — so without cleanup each run would leave probe
 * vouchers behind, drift the Trial Balance and eventually break `verify:demo`.
 * Ids are collected here and tombstoned in the `finally` block.
 */
const created: string[] = [];

/** Record a probe voucher id from a successful post, for later cleanup. */
function trackVoucher(r: Res): Res {
  if (r.status === 201) {
    try {
      const id = (JSON.parse(r.body) as { voucher_id?: string }).voucher_id;
      if (id) created.push(id);
    } catch {
      /* truncated/non-JSON body: nothing to track */
    }
  }
  return r;
}

/** A minimal balanced journal, for testing WRITE permission rather than accounting. */
async function probeJournal(
  tok: string,
  company: string,
  ledgerA: string,
  ledgerB: string,
  date: string,
): Promise<Res> {
  return trackVoucher(
    await call("POST", `/v1/companies/${company}/vouchers/journal`, tok, {
    voucher_date: date,
    narration: "acl probe",
    postings: [
      { ledger_id: ledgerA, dr_cr: "D", amount: "1.0000" },
      { ledger_id: ledgerB, dr_cr: "C", amount: "1.0000" },
    ],
    }),
  );
}

async function main(): Promise<void> {
  // Identities. Note the two Owner tokens: one WITH the webauthn AMR and one
  // WITHOUT — that single difference is what exercises the step-up branch.
  const ownerA = await mint("browser-dev-owner", ["webauthn"]);
  const ownerAWeak = await mint("browser-dev-owner", ["pwd"]);
  const dataEntryA = await mint("demo-data-entry", ["pwd"]);
  const ownerB = await mint("sharma-owner", ["webauthn"]);
  const stranger = await mint("nobody-at-all", ["webauthn"]);
  const service = await mint("svc:statutoryd", ["pwd"]);

  // Two ledgers on A to post a throwaway journal between. Read through the API
  // (GET /ledgers), not SQL: the chart of accounts is a supported read now.
  const ledgerRes = await call("GET", `/v1/companies/${companyA}/ledgers`, ownerA);
  const ledgers = (JSON.parse(ledgerRes.body) as { ledgers: { id: string; name: string }[] }).ledgers;
  const byName = (n: string): string | undefined => ledgers.find((l) => l.name === n)?.id;
  const ledA1 = byName("Cash");
  const ledA2 = byName("Capital A/c");
  if (!ledA1 || !ledA2) throw new Error("could not resolve two ledgers on company A");

  console.log(`\nVERIFYING ACCESS CONTROL\n  A = ${companyA}\n  B = ${companyB}`);

  // =========================================================================
  head("1. AUTHENTICATION — 401 is reserved for token problems");
  // =========================================================================
  let r = await call("GET", `/v1/companies/${companyA}/trial-balance`, null);
  check("no token -> 401", r.status === 401, brief(r));

  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, "not-a-jwt");
  check("malformed token -> 401", r.status === 401, brief(r));

  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, stranger);
  check(
    "valid token, no membership -> 403 (NOT 401)",
    r.status === 403,
    brief(r),
  );

  // =========================================================================
  head("2. OWNER — full access, but writes still gated");
  // =========================================================================
  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, ownerA);
  check("Owner + webauthn -> read report -> 200", r.status === 200, `${r.status}`);

  r = await probeJournal(ownerA, companyA, ledA1, ledA2, "2026-07-20");
  check("Owner + webauthn -> WRITE voucher -> 201", r.status === 201, brief(r));

  const probeLedgerName = `ACL Probe ${process.pid}`;
  r = await call("POST", `/v1/companies/${companyA}/ledgers`, ownerA, {
    name: probeLedgerName,
    group_name: "Indirect Expenses",
  });
  check("Owner -> create MASTER -> 201", r.status === 201, brief(r));

  // The step-up branch: same user, same right, token lacking the webauthn AMR.
  r = await probeJournal(ownerAWeak, companyA, ledA1, ledA2, "2026-07-20");
  check(
    "Owner WITHOUT webauthn -> write -> 403 STEPUP_REQUIRED",
    r.status === 403 && r.code === "STEPUP_REQUIRED",
    `code=${r.code ?? "(none)"} ${brief(r)}`,
  );

  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, ownerAWeak);
  check(
    "Owner WITHOUT webauthn -> READ still 200 (step-up gates writes only)",
    r.status === 200,
    `${r.status}`,
  );

  // =========================================================================
  head("3. DATA ENTRY — default-deny, plus explicit allow rules");
  // =========================================================================
  r = await probeJournal(dataEntryA, companyA, ledA1, ledA2, "2026-07-20");
  check(
    "Data Entry -> create journal -> 201 (allowed by rule)",
    r.status === 201,
    brief(r),
  );

  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, dataEntryA);
  check(
    "Data Entry -> display report -> 200 (allow display report)",
    r.status === 200,
    `${r.status}`,
  );

  // The class added in migration 0018 — allowed only because 0019 extended the
  // Data Entry rule set. Without that, this is a bare 403.
  const cnProbe = trackVoucher(
    await call(
      "POST",
      `/v1/companies/${companyA}/vouchers/credit-note`,
      dataEntryA,
      {
        voucher_date: "2026-07-20",
        narration: "acl probe",
        postings: [
          { ledger_id: ledA1, dr_cr: "D", amount: "1.0000" },
          { ledger_id: ledA2, dr_cr: "C", amount: "1.0000" },
        ],
      },
    ),
  );
  check(
    "Data Entry -> create CREDIT NOTE -> 201 (rule added by migration 0019)",
    cnProbe.status === 201,
    brief(cnProbe),
  );

  // Now the denials. Each is "no matching rule -> default_access none -> deny".
  const someVoucher = sql(
    `SELECT voucher_id FROM voucher_head
      WHERE company_id = '${companyA}' AND is_deleted = false LIMIT 1`,
  );
  r = await call("DELETE", `/v1/companies/${companyA}/vouchers/${someVoucher}`, dataEntryA);
  check(
    "Data Entry -> DELETE voucher -> 403 (no delete rule)",
    r.status === 403,
    brief(r),
  );

  r = await call("POST", `/v1/companies/${companyA}/ledgers`, dataEntryA, {
    name: `Denied ${process.pid}`,
    group_name: "Indirect Expenses",
  });
  check(
    "Data Entry -> create MASTER -> 403 (no master rule)",
    r.status === 403,
    brief(r),
  );

  r = await call("POST", `/v1/companies/${companyA}/query`, dataEntryA, {
    question: "what are my sales",
  });
  check(
    "Data Entry -> POST /query -> 403 (no query rule)",
    r.status === 403,
    brief(r),
  );

  r = await call("POST", `/v1/companies/${companyA}/import/tally-xml`, dataEntryA, {});
  check(
    "Data Entry -> import Tally XML -> 403 (no import rule)",
    r.status === 403,
    brief(r),
  );

  // Documented consequence of default-deny: the company master read is
  // Display/"company", which Data Entry has no rule for.
  r = await call("GET", `/v1/companies/${companyA}`, dataEntryA);
  check(
    "Data Entry -> read COMPANY master -> 403 (Display/company has no rule)",
    r.status === 403,
    brief(r),
  );

  // =========================================================================
  head("4. TENANT ISOLATION — membership is the gate");
  // =========================================================================
  r = await call("GET", `/v1/companies/${companyB}/trial-balance`, ownerA);
  check(
    "Owner of A -> company B -> 403 (not a member)",
    r.status === 403,
    brief(r),
  );

  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, ownerB);
  check(
    "Owner of B -> company A -> 403 (not a member)",
    r.status === 403,
    brief(r),
  );

  r = await call("GET", `/v1/companies/${companyB}/trial-balance`, ownerB);
  check("Owner of B -> company B -> 200", r.status === 200, `${r.status}`);

  // Positive isolation: the two tenants really do hold different books, so a
  // leak would be visible rather than coincidentally identical.
  // Read each tenant's books with ITS OWN owner's token. Fetched directly rather
  // than through call(), whose body is truncated for display.
  const totalDebit = async (company: string, tok: string): Promise<string> => {
    const res = await fetch(`${baseUrl}/v1/companies/${company}/trial-balance`, {
      headers: { Authorization: `Bearer ${tok}` },
    });
    const j = (await res.json()) as { total_debit?: string };
    return j.total_debit ?? "";
  };
  const tbA = { total_debit: await totalDebit(companyA, ownerA) };
  const tbB = { total_debit: await totalDebit(companyB, ownerB) };
  check(
    "A and B hold DIFFERENT books (a leak would be visible)",
    !!tbA.total_debit && !!tbB.total_debit && tbA.total_debit !== tbB.total_debit,
    `A total_debit=${tbA.total_debit} vs B total_debit=${tbB.total_debit}`,
  );

  // =========================================================================
  head("5. BACK-DATED CUTOFF — a write before the cutoff is refused");
  // =========================================================================
  // Applied to the Data Entry level on A, exercised, then removed — through the
  // ADMIN API (this used to be a psql UPDATE; setting a level's cutoff is now a
  // supported operation, so the verifier exercises the same path a customer has).
  const levelsRes = await call("GET", `/v1/companies/${companyA}/security-levels`, ownerA);
  const levels = (
    JSON.parse(levelsRes.body) as {
      security_levels: { id: string; name: string; is_owner: boolean }[];
    }
  ).security_levels;
  const deLevelID = levels.find((l) => l.name === "Data Entry")?.id;
  if (!deLevelID) throw new Error("company A has no 'Data Entry' security level");
  const setCutoff = async (value: string): Promise<Res> =>
    call("PUT", `/v1/companies/${companyA}/security-levels/${deLevelID}`, ownerA, {
      backdated_cutoff: value,
    });

  r = await setCutoff("2026-07-01");
  check("Owner sets the Data Entry back-dated cutoff via the API -> 200", r.status === 200, brief(r));
  try {
    r = await probeJournal(dataEntryA, companyA, ledA1, ledA2, "2026-06-15");
    check(
      "cutoff 2026-07-01, voucher dated 2026-06-15 -> 403",
      r.status === 403,
      brief(r),
    );
    r = await probeJournal(dataEntryA, companyA, ledA1, ledA2, "2026-07-15");
    check(
      "same level, voucher dated 2026-07-15 -> 201 (on/after the cutoff)",
      r.status === 201,
      brief(r),
    );
  } finally {
    const cleared = await setCutoff("");
    check(
      "cutoff cleared again (the verifier leaves the level as it found it)",
      cleared.status === 200,
      brief(cleared),
    );
  }

  // =========================================================================
  head("6. SERVICE PRINCIPAL — full access, no step-up, no cutoff");
  // =========================================================================
  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, service);
  check(
    "svc: principal -> read company A -> 200 (no membership needed)",
    r.status === 200,
    `${r.status}`,
  );
  r = await call("POST", `/v1/companies`, service, {
    name: "svc should not create",
    fy_start_date: "2026-04-01",
  });
  check(
    "svc: principal -> create a COMPANY -> 403 (cannot bootstrap tenants)",
    r.status === 403,
    brief(r),
  );

  // =========================================================================
  head("7. DEFAULT-DENY — an unmapped route is refused, never allowed");
  // =========================================================================
  r = await call("GET", `/v1/companies/${companyA}/not-a-real-route`, ownerA);
  check(
    "unmapped route, even for an Owner -> 403",
    r.status === 403,
    brief(r),
  );

  // =========================================================================
  head("8. ADMIN SURFACE — users, roles and custom levels, over HTTP");
  // =========================================================================
  // Everything here used to be psql-only. The section proves three things: the
  // surface exists, it is governed by the SAME PDP as the books (so an operator
  // cannot administer users), and a level created through it actually changes
  // what its holder can do.
  r = await call("GET", `/v1/companies/${companyA}/members`, ownerA);
  const memberSubjects = r.status === 200
    ? (JSON.parse(r.body) as { members: { subject: string }[] }).members.map((m) => m.subject)
    : [];
  check(
    "Owner -> list members -> 200 with both identities",
    r.status === 200 &&
      memberSubjects.includes("browser-dev-owner") &&
      memberSubjects.includes("demo-data-entry"),
    `${r.status} [${memberSubjects.join(", ")}]`,
  );

  r = await call("GET", `/v1/companies/${companyA}/members`, dataEntryA);
  check(
    "Data Entry -> list members -> 403 (administering users is not data entry)",
    r.status === 403,
    brief(r),
  );

  r = await call("POST", `/v1/companies/${companyA}/members/invites`, ownerAWeak, {
    subject: "should-never-exist",
    security_level_name: "Data Entry",
  });
  check(
    "Owner WITHOUT webauthn -> invite -> 403 STEPUP_REQUIRED (granting access is a write)",
    r.status === 403 && r.code === "STEPUP_REQUIRED",
    `code=${r.code ?? "(none)"} ${brief(r)}`,
  );

  r = await call("GET", `/v1/companies/${companyA}/members`, ownerB);
  check("Owner of B -> members of A -> 403 (not a member)", r.status === 403, brief(r));

  r = await call("GET", `/v1/companies/${companyA}/members`, service);
  check(
    "svc: principal -> members of A -> 403 (svc may pin an IRN, never grant access)",
    r.status === 403,
    brief(r),
  );

  // A misspelled rule is refused at write time. Stored, it would silently never
  // match — for a `disallow` that is the difference between forbidden and allowed.
  r = await call("POST", `/v1/companies/${companyA}/security-levels`, ownerA, {
    name: `ACL Probe Typo ${process.pid}`,
    default_access: "none",
    rules: [{ effect: "allow", action: "display", resource: "report:trail-balance" }],
  });
  check("a MISSPELLED rule resource -> 422 (closed vocabulary)", r.status === 422, brief(r));

  // A real custom level, granted to a real subject, judged by the PDP.
  const probeLevelName = `ACL Probe Auditor ${process.pid}`;
  const probeSubject = `acl-probe-auditor-${process.pid}`;
  r = await call("POST", `/v1/companies/${companyA}/security-levels`, ownerA, {
    name: probeLevelName,
    default_access: "none",
    rules: [{ effect: "allow", action: "display", resource: "report:trial-balance" }],
  });
  const probeLevelID = r.status === 201
    ? (JSON.parse(r.body) as { security_level_id: string }).security_level_id
    : "";
  check("Owner -> create a CUSTOM security level -> 201", r.status === 201 && !!probeLevelID, brief(r));

  r = await call("POST", `/v1/companies/${companyA}/members/invites`, ownerA, {
    subject: probeSubject,
    security_level_id: probeLevelID,
  });
  check("Owner -> invite a subject at the custom level -> 201", r.status === 201, brief(r));

  const probeTok = await mint(probeSubject, ["pwd"]);
  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, probeTok);
  check(
    "invitee BEFORE claiming -> 403 (an invite is not a membership)",
    r.status === 403,
    brief(r),
  );

  r = await call("GET", "/v1/me", probeTok);
  const me = r.status === 200
    ? (JSON.parse(r.body) as {
        claimed_invites: unknown[];
        companies: { company_id: string; security_level_name: string }[];
      })
    : { claimed_invites: [], companies: [] };
  check(
    "GET /v1/me claims the invite and lists the company (the basis for F3 switching)",
    r.status === 200 &&
      me.claimed_invites.length === 1 &&
      me.companies.some((c) => c.company_id === companyA && c.security_level_name === probeLevelName),
    `${r.status} claimed=${me.claimed_invites.length} companies=${JSON.stringify(me.companies.map((c) => c.security_level_name))}`,
  );

  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, probeTok);
  check("custom level -> the ONE report it was granted -> 200", r.status === 200, brief(r));

  r = await call("GET", `/v1/companies/${companyA}/balance-sheet`, probeTok);
  check("custom level -> a report it was NOT granted -> 403", r.status === 403, brief(r));

  r = await probeJournal(probeTok, companyA, ledA1, ledA2, "2026-07-20");
  check("custom level -> write a voucher -> 403", r.status === 403, brief(r));

  r = await call("DELETE", `/v1/companies/${companyA}/security-levels/${probeLevelID}`, ownerA);
  check("delete a level that still has a member -> 409", r.status === 409, brief(r));

  // The last-Owner guard: company A has exactly one Owner, so demoting them is
  // refused. (Refused, so this check changes no state.)
  const memberID = async (subject: string): Promise<string> => {
    const res = await call("GET", `/v1/companies/${companyA}/members`, ownerA);
    const parsed = JSON.parse(res.body) as { members: { user_id: string; subject: string }[] };
    return parsed.members.find((m) => m.subject === subject)?.user_id ?? "";
  };
  const ownerUserID = await memberID("browser-dev-owner");
  r = await call("PUT", `/v1/companies/${companyA}/members/${ownerUserID}`, ownerA, {
    security_level_name: "Data Entry",
  });
  check(
    "demote the LAST Owner -> 409 (a company can never be left unadministrable)",
    r.status === 409,
    brief(r),
  );

  // Undo the probe: remove the member, then the level. Both are also the tests
  // for those endpoints.
  const probeUserID = await memberID(probeSubject);
  r = await call("DELETE", `/v1/companies/${companyA}/members/${probeUserID}`, ownerA);
  check("Owner -> revoke a member -> 200", r.status === 200, brief(r));

  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, probeTok);
  check("the revoked member is locked out immediately -> 403", r.status === 403, brief(r));

  r = await call("DELETE", `/v1/companies/${companyA}/security-levels/${probeLevelID}`, ownerA);
  check("Owner -> delete the now-unused custom level -> 200", r.status === 200, brief(r));

  r = await call("GET", `/v1/companies/${companyA}/security-levels`, ownerA);
  const remaining = r.status === 200
    ? (JSON.parse(r.body) as { security_levels: { name: string }[] }).security_levels.map((l) => l.name)
    : [];
  // Assert the SEEDED levels are intact and THIS run's probe level is gone —
  // not that the company has exactly two levels. A real company is expected to
  // have custom levels; a verifier that failed because of one would be wrong.
  check(
    "the seeded levels are intact and this run's probe level is gone",
    remaining.includes("Owner") &&
      remaining.includes("Data Entry") &&
      !remaining.includes(probeLevelName),
    `[${remaining.join(", ")}]`,
  );

  // =========================================================================
  head("8b. ADMIN SURFACE — the writes the BROWSER now performs");
  // =========================================================================
  // Gateway > Users & Roles gained change-level, revoke-access, revoke-invite
  // and create/edit/delete-level. Those paths are exercised above; this section
  // covers the three the screen depends on that nothing else pinned LIVE: the
  // served rule vocabulary, the Owner level's immutability, and revoking a
  // PENDING invite (which the UI lists separately precisely because it is not a
  // membership).

  // -- The rule vocabulary the browser's rule editor is built from -----------
  // Hardcoding these tokens in TypeScript would drift from the PDP; a rule with
  // a drifted resource is not rejected by the PDP, it silently never matches.
  r = await call("GET", `/v1/companies/${companyA}/security-levels/vocabulary`, ownerA);
  const vocab = r.status === 200
    ? (JSON.parse(r.body) as {
        effects: string[]; actions: string[]; resources: string[]; default_accesses: string[];
      })
    : { effects: [], actions: [], resources: [], default_accesses: [] };
  check(
    "Owner -> the CLOSED rule vocabulary -> 200 (real tokens present, the typo absent)",
    r.status === 200 &&
      vocab.resources.includes("report:trial-balance") &&
      vocab.resources.includes("security") &&
      vocab.actions.includes("display") &&
      vocab.effects.includes("disallow") &&
      !vocab.resources.includes("report:trail-balance"),
    `${r.status} ${vocab.resources.length} resources / ${vocab.actions.length} actions`,
  );

  // Every served token must be WRITABLE, or the editor offers rules the server
  // 422s. Validation is per-token, so one level carrying each action against a
  // fixed resource and each resource against a fixed action covers them all.
  const vocabProbeName = `ACL Vocab Probe ${process.pid}`;
  r = await call("POST", `/v1/companies/${companyA}/security-levels`, ownerA, {
    name: vocabProbeName,
    default_access: "none",
    rules: [
      ...vocab.actions.map((a) => ({ effect: "allow", action: a, resource: "report" })),
      ...vocab.resources.map((res) => ({ effect: "allow", action: "display", resource: res })),
    ],
  });
  const vocabProbeID = r.status === 201
    ? (JSON.parse(r.body) as { security_level_id: string }).security_level_id
    : "";
  check(
    "a level carrying EVERY served token -> 201 (the editor cannot compose a 422)",
    r.status === 201,
    brief(r),
  );
  if (vocabProbeID) {
    r = await call("DELETE", `/v1/companies/${companyA}/security-levels/${vocabProbeID}`, ownerA);
    check("the vocabulary probe level is cleaned up -> 200", r.status === 200, brief(r));
  }

  r = await call("GET", `/v1/companies/${companyA}/security-levels/vocabulary`, dataEntryA);
  check(
    "Data Entry -> the rule vocabulary -> 403 (same `security` resource as the rest)",
    r.status === 403,
    brief(r),
  );

  // -- The Owner level is immutable except its back-dated cutoff -------------
  // The browser's level editor disables every other field for it and sends ONLY
  // the cutoff; this is the server-side half of that contract.
  r = await call("GET", `/v1/companies/${companyA}/security-levels`, ownerA);
  const ownerLevelID = r.status === 200
    ? (JSON.parse(r.body) as { security_levels: { id: string; is_owner: boolean }[] })
        .security_levels.find((l) => l.is_owner)?.id ?? ""
    : "";
  r = await call("PUT", `/v1/companies/${companyA}/security-levels/${ownerLevelID}`, ownerA, {
    requires_webauthn: false,
  });
  check(
    "turn OFF the Owner level's WebAuthn requirement -> 422 (built-in, immutable)",
    r.status === 422,
    brief(r),
  );
  r = await call("PUT", `/v1/companies/${companyA}/security-levels/${ownerLevelID}`, ownerA, {
    name: "Owner Renamed",
  });
  check("rename the Owner level -> 422", r.status === 422, brief(r));

  // ...but the cutoff IS settable, and it binds Owners too. Set then CLEAR, so
  // this check leaves the company exactly as it found it — a stray Owner cutoff
  // would break every later write in this script and in the browser demo.
  r = await call("PUT", `/v1/companies/${companyA}/security-levels/${ownerLevelID}`, ownerA, {
    backdated_cutoff: "2000-01-01",
  });
  check("set the Owner level's back-dated cutoff -> 200 (the ONE editable field)", r.status === 200, brief(r));
  r = await call("PUT", `/v1/companies/${companyA}/security-levels/${ownerLevelID}`, ownerA, {
    backdated_cutoff: "",
  });
  check("clear it again -> 200 (this section leaves no state behind)", r.status === 200, brief(r));
  r = await call("GET", `/v1/companies/${companyA}/security-levels`, ownerA);
  const ownerCutoff = r.status === 200
    ? (JSON.parse(r.body) as { security_levels: { is_owner: boolean; backdated_cutoff: string }[] })
        .security_levels.find((l) => l.is_owner)?.backdated_cutoff ?? "?"
    : "?";
  check(
    "the Owner level's WebAuthn requirement and cutoff are as they started",
    ownerCutoff === "",
    `backdated_cutoff=${JSON.stringify(ownerCutoff)}`,
  );

  // -- CHANGING a member's level, judged by the PDP -------------------------
  // §8 proves a demotion is REFUSED (the last-Owner guard). Nothing proved a
  // permitted change actually takes effect — and the admin API reading back its
  // own write would be the system echoing itself. So the oracle is a real
  // business route: the same token must reach a report before the change and be
  // refused after it, with nothing else altered.
  // The two levels are deliberately DISJOINT and both narrow — one report each,
  // named exactly. The seeded Data Entry level would NOT do: its rule is the
  // BARE `report`, which matches every report by name, so a move away from it
  // is invisible to a report oracle (the first draft of this check asserted
  // 403 -> 200 against Data Entry and observed 200 -> 200).
  const moveSubject = `acl-move-probe-${process.pid}`;
  const mkLevel = async (suffix: string, report: string): Promise<string> => {
    const res = await call("POST", `/v1/companies/${companyA}/security-levels`, ownerA, {
      name: `ACL Move ${suffix} ${process.pid}`,
      default_access: "none",
      rules: [{ effect: "allow", action: "display", resource: `report:${report}` }],
    });
    check(`create the "${suffix}" probe level (only report:${report}) -> 201`, res.status === 201, brief(res));
    return res.status === 201
      ? (JSON.parse(res.body) as { security_level_id: string }).security_level_id
      : "";
  };
  const fromLevelID = await mkLevel("from", "trial-balance");
  const toLevelID = await mkLevel("to", "balance-sheet");

  r = await call("POST", `/v1/companies/${companyA}/members/invites`, ownerA, {
    subject: moveSubject,
    security_level_id: fromLevelID,
  });
  check("invite them at the FROM level -> 201", r.status === 201, brief(r));
  const moveTok = await mint(moveSubject, ["pwd"]);
  r = await call("GET", "/v1/me", moveTok);
  check("they claim the invite -> 200", r.status === 200, brief(r));

  const canSee = async (report: string): Promise<number> =>
    (await call("GET", `/v1/companies/${companyA}/${report}`, moveTok)).status;
  const moveTbBefore = await canSee("trial-balance");
  const moveBsBefore = await canSee("balance-sheet");
  check(
    "on the FROM level they see the trial balance and NOT the balance sheet",
    moveTbBefore === 200 && moveBsBefore === 403,
    `trial-balance=${moveTbBefore} balance-sheet=${moveBsBefore} (want 200 / 403)`,
  );

  r = await call("GET", `/v1/companies/${companyA}/members`, ownerA);
  const moveUserID = r.status === 200
    ? (JSON.parse(r.body) as { members: { user_id: string; subject: string }[] })
        .members.find((m) => m.subject === moveSubject)?.user_id ?? ""
    : "";
  r = await call("PUT", `/v1/companies/${companyA}/members/${moveUserID}`, ownerA, {
    security_level_id: toLevelID,
  });
  check("Owner -> CHANGE their security level -> 200", r.status === 200, brief(r));

  const moveTbAfter = await canSee("trial-balance");
  const moveBsAfter = await canSee("balance-sheet");
  check(
    "the change takes effect at once, and it is a MOVE (both directions flip), judged by the PDP",
    moveTbAfter === 403 && moveBsAfter === 200,
    `trial-balance ${moveTbBefore}->${moveTbAfter} (want 200->403), balance-sheet ${moveBsBefore}->${moveBsAfter} (want 403->200)`,
  );

  r = await call("DELETE", `/v1/companies/${companyA}/members/${moveUserID}`, ownerA);
  check("the move probe's membership is cleaned up -> 200", r.status === 200, brief(r));
  for (const [what, lvl] of [["from", fromLevelID], ["to", toLevelID]] as const) {
    r = await call("DELETE", `/v1/companies/${companyA}/security-levels/${lvl}`, ownerA);
    check(`the "${what}" probe level is cleaned up -> 200`, r.status === 200, brief(r));
  }

  // -- Revoking a PENDING invite --------------------------------------------
  const revokeSubject = `revoke-probe-${process.pid}`;
  r = await call("POST", `/v1/companies/${companyA}/members/invites`, ownerA, {
    subject: revokeSubject,
    security_level_name: "Data Entry",
  });
  const revokeInviteID = r.status === 201
    ? (JSON.parse(r.body) as { invite_id: string }).invite_id
    : "";
  check("invite a throwaway subject -> 201", r.status === 201 && !!revokeInviteID, brief(r));

  const pendingSubjects = async (): Promise<string[]> => {
    const res = await call("GET", `/v1/companies/${companyA}/members`, ownerA);
    if (res.status !== 200) return [];
    return (JSON.parse(res.body) as { invites: { subject: string }[] }).invites.map((i) => i.subject);
  };
  check(
    "it is listed as PENDING (an invite is not yet a membership)",
    (await pendingSubjects()).includes(revokeSubject),
    revokeSubject,
  );

  r = await call("DELETE", `/v1/companies/${companyA}/members/invites/${revokeInviteID}`, ownerA);
  check("Owner -> revoke the pending invite -> 200", r.status === 200, brief(r));
  check(
    "it leaves the pending list at once",
    !(await pendingSubjects()).includes(revokeSubject),
    `[${(await pendingSubjects()).join(", ")}]`,
  );

  // The real claim: a revoked invite is INERT. The invitee signs in and gets
  // nothing — not an error, just no company.
  const revokeTok = await mint(revokeSubject, ["pwd"]);
  r = await call("GET", "/v1/me", revokeTok);
  const revokedMe = r.status === 200
    ? (JSON.parse(r.body) as { companies: { company_id: string }[] }).companies
    : [{ company_id: "(call failed)" }];
  check(
    "the revoked invitee signs in and is a member of NOTHING (a revoked invite grants nothing)",
    r.status === 200 && !revokedMe.some((c) => c.company_id === companyA),
    `${r.status} companies=${revokedMe.length}`,
  );
  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, revokeTok);
  check("...and cannot read company A -> 403", r.status === 403, brief(r));

  // =========================================================================
  head("8c. THE EDIT LOG — who may read a voucher's history, and what it says");
  // =========================================================================
  // GET .../vouchers/{id}/history is the MCA audit trail made readable. Two
  // things need pinning live: it is governed by the SAME (Display, voucher) pair
  // as fetching the voucher — so nobody can see a history they could not see the
  // voucher for — and, unlike the head fetch, it reads THROUGH a tombstone.
  r = await probeJournal(ownerA, companyA, ledA1, ledA2, "2026-07-20");
  const histVoucherID = r.status === 201
    ? (JSON.parse(r.body) as { voucher_id: string }).voucher_id
    : "";
  check("post a probe voucher to inspect the log of -> 201", r.status === 201 && !!histVoucherID, brief(r));

  const historyOf = async (tok: string | null): Promise<Res> =>
    call("GET", `/v1/companies/${companyA}/vouchers/${histVoucherID}/history`, tok);

  r = await historyOf(ownerA);
  const hist = r.status === 200
    ? (JSON.parse(r.body) as {
        is_deleted: boolean;
        versions: { version_no: number; op: string; created_by: string; hash: string; prev_hash: string }[];
        verification: { ok: boolean; versions_checked: number; reason: string };
      })
    : null;
  check(
    "Owner -> the edit log -> 200, and the server VERIFIES the hash chain",
    r.status === 200 && hist?.verification.ok === true && hist.verification.versions_checked === 1,
    `${r.status} verified=${hist?.verification.ok} checked=${hist?.verification.versions_checked}`,
  );
  check(
    "v1 is a CREATE that begins the chain (a hash, and no previous hash)",
    hist?.versions[0]?.op === "CREATE" &&
      (hist?.versions[0]?.hash.length ?? 0) === 64 &&
      hist?.versions[0]?.prev_hash === "",
    `op=${hist?.versions[0]?.op} hash=${hist?.versions[0]?.hash.slice(0, 16)}… prev=${JSON.stringify(hist?.versions[0]?.prev_hash)}`,
  );

  // The history and the voucher must give the SAME answer to the same caller.
  // A log readable by somebody who cannot read the voucher would be a leak; one
  // refused to somebody who can read the voucher would be a useless audit trail.
  const voucherStatus = (await call(
    "GET", `/v1/companies/${companyA}/vouchers/${histVoucherID}`, dataEntryA)).status;
  r = await historyOf(dataEntryA);
  check(
    "Data Entry gets the SAME answer for the log as for the voucher itself",
    r.status === voucherStatus,
    `history=${r.status} voucher=${voucherStatus} (they must agree)`,
  );

  r = await historyOf(ownerB);
  check("Owner of B -> a voucher's log in A -> 403 (cross-tenant)", r.status === 403, brief(r));

  r = await historyOf(null);
  check("no token -> the log -> 401", r.status === 401, brief(r));

  // DELETE it, then read the log again. The head fetch 404s; the log does not —
  // a deletion is the single most interesting entry an audit trail can carry,
  // and an edit log that forgot deletions would defeat its own purpose.
  r = await call("DELETE", `/v1/companies/${companyA}/vouchers/${histVoucherID}`, ownerA);
  check("Owner deletes the probe voucher -> 200", r.status === 200, brief(r));
  const headAfter = (await call(
    "GET", `/v1/companies/${companyA}/vouchers/${histVoucherID}`, ownerA)).status;
  r = await historyOf(ownerA);
  const after = r.status === 200
    ? (JSON.parse(r.body) as {
        is_deleted: boolean;
        versions: { op: string }[];
        verification: { ok: boolean };
      })
    : null;
  check(
    "the DELETED voucher's head is 404 but its log survives, tombstone and all",
    headAfter === 404 &&
      r.status === 200 &&
      after?.is_deleted === true &&
      after.versions.map((v) => v.op).join(",") === "CREATE,DELETE" &&
      after.verification.ok === true,
    `head=${headAfter} log=${r.status} ops=[${after?.versions.map((v) => v.op).join(",")}] verified=${after?.verification.ok}`,
  );

  // =========================================================================
  head("9. PASSWORD LOGIN — a real first-party credential, not a dev mint");
  // =========================================================================
  // Every token above came from POST /auth/token, which mints for ANY subject:
  // it ASSERTS an identity, it does not AUTHENTICATE one. This section drives
  // the real thing — POST /auth/login (subject + password) and POST
  // /auth/password (the only writer of a password anywhere in the system).
  //
  // It uses a THROWAWAY identity, `login-probe-<uuid>`, and deletes it again at
  // the end. That is not tidiness: the lockout checks below deliberately lock
  // the account they run against, and locking one of the three SEEDED subjects
  // would break every later run of this script and of the browser demo.
  const probeLoginSubject = `login-probe-${crypto.randomUUID()}`;
  const probeLoginPassword = "verify-tenancy-2026"; // >= 10 chars (the policy floor)

  // -- Provision the identity ------------------------------------------------
  // A subject with no app_user row cannot be meaningfully locked out (SetLock
  // updates zero rows), so the probe must be a REAL user before the lockout
  // checks mean anything. GET /v1/me is authn-only and runs
  // CreateUserOnFirstLogin, which is what materialises the row.
  const probeDevTok = await mint(probeLoginSubject, ["webauthn"]);
  r = await call("GET", "/v1/me", probeDevTok);
  check(
    "throwaway subject provisioned (GET /v1/me materialises the app_user row)",
    r.status === 200,
    brief(r),
  );

  r = await call("POST", "/auth/password", null, {
    current_password: "",
    new_password: probeLoginPassword,
  });
  check("POST /auth/password with NO bearer -> 401", r.status === 401, brief(r));

  r = await call("POST", "/auth/password", probeDevTok, {
    current_password: "",
    new_password: "short",
  });
  check(
    "POST /auth/password with a too-short password -> 400 (policy, with a reason)",
    r.status === 400 && /at least 10/.test(r.body),
    brief(r),
  );

  r = await call("POST", "/auth/password", probeDevTok, {
    current_password: "",
    new_password: probeLoginPassword,
  });
  check(
    "POST /auth/password first-set (no credential yet) -> 204",
    r.status === 204,
    brief(r),
  );

  // The acting user comes from the BEARER, never the body. Sending somebody
  // else's subject in the body must change NOTHING about them — asserted below
  // by logging that other subject in with its unchanged password.
  const probeVictimSubject = `login-probe-victim-${crypto.randomUUID()}`;
  const victimPassword = "victim-password-2026";
  const victimDevTok = await mint(probeVictimSubject, ["webauthn"]);
  await call("GET", "/v1/me", victimDevTok);
  await call("POST", "/auth/password", victimDevTok, {
    current_password: "",
    new_password: victimPassword,
  });
  r = await call("POST", "/auth/password", probeDevTok, {
    subject: probeVictimSubject, // ignored: there is no subject field in the contract
    user_id: "00000000-0000-0000-0000-000000000000",
    current_password: probeLoginPassword,
    new_password: probeLoginPassword,
  });
  const victimStillWorks = await call("POST", "/auth/login", null, {
    subject: probeVictimSubject,
    password: victimPassword,
  });
  check(
    "a body-supplied `subject` cannot re-point /auth/password at another user",
    r.status === 204 && victimStillWorks.status === 200,
    `set=${r.status} victim-login=${victimStillWorks.status}`,
  );

  // -- The login itself ------------------------------------------------------
  r = await call("POST", "/auth/login", null, { password: probeLoginPassword });
  check("login with no subject -> 400", r.status === 400, brief(r));

  r = await call("POST", "/auth/login", null, { subject: probeLoginSubject });
  check("login with no password -> 400", r.status === 400, brief(r));

  r = await call("POST", "/auth/login", null, {
    subject: probeLoginSubject,
    password: probeLoginPassword,
  });
  const loginBody = r.status === 200
    ? (JSON.parse(r.body) as {
        token: string;
        token_type: string;
        expires_in: number;
        user: { id: string; subject: string };
      })
    : null;
  check(
    "login with the RIGHT password -> 200 with a Bearer and a `user` block",
    r.status === 200 &&
      !!loginBody?.token &&
      loginBody.token_type === "Bearer" &&
      loginBody.expires_in > 0 &&
      loginBody.user.subject === probeLoginSubject,
    `${r.status} user=${loginBody?.user.subject ?? "(none)"} expires_in=${loginBody?.expires_in ?? 0}`,
  );
  const loginTok = loginBody?.token ?? "";

  // NO USER ENUMERATION: a wrong password and a subject that does not exist must
  // be indistinguishable on the wire. Compared as strings, so a future "user not
  // found" refinement fails here rather than shipping an oracle.
  const wrongPw = await call("POST", "/auth/login", null, {
    subject: probeLoginSubject,
    password: "definitely-not-the-password",
  });
  const unknownSubj = await call("POST", "/auth/login", null, {
    subject: `no-such-subject-${crypto.randomUUID()}`,
    password: "definitely-not-the-password",
  });
  check(
    "wrong password -> 401, byte-identical to an UNKNOWN subject (no enumeration)",
    wrongPw.status === 401 &&
      unknownSubj.status === 401 &&
      wrongPw.body === unknownSubj.body,
    `wrong=${wrongPw.status} ${wrongPw.body} | unknown=${unknownSubj.status} ${unknownSubj.body}`,
  );

  // -- A login token is a REAL token -----------------------------------------
  // Not "it parses" — it must pass the whole authn -> membership -> PDP stack on
  // a company-scoped route. The probe is granted a custom level on company A so
  // this exercises a real membership, and the level is toggled below to isolate
  // the step-up variable.
  const loginLevelName = `Login Probe Level ${process.pid}`;
  r = await call("POST", `/v1/companies/${companyA}/security-levels`, ownerA, {
    name: loginLevelName,
    default_access: "full",
    requires_webauthn: false,
    rules: [],
  });
  const loginLevelID = r.status === 201
    ? (JSON.parse(r.body) as { security_level_id: string }).security_level_id
    : "";
  check(
    "Owner -> create a level for the login probe (no step-up) -> 201",
    r.status === 201 && !!loginLevelID,
    brief(r),
  );

  r = await call("POST", `/v1/companies/${companyA}/members/invites`, ownerA, {
    subject: probeLoginSubject,
    security_level_id: loginLevelID,
  });
  check("Owner -> invite the login probe -> 201", r.status === 201, brief(r));

  // Claimed with the LOGIN token, so the claim itself is evidence the token
  // authenticates: an unclaimed invite grants nothing, and only an authenticated
  // call can claim it.
  r = await call("GET", "/v1/me", loginTok);
  check(
    "a token from a REAL LOGIN claims the invite on /v1/me (authn-only route)",
    r.status === 200 &&
      (JSON.parse(r.body) as { claimed_invites: unknown[] }).claimed_invites.length === 1,
    brief(r),
  );

  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, loginTok);
  check(
    "a token from a REAL LOGIN reads a company-scoped report -> 200",
    r.status === 200,
    `${r.status}`,
  );

  r = await probeJournal(loginTok, companyA, ledA1, ledA2, "2026-07-20");
  check(
    "a token from a REAL LOGIN writes a voucher -> 201 (level without step-up)",
    r.status === 201,
    brief(r),
  );

  // THE DISCLOSED GAP, pinned so it cannot change silently. A login token
  // carries NO `amr` (WebAuthn is a separate ceremony), so the moment a level
  // requires step-up, the SAME token that just wrote is refused. Nothing changes
  // here except requires_webauthn — that is the whole point of toggling it.
  r = await call("PUT", `/v1/companies/${companyA}/security-levels/${loginLevelID}`, ownerA, {
    requires_webauthn: true,
  });
  check("Owner -> require WebAuthn step-up on that level -> 200", r.status === 200, brief(r));

  r = await probeJournal(loginTok, companyA, ledA1, ledA2, "2026-07-20");
  check(
    "the SAME login token, level now requiring step-up -> 403 STEPUP_REQUIRED " +
      "(a password login alone cannot write where the level demands WebAuthn)",
    r.status === 403 && r.code === "STEPUP_REQUIRED",
    `code=${r.code ?? "(none)"} ${brief(r)}`,
  );

  r = await call("GET", `/v1/companies/${companyA}/trial-balance`, loginTok);
  check(
    "…and that same token still READS 200 (step-up gates writes only)",
    r.status === 200,
    `${r.status}`,
  );

  // -- Lockout ---------------------------------------------------------------
  // Five wrong passwords in the window lock the account. One wrong attempt was
  // already spent above, so four more reach the threshold.
  let lockedAt = 0;
  let lockUntil = "";
  for (let i = 2; i <= 6 && lockedAt === 0; i += 1) {
    const attempt = await call("POST", "/auth/login", null, {
      subject: probeLoginSubject,
      password: `wrong-attempt-${i}`,
    });
    if (attempt.status === 423) {
      lockedAt = i;
      lockUntil = (JSON.parse(attempt.body) as { until?: string; code?: string }).until ?? "";
      check(
        "5 wrong passwords -> 423 with code LOCKED and an `until`",
        attempt.code === "LOCKED" && !!lockUntil,
        `attempt ${i}: ${brief(attempt)}`,
      );
    }
  }
  if (lockedAt === 0) check("5 wrong passwords -> 423 LOCKED", false, "never locked");

  // THE non-vacuous half. The 423 above could come from the attempt COUNTER
  // alone (auth_login_attempt has no RLS and is written for any subject, even
  // one that does not exist). Only a DURABLE lock — a locked_until on the
  // app_user row — makes the CORRECT password fail, because that path goes
  // through Locker.IsLocked before the password is ever touched.
  r = await call("POST", "/auth/login", null, {
    subject: probeLoginSubject,
    password: probeLoginPassword,
  });
  check(
    "the lock is REAL: the CORRECT password is now 423, not 200",
    r.status === 423,
    brief(r),
  );

  const lockedUntilRow = sql(
    `SELECT locked_until FROM app_user WHERE subject = '${probeLoginSubject}'`,
  );
  check(
    "…and it is DURABLE: app_user.locked_until is set for the probe",
    lockedUntilRow.trim() !== "",
    `locked_until=${lockedUntilRow.trim() || "(null)"}`,
  );

  // NO USER ENUMERATION, pinned as a HARD invariant.
  //
  // This check originally asserted the OPPOSITE — that an unknown subject also
  // reaches 423 on the 5th attempt — and called it a "disclosed asymmetry".
  // Adversarial review showed it was not an asymmetry to disclose but a
  // USER-ENUMERATION ORACLE, readable straight off the wire with no timing
  // measurement: for a REAL account the 423 renders the STORED
  // app_user.locked_until, so `until` is identical on every later request, while
  // for a ghost it was recomputed as now+15m and therefore ADVANCED each time.
  // Six wrong passwords and a clock told you whether an account existed.
  // Locker.RecordFailure now reports only a lock that SetLock actually
  // persisted, so a ghost stays on the same 401 forever.
  const ghost = `login-probe-ghost-${crypto.randomUUID()}`;
  const ghostStatuses: number[] = [];
  const ghostBodies: string[] = [];
  for (let i = 0; i < 6; i += 1) {
    const attempt = await call("POST", "/auth/login", null, {
      subject: ghost,
      password: "wrong",
    });
    ghostStatuses.push(attempt.status);
    ghostBodies.push(JSON.stringify(attempt.body));
  }
  const ghostRows = sql(`SELECT count(*) FROM app_user WHERE subject = '${ghost}'`);
  check(
    "an UNKNOWN subject NEVER reaches 423 — past the threshold it stays on the same " +
      "401 as a wrong password (no enumeration), and is never created",
    ghostStatuses.every((s) => s === 401) &&
      ghostBodies.every((b) => b === ghostBodies[0]) &&
      ghostRows.trim() === "0",
    `statuses=[${ghostStatuses.join(",")}] identical-bodies=${
      ghostBodies.every((b) => b === ghostBodies[0])
    } app_user rows=${ghostRows.trim()}`,
  );

  // -- Self-cleaning ---------------------------------------------------------
  // Hard requirement: this section locks accounts and grants memberships, so it
  // must leave nothing behind. Memberships and the level go through the API (the
  // same endpoints §8 tests); the identities themselves have no delete route, so
  // they go through SQL in FK order.
  const loginProbeUserID = loginBody?.user.id ?? "";
  r = await call("DELETE", `/v1/companies/${companyA}/members/${loginProbeUserID}`, ownerA);
  check("login probe's membership revoked -> 200", r.status === 200, brief(r));

  r = await call("DELETE", `/v1/companies/${companyA}/security-levels/${loginLevelID}`, ownerA);
  check("login probe's security level deleted -> 200", r.status === 200, brief(r));

  // FK order: user_credential and company_membership reference app_user.
  // auth_login_attempt has no FK (it is keyed by subject, and is written for
  // subjects that never existed), so it is cleaned by subject prefix.
  sql(
    `DELETE FROM user_credential WHERE user_id IN
       (SELECT id FROM app_user WHERE subject LIKE 'login-probe-%')`,
  );
  sql(
    `DELETE FROM company_membership WHERE user_id IN
       (SELECT id FROM app_user WHERE subject LIKE 'login-probe-%')`,
  );
  sql(`DELETE FROM company_invite WHERE subject LIKE 'login-probe-%'`);
  sql(`DELETE FROM auth_login_attempt WHERE subject LIKE 'login-probe-%'`);
  sql(`DELETE FROM app_user WHERE subject LIKE 'login-probe-%'`);
  const leftovers = sql(
    `SELECT count(*) FROM app_user WHERE subject LIKE 'login-probe-%'`,
  );
  check(
    "SELF-CLEANING: every login-probe identity, credential and attempt is gone",
    leftovers.trim() === "0",
    `app_user rows matching 'login-probe-%': ${leftovers.trim()}`,
  );

  // =========================================================================
  head("10. CLEANUP — leave the books exactly as they were found");
  // =========================================================================
  // Reversing the probes is itself the last check: only an Owner may delete, and
  // the Trial Balance must return to balance afterwards. The tombstones remain
  // in the append-only edit log, which is correct — the probes DID happen.
  let reversed = 0;
  for (const id of created) {
    const d = await call("DELETE", `/v1/companies/${companyA}/vouchers/${id}`, ownerA);
    if (d.status === 200 || d.status === 204) reversed += 1;
  }
  check(
    "Owner reverses every probe voucher (Owner CAN delete)",
    reversed === created.length && created.length > 0,
    `${reversed}/${created.length} tombstoned`,
  );

  // The probe ledgers carry no balance, but leaving them would litter the chart
  // of accounts on every run. There is no delete-ledger API, so this is SQL.
  const removed = sql(
    `DELETE FROM ledger
      WHERE company_id = '${companyA}' AND name LIKE 'ACL Probe %'
        AND NOT EXISTS (SELECT 1 FROM posting p WHERE p.ledger_id = ledger.id)
      RETURNING name`,
  );
  console.log(`          removed probe ledgers: ${removed.split("\n").filter(Boolean).length}`);

  const tbAfter = await fetch(`${baseUrl}/v1/companies/${companyA}/trial-balance`, {
    headers: { Authorization: `Bearer ${ownerA}` },
  });
  const tbJson = (await tbAfter.json()) as { balanced?: boolean; total_debit?: string };
  check(
    "company A's Trial Balance still balances after cleanup",
    tbJson.balanced === true,
    `balanced=${tbJson.balanced} total_debit=${tbJson.total_debit}`,
  );

  console.log(
    `\n${failures === 0 ? "ALL ACCESS-CONTROL CHECKS PASSED" : `${failures} CHECK(S) FAILED`}` +
      ` (${checks - failures}/${checks})\n`,
  );
  process.exit(failures === 0 ? 0 : 1);
}

main().catch((err: unknown) => {
  console.error("verify-tenancy FAILED:", err instanceof Error ? err.stack : err);
  process.exit(2);
});
