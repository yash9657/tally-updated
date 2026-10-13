# Phase-0 developer + CI entrypoints. Each target maps to a verification gate in PLAN.md Part C.
.DEFAULT_GOAL := help

# Connection conventions shared by backend code, migrations, and tests.
# ADMIN role (superuser) is used ONLY for migrations + role creation in dev/CI.
# APP role is NOBYPASSRLS and is what application code + isolation tests connect as.
ADMIN_DATABASE_URL ?= postgres://postgres:postgres@localhost:5433/tally?sslmode=disable
DATABASE_URL       ?= postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable
export ADMIN_DATABASE_URL
export DATABASE_URL

# insightd's DURABLE draft store. With this unset, insightd silently falls back
# to an in-memory DraftStore and every Bill Inbox draft dies with the process —
# the inbox goes empty on restart with nothing to explain why. Same APP role as
# the rest of the stack, so migration 0014's RLS scopes it identically.
INSIGHTD_DATABASE_URL ?= $(DATABASE_URL)
export INSIGHTD_DATABASE_URL

# Local dev ports. cored's own default is 8080; the frontend and the seed script
# both expect 8087, so it must be set explicitly or `serve` binds the wrong port.
CORED_PORT    ?= 8087
INSIGHTD_PORT ?= 8088

# THE cored -> insightd SERVICE CREDENTIAL (register row D49). insightd is now
# reached ONLY through cored's draft proxy, and it refuses any request that does
# not present this token — so BOTH processes must be given the same value or the
# Bill Inbox answers 401 with cored's own message naming this variable.
#
# ⛔ A DEV DEFAULT IS SAFE HERE AND A COMPILED-IN ONE IS NOT, which is why the
# value lives in this file and not in Go. This Makefile runs a loopback dev stack
# against a throwaway database; a default inside the binary would be a credential
# published in the repository, shipped to every deployment, and defaulted to by
# every operator who did not know it existed. cored's newDraftProxy therefore
# keeps an empty token as EMPTY and refuses to forward, and insightd answers 503
# "unconfigured" — a deployment that forgets this gets two loud refusals rather
# than a quiet shared secret.
INSIGHTD_SERVICE_TOKEN ?= dev-insightd-service-token
export INSIGHTD_SERVICE_TOKEN
# statutoryd — the IRN e-invoice plane, a SEPARATE deployable that calls BACK
# into cored. ⛔ Check this port before assuming a fresh instance bound: a
# seven-day-old /tmp/statutoryd once held it while carrying a service token
# signed by a cored key that had died a week earlier, and the only symptom was a
# bare 401 from a call it made — naming neither the process, the token, nor the
# expiry. `lsof -nP -iTCP:8091`.
STATUTORYD_PORT ?= 8091
export CORED_PORT
export INSIGHTD_PORT
export STATUTORYD_PORT

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n",$$1,$$2}'

dev-db: ## Start local Postgres 16 (docker, port 5433)
	docker compose up -d db
	@echo "waiting for postgres..."; \
	for i in $$(seq 1 30); do docker compose exec -T db pg_isready -U postgres -d tally >/dev/null 2>&1 && break; sleep 1; done; \
	echo "postgres ready on :5433"

dev-db-down: ## Stop and remove local Postgres
	docker compose down -v

# cored reads ADMIN_DSN / APP_DSN (cmd/cored/main.go:69,85; hardcoded defaults at
# :29-31). This Makefile exports ADMIN_DATABASE_URL / DATABASE_URL. Those are
# DIFFERENT NAMES, so until this line existed `make migrate` ignored the Makefile's
# own variables entirely and always hit cored's compiled-in defaults. That was
# harmless only because the two happened to be equal; the moment `reset-dev-db` is
# parameterised on ADMIN_DATABASE_URL the mismatch drops one database and migrates
# another, and reports success. Bridge the names here, once.
migrate: ## Apply backend migrations to local db (uses ADMIN then APP roles)
	cd backend && ADMIN_DSN="$(ADMIN_DATABASE_URL)" APP_DSN="$(DATABASE_URL)" go run ./cmd/cored migrate

backend-build: ## go build + vet
	cd backend && go build ./... && go vet ./...

backend-test: ## Backend unit + RLS/isolation integration tests (needs dev-db)
	cd backend && go test ./... -count=1

# The SAME suite, wrapped in a before/after row-count snapshot of the six tables a
# leaked test company drags with it. The bound is ZERO. This is what `verify`
# depends on — it replaces `backend-test` there rather than being added alongside
# it, so the suite still runs exactly once. `backend-test` stays for anyone who
# wants the raw run without a database snapshot.
backend-test-residue: ## Backend suite + PROOF it left no rows in the dev database (bound 0)
	bash backend/scripts/test_residue.sh

tier0: ## W8 Tier 0 — replay every cassette with no LLM, no network, no database
	# Mirrors the `tier0` CI job. CORED_SKIP_DB=1 because this gate must not need
	# a database; TRAJECTORY_RECORD is cleared because with it set the seed test
	# RE-RECORDS the fixtures and asserts nothing (cassette_seed_test.go says so
	# in terms). Runs the whole package rather than a -run regex, so a new
	# Tier-0 test cannot be silently left out.
	cd backend && CORED_SKIP_DB=1 TRAJECTORY_RECORD= go test ./internal/trajectory/ -count=1

frontend-install: ## Install frontend deps
	cd frontend && pnpm install --frozen-lockfile || cd frontend && pnpm install

frontend-test: frontend-install ## Frontend unit tests (XState keystroke machine)
	# `pnpm test` is `vitest` = WATCH mode: under pnpm 9 the `-- --run` forwarding
	# no longer reaches vitest, so this target used to hang forever and `make
	# verify` could never complete. Call the explicit one-shot script instead.
	cd frontend && pnpm run test:run

frontend-build: frontend-install ## Type-check + build frontend
	cd frontend && pnpm build

corpora-verify: ## Independently re-derive the golden tax corpora (no deps beyond stdlib python3)
	python3 corpora/validate.py

# insightd's pytest suite is the Python plane's gate. Its deps live in a venv
# that is NOT required to exist (a Go-only contributor must still be able to run
# `make verify`), so this target SKIPS loudly rather than failing when the venv
# is absent. CI installs the deps, so CI never takes the skip path.
#
# No `-q` here on purpose: pyproject.toml already carries `addopts = "-q"`, so
# adding a second one makes it `-qq`, which SUPPRESSES the "N passed, M skipped"
# summary line. Exit 0 is not acceptance for this suite — with Postgres down the
# whole PG group turns into 13 silent skips and still exits 0 — so the counts have
# to be visible in the log.
insightd-test: ## insightd pytest (SKIPS loudly when insightd/.venv is absent)
	@if [ -x insightd/.venv/bin/python ]; then \
		cd insightd && .venv/bin/python -m pytest; \
	else \
		echo "*** SKIP insightd-test: insightd/.venv/bin/python not found."; \
		echo "***   create it with: python3 -m venv insightd/.venv && insightd/.venv/bin/pip install -r insightd/requirements.txt"; \
		echo "***   (the insightd plane is therefore NOT verified by this run)"; \
	fi

# APP_DSN, not DATABASE_URL — see the note on `migrate` above. Without it a
# customised DATABASE_URL is silently ignored and cored serves the compiled-in
# default database, which is the same bug one layer down.
# STATUTORYD_BASE_URL is passed EXPLICITLY rather than left to cored's compiled-in
# default, and that is the same class of bug the APP_DSN note above records: cored
# defaults to http://localhost:8091, so a customised STATUTORYD_PORT would be
# honoured by `run-statutoryd` and silently ignored by the proxy — and the symptom
# would be "statutoryd is not answering" while statutoryd is plainly running on
# the port this Makefile printed.
run-cored: ## Run cored locally on :$(CORED_PORT) with dev token minting enabled
	cd backend && PORT=$(CORED_PORT) AUTH_DEV_TOKEN_ENABLED=true APP_DSN="$(DATABASE_URL)" \
		STATUTORYD_BASE_URL="http://localhost:$(STATUTORYD_PORT)" \
		INSIGHTD_BASE_URL="http://localhost:$(INSIGHTD_PORT)" \
		INSIGHTD_SERVICE_TOKEN="$(INSIGHTD_SERVICE_TOKEN)" \
		go run ./cmd/cored serve

# ⚠️ NO `--host`, SO UVICORN BINDS 127.0.0.1 — AND THAT WAS AN ACCIDENT, NOT A
# CONTROL, FOR THE WHOLE OF D49's OPEN LIFE. Adding `--host 0.0.0.0` here, or
# running vite with `--host`, exposed all twelve routes with no code change and no
# test failing. It is still a good default, but it is no longer what keeps the
# drafts private: INSIGHTD_SERVICE_TOKEN is. Off-loopback exposure now costs an
# attacker the credential rather than a command-line flag.
run-insightd: ## Run insightd on :$(INSIGHTD_PORT) with the DURABLE (Postgres) draft store
	@if [ ! -x insightd/.venv/bin/python ]; then \
		echo "*** insightd/.venv missing — create it first (see insightd-test)"; exit 1; \
	fi
	@if ! insightd/.venv/bin/python -c "import psycopg" >/dev/null 2>&1; then \
		echo "*** psycopg missing: pip install -r insightd/requirements-db.txt"; \
		echo "*** without it insightd cannot use the durable draft store"; exit 1; \
	fi
	cd insightd && INSIGHTD_DATABASE_URL="$(INSIGHTD_DATABASE_URL)" \
		INSIGHTD_SERVICE_TOKEN="$(INSIGHTD_SERVICE_TOKEN)" \
		.venv/bin/python -m uvicorn insightd.api.app:app --port $(INSIGHTD_PORT)

run-statutoryd: ## Run statutoryd on :$(STATUTORYD_PORT) with the DURABLE store (needs cored running)
	@# ⛔ THE TOKEN IS MINTED HERE, NOT PASTED. It expires in 3600s and EVERY cored
	@# restart invalidates it (a restart mints a new ephemeral signing key), so a
	@# hand-copied token is a trap: both failures present as a bare 401 from a call
	@# statutoryd makes, surfaced as `{"error":"statutory: GET voucher: unexpected
	@# status 401"}` — which names neither the token nor the expiry. Re-run this
	@# target whenever you restart cored.
	@#
	@# The subject MUST be exactly svc:statutoryd: cored's auth middleware has a
	@# "svc:" FAST PATH built for this caller (auth_middleware.go) — no app_user
	@# row, no membership, no security level, no step-up.
	@if ! curl -sf http://localhost:$(CORED_PORT)/healthz >/dev/null; then \
		echo "*** cored is not answering on :$(CORED_PORT) — statutoryd calls BACK into it."; \
		echo "*** Start it first: make run-cored"; exit 1; \
	fi
	@echo "minting the svc:statutoryd service token from cored on :$(CORED_PORT)…"
	@TOK=$$(curl -sf -X POST http://localhost:$(CORED_PORT)/auth/token \
		-H 'Content-Type: application/json' -d '{"subject":"svc:statutoryd"}' \
		| python3 -c "import sys,json;print(json.load(sys.stdin)['token'])") && \
	if [ -z "$$TOK" ]; then \
		echo "*** could not mint a service token — is AUTH_DEV_TOKEN_ENABLED=true set on cored?"; exit 1; \
	fi && \
	cd backend && CORED_BASE_URL=http://localhost:$(CORED_PORT) CORED_SERVICE_TOKEN="$$TOK" \
		PORT=$(STATUTORYD_PORT) STATUTORYD_DSN="$(DATABASE_URL)" go run ./cmd/statutoryd
	@# Confirm the log says DURABLE, not the in-memory WARNING:
	@#   "statutoryd: DURABLE StepStore on Postgres (table statutory_step)"
	@# An in-memory store loses workflow state on restart, and a crash between IRN
	@# generation and the freeze hook then mints a SECOND IRN.

seed-demo: ## Seed the Demo Traders company (needs cored + insightd running)
	cd frontend && pnpm seed:demo -- --insightd

# Both seeders, in the ONE order that works, with the ids handed between them on
# disk instead of copy-pasted out of a log line.
#
# ORDERING IS NOT COSMETIC. seed-tenancy takes company A as argv[2], so demo must
# run first. And after a wipe `pnpm e2e:ui` signs in as `browser-dev-owner`, whose
# password exists ONLY because a seeder called auth.mts's ensurePassword — so
# seed-all must precede e2e:ui or the browser gate fails at the login screen with
# a 401 that looks like an auth regression.
#
# Kept OUT of reset-dev-db deliberately: seeding drives the REST API, so it needs
# cored and insightd already listening. reset-dev-db drops the database out from
# under them, so they have to be restarted in between.
seed-all: ## Seed demo + tenancy in order (needs cored + insightd running)
	cd frontend && pnpm seed:demo -- --insightd
	cd frontend && pnpm seed:tenancy "$$(cat .demo-company-id)"
	@echo ""
	@echo "  demo company    (A): $$(cat frontend/.demo-company-id)"
	@echo "  tenancy company (B): $$(cat frontend/.tenancy-company-id)"
	@echo ""
	@echo "  pnpm verify:demo    \$$(cat frontend/.demo-company-id)"
	@echo "  pnpm verify:tenancy \$$(cat frontend/.demo-company-id) \$$(cat frontend/.tenancy-company-id)"

# DESTRUCTIVE. Drops and recreates the DATABASE (not just the schema) and migrates
# it from empty. The owner asked to be asked, so it refuses without CONFIRM=WIPE.
#
# WHY DROP THE DATABASE rather than the schema: `schema_migrations` goes with it,
# so migrations replay from 0001 and every per-database GRANT and extension is
# recreated. A `DROP SCHEMA public CASCADE` leaves schema_migrations' own database
# objects in an inconsistent half-state.
#
# WHY NOT `dev-db-down && dev-db`: that destroys the container the rest of this
# Makefile and every runbook `docker exec`s BY NAME, and pays for a 2 GB volume
# delete plus a fresh initdb.
#
# `WITH (FORCE)` terminates cored's, insightd's and statutoryd's pooled connections
# — without it the DROP blocks forever behind an idle pgx pool. `-v ON_ERROR_STOP=1`
# is what stops the half-working failure mode where the DROP errors, the migrate
# then "succeeds" against the untouched old data, and this target reports success.
#
# NO `DROP ROLE`. Roles are CLUSTER-level, so tally_app survives the drop; 0001 and
# 0024 are IF NOT EXISTS-guarded and re-normalise it against the new database.
#
# The migrate must finish SERIALLY before anything else touches the database:
# migrations.Apply takes no advisory lock, so a `go test ./...` racing it on a
# freshly emptied database dies on `type voucher_op already exists`.
reset-dev-db: dev-db ## DESTRUCTIVE. Drop + recreate the dev database, then migrate. Needs CONFIRM=WIPE.
	@if [ "$(CONFIRM)" != "WIPE" ]; then \
		echo "*** reset-dev-db DESTROYS every row in $(ADMIN_DATABASE_URL)."; \
		echo "*** That includes every seeded demo company and every voucher in it."; \
		echo "*** Stop cored / insightd / statutoryd first, then run:"; \
		echo "***     make reset-dev-db CONFIRM=WIPE"; \
		exit 1; \
	fi
	docker compose exec -T db psql -U postgres -d postgres -v ON_ERROR_STOP=1 -c 'DROP DATABASE IF EXISTS tally WITH (FORCE)' -c 'CREATE DATABASE tally'
	$(MAKE) migrate
	@echo ""
	@echo "recreated + migrated. NOW, IN THIS ORDER:"
	@echo "  1) make run-cored      (background)"
	@echo "  2) make run-insightd   (background)"
	@echo "  3) make seed-all"

verify: dev-db migrate backend-build backend-test-residue tier0 frontend-test corpora-verify insightd-test ## Run ALL local gates (Go + frontend + corpora + insightd + Tier 0)
	@echo "\n✅ verification gates passed (see any SKIP lines above for planes not covered)"

.PHONY: help dev-db dev-db-down reset-dev-db migrate backend-build backend-test backend-test-residue tier0 frontend-install frontend-test frontend-build corpora-verify insightd-test run-cored run-insightd run-statutoryd seed-demo seed-all verify
