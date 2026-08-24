# Implementation Plan — AI-Native, Keystroke-Compatible Tally Competitor

**Companion to:** [`RESEARCH.md`](./RESEARCH.md) — the Tally research dossier. Read that first; every decision here is grounded in it.

**Compiled:** 13 June 2026 · **Method:** 12-agent dynamic planning workflow — 10 principal-level domain architects each produced an Architecture Decision Record (ADR) grounded in the research, a principal-integrator reconciled them into one coherent system, and a delivery lead produced the roadmap. **The build itself is executed primarily by dynamic agent workflows**, specified per phase in Part C.

> **Design tenets** (non-negotiable): *elegant, secure, stable, robust, scalable* — and fast to market. Replicate Tally's exact keystroke grammar for zero-retraining adoption; guarantee zero-data-loss migration so the Chartered Accountant (the real decision influencer) can't veto the switch; be cloud-native and offline-first underneath; layer agentic AI that Tally structurally lacks.

---

# Part A — Target System Architecture

*Synthesized by the principal-integrator from the 10 domain ADRs in Part B; conflicts were explicitly reconciled (see end of this part).*

## A.1 Architecture Overview

## Target architecture: a Go modular monolith over PostgreSQL 16, with an immutable voucher-version log as the single reconciliation primitive

The ten domain ADRs largely agree on the foundations (PostgreSQL 16 system of record, RLS pool tenancy, India/ap-south-1 residency, append-only hash-chained audit, node-partitioned UUIDv7, human-in-the-loop AI, keystroke-faithful DOM UI) but diverge sharply on three axes that I resolve here as Principal Engineer:

**1. Modular monolith vs microservices — DECISION: one Go modular monolith (`cored`) at MVP; Compliance and AI are the only separately-deployable planes.** The Backend ADR's seven-module monolith is correct for speed-to-market and for the atomic voucher post (Ledger + Inventory + Compliance tax-derive must commit in ONE Postgres transaction so Ctrl+A mirrors Tally exactly). The Compliance ADR's instinct to make the Statutory Core a separate service is over-engineered for the *write-path tax derivation* (that must be an in-process, sub-millisecond pure function called inside the voucher transaction) but *correct* for the *gateway orchestration* (IRN/e-way-bill/GSTR filing, GSP 6-hour sessions, IMS timers) which is long-running, external-facing, and failure-prone. Resolution: the **TaxDeriveEngine is an embedded in-process Go library** (the RuleSet interpreter linked into `cored`); the **Statutory Gateway is a separate deployable** (`statutoryd`) driven by durable workflows. The AI insight plane (`insightd`, Python/FastAPI) is also separate because it has a different language ecosystem, scaling profile, and a read-only data relationship. Everything else stays one binary with strictly enforced internal module boundaries (Identity, Ledger, Inventory, Compliance-derive, Integration-Gateway, Sync, Reporting), import-linted in CI so extraction stays mechanical.

**2. Event-sourcing vs CRUD-with-audit vs versioning — DECISION: application-level immutable voucher VERSIONING is the one model; it IS the event log, the Edit Log, AND the sync conflict primitive.** This is the central reconciliation. The Sync ADR wanted full event-sourcing with a WASM domain core replayed identically on Go-cloud and SQLite-clients; the Data ADR wanted Postgres voucher versioning with incrementally-maintained balances; the Backend ADR wanted CRUD + transactional outbox. I unify these: **a voucher is an immutable, whole-document fact identified by a node-partitioned UUIDv7; every Create/Alter/Delete writes a NEW version row (never an in-place update) carrying a BLAKE3 hash of the prior version on its node-chain.** This single construct satisfies all three needs simultaneously — it is the MCA Rule 11(g) Edit Log by construction (Data + Compliance + Security ADRs), it is the document-grained event the Sync ADR transports and orders (the whole voucher is the conflict unit, eliminating field-merge), and it feeds the transactional outbox the Backend ADR uses. I **reject full CQRS/Kafka event-sourcing as the system of record** (the Sync ADR's heaviest interpretation) because it triples the moving parts for MVP; instead the version table IS the durable log and Postgres logical replication is the bus. I also **reject the WASM-domain-core-replayed-everywhere requirement as a v1 must-have** because dual-determinism across Go and WASM is the Sync ADR's own top risk; v1 ships the canonical engine only in Go and treats offline clients as *thin/full replicas of projections* (see #3).

**3. Offline-first event log vs single cloud ledger — DECISION: single authoritative cloud ledger is the source of truth; offline desktop/mobile hold a local read-model + a write-outbox of intended voucher-version events, reconciled by a cloud sequencer.** The Frontend, Infra, and Backend ADRs converge on "single live cloud ledger + local read model (Dexie/IndexedDB or SQLite) + outbox," while the Sync ADR pushes a symmetric per-node event log with client-side WASM validation. The symmetric model is the more elegant long-term design but its risks (Go↔WASM divergence, sequencer HA as availability bottleneck, partial mobile replication correctness) are exactly the kind of novelty I avoid for MVP. **Resolution — a graded offline model:** (a) web is a thin replica (live, no local store) using TanStack Query; (b) desktop (Tauri) and mobile (Capacitor) keep a full local read-model in SQLite/IndexedDB and an append-only outbox of voucher-version intents tagged with a Hybrid Logical Clock and node-partitioned UUIDv7; (c) on reconnect the outbox replays through the *same authenticated voucher-post API a human keystroke uses* — the cloud is the authoritative sequencer that assigns the per-(tenant,type,series,FY) display number at commit and runs canonical validation/tax-derive. Offline-created vouchers carry a provisional display number (`INV/2026-27/PROV-<node>-<seq>`) swapped atomically at commit. Two offline edits to the same voucher do not field-merge: deterministic winner by (commit-order, HLC, node-id) becomes current; the loser is preserved as a superseded version AND raised into a surfaced reconciliation queue. This gives the Sync ADR's conflict semantics and zero-collision numbering without shipping a second canonical engine. The Sync ADR's WASM-everywhere convergence is the documented v2 path for true peer offline, gated behind golden-vector determinism tests.

### Request lifecycle (authoritative voucher post — the load-bearing path)

1. **Keystroke → intent.** The XState VoucherMachine (Frontend ADR) renders Tally's exact grammar (F8 Sales, Alt+C inline master, Ctrl+A accept). On Ctrl+A it produces a typed voucher command bound to the canonical schema (28 groups, bill-wise, cost-centre, GST/TDS fields).
2. **Transport.** POST to the versioned REST/JSON API (OpenAPI 3.1) with a mandatory `Idempotency-Key` + request-hash. Online clients write through directly; offline clients write to the local outbox first and replay later. The legacy ecosystem hits the same logic via the `POST /tally` ENVELOPE/JSONEX ingress in the Integration-Gateway module.
3. **AuthN/Z.** TLS 1.3 to edge (Cloudflare/AWS WAF) → Keycloak-issued short-lived JWT with company-scoped claims → middleware sets `app.current_company_id` GUC (so RLS default-deny applies) → PDP evaluates the replicated Tally Disallow/Allow level + back-dated cutoff.
4. **Atomic post (ONE Postgres transaction).** Ledger module opens the txn → calls the embedded TaxDeriveEngine (pure function of voucher snapshot + pinned RuleSet version) → Inventory participates (stock movement rows) → writes the immutable voucher-version + posting rows + bill/cost allocations → updates the incrementally-maintained `ledger_balance` rows → appends the hash-chained audit row (user/IP/device/session) → writes a domain event to the transactional outbox. Double-entry (Σdebit=Σcredit) enforced by a deferred constraint trigger; money is NUMERIC(19,4)/integer-paise, never float.
5. **Commit & display number.** The per-(tenant,type,series,FY) monotonic allocator (Postgres sequence-of-record, Redis-fronted hot counter) assigns the human voucher number at commit; identity stays the UUIDv7.
6. **Async fan-out (after commit).** Postgres logical-replication CDC drains the outbox → publishes domain events. `statutoryd` consumes `voucher.created/altered` to drive IRN/e-way-bill/GSTR via durable workflows behind GSP circuit breakers. `insightd` consumes the same stream (and reads a replica) for anomaly/recon/forecast. First-party webhooks fan out exactly-once via the outbox de-dupe.
7. **Read/projection.** Reports (Balance Sheet → group → ledger → voucher drill-down) aggregate `ledger_balance` up the ltree group hierarchy — sub-second at millions of vouchers, killing Tally's load-into-RAM pain. Heavy multi-year OLAP is offloaded to ClickHouse fed from the same CDC stream.

The result: Tally's exact Ctrl+A feel (synchronous atomic post), the MCA Edit Log for free (versioning), zero-collision branch numbering (UUIDv7 + cloud allocator), and a clean async seam for compliance + AI — built as one mostly-monolithic Go binary for speed to market, with the two genuinely-different planes (statutory gateway, AI) split out where their failure and scaling profiles demand it.

## A.2 System Topology

```
                          INDIA / AWS ap-south-1 (Mumbai) — primary | ap-south-2 (Hyderabad) — DR
 ┌──────────────────────────────────────────────────────────────────────────────────────────────┐
 │                                                                                                │
 │  CLIENTS (one TS/React+XState UX engine, three shells)                                         │
 │  ┌──────────────┐   ┌──────────────────────┐   ┌──────────────────────┐                        │
 │  │ Web PWA      │   │ Desktop (Tauri 2,    │   │ Mobile (Capacitor 6,  │                        │
 │  │ thin replica │   │ Rust shell)          │   │ iOS/Android)          │                        │
 │  │ TanStack Qry │   │ SQLite read-model +  │   │ SQLite read-model +   │                        │
 │  │              │   │ write-outbox(HLC,    │   │ write-outbox          │                        │
 │  │              │   │ UUIDv7)              │   │ (scoped FY/branch)    │                        │
 │  └──────┬───────┘   └──────────┬───────────┘   └──────────┬───────────┘                        │
 │         │ TLS 1.3              │                          │                                    │
 │         └──────────────┬───────┴──────────────────────────┘                                    │
 │                        ▼                                                                        │
 │             ┌─────────────────────┐   ┌──────────────────────────────────────────┐             │
 │             │ Cloudflare/AWS WAF  │   │ Migration Agent (.NET8, on user's Windows │             │
 │             │ rate limit, quotas  │   │ box) → mTLS → /tally ingress; never       │             │
 │             └─────────┬───────────┘   │ exposes Tally port 9000 beyond localhost  │             │
 │                       ▼               └───────────────────┬──────────────────────┘             │
 │     ┌─────────────────────────────────────────────────────▼───────────────────────┐           │
 │     │  cored  — Go 1.23 MODULAR MONOLITH (chi, sqlc/pgx) on EKS (Graviton)          │           │
 │     │  ┌─────────┬─────────┬───────────┬───────────────┬────────┬─────────────────┐ │           │
 │     │  │Identity │ Ledger  │ Inventory │ Compliance-   │ Sync   │ Integration-GW  │ │           │
 │     │  │(Keycloak│(version │(stock     │ derive        │(outbox │ REST(OAS3.1)/   │ │           │
 │     │  │ +RLS+   │ +audit  │ movements)│ EMBEDDED      │ replay,│ POST /tally     │ │           │
 │     │  │ PDP/OPA)│ +balance│           │ TaxDeriveEng  │ conflict│ ENVELOPE/JSONEX │ │           │
 │     │  └─────────┴────┬────┴───────────┴──────┬────────┴───┬────┴─────────────────┘ │           │
 │     │   ONE Postgres txn: version+postings+bill/cost+balance+audit+outbox            │           │
 │     │   River (Postgres-backed jobs) for in-core async                               │           │
 │     └───────────────┬──────────────────────────┬────────────────────┬───────────────┘           │
 │                     ▼                          │ logical-repl CDC    │                           │
 │     ┌──────────────────────────────┐          │ (transactional      │                           │
 │     │ Aurora PostgreSQL 16          │◄─────────┘ outbox = event bus) │                           │
 │     │ SYSTEM OF RECORD + Edit Log   │                               │                           │
 │     │ RLS pool tenancy; partition   │──repl──► read replica ──► ClickHouse (OLAP reports)        │
 │     │ by company_id; voucher VERSION│                               │         ▲                 │
 │     │ table = immutable hash-chain  │   Redis 7 (numbering counter, │         │                 │
 │     └──────────────┬───────────────┘   cache, locks, GSP session)  │         │                 │
 │                    │ daily Merkle root                              ▼         │                 │
 │                    ▼                                  ┌────────────────────┐  │                 │
 │     ┌──────────────────────────────┐                 │ insightd (Python/   │  │                 │
 │     │ S3 Object Lock (WORM,         │                 │ FastAPI) AI plane:  │──┘                 │
 │     │ compliance) 8-yr audit archive│                 │ Claude Opus/Haiku   │  reads replica/    │
 │     │ + raw Tally staging artifacts │                 │ via Bedrock; on-prem│  ClickHouse only;  │
 │     └──────────────────────────────┘                 │ OCR+PII redaction;  │  writes ONLY via   │
 │                    ▲                                  │ NL→typed DSL→RO qry │  human-approved    │
 │                    │ co-sign chain head               │ deterministic engs  │  voucher API       │
 │     ┌──────────────┴───────────────┐                 └─────────┬───────────┘                    │
 │     │ statutoryd (Go) STATUTORY     │◄── consumes voucher.* ────┘                                │
 │     │ GATEWAY: Temporal workflows   │    events (CDC)                                            │
 │     │ IRN/e-way/GSTR/IMS; signed    │──► GSP/ASP (ClearTax, Masters India), NIC IRP             │
 │     │ RuleSet bundles (KMS/Ed25519) │    behind circuit breakers + idempotent outbox            │
 │     │ pushed to cored TaxDeriveEng  │                                                            │
 │     └──────────────────────────────┘                                                            │
 │  AWS KMS (HSM, per-tenant data keys) · Vault (secrets) · OTel→Grafana/Tempo/Loki · Sentry        │
 └──────────────────────────────────────────────────────────────────────────────────────────────┘
```

## A.3 Canonical Tech Stack

| Layer | Chosen technology | Why (grounded in ADRs + Tally reality) |
|---|---|---|
| UX engine | TypeScript (strict) + React 18 DOM (not canvas) + XState v5 + TanStack Virtual | Tally's UX *is* a hierarchical state machine; XState models the screen stack/keymap deterministically. DOM+virtualization gives a11y and large-report perf without a custom cell renderer. One engine, two themes (Legacy/Modern) via data-driven keymaps. |
| Client shells | Web PWA (Vite + Workbox); Tauri 2 (desktop, canonical for full F-key/Alt capture); Capacitor 6 (mobile) | One web bundle, three shells. Desktop/mobile are the high-fidelity offline targets; browser PWA is the documented lower-fidelity target due to reserved-key limits. |
| Local offline store | SQLite (desktop/mobile via better-sqlite3 / op-sqlite); Dexie/IndexedDB acceptable for web app-shell | Full local read-model + write-outbox so the keyboard entry loop never blocks on network. Single SQLite choice over Dexie for desktop/mobile to reduce surface area. |
| Backend runtime | Go 1.23 single binary (`cored`), chi v5 + net/http, sqlc + pgx v5 | Boring, proven, fast, statically linked. Modular monolith for MVP speed; module boundaries import-linted for mechanical later extraction. |
| Public API | OpenAPI 3.1 REST/JSON (oapi-codegen) primary; `POST /tally` ENVELOPE + TallyPrime 7.0 JSONEX ingress; gRPC internal-only; no public GraphQL at MVP | REST is the durable contract for all shells + SDKs. The /tally ingress makes Suvit/Webtel/tally-database-loader work unchanged over authenticated HTTPS — a day-one ecosystem moat. |
| System of record | Amazon Aurora PostgreSQL 16, RLS pool tenancy, partition by company_id, NUMERIC(19,4)/integer-paise, ltree hierarchies, deferred Σdebit=Σcredit triggers | ACID kills Tally's .900 corruption class. One store is the system of record, Edit Log, and (via outbox) event bus — fewest moving parts. Voucher VERSION table is the immutable log. |
| Ledger model | Application-level immutable voucher VERSIONING + incrementally-maintained `ledger_balance` roll-up | MCA Edit Log by construction; sub-second Trial Balance/BS/P&L at millions of vouchers; whole-voucher version is the sync conflict unit. Reconciles event-sourcing vs CRUD vs versioning into ONE primitive. |
| ID + numbering | Node-partitioned UUIDv7 identity + server-side per-(tenant,type,series,FY) monotonic display-number allocator (Postgres sequence-of-record, Redis hot counter) | Eliminates Tally's GUID/voucher-number collision hard-fail across branches; human number is a display layer over the UUID. |
| Tax compute | Embedded Go TaxDeriveEngine interpreting CEL/JSON-Logic, signed effective-dated RuleSet bundles (Ed25519/KMS) | Synchronous sub-ms tax at voucher save (in the atomic txn); rules are data hot-pushed from cloud → no TSS upgrade tax. Pure/replayable for bit-for-bit audit re-derivation. |
| Statutory gateway | Separate `statutoryd` (Go) + Temporal (self-hosted on EKS) + ProviderAdapter (ClearTax/Masters India primary, NIC IRP secondary) | Long-running, external, failure-prone IRN/e-way/GSTR/IMS orchestration with 6-hr GSP sessions and deemed-accept timers belongs in durable workflows, isolated from the request path. |
| In-core async jobs | River (Postgres-backed) for OCR handoff, recon triggers, sync merge, exports | Avoids a second datastore for MVP; durable jobs co-located with the DB. Temporal reserved for the genuinely multi-day statutory sagas. |
| Event bus | Transactional outbox + Postgres logical-replication CDC at MVP; MSK/Kafka introduced only for `statutoryd`/`insightd` fan-out + ClickHouse feed at scale | Outbox+CDC is exactly-once-capable and cheap for MVP; Kafka added at the seam where two external planes and OLAP consume the stream — not as day-one infra. |
| OLAP/reports accel | ClickHouse (ap-south-1) fed from CDC | Replaces Tally's in-RAM Aggr.1800; keeps multi-year Day Book/Stock Summary hot. OLTP reports still served from `ledger_balance`. |
| AI plane | `insightd` Python/FastAPI; Claude Opus 4.8 (reasoning/agent) + Haiku 4.5 (classification) via Bedrock ap-south-1 (Anthropic API fallback); self-hosted PaddleOCR + Presidio (PAN/GSTIN/Aadhaar) on-prem; pgvector; XGBoost/statsmodels deterministic engines | AI sits beside the ledger, never in the keystroke loop. NL→typed DSL→read-only replica (never raw SQL). Raw docs/PII never leave India; only redacted/structured payloads cross border. Writes only via human-approved voucher API. |
| Identity & access | Keycloak 26 (OIDC/OAuth2/SAML/SCIM, TOTP/WebAuthn) self-hosted in-region; Postgres ACL tables replicating Tally Disallow/Allow levels; OPA/Rego for cross-cutting policy | Migrating CAs keep the exact Owner/Data-Entry/custom-level mental model; adds the MFA/SSO/lockout Tally lacks. Mandatory WebAuthn for Owner/Admin. |
| At-rest crypto | AWS KMS (HSM) envelope encryption, per-tenant AES-256-GCM data keys, opt-in audited key-escrow recovery; Argon2id KDF | Replaces TallyVault's Modified-DES and its "forget password = total loss"; crypto-shredding (delete tenant key) for DPDP offboarding. |
| Extensibility | FlowDef declarative DSL (JSON-Schema 2020-12 → versioned IR) for ~80%; V8 isolate (isolated-vm online / QuickJS-ng offline) + Wasmtime WASI-p2 server modules for ~20%; semantic anchors; Claude-authored copilot; OCI registry + Sigstore-signed bundles | Stable semantic API replaces TDL's brittle name-based overrides so add-ons survive upgrades; capability manifests enforce least privilege the in-process TDL never had. |
| Migration | .NET 8 signed Windows Migration Agent driving Tally XML/JSONEX over port 9000 → immutable Postgres staging (verbatim + SHA-256) → idempotent TS importer keyed on Tally GUID+MASTERID/VOUCHERID → reconciliation harness (TB/BS/P&L/GSTR to the rupee). Rust .900/.1800 parser deferred. | Live gateway is the dependable v1 path; rupee-exact reconciliation blocks go-live; round-trip Export-to-Tally keeps "the CA can still read it" true. |
| Infra/CICD | AWS EKS (K8s 1.31+, Karpenter, Graviton, Bottlerocket); Terraform + Helm + Argo CD/Rollouts; GitHub Actions; Trivy/Semgrep/gitleaks/Falco; OTel→Grafana/Tempo/Loki/Mimir + Sentry | Long-running warm services (not serverless) for low-latency keystroke handling + Postgres pool stability; serverless only for spiky OCR/GST-poll jobs. DR ap-south-2, RPO≤5min/RTO≤1hr. |

## A.4 Cross-Cutting Concerns

## Security
- **Tenancy isolation (highest-severity risk).** Pool model: shared schema, mandatory `company_id` on every business row, Postgres RLS default-deny. `cored` NEVER connects as a bypassrls/superuser role and ALWAYS sets `app.current_company_id` GUC before any query. Enforced by: a sqlc/pgx connection wrapper that refuses queries without the GUC set, CI architecture tests asserting an RLS policy exists on every table, and per-release tenant-isolation fuzzing + pen-testing. Largest/regulated tenants can be promoted to a dedicated schema/cluster later.
- **AuthN/Z.** TLS 1.3 to clients, mTLS between `cored`/`statutoryd`/`insightd` (Linkerd).
  ⚠️ **STATUS 2026-08-24: the plane-to-plane half is shipped at the APPLICATION layer, not the transport
  layer.** `cored` is the only door to both side planes — it proxies them after its own authn + PDP have
  decided, and each refuses a request that does not present its shared service credential (statutoryd a
  cored-minted `svc:` bearer, insightd `INSIGHTD_SERVICE_TOKEN` plus the company cored authorized). That is
  a real control and it is what closed register row D49, but it is **not** mTLS: it authenticates the
  CALLER, not the connection, so it does not encrypt plane-to-plane traffic and does not survive an
  attacker on the pod network who has read the secret. Linkerd remains the plan; the application credential
  is what makes the current deployment honest in the meantime. Keycloak issues short-lived company-scoped JWTs. PDP evaluates Tally Disallow/Allow levels (Postgres ACLs) + back-dated cutoff server-side on every write/read; OPA/Rego for cross-cutting policy. Mandatory MFA/WebAuthn for Owner/Admin (account-takeover at Admin = catastrophic, controls escrow). Extensions and AI run with narrow OAuth2 scopes bound to one company_id — no god-mode, no RLS bypass.
- **At-rest & secrets.** KMS-wrapped per-tenant AES-256-GCM; Vault dynamic short-TTL DB creds; no secrets in env/images. Migration Agent terminates mTLS so Tally's plaintext port 9000 never leaves localhost; the legacy Modified-DES bridge (two-way sync window) is network-isolated and treated as untrusted input.

## Observability
- OpenTelemetry traces/metrics/logs everywhere → Prometheus/Mimir, Tempo, Loki, Grafana; Sentry for client errors. Golden-signal SLOs with error budgets. Dedicated SLO dashboards on government-API latency, IRN/filing rejection rates, GSP token-refresh health (the externalities most likely to cascade). Voucher-post p99 latency is the canonical product SLO (keystroke feel).

## Multi-tenancy & data residency (India / DPDP)
- All financial data, audit logs, backups, KMS keys pinned to ap-south-1 (DR ap-south-2). The ONLY cross-border call is Claude via Bedrock ap-south-1 (Anthropic API fallback), carrying redacted/structured payloads only; on-prem OCR + Presidio redaction (PAN/GSTIN/Aadhaar/IFSC) runs before any text leaves the residency boundary. Design for stricter localization than currently mandated since DPDP rules are still being notified (2026). Crypto-shredding (per-tenant key deletion) for tenant offboarding, reconciled against the 8-year Section 128(5) retention obligation.
- **MCA Rule 11(g) Edit Log** is always-on and structurally undisableable (no toggle in schema or API): per-tenant hash-chained voucher-version + master-mutation ledger capturing user/UTC+IST time/before-after/device/IP/session/geo; daily Merkle root signed by KMS → S3 Object Lock WORM (8-yr). Every cross-node supersede/overwrite is itself a logged Altered event. One-click auditor-attestation export.

## Testing strategy (the safety net that makes agentic build velocity safe)
- **Property-based ledger invariants** (Σdebit=Σcredit, no orphan postings, balance roll-up == recompute-from-postings) — co-owned CI gate.
- **Golden keystroke-conformance suite** (Playwright) asserting F-key/Alt+G/Ctrl+A/Ctrl+H grammar matches the TallyPrime spec exactly, with per-state keymap-scoping tests for the known collisions (Ctrl+H Change Mode vs Change View, Alt+I invoice-toggle vs insert).
- **Compliance conformance suite**: NIC e-Invoice (12 sections/133 fields, forbidden chars, decimal precision, 1000-line cap), EWB/GSTR schema validation; RuleSet golden-transaction regression corpus with staged canary before any auto-push (a bad signed bundle is a fleet-wide miscompute risk).
- **Migration fidelity**: rupee-exact TB/BS/P&L/outstandings/stock(by valuation method)/GSTR-1/3B reconciliation against Tally exports; go-live blocked until green; round-trip Export-to-Tally validated against a headless Tally in CI.
- **Determinism contract**: TaxDeriveEngine uses fixed-point decimal + canonical serialization; golden-vector tests guard replay reproducibility across refactors (and gate any future Go↔WASM client engine).
- **Mutation testing + mandatory human intent review** on agent-generated PRs; money-math/compliance/auth/migration paths require a second human approver and cannot be agent-self-approved.

## Performance budgets
- Voucher post (Ctrl+A) p99 ≤ 150 ms server-side in-region; tax-derive ≤ 5 ms (in-txn pure function). Trial Balance / BS / P&L ≤ 1 s at 10M+ vouchers via `ledger_balance` roll-up (never full posting scan). Large multi-year reports virtualized client-side + ClickHouse server-side. Hot-ledger (Cash/Bank) contention mitigated by per-period balance rows / delta accumulation; mandatory async recompute-from-postings reconciliation job guards balance drift. Interactive AI (Alt+G NL query) kept snappy via prompt caching of the per-tenant schema prefix + Haiku routing + async/streaming UX; nightly anomaly/forecast jobs on the Batch API.

## A.5 Conflicts Reconciled Across Domains

- Event-sourcing (Sync ADR) vs append-only versioning (Data ADR) vs CRUD+outbox (Backend ADR): UNIFIED into application-level immutable voucher VERSIONING on Postgres. One construct serves as the durable log, the MCA Edit Log, and the whole-document sync conflict unit. Postgres logical-replication CDC over a transactional outbox is the event bus. Full CQRS/Kafka event-sourcing as system-of-record is rejected for MVP (too many moving parts); the version table IS the log.
- Symmetric offline event log with WASM domain core replayed on every node (Sync ADR) vs single cloud ledger + local read-model/outbox (Frontend/Infra/Backend ADRs): RESOLVED to single authoritative cloud ledger as source of truth. Web = thin replica; desktop/mobile = full local read-model + write-outbox of voucher-version intents (HLC + UUIDv7) replayed through the same authenticated voucher-post API; cloud is the sequencer assigning display numbers. The Sync ADR's WASM-everywhere convergence and its sequencer-as-single-writer HA risk are deferred to v2, gated behind golden-vector determinism tests — avoiding the dual-determinism risk the Sync ADR itself flagged as top risk.
- Compliance as a separate microservice with Temporal+Kafka (Compliance ADR) vs Compliance as an in-process monolith module with River (Backend ADR): SPLIT by latency profile. The TaxDeriveEngine is an EMBEDDED in-process Go library called inside the atomic voucher transaction (sub-ms, must be synchronous for Ctrl+A fidelity). The Statutory Gateway (IRN/e-way/GSTR/IMS, 6-hr GSP sessions, deemed-accept timers) is a SEPARATE deployable (statutoryd) driven by Temporal — long-running, external-facing, failure-prone work that must not sit in the request path.
- Modular monolith (Backend/Data/Infra ADRs) vs implied service-per-domain (Compliance/Sync/AI ADRs): RESOLVED to one Go modular-monolith binary (cored) for MVP, with ONLY two planes split out — statutoryd (different failure/scaling profile) and insightd (different language ecosystem + read-only data relationship). Internal module boundaries are import-linted so future extraction is mechanical.
- Job/workflow engine proliferation — River (Backend), Temporal (Compliance/Infra), NATS JetStream (Sync/Extensibility), Kafka (Compliance/Infra): CONSOLIDATED for MVP to River (in-core durable jobs) + Temporal (only the multi-day statutory sagas) + transactional-outbox/CDC (event bus). NATS JetStream and Kafka/MSK are deferred until the statutoryd/insightd fan-out and ClickHouse feed prove the need — not day-one infra.
- Tax-engine language split — Go Statutory Core (Compliance ADR) vs TypeScript/WASM domain core for client-side compute (Sync ADR): RESOLVED to Go as the single canonical tax/posting engine for v1. Clients do not run canonical tax math offline at v1; they collect intent and the cloud computes authoritatively on commit. This removes the Go↔WASM decimal/rounding divergence risk for MVP; a shared WASM core is the documented v2 path for true peer offline.
- Master conflict handling: vouchers use whole-document version supersede (no field merge), but masters (ledgers/stock items/GST settings) use scoped CRDTs (LWW-register per descriptive field, add-only sets for child collections) per the Sync ADR — kept, because master merges are safe and desirable whereas accounting-document merges are not.
- OLAP placement — ClickHouse (Infra ADR) vs Postgres read replicas + DuckDB mart (AI ADR) vs incremental ledger_balance (Data ADR): LAYERED. Real-time OLTP reports (TB/BS/P&L/drill-down) served from incrementally-maintained ledger_balance in Aurora. Heavy multi-year/cross-tenant OLAP and AI analytics served from ClickHouse fed by CDC; AI reads ClickHouse + a read replica. No DuckDB at MVP (avoid a third query engine).
- Audit-ledger ownership: the Compliance ADR claims the audit ledger but the Data/Security/Sync ADRs all write to it. RESOLVED: the audit hash-chain is a shared first-class primitive owned by the Ledger module's write path (appended in the same txn as the voucher version); all domains feed it via an append-only API and none can mutate it. Compliance consumes it; it does not gate it.

## A.6 Top Program Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Cross-tenant data leakage via a single missing RLS predicate or a code path that connects without setting app.current_company_id — the single highest-severity failure (a financial-data breach across SMBs). | Default-deny RLS on every table verified by CI architecture tests; a mandatory pgx/sqlc connection wrapper that refuses any query when the company_id GUC is unset; no bypassrls/superuser role available to app code; per-release tenant-isolation fuzzing + external pen-testing; dedicated-cluster promotion path for regulated tenants. |
| RuleSet correctness becomes a single point of systemic failure — one flawed signed tax bundle miscomputes GST/TDS fleet-wide, eroding the CA trust that is the entire go-to-market. | RuleSets are signed data interpreted (never eval'd as code); a golden-transaction regression corpus must pass before signing; staged canary rollout (small tenant cohort) with automated rejection-rate monitoring before any fleet auto-push; period-correct re-derivation kept pure/replayable so any bad bundle's blast radius is auditable and reversible. |
| Offline reconciliation + provisional numbering surprises: a branch offline for days commits vouchers whose authoritative numbers shift on reconnect, potentially clashing with a compliance freeze if a number was already externally referenced (e-invoiced/filed). | Identity (UUIDv7) decoupled from display number; freeze-on-external-reference rule pins a number at IRN-generation time via a hook statutoryd calls; the reconciliation queue HARD-BLOCKS superseding any voucher already in a filed GSTR-1 or e-invoiced, routing it to CA notification; strong UI signaling of provisional vs final state. |
| Keystroke-conformance and compliance golden suites are high-maintenance as TallyPrime (now 7.0) and GST schemas evolve 2-4x/year; silent under-maintenance erodes the gate that makes agentic build velocity safe and the CA-trust moat. | Dedicated regulatory-watch + Tally-version-watch function owning the golden corpora; a CI matrix of real Tally versions for round-trip migration; visual-regression gates on keystroke fidelity; treating schema/keymap drift as a planned, staffed maintenance tax (an accepted moat cost), not incidental work. |
| Modular-monolith boundary erosion under deadline pressure turns cored into a big ball of mud, destroying the mechanical-extraction property and the atomic-post invariants. | CI import-linting + architecture tests enforcing the seven module boundaries; in-process domain interfaces (not direct cross-module table access); CODEOWNERS per bounded context with a senior owner as required approver; the atomic voucher-post transaction owned solely by the Ledger module so Inventory/Compliance participate only through its interface. |
| Postgres/Aurora as OLTP + Edit-Log + outbox + job queue + numbering is a single scaling chokepoint and a noisy-neighbor hazard for the largest tenants before the dedicated-cluster path is exercised in production. | Partition hot tables by company_id; BRIN indexes for date-range scans; read replicas + ClickHouse offload reporting load; Redis-fronted numbering counter to cut hot-ledger contention; rehearse the largest-tenant dedicated-cluster promotion before it's needed; introduce MSK/Kafka at the fan-out seam only when CDC throughput proves the need. |
| GSP/NIC/GSTN external dependency: government-side outages on filing-deadline days, per-GSP rate limits, and 6-hour token expiry can cascade into customer filing failures outside our control. | statutoryd isolates all government calls in Temporal workflows behind circuit breakers + idempotent durable outbox; multi-homed ProviderAdapter (ClearTax + Masters India primary, NIC IRP secondary) for failover; background GSP-session auto-renew; resumable ARN-state tracking with clear user-facing retry rather than request-path blocking. |

---

# Part B — Architecture Decision Records (by domain)

Each domain was designed by a dedicated principal-level architect agent. ADRs state the recommendation, the concrete stack, the load-bearing decisions (with alternatives rejected and risks), and the detailed engineering writeup.

## B.1 Frontend & UX Replication Architecture

**Recommendation.** Render Tally's keyboard-first, single-window, character-grid UX as a DOM-based application (NOT a canvas grid) built on React 18 + TypeScript with TanStack Virtual for the large report grids. Wrap it in a single PWA-first web codebase that is packaged for desktop via Tauri (Rust shell, system WebView) and for mobile via Capacitor — one UX engine, three shells. The load-bearing core is a framework-agnostic Keystroke State Machine implemented with XState v5: a deterministic, declarative model of Tally's screen stack (Enter=drill/next-field, Esc=back, Ctrl+A=accept, F-key voucher switching, Alt+G Go To overlay vs Ctrl+G Switch To replace, Alt+C inline master creation, F2/F12/Ctrl+H report grammar). The same engine drives both a "Legacy-Faithful" theme (TallyPrime blue / ERP 9 green, fixed right button-bar, monospace grid) and a "Modern" theme by swapping a presentation layer over an identical interaction model and keymap registry. Keymaps are data (TallyPrime vs ERP 9 profiles) so muscle memory transfers for migrants from either product. Offline rendering is guaranteed by a local read model in IndexedDB (via a TypeScript domain replica) so the keyboard entry loop never blocks on the network. This is boring, proven technology applied with discipline; the only opinionated bet is XState as the interaction substrate, which is justified because Tally's UX *is* literally a hierarchical state machine and modeling it any other way invites the navigation bugs that would get a CA to veto the switch.

**Chosen stack:** `TypeScript (strict) as the single language across all shells`, `React 18 + DOM rendering (not canvas) for the character grid and forms`, `XState v5 — the Keystroke/Navigation State Machine (screen stack, field cursor, voucher mode, drill-down)`, `Vite as build tool; PWA via Workbox/vite-plugin-pwa (installable, offline app shell)`, `Tauri 2.0 (Rust shell + OS WebView) for Windows/macOS/Linux desktop`, `Capacitor 6 for iOS/Android native shells reusing the same web bundle`, `TanStack Virtual for windowed/virtualized report and selection-list rendering`, `Tailwind CSS + CSS custom-property design tokens for legacy/modern theming`, `Zustand for ephemeral UI/view state outside the XState machines`, `Dexie.js over IndexedDB for the offline local read model and outbox`, `Radix UI primitives (headless) for accessible overlays/menus, restyled to Tally chrome`, `TanStack Query for server cache sync of the read model`, `Playwright for keystroke-grammar end-to-end fidelity tests; Vitest for unit tests`

**Key decisions:**

#### Rendering substrate: DOM vs canvas character grid → **DOM (React) with virtualization, NOT a canvas/WebGL character-cell renderer**
Tally's literal character grid is an artifact of its DOS/TurboVision lineage, not a requirement. The research (sec 4 Opportunities) explicitly wants 'crisp fonts, high-DPI, dark mode' rather than the dated grid. DOM gives us free accessibility (screen readers, focus, ARIA), native text input/IME for Indian-language entry, OS clipboard, print-to-PDF, and trivial responsive reflow for mobile — all of which a canvas grid would force us to re-implement from scratch. Pixel fidelity (monospace font, fixed right button-bar, green active-field highlight, bottom shortcut bar) is fully achievable in styled DOM. We keep the *visual grammar* without inheriting the *rendering technology*.
- *Alternatives rejected:* Canvas/WebGL character-cell grid (xterm.js-style) for true terminal pixel fidelity; Server-side-rendered HTML over the wire (HTMX-style) with no client state machine; Native widgets per platform (Qt/SwiftUI/Compose) for max nativeness
- *Risks:* Very large reports (multi-year Day Book) must be virtualized aggressively or DOM node count degrades scroll performance — mitigated by TanStack Virtual + fixed row heights; Achieving exact monospace column alignment across OS font rasterizers needs a bundled webfont and CSS ch-unit discipline

#### Cross-platform shell → **PWA-first single web codebase, packaged with Tauri 2.0 for desktop and Capacitor 6 for mobile**
One UX engine serving web + offline-first desktop + mobile is an explicit strategic requirement (no RDP hacks, mobile data entry, eliminate the VPS workaround). Tauri uses the OS WebView (no bundled Chromium) so desktop installers are ~10MB vs Electron's ~150MB, with far lower RAM — critical for the low-end Windows machines Indian SMBs run, where Tally itself is famously light. Rust core gives us a secure place for the offline binary-format migration parser and local crypto. Capacitor reuses the identical web bundle for app-store presence and native keyboard/camera (OCR capture). The PWA itself covers browser users with zero install.
- *Alternatives rejected:* Electron for desktop (rejected: heavy binary, high RAM, contradicts Tally's lightweight feel on low-end SMB hardware); React Native / Flutter for a separate mobile UI (rejected: forks the UX engine, doubles the keystroke-grammar maintenance, defeats single-engine goal); Pure web/PWA only (rejected: cannot do the offline .900/.1800 binary parser, weaker offline guarantees, no native desktop install some CAs expect)
- *Risks:* WebView feature drift across Windows (WebView2/Edge), macOS (WKWebView), Linux (WebKitGTK) — must test the grid on all three; WebKitGTK on Linux is the weakest and needs a CI matrix; Desktop keyboard shortcut capture (e.g. F1, Alt+letter) can collide with browser/OS bindings; Tauri lets us intercept globally where the PWA cannot, so the desktop shell becomes the highest-fidelity target

#### Keystroke/navigation engine → **A deterministic hierarchical state machine in XState v5, fed by a data-driven keymap registry, sitting between raw key events and the view**
Tally's navigation IS a hierarchical state machine: a screen stack where Enter pushes (drill/next-field), Esc pops (back), Ctrl+A commits, F-keys transition voucher type, Alt+G opens an overlay child machine that returns to its parent, Ctrl+G replaces the stack. Modeling this declaratively makes the load-bearing behavior testable and verifiable rather than emergent from scattered onKeyDown handlers — which is exactly the class of subtle navigation bug (wrong Esc target, lost screen on Go To) that makes a CA distrust the clone. Keymaps as data let us ship TallyPrime and ERP 9 profiles (sec 21: Credit Note Ctrl+F8 vs Alt+F6 etc.) by swapping a table, not rewriting logic. The same machine drives legacy and modern themes — only the rendered presentation differs.
- *Alternatives rejected:* Ad-hoc per-component onKeyDown handlers with a global hotkey library (react-hotkeys-hook) (rejected: non-deterministic, untestable navigation, Esc/drill stack becomes spaghetti); A custom hand-rolled reducer/stack machine (rejected: reinventing XState's tested actor/hierarchy model, harder to visualize/verify); Command-pattern dispatcher without explicit states (rejected: loses the screen-stack semantics that ARE the product)
- *Risks:* XState v5 has a learning curve; mitigated by confining it to the navigation/voucher core and keeping presentation in plain React; Context-dependent keys (Ctrl+H = Change Mode in vouchers vs Change View in reports; Alt+I collisions noted in sec 21) require the active state to scope the keymap — handled by per-state key resolution, but demands a rigorous keymap conformance test suite

#### Legacy-faithful vs modern mode from one engine → **Single interaction model + keymap; two presentation skins selected by a theme token layer and a 'fidelity mode' flag**
The interaction grammar (Enter/Esc/Ctrl+A/F-keys/drill-down) must be byte-identical in both modes because that is the zero-retraining promise; only chrome, color, font, density, and optional mouse affordances change. Encoding the difference as CSS custom-property design tokens (TallyPrime blue, ERP 9 green, modern light/dark) plus a small set of layout flags (show fixed button-bar, monospace grid on/off, show bottom shortcut bar) means we never fork the engine. AI affordances (anomaly flags, NL command palette over Alt+G, hover tooltips on button-bar) are additive overlays that never intercept the keyboard-only loop.
- *Alternatives rejected:* Two separate frontends for legacy and modern (rejected: doubles surface area, guarantees behavioral drift); Modern-only UI with a 'compatibility cheat sheet' (rejected: fails the CA veto test; muscle memory is the moat)
- *Risks:* Theme token sprawl if components hardcode colors — enforced via lint rule banning raw color literals and a Storybook visual-regression gate against TallyPrime screenshots; Temptation to let modern mode diverge in behavior; prevented by sharing the exact same XState machine and asserting keystroke parity across both modes in Playwright

#### Offline-capable rendering → **Local read model in IndexedDB (Dexie) + write outbox; UI renders entirely from local state, sync is background**
The keyboard entry loop must never stall on network latency — that would destroy the fast-entry feel Tally operators expect. The frontend reads from a local replica (ledgers, stock items, recent vouchers, report cache) so selection-lists auto-complete and Ctrl+A commits instantly to a local outbox, then sync to the cloud core happens asynchronously with conflict handling owned by the data domain. PWA service worker (Workbox) caches the app shell so the UI cold-starts offline.
- *Alternatives rejected:* Online-only with optimistic UI (rejected: unusable on the flaky connectivity common in Indian SMB/branch settings; contradicts offline-first desktop requirement); Full local SQLite via Tauri only (rejected: doesn't serve PWA/mobile from one path; IndexedDB read model works across all three shells, with optional SQLite acceleration inside Tauri)
- *Risks:* IndexedDB size/eviction limits for large companies — mitigated by caching only the working set (recent + frequently used masters) and lazy-fetching cold report data; Local read model staleness vs server truth — surfaced with a sync-state indicator and resolved by the sync/conflict layer in the data domain (out of scope here, but the frontend must render conflict prompts)

#### Accessibility approach → **Semantic DOM + ARIA roles, full keyboard operability as the baseline (which Tally already demands), screen-reader labels on the fixed button-bar and grid**
Because we chose DOM over canvas, accessibility is largely free and we exceed Tally (which has essentially none). Keyboard-first IS the accessibility story; we add ARIA live regions for the bottom shortcut bar and anomaly flags, proper roles for the selection-list combobox pattern, and visible focus that maps to Tally's green active-field highlight. Indian-language input works via native IME because inputs are real DOM fields.
- *Alternatives rejected:* No explicit a11y (rejected: regulatory/enterprise risk, and trivial to get given DOM choice); Canvas with a parallel a11y tree (rejected: this is the major hidden cost of canvas and a reason to avoid it)
- *Risks:* Heavy F-key/Alt bindings can conflict with assistive-tech shortcuts — provide a documented escape and ensure all actions are reachable via the Alt+G command palette as an a11y-friendly alternate path

**Replication fidelity.** Zero-learning-curve is preserved by making the interaction grammar a first-class, formally-modeled contract rather than emergent behavior. The XState NavigationMachine reproduces Enter=drill, Esc=back, Ctrl+A=accept, the screen stack, Alt+G overlay (preserves prior screen) vs Ctrl+G replace, Alt+C inline master creation, F2/F12/Ctrl+H/Alt+N/Ctrl+B report grammar, and the universal Enter-drill-to-voucher guarantee (no report is a dead end) exactly as specified in RESEARCH sec 4/8/21. Keymaps are data so we ship both a TallyPrime profile (F1=Help, blue, Credit Note Alt+F6) and an ERP 9 profile (F1=Select Company, green, Credit Note Ctrl+F8) — neutralizing Tally's own breaking changes and letting migrants from either product keep muscle memory, which Tally itself does not offer. The fixed-position right button-bar with key-on-button labels and the bottom shortcut bar are reproduced pixel-close in styled DOM. A Playwright keystroke-conformance suite codifies every binding as an executable spec run against both profiles and both fidelity modes, so fidelity regressions are caught before a CA ever sees them. Data compatibility is upheld at the rendering layer by binding directly to the canonical Tally-equivalent voucher/report schema (bill-wise due dates drive ageing and cash-flow projection columns), and the Tauri-hosted .900/.1800 parser plus XML/JSON import path feed the same local read model the UI renders, so 'every CA can still read it' holds from day one.

**AI angle.** AI plugs in as additive overlays that never intercept the keyboard-only entry loop: (1) Alt+G becomes an AI command palette — preserve literal type-a-report-name behavior, but also resolve natural-language queries ('overdue receivables from Mumbai this quarter') to a pre-filtered report or pre-filled voucher; (2) selection-lists gain semantic/fuzzy ranking (alias/GSTIN/phone match, 'last used for this party', duplicate-ledger detection at create time) rendered in the identical right-of-field dropdown so it looks native; (3) inline anomaly/GST-rate-mismatch/wrong-Dr-Cr flags surfaced in the bottom-bar shortcut real estate as the user types; (4) 'explain this number' on any report drill level without changing the keystroke grammar; (5) hover/long-press AI explainers on the fixed button-bar to erase Tally's coaching-institute training burden; (6) OCR-to-voucher drops the user into the exact F8/F9 screen pre-filled, preserving the Ctrl+A accept flow. All of these run against the local read model or stream from the server cache, so they augment but never block the deterministic state machine.

**Interfaces to other domains:**
- Data/Sync domain: frontend reads from a local IndexedDB read model and writes to a local outbox; consumes the data domain's schema for ledgers/vouchers/reports and renders sync-state + conflict-resolution prompts it raises (CRDT merge logic stays in that domain)
- Accounting/Compliance core: the VoucherMachine and ReportMachine bind to the canonical voucher schema (28 groups, bill-wise details, GST/TDS fields, due-date metadata for ageing/cash-flow projection) so drill-to-voucher and Ctrl+B ageing slabs render correctly
- AI/Insight layer: exposes a command-palette intent API (NL -> resolved report/voucher action), a selection-list ranking hook, and an inline-flag stream; frontend renders these as non-blocking overlays only
- Identity/RBAC: frontend respects per-company scopes and role-based field/screen visibility, and renders MFA/SSO prompts; keymap profile and theme are per-user preferences
- Extensibility layer: the design-token + component model is the surface the future TDL-replacement renders custom screens into, keeping default screens keystroke-identical while allowing modern components
- Migration tooling: Tauri Rust core hosts the offline .900/.1800 binary parser; frontend offers the one-click import UX that maps incoming objects into the local read model

<details><summary>Detailed engineering writeup</summary>

## Frontend & UX Replication Architecture — ADR

### 1. Problem framing
Tally's interface is a keyboard-first, single-window, character-grid application where an expert operator never touches the mouse (RESEARCH sec 4). Navigation is a strict two-key grammar: **Enter drills down/forward** (into a menu, a selected line, or the next field) and **Esc walks back up** until the Gateway of Tally. Every screen is one of three things: a menu list, a data-entry voucher form, or a report. Selection lists auto-filter as you type. Reports share one universal button grammar (F2 period, F12 configure, Ctrl+H change view, Alt+N column, Ctrl+B basis, Alt+E export, Enter to drill to the source voucher — *no report is a dead end*, sec 8). The strategic mandate is **zero retraining for operators and their CAs**: anything less and the CA vetoes the switch. So the frontend's first job is *fidelity of interaction*, and its second is to be modern underneath (crisp fonts, high-DPI, cloud, mobile, AI overlays).

### 2. Rendering: DOM, not a canvas character grid
The character grid is a historical artifact of Tally's TurboVision/DOS lineage, not a product requirement. The research opportunities explicitly call for replacing the dated grid with crisp high-DPI fonts and dark mode while keeping the grammar. A **DOM tree rendered by React 18** reproduces every visual signature — monospace work grid (bundled webfont + CSS `ch` units for column alignment), the green active-field highlight, the fixed right-edge button-bar, the bottom shortcut bar, TallyPrime-blue / ERP9-green chrome — while giving us, for free: accessibility, IME for Indian-language entry, OS clipboard, print-to-PDF, responsive reflow for mobile, and native text inputs. A canvas grid (xterm.js-style) would force us to rebuild all of that. Large reports are the one risk; we solve it with **TanStack Virtual** windowing and fixed row heights so a multi-year Day Book renders only the visible rows.

### 3. Shell: PWA-first, Tauri for desktop, Capacitor for mobile
One web codebase, three packagings:
- **PWA** (Vite + Workbox) for instant browser access and an installable, offline-capable app shell.
- **Tauri 2.0** for desktop. Tauri uses the OS WebView (WebView2 on Windows, WKWebView on macOS, WebKitGTK on Linux), producing ~10 MB installers with low RAM — matching Tally's famously light footprint on the low-end Windows machines Indian SMBs run. The Rust core also hosts the offline `.900/.1800` binary migration parser and local crypto, and can intercept global keystrokes (F1, Alt+letter) that a browser PWA cannot reliably capture — making desktop the highest-fidelity target.
- **Capacitor 6** wraps the identical bundle for iOS/Android, adding native camera (OCR receipt capture) and keyboard.

Electron is rejected (150 MB, heavy RAM, wrong feel). React Native/Flutter is rejected because it forks the UX engine and doubles the keystroke-grammar maintenance.

### 4. The Keystroke/Navigation State Machine (the load-bearing core)
Tally's UX *is* a hierarchical state machine, so we model it as one with **XState v5**. A `NavigationMachine` owns a **screen stack**:

```
NavigationMachine
  context: { stack: ScreenRef[], company, period, keymapProfile }
  states:
    gateway
    menu        (Enter -> push child menu/screen; Esc -> pop)
    voucherForm (invoke VoucherMachine)
    report      (invoke ReportMachine)
    goToOverlay (Alt+G: invoke child, on done -> return to invoker)   // keeps prior screen
    switchTo    (Ctrl+G: replace top of stack)                         // cannot return
  on:
    ENTER  -> drill / next-field (context-scoped)
    ESC    -> pop one level
    CTRL_A -> commit active screen
    FKEY   -> resolve via keymap -> transition voucher type / report action
```

`VoucherMachine` models the field cursor, the **Ctrl+H Change Mode** three-state enum {Item Invoice, Accounting Invoice, As Voucher}, Dr/Cr vs By/To display, Alt+C inline master creation (spawns a child master machine and returns the new ref into the field), date auto-advance, Page Up/Down across saved vouchers, and Ctrl+A / Enter-on-last-field commit. `ReportMachine` models the universal report grammar and the **Enter drill chain** (group → ledger/monthly → voucher, with Esc walking the exact reverse path).

**Keymaps are data, not code.** A registry exposes `TallyPrime` and `ERP9` profiles. Per RESEARCH sec 21 the breaking differences (Credit Note Ctrl+F8→Alt+F6, Debit Note Ctrl+F9→Alt+F5, Order vouchers remapped, F1 = Help vs Select Company, Alt+I collisions, Ctrl+H = Change Mode in vouchers vs Change View in reports) all live in this table. The active machine state scopes which keymap entries are live, so context-dependent keys resolve correctly. This makes us *better than Tally*, which forces ERP 9 migrants to relearn — we offer both profiles.

Why a formal machine and not scattered `onKeyDown` handlers: the navigation stack semantics (where does Esc land, does Go To preserve the screen) are precisely the subtle behaviors a CA notices and distrusts when wrong. A declarative machine is deterministic and **testable** — we assert the full keystroke grammar in Playwright against both keymap profiles.

### 5. One engine, two modes
"Legacy-Faithful" and "Modern" share the *exact same XState machines and keymap*; only presentation differs. The difference is a CSS custom-property **design-token** layer plus a few layout flags (`showFixedButtonBar`, `monospaceGrid`, `showBottomBar`, theme = tallyBlue | erp9Green | modernLight | modernDark). AI affordances — the NL command palette layered over Alt+G, inline anomaly/GST-mismatch flags in the bottom-bar real estate, hover tooltips/explainers on the fixed button-bar, predictive selection-list ranking — are **additive overlays** that never intercept the keyboard-only data-entry loop. A lint rule bans raw color literals; Storybook visual-regression tests gate the legacy skin against TallyPrime reference screenshots.

### 6. Offline rendering
The UI renders entirely from a **local read model** (Dexie/IndexedDB): ledgers, stock items, voucher types, recent vouchers, and cached report slices. Selection-list auto-complete and Ctrl+A commit hit local state instantly (zero network in the hot path), writing to a local **outbox** that the data/sync domain drains in the background. Workbox caches the app shell for offline cold-start. Inside Tauri we may accelerate with local SQLite, but IndexedDB is the common path so PWA, desktop, and mobile share one rendering source. The frontend renders sync-state and conflict prompts surfaced by the sync layer; CRDT/merge logic itself is owned by the data domain.

### 7. Accessibility
Choosing DOM makes us dramatically more accessible than Tally (which has none). Keyboard-first *is* the a11y baseline. We add ARIA live regions for the bottom shortcut bar and anomaly flags, the combobox pattern for selection-lists, visible focus mapped to Tally's green active-field highlight, and ensure every action is also reachable via the Alt+G command palette as an assistive-tech-friendly alternate path. Native DOM inputs give Indian-language IME for free.

### 8. Test strategy (fidelity is a contract)
- **Playwright keystroke-conformance suite**: a machine-readable spec of every binding in RESEARCH sec 4/8/21, run against both keymap profiles and both fidelity modes, asserting the resulting screen-stack state. This is the regression net that protects the zero-retraining promise.
- **Vitest** unit tests on the XState machines (drill stack, Go To return, mode toggles).
- **Storybook visual regression** against reference screenshots for the legacy skin.

### 9. Honest risks
WebView feature drift (WebKitGTK weakest on Linux) needs a CI matrix. Large-grid DOM performance hinges on disciplined virtualization. Global keyboard capture is reliable only in the Tauri/Capacitor shells, so the browser PWA is a slightly lower-fidelity target for a handful of OS-reserved chords — acceptable, with the desktop shell as the canonical experience.

</details>

**Open risks:** WebView engine drift across Windows (WebView2), macOS (WKWebView), and especially Linux (WebKitGTK, the weakest) requires a CI test matrix; grid alignment and shortcut capture must be validated on all three; DOM performance on very large reports (multi-year Day Book, large Trial Balance) depends entirely on disciplined virtualization; needs load testing against realistic company datasets; Some OS/browser-reserved key chords (e.g. certain Alt+letter, F-keys) can only be reliably captured in the Tauri/Capacitor shells, making the browser-only PWA a slightly lower-fidelity target — acceptable but must be documented and the desktop shell positioned as canonical; Exact monospace column alignment and the precise TallyPrime UI font are not officially published (RESEARCH sec 4 open question); we must pick a bundled monospace webfont and visually regression-test against reference screenshots, accepting near-pixel rather than byte-exact fidelity; Context-dependent key collisions (Ctrl+H Change Mode vs Change View; Alt+I invoice-toggle vs insert-voucher) demand rigorous per-state keymap scoping; a gap here produces exactly the navigation bug that erodes CA trust; Heavy F-key/Alt bindings may conflict with assistive technologies; the Alt+G command palette must remain a complete alternate action path for a11y; IndexedDB storage limits and eviction for large companies could break offline rendering of cold data; working-set caching strategy needs validation under real data volumes; Two-mode (legacy/modern) discipline can erode over time if components diverge; enforced only by shared machines + lint + visual-regression gates, which require ongoing maintenance commitment

---

## B.2 Backend Services & API Architecture

**Recommendation.** Build the backend as a single deployable modular monolith in Go (chi router, sqlc + pgx over PostgreSQL 16) with clean internal module boundaries (Identity, Ledger, Inventory, Compliance, Sync, AI, Integration-Gateway), deployed multi-tenant with row-level isolation keyed on company_id and a shared schema. Expose three coordinated public surfaces: (1) a versioned REST/JSON API (OpenAPI 3.1) as the primary contract for web/mobile/desktop clients and SDKs; (2) a drop-in Tally-XML-compatible ingress at POST /tally that accepts the exact ENVELOPE/HEADER/BODY XML and the TallyPrime 7.0 JSONEX dialect so existing connectors (Suvit, Webtel, tally-database-loader, custom TDL) work unchanged but over authenticated HTTPS; (3) gRPC only for internal module/worker traffic, not as a public surface. Treat the database as the system of record and the event log: every state-changing write goes through an append-only, hash-chained voucher/master event store (satisfying the MCA Edit Log mandate as a first-class invariant), publishes a domain event onto a transactional outbox, and is keyed by a node-partitioned UUIDv7 so branch/offline writes never collide. Idempotency is mandatory on all mutating endpoints via an Idempotency-Key + request-hash table. Background work (e-invoice IRN, e-way bill, GST filing, OCR, reconciliation, sync merge) runs on River (Postgres-backed Go job queue) so we avoid a second datastore for the MVP. Defer microservice extraction until a module's scaling or team-ownership pressure proves it necessary; the module boundaries are drawn so extraction is mechanical when that day comes.

**Chosen stack:** `Go 1.23 (single binary, modular monolith)`, `chi v5 HTTP router + net/http`, `PostgreSQL 16 (primary OLTP, system of record, event store, job queue)`, `pgx v5 + sqlc (typed SQL, no heavy ORM)`, `River (Postgres-backed durable job queue, Go-native)`, `Transactional Outbox + CDC via Postgres logical replication for the event bus`, `OpenAPI 3.1 contract-first REST/JSON (oapi-codegen for server stubs + SDKs)`, `encoding/xml + a hand-written Tally ENVELOPE codec for the /tally compatibility endpoint`, `gRPC + Protobuf (internal worker/module RPC only)`, `Redis 7 (rate limiting, OTP/GSP-session cache, hot read cache) — NOT system of record`, `Connect-RPC for streaming/webhook fan-out workers`, `OpenTelemetry (traces/metrics/logs) + Prometheus`, `Casbin or home-grown policy engine for RBAC mapped to Tally Security Levels`, `Deployed on Kubernetes (EKS, ap-south-1 Mumbai for data residency) behind an ALB; later add read replicas + Citus/partitioning for tenant scale`

**Key decisions:**

#### Primary language / runtime → **Go 1.23, single statically-linked binary**
This backend is overwhelmingly I/O-bound CRUD + accounting arithmetic + XML/JSON marshalling + many concurrent integration/sync/filing jobs — exactly Go's sweet spot. Go gives us cheap goroutine concurrency for the connector fan-out and sync workers, fast cold start and tiny memory footprint for cost-efficient multi-tenant density, a single deployable artifact (no JVM/runtime to manage), and a mature, boring ecosystem. Critically, the SAME Go core can be compiled into the offline-first desktop/edge sync node, so we do not maintain two server codebases. Accounting math uses integer minor-units (paise) or a fixed-point decimal library — never float — to guarantee Tally-to-the-rupee fidelity.
- *Alternatives rejected:* Java/Kotlin + Spring Boot (richest ERP/financial ecosystem, but heavyweight, slow start, high per-tenant memory, and we do not need its framework gravity for an MVP); C#/.NET (excellent, and culturally close to Tally's Windows world, but ties us to a heavier runtime and offers no advantage over Go for our edge/desktop reuse goal); Node.js/TypeScript (tally-database-loader is TS, fast to prototype, but weak for CPU-adjacent accounting integrity, GC pauses under load, and a less robust concurrency story for thousands of sync workers); Rust (best-in-class safety/perf, but slower to ship an MVP, smaller hiring pool in India, overkill for I/O-bound CRUD)
- *Risks:* Go's decimal/money story relies on a library (shopspring/decimal or cockroachdb/apd), not a language primitive — must enforce via lint + code review that float64 never touches money; Smaller pool of Go engineers experienced in financial domains than Java; mitigate with strong internal domain modeling docs

#### Monolith vs microservices → **Modular monolith (one deployable) with strictly enforced internal module boundaries; extract to services only on proven pressure**
Time-to-market is an explicit constraint and a brand-new product has no traffic to justify the operational tax (distributed tracing-to-debug, network failure modes, eventual-consistency bugs, N deploy pipelines) of microservices. A modular monolith gives us in-process transactions across Ledger+Inventory+Compliance — which matters enormously because a single Sales voucher atomically touches ledger postings, stock movement, GST computation, and the audit chain; doing that across service boundaries on day one would force sagas/2PC for our core write path. We enforce boundaries with Go internal packages, an import-linter (no cross-module imports except via published interfaces), and per-module schema ownership, so the eventual carve-out (AI and Integration-Gateway first, since they scale and fail independently) is mechanical.
- *Alternatives rejected:* Full microservices from day one (independent scaling and team ownership, but premature: distributed transactions on the core voucher write, huge ops burden, slower MVP); Serverless/functions (great for spiky OCR/filing, terrible for a stateful long-lived accounting core and connection-pool-hungry Postgres workloads)
- *Risks:* Module boundaries erode under deadline pressure if not mechanically enforced — mitigate with import-linter in CI and architecture tests; A single deploy means a bad release affects all modules; mitigate with feature flags, canary deploys, and fast rollback; Vertical scaling ceiling — mitigate early with read replicas and tenant-aware Postgres partitioning before it becomes urgent

#### Public API protocol → **Contract-first REST/JSON (OpenAPI 3.1) as the primary public surface; gRPC internal-only; no public GraphQL at MVP**
REST/JSON is the lingua franca for Indian SMB integrators, partner SDKs, and the Tally connector ecosystem we are courting; OpenAPI gives us generated SDKs (Python/Node, which Tally never published) and stable, documented field names that directly attack Tally's TDL-method opacity. gRPC's typed contracts and streaming are ideal between our own modules and workers but a poor public choice (browser/partner friction, GSP/bank webhooks are HTTP). GraphQL is tempting for the report-heavy read side but adds query-cost/abuse surface and N+1 risk on a multi-tenant financial DB; we instead expose a small set of well-shaped report endpoints plus a documented, filterable report catalog (GET /reports), and revisit GraphQL post-MVP if client demand proves it.
- *Alternatives rejected:* GraphQL-first (flexible reads for dashboards, but query-cost governance, caching, and authz-per-field complexity slow the MVP and create abuse vectors on financial data); gRPC/Connect as the public surface (great typing, poor partner/browser ergonomics in this market); Pure RPC-over-XML mimicking Tally only (would saddle modern clients with Tally's cryptic envelope — we keep that strictly as a compatibility shim)
- *Risks:* Report endpoints can sprawl; govern with a canonical filter/pagination convention and a report-definition registry; REST over-fetch on mobile; mitigate with field-selection query params and ETag caching

#### Tally-XML-compatible ingress → **A dedicated Integration-Gateway module exposing POST /tally (and /tally/json) that parses the exact Tally ENVELOPE (TALLYREQUEST/TYPE/ID, STATICVARIABLES, TALLYMESSAGE) and TallyPrime 7.0 JSONEX, translates to internal domain commands, and emits byte-compatible RESPONSE blocks (CREATED/ALTERED/DELETED/LASTVCHID/LASTMID/LINEERROR) — over authenticated HTTPS with API keys / OAuth2 and per-company scopes**
This is the single highest-leverage compatibility surface in the whole product: existing connectors and custom TDL POST to localhost:9000 with this exact envelope. Accepting it drop-in means the entire third-party ecosystem works against our cloud on day one, while we fix Tally's worst flaw (the unauthenticated plaintext port) by wrapping it in TLS + auth + scopes. We normalize the date-format mess (accept YYYYMMDD, D-MMM-YYYY, ISO 8601; store YYYYMMDD/ISO internally), map SVCURRENTCOMPANY to a resolved tenant, and return both a legacy-compatibility tag mode and a modern normalized schema. The gateway also enforces idempotency and true transactional (all-or-nothing) import so a partial batch never leaves a half-applied LASTVCHID — explicitly fixing a Tally defect.
- *Alternatives rejected:* Only offer the modern REST API and ship migration adapters for top connectors (rejected: abandons the long tail of custom TDL/connectors that are precisely the lock-in we want to neutralize); Emulate the raw port 9000 socket semantics including no-auth (rejected: security non-starter; we instead document a local HTTPS proxy that speaks the identical envelope)
- *Risks:* The full set of valid <ID> report/collection names and the exact RESPONSE/exception nesting are partly tribal knowledge (flagged open in research) — must be validated against a live TallyPrime and real connector traffic; build a conformance test corpus captured from actual connectors; Schema drift between ERP9 / TallyPrime / JSONEX — we commit to a single stable schema and a translation layer, accepting maintenance cost as a moat

#### Service / module boundaries → **Seven modules: Identity (tenants, users, RBAC mapped to Tally Security Levels, SSO/MFA), Ledger (groups/ledgers/vouchers/double-entry/audit-chain), Inventory (stock items/godowns/batches/orders/BoM), Compliance (GST/TDS/TCS rules engine, e-invoice IRN, e-way bill, GSTR filing, IMS), Integration-Gateway (Tally-XML ingress, REST, webhooks, connectors), Sync (offline/branch merge, CRDT-ish line/field reconciliation, conflict queue), AI (NL query, OCR, forecasting, anomaly/recon) — all in one process at MVP**
These boundaries mirror Tally's own conceptual modules (so the domain model stays legible to CAs) and, crucially, separate the fast-changing/independently-scaling concerns (AI compute-heavy, Integration-Gateway burst/abuse-prone, Compliance tied to external GSP/NIC latency and statutory cadence) from the transactional core (Ledger+Inventory) that must share ACID transactions. Compliance is deliberately its own module because statutory schemas change on a regulatory clock and must be hot-patchable with effective-dated config without redeploying the ledger. AI and Integration-Gateway are the designated first extractions.
- *Alternatives rejected:* Collapsing Compliance into Ledger (rejected: couples statutory release cadence to core stability); Splitting Ledger and Inventory into separate services (rejected: the Sales/Purchase voucher write needs them in one transaction; integrated-vs-separate inventory is a per-company flag, not a service boundary)
- *Risks:* AI module pulling read load onto the OLTP primary — mitigate with a dedicated read replica / analytics store before launch of heavy AI features; Compliance module's external-dependency failures (GSP/NIC down) bleeding into request latency — isolate behind the job queue and circuit breakers

#### Idempotency, eventing, background jobs, and ID strategy → **Mandatory Idempotency-Key (client UUID) + request-hash dedupe table on all mutations; transactional outbox + Postgres logical-replication CDC as the event bus; River (Postgres-backed) for durable background jobs; node-partitioned UUIDv7 as the primary key for all vouchers/masters with human-friendly per-series voucher numbers as a display layer**
Idempotency is non-negotiable for a financial system fronting retrying connectors, flaky mobile networks, and GSP/bank webhooks — the same e-invoice request must never mint two IRNs. The transactional outbox guarantees that a domain event (voucher.created, payment.received, gstr2b.mismatch) is published if and only if the DB write commits, giving us first-party webhooks/event streams Tally never offered, without a separate broker at MVP (CDC reads the outbox; we add Kafka/NATS only if event volume demands). River keeps jobs in Postgres so the MVP runs on ONE datastore with exactly-once-ish semantics and visible job state — ideal for resumable, idempotent GST filing with ARN-state tracking and 6-hour GSP-token auto-renewal. Node-partitioned UUIDv7 keys eliminate the branch/offline voucher-number and GUID collisions that hard-fail Tally sync, while the audit chain is an append-only, per-tenant hash-chained event log so the MCA Edit Log is an architectural invariant, not a bolt-on, and every cross-node merge is provably recorded.
- *Alternatives rejected:* Kafka/NATS as the event bus from day one (rejected for MVP: operational weight; outbox+CDC covers our throughput and upgrades cleanly later); Redis/Sidekiq-style or SQS for jobs (rejected: second datastore and weaker transactional coupling than River-in-Postgres; durability/visibility worse for financial retries); Auto-increment or composite (vchtype, number) keys like Tally (rejected: the exact cause of Tally's branch-sync collisions); Last-writer-wins sync (rejected: silently destroys edits — we use a surfaced conflict queue with field-level merge)
- *Risks:* Postgres-as-everything (OLTP + jobs + event log) is a single scaling chokepoint — mitigate with partitioning, replicas, and a clean seam to externalize the bus/queue later; CDC/outbox ordering and replay correctness need careful testing; Hash-chained audit log must be carefully designed so legitimate corrections append (never mutate) and so chain verification is cheap to run for auditors

**Replication fidelity.** Zero-learning-curve is preserved at the protocol and data layers, not just the UI. The /tally ingress accepts the byte-exact Tally ENVELOPE and JSONEX so existing connectors and custom TDL work drop-in, and we emit a legacy-tag compatibility response mode (DSPACCNAME/LASTVCHID/etc.) alongside the modern schema. Date-format ambiguity (YYYYMMDD vs D-MMM-YYYY) is absorbed at the edge and normalized internally. Filing-flow keystroke semantics (Alt+Z exchange, C/E/D/S/V/T) map onto resumable idempotent jobs so the CA's muscle memory and the connector ecosystem both transfer unchanged. Data-compat fidelity: the importer maps Ledger/Group/Voucher/StockItem/CostCentre objects 1:1, accounting math is fixed-point to reproduce the trial balance and GSTR-1/3B to the rupee, and the append-only hash-chained audit log satisfies the MCA Edit Log mandate as a default invariant — making the migration verifiably lossless, which is the CA's veto condition.

**AI angle.** AI is a first-class module but is deliberately kept off the transactional write path. It subscribes to the outbox event stream and reads a dedicated Postgres read-replica/analytics store, so NL query, layout-independent OCR line-item extraction, predictive cash flow, GST/IMS reconciliation, and edit-log anomaly detection run server-side over the full multi-tenant dataset — something impossible against Tally's opaque in-RAM object graph. Every AI action that mutates data is confidence-scored and routed through the SAME idempotent REST/command path a human uses, preserving human-in-the-loop one-keystroke accept and guaranteeing AI writes are audited identically to manual ones. The Integration-Gateway additionally uses AI to auto-map arbitrary third-party CSV/Excel/JSON onto the Tally-native voucher schema, eliminating per-client TDL customization, and to turn Tally's single free-text LINEERROR into structured, auto-correctable error objects.

**Interfaces to other domains:**
- Identity issues short-lived JWTs / API keys with company-scoped claims that every other module's middleware validates; RBAC policies map 1:1 onto Tally Security Levels so migrating admins keep the Disallow/Allow mental model
- Integration-Gateway is the single public ingress: it translates Tally XML/JSONEX and REST into internal domain commands dispatched to Ledger/Inventory/Compliance via in-process interfaces (later gRPC)
- Ledger owns the transactional write that Inventory and Compliance participate in within the same DB transaction (in-process call, not network) for the atomic voucher post
- Compliance consumes voucher.created/altered outbox events to drive e-invoice IRN, e-way bill, and GSTR aggregation asynchronously, and calls external GSP/NIC APIs from jobs behind circuit breakers
- AI module subscribes to the outbox event stream and reads a Postgres read-replica for NL query, OCR-to-voucher prefill, forecasting, and anomaly/recon — writing back only via the same idempotent REST/command path a human would use (human-in-the-loop)
- Sync module reconciles offline/edge writes from the embedded Go node using node-partitioned UUIDv7 and a conflict queue, appending every merge to the hash-chained audit log shared with Ledger
- Outbox + CDC publishes domain events to first-party webhooks and partner event streams; the same bus later feeds an external broker without changing producers

<details><summary>Detailed engineering writeup</summary>

# ADR: Backend Services & API Architecture

## Context and forces

Tally has **no hosted API**. Its only first-party integration is an *unauthenticated, plaintext* XML-over-HTTP server embedded in `Tally.exe` on `localhost:9000`, which dies the moment the app closes (Research §12, §18). Its data engine is a proprietary in-RAM object store with no SQL schema (§10), so reports require loading the whole company into memory and corruption (`Manager.900`/`TranMgr.900`) is a recurring trust failure. Its multi-branch "sync" is a directional last-syncer-wins overwrite that silently destroys edits and hard-fails on GUID / voucher-number collisions (§20). The MCA Edit Log mandate (§13, §20) requires an immutable, append-only audit trail.

Our backend must (a) ship fast, (b) be ACID-correct to the rupee, (c) be drop-in compatible with the existing Tally connector ecosystem, and (d) scale multi-tenant with real concurrency and first-class compliance.

## Decomposition: modular monolith in Go

We deploy **one Go binary** containing seven modules with enforced boundaries. The decisive reason is the **core write path**: a single Sales voucher atomically posts double-entry ledger lines, moves stock, derives CGST/SGST/IGST via the Nature-of-Transaction rules engine (§19), appends to the hash-chained audit log, and emits an outbox event. On day one that *must* be one ACID transaction:

```
BEGIN;
  INSERT voucher (id=uuidv7, company_id, type, ...);          -- Ledger
  INSERT ledger_entries (...);                                -- Ledger (Dr=Cr enforced)
  INSERT stock_movements (...);                               -- Inventory
  INSERT gst_analysis (...);                                  -- Compliance
  INSERT audit_log (prev_hash, payload, hash=H(prev||payload)); -- immutable chain
  INSERT outbox (topic='voucher.created', payload, company_id); -- event
COMMIT;
```

Splitting Ledger/Inventory/Compliance into separate services on day one would force sagas or 2PC across this hot path — a self-inflicted wound for a pre-traffic product under time pressure. Boundaries are enforced mechanically (Go `internal/` packages, an import-linter in CI: no module imports another except via a published interface, each module owns its tables). **AI** and **Integration-Gateway** are the designated first extractions because they scale and fail independently of the ledger.

Go is chosen over Java/.NET/Node/Rust because the workload is I/O-bound CRUD + XML/JSON codec + thousands of concurrent connector/sync/filing goroutines, and because the same core compiles into the **offline-first desktop/edge sync node** — one codebase, not two. Money is fixed-point (`cockroachdb/apd` or integer paise); `float64` is lint-banned.

## The three API surfaces

1. **REST/JSON, contract-first (OpenAPI 3.1).** Primary surface for web, mobile, desktop, partner SDKs. `oapi-codegen` produces server stubs and Python/Node SDKs — the documented, stable field names Tally never published. Reports are exposed as well-shaped endpoints plus a discoverable `GET /reports` catalog (export-capable vs import-capable metadata), killing Tally's tribal "magic ID string" knowledge.

2. **Tally-XML-compatible ingress — the highest-leverage compatibility surface.** `POST /tally` accepts the *exact* `<ENVELOPE>/<HEADER>/<BODY>` dialect and TallyPrime 7.0 **JSONEX**, so Suvit, Webtel, `tally-database-loader`, and custom TDL POST unchanged — but over **HTTPS with API keys / OAuth2 + per-company scopes**, fixing Tally's worst security gap. Pseudocode:

```go
func (g *Gateway) HandleTally(w, r) {
    env := decodeEnvelope(r.Body)            // XML or JSONEX
    tenant := resolveCompany(env.SVCurrentCompany, r.Auth)
    date := normalizeDate(env)               // YYYYMMDD | D-MMM-YYYY | ISO -> internal
    switch env.Header.TallyRequest {
    case "Import":                           // Vouchers / All Masters
        res := g.ledger.ImportTxn(ctx, tenant, env.Data, idempotencyKey(r)) // all-or-nothing
        writeResponse(w, legacyResponseBlock(res)) // CREATED/ALTERED/LASTVCHID/LINEERROR
    case "Export":                           // Day Book, Trial Balance, ...
        writeResponse(w, g.reports.Render(ctx, tenant, env.Header.ID, date, env.ExportFormat))
    }
}
```

Imports are **transactional (all-or-nothing)** — fixing Tally's half-applied-`LASTVCHID` defect — and we emit both a legacy-tag compatibility mode and a modern normalized schema. The exact `<ID>` catalog and RESPONSE nesting are partly tribal (research open questions §12/§18); we build a **conformance corpus from captured real connector traffic** and validate against a live TallyPrime.

3. **gRPC/Protobuf — internal only.** Module-to-worker and (post-extraction) service-to-service RPC. Never public: browsers, GSPs, and banks speak HTTP.

GraphQL is deferred — its query-cost governance and per-field authz on multi-tenant financial data slow the MVP without proportional benefit.

## Idempotency, eventing, jobs, IDs

- **Idempotency** is mandatory on every mutation. Clients send `Idempotency-Key`; we store `(key, request_hash, response)` and replay the stored response on retry. Non-negotiable when a retried e-invoice request must never mint two IRNs.
- **Eventing via transactional outbox + Postgres logical-replication CDC.** Events publish iff the write commits. This yields first-party webhooks (`voucher.created`, `payment.received`, `gstr2b.mismatch`) Tally never offered, on a single datastore. Kafka/NATS is added only when volume demands; the seam is clean.
- **Background jobs on River** (Postgres-backed, Go-native): e-invoice IRN, e-way bill, OCR, GST filing, bank-feed recon, sync merge. Jobs are durable, visible, and resumable — perfect for **idempotent GST filing with ARN-state tracking and background 6-hour GSP/OTP-token renewal** (§19), replacing Tally's manual Continue/Restart.
- **IDs: node-partitioned UUIDv7** as the PK for all vouchers/masters, with human-friendly per-series numbers as a display layer. This structurally eliminates the cross-node collisions that hard-fail Tally sync (§20). The **Sync** module does field/line-level merge with a *surfaced conflict queue* instead of silent overwrite.
- **Audit log as architectural invariant:** an append-only, per-tenant **hash-chained** event log makes the MCA Edit Log tamper-evident by default (stronger than Tally's view-only two-binary kludge), and every cross-node merge is provably recorded.

## Multi-tenancy and data residency

Shared-schema, **`company_id`-scoped row isolation** enforced at the data-access layer (and Postgres RLS as defense-in-depth), deployed in **ap-south-1 (Mumbai)** for residency. Read replicas + tenant-aware partitioning (later Citus) absorb scale; the AI module reads from a replica so analytics never contend with the OLTP write path. PostgreSQL is system-of-record, event store, and job queue at MVP — deliberately one datastore — with explicit seams to externalize the bus and queue when a module's pressure proves it.

</details>

**Open risks:** The full valid <ID> report/collection catalog and the exact RESPONSE/EXCEPTIONS nesting for the Tally ingress are not fully documented (research-flagged open questions) and must be reverse-validated against a live TallyPrime and real connector traffic before GA; PostgreSQL as OLTP + event store + job queue is a single scaling chokepoint; partitioning, replicas, and externalization seams must be in place before high tenant volume, not after; Module-boundary erosion under deadline pressure would silently turn the monolith into a big ball of mud — relies on disciplined CI import-linting and architecture tests holding; Field/line-level CRDT-style sync merge with a surfaced conflict queue is genuinely hard; correctness under concurrent offline edits plus the immutable-audit-log constraint needs heavy property/fuzz testing; External GSP/NIC/bank API latency, 6-hour token expiry, encryption, and rate limits can cascade into our request path if circuit breakers and job isolation are not rigorous; Transactional outbox + CDC ordering/replay correctness (exactly-once webhook delivery) is a classic source of subtle bugs; Tally schema drift across ERP9 / TallyPrime / JSONEX is an ongoing maintenance tax we are choosing to absorb as a moat

---

## B.3 Data Model & Accounting/Ledger Core

**Recommendation.** Use PostgreSQL 16 as the single source of truth, with a shared-database / shared-schema multi-tenant model keyed on a mandatory tenant_id (company_id) column enforced by Row-Level Security, plus per-tenant logical partitioning of the hot voucher tables. Model the ledger as a hybrid: immutable, append-only voucher VERSIONS (an application-level event log, not full Kafka/event-sourcing) where every Create/Alter/Delete produces a new version row and never an in-place update of accounting data — this satisfies the MCA Rule 11(g) Edit Log mandate by construction and gives free before/after comparison. Postings (the double-entry lines) are immutable child rows of a version; the "current" voucher is just the latest non-superseded version. Trial Balance is a real-time roll-up computed from an incrementally-maintained ledger-period-balance table (refreshed inside the same transaction that commits a voucher), not a full scan, so it stays sub-second at millions of vouchers — directly attacking Tally's load-into-RAM / split-by-year pain. Reproduce Tally's exact 28-group skeleton, ledger/voucher-type/bill-wise/cost-centre/inventory masters 1:1 so muscle memory and the CA's mental model transfer with zero retraining, and so XML/ODBC migration maps field-for-field.

**Chosen stack:** `PostgreSQL 16 (primary OLTP + system of record)`, `Row-Level Security (RLS) for tenant isolation, GUC app.current_company_id set per connection`, `Declarative LIST/HASH partitioning on hot tables (voucher, posting) by company_id`, `NUMERIC(19,4) for all money/quantity; no floats`, `DECIMAL-safe double-entry enforced via deferred constraint triggers (sum(debit)=sum(credit) per voucher version)`, `Hash-chained append-only audit (per-row prev_hash + row_hash, SHA-256) for tamper-evidence`, `BRIN indexes on (company_id, voucher_date) for range scans; B-tree on ledger/bill lookups`, `ltree extension for Group / Stock Group / Cost Centre / Godown hierarchies + materialized path`, `Logical replication / pgBouncer for read replicas powering reports & the AI layer`, `Citus (optional, deferred) only if a single tenant outgrows one node — not needed for MVP`

**Key decisions:**

#### Primary datastore → **PostgreSQL 16 as the single system of record (OLTP + reporting via replicas).**
Boring, proven, ACID, free of license cost, ubiquitous DBA/CA-auditor familiarity. NUMERIC exact decimals, deferred constraint triggers for the balance check, RLS for tenancy, ltree for hierarchies, partitioning for scale, logical replication for read fan-out, and rich JSONB for the TDL-replacement extensibility fields — every primitive we need is first-party. Tally's #1 weakness is its opaque in-RAM object store that must be split by year and rewritten on corruption; a managed ACID RDBMS eliminates that entire failure class.
- *Alternatives rejected:* MySQL/MariaDB (weaker partitioning, no deferred constraints, weaker RLS, poorer NUMERIC ergonomics); A document store like MongoDB (no real cross-document ACID for double-entry, would force app-level balance integrity); A dedicated ledger DB (AWS QLDB / immudb) for the audit requirement (vendor lock-in, weak relational reporting, immature ecosystem — we get tamper-evidence cheaply with a hash chain in Postgres); Mirroring Tally with a bespoke embedded object store (re-creates the exact moat/pain we are attacking)
- *Risks:* A single very large tenant (10M+ vouchers/yr) could pressure one node; mitigated by partitioning now and Citus later.; RLS misconfiguration is a catastrophic cross-tenant leak; must be enforced by default-deny policies + integration tests on every table.

#### Tenancy isolation model → **Shared database, shared schema, mandatory company_id on every business row, enforced by Postgres Row-Level Security; large tenants can be promoted to a dedicated schema/database later.**
Tally's unit of isolation is the Company (a numbered folder). We map Company -> company_id (tenant). Shared-schema with RLS gives the lowest per-tenant overhead (millions of micro-SMBs), one migration to maintain, and pooled connections via PgBouncer. RLS with a default-deny policy and a session GUC (app.current_company_id) means even a buggy query physically cannot read another tenant's rows. A Group Company is modelled as a parent tenant with member company_ids for consolidation — no data copy, unlike Tally's Alt+F3 load-each-into-RAM.
- *Alternatives rejected:* Schema-per-tenant (migration/connection-pool explosion at millions of tenants); Database-per-tenant (operationally impossible at SMB scale; reserved as an enterprise upsell tier); App-only WHERE company_id = ? with no DB enforcement (one missed clause = silent cross-tenant breach)
- *Risks:* RLS adds a predicate to every query — verified negligible with proper indexes but must be load-tested.; Noisy-neighbour: a heavy tenant report on the primary; mitigated by routing all reads to replicas and per-tenant rate limits.

#### Event-sourcing vs mutable-with-audit-log for the immutable Edit Log → **Append-only voucher VERSIONING at the application/table level (a domain event log), NOT full CQRS/event-sourcing and NOT mutable-with-side-audit-table.**
MCA Rule 11(g) requires create/alter/delete to be recorded immutably and comparable before/after, never disabled. The cleanest model: a voucher_version row is written for every Create/Alter/Cancel/Delete; accounting rows (postings) are immutable children of a version; the 'live' voucher is the max(version) that is not a delete. This gives native before/after diff (Tally's compare-two-versions), free history, and tamper-evidence via a per-row hash chain — without the operational complexity, eventual-consistency, and rebuild-projection burden of true event-sourcing. Mutable-with-trigger-audit was rejected because an UPDATE that mutates accounting numbers in place is exactly what the regulation distrusts and makes point-in-time financials hard.
- *Alternatives rejected:* Full event-sourcing with an event store + projections (overkill, eventual consistency hurts a Trial Balance that must tie out instantly, harder for CAs to audit); Mutable rows + a separate trigger-populated audit_log table (the canonical row is destroyed on edit; reconstructing historical financials and proving immutability is fragile); Postgres temporal/system-versioned tables via an extension (less control over the domain semantics of cancel vs delete vs alter that Tally exposes)
- *Risks:* Version tables grow unbounded; mitigated by partitioning superseded versions to cheaper storage, never deleting.; The 'current voucher' query (latest version) must be indexed carefully or it scans history; solved with a current_version_id pointer on the voucher header.

#### Modelling the 28 predefined Groups, ledgers, voucher types → **Seed the 28 system Groups (15 primary + 13 sub) as is_system rows per tenant (or shared catalog cloned on tenant creation), with a self-referential parent_id + ltree path; nature (Asset/Liability/Income/Expense) and BS-vs-PL flag live on the group and are inherited by ledgers — never tagged on the ledger directly.**
This is the single most important Tally design fact: a ledger's report behaviour is wholly inherited from its parent group, so financial statements assemble themselves. Replicating the exact tree (and the behaviour flags: behaves-like-sub-ledger, nett-balances, used-for-calculation) preserves CA muscle memory and makes XML migration field-for-field. System groups are non-deletable (enforced by is_system + a delete-guard). Ledgers carry only Name + Under(group_id) + opening balance + flags (maintain-bill-by-bill, etc.). Cash and Profit & Loss A/c are seeded non-deletable ledgers.
- *Alternatives rejected:* A generic IFRS/GAAP chart-of-accounts (breaks zero-retraining and migration fidelity); Hard-coding group nature in app code instead of data (prevents the user-created primary groups Tally allows)
- *Risks:* The Reserves & Surplus / Bank OCC ambiguity in sources (28 vs 30) — resolve empirically against a clean TallyPrime install before freezing the seed.; Users creating their own primary groups must still resolve to a nature; enforce nature is non-null at the primary level.

#### Bill-wise allocation, cost centres/categories → **Bill references modelled as bill_allocation rows linked to a posting, with ref_type enum (New Ref / Agst Ref / Advance / On Account) and a bill master tracking running outstanding; cost allocation modelled as cost_allocation rows linking a posting to (cost_centre_id, cost_category_id) by amount, allowing parallel allocation across categories.**
Mirrors Tally exactly: a single invoice posting can split into multiple bill refs with their own due dates (feeds ageing/Outstandings), and the same amount can be allocated in parallel across independent cost categories (employee AND scheme). Outstanding/ageing and cost-centre P&L become straightforward GROUP BY queries over these child tables, kept fast by the same incremental-balance pattern.
- *Alternatives rejected:* Storing bill refs as JSON on the posting (loses queryable ageing and FK integrity to the bill master); One cost-centre column on the posting (cannot represent parallel category allocation)
- *Risks:* Allocation totals must equal the posting amount (Agst+Advance+OnAccount) — enforce with a deferred trigger.; Bill outstanding is itself a running balance; maintain incrementally like ledger balances or it scans.

#### Inventory masters (4-level hierarchy, godowns, batches, BoM) and integrated valuation → **Stock Group (ltree, nestable) + Stock Category (orthogonal, ltree) + Stock Item + Unit/Compound/Alternate unit masters; Godown hierarchy (ltree); per-(item,godown,batch) stock ledger of immutable inventory movements; BoM as header + component lines with type enum (Component/Co-Product/By-Product/Scrap, rate%). Costing method stored per item (default Avg Cost); closing valuation computed from the movement ledger; an 'Integrate Accounts with Inventory' tenant flag controls whether computed closing stock flows into the BS/P&L or is entered manually.**
Reproduces Tally's full inventory power (9 costing + 4 market methods, batch/expiry FEFO, multi-godown valuation, multi-level BoM) that SMB cloud rivals lack — a differentiator. Treating stock movements as an immutable ledger parallels the accounting ledger and lets us recompute valuation under any method on demand (the 'what-if FIFO vs Avg' AI feature) without mutating data. The integrate flag is the pivotal Tally footgun we make explicit and AI-guard against stale stock figures.
- *Alternatives rejected:* Mutable on-hand quantity columns (cannot reproduce historical/period valuation or method what-ifs; race conditions); Separate inventory microservice/DB at MVP (splits the transaction that must atomically post accounting + inventory for an Item Invoice)
- *Risks:* Valuation methods like FIFO Perpetual / Monthly Avg are subtle; must match Tally's exact algorithm or CAs reject the closing stock figure — needs a conformance test suite against Tally output.; Movement ledger volume is high; partition by company_id and period.

#### Real-time Trial Balance roll-up → **Maintain a ledger_balance table keyed (company_id, ledger_id, period) updated incrementally inside the same DB transaction that writes each voucher version's postings (via trigger or app service); Trial Balance and BS/P&L are then aggregations of these balances rolled up the group ltree — no full posting scan.**
Tally's Trial Balance is the real-time roll-up of ledger closing balances by group; correctness requires it to tie to the postings exactly. Incremental maintenance keeps it O(changed-ledgers) per voucher and O(groups) to render, so it is sub-second at millions of vouchers and never needs split-by-year. Because postings are append-only, a periodic reconciliation job can re-derive balances from postings to detect any drift (defence in depth).
- *Alternatives rejected:* Compute Trial Balance by scanning postings each time (matches Tally's recompute but slow at scale; loses our headline 'unlimited hot history' advantage); Postgres materialized views refreshed on a schedule (stale between refreshes; a Trial Balance that doesn't tie to the latest voucher is unacceptable to a CA)
- *Risks:* Incremental balances can drift if a bug skips an update; mitigated by an async reconciliation job that recomputes from the immutable postings and alarms on mismatch.; Hot-row contention on a popular ledger (e.g. Cash) under concurrency; mitigate with per-period rows and, if needed, balance deltas summed lazily.

**Replication fidelity.** Zero-learning-curve is preserved at the data level by mirroring Tally's exact conceptual model: the rigid 28-group skeleton (15 primary + 13 sub) with nature/BS-vs-PL inherited by ledgers (never tagged directly), the non-deletable Cash and Profit & Loss A/c ledgers, the 24 predefined voucher types with their semantics, the four bill-reference types (New Ref/Agst Ref/Advance/On Account), cost categories allowing parallel allocation, the 4-level inventory hierarchy with godowns/batches/BoM, and the 9 costing + 4 market valuation methods. Because every master maps 1:1 to a Tally object, the CA sees an identical Chart of Accounts and Trial Balance, and lossless XML/ODBC migration is field-for-field with Tally GUIDs stored as external_id for idempotent re-import. Data-compat with statutory reality is preserved by the always-on, immutable, hash-chained version log that satisfies MCA Rule 11(g) by construction (no two-binary kludge), and by computing closing stock and Trial Balance to tie exactly to the postings so figures match what a Tally-trained auditor expects.

**AI angle.** AI plugs in cleanly precisely because data lives in a queryable relational store rather than Tally's opaque in-RAM graph. Read replicas feed: (1) NL-to-SQL queries over ledgers/postings; (2) anomaly/fraud detection over the immutable, hash-chained voucher version log (back-dated alterations, round-tripping, weekend bulk deletes auto-surfaced to the CA); (3) auto-classification suggesting the correct Group for a new ledger with a confidence + explanation, killing the #1 beginner error; (4) bill auto-matching of incoming receipts to open invoices to pre-select Agst Ref; (5) valuation what-ifs (FIFO vs Avg) computed on demand from the immutable movement ledger; (6) Difference-in-Opening-Balances auto-reconciliation against prior-year closing trial balance. Human-in-the-loop: AI proposes, the user accepts with the same Ctrl+A flow, and every AI-applied change is itself a versioned, attributed entry in the Edit Log.

**Interfaces to other domains:**
- Compliance/GST/TDS engine: reads postings + voucher versions; the immutable version chain is the legal Edit Log evidence; tax ledgers live under the Duties & Taxes group; e-invoice IRN / e-way-bill numbers attach to the voucher header.
- Migration/Importer: maps Tally XML/ODBC objects (Company->tenant, Group, Ledger, VoucherType, Voucher, StockItem, CostCentre, Bill) field-for-field into these masters; idempotent on Tally GUID stored as external_id.
- AI insight layer: runs on read replicas over the same relational schema — NL query over ledgers, anomaly detection on the voucher version/hash chain (back-dated edits, weekend bulk deletes), bill auto-matching for Agst Ref, predictive cash flow from bill due dates.
- UI/keystroke layer: voucher entry posts atomically (header + postings + bill + cost allocations) via one transaction so Ctrl+A save mirrors Tally; drill-down (Balance Sheet -> group -> ledger -> voucher) maps to ledger_balance -> postings -> voucher_version.
- Extensibility (TDL replacement): user-defined fields stored as validated JSONB on masters/vouchers; custom report definitions read the same balance/posting tables.

<details><summary>Detailed engineering writeup</summary>

# ADR: Multi-Tenant Data Model & Double-Entry Ledger Core

## 1. Context and forces
We are replacing Tally's proprietary, encrypted, in-RAM object store (`.900`/`.1800` + `.tsf` WAL) with a cloud-native system of record. Tally's model is **Groups -> Ledgers -> Vouchers**: a ledger inherits its Balance-Sheet-vs-P&L nature from its parent group, so financial statements assemble themselves. The Trial Balance is simply the real-time roll-up of ledger closing balances by group. The MCA Rule 11(g) Edit Log mandate requires create/alter/delete of vouchers AND masters to be recorded immutably, comparable before/after, and impossible to disable. Tally's pain is loading the whole company into RAM, splitting data by year, and corruption/rewrite. Our edge: a real queryable ACID store with unlimited hot history and an AI layer that can read it.

## 2. Datastore: PostgreSQL 16
Chosen for ACID double-entry integrity, exact `NUMERIC(19,4)` money, deferred constraint triggers, Row-Level Security, `ltree` hierarchies, declarative partitioning, logical replication, and JSONB for the extensibility layer. No license cost, ubiquitous auditor familiarity. Rejected MySQL (weaker RLS/partitioning/deferred constraints), document stores (no cross-doc ACID for balanced postings), and dedicated ledger DBs like QLDB/immudb (lock-in, weak relational reporting — we get tamper-evidence cheaply with an in-table hash chain).

## 3. Tenancy
**Shared database, shared schema, mandatory `company_id`** on every business row, enforced by **RLS default-deny** with a per-connection GUC `app.current_company_id`. Company (Tally's numbered folder) == tenant. Group Company == a parent tenant referencing member `company_id`s for consolidation with no data copy. Hot tables (`voucher`, `voucher_version`, `posting`, `stock_movement`) are partitioned by `company_id` (HASH) and sub-ranged by period. Large tenants can be promoted to dedicated schema/DB later (enterprise tier). Rejected schema-per-tenant and DB-per-tenant: unmanageable at millions of micro-SMBs.

## 4. Immutability: append-only versioning, not event-sourcing
Every Create/Alter/Cancel/Delete writes a new `voucher_version`; **postings are immutable children of a version**. The live voucher is the latest non-delete version, tracked by a `current_version_id` pointer on the header (so the hot path never scans history). This delivers Tally's compare-two-versions natively, point-in-time financials, and — with a per-row SHA-256 `prev_hash`/`row_hash` chain — tamper-evidence. Master edits (ledger/group/stock item) use the same version pattern. This satisfies Rule 11(g) **by construction**, with no on/off toggle and no two-binary kludge.

Rejected: full event-sourcing (eventual consistency breaks a Trial Balance that must tie instantly; harder to audit) and mutable-rows-plus-audit-table (in-place UPDATE of accounting numbers is exactly what the regulation distrusts).

## 5. Core schema (abridged)
```
company(id PK, name, parent_company_id NULL, fy_start_date, integrate_inv bool, base_currency)

account_group(id PK, company_id, name, parent_id NULL, path ltree,
  nature enum(ASSET,LIABILITY,INCOME,EXPENSE), is_pl bool,
  behaves_as_subledger bool, nett_balances bool, used_for_calc bool,
  is_system bool)                       -- 28 seeded, is_system=true non-deletable

ledger(id PK, company_id, name, group_id FK->account_group,
  opening_balance NUMERIC(19,4), opening_dc char(1),
  maintain_bill_by_bill bool, gstin, pan, credit_days,
  is_system bool, external_id)          -- Cash & P&L A/c seeded non-deletable

voucher_type(id PK, company_id, name, parent_class enum, numbering_method enum,
  prefix, suffix, width, restart_rule, is_system bool)

voucher(id PK, company_id, voucher_type_id, current_version_id, external_id)

voucher_version(id PK, company_id, voucher_id, version_no,
  action enum(CREATE,ALTER,CANCEL,DELETE), voucher_no, voucher_date,
  narration, created_by, created_at, prev_hash, row_hash)  -- APPEND ONLY

posting(id PK, company_id, voucher_version_id FK, ledger_id,
  dc char(1) check in('D','C'), amount NUMERIC(19,4))       -- IMMUTABLE
  -- deferred trigger: sum(D)=sum(C) per voucher_version_id

bill(id PK, company_id, ledger_id, bill_ref, due_date, outstanding NUMERIC(19,4))
bill_allocation(id PK, posting_id FK, bill_id NULL,
  ref_type enum(NEW,AGST,ADVANCE,ON_ACCOUNT), amount NUMERIC(19,4))

cost_category(id PK, company_id, name, allocate_revenue bool)
cost_centre(id PK, company_id, name, parent_id NULL, path ltree, category_id)
cost_allocation(id PK, posting_id FK, cost_centre_id, cost_category_id,
  amount NUMERIC(19,4))                 -- parallel allocation across categories

ledger_balance(company_id, ledger_id, period, debit NUMERIC, credit NUMERIC,
  PK(company_id,ledger_id,period))      -- incrementally maintained
```
Inventory:
```
stock_group(id, company_id, parent_id, path ltree, add_quantities bool)
stock_category(id, company_id, parent_id, path ltree)          -- orthogonal
unit(id, company_id, symbol, type enum(SIMPLE,COMPOUND), base_unit, conv_factor)
stock_item(id, company_id, name, stock_group_id, category_id, unit_id,
  alt_unit_id, conv_factor, costing_method enum, market_method enum,
  hsn, gst_rate, external_id)
godown(id, company_id, parent_id, path ltree)
bom(id, company_id, item_id, name, output_qty)
bom_component(id, bom_id, item_id, godown_id, qty,
  type enum(COMPONENT,CO_PRODUCT,BY_PRODUCT,SCRAP), rate_pct)
stock_movement(id, company_id, voucher_version_id, item_id, godown_id,
  batch_id NULL, qty NUMERIC(19,4), rate NUMERIC(19,4),
  direction char(1))                    -- IMMUTABLE movement ledger
batch(id, company_id, item_id, batch_no, mfg_date, expiry_date)
```

## 6. Real-time Trial Balance
`ledger_balance` is updated incrementally **inside the same transaction** that commits a voucher version's postings (trigger or service). Trial Balance, Balance Sheet and P&L are then `GROUP BY` roll-ups up the `account_group` `ltree` — O(groups), not O(postings). This is the headline advantage over Tally: sub-second statements at millions of vouchers with no split-by-year. Because postings are immutable, an **async reconciliation job recomputes balances from postings** and alarms on any drift (defence in depth). Drill-down maps Balance Sheet group -> ledger -> `posting` -> `voucher_version`, mirroring Tally's "no report is a dead end".

## 7. Double-entry enforcement
A `DEFERRABLE INITIALLY DEFERRED` constraint trigger checks `sum(debit)=sum(credit)` per `voucher_version_id` at commit, so multi-line vouchers (double-entry mode) and single-entry mode both validate atomically. Bill allocations must sum to their posting amount; cost allocations validated per category. Single Item-Invoice save posts accounting + inventory movement in **one transaction** so an Item Invoice is atomic — impossible to leave stock and books out of sync.

## 8. Migration & extensibility hooks
Tally GUIDs land in `external_id` for idempotent re-import via XML/ODBC; every Tally object maps 1:1 to a master above. TDL-replacement custom fields live as validated JSONB on masters/vouchers; custom reports read the same `ledger_balance`/`posting` tables. The AI layer runs entirely on read replicas over this schema.

## 9. Key risks
Valuation algorithms (FIFO Perpetual, Monthly Avg, LIFO) must match Tally byte-for-byte — needs a conformance suite. The 28-vs-30 group ambiguity must be resolved against a clean install before freezing the seed. Incremental balance drift, RLS gaps, and hot-ledger contention are the operational watch-items, each with the mitigations noted in the decisions.

</details>

**Open risks:** FIFO Perpetual / Monthly Avg / LIFO valuation algorithms must match Tally's output byte-for-byte or CAs reject closing stock — needs a conformance suite run against real Tally exports.; The canonical group count (28 vs 30) and the Reserves & Surplus / Bank OCC ambiguity must be resolved against a clean TallyPrime install before freezing the seed catalog.; Incremental ledger_balance maintenance risks drift under concurrency/bugs; the async recompute-from-postings reconciliation job is mandatory, not optional.; RLS default-deny must be verified on every table by automated tests; a single missing policy is a cross-tenant breach.; Hot-ledger row contention (Cash/Bank) under high concurrent voucher entry needs load testing; may require per-period balance rows or delta accumulation.; Voucher version + inventory movement tables grow without bound (immutability is legally required); needs a partition-aging/cold-storage strategy that keeps history queryable but cheap.; Tally allows user-created primary groups and custom voucher types; the schema must permit these without breaking the seeded system rows.

---

## B.4 Offline-First & Multi-Location Sync Architecture

**Recommendation.** Adopt a per-tenant, append-only event log (event sourcing) as the single source of truth, replicated everywhere, instead of CRDTs or change-data-capture. Each client (offline desktop, web, mobile) holds a local SQLite-backed replica and writes immutable domain events tagged with a HLC (hybrid logical clock) and a node-partitioned ULID; the cloud is the authoritative sequencer that assigns a per-tenant total order. Accounting documents (vouchers) are modeled as immutable, whole-document facts: a voucher is never mutated in place — alterations and deletions are new events that supersede prior versions, which both eliminates field-level merge conflicts (the document is the conflict unit) and gives the MCA Edit Log for free. Conflicts that remain are deterministically resolved by (tenant-order, HLC, node-id) and, where two writers touched the same document, the loser is not destroyed (unlike Tally) but parked in a surfaced reconciliation queue. Voucher display numbers are decoupled from identity: identity is the ULID; the human-facing series number (INV/2026-27/0001) is assigned by a per-(tenant, voucher-type, series, FY) server-side monotonic allocator at commit time, with an offline provisional-number scheme, so the cross-branch duplicate-number hard-fail that plagues Tally cannot occur. All three client types converge on the same log and the same projection engine, so a voucher entered offline on mobile is byte-identical to one entered on the keyboard-driven desktop.

**Chosen stack:** `TypeScript across all clients and the sync server (shared domain + projection code, single source of truth for voucher semantics)`, `Cloud authority service in Go for the sequencer/commit path (low-latency, predictable GC, easy concurrency) — Go service owns ordering + numbering; TypeScript owns domain logic compiled to WASM and reused on clients`, `PostgreSQL 16 as the per-tenant authoritative event store + projections (one logical DB per tenant via schema-per-tenant, partitioned append-only events table; row-level security)`, `SQLite (better-sqlite3 on desktop via Electron, expo-sqlite/op-sqlite on mobile) as the embedded local replica + local projections on every offline client`, `Electron + the existing web UI bundle for the offline-first desktop client (keystroke-identical TallyPrime UI runs the same React/TS code as web; SQLite replica underneath)`, `Hybrid Logical Clocks (HLC) for causality + wall-clock-ish ordering; ULID/UUIDv7 with embedded 16-bit node id for globally unique, sortable, node-partitioned event and voucher identity`, `WebSocket (cloud) with a binary length-delimited Protobuf frame protocol for the live sync channel; HTTP/2 long-poll fallback for restrictive networks; resumable by (tenant, log-position) cursor`, `NATS JetStream as the internal per-tenant ordered subject/stream that fans committed events out to connected replicas and to the AI/projection workers`, `Redis (per-tenant Lua-scripted counters) as the hot path for the monotonic display-number allocator, durably backed by a Postgres sequence-of-record per (tenant, type, series, FY)`, `BLAKE3 hash-chaining over the per-node event sequence for tamper-evident audit (MCA Rule 11(g)); chain heads co-signed by the cloud at commit`

**Key decisions:**

#### Core sync model: event log vs CRDT vs change-data-capture → **Per-tenant append-only domain-event log (event sourcing) with a cloud sequencer assigning total order; clients hold the log + materialized projections locally.**
Accounting is intrinsically an append-only ledger — Tally itself is a transaction log (TranMgr) plus pre-aggregated projections (Aggr.1800), and the MCA Edit Log mandate already requires that every Create/Alter/Delete be retained immutably. An event log matches the domain natively: the audit trail is not a feature bolted on, it IS the storage. It gives deterministic replay, point-in-time reconstruction, trivial 'continuous two-way sync alongside live Tally during trial', and a clean substrate for server-side AI over the full history. CDC was rejected because it leaks mutable-row semantics and cannot represent the audit intent (a CDC 'UPDATE' loses the business meaning of 'Altered due to renumbering'). General CRDTs were rejected as the top-level model because accounting forbids the silent automatic merges CRDTs perform — you must never auto-merge two edits to a voucher's amount into a 'converged' value that no human authored; debits-equal-credits and 'this is the invoice we filed in GSTR-1' are hard invariants that a generic LWW-register or counter CRDT will silently violate.
- *Alternatives rejected:* State-based CRDTs (whole-document LWW) for everything; Operational Transform on voucher fields; Logical-replication CDC from a central Postgres to branch read replicas; Tally-style directional overwrite with CID/AID watermarks
- *Risks:* Event-sourcing has a learning curve; schema/event-versioning discipline is mandatory or replays drift; Projections must be deterministic and pure across desktop SQLite and cloud Postgres — any divergence in rounding/locale/tax math between the Go cloud and TS client breaks convergence; mitigated by compiling the one TS domain core to WASM and running it identically on client and server

#### Conflict unit and conflict resolution for accounting data → **Treat the whole voucher (document) as the atomic conflict unit and model it as immutable-versioned facts. Two offline edits to the same voucher do NOT field-merge; the deterministic winner by (cloud total-order, then HLC, then node-id) becomes the current version, and the losing version is preserved as a superseded fact AND raised into a human reconciliation queue. Masters (ledgers, stock items, GST settings) use scoped, semantically-safe CRDTs (LWW-register per field for descriptive fields; add-only set for child collections) because master merges are safe and desirable.**
Field-level merge of financial documents is unacceptable: merging two amount edits produces a number nobody authored and can break debit=credit or the figure already filed with GSTN. So for vouchers we choose determinism + never-destroy + surface, which is strictly better than Tally's silent sender-wins overwrite (Tally destroys the loser with no warning). Because vouchers are append-only facts, 'resolution' is just choosing which version is current — the loser is never lost, satisfying the Edit Log immutability constraint automatically (the overwrite is itself a logged event on the overwritten node). Masters genuinely benefit from CRDT auto-merge (two branches adding different child ledgers should both survive), so we apply CRDTs surgically only where merges are mathematically safe, not globally.
- *Alternatives rejected:* Global LWW (last-writer-wins) across all entities incl. vouchers — rejected: silently loses financial edits, same defect as Tally; Pessimistic distributed locking of vouchers — rejected: kills offline-first; you cannot acquire a lock while disconnected; Three-way auto-merge of voucher line items — rejected: can fabricate totals and violate tax/GST invariants
- *Risks:* Reconciliation queue must be impossible to ignore: an unresolved superseded voucher that was already pulled into a filed GSTR-1 is a compliance event — needs hard blocking + CA notification; Defining the document boundary precisely (does a voucher 'own' its bill-wise allocations and inventory links?) is subtle; getting it wrong reintroduces field conflicts

#### Voucher numbering under concurrency (the Tally hard-fail) → **Decouple identity from display number. Identity = node-partitioned ULID assigned at creation offline. The human series number (e.g. INV/2026-27/0001) is allocated by a server-side monotonic per-(tenant, voucher type, series, financial year) counter at COMMIT time. Offline-created vouchers carry a provisional placeholder ('INV/2026-27/PROV-<nodeshort>-<localseq>') that is replaced atomically by the authoritative number when the event reaches the sequencer; final numbering is by cloud-commit order, ties broken by HLC.**
Tally's collisions ('404: Voucher number 1 already exists', same-GUID-blocks-everything) happen precisely because the display number IS the key and numbering is decentralized. Separating a globally-unique sortable identity from a human-friendly, server-allocated display number makes collisions structurally impossible while preserving the exact gapless, FY-restarting, prefixed numbering CAs expect. Allocating at commit (not at offline creation) guarantees no gaps in the authoritative series even though branches create offline in parallel — the sequencer is the single point that hands out numbers, backed by a Postgres sequence-of-record with a Redis hot cache.
- *Alternatives rejected:* Per-branch number prefixes/series (Tally's recommended workaround) — kept as an OPTIONAL config for businesses that want branch-segmented books, but not the default because it pollutes the head-office series; Client-side optimistic numbering with later renumber — rejected as default: renumbering on collision is exactly the painful Tally 'Altered due to renumbering' churn and breaks already-printed/shared invoices; Pure UUID with no human number — rejected: CAs and GST returns require human series numbers
- *Risks:* Provisional→final number swap must be idempotent and must never reorder a number that was already legally issued/printed; once a voucher is e-invoiced (IRN generated) or shared, its number must freeze even if it loses commit-order race — so IRN generation must pin the number; Long offline periods accumulate provisional numbers; if two branches both go offline for a week the commit-order assignment can surprise users — needs clear UI showing 'number pending sync'

#### Client convergence: how offline desktop, web, and mobile agree → **All three clients run the identical TypeScript domain core (validation, tax/GST computation, double-entry posting, projection reducers) — compiled to WASM for reuse — over a local SQLite replica, and converge by replaying the same totally-ordered event log delivered over a resumable WebSocket cursor. Web is 'thin replica' (small/no local store, mostly live) but uses the same reducers; desktop and mobile keep a full local replica for true offline. The cloud runs the same reducers in Go-hosted WASM to produce authoritative projections and to validate every inbound event before sequencing.**
Convergence is guaranteed only if every node computes projections with bit-identical logic; the #1 event-sourcing failure mode is divergent reducers. One shared core eliminates that class of bug and means a voucher entered on mobile offline is semantically indistinguishable from one keyed on the desktop — directly delivering 'owner bills on mobile, accountant finalizes in the Tally-identical desktop view.' Local SQLite gives the instant offline desktop performance that beats RDP latency. Resumable cursor sync replaces Tally's fragile same-release/On-demand-7-day-retention model with continuous catch-up.
- *Alternatives rejected:* Separate native data layers per platform (Core Data on iOS, Room on Android, IndexedDB on web) — rejected: triples the projection-divergence risk and the maintenance cost; Server-rendered thin clients only (no local replica) — rejected: that is the RDP model we are explicitly killing; no true offline; PowerSync/ElectricSQL off-the-shelf sync — strong contenders for the replica transport, but they impose LWW/CRDT row semantics that conflict with our document-level financial conflict policy; we reuse their ideas (SQLite replica + change stream) but keep our own event/commit protocol
- *Risks:* WASM domain core must stay perfectly version-locked across clients; a client on an old core could write events the cloud rejects — needs a min-core-version gate and forced background update; Mobile/web storage limits and battery/network constraints mean full replica is fine on desktop but mobile may need partial/scoped replication (e.g. only the current FY + this branch), reintroducing a 'split company' style decision

#### Tamper-evident audit trail across nodes (MCA Rule 11(g)) → **Hash-chain every node's local event sequence with BLAKE3 (each event includes hash of prior event on that node); on commit the cloud co-signs the accepted chain head, producing a per-tenant merged, cross-node-verifiable ledger. Every cross-node supersede/overwrite is itself a logged, hash-chained 'Altered' event on the overwritten node.**
The MCA mandate requires immutable Create/Alter/Delete history that survives merges; event sourcing already retains everything, and hash-chaining makes it cryptographically tamper-evident — strictly stronger than Tally's view-only Edit Log, and it lets us offer 'one-click auditor-attestation export' and AI anomaly detection (back-dated edits, weekend bulk deletes) over a provable log.
- *Alternatives rejected:* Rely on Postgres immutability + RLS only — rejected: not tamper-EVIDENT (a DBA could rewrite history undetectably); Full blockchain/Merkle DAG per voucher — rejected: over-engineered for SMB, heavy
- *Risks:* Clock skew and offline reordering complicate a single global chain; we chain per-node then cross-stitch at the cloud rather than forcing one global chain; Key management for the cloud co-signing key is now a critical security asset

**Replication fidelity.** Zero-learning-curve is preserved because the sync architecture is entirely below the UI: the desktop client runs the identical keystroke-driven TallyPrime UI (F4–F9 vouchers, Alt+G Go To, Ctrl+A accept, auto-complete pick-lists) over a local SQLite replica, so it feels exactly like local Tally but without the .900 file-lock corruption or RDP latency — the user never sees events, HLCs, or ULIDs. Human-facing voucher numbering replicates Tally's exact behavior (prefixed series, FY restart, gapless, per-voucher-type) via the server allocator, so CAs see INV/2026-27/0001 just as today; the ULID identity is invisible. Data-compat is preserved because the event/projection model maps cleanly onto Tally's own concepts (TranMgr transaction log + Aggr pre-aggregated projections + immutable Edit Log), and the migration importer emits events directly from Tally's XML/ODBC export — enabling true zero-config import and even continuous two-way sync alongside a live Tally install during a trial, with no data risk. The conflict story is a strict superset of Tally: we never silently destroy an edit (Tally does), we never hard-fail a whole sync on a number/GUID collision (Tally does), and the audit trail is cryptographically stronger than Tally's view-only Edit Log while logging the same Create/Alter/Delete events the MCA mandate and CAs expect.

**AI angle.** Because the event log is the source of truth and lives server-side in full, the agentic AI layer reads a complete, queryable, totally-ordered history of every branch instead of a stale post-sync snapshot. This enables: (1) real-time cross-branch cash-flow and receivables-aging that Tally cannot do until a sync cycle completes; (2) anomaly/fraud detection running directly on the hash-chained Edit Log (back-dated edits, round-tripping, weekend bulk deletions, suspicious supersede events from the conflict queue) surfaced to the CA; (3) AI-assisted conflict reconciliation — when two branches edit the same voucher, the model summarizes the semantic diff ('Branch A changed qty 10→12, Branch B changed rate') and recommends a resolution rather than forcing the user to diff raw rows; (4) AI-assisted numbering/series setup ('number my sales invoices INV/2026-27/0001 restarting each year') that generates the allocator config; (5) predictive pre-sequencing hints — the AI can flag a provisional voucher likely to collide with a filed return before it commits. The projection/event substrate is also the ideal feed for OCR auto-capture: an OCR'd invoice becomes a draft event in the same log, validated by the same domain core.

**Interfaces to other domains:**
- Core Accounting Engine: consumes the same TypeScript/WASM domain core (double-entry posting, voucher validation) that this sync layer replays; the engine defines the event schemas (VoucherCreated/Altered/Deleted, MasterUpserted) that this layer transports and orders.
- Data Architecture / multi-tenant store: this layer writes the authoritative per-tenant event store and projections in PostgreSQL; the data team owns schema-per-tenant isolation, partitioning, and RLS. Display-number allocator shares Postgres sequences + Redis with the data layer.
- Compliance/GST: voucher numbers feeding GSTR-1/e-invoice IRN must FREEZE once an IRN is generated — the numbering allocator exposes a 'pin number' hook the compliance module calls at IRN time; the conflict/reconciliation queue must hard-block superseding any voucher already included in a filed return.
- Audit/Security: provides the hash-chained, cloud-co-signed event ledger that the audit-trail and security domains expose as the MCA Edit Log and auditor-attestation export; consumes the auth domain's per-company scopes for which node/user may sequence events.
- AI Insight Layer: exposes the ordered event stream via NATS JetStream subjects so AI workers (anomaly detection, cash-flow forecast, conflict summarization) consume committed events in real time.
- Migration: the Tally XML/ODBC importer emits the same VoucherCreated/MasterUpserted events into the log, so a one-time import and continuous two-way Tally sync share one ingestion path.

<details><summary>Detailed engineering writeup</summary>

## ADR: Offline-First & Multi-Location Sync Architecture

### Context (what Tally actually does)
Tally is a single-process desktop app over a proprietary in-RAM object graph persisted to `.900/.1800` files, with a `.TSF` write-ahead/lock layer (RESEARCH §10). Multi-user is either SMB folder-sharing on port 9000 (corrupts if two machines open the same folder) or the separately-licensed TallyPrime Server. Branch collaboration is **peer-to-peer directional sync**, not a hub DB: incremental by CID (Creation ID) / AID (Alteration ID) watermarks, with **no field-level merge** — `Synchronise Altered Transactions = Yes` makes the sender's voucher *blindly overwrite* the receiver's and "remove any changes," with no warning or queue (§14, §20). Same-GUID and duplicate-voucher-number collisions **hard-fail the entire sync** (§20.3). The MCA Edit Log (Rule 11(g)) requires every Create/Alter/Delete to be retained immutably, and any overwrite must itself be logged (§20.4). The real-world remote-access answer is RDP-on-VPS, which we are killing.

Our mandate: instant offline desktop performance **and** real-time multi-branch collaboration, with conflict semantics that are *correct for accounting* (conflicts are unacceptable) — strictly better than Tally on every axis above.

### Decision summary
A **per-tenant append-only domain-event log** is the source of truth, replicated to every client; a **cloud sequencer** assigns the authoritative per-tenant total order and the human voucher number at commit. Vouchers are **immutable versioned facts** (the document is the conflict unit); masters use **surgical, safe CRDTs**. Identity is a **node-partitioned ULID**, decoupled from the display series number.

### Why event log, not CRDT or CDC
Accounting *is* an append-only ledger; Tally is literally a transaction log (TranMgr) plus pre-aggregated projections (Aggr.1800) plus an immutable Edit Log. Event sourcing makes the audit trail the storage, not a bolt-on, and gives deterministic replay, point-in-time books, and a full server-side history for AI.

- **CDC rejected:** leaks mutable-row semantics; a CDC `UPDATE` cannot express "Altered due to renumbering" — it destroys business intent the MCA mandate requires.
- **Global CRDT rejected:** CRDTs *auto-converge*. For financial documents that is a defect: merging two `amount` edits yields a value no human authored and can break debit=credit or contradict a number already filed in GSTR-1. We must never silently merge money.

### The data model

```
Event (immutable, per tenant):
  event_id       ULID  (16-bit node id embedded)  -- globally unique, sortable
  tenant_id      uuid
  node_id        uint16                            -- desktop/mobile/web/branch
  hlc            (wall_ms, counter, node_id)       -- hybrid logical clock
  prev_hash      blake3                            -- per-node chain
  hash           blake3(payload || prev_hash)
  type           VoucherCreated | VoucherAltered | VoucherDeleted | MasterUpserted | ...
  doc_id         ULID                              -- the voucher/master identity
  doc_version    uint                              -- supersession chain
  payload        canonical-encoded domain fact
  seq            int8  (NULL until cloud-sequenced) -- authoritative tenant total order
```

Voucher = `(doc_id, doc_version)`. An "alteration" is a new event with a higher `doc_version` referencing the same `doc_id`. The **current** version is the one with the highest `(seq, hlc, node_id)`. Nothing is ever mutated; the Edit Log is the version chain.

### Conflict resolution (the crux)

```
on two events E1,E2 with same doc_id, neither causally after the other:
  winner = max by (seq, then hlc, then node_id)      -- deterministic, identical on every node
  loser  -> retained as superseded fact (Edit Log)    -- NOT destroyed (Tally destroys it)
         -> raised into reconciliation queue           -- surfaced (Tally surfaces nothing)
  if loser was already in a FILED return or has an IRN:
         -> HARD BLOCK + notify CA                      -- compliance gate
```

Determinism means every replica reaches the same current version with no coordination. "Resolution" is choosing the current version, never fabricating a merged amount. **Masters** (ledgers, stock items) are different: two branches adding different child ledgers should both survive, so masters use a per-field LWW-register + add-only set for child collections — CRDTs applied *only where merges are mathematically safe*.

### Voucher numbering under concurrency

Tally collides because the display number *is* the key and numbering is decentralized. We split them:

```
identity  = ULID (assigned offline at creation, never collides)
display # = server-allocated at COMMIT:
            counter[tenant, voucher_type, series, FY]  (Postgres sequence-of-record + Redis Lua cache)
offline   -> provisional "INV/2026-27/PROV-<node>-<localseq>"
commit    -> sequencer swaps provisional -> authoritative gapless number, by commit order
freeze    -> once IRN generated / invoice shared, number is PINNED and cannot be re-raced
```

Result: gapless, FY-restarting, prefixed numbers exactly as CAs expect, but cross-branch duplicate-number hard-fails are structurally impossible. Per-branch series remain an *optional* config for businesses that want segmented books.

### Client convergence

All clients run **one TypeScript domain core compiled to WASM** (validation, GST/tax math, double-entry posting, projection reducers) over a **local SQLite replica**; the cloud runs the same WASM core in Go to validate and project authoritatively. This kills the #1 event-sourcing failure mode — divergent reducers. Sync is a **resumable WebSocket cursor** keyed on `(tenant, seq)`; binary Protobuf frames; HTTP/2 long-poll fallback. Desktop and mobile keep full (or FY/branch-scoped) replicas for true offline; web is a thin replica using the same reducers.

```
client write:  append event locally -> apply reducer -> render instantly (offline-OK)
               -> ship to cloud when connected
cloud:         validate (WASM core) -> assign seq -> allocate number -> hash-chain co-sign
               -> JetStream fan-out to all replicas + AI workers
replica catch-up: pull events where seq > local_cursor; reducers are pure -> converge
```

### Audit / tamper-evidence
Per-node BLAKE3 hash chain; cloud co-signs accepted chain heads -> cross-node-verifiable per-tenant ledger. Every supersede is a logged event. This is strictly stronger than Tally's view-only Edit Log and directly serves Rule 11(g) + Section 128(5) 8-year retention, plus AI anomaly detection over a provable log.

### Topology
```
[Desktop Electron]  [Mobile RN]  [Web]      <- same WASM core, SQLite/IndexedDB replica
        \                |          /
         \---- WebSocket (resumable cursor) ----/
                        |
              [Go Sequencer (HA, Raft)]  -- per-tenant total order + numbering
                        |
        [PostgreSQL 16 event store + projections]  +  [Redis counters]
                        |
              [NATS JetStream]  -> AI / projection / compliance workers
```

### Sequencing / availability
The sequencer is the single writer for ordering + numbering per tenant and must be HA (leader-elected). Critically, **offline entry never blocks** on it — only *final* numbering and total-order assignment wait for connectivity, which is exactly the desired offline-first behavior.

### Why this is boring-and-proven
Event sourcing, Postgres, SQLite replicas, HLCs, ULIDs, hash chains, and WebSocket cursors are all mature. The only opinionated novelty — document-level (not field-level) conflict units with a surfaced reconciliation queue — is *forced by accounting correctness*, and it is simpler than CRDT merge, not more exotic.

</details>

**Open risks:** Projection determinism across Go (cloud) and SQLite (clients): any divergence in decimal rounding, currency conversion, or GST math breaks convergence silently. Mitigation is one WASM domain core everywhere, but the Go↔WASM boundary and float/decimal handling need a hard contract and golden-vector tests.; Long-lived offline + provisional numbering: a branch offline for days accumulates provisional numbers; commit-order assignment on reconnect can surprise users and (worst case) clash with compliance freezes if a number was externally referenced. Needs strong UI signaling and a freeze-on-external-reference rule.; Conflict-queue compliance hazard: a superseded voucher that was already pulled into a filed GSTR-1 or e-invoiced is a regulatory event, not a UI nicety — the blocking + CA-notification path must be bulletproof.; Document-boundary definition (does a voucher own its bill-wise allocations, inventory links, cost-centre splits?) directly determines whether field-level conflicts reappear; mis-scoping is a latent correctness bug.; Mobile partial replication: full-replica convergence proofs assume every node sees the whole log; scoped mobile replicas (current FY / one branch) reintroduce a 'split data' decision and partial-projection correctness questions.; Sequencer as availability bottleneck: the cloud total-order sequencer is the single writer for ordering + numbering per tenant; it must be HA (Raft/leader-election) or a tenant outage blocks all final numbering even though offline entry continues.; Min-core-version gating: clients on stale WASM cores could emit events the cloud rejects or compute wrong projections; forced background update + version floor is mandatory and has UX cost.

---

## B.5 Compliance Engine (GST / TDS / TCS / e-Invoice / e-Way Bill / MCA Audit Trail)

**Recommendation.** Build the compliance engine as a separate, versioned microservice — the "Statutory Core" — that is strictly decoupled from the voucher/ledger system of record. Its heart is a declarative, hot-loadable RuleSet: every statutory rule (CGST/SGST/IGST derivation from place-of-supply + registration type, Nature-of-Transaction classification, HSN/SAC→rate mapping, TDS/TCS sections, return-table routing, e-Invoice/e-Way-Bill thresholds) is data, not code, carried in cryptographically-signed, effective-dated RuleSet bundles that the cloud pushes to every tenant automatically — eliminating Tally's TSS upgrade-tax entirely. Tax computation is a deterministic, pure, fully-replayable function (idempotent given a voucher snapshot + RuleSet version) so any historical filing can be re-derived bit-for-bit for audit. Government integrations (IRP/IRN, e-Way Bill, GSTN GSTR-1/3B/CMP-08/IMS) run through licensed GSP/ASP partners behind a single internal "Statutory Gateway" abstraction with a pluggable provider interface, durable idempotent outbox, and circuit breakers — never directly against NIC/GSTN from app code. The MCA Rule 11(g) Edit Log is implemented as an always-on, append-only, per-tenant hash-chained audit ledger that is structurally impossible to disable or mutate, with Merkle-anchored daily checkpoints for tamper-evidence and one-click auditor attestation export, satisfying the 8-year Section 128(5) retention requirement. Stack: Go services for the deterministic compute/gateway core, Postgres (Citus-sharded, multi-tenant) for state, a CEL/JSON-Logic-based rule interpreter for RuleSets, Temporal for the long-running filing/IRN/e-way orchestrations, and Kafka for the audit and reconciliation event streams.

**Chosen stack:** `Go 1.22 for the Statutory Core service (deterministic tax compute, rule interpreter, gateway orchestration) — chosen for predictable performance, strong concurrency, and easy single-binary determinism`, `PostgreSQL 16 (Citus extension) as multi-tenant store for compliance state, filings, audit ledger, reconciliation; row-level-security per tenant_id`, `CEL (Google Common Expression Language) + a typed JSON-Logic DSL as the RuleSet expression language, interpreted (never eval'd as code) — rules are signed data bundles`, `Temporal (self-hosted) for durable, resumable, idempotent workflows: IRN generation, e-Way Bill, GSTR-1/3B/CMP-08 filing, 6-hour GSP session lifecycle, IMS deemed-accept timers`, `Apache Kafka for the audit event stream, reconciliation events, and compliance domain events (immutable, ordered per tenant)`, `ClearTax / Masters India as primary licensed GSP/ASP (both IRP + GSTN + EWB); NIC IRP direct as secondary for IRN where eligible — abstracted behind a ProviderAdapter interface`, `AWS KMS + HSM-backed signing keys (Mumbai ap-south-1 region for data residency) for RuleSet bundle signing, audit-chain anchoring, and DSC custody`, `OpenTelemetry + Prometheus/Grafana for SLO monitoring of government-API latency, rejection rates, and filing success`, `Sigstore-style detached signatures (Ed25519) on every RuleSet bundle; bundles distributed via signed S3 objects + CDN with version pinning per tenant`, `RDS/Postgres logical replication to an append-only WORM-mode S3 (Object Lock, governance/compliance mode) for the 8-year audit archive`

**Key decisions:**

#### How statutory rules are represented and updated as law changes → **Declarative, effective-dated, cryptographically-signed RuleSet bundles interpreted by a CEL/JSON-Logic engine, hot-pushed from cloud to every tenant — no app redeploy, no user reinstall, no paid upgrade**
This is THE strategic lever. Tally monetizes statutory change via TSS renewals and paid releases; SMBs upgrade only because a new mandate forces it. By making rules pure data with effective dates and automatic signed delivery, a GST council rate change, a new TDS section like 194Q, or an e-invoice threshold drop becomes a config push that lands the same day with zero user action and zero charge. Effective-dating lets the same engine compute a Mar-2025 voucher under Mar-2025 rules and an Apr-2025 voucher under new rules within one filing period. Interpreting (not compiling/eval'ing) signed bundles keeps the attack surface tiny and makes every computation auditable and replayable.
- *Alternatives rejected:* Hardcode rules in service code and ship via app releases (Tally's model) — rejected: recreates the upgrade-tax, slow, couples law changes to deploy cadence; Embed a full scripting language (Lua/JS) for rules — rejected: arbitrary code execution is a security and determinism hazard; auditors cannot reason about it; harder to prove replay-equivalence; Use a commercial rules engine (Drools) — rejected: JVM heavyweight, opaque to auditors, overkill; CEL/JSON-Logic is inspectable and signable
- *Risks:* A bad RuleSet push could miscompute tax for all tenants simultaneously — mitigate with staged canary rollout, golden-transaction regression suite per bundle, and instant signed rollback to prior version; Rule expressiveness ceiling: some statutory edge cases (works contracts, mixed/composite supply) may exceed JSON-Logic — mitigate with a small set of named, versioned, signed 'native rule plugins' for the long tail, still effective-dated

#### Coupling between tax computation and the ledger/voucher system of record → **Strict decoupling: the Statutory Core is a separate service; tax is a pure deterministic function of (voucher snapshot + RuleSet version + masters snapshot), persisted alongside the voucher so any filing is bit-for-bit replayable**
Compliance must be re-derivable for audit and for late RuleSet corrections. Storing the RuleSet version stamped on each computed voucher means we can answer 'why did this invoice get IGST?' years later and reproduce it. Decoupling also lets compliance evolve and scale independently of the high-throughput voucher path.
- *Alternatives rejected:* Compute tax inline inside voucher-save in the core accounting service (Tally's tight coupling) — rejected: makes replay and rule-versioning impossible, couples deploy cadence, and bloats the hot write path
- *Risks:* Latency: voucher entry must feel instant (Tally is keyboard-fast). Mitigate by running the rule interpreter in-process as an embedded library for the synchronous derive call (sub-millisecond), while the service boundary is used for filings/reconciliation/async work

#### GSP/IRP/GSTN integration strategy → **Single internal Statutory Gateway with a pluggable ProviderAdapter interface; route through licensed GSP/ASP partners (ClearTax + Masters India) as primary, NIC IRP direct as secondary; durable idempotent outbox + Temporal workflows + circuit breakers**
GSP licensing, security audits, and uptime SLAs are a specialized, regulated capability — building our own ASP from scratch delays MVP and adds regulatory burden. Abstracting behind ProviderAdapter avoids lock-in (we can swap or multi-home providers, and become our own GSP later without changing call sites). Idempotent outbox + IRN/ARN dedup keys prevent the classic double-IRN/double-filing disaster on retries. Temporal makes the 6-hour GSP session, OTP/EVC/DSC handshake, and resumable filing first-class and crash-safe.
- *Alternatives rejected:* Become a registered GSP/ASP ourselves on day one — rejected for MVP: long GSTN onboarding, audits, capital; revisit post-traction as cost/control play; Call NIC IRP/EWB and GSTN portal APIs directly from app code with no gateway — rejected: brittle, no idempotency, no provider failover, scatters credentials and retry logic; Single-GSP dependency — rejected: government-side outages and per-GSP rate limits make multi-homing a resilience requirement
- *Risks:* Provider outage on filing-deadline days (10th/11th/13th/14th/20th) — mitigate with multi-provider failover, queued offline-JSON fallback export, and deadline-aware backpressure; GSP contractual data-handling — mitigate with DPA, ap-south-1 residency, field-level minimization, and our own audit log of every payload sent

#### MCA Rule 11(g) Edit Log implementation → **Always-on, append-only, per-tenant hash-chained audit ledger (each row hashes prior row's hash); daily Merkle-root checkpoints signed by KMS and written to WORM S3 (Object Lock); no disable switch exists in the schema or API**
Tally ships an awkward two-build split (optional vs always-on) and its log is merely view-only, recording no machine/IP. We make immutability structural (append-only tables, no UPDATE/DELETE grants, RLS) and tamper-evidence cryptographic (hash chain + signed Merkle anchors), beating Tally on the exact assertion auditors must sign: configurable=N/A (always on), enabled all year (provable), covered all transactions (every write emits a log row via outbox), preserved 8 years (WORM). We additionally capture machine/device/IP/session/geo that Tally omits, enabling AI fraud detection (back-dated edits, after-hours deletions).
- *Alternatives rejected:* Toggleable edit log like standard TallyPrime — rejected: defeats the compliance guarantee and forces auditors to verify it was on all year; Generic application logging — rejected: not tamper-evident, mutable by admins/DBAs, no before/after field capture, no cryptographic proof; Full blockchain ledger — rejected: operational overhead and latency unjustified; a per-tenant hash chain with periodic anchoring gives equivalent tamper-evidence cheaply
- *Risks:* Write amplification on every voucher/master change — mitigate with async outbox to the audit stream and batched chain-append, accepting eventual (seconds) consistency for the chain while the source write is synchronous; Cross-node/branch sync must record every overwrite as an Alteration (per research section 20) — mitigate by making sync writes go through the same audit append path, never silent replace

#### GSTR-2A/2B reconciliation and IMS handling → **Continuous reconciliation: scheduled GSTN pulls of 2A/2B/IMS via the gateway, matched against purchase vouchers by a deterministic matcher (GSTIN+docno+amount+period, with tolerance rules) plus an AI probable-match scorer; IMS accept/reject/pending modeled as first-class state with a Temporal timer enforcing the 14th deemed-accept and 20th cutoff**
Tally does month-end batch download; continuous recon surfaces 'ITC at risk: supplier hasn't filed' early and lets us project GSTR-2B/3B ITC impact before the 14th — a concrete differentiator. Modeling IMS deadlines as durable timers prevents missed deemed-accept windows.
- *Alternatives rejected:* Batch-only month-end reconciliation (Tally parity) — rejected: misses the proactive-ITC differentiator the strategy calls for
- *Risks:* AI match false positives could wrongly accept ITC — mitigate with human-in-the-loop approval and confidence thresholds; AI only suggests, never auto-files

#### Validation strategy for government payloads → **Client/server-side schema validation against the NIC e-Invoice (12 sections/133 fields, cardinality, forbidden " and \ chars, decimal precision, 1000-line cap) and EWB/GSTR schemas before any submission, plus a pre-file 'Uncertain Transactions' AI pre-validator that explains each defect and proposes a one-click fix**
Eliminates IRP/portal rejection round-trips and replaces Tally's opaque 'Uncertain Transactions' bucket with reasoned, fixable guidance — directly from the opportunity map.
- *Alternatives rejected:* Submit-and-handle-rejection — rejected: wastes deadline-day time and government rate-limit budget
- *Risks:* Schema drift when NIC/GSTN revises versions — mitigate by treating these schemas as versioned, effective-dated RuleSet artifacts too, validated by the same signed-bundle pipeline

**Replication fidelity.** Zero-learning-curve is preserved by replicating Tally's exact compliance UX and keystroke grammar on top of the modern engine: the F11 GST setup, the six-level rate/HSN lookup hierarchy (Ledger→Group→Item→StockGroup→Company precedence, with the alternate ordering), GST Classifications with effective-dated bulk rate setup, the GSTR-1/3B/CMP-08 reports with the Included / Not-relevant / Uncertain-Transactions buckets and Ctrl+H Return-vs-Nature view toggle, and the Alt+Z (Exchange) menu with the identical C (Continue) / E (EVC) / D (DSC) / S (Send) / V (View) / T (Restart) filing keys — so the user and their CA see the same screens and press the same keys. Data compatibility is preserved on two fronts: (a) we emit byte-identical NIC/GSTN JSON (e-Invoice INV-01, EWB, GSTR offline-tool schemas) so filings are never rejected and the CA's portal workflow is unchanged; (b) every computed tax result stores its RuleSet version, so a migrated Tally period can be re-derived and reconciled against Tally's original output to prove parity. The Nature-of-Transaction codes mirror Tally's enumerated set exactly, and migrated vouchers retain their original heads — re-derivation only flags, never silently rewrites, honoring the lossless-migration constraint.

**AI angle.** AI is additive and human-gated, never in the deterministic tax path. Four high-value insertions: (1) replace Tally's opaque "Uncertain Transactions" bucket with an LLM pre-validator that explains in plain language why a voucher cannot be classified (missing HSN, wrong place-of-supply, missing RCM flag) and proposes a one-click fix; (2) place-of-supply / Nature-of-Transaction inference from free-text or voice invoice entry so non-accountants never pick a NoT or worry about CGST/SGST vs IGST; (3) AI-scored GSTR-2A/2B and IMS reconciliation with probable-match confidence and an ITC-impact projection before the 14th deemed-accept deadline, plus auto-drafted vendor follow-ups for "supplier hasn't filed" ITC-at-risk cases; (4) anomaly/fraud detection over the cryptographic Edit Log (back-dated edits, after-hours deletions, repeated alterations of the same voucher) — turning a passive compliance log into proactive insight. Critically, deterministic CEL RuleSets remain the source of truth: the LLM suggests, the signed rules compute, and a human approves before anything is filed.

**Interfaces to other domains:**
- Accounting Core: calls the embedded TaxDeriveEngine synchronously at voucher save (sub-ms); receives tax heads, amounts, NoT, RuleSet version stamp written back onto the voucher
- Audit/Edit-Log: every voucher and master mutation in any domain emits an AuditOutbox event consumed by the hash-chain appender — the compliance engine owns the audit ledger but all domains feed it
- Migration/Tally-import domain: imported Tally vouchers carry their original tax heads; engine re-derives under the period-correct RuleSet and flags discrepancies without overwriting (preserving zero-data-loss promise)
- Sync/multi-branch domain: all cross-node writes route through the audit append path so overwrites are recorded as Alterations, never silent replaces
- AI insight layer: consumes reconciliation events and the Edit Log stream over Kafka; posts suggestions back through human-in-the-loop approval APIs
- Extensibility (TDL-replacement) layer: third-party rule/report extensions may read compliance state via a read API but cannot mutate the audit ledger or the signed RuleSets
- Identity/security: pulls user, device, IP, session, geo to stamp every audit row (data Tally never captures)
- Notification domain: deadline reminders (10th/11th/13th/14th/20th), draft-saved reminders, ITC-at-risk alerts

<details><summary>Detailed engineering writeup</summary>

## ADR: The Statutory Core — A Pluggable, Always-Current Compliance Engine

### Context and grounding
Per the research dossier (Exec Summary, §7, §19, §22), statutory compliance is *the* killer feature and Tally's primary upgrade lever: SMBs renew TSS and install paid releases almost solely because a new mandate (GST rollout, e-invoice threshold drops, §194Q, B2CL threshold change) forces them to. Tally's tax logic is a six-level lookup hierarchy (Company→Group→Item→Stock Group→Ledger→GST Classification) that auto-derives CGST/SGST vs IGST from place-of-supply + registration type, classifies each voucher by a *Nature of Transaction*, routes it to GSTR-1 tables, and surfaces an *Uncertain Transactions* exception layer before filing. We must match this behavior exactly while beating Tally on three axes: **automatic statutory updates (no upgrade tax), transparent/auditable derivation, and a structurally-undisableable, cryptographically tamper-evident Edit Log.**

### Architecture overview
```
 Accounting Core (vouchers/ledgers)        Statutory Core (this domain)
 ───────────────────────────────           ───────────────────────────────────────────
  voucher save ──(embedded lib call)──▶  TaxDeriveEngine  (pure, deterministic)
        │                                     │ uses RuleSet@version (signed, effective-dated)
        │                                     ▼
        │                               compute result + RuleSet version stamp ──▶ persisted w/ voucher
        ▼                                                     │
  AuditOutbox ──▶ Kafka ──▶ HashChainAppender ──▶ per-tenant audit ledger ──▶ daily Merkle anchor ──▶ WORM S3
                                                              ▲
  Returns/IRN/EWB/IMS  ──▶ Temporal workflows ──▶ Statutory Gateway ──▶ ProviderAdapter ──▶ {ClearTax|MastersIndia|NIC}
```

### 1. RuleSet: rules as signed, effective-dated data
A `RuleSet` is a versioned bundle (`ruleset@2025.04.01-r3`) containing:
- **Rate/HSN tables**: HSN/SAC → {rate, cess, effective_from, effective_to, notification_ref}. We maintain a *dated* HSN→rate map with citation to the GST notification — closing the gap Tally openly disclaims (GSTN APIs don't return rate-for-HSN).
- **Derivation rules** (CEL expressions): e.g. `supply.same_state && party.regtype=='Regular' ? ['CGST','SGST'] : ['IGST']`; SEZ ⇒ IGST regardless; `IgstOnIntra` override.
- **Nature-of-Transaction classifier**: maps (regtype, state-pair, taxability, SEZ/export flags) → NoT code → GSTR table (B2B 4A/4B/4C/6B/6C; B2CL 5A/5B; B2CS 7; Exports 6A; Nil/Exempt 8A-8D; CDNR/amendments 9; HSN 12; docs 13; ECO 14/15).
- **TDS/TCS sections**: section, rate (with/without PAN), threshold, exemptions (incl. §194Q).
- **Schema validators**: NIC INV-01 (12 sections/133 fields, cardinality codes, forbidden `"`/`\`, decimal precision e.g. `AssAmt` dec(13,2), 1000-line cap), EWB JSON v1.02.01, GSTR offline-tool JSON — themselves versioned and effective-dated.

Every bundle is **Ed25519-signed**, distributed as immutable signed S3 objects via CDN, and **hot-loaded** by each tenant with version pinning. Rollout is staged: canary tenants → golden-transaction regression suite (a frozen corpus of vouchers with known-correct outputs) → fleet. Instant signed rollback to the prior version. This is how a council rate change lands same-day, free, with no reinstall — directly dismantling Tally's TSS lever.

**Why interpreted, not code:** CEL/JSON-Logic is sandboxed, deterministic, and auditor-inspectable. We never `eval` arbitrary code. The long tail (works contracts, composite/mixed supply) uses a small set of **named, signed native rule plugins**, still effective-dated and version-stamped.

### 2. Deterministic, replayable computation
`TaxDerive(voucherSnapshot, mastersSnapshot, ruleSetVersion) → TaxResult` is a **pure function**. The `ruleSetVersion` is persisted on the computed voucher. Consequences:
- Any historical filing is reproducible bit-for-bit (essential for audit and for late corrections — recompute a period under a corrected RuleSet and diff).
- The hot voucher-entry path calls the engine as an **embedded in-process library** (sub-ms) to keep Tally-grade keyboard speed; the *service* boundary is reserved for filings/reconciliation/async work.
- Full derivation is exposed transparently (state comparison, regtype, SEZ flag) — beating Tally's opaque "System Inferred" label.

### 3. GSP/IRP/GSTN gateway
All government I/O flows through one **Statutory Gateway** with a `ProviderAdapter` interface (`GenerateIRN`, `CancelIRN`, `GenEWB`, `UpdatePartB`, `FileGSTR1/3B/CMP08`, `DownloadGSTR2A/2B`, `FetchIMS`, `ActIMS`). Primary providers: **ClearTax + Masters India** (multi-homed for deadline-day resilience and rate-limit headroom); **NIC IRP direct** as secondary. Properties:
- **Idempotent outbox** with provider dedup keys (doc-hash → IRN; filing-intent-id → ARN) so retries never produce double IRNs or double filings.
- **Temporal workflows** model the 6-hour GSP session, OTP/EVC/DSC handshake (replicating Tally's `Alt+Z` → `C/E/D/S/V/T` keys for zero-learning-curve while offering a modern UI), resumable filing after connectivity drops, and **IMS deemed-accept timers** (action by 14th, changeable to 20th).
- **Circuit breakers + deadline-aware backpressure**; offline-JSON export fallback when all providers are down.
- DSC keys custodied in **AWS KMS/HSM** (ap-south-1, data residency).

### 4. MCA Rule 11(g) Edit Log
Implemented as a **per-tenant append-only, hash-chained ledger**: `row_hash = H(prev_row_hash || canonical(payload))`. Daily **Merkle-root checkpoints** are KMS-signed and written to **WORM S3 (Object Lock, compliance mode)** for the 8-year §128(5) retention. There is **no disable path in schema or API** — immutability is structural (no UPDATE/DELETE grants, RLS by tenant). Each row captures action (Created/Altered/Resaved/Deleted/Renumbered), username, timestamp, **field-wise before/after**, plus **machine/device/IP/session/geo** that Tally omits. Cross-node/branch sync writes go through this same append path (never silent overwrite), satisfying the §20 sync constraint that every overwrite be recorded as an Alteration. One-click **auditor-attestation export** services the Rule 11(g) assertion, and the captured forensic fields feed AI anomaly detection (back-dated edits, after-hours deletions, repeated alterations).

### 5. Reconciliation & IMS
Scheduled GSTN pulls (2A/2B/IMS) → deterministic matcher (GSTIN+docno+amount+period with tolerance/round-off rules, classifying fully/partially matched, portal-only, books-only, mismatch) + AI probable-match scorer with confidence. IMS accept/reject/pending is first-class state with deadline timers; we project the GSTR-2B/3B ITC impact **before the 14th** and raise "ITC at risk: supplier hasn't filed" alerts. AI only *suggests*; **human-in-the-loop approval** gates every accept and every filing.

### MVP sequencing
1. RuleSet engine + GST CGST/SGST/IGST derivation + NoT classifier + GSTR-1/3B JSON export + always-on Edit Log (covers the must-have day-one compliance + audit mandate).
2. e-Invoice IRN + e-Way Bill via GSP (gateway + Temporal).
3. Connected GSTR-1/3B/CMP-08 direct filing + 2A/2B reconciliation.
4. IMS + TDS/TCS automation + AI pre-validator/anomaly layer.

</details>

**Open risks:** RuleSet correctness is now a single point of systemic failure — a flawed signed bundle could miscompute tax fleet-wide; depends on a rigorous golden-transaction regression corpus and staged canary rollout that must be built before any auto-push is enabled; Rule expressiveness ceiling for statutory edge cases (works contracts, composite/mixed supply, branch/consignment transfers, RCM on advances) may force native rule plugins sooner than expected, partially eroding the pure-data ideal; GSP/ASP dependency: government-side outages on filing-deadline days and per-GSP rate limits are outside our control; multi-homing helps but the underlying NIC/GSTN/IRP availability is the hard ceiling; Becoming our own GSP later is a non-trivial regulatory/onboarding project; if a primary GSP changes terms or pricing the migration cost is real despite the ProviderAdapter abstraction; GSTN/NIC schema and process drift (IMS evolving since Oct 2024, Bills-of-Entry added Oct 2025, B2CL threshold changes, rate-wise HSN JSON gaps) means the schema-validator RuleSets need a dedicated regulatory-watch function to stay current; DSC custody and OTP/EVC handling carry liability and key-management risk; KMS/HSM mitigates but signatory-credential handling on behalf of clients needs careful legal and consent design; 8-year WORM retention with hash-chain + Merkle anchoring must be validated against what auditors and the MCA will actually accept as tamper-evidence — the legal sufficiency of cryptographic proof vs Tally's view-only log is unproven in practice; Replay determinism must survive across engine refactors and library upgrades; any non-determinism (float rounding, map ordering) in TaxDerive would break historical reproducibility — requires fixed-point decimal arithmetic and canonical serialization discipline

---

## B.6 AI / Actionable-Insights Platform

**Recommendation.** Build the AI layer as a server-side, multi-tenant "insight plane" that sits beside the cloud-native ledger core, never in the keystroke-critical entry/report path. Standardize on the Anthropic API with Claude Opus 4.8 (`claude-opus-4-8`) as the default reasoning model for the agentic copilot, NL-to-query planning, document understanding, and narrative generation; route high-volume cheap classification (ledger-suggestion ranking, narration autofill, statement-line categorization) to Claude Haiku 4.5; and use a small on-prem model + classical CV only for the OCR pre-pass and PII redaction so raw documents and PAN/Aadhaar/bank data never leave India-resident infrastructure for those steps. Every AI output is a *proposal* carrying a per-field confidence score that renders into Tally's exact voucher/report screens for one-keystroke accept/reject (human-in-the-loop by construction) — nothing posts silently. NL-to-ledger is text-to-a-typed-query-DSL (never raw SQL from the model) executed against a read-only per-tenant replica with row-level tenant isolation, so the LLM cannot exfiltrate or mutate cross-tenant data. Predictive cash flow, collections, anomaly/fraud, and GSTR-2B/3B reconciliation run as deterministic engines (statistics + rules over the warehouse) with the LLM used only for explanation, drafting, and edge-case classification — keeping the rupee-value features auditable and cheap. Strict data-privacy posture: zero training on customer data (Anthropic API does not train on inputs), India data residency for documents/PII via on-shore storage and the on-prem OCR/redaction pre-pass, full prompt/response audit logging, and cost controls via prompt caching, Haiku routing, batch API for nightly jobs, and per-tenant token budgets.

**Chosen stack:** `Anthropic Claude API — Claude Opus 4.8 (claude-opus-4-8) as default reasoning/agent model; adaptive thinking; effort=high for agentic loops`, `Claude Haiku 4.5 (claude-haiku-4-5) for high-volume cheap classification: ledger-suggestion ranking, narration autofill, bank-line categorization`, `Anthropic Messages API tool-use (manual agentic loop) for the copilot, with human-in-the-loop approval gates on every state-changing tool`, `Anthropic prompt caching (ephemeral, 1h TTL) for the per-tenant chart-of-accounts/schema/system-prompt prefix; Message Batches API for nightly anomaly + forecast + narrative jobs at 50% cost`, `Self-hosted OCR pre-pass: PaddleOCR / Tesseract + layout model (LayoutLMv3-class) running on India-region GPU nodes for raw-document text + bbox extraction`, `On-prem PII detection/redaction (Microsoft Presidio + India recognizers for PAN/GSTIN/Aadhaar/IFSC) before any text leaves the residency boundary`, `Postgres (primary OLTP ledger) + a per-tenant read-only logical replica / DuckDB-on-columnar mart for text-to-query and analytics; pgvector for RAG over masters/notifications`, `Python services (FastAPI) for the AI orchestration plane; the official anthropic Python SDK`, `Deterministic engines in Python/SQL: Prophet/statsmodels-class time-series for 13-week cash flow, gradient-boosted (XGBoost) collections risk scoring, rules + Benford/IsolationForest for anomaly detection`, `All AI infra in an India region (AWS ap-south-1 Mumbai / Hyderabad) with the Anthropic API as the only cross-border call, carrying redacted/structured payloads only`

**Key decisions:**

#### Default LLM and model routing → **Claude Opus 4.8 (claude-opus-4-8) via the Anthropic API as the default for the copilot, NL-to-query planning, document/voucher reasoning, reconciliation explanation, and MIS narratives; Claude Haiku 4.5 (claude-haiku-4-5) for high-volume cheap classification (ledger suggestion ranking, narration autofill, bank-line categorization); a small self-hosted model only for OCR pre-pass and PII redaction.**
Opus 4.8 is the strongest reasoning/agentic model and the right default for the trust-sensitive, India-statutory work where a wrong GST inference has rupee consequences; its 1M context comfortably holds a tenant's chart of accounts + recent vouchers + GST notification context for grounding. Haiku 4.5 is ~5x cheaper on output and fast enough for the millions of per-keystroke/per-line classification calls, so we don't burn Opus on trivial ranking. Keeping OCR + PII on a self-hosted India-region model is a residency/privacy decision, not a quality one.
- *Alternatives rejected:* Fine-tuned open-weight model (Llama/Mistral) self-hosted for everything — rejected: heavy MLOps burden, weaker reasoning on novel GST edge cases, and statutory rules change continuously so a frozen fine-tune rots; prompt+RAG on a frontier model adapts via config; Use Opus for everything including per-line classification — rejected on cost; Haiku routing cuts the dominant cost driver; Use a smaller Sonnet tier as default — viable but Opus 4.8's accuracy on ambiguous ledger/GST mapping is worth the spend on the trust-critical path; Haiku handles the volume
- *Risks:* Cross-border API dependency for the reasoning calls (mitigated by sending only redacted/structured payloads, never raw PII-bearing documents); Model version drift changing outputs (mitigated by pinning the exact model id and an eval gate before any model bump); Cost spikes from agentic loops (mitigated by effort tuning, prompt caching, per-tenant token budgets)

#### Where the AI sits relative to the keystroke loop → **A separate server-side 'insight plane' beside the ledger core. AI never blocks or replaces voucher entry / report navigation; it pre-fills the same forms and renders into the same reports as confidence-scored proposals with one-keystroke accept/reject.**
The whole strategy is zero retraining. If AI intrudes on the keyboard-only entry loop or posts silently, the CA vetoes the switch (RESEARCH.md sec 16 cross-cutting principles). Side-panel + smarter Alt+G keeps muscle memory exact while adding the wedge.
- *Alternatives rejected:* Inline auto-posting agent that books vouchers autonomously — rejected: violates the human-in-the-loop trust requirement and the MCA audit expectations; Separate chat-only app divorced from the ledger UI — rejected: loses the 'looks native' advantage and re-introduces integration friction
- *Risks:* Operators ignoring the side panel (mitigated by surfacing proactive insights in the bottom shortcut bar Tally already uses)

#### Natural-language to ledger query with guardrails → **Two-stage: LLM produces a typed, validated query in an internal DSL/structured filter object (report-id + dimensions + period + filters), NOT raw SQL; a deterministic compiler turns it into a parameterized read-only query against a per-tenant read replica with row-level tenant isolation. Results render in the matching native Tally report.**
Prevents prompt-injection-driven cross-tenant data access and accidental writes; keeps results explainable and mapped to a real report the CA can drill into; the DSL is a closed vocabulary the model can be evaluated against.
- *Alternatives rejected:* Model emits raw SQL executed directly — rejected: injection / cross-tenant / mutation risk and unbounded queries; Pure semantic search over text dumps — rejected: financial answers must be exact and reconcilable, not approximate
- *Risks:* Model maps an ambiguous request to the wrong report (mitigated by showing the resolved query + letting the user adjust filters with the same F6/Alt+F12 controls, and by an eval set of NL→query pairs); Query cost on large tenants (mitigated by the columnar mart and query timeouts)

#### Document OCR + auto-voucher pipeline → **On-shore OCR pre-pass (self-hosted PaddleOCR/Tesseract + layout model) extracts text+bbox; PII is detected/redacted on-prem (Presidio + India recognizers); then a structured, layout-independent extraction + ledger/GST/HSN inference call to Claude Opus 4.8 produces a confidence-scored draft voucher pre-filled into the native Purchase/Payment screen for one-keystroke accept; corrections feed a learning loop (few-shot exemplars per tenant, not model training).**
Layout-independent LLM extraction beats template tools and captures full line items (beating Xero/Hubdoc), but raw bills carry PAN/GSTIN/bank data — doing OCR+redaction on-shore keeps residency intact while still sending only the minimal structured text to the API. Learning from corrections via per-tenant exemplars is the cited moat without training on customer data.
- *Alternatives rejected:* Send raw document images straight to the multimodal API — rejected for India residency + PII exposure; Pure template/zonal OCR — rejected: breaks on vendor redesigns and misses line items (the known sore point to beat); Fine-tune an extraction model on customer docs — rejected: no-training-on-customer-data policy and statutory drift
- *Risks:* OCR errors on poor scans (mitigated by per-field confidence + mandatory human review below threshold; exception-only review above); Wrong GST rate/HSN inference (mitigated by a dated HSN→rate map with citation and confidence gating; never auto-files)

#### Predictive, collections, anomaly/GST features = deterministic engines with LLM-as-explainer → **13-week cash-flow forecast (time-series over collections/seasonality/open SOs/recurring payables/predicted GST-TDS outflows), collections risk scoring (gradient-boosted on per-debtor payment history), anomaly/fraud detection (rules + Benford + IsolationForest + edit-log signals), and GSTR-2B/2A vs register reconciliation all run as auditable deterministic pipelines; the LLM only generates the plain-English narrative, the graduated reminder drafts, and edge-case classifications.**
Rupee-value, statutory, and audit-facing numbers must be reproducible and defensible — you cannot have an LLM hallucinate an ITC figure or a cash-flow number. Deterministic engines are cheap, testable, and explainable; the LLM adds the language layer competitors lack. This also slashes cost (no LLM call per voucher for anomaly scanning).
- *Alternatives rejected:* LLM directly computes forecasts/reconciliation — rejected: non-reproducible, expensive, and unauditable for filing; Black-box ML with no explanation — rejected: CAs need the 'why' and a drill path
- *Risks:* Cold-start tenants with little history (mitigated by sensible priors + clearly labeled low-confidence forecasts); Model/rule false positives in anomaly digest (mitigated by daily 'things to check' digest framing + feedback to suppress repeat false flags)

#### Data privacy, residency, evaluation, and cost controls → **No training on customer data (Anthropic API inputs are not used for training); India data residency for documents/PII via on-shore storage + on-prem OCR/redaction so only minimal redacted/structured payloads cross the border; full prompt/response audit logging per tenant; an eval harness (golden sets for NL→query, OCR extraction, GST reconciliation) gates every prompt/model change; cost controls via prompt caching of the per-tenant schema prefix, Haiku routing, Batch API for nightly jobs, and per-tenant token budgets with backpressure.**
Compliance and residency are the killer features; the privacy posture must be first-class and demonstrable to CAs/auditors. Evals prevent silent regressions on statutory logic. Cost discipline keeps the AI margin viable at SMB price points.
- *Alternatives rejected:* Send raw PII to the API and rely on contractual no-training — rejected: residency + minimization demand on-shore redaction; Ship model changes without an eval gate — rejected: statutory correctness is non-negotiable
- *Risks:* Anthropic API region/latency for India traffic (mitigated by async/batch where possible and caching; structured payloads keep tokens small); Audit-log volume/cost (mitigated by tiered retention meeting the 8-year statutory requirement for filing-relevant artifacts only)

**Replication fidelity.** Zero-learning-curve is preserved by an architectural rule: AI lives in a non-blocking side panel and a smarter Alt+G command palette, never in the keyboard-only data-entry/report loop. The literal Tally behaviors are untouched — Alt+G type-ahead still resolves a literal report name; F5/F6/F8/F9 voucher entry, Ctrl+A accept, Alt+C on-the-fly master creation, Enter-drill, F12-configure, Alt+N auto-column all behave identically. AI only *pre-fills* the same voucher form (operator presses Ctrl+A to accept, or edits/escapes as today) and *renders into* the same report screens. Every suggestion carries a per-field confidence score and a one-keystroke accept/reject, matching the human-in-the-loop pattern Tally users and CAs already trust from exception-only review. Data compatibility holds because the AI never invents a parallel data model: NL-to-query targets the same ledger/voucher/stock schema migrated 1:1 from Tally, query results map back to Tally report IDs, and AI-proposed vouchers are written through the identical voucher API that enforces the GST nature-of-transaction rules and the MCA edit-log — so a CA reading the books (or the audit trail) sees ordinary, attributable entries, with AI actions clearly tagged as proposals that a named user accepted.

**AI angle.** The AI layer is the entire competitive wedge of this domain: Tally's own dashboard explicitly does no prediction, forecasting, NL query, OCR, or anomaly detection (RESEARCH.md sec 15.4, 16). AI plugs in as a separate "insight plane" beside the ledger: (1) an agentic copilot over Alt+G that turns "create sales invoice for Acme 50000 +18% GST" or "show debtors over 90 days in Maharashtra" into pre-filled vouchers / native-looking reports; (2) a document-in pipeline (photo/PDF/WhatsApp/email) that OCRs and extracts full line items, then proposes a Purchase/Payment voucher with ledger+GST+HSN inference at a confidence score; (3) deterministic predictive engines (cash-flow runway, collections risk, anomaly/fraud digest) that the LLM only explains; (4) GSTR-2B/2A vs purchase-register reconciliation with one-click ITC-recovery vouchers and auto-drafted vendor chase messages — the highest rupee-value India-specific feature. The model never touches the keystroke loop or posts silently; it produces confidence-scored proposals into Tally-identical screens for one-keystroke human approval.

**Interfaces to other domains:**
- Ledger/Accounting core (Postgres OLTP): the AI plane reads a per-tenant read-only logical replica; it never writes directly. All AI-proposed mutations go back through the core's normal voucher-creation API with the same validation, edit-log, and RBAC as a human keystroke — the AI is just another authenticated actor whose writes are gated by human approval.
- Reports/MIS layer: NL-to-query results render through the SAME report components (Balance Sheet, Outstandings, Day Book, Stock Summary) so output looks native and preserves Enter-drill / F12 / Alt+N grammar; the copilot resolves an NL request to a report ID + filter set rather than inventing a UI.
- GST/Compliance module: consumes GSTR-2B/2A/1/3B pulls and the e-invoice/e-way-bill schemas; the reconciliation engine matches against the purchase/sales registers and emits proposed correction vouchers and IMS accept/reject suggestions for human sign-off before filing.
- Edit-Log / Audit-Trail subsystem: anomaly/fraud detection reads the immutable hash-chained edit log (back-dated edits, weekend bulk deletes, round-tripping) and writes flags back as non-destructive annotations; every AI proposal and every accept/reject is itself written to the audit log.
- Cloud-native data sync / multi-branch core: the AI plane operates on the consolidated cloud dataset, so cross-branch cash-flow and receivables insights are real-time (impossible in Tally's batch-sync model).
- Extensibility/TDL-replacement layer: the no-code AI report builder emits the same declarative report definitions the extension API consumes, so 'describe the report you want' produces a first-class, versioned artifact.

<details><summary>Detailed engineering writeup</summary>

## AI / Actionable-Insights Platform — Architecture Decision Record

### Context and design center
Tally's dashboard explicitly performs **no** prediction, AI insight, forecasting, NL query, OCR, or anomaly detection (RESEARCH.md §15.4, §16). That gap is the product wedge. The non-negotiable constraint is that adding AI must not cost us the zero-learning-curve property: the keyboard-driven entry/report loop must stay byte-for-byte familiar, and nothing may post to the books silently — both because CAs veto otherwise, and because the MCA Edit Log audit-trail mandate requires every change to be attributable.

The design center is therefore: **an AI insight plane that runs server-side beside the ledger core, produces confidence-scored proposals, and renders them into the exact native Tally screens for one-keystroke human approval.** AI is additive and non-blocking; the ledger and report grammar are the system of record.

### LLM strategy
Default to the **Anthropic API with Claude Opus 4.8 (`claude-opus-4-8`)** for all reasoning-heavy, trust-critical work: the agentic copilot, NL→query planning, document→voucher inference, GST reconciliation explanation, and MIS narratives. Adaptive thinking on; `effort: "high"` for agentic loops. Opus 4.8's 1M context lets us ground each request on the tenant's chart of accounts, recent vouchers, and dated GST notifications without retrieval gymnastics.

Route the **high-volume, low-stakes** calls to **Claude Haiku 4.5 (`claude-haiku-4-5`)**: per-keystroke ledger-suggestion ranking, narration autofill, and bank-statement line categorization. These fire millions of times; Haiku is ~5× cheaper on output and fast enough for inline UX.

Use a **small self-hosted model only for OCR + PII** — not for quality reasons but for residency. A LayoutLM-class extractor + PaddleOCR/Tesseract runs on India-region GPU nodes to turn raw bills into text+bbox; Presidio with India recognizers (PAN/GSTIN/Aadhaar/IFSC) redacts before anything crosses the border. We reject self-hosting a frontier-class general model: statutory rules change continuously, so a frozen fine-tune rots, and the MLOps burden is not worth it when prompt+RAG on Opus adapts via config.

### Agentic copilot with human-in-the-loop
The copilot is a manual Anthropic tool-use loop (so we control approval gates), exposed through a **smarter Alt+G command palette** and a non-blocking side panel. Read tools (`run_report`, `query_ledger`, `lookup_hsn_rate`) execute freely. **Every state-changing tool (`propose_voucher`, `propose_reminder`, `propose_correction`) only stages a proposal** — it returns a pre-filled voucher into the native screen; the operator presses Ctrl+A to accept (which routes through the ordinary voucher API with full validation, GST nature-of-transaction rules, RBAC, and edit-log) or Escapes. The AI is an authenticated actor whose writes are gated by a named human; the audit log records the proposal, the actor, and the accept/reject.

```
User (Alt+G): "sales invoice Acme 50000 +18% GST"
  Opus 4.8 (tool: propose_voucher) -> staged draft, per-field confidence
  -> native F8 Sales screen, pre-filled  -> operator Ctrl+A (or edit/Esc)
  -> ledger core voucher API (validation + edit-log + RBAC)
```

### Natural-language to ledger query (guardrails)
Two-stage, never raw SQL from the model:
1. Opus 4.8 emits a **typed query object** in a closed DSL: `{report_id, period, dimensions, filters[]}` (e.g. "debtors over 90 days in Maharashtra" → `report_id=Outstandings/Receivables, ageing=>90, filter: state=MH`).
2. A deterministic compiler turns it into a **parameterized, read-only** query against a **per-tenant read replica with row-level tenant isolation**, with timeouts. Results render in the matching native report so Enter-drill / F12 / Alt+N still work.

This blocks prompt-injection-driven cross-tenant access and any mutation, keeps answers exact and reconcilable, and gives a closed vocabulary we can build an eval set against. Pure raw-SQL-from-LLM and pure semantic-search-over-dumps are both rejected (injection/mutation risk; financial answers must be exact).

### Document OCR + auto-voucher pipeline
```
Bill (photo/PDF/WhatsApp/email)
  -> [India region] OCR pre-pass: text + bbox (PaddleOCR/Tesseract + layout model)
  -> [India region] PII detect/redact (Presidio + PAN/GSTIN/Aadhaar/IFSC recognizers)
  -> Opus 4.8 structured extraction: header + LINE ITEMS, ledger/GST/HSN inference
  -> confidence-scored draft Purchase/Payment voucher -> native screen -> Ctrl+A
  -> corrections stored as per-tenant few-shot exemplars (NOT model training)
```
Layout-independent LLM extraction captures full line items (beating Xero/Hubdoc totals-only) and survives vendor redesigns. Per-field confidence drives exception-only review (above threshold = one-keystroke accept; below = mandatory review). The learning loop is the cited moat (Intuit/Zoho) achieved **without training on customer data**.

### Deterministic engines, LLM-as-explainer
The rupee-value, statutory, audit-facing computations are **deterministic and reproducible**; the LLM only adds language:
- **13-week cash-flow forecast + scenarios** — time-series over collections, seasonality, open sales orders, recurring payables (rent/salary/EMI), predicted GST/TDS outflows. "What if this customer pays 20 days late?" is a recompute, not a guess.
- **Collections prediction** — gradient-boosted risk score per debtor from their own payment history; LLM drafts graduated reminders timed to highest-response windows.
- **Anomaly/fraud** — rules + Benford + IsolationForest + **edit-log signals** (back-dated edits, weekend bulk deletes, round-tripping) → daily "things to check" digest.
- **GSTR-2B/2A vs purchase register** — deterministic match on GSTIN + invoice no. + date + taxable value + tax → Matched / Partial / Missing-in-books / Missing-in-2B / Ineligible; one-click ITC-recovery voucher + LLM-drafted vendor chase; IMS accept/reject suggestions. This is the highest-rupee-value India feature, and it must be auditable, so an LLM never computes the ITC number.

We reject LLM-computed forecasts/reconciliation outright: non-reproducible, expensive per voucher, and indefensible at filing time.

### Data privacy, residency, evaluation, cost
- **No training on customer data** — the Anthropic API does not train on inputs; we contractually and architecturally reinforce this.
- **India residency** — documents/PII stored on-shore (ap-south-1); OCR + redaction on-prem so only **minimal redacted/structured** payloads cross the border. The Anthropic API call is the only egress, and it never carries raw PII-bearing images.
- **Auditability** — full per-tenant prompt/response logging; every AI proposal and accept/reject in the edit log; tiered retention meeting the 8-year statutory window for filing-relevant artifacts.
- **Evaluation** — a golden-set eval harness for NL→query pairs, OCR extraction fields, and GST reconciliation classification **gates every prompt or model change** so statutory logic never silently regresses.
- **Cost controls** — Anthropic **prompt caching** (1h TTL) of the per-tenant schema/system-prompt prefix; **Haiku routing** for the volume tier; **Batch API** for nightly anomaly/forecast/narrative jobs at 50%; **per-tenant token budgets** with backpressure. These keep AI margin viable at SMB price points.

### Honest risks
Cross-border dependency for reasoning calls (mitigated by redaction + minimal payloads), model-version drift (pinned id + eval gate), cold-start tenants (priors + low-confidence labels), OCR errors on bad scans (confidence gating), and API latency for India traffic (async/batch + caching). None of these touch the keystroke loop or auto-post to the books, which is the load-bearing safety property.

</details>

**Open risks:** Anthropic API region and latency for India-origin interactive traffic — interactive copilot/NL-query calls are synchronous; needs measurement and possibly aggressive caching/async UX to keep Alt+G snappy.; India data-residency regulatory interpretation: confirm that sending redacted, non-PII structured payloads to a cross-border API satisfies sector expectations for SMB financial data, or whether a fully in-region inference option is contractually required for some customer segments.; Per-tenant cost ceiling: the cost model assumes Haiku routing + caching + batch hold; needs load-testing against a heavy-OCR, heavy-NL-query tenant to confirm AI doesn't erode SMB-tier margin.; Confidence-score calibration: 'one-keystroke accept above threshold' only works if confidence is well-calibrated per field; mis-calibration either over-burdens review (kills the time-saving) or lets bad GST/ledger inferences through.; HSN→rate map currency: Tally itself disclaims that GSTN APIs return rate-for-HSN; maintaining a dated, citation-backed HSN→rate table is an ongoing data-ops obligation, not a one-time build.; Anomaly-digest false-positive fatigue: if the daily 'things to check' digest is noisy, CAs stop reading it; needs a suppression/feedback loop and conservative default thresholds.; Eval coverage for the long tail of GST nature-of-transaction edge cases (RCM, composition, SEZ, exports) — gaps here surface as wrong reconciliation classifications with real ITC consequences.

---

## B.7 Migration & Tally Interoperability

**Recommendation.** Make the live-Tally XML/JSON gateway (port 9000) the single, dependable migration path for v1, wrapped in a tiny signed Windows "Migration Agent" that auto-enables the server, iterates companies, and runs one TDL-defined custom COLLECTION export to pull masters and every voucher field (narration, bill allocations, cost centres, GST analysis, batch/godown) in deterministic batches. Land everything first into an immutable, append-only "Tally Staging" store (raw XML/JSON preserved verbatim with CRC/hash provenance), then transform into our canonical domain model through a versioned, idempotent importer keyed on Tally's GUID + MASTERID/VOUCHERID. Prove zero data loss with an automated reconciliation harness that re-derives Trial Balance, Balance Sheet, P&L, bill-wise outstandings, stock summary, and GSTR-1/3B from OUR data and ties them to Tally's own exported reports to the rupee, blocking go-live until every report reconciles. Keep "the CA can still read it in Tally" true from day one via a faithful Tally-XML/JSONEX export (and .001-style guidance) that round-trips our data back into a Tally company, plus a bounded two-way bridge during a transition window that uses a strict directional ownership model (no silent last-syncer-wins) and node-partitioned IDs to avoid Tally's GUID/voucher-number collisions. Treat a from-scratch offline .900/.1800 binary parser as a deliberately deferred R&D moat for backups/dead-machine/corrupt cases — never the v1 dependency. Reject CSV/Excel export-import (lossy) and direct binary read (undocumented, version-divergent, fragile) as primary paths.

**Chosen stack:** `Migration Agent: .NET 8 self-contained single-file EXE (Authenticode-signed, WiX/MSIX installer), Windows tray app — must run on the same Windows box as Tally to reach localhost:9000`, `Gateway client + extraction: C# HttpClient driving Tally XML ENVELOPE export with embedded TDL <TDLMESSAGE><COLLECTION> definitions; JSONEX path for TallyPrime 7.0+; ODBC (cdata/Tally DSN) only as a fallback cross-check for tabular masters`, `Transport hardening: agent terminates TLS + token auth to our cloud (mTLS), so Tally's plaintext port 9000 is never exposed beyond localhost`, `Staging store: PostgreSQL 16 with JSONB columns holding verbatim Tally XML/JSON payloads + SHA-256 per batch; object storage (S3-compatible, e.g. AWS S3) for raw export artifacts and uploaded TBK*.001 backups`, `Importer/transform: TypeScript (Node 20) workers, deterministic and idempotent, keyed on Tally GUID + MASTERID/VOUCHERID; orchestrated as durable jobs via Temporal`, `Reconciliation harness: SQL + a typed assertion engine (TypeScript) that recomputes TB/BS/P&L/outstandings/stock/GSTR and diffs to the rupee against Tally-exported reports; produces a signed Migration Fidelity Report (PDF + JSON)`, `Two-way bridge: same Migration Agent in 'Both' mode, polling CID/AID watermarks for delta pull and POSTing our changes as Tally ENVELOPE imports; conflict adjudication in the cloud with an explicit ownership matrix`, `Export-to-Tally: server-side renderer emitting Tally-native All Masters + Vouchers XML and TallyPrime 7.0 JSONEX, validated by round-tripping into a headless Tally instance in CI`, `Date/number normalization: canonical YYYYMMDD internally; accept YYYYMMDD, D-MMM-YYYY, ISO-8601; rupee amounts as integer paise (no floats)`, `Deferred moat: Rust offline .900/.1800 parser (kaitai-struct-assisted reverse engineering) for backup/corrupt-file ingestion, gated behind the live path`

**Key decisions:**

#### Primary ingestion mechanism from Tally → **Drive a LIVE Tally instance over the XML-over-HTTP gateway (port 9000) using TDL-defined custom COLLECTION exports, wrapped in a signed Windows Migration Agent that auto-enables the server and iterates companies; JSONEX on TallyPrime 7.0+.**
Section 17 is definitive: the .900/.1800 object store is undocumented, version-divergent (.500/.900/.1800), uses on-disk self-balancing trees with no public fanout schema, stores variable-length non-contiguous records with free-space reuse, and the 3.0 format break was deliberate. Every shipping tool (tally-database-loader, Zoho, QuickBooks, SAP, cdata) drives live Tally; none parse the binary in production. The XML interface exposes the exact object/method/collection model (the same model TDL/ODBC use), so a single custom COLLECTION fetch returns complete voucher detail in one call. This is the only path that is complete, correct today, and works across Tally.ERP 9 and TallyPrime.
- *Alternatives rejected:* Direct binary .900/.1800 parse as v1 path; ODBC as primary; CSV/Excel report export then import (the Zoho/QuickBooks dance); Restore TBK*.001 backup into our own engine
- *Risks:* Requires Tally to be running on Windows with a company open — fails for dead machines/backups-only customers (mitigated by deferred binary parser + accepting TBK*.001 + 'bring your CA' assisted migration); Gateway is unauthenticated plaintext; agent must bind localhost and tunnel over TLS; XML schema/tag drift across Tally builds (DSP* display tags, ERP9 vs Prime) requires per-version extraction profiles; Large datasets: in-RAM Tally + single port serializes export; must batch by company/year/voucher-type and respect Split Company Data

#### Whether to build an offline binary .900/.1800 reader → **Defer it. Ship v1 on the live gateway only; fund a Rust offline parser later as a moat for backups, dead machines, and corrupt installs where competitors fail.**
Section 17 shows the crypto is NOT the blocker (TallyVault off by default, files merely obfuscated + CRC-32 block-checksummed), but the record-level schema and self-balancing-tree navigation are wholly undocumented and reset by the 3.0 break. Betting v1 on reverse engineering is schedule suicide and contradicts the fast-MVP bias. As a later differentiator it is genuinely valuable: migrate from a TBK backup or a corpse machine no one else can touch.
- *Alternatives rejected:* Make binary parse the v1 hero feature; Never build it
- *Risks:* Reverse-engineering cost is open-ended and per-dialect; Reading raw binary risks silent misinterpretation — must always cross-validate against a live export or our reconciliation harness before trusting it; TallyVault-on files need password + DES-derived decryption, raising support/legal questions

#### Provenance and idempotency model for the importer → **Land raw Tally XML/JSON verbatim into an immutable Staging store with SHA-256 per batch, then transform idempotently keyed on Tally company GUID + MASTERID/VOUCHERID, with our own ULID/UUIDv7 surrogate keys for internal links.**
Reconciliation-to-the-rupee and audit defensibility require that we can always re-derive the import from the original bytes and re-run transforms deterministically without duplication. Tally exposes stable GUID/MASTERID/VOUCHERID identifiers; keying on them makes re-runs and the two-way bridge idempotent. Keeping raw payloads supports the MCA Edit Log promise and 'verifiable fidelity' marketing (Section 17).
- *Alternatives rejected:* Transform-on-ingest with no raw retention; Key only on (voucher type, number) like Tally does internally
- *Risks:* (voucher type, number) is NOT globally unique across nodes/companies — keying on it would collide; must use GUID+ID; Storage cost of retaining raw payloads (acceptable; compress + lifecycle to cold storage); MASTERID/VOUCHERID semantics differ slightly ERP9 vs Prime — needs per-version mapping

#### How to prove zero data loss → **A mandatory automated reconciliation harness that recomputes Trial Balance, Balance Sheet, P&L, bill-wise outstandings, stock summary (qty+value by valuation method), and GSTR-1/3B from OUR migrated data and diffs them to the rupee/paise against Tally's own exported reports; go-live is blocked until all tie out, and a signed Migration Fidelity Report is issued.**
Section 3 says the migration must reproduce Tally's trial balance and GST returns to the rupee for the owner AND their CA to trust it — this is the switching-cost killer. Tally's own import even rejects unbalanced vouchers ('Voucher totals do not match! Diff: 1.00 Cr', Section 18), so we adopt the same zero-tolerance discipline. Recomputing from our model (not just counting rows) catches transform bugs, rounding drift, and dropped sub-objects (bill allocations, cost centre splits, batch/godown).
- *Alternatives rejected:* Row-count/control-total checks only; Trust the importer; spot-check manually
- *Risks:* Tally's report rounding and valuation-method nuances (FIFO/Avg/etc.) must be matched exactly or false mismatches appear; Period boundaries / opening-vs-closing balance handling and Split Company Data can cause apparent gaps; GSTR derivation depends on the Nature-of-Transaction rules engine (Section 19) — reconciliation must align with the GST domain's engine

#### Two-way bridge during transition → **Offer a bounded, explicitly-scoped two-way bridge using the same agent in 'Both' mode: delta-pull from Tally via CID/AID watermarks, push our writes as Tally ENVELOPE imports, with a strict per-entity directional ownership matrix (one system of record per voucher type/company during the window) and node-partitioned IDs — NOT silent last-syncer-wins.**
Section 3/17 want customers to run alongside Tally with zero data risk during trial. But Section 20 shows Tally sync is directional blind overwrite that silently destroys the loser's edit, and same-GUID/duplicate-voucher-number collisions hard-fail the whole run. We avoid that by assigning ownership and using node-prefixed ULIDs + distinct voucher series per source, surfacing any true conflict to a human queue and recording every cross-system write in our hash-chained audit log.
- *Alternatives rejected:* Full real-time bidirectional CRDT merge from day one; No bridge — one-shot migration only; Mirror Tally's last-syncer-wins
- *Risks:* Bidirectional sync is inherently risky; a bug could write bad vouchers back into the customer's Tally (mitigate: dry-run + diff preview, opt-in, reversible); Voucher-number/GUID collisions if ownership is misconfigured; Edit Log immutability means we cannot silently overwrite — every bridge write must be an auditable alteration on both sides; Encourages prolonged dual-running; cap the window and nudge to cutover

#### Keeping 'every CA can still read it in Tally' → **Ship a faithful server-side export that renders our data as Tally-native All Masters + Vouchers XML and TallyPrime 7.0 JSONEX, round-trip-validated by importing into a headless Tally in CI; provide one-click 'Export to Tally company' on demand.**
Section 3 names the 'my CA can't use it' objection as a core moat to neutralize. Re-emitting valid Tally XML that imports cleanly (passing Tally's own balance/voucher validation) means any CA can reconstruct a Tally company from our system at any time, defusing lock-in fear at the moment of switching.
- *Alternatives rejected:* Only provide PDF/Excel reports to the CA; Give CAs a read-only Tally-identical web view but no Tally-format export
- *Risks:* Faithful re-emission of GST/inventory/bill-wise/cost-centre structures is non-trivial; must validate against live Tally import RESPONSE (CREATED/ERRORS) in CI; Tally XML tag drift across builds means maintaining version-targeted emitters; Round-trip is not guaranteed bit-identical (Tally re-derives aggregates); we attest report-level equivalence, not byte equivalence

**Replication fidelity.** Zero learning curve is preserved because migration is genuinely one-click: the signed agent auto-performs the Settings→Connectivity→Server/port steps and iterates companies, so the owner only points at their Tally install — no CSV export dance, no field mapping, no retraining. Data compatibility is preserved bidirectionally: we ingest the complete Tally object model (masters, all historical vouchers, bill-wise, cost-centres, batches/godowns, GST ledgers, opening AND closing balances) and we re-emit valid Tally All Masters/Vouchers XML + JSONEX that imports cleanly into a real Tally company, so the customer's CA can keep reading the books in Tally from day one. The reconciliation harness proves equivalence by reproducing Tally's own Trial Balance, Balance Sheet, P&L, outstandings, stock summary, and GSTR-1/3B to the rupee before go-live, with a signed fidelity report tied to the SHA-256 of the source export — directly neutralizing the #1 SMB fear (silent data loss) and the #1 lock-in objection (my CA can't use it).

**AI angle.** AI accelerates the parts that are otherwise manual and brittle: (1) LLM-assisted field mapping that auto-maps arbitrary/custom Tally UDFs and non-standard ledgers/voucher types onto our canonical schema, and maps Vyapar/BUSY/Marg exports too, eliminating per-client TDL mapping. (2) A reconciliation copilot that, when a report fails to tie out, localizes the discrepancy (which group → ledger → voucher → sub-object), explains the likely cause (rounding, dropped bill allocation, valuation-method mismatch, period boundary), and proposes a fix in natural language. (3) Rich structured error translation: Tally's single free-text LINEERROR (e.g. 'Voucher totals do not match') is parsed into a field-path/expected-vs-actual object an agent can auto-correct. (4) For the deferred binary-parser moat, AI-assisted header/Masters-Transactions relinking for the Tranmgr.900/Linkmgr.900 corruption class that customers currently pay forensic labs to fix by hand. All AI output is confidence-scored and human-in-the-loop; nothing writes to the ledger or back to Tally without explicit accept and a passing reconciliation gate.

**Interfaces to other domains:**
- GST/Compliance domain: reconciliation harness calls their Nature-of-Transaction rules engine to derive GSTR-1/3B from migrated vouchers so migrated books match Tally's filed returns to the rupee; export-to-Tally must round-trip GST ledgers and HSN/tax analysis
- Core Accounting Engine: defines the canonical domain model (groups/ledgers/vouchers/bill-wise/cost-centres) the importer maps Tally objects onto; opening vs closing balance and financial-year/Split-Company semantics must align
- Inventory domain: stock items, batches, godowns, units, and the valuation method must be migrated and recomputed identically for stock-summary tie-out
- Audit/Edit-Log + Sync domain: every import and every bridge write appends to the hash-chained MCA-compliant audit trail; node-partitioned IDs and ownership matrix are shared with the multi-tenant sync engine
- Cloud platform/Security: the Migration Agent terminates mTLS to the cloud so Tally's unauthenticated plaintext port 9000 is never exposed; staging/raw-artifact storage and Temporal orchestration live in the multi-tenant core
- Extensibility (TDL replacement): the bundled extraction TDL and any partner customization importer share the same Tally object/method/collection vocabulary

<details><summary>Detailed engineering writeup</summary>

## ADR: Zero-Data-Loss Migration FROM Tally and Ongoing Interoperability

### Context and the one load-bearing fact
Tally stores each company as a numeric folder of proprietary, CRC-32-blocked, self-indexing object-store files (`.500`/`.900`/`.1800`), navigated by on-disk self-balancing binary trees, with variable-length non-contiguous records and a deliberate 3.0 format break (5-digit `.900` → 6-digit `.1800`). The schema is undocumented and version-divergent, and **no shipping product parses it in production** — every working tool (tally-database-loader, Zoho/QuickBooks/SAP migrations, cdata ODBC) drives a *running* Tally over the port-9000 XML/ODBC gateway. The crypto is not the blocker (TallyVault is off by default; files are obfuscated, not encrypted); the schema is. This single fact dictates the architecture: **the v1 ingestion path must go through live Tally's export API.**

### Architecture overview
```
[Customer Windows box]                         [Our Cloud]
  Tally.exe (company open)
      | localhost:9000 (XML / JSONEX)            Temporal workflows
      v                                          +------------------------+
  Migration Agent (.NET 8, signed)  --mTLS-->    | Staging (PG JSONB +     |
   - auto-enable Server/Both                      |  raw artifacts in S3)   |
   - iterate companies                            |   keyed: GUID+MASTERID/ |
   - TDL custom COLLECTION export                 |          VOUCHERID      |
   - batch by company/year/vch-type               +-----------+------------+
   - CRC/SHA-256 provenance                                   | idempotent transform
                                                              v
                                                   Canonical domain model
                                                   (ULID/UUIDv7 internal keys)
                                                              |
                                          +-------------------+-------------------+
                                          v                                       v
                              Reconciliation Harness                   Export-to-Tally (XML/JSONEX)
                              (recompute TB/BS/P&L/                     round-trip validated in CI
                               outstandings/stock/GSTR;
                               tie to the rupee; sign report)
```

### Ingestion: TDL-driven XML export, not CSV, not binary
We bundle a small **signed .NET 8 Migration Agent** that runs on the same Windows machine as Tally (the only place that can reach `localhost:9000`). It silently does the "open company → Settings → Connectivity → acts as Server/Both → set port" dance, enumerates companies (`List of Companies`), and for each runs an **Export request whose DESC embeds a custom `<TDLMESSAGE><COLLECTION>`** — exactly the technique tally-database-loader uses — so a single fetch returns *complete* voucher detail: ledger entries, `BILLALLOCATIONS.LIST`, `COSTCENTREALLOCATIONS.LIST`, `BATCHALLOCATIONS.LIST`, GST/tax analysis, narration, and the stable `GUID`/`MASTERID`/`VOUCHERID`. We export masters via `List of Accounts`/per-object FETCHLISTs and transactions batched by company → financial year → voucher type to respect Tally's in-RAM, single-port serialization and the Split-Company-Data reality. On TallyPrime 7.0+ we prefer **JSONEX** (valid arrays) over the legacy JSON (duplicate-key) dialect; ODBC is kept only as a tabular cross-check for masters because it is read-mostly and suffers 32/64-bit driver hell. We **reject CSV/Excel** (lossy on customizations, sub-objects, and precision — the multi-step Zoho dance Section 3 derides) and **reject direct binary read for v1** (undocumented, fragile, version-broken).

We normalize aggressively at the boundary: accept `YYYYMMDD`, `D-MMM-YYYY`, and ISO-8601 dates and canonicalize to `YYYYMMDD`; represent all money as **integer paise** to eliminate float drift before reconciliation ever runs.

### Provenance, idempotency, and identity
Raw export payloads are stored **verbatim** (JSONB + S3 artifacts) with a SHA-256 per batch before any transformation — so we can always re-derive the import from original bytes and re-run transforms deterministically. The transform is keyed on **Tally GUID + MASTERID/VOUCHERID**, never on `(voucher type, number)` — Section 20 proves that pair is not globally unique and is precisely what makes Tally branch sync collide. Internally we mint **ULID/UUIDv7 surrogate keys** with a node prefix, exposing human-friendly per-series voucher numbers as a display layer. This makes both re-imports and the two-way bridge idempotent and collision-proof.

### Proving zero data loss — the reconciliation harness (the actual product)
Counting rows is not proof. The harness **recomputes financial reports from our migrated model** and diffs them to the **rupee/paise** against Tally's own exported reports:
- Trial Balance (every ledger closing Dr/Cr), Balance Sheet, P&L
- Bill-wise outstandings / receivables-payables aging
- Stock Summary: quantity **and** value, matching Tally's valuation method (FIFO/Avg/etc.)
- GSTR-1 / GSTR-3B totals (delegated to the GST domain's Nature-of-Transaction engine for table mapping)

Pseudocode for the gate:
```
for company in migrated:
    ours   = recompute_reports(company)         # from canonical model
    theirs = parse(tally_exported_reports)      # Trial Balance, BS, P&L, GSTR...
    diffs  = tie_out(ours, theirs, tol=0_paise) # zero tolerance
    if diffs: block_go_live(company, diffs)      # itemized, drill to voucher
issue_signed_fidelity_report(company)            # PDF + JSON, hash of raw batches
```
We mirror Tally's own zero-tolerance discipline — its importer rejects unbalanced vouchers (`Voucher totals do not match! Diff: 1.00 Cr`). Go-live is **blocked** until every report ties out; the customer and their CA receive a **signed Migration Fidelity Report** referencing the SHA-256 of the source batches. This directly answers Section 3's mandate: reproduce the trial balance and GST returns to the rupee or it is not a migration.

### Keeping "the CA can still read it in Tally" — export path on day one
A server-side emitter renders our data back to **Tally-native `All Masters` + `Vouchers` XML and TallyPrime 7.0 JSONEX**, validated in CI by importing into a headless Tally instance and asserting the RESPONSE block (`CREATED` matches, `ERRORS=0`). One-click "Export to Tally company" is available any time. We attest **report-level equivalence**, not byte-identical files (Tally re-derives aggregates on import), which is exactly what a CA needs.

### Two-way bridge during transition (bounded, safe, auditable)
Customers can run alongside Tally during a capped trial window. The same agent runs in **Both** mode: delta-pull via CID/AID watermarks, push our writes as Tally imports. Critically we do **not** copy Tally's directional blind-overwrite (Section 20 — it silently destroys the loser's edit and hard-fails on GUID/voucher-number collisions). Instead:
- an explicit **ownership matrix** assigns one system-of-record per voucher type/company for the window;
- **node-partitioned ULIDs + distinct voucher series** per source prevent collisions;
- true conflicts surface to a **human resolution queue** (never silent);
- every cross-system write is appended to our **hash-chained, MCA-compliant Edit Log** on both directions.
Dry-run/diff preview is mandatory before any write back into the customer's live Tally, and the window is time-boxed to push cutover.

### Deferred moat: offline binary parser
A Rust `.900/.1800` reader (kaitai-struct-assisted) is funded *after* v1 to ingest TBK*.001 backups, dead machines, and corrupt installs — cases where live-Tally-only competitors fail entirely. Because raw binary risks silent misinterpretation, anything it produces is **always re-validated through the same reconciliation harness** before we trust a paisa of it. It is a differentiator, never a dependency.

### Why this is the principal-engineer call
It is the boring, proven path (it is what every shipping tool does), it makes fidelity *verifiable* rather than asserted, it preserves the CA escape hatch from day one, and it isolates the one genuinely risky idea (binary parsing, bidirectional sync) behind gates and human review — fast MVP now, moat later.

</details>

**Open risks:** Live-only v1 cannot serve customers with only a backup, a dead machine, or a corrupt install until the deferred Rust binary parser ships — bridge with 'bring it to a partner / assisted migration' and TBK*.001 ingestion via a temporary headless Tally; TallyVault-on companies need the user's password and DES-derived decryption even for the live export (TallyVault prompts on load) — define a clear consent/credential-handling flow and legal posture; Tally XML/JSON tag drift across ERP 9 vs TallyPrime vs builds (DSP* display tags, JSON vs JSONEX, date-format strictness) requires maintaining version-targeted extraction and emission profiles; needs a matrix of real Tally versions in CI; Stock valuation parity (FIFO/Weighted Avg/etc.) and Tally report rounding are the most likely sources of false reconciliation mismatches — must replicate Tally's exact valuation math, not approximate it; The two-way bridge can write bad vouchers back into a customer's production Tally if ownership is misconfigured or our emitter has a bug; mitigations (opt-in, dry-run diff, reversible, time-boxed) reduce but do not eliminate risk; Large datasets stress single-port, in-RAM Tally export; batching strategy must be validated on real multi-GB / multi-year companies and against Split Company Data; Headless/automated Tally for round-trip CI and TBK restore may have licensing/EULA implications that legal must clear; Export-to-Tally is report-level equivalent, not byte-identical; must set CA expectations explicitly so 'it doesn't match exactly' is not perceived as data loss

---

## B.8 Security, Multi-Tenancy & Identity

**Recommendation.** Build identity on a self-hosted Keycloak (OIDC/OAuth2/SAML/SCIM) running in-region on AWS Mumbai (ap-south-1), fronting a Postgres-backed core where tenant isolation is enforced by mandatory Row-Level Security keyed on company_id plus a hardened pool-model data layer (shared schema, RLS, no cross-tenant query path). Replicate Tally's exact Owner/Data-Entry-Operator + custom-level Disallow/Allow rights model as a declarative policy engine (Postgres-stored ACLs evaluated server-side in the voucher/report API, backed by OPA/Rego for complex policy where needed) so migrating users and CAs keep their mental model, while adding MFA (TOTP/WebAuthn), account lockout, and SSO that Tally lacks. Replace TallyVault's broken Modified-DES with envelope encryption: per-tenant AES-256-GCM data keys wrapped by AWS KMS (HSM-backed), with an OPT-IN, fully-audited admin key-escrow recovery path that fixes Tally's "forget password = permanent loss" without weakening the default. Make the MCA Edit Log first-class and always-on as a hash-chained, append-only audit ledger (per-tenant Merkle/hash chain anchored daily) capturing user, time, before/after, and the device/IP/session that Tally omits. Keep all primary data and keys in India (DPDP Act 2023 + RBI/MCA), with 8-year retention per Section 128(5). Treat the move from single-desktop to multi-tenant cloud as a new threat boundary: the desktop's implicit "physical possession = full trust" disappears, so every request is authenticated, tenant-scoped, authorized, and logged with defence-in-depth against cross-tenant leakage as the top risk.

**Chosen stack:** `Keycloak 26 (self-hosted, in-region) for OIDC/OAuth2/SAML2/SCIM identity, MFA (TOTP + WebAuthn/passkeys), brute-force lockout`, `PostgreSQL 16 (Amazon RDS/Aurora PostgreSQL, ap-south-1) as system of record with mandatory Row-Level Security for tenant isolation`, `AWS KMS (HSM-backed, ap-south-1) for envelope encryption; per-tenant data keys wrapping AES-256-GCM; CMK per tenant tier`, `Open Policy Agent (OPA) / Rego as the policy decision point for complex RBAC; Postgres ACL tables as the source of truth replicating Tally Disallow/Allow levels`, `HashiCorp Vault (or AWS Secrets Manager) for application secrets, DB credentials, signing keys; dynamic DB creds`, `Argon2id for password hashing and for deriving any client-side vault keys (replacing Modified-DES KDF)`, `mTLS + TLS 1.3 everywhere (service mesh: Linkerd or AWS App Mesh) replacing Tally's proprietary Modified-DES-over-HTTP`, `Hash-chained append-only audit ledger in Postgres (per-tenant chain, daily Merkle root anchored to AWS QLDB-style or S3 Object Lock WORM) for MCA Edit Log`, `AWS Mumbai (ap-south-1) primary + Hyderabad Local Zone / cross-AZ for India data residency; S3 with Object Lock (compliance mode) for 8-year immutable retention`, `Cloudflare/AWS WAF + rate limiting at the edge; per-tenant request quotas`, `SPIFFE/SPIRE optional for workload identity at scale (deferred past MVP)`

**Key decisions:**

#### Identity provider → **Self-hosted Keycloak in-region rather than Auth0/Okta/Cognito**
DPDP Act + CA/SMB trust demand that PII (emails, phone, auth events) stay in India under our control; Auth0/Okta store identity data in US/EU regions and bill per-MAU at a scale (millions of Tally users) that is economically hostile. Keycloak is boring, proven, supports OIDC/OAuth2/SAML/SCIM and pluggable MFA out of the box, and can run entirely in ap-south-1. It also lets us model the Tally Administrator super-user and Tally.NET-ID login flow exactly.
- *Alternatives rejected:* Auth0; Okta; AWS Cognito; Ory Kratos/Hydra (less batteries-included for SAML/admin UX)
- *Risks:* Operational burden of running/patching Keycloak (mitigate with managed Postgres backing store, blue-green upgrades, and a small SRE runbook); Keycloak clustering/session-replication complexity at very high scale (mitigate with stateless tokens + Infinispan tuning)

#### Tenant isolation model → **Pool model: shared schema with mandatory Postgres Row-Level Security on company_id, NOT schema-per-tenant or DB-per-tenant for the MVP**
Tally has millions of companies; DB-per-tenant or schema-per-tenant does not scale operationally (connection pools, migrations, backups multiply). Shared-schema + RLS gives strong, DB-enforced isolation that survives application bugs: even a missing WHERE clause cannot leak across tenants because the policy is enforced by the engine using a session GUC (app.current_company_id) set from the validated JWT on every connection checkout. Offer dedicated-DB/single-tenant deployment as a paid enterprise tier for customers (or CAs) who require physical isolation.
- *Alternatives rejected:* Schema-per-tenant; Database-per-tenant; Silo/single-tenant only; Application-only filtering without RLS (rejected: one bug = breach)
- *Risks:* RLS bypass if a code path uses a superuser/bypassrls role (mitigate: app connects only as a non-superuser role with FORCE ROW LEVEL SECURITY; CI test that asserts cross-tenant query returns zero rows; no raw connections without GUC set); Noisy-neighbour / large-tenant performance (mitigate: per-tenant rate limits, ability to promote a large tenant to a dedicated cluster, partition hot tables by company_id); Backup/restore granularity per tenant is harder in pool model (mitigate: logical export per company_id via the same RLS path)

#### At-rest encryption (TallyVault equivalent) → **Envelope encryption with AWS KMS-wrapped per-tenant AES-256-GCM data keys, plus OPT-IN audited admin key-escrow recovery**
Tally's Modified-DES/64-bit key with a known-plaintext 0x00000001 shortcut is brute-forceable at ~5-11M guesses/sec — unacceptable. AES-256-GCM via KMS HSM-backed CMKs with a proper KDF (Argon2id where a user passphrase is involved) is the modern standard. The killer improvement: an explicitly opt-in, fully-logged escrow custodian so a forgotten passphrase is recoverable, eliminating Tally's catastrophic permanent-data-loss while keeping the one-click 'encrypt company' UX. Escrow is off by default to preserve the zero-trust option for users who want Tally-equivalent no-backdoor behaviour.
- *Alternatives rejected:* Replicate Tally's no-recovery model exactly (rejected: it is a notorious pain point); Client-side-only E2E encryption with no escrow (rejected for SaaS: breaks server-side AI insights, search, GST reconciliation — the whole product value); Transparent disk encryption only (insufficient: doesn't protect against logical/tenant-level compromise)
- *Risks:* Server-side keys mean an attacker with full prod + KMS compromise could decrypt (mitigate: KMS HSM, strict IAM, key-use audit in CloudTrail, per-tenant CMK so blast radius is one tenant, optional customer-managed keys/BYOK for enterprise); Escrow custodian abuse (mitigate: escrow access requires dual-control approval and writes an immutable audit-ledger entry the tenant Owner can see)

#### RBAC / permission model → **Replicate Tally's level-based Disallow/Allow rights as declarative Postgres ACL tables (Owner, Data Entry Operator, custom levels with 'Use Basic Facilities of' inheritance, access types Full/Create/Alter/Display/Print/Preview/Data Entry/No Access, scoped to Vouchers/Back-Dated/Reports/Statutory/Masters), evaluated server-side; OPA/Rego for cross-cutting policy**
The CA is the real decision influencer and thinks in Tally's exact rights vocabulary; reproducing it verbatim removes a veto. Keeping ACLs as data (not code) lets us ship Tally-identical role templates and the same Disallow/Allow screen. Adding back-dated-voucher day limits, cut-off dates, and per-feature scope matches Tally 1:1. We layer modern additions (MFA enforcement per role, field-level masking) without breaking the familiar model.
- *Alternatives rejected:* Generic RBAC library only (rejected: doesn't capture Tally's deny+allow dual-list and back-dated cutoffs); Pure ABAC/OPA for everything (rejected: too abstract for SMB admins; we keep OPA behind the scenes); Casbin
- *Risks:* Tally's deny-list + allow-list interaction has subtle precedence; getting it wrong blocks or over-permits users (mitigate: port exact precedence rules, golden-test against documented Tally behaviours); Permission checks scattered across services drift (mitigate: single authorization middleware / PDP, deny-by-default)

#### Audit trail / MCA Edit Log → **Always-on, hash-chained append-only ledger per tenant, capturing Created/Altered/Renumbered/Resaved/Deleted with user, UTC+IST time, before/after field values, AND device/IP/session/geo; daily Merkle root written to S3 Object Lock (WORM, compliance mode) for 8-year retention**
MCA Rule 11(g) requires an undisableable audit trail; Section 128(5) requires 8-year retention. Tally ships two binaries (the regular one can disable logging) — we make it structurally impossible to disable and cryptographically tamper-evident, which is strictly stronger and a marketing 'killer feature'. Capturing IP/device/session closes Tally's forensic gap. Hash-chaining + anchored roots gives auditors provable non-repudiation and supports the Rule 11(g) attestation export.
- *Alternatives rejected:* Mutable audit table (rejected: not tamper-evident, fails MCA spirit); Full blockchain (rejected: over-engineered; a per-tenant hash chain + WORM anchor is sufficient and boring); Optional/disable-able log like Tally's standard build (rejected: undermines compliance value)
- *Risks:* Write amplification / storage growth over 8 years (mitigate: partition by FY, cold-tier to S3 Glacier with Object Lock, but keep chain heads online); Before/after capture of sensitive fields must itself respect encryption and access control (mitigate: audit reads gated by audit-view permission, sensitive values stored under the same tenant key)

#### Remote/multi-user access transport → **Replace proprietary Modified-DES-over-HTTP with TLS 1.3 to clients and mTLS between services; web-first + PWA + native mobile, unlimited authenticated seats; keep the Tally.NET-ID login UX**
Tally's wire security was obscurity + weak DES; modern AEAD + mTLS removes that class of risk. Web/PWA kills the RDP/VPS workaround entirely. We preserve the familiar 'Login as Remote User' / Tally.NET-ID flow so users see no change, and we remove Tally's arbitrary 10-user cap and its block on remote Backup/Restore/Import.
- *Alternatives rejected:* Emulate Modified-DES for wire compat (rejected: only needed for legacy Tally peer sync, which we bridge at the data layer, not the transport layer); VPN-only access (rejected: reintroduces friction we're trying to kill)
- *Risks:* Bridging legacy Tally branch sync still requires speaking Tally's encrypted XML for migration/coexistence (handled by a separate, sandboxed migration connector, not the core auth path)

#### Secrets management → **HashiCorp Vault (or AWS Secrets Manager) with dynamic, short-TTL DB credentials and no secrets in env/files/images**
Multi-tenant cloud has a large secret surface (DB creds, KMS grants, GSP/e-invoice API tokens, signing keys). Dynamic creds + rotation + audited access is the proven baseline. GSP/IRP/e-way-bill tokens (6-hour OTP) are stored encrypted per tenant and auto-renewed.
- *Alternatives rejected:* Static secrets in K8s Secrets only (rejected: long-lived, base64 not encrypted by default); Cloud-only Secrets Manager (acceptable alternative; Vault chosen for dynamic DB creds + transit engine)
- *Risks:* Vault is another stateful system to operate (mitigate: HA + auto-unseal via KMS, or use Secrets Manager if team prefers less ops)

**Replication fidelity.** Zero-learning-curve and data-compat are preserved deliberately. Identity reproduces Tally's exact mental model: the Administrator super-user, security levels (Owner, Data Entry Operator) with 'Use Basic Facilities of' inheritance, the two-column Disallow/Allow rights screen, access types (Full/Create/Alter/Display/Print/Preview/Data Entry/No Access) scoped to Vouchers/Back-Dated Vouchers/Reports/Statutory Reports/Masters, days-allowed-for-back-dated-vouchers, cut-off dates, and the per-user Active/Inactive flag — all keystroke-identical (Alt+K Company > Users and Passwords / User Roles). The Tally.NET-ID 'Login as Remote User' flow is preserved as the SSO/remote login UX. TallyVault's one-click 'encrypt this company' UX is kept, including company-name encryption (name hidden in the company list) — but the broken Modified-DES is silently swapped for AES-256-GCM/Argon2id/KMS underneath, and we ADD an opt-in recovery path that Tally users repeatedly ask for. The MCA Edit Log presents in the familiar voucher-alteration/Day Book/Edit Log Summary views with version-compare, so CAs see the same reports — only stronger (tamper-evident, IP/device captured, undisableable). Data-compat is maintained because migration ingests the Tally object model 1:1 and the audit/identity layer wraps it without altering ledger semantics.

**AI angle.** AI plugs into security in three audited, least-privilege ways. (1) Anomaly/fraud detection over the immutable Edit Log: flag suspicious back-dated voucher edits, weekend/after-hours bulk deletions, repeated alterations of the same voucher, round-tripping — surfaced to the Owner/CA, turning a passive compliance log into proactive insight (a capability Tally explicitly lacks). (2) AI-assisted statutory audit: rank vouchers by risk, auto-explain in plain language what changed between two versions, and draft audit observations — replicating Tally Audit's Alt+R/Alt+S accept workflow but smarter. (3) NL-driven RBAC/security setup: 'give the new accountant data-entry on sales but no access to the balance sheet' compiles to the exact Tally Disallow/Allow level config. Critically, AI services themselves are constrained subjects: each runs under a short-lived token scoped to a single company_id, reads only through RLS, cannot escalate, and every AI-initiated action is recorded in the same hash-chained audit ledger and gated by human-in-the-loop approval — so the AI layer never becomes a cross-tenant exfiltration path.

**Interfaces to other domains:**
- Core Accounting/Voucher engine: every voucher/master write and report read passes through the authorization PDP (Tally Disallow/Allow level check + back-dated cutoff) and is wrapped in a single DB transaction that also appends the hash-chained audit row; RLS guarantees company_id scoping.
- Sync/Concurrency domain: tenant_id/company_id and user identity flow into the CRDT/merge engine; every cross-node merge or overwrite must emit an audit-ledger Alteration entry (per MCA) — we expose an append-only audit API the sync engine calls, never a direct mutate.
- AI insight layer: AI services receive a scoped, short-lived service token bound to a single company_id; they read through the same RLS path (no god-mode), and anomaly detection runs over the audit ledger (back-dated edits, after-hours bulk deletes). AI actions are themselves audited and require human-in-the-loop approval gated by RBAC.
- Migration/ingestion: the Tally XML/ODBC and .900 binary importer runs in a sandboxed connector that authenticates to the core via OAuth2 client-credentials scoped to one target company_id; imported data is encrypted under that tenant's key on landing; key-escrow choice is set at company creation.
- GST/Compliance domain: GSP/IRP/e-way-bill API tokens are stored in Vault per tenant and injected at call time; filing actions are RBAC-gated (statutory-report scope) and audited; data residency guarantees the e-invoice/IRN payloads stay in-region.
- Extensibility/TDL-replacement: third-party extensions run with OAuth2 scopes and per-company consent; they cannot bypass RLS or the PDP, and their data access is logged — replacing TDL's unrestricted in-process access.

<details><summary>Detailed engineering writeup</summary>

# ADR: Security, Multi-Tenancy & Identity for an AI-Native Tally Competitor

## Context and the central trust-boundary shift

Tally's security model is built for a single desktop: the trust boundary is *physical possession of the machine and the data folder*. Security control is **off by default**; when on, it's a level-based Disallow/Allow rights system with no MFA, no SSO, no account lockout, and a TallyVault at-rest cipher (Modified-DES, 64-bit key, known-plaintext `0x00000001` shortcut) that ElcomSoft brute-forces at 5–11M guesses/sec. Remote access ran proprietary Modified-DES-over-HTTP — security by obscurity. The MCA Edit Log is strong in intent but shipped as a two-binary kludge where the standard build can disable logging.

Moving this to multi-tenant cloud **deletes the implicit physical trust boundary**. Every byte now sits in shared infrastructure reachable over the internet by millions of tenants. The dominant new threat is **cross-tenant data leakage**; the secondary threats are account takeover (especially Administrator), key compromise, audit tampering, and India regulatory non-compliance (DPDP Act 2023, MCA Rule 11(g), Section 128(5) 8-year retention). The design below is deny-by-default, defence-in-depth, and in-region.

## Identity

Self-hosted **Keycloak 26** in `ap-south-1` is the IdP: OIDC/OAuth2 for our apps/services, SAML2 + SCIM for enterprise CAs, TOTP + WebAuthn/passkeys for MFA, and built-in brute-force lockout (the gap Tally never closed). We model the **Tally Administrator** as a tenant-scoped super-user realm role and preserve the **Tally.NET-ID "Login as Remote User"** UX as ordinary SSO. PII stays in our Postgres-backed Keycloak in India — Auth0/Okta were rejected on residency and per-MAU economics at Tally's user scale.

Tokens are short-lived JWTs carrying `sub`, `tenant_id`, `company_id` (active company), and `security_level`. **MFA is mandatory for Owner/Administrator roles**, optional-but-encouraged below.

## Tenant isolation (the load-bearing decision)

```
Request --> API gateway (authn: validate JWT)
        --> service: extract company_id from JWT claims
        --> DB connection checkout: SET app.current_company_id = <validated company_id>
        --> query runs under RLS; engine appends WHERE company_id = current_setting(...)
```

We use the **pool model**: one shared schema, **mandatory Postgres Row-Level Security** on `company_id`. The app role is a **non-superuser** with `FORCE ROW LEVEL SECURITY`; it can never see another tenant's rows even if a developer forgets a `WHERE` clause — the engine enforces it. This is the single most important invariant. Guardrails:

- App **never** connects as a `BYPASSRLS`/superuser role in request paths.
- A connection without `app.current_company_id` set fails closed (policy returns zero rows / explicit error).
- CI test: issue a cross-tenant query, assert zero rows.
- Annual third-party pen-test focused on tenant escape.

DB-per-tenant and schema-per-tenant were rejected for the MVP — millions of companies make per-tenant migrations/backups/connection-pools operationally untenable. We offer **dedicated-DB single-tenant** and **BYOK** as an enterprise tier for CAs/large firms needing physical isolation.

## At-rest encryption — the TallyVault replacement

Envelope encryption:

```
AWS KMS CMK (HSM, per tenant tier)
   └─ wraps Tenant Data Key (DEK)
         └─ AES-256-GCM encrypts sensitive columns / company blobs
User passphrase (optional vault) --Argon2id--> wrapping key for the DEK
```

This kills the Modified-DES known-plaintext shortcut entirely. We keep Tally's one-click "encrypt this company" UX **including encrypting the company name** (hidden in the company list). The decisive improvement over Tally: an **opt-in, dual-control, fully-audited key-escrow** so a forgotten passphrase is recoverable by an authorized custodian — eliminating Tally's "forget = permanent loss" while leaving a no-backdoor option for purists. Server-side keys are required because our product value (AI insights, GST reconciliation, search) needs to read the data; pure E2E was rejected for that reason. Blast radius is contained by **per-tenant CMKs** and CloudTrail key-use auditing.

## RBAC — Tally fidelity as data, not code

We store Tally's model verbatim in ACL tables:

```
security_level(id, company_id, name, parent_level_id /*"Use Basic Facilities of"*/)
level_rule(level_id, mode /*ALLOW|DISALLOW*/, scope /*Voucher|BackDatedVoucher|Report|StatutoryReport|Master*/,
           object_ref, access_type /*Full|Create|Alter|Display|Print|Preview|DataEntry|NoAccess*/)
level_constraint(level_id, back_dated_days_allowed, back_dated_cutoff_date, ...)
user_company_role(user_id, company_id, level_id, active boolean)
```

A single server-side **Policy Decision Point** evaluates Disallow-then-Allow precedence (ported to match Tally exactly, golden-tested) plus back-dated day/cutoff constraints, before any voucher/master/report op. OPA/Rego sits behind it for cross-cutting policy (MFA-required-for-role, field masking). Deny-by-default. This keeps the CA's exact vocabulary and the identical Alt+K > User Roles screen.

## Immutable audit trail (MCA Edit Log, but stronger)

Always-on, **undisableable**, per-tenant **hash-chained append-only ledger**:

```
audit_entry(id, company_id, seq, prev_hash, this_hash,
            action /*Created|Altered|Renumbered|Resaved|Deleted*/,
            user_id, ts_utc, ts_ist, before_json, after_json,
            ip, device_id, session_id, geo)
this_hash = SHA-256(prev_hash || canonical(payload))
-- daily Merkle root -> S3 Object Lock (compliance/WORM), 8-yr retention
```

Every voucher write happens in the **same DB transaction** that appends the audit row (no skew). We capture **IP/device/session/geo that Tally omits**, giving SOC2/forensic-grade non-repudiation. Tamper-evidence (chain + anchored roots) directly serves the Rule 11(g) auditor attestation and Section 128(5) 8-year retention. Sync/merge overwrites must append an `Altered` entry — history is never silently lost.

## Secrets, transport, residency

- **Secrets**: HashiCorp Vault (or AWS Secrets Manager) — dynamic short-TTL DB creds, rotated signing keys, per-tenant GSP/IRP tokens (6-hour OTP auto-renewed). No secrets in images/env.
- **Transport**: TLS 1.3 to clients, **mTLS** service-to-service via Linkerd. The proprietary Modified-DES wire protocol is gone; we only speak it inside a sandboxed migration connector that bridges legacy Tally branch sync (treated as untrusted input).
- **Residency**: all primary data, keys, audit, and PII in **ap-south-1** (Mumbai) with cross-AZ HA; S3 Object Lock for WORM retention. Designed for stricter localization than DPDP currently mandates so future rule notifications don't force re-architecture. Tenant offboarding uses **crypto-shredding** (destroy the per-tenant key) consistent with retention law.

## Threat model summary (STRIDE-lite)

- **Cross-tenant leak (Information Disclosure)** — top risk → RLS + non-superuser role + CI + pen-test.
- **Account takeover (Spoofing)** — mandatory MFA/WebAuthn for Admin; lockout/backoff.
- **Audit tampering (Tampering/Repudiation)** — hash chain + WORM anchor; undisableable.
- **Key compromise (Information Disclosure)** — KMS HSM, per-tenant CMK, BYOK option, escrow dual-control + audited.
- **Privilege escalation (Elevation)** — deny-by-default PDP; AI/extensions run under scoped single-company tokens, never god-mode.
- **DoS / noisy neighbour** — edge WAF + per-tenant quotas; promote large tenants to dedicated clusters.

</details>

**Open risks:** RLS-based pool isolation is only as strong as the discipline that the app NEVER connects as a bypassrls/superuser role and ALWAYS sets app.current_company_id before any query — a single violating code path is a cross-tenant breach. Needs CI guardrails and pen-testing, not just code review.; Server-side encryption keys mean we are a high-value target: full prod + KMS-grant compromise decrypts tenant data. BYOK/customer-managed-key and dedicated-tenant tiers partially mitigate but cannot be the default for SMBs.; Key-escrow, even opt-in, is a trust and legal liability surface in India; abuse or compelled-disclosure scenarios (LEA requests under DPDP/CrPC) need a documented, dual-control, audited process and clear customer disclosure.; DPDP Act 2023 rules are still being notified (as of 2026); cross-border transfer restrictions, breach-notification timelines, and Consent Manager obligations may tighten — keep all PII in ap-south-1 and design for stricter localization than currently mandated.; 8-year WORM retention of immutable audit + encrypted financials creates a large, long-lived, sensitive data estate that itself becomes a breach/cost liability; lifecycle and crypto-shredding (delete the per-tenant key) strategy must be defined for tenant offboarding while still meeting Section 128(5).; Legacy Tally branch-sync coexistence still requires speaking Modified-DES-over-HTTP in the migration connector — that sandboxed component carries weak crypto we don't control; must be network-isolated and treated as untrusted input.; Account-takeover at the Administrator level is catastrophic (full company control + escrow). Enforce mandatory MFA/WebAuthn for Owner/Admin roles, not optional.; Performance/cost of evaluating fine-grained Tally-style permissions and appending hash-chained audit rows on every voucher write at SMB scale needs load validation; the audit append must not become the write bottleneck.

---

## B.9 Extensibility Platform (modern, open, AI-assisted TDL replacement)

**Recommendation.** Replace TDL with a two-layer extensibility platform that preserves TDL's definition/override mental model while replacing its closed toolchain. Layer 1 is a declarative, JSON-Schema-validated DSL ("FlowDef") covering ~80% of real-world TDL work — custom fields (UDFs), print/document templates, custom reports built over the same Collection/aggregation model, validations, menu/button/key bindings, and screen overrides. It is data, not code: deterministic, diffable, safe, and the natural target for the AI copilot. Layer 2 is sandboxed code (TypeScript/JS executed in a V8 isolate via the host runtime, plus server-side WASM modules) for the remaining ~20% — procedural validations, computed fields, custom integrations, and event handlers — gated by an explicit capability/permission manifest. Both layers attach to the host through a single stable, versioned, semantic Extension API and a typed object model (Company, Group, Ledger, StockItem, Voucher, etc.) so customizations survive upgrades via semantic anchors instead of TDL's brittle name-based [#Report] overrides. Authoring is Git-native with a hosted web Studio, hot reload, automated test harness, and an LLM copilot (Claude) that generates and edits FlowDefs and sandboxed handlers from natural language. Distribution is a curated multi-tenant marketplace with semver, signed bundles, escrowed source, subscription billing, ratings, and Control-Centre-style site-wide rollout. A best-effort .tdl/.tcp importer seeds the marketplace by transpiling the most common add-on shapes (print formats, UDFs, simple reports) into FlowDefs. This is deliberately boring at the core (declarative config + sandboxed isolates, both proven patterns from Shopify Functions, Stripe Apps, Zendesk, Salesforce) with novelty confined to the AI authoring and semantic-override layers where it is clearly justified.

**Chosen stack:** `FlowDef declarative DSL: JSON/YAML authored, validated against published JSON Schema (Draft 2020-12), compiled to a versioned IR — covers fields/UDFs, document templates, reports, validations, menus/keys, screen overrides`, `Document/print templates: typed React-like component schema rendered to PDF via a headless server-side renderer (Satori + resvg for SVG->PNG/PDF, or Chromium/Playwright pool for complex layouts) — keyboard-identical default screens stay native, templates only theme output`, `Sandboxed client extension code: TypeScript compiled to JS, executed in a per-tenant V8 isolate (isolated-vm in the Node host, or QuickJS-ng in the desktop/offline app) with frozen host bridge and CPU/memory/time quotas`, `Server-side extension modules / custom integrations: WebAssembly (compiled from Rust/AssemblyScript/TinyGo) run in Wasmtime with WASI-preview2 component model, fuel metering, and an explicit host-function import list = the capability set`, `Extension API surface: a single versioned gRPC + JSON contract generated from Protobuf, exposed to extensions as a typed SDK (TypeScript + Python), backing the same XML/JSON ENVELOPE compatibility endpoint`, `Event bus for extension triggers: NATS JetStream (voucher.created, payment.received, gstr2b.mismatch, master.altered) with at-least-once delivery, replaced/augmented by Postgres LISTEN/NOTIFY for in-tenant low-latency hooks`, `Authoring toolchain: Git-native (GitHub/GitLab), a hosted web Studio (Monaco editor + live preview + hot reload over WebSocket), CLI (npm-distributed) for local dev, and an LLM copilot powered by Claude (Opus for authoring, Haiku for inline completions) via the Anthropic API with tool-use over the published JSON Schema + Extension API spec`, `Marketplace + registry: OCI-compatible artifact registry for signed extension bundles, Sigstore/cosign signatures, semver resolution, Postgres metadata, Stripe/Razorpay for subscription billing, source escrow in encrypted object storage`, `Manifest + permissions: a signed extension.yaml manifest declaring capabilities (read:ledgers, write:vouchers, network:egress[domain], ui:override[anchor]), enforced at the host bridge`, `Override/compat model: semantic anchors (stable IDs on every overridable screen region, report column, voucher field) plus a TDL-compat shim that maps legacy Report/Form/Part/Line/Field names to anchors for the importer`

**Key decisions:**

#### Customization runtime: config/DSL vs sandboxed code vs both → **Both — a declarative FlowDef DSL as the primary surface (the 80% case and the AI target), with sandboxed code (V8 isolate on the client, WASM on the server) as an explicit escape hatch for the procedural 20%.**
TDL itself is effectively this hybrid: most add-ons are declarative definitions (print formats, UDFs, reports) while Functions add procedural logic. A pure-code SDK would be powerful but unsafe in a multi-tenant cloud, impossible to statically verify, and a poor target for an LLM (hallucinated code is hard to gate). A pure-config DSL cannot express the integrations and computed validations partners actually sell. Declarative-first means most extensions are pure data: diffable, deterministic, statically validatable, trivially sandboxed, and the ideal output format for the Claude copilot (generate JSON conforming to a published schema, then validate before commit). Code is reserved for cases the DSL genuinely cannot express, and is always capability-gated and metered.
- *Alternatives rejected:* Pure sandboxed code SDK (like a generic plugin API); Pure declarative DSL (config-only, no code); Re-implement a TDL-syntax interpreter for literal source compatibility; Embed a Lua/Python interpreter as the single runtime
- *Risks:* Two runtimes = two security/perf surfaces to maintain and document; Partners must learn when to drop from DSL to code; bad guidance leads to overuse of the code escape hatch; DSL expressiveness ceiling will be hit by edge cases and create pressure to keep expanding the schema

#### How partner customizations survive product upgrades → **Stable, versioned semantic Extension API + named semantic anchors (stable IDs on overridable regions/columns/fields) with semver contracts and a deprecation policy — never name-based source overrides like TDL's [#Report].**
TDL's single biggest tax is that override-by-name and the rigid Report>Form>Part>Line>Field containership break on every product release and on ERP9->Prime migration. We invert this: every overridable surface exposes an explicit, documented anchor ID and the host guarantees anchors are additive within a major version (anchors may be deprecated with a migration window but not silently removed). Extensions declare the API major version they target; the host runs a compatibility shim for older majors. This turns 'TDL broke after the update' from the norm into a contract violation we own and test against, and lets us run a marketplace-wide compatibility CI that flags affected extensions before a release ships.
- *Alternatives rejected:* Name-based textual override (TDL's [#Report]/Add/Delete/Replace) — rejected as the exact pain we are displacing; Fork-and-merge model where partners copy and re-merge product definitions; No stability guarantee; partners re-test every release manually
- *Risks:* Anchor surface becomes an API we can never freely refactor — long-term agility cost on core UI/reports; Granularity choice: too few anchors limits customization, too many ossifies the product; Semantic anchors still cannot protect against deep behavioral changes (e.g. a voucher field changing meaning under GST 2.0)

#### Client-side sandbox technology for in-app extension logic → **V8 isolates via isolated-vm in the cloud/Node host and QuickJS-ng in the offline-first desktop app, executing TypeScript-compiled JS with a frozen host bridge and CPU/memory/wall-time quotas.**
Isolates give true memory isolation between tenants at low overhead and are battle-tested at scale (Cloudflare Workers, Shopify, Figma plugins use the same family). TypeScript is the lingua franca for the partner/dev hiring pool we want to tap — directly attacking TDL's niche-hiring problem — and gives the copilot a language with rich type information. QuickJS-ng on desktop keeps the offline-first app able to run the same extension logic without shipping a full V8, preserving identical behavior online and offline.
- *Alternatives rejected:* Run extension code in the same process/realm (no isolation) — unacceptable multi-tenant risk; Server-only execution of all extension code (kills offline + interactive UI extensions); WASM for client-side too (worse DOM/UI ergonomics than JS for form/field logic)
- *Risks:* isolated-vm pins to specific Node/V8 versions and adds native-build/ops burden; Behavioral parity between isolated-vm and QuickJS-ng must be tested or extensions diverge online vs offline; Untrusted JS still needs strict timeouts and resource caps to prevent tenant-noisy-neighbor

#### Server-side / integration extension runtime → **WebAssembly components (Wasmtime, WASI-preview2 component model) with fuel metering and an explicit host-import capability list, for custom integrations, connectors, and heavy data transforms.**
Server-side connector code (e-commerce sync, custom GSP flows, bespoke imports) needs hard multi-tenant isolation, deterministic resource metering for billing/abuse control, and language flexibility (Rust/Go/AssemblyScript). WASM components give all three with a capability-secure import model: an extension can only call host functions explicitly granted in its manifest (e.g. network egress to a named domain, write:vouchers). Fuel metering lets us bill and throttle per execution. This is the right home for the 'always-on cloud connector' that replaces Tally's desktop-bound middleware.
- *Alternatives rejected:* Containers/microVMs per extension (gVisor/Firecracker) — heavier, slower cold start, costlier for thousands of small extensions; Same V8 isolate as client code — weaker capability model and language lock-in; Trusted partner code in shared workers — no isolation, no metering
- *Risks:* Component-model tooling (WASI p2) is still maturing in 2026; some language toolchains lag; Capability granularity and audit of granted host imports is security-critical and easy to get wrong; Cold-start and per-call overhead must be managed for latency-sensitive UI-triggered handlers

#### AI-assisted authoring scope and guardrails → **Claude-powered copilot in the Studio and CLI: generates/edits FlowDefs (preferred) and scaffolds sandboxed handlers from natural language, using tool-use grounded in the published JSON Schema + Extension API spec, with mandatory schema validation, test generation, and human review before any deploy.**
Natural-language extension authoring ('add a WhatsApp send button to the sales invoice', 'flag invoices where GST rate is inconsistent') is the headline differentiator over TDL and is only safe because the primary output is declarative JSON that we validate against a schema before it can run. The copilot biases toward FlowDef and only emits code when necessary; generated artifacts go through the same validation, capability review, and CI as hand-written ones, so AI never bypasses the security model. This directly attacks TDL's weak-debugging, no-version-control, niche-hiring pains.
- *Alternatives rejected:* No AI authoring (just better SDK) — leaves the headline TDL pain on the table; Let AI emit arbitrary code directly to production — unsafe, ungated; Fine-tuned in-house small model — premature; off-the-shelf Claude with schema-grounded tool-use is stronger and faster to ship
- *Risks:* LLM can produce plausible-but-wrong logic that passes schema validation but is semantically incorrect — mandatory tests + human review are the mitigation, not optional; Copilot quality depends on schema/spec coverage; gaps push users to the code escape hatch; Cost/latency of large-model authoring at scale — mitigated by Haiku for inline, Opus for full generation

#### TDL/.tcp migration strategy → **Ship a best-effort open-source .tdl source transpiler that converts the common add-on shapes (print formats, UDF definitions, simple Collection-based reports, menu/key bindings) into FlowDefs, plus a manual-assist path; do NOT attempt to decompile encrypted .tcp binaries.**
.tdl source is human-readable and its definition grammar maps cleanly onto FlowDef anchors and the Collection model, so a transpiler can migrate a large fraction of partner catalogs and instantly seed the marketplace — turning 28k TDL partners from a moat into our supply side. .tcp is encrypted/source-protected with undocumented internals; reliable decompilation is infeasible and legally fraught, so we route those through partner-provides-source or rebuild-via-copilot instead. Anything the transpiler cannot handle is flagged for the copilot to rebuild from the described behavior.
- *Alternatives rejected:* Full .tcp binary decompiler — infeasible and legally risky; Runtime TDL interpreter for literal compatibility — perpetuates the closed mental model and brittle overrides; No migration path — abandons the partner channel, the single biggest seeding lever
- *Risks:* Transpiler coverage will be partial; partners may perceive migration as incomplete; Legal exposure if importing source the partner does not own — must require ownership attestation; Maintaining the transpiler against TDL's evolving surface (Prime 5.x/7.x) is ongoing effort

#### Distribution, trust, and rollout model → **Curated multi-tenant marketplace: signed (Sigstore/cosign) bundles in an OCI registry, semver dependency resolution, capability-manifest review at publish time, escrowed source, subscription + one-time billing, ratings, and Control-Centre-style web-native site-wide/per-site rollout with staged canary and instant rollback.**
Breaks Tally's single-partner .tcp lock-in (managed auto-updates, ratings, escrow so customers are not hostage to one reseller) and gives enterprises the central-deployment control they rely on in Control Centre, but web-native and safer (canary + rollback). Signing + capability review + escrow is the trust spine that lets us safely run third-party code in a multi-tenant compliance-critical system.
- *Alternatives rejected:* Open free-for-all registry (npm-style) — unacceptable for code touching financial/GST data; Closed first-party-only extensions — abandons the 28k-partner ecosystem advantage; Per-customer manual file install (TDL status quo) — no updates, no trust, no scale
- *Risks:* Marketplace review at scale needs heavy automation (static analysis, capability diffing) or it becomes a bottleneck; Take-rate/economics must beat TallyShop to win partners; unclear partner willingness to escrow source; Malicious or compromised popular extension is a systemic risk — needs kill-switch + capability-scoped blast radius

**Replication fidelity.** Zero-learning-curve is preserved by separating the runtime from the UI: default data-entry and report screens remain native and keystroke-identical (F-keys, Alt+G, Enter-drill/Esc-back, Ctrl+A accept, auto-complete pick-lists), and extensions only attach at semantic anchors rather than rebuilding screens. Partners keep their exact mental model — Report/Form/Part/Line/Field, Collections, UDFs, $/$$ value access, Menu/Button/Key, override-by-use — now expressed as validated FlowDef data instead of TDL source. Data compatibility is preserved two ways: the .tdl transpiler migrates existing partner customizations, and the Extension API backs an XML/JSON ENVELOPE-compatible endpoint so existing TDL/connector code (Suvit, Webtel, tally-database-loader) runs unchanged. CAs see the same screens, the same compliance behavior, and an immutable Edit Log that is stronger than Tally's.

**AI angle.** AI plugs in at three points. (1) Authoring: a Claude copilot (Opus for generation, Haiku for inline) turns natural language into FlowDefs and scaffolded handlers via schema-grounded tool-use, with mandatory validation, test generation, and human review — the headline differentiator over TDL and only safe because the primary output is declarative JSON validated before execution. (2) Migration: the copilot rebuilds add-ons the .tdl transpiler cannot handle, working from described behavior. (3) First-party AI extensions: GSTR-2B reconciliation with anomaly flags, duplicate/fraud voucher detection, cash-flow forecasting, and natural-language report building ship as privileged extensions on the very same platform, proving the SDK is powerful enough for our own hardest features.

**Interfaces to other domains:**
- Core Accounting/Inventory engine: extensions read/write the typed object model (Company, Group, Ledger, StockItem, Voucher, CostCentre, Bill, UoM) only through the versioned Extension API; UDFs extend these objects and are persisted alongside core data
- UI/Keystroke layer: extensions attach to semantic anchors on native screens (button bars, report columns, voucher field clusters) so default screens stay keystroke-identical while customizations layer on top
- Compliance/Audit (GST/Edit Log): all extension-driven mutations route through the immutable hash-chained Edit Log; statutory IRN/e-way-bill/GSTR payloads are first-party and not extension-overridable
- Integration/API layer: the Extension API and event bus back the XML/JSON ENVELOPE-compatible endpoint, so legacy TDL/connector traffic and new partner integrations share one auth-wrapped surface
- AI Insight layer: first-party AI add-ons (GSTR-2B reconciliation, anomaly/fraud detection, cash-flow forecasting) ship as privileged extensions on the same platform; the authoring copilot consumes the same JSON Schema + API spec
- Identity/Tenancy + Marketplace: per-tenant capability grants, signed bundles, staged rollout, and billing tie into the platform's auth, multi-tenant DB, and Control-Centre-style admin

<details><summary>Detailed engineering writeup</summary>

## Goal and framing

TDL is simultaneously Tally's defensibility moat (28k partners, thousands of add-ons) and a deep liability: a closed 4GL, a proprietary IDE (Tally.Developer 9), no version control, brittle name-based overrides that break every release, encrypted `.tcp` files that lock customers to one reseller, and a desktop-file deployment model. Our extensibility platform must **preserve the mental model that partners and CAs already think in** — Report/Form/Part/Line/Field, Collections, UDFs, `$`/`$$` value access, Menu/Button/Key triggers, and override-by-use — while replacing the toolchain, runtime, distribution, and security with modern, open, AI-assisted equivalents. The design goal is not syntactic compatibility with TDL; it is **conceptual continuity with operational modernity**.

## Two-layer runtime: declarative-first, code-as-escape-hatch

The central decision is the runtime model, and the answer is **both**, deliberately ordered.

**Layer 1 — FlowDef (declarative DSL).** Authored in JSON/YAML, validated against a published JSON Schema (Draft 2020-12), compiled to a versioned intermediate representation. FlowDef covers the high-frequency TDL work that drives the marketplace today:

- **Custom fields (UDFs):** typed field definitions attached to objects (Ledger, StockItem, Voucher, etc.) with storage, validation, and default expressions.
- **Document/print templates:** invoice/PO/quotation layouts as a typed component tree, themed and bound to object data — rendered server-side to PDF. Default data-entry screens stay keystroke-identical and native; templates only affect output.
- **Custom reports:** built over the same Collection/aggregation primitives Tally exposes (`Walk`/`Fetch`/`Compute`/`By`/`Aggr` analogues) so partners' reporting mental model transfers directly.
- **Validations:** declarative constraints and cross-field rules with expression support.
- **Menu / Button / Key bindings:** add an item to Gateway, bind a key to an action, add a button to a voucher — the TDL Menu/Button/Key model, expressed as data.
- **Screen overrides:** target a **semantic anchor** and add/replace/remove a region.

FlowDef is *data, not code*: diffable in Git, deterministic, statically validatable, trivially sandboxed, and — critically — the **ideal output format for the Claude copilot**, which can be constrained to emit schema-conforming JSON that we validate before it ever runs.

**Layer 2 — Sandboxed code.** For the procedural ~20% (computed fields, multi-step validations, custom integrations, event handlers) we expose two sandboxes:

- **Client/interactive logic:** TypeScript→JS in a **V8 isolate** (`isolated-vm` in the cloud Node host; **QuickJS-ng** in the offline-first desktop app for identical online/offline behavior). A frozen host bridge plus CPU/memory/wall-clock quotas contain it.
- **Server-side / integrations:** **WebAssembly components** (Wasmtime, WASI-preview2) compiled from Rust/Go/AssemblyScript, with **fuel metering** and an explicit **host-import capability list**. This is the home for the always-on cloud connectors that replace Tally's desktop-bound middleware.

```yaml
# extension.yaml (manifest = the security contract)
id: acme.whatsapp-invoice
version: 1.4.2
targetApi: "v2"            # major version; host runs a compat shim for older majors
capabilities:
  - read:vouchers
  - read:ledgers
  - ui:override[voucher.sales.buttonbar]   # semantic anchor, not a TDL name
  - network:egress[graph.facebook.com]
runtime:
  flowdefs: [buttons/whatsapp.flow.json]
  handlers: [src/send.ts]   # V8 isolate; only the declared capabilities are reachable
signature: sigstore://...
```

## Surviving upgrades: semantic anchors, not name overrides

TDL's `[#Report: ...]` textual override and the rigid `Report>Form>Part>Line>Field` containership are the root cause of the "TDL broke after the update / ERP9→Prime migration" tax. We replace them with **semantic anchors**: every overridable surface (a button bar, a report column group, a voucher field cluster) carries a stable, documented anchor ID. Extensions target anchors, not source names.

Contract:
- Within a **major API version**, anchors are *additive* — never silently removed; deprecation requires a migration window and is announced.
- Extensions declare `targetApi`; the host runs a **compatibility shim** for older majors.
- We run a **marketplace-wide compatibility CI** before every product release: replay every published extension's tests against the release candidate and flag breakage *before* shipping. This turns regressions from the partner's problem into our owned, tested contract.

This is the difference between "customizations are brittle by design" and "breaking a customization is a contract violation we detect in CI."

## Authoring: Git-native, hot-reload, AI copilot

- **Git-native** source for every extension (FlowDefs + handlers + tests), real PR review, real diffs — directly fixing TDL's no-version-control pain.
- **Web Studio** (Monaco + live preview + hot reload over WebSocket) and an **npm-distributed CLI** for local dev.
- **Claude copilot**: Opus for full generation, Haiku for inline completions, via Anthropic API tool-use **grounded in the published JSON Schema + Extension API spec**. "Add a WhatsApp send button to the sales invoice" → the copilot emits a FlowDef button bound to the `voucher.sales.buttonbar` anchor plus a capability-scoped handler, generates tests, and runs schema validation. **AI never bypasses the security model**: generated artifacts pass the same validation, capability review, and CI as hand-written ones. The copilot biases to FlowDef and only drops to code when the DSL cannot express the requirement.

## Events and the integration surface

A first-party **event bus** (NATS JetStream for cross-service, Postgres LISTEN/NOTIFY for in-tenant low latency) emits `voucher.created`, `payment.received`, `gstr2b.mismatch`, `master.altered`, etc. Partners subscribe instead of polling — something Tally has never offered. The same Extension API also backs the **XML/JSON ENVELOPE compatibility endpoint**, so existing TDL/connector code that POSTs the legacy `<ENVELOPE><HEADER><BODY>` format keeps working against the cloud, with modern auth (API keys/OAuth2/TLS, per-company scopes) layered on the surface Tally left unauthenticated.

## Migration: transpile `.tdl`, never decompile `.tcp`

`.tdl` source is readable and maps cleanly onto FlowDef anchors and the Collection model, so an **open-source transpiler** migrates the common add-on shapes (print formats, UDFs, simple reports, menu/key bindings) — converting the 28k-partner catalog from a moat into our supply side and **seeding the marketplace instantly**. Encrypted `.tcp` is undocumented and legally fraught; we do **not** attempt decompilation — those route through partner-supplied source or copilot rebuild-from-behavior. Imports require an **ownership attestation** to manage legal exposure.

## Distribution and trust

A curated **multi-tenant marketplace**: signed bundles (Sigstore/cosign) in an OCI registry, semver resolution, **capability-manifest review at publish**, **source escrow**, subscription + one-time billing (Stripe/Razorpay), ratings, and a web-native **Control-Centre-style rollout** with staged canary and instant rollback. This breaks single-partner `.tcp` lock-in, gives enterprises central deployment control, and — with signing + capability scoping + a global kill-switch — keeps the blast radius of a malicious or compromised extension contained.

## Compliance interplay (load-bearing)

Extensions touch GST/voucher data, so the platform inherits compliance constraints: every extension-driven create/alter/delete flows through the same **immutable MCA Edit Log** (hash-chained per node), capability grants are themselves audited, and statutory output schemas (e-invoice IRN, e-way bill, GSTR) remain **first-party and not overridable** by extensions — partners may add to invoices but cannot alter the compliance payload. This is what lets us safely run third-party and AI-generated code in a system of record.

</details>

**Open risks:** Semantic-anchor surface becomes an API we can never freely refactor — long-term constraint on evolving core UI and reports; anchor granularity is a one-way door; Maintaining two sandbox runtimes (V8 isolate online + QuickJS-ng offline) risks behavioral divergence between online and offline execution of the same extension; needs a shared conformance test suite; WASI-preview2 component-model tooling is still maturing in 2026; some partner language toolchains (Go/AssemblyScript) may lag, pushing partners toward the JS isolate; Marketplace security review does not scale by hand — needs automated static analysis + capability-diffing or publishing becomes a bottleneck; a compromised popular extension is a systemic, compliance-grade risk requiring kill-switch and tight capability scoping; FlowDef expressiveness ceiling will be hit by long-tail TDL behaviors, creating constant pressure to expand the schema or over-route partners to the code escape hatch; AI copilot can emit semantically-wrong-but-schema-valid logic; mitigation (mandatory tests + human review) is only as good as the generated tests; Partner willingness to escrow source and accept a marketplace take-rate is unproven; if economics do not beat TallyShop, the 28k-partner channel may not migrate; Legal exposure on .tdl import if partners submit source they do not own — ownership attestation is a control, not a guarantee; TDL surface keeps evolving (Prime 5.x/7.x, GST 2.0, connected services); the transpiler and anchor mapping require ongoing maintenance to stay current

---

## B.10 Infrastructure, DevOps, Scalability & the Agentic Build Process

**Recommendation.** Run the entire platform on AWS in the Mumbai region (ap-south-1) with the Hyderabad region (ap-south-2) as an in-country DR pair, keeping all customer financial data, backups, and audit logs inside Indian borders to satisfy DPDP-Act residency and CA/auditor expectations. Compute is containerized stateless services on EKS (Kubernetes) behind a thin set of long-running Go/Rust services, NOT serverless functions, because the keystroke-grammar voucher engine needs warm low-latency in-region request handling and predictable connection pooling to Postgres; reserve serverless (Lambda/Fargate jobs) only for spiky async work (OCR, GST portal polling, scheduled reconciliations). The system of record is Amazon Aurora PostgreSQL (row-level multi-tenant, tenant_id on every row, RLS enforced) for ACID ledger correctness — directly eliminating Tally's .900 corruption/rewrite class of failures — with a per-tenant logical sharding path for the largest accounts, ClickHouse for OLAP/report acceleration (replacing Tally's in-RAM Aggr.1800 model so multi-year history stays hot), Redis for cache/locks, and S3 (Object Lock, WORM) for the hash-chained immutable audit trail and document blobs. CI/CD is GitHub Actions to a GitOps (Argo CD) flow with progressive delivery; observability is OpenTelemetry into Grafana/Tempo/Loki + Prometheus with golden-signal SLOs. The desktop offline-first app is Tauri (Rust core, web UI) distributed via signed installers with delta auto-update through its built-in updater fed from S3+CloudFront. DR target is RPO <= 5 min / RTO <= 1 hr via Aurora continuous backup + cross-region replication. The engineering org builds primarily through DYNAMIC AGENT WORKFLOWS: a polyglot monorepo with hard machine-enforced verification gates (typecheck, golden keystroke-conformance tests, property-based ledger-invariant tests, a compliance test suite, security scans) that every agent-generated PR must pass before a human reviewer — who reviews intent and the diff, never unreviewed merges — approves. This makes agent velocity safe because correctness is gated by deterministic tests, not by trust.

**Chosen stack:** `AWS Mumbai (ap-south-1) primary, Hyderabad (ap-south-2) DR — India data residency for DPDP Act`, `Amazon EKS (Kubernetes 1.31+) with Karpenter autoscaling; Graviton (arm64) nodes for cost; Bottlerocket OS`, `Stateless services in Go (voucher/ledger engine, sync) and TypeScript/Node (BFF, API gateway layer)`, `Amazon Aurora PostgreSQL 16 (multi-tenant, Row-Level Security, partitioning) as system of record`, `ClickHouse (self-managed on EKS or ClickHouse Cloud in ap-south-1) for OLAP report acceleration`, `Amazon ElastiCache (Redis 7) for cache, rate limits, advisory locks`, `Amazon S3 with Object Lock (WORM, compliance mode) for immutable audit trail + document store; SSE-KMS`, `AWS KMS + per-tenant data keys (envelope encryption); AES-256-GCM; Argon2id KDF for tenant vault passwords`, `Apache Kafka (MSK) for the event/CDC backbone (voucher events, audit append, AI triggers)`, `GitHub + GitHub Actions CI; Argo CD GitOps; Argo Rollouts for canary/progressive delivery`, `Tauri 2 (Rust shell + shared web UI) for offline-first desktop (Win/Mac/Linux); SQLite local store; built-in signed delta auto-update via S3/CloudFront`, `OpenTelemetry SDK -> Grafana stack (Prometheus, Tempo, Loki, Grafana, Mimir); Sentry for client error tracking`, `Terraform (infra) + Helm (app) as code; Atlantis for plan/apply review`, `Trivy + Semgrep + gitleaks + Dependabot in CI; Falco runtime security on EKS`, `Pulsar/Temporal (Temporal on EKS) for durable agentic/async workflows (OCR pipeline, GST polling, recon jobs)`, `Anthropic Claude (Opus/Sonnet) as the primary LLM for both the product AI layer and the internal agentic build workflows, via Amazon Bedrock in ap-south-1 where available, falling back to Anthropic API with India data-handling terms`

**Key decisions:**

#### Cloud provider and region for India data residency → **AWS, primary ap-south-1 (Mumbai), DR ap-south-2 (Hyderabad). All financial data, audit logs, backups, and KMS keys pinned in-country.**
India's DPDP Act 2023 plus auditor/CA comfort and government tendency toward data-localization make in-country residency a sales and compliance prerequisite, not a nice-to-have. AWS has two India regions (Mumbai + Hyderabad) enabling true in-country active/passive DR without leaving the jurisdiction — Azure (Central/South/West India) and OCI also qualify, but AWS has the deepest India SMB/partner ecosystem and the broadest managed-service catalog (Aurora, MSK, EKS, KMS) we depend on. Tally's own cloud runs on OCI/AWS, so this is familiar ground for the channel.
- *Alternatives rejected:* GCP (only one India region, Mumbai/Delhi NCR — weaker in-country DR pairing at project start); Azure India (viable; rejected for thinner managed-OLAP story and our team's AWS depth); Multi-cloud from day one (rejected: premature complexity, slows MVP, dilutes ops expertise)
- *Risks:* Single-cloud lock-in to AWS managed services; Region-pair correlated failure (mitigated by S3 cross-region + Aurora global); Bedrock model availability in ap-south-1 may lag; may need cross-region LLM calls with data-handling review

#### Compute model: containers/Kubernetes vs serverless → **Containerized long-running services on EKS for the core (voucher/ledger engine, API, sync, reporting). Serverless (Fargate tasks, Lambda, Temporal workers) only for spiky async jobs: OCR, GST/e-invoice portal polling, scheduled reconciliation, exports.**
The keystroke-grammar voucher path is latency-sensitive interactive traffic (sub-100ms server response so Enter-to-drill/Ctrl+A-accept feels native). Long-running pods keep DB connection pools warm, hold per-tenant caches, and avoid cold starts and per-invocation pricing that punish chatty interactive workloads. Postgres connection limits also make raw Lambda-to-Aurora a known anti-pattern (needs RDS Proxy and still risks pool exhaustion). Kubernetes gives us bin-packing on Graviton, multi-tenant noisy-neighbor controls, and a single deploy substrate for stateful sidecars (ClickHouse, Redis) we control. Serverless is still ideal for bursty, idempotent, infrequent jobs where scale-to-zero saves real money.
- *Alternatives rejected:* All-serverless (Lambda + DynamoDB): rejected — DynamoDB cannot express the relational double-entry/ledger integrity model cleanly, and cold-start/connection limits hurt the interactive voucher path; ECS Fargate only (simpler than EKS): viable for MVP, rejected long-term because we want the richer K8s ecosystem (Argo, Karpenter, Falco, operators) and portability; Raw EC2 + systemd: rejected — reinvents orchestration the team would maintain by hand
- *Risks:* EKS operational complexity and on-call burden; Kubernetes CVE churn requires disciplined patching (mitigated by Bottlerocket + managed node groups); Over-provisioning cost if autoscaling is mistuned

#### System of record data store → **Amazon Aurora PostgreSQL 16, single logical multi-tenant cluster with tenant_id on every row enforced by Row-Level Security; table partitioning by tenant+financial-year; a documented path to move the largest tenants to dedicated clusters. ClickHouse for OLAP report acceleration; Redis for cache/locks; S3 Object Lock for the immutable audit trail.**
Tally's #1 trust failure is .900/TranMgr corruption and the Ctrl+Alt+R rewrite ritual; ACID + WAL in a managed relational DB eliminates that entire class. Double-entry accounting is inherently relational (ledgers, vouchers, bill-wise links, cost centres) and maps 1:1 to Tally's object/method/collection model while being queryable — unlike Tally's opaque in-RAM graph. RLS gives defense-in-depth multi-tenant isolation. ClickHouse replaces Tally's Aggr.1800 pre-aggregation so unlimited multi-year history yields sub-second Balance Sheet/P&L without the 'Split Company Data' workaround. S3 Object Lock (WORM) makes the MCA Rule 11(g) edit log provably immutable and satisfies the 8-year Section 128(5) retention.
- *Alternatives rejected:* Database-per-tenant from day one (strong isolation but thousands of SMB tenants make migrations/ops/cost untenable at MVP scale — reserved for top-tier accounts); Schema-per-tenant (Postgres connection/catalog bloat at high tenant count); DynamoDB/Spanner (rejected — relational ledger integrity and ad-hoc reporting are awkward); CockroachDB (compelling for geo-distributed write scale, rejected for MVP: smaller India operational track record, Aurora is the boring proven choice)
- *Risks:* Row-level multi-tenancy means a missing tenant_id predicate is a cross-tenant data leak — must be enforced by RLS AND ORM guards AND tests; Largest tenants could become noisy neighbors before sharding kicks in; ClickHouse eventual-consistency vs Postgres source-of-truth requires careful CDC and reconciliation

#### Multi-tenant sync / concurrency model (replacing Tally's directional-overwrite sync) → **Single live cloud ledger per tenant as source of truth; clients (web/mobile/desktop) sync via an append-only event log with node-partitioned globally-unique IDs (UUIDv7 + node prefix). Offline desktop/mobile edits queue locally (SQLite) and replay; conflicts surface in an explicit resolution queue rather than silent last-syncer-wins. Every cross-node write is appended to the hash-chained audit ledger.**
Tally sync silently destroys the loser's edits (Synchronise Altered Transactions = Yes overwrites) and hard-fails on GUID/voucher-number collisions. Node-partitioned IDs make collisions structurally impossible; human-friendly per-series numbers (INV/2026-27/0001) are a display layer over the stable key. An append-only log + explicit conflict queue is auditable and trustworthy for CAs, and the hash chain gives tamper-evidence stronger than Tally's view-only log.
- *Alternatives rejected:* Full CRDT field-level auto-merge (powerful but accounting needs human-approved conflict resolution for legal/audit reasons — auto-merging financial figures is unacceptable); Tally-style directional overwrite (rejected — the exact pain we are displacing); Strict server-only, no offline (rejected — offline-first desktop is a stated product pillar)
- *Risks:* Offline edit windows can produce large conflict queues if a branch is disconnected for days; Hash-chain verification cost at scale; Sequence/series numbering under concurrency needs careful gap/lock handling

#### CI/CD and the agentic build verification gates → **GitHub Actions CI -> Argo CD GitOps with Argo Rollouts canaries. Every PR (the vast majority agent-generated) must pass machine-enforced gates before any human review: compile/typecheck, unit + property-based ledger-invariant tests (debits==credits, no orphan postings), a golden KEYSTROKE-CONFORMANCE suite that asserts F-key/Alt+G/Ctrl+A grammar exactly matches the Tally spec, a COMPLIANCE test suite (GSTR-1/3B/e-invoice IRN/e-way-bill schema conformance, audit-log immutability), Semgrep/Trivy/gitleaks security scans, and migration safety checks. Merge to main is blocked until green; deploy is automatic to staging, gated/canaried to prod.**
Agent-generated code is fast but untrustworthy by default. Safety must come from deterministic, machine-checkable contracts, not reviewer goodwill. Property-based tests pin the non-negotiable accounting invariants; golden keystroke tests pin the zero-learning-curve promise so an agent cannot silently regress muscle memory; the compliance suite pins the killer feature. GitOps gives a fully auditable, revertible deploy trail (itself useful for SOC2). Canary + auto-rollback contains the blast radius of any agent regression that slips through.
- *Alternatives rejected:* Human-reviews-everything-line-by-line (does not scale with agent velocity; reviewers rubber-stamp); Trust-the-agent auto-merge with no gates (unacceptable for a financial system); Jenkins/self-hosted CI (rejected — more ops burden than GitHub Actions for our scale); Spinnaker for delivery (heavier than Argo for our needs)
- *Risks:* Golden/compliance test maintenance burden as Tally and GST schemas evolve; Agents may learn to satisfy tests without satisfying intent (test-gaming) — mitigated by property-based + mutation testing and human intent review; Flaky tests erode the gate's authority — zero-tolerance flake policy required

#### Repo structure and human review model for agent workflows → **Polyglot monorepo (Nx/Turborepo + Bazel-style remote cache where needed) with strict module boundaries and CODEOWNERS. Agents work in isolated worktrees/branches scoped to a single bounded context (ledger, gst, sync, ai, infra). Humans review by INTENT + DIFF, not by authoring: a senior engineer owns each bounded context and is the required approver; high-risk paths (migrations, compliance, auth, money math) require a second human approver and cannot be agent-self-approved.**
A monorepo gives agents whole-system context, atomic cross-cutting changes, and one source of truth for the keystroke/compliance specs they must honor — critical when the same change touches UI grammar + ledger + tests. CODEOWNERS + tiered review keeps a human accountable for every merge while letting agents do the bulk of authoring. Scoping agents to one bounded context limits blast radius and keeps diffs reviewable.
- *Alternatives rejected:* Polyrepo (rejected — cross-cutting keystroke/compliance changes fragment across repos, agents lose context); No human review on low-risk paths (rejected for a financial product; even formatting-only auto-merge is risky near money math); Single human approver for everything including migrations (rejected — too risky for irreversible changes)
- *Risks:* Monorepo build/CI scaling (mitigated by remote caching + affected-only builds); Reviewer bottleneck on context owners; Defining the risk tiering precisely enough to be machine-enforceable

#### Desktop app distribution and auto-update → **Tauri 2 (Rust shell hosting the shared web UI), offline-first with a local SQLite mirror and the sync engine above. Code-signed installers (Authenticode on Windows, notarized on macOS); built-in Tauri updater pulling signed delta updates from S3 + CloudFront, staged-rollout with rollback.**
Tauri gives small signed binaries, a real native shell for offline + local keyboard handling (load-bearing for keystroke fidelity), and a maintained secure auto-updater — far lighter and more secure than Electron. Sharing the web UI codebase means web/desktop/mobile keystroke grammar stays identical from one source, so an agent change cannot drift the platforms apart. Auto-update over CloudFront lets us push statutory/GST changes continuously (attacking Tally's TSS upgrade-tax and same-release sync lock).
- *Alternatives rejected:* Electron (heavier, larger attack surface, higher memory — rejected); Pure PWA, no native shell (rejected — weaker offline + global keyboard capture for Tally-grade keystrokes); Per-customer MSI managed by partners (rejected — reintroduces Tally's manual update pain)
- *Risks:* Tauri ecosystem younger than Electron (mitigated: it is now production-mature in 2026); Windows SmartScreen reputation ramp for new signing cert; Delta-update integrity must be signature-verified to prevent supply-chain push attacks

#### Observability and disaster recovery → **OpenTelemetry everywhere -> Prometheus/Mimir (metrics), Tempo (traces), Loki (logs), Grafana (dashboards/alerts); Sentry for client errors; golden-signal SLOs with error budgets. DR: Aurora continuous backup + cross-region replica to ap-south-2, S3 cross-region replication of audit/docs, RPO <= 5 min, RTO <= 1 hr, quarterly game-day restore drills.**
Financial software demands provable recoverability and per-tenant traceability (which agent/AI suggestion produced which posting). OTel keeps us vendor-neutral and avoids per-GB SaaS observability bills at SMB scale. In-country DR pair keeps recovery inside India for residency. Quarterly restore drills turn DR from a document into a tested capability — directly countering Tally's manual-backup fragility.
- *Alternatives rejected:* Datadog/New Relic (excellent but cost scales painfully at high tenant/log volume; data egress/residency concerns); Single-region, backup-only DR (rejected — RTO too long for a system of record); Cross-cloud DR (rejected for MVP — residency + complexity)
- *Risks:* Self-managed Grafana stack is itself an ops surface; Cross-region replication lag could exceed RPO under heavy write bursts; Audit-log WORM means DR copies are also immutable — restore tooling must respect Object Lock

**Replication fidelity.** Zero-learning-curve is protected structurally: a single shared web UI codebase renders on web, Tauri desktop, and mobile so the F-key/Alt+G/Ctrl+G/Ctrl+A grammar can never diverge across platforms, and a golden keystroke-conformance CI gate fails any (agent or human) PR that regresses the Tally grammar. Continuous signed auto-update pushes statutory/GST changes instantly, beating Tally's manual reinstall + same-release sync lock. Data compatibility: the Aurora schema mirrors Tally's object/method/collection model 1:1, and the migration pipeline ingests existing Tally data losslessly via the XML/ODBC export path (binary is unreadable, per §17), so CAs see identical ledgers, vouchers, and reports on day one. The immutable hash-chained WORM audit trail meets MCA Rule 11(g) and Section 128(5) 8-year retention as a default — not a separate Edit Log build.

**AI angle.** AI plugs in at two levels. (1) Product layer: a durable Temporal-orchestrated agent fleet runs the OCR document-in pipeline, GSTR-2B reconciliation, agentic bank rec, NL-query-to-native-report, cash-flow forecasting, and edit-log anomaly/fraud detection — all server-side over the queryable Postgres/ClickHouse data (impossible against Tally's opaque in-RAM graph), every output carrying a confidence score with one-keystroke human-in-the-loop accept that posts through the same voucher engine and audit chain. (2) Build layer: dynamic agent workflows are the primary engineering mechanism — agents author the bulk of code in scoped worktrees, and the machine-enforced verification gates (property-based ledger invariants, golden keystroke conformance, compliance suite) are what make that velocity safe. Both layers use Anthropic Claude (Opus/Sonnet) via Amazon Bedrock in-region where available, with data-handling terms reviewed for India residency.

**Interfaces to other domains:**
- Ledger/Accounting engine: consumes the Aurora system-of-record schema and the node-partitioned ID scheme; the property-based ledger-invariant CI gate is co-owned with this domain
- UI/Keystroke domain: the golden keystroke-conformance suite and the shared web UI (rendered by both web and Tauri desktop) are the contract; one codebase prevents platform drift
- Compliance/GST domain: the compliance CI gate (GSTR/IRN/e-way-bill schema conformance, WORM audit immutability) and the serverless GST-portal polling workers integrate here
- AI/Insights domain: provides the Temporal worker runtime, Kafka event backbone, Bedrock/Claude access, and the confidence-scored human-in-the-loop posting path through the voucher engine
- Migration domain: provides the serverless ingestion pipeline that reads Tally XML/ODBC (port 9000) exports and maps Ledger/Voucher/StockItem objects into the Aurora schema
- Security/Identity domain: KMS envelope encryption, per-tenant data keys, RLS tenant isolation, MFA, and the auth review-tier in CODEOWNERS

<details><summary>Detailed engineering writeup</summary>

## ADR: Infrastructure, DevOps, Scalability & the Agentic Build Process

This ADR grounds every choice in what Tally actually is (RESEARCH.md sections 1, 10, 14, 20, 22): a single-process C/C++ desktop app over a proprietary, encrypted, in-RAM object store (`.900`/`.1800` + `.tsf` WAL), with LAN-only concurrency, peer-to-peer directional-overwrite branch sync, manual backups, obsolete Modified-DES encryption, and no native cloud/API. Our infra must invert each of those weaknesses while preserving exact keystroke fidelity and India compliance.

### 1. Cloud + region (residency)
**AWS, primary `ap-south-1` (Mumbai), DR `ap-south-2` (Hyderabad).** All financial data, audit logs, encrypted backups, and KMS keys stay inside India to satisfy the DPDP Act and CA/auditor expectations. AWS uniquely offers two in-country regions, enabling true active/passive DR without leaving the jurisdiction — the deciding factor over GCP (single India region today). Tally's own cloud runs on OCI/AWS, so the channel is comfortable.

### 2. Compute: containers, not serverless, for the core
The interactive voucher path must feel native: Enter-to-drill, Esc-to-back, Ctrl+A-accept all need sub-100ms server responses. That demands warm processes with pooled Postgres connections — the opposite of Lambda cold starts and per-invocation connection churn (a documented Aurora anti-pattern). So:
- **Core on EKS** (Kubernetes, Karpenter autoscaling, Graviton/arm64 nodes, Bottlerocket OS): the Go voucher/ledger engine, sync service, BFF/API, and the ClickHouse/Redis sidecars.
- **Serverless only for async/spiky work**: OCR document-in pipeline, GST/e-invoice/e-way-bill portal polling, scheduled GSTR-2B reconciliation, exports — run as Fargate tasks / Lambda / Temporal workers that scale to zero.

### 3. Data stores
```
Postgres (Aurora)         system of record — ACID double-entry ledger, RLS multi-tenant
   |  CDC (Kafka/MSK)
   v
ClickHouse                OLAP report acceleration (replaces Tally Aggr.1800 in-RAM)
Redis (ElastiCache)       cache, rate limits, advisory locks, sequence allocation
S3 (Object Lock / WORM)   immutable hash-chained audit trail + document blobs
KMS + per-tenant DEK      envelope encryption (AES-256-GCM), Argon2id for vault passwords
```
**Multi-tenancy:** single Aurora cluster, `tenant_id` on every row, enforced by Postgres **Row-Level Security** AND ORM guards AND tests (defense in depth). Partition by `tenant + financial_year`. Largest tenants graduate to dedicated clusters via a documented migration path. This directly kills Tally's `.900` corruption/`Ctrl+Alt+R` rewrite class and removes the 'Split Company Data' performance hack — unlimited multi-year history stays hot and queryable.

### 4. Sync & concurrency (the anti-Tally design)
Tally's sync silently overwrites the loser's edits and hard-fails on GUID/voucher-number collisions (RESEARCH §20). We instead use:
- One **live cloud ledger** per tenant as source of truth.
- **Node-partitioned UUIDv7 keys** (node prefix) so branch/HO collisions are structurally impossible; human-friendly `INV/2026-27/0001` series are a display layer.
- **Append-only event log**; offline desktop/mobile edits queue in SQLite and replay.
- Conflicts go to an **explicit resolution queue** — never silent auto-merge (auto-merging money is legally unacceptable) and never silent overwrite.
- Every cross-node write appends to the **hash-chained, WORM audit ledger**, satisfying MCA Rule 11(g) immutability + Section 128(5) 8-year retention with stronger tamper-evidence than Tally's view-only log. We also capture device ID / IP / session (Tally captures none).

### 5. CI/CD and the verification gates that make agent code safe
GitHub Actions CI -> **Argo CD GitOps** -> **Argo Rollouts** canaries with auto-rollback. The org builds primarily via **dynamic agent workflows**; safety comes from deterministic, machine-checkable contracts, not reviewer trust. Every PR (mostly agent-authored) must go green on:

| Gate | What it pins |
|---|---|
| compile / typecheck | basic soundness |
| property-based ledger invariants | `sum(debits)==sum(credits)`, no orphan postings, bill-wise links balance |
| **golden keystroke-conformance suite** | F-key / Alt+G / Ctrl+G / Ctrl+A grammar matches Tally spec exactly (zero-learning-curve promise) |
| **compliance suite** | GSTR-1/3B + e-invoice IRN + e-way-bill schema conformance, audit-log immutability |
| Semgrep / Trivy / gitleaks | no injected secrets, known CVEs, or unsafe patterns |
| migration safety | reversible, non-locking, tenant-scoped |

Merge to `main` is blocked until all green; deploy to staging is automatic, prod is canaried. Mutation testing + property tests defend against agents "gaming" tests.

### 6. Repo structure & human review model
**Polyglot monorepo** (Nx/Turborepo, remote build cache) with strict module boundaries and CODEOWNERS. Agents work in **isolated worktrees scoped to one bounded context** (`ledger`, `gst`, `sync`, `ai`, `desktop`, `infra`) — this gives them whole-system context for cross-cutting keystroke/compliance changes while limiting blast radius. Humans **review by intent + diff, never author-by-trust**:
- Each bounded context has a senior owner who is the required approver.
- **High-risk paths** (DB migrations, compliance, auth, money math) require a **second human approver** and can never be agent-self-approved.
- Low-risk paths (docs, copy) can merge on a single owner approval after green gates.

### 7. Desktop distribution & auto-update
**Tauri 2** (Rust shell + the shared web UI), offline-first over a local SQLite mirror. Code-signed (Authenticode + macOS notarization), **built-in Tauri delta auto-updater** pulling signed updates from S3+CloudFront with staged rollout and rollback. Sharing the web UI codebase keeps web/desktop/mobile keystroke grammar identical from one source — an agent change can't drift the platforms apart. Continuous auto-update lets us push GST/statutory changes the moment they land, attacking Tally's TSS upgrade-tax and its 'both ends must run the same release' sync lock.

### 8. Observability & DR
**OpenTelemetry -> Grafana stack** (Prometheus/Mimir, Tempo, Loki) + Sentry for clients; golden-signal SLOs with error budgets and full request tracing (so we can attribute every posting to the agent/AI suggestion that produced it). **DR:** Aurora continuous backup + cross-region replica to `ap-south-2`, S3 cross-region replication of audit/docs, **RPO <= 5 min, RTO <= 1 hr**, with **quarterly game-day restore drills** — turning backup from Tally's manual chore into a tested guarantee. WORM audit copies remain immutable in DR; restore tooling respects Object Lock.

### Cost model (multi-tenant SMB economics)
Row-level multi-tenancy + Graviton bin-packing + ClickHouse (cheap columnar) + S3 tiering keeps marginal per-tenant infra cost in the low tens of rupees/month, enabling the freemium/transparent-SaaS pricing the strategy demands versus Tally's ₹600/user/month VPS + license + 20% TSS stack. Serverless scale-to-zero on async jobs avoids paying for idle OCR/recon capacity. Autoscaling is tuned to keep interactive pods warm but async fleets elastic.

</details>

**Open risks:** Row-level multi-tenancy: a single missing tenant_id predicate is a cross-tenant financial-data breach — requires RLS + ORM guards + tests + fuzzing, and is the single highest-severity infra risk; Agents may satisfy CI gates without satisfying intent (test-gaming); mutation testing, property-based invariants, and mandatory human intent review mitigate but do not eliminate this; Golden keystroke + compliance suites are high-maintenance as TallyPrime releases (currently 7.0) and GST schemas evolve 2-4x/year; under-maintenance silently erodes the gate's value; Bedrock/Claude in-region (ap-south-1) availability for the latest models may lag, forcing cross-region LLM calls that need a data-residency/data-handling review; DPDP Act rules and any sector localization guidance could tighten residency/processing constraints mid-build, affecting any cross-region LLM or DR design; EKS + self-managed Grafana/ClickHouse are real operational surfaces; without disciplined SRE the ops burden could outpace a lean MVP team; Offline-first conflict queues can grow large after long branch disconnection, creating CA-facing reconciliation backlog; Largest tenants can become noisy neighbors on the shared Aurora cluster before the dedicated-cluster sharding path is exercised in production; Supply-chain risk on the desktop auto-update channel — every delta must be signature-verified end-to-end to prevent a malicious pushed update

---

# Part C — Delivery Roadmap (built via dynamic workflows)

## C.1 MVP Definition

## MVP: "A real Tally user signs in, sees their own books, and never notices they left Tally — plus one thing Tally cannot do"

The MVP is **not** a feature-complete ERP. It is the smallest slice that lets a single mid-sized Tally Gold trader (the canonical persona: a wholesale/distribution SMB with one accountant + one data-entry operator, and a CA who finalizes) **switch with zero learning curve**. The bar is set by the CA, who vetoes on two grounds only: *can I reproduce my numbers to the rupee* and *does my muscle memory still work*. Everything else is post-MVP.

**The zero-learning-curve switch (must all be true on day one):**

1. **Keystroke-faithful voucher entry for the 6 core voucher types** that are 90% of daily Tally use: F5 Payment, F6 Receipt, F4 Contra, F7 Journal, F8 Sales, F9 Purchase. Exact TallyPrime grammar: `Alt+G` Go To overlay, `Ctrl+G` Switch To, `Ctrl+H` Change Mode (Item Invoice / Accounting Invoice / As Voucher), `Alt+C` inline master create, `Enter` drill-down, `Esc` back, `Ctrl+A` accept, F2 date, the right-edge button bar with key-on-button labels, and the as-you-type substring-matching selection list. Ship the **TallyPrime keymap as default and a selectable ERP 9 legacy keymap** (F1/green theme, Ctrl+F8 Credit Note) since both cohorts migrate. The XState VoucherMachine renders Tally's exact screen stack; double-entry Σdebit=Σcredit enforced before accept.
2. **The masters that vouchers depend on:** the 28 reserved Groups, Ledgers (with GST/PAN/bill-wise/credit-period fields), Stock Items + Units + Godowns + Stock Groups, Voucher Types with per-(type,series,FY) numbering. Created via the on-the-fly `Alt+C` flow exactly as in Tally.
3. **The reports the CA opens to trust the system:** Day Book, Trial Balance, Balance Sheet, Profit & Loss, Group Summary, Ledger Vouchers/Outstandings with the default <45/45-90/90-180/>180 ageing, all with the shared report button grammar (F2 period, F12 configure, Ctrl+H change view, Alt+N columns, Ctrl+B basis, Alt+E export, Enter drill-to-voucher). These are served sub-second from the incrementally-maintained `ledger_balance` roll-up over the ltree group hierarchy.
4. **GST baked into voucher entry, not bolted on:** the embedded TaxDeriveEngine auto-derives CGST/SGST vs IGST from place-of-supply + registration type at save (sub-ms, in the atomic txn), and emits **schema-exact GSTR-1 and GSTR-3B JSON** matching the GSTN offline-tool schema to the rupee. (Connected/direct filing, e-invoice IRN, and e-way bill are Phase 2 — for MVP, schema-exact JSON export the CA uploads is sufficient and matches what Tally itself did until Release 5.0.)
5. **Zero-data-loss migration via the live-Tally XML gateway (port 9000)** — the only production-feasible path per the dossier (direct .900/.1800 binary parsing is explicitly rejected for v1). A signed .NET Windows Migration Agent drives Tally's TDL XML export → immutable Postgres staging (verbatim + SHA-256) → idempotent importer keyed on Tally GUID+MASTERID/VOUCHERID → **a reconciliation harness that proves Trial Balance, Balance Sheet, P&L, and GSTR-1 match the source Tally to the rupee.** Rupee-exact reconciliation is the hard go-live gate. Round-trip Export-to-Tally so "the CA can still read it" stays true.
6. **The always-on, cloud-native foundation** that is the whole point: multi-tenant `cored` over Aurora PostgreSQL 16 with default-deny RLS, immutable voucher versioning (= the MCA Rule 11(g) Edit Log for free), Keycloak auth replicating Tally's Owner/Data-Entry/custom Disallow-Allow levels plus the MFA/lockout Tally lacks, and web PWA + Tauri desktop shells. No RDP, no LAN gymnastics, no corruption-rewrite ritual.

**The one killer AI feature: OCR photo/PDF/WhatsApp-to-voucher auto-capture (purchase bills + receipts).**

This is chosen deliberately over NL-query/forecasting because (a) the dossier ranks it the **highest-ROI AI feature** — it attacks the 5 min/invoice, 3-5% error manual burden Tally has *no native answer to*; (b) it is the clearest "Tally literally cannot do this" wow-moment for the owner/operator on day one; and (c) it lands the user **inside the exact F9 Purchase voucher screen, pre-filled, with ledger + GST inferred and a confidence score, awaiting the human `Ctrl+A`** — so it reinforces rather than replaces the keystroke loop. AI never writes to the ledger directly; it only proposes a voucher the human accepts (the human-in-the-loop invariant). `insightd` (Python/FastAPI) runs self-hosted PaddleOCR + Presidio PII redaction in-region; raw bill images never leave India.

**Explicitly OUT of MVP** (sequenced later, to protect time-to-market): e-invoice IRN / e-way bill / Connected-GST direct filing (Phase 2), payroll/TDS/TCS (Phase 3), inventory beyond basic stock (batches/expiry — Phase 3), offline desktop write-outbox + reconciliation (Phase 2; web is thin-replica-only at MVP), NL conversational query + cash-flow forecast + anomaly detection (Phase 2-3), the FlowDef/WASM extensibility layer (Phase 4), the .900 binary parser (deferred indefinitely), ClickHouse OLAP and Kafka (introduced only when CDC throughput proves the need).

## C.2 Roadmap Narrative

## Roadmap shape and rationale

**Total to GTM-MVP launch: ~34 weeks (Phases 0-2), ~8 months.** Phases 3-4 (~30 more weeks) run post-launch and widen the wedge; they are explicitly off the time-to-market critical path.

**Why this sequencing (bias to fast MVP, grounded in the dossier):**

- **Guardrails before velocity.** The single highest-severity risk is cross-tenant leakage, and the modular-monolith-erosion risk is structural. Both are *un-retrofittable* once a large agent fan-out has written code. So Phase 0 spends 6 weeks building the RLS/GUC guard, the import-linted boundaries, the atomic-post spine, and the golden-corpus gates *first*. This is the precondition that makes the aggressive agent fan-out in Phases 1-2 safe rather than reckless.

- **The CA is the gatekeeper, so reconciliation is the gate.** The dossier is explicit: the CA vetoes on rupee-exact reproducibility and keystroke fidelity. Phase 1 therefore treats the rupee-exact migration reconciliation harness and the keystroke visual-regression suite as *merge gates*, not acceptance tests run at the end. Migration goes through the live-Tally XML gateway (port 9000) because the dossier definitively rejects direct .900/.1800 binary parsing as a v1 dependency — the binary parser is a deferred forensic moat, never a launch blocker.

- **One killer AI feature, chosen for ROI and for reinforcing the keystroke loop.** OCR photo-to-voucher is the highest-ROI gap (5 min/invoice, 3-5% error, no native Tally answer) and it lands the user inside the exact pre-filled F9 screen awaiting Ctrl+A — so it strengthens muscle memory rather than competing with it. NL-query/forecast/anomaly are deliberately deferred to Phase 3: they are differentiators but not switch-blockers, and shipping them at MVP would slow time-to-market.

- **Compliance is split exactly where the architecture says.** The embedded TaxDeriveEngine (sub-ms, in the atomic txn) ships in Phase 1 because it is on the write path and the CA needs schema-exact GSTR JSON to trust the books. The long-running, failure-prone statutoryd (IRN/e-way/Connected-GST filing) ships in Phase 2 as durable Temporal workflows isolated from the keystroke path — and the freeze-on-external-reference rule lands *with* it so the offline-numbering risk and the e-invoice-freeze risk are closed together.

- **Offline is graded, not symmetric.** Per the architecture decision, web is a thin replica at MVP (no local store), and the full offline-first SQLite outbox lands in Phase 2 for desktop/mobile — gated behind the atomic-post API being stable and behind a network-partition fuzzer that asserts zero number collisions and a hard-block on filed/e-invoiced vouchers. The WASM-everywhere symmetric model stays a documented v2 path behind golden-vector determinism tests, exactly as the Sync ADR resolution dictates.

- **Scale infra is introduced at the seam, not on day one.** Kafka/MSK and ClickHouse arrive in Phase 3 only when CDC throughput proves the need (the outbox+CDC bus is sufficient and cheap for MVP). The Aurora-chokepoint mitigations (company_id partitioning, BRIN indexes, read replicas, Redis-fronted numbering, dedicated-cluster promotion rehearsal) are load-tested in Phase 3 before the largest real tenant exercises them.

**The dynamic-workflow philosophy in one line:** agents fan out to *produce*, adversarial agents try to *break*, machine gates *verify the invariants*, and senior humans *approve only the load-bearing merges and curate the test matrices agents cannot source.* That is what lets a ~10-person senior core ship an ERP at agent-fan-out velocity without sacrificing the CA-trust moat.

## C.3 Phases

> Each phase is delivered by running the listed **dynamic workflows** — fan-out/pipeline fleets of coding agents whose output is gated by tests and adversarial verification before merge. This mirrors how this very plan and its research were produced.

### Phase 0 — Walking skeleton, isolation harness, and golden corpora
*Duration: ~6 weeks · Goal: Stand up the one load-bearing path end-to-end for a single voucher type with the cross-tenant-leakage and tax-correctness guardrails built BEFORE feature velocity — so the agent fan-out in later phases is safe. Nothing here is throwaway; it is the spine plus the test gates that make adversarial agent work possible.*

**Deliverables:**
- cored Go 1.23 monolith scaffold (chi v5, pgx v5, sqlc) with the 7 import-linted module boundaries (Identity, Ledger, Inventory, Compliance-derive, Integration-Gateway, Sync, Reporting) enforced by CI architecture tests on day one
- Aurora PostgreSQL 16 schema: voucher-version table (immutable, BLAKE3 hash-chained, node-partitioned UUIDv7), posting rows, ledger_balance, ltree groups, NUMERIC(19,4)/integer-paise, deferred Σdebit=Σcredit trigger, default-deny RLS on EVERY table partitioned by company_id
- Mandatory pgx/sqlc connection wrapper that REFUSES any query when app.current_company_id GUC is unset; no bypassrls/superuser role available to app code (the #1 risk, closed structurally before any feature lands)
- Keycloak 26 OIDC with company-scoped JWT claims; middleware sets the GUC; one end-to-end F8 Sales voucher posts atomically (Ledger opens txn, embedded TaxDeriveEngine derives GST, ledger_balance updates, hash-chained audit row, transactional outbox write) and renders in a Trial Balance
- XState VoucherMachine skeleton rendering ONE voucher screen (F8 Sales) with the exact button bar and selection list; React 18 DOM + TanStack Virtual; Vite/Workbox web shell
- The golden corpora and their CI gates, owned from week one: (a) keystroke-conformance visual-regression suite vs real TallyPrime 7.0 screen captures; (b) golden-transaction tax-derivation regression corpus (CGST/SGST/IGST cases) the TaxDeriveEngine must pass; (c) tenant-isolation fuzz harness; (d) rupee-exact migration reconciliation harness stub
- Terraform/EKS/Argo CD baseline in ap-south-1 with DR ap-south-2, OTel→Grafana/Tempo/Loki, Trivy/Semgrep/gitleaks/Falco in GitHub Actions

**Dynamic workflows to run this phase:**

- **`Schema + isolation spine pipeline (human-led, agent-assisted)`** — Build the voucher-version data model, RLS default-deny, the connection-wrapper guard, and the atomic-post transaction — the invariants every later agent depends on.
  - *Fan-out & verification:* SEQUENTIAL pipeline, NOT fan-out, because these are the load-bearing invariants: one migration-author agent proposes schema + RLS + triggers; an adversarial 'tenant-leakage red-team' agent then tries to defeat isolation (query without GUC, cross-company joins, missing predicate) and must FAIL to leak before merge; a senior human (Principal/DB owner) is required CODEOWNERS approver. Gate: tenant-isolation fuzz suite green + architecture import-lint green.
- **`Golden-corpus authoring fan-out`** — Produce the keystroke, tax, and reconciliation golden datasets that become the merge gates for all of Phase 1-2.
  - *Fan-out & verification:* FAN-OUT by corpus: parallel agents each own one corpus (keystroke-visual, tax-golden, migration-recon, isolation-fuzz), each capturing ground truth from the RESEARCH.md spec + real TallyPrime. Verification is adversarial: a separate 'corpus-auditor' agent cross-checks each generated case against the dossier's cited Tally behavior and rejects fabricated expectations. Human regulatory-watch lead signs off the tax corpus.


### Phase 1 — Zero-learning-curve core: 6 voucher types, masters, reports, migration to the rupee
*Duration: ~14 weeks · Goal: Make a real Tally user productive with zero retraining and prove migration is lossless. At the end of this phase, a pilot trader's CA can reconcile our Trial Balance / BS / P&L / GSTR-1 to their Tally to the rupee and find the keystrokes identical. This is the bulk of the agent-fan-out velocity.*

**Deliverables:**
- All 6 core voucher types (F4/F5/F6/F7/F8/F9) keystroke-faithful in both TallyPrime and ERP 9 legacy keymaps, including Ctrl+H Change Mode 3-state, Alt+C inline masters, single/double-entry config
- Masters: 28 reserved groups, Ledgers (GST/PAN/bill-wise/cost-centre/credit-period), Stock Items/Units/Godowns/Stock Groups, Voucher Types with per-(tenant,type,series,FY) Redis-fronted monotonic display-number allocator over UUIDv7 identity
- Reports: Day Book, Trial Balance, Balance Sheet, P&L, Group Summary, Ledger Outstandings with default ageing slabs and bill-date/due-date methods — all with the shared report button grammar and Enter drill-to-voucher (opening in alteration mode, which writes a new immutable version)
- Embedded TaxDeriveEngine v1 (CEL/JSON-Logic interpreter, Ed25519-signed effective-dated RuleSet bundle) deriving CGST/SGST/IGST from place-of-supply; schema-exact GSTR-1 and GSTR-3B JSON export matching GSTN offline-tool schema (including rate-wise HSN — a day-one differentiator over Tally)
- .NET 8 signed Windows Migration Agent + idempotent TS importer + rupee-exact reconciliation harness passing on real Tally datasets across .900 and .1800 dialects; round-trip Export-to-Tally
- Keycloak-backed Owner/Data-Entry/custom Disallow-Allow levels (OPA/Rego for cross-cutting policy) + mandatory WebAuthn for Owner/Admin + account lockout (the gaps Tally has)
- Web PWA thin-replica (TanStack Query, live, no local store) + Tauri 2 desktop shell for full F-key/Alt capture (read/write online)

**Dynamic workflows to run this phase:**

- **`Voucher-type fan-out (parallel, keystroke-gated)`** — Implement the 6 voucher types + their masters concurrently against the frozen VoucherMachine contract and the canonical voucher schema.
  - *Fan-out & verification:* FAN-OUT one agent per voucher type, each working in an isolated git worktree against the stable XState contract and the Ledger module's post interface (agents NEVER touch ledger_balance or posting tables directly — only through the Ledger interface, preserving the atomic-post invariant). Merge gate per agent: (1) keystroke visual-regression vs the TallyPrime golden frames must pass, (2) tax-golden corpus must pass for that voucher's GST cases, (3) an adversarial 'spec-conformance' reviewer agent diffs the implementation against RESEARCH.md §21 keystroke table and rejects deviations. Human bounded-context CODEOWNER approves each merge.
- **`Reporting roll-up pipeline`** — Build the ledger_balance incremental aggregation and the 6 reports with drill-down.
  - *Fan-out & verification:* PIPELINE: balance-engine agent first (ledger_balance maintenance + ltree aggregation), then FAN-OUT one agent per report consuming it. Verification is differential-testing: a generator agent posts random valid voucher sets, an oracle recomputes balances independently, and reports must match the oracle AND match a parallel real-Tally export of the same transactions (the rupee-exact gate reused from migration).
- **`Migration + reconciliation adversarial loop`** — Achieve lossless, rupee-exact migration from live Tally across version dialects.
  - *Fan-out & verification:* FAN-OUT by data domain (masters, accounting vouchers, inventory vouchers, GST attributes), each agent owning extract→stage→import for its domain keyed idempotently on Tally GUID. Verification is the reconciliation harness as an ADVERSARIAL gate: TB/BS/P&L/GSTR-1 must tie to the rupee against the source Tally on a matrix of real anonymized datasets and multiple Tally releases; any mismatch blocks merge and is routed back to the owning agent. Human migration lead owns the Tally-version test matrix.
- **`Tax-derive RuleSet authoring + canary`** — Encode GST nature-of-transaction rules as signed data and prove correctness.
  - *Fan-out & verification:* RuleSets authored as signed data (never code), one agent per transaction-nature cluster; an adversarial 'CA-persona' verifier agent constructs edge cases (RCM, SEZ, exports, inter/intra-state, composition) and the golden corpus must stay green before signing. No fleet auto-push without staged canary monitoring (carried into ops).


### Phase 2 — Killer AI feature, connected compliance, and offline desktop
*Duration: ~14 weeks · Goal: Ship the one killer AI feature (OCR auto-capture) that makes the product visibly better than Tally, make compliance first-class (e-invoice IRN, e-way bill, Connected-GST direct filing), and turn the desktop shell into a true offline-first client — the things that convert pilots to paid switchers. Go-to-market MVP launches at the END of this phase.*

**Deliverables:**
- Killer AI: insightd (Python/FastAPI) OCR pipeline — photo/PDF/WhatsApp/email-forward purchase bill → self-hosted PaddleOCR extract → Claude Haiku 4.5 classify + Claude Opus 4.8 line-item/ledger/GST inference → Presidio PII redaction (PAN/GSTIN/Aadhaar never leave India) → drops user INTO the pre-filled F9 Purchase voucher with confidence scores, awaiting human Ctrl+A. Writes ONLY via the human-approved voucher API.
- statutoryd (separate Go deployable) + Temporal: e-invoice IRN generation (NIC schema, 12 sections/133 fields) and e-way bill JSON via multi-homed ProviderAdapter (ClearTax/Masters India primary, NIC IRP secondary) behind circuit breakers; 6-hour GSP session auto-renew; Connected-GST direct GSTR-1/3B/CMP-08 filing with ARN tracking
- Freeze-on-external-reference rule: pin a voucher's display number at IRN-generation time; hard-block superseding any voucher already e-invoiced or in a filed GSTR-1, routing to CA reconciliation queue
- Offline-first desktop (Tauri) + mobile (Capacitor 6): full local SQLite read-model + append-only voucher-version write-outbox tagged with HLC + node-partitioned UUIDv7; reconnect replays through the SAME authenticated voucher-post API; provisional display numbers (INV/2026-27/PROV-<node>-<seq>) swapped atomically at commit; deterministic conflict winner + surfaced reconciliation queue
- Transactional-outbox + Postgres logical-replication CDC bus feeding statutoryd and insightd; River for in-core jobs (OCR handoff, recon triggers, sync merge, exports)
- AES-256-GCM KMS envelope encryption per-tenant, Argon2id KDF, opt-in audited key-escrow (replacing TallyVault's Modified-DES total-loss model)

**Dynamic workflows to run this phase:**

- **`OCR pipeline fan-out (accuracy-gated)`** — Build the document→voucher pipeline stage by stage with a held-out accuracy benchmark.
  - *Fan-out & verification:* PIPELINE with FAN-OUT inside stages: parallel agents own ingest (WhatsApp/email/upload), OCR-extract, classify+infer, PII-redact, and voucher-prefill mapping. Verification is a held-out labeled Indian-invoice benchmark with a hard field-level accuracy floor (target ~96% on header, line-item extraction explicitly measured); an adversarial agent injects adversarial/low-quality scans and the pipeline must degrade to 'low-confidence, route to manual' rather than silently mis-post. Human-in-the-loop is structural: no AI write path to the ledger exists.
- **`statutoryd workflow fan-out (chaos-gated)`** — Build IRN/e-way/GSTR filing as durable Temporal workflows isolated from the request path.
  - *Fan-out & verification:* FAN-OUT one agent per government surface (IRN, e-way, GSTR-1 file, GSTR-3B file, IMS), each as an idempotent Temporal workflow behind the ProviderAdapter. Verification is contract-tests against recorded GSP/NIC schema fixtures PLUS chaos injection (an adversarial agent simulates GSP outage, 6-hour token expiry, rate-limit, partial-failure) — workflows must resume idempotently and surface clear retry state, never block the keystroke path. Schema conformance gated on the NIC golden schemas.
- **`Offline sync + reconciliation adversarial fan-out`** — Make desktop/mobile offline-write safe, with the provisional-numbering and freeze invariants intact.
  - *Fan-out & verification:* FAN-OUT: local-store agent, outbox-replay agent, cloud-sequencer agent, conflict-resolution-queue agent. Verification is property-based adversarial simulation: a network-partition fuzzer creates concurrent offline edits, days-long offline branches, and edits to already-e-invoiced vouchers; invariants asserted — zero number collisions, deterministic winner, loser preserved as superseded version, and HARD-BLOCK on filed/e-invoiced vouchers. This directly tests the dossier's top offline risk.


### Phase 3 — Depth that widens the wedge: more AI, payroll/TDS, richer inventory, scale
*Duration: ~16 weeks · Goal: Move from 'a real Tally user can switch' to 'they would be foolish not to': add the analytics Tally explicitly cannot do (NL query, cash-flow forecast, anomaly detection), the statutory breadth (TDS/TCS, payroll Form 24Q/PF/ESI), inventory depth (batches/expiry for the pharma/distribution cohort), and the OLAP scale path.*

**Deliverables:**
- NL→typed DSL→read-only-replica conversational query (never raw SQL), rendered in the exact report column layouts; AI 'explain this number'/anomaly flag at every report drill level
- Predictive cash-flow runway (XGBoost/statsmodels deterministic engines blending seasonality, recurring vouchers, overdue-collection probability) in the exact Inflow/Outflow/Nett Flow layout; receivables risk scoring; anomaly/fraud detection over the hash-chained Edit Log (back-dated edits, after-hours deletions)
- TDS/TCS derivation in the TaxDeriveEngine; Payroll module with Form 24Q .txt (FVU), PF ECR, ESI returns kept schema-current via cloud-pushed RuleSets
- Inventory depth: batches/expiry (pharma), price lists, order/delivery/receipt-note voucher types, multi-godown — completing the Marg/BUSY-competitive surface
- ClickHouse (ap-south-1) fed from CDC for multi-year Day Book/Stock Summary; MSK/Kafka introduced at the fan-out seam where CDC throughput proves the need; dedicated-cluster promotion path rehearsed for largest tenants
- IMS (accept/reject/pending, deemed-accept-on-14th timers) and GSTR-2A/2B reconciliation + ITC recovery

**Dynamic workflows to run this phase:**

- **`AI-insight fan-out (read-only, hallucination-gated)`** — Build NL-query, forecast, and anomaly features beside the ledger, never in the keystroke loop.
  - *Fan-out & verification:* FAN-OUT one agent per insight (NL-query, forecast, anomaly, risk-score). Verification: NL-query agent is gated by a query-correctness suite where the NL→DSL output is executed and compared to a hand-written reference query (it must NEVER emit raw SQL — an adversarial agent attempts SQL-injection-shaped prompts that must be rejected); forecast/anomaly engines are deterministic (XGBoost/statsmodels) and gated on backtested accuracy thresholds, not LLM vibes. All read from a replica; zero write capability asserted by architecture test.
- **`Statutory-breadth fan-out (golden-corpus-gated)`** — Add TDS/TCS + payroll statutory outputs as signed RuleSets and schema-exact files.
  - *Fan-out & verification:* FAN-OUT one agent per return type (24Q, PF ECR, ESI, TDS challan); each gated by a golden-output corpus validated against the official FVU/ECR validators. Regulatory-watch human owns the corpus and the effective-dating; canary rollout per RuleSet before fleet push.
- **`Scale + OLAP pipeline`** — Offload reporting load and rehearse the multi-tenant scaling chokepoint mitigations.
  - *Fan-out & verification:* PIPELINE: CDC→ClickHouse loader agent, then report-acceleration agents. Verification is load-test-gated: a synthetic million-voucher tenant must keep BS/TB sub-second and prove the dedicated-cluster promotion under load before the largest real tenant needs it (directly addressing the Aurora-chokepoint risk).


### Phase 4 — Open extensibility (the TDL replacement) and ecosystem moat
*Duration: ~14 weeks · Goal: Replace TDL with a stable, AI-assisted, upgrade-surviving extensibility layer and harden the day-one ecosystem moat (the /tally ENVELOPE+JSONEX ingress, webhooks, SDKs) — converting integrators and partners off Tally.*

**Deliverables:**
- FlowDef declarative DSL (JSON-Schema 2020-12 → versioned IR) covering ~80% of customizations; semantic anchors (stable API) replacing TDL's brittle name-based [#Report] overrides so add-ons survive upgrades
- V8 isolate (isolated-vm online / QuickJS-ng offline) + Wasmtime WASI-p2 server modules for the ~20% needing code; capability manifests enforcing least privilege; OCI registry + Sigstore-signed bundles
- Claude-authored extension copilot ('describe the report you want') eliminating the TDL-developer dependency
- Hardened /tally ENVELOPE + TallyPrime 7.0 JSONEX ingress with API-key/OAuth2/TLS/per-company scopes so Suvit/Webtel/tally-database-loader work unchanged over authenticated HTTPS; first-party webhooks (voucher created, payment received, GSTR-2B mismatch) via exactly-once outbox; OpenAPI 3.1 spec + Python/Node SDKs
- Group-company live consolidation across differing charts/currencies (AI ledger auto-mapping)

**Dynamic workflows to run this phase:**

- **`FlowDef + sandbox fan-out (capability-gated)`** — Build the declarative DSL, the IR compiler, and the sandboxed code-execution planes.
  - *Fan-out & verification:* FAN-OUT: DSL-schema agent, IR-compiler agent, isolated-vm host agent, Wasmtime host agent, capability-manifest enforcer agent. Verification is a security-adversarial gate: a red-team agent writes malicious extensions attempting capability escape, cross-tenant reads, and resource exhaustion; the sandbox must contain all of them (asserted by the same RLS/GUC guard plus capability manifest). Upgrade-survival is gated by replaying old extensions against a new core version via semantic anchors.
- **`Legacy-ingress + ecosystem fan-out (compatibility-gated)`** — Make the existing Tally connector ecosystem work unchanged against our cloud.
  - *Fan-out & verification:* FAN-OUT one agent per connector contract (ENVELOPE import/export, JSONEX, ODBC-shaped reads, webhooks). Verification is differential: real third-party connectors (tally-database-loader, etc.) run against both real Tally and our /tally endpoint and outputs must match; an adversarial agent fuzzes malformed ENVELOPEs to prove the auth wrapper and error handling are robust (closing Tally's unauthenticated-open-port gap).


**Total roadmap span:** ~64 weeks of sequenced phases (phases overlap in practice; critical path below).

## C.4 Critical Path

1. Phase 0 isolation spine: default-deny RLS + the connection-wrapper GUC guard + import-linted module boundaries MUST exist before any feature agent runs — cross-tenant leakage is the highest-severity risk and cannot be retrofitted onto a fan-out that already wrote unsafe data paths.
2. Phase 0 atomic voucher-post (Ledger txn + embedded TaxDeriveEngine + ledger_balance + hash-chained version + outbox in ONE Postgres transaction) — this is the load-bearing path; every voucher-type agent and the offline outbox replay depend on it being correct and frozen.
3. Phase 0/1 golden corpora (keystroke-visual, tax-golden, migration-recon, isolation-fuzz) — these ARE the merge gates that make adversarial agent fan-out safe; without them, agent velocity produces unverifiable code. Gates must precede the fan-out they govern.
4. Phase 1 the 6 voucher types + masters + reports + rupee-exact migration — this is the zero-learning-curve switch and the CA-veto gate; nothing ships to a real user until BS/TB/P&L/GSTR-1 reconcile to the rupee against source Tally.
5. Phase 1 embedded TaxDeriveEngine correctness via golden corpus + signed RuleSet + canary — one bad bundle miscomputes GST fleet-wide and destroys CA trust; correctness gating is on the critical path, not parallel to it.
6. Phase 2 OCR killer feature + e-invoice/e-way/Connected-GST — the conversion lever; the freeze-on-external-reference rule must land WITH e-invoicing so offline numbering can never clash with a filed return.
7. Phase 2 offline outbox + cloud sequencer — gated behind the atomic-post API being stable (Phase 0/1); offline reconciliation is the dossier's top offline risk and must pass the partition-fuzzer before any customer goes offline.
8. Phases 3 and 4 (depth AI, statutory breadth, scale, extensibility) are largely parallelizable AFTER the Phase 2 launch and are NOT on the time-to-market critical path — they widen the wedge post-launch.

## C.5 Team Shape & How Humans Supervise the Agent Workflows

## Team shape: small senior human core supervising large dynamic agent fan-out

The build is executed **primarily by dynamic agent workflows** (fan-out + pipeline of coding subagents with adversarial/test-gated verification). Humans are not typing most of the code; they are **owning the invariants, the gates, and the merges that the gates cannot decide.** The principle: agents produce, gates verify, and a senior human is the required approver wherever an invariant is load-bearing.

**Human core (~10-12 senior engineers, no juniors on critical path):**

- **Principal Engineer / delivery lead (1):** owns the architecture, the module-boundary contract, and the cross-cutting invariants (atomic post, RLS isolation, signed RuleSets). Final required approver on anything touching the load-bearing voucher-post path.
- **Bounded-context owners / CODEOWNERS (7 — one per cored module: Identity, Ledger, Inventory, Compliance-derive, Integration-Gateway, Sync, Reporting):** each is the required human approver for merges into their context. They review what the adversarial agent gates flag as uncertain, and they defend the module boundary against erosion (the big-ball-of-mud risk). The Ledger owner specifically guards the atomic-post transaction so other modules participate only through its interface.
- **Compliance/statutory lead + regulatory-watch function (1-2):** owns the tax/keystroke golden corpora as a *permanently staffed maintenance tax*, not incidental work — tracks TallyPrime version changes (2-4x/yr) and GST schema changes, and signs RuleSet bundles. This is the moat-maintenance role the dossier flags as the silent-erosion risk.
- **Security/platform lead (1):** owns the RLS/GUC guard, KMS/crypto, the tenant-isolation fuzzer, the sandbox red-team, and external pen-testing per release.
- **AI/ML lead (1):** owns insightd, the OCR accuracy benchmark, and the human-in-the-loop write boundary (asserting AI never writes to the ledger).

**How humans supervise the agent workflows (the supervision contract):**

1. **Gates, not gut.** Every fan-out merges through automated adversarial gates the humans authored in Phase 0: keystroke visual-regression, tax-golden corpus, tenant-isolation fuzz, rupee-exact reconciliation, OCR accuracy floor, chaos/partition simulation. A human reviews only what a gate flags as uncertain — this is what lets a 10-person core supervise dozens of parallel coding agents.
2. **Adversarial pairing is structural.** Each producing agent (or fan-out) is paired with a separate verifier/red-team agent whose job is to defeat the producer (leak a tenant, mis-post a voucher, escape the sandbox, break GSP failover). The human reviews the *disagreements* between producer and adversary, not every line.
3. **Worktree isolation + import-lint as guardrails.** Agents work in isolated git worktrees; CI architecture tests reject any cross-module table access or any query without the GUC before a human ever looks — the boundary is machine-enforced so deadline pressure cannot erode it.
4. **CODEOWNERS as the human merge gate.** No agent merges into a bounded context without that context's senior owner approving. The load-bearing path (voucher post, RLS, signed RuleSets) additionally requires the Principal.
5. **Canary, not big-bang, for the dangerous bundles.** RuleSet/tax changes never auto-push fleet-wide; a human owns the canary cohort and the automated rejection-rate monitor that gates promotion.
6. **Humans own the test matrices the agents can't source:** real (anonymized) Tally datasets across .900/.1800 dialects and multiple TallyPrime releases for migration; recorded GSP/NIC fixtures for statutoryd; the labeled Indian-invoice OCR benchmark. Agents run against these; humans curate and grow them.

---

## Appendix — How this plan was produced

This plan and the research dossier were both generated using **dynamic agent workflows** (the same mechanism Part C prescribes for the build): a research workflow fanned 16 web-research agents + a self-critique + 6 deep-dive follow-ups; a planning workflow fanned 10 domain-architect agents into a principal-integrator and a delivery lead. Next sessions should resume from `RESEARCH.md` + this `PLAN.md`, and begin executing Phase 0's workflows.