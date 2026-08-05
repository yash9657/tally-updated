# HANDOFF — NEXT: T3's API + SCREENS, AND T5. PHASE 0 IS BUILT AND THE ORDER SPINE IS REACHABLE.

> ### ⛔ THIS IS THE LIVE HANDOFF, UPDATED IN PLACE. Every other `HANDOFF-*.md` in this repo is SPENT — see §11.
> ### Start with §9 (run-first), then §0.1 (what changed), then §3.1 (what is left of the plan).

---

## 0.1 ⛔ UPDATE, 2026-08-05 (second session): PHASE 0 IS DONE. READ THIS BEFORE §3.

**Commits `e18830b` and `7b93e39`.** This file is updated IN PLACE rather than superseded, because
§11's own finding is that eleven superseded handoffs each re-derived what the last one knew. Anything
below that this section contradicts, THIS SECTION WINS.

**§3.1's Phase 0 is built, DB-proved and committed.** All five items:

| Phase 0 item | State |
|---|---|
| 1. Migrations 0042 (orders) + 0043 (BOM) | ✅ DB-proved from empty, 56 files apply |
| 2. `VoucherInput.Fulfilments` + payload assembly + `order_fulfilment` writes | ✅ `internal/ledger/fulfilment.go`, 7 live-Postgres tests **RUN** |
| 3. ⛔ `voucherReq.fulfilments` + `buildVoucherInput` (the reachability step) | ✅ **and mutation-proved from HTTP** |
| 4. TS API types for orders, BOM, e-Way Bill | ✅ `frontend/src/api/types.ts` |
| 5. The ACL SHAPE decision + migration 0073 | ✅ verified three ways with two controls |

**Plus, beyond Phase 0 and not asked for — because migration 0073 forced it:** the two order-book
reports, their five registration sites, and the HTTP reachability pin. 0073 seeds rules naming
`report:sales-order-book`; an `acl_rule` row naming a resource the vocabulary does not know is not
merely untested, it makes the admin API **422 an owner who reads their own level's rules and saves
them back**. So the report names could not land without routes, and the routes could not be a stub.

**AND THEN, IN THE SAME SESSION, §3.1a ITEMS 1 AND 2 — THE WRITE API:** `internal/orders`
(order + BOM service), `handler_orders.go` (10 routes), the three bare tokens wired into
`routeAction`, and the full TS client trio (`client.ts` / `rest.ts` / `mock.ts`).
**A human can now create an order, close it with a reason, keep a recipe and explode it.**
The HTTP reachability pin's fixture no longer reaches around the API: it POSTs the order, reads the
line id back, then delivers against it — the "what this does NOT prove" caveat is **deleted, not
weakened**.

**Measured gates at `e08b65f`:** backend **2266 pass / 0 SKIP / 0 FAIL** (session baseline 2220) ·
frontend **614 / 47 files** (baseline 597 / 46) · `tsc` clean · `make verify` exit 0 · `verify:demo`
**33/33 run ALONE** on a version-checked new build — unchanged, and now *measured* rather than
reasoned, which closes §4.4 for this change.

**Commits, in order:** `e18830b` the spine · `7b93e39` the order book + ACL · `f7c9469` this handoff ·
`e08b65f` the order + BOM write API.

### 0.1.1 ⛔ WHAT IS **NOT** BUILT, STATED FIRST

- ⛔ **NO SCREENS FOR ANY OF IT.** No order entry, no BOM entry, no order-book screen, and — the
  largest gap — **no fulfilment picker on voucher entry**. The whole backend and the whole client
  API exist and are tested; **a keyboard-only operator still cannot link a delivery to an order.**
  That is precisely T2's §4.0 shape (its masters and reports shipped, its allocation UI did not), and
  it is now the highest-value next piece of T3. §3.1a items 3 and 4.
- **`ReviseLineQty` is not on the wire.** `orders.Service` has it and migration 0042's
  `order_line_still_covers_fulfilments` trigger backs it, but no route calls it, so an order's
  committed quantity cannot be corrected from a client. The mock says so in its own comment rather
  than inventing a route.
- **T5 e-Way Bill: nothing built.** Only the TS types and the recorded citation decision (§6). It is
  still free parallelism.
- **BOM explosion does not post the Stock Journal**, deliberately — it returns what the lines WOULD
  be and the operator submits the voucher. Wiring "explode straight into a posted journal" would make
  a manufacture a side effect of opening a recipe; if it is ever wanted it is a voucher-entry feature,
  not an orders one.
- **Nothing seeds T3 into `seed:demo`**, so the order-book screens will render empty against the demo
  company — the same unclaimed work T2 and T6 left behind (§4.3.5).

### 0.1.2 THE DECISIONS THIS SESSION TOOK, so they are not re-litigated

1. ⛔ **ONE `order_head` WITH A DIRECTION ENUM, not `sales_order` + `purchase_order`** — a deliberate
   departure from §2.5's sketch, on 0025's own house pattern ("exactly one primitive: a positive
   quantity plus a direction"). Two head tables mean two copies of every constraint and index, and a
   shared `order_line` cannot FK to two parents without a polymorphic key.
2. ⛔ **BUT TWO REPORT NAMES** (`sales-order-book`, `purchase-order-book`), which is the OPPOSITE call
   and is the one that made an ACL migration necessary at all. **A query parameter is invisible to the
   PDP**: `routeAction` decides on the path, so one shared name makes *"the salesperson may see our
   sales pipeline but not what we have committed to pay suppliers"* an **unwritable rule**.
   ⚠️ **Neither of these should be "made consistent" with the other** — two tables would duplicate
   constraints, one report name would destroy an authority. Both files say so at their top.
3. **NO `fulfilled_qty` AND NO `status` COLUMN, ever.** Derived by summing `order_fulfilment` across
   CURRENT, non-deleted voucher versions. `is_closed` IS stored, because "abandoned" cannot be
   computed — an undelivered order and a cancelled one have identical fulfilment histories.
4. **OVER-FULFILMENT IS REFUSED** (the §T3 decision, taken once, in 0042's header). It costs nothing
   *because* a fulfilment qty is a LINK and not a movement: ship 105 against a 100-unit order, record
   a movement of 105 and a link of 100, and the extra 5 correctly consumed no order.
5. **Fulfilments ARE honoured on an item invoice**, where cost allocations are refused. An invoice's
   postings are server-generated; its inventory is the operator's own grid.
6. **`sales-order` / `purchase-order` are two bare tokens; `bom` is one.** The test recorded in
   `vocabulary.go` is not "are these things similar" but **"would an administrator ever grant one
   without the other"**. No ACL rows for any of the three.
7. ⛔ **`internal/orders` MAY IMPORT NOTHING BUT `internal/platform/db`**, and `archtest` pins it.
   That empty-but-for-db set is the architectural enforcement of "an order is not a movement": an edge
   to `internal/ledger` would be the beginning of "while recording this order, also move the stock".
   ⚠️ **The dependency runs the OTHER way and already exists** — `internal/ledger` owns
   `order_fulfilment`, because a fulfilment is written inside the voucher's transaction. The order
   table is the shared vocabulary, not a call.
8. **The DIRECTION IS IN THE PATH, never the body.** `orderReq` has no `direction` field and
   `CreateOrderRequest` does not either. `routeAction` reads the path, so a body field would let a
   caller holding only `sales-order` POST a purchase commitment to the sales route and be allowed —
   the PDP would have said yes to the wrong object. **The handlers additionally 404 an id whose
   direction does not match its route**, because both families read one table; that guard is
   mutation-proved, and without it the full purchase-order body (party, quantities, agreed rates) is
   served to a sales-only caller.
9. **BOM explosion multiplies BEFORE dividing** (`orders.ScaleComponent`), and the mock reproduces the
   same arithmetic. Computing the ratio first shorts every non-whole-multiple batch, always downward.

### 0.1.3 NEW TRAPS, each paid for this session

- ⛔ **AN ACL MIGRATION AND ITS REPORT NAMES ARE ONE COMMIT, NOT TWO.** A `disallow print
  report:NAME` row whose name is not in `authz.KnownReportNames` breaks `printDisallowedBySeededMigrations`'
  count assertion AND makes the admin API 422 a round-trip PUT of the level's own rules. And a name IN
  `KnownReportNames` needs a mapped route (`TestReportVocabularyMatchesRouter`). **So an ACL
  counterweight cannot be "front-loaded in Phase 0" ahead of the report it governs** — plan them
  together. This is the one place §3.1's Phase-0 advice is wrong.
- ⚠️ **`loadPlan` in `internal/platform/testdb` reads the LIVE database**, so new tenant tables fail
  the census with "the fixture seeds X, which the delete plan does NOT cover" until you `make migrate`.
  The message points at `plan.go` and the actual fix is a migration run.
- ⚠️ **The fixture may hold only ONE row per table** (`seedCompany` fatals on a second), which collides
  with `bom_component_not_self`: a legal recipe needs a finished good that is not the fixture's single
  item. Resolved by creating it inline in the `bom_head` statement via a CTE — the teardown assertions
  require >0 → 0 and never pin the count at one.
- ⚠️ **`gofmt -l` exits 0 while printing a filename**, so `gofmt -l f.go && echo CLEAN` prints CLEAN on
  a dirty file. Read the output, not the status.
- ⚠️ `internal/ledger` already declares a package-level `dec()` in `item_invoice_test.go`; a new test
  file needs its own name.
- ⚠️ **`InventoryLine` HAS NO `LineNo` FIELD** — line numbers come from slice position, so
  `Inventory[0]` is line 1. That is precisely why `FulfilmentInput.LineNo` is 1-based and an array
  index is off by one, and the validator says so in its error text.
- ⛔ **A MOCK METHOD THAT THROWS SYNCHRONOUSLY DOES NOT REJECT.** A helper typed
  `Promise<T>` that `throw`s before its first `await` escapes past `.catch()` and past
  `expect(...).rejects` — so a screen sees a crash where the real server would have handed it a
  handled 422. Every mock helper must be `async`. **`frontend/src/api/orders.test.ts` caught this on
  its first run**, which is the whole argument for writing the mock-contract test at the same time as
  the mock.
- ⛔ **A `View` TYPE THAT `extends` AN `Input` TYPE INHERITS ITS OPTIONALITY, AND THAT IS A TYPE THAT
  LIES ABOUT THE RESPONSE.** Three of them landed this session before `tsc` and the mock test found
  them: `rate?` on the way in is always a fixed-4 string on the way back; `line_no` is *absent* on
  the way in (the server assigns it from position) and *required* on the way back. This is the same
  defect class as T2's flat-vs-envelope lie, one layer up — **diff a response type against the
  handler's JSON, not against the request type it resembles.**
- ⚠️ `optionalUUID` maps `""` to `uuid.Nil`, so a handler that wants "absent" and a handler that
  wants "required" both get the same value and the DOMAIN has to distinguish them. Keep the
  all-zeros refusal wherever `uuid.Nil` is a sentinel (§7.3) — the BOM list filter needed it.
- ⚠️ **A NEW `internal/` PACKAGE NEEDS *TWO* ARCHTEST ENTRIES, NOT ONE.** Its own allowed-import set,
  AND an entry in `internal/server`'s set — `server` is the composition root and the walker fails the
  build for an undeclared edge. Adding only the first passes `go test ./internal/archtest/` when run
  against the package alone and fails once `server` imports it, which is a full-suite-only failure.

### 0.1.4 ⛔ THE §4.0 LESSON, NOW WITH A THIRD OCCURRENCE AND A TS COROLLARY

While adding the TS types this session found that **`VoucherRequest` in `frontend/src/api/types.ts`
had no `cost_allocations` field either.** T2's Go wire was fixed late in the previous session; its
TypeScript type never was. So a browser still could not construct an allocation, and every one of the
597 frontend tests passed.

**That is the same defect at a third layer, and it generalises the rule:** a feature is not reachable
until the field exists on **EVERY** wire between the operator and the table — Go struct, TS type, and
the form that fills it. Both `cost_allocations` and `fulfilments` are now on `VoucherRequest`.

**The pin that catches it** is `internal/server/order_fulfilment_http_test.go`, and it is
mutation-proved in both directions: rename the wire tag and the POST still returns **201** while the
report reads **0.0000**; drop the `current_version_id` join and the report reads **13.0000** where 8 is
correct. Copy this shape for every new feature — it is the only test in the project that could have
caught T2.

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
| **T3 spine** (tables, ledger, wire, order book) | ✅ **BUILT `e18830b`/`7b93e39`** — see §0.1 | — |
| **T3 order + BOM API** | ⬜ `internal/orders` absent. **A human cannot create an order** | §3.1a — this is the whole remaining backend |
| **T3 screens** | ⬜ nothing | §3.6, after the API |
| **T5 e-Way Bill** | ⬜ nothing built; TS types + citation decision only | §6 — still free parallelism, take it |

⚠️ **"Reachable" is not "usable".** T2's allocation write is reachable over HTTP and has no UI. That
distinction is the single most expensive thing this session learned — see §4.0 — and it is why the
FeaturesScreen row leads with what is missing.

---

## 1. WHAT IS TRUE NOW — measured, not asserted

| Gate | Value | Notes |
|---|---|---|
| backend `go test ./... -count=1 -v` | **2266 pass / 0 SKIP / 0 FAIL** | 2026-08-05 second session; was 2220 |
| frontend `pnpm run test:run` | **614 pass / 47 files** | was 597 / 46; the new file is the mock-contract suite |
| `pnpm exec tsc --noEmit` | clean | ⛔ **a separate gate from `make verify`** |
| `make verify` | exit 0 | |
| `verify:demo` | **33/33**, run alone — re-measured after T3, unchanged | ⚠️ it needs **insightd on :8088** as well as cored — a killed insightd makes it die with `TypeError: fetch failed`, which looks like a regression and is not |
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

Consumed 2026-08-05 (second session): **0042** (orders), **0043** (BOM), **0073** (order-book ACL).
Highest number in the tree is now **0073**, and **0073 IS THE HIGHEST-NUMBERED RESTATEMENT — DERIVE
FROM IT** (13 PERFORMs).

> ### ⛔ 0044 IS NOW ALSO PERMANENTLY UNUSABLE, FOR THE IDENTICAL REASON AS 0041.
> §2.5 reserved it for T3's ACL. **Two tracks have now hit this wall.** Stop treating a reserved ACL
> number as a number to try: **reserve blocks for TABLES, take the seeder number at the end, every
> time.** Reserved blocks still free FOR TABLES ONLY: T5's 0047–0048, Tier 2's 0051–0061.

Previously consumed: **0040** (T2 tables), **0072** (T2 ACL).

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

### 3.1 ⛔ PHASE 0 IS SPENT — IT IS BUILT AND COMMITTED (§0.1). WHAT FOLLOWS IS WHAT IS LEFT.

The five Phase-0 items are done, DB-proved and committed at `e18830b` / `7b93e39`. **Do not redo them.**
The original table is kept below only for its reasoning, struck through in effect:

| # | What | State |
|---|---|---|
| 1 | Migrations 0042 (orders) + 0043 (BOM) | ✅ done, from-empty proof, controls |
| 2 | `VoucherInput.Fulfilments` + `post()` payload + `order_fulfilment` writes | ✅ done, 7 DB tests RUN |
| 3 | `voucherReq.fulfilments` + `buildVoucherInput` | ✅ done, mutation-proved from HTTP |
| 4 | TS API types for orders, BOM, e-Way Bill | ✅ done |
| 5 | The ACL shape decision + migration 0073 | ✅ done — **and see §0.1.3: it could NOT have been front-loaded ahead of its report** |

⚠️ **AND ONE PIECE OF §3.1's ADVICE IS NOW KNOWN WRONG.** Item 5 said to write the ACL migration in
Phase 0. You cannot: an `acl_rule` row naming a `report:NAME` the vocabulary does not know breaks a
count tripwire AND 422s an owner round-tripping their own rules, while a name in `KnownReportNames`
needs a mapped route. **An ACL counterweight ships WITH the report it governs, in one commit.** That is
why this session delivered the order book as well.

### 3.1a WHAT IS LEFT OF T3, IN THE ORDER THAT DE-RISKS IT

**⛔ START HERE: THE ORDER + BOM WRITE API. Nothing else in T3 is reachable without it.** A human still
cannot create an order, which is the §4.0 gap in a new place — the spine, the reports and the wire are
all built and tested against orders that only SQL can create.

| # | Piece | Owns | Notes |
|---|---|---|---|
| ~~1~~ | ~~Order CRUD~~ | `internal/orders/order.go`, `internal/server/handler_orders.go` | ✅ **DONE.** 10 routes, 7 HTTP tests, the direction guard mutation-proved |
| ~~2~~ | ~~BOM CRUD + explosion~~ | `internal/orders/bom.go` | ✅ **DONE.** `ScaleComponent` extracted as a pure function and mutation-proved; 8-case table |
| **3** | ⛔ **The fulfilment picker on voucher entry** | the inventory grid | **THE LARGEST REMAINING T3 GAP — a keyboard-only operator still cannot link a delivery to an order.** It must offer OUTSTANDING order lines for the item on the row (`GET /sales-orders?open_only=true`, then the line ids off `GET /sales-orders/{id}`) and **never ask for a `line_no` or an `order_line_id`**. Read `FulfilmentInput` in `types.ts` first — its `qty` is a LINK and must not be defaulted-and-locked to the line quantity, or an over-delivery cannot be saved at all |
| **4** | **Order + order-book screens** | one lane, all 13 registration sites each | §3.6 applies. ⚠️ The order book is **two screens, not one with a toggle** — a toggle would reach a resource the operator may be denied (§0.1.2 item 2). The client methods all exist and the mock refuses what the server refuses, so a screen can be built against `createMockClient()` with some confidence for once |
| **5** | **Seed T3 into `seed:demo`** | `frontend/scripts/seed-demo.mts` | Same unclaimed work T2 and T6 left behind (§4.3.5). One order plus one partial delivery makes both order books demonstrable |
| 6 | **`ReviseLineQty` on the wire** | `handler_orders.go` | The service method and 0042's trigger exist; no route calls it, so a committed quantity cannot be corrected from a client. Small, and the interesting part (a revision below what is already delivered) is already refused by the database |

**Every one of these must carry §3.3's six items plus the reachability question.** The bar piece 1 was
held to, and the one to reuse: **its HTTP test's fixture may not reach around the API.**
`order_fulfilment_http_test.go` now POSTs the order, reads the line id back, and delivers against it;
the only SQL left is opening stock, which is a fixture concern rather than the feature. When a screen
lands, the equivalent is that the browser drive (§7.5) creates the order through the UI.

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

**⛔ THE WHOLE SPINE IS NOW BUILT — see §0.1. `CanonicalFulfilment` is not merely frozen, it is WIRED:**
`VoucherInput.Fulfilments`, `validateFulfilments`, the payload assembly, the `order_fulfilment` writes,
the `voucherReq.fulfilments` wire field and both order-book reports all exist and are tested. What is
left is the WRITE API and the screens (§3.1a). The rest of this section is kept for its reasoning, which
is still the reasoning the code follows.

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

**~~Nothing populates it yet.~~ SUPERSEDED: `internal/ledger/fulfilment.go` populates it, modelled on
`cost_allocation.go` exactly as this paragraph advised.** Read that file rather than this paragraph.

**Migrations: ~~0042/0043~~ ALL CONSUMED.** 0042 (orders), 0043 (BOM) and **0073** (the order-book ACL,
taken above 0072 — **0044 is now permanently empty**, §2). The shape decision was taken: **two report
names**, because a query parameter is invisible to the PDP (§0.1.2).

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
