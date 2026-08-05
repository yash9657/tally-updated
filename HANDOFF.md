# HANDOFF — THE LIVE ONE. Start here, from cold.

> **Written 2026-08-05, at `c4f91e6`.** This file replaces the twelve `HANDOFF-*.md` files that
> preceded it. **Every other `HANDOFF-*.md` in this repository is SPENT** — see §11 for the list and
> for the one thing to do about them.
>
> **Read §1 → §2 → §3. Then act on §4.** Everything after §6 is reference you will need exactly when
> you hit it, not reading for now.

---

## 1. THE ANSWER TO THE QUESTION YOU ARE ABOUT TO ASK

**Is Tier 1 done? NO — one row short, and you can see it on the product's own face.**

The `FeaturesScreen` (`frontend/src/screens/FeaturesScreen.tsx`) is this project's parity ledger, and
T7's whole purpose was to make it honest. It is the authority, not this file. Measured at `c4f91e6`:
**27 rows `on`, 13 `off`, 6 qualified** (`always` / `server`).

| Tier 1 track | Row | State |
|---|---|---|
| T1 Printing | several | ✅ `on` |
| T2 Cost centres | "Cost centres and cost categories — the masters and both reports" | ✅ `on` |
| T2 (cont.) | "Cost centre allocation during voucher entry" | ⚠️ `server` — **no UI** |
| **T3 Orders + BOM** | "Order processing (Sales / Purchase Orders) and Bill of Materials" | ⛔ **`off` — no UI** |
| T4 Bank reconciliation | "Bank reconciliation" | ✅ `on` |
| **T5 e-Way Bill** | "e-Way Bill" | ⛔ **`off` — no route, no UI** |
| T6 TDS/TCS | "TDS / TCS" | ✅ `on` |
| T7 Features screen | — | ✅ done |

**So: do NOT start Tier 2.** Two rows are `off` and both are `off` for the SAME reason, which is the
most expensive lesson in this repository's history:

> ### ⛔⛔ EVERY BACKEND FOR T3 AND T5 IS BUILT, TESTED AND REACHABLE. NEITHER HAS A SCREEN.
> A keyboard-only operator cannot create an order, cannot link a delivery to one, and cannot raise an
> e-way bill. **The remaining Tier-1 work is almost entirely FRONTEND**, and §4 is that work.

---

## 2. RUN-FIRST CHECKLIST — ten minutes, and it has burned every session that skipped it

```bash
# 1. Tree clean? Commit or stash BEFORE doing anything.
git status --porcelain

# 2. Postgres. ⛔ A TCP CONNECT IS NOT LIVENESS — docker-proxy holds the port open past a
#    dead backend, and a watchdog built on `nc -z` logged NOTHING through a ten-minute outage.
docker ps | grep tally_pg
pg_isready -h localhost -p 5433 -t 5 && \
  PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"

# 3. Kill stale servers BY PID. `pkill -f "cmd/cored serve"` misses the compiled child.
lsof -nP -iTCP:8087 -sTCP:LISTEN     # -> kill -9 that PID
ps -eo pid,etime,command | grep "cored serve" | grep -v grep   # etime is AGE; a 28h process predates you
lsof -nP -iTCP:5173 -sTCP:LISTEN     # a stale vite serves day-old code just as happily
lsof -nP -iTCP:8088 -sTCP:LISTEN     # insightd (uvicorn). verify:demo needs it UP.

# 4. Baseline, and WRITE IT DOWN. Without -v the SKIP census is a lie.
cd backend && go test ./... -count=1 -v 2>&1 | tee /tmp/base.log | tail -3
grep -c -- '--- PASS' /tmp/base.log; grep -c -- '--- SKIP' /tmp/base.log   # SKIP MUST BE 0
cd ../frontend && pnpm exec tsc --noEmit && pnpm run test:run
cd .. && make verify
```

### 2.1 The gate baseline at `c4f91e6` — re-measure, do not assume

| Gate | Value |
|---|---|
| backend `go test ./... -count=1 -v` | **2292 pass / 0 SKIP / 0 FAIL** |
| frontend `pnpm run test:run` | **614 pass / 47 files** |
| `pnpm exec tsc --noEmit` | clean — ⛔ **a SEPARATE gate from `make verify`** |
| `make verify` | exit 0 |
| `verify:demo` | **33/33**, run **ALONE**, needs cored **and** insightd up |
| `gofmt -l` on every file you touch | clean |

⚠️ **A DOWN DATABASE MAKES EVERY DB TEST SKIP AND `go test` STILL EXITS 0.** A skip is
indistinguishable from a pass in the exit code. This has invalidated a whole session's evidence
before. Bracket every suite run with a live `psql` before **and** after.

⚠️ **`make verify` DOES NOT RUN `tsc`.** They are different gates. Folding `tsc` into `make verify` is
a ~10-minute job and is still the cheapest real improvement available.

### 2.2 Driving the app

```bash
make run-cored      # :8087
make run-insightd   # :8088
cd frontend && pnpm dev   # :5173
open "http://localhost:5173/?company=$(cat frontend/.demo-company-id)"
```

Sign in `browser-dev-owner` / `tally-demo-2026`. **It is a keystroke app: a click SELECTS, `Enter`
OPENS.**

⛔ **`/healthz` IS NOT A VERSION CHECK.** Poll for something only the new build serves, and read the
STATUS: `401` means the route exists and needs auth; `404` means you are talking to the old binary.

---

## 3. THE MACHINE, AND HOW TO NOT MAKE IT SLOW

| Constraint | Value | Consequence |
|---|---|---|
| Cores / RAM | 8 / 16 GB, shared with other worktrees | |
| Concurrent agents per workflow | `min(16, cores-2)` = **6** | asking for 20 queues 14 |
| Workflows at once | **ONE** | 3 × 6 agents each spawning a compiler is what turns a track into an afternoon |
| Postgres | **ONE instance on :5433, shared by every worktree** | ⛔ parallel lanes may NOT run DB-backed tests; concurrent `migrations.Apply` races and the residue check counts rows globally |
| Heavy commands | auto-queued through `ci-slot` (2 slots) by a PreToolUse hook | **do not wrap anything yourself**; a command that pauses is WAITING, not hung |

**Live gates (`verify:demo`, `verify:tenancy`, `e2e:ui`) run ALONE.** Slow? `/machine-health`.

### 3.1 The measured rate, which should shape what you promise

| Session | Delivered | Workflows | Subagent tokens |
|---|---|---|---|
| n−2 | T6 | 4, with 3 sequential barriers | ~1.7M |
| n−1 | T2 + the shared T3 contract | 2, zero barriers | ~1.3M |
| **n (this one)** | **T3 backend end-to-end + T5 core** | **0 — solo, no subagents** | **0** |

**Two observations, and the second is the surprising one.** First: plan for roughly one track per
session. Second: this session delivered more than either of the two before it **with no subagents at
all**, because the work was a long serial chain of decisions in contended files — schema, then the
ledger, then the wire, then the report, then the API — where a lane split would have spent its
parallelism on coordination. **Fan out for genuinely disjoint files; go solo down a dependency chain.**

---

## 4. ⛔⛔ THE WORK, IN ORDER. THIS IS THE PART YOU ACT ON.

Everything here is Tier 1. **Do not start Tier 2 (§5) until items 1–3 are done and their FeaturesScreen
rows are `on` on BROWSER evidence.**

### 4.1 THE BAR, restated because it is the whole point

> **A feature is not shipped when its tests pass. It is shipped when a human can reach it.**
>
> This has been learned three times, at increasing cost:
> 1. `voucherReq.ItemLedgerID` — every item invoice the F8/F9 grid could send came back 422; the
>    generator was reachable only from Go tests.
> 2. **T2's cost allocations** — the validation, the hashed payload entry and the row insert were
>    built, reviewed, and covered by unit AND live-Postgres tests **while no HTTP body could carry an
>    allocation.** Nothing failed. Both report routes answered 200 and were *correct*: they accurately
>    reported that nothing had been allocated, because nothing could be.
> 3. **T2 again, one layer up** — the Go wire was fixed and `VoucherRequest` in
>    `frontend/src/api/types.ts` was not. So a browser still could not construct one, and all 597
>    frontend tests passed.
>
> **THE RULE: a feature is not reachable until the field exists on EVERY wire between the operator and
> the table — Go struct, TS type, and the form that fills it.**
>
> **THE PATTERN THAT CATCHES IT:** `backend/internal/server/order_fulfilment_http_test.go`. It POSTs
> as JSON and reads the figures back out of a REPORT over HTTP, so the assertion spans
> wire → ledger → payload → table → report → wire, with nothing stubbed and **no SQL in the fixture
> that reaches around the API**. It is mutation-proved in both directions. Copy this shape for every
> new feature.

### 4.2 ITEM 1 — ⛔ THE FULFILMENT PICKER ON VOUCHER ENTRY (largest, do it first)

**A keyboard-only operator cannot link a delivery note to a sales order.** The entire backend exists:
`voucherReq.fulfilments` on the wire, `FulfilmentInput` in `types.ts`, over-fulfilment refused by a
deferred trigger, and both order books reading it back.

- It attaches to the **inventory grid**, on a row the operator is already looking at.
- Fetch candidates with `GET /sales-orders?open_only=true`, then the line ids from
  `GET /sales-orders/{id}`.
- ⛔ **NEVER ASK FOR A `line_no` OR AN `order_line_id`.** Both are identifiers no screen displays.
  `line_no` is the voucher's own 1-based inventory line and the client fills it from the row's
  position; `order_line_id` comes from the picked order.
- ⛔ **DO NOT DEFAULT `qty` TO THE LINE QUANTITY AND LOCK IT.** A fulfilment qty is a **LINK, not a
  movement**: shipping 105 against a 100-unit order records a movement of 105 and a link of 100, and
  the extra 5 correctly consumed no order. A locked field makes an over-delivery unsaveable.
- Read `FulfilmentInput` in `frontend/src/api/types.ts` first. It carries all of this.

**This is the exact twin of T2's missing allocation UI (item 4), so build them the same way once.**

### 4.3 ITEM 2 — THE T3 SCREENS

Order entry, order list, BOM entry, and **two** order-book screens.

- ⛔ **THE ORDER BOOK IS TWO SCREENS, NOT ONE WITH A DIRECTION TOGGLE.** `sales-order-book` and
  `purchase-order-book` are separate report names because **a query parameter is invisible to the
  PDP**; a toggle would reach a resource the operator may be denied and produce a 403 nobody can
  attribute.
- Every screen needs **all 13 registrations** — see §7.2. ⛔ `AppShell.tsx`'s `SCREEN_ACTIONS` is the
  one that ships a key **completely dead**, no error and no status line.
- The client methods all exist (`createSalesOrder`, `listSalesOrders`, `getSalesOrder`,
  `closeSalesOrder`, `createBom`, `explodeBom`, `salesOrderBook`, …) and **the mock refuses what the
  server refuses**, pinned by `frontend/src/api/orders.test.ts`. You can build against
  `createMockClient()` with more confidence than usual.

### 4.4 ITEM 3 — T5's ROUTE AND SCREEN

The rules-and-payload core is done (`internal/statutory/ewaybill.go`, `rule138.go`). What is missing:

- A **cored-side store** for the local reference — migrations **0047/0048 are reserved for exactly
  this** and are still free (they are TABLE numbers only; see §6).
- A route, and a screen that **renders the `disclosures` block**. That block is not decoration: it is
  the only thing standing between a printed sheet and a driver believing the consignment is covered.
- ⚠️ `internal/statutory` may import **nothing** from `internal/` (archtest pins the empty set) — it
  is a separate deployable that talks to cored over HTTP. Do not "just import the ledger".

### 4.5 ITEM 4 — T2's ALLOCATION UI on voucher entry

The `server` row that has been qualified since T2 shipped. Same shape as item 1, and both should reuse
one component.

- ⛔ `posting_seq` is a **0-BASED ordinal no screen displays**, and it is 0-based where the fulfilment
  `line_no` beside it is **1-based**. Both are inherited, neither is negotiable, and a client that
  confuses them is off by one.
- An **item invoice refuses allocations outright** — its postings are server-generated and index 0 is
  the *party* leg. Allocate on a Journal, Payment, Receipt or Contra.
- `CostAllocationInput` in `types.ts` carries the warning. Read it first.

### 4.6 ITEM 5 — SEED T3 AND T2 INTO `seed:demo`

`frontend/scripts/seed-demo.mts`. Nothing seeds cost data, TDS data or orders, so **four screens
render empty against the demo company** and cannot be demonstrated. One axis, two centres, one
allocated voucher, one order and one partial delivery would fix all of it. Same unclaimed work T2 and
T6 both left behind.

⚠️ `seed-demo.mts`, `verify-demo.mts` and `corpora/validate.py` are **CO-OWNED**. ADD only, and
re-read immediately before each edit.

### 4.7 SMALLER, REAL, AND EACH WORTH DOING

| # | What | Why |
|---|---|---|
| 6 | `ReviseLineQty` on the wire | The service method and 0042's trigger exist; no route calls it, so a committed quantity cannot be corrected from a client |
| 7 | `VersionView` does not project cost allocations | The edit log SIGNS them but does not SHOW them: an auditor opening an altered pay run sees two versions that render identically |
| 8 | The HTTP voucher GET/PUT may not carry `cost_allocations` | **Unverified.** If it does not, a client that rebuilds a voucher from the GET silently clears the split on an unrelated edit. Same class as §4.1's defect. **Check this before building item 4** |
| 9 | The pre-flight allocation refusal names the axis by raw UUID | Strictly *less* usable than the DB trigger message it exists to pre-empt. Resolve names at the in-transaction call site, which already holds the tx |
| 10 | The two cost reports disagree about an off-axis allocation | The category summary excludes it and reports it separately; the centre summary silently includes it. Neither says so |
| 11 | Fold `tsc` into `make verify` | ~10 minutes, and it closes a whole class of "green but broken" |

---

## 5. TIER 2 — WHAT IT IS, AND THE ONE THING NOT TO GET WRONG IN EACH

**Do not start these until §4 items 1–3 are done.** `PLAN-TALLY-PARITY.md` §4 is the authority;
this is the short form, ordered by rough value.

| # | Track | The one thing not to get wrong |
|---|---|---|
| **T8** | Multi-currency | A foreign amount, a rate, and the base amount are **three stored values**, not two plus arithmetic. Realised vs unrealised gain/loss are different postings |
| **T9** | FIFO / LIFO valuation | Weighted average's replay state is a strict SUBSET of what FIFO needs, so FIFO is a new arm in `ValuerFor` plus fixtures — **not** a schema change. ⚠️ Closing stock now feeds the Balance Sheet, so a valuation change MOVES THE SHEET: re-pin `verify:demo` deliberately |
| **T10** | Stock groups + compound units | A conversion factor makes every quantity in the system *unit-qualified*. Touch the movement primitive with care and **never store a converted quantity you can re-derive** |
| **T11** | Budgets | A budget is a comparison dimension, not a posting. ⛔ It inherits T2's axis model — see §8 |
| **T12** | GSTR-9 annual return | It is a *reconciliation* of the year's returns to the books and must **state where it disagrees** rather than smoothing it |
| **T13** | Interest on overdue bills | Simple vs compound and the day-count basis are configuration with statutory defaults — **cite them** |
| **T14** | Backup / restore, split company | Must be tenant-exact. Reuse the `testdb` delete-plan census: it already knows every tenant table and is anti-rot by construction |
| **T15/T16** | Export (Excel/CSV/XML), per-company print templates | 0065 / 0066 reserved |

**Also `off` and NOT in a numbered tier:** SSO/OIDC login.

> ### ⛔ T18 (the Rule 48(4) QR code) IS PERMANENTLY UNSHIPPABLE FROM HERE, AND ITS ROW MUST STAY OFF.
> The payload is signed with the Income Tax Department's private key. Anything we generate tells a
> scanner the invoice was registered when it was not. **If a workflow reports T18 done, reject it.**
> ⚠️ Note this is NOT true of T5: an EBN is just an identifier NIC allocates, so T5 becomes real the
> day credentials exist, with no change to the payload builder. The two gaps look alike and are not.

---

## 6. ⛔⛔ MIGRATIONS — READ BEFORE TAKING A NUMBER

Highest number in the tree is **0073**. **`0073_order_book_acl.sql` is the highest-numbered
restatement of `seed_company_auth_defaults` (13 PERFORMs) — DERIVE FROM IT.**

> ### ⛔ 0041 AND 0044 ARE PERMANENTLY UNUSABLE AND MUST BE LEFT EMPTY.
> Both were reserved for a track's ACL rules and neither can carry them.
> `seed_company_auth_defaults` is extended only by **restating its whole plpgsql body**,
> `migrations.Apply` runs files in `sort.Strings` order, and **the highest-numbered restatement is the
> only body that survives**. Rules written at 0041 or 0044 are overwritten on every fresh database and
> simply do not exist.
>
> **TWO TRACKS HAVE NOW HIT THIS WALL** (T2 → 0072, T3 → 0073). Stop treating a reserved ACL number as
> a number to try:
>
> ## RESERVE BLOCKS FOR **TABLES**. TAKE THE SEEDER NUMBER AT THE END. EVERY TIME.

Still free **for tables only**: T5's **0047–0048**, Tier 2's **0051–0061**, 0065, 0066.

`backend/migrations/seeder_census_test.go` guards this **statically, with no database**, because the
defect lives in apply order and is invisible on any incrementally-migrated developer machine. **If it
fails, do not route around it.**

### 6.1 Does your track even need an ACL migration? Usually not.

- **A BARE RESOURCE TOKEN NEEDS NONE.** The seeded `Data Entry` level has `default_access = 'none'`
  and every rule names a specific token, so a token never mentioned is **denied**. `tds-challan`,
  `tds-profile`, `cost-centre`, `sales-order`, `purchase-order` and `bom` all shipped this way with
  **zero** ACL rows.
- **A `report:NAME` DOES, MANDATORILY.** Migration 0011 seeds a coarse `allow display report` which
  `internal/authz` matches against *any* `report:NAME` at specificity 1 — so **a new report is GRANTED
  to Data Entry the moment it exists.** The refusal is the thing that must be written, and it must
  write **both** `disallow display` **and** `disallow print`: a report that cannot be displayed but
  *can* be printed is a leak with a paper trail.

**Decide the resource SHAPE first.** It decides whether you spend a migration number at all.

⛔ **AND AN ACL MIGRATION SHIPS IN THE SAME COMMIT AS ITS REPORT.** You cannot front-load it: an
`acl_rule` row naming a `report:NAME` the vocabulary does not know breaks
`printDisallowedBySeededMigrations`' count assertion **and makes the admin API 422 an owner who reads
their own level's rules and saves them back**, while a name in `KnownReportNames` needs a mapped
route. This was learned by trying it.

### 6.2 How to verify an ACL migration, since a green suite proves nothing here

Do all three. **The second catches the real defect.**

1. `go test ./migrations/` — the static census.
2. **Diff the restated body against its base with comments stripped.** For 0073 that was exactly one
   line and a PERFORM count of 12 → 13. Anything else means a predecessor's rules were dropped.
3. **Migrate a scratch database from empty, seed one company through the real
   `seed_company_auth_defaults`, and COUNT** — including a **CONTROL** row from an earlier migration to
   prove your restatement dropped nothing. 0073's controls were `report:bank-statement` (the line 0068
   once dropped: still 2 rows) and 0072's four `report:cost-%` rows.

---

## 7. THE REGISTRATION SURFACE — miss one and it is 403 FOR THE OWNER TOO

### 7.1 A new RESOURCE needs 4

`routeAction()` · `authz.KnownResources` · (`authz.KnownReportNames` + `reportName()` **only if it is
a report**). Gate: `go test ./internal/server/ -run TestEveryCompanyRouteIsMappedAndInVocabulary`.

⚠️ Adding a name to `KnownReportNames` **AUTO-MOUNTS** `GET /print/reports/<name>`; a name with no
`reportName()` case is **default-DENY for everyone including the Owner**. That bug shipped once for
`reconciliation-queue`.

⚠️ A report's route path must be a **FLAT LITERAL spelling its own name**. Need a selector? **Query
parameter** — unless the selector deserves a different *authority*, in which case it needs a second
NAME (that is why there are two order books).

⚠️ **Two count assertions move whenever a report name is added**, and both are deliberate tripwires:
`handler_print_reports.go`'s deferred-set header plus its count in `handler_print_test.go`, and
`printDisallowedBySeededMigrations`. **Update the prose, not just the number.**

⚠️ **A NEW `internal/` PACKAGE NEEDS *TWO* ARCHTEST ENTRIES** — its own allowed-import set, AND an
entry in `internal/server`'s. Only the first passes when run against the package alone; the second
fails only in the full suite.

### 7.2 A new SCREEN needs 13

The two whose file paths lie: `ScreenKind` is in `frontend/src/keystroke/types.ts` (**not**
`screenRegistry.tsx`), and `actionLabel()` is in `keystroke/keymaps/tallyprime.ts` (**not**
`AppShell.tsx`).

⛔ **`AppShell.tsx`'s `SCREEN_ACTIONS` is the one that ships the key COMPLETELY DEAD** — no error, no
status line. ⛔ **An unbound key is NOT inert**: F5 opens a Payment voucher over your form, F9 a
Purchase, F8 Sales.

Plus, for a **report** screen only: a classification in `FrameOverlays.tsx`'s `FULL_PERIOD_REPORTS` /
`AS_AT_ONLY_REPORTS` / `NO_DATE_DIMENSION_REPORTS`. **A report in none of the three is told its period
does nothing** — that shipped twice.

And **client methods in three files**: `api/client.ts` (declaring it there makes `rest.ts` and
`mock.ts` fail to compile until both implement it — *use that*), `api/rest.ts`, `api/mock.ts`.

> ### ⛔ IF A KEY IS THE FEATURE, THE TEST MUST PRESS THE KEY ON THE REAL SHELL.
> Render `<AppShell/>`, assert the panel is **ABSENT** as a control, `fireEvent.keyDown`, assert it
> appears. Then mutation-prove it by deleting the `SCREEN_ACTIONS` entry. The pattern is in
> `TDSChallansScreen.test.tsx`'s `describe("a screen-owned key is actually ROUTED by the shell")`.

### 7.3 ⛔ THE MOCK IS A REAL DELIVERABLE AND MUST REFUSE WHAT THE SERVER REFUSES

`AppShell.test.tsx` renders with `createMockClient()`, so a lenient mock means a green suite on top of
a create path that cannot work in a browser. That happened twice in one session — once for a missing
`created_by` (server 400, mock 201) and once for a response **SHAPE** (the server nests a view under
`cost_centre` and may send it null; the mock returned it flat and `rest.ts` typed the envelope AS the
flat view — a lie the compiler cannot catch, so every field read `undefined` at runtime).

**When you add a mock method, diff its refusals AND its shape against the handler**, not against the
type you wish existed. `frontend/src/api/orders.test.ts` is the pattern: it asserts the REFUSALS and
the arithmetic, with status codes, and it caught three real defects on its first run.

Two of those are worth stating as rules:

- ⛔ **A mock method that THROWS SYNCHRONOUSLY from a `Promise`-typed signature does not reject.** The
  exception escapes past `.catch()` and past `expect(...).rejects`, so a screen sees a crash where the
  server would have handed it a handled 422. **Every mock helper must be `async`.**
- ⛔ **A `View` type that `extends` an `Input` type inherits its optionality, and that is a type that
  LIES ABOUT THE RESPONSE.** `rate?` on the way in is always sent back; `line_no` is *absent* on the
  way in (the server assigns it from position) and *required* on the way back.

---

## 8. THE DOMAIN MODELS YOU WILL INHERIT — get these wrong and it costs three tracks

### 8.1 ⛔ A COST CATEGORY IS AN AXIS. A COST CENTRE IS A POSITION ON IT.

Several axes allocate the **same money in parallel**: one ₹1,00,000 posting is allocated in full across
Departments *and again in full* across Branches. Both are complete and both are correct.

**Therefore the sum-to-posting invariant is per (posting, CATEGORY), never per posting**, and
**anything that totals across axes double-counts** — for two axes it is 2× the cost borne. **T11
(budgets) and T24 inherit this shape.**

### 8.2 ⛔ AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT.

The stock ledger has exactly one primitive — a positive quantity plus a direction — and an order is
neither. Push it through `stock_movement` and closing stock is corrupted, **and closing stock now feeds
the Balance Sheet**. The error compounds with every open order and reconciles against nothing.

`internal/archtest` enforces this: `internal/orders` may import **nothing but `internal/platform/db`**.
⚠️ The dependency runs the OTHER way — `internal/ledger` owns `order_fulfilment`, because a fulfilment
is written inside the voucher's transaction. **The order table is the shared vocabulary, not a call.**

- **Partial fulfilment is the normal case**, and the outstanding balance is the entire point.
- ⛔ **NO `fulfilled_qty` AND NO `status` COLUMN, EVER.** Both are derived by summing
  `order_fulfilment` across **CURRENT, non-deleted** voucher versions. A stored figure goes stale
  undetectably the first time a fulfilling voucher is ALTERED, because an ALTER writes a NEW version
  and re-states its links without visiting the order. `is_closed` IS stored, because "abandoned"
  cannot be computed — an undelivered order and a cancelled one have identical fulfilment histories.
- ⛔ **EVERY READER MUST JOIN `voucher_head.current_version_id`.** Without it a delivery corrected from
  5 to 8 counts as 13. `stock_movement_not_negative` (0025) is the reference implementation.
- **A fulfilment qty is a LINK, not a movement.** That is why refusing over-fulfilment costs nothing.

### 8.3 ⛔ A SCHEMA MAY UNIFY WHERE AN AUTHORITY MUST SPLIT.

Orders are ONE table with a direction column (two head tables mean two copies of every constraint).
The order books are TWO report names (**a query parameter is invisible to the PDP**, so one name makes
"the salesperson may see our pipeline but not our purchase commitments" an unwritable rule).

**Neither should be "made consistent" with the other.** Both files say so at their top.

### 8.4 ⛔ `posting` IS HARD IMMUTABLE AND AN ALTER RE-CREATES EVERY POSTING ROW.

Anything keyed on `posting.id` from *outside* the voucher path goes silently stale.
`posting_cost_allocation` may key on it because `post()` writes it in the same transaction; 0045 and
0069 may not, and store what they matched instead. **Know which case you are in.**

---

## 9. TRAPS — every one of these cost real time

### 9.1 Prose rots. Correct it; do not delete it.

The false version is the plausible one and **will be re-invented**. Claims that died:

- *"a voucher number allocated and then rolled back is a gap in a statutory series"* — **false.** There
  is no sequence in this schema; `voucher_number_counter` is a table row advanced on the same
  transaction, so a rollback gives the number back. It survived because it is what a Postgres SEQUENCE
  *would* do.
- *"what the edit log SHOWS must be what the edit log SIGNS"* — true of postings and stock lines, **not**
  of cost allocations (§4.7 item 7). Borrowing a true sentence into a place where it is false is the
  same defect as writing a false one.
- *"cost_allocations is reachable from a client"* — true of Go, false of TypeScript, for a whole
  feature.

**And prefer a refusal that takes a company and a period over one that takes no arguments** — the
latter can only make claims about the software, and those go stale silently.

### 9.2 A test that CANNOT FAIL is worse than no test

A guard forbidding a money field asked whether a type satisfied `interface{ Total() any }` — a
signature nothing would ever declare — so the branch was unreachable and the test passed
unconditionally **while reading as a guard**. It was found by *adding the very field it forbade* and
watching it stay green.

**Mutation-prove every guard, especially the ones that assert a negative.** A negative assertion has
no natural failing case, so nothing else will ever tell you it is inert. Give each one a POSITIVE
assertion alongside so it cannot pass by finding nothing.

### 9.3 A differential test needs a CONTROL

A refusal that fires **for the wrong reason** reads as coverage and is not. Two probes once "passed" by
tripping a uniqueness constraint before ever reaching the invariant under test. Every refusal test in
`fulfilment_db_test.go` and `orders_http_test.go` carries the control that proves what it refused.

### 9.4 The all-zeros UUID parses, and `uuid.Nil` is everyone's sentinel

`uuid.Parse("00000000-...-0")` **succeeds**. Two handlers treated it as "absent", so an explicit
all-zeros `parent_id` **lifted an existing child out of its hierarchy** (with a 200 and a truthful
read-back) and an all-zeros filter silently returned **every** row.

**Any handler whose "absent" sentinel is `uuid.Nil` must refuse an explicit zero.** Grep for `uuid.Nil`
comparisons after a `uuid.Parse`.

### 9.5 Never claim a validation that was not performed

Unreachable from here: the **FVU** (no JRE), the **GSTN portal**, the **NIC e-Way Bill API**, the
**IRP**, the **EPFO/ESIC portals**, the **TIN `.csi`** file, and **CBIC's own rule repository** (TLS
chain verification failure, 2026-08-05). Ship the deterministic fake plus the recorded schema and
**say so on the artefact's face**. `internal/statutory/rule138.go` is the worked example of how:
citation, grade, sources, caveat, all travelling with the value.

### 9.6 Operational

- **A browser WRITE needs a passkey, and you CAN drive it. Do not disable step-up.** Any write answers
  403 `STEPUP_REQUIRED` — including from `curl` with a valid dev token. Attach a virtual authenticator
  over CDP and the ceremony completes on its own, because `stepUp()` **enrols this device** when it
  holds none of the listed credentials:
  ```js
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('WebAuthn.enable', { enableUI: false });
  await cdp.send('WebAuthn.addVirtualAuthenticator', { options: {
    protocol:'ctap2', transport:'usb', hasResidentKey:true,
    hasUserVerification:true, isUserVerified:true, automaticPresenceSimulation:true } });
  ```
  Chrome allows only ONE `internal` authenticator per environment (use `usb` for a second), and the
  authenticator lives on the **CDP session** — add it and drive in the same one. **Verified working.**
- **Launch servers detached and RETURN from the command.** `(nohup … &)` inside a command that then
  blocks dies with your own timeout, taking the server with it.
- **Every `cored` restart mints a new ephemeral signing key**, so every saved token dies with it. It
  presents as an unrelated 401.
- **`gofmt -l` EXITS 0 WHILE PRINTING A FILENAME**, so `gofmt -l f.go && echo CLEAN` prints CLEAN on a
  dirty file. **Read the output, not the status.**
- **`gofmt -w <dir>` reformats files you did not touch. Format NAMED FILES.** Pre-existing drift in
  `internal/statutory/{provider,schema,fake}.go` and `closing_stock*.go` is **not yours**.
- **NEVER `git add -A`.** Stage explicit paths and grep the staged list for `zz_`, `scratch`, `probe`,
  `.bak`, `.orig`. A reviewer's `zz_adversarial_authz_probe_test.go` was caught only at staging.
- **`loadPlan` in `internal/platform/testdb` reads the LIVE database**, so a new tenant table fails the
  census with "the fixture seeds X, which the delete plan does NOT cover" until you `make migrate`. The
  message points at `plan.go`; the actual fix is a migration run.
- **The `testdb` fixture may hold only ONE row per table** (`seedCompany` fatals on a second).
- **Money is `decimal.Decimal` server-side, fixed-4 strings on the wire, scaled integers client-side.**
  **Never float64 / ParseFloat / Number() / parseFloat.** Grep every diff. `decimal.NewFromString`
  accepts what `ParseFloat` would silently round — and `ParseFloat` accepts `"NaN"` and `"Inf"`, which
  panic inside the decimal package as an opaque 500.
- **Tolerances are how a real error hides for a year. Reconcile to the paisa.**
- Any Go test that creates a company MUST call `testdb.Cleanup(t, companyID)` or
  `internal/testhygiene` **fails the build**.

---

## 10. WHAT ONLY A HUMAN CAN DO

| # | Ask | Unblocks |
|---|---|---|
| 1 | **A real Tally export** (XML, any version) | **T32 — the commercial blocker.** A business cannot switch if it cannot bring its books. What exists has never met a real file |
| 2 | **A JRE + Protean's RPU zip** | lets us say the TDS return is *validated*, not merely generated |
| 3 | **One real business day on the app** | the highest-value item on this list, and unmeasurable any other way |
| 4 | **IRP / GSTN / e-Way Bill sandbox credentials** (needs a GSTIN) | **T5 for real** — the payload is already correct — and T18's portal half |
| 5 | **EPF/EDLI administrative-charge rate notification**, if issued since 2026-07-30 | ~₹500/establishment/month accruing unbooked. **Do NOT substitute `S.O. 2701(E)`** — that is *inspection* charges, a different levy |
| 6 | **The published text of CGST Rule 138 and 138(10)** into `docs/statutory/` | lets T5's threshold and validity table be **cited** rather than carried as secondary material. CBIC fails TLS verification from here |
| 7 | **Is a cost report something a data-entry clerk should see?** 0072 says no, deliberately | a one-line ACL decision either way, and a business call rather than a technical one |
| 8 | **Delete the twelve spent handoffs** (§11) | a handoff nobody can trust costs a session to re-verify |

---

## 11. THE SPENT HANDOFFS — and the one thing to do about them

**Live:** **this file**, `PLAN-TALLY-PARITY.md` (§2.5 migration numbers, §2 working rules, §4 Tier 2),
`RESEARCH.md` (Tally keystrokes, citable by line), `docs/design/*` (per-track models),
`docs/statutory/*` (primary sources — **Form 140 only**).

**SPENT — kept only for their reasoning, and safe to delete:**
`HANDOFF-TRACKS-2-3.md` · `HANDOFF-T1-PRINTING.md` · `HANDOFF-PARITY-SPRINT.md` ·
`HANDOFF-CLOSING-THE-GAP.md` · `HANDOFF-SESSION-T29-IMPORT.md` · `HANDOFF-FEATURE-LOCK.md` ·
`HANDOFF-NEXT-THREE.md` · `HANDOFF-TIER-ONE.md` · `HANDOFF-AFTER-T6.md` ·
`HANDOFF-CLEAR-A-TIER.md` · `HANDOFF-NEXT-T3-T5-PARALLEL.md`

That is **eleven superseded files**, and the sprawl is itself the finding each one recorded: every one
re-derived context the last already held. This file is deliberately ONE file that folds all of it, and
the way to keep it that way is to **update this file in place** rather than write a twelfth.

⚠️ **THEY ARE STILL ON DISK, DELIBERATELY.** Deleting them is a judgement about what documentation a
team keeps, and it belongs to whoever owns the repo. **`git rm` them in one commit when you agree** —
the list above is exactly what to remove.

### Left in the dev database on purpose

- **`T6 Drive Co`** (`9466b85c-…`) — three TDS deductions, two challans, a deductor profile.
- **`tally_freshproof`** — a scratch *database* predating all of this. Harmless; drop it.
- The **demo company has no cost data, no TDS data and no orders**; none is seeded (§4.6).

---

## 12. DEFINITION OF DONE — for whoever closes Tier 1

1. **The T3 and T5 FeaturesScreen rows are `on`**, on **BROWSER evidence**, not a green suite. The T2
   allocation row stops saying `server`.
2. `make verify` exit 0 **and** `pnpm exec tsc --noEmit` clean (**different gates**).
3. backend **≥2292 pass, 0 SKIP, 0 FAIL** (verbose census); frontend **≥614 / 47 files**;
   `verify:demo` **33/33 run ALONE** with insightd up, re-pinned deliberately if a figure moves — and
   **the change explained**.
4. ⛔ **THE REACHABILITY CHECK, for every new feature: name the exact HTTP request that exercises it
   end to end, and assert it in a test that reads the result back out through the OUTERMOST surface,
   with no SQL in the fixture that reaches around the API.** T2 passed every other bar on this list
   while being unreachable from every client. **This item exists because nothing else on the list
   catches that.**
5. Every new tenant table in `buildFixture` (`internal/platform/testdb/testdb_test.go`) or the teardown
   census fails **by name**.
6. Every new screen carrying **all 13** registrations including `SCREEN_ACTIONS`, with a **real-shell
   keypress test**.
7. `PLAN-TALLY-PARITY.md` §2.5 updated for every consumed block, **and §6 of this file obeyed** for
   anything touching the ACL seeder.
8. Every safety-critical invariant **mutation-proved**, and said so in the commit.
9. **This file updated in place** — not a thirteenth handoff.
