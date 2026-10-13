# D126 — W6, the MCP server half: an outside client in the agent's seat

**Written:** 2026-09-28, on `agentic/w6-mcp` (from `4e7cfff`). **Every figure below was printed by a
command run in this session**, on the private database `tally_w2_w6mcp`; the command is named beside it.
Builds on D121 (W2's agent plane), whose dispatcher, gate, approval inbox and commit this lane reuses
without a second copy. Migrations consumed: **0105** (0106 unused, kept for W6's WhatsApp/voice half).

⛔ **SCOPE, STATED FIRST.** PLAN-AGENTIC W6 is WhatsApp + voice + the MCP server. **WhatsApp and voice are
not built** — they wait on the user's BSP-vs-Cloud-API decision (HANDOFF §7, 6.5) — and are the first
two items of §9. This lane built the MCP server's load-bearing spine completely, with gates: the
protocol, the transport, grants, sessions, the pinned manifest, and the path from `tools/call` to a
committed voucher, which is the agent's own path. Full OAuth 2.1 (an authorization server, Protected
Resource Metadata, Client ID Metadata Documents) is **not** built; §4 says what stands in for it.

---

## 1. The shape in one screen

```
 MCP client ──POST /v1/companies/{c}/agent/mcp/{grant}──► companyAuthMiddleware (bearer = the grant's transport token, 0106;
   (Claude Desktop, ChatGPT, an IDE)                       membership; `display agent-mcp`)
                                                                   │
                                                   internal/mcp.Server.Serve
             Origin check · grant (RLS, same user, unrevoked, unexpired) · session · protocol header
                                                                   │
     initialize ─► agent.Engine.StartExternal ─► agent_run (agent_id "mcp:<grant>", channel mcp,
                   + agent_mcp_session (0105)       born with input provenance mcp_client)
                   manifest = BuildManifest(actions.Specs, grant principal) ─► SHA-256 pinned
     tools/list ─► recompute manifest; hash ≠ pinned ⇒ -32001
     tools/call ─► recompute + compare ─► binding → agent.ModelDecision ─► agent.Engine.Submit
                                                                   │
                  the SAME loop (D121 §5): decide:k ← the client's call (recorded, never a model call)
                    tool:k   actions.read / actions.preview → agentDispatcher → the real router, as the human
                    gate:k   policy.Decide: taint(mcp_client) ⇒ approval for money/master/statutory, any level
                    await:k  parks on agent_approval → the EXISTING inbox (GET/POST /agent/approvals…)
                    exec:k   the idempotent commit, agent_commit in the voucher's transaction
     run_status  ─► agent.Engine.StatusExternal (reads the run's own record; no handler, no decision)
     DELETE      ─► close the session; a run waiting for its client finishes
```

| package / file | role | allowed imports (archtest) |
|---|---|---|
| `internal/mcp` (NEW) | JSON-RPC 2.0, Streamable HTTP, manifest, grants, sessions | `{agent, actions, policy, platform/db}` |
| `internal/agent/external.go` (NEW) | `StartExternal`, `Submit`, `StatusExternal`: the external-planner entry point | unchanged set |
| `internal/server/mcp_http.go` (NEW) | 5 routes, test seam `ConfigureMCP` | + `internal/mcp` |
| `migrations/0105_…` (NEW) | `agent_mcp_grant`, `agent_mcp_session` | — |

Size (`wc -l`): internal/mcp 1,691 lines (310 of them its test); external.go 407; mcp_http.go 153;
server-side MCP tests 1,127 (harness 204, conformance 308, gates 615); 0105 160. `git diff 4e7cfff -- backend/go.mod backend/go.sum
frontend/package.json frontend/pnpm-lock.yaml insightd/requirements.txt | wc -l` = **0**.

---

## 2. Decision 1 — the MCP client takes the model's seat, and nothing else changes

An in-app run is `decide:k (model) → tool:k | gate:k → await:k → exec:k`. A `tools/call` names one tool
and its arguments, which is exactly a `ModelDecision`. So the MCP server does not implement preview,
commit or approval at all: it turns each call into a decision and hands it to `agent.Engine.Submit`,
which records it as `decide:k` and lets `loop.next` derive every later step from the record, as it does
for the model. A commit therefore replays the recorded preview (or the approver's edit), never anything
the client sends with the commit; `commit_preview`'s arguments are passed **verbatim** to the gate's
strict decode, so `{"preview":"tool:1","approved":true}` is refused by the agent's own rule.

**What the MCP package cannot do, by construction.** It imports no feature package, no `internal/server`,
no `internal/llm`/`durable`/`intentlog` (archtest). It holds no `agent.Dispatcher`, builds no
`agent.Request`/`agent.Caller`, makes no in-process HTTP request: `TestMCPReachesNoHandlerExceptThroughTheAgent`
parses the package's source and fails on any of those selectors (and on an `httptest` import), and
requires at least one `Submit` call so the scan cannot pass vacuously.

**The narrow entry point added to `internal/agent` — exactly this, for the integrator (the W2-breadth lane
touches the same files):**

- `engine.go`: `Start` now calls a private `start(ctx, req, agentID, expiresAt)`; `Start` passes
  `policy.AgentID` and a zero expiry, which `start` turns into the old default (window + 24 h) — **Start's
  behaviour is unchanged** (one new refusal, "delegation already expired", is unreachable from `Start`).
  `Resume` is now `resume(…, supplied=nil)` → `advance(…)`, the old body verbatim plus `lp.locked = true` on
  `ErrLocked`. New field `Engine.ExternalMaxDecisions` (0 ⇒ 200).
- `loop.go`: `loop` gains `supplied *ModelDecision`, `consumedK`, `locked`; `decideOrStop` reads
  `l.maxDecisions()` (the engine's bound, or the external bound for an external run); `decideStep`'s body
  starts with `if l.run.External() { return l.externalDecision(…) }`. **Nothing else in the loop changed**:
  tool, gate, await, exec and finish are the same functions for both planners.
- `external.go` (new): `ExternalAgentPrefix = "mcp:"`, `Run.External()`, `ExternalStartRequest`,
  `StartExternal`, `Submit`, `StatusExternal`, `ErrNotAwaitingPlanner`, the `External*` view types.

**"External" is decided by the agent id, never the channel.** `POST /agent/runs` takes a caller-chosen
channel label (including `"mcp"`) but never an agent id, which `Start` always sets to `khata-agent`. So no
in-app run can be made to park for a planner that does not exist. `TestMCPIdleRunNeverAsksAModel`
proves both halves: an idle MCP run polled 5 × 20 times across 10 minutes of engine clock made **0** model
calls and recorded **0** decisions; an in-app run started with `"channel":"mcp"` made exactly **1** model
call and completed.

**One consequence, kept identical on purpose.** While a run is parked on an approval (`await:k`), it takes
no decision — the in-app loop behaves the same. The client is told so (`status: not_accepting`, isError,
nothing recorded) and `run_status` still answers, because it reads the record rather than submitting a
decision. A client that wants to keep working opens a second session (a second run, tainted the same way).

---

## 3. Decision 2 — every MCP run is born tainted

The in-app agent's CaMeL gate (D121 §3) trusts the planner's context when it has seen only the user and
the books. An MCP client's model has seen whatever its user pasted and its other tools fetched — web
pages, mail, PDFs — none of which this server saw or can label. So `StartExternal` gives every MCP run
one input with the new provenance **`policy.SourceMCPClient` (`"mcp_client"`)** and empty text. The gate
then treats the run as tainted from its first step: **a money, master or statutory commit through MCP
waits for a human decision at every autonomy level, L3 included.** L2/L3 therefore never make an MCP
write autonomous today; the only class that could commit autonomously is `operational`, and none of it
is previewable yet (D121 §9 item 2).

Measured (`go test ./internal/server -run TestMCPPreviewThenCommitWaitsForTheInbox -v`, autonomy L3):
preview Dr/Cr **500000 / 500000 paise**, 0 vouchers after it; `commit_preview` → `pending_approval` with
reason *"untrusted input reached this run (mcp_client): a money action built from it can never be
authorised by it — a human must decide"*; the approval is in the ordinary inbox with the instruction
*"Acting through the MCP client …"*; after `approve` + one poll: **exactly 1 voucher, 1 agent_commit**;
`run_status` shows the decision and the voucher id; a second commit of the same preview is refused
("already submitted"); the intent log attributes **4** `decision_step` events to `agent:mcp:<grant>`.

Additions to `internal/policy` (vocabulary only — no rule's meaning changed): `SourceMCPClient`, accepted
by `ValidSource`, not `Trusted()`; `AgentRules` gains `disallow * agent-mcp` (a token that did not exist
before, so it narrows nothing). The frontend's `AgentProvenanceSource` union gains `"mcp_client"`.

---

## 4. Decision 3 — identity: the human's own bearer token, plus a grant; no second identity system

> ⛔ **AMENDED BY THE WAVE-2 REVIEW (migration 0106).** The human's own JWT was not a boundary: the MCP
> client held the very token that decides approvals (`POST /agent/approvals/{id}/decision`) and posts
> vouchers, so a prompt-injected client could approve its own `commit_preview`. A grant now carries a
> **transport token** (`mcpt_` + 32 random bytes, returned once by `POST /agent/mcp/grants`, stored as
> SHA-256 with the grant-maker's subject). `authenticate` accepts it **only** on
> `/v1/companies/{c}/agent/mcp/{grant}` for that grant, and **refuses a JWT there** (401); off the
> transport it is 401. It becomes the grant-maker's claims with `iat` = the grant's `created_at`, so
> membership, level, PDP and lock-revocation all still apply. Gate: `TestMCPTransportTokenCannotActAsThePerson`
> (the token cannot decide its own approval, post a voucher or make a grant; the owner's JWT cannot drive
> the transport; a forged token is refused; control: the human approves with their JWT, 1 voucher).
> A grant made before 0106 has no token and is unusable; make a new one. The text below is the original
> decision, kept for the record; where it says "the human's JWT" on the transport, read "the grant's
> transport token".

- **Authentication** is the existing `internal/authn` + `companyAuthMiddleware`, unmodified: the MCP
  client sends the human's IdP JWT. Membership, level, session revocation all apply before the MCP
  server runs; a non-member is refused by the router (`TestMCPCrossTenantRefusal`, "non-member").
- **Scope** is a **grant** (0105 `agent_mcp_grant`): a delegation the human makes through
  `POST /agent/mcp/grants` (`create agent-mcp`, a write — so a WebAuthn level steps up, recorded on the
  grant as `step_up` exactly as `POST /agent/runs` records it). It holds ACL keys (validated like a run's:
  a key no action evaluates to, or one only structurally-forbidden actions carry, is a 422), an optional
  paise ceiling, and an expiry (required, ≤ 90 days). The transport URL names the grant. A request is
  served only when the token's user **is** the grant's user, in the grant's company (RLS), and the grant
  is unrevoked and unexpired. Only revocation may change a grant row (trigger); widening its keys or
  ceiling after the fact is refused by the database (`TestMCPDelegationCeiling`).
- **Revocation** (`POST /agent/mcp/grants/{id}/revoke`) closes the grant's sessions and **cancels their
  runs**, so an approval a human grants after the revocation posts nothing (the commit hook's `FOR SHARE`
  status check). Measured: 0 vouchers, run `cancelled`, transport 403 "revoked"
  (`TestMCPRevokingAGrantStopsItsRuns`).
- **The transport route is `display agent-mcp`, not a write**, deliberately (auth_middleware.go's arm
  says why): it alters nothing itself, every `tools/call` is re-judged in process as the grant's user
  through the full middleware, and a write mapping would demand a WebAuthn assertion from a desktop
  program that cannot make one.
- `agent-mcp` is a **bare token** (the 0091 row's rule): Data Entry is refused all five routes with zero
  `acl_rule` rows (`TestDataEntryIsRefusedTheAgentPlane`, extended; Owner control 200). Governance-class
  in `risk.go` and in `governanceReads`, so an agent can never reach an MCP route in process.

⛔ **The OAuth gap, stated.** The MCP authorization spec (2025-06-18/2025-11-25) expects an OAuth 2.1
resource server: `WWW-Authenticate` with `resource_metadata`, a Protected Resource Metadata document
(RFC 9728), audience-bound tokens (RFC 8707), and — in 2025-11-25 — Client ID Metadata Documents. None is
built. What stands in: the IdP's JWT verified by `authn` (with `AUTH_AUDIENCE` in JWKS mode, which
`buildAuth` already requires), scoped by the grant. A client must be configured with a token by hand
today. §9 item 3.

---

## 5. Decision 4 — the tool manifest, generated and pinned

`tools/list` is `BuildManifest(actions.Specs, grant principal, now)`: every ActionSpec that
`policy.EffectiveRights` allows the grant (the same filter the in-app `actions.search` uses) —

- each GET **read** spec → one tool (`get_trial-balance`, …), bound to `actions.read`;
- each **delegated and previewable** write → one *preview* tool (`post_vouchers_payment`), bound to
  `actions.preview`;
- `commit_preview` (only if some preview exists), `search_actions`, `query_books`, `get_draft`,
  `run_status`.

Names are the spec id with `.` → `_`; `TestToolNamesAreUniqueAndValid` proves the mapping injective over
all 243 specs and every name `^[A-Za-z0-9_-]{1,64}$`. Every tool carries `_meta` `khata/risk`,
`khata/acl_key`, `khata/action_id`, `khata/mode`, and annotations (hints only; nothing reads them back).
Counts (`go test ./internal/mcp -run TestManifestIsTheRegistryFilteredByPolicy -v`): *"registry 243 specs;
payment+master grant: 86 tools (80 reads, 1 preview, 5 meta); read-only grant: 84 tools; all-voucher
grant: 93 tools (8 previews)"*. Structural specs (deletes, security, company, backup, the agent plane,
the MCP routes) never appear.

⚠️ **The body schema is a hint, not the validator.** Preview tools describe the voucher body
(`voucher_date`, `narration`, `postings[{ledger_id, dr_cr, amount}]`, `additionalProperties` open); the
handler's decoder and validators accept or refuse it, in preview, where a refusal costs nothing.
Generating per-handler schemas is owed (§9).

**The pin.** `initialize` stores SHA-256 of the canonical manifest on the session (and returns it in
`_meta.khata/manifest_hash`). Every `tools/list` and `tools/call` recomputes it from the registry and grant
**as they are now**; a mismatch is JSON-RPC error **-32001** with `data {pinned, current}`, and nothing is
recorded. `listChanged` is declared `false`: the server refuses rather than notifies. The hash is a
function of content (reordering the registry, or removing a spec the grant could never see, leaves it
unchanged; a wider delegation, an expired delegation or a changed registry change it —
`TestManifestHashPinsContent`). `TestMCPManifestChangeIsRefused` swaps the registry under a live session
(one report removed): tools/list, a read and a commit are all -32001, 0 decisions recorded; a new session
pins the new hash and no longer lists the tool; restoring the registry restores the old session.

---

## 6. Decision 5 — protocol scope

**Versions negotiated: 2025-11-25 and 2025-06-18** (newest first; an unsupported request is answered with
2025-11-25). PLAN-AGENTIC W6 names "spec 2026-07-28 … MRTR `input_required`"; this session could not read
that revision (no network), so it is **not claimed**. Confirmations are expressed in the 2025-06-18/11-25
shape: a successful tool result with `structuredContent.status = "pending_approval"` and an
`input_required {kind: "human_approval", approval_id, deadline_at, resolve_via, poll_with: run_status}`
object. Aligning that with MRTR's wire format is §9 item 4. **Elicitation is deliberately not used** for
approvals: a "yes" typed into the client would be the client's word, not an attributable decision by a
member with the authority and the step-up their level demands.

Offered: `initialize`, `notifications/initialized`, `ping`, `tools/list` (one page; any cursor is
-32602), `tools/call`. Not offered (-32601): resources, prompts, sampling, logging, completion, tasks.
Transport (Streamable HTTP): POST one message → `application/json` response (never SSE); notifications
and responses → **202**, empty body; `Mcp-Session-Id` issued on initialize (random v4 UUID) and required
after (missing → 400, unknown/closed → 404, i.e. "re-initialize"); `MCP-Protocol-Version` if present must
equal the negotiated version (else 400); GET → **405** (no server stream is offered, as the transport
allows); DELETE ends the session (204); a foreign `Origin` → **403** (`MCP_ALLOWED_ORIGINS` allowlist,
empty by default); Content-Type other than JSON → 415; an Accept that excludes JSON → 406; bodies ≤ 1 MiB.

**Batching is refused**, because both versions removed it: an array body is one `-32600` with id null
and **nothing in it runs** (0 decisions recorded — measured). Ids must be a string or an integer (null, a
fraction, an object → -32600). Unknown tool → **-32602** (the spec's own example); invalid arguments → a
tool result with `isError: true` (2025-11-25's rule), recording nothing.

`TestMCPProtocolConformance` (`go test ./internal/server -run TestMCPProtocolConformance -v`): **23
subtests, all PASS**, over the real router with the owner's real token.

---

## 7. The gates, and what each printed

| gate | command | printed |
|---|---|---|
| protocol conformance | `go test ./internal/server -run TestMCPProtocolConformance -v` | 23/23 subtests PASS (lifecycle, versions, transport codes, batch refusal with 0 decisions, id rules, errors, tools/list schemas + risk, invalid-args-as-isError, read content, DELETE) |
| pure protocol/manifest | `go test ./internal/mcp -v` | 6 tests PASS; manifest census as in §5 |
| preview → approval → commit | `… -run TestMCPPreviewThenCommitWaitsForTheInbox -v` | §3's figures; exactly 1 voucher only after the human approves |
| **injection suite over MCP** | `… -run TestMCPInjectionSuiteCommitsNothing -v` | *"32 injection cases over MCP at L3; 20 money-moving previews parked on the mcp_client taint; 13 calls refused as tools the session was never shown; 0 vouchers, 0 commits"* |
| cross-tenant | `… -run TestMCPCrossTenantRefusal -v` | 6 subtests PASS: A's grant under B's URL 404; A's session on B's grant 404; on A's other grant 404; a second Owner of A on the first Owner's grant 403; a non-member 403; A's session cannot read B's voucher (control: B's own session can) nor preview a payment between B's ledgers; vouchers A 0, B 1 (B's own) |
| manifest pin | `… -run TestMCPManifestChangeIsRefused -v` | -32001 on list, read and commit with both hashes; 0 decisions; new session re-pins; restore works |
| delegation ceiling | `… -run TestMCPDelegationCeiling -v` | structural keys refused at the grant (422 ×4); grant widening refused by trigger; undelegated receipt not listed and -32602; Rs 50,000 over a Rs 30,000 ceiling parks with the ceiling reason; an approver's Rs 45,000 edit is refused **403 by the ceiling** with 0 vouchers; a Rs 20,000 payment parks (taint only) and commits 1 voucher on approval |
| revocation | `… -run TestMCPRevokingAGrantStopsItsRuns -v` | approval after revoke → 0 vouchers; run cancelled; transport 403 |
| idle run | `… -run TestMCPIdleRunNeverAsksAModel -v` | 0 model calls, 0 decisions, parked; in-app `channel:"mcp"` run: 1 model call |
| Data Entry | `… -run TestDataEntryIsRefusedTheAgentPlane -v` | 5 MCP routes 403 for seeded Data Entry; Owner 200 |

The injection suite reuses wave 1's `injectionCases` and `attackScript` unchanged: the compromised planner
is now an MCP client whose decisions are translated to the `tools/call` a client would make. The injected
text lives in the client's context, where an MCP client meets it; what reaches the server is the attack.
Its control is the preview→approval test above: the same path does commit, once a human approves.

### Mutation proofs run in this session (each restored; the tree was clean after)

Driven by a scratch script that edits one anchor, runs the named tests, and restores the file.

1. **Born taint removed** (`StartExternal` passes no input) → `TestMCPInjectionSuiteCommitsNothing` fails
   from the first case: *"INJECTION SUCCEEDED OVER MCP: 1 voucher(s) after "invoice-line-pay""* (then 2, 3
   … as cases accumulate); `TestMCPPreviewThenCommitWaitsForTheInbox` fails with the commit posted
   autonomously (`"status":201,"ok":true`).
2. **Manifest pin off** (`if false && man.Hash != …`) → `TestMCPManifestChangeIsRefused`: *"after the
   change: 200 … tools"*.
3. **Grant-user check off** → cross-tenant *"second owner on the first owner's grant: 200"*.
4. **Session-grant binding off** → *"A's session on A's other grant: 200"*.
5. **Batch accepted** (first element executed) → conformance *"a batch is one Invalid Request …"*: 200.
6. **The ceiling sum reverted to `sum(amount)`** → `TestMCPDelegationCeiling`: *"the over-ceiling edit was
   refused, but not by the ceiling: … function sum(text) does not exist"* (see §8).
7. **External decide no longer parks when idle** → `TestMCPIdleRunNeverAsksAModel`: *"an idle MCP run
   asked the model 1 time(s)"*.
8. **A side door** (`s.Engine.Dispatch.Dispatch(…)` added to the MCP server) →
   `TestMCPReachesNoHandlerExceptThroughTheAgent` names 5 selectors (`.Dispatch`, `agent.Caller`,
   `agent.Request`).
9. **Revocation cancels no run** → *"an approval decided after revocation posted 1 voucher(s)"*.
10. **Origin check off** → conformance *"a foreign Origin is 403": 200*.

---

## 8. A wave-1 defect this lane found and fixed (for the integrator)

**`agent_dispatch.go`'s delegated-ceiling check could never pass.** It ran
`SELECT COALESCE(sum(amount),0)::text FROM (postingsWrittenSQL) p …`, and `postingsWrittenSQL` projects
`p.amount::text`. So **every commit of a run with `max_amount_paise` set** failed inside the voucher's
transaction with `function sum(text) does not exist (SQLSTATE 42883)` → 422, rolled back. It was
fail-closed (no over-ceiling edit ever posted, which is why D121 #2's mutation test stayed green), but no
*within*-ceiling commit could post either. Fixed with `sum(amount::numeric)`; the ceiling test now asserts
the refusal is **403 naming the ceiling** and that a within-ceiling commit posts (mutation 6).
`TestAgentApproverIsJudgedOnTheCommittedBody` still passes after the fix.

---

## 9. ⛔ What is OWED — stated, not buried

1. **WhatsApp** (own WABA / BSP, webhook → media → `ActionRequest`, templates, Flows, linked-number auth,
   step-up above L1) — blocked on HANDOFF §7 6.5. Migration 0106 is reserved for it.
2. **Voice** (Saaras STT, Bulbul TTS, amount readback, the Hinglish intent grammar and its eval) — needs a
   provider key (6.1) as well as 6.5.
3. **OAuth 2.1 for MCP**: Protected Resource Metadata (RFC 9728) + `WWW-Authenticate resource_metadata`,
   audience-bound tokens (RFC 8707), an authorization server or IdP integration for the client's flow,
   Client ID Metadata Documents (2025-11-25), dynamic registration. Today a client is configured with a
   token and a grant URL by hand.
4. **The "2026-07-28" revision and MRTR `input_required`** named by the plan: not read in this session;
   the pending-approval result is the 2025-06-18/11-25 shape (§6). Align once the text is in hand.
5. **The grant screen** (make / list / revoke, showing each grant's URL): the three grant routes are in
   `actionsWithNoHumanPathYet` (cap **27 → 32**; the two transport rows are permanent — no person speaks
   JSON-RPC).
6. **Per-handler input schemas** for preview tools, generated from the request types (today one voucher
   hint schema).
7. **Idle and closed sessions**: a run whose client vanished stays parked (polled once a minute,
   recording nothing). An idle-session reaper, and what happens to a closed session's run after its
   approval commits, are owed.
8. **A session parked on an approval takes no other call** (identical to the in-app loop); a W2 decision
   to let a run keep reading while an approval is pending would lift it for both planners.
9. **Breadth follows W2's**: only the eight accounting voucher creates are previewable, so they are the
   only MCP writes. Every handler W2 adds to `previewable` appears in the manifest with no change here.
10. **Server-initiated notifications / SSE** (e.g. an approval-decided notification instead of polling
    `run_status`), resources (reports as resources), and the tasks utility.
11. **An MCP client conformance run against a real client** (Claude Desktop / Inspector): impossible
    offline; the conformance test is this server's reading of the spec.
12. **A live-model trajectory through MCP**: none exists (no keys); the injection suite's client is the
    wave-1 scripted attacker.

---

## 10. Gates at the lane's HEAD (private database `tally_w2_w6mcp`, all four DSN envs set)

- `cd backend && go build ./... && go vet ./...`: clean. `GOFLAGS=-mod=readonly GOPROXY=off go build ./...`:
  clean. Dependency diff against `4e7cfff` (go.mod, go.sum, package.json, pnpm-lock.yaml,
  insightd/requirements.txt): **0 lines**.
- `go test ./... -count=1 -p 4 -timeout 60m`: **exit 0, 48 packages ok, 7 with no test files** (47 + the
  new `internal/mcp`). server 45.5 s, evals/tau 37.7 s.
- `make tier0`: ok; *"Tier 0: replayed 14 interactions across 4 cassettes in 2 directories, features
  [agent.decide extraction nlq.translate]"* — no cassette added (nothing here calls a model), pins unchanged.
- `cd frontend && pnpm exec tsc --noEmit`: exit 0. `pnpm exec vitest run`: **Test Files 126 passed (126),
  Tests 2019 passed (2019)** (only `AgentProvenanceSource` and the regenerated registry golden changed).
- insightd: not touched, not run.

## 11. Migrations and registry (PLAN-TALLY-PARITY §2.5 updated)

- **0105** `agent_mcp_grant`, `agent_mcp_session`: ENABLE + FORCE RLS, USING + WITH CHECK, company-scope
  UNIQUEs, composite FKs to the grant and `agent_run`, named CHECKs, a narrowing UPDATE/DELETE trigger,
  DELETE/TRUNCATE revoked from `tally_app`. Applied twice inside `BEGIN … ROLLBACK` on `tally_w2_w6mcp`
  (exit 0). testdb fixture rows added (`go test ./internal/platform/testdb` ok). No ACL rule; seeder census
  unchanged (`go test ./migrations` ok).
- **0106 unused.**
- Registry regenerated (`UPDATE_GOLDENS=1 go test ./internal/server -run TestActionRegistryGolden`):
  **243 rows** (+5 MCP routes). Risk census (`go test ./internal/actions -run TestEveryRowCarriesTheRisk
  -v`): destructive 6, export 46, governance **28**, master 29, money 25, operational 19, read 85,
  statutory 5.

## Wave-2 merge (integrator, `session/t32-and-close-7-3`)

What changed when W6 met W2 breadth and W3 on one branch (HANDOFF-AGENTIC-WAVE-2 §4):

- **The manifest follows W2 breadth's allowlist.** `agent.Previewable` grew from 8 to 33 specs, and every
  delegated one is now a preview tool. Only the eight accounting voucher routes (create and alter) carry
  `voucherBodySchema`. Every other previewable write (stock vouchers, masters, orders, bank ticks) gets an
  open object hint that the handler validates in preview. A DELETE (the bank un-tick) previews with `{}`
  when no body is sent. `legNo` is an ordinal pattern, not a uuid.
- **`reverse_voucher`** maps to `actions.reverse` (Go builds the reversal). It is listed when at least one
  accounting voucher create is delegated.
- **W3's five profile tools** are listed under the agent's own names and bound to them. A call reaches the
  same `ProfileReader` (per-fact ACL, stored_text label) through `Engine.Submit`.
- Counts printed by `TestManifestIsTheRegistryFilteredByPolicy` after the merge: registry 259 specs;
  payment+master grant 94 tools (80 reads, 3 previews, 6 meta, 5 profile); read-only grant 89; every
  previewable write delegated 124 tools (33 previews).
- **The injection suite runs the full 45-case list over MCP**, with the in-app suite's keys, L3 table,
  fixtures and books fingerprint: "45 injection cases over MCP at L3; 30 successful write previews (money,
  master, operational) parked on the mcp_client taint; 14 calls refused as tools the session was never
  shown; 0 vouchers, 0 commits". The in-app figure is the same 30.
- **The ceiling test now meets W2 breadth's decision-time re-preview first.** An over-ceiling edit is
  refused 422 EDIT_REFUSED at the decision. The commit hook is still proved by approving the unedited
  over-ceiling proposal: the mutation that disables the hook posts 1 voucher.
- **The ceiling query conflict.** W6's `sum(amount::numeric)` fix and W2 breadth's effect readback both
  replaced the broken `sum(text)`. The readback's `DebitPaise`, summed in Go, is kept.
