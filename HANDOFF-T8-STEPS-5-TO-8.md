# HANDOFF — T8 STEPS 5–8. THE VOUCHER PATH IS BUILT; SETTLEMENT, AGEING AND THE REPORT ARE NOT.

**Written:** 2026-09-02, working tree CLEAN at `1bb8a5c` · **Branch:** `session/t32-and-close-7-3`
**Read time: 10 minutes.** ⭐ **Every claim in §1, §3 and §4 was MEASURED at `1bb8a5c`** and the
command is beside it.

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** It is self-contained.
§1–§2 get you a running app you can safely break. §3 is exactly what exists. **§4 is the work, and
its order is not yours to choose.** §7 is the house standard your work must meet.

⛔⛔ **DO NOT RE-DESIGN T8. `docs/design/t8-multi-currency-model.md` ANSWERS SIX QUESTIONS AND THE
ANSWERS WERE GIVEN BY THE REPO OWNER.** §5 below tells you which parts you actually need.

---

## 0. ⭐ THE ONE-LINE VERSION

**A voucher can now carry a foreign currency end to end — but only through the API, and nothing yet
knows what to DO with one.** Steps 1–4 of the memo's build order are done. What does not exist:

```bash
grep -rn "company_forex_settings\|forex_gain_loss" backend/internal --include='*.go' | grep -v _test
#   (nothing)  ⛔ no settlement, no gain/loss, no route that sets the ledger

grep -rn "RefAgainst" backend/internal --include='*.go' | grep -v _test
#   ledger.go:190,191,199,3019 — the CONSTANT and one empty-name check.
#   ⛔ NOTHING JOINS AN `AGAINST` REF BACK TO THE `NEW` REF IT SETTLES.
```

⭐ **T8 IS STILL THE WHOLE OF WHAT IS BUILDABLE.** The other lock-line item, **T13 (interest), is
BLOCKED ON A HUMAN** — it needs day-count and simple-vs-compound statutory defaults sourced into
`docs/statutory/` and cannot be started.

---

## 1. THE STATE — verbatim, and what each number should be

```bash
cd /Users/yashbhalgat/code/tally-updated
git log --oneline -1          # 1bb8a5c feat(T8/4): a voucher can carry a foreign currency…
git status --porcelain        # (empty)
```

**Gate baselines at `1bb8a5c`, ALL RE-RUN AT THAT COMMIT ON 2026-09-02. Any red is yours.**

⚠️ **EVERY LINE IS RUN FROM THE REPO ROOT** and each `cd` is in its own subshell for that reason.
⛔ **AND `cd X && nohup … &` RUNS THE `cd` IN THE BACKGROUND SUBSHELL**, so the parent shell never
moves and the next line reads the wrong directory. Use absolute paths in any block that backgrounds
something. ⚠️ **`make` TARGETS LIVE IN THE ROOT `Makefile` ONLY** — there is no `backend/Makefile`,
so `make run-cored` from `backend/` fails with *"No rule to make target"*. That cost this session
two restarts.

```bash
(cd backend && go build ./... && go vet ./... && go test ./... -count=1)  # all green
bash backend/scripts/test_residue.sh                                     # OK: no residue
(cd frontend && pnpm exec tsc --noEmit)                                  # clean
(cd frontend && pnpm exec vitest run)                                    # 1866 passed / 116 files
(cd frontend && pnpm verify:demo "$(cat .demo-company-id)")              # 37/37 ⚠️ id is an ARGUMENT
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

### 2.1 ⛔⛔ A STALE `cored` IS INDISTINGUISHABLE FROM A BROKEN FIX — AND `pkill` MISSES IT

`make run-cored` is `go run`, so the listener is a compiled child with a different command line.
⛔ **DO NOT USE STARTUP DURATION AS THE TEST** — with a warm cache a fresh `cored` also listens in a
second. ⭐ **CHECK THE PROCESS START TIME AGAINST THE CLOCK:**

```bash
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # is this NOW?
```

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

### 2.2 ⛔⛔ A STALE `vite` IS EXACTLY AS DANGEROUS, AND THIS IS NEW — IT COST THIS SESSION A FALSE REGRESSION

`drive-t4-slice2b-matching` came back **34/35, then 33/35 on a re-run, with DIFFERENT probes failing
each time** — against a vite that had been up since the previous evening. Its log showed:

```
[vite] hmr invalidate /src/screens/BankMatchPanel.tsx  Could not Fast Refresh ("panelOps" export is incompatible)
```

…triggered by an edit to `VoucherScreen.tsx`, a file with nothing to do with bank matching. **After
restarting vite: 35/35, twice.** ⛔ **Two probes were nearly written up as a regression in a lane
that never touches bank matching.** Apply §2.1's rule to `:5173` too:

```bash
kill -9 $(lsof -nP -iTCP:5173 -sTCP:LISTEN -t) 2>/dev/null; sleep 2
(cd /Users/yashbhalgat/code/tally-updated/frontend && nohup pnpm dev > /tmp/vite.log 2>&1 &)
sleep 14; ps -o lstart= -p "$(lsof -nP -iTCP:5173 -sTCP:LISTEN -t)"; date
```

⚠️ **AND `pnpm dev` DIES WITH THE SHELL THAT SPAWNED IT** if that shell is killed on a timeout. Use
`disown`, or a subshell, and verify `:5173` afterwards rather than assuming.

### 2.3 ⚠️ VITE IS `localhost`, NOT `127.0.0.1`

`--strictPort`; WebAuthn origin matching is exact and vite binds `::1`. Use `http://localhost:5173/`.

### 2.4 ⛔⛔ AN EMPTY `?company=` MINTS A FRESH TENANT AND EVERY SCREEN LOOKS CLEAN

The app bootstraps an empty **"My Company"** when none is named. **Two audit rows were wrongly logged
as passing that way.** Always pass `?company=<uuid>`, and read the **id**, not the name — there are
~95 companies called "Demo Traders".

### 2.5 ⛔ DIRTY A SCRATCH BOOK, NEVER THE DEMO ONE

```bash
(cd frontend && pnpm seed:demo -- --name "T8 Settle Scratch")   # -> .t8-settle-scratch-company-id
```

⭐ **The name and the id file move together.** With **no** `--name` the seeder writes
`.demo-company-id`, which every gate reads. After any session re-run
`pnpm verify:demo "$(cat frontend/.demo-company-id)"` and expect **37/37**.

⚠️ **EXISTING SCRATCH BOOKS ALREADY CARRY DRIVE DATA — MAKE A FRESH ONE:** `.d108-fix-scratch-`,
`.d113-carry-scratch-`, `.year-end-scratch-` (its books have been closed and re-opened).

⭐ **BETTER STILL, FOLLOW `drive-t8-forex-entry.mjs` AND BUILD YOUR OWN TENANT IN THE PROBE**, with a
`finally` that reaps it and ASSERTS the reap worked. Copy its reap block verbatim — it is the only
one in the repo that lists `currency_rate` and `currency`, and a copy of an older reap fails on
`currency_company_scope` with the tenant half-removed.

### 2.6 ⚠️ THE FIRST WRITE OF A SESSION CARRIES A WEBAUTHN CEREMONY

The Owner level requires step-up: the first `POST /v1/…/vouchers/*` from a BROWSER returns **403**,
the ceremony runs, the write is retried. **That 403 is not a bug.** Install a virtual authenticator
over CDP **before the first navigation** — or use `openApp()` from `frontend/scripts/audit-harness.mjs`.

⭐ **A PURE WIRE DRIVE DOES NOT MEET THIS.** A token minted with `amr: ["webauthn"]` posts vouchers
directly — see `drive-t8-forex-entry.mjs`, which posts five without a ceremony.

---

## 3. ⭐ WHAT T8 ALREADY IS — MEASURED, NOT CARRIED

**Steps 1–4 of the memo's build order are DONE.** Do not redo them.

### 3.1 The schema — ALL of it, in `0080` (`20dd96f`)

| Table | What it carries |
|---|---|
| `currency` | the master. ⛔ A **trigger refuses the company's own base currency** — a base posting is encoded by `currency_id IS NULL`, and one encoding only |
| `currency_rate` | rates by date, `NUMERIC(19,10)`, one per (currency, day) |
| `posting` | `currency_id`, `foreign_amount NUMERIC(19,4)`, `rate NUMERIC(19,10)` |
| `bill_allocation` | the same three |
| `ledger_opening_bill` | ⭐ **the same three, and §0.2 of the memo is why** — Outstandings nets a UNION of `bill_allocation` **and** `ledger_opening_bill` |
| `company_forex_settings` | `forex_gain_loss_ledger_id`. ⛔ **A TABLE, not a column on `company`** — a `company → ledger → account_group → company` cycle makes every tenant undeletable. ⚠️ **NOTHING READS OR WRITES IT YET** |

⛔ **THE `all_or_none` CHECK IS THE POINT.** Base, foreign and rate are **three stored values and two
can be wrong while the voucher balances perfectly.** The database refuses two-of-three.

⚠️ **`rate` IS `NUMERIC(19,10)` AND MONEY IS `(19,4)`. A rate is not money.**

### 3.2 Steps 2–3 — the currency master and rate write path

`backend/internal/masters/currency.go`; `GET/POST /currencies`, `POST /currencies/rates`; the typed
client, a faithful mock, and `Alt+G > Create Currency` in `MasterCreateScreen.tsx`. Driven 15/15.

### 3.3 ⭐ STEP 4 — THE VOUCHER PATH (`1bb8a5c`, this session). READ `HANDOFF-PARITY-AUDIT.md` §7 `### L35`.

A voucher's postings AND bill allocations now carry the triple, **written, signed, read back and
preserved across an alter**:

- `ledger.PostingInput` / `BillAllocationInput` carry `CurrencyID`, `ForeignAmount`, `Rate`;
  `forexTriple.validate` turns 0080's CHECK into a sentence naming the missing value.
- `CanonicalPosting` / `CanonicalBillAllocation` carry three `omitempty` **string** keys, **LAST**.
  ⛔ **A base posting still marshals byte-identically**, which is what keeps every hash in the field
  valid; three existing guards go red if `omitempty` is removed.
- `canonicalRate` renders at **ten** decimals — `rateScale`, matching the column.
- `GetVoucher` projects them; `voucherFullJSON` emits them **only on a foreign row** (absent, never
  empty-stringed — `"currency_id": ""` is a two-of-three triple the server refuses).
- Client: `Rate` type + `api/rate.ts` (`parseRate`/`formatRate`/`normaliseRate`/`foreignToBase`),
  `postingsFromView` and `billRefsFromView`/`buildBillAllocations` carry the triple, the mock echoes it.

⭐ **THE FREE ORACLE HELD: `verify:demo` 37/37 UNCHANGED.** Q2 leaves `posting.amount` alone, so T8
does not move the Balance Sheet. **If it ever does, that is a defect, not a re-pin.**

### 3.4 ⛔ WHAT IS STILL NOT BUILT — steps 5–8, and it is the operator-visible half

```bash
grep -rn "company_forex_settings\|forex_gain_loss" backend/internal --include='*.go' | grep -v _test
#   (nothing)
```

**No settlement realises anything. No report shows a foreign bill as foreign. No screen offers a
currency field.** The `FeaturesScreen` row says so and is wholly true — it stays `off`, and its note
now says the data path exists over HTTP and that no screen offers it.

---

## 4. ⭐⭐ THE WORK — FOUR STEPS, AND THE ORDER IS THE MEMO'S

From `docs/design/t8-multi-currency-model.md` §5, steps 5–8. **Steps 1–4 are done (§3).**

| # | Step | The one thing that will bite |
|---|---|---|
| **5** | **Realised gain/loss on settlement**, incl. the REFUSAL when no ledger is chosen | §4.1 — **who computes the third posting** is not settled by the memo |
| **6** | **Outstandings gains two fields + the currency in its netting key** | §4.2 — the two-table UNION |
| **7** | **The unadjusted-forex report** + its ACL migration, **same commit** | §4.3 — the migration number is **NOT** the T8 gap |
| **8** | **F11 + the roster row last** — enforcement first, switch second | §4.4 — `partial` does not exist in the union |

⛔ **Q6 (GST ON AN EXPORT INVOICE) IS NOT IN THIS LIST AND MUST NOT BE STARTED.** It stops at the
design document until real GST data has been through the importer.

---

### 4.1 ⭐⭐ STEP 5 — AND THE FIRST DECISION IS ONE THE MEMO DOES NOT MAKE FOR YOU

**The arithmetic is settled.** Both rates are already stored by step 4:

- the `NEW` ref row (`bill_allocation.ref_type = 'NEW'`) carries the **bill's** historical rate;
- the `AGAINST` ref row on the Receipt/Payment carries the **settlement-date** rate.

```
realised = foreign_settled × (rate_settlement − rate_bill)
```

⛔ **WHICH LEDGER: THE OPERATOR'S CHOICE, AND ABSENCE IS A REFUSAL, NOT A DEFAULT.** A settlement
that would realise a difference at a company with no `forex_gain_loss_ledger_id` is **refused, naming
the setting** — never posted to a guessed ledger, never silently absorbed into the party balance.

⚠️ **`ADVANCE` AND `ON_ACCOUNT` HAVE NO BILL RATE TO DIFFERENCE AGAINST.** They are foreign-tagged and
carried, and realise nothing until allocated. **State that; do not compute zero** — zero and
not-applicable are different facts and only one of them is true.

> #### ⛔⛔ THE DECISION: DOES THE SERVER *GENERATE* THE THIRD POSTING, OR *VERIFY* ONE THE CLIENT SENT?
>
> The memo says *"posted to the operator-chosen ledger as an extra posting on the settlement
> voucher"* and does not say who supplies it. **Measure before you choose — both facts below are
> real:**
>
> ```bash
> sed -n '/^func validatePostings/,/^}/p' backend/internal/ledger/ledger.go
> #   ⛔ returns "double-entry violation: debit %s != credit %s" on UNBALANCED INPUT
> ```
>
> ⭐ **SO A CLIENT CANNOT SEND TWO LEGS AND LET THE SERVER ADD THE THIRD** — the input is refused
> before anything could be derived. A generate-design has to either skip that check for settlements
> (which removes the guard from the one voucher class that most needs it) or run the derivation
> before validation (which means computing a gain from postings nothing has validated yet).
>
> ⭐ **AND THERE IS AN ESTABLISHED PRECEDENT IN THIS EXACT FUNCTION'S NEIGHBOURHOOD:**
>
> ```bash
> sed -n '/^func deriveAndVerifyGST/,+35p' backend/internal/ledger/ledger.go
> #   "Cross-check the posted tax lines against the derived split when the caller
> #    identified the tax ledgers. This guarantees the books match the engine."
> #   -> "ledger: %s posting %s != derived %s"
> ```
>
> **GST derives and VERIFIES; it does not generate.** The client states the postings, names the
> ledger, and the server refuses any disagreement with its own engine. ⚠️ **That shape fits step 5
> without touching `validatePostings` at all**, and it makes the wrong number a 422 naming both
> figures rather than a silently-inserted line.
>
> ⛔ **WHICHEVER YOU CHOOSE, WRITE DOWN WHY, AND SAY WHICH.** The item-invoice path DOES generate
> postings (`item_invoice.go`), so this repo contains both patterns and a reader cannot infer which
> rule applies. ⚠️ **And note the consequence for step 8:** a verify-design needs the client to
> compute the gain, which means the settlement SCREEN must know the bill's historical rate — so the
> open-bills read that feeds an `AGAINST` picker has to return it.

**What you must build, and none of it exists:**

1. ⛔ **A ROUTE THAT SETS `company_forex_settings.forex_gain_loss_ledger_id`.** There is none. The
   refusal is meaningless until an operator can satisfy it, so **build the setter first** — it is
   also the cheapest thing to drive.
2. ⛔ **THE LOOKUP FROM AN `AGAINST` REF TO ITS `NEW` REF'S RATE.** Nothing does this today
   (§0's grep). It is the query the whole step rests on. ⚠️ **Mind the two-table UNION (§4.2):** a
   bill may have been opened by `ledger_opening_bill`, not by a voucher, and an opening bill carries
   the same three columns precisely so a foreign opening bill can be settled.
3. The realised computation, the third posting, and the refusal.

⭐ **AND `routeAction` — REGISTRATION FACT 2 (§5).** A new route with no `routeAction` arm compiles,
passes every Go test (they call the SERVICE, not the route) and is **403 for the Owner**. Only an
HTTP call through the real middleware catches it. **That is probe 1 of your drive.**

---

### 4.2 ⚠️ STEP 6 — TWO FIELDS, A NETTING KEY, AND THE UNION THAT BREAKS IT

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
  be **DISCLOSED on the report** — netting USD against INR under one bill number produces a number
  that is wrong in a way nothing on the page reveals.
- ⚠️ **THE GRAND TOTAL AND THE AGEING SLABS STAY BASE-ONLY.** Summing mixed currencies is
  meaningless. A design decision, not parity — say so.

⛔ **AND IT IS A UNION OF TWO TABLES, WHICH IS WHERE THIS STEP GOES WRONG:**

```bash
grep -n "FROM bill_allocation\|FROM ledger_opening_bill" backend/internal/reporting/outstanding.go
#   272: FROM ledger_opening_bill ob
#   376: FROM bill_allocation b2
#   411: FROM ledger_opening_bill ob2
#   420: FROM bill_allocation b
```

**Four sites, two tables.** Handle only `bill_allocation` and a foreign party's OPENING bill silently
becomes a base bill: its ₹ figure is real, its foreign column blank, and the two net together.

⚠️ **AND THERE IS A THIRD ROW TYPE:** `OnAccount` — the derived residue filed under
`ledger.OnAccountRefName`. It is deliberately un-aged and outside every slab. **Decide what currency
it shows and say why** (an unallocated amount tied to no bill has no single rate).

---

### 4.3 ⚠️ STEP 7 — A REPORT THAT POSTS NOTHING, AND A MIGRATION NUMBER THAT IS NOT THE OBVIOUS ONE

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
> ```
>
> ⚠️ **`PLAN-TALLY-PARITY.md` §2.5 RESERVES 0081–0084 FOR T8**, and they are still untouched — three
> separate rows since then record *"0081–0084 remain the T8 gap and were not touched."*
> ⭐ **ITS `⛔ DO NOT TAKE THESE` IS ADDRESSED TO OTHER LANES, NOT TO YOU** — the gap exists so T8
> could move forward without colliding, and **you are T8**. Read the row before you decide either
> way. **But a migration numbered 0081 runs BEFORE 0085–0089 on a fresh database.** For a plain `CREATE TABLE`
> that is harmless. For anything that **restates `seed_company_auth_defaults`** it is exactly the
> defect `0071` exists to repair: a body installed lower is overwritten on every fresh database and
> the rules simply do not exist.
>
> ⭐ **SO STEP 7's ACL MIGRATION TAKES 0090+, AND DERIVES ITS BODY FROM 0087 — MEASURED:**
>
> ```bash
> grep -c "PERFORM seed_data_entry" backend/migrations/0087_gstr9_acl.sql   # 16
> grep -ln "CREATE OR REPLACE FUNCTION seed_company_auth_defaults" backend/migrations/*.sql | tail -1
> #   backend/migrations/0087_gstr9_acl.sql   ← the HIGHEST that restates it
> ```
>
> ⭐ **AND THE HOUSE RECIPE IS EXACT — §2.5's own rows state it twice, and the PERFORM COUNT IS THE
> ASSERTION.** 0086 restated 0076: *"14 → 15 PERFORMs, comment-stripped body diff is exactly one
> line"*. 0087 restated 0086: *"15 → 16 PERFORMs, … exactly one line"*. **Yours is 16 → 17, and the
> comment-stripped diff against 0087 must be exactly one line plus your new
> `seed_data_entry_*_rules` function.** ⚠️ **0088 asserts the census is STILL 16** because it
> deliberately writes no rule — so if your migration is the next one to touch the seeder, 16 is the
> number you are starting from and a different reading means somebody else moved first.
> ⛔ **Also write the BACKFILL**, not only the seeder: existing companies already have their levels,
> and 0086/0087 both record *"303/303 Data Entry levels covered"* as the measured proof.
>
> ⚠️ **0088 MENTIONS THE SEEDER BUT DOES NOT RESTATE IT**, and its comment at line 274 explains why
> at length — *"SLICE 2b ADDS NO REPORT NAME AND NO RESOURCE TOKEN"*. **Read that note before you
> decide you need a migration at all**; it lists the twelve migrations that do, and the test that a
> new report name is one of them.
>
> ⛔ **`disallow display` AND `disallow print`, BOTH.** 0011's coarse `allow display report` reaches
> a BARE resource and `internal/authz` matches it against ANY report name (defect D95) — **the GRANT
> is the default and the REFUSAL is what must be written.**
>
> ⭐ **Update `PLAN-TALLY-PARITY.md` §2.5 in place** when you take a number, either way.

⚠️ **AND THE REPORT NAME IS A COLLISION RISK.** Check the candidate against `REPORT_NAMES`
(`frontend/src/keystroke/types.ts`) **and** `GatewayScreen.tsx`, **in both directions** — a label
that is a SUBSTRING of a shipped one is a silent collision.

---

### 4.4 ⛔ STEP 8 — AND THE MEMO ASSUMES A ROSTER STATE THAT DOES NOT EXIST

The memo says the `FeaturesScreen` row should read **`partial`**. It cannot today:

```bash
grep -n "^type State" frontend/src/screens/FeaturesScreen.tsx
# type State = "always" | "on" | "server" | "off";
```

⭐ **DECIDE IT AND SAY WHICH YOU CHOSE.** Either add `partial` to the union **and its rendering and
its legend** — the honest option, since `server` was added for exactly this reason on the offline
row — or split the row the way step 3 already did for the masters. ⛔ **What you must NOT do is flip
`Multi-currency` to `on` while the unrealised half only reports**: this screen's own rule is that *"a
toggle that flips a flag with nothing behind it is worse than no toggle at all."*

⚠️ **THE ROW'S NOTE WAS ALREADY CORRECTED IN `1bb8a5c`** to say the data path exists over HTTP and no
screen offers it. **Read it before rewriting it** — it is currently true, and the state is `off`.

⭐ **THIS IS ALSO WHERE THE VOUCHER-ENTRY UI LANDS.** Step 4 deliberately shipped no currency field
on any voucher form: *a form that keys a dollar invoice the rest of the product cannot settle, age or
revalue is this screen's own failure mode.* By step 8 all three exist, so the field can ship.
⚠️ **`api/rate.ts` ALREADY HAS WHAT THE FORM NEEDS** — `normaliseRate` (pad the operator's `83.65` to
the stored `83.6500000000`, so no screen reports a spurious edit) and `foreignToBase` (a **suggestion**
for the base field; the operator may override it and be right).

---

## 5. ⛔ READ THE MEMO, BUT ONLY THESE PARTS

`docs/design/t8-multi-currency-model.md` is 472 lines and you do not need all of it.

| Section | Why |
|---|---|
| **Q2** (line 113) | *"Which amount is authoritative?"* — **THE BASE AMOUNT. CLOSED.** Read it once so you never re-litigate it |
| **Q3** (167) | realised gain/loss — the third-posting design and the refusal. **Step 5 is this** |
| **Q4** (208) | unrealised — the report-not-a-posting decision. **Step 7 is this** |
| **Q5** (222) | Outstandings: separate columns and a disclosed conflict. **Step 6 is this** |
| **§0.2** (36) | the two-table UNION that breaks Q5 if you forget `ledger_opening_bill` |

⚠️ **SKIP Q6 AND §6.** Q6 is blocked; §6 records a probe that overturned an earlier scouting claim.
⚠️ **§4.1's table describes `forex_schema_test.go` ONLY** — step 4 added two more test files (§8).

⭐ **AND CARRY §5's THREE REGISTRATION FACTS — they are measured and they will bite:**

1. **A new `MasterKind` is not like a new `ScreenKind`.** `tsc` names every missing `MasterKind`
   registration; it enforces **none** of a `ScreenKind`'s fifteen. **Step 7 adds a `ScreenKind`.**
2. ⛔ **A NEW ROUTE STILL NEEDS ITS `routeAction` ARM AND NOTHING TYPE-CHECKS THAT.** Default-denies
   unmapped `{companyID}` routes → compiles, passes every Go test, **403 for the Owner**.
   **Steps 5 and 7 both add routes.**
3. ⭐ **A NEW DATE-SHAPED WIRE FIELD IS A REGISTRATION POINT.**
   `TestEveryDateShapedWireFieldIsAccountedForByTheCutoff` scans package `server` **by shape** and
   will fail your build on any new `*_date` field until you classify it **with a written reason**.
   ⚠️ **STEP 7 HAS ONE: the report's "as on" date.** That gap shipped open three times, and **two of
   the three shipped with a comment asserting the control was in place.**

---

## 6. ⚠️ THREE THINGS THIS SESSION LEARNED THAT APPLY DIRECTLY TO STEPS 5–8

### 6.1 ⛔⛔ "AN ALTER RE-STATES" HAS NOW BITTEN **SEVEN** TIMES. STEP 5 IS THE EIGHTH CANDIDATE.

Every table keyed on `voucher_version_id` is rewritten from the request body, so **whatever the body
omits is gone**, and nothing refuses it because zero rows is a legal state. It has cost:
`bill_allocation`, `stock_movement`, `posting_cost_allocation`, `order_fulfilment`,
`voucher_gst_line`, and in `1bb8a5c` the forex triple — **which was live at BOTH ends at once**
(`GetVoucher` did not project it, and `postingsFromView` rebuilt the body by listing fields by hand).

⚠️ **STEP 5's GAIN/LOSS POSTING IS A THIRD LEG ON A VOUCHER AN OPERATOR CAN LATER EDIT.** Ask, before
you write it: *what does an alter that omits it do?* If the answer is "silently drops the realised
loss and leaves the voucher balancing", you have the same defect an eighth time.

⭐ **THE TEST THAT CATCHES IT IS ALWAYS THE SAME SHAPE, AND IT NEEDS ITS INVERSE CONTROL:** post →
GET → rebuild the PUT body **from that response** → read the rows back out. Then prove the data is
**destructible** by omitting it — without that, "it survived" is equally consistent with the rows
being immune to an alter. `backend/internal/server/voucher_readback_audit_test.go` is the file, and
`TestReadBackAudit_ForeignCurrencySurvivesAnAlter` is the newest worked example.

### 6.2 ⛔ AN AGGREGATE QUERY IS NOT A SAMPLE OF THE QUERIES A USER MAKES

`verify:demo` once reported 34/34 against a book that could not produce **any** of its twelve monthly
GST returns, because it asked for one GSTR-3B over the **whole year** — where a July credit note nets
against April and everything looks positive. Nobody files that window.

⚠️ **THE STEP 5 VERSION IS ALREADY WAITING: A SETTLEMENT AT THE SAME RATE AS THE BILL REALISES ZERO,
AND ZERO IS WHAT A BROKEN COMPUTATION ALSO RETURNS.** ⭐ **Every settlement probe must settle at a
DIFFERENT rate from the bill, and at least two probes must use two different rates**, or a hard-coded
constant passes. Step 4's drive does this and its figures are reusable: base 83,651.23 / foreign
1,000.00 / rate 83.6512345678, plus a second voucher at 75.1234567890.

⚠️ **AND A PARTIAL SETTLEMENT IS A DIFFERENT CASE FROM A FULL ONE.** `realised = foreign_settled ×
(Δrate)` — settle $400 of a $1,000 bill and only 40% of the difference is realised; the rest stays
unrealised on the open remainder, which is step 7's report. **A probe that only ever settles in full
cannot tell `foreign_settled` from `foreign_billed`.**

### 6.3 ⛔ A RATE IS NOT MONEY, AND THE BUG IS INVISIBLE

`StringFixed(4)` on a rate READS as consistency with every other number on a voucher body. It gives
`0.0033` for `0.0033333333` — a relative error of ~1.5e-4, thousands of rupees on a fifty-lakh
invoice — **and the truncated value round-trips through its own type perfectly, so nothing fails.**
Use `canonicalRate` (server, `hash.go`) and `api/rate.ts` (client). ⛔ **Never `parseFixed4`, whose
own comment says it TRUNCATES the fifth decimal.**

⚠️ **STEPS 6 AND 7 BOTH RENDER RATES ON A REPORT.** That is the next place this will be got wrong,
because a report formatter is exactly where "make the columns line up" wins arguments.

---

## 7. ⛔⛔ THE HOUSE STANDARD YOUR WORK MUST MEET

Rows have been re-opened in this repo for failing each of these.

- [ ] **A failing test written FIRST**, asserting the thing no existing test covers.
- [ ] ⛔ **MUTATION-PROVED.** Revert the fix and watch the new test go red **with the number the real
      system produced**. ⭐ For T8 that means naming **which value** is wrong — for step 5, the
      realised amount **and which ledger took it** — never just "the voucher did not post".
- [ ] ⛔ **DRIVEN AGAINST THE REAL SYSTEM**, on a fresh scratch book (§2.5), against a cored verified
      fresh (§2.1) **and a vite verified fresh (§2.2)**. **The oracle is `psql` or the wire, never
      the screen.**
- [ ] ⚠️ **DRIVE THE CONTROL TOO** — a base-currency voucher must still post, settle, age and report
      exactly as it does today. Every existing posting in the demo book is base; if T8 moves any of
      them, `verify:demo` will say so and you should want it to.
- [ ] ⛔ **AND MUTATION-PROVE THE GATE, NOT ONLY THE CODE.** Rebuild cored **with the fix removed**,
      restart it, and confirm your drive goes red **and that your new checks are the only reds**.
      ⭐ Step 4 did this: 50/50 → **37/50, the 13 posting-column probes the only reds.**
- [ ] Gates in §1 re-run and **stated in the commit message, with every deferral named.**
- [ ] `HANDOFF-PARITY-AUDIT.md` updated **with the measurement, the mutation proof and the drive in
      the house format**, plus a `### L36 — …` lane section at the end of §7.
      (**`L34` and `L35` are the templates** — copy their shape.)
- [ ] ⭐ **AND THE ROSTER ROW LAST** (§4.4). A feature nobody can find is a feature that is not built.

---

## 8. THE READING LIST, in order

1. **This file.**
2. `HANDOFF-PARITY-AUDIT.md` §7 **`### L35`** — what step 4 actually built, every mutation proof, and
   the two design decisions it deliberately did NOT take (no arithmetic check on `base ≈ foreign ×
   rate`; no entry UI). **Start here — it is the most recent measured account of this feature.**
3. `docs/design/t8-multi-currency-model.md` — **Q3, Q4, Q5, §0.2**. Skip Q6 and §6.
4. `backend/internal/ledger/forex_voucher_test.go` — the step-4 service tests, and the oracle every
   forex test inherits (*"a forex test whose only assertion is that the voucher POSTED proves
   nothing"*).
5. `backend/internal/ledger/forex_schema_test.go` — the four schema invariants already proved, so you
   pin new ones rather than the same ones.
6. `frontend/scripts/drive-t8-forex-entry.mjs` — the drive to extend for step 5, and the reap block
   to copy verbatim.
7. `backend/internal/reporting/outstanding.go` — **step 6 is this file**, and its `OnAccount` comment
   is the one to read before you touch the netting key.
8. `backend/migrations/0088_bank_statement_match.sql` **line 274** — why a slice may legitimately need
   NO ACL migration, and the twelve that did.
9. `HANDOFF-FEATURE-LOCK-LINE.md` §6 — what closing T8 actually finishes.
   ⚠️ Its numbers are stale (written 2026-08-27); §1 above is current.
