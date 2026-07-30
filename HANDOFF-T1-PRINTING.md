# HANDOFF — after T1 (printing), before the live gates

> **Read §1 before running anything.** T1 is committed but **its live gates have never
> run**. The first job in the next session is not building — it is finding out whether
> what was committed actually works end to end. Everything needed to do that is in §2.
>
> Work from `PLAN-TALLY-PARITY.md` for *what comes next*; work from this file for
> *what is unfinished about what just landed*.

---

## 1. STATE

Branch `master`, HEAD **`c9e2126`** (`feat(printing): T1 — server-side PDF documents,
Rule 46 gate, and the print ACL`). Working tree **clean**. Migrations run to **0039**.

T1 shipped in one commit after three waves: a build wave (12 agents), a fix wave for the
review's 4 blockers (9 agents), and a third wave for the 4 blockers the *verification*
lenses found (4 agents). `PLAN-TALLY-PARITY.md` §3 T1 and T7 are now done; §5 item 1 is
complete. The migration table in §2.5 was renumbered — T1 took **0037–0039** and every
unstarted block below it shifted by one.

### 1.1 Gates that ran at `c9e2126`, with numbers

| Gate | Result |
|---|---|
| `gofmt -l` over the diff | clean (14 pre-existing offenders elsewhere, untouched) |
| `cd backend && go build ./... && go vet ./...` | clean |
| `go test` over 9 affected packages `-count=1 -v` | **548 PASS / 0 FAIL / 0 SKIP** |
| `cd frontend && pnpm exec tsc --noEmit` | clean |
| `pnpm exec vitest run` | **371 tests / 33 files** (baseline was 370) |
| `pnpm run build` | clean |
| `python3 corpora/validate.py` | tax=14 tds=13 tcs=5 keystroke=32 isolation=13 migration=6 forecast=4 inventory=6 gstr2b=17 ims=12 payroll=19 — identical to baseline |

The 9 packages were `internal/printing`, `internal/printing/printingdb`,
`internal/gststate`, `internal/server`, `internal/authz`, `internal/masters`,
`internal/platform/testdb`, `internal/archtest`, `internal/testhygiene`. Postgres
answered a real query immediately before **and** after that run, so the 0-skip census is
evidence rather than an artefact of a dead database.

### 1.2 Gates that DID NOT run — this is the top of the next session's list

**`make verify`**, **`make backend-test-residue`**, **`insightd` pytest**, and **every
live gate**: `verify:demo`, `verify:tenancy`, `e2e:ui`, `e2e:edit`, `e2e:numbering`,
`e2e:state-code`, `e2e:ocr`. Also the **statutoryd freeze gate** (which has no Makefile
target and is a standing carve-out).

Nothing in the commit message claims them. Do not assume any of them pass.

---

## 2. DO THIS FIRST — the live gate run T1 never got

Two things make this more than a formality, and both will bite if skipped.

**(a) The demo database predates migration 0039, so its parties have no address.** A tax
invoice without the recipient's address is refused under Rule 46(d) — correctly. So
`verify:demo`'s new print check will fail until the demo is re-seeded. **Re-seed before
judging the gate.**

**(b) A service principal is now refused `Print`.** `companyAuthMiddleware`'s `svc:`
fast path used to return before `routeAction()` ran; it now runs the mapping first and
403s any `authz.Print` plus any unmapped sub-path. **The statutoryd freeze gate uses a
service token** — confirm it still works. If it broke, that is a real regression in this
commit, not a pre-existing gap. `backend/internal/server/auth_middleware.go:180` is the
refusal.

```bash
# 0. the DB must be genuinely alive — see §4
docker start tally_pg; pg_isready -h localhost -p 5433 -t 5
PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"

# 1. the gates that were never run
make verify                      # includes migrate, build, residue, frontend, corpora, insightd
make backend-test-residue        # require per-table residue 0

# 2. a cored built FROM THIS TREE. Check the age of any running one first:
ps -eo pid,etime,comm | grep cored     # a stale process predates the commit; kill it
make run-cored                         # and make run-insightd in another shell

# 3. re-seed, because of (a) above
npm --prefix frontend run seed:all     # or seed-demo then seed-tenancy, in that order

# 4. live gates, ONE AT A TIME, nothing else running
npm --prefix frontend run verify:demo      # was 29/29 pre-T1; T1 adds 2 print checks
npm --prefix frontend run verify:tenancy   # was 113/113
npm --prefix frontend run e2e:ui           # was 38/38 — run it TWICE
# then e2e:edit, e2e:numbering, e2e:state-code, e2e:ocr

# 5. the standing carve-out: statutoryd freeze gate (needs a fresh service token
#    after any cored restart). Confirm C6's Print refusal did not break it.
```

**If `verify:demo`'s print check fails, read the response body before touching code.** It
was made deliberately diagnostic: a refusal returns JSON naming every missing Rule 46
particular, so `recipient.address1` means the demo data is stale (re-seed), not that the
pipeline is broken.

---

## 3. WHAT T1 ACTUALLY BUILT

```
internal/printing/            doc.go        the pinned document contract
                              pdf.go font.go page.go pdftext.go
                                            hand-rolled PDF 1.4 writer. No dependency,
                                            no font files, UNCOMPRESSED streams (so the
                                            bytes are greppable — verify-demo relies on
                                            this and asserts it), deterministic (no
                                            dates, no /ID) so goldens are stable.
                                            Base-14 AFM widths verified glyph-by-glyph
                                            against Adobe's AFMs and read back by poppler.
                              format.go words.go   Indian grouping (lakh/crore) + amount
                                            in words, decimal only.
                              invoice.go rule46.go tax invoice + the Rule 46 gate;
                                            bill of supply under rule 49.
                              voucherdoc.go delivery note, receipt, payment, credit/debit note
                              report.go     ONE generic statement renderer for all eight
                              testdata/     goldens: .pdf bytes AND .txt extracted text
internal/printing/printingdb/ voucher -> document assembly (READ ONLY, never touches
                              voucher_version) + the template override store
internal/gststate/            GST state code -> State name. Pure leaf, imports nothing.
internal/server/              handler_print.go, handler_print_reports.go
migrations/                   0037 print_template, 0038 print ACL, 0039 party address
```

Routes, pinned:

```
GET /v1/companies/{companyID}/print/vouchers/{class}/{voucherID}   -> (Print, <class>)
GET /v1/companies/{companyID}/print/reports/{reportName}           -> (Print, report:<name>)
```

### 3.1 Design decisions — settled, do not relitigate

- **PDF is server-side** so bytes are reproducible and testable. Golden tests assert on
  generated bytes **and** extracted text; never a screenshot.
- **The default template is in CODE.** 0037 stores only per-company overrides, so a
  company with zero rows prints on day one.
- **`Print` is its own ACL action**, so 0011's coarse `allow display report` does not
  reach it. 0038 grants `allow print` for the six classes that produce a document (never
  `payroll`) and writes **13 explicit `disallow print` rows** so a later `allow print *`
  cannot reach the wage bill, the priced supplier reconciliation, or cost prices.
- **`Print` is not a write**, so it inherits neither the back-dated cutoff nor WebAuthn
  step-up.
- **The class in the path is not trusted.** The ACL decision is made from the path class
  before anything is read, so the handler re-reads the **stored** class
  (`Store.VoucherClass`) and refuses a mismatch with **404, not 403** — a 403 would
  confirm the voucher exists. Without this, a caller allowed to print a sale could read a
  payroll voucher through the sales route.
- **Money is never re-derived for the page.** Every amount is rendered from the decimal
  the posting holds. This is the rule the discount bug (§3.2) broke.

### 3.2 The four blockers the review found, and why they are worth remembering

Every one was a *data* defect behind a *correct* renderer. This is the shape to expect
again on T6, T2 and T4.

1. **No tax invoice could print at all.** Rule 46(d) requires the recipient's address;
   the `ledger` party master had no address column. `RenderInvoice` refused every sale —
   correctly. Migration 0039 models it; `POST /ledgers` carries it; the demo seed sets it.
2. **A discount percentage was printed as rupees.** `stock_movement.discount_pct` (CHECK
   0..100) was assigned to a rupee field and rendered via `FormatMoney`, producing "Rate
   100.00, Qty 10, Less: Discount 2.50, Amount 975.00" — four figures that cannot all be
   true, on a document whose taxable value Rule 46(k) ties to the discount. The field is
   now `LineItem.DiscountPct` and renders as a percentage. **The name carries the unit
   because the unitless name is what caused it.**
3. **Names were drawn unwrapped** while every address line beneath them was wrapped, so an
   ordinary 69-character GST registration name overprinted the Rule 46(b) invoice number.
4. **No inter-State invoice could print.** Rule 46(n) wants the place of supply as a state
   *name*; this codebase works entirely in two-digit codes and nothing mapped one to the
   other. Hence `internal/gststate`.

### 3.3 `internal/gststate` — read this before touching it

Codes 01–38 plus 96/97/99, each with a citation and an effective-from date. The
dangerous entries are handled and **pinned by tests in both directions**:

- **25 `Daman and Diu` RETIRED** (Merger Act 44 of 2019, appointed day 2020-01-26);
  **26 `Dadra and Nagar Haveli and Daman and Diu`** is the surviving merged UT.
- **28 `Andhra Pradesh (before division)` RETIRED** (Act 6 of 2014, appointed day
  2014-06-02); **36 Telangana**, **37 Andhra Pradesh** are the successors.
- **38 Ladakh** is new (Act 34 of 2019, 2019-10-31); **01 keeps the name Jammu and
  Kashmir**.
- **96 and 99 are `StatusUnverified`** — the belief is recorded ("Other Country",
  "Centre Jurisdiction") but **not asserted**, and they are **refused** rather than
  guessed. **97 Other Territory** is named.

`Name()` answers only for `StatusCurrent`, so a retired or unverified code can never
reach paper. A retired code produces a refusal *with an explanation* — printing "Daman
and Diu" as a post-merger place of supply would be a wrong document rather than a missing
one. **If you extend this table, a wrong name is worse than an honest gap**: it prints the
wrong place of supply on a document a GST officer reads directly.

---

## 4. HOW TO WORK — corrections earned this session

Everything in `PLAN-TALLY-PARITY.md` §2 still applies. These are the amendments.

### 4.1 `nc -z localhost 5433` IS NOT A LIVENESS CHECK

`docker-proxy` holds the port open while the Postgres backend answers nothing. This
session the DB was unresponsive for ~10 minutes — `pg_isready` said "no response", `psql`
never got a reply, `docker ps` itself hung past 120s — while a `nc -z` watchdog logged
**nothing for the entire outage**. One agent's whole gate run was invalidated.

```bash
pg_isready -h localhost -p 5433 -t 5
PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"
```

Across 3.5 hours there were **24 unresponsive samples**, all traced to checkpoints taking
up to **170 seconds** under test load (`docker logs tally_pg` shows `write=170.284 s`).
The container never restarted (`RestartCount=0`). It always recovered on its own — so
**wait, re-run, and say that you did** rather than treating it as a failure. Put
`SET statement_timeout='60s'` / `lock_timeout='20s'` on psql work: one agent wedged
itself *and the shared DB* for ten minutes when its dry run collided with a concurrent
`migrations.Apply`.

### 4.2 Migrations apply themselves the moment the file exists

`backend/migrations/migrations.go` uses `//go:embed *.sql` and **every test's
`newServer()` calls `Apply`**. So a concurrent agent's test run applies your new `.sql`
before you can dry-run it. Consequences: a `BEGIN; … ROLLBACK;` proof may legitimately
report zero rows because the work is already done, and an idempotence proof against an
already-populated DB is weaker than one run before first application. **Say which you
got.** 0039 was applied in band this way and `make migrate` never ran against it.

There is **no central list of migrations to register.** (An earlier draft of the plan
said to add an entry to `internal/migration/expected.go`; that file is the *balances*
oracle for the migration reconciler and has nothing to do with the migration list. This
is corrected in the plan.)

### 4.3 Agents leave mutations behind — the gate run is what catches it

`roundOffBound` was raised from 1 to 100 to prove a test bites and **never restored**.
Its own new test caught it in the final gate run, at 547 PASS / **1 FAIL**. Two lessons:

- **Run the full scoped suite yourself before committing.** Do not accept "all slices
  COMPLETE" as evidence — a mutation left behind is invisible in an agent's own report.
- Sweep the diff for the specific constants reviewers said they mutated. The ones touched
  this session: the xref offset in `pdf.go`, `escapeLiteral` in `page.go`, the
  `authz.Print` refusal in `auth_middleware.go`, the `VoucherClass` check in
  `handler_print.go`, `gststate` code 25's status, and `roundOffBound`.

### 4.4 Retries, not the machine, are what makes a wave slow

Wave 2 spent **16 agent launches on 9 slices**; wave 3 spent **6 on 3**. A stalled agent
does not resume — it re-runs the slice from the beginning. Load average never exceeded
2.5 and the machine was never the constraint. Also: **an agent can die and the workflow
logs a clean result.** C3 died on `Connection closed mid-response`, returned nothing, and
the phase barrier passed as if it had run — its *code* had landed, its *evidence* had
not. `0 findings` from a review lens is **vacuous, not clean**; check
`agents_error` in the notification and read `journal.jsonl`.

### 4.5 The verification lenses are what made this commit trustworthy

The build wave reported 12/12 COMPLETE. Review then found **14 findings, 4 blockers**.
The fix wave reported 8/9 COMPLETE. The *verification* lenses then found **4 more
blockers** and graded 6 of 24 closure claims as not fully closed. Two specific patterns
worth reusing:

- **Make the lens re-derive, not re-read.** The lens that closed the unwrapped-name
  blocker parsed every `Tj` operator out of the content stream and re-measured each with
  `printing.Measure`. The one that closed the address blocker ran `cored` on a spare port
  against the live DB and fetched a real 200/`application/pdf` invoice over HTTP.
- **Demand a control assertion in every differential test.** The 0038 test PUTs
  `allow print *` and asserts 13 resources are still refused — but it *first* asserts
  `trial-balance` goes 403 → 200 across that PUT. Without the control, "still 403" could
  mean the rule never landed, and the other 13 assertions would be vacuous.

---

## 5. OPEN — carried forward deliberately

Ordered by what I would do first. None of these blocks the live gate run in §2.

| # | Item | Why it was not done |
|---|---|---|
| 1 | **`frontend/src/api/types.ts` + `mock.ts` drop the three address fields.** `CreateLedgerRequest` has no `address`/`city`/`pincode`, and the mock's `createLedger` silently discards them — so a future UI test that keys a party address and asserts it round-trips would pass against a mock that threw it away. | Out of the wave's ownership; cheap and worth doing first |
| 2 | **`internal/security/isolation_test.go`'s `tenantTables` is a hardcoded 7-table list.** It misses `print_template` — and already missed `employee`, `payslip`, `price_list`. `TestEveryTenantTableHasIsolationPolicy` and `TestForceRowLevelSecurity` therefore do not cover them. Should be catalogue-driven like `testdb`'s delete plan, which is anti-rot by construction. | Pre-existing rot that T1 made worse; needs its own slice |
| 3 | **Rule 46(e): a ≥Rs 50,000 supply to an unregistered party.** Now that `gststate` exists this may already work — **verify it before building anything.** | Untested path |
| 4 | **A party's address cannot be ALTERED.** `POST /ledgers` sets it; nothing changes it. `masters` has `SetLedgerOpening` and no general ledger-alter, so an operator who mistypes an address must delete and recreate the party — which its posted vouchers forbid. Needs `masters.SetLedgerAddress` + a `PUT` route + ACL decision. | A real gap for a real operator; own slice |
| 5 | **Bill of supply / HSN.** Wave 3 examined whether rule 49 genuinely requires an HSN or whether our check was over-strict. **Read `rule46.go`'s rule-49 arm and its caveats for what was decided** — do not assume, and do not re-relax it without reading. | Statutory judgement recorded in code |
| 6 | **`handler_print_reports.go` duplicates `printingdb`'s unexported `loadSupplier` verbatim** (same SELECT over `company`). Export one and delete the copy, or they will drift. | Cosmetic today, drift tomorrow |
| 7 | **The printed Day Book deliberately differs from the JSON one**: it validates `from`/`to` and 400s an unparseable date (the JSON handler passes them through), and prints one amount column with no total because `DayBook` lists voucher *versions*, so an altered voucher would contribute twice. **A judgement, not a spec** — decide whether it is the right one. | Needs a product call |
| 8 | **Data Entry gets no report print at all.** 0038 seeds zero `allow print report:NAME`. Fail-closed and reversible by an owner in one row, but nobody specified it. | Product call |
| 9 | **IRN invoices print without the rule 48(4) QR code**, and say so on their own face. The QR encodes the IRP-signed payload; no IRP/NIC call is possible here. Disclosed, not closed. | Cannot be closed in this environment |
| 10 | **EDLI premium and EPF/EDLI admin charges are unbooked from wage month June 2026** (~Rs 75 per member per month). Rates are `ConfUnverified`. **Still a release blocker; reading the primary G.S.R. is the fix, not a follow-up.** | Pre-existing, untouched by T1 |

**Never claimed, never validated:** the GSTN portal, the FVU, the NIC e-Way Bill API and
the EPFO/ESIC portals cannot be reached from this environment.

---

## 6. THEN — the next track

`PLAN-TALLY-PARITY.md` §5, with the renumbered migration blocks in §2.5:

1. **T6 TDS/TCS persistence, register and returns** — migrations **0049–0050**. The
   cheapest big win: the derivation engines already exist and are corpus-gated
   (`tds=13 tcs=5`), and a TDS leg is already verified against them. Nothing is persisted
   or reported. Follow the `payrollfile` emitter precedent and `WriteDisclosureSidecar`.
   **The FVU cannot run here.**
2. **T2 cost centres** — **0040–0041**. Data-model heavy; do it before Budgets (T11)
   depends on it. The sum-to-posting invariant is a CONSTRAINT TRIGGER, not a form check.
3. **T4 bank reconciliation** — **0045–0046**. Self-contained, high daily value.
4. **T3 orders + BOM** — **0042–0044**. An order is a committed quantity, **not** a
   movement; do not push it through `stock_movement`.

**Expect T1's shape to repeat:** the engine usually exists and the *data model* is what is
missing. On T6, before building the register, check that every field Form 26Q/27Q needs is
actually stored — a blank PAN is a rejected return, not a partial one.

Workflow shape that worked, and the one thing to change: three parallel chains partitioned
by file ownership → **one** owner for the shared wiring files → parallel adversarial
lenses → **then run the full scoped suite yourself** before committing. Give every agent
an explicit ownership list, the §4.1 DB liveness rule, and a structured-output schema with
`status`, `reproduced`, `mutationProof`, `skipCensus`, `handoffs`, `caveats`.
