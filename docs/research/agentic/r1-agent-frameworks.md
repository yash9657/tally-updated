# R1 — Agent frameworks & orchestration for a production ERP agent (state as of 2026-09-08)

Context: Go "cored" monolith on PostgreSQL 16 (RLS per tenant, hash-chained voucher log), Python/FastAPI "insightd" AI plane, React keystroke UI. Goal: an agent that can do everything a human can (vouchers, masters, reports, bank reco, GST filing, reminders, config, import/export) behind approval gates, plus proactive insights, using cheap open-weight models served OpenAI-compatibly (Fireworks). All facts below carry a source number → see Sources.

---

## 1. LangChain / LangGraph in 2026

**Where it stands.** LangChain 1.0 and LangGraph 1.0 shipped 2025-10-22 with a "no breaking changes until 2.0" pledge; Python 3.10+ and JS; the ecosystem claims ~90M monthly downloads and production use at Uber, LinkedIn, Klarna [1]. LangGraph repo: 41.3k stars, MIT, self-described "low-level orchestration framework for building stateful agents" [2]. LangChain 1.0 collapsed the old zoo into `create_agent` + a **middleware** system (HITL approval, summarization, PII redaction are middleware) [1].

**Durable execution / checkpointers.** State is checkpointed after every node ("super-step"); a run is addressed by `thread_id`. Checkpointers: `InMemorySaver` (dev), `SqliteSaver`, `PostgresSaver`, `RedisSaver`, `MongoDBSaver` [3]. This is *state* persistence, not a workflow engine: the process that was running the graph must still be alive or be re-invoked; nothing schedules the resume for you unless you run LangGraph Server/Platform.

**Interrupts (HITL).** `interrupt(payload)` raises a special exception, state is saved, and the caller later re-invokes with `Command(resume=value)`; the resume value is returned from the `interrupt()` call. Critical semantics: **on resume the whole node re-executes from its start**, so anything before the interrupt must be idempotent or moved after it (the docs' own example: a DB insert before `interrupt()` duplicates on resume). Don't wrap `interrupt()` in bare try/except; don't conditionally skip interrupts inside one node (index-based matching) [3]. Patterns: approve/reject (bool), review-and-edit (return edited args), interrupt inside the tool function itself [3].

**Agent Inbox / ambient agents.** LangChain's "ambient agents" thesis: agents triggered by events, not chats, surfacing to humans only when needed, via an email-like **Agent Inbox** [4]. Agent Inbox is an open-source (MIT, 1.1k stars) Next.js UI that consumes a `HumanInterrupt` schema — `action_request{action,args}`, `config{allow_accept, allow_edit, allow_respond, allow_ignore}`, `description` — and requires a LangGraph Server/Platform deployment to talk to [5]. The schema is worth copying even if you never run the UI.

**LangMem.** Long-term memory SDK (semantic/episodic/procedural), early 2025; ~746K monthly PyPI downloads as of mid-2026 [6].

**Deep Agents.** `deepagents` (29.2k stars, MIT) is LangChain's "batteries-included agent harness": `write_todos` planning, virtual filesystem (read/write/edit/search), sub-agents with isolated context, skills, shell, thread summarization, persistent memory, and HITL "approve, edit, or reject tool calls before they run" via `interrupt_on`. Explicitly model-agnostic: "works with any LLM that supports tool calling: frontier, open-weight, or local" incl. vLLM/Ollama [7][8].

**Temporal plugin (new, July 2026).** Temporal's LangGraph plugin (Public Preview) runs each node as a Temporal Activity with checkpoints at every node, and turns `interrupt()` into a durable Temporal signal wait — "no process remains active and no compute costs accrue during the wait." Python only; Temporal Python SDK ≥1.27; supports StateGraph, Functional API, `Send`, continue-as-new [9].

**Verdict.** LangGraph is still the default durable-*graph* library in Python, and 1.0 stabilised it. Its weakness for our case is the same as always: it is a library with a checkpoint store, not a scheduler; multi-day approval waits are the caller's problem unless you buy LangGraph Platform or bolt on Temporal/Restate/DBOS.

---

## 2. Alternatives (Python / TS / .NET)

- **Pydantic AI** — 19.8k stars, MIT; typed end-to-end; *first-party, co-maintained durable execution on Temporal, DBOS, Prefect, Restate and AWS Lambda Durable Functions* (plus external Kitaru, Airflow); built-in HITL via **deferred tools** (`DeferredToolRequests` → approve/deny → resume with matching tool-call IDs, and "validation runs before approval and deferral, so rejected arguments never reach an approver"); MCP, AG-UI, A2A/ACP (experimental) support; `pydantic-graph` when control flow isn't enough [10][11][12]. Provider list covers OpenAI/Anthropic/Google/Bedrock/Groq/Mistral/xAI/Ollama; any OpenAI-compatible base URL (Fireworks) works through the OpenAI model class.
- **OpenAI Agents SDK** — 29.3k stars, MIT; handoffs, guardrails, sessions, tracing, built-in HITL. Provider-agnostic via Chat Completions + LiteLLM/any-llm ("100+ LLMs") [13]. HITL: tools with `needs_approval`, run pauses with an interruption; `pre_approval_tool_input_guardrails` option runs input guardrails before emitting the approval interrupt; `alwaysApprove/alwaysReject` for the rest of a run [14]. Durable via Temporal (official, Python and TS: agent loop in Workflow, model calls as Activities) [15][16], DBOS, Restate.
- **Microsoft Agent Framework** — 1.0 GA 2026-04-03 (Python + .NET); unification of Semantic Kernel + AutoGen; graph workflows with type-safe routing, **checkpointing/resumability**, HITL via `RequestPort` and approval-required tools, OpenTelemetry. AutoGen v0.4 and Semantic Kernel are now maintenance-mode [17][18]. AG2 (community AutoGen fork) is a research niche.
- **Google ADK** — Python/Java/Go; **ADK Go 1.0 (2026-03-31)**: tools flagged `RequireConfirmation: true` pause for human approval, native OTel, "Retry and Reflect" plugin, A2A across Go/Java/Python [19]. **ADK Go 2.0 (2026-06-30)**: graph workflow engine (function/agent/tool/join/dynamic nodes), `RequestInputEvent` for HITL, durable resume by "scanning session history", `google.golang.org/adk/v2` [20]. Caveat: Go ADK is Gemini-first; OpenAI-compatible providers are a community adapter / open issue #596 (Feb 2026) [21]. Restate ships an ADK plugin [22].
- **Claude Agent SDK** — the Claude Code harness as a library (Python 3.10+/TS); six permission modes, `canUseTool` callback, hooks (PreToolUse/PostToolUse/PermissionRequest/SubagentStart…), MCP, subagents. Fixed evaluation order hooks → deny → ask → mode → allow → callback [23]. Anthropic-model-only — not usable with Fireworks.
- **CrewAI** — 54k+ stars; Crews (role-play) + Flows (deterministic, state, HITL triggers, resumable); AMP Enterprise adds SOC2/SSO/audit [24]. Weakest on typed tool contracts; HITL is coarse.
- **LlamaIndex Workflows 1.0 / AgentWorkflow** — event-driven typed steps; HITL by emitting an `InputRequiredEvent` and waiting; checkpointing is weaker than LangGraph's and the docs themselves position LangGraph as the better fit for "checkpointing, replay, and human-in-the-loop pauses at specific points" [25][26].
- **smolagents (HF)** — 27.8k stars, v1.26.0 (2026-05-29); `CodeAgent` writes Python actions, `ToolCallingAgent` emits JSON; HF's own guidance: code agents "reduce steps and LLM calls by about 30%" [27]. No durability, thin HITL.
- **DSPy** — ~34k stars, MIT; 3.3.x line (3.3.1, GEPA 0.1.4); `ReActV2` uses structured messages and native tool calls; GEPA can optimise both the reasoning and synthesis predictors of ReAct; experimental `Flex` module lets GEPA rewrite control flow [28][29]. Best used as an *optimiser* for prompts/tool descriptions on open models, not as the runtime.
- **Mastra (TS)** — 27.8k stars, Apache-2.0 core + EE dirs; workflows with `.then/.branch/.parallel`, **suspend/resume with storage** (pause indefinitely), 40+ providers, can author MCP servers [30].
- **Vercel AI SDK 6** (May 2026) — `ToolLoopAgent`, stable MCP, `needsApproval: true` on any tool pauses the loop [31]; durable via Temporal or Restate `durableCalls()` [16][22].
- **Agno (ex-Phidata)** — runtime-first (AgentOS FastAPI runtime); **Letta** (ex-MemGPT) — memory-centric; **Haystack** — DAG pipelines for search/RAG, not an agent runtime [32].

### Comparison matrix

| Framework | HITL / approval primitive | Durability | Multi-agent | Structured output | Model-agnostic / OpenAI-compat (Fireworks) | Lang | Maturity |
|---|---|---|---|---|---|---|---|
| LangGraph 1.0 | `interrupt()` / `Command(resume)`; middleware HITL; Agent Inbox schema | Checkpointers (Postgres/Redis/…); Temporal plugin (preview) | Subgraphs, `Send`, Deep Agents subagents | via provider `with_structured_output` | Yes | Py, JS | High; 41k★ |
| Pydantic AI | Deferred tools + approval, validated before approver | First-party Temporal/DBOS/Restate/Prefect/Lambda | Agent delegation, pydantic-graph, A2A | Native, typed (its core strength) | Yes | Py | High; 19.8k★ |
| OpenAI Agents SDK | `needs_approval`, interruptions, guardrails pre/post approval | Temporal/DBOS/Restate integrations | Handoffs | Pydantic output types | Yes via Chat Completions/LiteLLM | Py, TS | High; 29k★ |
| MS Agent Framework 1.0 | `RequestPort`, approval-required tools | Built-in workflow checkpointing | Graph workflows, SK/AutoGen patterns | Yes | Yes (OpenAI-compatible clients) | Py, .NET | GA Apr 2026 |
| Google ADK Go 2.0 | `RequireConfirmation`, `RequestInputEvent` | Session-history resume | Graph engine, A2A | Yes | Gemini-first; community OpenAI adapters | Go, Py, Java | 2.0 Jun 2026 |
| Claude Agent SDK | `canUseTool`, hooks, permission modes | None (session files) | Subagents | Yes | **No** (Claude only) | Py, TS | High |
| CrewAI | Flows HITL triggers | Flow state persistence | Crews | Pydantic | Yes | Py | 54k★ |
| LlamaIndex Workflows | `InputRequiredEvent` wait | Context serialization | AgentWorkflow handoffs | Yes | Yes | Py, TS | 1.0 |
| smolagents | Minimal | None | Managed agents | Weak (code) | Yes | Py | 27.8k★ |
| DSPy | n/a (optimizer) | n/a | n/a | Signatures | Yes | Py | 34k★ |
| Mastra | `suspend()/resume()` | Storage-backed suspend | Agent networks | Zod | Yes (40+) | TS | 27.8k★ |
| Vercel AI SDK 6 | `needsApproval` | Via Temporal/Restate | Manual | Zod | Yes | TS | GA May 2026 |
| Eino (ByteDance) | Any agent/tool can interrupt; checkpoint store | Checkpoint store | DeepAgent, multi-agent | Yes | OpenAI/Claude/Gemini/Ollama/Ark | Go | v0.9.19 (2026-09-01), 13k★ |
| Genkit Go | Interrupts (tool-level) | None | Flows | Strong | Yes (plugins) | Go (stable) | 6.4k★ (mono) |
| langchaingo | Weak | None | Weak | Basic | Yes | Go | 9.7k★, community |

---

## 3. Durable execution for long-running agents

The 2026 consensus (Temporal, Restate, DBOS, Inngest, Hatchet all say the same): persist every completed execution boundary (LLM call, tool call, side effect), replay on crash without re-running side effects, and make a multi-day approval wait a **free** durable wait rather than a live process [33][34].

- **Temporal** — server + workers; deterministic Workflow code with nondeterminism in Activities; first-class Go SDK (Temporal itself is Go). Official integrations: OpenAI Agents SDK (Python/TS) [15][16], LangGraph plugin (Python, preview) [9], Pydantic AI [10], Vercel AI SDK. Best-understood semantics; heaviest ops (a cluster + Cassandra/Postgres + Elastic, or Temporal Cloud).
- **Restate** — single-binary runtime, journal-based; SDKs TS/Python/Java/Kotlin/Go/Rust; Go SDK (MIT, 1.x) has `ctx.Run`, awakeables/promises (the "wait days for a human" primitive), virtual objects, durable timers [35]. Ships framework hooks: Vercel `durableCalls()`, OpenAI `DurableRunner`, ADK `RestatePlugin()`, Pydantic `RestateAgent`, LangChain `RestateMiddleware()` [22].
- **DBOS Transact** — *library*, not a server: annotate workflows/steps, state in **your Postgres** (same DB, same transaction); `dbos.Sleep`, `dbos.Recv[T](ctx, topic, timeout)`, `dbos.Send` cover human-approval waits. Official **Go** library (828★, MIT) shipped 2026; Python/TS/Java too; OpenAI Agents SDK and Pydantic AI integrations [36][37].
- **Inngest** — event-driven step functions, hosted or self-hosted; Go SDK exists but is small (80★); pricing per step, and the fit gets awkward when the agent loop is long and dynamic [33][38].
- **Hatchet** — Postgres-only orchestration engine (7.6k★), Python/TS/Go/Ruby; Go SDK can run the engine in-process with `hatchet.WithEmbeddedPostgres`; durable sleep/event waits [39].

**Fit for Go core + Python AI plane:** Temporal and Restate are the two that give you *one* durable fabric spanning both languages with equal-class SDKs. DBOS is the lowest-friction if you accept two runtimes (Go lib in cored, Python lib in insightd) sharing one Postgres — which you already run — and you don't need cross-language child workflows. Hatchet is a reasonable Postgres-native Temporal-lite. Inngest is the odd one out for a Go-centric shop.

---

## 4. Go-native options — and where the runtime should live

Go landscape (2026): **Eino** (ByteDance/CloudWeGo, Apache-2.0, 13k★, v0.9.19 published 2026-09-01, Go ≥1.18) has Graph/Chain orchestration plus an ADK with interrupt/resume from checkpoint, DeepAgent, multi-agent; battle-tested in Doubao/TikTok [40][41]. **Google ADK Go 2.0** (above) [20]. **Genkit Go** is "production-ready with full feature support" (tool calling, structured output, flows; Apache-2.0) [42]. **langchaingo** (9.7k★, MIT) is alive but explicitly moving to "community effort" [43]. Official model SDKs: `anthropic-sdk-go` v1.62.0 (2026-08-07) [44]; `openai-go` is official and covers Fireworks' OpenAI-compatible endpoint. Official **MCP Go SDK** (maintained with Google) v1.7.0+ implements spec 2026-07-28 [45].

**Trade-offs — runtime in Go (cored) vs Python (insightd):**

| | Go / cored | Python / insightd |
|---|---|---|
| Tool execution | In-process calls to the same services the REST/UI use; RLS context, auth, hash-chain, outbox all already there; no network hop, no second authz model | Every tool = HTTP call back into cored; must re-implement/propagate tenant + user + step-up auth |
| Approval gates | Can reuse the existing permission/ACL PDP and step-up (WebAuthn) directly | Must be mirrored |
| Durability | DBOS-Go/Restate-Go/Temporal-Go, Postgres you already run | Same engines, Python side |
| Framework richness | Eino/ADK-Go are credible but young; no Deep Agents / Agent Inbox equivalents | Richest ecosystem (LangGraph, Pydantic AI, DSPy optimisers, smolagents CodeAgent) |
| Model work (OCR, redaction, prompt optimisation, evals) | Awkward | Natural |

The asymmetry is decisive: **the expensive, correctness-critical part of an ERP agent is the tool layer (authz, tenancy, idempotency, audit), and all of that already lives in Go.** The LLM loop itself is ~300 lines. Put the agent runtime and tool registry in cored; keep insightd for model-adjacent work (OCR, PII redaction, DSPy/GEPA optimisation, evals, embeddings for tool retrieval) and as an optional Python "worker" that cored calls for specialised sub-tasks.

---

## 5. MCP, AG-UI, A2A, ACP

**MCP spec state.** Current revision **2026-07-28** [46][47]: protocol core is now **stateless** (every request self-describes version/identity/capabilities in `_meta`; no session handshake; works behind round-robin LBs); **Multi Round-Trip Requests (MRTR)** replace server-initiated streams — a tool needing mid-call input (confirmation, missing param) returns `resultType: "input_required"` and the client retries with `inputResponses` (this is how *elicitation* now works); header-based routing (`Mcp-Method`, `Mcp-Name`) so gateways can authz without parsing bodies; cacheable `tools/list` etc. with `ttlMs`/`cacheScope`; authorization hardening (RFC 9207 issuer validation, DCR → **Client ID Metadata Documents**, issuer-bound credentials); formal **Extensions** framework — **Tasks** (`io.modelcontextprotocol/tasks`, poll-based, for long-running work), **MCP Apps** (`io.modelcontextprotocol/ui`, server-rendered UI), **Enterprise Managed Authorization**; **Roots, Sampling and Logging deprecated** (≥12-month window); legacy HTTP+SSE transport deprecated. Tier-1 SDKs: TypeScript, Python, **Go**, C#; Rust beta [46]. 2026 roadmap priorities: transport scalability + `.well-known` discovery, Tasks retry/expiry semantics, governance (working groups), enterprise readiness (audit, SSO, gateways) [48]. Official registry: registry.modelcontextprotocol.io [49].

**MCP Apps (SEP-1865).** Tools declare a `ui://` resource in metadata; host renders the HTML in a sandboxed iframe; UI ↔ host speak MCP JSON-RPC; co-designed with OpenAI and Anthropic [50]. Directly relevant: a "review this draft voucher" approval card can be an MCP App usable from Claude/ChatGPT.

**ERP vendors as MCP servers.**
- **Intuit QuickBooks**: official `quickbooks-online-mcp-server` (TypeScript/Zod, Apache-2.0, 384★): **145 tools, 29 entities with full CRUD + 11 reports**, stdio-local, OAuth 2.0; write safety is only env flags (`QUICKBOOKS_DISABLE_WRITE/UPDATE/DELETE`), no per-action confirmation [51]. Separately, a multi-year Intuit–Anthropic partnership (Feb 2026) puts QuickBooks/TurboTax/Credit Karma/Mailchimp into Claude via hosted MCP connectors [52] (the connector's tool list — `qbo_sales_create_invoice`, `qbo_sales_send_invoice_reminder`, `qbo_payroll_*`, `qbo_lending_*` — is a good model of namespaced, coarse-grained business tools).
- **Xero**: official `xero-mcp-server` (TS, MIT, 364★), stdio, OAuth2 custom connections or bearer token; ~46 tools (22 list / 13 create / 11 update) plus a `xero-agent-toolkit` of framework examples [53][54].
- **Zoho Books**: official hosted MCP launched 2026-04-30, default Claude connector, also ChatGPT/Cursor/Windsurf; exposes create-invoice, record-expense, bank reconciliation, price/tax edits, reminders; admin-controlled tool enablement; Zoho recommends an approval layer in the finance team [55].
- **Stripe**: remote `https://mcp.stripe.com` with OAuth per the MCP spec (restricted key alternative), local `@stripe/mcp`, plus `@stripe/agent-toolkit` for Python/TS frameworks [56].

Pattern: everyone exposes coarse business operations (not raw REST), namespaces by domain, and *none* of them embed approval in the protocol — approval is the client's job (which MRTR `input_required` now makes possible server-side).

**AG-UI** (CopilotKit): bidirectional event stream between an agent backend and a UI; integrations with LangGraph, CrewAI, Pydantic AI, Strands [57]. **A2A**: Google → Linux Foundation (June 2025); v1.0 spec, signed Agent Cards, 5-language SDKs, ~22k★; native in ADK, LangGraph, CrewAI, LlamaIndex, MS Agent Framework [58][59]. **ACP** (IBM/BeeAI) was **merged into A2A** [57]. For us: MCP is mandatory (both directions), AG-UI is a useful wire format for streaming agent state into the React shell, A2A is not needed until a third-party agent (a CA-firm's agent, a bank's agent) needs to delegate to ours.

---

## 6. Tool design at scale (hundreds of ERP operations)

Evidence base:
- Anthropic's docs: selection accuracy "degrades once you exceed 30–50 available tools"; a five-server setup costs ~55k tokens; tool search cuts that by >85% by loading 3–5 tools per request [60]. Implementation: `defer_loading: true` on most tools, a search tool (regex or BM25) returns `tool_reference` blocks the API expands; **custom client-side tool search** (e.g. embeddings) returns `tool_reference` blocks from an ordinary tool — this pattern is model-independent and trivially portable to open models [60].
- Anthropic "code execution with MCP" (Nov 2025): present servers as a filesystem of typed TS/Python stubs; agent explores/searches and writes code; 150,000 → 2,000 tokens (98.7%) in a Drive→Salesforce example; intermediate data never transits the model (PII tokenised in the sandbox); reusable code saved as skills; cost = a real sandbox [61]. Productised as **programmatic tool calling** (`allowed_callers: ["code_execution_…"]`), +11% on BrowseComp/DeepSearchQA with 24% fewer input tokens [62].
- **Toolshed / Advanced RAG-Tool Fusion** (2024) and **Graph RAG-Tool Fusion** (Feb 2025): vector "tool knowledge bases" with query rewriting + reranking scale to thousands of tools without fine-tuning; graph variant captures tool dependencies [63][64]. Red Hat's "Tool RAG" (Nov 2025) is the enterprise framing [65].
- **CodeAct** (ICML 2024): code actions vs JSON — up to +20% success, ~30% fewer turns; crucially "JSON is consistently weaker than other approaches for **open-source** models" [66]. **"The Bitter Lesson of Tool Calling"** (Aug 2026, same authors as Toolshed): programmatic tool calling matches or beats JSON in 11/14 models on BFCL v4, 13/14 under parallel execution, and is more robust to context degradation [67].
- Anthropic "Writing effective tools for agents" (Sep 2025): consolidate multi-call workflows into one tool, namespace by resource/service, return high-signal (not raw) payloads, let the agent evaluate tool descriptions [68].

**What works with mid-size open models:** (a) hierarchical exposure — a *small* always-loaded core (search_tools, read-only query DSL, get_draft, submit_for_approval) plus deferred domain packs retrieved by embedding/BM25 search; (b) coarse, workflow-shaped tools (`create_sales_invoice_draft` that resolves party/ledger/GST in one call) rather than 400 CRUD endpoints; (c) code execution is the strongest *technique* but the riskiest to run against a ledger — use it only for read-only analysis (agent writes Python/JS against a read-only SDK in a sandbox) and keep writes as discrete, schema-validated, approval-gated tools; (d) `tool_choice: required` + strict JSON schema on Fireworks for any "decide which tool" step [69].

---

## 7. Planning patterns that hold up with open models

- **ReAct** remains the default for short tool chains but has a known "verifier stall"/looping failure mode with weaker models [70][71]. **Plan-and-execute** (plan once with the best model, execute steps with a cheaper one, replan on failure) gives inspectable plans *before* side effects — exactly what an approval gate needs — and fewer LLM calls [70][72]. **Deep-agent** style (todo list + filesystem + subagents) is now packaged (deepagents, Eino DeepAgent, ADK) and is model-agnostic [7][40].
- **Structured-output-first.** Open models lag on *format* more than on *task*: "When Correct Isn't Usable" (May 2026) finds 7B-class models hit ~85% task accuracy with 0% usable-format accuracy under naive prompting, and constrained decoding costs 3.6–8.2× latency while sometimes hurting task accuracy; prompt optimisation with a meta-agent recovers 84–87% [73]. Fireworks supports JSON-Schema 2020-12 (treats schemas with properties as `unevaluatedProperties:false`, i.e. no hallucinated fields) and BNF grammar mode [69]. XGrammar is the default backend in vLLM/SGLang/TRT-LLM (2026) [74]. → Constrain *every* decision step; keep free-text out of the control path.
- **Tool-calling capability of open models.** BFCL v4 (Apr 2026) is the reference; on v3 GLM-4.5 tops the table (~77%, above Claude Opus 4.7 at 76.6%), Qwen3 ~72%, Kimi K2 ~71% [75][76]; gpt-oss-120b gets 67.8% on τ-bench retail and is explicitly tuned for tool use [77]. Fireworks' own function-calling docs use `kimi-k2-instruct-0905` as the reference model and recommend temperature 0–0.3 for tool selection [69].
- **Verifier loops / reflection.** ADK Go ships a "Retry and Reflect" plugin [19]; the cheaper, more reliable verifier in accounting is *deterministic*: run the proposed voucher through the same validation the UI uses (debits=credits, GST rate table, party GSTIN, period lock) and feed violations back — a code verifier, not an LLM judge.
- **DSPy/GEPA** for open models: optimise tool descriptions, the router prompt, and the ReAct predictors offline against your own eval set; 3.3's `ReActV2` uses native tool calls so optimised programs run on Fireworks unchanged [28][29].

---

## 8. Recommendations for our build

1. **Runtime placement: Go, in cored.** Hand-roll the loop (or use Eino's ADK if you want a graph DSL) — the loop is small; the leverage is that tools are in-process Go functions with the tenant/RLS/ACL/step-up context already bound, and every write flows through the existing voucher-version + outbox path. insightd stays a Python *worker* (OCR, redaction, embeddings, DSPy/GEPA optimisation, evals), invoked by cored as a durable step — never a second write path.
2. **Framework: minimal + borrowed schemas, not LangGraph.** LangGraph's real value (checkpointers, `interrupt`) is Python-side and its node-re-execution semantics are a footgun around ledger writes. Copy three things: Agent Inbox's `HumanInterrupt` schema (accept/edit/respond/ignore) [5], Pydantic AI's "validate before the approver sees it" deferred-tool rule [12], and Deep Agents' todo/subagent harness shape [7]. If Python is unavoidable for a component, use Pydantic AI (typed, OpenAI-compatible, first-party durable backends).
3. **Durability: DBOS Transact (Go) on the existing Postgres**, or Restate if you want one fabric across Go+Python with a runtime. Every agent run = a workflow; every LLM call and tool call = a step; approval = `dbos.Recv` on a per-run topic with a timeout (days); the approval *decision* is itself written to the hash-chained log. Temporal is the fallback if you outgrow it; avoid Inngest for this stack. [36][35][33]
4. **Approval gating = policy, not prompt.** Tag every tool with a risk class (read / draft / commit / external-side-effect / config). Read: auto. Draft: auto (drafts are not vouchers). Commit: requires `HumanInterrupt` accept, or auto-commit only under a per-tenant policy (amount thresholds, known party, period open) evaluated by the existing PDP; step-up (WebAuthn) for external side effects (GST filing, payment reminders to customers, bank instructions). Reuse the deterministic validators as the verifier loop before anything reaches a human.
5. **Tools: ~40 coarse workflow tools + a search tool, not 400 CRUD tools.** Always-loaded core: `search_tools`, `query_books` (existing NL→DSL→SQL), `get_draft`, `propose_voucher`, `submit_for_approval`, `list_pending`. Domain packs (GST, bank reco, inventory, payroll, config, import/export) deferred and retrieved by embedding/BM25 over name+description+arg descriptions (Toolshed pattern) [60][63]. Namespaced like Intuit's connector (`gst_`, `bank_`, `voucher_`). Read-only **code execution** (sandboxed Python/JS against a read-only SDK) for analysis and insights — never for writes [61][66][67].
6. **Structured output everywhere.** Fireworks JSON-schema/grammar mode on every routing and argument-filling step; `tool_choice: required` where a tool must be chosen; low temperature; optimise prompts/tool descriptions with DSPy GEPA against a golden set of Indian-SMB tasks (voucher entry, GSTR-1/3B, reco) before trusting a 30–120B model [69][73][28]. Model candidates to bake off: GLM-4.5/5.x, Kimi K2, Qwen3 family, gpt-oss-120b [75][76][77].
7. **MCP both ways.** Expose the *same* Go tool registry as an MCP server (official Go SDK, spec 2026-07-28, streamable HTTP, OAuth with CIMD) so Claude/ChatGPT/Cowork/Zoho-style agents can act on a tenant's books — with the same risk classes enforced server-side, using MRTR `input_required` for confirmations and an MCP App card for approvals [46][50]. Consume MCP internally for external systems (Stripe, banks, GSP APIs, e-mail). Emit AG-UI events to the React shell for the agent panel [57]. Defer A2A until an external agent needs to delegate to ours [58].
8. **Proactive insights = ambient agents.** Cron/outbox-triggered runs (dunning candidates, GST deadline, unreconciled bank lines, anomaly reports) that produce drafts and land in an Inbox, never auto-commit — the LangChain "ambient agent" model, implemented as DBOS scheduled workflows in cored [4][36].

---

## Sources

1. https://www.langchain.com/blog/langchain-langgraph-1dot0
2. https://github.com/langchain-ai/langgraph
3. https://docs.langchain.com/oss/python/langgraph/interrupts
4. https://www.langchain.com/blog/introducing-ambient-agents
5. https://github.com/langchain-ai/agent-inbox
6. https://rywalker.com/research/langmem
7. https://github.com/langchain-ai/deepagents
8. https://docs.langchain.com/oss/python/deepagents/overview
9. https://temporal.io/blog/temporal-langgraph-plugin-durable-execution
10. https://pydantic.dev/docs/ai/integrations/durable_execution/overview/
11. https://github.com/pydantic/pydantic-ai
12. https://pydantic.dev/docs/ai/api/pydantic-ai/tools/
13. https://github.com/openai/openai-agents-python
14. https://developers.openai.com/api/docs/guides/agents/guardrails-approvals
15. https://temporal.io/blog/announcing-openai-agents-sdk-integration
16. https://docs.temporal.io/develop/typescript/integrations/openai-agents
17. https://alexbevi.com/blog/2026/06/18/two-lineages-one-framework-how-autogen-and-semantic-kernel-became-the-microsoft-agent-framework/
18. https://devblogs.microsoft.com/agent-framework/migrate-your-semantic-kernel-and-autogen-projects-to-microsoft-agent-framework-release-candidate/
19. https://developers.googleblog.com/adk-go-10-arrives/
20. https://developers.googleblog.com/announcing-adk-go-20/
21. https://github.com/google/adk-go/issues/596
22. https://docs.restate.dev/ai/patterns/durable-agents
23. https://code.claude.com/docs/en/agent-sdk/hooks
24. https://atlan.com/know/ai-agent/what-is-crewai/
25. https://www.llamaindex.ai/blog/announcing-workflows-1-0-a-lightweight-framework-for-agentic-systems
26. https://developers.llamaindex.ai/python/framework/understanding/agent/human_in_the_loop/
27. https://github.com/huggingface/smolagents
28. https://github.com/stanfordnlp/dspy/releases
29. https://dspy.ai/getting-started/gepa-optimization/
30. https://github.com/mastra-ai/mastra
31. https://vercel.com/blog/ai-sdk-6
32. https://futureagi.com/blog/oss-agent-frameworks-2026/
33. https://www.zenml.io/blog/inngest-alternatives
34. https://zylos.ai/research/2026-04-24-durable-execution-agent-runtimes/
35. https://github.com/restatedev/sdk-go
36. https://github.com/dbos-inc/dbos-transact-golang
37. https://www.dbos.dev/blog/dbos-new-features-march-2026
38. https://github.com/inngest/inngestgo
39. https://github.com/hatchet-dev/hatchet
40. https://github.com/cloudwego/eino
41. https://pkg.go.dev/github.com/cloudwego/eino
42. https://github.com/genkit-ai/genkit
43. https://github.com/tmc/langchaingo
44. https://github.com/anthropics/anthropic-sdk-go/releases
45. https://github.com/modelcontextprotocol/go-sdk
46. https://blog.modelcontextprotocol.io/posts/2026-07-28/
47. https://modelcontextprotocol.io/specification/2026-07-28
48. https://blog.modelcontextprotocol.io/posts/2026-mcp-roadmap/
49. https://registry.modelcontextprotocol.io/
50. https://modelcontextprotocol.io/seps/1865-mcp-apps-interactive-user-interfaces-for-mcp
51. https://github.com/intuit/quickbooks-online-mcp-server
52. https://investors.intuit.com/news-events/press-releases/detail/1305/intuit-and-anthropic-partner-to-bring-trusted-financial-intelligence-and-custom-ai-agents-to-consumers-and-businesses
53. https://github.com/xeroapi/xero-mcp-server
54. https://github.com/XeroAPI/xero-agent-toolkit
55. https://www.zoho.com/blog/books/model-context-protocol-accounting-software.html
56. https://docs.stripe.com/mcp
57. https://www.copilotkit.ai/ag-ui
58. https://en.wikipedia.org/wiki/Agent2Agent
59. https://google.github.io/adk-docs/a2a/
60. https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool
61. https://www.anthropic.com/engineering/code-execution-with-mcp
62. https://platform.claude.com/docs/en/agents-and-tools/tool-use/programmatic-tool-calling
63. https://huggingface.co/papers/2410.14594
64. https://arxiv.org/html/2502.07223v1
65. https://next.redhat.com/2025/11/26/tool-rag-the-next-breakthrough-in-scalable-ai-agents/
66. https://arxiv.org/abs/2402.01030
67. https://arxiv.org/abs/2608.06370
68. https://www.anthropic.com/engineering/writing-tools-for-agents
69. https://docs.fireworks.ai/guides/function-calling and https://docs.fireworks.ai/structured-responses/structured-response-formatting
70. https://dev.to/gabrielanhaia/react-plan-and-execute-or-reflection-the-three-agent-patterns-every-engineer-needs-in-2026-355p
71. https://theaiengineer.substack.com/p/the-4-single-agent-patterns
72. https://arxiv.org/pdf/2509.08646
73. https://arxiv.org/abs/2605.02363
74. https://dev.to/pockit_tools/llm-structured-output-in-2026-stop-parsing-json-with-regex-and-do-it-right-34pk
75. https://gorilla.cs.berkeley.edu/leaderboard.html
76. https://wavect.io/blog/open-weight-llm-comparison-2026/
77. https://fireworks.ai/blog/openai-gpt-oss
78. https://blog.modelcontextprotocol.io/posts/2025-11-21-mcp-apps/
79. https://temporal.io/blog/building-durable-agents-with-temporal-and-ai-sdk-by-vercel
80. https://www.reactify-solutions.com/articles/durable-ai-agents-2026
