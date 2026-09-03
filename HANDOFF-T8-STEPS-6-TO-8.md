# HANDOFF — T8 STEPS 6–8. SETTLEMENT IS ENFORCED; THE OPERATOR-VISIBLE HALF IS NOT BUILT.

**Written:** 2026-09-03, working tree CLEAN at `21ec34f` · **Branch:** `session/t32-and-close-7-3`
**Read time: 10 minutes.** ⭐ **Every claim in §1, §3 and §4 was MEASURED at `21ec34f`** and the
command is beside it. ⛔ **Where a measurement CONTRADICTS the previous handoff, §6 says so and shows
both commands** — two of its numbers are right and the obvious way to check them gives the wrong
answer.

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** It is self-contained.
§1–§2 get you a running app you can safely break. §3 is exactly what exists. **§4 is the work, and
its order is not yours to choose.** §7 is the house standard your work must meet.

⛔⛔ **DO NOT RE-DESIGN T8. `docs/design/t8-multi-currency-model.md` ANSWERS SIX QUESTIONS AND THE
ANSWERS WERE GIVEN BY THE REPO OWNER.** §5 tells you which parts you need. ⚠️ **ONE of its formulas
was measured wrong and replaced in step 5 — §3.4 — and that is the ONLY licence this file grants.**

---

## 0. ⭐ THE ONE-LINE VERSION

**A foreign bill can now be keyed, settled and its realised gain enforced — but only through the API,
and NO REPORT KNOWS A FOREIGN BILL IS FOREIGN.** Steps 1–5 of the memo's build order are done.

```bash
sed -n '/^type Bill struct/,/^}/p' backend/internal/reporting/outstanding.go | grep -c "Currency\|Foreign"
#   0   ⛔ Outstandings has ONE money column and every bill lands in it

grep -rn "unadjusted\|forex" frontend/src/keystroke/types.ts frontend/src/screens/GatewayScreen.tsx
#   (nothing)  ⛔ no report name, no Gateway entry

grep -n "^type State" frontend/src/screens/FeaturesScreen.tsx
#   type State = "always" | "on" | "server" | "off";   ⛔ `partial` STILL does not exist
```

⭐ **T8 IS STILL THE WHOLE OF WHAT IS BUILDABLE.** The other lock-line item, **T13 (interest), is
BLOCKED ON A HUMAN** — it needs day-count and simple-vs-compound statutory defaults sourced into
`docs/statutory/` and cannot be started.

---

## 1. THE STATE — verbatim, and what each number should be

```bash
cd /Users/yashbhalgat/code/tally-updated
git log --oneline -1          # 21ec34f feat(T8/5): a foreign bill settled at another rate…
git status --porcelain        # (empty)
```

**Gate baselines at `21ec34f`, ALL RE-RUN AT THAT COMMIT ON 2026-09-03. Any red is yours.**

⚠️ **EVERY LINE IS RUN FROM THE REPO ROOT** and each `cd` is in its own subshell for that reason.
⛔ **AND `cd X && nohup … &` RUNS THE `cd` IN THE BACKGROUND SUBSHELL**, so the parent shell never
moves and the next line reads the wrong directory. Use absolute paths in any block that backgrounds
something. ⚠️ **`make` TARGETS LIVE IN THE ROOT `Makefile` ONLY** — there is no `backend/Makefile`.
⚠️ **AND THE `Bash` TOOL'S WORKING DIRECTORY DID NOT PERSIST RELIABLY ACROSS CALLS THIS SESSION** —
a `cd backend` in one call was gone by the next, and three commands failed with *"directory prefix .
does not contain main module"*. **Use absolute paths in every call.**

```bash
(cd backend && go build ./... && go vet ./... && go test ./... -count=1)  # all green
bash backend/scripts/test_residue.sh                                     # OK: no residue
(cd frontend && pnpm exec tsc --noEmit)                                  # clean
(cd frontend && pnpm exec vitest run)                                    # 1872 passed / 117 files
(cd frontend && pnpm verify:demo "$(cat .demo-company-id)")              # 37/37 ⚠️ id is an ARGUMENT
node frontend/scripts/drive-t8-forex-settlement.mjs                      # 35/35, tenant reaped
node frontend/scripts/drive-t8-forex-entry.mjs                           # 50/50, tenant reaped
node frontend/scripts/drive-payroll.mjs                                  # 38/38 ⚠️ needs vite
node frontend/scripts/drive-t12-gstr9.mjs                                # 33/33
node frontend/scripts/drive-t4-slice2b-matching.mjs                      # 35/35, tenant reaped
node frontend/scripts/drive-t11-budgets.mjs                              # 36/36
node frontend/scripts/drive-year-end-close.mjs \
     "$(cat frontend/.year-end-scratch-company-id)"                      # 16/16 wire + 8/8 browser
python3 corpora/validate.py                                              # OK
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md            # 117 · 107 · 6 · 0 · 4
```

⛔ **THE BACKEND SUITE AND THE DRIVES NEED THE SERVICES UP (§2.1). `vitest` and `tsc` do not.**

⚠️ **`4 UNREADABLE` IS NOT WORK.** D49, D80, D82, D84 are CLOSED; the parser cannot read their status
cells. ⛔ **Do not "fix" the parser casually** — a rewrite that reconciles by counting classifies D13
off the wrong cell. It needs its own pass.

⚠️ **SIX ROWS ARE OPEN AND NONE IS HIGH** (D99, D109, D110, D111, D114, D117). None is on the
lock line, and none is T8's. Leave them unless one blocks you.

---

## 2. ⛔ THE TRAPS THAT WILL COST YOU AN HOUR

### 2.1 ⛔⛔ A STALE `cored` IS INDISTINGUISHABLE FROM A BROKEN FIX — AND IT COST *THIS* SESSION A RUN

`make run-cored` is `go run`, so the listener is a compiled child with a different command line.
⛔ **DO NOT USE STARTUP DURATION AS THE TEST** — with a warm cache a fresh `cored` also listens in a
second. ⭐ **CHECK THE PROCESS START TIME AGAINST THE CLOCK:**

```bash
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # is this NOW?
```

⭐ **AND HERE IS WHAT IT LOOKS LIKE WHEN YOU FORGET, MEASURED IN STEP 5.** The new drive's first run
came back **15/35**, and the failure was not a wrong number — it was:

```
✗ T8/5: GET /forex-settings is 200 FOR THE OWNER  — status 404 {"error":"no such route"}
```

⛔ **`no such route` AND A 403 ARE DIFFERENT FAILURES AND ONLY ONE OF THEM IS A MISSING `routeAction`
ARM.** A 404 means the ROUTE is not registered on the running binary — nine times out of ten a stale
`cored`. An arm that is genuinely missing gives **`unmapped route: access denied` and a 403**, which
step 5 mutation-proved deliberately so this distinction is on record. **Read the message before you
debug the code.**

Kill by PORT, always, and start **cored first** (a cored restart mints a new signing key, so
`statutoryd`'s service token dies with it; both failures present as a bare `401`):

```bash
kill -9 $(lsof -nP -iTCP:8087 -sTCP:LISTEN -t) 2>/dev/null
kill -9 $(lsof -nP -iTCP:8091 -sTCP:LISTEN -t) 2>/dev/null
sleep 2
nohup make run-cored > /tmp/cored.log 2>&1 &
for i in $(seq 1 90); do curl -sf localhost:8087/healthz >/dev/null && break; sleep 1; done
nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 &   # AFTER cored
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"; date
```

⭐ **RESTARTING IS ALSO HOW YOU MUTATION-PROVE ANYTHING ON THE WIRE.** A mutation proof against a
stale binary proves nothing and looks identical.

**Ports:** cored `:8087`, insightd `:8088`, statutoryd `:8091`, vite `:5173`.

### 2.2 ⛔⛔ A STALE `vite` IS EXACTLY AS DANGEROUS

`drive-t4-slice2b-matching` once came back **34/35, then 33/35 on a re-run, with DIFFERENT probes
failing each time** — against a vite up since the previous evening, whose log showed
`[vite] hmr invalidate … Could not Fast Refresh` triggered by an edit to an unrelated file. **After
restarting vite: 35/35, twice.** Apply §2.1's rule to `:5173` too:

```bash
kill -9 $(lsof -nP -iTCP:5173 -sTCP:LISTEN -t) 2>/dev/null; sleep 2
(cd /Users/yashbhalgat/code/tally-updated/frontend && nohup pnpm dev > /tmp/vite.log 2>&1 &)
sleep 14; ps -o lstart= -p "$(lsof -nP -iTCP:5173 -sTCP:LISTEN -t)"; date
```

⚠️ **`pnpm dev` DIES WITH THE SHELL THAT SPAWNED IT** if that shell is killed on a timeout. Use
`disown`, or a subshell, and verify `:5173` afterwards rather than assuming.
⭐ **STEPS 7 AND 8 BOTH SHIP SCREENS, so this trap is aimed straight at you** — restart vite before
you believe any browser result, and re-verify by start time after every edit to a `.tsx` file.

### 2.3 ⚠️ VITE IS `localhost`, NOT `127.0.0.1`

`--strictPort`; WebAuthn origin matching is exact and vite binds `::1`. Use `http://localhost:5173/`.

### 2.4 ⛔⛔ AN EMPTY `?company=` MINTS A FRESH TENANT AND EVERY SCREEN LOOKS CLEAN

The app bootstraps an empty **"My Company"** when none is named. **Two audit rows were wrongly logged
as passing that way.** Always pass `?company=<uuid>`, and read the **id**, not the name — there are
~95 companies called "Demo Traders".

### 2.5 ⛔ DIRTY A SCRATCH BOOK, NEVER THE DEMO ONE

```bash
(cd frontend && pnpm seed:demo -- --name "T8 Report Scratch")   # -> .t8-report-scratch-company-id
```

⭐ **The name and the id file move together.** With **no** `--name` the seeder writes
`.demo-company-id`, which every gate reads. After any session re-run
`pnpm verify:demo "$(cat frontend/.demo-company-id)"` and expect **37/37**.

⚠️ **EXISTING SCRATCH BOOKS ALREADY CARRY DRIVE DATA — MAKE A FRESH ONE:** `.d108-fix-scratch-`,
`.d113-carry-scratch-`, `.year-end-scratch-` (its books have been closed and re-opened).

⭐ **BETTER STILL, BUILD YOUR OWN TENANT IN THE PROBE**, with a `finally` that reaps it and ASSERTS
the reap worked. ⛔ **COPY `drive-t8-forex-settlement.mjs`'s REAP, NOT step 4's** — it is the only one
that lists **`company_forex_settings`** as well as `currency_rate` and `currency`. Omit it and the
delete fails on `company_forex_settings_ledger_fk` with the tenant half-removed.

### 2.6 ⚠️ THE FIRST WRITE OF A SESSION CARRIES A WEBAUTHN CEREMONY

The Owner level requires step-up: the first `POST /v1/…/vouchers/*` from a BROWSER returns **403**,
the ceremony runs, the write is retried. **That 403 is not a bug.** Install a virtual authenticator
over CDP **before the first navigation** — or use `openApp()` from `frontend/scripts/audit-harness.mjs`.

⭐ **A PURE WIRE DRIVE DOES NOT MEET THIS.** A token minted with `amr: ["webauthn"]` posts vouchers
directly — see either T8 drive.

---

## 3. ⭐ WHAT T8 ALREADY IS — MEASURED, NOT CARRIED

**Steps 1–5 of the memo's build order are DONE.** Do not redo them.

### 3.1 The schema — ALL of it, in `0080` (`20dd96f`). No migration has been needed since.

| Table | What it carries |
|---|---|
| `currency` | the master. ⛔ A **trigger refuses the company's own base currency** — a base posting is encoded by `currency_id IS NULL`, and one encoding only |
| `currency_rate` | rates by date, `NUMERIC(19,10)`, one per (currency, day) |
| `posting` | `currency_id`, `foreign_amount NUMERIC(19,4)`, `rate NUMERIC(19,10)` |
| `bill_allocation` | the same three |
| `ledger_opening_bill` | ⭐ **the same three, and §0.2 of the memo is why** — Outstandings nets a UNION of `bill_allocation` **and** `ledger_opening_bill` |
| `company_forex_settings` | `forex_gain_loss_ledger_id`. ⛔ **A TABLE, not a column on `company`** — a `company → ledger → account_group → company` cycle makes every tenant undeletable. ⭐ **READ AND WRITTEN SINCE STEP 5** |

⛔ **THE `all_or_none` CHECK IS THE POINT.** Base, foreign and rate are **three stored values and two
can be wrong while the voucher balances perfectly.** The database refuses two-of-three.

⚠️ **`rate` IS `NUMERIC(19,10)` AND MONEY IS `(19,4)`. A rate is not money.**

### 3.2 Steps 2–3 — the currency master and rate write path

`backend/internal/masters/currency.go`; `GET/POST /currencies`, `POST /currencies/rates`; the typed
client, a faithful mock, and `Alt+G > Create Currency` in `MasterCreateScreen.tsx`. Driven 15/15.

### 3.3 Step 4 — THE VOUCHER PATH (`1bb8a5c`). See `HANDOFF-PARITY-AUDIT.md` §7 `### L35`.

A voucher's postings AND bill allocations carry the triple, **written, signed, read back and preserved
across an alter**. `CanonicalPosting` carries three `omitempty` **string** keys, **LAST**, so a base
posting still marshals byte-identically and every hash in the field stays valid. `canonicalRate`
renders at **ten** decimals. Client: `Rate` type + `api/rate.ts`. Driven 50/50.

### 3.4 ⭐⭐ STEP 5 — SETTLEMENT (`21ec34f`, last session). READ `HANDOFF-PARITY-AUDIT.md` §7 `### L36`.

```bash
ls backend/internal/ledger/forex_settlement.go backend/internal/masters/forex_settings.go \
   backend/internal/server/handler_forex_settings.go     # all present
```

- **`GET/PUT .../forex-settings`** records the operator's choice of gain/loss ledger. A **bare**
  resource token, so default-DENIED for Data Entry **with no ACL migration**; a revenue or expense
  ledger only (`account_nature IN ('INCOME','EXPENSE')`).
- **The engine DERIVES AND VERIFIES, never generates** — the decision §4.1 of the last handoff left
  open, settled on measurement: `validatePostings` refuses unbalanced input before anything could be
  derived, and `deriveAndVerifyGST` and the TDS block in the same function both derive-and-verify.
- **The `AGAINST` → `NEW` lookup exists**, over the two-table UNION, in
  `openBillForex` (`forex_settlement.go`). ⛔ **BUT SEE §4.1 — STEP 6 MAY NOT CALL IT.**
- **A settlement realising a difference at a company with no ledger chosen is REFUSED**, naming the
  setting and the amount, and nothing is written.

> #### ⛔⛔ THE ONE PLACE STEP 5 OVERRODE THE MEMO, AND YOU MUST NOT "CORRECT" IT BACK
>
> Q3 states `realised = foreign_settled × (rate_settlement − rate_bill)`. **It cannot balance a
> voucher against a real bank amount**, and the counter-example is step 4's own specimen:
>
> | | |
> |---|---|
> | rupees the bank actually receives | 1000 × 85.2512345678 = **85,251.2346** |
> | receivable that must clear to ZERO | **83,651.23** (stored, deliberately ≠ 1000 × 83.6512345678) |
> | the difference the books require | **1,600.0046** |
> | the memo's formula | **1,600.0000** |
>
> The memo's figure is 46 hundredths of a paisa short, so a voucher carrying it either misstates the
> cash received (tying to no bank statement — T4's reconciliation would never match it) or strands
> 0.0046 on the receivable for ever. ⭐ **THE ENGINE THEREFORE DIFFERENCES THE VALUES:**
> `realised = round(foreign_settled × rate_settlement, 4) − base_cleared`, **which reduces to the
> memo's formula exactly when the bill's base IS the arithmetic product.**
> ⚠️ **`base_cleared` is PRO-RATA, except the final slice clears the REMAINDER** — pro-rata does not
> compose, and three $1 slices of a $3/₹100 bill otherwise strand 0.0001 no operator can clear.
> ⛔ **`drive-t8-forex-settlement.mjs` POSTS 1,600.0000 DELIBERATELY AND ASSERTS IT IS REFUSED.** If
> you "fix" the engine back to the memo's formula, that probe tells you at once.

### 3.5 ⛔ WHAT IS STILL NOT BUILT — steps 6–8, and it is the whole operator-visible half

```bash
sed -n '/^type Bill struct/,/^}/p' backend/internal/reporting/outstanding.go | grep -c "Currency\|Foreign"
#   0
grep -rn "unadjusted\|forex" frontend/src/keystroke/types.ts frontend/src/screens/GatewayScreen.tsx
#   (nothing)
```

**No report shows a foreign bill as foreign. No screen offers the setting. No voucher form offers a
currency field.** The `FeaturesScreen` row says so, stays `off`, and **its note was rewritten in
`21ec34f`** to record that realised gain/loss now works over HTTP and that no screen reaches it.

---

## 4. ⭐⭐ THE WORK — THREE STEPS, AND THE ORDER IS THE MEMO'S

From `docs/design/t8-multi-currency-model.md` §5, steps 6–8. **Steps 1–5 are done (§3).**

| # | Step | The one thing that will bite |
|---|---|---|
| **6** | **Outstandings gains two fields + the currency in its netting key** | §4.1 — **you may not reuse step 5's lookup**, and the reason is archtest |
| **7** | **The unadjusted-forex report** + its ACL migration, **same commit** | §4.2 — the migration number is **NOT** the T8 gap, and the census command LIES |
| **8** | **F11 + the entry field + the roster row last** — enforcement first, switch second | §4.3 — `partial` still does not exist in the union |

⛔ **Q6 (GST ON AN EXPORT INVOICE) IS NOT IN THIS LIST AND MUST NOT BE STARTED.** It stops at the
design document until real GST data has been through the importer.

---

### 4.1 ⭐⭐ STEP 6 — TWO FIELDS, A NETTING KEY, AND A LOOKUP YOU MUST WRITE AGAIN

```bash
sed -n '/^type Bill struct/,/^}/p' backend/internal/reporting/outstanding.go
#   RefName · Pending decimal.Decimal · DueDate · OverdueDays · SlabLabel · OnAccount
#   ⛔ ONE money column. Every bill lands in it.
```

- A foreign bill's **base** figure stays in `Pending` — it still ties to the Balance Sheet.
- The foreign figure gets its **own two fields**: `CurrencyCode` and `ForeignPending`, blank on a
  base bill. ⛔ **NEVER ONE COLUMN** — two different things must never share a column.
- ⛔⛔ **THE NETTING KEY GAINS THE CURRENCY.** Group by `(ledger, ref_name, currency)`, not
  `(ledger, ref_name)`. A `ref_name` under two currencies is **a data error, not a sum**, and must
  be **DISCLOSED on the report**.
- ⚠️ **THE GRAND TOTAL AND THE AGEING SLABS STAY BASE-ONLY.** Summing mixed currencies is
  meaningless. A design decision, not parity — say so.

> #### ⛔⛔ YOU MAY NOT CALL `openBillForex`. ARCHTEST FORBIDS THE EDGE, AND IT IS A CYCLE TOO.
>
> ```bash
> sed -n '216,217p' backend/internal/archtest/boundaries_test.go
> #   "internal/reporting": {"internal/platform/db": true, "internal/inventory": true,
> #                          "internal/compliance/form140": true},
> ```
>
> `internal/reporting` may import those three siblings and **nothing else** — and
> `internal/ledger`'s own tests import `internal/reporting`, so the edge would be a **compile-time
> cycle** as well as a policy breach. ⛔ **Exporting `openBillForex` does not help.** `outstanding.go`
> needs its OWN spelling of the two-table UNION, which is no hardship: it already has four.
>
> ⭐ **THE PRECEDENT IS `OnAccountRefName`, DECLARED TWICE ON PURPOSE:**
>
> ```bash
> grep -rn "OnAccountRefName =" backend/internal --include='*.go' | grep -v _test
> #   internal/reporting/outstanding.go:31   const OnAccountRefName = "On Account"
> #   internal/ledger/ledger.go:3077         const OnAccountRefName = "On Account"
> ```
>
> The agreement is enforced by **`TestOnAccountRefNameAgreesAcrossTheWriteAndReadSides`** in
> `internal/server`, the composition root that may import both. **Do the same for any currency
> constant you need on both sides, and add it to that test rather than inventing a second one.**
> ⚠️ **`ledger`'s comment on that constant still claims it is "one fact with one home". It is not,
> and cannot be** — `reporting`'s comment is the accurate one. Do not follow `ledger`'s.

⛔ **AND IT IS A UNION OF TWO TABLES, WHICH IS WHERE THIS STEP GOES WRONG:**

```bash
grep -n "FROM bill_allocation\|FROM ledger_opening_bill" backend/internal/reporting/outstanding.go
#   272: FROM ledger_opening_bill ob
#   376: FROM bill_allocation b2
#   411: FROM ledger_opening_bill ob2
#   420: FROM bill_allocation b
```

**Four sites, two tables** — unchanged since the last handoff. Handle only `bill_allocation` and a
foreign party's OPENING bill silently becomes a base bill: its ₹ figure is real, its foreign column
blank, and the two net together.

⚠️ **AND THERE IS A THIRD ROW TYPE, WHOSE COMMENT YOU MUST READ FIRST:** `OnAccount` — the derived
residue filed under `OnAccountRefName`, **deliberately un-aged and outside every slab**, which is why
`OnAccountTotal` exists and why `sum(SlabTotals) + OnAccountTotal == Total` is the report's own
reconciliation. **Decide what currency it shows and say why** (an unallocated amount tied to no bill
has no single rate). ⛔ **Whatever you decide, that identity must still hold.**

⭐ **AND STEP 6 IS WHERE STEP 5's DEFERRED HOLE BECOMES VISIBLE, WHICH IS THE DESIGN.** L36 records
that a *base* `AGAINST` row against a *foreign* bill is NOT refused — catching it would put the bill
lookup on every base settlement in the product. It is not silent: **the base nets to zero while the
foreign does not**, so once you build these two fields the bill shows base pending 0.00 against a
foreign pending of $1,000. **That is the disclosure Q5 asks for. Make sure your netting produces it
rather than hiding it, and put it in the drive.**

---

### 4.2 ⚠️ STEP 7 — A REPORT THAT POSTS NOTHING, AND A CENSUS COMMAND THAT LIES

Unrealised gain/loss is **a report, in v1** — per the owner's own answer. It reads the open-bill
UNION and shows, per bill: foreign pending, the bill's rate, the rate as on the report date, the base
figure in the books, and the difference. ⛔ **It posts nothing, and the screen and F11 must say so in
those words.** The revaluation *voucher* is deferred and costs no design debt: postings are
immutable, so it could only ever BE a voucher, and v1's schema is the schema it would need.

> #### ⛔⛔ ITS ACL MIGRATION TAKES A NUMBER AT THE **END** (0090+), NOT THE T8 GAP
>
> ```bash
> ls backend/migrations/*.sql | tail -3
> #   0087_gstr9_acl.sql  0088_bank_statement_match.sql  0089_close_financial_year.sql
> ls backend/migrations/ | grep -E "^008[1-4]"
> #   (nothing) — the T8 gap is still open
> ```
>
> ⚠️ **`PLAN-TALLY-PARITY.md` §2.5 RESERVES 0081–0084 FOR T8** and they are still untouched.
> ⭐ **ITS `⛔ DO NOT TAKE THESE` IS ADDRESSED TO OTHER LANES, NOT TO YOU** — you are T8.
> **But a migration numbered 0081 runs BEFORE 0085–0089 on a fresh database.** For a plain
> `CREATE TABLE` that is harmless. For anything that **restates `seed_company_auth_defaults`** it is
> exactly the defect `0071` exists to repair: a body installed lower is overwritten on every fresh
> database and the rules simply do not exist. ⭐ **SO STEP 7's ACL MIGRATION TAKES 0090+.**
> **Update `PLAN-TALLY-PARITY.md` §2.5 in place** when you take a number, either way.

> #### ⛔⛔ AND THE TWO COMMANDS FOR FINDING THE BODY TO DERIVE FROM BOTH GIVE THE WRONG ANSWER
>
> **This is measured, and it cost this session two wrong conclusions I had to walk back.**
>
> **(1) `grep -l | tail -1` IS NOT SORTED — AND IT IS NOT EVEN STABLE.** `grep` here is `ugrep`,
> which matches in parallel and emits files in completion order. The previous handoff's recipe run
> three times in a row:
>
> ```bash
> for i in 1 2 3; do echo "run $i: $(grep -ln 'CREATE OR REPLACE FUNCTION seed_company_auth_defaults' \
>     backend/migrations/*.sql | tail -1)"; done
> #   run 1: backend/migrations/0086_budget_report_acl.sql
> #   run 2: backend/migrations/0086_budget_report_acl.sql
> #   run 3: backend/migrations/0087_gstr9_acl.sql        ← THREE RUNS, TWO ANSWERS
> ```
>
> ⛔ **SO IT IS WORSE THAN WRONG, IT IS INTERMITTENT** — and one `tail -3` during this session did
> not contain 0087 at all (`0072`, `0086`, `0076`). A recipe that usually gives 0086 and sometimes
> gives 0087 is how the wrong body gets derived from once and nobody can reproduce it.
> ⭐ **The highest migration that restates the seeder is `0087_gstr9_acl.sql` (line 277). SORT
> EXPLICITLY, ALWAYS:**
>
> ```bash
> grep -ln "CREATE OR REPLACE FUNCTION seed_company_auth_defaults" backend/migrations/*.sql | sort | tail -1
> #   backend/migrations/0087_gstr9_acl.sql      ← stable across runs
> ```
>
> **(2) A WHOLE-FILE `grep -c` COUNTS A COMMENT.** The previous handoff says 0087 has **16**
> PERFORMs. A naive count says 17:
>
> ```bash
> grep -c "PERFORM seed_data_entry" backend/migrations/0087_gstr9_acl.sql          # 17  ⛔ WRONG
> grep -n  "PERFORM seed_data_entry" backend/migrations/0087_gstr9_acl.sql | head -1
> #   233:--   | grep -c 'PERFORM seed_data_entry'); …     ← the RECIPE, inside a comment
> ```
>
> ⭐ **USE THE MIGRATION'S OWN RECIPE, WHICH SCOPES TO THE FUNCTION BODY:**
>
> ```bash
> awk '/^CREATE OR REPLACE FUNCTION seed_company_auth_defaults\(/,/^\$\$;$/' \
>     backend/migrations/0087_gstr9_acl.sql | grep -c 'PERFORM seed_data_entry'   # 16 ✅
> awk '…' backend/migrations/0086_budget_report_acl.sql | grep -c 'PERFORM …'     # 15 ✅
> ```
>
> ⭐ **SO THE PREVIOUS HANDOFF WAS RIGHT AND THE OBVIOUS CHECK IS WRONG.** 0076 → 14, 0086 → 15,
> 0087 → 16. **Yours is 16 → 17, derived from 0087, and the comment-stripped diff against 0087 must
> be exactly one line plus your new `seed_data_entry_*_rules` function.**
>
> ⚠️ **BUT DO NOT CALL THE COUNT "THE ASSERTION" — NO TEST ASSERTS IT.** `migrations/seeder_census_test.go`
> is **structural**: it finds the winning body and fails if that body does not PERFORM every seeder
> some other migration defines. **It hard-codes no number.** So 0088's line-310 claim that the census
> *"still reads 16 PERFORMs"* is accurate PROSE with no test behind it. **The census test is still the
> gate you must satisfy — run it — but the number is a human check, not a machine one.**
>
> ⛔ **Also write the BACKFILL**, not only the seeder: existing companies already have their levels,
> and 0086/0087 both record *"303/303 Data Entry levels covered"* as the measured proof.
>
> ⚠️ **AND READ `0088_bank_statement_match.sql` LINE ~274 BEFORE YOU DECIDE YOU NEED A MIGRATION AT
> ALL** — *"SLICE 2b ADDS NO REPORT NAME AND NO RESOURCE TOKEN"*. It lists the twelve migrations that
> do. ⭐ **Step 5 is a fresh worked example of needing NONE**: `forex-settings` is a bare resource
> token, default-denied for Data Entry, so it shipped with no ACL migration. **A REPORT NAME is what
> forces one**, because of the next point.
>
> ⛔ **`disallow display` AND `disallow print`, BOTH.** 0011's coarse `allow display report` reaches
> a BARE resource and `internal/authz` matches it against ANY report name (defect D95) — **the GRANT
> is the default and the REFUSAL is what must be written.**

⚠️ **AND THE REPORT NAME IS A COLLISION RISK. MEASURED:**

```bash
python3 - <<'PY'
import re; s=open('frontend/src/keystroke/types.ts').read()
b=re.search(r'export const REPORT_NAMES = \[(.*?)\] as const', s, re.S).group(1)
print(re.findall(r'"([^"]+)"', b))
PY
#   20 names today, and THREE of them contain "Outstandings"
```

⛔ **Check your candidate against `REPORT_NAMES` AND `GatewayScreen.tsx`, IN BOTH DIRECTIONS** — a
label that is a SUBSTRING of a shipped one is a silent collision, and "Outstandings" already appears
three times.

⭐ **AND CARRY THE THREE — NOW FOUR — REGISTRATION FACTS (§5).**

---

### 4.3 ⛔ STEP 8 — AND THE MEMO STILL ASSUMES A ROSTER STATE THAT DOES NOT EXIST

```bash
grep -n "^type State" frontend/src/screens/FeaturesScreen.tsx
# type State = "always" | "on" | "server" | "off";     ⛔ still no `partial`
```

⭐ **DECIDE IT AND SAY WHICH YOU CHOSE.** Either add `partial` to the union **and its rendering and
its legend** — the honest option, since `server` was added for exactly this reason on the offline
row — or split the row the way step 3 already did for the masters. ⛔ **What you must NOT do is flip
`Multi-currency` to `on` while any half only reports.**

⚠️ **THE ROW'S NOTE WAS REWRITTEN IN `21ec34f` AND IS CURRENTLY TRUE. READ IT BEFORE YOU TOUCH IT.**
It now opens *"There is no screen in this build on which you can key a voucher in a foreign currency,
settle a foreign bill or read an exchange gain or loss"*, then records step 5 over HTTP, then says
what is still missing. ⛔ **Every clause becomes false at a different moment in step 8** — the setting
screen falsifies one, the entry field another. **Update it as each lands, not once at the end.**

⭐ **THIS IS WHERE THE VOUCHER-ENTRY UI AND THE SETTINGS SCREEN LAND.** By step 8 settlement, ageing
and the report all exist, so the field can ship.

```bash
grep -n "^export " frontend/src/api/rate.ts
#   RATE_DECIMALS · parseRate · formatRate · normaliseRate · hasRate · foreignToBase
grep -n "forexSettings\|setForexSettings" frontend/src/api/client.ts   # both already typed
```

⚠️ **`api/rate.ts` ALREADY HAS WHAT THE FORM NEEDS** — `normaliseRate` (pad the operator's `83.65` to
the stored `83.6500000000`, so no screen reports a spurious edit) and `foreignToBase` (a **suggestion**
for the base field; the operator may override it and be right). ⭐ **AND THE SETTINGS CLIENT IS DONE**
— `forexSettings` / `setForexSettings` are typed, implemented over REST and mocked faithfully
(`src/api/forexSettings.test.ts`, 6 tests). Step 8's settings screen is a UI on an existing client.

⛔ **AND THE SETTLEMENT FORM MUST SHOW THE BILL'S HISTORICAL RATE**, because step 5 chose the VERIFY
design: the client computes the realised figure and the server refuses any disagreement. **The
open-bills read that feeds an `AGAINST` picker therefore has to return the bill's rate** — which is
exactly what step 6's two new fields put on that read. **Steps 6 and 8 meet here; do 6 first.**

---

## 5. ⛔ READ THE MEMO, BUT ONLY THESE PARTS

`docs/design/t8-multi-currency-model.md` is 472 lines and you do not need all of it.

| Section | Why |
|---|---|
| **Q2** (line 113) | *"Which amount is authoritative?"* — **THE BASE AMOUNT. CLOSED.** Read once so you never re-litigate it |
| **Q4** (208) | unrealised — the report-not-a-posting decision. **Step 7 is this** |
| **Q5** (222) | Outstandings: separate columns and a disclosed conflict. **Step 6 is this** |
| **§0.2** (36) | the two-table UNION that breaks Q5 if you forget `ledger_opening_bill` |

⚠️ **SKIP Q6 AND §6.** Q6 is blocked; §6 records a probe that overturned an earlier scouting claim.
⚠️ **Q3 (167) IS STEP 5's AND IS DONE — but read §3.4 above before you read Q3**, because its formula
was measured wrong and replaced.

⭐ **AND CARRY THESE REGISTRATION FACTS. THE FIRST THREE ARE THE MEMO'S §5; THE FOURTH WAS FOUND IN
STEP 5 AND IS NOT IN THE MEMO:**

1. **A new `MasterKind` is not like a new `ScreenKind`.** `tsc` names every missing `MasterKind`
   registration; it enforces **none** of a `ScreenKind`'s. **Step 7 adds a `ScreenKind`.**
   ⚠️ **THE PREVIOUS HANDOFF SAYS "FIFTEEN" AND I DID NOT RE-MEASURE IT** — there is no list of them
   in `keystroke/types.ts` to count, so treat the number as indicative and **the enforcement gap as
   the fact**: find the registration points by imitating the most recently added `ScreenKind`
   end-to-end, not by trusting a count.
   ⭐ **`tsc` DID name every gap in the typed CLIENT, though** — adding a method to `CoredClient`
   failed the build until `rest.ts` and `mock.ts` both implemented it, which is how step 5's mock
   could not be forgotten.
2. ⛔ **A NEW ROUTE STILL NEEDS ITS `routeAction` ARM AND NOTHING TYPE-CHECKS THAT.** Default-denies
   unmapped `{companyID}` routes → compiles, passes every Go test, **403 for the Owner**.
   **Step 7 adds routes.** ⭐ Step 5 mutation-proved the symptom: **`unmapped route: access denied`**.
3. ⭐ **A NEW DATE-SHAPED WIRE FIELD IS A REGISTRATION POINT.**
   `TestEveryDateShapedWireFieldIsAccountedForByTheCutoff`
   (`internal/server/auth_backdated_wire_date_test.go:320`) scans package `server` **by shape** and
   will fail your build on any new `*_date` field until you classify it **with a written reason**.
   ⚠️ **STEP 7 HAS ONE: the report's "as on" date.** That gap shipped open three times, and **two of
   the three shipped with a comment asserting the control was in place.**
4. ⛔⛔ **A FOURTH: `authz.KnownResources`, THE PDP VOCABULARY.** A route may have its `routeAction`
   arm and still be unreachable by any rule. Step 5 hit it:
   ```
   --- FAIL: TestEveryCompanyRouteIsMappedAndInVocabulary
       route GET /v1/companies/{companyID}/forex-settings emits resource "forex-settings",
       which is not in the PDP vocabulary (authz.KnownResources) — no ACL rule could ever name it
   ```
   ⭐ **NOTE THE ASYMMETRY AND CARRY IT: the vocabulary gap FAILS THE BUILD; the `routeAction` gap
   SHIPS A 403.** Add your token to `internal/authz/vocabulary.go` with the reasoning comment its
   neighbours carry.

---

## 6. ⚠️ WHAT THE LAST SESSION LEARNED THAT APPLIES DIRECTLY TO STEPS 6–8

### 6.1 ⛔ "AN ALTER RE-STATES" HAS BITTEN **SEVEN** TIMES. STEP 5 WAS THE EIGHTH CANDIDATE AND SURVIVED — **FOR A REASON THAT DOES NOT PROTECT YOU.**

Every table keyed on `voucher_version_id` is rewritten from the request body, so **whatever the body
omits is gone**, and nothing refuses it because zero rows is a legal state. Victims:
`bill_allocation`, `stock_movement`, `posting_cost_allocation`, `order_fulfilment`,
`voucher_gst_line`, and in `1bb8a5c` the forex triple.

⭐ **STEP 5's REALISED LEG IS SAFE BECAUSE IT IS A `posting`**, so omitting it either unbalances the
voucher (the double-entry guard refuses it) or comes with a bank leg the derivation contradicts.
⛔ **STEPS 6 AND 7 ARE REPORTS AND WRITE NOTHING, so they inherit no protection and add no exposure —
but step 8 ships a FORM.** Ask, before you write it: *what does an alter through this form omit?* A
currency field that does not round-trip the triple is defect number nine.

⭐ **THE TEST THAT CATCHES IT IS ALWAYS THE SAME SHAPE, AND IT NEEDS ITS INVERSE CONTROL:** post →
GET → rebuild the PUT body **from that response** → read the rows back out. Then prove the data is
**destructible** by omitting it. `backend/internal/server/voucher_readback_audit_test.go`, and
`TestReadBackAudit_ForeignCurrencySurvivesAnAlter` is the worked example.

### 6.2 ⛔ AN AGGREGATE QUERY IS NOT A SAMPLE OF THE QUERIES A USER MAKES

`verify:demo` once reported 34/34 against a book that could not produce **any** of its twelve monthly
GST returns, because it asked for one GSTR-3B over the **whole year**. Nobody files that window.

⭐ **THE STEP 6 AND 7 VERSION IS SHARPER THAN STEP 5's WAS.** A report asked for ONE party, or over
the whole book, or unbounded, hides the case that breaks it. **Ask for a date that splits the
settlements**, and for a party who holds a base bill AND a foreign bill under **the same ref_name** —
that last one is Q5's disclosed conflict, and it is the row most likely to be silently netted.
⚠️ **AND AT LEAST TWO PROBES MUST USE TWO DIFFERENT RATES**, or a hard-coded constant passes. Step 5's
figures are reusable: bill 83,651.23 / $1,000 / 83.6512345678, settled at 85.2512345678 → realised
**1,600.0046**; a second bill 45,074.07 / $600 / 75.1234567890, $200 settled at 79.1234567890 →
realised **800.0014**, remainder **30,049.38 / $400**.

### 6.3 ⛔ A RATE IS NOT MONEY, AND THE BUG IS INVISIBLE

`StringFixed(4)` on a rate READS as consistency with every other number on a voucher. It gives
`0.0033` for `0.0033333333` — thousands of rupees on a fifty-lakh invoice — **and the truncated value
round-trips through its own type perfectly, so nothing fails.** Use `canonicalRate` (server,
`hash.go`) and `api/rate.ts` (client). ⛔ **Never `parseFixed4`, whose own comment says it TRUNCATES.**

⭐ **STEP 5 MUTATION-PROVED THE COST, AND STEPS 6 AND 7 BOTH RENDER RATES ON A REPORT.** Quantising
the rates before the multiplication turned **1,600.0046 into 1,599.9700** — a figure that is
plausible, in the right ledger, on the right side, and wrong for ever. **A report formatter is exactly
where "make the columns line up" wins arguments. Assert a ten-decimal rate survives to the page.**

### 6.4 ⚠️ MY OWN FIRST MEASUREMENTS WERE WRONG TWICE, BOTH TIMES FROM A PLAUSIBLE COMMAND

Recorded because the commands are the ones you will reach for: `grep -l … | tail -1` is neither
sorted nor STABLE — three consecutive runs gave two different answers (§4.2) — and a whole-file
`grep -c` for a code pattern counts the copies of it **inside comments** (§4.2). ⛔ **Both gave a confident wrong answer that contradicted the previous handoff, and the
previous handoff was right.** When a measurement contradicts a document written by someone who had
the database in front of them, **suspect the command first.**

---

## 7. ⛔⛔ THE HOUSE STANDARD YOUR WORK MUST MEET

Rows have been re-opened in this repo for failing each of these.

- [ ] **A failing test written FIRST**, asserting the thing no existing test covers.
- [ ] ⛔ **MUTATION-PROVED.** Revert the fix and watch the new test go red **with the number the real
      system produced**. ⭐ For steps 6–7 that means naming **which figure on which row** — the
      foreign pending, the rate, or the difference — never just "the report was wrong".
- [ ] ⛔ **DRIVEN AGAINST THE REAL SYSTEM**, on a fresh scratch book (§2.5), against a cored verified
      fresh (§2.1) **and a vite verified fresh (§2.2)**. **The oracle is `psql` or the wire, never
      the screen.**
- [ ] ⚠️ **DRIVE THE CONTROL TOO** — a base-currency bill must still age, net and report exactly as it
      does today. Every existing bill in the demo book is base; if T8 moves any of them,
      `verify:demo` will say so and you should want it to. ⭐ **37/37 is the number.**
- [ ] ⛔ **AND MUTATION-PROVE THE GATE, NOT ONLY THE CODE.** Rebuild cored **with the fix removed**,
      restart it, and confirm your drive goes red **and that your new checks are the only reds.**
      ⭐ Steps 4 and 5 both did this: step 4 went 50/50 → **37/50**; step 5 went 35/35 → **22/35, the
      13 reds exactly the derivation probes**, with the setter and control probes still green.
- [ ] Gates in §1 re-run and **stated in the commit message, with every deferral named.**
- [ ] `HANDOFF-PARITY-AUDIT.md` updated **with the measurement, the mutation proof and the drive in
      the house format**, plus a `### L37 — …` lane section at the end of §7.
      (**`L35` and `L36` are the templates** — copy their shape. L36 also shows how to record a
      correction to your own earlier claim, which you should expect to need.)
- [ ] ⭐ **AND THE ROSTER ROW LAST** (§4.3). A feature nobody can find is a feature that is not built.

---

## 8. THE READING LIST, in order

1. **This file.**
2. `HANDOFF-PARITY-AUDIT.md` §7 **`### L36`** — what step 5 built, the verify-vs-generate decision and
   its three measured reasons, the formula override, eight mutation proofs, and the corrected note
   about why step 6 cannot reuse the lookup. **Start here.**
3. `HANDOFF-PARITY-AUDIT.md` §7 **`### L35`** — step 4, and the two design decisions it deliberately
   did NOT take (no arithmetic check on `base ≈ foreign × rate`; no entry UI).
4. `docs/design/t8-multi-currency-model.md` — **Q4, Q5, §0.2**. Skip Q6 and §6. Read Q3 only after
   §3.4 above.
5. `backend/internal/reporting/outstanding.go` — **step 6 IS this file.** Read `Bill.OnAccount`'s
   comment and the `OnAccountRefName` header before you touch the netting key.
6. `backend/internal/ledger/forex_settlement.go` — the arithmetic steps 6 and 7 must agree with, and
   the three headers explaining why it is shaped as it is. ⛔ **Read it to COPY THE ARITHMETIC, not to
   call it** (§4.1).
7. `backend/internal/ledger/forex_settlement_test.go` — the settlement oracle, including the
   composition invariant (`Σ realised == Σ received − book value`) that your report must not
   contradict.
8. `frontend/scripts/drive-t8-forex-settlement.mjs` — the drive to model yours on, and the reap block
   to copy verbatim (**it is the one with `company_forex_settings`**).
9. `backend/migrations/0088_bank_statement_match.sql` **line ~274** — why a slice may legitimately need
   NO ACL migration, and the twelve that did.
10. `HANDOFF-FEATURE-LOCK-LINE.md` §6 — what closing T8 actually finishes.
    ⚠️ Its numbers are stale (written 2026-08-27); §1 above is current.
