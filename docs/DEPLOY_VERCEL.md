# Deploying Tally on Vercel + Neon

Everything runs on Vercel (Pro). Postgres is a Neon free-tier project with
pgvector. Nothing else is paid for.

> Status: prepared and verified locally (see §9). **Nobody has deployed this
> yet.** Placeholders you must fill in are written `<LIKE_THIS>`.

---

## 1. What runs where

| Service | Code | What it does | Needed? | Where it runs |
|---|---|---|---|---|
| **frontend** | `frontend/` (Vite + React) | Public landing page, separate sign-in, and authenticated keystroke UI. Calls `/v1/*` and `/auth/*` same-origin. | **Yes** | Existing Vercel project `tally-updated` (static build) |
| **cored** | `backend/cmd/cored` (Go) | The system of record: ledger, masters, reports, auth, PDP, agent and insight planes. The browser talks to cored and to nothing else. | **Yes** | New Vercel project `tally-cored`, Container Runtime (`backend/Dockerfile.vercel`) |
| **insightd** | `insightd/` (Python, FastAPI) | Bill Inbox: document -> purchase-voucher **draft**. Reached only through cored's `/v1/companies/{id}/drafts*` proxy with a shared service token. | Optional (Bill Inbox only) | New Vercel project `tally-insightd`, Container Runtime (`insightd/Dockerfile.vercel`) |
| **statutoryd** | `backend/cmd/statutoryd` (Go) | IRN / e-invoice. Runs a **fake** IRP provider only, calls back into cored with a `svc:statutoryd` token minted by cored's dev `/auth/token`. | No | **Not deployed** (see §8). E-invoice actions answer an error; everything else works. |
| **Postgres** | `backend/migrations` (86 files) | All state. pgvector for embeddings (0098); without it search is lexical-only. | **Yes** | Neon free tier |

### Why three Vercel projects and not one project with Vercel Services

The frontend is already a Vercel project (`rootDirectory: frontend`,
domain `tallyweb.world`). Adding cored and insightd as **separate projects** in
the same repo needs no change to that project except a `vercel.json`, and each
backend is just "a Dockerfile at the project root", which is the Container
Runtime's simplest case.

Vercel Services (one project, `vercel.json` `services` + `rewrites`) would make
insightd *private* (reachable only by binding, not from the internet) and ship
all three atomically. It is the better end state, but it means moving the
existing project's Root Directory to the repo root and Services is newer
surface. insightd already refuses every request without the service token
(and its OpenAPI docs are unmounted), so public reachability is acceptable for
a demo. Revisit Services when moving past the demo.

### Request path in production

```
browser ── https://tallyweb.world/v1/*, /auth/* ──> frontend project (rewrite, same-origin, no CORS)
                                                        │
                                                        ▼
                                  https://<CORED_HOST>/v1/*  (tally-cored container)
                                                        │ X-Insightd-Service-Token
                                                        ▼
                                  https://<INSIGHTD_HOST>/companies/{id}/drafts* (tally-insightd, optional)
cored, insightd ──(TLS, direct/unpooled)──> Neon Postgres, as role tally_app (NOBYPASSRLS)
```

---

## 2. Neon setup

1. Create a Neon project. **Region:** pick the one next to the Vercel function
   region you will use (§4). For Indian users: Neon `AWS Asia Pacific 1
   (Singapore)` + Vercel `sin1`. Postgres **16 or 17** (17 verified locally;
   the migrator needs 16+).
2. Keep the default database (`neondb`) and owner role (`neondb_owner`).
3. Copy two connection strings from *Connect*:
   - **Direct** (Connection pooling **off**) for the owner -> used **only** to run
     migrations (`NEON_DIRECT_URL`).
   - You do **not** use the `-pooler` host anywhere. cored takes session-level
     advisory locks (`internal/durable/advisory.go`, the migration runner), which
     PgBouncer transaction pooling breaks. Connections stay small: pgx opens at
     most `pool_max_conns` per container instance.
4. pgvector: nothing to do. Migration 0098 runs `CREATE EXTENSION vector`, which
   Neon permits for `neondb_owner`. `cored migrate` prints
   `embeddings: enabled: pgvector <version>` when it worked.

Neon free tier limits change; check them. Relevant here: storage cap, monthly
compute hours, and scale-to-zero after 5 idle minutes (first query after idle
waits for Neon to wake, ~hundreds of ms). The hourly cron in §5 wakes the
database at most once an hour.

---

## 3. Migrations (run from your machine, before the first deploy and after every schema change)

`cored serve` never migrates. Migrations run as the database **owner**, over
the **direct** URL:

```bash
cd backend
export NEON_DIRECT_URL='postgresql://neondb_owner:<OWNER_PW>@<ep-xxx>.<region>.aws.neon.tech/neondb?sslmode=require&channel_binding=require'
export TALLY_APP_ROLE_PASSWORD="$(openssl rand -hex 24)"   # keep it: it goes into APP_DSN below
scripts/migrate-neon.sh
# expected tail:
#   migrations applied (86 files)
#   embeddings: enabled: pgvector 0.8.x
```

No Go installed? Build the image once and let the script run it:
`docker build -f Dockerfile.vercel -t tally-cored:vercel . && MIGRATE_IMAGE=tally-cored:vercel scripts/migrate-neon.sh`.

What the script does and why:

- Refuses a `-pooler` URL.
- Passes `TALLY_APP_ROLE_PASSWORD` / `TALLY_STATUTORY_ROLE_PASSWORD` to
  `cored migrate`, which hands them to migrations 0001 / 0024 as session
  settings. Neon rejects the dev passwords (`tally_app`, `tally_statutory`:
  minimum 60 bits of entropy), and a known password on an internet-reachable
  database would be a hole anyway. With neither set (local dev, CI) the dev
  values are used exactly as before.
- `cored migrate` detects a **non-superuser** migrator (Neon's `neondb_owner`
  has CREATEROLE + BYPASSRLS but is not SUPERUSER) and sets
  `createrole_self_grant = 'set, inherit'`, so migrations 0097/0099 may hand
  their views/functions to the roles they just created. 0097/0099 also grant the
  new owners `CREATE` on schema `agent` for the hand-over only and revoke it in
  the same transaction (the check superusers skip). The committed privileges
  are identical to a superuser migration.
- Idempotent: re-running applies only new files. Roles that already exist keep
  their passwords. To rotate later: `ALTER ROLE tally_app PASSWORD '<new>';` in
  the Neon SQL editor as `neondb_owner`, then update `APP_DSN`.

The app connection string (used by cored and insightd) is the **direct** host
with the `tally_app` role:

```
APP_DSN=postgresql://tally_app:<TALLY_APP_ROLE_PASSWORD>@<ep-xxx>.<region>.aws.neon.tech/neondb?sslmode=require&channel_binding=require&pool_max_conns=5
```

(`pool_max_conns` is read by pgx; omit it from insightd's URL, psycopg does not
know it.)

---

## 4. Vercel project settings

### 4.1 `tally-cored` (new project, required)

| Setting | Value |
|---|---|
| Git repository | `yash9657/tally-updated` |
| Root Directory | `backend` |
| Framework Preset | Other |
| Build / Install / Output commands | leave empty (Vercel detects `backend/Dockerfile.vercel` and builds the image) |
| Production branch | `master` (deploy the `deploy/vercel` branch as a preview first) |
| `vercel.json` | `backend/vercel.json` (two cron jobs, §5) |
| Settings -> Functions -> Function Region | same region as Neon, e.g. `sin1` |
| Settings -> Functions -> CPU | Standard (2 GB / 1 vCPU) is enough |
| Max duration | default 300 s is enough (longest calls are LLM calls, 60 s client timeout) |
| Domain | add `api.tallyweb.world` (recommended) - this is `<CORED_HOST>` |
| Deployment Protection | Standard Protection is fine **as long as the frontend rewrites to a production domain** (`api.tallyweb.world` or `tally-cored.vercel.app`), which Standard Protection leaves public. Never point the rewrite at a generated deployment URL. |

Container contract (`backend/Dockerfile.vercel`): static Go binary on
distroless (non-root, CA certificates included), `ENTRYPOINT /cored`,
`CMD serve`, listens on `$PORT` (baked default 8080). Vercel sends traffic to
port 80 unless the **project** env var `PORT` says otherwise, so `PORT=8080`
is required (below). Image is ~8 MB; cold start measured ~2-3 s under x86
emulation, including the first Neon round-trip.

### 4.2 `tally-insightd` (new project, optional: Bill Inbox)

| Setting | Value |
|---|---|
| Root Directory | `insightd` |
| Framework Preset | Other |
| Build / Install / Output commands | leave empty (`insightd/Dockerfile.vercel`) |
| Function Region | same as cored / Neon |
| Domain | `tally-insightd.vercel.app` or `insightd.tallyweb.world` - this is `<INSIGHTD_HOST>` |

The image installs the core runtime + Postgres draft store + Anthropic
adapters. Real OCR (PaddleOCR, multi-GB) and Presidio are **not** installed;
with no `INSIGHTD_USE_REAL_*` flag every pipeline stage is the deterministic
fake, which is what the demo shows.

### 4.3 `tally-updated` (existing frontend project)

| Setting | Value |
|---|---|
| Root Directory | `frontend` (unchanged) |
| Framework Preset | Vite (unchanged) |
| Install / Build | defaults (`pnpm install`, `pnpm build` = `tsc --noEmit && vite build`); output `dist` |
| `frontend/vercel.json` | proxies `/v1/:path*` and `/auth/:path*` to `https://api.tallyweb.world/...`; serves `/signin` and `/app` through `index.html` |

The API rewrites keep the browser same-origin (no CORS), and the
`Authorization` header is forwarded. Update both API destinations if cored's
production hostname changes. `/` is the public landing page, `/signin` holds
the login form, and `/app` requires an authenticated session. Direct loads and
refreshes of both application routes resolve to the SPA. Historical
`/?company=...` links are preserved through sign-in.

---

## 5. Background work on a scale-to-zero container

`cored serve` starts three goroutines (`cmd/cored/main.go`):

| Goroutine | Interval | Does |
|---|---|---|
| `RunNotifications` | 15 s | drains the invite outbox. The only sender is `notify.LogSender` (logs, sends no mail). |
| `RunAgentPoller` | 2 s (+ 24 h anchor ticker) | resumes parked/retrying agent runs; writes daily intent-log anchors |
| `RunInsightPoller` | 5 s | processes queued voucher events; after 02:00 IST runs each company's nightly pass + digest |

They are kept (harmless, and they make work prompt while an instance is warm),
but a Vercel container scales to zero after 5 idle minutes, and is not
guaranteed CPU between requests. So the same jobs are exposed as cron routes,
**mounted only when `CRON_SECRET` is set** and requiring
`Authorization: Bearer <CRON_SECRET>` (what Vercel Cron sends):

| Route | Schedule (`backend/vercel.json`, UTC) | Runs one pass of |
|---|---|---|
| `GET /internal/cron/tick` | `0 * * * *` (hourly) | notifications, agent poller, insight events, insight nightly (no-ops before 02:00 IST and once per company per day) |
| `GET /internal/cron/anchor` | `30 21 * * *` (03:00 IST) | intent-log anchors |

Every job is safe to overlap with a warm instance's goroutines and with other
instances (advisory locks / `SKIP LOCKED` / unique keys). Hourly is a cost
choice: each tick wakes Neon for ~5 minutes. Tighten it if parked agent runs
must resume faster.

In-memory state elsewhere: only caches (JWKS, translator clients). The WebAuthn
challenge store, lockout counters and drafts are all in Postgres, so any
instance can serve any request.

---

## 6. Environment variables

"Secret" = mark *Sensitive* in Vercel. Set for **Production** (and Preview if
you deploy previews against the same database).

### 6.1 `tally-cored`

| Name | Secret | Value | Notes |
|---|---|---|---|
| `PORT` | no | `8080` | **Required.** Container listens here (non-root). |
| `APP_DSN` | **yes** | §3 app URL (`tally_app`, direct host, `pool_max_conns=5`) | **Required.** |
| `AUTH_DEV_TOKEN_ENABLED` | no | `true` | **Required for the current frontend** (see §7). |
| `AUTH_DEV_SIGNING_KEY` | **yes** | output of `backend/scripts/gen-dev-signing-key.sh` | **Required** whenever the dev issuer is on: without it every instance (and every cold start) generates its own key and sessions break across instances. |
| `AUTH_DEV_TOKEN_ROUTE_DISABLED` | no | `true` after seeding | Unmounts `POST /auth/token` (any-subject token mint); `POST /auth/login` keeps working. §7. |
| `AUTH_WEBAUTHN_RPID` | no | `tallyweb.world` | Passkey step-up (Owner writes need it). |
| `AUTH_WEBAUTHN_ORIGIN` | no | `https://tallyweb.world` | Must equal the browser origin. |
| `CRON_SECRET` | **yes** | `openssl rand -hex 32` | Enables §5 cron routes. |
| `INSIGHTD_BASE_URL` | no | `https://<INSIGHTD_HOST>` | Only with insightd deployed. |
| `INSIGHTD_SERVICE_TOKEN` | **yes** | `openssl rand -hex 32` (same value on insightd) | Only with insightd. Unset = Bill Inbox answers 503 naming the variable. |
| `INSIGHTD_TIMEOUT` | no | e.g. `60s` | optional |
| `LLM_PROVIDER` | no | `anthropic` \| `fireworks` \| `deepinfra` \| `sarvam` \| `vllm` | optional; unset = deterministic FakeTranslator, agent LLM off |
| `LLM_MODEL` | no | model id | required with a non-anthropic provider |
| `LLM_API_KEY` | **yes** | provider key | with `LLM_PROVIDER` |
| `LLM_BASE_URL`, `LLM_TIMEOUT` | no | | optional |
| `LLM_BUDGET_TOKENS_PER_MONTH` | no | e.g. `2000000` | **recommended whenever an LLM is configured** on a public demo (per-tenant cap) |
| `ANTHROPIC_API_KEY` | **yes** | | legacy NL-query path when `LLM_PROVIDER` is unset; prefer `LLM_*` |
| `MCP_ALLOWED_ORIGINS` | no | comma list | optional, MCP endpoint only |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, `OTEL_EXPORTER_OTLP_HEADERS` (secret), `OTEL_SERVICE_NAME`, `TRAJECTORY_SAMPLE_RATIO` | | | optional tracing |
| `STATUTORYD_BASE_URL`, `STATUTORYD_TIMEOUT` | no | leave unset | statutoryd is not deployed |

**Never set on Vercel:** `CORED_ENV=dev` (silently enables the dev issuer),
`CORED_SKIP_DB`, `TRAJECTORY_RECORD`, `DATABASE_URL` / `ADMIN_DATABASE_URL` /
`TESTDB_*` (test-only), `ADMIN_DSN` and `TALLY_*_ROLE_PASSWORD` (migrate-only,
run from your machine).

### 6.2 `tally-insightd` (optional)

| Name | Secret | Value |
|---|---|---|
| `PORT` | no | `8080` (**required**) |
| `INSIGHTD_SERVICE_TOKEN` | **yes** | same value as on cored |
| `INSIGHTD_DATABASE_URL` | **yes** | `postgresql://tally_app:<pw>@<direct host>/neondb?sslmode=require` (durable drafts; unset = drafts in memory, lost on scale-down) |
| `INSIGHTD_USE_REAL_INFERENCER`, `INSIGHTD_USE_REAL_CLASSIFIER` + `ANTHROPIC_API_KEY` (secret) | | optional real LLM stages; OCR stays fake |

### 6.3 `tally-updated` (frontend, **build-time**)

| Name | Secret | Value | Notes |
|---|---|---|---|
| `VITE_LIVE` | no | optional | Legacy standalone `App` composition flag. The browser entry explicitly enables auth for `/signin` and `/app`, independent of this flag. |
| `VITE_HOME_STATE_CODE` | no | e.g. `29` | optional default state for new companies |

The browser entry uses same-origin APIs; it does not need `VITE_CORED_BASE_URL`.
That variable remains available only to standalone `App` development harnesses.

---

## 7. Authentication - a decision for the owner

What the code supports (`internal/server/server.go` `buildAuth`):

1. **External IdP (JWKS)** - `AUTH_JWKS_URL` + `AUTH_ISSUER` + `AUTH_AUDIENCE`.
   Production-grade, but **the frontend has no OIDC sign-in flow**: its only
   login is `POST /auth/login` (subject + password), which exists only with the
   in-process signer. Choosing this needs frontend work **and** an IdP account
   (Keycloak, Auth0, Clerk, ...). Not usable today.
2. **In-process dev issuer** - `AUTH_DEV_TOKEN_ENABLED=true` +
   `AUTH_DEV_SIGNING_KEY`. Enables `POST /auth/login` (password, lockout,
   passkey step-up all work) **and** `POST /auth/token`, which mints a valid
   token for **any subject, including `svc:*` service principals, for anyone
   on the internet**. With that route public, anyone can act as any user and
   read or write any company.
3. **Option 2 hardened (added on this branch)** - additionally
   `AUTH_DEV_TOKEN_ROUTE_DISABLED=true`: `/auth/token` is not mounted (404);
   only accounts that already have a password can sign in.

**Recommendation for a public demo: option 3, no IdP needed.** Procedure:

1. Deploy cored with `AUTH_DEV_TOKEN_ENABLED=true`, `AUTH_DEV_SIGNING_KEY`, and
   `AUTH_DEV_TOKEN_ROUTE_DISABLED` **unset**, ideally before the frontend goes
   live.
2. Seed from your machine: `cd frontend && CORED_BASE_URL=https://<CORED_HOST>
   pnpm seed:demo` (creates the Demo Traders company and prints the Owner's
   login; skip `--insightd` unless insightd is deployed). Create any further
   accounts the same way (`/auth/token` + `/auth/password`).
3. Set `AUTH_DEV_TOKEN_ROUTE_DISABLED=true` and redeploy. From then on new
   users cannot self-register; adding one means briefly repeating 1-3.

Residual risk of option 3: tokens are signed by a key held in a Vercel env var
(rotate by changing `AUTH_DEV_SIGNING_KEY`, which logs everyone out); there is
no self-service sign-up or password reset. The Owner level requires a WebAuthn
passkey for writes, so `AUTH_WEBAUTHN_RPID` / `AUTH_WEBAUTHN_ORIGIN` must match
`tallyweb.world`; passkeys will not work on preview URLs.

Running option 2 (token route open) in production is **not** recommended and
should only ever be an explicit, temporary owner decision.

---

## 8. Known limitations and risks

- **Request/response bodies are capped at 4.5 MB** by Vercel (`413
  FUNCTION_PAYLOAD_TOO_LARGE`). cored itself accepts up to 32 MiB for Tally XML
  import, bank/ITC statements and backup restore; files above ~4.5 MB must be
  split. insightd uploads are base64 JSON, so real bills above ~3.3 MB fail.
- **statutoryd is not deployed.** It has no inbound authentication (it trusts
  that only cored can reach it), it needs a `svc:statutoryd` token minted by
  `/auth/token`, and it only has a fake IRP provider. Deploying it publicly
  would be a confused deputy. Needs Vercel Services (private binding) plus a
  service-token story first.
- **insightd is public** (separate project). Every route except `/healthz`
  requires the service token. Moving to Vercel Services would make it private.
- **Real OCR is not available** on Vercel (PaddleOCR's size and CPU profile);
  the Bill Inbox runs the deterministic fake pipeline unless LLM stages are
  enabled. insightd's "enqueue-and-poll" path for slow OCR uses
  `BackgroundTasks`, which on a request-scoped container is not guaranteed to
  run after the response.
- **Cold starts**: ~2-3 s for cored (plus Neon wake-up if it was idle). The
  first request after 5 idle minutes is slow.
- **Background jobs** run hourly via cron, plus whenever an instance is warm
  (§5). Vercel's guarantee for CPU between requests in a container is not
  documented; the cron routes are what guarantees progress.
- **Vercel Cron on a container project**: the routes are plain HTTP paths on
  the container, so they should be invoked like any other path, but this was
  not exercisable locally. After the first production deploy, check
  *Settings -> Cron Jobs* and the logs for a `200` from `/internal/cron/tick`.
- **Migration edits**: 0001, 0024, 0097, 0099 were changed (role passwords from
  session settings; temporary schema `CREATE` for ownership hand-over). The
  runner does not checksum files, so already-migrated databases are
  unaffected; a fresh superuser-migrated database ends in the same state as
  before.
- `migrate` must be run by hand before deploying a commit that adds
  migrations; a deploy does not migrate.
- Neon free-tier compute hours and storage are the practical ceiling; a
  public demo with heavy use may exhaust them.

---

## 9. Local verification (what was run on the `deploy/vercel` branch)

| Check | Result |
|---|---|
| `docker buildx build --platform linux/amd64 -f backend/Dockerfile.vercel backend` | builds; image ~8 MB, amd64, user `nonroot`. The Go compile runs under emulation on an ARM laptop: several minutes. |
| `docker buildx build --platform linux/amd64 -f insightd/Dockerfile.vercel insightd` | builds; ~60 MB |
| Neon emulation: `pgvector/pgvector:pg17`, owner role `neondb_owner` = `LOGIN CREATEDB CREATEROLE BYPASSRLS` (not superuser), `CREATE EXTENSION vector` done as postgres to stand in for Neon's extension allow-list; `scripts/migrate-neon.sh` (both `go run` and `MIGRATE_IMAGE`) | `migrations applied (86 files)`, `embeddings: enabled: pgvector 0.8.7`; re-run is a no-op; ACLs on schema `agent` and its objects match a superuser-migrated database (only PG17's extra `MAINTAIN` bit differs) |
| `scripts/migrate-neon.sh` with a `-pooler` URL | refused |
| cored image against the emulated Neon DB as `tally_app` (strong password), `AUTH_DEV_TOKEN_ENABLED` + fixed `AUTH_DEV_SIGNING_KEY` | `/healthz` 200 (ready in ~2-3 s); seed via `/auth/token`, `POST /v1/companies` 201, `POST /auth/password` 204; restarted with `AUTH_DEV_TOKEN_ROUTE_DISABLED=true`: `/auth/token` 404, `/auth/login` 200, `/v1/me` 200, ledgers 200; `/internal/cron/tick` 401 without / 200 with the bearer, `/internal/cron/anchor` 200; `docker stop` drains and exits promptly |
| insightd image + cored image, shared service token, durable draft store on the emulated DB | insightd `/healthz` 200 (all stages fake); direct call without the token 401; `GET /v1/companies/{id}/drafts` through cored 200 |
| cored image `migrate` against a fresh `tally_vercel_test` database in the docker-compose container | 86 applied. That container currently runs `postgres:16`, not the compose file's pgvector image, so embeddings report DISABLED there |
| `go build ./... && go vet ./...` | clean |
| `go test ./...` on a fresh `pgvector/pgvector:pg16-trixie` DB, `EMB_REQUIRE_VECTOR=1` | 50 packages ok, 1 FAIL: `internal/tierb` `TestAcceptedAdversarialQueriesLeakNothing`. **Same result on the base commit** (pre-existing: it fails only in the full parallel run after 02:00 IST, and passes alone on both commits) |
| frontend `pnpm install --frozen-lockfile && pnpm typecheck && VITE_LIVE=1 pnpm build` | clean |
| frontend `vitest run` under Node 22 (CI's version) | 128 files, 2040 tests passed. (Under Node 26, 33 tests fail because Node's own experimental `localStorage` shadows jsdom's; unrelated to this branch.) |
