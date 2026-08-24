# Tally-Compatible AI-Native ERP (working name: **Khata**)

An AI-native, cloud-native accounting/ERP for Indian SMBs that **replicates TallyPrime's exact keystroke-driven UX** (zero learning curve for existing Tally users and their CAs) while adding a genuinely agentic AI insight layer Tally structurally lacks.

> Strategy, research, and architecture live in [`RESEARCH.md`](./RESEARCH.md) and [`PLAN.md`](./PLAN.md). **Read those first.** This README covers the repo and local dev only.

## Monorepo layout

| Path | What | Stack |
|---|---|---|
| `backend/` | `cored` — modular monolith (system of record, ledger, atomic voucher post, compliance derive, reporting) | Go 1.26, PostgreSQL 16, pgx, chi |
| `frontend/` | Keystroke-faithful UI engine (the XState navigation machine + Tally screens) | React 18, TypeScript, XState v5, Vite |
| `insightd/` | The AI insight plane — document → purchase-voucher **DRAFT** (OCR → PII redaction → classify/infer → map). **NO ledger write path**, and reached **only** through cored's draft proxy | Python 3.11+, FastAPI, pydantic |
| `backend/cmd/statutoryd/` | The IRN / e-invoice plane — a **separate deployable** in the same Go module, called by cored's proxy and calling back into it to freeze the voucher | Go |
| `corpora/` | **Golden datasets that gate CI** — keystroke conformance, tax derivation, tenant isolation, migration reconciliation | JSON contracts (see `corpora/README.md`) |
| `infra/` | IaC + CI: Terraform (AWS ap-south-1), k8s/Argo, GitHub Actions verification gates | Terraform, Kustomize |
| `docs/` | Engineering notes, ADleavers, runbooks | Markdown |

## The load-bearing invariants (Phase 0)

These are built **before** feature velocity and enforced by CI so that later agent-driven work is safe:

1. **Tenant isolation is structural.** Default-deny Postgres Row-Level Security on every tenant table keyed by `company_id`; the app DB role has no `BYPASSRLS`; the connection wrapper *refuses any query* when the `app.current_company_id` GUC is unset. Verified by an adversarial fuzz suite.
2. **The ledger is immutable & double-entry.** Every Create/Alter/Delete writes a **new** voucher version (never an in-place update), BLAKE3 hash-chained per node — this single construct is the MCA Rule 11(g) Edit Log, the event log, and the sync conflict unit. Money is `NUMERIC(19,4)` / integer paise, never float. `Σdebit = Σcredit` enforced by a deferred constraint trigger.
3. **Keystroke fidelity is a contract.** The navigation grammar (Enter=drill/next, Esc=back, Ctrl+A=accept, F-keys=voucher type, Alt+G=Go To) is a deterministic XState machine driven by a data-driven keymap, regression-tested against captured TallyPrime behavior.

## Local development

Prereqs: Go 1.26+, Node 22+ / pnpm, Docker.

```bash
make dev-db        # start local Postgres 16 (docker, port 5433)
make migrate       # apply backend/migrations to local db  (⛔ serving does NOT migrate)
make backend-test  # go build + vet + RLS/isolation integration tests
make frontend-test # pnpm install + vitest (keystroke machine)
make verify        # run ALL Phase-0 CI gates locally
```

To run the stack: `make run-cored` (:8087) · `make run-insightd` (:8088) ·
`make run-statutoryd` (:8091) · `cd frontend && pnpm dev` (:5173).

> ⛔ **Use the make targets rather than launching the services by hand.** Two
> credentials are shared between processes and neither has a safe default in code:
> `statutoryd` holds a cored-minted service token that **dies with every cored
> restart**, and `insightd` shares `INSIGHTD_SERVICE_TOKEN` with cored's draft
> proxy. The Makefile mints the first and exports the second; start either service
> without them and you get an explicit refusal naming the missing variable, which is
> the intended failure — never a quiet open door.

See the `Makefile` for the full target list.

## How the three services relate

```
browser ──/v1, /auth──> cored ──proxy──> insightd    (document -> DRAFT; never writes the ledger)
                          └────proxy──> statutoryd   (IRN / e-invoice; calls back in to freeze)
```

**cored is the only door.** Nothing in the browser addresses insightd or
statutoryd: every request is authenticated, membership-checked and policy-judged by
cored first, and forwarded with the company its PDP authorized. The side planes
refuse anything that did not arrive that way. See
[`docs/TENANCY-AND-ACCESS.md`](./docs/TENANCY-AND-ACCESS.md) §1 for the three
layers and §5b for why the AI plane sitting *outside* them was a real tenancy hole.
