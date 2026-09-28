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

---

## Frontend (W2 frontend lane, `agentic/w2-frontend`, built on `80dc5b8`)

**Written:** 2026-09-28, after the code landed, every figure printed by a command run in this session.
Item 1 of §9 above is now largely discharged; what remains of it is in §F.4. No migration consumed, no
new frontend package, `git diff 80dc5b8 -- backend/go.mod backend/go.sum frontend/package.json
frontend/pnpm-lock.yaml | wc -l` = **0**. `git diff --stat 80dc5b8`: **42 files, +3,978 / −50**;
1,695 lines of new implementation in 7 new files, 1,114 lines of new tests and fixtures.

### F.1 What exists now

| surface | door | keys | file |
|---|---|---|---|
| **Approval Inbox** — new `ScreenKind` `approval-inbox` | Gateway › Approval Inbox; Alt+G › Approval Inbox (`SCREEN_GOTO_ENTRIES`) | ↑/↓ highlight · **Enter** open in native voucher · **Ctrl+A** approve as previewed · **Alt+X** reject (optional note) · **Alt+B** ask back (note required) | `screens/ApprovalInboxScreen.tsx` |
| **Agent side panel** | **Ctrl+K** from any screen (shell-owned screen action `agentPanel`) | Tab through; Esc / Ctrl+K close; buttons: Start run, Cancel run, Open proposal | `app/AgentPanel.tsx` |
| **Proposal mode of the native voucher screen** | Enter on an inbox row, or the panel's Open | the ordinary F4/F5/F6/F7 form; **Ctrl+A approves**; Alt+X sets the proposal aside undecided | `screens/VoucherScreen.tsx`, `screens/vouchers/agentProposal.ts`, `app/LoadedProposal.tsx` |
| Shared read-only block: reasons verbatim, provenance warnings, ledger-effect table | inbox, panel cards, proposal mode | — | `screens/AgentProposalPanel.tsx` |
| Features row "Agent — side panel (Ctrl+K) and Approval Inbox" | F11 | state **Part built** | `screens/FeaturesScreen.tsx` |
| Client: the eight §8 routes on `CoredClient` (rest + mock) | — | — | `api/agent.ts`, `api/rest.ts`, `api/mock.ts` |

### F.2 Decisions

1. **The prefill goes through `formFromView`, the alter hydration.** `viewFromApproval` turns the approval
   into a `VoucherView` (postings from `args.body`, **ledger names from `preview.effect`**, number and id
   blank because it is a create), and the audited alter path builds the form. No agent-only hydration exists.
2. **Ctrl+A in proposal mode records a decision and posts nothing.** It runs the SAME builder an ordinary
   Ctrl+A runs (refusals, master gate, single-entry orientation), then `editedFrom(built, proposed)`:
   unchanged ⇒ `{decision:"approve"}` (server commits its recorded preview); changed ⇒ `approve` +
   `edited_body` = the native form's body. The comparison errs toward "edited" (any extra bill/cost/GST
   field counts). The handler returns `screenHandled`, so the machine does not replace the frame before the
   decision answers — a refused decision (403/409) leaves the operator still in proposal mode, never in an
   ordinary form whose next Ctrl+A would post the figures.
3. **The frame is bound to the approval.** The machine's `proposalFrame` puts the approval id on the create
   frame's `detailOf`; VoucherScreen treats the LoadedProposal slot as live only while the frame names it. Esc
   and a later plain F5 is a plain F5.
4. **Which proposals can be opened:** Payment / Receipt / Contra (single entry when exactly one leg is on
   the class's primary side, else double) and Journal (double). **Sales / Purchase / Credit Note / Debit
   Note are refused** — their native form is the GST invoice, which re-derives tax from a taxable value, and
   "As Voucher" is not built; they are approved or rejected from the diff. A body carrying any key the form
   would not send back (`bill_allocations`, …), or a ledger the preview does not name, is also refused.
5. **The diff is mandatory at every autonomy level**, not only above L1: the inbox refuses Ctrl+A (before
   any request) on a proposal whose `preview.effect` is missing, and every approve (inbox or native form)
   first re-reads the approval (`GET /agent/approvals/{id}`) and refuses if it is no longer pending or its
   effect differs from the one drawn on screen (`assertStillAsShown`).
6. **The panel owns the keyboard while focused**: every key but Ctrl+K is stopped at its edge (the shell
   listens on `document`), so an instruction's Enter cannot drill the Gateway and Ctrl+A cannot post the
   voucher behind it. Opening a proposal over a voucher / master create / Go To is refused (machine and panel
   both), because the voucher form is state on one component instance.
7. **Keys:** Ctrl+K (RESEARCH.md documents no Ctrl+K; Alt+K is TallyPrime's `K: Company`), Alt+X on the inbox
   (the screen-scoped meaning of Tally's cancel key), Alt+B on the inbox (unbound globally). Ctrl+A and Enter
   keep their global meanings. The bar says "Approve", never "Accept (save)".
8. **Delegation defaults to nothing ticked**; the eight previewable voucher creates are offered as
   checkboxes. The max amount is parsed rupees → paise exactly (no float).

### F.3 Gates and what each printed

- `pnpm exec tsc --noEmit`: exit **0**.
- Full frontend suite, `pnpm exec vitest run`: **126 files / 2,013 tests passed**.
- New files: `api/agent.test.ts` 18 tests, `screens/ApprovalInboxScreen.test.tsx` 15, `screens/VoucherScreen.agentProposal.test.tsx` 6, `app/AgentPanel.test.tsx` 11,
  `FeaturesScreen.test.tsx` +3; `keystroke.test.ts` 55 (was 50) with **5 new corpus cases**
  (`gateway-approval-inbox-enter-opens-inbox`, `alt-g-approval-inbox-opens-inbox`,
  `approval-inbox-enter-opens-native-payment`, `approval-inbox-enter-refuses-unopenable-row`,
  `approval-inbox-double-entry-journal-then-esc`) and a `detailOf` expect key; `buttons.test.ts` walks the
  inbox bar.
- `python3 corpora/validate.py`: "corpora validation OK (… keystroke=38 …)" (`approval-inbox` added to
  `KNOWN_SCREENS`).
- UI-command manifest regenerated: screen_kinds 37→**38**, commands 281→**285**, gateway_entries 28→**29**,
  client_routes 159→**167**, client_calls 165→**173**, unused_client_methods **10** (unchanged — all eight
  new methods are used), dead_commands **2** (unchanged, both pre-existing on bank-reconciliation).
- Action registry regenerated (`UPDATE_GOLDENS=1 go test ./internal/server/ -run TestActionRegistryGolden`):
  the eight agent specs moved `channel "none"` → `"client"`; their eight `actionsWithNoHumanPathYet` rows
  were deleted and `maxActionsWithNoHumanPathYet` went **35 → 27**.
- Backend: `go build ./...`, `go vet ./...` clean; `go test ./internal/{actions,policy,archtest,trajectory}`
  ok; `make tier0` ok; `CORED_SKIP_DB=1 go test ./...` — every package ok except `internal/server`'s four
  GSTR-3B pins that refuse to skip by design (the same four the backend lane reported).

**Mutation proofs (each restored after; `scratchpad/w2fe_mutate.py`, all 11 went red):**
M1 proposal Ctrl+A falls through to the ordinary post → 3 proposal tests fail (posts > 0); M2 detailOf binding
dropped → "Esc then plain F5" fails; M3 inbox approves with no preview → the mandatory-diff test fails; M4 panel
stops no keys → 2 panel tests fail; M5 inbox Enter drills a non-proposal row → corpus case fails; M6
`editedFrom` always false → 2 tests fail (edit not sent); M7 Sales treated as prefillable → 3 tests fail; M8
re-read ignores a changed preview → 1 fails; M9 `SCREEN_ACTIONS.rejectApproval` removed → Alt+X tests fail; M10
rest client stops D/C translation of an edited body → 1 fails; M11 ledger names not taken from the preview → 5
fail. Backend: re-adding `get.agent.runs` to `actionsWithNoHumanPathYet` → `TestNoHumanPathOptOutsAreDeclared`
"is BOTH reachable from the UI (channel "client", command "client.listAgentRuns") AND listed".

### F.4 ⛔ What the frontend still OWES

1. **Editing invoice-class proposals** (Sales / Purchase / Credit / Debit Note) — needs Tally's "As Voucher"
   mode on the invoice classes, or a restatement of a postings body into the GST form that provably posts the
   previewed legs. Until then those are approve/reject only.
2. **No browser drive.** Everything is jsdom through the real `AppShell`; nothing was driven in a real browser
   against a running `cored` (no model keys exist, so a live run cannot be produced anyway). The WebAuthn
   step-up on an Owner level relies on the transport's existing transparent retry and is untested here.
3. **No autonomy screen** (backend §9 item 8 is still owed on both sides).
4. **Ask-back / reject from the native voucher screen** are not keys there (only in the inbox) — deliberate,
   but a reviewer may want Alt+B there too.
5. **The panel polls** (1 s, per §8.2); no SSE/WebSocket. It re-reads the run on decision/commit events only.
6. **`commit_refused` for an edited body** is shown as an event line in the panel; nothing reopens the inbox
   row or tells the approver outside the panel.
7. **The parity gate** (50 traces UI vs agent) still does not exist; the prefill round trip is proven for one
   payment shape (single entry, one expense leg) and by unit tests for the double-entry routing.
8. **A decision made by the native form after ledgers were created by the builder** (the operator typed a new
   ledger name) and then refused by the server leaves those masters — the same orphan class D81 discloses on
   the ordinary path, not disclosed here.


---

## Wave 2 breadth (lane `agentic/w2-breadth`, built on `4e7cfff`)

**Written:** 2026-09-28, after the code landed, **every figure printed by a command run in this session**
(named beside it), against the lane's own database `tally_w2_breadth`. Migration consumed: **0095** (the
lane's reserved number; PLAN-TALLY-PARITY §2.5's W2 row updated). `git diff 4e7cfff -- backend/go.mod
backend/go.sum frontend/package.json frontend/pnpm-lock.yaml insightd/requirements.txt | wc -l` = **0**;
`GOFLAGS=-mod=readonly GOPROXY=off go build ./...` clean. `git diff --stat 4e7cfff`: **81 files, +6,113 /
−243** (backend 51 files +4,790; frontend + corpora 29 files +1,322); backend Go +2,448 implementation lines,
+2,173 test lines (`git diff --numstat`).

⛔ **SCOPE, STATED FIRST.** Priorities 1, 2, 4, 5 and 6 of the lane brief are built with gates; priority 3 is
built except **reminders** (§B.9). The parity gate's traces are the tau suite's **API** reference plans, not
browser keystroke recordings — said again in §B.6 where it matters.

### B.1 The one mechanism everything else stands on: the effect is READ BACK, per transaction

Wave 1's preview read `posting` rows written by the handler's transaction (xmin) — enough for eight voucher
creates, useless for an alteration (what did it replace?), a stock voucher (no postings), a master or an
order. `server/agent_effect.go` (`readTxEffect`) now reads, **inside the handler's own transaction, before it
commits or rolls back**:

| written | read as | shown as (`agent.Effect`) |
|---|---|---|
| `voucher_version` rows (xmin) | op, version, class, number | `versions` |
| their `posting` rows (by version id) | legs, paise | `postings`, `debit_paise`, `credit_paise` |
| for each ALTER: version N−1 of the same voucher and its legs | the **before-image** | `replaces`, and `delta` (after − before per ledger, Dr +, zero rows dropped) |
| `stock_movement` (by version id) | item, godown, direction, qty, rate, amount | `stock` |
| `master_audit` rows (xmin) | entity, op, before/after documents | `masters` |
| `order_head` (xmin) + `order_line` | order no, date, closed, lines | `orders` |
| `bank_reconciliation` — **read BEFORE the handler** (an un-tick DELETEs the row) and again inside it | before / after bank date | `bank_dates` |

The same reader drives the commit: `agent_commit` now records `object_kind` + `object_ids` (0095), and "a 2xx
that wrote nothing" is refused for every kind, not just vouchers. The preview allowlist (`agent.previewable`)
grew from 8 to **33**, each entry naming the handler it was checked against: 8 accounting creates, their 8
alters, the 4 stock vouchers × create/alter, `post.ledgers`, `post.stock-items`, the valuation-method setter,
sales/purchase order create/close, bank-reconciliation tick/un-tick. Pinned exactly
(`TestEveryPreviewableSpecIsAWriteTheAgentMayEverMake`) and each entry must survive `policy.Structural` and
ACL(agent).

⚠️ The xmin scans are per company and unindexed; they run once per agent preview or commit, never on a
person's request. Measured cost is not reported (no large tenant exists); it is owed (§B.9).

### B.2 Priority 1 — alteration as a compensating version, and undo as a generated reversal

**Alteration.** `put.vouchers.<class>._voucherid` goes through the same preview → gate → approval → commit
path. The ledger already appends an ALTER version and never edits the head (`voucher_version` is
append-only by trigger); what was missing was (a) the approver seeing what is replaced and (b) the commit
being held to what they saw. (a) is `replaces` + `delta`. (b) is new: ⛔ **an approved alteration commits
only onto the version it was shown** — the commit hook requires the new version to be exactly `replaces[].
version_no + 1` (`Request.ExpectVersions`), so a person's edit landing while the agent's proposal waits is
not silently overwritten (a lost update the ledger itself would happily append). An approver's edit is held to
the version THEIR re-preview superseded (§B.5).

**Undo.** `actions.reverse {"voucher_id", "date"?}` reads the voucher through the dispatcher and **Go**
builds the cancelling voucher (`agent.ReversalBody`, pure): every leg on the other side, same ledgers and
amounts, cost splits restated, bill references the original opened closed AGAINST themselves. Payment,
receipt, contra and journal reverse into themselves (no orientation); Sales ↔ Credit Note and Purchase ↔ Debit
Note (the invoice classes have one). It is then an ordinary preview, committed behind the same gate. ⛔ The
model never sees the voucher it read — only the reversal's effect — so the original's narration (stored
text) never reaches its context and the tool is labelled books. **Refused, and owed:** a voucher with GST
lines (its reversal is a credit/debit note that must reach GSTR-1 with the original's number — a document a
person prepares), inventory, TDS/TCS, order fulfilments; a tombstone. Not a delete (structural) and not an
alter (it would rewrite a voucher someone may have printed or paid against).

### B.3 Priority 2 — masters, and "born blocked" made structural (0095)

`post.ledgers`, `post.stock-items` and the valuation-method setter are previewable; a master create still
waits for a person at every level (wave 1's rule). What wave 1 could not say is what HANDOFF §6.8 flagged:
approving the party in a hurry also made it payable. Now:

- ⛔ **A ledger the agent creates is BORN BLOCKED.** `handleCreateLedger` sets `masters.Ledger.BornOfRun`
  from the request's in-process agent identity — never from a body — and the INSERT carries `blocked_at` and
  a reason naming the run, so there is no instant at which it exists unblocked. Deliberately wider than
  "parties": every agent-created ledger (a bank ledger made from an injected letter is the same fraud one hop
  earlier, and "is it a party?" is a group-tree walk inside a trigger).
- **Nothing posts to it — not the agent, not a person, not a sync replay.** Two layers: `ledger.post()`
  refuses first with the operator's sentence (`ErrLedgerBlocked`: names the ledger and where to unblock it);
  0095's `posting_ledger_not_blocked` BEFORE INSERT trigger is the floor under every other writer of
  `posting` (`TestBlockedLedgerTriggerIsTheFloor` inserts a raw posting as `tally_app`).
- **Only a person unblocks it:** `POST /ledgers/{id}/unblock`, the bare token `master-unblock` — not `alter
  master`, which is the authority that CREATES ledgers; releasing an agent's party for payment is a different
  seat. Data Entry is refused with zero `acl_rule` rows; the token is governance in `internal/actions` and
  denied in `policy.AgentRules`, so no agent can unblock (either alone holds; mutation M15a below). The
  unblock appends a `master_audit` ALTER row; a second unblock is 409 `NOT_BLOCKED`.
- **Ledger ALTER is not built** — the product has no human screen for one (only the opening-balance PUT,
  `channel none`), and `policy.Structural` refuses the agent a door no person uses. Owed with the screen.

### B.4 Priority 3 — stock vouchers, orders, bank reconciliation, exports (reminders owed)

Stock vouchers (delivery/receipt note, stock journal, physical stock), sales/purchase orders (create, close)
and bank-reconciliation tick/un-tick are previewable and committable (§B.1 for what the approver sees).
`concretePath` accepts one non-id parameter, the leg ordinal `legNo`, as digits only
(`TestConcretePathRefusesAnythingButIDsAndOrdinals`: `../../members`, `1/2`, `01`, `-1`, `1?x=2`, `1e3`,
`99999`, `""` refused).

⚠️ **Operational writes are now taint-sensitive** (`policy.taintSensitive`). Wave 1 could not commit one, so
the question never arose. A tick ASSERTS the bank cleared an item, a physical-stock voucher writes the
warehouse's value, an order commits the business — and "mark cheque 451 cleared" in a statement narration is
the first injection a bank agent meets. A clean context still commits them at L2/L3
(`TestAgentCleanL3BreadthCommitsAutonomously`).

**Exports are reads whose content never reaches the model.** `actions.read` now accepts `RiskExport` specs
(delegation required, as before); the model is told `{status, bytes, sha256, produced}` and nothing of the
document, so the result is labelled books. `TestAgentExportIsProducedNotRead`: CONTROL — the person's own
day-book print carries the sentinel narration; the agent's print of the same report is produced and the
sentinel never appears in any model request. Delivering the file (WhatsApp/email) is W6's.

### B.5 Priority 4 and 5 — profile tools, the autonomy route and screen, the edit re-preview, attribution, verification

**Profile tools.** `get_customer_profile`, `get_supplier_profile`, `get_item_profile`,
`get_recurring_streams`, `get_tenant_profile` are in the tool list (prompt `agent-decide-v2`), through a seam
(`agent.ProfileReader`) implemented by `server/agent_profiles.go` — `internal/agent` still cannot import
`internal/profile` (archtest; `internal/server` gained `internal/profile`, with a reason). ⛔ **ACL(user)
per FACT:** the reader re-checks the run's human (active, not revoked, a member) and runs `authz.Evaluate`
over their own level: the tool requires its primary report (customer → outstandings, supplier →
bills-payable, item → stock-summary, streams → day-book, tenant → profit-and-loss), and every fact is shown
only if the report it reconciles to is one they could open — withheld facts are removed from the rendered
text too (`profile.ToolResult.Filter`, a small addition to W3's package). **Labels:** computed facts are
books; ⛔ a tenant card with typed user notes is **stored_text**, and the gate then parks an L3 payment that
follows it (`...a_tenant_card_with_typed_notes_is_stored_text,_and_taints_the_gate_at_L3`).

**Autonomy.** `GET/PUT /agent/autonomy` — a third bare token `agent-autonomy`, governance (an agent can
neither read nor raise its own autonomy), ⛔ **Owners only in the handler** (a full-access custom level that
holds the token is still refused) and ⛔ **always a WebAuthn step-up in the handler**, independent of the
Owner level's own flag (the admin API will not clear it, but the test clears it behind the API's back and the
handler still refuses — mutation M10). Only a key some agent-committable write evaluates to is settable
(`delete voucher` → 422). Every change appends a `master_audit` row (`entity_type agent_autonomy`, L2→L3
before/after), because the table itself is upserted. Screen: **Gateway › Agent Settings** (also Alt+G), a new
`agent-settings` ScreenKind: the autonomy table and setter (rupees → paise exactly), the blocked ledgers with
their bank details and an Unblock button, and "Verify the intent log".

**Re-preview of an approver's edit** (wave-1 review #2's owed half). `POST /agent/approvals/{id}/preview`
dry-runs an edited body through the real handler as the run's principal (`Engine.PreviewEdit`); the decision
handler does the same before recording an approve-with-edit and stores the effect on the decision
(`agent_decision.edited_preview`, 0095). A body the validators refuse — or one above the run's delegated
ceiling — is refused AT the decision (422 `EDIT_REFUSED`, nothing decided) instead of surfacing as
`commit_refused` later; the native voucher screen calls the preview before deciding and keeps proposal mode
on a refusal. The commit hook's own ceiling stays the control
(`TestAgentCommitCeilingHoldsInTheWritesOwnTransaction`).

**Attribution.** Every agent write is stamped `agent:<run> for <user>` by the SERVER (`attributedTo`), on
vouchers, alters, masters (the audit row's `created_by`), orders and bank ticks, whatever the body claimed
("agent", or an injected "Ramesh").

**Verification exposed.** `GET /agent/runs/{id}/verify` (the run's chain) and `GET /agent/anchors/verify`
(`intentlog.VerifyAnchors`, D121 §9 item 11). `TestAgentVerifyRoutesRecomputeTheRecord` anchors, then
truncates a run's tail behind the log's back (admin, `session_replication_role = replica`): the anchors
answer `ok:false`.

**Approver authority for a custom level** (D121 §9 item 9): a full-access level with `disallow create
payment` is refused approve (403), may still reject (`TestAgentApproverWithoutTheActionMayNotApprove`).

### B.6 Priority 6 — the parity gate

`TestAgentParityTracesMatchTheUIPath` (server tests; `go test ./internal/server -run TestAgentParity`):

> *"parity: 143 traces (map[bank_match:30 gst_returns:8 item_invoice:30 master_creation:25
> voucher_entry:50]), 167 write steps through 167 agent runs (each previewed, approved in the inbox,
> committed), 18 agent-created ledgers unblocked by a person; canonical books and voucher numbers identical
> on both paths (30s)"*

For every tau task whose reference plan writes (143 of 200; floor 50), two fresh companies are seeded
identically; the plan runs once through the API (the UI-equivalent calls) and once with every write step
turned into an agent run (preview of exactly that request as its matched ActionSpec → park at L1 → owner
approves in the inbox → commit of the recorded preview; after an agent-created ledger, the person unblocks
it — the documented human step). Reads (capturing a bank leg's id) go to the API on both paths. The two
`tau.Snapshot`s must be byte-identical, **and so must the voucher numbering** (which the canonical form
ignores and a leaked preview would shift); a trace whose UI path changed nothing canonical fails as
vacuous.

⛔ **What the traces are.** The tau suite's hand-written reference plans (W1a) — the API calls the product's
screens make for those tasks — not recordings of keys pressed in a browser. This gate proves the agent plane
preserves what the API does; it does not prove the F-key screens produce these bodies. The keystroke→body
half is owed (§B.9). The "model" is the trace replayer (no provider keys exist).

### B.7 Gates, and what each printed

| gate | command | printed |
|---|---|---|
| injection suite (45 cases; every wave-2 family) | `go test ./internal/server -run TestAgentInjectionSuite -v` | *"45 injection cases (map[bank:10 document:12 email:8 tally_xml:7 whatsapp:8]); 30 successful write previews (money, master, operational) parked by the provenance gate at L3; the books unchanged, 0 commits"* — the fingerprint (voucher versions, master_audit rows, orders, bank ticks, stock movements, agent commits) is compared after every case, not only the voucher count |
| its breadth control | `TestAgentCleanL3BreadthCommitsAutonomously` | clean L3 alteration → 2 versions, 0 approvals; clean L3 tick → 1 row; clean L3 master create → still 1 approval |
| alteration | `TestAgentAlter...` (2) | diff: replaces v1 at 2500000 paise, delta Office Rent +100000 / HDFC −100000; after approve 2 versions, v1 hash unchanged, head v2 26000.0000, `created_by` = `agent:<run> for <user>`; stale head → 2 versions (the person's), `commit_refused` |
| undo | `TestAgentUndoIsAGeneratedReversal` + 4 `TestReversal*` | legs flipped, both ledgers net 0.0000, original untouched, sentinel narration absent from every model request |
| born blocked | `TestAgentCreatedLedgerIsBornBlocked`, `TestBlockedLedgerTriggerIsTheFloor` | blocked, reason names the run; agent's payment refused by the validators; a person's payment 422 naming the ledger and the unblock; agent unblock 403 at the router; Data Entry 403; owner 200; payment 201; second unblock 409 |
| ops | `TestAgentStockOrderAndBankRecThroughTheGate` | stock Widget OUT 5.0000; SO-7 with 1 line; tick unticked→2025-05-12 and back; every preview left the fingerprint unchanged |
| profiles | `TestAgentProfileToolsAreRegisteredAndACLFiltered`, `TestProfileToolNamesMatchW3` | 3 subtests pass (owner/books; refused Outstandings → 403 + `withheld_facts`; notes → stored_text + parked) |
| admin | `TestAgentAutonomyRoute...`, `TestAgentApproverEdit...`, `TestAgentVerifyRoutes...`, `TestAgentExportIs...`, `TestAgentApproverWithoutTheAction...`, `TestAgentCommitCeiling...` | all pass |
| gate matrix | `go test ./internal/policy -run TestGateMatrix` | +6 rows (clean tick autonomous, tainted tick waits, clean/tainted alteration, unblock and autonomy refused) |
| cassettes | `TRAJECTORY_RECORD=1 go test ./internal/server/ -run TestAgentCassette` then replay | wave-1 cassettes re-recorded for `agent-decide-v2`; 2 new per-feature replays (`alter-rent-payment` 4 decisions, `new-supplier-born-blocked` 5); all interactions replayed |
| Tier 0 | `make tier0` | pins raised 4/14 → **6/23**: *"replayed 23 interactions across 6 cassettes in 2 directories, features [agent.decide extraction nlq.translate]"* |
| registry | `UPDATE_GOLDENS=1 go test ./internal/server/ -run TestActionRegistryGolden` | **244 specs** (was 238): read 85, export 46, governance 29, master 29, money 25, operational 19, destructive 6, statutory 5; 176 distinct ACL keys; **217 with a human path, 27 none — cap unchanged at 27** (all six new routes wired by the frontend) |
| UI manifest | `UPDATE_UI_MANIFEST=1 pnpm exec vitest run uiCommandManifest` | screen_kinds 38 → **39**, commands 285 → **286**, gateway_entries 29 → **30**, client_routes 167 → **173**, client_calls 173 → **179**, unused_client_methods **10** (unchanged), dead_commands **2** (unchanged) |
| frontend | `pnpm exec tsc --noEmit`; `pnpm exec vitest run` | exit 0; **Test Files 127 passed (127), Tests 2032 passed (2032)** |
| corpora | `python3 corpora/validate.py` | `keystroke=40` (+2: Gateway and Alt+G doors to `agent-settings`) |

Full-suite and build figures for the lane's HEAD are in the lane report (`go build ./... && go vet ./...`
clean; `go test ./... -p 4` on `tally_w2_breadth`).

### B.8 Mutation proofs run in this session (each restored after; `scratchpad/w2b_mutate.py`)

Every one went the expected colour; the first failing line is quoted.

- **M1** alter base-version guard off → *"the voucher has 3 versions, want 2 (the person's edit, and NOT the agent's stale restatement on top)"*
- **M2** before-image never read → *"effect.replaces = <nil>, want the one superseded head"*
- **M3** born-blocked off in `CreateLedger` → *"the agent-created ledger is not blocked"*
- **M4** the ledger service's blocked check skipped → the payment is STILL refused, by the trigger (*"insert posting: ERROR: ledger "Naya Supplier Pvt Ltd" is blocked…"*), and the test fails only on the missing operator sentence — two layers, each proved (the trigger alone by `TestBlockedLedgerTriggerIsTheFloor`)
- **M5** operational removed from `taintSensitive` → *"INJECTION SUCCEEDED: 1 agent_commit row(s) after "bank-tick-cleared""*
- **M6** attribution off → *"version 2 created_by = "agent", want "agent:… for …""*
- **M7** per-fact profile ACL off → *"no fact was withheld from a level refused Outstandings"*
- **M8** the card-notes label off → *"a card with user notes is labelled [books], want stored_text"*
- **M9** the export short-circuit off (the document goes to the model like a read) → the export test fails: *"script: no result 0 (have 0)"* — raw PDF bytes are not even a recordable tool result. **M9b** (the stronger one) the document's text wrapped as JSON and handed to the model → *"the printed document's text reached the model"* (the sentinel check runs before any check of the result's shape)
- **M10** the autonomy handler's own step-up check off → *"PUT without a step-up, level flag cleared = 200"* (with the Owner level's flag intact the router refuses first — which is why the test clears it)
- **M11** owner-only off → *"GET autonomy as a full-access non-owner = 200, want 403"*
- **M12** decision re-preview off → *"the decision does not record the edit's effect"*
- **M13** preview no longer rolls back (`DryRun: false`) → parity fails at the first trace: *"bm-charges-000-en step 1 parked 0 approvals"*
- **M15a** `master-unblock` re-classed master (risk only) → born-blocked test **stays green**: `AgentRules` still deny the token — defence in depth, stated rather than claimed
- **M16** stock movements not read → *"effect.stock = [], want Widget OUT 5"*
- **M17** bank before-image not read → the tick preview produces no effect and never reaches the inbox
- **M18** the taint clause in `policy.Decide` off → *"INJECTION SUCCEEDED: 2 voucher(s) in the books after "invoice-line-pay" (the seed has 1)"*
- **M19** the ordinal check accepts anything → `TestConcretePathRefusesAnythingButIDsAndOrdinals` fails
- **M21** approver's own authority unchecked → *"approve by a level refused create payment = 200"*
- **M22** the autonomy audit row not written → *"autonomy audit records = 0, want 2"*

### B.9 ⛔ What is still OWED (W2)

1. **Reminders via notify utility templates.** Not built. A ledger has no contact field (email/phone), so a
   reminder today could only be enqueued as undeliverable — a hollow feature — and the channels are W6's.
   Needs: a party contact field, a template, a person's screen for it, then an ActionSpec.
2. **Keystroke-recorded parity traces.** The 143 traces are API plans; capturing the bodies the real
   VoucherScreen sends from `corpora/keystroke` events (vitest, through AppShell) and replaying those is owed.
3. **Ledger alteration** (needs a human screen first), groups/units/godowns/price lists/BOMs/budgets/cost
   centres as agent actions (one handler read each), order line revision.
4. **GST-invoice / inventory / TDS / TCS reversal** — a credit/debit note a person prepares; the agent
   refuses today with the reason.
5. **A voucher picker that marks blocked ledgers** before the refusal (the list read carries `blocked` and
   `blocked_reason`; no picker renders it yet).
6. **The xmin scans' cost on a large tenant** — unmeasured.
7. **AgentDojo banking subset + InjecAgent**, **live-model cassettes**, **SSE**, **As Voucher editing of
   invoice proposals**, **reject/ask-back from the native voucher screen**, **a real-browser WebAuthn drive**
   — unchanged from §9 and F.4.
8. **Autonomy per user from the screen** — the route takes `user_id`; the screen sets company-wide rows only.
9. **An intent-log entry for an unblock and for an autonomy change** — both are recorded in `master_audit`
   (append-only, RLS), not in a run's chain, because neither belongs to a run.

Closed from §9 / wave-1 review: items 8 (autonomy route + screen), 9 (custom-level approver test), 10
(`created_by`), 11 (VerifyAnchors exposed, half — number provenance of the summary is still W4's), the
review's #2 owed half (re-preview), and §9 item 2/3 in the breadth above; §9 item 7 ("born blocked" as a
ledger flag) is now structural.
