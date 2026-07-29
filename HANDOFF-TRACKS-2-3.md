# HANDOFF — close out Track 2 (GSTR-2A/2B + IMS) and Track 3 (payroll)

> **Read this whole file before running anything.** Both tracks BUILD and both have
> passing unit suites in their pure leaves — and both are **NOT COMMITTABLE**. An
> adversarial review found 3 distinct blockers in Track 2 and 3 in Track 3, every
> one of them a wrong number or a wrong-by-construction gate rather than a style
> nit. The suites are green *because* the tests do not reach the broken paths.
>
> This is accounting software. Track 2 decides how much input tax credit a company
> claims; Track 3 pays people. A confidently wrong number here is a tax notice or a
> short salary.

---

## 1. WHERE THINGS STAND

Branch `master`, HEAD **`3afb871`**. Migrations run to **0035** (applied to the dev
DB already). Four commits landed this session:

| Commit | What |
|---|---|
| `75ec7f1` | Track 0 — per-test company teardown, residue gate (bound 0), `make verify` green |
| `8d43eb7` | Three holes the Track-0 review found in those gates |
| `66470f6` | Track 1 — closing stock on the Balance Sheet and the P&L |
| `3afb871` | Removed reviewer scratch committed by mistake |

**Tracks 2 and 3 are UNCOMMITTED in the working tree** — 28 paths:

```
M  backend/go.mod  backend/internal/archtest/boundaries_test.go
M  backend/internal/compliance/gstr/gstr3b.go (+_integration_test.go)
M  backend/internal/ledger/{hash,ledger,nonaccounting}.go
M  backend/internal/migration/{expected.go,reconcilefull_test.go}
M  corpora/{README.md,validate.py}  corpora/migration/02-multistate-gst.expected.json
M  frontend/scripts/{seed-demo,verify-demo}.mts
?? backend/internal/compliance/itc/            (Track 2 engine)
?? backend/internal/ledger/inward_doc*.go      (Track 2 — supplier doc on the voucher)
?? backend/internal/payroll/                   (Track 3 — engine + payrolldb + payrollfile)
?? backend/internal/ledger/payroll_class.go
?? backend/migrations/0032..0035
?? corpora/{gstr2b,ims,payroll}/
?? backend/internal/compliance/gstr/gstr3b_table4_test.go
```

**Current measured state** (re-verify, do not trust):

```
go build ./...                     exit 0
go vet ./internal/compliance/... ./internal/payroll/...   clean
go test ./internal/compliance/itc  ok
go test ./internal/payroll         ok
go test ./internal/payroll/payrolldb   FAIL  (9 tests — see Track 3 blocker B)
go test ./internal/platform/testdb     FAIL  (fixture gap — see §4, integrator's job)
corpora/validate.py                OK  (…gstr2b=16, ims=12, payroll=18)
```

---

## 2. HOW TO WORK — read before spawning anything

### 2.1 Machine constraints (NEW this session — these are why things were slow)

The laptop is 8 cores / 16 GB and runs several Claude worktrees plus local CI.

- **RUN ONE WORKFLOW AT A TIME.** Workflows cap at `min(16, cores-2)` = 6 agents
  each; three concurrent workflows meant up to 18 agents each spawning compilers.
  That, not the work, is why tracks took hours.
- Heavy commands are **auto-queued** through `ci-slot` by a PreToolUse hook
  (2 slots). You do not need to wrap anything; do not double-wrap. A test that
  seems to hang for a few seconds before starting is waiting for a slot.
- OrbStack is capped to 4 of 8 cores; the CI runners to 3 collectively. A
  `git push` to a PR starts up to 3 local CI jobs — **do not push while agents run**.
- If anything feels slow: `/machine-health` or
  `~/.claude/skills/machine-health/scripts/diagnose.sh`.

### 2.2 Rules that earned their place

- **NEVER `git add -A`.** A `git add -A` run while a review agent was mid-experiment
  committed a mutated `plan.go` plus two scratch directories in `75ec7f1`; it took
  two follow-up commits to clean up. **Stage explicit paths, always.**
- **Do NOT run `go test ./...`** while agents work — false failures. Scope with
  `-run` and by package.
- Live gates (`e2e:ui`, `verify:demo`) run **ALONE**.
- Any Go test that creates a company MUST call `testdb.Cleanup(t, companyID)` or
  `internal/testhygiene` fails the build (static, no DB needed).
- Money is `decimal.Decimal` server-side, scaled integers client-side. **Never
  float64 / parseFloat / Number()**. Grep every diff.
- **`corpora/validate.py`, `frontend/scripts/verify-demo.mts` and `seed-demo.mts`
  are CO-OWNED** by Tracks 1, 2 and 3. Edit by ADDING only; re-read immediately
  before each edit. Both tracks' content is already present and nothing was lost —
  keep it that way.

### 2.3 The review pass is mandatory and can send work back

Every wave ends with parallel adversarial reviewers, one per lens, each required to
produce **evidence** (command + observed output), not an opinion. Verdicts are
CONFIRMED / REFUTED / PARTIAL. **A mutation that does not bite is a FINDING.**

Two things this session proves are worth the cost:

1. Track 3's build wave reported all five slices COMPLETE. The review found three
   blockers including an **eight-month payroll under-contribution**.
2. A reviewer's recommended fix was itself **wrong** — implementing it broke the
   Balance Sheet (assets 7500 vs liabilities 500). **Verify findings before acting
   on them.** See `TestBackDatedStockKeepsTheSheetBalanced` in
   `internal/reporting/closing_stock_statement_test.go` for the counter-example.

---

## 3. TRACK 2 — three blockers, then wiring

Engine lives in `backend/internal/compliance/itc/`. It ingests a GSTR-2A/2B
statement, matches supplier documents against posted purchases, classifies each,
carries IMS accept/reject/pending with the deemed-accept timer, and feeds GSTR-3B
table 4. `internal/ledger/inward_doc.go` puts the supplier's invoice number/date and
ITC classification on `voucher_version`, inside the hashed canonical payload.

### B1 — s.16(4) time-bar is tested against the WALL CLOCK
`internal/compliance/itc/effect.go` (`neverAvailable`) and `match.go:998-1002`
(`pairFindings`) evaluate the s.16(4) expiry against `Policy.AsOn` — the instant the
report is generated — instead of the instant the credit was claimed.

**Consequence:** regenerate any past period's GSTR-3B after 30 November following
that document's FY and **100% of the period's ITC** moves from 4(A) to 4(D)(2) and
4(C) collapses to zero — including periods already filed. The same books produce a
different, catastrophic answer purely because time passed.

**Fix:** give `Policy` a distinct `ClaimOn` instant — `Policy.Freeze` when the
period's 3B is filed, otherwise the period's own GSTR-3B due date (`clock.go`
already has `GSTR3BDueDate(p)`) — and use it in `neverAvailable()` and
`pairFindings()`. `Policy.AsOn` must keep deciding **only** the IMS projection. Add
a named regression test that regenerates a filed period "years later" and asserts
4(C) is unchanged.

### B2 — a multi-period window silently reverses everything after month 1
`internal/compliance/itc/service.go:343` — `Reconcile` derives ONE tax period from
`from` alone (`period := PeriodOf(fromT)`) but loads BOOKS for the whole `[from,to]`
range, and `handler_gstr3b.go` takes `from`/`to` verbatim from the query string.

**Consequence:** on a quarterly or annual window, only month 1's 2B is loaded, so
every purchase in months 2..N is classified `MISSING_IN_2B` and **reversed in full**
in 4(B)(2) — even where the supplier filed and that month's 2B was ingested.

**Fix (pick one, state it in the code):** (a) REFUSE — error when
`PeriodOf(fromT) != PeriodOf(toT)` or the window is not exactly that calendar month,
naming the resolved period, and have the wiring wave iterate month by month; or
(b) iterate the periods inside `Reconcile`, loading each period's statement.
Refusing is the safer default and matches this codebase's `ValuerFor` precedent.

### B3 — the two halves of the seam contradict each other
On a supplier credit note the supplier declared LARGER than we booked, `itc` emits
`Available=-300` with a 4(B)(2) reversal of 100 (its own comment: *"books -300
against a statement -400 is an overclaim of 100"*), while
`gstr/gstr3b.go:781-806` (`checkITCDecision` rule 4) caps any reversal at
`max(Available, 0)`. One of them is wrong and they disagree silently.

**Fix:** decide which rule wins and state it ONCE. Either `itc` returns `ZeroTax()`
before the Mismatched branch when the books' tax total is negative (and the
"overclaim of 100" paragraph is deleted), or `gstr`'s rule (4) bound is widened.
Add a fixture that pins the chosen answer in both packages.

### Track 2 wiring (serial, one owner, AFTER the blockers)
`server.go` `routes()`, inside the `{companyID}` group next to `r.Get("/gstr3b", …)`:
```go
r.Get ("/gstr2b-reconciliation", s.handleGSTR2BReconciliation)
r.Get ("/ims-worklist",          s.handleIMSWorklist)
r.Post("/gstr2b-import",         s.handleGSTR2BImport)
r.Post("/ims-actions",           s.handleIMSAction)
r.Post("/gst-filings",           s.handleRecordGSTFiling)
```
**The two GET path segments must be FLAT LITERALS equal to the report name** — no
`{param}`, no nesting. `TestReportVocabularyMatchesRouter` walks the vocabulary and
asserts `GET /v1/companies/{id}/<name>` resolves for every name.

Plus `reportName()` arms in `auth_middleware.go` and the two names in
`authz.KnownReportNames`. GSTR-3B itself needs **nothing** — all three touches exist.

Query params: `/gstr2b-reconciliation` takes `from`,`to` (required),
`statement_id` (optional, defaults to newest non-superseded), `as_on` (optional
RFC3339, defaults to `time.Now()` **at the handler boundary, never inside the
engine**). `/ims-worklist` takes `return_period` (MMYYYY) and optional `as_on`.
`handler_gstr3b.go` gains optional `statement_id`, `as_on`, and rule-42/43 inputs
`exempt_turnover`, `total_turnover`, `capital_common_credit` — all via
`decimal.NewFromString`, **never ParseFloat**. With `statement_id` absent, output
must be byte-identical to today plus
`disclosures:["no-inward-statement","not-filing-ready"]`.

---

## 4. TRACK 3 — three blockers; the package is currently RED

Layout: pure `internal/payroll` (no DB imports) + `internal/payroll/payrolldb` (DB
seam) + `internal/payroll/payrollfile` (ECR / Form 24Q / ESI emitters).

A remediation wave already fixed the worst finding — the s.2(88) 50%-deeming wage
rule was dated 2026-07-01 when the Code on Social Security commenced **21 Nov 2025**,
under-contributing PF and ESI for eight months. It now splits into **three** windows
(`ss-2019-07`, `ss-2025-11`, `ss-2026-07`) with cited commencement dates. **Do not
re-litigate those dates without new sources.** Three blockers remain.

### B1 — `payrolldb` is RED: 9 tests fail, tree not committable
The wire-engine slice added an engine gate (`ErrSlipNotFromEngine`) that refuses the
pre-existing hand-supplied fixtures. Failing: `TestPayRunPostsThroughTheLedgerAndBalances`,
`TestPayRunVoucherTypeIDIsNotNull`, `TestPayRunIsIdempotentAcrossACrashBetweenTheTwoTransactions`,
`TestPayRunIsAlterableAndReversible`, `TestPayslipIsImmutable`,
`TestPerEmployeePayableLedgerSplitsTheNetPayLeg`, `TestPayrollRegisterAndPayslipReadBack`,
`TestPayrollTablesAreTenantIsolated`, `TestControlLedgerMissingIsRefusedNotInvented`.
Two now fail **before reaching their own assertions**, so the behaviours they exist
to prove are currently unverified by anything.

**Fix:** replace `samplePayRun()`'s hand-derived `slipA`/`slipB` with
`svc.ComputeSlips(ctx, wageMonth, inputs)` and feed the returned slices to
`RunPayRun`, so the posting-seam tests assert on real engine output.

### B2 — the EDLI/admin fix is DEAD CODE in the product
`payroll.AggregateLegs` — fixed by the remediation to debit the EDLI premium and the
EPF/EDLI administrative charges — has **zero non-test callers**. The real write path
is `payrolldb.buildPostings`, and `toSlipInput` silently drops those three
components, so the EPFO liability is never booked.

**Fix (pick one):** (a) route `payrolldb`'s posting through `payroll.AggregateLegs`
so there is ONE leg builder; or (b) add `edli_contribution`, `edli_admin_charges`,
`epf_admin_charges` columns to `payslip` in **migration 0036**, carry them on
`SlipInput`, compare them in `diffSlip`, and add both legs in `buildPostings`.
(a) is preferable — two leg derivations that can disagree is what caused this.
**Do not "fix" the imbalance by dropping the components: that deletes a real
liability from the books.**

### B3 — none of the three statutory outputs is reachable
`internal/payroll/payrollfile` (PF ECR, Form 24Q, ESI return — the three named
deliverables) has **no caller anywhere**. The product cannot emit any of them.

**Fix:** build the `payrolldb → payrollfile` row mappers and a route, or state
plainly in the commit that Track 3 ships a verified calculation library plus
schema/ACL and that emission is not wired. **Do not report a library as a feature.**
Note the FVU cannot run in this environment — say so rather than claim validation
that was not performed.

---

## 5. INTEGRATOR WORK — nobody else can do these

### 5.1 `testdb` fixture gap (blocks `go test ./...`)
`internal/platform/testdb` is RED because both tracks added company-scoped tables and
`buildFixture` seeds no row in them:

```
employee  employee_salary_structure  employee_statutory_election
gst_ims_action  gst_inward_document  gst_inward_statement  gst_return_filing
pay_run  payslip  payslip_component
```

This is the coverage gate **working as designed** — a table with no fixture row
means its teardown is never exercised. Add one row per table to `buildFixture` in
`internal/platform/testdb/testdb_test.go`, honouring FK order. The tracks were
forbidden to edit that file, so it is yours.

### 5.2 Shared files to reconcile
`corpora/validate.py` (both tracks added families — confirm `gstr2b`, `ims` **and**
`payroll` all survive), `frontend/scripts/verify-demo.mts` and `seed-demo.mts`
(Tracks 1 and 2 both wrote there; Track 1's closing-stock number-pinning checks and
Track 2's table-4 checks must both be present).

### 5.3 Track 1's deferred half
`66470f6` shipped the backend only. Slice 3 (inventory in the demo seed) and Slice 4
(pinning the closing-stock NUMBER in `verify:demo`) are in those two co-owned
scripts and are **not yet committed or run**. Until they land, the demo books have no
inventory and `verify:demo` cannot see closing stock at all.

---

## 6. GATE MATRIX — expected when done

| Gate | Expected |
|---|---|
| `cd backend && go build ./... && go vet ./...` | clean |
| `go test ./... -count=1` | exit 0; **27 packages before Tracks 2–3**, more after — state the real number |
| `make backend-test-residue` | suite + per-table residue all **0** |
| `make verify` | exit 0 (runs residue instead of raw backend-test) |
| `cd frontend && pnpm exec tsc --noEmit` | clean |
| `pnpm exec vitest run` | 370 tests / 33 files (pre-Track-2/3 baseline) |
| `pnpm run build` | ok |
| `cd insightd && python -m pytest` | **107 passed, 0 skipped** |
| `python3 corpora/validate.py` | tax=14 tds=13 tcs=5 keystroke=32 isolation=13 migration=6 forecast=4 inventory=6 gstr2b=16 ims=12 payroll=18 |
| `python -m insightd.benchmark.validate` / `.report` | 28 fixtures / ALL FLOORS PASSED |

Live, **run alone**: `verify:demo` 24/24 (more once Track 1's slice 4 lands) ·
`verify:tenancy` 113/113 · `e2e:ui` 38/38 twice · `e2e:edit`/`numbering`/`state-code`/
`ocr` OK · statutory freeze OK incl. durable restart.

Services: `docker start tally_pg` (does not auto-start) · `make run-cored` ·
`make run-insightd` · `make seed-all` · statutoryd needs a fresh service token after
any cored restart.

---

## 7. SUGGESTED PLAN — one workflow at a time

**Wave 1 — Track 2 blockers.** 3 slices on disjoint files (`effect.go`+`match.go` /
`service.go` / `gstr3b.go`+`itc` reconcile-decision), then 2 adversarial lenses
(mutation; independent Python oracle re-deriving 4(A)/4(B)/4(C)/4(D) from the
statute). Gate: `go test ./internal/compliance/... -count=1` + `corpora/validate.py`.

**Wave 2 — Track 3 blockers.** 2 slices (payrolldb tests → `ComputeSlips`;
AggregateLegs as the single leg builder), then 2 lenses (mutation; an oracle that
recomputes gross-to-net from the statute for a below-ceiling employee, an
above-PF-ceiling one, an ESI mid-period ceiling-crosser, a mid-year joiner, a
leaver, arrears, and one exactly on a slab and on the 87A boundary).
Gate: `go test ./internal/payroll/... -count=1`.

**Wave 3 — integrator, no agents.** `buildFixture` rows; reconcile the co-owned
files; commit Track 1's deferred slices.

**Wave 4 — serial wiring, ONE owner.** `server.go` + `auth_middleware.go` +
`authz/vocabulary.go` + ACL migration **0036** (or 0037 if Track 3 took 0036).
Remember the seeded `Data Entry` level carries a COARSE `allow display report`
matching EVERY `report:NAME`, so each sensitive report needs an explicit `disallow`
at specificity 2, as 0026/0028 do. **Payroll and statutory output are more sensitive
than the cost book** — salaries and PAN/Aadhaar are not "reports an operator may see
by inheritance".

**Wave 5 — integrator.** Full gate matrix; then live gates ALONE; then commit, one
verified-green commit per track, stating which gates ran and every deferral.

Commit trailer:
`Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`

---

## 8. DEFINITION OF DONE

- All 6 blockers closed, each with a mutation that makes a NAMED test fail with a
  message stating the consequence, restored byte-identically (`shasum -a 256`).
- `go test ./...` green, package count stated.
- `make verify` exit 0.
- Every statutory rate carries a source citation and an effective-from date; any
  unverified value is marked UNVERIFIED rather than asserted.
- Every new table: `ENABLE` + `FORCE ROW LEVEL SECURITY`, a policy with USING **and**
  WITH CHECK, composite `(company_id, id)` FKs; cross-tenant refusal proven **by the
  FK**, not merely by RLS.
- Every new route has its `routeAction` mapping (unmapped paths default-DENY) and a
  deliberate ACL decision.
- The GSTR-3B header comment still tells the truth about what is and is not
  implemented. If rule 42/43 or s.17(5) remain gaps, it must still say the output is
  **not filing-ready**. A stale "now fully compliant" comment is a BLOCKER.
- Nothing claimed as validated that was not: the FVU and the GSTN portal cannot run
  here.
