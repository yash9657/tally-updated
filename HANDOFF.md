# HANDOFF — AI-native, keystroke-compatible Tally competitor

**Purpose of this file:** let a fresh session (clean context) pick up and keep building this project to completion without re-deriving everything. Read it top-to-bottom once, then start at **§7 Next tasks**.

**Last updated:** after `95ce702` — **waves 1, 2, and wave-3 WS5b of `IMPLEMENTATION_PLAN.md` are DONE.** Wave 2 (`d6a3230` WS8b, `37661a5` WS1, `0099946` WS5a) and wave-3 WS5b (`95ce702`) landed this session, each adversarially-corrected, gated, and integrator-verified (full backend suite, frontend suite, insightd suite, corpora, and live e2e all re-run green). Branch: `master`, tree clean. **The ONLY remaining plan item is wave-3 WS10 (PaddleOCR spike), DEFERRED** (see §6) — everything else in `IMPLEMENTATION_PLAN.md` is shipped.

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
- **`docs/TALLY-FOR-FOUNDERS.md`** — learn Tally itself from this product's own demo books (groups/ledgers, the keyboard, company setup, opening balances + bill-wise breakup, every voucher type, the reports, and what is deliberately not built). Every figure is gated by `pnpm verify:demo`.
- **`docs/TENANCY-AND-ACCESS.md`** — the SaaS layer: the three layers a request passes (authn → membership → PDP), the 401-vs-403 contract, the PDP precedence table, and EVERY allow/disallow path with its observed status code. Gated by `pnpm verify:tenancy`.
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
- **DEMO REMEDIATION + TENANCY SESSION (2026-07-26)** — a founder-facing audit of the demo company turned up **three real product bugs**, all now fixed with failing-then-passing regression tests. Migrations **0015–0019**. Two new gated verifiers.
  - **THREE PRODUCT BUGS (not demo-data problems):**
    1. `ALTER` dropped `voucher_type_id` (`internal/ledger/ledger.go`). `gstr1.go`/`gstr3b.go` reach the class through an INNER JOIN, so **the current version of any altered invoice silently vanished from GSTR-1/GSTR-3B** while staying in the Trial Balance and P&L. The code comment claimed carry-forward that did not exist (DELETE genuinely did carry it; ALTER did not). Fixed inside `post()` so every alter path benefits, with a fallback that REPAIRS pre-existing NULL rows.
    2. `Outstandings` summed `bill_allocation` across **every voucher version** with no `voucher_head` join — an altered bill aged at (old + new), and a DELETED voucher aged forever. Made Outstandings disagree with the Balance Sheet's Debtors/Creditors while each looked internally consistent.
    3. GSTR-1 classified credit/debit notes by **voucher class** instead of **party side**, so a supplier-side (purchase-return) note was reported as outward supply. The discriminator must be the party's group nature (ASSET => outward, LIABILITY => input-credit).
  - **NEW SCHEMA:** `0015` company profile (mailing block, country, **`books_beginning_from`** distinct from `fy_start_date`, currency symbol/decimals; a BEFORE-INSERT trigger defaults books→fy so raw inserts still work) · `0016` **`voucher_gst_line`** (per-line HSN/rate/tax; HSN was previously discarded, so every `hsn_sc` was `""`) · `0017` **`ledger_opening_bill`** (bill-wise OPENING breakup; Outstandings UNIONs it) · `0018` **Credit/Debit Note voucher types** (own numbering series, GSTR-1 `cdnr`) · `0019` Data Entry ACL rules for the note classes.
  - **NEW ENDPOINTS:** `GET .../ledgers` (chart of accounts — previously you could create ledgers but never enumerate them, which is why the old seed minted a duplicate `Cash A/c`), `PUT .../ledgers/{id}/opening` (the only way to give the SEEDED `Cash` ledger an opening balance), `POST/PUT .../vouchers/credit-note|debit-note`. GSTR-1's filer `gstin` now falls back to `company.gstin` instead of only a query param.
  - **UI:** `Alt+F5`/`Alt+F6` open real Debit/Credit Note screens (were bound to "not built"). Voucher titles now come from `VOUCHER_CONFIGS` (a duplicate title map rendered the raw slug "credit-note Voucher").
  - **TWO GATED VERIFIERS (run these; they are the proof):**
    - `cd frontend && pnpm verify:demo <companyID>` — **18** accounting checks incl. BS-vs-Outstandings on both sides, GSTR-1 `gstin`/`b2b`/`cdnr`/`hsn`, GSTR-3B ITC == Input-ledger balances. NON-VACUOUS: fails 12/16 against a pre-remediation company.
    - `cd frontend && pnpm verify:tenancy <A> <B>` — **27** access-control checks: 401-vs-403 contract, Owner step-up, Data-Entry default-deny, cross-tenant refusal, back-dated cutoff, service principal, unmapped-route deny. **Self-cleaning** (tombstones its own probe vouchers; TB returns to the seed's exact `774970.0000`).
  - **DOCS:** `docs/TALLY-FOR-FOUNDERS.md` (learn Tally from these books) · `docs/TENANCY-AND-ACCESS.md` (every allow/disallow path with observed status codes).
  - **SEEDS:** `pnpm seed:demo -- --insightd` (company A) then `pnpm seed:tenancy <A>` (company B + a Data-Entry user on A). `make run-cored` / `make run-insightd` / `make seed-demo` added — `run-insightd` FAILS LOUDLY without psycopg instead of silently dropping to the in-memory draft store.
- **Wave 2 + wave-3 WS5b of `IMPLEMENTATION_PLAN.md` (2026-07-24), one commit per workstream:**
  - `95ce702` — **WS5b (wave 3): statsmodels Holt-Winters cash-flow forecast** in insightd, backtest-gated (MAPE beats the naive baseline by 82–96%, gate requires ≥20%), determinism-pinned, `POST /companies/{id}/forecast/cashflow`. statsmodels is OPTIONAL (`requirements-forecast.txt`; route mounts only when installed — lazy-import proven by shadowing it: core suite 71 pass / 2 skip; with it, 79 pass).
  - `0099946` — **WS5a: deterministic cash-flow projection + rule-based anomaly report** (`internal/forecast`, pure `Project`/`DetectAnomalies` + thin loaders; corpus-gated with an independent Python re-derivation in `validate.py`; invariance vs `reporting.Outstandings`; `GET .../cash-flow` + `.../anomalies`).
  - `37661a5` — **WS1: real-Claude NL→DSL translator** (`internal/nlq`, stdlib-only `net/http`, forced tool-use; ANY non-200 upstream → 503, 422 reserved for a 200 that isn't a valid Query; `Catalog()` + a network-free archtest on `internal/query`; QueryScreen 503 retry hint).
  - `d6a3230` — **WS8b: F9 per-field edit-through** (a dirty accept rebuilds a balanced, orientation-preserving purchase body via `draftAccept.applyDraftEdits` — never the sales-shaped `buildGstVoucherRequest`; pristine accept stays byte-identical).
- **Wave 1 of `IMPLEMENTATION_PLAN.md` (2026-07-24), one commit per workstream:**
  - `d5d4e8b` — **WS8a: company `state_code` end-to-end** (the `"29"` bootstrap constant is gone; `GET /v1/companies/{id}`).
  - `f44f70c` — **WS9: voucher delete/alter policy + `make verify` completeness** (idempotent re-DELETE, 404 on alter-after-delete).
  - `c4c2c5d` — **WS3: TDS/TCS derivation** (pure engine, 18 cited fixtures, derive-and-verify the posted leg).
  - `83b4503` — **WS6: auth prod-hardening** (real `JWKSVerifier`; Postgres-backed WebAuthn challenge store, migration 0012).
  - `ba7ecd9` — **WS4: GSTR-3B** (books-derived 3.1/3.2/table-4 beside GSTR-1; ties to GSTR-1 as an independent oracle).
  - `9bf93d2` — **WS7b: insightd PG DraftStore** (migration 0014, RLS per company).
  - `adc0f1b` — **WS7a: statutoryd PG StepStore** (migration 0013; IRN workflow survives a restart).
  - `842b82b` — **WS2: widened query DSL** (min/max/avg, quarter/year, opening-balance metrics; TB divergence closed).
- `7dcc680` — **Phase 3 flagship: NL → typed-DSL → safe read-only conversational query** ("ask your books"; injection/cross-tenant/write adversarially gated).
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
8. **Conversational query — NL → typed-DSL → safe read-only SQL (Phase 3 flagship, `7dcc680`):** ask the books in English (or POST a typed DSL) and get a typed, read-only answer. **User input never becomes SQL text:** NL → a closed typed `query.Query` DSL validated against a per-entity whitelist → a deterministic compiler emitting a fixed schema-driven FROM/JOIN skeleton with only whitelisted identifiers and **all values as `$N` params** → execution in a new **read-only, RLS-scoped** tx (`db.RunInReadOnlyTx`). Two independent guarantees: RLS tenant isolation + read-only write-impossibility. Entities: `ledger_balances`, `vouchers`, `postings`, `outstandings` (each bakes the current-version/not-deleted guard). Translator: deterministic fake (canonical questions) + a documented real-Claude swap (LLM emits only the DSL, never SQL — insightd pattern). Endpoint `POST /v1/companies/{id}/query` (authed; PDP `Display/query`); transparency panel in the frontend `QueryScreen` shows the DSL + SQL preview. **Adversarially gated** (mutation-tested compiler; injection into every DSL field, cross-tenant, write-attempt, limit-cap all proven safe with the DB byte-for-byte intact). Live-verified (`/query` DSL+NL, gibberish→422, injection→422 + DB intact, no-token→401).

9. **Wave-1 gap closure (2026-07-24, `842b82b`…`d5d4e8b`):** eight independently-gated increments — a **widened query DSL** (min/max/avg, quarter/year buckets, `opening`/`closing_net` gated against the independent Trial Balance oracle), a **TDS/TCS engine** that derives-and-verifies the posted leg (18 cited fixtures, re-derived independently by `corpora/validate.py`), **GSTR-3B** (asserted equal to GSTR-1's tax totals for the same period), **production auth** (real `JWKSVerifier` + PG-backed WebAuthn challenges, both mutation-tested), **durable state for both side planes** (statutoryd `statutory_step`, insightd `insightd_draft`), **real company `state_code`** end-to-end, and a **pinned delete/alter policy**. Every workstream was adversarially reviewed with at least one mutation test, and every gate below was re-run by the integrator before commit.

**DB migrations:** `backend/migrations/0001…0014` (embedded, applied lexically by `cored migrate`). 0009 = freeze (external-reference + reconciliation_queue); 0010 = offline sync (`sync_applied` + `reconciliation_queue.payload`); 0011 = auth catalog (app_user, security_level, acl_rule, company_membership, webauthn_credential, auth_login_attempt; user/subject GUC resolvers; FORCE RLS); **0012 = `webauthn_challenge`** (FORCE RLS, user-scoped, single-use); **0013 = `statutory_step`** (statutoryd durable runner; deliberately no RLS — see §6); **0014 = `insightd_draft`** (FORCE RLS on `company_id`).

**Phase status:** Phase 0 ✅ · Phase 1 ✅ (core + migration thesis) · Phase 2 ✅ on the time-to-market targets (OCR loop, F9 prefill + **bill-inbox**, statutoryd IRN+freeze, offline sequencer, **auth + WebAuthn step-up/lockout routes**); **deferred within Phase 2:** the **Tauri 2 desktop shell** (no Rust toolchain here) + web PWA shell, e-way bill + full GSTR filing, real Keycloak JWKS + OPA, and a shared (multi-instance) WebAuthn challenge store (see §6). **Phase 3 STARTED** — the flagship conversational query (`7dcc680`) ships; remaining Phase-3 depth (forecast/anomaly, TDS/TCS, payroll, inventory depth, GSTR-2A/2B/3B, more query entities + real-Claude translator) ⬜. Phase 4 ⬜.

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
go test ./internal/query/... -run Security -v -count=1                                   # Phase 3: NL-query injection/cross-tenant/write adversarial gate (THE gate)
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

**New since wave 1 (2026-07-24) — env vars and gates you need to know:**
```bash
# cored, PRODUCTION auth (WS6): AUTH_ISSUER is now REQUIRED with AUTH_JWKS_URL, and the
# JWKS is fetched ONCE at boot (5s timeout) — an unreachable IdP is a HARD boot failure.
AUTH_JWKS_URL=https://idp/realms/x/protocol/openid-connect/certs AUTH_ISSUER=https://idp/realms/x /tmp/cored serve
# statutoryd durable runner (WS7a). Unset -> in-memory + a loud warning; set but table missing -> "run cored migrate".
STATUTORYD_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" /tmp/statutoryd
# insightd durable drafts (WS7b). Needs `pip install -r requirements-db.txt` (psycopg is lazy-imported).
INSIGHTD_DATABASE_URL="postgres://tally_app:tally_app@localhost:5433/tally" python -m uvicorn insightd.api.app:app --port 8088
# new live e2e (WS8a): a state_code "27" company must derive CGST+SGST intra-27 from the REAL server.
CORED_BASE_URL=http://localhost:8087 pnpm e2e:state-code
# the statutory e2e now has a durable-restart phase (opt-in, with a no-DSN control):
CORED_BASE_URL=http://localhost:8087 STATUTORYD_BASE_URL=http://localhost:8091 \
  STATUTORYD_BIN=/tmp/statutoryd STATUTORYD_DSN="postgres://...:5433/tally?sslmode=disable" \
  python3 backend/scripts/e2e-statutory-freeze.py
# new focused gates:
go test ./internal/authn/... -run JWKS -v -count=1        # hermetic JWKS accept/reject matrix
go test ./internal/server -run 'Challenge|GSTR3B|GetCompany|Delete' -v -count=1
go test ./internal/compliance/... -count=1                # TDS/TCS golden corpora
INSIGHTD_DATABASE_URL=... python -m pytest tests/test_pg_store.py
```

**Corpora:** `corpora/migration/*.{xml,expected.json}` (5 four-statement reconciliation datasets) · `corpora/tax/*.json` (shared GST oracle for Go + insightd's cross-oracle test) · `insightd/insightd/benchmark/corpus/*.json` (28 OCR fixtures; the `e2e:ocr` script remaps `intra-state-18-1line-clean.json` onto real cored ledger ids).

**`make verify`** runs the Phase-0 gate set; extend it as planes mature.

---

## 6. Known gaps / tracked follow-ups (the honest edges; none block what's committed)

**SaaS / tenancy gaps (2026-07-26) — this is the top of the backlog now:**
- ❌ **No admin API for users, roles or memberships.** `POST /v1/companies` makes its creator an Owner and that is the ONLY membership any HTTP route can create. Granting a second user a level is SQL-only — which is why `frontend/scripts/seed-tenancy.mts` shells out to `psql`. **This blocks everything else in the SaaS story**; build it first.
- ❌ **No custom security levels.** The engine fully supports them (`security_level` + `acl_rule` are per-company rows, and the PDP's specificity/precedence is tested) but only the two seeded levels (Owner, Data Entry) exist and nothing can create a third.
- ❌ **No login.** `POST /auth/token` mints a token for ANY subject and only exists when a dev signer is configured. No password, no SSO, no IdP.
- ❌ **No company switching.** `F3` is bound and answers "one company per session". A user with two memberships cannot reach the second in the UI.
- ❌ **The web client is single-identity.** It authenticates as one hardcoded dev subject, so the whole allow/disallow story in `docs/TENANCY-AND-ACCESS.md` is reachable by API but INVISIBLE in the browser.
- ⚠️ **Data Entry cannot read the company master.** `GET /v1/companies/{id}` maps to `display`/`company` and the seeded Data Entry level has no rule for it, so it 403s. The web client hydrates the home `state_code` from exactly that endpoint to compute the in-browser GST split — so **the UI is not usable end-to-end as a Data-Entry operator**. The server is correct (nobody granted the right); the seeded rule set is incomplete for the client that exists. One ACL row fixes it, deliberately NOT added because it is a product decision about what that role should see.
- ⚠️ **The voucher screen's party picker is a HARDCODED list** (`PARTY_LEDGERS` in `VoucherScreen.tsx`: "Acme Traders", "Bharat Stores", "Chandra & Co"…), not the company's chart of accounts — "Bharat Stores" exists in no seeded company. `GET .../ledgers` now exists so the fix is unblocked; it was left alone because switching it changes `VoucherScreen.gst.test.tsx` / `.stateCode.test.tsx`.
- ⚠️ **Single-entry mode is DECLARED BUT INERT.** `voucherConfig.ts` types `entry: "single" | "double"` and marks Payment/Receipt/Contra `"single"`, but **nothing in `src/` reads `cfg.entry`** — every voucher renders the same explicit Dr/Cr form. Tally defaults those three to single-entry (pick one account, the other side is inferred). Deferred deliberately: it is a keystroke-flow change to the most fidelity-sensitive screen and needs its own keystroke-corpus cases.
- ⚠️ **An unmatched OCR supplier silently changes the tax split.** `insightd/adapters/mapper.py:111` falls back to using the PLACE OF SUPPLY as the supplier state when the classifier resolves no party ledger, which makes every such bill look intra-state. The draft is correctly flagged `unresolved: ["party","supplier"]` and the human-in-the-loop Alt+C flow is the designed mitigation, but the fallback itself is a silent-wrong-answer path.
- 📌 **Adding a voucher class is a TWO-file change:** the class, AND the security levels permitted to use it. Migration 0018 added credit/debit notes; 0019 had to follow because Data Entry (`default_access: none`) denied them. Default-deny made the omission a harmless 403 rather than an accidental grant — the right failure mode, but still a failure.


**Keyboard/UI session (2026-07-25, `40b7c3f` + `59b23a7`) — read this before touching the shell:**
- ✅ **Key delivery is now on the DOCUMENT, not the root div.** Two real-user bugs came from the same root cause: after any screen change the previously focused control unmounts and the browser puts focus on `<body>`, which is outside the app root — so a React `onKeyDown` there received NOTHING (Esc, F-keys, Alt+G, Ctrl+A all dead). **Never move key handling back onto a React element.**
- ✅ **TESTING RULE, non-negotiable:** in `scripts/e2e-ui-smoke.mjs` send keys with `page.keyboard`, **never** `locator.press()` on the app element — `locator.press` focuses first and manufactures the condition the app must not depend on. Both of the above bugs passed a suite that used `locator.press`. Every key flow in that file now uses `page.keyboard`.
- ✅ **Every documented TallyPrime combo is bound** (`keystroke/keymaps/tallyprime.ts`), with per-screen overrides resolved by `resolveAction(keymap, screen, combo)` — the single place the machine, the shell and F1 Help agree on. Combos whose feature is not built map to `{kind:"unavailable", feature, note}` and print that in the status line; a key that does not apply to the active screen says so too. `keystroke/buttons.test.ts` (68 cases) fails the build if a bar button is decorative or a documented combo is unbound.
- ✅ **Report drill is real data now.** The old generic report / report-detail screens rendered placeholder rows ("Row 1/2/3", "Voucher 1/2") — fabricated numbers in an accounting screen. Replaced by group > sub-group > ledger > voucher over the live statement tree + Day Book; a report with no data plane (Stock Summary) says so instead.
- ✅ **Alteration hydration fixed:** the seed guard was stamped BEFORE the fetch, so under StrictMode a drilled voucher loaded EMPTY (Ctrl+A would have blanked a real voucher). Stamp such guards only after the response is applied.
- 🟡 **The Edit Log has no viewer.** Versions/tombstones are recorded (verified live: create→v1, alter→v2 same number, delete→v3 op DELETE) but `GET /vouchers/{id}` returns only the latest — no API or screen shows the history. **Top functional gap; likely next task.**
- 🟡 **BS / P&L / Trial Balance are as-of-latest**: the F2 period picker applies server-side only to the Day Book (`from`/`to`) and Outstandings (`as_on`); the panel states this rather than filtering silently. Adding period support to those three is a backend change (`reporting.TrialBalance/ProfitAndLoss/BalanceSheet` take no dates).
- 🟡 **macOS caveat:** some `Ctrl+F<n>` chords are swallowed by the OS before the browser sees them (Tally is Windows-first). Bindings are correct; delivery is environmental.
- 📄 **`WALKTHROUGH.md`** (repo root) is the user-facing tour: every feature, the command to re-run it, and the live output observed on 2026-07-25.

**Deferred from the shipped Phase-2 work:**
- 🟡 **Tauri/PWA client shells not built.** The offline **sequencer** (cloud side) ships and the **F9 bill-inbox** web UI now exists, but the **Tauri 2 desktop shell + local SQLite read-model/outbox persistence + a TS replay client** that drives `POST /sync/commit` do not — and **cannot be built/verified here (no Rust toolchain).** Same for the **web PWA TanStack shell** (web is plain Vite). Sequencer **HA/leader-election** is also future. Mature packaging on top of a proven sequencer; the desktop shell must obtain + send a Bearer token (reuse `auth.mts`/`/auth/token`).
- ✅ **F9 per-field edit-through: RESOLVED (WS8b, `d6a3230`).** Editing a prefilled field now changes the POSTed body: a pristine accept still POSTs the draft byte-identical, a dirty accept rebuilds a balanced, **orientation-preserving** purchase body (`draftAccept.applyDraftEdits` — expense Dr + Input-tax Dr + party Cr, using the draft's OWN Input-tax ledger ids), deliberately NOT via the sales-shaped `buildGstVoucherRequest` (which would post a REVERSED purchase that still passes the server's dr_cr-blind GST verify — the trap the reviewer flagged). **Disclosed v1 limits:** only single-line, non-reverse-charge, non-exempt drafts are editable (multi-line/RCM/exempt keep the pristine body, surfaced in-UI); the editable set is date/narration/taxable/rate/place-of-supply (party editing deferred — the sample `PARTY_LEDGERS`/`SelectionList` cannot represent an arbitrary supplier). PRE-EXISTING (outside WS8b's scope, tracked): the NORMAL (non-draft) F9 purchase create path is itself orientation-inverted (`buildGstVoucherRequest` posts party Dr for a purchase) and no test pins it. The "create" affordance still pastes fixture JSON (real image OCR is WS10, deferred below).
- 🟡 **Predictive cash-flow + anomalies (WS5a `0099946`) inherit the bare-report over-count.** `internal/forecast` reuses `reporting.Outstandings` for the bill AMOUNTS (its netting is not re-implemented), so — like the bare Outstandings report — projected inflows **over-count bills on vouchers that were later ALTERed** (the query-DSL `outstandings` entity is the corrected version). v1 matches the report deliberately (the invariance gate is a conservation check against it); correcting the forecast to the current-version semantics is a tracked follow-up. Also disclosed: TDS/TCS-style **anomaly thresholds are constants** (not config v1); AFTER_HOURS uses a **fixed IST** company timezone; the opening-cash query **excludes 'Bank OD A/c'** (a liability Tally would net); the report reads a two-transaction snapshot (`Outstandings` + a supplementary read-only tx) so a concurrent post can skew opening-cash vs the bill set (negligible for a projection).
- 🟡 **Statistical forecast (WS5b `95ce702`) input is an INJECTED frame, and the model is a statistical ESTIMATE.** `POST /companies/{id}/forecast/cashflow` takes the history frame in its body (a weekly series or day-book rows) rather than fetching cored's day-book itself — so insightd holds NO cored client on this path (the read-only boundary is trivially strong). **Wiring a live service-token cored day-book GET is the follow-up.** The forecast is a rounded-float statistical estimate (`estimate: true`), never a paisa-exact ledger value — complementary to WS5a's deterministic open-bill projection. statsmodels is optional (`requirements-forecast.txt`); with it absent the route is unmounted and the forecast tests skip.
- 🟡 **WS10 (real PaddleOCR + Bill-Inbox file upload) is DEFERRED (wave-3 stretch, not started).** Feasibility probed this session: `paddlepaddle 3.3.1` + `paddleocr 3.7.0` wheels RESOLVE on the package index (install is feasible; arm64 wheels confirmed 2026-07-23), but the plan's timeboxed inference spike (run PaddleOCR on 2 fixture images, confirm offline-after-first-download) was **not undertaken** — model download + offline inference remain UNVERIFIED, and wiring `adapters/ocr_paddle.py` behind `INSIGHTD_USE_REAL_OCR=1` + a `BillInboxScreen` file input is a genuine half-day. This is the ONLY unshipped `IMPLEMENTATION_PLAN.md` item. Resume by running the spike first (§5 of the plan); if inference works offline, wire it behind the flag, else record the negative finding here and stop.
- 🟡 **statutoryd is IRN-only.** No **e-way bill**, no **full GSTR-1/3B/CMP-08 filing with ARN tracking**, no **real GSP/NIC ProviderAdapter / multi-homing / circuit breakers** (deterministic fake + recorded NIC schema only). `FILED_GSTR1` is a supported freeze `kind` but only `IRN` is wired end-to-end. ✅ The **Postgres-backed StepStore is DONE** (WS7a, migration 0013, `STATUTORYD_DSN`) — workflow state now survives a restart, proven live (IRP called exactly once across a process kill). Disclosed: `statutory_step` has **no RLS** (it is not user-facing and has no company_id to scope by; least privilege is REVOKE-from-PUBLIC + GRANT to `tally_app`), and statutoryd now holds a `tally_app` DSN, so a compromised statutoryd could read tenant tables it previously could not reach — a dedicated, narrower DB role is the follow-up. Multi-instance statutoryd HA / leader election is still future.
- 🟡 **Auth: real IdP integration test + OPA deferred.** WebAuthn ceremony + lockout are WIRED end-to-end (`e43d383`); ✅ the **`JWKSVerifier` is now BUILT** (WS6) — `AUTH_JWKS_URL` + `AUTH_ISSUER` boots against a real IdP (ES256/EdDSA only, symmetric keys never admitted), hermetically gated against an `httptest` JWKS server; ✅ the **WebAuthn challenge store is now Postgres-backed** (migration 0012, FORCE RLS), so a multi-instance deploy works. Remaining: (a) **a live Keycloak smoke test** (docker-compose) — the verifier is proven only hermetically; (b) **`aud` is NOT validated anywhere** — pinning `iss` scopes acceptance to one realm, but every *client* in that realm mints tokens with the same `iss`, so any of them is accepted; (c) **OPA/Rego deferred** — the in-Go PDP is the tested equivalent; (d) `POST /auth/login` is a **dev** credential check (env `AUTH_DEV_PASSWORD`) demonstrating the lockout machinery — production passwords/MFA live in Keycloak. **Operational change (WS6):** with `AUTH_JWKS_URL` set, `AUTH_ISSUER` is REQUIRED and the JWKS is fetched ONCE at boot with a 5s timeout — an unreachable IdP is a hard boot failure, deliberately, rather than a process that boots and 401s every request.

**Pre-existing / smaller:**
- 🟡 **insightd accuracy floors run on deterministic fakes** (measure the mapper + invariants, not real OCR/LLM accuracy). The `--real` calibration mode (PaddleOCR + Anthropic key) is the pre-release model-accuracy gate; it does **not** gate merges. Wire `insightd/tests/test_gst_oracle_reconcile.py` (independent GST signal vs Go golden corpora) into CI. `pii_presidio.py` is constructed but the regex floor (`redaction/recognizers.py`) is authoritative. ✅ The **`DraftStore` is now Postgres-backed** (WS7b, migration 0014, `INSIGHTD_DATABASE_URL`, FORCE RLS on `company_id`); psycopg is lazy-imported via `requirements-db.txt`, so the core suite still passes with no DB deps installed (verified by physically removing psycopg: 57 passed, 13 skipped).
- ✅ **Frontend supplier-state: RESOLVED (WS8a).** (The old entry mis-attributed the hardcode to `gstVoucher.ts`, which in fact parameterized correctly; the constant lived in `CompanyContext.tsx`.) `BOOTSTRAP_HOME_STATE_CODE = "29"` is deleted: `GET /v1/companies/{companyID}` now exists (PDP-mapped `Display`/`company`) and the shell hydrates `CompanyInfo.homeStateCode` from `company.state_code` on load, while the create-time default is config only (`VITE_HOME_STATE_CODE`). `buildGstVoucherRequest` OMITS `supplier_state_code` when the home state is unknown so the server default fires. Hydration fails closed. Live-gated by `pnpm e2e:state-code`.
- 🟡 **GSTR-3B ships as a books-derived SUMMARY, not a filing-ready return (WS4).** `GET /v1/companies/{id}/gstr3b` generates tables 3.1(a)–(e), 3.2 and 4 from the posted voucher log. Disclosed v1 limits (all documented at the top of `backend/internal/compliance/gstr/gstr3b.go`): **ITC is reported ALL-ELIGIBLE** (no eligibility test, no rule 42/43 apportionment, no reversals, no s.17(5) ineligible block, no GSTR-2A/2B matching); the **JSON field names are NOT verified against the GSTN 3B offline-utility schema**, so **portal-upload compatibility is NOT claimed**; no cess anywhere in the books; 3.1(b)/(c)/(e) are always zero (no export/SEZ/nil-rated marker exists on a voucher); inter-state is derived as `igst > 0`, so a 0%-rated inter-state supply classifies as intra. No migration corpus can exercise the ITC/RCM half (the Tally importer stamps GST on Sales only) — that coverage lives in `gstr3b_integration_test.go`.
- 🟡 **TDS/TCS thresholds are PER-TRANSACTION in v1 (WS3).** The engine, corpus (13 TDS + 5 TCS fixtures with statutory citations) and post-path verification ship, but 194H/194J thresholds are statutorily **annual** and 194I's is **monthly**, while v1 tests every threshold per transaction — the error direction is **under-deduction**. Each rule records its true statutory `Basis` so the approximation is machine-visible and corpus-pinned. Also deferred: annual-cumulative tracking (194C ₹1L/yr, 194Q ₹50L/yr), TDS returns (26Q/challans), payroll (24Q/PF/ESI), and any frontend UI (the `tds` block is API-level only). Rates were curated offline and spot-checked against independent live sources (194H, 194I, 194J, and the 206C(1H) omission) — **re-verify against current law before any release.**
- 🟡 **Conversational query: the DSL is widened but the translator is still fake.** `min`/`max`/`avg`, `quarter`/`year` buckets and opening-balance metrics (`opening`, `closing_net`) now compile (WS2), and `closing_net` is gated against the independent `reporting` Trial Balance, so **the documented TB divergence is closed when the caller selects `closing_net`** (movement-only `net` remains). ✅ **The real-Claude adapter is now BUILT (WS1, `37661a5`):** `internal/nlq.ClaudeTranslator` (stdlib `net/http` only, forced tool-use) is selected when `ANTHROPIC_API_KEY` is set, else the `FakeTranslator` ships as before; the LLM emits ONLY the typed DSL, re-validated by the same compiler. Disclosed: **no live real-key smoke test was run** (none available in this env — all gates are hermetic via an `httptest` fake); `aud`-style prompt-injection is handled by the compiler being the sole authority (proven by spraying `injectionPayloads` through the fake LLM with the DB byte-for-byte intact). Entities remain the starter four (`ledger_balances`, `vouchers`, `postings`, `outstandings`).
- ✅ **DELETE/ALTER policy: RESOLVED (WS9).** Re-deleting a deleted voucher is a 200 idempotent no-op echoing the existing tombstone (no second tombstone); altering a deleted voucher is a 404 (no more silent resurrection). Disclosed: the head probe and tombstone write are two transactions, so a hard race can still stack one extra tombstone — the guarantee is retry-safety, not serialisable exactly-once. `make verify` now also runs `corpora-verify` + `insightd-test` and, having had its vitest-watch hang fixed, completes end-to-end for the first time.
- Minor/untested: mock per-class number formats are frontend approximations; the `.NET` signed Windows migration *agent* doesn't exist (the importer logic does).

---

## 7. NEXT TASKS (priority-ordered)

> **⚠️ SUPERSEDED BY `IMPLEMENTATION_PLAN.md` (2026-07-23):** a principal-engineer plan now covers ALL §6 gaps (except the Tauri desktop shell, deferred) plus the Phase-3 depth increments, with pinned per-workstream contracts, pre-assigned migration numbers (0012–0014), wave-based dynamic workflows, and a Definition of Done for the review session. **Execute from that file.** The list below remains as background context only.
>
> **Progress against that plan (2026-07-24): WAVES 1 + 2 + wave-3 WS5b ARE DONE.** Wave 1 (WS2/3/4/6/7a/7b/8a/9), wave 2 (WS1/WS5a/WS8b), and WS5b all shipped, each adversarially-corrected and integrator-verified. §8.1 matrix re-run green at `95ce702`: backend **18 test packages** (now incl. `internal/forecast` + `internal/nlq`; sync fuzzer ~112s), frontend **134 vitest** + `tsc` + build, insightd **79 tests** (71 pass / 2 skip with statsmodels absent — lazy-import proven), `corpora/validate.py` tax=14/tds=13/tcs=5/keystroke=15/isolation=11/migration=6/**forecast=4** (with an independent Python re-derivation), plus live `e2e:edit` / `e2e:numbering` / `e2e:state-code` / `e2e:ocr` (extended with the WS8b edited-accept case) and live curl of `/cash-flow`, `/anomalies`, and the WS1 `/query` 503-vs-DSL-200 path. **The ONLY remaining plan item is wave-3 WS10 (PaddleOCR spike), DEFERRED — see §6.**

> For each, follow §3: pin contracts in design → parallel-implement on disjoint files → adversarial-review → **you integrate + re-run every gate + a live e2e** before committing. Workflow template in §9.

### ✅ Task D — Auth (DONE, `19d2fa6`) + follow-up (DONE, `e43d383`)
Server-side JWT authn + Tally Disallow/Allow PDP + back-dated cutoff + WebAuthn step-up + account lockout, **fail-closed**; the WebAuthn ceremony + lockout are now WIRED as HTTP routes (`/auth/webauthn/register|assert/{begin,finish}`, dev `/auth/login`). See §4 capabilities 6–7. Remaining auth work is prod-hardening only (§6): real Keycloak `JWKSVerifier`, OPA/Rego, a shared (multi-instance) challenge store.

### ✅ F9 Bill-Inbox UI (DONE, `e43d383`)
The OCR killer-feature loop is now demoable end-to-end in-browser (Gateway → Bill Inbox → list/create draft → F9 prefill → Ctrl+A). See §4 capability 7.

### ✅ Phase 3 flagship — Conversational query (DONE, `7dcc680`)
NL → typed-DSL → safe read-only SQL ("ask your books"), adversarially gated. See §4 capability 8. The natural follow-ups: **wire the real-Claude translator** (LLM→DSL, insightd adapter pattern) + **widen the query DSL** (more entities/metrics: min/max/avg, time-bucketing, top-N, joins to GST) — both build directly on the safe compiler.

### Next high-value increments (pick by priority)
- **Deepen the query** (real-Claude translator + more entities/metrics) — extends `7dcc680`; the safe core is done so this is mostly translator + whitelist work.
- **Predictive cash-flow forecast + anomaly detection** (Phase 3) — DETERMINISTIC (statsmodels/XGBoost), **backtest-gated, not LLM vibes**. NOTE: check whether the ML deps are installable offline before committing to it; if not, a simpler deterministic baseline (moving-average / due-date projection from `bill_allocation`) is a safe, testable first cut.
- **TDS/TCS in the TaxDeriveEngine** (Phase 3) — deterministic, golden-corpus-gated, extends the existing Go tax engine; no new deps. **GSTR-3B** (mirror GSTR-1, golden-corpus-gated). Both low-risk, high CA-value.
- **Tauri 2 desktop driving `POST /sync/commit`** (DEFERRED — needs a Rust toolchain not present here). **Real Keycloak `JWKSVerifier`** (prod-auth swap). **F9 per-field edit-through.**

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
3. **START HERE (2026-07-26).** The demo-remediation + tenancy session is committed and green (§4). The highest-value next increment is the **SaaS admin surface**, in this order — it is what unblocks everything else:
   1. **Admin API for membership + roles** — invite a user to a company, assign/change a security level, list members. Today this is SQL-only (§6), which is why `seed-tenancy.mts` shells out to `psql`. With it, `docs/TENANCY-AND-ACCESS.md` §4 becomes clickable instead of curl-only.
   2. **Custom security levels** (create/edit a level + its ACL rules). The PDP already supports them; only creation is missing.
   3. **A real login** replacing the dev `/auth/token`, then **company switching** (`F3`), then a **multi-identity web client** so the allow/disallow paths are visible in the browser.
   Before starting, run BOTH verifiers to confirm a green baseline: `pnpm seed:demo -- --insightd` → `pnpm seed:tenancy <A>` → `pnpm verify:demo <A>` (18/18) and `pnpm verify:tenancy <A> <B>` (27/27). Read `docs/TENANCY-AND-ACCESS.md` §7 for the full missing-pieces list.
   Prior tasks A (`d7b970e`), B (`4cc91eb`), C (`6387f8b`), **D (`19d2fa6`)**, the **auth follow-up + F9 bill-inbox (`e43d383`)**, and the **Phase-3 conversational-query flagship (`7dcc680`)** are DONE. Next (§7): **deepen the query** (real-Claude translator + more entities — builds on the safe compiler), **TDS/TCS + GSTR-3B** (deterministic, golden-corpus-gated, no new deps), or **forecast/anomaly** (deterministic + backtest-gated — check ML deps install offline first). Deferred: Tauri desktop (needs Rust), Keycloak JWKS, F9 edit-through. All web work must send a Bearer token (`auth.mts`/`/auth/token`); boot cored with `AUTH_DEV_TOKEN_ENABLED=true`.
4. Keep the §3 discipline: orchestrate with workflows, **verify everything yourself**, commit only green, update this file + memory as state changes.
