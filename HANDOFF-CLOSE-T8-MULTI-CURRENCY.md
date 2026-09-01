# HANDOFF — CLOSE T8, MULTI-CURRENCY. THE DESIGN IS DONE; THE VOUCHER PATH IS NOT.

**Written:** 2026-08-31, working tree CLEAN at `f381741` · **Branch:** `session/t32-and-close-7-3`
**Read time: 9 minutes.** ⭐ **Every claim in §1, §3 and §4 was MEASURED at `f381741`** and the
command is beside it.

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** It is self-contained.
§1–§2 get you a running app you can safely break. §3 is exactly what exists. **§4 is the work, and
its order is not yours to choose.** §7 is the house standard your work must meet.

⛔⛔ **DO NOT RE-DESIGN T8. `docs/design/t8-multi-currency-model.md` ANSWERS SIX QUESTIONS AND THE
ANSWERS WERE GIVEN BY THE REPO OWNER**, not recalled from Tally. Read it before you write a line;
§5 of this file tells you which parts you actually need.

---

## 0. ⭐ THE ONE-LINE VERSION

**T8 is the last buildable item on the feature-lock line** (`HANDOFF-FEATURE-LOCK-LINE.md` §6). Its
schema, its currency master and its rate table are **built, driven and mutation-proved**. What does
not exist is **the voucher path**: nothing anywhere writes `posting.currency_id`.

```bash
grep -n "INSERT INTO posting" -A 3 backend/internal/ledger/ledger.go
#   (id, company_id, voucher_version_id, ledger_id, dr_cr, amount)
#   ⛔ no currency_id, no foreign_amount, no rate — the columns exist and NOTHING WRITES THEM
```

⭐ **THE OTHER LOCK-LINE ITEM, T13 (interest), IS BLOCKED ON A HUMAN** and cannot be started: it
needs the day-count and simple-vs-compound statutory defaults sourced into `docs/statutory/`. So T8
is the whole of what is buildable.

---

## 1. THE STATE — verbatim, and what each number should be

```bash
cd /Users/yashbhalgat/code/tally-updated
git log --oneline -1          # f381741 feat: the year-end close reaches the operator…
git status --porcelain        # (empty)
```

**Gate baselines at `f381741`, ALL RE-RUN AT THAT COMMIT. Any red is yours.**

⚠️ **EVERY LINE IS RUN FROM THE REPO ROOT** and each `cd` is in its own subshell for that reason.
⛔ **AND `cd X && nohup … &` RUNS THE `cd` IN THE BACKGROUND SUBSHELL**, so the parent shell never
moves and the next line reads the wrong directory. That cost this session two failed runs — use
absolute paths in any block that backgrounds something.

```bash
(cd backend && go build ./... && go vet ./... && go test ./... -count=1)  # all green
bash backend/scripts/test_residue.sh                                     # OK: no residue
(cd frontend && pnpm exec tsc --noEmit)                                  # clean
(cd frontend && pnpm exec vitest run)                                    # 1853 passed / 114 files
(cd frontend && pnpm verify:demo "$(cat .demo-company-id)")              # 37/37 ⚠️ id is an ARGUMENT
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

### 2.2 ⚠️ VITE IS `localhost`, NOT `127.0.0.1`

`--strictPort`; WebAuthn origin matching is exact and vite binds `::1`. Use `http://localhost:5173/`.

### 2.3 ⛔⛔ AN EMPTY `?company=` MINTS A FRESH TENANT AND EVERY SCREEN LOOKS CLEAN

The app bootstraps an empty **"My Company"** when none is named. **Two audit rows were wrongly logged
as passing that way.** Always pass `?company=<uuid>`, and read the **id**, not the name — there are
~95 companies called "Demo Traders".

### 2.4 ⛔ DIRTY A SCRATCH BOOK, NEVER THE DEMO ONE

```bash
(cd frontend && pnpm seed:demo -- --name "T8 Scratch")   # -> .t8-scratch-company-id
```

⭐ **The name and the id file move together.** With **no** `--name` the seeder writes
`.demo-company-id`, which every gate reads. After any session re-run
`pnpm verify:demo "$(cat frontend/.demo-company-id)"` and expect **37/37**.

⚠️ **EXISTING SCRATCH BOOKS ALREADY CARRY DRIVE DATA — MAKE A FRESH ONE:** `.d108-fix-scratch-`,
`.d113-carry-scratch-`, `.year-end-scratch-` (its books have been closed and re-opened).

### 2.5 ⚠️ THE FIRST WRITE OF A SESSION CARRIES A WEBAUTHN CEREMONY

The Owner level requires step-up: the first `POST /v1/…/vouchers/*` returns **403**, the ceremony
runs, the write is retried. **That 403 is not a bug.** Install a virtual authenticator over CDP
**before the first navigation** — or just use `openApp()` from `frontend/scripts/audit-harness.mjs`,
which does it for you.

⛔ **T8 IS A WRITE TRACK, so you will meet this on probe one.**

---

## 3. ⭐ WHAT T8 ALREADY IS — MEASURED, NOT CARRIED

**Steps 1–3 of the memo's build order are DONE.** Do not redo them.

### 3.1 The schema — ALL of it, in `0080` (`20dd96f`)

```bash
psql "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable" -X -A -F' | ' -c "
SELECT table_name, column_name FROM information_schema.columns
 WHERE column_name IN ('currency_id','foreign_amount','rate')
   AND table_name IN ('posting','bill_allocation','ledger_opening_bill','currency_rate')
 ORDER BY table_name, column_name;"
```

| Table | What it carries |
|---|---|
| `currency` | the master. ⛔ A **trigger refuses the company's own base currency** — a base posting is encoded by `currency_id IS NULL`, and one encoding only |
| `currency_rate` | rates by date, `NUMERIC(19,10)`, one per (currency, day) |
| `posting` | `currency_id`, `foreign_amount NUMERIC(19,4)`, `rate NUMERIC(19,10)` |
| `bill_allocation` | the same three |
| `ledger_opening_bill` | ⭐ **the same three, and §0.2 of the memo is why** — Outstandings nets a UNION of `bill_allocation` **and** `ledger_opening_bill`, so columns on only the first would silently turn a foreign party's opening bill into a base bill |
| `company_forex_settings` | `forex_gain_loss_ledger_id`. ⛔ **A TABLE, not a column on `company`** — a census caught that a `company → ledger → account_group → company` cycle makes **every tenant undeletable**, breaking backup/restore |

⛔ **THE `all_or_none` CHECK IS ALREADY THERE AND IT IS THE POINT:**

```
posting_forex_all_or_none CHECK (
  (currency_id IS NULL AND foreign_amount IS NULL AND rate IS NULL) OR
  (currency_id IS NOT NULL AND foreign_amount IS NOT NULL AND rate IS NOT NULL))
```

Base, foreign and rate are **three stored values and two can be wrong while the voucher balances
perfectly.** The database refuses two of the three; your write path must supply all three or none.

⚠️ **`rate` IS `NUMERIC(19,10)` AND MONEY IS `(19,4)`. A rate is not money.** A rate read as money
quantises, and a fifty-lakh invoice moves by thousands. The client type must be `Rate`, **not**
`Money`.

### 3.2 The currency master + rate write path — DONE, driven 15/15

`backend/internal/masters/currency.go`; `GET/POST /currencies`, `POST /currencies/rates`; the typed
client, a faithful mock, and `Alt+G > Create Currency` in `MasterCreateScreen.tsx`.

### 3.3 ⛔ WHAT IS NOT BUILT — the whole of the feature

```bash
grep -n "INSERT INTO posting" -A 3 backend/internal/ledger/ledger.go   # no currency columns
grep -rn "forex_gain_loss" backend/internal --include='*.go' | grep -v _test   # (nothing)
```

**Nothing writes a foreign posting. Nothing reads `company_forex_settings`. There is no route that
sets the gain/loss ledger.** The roster row says so and is wholly true:

> *"You cannot yet key a voucher in a foreign currency, settle a foreign bill, or see an exchange
> gain or loss — those are the feature, and none of them exists."*

### 3.4 ⭐ THE ONLY GUARD ON 0080 TODAY, AND WHAT IT IS FOR

`backend/internal/ledger/forex_schema_test.go` — database tests, deliberately, because **there is no
Go write path to test.** Its header states the rule your work inherits:

> *"A forex test whose only assertion is that the voucher POSTED proves nothing: base, foreign and
> rate are THREE stored values and two of them can be wrong while the voucher balances perfectly. So
> each test names the specific value it is pinning."*

---

## 4. ⭐⭐ THE WORK — FIVE STEPS, AND THE ORDER IS THE MEMO'S

From `docs/design/t8-multi-currency-model.md` §5, steps 4–8. **Steps 1–3 are done (§3).**

| # | Step | Notes |
|---|---|---|
| **4** | **Foreign entry on a voucher** — the three values stored end to end | The client's rate type is `Rate`, **not** `Money` (§3.1) |
| **5** | **Realised gain/loss on settlement**, including the REFUSAL when no ledger is chosen | §4.1 below — the meatiest step |
| **6** | **Outstandings gains two fields + the currency in its netting key** | incl. the mixed-currency `ref_name` disclosure |
| **7** | **The unadjusted-forex report** + its ACL migration, **same commit** | `disallow display` **AND** `disallow print` |
| **8** | **F11 last** — enforcement first, switch second | ⚠️ **and see §4.3, the memo is wrong here** |

⛔ **Q6 (GST ON AN EXPORT INVOICE) IS NOT IN THIS LIST AND MUST NOT BE STARTED.** It stops at the
design document until real GST data has been through the importer.

### 4.1 ⭐ STEP 5 IS THE ONE WITH A REAL DECISION ALREADY MADE FOR YOU

**Realised gain/loss is a THIRD POSTING ON THE SETTLEMENT VOUCHER**, so the voucher still balances in
base and `check_double_entry` passes untouched. Both rates are already stored:

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

### 4.2 ⚠️ STEP 7 POSTS NOTHING, AND THE PRODUCT MUST SAY SO

Unrealised gain/loss is **a report, in v1** — per the owner's own answer. It reads the open-bill
UNION and shows, per bill: foreign pending, the bill's rate, the rate as on the report date, the base
figure in the books, and the difference. ⛔ **It posts nothing, and the screen and F11 must say so in
those words.** The revaluation *voucher* is deferred and costs no design debt: postings are immutable,
so it could only ever BE a voucher, and v1's schema is the schema it would need.

### 4.3 ⛔ THE MEMO'S STEP 8 ASSUMES A ROSTER STATE THAT DOES NOT EXIST — MEASURED

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

### 4.4 ⛔⛔ MIGRATION NUMBERS — T8 HAS A RESERVED BLOCK AND IT IS **NOT** THE END OF THE LIST

```bash
ls backend/migrations/*.sql | tail -2   # 0088_bank_statement_match.sql, 0089_close_financial_year.sql
```

⚠️ **THE USUAL RULE IN THIS REPO IS "TAKE THE NEXT NUMBER AT THE END". T8 IS THE DOCUMENTED
EXCEPTION.** `PLAN-TALLY-PARITY.md` §2.5 reserves **0081–0084** for T8 as a deliberate gap so this
lane can move forward without colliding, and three separate rows since then record *"0081–0084 remain
the T8 gap and were not touched."* **Take 0081 next.**

⛔ **BUT KNOW WHAT THAT COSTS.** A migration numbered 0081 runs **before** 0085–0089 on a fresh
database. For a plain `CREATE TABLE` that is harmless. For anything that **restates
`seed_company_auth_defaults`** it is exactly the defect `0071` exists to repair: a body installed
lower is overwritten on every fresh database and the rules simply do not exist. ⭐ **So step 7's ACL
migration — the only one here that restates the seeder — must take a number at the END (0090+), and
must derive its body from the HIGHEST-numbered one (`0087`, 16 PERFORMs).** Update §2.5 in place when
you take either.

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
| **§4.1** (324) | what `forex_schema_test.go` already guards, so you do not re-pin it |

⚠️ **SKIP Q6 AND §6** unless you are curious: Q6 is blocked, and §6 is the record of a probe that
overturned an earlier scouting claim about the importer.

⭐ **AND CARRY §5's THREE REGISTRATION FACTS — they are measured and they will bite:**

1. **A new `MasterKind` is not like a new `ScreenKind`.** `tsc` names every missing `MasterKind`
   registration; it enforces **none** of a `ScreenKind`'s fifteen.
2. ⛔ **A NEW ROUTE STILL NEEDS ITS `routeAction` ARM AND NOTHING TYPE-CHECKS THAT.** `routeAction`
   default-denies unmapped `{companyID}` routes, so a new route with no entry compiles, passes every
   Go test (they call the SERVICE, not the route) and is **403 for the Owner**. Only an HTTP call
   through the real middleware catches it.
3. ⭐ **A NEW DATE-SHAPED WIRE FIELD IS A REGISTRATION POINT.**
   `TestEveryDateShapedWireFieldIsAccountedForByTheCutoff` scans package `server` **by shape** and
   will fail your build on any new `*_date` field until you classify it **with a written reason**.
   That gap shipped open three times, and **two of the three shipped with a comment asserting the
   control was in place.**

---

## 6. ⚠️ TWO THINGS THIS SESSION LEARNED THAT APPLY DIRECTLY TO T8

### 6.1 ⛔⛔ A NEW SOURCE OF TRUTH INHERITS THE ENFORCEMENT IT REUSES — BUT NOT THE GATES IN FRONT OF IT

The year-end lock (`bc57f9b`) reused the PDP's hardened back-dated-cutoff path and the PDP was
correct from the first commit. But the middleware only reads a voucher's **stored** date when it
thinks a lock is active, and that gate read `level.BackdatedCutoff != nil` — written when that was
the only lock there was. With only the new lock set, the read was skipped and

```
DELETE /v1/companies/{C}/vouchers/{id}   -> 200   (removed from a CLOSED year)
```

⭐ **THE UNIT TESTS COULD NOT SEE IT** because they build the PDP request by hand; a caller that
never builds it is invisible to them. **The wire drive found it.**

⚠️ **T8 IS THE SAME SHAPE.** `posting.currency_id` is a new source of truth threaded through a path
with existing gates, guards and readers — `check_double_entry`, the hash chain, the importer, the
sync commit, Outstandings. **Find every reader of the base amount before you add a second amount
beside it.**

### 6.2 ⛔ AN AGGREGATE QUERY IS NOT A SAMPLE OF THE QUERIES A USER MAKES

`verify:demo` reported 34/34 against a book that could not produce **any** of its twelve monthly GST
returns, because it asked for one GSTR-3B over the **whole year** — where a July credit note nets
against April and everything looks positive. Nobody files that window.

⚠️ **THE T8 VERSION OF THIS MISTAKE IS ALREADY WAITING:** a probe that posts one foreign invoice and
settles it at the same rate will be green and will prove **nothing**, because the gain/loss is zero
and zero is what a broken computation also returns. ⭐ **Every forex probe must settle at a
DIFFERENT rate from the bill**, and must name the specific value it pins (§3.4).

---

## 7. ⛔⛔ THE HOUSE STANDARD YOUR WORK MUST MEET

Rows have been re-opened in this repo for failing each of these.

- [ ] **A failing test written FIRST**, asserting the thing no existing test covers.
- [ ] ⛔ **MUTATION-PROVED.** Revert the fix and watch the new test go red **with the number the real
      system produced**. ⭐ For T8 that means naming **which of the three values** is wrong, never
      just "the voucher did not post".
- [ ] ⛔ **DRIVEN AGAINST THE REAL SYSTEM**, on a **fresh scratch book** (§2.4), against a cored
      verified fresh (§2.1). **The oracle is `psql` or the wire, never the screen.**
- [ ] ⚠️ **DRIVE THE CONTROL TOO** — a base-currency voucher must still post, balance and report
      exactly as it does today. Every existing posting in the demo book is base; if T8 moves any of
      them, `verify:demo` will say so and you should want it to.
- [ ] ⛔ **AND MUTATION-PROVE THE GATE, NOT ONLY THE CODE.** If you add a check to `verify:demo`,
      rebuild cored **with the fix removed**, restart it, and confirm the gate goes red **and that
      your new checks are the only reds**.
- [ ] Gates in §1 re-run and **stated in the commit message, with every deferral named.**
- [ ] `HANDOFF-PARITY-AUDIT.md` updated **with the measurement, the mutation proof and the drive in
      the house format**, plus a `### L35 — …` lane section at the end of §7. (**`L32` and `L34` are
      the templates** — copy their shape.)
- [ ] ⭐ **AND THE ROSTER ROW LAST** (§4.3). A feature nobody can find is a feature that is not built.

---

## 8. THE READING LIST, in order

1. **This file.**
2. `docs/design/t8-multi-currency-model.md` — **§5 first** (the build order), then Q2, Q3, Q4, Q5,
   §0.2, §4.1. Skip Q6 and §6.
3. `backend/migrations/0080_multi_currency.sql` — the schema you are writing against, and its header
   on why the base amount stays authoritative.
4. `backend/internal/ledger/forex_schema_test.go` — the four invariants already proved, so you pin
   new ones rather than the same ones.
5. `HANDOFF-PARITY-AUDIT.md` §7's **`### L34`** — the house format, and a worked example of a wire
   drive finding what unit tests could not (§6.1).
6. `HANDOFF-FEATURE-LOCK-LINE.md` §6 — the lock line, so you know what closing T8 actually finishes.
   ⚠️ Its numbers are stale (written 2026-08-27); §1 above is current.
