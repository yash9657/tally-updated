# PLAN — closing the remaining gap to Tally

> **Read §1 and §2 before running anything.** This plan finishes the product. The
> accounting engine is done; what is left is the surface a business touches every
> day. One item on this list — printing — is on its own the difference between a
> correct system and a usable one.
>
> This is accounting software. A confidently wrong number is a tax notice, a short
> salary, or an invoice a customer disputes. **Speed comes from parallelism across
> disjoint files, never from skipping the review pass.**

---

## 1. WHERE THINGS STAND

Branch `master`, HEAD **`9fb1e95`**. Migrations run to **0036**. Working tree clean.

Tracks 2 (GSTR-2A/2B + IMS + ITC) and 3 (payroll) shipped in `e76a90e`, `4c74ba9`,
`9fb1e95`. `HANDOFF-TRACKS-2-3.md` is spent — do not work from it.

**Verified gate baseline at that tip.** Re-measure before trusting; do not assume.

| Gate | Baseline |
|---|---|
| `cd backend && go build ./... && go vet ./...` | clean |
| `go test ./... -count=1` | **25 packages ok, 0 fail, 0 SKIP** |
| `make backend-test-residue` | per-table residue **0** |
| `make verify` | exit 0 |
| `cd frontend && pnpm exec tsc --noEmit` · `pnpm run build` | clean |
| `pnpm exec vitest run` | **370 tests / 33 files** |
| `cd insightd && .venv/bin/python -m pytest` | **107 passed, 0 skipped** |
| `python3 corpora/validate.py` | tax=14 tds=13 tcs=5 keystroke=32 isolation=13 migration=6 forecast=4 inventory=6 gstr2b=17 ims=12 payroll=19 |
| `.venv/bin/python -m insightd.benchmark.validate` | 28 fixtures / ALL FLOORS PASSED |
| Live, **run ALONE** | `verify:demo` **29/29** · `verify:tenancy` **113/113** · `e2e:ui` **38/38 TWICE** · `e2e:edit`/`numbering`/`state-code`/`ocr` OK |

**Two standing carve-outs, inherited, not caused by this plan:**

1. The **statutoryd freeze gate has no Makefile target** and was not run last
   session. It needs a fresh service token after any `cored` restart.
2. **From wage month June 2026 payroll books ZERO EDLI premium and ZERO EPF/EDLI
   admin charges** — roughly Rs 75 per member per month accruing unbooked on every
   real pay run. The rates are `ConfUnverified` and deferring is the honest
   behaviour. **Reading the primary G.S.R. is a release blocker, not a follow-up.**
   Do not invent a rate to close it.

---

## 2. HOW TO WORK

### 2.1 Machine constraints — these are why things get slow

8 cores / 16 GB, running several Claude worktrees plus local CI.

- **RUN ONE WORKFLOW AT A TIME.** Each caps at `min(16, cores-2)` = 6 agents.
  Three concurrent workflows means up to 18 agents each spawning a compiler; that,
  not the work, is what turns a track into an afternoon.
- Parallelise **inside** one workflow, across **disjoint file sets**. Two agents
  editing one file is how a fix gets clobbered.
- Heavy commands are auto-queued through `ci-slot` by a PreToolUse hook (2 slots).
  Do not wrap anything yourself. A test that pauses a few seconds is waiting.
- **Do not `git push` while agents run** — it starts up to 3 local CI jobs.
- Slow? `/machine-health`.

### 2.2 The database will kill your evidence if you let it

`tally_pg` (docker, port **5433**) does not auto-start and **has died mid-run
before**. When it is down every DB-backed test calls `t.Skip` and `go test` still
prints `ok` and exits 0. **A skip is indistinguishable from a pass in the exit code.**
Last session this invalidated an entire wave — five build agents and three
reviewers all reasoned from a green that proved nothing.

- Start it: `docker start tally_pg`, then `pg_isready -h localhost -p 5433`.
- Before trusting ANY run:
  `go test <pkg> -count=1 -v 2>&1 | grep -c -- '--- SKIP'` → require **0**.
  Use `-- '--- SKIP'` so indented subtest skips are caught.
- Tell every subagent the DB state, the skip-census command, and **"if it is down,
  STOP and return BLOCKED — do not try to start it"** (agents are denied
  `docker start`).
- Arm a watchdog for long runs:
  `while true; do nc -z localhost 5433 || echo DB-DOWN; sleep 60; done` via Monitor.
  Silence for the whole run is the evidence your numbers are real.
- `make verify` is safe — `backend/scripts/test_residue.sh` refuses an unreachable
  DSN or `CORED_SKIP_DB`. **Do not "fix" that script.** Only bare `go test ./...` lies.
- **Check the age of any running `cored`** (`ps -o etime`) before a live gate. A
  28-hour-old process predates your changes; every gate against it is meaningless.
  Kill it and `make run-cored`.

### 2.3 Rules that earned their place

- **NEVER `git add -A`.** It has already committed reviewer scratch twice. Stage
  explicit paths. Before committing, grep the staged list for `zz_`, `scratch`,
  `probe`, `.bak`, `testdata/` — a reviewer left a `zz_adversarial_authz_probe_test.go`
  behind last session and it was caught only at staging.
- **Do NOT run `go test ./...`** while agents work — false failures. Scope by package.
- Live gates (`e2e:ui`, `verify:demo`, `verify:tenancy`) run **ALONE**.
- Any Go test that creates a company MUST call `testdb.Cleanup(t, companyID)` or
  `internal/testhygiene` fails the build.
- Money is `decimal.Decimal` server-side, scaled integers client-side. **Never
  float64 / ParseFloat / Number() / parseFloat.** Grep every diff.
- Parse money from query strings with `decimal.NewFromString`.
- `corpora/validate.py`, `frontend/scripts/verify-demo.mts` and `seed-demo.mts` are
  **CO-OWNED**. ADD only; re-read immediately before each edit.
- Every statutory rate carries a **source citation and an effective-from date**.
  Cannot verify it? Mark it `UNVERIFIED`. **Never invent a rate to make a test pass.**
- The FVU, the GSTN portal, the EPFO and ESIC portals and the NIC e-Way Bill API
  **cannot run in this environment.** Never claim validation that was not performed.

### 2.4 The four checklists a new surface must satisfy

Every track below adds tables, routes and reports. Each one must clear all four, or
it fails a gate that already exists:

**A new TABLE** — `ENABLE` + `FORCE ROW LEVEL SECURITY`; a policy with USING **and**
WITH CHECK; composite `(company_id, id)` FKs; cross-tenant refusal proven **by the
FK**, not merely by RLS; **and a row in `buildFixture`**
(`internal/platform/testdb/testdb_test.go`) or the teardown coverage gate fails by name.

**A new ROUTE** — a `routeAction` mapping (unmapped `{companyID}` sub-paths
default-DENY and are unreachable for every level), plus a deliberate ACL decision.

**A new REPORT** — the **route path segment must be a FLAT LITERAL exactly equal to
the report name**. No `{param}`, no nesting under `/reports/`.
`TestReportVocabularyMatchesRouter` walks `authz.KnownReportNames` and asserts
`GET /v1/companies/{id}/<name>` resolves for every one. A name that does not match
is a rule an administrator can write that can never match — a silent permit.
Add the name to `authz.KnownReportNames` and a `reportName()` arm in `auth_middleware.go`.

**ACL** — the seeded `Data Entry` level carries a COARSE `allow display report`
matching EVERY `report:NAME` at specificity 1. **Every sensitive new report therefore
needs an explicit `disallow` at specificity 2**, in the style of migrations 0026,
0028, 0033 and 0035. A new *bare* resource token needs no rule (that level's
`default_access` is `none`) but **does** need to be in the vocabulary or
`TestRouteActionVocabularyIsClosed` fails.

**A new PACKAGE** — declare its edge in `internal/archtest/boundaries_test.go` with
the reason, and keep the direction inward-only.

### 2.5 Migration numbers are pre-allocated — do not pick your own

A collision already happened when two agents were both told "0036 is free". Blocks
are reserved per track so parallel tracks cannot collide. **Leave gaps unused rather
than renumbering.**

| Track | Reserved |
|---|---|
| T1 Printing | 0037–0038 |
| T2 Cost centres | 0039–0040 |
| T3 Orders + BOM | 0041–0043 |
| T4 Bank reconciliation | 0044–0045 |
| T5 e-Way Bill | 0046–0047 |
| T6 TDS/TCS persistence | 0048–0049 |
| T8 Multi-currency | 0050–0051 |
| T9 FIFO/LIFO | 0052 |
| T10 Stock groups + units | 0053–0054 |
| T11 Budgets | 0055–0056 |
| T12 GSTR-9 | 0057 |
| T13 Interest | 0058 |
| T14 Backup/restore | 0059 |

Whoever adds a migration also adds its entry to `internal/migration/expected.go`
(**CO-OWNED** — re-read immediately before editing).

**Every migration must be IDEMPOTENT.** `applyOne` wraps each file in one
transaction and stamps `schema_migrations` inside it, so a hand-applied file that
is not stamped will abort on re-run and **wall off every later migration**.
`ADD COLUMN IF NOT EXISTS` is fine; `ADD CONSTRAINT` needs a `DO $$ ... IF NOT
EXISTS ... $$` guard. Prove it:

```bash
(echo "BEGIN;"; cat backend/migrations/00XX_*.sql; echo "ROLLBACK;") \
  | PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -v ON_ERROR_STOP=1
```

### 2.6 The review pass is mandatory and can send work back

Every workflow ends with parallel adversarial reviewers, one per lens, each required
to produce **evidence** (command + observed output), not an opinion.
**A mutation that does not bite is a FINDING.**

Three things prior sessions proved, all of which cost real time:

1. A build wave reported all five slices COMPLETE. Review found three blockers
   including an eight-month payroll under-contribution. **Reported-complete means nothing.**
2. A reviewer's recommended fix was itself **wrong** — implementing it broke the
   Balance Sheet. **Verify a finding before acting on it**; REFUTED is a good outcome.
3. A reviewer ran its mutations while the DB was down, so its headline "this does not
   bite" finding was drawn from a run where nothing *could* bite. **Check `dbWasUp`
   and the skip census on every review before believing it.**

If a review agent dies (API 500s happen), the workflow will log `0 findings`. That is
**vacuous, not clean.** Resume the run — cached agents replay, only the failures re-execute.

### 2.7 Workflow shape to reuse

```
phase('Fix')      3 concurrent chains, partitioned by FILE OWNERSHIP
                  (a chain is sequential where two slices share a file)
phase('Wire')     ONE owner for server.go + auth_middleware.go + vocabulary.go
phase('Review')   2–3 adversarial lenses in parallel: mutation, statutory oracle,
                  authorization
```

Give each agent: an explicit **ownership list**, the **DB/skip rule**, the house
rules, "do not edit files outside your list — report as HANDOFF", and a
**structured-output schema** with `status`, `reproduced`, `mutationProof`,
`skipCensus`, `handoffs`, `caveats`. Make `reproduced` mandatory — it forces the
agent to demonstrate the defect before fixing it.

---

## 3. TIER 1 — the five that hurt in week one, plus printing

### T1 — PRINTING AND DOCUMENT OUTPUT  ← **do this first**

**Why first:** there is currently **zero** print code. `print` exists only as an ACL
action word. A business can key a sale but cannot hand the customer an invoice.
Every other gap on this list is survivable for some businesses; this one is not
survivable for any of them.

**Scope.** A print/PDF pipeline for: Tax Invoice (the GST-compliant one), Bill of
Supply, Delivery Note, Receipt/Payment voucher, Credit/Debit Note, and the reports
that get handed to an accountant (Trial Balance, Balance Sheet, P&L, Day Book,
Outstandings, Stock Summary, Payroll Register, Payslip).

**Design constraints:**
- A **Tax Invoice has statutory content requirements** (Rule 46 CGST Rules):
  supplier name/address/GSTIN, invoice number and date, recipient details, HSN, rate
  and amount of tax per head, place of supply, reverse-charge marker, signature.
  A missing mandatory field is an invalid invoice. Enumerate them, cite Rule 46, and
  **fail loudly** on a missing one rather than printing a blank.
- **Money on the page is the same number the ledger holds** — render from
  `decimal.StringFixed`, never re-derive, never float. Amount in words is Indian
  numbering (lakh/crore), and it is a formatter with its own tests.
- Templates are **per company and per voucher type**, stored (migration 0037), with
  a seeded default so a fresh company can print on day one.
- Decide and state ONCE where PDF is generated — server-side is preferable so the
  bytes are reproducible and testable. **Whatever you choose, the golden test asserts
  on generated bytes or extracted text, not on a screenshot.**
- `Print` and `Export` are already distinct ACL actions. Use them: a level that may
  display a report may not automatically print or export it.

**Migrations:** 0037 (templates), 0038 (ACL).
**Gates:** golden-file tests per document; a Rule 46 completeness test naming each
missing field; `verify:demo` gains a check that the demo company can print its own
first invoice.

**Parallelises well:** one agent per document family, one for the amount-in-words
and number formatters, one for the template store, one wiring agent.

---

### T2 — COST CENTRES AND COST CATEGORIES

**Why:** very widely used in Tally, and there is **no allocation dimension on
postings at all** today.

**The hard part is the data model, not the UI.** A cost centre allocation splits a
single posting across centres; allocations must sum EXACTLY to the posting amount,
enforced by a deferred constraint the way `posting_balanced` is — not by the form.
Cost categories allow parallel allocation (the same amount allocated once per
category), which is where naive models go wrong: it is not one column on `posting`.

- New tables: `cost_category`, `cost_centre`, `posting_cost_allocation`.
- The sum-to-posting invariant is a **CONSTRAINT TRIGGER**, because a voucher with no
  allocation rows must also be legal (allocation is optional per ledger).
- Reports: Cost Centre Summary, Category Summary, Cost Centre vouchers.
- Remember `voucher_version` is immutable and hash-chained: allocations live inside
  the hashed canonical payload, exactly as `inward_doc` does. Follow that precedent.

**Migrations:** 0039 (schema), 0040 (ACL — a cost report exposes departmental
profitability; decide deliberately).

---

### T3 — SALES / PURCHASE ORDERS (and BOM in the same track)

**Why:** most trading businesses run on them.

**The modelling decision that must be made explicitly:** an order is a **COMMITTED
QUANTITY, NOT A MOVEMENT.** The stock ledger has exactly one primitive — a positive
quantity plus a direction — and an order is neither. Do **not** push orders through
`stock_movement`; that would corrupt closing stock, which the Balance Sheet now
depends on. Orders are their own tables with their own pending/fulfilled state, and
they affect *reports* (Order Book, Reorder Status) not *balances*.

- Tables: `sales_order`, `purchase_order`, `order_line`, and the link from a
  Delivery Note / Receipt Note / Invoice back to the order line it fulfils.
- Partial fulfilment is the normal case. Over-fulfilment is a decision — refuse or
  allow with a finding — state it once.
- **BOM** rides here because it shares the "committed quantity" shape: a BOM explodes
  a finished good into components at manufacture time via a Stock Journal, which
  already exists.

**Migrations:** 0041 (orders), 0042 (BOM), 0043 (ACL).

---

### T4 — BANK RECONCILIATION

**Why:** every business, every month. **Zero code today.**

- Add a bank date (the date the bank cleared it) alongside the voucher date on
  bank-ledger postings, plus a reconciliation state. In Tally this is exactly the
  "Bank Date" column.
- A Bank Reconciliation Statement report: balance as per books, balance as per bank,
  and the un-cleared items that explain the difference — **and that difference must
  reconcile to the paisa**, in the same spirit as `migration.ReconcileFull`.
- Statement import (CSV) with a matching pass is the second slice. Match on amount +
  date window + reference; **never auto-clear an ambiguous match** — surface it.
- The reconciliation must not mutate the voucher: it is metadata on the posting, and
  `voucher_version` immutability still holds.

**Migrations:** 0044 (schema), 0045 (ACL — a bank statement is sensitive).

---

### T5 — e-WAY BILL

**Why:** legally required to move goods over Rs 50,000.

**Reuse the IRN precedent — do not invent a second pattern.** `internal/statutory`
already has the e-invoice/IRN workflow, a durable Postgres step store, a runner, a
chaos test and a freeze hook. An e-Way Bill is the same shape: build a payload from a
voucher, call an external API, persist the returned number, freeze the voucher
against edits that would invalidate it.

- Extend `internal/statutory` with an `ewaybill.go` workflow beside `einvoice.go`.
- Threshold, validity-period-by-distance, Part A vs Part B, and the transporter
  fields all need **citations and effective-from dates**.
- **The NIC API cannot be reached from this environment.** Ship against the existing
  `fake.go` provider, keep the schema test, and say plainly in the commit that no
  live NIC call was made.

**Migrations:** 0046 (schema), 0047 (ACL).

---

### T6 — TDS / TCS PERSISTENCE, REGISTER AND RETURNS

**Why:** the *derivation engines already exist and are corpus-gated*
(`internal/compliance/tds.go`, `tcs.go`, corpora `tds=13 tcs=5`), and a TDS leg on a
Purchase/Payment/Journal is verified against them — but **nothing is persisted or
reported.** There is no register and no return. This is the cheapest big win on the
list: the maths is done and trusted.

- Persist the derived TDS/TCS lines against the voucher version (inside the hashed
  payload, following `inward_doc`).
- Deduction register and collection register reports.
- Form 26Q / 27Q / 27EQ quarterly return files, following the `payrollfile` emitter
  precedent — file-local row structs, `payrolldb`-style mappers, and
  `WriteDisclosureSidecar` for every emitted file.
- A field the mapper cannot populate is an **error or an explicit disclosure, never a
  silent empty string** — a blank PAN is a rejected return, not a partial one.
- **The FVU cannot run here.** Say so; do not claim validation.

**Migrations:** 0048 (schema), 0049 (ACL — these carry PAN per deductee).

---

### T7 — CORRECT THE FEATURES SCREEN  ← cheap, do it in the first workflow

`frontend/src/screens/FeaturesScreen.tsx` is the product's honesty surface and it is
now **stale in the direction that undersells the product.** It still declares:

- **"Payroll statutory — Not built yet."** It is built and shipped (`4c74ba9`).
- **"Closing stock on the Balance Sheet — NOT WIRED."** It is wired; `verify:demo`
  pins the number at five month-ends by two independent paths.
- **"Password / SSO login — NOT BUILT."** `POST /auth/login` and `/auth/password`
  are mounted (`auth_login.go`) with a real credential store.

Fix those three, and **add the still-missing items from this plan** so the screen
stays the honest list it was designed to be. As each track below lands, the screen
entry moves in the same commit — not in a follow-up.

---

## 4. TIER 2 — real gaps, survivable for many businesses

Each is a full track with the same §2.4 checklists and the same review pass. Ordered
by rough value.

| # | Track | The one thing not to get wrong | Migrations |
|---|---|---|---|
| **T8** | **Multi-currency** | Only bites exporters, but when it bites it is the whole ledger. A foreign-currency amount, a rate, and the base-currency amount are **three stored values**, not two plus arithmetic. Realised vs unrealised gain/loss are different postings. | 0050–0051 |
| **T9** | **FIFO / LIFO valuation** | v1 is weighted average (Tally's default) behind a **valuer interface whose state is a strict subset of what FIFO needs** — `ValuerFor` in `internal/inventory`. So FIFO is a NEW ARM plus fixtures, not a schema change. Closing stock now feeds the Balance Sheet, so a valuation change moves the sheet: re-pin `verify:demo`. | 0052 |
| **T10** | **Stock groups + compound units** ("Box of 12") | Items are flat and units are bare symbols today. A conversion factor turns every quantity in the system into a *unit-qualified* quantity — touch the movement primitive with care, and never store a converted quantity you can re-derive. | 0053–0054 |
| **T11** | **Budgets** | A budget is a comparison dimension, not a posting. Budget-vs-actual on groups, ledgers and (after T2) cost centres. | 0055–0056 |
| **T12** | **GSTR-9 annual return** | Builds on GSTR-1 + 3B + the new ITC engine. It is a *reconciliation* of the year's returns to the books; it must state where it disagrees rather than smoothing it. Not filing-ready without portal-schema verification — keep the disclosure discipline. | 0057 |
| **T13** | **Interest calculation** | On overdue receivables/payables. Simple vs compound, and the day-count basis, are configuration with statutory defaults — cite them. | 0058 |
| **T14** | **Backup / restore, split company data** | Operationally important and currently absent. Must be tenant-exact: a restore that leaks one company's rows into another is worse than no backup. Reuse the `testdb` delete-plan census — it already knows every tenant-scoped table and is anti-rot by construction. | 0059 |

---

## 5. SUGGESTED SEQUENCE — one workflow at a time

Each numbered item is **one workflow**, ending in its own adversarial review, its own
gate run, and its own commit. Do not start the next until the current one is green
and committed.

1. **T1 Printing** + **T7 Features-screen truth fix.** Biggest gap, and the screen
   fix is a few lines that ride along.
2. **T6 TDS/TCS.** Cheapest big win — the engines and corpora already exist.
3. **T2 Cost centres.** Data-model heavy; do it before Budgets (T11) depends on it.
4. **T4 Bank reconciliation.** Self-contained, high daily value.
5. **T3 Orders + BOM.** Largest Tier-1 modelling job.
6. **T5 e-Way Bill.** Reuses the IRN scaffolding, so it is mostly a new workflow arm.
7. **T9 FIFO/LIFO**, then **T10 stock groups/units** — both touch inventory; serialise.
8. **T8 Multi-currency.**
9. **T11 Budgets** → **T12 GSTR-9** → **T13 Interest** → **T14 Backup/restore.**

**Within each workflow, parallelise aggressively across disjoint files.** The pattern
that worked: 3 concurrent chains in a Fix phase, ONE serial owner for the shared
wiring files, then 2–3 review lenses in parallel.

---

## 6. DEFINITION OF DONE — per track

- Every blocker closed with a **mutation that makes a NAMED test fail with a message
  stating the consequence in domain terms** (rupees, credit, salary, "an operator
  could read every employee's salary"), restored byte-identically (`shasum -a 256`).
- `go test ./... -count=1` green, **0 SKIP**, package count stated.
- `make verify` exit 0. `make backend-test-residue` residue 0.
- Frontend `tsc` + `vitest` + `build` green; `insightd` pytest green; `corpora/validate.py`
  counts stated.
- Live gates run **ALONE** against a `cored` rebuilt from the tree under test:
  `verify:demo`, `verify:tenancy`, `e2e:ui` **twice**.
- Every new table has RLS (USING **and** WITH CHECK), composite FKs, a `buildFixture`
  row, and cross-tenant refusal proven **by the FK**.
- Every new route has a `routeAction` mapping and a deliberate ACL decision; every new
  report has a matching `disallow` for `Data Entry`.
- Every migration proven idempotent by the `BEGIN; … ROLLBACK;` re-apply.
- Every statutory rate carries a citation and an effective-from date; anything
  unverified is marked `UNVERIFIED`, not asserted.
- **Nothing claimed as validated that was not.** The FVU, the GSTN portal, the NIC
  e-Way Bill API, the EPFO and ESIC portals cannot run here.
- `FeaturesScreen.tsx` updated **in the same commit** as the feature.
- Commit message states which gates ran and **every deferral**.

Commit trailer:

```
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
```

---

## 7. WHAT THIS PLAN DELIBERATELY DOES NOT CLAIM

- That this is the complete gap to Tally. It is the gap **found by inventorying
  routes, tables and code** against Tally's feature set. Nobody has yet driven the
  app for a day as a shopkeeper would, which is how the smaller papercuts surface —
  the ones that do not show up as a missing table. **Budget a usability pass**
  before calling parity.
- That Tier 2 is exhaustive. Tally has thirty years of accumulated surface: POS,
  job costing, ODBC, data synchronisation, payroll attendance/leave, and more. What
  is here is what a typical Indian SMB would miss first.
- Any estimate of how long a track takes. The tracks are sized by risk, not by hours.
