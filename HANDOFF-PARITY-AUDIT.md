# THE PARITY AUDIT — ⛔ THE AUDIT IS DONE. THIS FILE IS NOW **DATA**, NOT A DRIVER.

> # ⛔⛔ STOP. IF YOU ARE HERE TO FIX DEFECTS, READ `HANDOFF-DEFECT-BURNDOWN.md` FIRST.
>
> That file is the live driver: the parallel execution plan, the lane/file-ownership table, the five rules,
> and the traps. **THIS file is the DATA it points at** — §7.1's register of every defect found by driving,
> each at file:line with a proposed fix, plus §7.2's six-door cutoff lesson and §7.3's still-open list.
>
> **Do not plan work from this file and do not copy its rows anywhere.** Two copies drift and the drifted
> one gets believed.

> ## ⛔ THIS IS A WORKSHEET, NOT A RIVAL HANDOFF. `HANDOFF.md` IS STILL THE LIVE ONE.
>
> §11 of `HANDOFF.md` records eleven SPENT `HANDOFF-*.md` files and the cost of having them around:
> *"a handoff nobody can trust costs a session to re-verify."* It is owned by `HANDOFF.md` §1.11.
>
> ⚠️ **ITS ORIGINAL INSTRUCTION WAS "WHEN THE AUDIT IS DONE, FOLD INTO `HANDOFF.md` §5.5 AND DELETE
> THIS FILE." THE AUDIT IS NOW DONE AND THE FILE HAS DELIBERATELY NOT BEEN DELETED — read why:**
> the audit's product turned out to be **§7.1's register of 78 defects, 66 of them still open**, each
> traced to file:line with a proposed fix. Deleting that would destroy the only thing this exercise
> produced and guarantee it is re-derived from scratch, which is the exact cost §11 warns about.
> **So its JOB has changed: it is no longer a work list for driving, it is the backlog for fixing.**
> ⛔ **Whoever finishes the backlog should delete it then — and should not add a twelfth handoff now.**

---

# ⛔⛔ COLD START? READ THIS, THEN §7.1, THEN GO.

**Rewritten 2026-08-07 at the end of the THIRD audit pass, for a session with an empty context window.**

## WHAT HAPPENED, IN ONE PARAGRAPH

34 rows of `FeaturesScreen` claim to be reachable from the keyboard. **All 34 have now been driven end
to end in a real browser against real cored** — 16 in passes 1–2, and the final **18 in this pass**.
⛔ **NOT ONE OF THE 18 CAME BACK CLEAN.** 17 were defective and 1 (row 32) cannot be reached from the
keyboard at all. **The audit produced 78 defects — 39 high, 28 medium, 11 low.** 9 were fixed outright
this pass and 3 partly, each mutation-proved and re-driven in a browser; **66 remain open and are the
job now.** ⛔ **Your work list is §7.1, not §3.**

| | |
|---|---|
| **Rows driven end to end** | ⛔ **34 of 34. THE MEASUREMENT IS COMPLETE** — this was the open question and it is closed |
| **Ever driven and found clean** | ⚠️ **5 of 34**, all in earlier passes |
| **Carry at least one defect an operator hits** | ⛔ **29 of 34** |
| ⛔ **YOUR WORK LIST — the open defects** | **§7.1's register: 66 open, every one at file:line with a proposed fix** |
| **Lie rate across everything ever driven** | **28 lying in 34 driven.** ⛔ Budget for FIXING. Driving is finished |

## ⛔ WHAT IS STILL OPEN FROM EARLIER PASSES

| # | What | Where |
|---|---|---|
| **Row 20** | ⛔ **CONFIRMED LYING, STILL NOT FIXED.** `Alt+C` renders **"Create ledger"** from the Gateway AND from the item-invoice Item cell. No keystroke, Gateway entry or Go To entry reaches `masterKind` `"stock-item"` or `"unit"` — **a company can never be given a stock item or a unit from the keyboard**, and the item-invoice empty state points at `Gateway > Masters`, which does not exist. It is a FEATURE TO BUILD | §7's row-20 box has the traced route (RESEARCH.md:568 documents it) and the reason the obvious keymap fix cannot work |
| **LEAD 1b** | ✅ **CLOSED — KILLED IN THE BROWSER, 3rd pass.** The predicted step-up wipe does NOT occur on `MembersScreen`: driven on BOTH write paths, twice each (first press carrying the ceremony, second press as the control), and again at **20× CPU throttle**. `refresh` clears the error synchronously *before* the retry's response can land, so the refusal is always the last write to `error`. ⛔ **It was deliberately left unpatched on a code reading, and driving it proved that was the right call** | §7's log |

## THE ORDER TO WORK IN — ⛔ THIS IS NOW A FIXING ORDER, NOT A DRIVING ORDER

1. **§0.1's RUN-FIRST CHECKLIST** (immediately below) — ten minutes, and skipping it has burned sessions.
2. **Work §7.1's register, highest severity first.** The 39 `high` rows are where an operator loses money,
   goods, or the ability to work at all. Suggested grouping, because file ownership is what serialises
   this work (§8) and these cluster naturally:
   - **Bill-wise / Outstandings** (rows 3, 5): the unnamed-reference stamp in `ledger.go:1754-1756` and
     `outstanding.go`'s `as_on`-for-ageing-but-not-inclusion are **one cluster** — four of the register's
     high defects collapse into those two places, and D-row-3's stamp is the root of three of them.
   - **Printing / export** (rows 40, 42, 43): several are dead-key and wrong-layout defects, individually
     cheap, and `printTarget.ts` owns most of them.
   - **NL query** (row 31): three of the four are in `query/translate.go`'s keyword switch.
   - **TDS** (row 17): establish the missing allocate-later ROUTE first; it decides the rest.
   - **Orders** (row 27): seven defects, mostly independent and mostly small.
3. ⛔ **Every fix carries a MUTATION PROOF *and* a BROWSER RE-DRIVE.** §8's rules are unchanged and the
   third pass re-confirmed why: a mutation proof proves the TEST, never the FIX.
4. **Mark the register row, and say what the fix deliberately did NOT close.** Three entries in §7.1 are
   deliberately-unfixed with reasons; follow that pattern rather than silently leaving a gap.

⚠️ **DO NOT PLAN TO FINISH THE BACKLOG IN ONE SESSION.** The third pass drove 18 rows and fixed 12
defects with 6 parallel lanes and it filled a session completely. **66 open defects is several sessions.**
Every defect fixed, mutation-proved, re-driven and marked in §7.1 is permanent progress.

## THE TOOL — COMMITTED, NOT DESCRIBED

`frontend/scripts/audit-harness.mjs` signs in, installs a virtual WebAuthn authenticator so the
step-up completes by itself, records every non-GET as a wire oracle, and carries the three traps that
cost the last pass an hour. **Import it; do not rewrite it.**

```js
import { openApp, rec, summary } from "./audit-harness.mjs";
const app = await openApp({ company: process.env.CO });
const { page, body, press, toGateway, altMac, openFromGateway, net } = app;
await toGateway();
await altMac("g");                     // ⛔ NOT press("Alt+g") — see below
rec("Go To opened", /Go To/.test(await body()));
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
```

```bash
cd /path/to/tally-updated                                  # ⛔ ABSOLUTE paths
CO="$(cat frontend/.demo-company-id)" node frontend/scripts/<probe>.mjs
```

## ⛔⛔ THE FOUR TRAPS THAT HAVE COST REAL TIME. ALL FOUR ARE LIVE FOR YOU.

1. **`press("Alt+g")` IS A LIE ON macOS.** Option COMPOSES: a Mac sends `key: "©"`, never `"g"`.
   Playwright sends `"g"`, which no operator here can produce. **Every Alt+letter binding in the
   product was found DEAD on macOS this way, while every browser test passed.** Use `altMac("g")`.
   The resolver is fixed; the METHOD is what hid it.
2. **`Alt+G` IS NOT THE ONLY DOOR.** The Go To picker is hard-wired to `["Vouchers", ...REPORT_NAMES]`.
   Cost Centres, both cost summaries, Bank Reconciliation, TDS Challans, both order registers and the
   BOM screen are **absent from it and present on the GATEWAY MENU**. Use `openFromGateway(name)`.
   Two rows in §3 still name the wrong key. **A screen that will not open is not thereby unbuilt.**
3. **AN EMPTY `?company=` BOOTSTRAPS A FRESH EMPTY TENANT**, and every screen on it looks clean. Two
   rows were wrongly logged as passing that way. `openApp` now REFUSES an empty id and asserts the
   chrome names "Demo Traders" — do not remove those guards.
4. **`verify:demo` NEEDS `pnpm seed:demo --insightd`.** Without the flag no Bill Inbox draft is queued
   and the gate reports **30/31 with one FAIL**, which reads exactly like a regression.

## ⛔⛔ THE ONE METHOD LESSON THAT OUTRANKS EVERYTHING ELSE HERE

The last pass traced a defect to a line, fixed it there, wrote a regression test that PASSED, and ran
a mutation proof that FAILED EXACTLY AS PREDICTED. **And the browser was still broken**, because the
traced cause was not the live one — there were two defects of the same shape on the same key.

> **A mutation proof proves the TEST is real. It does not prove the FIX is sufficient.
> Re-drive the browser after every fix. It is the only oracle that has never been wrong here.**

That is also why LEAD 1b is left unpatched: fixing it from a code reading would repeat exactly that
mistake.

## DEFINITION OF DONE, PER ROW

- The exact keystrokes an operator uses were **pressed in a browser against real cored** — not a test.
- The result was **read back from the oracle in §3's table**, which for anything moving money or goods
  means `psql` on `posting` / `stock_movement` joined to `ledger.name`. ⛔ **Never assert a total: a
  reversed voucher balances to the same paisa. Assert SIDES and LEDGER NAMES, and read VALUE as well
  as quantity.**
- Anything found is **fixed, mutation-proved (and check the failure is the one you predicted), and
  RE-DRIVEN in the browser.**
- The row is **written into §7's log** and the running count updated.
- **No residue**: re-seed, and leave every gate green. §0.1 has the commands; §8 has the numbers.

---

## 0. THE ANSWER TO THE FIRST QUESTION: THIS IS NOT ONE SESSION

Stated up front so nobody plans a session that cannot happen. **The ask is "audit 34, then build 13,
in one session." The audit alone is 1–2 sessions and the 13 builds are roughly 6–10.** That is not
pessimism; it is this repository's own measured rate:

| Evidence | Source |
|---|---|
| "Plan for roughly one track per session — the last three delivered exactly one each and all were full" | `HANDOFF.md` §3.1 |
| "**A track needing a SCREEN is a session.**" | §1.12 |
| "the frontend half of a track is not smaller than the backend half" — T3's frontend was a full session | §3.1 |
| Driving **ONE** new screen end to end found **THREE** defects, two in code that session never touched | §5.5 |
| The item this file's ancestor called "small, certain, needs no architectural decision" **took a whole session** | §3.1 |

⛔ **AND THE AUDIT'S COST IS NOT THE DRIVING — IT IS WHAT THE DRIVING FINDS.** Every session that has
driven `on` rows has found at least one lying, and the fix is then real work with its own tests and
mutation proofs. Last session found four. The session before found three. **Budget for the fixes, or
the audit will stop at the first one.**

**So do it in this order, and stop wherever the session ends — every phase is independently valuable:**

1. **PHASE A — the audit** (§2–§4). Produces the *true* number. Highest value per minute in the repo.
2. **PHASE B — fix what A finds** (inline, as found). Do not defer: a defect found and not fixed is a
   defect that will be re-found from scratch.
3. **PHASE C — build the 13** (§5), in the stated order. Expect several sessions.
4. **PHASE D — the big three** (§6): payroll, offline sync, backup/restore. Explicitly a later session.

> ### ⚠️ IF YOU ONLY GET THROUGH PHASE A, THAT IS A GOOD SESSION.
> The number "34 reachable" is currently an **upper bound with no measurement behind it**. Converting
> it to a measured number, with the liars named, is worth more than two half-built features.

---

## 0.1 ⛔ RUN FIRST — the whole stack, the seed, and the gates. Ten minutes.

**Everything below is in THIS file on purpose: a cold session should not have to open `HANDOFF.md`
to get started.**

```bash
cd /path/to/tally-updated
git status --porcelain          # commit or stash BEFORE anything

# 1. POSTGRES. ⛔ A TCP CONNECT IS NOT LIVENESS — docker-proxy holds the port open
#    past a dead backend, and a watchdog built on `nc -z` logged NOTHING through a
#    ten-minute outage.
docker ps | grep tally_pg
pg_isready -h localhost -p 5433 -t 5 && \
  PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"

# 2. THE FOUR SERVICES. Kill stale ones BY PID — `pkill -f "cmd/cored serve"`
#    misses the compiled child, and a SEVEN-DAY-OLD statutoryd on 8091 once cost an
#    hour (it held a service token signed by a cored key that had died a week
#    earlier; the symptom was a bare 401 naming neither process, token nor expiry).
for p in 8087 8088 8091 5173; do printf "%s: " $p; \
  curl -s -o /dev/null -w "%{http_code}\n" --max-time 3 http://localhost:$p/healthz; done
```

⛔ **START cored AND statutoryd DETACHED, or they die with your own tool call.** `setsid` does not
exist on macOS; the subshell-orphan form is what works, and a harness-managed background job gets
killed at teardown (this happened on 2026-08-07):

```bash
(nohup make run-cored      > /tmp/cored.log      2>&1 < /dev/null &)
# wait for :8087, THEN start statutoryd — it mints its token FROM cored, and
# every cored restart invalidates the previous one.
(nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 < /dev/null &)
# insightd :8088 and vite :5173, if not already up:
#   cd insightd && .venv/bin/python -m uvicorn insightd.api.app:app --port 8088
#   cd frontend && pnpm dev
```

```bash
# 3. SEED, and ⛔ NOTE THE FLAG — without it verify:demo reports 30/31 with one
#    FAIL that reads exactly like a code regression.
cd frontend && pnpm seed:demo --insightd
pnpm run verify:demo "$(cat .demo-company-id)"      # -> ALL CHECKS PASSED (33/33)
```

⚠️ **`seed:demo` MINTS A NEW COMPANY EVERY RUN** and rewrites `.demo-company-id`. Re-read that file
after seeding; do not cache the id. It also leaves the previous demo tenants behind — the dev
database holds a growing pile of them plus ~16 scratch `My Company` rows from the e2e harness, which
nothing reaps.

```bash
# 4. THE GATES. ⛔ RUN `make verify` ALONE — see §8; beside other load it fails two
#    ways that are NOT code, and one of them was reproduced on a STASHED, UNMODIFIED
#    tree, which is the only reason it was not mistaken for a regression.
make verify                                        # -> exit 0
cd frontend && pnpm exec tsc --noEmit              # a SEPARATE gate from make verify
cd backend && go test ./... -count=1 -v | tee /tmp/base.log | tail -3
grep -c -- '--- PASS' /tmp/base.log; grep -c -- '--- SKIP' /tmp/base.log   # SKIP MUST BE 0
```

⚠️ **A DOWN DATABASE MAKES EVERY DB TEST SKIP AND `go test` STILL EXITS 0.** A skip is
indistinguishable from a pass in the exit code, and this has invalidated a whole session's evidence
before. **The `0 SKIP` is the load-bearing number** — bracket the run with a live `psql`.

### Signing in, and the DB oracle

The app is at `http://localhost:5173/?company=$(cat frontend/.demo-company-id)`, user
`browser-dev-owner`, password `tally-demo-2026`. **It is a keystroke app: a click SELECTS, `Enter`
OPENS.** In practice you drive it with `frontend/scripts/audit-harness.mjs`, not by hand.

⛔ **`psql` AS `tally_app` RETURNS ZERO ROWS FOR EVERY TENANT TABLE.** RLS needs a session GUC a bare
`psql` does not set, and the symptom is `count(*) = 0` — which reads exactly like "the write did not
happen" and nearly produced a false "the browser did not save it" conclusion. **Use the `postgres`
superuser for every DB oracle**, as every command in this file does.

---

## 0.2 ⛔ ALL 18 OF THESE ARE NOW DRIVEN — the table below is kept only as an index to their traces

> ⛔ **`PARITY-RECON-2026-08-07.md` IS NOW HISTORY, BY ITS OWN RULE** (*"where a row has since been
> driven, `HANDOFF-PARITY-AUDIT.md` §7's log is the authority and this file is history"*). Every row it
> traced has been driven. It is still worth opening for the *reasoning* behind an open defect — its
> per-row sections carry the oracles and the code paths — but ⛔ **its predictions are no longer leads,
> they are settled.** Final scorecard, which is the honest measure of what a read-only pass is worth
> here: **6 confirmed to 2 killed across the first 8 driven, and in this pass its headline predictions on
> rows 3, 17, 24 and 33 were confirmed exactly** (the unnamed-reference stamp; TDS having no
> allocate-later route; `allow_negative_stock` having no writer; invites reaching nobody). ⚠️ **No
> full per-prediction tally was kept for this pass, so treat "6/2" as the only measured hit rate and the
> rest as impressions.** Two of its claims were KILLED by driving — row 34 was called possibly
> undrivable and was driven, and row 33's "an invite can never be claimed" turned out to be too strong
> (F3 claims it). ⛔ **And it did NOT anticipate the single most disruptive defect of the pass — the
> WebAuthn write-lock — which only driving found, and which five separate agents then hit at once.**

`PARITY-RECON-2026-08-07.md` holds an 18-agent read-only trace of every row below: the exact
keystrokes, the oracle to read back, and falsifiable predictions at file:line. ⛔ **It is READING, and
this repo flips a row on BROWSER evidence only** — but its measured hit rate is **6 confirmed / 2
killed out of 8 driven**, which is good enough to prioritise by and not good enough to log a row on.

| Row | What it covers | Trace at |
|---|---|---|
| 3 | Bill-wise details — all FOUR reference types | `PARITY-RECON-2026-08-07.md:19` |
| 5 | Opening balances + a bill-wise opening breakup | `:122` |
| 9 | Cost allocation during entry across TWO axes | `:709` |
| 17 | TDS/TCS — record a challan and allocate it | `:635` |
| 24 | Negative stock — and the FLAG must govern it | `:329` |
| 27 | Orders (`Ctrl+F8`/`Ctrl+F9`) + BOM (`Alt+X`) | `:791` |
| 29 | Closing stock on the Balance Sheet at two dates | `:855` |
| 30 | Bill Inbox — OCR draft into a real voucher | `:906` |
| 31 | Ask your books (NL query) + a cross-tenant probe | `:940` |
| 32 | Cash-flow projection / anomalies | `:971` |
| 33 | Multiple users + security levels | `:999` |
| 34 | Custom levels — ⛔ **SIGN IN AS THAT LEVEL AND BE REFUSED** | `:1024` |
| 35 | Switch company (`F3`) — no row of the old company may leak | `:1049` |
| 37 | Back-dated entry cutoff | `:1102` |
| 38 | Password login + lockout | `:1127` |
| 40 | Printed documents (`Ctrl+P`), seven classes | `:1155` |
| 42 | Printed statements (`Ctrl+P`), per report | `:1207` |
| 43 | Export (`Alt+E`) — ⛔ WIRE precision, not display strings | `:1242` |

⚠️ **THE HIGHEST-SEVERITY PREDICTIONS AMONG THESE, all `high` confidence** — treat as leads, not facts:
- **Row 17** — TDS allocation has no route to attach to an existing challan, so **the row may not be
  drivable as written at all.** Establish that first; it changes what "driving row 17" means.
- **Row 24** — `allow_negative_stock` has no HTTP route, handler or UI: only half the rule exists.
- **Row 3** — two New refs on one invoice collapse into ONE bill; Outstandings honours `as_on` for
  ageing but not for inclusion.
- **Row 5** — an `Alt+C` party gets `maintain_bill_by_bill = false`, so it can never carry any bill
  reference. ⚠️ **This interacts with row 20's open defect** — both are `Alt+C` master gaps.
- **Row 9 / row 22 (rest)** — a BATCH can never be keyed anywhere; `batchId` is hard-wired `""`.
- **Row 6 (rest, already driven)** — a Contra creates its counter-account under **Indirect Expenses**.

## 1. WHAT WE ARE MEASURING, AND WHY THE CURRENT NUMBER IS A GUESS

Re-measured **2026-08-07** with §1's `python3` pairing method (47 `state:` lines, 47 adjacent pairs,
0 unpaired — so nothing was missed):

| State | Count | Meaning |
|---|---|---|
| `on` | 32 | claims to be reachable from the keyboard |
| `always` | 2 | structural, cannot be switched off |
| `server` | 2 | built, but no key reaches it |
| `off` | 11 | not built |
| **TOTAL** | **47** | |

⛔ **`on` IS AN UPPER BOUND, NOT A MEASUREMENT — AND THIS IS THE WHOLE POINT OF THE EXERCISE.**
Rows caught lying, every one by driving and never by reading:

| Row | What `on` was hiding |
|---|---|
| Journal (F7) | the form had **ONE input**; a Journal could not be keyed at all |
| Day Book drill | opened a **Sales** frame for every class; saving re-classed the document |
| Receipt after a Sale | posted `DR <the sale's customer>` instead of a bank |
| ALTER of a GST invoice | **stripped the tax** — an 11,800 invoice became 10,000 and left GSTR-1 |
| `Alt+E` export | bound, live, and **losing paisa** (exported display strings) |
| **F9 Purchase, Credit Note, Debit Note** | **posted the money BACKWARDS**, with OUTPUT tax where INPUT belongs. Server answered **201** |

**Roughly 20 of the 32 `on` rows have still never been driven end to end.**

---

## 2. THE METHOD — AND THE ORACLE RULE IS THE PART THAT MATTERS

For each row:

1. **Name the exact keystroke sequence an operator uses**, from the Gateway.
2. **DO IT in a browser against real cored.** Not a test. The app.
3. **Read the result back from the place the money actually lands** — see the oracle column in §3.
4. **Write the row into §7's log** with what you saw, pass or fail.

> ### ⛔⛔ AN AUDIT INHERITS THE BLIND SPOT OF ITS ORACLE. THIS HAS COST THIS REPO THREE TIMES.
>
> - §4.3's read-back audit cleared `bill_allocation` **on half a check** — an ALTER was destroying
>   references.
> - The same audit declared `stock_movement` SAFE on a **quantity-only** oracle. Mutating the GET's
>   rate to `"0.0000"` left it green while **1,000 of stock value** was written off by a narration
>   edit — and closing stock feeds the **Balance Sheet**.
> - This session: a test asserting a purchase is "the MIRROR of a sale", which reasons explicitly
>   about orientation, still pinned **`Output` tax on a purchase**.
>
> **So: never assert a total. Totals prove nothing about direction — a reversed voucher balances to
> the same paisa. Assert SIDES and LEDGER NAMES, and read VALUE as well as quantity.**

### The three oracles, in order of trustworthiness

| Oracle | Use when |
|---|---|
| **`psql` on `posting` / `stock_movement`, joined to `ledger.name`** | anything that moves money or goods. This is the only oracle that cannot be flattered by the client |
| **A REPORT the row feeds** (Balance Sheet, Outstandings, GSTR-1, Stock Summary) | to prove the entry reaches where a business reads it |
| **The screen re-rendering** | read-only screens only — mostly self-evidencing |

**The money oracle, ready to paste:**

```sql
select vv.voucher_no, vv.payload->>'class' as class, l.name as ledger, p.dr_cr, p.amount
from posting p
join voucher_version vv on vv.id = p.voucher_version_id
join ledger l on l.id = p.ledger_id
where l.company_id = '<COMPANY>'
order by vv.created_at desc, p.amount desc limit 12;
```

**The goods oracle — ⛔ `closing_value`, NOT just `closing_qty`:**

```sql
select si.name, sm.direction, sm.qty, sm.rate, sm.amount
from stock_movement sm join stock_item si on si.id = sm.stock_item_id
where si.company_id = '<COMPANY>' order by sm.created_at desc limit 12;
```

### ⛔ Rules for the pass itself

- **ONE signed-in browser, ONE virtual authenticator** (§9.7's CDP recipe). The browser pass is the
  orchestrator's; read-only code recon can be fanned out to subagents, the driving cannot.
- **⛔ RE-SEED BEFORE AND AFTER.** `pnpm seed:demo` — the demo company **is** `verify:demo`'s fixture,
  and manually-keyed vouchers make that gate fail for reasons that have nothing to do with the code.
  This cost time on 2026-08-07 (a blank HSN produced an empty GSTR-1 `hsn_sc` row → 32/33).
- **Drive MORE THAN ONE TYPE when a screen is parameterised by type.** "The Day Book works" was
  established with a Sales voucher and was false for every other class. **The default is always right
  for exactly one of them.**
- **A row covering a CLASS is not proved by one member.** Row 26 covers *four* documents; row 1
  covers *six*.
- ⛔ **`Alt+G` IS NOT THE ONLY DOOR, AND SEVERAL ROWS IN §3 NAME THE WRONG ONE.** The Go To picker
  is hard-wired to `["Vouchers", ...REPORT_NAMES]`, so Cost Centres, both cost summaries, Bank
  Reconciliation, TDS Challans, the order registers and the BOM screen are **absent from it and
  present on the GATEWAY MENU**. Rows 8 and 11 were written as `Alt+G` and are reached by typing the
  name into the Gateway's search box. **A row that will not open is not thereby unbuilt — check the
  other door before logging a defect.**
- ⛔⛔ **CHECK WHAT COMPANY YOU ARE ACTUALLY ON, IN THE BROWSER, BEFORE BELIEVING A RESULT.** On
  2026-08-07 an empty `?company=` (a shell `cd` had moved out from under `cat .demo-company-id`)
  made the app **bootstrap a fresh "My Company"**, and rows 8 and 11 both came back "clean" against a
  company with no bank account and no cost category. The tell is in the title bar: the demo reads
  **"Demo Traders"**. Assert it in the probe rather than reading it in a dump.
- ⚠️ **`verify:demo` NEEDS `pnpm seed:demo --insightd`, NOT `pnpm seed:demo`.** Without the flag the
  Bill Inbox draft is never queued and the gate reports **30/31 with one FAIL** — which reads exactly
  like a code regression. §2.1 of `HANDOFF.md` says to run the bare command; it is wrong.

---

## 3. ⛔ THE CHECKLIST — 34 REACHABLE ROWS

`STATUS` legend: `—` not started · `✅` driven clean · `⛔` defect found (log it) · `~` partly driven.

⚠️ **"PREV" MEANS DRIVEN IN AN EARLIER SESSION, NOT "SKIP IT".** Several rows on that list were later
found lying anyway (the Day Book was "known-good" for two sessions). Re-drive anything cheap; spend
the time on the `—` rows first.

### Accounting

| # | Row | Keystrokes | ⛔ ORACLE — what to read back | Status |
|---|---|---|---|---|
| 1 | Double-entry vouchers (6 classes) | `F4` `F5` `F6` `F7` `F8` `F9`, each → key → `Ctrl+A` | `posting`: sides + ledger NAMES, **per class**. Six documents, not one | PREV (F5/F6/F4 + F7) |
| 2 | Edit Log / audit trail `always` | Day Book → `Enter` → `Alt+L` | the version list shows v1 **and** v2 with who/when + a before/after diff | ⛔→✅ 2026-08-07 (screen real; **all four chrome holes fixed**; `Alt+L` was dead on macOS) |
| 3 | Bill-wise details (4 ref types) | `F8` sale to a bill-by-bill party → `F6` receipt → settle | Outstandings **reconciles to the Balance Sheet to the paisa**. Drive **all four** ref types | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 5 defects (5 high); §7.1 |
| 4 | Credit / Debit Notes | `Alt+F6` / `Alt+F5` → key → `Ctrl+A` | `posting` sides. **CN verified 2026-08-07; ⛔ DEBIT NOTE STILL NEVER DRIVEN** | ~ |
| 5 | Opening balances (with bill-wise breakup) | `Alt+C` create ledger with opening + bills | the opening bill appears in Outstandings and the Balance Sheet opens balanced | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 3 defects (2 high); §7.1 |
| 6 | Single-entry mode (Payment/Receipt/Contra) | `F5`/`F6`/`F4`, `Ctrl+H` → Single Entry | `posting`: one primary leg + N counter-legs, correct sides | PREV |
| 7 | Voucher numbering series | key 2 of a class; cross a FY boundary | `voucher_number_counter`: per (class, FY), monotonic, **restarts next FY** | PREV |
| 8 | Cost centres + both reports | ⚠️ **NOT `Alt+G`** — Gateway → Cost Centres; both summaries | per-(posting, **CATEGORY**) sums — ⛔ **NOT per posting**; axes are parallel (§8.1) | ✅ 2026-08-07 (reachable from the GATEWAY; the empty period prompt is deliberate and says why) |
| 9 | Cost allocation during entry | `F5` → allocate across 2 axes | `posting_cost_allocation`: **both** axes complete and independent | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 5 defects (2 high); §7.1 |
| 11 | Bank reconciliation | ⚠️ **NOT `Alt+G`** — Gateway → Bank Reconciliation | a marked line changes the reconciled balance and survives a reload | ⛔→✅ 2026-08-07 (**four defects, all fixed**: silent out-of-window tick, `voucher_no` collides 4 ways, blank `As at`, refused date left on screen) |

### Statutory

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 14 | GST (derivation, GSTR-1, GSTR-3B) | `F8` intra **and** inter-state | CGST/SGST vs IGST split; the supply appears in **GSTR-1 b2b AND the table-12 HSN summary** | PREV (partly) |
| 15 | e-Invoice / IRN | `Alt+I` → pick → `Ctrl+A` | IRN in `voucher_external_reference`; re-post returns the SAME IRN; ALTER+DELETE both 409 | ✅ 2026-08-06 |
| 16 | e-Way Bill | `Alt+Z` → state the movement → `Ctrl+A` | `e_way_bill` row; disclosures rendered in full; ⛔ **check the step-up wipe (§4 lead 1)** | ⛔→✅ 2026-08-07 (**LEAD 1 confirmed with a control; TWO defects, both fixed, on this screen AND `EInvoiceScreen`**) |
| 17 | TDS / TCS | `Alt+G` → TDS Challans → record + allocate | `tds_deduction` / `tds_challan_allocation`; the challan's unallocated balance falls | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 6 defects (4 high); §7.1 |

### Inventory

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 20 | Stock items + units (masters) | `Alt+C` → Stock Item | the master persists **with its unit** — a blank unit is what broke every e-invoice line | ⛔ 2026-08-07 — **LIES. `Alt+C` renders `Create ledger` everywhere; NO key reaches a stock-item or unit form. NOT FIXED — §7's box** |
| 21 | Item Invoice (`Ctrl+H`) | `F8`/`F9` → Ctrl+H → Item Invoice | `stock_movement` **qty AND value**; server-derived postings | PREV |
| 22 | Godowns / batches | key a line with a godown **and** a batch | movement lands on the **right position** — ⛔ see §4 lead 3 (the NULL-godown default) | — |
| 23 | Stock Summary (qty AND value) | `Alt+G` → Stock Summary | ⛔ **`closing_value`, not just `closing_qty`** — this exact oracle gap shipped once | PREV (qty only) |
| 24 | Negative stock `always` | sell more than you hold | refused, or allowed per `allow_negative_stock` — and the flag actually governs it | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 3 defects (1 high), F11 claim fixed; §7.1 |
| 26 | Inventory vouchers (**FOUR** classes) | `Alt+F7` `Alt+F8` `Alt+F9` `Alt+F10` | ⛔ **the two `Alt+F7`/`Alt+F10` leads in §4 are the highest-value unverified items in this file** | ~ (F8/F9 only) |
| 27 | Orders + BOM | `Ctrl+F8` `Ctrl+F9` `Alt+X` | order book Ordered/Delivered/Outstanding; ⛔ an order **moves no stock** | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 7 defects (2 high); §7.1 |
| 29 | Closing stock on the Balance Sheet | Balance Sheet at 2 dates | closing stock **moves the sheet** and the sheet still balances | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 5 defects (2 high); §7.1 |

### AI-native

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 30 | Bill Inbox (OCR → draft) | Gateway → Bill Inbox → accept a draft | the accepted draft becomes a real voucher with the OCR'd figures; PII stays redacted | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 8 defects (6 high), 3 partly fixed; §7.1 |
| 31 | Ask your books (NL query) | Gateway → Query | the answer matches the same figure read from a report; ⛔ a cross-tenant probe is refused | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 4 defects (2 high); §7.1 |
| 32 | Cash-flow projection / anomalies | `Alt+G` → the forecast report | it discloses that it is a projection, and the basis is stated | ⛔ **2026-08-07 (3rd) — NOT DRIVABLE FROM THE KEYBOARD.** 4 defects (2 high); §7.1 |

### Users and security

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 33 | Multiple users + security levels | `Alt+G` → Users & Roles → invite | membership appears; the invitee is PENDING until claimed | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 3 defects (3 high), 2 fixed; §7.1 |
| 34 | Custom levels (Disallow/Allow) | edit a level, save, act as it | ⛔ **sign in AS that level and be REFUSED.** Reading the rules back is not the test | ⚠️→✅ **2026-08-07 (3rd) — DRIVEN AS THE LEVEL HOLDER, AND THE ENFORCEMENT IS REAL.** A second user on a browser-authored custom level got `403 {"error":"disallowed by rule"}` on Stock Summary with a legible refusal naming the exact rule, while BOTH controls (Day Book, Balance Sheet) returned 200 with real seeded rows. ⛔ Drivable, contrary to the recon caveat. 2 defects around it (wrong level NAMED in the refusal; the shared WebAuthn write-lock, now fixed); §7.1 |
| 35 | Switch company (`F3`) | `F3` → pick | the whole session re-scopes; no row of the old company leaks | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 2 defects (1 high), 1 fixed; §7.1 |
| 36 | WebAuthn step-up for writes | any write | 403 `STEPUP_REQUIRED` → ceremony → retry succeeds. ⛔ **§4 lead 1 lives here** | PREV |
| 37 | Back-dated entry cutoff | key a voucher before the cutoff | refused **by name**, and a voucher after it still posts (control) | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 4 defects (3 high), 2 fixed; §7.1 |
| 38 | Password login + lockout | N bad passwords | locked out, and it **unlocks** as configured | ⚠️→✅ **2026-08-07 (3rd) — DRIVEN WITH 47 REAL KEYSTROKE ATTEMPTS ON 9 THROWAWAY SUBJECTS, AND BOTH HALVES HOLD.** 5 wrong passwords trip a **DURABLE** lock (`app_user.locked_until` non-NULL, exactly 15m00.004s out — not an in-memory counter), the correct password is refused with the same fixed `until`, the lock does NOT leak to another subject, and it self-heals on the REAL wall clock (423 at 22:52:49Z → 200 at 22:59:41Z, no DB edit). ⛔ 6 defects around it — worst: **a lock does not revoke a live session** (a locked subject's held token posted a write, 201), and there is NO unlock affordance anywhere; §7.1 |

### Printing and output

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 40 | Printed documents (PDF) | `Ctrl+P` on each of the 7 document classes | a real PDF (`%PDF`…`%%EOF`) **naming itself** — drive more than one class | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 7 defects (4 high); §7.1 |
| 42 | Printed statements (PDF) | `Ctrl+P` on each report | as above, per report | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 5 defects (2 high); §7.1 |
| 43 | Export a report (`Alt+E`) | `Alt+E` on several reports | ⛔ **WIRE precision, not display strings** — this row lost paisa once. CSV-injection guarded | ⛔ **2026-08-07 (3rd) — DRIVEN, LIED.** 2 defects (0 high); §7.1 |

---

## 4. ⛔⛔ THE LEADS — START HERE, THESE ARE PREDICTED DEFECTS WITH THE CODE PATH ALREADY TRACED

Read-only recon (2026-08-06) traced these to specific lines. **None is verified.** Each is a
concrete, falsifiable prediction — confirm or kill it, and log which.

> ## ⛔ STATUS AFTER THE 2026-08-07 PASS — READ THIS BEFORE RE-DERIVING ANY LEAD BELOW
>
> | Lead | Verdict | State |
> |---|---|---|
> | 1 — step-up wipes the screen | ⛔⛔ **CONFIRMED IN A BROWSER, WITH A CONTROL** | ✅ **FIXED on BOTH screens — and it was TWO defects, not one.** The traced `shownForRef` half was real, fixed and mutation-proved, and the screen was **still blank**; the live cause was the raise discarding its own result on a generation the load effect had moved. See §7's box |
> | 2 — Stock Journal destroys stock VALUE on a blank Rate | ⛔ **CONFIRMED, exactly as predicted** | ✅ **FIXED** both sides + mutation-proved. 8,890 off the Balance Sheet, quantity untouched |
> | 3 — Physical Stock counts against the WRONG position | ⛔ **CONFIRMED, exactly as predicted** | ✅ **FIXED** (default godown) + mutation-proved. A stock-take invented 13 units |
> | 4 — the Edit Log's three chrome holes | ⛔ **CONFIRMED, all four claims** | ✅ **FIXED.** ⛔ **And its `Alt+L` footnote was the biggest find of the session — see below** |
> | 5 — blank HSN → empty GSTR-1 `hsn_sc` row | **NOT YET DRIVEN** | ⛔ still open |
> | 6 — `Alt+F5`/`Alt+F6` advertised nowhere | ⛔ **CONFIRMED by reading and in the browser** | ✅ **FIXED**, plus the guard that should have caught it (`buttons.test.ts` could only ever check the *converse*) |
>
> ### ⛔⛔ AND LEAD 4's THROWAWAY WARNING WAS THE REAL DEFECT
> *"Check `Alt+L` with a real keypress — macOS Option+L emits `¬`. Playwright will pass where a human
> fails."* It does, and it was not one key: **`Alt+G`, `Alt+C`, `Alt+E`, `Alt+D`, `Alt+Z`, `Alt+I`,
> `Alt+L`, `Alt+X` and `Alt+O` were ALL dead on macOS** — Go To, Create Master, Export, Delete,
> e-Way Bill, e-Invoice, Edit Log. `comboFromEvent` read `ev.key` alone and both call sites dropped
> `ev.code`. Fixed in `normalize.ts` + both call sites, with `normalize.test.ts` (new — there was no
> test for `comboFromEvent` at all) pinning the composed character for every bound letter.

### LEAD 1 — the step-up wipes the screen (⛔ CONFIRMED ON ONE SCREEN, UNFIXED ON ANOTHER)

`App.tsx`'s `resolvedClient` memo depends on `session`; a WebAuthn step-up **replaces the session
token**, so every screen effect keyed on `client` re-runs the instant the ceremony completes.

```
POST -> 403 STEPUP -> passkey -> POST -> 422 -> the load effect's reset WIPED the refusal
```

The operator pressed the key and **saw nothing happen**. Fixed on `EInvoiceScreen`;
⛔ **`EWayBillScreen.tsx` has the identical shape and is NOT fixed** — same unconditional reset, same
`client` dep. On the e-way bill the loss is a `bill`; on the e-invoice it was the **ack number**,
which the schema stores nowhere. **Reproduces on the FIRST write of a session — the only one most
operators ever do.** Check every screen that shows a result after a write.

### ⛔ LEAD 1b — `MembersScreen` IS THE THIRD SCREEN OF THAT SHAPE, TRACED AND NOT DRIVEN

Verified by READING on 2026-08-07, immediately after LEAD 1 was fixed on both e-* screens, because
§1.3's rule says the class is the unit of repair and not the call site:

- `MembersScreen.tsx:183-208` — `refresh` is `useCallback(..., [client, companyID])` and opens with an
  unconditional `setError(null)`;
- `:211-213` — a bare `useEffect(() => { void refresh(); }, [refresh])`, so a **new `client` identity
  re-runs it**;
- `:240-254` — the WRITE path puts its refusal into **the same `error`** via `setError(explain(e))`.

So a write that carries a step-up can have its refusal cleared by the reload the step-up itself
triggers — the `shownForRef` half of LEAD 1, on a third screen.

⚠️ **IT IS NOT THE WHOLE SHAPE, AND THE DIFFERENCE MATTERS FOR WHOEVER PICKS IT UP.** `MembersScreen`
has **no generation ref at all**, so the half that was actually killing the e-* screens — a raise
discarding its own result — cannot occur here. The failure mode, if it reproduces, is the *first*
one: the refusal is set and then wiped.

⛔ **DELIBERATELY NOT FIXED, AND THE REASON IS THIS SESSION'S OWN LESSON.** LEAD 1 was traced to a
specific line, fixed there, unit-tested, mutation-proved — **and the browser was still broken,
because the traced cause was not the live one.** Patching this screen from a code reading would
repeat exactly that mistake. **Drive it first:** fresh session → Gateway → *Users & Roles* → attempt a
level change the PDP refuses → does the red banner appear on the FIRST attempt? Repeat in the same
session as the control (no step-up, so no client swap). The clean screens listed in
`PARITY-RECON-2026-08-07.md` row 16 are the negative control — they show the refusal on the first
press.

### LEAD 2 — Stock Journal destroys stock VALUE on a blank Rate

`isKeyedStockLine` needs only `qty > 0`, so a row with an empty Rate is "keyed". `parseFixed4("")`
returns `0n`, so the wire carries `rate: "0.0000"` — a **stated** zero. Server-side
`rateUnstated := l.Rate == nil`, so the `RateUnstated` backstop (whose own comment warns about
exactly this) **does not fire**. The DB CHECK is `rate >= 0`.

**Predicted:** company-wide closing stock **value** drops by the full average cost of the transferred
goods, **quantity unchanged** — so a quantity-only audit passes. ⛔ **Closing stock feeds the Balance
Sheet.** Watch the wire for `rate: "0.0000"` on the IN leg, then re-read Closing Stock **value**.

### LEAD 3 — Physical Stock counts against the WRONG position

The Godown select defaults to `— not tracked —` (NULL) while all seeded stock sits at Main Location.
Counting "13" without touching it differences 13 against a book of **0** on the NULL position →
a **surplus Inward of 13**, inflating closing stock qty *and* value, leaving Main Location untouched.

⚠️ **The symptom is deceptive:** quantity rises by exactly what was counted, which reads as "it
worked."

### LEAD 4 — the Edit Log is real, but three things around it are not

The screen genuinely renders `voucher_version` with hash-chain verification (tri-state, honest).
But: `screenTitle` has **no `edit-log` case** → the chrome reads **"Tally"**; `hintsFor` has none
either → it advertises *"Enter: Open"* on a screen with nothing to open; and the `voucher` hints
**never mention `Alt+L`**, on the only screen where `Alt+L` exists — so it is undiscoverable.
`"edit-log"` is also **absent from `COMPANY_SCOPED_SCREENS`** (latent, not currently reachable).

⚠️ **And check `Alt+L` with a real keypress:** `comboFromEvent` keys off `ev.key`, and macOS
Option+L natively emits `¬` → `Alt+¬`, missing the binding. **Playwright will pass where a human
fails.** Same doubt applies to `Alt+F5`/`Alt+F6` and the F-keys (media keys).

### ⚠️ LEAD 2's RESIDUE — WHAT THE FIX DELIBERATELY DID **NOT** CLOSE

The refusal stops a *blank* Rate destroying value. It does **not** make a transfer value-neutral,
which is what the screen's own caption promises ("no value moves"). An operator who types **any**
wrong rate still moves value: transferring 10 units carried at 889 but keyed at 1000 *increases*
company closing stock by 1,110, on a document that says the total holding is unchanged.

**The real fix is for the server to derive a transfer's IN rate from the OUT leg's consumption cost**,
so the promise is structural rather than a matter of the operator's arithmetic. That was **not** done
here, deliberately and on this file's own rules: it changes the **movement primitive**, which §5
requires be serialised with T9 FIFO/LIFO, and it **MOVES THE BALANCE SHEET**, so it needs a deliberate
`verify:demo` re-pin. ⛔ **Do it as part of T9, not as a patch.**

### LEAD 5 — a blank HSN silently produces an empty GSTR-1 `hsn_sc` row

Found by accident 2026-08-07. An accounting-mode GST voucher keyed with HSN blank yields an
`hsn_sc` row with an **empty HSN code**, on a mandatory table. Nothing on the form requires it and
nothing refuses it. The item path is immune (HSN comes off the stock master).

### LEAD 6 — discoverability holes that make working features invisible

`VOUCHER_BAR` omits **`Alt+F5`/`Alt+F6`**, and `hintsFor`'s `voucher` case does not name them either.
Both keys work. By this repo's own stated rule — *"an operator reads the hint strip as the list of
what works here"* — they are unreachable in practice. `buttons.test.ts` is the guard that should
have caught it.

---

## 5. PHASE C — BUILDING THE 13, IN ORDER

Ordered by **how much of a real business's day is blocked**, which is this repo's standing criterion.

| # | Row | Size | The one thing not to get wrong |
|---|---|---|---|
| 1 | **Backup / restore / split company** (46) | 1–2 sessions | Must be **tenant-exact**. ⛔ Reuse `testdb`'s delete-plan census — it already knows every tenant table and is anti-rot by construction |
| 2 | **FIFO / LIFO / batch valuation** (25) | 1 session | ⛔ **SERIALISE with #3** — both touch the movement primitive. Closing stock feeds the **Balance Sheet**, so this MOVES THE SHEET: re-pin `verify:demo` deliberately and explain the change. A new arm in `ValuerFor` + fixtures, **not** a schema change |
| 3 | **Stock groups + compound units** (28) | 1 session | After #2, never beside it |
| 4 | **Multi-currency** (10) | 1–2 sessions | Rate at transaction date vs reporting date; realised/unrealised difference is the trap |
| 5 | **Budgets** (13) | 1 session | ⛔ **Must follow T2's AXIS model** (§8.1): a budget is a comparison dimension, not a posting |
| 6 | **Interest on overdue bills** (12) | 1 session | Rides bill-wise ageing, which already works |
| 7 | **Per-company print templates** (45) | 1 session | Pure output; low risk |
| 8 | **Export: e-mail + PDF** (44) | ~½ session | ⛔ Sending e-mail is **outward-facing** — needs an explicit decision before anything is sent |
| 9 | **GSTR-9 annual return** (19) | 1–2 sessions | Aggregates GSTR-1/3B; the reconciliation disclosures are the hard part |
| 10 | **SSO / OIDC** (39) | 1 session | Last, deliberately. Must not weaken step-up |
| — | **Rule 48(4) QR code** (41) | ⛔ **NEVER** | **PERMANENTLY UNSHIPPABLE.** The payload is signed with the Income Tax Department's private key. ⛔ **If any agent reports this done, REJECT IT** |

⛔ **EVERY ONE OF THESE NEEDS THE 15 REGISTRATIONS (§7.2) IF IT CARRIES A SCREEN**, including
`corpora/validate.py`'s `KNOWN_SCREENS` (outside `frontend/` entirely) and `printTarget.ts`'s
`REPORT_SLUGS`. ⚠️ **`make verify` does not run `tsc`** — they are separate gates.

⚠️ **A `report:NAME` frame is much cheaper than a `ScreenKind`** and is the right choice more often
than it looks: it inherits the report keymap, button bar, title, F2 and a place in Go To **for free**.
Pick a `ScreenKind` only for a screen carrying a create FORM. **Decide the resource SHAPE first** — it
decides whether you spend a migration number at all (§6.1).

---

## 6. PHASE D — THE FOLLOWING SESSION (the user's stated plan, recorded so it is not re-derived)

| Row | State | Note |
|---|---|---|
| **Payroll statutory** (18) | `server` | **The biggest module currently invisible.** Engine, statutory rates and file emitters all built, and `internal/payroll`/`payrolldb` are **already** in `internal/server`'s archtest allow-set — **no refactor, pure frontend**. Employees, pay heads, pay run, payslips. Budget **2 sessions** |
| **Offline entry with sync** (47) | `server` | Built. Needs a screen **plus a design decision**: how does a browser present queued intents? |
| Backup / restore / split (46) | `off` | Listed in §5 as #1 — pull it here instead if that ordering suits |

---

## 7. ⛔ THE LOG — FILL THIS IN AS YOU GO, ONE LINE PER ROW

**An audit whose findings live in one session's memory is an audit that will be run again from
scratch.** Append; do not start a new table. Fold into `HANDOFF.md` §5.5 at the end.

| Date | Row # | Keystrokes driven | Oracle used | Result |
|---|---|---|---|---|
| 2026-08-06 | 15 | `Alt+I` → pick → `Ctrl+A` | `voucher_external_reference` + re-post + ALTER/DELETE | ✅ IRN minted from a browser; re-post same IRN; both 409 |
| 2026-08-07 | 4 | `Alt+F6` Credit Note → `Ctrl+A` | `posting` sides + ledger names | ✅ after fix — `Cr party / Dr Sales / Dr Output tax` |
| 2026-08-07 | 1 (F9) | `F9` Purchase, accounting mode | `posting` sides + ledger names | ✅ after fix — `Cr supplier / Dr Purchases / Dr INPUT tax` |
| 2026-08-07 | 26 (`Alt+F7`) | Stock Journal, item + From godown + qty, **Rate left blank** → `Ctrl+A` | `stock-summary` **`closing_value`** + Balance Sheet Closing Stock | ⛔ **LIED — LEAD 2 CONFIRMED.** Saved 201. Closing stock **72837.1541 → 63947.1541**, a loss of **8890.0000** = 10 × the 889.0000 average. **Quantity unchanged** (75 = 10 + 65). Balance Sheet Closing Stock fell by the same and still reported `balanced: true`. ✅ **FIXED** (server refusal + client refusal); re-driven and refused at the keyboard |
| 2026-08-07 | 26 (`Alt+F10`) | Physical Stock, item + count 13 + rate, **Godown untouched** → `Ctrl+A` | `stock-summary` per-**position** qty and value | ⛔ **LIED — LEAD 3 CONFIRMED.** The count landed on the NULL position as a **surplus INWARD of 13**; Main Location kept its 195 untouched; item total **195 → 208**, value +410.8104. A stock-take INVENTED stock. ✅ **FIXED** (the grid now defaults to the seeded system godown); re-driven — Main Location corrected 195 → 13 |
| 2026-08-07 | 2 | Day Book → `Enter` → `Alt+L` | the screen, the chrome, and a **real macOS keydown** | ⛔ **LIED — LEAD 4 CONFIRMED, ALL FOUR PARTS.** The list itself is real, but the title read **"Tally"**, the strip advertised **"Enter: Open"** on a screen with nothing to open, no surface named `Alt+L`, and `edit-log` was absent from `COMPANY_SCOPED_SCREENS`. ✅ **ALL FIXED** — title now "Edit Log", strip now "Esc: Back / F1: Help", `Alt+L: Edit Log` on the voucher strip |
| 2026-08-07 | — (all `Alt`+letter) | `Alt+G` `Alt+C` `Alt+E` `Alt+D` `Alt+Z` `Alt+I` `Alt+L`, sent as **macOS really sends them** | the screen — did the action fire at all | ⛔⛔ **THE BIGGEST FIND OF THE SESSION.** **Every one was DEAD on macOS.** Option composes (`Alt+L`→`¬`, `Alt+G`→`©`, `Alt+C`→`ç`), `comboFromEvent` read `ev.key` alone, and both call sites dropped `ev.code`. ⚠️ **Playwright sends `key:"l"`, which a Mac never sends — so every driven test passed on keys no operator here could press.** ✅ **FIXED** and re-driven: `©`→Go To, `ç`→Create ledger |
| 2026-08-07 | 40/43-adjacent | direct `curl` POST with a valid dev token | HTTP status | ✅ step-up held: `403 STEPUP_REQUIRED` on a write from outside the browser (row 36 evidence) |
| 2026-08-07 | **1** (all six) | `F4` `F5` `F6` `F7` `F8` `F9`, each keyed → `Ctrl+A` | `posting` sides + ledger NAMES, per class | ✅ Contra `Dr HDFC/Cr Cash`; Payment `Dr Rent/Cr HDFC`; Receipt `Dr HDFC/Cr Acme` (the row that once posted `DR <the customer>`); Journal `Dr Electricity/Cr Cash`; Sales & Purchase below. **Six documents, not one** |
| 2026-08-07 | **4** (Debit Note) | `Alt+F5` → key → `Ctrl+A` | `posting` sides + ledger names | ✅ **FIRST EVER DRIVE.** `Dr Steel Suppliers 11800 / Cr Purchases 10000 / Cr Input CGST 900 / Cr Input SGST 900` — input credit correctly REVERSED. Row 4 now complete |
| 2026-08-07 | **6** | `F4`/`F5`/`F6` single-entry (the default) | `posting`: one primary leg + N counter-legs | ✅ correct sides on all three; the grid also REFUSED a same-side Journal by name |
| 2026-08-07 | **7** | back-dated `F8` into FY 2025-26 → `Ctrl+A` | `voucher_number_counter` per (class, FY) | ✅ **AND IT WAS UNMEASURABLE BEFORE TODAY** — see the Date defect below. `Sales\|2025\|2` beside `Sales\|2026\|7`: the series genuinely RESTARTS next FY |
| 2026-08-07 | **14** | `F8` intra (KA→KA) **and** inter (KA→MH), `F9` | `posting` + GSTR-1 `b2b` + table-12 `hsn` | ✅ intra → CGST+SGST 1800/1800; inter → IGST 5400; purchase → **Input** tax. b2b carries both. ⛔ table 12 was blank — LEAD 5, fixed below |
| 2026-08-07 | **21** | `F8` → `Ctrl+H` → Item Invoice → key → `Ctrl+A`, then **drill + re-accept** | `stock_movement` qty AND value + `posting` + version diff | ⛔⛔ **LIED, TWICE.** Create was refused outright (godown default); after fixing, the **ALTER SILENTLY DESTROYED THE MOVEMENT** — v1 `1 movement`, v2 `0 movements`, stock 190→**195**, the sold units came BACK while the customer stayed billed. ✅ **BOTH FIXED**, re-driven: v2 keeps its movement |
| 2026-08-07 | **22** | `F8` item invoice with the Godown left at its default | server refusal / `stock_movement` position | ⛔ **LIED — the godown-default bug had an UNFIXED TWIN in the item grid.** `negative stock refused … (godown -, batch -): closing balance would be -5.0000` on a company holding 195 at Main Location — **an operator could not sell stock they own.** ✅ FIXED (helper moved to shared module, both grids use it) |
| 2026-08-07 (2nd pass) | **20** | `Alt+C` from the Gateway **and** on the item-invoice Item cell, sent as macOS really sends it; plus the whole `Alt+G` list and the whole Gateway menu | the pushed frame's `<h1>` | ⛔ **LIED — RECON CONFIRMED EXACTLY, ON BOTH PATHS.** Both press-points render **`Create ledger`**. The Gateway menu has no masters entry and `Alt+G` offers only Vouchers + 10 report names. **A stock item or a unit can NEVER be created from the keyboard**, and the item-invoice empty state tells the operator to use `Alt+C from Gateway > Masters` — **a route that does not exist**. `PARENT_OPTIONS["stock-item"]` is `[]`, so even the dead form hard-codes `unit_symbol: "Nos"`. ⛔ **NOT FIXED — see the box below** |
| 2026-08-07 (2nd pass) | **8** | Gateway → `Cost Centre Summary` → axis `Department`; then `Ctrl+F`, `F12` | the screen | ✅ **RECON'S PREDICTION 1 CONFIRMED, PREDICTION 2 KILLED, PREDICTION 3 CONFIRMED BUT NOT A DEFECT.** Neither cost report is in `Alt+G` (it lists Vouchers + 10 report names) — but **both are on the Gateway menu and open correctly from there**. Opened from the Gateway the summary renders `cost-centre-summary-period-prompt` and no table — **and it says exactly why, on its face** ("BOTH dates are needed and neither is defaulted… a defaulted opening date would answer with everything since the company was formed while looking exactly like the year you asked for"). That is a stated design decision, not a lie. `Ctrl+F` and `F12` **both opened an overlay** — prediction 2 is dead |
| 2026-08-07 (2nd pass) | **11** | Gateway → `Bank Reconciliation`; read the header with no `F2`; count `brs-bankdate:1:0`; `F2` To=2026-06-30 → tick the 2026-06-05 leg with bank date **2026-07-10**; then widen to 2026-08-31; then tick the 2026-05-30 leg with **2026-05-01** | `bank_reconciliation` joined to `posting` + `ledger.name`, plus the screen | ⛔⛔ **LIED, FOUR WAYS — every recon prediction on this row confirmed.** (a) The tick with the LATER bank date returned **200** and wrote `cleared_amount 20000.0000 / C / by ui` — and **the screen was byte-identical**: `Un-cleared (5)`, `per-bank 2,50,000.00`, and the cell still displaying 2026-07-10 on an un-cleared row. Widening `F2` to 2026-08-31 showed it under `Cleared (1)` — proof it was stored all along. (b) `brs-bankdate:1:0` matched **4 elements**: HDFC Bank carries a **Contra 1, a Journal 1, a Receipt 1 and a Payment 1** and the table had **no class column**. (c) With no `F2` the header read **"As at"** with no date while Age showed 127d/79d/69d against today — §1.1's order-book defect, third occurrence. (d) A refused tick (400, *"a cheque cannot clear before it is written"*) left **2026-05-01 in the cell** with nothing stored. ✅ **ALL FOUR FIXED, mutation-proved (5 mutations, 5 predicted failures), and re-driven 7/7 in the browser** |

| 2026-08-07 (2nd pass) | **16** / LEAD 1 | FRESH session → `Alt+Z` → pick a document → **distance left BLANK** → `Ctrl+A`; then `Ctrl+A` a SECOND time | the wire (devtools) beside the screen | ⛔⛔ **LEAD 1 CONFIRMED, WITH THE CONTROL.** First press: `403 STEPUP` → ceremony → retry **422** — and **no `role="alert"` element existed at all**. Second press: the **identical** request, the **identical** 422, and the refusal rendered in full and stayed. Same request, same answer, different outcome — the client swap is the cause. ✅ **FIXED on BOTH `EWayBillScreen` AND `EInvoiceScreen`**, mutation-proved, re-driven — both now show the refusal on the FIRST press |
| 2026-08-07 (3rd) | **3** | NEW x2 ON ONE INVOICE: Esc*->Gateway, F8, Enter(date->party), click party combobox, type "Acme Traders", Enter, triple-click gst-taxable type 10000, triple-click gst-rate type 18, triple-click gst-hsn type 7308, triple-click billr … | (1) psql as postgres on tally@localhost:5433 scoped to company_id=763359dd-b59c-49ba-9047-c16423ecc45d: bill_allocation joined to voucher_version/voucher_type/ledger/vouc … | **⛔ DEFECT** — **5 defects (5 high)**. **DEFECT — 4 recon predictions: 3 CONFIRMED, 1 CONFIRMED-with-a-kill.** (a) two NEW refs collapse to ONE bill '4' 11800.0000 due 2026-09-06, second instalment's ageing gone; (b) ON_ACCOUNT and ADVANCE both stamped with the bare voucher number and net unrelated Purchase invoices — bill '3' shown IN THE PRODUCT at 6000.0000, and ADVANCE also overwrites invoice '4' due date to 2026-12-01; (c) as_on used for ageing not inclusion — os-total 2,65,720.00 vs Balance Sheet Sundry Debtors 1,79,000.00 at 2026-04-30 under an "As on 2026-04-30" caption; (d) payable side + per-bill breakdown unreachable on EVERY door (Go To offers no "Bills"/"Payable", Gateway and Display More Reports have no entry) — KILLS the "Bills Payable alias lands on receivables" reading: n … |
| 2026-08-07 (3rd) | **5** | THE ALT+C PATH: Esc*->Gateway, F8, Enter (date->party-ledger), altMac("c") -> master-create frame pushed; enumerated every input it renders; click Name, type "New Debtor Co", Enter (name->group), click the Under (group) combobox, … | (1) The rendered DOM of the Alt+C frame — every input/select/textarea enumerated by tagName + aria-label + value. (2) psql as postgres: `SELECT l.name, g.name, l.opening_ … | **⛔ DEFECT** — **3 defects (2 high)**. **DEFECT — headline case NOT DRIVABLE, established by trying every door; C-leg driven and broken.** The Alt+C frame renders EXACTLY two inputs ("INPUT[text] aria=Name", "INPUT[text] aria=Under (group)") — no Opening Balance, no Dr/Cr, no breakup, no bill-by-bill; the MASTER_FIELDS "details" cursor has no control; POST /ledgers 201 sends name+group only; psql: New Debtor Co / Sundry Debtors / 0.0000 / D / f / 0 opening bills; and a Sales to it renders NO bill-refs-panel while the same keys on Acme Traders do. No other door exists: no Gateway create/alter-master, no screens/masters ledger-alter, setLedgerOpening called by nothing. C-leg: at as-on 2026-03-31 (books begin 2026-04-01) Outstandings shows 2,65,720.00 receivable incl. OB/ACME/24-25/07 + /11 … |
| 2026-08-07 (3rd) | **9** | SEED: pnpm seed:demo --insightd -> company df06048c-d6e9-4304-824e-d5c42b873cad (chrome asserted "Demo Traders"). (A) HAPPY PATH, SINGLE ENTRY: Esc*n to Gateway -> F5 -> type "HDFC Bank" in the Account combobox, Enter -> single-le … | psql -h localhost -p 5433 -U postgres -d tally (superuser). (1) SIDES + NAMES + VALUE per (posting, CATEGORY): select vt.name vtype, vv.voucher_no, vv.version_no, l.name … | **⛔ DEFECT** — **5 defects (2 high)**. **DEFECT (5)** — the two-axis allocation ITSELF IS CLEAN on both grids: 4 rows on ledger `Rent` D, Branch 700+300=1000 and Department 600+400=1000, independent, survives a no-op alter as v2 (still 4 rows), and the report reads back Department 62,600.00 / Branch 49,000.00 with 111,600 nowhere. But: (1) HIGH the picker is MOUSE-ONLY — Tab/Shift+Tab/Enter move nothing on the particulars grid though the hint strip advertises them, and a swallowed Tab appended the amount into the ledger name (`Rent1000`) [useNavMachine.ts:54]; (2) HIGH a partial axis creates the named ledger BEFORE the 422 — orphans `Marketing Spend Q3` and `Warehouse Rent Q3` confirmed in the DB with 0 postings, on both grids [VoucherScreen.tsx:1057, :1158] (prediction 1 CONFIRMED); (3) … |
| 2026-08-07 (3rd) | **17** | DOOR CHECK: Esc*n to Gateway; altMac("g") -> Go To; type "TDS" -> only "TDS / TCS Register" listed, NO "TDS Challans"; Esc; openFromGateway("TDS Challans") -> opens. PREREQ (keyboard): Esc to Gateway; F11 -> Features; click "Tax y … | (1) psql as postgres, the recon's own query: select c.challan_no, c.tax_rupees, coalesce(sum(a.allocated_amount_rupees),0) allocated, c.tax_rupees - coalesce(sum(a.alloca … | **⛔ DEFECT** — **6 defects (4 high)**. DEFECT — 6 findings. Create-time allocation WORKS (05130 32000→unallocated 12000.0000, 05132 10000→0.0000, `who`=Pune Fabricators/194J-P/leg0). The row's OWN CLAIM IS FALSE: no allocate-later route exists (7 browser doors + PATCH/PUT/DELETE all refused), 05130 stayed 12000.0000. A 1-rupee challan was allowed to "pay" 27000.0000 across TWO deductees → unallocated -26999.0000, screen captions it normal; books now claim 57000.0000 deducted discharged by 42001.0000 deposited. A TDS deduction CANNOT BE KEYED AT ALL (VoucherRequest has no tds field) and TCS has no challan mechanism (tcs_collection absent from the picker). Recon predictions 1, 2, 3 CONFIRMED; its "PATCH returns 405" proof KILLED (403 "unmapped route: access denied"). |
| 2026-08-07 (3rd) | **24 (negative stock, and the FLAG must go …** | SEED: pnpm seed:demo --insightd -> company f2f91e35-e2f7-47b2-8cf1-935f5cdb0195 ("Demo Traders"). Sign in browser-dev-owner / tally-demo-2026, virtual WebAuthn installed before first nav. (1) REFUSAL, Sales item invoice: Esc*n to … | psql (postgres superuser, port 5433) on my own company only: - per-position closing: stock_opening + SUM(CASE direction WHEN 'IN' THEN qty ELSE -qty END) over stock_movem … | **⛔ DEFECT** — **3 defects (1 high)**. **DEFECT (3)** — the REFUSAL is clean and is a class: 422 on Sales/Delivery Note/Stock Journal/PUT-alter, always "negative stock refused for item <name> (godown Main Location, batch -): closing balance would be -1.0000", nothing partially written (voucher_head 22 -> 24 = exactly the 2 accepted vouchers); the own-voucher exclusion is real (alter 35->4 = 200, alter 4->40 = 422 "-6.0000") and a Physical Stock shortage commits to exactly the count (delta OUT 72, position 3.0000); a bare-psql insert bypassing Go was refused at COMMIT by `check_stock_not_negative` with identical wording, so "structural" is true. **BUT** `allow_negative_stock` has NO writer — F11 offers no toggle (0 checkboxes/switches, net delta []) while its note claims "A company can opt … |
| 2026-08-07 (3rd) | **27** | login (browser-dev-owner / tally-demo-2026, virtual WebAuthn) -> Esc*n to Gateway -> Ctrl+F8 (Sales Orders) -> Tab-to "Select order SO-2026-001" + Enter -> Ctrl+F9 (Purchase Orders) -> Tab-to "Select order PO-2026-001" + Enter -> … | (1) stock_movement/posting/voucher_head/voucher_version md5 fingerprint + row counts scoped to company_id, taken BEFORE and AFTER every order/BOM write: PGPASSWORD=postgr … | **⛔ DEFECT** — **7 defects (2 high)**. **DEFECT — 7 found (2 high, 1 medium, 4 low).** The invariant HOLDS (an order writes no stock_movement, no posting, no voucher_head; stock_movement has no order column; Alt+X issues no non-GET) and Ordered/Delivered/Outstanding RECONCILE through a partial delivery (30→35 delivered, 20→15 outstanding, and the as-at bound is applied to the delivery: 30/20 at 2026-09-19 vs 35/15 at 2026-09-30). All 4 recon predictions CONFIRMED, 0 killed: party unrestricted to the whole 27-ledger chart of accounts (SALES→HDFC Bank/Bank Accounts 201, and the untested mirror PURCHASE→Acme Traders/Sundry Debtors 201) [high]; fulfilment picker offers SO-10 (2026-09-01) before SO-2 (2026-04-01) while its comment claims oldest-first [high]; Alt+X note reads "3 of SS Sheet 2mm … |
| 2026-08-07 (3rd) | **29 (+ the as_on echo lead on rows 23/29)** | SIGN IN: type "browser-dev-owner", Tab, type "tally-demo-2026", Enter (virtual WebAuthn authenticator installed pre-navigation). BALANCE SHEET AT TWO DATES: Gateway combobox -> type "Balance Sheet" -> Enter; F2 -> fill period-from … | (1) The three report surfaces read off the SCREEN by data-testid (bs-assets-total, bs-liabilities-total, stock-total-value) plus the full body text; (2) the WIRE for ever … | **⛔ DEFECT** — **5 defects (2 high)**. **DEFECT ×5.** Row itself passes on the identity: Closing Stock MOVES 92,648.1818→72,837.1541, sheet balances at both, Stock Summary and P&L agree to the paisa, matches the hand fold. But: [P1 CONFIRMED] Closing Stock/Stock-in-Hand INVISIBLE on the condensed BS and ABSENT from the Alt+E CSV; [P2 CONFIRMED to the paisa] lexical voucher_no replay costed an 8-unit issue at 31.6008 instead of 106.7843 → Closing Stock 25,909.3478 vs 25,307.8797, qty 237.0000 both ways, "balanced":true; [P4 CONFIRMED] BS net profit 1,16,237.15 vs P&L -37,911.02 with F2 From set, BS still captioned "(Current Period)"; [NEW] Stock Summary with no period says "As at today" over 72,837.15 when as-at-2026-08-07 is 96,168.18, and the BS with no period states NO date; [P3 NOT EST … |
| 2026-08-07 (3rd) | **30** | login(browser-dev-owner/tally-demo-2026) -> Esc*n to Gateway -> type "Bill Inbox" -> Enter -> Enter (SelectionList autoFocus) -> [F9 Purchase opens in draft mode] -> Ctrl+A. Then: click [data-testid=inbox-fixture], paste corpus JS … | psql -h localhost -p 5433 -U postgres -d tally: posting JOIN ledger JOIN voucher_version JOIN voucher_head (sides + ledger NAMES + amounts); bill_allocation JOIN ledger; … | **⛔ DEFECT** — **8 defects (6 high)**. **DEFECT x7** — the seeded draft DOES accept correctly (Input IGST D 3240.0000, Pune Fabricators C 21240.0000, Raw Material Purchases D 18000.0000, no junk master), but: a 2nd Ctrl+A posts a DUPLICATE voucher (payable 42480.0000, ITC 6480.0000, draft stays "ready", supplier_invoice_no left NULL so the existing unique index cannot fire); the panel's "referenced in full (21240.0000)" writes ZERO bill_allocation rows; a raw PAN lands in voucher_version.voucher_no and a raw Aadhaar becomes a permanent ledger master while the badge says "PII redacted: 4"; screen-created drafts have no input-tax legs (1000 D vs 1180 C) and derive IGST on a 27->27 intra-state bill; a failed Ctrl+A still creates "Sundry Creditor"; insightd needs no token and leaks another te … |
| 2026-08-07 (3rd) | **31** | DOOR 1: Esc*n to Gateway -> Alt+G via altMac("g") -> type "Query" (Go To picker). DOOR 2: Esc*n to Gateway -> type "Query" -> Enter -> Enter. Then on the Query screen: click/Tab to [data-testid=query-input] -> type the question -> … | Three ways on the same figure: (a) the on-screen answer table [data-testid=query-table]; (b) the REPORT — GET /v1/companies/<CO>/outstandings?kind=receivable/payable; (c) … | **⛔ DEFECT** — **4 defects (2 high)**. **DEFECT x4, 2 checked negatives** — the answer does NOT match the report: "receivables" 133920.0000 vs report 253920.0000 (short 120000.0000) and "payables" 36050.0000 vs 104050.0000 (short 68000.0000), because whitelist.go:312-341 reads bill_allocation and never unions ledger_opening_bill. "sales last month" and "sales last quarter" answer the CURRENT period (DSL windows 2026-08-01..08-31 and 2026-07-01..09-30) — "last quarter" said "No rows." for a quarter holding 156500.0000. Cross-tenant probes by NAME, by UUID, "every company" and an ignore-your-instructions prompt all returned 200 with the caller's own rows and NO refusal and no scope disclosure. CHECKED NEGATIVES: enforcement is real (relforcerowsecurity=t on all nine tables; cross-company PO … |
| 2026-08-07 (3rd) | **32** | DOOR 1 (Alt+G Go To, via altMac("g")): typed "Cash Flow", "Cash-flow", "Forecast", "Anomal", "Projection" — all five absent. DOOR 2 (Gateway menu): Esc*n to Gateway, read all 18 entries; then typed "Cash Flow" -> Enter and "Anomal … | The screen itself for reachability (body text of the Gateway, the Go To picker, Display More Reports, F11 Features). Then, since no renderer exists, the wire: GET /v1/com … | **⛔ NOT_DRIVABLE** — **4 defects (2 high)**. default). Mutation proved the invented date: three bills with NULL due_date dated 2026-08-07 (11800.0000 each) landed as 35400.0000 of outflow in the bucket 2026-09-04..2026-09-11 (= voucher_date + 30) with nothing in the body saying so. CONSERVATION HOLDS: bucket-1 inflow 253920.0000 and outflow 104050.0000 both equal the Outstandings report totals (the forecast correctly includes ledger_opening_bill, which the ROW 31 NL query does not). insightd's statistical half returns floats (1000.4) but DOES set estimate:true and method — the disclosure cored lacks. All three recon predictions CONFIRMED. |
| 2026-08-07 (3rd) | **33** | Gateway -> openFromGateway('Users & Roles') -> Enter (the screen IS on the Gateway menu, 16th entry; confirmed reachable). Invite lifecycle, all pointer-driven as the recon said (no keystroke binding exists for any action on this … | SIDES/NAMES/STATES, never a total. (a) screen: [data-testid=invites-list] innerText verbatim, [data-testid=invite-delivery:<subject>] innerText verbatim, the members <tab … | **⛔ DEFECT** — **3 defects (3 high)**. ROW 33 DRIVEN end to end in the browser and DEFECTIVE in three ways. The mechanics work: the invite is a first-write-of-session step-up that returns 201, it appears as PENDING (psql: status='pending', zero company_membership rows, no app_user row at all), revoke works and says so, and a claimed membership does appear with its level. But (1) the invite reaches nobody while the screen says 'invitation sent' -- outbox recipient has no '@', status='sent', and cored's log says NO EMAIL ON FILE verbatim for my company id (recon prediction 1 CONFIRMED); (2) sign-in never claims an invite because /v1/me is called only by F3 (recon prediction 2 PARTLY KILLED -- a claim route DOES exist for a subject who already has a password); and (3) worst, a ?company= the … |
| 2026-08-07 (3rd) | **34** | AS OWNER: Gateway -> Users & Roles -> click [data-testid=level-new], fill [data-testid=level-name]='B1 Disallow Test', [data-testid=level-default-access]='none', three rules via [data-testid=rule-effect/action/resource:N] and [dat … | (1) SERVER, the trusted one: the actual HTTP status and the actual 403 body per route, from the browser's own wire. (2) psql on acl_rule joined to security_level, to prov … | **⛔ DEFECT** — **2 defects (1 high)**. ROW 34 IS DRIVABLE AND WAS DRIVEN -- contrary to the recon's caveat -- and the core Disallow/Allow enforcement is REAL. I authored a custom level in the browser level editor, put a real password-holding second user (demo-data-entry) on it via invite + F3 claim, signed in as that user in a separate context, and was REFUSED: GET stock-summary -> 403 {"error":"disallowed by rule"} (the RULE matched, not the default deny) with a legible on-screen refusal naming the exact rule; controls Day Book and Balance Sheet both 200 with real seeded rows, and the admin surface 403s 'default access: none' legibly. Two defects: the refusal names the WRONG level (hardcoded 'Data Entry'), and -- far more serious -- mid-row every write in the product stopped working beca … |
| 2026-08-07 (3rd) | **35** | Two private tenants seeded (2x `pnpm seed:demo --insightd`), then made tenant-identifiable by name AND by figure: A = e876261c-63e6-4cb3-b89c-ad413b63a604 renamed "Demo Traders ALPHA35" with party ledgers/stock items suffixed " A3 … | psql (postgres superuser) on posting -> voucher_version -> ledger -> company for sides and ledger NAMES per company_id; plus the rendered screens read by tenant-suffixed … | **⛔ DEFECT** — **2 defects (1 high)**. **DEFECT (2).** Re-scoping itself is CLEAN: 14 screens, both directions, zero A-rows on B and zero cross-tenant requests; the open frame unwinds to the Gateway, Esc does not resurrect it, Esc cancels the picker, master pickers and insightd's Bill Inbox re-scope, and a post-switch write landed in BETA35 (Dr "Acme Traders B35" 5005.56). **HIGH:** F3 never persists (CompanySelectScreen.tsx:88-96; persistCompanyId only reachable from the one-shot bootstrap, CompanyContext.tsx:142/188/202/257) — symmetric, not a ?company= artefact: a BARE reload reverts, and the next voucher (5353) was written to ALPHA35 while BETA35 was chosen. Recon prediction 1 CONFIRMED; prediction 3 (LoadedVoucher / 404-on-drill) KILLED. **MEDIUM, found en route:** assert/begin retur … |
| 2026-08-07 (3rd) | **37** | SET THE CUTOFF: [Esc]xN to Gateway > type "Users & Roles" in the Gateway search > [Enter] > click [data-testid=level-edit:Owner] > type 2026-08-01 into [data-testid=level-cutoff] > click [data-testid=level-save]. F7 JOURNAL: [Esc] … | (a) the rendered refusal read from [data-testid=status-line] / [data-testid=stock-denied] via body().innerText; (b) the wire, every non-GET recorded by the harness (`net` … | **⛔ DEFECT** — **4 defects (3 high)**. **DEFECT** — both halves work on the 6 accounting classes (403 "voucher date is before the back-dated cutoff", 0 rows written; control posts Rent Dr 777 / Cash Cr 777 and Acme Traders Dr 1180 / Domestic Sales Cr 1000 / Output CGST+SGST Cr 90 each on 2026-08-06). 4 defects: (1) HIGH the cutoff is BYPASSED by loose date spelling when voucher_no is supplied — "2026-7-2" posted 201 and is stored as 2026-07-02 inside the lock while "2026-07-02" was refused 403 (recon prediction 2 CONFIRMED, on the second attempt); (2) HIGH the 4 stock screens misname a cutoff 403 as an ACL denial; (3) HIGH step-up permanently dead past 64 credentials (RangeError, blocks EVERY write row); (4) MED the refusal never states the cutoff date. Recon prediction 1 (no accounting v … |
| 2026-08-07 (3rd) | **38** | Real Chromium at http://localhost:5173/?company=f9a1b43f-…, LoginScreen driven by page.keyboard ONLY, in the harness's exact shape (autoFocus asserted: focusedAtStart=="User name" on every attempt; keyboard.type(subject) -> Tab -> … | Three independent oracles, no totals asserted and no "screen looked right": (a) THE WIRE — every non-GET response captured as METHOD path STATUS plus the first 300 bytes … | **⛔ DEFECT** — **6 defects (0 high)**. ROW 38 DRIVEN: the lockout core actually works — 5 keystroke-driven wrong passwords trip a DURABLE lock (locked_until non-NULL, exactly 15m00.004s out), the 5th attempt itself answers 423 (recon prediction 3 CONFIRMED), the correct password is refused with the same fixed `until`, a different subject signs into Demo Traders unaffected, a success resets the window without a false re-lock, and it self-heals on the real wall clock (423 at 22:52:49Z -> 200 at 22:59:41Z, row back to 0/NULL, no DB edit) — but 6 defects: no unlock affordance anywhere in the product (Users & Roles renders no lock state and setPassword has zero callers), the lock does NOT revoke a live session (a locked subject's held token did POST /v1/companies -> 201), N/window/duration are … |
| 2026-08-07 (3rd) | **40** | Sign in (browser-dev-owner / tally-demo-2026) on ?company=df5008ef-b198-492e-b171-8602e4b5a475. Per class from the Gateway: F8 → click party combobox, type "Acme Traders", Enter → fill voucher-date 2026-07-20 → gst-taxable 10000.5 … | (1) The captured HTTP response of GET /v1/companies/{c}/print/vouchers/{class}/{id} — status, content-type, content-disposition, and the BYTES (route-intercepted and re-f … | **⛔ DEFECT** — **7 defects (4 high)**. DEFECT — 3 of 7 documents are unobtainable (debit note always 422; credit note 422 with no reachable Agst Ref; delivery note has NO Ctrl+P handler at all), receipt+payment print the BANK as "Received From"/"Paid To", sales/bill-of-supply 400 on an HSN the form never asks for, and the Day Book drill opens the wrong voucher after the first drill. Only 2 of 7 (tax invoice, bill of supply) print correctly from the keyboard, plus a seeded credit note. |
| 2026-08-07 (3rd) | **42** | Alt+G (altMac("g")) → type the report name → Enter → F2 → fill period-from 2026-07-01 / period-to 2026-07-31 → click Apply → Ctrl+P, for: Trial Balance, Day Book, Balance Sheet, Profit & Loss A/c, Outstandings, Stock Summary, TDS … | The PDF bytes captured off the wire (%PDF-1.4 … %%EOF), read back with pdftotext -layout and with `strings` over the uncompressed streams; the SUBTITLE line and the row c … | **⛔ DEFECT** — **5 defects (2 high)**. DEFECT — 7 reports have real layouts (TB, Day Book, BS, P&L, Outstandings, Stock Summary, BRS) but Ctrl+P IGNORES THE F2 PERIOD on every one of them: printed Stock Summary 72,837.15 vs screen 96,168.18, printed P&L +1,16,237.15 vs screen −14,580.00, printed Day Book 20 rows vs screen 4. TDS/TCS Register advertises Ctrl+P and the key is dead (no request). Day Book footer clipped to "No column total is..." on a reason its own query contradicts. Stock Summary paper has 10 columns to the screen's 7. Both Order Books correctly surface the 501. |
| 2026-08-07 (3rd) | **43** | altMac("g") → report name → Enter → F2 → period-from/period-to → Apply → altMac("e"), for Trial Balance, Day Book, Balance Sheet, Profit & Loss A/c, Outstandings, Stock Summary, TDS / TCS Register, Sales Order Book, Purchase Order … | The BYTES of the downloaded file (Playwright download → saveAs → Buffer): first-3-byte hex for the BOM, CRLF presence, and every field verbatim. Compared against psql as … | **⛔ DEFECT** — **2 defects (0 high)**. DEFECT (but the paisa is SAFE) — precision and injection both CONFIRMED REAL: BOM ef bb bf, CRLF, every money cell ="…" at 4 dp (116237.1541, 72837.1541, 31.6008, 7268.1818), a leading-= ledger name wrapped and a mid-string "=SUM" correctly left alone, SpreadsheetML ss:Type="String"; the 2026-08-06 regression shape did NOT reproduce. Two real defects: the Stock Summary export omits inward VALUE (7860.0000 / 56400.0000 absent, present on the PDF) and puts a qty beside money; Ctrl+P on the Cost Centre Summary claims Alt+E "is not built here" while Alt+E writes the file. |
| 2026-08-07 (3rd) | **LEAD 1b** | FRESH openApp (sign in browser-dev-owner/tally-demo-2026 on company e7b9f978-fb95-4e5a-89ff-516d2a4f7bb1) -> Gateway -> openFromGateway('Users & Roles') -> Enter. WRITE PATH A (invite()): fill [data-testid=invite-subject] with 'sv … | Wire (timestamped, GET requests included so the load effect's re-run is visible) + a DOM query for [data-testid=members-error] count and innerText, polled every 100ms for … | **✅ CLEAN** — **1 defect (0 high)**. LEAD 1b KILLED in the browser: the traced wipe does NOT occur on MembersScreen. Drove the exact wire (403 STEPUP_REQUIRED -> ceremony -> retry 4xx -> load-effect re-run) on BOTH write paths, twice each, and the refusal rendered in full on the FIRST press -- identical to the second-press control. Cause: `refresh` clears the error SYNCHRONOUSLY on its first line and is re-invoked the instant the session swaps, which is necessarily BEFORE the retry's response can be processed (the retry is not issued until the ceremony resolves, and the React state update that queues the effect precedes it), so the refusal is always the LAST write to `error`. Held at 20x CPU throttle. Only finding: the banner has no role="alert". |

### The running count — update it, do not assume it

Re-measured **2026-08-07, end of the THIRD pass. THE AUDIT IS COMPLETE: all 34 claimed-reachable rows
have now been driven end to end in a real browser against real cored.**

| | Rows |
|---|---|
| Claimed reachable (`on` + `always`) | **34** |
| **Driven end to end** | ⛔ **34 of 34 — the audit is DONE.** 16 in passes 1–2; the remaining **18** in this pass (3, 5, 9, 17, 24, 27, 29, 30, 31, 32, 33, 34, 35, 37, 38, 40, 42, 43) |
| **Came back CLEAN this pass** | ⛔⛔ **0 of 18. NOT ONE.** |
| **Found LYING this pass** | **17** — every row except 32 |
| **NOT DRIVABLE AT ALL** | **1** — row **32** (cash-flow projection / anomalies): four doors tried, no keystroke reaches it and the mandated disclosure has no renderer |
| **Predicted defects KILLED** | **1** — **LEAD 1b** on `MembersScreen`, driven with a control AND at 20× CPU throttle. It was deliberately left unpatched for exactly this reason, and that call is now vindicated |
| **DEFECTS FOUND** | ⛔ **78** (after merging duplicate reports of one shared defect) — **39 high, 28 medium, 11 low.** Register in §7.1 |
| **FIXED this pass** | **9 fully + 3 partial**, every one carrying a unit test and an EXECUTED mutation proof whose failure was checked against the prediction. §7.1 marks them |
| **STILL OPEN** | ⛔ **66.** This is now a BACKLOG, not an unknown — every one is traced to file:line with a proposed fix |
| **TRUE reachable number** | ⛔ **The honest answer is that "reachable" was the wrong question.** All 34 rows do OPEN from the keyboard — **but only 5 of the 34 have ever been driven and found clean**, all five in earlier passes. **29 of 34 carry at least one defect an operator would hit** (28 defective + row 32, which opens nowhere at all) |

> ### ⛔⛔ THE SECOND PASS'S BIGGEST METHOD LESSON: A GREEN UNIT SUITE CERTIFIED A SCREEN THAT WAS STILL BLANK
>
> LEAD 1 took **two** fixes, and the first one was wrong in the most instructive way.
>
> The traced diagnosis (and `EInvoiceScreen`'s existing fix) said: the load effect's **unconditional
> reset** wipes the refusal after the client swap. That is real, it was fixed with the `shownForRef`
> guard, its regression test **passed**, and the mutation proof **failed exactly as predicted**.
> ⛔ **And the browser was still blank.**
>
> The live cause was the *other* half of the same mechanism: `generationRef` is bumped by the load
> effect AND by its cleanup, so a client swap bumps it twice **while the retry is still in flight** —
> and the raise then re-checks its generation, finds it moved, and **discards its own result**. There
> was never anything on screen for a reset to wipe. The raise now owns a separate `raiseGenRef` that
> only a newer raise or a different document may invalidate.
>
> ⚠️ **WHY EVERY EXISTING TEST WAS BLIND TO IT:** both step-up tests — on both screens — swap the
> client **after** the raise has already settled. The ordering that actually happens is the reverse.
> A test now holds the raise open across the swap on each screen, and the mutation proof for it is
> "put the raise back on the shared counter".
>
> ⛔ **AND THE `EInvoiceScreen` FIX FROM TWO SESSIONS AGO WAS THEREFORE INCOMPLETE ALL ALONG** — on the
> screen where the discarded value is the **acknowledgement number, which the schema stores nowhere.**
> Both screens are now fixed together, which is §1.3's rule ("fixing the reported call site is not
> fixing the class") applied for the second time to this exact pair.

> ### ⛔⛔ ROW 20 IS FOUND, CONFIRMED, AND DELIBERATELY NOT FIXED — READ THIS BEFORE PICKING IT UP
>
> It is the only defect either pass has left open, and that is a scoping decision rather than an
> oversight: **it is a feature to build, not a line to change.** What the browser proved:
>
> - `Alt+C` renders **`Create ledger`** from the Gateway AND from the item-invoice Item cell.
> - The Gateway menu has no masters entry; `Alt+G` offers `["Vouchers", ...REPORT_NAMES]` only.
> - So **no keystroke in the build reaches `masterKind` `"stock-item"` or `"unit"`.**
>   `MasterCreateScreen`'s two arms are dead code.
> - `PARENT_OPTIONS["stock-item"]` and `["unit"]` are `[]`, so the picker never renders and the
>   create call sends `unit_symbol: p || "Nos"` — **a Kg item is impossible.**
> - The item-invoice empty state instructs *"Alt+C from Gateway > Masters"* — **that route does not
>   exist**, so the dead end is also mis-signposted.
>
> **THE ROUTE TO BUILD IS DOCUMENTED, NOT INVENTED.** RESEARCH.md:568: *"All are reached via Gateway
> of Tally > Create (single) or via **Alt+G (Go To) > Create Master / Alter Master**, and during any
> voucher/master you can create a sub-master on the fly with Alt+C."* So the cheapest honest fix is
> **Go To entries** routed to a `master-create` frame — `resolveGotoSelection` currently hard-codes
> `{screen:"report", reportName}`, so it needs a branch there, and **no new `ScreenKind` is needed**
> because `master-create` is already registered.
>
> ⚠️ **AND `Alt+C` CANNOT BE MADE FIELD-SENSITIVE WITHOUT A BIGGER CHANGE.** `resolveAction` is keyed
> on `ScreenKind` ONLY, and `VOUCHER_FIELDS` is `["date","party-ledger","ledger","amount","narration"]`
> — **the machine has no notion of the item grid at all**, so there is nothing for a field-scoped
> binding to key on today. Do not start with the keymap; start with Go To.
>
> ⚠️ Server-side, `masters.CreateStockItem` writes `unit_id NULL` for an empty `unit_symbol` with no
> CHECK and no refusal — the e-invoice builder is the only thing that ever notices
> (`einvoice.go:711`, `ErrNoUnitOfMeasure`), **by which time the invoice is already posted.** That is
> the `[medium]` recon prediction and it is still UNDRIVEN.

> ### ⛔ THE 22 UNDRIVEN ROWS ARE NOT UNANALYSED — SEE `PARITY-RECON-2026-08-07.md`
> An 18-agent read-only pass traced all of them to file:line and produced exact keystrokes, the
> oracle to read back, and falsifiable predictions. **Its hit rate is measured, not assumed:** four
> of its predictions were driven the same day, three confirmed exactly and one killed. It is the
> single biggest accelerator available to the next session — but ⛔ **it is READING, and this repo
> flips a row on BROWSER evidence only.**
>
> The highest-severity undriven predictions, all `high` confidence. ⚠️ **THREE OF THESE HAVE NOW BEEN
> DRIVEN — rows 20, 8 and 11 — and their entries below are struck through with the verdict.** The
> recon's running hit rate is now **6 confirmed / 2 killed out of 8 driven**, which is high enough to
> trust for PRIORITISING and still not evidence on its own.
> - ~~**Row 20**~~ — ⛔ **DRIVEN, CONFIRMED EXACTLY** (both press-points render "Create ledger"; unit hard-coded "Nos"). **Still unfixed — see the box above.**
> - **Row 17** — TDS allocation has no route to attach to an existing challan, so the row cannot be driven as stated at all.
> - ~~**Row 8**~~ — ⚠️ **DRIVEN: HALF RIGHT.** Not in `Alt+G` (true), but **both reports ARE on the Gateway menu and open correctly**; the empty period prompt is a stated design decision, not a lie. `Ctrl+F`/`F12` prediction **KILLED**.
> - **Row 24** — `allow_negative_stock` has no HTTP route, handler or UI: only half the rule exists.
> - **Row 22 (rest)** — a BATCH can never be keyed anywhere; `batchId` is hard-wired `""`.
> - **Row 3** — two New refs on one invoice collapse into ONE bill; Outstandings honours `as_on` for ageing but not for inclusion.
> - **Row 5** — an `Alt+C` party gets `maintain_bill_by_bill = false`, so it can never carry any bill reference.
> - ~~**Row 16**~~ — ⛔⛔ **DRIVEN, CONFIRMED WITH A CONTROL, AND IT WAS TWO DEFECTS.** ✅ Fixed on `EWayBillScreen` AND `EInvoiceScreen`. ⛔ **`MembersScreen` is the third screen of that shape and is TRACED BUT NOT DRIVEN — see LEAD 1b in §4, and read why it was not patched blind.**
> - ~~**Row 11**~~ — ⛔ **DRIVEN, CONFIRMED, AND IT WAS FOUR DEFECTS RATHER THAN ONE.** ✅ All fixed.
> - **Row 6 (rest)** — a Contra creates its counter-account under **Indirect Expenses**.

> ### ⚠️ A NEW TRACED LEAD THE ROW-11 FIX TURNED UP, NOT YET DRIVEN
> The `as_on` echo that made the statement header read **"As at"** with no date is a SHAPE, and
> `grep -rn "AsOn:\s*asOn\b"` finds **three more reports** that still echo the caller's raw
> parameter rather than the date they resolved: `reporting/stock_summary.go:570`,
> `reporting/stock_summary_godown.go:249`, `reporting/stock_batch_summary.go:319`. Whether each is
> operator-visible depends on whether its screen renders `as_on` — **that is the thing to drive**, and
> it touches rows 23 and 29.
>
> ⛔ **AND THE MOCK WAS RIGHT WHERE THE SERVER WAS WRONG, WHICH IS WHY NO CLIENT TEST COULD SEE IT.**
> `mock.ts` already returned the RESOLVED date (`as_on: asOnDate`). §9.4's catalogue records mocks
> that are *more permissive* than the server; this is the inverse and it hides a defect just as
> completely — **every client test ran against a mock that had already fixed the bug.**

> ### ⛔ WHAT THE macOS FINDING DOES TO EVERY EARLIER "✅" IN THIS TABLE
> Every row above driven with Playwright's `Alt+…` was driven with an event **no operator on this
> platform can produce**. The actions themselves were fine — the fix was in the resolver, not in any
> screen — so none of the earlier results is retracted. But the *method* was flattering the product,
> and any future row driven with an `Alt` key must be checked with a composed `key` as well.
> `src/keystroke/normalize.test.ts` now carries the composed characters for all nine bound letters.

---

## 7.1 ⛔ THE DEFECT REGISTER — 78 DEFECTS, 66 STILL OPEN

**This is the audit's actual product.** Every row below was found by DRIVING, is traced to file:line, and
carries a proposed fix in the source material. ⛔ **Do not re-derive these from scratch** — that is the
failure mode §11 of `HANDOFF.md` describes ("a handoff nobody can trust costs a session to re-verify").

⚠️ **HOW TO READ THE COUNT.** 82 defects were reported by the driving agents; **78** is the count after
merging five independent reports of the SAME shared defect (the WebAuthn write-lock, D1). Five agents
finding one defect independently is corroboration, not five defects.

⚠️ **SEVERITY IS THE FINDER'S, AND `high` HERE MEANS "AN OPERATOR LOSES MONEY, GOODS, OR THE ABILITY TO
WORK"** — not "hard to fix".

| # | Row | Sev | Defect | Where | Status |
|---|---|---|---|---|---|
| D1 | 17 | HIGH | There is no route to attach an allocation to an EXISTING TDS challan, so the row's own oracle — the challan's unallocated balance must FALL — can never be satisfied after the challan is recorded. Seven in-browser doors and six HTTP methods were tried; 05130's unallocated stayed at exactly 12000.0000. | backend/internal/server/server.go:846 | ⛔ OPEN |
| D2 | 17 | HIGH | Nothing compares the sum of a challan's allocations against its tax box, so a 1-rupee deposit was accepted as having "paid" 27,000.0000 of tax across two deductees. unallocated went to -26999.0000, both deductions were marked "already deposited", and the screen captions the negative as the ordinary paise remainder. | backend/internal/masters/tds_challan.go:495 | ⛔ OPEN |
| D3 | 17 | HIGH | A TDS or TCS deduction cannot be keyed from the keyboard anywhere in this product: the client's VoucherRequest type has no tds/tcs field at all, so row 17's precondition is unreachable and the challan picker is empty on any books a real operator built. | frontend/src/api/types.ts:698 | ⛔ OPEN |
| D4 | 17 | HIGH | The TCS half of row 17 does not exist: tcs_collection is not part of the challan mechanism, so a collector can never record that it deposited what it collected. A live 2000.0000 collection under 206C(1)-SCRAP never appears in the allocation picker. | backend/internal/masters/tds_challan.go:1014 | ⛔ OPEN |
| D5 | 24 | HIGH | `allow_negative_stock` has no writer anywhere in the product: no HTTP route, no request field, no service setter, no UI control. Both enforcers read it and honour it correctly, but it is permanently false for every tenant, so the "allowed per the company's flag" arm of Tally's rule can only be reached by a hand-written psql UPDA … | backend/internal/server/server.go:1161 (companyReq, field absent) + backend/internal/server/server.go:515-524 (no Put/Patch on /v1 … | ⛔ OPEN |
| D6 | 27 | HIGH | An order's party is picked from the ENTIRE chart of accounts — a sales order can be committed to a bank ledger and a purchase order to a customer. Nothing on the client, in Validate, or in the DB restricts it to Sundry Debtors / Sundry Creditors. | frontend/src/screens/OrderRegisterScreen.tsx:753 (and the unfiltered load at :258) / backend/internal/orders/order.go:156 | ⛔ OPEN |
| D7 | 27 | HIGH | The fulfilment picker sorts candidate orders by order_no as a raw string while its own comment claims "Oldest commitment first", so the order offered first is the NEWEST one. | frontend/src/screens/vouchers/FulfilmentPicker.tsx:156 | ⛔ OPEN |
| D8 | 29 | HIGH | Closing stock VALUE is replayed in LEXICAL voucher_no order across independent numbering series, so an issue keyed after a receipt on the same date is costed at the pre-receipt average. Confirmed to the paisa in the browser: Closing Stock 25,909.3478 for MS Angle where entry order gives 25,307.8797 — 601.4681 too high, with quan … | backend/internal/inventory/valuation.go:155-170 (func less; `if a.VoucherNo != b.VoucherNo { return a.VoucherNo < b.VoucherNo }`) | ⛔ OPEN |
| D9 | 29 | HIGH | On the Balance Sheet the Closing Stock row (and its Stock-in-Hand group) is invisible in the default condensed view and is likewise absent from the Alt+E export, while the identically named row on the P&L is always visible. | frontend/src/screens/reports/reportView.ts:158-162 (`if (!cfg.detailed) out = out.filter((r) => (r.depth ?? 0) <= 1)`), with backe … | ✅ **FIXED (L10) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D10 | 3 | HIGH | Two NEW references on one invoice collapse into a SINGLE bill: post() stamps the allocated voucher number onto EVERY unnamed reference with no uniqueness check, and Outstandings groups by (ledger_id, ref_name) taking MIN(due_date). An 11,800 invoice split 5,900@30d / 5,900@60d ages entirely at the 30-day date, and the 60-day ins … | backend/internal/ledger/ledger.go:1754-1756 (stamp); backend/internal/reporting/outstanding.go:141,158 (MIN(due_date) + GROUP BY r … | ⛔ OPEN |
| D11 | 3 | HIGH | ADVANCE and ON_ACCOUNT are stored as ordinary named bills. They take the same unnamed->voucher-number stamp as NEW, each voucher type has its own bare-integer counter, and Outstandings ignores ref_type when grouping — so an on-account or advance payment silently NETS AGAINST an unrelated invoice of another voucher type whose num … | backend/internal/ledger/ledger.go:1754-1756 (stamps ON_ACCOUNT/ADVANCE identically to NEW); backend/internal/ledger/ledger.go:2529 … | ⛔ OPEN |
| D12 | 3 | HIGH | Outstandings honours as_on for AGEING but not for INCLUSION: the query has no voucher_date predicate at all, so bills raised AFTER the as-on date are still reported under a caption asserting the two dates agree. Driven: os-total 2,65,720.00 at as-on 2026-04-30 against a Balance Sheet Sundry Debtors of 1,79,000.00. | backend/internal/reporting/outstanding.go:145-155 (the FROM sub-select's only predicate is `WHERE vh.is_deleted = false`; asOnTime … | ✅ **FIXED (F8)** — `as_on` now decides INCLUSION as well as ageing; the two reports AGREE at 2026-04-30 (both 1,79,000.00) and at today (both 2,53,920.00) |
| D13 | 3 | HIGH | The Outstandings screen can never show the PAYABLE side and never shows the per-bill breakdown, so no on-screen surface can verify any bill-wise reference on a supplier. The server serves both (kind=PAYABLE returns 200; the response already carries a per-bill `bills` array) — the client never sends `kind` and never reads `.bills … | frontend/src/screens/reports/ReportScreens.tsx:550-551 (`const query: { as_on?: string } = {}` — `kind` is never a member and neve … | ⚠️ **PARTIAL (F8)** — the per-bill breakdown is on screen and exported (that half is DONE and driven). ⛔ **The PAYABLE side is deliberately NOT a `?kind=` toggle:** `REPORT_NAMES`' own Order Book note settles it — two directions must be two report NAMES, because the PDP decides on the report name and a query parameter is INVISIBLE to it, so a toggle would make "may see debtors, not creditors" an unwritable rule. It needs the Order Book's treatment: a `Payables` report name + a server resource + a migration. **A BUILD, not a patch** |
| D14 | 3 | HIGH | BONUS, found by driving: normalize.ts folds the Meta (Command) key into Ctrl, so macOS's universal Cmd+A Select All resolves to Ctrl+A = ACCEPT. Selecting the contents of a voucher field to retype it POSTS THE VOUCHER. | frontend/src/keystroke/normalize.ts:132 — `const ctrl = Boolean(ev.ctrlKey // ev.metaKey);` | ✅ FIXED (F2) |
| D15 | 30 | HIGH | Accepting the same OCR draft twice posts a SECOND, identical purchase voucher. The draft never leaves status "ready" and stays in the inbox after a successful accept, so nothing on screen or on the server stops the operator (or a double keypress) from filing the same supplier invoice again. The one guard that exists — UNIQUE (co … | frontend/src/screens/vouchers/draftAccept.ts:326-338 (acceptPurchaseDraft: POSTs and returns; never marks the draft consumed) + fr … | ⚠️ PARTIAL (F6) |
| D16 | 30 | HIGH | PII is not redacted on the values that BECOME the voucher. A PAN placed in the OCR-inferred invoice number is written verbatim into voucher_version.voucher_no, and a raw Aadhaar in a line description becomes a PERMANENT ledger master name — while the draft panel tells the operator "PII redacted: 4 (PAN 1, IFSC 1, GSTIN 1, AADHAA … | insightd/insightd/pipeline.py:69-71 (fixture is extracted from the RAW doc, then stashed) + insightd/insightd/adapters/fakes.py:25 … | ⛔ OPEN |
| D17 | 30 | HIGH | The bill-wise references the draft panel renders — and explicitly certifies as "referenced in full (21240.0000)" — are silently dropped. draftBodyToVoucherRequest builds no bill_allocations key, so the accepted purchase writes zero bill_allocation rows, while a normal keyboard F9 purchase does send them. | frontend/src/screens/vouchers/draftAccept.ts:42-88 (draftBodyToVoucherRequest: postings + header scalars + gst only; no bill_alloc … | ⚠️ PARTIAL (F6) |
| D18 | 30 | HIGH | A draft created through the Bill Inbox screen carries NO input-tax posting legs and is unbalanced by exactly the tax (debits 1000.0000 vs credit 1180.0000), so a taxed bill filed through the screen's own front door can never post. The screen sends tenant: { company_id } only, the fixture's own tenant block is ignored, so ctx.cgs … | frontend/src/screens/BillInboxScreen.tsx:150-154 and :190-196 (tenant: { company_id: companyID }) + insightd/insightd/api/routes_d … | ⛔ OPEN |
| D19 | 30 | HIGH | An INTRA-state bill is derived as IGST. Because the screen supplies no tenant ledgers, the supplier can never be matched, supplier_state stays None, and tax.py only concludes intra-state when BOTH state codes are non-None and equal — so the else branch books the whole tax as IGST for a fixture whose OCR says supplier state 27 an … | insightd/insightd/adapters/mapper.py:194-199 and :229-237 (supplier_state resolved only from ctx.ledgers) + insightd/insightd/adap … | ⛔ OPEN |
| D20 | 31 | HIGH | The NL answer does not match the report. "receivables" / "top debtors" reports 133920.0000 against the Outstandings report's 253920.0000 (short by 120000.0000) and "payables" reports 36050.0000 against 104050.0000 (short by 68000.0000). The DSL's outstandings entity reads bill_allocation only and never unions ledger_opening_bill … | backend/internal/query/whitelist.go:312-341 (EntityOutstandings skeleton = `bill_allocation ba JOIN voucher_head ... JOIN ledger . … | ⛔ OPEN |
| D21 | 31 | HIGH | "sales last month", "sales last quarter" and "sales last year" silently answer for the CURRENT period. relativeRange has no last/previous branch; the substring "month" in "last month" matches the first case, so the window becomes the 1st..last of TODAY's month. The DSL that would reveal it is behind a collapsed-by-default panel. | backend/internal/query/translate.go:196-210 (relativeRange: only `this month/month`, `this quarter/quarter`, `this year/year`) rea … | ⛔ OPEN |
| D22 | 32 | HIGH | The row's requirement fails at the first hurdle: there is no way to reach a cash-flow projection or the anomaly list from the keyboard, so the mandated disclosure cannot be rendered because there is no renderer. Four doors tried and closed: the 18-entry Gateway menu has no such entry; Alt+G Go To offers none of "Cash Flow"/"Cash … | frontend/src/screens/GatewayScreen.tsx:4-60 (GATEWAY_MENU — no cash-flow/anomaly entry) + frontend/src/keystroke/navMachine.ts:650 … | ⛔ OPEN |
| D23 | 32 | HIGH | The cash-flow response carries no disclosure that it is a projection and no statement of its basis. The complete key set is as_on, horizon_days, bucket_days, opening_cash, buckets, beyond_horizon, overdue_folded_into_first_bucket — seven keys, none containing projection/estimate/basis/assumed/method. Yet the engine invents a set … | backend/internal/server/handler_forecast.go:105-118 (cashFlowJSON — the complete key set) + backend/internal/forecast/forecast.go: … | ⛔ OPEN |
| D24 | 33 | HIGH | Every invite is delivered to NOBODY and the screen then tells the operator "invitation sent". The invite form collects no email address, notify falls back to the SUBJECT as the recipient, LogSender sees no '@', logs NO EMAIL ON FILE and returns nil, so settle() records status='sent' and deliveryText renders that as "invitation s … | frontend/src/screens/MembersScreen.tsx:264-267 posts only {subject, security_level_name} (the InviteMemberRequest.email field at f … | ✅ FIXED (F5) |
| D25 | 33 | HIGH | Signing in never claims a pending invite. GET /v1/me is the only seam that converts an invite into a membership, and the frontend calls it from exactly ONE place -- the F3 Select Company screen. An invited user who signs in therefore does not land in the company they were invited to. | backend/internal/server/handler_admin.go:660-690 (handleMe is the claim seam: 'claim first, then list'); the ONLY frontend caller … | ⛔ OPEN |
| D26 | 33 | HIGH | When ?company=<id> is refused by the server, the app SILENTLY creates a brand-new company and makes the user its Owner, reporting the substitution only to the devtools console. A user who was invited as a restricted level lands on an empty tenant with FULL access, and every screen looks correct. | frontend/src/app/CompanyContext.tsx:129-140 isUnusablePersistedId() treats 403 as 'unusable'; CompanyContext.tsx:188-245 the boots … | ✅ FIXED (F4) |
| D27 | 35 | HIGH | F3 Switch Company changes the session but is NEVER persisted, so any page load silently returns the operator to the PREVIOUS company — and the next voucher they key lands in it. Proved in both directions and confirmed in the DB: a Sales voucher keyed after F3->BETA35 + a reload was written to ALPHA35. | frontend/src/screens/CompanySelectScreen.tsx:88-96 (calls client.getCompany + setCompany + COMPANY_SWITCHED_SENTINEL, and never pe … | ✅ FIXED (F4) |
| D28 | 37 | HIGH | The back-dated cutoff is bypassed outright by loosening the SPELLING of the date, whenever the body also carries an explicit voucher_no. The PDP's date peek fails OPEN, and the only strict re-parse in the write path lives inside the auto-numbering branch, which an explicit voucher_no skips. | backend/internal/server/server.go:3191-3203 (peekVoucherDate returns nil on any non-"2006-01-02" spelling) + backend/internal/auth … | ✅ FIXED (F3) |
| D29 | 37 | HIGH | On the four stock-only voucher screens a back-dated-cutoff refusal is rendered as an ACL refusal: every 403 is replaced with "...is refused to your security level — ask an Owner to allow 'create <type>' on your level", so the operator is sent to fix a permission they already hold and is never told a period is locked. | frontend/src/screens/vouchers/StockVoucherScreen.tsx:237-243 | ⛔ OPEN |
| D30 | 40 | HIGH | The Delivery Note — one of the six classes printTarget.ts declares printable — has no Ctrl+P at all: StockVoucherScreen registers only "accept", so the key falls through to the shell's generic "does not apply on this screen" and makes NO HTTP request, while the voucher bar on that very screen advertises "Ctrl+P Print". | frontend/src/screens/vouchers/StockVoucherScreen.tsx:251 (only useRegisterScreenAction("accept", …); no usePrintAction import at : … | ⛔ OPEN |
| D31 | 40 | HIGH | A Debit Note can NEVER be printed by this product. printingdb reads a debit note's against-invoice number only from voucher_version.supplier_invoice_no, and no HTTP request can set that column, so every debit note — including a seeded one that carries a bill_allocation AGAINST SUP-101 — answers 422. | backend/internal/printing/printingdb/document.go:1738-1743 (doc.AgainstNumber = h.supplierDocNo) and backend/internal/printing/vou … | ⛔ OPEN |
| D32 | 40 | HIGH | A Credit Note keyed in this app can never print either, and the escape hatch the code assumes does not exist: the bill picker's "Agst Ref" row is a <select> whose options come from useOpenBills, which only fetches when mode==="settle" — so on a credit note (mode="raise") the only option is the empty "— bill —", there is no free- … | frontend/src/screens/vouchers/BillAllocationPicker.tsx:128-131 (useOpenBills(..., mode === "settle")) and :191-200 (the AGAINST va … | ⛔ OPEN |
| D33 | 40 | HIGH | Re-opening the Day Book after any drill in the same session makes Enter open a DIFFERENT voucher than the highlighted row — the drill target freezes at one stale id — so Ctrl+P from the alter path prints or refuses the wrong document. | frontend/src/screens/reports/DrillRows.tsx:92-100 (the publish effect is keyed ONLY on [activeKey], so a freshly pushed report fra … | ⛔ OPEN |
| D34 | 42 | HIGH | Ctrl+P ignores the F2 period on EVERY report: the print target carries no from/to/as_on (only bank-reconciliation gets a query, and even that one carries ledger_id without a date), the server treats a missing date as "no filter", and the paper is subtitled "For all dates in the books" / "As at the latest date in the books" while … | frontend/src/screens/printTarget.ts:224-230 (the report branch returns { kind:"report", slug, label } with no `query`) and :250-25 … | ⛔ OPEN |
| D35 | 42 | HIGH | The TDS / TCS Register advertises "Ctrl+P Print" on its button bar and the key is dead: TDSRegisterScreen imports only useExportAction, never usePrintAction, so Ctrl+P makes no HTTP call and answers the shell's generic "does not apply on this screen" instead of the server's 501 sentence that printTarget.ts deliberately mapped th … | frontend/src/screens/reports/TDSRegisterScreen.tsx:7 and :112 (useExportAction only; no usePrintAction anywhere in the file) vs fr … | ⛔ OPEN |
| D36 | 5 | HIGH | There is NO keyboard path to an opening balance, an opening Dr/Cr, a bill-wise opening breakup, or maintain_bill_by_bill on ANY ledger. The Alt+C master-create frame renders exactly two inputs (Name, Under (group)); the MASTER_FIELDS 'details' cursor position has no control behind it; and no screen anywhere calls setLedgerOpenin … | frontend/src/screens/masters/MasterCreateScreen.tsx:66-69 (`await client.createLedger(companyID, { name: n, group_name: p })` — th … | ⛔ OPEN |
| D37 | 5/30/34/35/37 | HIGH | Past 64 WebAuthn credentials EVERY WRITE in the product becomes permanently impossible on every device: assert/begin returns an unbounded allow_credentials list, Chrome throws RangeError above 64, and the client only fell through to enrolment on NotAllowedError. Reported INDEPENDENTLY BY FIVE AGENTS; it blocked this audit for ~2 … | auth_webauthn.go:181-184 + webauthn/store.go ByUser (ORDER BY created_at, no LIMIT) + auth/webauthn.ts:352-362 | ✅ FIXED (F1) |
| D38 | 9 | HIGH | The cost-allocation picker cannot be reached from the keyboard at all: on the particulars grid, Tab, Shift+Tab and Enter move nothing, so `alloc:0-add`, the axis/centre selects and the `rest` button are MOUSE-ONLY — while the hint strip on that screen advertises "Enter: Next field" and "Tab / Shift+Tab: Field". | frontend/src/keystroke/useNavMachine.ts:54 | ✅ **FIXED (L12) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D39 | 9 | HIGH | Ctrl+A on a voucher whose cost split is INCOMPLETE on one axis creates every ledger the operator named BEFORE the server refuses, leaving an orphan account in the chart of accounts with no voucher behind it. Reproduced on BOTH grids (single entry and the Journal's double entry). Recon prediction 1 CONFIRMED. | frontend/src/screens/VoucherScreen.tsx:1057 | ⛔ OPEN |
| D40 | 17 | MEDIUM | Non-existent challan sub-routes answer 403 "unmapped route: access denied" rather than 404/405, so "this route was never built" is indistinguishable from "your level is not permitted". This KILLS the recon's literal proof that PATCH returns 405. | backend/internal/server/auth_middleware.go:967 | ⛔ OPEN |
| D41 | 17 | MEDIUM | LEAD, not row 17: opening an existing Purchase voucher for alteration from the Day Book and pressing Ctrl+A created a new ledger ("Purchases") over the wire and then failed the voucher write with "Error: invalid gst line gst_rate_pct". A refused alter left a master behind. | frontend/src/screens/VoucherScreen.tsx:1462 | ⛔ OPEN |
| D42 | 24 | MEDIUM | F11 Features asserts a capability the build does not have: the "Negative stock" row is labelled "Always on" and its note says "A company can opt in per-company", while the screen offers no toggle and nothing on it can write the flag. The product's own statement of what is switchable is false. | frontend/src/screens/FeaturesScreen.tsx:291-293 (state "always" renders as "Always on" via the map at FeaturesScreen.tsx:486; the … | ✅ FIXED (own) |
| D43 | 24 | MEDIUM | POST /v1/companies accepts `"allow_negative_stock": true` with 201 Created and silently discards it — the caller gets a success for a setting that was never applied. | backend/internal/server/server.go:1188-1240 (handleCreateCompany / decodeJSON tolerates the unknown field) with backend/internal/s … | ⛔ OPEN |
| D44 | 27 | MEDIUM | The Sales/Purchase Order Book flags NOTHING as overdue in its default (no as-at) state, because Overdue() short-circuits to false when asOn is empty — so the view an operator actually opens reports zero late deliveries. | backend/internal/reporting/orderbook.go:159 (the `// asOn == ""` guard at :160) | ⛔ OPEN |
| D45 | 29 | MEDIUM | With no period set, the Stock Summary prints "As at today (everything posted)" over a report that is actually unbounded — it includes post-dated vouchers. On 2026-08-07 the screen showed 72,837.15 under that label where the real as-at-today position is 96,168.18. The Balance Sheet has the same behaviour and prints no date at all … | frontend/src/screens/reports/StockSummaryScreen.tsx:110 (`{asOn ? `As at ${asOn}` : "As at today (everything posted)"}`); the Bala … | ✅ **FIXED (L10) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D46 | 29 | MEDIUM | The Balance Sheet's "Profit & Loss A/c (Current Period)" line ignores F2's From date while the P&L honours it, so the same F2 setting yields 1,16,237.15 on one statement and -37,911.02 on the other, and the Balance Sheet's caption still says "(Current Period)". | backend/internal/reporting/reporting.go:975-977 (`openingBS := decimal.Zero; stockAdj := closing.Sub(openingBS); bs.NetProfit = in … | ✅ **FIXED (L10) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D47 | 29 | MEDIUM | A master creation that the server refuses is reported into a screen that has already been unmounted: Ctrl+A on Create ledger returned the operator to the Gateway with no message while POST /ledgers answered 403 and nothing was created. | frontend/src/screens/masters/MasterCreateScreen.tsx:103-120 (onAccept calls onSelect(n) synchronously, then `create(n, p).catch((e … | ⛔ OPEN |
| D48 | 30 | MEDIUM | A FAILED Ctrl+A still writes to the chart of accounts. resolveDraftLedgers ensures the party ledger and every null posting ledger via real POST /ledgers calls BEFORE the voucher POST is attempted, so an accept the server then rejects leaves an orphan master behind — here 'Sundry Creditor' under 'Sundry Creditors'. | frontend/src/screens/vouchers/draftAccept.ts:274-320 (resolveDraftLedgers: ensureLedger for the party, then each posting) then :33 … | ⚠️ PARTIAL (F6) |
| D49 | 30 | MEDIUM | insightd has no authentication and scopes OCR drafts by the caller-supplied company_id alone, and vite proxies it same-origin at /companies/*. Any company_id lists and fully hydrates another tenant's drafts — supplier names, invoice numbers, dates, posting ledger names, amounts and GST state codes. | insightd/insightd/api/routes_drafts.py:192-219 (list/get keyed only on the path company_id; no auth dependency) + frontend/src/api … | ⛔ OPEN |
| D50 | 31 | MEDIUM | A cross-tenant question is not refused. Naming another company by name, by UUID, or asking for "every company" / "all tenants" all return 200 with the caller's OWN table and no statement that the named tenant was ignored. The data is genuinely safe — the compiled SQL carries no company predicate and scoping rests on FORCE RLS pl … | backend/internal/query/translate.go:113-183 (keyword switch; no entity/party/company recognition and no refusal branch — the fallt … | ⛔ OPEN |
| D51 | 31 | MEDIUM | "total sales" sums vv.taxable_value only. It excludes the GST component of every invoice and excludes credit notes entirely, and the screen discloses neither — so the figure matches no report: 156500.0000 answered, against a net sales-ledger position of 144500.0000 (after a 12000.0000 credit note) and an invoice total of 183080. … | backend/internal/query/translate.go:148-159 (metric sum(taxable_value), filter voucher_type_name = 'Sales' — Credit Note is a sepa … | ⛔ OPEN |
| D52 | 32 | MEDIUM | FeaturesScreen advertises "Cash-flow projection and anomaly detection" as state "on" with a note that names no door, while the two AI-native rows immediately above it in the same group name theirs ("Gateway > Bill Inbox", "Gateway > Query"). The capability list overstates what an operator can reach. | frontend/src/screens/FeaturesScreen.tsx:340-354 — :342 note "Gateway > Bill Inbox", :347 note "Gateway > Query", :353 note "Determ … | ⛔ OPEN |
| D53 | 34 | MEDIUM | The Stock Summary refusal hardcodes the words 'the Data Entry level', so a holder of any OTHER level is told a level they do not hold is the one denying them, and is pointed at the wrong level's rules. | frontend/src/screens/reports/StockSummaryScreen.tsx:175-183 (the status===403 branch of StockSummaryError): the literal 'so the Da … | ✅ **FIXED (L10) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D54 | 37 | MEDIUM | The refusal names the rule but never the cutoff DATE, so the operator learns a period is locked without learning where the boundary is. | backend/internal/authz/authz.go:270 (`deny("voucher date is before the back-dated cutoff")`), surfaced verbatim at frontend/src/sc … | ✅ FIXED (F3) |
| D55 | 38 | MEDIUM | There is no way to unlock a locked account: no HTTP route, no key, no screen. The only member-admin surface (Users & Roles) renders no lock state at all, and the one route that could plausibly be a remedy (POST /auth/password) has no caller anywhere in the app, so a user cannot even change their own password. | backend/internal/authn/lockout.go:91-96 (RecordSuccess is the sole ClearLock caller); backend/internal/server/auth_login.go:206-21 … | ⛔ OPEN |
| D56 | 38 | MEDIUM | Locking an account does not revoke the sessions it already holds: a locked subject's existing bearer keeps working for the full 3600-second token TTL, including authenticated WRITES. | backend/internal/server/auth_login.go:206-217 (IsLocked is consulted only on POST /auth/login) and loginTokenTTLSeconds (the 200 b … | ⛔ OPEN |
| D57 | 38 | MEDIUM | The lockout threshold, window and duration are hardcoded constants with no configuration path at all, so "N is the configured N" cannot be satisfied — N is a compile-time 5, and there is no per-deployment or per-company setting, nor the exponential backoff the project's own design docs promise. | backend/internal/server/server.go:231 (authn.NewLocker(authn.DefaultLockoutPolicy(), …) — the policy is constructed inline from a … | ⛔ OPEN |
| D58 | 38 | MEDIUM | The lock message hands the operator a raw UTC RFC3339 instant — "Try again after 2026-08-07T23:09:07Z" — with no conversion to local time and no duration, in a product whose audience is Indian accountants on IST (UTC+5:30). | frontend/src/auth/LoginScreen.tsx:38-44 failureText() interpolates `f.until` straight into the string; the value comes from AuthLo … | ⛔ OPEN |
| D59 | 40 | MEDIUM | The printed Receipt and Payment vouchers name the BANK as the counterparty — "Received From: HDFC Bank" / "Paid To: HDFC Bank" — because the single-entry form puts the bank in both `party` and `party_ledger_id` and printingdb builds the Party block from party_ledger_id. | frontend/src/screens/VoucherScreen.tsx:1036-1043 (party = accountName, req.party_ledger_id = f.partyLedgerId, where the Account fi … | ⛔ OPEN |
| D60 | 40 | MEDIUM | A Sales invoice keyed through the GST panel saves happily and then cannot be printed: the server refuses with 400 because items[0].hsn is a Rule 46(g) mandatory particular, but the voucher form never requires the HSN/SAC field to save. | frontend/src/screens/VoucherScreen.tsx:2063 (gst-hsn is an optional FieldRow in GstPanel) vs backend/internal/printing (validateVo … | ⛔ OPEN |
| D61 | 42 | MEDIUM | The printed Day Book's footer — the only thing on the page explaining why there is no amount total — is CLIPPED mid-sentence to "20 voucher version(s) in this period. No column total is...", and the sentence it was clipped from is factually false for this build: the report lists exactly one row per live voucher, not an original … | backend/internal/server/handler_print_reports.go:546-551 (the Footer row and its "DELIBERATELY NO COLUMN TOTAL" comment) against b … | ⛔ OPEN |
| D62 | 42 | MEDIUM | The printed Stock Summary and the on-screen Stock Summary are different reports: the PDF has ten columns including Opening Value and Inward Value, the screen has seven and puts a QUANTITY under "Inward" immediately beside a MONEY figure under "Outward (at cost)". | backend/internal/server/handler_print_reports.go:700-712 (ten Column headers) and :662-680 (ten cells per row) against frontend/sr … | ⛔ OPEN |
| D63 | 42 | MEDIUM | printTarget's Bank Reconciliation refusal ("Select a bank account first…") cannot be reached from the keyboard — the screen auto-selects the first bank account on open — so Ctrl+P as the very first key already prints a statement for an account the operator never chose, which is the exact outcome the refusal was written to preven … | frontend/src/screens/printTarget.ts:243-249 (the refusal, gated on !voucher.bankLedgerID) against frontend/src/screens/BankReconci … | ⛔ OPEN |
| D64 | 43 | MEDIUM | The Stock Summary export is blind to inward VALUE: the API returns inward_qty, inward_value, outward_qty and outward_value and the sheet writes only inward_qty (under a header reading "Inward") and outward_value (under "Outward (at cost)"), so two adjacent columns mix a quantity with money and the inward money figure — 7,860.00 … | frontend/src/screens/reports/StockSummaryScreen.tsx:276-288 and :290-300 (cells: exactCell(it.inward_qty) then moneyCell(it.outwar … | ✅ **FIXED (L10) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D65 | 5 | MEDIUM | Outstandings folds in ledger_opening_bill unconditionally, with none of the books-beginning-from guard the Balance Sheet applies to ledger.opening_balance. Asked for a date before the books began, Outstandings reports the entire opening ageing (Acme 1,85,480.00 incl. OB/ACME/24-25/07 and /11; Steel Suppliers 80,450.00 incl. OB/S … | backend/internal/reporting/outstanding.go:151-153 — the `UNION ALL SELECT ob.ledger_id, ob.ref_name, ob.dr_cr, ob.amount, ob.due_d … | ✅ **FIXED (F8)** — the opening breakup is `queryBalances`-guarded, so a date before the books began reports nothing on EITHER report |
| D66 | 9 | MEDIUM | The 422 the operator actually reads names the cost category by RAW UUID and prints the 0-based `posting_seq` — the one number CostAllocationPicker.tsx:28-33 declares no screen in this product may ever display. The server code's own comment claims it 'names the axis'. | backend/internal/ledger/cost_allocation.go:294 | ✅ **FIXED (L12) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D67 | 9 | MEDIUM | The picker reports an axis as "allocated in full" when one of its splits has an amount but no cost centre chosen; `buildCostAllocations` then drops that split, and Ctrl+A sends a short axis to the server. Recon prediction 3 CONFIRMED exactly. | frontend/src/screens/vouchers/costAllocation.ts:163 | ✅ **FIXED (L12) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D68 | 27 | LOW | The Alt+X explosion's first note prints the recipe's YIELD where the component phrase belongs, so it reads "3 of SS Sheet 2mm yields 3" — the finished good is named on the consuming side of "yields". | backend/internal/orders/bom.go:374 (args at :377-378) | ⛔ OPEN |
| D69 | 27 | LOW | An order line's due date is accepted when it falls BEFORE the order date, creating a commitment that is overdue the instant it is recorded. | backend/internal/orders/order.go:171 (the per-line loop validates StockItemID, Qty and Rate and never looks at DueDate) | ⛔ OPEN |
| D70 | 27 | LOW | After Alt+W pre-closes an order, the register's line panel keeps showing that order's balance as Outstanding with no pre-closed marker, while the row itself has vanished from the list above it. | frontend/src/screens/OrderRegisterScreen.tsx:1039 (the detail heading) and :1069-1073 (the Outstanding cell) | ⛔ OPEN |
| D71 | 27 | LOW | The Purchase Order Book's quantity column is hard-coded "Delivered" for goods a supplier owes the company, while the purchase register calls the identical figure "Received". | frontend/src/screens/reports/OrderBookScreens.tsx:351 (and the export header at :499) | ⛔ OPEN |
| D72 | 32 | LOW | The optional statistical forecast in insightd is unreachable from the client and carries money as JSON floats rather than the fixed-4 strings the rest of the system pins, so wiring it as-is would reintroduce paisa loss. It does at least declare estimate:true and its method — the disclosure cored's own projection lacks. | insightd/insightd/api/routes_forecast.py:29-36 (weekly_net_cash: list[float], daybook: list[tuple[str, float]]) and :13-16; no cal … | ⛔ OPEN |
| D73 | 38 | LOW | Sign-in attempts made while an account is locked are never audited: handleLogin returns 423 from the IsLocked branch without calling RecordAttempt, so the traffic during the lock window — the exact period an investigator cares about — leaves no trace. | backend/internal/server/auth_login.go:209-217 — the IsLocked branch writes writeLocked and returns; RecordAttempt is reached only … | ⛔ OPEN |
| D74 | 38 | LOW | app_user.failed_attempts is not a running counter: it stays 0 through failures 1-4 and is written only at the instant the lock trips, so nothing can observe that an account is partway to being locked. | backend/internal/authn/lockout_store.go:113-118 — failed_attempts is written ONLY inside SetLock (`UPDATE app_user SET locked_unti … | ⛔ OPEN |
| D75 | 40 | LOW | Every printed voucher's download filename appends the raw voucher UUID after the class and number, contradicting pdfFilename's own doc comment, which says it uses the FIRST non-empty part. | backend/internal/server/handler_print.go:117 (writePDF(w, pdfFilename(pathClass, number, voucherID.String()), pdf)) against the co … | ⛔ OPEN |
| D76 | 43 | LOW | Ctrl+P on the Cost Centre Summary tells the operator "Alt+E is not built here either; read it on screen", which is false: pressing Alt+E on that same screen immediately writes Cost-Centre-Summary-Branch-2026-04-01-to-2027-03-31.csv with correct 4-dp figures. | frontend/src/screens/printTarget.ts:276-279 (the refusal string ending "Alt+E is not built here either; read it on screen.") again … | ⛔ OPEN |
| D77 | 9 | LOW | The voucher read-back orders postings `ORDER BY p.dr_cr DESC, p.amount DESC` with no `p.id` tiebreak, so `posting_seq` is arbitrary for two same-side same-amount postings — the analogous ordinal elsewhere in the codebase uses `ORDER BY p.id`. Recon prediction 2 was KILLED as an observed data loss but the nondeterminism in the SQ … | backend/internal/ledger/ledger.go:846 | ⛔ OPEN |
| D78 | LEAD 1b | LOW | MembersScreen's error banner is a plain <p> with no role="alert", so the refusal that DOES render is never announced to assistive technology. | frontend/src/screens/MembersScreen.tsx:658-662 (the `error ? <p data-testid="members-error" className="... text-red-400">` block); … | ✅ FIXED (F5) |

### ⛔⛔ §7.2 — THE BACK-DATED CUTOFF TOOK **SIX** DOORS TO CLOSE, AND THAT IS THE SESSION'S MAIN LESSON

The cutoff is the control that makes a filed GST period trustworthy. It was believed fixed **four times**,
and each time a browser re-drive walked straight through a different door. Every door is now closed and
re-driven (22/22 checks, controls included, on a binary verified fresh BY SYMBOL). ⛔ **Read the pattern
before you fix anything else in this repo:**

| # | The door | How it was found | Status |
|---|---|---|---|
| 1 | A **loose date SPELLING** (`2026-5-31`) made the PDP's peek return nil, and nil means "no cutoff" | driven (row 37) | ✅ fail closed |
| 2 | A **NESTED** body: `/sync/commit` carries a whole voucher at `intents[].payload`, which a top-level key lookup cannot see | ⛔ re-drive **committed a voucher into the locked period, 200 COMMITTED** | ✅ walks any depth |
| 3 | A **non-JSON** body: `/import/tally-xml` dates its vouchers in `<DATE>` elements | found by walking all 70 write routes; **never driven before the fix** | ✅ own XML peek |
| 4 | The **STORED** date: ALTER moved a voucher OUT of the lock, and DELETE removed one, because the date that matters is in the DB and no body carries it | driven on the wire (200 + `version_no:2`, and 200 `op:DELETE`) | ✅ store read, BOTH ends judged |
| 5 | The **JSON KEY'S CASE**: `encoding/json` binds tags case-INSENSITIVELY; the peek compared `k == "voucher_date"` exactly | ⛔ re-drive put **five back-dated vouchers in the operator's own May Day Book** | ✅ binds via `encoding/json` |
| 6 | **TRAILING BYTES**: `decodeJSON` uses `Decoder.Decode` (ignores trailing bytes); three sibling peeks used `json.Unmarshal` (rejects them) — one appended byte switched the control off | driven on `/vouchers/journal`, `/tds-challans` and `/sync/commit` op=ALTER | ✅ all peeks decode as the handler does |

> ### ⛔⛔ THE ONE SENTENCE TO TAKE FROM THIS
> **Every one of the six doors was a place where an AUTHORIZATION PEEK RE-IMPLEMENTED A DECODING RULE THAT
> LIVES IN `encoding/json`.** The PDP and the handler have to agree about what a body SAYS; wherever they
> disagreed, the disagreement WAS the hole. A peek that decides a write's fate must decode the body the
> way the handler decodes it — same call, same semantics — or it is a second implementation and it will
> drift.

⚠️ **AND THE GUARD THAT MATTERS MORE THAN THE SIX FIXES.** Rounds 1–4 each asserted a *list* of hostile
inputs, and the next bypass was always off-list. Two structural guards now exist instead:
- `assertPeekIsNoLooserThanTheHandler` (in `auth_backdated_key_binding_test.go`) decodes the same bytes
  into the REAL wire structs with the REAL decode call and fails if the peek is nil-where-the-handler-bound
  -a-date, or later than what it bound. **It asserts the property, not the examples.**
- `TestEveryWriteRouteNamingAnExistingDocumentIsAccountedFor` walks the REAL chi router and requires every
  write route naming an existing document to be recognised or explicitly excused with a reason. **A new
  route cannot be mounted in silence.**

### ⛔ §7.3 — STILL OPEN AND HIGH PRIORITY (found by this session, traced, NOT fixed)

| # | Defect | Where | Why it was not fixed |
|---|---|---|---|
| **A** | ~~⛔ **The bank-reconciliation date is NOT covered by the cutoff, and `routeAction`'s comment SAYS IT IS**~~ — ✅ **FIXED (F7)**, see §7.4. Both ends are now judged, and the comment that asserted the control now describes it. ⚠️ **`peekBankDate` is NOT a copy of `peekDepositDate`, and that difference was a REAL door:** the challan's store parses `deposit_date` with exactly `2006-01-02` so a loose spelling dies at 400, but `banking.SetBankDate` hands the string straight to Postgres as `$6::date` — which **accepts `2026-5-20`** — so an unreadable-but-storable bank date has to fail CLOSED | `backend/internal/server/auth_middleware.go`, the `/bank-reconciliation` arm | — |
| **B** | ⛔ **THE SEVENTH DOOR ON THE DRAFT ACCEPT — one supplier bill, two vouchers, REPRODUCED IN A BROWSER.** The CONFIRM (`POST /drafts/{id}/accepted`) is deliberately fire-and-forget and only `console.warn`s, so if it fails after the voucher lands, the only DURABLE record of the acceptance is lost and the claim's TTL becomes the duplicate's timer. Driven: browser A posted purchase 6, its confirm was aborted at the network layer, and the operator was told "Saved" | `frontend/src/screens/vouchers/draftAccept.ts`, `recordAcceptance` (~:1770) | The claim→post→confirm reorder DOES close the race it was built for (verified: two browsers 13ms apart → ONE voucher, loser gets `claim 409`). This residual needs a durable pending-confirm retry, or the acceptance recorded transactionally by cored — a design decision, not a patch |
| **C** | A `?company=` that fails the client-side uuid regex is **silently discarded and somebody else's books are opened instead** — real, populated books, nothing on screen looks wrong. Same "work lands in the wrong company" class as two defects that WERE fixed | `frontend/src/app/CompanyContext.tsx` | Pre-existing; the round-4 lane that owned it was killed by a session limit before starting |
| **D** | A well-formed but NONEXISTENT `?company=` uuid now **wedges permanently** — the documented 400/404 recovery valve never fires because cored answers **403** for a company that does not exist (membership is checked before any handler can 404), so `isDeadCompanyId`'s 404 arm is dead code against this backend | same | Consequence of the (correct) "never silently substitute a company" rule; needs an actionable notice, not a revert |

### ✅ §7.4 — THE FIX LEDGER: WHAT WAS ACTUALLY REPAIRED, AND HOW EACH WAS PROVED

⛔ **Every row carries BOTH proofs, because this repo has been burned by the first one alone:** a unit test
with an EXECUTED mutation proof whose failure was checked against the prediction, **and** a re-drive in a
real browser (or on the wire, where the defect is server-side). §7.1's register rows are annotated; this is
the summary.

| Fixed | Proof | Re-drive verdict |
|---|---|---|
| **WebAuthn write-lock** — past 64 credentials EVERY write in the product was permanently impossible on every device (unbounded `allow_credentials`; Chrome throws `RangeError` above 64; the client only recovered from `NotAllowedError`). Found independently by FIVE agents; it blocked this audit for ~25 min | capped newest-first (⛔ the ASC ordering meant a naive `LIMIT` would have dropped the device the user just enrolled), client recovers by enrolling, retention ceiling with the trade-off documented; DB-backed HTTP tests at 70 and 131 credentials | ✅ **FIX_HOLDS 10/10** — full ceremony in-browser, `Saved No: 4`, cap proved on the wire at 131 creds → exactly 64, newest-first, plus an 18-cred boundary control |
| **⌘A posted the voucher** — `normalize.ts` folded Meta into Ctrl, so macOS Select All resolved to `Ctrl+A` = ACCEPT and posted the document mid-edit | Meta no longer resolves to a Ctrl binding; the test that pinned the fold was changed deliberately | ✅ **FIX_HOLDS 15/15** — real `Meta`+`KeyA`: `voucher_version` 27→27, `posting` 47→47, and ⌘A **still selects** (`getSelection() === "1000"`) |
| **Back-dated cutoff — six doors** (see §7.2) | fail-closed peeks, any-depth walk, XML peek, stored-date read judging BOTH ends, `encoding/json` binding, all sibling peeks aligned; plus two structural guards | ✅ **FIX_HOLDS 22/22** on a binary verified fresh by symbol before AND after the drive |
| **Work landing in the WRONG COMPANY** — a `?company=` the server 403s was silently replaced by a NEW tenant with the user as Owner (7 such tenants were minted during this audit); and **F3 Switch Company was never persisted**, so a reload silently returned the operator to the old company and the next voucher landed there | refuse legibly and mint nothing (URL *and* localStorage doors); the switch persists, with the precedence documented | ✅ **FIX_HOLDS** — no `My Company` minted, and a voucher keyed after an F3 switch + reload lands in **B**; ⛔ two doors of the same class remain open, §7.3 C and D |
| **Invites reached NOBODY while the screen said "invitation sent"** — 52 outbox rows at `sent`, **41 with a recipient containing no `@`** | recorded as terminal `failed` + `NoAddressReason` (⛔ no migration: `status` is CHECK-constrained, so the honesty lives in the pair); `role="alert"` added | ✅ **FIX_HOLDS 10/10** — polled 39s, the lie is gone at both sample points; DB reads `failed` / `no email address on file` |
| **One supplier bill posted TWICE from the Bill Inbox** — four guard layers all asked "is this accepted?" at the same wrong MOMENT; two browsers raced → two real vouchers | reordered to **claim → post → confirm** (claim taken BEFORE any ledger master is created, released on a failed post, TTL so a dead browser strands nothing); insightd CAS; 14 tests the server half had shipped without | ⚠️ **FIX_INSUFFICIENT — the race it was built for is CLOSED** (two browsers 13ms apart → ONE voucher, loser `claim 409`, anti-stranding verified) **but a SEVENTH door remains: §7.3 B** |
| **Bill-wise references silently dropped** from an accepted OCR draft while the panel certified "referenced in full (21240.0000)" | carried through to the wire | ✅ driven — `bill_allocation` now carries `PF/2026-27/0117 / NEW / C / 21240.0000`, postings correctly oriented (**Input** IGST debited on a 27→29 purchase) |
| **>1 MiB Tally XML imports were truncated** — every write's body was capped at 1 MiB while this route allows 32 MiB, and the cap REPLACES the body, so the importer parsed a document cut off mid-envelope | cap is now per route, matched on trailing path segments so it cannot silently miss outside a mounted chi route | mutation proof exact: `restored 1048576 bytes, want the whole 3145749 … does not end in </ENVELOPE>`. ⚠️ **Pre-existing, NOT collateral from the cutoff work** — `drainBody` has always been unconditional |
| **F11 claimed a per-company negative-stock opt-in that does not exist** — `allow_negative_stock` is read and honoured by both enforcers and can be written by NOTHING (no route, no request field, no setter, no UI) | the claim is now honest and says what is missing | the flag itself is still unwritable — a small build, §7.1 |
| **F8 — the ageing report's `as_on` did not decide what was IN it, and it had no ageing on screen** (D12, D65, half of D13). Outstandings honoured `as_on` for AGEING and not for INCLUSION, so it and the Balance Sheet were two reports over one set of books that disagreed by 86,720.00, each footing, with nothing to reconcile them. Separately the SCREEN dropped `p.bills` — the array the server always sent — so nothing anywhere in the product could show WHICH invoice was open or how overdue it was, which is why bill-wise defects reached a browser undetected | one date predicate, spelled the way `movementSQL` spells it (so a NULL-dated voucher is treated as it is everywhere else), plus `openingIsInScope` — `queryBalances`' books-beginning guard applied to the other place an opening position hides. The screen renders each bill at depth 1 (visible in the CONDENSED view, unlike the Balance Sheet's Closing Stock at depth 2) with its due date and its ageing IN DAYS. ⛔ **A test was PINNING the defect and was changed deliberately:** `forecast_db_test.go` dated an invoice a month AFTER the date it projected from and wrote 1770 down as the expectation — a cash-flow projection that already knows about an invoice raised next month is hindsight, not a forecast | ✅ **FIX_HOLDS 8/8 in a real browser** (⚠️ **the first run was against a STALE BINARY and said the total did not move** — §0.1's symbol check caught it, which is the second time that procedure has paid for itself): the bills render with due dates and days overdue, the party rows survive, and F2 → 2020-04-30 takes the total from **2,53,920.00 to 0.00**. On the wire the two reports now AGREE at both dates. 3 executed mutation proofs |
| **F7 — a BANK DATE could be asserted into a closed period** (§7.3 A), while `routeAction`'s comment said the cutoff already reached it. The third route of that class after the pay run and the challan, and the second to ship with a comment asserting the missing control | `peekBankDate` (the destination) + `storedBankDateForLeg` (the source, because the PUT is an UPSERT and the DELETE withdraws) — **both ends, as round 3 established for the voucher ALTER/DELETE**; the refusal now names the operator's own field (`authz.Request.DateSubject`). ⛔ **AND THE STRUCTURAL GUARD THIS CLASS NEVER HAD:** `TestEveryDateShapedWireFieldIsAccountedForByTheCutoff` parses package `server` and requires **every date-shaped json key on every wire type** to be classified as judged-or-excused-with-a-reason — the three defects were invisible at ROUTE granularity, which is why the existing router walk could not see them — and `TestTheCutoffActuallyReadsEveryDateItClaimsToRead` **executes** every "judged" claim through the real `voucherDateFromRequest`. 3 executed mutation proofs, each predicted then observed | ✅ **FIX_HOLDS 8/8 on the wire** against a binary verified fresh BY SYMBOL, on a seeded tenant: assert-into 403 · loose spelling 403 · trailing byte 403 · move-out 403 · withdraw 403 · **CONTROL** un-tick after the cutoff 200 · **CONTROL** old cheque cleared today 200 · journal control 403. ⛔ **DB oracle, as `postgres`:** two rows survive — `2026-04-02 / 2026-04-05` untouched and `2026-05-20 / 2026-08-01` from the allowed tick — **and not one `attack` row**, so no refusal wrote |
| **An accepted draft did not name the voucher it became** — insightd sent `accepted_voucher_no`, the wire returned it, the type declared it, and the inbox row never rendered it | the row now reads "already posted as … — do not re-enter" | found by the final re-drive; the operator-visibility half of the duplicate defect |

### ⛔ THE THREE DEFECTS DELIBERATELY NOT FIXED, AND WHY — read before picking one up

1. **D-row-29, closing stock replayed in LEXICAL `voucher_no` order** (`inventory/valuation.go:155-170`:
   `if a.VoucherNo != b.VoucherNo { return a.VoucherNo < b.VoucherNo }`). Voucher numbers are bare
   integers with an INDEPENDENT counter per type, so on a shared date `"10" < "9"` and two series
   interleave arbitrarily — an issue keyed after a receipt on the same date is costed at the
   **pre-receipt average**. ⛔ **NOT FIXED ON PURPOSE:** it changes the **valuation/movement primitive**,
   which §5 requires be serialised with T9 FIFO/LIFO, and it **MOVES THE BALANCE SHEET**, so it needs a
   deliberate `verify:demo` re-pin. Same call the second pass made about the value-neutral transfer. **Do
   it as part of T9.**
2. **Row 20's masters gap** — still open, still a BUILD and not a patch. See §7's row-20 box.
3. **Row 24's `allow_negative_stock` writer** — the flag is read and honoured by both enforcers and can
   be written by NOTHING (no route, no request field, no setter, no UI). Building the writer is a small
   feature; ⚠️ **what WAS fixed is F11's claim**, which asserted the per-company opt-in already existed.

## 7.5 ✅ THE DRIVE LEDGER — WHAT HAS BEEN DRIVEN SINCE THE WAVE LANDED

`HANDOFF-WAVE-A-RECOVERY.md` landed eleven lanes with every gate green and **nothing driven**. This
section is the drive, lane by lane. ⛔ **A row appears here only when BOTH proofs are executed:** a
mutation proof whose failure was predicted then observed, **and** a drive in a real browser against a
cored binary verified fresh BY SYMBOL.

**Stack for every drive below:** cored `:8087` (symbol-checked: `allocatable_deductions`,
`assumed_settlement_date` present), statutoryd `:8091` DURABLE, insightd `:8088` durable DSN, vite
`:5173`, tenant `6bc22065-0c2d-4661-be82-b309a7c53d58` seeded fresh, `verify:demo` **33/33**.

### ✅ L10 — THE LANE NOBODY HAD REVIEWED. D9, D45, D46, D53, D64. CLOSED 2026-08-20.

Its predecessor was killed before reporting, so its diff had never been read. **It was read; the code is
sound.** Six mutation proofs, each predicted then observed:

| Mutation | Anchor put back | Result |
|---|---|---|
| M1 (D9) | `out = out.filter((r) => (r.depth ?? 0) <= 1)` | **5 of 8** `reportView` tests RED |
| M2 (D45) | caption `"As at today (everything posted)"` | **3 of 15** RED |
| M3 (D45, server) | `StockSummary{AsOn: asOn}` — echo the caller's string | `TestStockSummaryEchoesTheBoundaryItAppliedAndNotTheCallersString` + `TestEveryStockReportResolvesTheSameDateTheSameWay` RED |
| M4 (D46) | `"Profit & Loss A/c (Current Period)"` | `TestTheBalanceSheetsProfitLineNamesItsOwnPeriodAndNotTheCurrentOne` + `TestTheTwoStatementsMayDIFFERAndTheSheetNoLongerDeniesIt` RED |
| M5 (D53) | hardcode "the Data Entry level" into the refusal | **5 of 15** RED |
| M6 (D64) | drop `"Inward Value"` from the one column list | **6 of 15** RED |

**The drive — `frontend/scripts/drive-l10-stock.mjs`, 13/13, and `drive-d53.mjs`, 6/6:**

- **D45** ✅ With no period the screen reads *"NO DATE LIMIT — every voucher ever posted, including any
  dated AFTER today"*, not the old *"As at today"*. F2 `To = 2026-06-30` → the caption becomes
  *"As at 2026-06-30"* **and the figures move with it** (MS Angle inward `260.0000 / 7,860.00` →
  `220.0000 / 4,340.00`). ⚠️ **A caption that changes over an unchanged table is the same lie in a new
  place, so the probe asserts BOTH.**
- **D64** ✅ Eleven columns, `Inward Qty` at 4 and `Inward Value` at 5 — **adjacent and each named**. The
  `7,860.00` of inward cost that was written down nowhere is on the row.
- **D9** ✅ `Closing Stock` is on the DEFAULT condensed Balance Sheet, under `Stock-in-Hand` under
  `Current Assets` — the ancestors came back with it.
- **D46** ✅ The line reads `Profit & Loss A/c (cumulative to the latest date posted)`. Never
  `(Current Period)`.
- **D53** ✅ Driven as a **real Data Entry member** (`drive-data-entry`, invited through
  `POST /members/invites` and claimed via `GET /v1/me`, exactly as a customer would). The refusal
  **never says "Data Entry"**, the advice is derived from the server's answer (*"A rule on your own
  security level denies this report by name"*), the server's words are quoted verbatim
  (*"disallowed by rule"*), it still states WHY the report is restricted (cost prices), and **no stock
  figure leaks onto the refused page**.
  ⚠️ **ONE HALF IS UNIT-TESTED AND NOT DRIVEN:** the `"default access: none"` branch needs a level with
  **no** rule for this report, and the seeded Data Entry level carries an explicit `disallow`. Reaching
  it in a browser means editing the seeded ACL, so it rests on M5's tests. Stated rather than implied.

⚠️ **ONE PROBE BUG, LOGGED SO IT IS NOT RE-FOUND:** the first run reported D45's F2 half as a defect.
F2 focuses **`period-from`**, and only `To` bounds a position report — *the dialog says so on its face*.
The probe had typed the date into the focused field. **The screen was right and the probe was wrong**,
which is the mirror image of this audit's usual finding and worth the same suspicion.

### ✅ L12 — D38, D66, D67. CLOSED 2026-08-20. **D38 WAS TWO DEFECTS AND ONLY ONE HAD A TEST.**

**Mutation proofs, each predicted then observed:**

| Mutation | Anchor put back | Result |
|---|---|---|
| M7 (D38a) | `combo === "Tab" \|\| combo === "Shift+Tab"` back into `useNavMachine`'s preventDefault | **3 of 195** keystroke tests RED, by name |
| M8 (D38b) | drop `"nextField"`/`"prevField"` from `ALWAYS_MEANINGFUL` | ⛔ **GREEN. EVERY SUITE.** See below |
| M9 (D67) | count a centre-less split toward the axis total | **6 of 12** RED |
| M10 (D66) | name the line `posting_seq %d` again | `TestAShortAxisIsRefused`, `TestValidateCostAllocations`, `TestNoRefusalPrintsAUUIDOrAPostingOrdinal`, `TestARefusalNamesTheLineTheWayTheFormDoes` RED |

⛔⛔ **M8 IS THE FINDING OF THIS LANE.** D38 was **two** defects under one number: the machine
swallowing Tab (fixed, and pinned by three tests) **and the shell reporting the key it had just let
through as inapplicable**. Removing the two strings that fix the second left **36 app tests, 195
keystroke tests, and every suite in the repo that so much as mentions the phrase GREEN** — while the
operator was told *"Tab — Next field does not apply on this screen"* as the focus ring visibly moved.
**The fix had no oracle.** Four tests now stand in `AppShell.test.tsx`, and M8 re-run against them turns
**3 red with the CONTROL still green** (Alt+D on the Gateway *is* still reported — so the assertions
cannot be satisfied by silencing the status line, which is how this class of test usually rots).

⚠️ **THE GENERAL LESSON, AND IT IS NOT ABOUT TAB.** A defect whose two halves live in two files gets
one test. The half with the test is safe; the other half is one refactor from returning, and the suite
will not say a word. **When a fix touches two files, ask which file the test is in.**

**The drive — `frontend/scripts/drive-l12-tab.mjs`, 21/21:** D38 was reported on the cost picker but was
bound GLOBALLY, so it was driven on **five** screens. On the **Gateway**, a **report** (Trial Balance),
**Users & Roles** and the **Bill Inbox**: Tab and Shift+Tab both move `document.activeElement`, and the
status line never denies it. On the **Payment particulars grid**: `alloc:0-add` takes focus after **8 Tab
presses** and Enter adds a split; choosing an axis enables the centre and renders the *rest* button, and
**centre, amount and rest are then all Tab-reachable**.

**`drive-l12-alloc.mjs`, 10/10 (D67):** a split carrying `1000.0000` against **no centre** is NOT
reported as allocated in full — the picker says *"A split on the Department axis has 1000.0000 against no
cost centre … only 0.0000 would reach the books"*. ⛔ **Both controls hold: Ctrl+A makes NO request at
all** (so the 422 never has to happen) **and mints NO ledger** on the way to being refused — D39's class.
Choosing a centre clears the refusal, and ⛔ **the completed voucher then SAVES** through the passkey, so
"refuses everything" cannot pass as "refuses the right thing". Read back as `postgres`:
`Cr HDFC Bank 1000.0000` / `Dr Office Rent 1000.0000` with **exactly one** allocation row,
`Admin / Department / 1000.0000`.

**D66 on the wire** (a client that refuses first can never show the server's message): a short axis
POSTed directly answers **422** — *"one cost category on **the Dr 1000.0000 line** totals 400.0000 …
600.0000 is unattributed"*. **No raw UUID, no `posting_seq`**, asserted by regex, and RULE 4 holds —
zero `voucher_version` and zero `posting_cost_allocation` rows for `created_by='d66-wire'`.

### ⚠️ THREE PROBE BUGS AND ONE PIECE OF RESIDUE — RECORDED BECAUSE EACH LOOKED LIKE A DEFECT

The screen was right and the probe was wrong **four** times in two lanes. This audit's standing
assumption is the opposite, so each one is written down.

1. **`ArrowDown` is inert on a native `<select>` in HEADLESS Chromium** (there is no popup to walk), so
   the axis read `value=""` and three assertions failed. **Type the option's name instead** — which is
   also what an operator does.
2. **A disabled control is not Tab-reachable, correctly.** `alloc:0-centre:0` carries
   `disabled={a.costCategoryId === ""}` and the *rest* button is **not rendered** until an axis is
   chosen. The probe walked past both and logged a defect. **Choose the axis first.**
3. ⛔⛔ **A LEDGER FIELD IS A COMBOBOX AND `.fill()` DOES NOT CHOOSE FROM IT.** Filling sets the visible
   text and binds no ledger id, so Ctrl+A answers *"Account is required"* over a box that plainly reads
   "HDFC Bank". **And do not press `ArrowDown` "to be safe": existing matches come FIRST and
   `Create "<what you typed>"` LAST**, so one ArrowDown past a single match lands on **CREATE**. That is
   how the first run **minted a junk ledger called "HDFC" beside the real "HDFC Bank" and posted a live
   payment to it.**
4. ⛔ **AND THAT RESIDUE THEN POISONED THE NEXT DIAGNOSIS.** With a ledger named "HDFC" now in the books,
   the combobox's top match for "HDFC" became the junk ledger, and the re-probe read that as the product
   pre-selecting raw typed text — a high-severity defect that did not exist. **It cost a diagnosis, and
   it is the concrete argument for §2.1's own-tenant rule**: the drive was moved to a freshly seeded
   tenant (`f338e9ea…`, `verify:demo` 33/33) and every result above is from that one.

## 8. RULES THAT MUST NOT BE BROKEN DURING THIS WORK

- ⛔ **One owner for the contended files** (§1.12): `VoucherScreen.tsx`, `AppShell.tsx`,
  `FeaturesScreen.tsx`, `corpora/validate.py`, `seed-demo.mts`, `HANDOFF.md`. Lanes **REPORT** the
  edit they need; they do not make it.
- ⛔ **Only ONE lane runs DB-backed Go tests.** One Postgres on :5433, shared across worktrees.
- ⛔ **The browser pass is the orchestrator's.** Read-only code recon fans out well (it produced §4);
  driving does not.
- ⛔ **A row flips to `on` on BROWSER evidence only.** Never on a green suite. That rule is the only
  control that has ever worked here.
- ⛔ **Every fix carries a MUTATION PROOF, and you must check that what fails is what you predicted
  would fail.** A proof asserted rather than executed is worth less than none, because it reads as
  evidence — this happened on 2026-08-06 and the claim was false.
- **Re-measure the gates; do not assume them.** Current (**2026-08-08, end of the THIRD pass**): backend
  **2463 / 0 SKIP / 0 FAIL** (verbose census, bracketed by a live `psql` before AND after), frontend
  **1074 / 69 files**, insightd **137**, `tsc` clean, `make verify` **exit 0** run ALONE,
  `verify:demo` **33/33** on a freshly seeded company.
  ⛔ **AND A NEW ENTRY FOR THIS LIST: VERIFY THE RUNNING BINARY BY SYMBOL, NOT BY UPTIME.** A restart in
  this pass silently failed — the kill pattern matched `cmd/cored` but the COMPILED CHILD's command line
  is `/var/folders/.../exe/cored serve`, so the old process kept port 8087 and `/healthz` answered **200
  from the stale binary**. A whole re-drive ran against 17-hour-old code and reported six closed doors as
  open again. `lsof -nP -iTCP:8087 -sTCP:LISTEN` gives the real owner; then
  `strings -a "$BIN" | grep -c <a symbol your fix introduced>` proves what is actually running
  (⚠️ `go tool nm` exposes only ~103 C externals for this binary and is useless here). **"A TCP connect is
  not liveness" already applied to Postgres in §0.1 — it applies to cored too.**
  ⛔ **RUN `make verify` ALONE.** Run beside anything else it fails two ways that are NOT code:
  `internal/sync` hits its **600s timeout** (and the timed-out package then trips the residue
  census), and `EWayBillScreen.test.tsx` fails on a missing `role="radio"` while passing **3/3**
  standalone. Both were seen on 2026-08-07 — and the timeout was reproduced on a **stashed, unmodified
  tree**, which is the only reason it was not mistaken for a regression. **Bisect a red `make verify`
  against a clean tree before believing it.**
  ⚠️ **A down database makes every DB test SKIP and `go test` still exits 0** — the `0 SKIP` is the
  load-bearing number, so bracket the run with a live `psql`.
- ⚠️ **`docker ps` can wedge while OrbStack reports Running.** It happened on 2026-08-07. `orb stop`
  then `orb start` clears it — but it bounces **every** container on the machine, and `tally_pg` does
  **not** come back on its own (`docker start tally_pg`).
