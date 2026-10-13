# Agentic Platform Research — an agent that can do everything a human can, and knows the business better than they do

*Compiled 2026-09-08 from nine parallel web-research streams (≈600 sources fetched or seen; full per-topic reports with source lists in `docs/research/agentic/r1…r9`). This document is the synthesis; the r-files are the evidence. Every quantitative claim below carries a pointer to the r-file that sourced it. Items the researchers could not verify are marked **[unverified]** in the r-files and are not relied on here.*

---

## 0. What this document is for

The founder's brief: **every action a human can take in the product must also be takeable by the agent**; the agent must **understand each tenant's own data** (ledgers, parties, stock, bank lines, GST filings, payroll, years of Tally history) well enough to produce **unique, detailed, decision-changing insights** — from a day's reminders to "push item X to these twelve customers"; the system must **keep getting better** as one user uses it and as many users use it; it must run on **cheap open-weight models via providers like Fireworks**; and it must do all this inside accounting software where a wrong number is someone's tax notice.

This dossier answers five questions:

1. What has the market already converged on, and where is the real gap? (§2)
2. What exactly does our codebase have today that the agent layer can stand on? (§3)
3. What architecture do the evidence and the constraints point to? (§4, ten decisions)
4. What does "unique insights" concretely mean — the catalogue? (§5)
5. What does "keeps getting better" concretely mean, under India's DPDP Act? (§6)

Then costs (§7), the honest limits (§8), and what to verify next (§9). The companion `PLAN-AGENTIC.md` turns the decisions into workstreams.

---

## 1. Executive summary — the findings that change the plan

1. **The industry pattern has converged, and it is not "a chat window".** Every serious product (Digits, Ramp, Puzzle, Xero JAX, Intuit agents, Dynamics 365 Payables Agent, Sage, SAP Joule, NetSuite Next) does: confidence-gated auto-posting above a threshold → a "needs review" queue below it → nothing irreversible without approval → corrections feed a per-tenant learning loop → continuous close instead of month-end → an MCP tool surface so external agents can drive the books. *(r4 §1.1)* Tally has shipped rule-based GST intelligence (7.0, Jan 2026) and a purchase-invoice-only OCR ("Docs by Ira", 7.1, Jul 2026) and its MD is on record that "AI is probabilistic… prediction degrades", so it will not lead here. *(r4 §2.1)*

2. **"Everything a human can do" is achievable only through a typed action layer, not through a pixel/computer-use agent.** Computer-use agents pass the human baseline on short desktop tasks (OSWorld-Verified 86%) but reach only 60–65% on write-heavy, login-gated web tasks, ~20% on long-horizon work, take 2.7–4.3× more steps, and cost 1,000–1,800 tokens per screenshot. *(r5 §1)* Our codebase already has the seed of the action layer: the `routeAction()` PDP mapping and the closed `authz` vocabulary give roughly sixty `(action, resource)` pairs and ~40 report names. *(§3 below)*

3. **Put the agent runtime in Go, in `cored`, not in the Python plane.** The expensive, correctness-critical part of an ERP agent is the tool layer (tenancy, ACL, step-up, hash chain, outbox), and all of it already lives in Go. The LLM loop is ~300 lines. Use DBOS Transact (Go, Postgres-only, day-long `Recv` for approvals) or Restate for durability; do not adopt LangGraph as the runtime — its node-re-execution-on-resume semantics duplicate writes placed before an interrupt. Borrow three schemas: Agent Inbox's `HumanInterrupt` (accept / edit / respond / ignore), Pydantic AI's "validate arguments before the approver sees them", Deep Agents' todo/sub-agent harness. *(r1 §4, §8)*

4. **The brief's model list is a generation stale; the open frontier is now within ~9 points of Claude Fable 5.1 / GPT-6 and costs 5–20× less.** As of 8 Sep 2026: GLM-5.3 and GLM-5.3-Flash (MIT), DeepSeek V4 Pro / Flash (MIT), Kimi K3, Qwen3.8 (27B dense, Apache-2.0, multimodal, document-strong), Gemma 4 (Apache-2.0), Granite 4.2, and **Sarvam-105B** (Apache-2.0, trained from scratch on 22 Indian languages including romanised Hinglish). Fireworks headline prices: GLM-5.3 $1.40 in / $0.26 cached / $4.40 out per 1M; GLM-5.3-Flash $0.15 / $0.03 / $0.50; DeepSeek V4 Pro $1.32 / $0.044 / $3.96. A routed open stack costs **≈ ₹110–190 per tenant-month** versus ≈ ₹2,200 on Claude Fable 5.1 everywhere. *(r2 §1, §2, §4)*

5. **Open models fail on format, not on task — so constrain every decision step.** A May-2026 study finds 7B-class models reach ~85% task accuracy with 0% usable JSON under naive prompting; Fireworks' `json_schema` mode implies `unevaluatedProperties:false` (no hallucinated fields) and has BNF grammar mode for enum decisions. JSON tool-calling is specifically weaker for open models than code-style actions ("Bitter Lesson of Tool Calling", Aug 2026: programmatic tool calling wins on 11/14 models). The dominant multilingual failure (tested on Hindi) is the model writing argument *values* in the user's language — so tool arguments must be English ids, canonicalised server-side. *(r1 §7, r2 §3)*

6. **The data layer that makes an agent "understand the books" is Postgres-first and three-tiered.** Tier A: the existing closed DSL, extended into a semantic catalogue with Hinglish synonyms and per-report agent metadata (semantic layers raise NL accuracy +17–23 pp in paired benchmarks and, critically, *fail loudly instead of silently*). Tier B: AST-validated, read-only SQL over published RLS views under a restricted role with a 5 s timeout. Tier C: a DuckDB/Polars sandbox over a nightly per-tenant Parquet extract. pgvector 0.8's iterative scans solve filtered-HNSW recall for tenant-scoped search; Splink handles party de-duplication and bank↔ledger linkage. The ledger *is* the knowledge graph — do not add Neo4j. *(r9 §1, §3, §4; r6 §5.2)*

7. **"Unique insights" = deterministic facts, LLM words.** Tableau Pulse, ThoughtSpot SpotIQ, Zoho Zia and Power BI Copilot all compute insights with fixed statistical models and let the LLM only narrate. The evidence base per insight class is strong and cheap: late-payment prediction is LightGBM with AUC > 0.85 on ~40k B2B orders; intermittent SKU demand is Croston/TSB/ADIDA with Chronos-2 / TimesFM-2.5 (Apache-2.0) as the ceiling; cross-sell is implicit ALS plus FP-growth rules on invoice lines; duplicates are Splink; integrity is PyOD + Benford screening. A 52-type catalogue is in §5. The failure mode is alert fatigue (fraud monitoring still runs ~93% false positives), so digests are capped at 5–7 items, ranked by impact × P(real) × actionability × novelty × affinity, with mandatory feedback. *(r6 §0–§8)*

8. **"Keeps getting better" is mostly not fine-tuning.** The gains vendors cite (Ramp >90% suggestion acceptance, Brex >65% fully automated) come from *rules first, model second* plus per-tenant exemplar retrieval. Dynamic few-shot retrieval adds +5–7 F1; Agent Workflow Memory +51% on WebArena; ReasoningBank +34%. Memory libraries (Mem0, Letta, Graphiti, LangMem) are 2–5k lines of "extract → dedupe → embed → inject" and their benchmarks are disputed (Mem0 matches plain RAG at 50× cost on some question types). Implement the five good ideas directly in Postgres under RLS: Letta's editable size-capped blocks, sleep-time consolidation, Graphiti's bi-temporal facts with invalidation, Memobase's profile slots, Mem0's ADD/UPDATE/DELETE consolidation. Fine-tune only when exemplars + rules plateau; KTO fits unpaired accept/reject signals; a Qwen 3.5 9B + GRPO run for ~$500 went 64% → 97% on a narrow verifiable task, beating GPT-5.6 and Opus 4.8 — and voucher validity *is* a narrow verifiable task. *(r3 §1, §3, §4; r2 §6)*

9. **Cross-tenant learning must "learn shapes, not values".** DPDP Rules were notified 13 Nov 2025 with full obligations by 14 May 2027; Significant Data Fiduciaries must screen ML for risk; research/statistical processing that never targets an individual is exempt. Design: public knowledge (HSN → rate) → k-anonymous (k ≥ 20) noisy aggregate priors in a separate read-only schema → opt-in scrubbed fine-tuning. Never share narrations, names, amounts, exemplars or rules across tenants. *(r3 §5)*

10. **Independent evaluations are humbling, and that dictates the unit of autonomy.** APEX-Accounting (Jul 2026): the best model scores 56.4% mean criteria and no model exceeds 21.5% Pass@8 on 160 expert accounting tasks. EnterpriseArena: only 15.4% of agent-as-CFO trials survive. FinVerBench: 9 of 14 models give 95–100% false positives on clean statements. Adding an LLM investigator to a fraud classifier *lowered* accuracy (71.7% → 65%). Therefore: the unit of autonomy is a single bounded task followed by a deterministic check (debits = credits, GSTIN checksum, 2B match); arithmetic is executed, never generated; LLMs explain anomalies, they do not decide them. *(r4 §4)*

11. **Data residency is a design constraint, not a footnote.** Fireworks has no India region (nearest Tokyo, Malaysia, Sydney; region-pinning +50%). No LLM observability SaaS has an India region either. Sarvam Inference is the only India-hosted serverless endpoint carrying GLM-5.3 / 5.3-Flash / DeepSeek V4 Flash / Gemma 4 (beta). Stance: Tier B/C models, OCR, embeddings and traces stay in India (Sarvam, self-host on E2E/Yotta, Langfuse self-hosted in ap-south-1); only the PII-minimised Tier-A planner traffic goes to Fireworks under zero-data-retention. *(r2 §2, §10; r8 §1, §5)*

12. **WhatsApp is both the distribution channel and the largest untrusted-input surface.** Every Indian mass-market player ships WhatsApp as the primary output (reminders with UPI links are the single most-cited feature); Vyapar TaxOne collects invoices and bank statements *from* WhatsApp. That makes prompt-injection defence architectural: quarantined extractor → strict schema → deterministic validation → provenance-labelled arguments → policy that forbids an untrusted party/amount from reaching money movement without approval (CaMeL, Meta's Rule of Two). Guard models are noisy signals that raise the review level, never the gate. *(r4 §2.3; r5 §5, §7)*

---

## 2. The market in September 2026

### 2.1 What the leaders' agents actually do *(r4 §1)*

| Product | Autonomous | Human-in-the-loop | Claimed metric | Price |
|---|---|---|---|---|
| Digits (Autonomous General Ledger; agents Jun 2025; Agentic Close Jun 2026) | Continuous categorisation, matching, recurring-transaction detection on first arrival, accrual drafting, variance analysis | Exceptions queue | 97.8% vs 79.1% for outsourced accountants on 2,000 transactions; 0.04 s/transaction | $65 / $100 / $250 per month; API + MCP on all plans |
| Ramp Accounting Agent (Feb 2026) | Codes 100% of spend incl. custom fields; auto-approves and syncs low-risk items; month-end accruals with reversals | Flags exceptions with suggested action | 90%+ overall; 98% on "ready to sync"; autonomy slider | Included in Ramp Plus |
| Puzzle (AI Close GA Jul 2026) | Month-end automation, chat-in-ledger, up to 98% auto-categorisation | "Nothing posts without an accountant's approval" | Close 15–20 days → 3–5 | $25 – $300 |
| Xero JAX (2025–26) | Real-time bank reconciliation of high-confidence matches, payment-date prediction with tailored follow-ups over SMS/WhatsApp/email, Bill Protection (altered bank details, unfamiliar suppliers), cash-flow actions | "You will always have the final say"; JAX Assure hallucination filter | 73% of customers tried AI | Included; XeroForce agent builder alpha |
| Intuit QuickBooks agents (all QBO users, Aug–Sep 2025) | Accounting (categorise, reconcile, anomaly), Payments (late-payer prediction, drafts reminders), Finance (KPI vs budget), Customer, Sales-tax, Project, Payroll | "You stay in control" | Paid ~5 days faster; 45% save 12 h/month (Intuit surveys) | Finance Agent needs Advanced (~$275/mo) |
| Dynamics 365 BC (Update 28.0, May 2026) | Payables Agent drafts purchase invoices from a vendor mailbox by *walking its own UI via metadata*; Sales Order Agent; Agent Designer | Supervisor gate + Review Bar; new vendors born `Blocked` | Honest limits: ≤ 500 invoices/day, PDF only, no PO matching, no anomaly detection, English only | Copilot credits |
| Sage Intacct (2026 R3) | AP agent with vendor-pattern anomaly detection; Financial Assurance agent | Surfaces exceptions | — | Read-only MCP server |
| SAP Joule (Q1 2026) | Dispute-resolution agent, cash-management agent, self-learning payment-advice extraction | Proposes; user confirms | 70% time reduction (SAP) | Enterprise |

Pricing has settled at $25–100/mo for SMB self-serve and $35–65 per client per month for accounting firms; in India that maps to ₹500–2,000/mo, where Zoho Books already sits at ₹749–7,999. **The agentic layer is a plan-tier differentiator, not an add-on.** *(r4 §1.3)*

### 2.2 India *(r4 §2)*

- **Tally**: 2.7M MSME customers, FY25 revenue ₹710 Cr, TallyPrime = 99% of revenue. 7.0 (Jan 2026): GST rate validation, GSTIN verification, GSTR-1/3B error detection, pattern-based ledger suggestion, smart bank reconciliation. 7.1 (Jul 2026): "Docs by Ira" purchase-invoice extraction, GSTN IMS inside Tally, ICICI deep integration (NEFT/RTGS/IMPS from payment voucher, daily statement fetch). **Not shipped**: conversational query, autonomous categorisation, anomaly agents, WhatsApp/voice entry, cloud-native multi-tenant.
- **Zoho Books India** (₹749–7,999/mo): NL questions, anomaly spotting, AI collections, Zia Agent Studio, GSP filing, GSTR-2B recon. Nearest cloud incumbent; Zoho–NVIDIA partnership for India-hosted models.
- **ClearTax Max ITC**: AI + fuzzy PAN-level matching, background auto-recon, vendor chasing by email and WhatsApp, vendor-risk gating of payments; claims ₹9,000 Cr ITC unblocked, "2–7% additional ITC", "95% of Indian businesses under-claim ITC". **This is the reference bar for GST reconciliation UX.**
- **Suvit → Vyapar TaxOne**: Excel/PDF → Tally entry, OCR, GSTR-1/2B/3B recon, real-time TallyPrime integration — Vyapar moving up into CA workflows *on top of Tally*.
- **Razorpay Agent Studio** (Mar 2026, built on the Claude Agent SDK): Bookkeeping agent posting to ERPs by rules, Reporting agent with anomaly flags, 3–7-day cash-position prediction with daily digests. Payments players are entering the ledger from the cash side.
- **Entries.ai**: AI-native 9-module ERP for Indian SMEs (500+ businesses) — the closest Indian analogue to what we are building.
- **Tally add-ons** (Tara, AI Accountant, VouchrIt): bill upload → extract → GSTIN mismatch flags → ledger mapping → push vouchers; AI Accountant publishes a Hinglish voice-entry design ("baarah hazaar", "paanch lakh", readback verification, 95%+ amount-accuracy target) — demand for voice + WhatsApp-first capture is confirmed.
- **What Indian SMB owners respond to**: WhatsApp as the surface (reminders with UPI links), vernacular and Hinglish copy (OkCredit 11 languages), *cash collection over bookkeeping* (OkCredit "3× faster collections", Razorpay cash forecast), and the CA as approver of record.

### 2.3 What "unique insights" looks like when shipped *(r4 §5)*

Payment-date prediction per customer with tailored follow-ups (Xero); 3–7-day cash position with a daily digest (Razorpay); bill fraud (unusual amounts, altered bank details, unfamiliar suppliers); variance/flux narration with evidence (Numeric drafts 80% of first drafts); vendor price benchmarking from pooled transactions (Ramp, 16% savings claim); peer benchmarking (QuickBooks `benchmarking_against_industry`, Xero Small Business Insights); tax-saving nudges (Kick missed deductions; ClearTax "2–7% additional ITC" is the Indian equivalent); ask-your-books chat with shown reasoning. **No vendor publishes retention or weekly-active on the agent features themselves.**

### 2.4 Failure stories worth internalising *(r4 §6)*

Bench shut down on 27 Dec 2024 with immediate loss of platform access ($65.4M liabilities vs $2.8M cash) — **export-anytime and CA-readable books are a trust feature**. Intuit critiques: "the AI learns your mess", English-only, agents behind $275/mo, "shifts work to review". Odoo's own caveat that its AI actions "do not enforce business rules". Microsoft's honesty about the Payables Agent's limits. ICAI's revised Code of Ethics (effective 1 Apr 2026) explicitly covers AI; every agent action therefore needs a documented rationale, source evidence and a human sign-off a CA can hand to an auditor.

---

## 3. What we already have (the surface the agent must cover)

Measured from the working tree on 2026-09-08:

| Layer | What exists | Relevance to the agent |
|---|---|---|
| **Permission vocabulary** (`backend/internal/authz/vocabulary.go`, `routeAction()` in `server/auth_middleware.go`) | Five actions (`create`, `alter`, `delete`, `display`, `print`) over a **closed resource set**: voucher classes (`sales`, `purchase`, `receipt`, `payment`, `contra`, `journal`, `credit-note`, `debit-note`, `delivery-note`, `receipt-note`, `stock-journal`, `physical-stock`, `payroll`), `master`, `voucher`, `report` (+ ~45 named reports incl. `trial-balance`, `outstandings`, `cash-flow`, `anomalies`, `gstr1`, `gstr3b`, `gstr9`, `itc-reconciliation`, `ims-inbox`, `stock-summary`, `payroll-register`, `budget-variance`, `unadjusted-forex`), `company`, `import`, `sync`, `query`, `security`, `backup`, `price-list`, `employee`, `inward-statement`, `ims-action`, `gst-filing`, `bank-reconciliation`, `bank-statement`, `deductor-status`, `tcs-declaration`, `tds-challan`, `tds-profile`, `forex-settings`, `cost-centre`, `sales-order`, `purchase-order`, `bom`, `e-way-bill`, `e-invoice`, `reconciliation`, `budget`. Roughly **60 `(action, resource)` pairs**, every company route mapped or default-denied, enforced by `TestEveryCompanyRouteIsMappedAndInVocabulary`. | **This is the action registry's skeleton.** Each pair is a candidate `ActionSpec`; the PDP already decides for humans and will decide for the agent principal identically. |
| **Screens** (`frontend/src/keystroke/types.ts` `ScreenKind`) | ~40 kinds: gateway, voucher, report, report-detail, master-create, bill-inbox, query, members, edit-log, features, print-templates, tds-challans, cost-centres, budgets, gstr9, sales/purchase orders, boms, e-way-bill, e-invoice, bank-reconciliation, employees, payroll-run/register/payslip/form-138, data (backup/restore), tally-import, … | Coverage test target: every screen command ↔ an action; every action ↔ a UI path (so a human can always see and undo what the agent did). |
| **Query DSL** (`backend/internal/query`, `internal/nlq`) | Closed typed DSL over four entities (`ledger_balances`, `vouchers`, `postings`, `outstandings`), whitelist-validated, compiled to parameterised SQL in a read-only transaction with the company GUC; `nlq` is the only network-touching translator (Claude today), pinned by `archtest`. | **Tier A of the data layer already exists.** Needs: more entities/reports as catalogue items, synonyms (Hinglish), per-report agent metadata, golden-example retrieval, an OpenAI-compatible translator adapter. |
| **AI plane** (`insightd/`) | OCR → PII redaction (Presidio + PAN/GSTIN/Aadhaar-Verhoeff/IFSC) → classifier (Haiku) → inferencer (Opus) → pure `VoucherDraftMapper` → `PurchaseVoucherDraft`; **no ledger write path**, reached only through cored's draft proxy with a two-process service token; GST amounts re-derived, never trusted from the LLM. | The quarantined-extractor half of the injection defence is already structural. Swap the Anthropic adapters for PaddleOCR-VL + GLM-5.3-Flash / Qwen3.8-27B with `json_schema`; generalise from purchase invoices to sales, expenses, bank statements. |
| **Deterministic engines** (`internal/forecast`) | Cash-flow projection in Tally's Inflow/Outflow/Nett layout from open bills; rule-based anomaly report over the voucher-version chain; explicitly "NO statistics or ML here". | Seed of the insight engine's "deterministic facts" half. |
| **Notification outbox** (`internal/notify`) | Transactional outbox for outbound notifications. | The event source for ambient agents; the delivery path for digests and reminders. |
| **Ledger primitives** | Immutable BLAKE3 hash-chained voucher versions; deferred debit = credit trigger; `FORCE RLS` + `NOBYPASSRLS` app role + GUC guard; WebAuthn step-up on first write; account lockout; back-dated cutoff. | Every agent write flows through these unchanged. The intent log (§4 D4) links into the same chain. |
| **PLAN.md AI ADR** | Assumed Claude Opus 4.8 + Haiku 4.5, a manual tool-use loop, Anthropic prompt caching and Batch API. | Superseded on model strategy by §4 D3; the HITL, DSL-not-SQL, deterministic-engines and residency principles stand and are strengthened by this research. |

The gap between this and the brief is therefore *breadth and orchestration*, not foundations: an action registry generated from what exists, an agent runtime and approval inbox around it, the data-understanding layer (profiles, embeddings, sandbox), the insight engine, the learning loop, channels, and the LLMOps stack.

---

## 4. Ten architecture decisions

> **Approved by the founder on 2026-09-08.** These are no longer proposals; `PLAN-AGENTIC.md` executes them.

Each decision states the choice, the evidence, and what it changes.

### D1 — A typed **action layer** is the product; computer-use is a test oracle *(r5 §1, §2, §6, §8)*

- Every user-facing command becomes an `ActionSpec` in a Go package `actions/`: stable id equal to the keystroke-machine command id (e.g. `voucher.purchase.create`), title, description-for-agent, UI binding (screen, keys, accept key), ACL key (the existing `(action, resource)`), input schema *identical to the form's schema*, output schema, side-effect list, reversibility (compensating action), idempotency requirement, risk class, provenance rules, `preview` support, `long_running` flag, and parity-test traces.
- From `ActionSpec` we code-generate: OpenAPI, MCP tool JSON, TypeScript form types, and the RLS/ACL check. One artefact, three surfaces (UI, API, tool).
- The model sees **three meta-tools** (`actions.search`, `actions.preview`, `actions.commit`, plus `actions.status` for durable jobs) and a small always-loaded core (`query_books`, `get_draft`, `list_pending`), with domain packs retrieved by embedding/BM25 search over tool descriptions (Anthropic: accuracy degrades past 30–50 tools; tool search cuts context >85%; Composio and Cloudflare Code Mode expose thousands of endpoints through two tools). Never preload 400 CRUD tools.
- **Coverage test in CI**: for every keystroke-machine command assert a registered action with a superset schema and equal ACL key; for every action assert a UI path.
- **Parity test**: record human keystroke traces + resulting voucher hash; replay (i) through the keystroke machine headless and (ii) as an agent task from the natural-language intent; diff canonical voucher JSON; track success, steps, latency, cost. Microsoft's Payables Agent walks its *own* UI via metadata — our keystroke machine is already a metadata-described state machine, so a semantic (DOM/key-dispatch) driver, not pixels, is the parity oracle.
- Pixel computer-use only as a gated escape hatch for third-party portals with no API, in a VM, human-confirmed, credentials never in model context.

### D2 — Agent runtime in Go (`cored`), hand-rolled loop, durable via DBOS or Restate *(r1 §3, §4, §8)*

- Tools are in-process Go functions with tenant/RLS/ACL/step-up context already bound; every write flows through the voucher-version + outbox path. `insightd` stays a Python *worker* (OCR, redaction, embeddings, DSPy/GEPA prompt optimisation, evals, statistical features) invoked as a durable step — never a second write path.
- Durability: **DBOS Transact Go** (library, state in our Postgres, `dbos.Recv` with day-long timeouts for approvals) is the lowest-friction; **Restate** if we want one fabric across Go + Python; Temporal is the heavyweight fallback; avoid Inngest for a Go shop. Every agent run = a workflow; every LLM call and tool call = a step; the approval *decision* is itself written to the hash-chained log.
- Framework: none as runtime. Borrow Agent Inbox's `HumanInterrupt` schema, Pydantic AI's validate-before-approver rule, Deep Agents' todo/sub-agent shape. If Python is unavoidable for a component, Pydantic AI (typed, OpenAI-compatible, first-party Temporal/DBOS/Restate backends).
- Planning pattern: **plan-and-execute** (plan once with the Tier-A model, execute steps with cheaper models, replan on failure) because it yields an inspectable plan *before* side effects — exactly what an approval gate needs. Verifier loop = the deterministic validators the UI already uses (debits = credits, GST rate table, GSTIN, period lock), fed back as structured errors, one bounded retry.
- Structured output on every decision step; `tool_choice: required` where a tool must be chosen; temperature 0–0.3; DSPy GEPA to optimise tool descriptions and router prompts offline against our golden set before trusting a 30–120B model.

### D3 — Model routing: open weights in three tiers, India-hosted by default, Fireworks for the planner *(r2 §10)*

| Tier | Job | Primary | Fallbacks | Why |
|---|---|---|---|---|
| **A — planner** | multi-step voucher / reconciliation / filing flows | **GLM-5.3** on Fireworks (Standard lane; $1.40 / $0.26 / $4.40) — non-thinking for tool steps, `reasoning_effort: low` for the plan step | DeepSeek V4 Pro ($1.32 / $0.044 / $3.96, MIT, cheapest cached reads); Kimi K3 on explicit escalation; Claude Sonnet 5 behind a per-tenant consent flag for the ~2% that fail validation twice | Top open model on the Artificial Analysis index (45), #1 Terminal-Bench 3.0, Toolathlon 73.0 |
| **B — mid** | NL → DSL, invoice / bank-statement field extraction, WhatsApp dialogue | **GLM-5.3-Flash** (MIT, $0.15 / $0.03 / $0.50, multimodal) | DeepSeek V4 Flash; **Qwen3.8-27B** (Apache-2.0, OmniDocBench 91.1, the LoRA base — dense 27B fits FP8 on one 80 GB GPU) | Best price/intelligence in the open catalogue |
| **B-Indic** | Hindi / Gujarati / Marathi / Tamil / Hinglish chat surface | **Sarvam-105B** via Sarvam's India-hosted API (₹29 / ₹73 per 1M) | Hinglish-SFT Qwen3.5-9B | Only open model trained natively on romanised Hinglish; tool arguments stay English ids underneath |
| **C — small** | ledger suggestion, narration autofill, bank-line categorisation, intent routing | **Qwen3.5-4B** LoRA-tuned (paper evidence: 96.6 F1 on bank-descriptor extraction; 0.8B still 94.75) | Gemma 4 E4B on-device (Android, offline), Granite 4.2-3B | Grammar-constrained enum outputs, never free text |
| **Embeddings / rerank** | search over narrations, parties, items, statutes | Qwen3-Embedding-0.6B per tenant + BGE-M3 sparse; Qwen3-Reranker-0.6B | multilingual-e5 | $0.008–0.10 per 1M; run our own Hinglish eval on 2–3k narrations first |
| **OCR** | invoices, bank statements, kachha bills | **PaddleOCR-VL-1.6** self-hosted on an India GPU (0.9B, Apache-2.0, 109 languages incl. Devanagari; OmniDocBench 96.34 vs Gemini-3 Pro 92.91) | Qwen3.8-27B direct-image for photos/handwriting | Hundreds of pages/minute on an L4 for ₹50–100/hr |
| **Speech** | voice entry, readback | Sarvam Saarika (₹30/hr) + Bulbul (₹30 per 10K chars) now | IndicConformer-600M + IndicF5 (MIT) self-hosted at scale | Buy first, self-host at 100k min/month |

Provider posture: Fireworks primary (grammar mode, n-gram speculative decoding for DSL/JSON, RFT + Eval Protocol, session-affinity caching, Batch at 50% off, SOC 2 / ISO 42001, zero data retention), DeepInfra as cheap secondary (undercuts Fireworks on 30 of 38 shared models), Together tertiary; keep a provider abstraction with per-model golden tests because chat templates and tool-call parsers differ. Fine-tuned LoRAs serve **only on dedicated deployments** (H100 $8/hr ≈ $5.8k/month), so fine-tuning pays off only at tenant volume — collect trajectories from day one, train later. Known traps: `json_schema` disables reasoning output on reasoning models; Kimi K3 needs `reasoning_content` echoed back; Fireworks gives two weeks' deprecation notice and no weight-immutability promise — hence the nightly canary suite (D9).

### D4 — Agent as a principal acting on-behalf-of a human; autonomy is policy, not prompt *(r5 §3, §4, §8)*

- Principal = `{agent_id, on_behalf_of: user_id, delegation: {actions[], companies[], max_amount, daily_cap, valid_until, sig}}` (RFC 8693 on-behalf-of shape). **Effective rights = ACL(user) ∩ ACL(agent) ∩ delegation.** The agent can never exceed the human; the human may grant less.
- Structural controls independent of any prompt: agent-created parties are born **blocked** (Business Central pattern); the agent is never the checker in maker–checker flows; agents have **no** delete, ACL, period-lock or delegation-grant actions at all; hard deletes are replaced by compensating versions.
- **Risk classes** on every action: read / draft / commit / external-side-effect / config. Read: auto. Draft: auto (a draft is not a voucher). Commit: `HumanInterrupt` accept, or auto-commit only under a per-tenant policy the existing PDP evaluates (amount band, known party, period open). External side effects (GST filing, reminders to customers, bank instructions): WebAuthn step-up.
- **Autonomy levels** `L0 suggest | L1 approve | L2 auto+notify | L3 auto-silent`, stored per `(tenant, user, action, amount_band)`; default L1 everywhere; L2/L3 only for `reversible=true` actions below thresholds; promotion after N clean accepts with zero edits, demotion on any reversal or guard flag; "approve for session" as temporary L2 (Copilot Studio's per-tool gate is "independent of the agent's instructions" — that is the standard). OWASP ASI09 warns one-key accept breeds rubber-stamping: mandatory diff view above L1 thresholds plus periodic sampled re-review.
- Every write is `preview → policy → [approval] → commit`; preview returns the canonical ledger diff; `commit` replays the approved plan by id, never regenerates.
- **Intent log** row per attempt — `{request_id, idempotency_key, principal, channel, prompt_hash, source_docs[], tool_call, provenance_labels, policy_decision + policy_version, approver, edits_diff, result_version_hash, reversal_of}` — INSERT-only role, hash-chained, nightly head anchored to S3 Object Lock. RBI's FREE-AI survey found only 18% of regulated entities keep AI audit logs; CAs and bank partners will ask for exactly this artefact.
- Approval inbox with four decisions (approve / edit-then-approve / reject / ask-back), routed to roles, deliverable to WhatsApp/email with step-up above amount thresholds, backed by the durable workflow so approvals can take days.
- Undo = generated reversal action, one key, within the period lock.

### D5 — Prompt-injection defence is architectural (CaMeL / Rule of Two), not a classifier *(r5 §5; r8 §3)*

Untrusted inputs: supplier invoices, bank statements, WhatsApp/email text, GSTR-2B JSON, Tally XML. Adaptive attackers break detection-based defences; the architecture must make it *impossible* for ingested text to trigger a consequential action:

1. **Quarantined extractor** (insightd today): input = document; output = strict JSON schema; no tools, no memory, no other-tenant data; spotlighting on raw text.
2. **Deterministic validation** decides confidence (GSTIN checksum, HSN ↔ rate table, duplicate detection, bill-wise outstanding, totals reconcile) — not an LLM.
3. **Provenance labels** on every field (`source: doc#123 p1 r3`, `trust: untrusted`) carried into action arguments; policy: an untrusted `party_id` / `amount` / `bank_account` can never reach money movement without human approval; untrusted text can never populate a free-text instruction to another agent.
4. Planner/executor sees validated JSON plus the user's request only.
5. **No memory writes from document content** (OWASP ASI06); memory entries carry the same provenance labels.
6. Guard models (Prompt Guard 2 86M — Hindi supported; Granite Guardian 3.3 8B for function-call hallucination on write plans) **raise the review level**, never auto-deny — a 2026 classifier study reports Prompt Guard flagging ~96% of benign agentic content.
7. **No secrets in model context**: e-invoice/GSP credentials and bank tokens live in the executor; the model calls `einvoice.generate(voucher_id)`.
8. Evaluate quarterly on AgentDojo (banking domain included), InjecAgent, and a Tally-specific suite (injected text in invoice line descriptions, bank narrations, WhatsApp forwards, Tally XML `<NARRATION>`).

### D6 — Data layer: semantic catalogue → guarded SQL → sandbox, all Postgres-first *(r9 §1–§7; r6 §5.2)*

- **Tier A (default, ≥80% of questions)**: the existing DSL, extended into a semantic catalogue — per-report agent metadata (description, synonyms incl. Hinglish "udhaar" → receivables, typical questions, example DSL), a CHESS-style **selector** step (embed the question, retrieve top-k reports + entities via pgvector, then generate the DSL), and a per-tenant `emb_golden_query(question, dsl, accepted)` store for few-shot retrieval — Vanna's core idea and our "keeps learning" loop for query. Semantic layers fail loudly; raw text-to-SQL "returns plausible, but incorrect answers".
- **Tier B (long tail)**: agent-written SQL over *published read-model views* (`v_ledger_entries`, `v_voucher_lines`, `v_party`, `v_stock_movements`, `v_bank_lines`, `v_gst_returns`) with `security_barrier`; separate `agent_ro` role with SELECT on the view schema only; `SET ROLE` + `SET LOCAL app.tenant_id` in a `READ ONLY` transaction; `statement_timeout ≈ 5 s`; AST validation (pg_query / sqlglot: one SELECT, no COPY, allow-listed functions, no catalogs); forced `LIMIT`; EXPLAIN cost gate; M-Schema cards (columns + 3 sample values + FK edges) only for selected views. RLS scopes rows; the role/timeout/AST layers handle resource, timing and side channels.
- **Tier C (analysis)**: DuckDB + Polars in a locked sandbox (gVisor runtime pods on EKS in ap-south-1 first; Windmill/nsjail in-VPC; Modal `ap-south` as burst) over a **nightly per-tenant Parquet extract**; no network except the model gateway; result-size gate before anything re-enters the prompt. Browser DuckDB-Wasm/Pyodide mode for zero-egress "explore" screens. This is also the only place the agent may write code — read-only analysis, never writes.
- **Search**: pgvector 0.8.6 (`halfvec(1024)`, HNSW, iterative scans for tenant-filtered recall), embeddings tables partitioned by tenant hash under the same `FORCE RLS`; `pg_trgm` for lexical; Splink 4 (MIT, Postgres backend) for party dedup and bank ↔ ledger linkage. Keep amounts, dates, GSTIN, HSN, voucher numbers relational; embed narrations, party aliases, item names, document chunks.
- **Statutory grounding is mandatory**: the 56th GST Council collapsed slabs to 5 / 18 / 40% effective 22 Sept 2025; any model with earlier weights emits 12% / 28%. Versioned notification chunks with `effective_from / effective_to`, rate lookups relational, every answer cites notification number and paragraph; ingest from Indian egress (NIC/CBIC hosts are geo-fenced, per project memory).
- **Tenant context packing**: full masters of a typical SMB are 15–110k tokens; a tiered 2–5k-token "business card" (industry, FY, GST scheme, group tree, top-50 ledgers/parties/items, naming conventions, recurring patterns) sits behind a cache breakpoint; everything else is retrieved on demand.
- **Events and jobs**: outbox → **River** (Go, transactional enqueue, periodic jobs) → ambient handlers with three outcomes (notify / question / review) landing in the inbox; LISTEN/NOTIFY only to cut poll latency; pg_cron for materialised views; no Kafka/CDC until an external sink exists.
- **Documents**: Docling (MIT, bounding-box provenance) → PaddleOCR-VL / Qwen3-VL schema-guided extraction into typed models with per-field `{value, page, bbox, confidence}` → validators → draft with provenance links; Gmail Pub/Sub watch + Graph webhooks for mailboxes; originals immutable under 8-year retention.
- **DPDP × immutable ledger**: separate personal contact data (phone, email, address, individual PAN) into referenced tables; erasure = crypto-shred the referenced row while the ledger keeps statutory identifiers; the hash chain covers voucher content, not the mutable contact table; cascade erasure to embeddings, profiles and extracts (regenerated nightly, so a 24 h SLA is natural).

### D7 — Memory and learning: seven tiers in Postgres, corrections → exemplars → rules → (later) adapters *(r3 §2, §3, §6, §7, §9)*

| Tier | Table(s) | Written by | Read how |
|---|---|---|---|
| M0 Company block | `tenant_profile` (≤1.5k tokens text + JSON prefs) | nightly job + user edits | always in system prompt (Letta core-memory pattern), user-visible and editable |
| M1 Entity facts | `entity_facts`, `entity_edges` — bi-temporal (`valid_from/valid_to`), evidence ids, `source ∈ {deterministic_job, llm_inference, user_stated, user_corrected}`; ≤400-token narrative per entity | nightly deterministic + LLM narrative | by entity id whenever the entity appears in a turn |
| M2 Exemplars | `exemplars` (pgvector; features → label + outcome + who/when) | every accept/edit | top-k by similarity per suggestion (+5–7 F1 in cited studies) |
| M3 Rules | `rules` (predicate DSL, confirmed_by, hit/miss) | LLM proposes after ≥3 consistent corrections; human confirms | evaluated deterministically **before** any model call (Ramp/Brex pattern) |
| M4 Procedural | `skills` (SKILL.md text, trigger, version), `strategies` (ReasoningBank-style, incl. failures) | agent reflection, ops | trigger match; Skill-to-LoRA later |
| M5 Episodic | conversation summaries + Reflexion notes | end of session | semantic search; 90 days then summarise |
| X Cross-tenant priors | separate schema `network_priors` (k ≥ 20, Laplace noise, hashed tokens, no free text) | monthly aggregate job | read-only service role |

Cold start comes from **mining the Tally import**, not conversation: ledger-mapping habits per (party, narration tokens), recurring streams (tolerance ±3 days / ±5% or Lomb-Scargle), party payment-lag distributions, ABC/XYZ, books style, process chains (Order → Challan → Invoice → Receipt). Vic.ai's framing is the target: "accurate from day one" from a global prior plus the company's own history. The nightly refresh is our sleep-time compute: precompute every *number* deterministically; the LLM only narrates changed entities (200–400 tokens each on an 8–30B model — a few rupees per tenant-night).

Measurement: edit-weighted accept rate, **audited error rate behind the confidence gate** (report both, honestly — every vendor "95–98%" is an accept rate or accuracy-behind-a-gate), time-to-accept, insight click-through and action-within-7-days, false-positive rate on flags, stale-fact rate, memory-write cost per turn. Weekly regression replay of all corrections against current prompts; per-cohort guardrails (Stripe's per-merchant pattern); publish a per-tenant "corrections per 100 vouchers by month" learning curve.

### D8 — Insight engine: deterministic detectors, expected-value ranking, capped digests *(r6 §8)*

```
Postgres 16 (tenant RLS)
  source tables → NOTIFY on commit → Go event router → real-time rules (e-way bill needed, credit-limit breach, duplicate bill, vendor bank change, HSN/POS mismatch)
  features schema: party_features · item_features(godown) · supplier_features · recurring_stream · tenant_features (as_of, extras jsonb)
  insights(id, tenant, type, entity, score, impact_inr, p_real, payload_json, evidence_sql, voucher_ids[], expires_at, state)
  insight_feedback · insight_suppression
Python plane (nightly River/Temporal schedule, staggered per tenant)
  StatsForecast (Croston/TSB/ADIDA/IMAPA) + Chronos-2 / TimesFM-2.5 for A/B items · LightGBM + scikit-survival for P(late) and pay-date quantiles
  PyMC-Marketing BG/NBD + Gamma-Gamma for CLV / P(alive) · implicit ALS + mlxtend FP-growth for cross-sell · splink dedup · PyOD ECOD/IForest · Nigrini Benford (screening only)
  ranker: score = impact × p_real × actionability × novelty(type, entity) × affinity(user, type)
  digest composer: top-5 per user per morning; LLM narrates from payload_json by key only
Feedback: verdicts → per-type precision per tenant → auto-lower cadence below 0.5; bandit over templates/timing (Duolingo Recovering Difference Softmax)
```

Build rules: every insight stores its evidence query and voucher ids (Tally users trust what they can drill into); **no LLM call may produce a number not present in `payload_json`** (Program-of-Thought discipline, ~12% gain over chain-of-thought on FinQA-class tasks); thresholds are tenant-relative before absolute; hard cap on digest size and per-type weekly caps; foundation-model forecasts opt-in per tenant after a rolling-origin backtest beats the classical ensemble; licence check (Chronos-2 / TimesFM-2.5 Apache-2.0 yes; Moirai-2 CC-BY-NC no). The `holidays` library covers India with 36 subdivisions and computes Diwali/Navratri/Holi/Eid for 2001–2035; wedding-season and monsoon covariates are built from panchang tables.

### D9 — LLMOps: self-hosted Langfuse in Mumbai, state-based evals, four-tier CI gate *(r8 §1, §2, §7, §9)*

- **Tracing**: Langfuse v3 self-hosted via its official AWS Terraform module (EKS Fargate, Aurora Serverless v2, ElastiCache, S3, in-cluster ClickHouse) in ap-south-1 — no LLM observability SaaS has an India region; Go emits `gen_ai.*` spans via the plain OTel Go SDK to Langfuse's OTLP/HTTP endpoint (no gRPC); Python via the Langfuse SDK or OpenLIT. Phoenix (Postgres backend) is the alternative.
- **Evals**: adopt tau-bench's principle for the voucher agent — **judge the ledger state after the run, not the transcript**, and track pass^k. Golden sets: NL→DSL (canonical AST + result-set equality on a fixture ledger), extraction (per-field F1 with normalisers; every F9 Bill Inbox edit becomes a label), GST reconciliation classification (macro-F1, ITC-weighted; CA-reviewed month-ends), voucher-agent tasks (state equality, pass^5, "no unapproved write" invariant), insight quality (human 1–5 + pairwise vs champion with position swap). Tools: DeepEval (agent metrics), promptfoo (GitHub Action on PRs touching prompts), Inspect (sandboxed simulated-user agent evals); Fireworks Eval Protocol later for RFT.
- **CI gate**: Tier 0 every PR, no LLM (schema/DSL tests, ledger invariants, replay of 500 recorded runs against cassettes). Tier 1 on prompt/model/tool changes (exact-match and F1 within 0.5 pt of champion; zero unapproved writes; zero injection-suite bypasses). Tier 2 nightly (tau-style sims pass^5 on 200 tasks; pairwise insight judge n=200 with paired bootstrap; garak probes) blocking the weekly release train. Tier 3 weekly/monthly (DeepTeam red-team mapped to OWASP Agentic Top 10; replay of last-30-days corrections; per-cohort drift).
- **Drift**: nightly 200-case canary against every production model id (Fireworks may change weights behind an id); accept-rate CUSUM per tenant; shadow mode and champion/challenger with 5% → 25% → 100% ramps and automatic rollback; a dedicated deployment of a pinned checkpoint for the write-agent path once volume justifies.
- **Guardrail pipeline**: PII redact (Presidio `IN_PAN`, `IN_AADHAAR`, `IN_GSTIN` + GLiNER + IFSC/UPI regex) → Prompt Guard 2 on user text / indirect-injection classifier on documents → budget reserve + exact-match cache keyed on `(tenant, normalised NL, ledger snapshot version)` → LLM with static prefix first and `json_schema` → Pydantic/Go re-validation with ≤2 schema repairs → scope check vs capability token → Granite Guardian on write plans → Go plan-mode ledger diff with invariants → approval UI → `commit(plan_id, idempotency_key)` → saga log; narratives pass a **number-provenance check** (every digit bound to a tool result). Deterministic replay against recorded tool results (VCR cassettes) so debugging a bad voucher never needs live inference.
- **Landscape churn to note**: LLM Guard archived Jul 2026, Rebuff archived May 2025, Vanna archived Mar 2026, Kuzu archived Oct 2025, Daytona went closed-core Jun 2026 — prefer Postgres-native and first-party implementations over thin libraries.
- **Compliance artefacts** a CA/auditor will ask for: per AI-originated voucher — who approved, what they saw (plan diff), model/prompt version, inputs; an exportable AI action register; evidence the approval gate cannot be bypassed; a processing-location statement; model cards; a DPIA. Map controls to NIST AI RMF; ISO 42001 readiness by month 12 (Fireworks already holds 42001).

### D10 — Channels and rails: same action layer, channel-native approvals, WhatsApp-first, India egress *(r4 §2.3; r5 §7; r7)*

Every channel adapter (in-app Alt+G palette and side panel, WhatsApp, voice, email, MCP) authenticates the human (linked number + step-up above L1), converts the message into the same `ActionRequest`, and renders approvals as channel-native cards. Transcripts and forwarded documents are untrusted input (D5). MCP exposure of the *same* Go registry (official Go SDK, spec 2026-07-28, streamable HTTP, OAuth with Client ID Metadata Documents) lets Claude / ChatGPT / a CA firm's agent act on a tenant's books with the same risk classes enforced server-side — Intuit (145 tools), Xero (~46), Zoho Books (hosted, Apr 2026) and Stripe all do this, and none embed approval in-protocol, which MRTR `input_required` now makes possible for us. AG-UI events stream agent state into the React shell; A2A is deferred until an external agent must delegate to ours.

**WhatsApp (own WABA on the Cloud API, BSP optional for speed)** *(r7 §1)*: per-message billing since 1 Jul 2025; India list rates (Jan 2026, INR-billed) marketing ₹0.86, **utility/authentication ₹0.115**, service free; utility templates are **free inside the 24-hour customer-service window**, so owner ↔ agent conversation and most reminders cost nothing — the only material spend is proactive nudges to debtors who have not replied. INR billing migration is mandatory by 31 Dec 2026. The API accepts voice notes (OGG/Opus ≤16 MB), invoice photos (≤5 MB) and PDFs (≤100 MB); media URLs expire in 5 minutes, so persist on webhook. **Flows** give in-chat forms with photo/document upload and an encrypted data endpoint — "confirm this bank match?" and "approve this GSTR-3B summary?" without an app install. SMS only for OTP/critical alerts under TRAI DLT templates; skip RCS and Telegram for v1.

**Voice** *(r7 §2)*: Sarvam Saaras v3 streaming STT (23 languages, verbatim and code-mixed modes, ₹30/hour ≈ ₹0.25 per 30-second voice note), Bulbul v3 TTS (₹30 per 10K characters); open fallbacks IndicConformer-600M (MIT, Hindi WER 13.2), Indic Parler-TTS (Apache-2.0, 21 languages), IndicWhisper (best on 39 of 59 Vistaar benchmarks). Real SMB speech is Hinglish/Gujlish by default — evaluate STT on code-mixed domain utterances ("₹4,500 Sharma ji ko diya"), not read Hindi; readback verification of amounts before anything posts. PSTN voice agents (Exotel Agent Stream) only after WhatsApp voice proves the intent grammar.

**Bank and payment rails** *(r7 §3)*: the Account Aggregator ecosystem is large (17 AAs, 176 FIPs, 1,076 FIUs, 295M data shares/month in Jun 2026; GSTN is a FIP sharing GSTR-1/3B for 18 months) but **an unregulated SaaS cannot be an FIU** and **partnership/company current accounts are excluded from AA today** — so statement import (our `bankstatement` package) stays essential, AA via a regulated FIU partner covers proprietors, and connected-banking APIs (Tally covers Axis/SBI/ICICI/Kotak) come later. UPI has zero MDR but payment aggregators charge ~2%; payment links and invoices are free on Razorpay. **Bharat Connect for Business** (NPCI/NBBL) is live in TallyPrime: Party B2B ID on the ledger → invoice presented over the network → buyer pays → both sides auto-reconcile — the single most important new rail, because it gives interoperable invoice presentment, payment and reconciliation across accounting platforms.

**Government rails and filing legality** *(r7 §4)*: GST taxpayer APIs via a GSP after an OTP session (GSTR-1/3B save/submit/file, 2A/2B fetch, ledgers, IRP, e-way bill); IMS (live Oct 2024) exposes Accept/Reject/Pending with deemed acceptance into 2B; GSP pricing is quote-based. **Returns must be verified by the authorised signatory's EVC OTP or DSC (Rule 26), and a GST practitioner's filing needs taxpayer confirmation (Rule 83(8))** — so the agent may prepare, validate, reconcile, populate, *save* via API and stage IMS decisions, and the human presses "file" with the OTP on their phone. Design the last mile as "review → tap → OTP", and render "prepared by agent, filed by <name> via EVC at <time>". NIC/IRP and RBI hosts are geo-fenced, so the backend egresses from India regardless of where inference runs.

**Regulatory clocks** *(r7 §6)*: DPDP Rules — consent-manager registration from 13 Nov 2026, full enforcement and penalties (₹250 cr safeguards, ₹200 cr breach) from 13 May 2027, breach report to the Board within 72 hours, cross-border on a negative list with none notified yet; CERT-In — incident report within **6 hours**, **180 days of logs retained in India**, NIC/NPL NTP; MCA audit trail since Apr 2023, 8-year retention, non-disableable, and agent edits must be attributed to a principal. India-hosted inference options today: Sarvam (hosts Gemma-4 31B, GLM-5.3-Flash, DeepSeek V4 Flash in INR), Krutrim, E2E Networks (H100 ≈ ₹315/hr), Yotta, Bedrock Mumbai.

---

## 5. The insight catalogue (what "unique insights about everything" means) *(r6 §8.1, condensed)*

Cadence: RT = on event, D = daily, W = weekly, M = monthly. Every row's action is offered behind approval (D4).

**Daily ops** — invoices due today/tomorrow (rule → send reminder); bills we owe due in 3 days (rule + cash forecast → schedule payment); cheques/PDCs maturing; sales orders past promised date (→ draft delivery note / call list); unreconciled bank lines > 3 days (→ suggest matches); unposted drafts; e-way bill needed for today's dispatch above the state threshold (RT → generate).

**Receivables** — predicted-late invoices, P(late) > 0.5 (LightGBM + SHAP → pre-emptive reminder); expected payment date per invoice, p50/p80 (survival model → feeds cash forecast); customer paying slower than their own norm (CUSUM vs party baseline → call / tighten terms); credit-limit breach (RT → hold order); concentration risk, top-3 debtors > 50% of AR; best time to remind this customer (bandit); disputed / short-pay patterns.

**Payables** — early-payment discount worth taking (NPV vs cash forecast); duplicate supplier invoice suspected (Splink, RT → hold payment); supplier price creep per item (rolling p90 → renegotiate / alternate supplier); **supplier hasn't filed GSTR-1, ITC at risk** (GSTR-2B vs register, on the 15th → nudge supplier); vendor bank details changed before payment (RT → verify by call); paying suppliers faster than customers pay us (DPO < DSO).

**Sales growth / cross-sell** — **customers to push item X to** (implicit ALS + rule lift + margin + seasonality → draft offer list with expected margin); complementary items missing from a customer's basket (FP-growth); customers overdue for reorder vs their own cadence (BG/NBD P(alive) → reorder prompt); churn-risk customers (declining frequency/value); top-CLV customers and share (BG/NBD + Gamma-Gamma); under-priced customer–item pairs below margin floor (→ reprice); **festival demand uplift expected per item/segment** (forecast with holiday covariates, T-45 days → pre-book stock / campaign); region/state where sales are dropping; new-customer acquisition trend.

**Inventory** — reorder now, on-hand < ROP (quantile forecast → draft PO); stock-out risk in 14 days (→ expedite); dead stock, no movement N days (→ discount / return); expiring batches vs sell-through (→ promote); ABC-XYZ reclassification; overstock, days of cover > threshold (→ pause POs); supplier lead-time drift; godown imbalance (→ transfer note); negative stock / valuation anomalies.

**Cash** — 13-week cash path p10/p50/p90 (Monte Carlo over AR/AP timing distributions and recurring streams); runway / shortfall alert when P(cash < 0) > 20% (→ accelerate-collections list); OD utilisation > 80% sustained; idle cash above buffer (→ FD / sweep); recurring outflow increased (rent, EMI, subscriptions).

**Tax / compliance** — GST liability estimate for the month + ITC available (from the 14th → reserve cash); HSN-rate mismatch on invoice (RT → correct); place-of-supply / GSTIN state mismatch (RT); TDS deducted but not deposited by the 7th; purchase register vs GSTR-2B mismatch list (→ contact supplier); IMS accept/reject/pend suggestions with reasons.

**Integrity** — back-dated / post-close edits by user (edit-log rate vs peers); amount outliers by ledger/user (PyOD, top-k weekly); threshold clustering just below ₹50k / ₹2 lakh; Benford deviation on expense ledgers (quarterly, screening only).

**People / payroll** — salary cost % of revenue trend; PF/ESI due nudges; overtime spikes; headcount vs sales productivity. **Benchmarking** — DSO/DPO/DIO/gross margin vs anonymised same-industry tenants once a cohort has ≥ 30 members ("leading / lagging / in line", Xero's framing); until then vs own trailing 12 months.

---

## 6. The learning flywheel, end to end *(r3 §9)*

**Per turn**: M0 company block + M1 facts for entities in the turn + M2 top-k exemplars + M3 rule hits (~3k tokens total). **On every suggestion shown**: log `shown → {accepted, edited(field diff), rejected, ignored}`, time-to-decision, who decided, later reversal. **Immediately**: exemplar upsert. **At ≥3 consistent corrections for one predicate**: LLM proposes an explicit rule ("Narration ILIKE '%swiggy%' → Staff Welfare, ITC blocked"); user confirms; rule runs before the model. **Nightly 02:00 IST**: deterministic features, LLM narratives for changed entities, exemplar pruning by utility (ReMe), reflection distillation, recurring-pattern rescan on new bank lines. **Weekly**: regression replay of all corrections against current prompts; A/B readout (edit-weighted accept rate). **Monthly**: network-prior rebuild with k-anonymity + noise; optional KTO/SFT LoRA on opted-in scrubbed traces (Fireworks LoRA-SFT $0.50 per 1M tokens ≤16B; DPO $1.00) or ART/GRPO with a RULER-style judge for the multi-step agent, gated on held-out replay per cohort, canaried at 5% of tenants, rolled back on any per-cohort regression; DPIA delta.

**Across tenants**: Tier 0 public knowledge → Tier 1 aggregate priors (`normalised_counterparty_token → ledger_group`, `HSN → typical ledger`, industry DSO/DPO/margin quantiles, seasonal indices) released only from ≥ 20 tenants with Laplace noise, hashed tokens, provenance for Rule 8 erasure → Tier 2 model weights only from synthetic data generated from Tier-1 shapes or separately-consented, scrubbed tenant data. Governance: DPIA per tier, algorithm-risk screening write-up, per-tenant "contribute anonymised statistics" toggle, published k and ε.

**Do not** publish LoCoMo-style memory scores; publish accept rate and audited error rate. **Do** build Letta-style editable memory blocks visible to the user — ChatGPT and Claude have taught users to expect to see and edit what the system remembers.

---

## 7. Cost envelope (reconciled across r2, r8, r9)

Assumptions: ₹88/$; Fireworks Standard lane; prefix caching on; PII redaction local; typical tenant = 200–300 NL queries, 150 documents, 60–100 agentic runs (5–6 tool calls each), 30 nightly insight runs.

| Component | Typical tenant-month | Notes |
|---|---|---|
| Tier-A agentic runs (GLM-5.3) | $0.60 – 2.00 | >90% of open-stack inference spend; the fine-tuning target |
| NL → DSL (Flash) | $0.04 – 0.30 | ≈ $0.0002 per query with caching |
| Extraction (PaddleOCR-VL + Flash / Gemma 4) | $0.10 – 0.40 | ≈ $0.0006 per invoice; self-hosted OCR ~ $0.0002/page |
| Nightly insights (Batch, 50% off) | $0.06 | ≈ $0.002 per run |
| Guardrails, embeddings, judge samples | $0.12 | |
| Observability share (self-hosted Langfuse cluster amortised over 1,000 tenants) | $0.60 – 0.90 | fixed cost, falls with scale |
| Sandbox compute | $0.05 – 0.10 | |
| **Total** | **≈ $1.5 – 3.5 (₹130 – 310)** | vs ≈ ₹505 on Claude Sonnet 5 everywhere, ≈ ₹2,200 on Claude Fable 5.1 |

At ₹500–2,000/month plan prices this is 7–60% of revenue depending on tier and tenant weight — viable, but only with per-tier token budgets (reserve/settle with degraded modes, not hard failures), Batch for everything non-interactive, and a LoRA'd small model for extraction and bank-line categorisation once golden sets exist. A fine-tuned Qwen3.8-27B handling 70% of vouchers at ~$0.003 each drops the tenant month under $1 — but LoRA serving needs a dedicated H100 ($8/hr), so it pays off only at a few thousand tenants.

---

## 8. Honest limits and risks

- **End-to-end accounting autonomy is far from solved** (APEX-Accounting 56.4% criteria, ≤21.5% Pass@8; AccountingBench shows frontier models "plugging" discrepancies over successive months). The product promise must be "every task is doable and gated", not "the agent closes your books".
- **Open-model tool-calling fragility**: text-in-content instead of `tool_calls`, hallucinated tool names, parallel-call loops, argument values in Hindi (gpt-oss reports; MLCL paper). Mitigated by constrained decoding, small per-turn tool surface, id-not-name arguments, canonicalisation, one bounded retry — and measured by our own 200-task tau-style eval in English + Hindi + Hinglish before choosing GLM-5.3 vs DeepSeek V4 Pro.
- **Alert fatigue** kills digests that are wrong twice; ranking, caps and feedback are load-bearing, not polish.
- **Provider risk**: no India region on Fireworks; two-week deprecation notices; no weight immutability; region-pinning +50%. Sarvam Inference hosting of GLM/DeepSeek is beta. Plan a fully India-resident mode (self-hosted GLM-5.3-Flash on 2×H100 at Yotta/E2E) as a paid tier for regulated customers.
- **Licences**: Kimi K3 and GLM-5.3 have custom licences with revenue clauses — read before shipping; Moirai-2 and Cohere Command A are non-commercial; ParadeDB, accounted, india-compliance, rustledger are AGPL/GPL — study, do not link.
- **Filing legality**: GST returns are verified by the authorised signatory's EVC OTP or DSC (Rule 26) and practitioner filings need taxpayer confirmation (Rule 83(8)) — the agent prepares, saves and stages; the human files. Several r7 rule-text citations are marked **[unverified — prior knowledge]** because the government portals were unreachable; confirm with counsel.
- **Bank data access**: an unregulated SaaS cannot be an Account Aggregator FIU, and company/LLP/partnership current accounts are excluded from AA today — statement import remains the workhorse for the segment Tally serves.
- **CA trust**: Tally's scepticism is the CA community's default. Publishing our per-tenant auto-post rate, audited error rate and hard operational limits (Microsoft's honesty) will build trust faster than a "99%" banner.
- **Research-agent caveats**: several r-files hit their web-search budget mid-task and completed via direct fetches; each lists pages that were unreachable (MeitY, iso.org, some Fireworks docs) and marks recall-based items. Baseten/Novita/Nebius were not researched.

---

## 9. What to verify before committing engineering time

1. **Bake-off on our distribution**: 200 tasks (voucher entry, GSTR-1/3B prep, bank reco, NL query) in English, Hindi, Hinglish across GLM-5.3, DeepSeek V4 Pro, Qwen3.8-27B, Sarvam-105B on Fireworks / DeepInfra / Sarvam; measure state-based success, pass^5, tokens, latency, malformed-call rate.
2. **Hinglish embedding eval** on 2–3k real narrations (Qwen3-Embedding vs BGE-M3 vs multilingual-e5).
3. **PaddleOCR-VL-1.6 + Flash** field-level F1 on 100 Indian invoices and 50 bank statements (HDFC, ICICI, SBI, Axis, Kotak) vs the current Opus pipeline.
4. **DBOS-Go vs Restate** spike: an approval that waits three days across a `cored` restart.
5. **Fireworks facts to re-check live**: US-only routing premium (10% vs 50% from 1 Sep 2026), RFT base-model list (V4 / K3 / GLM-5.x), "document inlining" existence, Sarvam Inference residency statement.
6. **Legal**: DPDP Rule 15 negative list vs bank-partner contractual residency demands; GST filing authority for an agent; ICAI documentation expectations for AI-assisted entries.

---

## Appendix — the nine research streams

| File | Topic | Words | Sources |
|---|---|---|---|
| `docs/research/agentic/r1-agent-frameworks.md` | Frameworks, durable execution, Go vs Python, MCP 2026-07-28, tool design at scale, planning patterns | ~4,000 | 80 |
| `docs/research/agentic/r2-open-models-providers.md` | Open-weight landscape Sept 2026, Fireworks in depth, provider comparison, tool-calling reliability, cost model, fine-tuning evidence, Indic, embeddings, OCR/VLM | ~6,000 | 80 |
| `docs/research/agentic/r3-memory-learning.md` | Memory systems and benchmarks, business profile, learning without/with training, cross-tenant learning under DPDP, cold start, evaluation | ~4,800 | 70 |
| `docs/research/agentic/r4-finance-ai-landscape.md` | ~25 Western products, India landscape incl. Tally 7.0/7.1, 20 OSS repos, 17 papers, insights shipped, failures, capability map | ~6,000 | 100 |
| `docs/research/agentic/r5-agent-native-app.md` | Computer-use vs action layer, agent-native patterns, permission models and regulation, graduated autonomy, injection defence, long-tail coverage, `ActionSpec` sketch | ~5,400 | 76 |
| `docs/research/agentic/r6-insight-engines.md` | Customer/sales, inventory, cash, integrity analytics; data-to-words; text-to-SQL and semantic layers; ambient agents; 52-type insight catalogue and engine architecture | ~5,900 | 80 |
| `docs/research/agentic/r7-india-channels-rails.md` | WhatsApp Business Platform pricing/media/Flows, BSPs, Indic voice stack, Account Aggregator and bank/UPI/Bharat Connect rails, GSTN/IMS/GSP and filing legality, marketplaces, DPDP/CERT-In/RBI/MCA constraints, India-hosted inference | ~4,800 | 70 |
| `docs/research/agentic/r8-eval-guardrails-ops.md` | Observability, eval frameworks and golden sets, guardrails and constrained decoding, reliability engineering, cost control, security (OWASP/MCP/ATLAS), drift, compliance, stack + CI gate + playbook | ~6,200 | 102 |
| `docs/research/agentic/r9-data-layer-for-ai.md` | Text-to-SQL vs semantic layer vs DSL, sandboxes, retrieval and statutory RAG, KG vs facts tables, triggers and jobs, feature store, document ingestion, multi-tenant AI data, cost | ~5,500 | 77 |
