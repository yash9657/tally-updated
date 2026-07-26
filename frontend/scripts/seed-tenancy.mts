/**
 * SEED THE MULTI-TENANT / MULTI-USER DEMO.
 *
 *   cd frontend && pnpm seed:tenancy
 *
 * The single-company demo (`pnpm seed:demo`) shows the ACCOUNTING. This one
 * shows the SaaS: two companies that cannot see each other, and two users on the
 * same company with different powers.
 *
 * WHAT IT BUILDS
 * --------------
 *   Company A — whatever `pnpm seed:demo` last built (passed as argv[2]), the
 *               full Demo Traders books.
 *   Company B — "Sharma Distributors", a SEPARATE tenant with its own small set
 *               of books, owned by a DIFFERENT user.
 *
 *   owner@a       (subject "browser-dev-owner")  Owner      on A
 *   dataentry@a   (subject "demo-data-entry")    Data Entry on A   <- new
 *   owner@b       (subject "sharma-owner")       Owner      on B
 *
 * With that in place, every allow/disallow path in the PDP is reachable:
 * an Owner write, a stepped-down Owner write without WebAuthn, a Data Entry
 * write that is allowed, one that is denied, and a cross-tenant read that is
 * refused. `pnpm verify:tenancy` exercises all of them.
 *
 * A CAVEAT, STATED PLAINLY
 * ------------------------
 * There is NO admin API for users, roles or memberships. `POST /v1/companies`
 * makes its creator an Owner and that is the only membership any HTTP route can
 * create. So granting `demo-data-entry` the Data Entry level on company A is
 * done with direct SQL below. That is a real product gap, not a shortcut for the
 * demo: a SaaS needs "invite a user, assign a role" as a first-class flow.
 */

import { execFileSync } from "node:child_process";
import { createRestClient } from "../src/api";
import type { CoredClient, VoucherRequest } from "../src/api";

const baseUrl = process.env.CORED_BASE_URL ?? "http://localhost:8087";
const uiUrl = process.env.UI_BASE_URL ?? "http://localhost:5173";

/** How the seeder reaches Postgres for the membership grant (see caveat above). */
const PG_CONTAINER = process.env.PG_CONTAINER ?? "tally_pg";
const PG_USER = process.env.PG_USER ?? "postgres";
const PG_DB = process.env.PG_DB ?? "tally";

const SUBJ_OWNER_A = "browser-dev-owner";
const SUBJ_DATA_ENTRY_A = "demo-data-entry";
const SUBJ_OWNER_B = "sharma-owner";

const money = (n: number): string => n.toFixed(4);

/**
 * Mint a token for a subject. `amr` carries the authentication methods; passing
 * ["webauthn"] is what satisfies a level with requires_webauthn, and OMITTING it
 * is how the step-up denial is demonstrated.
 */
async function token(subject: string, amr: string[] = ["webauthn"]): Promise<string> {
  const res = await fetch(`${baseUrl}/auth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject, amr }),
  });
  if (!res.ok) {
    throw new Error(
      `mint token for ${subject}: ${res.status} ${await res.text()} ` +
        "(is cored running with AUTH_DEV_TOKEN_ENABLED=true?)",
    );
  }
  return ((await res.json()) as { token: string }).token;
}

function clientFor(tok: string): CoredClient {
  return createRestClient({ baseUrl, headers: { Authorization: `Bearer ${tok}` } });
}

/**
 * Run one SQL statement as the DB superuser and return its rows.
 *
 * psql appends a COMMAND TAG line ("INSERT 0 1") after the rows of a
 * `... RETURNING` statement, which is not data — stripped here, or the first
 * caller to read a returned uuid gets "<uuid>\nINSERT 0 1" and a type error.
 */
const COMMAND_TAG = /^(INSERT|UPDATE|DELETE|SELECT|MERGE)\s+\d/;

function sql(statement: string): string {
  let out: string;
  try {
    out = execFileSync(
      "docker",
      ["exec", PG_CONTAINER, "psql", "-U", PG_USER, "-d", PG_DB, "-t", "-A", "-c", statement],
      { encoding: "utf8" },
    );
  } catch (err) {
    throw new Error(
      `SQL failed (is the '${PG_CONTAINER}' container running?): ` +
        (err instanceof Error ? err.message : String(err)),
    );
  }
  return out
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "" && !COMMAND_TAG.test(l))
    .join("\n");
}

async function main(): Promise<void> {
  const companyA = process.argv[2];
  if (!companyA) {
    console.error(
      "usage: pnpm seed:tenancy <companyA-id>\n" +
        "  companyA is the id printed by `pnpm seed:demo` — this script adds a\n" +
        "  second user to it and builds a second, separate company.",
    );
    process.exit(2);
  }

  console.log(`\nMULTI-TENANT DEMO\n  company A: ${companyA} (Demo Traders)`);

  // ---- Company B: a genuinely separate tenant, created by a DIFFERENT user --
  // Creating a company makes its creator an Owner. That is the ONLY membership
  // an HTTP route can establish, which is why owner@b must create B itself.
  const tokOwnerB = await token(SUBJ_OWNER_B);
  const clientB = clientFor(tokOwnerB);

  const b = await clientB.createCompany({
    name: "Sharma Distributors",
    fy_start_date: "2026-04-01",
    books_beginning_from: "2026-04-01",
    base_currency: "INR",
    state_code: "27", // Maharashtra — a different home state from A's Karnataka
    gstin: "27AABCS9876R1Z4",
    mailing_name: "Sharma Distributors",
    address: "22, Lakshmi Road\nShivajinagar",
    city: "Pune",
    pincode: "411005",
    country: "India",
  });
  const companyB = b.company_id;
  console.log(`  company B: ${companyB} (Sharma Distributors, state 27)`);

  // A few vouchers so B's reports are non-empty and OBVIOUSLY different from A's
  // — that difference is what makes a cross-tenant leak visible if one existed.
  const led = async (name: string, group: string, extra: Record<string, unknown> = {}) =>
    (await clientB.createLedger(companyB, { name, group_name: group, ...extra })).ledger_id;

  const bCustomer = await led("Kolhapur Retail", "Sundry Debtors", {
    state_code: "27",
    gstin: "27AAECK1234L1Z9",
    registration_type: "REGULAR",
    maintain_bill_by_bill: true,
  });
  const bSales = await led("Trading Sales", "Sales Accounts");
  const bCgst = await led("Output CGST", "Duties & Taxes");
  const bSgst = await led("Output SGST", "Duties & Taxes");
  const bIgst = await led("Output IGST", "Duties & Taxes");
  const bCapital = await led("Capital A/c", "Capital Account", {
    opening_balance: money(200000),
    opening_dc: "C",
  });
  const bBank = await led("ICICI Bank", "Bank Accounts", {
    opening_balance: money(200000),
    opening_dc: "D",
  });
  void bCapital;
  void bBank;

  // One intra-state sale (27 -> 27 => CGST + SGST).
  const taxable = 40000;
  const tax = (taxable * 18) / 100;
  const body: VoucherRequest = {
    voucher_date: "2026-05-05",
    party: "Kolhapur Retail",
    party_ledger_id: bCustomer,
    narration: "SD-001 — Kolhapur Retail",
    postings: [
      { ledger_id: bCustomer, dr_cr: "D", amount: money(taxable + tax) },
      { ledger_id: bSales, dr_cr: "C", amount: money(taxable) },
      { ledger_id: bCgst, dr_cr: "C", amount: money(tax / 2) },
      { ledger_id: bSgst, dr_cr: "C", amount: money(tax / 2) },
    ],
    bill_allocations: [
      {
        ledger_id: bCustomer,
        ref_name: "SD-001",
        ref_type: "NEW",
        dr_cr: "D",
        amount: money(taxable + tax),
        due_date: "2026-06-05",
      },
    ],
    gst: {
      supplier_state_code: "27",
      place_of_supply_state_code: "27",
      lines: [{ taxable_value: money(taxable), gst_rate_pct: "18", hsn: "6109" }],
      cgst_ledger_id: bCgst,
      sgst_ledger_id: bSgst,
      igst_ledger_id: bIgst,
    },
  };
  await clientB.postSalesVoucher(companyB, body);
  console.log("  company B books: 1 customer, 1 intra-state sale of 40,000 @18%");

  // ---- A second USER on company A, at the Data Entry level -----------------
  // No HTTP route can do this (see the caveat at the top), so it is direct SQL.
  //
  // The user row is created the same way the auth middleware would on first
  // login (create-on-first-login), then given the Data Entry security level that
  // seed_company_auth_defaults already created for company A.
  const userID = sql(
    `INSERT INTO app_user (id, subject, email, display_name)
     VALUES (gen_random_uuid(), '${SUBJ_DATA_ENTRY_A}', 'dataentry@demo.local', 'Demo Data Entry')
     ON CONFLICT (subject) DO UPDATE SET display_name = EXCLUDED.display_name
     RETURNING id`,
  );
  const deLevel = sql(
    `SELECT id FROM security_level
      WHERE company_id = '${companyA}' AND name = 'Data Entry'`,
  );
  if (!deLevel) {
    throw new Error(
      `company ${companyA} has no 'Data Entry' security level — was it created by POST /v1/companies?`,
    );
  }
  sql(
    `INSERT INTO company_membership (user_id, company_id, security_level_id)
     VALUES ('${userID}', '${companyA}', '${deLevel}')
     ON CONFLICT (user_id, company_id)
     DO UPDATE SET security_level_id = EXCLUDED.security_level_id`,
  );

  console.log(
    `\n  IDENTITIES\n` +
      `    ${SUBJ_OWNER_A.padEnd(20)} Owner      on A   (full access; requires WebAuthn for writes)\n` +
      `    ${SUBJ_DATA_ENTRY_A.padEnd(20)} Data Entry on A   (may enter vouchers; may NOT delete or create masters)\n` +
      `    ${SUBJ_OWNER_B.padEnd(20)} Owner      on B   (no membership on A at all)`,
  );

  // Prove the memberships landed, reading them back independently.
  const rows = sql(
    `SELECT u.subject || ' -> ' || c.name || ' [' || sl.name || ']'
       FROM company_membership m
       JOIN app_user u ON u.id = m.user_id
       JOIN company c ON c.id = m.company_id
       JOIN security_level sl ON sl.id = m.security_level_id
      WHERE m.company_id IN ('${companyA}', '${companyB}')
      ORDER BY c.name, sl.name`,
  );
  console.log(`\n  MEMBERSHIPS (read back from the database)`);
  for (const r of rows.split("\n").filter(Boolean)) console.log(`    ${r}`);

  console.log(
    `\n  OPEN A:  ${uiUrl}/?company=${companyA}` +
      `\n  OPEN B:  ${uiUrl}/?company=${companyB}` +
      `\n\n  VERIFY:  pnpm verify:tenancy ${companyA} ${companyB}\n`,
  );
}

main().catch((err: unknown) => {
  console.error("seed-tenancy FAILED:", err instanceof Error ? err.message : err);
  process.exit(1);
});
