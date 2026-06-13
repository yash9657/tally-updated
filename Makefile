# Phase-0 developer + CI entrypoints. Each target maps to a verification gate in PLAN.md Part C.
.DEFAULT_GOAL := help

# Connection conventions shared by backend code, migrations, and tests.
# ADMIN role (superuser) is used ONLY for migrations + role creation in dev/CI.
# APP role is NOBYPASSRLS and is what application code + isolation tests connect as.
ADMIN_DATABASE_URL ?= postgres://postgres:postgres@localhost:5433/tally?sslmode=disable
DATABASE_URL       ?= postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable
export ADMIN_DATABASE_URL
export DATABASE_URL

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n",$$1,$$2}'

dev-db: ## Start local Postgres 16 (docker, port 5433)
	docker compose up -d db
	@echo "waiting for postgres..."; \
	for i in $$(seq 1 30); do docker compose exec -T db pg_isready -U postgres -d tally >/dev/null 2>&1 && break; sleep 1; done; \
	echo "postgres ready on :5433"

dev-db-down: ## Stop and remove local Postgres
	docker compose down -v

migrate: ## Apply backend migrations to local db (uses ADMIN then APP roles)
	cd backend && go run ./cmd/cored migrate

backend-build: ## go build + vet
	cd backend && go build ./... && go vet ./...

backend-test: ## Backend unit + RLS/isolation integration tests (needs dev-db)
	cd backend && go test ./... -count=1

frontend-install: ## Install frontend deps
	cd frontend && pnpm install --frozen-lockfile || cd frontend && pnpm install

frontend-test: frontend-install ## Frontend unit tests (XState keystroke machine)
	cd frontend && pnpm test -- --run

frontend-build: frontend-install ## Type-check + build frontend
	cd frontend && pnpm build

verify: dev-db migrate backend-build backend-test frontend-test ## Run ALL Phase-0 gates locally
	@echo "\n✅ Phase-0 verification gates passed"

.PHONY: help dev-db dev-db-down migrate backend-build backend-test frontend-install frontend-test frontend-build verify
