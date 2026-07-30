# HANDOFF — T1 shipped; the road to Tally parity

> **Two jobs, in this order.**
>
> 1. **§2 — run T1's live gates.** T1 is committed and its live gates have never run.
>    Do not build on top of unverified work.
> 2. **§3 — adopt the data-model-first discipline before writing a line of any new
>    track.** T1 took three waves because the engine was right and the *schema* was
>    missing. §3 is the procedure that stops that repeating. **It is not optional and it
>    is not advice — it is a required, committed artefact per track.**
>
> `PLAN-TALLY-PARITY.md` is the catalogue of tracks. This file is how to execute them
> and what is still open. §6 is the complete parity gap in one place.

---

## 1. STATE

Branch `master`, HEAD **`6229e73`**; T1 is **`c9e2126`**. Working tree **clean**.
Migrations run to **0039**.

T1 (printing) shipped after three waves: a build wave (12 agents), a fix wave for the
review's 4 blockers (9 agents), and a third wave for the 4 blockers the *verification*
lenses found (4 agents). `PLAN-TALLY-PARITY.md` §3 T1 and T7 are done; §5 item 1 is
complete. Migration blocks in §2.5 were renumbered — T1 took **0037–0039** and every
unstarted block below shifted by one.

### 1.1 Gates that ran at `c9e2126`

| Gate | Result |
|---|---|
| `gofmt -l` over the diff | clean (14 pre-existing offenders elsewhere, untouched) |
| `cd backend && go build ./... && go vet ./...` | clean |
| `go test` over the 9 affected packages `-count=1 -v` | **548 PASS / 0 FAIL / 0 SKIP** |
| `cd frontend && pnpm exec tsc --noEmit` · `pnpm run build` | clean |
| `pnpm exec vitest run` | **371 tests / 33 files** (baseline 370) |
| `python3 corpora/validate.py` | tax=14 tds=13 tcs=5 keystroke=32 isolation=13 migration=6 forecast=4 inventory=6 gstr2b=17 ims=12 payroll=19 — identical to baseline |

Packages: `internal/printing`, `internal/printing/printingdb`, `internal/gststate`,
`internal/server`, `internal/authz`, `internal/masters`, `internal/platform/testdb`,
`internal/archtest`, `internal/testhygiene`. Postgres answered a real query immediately
before **and** after that run, so the 0-skip census is evidence, not an artefact of a
dead database.

### 1.2 Gates that DID NOT run

**`make verify`**, **`make backend-test-residue`**, **`insightd` pytest**, and **every
live gate**: `verify:demo`, `verify:tenancy`, `e2e:ui`, `e2e:edit`, `e2e:numbering`,
`e2e:state-code`, `e2e:ocr`, plus the **statutoryd freeze gate** (no Makefile target;
standing carve-out). Nothing in the commit claims them. Assume nothing.

---

## 2. DO THIS FIRST — T1's live gate run

Two things will bite if skipped.

**(a) The demo database predates migration 0039, so its parties have no address.** A tax
invoice without the recipient's address is refused under Rule 46(d) — *correctly*. So
`verify:demo`'s print check fails until the demo is re-seeded. **Re-seed before judging.**

**(b) Service principals are now refused `Print`.** `companyAuthMiddleware`'s `svc:` fast
path used to return before `routeAction()` ran; it now runs the mapping first and 403s any
`authz.Print` plus any unmapped sub-path (`auth_middleware.go:180`). **The statutoryd
freeze gate uses a service token** — confirm it still works. If it broke, that is a
regression in `c9e2126`, not a pre-existing gap.

```bash
# 0. the DB must be genuinely alive — see §5.1
docker start tally_pg; pg_isready -h localhost -p 5433 -t 5
PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"

# 1. never-run gates
make verify                       # migrate, build, residue, frontend, corpora, insightd
make backend-test-residue         # require per-table residue 0

# 2. cored built FROM THIS TREE — check the age of any running one first
ps -eo pid,etime,comm | grep cored      # a stale process predates the commit; kill it
make run-cored                          # + make run-insightd in another shell

# 3. re-seed, because of (a)
npm --prefix frontend run seed:all

# 4. live gates, ONE AT A TIME, nothing else running
npm --prefix frontend run verify:demo      # 29/29 pre-T1; T1 adds 2 print checks
npm --prefix frontend run verify:tenancy   # 113/113
npm --prefix frontend run e2e:ui           # 38/38 — run it TWICE
# then e2e:edit, e2e:numbering, e2e:state-code, e2e:ocr

# 5. statutoryd freeze gate (fresh service token after any cored restart)
```

**If the print check fails, read the response body before touching code.** It is
deliberately diagnostic: a refusal returns JSON naming every missing Rule 46 particular,
so `recipient.address1` means stale demo data (re-seed), not a broken pipeline.

---

## 3. THE STANDING RULE — DATA MODEL FIRST

> **The data model is no longer allowed to be the thing that turns out to be missing.**

### 3.1 Why this rule exists — the evidence, not a slogan

T1's engine was right on the first pass. **All four review blockers, and the worst two
found by verification, were the same defect:** the code that computes was correct and the
column that stores was absent or mislabelled.

| What broke | Root cause |
|---|---|
| **No tax invoice could print at all** | Rule 46(d) requires the recipient's address; `ledger` had no address column. The renderer refused every sale — correctly. |
| **No inter-State invoice could print** | Rule 46(n) wants the place of supply as a state *name*; the whole codebase works in two-digit codes and nothing mapped one to the other. |
| **A discount % printed as rupees** | `stock_movement.discount_pct` (0..100) assigned to a field named `Discount` and rendered with `FormatMoney`: "Rate 100.00, Qty 10, Less: Discount 2.50, Amount 975.00" — four figures that cannot all be true. |
| **Credit notes could not print** | The renderer required a `Reason`; no column holds one. |
| **`RoundOff` absorbed any residual** | A derived value with no bound, under a label ("Round Off") that asserts it is small. Rs 500 of freight would print as a rounding. |

Each cost a full wave. **Every one was findable in an afternoon of schema reading before
any code was written.**

### 3.2 The required artefact — `docs/design/<track>-model.md`

**Before any implementation agent is launched for a track, commit a design memo.** It is
reviewed adversarially like code, and the build does not start until it is accepted. It
must contain all five sections:

**(1) THE OUTPUT INVENTORY.** Enumerate every field that must appear on every artefact the
track produces — each document, each report, each statutory file, each screen. For a
statutory output, enumerate from the **form or the rule itself**, with a citation and an
effective-from date per field. Not from what seems reasonable.

**(2) THE PROVENANCE TABLE.** *This is the section that would have prevented all of T1's
blockers.* One row per field from (1):

| Field | Required by | Stored where | Verdict |
|---|---|---|---|
| recipient address | Rule 46(d) | *nowhere* | **MIGRATION** |
| place of supply (name) | Rule 46(n) | `ledger.state_code` (code only) | **NEEDS DERIVATION — new leaf** |
| deductee PAN | Form 26Q | `ledger.pan` | OK |
| challan BSR code | Form 26Q | *nowhere* | **MIGRATION** |

**Verify every "stored where" against the live database with `\d <table>`, not from
memory and not from a model summary.** A field whose verdict is MIGRATION or NEEDS
DERIVATION is decided *here* — with its migration number reserved — not discovered at
review. **A field that cannot be stored is a refusal that must be designed, not a blank
that appears on paper.**

**(3) UNITS AND SCALE ON EVERY NUMERIC FIELD, AT EVERY SEAM.** Name the unit in the
*identifier*, not only in a comment: `DiscountPct` not `Discount`, `AmountPaise` not
`Amount` if that is what it is. State the decimal scale (the ledger stores 4; documents
print 2 — say what rounds where, and in which direction). **A unitless numeric name that
crosses a package boundary is a defect in review.**

**(4) INVARIANTS, EACH WITH THE TEST THAT PINS IT.** For every derived or aggregate
value, state the invariant *and* how it is enforced. "By the form" is not enforcement.
Enforcement is a `CHECK`, a constraint trigger, a deferred constraint, or a test that
fails from **both** sides of a boundary. The `RoundOff` bug was an invariant that existed
only as prose; `roundOffBound` could be raised from 1 to 100 and no test noticed.

**(5) THE EDGE-CASE LEDGER.** Every entry answers: what is the behaviour, and which test
names it? §3.3 is the minimum list.

### 3.3 The edge-case checklist — every one of these has bitten a real ledger

Work it for each track. An entry you decide is not applicable must say *why*.

**Money and arithmetic**
- Zero. Negative (a credit note, a reversal, a refund). The smallest unit (0.01).
- Rounding: which direction, at which scale, and does the same value round identically on
  every surface it appears on? **Words and figures on one invoice must agree** — the
  ledger stores 4 decimals and paper shows 2.
- A value that does not reconcile: **refuse, or print as given and make the discrepancy
  visible. Never silently adjust.** Never re-derive a stored figure.
- Very large values (crores) and the formatter's behaviour at the top of its range.

**Time**
- Financial-year boundary. Period boundary (a report's from/to). A back-dated entry.
  Books-beginning-from as distinct from FY start. A voucher dated in a **filed** period.
- Effective-from dates on every statutory rate: what does the engine do for a wage month
  or quarter *before* the earliest rate it knows?

**Voucher lifecycle — the one this codebase makes easy to get wrong**
- `voucher_version` is **immutable and hash-chained**. An alter writes a **new version**.
  So: does your report read the *current* version only, or every version? **The printed
  Day Book got this wrong** — it lists versions, so an altered voucher contributes twice.
- A **cancelled/deleted** voucher must not feed a report, a return, or date another
  document. Prove it with a test named for the breach.
- New data on a voucher must live **inside the hashed canonical payload** (follow
  `inward_doc`), or an alter silently drops it.

**Tenancy — every new table, every time**
- RLS `ENABLE` **and** `FORCE`. A policy with `USING` **and** `WITH CHECK` (USING alone
  lets a tenant write a row it can never read). Composite `(company_id, id)` FKs.
- Cross-tenant refusal proven **by the FK**, not merely by RLS. A row in `buildFixture`.

**Authorization**
- A `routeAction` mapping for every route (unmapped `{companyID}` sub-paths default-DENY).
- Is the resource token derived from the **path** anything a caller can spoof? T1's print
  route re-reads the **stored** voucher class and refuses a mismatch with **404, not 403**
  — a 403 confirms the record exists.
- The seeded `Data Entry` level has a coarse `allow display report` matching every
  `report:NAME` at specificity 1. **Every sensitive new report needs an explicit
  `disallow` at specificity 2.**

**Absent and partial data**
- Every nullable column: what does the output do when it is null? Blank on paper is
  almost never right — refuse and name the field.
- Partial states are usually the *normal* case (partial fulfilment, partial clearing,
  partial payment). Over-completion is a decision to state once, not to discover.

### 3.4 Where "no mistakes" actually comes from

Absolute correctness is not something a test suite can promise, and I will not claim it.
What is achievable — and what this codebase already does well in places — is that **every
number is either independently verified or explicitly refused.** Four mechanisms carry it,
and all four already exist here. Use them; do not invent a fifth.

1. **Independent oracles.** `corpora/validate.py` re-derives the tax corpora from scratch
   in plain Python — a second implementation, not a re-run of the first. Extend it for
   every new statutory engine. A test that calls the code under test to compute its own
   expectation proves nothing.
2. **Refuse rather than guess.** `gststate` marks codes 96/99 `StatusUnverified` and
   refuses them instead of printing a plausible name. `Rate.Use` refuses a
   `ConfUnverified` rate. **This is the single most important habit in the project.** A
   confidently wrong number is a tax notice; a refusal is a support call.
3. **Machine-checked invariants.** `posting_balanced`, the residue bound, RLS proven by FK,
   `migration.ReconcileFull` to the paisa. If an invariant lives only in a comment, it is
   not enforced — see `roundOffBound`.
4. **Adversarial verification that re-derives.** §5.5.

**And the standing prohibitions:** money is `decimal.Decimal` server-side and scaled
integers client-side — never `float64`, `ParseFloat`, `.Float64()`, `Number()`,
`parseFloat`. Every statutory rate carries a citation and an effective-from date or is
marked `UNVERIFIED`. **Never invent a rate, a threshold or a clause letter to make a test
pass** — T1 had to remove a fabricated `Rule 53(1A)(d)`. The GSTN portal, the FVU, the NIC
e-Way Bill API and the EPFO/ESIC portals **cannot be reached from this environment**;
never claim a validation that was not performed.

### 3.5 Worked example — T6, ready to use

Ten minutes against the live DB, and the shape of T6 is already visible:

- `internal/compliance/tds.go:36` **says it in its own comment**: *"no schema records the
  section / deductee / derived amount, so no report."*
- `\d` sweep: **no challan/TDS/TCS/deduction table exists at all.** No `section` column
  anywhere outside `gst_inward_document.section` (a GST field, not TDS).
- `ledger.pan` **does** exist — deductee PAN is available.

So the provenance table already tells you T6 needs, *before any register is written*:
**(a)** the TDS/TCS section per transaction, **(b)** the derived TDS/TCS amount persisted
against the voucher version inside the hashed payload, and **(c)** a challan/deposit table
— Form 26Q cannot be filed without BSR code, challan serial number, date of deposit and
amount. A session that builds the register first discovers (c) at review. **That is the T1
story, and it is avoidable in an afternoon.**

Also settle in the memo: is the deductee's *address* required on the return? The section
code — per transaction, per ledger, or per voucher line? Lower-deduction certificate
numbers (206AA / 197)? Higher rate where no PAN is on record — the engine knows the rule,
does anything record *whether* a PAN was on record at deduction time? **A blank PAN is a
rejected return, not a partial one.**

---

## 4. WHAT T1 BUILT

```
internal/printing/            doc.go        the pinned document contract
                              pdf.go font.go page.go pdftext.go
                                            hand-rolled PDF 1.4 writer: no dependency, no
                                            font files, UNCOMPRESSED streams (verify-demo
                                            greps them and asserts they stay uncompressed),
                                            deterministic (no dates, no /ID) so goldens are
                                            stable. Base-14 AFM widths verified glyph-by-
                                            glyph against Adobe's AFMs; read back by poppler.
                              format.go words.go   Indian grouping (lakh/crore) + amount in
                                            words, decimal only
                              invoice.go rule46.go tax invoice + Rule 46 gate; bill of
                                            supply under rule 49
                              voucherdoc.go delivery note, receipt, payment, credit/debit note
                              report.go     ONE generic renderer for all eight statements
                              testdata/     goldens: .pdf bytes AND .txt extracted text
internal/printing/printingdb/ voucher -> document assembly (READ ONLY, never touches
                              voucher_version) + template override store
internal/gststate/            GST state code -> State name. Pure leaf, imports nothing.
internal/server/              handler_print.go, handler_print_reports.go
migrations/                   0037 print_template, 0038 print ACL, 0039 party address
```

```
GET /v1/companies/{companyID}/print/vouchers/{class}/{voucherID}   -> (Print, <class>)
GET /v1/companies/{companyID}/print/reports/{reportName}           -> (Print, report:<name>)
```

**Settled decisions — do not relitigate.** PDF is server-side so bytes are reproducible
and testable (goldens assert bytes *and* extracted text, never a screenshot). The default
template is in **code**; 0037 stores only overrides, so a fresh company prints on day one.
`Print` is its own ACL action, so 0011's coarse `allow display report` does not reach it;
0038 grants `allow print` for the six document-producing classes (never `payroll`) and
writes 13 explicit `disallow print` rows so a later `allow print *` cannot reach the wage
bill. `Print` is not a write — no back-dated cutoff, no WebAuthn step-up. The path class
is **not trusted**: the handler re-reads the stored class and 404s a mismatch.

**`internal/gststate` — read before extending.** Codes 01–38 + 96/97/99, each with a
citation and effective-from date. **25 `Daman and Diu` RETIRED** (Merger Act 44 of 2019,
2020-01-26) with **26** the surviving merged UT; **28 RETIRED** (Act 6 of 2014,
2014-06-02) with **36 Telangana** / **37 Andhra Pradesh** successors; **38 Ladakh** new
(Act 34 of 2019) while **01 keeps the name Jammu and Kashmir**; **96 and 99
`StatusUnverified`** — belief recorded, not asserted, and **refused** rather than guessed.
`Name()` answers only for `StatusCurrent`, so a retired code can never reach paper.
**A wrong state name is worse than an honest gap** — it prints the wrong place of supply on
a document a GST officer reads.

---

## 5. HOW TO WORK — corrections earned the hard way

`PLAN-TALLY-PARITY.md` §2 still applies. These are the amendments.

### 5.1 `nc -z localhost 5433` IS NOT A LIVENESS CHECK

`docker-proxy` holds the port open while Postgres answers nothing. This session the DB was
unresponsive for ~10 minutes — `pg_isready` said "no response", `psql` never got a reply,
`docker ps` hung past 120s — while a `nc -z` watchdog logged **nothing for the entire
outage**, and one agent's whole gate run was invalidated.

```bash
pg_isready -h localhost -p 5433 -t 5
PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"
```

24 unresponsive samples over 3.5 hours, all checkpoints taking up to **170 s** under test
load (`docker logs tally_pg`: `write=170.284 s`). The container never restarted
(`RestartCount=0`) and always recovered on its own — so **wait, re-run, and say you did**.
Put `SET statement_timeout='60s'` / `lock_timeout='20s'` on psql work: one agent wedged
itself *and the shared DB* for ten minutes colliding with a concurrent `migrations.Apply`.

When the DB is down every DB-backed test `t.Skip`s and `go test` still prints `ok` and
exits 0. **Census every run you cite:**
`go test <pkg> -count=1 -v 2>&1 | grep -c -- '--- SKIP'` → require **0**, and bracket long
runs with a live query before *and* after.

### 5.2 Migrations apply themselves the moment the file exists

`migrations.go` uses `//go:embed *.sql` and **every test's `newServer()` calls `Apply`**,
so a concurrent agent's test run applies your new `.sql` before you can dry-run it. A
`BEGIN; … ROLLBACK;` proof may legitimately report zero rows because the work is already
done, and an idempotence proof against an already-populated DB is weaker than one run
before first application — **say which you got.** 0039 was applied in band this way and
`make migrate` never ran against it.

**There is no central list of migrations to register.** (An earlier plan draft said to add
an entry to `internal/migration/expected.go`; that file is the *balances* oracle for the
reconciler. Corrected in the plan.)

**Every migration must be idempotent**: `applyOne` wraps each file in one transaction and
stamps `schema_migrations` inside it, so a non-re-runnable file **walls off every later
migration**. `ADD COLUMN IF NOT EXISTS` is fine; `ADD CONSTRAINT` and `CREATE POLICY` need
a `DO $$ … IF NOT EXISTS … $$` guard.

### 5.3 Agents leave mutations behind — only your own gate run catches it

`roundOffBound` was raised 1 → 100 to prove a test bites and **never restored**. Its own
new test caught it in the final gate run (547 PASS / **1 FAIL**).

- **Run the full scoped suite yourself before committing.** "All slices COMPLETE" is not
  evidence — a left-behind mutation is invisible in the agent's own report.
- Sweep the diff for the constants reviewers said they mutated.
- **Never `git add -A`.** Stage explicit paths and grep the staged list for `zz_`,
  `scratch`, `probe`, `.bak`.

### 5.4 Retries are what make a wave slow, and a dead agent looks clean

Wave 2 spent **16 launches on 9 slices**; wave 3, **6 on 3**. A stalled agent does not
resume — it re-runs the slice from scratch. Load average never exceeded 2.5; the machine
was never the constraint.

**An agent can die and the workflow logs a clean result.** C3 died on `Connection closed
mid-response`, returned nothing, and the phase barrier passed as if it had run — its
*code* had landed, its *evidence* had not. **`0 findings` from a review lens is vacuous,
not clean.** Check `agents_error` in the notification and read `journal.jsonl`.

### 5.5 The verification pass is what made T1 trustworthy

Build reported 12/12 COMPLETE → review found **14 findings, 4 blockers**. Fix reported
8/9 → verification found **4 more blockers** and graded 6 of 24 closure claims as not
fully closed. **Reported-complete means nothing.** Two patterns to reuse:

- **Make the lens re-derive, not re-read.** The lens that closed the unwrapped-name
  blocker parsed every `Tj` operator out of the content stream and re-measured with
  `printing.Measure`. The one that closed the address blocker ran `cored` on a spare port
  against the live DB and fetched a real 200/`application/pdf` invoice over HTTP.
- **Demand a control assertion in every differential test.** The 0038 test PUTs
  `allow print *` and asserts 13 resources are still refused — but *first* asserts
  `trial-balance` goes 403 → 200 across that PUT. Without the control, "still 403" could
  mean the rule never landed and the other 13 assertions would be vacuous.
- **A mutation that does not bite is a finding.** Restore byte-identically
  (`shasum -a 256`) and say so. **Verify a finding before acting on it** — one prior
  reviewer's recommended fix was itself wrong and broke the Balance Sheet. `REFUTED` is a
  good outcome.

### 5.6 Workflow shape

```
phase('Design')   ONE agent per track writes docs/design/<track>-model.md (§3.2),
                  then ONE adversarial reviewer on the memo. No build until accepted.
phase('Fix')      3 concurrent chains, partitioned by FILE OWNERSHIP
phase('Wire')     ONE owner for server.go + auth_middleware.go + vocabulary.go
phase('Review')   2–3 adversarial lenses in parallel
THEN              run the full scoped suite YOURSELF, then the live gates alone
```

Give every agent an explicit ownership list, the §5.1 DB rule, "do not edit files outside
your list — report as HANDOFF", and a structured-output schema with `status`, `reproduced`,
`mutationProof`, `skipCensus`, `handoffs`, `caveats`. Run **one workflow at a time** (8
cores; each caps at 6 agents). Live gates run **alone**.

---

## 6. THE COMPLETE PARITY GAP

`FeaturesScreen.tsx` is the honesty surface and is accurate as of `c9e2126`: **40 rows —
22 on, 2 structural, 16 not built.** A feature count is the wrong unit, so this is ordered
by *who it stops*.

### 6.1 The spine is done

Double-entry vouchers, immutable hash-chained edit log, bill-wise details with opening
breakup, credit/debit notes, opening balances, single-entry mode, numbering series, GST
(GSTR-1, 2A/2B, 3B, IMS, ITC), statutory payroll, inventory with closing stock feeding the
Balance Sheet, e-invoice/IRN, auth + Tally's Disallow/Allow ACL + WebAuthn, offline sync,
and now printed documents. That is the correctness-critical half, and it is corpus-gated
rather than merely written.

### 6.2 The 16 gaps

| Gap | Who it blocks | Track |
|---|---|---|
| **Bank reconciliation** | **Every business, every month.** Zero code. **This alone means nobody can switch off Tally today.** | T4 |
| **TDS / TCS persistence, register, returns** | Anyone with a TDS obligation. Engines exist and are corpus-gated (`tds=13 tcs=5`); nothing is stored or filed. **Cheapest big win** — see §3.5 | T6 |
| **Cost centres and cost categories** | Very widely used in Tally; no allocation dimension on postings at all | T2 |
| **Order processing (Sales/Purchase Orders) + BOM** | Most trading businesses run on them | T3 |
| **e-Way Bill** | Legally required to move goods over Rs 50,000 | T5 |
| **Multi-currency** | Only exporters — but for them it is the whole ledger | T8 |
| **FIFO / LIFO / batch-wise valuation** | v1 is weighted average. Closing stock feeds the Balance Sheet, so a valuation change moves the sheet — re-pin `verify:demo` | T9 |
| **Stock groups + compound units** ("Box of 12") | Items are flat, units are bare symbols | T10 |
| **Budgets and budget-vs-actual** | Depends on T2 for the cost-centre dimension | T11 |
| **GSTR-9 annual return** | Annual filers | T12 |
| **Interest on overdue bills** | Anyone charging it | T13 |
| **Backup / restore, split company data** | Operationally important, absent. Must be tenant-exact | T14 |
| **Export (Excel/CSV/XML/e-mail)** | Daily annoyance. `authz.Export` exists as an action and is unmapped/default-DENY | — |
| **Per-company print templates (UI)** | 0037 built the table and the store; **nothing writes an override**, so every company prints the built-in layout | — |
| **Rule 48(4) QR on a printed e-invoice** | Companies above the e-invoicing threshold. **Cannot be closed here** — the QR encodes the IRP-signed payload and no IRP/NIC call is possible. The document says so on its face | — |
| **SSO / OIDC login** | Password login, lockout and WebAuthn exist; SSO does not | — |

### 6.3 Three gaps that no feature list shows — and they matter

1. **T1 shipped a pipeline with no button.** Printing is reachable over **HTTP only**;
   there is no print key anywhere in `frontend/src`. An operator cannot print the invoice
   from the screen they keyed it on. **For a keystroke-compatible product this is a parity
   gap, not a nicety** — and it is small work.
2. **Nobody has driven this app for a day as a shopkeeper.** Every gap above was found by
   inventorying routes, tables and code, which finds *missing tables* and not the
   papercuts that make software unpleasant. **Budget a usability pass** before anyone says
   parity. The gaps it finds will not look like this table.
3. **Tally has thirty years of surface this catalogue does not enumerate**: POS, job
   costing, ODBC, data synchronisation, payroll attendance and leave, and more.
   `PLAN-TALLY-PARITY.md` §7 says Tier 2 is explicitly not exhaustive.

### 6.4 Where this product is ahead

The hash-chained audit trail is **structural** rather than a toggle (Tally's Edit Log can
be switched off; this cannot). Conversational NL query over the books and OCR bill capture
have no Tally equivalent. And **keystroke compatibility** (32 corpus cases) is the thing
that lets an operator switch without retraining — the real moat, and worth protecting in
every track.

### 6.5 Honest assessment

**The correctness core is strong and roughly done; the daily-workflow surface is about
60–70% there.** Two tracks (T4 bank rec + T6 TDS/TCS) get a straightforward GST
trading/services SMB to *usable*; four or five to *comfortable*. The remaining work is
well understood and mostly not research — **but that is exactly what was said about T1**,
which took three waves because the data model kept being the gap. §3 exists so that
sentence stops being true.

---

## 7. OPEN FROM T1

Ordered by what I would do first. None blocks §2.

| # | Item |
|---|---|
| 1 | **`frontend/src/api/types.ts` + `mock.ts` drop the three address fields.** `CreateLedgerRequest` has no `address`/`city`/`pincode` and the mock's `createLedger` silently discards them — a UI test that keys an address and asserts a round-trip would pass against a mock that threw it away. Cheap; do it first. |
| 2 | **`internal/security/isolation_test.go`'s `tenantTables` is a hardcoded 7-table list.** It misses `print_template` — and already missed `employee`, `payslip`, `price_list`. Should be catalogue-driven like `testdb`'s delete plan, which is anti-rot by construction. |
| 3 | **Rule 46(e): a ≥Rs 50,000 supply to an unregistered party.** Now that `gststate` exists this may already work — **verify before building.** |
| 4 | **A party's address cannot be ALTERED.** `POST /ledgers` sets it; nothing changes it. An operator who mistypes must delete and recreate the party, which its posted vouchers forbid. Needs `masters.SetLedgerAddress` + `PUT` route + ACL decision. |
| 5 | **Bill of supply / HSN.** Wave 3 examined whether rule 49 truly requires an HSN or whether our check was over-strict. **Read `rule46.go`'s rule-49 arm and its caveats for what was decided** — do not assume, do not re-relax without reading. |
| 6 | **`handler_print_reports.go` duplicates `printingdb`'s unexported `loadSupplier` verbatim.** Export one and delete the copy or they will drift. |
| 7 | **The printed Day Book deliberately differs from the JSON one**: it validates `from`/`to` and 400s a bad date, and prints one amount column with no total because `DayBook` lists voucher *versions* (an altered voucher would contribute twice). **A judgement, not a spec — needs a product call.** See §3.3. |
| 8 | **Data Entry gets no report print at all.** 0038 seeds zero `allow print report:NAME`. Fail-closed and reversible in one row, but unspecified. |
| 9 | **EDLI premium and EPF/EDLI admin charges unbooked from wage month June 2026** (~Rs 75 per member per month; rates `ConfUnverified`). **Release blocker. Reading the primary G.S.R. is the fix, not a follow-up.** Do not invent a rate. |

---

## 8. NEXT

Per `PLAN-TALLY-PARITY.md` §5, with renumbered blocks. **Each starts with its §3.2 design
memo.**

1. **T6 TDS/TCS** — migrations **0049–0050**. Engines exist and are corpus-gated. §3.5 has
   the provenance work already started: section, persisted amount, **and a challan table**.
   The FVU cannot run here.
2. **T2 Cost centres** — **0040–0041**. Before Budgets (T11) depends on it. The
   sum-to-posting invariant is a **constraint trigger**, not a form check; allocations live
   inside the hashed payload; parallel allocation per category is not one column on
   `posting`.
3. **T4 Bank reconciliation** — **0045–0046**. Highest daily value of anything left. The
   difference must reconcile **to the paisa**; reconciliation is metadata on the posting and
   must not mutate the voucher; never auto-clear an ambiguous match.
4. **T3 Orders + BOM** — **0042–0044**. An order is a **committed quantity, not a
   movement** — do not push it through `stock_movement` or closing stock (and the Balance
   Sheet) is corrupted. Partial fulfilment is the normal case.

Then T9 → T10 (both touch inventory; serialise), T8, T11 → T12 → T13 → T14, plus the
small items in §6.2 and §6.3.
