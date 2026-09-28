# Handoff: agentic **wave 2 merged** (W2 breadth, W3 follow-ons, W4, W6-MCP). ⛔ DRIVER: `PLAN-AGENTIC.md` §3
Updated: 2026-09-28, after the integration merge on `session/t32-and-close-7-3`. This file replaces
`HANDOFF-AGENTIC-WAVE-1.md` as the entry point. Still valid from that file: §4.3 (DSN envs), §4.4 (the
shared `tally` database is in a mixed state), §7 decision numbering (6.x) and §8 (wave-1 review). They are
not repeated here. Also still valid: `HANDOFF-AGENTIC-AFTER-W0.md` §7 (traps) and §9 (workflow recipe).

**In one sentence:** the agent can now write through the approval gate beyond the eight voucher creates:
it can alter and undo vouchers, create masters (every ledger it creates starts blocked), post stock
vouchers, place orders and tick bank reconciliations. An owner sets its autonomy on a screen. An outside
MCP client can drive the same loop, but only ever through a human approval. A deterministic insight engine
produces a verified morning digest. Tier B raw SQL is now bound to its tenant by the database itself. **No
model has run on any of it**: there are still no provider keys (§7, 6.1).

## START HERE

1. **The lane ADRs.** Each lists what was built, what is owed, and the mutations that prove its gates:
   - `docs/design/d121-w2-agent-plane.md`, section "Wave 2 breadth" (W2 breadth)
   - `docs/design/d122-w3-data-understanding.md`, section "Wave 2" (W3 follow-ons; the pgvector switch)
   - `docs/design/d125-w4-insights.md` (W4, new)
   - `docs/design/d126-w6-mcp.md` (W6 MCP, new; its last section "Wave-2 merge" is the integrator's)
   - Wave 1: d124 (W1a), d123 (W5), d119/d120 (W0, durable runs)
2. **§4 (what the merge found)**, then **§6 (next order)** and **§7 (decisions on the user)**.
3. **Before any test touches Postgres:** create a private database and set all four DSN envs
   (`ADMIN_DATABASE_URL`, `DATABASE_URL`, `TESTDB_ADMIN_DSN`, `TESTDB_APP_DSN`). Migrations now take an
   advisory lock (§1, W3), so a fresh database with `go test -p 4` no longer needs pre-migrating.

---

## 1. What each workstream delivered in wave 2

Merge commits on `session/t32-and-close-7-3`, in order: `agentic/w3-followons` (649a3d1), then
`agentic/w2-breadth` (0326873) as `37e15af`, `agentic/w4` (e66b04f) as `43ddbf2`, and `agentic/w6-mcp`
(e829a59) as `0d6a8ea`. Integration fixes followed in `0f4afc0` and the handoff commit.

### W3 follow-ons (d122 "Wave 2"), migrations 0098–0099
- **0099 binds Tier B to its tenant in the database.** This closes the GUC residual that wave 1 pinned.
  The lane also found and closed a worse hole: `set_config('role','tally_app')` undid
  `SET LOCAL ROLE agent_ro`, and tenant B then read tenant A's base-table ledgers. The fix has four parts:
  - SECURITY DEFINER `tier_b_run`/`tier_b_plan`, owned by `agent_ro`;
  - a `(pid, xid8)` binding table that only `tally_app` writes;
  - RESTRICTIVE `tier_b_bound` policies on the 16 base tables;
  - PUBLIC EXECUTE revoked from 0074's two security-definer functions.

  The tamper suite has 22 attacks, all refused, and 6 live controls. The pinned residual test is inverted.
- **`migrations.Apply` holds `pg_advisory_lock(0x4D47,1)`.** This closes wave-1 §4.1. Measured now:
  "4 concurrent appliers on an empty database: 0 failed, 84 migrations recorded once each".
- **nlq prompt `nlq-dsl-v4`.** The semantic catalogue is in the prompt: an entity guide and rule 11 in the
  cached half, and the top-3 hints per question in the per-call half. 4 cassettes replay by keys taken
  from the wire. 21 of 30 tau utterances reach an answering entity; the floor is pinned at 21.
- **0098 embeddings store.** `pg_trgm` is always on. `halfvec(1024)` + HNSW are created only where
  pgvector exists, and the store says loudly when they are missing. The migration also adds
  `emb_document` and `emb_golden_query`, whose decision can be made once only. Also added:
  `llm.Client.Embed`, the `internal/embed` store (Lexical, Vector, rank-fused Search) and the golden
  store. `docker-compose.yml` and CI now use `pgvector/pgvector:pg16-trixie` (see §4.6).
- **Supplier lead-time facts** (Bharat p50 12 / p90 20; Deccan 162).

### W2 breadth (d121 "Wave 2 breadth"), migration 0095
- **The dispatcher reads back everything a handler's transaction wrote:** voucher versions and the
  version each one replaces, stock movements, master_audit, orders, and bank ticks before and after. That
  readback is both the approver's diff and the commit record (`agent_commit.object_kind/object_ids`).
  The preview allowlist grew from 8 specs to 33.
- **Alterations** are a compensating version with a before/after diff. A commit lands only on the
  version the approver saw.
- **Undo** is `actions.reverse`: Go builds the reversal, and the model only names the voucher. It refuses
  GST, inventory, TDS/TCS and order-linked vouchers, with a reason.
- **Masters through approval.** Every ledger the agent creates starts **blocked**. The ledger service
  refuses postings to it, and the `posting_ledger_not_blocked` trigger backs that up. Only a person can
  release it, through `POST /ledgers/{id}/unblock` (bare token `master-unblock`, governance class).
- **Other writes:** stock vouchers, sales/purchase orders and bank-reconciliation tick/un-tick all go
  through the same gate. Exports run as reads, and the model sees only status, size and hash.
- **W3's five profile tools** are in the agent's tool list. The server applies the human's ACL fact by
  fact, and a tenant card with typed notes is labelled `stored_text`.
- **Owner controls:** `GET/PUT /agent/autonomy` is owners only and always needs a step-up; every change
  is audited. The Agent Settings screen is reached from the Gateway or Alt+G.
- **Approvals and attribution:** an approver's edit is re-previewed before it is decided, and a refused
  edit returns `422 EDIT_REFUSED`. Every agent write is attributed `agent:<run> for <user>`. Run-chain and
  anchor verification have routes.
- **The parity gate** replays all 143 tau write traces through both the API and the agent with inbox
  approval. The canonical books and voucher numbers are identical on both paths.

### W4 insight engine v1 (d125, new), migrations 0102–0103
- **15 insight types, all deterministic Go:** 5 real-time (fed from a `voucher_head` trigger queue) and
  10 nightly.
- **The evidence gate.** Every row carries `evidence_sql` + args + voucher ids. The SQL is re-run at
  write time and must reproduce every figure exactly, or the insight is refused and counted.
- **The nightly run** takes one transaction per (company, IST day). It calls `profile.Refresh`, which was
  W3's owed scheduler, and it is idempotent.
- **Ranking and the digest.** The ranker scores impact × p_real × actionability × novelty × affinity.
  p_real is a Beta posterior over declared priors, with a digest floor of 0.70. The digest holds at most
  5 items.
- **The narrator** sends the model keys only. It rejects any numeral or number word in any script,
  re-checks the rendered text, and falls back to a template.
- **Routes and screen:** 10 routes under the bare token `insights` (Data Entry is refused on all 10).
  The Insights inbox screen is keystroke-driven. `0103` adds `ledger.credit_limit`.

### W6 MCP server (d126, new), migration 0105
- **Transport.** Hand-rolled JSON-RPC 2.0 over Streamable HTTP, stdlib only, protocol versions
  2025-11-25 and 2025-06-18.
- **An MCP client takes the model's seat.** `initialize` calls `agent.Engine.StartExternal`, and each
  `tools/call` becomes one recorded `decide:k` through `Engine.Submit`. `internal/mcp` holds no
  dispatcher; archtest and a source scan pin that.
- **Every MCP run starts tainted** with the untrusted provenance `mcp_client`. A money, master or
  statutory commit therefore always waits for a human, at every autonomy level.
- **Identity** is the human's own bearer token plus a grant (0105): delegated keys, an optional paise
  ceiling, an expiry and a step-up flag. The manifest SHA-256 is pinned per session.
- **A wave-1 bug fixed on the way:** the ceiling query summed a TEXT column.

### Integrator (this merge)
- **The MCP manifest now covers wave 2.** It shows a preview tool for every one of the 33 previewable
  specs. Only voucher routes carry the voucher body schema, a DELETE previews with `{}`, and `legNo` is
  an ordinal. It adds `reverse_voucher` (maps to `actions.reverse`) and W3's five profile tools under the
  agent's own names. Figures: registry 259 specs; payment+master grant 94 tools (80 reads, 3 previews, 6
  meta, 5 profile); read-only grant 89; all-previewable grant 124 (33 previews).
- **The injection suite runs on both paths from one case list.** In-app: "45 injection cases (bank:10
  document:12 email:8 tally_xml:7 whatsapp:8); 30 successful write previews (money, master, operational)
  parked by the provenance gate at L3; the books unchanged, 0 commits". MCP: "45 injection cases over
  MCP at L3; 30 successful write previews … parked on the mcp_client taint; 14 calls refused as tools the
  session was never shown; 0 vouchers, 0 commits".
- **A cross-tenant posting hole is closed** (§4.1).
- **Three harnesses that assumed the shared database are fixed** (§4.4).

---

## 2. What is OWED, per workstream (merged from the lane reports and ADRs; checked at this HEAD)

**W0 (carried).** The keystroke half of the D3 coverage join: `ui-commands.json` still has no `acl`
claims. The no-human-path cap is now **32**, because W6 added 5 MCP rows with written reasons. Langfuse
is owed; it moves to W8.

**W1a / W1b (unchanged from wave 1).** The W1b bake-off (blocked on 6.1 and 6.4). A real-document
extraction set. Native-speaker review. Tau breadth. A live-stack CompanyFactory and `TAU_K=5` CI. The Dr/Cr
half of the judge (wave-1 review #11).

**W2.**
- **Breadth still missing:**
  - Ledger alteration: there is no human screen for it, so the policy refuses the agent.
  - Agent actions for groups, units, godowns, price lists, BOMs, budgets and cost centres.
  - Order line revision.
  - Reversal of GST, inventory, TDS/TCS and order-linked vouchers, which needs a credit or debit note
    that a person prepares.
- **Reminders** through notify templates are not built. Ledgers have no contact field, and the delivery
  channels belong to W6.
- **Parity:** traces recorded from real keystrokes. The 143 traces are tau API plans, not recordings
  from VoucherScreen keystrokes.
- **UI:**
  - A voucher ledger picker that marks blocked ledgers.
  - Per-user autonomy rows set from the screen.
  - "As Voucher" editing of invoice proposals.
  - Reject and ask-back from the voucher screen.
  - SSE instead of polling.
  - A real-browser WebAuthn drive.
- **Evals:** AgentDojo/InjecAgent, and live-model cassettes. Every `agent.decide` cassette comes from a
  scripted fake.
- **Cost not measured:** the per-company xmin scans in the effect readback, on a large tenant.

**W3.**
- **Accuracy:** the +15 pt NL accuracy gate comparing v3 and v4 (needs W1b and a provider).
- **Embeddings:**
  - Choose the embedding model.
  - Wire the indexers and golden-query proposals, and serve NearestGolden into the nlq prompt.
  - Tenant partitioning for HNSW: at small scale the planner was measured to skip HNSW under RLS.
  - Switch `tally_pg` to pgvector (§4.6), then run `emb_enable_vectors()` on databases already migrated.
- **Authorization, then new DSL entities:** per-entity authorization for `/query` (risk §7, 6.10), then
  the orders, GST-return and cost-centre entities.
- **More facts and data:** GSTR-2B regularity (there is no persisted 2B data), item margin, ≤400-token
  narratives, history backfill, Devanagari catalogue phrasings.
- **Tier C sandbox.**
- **Done at this merge:** the nightly `profile.Refresh` scheduler (W4's nightly run calls it) and the
  `cmd/statutoryd` DSN harness (§4.4).

**W4.**
- **Model-backed insights:** predicted-late (LightGBM + SHAP), expected pay date, BG/NBD reorder,
  ALS/FP-growth cross-sell, festival uplift, and 13-week cash p10/p90.
- **Two insight types need data the schema lacks:** `early_payment_discount` (no discount terms) and
  `vendor_bank_change` (no ledger ALTER path).
- **Measurement gates:** a P(late) AUC backtest, forecast vs seasonal-naive, a 200-insight CA panel, and
  a measured per-type false-positive rate. Today p_real is a declared prior, not a measurement.
- **Delivery:** the WhatsApp digest (W6). Per-recipient digests, a bandit over timing, quiet hours.
- **Agent insight tools:** register the tools, add a provenance class for them (the digest is
  model-written text, so `stored_text`), and decide whether `insights` stays governance-class.
- **UI:** the credit-limit field on the ledger form, a per-row suppression list, and per-item drill.
- **Reference data and detector gaps:** HSN directory reference data (W5), fuzzy duplicate bills, supplier
  lead time feeding reorder-now (today a 14-day constant), and QRMP / Rule 36(4) for the GST liability
  estimate.
- **Done at this merge:** the migrate advisory lock (§1, W3).

**W5 (unchanged from wave 1).**
- Everything model-backed: PaddleOCR-VL, the VLM path, Docling, and the field F1 gate.
- The HSN/SAC directory, the round-off leg, Bill Inbox rendering of validations and provenance.
- Idempotency keys from hand-keyed screens and the offline outbox, and `voucher_idempotency` retention.
- Real-bank signatures, the matcher's use of counterparty/rail_reference, and the §2.8 adversarial pass.

**W6.**
- **WhatsApp**, blocked on 6.5; 0106 is reserved for it.
- **Voice** (Saaras/Bulbul), which needs 6.1 as well as 6.5.
- **OAuth 2.1 for MCP** (RFC 9728 PRM, RFC 8707, CIMD, dynamic registration). Today a client is configured
  by hand.
- **Protocol revision:** align with spec revision 2026-07-28 / MRTR `input_required`, which the lane could
  not verify offline.
- **Missing pieces:** a grant screen in the frontend, per-handler input schemas (today an open hint for
  non-voucher writes), a session reaper, and server notifications/SSE/resources/tasks.
- **Live runs:** against a real MCP client, and a live-model trajectory.
- **Done at this merge:** MCP write breadth now follows W2's allowlist.

**W8.** Tier 1/2 evals, Langfuse, and production guardrails.

**Integrator (new at this merge).**
- A **composite FK** `posting (company_id, ledger_id) → ledger (company_id, id)`, which makes §4.1
  structural. It needs a migration number that no lane owns; see 6.12.
- Drop the lanes' private databases (§4.5).

---

## 3. Gate baselines at the merge HEAD (measured 2026-09-28 on a fresh `tally_w2_integ`, since dropped)

| Gate | Command | Baseline now |
|---|---|---|
| backend build + vet | `cd backend && go build ./... && go vet ./...` | clean |
| offline build | `GOFLAGS=-mod=readonly GOPROXY=off go build ./...` | clean |
| no new deps | `git diff 4e7cfff -- backend/go.mod backend/go.sum frontend/package.json frontend/pnpm-lock.yaml insightd/requirements.txt \| wc -l` | **0** |
| migrations on a fresh DB | `make migrate` on an empty `tally_w2_integ` | "migrations applied (83 files)", max `0105_agent_mcp_grants_and_sessions`; "embeddings: DISABLED — pgvector is not installed on this server …" (expected on `postgres:16`, §4.6) |
| backend tests | `go test ./... -count=1 -p 4 -timeout 60m`, all four DSN envs on `tally_w2_integ`, **not** pre-migrated | **exit 0, 51 packages ok, 7 with no test files** (47 after wave 1). server 89 s, sync 66 s, evals/tau 39 s (a second run on the pre-migrated DB: 51 ok, server 92 s) |
| Tier 0 | `make tier0` | ok: "Tier 0: replayed **29 interactions across 12 cassettes in 5 directories**, features [agent.decide embed.index extraction insight.narrate nlq.translate], with no network and no database" |
| injection (in-app) | `go test ./internal/server/ -run TestAgentInjectionSuiteCommitsNothing -v` | 45 cases; **30** write previews parked at L3; 0 commits |
| injection (MCP) | `-run TestMCPInjectionSuiteCommitsNothing -v` | 45 cases; **30** parked on `mcp_client`; 14 unknown-tool refusals; 0 vouchers, 0 commits |
| parity | `-run TestAgentParityTracesMatchTheUIPath -v` | "143 traces …, 167 write steps through 167 agent runs …, 18 agent-created ledgers unblocked by a person; canonical books and voucher numbers identical on both paths" |
| Tier B leak replay | `go test ./internal/tierb/ -v` | "2208 queries, 1056 ran as tenant B, 1152 refused by Postgres (type errors), **0 leaked** (0 by marker); 834 of the 1056 that ran would register a leak" |
| insight evidence | `go test ./internal/insights/ -run TestEveryInsightReproducesItsNumbers -v` | "verified 37 insight rows across 15 types, every figure equal to the paisa" |
| migrate lock | `go test ./migrations/ -run Lock -v` | "4 concurrent appliers on an empty database: 0 failed, 84 migrations recorded once each" |
| action registry | `frontend/src/api/goldens/action-registry.json` | **259 specs**, 227 with a human path, 32 without; cap **32**; 259 route arms |
| frontend types | `cd frontend && pnpm exec tsc --noEmit` | exit 0 |
| frontend tests | `pnpm exec vitest run` | **Test Files 128 passed (128), Tests 2040 passed (2040)** |
| insightd | `cd insightd && .venv/bin/python -m pytest` (INSIGHTD_DATABASE_URL on the private DB) | **249 passed** (it was 247 passed + 2 skipped on a private DB before §4.4's fix) |
| corpora | `python3 corpora/validate.py` | `tax=14, tds=15, tcs=5, keystroke=40, isolation=13, migration=6, forecast=4, inventory=11, gstr2b=17, ims=12, payroll=19, tau=200, extraction=150, embedding=2342` |

Regenerate the goldens with `UPDATE_GOLDENS=1 go test ./internal/server/ -run TestActionRegistryGolden`
(from `backend/`) and `UPDATE_UI_MANIFEST=1 pnpm exec vitest run uiCommandManifest` (from `frontend/`).
Then run the Go golden again, because it joins the UI manifest.

---

## 4. What the merge found

**4.1 ⛔ Cross-tenant posting hole: a voucher in company A could post to company B's ledgers.** This was
fixed in `0d6a8ea`.
- **The cause.** `posting.ledger_id`'s FK is single-column (`REFERENCES ledger(id)`), and the ledger
  service never asked whether a posting's ledger belonged to the company.
- **What it allowed.** A member of two companies could `POST A/vouchers/payment` with B's ledger ids and
  get **201**. That wrote two postings under A that A's own reports can never join back, plus
  `ledger_balance` rows for them. The same result appeared at the W6 branch head, so the hole predates
  wave 2.
- **Why the merge surfaced it.** W6's cross-tenant MCP gate had passed only because the wave-1 effect
  readback errored on the missing ledger names. W2 breadth's readback returned `postings: []` and 201
  instead.
- **The fix.** `ledger.refuseBlockedLedgers` now counts the voucher's ledgers under `company_id = $1` and
  refuses a shortfall (`ErrLedgerNotInCompany`, 422). The readback now LEFT JOINs, so a leg like this
  would still count in the effect and the ceiling.
- **The gates.** `TestAVoucherCannotPostToAnotherCompanysLedgers` (human path, including a half-foreign
  voucher, with a control) and `TestMCPCrossTenantRefusal`. Mutation: with the count check off, both fail
  (201; the MCP preview shows two legs with empty names).
- **Owed:** the composite FK (§2, 6.12).

**4.2 The MCP ceiling test meets W2 breadth's re-preview first.** An over-ceiling approver edit is now
refused at the decision (`422 EDIT_REFUSED`), not at commit. The test now also approves the unedited
over-ceiling proposal. That proves the commit-time hook is still the last line: with the hook disabled,
it posts 1 voucher.

**4.3 Conflicts resolved.**
- **Merged as unions of both sides:** `risk.go` governance tokens (`agent-autonomy`, `master-unblock`,
  `insights`, `agent-mcp`), authz vocabulary, `policy.AgentRules`, boundaries allowedDeps (the
  network-free list gains `embed` and `insights`; `internal/server` gains `profile` and `mcp`), testdb
  fixture rows (0098 and 0102), the stored-date allowlist, `KNOWN_SCREENS`, and both new ScreenKinds
  (`agent-settings` and `insights`) through types, navMachine, AppShell, Gateway, Help, defaultScreens and
  client/rest/mock.
- **`masters.CreateLedger`** inserts both 0095's born-blocked columns and 0103's `credit_limit`, and
  audits both.
- **The dispatcher ceiling** keeps W2 breadth's readback `DebitPaise`. W6's `sum(amount::numeric)` query
  was retired along with the `sum(text)` query it fixed.
- **Tier 0 pins:** W3 9/19, plus W2 breadth +2/+9, plus W4 +1/+1, gives **12 cassettes / 29
  interactions**.
- **PLAN-TALLY-PARITY §2.5:** each lane's row is kept. The W2 row's number column now reads 0092–0095,
  and the reserved row states what is still free.
- **Goldens** were regenerated after each merge, not hand-merged.

**4.4 Three harnesses that assumed the shared database** (`0f4afc0`).
- `migrations/apply_lock_test.go` leaked a `tally_applylock_*` database on every run. Its drop ran after
  a deferred `admin.Close`. The 5 leaked databases were dropped.
- `cmd/statutoryd/main_test.go` granted and connected to the hardcoded `5433/tally`. It now derives both
  from `testdb.AppDSNValue()`.
- `insightd/tests/test_pg_store.py` derived its schema-less DSN by matching the literal `/tally?`, so on
  a private database both fail-loud assertions were skipped.

**4.5 Databases left in the cluster.** Nothing here touched `tally`, and `tally_pg` was not restarted.
The lanes' private databases remain, and the user can drop them: `tally_w2_breadth`, `tally_w2_w3f`,
`tally_w2_w4`, `tally_w2_w6mcp`, and `tally_freshproof` (origin not recorded). `tally_w2_integ` was
dropped at the end of this merge. The cluster-wide NOLOGIN roles are `agent_ro`, `agent_view_owner` and,
new in wave 2, `agent_binder`.

**4.6 ⛔ pgvector: the compose file was switched; the running `tally_pg` was NOT.** `docker-compose.yml`
names `pgvector/pgvector:pg16-trixie`. `tally_pg` still runs `postgres:16` (16.14) on the volume
`tally-updated_tally_pgdata`. Use the **-trixie** tag. The bare `pg16` tag is Debian 12 (glibc 2.36), and
d122 measured a collation-version mismatch on this volume under it. To switch, with nothing else writing
to the cluster:

```sh
# 1. stop cored / insightd / statutoryd and every lane's tests
# 2. recreate the container on the SAME volume (same major version: no pg_upgrade, no dump)
cd /Users/yashbhalgat/code/tally-updated && docker compose up -d db
#    or, without compose:
#    docker stop tally_pg && docker rm tally_pg && docker run -d --name tally_pg -p 5433:5432 \
#      -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=tally \
#      -v tally-updated_tally_pgdata:/var/lib/postgresql/data pgvector/pgvector:pg16-trixie
# 3. verify: 16.x on Debian 13, and the two collation columns equal for every database
docker exec tally_pg psql -U postgres -c "SELECT version();" \
  -c "SELECT datname, datcollversion, pg_database_collation_actual_version(oid) FROM pg_database;"
#    if any row differs: REINDEX DATABASE <db>; ALTER DATABASE <db> REFRESH COLLATION VERSION;
# 4. databases already migrated past 0098 recorded it with the vector half skipped:
docker exec tally_pg psql -U postgres -d tally -c "SELECT emb_enable_vectors();"   # repeat per database
# 5. make migrate must now log "embeddings: enabled: pgvector ..."
```

This is decision 6.6. The Docker VM is capped at 4 GB, and the image change does not alter that.

---

## 5. Migrations consumed (PLAN-TALLY-PARITY §2.5 is the record)

| Lane | Numbers | Files |
|---|---|---|
| W2 breadth | **0095** | `0095_agent_breadth` (no new table: born-blocked ledger columns and trigger, `agent_commit.object_kind/object_ids`, `agent_decision.edited_preview`) |
| W3 follow-ons | **0098–0099** | `0098_embeddings_and_golden_queries`, `0099_tier_b_tenant_binding` (new cluster role `agent_binder`) |
| W4 | **0102–0103**; 0104 unused, still W4's | `0102_insights`, `0103_ledger_credit_limit` |
| W6 | **0105**; 0106 unused, reserved for WhatsApp/voice | `0105_agent_mcp_grants_and_sessions` |
| W7 | 0107–0109 reserved | none |
| Integrator | none | the composite posting FK needs a number (6.12) |

No wave-2 file writes an ACL rule, so the seeder census is unchanged at 17 PERFORMs. The new route
authorities are all bare tokens: `agent-autonomy`, `master-unblock`, `insights` and `agent-mcp`. All four
are classed governance, so no agent can use any of them.

---

## 6. Next: the order

1. **Remaining breadth, as follow-on lanes. Start now.**
   - **W2:** ledger alteration (it needs a human screen first), groups, units, godowns, price lists,
     BOMs, budgets, cost centres, order line revision, keystroke-recorded parity traces, and a picker
     that marks blocked ledgers.
   - **W3:** per-entity `/query` authorization in the PDP (6.10), then the orders, GST-return and
     cost-centre entities; wire the embedding indexers once 6.6 is settled.
   - **W5:** the HSN directory (W4's `hsn_rate_mismatch` needs it too), counterparty use in the matcher
     (W4's `vendor_bank_change` needs it), and the round-off leg.
   - **Integrator:** the composite posting FK (6.12).
2. **W6 WhatsApp and voice: only after the BSP decision (6.5).** Voice also needs 6.1. The MCP OAuth
   half (PRM, CIMD) can go ahead now.
3. **W7 (learning loop): NOT before at least 6 weeks of real W2/W4 signals.** There are still zero real
   signals: every cassette comes from a fake, and W4's p_real is a declared prior. Start counsel review
   (6.5) now because of its lead time.
4. **W8:** Tier 1/2 evals on W1a's sets, and Langfuse. They run alongside everything else.
5. **W9 (India-resident inference):** gated on the residency decision (6.3).
6. **W1b (bake-off):** needs keys and licences (6.1, 6.4). It is still the biggest unblocker: W3's
   +15 pt, W5's field F1, W2's live cassettes and every W4 model-backed insight wait on it.

---

## 7. Decisions still on the user

The 6.x numbers continue from `HANDOFF-AGENTIC-WAVE-1.md` §7.

- **6.1 ⛔ Bake-off spend and provider keys.** Unchanged.
- **6.3 Residency posture.** Unchanged.
- **6.4 ⛔ GLM-5.3 / Kimi K3 licences.** Unchanged.
- **6.5 WhatsApp BSP vs direct Cloud API, plus counsel.** This now blocks all of the remaining W6 work
  except OAuth.
- **6.6 pgvector as infrastructure.** Compose and CI already moved to `pg16-trixie`. Accepting it means
  running §4.6 on `tally_pg`. Rejecting it means reverting those two files; the store degrades to
  lexical search on its own.
- **6.7 Ratify three W5 behaviour changes.** Unchanged.
- **6.8 Agent defaults, revised by W2 breadth.** Default autonomy L1. Owner approvals need a step-up.
  **"Born blocked" is now a real `ledger.blocked_at` column**, not "exists only after a human decision".
  Ratify each of these:
  - **Every** agent-created ledger is born blocked, not only party ledgers.
  - Untrusted input in a run now forces human approval for **operational** writes too (bank ticks, stock
    vouchers, orders). A bank-statement agent will therefore never tick on its own.
  - `/agent/autonomy` is owners only and always requires a step-up, even for a level granted the token.
- **6.9 NEW: MCP is never approval-free.** Every MCP money, master or statutory commit waits for a human
  at every autonomy level, including L3. Other choices to ratify:
  - The transport route maps to `display agent-mcp`, and each call is re-judged in process.
  - Protocol versions 2025-11-25 / 2025-06-18, not 2026-07-28.
  - A session parked on an approval accepts no other call.
- **6.10 NEW: `/query` bypasses report-level grants.** It is authorized by a single `query` right, so a
  level that has `query` but is denied Bills Payable can still read payables through the outstandings
  entity. The fix belongs in the PDP. The decision is whether to gate per entity (recommended) or accept
  the current rule until then.
- **6.11 NEW: W4 policy.**
  - `insights` is owner-only by default (bare token) and governance-class, so the agent cannot read the
    digest.
  - p_real priors are judgement, not measurement.
  - The `voucher_head` trigger adds one INSERT per voucher post.
- **6.12 NEW: a migration number for the integrator's composite FK** `posting (company_id, ledger_id)`.
  Every number up to 0109 belongs to a lane. Either allocate a new range (for example 0110) or give it to
  W2 breadth's next lane.
- **Carried: the budget-variance deny (0086).**
