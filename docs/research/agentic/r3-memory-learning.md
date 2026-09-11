# Research Topic 3 — Memory, personalization and continual learning for the agent

Date: 2026-09-08. Context: AI-native Tally competitor (Go + PostgreSQL 16 with tenant RLS, Python AI plane, open-weight LLMs on Fireworks). Goal: "an agent that keeps getting better as the user uses it, and as other users use it", under India DPDP.

---

## 1. Agent memory systems — 2026 state

### 1.1 What the systems are

| System | Architecture / memory model | Retrieval | Temporal validity | Storage backend | License | Headline benchmark claims (self-reported unless noted) |
|---|---|---|---|---|---|---|
| **Mem0** (paper arXiv 2504.19413) | Two-phase: LLM *extracts* salient facts from each turn, then *consolidates* (ADD/UPDATE/DELETE/NOOP vs existing memories). Optional **Mem0g** graph variant (entities + relations). | Vector similarity over memory strings; graph traversal in Mem0g | Facts overwritten/deleted on conflict; no bi-temporal model | Pluggable; **pgvector supported** (hnsw / diskann via pgvectorscale); graph store needs Neo4j/Memgraph/Kuzu/Neptune | Apache-2.0 | LoCoMo: +26% LLM-judge vs OpenAI memory, 91% lower p95 latency, >90% token saving vs full-context; Mem0g ~+2% over base. Marketing page claims 92.5% LoCoMo / 94.4% LongMemEval; independent reruns land at ~58–66% |
| **Letta / MemGPT** | Agent-editable **memory blocks** (label, value, size limit, description; read-only or agent-editable via tools like `rethink_memory`) in-context ("core"), plus archival (vector) and recall (conversation search). **Sleep-time compute** (arXiv 2504.13171): a second agent with exclusive memory-edit tools rewrites the primary agent's blocks during idle time ("learned context"), can run a stronger model than the primary. | Core memory is always in prompt; archival via embeddings | None built in; whatever the agent writes | Self-hosted server = **PostgreSQL + pgvector** (`CREATE EXTENSION vector`) | Apache-2.0 (server, learning-sdk) | Independent evals put Letta ~83% on LoCoMo (highest *verified* classic OSS result per third-party roundups) |
| **Zep / Graphiti** (arXiv 2501.13956) | Three-tier temporal KG: **episode subgraph** (raw messages/JSON, non-lossy) → **semantic entity subgraph** (deduped entities + relation edges) → **community subgraph** (label-propagation clusters with summaries). | Hybrid: cosine + BM25 + BFS n-hop, then rerank (RRF, MMR, mention-frequency, node-distance, cross-encoder) | **Bi-temporal**: `t_valid/t_invalid` (world time) and `created_at/expired_at` (ingest time); contradictions *invalidate* old edges rather than delete | Neo4j 5.26, FalkorDB, Amazon Neptune, Kuzu (deprecated). **No Postgres backend.** Zep Community Edition is no longer maintained; Graphiti stays Apache-2.0 | Apache-2.0 (Graphiti) | DMR 94.8% vs MemGPT 93.4%; LongMemEval (gpt-4o-mini) +15.2% overall / up to +18.5%, ~90% lower latency; by type: temporal +48%, single-session preference +78%, knowledge-update **−3.4%**, single-session-assistant −9.1%. LoCoMo score disputed (Zep 84%→75.1%; Mem0 rerun 58.4%) |
| **LangMem** (LangChain) | Semantic (facts/preferences), episodic (interaction summaries), **procedural** (agent rewrites its own instructions from feedback) over the LangGraph `BaseStore` | Namespaced store, vector search | None | `AsyncPostgresStore` (Postgres + pgvector) | MIT | Mem0 paper reports LangMem 62.2 J on single-hop vs Mem0 67.1 |
| **cognee** | ECL (extract–cognify–load) pipelines building graph-native memory; graph-aware RAG | Graph + vector hybrid | Partial (graph timestamps) | Pluggable vector/graph; Postgres+pgvector supported for vector; graph via Kuzu/Neo4j | Apache-2.0 | **BEAM** (10M-token tier): 0.67 vs Mem0 0.486; 0.79 at 100K |
| **MemoryOS** (arXiv 2506.06326, EMNLP'25 oral) | OS-inspired: short-term → mid-term (dialogue-chain FIFO) → long-term personal (segmented pages, heat-based promotion) | Layered lookup | Implicit via heat/decay | Own store | Apache-2.0 | LoCoMo w/ GPT-4o-mini: +49.1% F1, +46.2% BLEU-1 vs baselines |
| **A-MEM** (arXiv 2502.12110, NeurIPS'25) | Zettelkasten notes (context, keywords, tags) with LLM-generated links; new notes *evolve* attributes of old ones | Embedding + link traversal | None | Own store | MIT | Gains across 6 base models; Mem0 paper lists A-Mem single-hop J 39.8 |
| **Memobase** | **Profile-based** user memory: topic/subtopic slots (e.g. `work: title`) + event timeline; buffer → batch extraction | Read profile directly (<100 ms), no per-turn retrieval | Event timeline | **Postgres + Redis** | Apache-2.0 | Claims SOTA temporal on LoCoMo; v0.0.40 cut LLM tokens 40–50% |
| **Supermemory** | Proprietary `learner-1` extractor; vector-graph with temporal understanding; context fencing per tenant | Hybrid semantic + keyword, <300 ms | Temporal edges | Cloud (Cloudflare) | Apache-2.0 (repo) | Claims #1 on LongMemEval/LoCoMo/ConvoMem; third parties quote 81.6% LongMemEval |
| **Honcho** (Plastic Labs) | Purpose-built **user modeling**: storage layer (workspaces, peers, sessions, messages) + async "insights" layer that derives deductive/inductive conclusions into per-`(observer, observed)` representations; "dialectic" chat endpoint | Query the representation in natural language | Timeline of derived facts | **Postgres + pgvector** | **AGPL-3.0** (commercial licence needed to embed) | No public LoCoMo/LME |
| **OpenAI ChatGPT memory** | Two channels: explicit **saved memories** + implicit **chat-history insights**; relevant items injected into context as a hidden system note; user can toggle each channel | Server-side selection | Saved memories are editable/deletable | OpenAI servers | — | — |
| **Anthropic Claude memory** | Project-scoped memory (separate memory per project), an editable **memory summary** (July 2026 moved to individual categorised entries + monthly recap); incognito chats not saved; user can tell Claude to focus/ignore | Summary always in context | User-edited | Anthropic | — | — |

### 1.2 Benchmarks and why to distrust them

* **LoCoMo**: 10 conversations, ~600 turns / ~26k tokens each, ~200 QA per conversation across single-hop, multi-hop, temporal, open-domain. Fits in any 2026 context window, so it "tests the model, not the memory architecture" (cognee). Public dispute: Zep claimed 84% then 75.1%; Mem0 reran Zep at 58.4%.
* **LongMemEval** (ICLR'25): 500 questions, five abilities (information extraction, multi-session reasoning, knowledge updates, temporal reasoning, abstention); LME-S ≈115k tokens, LME-M ≈500 sessions (~1.5M tokens). Leaderboard (Mem0's own 2026 post): Mem0 94.4% (self), ByteRover 92.8% (self, LME-S), Zep 71.2% (GPT-4o judge) — "none of these numbers were generated using the same model stack, judge model, or retrieval configuration."
* **BEAM**: 128k/500k/1M/10M-token tiers, 10 abilities incl. contradiction resolution, event ordering, instruction following. Long-context models fall from ~0.25 to ~0.10–0.13 at 10M. Cognee 0.67 vs Mem0 0.486 at 10M — the only benchmark where the system genuinely *must* store outside the prompt.
* **MemDelta** (arXiv 2606.29914) is the key sobriety check: swapping only the embedding model shifts accuracy +6.2 pp (p=0.004) and can flip rankings; Claude Sonnet refuses 63% of full-context queries so "RAG wins" partly by avoiding refusals; on 2 of 6 question types Mem0 merely matches plain cloud RAG **at 50× the cost**; "agent self-memory" scored 42% vs 47% for basic retrieval. Recommendation: fix embedders, stratify by model, report write-path cost.
* LongMemEval-V2 (web-agent trajectories) and LoCoMo-Plus (cognitive, beyond-factual) appeared in 2026.

### 1.3 Which fit a per-tenant Postgres + pgvector deployment?

* **Native fit (no extra database)**: Letta server (Postgres+pgvector), LangMem/LangGraph store (`AsyncPostgresStore`), Memobase (Postgres+Redis), Honcho (Postgres+pgvector, but AGPL), Mem0 with pgvector as the vector store (graph variant needs Neo4j-class store).
* **Poor fit**: Graphiti/Zep (Neo4j/FalkorDB/Neptune only), cognee graph layer, Supermemory (cloud).
* Practical conclusion: none of these is a moat; all are ~2–5k lines of "LLM extract → dedupe/update → embed → retrieve → inject". The valuable ideas are (a) Letta's *agent-editable, size-capped memory blocks* that are always in context, (b) Letta's *sleep-time agent* doing consolidation off the hot path, (c) Graphiti's *bi-temporal edges with invalidation instead of deletion*, (d) Memobase's *fixed-schema profile slots* read with zero retrieval, and (e) Mem0's ADD/UPDATE/DELETE consolidation prompt. All five are trivially implementable in Postgres and should be, because our tenant RLS then applies to memory for free.

---

## 2. Business-entity knowledge graph ("business profile")

### 2.1 Evidence on graphs vs tables for LLM agents

* **Microsoft GraphRAG** cost: early-2024 indexing runs cost ~$33k per corpus; by mid-2025 Microsoft cut it ~1000× (one report: 2.97M input tokens ≈ $6.67 with DeepSeek-R1-70B). Global-search prompts still reach ~40k tokens; **LightRAG** ~100k tokens per query; **HippoRAG 2** ~1k tokens per query.
* **HippoRAG 2** ("From RAG to Memory", ICML'25): KG + Personalized PageRank + deeper passage integration; mean **+7 F1** over NV-Embed-v2 on associative tasks with no loss on factual QA; supports continual insertion of new facts via OpenIE — the best-evidenced "non-parametric continual learning" recipe.
* **GraphRAG-Bench** ("When to use graphs in RAG", arXiv 2506.05690): "GraphRAG frequently underperforms vanilla RAG on many real-world tasks"; graphs help for hierarchical knowledge, multi-hop and relationship-heavy reasoning, not for fact lookup.
* **Apache AGE vs Neo4j**: AGE is *slower* on deep variable-length traversals but *faster* on 1–2-hop lookups and CRUD — the patterns an accounting agent needs — and gives one database, one connection string, one backup, one RLS policy. AGE+pgvector "provides the same GraphRAG capabilities as Neo4j+Pinecone".
* **Digits** describes a "vector-graph data model, where every vendor, customer, invoice and account is an interconnected object", indexing the whole financial history for real-time search; "deep learning enables it to detect recurring transactions, even upon their first arrival".

### 2.2 Recommendation for us: the graph already exists — it is the ledger

Tally-style books are *already* a typed property graph: Party —bills→ Voucher —lines→ Ledger/StockItem, Ledger —under→ Group, Voucher —matched→ BankTxn. We should not re-extract it with an LLM. Build a **derived "business profile" layer** in Postgres:

1. **`entity_facts`** (per tenant, per entity, per fact_key): `value_json`, `confidence`, `evidence` (query + row ids), `computed_at`, `valid_from`, `valid_to`, `source` ∈ {deterministic_job, llm_inference, user_stated, user_corrected}. Bi-temporal columns copied from Graphiti; supersede rather than delete.
2. **`entity_edges`** for the small number of *non-ledger* relations the agent learns (Party ⇢ "is same as" bank counterparty string; Item ⇢ substitute Item; Employee ⇢ approves for Cost Centre). Store in plain tables; add AGE only if we find real multi-hop needs.
3. **Narrative profile per entity** (≤ 400 tokens, LLM-written nightly from the facts, in the style of a Letta memory block): "Customer X: avg 42 days to pay vs 30-day terms; 3 of last 6 bills paid late; buys mainly Item Y in Oct–Dec; GSTIN active; GSTR-2B mismatch twice in FY25-26." Retrieved by entity id whenever the entity appears in a turn — no embedding search needed.
4. A **tenant-level block** ("Company profile": FY, GST scheme, books style, ledger naming habits, top 20 parties/items, seasonality, owner's stated preferences) that is always in the system prompt (Letta core-memory pattern, capped ~1.5k tokens).

---

## 3. Learning from corrections without training

| Technique | Paper / product | What it showed | Our use |
|---|---|---|---|
| **Dynamic few-shot (retrieval-augmented ICL)** | Retrieval-style ICL for hierarchical text classification (TACL'24); RAG-based dynamic prompting for biomedical NER (+7.3 / +5.6 F1 in 5/10-shot); STAYKATE hybrid selection | Selecting demonstrations by similarity to the input beats static few-shot consistently, esp. for hierarchical label spaces (chart of accounts is one) | Every accepted/corrected voucher becomes an exemplar `(narration, party, amount, bank_ref) → (ledger, cost centre, GST treatment)` in a per-tenant pgvector table; top-k injected per suggestion |
| **Rule induction with human confirmation** | Ramp ("Rules cover most scenarios, AI fills the gaps by learning from corrections"); Brex ("ML models trained on millions of transactions proactively suggest coding rules and mappings"; >90% of AI suggestions accepted; >65% of expenses fully automated); QuickBooks bank rules | Vendors converge on *rules first, model second*; rules give deterministic, auditable behaviour | After N≥3 consistent corrections the LLM proposes an explicit rule ("Narration ILIKE '%swiggy%' → Staff Welfare, GST ITC blocked"); user confirms; rule is evaluated deterministically before any model call |
| **Preference / profile memory** | LangMem procedural memory (agent rewrites its own instructions from feedback); Memobase profile slots; ChatGPT/Claude editable memory | Small, user-visible, editable set of preferences works better than opaque embeddings | `tenant_preferences` block: rounding, narration style, default GST rates, which reports they open on Monday, tone |
| **Reflexion** (NeurIPS'23) | Verbal self-critique stored in an episodic buffer as a "semantic gradient" | +8% absolute over episodic-memory-only baseline; SOTA pass@1 on HumanEval at the time | For agent *task* failures (e.g. wrong GSTR-3B reconciliation approach) store a one-paragraph reflection keyed by task type |
| **Agent Workflow Memory** (ICML'25) | Induce reusable workflows from successful trajectories, inject selectively | +24.6% Mind2Web, +51.1% WebArena relative success; beats human-written workflows by 7.9% | Distill successful multi-step agent runs ("month-end GST close for a composition dealer") into named workflows |
| **ReasoningBank + MaTTS** (Google, ICLR'26) | Store *strategies* distilled from self-judged successes **and failures**; scale test-time rollouts for contrastive signal | up to +34.2% relative effectiveness, −16% steps on web/SWE benchmarks | Strategy memory for the analysis agent ("when DSO jumps, check for unapplied receipts first") |
| **Dynamic Cheatsheet** (Stanford, 2025) | Self-curated persistent notes of transferable snippets | GPT-4o Game-of-24 10%→99%; Claude 3.5 +27–30% AIME | Store validated SQL/DSL snippets for report questions per tenant schema quirks |
| **ReMe dynamic procedural memory** (arXiv 2512.10696) | Multi-faceted distillation, scenario-aware indexing, utility-based pruning | SOTA on BFCL-V3/AppWorld; 8B + memory beats 14B without | Pruning policy for our exemplar/rule stores |
| **Skill libraries** | Voyager (skill = executable code indexed by description embedding, top-5 retrieval, compositional); Anthropic Agent Skills (`SKILL.md` with name/trigger description/body, progressive disclosure); **Skill-to-LoRA** (arXiv 2606.16769: 21 skills → LoRA adapters on Qwen 3.6-27B, +2.9–5.2 pp, −6.6% tokens/step, matched/exceeded full skill text on 18/21) | Procedures should be versioned files, retrieved by trigger, and later compiled to adapters when stable | Tenant- and industry-level skills ("Textile trader: job-work challan handling") as `SKILL.md` documents; the Skill-to-LoRA path is our bridge to §4 |
| **Sleep-time compute** (Letta, arXiv 2504.13171) | Move memory reorganisation to idle time with a stronger model | Same quality at lower interactive latency/cost | Our nightly refresh *is* sleep-time compute |

---

## 4. Learning WITH training

**Signals to collect from HITL** (every suggestion the UI shows): `shown → {accepted, edited(field-level diff), rejected, ignored}`, plus time-to-decision, who decided (owner/accountant/junior), and whether a later reversal happened (a correction that itself gets corrected is a noisy label). Ramp explicitly lets users "leave feedback so Ramp learns your preferences"; Brex says accepting/rejecting "trains the system"; Intuit's KDD'21 paper describes "real-time learning that observes user actions to deliver timely category recommendations".

**Methods**
* **SFT on accepted traces** (Fireworks SFT V2 supports multi-turn function-calling data in vLLM format). Cheapest, first step.
* **Preference optimisation**: DPO needs pairs (shown-vs-edited gives them for free); **KTO** works from unpaired accept/reject signals (loss-aversion-aware), which matches our data shape best; **ORPO** folds the penalty into SFT. **DiaTool-DPO** (SIGDIAL'25) built pairs automatically from correct vs incorrect dialogue flows for tool-augmented LLMs: information-gathering 44%→94.8%, tool-call rejection 9.6%→91%.
* **RL on tool traces**: **OpenPipe ART** (Apache-2.0) runs GRPO with LoRA checkpoints hot-loaded into vLLM; **RULER** uses an LLM judge to *rank* a group of trajectories (0–1) with no labels/hand-written reward. **Fireworks RFT** (managed; DeepSeek V3, Kimi K2, etc.; evaluator is any Python function returning 0–1, incl. LLM-judge): Genspark +10% quality vs SOTA closed model, +33% tool calls, ~50% cost cut; Vercel 93% error-free generation. Fireworks' multi-turn RL guidance: trajectory-level reward must dominate (±1), step penalty ≈ −0.05, invalid-tool penalty ≈ −0.2, partial rewards cause myopia/reward-hacking, base model needs ≈20% zero-shot success first, select checkpoints on a held-out episodic benchmark ("the final checkpoint is often not the best"), keep environment prod-parity.
* **Online vs periodic**: Stripe Radar retrains monthly and reports +0.5 pp recall per refresh, ships only behind guardrail metrics (false-positive, block, authorisation rates, aggregate *and per merchant*) and uses counterfactual analysis for blocked items. Intuit fine-tunes custom "Financial LLMs" on anonymised/scrubbed bank data: 90% categorisation accuracy, −50% latency vs general LLMs; their GenOS Evaluation Service scores "decision paths", not just correctness.

**What backs the "95%+" claims**

| Vendor | Claim | What it actually measures |
|---|---|---|
| Digits | Bookkeeping Agent 97.8% vs 79.1% for outsourced accountants on 2,000 SMB transactions; "96% of transactions booked... not touched by a human afterwards"; ~95% auto-reconciled, 5% held | Untouched-after-posting rate, i.e. accept rate — not audited correctness. Trained on "$825B+ transactions" |
| Ramp | "up to 98% accuracy on ready-to-sync decisions"; "up to 70% fewer corrections" in first month | Accuracy *conditional on* the model's own confidence gate; correction-rate decline |
| Brex | >90% of AI suggestions accepted; >65% of expenses fully automated | Accept rate; automation coverage |
| Puzzle | "98% automated categorisation" (third-party); natural-language categorisation, vector similarity via Vertex AI, "no rules to maintain" | Coverage, not error rate |
| Intuit | 90% categorisation accuracy with custom LLMs | Model accuracy on held-out labels |

Pattern: every headline figure is either accept-rate or accuracy *behind a confidence threshold*. We should publish the same two numbers honestly and separately, plus a sampled audited-correctness rate.

---

## 5. Cross-tenant ("network") learning under DPDP

**What incumbents do**: Stripe Radar trains on "hundreds of billions in payments from millions of businesses" (70T+ data points; 90% of cards seen before) and uses *embeddings* of merchants/banks/countries so a pattern learned in Brazil transfers to the US. Intuit's Small Business Index uses anonymised data from 533k QuickBooks firms; its 2026 AI Impact Report used 5.3M businesses; QuickBooks Capital exposes "anonymized, aggregated data based on the top 10% of businesses like yours" via its Claude/ChatGPT connector. Digits runs a **three-tier** loop — company → firm-wide (a new vendor is checked against other clients' categorisations) → global model. Vic.ai: "global understanding of accounting concepts with local, entity-specific learning."

**DPDP constraints** (Act 2023; Rules notified 13 Nov 2025; phased: definitions/Board immediately, consent managers Nov 2026, all substantive obligations **14 May 2027**): consent must be free/specific/informed/unambiguous, no bundling; purpose limitation and Rule 8 erasure when purpose is exhausted; breach notice 72 h; penalties up to ₹250 cr per instance; Significant Data Fiduciaries must appoint an India-based DPO, run annual DPIAs/audits and **screen algorithms/ML for risk to Data Principals**. Two openings matter: (i) the Act covers *personal* data — properly anonymised/aggregate data about businesses is outside it, but most SMB books contain personal data (sole proprietors' names, PAN, employee salaries, customers' phone numbers); (ii) Rule 16 / s.17(2)(b) exempts processing for **research, archiving or statistical purposes** provided it is not used to take a decision specific to a Data Principal and follows the prescribed standards. A pooled prior that never targets an individual sits inside that exemption; a per-person recommendation does not.

**Compliant design ("learn shapes, not values")**
1. **Tier 0 – public knowledge**: HSN/SAC → GST rate, TDS sections, state codes, bank-statement format grammars. Shipped as data, no tenant data involved.
2. **Tier 1 – aggregate priors with k-anonymity + noise**: `normalised_counterparty_token → ledger_group` distributions, `HSN → typical ledger`, industry DSO/DPO/margin quantiles, seasonal indices. Release a row only if ≥ k distinct tenants (k = 20 is our default; never < 5) contributed, add Laplace noise to counts, drop free-text and any token that is a name/PAN/GSTIN/phone; tokens are hashed merchant strings, not narrations. Refresh monthly; store provenance so a tenant's contribution can be removed on deletion (Rule 8).
3. **Tier 2 – model weights**: fine-tune only on (a) synthetic vouchers generated from Tier-1 shapes, (b) tenant data where the customer has *opted in* to "improve the service" with a separate, unbundled consent, scrubbed of personal identifiers (Intuit's "anonymized and scrubbed" recipe). Federated learning / DP-SGD (DPFedBank, Fed-RD) is available but over-engineered for our stage; document it as a Phase-3 option.
4. **Never cross the boundary**: raw narrations, party names, amounts, exemplars, rules, profiles. Exemplar retrieval is tenant-scoped by RLS; the cross-tenant prior is a *separate* read-only schema owned by a service role.
5. **Governance**: DPIA per tier, algorithm-risk screening write-up (SDF-ready), per-tenant toggle "contribute anonymised statistics", published k and ε.

---

## 6. Cold start from the Tally import

Day-1 intelligence comes from *mining years of vouchers*, not from conversation. Deterministic jobs on import:

* **Ledger-mapping habits**: for each (party, narration-token-set) → ledger/cost-centre pair, frequency and last-used; this seeds exemplars *and* candidate rules without any user having corrected anything. Intuit's KDD'21 paper segments the model "by customer lifecycle stage" precisely because new vs mature books need different priors; **TopoLedgerBERT** (Siamese BERT with chart-of-accounts hierarchy) shows tenant ledgers can be mapped to a standard CoA — use it (or an embedding equivalent) to align odd Tally ledger names to our canonical groups.
* **Recurring transactions**: unsupervised filtering (Ibrain et al., ICAIF'24: disentangle overlapping patterns, remove non-recurrent points, then forecast next-date/next-amount); in practice normalise description → group by counterparty → test interval regularity with tolerance (±3 days, ±5%/₹5 per patent literature) or a Lomb-Scargle periodogram for irregular spacing. Finexer's point stands: it is "a data quality problem before it is an algorithm problem" — counterparty normalisation (UPI VPA, IMPS ref, cheque payee) is the hard part for Indian bank statements.
* **Party behaviour**: per party — payment lag distribution vs credit terms, bounce/reversal count, GSTR-2B mismatch count, seasonality of purchases, share of revenue, concentration risk.
* **Item behaviour**: ABC (value) × XYZ (demand variability), seasonality index by month, reorder lead time from PO→GRN gaps, margin drift.
* **Books style**: voucher-type usage, narration templates, rounding conventions, FY practices — becomes the tenant profile block.
* **Process mining**: sequence-mine voucher chains (Order→Challan→Invoice→Receipt) to learn each business's actual workflow (AWM-style, but from data).

Vic.ai's framing is the target: "accurate from day one" by combining a global prior with the company's own history, then "quickly learns from user feedback".

---

## 7. Cheap continuous data understanding

Nightly (and on-import) **profile refresh** = our sleep-time compute. Precompute deterministically everything that is a *number*; let the LLM only *narrate* and *hypothesise*.

| Entity | Deterministic features (SQL/Go job) | LLM job (nightly, batched, small model) |
|---|---|---|
| Customer | RFM, DSO, on-time %, avg delay, dispute count, credit exposure vs limit, trend deltas | 3-sentence narrative + up to 3 "watch" flags |
| Supplier | DPO, GSTR-2B match rate, price drift per item, delivery lag | Same |
| Item | ABC/XYZ class, seasonality, days-of-stock, dead-stock flag, margin trend | Same |
| Ledger | Monthly run-rate, variance vs 12-mo mean, anomaly z-scores (Zoho Zia does exactly this "spot unusual patterns… how far they deviate") | Explanation candidates |
| Bank account | Recurring debits/credits, uncategorised backlog, low-balance risk dates | — |
| Company | Cash runway, GST liability forecast, top movers | Weekly digest |

Store in `entity_facts` (§2) with `computed_at` and evidence ids; the agent reads facts, never recomputes them in-prompt. Digits/Zoho describe this same layering (Digits: "continuously index… entire financial history"; Zoho Zia Invoice Agent: "payment delay patterns, late payment trends"). Cost: one nightly LLM pass over changed entities only (~200–400 tokens each on an 8B–30B open model) is a few rupees per tenant per night on Fireworks.

---

## 8. Measuring "getting better"

* **Suggestion-level**: accept rate; edit rate with *field-level* edit distance (which field, how far — a ledger swap within the same group is minor, a P&L↔BS swap is severe); time-to-accept; auto-post rate at the confidence gate and the audited error rate behind it (report both, per Ramp/Digits critique).
* **Insight-level**: click-through, "useful/not useful", action taken within 7 days, false-positive rate on anomaly flags, repeat-flag suppression rate.
* **Agent-level** (Confident AI taxonomy): tool correctness, argument correctness, step efficiency, task completion; trace every span; "turn every production failure into a regression test — your dataset should grow every time the agent embarrasses you."
* **Offline replay**: every correction is a labelled example → a per-tenant and global regression suite; replay historical corrections against each new prompt/model/adapter before rollout; gate on no-regression per tenant cohort (Stripe's per-merchant guardrail metric).
* **A/B on prompts/adapters**: interleave arms per suggestion within tenant; primary metric edit-weighted accept rate; secondary latency and tokens.
* **Memory-specific**: "acknowledge rate" (does the answer reflect stored preferences), stale-fact rate (facts contradicted by later data — Graphiti-style invalidation makes this countable), memory-write cost per turn (MemDelta's plea).
* Publish per-tenant "learning curve" dashboards (corrections per 100 vouchers by month) — it is the product promise made measurable.

---

## 9. Recommendations for our build

**Memory tiers (all in Postgres, tenant-RLS, no new database)**

| Tier | Table(s) | Written by | Read how | Retention |
|---|---|---|---|---|
| M0 Company block | `tenant_profile` (≤1.5k tokens text + JSON prefs) | nightly job + user edits | always in system prompt | supersede, keep history |
| M1 Entity facts | `entity_facts`, `entity_edges` (bi-temporal, evidence ids) | nightly deterministic + LLM narrative | by entity id when entity is in the turn | valid_to on supersede |
| M2 Exemplars | `exemplars` (pgvector, features + label + outcome + who/when) | every accept/edit | top-k by similarity per suggestion | prune by utility (ReMe) |
| M3 Rules | `rules` (predicate DSL, confirmed_by, hit/miss counts) | LLM proposes, human confirms | evaluated before the model | disable on 3 misses |
| M4 Procedural | `skills` (SKILL.md text, trigger, version), `strategies` (ReasoningBank-style) | agent reflection, ops | trigger match | versioned |
| M5 Episodic | conversation summaries + Reflexion notes | end of session | semantic search | 90 days then summarise |
| X Cross-tenant priors | separate schema `network_priors` (k≥20, noisy counts) | monthly aggregate job | read-only service role | rebuild monthly |

**Correction flow**: suggestion shown → decision logged (`suggestion_events`) → (a) exemplar upsert immediately, (b) rule proposal when ≥3 consistent corrections for the same predicate, (c) nightly: profile narrative refresh, exemplar pruning, reflection distillation, (d) weekly: regression replay of all corrections against current prompts, (e) monthly: KTO/SFT LoRA on opted-in scrubbed traces via Fireworks (or ART/GRPO with RULER-style judge for the multi-step agent), gated on held-out replay per cohort; canary 5% of tenants; roll back on any per-cohort regression.

**Schedule**: per turn — M0 + M1(entities in turn) + M2 top-k + M3 hits (~3k tokens total). Nightly 02:00 IST — deterministic features, LLM narratives for changed entities, recurring-pattern rescan on new bank lines. Weekly — regression replay, A/B readout. Monthly — network prior rebuild with k-anonymity + noise, optional adapter training, DPIA delta.

**Do / don't**: don't adopt Graphiti/Neo4j (no Postgres backend, deep traversals we don't need); don't fine-tune before exemplars+rules plateau (they deliver most of Ramp/Brex-style gains and are auditable); don't publish LoCoMo-style numbers — publish accept-rate and audited error rate; do build Letta-style editable blocks visible to the user (ChatGPT/Claude precedent shows users expect to see and edit memory); do treat the nightly job as the moat — the "unique detailed insight" comes from precomputed facts the LLM narrates, not from the LLM discovering them in-context.

---

## Sources

1. https://arxiv.org/abs/2504.19413 — Mem0 paper
2. https://github.com/mem0ai/mem0 — Mem0 repo (Apache-2.0)
3. https://docs.mem0.ai/components/vectordbs/dbs/pgvector — Mem0 pgvector config
4. https://mem0.ai/blog/ai-memory-benchmarks-in-2026 — Mem0's 2026 leaderboard post
5. https://www.letta.com/blog/sleep-time-compute/ — Letta sleep-time compute
6. https://www.letta.com/blog/memory-blocks/ — Letta memory blocks
7. https://docs.letta.com/guides/selfhosting — Letta self-hosting (Postgres+pgvector)
8. https://github.com/letta-ai/letta — Letta (Apache-2.0)
9. https://github.com/letta-ai/learning-sdk — Letta learning SDK
10. https://arxiv.org/abs/2501.13956 and https://arxiv.org/html/2501.13956v1 — Zep/Graphiti paper
11. https://github.com/getzep/graphiti — Graphiti (backends, Apache-2.0)
12. https://blog.getzep.com/announcing-a-new-direction-for-zeps-open-source-strategy/ — Zep CE discontinued
13. https://github.com/langchain-ai/langmem and https://raw.githubusercontent.com/langchain-ai/langmem/main/LICENSE — LangMem (MIT)
14. https://docs.langchain.com/oss/python/langchain/long-term-memory — LangGraph store / memory types
15. https://github.com/topoteretes/cognee — cognee
16. https://www.cognee.ai/ai-memory-benchmarks and https://www.cognee.ai/cognee-vs-mem0 — BEAM results
17. https://arxiv.org/abs/2506.06326 — MemoryOS
18. https://arxiv.org/abs/2502.12110 — A-MEM
19. https://github.com/memodb-io/memobase — Memobase
20. https://supermemory.ai/research/ and https://github.com/supermemoryai/supermemory — Supermemory
21. https://github.com/plastic-labs/honcho — Honcho (AGPL-3.0, Postgres+pgvector)
22. https://help.openai.com/en/articles/8590148-memory-faq — ChatGPT memory
23. https://embracethered.com/blog/posts/2025/chatgpt-how-does-chat-history-memory-preferences-work/ — ChatGPT memory internals
24. https://claude.com/blog/memory — Claude memory
25. https://github.com/xiaowu0162/longmemeval — LongMemEval
26. https://arxiv.org/abs/2606.29914 — MemDelta
27. https://arxiv.org/abs/2502.14802 — HippoRAG 2
28. https://arxiv.org/abs/2506.05690 — GraphRAG-Bench "When to use graphs in RAG"
29. https://medium.com/graph-praxis/the-graphrag-cost-cliff-how-33-000-became-33-in-eighteen-months-be1b0fbe37e4 — GraphRAG cost
30. https://www.puppygraph.com/learn/apache-age-vs-neo4j — AGE vs Neo4j
31. https://thierno.dev/blog/apache-age-vs-neo4j/ — AGE vs Neo4j (RAG patterns)
32. https://arxiv.org/abs/2409.07429 — Agent Workflow Memory
33. https://arxiv.org/abs/2509.25140 — ReasoningBank
34. https://arxiv.org/abs/2504.07952 — Dynamic Cheatsheet
35. https://arxiv.org/abs/2303.11366 — Reflexion
36. https://arxiv.org/abs/2512.10696 — ReMe dynamic procedural memory
37. https://arxiv.org/abs/2305.16291 — Voyager
38. https://github.com/agentskills/agentskills — Agent Skills spec
39. https://arxiv.org/abs/2606.16769 — Skill-to-LoRA
40. https://direct.mit.edu/tacl/article/doi/10.1162/tacl_a_00697/124630 — Retrieval-style ICL for hierarchical classification
41. https://www.ncbi.nlm.nih.gov/pmc/articles/PMC12408026/ — RAG-based dynamic few-shot prompting (+7.3 F1)
42. https://arxiv.org/abs/2504.02882 — DiaTool-DPO
43. https://github.com/OpenPipe/ART — OpenPipe ART / RULER
44. https://fireworks.ai/blog/fireworks-rft — Fireworks RFT
45. https://fireworks.ai/blog/best-practices-for-multi-turn-RL — Fireworks multi-turn RL
46. https://fireworks.ai/blog/supervised-finetuning-v2 — Fireworks SFT V2 (multi-turn function calling)
47. https://api.semanticscholar.org/graph/v1/paper/DOI:10.1145/3447548.3467100 — Intuit KDD'21 categorisation paper
48. https://venturebeat.com/ai/how-intuit-built-custom-financial-llms-that-cut-latency-50-while-boosting — Intuit Financial LLMs / GenOS
49. https://ramp.com/accounting-automation-software — Ramp claims
50. https://www.brex.com/journal/accelerate-accounting-transaction-to-close — Brex claims
51. https://finance.yahoo.com/news/digits-launches-first-ai-agents-140000473.html — Digits agents press release
52. https://earmarkcpe.com/four-years-of-cleanup-in-four-hours-inside-the-ai-ledger-that-learns-from-your-work/ — Digits three-tier learning
53. https://puzzle.io/blog/puzzle-accounting-ai — Puzzle
54. https://www.vic.ai/blog/how-ai-learns-from-your-general-ledger-and-historical-data — Vic.ai global+local learning
55. https://www.zoho.com/us/books/help/ai-features/ai-features.html — Zoho Books Zia features
56. https://stripe.com/guides/primer-on-machine-learning-for-fraud-protection — Stripe Radar ML guide
57. https://quickbooks.intuit.com/r/small-business-data/index/methodology/ — Intuit SB Index methodology (533k anonymised firms)
58. https://www.intuit.com/blog/news-social/quickbooks-expands-into-claude-and-chatgpt-with-new-sales-invoicing-payroll-and-lending-features/ — QuickBooks aggregated peer benchmarks in Claude/ChatGPT
59. https://static.pib.gov.in/WriteReadData/specificdocs/documents/2025/nov/doc20251117695301.pdf — DPDP Rules 2025 notified (PIB)
60. https://www.dpdpa.com/dpdparules.html — DPDP Rules index (Rule 8, 13, 16)
61. https://www.getastra.com/blog/compliance/dpdp-compliance/ — DPDP compliance summary (penalties, SDF, timelines)
62. https://www.scrut.io/post/dpdp-rules — DPDP Rules practical guide
63. https://arxiv.org/pdf/2410.13753 — DPFedBank (federated + DP for banks)
64. https://arxiv.org/pdf/2408.01609 — Fed-RD federated financial crime detection
65. https://api.semanticscholar.org/graph/v1/paper/DOI:10.1145/3677052.3698596 — "Unveiling Recurring Financial Patterns" (ICAIF'24)
66. https://blog.finexer.com/recurring-transaction-detection-bank-data-apis/ — recurring detection as a data-quality problem
67. https://image-ppubs.uspto.gov/dirsearch-public/print/downloadPdf/12243033 — recurring-transaction tolerance patent
68. https://arxiv.org/abs/2407.05175 — TopoLedgerBERT
69. https://www.confident-ai.com/blog/llm-agent-evaluation-complete-guide — agent eval metrics / regression suites
70. https://theaiengineer.substack.com/p/cognee-vs-zep-vs-mem0-vs-letta — LoCoMo dispute summary
