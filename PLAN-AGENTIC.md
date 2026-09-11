# Agentic Platform Plan — from "AI insight plane" to an agent that can operate the whole product and knows the business

> **Status (2026-09-08): decisions D1–D10 approved by the founder.** The architecture in §1 and the non-negotiables in §2 are settled; the open items are the six operational choices in §7, which W0/W1 resolve with data.

*2026-09-08. Companion to `RESEARCH-AGENTIC.md` (the evidence) and `PLAN.md` (the original architecture). This plan turns the ten decisions D1–D10 into workstreams grounded in the packages that exist today. It supersedes the model-strategy half of PLAN.md's AI ADR; it keeps and hardens its four load-bearing principles: human-in-the-loop on every write, DSL-not-SQL by default, deterministic engines with the LLM as explainer, India residency for documents and PII.*

---

## 1. What we are building, in one screen

```
                 ┌───────────────────────────── channels ─────────────────────────────┐
                 │ Alt+G palette + side panel (AG-UI)   WhatsApp (Cloud API, Flows)   │
                 │ voice (Saaras/IndicConformer)        email      MCP server (Go SDK)│
                 └───────────────┬──────────────────────────────────────┬─────────────┘
                                 │ ActionRequest {principal, channel, args, provenance}
   ┌─────────────────────────────▼──────────────────────────────────────▼─────────────┐
   │ cored (Go)                                                                        │
   │  actions/   ActionSpec registry ← generated from routeAction vocabulary +         │
   │             keystroke command table; meta-tools search/preview/commit/status      │
   │  agent/     plan-and-execute loop · structured output · tool search · verifier    │
   │             = existing validators · DBOS/Restate durable runs · HumanInterrupt    │
   │  policy/    principal = user ∩ agent ∩ delegation · risk classes · autonomy L0–L3 │
   │             · intent log (hash-chained, INSERT-only) · approval inbox             │
   │  query/     Tier A semantic catalogue (existing DSL + synonyms + golden examples) │
   │  readmodel/ Tier B guarded SQL over RLS views (agent_ro, AST allow-list, 5 s)     │
   │  profile/   tenant_profile · entity_facts (bi-temporal) · exemplars · rules       │
   │  insights/  detectors → insights table → ranker → digest → feedback/suppression   │
   │  jobs/      River (transactional enqueue) · outbox → ambient handlers · pg_cron   │
   │  llm/       provider abstraction (Fireworks · DeepInfra · Sarvam · self-host)     │
   │             json_schema/grammar · caching · budgets · trajectory logger → OTel    │
   └───────────────┬──────────────────────────────┬─────────────────────┬─────────────┘
                   │ durable step                 │ read-only extract   │ OTLP/HTTP
   ┌───────────────▼──────────────┐  ┌────────────▼──────────┐  ┌───────▼──────────────┐
   │ insightd (Python worker)     │  │ Tier C sandbox        │  │ Langfuse (self-host, │
   │ PaddleOCR-VL · redaction ·   │  │ DuckDB/Polars, gVisor │  │ ap-south-1) · evals  │
   │ extraction · embeddings ·    │  │ over nightly Parquet  │  │ · canaries · drift   │
   │ StatsForecast/LightGBM/ALS · │  │ per tenant, no egress │  └──────────────────────┘
   │ DSPy/GEPA · eval harness     │  └───────────────────────┘
   └──────────────────────────────┘
   Postgres 16 · FORCE RLS · hash-chained voucher versions · pgvector 0.8 · River · outbox
```

**The agent never gains a write path the human does not have.** Every write is an `ActionSpec` the UI also uses, evaluated by the same PDP, validated by the same code, versioned into the same chain, and gated by a policy the tenant controls.

---

## 2. Non-negotiables (carry into every workstream's adversarial review)

1. **No unapproved write.** A commit requires either an accepted `HumanInterrupt` or an L2/L3 policy row the tenant set, and it is replayed from an approved plan id, never regenerated.
2. **No LLM-computed money.** The model emits references and operations; Go computes in paise; every digit in a narrative is bound to a tool result (number-provenance check).
3. **No raw SQL from the model against base tables.** Tier A DSL by default; Tier B only over `security_barrier` views under `agent_ro` with AST allow-list, `READ ONLY`, 5 s timeout, forced `LIMIT`.
4. **No untrusted value reaches money movement without a human.** Provenance labels travel with arguments; policy enforces it; guard models only raise the review level.
5. **No cross-tenant leakage.** Memory, embeddings, exemplars, rules, profiles all under the same `FORCE RLS`; cross-tenant priors only from a separate k ≥ 20 noisy aggregate schema.
6. **No agent action without an attributable, immutable record.** Intent-log row per attempt, linked into the hash chain, exportable as an AI action register.
7. **No PII to a non-India inference endpoint without redaction and a tenant consent flag.** Tier B/C, OCR, embeddings and traces stay in India by default.
8. **Every wave ends with an adversarial review pass** with authority to send work back (the repo's standing rule), plus the CI eval gate of W8.

---

## 3. Workstreams

Each workstream lists what it builds on today, deliverables, the gate that proves it, and dependencies. Sizes are engineering-weeks for one lane; lanes that touch disjoint packages run concurrently as dynamic workflows.

### W0 — Foundations: provider abstraction, trajectory logging, action registry skeleton (3 wk)

Builds on: `internal/nlq` (only network-touching translator, pinned by `archtest`), `authz/vocabulary.go` + `routeAction()`, `frontend/src/keystroke/types.ts`, `insightd/adapters`.

Deliverables:
- `internal/llm`: OpenAI-compatible client with per-model adapters (Fireworks, DeepInfra, Sarvam Inference, self-hosted vLLM), `json_schema` and grammar mode, `tool_choice`, prompt-prefix ordering (static first), cache metrics, per-tenant token budget `reserve/settle`, retries with schema repair (≤2), provider-fingerprint capture per response.
- Trajectory logger: every call and tool step as an OTel span with `gen_ai.*` attributes, `tenant_id`, `feature`, `model_id`, `prompt_version`, exported OTLP/HTTP; recorded tool results as replayable cassettes.
- `internal/actions`: `ActionSpec` type (per RESEARCH-AGENTIC §4 D1 and r5 §8.7) and a **generator** that emits one spec per `(action, resource)` arm in `routeAction()` and per screen command, with the ACL key equal to the arm; **coverage test** failing CI on any UI command without an action or any action without a UI path.
- `nlq` re-pointed at `internal/llm` (GLM-5.3-Flash for translation) with the existing golden NL→DSL tests green.

Gate: archtest still proves `query` is network-free; coverage test enumerates ≥ 60 actions; NL→DSL exact-AST + result-set equality on the fixture ledger ≥ current Claude baseline − 0.5 pt; trajectories visible in a local Langfuse.

### W1 — Model bake-off and golden sets (2 wk, parallel with W0)

Deliverables: 200-task tau-style suite (voucher entry incl. item invoices, bank match, GSTR-1/3B prep, NL query, master creation) in English, Hindi, Hinglish, judged by **ledger state after the run** and pass^5; extraction set of 100 Indian invoices + 50 bank statements (HDFC, ICICI, SBI, Axis, Kotak) with field-level labels; 2–3k narration Hinglish embedding eval; run GLM-5.3, DeepSeek V4 Pro, Qwen3.8-27B, Sarvam-105B, Gemma 4 31B across Fireworks / DeepInfra / Sarvam; measure success, malformed-call rate, tokens, latency, ₹ per task.

Gate: a written model-routing decision (which model per tier, with numbers) replacing the assumptions in D3; the suites become W8's Tier 1/2 evals.

### W2 — Action layer + agent loop + approval inbox (6 wk)

Builds on: W0 registry, `authz` PDP, `webauthn` step-up, voucher API validators, `notify` outbox, `frontend/src/app/ScreenActions.tsx`, `QueryScreen`, `BillInboxScreen`.

Deliverables:
- `internal/agent`: plan-and-execute loop; always-loaded core tools (`actions.search`, `actions.preview`, `actions.commit`, `actions.status`, `query_books`, `get_draft`, `list_pending`); domain packs retrieved by embedding/BM25 over tool descriptions; deterministic verifier = the voucher/GST/master validators, fed back as structured errors, one bounded retry; structured output on every decision step.
- `internal/policy`: agent principal `{agent_id, on_behalf_of, delegation}`; effective rights = ACL(user) ∩ ACL(agent) ∩ delegation; risk classes on every `ActionSpec`; autonomy table `(tenant, user, action, amount_band) → L0..L3` defaulting to L1; structural rules (agent-created parties born blocked; no delete/ACL/period-lock/delegation actions; agent never checker).
- Durable runs: DBOS Transact Go (or Restate after the W0 spike) — run = workflow, LLM/tool call = step, approval = `Recv` on a per-run topic with a days-long timeout; the decision is written to the chain.
- **Intent log** table (INSERT-only role, `prev_hash`, nightly anchor) and the **approval inbox** (approve / edit-then-approve / reject / ask-back) as a new `ScreenKind` reachable from the Gateway and Alt+G, with the diff view mandatory above L1 thresholds and undo as a generated reversal.
- Frontend: agent side panel driven by AG-UI events; the panel pre-fills the *same* native screens (F8/F9/F5…) so Ctrl+A remains the accept key; `preview` renders as the ledger-effect diff the human would see anyway.
- First write actions live: all voucher classes (create/alter via compensating version), masters, sales/purchase orders, bank-reconciliation match/unmatch, reminders (utility templates), exports/prints. Read actions: every report in the vocabulary.

Gate: parity test — 50 recorded keystroke traces replayed via UI and via agent produce identical canonical voucher JSON; injection suite (AgentDojo banking subset + InjecAgent + 30 Tally-specific cases: invoice line text, bank narration, WhatsApp forward, Tally XML `<NARRATION>`) shows **zero** money-moving actions from untrusted values without approval; a three-day approval survives a `cored` restart; adversarial review.

### W3 — Data understanding: tenant card, entity profiles, embeddings, Tier B/C (5 wk, parallel with W2)

Builds on: `internal/query` catalogue, `internal/forecast` (bucketed cash view, rule anomalies), `internal/bankstatement`, migration importer, pgvector.

Deliverables:
- Semantic catalogue: every report in `authz` vocabulary registered with agent metadata (description, synonyms incl. Hinglish, typical questions, example DSL); CHESS-style selector (embed question → top-k reports + entities → DSL); `emb_golden_query(tenant, question, dsl, accepted)` few-shot store; more DSL entities (stock movements, bank lines, GST returns, orders, cost-centre allocations).
- `tenant_profile` (≤1.5k tokens, user-visible and editable) and `entity_facts` / `entity_edges` (bi-temporal, evidence ids, source enum) with the deterministic feature jobs: party payment-lag distributions and DSO, supplier lead time / price drift / GSTR-2B regularity, item velocity / ABC-XYZ / days-of-cover / margin, recurring streams (±3 days / ±5% tolerance, ≥3 occurrences), tenant seasonality and run-rates; ≤400-token narratives for changed entities nightly (Tier-B model, Batch).
- **Cold-start miner** run on Tally import: ledger-mapping habits per (party, narration tokens) → seed exemplars and candidate rules; process chains; books style → profile.
- Embedding tables (`halfvec(1024)`, HNSW, tenant-hash partitions, `FORCE RLS`): narrations, bank lines, party aliases, items, document chunks, catalogue, golden queries; `pg_trgm` lexical; Splink model for party dedup and bank ↔ ledger linkage.
- Tier B: `agent_ro` role, published views, AST validator, EXPLAIN gate, forced LIMIT. Tier C: gVisor sandbox pods over the nightly per-tenant Parquet extract (River job), read-only, no egress, result-size gate.
- Statutory corpus: versioned GST notifications/circulars with `effective_from/to` (post-22-Sept-2025 schedule first), relational HSN/SAC rate history, citation-required retrieval, ingested from Indian egress.
- Tools: `get_customer_profile`, `get_item_profile`, `get_supplier_profile`, `get_recurring_streams`, `get_tenant_profile`, each ≤1k tokens with `as_of`, and each also a DSL report so the UI shows the same numbers.

Gate: NL query accuracy on the W1 suite with catalogue + selector vs without (target ≥ +15 pt); zero Tier-B queries escape the AST allow-list in a 500-case fuzz; profile facts reconcile to the rupee with the reports they summarise; Hinglish embedding eval decides the embedding model; adversarial review.

### W4 — Insight engine v1 and the morning digest (5 wk, after W3)

Builds on: W3 features, `notify` outbox, `internal/forecast`, WhatsApp adapter from W6 (or in-app inbox first).

Deliverables: `insights` / `insight_feedback` / `insight_suppression` tables; real-time detectors on voucher commit (e-way bill needed, credit-limit breach, duplicate bill via Splink, vendor bank change, HSN-rate and place-of-supply mismatch); nightly detectors for the first **15 insight types** by rupee value — predicted-late invoices (LightGBM + SHAP), expected pay date (survival), customer slower than own norm, supplier GSTR-1 not filed (ITC at risk), supplier price creep, early-payment discount worth taking, customers overdue for reorder (BG/NBD), customers to push item X to (implicit ALS + FP-growth + margin), festival uplift (Chronos-2 / StatsForecast with `holidays` covariates), reorder now / stock-out risk, dead stock, 13-week cash path p10/p50/p90 with runway alert, GST liability estimate + ITC available, TDS not deposited, back-dated edits by user; ranker `impact × p_real × actionability × novelty × affinity`; digest composer (≤5 items, numbers by key only, every item drillable to voucher ids); feedback buttons feeding per-type precision and suppression; per-type weekly caps.

Gate: every insight row carries `evidence_sql` and `voucher_ids[]` that reproduce its numbers; a number-provenance check rejects any narrative digit not in `payload_json` (tested with adversarial narrations); backtests — P(late) AUC on the tenant fixture, forecast ensemble vs seasonal-naive on rolling origin; CA panel rates 200 insights, pairwise vs a rules-only baseline; false-positive rate per type below 30% before it may appear in a digest; adversarial review.

### W5 — Documents and bank statements on open weights (4 wk, parallel with W2)

Builds on: `insightd` pipeline (ingest → OCR → PII → classify → infer → mapper → draft), `bankstatement`, Bill Inbox screen, draft proxy and service token.

Deliverables: PaddleOCR-VL-1.6 self-hosted on an India GPU replacing the OCR pre-pass; GLM-5.3-Flash / Qwen3.8-27B with `json_schema` replacing the Haiku/Opus adapters (image-native path for photos and kachha bills); Docling for born-digital PDFs with bounding-box provenance; per-field `{value, page, bbox, confidence}` into the draft; validators (GSTIN checksum, HSN existence, totals reconcile, duplicate detection) deciding confidence; generalisation from purchase invoices to sales invoices, expense bills and **bank statements** (top-10 bank formats, UPI/NEFT/IMPS narration parsing, opening + credits − debits = closing proof); exception-only review with a field count; every Bill Inbox edit stored as a labelled example; WhatsApp media intake once W6 lands.

Gate: field-level F1 on the W1 extraction set ≥ current Opus pipeline; balance proof on every imported statement; the existing PII boundary tests stay green with the new adapters; the Bill Inbox duplicate-post defect class (row 30 of the parity register) is closed by idempotency keys on `commit`; adversarial review.

### W6 — Channels: WhatsApp, voice, MCP server (5 wk, after W2)

Builds on: W2 action layer and inbox, `notify` outbox, `webauthn`.

Deliverables: own WABA on the Cloud API (BSP optional for Payments/Forms out of the box); webhook → persist media (5-minute URLs) → same `ActionRequest` path; utility templates for reminders with UPI link/QR; Flows for structured confirmations; linked-number authentication with step-up above L1 amounts; Sarvam Saaras streaming STT + Bulbul TTS adapter with amount readback before any draft; Hinglish intent grammar ("₹X <party> ko diya/liya/bill") evaluated on code-mixed utterances; MCP server exposing the same registry (Go SDK, spec 2026-07-28, OAuth with CIMD, MRTR `input_required` for confirmations, tool manifest hash pinned per run); AG-UI stream for the in-app panel.

Gate: an owner records a voice note on WhatsApp and a Payment voucher appears as a draft with readback and one-tap approve; a debtor reminder rides the free 24-hour window when they reply; MCP conformance tests plus the injection suite run over the MCP path; adversarial review.

### W7 — Learning loop and cross-tenant priors (4 wk, after W3/W4 have produced signals)

Deliverables: `suggestion_events` capturing shown → accepted / edited (field diff) / rejected / ignored with decider and later reversal; exemplar upsert on every decision; rule proposal at ≥3 consistent corrections with human confirmation, rules evaluated before any model call; procedural memory (`skills`, `strategies`) from successful multi-step runs; nightly consolidation (ADD/UPDATE/DELETE, pruning by utility); weekly regression replay of all corrections against current prompts as a CI job; per-tenant learning-curve dashboard (corrections per 100 vouchers by month; auto-post rate and audited error rate reported separately); `network_priors` schema rebuilt monthly (k ≥ 20, Laplace noise, hashed tokens, provenance for erasure), per-tenant "contribute anonymised statistics" toggle, DPIA per tier; **fine-tuning track** (gated on volume): LoRA SFT on accepted trajectories, KTO on accept/reject, first target Qwen3.5-4B for bank-line categorisation, later GRPO/RFT for the voucher agent with the deterministic ledger check as reward, champion/challenger ramps 5 → 25 → 100% by cohort.

Gate: edit-weighted accept rate improves month over month on the replay set for the pilot cohort; no rule fires without a confirming user id; the priors schema contains no free text and every row has ≥ 20 contributing tenants; adversarial review including a DPDP read of the priors pipeline.

### W8 — LLMOps: observability, eval gates, guardrails, red-team, compliance (4 wk, starts with W0, runs throughout)

Deliverables: Langfuse v3 self-hosted in ap-south-1 via the official Terraform module; Go OTel helper for `gen_ai.*`; DeepEval + promptfoo GitHub Action + Inspect simulations; the four-tier CI gate (Tier 0 no-LLM replay of 500 cassettes on every PR; Tier 1 on prompt/model/tool changes with exact-match/F1 within 0.5 pt, zero unapproved writes, zero injection bypasses; Tier 2 nightly pass^5 sims and pairwise insight judge; Tier 3 weekly/monthly red-team with DeepTeam/garak/PyRIT and correction replay); nightly 200-case canary against every production model id with fingerprint capture; Prompt Guard 2 (Hindi) on user text and an indirect-injection classifier on documents as review-level raisers; Granite Guardian 3.3 on write plans; Presidio `IN_*` + GLiNER + IFSC/UPI regex before traces and egress; exact-match semantic cache keyed on `(tenant, normalised NL, snapshot version)`; incident/rollback playbook (feature flags `ai.agent.write`, `ai.insights`; model pin rollback; blast-radius query over the intent log; compensating versions; 6-hour CERT-In and 72-hour DPB clocks); compliance pack (AI action register export, processing-location statement, model cards, DPIA template, NIST AI RMF mapping, ISO 42001 readiness plan).

Gate: a deliberately regressed prompt is blocked by Tier 1; a simulated provider weight change is caught by the canary within one night; the playbook is rehearsed once end-to-end on a seeded tenant.

### W9 — India-resident inference and scale economics (3 wk, before the 13 May 2027 DPDP enforcement date and before any bank/CA-firm channel deal)

Deliverables: Tier B/C, OCR, embeddings on Sarvam Inference or self-hosted vLLM (GLM-5.3-Flash, Qwen3.8-27B) on E2E/Yotta with XGrammar structured outputs; Fireworks retained for the Tier-A planner under zero-data-retention with PII minimisation and the tenant consent flag; a fully India-resident paid tier; dedicated deployment of a pinned checkpoint for the write-agent path once serverless spend sustains > $8/hour; per-tier token budgets tuned to the cost envelope (₹130–310 per typical tenant-month).

Gate: a tenant with residency mode on produces zero non-India egress in the trace store over a week; cost per tenant-month within envelope on the pilot cohort.

---

## 4. Phasing — what a tenant experiences at each step

| Phase | Weeks | Lanes | Tenant-visible outcome |
|---|---|---|---|
| **P0 Foundations** | 1–3 | W0, W1, W8 start | Nothing visible; NL query runs on GLM-5.3-Flash at ~₹0.02/question; model routing decided on our data, not leaderboards |
| **P1 Agent can act** | 4–9 | W2, W3, W5 in parallel | Alt+G: "sales invoice Acme 50000 +18% GST" → pre-filled F8 screen → Ctrl+A; approval inbox; every report and voucher class reachable by the agent; bills and bank statements extracted on open weights with exception-only review; the tenant card and entity profiles make answers specific ("Acme usually pays 42 days late") |
| **P2 Agent tells you things** | 10–14 | W4, W6 | Morning WhatsApp digest with ≤5 ranked, drillable insights and one-tap actions; voice-note voucher entry with readback; debtor reminders timed to pay-date predictions; GSTR-2B/IMS mismatch list with supplier nudges; Claude/ChatGPT can act on the books via our MCP server under the same gates |
| **P3 It gets better** | 15–18 | W7 | Corrections become exemplars and confirmed rules; per-tenant learning curve visible; L1 → L2 promotion for clean action types; anonymised peer benchmarks once cohorts reach 30 |
| **P4 Resident and economical** | 19–21 | W9 | India-resident inference default; residency tier; fine-tuned small models where volume justifies; published per-tenant accuracy and limits |

Total ≈ 21 weeks with three concurrent lanes; W8 runs throughout. Sequencing constraints: W2 before W6 (channels need the action layer); W3 before W4 (insights need features); W7 needs ≥ 6 weeks of W2/W4 signals; W9 before any regulated-partner deal.

---

## 5. ADR deltas versus PLAN.md

| PLAN.md AI ADR said | This plan says | Why |
|---|---|---|
| Default to Claude Opus 4.8 + Haiku 4.5 via the Anthropic API | Open weights in three tiers (GLM-5.3 planner; GLM-5.3-Flash / DeepSeek V4 Flash / Qwen3.8-27B mid; Qwen3.5-4B small; Sarvam-105B Indic) with Claude Sonnet 5 as a consent-gated fallback | Open frontier within ~9 index points at 5–20× lower cost; ₹110–310 vs ₹2,200 per tenant-month; DPDP/residency posture; fine-tunability (r2) |
| Manual Anthropic tool-use loop in the insight plane | Hand-rolled loop in **Go/cored** over an `ActionSpec` registry, durable via DBOS/Restate; insightd is a worker | Tool layer with tenancy/ACL/step-up/chain already lives in Go (r1 §4) |
| Copilot exposed through Alt+G and a side panel | Same, plus WhatsApp, voice, MCP server; all channels produce the same `ActionRequest` | India distribution is WhatsApp; MCP is table stakes (r4, r7) |
| Every state-changing tool only stages a proposal; Ctrl+A accepts | Kept, formalised as risk classes + autonomy levels L0–L3 per (tenant, user, action, amount band), agent principal ∩ delegation, intent log, inbox with edit-then-approve, undo as reversal | Copilot Studio / Ramp / Digits / BC patterns; OWASP ASI09 (r5) |
| NL → typed DSL → parameterised SQL, never raw SQL | Kept as Tier A; adds Tier B guarded SQL over RLS views and Tier C sandbox for the long tail | Semantic layers +17–23 pt and fail loudly; LiveSQLBench 48% shows why raw SQL stays fenced (r6, r9) |
| Self-hosted OCR + Presidio only for residency; Opus for extraction | PaddleOCR-VL-1.6 (beats Gemini-3 Pro on OmniDocBench) + GLM-5.3-Flash / Qwen3.8-27B with `json_schema`; image-native for photos | Small dedicated OCR models now dominate; open VLMs are document-strong (r2 §9) |
| Deterministic engines, LLM as explainer | Kept and expanded into the 52-type insight catalogue with ranking, caps, feedback | Industry-wide pattern; alert-fatigue evidence (r6) |
| Learning loop = per-tenant few-shot exemplars, no training | Exemplars + confirmed rules + procedural memory + bi-temporal facts in Postgres; **plus** a gated fine-tuning track (KTO/LoRA/RFT) and k-anonymous cross-tenant priors | Rules-first is what produces vendors' numbers; narrow verifiable tasks are where small fine-tunes beat frontier models (r3, r2 §6) |
| Anthropic prompt caching, Batch API, per-tenant budgets | Fireworks default-on prefix caching + Batch (50%), DeepInfra secondary, Sarvam India-hosted; budgets via reserve/settle with degraded modes | Provider facts (r2 §2, r8 §5) |
| Golden-set eval gates every prompt/model change | Four-tier CI gate, state-based agent evals (tau-bench principle), nightly canaries, shadow/champion-challenger, self-hosted Langfuse in Mumbai | No SaaS observability in India; providers change weights behind ids (r8) |

---

## 6. How we will know it is "getting better"

Report these per tenant and per cohort, monthly, and publish the tenant's own view in-product:

- **Suggestions**: accept rate; edit-weighted accept rate (field-level severity); auto-post rate at the confidence gate **and** audited error rate behind it (sampled CA review); time-to-accept; reversal-within-7-days.
- **Insights**: click-through; "acted within 7 days"; per-type precision from feedback; suppression rate; digest open rate.
- **Agent**: task success on the replay suite; pass^5; steps and ₹ per task class; schema-repair rate; malformed-call rate; approval latency.
- **Learning**: corrections per 100 vouchers by month; rules confirmed and their hit/miss; stale-fact rate; exemplar utility.
- **Trust**: injection-suite bypasses (must stay zero); unapproved-write invariant violations (must stay zero); intent-log completeness.

---

## 7. Decisions to take now

1. Approve the model bake-off (W1) as the first spend; it decides D3 with our data.
2. Choose DBOS Transact Go vs Restate after a one-week spike (three-day approval across a restart).
3. Confirm the residency posture for pilot tenants (Fireworks Tier-A egress with redaction and consent vs India-only from day one).
4. Pick the WhatsApp path for the pilot (BSP for Payments/Forms speed vs direct Cloud API).
5. Confirm with counsel the GST filing boundary (Rule 26 / Rule 83(8)) and the DPDP reading of the priors schema before W7.
6. Read the GLM-5.3 and Kimi K3 licence files before either enters production.

---

## 8. Risks and mitigations

| Risk | Mitigation |
|---|---|
| Open-model tool-calling fragility (text-in-content, hallucinated tool names, Hindi argument values) | Constrained decoding on every step; ≤ 8 tools per turn via search; id-not-name arguments with server canonicalisation; one bounded retry; W1 measures malformed-call rate before we commit |
| Fireworks weight changes / deprecations / no India region | Nightly canary + fingerprint capture; provider abstraction with DeepInfra and Sarvam; dedicated pinned deployment for the write path at volume; W9 residency tier |
| Alert fatigue kills the digest | ≤5 items, expected-value ranking, per-type caps, mandatory feedback, precision floor of 70% before a type may appear |
| Rubber-stamping under one-key accept | Mandatory diff above L1 thresholds; sampled re-review; demotion on reversal |
| CA distrust | Publish per-tenant auto-post rate, audited error rate, and hard limits; exportable AI action register; prepared-by/filed-by attribution |
| DPDP enforcement (May 2027) and CERT-In logs | Data plane and traces in ap-south-1 from day one; 180-day logs in India; 6 h / 72 h playbook rehearsed in W8 |
| Scope creep into "the agent closes your books" | The unit of autonomy is one bounded task with a deterministic check; APEX-Accounting and EnterpriseArena numbers are cited in the product copy's honesty |
| Licence exposure | Allow-list of Apache/MIT components; GPL/AGPL repos are studied, not linked; custom model licences read before use |
