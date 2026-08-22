/**
 * D84 · RUNNER B — the same corpus against LIVE CORED.
 *
 * ⛔ WHY TWO RUNNERS AND NOT ONE. Runner A (`src/api/contract/`) proves the MOCK
 * obeys the corpus, and runs in `pnpm vitest`. This one proves the CORPUS obeys
 * CORED, and cannot run in CI because it needs a live server and a seeded tenant.
 * Neither is sufficient: A alone lets the corpus drift away from the server it
 * claims to describe (the corpus would be a second, prettier mock), and B alone
 * never runs often enough to stop the mock loosening again.
 *
 * ⚠️ SO A GREEN RUN HERE IS THE PART THAT DECAYS. Every `expect` in the corpus
 * was measured on the wire the day it was written; this script is how a later
 * session finds out that cored's answer moved. Run it whenever the corpus is
 * edited and whenever `masters.go` or the 422/400 binding in `server.go` is.
 *
 * ⛔⛔ IT CREATES ITS OWN THROWAWAY TENANT AND WILL NOT WRITE INTO YOURS — D91.
 * Until 2026-08-22 this script took the company from `CO`, which every session
 * hands it the DEMO company, and it left its `CT Ledger *` / `CT Control *` /
 * `CT Dup *` fixture parties behind in it. Those parties have no `state_code`,
 * no GSTIN and no bill-by-bill, so they fail three of `verify-demo.mts`'s party
 * checks BY CONSTRUCTION — and cored has no `DELETE /ledgers` to undo them. So
 * one run of this gate permanently broke the other one on the same tenant, and
 * the damage accumulated a run at a time: the demo tenant read 27/34 by the time
 * L15 measured it, against 34/34 on a fresh seed.
 *
 * THE COST WAS NEVER THE FAILING CHECK — IT WAS MISATTRIBUTION. A session that
 * did not re-seed read seven failures, two of them money failures, as a
 * regression in whatever lane it was working on.
 *
 * A fresh company is all this gate ever needed: `seed_company_defaults` gives it
 * the 28 standard groups (so `Sundry Debtors` resolves) and the `Nos` unit (so
 * the two stock-item rows have a unit), which is the entire fixture surface the
 * corpus touches. Nothing here reads a figure, so it has nothing to gain from
 * populated books.
 *
 * ⚠️ IT LEAVES THAT COMPANY BEHIND, and that is the deliberate trade. cored has
 * no company-delete route and `company` has 14 non-cascading FKs, so reaping
 * needs a written script that does not exist yet. One more unreaped tenant among
 * 112+ costs a little `GET /v1/me` latency; poisoning the demo tenant cost a
 * lane. The companies are named `CT Contract <stamp>` so a future reaper can
 * find them.
 *
 * RUN:  cd frontend && pnpm exec tsx scripts/contract-mock-vs-cored.mts
 */
import { createRestClient } from "../src/api";
import { loadCorpus, runRow } from "../src/api/contract/corpus";

const baseUrl =
  process.env.CORED_BASE_URL ??
  process.env.VITE_CORED_BASE_URL ??
  "http://localhost:8087";

// ⛔ A STAMP PER RUN. This runs against a PERSISTENT database, so every row that
// creates a master would collide with its own previous run — and
// `ledger-duplicate-name` would then pass for the wrong reason (colliding with
// last run's row rather than with its own setup).
const stamp = Math.random().toString(36).slice(2, 7);

/**
 * ⛔ MINT FOR THE **SEEDED OWNER**, ONCE, AND NOT THROUGH `authHeaders()`.
 * `auth.mts`'s helper mints for a fresh random subject (`e2e-owner-<uuid>`) —
 * correct for a script that creates its own company, wrong here: this script is
 * handed an EXISTING tenant, and a stranger is not a member of it. The first
 * version of this file called `authHeaders()` with no argument and every row
 * reported `401 missing bearer token`, which the runner correctly refused to
 * read as "cored refuses this body" — it said the CORPUS does not describe
 * cored, which is what a 401 on every row actually means.
 *
 * ⚠️ ONE TOKEN FOR THE RUN, not one per request: `mintOwnerToken` is a POST, and
 * a fresh mint per row is 11 needless round trips plus 11 more identities in a
 * dev database that already has 112+ companies nobody reaps.
 */
// ⛔ A FRESH RANDOM SUBJECT, NOT THE SEEDED OWNER — and that is the opposite of
// what this file used to say, because what it does changed. Minting for
// `browser-dev-owner` was right while the script was handed an EXISTING tenant
// (a stranger is not a member of one). Now it CREATES the tenant, so the
// create-on-first-login path materialises the identity and
// handleCreateCompany seeds it as Owner — and using the demo owner instead
// would only tie these throwaway companies to the account a human logs in as,
// putting them in that operator's company switcher forever.
const subject = process.env.E2E_SUBJECT ?? `ct-owner-${stamp}-${Math.random().toString(36).slice(2, 8)}`;
const minted = await fetch(`${baseUrl.replace(/\/$/, "")}/auth/token`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ subject, amr: ["webauthn"] }),
});
if (!minted.ok) {
  console.error(
    `contract-mock-vs-cored: could not mint a token for ${subject}: ` +
      `${minted.status} ${await minted.text()}\n` +
      `  Is cored running with AUTH_DEV_TOKEN_ENABLED=true?`,
  );
  process.exit(2);
}
const token = ((await minted.json()) as { token: string }).token;

// ⚠️ THE OPTION IS `headers`, NOT `getAuthHeaders`. `HttpClientOptions` merges a
// STATIC header map into every request; there is no per-request hook, which is
// the other reason to mint once. A `getAuthHeaders` key is silently ignored —
// TypeScript did not catch it because the options type is a union and the object
// still matched the `{ http }` arm's absence loosely — and every row came back
// `401 missing bearer token`.
const client = createRestClient({
  baseUrl,
  headers: { Authorization: `Bearer ${token}` },
});

// ⛔ THE THROWAWAY TENANT. Created through the product's own route, so it is
// seeded exactly as a real company is — 28 groups, the `Nos` unit, the
// `Main Location` godown — rather than by an INSERT this script would have to
// keep in step with the migrations.
const companyName = `CT Contract ${stamp}`;
const created = await fetch(`${baseUrl.replace(/\/$/, "")}/v1/companies`, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    Authorization: `Bearer ${token}`,
  },
  body: JSON.stringify({
    name: companyName,
    fy_start_date: "2026-04-01",
    base_currency: "INR",
    state_code: "29",
  }),
});
if (!created.ok) {
  console.error(
    `contract-mock-vs-cored: could not create its own tenant: ` +
      `${created.status} ${await created.text()}`,
  );
  process.exit(2);
}
const companyID = ((await created.json()) as { company_id: string }).company_id;

// ⚠️ SAY SO WHEN `CO` IS SET RATHER THAN SILENTLY IGNORING IT. Every existing
// invocation of this gate passes CO=..., and a caller who is not told their
// argument was dropped will assume the run measured the tenant they named.
if (process.env.CO && process.env.CO !== companyID) {
  console.log(
    `\n  ⚠️ CO=${process.env.CO} was IGNORED. This gate creates its own tenant ` +
      `(D91): it leaves fixture parties behind, and in the demo company those ` +
      `permanently fail verify:demo's party checks.`,
  );
}

const corpus = loadCorpus("masters.json");
console.log(
  `\n── D84 · corpus vs LIVE CORED at ${baseUrl} ` +
    `(${corpus.rows.length} rows, stamp ${stamp}, own tenant "${companyName}" ${companyID}) ─────`,
);

let diverged = 0;
for (const row of corpus.rows) {
  const wanted = row.expect.accepted
    ? "accepted"
    : `refused ${row.expect.status} "${row.expect.sentence}"`;
  const outcome = await runRow(client, companyID, row, stamp);
  if (outcome.divergence === "") {
    console.log(`  ✓ ${row.name.padEnd(34)} ${wanted}`);
    continue;
  }
  diverged += 1;
  console.log(`  ✗ ${row.name.padEnd(34)} corpus says ${wanted}`);
  console.log(`      CORED  ${outcome.got}${outcome.message ? ` — ${outcome.message}` : ""}`);
  console.log(`      ⛔ ${outcome.divergence}`);
}

if (corpus.deliberate_divergence.length > 0) {
  console.log(`\n  DECLARED DIVERGENCES (not gaps — decisions):`);
  for (const d of corpus.deliberate_divergence) {
    console.log(`    · ${d.what}`);
  }
}

console.log(
  diverged === 0
    ? `\n${corpus.rows.length}/${corpus.rows.length} rows: the corpus describes cored.\n`
    : `\n⛔ ${diverged} of ${corpus.rows.length} rows DO NOT describe cored. ` +
        `Fix the corpus (cored moved) — do NOT loosen the mock to match it.\n`,
);
process.exit(diverged > 0 ? 1 : 0);
