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
 * NO SQL, AS OF THE ADMIN-API PASS
 * --------------------------------
 * This script used to shell out to `psql` to grant `demo-data-entry` the Data
 * Entry level, because no HTTP route could create a membership. It now uses the
 * real admin surface, which is the flow a customer would use:
 *
 *   1. the Owner INVITES a subject at a named security level
 *        POST /v1/companies/{A}/members/invites
 *   2. the invitee CLAIMS it on their next authenticated call
 *        GET  /v1/me
 *   3. the Owner reads the membership back
 *        GET  /v1/companies/{A}/members
 *
 * An administrator never writes another person's identity row: step 1 records an
 * invite addressed to a JWT subject, and step 2 is the invitee materialising
 * their own membership. If this seeder ever needs psql again, that is a signal
 * the admin API has a hole.
 */

import { createRestClient } from "../src/api";
import type { CoredClient, VoucherRequest } from "../src/api";

const baseUrl = process.env.CORED_BASE_URL ?? "http://localhost:8087";
const uiUrl = process.env.UI_BASE_URL ?? "http://localhost:5173";

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
 * Raw JSON call against cored. The typed client covers the accounting surface;
 * the admin surface (members, invites, security levels) is driven directly so a
 * non-2xx is visible here rather than thrown from inside a wrapper.
 */
async function api<T>(method: string, path: string, tok: string, body?: unknown): Promise<T> {
  const res = await fetch(`${baseUrl}${path}`, {
    method,
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${tok}` },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status} ${text}`);
  return (text ? JSON.parse(text) : {}) as T;
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
  // Through the admin API, exactly as a customer would: the Owner invites a
  // subject at a named level, and the invitee claims it themselves.
  const tokOwnerA = await token(SUBJ_OWNER_A);

  const existing = await api<{ members: { subject: string; security_level_name: string }[] }>(
    "GET",
    `/v1/companies/${companyA}/members`,
    tokOwnerA,
  );
  if (!existing.members.some((m) => m.subject === SUBJ_DATA_ENTRY_A)) {
    const invite = await api<{ invite_id: string }>(
      "POST",
      `/v1/companies/${companyA}/members/invites`,
      tokOwnerA,
      {
        subject: SUBJ_DATA_ENTRY_A,
        email: "dataentry@demo.local",
        display_name: "Demo Data Entry",
        security_level_name: "Data Entry",
      },
    );
    console.log(`\n  INVITED ${SUBJ_DATA_ENTRY_A} to A as Data Entry (invite ${invite.invite_id})`);

    // The invitee claims it. GET /v1/me is authn-only precisely so an invited
    // user — not yet a member of anything — can reach it.
    const claimed = await api<{ claimed_invites: { company_id: string }[] }>(
      "GET",
      "/v1/me",
      await token(SUBJ_DATA_ENTRY_A, ["pwd"]),
    );
    console.log(`  CLAIMED by ${SUBJ_DATA_ENTRY_A}: ${claimed.claimed_invites.length} invite(s)`);
  } else {
    console.log(`\n  ${SUBJ_DATA_ENTRY_A} is already a member of A — nothing to invite`);
  }

  console.log(
    `\n  IDENTITIES\n` +
      `    ${SUBJ_OWNER_A.padEnd(20)} Owner      on A   (full access; requires WebAuthn for writes)\n` +
      `    ${SUBJ_DATA_ENTRY_A.padEnd(20)} Data Entry on A   (may enter vouchers; may NOT delete or create masters)\n` +
      `    ${SUBJ_OWNER_B.padEnd(20)} Owner      on B   (no membership on A at all)`,
  );

  // Prove the memberships landed, reading them back through the admin API —
  // each company read by ITS OWN owner, since neither may read the other's.
  console.log(`\n  MEMBERSHIPS (read back through GET /members)`);
  for (const [label, company, tok] of [
    ["A", companyA, tokOwnerA],
    ["B", companyB, tokOwnerB],
  ] as const) {
    const { members } = await api<{
      members: { subject: string; security_level_name: string }[];
    }>("GET", `/v1/companies/${company}/members`, tok);
    for (const m of members) {
      console.log(`    ${m.subject.padEnd(20)} -> company ${label} [${m.security_level_name}]`);
    }
  }

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
