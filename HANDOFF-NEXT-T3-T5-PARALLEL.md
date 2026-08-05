# HANDOFF — NEXT: T3 + T5 IN PARALLEL. T2 IS SHIPPED AND THE CONTRACT T3 NEEDS IS SET.

> ### ⛔ THIS IS THE LIVE HANDOFF. Every other `HANDOFF-*.md` in this repo is SPENT — see §11.
> ### The executable plan is §3. Start with §9 (run-first), then §1, §2, §3.

> **Written 2026-08-05.** Supersedes `HANDOFF-CLEAR-A-TIER.md` as the plan of record.
> `PLAN-TALLY-PARITY.md` §2.5 remains the **only** authority on migration numbers — but read
> **§2 of this file before taking any number**, because two reserved blocks are now spent and one is
> permanently unusable.
>
> **What this session was asked for and what it delivered, plainly.** It was asked to clear Tier 1
> (T2, T3, T5). It delivered **T2** — schema, ledger spine, masters, both reports, screens — plus the
> **shared hashed-payload contract that T3 also needed**. T3 and T5 are **not built**. That was a
> deliberate call taken with the user at the point where the arithmetic became clear (§1), not a
> discovery at the end.
>
> **Read in this order:** §9 (run-first — ten minutes, and it has burned every session that skipped it)
> → §1 (what is true now) → §2 (⛔ migrations: two blocks spent, one poisoned) → **§3 (⛔⛔ THE PARALLEL
> PLAN — the executable part of this file)** → §4 (the T2 gaps that are real) → §5 (T3) → §6 (T5) →
> §7 (traps) → §8 (human-only) → §10 (definition of done).
>
> **§3 IS THE PART YOU ACT ON.** It is a phase-by-phase parallel plan with the lane map, the file
> ownership, a copy-paste workflow script, and an explicit list of what does *not* parallelise. It is
> written to be executed by a clean session with no memory of this one.

---

## 0. THE SCOREBOARD — done, left, and what is merely *reachable*

**Tier 1:** T1 ✅ · T4 ✅ · T6 ✅ · T7 ✅ · **T2 ✅** — remnant **T3, T5**. Tier 1 is **NOT** complete.

| T2 piece | State | Note |
|---|---|---|
| Hashed-payload contract (T2 **and T3**) | ✅ committed, mutation-proved | field order fixed **forever**; T3's half is done |
| Schema + per-category constraint trigger | ✅ 0040, DB-proved from empty, 6 cases w/ controls | parallel allocation accepted; a paisa short refused |
| ACL counterweight | ✅ 0072, fresh-DB census w/ control | 0041 **permanently poisoned** — §2 |
| Ledger spine (validate → payload → rows) | ✅ 5 DB tests, all green | all five failed on FIRST run — §3.4 |
| Masters + both reports (backend) | ✅ | |
| HTTP reachability (`cost_allocations` on the wire) | ✅ **fixed late** | was unreachable from **every** client — §4.0 |
| API client + mock | ✅ | mock now refuses what the server refuses |
| Three screens, 13 registrations each | ✅ 5 mutation proofs | |
| Browser-proved end to end | ✅ through real WebAuthn step-up | axis + centres created via UI; both reports render |
| **Allocation UI on voucher entry** | ⬜ **NOT BUILT** | ⛔ **a keyboard-only operator still cannot allocate.** Largest remaining T2 gap — §4.0 |
| Rename axis / re-parent centre | ⬜ routes exist, no key calls them | §4.3 |
| Allocations in the **edit log** view | ⬜ signed but not shown | §4.3.4 |

| Track | State | First move |
|---|---|---|
| **T3 orders + BOM** | ⬜ nothing exists (`internal/orders` absent, no tables) — **but `CanonicalFulfilment` is committed and frozen** | §3.1 Phase 0, then the 4-lane workflow |
| **T5 e-Way Bill** | ⬜ nothing exists; IRN precedent is the worked example; **citation approach already decided** | §6 — free parallelism, take it |

⚠️ **"Reachable" is not "usable".** T2's allocation write is reachable over HTTP and has no UI. That
distinction is the single most expensive thing this session learned — see §4.0 — and it is why the
FeaturesScreen row leads with what is missing.

---

## 1. WHAT IS TRUE NOW — measured, not asserted

| Gate | Value | Notes |
|---|---|---|
| backend `go test ./... -count=1 -v` | **2220 pass / 0 SKIP / 0 FAIL** | baseline at session start was 2126 |
| frontend `pnpm run test:run` | **597 pass / 46 files** | baseline 507 / 43 |
| `pnpm exec tsc --noEmit` | clean | ⛔ **a separate gate from `make verify`** |
| `make verify` | exit 0 | |
| `verify:demo` | **33/33**, run alone | ⚠️ it needs **insightd on :8088** as well as cored — a killed insightd makes it die with `TypeError: fetch failed`, which looks like a regression and is not |
| `gofmt -l` on every file touched | clean | named files only, never `-w` on a directory |
| Postgres :5433 | live, bracketed before **and** after every suite run | a down DB makes every DB test skip and `go test` still exit 0 |

**Tier 1 status:** T1 ✅ T4 ✅ T6 ✅ T7 ✅ **T2 ✅** — **remnant: T3, T5.** Tier 1 is **not** complete.

### 1.1 The arithmetic, updated — this is the number that should shape the next session

| | Last session | This session |
|---|---|---|
| Tracks delivered | 1 (T6) | 1 (T2) + the shared contract T3 needs |
| Workflows | 4 | **2** |
| Subagent tokens | ~1.7M | ~1.3M |
| Barrier stages | 3 sequential (58 min for ~20 min of work) | **0** — review ran inside each lane |

**Two tracks in two sessions is the observed rate, and the handoff before this one asked for three in
one.** Plan for one track per session and treat anything more as upside. The orchestration changes
*how much of a track* fits, not how many tracks.

---

## 2. ⛔⛔ MIGRATIONS — READ BEFORE TAKING A NUMBER

Consumed this session: **0040** (T2 tables), **0072** (T2 ACL). Highest number in the tree is now
**0072**.

> ### ⛔ 0041 IS PERMANENTLY UNUSABLE AND MUST BE LEFT EMPTY.
> §2.5 reserved it for T2's ACL. It cannot carry an ACL rule and neither can any other reserved block
> below 0072. `seed_company_auth_defaults` is extended only by **restating its whole plpgsql body**,
> `migrations.Apply` runs files in `sort.Strings` order, and **the highest-numbered restatement is the
> only body that survives**. A rule set written at 0041 is overwritten by 0072's body on every fresh
> database and the rules simply do not exist.

**So the rule, restated because a reserved block makes it easy to forget:**
**DERIVE FROM THE HIGHEST-NUMBERED RESTATEMENT (now `0072_cost_report_acl.sql`) AND TAKE A HIGHER
NUMBER THAN IT.** Use the reserved block for TABLES; take a fresh number at the end for the seeder.

Reserved blocks still free **for tables only**: T3's 0042–0044, T5's 0047–0048, Tier 2's 0051–0061.
Every one of them is below 0072, so **none may carry a seeder change.**

`backend/migrations/seeder_census_test.go` guards this statically with no database, because the defect
lives in apply order and is invisible on any incrementally-migrated developer machine. **If it fails,
do not route around it.**

### 2.1 Does your track even need an ACL migration? Usually not.

Unchanged and re-confirmed by measurement this session:

- **A BARE RESOURCE TOKEN NEEDS NO ACL MIGRATION.** The seeded `Data Entry` level has
  `default_access = 'none'` and every rule names a specific token, so a token never mentioned is
  denied. T2's cost masters shipped as the bare token `cost-centre` with **zero** ACL rows, exactly as
  T6 shipped `tds-challan` and `tds-profile`.
- **A `report:NAME` DOES need one, mandatorily.** Migration 0011 seeds a coarse `allow display report`
  which `internal/authz` matches against *any* `report:NAME` at specificity 1 — so a new report is
  **GRANTED to Data Entry the moment it exists**. 0072 is the T2 counterweight and writes **both**
  `disallow display` and `disallow print` per name: a report that cannot be displayed but *can* be
  printed is a leak with a paper trail.

**Decide the resource SHAPE first.** It decides whether you spend a migration number at all.

### 2.2 How to verify an ACL migration, since a green suite proves nothing here

Do all three. The second one is the one that catches the real defect:

1. `go test ./migrations/` — the static census.
2. **Diff the restated body against its base with comments stripped.** For 0072 that yielded exactly
   one line of difference and a PERFORM count of 11 → 12. Anything else means a predecessor's rule set
   was dropped.
3. **Migrate a scratch database from empty, seed one company through the real
   `seed_company_auth_defaults`, and COUNT the rules** — including a **control** row from an earlier
   migration to prove your restatement did not drop it. (0072's control was
   `report:bank-statement`, the line 0068 once dropped: still present, 2 rows.)

---

## 3. ⛔⛔ THE PARALLEL PLAN FOR T3 + T5 — COPY THIS, DO NOT RE-DERIVE IT

This section is the executable part of the handoff. It is written for a **clean session** and it
assumes you have read §1 and §2 and nothing else yet.

> ### THE ONE COUNTER-INTUITIVE THING, AND IT IS THE WHOLE LESSON
> **The speedup did not come from more agents. It came from removing barriers and front-loading the
> shared contracts.** Last session ran 4 workflows with 3 sequential stages and delivered one track.
> This session ran 2 workflows with ZERO barrier stages and delivered one track plus a shared contract,
> on ~25% fewer subagent tokens. The agent count was the same.
>
> **PHASE 0 BELOW IS NOT OVERHEAD — IT IS WHAT BUYS THE PARALLELISM.** Every hour spent there converts
> a sequential dependency into a disjoint lane. Skip it and the lanes collide, the manifests duplicate,
> and you are back to a barrier chain.

### 3.0 THE MEASURED CEILING — do not plan past it

| Constraint | Value | Consequence |
|---|---|---|
| Concurrent agents per workflow | `min(16, cores-2)` = **6** | asking for 20 does not give you 20; excess queues |
| Workflows at once | **ONE** | 3 concurrent workflows = up to 18 agents each spawning a compiler; that, not the work, turns a track into an afternoon |
| Postgres | **ONE instance on :5433, shared by every worktree** | ⛔ lanes may NOT run DB-backed tests; concurrent `migrations.Apply` races; the residue check counts rows globally |
| Heavy commands | auto-queued through `ci-slot` (2 slots) by a PreToolUse hook | **do not wrap anything yourself**; a command that pauses is waiting, not hung. Another project's worktree competed for slots throughout this session |

So the real parallelism is **~6 agents on disjoint files, one workflow at a time**, and the serial
phases below are not compressible.

### 3.1 PHASE 0 — SERIAL, YOURS, ~1–2 HOURS. NOTHING PARALLELISES UNTIL THIS EXISTS.

Every item here is a file that **two or more lanes would otherwise both edit**. Write them yourself,
commit them, and the lanes become genuinely disjoint.

| # | What | Why it cannot be a lane |
|---|---|---|
| 1 | **Migrations 0042 (orders) + 0043 (BOM)**, DB-proved from empty with controls | Two lanes writing migrations collide on numbers, and the seeder trap (§2) needs one author. Lanes get the `CREATE TABLE` **quoted verbatim** — a paraphrase is what made an agent invent a third copy of a vocabulary |
| 2 | **`VoucherInput.Fulfilments` + `post()` payload assembly + `order_fulfilment` row writes** | This is `internal/ledger/ledger.go`, the single most contended file. `CanonicalFulfilment` is already committed and frozen — you are only wiring it |
| 3 | ⛔ **`voucherReq.fulfilments` on the HTTP wire + `buildVoucherInput` translation** | **THE REACHABILITY LESSON (§4.0).** Do this in Phase 0, not as a lane, because it is precisely the step that was forgotten for cost allocations — and forgetting it makes every downstream lane build against a feature no client can reach |
| 4 | **TS API types** for orders, BOM and e-Way Bill in `frontend/src/api/types.ts` | The screens workflow needs them; writing them first is what let this session's screens lane start immediately instead of waiting on an API phase |
| 5 | **The ACL SHAPE decision** (§2.1), and migration **0073** only if a `report:NAME` is involved | Three lanes each restating the seeder is the §3.3 duplicate disaster at schema level |

**Then commit Phase 0 on its own.** Each lane prompt quotes the committed DDL and the committed types.

### 3.2 PHASE 1 — ONE WORKFLOW, FOUR LANES, EACH `implement → adversarially verify`

Ownership is **new files in disjoint packages**, which is what makes this safe. Two lanes writing
different files in the *same* Go package is fine; two lanes writing the *same file* is not.

| Lane | Owns (creates only these) | Shared files |
|---|---|---|
| **T3-orders** | `internal/orders/order.go` + `_test` + `_db_test`, `internal/server/handler_orders.go` | manifest only |
| **T3-bom** | `internal/orders/bom.go` + `_test` + `_db_test`, `internal/server/handler_bom.go` | manifest only |
| **T3-reports** | `internal/reporting/orderbook.go` + `_test` + `_db_test`, `internal/server/handler_orderbook.go` | manifest only |
| **T5-eway** | `internal/statutory/ewaybill.go`, its schema JSON, `ewaybill_test.go` | **none** |

> **T5 IS FREE PARALLELISM — TAKE IT.** `internal/archtest/boundaries_test.go` declares
> `"internal/statutory": {}` — an EMPTY allowed-import set, so the package may import nothing from
> `internal/` at all. It is a separate deployable that talks to cored only over HTTP. It therefore
> cannot collide with any T3 lane by construction, and it needs no registration manifest. Run it
> alongside whatever else is going.

4 lanes × 2 agents = 8, against a cap of 6 — the last two queue, which `pipeline()` handles. **Do not
add a fifth lane to "use spare capacity": the cap is 6 and the database is shared.** Do not add a
synthesis agent at the end either; you are the synthesis, and you hold context the agents do not.

```js
export const meta = {
  name: 't3-t5-backend',
  description: 'T3 orders + BOM + order book, and T5 e-Way Bill — each implemented then adversarially verified in its own lane',
  phases: [{ title: 'Lanes', detail: 'four tracks, each implement -> verify, no barrier between lanes' }],
}

const LANES = [
  { key: 'T5', label: 'eway-bill',  prompt: T5_PROMPT },  // fully disjoint — starts immediately
  { key: 'ORD', label: 'orders',    prompt: ORDERS_PROMPT },
  { key: 'BOM', label: 'bom',       prompt: BOM_PROMPT },
  { key: 'RPT', label: 'order-book', prompt: REPORTS_PROMPT },
]

// pipeline(), NOT parallel(): T5's verifier runs WHILE the reports lane is still
// implementing. A trailing review phase is a barrier across every lane at once, and
// that cost the session before this one 58 minutes for ~20 minutes of parallel work.
const results = await pipeline(
  LANES,
  (lane) => agent(CONTRACT + lane.prompt,
    { label: `impl:${lane.label}`, phase: 'Lanes', schema: MANIFEST }),
  (built, lane) => built ? agent(REVIEW_PREAMBLE + reviewTask(lane, built),
    { label: `verify:${lane.label}`, phase: 'Lanes', schema: REVIEW, effort: 'high' })
      .then((v) => ({ lane: lane.key, built, verdict: v })) : null,
)
return results.filter(Boolean)
```

### 3.3 WHAT EVERY LANE PROMPT MUST CARRY — all six, every time

Copy this list. Each item is here because omitting it cost something real:

1. **The domain contract, QUOTED VERBATIM** — the migration's `CREATE TABLE` with its CHECKs. Agents
   given the constraints verbatim produced correct validation; the one given a paraphrase invented a
   third copy of a vocabulary.
2. **The precedent file to copy, BY PATH.** "Model it on `internal/ledger/cost_allocation.go`" produced
   structurally correct code first time.
3. **The decisions ALREADY MADE, as orders, with reasons.** Every decision you leave open comes back as
   a plausible wrong answer you then have to argue with.
4. **The forbidden-files list and the manifest schema** (§3.5).
5. **The traps, each WITH ITS COST.** "A missing `SCREEN_ACTIONS` entry ships the key DEAD with 459
   green tests" gets obeyed; "be careful with registrations" does not.
6. **What they may run**: `go build ./...`, `go vet ./<pkg>`, `gofmt -l <NAMED FILES>`, their own no-DB
   tests, and `go test ./internal/server/ -run TestEveryCompanyRouteIsMappedAndInVocabulary`.
   ⛔ **Explicitly: NO DB-BACKED TESTS.** They must WRITE them (a required deliverable) and NOT run
   them, and name them in the report so you can run them serially.

⛔ **AND ADD THIS, WHICH IS NEW AND IS THE MOST EXPENSIVE OMISSION OF THE LAST TWO SESSIONS:**

> **CAN A CLIENT REACH WHAT YOU BUILT?** Name, in your report, the exact HTTP request that exercises
> your feature end to end. If no wire field carries your input, say so as a BLOCKER rather than
> assuming someone else added it. T2's entire write path was built, reviewed and covered by unit AND
> live-Postgres tests while being reachable only from Go — because `voucherReq` had no field for it.
> Nothing failed. See §4.0.

### 3.4 PHASE 2 — SERIAL INTEGRATION. BUDGET FOR IT; IT IS WHERE THE DEFECTS ARE.

In this order:

1. **Apply the manifests**, re-verifying anchor uniqueness **per entry AT APPLY TIME** — an earlier
   insert can introduce a second occurrence of a later anchor. ⚠️ **A duplicate chi registration is
   SILENT** (`node.setEndpoint` overwrites, no panic), so this assertion is the only guard.
2. **Run the no-DB registration gate**: `go test ./internal/server/ -run TestEveryCompanyRouteIsMappedAndInVocabulary`.
3. ⛔ **Run every DB-backed test the lanes wrote.** *All five* of this session's ledger DB tests failed
   on first execution — handed over "green" having never run, every one dying in setup because
   migration 0034 already seeds a `Salaries & Wages` ledger per company. **A lane's green is a CLAIM
   about untested code.**
4. **Triage the review findings.** 28 came back across two workflows this session (1 blocker, 10
   serious) and **none was visible to a green suite**. Fix correctness and data-loss first, prose rot
   second, polish last — and write down what you did not fix.
5. **Full suite, bracketed with a live `psql` before AND after.** A down DB makes every DB test skip
   and `go test` still exit 0.
6. **Commit per lane**, staging explicit paths, grepping the staged list for `zz_ scratch probe .bak`.

### 3.5 The manifest protocol — and when NOT to use it

Lane agents must not edit shared registration files. They return a manifest; **you** apply it:

```js
entries: [{ file, anchor /* verbatim, UNIQUE in the file */, placement: "before"|"after",
            insert, why }]
```

```python
# Re-verify uniqueness AT APPLY TIME, per entry.
assert s.count(anchor) == 1
```

**Tell each lane explicitly which registrations are NOT its own.** That instruction is why this
session got 6 clean entries where the one before got 12 for 6 registrations, one naming a handler that
did not exist.

> ### ⛔ BUT PREFER ONE LANE PER SHARED-FILE CLUSTER OVER TWO LANES PLUS A MANIFEST.
> The screens workflow was a SINGLE lane, so it owned all 13 registration files directly and there was
> no manifest to apply at all — removing an entire integration step. Use a manifest only when two lanes
> genuinely must touch one file. **If two lanes would fight over a file, that is a signal to
> restructure the lanes, not to add coordination.**

### 3.6 PHASE 3 — THE SCREENS WORKFLOW, and why it is ONE lane

All 13 registration sites plus `FrameOverlays` are shared by every screen, so **one lane owns them
all**. Give it the committed TS types, the 13-site table from §9.2, and the mutation-proof requirement.
Then **you** drive the browser (§7.5) — that is not delegable, and it is where this session found four
defects that 597 green tests could not see.

### 3.7 What does NOT parallelise, stated so nobody tries

- **Phase 0.** It is the shared contract; two authors means two contracts.
- **Migrations.** One author, or you get number collisions and the seeder-order trap.
- **The integration pass.** It is serial by definition and it is where the integrator earns their keep.
- **The browser drive.** One session, one authenticator, one set of eyes.
- **`verify:demo`.** Run it **ALONE** — and start insightd on :8088 first (§1).

## 4. ⛔⛔ T2 — WHAT SHIPPED, AND THE GAPS THAT ARE REAL

### 4.0 THE BIG ONE, STATED FIRST: THERE IS NO ALLOCATION UI ON VOUCHER ENTRY

**A human cannot allocate a cost from inside this product.** The masters screen creates axes and
centres; the two reports read allocations; **the allocation itself is API-only.** Tally puts
allocation on the voucher form as a sub-screen when the ledger has cost centres applicable, and that
is not built.

> ⛔ **AND IT WAS WORSE THAN THAT UNTIL LATE IN THE SESSION: THE FEATURE WAS UNREACHABLE FROM EVERY
> CLIENT.** `voucherReq` had no `cost_allocations` field, so no HTTP body could carry one — the
> validation, the hashed payload entry and the row insert were reachable ONLY from Go. Nothing failed:
> both report routes answered 200 and were *correct*, reporting accurately that nothing had been
> allocated because nothing could be. Fixed, and pinned by
> `internal/server/cost_allocation_http_test.go`, which POSTs JSON and reads the figures back out of
> the **report** over HTTP so the assertion spans wire → ledger → payload → table → report → wire.
>
> **The lesson generalises and is the most valuable thing in this file:** ask §5.1's question at
> FEATURE level, not form level — not "can a human obtain every value this form demands" but **"can a
> human reach this feature at all"**. `voucherReq.ItemLedgerID` carries a comment describing this exact
> defect from an earlier feature. It recurred one feature later, and NEITHER TIME did any test notice,
> because both times the layer under test was fine.

Apply §5.1's question at feature level — *can a human obtain every value it demands, from inside this
product?* — and the answer for the allocation path is **no**. That is why the FeaturesScreen note says
so in its own words rather than claiming a working feature. **This is the single highest-value next
piece of T2 work and it is small compared with what already exists**: the write path, the validation,
the hashed payload and both reports are done and tested.

⚠️ **And it is harder than it looks in one specific way.** `posting_seq` is a 0-based ordinal **no
screen displays**, and an item invoice **refuses allocations outright** because its postings are
server-generated and index 0 is the *party* leg. So the allocation UI must attach to a posting **row
the operator is already looking at** on a class that states its own postings — never ask for a number.
`CostAllocationInput` in `frontend/src/api/types.ts` carries this warning; read it first.

### 4.1 What is genuinely done and proved

- **Migration 0040** — `cost_category`, `cost_centre`, `posting_cost_allocation`, and the
  **per-(posting, category) deferred constraint trigger**. Proved on a from-empty database with six
  cases *and controls*: parallel allocation across two axes accepted; a short axis refused naming the
  axis and the gap in rupees; one paisa short refused; a centre claimed on the wrong axis refused by
  the composite FK; and the two controls (full allocation accepted, unallocated postings still legal).
- **The hashed payload contract** — `CanonicalCostAllocation` and `CanonicalFulfilment`, LAST and in
  that fixed order, both `omitempty`, all-string fields. **Mutation-proved:** swapping the two fails
  exactly one test and **nothing else in the package**, which is precisely why that test had to exist.
- **The ledger spine** — `VoucherInput.CostAllocations`, per-category validation, payload assembly,
  row inserts in one transaction, ALTER re-states, DELETE carries none.
- **Both reports** — Cost Centre Summary (one axis, nested roll-up, `unattached` for centres the walk
  could not place) and Cost Category Summary (one row per axis **and the gap**, with no cross-axis
  total and `notes` saying why).
- **Migration 0072** — the ACL counterweight, verified on a fresh database with a control.
- **Three screens**, all 13 registrations each, with **five mutation proofs** (SCREEN_ACTIONS for two
  keys, the buttonBar fall-through that no pre-existing test catches, the FrameOverlays period
  classification, and the Gateway Enter branch).
- **BROWSER-PROVED end to end** on the demo company: the axis created through the real WebAuthn
  step-up (403 `STEPUP_REQUIRED` → ceremony → 201, row confirmed in Postgres), two centres created with
  the axis PICKED from a served list, and both reports rendering the allocation — Cost Centre Summary
  `Admin 40,000.00 / Sales 60,000.00`, axis total `1,00,000.00`, residual `0.00`; Cost Category Summary
  `allocable 2,15,600.00 / allocated 1,00,000.00 / UNALLOCATED 1,15,600.00 / 1 of 10 postings`, with no
  cross-axis total and the reason printed on the artefact.

### 4.2 ⛔ THE MODEL, in one paragraph, because three tracks inherit it

A **category is an AXIS** (Department, Branch, Project); a **centre is a POSITION** on it. Several axes
allocate the **same money in parallel**: one ₹1,00,000 posting is allocated in full across Departments
*and again in full* across Branches. Both are complete and correct. Therefore **the sum-to-posting
invariant is per (posting, category), never per posting**, and **anything that totals across axes
double-counts** — for two axes it is 2× the cost borne. T11 (budgets) and T24 inherit this shape.

### 4.3 Known, recorded, NOT fixed — each is small and each is real

1. **The pre-flight sum refusal names the axis by raw UUID and the ledger by ordinal**, which is
   strictly *less* usable than the DB trigger message it exists to pre-empt (that one names both in
   words). The trigger backstops it, so this is usability, not correctness. Fix: resolve names at the
   in-transaction call site, which already holds the tx.
2. **The Go layer declines the cross-axis centre check**, so the most likely operator error — picking
   a centre on the wrong axis — surfaces as a Postgres constraint name after a rollback.
3. **The two reports disagree about an off-axis allocation.** The category summary excludes it (and
   reports it separately); the centre summary silently includes it. Neither says so.
4. **`VersionView` does not project allocations**, so the edit log does not *show* what it *signs*: an
   auditor opening an altered pay run sees two versions that render identically. The comment in
   `ledger.go` now states this as the gap it is rather than borrowing the "shows what it signs"
   formula. Projecting them is the fix.
5. **The demo company has no cost data** and nothing seeds any, so both report screens render empty
   against `?company=$(cat frontend/.demo-company-id)`. Seeding one axis, two centres and one allocated
   voucher into `seed:demo` would make T2 demonstrable — the same unclaimed work T6 left behind.
6. **The audit actor is CLIENT-SUPPLIED.** `created_by` comes from the request body, so a client
   chooses what the audit trail records. The rest of this codebase does the same (`created_by: "ui"`
   in the bank screens) so T2 matches the house pattern rather than fixing it — but the server holds
   the authenticated subject and ought to be the one deciding. Noted, not endorsed.
7. **`GetVoucher` now projects allocations, but the HTTP voucher GET does not.** The domain-layer
   data-loss path is closed (`VoucherView.CostAllocations`); whether the HTTP voucher GET/PUT carries
   them was **not** re-checked, so a client that rebuilds a VoucherInput from the HTTP GET may still
   clear a split on an unrelated edit. **Check this before building the allocation UI** — it is the
   same class of defect as §4.0 and the same size.
8. ⚠️ **THE MOCK MUST REFUSE WHAT THE SERVER REFUSES, and twice this session it did not** — which is
   why 597 green tests sat on top of a create path that could not work in a browser. Once for a
   missing `created_by` (server 400, mock 201) and once for a response SHAPE (the server nests the
   centre view under `cost_centre` and may send it null; the mock returned it flat, and `rest.ts` typed
   the envelope AS the flat view — a lie the compiler cannot catch, so every field read `undefined` at
   runtime while the code type-checked). **When you add a mock method, diff its refusals AND its shape
   against the handler, not against the type you wish existed.**

### 4.4 `verify:demo` was NOT re-run

T2 adds no voucher and moves no figure the demo asserts, so it should be unaffected — but that is
reasoning, not measurement, and §5 is explicit that reasoning is not evidence. **Run it alone**
(`pnpm verify:demo $(cat frontend/.demo-company-id)`) and re-pin deliberately if anything moved.

---

## 5. T3 — ORDERS + BOM. The hard part is already done.

**⛔⛔ AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT.** Push it through `stock_movement` and closing
stock — and therefore the Balance Sheet — is corrupted. **Partial fulfilment is the normal case**: an
order is consumed progressively by delivery notes and invoices, and the outstanding balance is the
entire point.

**What this session already did for T3, so do not redo it:**

`CanonicalFulfilment` is **committed, frozen and in the hashed payload**, declared LAST after
`CostAllocations`:

```go
type CanonicalFulfilment struct {
    LineNo      string `json:"line_no"`        // this voucher's 1-based inventory line
    OrderLineID string `json:"order_line_id"`  // the order LINE, not the order
    Qty         string `json:"qty"`            // fixed-4; how much of the ORDER was consumed
}
```

**Why it is hashed** (decided, with reasons, so it is not re-litigated): it is **operator-keyed
input** — someone chose at entry which order this shipment satisfies — which places it with
`inward_doc` and **not** with `bank_reconciliation`. 0045 and 0069 key on `voucher_id` and store what
they matched *because* they are metadata added later by a different act, which an ALTER can leave stale
without ever seeing it. A fulfilment cannot go stale: an ALTER re-states it in the same transaction, so
the outstanding balance recomputes from the current version with nothing to detect and nothing to
repair.

**It records a LINK, never a stock position.** Its `qty` is how much of an *order* was consumed;
`CanonicalStockLine.qty` is how much *stock* moved. Closing stock reads the movement, the order book
reads this, and neither reads the other.

**Nothing populates it yet.** `VoucherInput` has no `Fulfilments` field and `post()` never sets
`payload.Fulfilments`. That is the T3 spine and it is the same shape as the cost spine that landed this
session — read `internal/ledger/cost_allocation.go` and follow it.

**Migrations:** 0042 (orders), 0043 (BOM) for **tables**. If T3 wants `report:order-book` it needs an
ACL counterweight **above 0072** (§2) — and decide the shape first: an order book is the forward sales
pipeline, which is at least as sensitive as the tds-register's counterparty list.

---

## 6. T5 — e-WAY BILL. Its citation problem is decided; implement the decision.

**Reuse the IRN precedent in `internal/statutory` wholesale.** ⚠️ Note the package is a **separate
deployable** and **must not import `internal/ledger` or `internal/server`** — its own doc comment says
so and an archtest enforces it. That makes T5 genuinely disjoint from everything else, which is why it
can run alongside any other lane.

### 6.1 ⛔ THERE IS NO PRIMARY SOURCE FOR RULE 138 IN THIS REPO, AND CBIC IS UNREACHABLE

`docs/statutory/` holds **only Form 140 (TDS) material**. `RESEARCH.md` mentions e-way bills solely in
market-analysis prose — no rule citation, no threshold, no validity table. An attempt to fetch the
official CBIC text on 2026-08-05 **failed TLS chain verification** from this environment
(`taxinformation.cbic.gov.in`, both the HTML and the PDF endpoints).

§5 says *cite it or refuse*, and the handoff's own §4.3 asserted the numbers — which is exactly the
prose-rot shape §7.1 warns about.

> **THE DECISION, TAKEN WITH THE USER THIS SESSION:** put the figures in **ONE** Go location,
> annotated as **secondary material** with the source URLs and the recorded TLS failure, pending
> re-verification — and **deliberately NOT in a migration CHECK**, so re-verification is a code change
> rather than a schema change. This is the repo's own precedent: `CanonicalInward.ITCBlockedClause` is
> unconstrained in both Go and the schema for exactly this reason, and says so.

The figures corroborated across secondary sources (Rule 138(1) ₹50,000 consignment value; Part B not
required under 50 km within the State; Rule 138(10) validity of one day per 200 km, and one day per
20 km for over-dimensional cargo) are **NOT verified against the bare Rule** and must carry that
caveat in the code. Sources: [CBIC Rule 138](https://taxinformation.cbic.gov.in/content/html/tax_repository/gst/rules/cgst_rules/active/chapter16/rule138_v1.00.html) (unreachable from here),
[ClearTax](https://cleartax.in/s/eway-bill-gst-rules-compliance), [Taxmann](https://www.taxmann.com/post/blog/process-of-generating-e-way-bill).

**Getting the published text into `docs/statutory/` — as `form-140.pdf` already is — remains the
better answer and is a human-only ask (§8).**

### 6.2 And say so on the artefact's own face

NIC is unreachable, so ship the deterministic fake plus the recorded schema and **print the gap on the
document**. `invQRNotPrintedNotice` in `internal/printing/invoice.go` is the template; note its
conditioning rule — the notice fires on the artefact being the KIND of thing the gap applies to, not as
a global constant. **Do not claim integration.**

---

## 7. TRAPS — carried forward, and the new ones

### 7.1 Prose rots. Two more claims died this session.

Both were correct when written and false when found:

- *"a voucher number allocated and then rolled back is a gap in a statutory series"* — **false.** There
  is no sequence in this schema (no `nextval` in any migration); `voucher_number_counter` is a table row
  advanced by `INSERT … ON CONFLICT DO UPDATE` **on the same transaction**, so a rollback gives the
  number back. It survived because it is what a Postgres SEQUENCE *would* do.
- *"what the edit log SHOWS must be what the edit log SIGNS"* — true of postings and stock lines, **not**
  of cost allocations (§4.3.4). Borrowing a true sentence into a place where it is false is the same
  defect as writing a false one.

**Correct them rather than delete them: the false version is the plausible one and will be
re-invented.** And prefer a refusal that takes a company and a period over one that takes no
arguments — the latter can only make claims about the software, and those go stale silently.

### 7.2 A test that CANNOT FAIL is worse than no test

A guard forbidding a money field on `CostCentreRecord` asked whether it satisfied
`interface{ Total() any }` — a signature nothing would ever declare, so the branch was unreachable and
the test passed unconditionally **while reading as a guard**. The reviewer found it by *adding the very
field it forbade* and watching it stay green.

**Mutation-prove every guard, especially the ones that assert a negative.** A negative assertion has no
natural failing case, so nothing else will ever tell you it is inert.

### 7.3 The all-zeros UUID parses, and `uuid.Nil` is everyone's sentinel

`uuid.Parse("00000000-0000-0000-0000-000000000000")` **succeeds**. Two handlers treated that as their
"absent" sentinel, so an explicit all-zeros `parent_id` silently created a top-level centre or **lifted
an existing child out of its hierarchy** (changing which totals it rolls into, with a 200 and a
truthful read-back), and an all-zeros filter silently returned **every axis**.

**Any handler whose "absent" sentinel is `uuid.Nil` must refuse an explicit zero UUID.** Grep for
`uuid.Nil` comparisons after a `uuid.Parse` and check each one.

### 7.4 A differential test needs a CONTROL, and this session wasted two probes proving nothing

The first attempt at the "short allocation is refused" probe tripped the **uniqueness** constraint on a
previous case's leftover rows and never reached the invariant under test. Both cases "passed" — as
refusals for the wrong reason. Redone on a clean posting with a control leg proving a correct
allocation commits.

**A refusal that fires for the wrong reason is worse than no test**, because it reads as coverage.

### 7.5 Operational traps that still cost real time

- **A browser WRITE needs a passkey, and you CAN drive it. Do not disable step-up.** The seeded Owner
  level has `requires_webauthn`, so any write answers 403 `STEPUP_REQUIRED` — **including from `curl`
  with a valid dev token**, which is how this session discovered it. Attach a virtual authenticator
  over CDP and the ceremony completes on its own, because `auth/webauthn.ts`'s `stepUp()` **enrols this
  device** when it holds none of the listed credentials:
  ```js
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('WebAuthn.enable', { enableUI: false });
  await cdp.send('WebAuthn.addVirtualAuthenticator', { options: {
    protocol:'ctap2', transport:'usb', hasResidentKey:true,
    hasUserVerification:true, isUserVerified:true, automaticPresenceSimulation:true } });
  ```
  Chrome allows only ONE `internal` authenticator per environment (use `usb` for a second), and the
  authenticator lives on the **CDP session** — add it and drive in the same one. **Verified working
  this session.**
- **`/healthz` IS NOT A VERSION CHECK.** Poll for something only the new build serves. This session
  probed `GET …/cost-categories` and read **401** (auth required) rather than **404** (route absent) —
  that distinction is the version check.
- **Launch servers detached and RETURN from the command.** `(nohup … &)` inside a command that then
  blocks dies with your own timeout, taking the server with it. `nohup … & disown` then poll.
- **Every `cored` restart mints a new ephemeral signing key**, so every saved token dies with it. It
  presents as an unrelated 401.
- **`gofmt -w <dir>` reformats files you did not touch. Format NAMED FILES.** Pre-existing drift in
  `closing_stock.go` / `closing_stock_statement_test.go` is **not yours**.
- **NEVER `git add -A`.** Stage explicit paths and grep the staged list for `zz_`, `scratch`, `probe`,
  `.bak`.
- **`make verify` DOES NOT RUN `tsc`.** They are different gates. Folding `tsc` into `make verify` is a
  ~10-minute job and is still the cheapest real improvement on this list.
- **The machine is shared.** `ci-slot` (2 slots) queues heavy commands via a PreToolUse hook — do not
  wrap anything yourself; a command that pauses is waiting, not hung. Another project's worktree was
  competing for slots throughout this session.
- **`posting` is HARD IMMUTABLE and an ALTER re-creates every posting row.** Anything keyed on
  `posting.id` from *outside* the voucher path goes silently stale. `posting_cost_allocation` may key on
  it because `post()` writes it in the same transaction as the postings; 0045 and 0069 may not, and
  store what they matched instead. **Know which case you are in.**
- **`posting` now has `UNIQUE (company_id, id)`** (added by 0040) so tenant tables can FK to it
  company-scoped. A single-column FK to `posting(id)` would let one company's row point at another's.
- **Tolerances are how a real error hides for a year. Reconcile to the paisa.**

### 7.6 Never claim a validation that was not performed

Unreachable from here: the **FVU** (no JRE), the **GSTN portal**, the **NIC e-Way Bill API**, the
**IRP**, the **EPFO/ESIC portals**, the **TIN `.csi`** file — and, as of this session, **CBIC's own rule
repository** (TLS failure). Ship the deterministic fake plus the recorded schema and say so on the
artefact's face.

---

## 8. WHAT ONLY A HUMAN CAN DO

| # | Ask | Unblocks |
|---|---|---|
| 1 | **A real Tally export** (XML, any version) | **T32 — the commercial blocker.** A business cannot switch if it cannot bring its books. What exists has never met a real file |
| 2 | **A JRE + Protean's RPU zip** | lets us say the TDS return is *validated*, not merely generated |
| 3 | **One real business day on the app** | the highest-value item in the whole plan, and unmeasurable any other way |
| 4 | **IRP / GSTN / e-Way Bill sandbox credentials** (needs a GSTIN) | T5 for real, and T18 — the only thing that ever will |
| 5 | **EPF/EDLI administrative-charge rate notification**, if issued since 2026-07-30 | ~₹500/establishment/month accruing unbooked. **Do NOT substitute `S.O. 2701(E)`** — that is *inspection* charges, a different levy |
| 6 | **The published text of CGST Rule 138 and 138(10)** into `docs/statutory/` | lets T5's threshold and validity table be **cited** rather than carried as secondary material (§6.1). CBIC fails TLS verification from this environment |
| 7 | **Is a cost report something a data-entry clerk should see?** 0072 says no, deliberately | it is a one-line ACL decision either way, and it is a business call, not a technical one |

**T18 (the Rule 48(4) QR) is permanently unshippable from here.** The payload is signed with the Income
Tax Department's private key; anything we generate tells a scanner the invoice was registered when it
was not. **If a workflow reports T18 done, reject it.**

---

## 9. RUN-FIRST CHECKLIST — ten minutes, and it has burned every session that skipped it

```bash
# 1. Tree clean? Commit or stash BEFORE fanning out.
git status --porcelain

# 2. Postgres. A TCP connect is NOT liveness (docker-proxy holds the port past a dead backend).
docker ps | grep tally_pg
pg_isready -h localhost -p 5433 -t 5 && \
  PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"

# 3. Kill stale servers BY PID. `pkill -f "cmd/cored serve"` misses the compiled child.
lsof -nP -iTCP:8087 -sTCP:LISTEN     # -> kill -9 that PID
ps -eo pid,etime,command | grep "cored serve" | grep -v grep   # etime is AGE
lsof -nP -iTCP:5173 -sTCP:LISTEN     # a stale vite serves day-old code just as happily

# 4. Baseline, and WRITE IT DOWN. Without -v the SKIP census is a lie.
cd backend && go test ./... -count=1 -v 2>&1 | tee /tmp/base.log | tail -3
grep -c -- '--- PASS' /tmp/base.log; grep -c -- '--- SKIP' /tmp/base.log   # SKIP must be 0
cd ../frontend && pnpm exec tsc --noEmit && pnpm run test:run
cd .. && make verify
```

### 9.1 Driving the app

```bash
make run-cored      # :8087, sets PORT + AUTH_DEV_TOKEN_ENABLED
make run-insightd   # :8088
cd frontend && pnpm dev   # :5173
open "http://localhost:5173/?company=$(cat frontend/.demo-company-id)"
```
Sign in `browser-dev-owner` / `tally-demo-2026`. **It is a keystroke app: a click SELECTS, `Enter`
OPENS.** A write needs the virtual authenticator — §7.5.

### 9.2 The registration surface, unchanged

**A new RESOURCE needs 4** — `routeAction()` · `authz.KnownResources` · (`authz.KnownReportNames` +
`reportName()` **only if it is a report**). Miss one ⇒ **403 FOR THE OWNER TOO**. Gate:
`go test ./internal/server/ -run TestEveryCompanyRouteIsMappedAndInVocabulary`.

⚠️ Adding a name to `KnownReportNames` **AUTO-MOUNTS** `GET /print/reports/<name>`; a name with no
`reportName()` case is default-DENY for everyone. ⚠️ A report's route path must be a **FLAT LITERAL**
spelling its own name — need a selector? **Query parameter.**

⚠️ **Two count assertions move whenever a report name is added**, and both are deliberate tripwires:
`handler_print_reports.go`'s deferred-set header + its count in `handler_print_test.go`, and
`printDisallowedBySeededMigrations`. They are written out rather than read from the migrations so an
addition fails **loudly**. Update the prose, not just the number.

**A new SCREEN needs 13**, and the two the file paths lie about: `ScreenKind` is in
`frontend/src/keystroke/types.ts` (**not** `screenRegistry.tsx`), and `actionLabel()` is in
`keystroke/keymaps/tallyprime.ts` (**not** `AppShell.tsx`). ⛔ **`AppShell.tsx`'s `SCREEN_ACTIONS` is
the one that ships the key COMPLETELY DEAD** — no error, no status line. ⛔ **An unbound key is NOT
inert**: F5 opens a Payment voucher over your form, F9 a Purchase, F8 Sales.

Plus, for a **report** screen only: a classification in `FrameOverlays.tsx`'s `FULL_PERIOD_REPORTS` /
`AS_AT_ONLY_REPORTS` / `NO_DATE_DIMENSION_REPORTS`. **A report in none of the three is told its period
does nothing** — that shipped twice. A bare `ScreenKind` is exempt.

And **client methods in three files**: `api/client.ts` (declaring it there makes `rest.ts` and
`mock.ts` fail to compile until both implement it — use that), `api/rest.ts`, `api/mock.ts`. **The mock
is a real deliverable**: `AppShell.test.tsx` renders with `createMockClient()`.

> ### ⛔ IF A KEY IS THE FEATURE, THE TEST MUST PRESS THE KEY ON THE REAL SHELL.
> Render `<AppShell/>`, assert the panel is **ABSENT** as a control, `fireEvent.keyDown`, assert it
> appears. Then mutation-prove it by deleting the `SCREEN_ACTIONS` entry. The pattern is in
> `TDSChallansScreen.test.tsx`'s `describe("a screen-owned key is actually ROUTED by the shell")`.

---

## 10. DEFINITION OF DONE, for whoever takes T3

1. **T3 shipped and its FeaturesScreen row `on`** — on **browser evidence**, not a green suite.
2. `make verify` exit 0 **and** `pnpm exec tsc --noEmit` clean (different gates).
3. backend **≥2220 pass, 0 SKIP, 0 FAIL** (verbose census); frontend **≥597 / 46 files**;
   `verify:demo` **33/33 run ALONE** (start insightd on :8088 first), re-pinned deliberately if T3
   moves a figure — it plausibly will, and the change must be explained.
4. ⛔ **THE REACHABILITY CHECK, for every new feature: name the exact HTTP request that exercises it
   end to end, and assert it in a test that reads the result back out through the OUTERMOST surface.**
   T2 passed every other bar on this list while being unreachable from every client. This item exists
   because nothing else on the list catches that.
5. Every new tenant table in `buildFixture` (`internal/platform/testdb/testdb_test.go`) or the teardown
   census fails **by name** — it did this session, exactly as designed.
6. Every new screen carrying **all 13** registrations including `SCREEN_ACTIONS`, with a real-shell
   keypress test.
7. `PLAN-TALLY-PARITY.md` §2.5 updated for every consumed block, **and §2 of this file obeyed** for
   anything touching the ACL seeder.
8. Every safety-critical invariant **mutation-proved**, and said so.
9. A successor handoff — and **fold what is still true into it rather than adding a twelfth file.**

---

## 11. WHICH HANDOFFS ARE SPENT

**Live:** this file, `PLAN-TALLY-PARITY.md` (§2.5 numbers, §2 working rules), `RESEARCH.md` (Tally
keystrokes, citable by line), `docs/design/*` (per-track models), `docs/statutory/*` (primary sources —
Form 140 only).

**Spent — kept for their reasoning only:** `HANDOFF.md`, `HANDOFF-TRACKS-2-3.md`,
`HANDOFF-T1-PRINTING.md`, `HANDOFF-PARITY-SPRINT.md`, `HANDOFF-CLOSING-THE-GAP.md`,
`HANDOFF-SESSION-T29-IMPORT.md`, `HANDOFF-FEATURE-LOCK.md`, `HANDOFF-NEXT-THREE.md`,
`HANDOFF-TIER-ONE.md`, `HANDOFF-AFTER-T6.md`, `HANDOFF-CLEAR-A-TIER.md`.

That is **eleven** superseded handoffs plus this one, and the sprawl is itself the finding its
predecessor named: each one re-derived context the last one had. `HANDOFF-CLEAR-A-TIER.md` was
accurate and useful, and its one wrong call was scope — it asked for three tracks at a measured rate
of one.

⚠️ **THEY ARE STILL ON DISK, DELIBERATELY.** Its §10 recommended deleting the spent ones and this
session did not, because that is a judgement about what documentation a team keeps and it belongs to
whoever owns the repo, not to whoever happens to be holding the current session. The recommendation
stands and is a good one — a handoff nobody can trust costs a session to re-verify — and the list
above is exactly what to remove. `git rm` them in one commit when you agree.

### Left in the dev database on purpose

- **`T6 Drive Co`** (`9466b85c-…`) from the previous session — three TDS deductions, two challans, a
  deductor profile. Drop it when you like.
- **`tally_freshproof`** — a scratch *database* predating this session. Not mine, harmless, drop it.
- This session's scratch databases (`tally_scratch_t2`, `tally_fresh_acl`) were **dropped**.
- The **demo company has no cost data and no TDS data**; neither is seeded. See §4.3.5.
