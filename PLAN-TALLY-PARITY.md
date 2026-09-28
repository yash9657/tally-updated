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
- **`nc -z localhost 5433` IS NOT A LIVENESS CHECK, and a watchdog built on it lies.**
  `docker-proxy` holds the port open even when the Postgres backend has stopped answering.
  During T1 the database was unresponsive for roughly ten minutes — `pg_isready` returned
  "no response", `psql` never got an answer to an SSLRequest, and `docker ps` itself hung
  past 120s — while a TCP-connect watchdog logged **nothing** for the whole outage and one
  agent's entire gate run was invalidated. Check with `pg_isready -t 5` **and** a real
  query (`psql -tAc "select 1"`). Set `statement_timeout`/`lock_timeout` on any psql work
  that could collide with a concurrent `migrations.Apply`.
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

> **⚠️ THIS TABLE IS THE ONLY AUTHORITY. Resolved 2026-07-31.** The Tier-2 table in §7 of
> this file carried its own migration column that was **stale by one** for T8–T14 (it said
> T8 0050–0051, T9 0052, T10 0053–0054, T11 0055–0056, T12 0057, T13 0058, T14 0059). Those
> numbers have been **struck from §7** — a second copy of a reservation table is a collision
> waiting for two parallel tracks, which is the exact failure this section exists to prevent.
> If you find a migration number anywhere else in this repo, it is not authoritative; this is.

| Track | Reserved | Status |
|---|---|---|
| T1 Printing | 0037–**0039** | consumed |
| T2 Cost centres | 0040, **0041 POISONED** | **consumed — 0040 tables. ⛔ 0041 IS PERMANENTLY UNUSABLE AND LEFT EMPTY.** 0040 is `cost_category` + `cost_centre` + `posting_cost_allocation`, plus a `UNIQUE (company_id, id)` on `posting` so a tenant table can FK to it company-scoped, plus the **per-(posting, category) DEFERRED CONSTRAINT TRIGGER** — the invariant is per AXIS and never posting-wide, because several categories allocate the SAME money in full and in parallel (one Rs 1,00,000 posting across Departments AND again across Branches), so a posting-wide check refuses correct data and no check lets cost go silently unattributed. 0041 was reserved for T2's ACL and **cannot carry one**: the highest-numbered `seed_company_auth_defaults` restatement is the only body that survives, so rules at 0041 are overwritten on every fresh database. **The T2 ACL is 0072 instead.** |
| T3 Orders + BOM | 0042–0043, **0044 POISONED** | **consumed — 0042 + 0043 tables. ⛔ 0044 IS PERMANENTLY UNUSABLE AND LEFT EMPTY, exactly as 0041 is.** 0042 is `order_head` + `order_line` + `order_fulfilment`, plus a `UNIQUE (company_id, voucher_version_id, line_no)` on `stock_movement` so a fulfilment can FK to a real inventory LINE rather than assert one, plus **two DEFERRED CONSTRAINT TRIGGERS**. ⛔ **ONE `order_head` WITH A DIRECTION ENUM, NOT `sales_order` + `purchase_order`** — a deliberate departure from this table's original sketch, on the house pattern 0025 records ("exactly one primitive, a positive quantity plus a direction"): two head tables mean two copies of every constraint and index, and a shared `order_line` cannot FK to two parents without a polymorphic key. What is genuinely asymmetric between the two — who may READ each pipeline — is an authorisation difference and is expressed in 0073 as two report names. ⛔ **NO `fulfilled_qty` AND NO `status` COLUMN, EVER**: both are derived by summing `order_fulfilment` across CURRENT, non-deleted voucher versions, exactly as stock VALUE is derived and never stored. A stored figure goes stale undetectably the first time a fulfilling voucher is ALTERED, because an ALTER writes a NEW version and re-states its links without visiting the order. `is_closed` IS stored, because "abandoned" cannot be computed — an undelivered order and a cancelled one have identical fulfilment histories. **Over-fulfilment is REFUSED** (the §T3 decision, taken once): it costs nothing, because a fulfilment qty is a LINK and not a movement — ship 105 against a 100-unit order, record a movement of 105 and a link of 100, and the extra 5 correctly consumed no order. 0043 is `bom_head` + `bom_component`; the recipe is stored as written with an `output_qty` YIELD and is **never pre-divided per unit**, or a 10kg-makes-3 recipe truncates at NUMERIC(19,4) and shorts every batch forever. **The T3 ACL is 0073 instead** — see the row below. |
| T4 Bank reconciliation (slice 1) | 0045–0046 | consumed |
| T5 e-Way Bill | 0047–0048 | **0047 CONSUMED** (`0047_e_way_bill.sql`); **0048 still free**. ⛔ T5 took NO ACL number: `e-way-bill` is a bare resource token, so Data Entry's `default_access = 'none'` denies it with zero `acl_rule` rows — see HANDOFF §6.1 |
| T6 TDS/TCS persistence | 0049–0050 | **consumed** — 0049 liability + s.394(2) declarations, 0050 `tds_deduction` |
| **T6 TCS collection persistence (Item Zero step 3)** | **0067** | **consumed** — `tcs_collection`, the collection-side mirror of 0050 |
| **T6 write path (challan entry + deductor profile)** | **none needed** | **T6 IS COMPLETE AND ITS ROW IS `on`.** `tds-challan` and `tds-profile` are BARE RESOURCE TOKENS, and the seeded Data Entry level's `default_access` is `none` with every rule naming a specific token — so both are refused with no rule written. Measured on a freshly-migrated database. A `report:NAME` would have been the opposite case and MANDATORY (0011's coarse `allow display report` reaches any report name at specificity 1), which is why the shape was chosen deliberately rather than by analogy with the register beside it |
| **T6 register ACL (Item Zero step 4)** | **0068** | **consumed** — `disallow display/print report:tds-register` for Data Entry |
| **T6 challan register (the return's prerequisite)** | **0069** | **consumed** — `tds_challan` + `tds_challan_allocation`. Promoted ahead of the return because Deductee Detail field 5 is a CHALLAN REFERENCE: no deductee row can exist outside a challan |
| **T6 deductor return profile** | **0070** | **consumed** — `company_tds_profile`: TAN, PAN, Annexure 4 category, statutory address, responsible person. `company` has none of them, and the TAN is not the GSTIN |
| **⛔ Seeder apply-order repair (was reserved for the T6 write-path ACL)** | **0071** | **consumed — A SECURITY REPAIR, NOT A FEATURE.** `0063_bank_statement_acl` was authored a day after `0068_tds_register_acl`, derived from it correctly, and numbered five LOWER — so `sort.Strings` apply order let 0068's older 10-PERFORM `seed_company_auth_defaults` body overwrite 0063's correct 11-PERFORM one. On a **fresh database** every new company's Data Entry level therefore lacked `disallow display/print report:bank-statement`, and 0011's coarse `allow display report` **granted the imported bank statement**. Measured on a scratch database, not reasoned about. 0071 re-runs the backfill and restates the seeder from 0063's body. **T6's write path needs no ACL rows of its own** — `tds-challan` / `tds-profile` are bare resource tokens and Data Entry's `default_access` is `none`, the same recorded decision as `deductor-status` |
| T8 Multi-currency | 0051–0052 | free |
| T9 FIFO/LIFO | 0053 | free |
| T10 Stock groups + units | 0054–0055 | free |
| ~~T11 Budgets~~ | ~~0056–0057~~ | **ABANDONED — never taken, and PERMANENTLY UNUSABLE.** T11 shipped at **0085–0086** (see the row below). ⛔ These two must stay unused rather than being recycled for something else: a rule seeder written at 0056 would sort BELOW 0071/0076/0086 and be overwritten by their bodies on every fresh database — which is the exact defect 0071 exists to repair |
| T12 GSTR-9 | **0087** | **consumed** — ACL only; see the detailed row below. **No table number was taken.** ⚠️ This row read "and 0088 stays free" until 2026-08-28; **T4 slice 2b then took 0088**, which is exactly what the reservation intended. |
| T13 Interest | see §2.5 | free |
| T14 Backup/restore | 0060–0061 | ⛔ **STALE ROW, CORRECTED 2026-08-28: T14 IS ALREADY BUILT.** It shipped on another lane (`backend/internal/backup/`, `handler_backup.go`, Alt+Y > Data) and took **no migration number** — `backup` is a BARE token in `authz.vocabulary`, so Data Entry's `default_access = 'none'` refuses it with zero `acl_rule` rows written. **0060–0061 are therefore still free**, and this row said "free" for the right reason and the wrong one. ⚠️ A session was sent to build T14 by a handoff that read this table; the lesson is recorded in `HANDOFF-AFTER-T12.md` §4 — **the plan is a reservation document, not the state of the tree**, and `FeaturesScreen.tsx` is the closest thing to ground truth |
| **T4 slice 2a — statement IMPORT** | **0062–0064** | **consumed** — 0062 `bank_statement_import`+`bank_statement_line`, 0063 `bank-statement` ACL, 0064 `ledger.bank_account_number`/`bank_ifsc` |
| **T4 slice 2b — statement MATCHING** | **0088** | **CONSUMED 2026-08-28** — `0088_bank_statement_match.sql`. See the detailed row below |
| **T8 step 7 — the unadjusted-forex report** | **0090** | **CONSUMED 2026-09-03.** `0090_unadjusted_forex_acl.sql` — `disallow display` **and** `disallow print` for `report:unadjusted-forex`, seeder restated from **0087** (16 → **17 PERFORMs**, comment-stripped body diff is exactly one line), plus the backfill; measured after apply: **321/321 Data Entry levels covered**, both rows. ⭐ **THE NUMBER IS AT THE END AND NOT IN T8's OWN 0081–0084 GAP** — see that row above: a file restating the seeder must sort ABOVE 0087 or its body is overwritten on every fresh database (the 0071 defect). T8 added no table after 0080, so the four reserved slots were never needed and are now recorded as permanently free. ⛔ **THE DENY IS NOT RECORDED AS AN ASSUMPTION**, unlike 0086's: 0068 already refuses the supplier-and-customer list with amounts and 0072 departmental profitability, and this is that list restricted to foreign counterparties PLUS the rates transacted on PLUS the company's unhedged position as one signed total. Reversing it means **DELETING the disallow**, not adding an allow (they tie at specificity 3 and Disallow wins). ⭐⭐ **IT IS **ONE** REPORT NAME COVERING BOTH SIDES — THE OPPOSITE CALL FROM `bills-payable` (D94), ARGUED IN 0090's HEADER RATHER THAN INHERITED:** the subject is the NET exposure to a currency, and a \$1,000 receivable against a \$1,000 payable at one rate is an exposure of NOTHING — so splitting it would not restrict information, it would destroy the only figure the report exists to produce; and unlike the payables ageing, no form in the product asks for it, so denying it costs no operator any work. ⭐ **AND UNLIKE EVERY EARLIER FILE IN THIS FAMILY THE `print` ARM IS TESTABLE:** those reports all answer 501, so their Owner control could only assert 501. This one really renders — Owner 200, clerk 403 — which makes the `disallow print` row the only thing between a coarse print grant and a PDF of the whole exposure. Steps 4, 5 and 6 took **no migration at all**: 0080 has every column, and `forex-settings` is a BARE token (default-denied with zero rows) |
| **T15 Export (Excel/CSV/XML) — if it needs an ACL migration** | **0065** | **free (NEW)** |
| **T16 Per-company print template UI** | **0066** | **free (NEW)** |
| **T3 order-book ACL (taken ABOVE the poisoned block)** | **0073** | **consumed** — `disallow display` **and** `disallow print` for `report:sales-order-book` and `report:purchase-order-book`, for Data Entry. Mandatory for the reason 0072's row gives. ⛔ **TWO REPORT NAMES AND NOT ONE `order-book` WITH A `?direction=`, AND THAT IS WHAT MADE AN ACL MIGRATION NECESSARY AT ALL: a query parameter is invisible to the PDP.** `routeAction` decides on the path, so one shared name would make *"the salesperson may see our sales pipeline but not what we have committed to pay suppliers"* an **unwritable rule** — an ordinary rule an ordinary business wants, because the two lists belong to two different jobs. Note this goes the OPPOSITE way from 0042's schema (one table, one direction column) and neither should be "made consistent" with the other: two tables would duplicate constraints, while one report name would destroy an authority. Derived verbatim from 0072 (12 → 13 PERFORMs; comment-stripped diff is exactly one line) and verified on a database migrated from empty and seeded through the real `seed_company_auth_defaults`, with **two controls** — `report:bank-statement` still 2 rows (the line 0068 once dropped) and the four `report:cost-%` rows 0072 seeds still present. The order and BOM MASTERS took **no** rows: `sales-order`, `purchase-order` and `bom` are bare tokens against a level whose `default_access` is `none`, the same authority T6 shipped `tds-challan` under. ~~**0073 is now the highest-numbered restatement — derive the next one from it.**~~ ⛔ **STALE — CORRECTED 2026-08-27. `0076_forecast_report_acl.sql` is the highest-numbered restatement (14 PERFORMs in the function body). DERIVE FROM 0076.** Re-measure rather than inherit this line; the command is in `HANDOFF-NEXT-UNBLOCKED.md` §2. This is the second time this sentence has gone stale, which is why it now names the measurement instead of a number. |
| **T2 cost-report ACL (taken ABOVE the poisoned block)** | **0072** | **consumed** — `disallow display` **and** `disallow print` for `report:cost-centre-summary` and `report:cost-category-summary`, for Data Entry. Mandatory, not optional: 0011's coarse `allow display report` reaches any `report:NAME` at specificity 1, so a new report is GRANTED the moment it exists and the REFUSAL is what must be written. Both forms, because a report that cannot be displayed but CAN be printed is a leak with a paper trail. Derived verbatim from 0071 (11 -> 12 PERFORMs; comment-stripped diff is exactly one line) and verified on a database migrated from empty with a CONTROL row from an earlier migration to prove nothing was dropped. The cost MASTERS took **no** rows — `cost-centre` is a bare token against a level whose `default_access` is `none`, the same authority T6 shipped `tds-challan` under |
| **⛔ T8 multi-currency — RESERVED GAP, NOW PERMANENTLY UNNEEDED** | **0081–0084** | ⛔ **STILL DO NOT TAKE THESE, BUT THE REASON HAS CHANGED — UPDATED 2026-09-03.** T8 consumed **0080** for its whole schema and, through steps 4, 5, 6 and 7, **has added no table since** — so this block was never needed and is now recorded as permanently free rather than reserved. ⭐ **T8's ACL MIGRATION TOOK 0090, NOT ONE OF THESE, AND THE REASON IS APPLY ORDER RATHER THAN ETIQUETTE:** `migrations.go` applies files in `sort.Strings` order and the last `CREATE OR REPLACE FUNCTION seed_company_auth_defaults` wins, so a body installed at 0081 would be overwritten on every fresh database by 0086's and then 0087's — sixteen PERFORMs, none of them T8's — and the rules would simply not exist for any new company. That is exactly the defect **0071** exists to repair. **The rule is: a file that restates the seeder takes a number at the END; a file that only creates a TABLE may take a reserved one.** ⚠️ This row's `DO NOT TAKE` was always addressed to OTHER lanes, never to T8 itself — a T8 file was free to take 0081 for a table, and there was never a table to put there. **Leave them unused rather than renumbering** — this section's own rule |
| **T11 Budgets** | **0085–0086** | **CONSUMED 2026-08-28.** `0085_budgets.sql` — `budget` (name + period) and `budget_line` (dimension GROUP/LEDGER/COST_CENTRE, basis CLOSING_BALANCE/NETT_TRANSACTIONS, side, positive NUMERIC(19,4)), both RLS ENABLE+FORCE with USING and WITH CHECK. `0086_budget_report_acl.sql` — `disallow display` **and** `disallow print` for `report:budget-variance`, seeder restated from **0076** (14 → **15 PERFORMs**, comment-stripped body diff is exactly one line), plus the backfill; measured after apply: 1 row per (level, effect, action), 303/303 Data Entry levels covered. ⚠️ **THE DENY IS AN ASSUMPTION, NOT AN ANSWERED QUESTION** — it follows 0072's precedent for the cost reports because the owner has not ruled; reversing it means **DELETING the disallow**, not adding an allow (they tie at specificity 3 and Disallow wins). The MASTER token `budget` is BARE and takes **zero rules**, deliberately. ⚠️ 0081–0084 remain the T8 gap and were not touched |
| **T12 GSTR-9** | **0087** | **CONSUMED 2026-08-28.** `0087_gstr9_acl.sql` — `disallow display` **and** `disallow print` for `report:gstr9`, seeder restated from **0086** (15 → **16 PERFORMs**, comment-stripped body diff is exactly one line), plus the backfill; measured after apply: 1 row per (level, effect, action), 303/303 Data Entry levels covered. ⭐ **T12 TOOK NO TABLE NUMBER AT ALL, SO 0088 WAS LEFT FREE — AND T4 SLICE 2b TOOK IT ON 2026-08-28** (the row below). The reservation allowed for storage if the reconciliation needed it; it does not. GSTR-9 reads the twelve GSTR-1s, the twelve GSTR-3Bs and the postings, and a report is not a table. ⚠️ **THE NAME TAKEN IS `report:gstr9`, NOT THE `report:gstr-9` THIS ROW RESERVED** — a deliberate departure, argued in 0087's header and pinned by `TestTheAnnualReturnIsSpelledLikeItsTwoSiblings`: the two names it sits beside in `authz.KnownReportNames` are `gstr1` and `gstr3b`, served at `/gstr1` and `/gstr3b`, and one member of that list spelled with a hyphen for no visible reason is how an administrator writes a rule for a resource that does not exist and silently governs nothing. ⛔ Do not "correct" it back: the rules would then govern a name no route emits, 0011's coarse `allow display report` would reach the real name unopposed, and the rule list would say "refused" while the report was served. **The deny is NOT recorded as an assumption**, unlike 0086's — 0035 already refuses payroll totals to this level and 0072 departmental profitability, and a document strictly more disclosive than both, which additionally names the company's filing risk, raises no new question. ⚠️ 0081–0084 remain the T8 gap and were not touched |
| **T4 slice 2b — statement MATCHING** | **0088** | **CONSUMED 2026-08-28.** `0088_bank_statement_match.sql` — the `bank_statement_match` table, plus two additive composite UNIQUEs on 0045's and 0062's tables that its three-column FKs need. ⭐ **IT STORES RESOLUTIONS, NOT CANDIDATES**, departing from the memo's own word "candidate links": proposals are DERIVED on every read, because a stored candidate goes stale silently — a voucher altered, deleted or cleared between the proposal and the acceptance leaves the operator choosing from a list that no longer describes the books while still looking authoritative. ⭐ **AND IT WRITES NO ACL RULE, WHICH IS A DECISION AND NOT AN OMISSION** — the seeder census still reads **16 PERFORMs, unchanged from 0087**, and that number is the assertion. Every 2b route lands on an authority an existing migration already governs: the proposals read is `Display / report:bank-statement` (0063 disallows it), both set-aside writes are `Create / bank-statement` (0063 grants Data Entry nothing), and ⛔ **ACCEPTING A MATCH IS THE EXISTING `PUT .../bank-reconciliation/{ledgerID}/{voucherID}/{legNo}`** with an optional `statement_line_id` added to its body, rather than a route of its own. That is the slice's load-bearing choice: matching must not become a SECOND WAY TO ACQUIRE THE AUTHORITY TO ASSERT A CLEARANCE, and sharing the route also keeps the back-dated cutoff (`peekBankDate` reads `bank_date` from that body), the WebAuthn step-up and invariant B-2 working without any of them being re-implemented. The match row carries a composite FK to `bank_reconciliation` **ON DELETE CASCADE**, which makes B-8 structural: un-ticking a bank date and un-linking a match are ONE operation. ⚠️ 0081–0084 remain the T8 gap and were not touched |
| **W0 agentic foundations — the per-tenant LLM token budget** | **0091** | **CONSUMED 2026-09-08.** `0091_llm_token_budget.sql` — the `llm_token_budget` table (one row per company × calendar month × feature), written by `internal/llm`'s `PGBudget` under **RESERVE-THEN-SETTLE**. ENABLE **and** FORCE RLS with USING **and** WITH CHECK, the `(company_id, id)` company-scope UNIQUE, and nine named CHECKs. ⭐ **IT WRITES NO ACL RULE, WHICH IS A DECISION AND NOT AN OMISSION** — the seeder census still reads the same **17 PERFORMs it read after 0090**, and that number is the assertion. No route reaches this table in W0 at all: it is written inside the RLS-bound session of whichever feature spent the tokens, `routeAction` gains no arm, and an unmapped `{companyID}` sub-path is default-DENY. ⛔ **AND WHEN A ROUTE DOES ARRIVE — a spend report, an admin cap editor — IT MUST BE A BARE RESOURCE TOKEN (`llm-budget`), NOT A `report:NAME`**: a bare token is refused with zero `acl_rule` rows because Data Entry's `default_access` is `none` (the authority `tds-challan`, `tds-profile`, `forex-settings`, `backup` and `e-way-bill` all ship under), while a report name makes an ACL migration MANDATORY under D95. Choose the shape deliberately then, as T6 did, rather than by analogy with whatever report sits next to it in the menu. ⭐ **THERE IS NO MONEY COLUMN, AND THAT IS THE OTHER DECISION**: token counts are BIGINT, and a rupee figure would need a price that depends on provider, model id, cache state and date — four things this table does not know and one it cannot know retrospectively. A cost computed from a stale price list reads as authoritative forever; cost belongs with the trajectory store, which records which model actually **served** each call. ⚠️ The number is **0091 and not one of the free 0081–0084**, even though this file restates no seeder and by §2.5's own rule could have taken a reserved slot: 0090 is the highest applied number, and spending a reservation on a table that needs no gap buys nothing. **0081–0084 remain the T8 gap and were not touched** |
| **W2 agent plane — intent log, autonomy, runs/approvals, delegation** | **0092–0094 (0095 unused)** | **CONSUMED 2026-09-28** on `agentic/w2-backend` (D121). **0092** `agent_intent_log` + `agent_intent_anchor` + `agent_intent_unanchored_companies()` — append-only by trigger AND by grant (`SELECT, INSERT` only), BLAKE3 chain per run computed in Go. **0093** `agent_autonomy` (company, user\|NULL, acl_key, amount band in paise) → L0..L3, no row = L1. **0094** `agent_run` (the one mutable agent table; carries `created_by_subject` and the delegation snapshot), `agent_run_step` (durable steps under FORCE RLS — not `statutory_step`), `agent_approval`, `agent_decision` (ONE per approval: what makes expiry terminal), `agent_commit` (idempotency, inserted in the voucher's own transaction), `agent_runs_due()` (SECURITY DEFINER, ids only). All ENABLE+FORCE RLS, USING+WITH CHECK, `(company_id, id)` UNIQUEs; idempotent (applied twice in BEGIN/ROLLBACK). ⭐ **NO SEEDER RESTATED AND NO ACL RULE WRITTEN** — the routes use the bare tokens `agent-run` and `agent-approval`, refused to Data Entry by `default_access = 'none'` with zero rows; the seeder census is unchanged. **0095 stays unused** (delegation is a snapshot column on `agent_run`, not a table). ⭐ **0095 CONSUMED 2026-09-28 by W2 BREADTH** (branch `agentic/w2-breadth`, D121 "Wave 2 breadth"): `0095_agent_breadth.sql` — NO NEW TABLE. (1) `ledger.created_by_run / blocked_at / blocked_reason / unblocked_by / unblocked_at` with three named CHECKs and the `posting_ledger_not_blocked` BEFORE INSERT trigger on `posting`: an AGENT-CREATED LEDGER IS BORN BLOCKED and nothing can post to it until a person unblocks it (`POST /ledgers/{id}/unblock`); (2) `agent_commit.object_kind / object_ids` (a commit can now write a master, an order or a bank tick; `ADD COLUMN` with a constant default rewrites no row, so 0094's append-only trigger never fires); (3) `agent_decision.edited_preview` (the approver's edit, dry-run at the decision). Applied twice inside BEGIN/ROLLBACK on `tally_w2_breadth` (exit 0). ⭐ **NO ACL RULE WRITTEN, THE SEEDER CENSUS UNCHANGED** — the two new route authorities are BARE tokens per the 0091 row: `master-unblock` (releasing an agent's party for payment) and `agent-autonomy` (the autonomy table), both refused to Data Entry by `default_access = none` with zero rows, both classed governance in `internal/actions` so no agent can ever use either. |
| **W3 data understanding — catalogue, tenant profile, entity facts, embeddings, Tier B role/views** | **0096–0099** | **0096 + 0097 CONSUMED 2026-09-28 (branch `agentic/w3`, ADR `docs/design/d122-w3-data-understanding.md`); 0098–0099 UNUSED and stay reserved for W3's owed embeddings / golden-query store.** `0096_entity_facts_and_tenant_profile.sql` — `entity_facts`, `entity_edges`, `tenant_profile`: bi-temporal (`valid_from/valid_to` × `recorded_at/superseded_at`), append-only except `superseded_at` (trigger `w3_only_supersede` + **no DELETE grant**), ENABLE **and** FORCE RLS with USING **and** WITH CHECK, `(company_id, id)` company-scope UNIQUE, named CHECKs, a partial one-current unique index. `0097_agent_ro_and_published_views.sql` — roles `agent_ro` (NOLOGIN NOBYPASSRLS NOINHERIT, SELECT on `agent.*` views only) and `agent_view_owner` (NOLOGIN NOBYPASSRLS, owns the views — ⛔ **a superuser-owned view bypasses RLS; measured: re-owning `agent.items` to postgres leaked tenant A to tenant B**), `GRANT agent_ro TO tally_app WITH INHERIT FALSE, SET TRUE`, schema `agent` with 8 `security_barrier` views. ⚠️ **Roles are cluster-wide**; both guarded by IF NOT EXISTS. ⭐ **NEITHER FILE WRITES AN ACL RULE**: no route reaches W3 yet, the DSL entity rides the existing `query` token, and the seeder census stays at **17 PERFORMs**. When a tenant-profile edit route arrives it must be a **bare token** (`agent-profile`), per the 0091 row. ⛔ **Both files were once applied to the SHARED `tally` DB by test harnesses that ignored the lane's DSN** (`migrations/master_name_index_census_test.go` hardcoded; `internal/server/einvoice_http_test.go` ignored `CORED_SKIP_DB`) — both harnesses fixed on this branch, and the objects dropped from `tally` by hand (tables were empty) |
| **W5 documents + bank statements on open weights — provenance, idempotency keys** | **0100–0101** | **CONSUMED 2026-09-28** (branch `agentic/w5`, ADR `docs/design/d123-w5-documents-bank.md`). `0100_voucher_idempotency.sql` — `voucher_idempotency` (company_id, idempotency_key) PK, request_sha256, company-scoped FK to `voucher_head`, stored result; ENABLE+FORCE RLS with USING and WITH CHECK; **INSERT-only by BOTH halves** (no-mutate trigger + `GRANT SELECT, INSERT`). `0101_bank_statement_proof_and_narration.sql` — `bank_statement_import.balance_proof / balance_declared_by / row_order` and `bank_statement_line.txn_rail / counterparty / counterparty_vpa / rail_reference`, all nullable with named CHECKs; no new table. ⭐ **NEITHER WRITES AN ACL RULE, which is a decision**: no route is added — the key rides `POST /vouchers/{class}` (already per-class), the proof rides the existing `bank-statement` import — so the seeder census is unchanged (17 PERFORMs). `buildFixture` gained a `voucher_idempotency` row (trap §7.8) |
| **W4 insights / W6 channels / W7 learning (wave 2)** | **0102–0109** | **RESERVED 2026-09-28** — W4 0102–0104, W6 0105–0106, W7 0107–0109. Not to be taken by wave 1 |
| **W1a golden sets — tau suite, extraction set, narration retrieval eval** | **none** | **CONSUMED NOTHING, 2026-09-28, by design** (d124). Everything W1a built is a fixture (`corpora/tau`, `corpora/extraction`, `corpora/embedding`) or a judge (`internal/evals/tau`, `insightd/evals`), and a judge that owned a table would be a judge that could be written to. It adds no route, no `routeAction` arm and no ACL rule; the seeder census is untouched. Each trial runs in a freshly created company through the existing HTTP API and is dropped by `testdb.Cleanup` |

**T6 CAME BACK FOR A THIRD NUMBER, AND TOOK 0067 RATHER THAN RENUMBERING.** 0049–0050 were
reserved on the assumption that TCS needed only the declaration table. It does not: wiring
`TCSDerive` into the post path showed that a *collection* has nowhere to be recorded, so it
would have been derived, verified and discarded — the exact defect 0050 exists to end on the
deduction side. Its block was consumed, so per the rule above it gets a new number at the end
instead. **0067 was the first free number; the register ACL then took 0068, so Group B's blocks now
continue from 0073** (0069-0070 T6 challan + profile, 0071 the seeder repair, 0072 T2's cost-report
ACL).

**⛔ AND THE RULE THAT RESERVED BLOCKS DO NOT SURVIVE CONTACT WITH `seed_company_auth_defaults`.**
Six migration headers say "derive your restatement from the immediately preceding one", and every
author obeyed. It broke anyway: **`CREATE OR REPLACE` order is decided by the FILENAME, not by
authorship**, so 0063 — written later, derived correctly, numbered into its reserved block below
0068 — lost the race to an older body and silently dropped a refusal. `migrations/seeder_census_test.go`
now guards this **statically, with no database**, because the defect lives in apply order and is
therefore invisible on any incrementally-migrated DB (it was green on every developer machine).
**A seeder restatement may never take a number below an existing restatement, reserved or not:
leave the gap unused and take a number at the end.**

**T4 SLICE 2 SPLIT INTO 2a AND 2b, AND THE BLOCK WAS RE-ALLOCATED WITHIN ITSELF.**
`docs/design/t4-slice2-model.md` §8.3–8.4 records why: the import and the matching are two
user-visible slices, 2a needs an ACL migration of its own, and the match table is 2b's. So
0063 became the ACL rather than the match table, and 2b's table takes a new number at the end —
the same call T6 made when it took 0067. **Group B's blocks now continue from 0073** — 0071 was consumed by the seeder apply-order repair and **0072 by T2's cost-report ACL**, which is now the highest-numbered restatement. See HANDOFF-NEXT-T3-T5-PARALLEL.md §2: **every reserved block below the highest restatement is unusable for an ACL SEEDER change**, because the highest-numbered restatement is the only body that survives. T5's 0047-0048 and all of Tier 2's 0051-0061 are TABLE numbers only; a seeder change in any of those tracks takes a fresh number at the end.

**⛔ THE HIGHEST-NUMBERED RESTATEMENT IS NOW `0073_order_book_acl.sql` (13 PERFORMs). DERIVE FROM IT.**
T3 consumed 0042-0043 for tables and took **0073** for its ACL, leaving its reserved **0044 permanently
empty** — the second reserved ACL block to be poisoned this way after T2's 0041. Two tracks have now hit
the same wall for the same reason, so treat a reserved ACL number as a *number that will not work* rather
than as a number to try: **reserve blocks for TABLES, and take the seeder number at the end, every time.**

**T4 slice 2 does NOT take 0047.** `HANDOFF-CLOSING-THE-GAP.md` §3.2 said "migration 0047",
which is **T5's reserved block**. Slice 2 gets its own block at the end (0062–0064) instead,
because 0045–0046 already exist and the rule is *renumber unstarted blocks, never renumber a
migration that exists* — and because T5 and T4-slice-2 are both live candidates for parallel
work, so the overlap would have been a real collision rather than a theoretical one.

**T1 took a third number and everything below it shifted by one.** 0037 (templates) and
0038 (ACL) were the reservation; the review then found that no tax invoice could print at
all, because Rule 46(d) requires the recipient's address and the `ledger` party master had
no address column — so **0039** models it. Nothing had yet been written against 0040+, so
renumbering the unstarted blocks cost nothing and keeps the file ordered. **Renumber
unstarted blocks; never renumber a migration that exists.**

There is **no central list of migrations to update**. `backend/migrations/migrations.go`
discovers them with `//go:embed *.sql` and applies them in lexical order, so a new file is
picked up by its name alone. (An earlier draft of this plan said to add an entry to
`internal/migration/expected.go`; that file is the *balances* oracle for the migration
reconciler and has nothing to do with the migration list. Do not go looking for it.)

**Migrations are `go:embed`'d and every test's `newServer()` calls `Apply`**, so the moment
a new `.sql` file exists on disk a concurrent agent's test run can apply it to the dev
database. Two consequences: a `BEGIN; … ROLLBACK;` dry run may legitimately report zero
rows because the work is already done, and an idempotence proof run on an already-populated
database is weaker than one run before first application. Say which you got.

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

**Migrations:** 0040 (schema), 0041 (ACL — a cost report exposes departmental
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

**Migrations:** 0042 (orders), 0043 (BOM), 0044 (ACL).

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

**Migrations:** 0045 (schema), 0046 (ACL — a bank statement is sensitive).

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

**Migrations:** 0047 (schema) — TAKEN. ⛔ **0048 (ACL) WAS NOT NEEDED AND WAS NOT TAKEN.** Deciding
the resource SHAPE first is what saved it: `e-way-bill` is a BARE RESOURCE TOKEN, not a
`report:NAME`, and a token the seeded rules never mention is denied by `default_access = 'none'`.
An e-way bill REGISTER report, if one is ever added, DOES need an ACL migration — writing both
`disallow display` and `disallow print`, in the same commit as the report (HANDOFF §6.1, §6.2).

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

**Migrations:** 0049 (schema), 0050 (ACL — these carry PAN per deductee).
**Design memo (§3.2 gate):** `docs/design/t6-model.md`.

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
| **T8** | **Multi-currency** | Only bites exporters, but when it bites it is the whole ledger. A foreign-currency amount, a rate, and the base-currency amount are **three stored values**, not two plus arithmetic. Realised vs unrealised gain/loss are different postings. | see §2.5 |
| **T9** | **FIFO / LIFO valuation** | v1 is weighted average (Tally's default) behind a **valuer interface whose state is a strict subset of what FIFO needs** — `ValuerFor` in `internal/inventory`. So FIFO is a NEW ARM plus fixtures, not a schema change. Closing stock now feeds the Balance Sheet, so a valuation change moves the sheet: re-pin `verify:demo`. | see §2.5 |
| **T10** | **Stock groups + compound units** ("Box of 12") | Items are flat and units are bare symbols today. A conversion factor turns every quantity in the system into a *unit-qualified* quantity — touch the movement primitive with care, and never store a converted quantity you can re-derive. | see §2.5 |
| **T11** | **Budgets** | A budget is a comparison dimension, not a posting. Budget-vs-actual on groups, ledgers and (after T2) cost centres. | see §2.5 |
| **T12** | **GSTR-9 annual return** | Builds on GSTR-1 + 3B + the new ITC engine. It is a *reconciliation* of the year's returns to the books; it must state where it disagrees rather than smoothing it. Not filing-ready without portal-schema verification — keep the disclosure discipline. | see §2.5 |
| **T13** | **Interest calculation** | On overdue receivables/payables. Simple vs compound, and the day-count basis, are configuration with statutory defaults — cite them. | see §2.5 |
| **T14** | **Backup / restore, split company data** | ⛔ **BACKUP/RESTORE IS BUILT (corrected 2026-08-28); SPLIT COMPANY DATA IS A DECLARED NON-GOAL.** The original note, kept because its reasoning still holds: operationally important and currently absent. Must be tenant-exact: a restore that leaks one company's rows into another is worse than no backup. Reuse the `testdb` delete-plan census — it already knows every tenant-scoped table and is anti-rot by construction. | see §2.5 |

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
9. **T11 Budgets** → **T12 GSTR-9** → **T4 slice 2b (bank matching)**. ⛔ **CORRECTED 2026-08-28.** This step used to read "→ **T13 Interest** → **T14 Backup/restore**" and both were wrong by then: **T14 was ALREADY BUILT** on another lane (see its §2.5 row), and **T13 is blocked on statutory documents a human must read** — the day-count basis and simple-vs-compound are configuration with statutory defaults, and inventing them would be worse than the gap (`docs/design/t13-interest-model.md`). T4 slice 2b was the last unblocked Tally-parity track and shipped on 2026-08-28.

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
  routes, tables and code** against Tally's feature set — which is why a usability
  pass was budgeted here, to catch the papercuts that do not show up as a missing
  table. ⭐ **THE FIRST HALF OF THAT PASS RAN 2026-08-30:
  `docs/usability-day-2026-08-30.md`.** It produced **D106** — Outstandings and the
  Balance Sheet disagreeing by an on-account receipt, which no inventory could have
  found because nothing is missing — plus seven smaller papercuts, all in the daily
  loop. ⛔ **THE WRITE HALF IS STILL UNDRIVEN**: voucher entry has nowhere safe to
  run, because the only populated books are the demo company the gates assert
  against. That pass needs a throwaway company first, and §4 of the write-up says so.
- That Tier 2 is exhaustive. Tally has thirty years of accumulated surface: POS,
  job costing, ODBC, data synchronisation, payroll attendance/leave, and more. What
  is here is what a typical Indian SMB would miss first.
- Any estimate of how long a track takes. The tracks are sized by risk, not by hours.
