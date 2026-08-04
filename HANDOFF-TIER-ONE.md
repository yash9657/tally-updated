# HANDOFF — DESTROY TIER 1, IN ONE SESSION, WITH PARALLEL WORKFLOWS

> **Written 2026-08-04.** Successor to `HANDOFF-NEXT-THREE.md` (state + traps) and
> `HANDOFF-FEATURE-LOCK.md` (the plan of record). `PLAN-TALLY-PARITY.md` §2.5 is the **only**
> authority on migration numbers.
>
> **Mission:** the 15 features `FeaturesScreen.tsx` admits are `off`. Flip every row that *can*
> be flipped, in one session, fanning out across independent subsystems.
>
> **Read in this order:** §0 (what this session can and cannot deliver — it changes the goal) →
> §1 (⛔ the run-first checklist) → §2 (the lane architecture) → §3 (⛔⛔ **the shared-file
> protocol — this is the one that decides whether the session succeeds**) → §4 (per-track
> dossiers) → §5 (gates) → §6 (traps).

---

## 0. ⛔ READ FIRST: "ALL FIFTEEN" IS FOURTEEN, AND ONE OF THEM IS A LIE IF YOU SHIP IT

Fourteen of the fifteen can genuinely land. **One cannot ever**, and an agent that "completes"
it has forged a government signature.

| Row | Verdict |
|---|---|
| **T18 — Rule 48(4) QR on an e-invoice** | **⛔ NEVER SHIPPABLE FROM HERE. DO NOT ASSIGN IT A LANE.** The QR encodes a payload signed with the **Income Tax Department's private key**. Anything we generate that *looks* like one tells a scanner the invoice was registered when it was not. It ships the day we have real IRP credentials and not one hour sooner. The honest deliverable already exists: the placeholder `internal/statutory` prints for IRN. **If a workflow reports T18 done, that is the single most serious possible failure of this session — reject it and read §6.4.** |
| **T5 — e-Way Bill** | ✅ **Ships, in the same shape as IRN.** NIC is unreachable, so ship the deterministic fake + the recorded schema and **say so on the document's own face**. Do NOT claim integration. |
| **T17 — SSO / OIDC** | ✅ Ships against a local/test IdP. Real-IdP verification is a human task. |
| Other 12 | ✅ Ship for real. |

**So the goal is: 14 rows flipped, T18 documented as permanently blocked with the reason on the
FeaturesScreen row itself.** Ending the session with 14 `on` and one honest `off` **is total
success.** Ending it with 15 `on` is a failure that will be discovered by a customer.

> **The standing rule that governs every lane:** `HANDOFF-FEATURE-LOCK.md` §1.1 — **cite it or
> refuse.** Never invent a rate, a code, a field order or a schema. A confidently wrong number
> here is a tax notice with somebody's name on it.

---

## 1. ⛔ RUN-FIRST CHECKLIST — do these before the first workflow

### 1.1 Commit the working tree. It is NOT clean.

The previous session left **6 new files and 11 modified, uncommitted**: the Form 140 emitter
(`backend/internal/compliance/form140/`), migrations **0069/0070**, the return sourcing layer,
the F2 period-classification fix, and doc updates. **Commit or stash before fanning out**, or
six worktrees will branch from a dirty base and the diffs will be unreadable.

### 1.2 Kill any stale server. This cost half an hour last session.

```bash
lsof -nP -iTCP:8087 -sTCP:LISTEN     # who actually holds the port
ps -eo pid,etime,command | grep cored   # ⚠️ etime is AGE. "16:51:35" = 16 HOURS, not 4:51pm
pkill -f "cmd/cored serve"
```
A detached `cored` outlives the session that started it, keeps answering `/healthz` with **200**,
and serves **day-old code** while your new process dies with `bind: address already in use` into
a log nobody reads. It makes correct code look broken — the direction that wastes the most time.

### 1.3 The database is one shared resource, and it will invalidate your evidence

- `tally_pg` on **:5433**, docker. `pg_isready -t 5` **and** `psql -tAc "select 1"` — a TCP
  connect is **not** a liveness check (`docker-proxy` holds the port open past a dead backend).
- **When it is down, every DB test calls `t.Skip` and `go test` still prints `ok` and exits 0.**
  A skip is indistinguishable from a pass in the exit code. Census every run:
  `go test <pkg> -count=1 -v 2>&1 | grep -c -- '--- SKIP'` → require **0**.
- ⚠️ **ALL WORKTREES SHARE THIS ONE POSTGRES.** See §3.4 — it is the hard limit on parallelism.

### 1.4 Raise the workflow size limit and opt in

The session default is *"medium — keep workflows under 15 agents"*. This plan needs more.
**`/config` → Dynamic workflow size → raise it**, and say **`ultracode`** (or "use a workflow")
so the Workflow tool is reachable at all.

### 1.5 Know how to run the app — three of the last five defects came from using it

```bash
docker ps | grep tally_pg          # usually already up; else: make dev-db
make run-cored                     # :8087 — sets PORT and AUTH_DEV_TOKEN_ENABLED for you
make run-insightd                  # :8088
cd frontend && pnpm dev            # :5173
cd frontend && pnpm seed:demo -- --insightd && pnpm verify:demo "$(cat .demo-company-id)"
open "http://localhost:5173/?company=$(cat .demo-company-id)"
```
Sign in `browser-dev-owner` / `tally-demo-2026`. **Prefer the make targets** — `cored`'s own
default port is 8080 and everything silently fails on it. Launch detached
(`(nohup <cmd> > log 2>&1 &)`); harness-tracked background tasks get reaped mid-session.
**It is a keystroke app — a click on a list selects, `Enter` opens.**

---

## 2. THE LANE ARCHITECTURE — six conflict domains, not fifteen tracks

**Do not fan out one agent per track.** Tracks collide by *subsystem*, not by name, and two
agents in the same subsystem will clobber each other's work. There are **six conflict domains**.
**Run at most one track per domain at a time**; run the six domains concurrently.

| Lane | Domain | Sequence | Notes |
|---|---|---|---|
| **1** | ledger payload / money | **T2 → T11 → T8** | T11 (Budgets) **depends on T2**, so it lives in this lane rather than crossing lanes. T8 touches every money path — last, and alone in its lane. |
| **2** | inventory | **T9 → T10 → T3** | **The critical path — 3 tracks, all high-risk.** T9 first: it moves the Balance Sheet, so everything after it re-pins against a changed baseline. |
| **3** | reports | **T12 → T15** | T15 (Export) touches every report, so it goes after the last new one. |
| **4** | printing | **T16** | Short lane. Give it T6's screens if it finishes early. |
| **5** | auth | **T17 → T6 write path** | T6 is small: routes + screens + ACL over the schema that already shipped. |
| **6** | statutory | **T5 → T13** | T5 reuses the IRN precedent wholesale. |
| **7** | — | **T14 ALONE, LAST** | See below. |

### ⛔ T14 (Backup / restore / split company) RUNS LAST AND ALONE. Non-negotiable.

Two reasons, and either alone is sufficient:

1. **It must enumerate EVERY tenant-scoped table**, and the six lanes above are busy adding
   them. Started early, it ships a backup that silently omits cost centres, orders and budgets —
   and the omission is discovered when somebody restores.
2. **Highest blast radius in the product: a bug loses somebody's books.**

Build it on `internal/platform/testdb`'s **delete-plan census**, which already knows every
tenant table and is anti-rot by construction — the same mechanism that failed this session's
build until migrations 0069/0070 were added to `buildFixture`. **Do not hand-write a table list.**

### Longest lane is 3 tracks

Lane 2 sets the wall clock. If you have agent budget spare, spend it **inside** Lane 2 (split
T3's order-vs-fulfilment halves) rather than opening a seventh lane.

---

## 3. ⛔⛔ THE SHARED-FILE PROTOCOL — the thing that decides whether this session works

Every track adds tables, routes, reports and screens. **Every one of those touches the same
small set of registration files.** Six agents editing them in six worktrees will silently
clobber one another, and the failure is invisible: the code compiles, the tests pass in the
worktree, and the feature is dead after the merge.

### 3.1 The 14 files no lane agent may edit

| File | Why every track wants it |
|---|---|
| `backend/internal/authz/vocabulary.go` | `KnownResources`, `KnownReportNames` |
| `backend/internal/server/auth_middleware.go` | `routeAction()`, `reportName()` |
| `backend/internal/archtest/boundaries_test.go` | every new package declares its edge |
| `backend/internal/platform/testdb/testdb_test.go` | `buildFixture` — every new table |
| `backend/migrations/*_acl.sql` | ACL rows + `seed_company_auth_defaults` |
| `corpora/validate.py` | `KNOWN_SCREENS` |
| `frontend/src/app/screenRegistry.tsx` | `ScreenKind` |
| `frontend/src/app/defaultScreens.tsx` | screen registration |
| `frontend/src/app/AppShell.tsx` | `COMPANY_SCOPED_SCREENS`, **`SCREEN_ACTIONS`**, `actionLabel()` |
| `frontend/src/keystroke/buttonBar.ts` | button-bar items |
| `frontend/src/keystroke/keymaps/tallyprime.ts` | `screenBindings` |
| `frontend/src/keystroke/buttons.test.ts` | `SCREEN_PATHS` |
| `frontend/src/screens/FeaturesScreen.tsx` | **the row you are flipping** |
| `frontend/src/api/types.ts`, `mock.ts`, `screens/overlays/FrameOverlays.tsx` | co-owned; `FrameOverlays` now carries the F2 period classification lists |

### 3.2 The protocol: lane agents emit a MANIFEST, one integrator applies it

Each lane agent:
1. Writes its **own new files** freely — migrations, packages, screens, tests.
2. For each of the 14 files above, writes **nothing**. Instead it returns a
   **REGISTRATION MANIFEST**: exact file, exact anchor, exact text to insert.
3. Runs only **non-DB** tests in its worktree (see §3.4).

Then a **single serial integrator agent** applies every manifest, resolves ordering, and runs the
full gate. Give the integrator a schema so this is machine-checkable:

```js
const MANIFEST = { type:"object", required:["track","entries"], properties:{
  track:{type:"string"},
  entries:{type:"array", items:{type:"object",
    required:["file","anchor","insert","why"],
    properties:{ file:{type:"string"}, anchor:{type:"string"},
                 insert:{type:"string"}, why:{type:"string"} }}}}}
```

**A lane that reports "done" with an empty manifest and a new screen is lying to you** — a screen
needs 8 registrations. Have the integrator reject it.

### 3.3 The registration points, and what each miss costs

**A new RESOURCE needs 4** — `routeAction()` · `authz.KnownResources` · `authz.KnownReportNames`
· `reportName()`. **Miss one ⇒ default-DENY for every level, including Owner.**

**A new SCREEN needs 8** — `ScreenKind` · `corpora/validate.py` `KNOWN_SCREENS` (**this one fails
`make verify`, and it is the only miss a gate catches**) · `defaultScreens` · `buttonBar` (else it
falls through to `GATEWAY_BAR` and **no test catches it**) · `AppShell` `COMPANY_SCOPED_SCREENS`
(else `useCompanyID()` throws) · `keymaps` `screenBindings` (**an unbound key is not inert — global
`F5` opens a Payment voucher and `F9` a Purchase**) · `buttons.test.ts` `SCREEN_PATHS` ·
`actionLabel()`.

**⛔ AND THE THIRTEENTH, WHICH §5.2 OF THE OLD HANDOFF DOES NOT LIST:**
**`SCREEN_ACTIONS` in `AppShell.tsx`.** `Alt+O` shipped **completely dead** through this gap in a
commit whose 459 tests were green — keymap, `ScreenActionKind`, navMachine case, button-bar item
and the screen's own handler were **all present and correct**. Without the map entry the shell
resolves the combo, finds no screen action, and forwards the key to a machine that deliberately
does nothing. **No error. No status line. Nothing.**

> **Why every existing test missed it, and the rule that follows:** the panel's 11 tests called
> the dispatcher **directly**, bypassing the map; `buttons.test.ts` asserted the combo *resolves*,
> which it did. **Nothing rendered the real shell and pressed the real key.**
> **If a key is the feature, the test must press the key on the real shell** — the pattern is in
> `AppShell.test.tsx`, each test carries a control asserting the panel is absent first, and both
> were mutation-proved by deleting the map entry. Copy it for every new screen action.

**An ACL migration needs 2** — backfill existing companies **and** extend
`seed_company_auth_defaults`. Write the test that **creates its company at test time**, or you
prove the backfill and not the seeder.

**A new PACKAGE** — declare its edge in `archtest/boundaries_test.go` **with the reason**, and
keep it inward-only.

**A new REPORT** — the route path segment must be a **flat literal exactly equal to the report
name**. No `{param}`, no nesting under `/reports/`. A name that does not match is a rule an
administrator can write that can never match — **a silent permit.**

### 3.4 ⚠️ ALL WORKTREES SHARE ONE POSTGRES. This is the hard limit.

`tally_pg` on :5433 is a single instance. Six agents running `migrations.Apply` concurrently
**will race**, and the residue check counts rows globally.

**Therefore: lane agents run unit/pure tests only. All DB-backed verification is the
integrator's, serially.** That is a real cost of this plan and you should know it going in — it
means a lane's "green" is weaker evidence than usual, and the integrator's gate is where truth
happens. Heavy commands are auto-queued through `ci-slot` (2 slots) by a PreToolUse hook; do not
wrap anything yourself.

### 3.5 Migration numbers are pre-allocated — nobody picks their own

A collision already happened when two agents were both told "0036 is free". **Leave gaps unused
rather than renumbering.**

| Track | Block | | Track | Block |
|---|---|---|---|---|
| T2 | **0040–0041** | | T12 | **0058** |
| T3 | **0042–0044** | | T13 | **0059** |
| T5 | **0047–0048** | | T14 | **0060–0061** |
| T8 | **0051–0052** | | T15 | **0065** |
| T9 | **0053** | | T16 | **0066** |
| T10 | **0054–0055** | | T6 write-path ACL | **0071** (new) |
| T11 | **0056–0057** | | T17 SSO | **0072–0073** (new) |

Consumed and untouchable: 0045–0046, 0049–0050, 0062–0064, 0067–0068, **0069–0070**.
**Update `PLAN-TALLY-PARITY.md` §2.5 as each lands — it is the only authority.**

---

## 4. PER-TRACK DOSSIERS — the domain traps, each of which has corrupted something real

### Lane 1 — ledger payload / money

**T2 Cost centres + cost categories** (0040–0041) · **HIGH**
⛔ **Allocations live INSIDE the hashed canonical payload**, and the sum-to-posting invariant is a
**constraint trigger**, not a form check. Parallel allocation across *categories* is not one
column — a cost centre and a cost category are different axes and an allocation belongs to both.
T11 and T24 depend on this; get the shape right or two more tracks inherit it.

**T11 Budgets + budget-vs-actual** (0056–0057) · Medium · **depends on T2.**
Budgets are per (ledger|group|cost centre) per period. The variance report is the deliverable,
not the storage.

**T8 Multi-currency** (0051–0052) · **HIGH**
Realised forex gain/loss on settlement, **unrealised revaluation at period end**, and an
effective-dated rate table. Deep but isolated — *provided* it goes last in this lane, because it
touches every money path. **`NUMERIC(19,4)`, direction is `dr_cr`, never a sign. No `float64`,
no `parseFloat`, anywhere.**

### Lane 2 — inventory (the critical path)

**T9 FIFO / LIFO / batch valuation** (0053) · **HIGH — IT MOVES THE BALANCE SHEET**
Closing stock feeds the Balance Sheet. **Re-pin `verify:demo`** (currently 33/33) and expect the
demo figures to change — that is correct, not a regression, but it must be *deliberate*. Stock
value is never stored: it is derived by replaying the movement ledger through the pure valuer in
`internal/inventory`. Keep it that way.

**T10 Stock groups + compound units** ("Box of 12") (0054–0055) · Medium
Serialised **after** T9 because both touch valuation.

**T3 Sales / Purchase Orders + BOM** (0042–0044) · **HIGHEST RISK, AND THE MOST-USED MISSING
FEATURE**
⛔⛔ **AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT.** Push it through `stock_movement` and
closing stock — and the Balance Sheet — is corrupted. **Partial fulfilment is the normal case,
not an edge**: an order is consumed progressively by delivery notes and invoices, and the
outstanding balance is the whole point of the feature.

### Lane 3 — reports

**T12 GSTR-9 annual return** (0058) · Medium
⛔ **It is a RECONCILIATION of the year's GSTR-1/3B to the books, NOT a fresh derivation.** Its
value is stating *where the three disagree*. A GSTR-9 that silently re-derives is worse than none:
it hides the discrepancy it exists to surface.

**T15 Export — Excel / CSV / XML / e-mail** (0065) · Low technically
⚠️ **`authz.Export` has ZERO route mappings, so it default-DENIES today.** That ACL decision must
be made **explicitly** — who may export is a real policy question (an export is exfiltration of
the whole book), not a checkbox. Goes last in this lane so it covers every new report.

### Lane 4 — printing

**T16 Per-company print templates** (0066) · Low
The override table already exists; **nothing writes it**. This is a UI + write path over existing
schema. Logo, address block, footer, declarations.

### Lane 5 — auth

**T17 SSO / OIDC** (0072–0073) · Low-medium
`internal/authn` is a pure stdlib-crypto leaf and `internal/authz` may import it — **the edge is
one-way and must stay so** (a cycle makes the whole auth stack uncompilable).

**T6 TDS/TCS write path** (0071) · Small — **and it flips a row that is otherwise fully built**
Engine, storage, register, **the prescribed file format**, the challan schema (0069/0070) and the
sourcing layer all shipped. What is missing is only: **routes + screens + ACL** for entering a
challan and a deductor profile. Both are seeded by raw SQL in tests today.
- `company_tds_profile` carries **a named individual's PAN** → **disallow Data Entry**, exactly as
  0068 does for the register.
- Make the register's `return_file` status **company-aware**: the sourcing layer already produces
  four specific refusals (`ErrNoDeductorProfile`, `ErrTaxNotDeposited`,
  `ErrAllocationBrokenByAlter`, `form140.ErrNoValidReasonFlag`). Surfacing them turns "a challan
  register is missing" into "voucher P1 deducted ₹20,000 from Chitra Consulting and no challan is
  recorded against it".

### Lane 6 — statutory

**T5 e-Way Bill** (0047–0048) · Medium
**Reuse the IRN precedent in `internal/statutory` wholesale** — it is the worked example: NIC is
unreachable, so ship the deterministic fake + the recorded schema and **say so on the document's
own face**. Distance ≥ 50 km and value > ₹50,000 drive applicability; Part A / Part B split;
validity is a function of distance.

**T13 Interest on overdue bills** (0059) · Medium
Effective-dated rates, **simple vs compound is a per-party choice**, per-party override. It reads
`internal/reporting`'s Outstandings netting — **reuse it, never re-implement it** (that edge
already exists for `internal/forecast`).

### Lane 7 — alone, last

**T14 Backup / restore / split company** (0060–0061) · **HIGHEST BLAST RADIUS**
See §2. Build on `testdb`'s delete-plan census. **Tenant-exact or nothing**: a restore that
leaks one company's rows into another is worse than no backup at all. Split-company is the
hardest half — it partitions a book by date and both halves must independently balance.

---

## 5. THE GATES — a lane is not done until these pass

```bash
make verify                       # the whole thing: build, vet, backend, frontend, corpora, insightd
cd backend && gofmt -l internal/ migrations/    # your files must NOT appear (there is pre-existing drift)
cd frontend && pnpm exec tsc --noEmit && pnpm run build
cd frontend && pnpm verify:demo "$(cat .demo-company-id)"    # run ALONE
```

**Baseline to beat, measured 2026-08-04 at migrations 0070:**

| Gate | Now |
|---|---|
| backend `go test ./... -count=1` | **2014 pass / 0 SKIP / 0 FAIL** |
| frontend `pnpm run test:run` | **464 pass** |
| `verify:demo` | **33/33** (T9 will legitimately move these — re-pin deliberately) |
| `make verify` | exit 0 |

**Non-negotiables, all of which already have gates:**
- Any Go test that creates a company **must** call `testdb.Cleanup(t, companyID)` or
  `internal/testhygiene` fails the build.
- Every new tenant table needs a row in `buildFixture` or the teardown census fails **by name**.
- **NEVER `git add -A`.** It has committed reviewer scratch twice. Stage explicit paths and grep
  the staged list for `zz_`, `scratch`, `probe`, `.bak`.
- Every statutory rate carries a **source citation and an effective-from date**. Cannot verify it?
  Mark it `UNVERIFIED`. **Never invent a rate to make a test pass.**

### The evidence bar, and it is what makes the numbers mean anything

**Mutation-prove every safety-critical assertion.** Break the code deliberately, watch the test
fail, restore. This session did it for the Annexure 6 validity matrix, the broken-by-alter
detection, the rate-zeroing rule and the F2 classification guard — and every mutation was caught.
**A test that passes against broken code is worse than no test**, because it is counted.

---

## 6. TRAPS — do not re-derive these

### 6.1 A test that calls the dispatcher is testing the handler, not the keystroke
See §3.3. **If a key is the feature, press the key on the real shell.**

### 6.2 A default branch that produces a plausible sentence is invisible to tests
The F2 Period dialog told the **TDS Register** and the **Stock Summary** that their dates did
nothing, while both read and acted on them. On the register it contradicted the page *behind* it
in the same instant ("Press F2 to set the period. Both dates are needed"). Tests assert the
sentences they expect and **never the one nobody meant to render**. Fixed by removing the default:
`FULL_PERIOD_REPORTS` / `AS_AT_ONLY_REPORTS` / `NO_DATE_DIMENSION_REPORTS` in `FrameOverlays.tsx`,
with `periodClassification.test.ts` asserting **every** registered report is in exactly one.
⚠️ **Every lane adding a report screen must classify it there** — and that file is on the §3.1
manifest list.

### 6.3 A blocker is a claim with an expiry date
The TDS return file was recorded as blocked on a site that returns 403. **The document was never
on that site** — it was Protean/TIN's, it had been committed to this repo for two days, and the
plan carried the dead blocker anyway. **Re-check a blocker before building around it, and write
down the DOCUMENT you need, not the site that refused you.**

### 6.4 Never claim a validation that was not performed
Unreachable from here: the **FVU** (no JRE), the **GSTN portal**, the **NIC e-Way Bill API**, the
**IRP**, the **EPFO/ESIC portals**, the **TIN `.csi`** file. Ship the deterministic fake plus the
recorded schema and **say so on the artefact's own face**. A "generated" return that reads as
"validated" is worse than no return.

### 6.5 The lesson that cost real money this month
A shipped TCS rate was **half the legal rate for four months** because a verification was measured
against an **unamended edition** of the Act. The secondary source that carried the (correct)
payment codes also carried **wrong thresholds**. **Record the edition. Re-read on every Finance
Act.** Primary sources live in `docs/statutory/`.

### 6.6 Other traps worth the line
- **`posting` is HARD IMMUTABLE, and an ALTER re-creates every posting row.** Anything keyed on
  `posting.id` goes silently stale on the first correction. Key on `voucher_id` and **store what
  you matched**, so an alter is *detectable* — migrations 0045 and **0069** are the worked
  precedents.
- **The printed Day Book lists voucher VERSIONS**, so an altered voucher contributes twice. Still
  an open product call — do not "fix" it inside a lane.
- **`gofmt -w <dir>` reformats files you did not touch.** Format **named files**, not directories.
- **Tolerances are how a real error hides for a year. Reconcile to the paisa.**

---

## 7. WHAT ONLY A HUMAN CAN DO — hand these back, do not simulate them

| # | Ask | Unblocks |
|---|---|---|
| 1 | **A real Tally export** (XML, any version) | T32 and the whole commercial migration path. What exists has never met a real file |
| 2 | **A JRE + Protean's RPU zip** | Lets us say the TDS return is *validated*, not merely generated |
| 3 | **One real business day on the app** | **Tier 3 — the highest-value item in the plan**, and unmeasurable any other way |
| 4 | **IRP / GSTN / e-Way Bill sandbox credentials** (needs a GSTIN) | T5 for real, and T18 — the only thing that ever will |
| 5 | **EPF/EDLI administrative-charge rate notification**, if issued since 2026-07-30 | ~₹500/establishment/month accruing. **Do NOT substitute `S.O. 2701(E)`** — that is *inspection* charges, a different levy |

---

## 8. DEFINITION OF DONE

1. **14 rows flipped `on`** in `FeaturesScreen.tsx`; **T18 stays `off` with the forgery reason on
   its own row.**
2. `make verify` **exit 0**; backend **≥2014 pass, 0 SKIP, 0 FAIL**; frontend **≥464**;
   `verify:demo` green (**re-pinned deliberately after T9**, with the change explained).
3. Every new table in `buildFixture`; every new package's archtest edge declared **with its
   reason**; every new screen carrying **all 13** registrations including `SCREEN_ACTIONS`.
4. `PLAN-TALLY-PARITY.md` §2.5 updated for every consumed block.
5. **The app was actually run**, and the flipped features were driven in a browser — not merely
   tested. Three of the last five defects were found that way and none by reading code.
6. A successor handoff naming what Tier 2 inherits — especially **T29 slice 2b** (bank statement
   matching, where *ambiguity is a first-class outcome that must never auto-clear*) and **T32**
   (import from Tally, the commercial blocker).
