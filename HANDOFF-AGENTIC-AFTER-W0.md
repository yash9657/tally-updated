# Handoff — the agent plane: **W0 CLOSED, W8 TIER 0 CLOSED, §7-2 DECIDED**, W1a next. ⛔ DRIVER: `PLAN-AGENTIC.md` §3
Updated: 2026-09-09 (after W0 — foundations landed, adversarially reviewed, every gate green,
**nothing committed**). ⛔ **One thing is blocked on the user: §6.1 (bake-off spend + provider keys). §6.2 is
RESOLVED — the spike ran; see §5 and `docs/design/d120-durable-runs-spike.md`.**

⛔ **READ `docs/design/d119-agentic-w0-foundations.md` FIRST.** It is W0's ADR: five decisions, the one
line of `RESEARCH-AGENTIC.md` they overruled, and §7's honest account of the half of D3 that is owed.
Every figure in it was printed by a command, not derived by hand — the two disagreed twice and both
times the command won.

⛔⛔ **THE ONE SENTENCE THAT MATTERS MOST BEFORE YOU PLAN W2:** `internal/statutory` **already has a
durable step engine on Postgres** — `Run`/`StepStore`/`PGStore`, migration 0013, in production on the
e-invoice IRN path — and it covers the hard half of what W2 needs with **zero new dependencies**. The
§7-2 decision as written ("DBOS Transact Go vs Restate") has a third option the plan did not consider,
and it is the only one of the three compatible with `IMPLEMENTATION_PLAN.md:25` §1.1. **See §5. Do not
run the DBOS-vs-Restate spike until you have read it.**

⚠️ **W0 IS UNCOMMITTED AND SO IS UNRELATED WORK.** `git status` mixes W0's eight new paths (§1.5) with
the pre-existing `session/t32-and-close-7-3` tree (bankstatement/name.go, gstr datasource, the D109 and
D118 export work, D114 `closePanel` in `ScreenActions.tsx`). ⛔ **`git diff` on
`frontend/src/app/ScreenActions.tsx` shows BOTH** — 97 of its insertions are D114's, not W0's. Never
`git stash`/`checkout`/`reset` to "see the baseline"; you will lose someone else's day.

## START HERE

1. ⛔⛔ **`PLAN-AGENTIC.md` §3 — THE DRIVER.** W0 is closed; the wave order is §4 of this file.
2. **`docs/design/d119-agentic-w0-foundations.md`** — what W0 decided and why. §7 is the owed half.
3. **This file §7 — the traps.** Eleven measured hazards. §7.1 hung a test binary and burned two CI
   slots in this session; §7.3 and §7.5 each sent an agent after the wrong thing. Read it before you
   write a test that touches Postgres or vitest.
4. `RESEARCH-AGENTIC.md` §4 (D1–D10) and `docs/research/agentic/r5-agent-native-app.md` §8.7 — the
   `ActionSpec`/`ActionRequest`/`ActionDecision` sketch W2 builds out.
5. `PLAN-AGENTIC.md` §7 — six operational decisions. **One is resolved (D-A, §1.1 upheld), one is
   reframed (§5), four remain (§6).**

---

## 1. What W0 landed

### 1.1 `internal/llm` — the provider abstraction (D1)

2,394 impl / 1,956 test lines, **30 tests**, zero new dependencies.

Five adapters behind one OpenAI-compatible client: `ProviderFireworks`, `ProviderDeepInfra`,
`ProviderSarvam`, `ProviderVLLM`, `ProviderAnthropic`. `Adapter.Encode`/`Decode` are **pure** (no
context, no `*http.Client`), so every provider's wire shape is a table test over real bytes with no
server. `json_schema` and grammar mode, named `tool_choice`, per-response `Fingerprint`, `Usage` with
`CachedPromptTokens`, schema repair bounded at `DefaultMaxRepairs = 2` and **observable** as
`Response.Repairs`.

⛔ **`Request.Static` / `Request.Dynamic` IS THE CACHE CONTRACT, NOT A STYLE CHOICE.** Static must be
byte-identical across calls in a feature; anything per-call (the date, resolved period windows, the
question) belongs in Dynamic. `PLAN-AGENTIC.md`'s whole cost envelope (₹130–310 per tenant-month) is
priced on provider prefix caching hitting. `TestComplete_StaticPrefixIsStableAcrossCalls` fails if the
date moves into Static.

⛔ **RESERVE-THEN-SETTLE, NOT COUNT-AFTERWARDS.** `Budget.Reserve` → `Reservation.Settle`/`Release`,
exactly one of the two on every exit path including **panic** (`budget_test.go`'s `panicTransport`
subtest is the only detector; it was added because the `defer` was pinned by nothing).

### 1.2 `internal/trajectory` — the trajectory logger (D2)

2,479 impl / 1,794 test lines, **38 tests**, green under `-race`.

A complete OTLP/HTTP **protobuf-JSON** exporter in stdlib (`otlp.go`, 487 lines of code). `gen_ai.*`
semconv pinned as a literal `SemconvVersion = "1.38.0-dev"` on every span, because those names are
still Development status and will move — **the mapping layer lives in `attrs.go` and nowhere else**, so
no dashboard hard-codes a key.

⛔ **THREE ENCODING RULES THAT FAIL SILENTLY IF BROKEN**, all byte-pinned by test: 64-bit ints
(`startTimeUnixNano`, `intValue`) are **decimal strings**; ids are **lowercase hex**; `AnyValue` is a
true one-of with **pointer** fields so `output_tokens = 0` survives instead of being eaten by
`omitempty`. A collector rejects numeric nanos, and "it is valid JSON" would sail through.

⛔ **PROMPT AND COMPLETION CONTENT IS NEVER AN ATTRIBUTE** (`PLAN-AGENTIC.md` §2.7). Spans carry
`khata.prompt.hash` and `khata.tool.args_hash`, never text. `content_test.go` proves a prompt cannot
reach an attribute through the public API. `SetError` records `error.type` and **not** the message,
because `masters/price_list.go:186` and friends interpolate party and item **names** into error strings.

⚠️ **THE PROMPT-HASH PRIMITIVE IS SHA-256, NOT BLAKE3, AND THE TWO PACKAGES DISAGREED ONCE.**
`internal/llm` hashes the static prefix with `crypto/sha256` (`client.go:529`); `trajectory`'s own
`Hash`/`HashString` are BLAKE3 and are what the **tool** hashes use. Both render as 64 lowercase hex,
so the divergence is invisible to inspection. It is now documented on both sides
(`attrs.go:91-96`). ⛔ **A cassette keyed with the wrong primitive misses and blames the wrong fix.**

Cassettes: JSONL under `internal/trajectory/testdata/cassettes/<feature>/`, mandatory header line,
replay keyed on `khata.prompt.hash` + `khata.tool.args_hash`, **`ErrCassetteMiss` with no network
fallback**, and a `replayed == 0` Fatal so a Tier-0 gate over an empty directory cannot be green. Two
seed fixtures exist: `nlq.translate/` and `extraction/`. Their headers say `SeedNote`, not
`RecordedNote`, because W0 had no provider to record against and a fixture claiming a live provider
would be a lie in a file whose whole job is provenance.

### 1.3 `internal/actions` — the ActionSpec registry (D3)

3,467 impl (mostly the generated registry) / 723 test lines, **12 tests**.

**230 specs, 168 distinct ACL keys** — `create 55, alter 34, delete 6, display 89, print 46`; 95
writes, 135 reads. The W0 gate asked for ≥ 60.

Generated by `TestActionRegistryGolden` (`internal/server/action_registry_golden_test.go`): builds the
**real** router with `srv := &Server{}; srv.routes()` (no database — the move
`route_vocabulary_test.go:81` has always made), walks it with `chi.Walk`, and calls the **real**
`routeAction` on a synthesised request. ⛔ **`ActionSpec.Action` is `authz.Action` and `Resource` is
`authz.Resource`, not strings** — so a spec cannot name a verb outside the closed vocabulary and "the
ACL key equals the arm the router took" holds by construction.

Regenerate with: `UPDATE_GOLDENS=1 go test ./internal/server/ -run TestActionRegistryGolden`

⛔ **TWO FIELDS WERE DELETED IN REVIEW RATHER THAN KEPT, AND THE REASONING IS THE MODEL FOR W2.**
`CutoffJudged` was `IsWrite` spelled twice (the probe body carried every date spelling to every route,
and `voucherDateFromRequest` falls through to a generic body walk) — and worse, **counterfactual**:
`DELETE /members/{userID}` and `DELETE /books-closure` send no body, so the cutoff never runs, yet the
column claimed it governs deleting a company member. W2 builds an autonomy table on these fields.
`Description` was renamed `Label` because every row read `"<ACLKey> (<METHOD> <route>)"` and no
embedding can separate `delete security (DELETE .../members/{userID})` from
`delete security (DELETE .../members/invites/{inviteID})`. What survives is `DateSubject`, which
answers the same with an **empty** body — 3 of 230 name one (bank date 2, deposit date 1).

### 1.4 `internal/nlq` re-pointed (D4)

All wire code gone; `nlq` now hands a formed prompt to `internal/llm`. The exported error contract is
unchanged: `ErrTranslatorUnavailable` → 503 (operator fault), `query.ErrUntranslatable` → 422 (user
fault), and W0 widened only what *produces* the first (budget exhausted, provider unconfigured).

⛔ **`DefaultModel = "claude-haiku-4-5"` AND `DefaultProvider = llm.ProviderAnthropic` ARE DELIBERATE
AND W1 IS WHAT CHANGES THEM.** `PLAN-AGENTIC.md` W0 says translation moves to a cheap open-weight
flash model; the default stayed put because the alternative is that "we refactored the LLM layer"
presents in the field as "NL query got worse the day we upgraded", with the cause two packages from the
symptom. **Change both constants together, when the bake-off has numbers.**

⚠️ `translatorMaxRepairs = 1`, not `llm`'s default of 2, and it is not an oversight: a human is
watching a query box, and every repair is a billed round trip **and** a round trip of latency.
`translatorPromptVersion = "nlq-dsl-v2"` — **bump it whenever `staticPrompt()` changes meaningfully**,
or a W8 eval score gets credited to the wrong prompt.

### 1.5 The eight new paths, and the boundary edits

```
backend/internal/trajectory/                    NEW  (allowedDeps {} — boundaries_test.go:267)
backend/internal/llm/                           NEW  (allowedDeps {platform/db} — :289)
backend/internal/actions/                       NEW  (allowedDeps {authz} — :313)
backend/migrations/0091_llm_token_budget.sql    NEW  (13 columns, all COMMENT-ed)
docs/design/d119-agentic-w0-foundations.md      NEW  (the ADR)
frontend/src/actions/                           NEW  (uiCommandManifest.ts + .test.ts)
frontend/src/api/goldens/action-registry.json   NEW  (generated)
frontend/src/api/goldens/ui-commands.json       NEW  (generated)
```

Modified: `boundaries_test.go` (3 new keys + `nlq` widened at :244 + three lines in server's set at
:690/:696/:702), `internal/server/server.go` (`buildLLM`, `buildBudget`, the trajectory adapter, the
scope middleware), `cmd/cored/main.go` (recorder lifecycle + `signal.NotifyContext`),
`internal/nlq/*`, `frontend/src/app/ScreenActions.tsx`, `.github/workflows/ci.yml`,
`PLAN-TALLY-PARITY.md` §2.5. New server tests: `action_registry_golden_test.go`,
`agent_wiring_test.go`, `llm_budget_wiring_test.go`, `llm_span_error_type_test.go`.

⛔ **`allowedDeps["internal/query"]` (`boundaries_test.go:226`) IS UNTOUCHED AND MUST STAY SO.**
`TestQueryPackageIsNetworkFree` scans only `internal/query`'s **own** files for `net`/`os/exec` and does
**not** follow transitive imports — so adding `internal/llm` there would hand the SQL compiler a socket
and leave that test green while it happened. **The line in `allowedDeps` is the invariant; the test is a
second opinion.**

⚠️ **A NEW PACKAGE NEEDS TWO EDITS.** Its own entry passes when tested alone; the missing line in
`internal/server`'s set fails **only in the full suite**. `boundaries_test.go:670-673` says so in terms:
*"which is how it gets missed"*.

### 1.6 New environment variables (all default to the hermetic path)

```
LLM_PROVIDER   fireworks|deepinfra|sarvam|vllm|anthropic   unset => FakeTranslator, as today
LLM_API_KEY    no compiled-in default, ever
LLM_MODEL / LLM_BASE_URL / LLM_TIMEOUT
LLM_BUDGET_TOKENS_PER_MONTH    unset => NoBudget + a logged line; set-but-unparseable => REFUSE TO BOOT
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT   unset => Nop() + one line saying tracing is off
OTEL_EXPORTER_OTLP_HEADERS ("k=v,k=v")  /  OTEL_SERVICE_NAME (default "cored")
TRAJECTORY_SAMPLE_RATIO   default 1.0; 0 is a MEANING (export nothing), not an absence
```

⛔ **THE SHIPPED DEFAULT IS HERMETIC AND MUST STAY SO.** With no keys and no network every gate runs and
passes, exactly as it did with `ANTHROPIC_API_KEY` unset. A W1 or W2 change that makes CI need a key has
broken the thing that lets anyone work on this repo.

---

## 2. Gate baselines at HEAD — pin these, a regression is otherwise invisible

| Gate | Command | Baseline |
|---|---|---|
| backend build | `cd backend && go build ./... && go vet ./...` | clean |
| **no new deps** | `git diff -- backend/go.mod backend/go.sum` | **0 lines** |
| offline build | `GOFLAGS=-mod=readonly GOPROXY=off go build ./...` | clean |
| backend tests | `cd backend && go test ./... -count=1` | **exit 0, 39 packages ok** |
| W0 packages | `CORED_SKIP_DB=1 go test ./internal/{trajectory,llm,actions}/` | **38 / 30 / 12 tests** |
| frontend types | `cd frontend && pnpm exec tsc --noEmit` | clean |
| frontend tests | `cd frontend && pnpm run test:run` | **122 files / 1954 tests** |
| corpora | `python3 corpora/validate.py` | `tax=14 tds=15 tcs=5 keystroke=33 isolation=13 migration=6 forecast=4 inventory=11 gstr2b=17 ims=12 payroll=19` |
| **Tier 0** | `make tier0` (or `CORED_SKIP_DB=1 TRAJECTORY_RECORD= go test ./internal/trajectory/`) | **2 cassettes / 3 interactions / 2 features** |
| durable spike | `go test ./internal/statutory/ -run 'TestDurablePark\|TestConcurrentPollers\|TestAdvisoryLock'` | 4 pids, commit×1; F5 defect + fix both green |
| insightd | `cd insightd && .venv/bin/python -m pytest` | **166 passed** (unchanged by W0) |

⚠️ **JUDGE THE FRONTEND BY ITS PRINTED `Test Files` LINE, NEVER BY THE EXIT CODE.** Measured again this
session: a run with `1 failed | 121 passed` **exited 0**.

---

## 3. ⛔ What W0 OWES — three things, stated rather than buried

**3.1 The keystroke half of the D3 coverage join.** The gate is green on the *weaker* of its two
halves. `ui-commands.json` ships 281 commands and **not one carries an `acl` claim**, so
`Source.UICommandsJoined == 0`; what closes the gate instead is a client-call census
(`ClientCallsJoined == 162`, `ClientCallsUnused == 10`). `Channel` therefore reads **`client`** on 203
of 230 rows and **never `keystroke`** — the evidence is "a shipped client requests this route and the
app references that method", not "this key presses it". 27 rows carry individually-argued opt-outs and
`maxActionsWithNoHumanPathYet` is pinned at 27.

⛔ **THE SEAM THAT WAS MEANT TO SUPPLY IT WAS DELETED, DELIBERATELY.** Lane E opened a
`calls?: readonly (keyof CoredClient)[]` parameter on `useRegisterScreenAction` plus a
`DECLARED_CLIENT_CALLS` map. Nothing fed it (47 call sites, none passing a third argument), nothing read
it, and **it could not have worked in that shape**: it registered inside a `useEffect`, so it held only
what was *mounted*, and `uiCommandManifest.ts` runs in a bare Node process where the map is empty by
construction. It was removed rather than left standing, because a typed, documented, exported API wired
to nothing is how the next reader concludes the gate is fed — `authz/vocabulary.go:24-57` on the deleted
`export` token is the same defect and the more expensive one. **The replacement is written down** in
`uiCommandManifest.ts`'s `ClientCall` doc: a plain `const` exported from each screen module beside its
handler, imported the way `SCREEN_ACTIONS` and `GATEWAY_MENU` already are. The Go generator **already
reads and prefers `commands[].acl`**, so a real claim sharpens those rows with no Go change — only a
regeneration.

**3.2 Two W0 gate lines are not achievable in W0, and were not faked.** `PLAN-AGENTIC.md` W0's gate says
*"trajectories visible in a local Langfuse"* (needs Langfuse deployed — that is W8) and *"NL→DSL
exact-AST + result-set equality ≥ current Claude baseline − 0.5 pt"* (needs a live provider and a
bake-off — that is W1). The NL→DSL **golden suite is green**; the *comparative* claim is unmade.

**3.3 Four real write routes with no operator in front of them** — surfaced by the census on its first
run, and not W0's to fix. `client.alterBudget`, `alterBudgetLine`, `deleteBudget` and `deleteBudgetLine`
all exist in `rest.ts`; `BudgetsScreen.tsx` calls none of them. So the screen creates and lists budgets
and **nothing in the product alters or deletes one**, while four write routes into the books stand open.
They are in `actionsWithNoHumanPathYet` with that reason. ⛔ **The agent layer will read those rows and
walk through them happily** — decide in W2 whether they get a UI, a deny, or an ActionSpec that is
agent-only by policy.

---

## 4. The next wave, in order

`PLAN-AGENTIC.md` §4 puts W0+W1+W8-start in P0 and W2/W3/W5 in P1. W0 is closed. The honest order:

**4.1 W1a — the golden sets, with NO spend and NO keys (do this first, it is unblocked).**
W1 splits cleanly and the plan does not say so. The **suites are fixtures and a harness**; only the
*runs* need providers. Buildable today: the 200-task state-based suite (voucher entry incl. item
invoices, bank match, GSTR-1/3B prep, NL query, master creation) in English/Hindi/Hinglish, **judged by
ledger state after the run**, `pass^5`; the 100-invoice + 50-bank-statement extraction set with
field-level labels (HDFC, ICICI, SBI, Axis, Kotak); the 2–3k narration Hinglish embedding eval. These
become W8's Tier 1/2 evals, so building them early costs nothing and gates everything.

**4.2 W8 Tier 0 — ✅ DONE (2026-09-11). The no-LLM cassette replay is now a CI gate.**
`backend/internal/trajectory/tier0_test.go` (NEW), the `tier0` job in `.github/workflows/ci.yml` (NEW),
and `make tier0` (NEW, and added to `make verify`).

- **The census is repo-wide, not one directory.** `findCassetteRoots` walks the whole backend module for
  `testdata/cassettes`, so cassettes recorded by `internal/llm` (W1b) or `internal/agent` (W2) are
  replayed **without editing the job or the test**.
- **Two numbers are pinned, and they are not the same number:** `tier0MinCassettes = 2` (files — the
  unit PLAN-AGENTIC's "500 cassettes" is expressed in) and `tier0MinInteractions = 3` (replayed
  entries; the extraction fixture holds a model call *and* the `validate_gstin` tool call). ⛔ **The
  handoff previously said "two cassettes" and that is a FILE count — the gate found 3 interactions on
  its first run.** Both are **exact pins**, not floors: adding a cassette costs one line, and the
  constant is then the auditable claim.
- ⛔ **WHAT IT PROVES AND WHAT IT DOES NOT.** It replays each entry by *the key the entry itself
  carries*, so it proves the corpus is walkable, reachable and non-empty. It **cannot** prove the key
  matches what production would compute — a cassette records the hash, never the prompt, so there is no
  input to re-derive from. That half is the **per-feature** test's job (`TestSeedCassettes` does it with
  `PromptHash(in.prompt)`). **A new cassette feature owes a per-feature replay test that starts from the
  input**, or a prompt change that moved every key leaves both halves green.
- **The `tier0` CI job is separate from `backend` on purpose.** Same tests, but that job has a Postgres
  service and open network, so a replay path that quietly acquired either would stay green there
  forever. The `tier0` job has **no service container, no provider key, `GOPROXY=off`, `CORED_SKIP_DB=1`
  and `TRAJECTORY_RECORD=""`** — the claim is enforced by the environment, not asserted.
- ⛔ **IT DELIBERATELY HAS NO `Skip if ... not present yet` GUARD** (§7.4b). Those three guards pass a
  whole job green with a `::notice::`. A new gate must not inherit Phase-0 scaffolding.
- **Mutation-proved, all measured this session:** deleting a cassette → `SHRANK` (caught); a recorded
  payload set to `null` → caught; the payload field removed → caught; malformed JSON → caught earlier at
  `LoadDir`, with file and line. ⚠️ **The first version of the payload check was a tautology** — it
  compared `Lookup`'s return against the same in-memory entry, and was green against a cassette whose
  every payload had been tampered with. It is now a kind→payload presence + validity check, which is the
  strongest claim a census with no inputs can make.
- Also fixed: `cassette.go:501` referenced `TestCassetteHarnessIsNotVacuous`, **which never existed** —
  the same documented-name-wired-to-nothing defect `authz/vocabulary.go:24-57` records.

**4.3 W1b — the bake-off runs. ⛔ BLOCKED ON §6.1 (spend + keys) AND §6.4 (licences).**
GLM-5.3, DeepSeek V4 Pro, Qwen3.8-27B, Sarvam-105B, Gemma 4 31B across Fireworks / DeepInfra / Sarvam;
measure success, **malformed-call rate**, tokens, latency, ₹ per task. Gate: a written model-routing
decision **with numbers**, replacing D3's assumptions — and then, and only then, change
`nlq.DefaultModel` + `nlq.DefaultProvider` together (§1.4).

**4.4 W2 — action layer, agent loop, approval inbox (6 wk). The big one. ⛔ Read `d120` §6 first —
five things W2 owes the durable-runs design before it adopts it.**
W0 handed it the registry, the PDP join, the provider client, the budget and the span. What it needs
that does not exist: `internal/agent` (plan-and-execute, tool search, the deterministic verifier =
the existing voucher/GST/master validators), `internal/policy` (agent principal, risk classes, autonomy
L0–L3), the **intent log** (INSERT-only, `prev_hash`, nightly anchor), the approval inbox as a new
`ScreenKind`, and durable runs (§5).

**4.5 W3 and W5 in parallel with W2** once W2's `ActionSpec` risk fields exist. W3 (semantic catalogue,
`tenant_profile`, `entity_facts`, embeddings, Tier B/C) is the one that makes answers *specific*; W5
(PaddleOCR-VL + open-weight extraction) is the one that needs W1b's extraction numbers first.

⚠️ **DO NOT START W6.** `PLAN-AGENTIC.md` §4 sequencing: W2 before W6, W3 before W4, W7 needs ≥6 weeks
of W2/W4 signals, W9 before any regulated-partner deal.

---

## 5. ⛔⛔ The decision that changed: durable runs (§7-2)

`PLAN-AGENTIC.md` §7-2 says: *"Choose DBOS Transact Go vs Restate after a one-week spike (three-day
approval across a restart)."* **Both are new Go modules, and `IMPLEMENTATION_PLAN.md:25` §1.1 is
binding: "NO new module dependencies."** W0 upheld §1.1 by hand-writing a 487-line OTLP exporter rather
than taking the OTel SDK (d119 §1). The same rule applies here, and it bites much harder — a workflow
engine is not 487 lines.

**There is a third option, already in this repo, already in production.** `internal/statutory`:

| piece | where | what it gives |
|---|---|---|
| `StepResult{Name, Output}` | `runner.go:11-16` | a recorded step |
| `StepStore{Load, Append}` | `runner.go:18-23` | `workflowID` is the idempotency key |
| `Run(ctx, store, workflowID, steps)` | `runner.go:79-104` | **memoised replay**: a completed step is reused, never re-run |
| `MemStore` / `PGStore` | `runner.go:27`, `pgstore.go:69` | Postgres-backed, **migration 0013** `statutory_step` |
| `PRIMARY KEY (workflow_id, step_name)` | `0013:58` | what makes `Append` idempotent |

It drives the e-invoice IRN workflow, where exactly-once matters because the government issues the IRN
once. **That is the hard half of durable execution and it is proven here.** A step error aborts `Run`
**without recording that step**, so a re-`Run` retries only the failed step and the tail; a
fully-completed workflow re-`Run` performs zero executions.

⚠️ **AND W0 ALREADY SHAPE-MATCHED IT ON PURPOSE.** `trajectory.StepResult` is field-for-field
`statutory.StepResult` and `trajectory.FileStepStore` has exactly `statutory.StepStore`'s method set
(`cassette.go:571-589`). It cannot *be* one — Go satisfies interfaces on named types and
`internal/trajectory`'s `allowedDeps` is `{}` — but the six-line bridge for a package that may import
both is written out verbatim in that comment. **A cassette is then "a StepStore backed by a file", and
record/replay is a one-line store swap.**

**What it does NOT give, and this is exactly what the spike must measure.** There is no `Recv`, no
per-run topic, no timers, no days-long timeout. The plan's requirement is *"approval = `Recv` on a
per-run topic with a days-long timeout; a three-day approval survives a `cored` restart"*.

⭐ **THE STRONG ARGUMENT, WHICH IS WHY THE SPIKE QUESTION CHANGES SHAPE:** a park-and-wake built on step
replay has **no in-memory state to lose**. Write the approval step as *"read the decision from the
approvals table; if absent, return a sentinel"* — the run aborts, nothing is recorded, and a poller
(`pg_cron`, or the existing `notify` outbox pattern) re-invokes it later. A parked run is then just an
unrecorded step plus a row in a table. A three-day approval surviving a restart is not a feature you
buy; it is the **absence** of durable in-process state.

### ✅ RESOLVED 2026-09-11 — **neither DBOS nor Restate. §1.1 stands, unamended.**

⛔ **READ `docs/design/d120-durable-runs-spike.md`.** The spike ran the W2 gate itself against
`statutory.Run` + `PGStore`, against the live database, with **real process restarts** (four distinct
pids, asserted). Evidence: `backend/internal/statutory/park_resume_spike_test.go`.

**A 72-hour approval survived four process lifetimes and the money step executed exactly once.** The gate
clause is met with one substitution — park-by-polling instead of `Recv` on a topic — and that
substitution is the point: a topic subscription is in-process state to lose; the polled design has none.

⛔⛔ **BUT THE SPIKE FOUND THE THING THAT DECIDES THE DESIGN, AND IT IS NOT IN THE PLAN.** `PGStore`
discharges its no-row-lock race with *"single-instance statutoryd remains the deployment shape"*. **That
assumption does not carry over: the agent plane runs inside `cored`, which is horizontally scaled.**
Measured — **two concurrent pollers executed the money step 2 times while the step store recorded 1.**
The durable record does not reveal the double execution; the books get two vouchers and the store shows
one. The fix is measured too: a per-run `pg_try_advisory_lock(hashtext(workflow_id))` held across `Run`
→ **exactly once under contention**, with no new table, no migration and no new dependency.

⛔ **W2 owes five things before adopting this — d120 §6.** F1 `Run` cannot distinguish parked from
failed (needs an exported `ErrParked`); F2 **expiry is not terminal — a late approval still commits**,
measured; F3 the advisory lock is mandatory, not an optimisation; F4 plumb `ctx` through `StepStore` at
adoption, not later; F5 the poller itself does not exist.

⚠️ `statutory.StepStore`'s methods predate context plumbing (`pgstore.go:25-33` says so and says the
fix is worth doing). If W2 adopts it, plumb `ctx` **then**, not later.

---

## 6. Decisions the user must take before the wave starts

**6.1 ⛔ Approve the bake-off spend and supply provider keys** (`PLAN-AGENTIC.md` §7-1: "the first
spend"). W1a needs neither; **W1b cannot start without both**. Needed: Fireworks, DeepInfra and Sarvam
accounts, and a ceiling. Without this, the model-routing half of D3 stays an assumption and
`nlq.DefaultModel` stays on Claude.

**6.2 ✅ RESOLVED 2026-09-11 — no longer a decision to take.** The spike ran; see §5 and
`docs/design/d120-durable-runs-spike.md`. Verdict: neither DBOS nor Restate; build on `statutory.Run` +
`PGStore` + a poll-driven re-invoker **with a per-run advisory lock**. `IMPLEMENTATION_PLAN.md:25` §1.1
stands unamended.

**6.3 Residency posture for pilot tenants** (§7-3): Fireworks Tier-A egress with redaction and a consent
flag, versus India-only from day one. This decides whether W9 is a late optimisation or an early
constraint, and it changes W1b's provider list.

**6.4 ⛔ Read the GLM-5.3 and Kimi K3 licence files before either enters production** (§7-6). This is a
**precondition of W1b**, not a follow-up: measuring a model you may not ship is wasted spend.

**6.5 Deferred, correctly.** §7-4 (WhatsApp BSP vs direct Cloud API) is W6 and W2 must land first. §7-5
(counsel on the GST filing boundary, Rule 26 / Rule 83(8), and the DPDP reading of the priors schema) is
before W7 — but it has a long lead time, so **start it now** rather than when W7 begins.

---

## 7. Traps measured in this session — read before writing tests

**7.1 ⛔⛔ `t.Fatalf` INSIDE A `RunInReadOnlyTx` CLOSURE HANGS THE SUITE.** `t.Fatalf` runs
`runtime.Goexit`, which unwinds **past the rollback**; the connection is never returned to the pool and
the next acquire blocks until the test binary times out. Observed this session as a hang, not a failure,
occupying two CI slots. ⛔ **Carry every outcome out of the closure and assert after it returns.**
`llm_budget_wiring_test.go` does this with a comment recording the measurement.

**7.2 ⛔ A `t.Skip` ON AN UNREACHABLE DATABASE IS A GREEN RUN WITH THE CONTROL NEVER EXECUTED.**
`budget_db_test.go` shipped that way and was fixed: keep the `CORED_SKIP_DB` skip, but a database that
is *unreachable while `CORED_SKIP_DB` is unset* is now a `t.Fatalf`. `handler_itc_test.go` is the shape
to copy. The token cap decides whether a tenant can spend without limit.

**7.3 ⛔ `pnpm run test:run -- <filter>` SILENTLY IGNORES THE FILTER** — all 122 files run. Same pnpm
`--`-forwarding failure that made `pnpm test -- --run` hang. Use `pnpm exec vitest run <filter>`. Note
the recipe printed by `uiCommandManifest.ts` and `.test.ts` still says the misleading spelling.

**7.4 `.github/workflows/ci.yml:133` was fixed** (`pnpm test -- --run` → `pnpm run test:run`), which is
what makes a stale `ui-commands.json` able to turn CI red at all. ⚠️ **Two more ci.yml gates still
cannot fail, left alone as out-of-scope restructuring:** (a) the "Validate YAML parses" step globs
`infra/k8s/**` and `infra/observability/**` and **exits 0 on an empty list** ("checked 0 YAML files") —
a scanned-nothing hole against this repo's own walker rule; (b) the three `Skip if X not present yet`
guards pass a **whole job green with a `::notice::`** if `backend/go.mod`, `frontend/package.json` or
`insightd/pyproject.toml` moves. Both are Phase-0 scaffolding that has outlived its reason.

**7.5 ⚠️ `EWayBillScreen.test.tsx` IS A KNOWN PRE-EXISTING FLAKE.** Fails on a missing `role="radio"`
under the full parallel suite, passes 6/6 standalone; observed twice in eleven full runs this session.
Documented in **seven** handoff files, long before W0. ⛔ **Do not chase it as your regression.**

**7.6 A migration already stamped in `schema_migrations` will not re-run.** `0091`'s three added
`COMMENT ON COLUMN` statements reached the dev DB only via a manual `psql` replay; a fresh database gets
them from the file. `go run ./cmd/cored migrate` would not have applied them.

**7.7 ⛔ A FILE THAT RESTATES `seed_company_auth_defaults` MUST TAKE A NUMBER AT THE END.**
`migrations.go:71` applies in `sort.Strings` order and `CREATE OR REPLACE` is last-writer-wins, so a
seeder body at 0081 is overwritten by 0090's and its rules never exist for any new company — the 0071
defect, invisible on any incrementally-migrated machine. **W0 wrote no ACL migration at all** and
recorded why in `0091`'s header; the PERFORM count stays 17 and that number is the assertion.

**7.8 A new tenant table needs a `buildFixture` row** in `internal/platform/testdb/testdb_test.go`, or
`seedCompany` Fatals **by name** in a package nobody editing your feature would open.

**7.9 semgrep will fail the build on two shapes the agent plane invites:**
`money-must-not-be-float` is an ERROR on `(amount|debit|credit|taxable|cgst|sgst|igst|total)\s+float(32|64)`
anywhere under `backend/**` — a token-cost field named `total_cost float64` trips it. And
`no-bypassrls-role-in-app` is an ERROR on `(?i)(bypassrls|user=postgres|tally_admin)` under
`backend/internal/**` — a sample DSN **in a comment** trips it.

**7.10 `gofmt -l` flags `internal/server/handler_print_bankrecon_test.go`.** Pre-existing, committed,
untouched by W0. Not yours.

**7.11 ⚠️ `nlq.DefaultBaseURL` is still Anthropic's host and `server.go` passes it unconditionally.**
Once the composition root selects a provider it must pass `""` and let the chosen adapter supply its own
default host — otherwise **selecting Fireworks sends a Fireworks request to `api.anthropic.com`** and
the failure reads as a 404 on a model id. `translate.go:80-86` carries the note. **W1b will hit this.**

---

## 8. Migration numbers

`PLAN-TALLY-PARITY.md` §2.5 is the only authority. **0091 is CONSUMED** (`0091_llm_token_budget.sql`,
table only, no seeder restatement). **0092 and 0093 are free.** Suggested reservations, to be recorded
in §2.5 *before* a parallel lane takes them: **0092** the W2 intent log (INSERT-only, `prev_hash`,
nightly anchor), **0093** the W2 autonomy table `(tenant, user, action, amount_band) → L0..L3`.

For the intent log, both halves are required, not one: a `BEFORE UPDATE OR DELETE` trigger that raises
(copy `voucher_version_no_mutate`, `0002_core_schema.sql:123`) **and** `GRANT SELECT, INSERT` only.
Chain columns follow `voucher_version` (`0002:95-112`, `prev_hash` at `:105`); the hash is computed in Go with BLAKE3, never in
SQL. ⚠️ **A tenant-wide chain serialises every agent write behind one row** — chain per run or per
voucher unless you mean to.

---

## 9. The workflow recipe that worked, for the next session

W0 was built with three `Workflow` runs and it is worth repeating:

1. **Recon (6 parallel readers, no writes).** One lane per subsystem, each returning a structured report
   plus *pinned contracts* and *risks*. ⛔ **This is what made the build accurate** — it caught that
   §1.1 forbids new dependencies (which killed the OTel SDK plan before a line was written) and that 108
   of 199 keymap bindings do not exist as source text.
2. **Build (4 disjoint lanes → nlq re-point chained behind `llm` → integrate → 4 adversarial reviewers →
   fix).** Disjoint **file lists** in the brief, integration edits reserved to the integrator.
3. **Verify-then-fix.** Every finding re-checked against the code first: of 24 findings, several were
   already fixed and a few were not defects. ⛔ **A wrong finding "fixed" is a regression.**

⚠️ **The fix pass died mid-run on a session limit after 145 tool calls, having already applied most of
the major tier.** That is why step 3 leads with "determine the current state". Assume any interrupted
pass did *some* of its work.

⛔ **Give every lane the house style explicitly** — the why-comment register (`nlq/translate.go:1-31`,
`authz/vocabulary.go:24-57`, `insightd_proxy.go:67-75`) and the non-vacuous-test rule with a demanded
mutation proof. The lanes that were told to mutate-and-observe found real holes; the reviewers found the
rest.
