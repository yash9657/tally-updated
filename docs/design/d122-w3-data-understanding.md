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
- held-out (40 questions written before any tuning, kept out of `entries.go`):
  **`top-1 38/40 (95.0%), top-3 39/40 (97.5%)`**. The one miss, kept and not tuned away:
  `"items expiring soon"` → want `batch-summary`, got `stock-item-vouchers`, `stock-movement`,
  `reconciliation-queue`. The floor `heldOutTop3Floor` is pinned at the measured **0.975**.

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
| **DB replay of every accepted query as tenant B, tenant A's books present** | **2,208** | — | **1,055–1,056 ran, 1,152–1,153 refused by Postgres (type errors, never permission/read-only), 0 leaked** (two runs) |

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
