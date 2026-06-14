# HANDOFF — AI-native, keystroke-compatible Tally competitor

**Purpose of this file:** let a fresh session (clean context) pick up and keep building this project to completion without re-deriving everything. Read it top-to-bottom once, then start at **§7 Next tasks**.

**Last updated:** after commit `19d2fa6` (Task D — server-side auth: JWT + Disallow/Allow PDP + WebAuthn + lockout). Branch: `master`. Tasks A, B, C, **D** of the Phase-2 queue are done; **a client shell (Tauri desktop or F9 bill-inbox UI) is the next high-value increment** (see §7).

---

## 1. What this is (the one-paragraph thesis)

We are building a **smarter Tally with zero learning curve**: replicate Tally's exact keystroke grammar and screen flows so a real Tally user is productive with no retraining, while underneath it is cloud-native, ACID-correct, and layers the agentic AI Tally structurally lacks. Two moats: (1) **zero-retraining adoption** (keystroke fidelity), and (2) **a CA who can't veto the switch** (rupee-exact migration + compliance). The full vision, architecture, and 5-phase roadmap live in **`PLAN.md`** (grounded in the research dossier **`RESEARCH.md`**). This handoff is the operational layer on top of those.

**Stack (3 planes):**
- **`cored`** — Go modular monolith (chi + pgx, Postgres 16, RLS pool tenancy). The system of record. Its single reconciliation primitive is an **immutable, hash-chained voucher-version ledger** (alter/delete = new versions; the MCA Edit Log is an architectural invariant). Now also hosts the **freeze-on-external-reference rule** (Task B) and the **offline sync sequencer** (Task C).
- **`statutoryd`** — separate Go deployable (`backend/cmd/statutoryd`) for connected compliance. Currently: IRN e-invoice generation via a durable, idempotent workflow behind a `ProviderAdapter`, calling cored's freeze hook. Talks to cored only over HTTP (archtest-enforced).
- **`insightd`** — Python/FastAPI AI plane (OCR→purchase-voucher draft). Read-only relationship with cored; **no ledger write path** (human-in-the-loop is structural).
- **Frontend** — React 18 + XState v5 keystroke engine (plain Vite web today; Tauri/PWA shells deferred).

**Money is never a float:** NUMERIC(19,4) / integer-paise in Go; fixed-4 decimal **strings** on the wire and in TS. Reconciliation is paisa-exact.

---

## 2. Read-this-first: the docs map

- **`PLAN.md`** — target architecture (Part A), the 10 ADRs (Part B), the phase-by-phase roadmap with per-phase dynamic workflows (Part C). **Source of truth for what to build and in what order.**
- **`RESEARCH.md`** — the Tally research dossier. §21 keystroke table = fidelity reference; §19 = Tally XML/migration surface; GST sections drive tax derivation. Cite it for anything user-facing.
- **Memory** (`~/.claude-work/.../memory/`, loads automatically): `MEMORY.md` (index), `tally-build-state.md` (current progress + gaps — keep current), `tally-build-approach.md` (how to work), plus per-task scope notes `task-a-ocr-prefill-integration.md`, `task-b-statutoryd-scope.md`, `task-c-offline-sequencer-scope.md`.
- **This file (`HANDOFF.md`)** — operational runbook + next-tasks queue.

---

## 3. NON-NEGOTIABLE working method (this is why the build is trustworthy)

The single most important thing that has kept quality high: **re-verify every gate yourself; never trust a workflow's or agent's self-report.** Real bugs this project found were invisible to "green" self-reports and only surfaced by booting the real stack and adversarially reviewing.

1. **Orchestrate with dynamic workflows, but YOU are the integrator.** The proven pattern (every substantial increment):
   - **Design** (1 agent): reads PLAN/RESEARCH + the exact code, emits a *structured* spec with **pinned contracts** (exact Go structs / Pydantic models / TS types / JSON + HTTP shapes every implementer follows verbatim). Have it *validate* the integrator's pinned brief against real code and return corrections only.
   - **Implement** (parallel agents on **disjoint file sets** — no worktrees needed when files don't overlap): build to the pinned spec; self-verify with REAL commands; report only observed results.
   - **Review** (parallel adversarial agents): be a skeptic, RE-RUN the gates, try to *refute* (oracle circularity, invariant violations, vacuous tests). The best review this project saw **mutation-tested** the code (injected bugs, confirmed the gate caught each, restored byte-identical).
   - **Integrate (you, main loop):** read the full result, apply any `mustFix`, **re-run the entire gate matrix yourself + a live e2e**, then commit.
2. **Adversarial verification catches what green tests miss.** Always include a reviewer that checks: are oracles *independent* (not the system echoing itself)? Are invariants *structurally* enforced (by a test, not a comment)? Would the "gate" actually fail on a regression (is it non-vacuous)?
3. **Commit only verified-green work, directly to `master`,** with a clear multi-line message stating the gates you re-ran. End commits with:
   `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
4. **Be honest about scope.** Disclosed SKIPs/caveats beat a green that overclaims. Note every deferral in §6.
5. **Scope each workflow to a coherent increment.** Pin data contracts in design so parallel agents don't diverge. Disjoint files = safe parallelism.
6. **Money is never a float** (see §1). **Keystroke fidelity is law** — match Tally (RESEARCH §21); research, then build.
7. **De-risk before building.** Before launching a workflow, read the real code and (where possible) smoke-test the integration path live (e.g. a curl/Python loop against the running stack), so the pinned contracts are correct. This caught several would-be integration gaps.

---

## 4. Current state (what's built, committed, and GREEN)

Commits on `master` (newest first; `docs:` commits between feature commits omitted):
- `e43d383` — **WebAuthn step-up + lockout HTTP routes (Task-D follow-up) + F9 bill-inbox UI** (client shell; OCR loop now demoable end-to-end).
- `19d2fa6` — **Task D: server-side auth** — JWT (Ed25519/ES256) + Tally Disallow/Allow PDP + WebAuthn + lockout; fail-closed.
- `6387f8b` — **Task C: offline cloud sequencer + property-based partition fuzzer.**
- `4cc91eb` — **Task B: statutoryd IRN e-invoice + cored freeze-on-external-reference.**
- `d7b970e` — **Task A: F9 OCR-draft prefill** (the OCR killer-feature loop closes in-browser).
- `4628634` — Phase 2 flagship: `insightd` OCR→voucher pipeline (HITL, no AI write path, PII boundary).
- `258e40b` — Migration story closed: bill-wise outstandings + edit/delete on import; all 5 corpora reconcile.
- `619af10` — Rupee-exact **four-statement** migration reconciliation (TB/BS/P&L/GSTR-1/Outstandings) + company GST state.
- `fc3da77` — GET single-voucher endpoint + GST entry in the F8/F9 form.
- `6b9e72a` — Tally auto-numbering + in-browser screens driving live `cored`.
- `a69ca07` — Edit-loop closure: voucher Alter/Delete over HTTP (immutable versioning).
- `395bff3` — Phase 1 frontend (6 voucher screens, reports, masters).
- `156ba7c` / `8d26b07` / `ce2d213` — Phase 1 backend / Phase 0 spine / scaffold.

**Capabilities that work end-to-end and are independently verified:**
1. **Keystroke entry loop (Phase 1):** a Tally user drives all 6 voucher types (F4–F9) in-browser against live `cored` — FY-scoped auto-numbering (gap-on-delete, no-realloc-on-alter), server-derived GST split (intra→CGST+SGST, inter→IGST), immutable edit/delete as hash-chained versions, drill-to-alter (real ledger ids via `GET /vouchers/{id}`), reports with drill-down, master creation.
2. **Migration thesis (Phase 1):** a CA migrates a Tally-XML export and `migration.ReconcileFull` ties **TB / Balance Sheet / P&L / GSTR-1 / Outstandings to the rupee** across opening balances, multi-state GST, bill-wise bills, edit/delete-on-import, and rounding — against an independently hand-derived (verified non-circular) oracle. All 5 corpora pass as unconditional gates.
3. **OCR killer-feature loop (Task A + insightd):** a purchase bill → `insightd` (OCR → Haiku classify → Opus infer → PII redact) → `PurchaseVoucherDraft` → the **F9 Purchase screen prefills** (per-field amber confidence, unresolved focus hints, PII-redaction proof, derived-GST preview) → **Ctrl+A posts the balanced body verbatim through the EXISTING voucher API**. HITL is structural (insightd has no ledger write path; accept is the only write). Live `pnpm e2e:ocr` proves it: draft carries the real cored party id; trial balance is all-zero before accept; balanced to the rupee after.
4. **Connected compliance — IRN + freeze (Task B):** `statutoryd` mints a deterministic e-invoice IRN through a durable, idempotent, **chaos-tested** workflow (no double-IRN across provider outage / token-expiry / rate-limit / partial-failure) and calls cored's freeze hook. cored then **pins the voucher's display number and hard-blocks any later alter/delete** (→ reconciliation queue). Live two-binary e2e + chaos gate green.
5. **Offline cloud sequencer (Task C):** a desktop node's append-only outbox commits through the same authenticated ledger path a keystroke uses. The sequencer assigns the **final** display number (zero collisions, by routing offline CREATEs through the existing atomic allocator with an empty `voucher_no`), resolves document-level conflicts by **(HLC, node-id) total order** (loser parked + surfaced in the reconciliation queue with its full payload), enforces **exactly-once replay** (`sync_applied`), and reuses Task B's freeze rule to hard-block superseding an e-invoiced voucher. Proven by a **200-seed property-based partition fuzzer** (mutation-tested to confirm it bites).

6. **Server-side auth — JWT + Disallow/Allow PDP + WebAuthn + lockout (Task D + follow-up):** every request is now authenticated and authorized server-side, **fail-closed**. A verified compact-JWS Bearer (Ed25519/ES256, stdlib-only; real Keycloak via `AUTH_JWKS_URL` is the documented swap) establishes the caller; **membership in `{companyID}` is the tenant gate** (the URL path param no longer drives `db.WithCompany` — the authz-verified company is injected into ctx); a Go **PDP** evaluates Tally **Disallow/Allow** rules (disallow beats allow at equal specificity; exact beats wildcard; `default_access` full/none), **back-dated cutoff** on writes, and **WebAuthn step-up** requirement (distinguishable `403 STEPUP_REQUIRED`; `401` reserved for missing/invalid/expired tokens). Auth catalog (migration 0011) is FORCE-RLS + explicit WITH CHECK on new `app_current_user_id()`/`app_current_subject()` GUC resolvers. `svc:*` service principals get signature-gated full access scoped to the target company (statutoryd freeze hook + sync). **The WebAuthn ceremony + lockout are now WIRED as HTTP routes (`e43d383`):** `POST /auth/webauthn/register|assert/{begin,finish}` (single-use user-bound challenge; assert verifies against the registered credential and ONLY THEN mints an `amr:["webauthn"]` step-up token) and a dev `POST /auth/login` exercising lockout (5 bad → `423`). Proven by an authz matrix (mutation-tested), cross-tenant 403 tests, a fail-closed regression test, and a **hermetic integration test (live PG, mutation-tested) where a write goes 403 STEPUP_REQUIRED → register+assert → stepped-up token → 201**; plus live curl (lockout fires on the 5th attempt, routes 401-gated).
7. **F9 Bill Inbox UI (client shell, `e43d383`):** a Gateway-reachable `BillInboxScreen` lists pending OCR drafts (insightd `GET /companies/{id}/drafts` → server-derived `DraftSummary`) and offers a create-from-fixture affordance; selecting a draft loads it into the `LoadedDraft` context and opens the F9 Purchase voucher in prefill mode (the existing accept path posts the balanced body verbatim). Turns the Task-A OCR loop into a demoable end-to-end feature. The nav machine stays **pure** (a `reduceSelect` sentinel branch pushes the purchase-create frame); HITL holds (the inbox never writes to cored). Verified by a non-vacuous vitest + live `e2e:ocr` + a live list-route curl (company-scoped, no leak).

**DB migrations:** `backend/migrations/0001…0011` (embedded, applied lexically by `cored migrate`). 0009 = freeze (external-reference + reconciliation_queue); 0010 = offline sync (`sync_applied` + `reconciliation_queue.payload`); 0011 = auth catalog (app_user, security_level, acl_rule, company_membership, webauthn_credential, auth_login_attempt; user/subject GUC resolvers; FORCE RLS).

**Phase status:** Phase 0 ✅ · Phase 1 ✅ (core + migration thesis) · Phase 2 ✅ on the time-to-market targets (OCR loop, F9 prefill + **bill-inbox**, statutoryd IRN+freeze, offline sequencer, **auth + WebAuthn step-up/lockout routes**); **deferred within Phase 2:** the **Tauri 2 desktop shell** (no Rust toolchain here) + web PWA shell, e-way bill + full GSTR filing, real Keycloak JWKS + OPA, and a shared (multi-instance) WebAuthn challenge store (see §6). Phases 3–4 ⬜.

---

## 5. Runbook (exact commands — Postgres `tally_pg` on port **5433**)

**Ports:** cored `:8087` · insightd `:8088` · statutoryd `:8091`.

**Backend (`cored`) — the gate matrix:**
```bash
cd backend
go run ./cmd/cored migrate            # apply embedded migrations (idempotent; 11 files)
go build ./... && go vet ./...        # must be clean
go test ./... -count=1                # FULL suite, hits live DB on :5433 (~2-3 min; internal/sync ~130s is the fuzzer)
# boot cored for live e2e (AUTH IS FAIL-CLOSED: dev mode mounts /auth/token):
go build -o /tmp/cored ./cmd/cored && \
  AUTH_DEV_TOKEN_ENABLED=true PORT=8087 APP_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" /tmp/cored serve &
# focused gates for the newest work:
go test ./internal/authz/... ./internal/authn/... ./internal/webauthn/... -v -count=1   # Task D: PDP matrix, JWS, WebAuthn
go test ./internal/server -run 'Auth|Tenant|Token' -v -count=1                          # Task D: middleware contract (401 vs 403, fail-closed)
go test ./internal/sync -run Fuzz -v -count=1        # Task C: 200-seed partition fuzzer (THE gate)
go test ./internal/statutory/... -run Chaos -v       # Task B: no-double-IRN chaos gate
```
(Admin/migrate DSN default: `postgres://postgres:postgres@localhost:5433/tally?sslmode=disable`.)
**Auth note (Task D):** without `AUTH_DEV_TOKEN_ENABLED=true` (or `CORED_ENV=dev`), `cored serve` **refuses to start fail-open** — this is intentional (the in-process `/auth/token` mints validly-signed tokens for any subject, so it is dev-only; production sets `AUTH_JWKS_URL` for the external IdP, which is not yet built offline). Every `/v1/companies/*` call needs `Authorization: Bearer <jwt>`; mint one via `curl -XPOST :8087/auth/token -d '{"subject":"owner-x","amr":["webauthn"]}'`. WebAuthn step-up routes (`e43d383`): `POST /auth/webauthn/register|assert/{begin,finish}` (authed; assert/finish mints an `amr:["webauthn"]` token). Dev login + lockout: `POST /auth/login {subject,password}` — `AUTH_DEV_PASSWORD` (default `dev-password`); 5 wrong → `423`. Bill-inbox demo: boot insightd too (`:8088`) and open the Gateway → "Bill Inbox".

**statutoryd (IRN plane) + the freeze e2e:**
```bash
cd backend && go build -o /tmp/statutoryd ./cmd/statutoryd
# cored must be booted with AUTH_DEV_TOKEN_ENABLED=true (above); mint a service token:
SVC=$(curl -s -XPOST http://localhost:8087/auth/token -d '{"subject":"svc:statutoryd"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
CORED_BASE_URL=http://localhost:8087 CORED_SERVICE_TOKEN=$SVC PORT=8091 /tmp/statutoryd &
python3 backend/scripts/e2e-statutory-freeze.py      # live: 401/403 auth gate -> post sale -> IRN -> freeze -> 409 alter/delete -> recon queue
```

**Frontend:**
```bash
cd frontend
pnpm exec tsc --noEmit                # typecheck
pnpm exec vitest run                  # unit/component tests (110 currently)
pnpm run build                        # tsc + vite build
# live e2e against running cored (:8087) [+ insightd (:8088) for e2e:ocr]:
CORED_BASE_URL=http://localhost:8087 pnpm e2e:edit          # edit/delete loop
CORED_BASE_URL=http://localhost:8087 pnpm e2e:numbering     # auto-numbering
CORED_BASE_URL=http://localhost:8087 INSIGHTD_BASE_URL=http://localhost:8088 pnpm e2e:ocr   # OCR draft -> F9 -> post -> TB
```

**insightd (Python AI plane):**
```bash
cd insightd
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt pytest      # CORE deps only — no anthropic/paddle/presidio needed
python -m pytest                            # ~49 tests; fakes only, no API key
python -m insightd.benchmark.validate       # corpus validator (28 fixtures)
python -m insightd.benchmark.report         # field-level accuracy floor gate
# serve (for the frontend e2e:ocr):
INSIGHTD_PII_BOUNDARY_STRICT=1 python -m uvicorn insightd.api.app:app --port 8088 &
```

**Corpora:** `corpora/migration/*.{xml,expected.json}` (5 four-statement reconciliation datasets) · `corpora/tax/*.json` (shared GST oracle for Go + insightd's cross-oracle test) · `insightd/insightd/benchmark/corpus/*.json` (28 OCR fixtures; the `e2e:ocr` script remaps `intra-state-18-1line-clean.json` onto real cored ledger ids).

**`make verify`** runs the Phase-0 gate set; extend it as planes mature.

---

## 6. Known gaps / tracked follow-ups (the honest edges; none block what's committed)

**Deferred from the shipped Phase-2 work:**
- 🟡 **Tauri/PWA client shells not built.** The offline **sequencer** (cloud side) ships and the **F9 bill-inbox** web UI now exists, but the **Tauri 2 desktop shell + local SQLite read-model/outbox persistence + a TS replay client** that drives `POST /sync/commit` do not — and **cannot be built/verified here (no Rust toolchain).** Same for the **web PWA TanStack shell** (web is plain Vite). Sequencer **HA/leader-election** is also future. Mature packaging on top of a proven sequencer; the desktop shell must obtain + send a Bearer token (reuse `auth.mts`/`/auth/token`).
- 🟡 **F9 OCR prefill is display-only (no per-field edit-through).** The bill-inbox/upload UI now EXISTS (`BillInboxScreen` → list/create draft → load into F9), but editing a prefilled field still does not alter the POSTed body (a `ready` draft is accept-as-is). The "create" affordance pastes a fixture JSON (real image OCR needs PaddleOCR, not wired offline). Per-field edit-through is the remaining polish.
- 🟡 **statutoryd is IRN-only.** No **e-way bill**, no **full GSTR-1/3B/CMP-08 filing with ARN tracking**, no **Postgres-backed StepStore** (the durable runner is in-memory, so workflow state does not survive a `statutoryd` restart), no **real GSP/NIC ProviderAdapter / multi-homing / circuit breakers** (deterministic fake + recorded NIC schema only). `FILED_GSTR1` is a supported freeze `kind` but only `IRN` is wired end-to-end.
- 🟡 **Auth: real IdP + a few prod hardenings deferred.** WebAuthn ceremony + lockout are now WIRED end-to-end (`e43d383`). Remaining: (a) **real Keycloak** — `AUTH_JWKS_URL` is the documented swap but the `JWKSVerifier` (network JWKS fetch) is not built offline (prod boot intentionally errors until it is); (b) **OPA/Rego deferred** — the in-Go PDP is the tested equivalent; (c) the **WebAuthn challenge store is in-memory** (single-instance dev) — a multi-instance deploy needs a shared cache (Redis); (d) `POST /auth/login` is a **dev** credential check (env `AUTH_DEV_PASSWORD`) demonstrating the lockout machinery — production passwords/MFA live in Keycloak. The webauthn pg `CredentialStore` is now exercised by the live ceremony test.

**Pre-existing / smaller:**
- 🟡 **insightd accuracy floors run on deterministic fakes** (measure the mapper + invariants, not real OCR/LLM accuracy). The `--real` calibration mode (PaddleOCR + Anthropic key) is the pre-release model-accuracy gate; it does **not** gate merges. Wire `insightd/tests/test_gst_oracle_reconcile.py` (independent GST signal vs Go golden corpora) into CI. `insightd` `DraftStore` is in-memory (add Postgres-backed). `pii_presidio.py` is constructed but the regex floor (`redaction/recognizers.py`) is authoritative.
- 🟡 **Frontend supplier-state:** `gstVoucher.ts` still sends a hardcoded `"29"`; should send the company's real `state_code` (server default from `company.state_code` is already correct as of `619af10`) and pass `state_code` at company bootstrap. Affects non-Karnataka companies in-browser.
- 🟡 **GSTR-3B** not built (only GSTR-1).
- Minor/untested: DELETE idempotency (re-delete appends another tombstone, returns 200); altering a deleted voucher silently "undeletes"; mock per-class number formats are frontend approximations; the `.NET` signed Windows migration *agent* doesn't exist (the importer logic does).

---

## 7. NEXT TASKS (priority-ordered)

> For each, follow §3: pin contracts in design → parallel-implement on disjoint files → adversarial-review → **you integrate + re-run every gate + a live e2e** before committing. Workflow template in §9.

### ✅ Task D — Auth (DONE, `19d2fa6`) + follow-up (DONE, `e43d383`)
Server-side JWT authn + Tally Disallow/Allow PDP + back-dated cutoff + WebAuthn step-up + account lockout, **fail-closed**; the WebAuthn ceremony + lockout are now WIRED as HTTP routes (`/auth/webauthn/register|assert/{begin,finish}`, dev `/auth/login`). See §4 capabilities 6–7. Remaining auth work is prod-hardening only (§6): real Keycloak `JWKSVerifier`, OPA/Rego, a shared (multi-instance) challenge store.

### ✅ F9 Bill-Inbox UI (DONE, `e43d383`)
The OCR killer-feature loop is now demoable end-to-end in-browser (Gateway → Bill Inbox → list/create draft → F9 prefill → Ctrl+A). See §4 capability 7.

### Next high-value increments
- **Tauri 2 desktop driving `POST /sync/commit`** (DEFERRED — needs a Rust toolchain not present here): a local SQLite read-model + append-only outbox (HLC + node-partitioned UUIDv7 already modeled in `internal/sync/node.go`), replaying through the sequencer; provisional `INV/<fy>/PROV-<node>-<seq>` numbers swapped to the server's final number on commit. Must obtain + send a Bearer token (reuse the `auth.mts` / `/auth/token` pattern).
- **Real Keycloak `JWKSVerifier`** (turns the documented prod-auth swap into a working path) and/or **F9 per-field edit-through** (let edits to a prefilled draft alter the POSTed body). 
- Or proceed to **Phase 3** (§8): NL→typed-DSL conversational query, predictive cash-flow/anomaly, TDS/TCS, payroll, inventory depth.

### Smaller, do-anytime cleanups (good warm-ups / fillers)
- Wire `insightd/tests/test_gst_oracle_reconcile.py` into CI (repo-root checkout). Frontend supplier-state fix (§6). GSTR-3B (mirror GSTR-1, golden-corpus-gated). Decide DELETE idempotency policy + test it. Add a Postgres-backed `DraftStore` (insightd) and `StepStore` (statutoryd).

---

## 8. Roadmap to "done" (after Phase 2)

Per `PLAN.md` Part C — sequence, don't skip:
- **Phase 3 — depth that widens the wedge:** NL→typed-DSL→read-only-replica conversational query (never raw SQL; SQL-injection-adversarial gated); predictive cash-flow forecast + anomaly detection (deterministic XGBoost/statsmodels, backtest-gated — *not* LLM vibes); TDS/TCS in the TaxDeriveEngine; Payroll (Form 24Q/PF/ESI, FVU-validated); inventory depth (batches/expiry, price lists, order/delivery/receipt notes); ClickHouse OLAP + Kafka at the CDC seam; IMS + GSTR-2A/2B recon. Each as a golden-corpus- or backtest-gated workflow.
- **Phase 4 — extensibility & ecosystem moat:** FlowDef declarative DSL (JSON-Schema→versioned IR) for ~80% of customizations + V8 isolate/Wasmtime sandboxes for ~20% (capability-manifest, red-team-gated); Claude-authored extension copilot; hardened `/tally` ENVELOPE+JSONEX ingress so the existing Tally connector ecosystem (Suvit/Webtel/tally-database-loader) works unchanged over authenticated HTTPS (compatibility-differential-gated); first-party webhooks; group-company consolidation.

Critical path: Phase 2 (OCR ✅ + connected compliance ✅ + offline sequencer ✅) is the time-to-market lever; Phases 3–4 are largely parallelizable post-launch.

---

## 9. Dynamic-workflow authoring template (the pattern that works)

```js
export const meta = {
  name: 'phaseN-thing',
  description: '…one line…',
  phases: [{ title: 'Design' }, { title: 'Implement' }, { title: 'Review' }],
}
const REPO = '/Users/yashbhalgat/code/tally-updated'
const BRIEF = `…the integrator's PINNED contracts (exact structs/types/JSON/HTTP shapes),
  pinned VALIDATED FACTS from reading the real code, the DISJOINT file set per workstream,
  and env-realistic verification (fakes/fixtures where no real service/key exists).`

phase('Design')   // 1 agent VALIDATES the pinned brief against real code, returns corrections only
const SPEC = await agent(`Read the exact files + PLAN/RESEARCH. Validate these PINNED contracts;
  report CORRECTIONS only (no redesign): ${BRIEF}`,
  { label: 'design:validate', phase: 'Design', schema: {/* structured corrections + harness notes */} })
const specText = JSON.stringify(SPEC, null, 2)

phase('Implement')   // DISJOINT file sets → safe parallelism, no worktrees
const [a, b] = await parallel([
  () => agent(`Implement WORKSTREAM 1 per the brief VERBATIM + corrections. Touch ONLY <files>.
    Self-verify with REAL commands; report only observed results.\n${BRIEF}\n${specText}`,
    { label: 'impl:WS1', phase: 'Implement', schema: IMPL_SCHEMA }),
  () => agent(`Implement WORKSTREAM 2 … Touch ONLY <files> …`,
    { label: 'impl:WS2', phase: 'Implement', schema: IMPL_SCHEMA }),
])

phase('Review')   // adversarial: be a skeptic, RE-RUN gates, try to refute (and mutation-test if you can)
const [ra, rb] = await parallel([
  () => agent(`Adversarially review WS1: invariants structurally enforced? oracle independent?
    tests non-vacuous (would they fail on a regression)? RE-RUN the gates; report the REAL result.
    Reject on any violation, with concrete mustFix.`,
    { label: 'review:WS1', phase: 'Review', schema: REVIEW_SCHEMA }),
  () => agent(`Adversarially review WS2 …`, { label: 'review:WS2', phase: 'Review', schema: REVIEW_SCHEMA }),
])
return { design: SPEC, implA: a, implB: b, reviewA: ra, reviewB: rb }
```
Then **you** (main loop): read the result, apply any `mustFix`, **re-run the full gate matrix + a live e2e yourself**, write a clear commit stating the gates re-run, update `HANDOFF.md` + memory.

**Guardrails learned this project:**
- Pin contracts in design or parallel agents diverge. Pin them by *reading the real code first* (the integrator's brief), then have Design validate against the code.
- Tell implementers to report only observed results; tell reviewers to re-run gates and try to refute. A coverage/accuracy number is meaningful only if the oracle is independent and the adversarial cases would actually fail a regression — make the reviewer prove that (mutation testing is the gold standard).
- Disjoint files = safe parallelism without worktrees. Two agents in the SAME package is fine if their *files* are disjoint; pin shared types in one agent's file.
- River/Temporal are NOT deps (despite older notes) — durable workflows here use a self-contained in-process StepStore/runner. Check `go.mod` before assuming a dependency.

---

## 10. First moves for the next session

1. Read this file + skim `PLAN.md` Part C + the memory index (`MEMORY.md`).
2. Bring up the stack (§5) and run the full gate matrix once to confirm everything is still green from a clean state: `cd backend && go run ./cmd/cored migrate && go build ./... && go vet ./... && go test ./... -count=1`; `cd frontend && pnpm exec tsc --noEmit && pnpm exec vitest run && pnpm run build`; `cd insightd && python -m pytest`.
3. Tasks A (`d7b970e`), B (`4cc91eb`), C (`6387f8b`), **D (`19d2fa6`)**, the **auth follow-up + F9 bill-inbox (`e43d383`)** are DONE. Next: **real Keycloak `JWKSVerifier`** (prod-auth swap) or **F9 per-field edit-through**, the **Tauri desktop shell** (needs a Rust toolchain — not available here), or proceed to **Phase 3** (§8). All web work must send a Bearer token (`auth.mts`/`/auth/token`).
4. Keep the §3 discipline: orchestrate with workflows, **verify everything yourself**, commit only green, update this file + memory as state changes.
