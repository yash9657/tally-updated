# D122 — W3 data understanding: the catalogue, the entity facts, and Tier B — what is built, what is owed

**Written:** 2026-09-28, on branch `agentic/w3` (from `session/t32-and-close-7-3` @ `c51c0a0`), **after
the code landed. Every figure below was printed by a command run in this lane**; the command is named
beside it. Where a first measurement disagreed with the first draft, the measurement is what is recorded
(§5 has two such corrections).

Driver: `PLAN-AGENTIC.md` §3 W3. Binding rules: `IMPLEMENTATION_PLAN.md` §1.1 (no new Go module, no new
frontend package), migration block **0096–0099** (`PLAN-TALLY-PARITY.md` §2.5), `PLAN-AGENTIC.md` §2.

⛔ **W3 IS FIVE ENGINEER-WEEKS AND THIS LANE BUILT FOUR SLICES OF IT, COMPLETELY, WITH GATES.** It did
not build embeddings, the cold-start miner, Tier C, the statutory corpus, the LLM narratives, or any
server route. §6 lists every owed item, in the plan's own words, so nobody reads "W3" in a commit log as
"W3 done".

---

## 1. What landed

| Slice | Where | Size (impl / test lines, `wc -l`) |
|---|---|---|
| Semantic catalogue + selector | `backend/internal/catalogue/` (NEW) | 668 / 336 |
| DSL entities `stock_movements`, `bank_statement_lines`, `profile_facts` | `backend/internal/query/whitelist.go`, `dsl.go`, `compile.go` | + `w3_compile_test.go`, `w3_execute_test.go` |
| Entity facts, feature jobs, tenant card, profile tools | `backend/internal/profile/` (NEW) | 2,270 / 1,151 |
| Tier B guarded SQL | `backend/internal/tierb/` (NEW) | 1,614 / 861 |
| Migrations | `0096_entity_facts_and_tenant_profile.sql` (330), `0097_agent_ro_and_published_views.sql` (222) | — |

`git diff c51c0a0 --shortstat`: **40 files changed, 8,436 insertions, 16 deletions.**
`git diff c51c0a0 -- backend/go.mod backend/go.sum | wc -l`: **0**. No frontend package added
(`frontend/src/api/query.ts` gained three union members; that is the only frontend edit).

---

## 2. The semantic catalogue (`internal/catalogue`)

**Every report in `authz.KnownReportNames` has an `Entry`**: title, a description that says what the
report does **not** answer, English synonyms, Hinglish phrasings (Latin script), typical questions, and
example DSL — or a `NoDSLReason`.

Measured (`go test ./internal/catalogue/ -v`):

- `33 reports, 33 catalogue entries`; `13 entries with DSL (6 exact), 20 without`.
- `self-retrieval top-3: 82/82`.
- held-out (40 questions written before any tuning, kept out of `entries.go`): first measured
  `top-1 38/40 (95.0%), top-3 39/40 (97.5%)` — **but five of those questions named the catalogue's own
  example parties** (Kaveri, Bharat Metals, Ramesh, HDFC, Widget are in `entries.go`'s Questions), so
  part of that figure was memorised names (wave-1 review). With those names replaced by ones the index
  has never seen (`heldOutNames`, pinned unseen by `TestHeldOutNamesAreUnseen`) and nothing else
  changed: **`top-1 36/40 (90.0%), top-3 38/40 (95.0%)`**. The two misses, kept and not tuned away:
  `"what do we have to pay Sunrise Steel"` → want `bills-payable`, got `payroll-payslip`,
  `payroll-register`, `gstr3b`; `"items expiring soon"` → want `batch-summary`, got
  `stock-item-vouchers`, `stock-movement`, `reconciliation-queue`. The floor `heldOutTop3Floor` is
  re-pinned at the re-measured **0.95**.
- `lenadari` (receivables) was listed under Bills Payable and `denadari` (payables) nowhere; corrected
  and pinned by `TestReceivableAndPayableWordsPointTheRightWay`.

Decisions:

- **D122-1. The catalogue is keyed BY the vocabulary, not a list of its own.** The completeness test
  fails both ways — a vocabulary report with no entry ("the agent cannot find it") and an entry naming
  no report ("the PDP would refuse it") — and a duplicate entry (which would silently overwrite in the
  lookup map).
- **D122-2. An `"exact"` DSL claim must name its parity test, and the test is FOUND in the tree.**
  `TestEveryExactClaimHasItsParityTest` walks every `_test.go` for `func <Name>(t *testing.T)`. A label
  a model trusts must not outlive the test that earned it. The six: trial-balance
  (`TestExecute_ClosingNetEqualsTrialBalance`), profit-and-loss (`…NetSalesMatchesProfitAndLoss`),
  outstandings and bills-payable (`…OutstandingsMatchesTheOutstandingsReport`), bank-statement and
  stock-movement (this lane's, below).
- **D122-3. Every report resolves to a DISPLAY ActionSpec in the generated registry**
  (`TestEveryReportHasADisplayAction`) — the catalogue can only advertise what the agent can call.
- **D122-4. The catalogue lives outside `internal/query`** (allowed deps `{authz, query, actions}`),
  because `internal/query`'s set must stay `{platform/db}` (HANDOFF-AGENTIC §1.5).
- **D122-5. The selector is BM25, and that is a stage.** No embedding model exists here (no keys, no
  pgvector); W1a's Hinglish eval decides the model. The signature (`Select(question, k) []Match`, with
  the second-hop `Entities`) is what an embedding ranking will fuse into (RRF). Deterministic ties.
  An off-domain question selects **nothing** (`TestSelectorRefusesToGuess`), which is the planner's cue
  to refuse rather than guess.

## 3. The DSL entities (`internal/query`)

- **`stock_movements`** — stockMovementSQL's current-version join, carried over. **No metric may read
  as a valuation** (`TestW3_NoValuedIssueMetric`): `sm.amount` is the line as entered; the cost of an
  issue is the valuer's and SQL cannot replay a valuer. Pinned against `StockMovementReport`
  (`TestExecute_StockMovementsMatchTheStockMovementReport`): per-item in/out/net quantity and inward
  amount, plus absolute literals (Widget 106/50 after a 10→6 ALTER and a deleted 7-unit sale).
- **`bank_statement_lines`** — **the narration is a FILTER and never a DIMENSION**: third-party text
  (a NEFT remark) is where an injection payload arrives (PLAN-AGENTIC §2.4); a filter binds the user's
  own search as `$N` and returns only amounts. Pinned against `ImportedStatementReport`
  (`TestExecute_BankStatementLinesMatchTheImportedStatement`) including an overlapping second download
  (deduplicated at import; a double count would be 1.00 over).
- **`profile_facts`** — the DSL twin of every profile tool (§4).
- `TestW3_EveryFilterOfEveryEntityBindsItsValue` is generic over the whole catalogue:
  **`checked 31 filter columns across 7 entities`**, each hostile literal either bound as an arg or
  refused by its closed type.
- **The nlq `emit_query` enum had silently drifted**: the prompt walks `query.Catalog()` and picked up
  the new entities, the schema's hand-written enum did not, and the schema test compared the enum to a
  hand list that had not moved either — green while the model was forbidden to emit an entity it was
  told about. The enum is now ALSO pinned to `query.Catalog()`; mutation (drop `profile_facts` from the
  enum) → red. Prompt version bumped to **`nlq-dsl-v3`**.

## 4. Entity facts, feature jobs, the tenant card, the tools (`internal/profile`, migration 0096)

**D122-6. A money fact that a report prints is TAKEN FROM THAT REPORT, never re-derived.** A party's
receivable is `reporting.Outstandings`'s; a month's sales is the P&L's `Sales Accounts` node; closing
quantity/value are the Stock Summary's; window in/out quantity the Stock Movement report's. This repo
learned D20 and D51 the hard way: a second SQL derivation of a report figure agrees until it does not.
The jobs compute only what no report prints — lags, percentiles, DSO, drift, velocity, cover,
ABC/XYZ, streams, seasonality.

**D122-7. The fact vocabulary is CLOSED** (`profile.Facts`, 39 keys): each declares its unit and
**exactly one** of `ReconcilesTo` (an `authz` report name) or `Statistic`. `writeFacts` refuses an
undeclared key, a kind mismatch, and a class/number mismatch.

**D122-8. Bi-temporal by copy-on-close.** The only UPDATE the table accepts is `superseded_at` NULL →
time (trigger `w3_only_supersede` compares the whole row as JSON minus that column, so a column added
later cannot be forgotten); closing valid time supersedes the open row and INSERTs a closed copy
`[valid_from, as_of)`. An earlier as-of than the current facts is refused (`ErrAsOfBehind`) —
backfill is owed, not faked.

**D122-9. One transaction, one advisory lock.** `Refresh` reads every report through a runner that
reuses its own transaction and holds `pg_advisory_xact_lock(hashtext('profile.refresh:'||company))`.

The jobs, over a 365-day window ending at `as_of`:

| Job | Facts |
|---|---|
| customers (Sundry Debtors subtree) | receivable (report), invoiced in window (Day Book), DSO, receipt-lag p50/p90/mean, bills settled — lag ends at the settlement that FULLY settles a bill |
| suppliers (Sundry Creditors subtree) | payable (report), purchases (Day Book), payment-lag p50/p90, per-item first/last rate, drift %, amount (Stock Item Vouchers); `supplies_item` edges |
| items | closing qty/value (Stock Summary), in/out qty (Stock Movement), out amount (Stock Item Vouchers), velocity, days of cover, ABC (80/15/5 on out amount), XYZ (CV of monthly out qty, empty months as zero) |
| streams | Payment debit legs / Receipt credit legs per counter-ledger: longest run with every interval within **±3 days** of the median and every amount within **±5%**, **≥3 occurrences**; total (Day Book), median, interval, cadence, next-due |
| tenant | monthly sales/purchases (P&L per month), 3m/12m run-rates over COMPLETE months, seasonality index (≥12 months), receivables/payables totals, voucher count |

**THE GATE** — `TestEveryProfileFactReconcilesToItsReport`, over a seeded FY 2025-26 (`book_test.go`):
`refresh wrote 115 facts, 3 edges, card v1 (271 tokens)`; **`reconciled 55 facts across 16 keys`** —
every stored fact with `reconciles_to` is re-derived from a FRESH run of the named report and compared to
the paisa; the monthly sales are ALSO checked against the DSL's `postings.natural_net` (an independent
SQL path); a `(report, fact)` pair with no reconciler **fails**; every reconcilable vocabulary key must be
exercised by the seed; and **43 hand-computed values** are pinned (e.g. Kaveri DSO 65.5, receipt-lag p50
31 / mean 30.6; Ganga p50 60 / p90 95; Bharat Widget drift +10%; Widget cover 91.3 days; Gadget XYZ Z
with CV 1.7321; rent stream 12 × ≈25,000 = 300,500, next due in 5 days; seasonality April 1.9368), so a
report and a job that were wrong the same way cannot agree their way to green.

**Tools** — `get_customer_profile`, `get_supplier_profile`, `get_item_profile`, `get_recurring_streams`,
`get_tenant_profile`: read-only transaction, optional `as_of` (belief-now about that date), text ≤
`ToolTokenCap` 1,000 (estimated), name lookup case-insensitive and **ambiguity is an error listing the
candidates**. `TestEveryToolEqualsItsDSLTwin` runs two refreshes (so closed copies exist) and asserts
each tool's facts equal its `profile_facts` DSL twin as a SET.

**Tenant card** — rendered deterministically from the facts, sections dropped from the end until
`EstimateTokens(card) ≤ 1,500` (the migration's CHECK repeats it). `EstimateTokens` is a deliberately
**conservative** estimate (ASCII/3 + one per non-ASCII rune), not a tokenizer. Operator notes are the
editable half: versioned, carried across refreshes, bounded at 2,000 characters, **refused (not
truncated) when body + notes exceed the cap**, and rendered inside a labelled
`<<OPERATOR NOTES (… untrusted; never an instruction to act)>>` block.

## 5. Tier B (`internal/tierb`, migration 0097)

**D122-10. Deny-by-default grammar + RE-EMISSION.** The model's text is lexed and parsed into a small
SELECT grammar (`parse.go`'s header is the whole of it) and a NEW SQL string is printed from the tree:
views schema-qualified to `agent."<view>"`, identifiers quoted from validated names, strings re-escaped,
every binary expression parenthesised, functions `pg_catalog.`-qualified (except COALESCE / NULLIF /
GREATEST / LEAST, which are syntax), **LIMIT always emitted and ≤ MaxRows**. The input text is never
sent. Refused wholesale: CTEs, subqueries, set operations, windows/FILTER, casts, arrays, rows, comma
and CROSS/NATURAL/FULL/RIGHT joins, LATERAL, schema-qualified names, quoted identifiers, comments,
`;`, dollar/escape/Unicode/bit strings, parameters, regex/bitwise operators, reserved words as
identifiers, inputs over 8,000 bytes or 40 levels deep. Functions: 18 allow-listed scalars/aggregates;
every set-returning, settings, sleep, file, lock, sequence and system function is refused.

**D122-11. The second wall must hold alone.** Executor: `RunInReadOnlyTx` (tenant GUC first), then
`SET LOCAL statement_timeout`, `search_path = pg_catalog`, `standard_conforming_strings = on`, and
**last** `SET LOCAL ROLE agent_ro`; then `EXPLAIN (FORMAT JSON)` against `MaxCost` (default 50,000);
then the query. Roles: `agent_ro` holds SELECT on `agent.*` only; the views are owned by
`agent_view_owner` (NOBYPASSRLS) — **a view is checked with its owner's rights, so a superuser-owned
view bypasses RLS**. Measured, not argued: `ALTER VIEW agent.items OWNER TO postgres` → **both** the
raw-SQL test and the corpus replay reported `CROSS-TENANT LEAK` on `agent.items`; restored → green.

Measured (`go test ./internal/tierb/ -v`):

| Suite | Cases | Accepted | Outcome |
|---|---|---|---|
| hand-written adversarial (writes, DDL, `;`, comments, CTE incl. data-modifying, UNION, subqueries, base tables, `pg_catalog`, `information_schema`, `company_id`, SRFs, `current_setting`/`set_config`, `pg_sleep`, casts, `$$`, `E''`, `U&''`, quoted identifiers, depth bombs, …) | **218** | 0 | all refused with `ErrRejected` |
| payload injection at every token boundary of 23 valid queries × 25 payloads | **13,450** | 167 | every accepted one held to the 6 re-emission invariants |
| random token soup | **3,000** | 0 | — |
| grammar-shaped random with dangerous splices | **4,000** | 2,018 | every accepted one held to the invariants |
| **DB replay of every accepted query as tenant B, tenant A's books present** | **2,208** | — | **1,056 ran, 1,152 refused by Postgres (type errors, never permission/read-only), 0 leaked**. ⚠️ The first oracle (A's marker string in a result cell) could register a leak in only ~154 queries; the oracle is now DIFFERENTIAL (B's answer before vs after A's books exist), and **834 of the 1,056 that run are sensitive** to A's data. Mutation: `agent.stock_movements` re-owned to `postgres` → 86 leaks flagged, only 17 by the marker. |

The six invariants (`invariantsOf`): no `;`/comment/`$`/`::`/`?` outside strings; every relation is a
published `agent."<view>"`; every function allow-listed; a trailing `LIMIT ≤ maxRows`; no catalog /
setting / role / `company_id` words outside strings; **re-emission is a fixed point** (the printed SQL
re-parses to the identical printed SQL).

Raw, UNVALIDATED SQL as agent_ro (`TestTheSecondWallStandsWithoutTheValidator`): `current_user,
statement_timeout, transaction_read_only` = `agent_ro|5s|on`; nine `public.<table>` reads →
`permission denied`; five writes/DDL refused; tenant B over all 8 views sees none of A's marker, while A
sees its own in all 8 (so the probe is not vacuous); the bank narration is unreachable even to A.
Also: the cost gate refuses a priced join at `MaxCost: 10`; `LIMIT 100000` with `MaxRows: 3` returns
exactly 3 rows and the SQL ends `LIMIT 3`; views equal `tierb.Views` column-for-column as agent_ro reads
`information_schema`.

⛔ **THE RESIDUAL, MEASURED AND STATED.** The tenant GUC is a session setting any role may change.
RAW SQL that knew another company's id **did** re-scope itself
(`RESIDUAL CONFIRMED: raw SQL with set_config re-scoped to tenant A`). The second wall therefore stops
base-table reads, writes, catalog-row access and default cross-tenant reads — **but not GUC tampering;
the validator is load-bearing for that one**: `set_config` / `current_setting` are not allow-listed and
`SET` is not a SELECT (both pinned, hermetic and against the DB). Hardening options (owed, §6): revoke
`set_config` from PUBLIC with explicit grants to the app roles, or an RLS predicate keyed to a value
agent_ro cannot write.

## 6. ⛔ OWED — every W3 deliverable this lane did not build

1. **Embeddings** — `halfvec(1024)` tables, HNSW, tenant-hash partitions, FORCE RLS, `pg_trgm` lexical;
   narrations, bank lines, party aliases, items, document chunks, catalogue, golden queries. The compose
   image is `postgres:16` without pgvector; switching needs a separate container (not the shared
   `tally_pg`). **0098–0099 are reserved for this.** The Hinglish embedding eval (W1a) decides the model.
2. **The embedding half of the CHESS selector** and the **`emb_golden_query(tenant, question, dsl,
   accepted)`** few-shot store.
3. **The W3 accuracy gate** "NL query accuracy with catalogue + selector vs without (≥ +15 pt)" — needs
   W1's suite and a provider; the catalogue is not yet in the nlq prompt (the translator still reads
   `query.Catalog()` only).
4. **More DSL entities**: GST returns, orders, cost-centre allocations (the catalogue's `NoDSLReason`
   says so on each report).
5. **Cold-start miner** on Tally import (ledger-mapping habits, process chains, books style).
6. **Supplier lead time** (needs PO → GRN linkage), **GSTR-2B regularity**, **item margin**.
7. **≤400-token LLM narratives** for changed entities (Tier-B model, batch) — belongs in a package that
   may import `internal/llm`; `internal/profile` may not (archtest).
8. **Scheduling**: nothing calls `profile.Refresh` nightly (River job / poller), and history backfill
   (`ErrAsOfBehind`) is refused rather than supported. Refresh runs READ COMMITTED: a voucher posted
   mid-refresh can be seen by a later report and not an earlier one; the gate is asserted on a
   quiescent book.
9. **Routes and UI**: no server route reads or edits the tenant card, serves the catalogue, or runs
   Tier B; W2's agent registers the tools (`profile.Tool*` names are exported constants for it). When
   the card edit route arrives it must be a **bare token** (`agent-profile`), per the 0091 row.
10. **Tier C** (gVisor sandbox over a nightly per-tenant Parquet extract), the **statutory corpus**, and
    **Splink** party dedup / bank ↔ ledger linkage.
11. **Devanagari** phrasings in the catalogue; **FakeTranslator** phrases for the three new entities.
12. **GUC-tamper hardening** for Tier B's raw path (§5 residual).

## 7. Mutations run (each observed red, then restored)

| Mutation | Caught by |
|---|---|
| drop `vh.current_version_id` join from `stock_movements` | `TestW3_EntitiesCarryTheirGuards` + parity test (DSL 116/50/66 vs report 106/50/56) |
| blank `stock_movements` `vh.is_deleted` guard | ⚠️ **NOT caught, and correctly**: a DELETE's tombstone carries no movement rows and is the current version; recorded in the test's comment instead of claimed |
| a self-join on `line_hash` in the bank skeleton | ⚠️ not caught — dedupe at import makes it a no-op; replaced by a real mutation ↓ |
| bank skeleton drops one date's lines | parity test (70000.5 vs 70001.5; 4 vs 5 lines) |
| `profile_facts` enum dropped from `emit_query` | `TestSchemaEnumsMatchDSLConstants` (both oracles) |
| rename a catalogue entry / cite a nonexistent parity test / non-compiling example | the three completeness tests |
| remove the advisory lock in `Refresh` | `TestConcurrentRefreshesSerialise` (`duplicate key … entity_facts_one_current`) |
| `now()` for `superseded_at` (the original code) | the same test — **found a real bug**: a waiter's `now()` predates the holder's `recorded_at`; fixed with `clock_timestamp()` |
| drop the closed-copy INSERT | `TestBiTemporalHistoryIsKept` |
| drop `valid_to IS NULL` from `profile_facts` | `TestEveryToolEqualsItsDSLTwin` (every value doubled) |
| Outstandings as-on moved to 2026-03-10 | reconciliation gate + hand-computed values |
| a `gst_output_window` fact reconciling to `gstr1` with no reconciler | "NO reconciler for that pair" + "never reconciled" |
| `pg_sleep` / `current_setting` allow-listed | 567 ESCAPE lines |
| view allow-list check disabled | ESCAPE on `ledger`, `voucher_version`, `pg_class` … |
| printer drops the `agent.` qualification | invariant 2 (2,302 failures) |
| `_ "net/http"` in `tierb/exec.go` | `TestW3PackagesAreNetworkFree` |
| `agent.items` owned by `postgres` (live DB) | CROSS-TENANT LEAK in both DB suites |

## 8. ⛔ An incident, and the two harnesses it exposed

**This lane's unmerged 0096 — and later 0096 + 0097 — landed in the SHARED `tally` database**, despite
every command pointing `ADMIN_DATABASE_URL` / `TESTDB_ADMIN_DSN` at `tally_w3`:

1. `migrations/master_name_index_census_test.go` **hardcoded** the tally DSN and calls `Apply`.
2. `internal/server/einvoice_http_test.go` **ignored `CORED_SKIP_DB`** (it checked only
   `testing.Short()`), so a "hermetic" `CORED_SKIP_DB=1 go test ./...` migrated `tally` too.

Both times the W3 objects were dropped from `tally` by hand (the tables were empty; the views, grants
and `schema_migrations` rows removed; the cluster-wide roles left, since they cannot log in).
`schema_migrations` in `tally` now carries no 0096/0097. Both harnesses are fixed on this branch
(env-honouring DSN; a `CORED_SKIP_DB` skip), and `internal/server/server_test.go`'s DSNs honour the
env too. ⚠️ **`0100_voucher_idempotency` and `0101_bank_statement_proof_and_narration` (W5's block)
are also applied in `tally`** (19:22 UTC) — very likely the same harnesses, from the W5 lane; they
were **not** touched here. ⚠️ 22 other test files still hardcode the tally DSN; the full suite was run
against `tally_w3` by repointing them temporarily (restored; not committed).

## 9. Gate figures at the end of the lane

| Gate | Command | Result |
|---|---|---|
| build / vet | `go build ./... && go vet ./...` | clean |
| no new deps | `git diff c51c0a0 -- backend/go.mod backend/go.sum \| wc -l` | **0** |
| full backend suite on the lane DB | `go test ./... -count=1` (all hardcoded DSNs repointed to `tally_w3`) | **42 packages ok, 0 FAIL** |
| hermetic suite | `CORED_SKIP_DB=1 go test ./...` | all ok except the 4 pre-existing GSTR-3B pins in `internal/server` that **refuse** to run under `CORED_SKIP_DB` by design |
| Tier 0 | `make tier0` | ok — `replayed 3 interactions across 2 cassettes` (unchanged; W3 added no cassette) |
| frontend types | `pnpm exec tsc --noEmit` | exit 0 |
| frontend query tests | `pnpm exec vitest run QueryScreen query` | `Test Files 1 passed (1)`, `Tests 11 passed (11)` |
| corpora | `python3 corpora/validate.py` | OK, counts unchanged |
| insightd | — | not touched by W3; not run |


---
---

# Wave 2 — W3 follow-ons + security (branch `agentic/w3-followons`)

**Written:** 2026-09-28, on `agentic/w3-followons` from `session/t32-and-close-7-3` @ `4e7cfff`, after
the code landed. **Every figure below was printed by a command run in this lane**, against the lane's
own database `tally_w2_w3f` on the shared cluster (`tally_pg`, postgres:16, port 5433) or on a
separate pgvector container (`tally_pg_vec`, pgvector/pgvector:pg16, port 5434, 512 MB, removed at
the end). The shared `tally` database and the `tally_pg` container were not touched.

Binding rules held: `git diff 4e7cfff -- backend/go.mod backend/go.sum frontend/package.json
frontend/pnpm-lock.yaml insightd/requirements.txt | wc -l` = **0**. Migrations used: **0098, 0099**
(this lane's block). No ACL rule was written and the seeder census is unchanged (17 PERFORMs).
`internal/agent` and `internal/policy` were not modified.

| # | Priority (lane brief) | Status |
|---|---|---|
| W2-1 | ⛔ Close the Tier B GUC residual | **DONE**, and a second, worse hole found and closed with it (§W2-1) |
| W2-2 | Migrate lock (wave-1 §4.1) | **DONE** (§W2-2) |
| W2-3 | Catalogue into the nlq prompt | **DONE**, prompt `nlq-dsl-v4` (§W2-3) |
| W2-4 | Embeddings: pgvector image, halfvec tables, pg_trgm, provider path, golden queries | **DONE** for the store, the provider path and the golden-query store; partitioning and the model choice are owed (§W2-4) |
| W2-5 | More DSL entities; supplier lead time / GSTR-2B facts | **Supplier lead time DONE.** New DSL entities **NOT built, on purpose** — a security finding blocks them (§W2-5). GSTR-2B regularity has no data to read (owed) |

---

## W2-1. Tier B: the database alone now keeps raw SQL in its tenant (migration 0099)

### What 0097 left open — measured before any change

A throwaway probe ran raw SQL through the wave-1 path (`tierb.RunRawAsAgent`, the executor minus the
validator) as tenant B, with tenant A's books present:

| Raw SQL as "agent_ro" | Result on 0097 |
|---|---|
| `SELECT set_config('role','tally_app',true), current_user` | `[[tally_app tally_app]]` — **the role switch undone** |
| `SELECT set_config('role','tally_app',true), set_config('app.current_company_id','<A>',true), query_to_xml('select name, company_id from public.ledger',true,false,'')::text` | **tenant A's ledger names, from the BASE table** (marker found) |
| `SELECT count(*) FROM (SELECT set_config('app.current_company_id','<A>',true)) s, agent.items` | non-zero (the pinned wave-1 residual) |
| `SELECT * FROM public.auth_session_state('x')` | ran (0074 never revoked PUBLIC EXECUTE) |

So there were **two** holes, and the second was not in the wave-1 record: `SET LOCAL ROLE agent_ro` is
not a boundary against SQL that runs after it, because `SET ROLE` is checked against the SESSION user
(`tally_app`), and `set_config('role', …)` is `SET ROLE` in function form. Once back as `tally_app`,
the SQL could re-point the GUC and read any base table, narrations included.

### D122-12. The design: two properties the database enforces by itself

**(a) The untrusted text runs only inside a SECURITY DEFINER frame.** `agent.tier_b_run(sql, max_rows)`
and `agent.tier_b_plan(sql)` are SECURITY DEFINER, owned by `agent_ro`, and run the text with
`FOR r IN EXECUTE`. Inside a security-definer function Postgres refuses to change `role` or
`session_authorization` at all (verified: `cannot set parameter "role" within security-definer
function`), and a cursor loop refuses anything that is not a row-returning query (`cannot open SET /
RESET / DO / COPY / PREPARE query as cursor`, `cannot open multi-query plan as cursor`). The Go side
sends the text to Postgres in exactly two ways: as the `$1` of those functions, and as a
Parse/Describe (`tx.Conn().Prepare`) to learn the result columns' names and types — which executes
nothing. A bind parameter also keeps the text out of `pg_stat_activity`.

**(b) The tenant is bound by a row agent_ro cannot write.** `agent.tier_b_binding (backend_pid, xid8)
-> company` is written by `tally_app` through `agent.tier_b_bind()` at the top of the Tier-B
transaction — BEFORE `SET LOCAL transaction_read_only = on` and before any untrusted text exists in
it. It takes no argument (it binds the company the transaction's GUC already names), the primary key
makes a second bind in one transaction a duplicate-key error, and the transaction is always rolled
back (`errRollback`), so the row never commits. A **RESTRICTIVE** policy `tier_b_bound` on each of the
16 base tables the views read, `TO agent_view_owner` only, ANDs `company_id = (SELECT
agent.tier_b_company())` onto the permissive GUC policy. Re-pointing the GUC now yields
A's rows ∩ B's binding = nothing; an unbound transaction reads nothing; `tier_b_run` refuses to start
unbound or writable (loud, never an empty answer).

The executor's order is load-bearing and is in `tierb/exec.go`'s header: bind (must write, trusts
the GUC only because nothing untrusted has run) → READ ONLY (cannot be undone after a query) →
timeouts / search_path → `SET LOCAL ROLE agent_ro` → Describe → plan → run.

**Rejected alternatives** (0099's header has the same list):
- `REVOKE EXECUTE ON set_config FROM PUBLIC` — `SET` and `RESET` are statements and stay open;
  every app transaction uses set_config. Closes neither hole.
- `GRANT SET ON PARAMETER` — only restricts SUSET parameters; a placeholder GUC is USERSET for all.
- A LOGIN role for Tier B (session user = agent_ro) — kills the role escape, but adds a credential to
  provision and rotate and a second pool, and still needs (b). (a) gives the same property with no
  credential.
- Making `app_current_company_id()` honour a binding — every RLS check in the product would pay a
  lookup. The restrictive policy is scoped to `agent_view_owner`; no other role's plan changes.

**Also closed:** 0074's `auth_session_state()` and `auth_admin_clear_lock()` are SECURITY DEFINER,
superuser-owned, and were EXECUTE-able by PUBLIC (a lock-state read by subject, and a
company-membership oracle through clear_lock's boolean). 0099 revokes PUBLIC; `tally_app` keeps its
explicit grant. `grep` shows only cored's `tally_app` paths call them.

### The gates (`go test ./internal/tierb/ -v`, both clusters)

| Test | Measured |
|---|---|
| `TestRawSQLCannotLeaveItsTenant` (the inverted residual pin) | "raw tamper suite: **22 attacks refused for the named reason, 6 ran and returned only tenant B's own answer (each with a live control), 8 views differential**". Refused: SET / SET LOCAL / RESET / RESET ALL / RESET ROLE / SET ROLE; set_config of `role` (alone, with the GUC + base-table `query_to_xml`, to `none`) and of `session_authorization`; DO; two statements; turning READ ONLY off; calling `tier_b_bind`, reading or writing `tier_b_binding`; base table via `query_to_xml`; both 0074 functions; `pg_read_file`; PREPARE; COPY. Ran: set_config GUC (local and session-wide) joined to a view, GUC then `query_to_xml` over a view, a nested `tier_b_run` under a tampered GUC, an empty GUC. Every running attack is re-run **as A aimed at A** and must find A's marker/rows (the control). Per view, B re-pointed at A counts **0** rows while A counts rows in all 8 |
| `TestTheBindingCannotBeMovedOrSkipped` | second bind (even after re-pointing the GUC) → `tier_b_binding_pkey`; bind in a READ ONLY tx refused; `tier_b_run` unbound → "no tenant binding", writable → "must be READ ONLY"; unbound view read as A → 0 rows; **0** binding rows left after a call |
| `TestNoSecurityDefinerDoorForAgentRo` | "security-definer functions agent_ro may execute: **[agent.tier_b_company agent.tier_b_plan agent.tier_b_run]**; **16** base tables behind the views, all with tier_b_bound" |
| `TestOtherSessionsQueryTextIsHidden` | a concurrent `tally_app` session's SQL is visible to the admin (control) and **not** to agent_ro |
| `TestStatementTimeoutCannotBeTalkedAway` | `set_config('statement_timeout','0',true), pg_sleep(30)` "stopped after **5s** by … canceling statement due to statement timeout" — the timer is armed at statement start, so text cannot talk it away inside its one statement; the Go deadline (timeout + 2 s) is the backstop |
| `TestJSONDecodeRendersLikeTheNativeDriver` | "render parity over the accepted corpus as tenant A: **1054 identical, 1154 refused by both, 0 differ**" |
| `TestAcceptedAdversarialQueriesLeakNothing` (wave-1 gate, now through the new path) | "2208 queries, **1055** ran as tenant B, 1153 refused by Postgres (type errors), **0 leaked** (0 by marker); **834** of the 1055 that ran would register a leak" (1056/1152 on another run of the same code — one data-dependent type error; it was 1056 at the wave-1 merge) |

**Mutations run (each observed red, then restored):**

| Mutation | Caught by |
|---|---|
| drop `tier_b_bound` on `ledger` and `stock_item` | `CROSS-TENANT LEAK (set_config GUC session-wide…)`: `[[1]] vs untampered [[0]]`; `agent.items` "B re-pointed at A counts 1 rows, want 0"; census "WITHOUT the restrictive tier_b_bound policy: [ledger stock_item]". (Dropping it on `ledger` alone did not leak `agent.ledgers`, because the view's `account_group` join was still bound — the census is what names it.) |
| `ALTER FUNCTION agent.tier_b_run … SECURITY INVOKER` | 4 attacks stop being refused: `set_config('role','tally_app')` → `[[tally_app tally_app]]`; role + GUC + `query_to_xml('… public.ledger')` returned **A's base-table ledger names**; `role` → none; `session_authorization` → tally_app |
| executor skips `tier_b_bind()` | every call fails loudly: "tier_b_run: no tenant binding in this transaction (call agent.tier_b_bind() first)" / "tier_b_plan: no tenant binding" |
| `GRANT EXECUTE ON FUNCTION auth_session_state(TEXT) TO PUBLIC` | census "[… public.auth_session_state], want exactly […] — a new one needs REVOKE ALL … FROM PUBLIC"; the attack is no longer refused |

**Found on the way — a render bug in the pre-0099 path.** The oracle for the JSON decode first
disagreed on 2 of 2,208 queries: `date_trunc('month', voucher_date)` rendered April as **2025-03-31**
through the old native path, because pgx hands a `timestamptz` back in the GO PROCESS's zone (the
host was PDT, the session UTC). The new path renders in the session's zone (2025-04-01). The oracle
(`native_export_test.go`, test-only) now converts to the session zone, and the parity is 0 differ.

**Residuals that remain (stated, not claimed away):** timing/row-count side channels within one's own
tenant are not a cross-tenant channel; a CPU-heavy query can still use its 5 s; `TEMP` on the database
is still granted to PUBLIC but every DDL is refused in a READ ONLY transaction; the Parse/Describe of
the untrusted text runs outside the definer frame (it executes nothing — no planning, no function
call); any NEW security-definer function added to `public` without `REVOKE … FROM PUBLIC` is caught by
the census test only on a database the test runs against.

## W2-2. The migrate lock (HANDOFF-AGENTIC-WAVE-1 §4.1)

`migrations.Apply` now holds `pg_advisory_lock(0x4D47, 1)` (a two-int key, disjoint from durable's
bigint keys and intentlog's `(0x1A7E, …)`) from before `schema_migrations` is created until it
returns, and reads the applied set under it; the unlock runs on a background context. Advisory locks
are per database, so lanes on other databases never wait.
`TestConcurrentAppliesOnAnEmptyDatabase` creates a throwaway database and races 4 appliers: "**4
concurrent appliers on an empty database: 0 failed, 79 migrations recorded once each**" (80 with
0098). Mutation — the lock replaced by `SELECT $1::int`: "applier 1 of 4 … duplicate key value
violates unique constraint "pg_type_typname_nsp_index"", 3 of 4 failed.

## W2-3. The semantic catalogue is in the NL→DSL prompt (`nlq-dsl-v4`)

- **Static (cached, byte-identical across calls AND questions):** an ENTITY GUIDE rendered from
  `catalogue.Entities()` (description, synonyms, Hinglish words per DSL entity), and rule 11 — hints
  are keyword-selected shapes; replace an example's party name with the question's; a "the DSL cannot
  answer this report" hint means refuse (R4). Static grew to 8,244 bytes.
- **Dynamic (per question):** a new system message, CATALOGUE HINTS — the top-3 reports
  `catalogue.Select` ranks, each with title, description, DSL entities and one example rendered
  exactly as the `emit_query` schema demands (every property present, arrays never null), or the
  `NoDSLReason`. It sits after the clock half and before the user turn, so `dynamicPrompt` is still a
  function of (clock, FY month) alone and the six wave-0 prompt assertions keep their meaning. It never
  echoes the question (`TestHintsNeverEchoTheQuestion`).
- `internal/archtest`: `internal/nlq` may import `internal/catalogue` (the safe direction; the
  catalogue still may not import nlq or llm).

Measured (`go test ./internal/nlq/ -v`): Static identical across two questions and two clocks
(`TestTheEntityGuideIsStaticAndComplete`, `TestStaticPromptIsClockIndependent`); **13** hint examples,
each schema-shaped, strictly decodable and compiling; over W1a's 30 tau `nl_query` utterances,
"**21/30** reach an answering entity (en 8/10, hinglish 8/10, hi 5/10)". The floor is pinned at 21;
the nine misses are logged and **the catalogue was not tuned to them** (the wave-1 review's finding 8
is what tuning to a test set looks like). The Devanagari misses are the owed Devanagari phrasings;
"output GST by head" and "electricity/telephone charges" have no catalogue words.

**Cassettes (the per-feature replay test starting from the input):** four tau utterances
(`nlq-bank-000-hinglish`, `nlq-creditor-002-en`, `nlq-debtor-005-hinglish`, `nlq-sales-003-en`) run
through the production Translator; the fake provider re-derives the key from the ENCODED wire body
(system text + every message) and replays a scripted `emit_query` input; the test asserts the wire
carried the ENTITY GUIDE and this question's hint, one provider call, and a compiling DSL. SeedNote:
the answers are scripted, not a model's. Mutations: rewording the hint header → all 4 cassettes miss
("The prompt changed, so its key changed. Re-record"); moving the hints into Static → "Static differs
between two QUESTIONS — the hints leaked into the prefix; first difference at byte 8331" plus the
wave-0 clock test red.

⚠️ **The +15 pt accuracy gate is still owed** — it needs a model (W1b). v3 is the "without catalogue"
arm and v4 the "with" arm; a score must be credited to its version.

## W2-4. Embeddings and the golden-query store (migration 0098, `internal/embed`, `llm.Client.Embed`)

### D122-13. Split the store so it degrades LOUDLY on a server without pgvector

The shared cluster and every other lane's database run postgres:16, which has no pgvector, and must
keep migrating. So 0098 creates the lexical half always and the vector half only where it can:

- **Always:** `pg_trgm`; `emb_document` (closed `kind`; `untrusted` tied to kind by CHECK —
  narration, bank line and document chunk are third-party text, PLAN-AGENTIC §2.4; content ≤ 4,000;
  sha256 natural key for idempotent re-indexing; trigram GIN); `emb_golden_query` (question, DSL,
  prompt version, proposer; accepted/rejected ONCE by trigger, question/DSL immutable, and a
  column-scoped UPDATE grant as the second wall); `emb_vector_status()`.
- **Only with pgvector:** `emb_enable_vectors()` creates `emb_vector` (halfvec(1024), version =
  embedding scheme, composite FK to the document) and its HNSW `halfvec_cosine_ops` index. Every
  statement naming a pgvector type is inside `EXECUTE`, so the file parses on postgres:16; the
  function is idempotent and is what an operator re-runs after the image switch.
- All three tables: ENABLE + FORCE RLS, USING + WITH CHECK, `(company_id, id)` UNIQUE.

**Loud, three ways:** the function RAISEs a WARNING naming the fix; `cored migrate` reads
`emb_vector_status()` and logs it (pgx does not print notices) — measured on 5433: "embeddings:
DISABLED — pgvector is not installed on this server; run Postgres from pgvector/pgvector:pg16, then
SELECT emb_enable_vectors(); (lexical search only; …)", and on 5434: "embeddings: enabled: pgvector
0.8.6"; and `internal/embed` returns `ErrVectorUnavailable` from `Vector` and sets `Degraded` on
`Index`, `Search` and `NearestGolden`. CI now sets `EMB_REQUIRE_VECTOR=1`, which turns the degraded
branch of the store test into a failure.

### What is built

- **`llm.Client.Embed`** (`internal/llm/embed.go`): the same walls as `Complete`, in the same order —
  Feature and Version required, pure encode, size gate, the tenant-id needle on the encoded body,
  reserve → content-free span (the prompt hash is a hash of model + dimensions + inputs) → one call →
  settle-or-release exactly once. Per-provider paths (Fireworks `/v1/embeddings`, DeepInfra
  `/v1/openai/embeddings`, vLLM `/v1/embeddings`); Anthropic and Sarvam refused with
  `ErrNotConfigured` before the network. A response whose count, index set or dimensions do not match
  the request is refused (`ErrNoStructuredOutput`) and never carries a partial batch.
- **`internal/embed`** (allowedDeps `{platform/db, llm}`, network-free by `TestW3PackagesAreNetworkFree`):
  `Store.Index` (idempotent; the provider call is OUTSIDE any transaction — insert, embed, insert —
  and a crash between leaves documents the next Index re-embeds), `Lexical` (pg_trgm
  `word_similarity`, threshold 0.3), `Vector` (cosine over one scheme version; `hnsw.iterative_scan =
  relaxed_order`), `Search` (reciprocal-rank fusion, k = 60), `Hit.Untrusted` carried through;
  `ProposeGolden` / `DecideGolden` / `NearestGolden` (only human-accepted examples are served).
- **`LLMEmbedder`** is the one production Embedder. The test double (`trigramEmbedder`, hashed
  character trigrams) lives only in `_test.go` and says why: a "semantic" search backed by it would be
  lexical search in disguise.

### Measured (`go test ./internal/embed/ -v`, both clusters)

| Cluster | Result |
|---|---|
| 5433, postgres:16 (no pgvector) | "this cluster: enabled=false (disabled: pgvector is not installed on this server; …)"; lexical search as A finds the Kaveri lines, **B finds none**; Index/Search/NearestGolden report `Degraded` with that reason; `Vector` → `ErrVectorUnavailable` naming `pgvector/pgvector:pg16`; the provider is never called |
| 5434, pgvector 0.8.6 | 4 vectors written, a text's own vector ranks first (score ≈ 1) and the other Kaveri line second; a query under another scheme version finds nothing; **B's vector search finds 0 and B counts 0 rows in emb_vector (A: 4)**; B cannot hang a vector off A's document; a 3-component vector is refused by `halfvec(1024)`; hybrid top hit correct |
| both | golden: 3 proposed, 1 accepted, 1 rejected, 1 undecided → **served 1**; re-deciding and editing a question refused by the trigger; B served none of A's; 0098's CHECK refuses every kind with the wrong `untrusted` flag (7 of 7) |

`TestLLMEmbedderReplaysItsCassette` replays one Fireworks-shaped embeddings batch by a key re-derived
from the wire body; mutation `Dimensions: 768` → "The request changed, so its key changed.
Re-record". Tier 0 now: "**replayed 19 interactions across 9 cassettes in 4 directories, features
[agent.decide embed.index extraction nlq.translate]**" (pins updated: 9 / 19).

**A measured limit, not a claim:** under RLS on a book this small the planner reads `emb_vector`
through the `company_id` index and an exact sort, not HNSW (correct results, no ANN). The HNSW index
is used when asked as the admin with `enable_seqscan = off` (`TestTheStoreOnThisCluster` checks
that). At scale one shared HNSW graph filtered by RLS is the wrong shape; **tenant-hash partitioning
(or per-tenant partial indexes) is owed**, as the plan said.

`internal/platform/testdb`'s teardown fixture seeds `emb_document`, `emb_golden_query` and — only on a
cluster that has it, via a named `optionalFixtureTables` entry with its reason — `emb_vector`
(teardown and census green on both clusters: "classified 90 base tables: 83 by company_id, 1
SpecialTenancy, 6 NotTenantScoped" on 5434).

### The image: `pgvector/pgvector:pg16-trixie` (docker-compose.yml and ci.yml)

pgvector is a Postgres extension, not a Go module or npm package, so §1.1 does not forbid it; HANDOFF
§6.6 asks the user to accept it as infrastructure — this lane made it switchable, not mandatory.

⛔ **The `-trixie` tag is a measured decision.** `tally_pg` runs `postgres:16` = 16.14 on Debian 13
(glibc 2.41); the bare `pgvector/pgvector:pg16` tag is 16.15 on **Debian 12 (glibc 2.36)**. On a
throwaway volume created by `postgres:16` (a 1,000-row text-keyed table):

| Started under | Result |
|---|---|
| `pgvector/pgvector:pg16-trixie` (16.15, Debian 13) | 1,000 rows; `datcollversion` / actual = **2.41 / 2.41**; `CREATE EXTENSION vector` → 0.8.6; halfvec cosine works |
| `pgvector/pgvector:pg16` (16.15, Debian 12) | "**WARNING: database "probe" has a collation version mismatch** — created using collation version 2.41, but the operating system provides version 2.36" — every text index would need a REINDEX |

**How the integrator / user switches `tally_pg` later** (not done here — other lanes share it):

1. Stop everything writing to it (cored, insightd, statutoryd, every lane's tests).
2. Recreate the container on the SAME volume with the new image. The volume is
   `tally-updated_tally_pgdata` (from `docker inspect tally_pg`). With compose (the file now names the
   image): `docker compose up -d db`. Without compose: `docker stop tally_pg && docker rm tally_pg &&
   docker run -d --name tally_pg -p 5433:5432 -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=tally -v
   tally-updated_tally_pgdata:/var/lib/postgresql/data pgvector/pgvector:pg16-trixie`. Same major
   version (16), same data-directory format: no `pg_upgrade`, no dump/restore; 16.14 → 16.15 is a
   minor-version step.
3. Verify: `SELECT version();` (16.x, Debian 13) and `SELECT datname, datcollversion,
   pg_database_collation_actual_version(oid) FROM pg_database;` — the two columns must be equal. If
   they are not (someone used a non-trixie tag): `REINDEX DATABASE <db>;` then `ALTER DATABASE <db>
   REFRESH COLLATION VERSION;` for each database.
4. For every database ALREADY migrated past 0098 on the old image (it recorded 0098 as applied with
   the vector half skipped): `SELECT emb_enable_vectors();` as postgres → `enabled: pgvector 0.8.6`.
   A database not yet at 0098 gets it from `make migrate`.
5. `make migrate` must now log "embeddings: enabled: pgvector …".

## W2-5. Supplier lead time — and why no new DSL entity

**Supplier lead time (DONE).** Three statistics per supplier — `lead_time_p50_days`,
`lead_time_p90_days`, `lead_time_lines_count` — from `order_fulfilment` joined to CURRENT, non-deleted
voucher versions, bounded by as-of, measured to the receipt that COMPLETES the line (not the first
drop — a trickle-shipper must not look fastest). The seeded book gains purchase orders on every
purchase, a two-drop Gadget order, an order never completed, and an ALTERED receipt. Hand-computed
and pinned: Bharat p50 **12**, p90 **20**, **3** lines (one ordered before the window, completed
inside it); Deccan p50 **162** (not 9), **1** line (the Bolt line never completed). "refresh wrote
**121** facts" (was 115); "reconciled **55** facts across **16** keys" (unchanged). Mutation — the
current-version join dropped from `leadTimes`: "Deccan Steel lead_time_p50_days[] = 9.0000,
hand-computed 162". The facts reach `agent.facts`, the `profile_facts` DSL twin and the supplier tool
automatically (`TestEveryToolEqualsItsDSLTwin` green).

**⛔ FINDING — why the orders / GST-return / cost-centre DSL entities were NOT added.** `POST /query`
is decided by the PDP as ONE token, `display query` (`auth_middleware.go` routeAction), and the DSL
compiler serves whatever entity the body names. So every entity the DSL gains is readable by anyone
holding `query`, whatever the report split says. It is already true once: the `outstandings` entity
answers `nature = LIABILITY` — the payables book — and the catalogue's own `bills-payable` example
uses it, so a level with `allow display query` and `disallow display report:bills-payable` reads the
payables figures through `/query`. That is the D97 shape exactly ("the rule governed the DOOR, not the
bytes"). The order books are split into `report:sales-order-book` and `report:purchase-order-book`
precisely so they can be granted separately (reporting/orderbook.go says so), so an `orders` entity
would widen the hole to purchase commitments. The fix is a PDP decision (decide `/query` per entity —
or per entity and filter for outstandings — against the covering report names), which belongs to the
authz/server owners and has to keep "the PDP lives in one place" (`server.go`: "a 403 written here
would be the first authorization decision inside a handler"). **OWED, and it gates W3's remaining
DSL entities.** Not measured by a test in this lane (the evidence is the route mapping and the
compiler's entity table).

**GSTR-2B regularity: owed.** No migration persists GSTR-2B data (`grep gstr2b migrations/` is
empty); the 2B match runs over an uploaded file. A regularity fact needs a stored history of 2B
periods per supplier first.

## W2-6. ⛔ OWED after wave 2 (W3's list, updated)

1. **The +15 pt NL accuracy gate** (v3 vs v4) — needs W1b and a provider.
2. **The embedding model** — W1a's Hinglish retrieval eval decides it; `LLMEmbedder.Scheme` carries
   the choice. **No indexer is wired**: nothing yet calls `Store.Index` for narrations, bank lines,
   parties, items, document chunks or the catalogue, and nothing calls `ProposeGolden` from an accepted
   `/query` answer or serves `NearestGolden` into the nlq prompt (the obvious next step: accepted
   examples as a Dynamic few-shot message, re-compiled under the current prompt version first).
3. **Tenant-hash partitioning / per-tenant HNSW** — measured: HNSW is not chosen under RLS at small
   scale.
4. **The `/query` per-entity authorization** (W2-5 finding), then the orders, GST-return and
   cost-centre allocation entities.
5. **GSTR-2B regularity** (needs persisted 2B), **item margin**, the **cold-start miner**, **≤400-token
   narratives**, a **nightly `profile.Refresh`** and history backfill — unchanged from §6.
6. **Switching `tally_pg`** to `pgvector/pgvector:pg16-trixie` (steps above) — the integrator's call,
   with HANDOFF §6.6.
7. **Devanagari catalogue phrasings** (5 of the 9 tau misses), and catalogue words for "output GST by
   head" / expense heads.
8. Routes and UI for Tier B, the catalogue, the tenant card and the retrieval store (bare tokens).
9. The retrieval hit's `Untrusted` flag must be honoured by the agent's taint rule when W2 wires
   retrieval in (`internal/agent` is W2-breadth's).

## W2-7. Gate figures at the end of the lane

| Gate | Command | Result |
|---|---|---|
| build / vet | `cd backend && go build ./... && go vet ./...` | clean |
| no new deps | `git diff 4e7cfff -- backend/go.mod backend/go.sum frontend/package.json frontend/pnpm-lock.yaml insightd/requirements.txt \| wc -l` | **0** |
| full backend suite, postgres:16 (shared cluster, lane DB `tally_w2_w3f`) | `go test ./... -count=1 -p 4 -timeout 60m`, all four DSN envs on the lane DB | **exit 0, 48 packages ok, 7 with no test files** (47 at the wave-1 merge + `internal/embed`). server 59.9 s, evals/tau 51.5 s |
| full backend suite, pgvector 0.8.6 (`tally_pg_vec`, port 5434, `EMB_REQUIRE_VECTOR=1` — what CI now runs) | same, `-p 3` | **47 ok, 1 FAIL**: `cmd/statutoryd` `TestNewStepStore_UnprivilegedRoleFailsAtBoot` — its probe DSN is HARDCODED to `localhost:5433/tally` (`cmd/statutoryd/main_test.go:156`), so on another port it creates the probe role on one cluster and connects to the other. Pre-existing harness defect (the §8 incident's class), unrelated to pgvector; it passes on 5433 and on CI (which is 5433/tally). Owed: route it through `testdb`. embed 2.0 s, tierb 56.9 s, server 79.0 s, evals/tau 103.2 s all ok |
| Tier 0 | `make tier0` | ok — "replayed **19** interactions across **9** cassettes in 4 directories, features [agent.decide embed.index extraction nlq.translate]" |
| migrations on a fresh database | `make migrate` on 5433 / 5434 | "migrations applied (**80** files)"; "embeddings: DISABLED — …" / "embeddings: enabled: pgvector 0.8.6" |
| 0099 idempotence | 0099 applied twice inside `BEGIN … ROLLBACK` (psql) | clean; 16 `tier_b_bound` policies |
| frontend / insightd / corpora | — | not touched by this lane; not run |

Commits on `agentic/w3-followons`: `c71a63f` (0099, Tier B), `a40aabe` (migrate lock), `d32d451`
(catalogue → nlq v4), `6685b58` + `60376e5` (0098, embed, llm.Embed), `07e413e` (pg16-trixie pin),
`63d2dce` (supplier lead time), and this ADR / §2.5 row.
