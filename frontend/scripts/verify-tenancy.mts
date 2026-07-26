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

  // Two ledgers on A to post a throwaway journal between.
  const ledgerIDs = sql(
    `SELECT id FROM ledger WHERE company_id = '${companyA}'
      AND name IN ('Cash', 'Capital A/c') ORDER BY name`,
  ).split("\n");
  const ledA1 = ledgerIDs[0];
  const ledA2 = ledgerIDs[1];
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
  // Applied to the Data Entry level on A, exercised, then removed.
  sql(
    `UPDATE security_level SET backdated_cutoff = '2026-07-01'
      WHERE company_id = '${companyA}' AND name = 'Data Entry'`,
  );
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
    sql(
      `UPDATE security_level SET backdated_cutoff = NULL
        WHERE company_id = '${companyA}' AND name = 'Data Entry'`,
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
  head("8. CLEANUP — leave the books exactly as they were found");
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
