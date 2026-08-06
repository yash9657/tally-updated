# HANDOFF — THE LIVE ONE. Start here, from cold.

> **Written 2026-08-05, updated in place at `7e565a3`.** This file replaces the ELEVEN `HANDOFF-*.md`
> files that preceded it (§11 lists them; `ls` confirms eleven). **Every other `HANDOFF-*.md` in this repository is SPENT** — see §11 for the
> list and for the one thing to do about them.
>
> **Read §1 → §2 → §3. Then act on §4.** Everything after §6 is reference you will need exactly when
> you hit it, not reading for now.

---

## 1. THE ANSWER TO THE QUESTION YOU ARE ABOUT TO ASK

**Is Tier 1 done? NO — ONE row short, and it is T5. Everything else on the tier is `on`.**

The `FeaturesScreen` (`frontend/src/screens/FeaturesScreen.tsx`) is this project's parity ledger, and
T7's whole purpose was to make it honest. It is the authority, not this file. Measured at `a86283b`:
**29 rows `on`, 12 `off`, 5 qualified** (`always`). ⚠️ **There is no longer a single `server` row** —
the state still exists and is still right to have, but nothing is currently in it.

| Tier 1 track | Row | State |
|---|---|---|
| T1 Printing | several | ✅ `on` |
| T2 Cost centres | "Cost centres and cost categories — the masters and both reports" | ✅ `on` |
| T2 (cont.) | "Cost centre allocation during voucher entry" | ✅ **`on` — browser-verified, see §1.2** |
| **T3 Orders + BOM** | "Order processing (Sales / Purchase Orders) and Bill of Materials" | ✅ **`on` — browser-verified, see below** |
| T4 Bank reconciliation | "Bank reconciliation" | ✅ `on` |
| **T5 e-Way Bill** | "e-Way Bill" | ⛔ **`off` — no route, no UI. THE LAST TIER-1 ROW** |
| T6 TDS/TCS | "TDS / TCS" | ✅ `on` |
| T7 Features screen | — | ✅ done |

**So: do NOT start Tier 2.** ONE row is left, and it is `off` for the reason that has been the most
expensive lesson in this repository's history:

> ### ⛔⛔ T5's BACKEND IS BUILT, TESTED AND REACHABLE. IT HAS NO SCREEN AND NO ROUTE.
> A keyboard-only operator cannot raise an e-way bill. **The remaining Tier-1 work is a store, a
> route and a screen**, and §4 is that work — but read §4.2 first, because the architecture is not
> the one T3's was and the obvious approach is forbidden by archtest.

### 1.1 What the session at `8db0ffd..7e565a3` closed, and how it is known

T3 went from `off` to `on` across three commits, and the row flipped on **BROWSER EVIDENCE** rather
than a green suite — which §12 requires and which is the only thing that catches the defect class
this repo keeps re-learning. Driven in a real browser against real `cored`, with a virtual WebAuthn
authenticator over CDP so the step-up completed (§9.7's recipe, **verified working again**):

| Keystroke | What actually happened |
|---|---|
| `Ctrl+F8` | the Sales Orders register opened |
| `Alt+C`, `Ctrl+A` | **SO-BROWSER-1 recorded through the passkey**, read back with the server-assigned line 1 |
| `Alt+F9`, `Ctrl+A` | a Receipt Note — and the picker **offered no orders**, because the only open order is a SALES order and a receipt discharges PURCHASE orders |
| `Alt+F8` + link 8 | a Delivery Note linked to the order; the qty field was **empty after picking** |
| Sales Order Book | **Ordered 20 / Delivered 8 / Outstanding 12 / ₹6,000** |
| `Alt+C`, `Alt+X` | a recipe (10 yields 3) exploded for 9 gave **exactly 30.0000** |

⛔ **AND THE BROWSER FOUND A BUG NOTHING ELSE COULD.** The order book heading rendered `As at ·` with
nothing after it: the server does **not** substitute its own today when no `as_on` is given — it
returns `as_on: ""` and reports the position from every order ever placed. That is now the third
defect of this exact shape found by driving the app rather than reading it (the F2 period dialog,
Alt+O's dead binding, this). **A default branch that produces a plausible-looking string is invisible
to every test that only asserts the sentences it expects.** Worse, and worth naming: **the test I had
written myself certified the bug** — it asserted `As at ${book.as_on}` and passed on the empty string.

### 1.2 The T2 allocation row, and the live bug it walked into

Closed at `a86283b`, browser-verified the same way: `F5` → HDFC Bank → Electricity
10,000 → **allocate** → Department/Sales 6,000 showed *"6000.0000 of 10000 — 4000.0000
left"* plus the short-axis refusal; a second split to Admin via the **rest** button
filled 4000.0000, the refusal cleared, and `Ctrl+A` saved through the passkey. The Cost
Centre Summary then read **Admin 44,000 / Sales 66,000** and reconciled.

⛔ **THE OFFSET IS THE HAZARD, AND IT IS PROVED IN THE DATABASE, NOT IN A TEST.** In
single entry the postings array is `[the account paid from, ...the particulars]`, so the
particular at row 0 is posting **1**. The allocations landed on **Electricity (Dr
10,000)** — not on HDFC Bank. With the offset at 0 they would have attached to the bank
credit leg, on a voucher that balances perfectly and a Cost Centre Summary that foots.
Nothing downstream can detect that. `buildCostAllocations` therefore takes
`primaryLegCount` as a REQUIRED parameter with no default.

> ### ⛔⛔ AND THE BUG IT EXPOSED, WHICH WAS LIVE AND WHICH NO TEST COULD SEE
>
> `ensureLedger` (`screens/vouchers/gstVoucher.ts`) called `createLedger`
> **unconditionally**, on an assumption written in its own doc comment: *"createLedger
> is idempotent server-side (by name within a company)"*. **That was false.**
> `INSERT INTO ledger` has no `ON CONFLICT` and `ledger_company_id_name_key` is a plain
> UNIQUE index on `(company_id, name)`, so creating an existing ledger is a raw
> unique-violation.
>
> **On a fresh page load the memo is empty, so keying a Payment / Receipt / Contra
> against any ledger the company ALREADY HAD — the normal case, and every case after the
> first day — failed with `masters: insert ledger: ERROR: duplicate key value violates
> unique constraint`.** It only ever appeared to work when the memo was warm from an
> earlier post in the same page session.
>
> **NOT ONE OF THE 705 TESTS COULD CATCH IT**, because they all run against the mock,
> whose `createLedger` mints a fresh id per call. That is the lesson to carry: *a mock
> that is more permissive than the server hides a whole class of defect no amount of
> coverage reaches.* Fixed to resolve-then-create, pinned by four tests against a client
> that refuses duplicates the way cored does, and mutation-proved.

---

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

### 2.1 The gate baseline at `a86283b` — re-measure, do not assume

| Gate | Value |
|---|---|
| backend `go test ./... -count=1 -v` | **2293 pass / 0 SKIP / 0 FAIL** |
| frontend `pnpm run test:run` | **705 pass / 52 files** |
| `pnpm exec tsc --noEmit` | clean — ⛔ **a SEPARATE gate from `make verify`** |
| `make verify` | exit 0 |
| `verify:demo` | **33/33**, run **ALONE**, needs cored **and** insightd up |
| `gofmt -l` on every file you touch | clean |

⚠️ **`verify:demo` TAKES THE COMPANY ID AS AN ARGUMENT** and prints a usage line without it:
`pnpm run verify:demo "$(cat frontend/.demo-company-id)"`. Bare `pnpm run verify:demo` exits 2, which
is easy to misread as a failing gate.

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
| n−3 | T6 | 4, with 3 sequential barriers | ~1.7M |
| n−2 | T2 + the shared T3 contract | 2, zero barriers | ~1.3M |
| n−1 | T3 backend end-to-end + T5 core | 0 — solo | 0 |
| **n (this one)** | **T3 frontend end-to-end (5 screens + the fulfilment picker) AND T2's allocation UI — two rows flipped, both browser-verified** | **0 — solo** | **0** |

**Three observations now, and the third is new.** First: plan for roughly one track per session — this
session delivered exactly one and it was full. Second: the last two sessions each delivered a whole
track **with no subagents at all**, because both were long serial chains of decisions in contended
files; **fan out for genuinely disjoint files, go solo down a dependency chain.** Third, and it is the
one to plan around: **the frontend half of a track is not smaller than the backend half.** T3's
frontend was five screens × fourteen registration points, plus a picker that had to be wired into two
existing grids, plus a real backend defect the picker exposed. It took a full session on its own.

---

## 4. ⛔⛔ THE WORK, IN ORDER. THIS IS THE PART YOU ACT ON.

Everything here is Tier 1. **Do not start Tier 2 (§5) until item 1 is done and the T5 FeaturesScreen
row is `on` on BROWSER evidence.**

### 4.1 THE BAR, restated because it is the whole point

> **A feature is not shipped when its tests pass. It is shipped when a human can reach it.**
>
> This has been learned FOUR times now, at increasing cost:
> 1. `voucherReq.ItemLedgerID` — every item invoice the F8/F9 grid could send came back 422; the
>    generator was reachable only from Go tests.
> 2. **T2's cost allocations** — the validation, the hashed payload entry and the row insert were
>    built, reviewed, and covered by unit AND live-Postgres tests **while no HTTP body could carry an
>    allocation.** Nothing failed. Both report routes answered 200 and were *correct*: they accurately
>    reported that nothing had been allocated, because nothing could be.
> 3. **T2 again, one layer up** — the Go wire was fixed and `VoucherRequest` in
>    `frontend/src/api/types.ts` was not. So a browser still could not construct one, and all 597
>    frontend tests passed.
> 4. **T3, and this one ran a whole release** — the order tables, the fulfilment links, the
>    over-fulfilment trigger, both order books, the ACL migration and all fourteen client methods
>    shipped and were tested while **no screen existed**. The order book reported every commitment as
>    fully outstanding forever, *correctly*, because nothing could discharge one.
>
> **THE RULE: a feature is not reachable until the field exists on EVERY wire between the operator and
> the table — Go struct, TS type, and the form that fills it.**
>
> **THE PATTERN THAT CATCHES IT:** `backend/internal/server/order_fulfilment_http_test.go`. It POSTs
> as JSON and reads the figures back out of a REPORT over HTTP, so the assertion spans
> wire → ledger → payload → table → report → wire, with nothing stubbed and **no SQL in the fixture
> that reaches around the API**. It is mutation-proved in both directions. Copy this shape for every
> new feature.
>
> ⚠️ **AND ITS TWIN, WHICH IS NEWER AND WAS MISSING: THE READ-BACK.**
> `TestTheVoucherGETCarriesItsOrderLinksSoAnAlterCannotSilentlyReopenTheOrder` in the same file. A
> feature can be perfectly reachable on the way IN and destroy data on the way OUT — see §4.3.

### 4.2 ⛔⛔ ITEM 1 — T5's STORE, ROUTE AND SCREEN. THE LAST TIER-1 ROW.

The rules-and-payload core is done and it is good (`internal/statutory/ewaybill.go`, `rule138.go`):
`Assess` returns the Rule 138 conclusion **with its reasoning**, `BuildEWayBill` emits the NIC payload
subset, and `EWayBillDisclosures` assembles the block that must reach paper. What is missing is a
store, a route and a screen.

> ### ⛔ READ THIS BEFORE YOU TAKE MIGRATION 0047. THE ARCHITECTURE IS NOT T3's.
>
> **`internal/archtest` pins `internal/statutory` to the EMPTY allowed-import set**, and the edge runs
> the way you do not want: **nothing in `internal/` imports `statutory` either** — only
> `cmd/statutoryd` does. `statutoryd` is a **separate deployable** that talks to cored ONLY over HTTP.
>
> So `cored` **cannot call the payload builder**. That is the decision to make first, and there are
> two honest shapes:
>
> 1. **Mirror the e-invoice flow.** `cmd/statutoryd/main.go`'s `handleEInvoice` reads the voucher
>    snapshot from cored over HTTP, generates, and the result is pinned back to cored via
>    `POST /vouchers/{id}/external-reference` (migration 0009's `voucher_external_reference`, which is
>    CHECK-constrained to `IRN`/`FILED_GSTR1` — **so it needs a new kind, or a new table**). Add
>    `POST .../vouchers/{voucherID}/e-way-bill` to statutoryd beside the e-invoice route. This is the
>    shape the codebase already has and the one archtest was written for.
> 2. **Move the payload builder** out of `statutory` into a package cored may import. This is a bigger
>    change and it fights an architectural decision that was made deliberately — do not do it without
>    a stated reason.
>
> **Item 1 is blocked on choosing between those, not on typing.** Whichever you choose, say why in the
> commit, because the next person will ask.

Then, and only then:

- A **cored-side store** for the local reference, the payload and the validity — **0047/0048 are
  reserved for exactly this and are still free** (they are TABLE numbers only; see §6).
- A route.
- A screen that **renders the `disclosures` block**. That block is not decoration: it is the only thing
  standing between a printed sheet and a driver believing the consignment is covered. `EWayBillResponse`
  in `frontend/src/api/types.ts` already carries it and says so.
- ⛔ **NEVER LABEL `local_reference` AS AN "e-Way Bill Number".** It is not an EBN. An EBN is issued by
  NIC and this deployment cannot reach NIC.
- ⚠️ **T5 IS NOT T18.** An EBN is just an identifier NIC allocates, so T5 becomes real the day
  credentials exist with **no change to the payload builder**. T18's QR code is signed with the Income
  Tax Department's private key and is permanently unshippable (§5). The two gaps look alike and are not.

### 4.3 ⛔ ITEM 2 — THE READ-BACK AUDIT. Cheap, and it is a DATA-LOSS class.

**T3 was about to ship a defect that silently un-delivers an order, and the shape generalises.**
`order_fulfilment` keys on `(voucher_version_id, line_no)`, so **an ALTER RE-STATES**: whatever the
body omits is gone. Meanwhile `GET /vouchers/{id}` did not return the links at all. Open a delivered
delivery note, fix a typo in the narration, save → **the order silently re-opens.** Nothing refuses it,
because zero links is a legal state: no trigger fires, no total stops adding up, and the only trace is
an order book reporting a commitment the company already met.

Fixed for fulfilments AND for `cost_allocations` (which answers the old §4.7 item 8 — and the answer
was worse than feared: `ledger.VoucherView` had carried them since T2 **with a doc comment explaining
at length why omitting them destroys a departmental split**, and `voucherFullJSON` dropped them).

> ### ⛔ THE AUDIT TO RUN, because this is now FOUR occurrences (postings, inventory, cost allocations, fulfilments):
> **For every table keyed on `voucher_version_id`, ask: does `voucherFullJSON` emit it?** If an ALTER
> re-states a table and the GET does not return it, the ordinary round trip DELETES it. Grep the
> migrations for `voucher_version_id` and check each one off against
> `internal/server/server.go`'s `voucherFullJSON`. `bill_allocation` is emitted; `stock_movement` is;
> the two just fixed are. **Verify the rest, and write down which ones you checked.**

### 4.4 ✅ T2's ALLOCATION UI — DONE at `a86283b`. What is left around it.

The row is `on` (§1.2). Two gaps remain, and both are worth knowing before you touch a
voucher form:

- ⛔ **A JOURNAL CANNOT BE KEYED FROM SCRATCH IN THIS BUILD AT ALL.** `journal` is
  `entry: "double"`, and VoucherScreen's `double` branch renders **no posting grid** —
  the form falls through to `null` after Date / No. / Party / Narration.
  `buildPlainRequest` forwards `f.postings`, which is only ever populated from a LOADED
  voucher (alter) or an OCR draft. So F7 opens a form that cannot produce a voucher.
  This is pre-existing, it is not what T2 was blocked on, and it is now stated on the
  cost masters screen rather than left to be discovered. **It is probably the single
  biggest remaining hole in accounting parity** — a Journal is how every adjustment,
  provision and reclass is keyed.
- Allocation on an ITEM INVOICE is refused outright **and always will be** — its
  postings are generated inside the server's transaction and index 0 is the party leg.
  That is a decision, not a gap; the screen says so.

### 4.5 ⛔ ITEM 3 — A DOUBLE-ENTRY POSTING GRID, SO A JOURNAL CAN BE KEYED AT ALL

**F7 opens a form that cannot produce a voucher.** `journal` is `entry: "double"` and
VoucherScreen's `double` branch renders no posting grid — after Date / No. / Party /
Narration the panel chain falls through to `null`. `buildPlainRequest` forwards
`f.postings`, which is only ever populated by hydrating a LOADED voucher or an OCR
draft. Nothing on the screen can add a posting.

This was found while looking for somewhere to put the allocation control (§1.2) and it
is pre-existing, not a regression. It matters more than its size suggests:

- **A Journal is how every adjustment, provision, depreciation and reclass is keyed.**
  It is the voucher an accountant uses most at period end, and this build cannot key one.
- The F11 row "Double-entry vouchers (Contra, Payment, Receipt, Journal, Sales,
  Purchase)" reads `on`, which is **overstated for Journal specifically**. Correct that
  row in the same change (§9.1) — do not flip it to `off`, because five of the six are
  genuinely reachable; say which one is not.
- It also unblocks allocation on a journal, which currently has to arrive over HTTP.

⚠️ **The single-entry panel is NOT the model to copy.** It infers the primary leg and
puts the SUM on it; a double-entry grid states both sides and must refuse an unbalanced
body in the form (the server refuses it too, but a 422 does not survive the round trip
in a shape the operator can act on). ⛔ And `posting_seq` for an allocation on such a
grid is **0-based with NO offset** — `buildCostAllocations(rows, 0)` — because the rows
map one-for-one from index 0. That parameter exists for exactly this second call site.

### 4.6 ITEM 4 — SEED T3 AND T2 INTO `seed:demo`

`frontend/scripts/seed-demo.mts`. Nothing seeds cost data, TDS data or orders, so **four screens render
empty against a fresh demo company** and cannot be demonstrated. One axis, two centres, one allocated
voucher, one order and one partial delivery would fix all of it. Same unclaimed work T2, T3 and T6 all
left behind.

⚠️ **THE DEV DATABASE NOW HAS SOME OF THIS AND THE SEED SCRIPT STILL DOES NOT** — see §11's residue
list. That is the worst of both worlds for anyone who reasons from what they see on screen, so this
item is more urgent than it was.

⚠️ `seed-demo.mts`, `verify-demo.mts` and `corpora/validate.py` are **CO-OWNED**. ADD only, and
re-read immediately before each edit.

### 4.7 SMALLER, REAL, AND EACH WORTH DOING

| # | What | Why |
|---|---|---|
| 5 | `ReviseLineQty` on the wire | The service method and 0042's trigger exist; no route calls it, so a committed quantity cannot be corrected from a client. The order register SAYS SO on its face rather than hiding it |
| 6 | `VersionView` does not project cost allocations | The edit log SIGNS them but does not SHOW them: an auditor opening an altered pay run sees two versions that render identically. ⚠️ It does not project `fulfilments` either, and that is now the same gap twice |
| 7 | The pre-flight allocation refusal names the axis by raw UUID | Strictly *less* usable than the DB trigger message it exists to pre-empt. Resolve names at the in-transaction call site, which already holds the tx |
| 8 | The two cost reports disagree about an off-axis allocation | The category summary excludes it and reports it separately; the centre summary silently includes it. Neither says so |
| 9 | Fold `tsc` into `make verify` | ~10 minutes, and it closes a whole class of "green but broken". **Still not done, and it caught a real error this session** — `printTarget.ts`'s exhaustive `Record<ReportName, string>` failed only under `tsc`, never under `make verify` |
| 10 | No tracking numbers | RESEARCH.md:622 — Tally stitches Delivery Note ↔ Sales invoice with a **Tracking No.**, and creates "bill pending" entries when goods and bill arrive separately. T3 links a voucher to an ORDER but not to another VOUCHER. The order register states this gap |

## 5. TIER 2 — WHAT IT IS, AND THE ONE THING NOT TO GET WRONG IN EACH

**Do not start these until §4 item 1 (T5) is done and its row is `on`.** `PLAN-TALLY-PARITY.md` §4 is the authority;
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

### 7.2 A new SCREEN needs 14 — it was documented as 13, and the 14th was found by `tsc`

The two whose file paths lie: `ScreenKind` is in `frontend/src/keystroke/types.ts` (**not**
`screenRegistry.tsx`), and `actionLabel()` is in `keystroke/keymaps/tallyprime.ts` (**not**
`AppShell.tsx`).

⛔ **THE FOURTEENTH: `frontend/src/screens/printTarget.ts` holds `REPORT_SLUGS`, a
`Record<ReportName, string>`** mapping every report name to its server print slug. Adding a name to
`REPORT_NAMES` makes that record non-exhaustive and **`tsc` refuses to compile** — which is how it was
found, and it is a good tripwire rather than a nuisance. ⚠️ **`make verify` DOES NOT RUN `tsc`**
(§2.1), so this error is invisible to the gate most people run. Map the slug even when the server
DEFERS the report and answers 501: the TDS register and both order books are mapped-and-deferred on
purpose, so `Ctrl+P` says the layout does not exist instead of doing nothing. An absent slug is
indistinguishable from a key that quietly failed.

⚠️ **A `report:NAME` FRAME IS MUCH CHEAPER THAN A `ScreenKind`, and it is the right choice more often
than the list above suggests.** The two order books are registered as `report:Sales Order Book` /
`report:Purchase Order Book` and inherit the report keymap, the report button bar, the title, F2 and a
place in both Go To and Display More Reports **for free**. Pick a ScreenKind only when the screen
carries a create FORM (which needs its own keymap table to hold the voucher keys back) or renders a
shape no report screen does. `cost-centres`, `tds-challans`, the two order registers and `boms` are
ScreenKinds for that reason; the four order/cost REPORTS split on it.

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
> `TDSChallansScreen.test.tsx`'s `describe("a screen-owned key is actually ROUTED by the shell")`, and
> `OrderRegisterScreen.test.tsx` takes it further: **every test in that file** drives the shell and
> presses a real combo, because there is no other kind worth writing for a feature whose whole failure
> mode was being unreachable. Deleting `createOrder`/`closeOrder` from `SCREEN_ACTIONS` fails 12 of
> them **and nothing else in the repo** — that is the mutation proof, and it was run.

### 7.3 Two things about the keymap that save ~120 lines a screen

- **`voucherKeysHeldBack(screen, what)`** in `keymaps/tallyprime.ts` generates the twelve
  voucher-key refusals a form-carrying screen needs. It exists because the block was ~120 lines of
  near-identical prose per screen and **the fifth copy is where copies start to drift** — a paste that
  keeps the previous screen's noun tells the operator the keys are held back on a screen they are not
  on. `tds-challans` and `cost-centres` are still hand-written (they predate it) and that asymmetry is
  documented at the helper; new screens use the helper.
- ⚠️ **CHECK WHETHER THE KEY YOU WANT IS ALREADY BOUND TO `unavailable` WITH A STALE NOTE.** `Ctrl+F8`
  and `Ctrl+F9` said "Order processing is not built yet" — they are Tally's real Sales/Purchase Order
  keys (RESEARCH.md:400, :614, :616, :2145–2146). `buttons.test.ts`'s "features this build lacks" loop
  is what forces the note to be corrected instead of rotting: its example has now been re-pointed
  **twice** by features shipping (Alt+F7, then Ctrl+F8). Also correct `F10`'s note — it lists what the
  "Other Vouchers" menu would contain.

### 7.4 ⛔ THE MOCK IS A REAL DELIVERABLE AND MUST REFUSE WHAT THE SERVER REFUSES

*(§9.4 names the two KINDS of mock-fidelity defect and the control that catches the second.)*

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
  of cost allocations (§4.6 item 6). Borrowing a true sentence into a place where it is false is the
  same defect as writing a false one.
- *"cost_allocations is reachable from a client"* — true of Go, false of TypeScript, for a whole
  feature.
- *"Order processing is not built, and not modelled at all rather than modelled wrong"* — the F11 note,
  **false for a whole release** while the tables, both books and fourteen client methods shipped. It
  read as a considered decision, which is what made it survive.
- *"Order vouchers are not built"* — `F10`'s keymap note, and *"Order processing is not built yet"* —
  `Ctrl+F8`/`Ctrl+F9`'s. All three were corrected by one assertion in `buttons.test.ts` that forces
  the claim to be re-pointed when a feature ships.
- *"NO SCREEN IN THIS BUILD ALLOCATES A POSTING"* — `CostCentresScreen.tsx`'s closing paragraph.
  **Corrected at `a86283b`**, in the change that made it false. It now says where allocation happens
  AND where it still is not reachable (a Journal, and an item invoice by design).
- ⛔ *"createLedger is idempotent server-side (by name within a company)"* — `ensureLedger`'s own doc
  comment, and **the whole helper was built on it.** It was never true. This is the most expensive
  false claim found so far, because it was a claim about ANOTHER LAYER's behaviour: nothing in the file
  that stated it could contradict it, and the mock agreed with it. **When a comment asserts what a
  different service does, that assertion needs a test against that service — or it is a guess with
  authority.**
- *"Double-entry vouchers (Contra, Payment, Receipt, Journal, Sales, Purchase)"* reads `on` on F11, and
  is **overstated for Journal**: F7 renders no posting grid, so a journal cannot be keyed from scratch
  (§4.5). Still uncorrected — correct it when you build the grid.

**And prefer a refusal that takes a company and a period over one that takes no arguments** — the
latter can only make claims about the software, and those go stale silently.

⚠️ **A TEST CAN CERTIFY A BUG, and mine did.** The order book's heading test asserted
`As at ${book.as_on}` and passed on the empty string, so it locked in a heading reading "As at ·". A
test written from the same misunderstanding as the code agrees with it. **The only thing that caught it
was a screenshot.**

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

**The newest example is worth copying because the control is the INVERSE of the assertion.** The
round-trip test asserts that altering a narration leaves the order's fulfilment intact. On its own that
is consistent with links being immune to an alter — in which case the test proves nothing. So it alters
a second time with the links **deliberately omitted** and asserts the order DOES re-open. The control
establishes that the behaviour the first half depends on is genuinely dangerous.

### 9.4 ⛔ THE MOCK IS WHERE FIDELITY DEFECTS HIDE, AND THERE ARE THREE KINDS

`api/orders.test.ts` exists because the mock disagreed with the server twice in one session. Three more
have been found since. The second kind is the one no TYPE can catch; the third is the one no TEST can:

1. **A REFUSAL THE MOCK DID NOT MAKE.** The mock ignored `fulfilments` entirely — it did not reject
   them, it **dropped them silently**, so a screen keyed a link, got a 201, and the order book never
   moved. A developer driving the mock would conclude their own code was broken.
2. **A SHAPE THE MOCK GOT WRONG.** `party` and `stock_item` returned **the id that was sent in**, where
   the server LEFT JOINs the master and returns `COALESCE(name, '')`. Both fields are `string`, so
   `tsc` is clean and every quantity assertion stays green **while the operator is shown a UUID**.

**When you add a mock method, diff its refusals AND its shape against the handler**, not against the
type you wish existed. And include the control the fix needs: an id that resolves to NOTHING must come
back `""`, never as the id — a fallback to the id would put the UUID back on screen in exactly the case
an operator can least explain, while making the happy-path assertion pass.

> ### ⛔⛔ AND THE THIRD KIND, WHICH IS THE WORST: A MOCK MORE PERMISSIVE THAN THE SERVER
>
> `ensureLedger` called `createLedger` unconditionally for two releases. `INSERT INTO
> ledger` has no `ON CONFLICT`, so against cored that is a unique-violation — and
> against the MOCK, whose `createLedger` mints a fresh id per call, it is a success.
> **Every one of the 705 tests passed while Payment / Receipt / Contra could not save a
> voucher in a browser** against any ledger the company already had.
>
> This is not the "shape" defect or the "missing refusal" defect. Those are about a
> mock that answers differently. This is about a mock that answers *more generously*,
> and it is invisible by construction: no test written against it can fail.
>
> **The technique that catches it, and it is cheap:** for any client helper whose
> correctness depends on a server REFUSAL, write the test against a hand-rolled client
> that makes that refusal — see `gstVoucher.test.ts`'s `strictClient`. Four tests, and
> they would have caught a bug that shipped and survived a full session's browser
> driving on other screens.
>
> **The smell to grep for:** a helper named `ensure*` / `upsert*` / `getOrCreate*` that
> only ever calls a CREATE route. If it never reads, it is not an ensure.

### 9.5 The all-zeros UUID parses, and `uuid.Nil` is everyone's sentinel

`uuid.Parse("00000000-...-0")` **succeeds**. Two handlers treated it as "absent", so an explicit
all-zeros `parent_id` **lifted an existing child out of its hierarchy** (with a 200 and a truthful
read-back) and an all-zeros filter silently returned **every** row.

**Any handler whose "absent" sentinel is `uuid.Nil` must refuse an explicit zero.** Grep for `uuid.Nil`
comparisons after a `uuid.Parse`.

### 9.6 Never claim a validation that was not performed

Unreachable from here: the **FVU** (no JRE), the **GSTN portal**, the **NIC e-Way Bill API**, the
**IRP**, the **EPFO/ESIC portals**, the **TIN `.csi`** file, and **CBIC's own rule repository** (TLS
chain verification failure, 2026-08-05). Ship the deterministic fake plus the recorded schema and
**say so on the artefact's face**. `internal/statutory/rule138.go` is the worked example of how:
citation, grade, sources, caveat, all travelling with the value.

### 9.7 Operational

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
| 8 | **Delete the ELEVEN spent handoffs** (§11 — `ls HANDOFF-*.md` says eleven, and this row said twelve) | a handoff nobody can trust costs a session to re-verify |

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
- ⚠️ **THE DEMO COMPANY NOW HAS T3 DATA, PUT THERE BY A BROWSER AND NOT BY THE SEED SCRIPT.** Measured
  at `7e565a3` in `71fe4e15-…`: one sales order **`SO-BROWSER-1`** (20 MS Angle @ 500, 8 delivered, 12
  outstanding), one BOM **`Standard`** (10 MS Angle yields 3 SS Sheet), one `order_fulfilment` link, and
  a Receipt Note / Delivery Note pair that moved **+50 / −8 MS Angle** — so this residue IS IN CLOSING
  STOCK, which feeds the Balance Sheet. `verify:demo` still passes **33/33** because its assertions are
  differential rather than pinned constants; if you ever pin an absolute figure, this residue is baked
  into it. **The honest fix is §4.5 (seed it properly), not deleting it** — a fresh database still has
  none of this, which is the worst of both worlds for anyone reasoning from what is on screen.
- The demo company **does** now have cost masters (a `Department` axis with `Sales` and `Admin`) and
  allocated vouchers — including one keyed in a browser at `a86283b` (Electricity 10,000 split
  6,000/4,000), on top of pre-existing HTTP-posted allocations. The Cost Centre Summary reads Admin
  44,000 / Sales 66,000 over FY 2026-27 and reconciles. **Still none of it is in the seed script**
  (§4.6). No TDS data is seeded either.

---

## 12. DEFINITION OF DONE — for whoever closes Tier 1

1. **The T5 FeaturesScreen row is `on`**, on **BROWSER evidence**, not a green suite. (**T3 is DONE —
   §1.1. The T2 allocation row is DONE and no longer says `server` — §1.2. T5 is the only Tier-1 row
   left.**)
2. `make verify` exit 0 **and** `pnpm exec tsc --noEmit` clean (**different gates** — and `tsc` is the
   one that caught a real error this session, see §7.2).
3. backend **≥2293 pass, 0 SKIP, 0 FAIL** (verbose census); frontend **≥705 / 52 files**;
   `verify:demo` **33/33 run ALONE** with insightd up **and the company id as an argument** (§2.1),
   re-pinned deliberately if a figure moves — and **the change explained**.
4. ⛔ **THE REACHABILITY CHECK, for every new feature: name the exact HTTP request that exercises it
   end to end, and assert it in a test that reads the result back out through the OUTERMOST surface,
   with no SQL in the fixture that reaches around the API.** T2 passed every other bar on this list
   while being unreachable from every client. **This item exists because nothing else on the list
   catches that.**
4a. ⛔ **THE READ-BACK CHECK, and it is NEW because item 4 does not catch it.** For anything keyed on
   `voucher_version_id`: assert that `GET /vouchers/{id}` returns it, and that an ALTER rebuilt from
   that response preserves it — **with the control that an alter omitting it DESTROYS it** (§4.3, §9.3).
   A feature can pass item 4 perfectly on the way in and silently delete data on the way out. T3 was
   one commit from shipping exactly that, and it would have presented as a warehouse problem.
5. Every new tenant table in `buildFixture` (`internal/platform/testdb/testdb_test.go`) or the teardown
   census fails **by name**.
6. Every new screen carrying **all 14** registrations including `SCREEN_ACTIONS` and
   `printTarget.ts`'s `REPORT_SLUGS` (§7.2), with a **real-shell keypress test** that is
   **mutation-proved** against the `SCREEN_ACTIONS` entry.
7. `PLAN-TALLY-PARITY.md` §2.5 updated for every consumed block, **and §6 of this file obeyed** for
   anything touching the ACL seeder.
8. Every safety-critical invariant **mutation-proved**, and said so in the commit.
8a. ⛔ **ANY CLIENT HELPER WHOSE CORRECTNESS DEPENDS ON A SERVER REFUSAL is tested against a client
   that MAKES that refusal**, not against the mock (§9.4's third kind). A mock more permissive than the
   server cannot be caught by any test written against it.
9. **This file updated in place** — not a thirteenth handoff.
