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
 * RUN:  cd frontend && CO="$(cat /tmp/drive-company-id)" pnpm exec tsx scripts/contract-mock-vs-cored.mts
 */
import { createRestClient } from "../src/api";
import { loadCorpus, runRow } from "../src/api/contract/corpus";

const baseUrl =
  process.env.CORED_BASE_URL ??
  process.env.VITE_CORED_BASE_URL ??
  "http://localhost:8087";

const companyID = process.env.CO;
if (!companyID) {
  console.error(
    "contract-mock-vs-cored: CO is required (the seeded company id).\n" +
      '  CO="$(cat /tmp/drive-company-id)" pnpm exec tsx scripts/contract-mock-vs-cored.mts',
  );
  process.exit(2);
}

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
const subject = process.env.E2E_SUBJECT ?? "browser-dev-owner";
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

const corpus = loadCorpus("masters.json");
console.log(
  `\n── D84 · corpus vs LIVE CORED at ${baseUrl} ` +
    `(${corpus.rows.length} rows, stamp ${stamp}) ─────`,
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
