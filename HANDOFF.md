# HANDOFF — AI-native, keystroke-compatible Tally competitor

**Purpose of this file:** let a fresh session (clean context) pick up and keep building this project to completion without re-deriving everything. Read this top-to-bottom once, then start at **§7 Next tasks**.

**Last updated:** after commit `6387f8b` (Task C — offline cloud sequencer + partition fuzzer — shipped). Branch: `master`.

---

## 1. What this is (the one-paragraph thesis)

We are building a **smarter Tally with zero learning curve**: replicate Tally's exact keystroke grammar and screen flows so a real Tally user is productive with no retraining, while underneath it is cloud-native, ACID-correct, and layers the agentic AI Tally structurally lacks. Two moats: (1) **zero-retraining adoption** (keystroke fidelity), and (2) **a CA who can't veto the switch** (rupee-exact migration + compliance). The full vision, architecture, and 5-phase roadmap live in **`PLAN.md`** (grounded in the research dossier **`RESEARCH.md`**). This handoff is the operational layer on top of those.

**Stack:** Go modular monolith `cored` (chi/pgx, Postgres 16, RLS pool tenancy, **immutable voucher-version ledger** as the single reconciliation primitive) · React 18 + XState v5 frontend (keystroke engine) · `insightd` Python/FastAPI AI plane (OCR→voucher, read-only, no ledger write path).

---

## 2. Read-this-first: the docs map

- **`PLAN.md`** — target architecture (Part A), the 10 ADRs (Part B), and the phase-by-phase delivery roadmap with the dynamic workflows per phase (Part C, ~line 1500+). **This is the source of truth for what to build and in what order.**
- **`RESEARCH.md`** — the Tally research dossier. §21 keystroke table is the fidelity reference; §19 is the Tally XML/migration surface; the GST sections drive tax derivation. Cite it when building anything user-facing.
- **Memory** (`~/.claude-work/.../memory/`): `tally-build-state.md` (current progress + gaps), `tally-build-approach.md` (how to work), `MEMORY.md` (index). These load automatically; keep them updated as you go.
- **This file (`HANDOFF.md`)** — the operational runbook + next-tasks queue.

---

## 3. NON-NEGOTIABLE working method (this is why the build is trustworthy)

The single most important thing that has kept quality high: **re-verify every gate yourself; never trust a workflow's or agent's self-report.** Seven real bugs this project found were invisible to "green" self-reports and only surfaced by booting the real stack and adversarially reviewing. Specifically:

1. **Orchestrate with dynamic workflows, but YOU are the integrator.** The proven pattern (use it for every substantial increment):
   - **Design** (1 agent): reads PLAN/RESEARCH + the relevant code, produces a *structured* spec with **pinned contracts** (exact types/JSON shapes both implementers must follow verbatim).
   - **Implement** (parallel agents on **disjoint file sets** — no worktrees needed when files don't overlap): each builds against the pinned spec and self-verifies with real commands.
   - **Review** (parallel adversarial agents): each is told to be a skeptic, re-runs the gates, and tries to *refute* the work (oracle circularity, invariant violations, vacuous tests).
   - **Integrate (you, in the main loop):** read the full result, fix any `mustFix`, then **re-run the entire gate matrix yourself** (build/vet/test + a live e2e) before committing.
2. **Adversarial verification catches what green tests miss.** Always include a reviewer that checks: are oracles *independent* (not the system's own output echoed back)? Are invariants *structurally* enforced (by a test, not a comment)? Do "accuracy/coverage" numbers measure the real thing?
3. **Commit only verified-green work, directly to `master`**, with a clear multi-line message that states the gates you re-ran. End commits with:
   `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
4. **Be honest about scope in the commit/PR.** Disclosed SKIPs/caveats (e.g. "accuracy floor runs on fakes, not real models") beat a green that overclaims.
5. **Scope each workflow to a coherent increment.** Disjoint file sets → parallel agents safely. Pin the data contracts in the design phase so parallel agents don't diverge.
6. **Money is never a float.** NUMERIC(19,4)/integer-paise in Go; fixed-4 decimal **strings** on the wire and in TS. Reconciliation is paisa-exact.
7. **Keystroke fidelity is law.** Any user-facing flow must match Tally (RESEARCH §21). When in doubt, research, then build.

---

## 4. Current state (what's built, committed, and GREEN)

Commits on `master` (newest first):
- `6387f8b` — **Task C: offline cloud sequencer + property-based partition fuzzer.** New `internal/sync` commits a desktop node's outbox through the existing ledger path: offline CREATEs get final numbers from the atomic allocator (zero collisions by construction), conflicts resolve by (HLC,node-id) order (loser parked + surfaced with payload), and Task B's freeze rule hard-blocks superseding an e-invoiced voucher. Gate: a 200-seed fuzzer (mutation-tested). Migration 0010, `POST /sync/commit`. Tauri/SQLite/TS-client deferred.
- `4cc91eb` — **Task B: statutoryd IRN + cored freeze-on-external-reference.** Separate `cmd/statutoryd` Go plane (ProviderAdapter + fake + recorded NIC schema + in-process durable runner, chaos-tested no-double-IRN) mints an IRN and calls cored's freeze hook; cored migration 0009 + `ledger.post()` freeze gate hard-blocks alter/delete of an IRN-pinned voucher → reconciliation queue. Live e2e `backend/scripts/e2e-statutory-freeze.py`.
- `d7b970e` — **Task A: F9 OCR-draft prefill** — OCR→insightd draft→prefilled F9→Ctrl+A→cored→trial balance, in-browser, HITL kept structural (accept is the only write). New `api/draft.ts`+`api/insightd.ts`+`vouchers/draftAccept.ts`+`app/LoadedDraft.tsx`, VoucherScreen draft mode, live `pnpm e2e:ocr`.
- `4628634` — **Phase 2 flagship: `insightd`** OCR→voucher pipeline (HITL, no AI write path, PII boundary).
- `258e40b` — Migration story closed: bill-wise outstandings + edit/delete on import; all 5 corpora reconcile.
- `619af10` — Rupee-exact **four-statement** migration reconciliation (TB/BS/P&L/GSTR-1/Outstandings) + company GST state.
- `fc3da77` — GET single-voucher endpoint + GST entry in the F8/F9 form.
- `6b9e72a` — Tally auto-numbering + in-browser screens driving live `cored`.
- `a69ca07` — Edit-loop closure: voucher Alter/Delete over HTTP (immutable versioning).
- `395bff3` — Phase 1 frontend (6 voucher screens, reports, masters).
- `156ba7c` / `8d26b07` / `ce2d213` — Phase 1 backend / Phase 0 spine / scaffold.

**Product capabilities that work end-to-end and are independently verified:**
- A Tally user drives all 6 voucher types (F4–F9) in-browser against live `cored`: **Tally auto-numbering** (FY-scoped, gap-on-delete, no-realloc-on-alter), **GST split** (intra→CGST+SGST, inter→IGST, server-derived), **immutable edit/delete** as hash-chained versions, **drill-to-alter** (loads real ledger ids via `GET /vouchers/{id}`), reports with drill-down, master creation.
- A CA migrates a Tally XML export and **reconciles TB / Balance Sheet / P&L / GSTR-1 / Outstandings to the rupee** (`migration.ReconcileFull`), across opening balances, multi-state GST, bill-wise bills, edit/delete-on-import, and rounding — against an **independently hand-derived** oracle (verified non-circular).
- A purchase bill flows through `insightd` (OCR → Haiku classify → Opus infer → PII redact) into a pre-filled **F9 Purchase draft** awaiting Ctrl+A, with **human-in-the-loop** (no ledger write path) and **PII-never-leaves-India** invariants enforced by tests.
- **The full OCR loop now closes in-browser** (`d7b970e`): the F9 Purchase screen consumes an `insightd` draft (prefill + per-field amber confidence + redaction proof + derived-GST preview), and Ctrl+A posts the balanced body verbatim through the **existing** voucher API — verified by the live `pnpm e2e:ocr` (cored+insightd): draft carries the real cored party id, trial balance is all-zero before accept (insightd wrote nothing), then balanced to the rupee after.

- **Connected compliance — IRN + freeze (`4cc91eb`):** a separate `statutoryd` Go deployable mints an e-invoice IRN through a durable, idempotent, chaos-tested workflow (no double-IRN across provider outage/token-expiry/rate-limit/partial-failure) and calls cored's freeze hook; cored then **pins the voucher's display number and hard-blocks any later alter/delete** (→ reconciliation queue). This is the freeze-on-external-reference primitive Task C's offline reconciliation depends on. Proven by the live two-binary e2e + the chaos gate.

- **Offline cloud sequencer (`6387f8b`):** a desktop node's append-only outbox commits through the same authenticated ledger path a keystroke uses; the sequencer assigns the final display number (zero collisions, by routing through the existing atomic allocator), resolves document-level conflicts by (HLC,node-id) total-order (loser parked + surfaced with its payload), enforces exactly-once replay, and reuses Task B's freeze rule to hard-block superseding an e-invoiced voucher. Proven by a 200-seed property-based partition fuzzer (mutation-tested to confirm it bites). The actual Tauri/SQLite client is deferred — the correctness novelty (the sequencer) is what shipped.

**Phase status:** Phase 0 ✅ · Phase 1 ✅ (core + migration thesis complete) · Phase 2 ✅ for the build-out targets here (OCR loop ✅ + F9 prefill ✅ + statutoryd IRN+freeze ✅ + offline sequencer ✅); **deferred within Phase 2:** the Tauri/PWA client shells, e-way/GSTR-filing, and Task D (auth). Phases 3–4 ⬜.

---

## 5. Runbook (exact commands — Postgres `tally_pg` runs on port 5433)

**Backend (`cored`):**
```bash
cd backend
go run ./cmd/cored migrate            # apply embedded migrations (idempotent; 8 files)
go build ./... && go vet ./...        # must be clean
go test ./... -count=1                # full suite (hits live DB on :5433)
# boot the server for live e2e:
go build -o /tmp/cored ./cmd/cored && \
  PORT=8087 APP_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" /tmp/cored serve &
```

**Frontend:**
```bash
cd frontend
pnpm exec tsc --noEmit                          # typecheck (also: -p scripts/tsconfig.json)
pnpm exec vitest run                            # unit/component tests
pnpm run build                                  # tsc + vite build
# live e2e against a running cored on :8087:
CORED_BASE_URL=http://localhost:8087 pnpm e2e:edit                 # edit/delete loop
CORED_BASE_URL=http://localhost:8087 pnpm exec tsx scripts/e2e-numbering.mts   # auto-numbering
# OCR-prefill loop (needs BOTH cored :8087 and insightd :8088 running, see below):
CORED_BASE_URL=http://localhost:8087 INSIGHTD_BASE_URL=http://localhost:8088 pnpm e2e:ocr
```

**insightd server (for the OCR-prefill e2e):**
```bash
cd insightd && . .venv/bin/activate     # venv set up per the insightd block below
INSIGHTD_PII_BOUNDARY_STRICT=1 python -m uvicorn insightd.api.app:app --port 8088 &
```

**statutoryd (IRN plane) + the freeze e2e:**
```bash
cd backend && go build -o /tmp/statutoryd ./cmd/statutoryd
CORED_BASE_URL=http://localhost:8087 PORT=8091 /tmp/statutoryd &        # IRN plane, calls cored's freeze hook
go test ./internal/statutory/... -run Chaos -v                          # no-double-IRN chaos gate
python3 backend/scripts/e2e-statutory-freeze.py                         # live: IRN -> freeze -> 409 alter/delete -> recon queue
```

**Offline sync sequencer (Task C):**
```bash
cd backend && go test ./internal/sync -run Fuzz -v -count=1   # THE GATE: 200-seed partition fuzzer (~90s, DB-backed on :5433)
# also: go test ./internal/sync -count=1   (sequencer unit + HTTP /sync/commit tests)
```

**insightd (Python AI plane):**
```bash
cd insightd
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt pytest          # CORE deps only — no anthropic/paddle/presidio
python -m pytest                                # 49 tests; fakes only, no API key needed
python -m insightd.benchmark.validate           # corpus validator (28 fixtures)
python -m insightd.benchmark.report             # field-level accuracy floor gate
```

**Migration reconciliation corpora:** `corpora/migration/*.{xml,expected.json}` (5 datasets, all reconcile four statements to the rupee). Tax golden corpora: `corpora/tax/*.json` (shared oracle for both the Go backend and insightd's GST cross-oracle test).

**`make verify`** runs the Phase-0 gate set; extend it as new planes mature.

---

## 6. Known gaps / tracked follow-ups (carry these forward)

🔴 / 🟡 = priority. None of these block what's committed; they're the honest edges.

- ✅ **DONE (`d7b970e`) — Frontend F9 OCR-draft prefill wired.** The F9 Purchase screen consumes `body + confidence + unresolved + status + redaction + derived_gst`; Ctrl+A posts the balanced body verbatim via the existing voucher API (`acceptPurchaseDraft`). Follow-up: draft-mode prefill is **display-only** — editing a prefilled field does not yet alter the POSTed body (a `ready` draft is accept-as-is); wire body-editing if operators need to correct before accept. Also: no UI entry to *upload a bill / pick a draft_id* yet (draft is injected via `LoadedDraft` context; the e2e/tests seed it directly) — add an upload affordance + nav when building the bill-inbox screen.
- 🟡 **`insightd` accuracy floors run on deterministic fakes** — they measure the *mapper* + invariants, not real OCR/LLM accuracy. The `--real` calibration mode (PaddleOCR + Anthropic key) is the pre-release model-accuracy gate and does **not** gate merges. Wire `tests/test_gst_oracle_reconcile.py` (the independent GST signal vs Go golden corpora) into CI checkout of the repo root.
- 🟡 **`insightd` PII:** `pii_presidio.py` is constructed but the regex floor (`redaction/recognizers.py`) is authoritative; flesh out Presidio span-adding as an accuracy upgrade. Add a Postgres-backed `DraftStore` (currently in-memory).
- 🟡 **Frontend supplier-state:** `gstVoucher.ts` still sends a hardcoded `"29"`; should send the company's real `state_code` (or omit → server default from `company.state_code`, which is already correct as of `619af10`) and pass `state_code` at company bootstrap. Affects non-Karnataka companies in-browser.
- 🟡 **Auth not built:** Keycloak + Tally Disallow/Allow levels + WebAuthn + account lockout (Phase 1 deliverable, deferred). A real multi-user deployment switch-blocker. (Task D.)
- 🟡 **GSTR-3B** not built (only GSTR-1). Tauri desktop + web PWA TanStack shells not built (web is plain Vite).
- Minor/untested: DELETE idempotency (re-delete appends another tombstone, returns 200); altering a deleted voucher silently "undeletes"; mock per-class number formats are frontend approximations; the `.NET` signed Windows migration *agent* doesn't exist (the importer logic does).

---

## 7. NEXT TASKS (priority-ordered — each with the dynamic workflow to spin up)

> Reminder: for each, follow §3 — pin contracts in design, parallel-implement on disjoint files, adversarial-review, then **you integrate + re-run every gate + a live e2e** before committing. The workflow template is in §9.

### Task A — Wire the frontend F9 OCR-draft prefill — ✅ DONE (`d7b970e`)
Shipped: `api/draft.ts` (contract mirror) + `api/insightd.ts` (thin client) + `vouchers/draftAccept.ts` (`draftBodyToVoucherRequest`/`resolveDraftLedgers`/`acceptPurchaseDraft`) + `app/LoadedDraft.tsx` + VoucherScreen draft mode + `DraftPrefillPanel`. Live `pnpm e2e:ocr` proves the loop end-to-end (HITL: zero writes before accept). Key insight that made it clean: the insightd `body` is already a balanced cored payload, so accept posts it verbatim (no client-side tax math); cored's GST verify sums by tax-ledger id ignoring Dr/Cr so input-tax DEBITs pass. Remaining follow-ups moved to §6. **Next: Task B.**

### Task B — `statutoryd`: connected compliance — ✅ DONE (`4cc91eb`, IRN + freeze slice)
Shipped the IRN e-invoice + freeze vertical slice: separate `cmd/statutoryd` plane (ProviderAdapter + deterministic fake + recorded NIC golden schema + hand-written conformance validator + an **in-process durable runner** — River/Temporal are NOT deps, so a StepStore with resume/exactly-once was built instead) → chaos-gated no-double-IRN → calls cored's freeze hook. cored side: migration 0009 (`voucher_external_reference` + `reconciliation_queue`), `ledger.post()` freeze gate, `RegisterExternalReference`/`RaiseReconciliation`/`ListReconciliationQueue`, the freeze-hook + reconciliation-queue endpoints, 409 mapping. Live two-binary e2e + chaos gate green. **Deferred follow-ups** (moved to §6): e-way bill, full GSTR-1/3B/CMP-08 filing with ARN tracking, a Postgres-backed StepStore (durability across a statutoryd restart), real GSP/NIC ProviderAdapter + multi-homing/circuit-breakers, and `FILED_GSTR1` freeze wired from an actual filing (the kind is supported; only IRN is wired end-to-end). **Next: Task C.**

### (was Task B — now done; see above)
- **Goal:** separate Go deployable + durable workflows for **e-invoice IRN generation** (NIC schema), **e-way bill**, and **Connected-GST direct filing** (GSTR-1/3B/CMP-08 with ARN tracking), behind a multi-homed ProviderAdapter (ClearTax/Masters India primary, NIC IRP secondary) with circuit breakers + 6-hour GSP session auto-renew. Plus the **freeze-on-external-reference rule**: pin a voucher's display number at IRN time; hard-block superseding any voucher already e-invoiced or in a filed GSTR-1 (route to a CA reconciliation queue). This rule **must land with e-invoicing** so offline numbering can never clash with a filed return.
- **Env reality:** no real GSP/NIC endpoints or Temporal cluster here → build behind a `ProviderAdapter` interface with **recorded GSP/NIC schema fixtures** + a deterministic fake; durable-workflow logic testable with an in-process runner or River (already a dep). Schema conformance gated on NIC golden schemas. Add **chaos tests** (GSP outage, token expiry, rate-limit, partial-failure) asserting idempotent resume.
- **Workflow:** `phase2-statutoryd-connected-compliance` — Design (service layout; ProviderAdapter contract; IRN/e-way/GSTR JSON schemas from RESEARCH; freeze-on-external-reference DB rule in cored) → Implement parallel: [statutoryd workflows + adapter + fakes/fixtures] ‖ [cored freeze rule + reconciliation-queue surface] → Review (chaos-gated idempotency; schema conformance; freeze rule actually hard-blocks). Gate: go test (both) + chaos sims + schema validation.

### Task C — Offline-first desktop — ✅ DONE (`6387f8b`, cloud sequencer + fuzzer slice)
Shipped the correctness core: `internal/sync` Service.Commit (offline outbox → existing ledger path; final numbering via the atomic allocator = zero collisions; (HLC,node-id) deterministic conflict resolution with the loser parked + surfaced in the reconciliation queue with its payload; exactly-once replay via `sync_applied`; Task B freeze hard-block reused), deterministic HLC + node-partitioned UUIDv7 + offline Node/Outbox model, `ledger.PostVoucherWithID`/`HeadVersion`, migration 0010, `POST /sync/commit`. Gate: a **200-seed property-based partition fuzzer** that forces non-vacuity (a conflict target + an IRN-frozen target) and was mutation-tested. **Deferred follow-ups** (moved to §6): the actual **Tauri 2 Rust shell + local SQLite read-model/outbox persistence + a TS replay client** (the sequencer is the cloud side; the client is mature packaging that drives `POST /sync/commit`), web PWA TanStack shell, and sequencer HA/leader-election. **Next: Task D.**

### (old Task C detail — Tauri client, now a deferred follow-up of the shipped sequencer)
- **Goal (deferred client):** Tauri 2 desktop shell with a full local **SQLite read-model + append-only voucher-version write-outbox** tagged with a Hybrid Logical Clock + node-partitioned UUIDv7; on reconnect the outbox replays through the **same authenticated voucher-post API a human keystroke uses**; provisional display numbers (`INV/2026-27/PROV-<node>-<seq>`) swapped atomically at commit by the cloud sequencer; deterministic conflict winner + surfaced reconciliation queue.
- **Verification (this is the whole point):** a **network-partition fuzzer** (property-based) creating concurrent offline edits, days-long offline branches, and edits to already-e-invoiced vouchers — asserting **zero number collisions**, deterministic winner, loser preserved as a superseded version, and a **hard-block on filed/e-invoiced vouchers** (depends on Task B's freeze rule). Gate this behind the fuzzer before any customer goes offline.
- **Workflow:** `phase2-offline-outbox-fuzzed` — Design (graded offline model per PLAN A.1 #3; outbox schema; HLC; cloud sequencer; conflict resolution) → Implement parallel: [local SQLite store + outbox replay] ‖ [cloud sequencer + provisional→final numbering + conflict queue] → Review (property-based partition fuzzer; invariants). Gate: the fuzzer must pass with zero collisions.

### Task D — Auth: Keycloak + Disallow/Allow + WebAuthn (deployment switch-blocker)
- **Goal:** Keycloak 26 (OIDC/OAuth2/SAML/SCIM, TOTP/WebAuthn) + Postgres ACL tables replicating Tally's Owner/Data-Entry/custom Disallow-Allow levels + OPA/Rego for cross-cutting policy + mandatory WebAuthn for Owner/Admin + account lockout + back-dated cutoff. Migrating CAs keep the exact mental model; adds the MFA/SSO Tally lacks.
- **Workflow:** `phase1-auth-disallow-allow` — Design (ACL schema mirroring Tally levels; JWT→GUC company scoping already exists; PDP eval points) → Implement (Keycloak integration + ACL tables + middleware + WebAuthn) → Review (security-adversarial: privilege escalation, cross-tenant, RLS still enforced). Gate: go test + an authz test matrix.

### Smaller, do-anytime cleanups (good warm-ups or fillers)
- Wire `insightd/tests/test_gst_oracle_reconcile.py` into CI (repo-root checkout). Frontend supplier-state fix (§6). GSTR-3B (mirror GSTR-1, golden-corpus-gated). Decide DELETE idempotency policy + test it.

---

## 8. Roadmap to "done" (after Phase 2)

Per `PLAN.md` Part C — sequence, don't skip:
- **Phase 3 — depth that widens the wedge:** NL→typed-DSL→read-only-replica conversational query (never raw SQL; SQL-injection-adversarial gated), predictive cash-flow forecast + anomaly detection (deterministic XGBoost/statsmodels, backtest-gated — *not* LLM vibes), TDS/TCS in the TaxDeriveEngine, Payroll (Form 24Q/PF/ESI, FVU-validated), inventory depth (batches/expiry, price lists, order/delivery/receipt notes), ClickHouse OLAP + Kafka at the CDC seam, IMS + GSTR-2A/2B recon. Each as a golden-corpus- or backtest-gated workflow.
- **Phase 4 — extensibility & ecosystem moat:** FlowDef declarative DSL (JSON-Schema→versioned IR) for ~80% of customizations + V8 isolate/Wasmtime sandboxes for ~20% (capability-manifest, red-team-gated), Claude-authored extension copilot, hardened `/tally` ENVELOPE+JSONEX ingress so the existing Tally connector ecosystem (Suvit/Webtel/tally-database-loader) works unchanged over authenticated HTTPS (compatibility-differential-gated), first-party webhooks, group-company consolidation.

The critical path: Phase 2 (OCR ✅ + connected compliance + offline) is the time-to-market lever; Phases 3–4 are largely parallelizable post-launch and widen the wedge.

---

## 9. Dynamic-workflow authoring template (the pattern that works)

```js
export const meta = {
  name: 'phaseN-thing',
  description: '…one line…',
  phases: [{ title: 'Design' }, { title: 'Implement' }, { title: 'Review' }],
}
const REPO = '/Users/yashbhalgat/code/tally-updated'

phase('Design')
const SPEC = await agent(`…read PLAN/RESEARCH + the exact files; PIN the contracts
  (exact Go structs / Pydantic models / TS types both implementers follow verbatim);
  specify env-realistic verification (fakes/fixtures where no real service/key exists).`,
  { label: 'design:thing', phase: 'Design', schema: {/* structured spec */} })
const specText = JSON.stringify(SPEC, null, 2)

phase('Implement')
const [a, b] = await parallel([            // DISJOINT file sets → no worktrees needed
  () => agent(`Implement half A per spec VERBATIM. Self-verify with REAL commands;
               report only what you observed; don't claim green you didn't run.`,
              { label: 'impl:A', phase: 'Implement', schema: IMPL_SCHEMA }),
  () => agent(`Implement half B per the SAME pinned contracts. …`,
              { label: 'impl:B', phase: 'Implement', schema: IMPL_SCHEMA }),
])

phase('Review')
const [ra, rb] = await parallel([          // adversarial: be a skeptic, RE-RUN gates
  () => agent(`Adversarially review A: invariants structurally enforced? oracle
               independent (not circular)? tests non-vacuous? Re-run the gates and
               report the REAL result. Reject on any violation.`,
              { label: 'review:A', phase: 'Review', schema: REVIEW_SCHEMA }),
  () => agent(`Adversarially review B …`, { label: 'review:B', phase: 'Review', schema: REVIEW_SCHEMA }),
])
return { spec: SPEC, a: { impl: a, review: ra }, b: { impl: b, review: rb } }
```
Then **you** (main loop): read the result, apply any `mustFix`, **re-run the full gate matrix + a live e2e yourself**, write a clear commit, update `HANDOFF.md` + memory.

**Workflow guardrails learned this project:**
- Pin contracts in design or parallel agents diverge.
- Tell implementers to report only observed results; tell reviewers to re-run gates and try to refute.
- The accuracy/coverage number is only meaningful if the oracle is independent and the adversarial cases would actually fail a regression — make the reviewer prove that.
- Disjoint files = safe parallelism without worktrees. Use `isolation: 'worktree'` only when agents mutate the same files.

---

## 10. First moves for the next session

1. Read this file + skim `PLAN.md` Part C (Phase 2 section) + the memory index.
2. Bring up the stack (§5), run the full gate matrix once to confirm everything is still green from a clean clone state.
3. Tasks A (`d7b970e`), B (`4cc91eb`), and C (`6387f8b`) are DONE. Start **Task D** (`phase1-auth-disallow-allow`) — Keycloak + Tally Disallow/Allow ACLs + WebAuthn + account lockout; the deployment switch-blocker for multi-user. (Or, if a customer-facing demo is the priority, pick up a deferred client shell: the Tauri desktop driving `POST /sync/commit`, or the F9 bill-upload UI from §6.)
4. Keep the discipline in §3. Verify everything yourself. Commit only green. Update this file and memory as state changes.
