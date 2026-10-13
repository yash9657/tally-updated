# R9 — The data layer that lets an LLM agent "understand" a tenant's books

*Research date: 2026-09-08. Method note: the session's web-search budget was exhausted before this task began, so every fact below comes from direct fetches of primary pages (leaderboards, GitHub READMEs, vendor docs, arXiv abstracts, statute commentaries). Where a number is my own estimate, or a fact I could not re-verify this session, it is marked **[est.]** or **[unverified]**.*

Context we design against: Go modular monolith on PostgreSQL 16 with `FORCE ROW LEVEL SECURITY` per tenant; append-only, hash-chained voucher-version log; Python/FastAPI AI plane; open-weight models on Fireworks; an existing **closed typed query DSL** (`report_id + period + dimensions + filters`) compiled to parameterised read-only SQL.

---

## 1. Text-to-SQL vs semantic layer vs closed DSL (state of the art, Sept 2026)

### 1.1 What the benchmarks actually say

| Benchmark | What it measures | Best public score (date) | Take-away |
|---|---|---|---|
| **BIRD** (dev/test, SQLite, 12.7k q) | single-turn EX on "dirty" DBs w/ evidence | SiriusAI-SQL (Tencent) 82.28% test, 77.77% dev (2 Sep 2026); human 92.96% | Top ~10 entries are all within 78–82% — the benchmark is near saturation for pipelines; a 10-pt gap to humans persists. |
| **Spider 2.0-Snow** (547 tasks, Snowflake) | enterprise text-to-SQL, 1000+-column schemas | 96.70 (Genloop Sentinel v2 Pro); 94–96 for top 3 | Was 17–21% for o1-preview at launch (Nov 2024). Agentic harnesses with schema exploration closed the gap in ~18 months. |
| **Spider 2.0-Lite** (547, BigQuery/Snowflake/SQLite) | same, no environment | 76.23 (Tianqiong + GLM 5.2) | Still ~24% failure. |
| **Spider 2.0-DBT** (68 tasks, DuckDB) | *code-agent* tasks over a dbt project | 65.6 (SignalPilot); GPT-5 harness 41.18 | Writing transformations, not just SELECTs, is far less solved. |
| **LiveSQLBench** (Base-Lite 270 / Full 600 / Large-v1 480 tasks over ~1000-col DBs) | contamination-free, full SQL spectrum incl. CRUD, "hierarchical knowledge base" | 48.00% (C3 AI DIA, 2 Mar 2026); Claude Opus 4.6 35.5%, Gemini 3.1 Pro 36.5% | **Frontier models still fail >50% of realistic BI+CRUD tasks when the business knowledge is external.** |
| **BIRD-Interact** (300 Lite / 600 Full, PostgreSQL) | multi-turn conversational and agentic | ≈24% / ≈18% (Lite), ≈16% (Full) | Interaction with an ambiguous user is the weakest link; 4k–12k turns per eval. |

Sources: spider2-sql.github.io; bird-bench.github.io; livesqlbench.ai; bird-interact.github.io; arXiv 2411.07763.

Interpretation for us: raw text-to-SQL on a **known, fixed** schema (ours) is a much easier setting than Spider 2.0, but the failure mode that matters — silently wrong aggregation semantics (period boundaries, debit/credit sign, bill-wise vs ledger balance, GST place-of-supply) — is exactly what LiveSQLBench's "knowledge base" tasks probe, and there the frontier is ~48%.

### 1.2 The "semantic layer raises accuracy" evidence

- **data.world benchmark (arXiv 2311.07509, Sequeda/Allemang/Jacob):** GPT-4 zero-shot SQL on an insurance schema: **16% → 54%** when the LLM queried through an ontology/knowledge graph (SPARQL) instead of raw tables (3× relative). This is the most cited number in the space; it is a single schema, GPT-4-era, and the KG side got hand-built mappings.
- **dbt Semantic Layer (Nov 2023 blog):** replicating a *subset of eight* data.world questions ("high question complexity / low schema complexity") via MetricFlow gave **83%**, after excluding three multi-hop-join questions MetricFlow could not express. Small n, self-reported.
- **Vendors (2025–26):** Snowflake *Semantic Views* (facts, dimensions, metrics, relationships as schema objects; Cortex Analyst reads them and emits SQL against physical tables); Databricks *Unity Catalog metric views* (YAML measures/dimensions/joins, plus "agent metadata" — synonyms, display names, formatting — explicitly to "improve agent accuracy"); Cube (*Semantic SQL* with a `MEASURE()` function, MCP server, access policies applied in the semantic runtime); WrenAI (MDL: models/columns/relationships/views/metrics; "dry-plan validation, row limits, structured error hints"); Malloy (MIT; Publisher serves `.malloy` models over REST **and MCP**). None publish controlled accuracy deltas; the consistent architectural claim is *governance* (one definition of "revenue"), not raw accuracy.
- **CHESS (arXiv 2405.16755):** schema pruning (Schema Selector) alone cuts tokens 5× and adds ~2% accuracy; 71.10% BIRD test with 83% fewer LLM calls. Schema *selection* is a cheap win we should replicate.
- **XiYan-SQL (arXiv 2411.08599):** M-Schema (semi-structured schema representation with sample values) + multi-generator ensemble + selector: 75.63% BIRD, 89.65% Spider.

### 1.3 Open text-to-SQL agents and models (status)

| Project | Status (Sept 2026) | Notes |
|---|---|---|
| **Vanna 2.0** | GitHub repo **archived 29 Mar 2026** (read-only), MIT, 23.8k★ | Agent + tool registry, "user-aware at every layer" (auto filters by permission). Good design reference; do not depend on it. |
| **WrenAI** | Apache-2.0 core, 17.5k★ | Rust Wren Engine on Apache DataFusion, 22+ sources incl. Postgres/DuckDB; MDL semantic layer; LanceDB memory. Closest OSS analogue to "semantic catalogue + agent". |
| **DB-GPT** | MIT, 19.9k★ | Text2SQL fine-tuning hub (Qwen, Llama…), AWEL workflows, sandboxed code exec. Heavy; Chinese-ecosystem-centric. |
| **Dataherald** | Not re-verified this session **[unverified]** | Historically Apache-2.0 NL-to-SQL API with "golden SQL" store. |
| **SQLCoder (Defog)** | Not re-verified **[unverified]** | Superseded in quality by the models below. |
| **Arctic-Text2SQL-R1** (Snowflake, arXiv 2505.20315) | 7B outperforms prior 70B-class; RL with execution-correctness reward; held top BIRD leaderboard spot in 2025 | Open weights family; the strongest small open text-to-SQL model to consider for Fireworks/self-host. |
| **OmniSQL** (arXiv 2503.02240) | 7B/14B/32B, trained on SynSQL-2.5M (2.5M pairs, 16k synthetic DBs) | Code/data/models released; matches GPT-4o/DeepSeek-V3 on BIRD/Spider. |
| **XiYan-SQL**, **CHESS** | Pipelines, not products | Reusable ideas: M-Schema, schema selector, NL unit tests. |

### 1.4 Recommendation: how far to open SQL to the agent

Three tiers, each strictly more constrained than the last in *blast radius*, not in *expressiveness*:

1. **Tier A — semantic catalogue (default path, ≥80% of questions).** The agent picks a `report_id`/metric from a catalogue and fills the DSL. This is what Snowflake/Databricks/Cube all converged on; it also makes answers *explainable* ("this is your Outstandings report, FY25-26, party=X"). Our DSL is already this. Add: metric synonyms, Hinglish aliases ("udhaar" → receivables), and "agent metadata" per report as Databricks does.
2. **Tier B — agent-written SQL over a *published* read-model.** Not the raw tables: a set of **stable views** (`v_ledger_entries`, `v_voucher_lines`, `v_party`, `v_stock_movements`, `v_bank_lines`, `v_gst_returns`) with `security_barrier` and the RLS policies of the base tables applying transitively. Constraints: separate Postgres role with `SELECT` only on the view schema; `SET ROLE` + `SET LOCAL app.tenant_id` inside a read-only transaction (`SET TRANSACTION READ ONLY`); `statement_timeout` ≈ 5 s; `work_mem` cap; automatic `LIMIT` injection (WrenAI does this); AST-validate with a SQL parser (pg_query / sqlglot) to allow only one `SELECT`, no `COPY`, no functions outside an allowlist, no `pg_catalog`/`information_schema`; refuse if the plan estimate exceeds N rows. Give the model the M-Schema-style card (columns + 3 sample values + FK edges) *only for the views the schema-selector step chose*.
3. **Tier C — DuckDB/Python sandbox over a tenant extract** (section 2) for statistics, forecasting, pivots. SQL here can be fully free because the data is already tenant-scoped and the process cannot reach Postgres.

RLS is necessary but not sufficient for Tier B: RLS scopes rows, not resource consumption, not timing/error side-channels, not `pg_sleep`. The role/timeout/AST layers handle those. **Do not** grant the agent role `BYPASSRLS`, table ownership (owners bypass RLS unless `FORCE` — you already use FORCE, keep it), or access to the version-log's raw hash columns beyond the view.

---

## 2. Agent-side analytics sandboxes

### 2.1 Engines

- **DuckDB `postgres` extension** (github.com/duckdb/duckdb-postgres): `ATTACH 'dbname=… host=…' AS pg (TYPE postgres)`; reads Postgres at query time; pooling knobs `pg_pool_max_connections`, `pg_pool_acquire_mode='wait'` for PgBouncer transaction mode; RDS IAM tokens. (Binary-COPY parallel scanning by `pg_pages_per_task`, `READ_ONLY`, and pushdown are documented on duckdb.org but the page redirected this session — **[unverified]** details.) Important for us: the extension connects as a Postgres user, so **RLS applies** if we connect as the tenant-scoped role; but it opens *multiple* connections, and `SET app.tenant_id` is per-session — use a role-per-tenant or a connection-string `options=-c app.tenant_id=…` so every pooled connection carries the tenant.
- **pg_duckdb 1.1.1** (DuckDB + Hydra + MotherDuck; 3.2k★): DuckDB vectorised engine *inside* Postgres 14–18; `duckdb.force_execution=true` runs analytical SQL over heap tables; reads/writes Parquet/Iceberg/Delta on S3/GCS/R2. Advantage: query stays inside the RLS-enforcing server. Caveat: it is an in-process extension in your OLTP primary; run it on a read replica.
- **Polars / pandas**: standard inside any of the sandboxes below; Polars' lazy engine is the practical choice for 100k–5M-row tenant frames **[est.]**.
- **DuckDB-Wasm 1.5.5**: 4 GB Wasm memory ceiling, single-threaded by default, Arrow protocol, HTTP range reads of Parquet. **Pyodide** (CPython on Emscripten): numpy, pandas, scipy, scikit-learn, matplotlib available; micropip for pure-Python wheels. Together they enable a **zero-egress** "analysis runs in the accountant's browser on a Parquet extract" mode — data never leaves the tenant's session except to the model as *aggregates*.

### 2.2 Hosted code-interpreter sandboxes (for the server-side agent)

| | E2B | Modal Sandboxes |
|---|---|---|
| Billing | $0.000014/vCPU-s + $0.0000045/GiB-s; storage free; Hobby $0 w/ $100 one-time credit; Pro $150/mo | $0.0000131 per physical core-s (min 0.125 core) + $0.00000222/GiB-s; Volumes $0.09/GiB-mo; Starter $30/mo credit |
| Session | Hobby ≤1 h, Pro ≤24 h; 20 / 100 concurrent (add-ons to 1,100) | default 5 min, configurable to 24 h; idle timeout; filesystem snapshots to resume |
| Persistence | pause saves filesystem **and memory**, kept until killed | Volumes + snapshots |
| SDKs | Python, JS/TS (code-interpreter SDK) | Python |
| Network control | not stated in fetched docs | not stated in fetched docs |

**Cost per analysis run [est.]:** a 2-vCPU/1 GiB sandbox alive 30 s ≈ E2B $0.00098, Modal ≈ $0.00046. Even at 200 runs/tenant/month this is < $0.25/tenant/month. Cold start is not documented on the pages fetched (E2B markets ~150 ms **[unverified]**). Self-hosting alternative: Windmill (AGPL/EE, Postgres-backed queue, **nsjail** sandboxing, Python via uv, TS via Bun/Deno, Go, SQL) — attractive because we already run Postgres and want the tenant extract to stay in-VPC.

### 2.3 MotherDuck's text-to-SQL work
- **DuckDB-NSQL-7B** (with Numbers Station, early 2024): trained on ~200k synthetic DuckDB queries + 250k general pairs, "aware of all documented features in DuckDB 0.9.2". No headline accuracy published on the fetched page. Dated, but the lesson — a *dialect-specialised* small model + docs-aware prompting — transfers.
- **`prompt()` SQL function** (MotherDuck): per-row LLM calls with `struct`/`json_schema`/`return_type` structured output; enum return types for classification; NULL on row failure; cost in "AI Units" (≈8,000 rows/unit with gpt-4o-mini vs 480 with gpt-4o). Pattern worth copying in our own SQL surface: `classify_narration(text) RETURNS enum` backed by a cached Fireworks call.

### 2.4 Safety and latency envelope
- Sandbox receives a **tenant-scoped Parquet/DuckDB extract** (section 5 nightly job) or a *tenant-role* Postgres connection — never a superuser DSN. No outbound network except the model gateway. Kill at 60 s CPU / 2 GiB.
- Latency: extract open (<50 ms for a 20–50 MB DuckDB file **[est.]**) + model code-gen (2–6 s open model) + execution (sub-second for ≤5M rows) ⇒ 4–10 s end-to-end, acceptable for "analysis" mode; keep Tier A for chat-speed answers.
- Egress control: return only tables ≤ N rows and images; enforce with a result-size gate before anything re-enters the prompt.

---

## 3. Retrieval over the books

### 3.1 Postgres-native search stack (versions as of Sept 2026)

| Component | Version / facts | Use here |
|---|---|---|
| **pgvector 0.8.6** | HNSW + IVFFlat; `vector`/`halfvec` (16k dims), `bit`, `sparsevec`; **iterative index scans** since 0.8.0 (`hnsw.iterative_scan = strict_order|relaxed_order`, `hnsw.max_scan_tuples` default 20,000, `hnsw.ef_search` default 40) fix the "filtered HNSW returns too few rows" problem — critical when every query is filtered by `tenant_id`. | Embeddings of narrations, party/item names, document chunks. |
| **pgvectorscale** | StreamingDiskANN + Statistical Binary Quantization; **label-based filtered search** (Filtered DiskANN); claims 28× lower p95 latency / 16× throughput vs Pinecone s1 at 99% recall on 50M×768; PostgreSQL licence. | Only if a single index exceeds ~10M vectors; otherwise pgvector 0.8 with per-tenant partitions suffices. |
| **ParadeDB pg_search** | BM25 via Tantivy in Postgres; hybrid with pgvector; AGPL-3.0 (Enterprise for commercial). | AGPL is a consideration for a SaaS; Postgres `tsvector` + `pg_trgm` is licence-free and adequate for names/narrations. |
| **Splink 4** (MoJ, MIT) | Fellegi–Sunter probabilistic linkage; backends DuckDB (default), Spark, **Postgres**, Athena; "a million records on a laptop in around a minute"; unsupervised (EM), term-frequency adjustments. | Party-master dedup, supplier-name ↔ ledger linkage, GSTIN-less matching. |

### 3.2 What to embed vs keep relational

Keep relational (exact, indexed): amounts, dates, GSTIN, PAN, HSN/SAC codes, voucher numbers, cheque/UTR numbers, ledger group hierarchy. **Embed** (fuzzy): bank-statement narrations ("UPI/DR/4123…/RAMESH TRADERS/…"), party names and aliases, stock item names/descriptions, voucher narrations, document chunks. Matching pipeline for bank ↔ ledger: (1) deterministic — exact amount ± date window ± UTR/cheque; (2) lexical — `pg_trgm` similarity on cleaned tokens (strip UPI/NEFT/IMPS prefixes, digits); (3) semantic — cosine over `halfvec(1024)`; (4) Splink model on (name, amount-bucket, periodicity) to produce a calibrated match probability; (5) learned per-tenant "narration pattern → ledger" memory table (section 6) that short-circuits 1–4 after the first confirmation.

**Multilingual / Hinglish embeddings (open weights):** BGE-M3 (MIT, 1024-d, 8192 tokens, 100+ languages, dense+sparse+ColBERT from one model, XLM-R base); multilingual-e5-large (MIT, 1024-d, **512-token cap**, 100 languages incl. Hindi, low-resource degradation noted); Qwen3-Embedding 0.6B/4B/8B (Apache-2.0, 32k context, MRL dims 32–1024, instruction-aware; 8B topped MTEB-multilingual at 70.58 in June 2025). Fireworks embedding pricing: **$0.008/M tokens (≤150M params), $0.016/M (150–350M), $0.10/M (Qwen3-Embedding-8B)**. None of these publish Hinglish (romanised Hindi) scores; test on our own narration corpus before choosing — romanised business names ("Shree Balaji Traders") are mostly handled as Latin-script tokens anyway.

### 3.3 Statutory RAG (GST law, rates, HSN, circulars)

- Ground truth for rates changed on **22 Sept 2025** after the **56th GST Council (3–4 Sept 2025, New Delhi)**: 5% / 18% / 40% structure, 12% and 28% largely eliminated; e.g. small cars, ACs, TVs → 18%; aerated drinks, >350 cc motorcycles, betting → 40%; 33 lifesaving drugs and individual health/life insurance → nil (cleartax summary; gstcouncil.gov.in hosts the press release and FAQ). **Any LLM whose weights predate late 2025 will confidently emit the old 12%/28% slabs** — grounding is mandatory, not optional.
- Corpus design: version every rate notification/circular as a row with `(source_url, doc_type, notification_no, issued_on, effective_from, effective_to NULL, superseded_by)`; chunk ≤512 tokens with the header (Chapter/HSN, rate, condition) prepended; embed with BGE-M3 dense + store `sparsevec` for lexical; retrieval filter `effective_from <= txn_date AND (effective_to IS NULL OR effective_to > txn_date)`; answers must cite `notification_no` + paragraph. NIC/CBIC hosts are geo-fenced to Indian IPs (per project memory) — ingest from an Indian egress or mirror.
- HSN/SAC: keep as a relational table (code, description, chapter, rate history) with `pg_trgm` + embedding on description; never let the model "recall" a code.

---

## 4. Knowledge graph / entity model of a business

### 4.1 Ontologies
- **XBRL**: the global digital-reporting standard (taxonomies = dictionaries of concepts; facts tagged; used in ~220 implementations across 65 countries; MCA India mandates XBRL for certain company filings **[unverified this session]**). Useful as the *vocabulary* for financial-statement line items (map our ledger groups → Ind AS taxonomy concepts) so exports and agent explanations use standard names.
- **FIBO** (EDM Council/OMG, OWL): enterprise financial-services concepts (entities, instruments); does **not** cover SMB bookkeeping — skip.
- **schema.org**: `Invoice`, `Order`, `Organization`, `PostalAddress`, `MonetaryAmount` are adequate for document-level entity typing; use for extracted-document JSON, not for the ledger.
- Practical stance: the double-entry ledger *is already a graph* (party —voucher— ledger —group— statement line). A separate ontology adds little; a **data dictionary** does.

### 4.2 Graph stores vs relational "business facts"

| Option | Facts | Verdict |
|---|---|---|
| **Apache AGE 1.8.0** | openCypher on Postgres 11–18, Apache-2.0, 4.8k★, Go/Python drivers | Viable if we want Cypher for path questions ("who supplies the items I sell to X?"). Adds an extension to the primary. |
| **Graphiti (Zep)** | Apache-2.0, 30.7k★; **bi-temporal** facts ("old facts are invalidated — not deleted"); hybrid BM25+embedding+graph retrieval; backends Neo4j 5.26+, FalkorDB, Neptune (Kuzu 0.11.2 **deprecated**) | Best-in-class design for *agent memory*; requires a graph DB we do not otherwise run. Copy the bi-temporal schema into Postgres instead. |
| **Kuzu** | **Archived 10 Oct 2025**, MIT, 0.11.3 last | Do not adopt. |
| **Neo4j / Memgraph** | Not fetched this session | Extra infra; unjustified for SMB scale. |
| **Relational business-facts tables** | `facts(tenant_id, subject_type, subject_id, predicate, object, valid_from, valid_to, source_ref, confidence)` | Postgres-first, RLS-native, queryable by the DSL. **Recommended.** |

### 4.3 GraphRAG vs vector RAG evidence
Microsoft GraphRAG (arXiv 2404.16130, rev. Feb 2025) shows gains in *comprehensiveness and diversity* for **global sensemaking** questions over ~1M-token corpora; the systematic comparison (arXiv 2502.11371) concludes neither dominates and that *selection/integration* of both gives consistent gains. For accounting data the "global" questions ("what changed this quarter?") are better answered by **SQL aggregates** than by community summaries; reserve graph-style retrieval for the statutory corpus and for provenance walks over the version log.

### 4.4 How much structure to put in the prompt (tenant context packing)

Token estimates for a typical Indian SMB on Tally **[est., ~15–25 tokens per compact line]**:

| Master | Typical count | Compact card size |
|---|---|---|
| Ledger groups (Tally's 28 primary + user) | 40–80 | 0.6–1.5k tokens |
| Ledgers | 300–1,500 | 6–30k |
| Parties (subset of ledgers, with GSTIN, state, credit days) | 100–800 | 3–20k |
| Stock items (name, unit, HSN, rate) | 200–3,000 | 4–60k |
| Cost centres, godowns, voucher types, price lists | 20–200 | <3k |
| **Full masters** | | **15–110k tokens** |
| **Tiered card** (groups + top-50 ledgers by activity + counts + naming patterns) | | **2–5k tokens** |

Strategy: a *stable* per-tenant "business card" (2–5k tokens: industry, FY, GST scheme, group tree, top ledgers/parties/items, naming conventions, known recurring patterns) goes in the **system prompt behind a cache breakpoint**; everything else is retrieved on demand (schema-selector step, exactly as CHESS/XiYan do). Prompt caching economics: Anthropic — 5-min cache write 1.25×, 1-h write 2×, read 0.1× base; minimum cacheable 512–4,096 tokens by model; 4 breakpoints; hierarchy tools → system → messages, so keep tool definitions and the tenant card static. **Fireworks** — automatic longest-prefix caching, 50% discount on cached tokens serverless (near-free on dedicated), separate caches per account, `x-session-affinity`/`user` for replica routing, `x-prompt-cache-isolation-key` for hard per-tenant isolation. Up to 80% TTFT reduction.

---

## 5. Event-driven and scheduled agent triggers

| Mechanism | Facts | Role in our stack |
|---|---|---|
| **Postgres LISTEN/NOTIFY** (PG16) | delivered at commit, deduped within a txn, payload ≤ 8,000 B, 8 GB queue, **no persistence / at-most-once**, cannot be used with 2PC | Wake-up signal only; never the source of truth. |
| **Outbox** (we have one) | — | Source of truth for events; NOTIFY just says "poll now". |
| **River** (Go, MPL-2.0, 5.6k★) | Postgres-backed; **transactional enqueue**; periodic/cron jobs; unique jobs; pgx v5 + database/sql; River UI | **Primary job runner** for the Go monolith: agent triggers, nightly features, doc pipelines. |
| **pg-boss** (Node, MIT, 3.9k★) | SKIP LOCKED, cron, retries, DLQ, LISTEN/NOTIFY assist; PG 13+ | Not our language. |
| **Hatchet** (MIT, 7.9k★) | Postgres durability, DAGs, rate limits, concurrency keys, 10k tasks/s load-tested; Go/Python/TS SDKs | Consider if AI-plane (Python) workflows need DAG + durable replays across Go and Python. |
| **Temporal** (MIT, Go server; Postgres persistence) | mature durable execution | Heavier ops footprint than River/Hatchet; defer. |
| **Inngest / Trigger.dev** | Inngest: TS/Python/Go, per-tenant concurrency/throttling/batching keys; Trigger.dev v4: TS-only, self-hostable | Trigger.dev excluded (TS-only). Inngest's *tenant-keyed flow control* is the feature to copy in River (queue per priority + tenant fairness). |
| **Windmill** (AGPL/EE, 17.8k★) | Postgres queue, nsjail sandbox, many languages, schedules/webhooks/Kafka triggers | Candidate for the **sandbox runtime** (section 2), not the queue. |
| **CDC: Sequin** (MIT, Elixir, 2.2k★) | logical replication → Kafka/SQS/webhooks/Elastic/Redis; ">50k ops/s, 55 ms" vs Debezium 6–23k ops/s; backfills; exactly-once via idempotency keys; PG 14+ | Only if we need to fan out to external sinks; the outbox already gives us ordered, transactional events. |
| **pgstream** (Xata, Apache-2.0, Go) | CDC **with DDL tracking**, snapshots, transforms (greenmask/neosync) | Useful for building the per-tenant analytical replica / anonymised cross-tenant sets. |
| **pg_cron 1.6** | cron and `'10 seconds'` intervals; one DB per cluster (`cron.schedule_in_database`); no run on hot standby | Refresh of materialized views and small nightly SQL. |
| **TimescaleDB continuous aggregates** | incremental time-bucketed materialisation; real-time aggregates (off by default ≥2.13); hierarchical caggs; joins since 2.16 | Only if we adopt Timescale; plain MVs + pg_cron cover us. |
| **dbt / SQLMesh** | SQLMesh: virtual environments, incremental models, Postgres among 10+ dialects | Overkill for per-tenant nightly features living in Go/SQL; revisit for the cross-tenant warehouse. |

**Ambient agents** (LangChain, 14 Jan 2025): agents "listen to an event stream and act on it", with three HITL primitives — *notify*, *question*, *review* — and an "Agent Inbox". This maps cleanly onto accounting: bank line arrives → propose match (review); GSTR-2B mismatch → notify; unknown party in an invoice → question.

**Nightly per-tenant feature computation, cheaply:** River periodic job per tenant, staggered by hash; each job runs (a) SQL features into typed tables (section 6) inside the RLS'd connection, (b) `COPY ... TO` Parquet (or `duckdb` `ATTACH` pull) into object storage as `tenant/{id}/{date}/*.parquet` for the sandbox tier, (c) embedding of new narrations/parties (Fireworks batch, $0.008/M tokens). For a 100k-voucher tenant this is seconds of DB time and <$0.01 of embedding **[est.]**.

---

## 6. Feature store / "business profile" for the agent

### 6.1 Features worth computing (per tenant, nightly + on-event)

| Entity | Features | Method |
|---|---|---|
| Customer (party) | DSO, on-time %, avg days late, ageing bucket mix, disputed %, trend (last 3 vs prior 3 months), credit utilisation vs limit, seasonality | SQL windows over bill-wise allocations |
| Supplier | lead time, price variance vs last PO, GSTR-2B filing regularity (ITC risk), returns % | SQL + GSTR-2B/2A ingest |
| Item | velocity (units/week), days-of-cover, dead-stock flag, margin, price elasticity proxy | SQL |
| Bank/ledger | recurring streams (rent, EMI, salaries, subscriptions), expected next date/amount, missed-payment flag | periodicity detection below |
| Tenant | seasonality curve, GST liability run-rate, cash-runway, edit-log anomaly rate | SQL |

### 6.2 Recurring-transaction detection
Plaid's Recurring Transactions API asks for **≥180 days of history** for optimal results and returns per-stream frequency, status, predicted next date, average/last amount, `is_active` (field list from memory of the API reference — page was truncated **[unverified]**). Actual Budget's "Find schedules" requires **identical payee names** to group (documented limitation) — i.e., matching quality upstream determines detection quality. Algorithm we can implement in SQL/Polars: group by canonical counterparty (after the section-3 linkage) → sort dates → inter-arrival deltas → classify into {weekly, fortnightly, monthly, quarterly, annual} if median delta within ±15% and ≥3 occurrences → amount tolerance ±10% or fixed → status `mature/early/tombstoned` → `predicted_next = last + median_delta`. Store confidence and the evidence rows.

### 6.3 Storage: JSONB vs typed tables vs Feast
- **Feast** (Apache-2.0, 7.3k★): offline stores incl. Postgres and DuckDB, online stores incl. Postgres/SQLite; point-in-time-correct joins. It solves training-serving skew for ML models — we have no such models yet. Skip.
- **Typed tables** (`party_profile(tenant_id, party_id, as_of, dso_days numeric, on_time_pct …)`, PK (tenant_id, party_id, as_of)) for the ~30 stable features: indexable, RLS'd, queryable by the DSL (they become *reports*).
- **JSONB `extras`** column for experimental features; promote to typed columns after two releases.
- **Freshness**: `as_of` + `computed_at`; the tool response states freshness ("as of last night 02:10 IST"); on-event recompute for the touched party only (River job keyed `(tenant, party)` unique).
- **Tools**: `get_customer_profile(party_id)`, `get_item_profile(item_id)`, `get_recurring_streams(ledger_id)`, `get_tenant_profile()` — each returns ≤1k tokens, cites `as_of`, and is *also* a DSL report so the UI can show the same numbers.

---

## 7. Document & unstructured ingestion at scale

| Tool | Facts (Sept 2026) | Fit |
|---|---|---|
| **Docling** (LF AI & Data, IBM; MIT; 66.2k★) | PDF/DOCX/XLSX/HTML/**EML/MSG**/images/**XBRL**; DocLayNet layout + TableFormer tables; GraniteDocling 258M VLM; **bounding-box provenance in DoclingDocument**; lossless JSON export | **Default parser.** Provenance is exactly what we need for field → bbox audit links. |
| **Marker** (Datalab) | 76.0% olmocr-bench balanced at 2.9 pages/s GPU; 66.6% fast at 7.4 p/s; CPU no-OCR 43.6%; `--use_llm` hybrid; code Apache-2.0 but **weights OpenRAIL-M (startups <$5M funding)** | Licence friction for a funded SaaS; use only if Docling underperforms on scanned Indian statements. |
| **unstructured** (Apache-2.0, 15.4k★) | fast/hi_res/ocr_only; elements carry coordinates; 60+ types incl. .eml/.msg | Alternative to Docling for email bodies. |
| **LlamaParse** | tiers Fast / Cost Effective / Agentic / Agentic Plus; JSON with bounding boxes; SDKs Py/TS/Go/Java | Managed fallback for hard scans. |
| **Reducto** | parse/extract/split/classify; r-1 unified model; **bounding-box citations per extracted field**; SOC2/HIPAA, 24 h deletion; Go SDK | Managed schema-guided extraction with provenance; the product behaviour to emulate. |
| **Open VLMs: Qwen3-VL** (Apache-2.0; 8B dense and MoE sizes) | OCR in **32 languages**, 2D grounding, long-document structure parsing, 256K native context | Schema-guided extraction with JSON schema + grounding boxes, hosted on Fireworks; handles Hindi/Gujarati/Marathi text on invoices. |
| **OmniAI OCR benchmark** | JSON-accuracy = 1 − (diff fields / total fields); providers GPT-4o, Gemini, Claude, Mistral OCR, Qwen, Textract, Azure DI, Google DocAI (results page redirected; numbers not re-verified) | Use the *methodology* (JSON field accuracy, not text similarity) for our own eval set of Indian bank statements and GST invoices. |

Pipeline: source (WhatsApp image / Gmail push via Pub/Sub `watch()` renewed ≤7 days with `history.list` catch-up and 1 event/s/user cap / Outlook Graph / upload) → dedupe (SHA-256 of bytes + perceptual hash for photos) → Docling parse (bbox-bearing JSON) → **schema-guided extraction** (Qwen3-VL for images/scans, text LLM for born-digital) into typed Pydantic models with per-field `{value, page, bbox, confidence}` → validators (GSTIN checksum, HSN existence, totals reconcile) → *draft voucher* with provenance links → human review (HITL "review") → post. Store the original blob immutably (8-year retention, section 8) and the extraction JSON alongside the voucher version.

**Tally XML backups/exports**: Tally exposes an XML/TDL server on port 9000; `tally-database-loader` (MIT, Node) shows the mapping of masters (groups, ledgers…) and vouchers to relational tables with incremental sync — a good reference for our Tally importer's field coverage. **Excel**: parse with Docling/openpyxl; map columns with the LLM once, then persist the mapping per tenant as a reusable "import template".

---

## 8. Multi-tenant AI data architecture

- **Isolation of embeddings/memory**: every vector, memory, feature and document table carries `tenant_id` with the same `FORCE RLS` policy as the ledger (Supabase's RAG-with-permissions guide demonstrates RLS on pgvector tables and warns RLS is latency-sensitive — use `current_setting('app.tenant_id')` not cross-table lookups). With pgvector 0.8 iterative scans, a tenant filter on a shared HNSW index remains correct; for large tenants, **partition the embeddings table by tenant hash** so HNSW indexes are per partition.
- **Per-tenant prompt caches**: Fireworks isolates caches per account, not per end-tenant, on serverless; on dedicated it is a single shared cache unless `x-prompt-cache-isolation-key` is set. Set it to the tenant id — a cache *hit* leaks nothing in content but timing could reveal that another tenant shares a prefix; isolation removes even that.
- **Cross-tenant aggregates** (benchmarks like "your DSO vs peers in textiles"): build from a pgstream/anonymised replica with k-anonymity thresholds (≥20 tenants per cell **[our policy, est.]**), never from live RLS tables; publish as read-only reference tables.
- **Retention**: Companies Act 2013 s.128(5) — books and vouchers ≥ **8 financial years**; electronic books must be backed up on servers physically in India, and from FY 2021-22 accounting software must record an **audit trail / edit log of every change, non-disableable** (our hash-chained version log satisfies this). CGST s.36 — 72 months from the due date of the annual return, longer if proceedings pending **[unverified this session]**.
- **DPDP Act 2023 / Rules 2025**: Data Protection Board and core provisions commenced **13 Nov 2025**; fiduciary obligations phase in **13 Nov 2026**; remaining provisions **13 May 2027**; erasure right exists but retention **required by law** is exempt; penalties from ₹50 crore. Reconciliation with an immutable ledger: (1) separate *personal* data (phone, email, address, PAN of individuals) into referenced tables from the *transactional* data; (2) erasure = crypto-shred or overwrite the referenced personal row while the ledger keeps the party id and legally required identifiers (GSTIN/PAN on tax invoices are statutory); (3) the hash chain covers voucher content, not the mutable contact table, so shredding does not break the chain; (4) AI-derived data (embeddings of narrations that contain names, memories, profiles) is derived personal data — cascade erasure to those tables and to the sandbox extracts (they are regenerated nightly, so a 24 h SLA is natural).
- **Export of AI-derived data**: include `party_profile`, recurring streams, match decisions, and extraction provenance JSON in the tenant export bundle; embeddings are optional (regenerable).

---

## 9. Recommendation: a concrete data-layer design

### 9.1 Query tiers
1. **Semantic catalogue + DSL (Tier A)** — extend the existing DSL catalogue with per-report *agent metadata* (synonyms incl. Hinglish, description, typical questions, example DSL). Implement a CHESS-style **selector** step: embed the question, retrieve top-k reports + top-k entities (parties/items/ledgers) via pgvector, then generate the DSL. Log every (question, DSL, accepted?) pair as *golden examples* per tenant for few-shot retrieval (Vanna's core idea) — this is the "keeps learning" loop.
2. **Guarded SQL over read-model views (Tier B)** — `agent_ro` role, `SET ROLE` per request, `SET LOCAL app.tenant_id`, `READ ONLY` txn, `statement_timeout=5s`, AST allow-list, forced `LIMIT 1000`, view-only schema, EXPLAIN cost gate. Model: OmniSQL-32B or Arctic-Text2SQL-R1 on Fireworks with M-Schema cards of selected views + 3 sample rows.
3. **Sandbox analysis (Tier C)** — DuckDB + Polars in a locked sandbox (Windmill/nsjail in-VPC first; E2B/Modal as burst) over the nightly Parquet extract; browser DuckDB-Wasm/Pyodide mode for zero-egress "explore" screens.

### 9.2 Embedding tables (all RLS'd, `halfvec(1024)`, HNSW, partitioned by tenant hash)
`emb_narration(voucher_line_id)`, `emb_bank_line(bank_line_id)`, `emb_party(party_id, alias)`, `emb_item(item_id)`, `emb_doc_chunk(doc_id, page, bbox)`, `emb_catalog(report_id)`, `emb_golden_query(tenant_id, question, dsl)`. Global (no tenant): `emb_statute_chunk` with validity dates. Model: BGE-M3 dense (+ `sparsevec` lexical) via Fireworks/TEI; re-evaluate Qwen3-Embedding-4B on our Hinglish set.

### 9.3 Business-profile tables and jobs
Typed `party_profile`, `item_profile`, `supplier_profile`, `recurring_stream`, `tenant_profile`, each with `as_of` and `extras jsonb`; computed by River periodic jobs (staggered per tenant) plus on-event recompute from the outbox; exposed as DSL reports *and* as `get_*_profile` tools.

### 9.4 Event triggers
Outbox → River (transactional enqueue) → agent "ambient" handlers with three HITL outcomes (notify / question / review) landing in an in-app inbox. LISTEN/NOTIFY only to cut poll latency. pg_cron for MV refresh. No Kafka/CDC until an external sink exists.

### 9.5 Document pipeline
Docling (bbox JSON) → Qwen3-VL/text-LLM schema-guided extraction → validators → draft voucher with per-field provenance → review. Gmail Pub/Sub watch + Graph webhooks for mailboxes; WhatsApp via upload/Business API. Originals in object storage under 8-year retention; personal-data fields referenced, not copied.

### 9.6 Statutory corpus
Versioned, date-validity-filtered chunks of CGST/SGST/IGST Acts, rate notifications (post-22-Sept-2025 schedule first), circulars, HSN/SAC master; ingested from Indian egress; every answer cites notification number and paragraph; rate lookups are relational, never recalled.

### 9.7 Estimated per-tenant monthly cost [est.]
| Item | Assumption | Cost |
|---|---|---|
| Embeddings | 20k new lines × 25 tokens = 0.5M tokens @ $0.016/M | ≈ $0.01 |
| Nightly features + extract | 30 River jobs, seconds of DB each | ≈ $0.02 in DB/compute |
| Sandbox runs | 100 runs × 30 s × 2 vCPU/1 GiB (E2B rates) | ≈ $0.10 |
| Agent LLM calls | 300 turns × 6k prompt (70% cached at 50% discount) + 400 out, mid-size open model at ~$0.2–0.9/M | ≈ $0.30–1.50 |
| Document extraction | 200 pages × VLM (~1.5k tokens/page) | ≈ $0.10–0.40 |
| Storage | 50 MB Parquet + 200 MB blobs | < $0.02 |
| **Total** | | **≈ $0.6–2.1 / tenant / month**, dominated by LLM tokens — hence the cached tenant card and Tier-A-first routing. |

---

## Sources (fetched this session)

1. https://spider2-sql.github.io/
2. https://github.com/xlang-ai/Spider2
3. https://arxiv.org/abs/2411.07763
4. https://bird-bench.github.io/
5. https://livesqlbench.ai/
6. https://bird-interact.github.io/
7. https://arxiv.org/abs/2505.20315 (Arctic-Text2SQL-R1)
8. https://arxiv.org/abs/2503.02240 (OmniSQL)
9. https://arxiv.org/abs/2411.08599 (XiYan-SQL)
10. https://arxiv.org/abs/2405.16755 (CHESS)
11. https://arxiv.org/abs/2311.07509 (data.world KG benchmark)
12. https://www.getdbt.com/blog/semantic-layer-as-the-data-interface-for-llms
13. https://docs.snowflake.com/en/user-guide/views-semantic/overview
14. https://docs.databricks.com/aws/en/metric-views/
15. https://docs.cube.dev/docs/introduction
16. https://github.com/malloydata/malloy
17. https://github.com/vanna-ai/vanna
18. https://github.com/Canner/WrenAI
19. https://github.com/eosphoros-ai/DB-GPT
20. https://github.com/duckdb/pg_duckdb
21. https://github.com/duckdb/duckdb-postgres
22. https://duckdb.org/docs/current/clients/wasm/overview.html
23. https://github.com/pyodide/pyodide
24. https://e2b.dev/pricing
25. https://docs.e2b.dev/
26. https://modal.com/docs/guide/sandbox
27. https://modal.com/pricing
28. https://motherduck.com/blog/duckdb-text2sql-llm/
29. https://motherduck.com/docs/sql-reference/motherduck-sql-reference/ai-functions/prompt/
30. https://github.com/pgvector/pgvector
31. https://github.com/timescale/pgvectorscale
32. https://github.com/paradedb/paradedb
33. https://github.com/moj-analytical-services/splink
34. https://huggingface.co/BAAI/bge-m3
35. https://huggingface.co/intfloat/multilingual-e5-large
36. https://huggingface.co/Qwen/Qwen3-Embedding-0.6B
37. https://supabase.com/docs/guides/ai/rag-with-permissions
38. https://gstcouncil.gov.in/
39. https://cleartax.in/s/gst-rates
40. https://github.com/getzep/graphiti
41. https://github.com/apache/age
42. https://github.com/kuzudb/kuzu
43. https://arxiv.org/abs/2404.16130 (GraphRAG)
44. https://arxiv.org/abs/2502.11371 (RAG vs GraphRAG)
45. https://www.xbrl.org/the-standard/what/an-introduction-to-xbrl/
46. https://spec.edmcouncil.org/fibo/
47. https://platform.claude.com/docs/en/build-with-claude/prompt-caching
48. https://docs.fireworks.ai/guides/prompt-caching
49. https://fireworks.ai/pricing
50. https://www.postgresql.org/docs/16/sql-notify.html
51. https://github.com/riverqueue/river
52. https://github.com/timgit/pg-boss
53. https://github.com/hatchet-dev/hatchet
54. https://github.com/temporalio/temporal
55. https://www.inngest.com/docs
56. https://trigger.dev/docs/introduction
57. https://github.com/windmill-labs/windmill
58. https://github.com/sequinstream/sequin
59. https://github.com/xataio/pgstream
60. https://github.com/citusdata/pg_cron
61. https://www.tigerdata.com/docs/use-timescale/latest/continuous-aggregates
62. https://sqlmesh.readthedocs.io/en/stable/
63. https://www.langchain.com/blog/introducing-ambient-agents
64. https://github.com/feast-dev/feast
65. https://plaid.com/docs/api/products/transactions/
66. https://actualbudget.org/docs/schedules/
67. https://github.com/docling-project/docling
68. https://github.com/datalab-to/marker
69. https://github.com/Unstructured-IO/unstructured
70. https://developers.llamaindex.ai/python/cloud/llamaparse/getting_started
71. https://docs.reducto.ai/overview
72. https://github.com/getomni-ai/benchmark
73. https://huggingface.co/Qwen/Qwen3-VL-8B-Instruct
74. https://developers.google.com/gmail/api/guides/push
75. https://github.com/dhananjay1405/tally-database-loader
76. https://ca2013.com/128-books-of-account-etc-to-be-kept-by-company/
77. https://en.wikipedia.org/wiki/Digital_Personal_Data_Protection_Act,_2023

*Not reachable this session (403/404): atscale.com semantic-layer benchmark, cube.dev AI-API blog, pyodide.org docs, debezium.io connector docs, PIB 56th-Council press release, plaid recurring-transactions guide, monumint/omni OCR results page, CGST s.36 commentaries. Claims depending on them are flagged [unverified].*
