# D121 — W2 backend: the agent plane (durable runs, policy, intent log, the loop, the inbox API)

**Written:** 2026-09-28, after the code landed on `agentic/w2-backend`, **with every figure printed by a
command run in this session** (the command is named beside each figure). Builds on D119 (W0) and D120
(the durable-runs spike). Migrations consumed: **0092, 0093, 0094** (0095 unused).

⛔ **SCOPE, STATED FIRST.** PLAN-AGENTIC W2 is six engineer-weeks. This lane built the load-bearing
spine **completely, with gates** — durable runs, policy, intent log, the loop with its core tools, the
approval API, and the three test gates the brief named — and deliberately did **not** build breadth
(every voucher class, masters, orders, bank-rec, reminders, the parity trace suite, AgentDojo). §9 is the
honest list of what is owed.

---

## 1. What exists now — the shape in one screen

```
 POST /agent/runs ──► agent.Engine.Start ──► agent_run (0094) + run_started intent (0092)
                                                     │  poller (agent_runs_due, SECURITY DEFINER)
                                                     ▼
        durable.RunDynamic(runStore, run_id, loop.next)   ← per-run pg_try_advisory_lock (F3)
          start → decide:k ─(llm.Client, forced "decide" tool)─► tool:k | gate:k → await:k → exec:k → … → finish
                              │                                    │          │ ErrParked (F1)     │
                              ▼                                    ▼          ▼ reads agent_decision│
                    agentDispatcher (internal/server) ── in-process request through THE SAME router ─┘
                      read   : any RiskRead spec                      companyAuthMiddleware:
                      preview: TxHook{DryRun} — real handler,          ACL(user) via authz.Evaluate
                               postings read back, rolled back         + agentGate: route == spec,
                      commit : TxHook{BeforeCommit} — agent_commit       ACL(agent) ∩ delegation ∩ structural
                               inserted IN the voucher's transaction
```

| package | role | allowed imports (archtest) |
|---|---|---|
| `internal/durable` (NEW) | the step engine, lifted out of statutory: `Run`, `RunDynamic`, `StepStore{Load,Append,TryLock}` (ctx), `ErrParked`, `ErrLocked`, `AdvisoryLocker` | `{}` |
| `internal/statutory` | now **aliases** durable (`statutory.Run` IS `durable.Run`) | + `internal/durable` |
| `internal/intentlog` (NEW) | append / verify / anchor over a caller's `pgx.Tx`, BLAKE3 in Go | `{}` |
| `internal/policy` (NEW) | principal, effective rights, structural rules, autonomy, provenance gate | `{authz, actions}` |
| `internal/agent` (NEW) | the loop, tools, run store, approvals, poller | `{durable, llm, actions, policy, intentlog, platform/db}` |
| `internal/server` | dispatcher, router gate, HTTP API, wiring | + `{agent, policy, intentlog, durable}` — **still not `statutory`** |
| `internal/actions` | `Risk` on every `ActionSpec` (regenerated golden) | unchanged |
| `internal/platform/db` | `TxHook` on `RunInTx` (preview rollback, commit hook) | unchanged |

Size (`wc -l`): 4,596 lines of implementation across the 14 new non-test files; 3,455 lines of tests.
`git diff --stat c51c0a0` over the branch: **59 files, +9,435 / −300**. `git diff c51c0a0 -- backend/go.mod
backend/go.sum | wc -l` = **0** (§1.1 upheld). `GOFLAGS=-mod=readonly GOPROXY=off go build ./...` clean.

---

## 2. Decision 1 — durable runs: D120's five items, and one decision D120 did not foresee

**The engine moved; the workflow stayed.** D120 said "build on `statutory.Run` + `PGStore`". But
`internal/server`'s allowed set says in terms that `internal/statutory` is **not** importable from cored
(it wraps an exactly-once government call), and the agent runs inside cored. So `Run`/`StepStore`/`MemStore`
moved to a new leaf `internal/durable` (allowed set `{}`), and `internal/statutory/runner.go` is now type
aliases and a one-line `Run` — one implementation, two importers, the import graph unchanged in meaning.
`internal/statutory`'s set gained exactly `internal/durable`.

| D120 item | where it landed | gate |
|---|---|---|
| **F1** parked ≠ failed | `durable.ErrParked` + `Park()`/`*ParkedError` (`errors.Is`), re-exported in `statutory/runner.go` | `TestParkedIsDistinguishableFromFailed` |
| **F2** expiry terminal | the **agent** writes an `'expired'` `agent_decision` row (await step, and the decision API if a late approve arrives first); 0094's `agent_decision_one_per_approval` UNIQUE refuses any later decision | `TestAgentExpiredApprovalIsTerminal`, `TestAgentLateApprovalIsRecordedAsExpired` |
| **F3** lock mandatory | `StepStore.TryLock` is part of the interface; **`Run` and `RunDynamic` take it themselves** before the first `Load`; `ErrLocked` to the loser | `TestConcurrentPollersExecuteTheMoneyStepExactlyOnce`, `TestRunRefusesWhileLocked`, `TestMemStoreConcurrentRunsExecuteOnce`, `TestAdvisoryLockerExcludesAcrossSessions` |
| **F4** ctx on StepStore | `Load/Append/TryLock(ctx, …)`; `PGStore` no longer uses `context.Background()` | compile + statutory suite |
| **F5** the poller | `agent.Poller` (`RunOnce`, `Run` with a kick channel, `AnchorOnce`), started by `cored serve` every 2 s | `TestAgentThreeDayApprovalSurvivesRestarts` |

⛔ **The spike's defect test was deleted in the change that turned it red**, as its own header required:
`TestConcurrentPollersDoubleExecuteTheMoneyStep` asserted the double execution; with the lock inside `Run`
the second poller answers `LOCKED`. Its replacement, measured (`go test ./internal/statutory/ -run
TestConcurrentPollers -v`): *"F5 CLOSED: under contention ([COMPLETED LOCKED]), with no caller-side lock,
the money step executed exactly once"*.

**`RunDynamic` is new** (an agent loop's shape is only known as it unfolds). Its contract: `next` is a
**pure** function of the recorded steps, so a resumed run derives the same next step. It refuses a `next`
that proposes an already-recorded name (that would loop forever on `Append`'s no-op).

`AdvisoryLocker`: `pg_try_advisory_lock(hashtextextended(namespace:id, 0))` — 64-bit, not the spike's
32-bit `hashtext` — on one connection held out of the pool for the run; if the unlock does not
demonstrably succeed the session is **hijacked and closed** rather than returned holding the lock.

**Agent steps are tenant data**, so they are not in `statutory_step` (no RLS by design); they live in
0094's `agent_run_step` under FORCE RLS, reached by `agent.runStore`.

---

## 3. Decision 2 — policy: effective rights on the existing PDP, and the rules no table can relax

`EffectiveRights = ACL(user) ∩ ACL(agent) ∩ delegation`:

- **ACL(user)** is judged by the router's own `companyAuthMiddleware` on the real in-process request —
  membership, level, rules, back-dated cutoff, year-end lock, WebAuthn, session revocation — unmodified.
- **ACL(agent)** is `policy.AgentRules` evaluated by **`authz.Evaluate`** over a synthetic
  `default_access: none` level (not a second evaluator).
- **delegation** is the list of ACL keys the human granted this run (no wildcards), with an expiry and an
  optional paise ceiling, snapshotted on `agent_run.delegation`. Reads are not delegated one by one.

Both the dispatcher (pre-check) **and the router** (`agentGate` inside `companyAuthMiddleware`, after the
PDP allowed) apply it; the router also requires the reached route's `(action, resource)` to equal the
declared `ActionSpec`. Proof that the router half stands alone: `TestAgentGateInTheRouterRefusesWithoutTheDispatcher`.

**Risk classes on every ActionSpec** (`actions.ClassifyRisk(action, resource)`, one rule, generator
writes it, `registry_test` re-derives it). Census (`go test ./internal/actions -run
TestEveryRowCarriesTheRisk -v`): **238 rows** — read 85, export 46, master 29, money 25, governance 23,
operational 19, destructive 6, statutory 5, unclassified 0. Registry: 238 rows / 173 distinct ACL keys
(was 230/168; the +8 are the agent routes).

**Structural rules** (Go, `policy.Structural`; no autonomy row, rule or delegation relaxes them):
destructive (every delete) and governance (ACL/security, `company` incl. the year-end lock, backup,
import, sync, and the agent's own `agent-run`/`agent-approval`) are refused; **a write with no human path
(`ChannelNone`) is refused** — which is D121's answer to HANDOFF §3.3's four budget routes: **deny**, until
a screen puts an operator in front of them. Census (`TestStructuralRulesHoldOverTheWholeRegistry`):
destructive 6, governance 23, no-human-path 14 refused with every key delegated.

**"Agent-created parties born blocked"** is realised as *an agent-created master can only be born through
a human decision* (master creates always gate to approval, any level). There is no `ledger.blocked` column;
see §9. **"Agent never the checker"**: `policy.CanDecide` in the decision handler, plus the governance class
on `agent-approval` in the router.

**Autonomy** (0093): `(company, user|NULL, acl_key, amount_max_paise|NULL) → L0..L3`, **no row = L1**.
Most-specific user row wins, then the narrowest band containing the amount; an amount Go could not compute
matches only the any-amount band (`TestLookupLevelPrecedence`, 8 lookups).

**The provenance (CaMeL) rule — decided in its strongest simple form.** A run whose model context has seen
**any** untrusted material (an input labelled document/bank/whatsapp/tally_xml/email, or a `get_draft`
result) may not commit a money-moving action without a human — at any autonomy level. Per-field matching
is computed (`Provenance.Fields`) **for the approver's diff only**, never to decide: once untrusted text is
in the planner's context every value it emits is potentially attacker-chosen. ⚠️ **Wider than §2.4 on
purpose:** master and statutory writes are gated on a clean context too, because the commonest SME fraud
is an *altered bank account* on a party, not a payment in the same step.

**Step-up.** A read, a preview and an *autonomous* commit carry the delegating human's step-up
(`Delegation.StepUp`, recorded from the start request); a commit behind an approval carries the
**approver's** step-up (`agent_decision.decided_webauthn`), never the starter's.

---

## 4. Decision 3 — the intent log (0092)

`agent_intent_log`: one row per event, **append-only by trigger AND by grant** (`SELECT, INSERT` only;
`UPDATE/DELETE/TRUNCATE` revoked), FORCE RLS. `hash = BLAKE3(len-prefixed(domain, run_id, seq, kind,
actor_kind, actor_id, on_behalf_of, action_id, acl_key, created_at µs, canonical(payload), prev_hash))`
computed **in Go**; chained **per run** (a tenant-wide chain would serialise every agent write behind one
row). Canonical payload = decode-with-UseNumber → re-marshal (JSONB reorders keys); **fractional JSON
numbers are refused** because JSONB re-renders them and the chain would then report tampering that never
happened. `agent_intent_anchor` commits to every run head (as of `now − AnchorLag`, 5 min) chained to the
previous anchor, so a **truncated tail** — invisible to a per-run chain — is detected
(`TestAnchorCatchesATruncatedTail`). Payloads carry ids, verdicts, paise and **hashes** of text, never
prompt/completion text (`TestAgentInboxVerbs/the_side_panel's_event_stream` asserts the instruction does
not reach the event feed).

---

## 5. Decision 4 — the loop, the tools, preview and commit

**Loop** (`agent/loop.go`): `start → decide:k → {tool:k | gate:k → [await:k] → exec:k} → … → finish`,
≤ 8 decisions. Every decision is a forced `decide` tool call through `internal/llm` (`DecodeInto`,
`ToolChoiceNamed`), decoded strictly; unknown tools are refused in Go. Static prompt is byte-stable
(`TestStaticPrefixIsStable`); untrusted inputs are fenced and cannot close their own fence
(`TestUntrustedInputIsFencedAndCannotCloseItsFence`).

**Tools:** `actions.search` (registry, filtered by effective rights), **`actions.read`** (any `RiskRead`
GET spec — added beyond the brief's list because the model had no other way to learn a ledger id),
`actions.preview`, `actions.commit`, `actions.status`, `query_books` (POST /query), `get_draft`
(document-sourced ⇒ taints), `list_pending`, `finish`.

**Preview = the real handler, rolled back.** `platform/db.TxHook{DryRun}` makes every `RunInTx` under the
request's context roll back instead of commit, after `BeforeCommit` reads the postings the transaction
wrote (`xmin = txid_current_if_assigned()`). The dispatcher asserts `committed == 0` afterwards. Measured
(`TestAgentPreviewLeavesNoTrace`): Dr 118000 / Cr 118000 paise over 2 postings, 0 vouchers left, and the
next real voucher took the **same voucher number** the preview showed (the counter rolled back — no gap in
an invoice series). Allowlisted to the 8 accounting voucher creates whose handler is one hooked
transaction (`agent.previewable`).

**Commit = replay of an approved preview, never regenerated.** `actions.commit` takes exactly
`{"preview":"tool:N"}` (strict decode refuses `approved:true`, inline bodies, anything else); the body
committed is that preview's recorded body or the approver's edit; one commit per preview. The exec step
re-reads the decision from the database before committing. **Idempotency:** key
`agent:<run>:exec:<k>`; the dispatcher answers a known key from `agent_commit` without running the
handler, and inserts the `agent_commit` row **inside the voucher's own transaction** so a raced duplicate
rolls its voucher back (`TestAgentCommitIsIdempotentByKey`: sequential repeat → replay; 8 concurrent
dispatches of one key → 1 voucher).

**Verifier = the existing validators**, fed back as the structured tool result; **one bounded retry** per
action (the second refusal is terminal), counted from recorded steps so a restart cannot reset it — the
sales cassette exercises it (first preview 422 "double-entry violation: debit 11800.0000 != credit
10000.0000", corrected preview 201).

In-process requests start from a **fresh context** (only the caller's cancellation carries over), so no
value from the poller's context reaches the router as if the middleware had set it.

---

## 6. The gates, and what each printed

| gate | command | printed |
|---|---|---|
| Payment + Sales end to end, cassette replay | `go test ./internal/server -run TestAgentCassette -v` | both PASS; parked with 0 vouchers → approve → exactly 1 voucher; 5 and 6 model calls; every recorded interaction replayed |
| Tier 0 | `make tier0` / `go test ./internal/trajectory -run TestTier0 -v` | *"replayed 14 interactions across 4 cassettes in 2 directories, features [agent.decide extraction nlq.translate]"* (pins raised 2→4, 3→14) |
| Injection suite | `go test ./internal/server -run TestAgentInjection -v` | *"32 injection cases (map[bank:7 document:8 email:5 tally_xml:6 whatsapp:6]); 20 money-moving previews parked by the provenance gate at L3; 0 vouchers, 0 commits"* |
| its control | `TestAgentCleanL3CommitsAutonomously` | clean context at L3 → exactly 1 voucher, 1 agent_commit, double commit refused |
| 72-hour approval across restarts | `go test ./internal/server -run TestAgentThreeDay -v` | *"a 72h approval survived 4 distinct cored processes [13763 13764 13765 13766]; the payment voucher was committed exactly once"*; epoch 2 (+71 h) made **0** model calls |
| expiry terminal (F2) | `TestAgentExpiredApprovalIsTerminal` | expired with 0 calls; late approve → 409 `ALREADY_DECIDED`; 0 vouchers |
| full server package, own DB | `go test ./internal/server/ -count=1` (tally_w2) | `ok … 27.1s` |

Cassettes: `backend/internal/server/testdata/cassettes/agent.decide/{payment-rent-hdfc,sales-acme-retry}.jsonl`,
**SeedNote** (a deterministic in-repo scripted model, **not** a live provider — there are no keys). The
key is SHA-256 over the full rendered conversation with UUIDs normalised to `uuid#N` in order of first
appearance; `TestAgentCassetteKeyIsTheAgentsOwnRequestHash` pins the harness's wire-derived key to
`agent.RequestHash`. Regenerate: `TRAJECTORY_RECORD=1 go test ./internal/server/ -run TestAgentCassette`.

### Mutation proofs run in this session (each restored after)

1. `durable.Run` with `TryLock` replaced by an always-granted stub → `TestConcurrentPollers…ExactlyOnce`:
   *"executed 2 time(s) … (pollers reported [COMPLETED COMPLETED])"*; `TestRunRefusesWhileLocked` and
   `TestMemStoreConcurrentRunsExecuteOnce` (*"executed the step 16 times"*) also fail.
2. `"payment"` removed from `resourceRisk` → `TestEveryRowCarriesTheRiskItsKeyImplies` fails on the payment rows.
3. `computeHash` ignoring the payload → `TestTamperingIsDetected` fails.
4. `Structural`'s `ChannelNone` clause disabled → `TestStructuralRulesHold…` fails (*"post.batches is a write
   with no human path and was allowed"*, …).
5. The taint clause in `policy.Decide` disabled → `TestGateMatrix` fails on 3 rows **and** the injection
   suite fails: *"INJECTION SUCCEEDED: 1 voucher(s) in the books after "invoice-line-pay""*.
6. `agentGate` returning `""` → `TestAgentGateInTheRouter…` fails (*"router gate: 200 {"op":"DELETE"…"*);
   the injection suite stays green because the dispatcher's pre-check refuses first — two lines, each proved.
7. `agent_commit` INSERT replaced by a no-op → the sequential idempotency half fails (a second voucher,
   `Replayed:false`); INSERT made `ON CONFLICT DO NOTHING` → the race half: *"= 9, want 2"*.
8. The await step never writing `expired` → `TestAgentExpiredApprovalIsTerminal`: *"status:parked"*.
9. `runStore.Load` returning nothing → `TestAgentThreeDay…`: *"epoch 2 … calls:4, want … 0 model calls"*.

---

## 7. Migrations (PLAN-TALLY-PARITY §2.5 updated)

- **0092** `agent_intent_log`, `agent_intent_anchor`, `agent_intent_unanchored_companies()` (SECURITY DEFINER, ids only).
- **0093** `agent_autonomy` (`UNIQUE NULLS NOT DISTINCT`, level 0..3, paise band).
- **0094** `agent_run` (the only mutable agent table), `agent_run_step`, `agent_approval`, `agent_decision`,
  `agent_commit`, `agent_runs_due(p_now, p_limit)` (SECURITY DEFINER, ids only; `p_now` so the one clock is the engine's).
- **0095 unused.** No seeder restated, no ACL rule written: the routes use bare tokens `agent-run` and
  `agent-approval`, which Data Entry's `default_access = 'none'` refuses with zero rows
  (`TestDataEntryIsRefusedTheAgentPlane`, with the trial-balance control).

All ENABLE+FORCE RLS with USING+WITH CHECK and `(company_id, id)` company-scope UNIQUEs; each applied twice
inside `BEGIN … ROLLBACK` against `tally_w2` (exit 0). `testdb` fixture rows added for all eight tables
(`go test ./internal/platform/testdb` green). Seeder census unchanged.

---

## 8. Frontend contract

All routes are under `/v1/companies/{companyID}` with the ordinary bearer token. Money is **paise**
(integers) wherever a field ends `_paise`, and decimal **strings** in voucher bodies. Timestamps RFC 3339.

### 8.1 Runs

**`POST /agent/runs`** — `create agent-run`. Body:
```json
{ "instruction": "Pay the May office rent of Rs 25,000 from HDFC Bank, dated 10 May 2025.",
  "channel": "panel",
  "inputs": [ { "label": "HDFC stmt line 14", "provenance": "bank", "text": "NEFT/…" } ],
  "delegate": ["create payment"],
  "max_amount_paise": 5000000 }
```
`inputs[].provenance` ∈ `document | bank | whatsapp | tally_xml | email` (`user`/`books` are refused —
an input is external by definition). `delegate` are PDP ACL keys; a key an agent can never use (e.g.
`delete voucher`) is refused. → **202** `{"run": Run}`; **422** invalid; **503**
`{"code":"AGENT_UNCONFIGURED"}` when the server has no model (the shipped default). The run starts on the
next poller tick (≤ 2 s; the start kicks it).

**`GET /agent/runs?limit=50`** → `{"runs": [Run]}` newest first.
**`GET /agent/runs/{runID}`** → `{"run": Run & {"approvals": [Approval]}}`.
**`POST /agent/runs/{runID}/cancel`** — `alter agent-run` → `{"run": Run}` (status `cancelled`; nothing it
parked will ever commit).

`Run`:
```json
{ "id": "uuid", "created_by": "uuid", "agent_id": "khata-agent", "channel": "panel",
  "instruction": "…", "inputs": [ … ],
  "delegation": { "acl_keys": ["create payment"], "max_amount_paise": 5000000,
                  "expires_at": "…", "step_up": true },
  "status": "running | parked | completed | failed | expired | cancelled",
  "outcome": { "status": "completed", "final": "one-paragraph summary", "reason": "…" },
  "last_error": "", "attempts": 0, "next_poll_at": "…",
  "created_at": "…", "updated_at": "…", "finished_at": "…" }
```

### 8.2 The side panel's event stream

**`GET /agent/runs/{runID}/events?after=<seq>`** — poll it (e.g. every 1 s while `finished` is false; pass
the previous `next_after`). → 
```json
{ "run_id": "uuid", "status": "parked", "finished": false, "next_after": 12,
  "events": [ { "seq": 1, "type": "RUN_STARTED", "kind": "run_started", "at": "…",
                "actor_kind": "user | agent | system", "actor_id": "…", "on_behalf_of": "uuid",
                "action_id": "post.vouchers.payment", "acl_key": "create payment",
                "name": "…", "data": { … }, "hash": "64 hex" } ] }
```
`type` is the AG-UI vocabulary; `kind` is ours (lossless). Mapping: `run_started→RUN_STARTED`,
`run_finished→RUN_FINISHED`, `run_failed→RUN_ERROR`, `decision_step→STEP_FINISHED` (`name:"decide"`),
`tool_call | preview | verifier_error → TOOL_CALL_RESULT`, and `CUSTOM` with `name` = the kind for
`policy`, `approval_requested` (**the interrupt: render the approval card**), `approval_decided`,
`commit`, `commit_refused`. `data` per kind (ids, verdicts, paise, **hashes** of text; never prompts):
`preview {k, tool, ok, status, debit_paise, credit_paise, postings}`; `policy {k, mode, level, reasons,
amount_paise, context_sources}`; `approval_requested {approval_id, deadline_at, amount_paise, body_hash}`;
`approval_decided {approval_id, decision, edited, webauthn}`; `commit {k, status, voucher_ids, replayed,
idempotency_key, approval_id, mode}`; `run_finished/run_failed {status, reason}`. To show the preview itself,
fetch the approval (§8.3) — the event carries its id.

### 8.3 The approval inbox (a new ScreenKind; also reachable from the panel)

**`GET /agent/approvals?state=pending|all&run_id=<uuid>`** — `display agent-approval` → `{"approvals": [Approval]}`.
**`GET /agent/approvals/{approvalID}`** → `{"approval": Approval}`.

`Approval`:
```json
{ "id": "uuid", "run_id": "uuid", "step_key": "gate:3", "state": "pending | approved | rejected | ask_back | expired",
  "action_id": "post.vouchers.payment", "acl_key": "create payment", "risk": "money",
  "instruction": "what the human asked",
  "args": { "action_id": "post.vouchers.payment", "path_params": {}, "body": { "voucher_date": "2025-05-10", "narration": "…",
            "postings": [ { "ledger_id": "uuid", "dr_cr": "D", "amount": "25000.00" } ] } },
  "preview": { "status": 201, "effect": { "postings": [ { "ledger_id": "uuid", "ledger": "Office Rent", "dr_cr": "D",
               "amount": "25000.0000", "amount_paise": 2500000 } ], "debit_paise": 2500000, "credit_paise": 2500000, "vouchers": 1 } },
  "amount_paise": 2500000,
  "reasons": ["autonomy for create payment is L1: every commit waits for a human"],
  "provenance": { "context_sources": ["user", "bank"], "fields": { "narration": "bank" } },
  "on_behalf_of": "uuid", "requested_at": "…", "deadline_at": "…  (72 h after requested_at)",
  "decision": { "id": "uuid", "decision": "approve", "edited_args": { … }, "note": "…",
                "decided_by": "uuid", "decided_webauthn": true, "decided_at": "…" } }
```
The **diff view** is `preview.effect.postings` (what the real handler would post); `provenance.fields`
marks which body values appear verbatim in which untrusted input — render those loudly. `reasons` must be
shown verbatim (they say *why* a human is needed).

**`POST /agent/approvals/{approvalID}/decision`** — `alter agent-approval` (so a WebAuthn level must step
up; the approver's step-up is what the commit carries). Body:
```json
{ "decision": "approve | reject | ask_back",
  "edited_body": { …a full voucher body, approve only… },
  "note": "required for ask_back" }
```
→ **200** `{"approval": Approval}`; **409** `{"code":"ALREADY_DECIDED"|"APPROVAL_EXPIRED", "approval": …}`
(one decision per approval; a decision after `deadline_at` is recorded as `expired`); **403** if the caller
is an agent, lacks `agent-approval`, or (for approve) **may not take the underlying action themselves**;
**422** invalid (unknown decision, ask_back without a note, a non-object edit, an edit with reject); **404**.
After approve the run commits on the next tick; edit-then-approve commits `edited_body` through the same
validators (a refused edit appears as a `commit_refused` event, not a voucher). `ask_back` hands the note to
the model, which may propose again (a new approval). The keyboard contract is the frontend lane's: Ctrl+A
accept, as on the native screens.

---

## 9. ⛔ What is OWED — stated, not buried

1. **The W2 frontend** — side panel (AG-UI), the inbox `ScreenKind`, Gateway/Alt+G entries, pre-filling
   the native F8/F9/F5 screens. The eight agent routes are in `actionsWithNoHumanPathYet`
   (`maxActionsWithNoHumanPathYet` 27 → 35); wiring them deletes the rows.
2. **Breadth of write actions.** Only the 8 accounting voucher **creates** are previewable/committable.
   Owed, one checked handler at a time: alter-via-compensating-version, non-accounting stock vouchers,
   masters, orders, bank-rec match/unmatch, reminders, exports/prints; **undo as a generated reversal**.
3. **The parity gate** — 50 recorded keystroke traces replayed via UI and via agent → identical canonical
   voucher JSON. Not built.
4. **AgentDojo banking subset + InjecAgent** — only the 32 Tally-specific cases exist.
5. **Domain packs by embedding/BM25** — `actions.search` is the registry's lexical matcher (W3's job).
6. **Live-model cassettes** — both cassettes are SeedNote against a scripted fake; no provider keys exist
   (HANDOFF §6.1). The agent's real model is whatever `LLM_PROVIDER` selects; unset ⇒ 503, hermetic.
7. **"Born blocked" as a ledger flag** — realised here as "born only through a human decision"; a
   `ledger.blocked` column that also stops a human-approved-in-a-hurry party receiving a payment until a
   second look is owed.
8. **An autonomy admin API** — `policy.SetLevel` exists; no HTTP route or screen sets a level yet.
9. **Approver-authority test for a custom level** — `userMayTake` is exercised only by Owners in the
   tests; a level with `agent-approval` but not `create payment` is untested.
10. **Attribution on the voucher** — the committed voucher's `created_by` is the body's (`"agent"`); the
    attributable record is the intent log. Stamping `agent:<run> for <user>` is owed.
11. **Number provenance of the final summary** (W4) and **anchor verification exposed** (`VerifyAnchors`
    exists; no route or job calls it).
12. **Full `go test ./...` against a database was not run** for packages whose tests hard-code the shared
    `tally` DSN and apply migrations (ledger, masters, reporting, …): running them would migrate the shared
    database with 0092–0094. They ran with `CORED_SKIP_DB=1` (42 packages ok; `internal/server`'s four
    GSTR-3B backward-compatibility pins fail **by design** under `CORED_SKIP_DB` — they refuse to skip). The
    DB-backed runs were `internal/{server,security,platform/...,agent,policy,intentlog,durable,statutory}`,
    `cmd/...`, `migrations` against `tally_w2`, all ok. Test DSNs in `internal/server`, `internal/security`
    and `internal/statutory` now honour `ADMIN_DATABASE_URL`/`DATABASE_URL` (Makefile defaults unchanged).
13. **Load** — two contending pollers and eight contending commits are measured; hundreds are not.
