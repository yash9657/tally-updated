> **⚠️ SUPERSEDED (2026-07-24).** Wave 2 is DONE — WS8b `d6a3230`, WS1 `37661a5`, WS5a `0099946`, plus wave-3 WS5b `95ce702`, all on `master`, tree clean. It was implemented DIRECTLY from `docs/WAVE2_DESIGN_CORRECTIONS.md` (the stall was infra, not the plan; direct integration was faster). The `wip/wave2-partial` branch this doc refers to is spent and has been deleted. This file is retained as a historical record; **the authoritative state is `HANDOFF.md`** (only wave-3 WS10 remains, deferred). The runbook below is no longer a live task.

# Session handoff — resume at IMPLEMENTATION_PLAN.md §4 (wave 2)

**Written:** 2026-07-24, end of the session that executed **wave 1** of `IMPLEMENTATION_PLAN.md` and *started* wave 2.
**Branch:** `master`, HEAD = the docs commit that adds this file. Working tree is **clean**.
**Read order for the next session:** this file → `HANDOFF.md` (the standing runbook, now current) → `IMPLEMENTATION_PLAN.md` §4 → `docs/WAVE2_DESIGN_CORRECTIONS.md` (pinned, code-verified corrections for the three wave-2 workstreams).

---

## 1. Where things stand

### ✅ Wave 1 — DONE, committed, green (8 workstreams, `842b82b`…`d5d4e8b`, docs `85065ba`)
WS2 query-DSL widening · WS3 TDS/TCS · WS4 GSTR-3B · WS6 JWKSVerifier + PG WebAuthn challenge store · WS7a statutoryd PG StepStore · WS7b insightd PG DraftStore · WS8a company `state_code` end-to-end · WS9 delete/alter policy + `make verify`. Migrations run to **0014**.

Full §8.1 gate matrix was re-run by the integrator and is green at HEAD:
- backend `go build`/`go vet` clean; `go test ./...` **16/16 packages** (incl. the ~40s sync fuzzer)
- frontend `tsc --noEmit` clean; **126 vitest**; `pnpm run build`
- insightd **70 pytest** (and **57 passed / 13 skipped with psycopg physically removed** — proves the lazy import); PG DraftStore suite 14 passed / 2 skipped against live PG
- `corpora/validate.py` → tax=14, tds=13, tcs=5, keystroke=15, isolation=11, migration=6
- `make verify` end-to-end (its long-standing vitest-watch **hang was fixed in WS9** — `frontend-test` now calls `pnpm run test:run`)
- live e2e: `e2e:edit`, `e2e:numbering`, `e2e:state-code`, `e2e:ocr` (insightd in PG mode), `e2e-statutory-freeze.py` **with a mid-workflow process kill** (same IRN, `generate` row untouched → IRP called exactly once)

### 🟡 Wave 2 — STARTED, then stalled. **Nothing wave-2 is on `master`.**
The wave-2 workflow ran **Design (3/3 completed)** then **Implement + Review (all 6 agents STALLED)** — an infrastructure timeout (`agent stalled … no progress for 180000ms`), **not** a code fault. The stalled implementers had written partial files to disk first.

Two artifacts were salvaged:
1. **`docs/WAVE2_DESIGN_CORRECTIONS.md`** (on `master`) — the three Design agents' code-verified corrections to the §4 specs. **These are authoritative and highly detailed** (exact drifted line numbers, the archtest `allowedDeps` edits WS1 needs, the 503-vs-422 error-mapping fix, the `QUERY_TRANSLATOR_BASE_URL` test seam, the empty-`outstandings`-metrics-map trap for `Catalog()`, etc.). Read this before touching anything.
2. **Branch `wip/wave2-partial`** (commit `b7598cc`) — the partial, **UNVERIFIED** code the stalled agents left behind. It `go build`s but was never self-verified, never reviewed, and **never wired** (no `buildTranslator`, no 503 branch, no archtest entry, no forecast routes/PDP maps). Treat it as a *draft to finish and re-review*, not landed work.

---

## 2. Remaining tasks (priority order)

### Wave 2 (do this next) — `IMPLEMENTATION_PLAN.md` §4
- **WS1 — real-Claude NL→DSL translator.** New `internal/nlq` (stdlib `net/http` only, no SDK). LLM emits ONLY the typed DSL via forced tool-use; the existing whitelist→compiler→read-only-tx path stays the sole authority. Key corrections already pinned in `WAVE2_DESIGN_CORRECTIONS.md`: (a) add **two** `allowedDeps` entries to `archtest/boundaries_test.go` (`internal/nlq → internal/query`, and `internal/nlq` into the `internal/server` set) or the archtest hard-fails; (b) add a real `TestQueryPackageIsNetworkFree` (today "network-free" is only a prose comment); (c) map **any non-200 upstream status → `ErrTranslatorUnavailable` → 503**, keeping 422 strictly for a 200 whose body isn't a valid `Query`; (d) pin `QUERY_TRANSLATOR_BASE_URL` as the test injection seam and use the dead-port idiom `http://127.0.0.1:1`; (e) `Catalog()` must synthesize the `outstandings` `pending` metric and universal `count(*)` (they live outside the whitelist maps); (f) `POST /query` is already routed + PDP-mapped — **do not** touch `auth_middleware.go` for WS1. All gates hermetic (no API key/network in this env).
- **WS5a — deterministic cash-flow projection + rule-based anomaly report.** New `internal/forecast`; two read-only reports, corpus-gated, zero new deps. Reuse `reporting/outstanding.go`'s per-bill pending computation (don't reimplement). Two new routes (`report:cash-flow`, `report:anomalies`) need the §1.5 two-touch wiring (route + `reportName`) — WS1 owns `server.go`/`auth_middleware.go` this wave, so WS5a emits wiring diffs. Independent-oracle gate: infinite-horizon inflow total == `reporting.Outstandings` receivables total.
- **WS8b — F9 per-field edit-through.** Editing a prefilled field must change the POSTed body. Pristine-accept still POSTs the draft body **byte-identical** (regression pin); edited-accept rebuilds a balanced body via `buildGstVoucherRequest`; nothing POSTs before Ctrl+A.

### Wave 3 (stretch, optional) — `IMPLEMENTATION_PLAN.md` §5
- **WS5b** statsmodels Holt-Winters forecast in insightd, **backtest-gated** (MAPE must beat naive baseline by ≥20% or the gate fails) + determinism test. Deps: `requirements-forecast.txt` (statsmodels only — **NOT xgboost**, needs libomp).
- **WS10** PaddleOCR spike (timeboxed half-day); wire real OCR + a real file upload in Bill Inbox only if the spike works offline. **Record findings in `HANDOFF.md` §6 regardless of outcome.**

### Then
- Final pass of the reviewing session against `IMPLEMENTATION_PLAN.md` **§8 Definition of Done** (§8.2 per-WS + §8.3 adversarial meta-checks).
- Update `HANDOFF.md` §4/§6/§7 + memory after each wave; leave the repo clean.

---

## 3. How to resume wave 2 (recommended method)

Follow `HANDOFF.md` §3 + §9 exactly (the pattern that produced wave 1):

1. **Bring up the stack + confirm the green baseline** (§8.1): `docker compose up -d db`; `cd backend && go run ./cmd/cored migrate && go build ./... && go vet ./... && go test ./... -count=1`; frontend `tsc`/`vitest`/`build`; `cd insightd && .venv/bin/python -m pytest`. Any pre-existing red invalidates gate deltas — investigate first.
2. **Decide on the WIP branch.** Either (a) `git checkout wip/wave2-partial -- <paths>` to salvage specific draft files and finish them, or (b) ignore it and re-run the wave-2 workflow fresh — the design corrections make (b) fast and low-risk. Recommendation: **re-run the workflow fresh**, feeding each agent its `WAVE2_DESIGN_CORRECTIONS.md` section verbatim, and optionally diffing the agents' output against the `wip/wave2-partial` draft as a sanity cross-check. The stall was infra, not the plan; the plan is sound.
3. **When you re-run the workflow:** WS1 is the sole owner of `server.go`/`auth_middleware.go`; WS5a and WS8b emit wiring diffs. WS1 and WS5a are backend-disjoint; WS8b is frontend-only. No two wave-2 workstreams share a file, so unlike wave 1 they need **no serialization** — but keep the "one owner for shared wiring files + others emit diffs" rule. Consider `run_in_background: false` or smaller batches if the infra stall recurs.
4. **Integrate yourself:** apply wiring diffs (split the shared-file diff by hunk per workstream at commit time, as in wave 1), apply mustFixes, re-run the **full** §8.1 matrix + live e2e, commit one-per-WS with gates stated, update docs.

### Env vars / gates introduced in wave 1 (you'll need these)
```bash
# production auth (WS6 + D102): AUTH_ISSUER **and** AUTH_AUDIENCE are BOTH REQUIRED with AUTH_JWKS_URL;
# JWKS fetched once at boot (5s), hard-fail if unreachable
AUTH_JWKS_URL=... AUTH_ISSUER=... AUTH_AUDIENCE=... /tmp/cored serve
STATUTORYD_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" /tmp/statutoryd   # WS7a durable runner
INSIGHTD_DATABASE_URL="postgres://tally_app:tally_app@localhost:5433/tally" uvicorn insightd.api.app:app --port 8088  # WS7b (needs requirements-db.txt)
CORED_BASE_URL=http://localhost:8087 pnpm e2e:state-code   # WS8a live gate
# statutory freeze e2e now has an opt-in durable-restart phase (STATUTORYD_BIN + STATUTORYD_DSN)
```
**`AUTH_AUDIENCE` — what to set it to, and why it is not optional (D102, closed 2026-08-27).** Set it to the **OIDC client id / audience cored's tokens are minted for** — the value the IdP puts in the token's `aud`. Keycloak emits `aud` as an **array** and usually includes `"account"` alongside your client id (`"aud":["account","cored"]`); cored accepts both RFC 7519 forms (bare string and array) and requires only that the list **contains** the configured value. Exactly one expected value is supported in v1 — it is **not** comma-split into a list.

⛔ **cored REFUSES TO BOOT if `AUTH_JWKS_URL` is set and `AUTH_AUDIENCE` is empty.** This is deliberate, and `AUTH_ISSUER` does not cover it: `iss` identifies the **realm**, and *every client in a realm mints tokens carrying the same `iss`*. Before this check landed, a token a user had legitimately obtained for **any other application in that realm** authenticated against cored as that subject — the issuer pin cannot see the difference. The constraint used to live only as a "deploy the realm with trusted clients only" sentence in a source comment (`backend/internal/authn/jwks.go`), where no operator would ever read it; it is now enforced at boot and written here.

⚠️ In JWKS mode there is **no in-process signer**, so any service principal (`svc:` subject) must obtain its token from the IdP — and that token now needs cored's `aud` too. Nothing in the tree mints such a token today, so this changes nothing that currently works, but plan for it before wiring statutoryd to a real IdP.

Boot cored for e2e with `AUTH_DEV_TOKEN_ENABLED=true PORT=8087 APP_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"`. Concurrent workflow agents must use unique high ports and kill their servers when done.

---

## 4. Honest edges to keep visible (already in HANDOFF §6, don't re-drop)
- GSTR-3B is a **books-derived summary**, ITC all-eligible, NIC field names unverified → no portal-upload claim.
- TDS/TCS thresholds are **per-transaction in v1** (annual/monthly are the true basis → v1 **under-deducts**; each rule records its statutory `Basis`). Rates were curated offline and spot-checked vs live sources — **re-verify against current law before any release.**
- ~~Auth `aud` claim is **never validated** — `iss` pins the realm, not the client.~~ **CLOSED (D102, 2026-08-27):** `aud` is now parsed (both RFC 7519 forms) and enforced against `AUTH_AUDIENCE`, which cored refuses to boot without whenever `AUTH_JWKS_URL` is set. See the env-var block in §3. Live-Keycloak smoke test **is still a follow-up** — the audience path is proven against a hermetic JWKS server, not against a real Keycloak.
- `statutory_step` has **no RLS** (deliberate; not user-facing) and statutoryd now holds a `tally_app` DSN → a narrower DB role is a follow-up.
- Re-DELETE is **retry-safe, not serialisable** (head-probe and tombstone write are two txns).
- Deferred entirely (tracked in `IMPLEMENTATION_PLAN.md` §7): Tauri desktop shell (no Rust toolchain here), OPA/Rego, real GSP/NIC adapter + e-way bill + full GSTR filing, sequencer/statutoryd HA.
