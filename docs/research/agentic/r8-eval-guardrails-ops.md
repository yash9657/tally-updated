# R8 — LLMOps for a financial agent: evaluation, guardrails, observability, cost, reliability

Research date: 2026-09-08. Method note: the session's web-search budget was exhausted before this task started, so every fact below comes from direct fetches of primary pages (vendor docs, GitHub READMEs, model cards, standards pages). Items I could not verify are explicitly flagged "(unverified recall)". Where a page returned 403/404 that is noted rather than guessed around.

Context assumed: Go core over PostgreSQL 16 with tenant RLS; Python/FastAPI AI plane; open-weight models on Fireworks; tool-calling agent with approval gates; NL query -> DSL; invoice extraction; GST reconciliation; proactive insights. Deployment target AWS ap-south-1 (Mumbai).

---

## 1. Observability and tracing (state of play, Sept 2026)

### 1.1 The standard: OpenTelemetry GenAI semantic conventions
- The GenAI semconv moved out of the main `semantic-conventions` repo into `open-telemetry/semantic-conventions-genai`. Status is still **"Development"** (not Stable). It defines model spans, **agent spans**, events for inputs/outputs, exceptions, metrics, and provider-specific pages (OpenAI, Anthropic, Bedrock, Azure AI) plus an **MCP** page. [1][2]
- Practical consequence: the attribute names (`gen_ai.*`) can still change; pin the semconv version you emit and keep a mapping layer in the Go core rather than hard-coding attribute names in dashboards.

### 1.2 Tool-by-tool

| Tool | License / model | Self-host in ap-south-1? | OTel ingest | Go story | Python story | Pricing signal |
|---|---|---|---|---|---|---|
| **Langfuse v3** | MIT except `ee/` folders; joined ClickHouse Jan 2026 [3] | Yes. Postgres + ClickHouse + Redis/Valkey + S3 + web/worker; Docker Compose, Helm, and an **official AWS Terraform module** (EKS Fargate, Aurora Serverless v2, ElastiCache, S3, in-cluster ClickHouse) — region is a variable, so Mumbai works [4][5] | OTLP/HTTP (JSON + protobuf) at `/api/public/otel`; maps `gen_ai.*`; **no gRPC** [6] | No native SDK; use the OTel Go SDK -> OTLP/HTTP. Langfuse docs point Go users at OpenLIT/OpenLLMetry [6] | Native SDK; LLM-as-judge, datasets/experiments, annotation queues, CI gating [7] | Cloud: Hobby free 50k units; Core $29; Pro $199; Enterprise $2,499/mo; regions **US, EU, Japan** (no India) [8] |
| **Arize Phoenix** | **Elastic License 2.0** (not OSI); SQLite or **PostgreSQL** backend; OTLP + OpenInference [9][10] | Yes: Docker/Helm/CloudFormation; "100% free, no usage limits"; OAuth2/LDAP auth; pin versions like `version-8.0.0` [10] | OTLP native | OTel Go -> OTLP | `arize-phoenix-evals` (Python + TS), traced judge runs, 20x concurrency [11] | Free self-host; Arize AX for online evals |
| **LangSmith** | Proprietary | Only on **Enterprise** add-on with license key; Postgres + ClickHouse + Redis/Valkey + blob; Kubernetes [12] | Yes | OTLP | Native | Developer free (5k traces); Plus **$39/seat** + 10k traces; cloud regions US/EU [13] |
| **Braintrust** | Proprietary; `autoevals` is MIT (Py + TS) [14] | Hybrid: data plane (API, Postgres, Redis, Brainstore, object store) in your VPC via **Terraform for AWS/GCP/Azure**; control plane/UI stays SaaS [15] | Yes | OTLP | Native | Free tier; **Pro $249/mo**; 1 GB data included then $4/GB; 10k scores then $2.50/1k [16] |
| **Helicone** | Open source (license not stated on pricing page); gateway-style proxy | On-prem only on Enterprise [17] | Proxy-based | Proxy works for any language | Proxy | Hobby free 10k req; Pro $79; Team $799 |
| **W&B Weave** | Apache-2.0 SDK (Python, TS) [18] | Self-host not documented on repo page | — | None | Native | SaaS-first |
| **OpenLLMetry (Traceloop)** | Apache-2.0; Python + JS; 24+ backends [19] | It is an SDK, not a backend | Emits OTel | `go-openllmetry` is **"early-alpha exploratory"**, 20 commits, manual `LogPrompt/LogCompletion` only [20] | Broad auto-instrumentation | n/a |
| **OpenLIT** | Apache-2.0; **Python, TypeScript, Go SDKs**; ClickHouse + OTel Collector self-host stack; 11 built-in eval types, guardrails, prompt hub, vault [21] | Yes (Docker Compose) | OTel native | Best Go option among LLM-specific SDKs | Yes | Free |
| **MLflow 3** | Apache-2.0 (Linux Foundation); tracing "fully compatible with OpenTelemetry and natively supports GenAI Semantic Conventions"; `mlflow.genai.evaluate()` with Correctness/Guidelines judges, custom scorers, human feedback on traces [22][23] | Yes | Yes | OTel Go | Native autolog for OpenAI/LangChain/DSPy/Pydantic AI | Free |
| **Datadog LLM (now "Agent") Observability** | Proprietary | No; sites are **US1/3/5, EU1, UK1, AP1 (Japan), AP2 (Australia), FED** — **no India site** [24] | SDKs only: **Python, Node.js, Java** (no Go); no OTel path documented on the SDK page [25] | None | Yes | Pricing not published on the public pricing page (custom) [26] |
| **Honeycomb** | Proprietary | No; US and EU endpoints only [27] | OTLP gRPC/HTTP; Go SDK first-class; no GenAI-specific features | Yes (generic OTel) | Yes | Free 20M events/mo; Pro from $150/mo [28] |

### 1.3 Takeaways for a Go core + Python AI plane
- Only **OpenLIT** ships a Go LLM SDK; go-openllmetry is alpha. For Go, the realistic path is the plain OTel Go SDK emitting `gen_ai.*` attributes yourself (a ~200-line helper), exported OTLP/HTTP to Langfuse (or an OTel Collector fan-out to Langfuse + your APM).
- Data residency: Langfuse Cloud, LangSmith Cloud, Datadog, Honeycomb, Braintrust SaaS all lack an India region. **Self-hosting in ap-south-1 is the only way to keep traces (which contain ledger data) in India.** Langfuse and Phoenix are the two that self-host cleanly; MLflow is the fallback.
- Langfuse's OTLP endpoint is HTTP-only; do not plan on gRPC.

---

## 2. Evaluation frameworks and agent evaluation

### 2.1 Frameworks

| Framework | License | Sweet spot | Agent-specific | CI story |
|---|---|---|---|---|
| **promptfoo** | Open source CLI; 20.6k stars | YAML matrix of prompts x providers x assertions (`llm-rubric`, JSON schema, JS); red-team module | Trajectory asserts via custom JS providers | Official **GitHub Action** runs on PRs touching prompt files, posts before/after comment, caches LLM calls in `~/.cache/promptfoo`; Node >= 22 [29][30] |
| **DeepEval** | Apache-2.0; 18.2k stars | "Pytest for LLM apps": G-Eval, DAG (deterministic graph judge), JSON correctness | **Task Completion, Tool Correctness, Goal Accuracy, Step Efficiency, Plan Adherence, Plan Quality** [31] | pytest; sibling **DeepTeam** for red-team |
| **RAGAS** | Apache-2.0; 15.7k stars | RAG metrics, test-set generation; agent_evals template in progress [32] | Partial | Python |
| **Inspect (UK AISI)** | MIT; 2.7k stars | Solvers/scorers/tools, **sandboxes (Docker, K8s, Modal, Proxmox)**, ReAct agents, 200+ prebuilt evals, 20+ providers incl. vLLM/SGLang [33][34] | Strong — designed for agentic evals with sandboxed tools | CLI, eval logs |
| **OpenAI Evals** | MIT; still receiving commits but a registry more than a framework [35] | Reference | Weak | — |
| **Braintrust autoevals** | MIT; Py + TS | Factuality, **JSONDiff, NumericDiff**, LLMClassifier, **Battle (pairwise)** [14] | Building blocks | Braintrust CI |
| **Langfuse evals** | MIT core | Online LLM-as-judge on prod traces, datasets/experiments, annotation queues, "block deployments when regressions are detected" [7] | Score any span | Yes |
| **Phoenix evals** | ELv2 | Judge runs are themselves traced; Py + TS; function-calling extraction of scores [11] | Yes | Yes |
| **Fireworks Eval Protocol** | MIT (python-sdk) | pytest `@evaluation_test`, rollout processors, **MCP-Gym**, Langfuse integration; primary purpose is **RL fine-tuning (RFT) on existing agents** — wrap your agent as an HTTP rollout service, connect to Fireworks RFT/TRL/Unsloth [36][37] | Yes | pytest |
| **MLflow evaluate** | Apache-2.0 | `mlflow.genai.evaluate`, Correctness/Guidelines judges, custom scorers, human feedback [23] | Trace-level | Yes |
| **Giskard** | Apache-2.0; v3 rewrite for **multi-turn agent testing**, OWASP-LLM-mapped scan, Python 3.12+ [38] | Vulnerability scan | Yes | Python |
| **TruLens** | MIT; OTel-native; RAG triad [39] | RAG | Weak | Python |

### 2.2 Agent benchmarks worth borrowing methodology from
- **tau-bench / tau2-bench** (Sierra, MIT): agent + LLM-simulated user + database + policy; success is judged by **comparing the final DB state to an annotated goal state**, not the transcript; **pass^k** measures reliability over k trials. Headline: gpt-4o-era agents <50% success, retail pass^8 <25%. tau2 adds dual-control (user has tools too), telecom and banking domains, voice mode [40][41].
- **ToolSandbox** (Apple): stateful world, implicit tool dependencies, milestone-based scoring that allows multiple valid paths [42].
- **BFCL V4** (Berkeley, updated Apr 2026): AST-based function-call checking, multi-turn, agentic/web-search categories; useful for picking the base model for tool-calling [43].
- **AgentBench FC** (Oct 2025 release, Apache-2.0): 8 environments incl. OS and **DB**, dockerized [44].
- **LLM-as-judge calibration** (Zheng et al.): GPT-4-class judges hit >80% agreement with humans (same as human-human) but exhibit **position, verbosity and self-enhancement bias**; mitigate with position swapping and reference-guided grading [45].

### 2.3 Golden sets for our five surfaces (design)

| Surface | Unit of truth | Metric | Judge type | Source of examples |
|---|---|---|---|---|
| NL -> DSL | Canonicalised DSL AST + result set on a fixture ledger | Exact-AST match; **result-set equality** (rows/aggregates) as the real pass; execution safety (must be read-only, RLS-scoped) | Deterministic | Hand-written 300-500; every user correction ("that's not what I meant" -> re-query) appended |
| Invoice extraction | Field-level JSON | Per-field **precision/recall/F1** with normalisers (GSTIN checksum, date parse, amount to paise); doc-level "all-critical-fields-correct" rate | Deterministic (`JSONDiff`/`NumericDiff` style) | Every human edit in the F9 bill inbox becomes a labelled example |
| GST reconciliation classification | Match label per line (matched / mismatch-amount / mismatch-GSTIN / missing-in-2B / duplicate) | Macro-F1 + confusion matrix; cost-weighted (ITC at stake) | Deterministic | CA-reviewed month-ends |
| Voucher-creation agent | **Ledger state after the run** (tau-bench style): trial balance delta, ledger postings, bill references, tax splits | State equality vs golden; pass^k over k=5; extra: "no unapproved write" invariant | Deterministic on state; LLM rubric only for the explanation text | Simulated-user transcripts + real approved sessions |
| Insight quality | Rendered insight | Human 1-5 usefulness + "would act on it"; pairwise **Battle** vs previous champion | Human + pairwise judge with position swap | Weekly CA panel |

Regression gating in CI (see section 9 for the full design): deterministic suites block merge; judge-based suites block only on a >2-point drop with n >= 200 and a paired bootstrap CI that excludes zero, to avoid flakiness-driven reverts.

Replay of real corrections: every accepted/edited/rejected AI output is stored as `(input snapshot, model output, human final, model_version, prompt_version)`. A nightly job re-runs the current champion on last-30-days corrections; acceptance-rate-on-replay is the single best early-warning metric for silent model drift (section 7).

---

## 3. Guardrails

### 3.1 Landscape and status (important churn in 2025-26)

| Tool | Status Sept 2026 | License | What it gives us |
|---|---|---|---|
| **NVIDIA NeMo Guardrails** v0.24.0 | Active | Apache-2.0 | Input/output/dialog/retrieval/**execution (tool) rails**; Colang 1.0 default, **Colang 2.0 supported**; built-in content/topic safety, jailbreak and hallucination detection; integrations ActiveFence, AlignScore, LangChain [46] |
| **Guardrails AI** | Active but **restructuring**: validators moving to plain PyPI packages; hub remote inference discontinued Aug 25 2026 [47] | Apache-2.0 | Pydantic-driven output guards, `guardrails start` Flask server |
| **Llama Guard 4 (12B)** | Released Apr 5 2025 | Llama 4 Community License | Multimodal 14-category MLCommons hazard classifier [48] — mostly irrelevant hazards for accounting; useful only as a generic input filter |
| **Llama Prompt Guard 2 (22M / 86M)** | Apr 2025 | Llama 4 Community License | Injection/jailbreak classifier; 86M: 99.8% AUC, 97.5% recall @1% FPR, **Hindi among 8 supported languages**; 22M ~19 ms/512 tokens on A100 [49] |
| **IBM Granite Guardian 3.3 8B** | Aug 1 2025 | **Apache-2.0** | Harm + jailbreak + RAG groundedness + **function-calling hallucination (syntax/semantic errors in tool calls)** + custom criteria; thinking mode emits `<score>` [50] |
| **LLM Guard (Protect AI)** | **Archived July 9 2026** | MIT | Scanner list still useful as a checklist; do not adopt [51] |
| **Rebuff** | **Archived May 16 2025** | Apache-2.0 | Canary-token idea worth copying [52] |
| **Lakera Guard** | Active SaaS; "sub-50 ms"; regions/pricing not on public pages (pricing page is JS-only) [53] | Proprietary | Injection/PII/moderation API |
| **Azure Prompt Shields** | Active; user-prompt attacks + **document (indirect) attacks** with sub-classes (manipulated content, fraud, info gathering, availability...); languages incl. English, no Hindi listed [54] | Proprietary | Reference taxonomy for our own indirect-injection classifier on uploaded invoices/emails |
| **Microsoft Presidio** | Moved from Microsoft to **Data Privacy Stack** org (presidio.dataprivacystack.org); ships **IN_PAN, IN_AADHAAR (checksum), IN_GSTIN (15-char with state code + PAN + checksum), IN_VOTER, IN_PASSPORT, IN_VEHICLE_REGISTRATION** recognisers [55] | MIT | PII redaction before traces/prompts leave the tenant boundary |
| **GLiNER / gliner_multi_pii** | Active; Apache-2.0; CPU-friendly zero-shot NER, 100+ languages [56] | Apache-2.0 | Catch free-text PII Presidio's regexes miss (names, addresses); add IFSC/UPI-ID regexes ourselves (`^[A-Z]{4}0[A-Z0-9]{6}$`, `[\w.-]+@[a-z]{3,}`) |

### 3.2 Constrained decoding (output validation at generation time)
- **Fireworks**: `response_format: json_schema` supports most of JSON Schema 2020-12 incl. `anyOf/allOf/oneOf` and recursion; **hallucinated fields are prevented** (schemas with `properties` treated as `unevaluatedProperties:false`); external `$ref` unsupported; `pattern` is best-effort; **Grammar mode** takes BNF; JSON mode is auto-on for tool calling; truncation (`finish_reason=length`) can break validity [57].
- **vLLM** (if we self-host): `structured_outputs` param (old `guided_*` deprecated in v0.12); backends **xgrammar** (default via auto) and **guidance/llguidance**; JSON schema, regex, CFG (e.g. SQL), choice; **structural tags** for tool-call JSON inside free text [58].
- **XGrammar-2** (May 2026, Apache-2.0) "near-zero overhead", default in vLLM/SGLang/TensorRT-LLM [59]. **llguidance** (MIT, Rust): ~50 us/token, powers OpenAI Structured Outputs, in llama.cpp/vLLM/SGLang [60]. **Outlines 1.x** (Apache-2.0): same code over transformers/vLLM/llama.cpp/OpenAI-compatible APIs [61].
- Rule for us: every tool call and every extraction is schema-constrained at decode time **and** re-validated by Pydantic (Python) / a Go JSON-Schema validator before it touches the ledger. Constrained decoding guarantees shape, not truth.

### 3.3 Numeric hallucination controls (non-negotiable for accounting)
1. The LLM never emits a computed money value. It emits **references and operations** (`sum(ledger=X, period=Y)`, `gst(rate=18, taxable=ref#3)`); the Go core computes in integer paise with `decimal`-safe types.
2. Any number in a user-facing narrative must be **bound to a tool result id**; a post-generation "number provenance" check rejects any digit string not present in tool outputs (allowing formatting variants). This is cheap, deterministic, and catches the classic "the model rounded/transposed" failure.
3. Granite Guardian's function-calling-hallucination check runs on tool-call arguments as a second opinion for high-value writes.
4. Cross-footing invariants in the Go core (debits == credits, tax splits sum, ITC <= eligible) reject bad vouchers before approval UI.

---

## 4. Reliability engineering for tool-calling agents

- **Retries with schema repair**: on validation failure, re-prompt with the validator's error path (Pydantic `loc`) and the offending fragment only; cap at 2 repairs then fall back to a smaller/other model or to a human queue. Log every repair as a span event — repair rate is a drift metric.
- **Tool result truncation/summarisation**: hard token budget per tool result (e.g. 2k) with deterministic truncation of tabular data (head + tail + row count + "use `page`"); never let the model summarise financial rows — return aggregates from SQL instead.
- **Context budgeting**: fixed prefix (system + tool schemas) first for KV-cache reuse (Fireworks explicitly recommends static content first, dynamic last) [62]; rolling window over turns; tenant "memory" as retrieved facts, not transcript.
- **Timeouts**: per-tool-call deadlines propagated via Go `context`; whole-run budget; partial results returned with an explicit "incomplete" flag.
- **Idempotency keys for writes**: follow Stripe's model exactly — client-generated UUIDv4 key per intended write, server stores status+body of first attempt (including 5xx) for >= 24 h, replays same result, **errors if parameters differ**, never put PII in keys [63]. For the agent, key = hash(run_id, step_id, canonical tool args).
- **Sagas / compensation**: every write tool must declare its compensating action (`create_voucher` -> `cancel_voucher` with audit trail, not hard delete). The run's saga log lives in Postgres alongside the ledger so a crashed agent can be rolled back by a worker.
- **Dry-run then commit**: all write tools accept `mode=plan|commit`; the plan returns the exact ledger diff (postings, tax lines, bill refs). Approval gate shows the diff; `commit` replays the plan by id (not by regenerating), which also removes non-determinism between what the human approved and what executes.
- **Sandboxing analysis code** (if the agent writes pandas/DuckDB over tenant data):

| Option | Isolation | Cost | India region? | Notes |
|---|---|---|---|---|
| **E2B** | Firecracker microVMs; infra repo Apache-2.0, self-host GCP (stable) / AWS (beta) via Terraform [64] | $0.000014/vCPU-s + $0.0000045/GiB-s (~$0.115/h for 2 vCPU/4 GiB); Hobby 1 h sessions, Pro 24 h [65] | Not listed | Self-hosting needs KVM => EC2 `.metal` in ap-south-1 |
| **Modal Sandboxes** | gVisor-based containers | Sandbox tier $0.00003942/core-s + $0.00000667/GiB-s; **`ap-south` (Mumbai) region exists at 1.75x** [66][67] | **Yes** | Easiest managed option with Indian residency |
| **Daytona** | "dedicated kernel" microVM-style; SDKs Py/TS/Go/Ruby/Java; **core dev moved to a private codebase June 2026**, public repo frozen [68] | $0.0504/vCPU-h + $0.0162/GiB-h; $200 free [69] | Not stated | Vendor-lock risk rose in 2026 |
| **Firecracker** (DIY) | KVM microVM, <125 ms boot, <5 MiB overhead, Apache-2.0 [70] | EC2 bare-metal | Yes | Highest control, highest ops cost |
| **gVisor** (`runsc`) | User-space application kernel in Go; OCI runtime; higher syscall overhead, Apache-2.0 [71][72] | Runs on normal EKS nodes | Yes | Good default for read-only DuckDB analysis pods |

Recommendation: gVisor-runtime pods on EKS in ap-south-1 for read-only analysis (data mounted as tenant-scoped Parquet exports, no network), Modal `ap-south` as burst/overflow.

- **Deterministic replay**: persist per step `(prompt hash, model id, temperature=0/seed, tool args, tool result hash)`. Replay runs against **recorded tool results** (like VCR cassettes), so debugging a bad voucher never needs live inference or live ledger access. Inspect's eval-log format and Eval Protocol's rollout traces are good references for the shape.

---

## 5. Cost control

### 5.1 Provider facts (open-weight)
- **Fireworks prompt caching**: on by default for all models/deployments, automatic longest-prefix match; serverless cached tokens ~**10% of input price** on the current price table (docs say "typically 50%, varies by model" in the guide, but the Sept 2026 price table lists cached-input at ~10%); dedicated deployments: cached tokens "close to free"; cache lives minutes to hours; headers `fireworks-cached-prompt-tokens` on dedicated [62][73].
- **Fireworks serverless prices (Sept 2026, per 1M tokens, Standard tier)**: gpt-oss-120b $0.15 in / $0.60 out; Qwen 3.7 $0.40/$1.60; DeepSeek V4 Flash $0.22/$0.66; DeepSeek V4 Pro $1.32/$3.96; GLM 5.2 $2.10/$6.60; Kimi K2.6 $0.95/$4.00; Kimi K3 $3.00/$15.00. Priority tier = 1.25-1.5x [73]. Embeddings $0.008-0.016/1M; Qwen3-8B embed $0.10/1M. On-demand H100 $8/h, B200 $13/h from Sept 1 2026; LoRA SFT $0.50/1M tokens (<=16B) [74].
- **Fireworks Batch**: **50% off serverless**, plus caching discounts; 12/24/48/72 h windows; any model with on-demand support [75].
- **Together**: batch "up to 50% off" but only two models get the full discount; prompt-caching page not found (404) [76]. **DeepInfra**: no prompt-caching documented [77].
- **Versioning risk**: Fireworks serverless models are "best-effort, no SLA", may be discontinued with **at least 2 weeks notice**; model IDs are `accounts/fireworks/models/<id>` and docs do not promise immutability of weights behind an ID [78][79]. Mitigation in section 7.
- **Data residency**: Fireworks regions are US (many), EU (Frankfurt, Iceland), APAC (**Malaysia, NSW, Tokyo**) — **no India**; only a "US-only residency" dedicated option is documented; zero retention of prompts for open models unless opted in; SOC 2 II, HIPAA, ISO 27001/27701/**42001** [80][81]. Inference therefore leaves India; DPDP does not forbid cross-border transfer by default (see section 8), but redact PII (section 3) and disclose in the privacy notice.

### 5.2 Caching, routing, budgets
- **Semantic caching**: Redis **LangCache** (managed on Redis Cloud, REST API, BYO embeddings; claims up to 90% API savings, a customer at 70% hit rate) [82]; **GPTCache** (MIT, Zilliz) still maintained-ish, modular [83]. For us, exact-match caching keyed on `(tenant, normalised NL, ledger snapshot version)` beats semantic caching: accounting questions have low paraphrase variety per tenant and high correctness stakes (a semantic hit that returns last month's number is a defect).
- **Routing/cascades**: **RouteLLM** (Apache-2.0): mf/bert/causal_llm/sw_ranking routers, "up to 85% cost reduction at 95% GPT-4 quality", trainable on your own preference pairs [84]. **Not Diamond**: $0.05/1M tokens routed, RL-tuned on your feedback, +100-150 ms [85]. **OpenRouter**: price/throughput/latency sort, `zdr` and `data_collection=deny` filters, provider `order`/`only` pinning [86]. **Martian** has pivoted to interpretability research [87]. Our cascade should be rule-first (task type -> model class), then a learned router only for the open-ended chat path.
- **Per-tenant token budgets**: monthly budget in the Go core (plan tier x multiplier), enforced pre-call by a `reserve(tokens_est)` -> `settle(tokens_actual)` pattern with degraded modes (smaller model, disable insights) rather than hard failures.
- **Cost attribution**: every span carries `tenant_id`, `feature` (nl_query, extraction, recon, agent, insight), `model_id`, `prompt_version`; Langfuse computes cost from token usage with a model price table you maintain (needed anyway for open models).

### 5.3 Cost envelope per tenant

Assumptions: Fireworks Standard serverless, prompt-cache hit on the ~3k-token static prefix, PII redaction local, small model for classification, mid model for agent, exchange rate INR 88/USD (assumption). Token estimates are engineering estimates, not vendor data.

| Feature | Volume / month (typical SMB) | Tokens in/out per op | Model class | USD / month |
|---|---|---|---|---|
| Invoice extraction (vision or OCR+LLM) | 150 docs | 2.5k / 0.6k | gpt-oss-120b-class $0.15/$0.60 | 150 x (2.5k x 0.15 + 0.6k x 0.60)/1M = **$0.11** |
| NL queries | 300 | 4k (3k cached) / 0.3k | Qwen 3.7-class $0.40/$1.60, cached ~$0.04 | 300 x (1k x 0.40 + 3k x 0.04 + 0.3k x 1.60)/1M = **$0.30** |
| Agent runs (multi-step, 6 calls avg) | 60 runs x 6 = 360 calls | 6k (4k cached) / 0.5k | Qwen 3.7-class | 360 x (2k x 0.40 + 4k x 0.04 + 0.5k x 1.60)/1M = **$0.63** |
| GST recon classification | 2 runs x 400 lines, batched 20/line-call | 40 calls x 3k / 1k | gpt-oss-120b via **Batch (50% off)** | ~**$0.03** |
| Proactive insights | 30 | 8k / 0.6k | Qwen 3.7-class via Batch | 30 x (8k x 0.20 + 0.6k x 0.80)/1M = **$0.06** |
| Guardrail passes (Prompt Guard 2 22M, Granite 8B on writes) | 60 write checks | 2k / 0.1k | Granite 8B on Fireworks small-model tier (~$0.20/1M assumed) | **$0.03** |
| Embeddings (docs + memory) | 500k tokens | — | $0.016/1M | **$0.01** |
| Judge evals sampled online (5%) | ~40 | 4k / 0.3k | mid model | **$0.08** |
| **Total inference** | | | | **~$1.25 (~INR 110)** typical; heavy tenant (3x) ~$3.75 (~INR 330) |
| Observability (self-hosted Langfuse share) | EKS Fargate + Aurora Serverless v2 + ClickHouse ~ $600-900/mo cluster amortised over 1,000 tenants | | | **$0.60-0.90** |
| Sandbox compute (Modal ap-south, 10 min/tenant/month) | | | | **~$0.05** |

At INR 500-2000/month pricing, AI COGS land at roughly 6-20% of revenue for a typical tenant and 20-60% for a heavy one at INR 500 — which argues for (a) token budgets tied to tier, (b) Batch for everything non-interactive, and (c) fine-tuning a small model (LoRA SFT $0.50/1M training tokens) for extraction and recon once the golden sets exist, since those two are the volume drivers as tenants scale document counts.

---

## 6. Security of the agent stack

- **OWASP Top 10 for LLM Applications 2025**: LLM01 Prompt Injection, LLM02 Sensitive Information Disclosure, LLM03 Supply Chain, LLM04 Data and Model Poisoning, LLM05 Improper Output Handling, LLM06 Excessive Agency, LLM07 System Prompt Leakage, LLM08 Vector and Embedding Weaknesses, LLM09 Misinformation, LLM10 Unbounded Consumption [88].
- **OWASP Agentic AI — Threats and Mitigations** (Feb 17 2025): T1 memory poisoning, T2 tool misuse, T3 privilege compromise, T4 resource overload, T5 cascading hallucination, T6 intent breaking, T7 misaligned behaviours, T8 repudiation, T9 identity spoofing, T10 overwhelming HITL, T11 unexpected RCE, T12 agent communication poisoning, T13 rogue agents, T14 human attacks on multi-agent, T15 human manipulation [89]. **OWASP Top 10 for Agentic Applications 2026** published Dec 9 2025 [90]; the ASI01-ASI10 names (Agent Goal Hijack, Tool Misuse, Identity & Privilege Abuse, Agentic Supply Chain, Unexpected Code Execution, Memory/Context Poisoning, Insecure Inter-Agent Communication, Cascading Failures, Human-Agent Trust Exploitation, Rogue Agents) are **unverified recall** — the fetched landing page did not enumerate them.
- **MITRE ATLAS** version **2026.05** (data format 6.0.0), YAML in `atlas-data`, techniques `AML.T####`; MITRE copyright, approved for public release [91]. Use it as the taxonomy for red-team findings.
- **MCP security (spec 2025-11-25 best practices)**: confused-deputy via static client ID + dynamic registration + consent cookies -> **per-client consent registry, exact redirect_uri match, single-use state**; **token passthrough forbidden** ("MUST NOT accept tokens not issued for the MCP server"); SSRF during OAuth discovery -> block private ranges/169.254.169.254, egress proxy; **sessions MUST NOT be used for authentication**, bind session IDs as `<user_id>:<session_id>`; **scope minimisation** with incremental `WWW-Authenticate scope=` elevation [92]. **Tool poisoning / rug pulls / cross-server shadowing** (Invariant Labs): hidden instructions in tool descriptions; mitigations: show full descriptions, **pin tool definitions by hash**, isolate data flow between servers [93]. For us: all tools are first-party; still pin the tool manifest hash in each run's audit record and diff on deploy.
- **Supply chain of weights/LoRA**: pin model by ID **and** record the provider's reported model fingerprint/version per response; store LoRA adapters in our S3 with SHA-256 manifests; only load from our bucket (Fireworks BYOB supports S3) [81].
- **Secrets in prompts**: never; tools receive a per-run capability token (short-lived, tenant + scope bound) and resolve secrets server-side. Run Presidio + a secrets regex scanner on outbound prompts as a tripwire.
- **Tenant isolation in vector stores**: pgvector under the same **RLS policies** as the ledger (`tenant_id` column, `SET LOCAL app.tenant`), embeddings computed per tenant, and — the subtle one — **no cross-tenant ANN index sharing for the same model**; partition by tenant or include tenant in the index key.
- **Audit immutability for AI actions**: append-only `ai_action_log` (INSERT-only role, no UPDATE/DELETE grants), hash-chained rows (`prev_hash`), nightly anchor of the chain head to S3 Object Lock (compliance mode). Store prompt hash, model id, tool args, plan diff, approver id, approval timestamp.
- **Model version pinning and change management**: model/prompt/guardrail versions are a deployable "AI release" with its own eval report; changes go through the CI gate (section 9); provider-forced changes are detected by shadow runs (section 7).
- **Red-team tooling**: **PyRIT** moved to `microsoft/PyRIT` (Azure/PyRIT archived Mar 27 2026; MIT; 4.4k stars) [94][95]; **garak** (NVIDIA, Apache-2.0, 9.1k stars) probes incl. injection, DAN, leak replay, package hallucination; REST generator fits our FastAPI [96]; **promptfoo red-team** [29]; **DeepTeam** (Apache-2.0): 50+ vulnerabilities, 20+ attacks incl. multi-turn, **maps to OWASP LLM 2025 and OWASP Agents 2026**, agentic attacks (goal redirection, tool orchestration abuse) [97].

---

## 7. Data quality and drift

- **Metrics per tenant and per feature**: accept rate, edit rate (with edit distance on structured fields), reject rate, schema-repair rate, tool-error rate, time-to-approve, and "reverted within 7 days" for agent-written vouchers. Emit as Langfuse scores + Prometheus metrics; alert on CUSUM/EWMA shifts, not fixed thresholds (tenants differ).
- **Provider silent updates**: Fireworks documents 2-week deprecation notice for serverless and no immutability promise for IDs [78][79]. Defences: (1) nightly **canary suite** (200 deterministic cases: NL->DSL exact match, extraction F1 on a frozen 100-doc set) against every production model ID; alert on any delta beyond historical noise; (2) record any provider-side version/fingerprint fields per response and alert on change; (3) for the highest-stakes path (voucher agent), a **dedicated/on-demand deployment** of a pinned checkpoint is the only real guarantee — price it in once volume justifies (H100 $8/h).
- **Shadow mode**: new prompt/model runs on a sample of live traffic, outputs stored but not shown; compare against champion on the state-based and deterministic metrics. **Champion/challenger for fine-tunes**: challenger must beat champion on replayed corrections (section 2.3) with a paired test, then ramp 5% -> 25% -> 100% by tenant cohort with automatic rollback on accept-rate drop.
- **Feedback capture UX**: every AI artefact has a three-state control (accept / edit / reject-with-reason) that is *the* action (not an extra survey). Edits are diffed automatically into labelled examples; "reject reasons" are a small fixed taxonomy (wrong ledger, wrong amount, wrong tax, wrong period, unsafe). Langfuse annotation queues for weekly CA review of a 2% sample.

---

## 8. Compliance artefacts

**What a CA/auditor will ask for** (practically): per AI-originated voucher — who approved, what they saw (plan diff), which model/prompt version, the inputs; an exportable "AI action register"; evidence that the human approval gate cannot be bypassed; a statement on where data was processed (inference outside India); model cards for fine-tuned models; a DPIA if we are (or resemble) a Significant Data Fiduciary.

- **DPDP Act 2023 / DPDP Rules 2025**: Rules notified **14 Nov 2025**; phased commencement (Board and procedural provisions immediately; remaining obligations over 12-18 months — the fetched summary gives Nov 2026 and May 2027 milestones); SDF obligations include **DPIA, annual independent audit, algorithmic due diligence**, and possible localisation directions; consent managers; breach notice to the Board and affected principals within specified timelines (the "72-hour detailed report" figure is **unverified recall**); penalties up to **INR 250 crore** [98][99]. Note MeitY's own site returned 403 from this environment (consistent with the geo-fencing noted in memory).
- **ISO/IEC 42001:2023**: first AI management system standard; clauses 4-10 plus Annex A controls, Annex B implementation guidance, Annex C objectives/risk sources, Annex D domains; certifiable, structured like ISO 27001 [100]. Fireworks already holds 42001 [81], which helps our vendor-risk narrative.
- **NIST AI RMF 1.0** (Govern/Map/Measure/Manage) + **Generative AI Profile NIST AI 600-1** (July 26 2024); RMF revision under way in 2026 [101]. Use as the internal control taxonomy; map each section-9 control to a Measure/Manage subcategory.
- **SOC 2 for AI features**: no fetched primary source; practical approach is to bring the AI plane into the existing SOC 2 scope with change-management (eval gate), logical access (capability tokens), and monitoring (drift alerts) as the controls.
- **EU AI Act**: extraterritorial when output is used in the EU; **credit-scoring is Annex III high-risk**, bookkeeping is not; GPAI obligations from Aug 2 2025; Annex III high-risk from Dec 2 2027 per the fetched summary; Digital Omnibus page unavailable (404) [102]. Relevance for an Indian SMB SaaS is low unless we add lending/credit features or sell to EU-based deployers.

---

## 9. Recommendations for our build

### 9.1 Stack

| Layer | Choice | Where |
|---|---|---|
| Tracing backend | **Langfuse v3 self-hosted** via the official AWS Terraform module (EKS Fargate, Aurora Serverless v2, ElastiCache, S3, in-cluster ClickHouse) | ap-south-1 |
| Instrumentation | Go: OTel Go SDK + thin `gen_ai.*` helper -> OTLP/HTTP; Python: Langfuse SDK (or OpenLIT) -> same project; OTel Collector fan-out to Grafana/Tempo for infra | in-cluster |
| Offline evals | **DeepEval** (pytest, agent metrics) + **promptfoo** (prompt matrices, GitHub Action) + **Inspect** for sandboxed agent sims (tau-bench-style with a simulated user) | CI runners in ap-south-1 |
| Online evals | Langfuse LLM-as-judge on 5% sample + annotation queues | same cluster |
| Structured output | Fireworks `json_schema` + Pydantic/Go re-validation; vLLM+xgrammar if we self-host | — |
| Input guard | Prompt Guard 2 86M (Hindi support) on CPU in the FastAPI plane; Azure-Prompt-Shields-style indirect-injection classifier on uploaded documents (own fine-tune later) | in-cluster |
| Output/tool guard | Granite Guardian 3.3 8B (function-call hallucination) on write plans; deterministic ledger invariants in Go | Fireworks small tier / in-cluster |
| PII | Presidio (IN_* recognisers) + GLiNER-PII + custom IFSC/UPI regex, applied before traces and before any egress | in-cluster |
| Sandbox | gVisor runtime class on EKS; Modal `ap-south` overflow | ap-south-1 |
| Red team | garak + DeepTeam quarterly; PyRIT for multi-turn; promptfoo red-team in CI weekly | CI |
| Governance | NIST AI RMF mapping; ISO 42001 readiness by month 12; DPIA template | — |

### 9.2 CI eval gate
1. **Tier 0 (every PR, < 5 min, no LLM)**: schema/DSL unit tests; ledger-invariant tests; replay of 500 recorded agent runs against cassettes (deterministic; asserts tool-arg equality and final-state equality).
2. **Tier 1 (PRs touching prompts/models/tools, ~15 min)**: promptfoo matrix on NL->DSL (exact AST + result-set) and extraction F1 (frozen 100 docs); DeepEval Tool Correctness / Task Completion on 100 sim tasks; **hard gates**: exact-match >= champion - 0.5 pt; F1 >= champion - 0.5 pt; zero unapproved-write violations; zero injection-suite bypasses.
3. **Tier 2 (nightly)**: Inspect tau-style simulation, pass^5 on 200 tasks; LLM-judge insight quality pairwise vs champion with position swap (n=200, paired bootstrap); garak quick probes. Failures open a ticket and block the weekly release train, not individual merges.
4. **Tier 3 (weekly/monthly)**: full red-team (DeepTeam, OWASP mapping), replay of last-30-days corrections, drift report per tenant cohort.
Every gate writes its report to Langfuse experiments and attaches the report id to the "AI release" record.

### 9.3 Guardrail pipeline (text diagram)

```
user text / uploaded doc
  -> [PII detect+redact: Presidio IN_* + GLiNER]  (keeps mapping for de-redaction in-tenant)
  -> [Prompt Guard 2 (user text) | indirect-injection classifier (doc text)] --block/flag-->
  -> [tenant budget reserve + cache lookup (exact, snapshot-versioned)]
  -> [LLM call: static prefix (system+tools) | dynamic tail; json_schema constrained]
  -> [schema validate (Pydantic/Go) --repair x2--> fallback model/human queue]
  -> tool call?  -> [scope check vs capability token] -> [Granite Guardian fn-call check on writes]
                 -> [Go core: plan mode -> ledger diff; invariants: Dr=Cr, tax sums, ITC caps]
                 -> [approval gate UI shows diff] -> commit(plan_id, idempotency_key) -> saga log
  -> narrative?  -> [number-provenance check: every digit bound to a tool result]
                 -> [de-redact PII in-tenant] -> user
  every hop -> OTel span (gen_ai.*) -> Langfuse (ap-south-1) -> 5% judge sample + scores
```

### 9.4 Cost envelope (summary)

| Tenant profile | Inference | Observability + sandbox | Total USD | Total INR (@88) | % of INR 500 plan | % of INR 2000 plan |
|---|---|---|---|---|---|---|
| Light (50 docs, 100 queries) | $0.45 | $0.65 | $1.10 | ~INR 97 | 19% | 5% |
| Typical (150 docs, 300 queries, 60 agent runs) | $1.25 | $0.70 | $1.95 | ~INR 172 | 34% | 9% |
| Heavy (500 docs, 1,000 queries, 200 runs) | $3.75 | $0.80 | $4.55 | ~INR 400 | 80% | 20% |

Levers in order of impact: Batch for recon/insights (50%), prefix caching (already ~90% on cached input), small fine-tuned model for extraction, per-tier token budgets, dedicated deployment only once > ~$8/h of serverless spend/hour is sustained.

### 9.5 Incident and rollback playbook (outline)
1. **Detect**: canary suite delta, accept-rate CUSUM alarm, judge-score drop, guardrail block spike, provider status/fingerprint change, or a user-reported wrong posting.
2. **Triage (15 min)**: classify — model regression / prompt regression / tool bug / provider outage / security (injection, exfil). Pull the run's deterministic replay from cassettes; confirm reproducibility offline.
3. **Contain**: feature flags per surface (`ai.agent.write=off` keeps read-only NL; `ai.insights=off`); switch model ID to the pinned previous champion; raise approval requirement to "all writes" for affected tenants; for security incidents rotate capability-token signing key.
4. **Assess blast radius**: query `ai_action_log` for all commits by the affected model/prompt version in the window; compute ledger impact per tenant; list vouchers already included in filed returns (GSTR-1/3B) separately.
5. **Remediate**: compensating vouchers via saga log (never delete); notify affected tenants with the diff; if personal data was exposed, start the DPDP breach clock immediately (Board + principals).
6. **Recover**: re-run Tier 1 + Tier 2 gates on the rollback build; ramp back 5% -> 25% -> 100%.
7. **Learn**: add the failing cases to the golden set; write the postmortem into the AI release record; map the root cause to an OWASP/ATLAS ID for the risk register.

---

## Sources (all fetched 2026-09-08)

1. https://opentelemetry.io/docs/specs/semconv/gen-ai/
2. https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/README.md
3. https://github.com/langfuse/langfuse
4. https://langfuse.com/self-hosting
5. https://github.com/langfuse/langfuse-terraform-aws
6. https://langfuse.com/docs/opentelemetry/get-started
7. https://langfuse.com/docs/evaluation/overview
8. https://langfuse.com/pricing
9. https://github.com/Arize-ai/phoenix
10. https://arize.com/docs/phoenix/self-hosting
11. https://arize.com/docs/phoenix/evaluation/llm-evals
12. https://docs.langchain.com/langsmith/architectural-overview
13. https://www.langchain.com/pricing
14. https://github.com/braintrustdata/autoevals
15. https://www.braintrust.dev/docs/guides/self-hosting
16. https://www.braintrust.dev/pricing
17. https://www.helicone.ai/pricing
18. https://github.com/wandb/weave
19. https://github.com/traceloop/openllmetry
20. https://github.com/traceloop/go-openllmetry
21. https://github.com/openlit/openlit
22. https://mlflow.org/docs/latest/genai/tracing/
23. https://mlflow.org/docs/latest/genai/eval-monitor/
24. https://docs.datadoghq.com/getting_started/site/
25. https://docs.datadoghq.com/llm_observability/instrumentation/sdk/
26. https://www.datadoghq.com/pricing/
27. https://docs.honeycomb.io/send-data/opentelemetry/
28. https://www.honeycomb.io/pricing
29. https://www.promptfoo.dev/docs/intro/
30. https://www.promptfoo.dev/docs/integrations/github-action/
31. https://github.com/confident-ai/deepeval
32. https://github.com/explodinggradients/ragas
33. https://inspect.aisi.org.uk/
34. https://github.com/UKGovernmentBEIS/inspect_ai
35. https://github.com/openai/evals
36. https://evalprotocol.io/
37. https://github.com/eval-protocol/python-sdk
38. https://github.com/Giskard-AI/giskard
39. https://github.com/truera/trulens
40. https://github.com/sierra-research/tau2-bench
41. https://arxiv.org/abs/2406.12045
42. https://github.com/apple/ToolSandbox
43. https://gorilla.cs.berkeley.edu/leaderboard.html
44. https://github.com/THUDM/AgentBench
45. https://arxiv.org/abs/2306.05685
46. https://github.com/NVIDIA/NeMo-Guardrails
47. https://github.com/guardrails-ai/guardrails
48. https://huggingface.co/meta-llama/Llama-Guard-4-12B
49. https://huggingface.co/meta-llama/Llama-Prompt-Guard-2-86M
50. https://huggingface.co/ibm-granite/granite-guardian-3.3-8b
51. https://github.com/protectai/llm-guard
52. https://github.com/protectai/rebuff
53. https://www.lakera.ai/lakera-guard
54. https://learn.microsoft.com/en-us/azure/ai-services/content-safety/concepts/jailbreak-detection
55. https://presidio.dataprivacystack.org/supported_entities/
56. https://github.com/urchade/GLiNER
57. https://docs.fireworks.ai/structured-responses/structured-response-formatting
58. https://docs.vllm.ai/en/latest/features/structured_outputs.html
59. https://github.com/mlc-ai/xgrammar
60. https://github.com/guidance-ai/llguidance
61. https://github.com/dottxt-ai/outlines
62. https://docs.fireworks.ai/guides/prompt-caching
63. https://docs.stripe.com/api/idempotent_requests
64. https://github.com/e2b-dev/infra
65. https://e2b.dev/pricing
66. https://modal.com/pricing
67. https://modal.com/docs/guide/region-selection
68. https://github.com/daytonaio/daytona
69. https://www.daytona.io/pricing
70. https://firecracker-microvm.github.io/
71. https://gvisor.dev/docs/
72. https://github.com/google/gvisor
73. https://docs.fireworks.ai/serverless/pricing
74. https://fireworks.ai/pricing
75. https://docs.fireworks.ai/guides/batch-inference
76. https://docs.together.ai/docs/batch-inference
77. https://docs.deepinfra.com/
78. https://docs.fireworks.ai/models/overview
79. https://docs.fireworks.ai/deployments/regions
80. https://docs.fireworks.ai/deployments/regions
81. https://docs.fireworks.ai/guides/security_compliance
82. https://redis.io/langcache/
83. https://github.com/zilliztech/GPTCache
84. https://github.com/lm-sys/RouteLLM
85. https://www.notdiamond.ai/pricing
86. https://openrouter.ai/docs/features/provider-routing
87. https://withmartian.com/
88. https://genai.owasp.org/llm-top-10/
89. https://genai.owasp.org/resource/agentic-ai-threats-and-mitigations/
90. https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications/
91. https://github.com/mitre-atlas/atlas-data
92. https://modelcontextprotocol.io/specification/2025-06-18/basic/security_best_practices (served the 2025-11-25 revision)
93. https://invariantlabs.ai/blog/mcp-security-notification-tool-poisoning-attacks
94. https://github.com/Azure/PyRIT (archived notice)
95. https://github.com/microsoft/PyRIT
96. https://github.com/NVIDIA/garak
97. https://github.com/confident-ai/deepteam
98. https://en.wikipedia.org/wiki/Digital_Personal_Data_Protection_Act,_2023
99. https://en.wikipedia.org/wiki/Digital_Personal_Data_Protection_Rules,_2025
100. https://www.a-lign.com/articles/understanding-iso-42001
101. https://www.nist.gov/itl/ai-risk-management-framework
102. https://artificialintelligenceact.eu/high-level-summary/

Pages attempted but unavailable from this environment: iso.org/standard/42001 (403), meity.gov.in/data-protection-framework (403), atlas.mitre.org (empty/404), docs.fireworks.ai/guides/deprecations and /models/deprecations (404), docs.together.ai/docs/prompt-caching (404), platform.lakera.ai/pricing (JS-only), artificialintelligenceact.eu/digital-omnibus (404), weave-docs.wandb.ai (403).
