# HANDOFF — CLEAR A WHOLE TIER IN ONE SESSION. THE ORCHESTRATION *IS* THE DELIVERABLE.

> **Written 2026-08-05.** Supersedes `HANDOFF-TIER-ONE.md` and `HANDOFF-AFTER-T6.md` as the plan of
> record. `PLAN-TALLY-PARITY.md` §2.5 remains the **only** authority on migration numbers — but read
> **§6.1 of this file before using any reserved block**, because one of them is now unusable for a
> whole class of change.
>
> **Why we are in a hurry, stated so it shapes decisions:** we are still trying to be *feature-locked*
> with Tally. Nothing we build here differentiates us. Every session spent on parity is a session not
> spent on the things that actually win — so the goal is to clear parity fast, without buying speed
> with wrong numbers. Those two constraints fight, and §5 is where the fight is resolved.
>
> **Read in this order:** §0 (the arithmetic — it changes what you attempt) → §1 (run-first) →
> §2 (the machine is the constraint) → §3 (⛔⛔ the orchestration pattern — **this is the session's
> main lesson**) → §4 (which tier, and the lane plan) → §5 (the evidence bar) → §6 (registration +
> migrations) → §7 (traps) → §8 (human-only) → §9 (done).

---

## 0. ⛔ READ FIRST: THE ARITHMETIC, AND WHY "ALL OF IT IN ONE SESSION" FAILED LAST TIME

`HANDOFF-TIER-ONE.md` asked for **14 feature rows in one session**. The session delivered **one**
(T6), at roughly **1.7M subagent tokens across four workflows**. That is not a criticism of the plan's
ambition; it is data, and it should change what the next session attempts.

**Where the time actually went**, measured:

| Cost | Measured | Fixable? |
|---|---|---|
| Barriers between workflow phases | 58 min for ~20 min of parallel work | **YES — §3.2** |
| Re-reading large files per agent | `mock.ts` 2,600 lines; 120–240 tool calls per agent | Partly — §3.4 |
| Manifest deduplication by hand | 12 entries for 6 registrations | **YES — §3.3** |
| Adversarial review finding real defects | 2 blockers + 6 serious, all invisible to green tests | **NO — do not cut this** |
| An unplanned live security defect | ~1 hour | No, and it was worth it |

**So the target is a TIER, not a row count, and the tier is chosen to be clearable.** §4 picks it.

> **The one thing that must not be optimised away:** in this session *every* defect that mattered was
> found by driving the app or by an adversarial reviewer, and **none by the test suite**. Two of them
> — a form no human could complete, and a security control that never ran — would have shipped. If
> you cut the review pass to go faster, you will ship those.

---

## 1. RUN-FIRST CHECKLIST — ten minutes, and it has burned every session that skipped it

```bash
# 1. Tree clean? Commit or stash BEFORE fanning out, or N worktrees branch from a dirty base.
git status --porcelain

# 2. Postgres. A TCP connect is NOT liveness (docker-proxy holds the port past a dead backend).
docker ps | grep tally_pg
pg_isready -h localhost -p 5433 -t 5 && \
  PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"

# 3. Kill stale servers BY PID. `pkill -f "cmd/cored serve"` misses the compiled child.
lsof -nP -iTCP:8087 -sTCP:LISTEN     # -> kill -9 that PID
ps -eo pid,etime,command | grep "cored serve" | grep -v grep   # etime is AGE: 01:03:31 = 1h3m
lsof -nP -iTCP:5173 -sTCP:LISTEN     # a stale vite serves day-old code just as happily

# 4. Baseline, and WRITE IT DOWN. -v or the SKIP census is a lie.
cd backend && go test ./... -count=1 -v 2>&1 | tee /tmp/base.log | tail -3
grep -c -- '--- PASS' /tmp/base.log; grep -c -- '--- SKIP' /tmp/base.log   # SKIP must be 0
cd ../frontend && pnpm exec tsc --noEmit && pnpm run test:run
cd .. && make verify
```

**Baseline as of this handoff (measured, clean worktree, HEAD `28e4a18`):**

| Gate | Value |
|---|---|
| backend `go test ./... -count=1 -v` | **2126 pass / 0 SKIP / 0 FAIL** |
| frontend `pnpm run test:run` | **507 pass / 43 files** |
| `pnpm exec tsc --noEmit` | clean |
| `make verify` | exit 0 |
| `verify:demo` | **33/33** (run ALONE) |
| `gofmt -l internal/ migrations/` | pre-existing drift in `closing_stock.go`, `closing_stock_statement_test.go` — **not yours, do not fix by reformatting the directory** |

### 1.1 ⛔ `make verify` DOES NOT RUN `tsc`. This cost this session a shipped type error.

I edited a test, ran `make verify`, got exit 0, and committed a type error. An agent found it later.
`pnpm exec tsc --noEmit` is a **separate gate**. Never read "verify passed" as "types are fine".
Folding `tsc` into `make verify` is a 10-minute job and would be a genuine improvement — consider it
the cheapest item on this list.

### 1.2 Know how to run the app — three of the last five defects came from using it

```bash
make run-cored      # :8087, sets PORT + AUTH_DEV_TOKEN_ENABLED
make run-insightd   # :8088
cd frontend && pnpm dev   # :5173
open "http://localhost:5173/?company=$(cat frontend/.demo-company-id)"
```
Sign in `browser-dev-owner` / `tally-demo-2026`. **It is a keystroke app: a click SELECTS, `Enter`
OPENS.** Launch detached and **return from the command** — `(nohup … &)` inside a command that then
blocks dies with your own timeout (it SIGTERM'd the process group and took the server with it).

---

## 2. THE MACHINE IS THE CONSTRAINT — "parallelize everywhere" has a hard ceiling

**8 cores, 16 GB**, shared with local CI and other worktrees. Therefore:

- A workflow caps at **`min(16, cores-2)` = 6 concurrent agents**. Asking for 20 does not give you 20.
- **RUN ONE WORKFLOW AT A TIME.** `PLAN-TALLY-PARITY.md` §2.1: three concurrent workflows means up to
  18 agents each spawning a compiler, and *that*, not the work, is what turns a track into an
  afternoon.
- **ALL WORKTREES SHARE ONE POSTGRES** on :5433. Concurrent `migrations.Apply` races; the residue
  check counts rows globally.

> **So the parallelism you actually have is ~6 agents on disjoint files, one workflow at a time. Speed
> therefore comes from REMOVING BARRIERS, not from adding agents.** That is §3.

Heavy commands are auto-queued through `ci-slot` (2 slots) by a PreToolUse hook. **Do not wrap
anything yourself.** A command that pauses a few seconds is waiting, not hung.

---

## 3. ⛔⛔ THE ORCHESTRATION PATTERN — THE MAIN LESSON OF THIS SESSION

### 3.1 What to give every lane agent, every time

1. **The domain contract, quoted.** The migration's `CREATE TABLE` with its CHECKs. Agents that got
   the constraints verbatim produced correct validation; the one that got a paraphrase invented a
   third copy of a vocabulary.
2. **The precedent file to copy, by path.** "Model it on `masters/deductor_status.go` and
   `handler_deductor_status.go`" produced structurally correct code first time.
3. **The decisions ALREADY MADE, as orders, with reasons.** Every decision you leave open comes back
   as a plausible wrong answer you then have to argue with.
4. **The forbidden-files list, and the manifest schema** (§3.3).
5. **The traps, each with its cost.** "A missing `SCREEN_ACTIONS` entry ships the key DEAD with 459
   green tests" got obeyed. "Be careful with registrations" would not have.
6. **What they may run**: `go build ./...`, `go vet ./<pkg>`, `gofmt -l <named files>`, the frontend
   vitest suite, and the **no-DB** gates. Explicitly: **NO DB-backed tests** (shared Postgres).

### 3.2 ⛔ BARRIERS DOMINATE WALL CLOCK. Default to `pipeline()`, review INLINE.

The frontend workflow was api → (panel ‖ screen) → review: **three sequential stages, 58 minutes**,
for two agents that overlapped for 15. The fix, in order of value:

- **Put the review INSIDE the lane, not after it.** Make each lane a `pipeline()` of
  *implement → adversarially verify*, so lane B's review runs while lane C is still implementing.
  A trailing review phase is a barrier across every lane at once.
- **Barrier only when a stage genuinely needs ALL of the previous stage.** Dedup across findings, or
  an early-exit on zero, are real reasons. "It's cleaner" is not.
- **Front-load the shared contract yourself** rather than making it a phase. This session's API layer
  was a barrier because the screens needed its types. Writing those types by hand first (20 minutes)
  would have let both screens start immediately.

### 3.3 The manifest protocol — and the guardrail it claimed that DOES NOT EXIST

Lane agents must not edit the shared registration files. They return a manifest; **you** apply it.
Schema that worked:

```js
entries: [{ file, anchor /* verbatim, UNIQUE in the file */, placement: "before"|"after",
            insert, why }]
```

Then, when applying — and this is the part that matters:

```python
# Re-verify uniqueness AT APPLY TIME, per entry: an earlier insert can introduce a
# second occurrence of a later anchor.
assert s.count(anchor) == 1
```

**⛔ THE MANIFEST WILL LIE TO YOU IN TWO SPECIFIC WAYS, both seen this session:**

1. **It comes back DUPLICATED.** Twelve entries for six registrations, because two lanes each
   produced a full set for the same shared file. One named a handler that did not exist. Deduplicate
   by hand and diff the survivors.
2. **It asserted a guardrail that is FALSE.** It claimed chi *panics* on a duplicate
   method+pattern registration, "so applying both takes the server down loudly". **It does not** —
   `node.setEndpoint` overwrites silently. Applying all twelve would have compiled, passed every
   test, and left four registration points each carrying two contradictory comment blocks.
   **A duplicate registration is SILENT. There is no loud failure to rely on.**

### 3.4 A LANE'S "GREEN" IS WEAK EVIDENCE. Budget for the integrator to be the one who knows.

Because lane agents cannot touch the shared Postgres, **22 challan and profile tests had never been
executed** when they were handed to me. All 22 passed on first run — that was luck, not proof. Plan
for a serial integration phase that runs every DB-backed test the lanes wrote, and treat a lane's
report as a *claim* until then.

### 3.4a THE SKELETON — copy this, do not re-derive it

The shape that follows from everything above: **one workflow, one lane per track, review INSIDE the
lane, no trailing barrier.** Three lanes × (implement → verify) = 6 agents, which is exactly the cap.

```js
export const meta = {
  name: 'tier1-remnant',
  description: 'T2 cost centres, T3 orders+BOM, T5 e-Way Bill — each implemented then adversarially verified in its own lane',
  phases: [{ title: 'Lanes', detail: 'three tracks, each implement -> verify, no barrier between lanes' }],
}

const LANES = [
  { key: 'T5', label: 'eway-bill',    prompt: T5_PROMPT },  // fully disjoint — starts immediately
  { key: 'T2', label: 'cost-centres', prompt: T2_PROMPT },  // owns the ledger payload
  { key: 'T3', label: 'orders-bom',   prompt: T3_PROMPT },  // rebase on T2's payload; critical path
]

// pipeline(): T5's verifier runs WHILE T3 is still implementing. That is the whole point —
// a trailing review phase would make every lane wait for the slowest.
const results = await pipeline(
  LANES,
  (lane) => agent(COMMON + lane.prompt,
    { label: `impl:${lane.label}`, phase: 'Lanes', schema: MANIFEST }),
  (built, lane) => agent(REVIEW_PREAMBLE + reviewTask(lane, built),
    { label: `verify:${lane.label}`, phase: 'Lanes', schema: REVIEW, effort: 'high' })
      .then((v) => ({ lane: lane.key, built, verdict: v })),
)

return results.filter(Boolean)
```

Then **you**, serially, as the integrator: dedupe and apply the manifests (§3.3), run every DB-backed
test the lanes wrote (§3.4), fix what the verifiers found, drive it in a browser, and commit per lane.

**Two things to resist.** Do not add a fourth lane to "use spare capacity" — the cap is 6 and the DB
is shared. Do not add a synthesis agent at the end; you are the synthesis, and you hold the context
the agents do not.

### 3.5 Worktree isolation: use it only for genuine write conflicts

`isolation: 'worktree'` costs ~200–500 ms + disk per agent and makes integration harder (you must
copy files out). Lanes writing **new files** in disjoint packages do not need it. Lanes that mutate
the same existing files should not both exist — restructure instead.

---

## 4. THE TIER TO CLEAR, AND THE LANE PLAN

### 4.1 Which tier — and why I recommend the smaller one

`PLAN-TALLY-PARITY.md` defines the tiers. Current state:

| Tier | Tracks | Status |
|---|---|---|
| **Tier 1** (§3) | T1 printing, T2 cost centres, T3 orders+BOM, T4 bank recon, T5 e-Way Bill, T6 TDS/TCS, T7 features screen | T1 ✅ T4 ✅ T6 ✅ T7 ✅ — **remnant: T2, T3, T5** |
| **Tier 2** (§4) | T8 multi-currency, T9 FIFO/LIFO, T10 stock groups, T11 budgets, T12 GSTR-9, T13 interest, T14 backup/restore | all 7 open |
| Beyond the plan | T15 Export, T16 print templates, T17 SSO, T18 QR (**never**) | all open |

> ### ⛔ RECOMMENDATION: CLEAR TIER 1. It is three tracks — T2, T3, T5 — and it genuinely COMPLETES a
> tier, which is what was asked for. Tier 2 is seven tracks and at this session's demonstrated rate
> that is an over-reach; attempting it yields three finished and four half-done, which is worse than
> three finished and four untouched.

T3 is also, by the plan's own words, **the most-used missing feature** in the product. Finishing
Tier 1 is the highest-value three tracks available.

### 4.2 The lane plan — three lanes, genuinely disjoint

| Lane | Track | Files it owns | Conflicts with |
|---|---|---|---|
| **A** | **T2 cost centres + categories** (0040–0041) | `internal/costing/*` (new), ledger payload | Lane B via the ledger payload — **A goes first, B rebases** |
| **B** | **T3 orders + BOM** (0042–0044) | `internal/orders/*` (new), inventory read side | Lane A (payload), and it is the **critical path** |
| **C** | **T5 e-Way Bill** (0047–0048) | `internal/statutory/*` | nothing — fully disjoint, run it alongside either |

**Sequence:** start **C in parallel with A**; start **B when A's payload shape is committed**. Lane C
never touches money or inventory, so it is free parallelism — take it.

### 4.3 The per-track traps, each of which has corrupted something real

**T2 — cost centres (HIGH).** ⛔ Allocations live **INSIDE the hashed canonical payload**, and the
sum-to-posting invariant is a **constraint trigger**, not a form check. A cost centre and a cost
category are **different axes** — an allocation belongs to both. T11 and T24 inherit this shape, so
getting it wrong costs three tracks.

**T3 — orders + BOM (HIGHEST RISK).** ⛔⛔ **AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT.** Push
it through `stock_movement` and closing stock — and therefore the Balance Sheet — is corrupted.
**Partial fulfilment is the normal case, not an edge**: an order is consumed progressively by
delivery notes and invoices, and the outstanding balance is the entire point of the feature.

**T5 — e-Way Bill (medium).** **Reuse the IRN precedent in `internal/statutory` wholesale** — it is
the worked example: NIC is unreachable, so ship the deterministic fake plus the recorded schema and
**say so on the document's own face** (`invQRNotPrintedNotice` in `internal/printing/invoice.go` is
the template, and note its conditioning rule: the notice fires on the artefact being the KIND of
thing the gap applies to, not as a global constant). Distance ≥ 50 km and value > ₹50,000 drive
applicability; Part A / Part B split; validity is a function of distance. **Do not claim integration.**

---

## 5. THE EVIDENCE BAR — this is where "go fast" and "be right" are reconciled

Carried forward from `HANDOFF-FEATURE-LOCK.md` §1.1, all still true:

- `go test ./... -count=1 -v`, **0 SKIP, 0 FAIL**. Without `-v` no SKIP lines print, and **a skip is
  indistinguishable from a pass in the exit code**.
- **Bracket long runs with a live `psql` query, before and after.** DB down ⇒ every DB test skips ⇒
  `go test` exits 0. This has invalidated an entire wave of agents before.
- **Every new invariant gets a MUTATION PROOF**: break it, watch the *right* test fail with a message
  stating the consequence in rupees or in a user's words, restore, and **say that you did it**. Five
  mutation proofs in this session each caught exactly what they were aimed at — and one of them
  (reading the generated golden) exposed a defect in my own code that four passing tests had missed.
- **Every differential test gets a CONTROL.** A 403 that proves nothing because the cutoff was never
  live is worse than no test.
- **NEVER invent a rate, threshold, clause letter, code or keystroke. Cite it or refuse.**
  `RESEARCH.md` is citable by line for every Tally keystroke; primary sources live in
  `docs/statutory/`.
- **Record the EDITION.** "Verified against the Act" is not durable: an Act is amended annually. A
  TCS rate shipped at **half the legal rate for four months** because a verification read an
  unamended edition while the amended one sat unread in the repo. Re-read on every Finance Act.
- **NEVER `git add -A`.** It has committed reviewer scratch twice. Stage explicit paths and grep the
  staged list for `zz_`, `scratch`, `probe`, `.bak`.
- Money is `decimal.Decimal` server-side, fixed-point **strings** on the wire, scaled integers
  client-side. **No `float64`, no `parseFloat`, no `Number()`, no `toFixed` on an amount, anywhere.**
- Any Go test that creates a company **must** call `testdb.Cleanup(t, companyID)` or
  `internal/testhygiene` fails the build. **Do not use `testdb.Keep`** — `declaredKeeps` is asserted
  empty.
- Every new tenant table needs a row in `buildFixture` (`internal/platform/testdb/testdb_test.go`) or
  the teardown census fails **by name**.

### 5.1 The question that would have caught this session's worst defect

The challan form asked the operator to type a **voucher UUID** and a **0-based leg ordinal** —
identifiers **no screen in this product displays**. It passed every test, because the tests keyed
mock fixture ids the real server rejects as non-UUIDs.

> **Ask of every new form: can a human obtain EVERY value it demands, from inside this product?**
> If not, the feature is not built, however green the suite is.

---

## 6. MIGRATIONS AND REGISTRATIONS

### 6.1 ⛔⛔ THE RESERVED BLOCKS ARE NOW UNUSABLE FOR ANY ACL SEEDER CHANGE. READ THIS.

`seed_company_auth_defaults` can only be extended by **restating the whole plpgsql body**, and
`migrations.Apply` runs files in `sort.Strings` order — so **the highest-numbered restatement is the
only body that survives**. The current highest is **`0071_seeder_order_repair.sql`**.

Every reserved block in `PLAN-TALLY-PARITY.md` §2.5 — T2's 0040–0041, T3's 0042–0044, T5's
0047–0048, all of Tier 2's 0051–0061, T15's 0065, T16's 0066 — is **BELOW 0071**.

> **Therefore: if T2, T3, T5 or any Tier 2 track needs an ACL rule for a `report:NAME`, its migration
> MUST take a NEW NUMBER ABOVE 0071. Use the reserved block for the TABLES and take a fresh number at
> the end for the seeder restatement. Leave the gap unused.**

This is exactly how the last defect happened: `0063_bank_statement_acl` derived its body correctly
from 0068 and was numbered five *lower*, so 0068's older body overwrote it and **every company on a
fresh database got the imported bank statement handed to its Data Entry level**. Measured on a
scratch DB: 29 report rules, no `report:bank-statement` row.

`backend/migrations/seeder_census_test.go` now guards this **statically, with no database** (the
defect lives in apply order and is invisible on any incrementally-migrated DB — it was green on every
developer machine). **If it fails, do not route around it.**

### 6.2 Does your track even need an ACL migration? Usually NOT.

**A BARE RESOURCE TOKEN NEEDS NO ACL MIGRATION.** Measured on a fresh database: the seeded `Data
Entry` level has `default_access = 'none'` and every rule it carries names a specific token, so a
token never mentioned is denied. T6 shipped `tds-challan` and `tds-profile` with **zero** ACL rows.

**A `report:NAME` DOES need one, mandatorily.** Migration 0011 seeds a coarse `allow display report`
which `internal/authz` matches against *any* `report:NAME` at specificity 1 — so a new report is
**GRANTED to Data Entry the moment it exists**. That is why 0026/0028/0033/0035/0046/0063/0068 exist.

> **Decide the resource SHAPE first: bare token or report name. The answer decides whether you spend a
> migration number at all.**

### 6.3 The registration surface

**A new RESOURCE needs 4** — `routeAction()` · `authz.KnownResources` · (`authz.KnownReportNames` +
`reportName()` **only if it is a report**). Miss one ⇒ **403 FOR THE OWNER TOO**:
`companyAuthMiddleware` returns "unmapped route: access denied" *before* it loads the level, so
`default_access='full'` never applies. The no-DB gate that catches it:
`go test ./internal/server/ -run TestEveryCompanyRouteIsMappedAndInVocabulary`.

⚠️ **Adding a name to `KnownReportNames` AUTO-MOUNTS `GET /print/reports/<name>`.** A name with no
`reportName()` case is a route `routeAction` cannot map — default-DENY for everyone. That shipped once
for `reconciliation-queue`.

⚠️ **A report's route path must be a FLAT LITERAL spelling its own name.** No `{param}`, no nesting
under `/reports/`. Need a selector? **Query parameter** (`stock-item-vouchers` takes
`stock_item_id` that way).

**A new SCREEN needs 13.** The two the file paths lie about:
`ScreenKind` is in `frontend/src/keystroke/types.ts` (**not** `screenRegistry.tsx`), and
`actionLabel()` is in `keystroke/keymaps/tallyprime.ts` (**not** `AppShell.tsx`).

| # | Where | Cost of missing it |
|---|---|---|
| 1 | `keystroke/types.ts` `ScreenKind` | compile |
| 2 | `corpora/validate.py` `KNOWN_SCREENS` | **the ONLY one a gate catches** |
| 3 | `app/defaultScreens.tsx` | screen unreachable |
| 4 | `keystroke/buttonBar.ts` | falls through to `GATEWAY_BAR`, **no test catches it** |
| 5 | `app/AppShell.tsx` `COMPANY_SCOPED_SCREENS` | `useCompanyID()` throws |
| 6 | `keymaps/tallyprime.ts` `screenBindings` | **an unbound key is NOT inert: F5 opens a Payment voucher over your form, F9 a Purchase, F8 Sales** |
| 7 | `keystroke/buttons.test.ts` `SCREEN_PATHS` | — |
| 8 | `keymaps/tallyprime.ts` `actionLabel()` | — |
| **9** | **`app/AppShell.tsx` `SCREEN_ACTIONS`** | ⛔ **the key ships COMPLETELY DEAD. No error, no status line, nothing.** Alt+O shipped this way in a commit with 459 green tests |
| 10 | `keystroke/navMachine.ts` SELECT/ENTER arm, **before** the `canonicalReportName` fallback | a plausible frame that 501s at the print route |
| 11 | `app/ScreenActions.tsx` | dispatcher |
| 12 | `screens/GatewayScreen.tsx` | not listed on the Gateway |
| 13 | `screens/printTarget.test.ts` | print target |

Plus, for a **report** screen only: a classification in `FrameOverlays.tsx`'s
`FULL_PERIOD_REPORTS` / `AS_AT_ONLY_REPORTS` / `NO_DATE_DIMENSION_REPORTS`, asserted by
`periodClassification.test.ts`. A bare `ScreenKind` is exempt (the F2 overlay opens only for report
screens). **A report in none of the three lists is told its period does nothing** — that shipped
twice, to the TDS Register and the Stock Summary, while both read and acted on the dates.

And **client methods in three files** none of the thirteen mentions: `api/client.ts` (the interface —
declaring it there makes `rest.ts` and `mock.ts` fail to compile until both implement it), `api/rest.ts`,
`api/mock.ts`. **The mock is a real deliverable**: `AppShell.test.tsx` renders with
`createMockClient()`, so a screen with no mock method cannot be tested through the real shell at all.

> ### ⛔ AND THE RULE THAT FOLLOWS FROM #9: IF A KEY IS THE FEATURE, THE TEST MUST PRESS THE KEY ON THE
> REAL SHELL. The eleven tests that missed Alt+O called the dispatcher directly, bypassing the map,
> and `buttons.test.ts` asserted the combo *resolves* — which it did. Nothing rendered the real shell
> and pressed the real key. The pattern is in `TDSChallansScreen.test.tsx`'s
> `describe("a screen-owned key is actually ROUTED by the shell")`: render `<AppShell/>`, assert the
> panel is **ABSENT** as a control, `fireEvent.keyDown(app, {key:'c', altKey:true})`, assert it
> appears. Mutation-prove it by deleting the `SCREEN_ACTIONS` entry.

---

## 7. TRAPS — carried forward and new. Do not re-derive these.

### 7.1 Prose rots. Phrase refusals as questions about the books.

**Four times in one feature** a claim outlived its truth: "the Form 140 layout has not been read"
(read two days earlier), "there is no `tds_challan` table" (shipped), "this company has no TAN on
file" (rendered to companies that had one), and a comment asserting a back-dated cutoff that never
ran. Every one was correct when written.

> **The defect is ARITY, not wording.** A refusal that takes no arguments can only make claims about
> THE SOFTWARE, and those go stale silently. A refusal that takes a company and a period can only make
> claims about THOSE BOOKS, and it is re-answered on every request. `ReturnFileRefusal()` was niladic
> and was deleted, not corrected — leaving it would have left a caller able to reach the constant.

### 7.2 A hand-copied string across the Go/TS boundary WILL go stale, and the suite will stay green

`mock.ts` retyped the register's prose with a comment asking the next author to remember. Nobody did,
twice — and because `TDSRegisterScreen.test.tsx` matches strings that live in `mock.ts`, **the whole
frontend suite stayed green against a refusal the product no longer made.**

**The fix, now in place and worth copying:** a Go test
(`internal/reporting/tds_return_status_golden_test.go`) renders the real values from production code
into `frontend/src/api/goldens/tds-register-prose.json`, which the mock **imports**. There is no copy
left to drift. It is deliberately **database-free**, because a golden guarded by a DB connection
silently stops being checked when the DB is down.
Regenerate deliberately: `UPDATE_GOLDENS=1 go test ./internal/reporting/ -run TestReturnFileStatusGolden`.
**Two hand-copied vocabularies remain in `mock.ts`** (the 16 deductor categories, the 3 minor heads) —
verified identical today, unguarded tomorrow. Generating them is a good small task.

### 7.3 The back-dated cutoff is INERT on two routes, and both claim otherwise — UNFIXED, yours to decide

`voucherDateFromRequest` peeks the body for a date. It knows `voucher_date`, `wage_month` (pay run)
and `deposit_date` (challan — fixed this session). It does **not** know:

| Route | Carries | The comment claims |
|---|---|---|
| `POST .../bank-reconciliation` writes | `bank_date` | "a bank date asserted into a closed period is…" |
| `PUT .../deductor-status/{taxYear}` | no date at all | "correctly inherits the back-dated cutoff" |

`cutoffDeny` returns early on a nil date, so on both the cutoff **never evaluates**. Each needs a
product call (is a bank date a back-dated act?), which is why they are handed back rather than
guessed. Copy `peekDepositDate` and `TestAChallanDepositedIntoAClosedPeriodIsRefused`, which carries
a **CONTROL** leg (an ordinary journal must also be refused, or the 403 proves nothing) and an
**ALLOWED** leg (a write after the cutoff must still work, or a fix that refuses everything passes).

### 7.4 Migration 0069's header contradicts the code on part payment — a human must pick a side

0069 says "a part payment is modelled as an allocation smaller than the deduction".
`challanRefusalFor` tests **inequality in either direction**, so a deliberately partial allocation is
indistinguishable from one broken by a voucher ALTER, and the whole quarter refuses with a message
about a change nobody made. This slice refuses partials **at entry** instead. Either correct 0069's
header, or model part payment properly (a distinguishable third state — schema, not Go).

### 7.5 Operational traps that cost this session real time

- **`/healthz` IS NOT A VERSION CHECK.** A cored 1h03m old answered **200** while my replacement died
  with `bind: address already in use` into a log nobody reads. **Poll for a field only the new build
  serves.**
- **Never default a probe you are using to detect staleness.** My `d.get('key', [])` turned a
  *missing* field into "count: 0", which read as "no data" instead of "old binary".
- **Every `cored` restart mints a new ephemeral signing key**, so every saved token dies with it. It
  presents as an unrelated 401.
- **A browser WRITE needs a passkey — and you CAN drive it. Do not disable step-up.** The seeded Owner
  level has `requires_webauthn`, so a write answers 403 `STEPUP_REQUIRED`. Attach a virtual
  authenticator over CDP and the ceremony completes on its own, because `auth/webauthn.ts`'s
  `stepUp()` **enrols this device** when it holds none of the listed credentials:
  ```js
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('WebAuthn.enable', { enableUI: false });
  await cdp.send('WebAuthn.addVirtualAuthenticator', { options: {
    protocol:'ctap2', transport:'usb', hasResidentKey:true,
    hasUserVerification:true, isUserVerified:true, automaticPresenceSimulation:true } });
  ```
  Chrome allows only ONE `internal` authenticator per environment (use `usb` for a second), and the
  authenticator lives on the **CDP session** — add it and drive in the same one.
- **`gofmt -w <dir>` reformats files you did not touch.** Format **named files**.
- **The printed Day Book lists voucher VERSIONS**, so an altered voucher contributes twice. Still an
  open product call — do not "fix" it inside a lane.
- **`posting` is HARD IMMUTABLE and an ALTER re-creates every posting row.** Anything keyed on
  `posting.id` goes silently stale on the first correction. Key on `voucher_id` and **store what you
  matched** so an alter is *detectable* — 0045 and 0069 are the worked precedents.
- **Tolerances are how a real error hides for a year. Reconcile to the paisa.**

### 7.6 Never claim a validation that was not performed

Unreachable from here: the **FVU** (no JRE), the **GSTN portal**, the **NIC e-Way Bill API**, the
**IRP**, the **EPFO/ESIC portals**, the **TIN `.csi`** file. Ship the deterministic fake plus the
recorded schema and **say so on the artefact's own face**. A "generated" return that reads as
"validated" is worse than no return.

---

## 8. WHAT ONLY A HUMAN CAN DO — hand these back, do not simulate them

| # | Ask | Unblocks |
|---|---|---|
| 1 | **A real Tally export** (XML, any version) | **T32 — the commercial blocker.** A business cannot switch if it cannot bring its books. What exists has never met a real file |
| 2 | **A JRE + Protean's RPU zip** | lets us say the TDS return is *validated*, not merely generated |
| 3 | **One real business day on the app** | the highest-value item in the whole plan, and unmeasurable any other way |
| 4 | **IRP / GSTN / e-Way Bill sandbox credentials** (needs a GSTIN) | T5 for real, and T18 — the only thing that ever will |
| 5 | **EPF/EDLI administrative-charge rate notification**, if issued since 2026-07-30 | ~₹500/establishment/month accruing unbooked. **Do NOT substitute `S.O. 2701(E)`** — that is *inspection* charges, a different levy |

**T18 (the Rule 48(4) QR) is permanently unshippable from here.** The payload is signed with the
Income Tax Department's private key; anything we generate tells a scanner the invoice was registered
when it was not. **If a workflow reports T18 done, reject it.**

---

## 9. DEFINITION OF DONE

1. **T2, T3 and T5 all shipped and their FeaturesScreen rows `on`** — Tier 1 complete. A row flips
   only on **browser evidence** (§1.2), not on a green suite.
2. `make verify` exit 0 **and** `pnpm exec tsc --noEmit` clean (§1.1 — they are different gates).
3. backend **≥2126 pass, 0 SKIP, 0 FAIL** (verbose census); frontend **≥507**; `verify:demo` green,
   run ALONE and **re-pinned deliberately if T3 or T9 moves a figure**, with the change explained.
4. Every new tenant table in `buildFixture`; every new package's archtest edge declared **with its
   reason**; every new screen carrying **all 13** registrations including `SCREEN_ACTIONS`, with a
   real-shell keypress test.
5. `PLAN-TALLY-PARITY.md` §2.5 updated for every consumed block — **and §6.1 of this file obeyed** for
   anything touching the ACL seeder.
6. Every safety-critical invariant **mutation-proved**, and said so.
7. A successor handoff. If Tier 1 lands, the next target is **Tier 2 (T8–T14)**, and the one after is
   the thing that actually matters: **the screens for what is already built** — e-Invoice/IRN, payroll
   and offline sync are `server only`, which to a user is indistinguishable from missing.

---

## 10. WHICH HANDOFFS ARE SPENT

**Live:** this file, `PLAN-TALLY-PARITY.md` (§2.5 numbers, §2 working rules), `RESEARCH.md` (Tally
keystrokes, citable by line), `docs/design/*` (per-track models), `docs/statutory/*` (primary sources).

**Spent — do not work from them; they are kept for their reasoning only:**
`HANDOFF.md`, `HANDOFF-TRACKS-2-3.md`, `HANDOFF-T1-PRINTING.md`, `HANDOFF-PARITY-SPRINT.md`,
`HANDOFF-CLOSING-THE-GAP.md`, `HANDOFF-SESSION-T29-IMPORT.md`, `HANDOFF-FEATURE-LOCK.md`,
`HANDOFF-NEXT-THREE.md`, `HANDOFF-TIER-ONE.md`, `HANDOFF-AFTER-T6.md`.

That is **eleven** superseded handoffs. The sprawl is itself a finding: each one re-derived context
the last one had, and `HANDOFF-TIER-ONE.md` carried a blocker that had been dead for two days. **Fold
what is still true into THIS file and delete the rest** — a handoff nobody can trust is worse than no
handoff, because it costs a session to re-verify.

### Left in the dev database on purpose

A company **`T6 Drive Co`** (`9466b85c-…`) with three TDS deductions, two challans (one recorded
through the UI) and a deductor profile, so the browser drive did not pollute the demo. Drop it when
you like. Note also that **the demo company has no TDS data at all** — the seeder creates no TDS
vouchers — so both new T6 screens render empty against `?company=$(cat frontend/.demo-company-id)`.
Seeding one deduction and one challan into `seed:demo` would make T6 demonstrable and is a small,
real, unclaimed piece of work.
