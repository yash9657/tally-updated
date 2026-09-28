> ⛔ **SUPERSEDED as the entry point by `HANDOFF-AGENTIC-WAVE-2.md`** (wave 2 merged 2026-09-28: W2
> breadth, W3 follow-ons, W4, W6-MCP). Start there. This file stays valid for wave 1's record: §4.3–§4.4,
> the §7 decision numbering and the §8 review.

# Handoff — agentic **wave 1 merged**: W1a, W2 (backend + frontend), W3, W5. ⛔ DRIVER: `PLAN-AGENTIC.md` §3
Updated: 2026-09-28, after the integration merge on `session/t32-and-close-7-3`. Supersedes
`HANDOFF-AGENTIC-AFTER-W0.md` as the entry point. That file's §7 (traps) and §9 (workflow recipe) still hold
and are not repeated here.

**One sentence:** the agent can now propose and, after a human decision, commit the eight accounting
voucher creates through a durable, policy-gated, intent-logged run; it has a semantic catalogue, entity
profiles and a fenced SQL tier to read the books with; bank statements must now prove their balance;
voucher creates are idempotent; and there are golden sets to judge all of it. **No model has been run on
any of it**, because there are no provider keys (§6.1).

## START HERE

1. **The five ADRs, one per lane.** Each lists what was built, what is owed and the mutations that prove
   its gates. They are the detail behind §1 and §2 below.
   - `docs/design/d124-w1a-golden-sets.md` (W1a)
   - `docs/design/d121-w2-agent-plane.md` (W2). Its "Frontend contract" and "Frontend" sections are the
     API between the two W2 halves.
   - `docs/design/d122-w3-data-understanding.md` (W3)
   - `docs/design/d123-w5-documents-bank.md` (W5)
   - `docs/design/d119` and `d120` are still the W0 and durable-runs records.
2. **§6 of this file (wave 2 order)**, then **§7 (decisions still on the user)**.
3. **§4 (integration findings)** before writing any test that touches Postgres.

---

## 1. What each workstream delivered

### W1a: golden sets (no spend, no keys). Branch `agentic/w1a`, merge `22c36a7`
- **Tau suite** (`corpora/tau`, 200 tasks): voucher_entry 50, item_invoice 30, bank_match 30,
  gst_returns 25, nl_query 30, master_creation 25, guard 10; en 65 / hi 68 / hinglish 67. The expected
  ledger state is computed in Python Decimal by the generator and re-derived by `validate.py`. It is never
  recorded from an engine run.
- **Harness** `backend/internal/evals/tau` (allowedDeps `{}`): snapshots the canonical state over HTTP,
  runs a multiset judge, reads answer figures, and computes an exact pass^k. Reference agent 200/200. Null
  agent passes exactly the 10 guards. Wrong agent 0/200. Flaky agent pass^1..5 = .6/.3/.1/0/0.
- **Extraction set** (`corpora/extraction`): 150 documents, all SYNTHETIC (100 invoices, 50 statements in
  5 bank layout families). The scorer is `insightd/evals/extraction.py`. Regex floor micro-F1 0.877864.
- **Hinglish retrieval eval** (`corpora/embedding`): 1,278 narrations, 218 queries, 2,342 judgments.
  The scorer is `insightd/evals/retrieval.py`. Lexical floor xlang_recall@10 0.124792.

### W2: the agent plane. Branches `agentic/w2-backend` (merge `b5a43b1`) and `agentic/w2-frontend` (merge `374f910`)
- **`internal/durable`**: the step engine moved out of `statutory`, which is now an alias. It has
  `Run`/`RunDynamic`, a per-run advisory lock, `ErrParked`, and expiry recorded as a decision row. All
  five d120 findings are closed. `agent.Poller` is started by `cored serve`.
- **`internal/policy`**: effective rights = ACL(user) ∩ ACL(agent) ∩ delegation, on `authz.Evaluate`,
  checked by the dispatcher and again in the router. Structural refusals: no delete, no governance
  action, no write that has no human path (this answers W0 §3.3: the budget alter/delete routes are
  denied), and the agent is never the checker. Autonomy levels L0–L3, default L1. **Taint rule:**
  untrusted input forces approval for money, master and statutory writes at every autonomy level.
- **Risk class on every ActionSpec**: 238 specs. read 85, export 46, master 29, money 25, governance 23,
  operational 19, destructive 6, statutory 5.
- **Intent log** (0092): append-only by trigger and by grant, a BLAKE3 chain per run, and a tenant anchor.
- **`internal/agent`**: a plan-and-execute loop over `internal/llm`. Preview runs the real handler and
  rolls back. Commit replays only the recorded preview, or the approver's edit. Validator errors get one
  bounded retry.
- **HTTP**: runs, AG-UI event stream, cancel, and the approval inbox (approve / edit-then-approve /
  reject / ask-back). Uses bare ACL tokens `agent-run` and `agent-approval`, so no ACL migration.
- **Frontend**: an `approval-inbox` ScreenKind (Gateway entry and Alt+G; Ctrl+A / Alt+X / Alt+B), a
  Ctrl+K agent side panel, and Payment/Receipt/Contra/Journal proposals opened in the normal voucher
  screens. There, Ctrl+A approves the proposal and never posts a voucher itself. There is an F11
  Features row, "Part built".
- **Gates**: 32 Tally injection cases (0 vouchers, 0 commits), a 72-hour approval across 4 processes
  committed exactly once, and exactly-once under contending pollers.

### W3: data understanding. Branch `agentic/w3`, merge `1cfd706`
- **`internal/catalogue`**: an entry for each of the 33 `authz.KnownReportNames`, and a deterministic
  BM25 selector. Held-out results: top-1 36/40, top-3 38/40 (re-measured with unseen party names; the
  first figure, 38/40 and 39/40, partly measured memorised names — see §8).
- **DSL entities** `stock_movements`, `bank_statement_lines` (narration can be filtered on, never
  returned) and `profile_facts`. The nlq enum is pinned to `query.Catalog()`; prompt `nlq-dsl-v3`.
- **`internal/profile`** + 0096: bi-temporal `entity_facts` / `entity_edges` / `tenant_profile`, built
  by 5 deterministic jobs. 55 facts across 16 keys reconcile to their reports to the paisa. Five profile
  tools (each ≤1k tokens) and a tenant card (≤1.5k tokens).
- **`internal/tierb`** + 0097: roles `agent_ro` and `agent_view_owner`, 8 security_barrier views, and a
  hand-written deny-by-default validator that re-emits SQL from the parse tree. 2,208 accepted
  adversarial queries were replayed as a second tenant with **0 leaks**, re-measured at this merge.

### W5: documents and bank statements. Branch `agentic/w5`, merge `03766ba`
- **Bank statements**: header found by its content, delimiter detection, and header signatures for five
  banks (all `Verified=false`). Narration rails (UPI/NEFT/IMPS/RTGS/NACH/…) with VPA/UTR/IFSC. A
  **mandatory balance proof**: a running walk, or declared opening + deposits − withdrawals = closing.
  Proof and narration columns are in 0101.
- **Voucher idempotency** (0100): insert-only key rows written as the last write of the post
  transaction, with replay and a 422 on a reused key. The frontend `draftAccept` sends a key derived
  from the content.
- **insightd**: validators (GSTIN check character, HSN/SAC structure, totals, duplicate bill),
  per-field provenance, and an OpenAI-compatible `json_schema` adapter. The adapter is **not** the
  default. There are 3 synthetic cassettes.

---

## 2. What is OWED, per workstream

This list is merged from the lane reports and the ADRs. Items that the merge made true or false are
marked.

**W0 (carried over).** The keystroke half of the D3 coverage join: `ui-commands.json` still has no `acl`
claims, and the cap `maxActionsWithNoHumanPathYet` is back at **27**. W0 §3.2 (Langfuse, and the
comparative NL→DSL claim) is unchanged.
~~§3.3 budget routes~~ is answered by W2's structural no-human-path refusal.

**W1a / W1b.**
- W1b bake-off runs. No model has been scored on any set. Blocked on §6.1 and §6.4.
  `nlq.DefaultModel` / `DefaultProvider` are unchanged.
- A real-document extraction set (consented, redacted, CA-reviewed). The synthetic F1 must not be
  quoted as accuracy on real documents.
- Native-speaker review of the Hindi and Hinglish text.
- Tau breadth: alteration and credit notes, TDS/TCS, bill-wise allocation, multi-currency, statement
  file import, units, period-filtered questions, multi-turn tasks.
- A live-stack CompanyFactory for a Tier-2 nightly, and a CI job for `TAU_K=5`.
- The "current Opus pipeline" extraction baseline that W5's gate names.

**W2.**
- Write breadth: only the 8 accounting voucher creates can be previewed or committed. Still to do:
  alterations (as a compensating version), stock vouchers, masters, orders, bank reconciliation,
  reminders, exports and prints, and undo as a generated reversal.
- The parity gate (50 keystroke traces, UI vs agent).
- The AgentDojo banking subset and InjecAgent.
- Live-model cassettes. Both `agent.decide` cassettes come from a scripted fake.
- An autonomy-level route and screen. Only `policy.SetLevel` exists.
- `created_by` should read `agent:<run> for <user>`.
- `VerifyAnchors` is not exposed.
- The approver-authority check is untested for a custom level.
- Editing Sales/Purchase/Credit Note/Debit Note proposals needs an "As Voucher" mode.
- Reject and ask-back from the voucher screen in proposal mode.
- SSE instead of 1 s polling.
- Orphan ledgers after a refused proposal-mode approval.
- A real-browser WebAuthn step-up drive.

**W3.**
- Embeddings: halfvec(1024), HNSW, pgvector. This needs a **new Postgres image** (§6.6). 0098–0099 are
  reserved for it.
- The embedding half of the selector, and the golden-query store.
- The +15 pt accuracy gate, which needs W1b and a provider. The catalogue is **not yet fed into the nlq
  prompt**.
- DSL entities for GST returns, orders and cost-centre allocation. The cold-start miner.
- Supplier lead time, GSTR-2B regularity and margin facts. ≤400-token narratives.
- A nightly `profile.Refresh` scheduler and history backfill.
- Server routes and UI for the tenant card (bare token `agent-profile`), the catalogue and Tier B.
  **W2 must register the profile tools**: they exist, but the agent's tool list does not include them.
- Tier C sandbox.
- Devanagari catalogue phrasings.
- The Tier B GUC-tamper residual: raw `set_config` as `agent_ro` can re-scope the tenant, and only the
  validator blocks it.

**W5.**
- Everything model-backed: PaddleOCR-VL, the VLM image path, Docling, and the field-level F1 gate. These
  need keys and a GPU.
- An HSN/SAC directory as reference data. The Bill Inbox does not yet send `known_hsn`.
- A round-off posting leg.
- Bill Inbox rendering of validations and provenance.
- Idempotency keys from hand-keyed screens and the offline outbox. The mock client does not model replay.
- A retention policy for `voucher_idempotency`.
- Bank signatures verified against real exports, and five more banks.
- The matcher does not use rail_reference/counterparty yet.
- The §2.8 adversarial review pass.

**W8.** Tier 0 is done. Tier 1/2 evals (built on W1a's sets), Langfuse, and guardrails in production
are owed.

---

## 3. Gate baselines at the merge HEAD (measured 2026-09-28 on a fresh `tally_integ`)

| Gate | Command | Baseline now |
|---|---|---|
| backend build + vet | `cd backend && go build ./... && go vet ./...` | clean |
| no new deps | `git diff c51c0a0 -- backend/go.mod backend/go.sum \| wc -l` | **0** (also 0 for `frontend/package.json`, `pnpm-lock.yaml`, `insightd/requirements.txt`) |
| offline build | `GOFLAGS=-mod=readonly GOPROXY=off go build ./...` | clean |
| backend tests | `go test ./... -count=1 -p 4 -timeout 60m` with all four DSN envs on `tally_integ` (pre-migrated, see §4.1) | **exit 0, 47 packages ok**, 7 with no test files (was 39 after W0). server 68 s, evals/tau 96 s |
| Tier 0 | `make tier0` | ok: **"replayed 14 interactions across 4 cassettes in 2 directories, features [agent.decide extraction nlq.translate]"** |
| Tier B leak replay | `go test ./internal/tierb/` | "2208 queries, 1056 ran as tenant B, 1152 refused by Postgres (type errors), **0 leaked** (0 by marker); **834 of the 1056** that ran would register a leak" (differential oracle since the review, §8) |
| action registry | `frontend/src/api/goldens/action-registry.json` | 238 specs; channel client 211 / none 27; cap 27 |
| frontend types | `cd frontend && pnpm exec tsc --noEmit` | exit 0 |
| frontend tests | `pnpm exec vitest run` | **Test Files 126 passed (126), Tests 2019 passed (2019)** (read the printed line, not the exit code) |
| insightd | `cd insightd && .venv/bin/python -m pytest` | **248 passed** (166 + 31 W1a + 51 W5); **249** after the review (§8) |
| corpora | `python3 corpora/validate.py` | `tax=14, tds=15, tcs=5, keystroke=38, isolation=13, migration=6, forecast=4, inventory=11, gstr2b=17, ims=12, payroll=19, tau=200, extraction=150, embedding=2342` |
| migrations on fresh DB | `make migrate` against `tally_integ` | "migrations applied (78 files)", max `0101_bank_statement_proof_and_narration` |

---

## 4. Integration findings: what the merge surfaced

**4.1 ⛔ A fresh database with `go test -p >1` fails: `migrations.Apply` takes no lock.** On an empty
`tally_integ`, the first full run failed in 5 packages with `duplicate key … pg_type_typname_nsp_index`
and `relation … already exists`. Each package migrated from empty at the same moment. **Pre-migrate
first** (`make migrate ADMIN_DATABASE_URL=… DATABASE_URL=…`), and then the suite is green. This bug
predates wave 1; every lane ran on a database that was already migrated. The fix (a
`pg_advisory_lock` around Apply) is owed.

**4.2 A real cross-lane break, fixed in `ebb53a7`.** W5 made the balance proof mandatory, and W3's
Tier B fixture imported a one-row CSV with no balance. All 4 DB-backed Tier B tests failed at setup. The
fixture now declares opening 0 / closing 1,180. Neither lane could have seen this on its own. Mutation
check: without `Declared`, all 4 tests fail with the proof error (observed in the pre-fix run).

**4.3 Three lanes fixed the same shared-DB hazard under two naming schemes.** W3 used
`ADMIN_DATABASE_URL`/`DATABASE_URL`, W2 used per-package helpers with the same names, and W5 used
`TESTDB_ADMIN_DSN`/`TESTDB_APP_DSN` through `testdb.AdminDSNValue()`. Resolution: every test DSN goes
through `testdb.AdminDSNValue()/AppDSNValue()`, which honour `TESTDB_*` **then** the Makefile names.
Four duplicate helpers were deleted, including `server/testdsn_test.go`. Exceptions that read the
Makefile names directly: `internal/durable` (a leaf package) and
`migrations/master_name_index_census_test.go`. **Set all four envs** when pointing at a private DB.

**4.4 The shared `tally` database is in a mixed state, and nobody should "fix" it by hand.** Lane test
harnesses applied 0092–0094 (19:50 UTC) and 0100–0101 (19:22 UTC) to it before the harness fixes. W3
dropped its own 0096/0097 from it by hand. So `tally` has 0092–0094 and 0100–0101 but **not** 0096–0097.
A `make migrate` on `tally` from this HEAD will apply 0096–0097 cleanly, because migrations apply by
name, not in sequence. The cluster-wide NOLOGIN roles `agent_ro` / `agent_view_owner` already exist.
Private databases `tally_w1a/w2/w3/w5/integ` remain and can be dropped.

**4.5 Conflicts resolved:**
- `PLAN-TALLY-PARITY.md` §2.5: took each lane's own row.
- `testdb_test.go` buildFixture: kept W3's 3 rows and W2's 8 rows.
- 8 test-DSN files: see §4.3.

Everything else merged cleanly: the boundaries allowedDeps, authz vocabulary, the registry goldens (W2
frontend had already regenerated on top of W2 backend), the tier0 pins (4/14) and the keystroke corpora.

---

## 5. Migrations consumed (PLAN-TALLY-PARITY §2.5 is the record)

| Lane | Numbers | Files |
|---|---|---|
| W1a | none | (fixtures and judges only) |
| W2 | 0092–0094; **0095 unused** | `0092_agent_intent_log`, `0093_agent_autonomy`, `0094_agent_runs_and_approvals` |
| W3 | 0096–0097; **0098–0099 reserved** for embeddings | `0096_entity_facts_and_tenant_profile`, `0097_agent_ro_and_published_views` |
| W5 | 0100–0101 | `0100_voucher_idempotency`, `0101_bank_statement_proof_and_narration` |
| wave 2 | 0102–0109 reserved | W4 0102–0104, W6 0105–0106, W7 0107–0109 |

No wave-1 file writes an ACL rule. The seeder census is unchanged at 17 PERFORMs. New route
authorities are bare tokens: `agent-run`, `agent-approval`, and later `agent-profile` and `llm-budget`.

---

## 6. Wave 2: the order

The sequencing constraints come from `PLAN-AGENTIC.md` §4, and wave 1's state settles the rest.

1. **W4 (insight engine + morning digest), now.** W3 has landed its prerequisite: facts, profiles,
   streams and the tenant card. Start from W3's owed "W2 registers the profile tools" and "nightly
   `profile.Refresh`". The digest needs both. Migrations 0102–0104.
2. **W6 (WhatsApp, voice, MCP server), now, in parallel with W4.** W2 has landed the action layer that
   every channel must produce an `ActionRequest` for. ⛔ Its WhatsApp half waits on §6.5 (BSP vs direct
   Cloud API). The **MCP server** half can start at once on the same policy/dispatcher path.
   Migrations 0105–0106.
3. **W2 breadth and W3/W5 owed items run alongside,** as follow-on lanes rather than new workstreams:
   alterations, masters and stock vouchers; the migrate lock (§4.1); the pgvector container (§6.6).
4. **W7 (learning loop), NOT before ≥6 weeks of W2/W4 signals.** Today there are zero real signals:
   no model has run, and every cassette comes from a fake. Counsel review (§6.5) should start now
   because of its lead time. Migrations 0107–0109.
5. **W9 (India-resident inference), gated on the residency decision (§6.3)**, and required before any
   regulated-partner deal.
6. **W1b (bake-off), blocked on keys and licences (§6.1, §6.4).** It is the single biggest unblocker.
   Every accuracy gate the plan names (W3 +15 pt, W5 field F1, W2 live cassettes) waits on it.
7. **W8 continues throughout**: Tier 1/2 evals built on W1a's sets, and Langfuse.

---

## 7. Decisions still on the user

Items keep the numbers they had in `HANDOFF-AGENTIC-AFTER-W0.md` §6, so every "§6.x" in this file and
in the lane ADRs still resolves. 6.2 (durable runs) is resolved; 6.6–6.8 are new.

- **6.1 ⛔ Bake-off spend and provider keys** (Fireworks, DeepInfra, Sarvam, and a ceiling). This
  unblocks W1b and every model-backed gate.
- **6.3 Residency posture for pilot tenants**: Tier-A egress with redaction and consent, or India-only.
  This decides W9's timing and W1b's provider list.
- **6.4 ⛔ Read the GLM-5.3 and Kimi K3 licences** before W1b measures them.
- **6.5 WhatsApp BSP vs direct Cloud API** (W6), and **counsel** on the GST filing boundary and the DPDP
  priors schema (before W7; start now).
- **6.6 NEW: pgvector.** W3's embeddings need a Postgres image with pgvector. Is it acceptable as an
  infrastructure dependency (it is not a Go/npm package, so §1.1 does not forbid it outright), or should
  embeddings live elsewhere?
- **6.7 NEW: ratify three W5 behaviour changes.**
  - A no-balance bank CSV is now **refused** unless the operator keys the opening and closing balances.
  - Five bank header profiles are a bounded departure from t4-slice2 §8.2. They match header names only
    and are unverified.
  - The benchmark GSTIN fixture was corrected (…Z5 → …Z0).
- **6.8 NEW: agent defaults.**
  - The default autonomy level is L1.
  - "Born blocked" masters are implemented as "an agent-created master exists only after a human
    decision"; there is no `ledger.blocked` column.
  - Owner-level approvals need a WebAuthn step-up.
- **Carried: the budget-variance deny** (0086) is still an assumption the owner has not ruled on.

---

## 8. Review outcome (wave-1 review, verified and fixed on `session/t32-and-close-7-3`)

Each finding was re-checked against the code first (a reproduction or a failing test), then fixed with
a test that fails before the fix. Mutations named are the ones actually run; every figure below was
printed by a command run on the database `tally_fix`.

| # | Finding | Verdict | Fix / commit | Gate and mutation |
|---|---|---|---|---|
| 1 | CaMeL gate bypass: every `actions.read` labelled `books` (trusted), so a bank narration or a draft read through the generic door left the context clean and an L2 payment committed autonomously | **CONFIRMED, fixed** | `a51c107`: `agent/readsource.go` classifies all 79 GET read specs (bank / document / new `stored_text` / books), unlisted falls closed; `query_books` stays `books` (the query whitelist never projects a prose column) | `TestReadOfUntrustedStoredTextTaintsTheGate` (bank statement, draft via actions.read, day book, recon queue all force approval at L2; trial balance control stays autonomous). Mutation: label back to `books` → 4 cases commit autonomously. `TestReadSourcesCoverEveryReadSpec`: deleting one entry fails it |
| 2 | Approver's authority judged without date/body; commit ran as the creator only; edited body never re-gated | **CONFIRMED, fixed** (authorship left as designed) | `d4abc44`: `Caller.Approver`; `companyAuthMiddleware` re-evaluates the approver's level, rules, cutoff and step-up on the concrete commit request (`approverDenies`); the decision's early check dates from the body that will commit; the commit hook rolls back debits above `Delegation.MaxAmountPaise` | `TestAgentApproverIsJudgedOnTheCommittedBody`, 4 subtests. Mutations: no date at decision → subtest 1 fails; no router re-judge → "cutoff set between approval and commit" fails; no ceiling → Rs 4,90,000 edit commits past a Rs 30,000 ceiling. The voucher is still authored by the run's creator (it is their delegation); the approver is in `agent_approval_decision` and the intent log. Re-previewing an edited body (so the approver sees the recomputed effect) is **OWED** |
| 3 | Cancel does not stop a run mid-Resume | **CONFIRMED, fixed** | `21baf44`: every step re-reads `agent_run.status` (`stopWhenEnded`); the commit hook takes `FOR SHARE` on the run row inside the voucher's transaction | `TestAgentCancelStopsARunMidResume` (cancel issued from inside the fake model's round trip). Mutations: guard off → `commit_refused` recorded (hook catches it); guard + hook off → 1 voucher commits |
| 4 | Any `agent_commit` insert error treated as a lost race → 200 Replayed with no voucher | **CONFIRMED, fixed** | `afa08f9`: only SQLSTATE 23505 is a replay; other failures are transient errors (step retried under the same key); a collided key with no visible winner is an error | `TestAgentCommitBookkeepingFailureIsRetriedNotReplayed` (company-scoped trigger raises 40001, then is dropped; retry commits exactly 1). Mutation: restore "any error is dupKey" + ignore `found` → run completes with a `commit` intent and 0 vouchers |
| 5 | 0092 claims per-run appends are serialised by the run lock; Decide/Cancel append without it | **CONFIRMED, fixed** | `61c3be2`: `intentlog.Append` takes `pg_advisory_xact_lock(0x1A7E, hashtext(run))` (two-int key space, disjoint from durable's bigint lock); Cancel now appends before updating `agent_run` (Decide's order, no deadlock); 0092 header corrected (comment only) | `TestConcurrentAppendsToOneRunSerialise` (8 writers). Mutation: lock removed → "7 of 8 concurrent appends failed … agent_intent_log_one_seq_per_run" |
| 6 | Balance proof accepts a swapped Deposit/Withdrawal mapping on identical-amount rows | **CONFIRMED, fixed** | `982d357`: when the mirror image (other order, every sign inverted) also walks, the dates decide; a contradiction is refused (`ErrBalanceWalk`), undecidable dates leave `BalanceWalked` false so declared balances are required | `TestASwappedMappingOnIdenticalRowsIsRefusedByTheDates`, `TestAnUndecidableMirrorNeedsDeclaredBalances`. Mutation: mirror check off → "err=<nil> order=newest-first proof=running opening=6000.00 lines=DDD" |
| 7 | Tier B leak gate mostly blind (marker only visible to name-projecting queries) | **CONFIRMED, fixed** | `7ae3c25`: differential oracle — every accepted query runs as B before and after A's books exist; any change is a leak; sensitivity measured and floored (≥ half of what runs) | "2208 queries, 1056 ran as tenant B, 1152 refused by Postgres (type errors), 0 leaked (0 by marker); 834 of the 1056 that ran would register a leak". Mutation: `agent.stock_movements` re-owned to `postgres` → **86 leaks flagged, only 17 by the marker** |
| 8 | Held-out selector set contaminated by the catalogue's example party names | **CONFIRMED, fixed** | `52f6444`: held-out names replaced with unseen ones (`heldOutNames`), pinned unseen by `TestHeldOutNamesAreUnseen`; floor re-pinned 0.975 → **0.95**; no synonym or weight tuned | Re-measured "held-out: top-1 36/40 (90.0%), top-3 38/40 (95.0%)"; new miss "what do we have to pay Sunrise Steel" → payroll-payslip. Mutation: "Kaveri Traders" back in a question → the unseen test fails. d122 §2 updated |
| 9 | `lenadari` (receivables) listed under Bills Payable; `denadari` nowhere | **CONFIRMED, fixed** | `52f6444`: `lenadari` → Outstandings, `denadari` → Bills Payable | `TestReceivableAndPayableWordsPointTheRightWay`. Mutation: original entries → "lenadari kitni hai" selects bills-payable, "denadari kitni hai" selects nothing |
| 10 | Invoice total "grounded" by any number on any OCR line (qty, rate, HSN, GST %) | **CONFIRMED, fixed** | `dad8188`: on a bill that labels its total, the total grounds only on the labelled line or the line under a bare label; otherwise `fail` | `test_a_total_that_is_a_quantity_rate_hsn_or_rate_percent_is_not_grounded` (5000, 20, 8471, 18 all ungrounded + fail; real total and a two-line layout still ground). Mutation: labelled-line rule off → test fails. insightd **249 passed** |
| 11 | tau judge passes a shotgun answer; Dr/Cr never judged on read-only tasks | **CONFIRMED; shotgun fixed, direction DEFERRED** | `3ece263`: money-shaped figures the task did not ask for are bounded at one per expected output (min 1); the reference agent still solves every task | `TestJudgeRefusesAShotgunAnswer`. Mutation: bound off → both trial-balance dumps pass. The Dr/Cr half needs a side per output across 204 fixtures and a prose side reader: **OWED** (d124 §3) |
| 12 | `stream_total_window` reconciler circular (sums its own evidence) | **CONFIRMED, fixed** | `7eafc5c`: the run is re-derived from the day book — evidence must be one contiguous stretch of the ledger's legs, inside the ±5% / ±3-day bands, not extendable by either neighbour; the seed gains a one-off rent repair (vouchers_window 50 → 51) | Still "reconciled 55 facts across 16 keys". Mutations in `detectStream`: drop the latest month → "the leg after the run also fits it"; group the one-off in → "a member of the run is outside the ±5% … band" |
| 13 | 0097 header says either half holds alone; the GUC-tamper residual is only `t.Logf`'d | **CONFIRMED (doc/test), residual itself DEFERRED** | `52f6444`: 0097 header and `tierb` package doc state the residual; the test now PINS it (fails if it stops reproducing, telling the reader what to update) | "RESIDUAL CONFIRMED (pinned): raw SQL with set_config re-scoped to tenant A". Closing it (RLS keyed to something agent_ro cannot set, since `SET` is also open to it) is **OWED** — d122 §5 item 12 |

New owed items from the review: re-preview an approver's edited body (#2), Dr/Cr in read-only answers
(#11), the GUC-tamper hardening (#13, already listed).

Gates re-run after the fixes, at `7eafc5c`, on the database `tally_fix`:

- `cd backend && go build ./... && go vet ./...`: clean. `git diff 743e39a -- backend/go.mod backend/go.sum
  frontend/package.json frontend/pnpm-lock.yaml insightd/requirements.txt | wc -l`: **0**.
- `go test ./... -count=1` (ADMIN_DATABASE_URL / DATABASE_URL on `tally_fix`): **exit 0, 47 packages ok,
  7 with no test files**.
- `make tier0`: `ok github.com/khata/cored/internal/trajectory` (no cassettes added or changed).
- `cd frontend && pnpm exec tsc --noEmit`: exit 0. `pnpm exec vitest run`: **Test Files 126 passed (126),
  Tests 2019 passed (2019)** (`AgentProvenanceSource` gained `stored_text`).
- `cd insightd && .venv/bin/python -m pytest`: **249 passed**.
