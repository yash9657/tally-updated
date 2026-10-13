# R2 — Open-weight models and inference providers for an AI-native Tally competitor (as of 8 Sep 2026)

Scope: which open-weight models to run, on which providers, at what cost, for an agentic accounting layer (tool-calling agent with approval gates, NL query -> typed DSL, document extraction, nightly insights, WhatsApp/voice in Indian languages). Numbers below are from vendor pricing pages, model cards and leaderboards fetched on 2026-09-08 unless dated otherwise. Where a number comes from a third-party aggregator rather than a primary source it is flagged.

Headline: the brief's model list (Llama 4, Qwen3/3.5, DeepSeek V3/R1, Kimi K2, GLM-4.5/4.6, gpt-oss) is a generation behind. The relevant open frontier today is **Kimi K3 (Jul 2026), GLM-5.3 / 5.3-Flash (Aug 2026), DeepSeek V4 Pro / Flash (Apr–Aug 2026), Qwen3.8 (Aug 2026), MiniMax M3 (Jun 2026), Nemotron 3 Ultra (Jun 2026), Gemma 4 (Apr 2026), Mistral Medium 3.5 (Apr 2026), Granite 4.2 (Aug 2026), Sarvam-105B/30B (Feb 2026)**. Meta has effectively stepped back: Behemoth was never shipped, there is no Llama 5, and Meta's April 2026 frontier release (Muse Spark) is closed-weight.

---

## 1. Open-weight model landscape (Sept 2026)

### 1.1 Frontier-class open models (agentic planning tier)

| Model (lab, date) | Params total / active | Context | License | Agentic / tool-use evidence | Notes |
|---|---|---|---|---|---|
| **Kimi K3** (Moonshot, API 16 Jul 2026, weights 26 Jul 2026) | 2.8T / 104B (16 of 896 experts) | 1M | Custom "kimi-k3": separate agreement once MaaS revenue > $20M/yr; "Kimi K3" attribution if > 100M MAU or > $20M monthly revenue | Terminal-Bench 2.1 88.3 (Claude Fable 5: 78.0, GPT-5.6 Sol: 88.8); BrowseComp 91.2; DeepSWE 67.5; #1 on WebDev Arena (1,678 Elo, first open model to top it); AA Intelligence Index 44 | Native multimodal (text/image/video), MXFP4 weights. Needs ~8xH100 minimum to self-host. Multi-turn requires passing `reasoning_content` + `tool_calls` back ("preserved thinking history"). Most expensive open model ($3 / $15 per 1M). |
| **GLM-5.3** (Z.ai, 14 Aug 2026; weights 28 Aug 2026) | ~744–753B MoE (active not disclosed) | 1M (300K used in HLE eval) | Custom "glm-5.3": MIT-style; MaaS operators > $10B TTM revenue need Z.ai security review | Terminal-Bench 2.1 88.2; Terminal-Bench 3.0 28.3 (#1); Toolathlon Verified 73.0; AutomationBench 48.2; Agents' Last Exam (CLI) 28.5; predecessor GLM-5.2 was #1 open on AA Intelligence Index (51 on v4.1) and led open weights on GDPval-AA v2 "effectively equal with GPT-5.5 xhigh"; GLM-5.2 tops the aggregated tau2 table at 99.1% (telecom, uncontrolled) | `reasoning_effort` low/high/max. AA Index 45 (top open, 8 Sep 2026). 70 tok/s on AA median. |
| **GLM-5.3-Flash** (Z.ai, 26 Aug 2026) | 320B / 18B | 1M | MIT | AA Index 42 — within 3 points of the flagship at ~1/10 the price | First natively multimodal GLM-5 (text/image/video). **Best price/intelligence in the open catalog** ($0.15 / $0.50 on Fireworks and Together). |
| **DeepSeek V4 Pro** (Apr 2026, 0813 refresh) | 1.6T / 49B | 1M (384K max output) | MIT | SWE-bench Verified 80.6; SWE-bench Multilingual 76.2; aggregated tau2 96.2% (uncontrolled); "ties the closed frontier on SWE-Bench at ~1/10 the output price" | Non-think / Think-High / Think-Max modes; Think-Max needs >= 384K context. AA Index 36. Trained on 33T tokens. |
| **DeepSeek V4 Flash** (Apr 2026, 0731 refresh) | 284B / 13B | 1M | MIT | SWE-bench Verified 79.0 | 10% of V3.2's per-token FLOPs and 7% of its KV cache at 1M ctx. Cheapest frontier-adjacent model: $0.22 / $0.66 Fireworks, $0.14 / $0.28 Together, $0.06–0.09 / $0.18 DeepInfra. Text-only. |
| **Qwen3.8-2.4T-A95B / Qwen3.8-Max** (Alibaba, 3 Aug API, ~12 Aug weights) | 2.4T / 95B | ~1M | Custom (conditional) | OSWorld-Verified 86.1 (vs GPT-5.6 Sol Max 83.2, Fable 5 85.0); Qwen3.7-Max is #1 on BFCL-V4 (0.750) | AA Index 40. 119 languages (Qwen3 lineage); Qwen3.5 cards claim 201 languages/dialects. |
| **Qwen3.8-27B** (Alibaba, 13–14 Aug 2026) | 27.8B dense (48 Gated-DeltaNet + 16 full-attn layers) | 262K native, 1M via YaRN | **Apache 2.0** | SWE-bench Pro 61.7; Terminal-Bench 2.1 73.0 (Opus 4.8: 78.9); OSWorld-Verified 84.3; WebArena-Verified 64.8; IFBench 79.5; **OmniDocBench 1.5: 91.1** | Multimodal (text/image/video). FP8 fits 48GB; BF16 needs 80GB-class. AA Index 34. This is the best "mid" open model for us: Apache, vision, document-strong, tool-capable. |
| **MiniMax M3** (1 Jun 2026) | 428B / ~23B | 1M | MiniMax Community (attribution; larger products need authorization) | SWE-bench Verified 80.5; SWE-bench Pro 59; Terminal-Bench 2.1 66.0; ~Claude Sonnet 4.6 on GDPval-AA; BenchLM #13/28 on verified agentic tool use; on an autonomous BFCL-style composite scored 0.37 vs Opus 4.7 0.42 | Fastest of the big MoEs (156 tok/s on AA). $0.30 / $1.20 (cached $0.06). Native image/video. |
| **Nemotron 3 Ultra** (NVIDIA, 4 Jun 2026) | 550B / 55B, Mamba-2 + Transformer hybrid | 1M | NVIDIA Open Model License / OpenMDW | AA Index 48 on v4.1 (June), 23 on current index (Sep) — the index was re-based; treat with care | 168 tok/s. Free tier on OpenRouter. |
| **Nemotron 3 Super** | 120B / 12B | 1M native | NVIDIA Open Model License | "best open model in its class" on PinchBench (85.6%); supports `tools`, `tool_choice`, JSON-schema `response_format`; multi-token prediction gives "3x wall-clock speedups for structured generation tasks like code and tool calls" | Hosted on Fireworks, DeepInfra, Baseten, etc. |
| **Mistral Medium 3.5** (Apr 2026) | 128B dense | 256K | Modified MIT with revenue carve-out | **tau3-Telecom 91.4%**; SWE-bench Verified 77.6%; replaces Medium 3.1 + Magistral + Devstral 2 | AA Index 15 (low on general index but strong on customer-service-style tool use). Function calling + JSON structured outputs. |
| **Mistral Large 3** (Dec 2025) | 675B / 41B | 256K | Apache 2.0 | — | $0.50 / $1.50 on Mistral. Largest Apache-licensed frontier MoE. |
| **gpt-oss-120b / 20b** (OpenAI, 5 Aug 2025) | 117B / 5.1B; 21B / 3.6B | 131K | Apache 2.0 | Near o4-mini on reasoning at launch; AA Index 12 today; **repeated production reports of malformed tool calls** (see §3) | 215 tok/s (fastest on AA), $0.15 / $0.60 on Groq & Together; Cerebras ~3,000 tok/s. Requires Harmony format. |
| **Llama 4 Scout / Maverick** (Meta, Apr 2025) | 109B/17B; 400B/17B | 10M / 1M | Llama 4 Community | Behind every 2026 model above | Behemoth (2T) shelved, no Llama 5; Muse Spark (Apr 2026) is closed. Only worth it for legacy/edge (Scout at $0.10 / $0.30 on DeepInfra). |
| **Cohere Command A** | 111B dense (Mar 2025) | 256K | CC-BY-NC (non-commercial) | — | No 2026 open refresh found; license unsuitable for us. |

### 1.2 Mid / small tier (extraction, NL->DSL, classification)

| Model | Params | Context | License | Tool / structured evidence | Multilingual |
|---|---|---|---|---|---|
| **Qwen3.5-397B-A17B / 122B-A10B** (Feb 2026) | 397B/17B; 122B/10B | 256K | Apache 2.0 | **BFCL-V4 0.729 / 0.722 — #3 and #5 overall, top open-weight** | 201 languages/dialects claimed |
| **Qwen3.5-35B-A3B** | 35B / 3B | 256K | Apache 2.0 | — | $0.14 / $1.00 DeepInfra |
| **Qwen3.5-9B / 4B / 2B / 0.8B** | dense | 262K native (1M YaRN) | Apache 2.0 | 4B: **BFCL-V4 50.3, tau2-bench 79.9, IFEval 89.8, MMMLU 76.1**; multimodal | 201 langs; 9B at $0.17 / $0.25 on Together |
| **Gemma 4** (Google, 2 Apr 2026): E2B (5.1B total / 2.3B eff.), E4B (8B / 4.5B), 26B-A4B MoE, 31B dense | as listed | 128K (E*) / 256K | **Apache 2.0** (no MAU cap) | 31B: **tau2-bench 86.4**, MMLU-Pro 85.2, LiveCodeBench 80.0; "native function-calling"; E4B "built for agents and tool calling on constrained hardware" | Pretrained on 140+ langs, 35+ supported; E2B runs in <1.5GB quantized, E4B in ~5GB; E-models take native audio input |
| **Granite 4.2** (IBM, 25 Aug 2026): 3B / 8B / 30B dense | as listed | 128K (512K pretrain) | Apache 2.0 | **BFCL-v4 52.41 / 50.29 / 61.39**; SWE-bench Verified 47.7 / 57.0; thinking toggle; "agentic RL on 8B/30B" | Enterprise-oriented; Granite Speech 5.0 Turbo CTC 470M |
| **Sarvam-105B** (Sarvam, 18 Feb 2026) | 105B / 10.3B MoE (128 experts top-8 + 1 shared) | 128K | Apache 2.0 | **tau2-bench avg 68.3**, IFEval 84.8, MMLU 90.6, BrowseComp 49.5, AIME25 w/ tools 96.7; 90% pairwise wins on Indian-language evals | Trained from scratch on 12T tokens; 22 scheduled languages, **native script + romanized (Hinglish) + code-mixed**; custom Indic tokenizer. Weights on HF + AI Kosh. tp=8 to self-host. |
| **Sarvam-30B** | MoE (active undisclosed) | 32K | Apache 2.0 | — | 16T tokens; conversational/low-latency |
| **Sarvam-M** (May 2025) | 24B (Mistral Small fine-tune) | 32K | Apache 2.0 | — | Criticised as a thin fine-tune; superseded |
| **Krutrim** (Ola) | 7B open (Krutrim-1); Krutrim-2/3 API | — | mixed | — | Cloud lists Llama-4-Scout ₹7/1M, Gemma-3-27B ₹8/1M, DeepSeek-R1 ₹11/1M; A100 ₹189/hr. Not a serious model option; possibly a residency option. |
| **Phi-4-mini / SmolLM3-3B / Llama 3.2 3B** | 3–4B | 128K | MIT / Apache / Llama | — | Now outclassed by Qwen3.5-4B and Gemma 4 E4B on almost every axis; keep only as distillation/draft candidates |

**Structured-output reliability across the open catalog:** every model above is served with OpenAI-compatible `tools` and `response_format` on Fireworks/Together/DeepInfra, and reliability in practice is determined more by the *serving stack's constrained decoding* than by the model (see §2, §3). Reasoning models have one important trap: on Fireworks, `response_format: json_schema` **disables reasoning output** — so for GLM-5.3 / DeepSeek V4 / Kimi K3 you either take structured JSON *or* visible chain-of-thought, not both.

---

## 2. Fireworks AI in depth, and provider comparison

### 2.1 Fireworks catalog and pricing (docs.fireworks.ai/serverless/pricing, fetched 8 Sep 2026; $/1M tokens, Standard tier: input / cached input / output)

| Model | Standard | Priority | Notes |
|---|---|---|---|
| Kimi K3 | 3.00 / 0.30 / 15.00 | 3.75 / 0.375 / 18.75 | "Fast" lane 4.50 / 0.45 / 22.50; US-only 3.30 / 0.33 / 16.50 |
| DeepSeek V4 Pro | 1.32 / 0.044 / 3.96 | 1.65 / 0.055 / 4.95 | cached input is 3.3% of base — very cache-friendly |
| DeepSeek V4 Flash | 0.22 / 0.007 / 0.66 | 0.275 / 0.00875 / 0.825 | |
| GLM 5.3 | 1.40 / 0.26 / 4.40 | 1.75 / 0.325 / 5.50 | |
| GLM 5.3 Flash | 0.15 / 0.03 / 0.50 | — | |
| Qwen 3.8 Max | 2.00 / 0.25 / 6.00 | 3.00 / 0.375 / 9.00 | |
| Kimi K2.6 | 0.95 / – / 4.00 (Standard), 1.50 in (Priority), 2.00 in (Fast) | | third-party (Morph) |
| Generic by size | <4B: 0.10; 4–16B: 0.20; >16B: 0.90; MoE 0.50–1.20 | | applies to Qwen3.5 small, Gemma 4, Granite, Sarvam uploads |
| Embeddings | <=150M: 0.008; up to 0.016 by size; Qwen3-Embedding-8B 0.10 | | |

Mechanics: **Standard / Priority / Fast** lanes (Priority less likely to be load-shed; Fast targets 100+ tok/s at ~2x price); **US-only routing** was +10% and is documented as moving to a 50% premium from 1 Sep 2026 for some models — re-check before relying on it. **Prompt caching** is on by default for every model and deployment, longest-prefix match, cached tokens at roughly 50% (headline models much lower, e.g. 3–10%), TTL "minutes to hours", tool definitions count as prompt prefix, `x-session-affinity` header for cache-sticky routing, cache metrics in `fireworks-prompt-tokens` / `fireworks-cached-prompt-tokens` headers. **Batch API**: 50% off serverless, JSONL with `custom_id` + `body`, 12/24/48/72-hour expiry windows, no SLA, 80 GiB input / 8 GB output, caching still applies. **Structured output**: `json_object`, `json_schema` (most of JSON Schema 2020-12, internal `$defs` only, `unevaluatedProperties:false` implied — "automatically prevents hallucinated fields"), and **Grammar mode (BNF)** for classification/enum outputs; JSON mode is implicitly active during tool calling. **Function calling**: OpenAI-compatible `tools`, `tool_choice` auto/none/required/named, streaming argument deltas, parallel calls only on models flagged `supportsTools`/parallel. **Speculative decoding**: dedicated deployments only (serverless is managed); default drafters per model, custom same-family drafters (fallbacks Llama 3.2 1B, Qwen2.5 0.5B), EAGLE / DFlash / DSpark / Medusa addons, and **n-gram speculation** (no draft model) which is explicitly recommended for "structured generation where output often repeats the prompt" — exactly our voucher/DSL shape.

**Fine-tuning**: LoRA-only managed SFT/DPO (3 to 3M examples, JSONL chat format, weighted samples); priced per 1M training tokens: <=16B $0.50 LoRA-SFT / $1.00 LoRA-DPO; 16–80B $3 / $6; 80–300B $6 / $12; >300B $10 / $20 (full-weight is 2x). **RFT** (reinforcement fine-tuning, RLVR) bills per GPU-hour; evaluators are Python functions returning 0–1 (exact/fuzzy match, execution, LLM-judge); supported bases include Llama, Phi, Qwen 2.5/3, DeepSeek V3/R1, Kimi K2 (V4/K3 not confirmed on the page). **Eval Protocol** is the open-source, language-agnostic harness that lets you RFT an agent "in any environment" (demoed at GTC Mar 2026). Published RFT results: a 32B open model "exceeded GPT-4o" on an agent/tool-use task; Vercel 93% error-free code-gen (+33% vs closed) at 40x speed; Genspark +10% quality, +33% tool calls, ~50% cost. **Serving trained LoRAs is dedicated-only** ("the only supported method for serving trained models"), multi-LoRA on one deployment supported — so a fine-tune commits you to at least one always-on GPU.

**Dedicated GPUs** (fireworks.ai/pricing, effective 1 Sep 2026): H100/H200 $8/hr, B200 $13/hr, B300 $15/hr, GB300 $20/hr; **region-restricted deployments +50%**. **Regions**: multi-regions GLOBAL / US / EUROPE / APAC (new accounts get GLOBAL only; others need sales); 24 single regions including AP_TOKYO_1/2, AP_NEWSOUTHWALES_1, AP_MALAYSIA_2 (B300) — **no India region**. Enterprise: zero data retention by default, SSO, audit logs, HIPAA/SOC 2, air-gapped EKS (BYOC). **Rate limits**: 10 RPM with no card; 6,000 RPM account-wide fixed cap once paying; spend tiers $50 -> $500 -> $5k -> $50k/month by cumulative spend; on-demand deployments are quota-by-GPU, and a 429 there means saturation not TPM. $1 free credit.

### 2.2 Provider comparison on the same models ($/1M in / out, Sept 2026)

| Model | Fireworks | Together | DeepInfra | Groq | Cerebras | Other |
|---|---|---|---|---|---|---|
| Kimi K3 | 3.00 / 15.00 | 3.00 / 15.00 (cached 0.30); PTU $0.05/PTU-min | 2.85 / 14.25 | — | — | Moonshot 3 / 15, cache-hit 0.30 |
| DeepSeek V4 Pro | 1.32 / 3.96 | 1.32 / 3.96 (cached 0.13) | 1.30 / 2.60 | — | — | |
| DeepSeek V4 Flash | 0.22 / 0.66 | 0.14 / 0.28 | 0.06–0.09 / 0.18 | — | — | OpenRouter 0.054 / 0.242 (DeepSeek's own, data retained) |
| GLM-5.3 | 1.40 / 4.40 | 1.40 / 4.40 | — | — | — | Sarvam Inference ₹126 / ₹396 (beta) |
| GLM-5.3-Flash | 0.15 / 0.50 | 0.15 / 0.50 | — | — | — | Sarvam ₹13.5 / ₹45 (beta) |
| Qwen3.8 Max | 2.00 / 6.00 | 2.00 / 6.00 | 1.65 / 4.95 | — | — | |
| MiniMax M3 | — | 0.30 / 1.20 | — | — | — | |
| Gemma 4 31B | 0.90 (generic) | 0.39 / 0.97 | 0.13 / 0.38 | — | — | Sarvam ₹36.6 / ₹91.5 |
| gpt-oss-120b | ~0.15 / 0.60 | 0.15 / 0.60 | — | 0.15 / 0.60 @ ~500 tok/s | ~3,000 tok/s | AA median 0.15 / 0.69 |
| Qwen3.5-9B / 35B-A3B | 0.20 / 0.90 (generic) | 9B 0.17 / 0.25 | 35B-A3B 0.14 / 1.00 | — | — | |
| Dedicated H100 / hr | 8.00 | 3.99–5.49 | 2.20 | n/a | n/a | E2E (India) from ₹50/hr for small GPUs |
| SFT / 1M tok (<=16B) | 0.50 (LoRA) | 0.48 | — | — | — | |

Take-aways: DeepInfra undercuts Fireworks on 30 of 38 shared models (pricepertoken) and is the cheapest home for V4 Flash and Gemma 4; Fireworks and Together are price-matched on the headline Chinese models (they are re-listing lab pricing); Groq/Cerebras only matter for gpt-oss/Llama-class speed (Groq LPU ~500–750 tok/s, Cerebras 2,100–3,000 tok/s) and neither carries GLM/DeepSeek V4/Kimi K3 as of the docs fetched. Fireworks' differentiators are the feature surface (grammar mode, n-gram speculation, RFT + Eval Protocol, session-affinity caching, batch) and enterprise controls, not price.

---

## 3. Which open models are actually reliable at multi-turn tool calling and JSON

**Leaderboard evidence (controlled):**
- **BFCL-V4** (llm-stats mirror, updated 8 Sep 2026, 21 models): Qwen3.7 Max 0.750 (closed) > Ling 3.0 Flash 0.730 > **Qwen3.5-397B-A17B 0.729 (open)** > Qwen3.7-Plus 0.729 > **Qwen3.5-122B-A10B 0.722 (open)**; mean 0.594. Granite 4.2-30B 61.4, Granite 4.2-3B 52.4, Qwen3.5-4B 50.3 (self-reported). Gorilla's own page last updated 2026-04-12.
- **tau2-bench** (vendor cards): Gemma 4 31B 86.4; Qwen3.5-4B 79.9; Sarvam-105B 68.3 avg; Kimi K2 66.1 (2025). Mistral Medium 3.5 reports **tau3-Telecom 91.4**. Aggregated tables (BenchLM, 125 rows) show GLM-5.2 99.1%, GPT-5.4 98.9%, Claude Fable 5 98.5%, DeepSeek V4 Pro 0813 96.2%, Kimi K2.6 95.9% — but BenchLM itself warns rows mix domain/user-simulator/scaffold/pass^k and are "a source ledger, not a controlled cross-provider ranking".
- **Artificial Analysis** (8 Sep 2026): top open on Intelligence Index GLM-5.3 (45), Kimi K3 (44), GLM-5.3-Flash (42), Qwen3.8-2.4T (40), DeepSeek V4 Pro (36), Qwen3.8-27B (34); open models sit ~9 points behind Claude Fable 5.1 / GPT-6. GLM-5.2 was flagged as "token hungry"; MiniMax M3 and Nemotron 3 Ultra are the throughput leaders (156–168 tok/s).
- **Terminal-Bench** (long-horizon tool use): Kimi K3 88.3, GLM-5.3 88.2, Qwen3.8-27B 73.0, MiniMax M3 66.0, Granite 4.2-30B 29.2 (TB 2.1).

**Production failure modes reported for open models (GitHub/HF/Groq forum, 2025–26):**
1. Tool call emitted as text in `content` instead of `tool_calls` (gpt-oss-120b on exo, Ollama, vLLM issues #22337, opencode #7185).
2. Hallucinated tool names — gpt-oss emitting `assistant<|channel|>analysis` as the function name, "a very common problem" (Groq community).
3. Parallel-tool-call loops / repeating the same call until context exhaustion; not handing control back to the user (HF gpt-oss discussion #151).
4. **Language-induced argument corruption**: the MLCL benchmark paper "Lost in Execution" (Jan 2026; Chinese, **Hindi**, Igbo) finds the dominant multilingual failure is *parameter value language mismatch* — the model picks the right tool but writes the argument value in the user's language (e.g. a Hindi ledger name or Devanagari digits where an enum/ISO value is required), and no inference-time fix "can fully recover English-level performance".
5. Reasoning-model conflicts: structured-output enforcement disables reasoning (Fireworks), and Kimi K3 requires the full `reasoning_content` echoed back or multi-turn state breaks.
6. Benchmark drift under realistic MCP loads: MCPMark reports GPT-5 Medium dropping 52.6% -> 33.9% pass@1 and Claude Sonnet 4 28.1% -> 12.6% between runs — long tool lists and long horizons degrade everyone, open or closed.

**Mitigations that work (and that we should design in):**
- Constrained decoding for every tool call: Fireworks' `json_schema` + implicit `unevaluatedProperties:false` kills hallucinated fields; grammar mode for enum-only decisions (ledger group, voucher type, GST rate slab). Self-hosted vLLM/SGLang get the same via XGrammar/Outlines.
- Keep the tool surface small per turn (a router picks 5–8 tools from the full set; VAREX and MCPMark both show accuracy collapses with tool/schema sprawl); ids not names for arguments (ledger_id, item_id) so the model never has to transliterate.
- Canonicalise arguments server-side: Indic numerals, lakh/crore words, dd-mm-yyyy, Hinglish ledger aliases -> resolved ids before validation; return a structured validation error and let the model retry once (bounded).
- Prefer non-thinking mode for tool steps and thinking only for the plan step; never mix `json_schema` with reasoning on the same call.
- Log every trajectory: this is the dataset for §6.

### Unverified or weakly-sourced items (flagged)
- Fireworks "US-only" premium: docs page summary said 10% in one place and "50% premium applies September 1, 2026" in another — **re-check on the live pricing page before quoting**.
- Fireworks RFT supported-base list (Llama, Phi, Qwen 2.5/3, DeepSeek V3/R1, Kimi K2) is from 2025 blog posts; **V4 / K3 / GLM-5.x support not confirmed**.
- Fireworks "document inlining" feature page returned 404 — **existence/pricing unverified**.
- Fireworks per-token prices for Kimi K2.6, gpt-oss-120b and Qwen3.5 small models come from third-party aggregators (Morph, Artificial Analysis medians), not the Fireworks page.
- Groq and Cerebras catalogs: only gpt-oss / Llama / Whisper confirmed from Groq docs; **no evidence they serve GLM-5.3, DeepSeek V4 or Kimi K3**. Baseten, Novita, Nebius were **not researched** (search budget exhausted).
- Aggregated tau2-bench numbers (GLM-5.2 99.1%, DeepSeek V4 Pro 96.2%, Kimi K2.6 95.9%) are from BenchLM's uncontrolled ledger; vendor-card tau2 numbers (Gemma 4 31B 86.4, Qwen3.5-4B 79.9, Sarvam-105B 68.3) are self-reported.
- Kimi K3 and GLM-5.3 license terms are paraphrased from model cards / secondary write-ups; **read the license files before shipping**.
- Sarvam Inference hosting of GLM-5.3 / DeepSeek V4 Flash / Gemma 4 is labelled "beta" on Sarvam's pricing page; India-residency of that hosting is implied by Sarvam's positioning, **not stated on the pricing page**.
- Krutrim model prices (₹7–11/1M) are from a Techjockey listing, not Krutrim's own page.
- Bhashini (government) commercial API pricing not found; bhashini.ai is a private company.
- Cohere Command A: no 2026 update found; the 111B / CC-BY-NC facts are from memory of the March 2025 release.
- Cost-model token shapes (§4) are our engineering estimates, not measurements; ₹88/$ is an assumption.

---

## 4. Cost model per task

Assumptions: ₹88/$; Fireworks Standard prices; prompt caching on for the static prefix (system prompt + tool schemas + DSL grammar); frontier comparison uses Anthropic's list prices fetched 8 Sep 2026 (Claude Fable 5.1 $10 / $0.25 cached / $50; Claude Sonnet 5 $2 / $0.20 / $10; Haiku 4.5 $1 / $0.10 / $5; GPT-6 Astra is also $10 / $50 with $1 cached). Token shapes are engineering estimates for our app.

| Task | Token shape | GLM-5.3-Flash | DeepSeek V4 Flash | GLM-5.3 | DeepSeek V4 Pro | Kimi K3 | Claude Sonnet 5 | Claude Fable 5.1 |
|---|---|---|---|---|---|---|---|---|
| (a) NL query -> typed DSL | 2.5k cached prefix + 300 fresh in + 150 out | **$0.00020** | $0.00018 | $0.0016 | $0.0011 | $0.0039 | $0.0026 | $0.011 |
| (b) Invoice extraction (VLM, 1 page) | ~2k image + 1k prompt in, 600 out | Gemma 4 31B on DeepInfra **$0.0006**; Qwen3.8-27B on Fireworks generic $0.0032; PaddleOCR-VL self-host ~$0.0002 | — | — | — | — | $0.012 | $0.060 |
| (c) Agentic voucher creation, 5 tool calls | 40k cached re-reads + 3k fresh in + 1.2k out | $0.0023 | $0.0018 | **$0.020** | **$0.0105** | $0.039 | $0.026 | $0.100 |
| (d) Nightly insight per tenant (batch, 50% off) | 20k in + 1.5k out | **$0.0019** | $0.0027 | $0.0081 | $0.016 | $0.041 | $0.0275 | $0.1375 |

Per-tenant month (200 NL queries, 150 invoices, 100 agentic vouchers, 30 nightly runs):
- **Open routed stack** (Flash for a/d, Gemma 4 31B for b, GLM-5.3 for c): ≈ **$2.19 ≈ ₹190/month**; swapping c to DeepSeek V4 Pro ≈ $1.24 ≈ ₹110.
- **Claude Sonnet 5 everywhere**: ≈ $5.75 ≈ ₹505. **Claude Fable 5.1 everywhere**: ≈ $25.3 ≈ ₹2,230.
- Agentic voucher creation dominates (>90% of open-stack spend). If a fine-tuned Qwen3.8-27B / Qwen3.5-35B-A3B handles 70% of vouchers at ~$0.003, the tenant month drops under $1.
- Sanity check against Tally's price point (TallyPrime Silver ≈ ₹750/month equivalent): an AI layer costing ₹110–190/tenant-month in tokens is viable; ₹2,000+ is not.

---

## 5. Small models for edge / cheap classification

- **Qwen3.5-4B** (Apache 2.0, multimodal, 262K ctx): BFCL-V4 50.3, tau2 79.9, IFEval 89.8, MMMLU 76.1 — comparable to gpt-oss-120b on several axes per third-party writeups. **Qwen3.5-0.8B/2B** exist for on-device.
- **Gemma 4 E2B / E4B** (Apache 2.0): E2B <1.5GB quantized, E4B ~5GB at 4-bit, 128K ctx, native audio in, "built for agents and tool calling on constrained hardware"; ARC-C 83.7% for E4B. Best candidate for an offline Android voucher-entry assistant.
- **Granite 4.2-3B** (Apache 2.0): BFCL-v4 52.4 — unusually strong tool calling for 3B.
- **Direct evidence for our use-cases**: "How Small Can You Go?" (arXiv 2606.08051, Jun 2026) LoRA-tuned 270M–8B models (Gemma 3, **Qwen 3.5 0.8B–4B**, Aya 3.35B, Llama 3.1-8B) for **merchant extraction from bank-transaction descriptors**: production Llama-8B r32 96.95 F1; **Qwen3.5-4B 96.60 F1 (91.7% exact match) at 3.8x the throughput; Qwen3.5-0.8B still 94.75 F1**. VAREX (Mar 2026) shows sub-4B models suffer "schema echo" (-45 to -65 pp) untuned but **extraction-specific fine-tuning at 2B yields +81 pp**. Practitioner writeups fine-tune Llama 3.2-3B with Unsloth for bank categorisation ("BankAssist-SLM").
- Use: ledger suggestion (top-k ids from embedding search, reranked by a 4B classifier), narration autofill (0.8–4B, grammar-constrained), bank-line categorisation (fine-tuned 4B; fall back to Flash when confidence < threshold), WhatsApp intent routing.
- **Speculative decoding**: same-family small models are the drafters (Fireworks pairs Qwen 2.5 0.5B with Qwen targets); n-gram speculation is ideal for DSL/JSON whose output copies the prompt. Fireworks serverless already has drafters configured; on dedicated deployments we can supply an EAGLE head trained on our own traces. Distillation: use GLM-5.3 / DeepSeek V4 Pro trajectories that passed approval as SFT data for Qwen3.5-4B/9B.

---

## 6. Fine-tuning strategy on open models — what the evidence says

Pipeline: (1) log every agent trajectory with the human approve/edit/reject signal; (2) **SFT/LoRA** on approved trajectories (Fireworks LoRA-SFT $0.50/1M tokens <=16B; Together $0.48); (3) **DPO** from accept-vs-edit pairs ($1.00/1M on Fireworks); (4) **RFT/GRPO** with a *verifiable* reward — for us the reward is deterministic: does the resulting voucher balance, pass GST validation, match the approved ledger ids, and reproduce the human's final posting? That is exactly the regime where small models beat frontier ones.

Evidence that a fine-tuned 4B–32B model can match or beat frontier models on narrow tool-use:
- **FermiSense catalog-review case (Developers Digest, 2026)**: Qwen 3.5 9B + GRPO, 1,000 labelled examples, ~2,500 RL steps, ~$500 on one 80GB GPU: 64.2% -> **97%**, vs GPT-5.6 Sol 93% and Claude Opus 4.8 91%; inference $0.50 per 1,000 listings vs $20–34 frontier. Caveat stated: only for "narrow, verifiable tasks".
- **OpenPipe ART-E**: Qwen 2.5 14B with GRPO + RULER (LLM-judge ranking 4–8 trajectories) beat o3 on an email-search agent task; RULER matched hand-tuned rewards on 3 of 4 agent benchmarks with 2–3x faster development. OpenPipe's earlier "Temporal Clue" GRPO run beat o1/o3-mini/R1.
- **Fireworks RFT**: 32B open model "exceeded GPT-4o" on agent/tool use; Vercel 93% error-free generation (+33% vs closed, 40x faster); Genspark +10% quality / -50% cost on a deep-research agent.
- **Merchant extraction paper**: 0.8–4B LoRA models within 0.2–2 F1 of an 8B production model (see §5).
- **RC-GRPO** (arXiv 2602.03025) targets multi-turn tool calling specifically with reward-conditioned GRPO; **MUA-RL** (2508.18669) trains agents against simulated users for multi-turn tool use — both open recipes usable with TRL/verifiers.
- Counter-evidence: "GRPO Does Not Close the Multi-Agent Coordination Gap" (2606.07845) — RL helps single-agent tool execution, not multi-agent coordination; keep our planner single-agent.

Tooling: Fireworks RFT + Eval Protocol (managed, per GPU-hour, dedicated serving after), OpenPipe ART (RULER), Unsloth (cheap single-GPU LoRA/GRPO), Axolotl, HF TRL, `verifiers`. Practical constraint: Fireworks serves trained LoRAs **only on dedicated deployments** (H100 $8/hr ≈ $5.8k/month) — so a fine-tune pays off only once we have enough tenants; before that, prompt-engineer on serverless GLM-5.3-Flash / Qwen3.5 and collect data.

---

## 7. Indic language and speech

- **LLMs**: Sarvam-105B is the only open frontier-adjacent model trained natively on 22 Indian languages *including romanized Hinglish and code-mixed text*, with a custom Indic tokenizer (fewer tokens per Devanagari/Gujarati sentence => cheaper). Qwen3.5/3.8 (119–201 languages) and Gemma 4 (140+ pretrain languages) handle Hindi/Gujarati/Marathi/Tamil acceptably for understanding; Hinglish specifically remains weak across generic LLMs — Hinglish sarcasm/hate-speech studies (2026) still find small fine-tuned encoders (Hing-RoBERTa) beating prompted LLMs. Practical stance: Sarvam-105B or a Hinglish-SFT'd Qwen3.5-9B for the *chat* surface; English-only, id-based tool arguments underneath (the MLCL finding).
- **STT (open)**: AI4Bharat **IndicConformer-600M** (MIT, 22 languages, CTC + RNNT, 16 kHz; Hindi WER 13.2 on Vaani); IndicWav2Vec/IndicWhisper (Hindi WER 12–18% clean, 22–30% telephony per Caller Digital). **Sarvam Saarika** (API, 11 langs incl. Gujarati/Marathi/Tamil) at **₹30/hour** (₹45 with diarisation). Groq Whisper Turbo $0.04/hour but Indic accuracy is weaker. Code-switching "every few seconds" still degrades WER materially across all of them.
- **TTS (open)**: AI4Bharat **IndicF5** (0.4B, MIT, 11 languages, voice cloning from a reference clip, 1,417 h training data); IndicTTS (MOS ~3.6–3.9); Sarvam **Bulbul v3** API ₹30 per 10K characters, 30+ voices, 11 languages.
- **MT**: IndicTrans2 (1.1B, MIT, 22 languages, IN22 benchmarks); IndicTrans3-beta adds document-level MT. Sarvam Mayura ₹20 per 10K characters.
- **Bhashini** (bhashini.gov.in, government DPI): ASR/TTS/NMT in 22 languages, free for non-commercial and discounted for commercial via the Udyat API; note that **bhashini.ai is an unrelated private company** with undisclosed pricing.
- **Sarvam API platform** (India-hosted, OpenAI-compatible): Sarvam-105B ₹29.28 in / ₹10.98 cached / ₹73.2 out per 1M (~$0.80 blended); also hosts **GLM-5.3 (₹126/₹396), GLM-5.3-Flash (₹13.5/₹45), DeepSeek V4 Flash (₹19.8/₹59.4), Gemma 4 31B (₹36.6/₹91.5)** in beta; document digitisation ₹0.5/page; rate limits 60 / 200 / 1,000 RPM by plan; ₹100 free credit. This is currently the only India-hosted serverless endpoint for the open frontier models we want.
- Self-hosting speech at 100k minutes/month runs ₹10–15 lakh/year of GPU plus engineering (Caller Digital TCO ₹1.3–1.5 crore) — buy Saarika/Bulbul first, self-host IndicConformer/IndicF5 only at scale.

---

## 8. Embeddings and rerankers (open)

| Model | Size / dims / ctx | License | Languages | Scores | Fit |
|---|---|---|---|---|---|
| **Qwen3-Embedding 0.6B / 4B / 8B** | 1024–4096 dims, 32K | Apache 2.0 | 119 | 8B #1 MTEB-Multilingual 70.58 (Jun 2025); Fireworks serves 8B at $0.10/1M | Default for ledger/narration/product search; 0.6B is fine for per-tenant indexes |
| **Qwen3-Reranker 0.6B / 4B / 8B** | — | Apache 2.0 | 119 | 8B MMTEB-R 72.94, MTEB-Code 81.22 | Rerank top-50 ledger candidates before the classifier |
| **BGE-M3** (568M, 1024d, 8K) | dense + sparse + ColBERT | MIT | 100+ | MTEB ~63; strong MIRACL incl. Hindi/Bengali/Telugu | Hybrid lexical+dense in one model — good for messy narrations; pair with bge-reranker-v2-m3 |
| **jina-embeddings-v4** (4B on Qwen2.5-VL-3B, 2048d/128 multi-vector, 32K) | multimodal | Qwen Research License (check commercial) | 30+ | ViDoRe / visual-document retrieval | Retrieval over scanned invoices as images; license is the blocker |
| **Multilingual-E5 / nomic-embed** | 0.3–0.6B | MIT / Apache | 90+ / EN | MTEB ~62 | Fallbacks |
| **Qwen3-VL-Embedding / Reranker** (Jan 2026 paper) | multimodal | Apache 2.0 | — | SOTA multimodal retrieval | Alternative to jina v4 with a clean license |

MMTEB has an Indic track; none of the vendor pages we fetched publish per-language Hindi/Gujarati numbers, so run our own eval on 2–3k tenant narrations before choosing. Cost is negligible either way (Fireworks $0.008–0.10/1M; DeepInfra similar).

---

## 9. Vision-language and OCR for invoices and bank statements

**OmniDocBench v1.6 (opendatalab, 2026) end-to-end** — overall = mean of (1 − text edit dist)·100, table TEDS, formula CDM:

| Model | Params | Overall | Text edit ↓ | Table TEDS | License |
|---|---|---|---|---|---|
| PaddleOCR-VL-1.6 | 0.9B | **96.34** | 0.033 | 94.76 | Apache 2.0; 109 languages incl. **Hindi/Devanagari** |
| MinerU2.5-Pro | 1.2B | 95.75 | 0.036 | 93.42 | Apache 2.0*; 109 languages |
| GLM-OCR | 0.9B | 95.22 | 0.044 | 92.83 | MIT; 8 languages (no Indic) |
| Gemini-3 Pro (closed) | — | 92.91 | 0.064 | 89.15 | — |
| Qwen3.8-27B (self-report, v1.5) | 27B | 91.1 | — | — | Apache 2.0 |
| DeepSeek-OCR-2 | ~3B | 90.25 | 0.050 | 83.89 | MIT |
| Qwen3-VL-235B-A22B | 235B | 89.78 | 0.063 | 83.07 | Apache 2.0 |
| GPT-5.2 (closed) | — | 86.59 | 0.114 | 82.95 | — |
| Nanonets-OCR-s | ~3B | 83.61 | 0.108 | 80.18 | — |
| InternVL3.5-241B | 241B | 83.76 | 0.130 | 74.35 | — |
| Docling (pipeline) | — | 78.44 | 0.157 | 65.77 | MIT |

Also: dots.mocr (100+ languages, emits SVG for charts), Chandra OCR 2 (best handwriting, 90+ languages), PP-OCRv6-Tiny (1.5–34.5M params for edge), olmOCR-2, Marker (pipeline). Structured-field evidence: a 2026 KYC pipeline study on long scanned financial documents rates **Qwen3-VL-8B-Instruct** "state-of-the-art among open-weight VLMs" but slightly below MiniCPM-o-2.6 in that setting; on key-value extraction, Gemini-2.5-Pro reached 92.2% where Qwen2.5-VL-7B got 59.8% — the gap closes with layout-preserving text as input (+3–18 pp, VAREX) and with extraction-specific fine-tuning (+81 pp at 2B).

Hosting: a 0.9–1.2B OCR model runs on an L4/T4 (₹50–100/hr India, or ~$0.30–0.60/hr on DeepInfra-class GPUs) at hundreds of pages/minute — orders of magnitude cheaper than routing images to a 27B+ VLM at $0.90/1M (Fireworks generic) or a closed model. Fireworks' "document inlining" page (`#transform=inline`) was not reachable at fetch time; treat it as unverified.

Recommended pipeline: **PaddleOCR-VL-1.6 (layout + tables + Hindi/Gujarati script) -> layout-preserving markdown -> Qwen3.8-27B or GLM-5.3-Flash with `json_schema` for field extraction** (GSTIN regex, HSN, line items, tax splits) -> deterministic validators (GSTIN checksum, totals reconcile) -> confidence gate -> human review. Keep Qwen3.8-27B direct-image extraction as fallback for photos/handwritten kachha bills; fine-tune Qwen3.5-4B on our own corrected extractions once we have ~5k documents.

---

## 10. Recommendation — model routing plan for our build

**Tier A — agentic planner (multi-step voucher/reconciliation/GST filing flows, approval-gated)**
- Primary: **GLM-5.3** on Fireworks (Standard lane; $1.40/$0.26/$4.40; top open on AA Index, #1 Terminal-Bench 3.0, Toolathlon 73.0). Non-thinking for tool steps, `reasoning_effort: low` for the plan step.
- Cost fallback / A-B: **DeepSeek V4 Pro** ($1.32/$0.044/$3.96; MIT; cheapest cache reads of any frontier open model — a 40k cached prefix costs $0.0018).
- Quality fallback for hard cases (multi-company consolidations, forex, audit queries): **Kimi K3** ($3/$15) — only on explicit escalation because output price is 3.4x GLM.
- Closed fallback behind a feature flag: **Claude Sonnet 5** ($2/$10; $0.20 cached) for the ~2% of trajectories that fail validation twice. Never route customer PII to a closed model without the tenant's DPDP consent flag set.

**Tier B — mid (NL -> DSL, invoice/bank-statement field extraction, WhatsApp dialogue)**
- **GLM-5.3-Flash** (MIT, $0.15/$0.03/$0.50, AA 42, multimodal) as the workhorse; **DeepSeek V4 Flash** ($0.22/$0.007/$0.66 Fireworks or $0.06–0.09/$0.18 DeepInfra) as the price fallback; **Qwen3.8-27B** (Apache 2.0, OmniDocBench 91.1, vision) for image-in extraction and as the fine-tune base (dense 27B is the sweet spot for LoRA + FP8 on one 80GB GPU).
- Indic chat surface: **Sarvam-105B** via Sarvam's India-hosted API (₹29/₹73) for Hindi/Gujarati/Marathi/Tamil and Hinglish; tool arguments always in English ids.

**Tier C — small (classification, ledger suggestion, narration autofill, bank-line categorisation, intent routing)**
- **Qwen3.5-4B** (Apache 2.0) fine-tuned with LoRA on our labelled lines (paper evidence: 96.6 F1 on bank-descriptor extraction); **Gemma 4 E4B** for on-device/offline Android; **Granite 4.2-3B** as a tool-calling alternative. Grammar-constrained outputs (enum of ledger ids) — never free text.
- Embeddings: **Qwen3-Embedding-0.6B** per tenant + **BGE-M3** sparse for narrations; **Qwen3-Reranker-0.6B**.
- OCR: **PaddleOCR-VL-1.6** self-hosted on an India GPU.
- Speech: **Sarvam Saarika/Bulbul** APIs now (₹30/hr, ₹30/10K chars); **IndicConformer-600M + IndicF5** self-hosted later.

**Providers**: Fireworks as primary (feature surface, RFT/Eval Protocol, session-affinity caching, batch, SOC 2/HIPAA, ZDR) with **DeepInfra** as the cheap secondary and **Together** as the tertiary for the same model ids (all OpenAI-compatible; keep a provider abstraction and per-model golden tests since chat templates and tool-call parsers differ). Use Groq/Cerebras only if we ever need >500 tok/s on gpt-oss-class models (not planned). Run all Tier-B/C batch work (nightly insights) through the 50%-off Batch API with 24h windows.

**Data residency (DPDP Act 2023 + DPDP Rules 2025)**: the Rules are notified and phase in over 18 months (consent-manager rules in 2026, full obligations by mid-2027); Rule 15 uses a **negative list** — cross-border transfer is allowed except to blacklisted countries, with extra conditions for Significant Data Fiduciaries and for making data available to foreign states. So US/EU inference is legal today for an SMB accounting SaaS, but (a) financial books of Indian businesses are exactly the category regulators and enterprise buyers care about, (b) Fireworks has **no India region** (nearest: Tokyo, Malaysia, Sydney; region-pinning costs +50% and needs sales), and (c) a CA-firm or bank-partner channel will demand India-resident processing. Stance: **default all Tier-B/C and OCR/embedding traffic to India-hosted inference (Sarvam Inference for GLM-5.3-Flash / DeepSeek V4 Flash / Gemma 4 / Sarvam-105B; self-hosted PaddleOCR-VL, Qwen3.5-4B, embeddings on E2E Networks or Yotta Shakti Cloud)**, and route only the Tier-A planner to Fireworks (GLOBAL or APAC) under zero-data-retention with tenant-level consent and PII minimisation (ids, amounts, masked party names). Plan for a fully India-resident mode (self-hosted GLM-5.3-Flash on 2xH100 at Yotta/E2E) as a paid tier for regulated customers.

**What to build first**: (1) the provider abstraction + trajectory logger; (2) grammar/JSON-schema constrained tool calling with argument canonicalisation and the one-retry policy; (3) the OCR -> layout-text -> Flash extraction pipeline; (4) an internal tau2-style eval of 200 accounting tasks (Hindi + English + Hinglish) to pick GLM-5.3 vs DeepSeek V4 Pro on *our* distribution rather than on leaderboards; (5) LoRA on Qwen3.5-4B for bank-line categorisation as the first fine-tune (cheapest, most verifiable).

---

## Sources (fetched or seen in search results, 8 Sep 2026)

1. https://docs.fireworks.ai/serverless/pricing
2. https://fireworks.ai/pricing
3. https://docs.fireworks.ai/guides/prompt-caching
4. https://docs.fireworks.ai/guides/batch-inference
5. https://docs.fireworks.ai/structured-responses/structured-response-formatting
6. https://docs.fireworks.ai/guides/function-calling
7. https://docs.fireworks.ai/deployments/regions
8. https://docs.fireworks.ai/deployments/speculative-decoding
9. https://docs.fireworks.ai/fine-tuning/fine-tuning-models
10. https://docs.fireworks.ai/guides/quotas_usage/rate-limits
11. https://fireworks.ai/blog/fireworks-rft
12. https://fireworks.ai/blog/reinforcement-fine-tuning
13. https://fireworks.ai/blog/eval-protocol-rl-on-your-agents
14. https://www.together.ai/pricing
15. https://deepinfra.com/pricing
16. https://pricepertoken.com/endpoints/compare/deepinfra-vs-fireworks
17. https://console.groq.com/docs/models
18. https://www.cerebras.ai/pricing
19. https://artificialanalysis.ai/models/open-source
20. https://artificialanalysis.ai/articles/analysis-openai-gpt-oss-models
21. https://llm-stats.com/benchmarks/bfcl-v4
22. https://gorilla.cs.berkeley.edu/leaderboard.html
23. https://benchlm.ai/benchmarks/tau2-bench
24. https://openrouter.ai/blog/insights/the-open-weight-models-that-matter-june-2026/
25. https://huggingface.co/moonshotai/Kimi-K3
26. https://roo.beehiiv.com/p/kimi-k3-open-weights-license-benchmarks
27. https://huggingface.co/zai-org/GLM-5.3
28. https://innfactory.ai/en/ai-models/glm/
29. https://huggingface.co/deepseek-ai/DeepSeek-V4-Pro
30. https://www.morphllm.com/deepseek-v4
31. https://huggingface.co/MiniMaxAI/MiniMax-M3
32. https://huggingface.co/Qwen/Qwen3.8-27B
33. https://kingy.ai/blog/qwen3-8-27b-specs-benchmarks-local-hardware/
34. https://huggingface.co/Qwen/Qwen3.5-4B
35. https://huggingface.co/google/gemma-4-31b-it
36. https://www.labellerr.com/blog/gemma-4-open-weight-ai-model-overview/
37. https://developer.nvidia.com/blog/introducing-nemotron-3-super-an-open-hybrid-mamba-transformer-moe-for-agentic-reasoning/
38. https://www.marktechpost.com/2026/08/25/ibm-releases-granite-4-2-bringing-native-reasoning-and-agentic-rl-to-open-enterprise-models/
39. https://huggingface.co/mistralai/Mistral-Medium-3.5-128B
40. https://huggingface.co/openai/gpt-oss-120b
41. https://codersera.com/blog/llama-4-complete-guide-2026/
42. https://huggingface.co/sarvamai/sarvam-105b
43. https://www.buildfastwithai.com/blogs/sarvam-105b-india-s-open-source-llm-for-22-indian-languages-2026
44. https://techcrunch.com/2026/02/18/indian-ai-lab-sarvams-new-models-are-a-major-bet-on-the-viability-of-open-source-ai/
45. https://docs.sarvam.ai/api-reference-docs/pricing
46. https://caller.digital/blog/open-source-voice-ai-india-sarvam-ai4bharat-bhasini-2026
47. https://huggingface.co/ai4bharat/indic-conformer-600m-multilingual
48. https://huggingface.co/ai4bharat/IndicF5
49. https://huggingface.co/ai4bharat/indictrans2-en-indic-1B
50. https://www.bhashini.ai/pricing
51. https://www.techjockey.com/detail/krutrim-cloud
52. https://qwenlm.github.io/blog/qwen3-embedding/
53. https://huggingface.co/BAAI/bge-m3
54. https://huggingface.co/jinaai/jina-embeddings-v4
55. https://www.premai.io/blog/best-embedding-models-for-rag-2026-ranked-by-mteb-score-cost-and-self-hosting/
56. https://github.com/opendatalab/OmniDocBench
57. https://blog.roboflow.com/best-open-source-ocr-models/
58. https://arxiv.org/abs/2603.15118 (VAREX)
59. https://arxiv.org/pdf/2604.26462 (KYC multistage extraction)
60. https://arxiv.org/abs/2606.08051 (LoRA 270M–8B merchant extraction)
61. https://arxiv.org/abs/2601.05366 (Lost in Execution — multilingual tool calling)
62. https://arxiv.org/pdf/2602.03025 (RC-GRPO)
63. https://arxiv.org/html/2606.07845 (GRPO and multi-agent coordination)
64. https://www.developersdigest.tech/blog/500-dollar-rl-fine-tune-beats-frontier-models
65. https://openpipe.ai/blog/using-grpo-to-beat-o1-o3-mini-and-r1-on-temporal-clue
66. https://themenonlab.blog/blog/openpipe-art-agent-reinforcement-training
67. https://huggingface.co/openai/gpt-oss-120b/discussions/151
68. https://github.com/vllm-project/vllm/issues/22337
69. https://github.com/ollama/ollama/issues/11704
70. https://community.groq.com/t/gpt-oss-120b-hallucinates-badly/1408
71. https://platform.claude.com/docs/en/about-claude/pricing
72. https://medium.com/@rohithreddy66666/gpt-6-astra-vs-claude-fable-5-1-same-headline-price-different-real-world-bill-85a6fe5c8937
73. https://www.dpdpa.com/dpdparules/rule15.html
74. https://www.medianama.com/2025/11/223-dpdp-rules-cross-border-data-transfers/
75. https://acecloud.ai/blog/dpdp-data-residency-gpu-workloads-india/
76. https://www.spheron.network/blog/gpu-cloud-india-2026/
77. https://blogs.nvidia.com/blog/india-ai-mission-infrastructure-models/
78. https://www.e2enetworks.com/
79. https://sacra.com/c/fireworks-ai/
80. https://www.morphllm.com/fireworks-ai-pricing
