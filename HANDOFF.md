# HANDOFF — AI-native, keystroke-compatible Tally competitor

**Purpose of this file:** let a fresh session (clean context) pick up and keep building this project to completion without re-deriving everything. Read this top-to-bottom once, then start at **§7 Next tasks**.

**Last updated:** after commit `d7b970e` (Task A — F9 OCR-draft prefill — shipped; the OCR killer-feature loop closes in-browser). Branch: `master`.

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

**Phase status:** Phase 0 ✅ · Phase 1 ✅ (core + migration thesis complete) · Phase 2 🔄 (flagship OCR ✅ + F9 prefill loop ✅; **statutoryd** and **offline desktop** remain) · Phases 3–4 ⬜.

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

### Task B — `statutoryd`: connected compliance (the conversion lever) — **do this next**
- **Goal:** separate Go deployable + durable workflows for **e-invoice IRN generation** (NIC schema), **e-way bill**, and **Connected-GST direct filing** (GSTR-1/3B/CMP-08 with ARN tracking), behind a multi-homed ProviderAdapter (ClearTax/Masters India primary, NIC IRP secondary) with circuit breakers + 6-hour GSP session auto-renew. Plus the **freeze-on-external-reference rule**: pin a voucher's display number at IRN time; hard-block superseding any voucher already e-invoiced or in a filed GSTR-1 (route to a CA reconciliation queue). This rule **must land with e-invoicing** so offline numbering can never clash with a filed return.
- **Env reality:** no real GSP/NIC endpoints or Temporal cluster here → build behind a `ProviderAdapter` interface with **recorded GSP/NIC schema fixtures** + a deterministic fake; durable-workflow logic testable with an in-process runner or River (already a dep). Schema conformance gated on NIC golden schemas. Add **chaos tests** (GSP outage, token expiry, rate-limit, partial-failure) asserting idempotent resume.
- **Workflow:** `phase2-statutoryd-connected-compliance` — Design (service layout; ProviderAdapter contract; IRN/e-way/GSTR JSON schemas from RESEARCH; freeze-on-external-reference DB rule in cored) → Implement parallel: [statutoryd workflows + adapter + fakes/fixtures] ‖ [cored freeze rule + reconciliation-queue surface] → Review (chaos-gated idempotency; schema conformance; freeze rule actually hard-blocks). Gate: go test (both) + chaos sims + schema validation.

### Task C — Offline-first desktop (the dossier's top offline risk)
- **Goal:** Tauri 2 desktop shell with a full local **SQLite read-model + append-only voucher-version write-outbox** tagged with a Hybrid Logical Clock + node-partitioned UUIDv7; on reconnect the outbox replays through the **same authenticated voucher-post API a human keystroke uses**; provisional display numbers (`INV/2026-27/PROV-<node>-<seq>`) swapped atomically at commit by the cloud sequencer; deterministic conflict winner + surfaced reconciliation queue.
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
3. Task A is DONE (`d7b970e`). Start **Task B** (`phase2-statutoryd-connected-compliance`) — note **Task C's** offline hard-block depends on **Task B's** freeze-on-external-reference rule, so build B before C.
4. Keep the discipline in §3. Verify everything yourself. Commit only green. Update this file and memory as state changes.
