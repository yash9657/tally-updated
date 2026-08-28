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
| **Row 20** | ✅ **CLOSED (L13) 2026-08-21 — this row was STALE and contradicted §7.4 item 2 and row 20 of §3 for four days.** It read *"CONFIRMED LYING, STILL NOT FIXED — a company can never be given a stock item or a unit from the keyboard"*. It can: `Alt+G > Create Unit` / `Create Stock Item` reach `masterKind` "unit"/"stock-item", and a **Kg** item was created from the keyboard and read back from the books as `Kg` (not the silent "Nos"), mutation-proved and driven, 33/33. See §7.5's L13 lane. ⚠️ **Corrected 2026-08-25 rather than deleted, because a still-open table is exactly what a session reads first** — and this one would have sent it to rebuild a feature that already works, and would have blocked the inventory-on-import work that depends on it | §7.5, lane L13 |
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
| 20 | Stock items + units (masters) | `Alt+C` → Stock Item | the master persists **with its unit** — a blank unit is what broke every e-invoice line | ✅ **CLOSED (L13) 2026-08-21 — MUTATION-PROVED *AND* DRIVEN.** `Alt+G > Create Unit` / `Create Stock Item` reach `masterKind` "unit"/"stock-item"; a **Kg** item was created from the keyboard and read back from the books as `Kg`, not the silent "Nos". See §7.5 |
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
| ⚠️ **Superseded — this table is the 2026-08-07 snapshot** | The register has since grown to **86** (D79–D84 registered from the wave's §6 findings; **D85** and **D86** found by the L11 drive) and **25 are still open.** ⛔ **§7.1's own header is the live count — read it, not this row.** |
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

## 7.1 ⛔ THE DEFECT REGISTER — 102 REGISTERED · 100 CLOSED · **2 OPEN** · 0 PARTIAL (2026-08-27)

> ⭐ **THE REGISTER RE-OPENED ON 2026-08-27, DELIBERATELY.** It stood at 97 · 97 · 0 for three days.
> **D98**, **D99**, **D100** and **D101** were registered by the U1 lane (`HANDOFF-AUDIT-THE-UNDRIVEN.md`) and all four are
> **OPEN BY CHOICE** — §5's rule is that registering closes a row and FIXING is a separate decision.
> ⭐ **D102 JOINED THEM 2026-08-27** — the unchecked `aud` claim in the production JWKS verifier. It is
> the one row here that was NOT found by driving: it was found by `HANDOFF-FEATURE-LOCK-LINE.md` §2.8
> reading the SSO FeaturesScreen row, which disclosed a defect inside a shipped half while arguing the
> FEATURE should be deferred. ⛔ **THE LESSON IS THE ONE THIS REGISTER EXISTS FOR: a gap disclosed on a
> screen is not a gap recorded on the register, and deferring a feature must never defer the defects
> already inside it.**
> ⭐⭐ **AND THREE OF THEM CLOSED THE SAME DAY THEY WERE DECIDED.** D100 (the export recipe), D98 (the
> voucher pass is now one transaction) and D101 (voided vouchers stay out of the books) are FIXED,
> each with a mutation proof whose failure was **predicted and then observed** — 2 and 5 vouchers for
> D98, 3 posted for D101, the exact numbers the browser drives measured when the rows were written —
> and each re-driven in a browser against a cored verified fresh. **D99 stays OPEN by decision** (its
> premise was refuted by a real export; do not fix it speculatively) and **D102 is newly registered**.
> Re-derived by parsing, not by editing this line: `102 · 96 · 2 · 0` from `docs/parse-defect-register.py`, plus the
> **four rows the naive parser cannot read**, every one of which is CLOSED and each for a known reason:
> **D49** and **D84** say `OPEN` in their status PROSE beside `CLOSED`/`FIXED`; **D82** says
> `WORKING AS INTENDED` and carries no marker word at all; **D80**'s status cell contains a literal
> ` | ` which splits its row into eight cells, so the LAST cell is not the status (it is cell 5).
> `93 + 4 = 97 closed`. ⛔ **Do not "fix" the parser to absorb those four — fix the ROW TEXT**, which is
> this section's own rule and is why D98-D101's status cells avoid the words a parser matches on.

⚠️ **THIS HEADING WAS STALE FOR THREE LANES** (it read `96 · 88 · 8` from 2026-08-23 while the rows below
said otherwise) — which is the file's own §5.21 rule proving itself: **re-derive the count by PARSING the
rows, never by reading this line.** The snippet that does it lives in `HANDOFF-THE-REMAINING-SIX.md`.

> ⛔⛔ **THIS HEADER IS A COPY AND COPIES GO STALE — IT SAID `90 · 74 · 12 · 4` FOR THREE SESSIONS WHILE THE TABLE
> SAID `93 · 81 · 8 · 4`. RE-PARSE THE TABLE; DO NOT QUOTE THIS LINE.** The snippet is in
> `HANDOFF-CLOSE-THE-LAST-EIGHT.md` §0. ⚠️ **It reads the STATUS COLUMN, never the whole line** — CLOSED rows say
> "OPEN" in their prose (D28, D84) and a row now says "PARTIAL" in its prose too, so a line-wide `in` test has
> been wrong in BOTH directions. The hardened parser reproduces `93 · 81 · 8 · 4` on the commit before this one,
> which is how it was checked.
> ⛔ **AND WRITING THESE TWO ROWS BROKE THE PARSER TWICE MORE, IN TWO NEW WAYS** — D22's status cell contained a
> literal ` | ` which split it into two cells (so the last cell had no marker and the row read `?`), and D96's
> status prose contained the words "marked ✅" (so the row read CLOSED while saying OPEN). **Both were fixed in the
> ROW TEXT, not the parser**: a status cell must contain exactly one marker and no cell separator. ⚠️ **The `?`
> bucket is what caught the first one** — a parser that silently bucketed it would have reported a plausible total.

⛔⛔ **THE COUNT WAS RE-DERIVED BY PARSING THIS TABLE ON 2026-08-22, AND IT DID NOT MATCH THE HEADER.**
Two errors, both of the kind §7's own rule warns about (*"update it, do not assume it"*):

1. **D90 was registered and never added to the open count** — the header said 11 open when the table
   held 12. A row can be written and still not counted.
2. **FOUR ROWS ARE `PARTIAL`, NOT CLOSED, AND EVERY PREVIOUS SESSION COUNTED THEM AS CLOSED** —
   D13 (F8), D15, D17, D48 (all F6). That is where the old "73 closed" came from.

`74 + 4 + 12 = 90`. **Do not restate this header from memory — re-parse the table.**

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
| D1 | 17 | HIGH | There is no route to attach an allocation to an EXISTING TDS challan, so the row's own oracle — the challan's unallocated balance must FALL — can never be satisfied after the challan is recorded. Seven in-browser doors and six HTTP methods were tried; 05130's unallocated stayed at exactly 12000.0000. | backend/internal/server/server.go:846 | ✅ **FIXED (L4) — MUTATION-PROVED *AND* DRIVEN 2026-08-21.** `POST /tds-challans/{id}/allocations` + an `Alt+A` panel. ⛔ The oracle is the UNALLOCATED FIGURE ON THE REGISTER FALLING **5000.0000 → 0.0000**, never the 200. See §7.5 |
| D2 | 17 | HIGH | Nothing compares the sum of a challan's allocations against its tax box, so a 1-rupee deposit was accepted as having "paid" 27,000.0000 of tax across two deductees. unallocated went to -26999.0000, both deductions were marked "already deposited", and the screen captions the negative as the ordinary paise remainder. | backend/internal/masters/tds_challan.go:495 | ✅ **FIXED (L4) — MUTATION-PROVED *AND* DRIVEN 2026-08-21.** `ErrChallanOverAllocated`, on BOTH write paths, tolerance one rupee PER ALLOCATION. Driven: a third deduction attached one at a time to a full challan is refused naming **25000 / 26000 / 1000 more**. See §7.5 |
| D3 | 17 | HIGH | A TDS or TCS deduction cannot be keyed from the keyboard anywhere in this product: the client's VoucherRequest type has no tds/tcs field at all, so row 17's precondition is unreachable and the challan picker is empty on any books a real operator built. | frontend/src/api/types.ts:698 | ✅ **FIXED (L4) — MUTATION-PROVED *AND* DRIVEN 2026-08-21, BOTH HALVES.** TDS on the Dr/Cr grid (F7); TCS on the accounting-mode Sales invoice, charged to the buyer through a **keyed additional-charge line** — the user's decision, taken because deriving it in `buildGstVoucherRequest` makes the browser the author of an amount the server owns. See §7.5 |
| D4 | 17 | HIGH | The TCS half of row 17 does not exist: tcs_collection is not part of the challan mechanism, so a collector can never record that it deposited what it collected. A live 2000.0000 collection under 206C(1)-SCRAP never appears in the allocation picker. | backend/internal/masters/tds_challan.go:1014 | ✅ **FIXED (L4) — MUTATION-PROVED *AND* DRIVEN 2026-08-21.** The server half was done; **driving found the client half was not** — both wire mappings DROPPED the row's `kind`, so every collection was refused as "nothing was deducted". A challan paying a collection is recorded, and one paying both is refused. See §7.5 |
| D5 | 24 | HIGH | `allow_negative_stock` has no writer anywhere in the product: no HTTP route, no request field, no service setter, no UI control. Both enforcers read it and honour it correctly, but it is permanently false for every tenant, so the "allowed per the company's flag" arm of Tally's rule can only be reached by a hand-written psql UPDA … | backend/internal/server/server.go:1161 (companyReq, field absent) + backend/internal/server/server.go:515-524 (no Put/Patch on /v1 … | ✅ CLOSED L15 2026-08-22 |
| D6 | 27 | HIGH | An order's party is picked from the ENTIRE chart of accounts — a sales order can be committed to a bank ledger and a purchase order to a customer. Nothing on the client, in Validate, or in the DB restricts it to Sundry Debtors / Sundry Creditors. | frontend/src/screens/OrderRegisterScreen.tsx:753 (and the unfiltered load at :258) / backend/internal/orders/order.go:156 | ✅ **FIXED (L8) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D7 | 27 | HIGH | The fulfilment picker sorts candidate orders by order_no as a raw string while its own comment claims "Oldest commitment first", so the order offered first is the NEWEST one. | frontend/src/screens/vouchers/FulfilmentPicker.tsx:156 | ✅ **FIXED (L8) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D8 | 29 | HIGH | Closing stock VALUE is replayed in LEXICAL voucher_no order across independent numbering series, so an issue keyed after a receipt on the same date is costed at the pre-receipt average. Confirmed to the paisa in the browser: Closing Stock 25,909.3478 for MS Angle where entry order gives 25,307.8797 — 601.4681 too high, with quan … | backend/internal/inventory/valuation.go:155-170 (func less; `if a.VoucherNo != b.VoucherNo { return a.VoucherNo < b.VoucherNo }`) | ✅ **CLOSED L22 2026-08-24 — the second replay key is the voucher's ID, not its NUMBER.** `inventory.less()` ordered on `voucher_no`, a STRING from a PER-VOUCHER-TYPE counter (and on a purchase the supplier's own reference), so on one date a delivery note numbered 2 sorted before a receipt numbered 4 and — within one series — 10 before 9. `voucher_id` is a UUIDv7 minted at CREATE, so it is entry order across every series, and an ALTER keeps it. ⛔ **THIS RE-PRICED REAL BOOKS ON THE NEXT REPORT READ** — `docs/RELEASE-NOTES.md` is created for it. ✅ **The cost model held exactly as the driver predicted:** with ONLY the new fixture added, `corpora/validate.py` failed 3 times on that fixture and `go test ./...` failed in exactly ONE subcase, nothing else — verified by grouping every existing fixture's movements and finding not one same-date pair. ⛔⛔ **M2 DID NOT REPRODUCE AND THAT BOUGHT THE REAL GATE:** deleting `r.movement.VoucherID = …` from the scan left the whole Go suite green AND the wire figure correct, because with every id empty the order fell through to `version_created_at`, which on a seed that posts each voucher in its own transaction happens to agree with entry order — D90's shape, an application key shadowed by a tiebreak. `TestReplayOrdersByENTRY_NotByVoucherNumber` closes it by RESTATING the receipt: an ALTER keeps `voucher_id` and re-mints both `created_at` and `voucher_version.id`, so only the id still orders it first. ⚠️ **And the mutation corrected the test twice** — the first fixture numbered the receipt 10 and the issue 2, and `"10" < "2"`, so M1 passed straight through a test written to catch it. Driven **14/14** on the wire and in a browser, figures predicted before they were read. Full record: §7.5 **L22**. |
| D9 | 29 | HIGH | On the Balance Sheet the Closing Stock row (and its Stock-in-Hand group) is invisible in the default condensed view and is likewise absent from the Alt+E export, while the identically named row on the P&L is always visible. | frontend/src/screens/reports/reportView.ts:158-162 (`if (!cfg.detailed) out = out.filter((r) => (r.depth ?? 0) <= 1)`), with backe … | ✅ **FIXED (L10) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D10 | 3 | HIGH | Two NEW references on one invoice collapse into a SINGLE bill: post() stamps the allocated voucher number onto EVERY unnamed reference with no uniqueness check, and Outstandings groups by (ledger_id, ref_name) taking MIN(due_date). An 11,800 invoice split 5,900@30d / 5,900@60d ages entirely at the 30-day date, and the 60-day ins … | backend/internal/ledger/ledger.go:1754-1756 (stamp); backend/internal/reporting/outstanding.go:141,158 (MIN(due_date) + GROUP BY r … | ✅ **FIXED (L1r) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D11 | 3 | HIGH | ADVANCE and ON_ACCOUNT are stored as ordinary named bills. They take the same unnamed->voucher-number stamp as NEW, each voucher type has its own bare-integer counter, and Outstandings ignores ref_type when grouping — so an on-account or advance payment silently NETS AGAINST an unrelated invoice of another voucher type whose num … | backend/internal/ledger/ledger.go:1754-1756 (stamps ON_ACCOUNT/ADVANCE identically to NEW); backend/internal/ledger/ledger.go:2529 … | ✅ **FIXED (L1r) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D12 | 3 | HIGH | Outstandings honours as_on for AGEING but not for INCLUSION: the query has no voucher_date predicate at all, so bills raised AFTER the as-on date are still reported under a caption asserting the two dates agree. Driven: os-total 2,65,720.00 at as-on 2026-04-30 against a Balance Sheet Sundry Debtors of 1,79,000.00. | backend/internal/reporting/outstanding.go:145-155 (the FROM sub-select's only predicate is `WHERE vh.is_deleted = false`; asOnTime … | ✅ **FIXED (F8)** — `as_on` now decides INCLUSION as well as ageing; the two reports AGREE at 2026-04-30 (both 1,79,000.00) and at today (both 2,53,920.00) |
| D13 | 3 | HIGH | The Outstandings screen can never show the PAYABLE side and never shows the per-bill breakdown, so no on-screen surface can verify any bill-wise reference on a supplier. The server serves both (kind=PAYABLE returns 200; the response already carries a per-bill `bills` array) — the client never sends `kind` and never reads `.bills … | frontend/src/screens/reports/ReportScreens.tsx:550-551 (`const query: { as_on?: string } = {}` — `kind` is never a member and neve … | ✅ **CLOSED — audited at HEAD 2026-08-23 (L16 position 0), SPLIT, and the surviving half is now its own row D94.** The per-bill breakdown half is DONE and driven (`ReportScreens.tsx:586` reads `p.bills ?? []`). ⛔ **The PAYABLE side is deliberately NOT a `?kind=` toggle:** `REPORT_NAMES`' own Order Book note settles it — two directions must be two report NAMES, because the PDP decides on the report name and a query parameter is INVISIBLE to it, so a toggle would make "may see debtors, not creditors" an unwritable rule. It needs the Order Book's treatment: a `Payables` report name + a server resource + a migration. **A BUILD, not a patch — so it is REGISTERED AS ONE: see D94.** ⛔ **This row stops being PARTIAL because a PARTIAL row hides a build**, and "the register is finished" was going to be claimed with a whole reporting direction missing |
| D14 | 3 | HIGH | BONUS, found by driving: normalize.ts folds the Meta (Command) key into Ctrl, so macOS's universal Cmd+A Select All resolves to Ctrl+A = ACCEPT. Selecting the contents of a voucher field to retype it POSTS THE VOUCHER. | frontend/src/keystroke/normalize.ts:132 — `const ctrl = Boolean(ev.ctrlKey // ev.metaKey);` | ✅ FIXED (F2) |
| D15 | 30 | HIGH | Accepting the same OCR draft twice posts a SECOND, identical purchase voucher. The draft never leaves status "ready" and stays in the inbox after a successful accept, so nothing on screen or on the server stops the operator (or a double keypress) from filing the same supplier invoice again. The one guard that exists — UNIQUE (co … | frontend/src/screens/vouchers/draftAccept.ts:326-338 (acceptPurchaseDraft: POSTs and returns; never marks the draft consumed) + fr … | ✅ **CLOSED — audited at HEAD 2026-08-23 (L16 position 0). The race it was built for is closed by FIVE layers and the residue is TRACKED ELSEWHERE, so it stops being PARTIAL.** ⛔ **But the driver's guess — "the residue is exactly §7.3 B" — is NOT quite right, and the difference is worth the row:** §7.3 B is the CONFIRM, AFTER the money moved (`recordAcceptance` fires `recordAccepted(...).catch(report)` un-awaited and only `console.warn`s — confirmed at HEAD, still the shape §7.3 B describes, ⚠️ **updated 2026-08-25: BOTH acceptable answers are now BUILT and driven — see §7.6. The durable pending-confirm retry is the client half; cored recording the acceptance transactionally (`voucher_head.source_draft_id` under a partial unique index, migration 0077) is the server half, and §7.3 B is CLOSED**). The OTHER residue is at the CLAIM, BEFORE the write, and it is **four windows enumerated in full at the code site** (`postDraftBody`'s "THE RESIDUAL WINDOW, STATED IN FULL"): insightd unreachable at step 4 (claim skipped with a warning rather than blocking the operator's only write path), no draft id resolvable, a POST outliving the 120s lease, and an ambiguous timeout. ⚠️ **Those four are DISCLOSED DELIBERATE TRADES, not undisclosed defects** — each states what it costs and why the alternative is worse — so they are NOT registered as rows. **The distinction is the point: §7.3 B is unfinished work; the four windows are finished decisions.** |
| D16 | 30 | HIGH | PII is not redacted on the values that BECOME the voucher. A PAN placed in the OCR-inferred invoice number is written verbatim into voucher_version.voucher_no, and a raw Aadhaar in a line description becomes a PERMANENT ledger master name — while the draft panel tells the operator "PII redacted: 4 (PAN 1, IFSC 1, GSTIN 1, AADHAA … | insightd/insightd/pipeline.py:69-71 (fixture is extracted from the RAW doc, then stashed) + insightd/insightd/adapters/fakes.py:25 … | ✅ **FIXED (L11) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D17 | 30 | HIGH | The bill-wise references the draft panel renders — and explicitly certifies as "referenced in full (21240.0000)" — are silently dropped. draftBodyToVoucherRequest builds no bill_allocations key, so the accepted purchase writes zero bill_allocation rows, while a normal keyboard F9 purchase does send them. | frontend/src/screens/vouchers/draftAccept.ts:42-88 (draftBodyToVoucherRequest: postings + header scalars + gst only; no bill_alloc … | ✅ **CLOSED — audited at HEAD 2026-08-23 (L16 position 0), and it was CLOSED ALREADY AND NEVER RE-MARKED.** `draftBodyToVoucherRequest` takes `billAllocations` as its SECOND parameter and sets `req.bill_allocations` when it is non-empty, and — the half that matters, because a converter nobody calls with refs is the same defect — **the CALL SITE passes them**: `postDraftBody` builds `refs = opts.billRefs ?? defaultDraftBillRefs(resolved, chart)` and calls `draftBodyToVoucherRequest(resolved, draftBillAllocations(resolved, refs))`. Guarded in BOTH directions at HEAD (`draftAccept.test.ts` **56/56**): `expect(req.bill_allocations).toEqual([…])` for a bill-by-bill party AND `expect(req).not.toHaveProperty("bill_allocations")` for one that is not — the second arm is what stops a fix that always emits the key. Driven on the wire in §7.4 (`PF/2026-27/0117 / NEW / C / 21240.0000`). ⚠️ The row's anchor `:42-88` is stale; the converter is at `:92` |
| D18 | 30 | HIGH | A draft created through the Bill Inbox screen carries NO input-tax posting legs and is unbalanced by exactly the tax (debits 1000.0000 vs credit 1180.0000), so a taxed bill filed through the screen's own front door can never post. The screen sends tenant: { company_id } only, the fixture's own tenant block is ignored, so ctx.cgs … | frontend/src/screens/BillInboxScreen.tsx:150-154 and :190-196 (tenant: { company_id: companyID }) + insightd/insightd/api/routes_d … | ✅ **FIXED (L11) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D19 | 30 | HIGH | An INTRA-state bill is derived as IGST. Because the screen supplies no tenant ledgers, the supplier can never be matched, supplier_state stays None, and tax.py only concludes intra-state when BOTH state codes are non-None and equal — so the else branch books the whole tax as IGST for a fixture whose OCR says supplier state 27 an … | insightd/insightd/adapters/mapper.py:194-199 and :229-237 (supplier_state resolved only from ctx.ledgers) + insightd/insightd/adap … | ✅ **FIXED (L11) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D20 | 31 | HIGH | The NL answer does not match the report. "receivables" / "top debtors" reports 133920.0000 against the Outstandings report's 253920.0000 (short by 120000.0000) and "payables" reports 36050.0000 against 104050.0000 (short by 68000.0000). The DSL's outstandings entity reads bill_allocation only and never unions ledger_opening_bill … | backend/internal/query/whitelist.go:312-341 (EntityOutstandings skeleton = `bill_allocation ba JOIN voucher_head ... JOIN ledger . … | ✅ **FIXED (L5) — MUTATION-PROVED *AND* DRIVEN 2026-08-21.** The union was already in the skeleton; reverting it live reproduced **133920 vs 253920** and **36050 vs 104050** — this row's own numbers. See §7.5 |
| D21 | 31 | HIGH | "sales last month", "sales last quarter" and "sales last year" silently answer for the CURRENT period. relativeRange has no last/previous branch; the substring "month" in "last month" matches the first case, so the window becomes the 1st..last of TODAY's month. The DSL that would reveal it is behind a collapsed-by-default panel. | backend/internal/query/translate.go:196-210 (relativeRange: only `this month/month`, `this quarter/quarter`, `this year/year`) rea … | ✅ **FIXED (L5) — MUTATION-PROVED *AND* DRIVEN 2026-08-21.** All six windows driven against the P&L for the window the SCREEN disclosed. The LLM prompt gave the model no clock at all and is fixed too. See §7.5 |
| D22 | 32 | HIGH | The row's requirement fails at the first hurdle: there is no way to reach a cash-flow projection or the anomaly list from the keyboard, so the mandated disclosure cannot be rendered because there is no renderer. Four doors tried and closed: the 18-entry Gateway menu has no such entry; Alt+G Go To offers none of "Cash Flow"/"Cash … | frontend/src/screens/GatewayScreen.tsx:4-60 (GATEWAY_MENU — no cash-flow/anomaly entry) + frontend/src/keystroke/navMachine.ts:650 … | ✅ **CLOSED 2026-08-23 (L16 position 1) — MUTATION-PROVED AND DRIVEN, 34/34 in a real browser, keyboard only.** Both routes now have a door: report names `"Cash Flow"` and `"Anomalies"`, screens in `frontend/src/screens/reports/ForecastScreens.tsx`, client methods `cashFlow()`/`anomalies()`, reachable from Alt+G and Display More Reports. ⛔ **THE ORACLE WAS A KEYBOARD WALK, NEVER AN HTTP 200** — every "is it built?" question that reads the SERVER had answered yes for a whole wave. ⛔⛔ **AND THE LOAD-BEARING ARM WAS A FOOTING AGAINST A DIFFERENT REPORT:** the three basis bill counts READ OFF THE PAGE (9 + 0 + 3 = 12) footed against the OUTSTANDINGS route's open-bill count (receivable 8 + payable 4 = 12). Every other arm has both sides coming from the same handler; only this one could see a projection that lost or duplicated a bill. ⚠️ **D72's FOLDED-IN CLAUSE IS *NOT* CLOSED BY THIS AND HAS BEEN EXTRACTED AS D96** — insightd's OPTIONAL STATISTICAL forecast is a different service and a different endpoint from cored's `/cash-flow`, and it still has no renderer. Leaving it inside a CLOSED row is exactly the mistake position 0 had just finished repairing on D13  ⚠️ **AND THE ACL HOLE IT EXPOSED IS D95**, not this row: neither name has a counterweight migration |
| D23 | 32 | HIGH | The cash-flow response carries no disclosure that it is a projection and no statement of its basis. The complete key set is as_on, horizon_days, bucket_days, opening_cash, buckets, beyond_horizon, overdue_folded_into_first_bucket — seven keys, none containing projection/estimate/basis/assumed/method. Yet the engine invents a set … | backend/internal/server/handler_forecast.go:105-118 (cashFlowJSON — the complete key set) + backend/internal/forecast/forecast.go: … | ✅ **FIXED (L11) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D24 | 33 | HIGH | Every invite is delivered to NOBODY and the screen then tells the operator "invitation sent". The invite form collects no email address, notify falls back to the SUBJECT as the recipient, LogSender sees no '@', logs NO EMAIL ON FILE and returns nil, so settle() records status='sent' and deliveryText renders that as "invitation s … | frontend/src/screens/MembersScreen.tsx:264-267 posts only {subject, security_level_name} (the InviteMemberRequest.email field at f … | ✅ FIXED (F5) |
| D25 | 33 | HIGH | Signing in never claims a pending invite. GET /v1/me is the only seam that converts an invite into a membership, and the frontend calls it from exactly ONE place -- the F3 Select Company screen. An invited user who signs in therefore does not land in the company they were invited to. | backend/internal/server/handler_admin.go:660-690 (handleMe is the claim seam: 'claim first, then list'); the ONLY frontend caller … | ✅ **FIXED (L9) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D26 | 33 | HIGH | When ?company=<id> is refused by the server, the app SILENTLY creates a brand-new company and makes the user its Owner, reporting the substitution only to the devtools console. A user who was invited as a restricted level lands on an empty tenant with FULL access, and every screen looks correct. | frontend/src/app/CompanyContext.tsx:129-140 isUnusablePersistedId() treats 403 as 'unusable'; CompanyContext.tsx:188-245 the boots … | ✅ FIXED (F4) |
| D27 | 35 | HIGH | F3 Switch Company changes the session but is NEVER persisted, so any page load silently returns the operator to the PREVIOUS company — and the next voucher they key lands in it. Proved in both directions and confirmed in the DB: a Sales voucher keyed after F3->BETA35 + a reload was written to ALPHA35. | frontend/src/screens/CompanySelectScreen.tsx:88-96 (calls client.getCompany + setCompany + COMPANY_SWITCHED_SENTINEL, and never pe … | ✅ FIXED (F4) |
| D28 | 37 | HIGH | The back-dated cutoff is bypassed outright by loosening the SPELLING of the date, whenever the body also carries an explicit voucher_no. The PDP's date peek fails OPEN, and the only strict re-parse in the write path lives inside the auto-numbering branch, which an explicit voucher_no skips. | backend/internal/server/server.go:3191-3203 (peekVoucherDate returns nil on any non-"2006-01-02" spelling) + backend/internal/auth … | ✅ FIXED (F3) |
| D29 | 37 | HIGH | On the four stock-only voucher screens a back-dated-cutoff refusal is rendered as an ACL refusal: every 403 is replaced with "...is refused to your security level — ask an Owner to allow 'create <type>' on your level", so the operator is sent to fix a permission they already hold and is never told a period is locked. | frontend/src/screens/vouchers/StockVoucherScreen.tsx:237-243 | ✅ **FIXED (L7) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D30 | 40 | HIGH | The Delivery Note — one of the six classes printTarget.ts declares printable — has no Ctrl+P at all: StockVoucherScreen registers only "accept", so the key falls through to the shell's generic "does not apply on this screen" and makes NO HTTP request, while the voucher bar on that very screen advertises "Ctrl+P Print". | frontend/src/screens/vouchers/StockVoucherScreen.tsx:251 (only useRegisterScreenAction("accept", …); no usePrintAction import at : … | ✅ **FIXED (L7) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D31 | 40 | HIGH | A Debit Note can NEVER be printed by this product. printingdb reads a debit note's against-invoice number only from voucher_version.supplier_invoice_no, and no HTTP request can set that column, so every debit note — including a seeded one that carries a bill_allocation AGAINST SUP-101 — answers 422. | backend/internal/printing/printingdb/document.go:1738-1743 (doc.AgainstNumber = h.supplierDocNo) and backend/internal/printing/vou … | ✅ **FIXED (L7) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D32 | 40 | HIGH | A Credit Note keyed in this app can never print either, and the escape hatch the code assumes does not exist: the bill picker's "Agst Ref" row is a <select> whose options come from useOpenBills, which only fetches when mode==="settle" — so on a credit note (mode="raise") the only option is the empty "— bill —", there is no free- … | frontend/src/screens/vouchers/BillAllocationPicker.tsx:128-131 (useOpenBills(..., mode === "settle")) and :191-200 (the AGAINST va … | ✅ **FIXED (L1r) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D33 | 40 | HIGH | Re-opening the Day Book after any drill in the same session makes Enter open a DIFFERENT voucher than the highlighted row — the drill target freezes at one stale id — so Ctrl+P from the alter path prints or refuses the wrong document. | frontend/src/screens/reports/DrillRows.tsx:92-100 (the publish effect is keyed ONLY on [activeKey], so a freshly pushed report fra … | ✅ **FIXED (L7) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D34 | 42 | HIGH | Ctrl+P ignores the F2 period on EVERY report: the print target carries no from/to/as_on (only bank-reconciliation gets a query, and even that one carries ledger_id without a date), the server treats a missing date as "no filter", and the paper is subtitled "For all dates in the books" / "As at the latest date in the books" while … | frontend/src/screens/printTarget.ts:224-230 (the report branch returns { kind:"report", slug, label } with no `query`) and :250-25 … | ✅ **FIXED (L7) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D35 | 42 | HIGH | The TDS / TCS Register advertises "Ctrl+P Print" on its button bar and the key is dead: TDSRegisterScreen imports only useExportAction, never usePrintAction, so Ctrl+P makes no HTTP call and answers the shell's generic "does not apply on this screen" instead of the server's 501 sentence that printTarget.ts deliberately mapped th … | frontend/src/screens/reports/TDSRegisterScreen.tsx:7 and :112 (useExportAction only; no usePrintAction anywhere in the file) vs fr … | ✅ **FIXED (L7) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D36 | 5 | HIGH | There is NO keyboard path to an opening balance, an opening Dr/Cr, a bill-wise opening breakup, or maintain_bill_by_bill on ANY ledger. The Alt+C master-create frame renders exactly two inputs (Name, Under (group)); the MASTER_FIELDS 'details' cursor position has no control behind it; and no screen anywhere calls setLedgerOpenin … | frontend/src/screens/masters/MasterCreateScreen.tsx:66-69 (`await client.createLedger(companyID, { name: n, group_name: p })` — th … | ✅ **FIXED (L13) — MUTATION-PROVED *AND* DRIVEN 2026-08-21.** All four keyed from the keyboard and read back through the OUTSTANDINGS REPORT, the product's own reader of `ledger_opening_bill`. See §7.5 |
| D37 | 5/30/34/35/37 | HIGH | Past 64 WebAuthn credentials EVERY WRITE in the product becomes permanently impossible on every device: assert/begin returns an unbounded allow_credentials list, Chrome throws RangeError above 64, and the client only fell through to enrolment on NotAllowedError. Reported INDEPENDENTLY BY FIVE AGENTS; it blocked this audit for ~2 … | auth_webauthn.go:181-184 + webauthn/store.go ByUser (ORDER BY created_at, no LIMIT) + auth/webauthn.ts:352-362 | ✅ FIXED (F1) |
| D38 | 9 | HIGH | The cost-allocation picker cannot be reached from the keyboard at all: on the particulars grid, Tab, Shift+Tab and Enter move nothing, so `alloc:0-add`, the axis/centre selects and the `rest` button are MOUSE-ONLY — while the hint strip on that screen advertises "Enter: Next field" and "Tab / Shift+Tab: Field". | frontend/src/keystroke/useNavMachine.ts:54 | ✅ **FIXED (L12) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D39 | 9 | HIGH | Ctrl+A on a voucher whose cost split is INCOMPLETE on one axis creates every ledger the operator named BEFORE the server refuses, leaving an orphan account in the chart of accounts with no voucher behind it. Reproduced on BOTH grids (single entry and the Journal's double entry). Recon prediction 1 CONFIRMED. | frontend/src/screens/VoucherScreen.tsx:1057 | ✅ **FIXED (L6) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D40 | 17 | MEDIUM | Non-existent challan sub-routes answer 403 "unmapped route: access denied" rather than 404/405, so "this route was never built" is indistinguishable from "your level is not permitted". This KILLS the recon's literal proof that PATCH returns 405. | backend/internal/server/auth_middleware.go:967 | ✅ **FIXED 2026-08-20 — MUTATION-PROVED (3 ways, incl. the DANGEROUS direction) *AND* DRIVEN ON THE WIRE.** Authorized by the user. See §7.5 |
| D41 | 17 | MEDIUM | LEAD, not row 17: opening an existing Purchase voucher for alteration from the Day Book and pressing Ctrl+A created a new ledger ("Purchases") over the wire and then failed the voucher write with "Error: invalid gst line gst_rate_pct". A refused alter left a master behind. | frontend/src/screens/VoucherScreen.tsx:1462 | ✅ **FIXED (L6) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D42 | 24 | MEDIUM | F11 Features asserts a capability the build does not have: the "Negative stock" row is labelled "Always on" and its note says "A company can opt in per-company", while the screen offers no toggle and nothing on it can write the flag. The product's own statement of what is switchable is false. | frontend/src/screens/FeaturesScreen.tsx:291-293 (state "always" renders as "Always on" via the map at FeaturesScreen.tsx:486; the … | ✅ FIXED (own) |
| D43 | 24 | MEDIUM | POST /v1/companies accepts `"allow_negative_stock": true` with 201 Created and silently discards it — the caller gets a success for a setting that was never applied. | backend/internal/server/server.go:1188-1240 (handleCreateCompany / decodeJSON tolerates the unknown field) with backend/internal/s … | ✅ CLOSED L15 2026-08-22 |
| D44 | 27 | MEDIUM | The Sales/Purchase Order Book flags NOTHING as overdue in its default (no as-at) state, because Overdue() short-circuits to false when asOn is empty — so the view an operator actually opens reports zero late deliveries. | backend/internal/reporting/orderbook.go:159 (the `// asOn == ""` guard at :160) | ✅ **FIXED (L8) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D45 | 29 | MEDIUM | With no period set, the Stock Summary prints "As at today (everything posted)" over a report that is actually unbounded — it includes post-dated vouchers. On 2026-08-07 the screen showed 72,837.15 under that label where the real as-at-today position is 96,168.18. The Balance Sheet has the same behaviour and prints no date at all … | frontend/src/screens/reports/StockSummaryScreen.tsx:110 (`{asOn ? `As at ${asOn}` : "As at today (everything posted)"}`); the Bala … | ✅ **FIXED (L10) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D46 | 29 | MEDIUM | The Balance Sheet's "Profit & Loss A/c (Current Period)" line ignores F2's From date while the P&L honours it, so the same F2 setting yields 1,16,237.15 on one statement and -37,911.02 on the other, and the Balance Sheet's caption still says "(Current Period)". | backend/internal/reporting/reporting.go:975-977 (`openingBS := decimal.Zero; stockAdj := closing.Sub(openingBS); bs.NetProfit = in … | ✅ **FIXED (L10) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D47 | 29 | MEDIUM | A master creation that the server refuses is reported into a screen that has already been unmounted: Ctrl+A on Create ledger returned the operator to the Gateway with no message while POST /ledgers answered 403 and nothing was created. | frontend/src/screens/masters/MasterCreateScreen.tsx:103-120 (onAccept calls onSelect(n) synchronously, then `create(n, p).catch((e … | ✅ **FIXED (L13) — MUTATION-PROVED *AND* DRIVEN 2026-08-21.** Server refusal, client refusal and the inline `Alt+C` refusal all reach the shell's status line after the frame pops. ⚠️ Driving found the refusal was a RAW POSTGRES ERROR; fixed in the same lane. See §7.5 |
| D48 | 30 | MEDIUM | A FAILED Ctrl+A still writes to the chart of accounts. resolveDraftLedgers ensures the party ledger and every null posting ledger via real POST /ledgers calls BEFORE the voucher POST is attempted, so an accept the server then rejects leaves an orphan master behind — here 'Sundry Creditor' under 'Sundry Creditors'. | frontend/src/screens/vouchers/draftAccept.ts:274-320 (resolveDraftLedgers: ensureLedger for the party, then each posting) then :33 … | ✅ **CLOSED — audited at HEAD 2026-08-23 (L16 position 0). The draft path discloses its orphans, and the ORDERING now goes further than this row asked.** (1) DISCLOSURE: `postDraftBody` snapshots the chart BEFORE `resolveDraftLedgers`, computes `ledgersMintedByThisAccept(body, resolved, chart)`, and on a refused POST throws `DraftAcceptFailedError` NAMING them — "…had already created 2 new ledger masters (X, Y), which now have no voucher against them. Retrying this accept REUSES them… There is no route that deletes a ledger". Guarded at HEAD by BOTH arms: thrown when masters were minted, and `rejects.not.toBeInstanceOf(DraftAcceptFailedError)` when none were. (2) ⛔ **AND THE RACE LOSER NOW MINTS NOTHING AT ALL** — L15's layer-5 posting claim is taken BEFORE `resolveDraftLedgers` **deliberately, for this row's reason**, documented at the site: "A claim taken after it would mean the loser of a race still left orphan masters behind for a bill it never posted". ⛔ **THIS ROW WAS SCOPING D81** (see §7.1 D81): the draft path is genuinely covered, so **D81 is exactly the VOUCHER-path half and nothing else**, and `DraftAcceptFailedError` + the claim ordering are the two shapes it copies. ⚠️ Anchors `:274-320`/`:33…` are stale — `resolveDraftLedgers` is at `:427`, the mint-and-name block at `:1694-1717` |
| D49 | 30 | MEDIUM | insightd has no authentication and scopes OCR drafts by the caller-supplied company_id alone, and vite proxies it same-origin at /companies/*. Any company_id lists and fully hydrates another tenant's drafts — supplier names, invoice numbers, dates, posting ledger names, amounts and GST state codes. | ✅ DONE — cored is the only door. `backend/internal/server/insightd_proxy.go` (the six proxy routes) · the `/drafts` arms + `isDraftPath` in `auth_middleware.go` · six `r.Get/r.Post` lines in `server.go`'s `{companyID}` subtree · `maxDraftUploadBody` + its `bodyCapFor` arm · `insightd/insightd/api/authn.py` (`require_proxy`, `require_service_token`) · `routes_drafts.py`'s router dependency · `app.py` (`openapi_url=None`, the healthz disclosure, the guarded `/benchmark/last`) · `config.py` `service_token` · `api/http.ts` `setSessionFetch` installed by `App.tsx` · `api/insightd.ts` + `draftAccept.ts` `draftPath` under `/v1` · `vite.config.ts` (the `/companies` rule DELETED) · `Makefile` `INSIGHTD_SERVICE_TOKEN` | ✅ **CLOSED L20 2026-08-24 — Option B, cored proxies insightd.** The recommendation on file, re-verified at HEAD rather than trusted: cored's dev issuer mints an EPHEMERAL Ed25519 key and publishes no JWKS, insightd's core deps carry no crypto library at all, and a signature proves *who* and never *which company* — so validating a token inside insightd needed a second copy of cored's PDP in another language. Measured before the build: **8 application routes + 4 auto-docs = 12**, `securitySchemes: None`, and an anonymous `GET :8088/companies/{co}/drafts` returned **15 drafts in full** while cored answered 401 for the same tenant. Now every company-scoped route needs cored's service credential AND the company cored's PDP authorized (`X-Insightd-Company` must equal the path), `/benchmark/last` needs the credential, and the four auto-docs are unmounted. ⛔ **The six routes map onto the EXISTING `purchase` voucher-class token, so no migration and no pre-granted resource** — migration 0011 already grants Data Entry create/display on it, and 0077 stays free for D94. Drive `drive-l20-d49.mjs` **52/52**, including the two arms that prove the row was understood: the CLAIM still reaches insightd (200, read back out of band) with the 409's `detail.code` intact — because all four insightd-backed layers of the duplicate-bill guard fail OPEN on a non-409, so a gate asserting *the accept still works* would be green while a money defect re-opened — and `disallow display purchase` hides the inbox while `disallow create purchase` leaves it readable and refuses the claim. See §7.5 **L20** |
| D50 | 31 | MEDIUM | A cross-tenant question is not refused. Naming another company by name, by UUID, or asking for "every company" / "all tenants" all return 200 with the caller's OWN table and no statement that the named tenant was ignored. The data is genuinely safe — the compiled SQL carries no company predicate and scoping rests on FORCE RLS pl … | backend/internal/query/translate.go:113-183 (keyword switch; no entity/party/company recognition and no refusal branch — the fallt … | ✅ **FIXED (L5) — MUTATION-PROVED *AND* DRIVEN 2026-08-21.** ⚠️ Three arms refuse (every/all-tenants/UUID); a company named by an UNENUMERATED NAME is still ANSWERED, under the unconditional scope caption — by design, see §7.5 |
| D51 | 31 | MEDIUM | "total sales" sums vv.taxable_value only. It excludes the GST component of every invoice and excludes credit notes entirely, and the screen discloses neither — so the figure matches no report: 156500.0000 answered, against a net sales-ledger position of 144500.0000 (after a 12000.0000 credit note) and an invoice total of 183080. … | backend/internal/query/translate.go:148-159 (metric sum(taxable_value), filter voucher_type_name = 'Sales' — Credit Note is a sepa … | ✅ **FIXED (L5) — MUTATION-PROVED *AND* DRIVEN 2026-08-21.** 144500 = the P&L Sales line, against 156500 for the old taxable-value shape. See §7.5 |
| D52 | 32 | MEDIUM | FeaturesScreen advertises "Cash-flow projection and anomaly detection" as state "on" with a note that names no door, while the two AI-native rows immediately above it in the same group name theirs ("Gateway > Bill Inbox", "Gateway > Query"). The capability list overstates what an operator can reach. | frontend/src/screens/FeaturesScreen.tsx:340-354 — :342 note "Gateway > Bill Inbox", :347 note "Gateway > Query", :353 note "Determ … | ✅ **CLOSED 2026-08-23 (L16 position 1), in the SAME COMMIT as D22 — a door without the sentence leaves the note naming no door, and the sentence without the door is worse.** The one row became TWO, each naming its real door (`Alt+G > Cash Flow` / `Alt+G > Anomalies`), matching the form its two AI-native neighbours already used. ⛔ **THE DRIVE'S ARM IS THE POINT OF THE ROW: read F11's note, then FOLLOW ITS OWN INSTRUCTION from the keyboard.** A note that names a door is only true if the door opens, and both do. The old note's `"optional statistical forecast"` clause is GONE (it advertised D96, which has no renderer). ⚠️ **A PINNED COUNT MOVED:** the Alt+E row's "all NINE reports in the Alt+G list" is now ELEVEN — and the four items it enumerated afterwards (Stock Summary, TDS/TCS register, both order books) were ALREADY members of that same list, so the sentence had been double-counting. ⚠️ **AND ONE CLAIM I WROTE INTO IT WAS FALSE AND WAS CHECKED BEFORE SHIPPING:** I added "the Bank Reconciliation Statement" to the exportable list, grepped `useExportAction`, and no BRS screen registers it — removed rather than shipped |
| D53 | 34 | MEDIUM | The Stock Summary refusal hardcodes the words 'the Data Entry level', so a holder of any OTHER level is told a level they do not hold is the one denying them, and is pointed at the wrong level's rules. | frontend/src/screens/reports/StockSummaryScreen.tsx:175-183 (the status===403 branch of StockSummaryError): the literal 'so the Da … | ✅ **FIXED (L10) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D54 | 37 | MEDIUM | The refusal names the rule but never the cutoff DATE, so the operator learns a period is locked without learning where the boundary is. | backend/internal/authz/authz.go:270 (`deny("voucher date is before the back-dated cutoff")`), surfaced verbatim at frontend/src/sc … | ✅ FIXED (F3) |
| D55 | 38 | MEDIUM | There is no way to unlock a locked account: no HTTP route, no key, no screen. The only member-admin surface (Users & Roles) renders no lock state at all, and the one route that could plausibly be a remedy (POST /auth/password) has no caller anywhere in the app, so a user cannot even change their own password. | backend/internal/authn/lockout.go:91-96 (RecordSuccess is the sole ClearLock caller); backend/internal/server/auth_login.go:206-21 … | ✅ **FIXED (L9) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D56 | 38 | MEDIUM | Locking an account does not revoke the sessions it already holds: a locked subject's existing bearer keeps working for the full 3600-second token TTL, including authenticated WRITES. | backend/internal/server/auth_login.go:206-217 (IsLocked is consulted only on POST /auth/login) and loginTokenTTLSeconds (the 200 b … | ✅ **FIXED (L9) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D57 | 38 | MEDIUM | The lockout threshold, window and duration are hardcoded constants with no configuration path at all, so "N is the configured N" cannot be satisfied — N is a compile-time 5, and there is no per-deployment or per-company setting, nor the exponential backoff the project's own design docs promise. | backend/internal/server/server.go:231 (authn.NewLocker(authn.DefaultLockoutPolicy(), …) — the policy is constructed inline from a … | ✅ **FIXED (L9) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D58 | 38 | MEDIUM | The lock message hands the operator a raw UTC RFC3339 instant — "Try again after 2026-08-07T23:09:07Z" — with no conversion to local time and no duration, in a product whose audience is Indian accountants on IST (UTC+5:30). | frontend/src/auth/LoginScreen.tsx:38-44 failureText() interpolates `f.until` straight into the string; the value comes from AuthLo … | ✅ **FIXED (L9) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D59 | 40 | MEDIUM | The printed Receipt and Payment vouchers name the BANK as the counterparty — "Received From: HDFC Bank" / "Paid To: HDFC Bank" — because the single-entry form puts the bank in both `party` and `party_ledger_id` and printingdb builds the Party block from party_ledger_id. | frontend/src/screens/VoucherScreen.tsx:1036-1043 (party = accountName, req.party_ledger_id = f.partyLedgerId, where the Account fi … | ✅ **FIXED (L6) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D60 | 40 | MEDIUM | A Sales invoice keyed through the GST panel saves happily and then cannot be printed: the server refuses with 400 because items[0].hsn is a Rule 46(g) mandatory particular, but the voucher form never requires the HSN/SAC field to save. | frontend/src/screens/VoucherScreen.tsx:2063 (gst-hsn is an optional FieldRow in GstPanel) vs backend/internal/printing (validateVo … | ✅ **FIXED (L6) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D61 | 42 | MEDIUM | The printed Day Book's footer — the only thing on the page explaining why there is no amount total — is CLIPPED mid-sentence to "20 voucher version(s) in this period. No column total is...", and the sentence it was clipped from is factually false for this build: the report lists exactly one row per live voucher, not an original … | backend/internal/server/handler_print_reports.go:546-551 (the Footer row and its "DELIBERATELY NO COLUMN TOTAL" comment) against b … | ✅ **FIXED (L7) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D62 | 42 | MEDIUM | The printed Stock Summary and the on-screen Stock Summary are different reports: the PDF has ten columns including Opening Value and Inward Value, the screen has seven and puts a QUANTITY under "Inward" immediately beside a MONEY figure under "Outward (at cost)". | backend/internal/server/handler_print_reports.go:700-712 (ten Column headers) and :662-680 (ten cells per row) against frontend/sr … | ✅ **FIXED (L7) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D63 | 42 | MEDIUM | printTarget's Bank Reconciliation refusal ("Select a bank account first…") cannot be reached from the keyboard — the screen auto-selects the first bank account on open — so Ctrl+P as the very first key already prints a statement for an account the operator never chose, which is the exact outcome the refusal was written to preven … | frontend/src/screens/printTarget.ts:243-249 (the refusal, gated on !voucher.bankLedgerID) against frontend/src/screens/BankReconci … | ✅ **FIXED (L7) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D64 | 43 | MEDIUM | The Stock Summary export is blind to inward VALUE: the API returns inward_qty, inward_value, outward_qty and outward_value and the sheet writes only inward_qty (under a header reading "Inward") and outward_value (under "Outward (at cost)"), so two adjacent columns mix a quantity with money and the inward money figure — 7,860.00 … | frontend/src/screens/reports/StockSummaryScreen.tsx:276-288 and :290-300 (cells: exactCell(it.inward_qty) then moneyCell(it.outwar … | ✅ **FIXED (L10) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D65 | 5 | MEDIUM | Outstandings folds in ledger_opening_bill unconditionally, with none of the books-beginning-from guard the Balance Sheet applies to ledger.opening_balance. Asked for a date before the books began, Outstandings reports the entire opening ageing (Acme 1,85,480.00 incl. OB/ACME/24-25/07 and /11; Steel Suppliers 80,450.00 incl. OB/S … | backend/internal/reporting/outstanding.go:151-153 — the `UNION ALL SELECT ob.ledger_id, ob.ref_name, ob.dr_cr, ob.amount, ob.due_d … | ✅ **FIXED (F8)** — the opening breakup is `queryBalances`-guarded, so a date before the books began reports nothing on EITHER report |
| D66 | 9 | MEDIUM | The 422 the operator actually reads names the cost category by RAW UUID and prints the 0-based `posting_seq` — the one number CostAllocationPicker.tsx:28-33 declares no screen in this product may ever display. The server code's own comment claims it 'names the axis'. | backend/internal/ledger/cost_allocation.go:294 | ✅ **FIXED (L12) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D67 | 9 | MEDIUM | The picker reports an axis as "allocated in full" when one of its splits has an amount but no cost centre chosen; `buildCostAllocations` then drops that split, and Ctrl+A sends a short axis to the server. Recon prediction 3 CONFIRMED exactly. | frontend/src/screens/vouchers/costAllocation.ts:163 | ✅ **FIXED (L12) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D68 | 27 | LOW | The Alt+X explosion's first note prints the recipe's YIELD where the component phrase belongs, so it reads "3 of SS Sheet 2mm yields 3" — the finished good is named on the consuming side of "yields". | backend/internal/orders/bom.go:374 (args at :377-378) | ✅ **FIXED (L8) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D69 | 27 | LOW | An order line's due date is accepted when it falls BEFORE the order date, creating a commitment that is overdue the instant it is recorded. | backend/internal/orders/order.go:171 (the per-line loop validates StockItemID, Qty and Rate and never looks at DueDate) | ✅ **FIXED (L8) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D70 | 27 | LOW | After Alt+W pre-closes an order, the register's line panel keeps showing that order's balance as Outstanding with no pre-closed marker, while the row itself has vanished from the list above it. | frontend/src/screens/OrderRegisterScreen.tsx:1039 (the detail heading) and :1069-1073 (the Outstanding cell) | ✅ **FIXED (L8) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D71 | 27 | LOW | The Purchase Order Book's quantity column is hard-coded "Delivered" for goods a supplier owes the company, while the purchase register calls the identical figure "Received". | frontend/src/screens/reports/OrderBookScreens.tsx:351 (and the export header at :499) | ✅ **FIXED (L8) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D72 | 32 | LOW | The optional statistical forecast in insightd is unreachable from the client and carries money as JSON floats rather than the fixed-4 strings the rest of the system pins, so wiring it as-is would reintroduce paisa loss. It does at least declare estimate:true and its method — the disclosure cored's own projection lacks. | insightd/insightd/api/routes_forecast.py:29-36 (weekly_net_cash: list[float], daybook: list[tuple[str, float]]) and :13-16; no cal … | ✅ **CLOSED 2026-08-21 — THE MONEY HALF WAS ALREADY FIXED, and this row was stale.** `ForecastRequest` takes `list[Money]` (fixed-4 decimal STRINGS) on both fields, the module docstring states the rule at length, and `tests/test_forecast_api.py` gates it WITH a non-vacuity arm (`test_a_float_amount_is_REFUSED_rather_than_silently_narrowed`). Mutation-proved: reverting both fields to `list[float]` turns that arm RED. ⛔ **A BUILDER WHO READ THIS ROW AND NOT THE CODE WOULD HAVE CONVERTED Money STRINGS BACK TO FLOATS**, which is why it is struck rather than left. The surviving clause — still not reachable from the keyboard — is **D22's**, not a defect of its own; the docstring says so at the field. |
| D73 | 38 | LOW | Sign-in attempts made while an account is locked are never audited: handleLogin returns 423 from the IsLocked branch without calling RecordAttempt, so the traffic during the lock window — the exact period an investigator cares about — leaves no trace. | backend/internal/server/auth_login.go:209-217 — the IsLocked branch writes writeLocked and returns; RecordAttempt is reached only … | ✅ **FIXED (L9) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D74 | 38 | LOW | app_user.failed_attempts is not a running counter: it stays 0 through failures 1-4 and is written only at the instant the lock trips, so nothing can observe that an account is partway to being locked. | backend/internal/authn/lockout_store.go:113-118 — failed_attempts is written ONLY inside SetLock (`UPDATE app_user SET locked_unti … | ✅ **FIXED (L9) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D75 | 40 | LOW | Every printed voucher's download filename appends the raw voucher UUID after the class and number, contradicting pdfFilename's own doc comment, which says it uses the FIRST non-empty part. | backend/internal/server/handler_print.go:117 (writePDF(w, pdfFilename(pathClass, number, voucherID.String()), pdf)) against the co … | ✅ **FIXED (L7) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D76 | 43 | LOW | Ctrl+P on the Cost Centre Summary tells the operator "Alt+E is not built here either; read it on screen", which is false: pressing Alt+E on that same screen immediately writes Cost-Centre-Summary-Branch-2026-04-01-to-2027-03-31.csv with correct 4-dp figures. | frontend/src/screens/printTarget.ts:276-279 (the refusal string ending "Alt+E is not built here either; read it on screen.") again … | ✅ **FIXED (L7) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** See §7.5 |
| D77 | 9 | LOW | The voucher read-back orders postings `ORDER BY p.dr_cr DESC, p.amount DESC` with no `p.id` tiebreak, so two legs on the same SIDE for the same AMOUNT come back in an arbitrary order — the analogous ordinal elsewhere in the codebase uses `ORDER BY p.id`. ⛔ **NOT SYNTHETIC: the CGST and SGST legs of every intra-state supply tie** (18% on 10,000 is 900 credit and 900 credit), so the exposure is the ordinary shape of the most common document in the product. **THREE sites share the projection.** | `backend/internal/ledger/ledger.go` GetVoucher's postings + `internal/reporting/daybook.go` loadPostings + `internal/printing/printingdb/document.go` loadLegs | ✅ **FIXED (L14b) — MUTATION-PROVED *AND* DRIVEN ON THE WIRE 2026-08-22, 8/8.** `, p.id` at all three: GetVoucher's comment promises "same projection and ordering as the day book's loadPostings", so tie-breaking one and not the other would make a STATED invariant false. **Oracle: a HEAP REWRITE** — plan toggles, `work_mem` and `VACUUM ANALYZE` all leave the untied order alone (the read is an index scan, so ties come back in physical order and the sort preserves it), but `CLUSTER` / `pg_repack` — routine maintenance — moves it. The probe runs the OLD query itself as raw SQL in both heap states and REQUIRES them to differ, because whether a rewrite reorders a given pair is a coin toss and without that precondition every arm passes on a broken build. ⛔⛔ **AND IT IS A PRESENTATION-ORDER DEFECT, NOT A MONEY ONE — the mutation proof KILLED the claim that it misattributes a cost split.** `posting_seq` looked positional, but `GetVoucher` builds `postingSeqByID[pid]` while walking the same read and joins allocations by POSTING_ID, so the seq is re-derived to name the same posting however the array is ordered: with the fix reverted and the array demonstrably reordered, the split arm stayed GREEN. Kept as a labelled CONTROL. ⚠️ **`document.go` is the honest exception** — its ORDER BY already carried the ledger NAME, and a leg tying on side+amount+name also ties on ledger_id and group (names are unique per company), so the rows are identical in every projected column and no byte can move; `p.id` makes its "chosen to be total" comment TRUE (it was FALSE for legs to the same ledger) and changes no output. ⚠️ Buys DETERMINISM, not the input order: no ordinal column, and `uuid.NewV7` is not monotonic within a millisecond. See §7.5 |
| D78 | LEAD 1b | LOW | MembersScreen's error banner is a plain <p> with no role="alert", so the refusal that DOES render is never announced to assistive technology. | frontend/src/screens/MembersScreen.tsx:658-662 (the `error ? <p data-testid="members-error" className="... text-red-400">` block); … | ✅ FIXED (F5) |
| **D79** | 17 | HIGH | ⛔ **An accounting-mode ALTER RE-POINTED THE VOUCHER'S INCOME/EXPENSE HEAD — DATA LOSS, not an orphan master.** `buildGstRequest` always `ensureLedger`d the orientation's canonical base, so altering a purchase booked to any other head silently moved it. Driven: `Dr Raw Material Purchases 30000` became `Dr Purchases 30000` on an alter that edited NOTHING, and a `Purchases` ledger was minted to receive it. The voucher still balances and the tax is unchanged, so no trigger, total or server check disagrees — the company's expense analysis simply changes because somebody LOOKED at a voucher | `frontend/src/screens/VoucherScreen.tsx` `buildGstRequest` (the unconditional `ensureLedger(orientation.baseLedger)`) | ✅ **FIXED (L6) — MUTATION-PROVED *AND* DRIVEN 2026-08-20.** `baseLegFromView` hydrates the head; the canonical base is now the fresh-create fallback only. See §7.5 |
| **D80** | 9 | MEDIUM | `Alt+G > Vouchers` is broken and always has been: `canonicalReportName("Vouchers")` returns undefined, the `??` falls back to the raw label, and the Go To pushes the **Unwired** screen. ⛔ **It is the FIRST entry `GOTO_ENTRIES` offers** (`["Vouchers", ...masters, ...REPORT_NAMES]`) and the only one that never worked — invisible because the Gateway's own drill has always been correct for the identical label. | `frontend/src/keystroke/navMachine.ts` `resolveGotoSelection` | ✅ **FIXED (L14c) — MUTATION-PROVED *AND* DRIVEN IN A BROWSER 2026-08-22, 17/17.** One arm before the report fallback, mirroring the gateway arm exactly. Destination is DOCUMENTED not chosen: `RESEARCH.md:903` — *"Alt+G > **Create Voucher** > Ctrl+F4"* — a create-voucher FORM, not the Day Book. **Oracle: the ABSENCE of "is not available in this build" PLUS a real form, and the Alt+G door landing on the SAME SCREEN as the Gateway door** (a fix that invented a different voucher frame would pass every other arm). ⛔⛔ **THE GUARD WRITTEN TO MAKE REACHABILITY STRUCTURAL IS WHAT LET IT SHIP:** `masterGoto.test.ts`'s "GOTO_ENTRIES offers exactly what the machine can route" contained `entry === "Vouchers" ||` — a STRING COMPARISON against labels believed routable, not a check that the machine routes them, so the one dead entry was certified reachable by the test whose whole purpose was to prevent that. Now every entry outside the machine's own routing tables is DRIVEN. ⚠️ `replaced` is deliberately NOT set and that is PINNED as a decision (drive Ctrl+G > Vouchers > Alt+C: `canReturnToVoucher` must be true AND Esc must really return) — on the depth-2 stack the flag is unobservable, so an assertion there would be vacuous. See §7.5 |
| **D81** | 17 | MEDIUM | A server-only refusal (cutoff, ACL, duplicate number, negative stock) still leaves **masters** behind on the voucher path: a posting needs a real uuid and cored has no `DELETE /ledgers`. The DRAFT path discloses its orphans; the voucher path did not | `frontend/src/screens/VoucherScreen.tsx` (discloseOrphanedMasters) | ✅ CLOSED L18 2026-08-24 — the refused accept now NAMES every master it minted, from a chart snapshot taken BEFORE the builder runs; one sentence shared with the draft path (`voucherAcceptFailure`). 12/12 browser + wire, 2 mutations. ⚠️ The row's second half — "and throws away the keying" — DOES NOT REPRODUCE and was closed as such by the user: the form is component state and the frame replacement never cleared it. See §7.5 L18 |
| **D82** | 17 | LOW | The HSN refusal bites the ALTER path: an old sales invoice stored without an HSN cannot be re-saved without keying one. Arguably correct (it cannot be printed either way) but it is a new refusal on existing data | `frontend/src/screens/VoucherScreen.tsx` `HSN_REQUIRED_CLASSES` | ✅ **WORKING AS INTENDED — THE USER'S DECISION, 2026-08-21. `HSN_REQUIRED_CLASSES` STAYS AS IT IS.** The invoice is unprintable without an HSN (CGST Rules 2017, Rule 46(g)), so keying the code is the FIX rather than a hoop. ⛔ A later session that finds this row and "fixes" it is REVERSING A USER DECISION — the SENTENCE is pinned, not just the behaviour (`VoucherScreen.gstAlter.test.tsx`, mutation-proved: disabling the condition turns this pin AND `masterGate.test.tsx`'s D60 arm red). **Driven (L14) 2026-08-22, 12/12:** the refusal reaches the operator on the alter naming Rule 46(g) and "cannot be printed", the HSN field sits empty and keyable on the SAME screen, nothing is sent, and keying the code lets the same alter through as a PUT. ⛔ **AND THE DRIVE FOUND D89** — the remedy this refusal prescribes was minting a duplicate invoice. See §7.5 |
| **D83** | 5 | LOW | Two SIMULTANEOUS posts with the same hand-typed bill number on one party can both pass the uniqueness check. Needs an index `bill_allocation` cannot carry, because duplicate names are legal for AGAINST/ON_ACCOUNT/caller-named openers. ⚠️ **The sequential case — what an operator actually produces — is closed** | `backend/internal/ledger/` bill uniqueness | ✅ **CLOSED L25 2026-08-24 — an ADVISORY lock on the NAME, because there is no row to lock.** Measured on the wire first try: two posts in flight together, both 201, and `GET /outstandings` showed **ONE bill of 3000.0000 aged at the EARLIER due date** — the second instalment's amount and its own due date gone, with nothing to say so. ⛔ **THE REGISTER'S PROPOSED UNIQUE INDEX CANNOT EXPRESS THE RULE:** duplicate ref names are LEGAL for AGAINST, ON_ACCOUNT and caller-named openers (that is how Tally adds to an existing bill), so an index cannot tell the names the SERVER invented from the ones a caller supplied. And there is no ROW: the collision is between two names neither transaction has written. `lockGeneratedBillRefStems` takes `pg_advisory_xact_lock` on each (company, ledger, stem) BEFORE the has-any-other-voucher check, so the loser reads the winner's committed row and takes the RENAME branch it would have taken sequentially. ⚠️ **THE FIRST `pg_advisory_*` IN THIS CODEBASE**, keys SORTED so two posters cannot deadlock, and `_xact_` scoped so it is still held while the rows are written. ⛔⛔ **THE DRIVER PREDICTED THE MUTATION WOULD NOT REPRODUCE AND IT DID, 10 RUNS OUT OF 10** — the difference from D90 is a BARRIER that makes the overlap constructed rather than hoped for. ⚠️ And it failed on the NAMES check, not the row count: both allocation rows exist, sharing one name, and the report groups by (ledger, ref_name). Driven **9/9**, own tenant, reaped. Full record: §7.5 **L25**. |
| **D84** | — | MEDIUM | ⛔ **`api/mock.ts` IS LOOSER THAN CORED AND IS A SOURCE OF FALSE GREEN — three lanes hit it.** 61 test files build their masters through it, and `App.tsx`'s dev-client switch hands the same mock to a real operator in the dev browser, so all of them were measured against an engine that enforces less than the server. Needs a STRUCTURAL mock-vs-server gate, not three patches | `frontend/src/api/mock.ts` + `corpora/mock-vs-cored/` | ✅ **FIXED (L14d) — MUTATION-PROVED 2026-08-22. Runner A 14/14 in vitest, runner B 11/11 against live cored.** The instrument: `corpora/mock-vs-cored/masters.json` (refusal-shaped rows), `src/api/contract/corpus.ts` (ONE reader, so the two runners cannot drift on what a row means), runner A in vitest over `createMockClient()`, runner B as `pnpm contract:cored` over live cored. **Ran as the row prescribed — both runners BEFORE any fix, divergences printed side by side: 9 rows where the mock ACCEPTED a body cored refuses.** Eight closed, mirroring cored's SENTENCES not just its statuses. ⛔⛔ **EVERY EXPECTATION WAS MEASURED ON THE WIRE AND TWO CONTRADICTED THE RECON:** an unknown group answers **404** (not 422), and **a duplicate STOCK ITEM name is ACCEPTED** (201, new id) — the recon's `EnsureStockItem`-does-not-exist premise is true but its conclusion was not. ⛔ **THE TIGHTENING FOUND FIVE TESTS THAT COULD NEVER HAVE RUN AGAINST CORED:** `draftAccept.test.ts`'s `realDraftBody` called `createLedger` unconditionally, so the passing tests were measuring a chart of accounts with DUPLICATE party ledgers — the exact state the product's `ensureLedger` was fixed TWICE to avoid. ⚠️ **BLAST RADIUS MEASURED, NOT FEARED:** the split criterion was ">~10 files from one arm"; the first arm broke **ZERO** and all eight broke **ONE** file. Not split — but the premise ("tightening turns green tests red — that is the deliverable") only half held: most of the value is preventing FUTURE false green. ⚠️ **ONE ROW IS A PINNED GAP, NOT A FIX** — `ledger-unknown-group`, because the mock models no chart of accounts and copying cored's 28 seeded groups would be an undetectable duplicate (no `listGroups` for either runner to compare). Runner A asserts the gap is STILL OPEN so closing it is deliberate. See §7.5 |
| **D85** | 30 | MEDIUM | ⛔ **The duplicate-supplier-bill guard keys on the POST-REDACTION `voucher_no`, so every PII-shaped invoice number collapses to ONE key and a legitimate second bill is refused.** `assertNotAlreadyOnTheBooks` matches on `body.voucher_no`, which for a bill whose invoice number is a PAN/GSTIN/Aadhaar is the constant token `[PAN REDACTED]` — not the supplier's number. Two DIFFERENT PAN-shaped invoice numbers from one supplier on one date are therefore the same bill as far as this check is concerned, and the second accept is refused. Its own error text advises *"If this is genuinely a second bill, key its own supplier invoice number"* — **which cannot work**, because any number that redacts lands on the same token. Found by driving, not by reading: the L11 probe randomised its PAN to become re-runnable and was still refused. | `frontend/src/screens/vouchers/draftAccept.ts:1526-1545` (`assertNotAlreadyOnTheBooks` — `no = body.voucher_no`, compared to `e.voucher_no`) + `insightd/insightd/pipeline.py:65` (redaction happens BEFORE the value ever reaches the client) | ✅ **CLOSED L21 2026-08-24 — the discriminator went into the value that PERSISTS, and the row was SMALLER than three handoffs predicted.** `redaction/persistence.py`'s `placeholder()` now ends the tag in a short UNSALTED fingerprint of the removed value (`[PAN REDACTED 6442]` vs `[PAN REDACTED 0413]`), computed from `m.value` inside `scrub()` where the raw number already is. ⛔ **NO new draft-body field, NO wire-shape change, NO `draft.ts` mirror and nothing near cored's byte-for-byte 409 passthrough** — the driver and the recon both expected a hash carried ON the draft, and the day book has no field to carry one, so that design could not have closed this layer at all. ⛔ **UNSALTED is load-bearing and is the OPPOSITE choice from `boundary.py`:** the same bill re-uploaded must redact to the same `voucher_no` or layer 2 stops seeing the duplicate. ⚠️ **The register's own claim that it moves pinned numbers is FALSE** — the fingerprint changes the tag STRING, not the COUNT of matches, and the whole insightd suite was green before AND after (161 → 161, nothing moved), so the gate had to be WRITTEN rather than re-pinned. ⛔ **The advice changed with the key too:** the day-book refusal no longer advises a remedy that cannot work, and one `secondBillAdvice()` now names whether the memo layers refused on the DRAFT or on the body's CONTENT. 3 mutations — two corrected the TESTS (an arm written for the salting hazard passed through it in-process; it is cross-process now) — and **16/16 on the wire and in a browser**, both halves of the oracle, footed against Outstandings. Full record: §7.5 **L21**. |
| **D86** | 3 | MEDIUM | ⛔ **Sundry Creditors on the Balance Sheet and the payables Outstandings DISAGREE the moment any voucher is dated after today, and the repo's own gate asserts they agree EXACTLY.** With no `as_on`, Outstandings bounds at today and drops a future-dated bill; the Balance Sheet does not bound and includes it. Measured: one bill dated 2026-08-27 with today at 2026-08-20 gave BS **109950.0000** vs Outstandings **108770.0000** — short by exactly that bill's 1180.0000 — while `?as_on=2026-09-30` returned **109950.0000** and agreed. Nothing refuses a future-dated purchase (cored answers 201), so an operator who post-dates one bill sees the ageing disagree with the balance sheet with no explanation. ⚠️ `verify:demo`'s check passes today only because the seeded demo data happens to carry no future dates — it calls **neither** report with an `as_on`, and its own comment says the two paths "must agree exactly". | `frontend/scripts/verify-demo.mts:154-168` (both calls omit `as_on`) + the default-`as_on` divergence between the Outstandings and Balance Sheet handlers | ✅ **CLOSED L23 2026-08-24 — the DEFAULT was the defect, not the predicate.** `resolveAsOn` defaults an empty as-on to today, which is right for AGEING and was also feeding INCLUSION, so a bill-wise breakup explained less than the ledger balance it is a breakup OF the moment a bill was dated ahead. With no `as_on` the rows are unbounded now; an EXPLICIT `as_on` still bounds exactly as before, and every arm carries that control. ⛔ **THE BALANCE SHEET DELIBERATELY DID NOT MOVE**, so `verify:demo` needed no re-pin — and the end-to-end proof is that it stays **34/34 WITH a future-dated bill on the books**, the very combination that used to break it. ⛔ **THREE PLACES MOVED TOGETHER** (the report, `whitelist.go`'s `EntityOutstandings` skeleton including its books-beginning guard, and the caption in FOUR copies — `QueryScreen`'s basis line, the wire echo, the screen's scope line and the printed subtitle). ⚠️ `resolveAsOn` is package-shared with `bankrecon.go` and its signature is UNTOUCHED. ⛔⛔ **THE CHEAPEST CHECK FOUND THE REAL SHAPE OF THE LANE:** with only the report-side change, `internal/reporting` was ENTIRELY GREEN and the only three failures were in `internal/query` — so this package had **no test on the unbounded default at all**, which is why the row existed. One of those three was a tripwire an earlier lane had planted for exactly this day. ⛔⛔ **AND M3 — reverting the wire echo to the false caption — LEFT THE WHOLE GO SUITE GREEN, 0 FAIL:** nothing read the sentence, which bought `TestTheAgeingReportsScopeOnTheWireIsHONEST`. Two Go tests INVERTED rather than deleted, 3 mutations, driven **16/16** on the wire and in a browser. Full record: §7.5 **L23**. |
| **D87** | 5 | MEDIUM | ⛔ **A PARTY CREATED FROM THE KEYBOARD HAS NO GSTIN, NO STATE CODE AND NO REGISTRATION TYPE — and state_code NULL is the exact input that made D19 derive an intra-state bill as IGST.** `masters.CreateLedger` and `CreateLedgerRequest` have accepted all three since they were written; the master-create form renders no field for any of them, so every Sundry Debtor/Creditor keyed by an operator lands with `gstin` NULL, `state_code` NULL and `registration_type` UNKNOWN. Driven in a browser while closing D36: the form's text contains no "GSTIN", no "State code" and no "Registration type", and the ledger it created read back `(null)|(null)|UNKNOWN`. The repo's OWN gate flags it — `verify:demo`'s "Every party's GSTIN prefix == its state_code" and "Every party is registration_type REGULAR" both fail on a keyed party, which is how it was found. ⚠️ NOT FIXED: it is the same class of build as D36 (fields the form does not render) but it carries a judgement D36 did not — whether `state_code` becomes MANDATORY under Sundry Debtors/Creditors, which would refuse creations that succeed today, and how many of the party's ten-odd optional columns (address block, PAN, credit days, bank details) the form should expose alongside it. | `frontend/src/screens/masters/MasterCreateScreen.tsx` (the ledger `Draft` has no gstin/state/registration field; `build`'s `CreateLedgerRequest` therefore sends none) vs `backend/internal/masters/masters.go` `CreateLedger`, which stores all three | ✅ CLOSED L15d 2026-08-22 |
| **D88** | 17 | HIGH | ⛔ **ALTERING A VOUCHER SILENTLY DELETES ITS TDS DEDUCTION, and any challan allocation against it is orphaned.** `internal/ledger`'s `deleteTDSDeductions` runs on EVERY alter and the row is re-derived from the body — which is correct on its own terms ("a voucher altered to carry no TDS at all must lose its deduction row rather than keep a stale one"). But the voucher READ path serves no `tds`/`tcs` block, so the form CANNOT re-state what it loaded: hydrate a voucher that carried a deduction, edit its narration, press Ctrl+A, and the deduction is gone — while the challan that paid it keeps an allocation whose natural key now matches no deduction row (migration 0069 carries no foreign key by design, so nothing in the database refuses it). The register then shows the allocation with `deduction_on_record: false`, which is the state that panel already renders and calls out. Found while building D3's form, which is the first thing in the product that can create the precondition. ⚠️ NOT FIXED: the fix is a SERVER READ-PATH change (serve the stored deduction on `GET /vouchers/{id}`) plus hydration in `formFromView`, and it should land with D3's remaining TCS half rather than ahead of it. `formFromView` states the gap at the field today. | `backend/internal/ledger/tds.go` `deleteTDSDeductions` + the voucher GET (`VoucherView` has no tds/tcs) vs `frontend/src/screens/VoucherScreen.tsx` `formFromView` | ✅ **FIXED (L4) — MUTATION-PROVED *AND* DRIVEN 2026-08-21.** The GET serves the stored block and `formFromView` re-states it. Oracle: alter the narration, the `tds_deduction` row SURVIVES and the challan register stops reading "deduction no longer on record". Pinned with its INVERSE CONTROL — an alter that OMITS the block is accepted and deletes it. See §7.5 |
| **D89** | 17 | HIGH | ⛔⛔ **A REFUSED ALTER SILENTLY BECAME A CREATE, SO THE REMEDY THE REFUSAL PRESCRIBED MINTED A SECOND INVOICE.** `navMachine`'s `accept` reducer replaced ANY voucher frame with a fresh `create` frame on every Ctrl+A. It is pure, so it can never learn that the write was refused — least of all here, where the refusal was CLIENT-SIDE and not one byte left the browser. That dropped the loaded `voucher_id` which routes the PUT, while the form kept the loaded voucher's values on screen: the heading was the only thing that changed, by thirteen characters, at the bottom of a refusal the operator is reading. Driven in a browser: drill a legacy no-HSN sales invoice out of the Day Book, Ctrl+A → refused by `gstRefusal`, key the HSN the refusal asked for, Ctrl+A → **`POST /vouchers/sales` 201**, status line "Saved", and the original invoice's `hsn` still empty. **One tax invoice keyed, two on the books.** ⚠️ NOT GST-SPECIFIC: any refusal on an alter had this shape, client- or server-side. ⚠️ The class was already known on the DRILL side — `phase1.test.ts`'s CONTROL "a row naming no class does not open a voucher frame" closes the same failure reached the other way; that guarded the door IN, nothing guarded the door THROUGH. | `frontend/src/keystroke/navMachine.ts`, the `accept` reducer's voucher arm | ✅ **FIXED (L14) — MUTATION-PROVED *AND* DRIVEN 2026-08-22.** An alter frame stays put; a second Ctrl+A re-PUTs the same `voucher_id` and Esc still returns to the report it was drilled from — which is what `delete` directly below already did. Oracle: the Day Book entry count over the date is UNCHANGED and the voucher reads `op=ALTER`, not a second `CREATE`. Pinned with both CONTROLs (a CREATE frame still re-opens fresh for bulk entry; two accepts on a create really do key two invoices) so neither "never re-open a fresh form" nor "never accept a frame twice" can pass in place of the fix. See §7.5 |
| **D90** | — | MEDIUM | ⚠️ **A DUPLICATE STOCK ITEM NAME IS ACCEPTED (201, a NEW id) WHILE A DUPLICATE LEDGER NAME IS REFUSED (422).** Found by D84's wire discovery, which measured the recon's claim instead of trusting it: the recon said the route "only ever reaches `CreateStockItem`, which 422s" — cored actually mints a second item with the same name. Consequence: two identical rows in every item picker with nothing to tell them apart, and one item's movements and valuation split across two masters, each weighted-average costed on half the history. ⚠️ **The mock does the OPPOSITE** (create-or-select returns the existing id), so no test can see this — it is recorded as a declared divergence in `corpora/mock-vs-cored/masters.json` rather than mirrored, because mirroring cored here would spread the more suspicious of the two behaviours. ⛔ NOT FIXED: whether a duplicate item name should be refused is a judgement (Tally allows same-named items under different stock GROUPS, which this build does not have yet — that is T10), so it needs deciding before code moves. | `backend/internal/masters/masters.go` CreateStockItem vs CreateLedger's uniqueness check | ✅ CLOSED L15f 2026-08-22 — ⛔ **PREMISE DISPROVEN, CONSEQUENCE REAL.** cored refuses an exact duplicate on BOTH masters and always has; the real defect was BYTE-EXACT uniqueness across ledgers, stock items AND units. See §7.5's L15f section. |
| **D91** | — | MEDIUM | ⚠️ **`pnpm contract:cored` PERMANENTLY BREAKS `pnpm verify:demo` ON WHATEVER TENANT IT IS POINTED AT.** The D84 contract runner creates its fixture parties (`CT Ledger *`, `CT Control *`, `CT Dup *`) in the company named by `CO`, which every session hands it the DEMO company. Those parties have no `state_code`, no GSTIN and no bill-by-bill, so they fail three of `verify:demo`'s party checks BY CONSTRUCTION, and cored has no `DELETE /ledgers` to undo them. The damage accumulates one run at a time: fixture ledgers dated 20:33 and 20:44 (L14d) and 22:59 (L15) took the demo tenant from 34/34 to 27/34, and a fresh seed reads 34/34. ⛔ **THE REAL COST IS MISATTRIBUTION** — the next session runs `verify:demo`, sees seven failures including two money ones, and spends the lane debugging its own change. Found 2026-08-22 while measuring L15's gates, by re-seeding instead of assuming. **FIX: give `contract:cored` its own throwaway tenant** (it already creates everything it needs; only the company is borrowed). Reaping is not the alternative — there is no delete route, and `company` has 14 non-cascading FKs. ⚠️ Note the two MONEY failures on the stale tenant (BS vs Outstandings, GSTR-1 vs Domestic Sales) are a DIFFERENT residue — L14's deliberately-left probe fixtures — and are not this row. | `frontend/scripts/contract-mock-vs-cored.mts` (the runner, takes `CO`) vs `frontend/scripts/verify-demo.mts:357-375` (the party checks) | ✅ CLOSED L15b 2026-08-22 |
| **D92** | — | HIGH | ⚠️ **A REFUSED `Ctrl+A` ON A MASTER-CREATE FRAME THREW AWAY EVERYTHING THE OPERATOR HAD KEYED.** `navMachine`'s `accept` reducer pops a master-create frame UNCONDITIONALLY — it was written when the only way to reach that reducer was a SUCCESSFUL create — and the shell could not tell a refusal from a success because both returned a plain string. So a `build()` refusal (no group, or a bill-wise breakup that does not add up) threw the operator back to the Gateway, discarded the form, and explained why on a screen they could no longer act on. Measured in a browser 2026-08-22: name a ledger, leave the group blank, Ctrl+A, and the form is gone along with the name. ⛔ **THE BREAKUP REFUSAL'S OWN COMMENT CLAIMED THE OPPOSITE** — *"so the operator learns it while the figures are still in front of them rather than from a status line after the frame has popped"* — which is precisely what did NOT happen. ⚠️ Survivable only because the refusals were RARE; **D87 makes one of them the common case** (every party keyed without a state code), which is why this was found and closed immediately before it. Found by measuring a recon TRAP instead of reasoning about it. | `frontend/src/keystroke/navMachine.ts` (the unconditional `stack.pop()` in `case "accept"`) + `frontend/src/app/AppShell.tsx` (the keydown wrapper, which forwarded the key regardless) + `frontend/src/app/ScreenActions.tsx` (`ScreenActionResult` had no way to say "refused") | ✅ CLOSED L15c 2026-08-22 |
| **D93** | — | LOW | ⚠️ **`masters.CreateLedger` VALIDATED SOME FIELDS BY HAND AND LET THREE OTHERS FAIL AT THE DATABASE, leaking the raw driver error — SQLSTATE and the CONSTRAINT NAME — to the HTTP caller.** `registration_type` (a Postgres ENUM), `bank_ifsc` and `bank_account_number` (two CHECK constraints) all answered things like *`masters: insert ledger: ERROR: invalid input value for enum gst_registration_type: "Regular" (SQLSTATE 22P02)`* and *`violates check constraint "ledger_bank_ifsc_shape" (SQLSTATE 23514)`*. Each names an internal type or constraint, quotes a SQLSTATE, and tells the operator nothing about what they may type instead. ⛔ **THE EXPECTATION WAS ALREADY PINNED — ON ONE PATH:** `drive-l13-masters.mjs` asserts `!/SQLSTATE/` on a refusal from THIS ROUTE (the duplicate-name arm), so three sibling refusals were unenforced beside one that was. ⛔ **FOUND BY A MUTATION PROOF, AND BY THE PREDICTION BEING WRONG** (§4.2): D87's M13 sent the display label `"Regular"` instead of `"REGULAR"` and predicted cored would silently coerce it to the UNKNOWN default. It does not — the refusal was correct all along and only its SENTENCE was wrong. | `backend/internal/masters/masters.go` `CreateLedger` (the RegistrationType default with no validation beside the hand-written OpeningDC check) | ✅ CLOSED L15e 2026-08-22 |
| **D94** | 3 | HIGH | ⛔ **THE PAYABLES AGEING REPORT HAS NO DOOR: the server serves it, and nothing in the product can ask for it.** Split out of **D13** on 2026-08-23 (L16 position 0) because it is a BUILD and a half-closed row was hiding it. **MEASURED ON THE WIRE, not inferred** (§5.1 — this time the register's server-side claim SURVIVED the check), against a binary verified fresh by symbol: `GET /outstandings?kind=PAYABLE` → **200, total 1,04,050.0000, 2 creditors, 4 bills** (`Steel Suppliers` 80,450.0000 / 3 bills · `Delhi Parts` 23,600.0000 / 1 bill), each with its full per-bill array; `?kind=payable` is accepted too, and `?kind=BOTH` is refused **400 `invalid kind (RECEIVABLE\|PAYABLE)`**. So the whole payable direction — every supplier bill the business owes — is live on the transport and unreachable from the app: the client builds `const query: { as_on?: string } = {}` and `kind` is never a member, there is ONE report name (`"Outstandings"`), ONE screen (`report:Outstandings`) and ONE PDP resource. ⛔ **AND IT IS NOT A ONE-LINE `?kind=` TOGGLE — THAT IS THE WHOLE REASON IT IS A ROW.** `routeAction` maps the flat literal path `/outstandings` → `"outstandings"`, so the PDP decides on the report NAME and **a query parameter is invisible to it**; a toggle would make *"the sales clerk may see what customers owe us but not what we owe suppliers"* an **unwritable rule**, and those two lists belong to two different jobs. Migration 0011 seeds Data Entry a COARSE `allow display report` that matches ANY `report:NAME` at specificity 1, so **a new report name arrives PRE-GRANTED** and the refusal is the thing that must be written. | **THE BUILD, and it is the Order Book's treatment end to end** (`0073_order_book_acl.sql` is the template and its header is the argument): (1) a second FLAT LITERAL route beside `/outstandings` — additive, so no existing `acl_rule` row is invalidated; (2) its arm in `routeAction` (`auth_middleware.go:2258`); (3) the name in `authz.KnownReportNames` (`vocabulary.go:457`); (4) the printed form (`handler_print_reports.go` — `printOutstandings` already parses `kind`, and `outstandingKindParam` is SHARED with the JSON handler on purpose, so both directions print from one parser); (5) **migration 0076 = the ACL counterweight**, `disallow display` **and** `disallow print`, no allow seeded, ⛔ **derived from `0073`'s body — the highest-numbered restatement of `seed_company_auth_defaults` (confirmed 2026-08-23) — carrying all THIRTEEN existing `PERFORM`s plus the new one, or every fresh database silently loses rules** (the defect `0071` exists to repair); (6) the client: the report name in `keystroke/types.ts:290`, the screen in `defaultScreens.tsx:301`, and the screen sending `kind`. | ✅ **CLOSED L24 2026-08-24 — a second FLAT LITERAL route, so the rule can be written at all.** `/bills-payable` beside `/outstandings`, its `routeAction` arm, `bills-payable` in `authz.KnownReportNames`, the printed form from the SAME layout with the kind passed rather than re-parsed, and the client registries (tsc named both `Record<ReportName,…>` tables). ⛔ **ONE WRITER, TWO DOORS ON ALL THREE SIDES** — cored's `writeOutstandings`, the client's `OutstandingsLikeScreen`, the mock's `outstandingsOf` — so the two reports cannot drift while the two RULES stay separate. ⛔⛔ **THE ORACLE'S SECOND ARM, DRIVEN:** a level with `disallow display report:bills-payable` gets **403** on `/bills-payable` and **200 with a row** on `/outstandings`. A `?kind=` toggle could never pass that. ⛔ **AND THE ALIAS THAT LIED IS RE-POINTED:** `REPORT_ALIASES` mapped `"bills payable"` to the RECEIVABLE report and had SHIPPED that way. ⛔⛔ **NO COUNTERWEIGHT MIGRATION, AND 0077 STAYS FREE — a user decision taken on a measurement that contradicts the row's own text.** A seeded Data Entry level ALREADY reads the payables data as `?kind=PAYABLE` (200, measured with a membership control), and NEEDS to: the bill-wise picker on a Payment asks for exactly that, and a clerk refused it settles ON ACCOUNT. The rule is WRITABLE, not written. 3 mutations, driven **15/15**. Full record: §7.5 **L24**. |
| **D95** | 32 | MEDIUM | ⛔ **THE ANOMALY LIST AND THE CASH-FLOW PROJECTION HAVE NO ACL COUNTERWEIGHT, SO THE SEEDED DATA ENTRY LEVEL CAN READ BOTH — AND THE ANOMALY LIST IS THE CONTROL SURFACE THAT MONITORS THAT VERY OPERATOR.** Migration 0011 seeds Data Entry a COARSE `allow display report` which `internal/authz` matches against ANY `report:NAME` at specificity 1, so **a report name is GRANTED by default and the refusal is the thing that must be written.** Every comparable report in this product has that refusal written: the Stock Summary (0026, it prints cost prices), the other inventory reports (0028), the ITC reports (0033), payroll (0035), the bank statement (0063), the TDS/TCS register (0068), the cost reports (0072) and both order books (0073). **`report:cash-flow` and `report:anomalies` have nothing** — `rg 'cash-flow\|anomalies' backend/migrations` is empty. So a Data Entry clerk can read **every voucher they backdated, keyed after hours, edited three times in a day or wrote for a round amount, by number** — the list a supervisor reads ABOUT them. A control surface visible to the people it monitors is not much of a control. ⚠️ **THIS PREDATES THE SCREEN AND IS NOT CAUSED BY IT:** the routes have been open since wave 2 and were reachable by anyone who knew the URL. What L16 changed is that it is now one keystroke, which is why it is registered rather than left implicit. | **THE FIX IS `0073_order_book_acl.sql` COPIED, and its header is the whole argument:** a new migration writing `disallow display` **and** `disallow print` for BOTH tokens (a report that cannot be displayed but CAN be printed is a leak with a paper trail — 0063/0068/0072/0073 all write the pair), **no allow seeded**, one shared `*_acl_grants()` definition read by both the per-company seeder and the backfill, plus a restatement of `seed_company_auth_defaults` ⛔ **derived from the HIGHEST-NUMBERED restatement at the time and taking a higher number than it** — a body installed lower is overwritten on every fresh database and the rules simply do not exist, which is the defect 0071 exists to repair. `migrations/seeder_census_test.go` guards it statically. ✅ **ASKED AND ANSWERED 2026-08-23: DENY BOTH, NO ALLOW SEEDED** (driver §6.4, which is AUTHORITY). The user endorsed the precedent argument: a forward cash position is a MANAGEMENT report, and a tenant wanting a senior clerk to read it writes an explicit `allow display report:cash-flow` through the admin API — visible in `acl_rule`, chosen rather than inherited. ⛔ **Do not seed an allow back and do not treat the two tokens differently.** Nothing about this row is waiting on the user any more | ✅ **CLOSED 2026-08-23 (L17) — migration 0076, MUTATION-PROVED AND DRIVEN 20/20 on the wire AND in a browser.** ⛔ **THE TWO `display` ROWS ARE THE FIX; THE TWO `print` ROWS ARE DURABILITY, AND BOTH THE MIGRATION HEADER AND THE PROBE SAY SO.** Data Entry was ALREADY 403 on both print forms (0038 seeds no `allow print report:`, 0011's coarse grant is `display`-only, default_access is `none`), so only the display arms go red-to-green — a probe reporting "4/4 green" would be half vacuous. ⛔ **AND THE DRIVER'S STATED ORACLE WAS WRONG:** it said *"an Owner still reads all four"*; printing these two is UNIMPLEMENTED and an Owner gets **501**, which `TestTheUnimplementedReportsAreStillGovernableAndSaySo` actively requires — asserting 200 would have failed on correct code. The arm now asserts 501 **and** that the body names the report, because the Owner's refusal is a MISSING LAYOUT and the clerk's is a POLICY DECISION. ✅ **THE BROWSER ARM IS THE ONE WORTH KEEPING:** L16 shipped a 403-as-a-decision panel on speculation and this row is its FIRST REAL REFUSAL — the clerk reaches Cash Flow from the keyboard and reads *"Your security level may not open the Cash Flow projection. This is a decision recorded against your level, not a failure."* ⚠️ **The probe brings its own tenant and reaps it (D91 pattern)** — the demo tenant has no Data Entry member, and provisioning one there would leave a user `verify:demo` does not tear down |
| **D96** | 32 | LOW | ⚠️ **insightd's OPTIONAL STATISTICAL FORECAST STILL HAS NO RENDERER — extracted from D22 on closure rather than buried inside it.** This is D72's one surviving clause. D72 was closed 2026-08-21 because its MONEY half was already fixed (`ForecastRequest` takes fixed-4 `Money` strings, gated with a non-vacuity arm), and its remaining clause — *"still not reachable from the keyboard"* — was folded into D22. ⛔ **L16 CLOSED D22 WITHOUT CLOSING THIS**, and the distinction is real rather than pedantic: D22's door was built over **cored's** `GET /cash-flow`, a deterministic open-bill settlement projection. This is a **different service** (insightd), a different endpoint, and a genuinely different method — a STATISTICAL forecast, which cored's own `method` string goes out of its way to say it is not (*"deterministic; no statistics, no model"*). Wiring one did not wire the other. ✅ It already declares `estimate: true` and its own method, so the disclosure work is done on its side. | ✅ DONE — deleted. `insightd/insightd/forecast/` · `api/routes_forecast.py` · `requirements-forecast.txt` · `tests/test_forecast_{api,backtest,no_cored_write}.py`, and the optional mount in `api/app.py` | ✅ CLOSED L19 2026-08-24 — **DELETED, not built.** The user's answer to the row's own prior question (§6.8): cored's projection is explainable to an auditor bill by bill, and a second cash-forward number by a different method with no auditor story invites exactly the confusion the Cash Flow disclosure block exists to prevent. The package, the route, the optional dependency pin and its three test modules are gone; `tests/test_forecast_plane_is_gone.py` keeps them gone. ⚠️ **Needs NO report name and NO ACL counterweight** — contrast D95/0076, which existed because the forecast plane's CORED reports ARE reachable. ⛔ **This LOWERS D49 from 9 routes to 8.** See §7.5 L19 |
| **D97** | — | LOW | ✅ **CLOSED L26 2026-08-24.** A level denied `report:bills-payable` could still read the same figures as `GET /outstandings?kind=PAYABLE` — `routeAction` maps the FLAT LITERAL PATH to the PDP resource and a query parameter never reaches it, so **the rule governed the DOOR, not the bytes**. It was NOT patchable where it was found: that read is what the BILL-WISE PICKER uses when a clerk keys a Payment, and refusing it there pushes the money onto ON ACCOUNT. **The user took the design decision** (options put 2026-08-24): (1) the picker gets a PARTY-SCOPED door authorised as **the VOUCHER CLASS being keyed** — `GET /vouchers/{class}/open-bills/{ledgerID}?kind=` — because that is the authority a clerk keying the voucher already holds (migration 0011 seeds `allow display <class>`), so **NO migration is needed and no default-deny level can silently lose its picker on upgrade**; and (2) `?kind=PAYABLE` is **RETIRED** from BOTH ageing doors — JSON *and* PRINTED — with a **308** to `/bills-payable`, so a stale bookmark stays an ANSWER and the refusal still comes from the MIDDLEWARE rather than from a first-ever in-handler 403. The ledger is a PATH segment so there is **no unscoped form of the route to serve** — structure, not validation. The picker also stops over-reading: it used to fetch the WHOLE ageing report and narrow it in the browser; the route emits ONE party, no slab totals, no grand total. ⚠️ **RESIDUAL, STATED NOT HIDDEN:** a level with `display payment` can call the route once per supplier ledger it can already see and rebuild a list by hand — inherent to ANY party-scoped door, and the accepted cost of decision (1). What it can no longer do is read the BOOK. §7.5 **L26**. | `backend/internal/server/auth_middleware.go` (the 4-segment `open-bills` arm) + `backend/internal/server/server.go` (`handleOpenBills`, the 8 literal routes, the 308 in `handleOutstandings`) + `backend/internal/server/handler_print_reports.go` (the PAPER door's 308) + `frontend/src/screens/vouchers/BillAllocationPicker.tsx` (`useOpenBills` reads the new door) + `frontend/scripts/drive-l24-d94.mjs` (**the arm that registered it, now INVERTED**) | ✅ CLOSED |
| **D98** | **U1** | HIGH | ⛔⛔ **A REFUSED TALLY IMPORT IS NOT ATOMIC, THE REFUSAL SAYS NOTHING ABOUT WHAT LANDED, AND THE OPERATOR'S NATURAL RECOVERY DOUBLE-POSTS IT.** Driven in a browser 2026-08-27 (`drive-u1-import-partial.mjs`, four throwaway tenants, all reaped). A three-voucher export whose THIRD voucher carries `<AMOUNT>$ 1000.00</AMOUNT>` is refused on the screen with `migration: voucher 3: integration: ledger "Rent": bad AMOUNT "-$ 1000.00": can't convert -$ 1000.00 to decimal` — a good sentence, which names the voucher ordinal, the ledger and the offending value. ⛔ **AND VOUCHERS 1 AND 2 ARE IN THE BOOKS.** `voucher_head` holds 2 rows for that company, and — the assertion that makes this a finding rather than a schema note — **both are visible in the operator's own Day Book**, keyed from the keyboard in the same session: *"2026-04-01 1 THE FIRST GOOD VOUCHER 50,000.00 · 2026-04-02 1 THE SECOND GOOD VOUCHER 30,000.00"*. Nothing on the screen says so; `TallyImportScreen` renders the server's sentence and its own copy says *"There is no way to undo an import"*, which is TRUE and is exactly why the silence costs money. ⛔ **THE RECOVERY IS THE MONEY LOSS, AND IT WAS MEASURED BOTH WAYS.** Fix the amount, import again: a **GUID-LESS** export ends with **5 vouchers** — the two that already landed, posted a second time, real money duplicated with no delete path anywhere in this product. A **GUID-BEARING** export (what TallyPrime itself writes) ends with **3**, because the Create arm skips a GUID already imported. So the blast radius is GUID-less and partial files — hand-built exports, a Day Book export, anything assembled by a migration consultant — and NOT a whole-company TallyPrime dump. ⚠️ **The trigger is not currency-specific**: any per-voucher build or post failure lands here — the back-dated cutoff, a closed period, a refused ledger — because the loop `return res, err`s on the first one. | `backend/internal/migration/migration.go:247-336` (the second-pass voucher loop: `im.ledger.PostVoucher` per voucher, `return res, err` on failure, **no `BeginTx`/`Rollback` anywhere in the package**) + `backend/internal/server/server.go:3782-3789` (`res, err := s.importer.ImportReader(...)`; on error `writeError(w, 422, err.Error())` and **`res` — which holds `VouchersPosted` — is discarded**) + `frontend/src/screens/TallyImportScreen.tsx:173-177` (the error paragraph, the whole disclosure surface). **THE FIX IS A DECISION, NOT A PATCH, AND IT IS ONE OF TWO:** (a) **make the import atomic** — one transaction around the second pass, so a refusal leaves the books as they were and the "no undo" sentence stops being load-bearing; note every voucher goes through the immutable ledger path and the audit hash chain, so whether that path can be enrolled in a caller's transaction is the thing to check FIRST. ⭐⭐ **THAT CHECK WAS RUN 2026-08-27 AND THE ANSWER IS YES — OPTION (a) IS AVAILABLE AND NEEDS NO CHANGE TO THE `ledger` PACKAGE AT ALL.** `ledger.NewService` takes a **one-method interface** — `TxRunner{ RunInTx(ctx, db.TxFunc) error }` (`ledger.go:40`) — and every write in the package, `post` included, goes through `s.runner.RunInTx`. So an enrolling runner that opens ONE transaction and hands the SAME `pgx.Tx` to each nested call makes the whole second pass one unit of work, and the hash chain simply rolls back with it (it is computed inside that transaction, not against committed state). ⭐ Three facts make this concrete rather than hopeful: (1) **pgx v5.10.0 already implements pseudo-nested transactions as SAVEPOINTs** — `tx.Begin()` issues `savepoint sp_N`, Commit RELEASEs and Rollback rolls back to it (`pgx@v5.10.0/tx.go:164-176`) — which is exactly the primitive needed if a per-voucher failure should ever be tolerated rather than abort the run; (2) **a non-Pool `TxRunner` already exists in this tree** (`internal/compliance/itc/service_window_test.go:38`), so the seam is used, not merely typed; (3) reads-your-own-writes holds inside one transaction, so the negative-stock check and the numbering still see voucher N-1 while voucher N is being built. ⛔ **THE ENROLLING RUNNER MUST REPRODUCE `Pool.RunInTx`'S TWO GUARANTEES OR IT IS A TENANCY HOLE, NOT A FIX** (`internal/platform/db/db.go:141-166`): `ErrNoCompany` returned BEFORE any database round-trip when no company is in scope, and `set_config('app.current_company_id', …, true)` as the FIRST statement on the transaction so RLS scopes every statement. Set it once at BEGIN; `SET LOCAL` is transaction-scoped and one import is one company. ⚠️ **AND THE REAL COST IS A CONSTRUCTOR CHANGE, WHICH IS WHERE THE WORK ACTUALLY IS**: `NewImporter(m *masters.Service, l *ledger.Service)` (`migration.go:66`) receives services already built over the pool at `server.go:344`, so an atomic import has to build BOTH services per-run over the enrolling runner — `masters` takes the same interface — rather than reuse the injected pair. ⛔ **THERE IS A SUB-DECISION INSIDE (a) AND IT MUST BE MADE DELIBERATELY: does the FIRST pass roll back too?** Today masters commit per-master and deliberately SURVIVE a refusal — `inventory_shapes_test.go` states the reasoning in terms ("they are facts about the file, not about the trade"). Rolling the whole import back reverses that choice; rolling back only the voucher pass preserves it and still closes the double-post. **(a-vouchers-only) is the smaller change and the one that contradicts no existing decision**; or or (b) **keep it partial and SAY SO** — carry `res` past the error, return the 422 with `vouchers_posted` beside the sentence, and have the screen render *"N vouchers were committed before this and cannot be removed"* plus what to do next. ⛔ (b) is strictly cheaper and strictly worse: it leaves the GUID-less double-post live. | ✅ **FIXED AND RE-DRIVEN 2026-08-27.** Option (a), scoped to the VOUCHER PASS — decided with the repo owner, who also chose to leave the master passes committing per-master so the documented "facts about the file" behaviour stands. `migration.go` now runs the whole second pass inside ONE `RunInTx`, handing both services a `txPin` that returns the same `pgx.Tx`; the `ledger` and `masters` packages did not change. ⭐ **THE MUTATION PROOF WAS PREDICTED THEN OBSERVED**, which is the only reason to believe the test: with the enrolment reverted, `TestARefusedImportCommitsNothing` reports *"a REFUSED import left 2 voucher(s) in the books"* and `TestTheNaturalRecoveryDoesNotDoublePost` reports *"the books hold 5 vouchers, want 3"* — **2 and 5 are exactly the numbers the browser drive measured when this was registered.** Restored, both green. ⭐ **AND RE-DRIVEN IN A BROWSER** on a cored verified fresh (started 19:45:19, after every backend edit): `drive-u1-import-partial.mjs` **15/16**, with all four D98 arms green — *"a REFUSED import left the books UNCHANGED (0 vouchers) — atomic"*, *"the Day Book shows NOTHING after the refused import"*, and *"0 landed under the refusal, 3 in the books after the corrected re-import"* on BOTH the GUID-less and GUID-bearing files. ⚠️ The 16th probe is the **D99 comma arm**, which fails BY DECISION — that row is deliberately left alone and this drive asserts the fix it did not get. ⚠️ The screen now states the guarantee where the recovery begins (`tally-import-refusal-atomic`): *"No vouchers were imported… do not import a trimmed file"*, plus the note that masters DO remain and re-importing resolves rather than duplicates them |
| **D99** | **U1** | LOW | ⚠️ **THE THREE MONEY READERS REFUSE A COMMA-GROUPED NUMBER WHILE THE QUANTITY READER STRIPS COMMAS ON PURPOSE — SO THIS CODEBASE STATES THAT TALLY EMITS THEM AND THEN CANNOT READ THEM.** `SplitQtyUnit`'s own comment: *"Commas are thousands separators in Tally's own output and are not part of the number"* — and it strips them. `Magnitude()`, `OpeningSide()` and `OpeningMagnitude()` all hand the raw string to `decimal.NewFromString`, which refuses. Predicted in `docs/design/t8-multi-currency-model.md` §6 against the unchanged build; **driven through the screen 2026-08-27**: `<AMOUNT>83,650.00</AMOUNT>` is refused with `migration: voucher 3: integration: ledger "Rent": bad AMOUNT "-83,650.00": can't convert -83,650.00 to decimal`, and — because it is a per-voucher failure — it lands squarely in **D98**: the two vouchers before it are committed and the operator is told only about the third. ⭐⭐ **AND THE PREMISE IS NOW REFUTED BY A REAL EXPORT — SEVERITY LOWERED FROM MEDIUM TO LOW, 2026-08-27.** The user supplied a genuine TallyPrime pair and every money and quantity field in it was COUNTED rather than sampled: **1,156 `<AMOUNT>` values, 714 `<OPENINGBALANCE>` values, 454 `<BILLEDQTY>`, 454 `<ACTUALQTY>`, 227 `<RATE>` — and ZERO of them carries a comma.** Every money value is plain `[-]digits.digits`; the quantities carry a UNIT inside the value (`" 101.00 BAG"`) and the rates a `/UNIT` (`"59.03/BAG"`), which is what `SplitQtyUnit` genuinely exists for. ⛔ **SO `SplitQtyUnit`'s COMMENT IS THE ONLY EVIDENCE FOR THE CLAIM AND THE ONE REAL EXPORT WE HAVE CONTRADICTS IT** — its comma-stripping is defensive, not a response to observed Tally output. ⚠️ **A COMMA DOES APPEAR IN THAT FILE, AND IT IS DATA RATHER THAN FORMATTING:** 51 of the 227 `<STOCKITEMNAME>` values name an item called `TOORDAL & DALI,ATTA,ETC`. A reader that stripped commas from a NAME would merge distinct items. ⛔ **LATENT, NOT ACTIVE.** The reader asymmetry is still real and still worth recording; what has gone is the reason to think a customer will hit it. ⛔ **DO NOT FIX IT SPECULATIVELY.** Accepting commas everywhere also makes a genuinely malformed value readable — `1,2,3.00` and `1,00,000.00` (the Indian grouping) both become numbers under a naive strip — so a fix has to decide what it is willing to call a number, and that decision wants a real file behind it. | `backend/internal/integration/tallyxml.go:890` (`Magnitude`), `:478` (`OpeningSide`), `:496` (`OpeningMagnitude`) — all three call `decimal.NewFromString` on the raw string — versus `:612` (`SplitQtyUnit`, which strips `,` in its digit scan). ⛔ **THE DECISION IS NOW "LEAVE IT", NOT "WAIT".** One real export has arrived and refutes the premise; a second would have to CONTRADICT it before any reader changes. If one ever does, settle all three money readers together — one left alone is the shape that ships a half-fix — and settle `SplitQtyUnit`'s comment at the same time, because a comment asserting a format that no observed file uses is how this row came to exist | ⛔ **OPEN — REGISTERED 2026-08-27, and deliberately left alone.** ⭐ Its premise was tested against a real export the same day and did not survive; it stays on the register at LOW because the reader asymmetry is real, and it is now a note for whoever meets a grouped money field rather than a repair anybody should make today. ⚠️ D98 is what would make this one cost money; on its own it is a refusal an operator can act on |
| **D100** | **U1** | MEDIUM | ⛔ **THE PRODUCT TELLS A MIGRATING OPERATOR TO PRODUCE A FILE, AND DOES NOT TELL THEM HOW — SO THE ABORT'S OWN "CORRECT FIX" IS THE ONE DOOR THEY CANNOT FIND.** Found 2026-08-27 driving `drive-u1-real-export.mjs` against a genuine TallyPrime export pair supplied by the user (a real trading company's 2019-20 books; 84 MB `Master.xml`, 121-voucher `Transactions.xml`). `TallyImportScreen.tsx:88-92` says *"Export the whole company from Tally (Masters and Transactions)"*. An operator who does exactly that gets a masters file whose `TALLYMESSAGE` children are **LEDGER 3706 · GROUP 37 · CURRENCY 2 · COMPANY 1 and nothing else** — no `STOCKITEM`, no `UNIT`, no `GODOWN`, no `STOCKGROUP` — while the transactions name **6 stock items, 1 godown and 75 batches**, so those masters plainly exist in the source company. The import then aborts on voucher 1 (`… would drive a stock position NEGATIVE: item RICE …`) and offers *"(1) import a Masters.xml carrying the items with their opening balances FIRST — that is the correct fix"*, which the operator has no way to act on, pushing them to remedy (2) — the one the message itself says **understates the cost of goods sold**. ⭐⭐ **SETTLED 2026-08-27, AND THE ANSWER IS THAT THE CAPABILITY EXISTS**: TallyPrime's master export carries a **Type of Master** dropdown (`Accounting Masters` / `Inventory Masters` / **All Masters**) plus **Include dependent masters** and **Export closing balance as opening balance** ([TallyHelp — Export Data in TallyPrime](https://help.tallysolutions.com/tally-prime/data-management/export-data-in-tally/)). The user's file is an **Accounting Masters** export. ⛔ **SO THIS IS A DOCUMENTATION DEFECT, NOT A MISSING CAPABILITY, AND THAT IS WHY IT IS MEDIUM RATHER THAN HIGH** — nothing is silently wrong, the product refuses correctly and loudly; the cost is that it sends every migrating customer down the worse of two doors for want of one sentence. ⚠️⚠️ **AND ONE PIECE OF EVIDENCE IN THE FIRST DRAFT OF THIS ROW WAS WRONG — RECORDED RATHER THAN QUIETLY DROPPED.** It said *"their Master.xml IS the All Masters export — its own REPORTNAME says so"*. **`<REPORTNAME>` IS VESTIGIAL IN A TALLYPRIME EXPORT: both files carry `<REPORTNAME>All Masters</REPORTNAME>`, including `Transactions.xml`, which holds 121 VOUCHERs and ZERO ledgers.** It identifies nothing. **Count the `TALLYMESSAGE` children instead** — that measurement was always the load-bearing one and it is unchanged. ⭐ The rest of the migration is good, which is what makes this worth one sentence rather than a project: with negative stock allowed, 121 vouchers and 226 stock movements post with **zero** warnings and the trial balance **BALANCES at 25,986,501.8800 over 3,712 rows**. ⚠️ A second measured fact, not a defect: the transactions create **72 ledgers the masters export did not contain**, so Tally's masters export is not all the masters its own vouchers post to — which is why `ensureLedger` exists and must keep existing. | **THE FIX IS THREE SENTENCES AND NO NEW CODE PATH.** (1) `frontend/src/screens/TallyImportScreen.tsx:88-92` — replace *"Export the whole company from Tally (Masters and Transactions)"* with the actual keystroke recipe: in TallyPrime press **Alt+E**, and under **Configure** set **Type of Master = All Masters** and **Include dependent masters = Yes**; ⭐ **and name `Export closing balance as opening balance`, because that is the option that makes remedy (1) say what it promises** — an export of a closed year carries CLOSING balances, and the new company needs them as OPENINGS. (2) `backend/internal/migration/migration.go` — remedy (1) in the abort text carries the same recipe, since that is where an operator meets the problem. (3) ⚠️ **Consider having the importer SAY SO when a masters file contains no inventory masters at all** — the import is otherwise clean and the operator learns their file was incomplete one step later, from an abort about RICE. ⛔ **STILL DO NOT DEFAULT `allow_negative_stock` ON FOR IMPORTS:** the Features panel's own warning is the reason — an issue with no stock to consume has no COST to consume, so closing valuation stays wrong until a receipt prices it, and choosing that silently is what the two-remedies message exists to avoid | ✅ **FIXED 2026-08-27.** Three sentences and no new code path, as the row said. `TallyImportScreen.tsx` carries the recipe — **Alt+E**, Configure, **Type of Master = All Masters**, **Include dependent masters = Yes**, **Export closing balance as opening balance = Yes** — and `describeNegativeStock` in `inventory.go` carries the same recipe inside remedy (1), because that is where an operator actually meets the problem. ⭐ **THE TWO COPIES ARE PINNED TOGETHER BY A TEST**: `TestAnExportWithNoOpeningStockIsRefusedWithBothWaysForward` now asserts the refusal contains `"All Masters"` beside its existing needles, so the recipe cannot drift out of one of them silently — which is the failure mode a two-place copy fix invites. ⚠️ The doc comment on `describeNegativeStock` was corrected too: it blamed a Day Book export, and the commonest cause is a whole-company export taken with the DEFAULT Type of Master |
| **D101** | **U1** | HIGH | ⛔⛔ **THE IMPORTER POSTS VOUCHERS THAT TALLY KEEPS OUT OF THE BOOKS, AND SAYS NOTHING.** Found 2026-08-27 by ENUMERATING a real TallyPrime export element by element and diffing it against every `xml:"…"` tag the parser binds: `<VOUCHER>` has **207 distinct direct children in a real file and `TallyVoucher` binds 15**. Almost all of the other 192 are Tally internals nobody should read (`USEFOREXCISE`, `LEDGERFBTCATEGORY`, `PERSISTEDVIEW`). **Three are not**: `ISCANCELLED`, `ISOPTIONAL` and `ISDELETED`. ⛔ **DRIVEN, NOT INFERRED** (`drive-u1-voided-vouchers.mjs`, fixture `corpora/migration/10-voided-vouchers.xml`, own tenant, reaped): a file whose second voucher is `ISCANCELLED=Yes` and whose third is `ISOPTIONAL=Yes` reports **"Vouchers posted: 3"** and **"Warnings (0)"**, and **both voided vouchers appear in the operator's own DAY BOOK** beside the real one — 30,000.00 of sales the source books show as VOIDED, and a provisional Journal the source books exclude by definition. ⛔ **AND A LEDGER MASTER MARKED `ISDELETED=Yes` IS CREATED.** ⭐ **THE ASYMMETRY IS THE TELL, AND IT IS WHY THIS IS AN OVERSIGHT RATHER THAN A DECISION:** this codebase already knows the idea — `Deleted()` is defined AND honoured on **five** master kinds (stock group, unit, godown, stock item at `inventory.go:299,363,405,423`; account group at `migration.go:384`). It never reached the voucher or the ledger master, which are the two oldest readers. ⚠️ **LATENT IN THE ONE REAL EXPORT WE HAVE, AND THE LIMITS OF THAT ARE STATED:** all 121 vouchers in it carry `No` for all three, so this company has none. What the file DOES show is that the export is **not filtered** — all five voucher-number series are CONTIGUOUS (Sales 7594-7641 · Receipt 475-511 · Payment 403-414 · Journal 60-68 · Purchase 105-119, **zero missing numbers in any series**), and a cancelled voucher in Tally RETAINS its number, so a filtering export would leave gaps. Tally also emits the flag on every voucher, which is what an export that CARRIES them looks like rather than one that drops them. ⛔ **The consequence if it fires is silent and unrecoverable**: the trial balance still balances, no warning is raised, nothing on any screen distinguishes voided money from real money, and there is no delete path in this product. Tally semantics: `RESEARCH.md:2173` (Ctrl+L is the optional-voucher toggle) and `RESEARCH.md:2225` (only Cash Flow's *"Include Scenarios"* pulls Memorandum, Reversing Journal and Optional vouchers in — everywhere else Tally keeps them out). | **THE FIX IS THE PATTERN THIS PACKAGE ALREADY USES, APPLIED TO TWO MORE READERS.** (1) `backend/internal/integration/tallyxml.go` — add `IsCancelled`/`IsOptional`/`IsDeleted` to `TallyVoucher` and `IsDeleted` to `TallyLedgerMaster`, with the same `Deleted()`-style `strings.EqualFold(…,"Yes")` accessors the five master kinds have. (2) `backend/internal/migration/migration.go:247` — the second-pass loop skips a voided voucher the way `migration.go:384` already skips a deleted account group, and counts it. ⛔ **IT MUST BE COUNTED AND REPORTED, NOT SILENTLY DROPPED** — `ImportResult` has `Skipped`/`SkippedGUIDs` for GUID idempotency and that is the WRONG bucket (an operator reading "skipped: 2" would think they were already imported); this wants its own figure beside `vouchers_posted`, rendered on `TallyImportScreen` like the goods are, because *"3 posted"* against a file of 5 is the same class of silence as `stock_lines: 0`. ⚠️ **AND THE OPPOSITE DECISION IS DEFENSIBLE IF IT IS DISCLOSED**: importing them for the audit trail's sake is a choice somebody could make, but then the report must SAY they were posted as live entries. The drive's last arm asserts exactly that, so either fix turns it green. ⛔ **RELATED AND NOT THE SAME ROW:** `classByVchType` maps only the six built-in classes, so a `Memorandum` or `Reversing Journal` voucher — the other two kinds Tally keeps out of the books — aborts the whole import with *"unrecognised VCHTYPE"*, which is safe for the money and lands squarely in **D98**'s partial commit | ✅ **FIXED AND RE-DRIVEN 2026-08-27.** Skip-and-report, decided with the repo owner. `TallyVoucher` binds `ISCANCELLED`/`ISOPTIONAL`/`ISDELETED` with `Cancelled()`/`Optional()`/`Deleted()` accessors in the same `strings.EqualFold(…,"Yes")` shape the five master kinds use, plus `VoidReason()`; `TallyLedgerMaster` binds `ISDELETED` and gets its `Deleted()`. ⛔ **THE SKIP IS CHECKED BEFORE THE ACTION DISPATCH, DELIBERATELY** — a cancelled voucher is not money whether it arrives as a Create, an Alter or a Delete, and an ALTER resolving to a cancelled source would otherwise append a LIVE version to a voucher the source company had voided. ⭐ **IT IS COUNTED IN ITS OWN BUCKET AND IT IS ON THE SCREEN.** `VouchersVoided`/`VoidedVouchers`/`LedgersTombstoned` on the result, `vouchers_voided`/`voided_vouchers`/`ledgers_tombstoned` on the wire (never nil), and a report block headed *"Excluded as void in Tally: N"* whose note says they were **not posted, are not missing, and are not duplicates**. ⛔ It is NOT folded into `vouchers_skipped`, which means "already imported under this GUID" — the register said that bucket was wrong and it stayed wrong. ⭐ **MUTATION PROOF PREDICTED THEN OBSERVED**: with the skip disabled, `TestVoucherFlagsTallyExcludesFromTheBooks` reports *"3 vouchers posted, want 1"* and *"VouchersVoided = 0, want 2"* — **3 is exactly the pre-fix number the drive recorded.** The screen block has its own mutation proof too (blank it and the render test fails). ⭐ **RE-DRIVEN: `drive-u1-voided-vouchers.mjs` 12/12**, including *"NEITHER VOIDED VOUCHER APPEARS IN THE OPERATOR'S DAY BOOK"* and the ISDELETED ledger master not created. ⚠️ **NOT CLOSED BY THIS ROW:** `classByVchType` still aborts a whole import on a `Memorandum` or `Reversing Journal` VCHTYPE — the other two kinds Tally keeps out of the books. It is safe for the money and no longer lands in a partial commit now that D98 is fixed, but it is a separate row waiting to be written |
| **D102** | — | MEDIUM | ⛔ **THE PRODUCTION TOKEN VERIFIER DOES NOT CHECK `aud`, SO ANY TOKEN MINTED BY ANY CLIENT IN THE PINNED REALM IS ACCEPTED AS A CREDENTIAL FOR THE BOOKS.** Registered 2026-08-27 from `HANDOFF-FEATURE-LOCK-LINE.md` §2.8, which found it disclosed on the SSO FeaturesScreen row and **nowhere on this register** — the row argued the feature should be deferred (Tally has no SSO, so it is not a parity item) while the defect INSIDE its shipped half stayed unrecorded. ⛔ **NOT INFERRED FROM THE COMMENT — CONFIRMED IN THE TYPE.** `authn.Claims` binds `sub`, `email`, `companies`, `amr`, `iss`, `exp`, `iat`, `jti` and **has no `aud` field at all**, so the claim is not merely unchecked, it is never parsed; `grep -rn aud --include=*.go backend/internal/` returns exactly ONE hit in the whole tree and it is the comment admitting the gap. There is no `AUTH_AUDIENCE` env var anywhere in the repo. ⭐ **THE SURROUNDING POSTURE IS GOOD, WHICH IS WHY THIS IS THE ONE HOLE AND NOT A CLASS OF THEM**: keys cached by kid, asymmetric only (RSA/oct/`oct` HMAC and empty-kid JWKs skipped at parse time), fail-closed on fetch failure, a missing `exp` REJECTED, and `buildAuth` refuses to BOOT when `AUTH_JWKS_URL` is set without `AUTH_ISSUER` — with a sentence that names the exact reasoning (*"without a pinned issuer the JWKS verifier would accept tokens from any realm sharing the signing key"*). ⛔ **PINNING `iss` IS THE MITIGATION AND IT STOPS ONE DOOR SHORT.** Every client inside one Keycloak realm mints tokens carrying the SAME `iss`, so the issuer pin scopes acceptance to the realm and not to this application. A token a user legitimately obtained for ANY other client in that realm — another internal app, a public SPA, a service account — replays at cored and authenticates as that `sub`, after which the PDP grants on subject and company exactly as if the token had been minted for the books. ⚠️⚠️ **AND THE COMPENSATING CONTROL IS WRITTEN WHERE NO OPERATOR WILL EVER READ IT.** `jwks.go:31-35` says *"Deploy cored's realm with only trusted clients until an AUTH_AUDIENCE check lands"* — that is a DEPLOYMENT constraint living in a Go source comment. The only operator-facing production recipe in the repo, `docs/SESSION_HANDOFF_WAVE2.md:61-62`, hands over `AUTH_JWKS_URL=... AUTH_ISSUER=... /tmp/cored serve` and says nothing about the realm needing to be single-tenant. Nothing warns at boot. ⭐ **MEDIUM RATHER THAN HIGH, AND THE REASON IS THE DEPLOYMENT SHAPE, NOT THE SEVERITY OF THE HOLE**: the JWKS path is live only when `AUTH_JWKS_URL` is set, and this product has no such deployment today (`docs/TENANCY-AND-ACCESS.md:453` — first-party password login is what ships, and it runs on the dev signer with a single pinned issuer where `aud` is meaningless). ⛔ **It becomes HIGH the day anybody points this at a shared realm, which is the ordinary way Keycloak is run.** | `backend/internal/authn/authn.go` (`type Claims struct` — no `aud` member) + `backend/internal/authn/jwks.go:31-35` (the KNOWN GAP note) + `backend/internal/server/server.go:500-514` (`buildAuth`, which enforces the issuer pin and has no audience equivalent). **THE FIX IS SMALL AND ITS ONLY REAL DECISION IS WHAT TO DO WHEN THE VAR IS UNSET.** (1) add `Audience` to `Claims` — ⛔ it must be `json.RawMessage` or a custom unmarshaller, because RFC 7519 lets `aud` be EITHER a string OR an array of strings and a plain `string` field silently drops the array form, which is the form Keycloak actually emits; (2) check it in the same place the issuer is checked, against a new `AUTH_AUDIENCE`; (3) ⛔ **decide the unset case DELIBERATELY and prefer refusing to boot** — the `AUTH_ISSUER` branch of `buildAuth` is the precedent and it refuses, with a sentence explaining why, rather than warning and continuing. A verifier that accepts anything when a var is unset is the fail-open shape this function was written to avoid. ⚠️ Whichever is chosen, move the realm constraint out of the source comment and into the operator recipe at `docs/SESSION_HANDOFF_WAVE2.md`, because a mitigation nobody can read is not a mitigation. | ⛔ **OPEN — REGISTERED 2026-08-27, and no code has changed.** Registering is what a reading lane owes; the repair is a separate decision, and this one is gated on the unset-case question above rather than on any investigation. ⚠️ Status cell deliberately avoids the words a status parser matches on, per this section's own rule |

### ⛔⛔ §7.2 — THE BACK-DATED CUTOFF TOOK **SEVEN** DOORS TO CLOSE, AND THAT IS THE SESSION'S MAIN LESSON

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
| 7 | ⛔⛔ **THE ENCODING.** `isTallyEnvelope` — the gate BOTH XML peeks pass through — decoded the RAW BYTES. Every real TallyPrime export is **UTF-16LE**, so it answered `false` ("no voucher can hide in this body"), the cutoff did not apply and the step-up named nothing — **while the importer read the same bytes and posted every voucher in them** | ⛔ found 2026-08-25 by MEASURING both ends on one corpus fixture: importer `12 messages, 1 voucher, DATED` vs peek `nil`. Driven at the door: a back-dated UTF-16 import answered **`200`, `"vouchers_posted":1`**, and the voucher landed in the May day book inside a period closed at 2026-07-01 | ✅ `isTallyEnvelope` decodes through `integration.NewTallyDecodeReader` |

> ### ⛔⛔ THE ONE SENTENCE TO TAKE FROM THIS
> **Every one of the seven doors was a place where an AUTHORIZATION PEEK RE-IMPLEMENTED A DECODING RULE
> THE HANDLER GETS FROM A LIBRARY.** For doors 1–6 that library was `encoding/json`; for door 7 it was
> `integration.NewTallyDecodeReader`, and the rule was *which bytes are even text*. The PDP and the
> handler have to agree about what a body SAYS; wherever they disagreed, the disagreement WAS the hole. A
> peek that decides a write's fate must decode the body the way the handler decodes it — same call, same
> semantics — or it is a second implementation and it will drift.

> ### ⛔ DOOR 7 ALSO CARRIES A LESSON THE OTHER SIX DO NOT, AND IT IS ABOUT THE TESTS
>
> Door 3 (a non-JSON body) was closed with an XML peek and then tested **hard** — three suites, dozens of
> hostile inputs: the earliest voucher in a batch, a loose `<DATE>`, a truncated envelope, an
> `ACTION=Delete` carrying a legal date, a masters-only envelope that must stay `nil`. Every one of those
> bodies is **UTF-8**, and **no real Tally file is.** The control was tested exhaustively along the axis of
> hostile CONTENT and never once along the axis of ENCODING — the axis the product it exists to read
> actually varies on.
>
> ⚠️ **AND T32 BELIEVED IT HAD ALREADY FIXED THIS.** It found both peeks blind to a real export, fixed the
> ENVELOPE SHAPE in each, and recorded the seventh door as closed. That fix is *downstream* of
> `isTallyEnvelope`, so it never ran. **Fixing stage two of a pipeline is worth nothing while stage one
> returns false** — and a session that has just fixed something is the least likely to re-measure it.

⚠️ **AND THE GUARD THAT MATTERS MORE THAN THE SIX FIXES.** Rounds 1–4 each asserted a *list* of hostile
inputs, and the next bypass was always off-list. Two structural guards now exist instead:
- `assertPeekIsNoLooserThanTheHandler` (in `auth_backdated_key_binding_test.go`) decodes the same bytes
  into the REAL wire structs with the REAL decode call and fails if the peek is nil-where-the-handler-bound
  -a-date, or later than what it bound. **It asserts the property, not the examples.**
- `TestEveryWriteRouteNamingAnExistingDocumentIsAccountedFor` walks the REAL chi router and requires every
  write route naming an existing document to be recognised or explicitly excused with a reason. **A new
  route cannot be mounted in silence.**
- `TestEveryDateShapedWireFieldIsAccountedForByTheCutoff` parses package `server` and makes somebody
  classify **every date-shaped json key on every wire type** as judged-or-excused-with-a-reason.
- ⛔ **`TestTheCutoffSeesEveryTallyFileTheImporterCanRead` (new, door 7)** — and it exists because the gate
  directly above it is *structurally* blind to this route, twice over: the Tally date is not a json key
  (it is a `<DATE>` element) and it is not in package `server` (it is `integration.TallyVoucher`). So the
  ONE write route whose body is not JSON, and which posts more vouchers per request than every other route
  combined, sits outside the universe that gate quantifies over. The new property quantifies over the axis
  this route actually varies on: **for every corpus fixture, in every encoding, what the cutoff sees must
  equal what the importer would post** — both ends computed independently and compared, so a new envelope
  shape, a new encoding, or either end being changed without the other fails there naming the file.

### §7.3 — ✅ **CLOSED. A ✅, B ✅, C ✅, D ✅** (A/C/D 2026-08-25 · B's server half 2026-08-25 — see §7.6)

⛔ **This heading used to read "STILL OPEN AND HIGH PRIORITY … NOT fixed" and it must be
kept honest as rows close** — a stale heading over a table of resolved rows sends the next
session looking for work that is done, which has cost this repo a session before.
**Nothing on this table is open.** B's server half — cored recording a draft acceptance in
the same transaction as the voucher — landed the same day the handoff that ordered it was
written, and the two-voucher test that PINNED the residue was deleted and replaced with its
inverse, as that pin instructed.

| # | Defect | Where | Why it was not fixed |
|---|---|---|---|
| **A** | ~~⛔ **The bank-reconciliation date is NOT covered by the cutoff, and `routeAction`'s comment SAYS IT IS**~~ — ✅ **FIXED (F7)**, see §7.4. Both ends are now judged, and the comment that asserted the control now describes it. ⚠️ **`peekBankDate` is NOT a copy of `peekDepositDate`, and that difference was a REAL door:** the challan's store parses `deposit_date` with exactly `2006-01-02` so a loose spelling dies at 400, but `banking.SetBankDate` hands the string straight to Postgres as `$6::date` — which **accepts `2026-5-20`** — so an unreadable-but-storable bank date has to fail CLOSED | `backend/internal/server/auth_middleware.go`, the `/bank-reconciliation` arm | — |
| **B** | ✅ **CLOSED 2026-08-25 — BOTH HALVES. See §7.6.** The CONFIRM (`POST /drafts/{id}/accepted`) was fire-and-forget with a `console.warn`, so a confirm that died after the voucher landed lost the only durable record of the acceptance and the claim's TTL became the duplicate's timer. **The client half** writes the confirm to a durable queue BEFORE attempting it, removes it only on an acknowledgement (a resolve, or a 409 = insightd already holds it), and retries on the next accept AND on every Bill Inbox open, awaiting the drain when anything is owed so layer 4's read is not racing it. ⛔ **THE SERVER HALF — WHICH IS WHAT ACTUALLY CLOSES THE ROW:** the voucher POST now carries `draft_id`, cored writes it to `voucher_head.source_draft_id` under a **PARTIAL UNIQUE INDEX** (migration 0077) **in the same transaction as the voucher**, and a second accept rolls back with no voucher, no numbering advance and no input tax, answering 409 `draft_already_accepted` in insightd's own body shape. ⚠️ **The two-voucher pin in `draftAccept.test.ts` went RED exactly as it predicted and was DELETED**, replaced by `⛔ THE DOOR IS SHUT: a DIFFERENT browser with a lost confirm gets ONE voucher` | `frontend/src/screens/vouchers/draftAccept.ts` (layer 6, `draftAlreadyAcceptedOnTheServer`); `backend/migrations/0077_voucher_source_draft.sql`; `internal/ledger/ledger.go` (`SourceDraftID`, `DraftAlreadyAcceptedError`); `internal/server/server.go` (`voucherReq.DraftID`, `writeDraftAlreadyAccepted`) | ⛔ **WHY A COLUMN ON `voucher_head` AND NOT A LIFECYCLE WRITE INTO `insightd_draft`** — the three reasons are in 0077's header and the third is the decisive one: **a unique index is enforced by the index, not by a visible row.** `insightd_draft`'s RLS is pinned to the RAW text GUC and is `FORCE`, so an UPDATE there can silently affect zero rows and present exactly as the duplicate it was meant to prevent; and every lifecycle field of that table lives inside an untyped `body` JSONB owned by insightd's Python |
| **C** | ~~A `?company=` that fails the client-side uuid regex is **silently discarded and somebody else's books are opened instead** — real, populated books, nothing on screen looks wrong~~ — ✅ **FIXED 2026-08-25, see §7.6.** `readCompanyIdFromURL` folded "not a uuid" into "no `?company=` at all" (both returned `null`), so the bootstrap read localStorage next. It is now a TRI-STATE — `absent` / `malformed` / `ok` — and `malformed` is TERMINAL: nothing sent, nothing opened, nothing minted, storage untouched, and a notice naming the value that was ignored. ⚠️ An EMPTY `?company=` stays `absent` on purpose, because `App.tsx`'s `readCompanyIdInURL` (which gates D25's invite adoption) also reads an empty value as "no preference" — two readers disagreeing about one URL is how this class starts | `frontend/src/app/CompanyContext.tsx` | — |
| **D** | ~~A well-formed but NONEXISTENT `?company=` uuid now **wedges permanently**~~ — ✅ **FIXED 2026-08-25, see §7.6.** ⛔ **THE DIAGNOSIS IS CONFIRMED AT HEAD AND IS NOT GOING AWAY:** `auth_middleware.go` checks `Membership` before any handler can look for the row, so a uuid naming a company that never existed answers **403, not 404** — deliberately, so a non-member cannot enumerate tenants. The 404 arm of `isDeadCompanyId` is therefore dead code against cored, and it is KEPT (the in-memory mock answers it) rather than pretended into a valve. The refusal is unchanged — no mint, no substitution — and it grew what it never had: an EXIT. The notice now (a) states that a 403 cannot distinguish "not a member" from "no such company", so "claim your invite and reload" is not offered as the whole story, (b) says a reload cannot change the answer, and (c) carries one control that drops `?company=` and re-runs the bootstrap in place — `replaceState`, not a reload, so Back cannot walk the operator onto the refusing URL | same | — |

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
| **Work landing in the WRONG COMPANY** — a `?company=` the server 403s was silently replaced by a NEW tenant with the user as Owner (7 such tenants were minted during this audit); and **F3 Switch Company was never persisted**, so a reload silently returned the operator to the old company and the next voucher landed there | refuse legibly and mint nothing (URL *and* localStorage doors); the switch persists, with the precedence documented | ✅ **FIX_HOLDS** — no `My Company` minted, and a voucher keyed after an F3 switch + reload lands in **B**; ✅ **and the two doors of the same class that remained — §7.3 C and D — are now CLOSED too (§7.6), so this class has no open door left** |
| **Invites reached NOBODY while the screen said "invitation sent"** — 52 outbox rows at `sent`, **41 with a recipient containing no `@`** | recorded as terminal `failed` + `NoAddressReason` (⛔ no migration: `status` is CHECK-constrained, so the honesty lives in the pair); `role="alert"` added | ✅ **FIX_HOLDS 10/10** — polled 39s, the lie is gone at both sample points; DB reads `failed` / `no email address on file` |
| **One supplier bill posted TWICE from the Bill Inbox** — four guard layers all asked "is this accepted?" at the same wrong MOMENT; two browsers raced → two real vouchers | reordered to **claim → post → confirm** (claim taken BEFORE any ledger master is created, released on a failed post, TTL so a dead browser strands nothing); insightd CAS; 14 tests the server half had shipped without | ✅ **FIX_HOLDS — the race it was built for is CLOSED** (two browsers 13ms apart → ONE voucher, loser `claim 409`, anti-stranding verified) **and the SEVENTH door, §7.3 B, is CLOSED TOO**: the lost confirm is queued durably and retried, AND cored now records the acceptance on the voucher itself (`voucher_head.source_draft_id`, migration 0077) **inside the voucher's transaction**. ⛔ **Driven from a SECOND browser context with no localStorage, on a NUMBERLESS bill, with the claim lease waited out: the voucher POST is answered 409 `draft_already_accepted` and the tenant holds ONE purchase. Against a HEAD-equivalent client the same arm reads `2 purchase(s)`** |
| **Bill-wise references silently dropped** from an accepted OCR draft while the panel certified "referenced in full (21240.0000)" | carried through to the wire | ✅ driven — `bill_allocation` now carries `PF/2026-27/0117 / NEW / C / 21240.0000`, postings correctly oriented (**Input** IGST debited on a 27→29 purchase) |
| **>1 MiB Tally XML imports were truncated** — every write's body was capped at 1 MiB while this route allows 32 MiB, and the cap REPLACES the body, so the importer parsed a document cut off mid-envelope | cap is now per route, matched on trailing path segments so it cannot silently miss outside a mounted chi route | mutation proof exact: `restored 1048576 bytes, want the whole 3145749 … does not end in </ENVELOPE>`. ⚠️ **Pre-existing, NOT collateral from the cutoff work** — `drainBody` has always been unconditional |
| **F11 claimed a per-company negative-stock opt-in that does not exist** — `allow_negative_stock` is read and honoured by both enforcers and can be written by NOTHING (no route, no request field, no setter, no UI) | the claim is now honest and says what is missing | the flag itself is still unwritable — a small build, §7.1 |
| **F8 — the ageing report's `as_on` did not decide what was IN it, and it had no ageing on screen** (D12, D65, half of D13). Outstandings honoured `as_on` for AGEING and not for INCLUSION, so it and the Balance Sheet were two reports over one set of books that disagreed by 86,720.00, each footing, with nothing to reconcile them. Separately the SCREEN dropped `p.bills` — the array the server always sent — so nothing anywhere in the product could show WHICH invoice was open or how overdue it was, which is why bill-wise defects reached a browser undetected | one date predicate, spelled the way `movementSQL` spells it (so a NULL-dated voucher is treated as it is everywhere else), plus `openingIsInScope` — `queryBalances`' books-beginning guard applied to the other place an opening position hides. The screen renders each bill at depth 1 (visible in the CONDENSED view, unlike the Balance Sheet's Closing Stock at depth 2) with its due date and its ageing IN DAYS. ⛔ **A test was PINNING the defect and was changed deliberately:** `forecast_db_test.go` dated an invoice a month AFTER the date it projected from and wrote 1770 down as the expectation — a cash-flow projection that already knows about an invoice raised next month is hindsight, not a forecast | ✅ **FIX_HOLDS 8/8 in a real browser** (⚠️ **the first run was against a STALE BINARY and said the total did not move** — §0.1's symbol check caught it, which is the second time that procedure has paid for itself): the bills render with due dates and days overdue, the party rows survive, and F2 → 2020-04-30 takes the total from **2,53,920.00 to 0.00**. On the wire the two reports now AGREE at both dates. 3 executed mutation proofs |
| **F7 — a BANK DATE could be asserted into a closed period** (§7.3 A), while `routeAction`'s comment said the cutoff already reached it. The third route of that class after the pay run and the challan, and the second to ship with a comment asserting the missing control | `peekBankDate` (the destination) + `storedBankDateForLeg` (the source, because the PUT is an UPSERT and the DELETE withdraws) — **both ends, as round 3 established for the voucher ALTER/DELETE**; the refusal now names the operator's own field (`authz.Request.DateSubject`). ⛔ **AND THE STRUCTURAL GUARD THIS CLASS NEVER HAD:** `TestEveryDateShapedWireFieldIsAccountedForByTheCutoff` parses package `server` and requires **every date-shaped json key on every wire type** to be classified as judged-or-excused-with-a-reason — the three defects were invisible at ROUTE granularity, which is why the existing router walk could not see them — and `TestTheCutoffActuallyReadsEveryDateItClaimsToRead` **executes** every "judged" claim through the real `voucherDateFromRequest`. 3 executed mutation proofs, each predicted then observed | ✅ **FIX_HOLDS 8/8 on the wire** against a binary verified fresh BY SYMBOL, on a seeded tenant: assert-into 403 · loose spelling 403 · trailing byte 403 · move-out 403 · withdraw 403 · **CONTROL** un-tick after the cutoff 200 · **CONTROL** old cheque cleared today 200 · journal control 403. ⛔ **DB oracle, as `postgres`:** two rows survive — `2026-04-02 / 2026-04-05` untouched and `2026-05-20 / 2026-08-01` from the allowed tick — **and not one `attack` row**, so no refusal wrote |
| **An accepted draft did not name the voucher it became** — insightd sent `accepted_voucher_no`, the wire returned it, the type declared it, and the inbox row never rendered it | the row now reads "already posted as … — do not re-enter" | found by the final re-drive; the operator-visibility half of the duplicate defect |

### ✅ THE THREE DEFECTS DELIBERATELY NOT FIXED — **ALL THREE ARE NOW FIXED** (re-checked 2026-08-25)

⛔ **THIS LIST WAS STALE ON ALL THREE ROWS, AND IT IS THE THIRD STALE "STILL OPEN" CLAIM FOUND IN ONE
DAY** (the others: §0's Row 20 entry, and `maxXMLBody`'s memory figure). A list of things deliberately
NOT fixed is the most dangerous kind to leave stale — a session reads it, believes there is known
broken behaviour, and either works around it or rebuilds it. **Re-check by reading the code, not this
list.** Each row below records what was verified and how.

1. ~~**D-row-29 / D8, closing stock replayed in LEXICAL `voucher_no` order**~~ — ✅ **FIXED.** The claim
   named `inventory/valuation.go:155-170` and the line
   `if a.VoucherNo != b.VoucherNo { return a.VoucherNo < b.VoucherNo }`. **That line does not exist**;
   `grep -rn "a.VoucherNo < b.VoucherNo" internal/inventory/` returns nothing. `less()` now orders on
   `(voucher_date, VOUCHER_ID, version_created_at, line_no, voucher_version_id)` — voucher_id is a
   UUIDv7 minted at CREATE, so it is entry order per company across every series and an ALTER keeps it.
   The code comment carries the reasoning and the measurement (601.4681 too high on an unedited issue).
   D8 is CLOSED in §7.1. ⚠️ **CONSEQUENCE FOR PLANNING: T9 FIFO/LIFO is NO LONGER BLOCKED BY THIS.** The
   "serialise it with T9 because it moves the Balance Sheet" instruction was about this fix, and this
   fix has landed and been re-pinned.
2. ~~**Row 20's masters gap**~~ — ✅ **CLOSED (L13) 2026-08-21.** It was a build, and the build had
   landed; it had never been driven. See §7.5. §0's still-open table disagreed until 2026-08-25 and was
   the stale half.
3. ~~**Row 24's `allow_negative_stock` writer**~~ — ✅ **FIXED (D5 + D43) 2026-08-22.** The claim was
   "no route, no request field, no setter, no UI". All four now exist: `PUT /v1/companies/{id}` →
   `handleSetCompanyFeatures` (`handler_company_get.go`), `AllowNegativeStock *bool` on the request,
   and an **ALLOW NEGATIVE STOCK panel at the top of `FeaturesScreen.tsx`** wired through
   `client.setCompanyFeatures`. The route is deliberately narrow — it writes this one column and is not
   a general company editor, because name/FY/state/currency are identity that posted books depend on.

> ⚠️ **SO THERE IS NO KNOWN-BROKEN-AND-UNFIXED BEHAVIOUR IN THIS PRODUCT AS OF 2026-08-25.** §7.1 is
> 97/97/0/0, §7.2's seven doors are closed, §7.3's four rows are closed, and this list is empty. The
> ONLY thing that is permanently unshippable is **T18 (rule 48(4) QR)** — it is signed with the Income
> Tax Department's private key. **Reject any session that reports it done.**

## 7.6 ✅ §7.3 B / C / D — THE LAST THREE ROWS OF §7.3, WORKED 2026-08-25 (B FINISHED THE SAME DAY)

The round-4 lane that owned C and D was killed by a session limit before starting, and B
had been left as "a design decision, not a patch" since the D15 audit. All three are
frontend, and C/D are one file.

⛔ **Both proofs for every row, per §8:** an EXECUTED mutation proof whose failure was
predicted then observed, **and** a drive in a real browser — and here the drive was run
against `git show HEAD:` FIRST, so the defect is reproduced before it is fixed. A probe
that has never failed proves nothing.

### C + D — `frontend/src/app/CompanyContext.tsx`

| | |
|---|---|
| **Mutation proofs** | **3, each predicted then observed.** M1 fold `malformed` back into `absent` → **4 RED**, and the first one fails on the DANGER (`expected '7a3f1c2e-…' to be 'none'` — the stored company opened), not on a missing notice. M2 revert the URL-door wording → **exactly 1 RED**. M3 make the exit a no-op that only hides the notice → **3 RED**, the shape the `retryTick` comment warns about |
| **Browser drive** | `frontend/scripts/drive-7-3-cd-company.mjs`, on the demo tenant with cored `:8087` + vite. ⛔ **AT HEAD: 7/16, and the two lines that matter read `C: the stored company is NOT opened behind a malformed ?company= — THE DEFECT: Demo Traders opened anyway` and `D: a reload really does repeat the refusal (the 404 valve never fires) ✓` with no exit control on the page.** WITH THE FIX: **16/16**, including a CONTROL that a good `?company=` still opens those books |
| **Unit tests** | 10 new in `CompanyContext.test.tsx` (20 in the file, all green). One pins the LIMIT deliberately: an empty `?company=` stays "no preference", because `App.tsx` reads it that way for D25 |

⚠️ **The D fix does NOT revive the 404 arm and must not be read as doing so.** cored's
403-before-404 is correct and stays; what changed is that the refusal now has a door out
of it. The arm is kept for the in-memory mock, and the code says so at the predicate.

### B — the pending-confirm queue

| | |
|---|---|
| **Mutation proofs** | **4, each predicted then observed.** B-M1 remove the enqueue → **8 RED** (⚠️ I predicted 7; the eighth is the LIMITATION test, which also reads the queue — the observed set is a superset and the extra one is correct). B-M2 kick the drain off un-awaited → **2 RED**, and ⚠️ **`THE DOOR` stayed GREEN under it**, which is exactly why the separate `the drain is AWAITED` test exists: the door test is not sensitive to the race, so it cannot be the guard against it. B-M3 stop treating 409 as an acknowledgement → **1 RED**. B-M4 never clear a settled confirm → **1 RED** |
| **Browser drive** | `frontend/scripts/drive-7-3-b-confirm.mjs` — the confirm is killed **at the network layer** (`page.route(...).abort()` on `POST **/drafts/*/accepted`) AFTER the voucher POST succeeds, because that is how it was reported. A stubbed port would prove the stub |
| **Unit tests** | 11 new in `draftAccept.test.ts` (71 in the file, all green) |

### B, THE SERVER HALF — ✅ **THE ROW IS CLOSED** (same day; migration 0077)

**What was built.** The voucher POST carries `draft_id`; cored writes it to
`voucher_head.source_draft_id` under a **partial unique index**, *inside the voucher's own
transaction*. A second accept trips the index, the transaction rolls back, and the loser has
written **no voucher, no numbering advance, no bill reference and no input tax credit**. The
refusal is 409 in **insightd's own body shape** (`detail.code = draft_already_accepted` plus
`accepted_voucher_no`/`accepted_voucher_id`), so `draftAccept.ts` translates ONE refusal shape
rather than two, and the operator is told which voucher their bill is already on.

⛔ **WHY IT IS `voucher_head` AND NOT A WRITE INTO `insightd_draft`.** That table is in the same
database, so a lifecycle write was *physically* available. Three reasons it is not the answer,
and the third decides it:

1. every lifecycle field lives inside an untyped `body` JSONB **owned by insightd's Python** —
   a second writer with no schema holding the two sides to the same field names;
2. its RLS is pinned to the **raw text GUC** and is `FORCE`, so any casing or format drift makes
   the row invisible **to the writer** and the UPDATE silently affects zero rows — presenting
   exactly as the duplicate it was meant to prevent;
3. ⛔ **a unique index is enforced by the index, not by a visible row.** The guarantee therefore
   does not depend on the writer being able to READ the winner. It is the same shape 0008 already
   uses for the Tally import (`source_guid` + a partial unique index), not a second invented one.

| | |
|---|---|
| **Mutation proofs (server)** | **6, each predicted then observed.** M2 drop the `op != OpCreate` gate → **1 RED** (an ALTER repoints provenance). M3 stop recognising the constraint → **2 RED**. M4 head upsert forgets the column → **3 RED**. M5 `buildVoucherInput` drops `draft_id` → **1 RED, reading `second accept = 201`** — the reachability defect verbatim. M6 fall back to cored's own `{"error"}` 422 → **1 RED**. ⛔ **M1 IS THE INTERESTING ONE AND IT DISPROVED MY OWN COMMENT:** dropping the index's `WHERE source_draft_id IS NOT NULL` left **every test GREEN**, because a Postgres unique index is `NULLS DISTINCT` by default. The partial clause is about index SIZE, not correctness; what keeps hand-keyed vouchers unconstrained is `nullIfEmpty`. Re-run as M1b (store `''` instead of NULL) → **predicted 1 RED, observed 3** — 0077's non-empty CHECK also refuses the `''` an alter and a delete restate. **Both the test's rationale and the migration comment were corrected to say this** |
| **Mutation proofs (client)** | **4, each predicted then observed, 1 RED each.** M7 stop sending `req.draft_id`. M8 mock stops enforcing once-only. M9 the refusal no longer teaches insightd. M10 the 409 falls through as a generic failure |
| **Go tests** | 5 new in `internal/ledger/draft_acceptance_test.go` (the DB guarantee, incl. that the numbering counter does not advance and that acceptance is terminal across a DELETE) + 2 new in `internal/server/draft_accept_http_test.go` (the wire: reachability, and the refusal shape the client parses). `go test ./...` **exit 0** |
| **Browser drive** | `drive-7-3-b-confirm.mjs` grew **arm 5** and went 13 → **22 arms**. ⛔ **Arms 1–4 all run in the browser that POSTED, so all four would stay green against a server that knew nothing** — arm 5 is a SECOND browser context (no localStorage), a NUMBERLESS bill (so the day book has nothing to match on), the 120-second claim lease **waited out** (otherwise the refusal comes from layer 5, a different door), and it asserts **on the wire** that cored answered the voucher POST 409 `draft_already_accepted`. **22/22 with the fix. Against a HEAD-equivalent client: 19/22, predicted 3 RED and observed exactly 3, reading `2 purchase(s) on 2026-08-16`** — one supplier bill, two real vouchers, reproduced end to end |

⚠️ **AND THE SAME VACUOUS-ARM LESSON BIT AGAIN, IN THE NEW ARM.** `the refusal RECONCILED
insightd` was GREEN on the HEAD-equivalent run — because the second browser SUCCEEDED and its
own ordinary post-accept confirm marked the draft `accepted`, while the three arms above it were
RED and the tenant held two vouchers. It is now gated on `posts[0].status === 409`. **That is the
second time in two sessions that measurement, not inspection, found a green arm asserting
nothing — treat "run every new arm against the broken build" as the rule it is.**

⚠️ **THE FIX HAD TO NOT WEDGE THE BILL.** Closing the duplicate door leaves a draft cored refuses
and insightd still offers — the §7.3 D shape the same session had just fixed. So the refusal path
**teaches insightd**, through the same durable queue every other confirm uses: the second machine
delivers the fact the first one never managed to. Driven (arm 5's last row) and unit-tested.

⚠️ **One drive arm was found VACUOUS and was gated, by measurement not by inspection:**
`nothing is owed any more` was GREEN at HEAD (where the queue never holds anything) while
the three arms around it were RED. It now asserts the debt EXISTED and then went.

⚠️ **Two drive arms were written against the wrong status name and were corrected:** this
fixture lands as `low_confidence_manual`, not `ready`. Asserting a specific non-terminal
name fails on a fixture detail and says nothing about §7.3 B, so the arms assert
`!== "accepted"` and `accepted_voucher_no` absent — which is the actual defect state.

✅ **THE ROW IS CLOSED.** The client queue is still per-browser-profile and always will be —
that is what localStorage is. It is no longer load-bearing: it is the CHEAP path, and layer 6
is the guarantee. `draftAccept.test.ts`'s two-voucher pin went RED exactly as it predicted,
was DELETED as it instructed, and its place is taken by
`⛔ THE DOOR IS SHUT: a DIFFERENT browser with a lost confirm gets ONE voucher` — the same
scenario, nothing softened, asserting ONE voucher and a refusal that names it.

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

### ✅ L7 — PRINTING. ELEVEN ROWS. CLOSED 2026-08-20, AND IT FOUND A NEW DEFECT.

**The oracle is a PDF that names itself**: `%PDF` … `%%EOF`, uncompressed streams, and the document's own
number, period and party read back out of the bytes.

⛔ **TWO THINGS ABOUT THE ORACLE, BOTH OF WHICH COST A DIAGNOSIS.**
1. **Playwright's `response.body()` comes back EMPTY for the PDF streams** — 0 bytes on a 200 that really
   is 6KB of paper (a 501's JSON body reads fine). So the probe records the URL **with its query string**
   and re-fetches the bytes server-side. That is the stronger oracle anyway: **the browser proves what the
   client SENT, the refetch proves what the paper SAYS.**
2. ⛔ **THE FIRST RUN RECORDED `new URL(u).pathname` AND THREW THE QUERY AWAY — and the query string IS
   D34.** A probe that drops it can only ever pass.

**Mutation proofs, each predicted then observed:**

| Mutation | Anchor put back | Result |
|---|---|---|
| M11 (D34) | `return query` before any date is added | **6 of 70** printTarget tests RED |
| M12 (D61) | footer reads `voucher version(s)` again | `TestTheDayBooksFooterIsCutOffMidSentence` RED |
| M13 (D33) | publish effect keyed on `[activeKey]` alone | `DrillRows` RED |
| M14 (D31) | debit-note arm stops reading the bill reference | `TestNoDebitNoteInThisProductCanBePrinted` RED |
| M15 (D30) | `usePrintAction` removed from `StockVoucherScreen` | **10 of 41** RED |
| M16 (D29) | the `/back-dated cutoff/` discriminator → `false` | **7 of 14** RED |

**The drives — `drive-l7-print.mjs` 28/28 and `drive-l7-cutoff.mjs` 7/7:**

- **D34** ✅ Both report classes, and the paper agrees with the query. A **position** report sends
  `?as_on=2026-06-30` and prints *"As at 30-Jun-2026"*; a **period** report sends
  `?from=2026-04-01&to=2026-06-30` and prints *"From 01-Apr-2026 to 30-Jun-2026"*. Never *"all dates in
  the books"*. ⚠️ **The paper spells a date `30-Jun-2026`, not ISO** — the first run searched for the ISO
  form and called a working fix broken.
- **D61** ✅ The footer is on the paper, complete to *"…would count the same money twice"*, and counts
  **`14 voucher(s)`**. ⛔ **The probe had been searching for `voucher version(s)` — D61's own FALSE
  wording, which the fix removed.** A probe that greps for the defect's text can only pass while the
  defect lives.
- **D30 / D75** ✅ Driven in the browser: a seeded Delivery Note drilled from the Day Book, **Ctrl+P
  reaches the server**, a 3,765-byte PDF comes back naming its **consignee** with a **"Received by"**
  line, filename `delivery-note-1.pdf` — **no raw UUID**.
- **D31** ✅ On the wire: the debit note prints **200** and its paper reads *"Against Invoice No.:
  **SUP-101** dated 20-Apr-2026"* — the against-number that used to be unreachable. Credit note 200 too.
- **D62** ✅ Paper and screen are the same report now: all four movement columns on the paper, and the
  screen carries `Opening Value` and `Inward Value`.
- **D63** ✅ The account is NAMED: *"Printed Bank Reconciliation Statement — **HDFC Bank**"*, and the wire
  carries `?ledger_id=`. ⚠️ **The fix did NOT remove the auto-select** (on a one-bank company that is
  right) — it made the status line say which account went to paper, so the ORACLE IS THE NAME, not the
  refusal. The probe had to be corrected to match the fix rather than the report.
- **D35** ✅ Ctrl+P on the TDS Register is never the shell's generic *"does not apply"* — it reaches the
  route and states the server's own 501.
- **D76** ✅ The Cost Centre Summary's refusal no longer claims Alt+E is unbuilt there.
- **D29** ✅ Driven with a real cutoff (`backdated_cutoff = 2026-07-01` on the Owner level, set over the
  admin API). A delivery note dated **2026-05-15** is refused **403**, and the screen says *"voucher date
  is before the back-dated cutoff of 2026-07-01. **This is a CLOSED PERIOD, not a missing permission**"* —
  never *"ask an Owner to allow…"*. RULE 4: no voucher written.
- **D33** ✅ Drill row 0, back out, re-open the Day Book, drill **row 3** — the voucher that opens is row
  3's (`Steel Suppliers`), not the frozen first target.

### ⛔⛔ NEW DEFECT FOUND BY THIS DRIVE, AND FIXED IN THE SAME COMMIT: A DELIVERY NOTE COULD BE SAVED AND THEN NEVER PRINTED

**D30 gave the Delivery Note its Ctrl+P back — and every delivery note on the demo books answered 422.**
`validateVoucherDoc` requires *"the other party"* on **every** printable kind, and **nothing on the
writing side required it**: not `buildStockVoucherRequest`, not the screen, not the wire. All four seeded
delivery notes carried a NULL party. **So the one stock class in `PRINTABLE_VOUCHER_CLASSES` could not
print a single document, and D30's fix was unverifiable on demo data.**

⛔ **THIS IS D60'S CLASS ON ANOTHER SCREEN** — a document that can be keyed and then not printed, learned
at the counter with the customer waiting — so it is fixed the way D60 was: **a refusal on the form, scoped
by what the printing layer actually requires**, with the reasoning stated per class (delivery-note
REQUIRES one; receipt-note is not printable; a stock journal has no other party by construction; a stock
take has nobody on the other side). Proved: the guard → `if (false)` turns the two new assertions RED, and
a party-named delivery note **prints 200, names "Acme Traders", carries "Received by"**.

**And the SEED was wrong in the same place.** Its four delivery notes named the customer in the
*narration* and passed no `party`. They now pass one, and **`verify:demo` carries a new 34th check** — *a
seeded Delivery Note PRINTS, names its consignee, and has a signature line* — because a seed whose own
documents cannot print makes every printing fix unverifiable, which is exactly how this survived the
session that fixed D30.

⚠️ **SIX TESTS WERE PINNING THE OLD BEHAVIOUR** across three files, and each was changed deliberately: the
subject of every one is the stock movement, the order link, the print route or the 403 rendering — **not
the party** — so the consignee is keyed in each file's helper, where a precondition belongs.

### ⚠️ TRAPS THIS LANE ADDS TO THE LIST

1. ⛔⛔ **`git checkout --` AFTER A MUTATION PROOF WIPES AN UNCOMMITTED FIX.** It happened here: the
   consignee fix was reverted along with its own mutation, and only the four tests left behind (now
   failing) revealed it. **COMMIT THE FIX BEFORE MUTATION-PROVING IT**, or the proof destroys the thing it
   is proving.
2. **The Day Book has NO class column**, and its particulars cell shows the **party** when there is one
   and the narration otherwise — so giving the seeded delivery notes their consignees *changed how they
   read in the Day Book*, and two runs of the probe failed to find them. Match by shape (a stock-only
   voucher has 0.00 on both sides) and **confirm the class by the heading after drilling**.
3. **A `null` `backdated_cutoff` is "leave unchanged", not "clear".** `parseCutoff` treats `""` as CLEAR
   and the field is a `*string`, so `{"backdated_cutoff":null}` answers **200 and changes nothing**. Send
   `""`. A cutoff left set on a drive tenant 403s every back-dated write in the next lane.

### ✅ L6 — THE VOUCHER FORM. D39, D41, D59, D60. CLOSED 2026-08-20, AND D41'S DRIVE FOUND **D79**.

**Mutation proofs:** M17 (`f.baseLedgerId ||` → `"" ||`) turns the two new alter tests RED with the
CONTROL green. D39/D41's form-side controls are proved by the L12 wire assertions (Ctrl+A on a refused
form makes **no request and mints no ledger**) and by M9/M16.

**The drives — `drive-l6-form.mjs` 11/11, `drive-l6-alter.mjs` 5/5:**

- **D59** ✅ Driven on a payment **keyed through the single-entry form**, not a seeded one — which is where
  the defect lived. Account `HDFC Bank`, particular `Office Rent 1750`, saved through the passkey, and the
  paper reads **"Paid To Office Rent"**. Never the bank. Read back as `postgres`:
  `Cr HDFC Bank 1750.0000 / Dr Office Rent 1750.0000`, and **`party_ledger_id` is NULL with the party TEXT
  = "Office Rent"** — the bank is no longer in both fields, which was the root cause.
  ⚠️ The seeded receipt and payment print *"Received From Acme Traders"* / *"Paid To Steel Suppliers"*
  too, but those were never the defect: the seed always passed a real party.
- **D60** ✅ A Sales invoice with no HSN is refused **by the form, before the wire** — no voucher POST and
  **no ledger POST** — citing *"a MANDATORY particular of a tax invoice (CGST Rules 2017, Rule 46(g)), so
  an invoice saved without one cannot be printed"*.
- **D41** ✅ A seeded Purchase drilled from the Day Book opens as `Purchase Voucher (Alteration)`, Ctrl+A
  saves (403 → step-up → **200**), and **the chart of accounts is the same size afterwards (26 → 26)**.

### ⛔⛔ D79 — WHAT THE D41 DRIVE ACTUALLY FOUND, AND IT IS THE WORST DEFECT THIS SESSION HAS SEEN

D41's own assertions passed. **The ledger COUNT did not: 26 → 27.** Reading the books back:

```
version 1:  Dr Raw Material Purchases  30000.0000
version 2:  Dr Purchases               30000.0000     <- an alter that edited NOTHING
```

`buildGstRequest` ensured the ORIENTATION'S CANONICAL base on every accept, so **any accounting-mode GST
voucher booked to a head other than "Domestic Sales" / "Purchases" was silently re-pointed the first time
anyone opened it and pressed Ctrl+A**, and a canonical ledger was minted to receive it. This was predicted
in `HANDOFF-WAVE-A-RECOVERY.md` §6 item 1 and had never been registered or fixed; it is now **D79**.

⛔ **IT IS WORSE THAN AN ORPHAN MASTER.** An orphan is visible in the chart of accounts. This restates the
**Profit & Loss** — the voucher still balances, the tax is unchanged, and no trigger, total or server check
disagrees. The only visible symptom is that a company's expense analysis changes because somebody LOOKED
at a voucher.

**The fix** is §6's own prescription: `baseLegFromView` reads the head off the stored postings (base side,
minus the party leg by uuid, minus the three tax legs by name) and the builder prefers it; the canonical
base survives as the **fresh-create fallback only**. Re-driven on a fresh tenant: **5/5**, ledgers
**26 → 26**, version 2 keeps `Raw Material Purchases`, and **no `Purchases` ledger exists**.

⚠️ **A CONTROL SHIPPED WITH IT**: a voucher booked to the canonical head still alters cleanly. Without it,
"preserve whatever was hydrated" would be satisfied by a builder that had stopped resolving a base ledger
at all.

### 📋 THE WAVE'S SIX FINDINGS ARE NOW REGISTERED AS **D79–D84**

`HANDOFF-WAVE-A-RECOVERY.md` §6 said *"ADD THESE TO §7.1"* and nobody had. They are rows now, so they can
be worked and counted: **D79** (fixed above), **D80** `Alt+G > Vouchers` pushes the Unwired screen,
**D81** a SERVER-side refusal still leaves masters behind (the form-side half is closed and driven),
**D82** the HSN refusal on the alter path (a judgement, not a bug), **D83** concurrent duplicate manual
bill numbers, **D84** `api/mock.ts` is looser than cored and is a source of false green.

### ✅ L8 — ORDERS + BOM. D6, D7, D44, D68, D69, D70, D71. CLOSED 2026-08-20.

**Mutation proofs — six, and TWO OF THEM EXPOSED AN ORACLE GAP OR A BAD MUTATION:**

| Mutation | Anchor put back | Result |
|---|---|---|
| M18 (D7) | picker sorts `orderNo.localeCompare` | ⛔ **STILL GREEN.** See below |
| M18b (D7) | the same, against the NEW use-site test | RED, by name |
| M19 (D44) | `Overdue()` returns false when the caller sent no `as_on` | `TestTheDefaultOrderBookStateStillFlagsAnOverdueLine` + `TestTheDefaultOrderBookReportsItsLateLines` RED |
| M20 (D71) | *(a comment-only edit — a NO-OP mutation, my error)* | ⛔ green, and it proved nothing |
| M20b (D71) | `PURCHASE_COPY.fulfilledHeading = "Delivered"` | RED, by name |
| M21b (D69) | `if dueBeforeOrder(...)` → `if false` | `TestOrderInputValidateRefusesTheOrdersThatCannotWork/a_delivery_date_BEFORE_the_order_date` RED |
| M22 (D68) | the finished good back on the consuming side of "yields" | `TestTheExplosionNoteNamesTheFinishedGoodOnTheYieldingSide` RED |
| M23 (D6) | `Direction.PartyGroup()` returns `""` for both directions | `TestAnOrderMayOnlyBeCommittedToACounterpartyOfItsDirection` + 2 more RED |

⛔⛔ **M18 IS D38's LESSON AGAIN, IN A DIFFERENT FILE.** `byCommitmentOrder` is exported *specifically* so
the ordering can be pinned without a rendered picker, and it is pinned — nine cases, including the `"100" <
"9"` hazard. **But nothing pinned the PICKER USING IT.** Replacing `list.sort(byCommitmentOrder)` with the
exact defect (`orderNo.localeCompare`) left **every test in the repository green**; only the browser drive
saw it. A test now renders the real picker over orders whose lexical and chronological orders disagree, and
M18b turns it red. **When a fix is "call the right comparator", the comparator's test is not the fix's
test.**

⚠️ **AND M20 IS A WARNING ABOUT MUTATION QUALITY**: the first attempt only added a comment, so it could
not fail. **A mutation that cannot change behaviour is not a proof** — it is a green tick with nothing
behind it, which is the same failure as a weak assertion.

**The drives — `drive-l8-orders.mjs` 12/12, `drive-l8-fulfil.mjs` 4/4, `drive-l8-preclose.mjs` 5/5:**

- **D6** ✅ Alt+C's **Customer** field is a `<select>` labelled *"Customer · under Sundry Debtors"* offering
  exactly `Acme Traders, Mumbai Metals` — no bank, tax, capital or nominal head — and the screen states
  WHY. ⛔ **And the escape hatch was driven, because that is where D6 could have come back with a checkbox
  in front of it:** ticking *"Offer every ledger"* widens the list **2 → 26**, and committing a sales order
  to **HDFC Bank** is then refused on the wire — **403 (step-up) → 422**, nothing created. The screen's own
  promise ("the server still refuses anything outside Sundry Debtors and says so") holds.
- **D7** ✅ Driven over a fixture built to tell the two sorts apart: `ZZ-OLDEST-001` (2026-04-01) sorts
  **last** alphabetically and is offered **first**, ahead of two same-day orders.
- **D44** ✅ The order book's **default** (no `as_on`) state flags **2 of 2** dated lines overdue, and an
  explicit `as_on=2027-03-31` agrees rather than contradicting it. ⚠️ The route is
  `/v1/companies/{co}/sales-order-book` — **NOT** under `/reports`, which answers 403 and reads exactly
  like a permission problem (D40's shape).
- **D68** ✅ Driven by creating a real BOM over the API (10 of MS Angle yields 3 of SS Sheet): the note
  reads *"it **yields 3 of SS Sheet 2mm**"*, with the consuming side left to the component table.
  ⚠️ **`seed:demo` CREATES NO BOM**, so Alt+X cannot be exercised on the demo books at all — the same
  class of seed gap as the party-less delivery notes, and worth closing when T3's seed is next touched.
- **D69** ✅ On the wire: a due date of `2026-05-01` on an order dated `2026-06-15` is refused **422** —
  *"a commitment cannot be due before it was made … It is nearly always a mis-keyed year"* — and the
  **CONTROL** (due `2026-07-01`) is **201**.
- **D70** ✅ Alt+W pre-closes with a reason (403 → step-up → **200**), the order leaves the outstanding
  list (3 → 2), and the panel below no longer asserts an unmarked "Outstanding".
- **D71** ✅ The Purchase Order Book says **Received**; "Delivered" appears nowhere on it.

### ✅ L1r — BILL-WISE. D10, D11, D32. CLOSED 2026-08-20.

**Mutation proofs:** M24 replaces `stampBillRefNames(...)` with the one line that was two defects
(`if RefName == "" { RefName = in.VoucherNo }`, no uniqueness, no ref-type sense) → `internal/ledger` RED.
M25 drops the picker's `bills` (the D32 shape — open bills fetched only in "settle" mode, which a credit
note is not) → `BillAllocationPicker` RED by name.

**The drive — `drive-l1r-bills.mjs`, 15/15, plus a wire pass:**

- **D10** ✅ Two **NEW** references keyed on ONE invoice in the browser became **two bills with distinct
  names** — `Sales/2026-27/4-1` = 5000.0000 and `Sales/2026-27/4-2` = 6800.0000 in `bill_allocation`, and
  **both appear separately in Outstandings**. They used to collapse into one row, because the stamp put the
  same bare voucher number on both and the report groups by `(ledger_id, ref_name)`.
- **D11** ✅ The form offers **four distinct kinds** (`New Ref | Agst Ref | Advance | On Account`), and on
  the wire an `ADVANCE` receipt stores `ref_type=ADVANCE` with a **type-and-year-qualified** name
  (`Receipt/2026-27/2` — not a bare integer that collides across voucher types), while `ON_ACCOUNT` stores
  as itself. ⛔ **And Outstandings HONOURS the distinction**: both show as **`pending: -2500.0000`** — a
  credit the party holds, not a bill to collect.
- **D32** ✅ The Agst Ref row is an `<input list="billrefs-openbills">` whose datalist offers this party's
  real open bills (`INV-001 24000.0000 100d overdue`, `INV-003`, `OB/ACME/24-25/07`, …, plus the two this
  session raised). Naming one clears the refusal *"A settlement of 1180.0000 does not say which bill it
  settles"*, the Credit Note **saves 201**, and ⛔ **the note keyed in the browser then PRINTS** — 4,804
  bytes of PDF reading *"Against Invoice No.: 4 dated 20-Aug-2026"*. That was D32's second half: it could
  never print, because a note that names no invoice is refused by `validateVoucherDoc`.
- ✅ **AND THE CHAIN IS CONSISTENT END TO END**: after the credit note, `Sales/2026-27/4-1` reports
  `pending: 3820.0000` (5000.00 less the 1180.00 note). The invoice, the note and the report agree.

⚠️ **THREE PROBE LESSONS, all of which cost a run:**
1. **The accounting-mode Sales form has NO particulars ledger** (`single-ledger:0` never appears — the
   base leg is implicit in the orientation), and its bill picker is **`billrefs-*`**, not `bill:0-*`.
2. **The AGAINST row is an `<input>` + `<datalist>`**, not a `<select>` and not a `role="option"` listbox.
   Two runs read an empty option list and reported D32 as still live. **Read the `list` attribute.**
3. ⛔ **The Outstandings report is at `/outstandings` (PLURAL).** `/outstanding` answers **403 "unmapped
   route: access denied"** — D40's shape, and **the third route this session** where a 404-shaped
   condition arrives as a permission refusal. `/reports/sales-order-book` and `/reports/outstanding` did
   the same. **This is the concrete argument for fixing D40.**

### ✅ L11 — AI-NATIVE. D16, D18, D19, D23. CLOSED 2026-08-20. **D72 STAYS OPEN.**

⛔ **The probe that reported 13/13 in the session that wrote it scored 9/11 here on a fresh seed, and
all three differences were the PROBE.** That is gotcha #2 — "suspect the probe first" — for the
eighth time in this audit, and it is the reason this entry leads with the probe rather than the
product. See `f1e252d`, which commits the probe BEFORE its proofs ran (f48e353's lesson).

1. **It hardcoded two ledger UUIDs.** That pinned it to the one tenant it was authored against.
   After a reseed those ids belong to a **different company**, so insightd matched no supplier,
   `supplier_state` stayed `None`, and `tax.py` fell back to IGST with no input-tax legs — **the
   probe reproduced D18 AND D19 itself and blamed the product.** It now reads `/ledgers` and
   **asserts Steel Suppliers really is in state 29** rather than trusting the comment that says so.
2. **It selected its draft by `hasText: "Steel Suppliers"` + `.first()`.** The row label is
   `party · total · status · pct` and `BillInboxScreen` appends `· #id` **only on a collision**, so
   the second run silently measured the FIRST run's stale draft — same `draft_id` in the wire log
   two runs apart was the tell. It now diffs insightd's own draft list and clicks **by index**
   (the render order is the API order; `BillInboxScreen` does not sort).
3. ⛔ **It read `/reports/day-book`, which does not exist.** That answers **403**
   `{"error":"unmapped route: access denied"}`, and the substring assertion then reported *"no Input
   CGST in the books"* — **a permission error wearing a missing-tax-leg costume**, which is gotcha
   #5's fourth appearance. The route is **`/day-book`**. The status is now asserted before the body
   is believed, and the legs are matched **by side and by ledger name** (RULE 4), not by substring.

**Mutation proofs — all four RED, and what failed is what was predicted:**
- **M23 (D16)** keeps the redaction *report* but hands the **unredacted** `ocr` downstream
  (`insightd/insightd/pipeline.py:65`) — i.e. the panel still claims redaction while the values go
  through verbatim, which is the defect exactly → `test_pii_boundary.py` RED by name on
  `test_no_raw_pii_reaches_the_faked_cross_border_call`, `test_masked_placeholders_are_present` and
  `test_redaction_report_recall_matches_planted`.
- **M24a (D18/D19)** the **fixture door** sends `{ company_id }` alone, as shipped →
  `BillInboxScreen.test.tsx` RED on four named cases, including *"carries each party's STATE CODE —
  the field that decides CGST+SGST vs IGST"*.
- **M24b (D18/D19)** ⛔ **the OTHER door, asked separately.** The fix touched **two call sites** in
  one file, and the upload path is the one with **no fixture to fall back on** — a photographed bill
  carries no tenant block at all. Mutated alone → RED on *"sends the same context on the UPLOAD
  path, which has no fixture to fall back on"*. **This is D7's lesson applied before it could bite:
  when a fix touches two sites, prove EACH.**
- **M25 (D23)** removes the `estimate` / `method` / `basis` keys from `cashFlowJSON` →
  `TestCashFlowResponseSaysItIsAnEstimate` and `TestCashFlowResponseNamesTheINVENTEDSettlementDates`
  RED. ⚠️ The first attempt replaced them with `_ = basisJSON(p.Basis),` inside a map literal, which
  is a **syntax error** — the package did not build, and a tree that does not compile proves nothing
  about coverage (gotcha #9). The runner now separates "did not build" from "RED".

**The drive — `drive-l11-ai.mjs`, 14/14, and re-runnable:**

- **D16** ✅ A fixture whose invoice number is a **PAN** and whose line description carries an
  **Aadhaar** was created through the Bill Inbox and accepted. The panel prefills
  `voucher_no = "[PAN REDACTED]"`, states the redaction, and **neither the PAN nor the Aadhaar
  appears anywhere in the day book** — swept across every field of every voucher in the window, not
  just my own voucher's legs.
- **D19** ✅ An **intra-state** bill (Steel Suppliers, state **29** — read from `/ledgers` and
  asserted, the company's own) derives **CGST 90.0000 / SGST 90.0000 / IGST 0.0000** on 1000.0000
  @ 18%. The seeded draft cannot test this: its supplier is in 27, where IGST is *correct*.
- **D18** ✅ The accept chain runs **claim 200 → POST purchase 403 (step-up) → 201 → accepted 200**,
  and ⛔ **the books read back by side and name**:
  `C:Steel Suppliers:1180.0000 | D:Raw Material Purchases:1000.0000 | D:Input CGST:90.0000 |
  D:Input SGST:90.0000`, **with no IGST leg anywhere**. Those are the input-tax legs the unbalanced
  draft lacked — and cored's deferred `debit = credit` trigger is why this defect showed up as a
  *refused* accept rather than a bad voucher.
- **D23** ✅ `GET /v1/companies/{co}/cash-flow` answers 200 with
  `as_on, basis, beyond_horizon, bucket_days, buckets, estimate, horizon_days, method, opening_cash,
  overdue_folded_into_first_bucket` — it now discloses `estimate`, `method` and `basis`.
- ⛔ **D72 IS NOT DRIVEN AND STAYS OPEN.** insightd's optional statistical forecast is still
  unreachable from the client and still carries money as JSON floats. **Nothing was keyed for it** —
  it is not part of this closure.

### ⛔ NEW DEFECT FOUND BY THIS DRIVE, REGISTERED AS **D85** (not fixed)

Making the probe re-runnable is what found it. Varying the PAN **did not work**: the run with a
freshly randomised PAN was still refused with *"This supplier bill is already on the books: purchase
**[PAN REDACTED]** dated …"*. The reason is that `assertNotAlreadyOnTheBooks` keys on
`body.voucher_no` — the **post-redaction** value — so **every PII-shaped invoice number collapses to
the same constant token**. Two genuinely different bills from one supplier on one date are then "the
same bill", and the refusal's own advice ("key its own supplier invoice number") **cannot be
followed**, because any number that redacts lands on the same token.

It is registered rather than fixed because the fix is a judgement about what the dedup key should be
(the draft id? the pre-redaction hash? the redacted number plus a line-total?) and that check's
comment block is explicit that **tightening or loosening it trades a missed duplicate for a refused
first accept**. ⚠️ It is also a *class*: any value the redactor rewrites to a fixed token is unsafe
as an identity key, wherever else that pattern appears.

**The probe is now keyed on the invoice DATE** — it picks an August day no Steel Suppliers purchase
holds yet — and passed **14/14 twice consecutively on an already-dirty tenant**.

### ⚠️ TRAPS THIS LANE ADDS TO THE LIST

1. ⛔⛔ **A PROBE THAT HARDCODES A TENANT'S UUIDs IS A TIME BOMB, AND IT DETONATES AS A PRODUCT
   DEFECT.** Every drive here is told to reseed between lanes (gotcha #3), which *guarantees* those
   ids go stale — and the failure is not "ledger not found", it is a plausible, on-message
   *IGST-instead-of-CGST* result that matches the defect being tested. **Resolve masters by NAME at
   run time, and assert the property the test depends on** (here: the supplier's state code).
2. **`.first()` on a text locator is residue-blind.** Any probe that creates a row and then finds it
   by party name will re-measure its own earlier run the moment it runs twice. Diff a server-side
   list and select by index.
3. **A re-runnable probe is worth building.** Two of this lane's four "product failures" only
   appeared because the probe could not run twice, and the third only appeared because it could not
   run on a different tenant. **The probe that survives a reseed and a re-run is the one whose green
   means something.**

### ✅ D40 — WHAT AN UNROUTED PATH ANSWERS. FIXED 2026-08-20, **ON THE USER'S EXPLICIT AUTHORIZATION.**

This one had been deferred three times as "not a lane's call" because it changes
security-visible behaviour. The user was asked directly this session and said fix it.

**What it was.** The PDP refused everything `routeAction` could not map with a flat 403
`unmapped route: access denied`, so **"this route was never built" was indistinguishable from
"your level is not permitted"**. The bill was paid four times: `/reports/sales-order-book`,
`/reports/outstanding`, `/outstanding` and (this session, in the L11 probe)
`/reports/day-book` are all *misspellings of real routes*, every one answered 403, and every
one was first diagnosed as a permission defect. In L11 it was worse than lost time — the 403
body was fed to a substring assertion that then reported *"no Input CGST in the books"*, so a
routing typo presented as a **missing tax leg**.

**The fix.** `denyUnmappedRoute` asks the **router** — `chi.Mux.Match` on the same radix tree
`ServeHTTP` walks, given a fresh route context so it cannot disturb the live one — and splits
three cases that used to share an answer:

| condition | answer |
|---|---|
| the router serves nothing at this path | **404** `no such route` |
| the router serves the path, not this method | **405** + `Allow` |
| the router serves it and the PDP cannot map it | **403**, ⛔ **UNCHANGED — fail CLOSED** |

⛔⛔ **THE THIRD ROW IS THE WHOLE POINT AND IT DID NOT MOVE.** A mounted route no rule governs
must never be served. `TestEveryCompanyRouteIsMappedAndInVocabulary` walks the router and fails
on any mounted company route the PDP cannot map, so in a healthy tree that arm is
**unreachable** — which is precisely why it now has a test that reaches it *deliberately*
rather than being reasoned away as impossible. A nil router also fails closed: **not knowing
whether a route exists must never become "it does not exist."**

⛔ **THE ROUTER IS THE ORACLE, NOT A SECOND TABLE.** A hand-kept list of "routes that exist"
would drift the first time somebody mounted something, and the drift would surface as this
function lying about a real route.

**Mutation proofs — three, and one of them is the DANGEROUS direction:**
- **M26** puts the flat 403 back → `TestANeverBuiltRouteAnswers404AndNot403` and
  `TestAWrongMethodOnARealRouteAnswers405WithAllow` RED.
- **M27** ⛔ **opens the fail-closed arm** (mounted-but-unmapped → 404) →
  `TestAMountedButUnmappedRouteStillFailsClosed` RED. **This is the proof that matters.** A fix
  in this area can fail in two directions and only one of them looks like a bug; the direction
  that *widens access* is the one that must have its own oracle.
- **M28** makes a nil router report "askable" → `TestRouteMethodsFailsClosedWithNoRouter` RED.

**Driven on the wire** against a cored **verified fresh by symbol** (`no such route` and
`method not allowed on this route` both present in the running binary):

```
CONTROL — real routes still serve:   /day-book 200 · /outstandings 200 ·
                                     /sales-order-book 200 · /trial-balance 200
the four misspellings + nonsense:    404 {"error":"no such route"}  (all five)
PATCH /tds-challans:                 405  Allow: GET, POST
UNAUTHENTICATED /no-such-thing:      401
NON-MEMBER      /no-such-thing:      403 {"error":"not authorized for this company"}
```

The last two are the disclosure controls and they are the reason this is safe: **the shape
answer lives behind authentication AND behind the membership gate.** A stranger gets 401 and a
foreign tenant gets "not authorized" — neither can map this company's API surface. What an
authenticated *member* learns is the static shape of the API, which is identical for every
tenant and carries no tenant data. The `Allow: GET, POST` line is also, exactly, the recon
proof D40's row said the flat 403 had killed.

⚠️ **ONE TEST WAS PINNING D40 ALONGSIDE ITS OWN PROPERTY.**
`TestAPathNobodyMappedIsRefusedForASyncCredentialExactlyAsForAPerson` asserted a literal 403 on
three paths the router mounts nothing at — so it *demanded* the confusion D40 removed. It now
asserts what it was always about: **PARITY** between the service credential and the person,
plus **not served**. That is strictly stronger — the bug it exists for was an *asymmetry* (svc
served where a person was refused), and parity catches that while a literal status code merely
correlated with it. ⛔ **M26 leaves the rewritten test GREEN, which is the evidence that it no
longer pins the defect.** That makes **five** tests found pinning the defects they covered.

**Gates:** `internal/server` **325 passed / 0 FAILED / 0 SKIP**; full backend **35 packages ok**,
bracketed by a live `psql` before AND after (the `0 SKIP` is the load-bearing number — a down
database makes every DB test skip and `go test` still exits 0). Frontend **1286/1286**,
`verify:demo` **34/34** on a fresh seed.

### ⛔ SECOND NEW DEFECT FOUND THIS SESSION, REGISTERED AS **D86** (not fixed)

A `verify:demo` run came back **33/34** — *"BS Sundry Creditors == Outstandings (payable): BS
109950.0000 vs Outstandings 108770.0000"* — immediately after the D40 work, which is exactly
when a regression would look most convincing. **It was not D40** (a 404-vs-403 on routes that do
not exist cannot move a balance), and the decisive check was a **fresh seed: 34/34**.

The residue was the tell. The gap was **exactly 1180.0000** — one L11 bill — and the cause is
real: **Outstandings with no `as_on` bounds at today; the Balance Sheet with no `as_on` does
not.** My probe had picked a free August date that happened to fall *after* today (2026-08-27
vs 2026-08-20), cored accepted the future-dated purchase **201**, and the two reports parted
company by that bill. `?as_on=2026-09-30` returned **109950.0000** and agreed exactly.

`verify:demo` calls **neither** report with an `as_on` while its own comment says the two
independent paths *"must agree exactly"* — so that check passes today only because the seeded
data happens to carry no future dates. Registered as **D86**, not fixed: which default is wrong
is a judgement, and changing the Balance Sheet's bound moves the Balance Sheet, which the audit
already requires a deliberate `verify:demo` re-pin for (see D8/T9).

⚠️ **THE PROBE WAS FIXED, THOUGH — a probe must not leave the tenant failing the repo's own
gate.** `drive-l11-ai.mjs` now clamps its date to today or earlier; it passes 14/14 and
`verify:demo` stays **34/34 after the probe has run**.

⚠️ **AND THE TRAP WORTH KEEPING:** *a gate that fails right after your change is not evidence
your change broke it.* Two of this session's three false alarms were residue from earlier runs
on the same tenant. **Reseed before believing a red gate** — it costs one minute and it has now
overturned the obvious diagnosis twice.

### ✅ L9 — USERS + SECURITY. D25, D55, D56, D57, D58, D73, D74. CLOSED 2026-08-20. **25/25.**

⛔⛔ **THE FIRST RUN OF THIS PROBE AIMED THE DEMO OWNER'S OWN LOCKOUT BUDGET AT ITSELF, AND
THAT IS THE LESSON OF THE LANE.** `openApp` had no way to say who to sign in as, so a probe
written to drive a throwaway user silently drove `browser-dev-owner`. Bad twice over: every
assertion went **vacuous** (the owner is already a member of every company, so *"an invited user
lands in the invited company"* passed while testing nothing), and the change-password form sends
a `current_password` — a wrong one is a **password guess** — so the probe was hammering the
account every other probe in the directory signs in as. It survived only because a later
successful sign-in happened to reset the counter. `openApp` now takes `subject`/`password`, and
its sign-in failure quotes what the login screen actually said.

**Two throwaway subjects per run, kept apart deliberately:** `U_PW` never sees a wrong password;
`U_LOCK` is the one that gets locked. Mixing them drops a lock into the middle of the
password-change drive and the 423 reads as a broken form.

**`allowNoCompany`, and only D25 may use it.** D25's fix adopts the invited company ONLY when
nothing is persisted and nothing is in the URL, so a probe passing `?company=` **disables the
code it is testing** and reports the defect fixed whatever the app does. The trap that refusal
existed for (an empty `?company=` mints a clean empty "My Company") is closed by making
`expectCompany` **required** with the option.

**Mutation proofs — eight, all RED with the predicted case named:**
- **M29 (D25)** the bootstrap adopts nothing while still claiming → *"signing in CLAIMS a pending
  invite and opens that company instead of minting one"* RED.
- **M30 (D55)** `changePassword` sends nothing while the form still reports success → 4 RED.
- **M31 (D55)** the confirmation is no longer compared → *"a MISTYPED confirmation never reaches
  the server"* RED.
- **M32 (D56)** ⛔ **the dangerous direction** — revocation removed from the authenticate funnel
  → `TestLockingAnAccountRevokesTheSessionsItAlreadyHolds` and
  `TestLockingRevokesEveryTokenDoorNotOnlyTheCompanySubtree` RED.
- **M33 (D57)** the policy back to compile-time constants → `TestLockoutPolicyIsConfigurableFromTheEnvironment` RED.
- **M34 (D58)** the raw UTC instant restored → 2 RED, including the already-lapsed case.
- **M35 (D73)** the knock unaudited → `TestAttemptsDuringALockAreAuditedAndDoNotExtendIt` RED.
- **M36 (D74)** the running counter removed → `TestFailedAttemptsIsARunningCounter` RED.

**The drive — `drive-l9-users.mjs`, 25/25:**

- **D25** ✅ An **invited** user signing in with **no company in the URL** lands in **Demo
  Traders**, not a minted "My Company" — and `company_membership` gains exactly **1** row for
  that subject, so the invite really converted.
- **D55** ✅ *(own password)* The chrome offers the form, the browser reaches
  **`POST /auth/password 204`**, and the session **survives**. ⛔ The oracle is not the 204: the
  **old password is then refused 401 and the new one signs in 200**. A screen reporting success
  from a status code looks identical whether it changed anything or not.
- **D55** ✅ *(administrative unlock)* Users & Roles **shows** `locked_until`,
  `PUT /members/{userID} {"unlock":true}` answers **200 `{"lock":"cleared"}`**, the account
  **signs in again 200**, and the unlock itself is **audited** (`outcome=unlocked`).
- **D56** ✅ With a **CONTROL first** — the bearer answers 200 *before* the lock, so the refusal
  after it cannot be a token that was never valid. After the lock the **same** bearer is refused
  **401 `session revoked: this account was locked`** on a **read** and on a **write** (the write
  used to answer 201). ⛔ And it stays dead **after the unlock** — otherwise a lock is a pause
  button and a stolen bearer simply waits.
- **D57** ✅ Driven by **restarting cored with `AUTH_LOCKOUT_MAX_FAILURES=3`**: the lock trips on
  the **3rd** failure where the default deployment took **5**. A configuration knob cannot be
  driven without changing the configuration, and reading the constant out of the source is not
  driving it. ⛔ The probe **puts the deployment back** and asserts it did — a three-strike
  lockout left running would make the next lane's first typo lock an account.
- **D58** ✅ The login screen says *"ACCOUNT LOCKED — too many failed attempts. Try again after
  **Aug 20, 2026, 5:22 PM PDT** — that is about **15 minutes** from now."* No RFC3339 instant
  anywhere, a named zone, and the duration — the only question actually being asked. It refuses
  even the **correct** password, which is the lock doing its job.
- **D73** ✅ The attempt refused **during** the lock window is audited with its own
  `outcome=locked`, and there are **exactly 5** `outcome=failure` rows — the five real password
  guesses. A lock must not renew itself off its own refusals.
- **D74** ✅ `failed_attempts` read **between** attempts: **fa=1, 2, 3, 4** then the lock at 5.
  It used to sit at 0 until the instant the lock tripped, so nothing could observe an account
  partway to being locked.

⚠️ **THREE PROBE LESSONS, every one of which read correct behaviour as a defect:**
1. ⛔ **AN INVITE IS NOT A MEMBERSHIP.** `GET /v1/me` is the only seam that converts one — **that
   IS D25** — so a throwaway subject that never calls it is refused **403 "not a member"** by
   every company route. The probe read those 403s as a broken D56 control and a missing member
   row. `U_LOCK` now claims in setup; `U_PW` deliberately does not, because D25's assertion is
   that *signing in* claims it.
2. **THE LOCK TRIPS *ON* THE Nth FAILURE, AND THAT SAME RESPONSE IS THE 423** — not "N failures,
   then an N+1th attempt refused". The probe predicted 6 (and 4 for the configured-3 case) and
   called the correct behaviour broken. Twice, in one file.
3. **openApp asserts it got PAST the login screen**, so the D58 leg — whose whole point is to be
   **refused at it** — is the one place raw Playwright is right. It says so in the file.

**Gates:** frontend **1293/1293** (up 7 for D55's build), `tsc` clean, backend **35 packages ok /
0 FAIL** bracketed by a live `psql`. Verified after the run: cored fresh **by symbol**, **no
`AUTH_LOCKOUT_*` in its environment**, and `browser-dev-owner` at **failed_attempts 0, signing
in 200**.

### ✅ L5 — NL QUERY. D20, D21, D50, D51. CLOSED 2026-08-21. **41/41.**

**Probe:** `frontend/scripts/drive-l5-nlq.mjs` (41 assertions: 8 on the wire, 33 in a browser).
**Proof runners:** `/tmp/proofs-l5.py` (9 unit-level mutations) and `/tmp/proofs-l5-e2e.py`
(2 mutations that **rebuild and restart cored and re-run the browser probe**).
Binary verified fresh by symbol before every run: `asks for another company` = 1,
`RESOLVED PERIOD WINDOWS` = 2.

#### ⛔ THE BLOCKER THE PREVIOUS HANDOFF NAMED WAS REAL, AND IT WAS BIGGER THAN THE EXAMPLE

`server.go`'s `buildTranslator` swaps `FakeTranslator` for `nlq.ClaudeTranslator` the moment
`ANTHROPIC_API_KEY` is set, so **the system prompt IS the production translator's behaviour**.
The compiler downstream rejects ILLEGAL queries; it cannot reject a legal query that measures the
wrong thing. Three of this lane's four rows were still live on that path:

| Row | What the prompt was still doing |
|---|---|
| D51 | its only worked sales/purchase example was `sum(taxable_value)` over voucher type `Sales` — the defect verbatim |
| D21 | the model was given **no clock at all** and one hard-coded quarter, so every relative phrase was a guess. Worse than answering the current period: an arbitrary one |
| D50 | `namesAnotherScope` lives in `FakeTranslator`; `ClaudeTranslator` never runs it, so **the model is the only thing that can refuse** — and nothing told it to |

Fixed in `f7ec380`. The resolved windows now come from `query.NamedWindows`, which resolves each
phrase **by running `FakeTranslator.relativeRange` itself**, so the two translators cannot name
different periods for one question, and the worked example interpolates the **live** "this
quarter" window rather than freezing one. The nlq tests parse the **rendered prompt**, decode each
worked example with the production decoder (`DisallowUnknownFields`) and run it through `Compile`.

#### FOUND BY DRIVING, FIXED IN THE SAME LANE: the refusal that never said why

`handleQuery` routed the translator's typed `ErrCrossTenantScope` through the **generic**
untranslatable arm, so "total sales for every company" was refused with *"could not understand the
question"*. That is the same non-statement D50 records — an operator who asks for every company's
sales and is shown one company's sales has nothing to tell the two apart. Now worded; the test
pins the **contrast** (gibberish must keep the generic message) so a blanket message fails it.

#### ⚠️ D50: THREE ARMS REFUSE, THE FOURTH DISCLOSES — AND THAT IS DELIBERATE

The previous handoff expected **all** cross-tenant arms to be refused. Driven, three are:
`every company`, `across all tenants`, and a bare UUID → **422** naming the reason. The fourth —
naming a real other tenant by its real name — is **answered 200 with this company's figures**,
under the unconditional scope caption. The probe creates a **second real tenant** and asks for it
by name, so this is measured rather than assumed.

It is left that way on purpose, and `namesAnotherScope`'s own header argues it: no tenant data
ever enters the prompt, so the translator cannot tell *"Zenith Exports"* the tenant from *"Zenith
Exports"* the customer — and refusing every proper noun would break `sales to <customer>`, which
is a question about your own books. The durable guard is the caption `QueryScreen` renders on
**every** answer and **every** refusal ("Scope: Demo Traders, and only this company"), asserted
here on both. **A judgement for the user; see the handoff's open questions.**

#### THE ORACLES — never the NL answer's own plausibility

Every money assertion re-fetches the report the answer claims to agree with, and compares in
**integer paisa** (`parseFloat` on a rupee would pass on a rounding a ledger would not):

| Question | Oracle | Measured |
|---|---|---|
| `total sales` | P&L `Sales Accounts`, FY window | **144500.0000 = 144500.0000** |
| … and NOT the old shape | `sum(taxable_value)` over Sales vouchers, via the typed-DSL door | **144500 ≠ 156500** |
| `total purchases` | P&L `Purchase Accounts` | **47500.0000 = 47500.0000** |
| `top debtors` | Outstandings `?kind=RECEIVABLE` | **253920.0000**, and Acme 173680 / Mumbai Metals 80240 by NAME |
| `payables` | Outstandings `?kind=PAYABLE` | **104050.0000**, and Steel Suppliers 80450 / Delhi Parts 23600 by NAME |

The second row is load-bearing: without it D51 could pass on a book where the two figures happen
to coincide.

#### D21 — THE PROBE OWNS THE CALENDAR, AND THAT IS WHAT MAKES IT AN ORACLE

The probe computes each expected window itself from `new Date()`, asserts the screen **discloses**
that window above the table, and **then fetches the P&L for the window the screen named**. A wrong
period fails on the disclosure; a caption describing a window the answer did not use fails on the
figure. All six drove green:

| Phrase | Disclosed window | Screen | P&L |
|---|---|---|---|
| this month | 2026-08-01..2026-08-31 | *No rows.* | 0.0000 |
| last month | 2026-07-01..2026-07-31 | -12000.0000 | -12000.0000 |
| this quarter | 2026-07-01..2026-09-30 | -12000.0000 | -12000.0000 |
| last quarter | 2026-04-01..2026-06-30 | **156500.0000** | 156500.0000 |
| this year | 2026-04-01..2027-03-31 | 144500.0000 | 144500.0000 |
| last year | 2025-04-01..2026-03-31 | *No rows.* | 0.0000 |

156500 for last quarter is the very figure the register says the old code answered "No rows." for.
A **vacuity guard** asserts last-quarter ≠ this-quarter in the books, so the pair cannot both pass
by coincidence. Also driven: `sales at headquarters` → **ALL DATES** (substrings are not words),
`monthly sales` → **ALL DATES** and equal to the all-dates total (an unqualified period word names
no window), and `last 6 months` / `last month and this year` / `receivables last quarter` →
**refused**, not answered for one period.

#### THE MUTATIONS — 9 unit-level, all RED

| # | Mutation | Caught by |
|---|---|---|
| M1 | sales measured on `taxable_value` again | `TestFakeTranslatorSalesThisMonth`, `TestExecute_NetSalesMatchesProfitAndLoss`, `…FollowsASubGroup` |
| M2 | the **prompt's** example teaches `taxable_value` | `TestSystemPromptTeachesThePLMeasure` |
| M3 | `last` stops selecting the previous window | 4 tests incl. `TestFakeTranslatorPreviousPeriods`, `…QualifierBeatsArmOrder` |
| M4 | the **prompt** stops carrying the resolved windows | `TestSystemPromptWindowsMatchTheTranslator` |
| M5 | outstandings drops the opening-bill UNION | `TestExecute_OutstandingsMatchesTheOutstandingsReport`, `…ExcludesTheFutureDatedBill` |
| M6 | the translator stops recognising another scope | `TestFakeTranslatorCrossTenantScopeRefused` |
| M7 | the scope refusal loses its reason | `TestQueryCrossTenantQuestionIsRefusedInWords` |
| M8 | the screen stops stating the period | `QueryScreen.test.tsx` ×2 (D21) |
| M9 | the screen stops saying what was measured | `QueryScreen.test.tsx` (D51) |

#### ⛔ AND TWO MUTATIONS AGAINST THE PROBE ITSELF — REBUILT, RESTARTED, RE-DRIVEN

A probe that goes **41/41 on its first run** is exactly the one gotcha #2 says to distrust, so the
product was reverted, **cored rebuilt and restarted** (a Go edit is invisible to a running binary)
and the browser probe asked whether it noticed. Both reproduced this register's own numbers:

| # | Mutation | What the PROBE saw |
|---|---|---|
| E1 | sales back on `taxable_value` | 14 red. `total sales` **156500 vs P&L 144500**; the not-a-coincidence arm went red too (**156500 = 156500**); the measure caption changed to *"stamped on the voucher by GST derivation"*. Purchases stayed **green** — the mutation is discriminating |
| E2 | outstandings drops the UNION | 8 red. receivables **133920 vs 253920**, payables **36050 vs 104050** — **character-for-character the figures in rows D20** |

#### TRAPS THIS LANE ADDS

1. **A REPORT ROUTE'S JSON SHAPE IS NOT GUESSABLE.** `profit-and-loss` returns
   `{income:{nodes:[…],total},expenses:{…}}` — the sections are OBJECTS with a `nodes` array, not
   arrays. Two oracle attempts died on `'str' object has no attribute 'get'` before the shape was
   read. `outstandings` takes `?kind=RECEIVABLE|PAYABLE` and reports one orientation per call.
2. **`set -- $r` DOES NOT WORD-SPLIT IN ZSH.** A loop over `"from to"` pairs silently passed the
   whole string as `$1` and an empty `$2`, and the reports 400'd. Use a shell function with two
   arguments.
3. **THE PROBE CREATES A SECOND TENANT AND MUST NEVER SIGN INTO IT.** It exists only to be named
   by name. It is never posted to, so `verify:demo` on the drive tenant is unaffected — re-run
   after the drive: **34/34**.

**Gates:** backend **35 packages ok / 0 FAIL** bracketed by a live `psql`; frontend vitest and
`tsc` clean; `verify:demo` **34/34** on the drive tenant both before and after.

### ✅ L13 — MASTERS. D36, D47, and row 20's masters gap. CLOSED 2026-08-21. **33/33.**

**Probe:** `frontend/scripts/drive-l13-masters.mjs`. **Proof runners:** `/tmp/proofs-l13.py` (6
unit-level mutations) and `/tmp/proofs-l13-e2e.py` (3 mutations that re-run the browser probe;
the backend one rebuilds and restarts cored). Binary verified fresh by symbol:
`this company already has a %s named` = 1.

#### ALL THREE ROWS WERE BUILT AND HAD NEVER BEEN DRIVEN — the L5 pattern again

The register still described the shipped code. `MasterCreateScreen` renders the whole details
block; `MASTER_GOTO_ENTRIES` routes all five kinds; `onAccept` already returns its outcome to the
shell. What was missing was the proof, and driving found two things reading the code would not.

#### THE ORACLE IS A SURFACE THE FORM DOES NOT OWN

A master form that reports success from a status line looks identical whether it persisted D36's
four fields or discarded them — that is `allow_negative_stock`'s shape exactly (D5/D43: accepted at
201, thrown away). So each field is read back from Postgres AND from the **Outstandings report**,
which is the product's own reader of `ledger_opening_bill`:

| Keyed on screen | Read back |
|---|---|
| opening 5000.0000, side **Dr**, bill-by-bill on | `ledger` row = `5000.0000 \| D \| true` |
| two references, 3000 + 2000, with due dates | `L13/OB/…/1 D 3000.0000 2026-09-30` and `…/2 D 2000.0000 2026-10-31` |
| — | Outstandings report: the party at **5000.0000**, both refs by name |
| — | the Outstandings **screen** names the party |

The **cursor** is asserted, not the field's existence: D36's traced cause was that `MASTER_FIELDS`'
`"details"` position had no control behind it, so the probe reads `document.activeElement`'s testid
after Enter. A form with the fields and no cursor is the half-built state the row describes.

**Row 20** is driven on its own words — *"a Kg item is impossible"*. `Alt+G > Create Unit` and
`> Create Stock Item` open the frames, and the item is created **in Kg by name**, read back from
`stock_item ⋈ unit` as `Kg`, not the silent "Nos" the old empty picker produced.

**D47** is driven on all three refusal paths: the SERVER's (a duplicate name), the CLIENT's (a
breakup that does not add up, refused before any request), and the INLINE `Alt+C`-from-a-voucher
path — which additionally has to warn that the voucher field now names a master that does not
exist. Each is asserted on the shell's `status-line`, which survives the frame pop, together with
the fact that the frame really did pop (else a screen-local message would have been enough).

#### ⛔ FOUND BY DRIVING, FIXED IN THE LANE: the refusal was a Postgres error

D47's fix delivered the refusal. What it delivered was:

```
Could not create ledger "Acme": masters: insert ledger: ERROR: duplicate key value
violates unique constraint "ledger_company_id_name_key" (SQLSTATE 23505)
```

A refusal an accountant cannot read is barely better than the silence D47 records, and it hands
out the schema's constraint names. All five kinds the form can build now map their name collision
through `nameTaken` to `masters.MasterNameTaken` — a typed error whose `Is` matches the
`ErrMasterNameTaken` sentinel, so `writeMastersError` keeps ONE branch (422) instead of five, and
whose `Error()` is the single sentence *"masters: this company already has a ledger named …"*. The
test asserts the sentence AND asserts `SQLSTATE` / `unique constraint` / `duplicate key` are gone,
so a friendly prefix on the pg error would still fail it. A bare 23505 test would have been wrong:
these tables also carry company-scope unique keys, and a violation of one of those is a bug, not a
name collision — the constraint is named explicitly, exactly as `isUniqueViolationOn`'s own header
requires.

#### ⛔ FOUND BY DRIVING, REGISTERED AS **D87**, NOT FIXED

The form can key an opening balance now and still cannot key a **party's GSTIN, state code or
registration type** — all three of which `CreateLedger` has stored since it was written. Every
party keyed from the keyboard therefore lands with `state_code` NULL, which is the exact input that
made D19 derive an intra-state bill as IGST. `verify:demo`'s own party checks fail on it, which is
how it surfaced. Left open because the fix carries a judgement D36's did not: whether `state_code`
becomes MANDATORY under Sundry Debtors/Creditors (refusing creations that succeed today), and how
much of the party master the form should expose.

#### ⛔ THE PROBE HAD TO LEARN GOTCHA #4 THE HARD WAY

A 5000.0000 opening **Dr** keyed on one ledger is a ONE-SIDED opening. After three runs the Trial
Balance was 15000.0000 out and `verify:demo` read **29/34** — Trial Balance, Balance Sheet, the
month-end balance check, and both party checks (the new debtors have no GSTIN, i.e. D87). None of
that is a product defect: Tally itself carries a *difference in opening balances* until every
opening is keyed. It is residue, and to the next lane it reads as a broken Balance Sheet.

Masters cannot be deleted from the keyboard, so the probe now **tears itself down**: after every
assertion has read the books back, it removes exactly the rows it created, by name, and then
asserts the Trial Balance **reports itself `balanced`** again. That last assertion is what gotcha #4
should always have been — a probe that checks its own residue instead of a handoff note asking the
next session to remember.

#### THE MUTATIONS — 6 unit-level, all RED

| # | Mutation | Caught by |
|---|---|---|
| M1 | the ledger request drops the four opening fields (form unchanged) | `MasterCreateScreen.test.tsx` ×2 |
| M2 | the `"details"` cursor has no control behind it | `…the 'details' cursor position actually LANDS on a control` |
| M3 | an unbalanced opening breakup is accepted | `…a breakup that does not ADD UP is refused, and NOTHING is sent` |
| M4 | Create Stock Item / Create Unit leave the Go To list | `masterGoto.test.ts` ×3 + `MasterCreateScreen.test.tsx` |
| M5 | `onAccept` stops returning its outcome | 5 tests, incl. both D47 status-line tests |
| M6 | the duplicate refusal is a Postgres error again | `TestADuplicateMasterNameIsARefusalTheOperatorCanRead` |

#### AND 3 AGAINST THE PROBE — RE-DRIVEN LIVE

| # | Mutation | What the PROBE saw |
|---|---|---|
| E1 | the request drops the opening fields | 8 red. `ledger` row `0.0000\|D\|false`, **no opening bills**, the party **absent from Outstandings** — while the screen still showed every field and still reported success |
| E2 | the two kinds leave Go To | the probe could not open the unit frame at all |
| E3 | the duplicate refusal reverts to raw pg | the "it is a SENTENCE" arm went red; **"the refusal reaches the status line" stayed GREEN** — the two assertions really do measure different things |

⚠️ E2's mutation makes the probe **crash before its own tally** (it types into a frame that never
opened) and therefore before its teardown, so `/tmp/proofs-l13-e2e.py` scrubs `L13 %` masters after
every case. A proof runner that mutates a probe with side effects needs its own cleanup.

#### TRAPS THIS LANE ADDS

1. ⛔ **`psql -tAc` RENDERS A BOOLEAN AS `true`/`false`, NOT `t`/`f`.** (`t`/`f` is the *aligned*
   table format.) The first run failed on its own expectation while the product was correct —
   gotcha #2, once more.
2. **A frontend mutation needs no rebuild** (vite serves from source) but **does** need a beat for
   HMR before the probe launches; a Go mutation needs a rebuild AND a restart.
3. **A one-sided opening balance unbalances the tenant.** Any probe keying an opening must either
   key its contra or remove what it made.

**Gates:** backend **35 packages ok / 0 FAIL**, frontend vitest and `tsc` clean, `verify:demo`
**34/34** before and after the drive.

### ✅ L4 — TDS/TCS. D1, D2, D3, D4, D88. CLOSED 2026-08-21. **67/67.**

**Probe:** `frontend/scripts/drive-l4-tds.mjs`, **67/67**. **Proof runners:** `/tmp/proofs-l4.py`
+ `/tmp/proofs-l4b.py` (**26** unit-level mutations) and `/tmp/proofs-l4-e2e.py` +
`/tmp/proofs-l4b-e2e.py` (**9** mutations that re-run the browser probe; the Go ones rebuild and
restart cored). **35 mutations, every one predicted then observed.** Binary verified fresh by
symbol: `allocatable_deductions` = 1, `one challan cannot pay both` = 1, `query tds deduction` = 1.

⛔⛔ **THE WHOLE LANE IS CLOSED, AND THREE OF THE FIVE ROWS ONLY CLOSED BECAUSE OF WHAT DRIVING
FOUND.** D1 and D2 really were built-and-never-driven, as the plan predicted. D3's collection half,
D4's client half and two live defects underneath them were NOT — see "what driving found" below.

#### THE ORACLE IS THE FALLING NUMBER, AND IT IS NEVER THE STATUS CODE

D1's whole point is that a challan's unallocated balance must MOVE. A screen that reported
"attached" off the 200 would look identical whether it wrote anything or not, so the probe captures
the panel's BEFORE figure (`tds-allocate-before`) and then reads
`tds-challan-unallocated:{serial}` off the register the screen re-read for itself:
**5000.0000 → 0.0000.** Mutation **E1** is exactly that failure — the re-read is removed, the wire
still shows `POST …/allocations 200`, the notice still quotes the server's own sentence, the two
allocation rows really are in Postgres, and the number on screen never moves. One assertion goes
red: the oracle. That is the proof the oracle is load-bearing.

#### THE PRECONDITION NOTHING ON A VOUCHER SCREEN ASKS FOR

A TDS post is refused outright unless the company has a `company_deductor_status` row for the tax
year **PRECEDING** the payment's, and `seed:demo` writes none. The probe drives it from **F11 > TDS
Deductor Details** rather than setting it up on the wire, because getting out of that refusal is
half of what D3 is. ⚠️ `COMPANY` is a specified person WHATEVER its turnover, so the turnover boxes
are HIDDEN for it — a figure against one is a 400, not a harmless extra — and the probe asserts
their **absence**, then reads `entity_category`/`prior_year_turnover_rupees` back as
`COMPANY|(null)`.

#### THE FIFTEEN MUTATIONS, EACH PREDICTED THEN OBSERVED

| Mutation | What was broken | Result |
|---|---|---|
| **M1** (D1) | the allocate transaction stores nothing, still answers 200 | 5 Go tests RED incl. `TestAllocatingLaterMakesTheUnallocatedBalanceFALL` |
| **M2** (D1/D2) | the running totals stop being carried into the cap | `TestAllocatingLaterCannotOverrunTheDeposit` (+ its wire twin) RED |
| **M3** (D1) | the ticked deductions never reach the wire | 4 `TDSChallansScreen` tests RED |
| **M4** (D1) | an already-paid deduction becomes tickable | 2 RED — the 409 stops being unreachable from the keyboard |
| **M5** (D2) | the cap comes off the ALLOCATE path only | 2 RED — precisely the hole the row describes |
| **M6** (D2) | tolerance becomes per-CHALLAN, not per-ALLOCATION | 3 RED incl. `TestRoundedDepositsAreNotRefusedAsOverAllocation` |
| **M7** (D2) | the cap becomes an EQUALITY (the opposite error) | 5 RED incl. `TestUnderAllocationIsStillLegal` |
| **M8** (D3) | `tds_ledger_id` never leaves the form | 2 RED — the server's cross-check would pass over nothing |
| **M9** (D3) | an off-grid liability line stops being refused | `⛔ a named leg that is NOT on the grid is REFUSED` RED |
| **M10** (D3) | the unposted-leg DISCLOSURE goes silent | 2 RED — the state stays legal, the operator stops being told |
| **M11** (D3) | the block's kind is inferred from `primarySide` again | 5 RED — a tax-**COLLECTED** panel on the Journal |
| **E1** (D1) | the register is not re-read (success off the 200) | the ORACLE alone RED, everything else green |
| **E2** (D1) | the route stores nothing | 14 RED incl. the oracle, the notice (`now pays 1 deduction(s)`) and the books |
| **E3** (D2) | the cap comes off the allocate path | 4 RED; the books read back `3\|26000.0000` |
| **E4** (D3) | the `tds` block never leaves the form | the voucher still POSTS 201; `THE RETURN` and the picker RED |
| **E5** (D3) | no class offers a statutory block (the defect as shipped) | `the Journal offers a TDS block` RED |
| **M12–M18** (D3 TCS) | the class offers no block · the charge is never posted · it enters the taxable value · the leg picker reads the chart · a half-keyed charge is dropped · the bill ref tracks the pre-charge gross | RED, each |
| **M14a / M14b** (D3) | the charge posts on the wrong SIDE · it is booked to the base ACCOUNT | RED (M14 itself was vacuous — see below) |
| **M19 / M20** (D4) | the create path drops `kind` · the allocate-later path drops it | RED **separately** — the fix touched two doors (D7's lesson) |
| **M21** (D4) | the picker labels a collection as a deduction | RED |
| **M22–M24** (D88) | the alter stops re-stating the block · the leg is GUESSED · a DEBIT becomes a candidate | RED, each |
| **M25 / M26** (D88, Go) | the GET stops serving the block · the loader never populates it | `TestReadBackAudit_TDSDeductionSurvives…` RED |
| **E6** (D3) | Sales offers no statutory block | `the sale offers a TCS block` RED |
| **E7** (D3) | the charge is never posted | the sale still posts 201; `THE BOOKS — the buyer is DEBITED 120000` RED |
| **E8** (D4) | the create path drops the kind | `a deposit against a COLLECTION is recorded` RED — 400 on the wire |
| **E9** (D88, Go) | the GET stops serving the block | the ORACLE RED, and the register reads **"deduction no longer on record"** — the row's registered symptom, verbatim |

⚠️ **E4 IS THE ONE TO NOTICE.** The screen is untouched: the panel is still there, still keyed,
still echoing the rate. The voucher still posts 201. Only `tds_deduction` is empty and only a
reader outside the form can tell — which is why D3's assertions read the BOOKS (postings by SIDE
and ledger NAME, then the deduction row's section, amount and deductee) and not the form.

#### D3's COLLECTION HALF — a keyed charge line, and why not the other way

A collection has to be **CHARGED TO THE BUYER**, and the GST invoice form SYNTHESISES its postings
rather than taking a Dr/Cr grid — so until this lane there was no line for the block to name, and
`statutoryTax` was `null` on Sales for exactly that reason. A `tcs` block alone records the
collection for the return and **posts nothing**: the invoice would charge the goods and the GST
while the return says a further 2% was collected, which is money the company must deposit and never
billed.

**The user chose the explicit keyed charge line**; deriving it inside `buildGstVoucherRequest` was
put to them and rejected, because it makes the browser the author of an amount the server owns. So
`GstFormState.charges` is a list the operator keys — a statutory collection, freight, insurance,
packing — and each one enters the PARTY leg and the BILL REFERENCE, posts on the class's tax side,
and is **absent from `taxable_value`** (GST is charged on the supply; folding a charge in would
raise the tax on it). The item invoice carries none and **says why on its face**: its postings are
generated server-side, so there is no line to name.

#### ⛔⛔ WHAT DRIVING FOUND THAT READING COULD NOT — FOUR THINGS, AND THE SUITE WAS GREEN FOR ALL FOUR

**1. THE BILL REFERENCE TRACKED THE PRE-CHARGE TOTAL.** The builder added the charges to the party
leg; `gstVoucherGross` did not — and the bill-reference panel's refusal, its caption and its
auto-tracking default reference were all built on the latter. On any party that maintains
bill-by-bill, keying a charge produced

> `ledger: bill references on this ledger total 118000.0000 but it posts 120000.0000 — the
> difference is 2000.0000`

over a panel that had already certified *"referenced in full (118000.0000)"*. **The 25 tests that
landed with the feature all used a party WITHOUT bill-by-bill**, so the panel never rendered and
nothing computed "what the party owes" twice in the same breath. `gstVoucherPartyTotal` is now the
ONE definition, and the builder ASSERTS the two agree rather than earning a 422 at Ctrl+A.

**2. THE SCREEN DROPPED THE ALLOCATION'S `kind` — D4's client half.** The server side was done
(`AllocationKindTCS`, `ErrMixedChallanKinds`, the LEFT JOIN). Both of `TDSChallansScreen`'s wire
mappings omitted `kind`, and an omitted kind means **TDS**, so a live collection was looked up in
`tds_deduction`, found nothing, and came back as *"nothing was deducted on voucher &lt;uuid&gt; under
206C(1)-SCRAP at leg 0"* — which reads like the BOOKS are wrong rather than like the request is.
The wire type already said *"SEND back what it was served rather than that default"*. **Forty
`TDSChallansScreen` tests were green throughout, because every fixture in `api/mock.ts` was a
DEDUCTION** — so no test ever sent an allocation whose kind mattered, and the mock never served the
field at all. That is **register row D84's shape**, and it was fixed at the source: a collection
fixture, `kind` on every served row, and the two refusals the mock did NOT mirror (the wrong/absent
kind, and `ErrMixedChallanKinds`, which the mock **accepted**).

**3. THE PICKER PRESENTED A COLLECTION AS A DEDUCTION** — one "Deductee" column and an aria-label
reading *"deducted"* over a party the company had in fact CHARGED. They are opposite acts.

**4. A RE-STATED DEDUCTION SAID "(nobody)".** `taxDeductionFromView` is pure and runs before the
chart loads, so it resolves the deductee from the VOUCHER'S OWN POSTINGS — and a deductee very often
is not a leg (a rent withholding debits Rent and credits the bank; the landlord appears nowhere).
The block read *"will carry TDS under 194I-B on (nobody)"* over a real party. A disclosure saying
the wrong thing is worse than a terse one.

⚠️ **AND ONE HARNESS FLAKE, FIXED SO IT IS NOT RE-FOUND.** `openApp` waited 600ms and then read the
chrome ONCE for its tenant assertion. The chrome shows *"Opening your companies…"* until the list
resolves, and that read is O(tenants) — so on a database carrying several seeded drive tenants the
assertion fired against a LOADING SCREEN and reported **"signed in to the WRONG COMPANY"**. It cost
a diagnosis mid-proof-run, where it read as four mutations crashing the probe. It polls now.

#### THE STEP-UP, FOR THE NEXT LANE

The wire log records the shape of the first write of a session:
`PUT /deductor-status/2025` **403**, then the WebAuthn register/assert ceremony, then the same PUT
**200**. A probe must not mistake that 403 for a permission defect.

#### ⚠️ ONE MUTATION WAS VACUOUS, AND IT IS RECORDED RATHER THAN DELETED

M14 swapped the charge posting's `o.taxSide` for `o.baseSide` and was **STILL GREEN** — correctly
so: every row of `GST_ORIENTATION` gives the two the SAME value, so the mutation cannot change
behaviour and proves nothing (gotcha #11). It also caught a **false comment** of this session's own
making, claiming the swap "would credit it to the SALES account" — it would not; only the LEDGER
decides that. The comment is corrected, the field choice is kept (a charge belongs with the tax, and
a fifth orientation whose sides differ must carry it there), and the real claims are proved by
**M14a** (wrong side of the entry) and **M14b** (booked to the base account).

#### WHAT THE PROBE LEAVES BEHIND, AND WHY IT IS NOT L13's MISTAKE

Three real Journals and one real deposit. The Journals are **balanced** (Dr the expense in full, Cr
the bank NET, Cr the TDS liability), so unlike L13's one-sided opening they leave the books level:
the probe asserts the Trial Balance reports itself `balanced` at the bottom, and `verify:demo`
answered **34/34** on the tenant afterwards. Nothing is torn down on purpose — deleting a voucher
while keeping its deduction (or the reverse) would leave the books and the return disagreeing,
which is worse residue than the entries. ⛔ **The challan serial is stamped per run**, because a CIN
already on file is a 409 and a fixed serial would make the SECOND run fail inside D1's arm and read
as a regression.

### ✅ L14 — THE CHEAP HONEST ROWS. D82 CLOSED 2026-08-22, **12/12 — AND THE DRIVE FOUND D89.**

**Probe:** `frontend/scripts/drive-l14-cheap.mjs`. Ran against a cored verified fresh by symbol
(`query tds deduction`, `one challan cannot pay both`, `no such route` = 1 each). ⚠️ D82's own
refusal is CLIENT-SIDE, so the load-bearing freshness check for this lane is **vite's**, not the
binary's — see the mutation note below.

#### D82 IS WORKING AS INTENDED, AND WHAT WAS DRIVEN IS THAT IT READS AS A REASON

The user decided on 2026-08-21 that the refusal is correct: the invoice is unprintable without an
HSN, so keying the code is the fix rather than a hoop. So the drive does not test a behaviour
change — it tests that the refusal **reaches an operator**, **says why**, and has its **remedy on
the same screen**: Rule 46(g) and "cannot be printed" in the status line while the HSN field sits
empty and keyable, nothing sent, and keying the code lets the same alter through as a PUT. The
legacy invoice is seeded OVER THE WIRE because the form refuses to create one (that is D60), which
is exactly how a real one arrives — an import, or a build predating the refusal.

#### ⛔⛔ AND THEN THE REMEDY MINTED A SECOND INVOICE — D89

The drive pressed Ctrl+A **twice**: once to be refused, once after keying the code the refusal
asked for. That is the operator's actual order, and nothing in the product had ever done it.

```
drill a legacy no-HSN sales invoice   ->  h1 "Sales Voucher (Alteration)"
Ctrl+A                                ->  refused CLIENT-SIDE, "(no non-GET traffic)"
                                          h1 "Sales Voucher"          <- thirteen characters
key the HSN the refusal asked for
Ctrl+A                                ->  POST /vouchers/sales 201, status line "Saved"
GET the original voucher              ->  hsn: ""
```

One tax invoice keyed, two on the books, and the remedy the product itself prescribed is what
minted the second. `navMachine`'s `accept` reducer replaced ANY voucher frame with a fresh `create`
frame — it is pure, so it can never learn the write was refused — dropping the loaded `voucher_id`
that routes the PUT while the form kept showing the loaded voucher's 10,000. Registered and fixed
as **D89**; an alter frame now stays put, which is what `delete` directly below it already did.

#### THE ORACLE IS THE DAY BOOK, AND IT IS A ROW **NOT** APPEARING

A status line reading "Saved" is identical in both worlds — that is the whole defect. So the oracle
is read back through a surface neither the form nor the reducer owns: the Day Book over the date
must hold the **same number of entries**, and the invoice must read **`op=ALTER`** rather than a
second `CREATE`. ⛔ `op` is the part a count alone would miss: a twin plus a deleted original also
leaves the count flat. (Verified on the wire first that the Day Book lists one entry per VOUCHER,
latest version — a per-version listing would have made the count assertion wrong.)

#### MUTATION PROOF — PREDICTED, THEN OBSERVED

Removing the reducer's alter arm, with the failure predicted **before** running:

| Gate | Predicted | Observed |
|---|---|---|
| `phase1.test.ts` D89 arm 1 | red, `mode` = "create" | ✅ `expected 'create' to be 'alter'` |
| `phase1.test.ts` D89 arm 2 (Esc) | **green** — the fresh frame replaces the alter in place, so the parent is untouched | ✅ green |
| both CONTROLs | green | ✅ green |
| `gstAlter` D89 | red at the HEADING, step 2 | ✅ `expected 'Sales Voucher' to match /\(Alteration\)/` |
| the drive | 6 red: the four D89 arms **plus** the two D82 arms that depend on alter routing (PUT, and the stored `hsn`) | ✅ **6/12, exactly those six** |

Reverted → 12/12 again. Gates: vitest **1399/1399 in 85 files** (1394 before, +5 new), tsc clean.

#### ⚠️ TRAPS THIS LANE ADDS TO THE LIST

1. ⛔⛔ **A GREEN SUITE THAT NEVER PRESSES THE KEY TWICE.** All three D82 pins press Ctrl+A exactly
   ONCE — one proves the refusal reaches the operator, another proves keying the code lets the alter
   through, and **neither does both in one frame**. The defect lived precisely in the seam. This is
   §4.5's rule ("when a row's tests pass first try, ask what shape they all share") paying out for
   the fifth time. **Ask what a second press does.**
2. ⛔ **`gst-panel` IS NOT A MODE ORACLE — THE HEADING IS.** A create frame has a gst-panel too, so
   the drill's original assertion passed on either. `VoucherScreen` renders
   `{title} Voucher{isAlter ? " (Alteration)" : ""}`, and that suffix is the only thing on screen
   that tells the two apart.
3. ⛔⛔ **AN EVIDENCE STRING SLICED TOO SHORT MADE A REAL DEFECT READ AS A PROBE BUG.** The arm
   printed `.slice(0, 80)` of the body, which cut " (Alteration)" off the end — so the first reading
   of the failure was "the drill never opened an alteration", and half an hour went into the drill
   before the screen. **Print the field you are asserting on, not a prefix of the page.**
4. ⚠️ **4 SECONDS DOES NOT FIT THE STEP-UP CEREMONY.** The first write of a session goes
   `PUT 403 → assert/begin → register/begin → register/finish → assert/begin → assert/finish → PUT 200`.
   At 4000ms the probe cut it off mid-assert and reported the **403** as the outcome — which reads
   exactly like an ACL defect. 9000ms held.
5. ⚠️ **THE DRILL'S WAITS WERE TOO SHORT UNDER LOAD.** At 700/400/2200 it intermittently found
   **zero** Day Book rows, which reads as "the voucher is not on the books". 1200/800/4000 held.
   Load average was 80+ throughout, from OTHER repos — see 6.
6. ⛔ **VERIFY **VITE**, NOT ONLY THE BINARY, FOR A FRONTEND MUTATION.** The symbol check in §0.1
   proves what cored is running; it says nothing about a client-side refusal. Curl the module out of
   vite and count the mutated construct: `curl -s localhost:5173/src/keystroke/navMachine.ts | grep -c
   '<the construct>'`. ⚠️ **AND CALIBRATE THE COUNT AGAINST `git show HEAD:`** — the first attempt
   grepped a string that also matches a PRE-EXISTING arm elsewhere in the same file, expected 0, and
   would have looped forever concluding HMR was stale when it had already updated (4 at HEAD, 3
   mutated — never 0).
7. ⚠️ **`ci-slot` NAMES A STALE HOLDER, AND DEAD PIDS DO NOT MEAN A WEDGED QUEUE.** The
   "queuing behind [pid=…]" line is printed ONCE, so by the time it is read the named processes are
   often gone. The semaphore is an `flock`, which the OS releases on death — it **cannot** wedge on a
   dead holder. Check `~/.claude/machine-health/ci-slots/*.owner` for the current pair before
   concluding anything.
8. ⚠️ **ONE FULL-SUITE FAILURE ON THIS LANE WAS A LOAD FLAKE, AND WAS CHECKED RATHER THAN ASSUMED.**
   `VoucherScreen.draft.test.tsx` timed out on a `waitFor` at load 80+; it dispatches the accept
   handler DIRECTLY and never touches `navMachine`, it passes standalone, and the full suite is green
   on a re-run. **Structural argument first, then the standalone run, then the re-run** — the handoff
   already lists two members of this class (`internal/sync`, `EWayBillScreen`).

#### ⛔ THE REST OF L14

**D77 is CLOSED — see L14b below.** D80 was scoped into this probe and is **not driven**; it remains
at position 4 of `HANDOFF-FINISH-THE-REGISTER.md` §1, unchanged.

### ✅ L14b — D77, THE NON-TOTAL POSTING ORDER. CLOSED 2026-08-22, **8/8 on the wire.**

**Probe:** `frontend/scripts/drive-l14b-d77.mjs`. A WIRE probe, not a browser one: the defect is three
SQL strings, and the surfaces it reaches (the voucher GET, the day book, the printed document) are all
readable on the wire. Binary verified fresh BY SYMBOL in BOTH directions — 3 tie-breaks with the fix,
**0 while mutated** — because a Go edit is invisible to a running binary and this proof depends on
which SQL is actually executing.

#### THE FIXTURE IS THE MOST COMMON DOCUMENT IN THE PRODUCT

The tie needs two legs sharing a side and an amount. **The CGST and SGST legs of every intra-state
supply do**: 18% on 10,000 is 900 credit and 900 credit. Nothing had to be contrived.

#### ⛔⛔ THE ORACLE IS A HEAP REWRITE — AND FOUR CHEAPER MECHANISMS WERE TRIED FIRST AND FAILED

`enable_seqscan` / `enable_indexscan` / `enable_bitmapscan` / `enable_sort` toggles, `work_mem` down to
64kB, and `VACUUM ANALYZE` **all leave the untied order untouched.** The read is an index scan over
`posting_version`, so tied rows come back in physical heap order and the sort preserves it. That matters
methodologically: **the recon's proposed oracle — "GET, VACUUM ANALYZE, GET again" — does not work**, and
a probe built on it would have gone green against a broken build and closed the row falsely.

What does move it is rewriting the heap: `CLUSTER`, i.e. what `pg_repack` does as routine production
maintenance. Proved in raw SQL before a line of Go changed — cluster by `posting_ledger` and the untied
query's output changes while the tied one does not.

⚠️ **THE TWO CLUSTERS ARE ORDERED DELIBERATELY:** `posting_pkey` first, `posting_ledger` second. Ending
on `posting_pkey` would leave the heap in `p.id` order — the very order the fix imposes — so the untied
query would come out accidentally correct.

#### ⛔ THE PRECONDITION ARM, AND WHY A PROBE LIKE THIS NEEDS ONE

Whether a rewrite reorders a *given* tied pair is a coin toss: `posting_pkey` orders the heap by posting
id, `posting_ledger` by ledger id, and for any one pair those agree about half the time. A run where
neither fixture reordered would pass every arm on a broken build. So the probe runs **the old untied
query itself, as raw SQL, in both heap states, and requires them to DIFFER.** It earned its place on the
first run: *"invoice untied: UNCHANGED · journal untied: CHANGED"* — and the mutation run then went red
on exactly the journal's two arms and green on the invoice's.

#### ⛔⛔ THE MUTATION PROOF KILLED THIS ROW'S HEADLINE CLAIM

The probe was built around a "money" arm: `cost_allocations[].posting_seq` indexes the postings array, so
a tied swap should point a cost split at a different ledger — invisibly, because both legs are equal and
every per-category total still foots. **That is wrong.** `GetVoucher` builds `postingSeqByID[pid]` while
walking the same ordered read and joins allocations by **posting_id**, so the seq is re-derived to name
the SAME posting however the array is ordered. With the fix reverted and the array demonstrably reordered
(that arm red), the split arm stayed **GREEN**.

So D77 is a PRESENTATION-order defect and the register now says so. The arm is kept, relabelled as a
CONTROL, so the next reader who derives the same hypothesis finds it already tested. **This is the
clearest example in the whole register of a mutation proof correcting the fixer rather than the code** —
the first draft of the commit message asserted the money claim, and the proof retracted it.

#### ⚠️ AND ONE SITE IS AN HONEST NON-FIX

`document.go` already tie-broke on the ledger NAME, and a leg that ties on side+amount+name also ties on
ledger_id and group (ledger names are unique per company), so the two rows are identical in every
projected column and swapping them cannot change a byte. Its `p.id` makes the comment's *"chosen to be
total (the name breaks every remaining tie)"* claim TRUE — it was FALSE for two legs to the same ledger —
and changes no output. Predicted to stay green under mutation, and it did.

#### ⚠️ TRAPS THIS LANE ADDS TO THE LIST

1. ⛔⛔ **A PROBE THAT READS THE WRONG FIELD NAME PASSES VACUOUSLY AND LOOKS LIKE COVERAGE.** The first
   run of this probe read `p.ledger_name`; the wire field is `p.ledger`. Every leg projected
   `undefined`, so "the array is element-identical" compared SIDE AND AMOUNT ONLY — the two things that
   are equal BY CONSTRUCTION in a tie — and dropped the one thing a swap changes. **7/7, asserting
   nothing.** The probe now THROWS on an `undefined` projection and on an out-of-range `posting_seq`
   rather than comparing them.
2. ⚠️ **`ps -o command= -p $PID` WITH AN EMPTY PID PRINTS A USAGE ERROR AND THE SYMBOL CHECK THEN
   COUNTS ZERO** — which looks exactly like "stale binary". Poll for the port BEFORE resolving the pid.
3. ⚠️ **`make run-cored` COMING UP IN "1s" IS NORMAL, NOT SUSPICIOUS** — the build is cached. Judge the
   binary by its build directory and its symbols, never by how fast the port opened.


### ✅ L14c — D80, THE PICKER'S FIRST ENTRY. CLOSED 2026-08-22, **17/17 in a browser.**

**Probe:** `frontend/scripts/drive-l14-cheap.mjs` (the lane's own probe, now 17 arms: 12 for D82/D89, 5 for D80).

#### THE ENTRY AT THE TOP OF THE LIST WAS THE ONE THAT DID NOT WORK

`GOTO_ENTRIES` is literally `["Vouchers", ...masters, ...REPORT_NAMES]`. `canonicalReportName` returns
undefined for "Vouchers" **on purpose** — a voucher is not a report — and `resolveGotoSelection`'s `??`
then fell back to the RAW LABEL, so the operator landed on `{screen:"report", reportName:"Vouchers"}`
and `UnwiredReportScreen` announced that vouchers are *"not available in this build"*.

It stayed invisible because **the door most people use was always right**: the Gateway's own drill has
pushed `newVoucherFrame(DEFAULT_LAST_VOUCHER)` for the identical label since the beginning. The fix
mirrors that arm exactly, because a Go To and a Gateway drill on one label must land on one frame.

⛔ **THE DESTINATION WAS LOOKED UP, NOT DECIDED.** The driver said to ask the user only if `RESEARCH.md`
did not say. It says: `:903`, *"Payroll voucher: **Alt+G > Create Voucher** > Ctrl+F4 (Payroll)"* — a
create-voucher form. No question needed.

#### ⛔⛔ THE TEST WRITTEN TO MAKE REACHABILITY STRUCTURAL IS WHAT LET THIS SHIP

`masterGoto.test.ts` carries a guard called *"GOTO_ENTRIES offers exactly what the machine can route"*,
added by row 20 precisely so no picker entry could land on "not available in this build". It contained:

```js
const routed = entry === "Vouchers" || masterKindForGotoLabel(entry) !== undefined || REPORT_NAMES.includes(entry);
```

`masterKindForGotoLabel` and `REPORT_NAMES` are the machine's OWN routing tables, so those two clauses
are real. `entry === "Vouchers"` is a **string comparison against a label believed routable** — and it
certified the single dead entry in the product as reachable. The clause is gone: anything outside the
two real tables is now DRIVEN through the machine and must not land on a report frame. That one-line
change is what turns this from a fix into a fix that cannot come back.

#### THE ORACLE IS AN ABSENCE PLUS A PRESENCE PLUS AN EQUIVALENCE

Asserting `h1 === "Sales Voucher"` alone would pass on a build that rendered both the heading and the
refusal. Asserting the refusal's absence alone would pass on a blank screen. So: the sentence is
**absent**, the form's own fields (`Party A/c Name`, `Narration`) are **present**, and — the arm that
actually proves the fix's claim — **the Alt+G door lands on the same screen as the Gateway door**. A fix
that invented a *different* voucher frame (wrong class, wrong mode) satisfies everything else and fails
that one.

#### THE `replaced` DECISION IS PINNED, NOT COMMENTED

Every other arm of `resolveGotoSelection` sets `replaced` under a Switch To, so its absence here would
read as an oversight. It is read in exactly ONE place — `canReturnToVoucher`, over the frames BELOW the
top — so on the depth-2 stack the picker produces it is **unobservable, and an assertion there would be
vacuous** (the recon's skeptic overturned its reader on exactly this). Push a master-create over it with
Alt+C, the real keystroke, and it becomes observable: with `replaced` set, the projection reports "you
cannot Esc back to a voucher" while Esc pops unconditionally and does. The test drives
Ctrl+G > Vouchers > Alt+C and asserts BOTH `canReturnToVoucher === true` and that Esc really returns.

#### MUTATION PROOF — PREDICTED, THEN OBSERVED

| Gate | Predicted | Observed |
|---|---|---|
| the de-vacuumed routing guard | red, naming Vouchers | ✅ *"Vouchers is offered but routed nowhere"* |
| D80 landing test | red, `report` for `voucher` | ✅ exactly that |
| the `replaced` decision | red on `canReturnToVoucher` | ✅ `expected false to be true` |
| `canonicalReportName` control | red | ✅ exactly that |
| Esc arm · report-entry control | **green** — a report frame also pops to the Gateway | ✅ both green |
| the drive | 4 red, Esc green, all 12 D82/D89 arms untouched | ✅ **13/17, exactly those** |

Reverted → 17/17. Gates: vitest **1404/1404 in 85 files** (1399 before, +5), tsc clean.

#### ⚠️ TRAPS THIS LANE ADDS TO THE LIST

1. ⛔ **A "CURSOR IS ON THE DATE FIELD" ARM ASSERTED SOMETHING THE PRODUCT NEVER DOES.** `activeElement`
   is always AppShell's ROOT DIV (`h-full w-full outline-none`) — it pulls focus there on every screen
   change **on purpose**, so no field is ever the DOM's focus and the "cursor" is a rendered highlight.
   Caught by driving the Gateway door and the Alt+G door side by side and finding them byte-equal on it:
   **compare the new door against the door that already worked before believing either is broken.**
   `field: "date"` is a MACHINE-level fact and belongs in the machine's test, where it now lives.
2. ⛔ **L14b's vite-freshness trap fired again, in the same session, and the calibration is the fix.**
   The mutated construct also appears in COMMENTS, so the count to wait for was **2**, not 0 — and 3 at
   HEAD, not 1. Waiting for 0 would have looped forever concluding HMR was stale. **Always
   `git show HEAD:<file> | grep -c` first and derive the target from the diff.**

### ✅ L14d — D84, THE FALSE-GREEN ROOT. CLOSED 2026-08-22. **Runner A 14/14 · runner B 11/11.**

**The instrument** (this is the part later rows depend on):

| | |
|---|---|
| `corpora/mock-vs-cored/masters.json` | the ONE place that says what cored does. Refusal-shaped rows, never value-shaped |
| `frontend/src/api/contract/corpus.ts` | ONE reader, so the two runners cannot drift on what a row MEANS |
| `src/api/contract/mockVsCored.contract.test.ts` | runner A — over `createMockClient()`, in `pnpm vitest` |
| `frontend/scripts/contract-mock-vs-cored.mts` | runner B — over LIVE cored, as `pnpm contract:cored` |

**Why two runners and not one:** A alone lets the corpus drift away from the server it claims to
describe (it becomes a second, prettier mock). B alone never runs often enough to stop the mock
loosening again. A is the gate; B is what detects that cored moved.

#### THE ORACLE RAN AS PRESCRIBED — BOTH RUNNERS BEFORE ANY FIX

Runner A's first run printed **9 rows where the mock ACCEPTED a body cored refuses**, side by side
with what cored answers. Runner B: **11/11, the corpus describes cored.** Then eight tightenings, and
both agree.

⛔ **THE SENTENCES ARE MIRRORED, NOT JUST THE STATUS CODES** — `masters: ledger name is required`, not
"422" — because §2's rule is that a status code is never an oracle. A corpus row with no sentence is
rejected by the gate's own meta-test.

#### ⛔⛔ EVERY EXPECTATION WAS MEASURED ON THE WIRE, AND TWO CONTRADICTED THE RECON

The recon's anchors were used to find the call sites and **not** to fill in the expectations. Two
would have been encoded wrong:

- an unknown group answers **404**, not 422;
- **a duplicate STOCK ITEM name is ACCEPTED** — 201, a new id. The recon said `masters.EnsureStockItem`
  does not exist so the route "only ever reaches `CreateStockItem`, which 422s". The premise is true;
  the conclusion is false. That is now register row **D90**, and it is recorded in the corpus as a
  DECLARED divergence rather than mirrored — because the mock does the opposite (create-or-select) and
  copying cored here would spread the more suspicious of the two behaviours.

**The lesson:** a recon is for finding the code, never for filling in the expected value. Ten minutes
of `curl` overturned two of eleven claims.

#### ⛔ THE TIGHTENING FOUND FIVE TESTS THAT COULD NEVER HAVE RUN AGAINST CORED

`draftAccept.test.ts`'s `realDraftBody` helper called `createLedger` **unconditionally**, so any test
building a second draft body in one company re-created "Acme Traders". The old mock accepted it and
pushed a second ledger row with the same name — so the tests that *passed* were measuring a chart of
accounts with **duplicate party ledgers in it**, which is the exact state the product's own
`ensureLedger` was fixed TWICE to avoid (see `gstVoucher.ts`). The helper now resolves-then-creates,
like the product. **This is the false green the row was written about, and it was in the fixtures.**

#### ⚠️ BLAST RADIUS: MEASURED, NOT FEARED — AND THE ROW'S PREMISE ONLY HALF HELD

The split criterion was "if one tightened arm breaks more than ~10 files, split the row". Measured:

| | files broken |
|---|---|
| `createLedger` empty name alone (the prescribed cheapest check) | **0** |
| all eight tightenings together | **1** (5 tests) |

So the row was **not** split. But the premise — *"tightening turns green tests red, that is the
deliverable"* — only half held: one file's worth of existing false green, and eight refusals whose real
value is preventing the NEXT one. Worth knowing before scoping a row on that assumption again.

#### ⚠️ ONE ROW IS A PINNED GAP, AND PINNED IS NOT SKIPPED

`ledger-unknown-group` stays open: cored resolves the group and 404s; the mock cannot, because
`createGroup` **stores nothing** and the mock models no chart of accounts. Refusing on "this group was
never created here" would refuse essentially every ledger every one of the 61 files makes, because
cored **seeds 28 standard groups** on company create (migration 0003) and tests reasonably rely on
"Sundry Debtors" existing.

The cheap fix — copy that list into the mock — was **deliberately declined**: it would be a silent copy
of the SQL with nothing to detect drift, because the client has no `listGroups` for either runner to
compare against. Closing it properly means adding that read method first.

⛔ **So runner A asserts the gap is STILL OPEN.** Whoever closes it gets a red test telling them to
delete the `mock_gaps` entry. A `.skip` would have made the gate green and the gap invisible — the same
reasoning as D82's pin, and the opposite of what D80's string-equality guard did.

#### MUTATION PROOF — PREDICTED, THEN OBSERVED

Re-loosened **one** check (`createLedger`'s duplicate-name refusal). Predicted: exactly one red test,
naming exactly that row; runner B unchanged.

Observed: **1 failed / 1417 passed**, `ledger-duplicate-name is refused 422 "already has a ledger
named"`, with `DIVERGES ACCEPTED a body cored refuses with 422` printed beneath it. Runner B **11/11**.
Reverted → 1418/1418.

#### ⚠️ TRAPS THIS LANE ADDS TO THE LIST

1. ⛔ **`createRestClient`'s auth option is `headers`, NOT `getAuthHeaders`, AND A WRONG KEY IS SILENTLY
   IGNORED.** `HttpClientOptions` merges a STATIC header map; there is no per-request hook. tsc did not
   catch the wrong key (the options type is a union), and every row came back `401 missing bearer
   token`. ⚠️ The runner reported that correctly — *"the CORPUS does not describe cored"* — which is
   what a 401 on every row actually means, so the shape of the error is worth trusting.
2. ⚠️ **`auth.mts`'s `authHeaders()` MINTS FOR A FRESH RANDOM SUBJECT** (`e2e-owner-<uuid>`), which is
   right for a script that creates its own company and wrong for one handed an EXISTING tenant — a
   stranger is not a member of it. Mint for `browser-dev-owner`, once per run.
3. ⚠️ **READ THE CORPUS WITH `node:fs`, DO NOT `import` IT.** It lives outside `frontend/`, so a static
   import needs `resolveJsonModule` plus a vite `fs.allow` entry, and the test runner and the script
   runner would each need a different one. Both execute in node; reading the file works identically.
4. ⛔ **THE DISPATCH IS EXPLICIT, NOT `(client as any)[call]`.** A typo'd `call` would be `undefined`,
   throw a TypeError, and the row would report as "the engine refused" — a corpus typo that reads as a
   passing refusal is the precise failure mode this whole row exists to stop.

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

### ✅ L15 — D5 + D43, `allow_negative_stock`. CLOSED 2026-08-22. **22/22, wire + browser.**

ONE COMMIT (`766af00`) because they were one defect seen from two ends. Migration 0025 shipped the
column and TWO enforcers that read it — `ledger.checkNegativeStock` inside the post transaction, and
the deferred `check_stock_not_negative()` constraint trigger. Both honoured it correctly. **Nothing
could write it**: no field on the create body, no route, no service setter, no control. It was false
for every tenant that has ever existed.

#### ⛔ THE PREMISE WAS MEASURED, NOT READ — §4.1 APPLIED BEFORE A LINE WAS WRITTEN

```
POST /v1/companies {"allow_negative_stock": true}      -> 201
SELECT allow_negative_stock FROM company WHERE id=...  -> f
PUT  /v1/companies/{id}                                -> 403 (no writer existed)
```

That is D43 reproduced on the wire in under a minute. The recon was right here — but D84 had just
overturned 2 of 11 recon claims, so it was fired at the server rather than trusted.

#### WHAT MOVED

| Layer | Change |
|---|---|
| `internal/identity` | `AllowNegativeStock *bool` on `Company` + the INSERT column (**that alone closes D43**); `SetCompanyFeatures` + `CompanyFeatures`, both cross-checking the RLS scope |
| `internal/server` | the key on `companyReq`; `PUT /v1/companies/{companyID}` beside the GET; the column on the GET |
| `auth_middleware.go` | ⛔ **the SECOND TOUCH** — `PUT ""`/`"/"` → `(authz.Alter, "company")` |
| frontend | types on create + view, `CompanyFeaturesRequest`/`Response`, `setCompanyFeatures` on the client, the rest and mock arms, and the F11 control |

#### ⛔ THE TOKEN CHOICE IS WHY THIS NEEDED NO MIGRATION

`company` was already in authz's vocabulary; migration 0022 seeds Data Entry `allow display company`
and nothing wider, and that level's `default_access` is `'none'`. So `alter company` is
default-DENIED for every seeded non-Owner level **the day the route appears**, while the read keeps
working. Anything reached by 0011's coarse `allow display report` would have handed the inventory
escape hatch to every operator.

#### THE DRIVE — `frontend/scripts/drive-l15-d5.mjs`, 22/22

⛔ **Every setting assertion is `SELECT allow_negative_stock FROM company` over a direct psql
connection.** Never a status code, never a response body: a 201 is exactly what D43 handed callers
while the column stayed false. The accept/refuse pair is the oracle and **no single arm of it
counts** — the accept means something only because the identical body was refused before it and is
refused after it, with only the setting moving:

```
setting OFF -> the issue is REFUSED   "closing balance would be -5.0000"
PUT true    -> column reads t out of Postgres
IDENTICAL body                        -> 201
Stock Summary                         -> closing_qty -5.0000, closing_value 0.0000
PUT false   -> column reads f
IDENTICAL body                        -> REFUSED "closing balance would be -10.0000"
```

**The `-10` in the second refusal is the accept arm's five units being counted back by an enforcer
that has no idea a probe is running.** That is the arm that makes the pair a measurement rather than
a pair of status codes.

§4.4's next keystroke is driven too: the remedy the refusal invites (turn it on, retry) works, and
`voucher_head` holds **exactly 2 rows for 4 attempts** — the refusals wrote no ghosts.

Browser arm: F11 carries a control that **reads its state from the company row**, and both clicks
move the column, read back out of Postgres. It restores the tenant's setting in a `finally`.

#### ⚠️ TWO PROBE BUGS, NEITHER OF THEM CODE BUGS — recorded because §4.6 says to

1. The first run sent `dr_cr: "DR"`. **The REST client normalises that (`rest.ts`'s `wireDrCr`);
   raw cored does not**, so every voucher arm came back `422 invalid dr_cr "DR"`. It FAILED rather
   than passing **only because each arm matches the refusal's SENTENCE, not its status code.** An
   arm written as `status >= 400` would have gone green on a completely different refusal.
2. The summary row was matched on `stock_item_name ?? name`; **the wire field is `item`**. Every row
   projected `undefined`, `.includes()` ran on `""`, nothing matched. It now matches on
   `stock_item_id` and **THROWS on a missing projection rather than comparing one** — had the
   fallback been a number instead of a string it would have compared `0 < 0` and read as a clean,
   meaningless false.

#### THE MUTATION PROOF — six mutations, predictions written down FIRST

| # | Mutation | Predicted | Observed |
|---|---|---|---|
| M1 | create drops the field (D43 reverted) | only `explicit_true` fails | ✅ exactly that — the `false` and `omitted` arms stayed green |
| M2 | `SetCompanyFeatures` no-ops | **both** the response and the column assertion fail | ✅ both |
| M2b | M2 **plus** the handler echoes the request | column fails, **response now PASSES** | ✅ — **D43 rebuilt on purpose** |
| M3 | the routeAction arm deleted | the compile-time gate fails, then 403s | ✅ `TestEveryCompanyRouteIsMappedAndInVocabulary` + 403 `unmapped route` |
| M4 | `authz.Alter` → `authz.Display` | **the structural gate stays GREEN**, only the ACL test fails | ✅ Data Entry PUT = **200** |
| M5 | `*bool` → `bool` | the omitted-field test fails | ✅ `{}` reset the setting |
| M6 | the mock echoes instead of storing | two vitest cases fail, one on `undefined` | ✅ exactly, including the `undefined` |

⛔ **M2b IS THE ONE WORTH KEEPING.** It is the read-back-not-echo discipline earning its place:
with the echo restored, a caller is told `true` while the column holds `false`, the response
assertion goes green, and **only an out-of-band oracle can see the difference.** That is D43's exact
mechanism, and it is one line away at all times.

⛔ **M4 IS THE ONE THAT WOULD HAVE SHIPPED.** M3 fails loudly — the structural gate catches an
unmapped route. M4 keeps the route mapped, keeps every Owner test green, keeps
`TestEveryCompanyRouteIsMappedAndInVocabulary` green, and hands a clerk the inventory escape hatch.
A one-token slip is invisible to the gate built to catch route mistakes. §4.5's lesson, generalised:
**read what the guard COMPARES.**

#### THE MOCK ROUND-TRIPS THE SETTING AND SWITCHES NOTHING — declared, not hidden

The mock has **no negative-stock enforcement at all** (its `post` never computes a closing position),
so an outward move below zero is accepted whichever way the flag is set. Not expressible as a
mock-vs-cored `row` — that corpus is refusal-shaped, and this is a value that round-trips plus an
**absent refusal** — so it is a `deliberate_divergence`, and the last case in
`src/api/companyFeatures.test.ts` is what asserts the gap is still open.

#### GATES
`go test ./... -count=1` **0 FAIL / 0 SKIP** (bracketed with a live `psql`) · `pnpm vitest run`
**1425 / 87 files** (was 1418/86; +7 in one new file) · `pnpm exec tsc --noEmit` clean ·
`pnpm contract:cored` **11/11** · `pnpm verify:demo` **34/34 ON A FRESH SEED**.

#### ⛔⛔ A GATE INTERACTION FOUND WHILE MEASURING, AND IT IS NOT THIS ROW

`verify:demo` read **27/34** on the tenant seeded 2026-08-21, and **none of it was this change** —
a fresh seed is 34/34. Six of the seven failures name `CT Control *` / `CT Dup *` parties. Those are
**fixtures the D84 contract runner leaves in whatever tenant it is pointed at**, and they have no
state code, no GSTIN and no bill-by-bill — so they fail D87's party checks by construction.

**Running `pnpm contract:cored` against the demo company BREAKS `pnpm verify:demo` on it,
permanently.** The fixture ledgers are dated 20:33 and 20:44 (L14d) and 22:59 (this session), so the
damage accumulates one run at a time and the next session will read it as a regression in whatever
it happens to be working on. ⚠️ **Either point `contract:cored` at its own throwaway tenant, or have
it reap what it creates** — cored has no `DELETE /ledgers`, so it is the former. Registered below as
**D91**.

### ✅ L15b — D91, the gate that broke the other gate. CLOSED 2026-08-22. **34/34 → 11/11 → 34/34.**

Found while measuring L15's gates, by **re-seeding instead of assuming**. `verify:demo` read 27/34 on
the tenant seeded 2026-08-21 and 34/34 on a fresh one, so the failures were residue — and six of the
seven named `CT Control *` / `CT Dup *` parties, which are the **D84 contract runner's own fixtures**.

`contract-mock-vs-cored.mts` took its company from `CO`, and every session hands it the DEMO company.
Its fixture parties have no `state_code`, no GSTIN and no bill-by-bill, so they fail three of
`verify-demo.mts:357-375`'s party checks **by construction**, and cored has no `DELETE /ledgers`.
So one gate permanently broke the other on the same tenant, a run at a time.

⛔ **THE COST WAS NEVER THE FAILING CHECK — IT WAS MISATTRIBUTION.** A session that did not re-seed
read seven failures, two of them MONEY failures, as a regression in whatever lane it was in.

**THE FIX:** the runner creates its own tenant through the product's own route. That is all it ever
needed — `seed_company_defaults` gives it the 28 groups (so `Sundry Debtors` resolves) and the `Nos`
unit (so the two stock-item rows have one), which is the whole fixture surface the corpus touches;
nothing here reads a figure. The subject flips from `browser-dev-owner` to a fresh random one, which
**reverses what the file used to say and is right because what it does changed**: minting for the
seeded owner was correct while the script was HANDED a tenant, and is wrong now that it CREATES one.

#### THE ORACLE — one tenant, one sitting, three commands
```
verify:demo      34/34
contract:cored   11/11   own tenant "CT Contract fkji8"
verify:demo      34/34   <- the point
```
And the fixtures landed where they should: **2 CT ledgers in the throwaway tenant, 0 in the demo one.**

#### THE MUTATION PROOF — and it took TWO attempts, which is the finding

| # | Mutation | Predicted | Observed |
|---|---|---|---|
| M7a | `companyID` reads `CO` again (company only) | reproduce the poisoning | ❌ **it could not** — every row `403 not a member of this company` |
| M7 | `CO` **and** the `browser-dev-owner` subject restored | 34/34 → **31/34**, exactly the three PARTY checks, **not** the money ones | ✅ exactly that |

⛔ **M7a IS THE MORE INTERESTING RESULT AND IT WAS NOT PREDICTED.** Changing the SUBJECT gave the fix
a **second, independent layer**: even if a future caller re-points this gate at a tenant by setting
`CO`, the fresh identity is not a member of it and every row fails loudly with `403` instead of
quietly writing fixtures into somebody's books. The company change makes the poisoning stop; the
subject change makes re-introducing it **impossible to do silently**. That was a side effect of
correcting the identity, not a design goal, and it is worth keeping in mind for the next gate that
borrows a tenant.

M7 also confirmed the **diagnosis**, not just the fix: it moved exactly the three party checks and
left the two money checks (BS vs Outstandings, GSTR-1 vs Domestic Sales) alone. Those were a
**different** residue — L14's deliberately-left probe fixtures — and this row never claimed them.

⚠️ **THE RUNNER STILL LEAVES ITS COMPANY BEHIND**, and that is the deliberate trade, stated rather
than discovered: no company-delete route, and `company` has 14 non-cascading FKs, so reaping needs a
script that does not exist. One more unreaped tenant among 112+ costs a little `GET /v1/me` latency;
poisoning the demo tenant cost a lane. They are named `CT Contract <stamp>` so a reaper can find them.

### ✅ L15c — D92, the refusal that cost the operator their work. CLOSED 2026-08-22. **6/6 in a browser.**

⛔ **FOUND BY MEASURING A TRAP INSTEAD OF REASONING ABOUT IT.** `HANDOFF-RECON-LAST-16.md` §3.7 flags
the master-create frame-pop as something to *"decide deliberately"* for D87. Rather than decide from
the code, it was DRIVEN — name a ledger, leave the group blank, Ctrl+A — and the form was gone, the
name with it, with the reason on the Gateway.

`navMachine`'s `accept` reducer pops a master-create frame **unconditionally**, which was correct
while the only way to reach it was a successful create. The shell could not tell a refusal from a
success: `onAccept` returned a plain string for both.

⛔ **THE BREAKUP REFUSAL'S OWN COMMENT ASSERTED THE OPPOSITE** — *"so the operator learns it while the
figures are still in front of them rather than from a status line after the frame has popped"* —
describing behaviour the product did not have. **A comment asserting the fix is not the fix**, and
this one had been read by three sessions.

**THE FIX IS AN EXPLICIT SIGNAL:** `screenRefusal(message)` → `ScreenActionResult.refused` → the shell
shows it, consumes the key, and does not forward it to the machine. A thrown error would be
indistinguishable from a real bug at every dispatch site; a magic string is what a success returns.
Only opt-in handlers take the path.

⚠️ **SEQUENCED BEFORE D87 DELIBERATELY.** D87 makes this refusal the COMMON case (every party keyed
without a state code) and adds four more fields to lose. **Refusing on the common path while
discarding everything keyed would make D87 a net harm**, so this is a prerequisite, not polish.

| Arm | Result |
|---|---|
| refusal → the form is STILL UP | ✓ `master-opening-balance` present |
| refusal → **everything keyed survived** | ✓ `master-name` still reads `"D92 Probe Party"` |
| the reason reached the operator | ✓ on a screen they can still act on |
| §4.4 the remedy works from the SAME frame, no retyping | ✓ |
| ⛔ **CONTROL: a success still creates and pops** | ✓ asserted on the creation MESSAGE, not on the form having closed |

**MUTATIONS — M8** (delete the shell's early return) and **M9** (make `refuse` return a plain string
again) produced **identical** failures, as predicted: the two refusal cases fail, **the success
control stays green in both**. That insensitivity is what makes it a control rather than a fourth
assertion. The pair also confirms the halves are exactly that — a pair: the signal is worth nothing
unless the shell honours it, and vice versa.

⚠️ **§4.6 FOR THE THIRD TIME IN ONE SESSION.** The browser arm first checked `body.innerText` for the
typed name and reported it LOST against the FIXED build, while vitest said it survived —
**`innerText` does not contain input values.** It now reads `.inputValue()` and THROWS on an absent
field, so a missing field can never read as an empty one. The three instances this session (D77's
`p.ledger_name`, D5's `stock_item_name`, this one) were all the same shape: **a projection that does
not exist, compared instead of thrown on.**

### ✅ L15d/e — D87 (the party block) and D93 (the leaked SQLSTATE). CLOSED 2026-08-22. **13/13, both doors.**

The form rendered NONE of the fields the server had accepted all along, so every keyed party landed
`(null)|(null)|UNKNOWN` — the exact input that made D19 derive an intra-state bill as IGST. Five
fields per the user's decision: `gstin`, `state_code`, `registration_type`, `pan`, `credit_days`,
with `state_code` MANDATORY under Sundry Debtors/Creditors.

#### ⛔⛔ FIRING THE WIRE FIRST (§4.1) OVERTURNED THE PLAN TWICE, AND NEITHER WAS A CLIENT PROBLEM

The recon says *"the server has accepted all three since it was written"*. True for three of five:

1. **`credit_days` was ACCEPTED AT 201 AND DISCARDED.** `masters.Ledger.CreditDays` existed and the
   INSERT bound it; `ledgerReq` had no field to fill it from, so it was nil on every create. **D43's
   shape on a second route** — and a form field for it would have been a lie.
2. **`pan` and `credit_days` were NOT READABLE BACK.** `GET /ledgers` omitted both, so a party's PAN
   could be written and never seen again through HTTP. That is also how D87 could report `(null)` for
   a field that was in fact stored.

**Two of five, on a row whose server side the recon called done.** This is the third time this session
that ten minutes of `curl` overturned a documented claim.

#### THE PICKER'S VOCABULARY WAS TWO SHORT OF THE SERVER'S ENUM
`gst.ts` listed FOUR registration types; `masters.go` has SIX. A picker built from it makes **`SEZ`
unreachable — silently, because a missing option cannot fail** — and a supply to an SEZ unit is
ZERO-RATED, so an SEZ party keyed Regular is charged tax it does not owe. `SEZ` is now offered;
`UNKNOWN` deliberately is not (it is the ABSENCE of a statement, and every keyed party landing on it
is D87 itself). A keyed party defaults to **Unregistered — a real claim**.

⚠️ `gst.ts` had **zero importers**: its GSTIN validator, state-code helper and vocabulary were
written, tested and dead. Imported, not re-derived, so the dead copy cannot drift.

#### THE DRIVE — `drive-l15d-d87.mjs`, 13/13, BOTH DOORS
`Alt+G > Create Ledger` **and** `Alt+C` from a voucher's party field — the D19 path, and the door
where a new refusal is most disruptive because the operator is mid-invoice.

⛔ **THE REFUSAL'S ORACLE IS ZERO `POST /ledgers` ON THE WIRE, NOT A MESSAGE.** The server answers
**201** to a party with no state code — `masters.CreateLedger` creates it and then has nothing left to
refuse with — so a refusal that let the request go would be refusing a party that already exists. The
probe counts the requests.

Seven existing tests began failing on the new refusal, **exactly as the recon predicted**. They were
creating parties the way the product used to allow; each now states a state code. **The refusal was
not weakened.**

#### THE MUTATIONS

| # | Mutation | Predicted | Observed |
|---|---|---|---|
| M10 | `credit_days` discarded again | only the credit_days assertions fail; the other four fields and the "omitted is NULL" subtest pass | ✅ exactly |
| M11 | the state-code refusal deleted | the refusal test fails; **the non-party counter-case passes** | ✅ — the counter-case is insensitive by construction, which is what makes it one |
| M12 | `SEZ` dropped from the picker | **only** the SEZ test fails | ✅ — every other test picks Regular or the default, which is exactly why the ABSENCE needed its own test |
| M13 | send the LABEL, not the wire value | two tests fail; **and the server silently coerces to UNKNOWN** | ⛔ **HALF WRONG — see below** |
| M14 | blank credit days sent as 0 | only the "not 0" test fails | ✅ exactly |

#### ⛔⛔ M13 CORRECTED ME, NOT THE CODE — §4.2, and it produced D93

The prediction was that cored would **silently coerce** `"Regular"` to the UNKNOWN default, making the
label→wire mapping table load-bearing. **It does not.** The INSERT fails the enum cast and the request
is refused, so the mapping is belt-and-braces and the wire is safer than the commit claimed.

What is **not** safe is the sentence. Three fields an HTTP caller can reach were failing at the
DATABASE and leaking the driver error verbatim:

```
masters: insert ledger: ERROR: invalid input value for enum gst_registration_type: "Regular" (SQLSTATE 22P02)
violates check constraint "ledger_bank_ifsc_shape" (SQLSTATE 23514)
violates check constraint "ledger_bank_account_number_not_blank" (SQLSTATE 23514)
```

⛔ **AND THE EXPECTATION WAS ALREADY PINNED — ON ONE PATH.** `drive-l13-masters.mjs` asserts
`!/SQLSTATE/` on a refusal from THIS ROUTE (the duplicate-name arm). Three sibling refusals sat
unenforced beside one that was. **That is D80's shape (§4.5) in a new place: a guard exists, and what
it COMPARES is narrower than what it appears to cover.**

Registered as **D93** and fixed in the same wave. **The control is the test that matters:** all six
enum members still get through, including `UNKNOWN` — without it, "refuse everything" passes the
refusal test.

### ✅ L15f — D90, the row whose PREMISE was wrong and whose CONSEQUENCE was right. CLOSED 2026-08-22. **8/8.**

#### ⛔⛔ THIS IS THE MOST IMPORTANT ENTRY IN §7.5 FOR ANYONE WHO READS ONE

D90 said: *"a duplicate STOCK ITEM name is accepted (201, a NEW id) while a duplicate LEDGER name is
refused (422)."* Re-measured on the wire before a line was written, against a binary verified fresh by
symbol:

```
POST the same stock item name twice  -> 201, then 422 "already has a stock item named ..."
POST the same ledger name twice      -> 201, then 422
```

**Both refused. No asymmetry. `UNIQUE (company_id, name)` has been on `stock_item` since migration
0005** — the very first schema. D84's measurement was wrong, the recon it "overturned" was right, and
**a user decision was taken on the strength of it.**

⚠️ **AND THE LESSON IS NOT "MEASURE LESS."** Measuring is what found the real defect underneath.
Uniqueness was **BYTE-EXACT**, so all of these coexisted:

```
'Acme Traders'      'acme traders'      'Acme Traders '
```

Three masters, **indistinguishable in every picker** — which is exactly the damage D90 predicted for
its own non-existent mechanism, reached by a mechanism nobody had measured. It applied to **ledgers
and units** too, and **the ledger case is the worst of the three and was not in D90 at all**: a party
keyed twice with different capitalisation splits one supplier's outstandings across two accounts.

⛔ **THE RULE THIS YIELDS: A MEASUREMENT IS EVIDENCE, NOT PROOF.** §4.1 says a recon is for finding the
code and never for filling in an expected value, and it is right — but the counterweight now exists
too: **a measurement that OVERTURNS a documented claim deserves the same scepticism as the claim it
overturns.** D84's overturning of the recon was celebrated in three handoffs and was false.

#### THE FIX — migration 0075
Expression indexes on `lower(btrim(...))` for `ledger`, `stock_item` and `unit`, replacing the
byte-exact constraints.
- **Not `citext`** (changes the column's type and every comparison with it) and **not a normalising
  trigger** (discards the operator's capitalisation — `HDFC Bank` must still PRINT that way). An
  expression index changes what **collides** and nothing about what is **stored**.
- **`btrim`, not a whitespace fold.** `Acme  Traders` stays distinct, deliberately: an inner double
  space is VISIBLE in a picker; a trailing space and a capital letter are not.
- Verified safe on existing data **before** it was written: zero colliding groups across 208
  companies, 3,782 ledgers, 211 items, 301 units. The proving query is in the migration.

#### ⛔ THE MIGRATION RENAMES THE CONSTRAINTS, AND `nameTaken` MATCHES THE NAME EXACTLY
Left alone, every one of these refusals would have kept refusing and started coming out as a **raw
wrapped driver error instead of a sentence** — **D93's exact shape, one migration away at all times.**
That is why the tests assert the SENTENCE and not the status code.

#### THE MESSAGE NAMES THE STORED MASTER — and the obvious way does not work
Letting the index fire and then reading the stored name **fails**: by then the transaction is in a
FAILED state and Postgres rejects every further query in it. Measured on the wire — every refusal fell
back to quoting the caller's own text, so an operator who typed `"d90x ..."` was told they already had
a stock item named `"d90x ..."`, **which they can see they do not.** The lookup now runs BEFORE the
insert; the index is still the authority and still catches the race.

#### THE MOCK'S COMMENT NAMED A FUNCTION THAT DOES NOT EXIST
*"create-or-select by NAME mirrors `masters.EnsureStockItem`"* — there is no `masters.EnsureStockItem`.
The mock was the loose one, the exact class D84 exists to stop, and **it survived because the corpus
recorded it as a DELIBERATE divergence on the strength of the wrong measurement.** That divergence is
**DELETED, not edited**: it certified behaviour that never existed. The corpus `$provenance` now
records its own error.

#### THE DRIVE — 8/8, and ⛔ THE ORACLE IS THE PICKER, NOT THE REFUSAL
A refusal message proves the server said no; it does not prove the operator can no longer end up with
two indistinguishable rows. The probe counts the ledger rows and requires **ONE where two creates were
attempted.** Two controls: a genuinely different name is still accepted, and an inner double space is
still distinct.

#### ⚠️ AN ASYMMETRY THE DRIVE RECORDED AND DID NOT FIX — FOR D81's LANE
**D92 made a CLIENT-side refusal non-destructive. This is a SERVER refusal, so the frame still pops
and the keying is still lost** (which is why D47 exists at all). **The product now refuses in two
different ways.** That seam is register row D81's — it is the same seam, a server refusal leaving the
operator holding nothing — and folding it in here would put two rows in one commit with one oracle
between them. ⛔ **D81's lane owes this.**

---

### ✅ L16 position 0 — THE FOUR `PARTIAL` ROWS. AUDITED 2026-08-23. **D13, D15, D17, D48 resolved; D94 registered.**

⛔ **THIS LANE WAS A READ, NOT A BUILD, AND IT IS THE ONE THE DRIVER PUT FIRST FOR A REASON.** Four rows
had been counted as CLOSED by every session since F6/F8 while the table said `PARTIAL`, and one of them
was **scoping the next lane**. No product code changed. What changed is that the register now says what
is true, and one hidden build is written down.

**What each row turned out to be:**

| Row | Verdict | The thing that settled it |
|---|---|---|
| **D17** | ✅ CLOSED — **and it had been closed for two waves, never re-marked** | The converter takes `billAllocations` AND `postDraftBody` actually passes them. **The call site is the half that matters** — a converter nobody calls with refs is the same defect wearing a fix. `draftAccept.test.ts` **56/56** at HEAD, guarding BOTH directions |
| **D48** | ✅ CLOSED — **and the code goes further than the row asked** | `DraftAcceptFailedError` names the minted masters in a sentence. **And the L15 posting claim is taken BEFORE `resolveDraftLedgers` deliberately for this row's reason**, so a race loser now mints nothing at all |
| **D15** | ✅ CLOSED, residue closed too | **SIX layers now.** Residue = §7.3 B (the CONFIRM, after the money moved — ✅ **CLOSED 2026-08-25: queued durably and retried, AND the draft id is on the voucher under a unique index in the same transaction, §7.6**) **plus four windows at the CLAIM, enumerated in full at the code site** |
| **D13** | ✅ CLOSED (breakdown half) + ⛔ **D94 registered** (payables) | The build was real, and a `PARTIAL` row was hiding it |

#### ⛔ THE FINDING THAT PAID FOR THE LANE — A WHOLE REPORTING DIRECTION IS LIVE ON THE WIRE WITH NO DOOR
`GET /outstandings?kind=PAYABLE` answers **200 · total 1,04,050.0000 · 2 creditors · 4 bills**, each with
its per-bill array, against a binary verified fresh by symbol. `?kind=payable` works, `?kind=BOTH` is
refused **400**. **Every supplier bill the business owes is being served right now and the app cannot ask
for it** — the client's query object has no `kind` member, and there is one report name, one screen and
one PDP resource. That is **D94**, and it is a build because `routeAction` decides on the flat literal
PATH: a `?kind=` toggle would make *"may see debtors, not creditors"* an **unwritable rule**.

#### ✅ §5.1 HELD, AND THIS TIME THE REGISTER'S CLAIM SURVIVED IT
The register asserted "the server serves both; `kind=PAYABLE` returns 200". Three sessions running, ten
minutes of `curl` had overturned such a claim — so it was fired at the server rather than trusted, and
**it was right**. ⚠️ **That is worth recording precisely because the rule is not "the recon is always
wrong"** (§5.2's lesson from the other side). It is *"an unfired claim has no status."* Firing it also
bought the row its real scope: the build is a NAME, an ACL migration and a screen — **not a query
parameter**, and not the server work the row might have implied.

#### ⛔ THE DRIVER'S OWN COUNT SNIPPET WAS WRONG THE MOMENT A NEW ROW WAS WRITTEN
§0 warns that CLOSED rows say "OPEN" in prose and tells you to test `✅` first. **D94's prose contained
the word `PARTIAL`, so the snippet filed the register's newest OPEN row as PARTIAL** — the same defect
class, from the other side, introduced by the very act of registering a row. **Fixed properly: the
parser now reads the STATUS COLUMN (the last cell), not the line.** ⚠️ **It was validated by
re-deriving `93 · 81 · 8 · 4` from the previous commit** — a parser change that cannot reproduce the old
number on the old file is a redefinition pretending to be a fix. The row's prose was reworded too, but
that is the weaker of the two fixes and on its own it would have failed again on the next row.

#### ⛔ WHAT THIS LANE HANDS TO D81 (the driver's position 2) — IT IS NOW SMALLER AND SHARPER
D48 was the scoping question, and the answer is **the draft path is genuinely covered**. So D81 is
**exactly** the two halves already named — the VOUCHER path's orphan masters on a server refusal, and
D92's screen-side loss — **and it has two working shapes to copy rather than a design to invent**:
`DraftAcceptFailedError`'s naming sentence, and **the claim-before-ledger-create ORDERING, which is the
stronger of the two** because disclosure explains an orphan while ordering prevents it.

#### ⚠️ AND A SERIALISATION THE DRIVER DID NOT HAVE
**D94 and D86 both move the Outstandings report** — D86 changes its `as_on` bounding in three places at
once. They must not be concurrent, and whichever lands second rebases on the first.

---

### ✅ L16 position 1 — D22 + D52, THE FORECAST PLANE'S DOOR. CLOSED 2026-08-23. **34/34 in a browser, keyboard only.**

⛔ **THE SHAPE OF THIS DEFECT IS THE MOST REUSABLE THING IN THE ENTRY.** cored had served
`GET /cash-flow` and `GET /anomalies` since wave 2 — routed, ACL-mapped, in the PDP vocabulary, with a
long comment in `handler_forecast.go` explaining that `estimate`/`method`/`basis` were added *"so that
the screen has something true to render the moment it exists"*. **The screen did not exist.** No Gateway
entry, no Go To entry, no client method, no component — and F11 advertised the capability as `on` with a
note that named no door, while both its neighbours named theirs.

**So every question that reads the SERVER answered "built" for a whole wave.** `curl` said 200. The
route walk said mapped. The PDP vocabulary said present. The disclosure was on the wire and pinned by a
Go test. ⛔ **A DISCLOSURE WITH NO RENDERER IS A COMMENT IN A GO FILE**, and nothing in the backend's
own gates can tell the difference.

#### WHAT SHIPPED
Two report names (`"Cash Flow"`, `"Anomalies"`), two screens in `ForecastScreens.tsx`, two client
methods, the mock arms, the four name registries, and D52's two corrected feature rows — **one commit**,
because a door without the sentence leaves the note naming no door and the sentence without the door is
worse.

**TWO NAMES AND NOT ONE SCREEN WITH TABS**, which is the order books' argument reused: the PDP decides
on the report NAME and a tab is invisible to it. A cash projection is a management report about money;
the anomaly list is a **control surface naming operators' irregular entries**. *"May project cash, may
not audit who backdated what"* is an ordinary rule that one tabbed screen would make unwritable.

#### ⛔⛔ THE ORACLE — AND WHY THE HEADING ARM WOULD HAVE BEEN WORTHLESS
```
the screen's three basis counts   9 + 0 + 3  = 12
the OUTSTANDINGS report's bills   8 + 4      = 12
```
Three figures **read off the Cash Flow page**, footed against **a different report's route**. The
projection's job is to give every open bill a date and invent none, so the count must survive the trip
exactly. ⚠️ **Every other arm in the probe has both sides coming from the same handler** — comparing the
screen against `curl /cash-flow` is the screen re-reading its own source. This was the only arm that
could see a projection that lost or duplicated a bill, **and the M3 mutation proved it was the only one.**

Also on the wire, and worth keeping: `Ctrl+P` surfaced **the server's own words** — *"the ACL rule for
print report:cash-flow is honoured, but no printed layout has been specified"*. That is the proof the
name is MAPPED rather than locally refused, and **a locally-invented refusal could not have said it.**

#### THE TWO MUTATION PROOFS — PREDICTED IN THE COMMIT, THEN OBSERVED
| | mutation | predicted | observed |
|---|---|---|---|
| **M1** | delete the disclosure panel's render | the run DIES at `cash-flow-basis`; the three door arms stay green; the footing never runs | ⛔ **exactly that** — `Error: data-testid="cash-flow-basis" is NOT on the page. This probe refuses to compare an absent projection` |
| **M3** | render the three basis sources TOTALLED into one figure — the design the screen refuses | **FOUR failures and no more**: due 12≠9, party 12≠0, default 12≠3, footing 36≠12; ESTIMATE/method/all D52 arms stay GREEN | ⛔ **30/34, exactly those four** |

⛔ **M3 IS THE ENTRY'S REAL LESSON. A TOTALLED DISCLOSURE STILL LOOKS LIKE A DISCLOSURE.** The page still
said "THIS IS AN ESTIMATE", still carried the method verbatim, still listed all four `not_modelled`
lines, still named the 30-day convention — and every one of those arms passed while the numbers lied.
**Only the counts and the footing saw it.** Any probe built out of "is the disclosure on the page?"
assertions would have been green through it.

#### ⚠️ THE §5.11 VITE CHECK HAS A RACE, AND IT NEARLY COST A REAL MUTATION
M3's first `curl` of the module out of vite counted **0** occurrences of the mutated construct — which
reads exactly like *"the mutation never landed"*, and the honest next step would have been to write a
better mutation. **It had landed; the curl beat the dev server's re-transform.** A second curl counted
3 against a HEAD baseline of 0. ⛔ **CURL THE MODULE TWICE, OR CALIBRATE AGAINST THE FILE ON DISK
FIRST** (`grep -c` the source said 3 immediately). This belongs beside §5.11.

#### WHAT THE LANE FOUND THAT IT WAS NOT LOOKING FOR
- ⛔ **D95 — neither name has an ACL counterweight.** Migration 0011's coarse `allow display report`
  matches any `report:NAME` at specificity 1, so **a report name arrives PRE-GRANTED and the refusal is
  what must be written.** Eight migrations write exactly that refusal for every comparable report
  (0026/0028/0033/0035/0063/0068/0072/0073). These two have none, so **a Data Entry clerk can read the
  list of their own backdated and after-hours entries** — the list a supervisor reads about them.
  Predates the door; registered rather than silently decided, and **the cash-flow half needs the user.**
- ⚠️ **D96 — insightd's statistical forecast still has no renderer**, extracted from D22 *on closure*.
  It was D72's surviving clause, folded into D22 — and D22's door was built over **cored's** endpoint, a
  different service with a deliberately different method. ⛔ **A clause living inside another row's text
  disappears the moment that row is marked closed. That is the D13 failure position 0 had just finished
  repairing, and it would have recurred in the same session.**
- ⚠️ **A GATE WAS ASSERTING THE ABSENCE OF THIS DOOR.** `printTarget.test.ts` used `"Cash Flow"` as its
  stand-in for an unmapped report name, so shipping the screen turned that test into an assertion that
  the door does not exist. **Repointed, not deleted** (§5.16): the branch is still live and now reachable
  only by an OFF-VOCABULARY name, since `REPORT_SLUGS` is total over `ReportName` by construction.
- ⚠️ **A PINNED COUNT MOVED AND WAS ALREADY WRONG.** F11's *"all NINE reports in the Alt+G list"* became
  ELEVEN — and the four items it enumerated afterwards were **already members of that same list**, so the
  sentence had been double-counting before anyone touched it.
- ⚠️ **I WROTE A FALSE CLAIM INTO F11 AND CAUGHT IT BY CHECKING.** I added "the Bank Reconciliation
  Statement" to the exportable list, then grepped `useExportAction`: no BRS screen registers it. Removed
  before shipping. **The lane's own output needed the same suspicion as the recon's.**
- ✅ **A PROBE ARM PASSED FOR THE WRONG REASON AND WAS CAUGHT.** The "Cash Flow Statement must not
  alias" arm asserted `h1 !== "Cash Flow"` and passed with `h1 === ""` — passing because *nothing* was on
  the page. Now it also asserts positively where the operator ended up (still in the picker). 33/33 → 34/34.

#### A DECISION WORTH KEEPING: THE ALIAS THAT WAS DELIBERATELY NOT ADDED
`"cash flow statement"` and `"funds flow"` are **not** aliases of this report. In Tally those name a
HISTORIC statement of cash that actually moved; this is a forward projection of invented settlement
dates. **An alias that silently answers a different question is worse than no alias** — the operator
reads projected figures as recorded ones, which is the exact confusion the whole disclosure block exists
to prevent. The drive asserts the phrase leads nowhere.

---

### ✅ L17 — D95, THE FORECAST PLANE'S ACL COUNTERWEIGHT. CLOSED 2026-08-23. **20/20, wire + browser.**

Migration **0076**, derived verbatim from 0073. The first row of `HANDOFF-CLOSE-THE-FINAL-NINE.md`.

⛔ **THE DEFECT IS THAT THE GRANT IS THE DEFAULT.** 0011 seeds Data Entry a coarse
`allow display report` which `internal/authz` matches against ANY `report:NAME`, so a report becomes
readable the moment its name enters `KnownReportNames` — **the refusal is the thing somebody has to
remember to write.** Eight migrations write it for every comparable report. `cash-flow` and `anomalies`
had none, and `anomalies` is the **control surface that names, by voucher number, the reader's own
irregular entries.**

#### ⛔ THREE CLAIMS CHECKED BEFORE ANY CODE MOVED, AND ONE OF THEM WAS FALSE
| claim | source | verdict |
|---|---|---|
| the two tokens have no counterweight | the register | ✅ **TRUE, and measured IN THE DATABASE** — the demo tenant's Data Entry level carried 92 `acl_rule` rows, 19 distinct `report:NAME` disallows, the coarse allow row, and **zero** rows mentioning either token |
| 0073 is the highest restatement, so 0076 sorts above it | recon | ✅ TRUE — 0074 and 0075 exist but neither restates the seeder. Derivation **proved, not claimed**: diffing the two restatements with comments stripped yields **exactly one added line** and the PERFORM count goes 13 → 14 |
| the coarse rule matches "at specificity 1" | ⛔ **eight migration headers** | ❌ **FALSE, off by one.** `ruleSpecificity` sums an exact-ACTION point and a resource weight, so `allow display report` scores **2** and `disallow display report:cash-flow` scores **3** |

⛔ **THE SPECIFICITY ERROR IS IN EIGHT FILES BECAUSE EACH WAS DERIVED FROM THE LAST**, and it survived
because it is internally consistent and the conclusion it supports is correct. 0076's header states the
real arithmetic and refuses to propagate it into a ninth. ⚠️ **The recon agent flagged this as
`uncertain` rather than asserting it — which is the only reason it got checked.** §5.1 applies to
documentation that agrees with itself.

#### ⛔⛔ HALF THE ROW'S OBVIOUS ORACLE WAS VACUOUS, AND HALF WAS IMPOSSIBLE
The driver said: *"`GET /anomalies` → 403 · its print form → 403 · `GET /cash-flow` → 403 · print → 403
… AND A CONTROL ARM: an Owner still reads all four."* **Both halves of that were wrong:**
- **The two print arms pass BEFORE the migration exists.** 0038 seeds zero `allow print report:`, the
  coarse grant is `display`-only, and the level's `default_access` is `none` — so print of every report
  is already default-denied. Only the **display** arms go red-to-green. A probe reporting "4/4 green"
  would have been half a lie, so **the migration header, the Go test's name and the probe's own arm
  labels all say "durability, not a fix"**.
- **An Owner CANNOT read the two print forms.** Printing them is unimplemented: an Owner gets **501**,
  and `TestTheUnimplementedReportsAreStillGovernableAndSaySo` actively requires it. **Asserting 200
  would have failed on correct code.** The arm asserts 501 *and* that the body names the report, because
  the Owner's refusal is a MISSING LAYOUT and the clerk's is a POLICY DECISION — two different answers a
  test must not blur.

#### THE MUTATION PROOF — PREDICTED, THEN OBSERVED, AND IT CORRECTED ME ON A SECOND POINT
**M1: hoist the new PERFORM above the coarse-seed `IF NOT EXISTS` guard.**
**Predicted:** `TestDataEntryIsRefusedTheForecastPlane`'s CONTROL arm fails (Trial Balance 403, want
200) while **both refusal arms still pass, for the wrong reason.**
**Observed:** exactly that — `CONTROL: Data Entry must still read the Trial Balance = 403, want 200`,
and because the control is a `t.Fatalf` the run **aborts before the refusal arms can report a false
green.** Confirmed in the database: the mutated seeder left the level with **`allow display report`
absent and only 3 of the 18 voucher-class rules**.
⚠️ **AND THE PROOF CORRECTED A SECOND PREDICTION I HAD MADE (§5.3).** I also predicted the print-star
test would fail on its row count. It **passed**, and the reason matters: **thirteen of the fourteen
seeders run UNCONDITIONALLY after the guard** — only the voucher classes and the coarse report grant are
inside it. So the level still had all 22 `disallow print` rows. My mental model of "the guard skips
everything" was wrong, and the mutation is what said so.

#### TWO EXISTING GATES MOVED — BOTH REPOINTED, NEITHER DELETED (§5.16)
- **`TestAnOwnerWritingOneConvenientAllowPrintStar…`** pins the seeded level's `disallow print` count
  and demands every row **declare what it discloses**. It went 20 → 22 and its own message asked for the
  declaration. ⛔ **This also proves the print rows are NOT inert:** they are the differential that makes
  one convenient `allow print *` safe. The anomaly entry notes it is **the only row in that whole table
  that is not a disclosure about the BUSINESS — it is a disclosure about the OPERATOR.**
- **The same test's final arm named `cash-flow`** as its example of "authorized by the convenience rule
  but has no layout" — *precisely because* it had no seeded refusal, which 0076 removes. Repointed to
  `gstr1`, **with the constraint written down**: a stand-in must be in `KnownReportNames` with no seeded
  `disallow print` AND no implemented layout, and **exactly four names still qualified** (`gstr1`,
  `gstr3b`, `itc`, `reconciliation-queue`). If that set ever empties, grant an explicit allow rather than
  deleting the arm.

#### ⚠️ MY OWN PROBE READ THE WRONG FIELD NAME — AND FAILED LOUDLY ONLY BY DESIGN
The backfill test read `body["levels"]`; the wire key is **`security_levels`**. It failed instead of
passing vacuously **for one reason**: the not-found case is a `t.Fatalf`, not a skipped assertion. That
is §5.7 made mechanical — an absent projection compares cleanly against anything, so **make the probe
throw rather than resolving to read carefully.**

#### ✅ THE BROWSER ARM IS THE ONE WORTH KEEPING
L16 shipped a 403-as-a-decision panel **on speculation** — nothing had ever refused those screens. This
row is its first real refusal, and it renders: the clerk reaches Cash Flow from the keyboard and reads
*"Your security level may not open the Cash Flow projection. This is a decision recorded against your
level, not a failure."* plus the sentence that printing is a separate permission. ⛔ **A screen built for
a refusal that has never happened is unproven until something refuses it.**

#### ⛔ AND THE REAP TAUGHT SOMETHING THE HANDOFF ONLY WARNED ABOUT
`HANDOFF-CLOSE-THE-FINAL-NINE.md` §4 says reaping needs a written script because *"`company` has 14
non-cascading FKs"*. It has **fifteen**, and the four-table delete the probe first tried **failed on
`notification_outbox_company_id_fkey`** — company creation writes an outbox row. ✅ The transaction
**rolled back cleanly**, so nothing was half-deleted. The working order for a BOOK-LESS tenant is now in
`drive-l17-d95.mjs`, measured rather than guessed:
`acl_rule · company_membership · company_invite · notification_outbox · ledger · godown · unit ·
voucher_type · account_group · security_level · company`
⚠️ `account_group` self-references via `parent_id` and the single-statement DELETE handles it fine.
⚠️ **A tenant WITH BOOKS needs more than this**, which is why the probe refuses to reap one that has any
voucher versions and says so instead.

### ✅ L18 — D81, THE ORPHANED MASTERS. CLOSED 2026-08-24. **12/12, wire + browser. AND HALF THE ROW WAS NOT THERE.**

**Stack:** cored `:8087` symbol-checked (`p.amount DESC, p.id` ×2, `identity: set company features`,
`is not a GST registration type`, `apart from capitalisation` — 2/1/1/1), vite `:5173` verified to be
serving the change ITSELF (§5.11: `curl localhost:5173/src/screens/VoucherScreen.tsx | grep -c
discloseOrphanedMasters` → 2), migrations 60, probe tenant minted and reaped.

#### ⛔⛔ THE ROW WAS TWO CLAIMS AND ONLY ONE OF THEM WAS TRUE

D81 read *"a server refusal leaves masters behind AND throws away the keying"*. The first half is real
and worse than written. The second **does not reproduce, and never did.**

**Half A, measured at HEAD before a line was written:** a complete GST sales invoice refused for a closed
period left **FOUR** accounts on the chart — `Output CGST`, `Output SGST`, `Output IGST`,
`Domestic Sales` — and the status line read `Error: period is closed for back-dated entry`. Four masters,
named by nothing. The chart of accounts is what an auditor reads.

**Half B, measured the same way:** every keyed value survived. Ledger, amount, narration, date, the party
fed through the pick list, the GST block — byte-identical before and after, on the single-entry payment
form AND the GST sales form, and again in a real browser after a genuine 403. ⛔ **The accept reducer
replaces the FRAME, but the form is component state in `AccountingVoucherScreen` and only two things
reset it: a voucher-CLASS change (F5/F7/F8) and Alt+X.** The entry mode survives too — `Ctrl+H` writes it
to `ctx.entryModeByType`, which the fresh frame re-reads.

⛔⛔ **AND THAT MATTERED, BECAUSE THE USER HAD ALREADY DECIDED HALF B.** §6.7 was answered "shape (b) —
the reducer stays put, the screen advances on success", chosen specifically to save the keying. Building
it would have moved ownership of *"the entry succeeded"* out of the machine and into **both** voucher
screens, to fix something that was not broken — and if either screen failed to advance, bulk entry
breaks silently, which is worse than the defect. **The user was re-asked with the measurement and closed
half B as not-reproducing.** This is D90's shape for the second time: *the row's premise was false and the
consequence was still worth acting on.* ⛔ **§5.2 IS THE MOST LOAD-BEARING RULE IN THIS FILE.**

**The residual, recorded rather than built:** a refused create still stamps the frame `saved: true`. It
drives a "Saved" badge that the error status masks, so nothing renders it today. Not a row.

#### THE FIX
`VoucherScreen.onAccept` reads the chart BEFORE the builder runs and, on a refused POST, diffs the
request's ledger ids against it and names every master this accept minted.

- ⛔ **THE SNAPSHOT IS A DIRECT `client.listLedgers`, NEVER `gstVoucher`'s `ledgerIndex`.** `ensureLedger`
  writes each id it mints straight into that shared index, so a "before" picture taken from the cache
  already contains the masters this accept is about to create: the diff comes back EMPTY and the
  disclosure is **silently absent**, which is indistinguishable from having nothing to disclose.
- ⛔ **THE IDS ARE FOUND BY A WALK, NOT A FIELD LIST.** They sit at `postings[]`, `party_ledger_id`,
  `item_ledger_id`, `bill_allocations[]` and `gst.{c,s,i}gst_ledger_id`, and `types.ts` carries
  `tds_/deductee_/buyer_ledger_id` besides. An enumeration stops covering the body the day a tenth field
  is added, and the failure is the quiet one.
- ⛔ **ONE SENTENCE, TWO CALLERS.** `voucherAcceptFailure` in `draftAccept.ts` is now the only place the
  orphan wording is written, and `DraftAcceptFailedError` is renamed `VoucherAcceptFailedError` because it
  is no longer draft-specific. This module's own §1.3 lesson is that a second copy is how a defect gets
  fixed once and shipped twice.
- ⚠️ **AND IT IS BEST-EFFORT, NEVER FATAL.** The read exists only to word an error message, so its failure
  costs the SENTENCE and never the ENTRY. A disclosure that cannot be made accurately is not made at all.

#### THE DRIVE — `frontend/scripts/drive-l18-d81.mjs`, 12/12
The server-only refusal is a **back-dated cutoff on the Owner level** (the one level whose cutoff may be
set). It is judged on `VoucherDate`, so `POST /ledgers` — which carries no date — is **not** refused: the
masters are created and the voucher behind them is not. ⛔ **That asymmetry IS the defect**, and it turns
this from an ACL provisioning exercise into a one-field browser drive.

- ⛔ **THE MINTED SET COMES FROM THE WIRE.** `GET /ledgers` before and after, diffed by id by the script:
  9 → 10, one master, named in the status line. Both sides from one handler would prove only that the
  handler agrees with itself (§2.2).
- ⛔ **THE ADVICE IS EXERCISED, NOT JUST READ.** The sentence promises *"retrying this accept REUSES it
  rather than creating more"*. The control clears the cutoff, presses Ctrl+A again, and asserts the retry
  **POSTED while creating ZERO further ledgers** (chart 10 → 10). Books read back by SIDES and LEDGER
  NAMES: `C Cash | D D81 Orphan Expense …`. **§5.19 says a disclosure can be fully present and fully
  wrong; this arm is what tells them apart.**
- ⚠️ **The keying arm is labelled a PIN, not a fix** — green before the change, and it says so (§5.23).

#### THE MUTATIONS — predicted, then observed, and one of them corrected me
- **M1 — the ORDER is load-bearing.** Moved the snapshot to AFTER the builder. **Predicted:** arm 1 fails,
  the minted diff being empty. **Observed:** it did — *and a second arm failed that I had predicted would
  pass.* With the order swapped, the builder's own `ledgerIndex` read becomes the first read after the
  probe's fail-flag, so `ensureLedger` throws and the voucher never posts at all. ⚠️ **§5.3 again: the
  proof corrected the predictor, not the code.**
- **M2 — the DIFF is load-bearing.** Replaced `!known.has(id)` with `true`. **Predicted:** arm 1 fails on
  `/4 new ledger masters/` (it would claim 5, including the pre-existing party) and control 2 fails by
  naming accounts that existed before. **Observed:** exactly both.

#### ⚠️ THREE THINGS THE PROBE COST BEFORE IT WORKED, ALL OF THEM THIS REPO'S OWN RECURRING SHAPES
1. **§5.6, in the unit tests.** The "a failed chart read cannot make a voucher unpostable" arm used
   `mockImplementationOnce` — which the **party pick list consumes AT MOUNT**. It passed having tested
   nothing. A flag makes the accept's read the one that fails and a counter proves it did.
2. **§5.7, on the wire.** The postings arm read `ledger_name` and `"DR"/"CR"`; cored says `ledger` and a
   **one-letter** `dr_cr`. It printed `C undefined | D undefined` — and only showed at all because the arm
   asserts CONTENT rather than a count.
3. **The reap does not port from L17.** That tenant was BOOK-LESS; this one posts on purpose. There is no
   `cost_allocation` (it is `posting_cost_allocation`) and no `voucher` (it is `voucher_head`);
   `bill_allocation` hangs off `voucher_version_id`, not `posting_id`; and `voucher_head.current_version_id`
   means the head must be deleted BEFORE the version. ⛔⛔ **And `posting` is APPEND-ONLY BY TRIGGER**
   (`posting_no_mutate`, `voucher_version_no_mutate`). The reap uses `SET LOCAL
   session_replication_role = replica`, which is **transaction-local and reverts at COMMIT**. It is spelled
   out loudly on purpose: suppressing an immutability trigger must never read as routine, and it is
   permissible only because the tenant was minted by the probe sixty seconds earlier and its books are the
   two postings the probe itself wrote. ⛔ **NEVER run that shape against books an operator keyed.**

### ✅ L19 — D96, THE FORECAST NOBODY COULD EXPLAIN. CLOSED 2026-08-24 BY **DELETION**. **154 pytest + the wire.**

**Stack:** insightd `:8088` restarted onto the change and re-read from the wire; `insightd/.venv` (the
gate's interpreter — see the trap below); cored `:8087` and vite `:5173` untouched by this lane.

#### ⛔ THE ROW ASKED A PRODUCT QUESTION AND THE ANSWER WAS "NO"
D96 was D72's one surviving clause, extracted when L16 closed D22. Its own text said the honest prior
question was *"does it belong in the product at all?"* — and it did not. `POST /companies/{id}/forecast/
cashflow` was a Holt-Winters estimate over an injected history frame, mounted behind a
`try: import statsmodels`, **called by nothing.** ⛔ **The reason for deleting it is NOT that it was
unused.** cored's WS5a projection is explainable to an auditor **bill by bill** — its own `method` string
says *"deterministic; no statistics, no model"*. A second cash-forward number, by a different method,
with no auditor story, invites exactly the confusion the Cash Flow screen's whole disclosure block exists
to prevent. **A forecast nobody can explain is worse than no forecast.**

⚠️ **AND IT THEREFORE NEEDS NO REPORT NAME AND NO ACL COUNTERWEIGHT** — the contrast with D95/migration
0076 is exact and worth keeping: 0076 existed *because* the forecast plane's two **cored** reports are
reachable and a clerk could read them. There is nothing here left to deny.

#### ⛔⛔ IT LOWERS D49 — 9 APPLICATION ROUTES, NOT 9 ANY MORE
`routes_forecast.py:54` was **one of D49's nine**, and D49's driver entry says "9 routes, not 7 — 13 with
the auto-docs" in three places. It is now **8 application routes and 12 with the auto-docs**, measured
from the live `/openapi.json` after the restart. ⚠️ **This is precisely §5.24's shape** — a number
propagated because each copy was derived from the last — so it is corrected in the driver in the same
pass, not left for D49's session to trip over.

#### THE PROOF — A DELETION ORACLE, WHICH IS THE PROOF A DELETION ACTUALLY OWES
The row said it plainly: *"the row still owes a proof — that nothing imports it and no route serves it,
asserted by a test rather than by a grep in a commit message."* `tests/test_forecast_plane_is_gone.py`
asserts the absence **three ways**, because each alone is weaker than it looks — **a module can exist
unimported, a route can exist unpublished, and an import can survive a route**:
- the PACKAGE is unimportable, on all three module paths;
- the ROUTE TABLE **and** the PUBLISHED SCHEMA carry no forecast path (kept separate: `/openapi.json` is
  the surface a deployer or an integrator actually reads, and the two can disagree);
- **NO SOURCE FILE still imports it** — the half a route check cannot see, and it mattered here: the old
  mount **swallowed `ImportError`**, so a dangling import could have taken a real route down and been
  reported as *"statsmodels isn't installed"*.

⛔⛔ **AND THE `try/except ImportError` MOUNT IS WHY A ROUTE-TABLE CHECK ALONE WOULD HAVE BEEN VACUOUS.**
Before the deletion, a route-only version of this test would have **PASSED on any machine without
statsmodels** — the route is simply not mounted there — while the code, the dependency pin and the tests
were all still in the tree. **That is a test that looks green and guards nothing.** The package and
source-scan arms are what make it hold on every machine.

#### ⚠️⚠️ THE DELETION WAS INCOMPLETE UNTIL THE TEST SAID SO — AND THIS APPLIES TO EVERY DELETION HERE
`git rm` removed the tracked files and left **`insightd/insightd/forecast/` on disk**, holding a stale
`__pycache__`. Python imported the surviving directory as an **IMPLICIT NAMESPACE PACKAGE**, so
`import insightd.forecast` still succeeded and the module "existed" with no source at all. The first run
of the new test caught exactly that. ⛔ **`git rm` IS NOT A DELETION — check the directory and the
`__pycache__` too.**

#### ⛔ THE GATE'S INTERPRETER, AND A FALSE RED THAT COST TIME
`python3 -m pytest` from a shell reports **`test_hitl_invariant.py::test_only_allowed_routes_mutate`
FAILED with `paths == set()`** — which reads exactly like a broken app factory. It is not: that
interpreter has neither `statsmodels` nor a working router set, so `create_app()` comes back with no
mutating routes at all. ⛔ **The gate is `insightd/.venv/bin/python -m pytest` (what `make insightd-test`
runs). Anything else is not a measurement.**

#### THE MUTATIONS — predicted, then observed
- **M1 — the incomplete deletion, reproduced deliberately.** Recreated `insightd/forecast/` as a bare
  directory. **Predicted:** only the `insightd.forecast` package arm fails, by namespace import; route,
  schema and source arms pass. **Observed:** exactly that, 1 failed / 11 passed.
- **M2 — the route comes back.** Re-added `routes_forecast.py` and the mount. **Predicted:** the
  `routes_forecast` import arm, the route-table arm, the schema arm and
  `test_hitl_invariant::test_no_route_targets_cored_vouchers_write` all fail. **Observed:** all four, plus
  two I had not predicted — the SOURCE-SCAN arm (it saw `app.py`'s new import) and
  `test_only_allowed_routes_mutate`. **6 failed / 6 passed.** ⚠️ Both extra failures are the allowlist
  doing its job: the hitl entry for `/forecast/cashflow` was **removed rather than left as a harmless
  spare**, because an allowlist naming a route that no longer exists cannot fail when that route returns.

#### GATES
insightd pytest **154 passed** — was 160; **−13 deleted, +7 new, exactly accounted for**, which is the
only arithmetic that distinguishes "tests removed with their feature" from "tests quietly lost".
Wire, after restarting insightd onto the change: `/openapi.json` **9 → 8 routes**, the deleted path
absent, a direct `POST .../forecast/cashflow` answers **404**, `/healthz` still `ok` with all six draft
routes published.

---

### L20 — 2026-08-24 · D49 CLOSED: insightd authenticated nobody, and cored is now the only door

**Position 1 of `HANDOFF-THE-REMAINING-SIX.md`.** One row, one commit. The driver called it the largest
of the wave and it was.

#### ⛔⛔ THE NUMBER WAS RIGHT THIS TIME, AND IT WAS RE-DERIVED RATHER THAN TRUSTED
Three written scopings of this row had said 7, then 9, then 8. **Measured on the live wire before a line
moved: 8 application routes, `securitySchemes: None`, and `/docs` `/redoc` `/openapi.json`
`/docs/oauth2-redirect` all 200 — twelve.** The driver's 8 was correct. ⚠️ The four auto-doc routes are
absent from `openapi.paths`, which is *why* every openapi-based count had missed them, and one
`openapi_url=None` unmounts all four (FastAPI gates /docs and /redoc on it too).

**And the defect itself, re-driven rather than quoted:**
```
GET  :8088/companies/ddb8c6ec-…/drafts   -> 200 · 15 drafts · "Pune Fabricators | 21240.0000"
GET  :8087/v1/companies/ddb8c6ec-…       -> 401
```

#### ⛔ OPTION A WAS RE-VERIFIED AS TOO BIG, WHICH IS WHY THE RECOMMENDATION ON FILE HELD
The recommendation (Option B, cored proxies) was recorded in two earlier handoffs. It was checked, not
inherited: cored's `devIssuer` generates an **ephemeral** Ed25519 key (no `AUTH_DEV_SIGNING_KEY` anywhere)
and cored publishes **no JWKS route**, so in dev there is nothing for a second verifier to verify against;
insightd's core dependency set is pydantic/fastapi/httpx/uvicorn/pypdf, with **no crypto library**, behind
its own import-lint. And the part that is not a dependency problem: **a signature proves *who*, never
*which company*.** Membership and the per-(action, resource) rules live in cored's database behind cored's
PDP, so a token validated inside insightd would need a second copy of that PDP in Python.

#### ⛔⛔ THE TOKEN CHOICE WAS THE LOAD-BEARING DECISION, AND IT AVOIDED A MIGRATION ENTIRELY
The six proxied routes map onto the **existing `purchase` voucher-class token** — GET → `Display purchase`,
POST → `Create purchase`. A draft IS a proposed purchase voucher; the accept posts one through
`POST /vouchers/purchase` moments later.
- Migration **0011 already seeds Data Entry `allow create|alter|display purchase`**, so the F9 Bill Inbox
  works for the level that actually keys supplier bills **with no migration** — and **no new resource
  arrives pre-granted**, so this row owed no ACL counterweight at all (contrast D95 and D94, which exist
  precisely because a new report NAME arrives granted through 0011's coarse `allow display report`).
- ⚠️ **A new `draft` token would have been the obvious answer and the wrong one:** it would arrive
  DENIED for Data Entry (`default_access = 'none'`), and the lane would then have had to write a migration
  granting it — asking a tenant to re-decide a power they had already given that level.
- ⛔ **0077 therefore stays free for D94.**

#### THE FIX, IN THREE PLACES THAT HAD TO LAND TOGETHER
1. **cored** — `insightd_proxy.go`: six routes inside the `{companyID}` subtree, so the same middleware that
   guards the day book guards the drafts; the upstream path is derived from `routeSuffix`, so it cannot
   forward a path chi did not route. The company sent onward is the one **the PDP authorized** (from
   `db.CompanyFromContext`), never `chi.URLParam`.
2. **insightd** — `api/authn.py`: the credential and the company assertion, attached to the drafts
   **router** so a route added later inherits it. `/benchmark/last` takes the credential alone (no company
   to assert); `/healthz` stays anonymous and now states the truth; the auto-docs are unmounted.
3. **the clients** — `api/http.ts` gains `setSessionFetch`, installed once by the auth gate, resolved
   **per request** so construction order cannot matter. That is what lets `createInsightdClient()` and
   `insightdDraftLifecycle()` — both built from module scope, nowhere near the gate — carry the bearer with
   no plumbing of their own. `vite.config.ts`'s `/companies -> insightd` rule is **deleted**, which closes
   the namespace grab as well as the exposure.

#### ⛔⛔ THE ARM THE WHOLE ROW TURNS ON, AND WHY "THE ACCEPT STILL WORKS" IS THE WRONG ORACLE
All four insightd-backed layers of the duplicate-bill guard **fail OPEN on any non-409**: `claimDraftForPost`
warns and returns null, `draftAlreadyAcceptedInsightd` returns null, `releaseClaimAfterFailure` never throws,
`recordAccepted` is fire-and-forget. **A 401 lands in every one.** So shipping insightd's refusal before the
clients could send a credential would have collapsed a five-door guard onto its client-side layers — the
accept would still succeed, and the only evidence would be a `console.warn`. Hence one commit, and hence the
two arms that matter:
- **the CLAIM still reaches insightd (200)**, and `insightd_draft.body.claim_token` was read back **out of
  band from Postgres** rather than believed from the response (§5.10);
- **the 409 still carries `detail.code`** (`draft_being_posted`) **through the proxy.** cored's own errors are
  `{"error": …}`; the proxy passes status, content type and bytes verbatim, and a tidier version of it would
  have turned three specific refusals into the fail-open branch.

#### ⛔ A STRUCTURAL GATE CAUGHT A CLASS I HAD NOT PREDICTED — AND IT WAS RIGHT TO
`TestEveryWriteRouteNamingAnExistingDocumentIsAccountedFor` went red on all three `/drafts/{draftID}/…`
POSTs: a write naming an already-stored document must either have its STORED date judged or be listed in
`writeRoutesNamingADocumentButNotMovingIt` **with the reason**. They are now listed, with it: a draft is not
a ledger document — no `voucher_version`, no posting, no figure in any period — and the one that looks like it
might move something, `accepted`, cannot: it records which voucher a human already posted, and that
`POST /vouchers/purchase` is where the cutoff judged the date. **This is the round-3 enumeration doing exactly
what it was built for**, on a route class nobody had in mind when it was written.

#### ⚠️ AND A SILENT TRUNCATION THAT THE MOVE ITSELF CREATED
`POST .../drafts` carries a **base64 scan** — the Bill Inbox has a file input and does `btoa(binary)` — and
base64 inflates by 4/3, so an ordinary ~800 KB photo of a bill exceeds 1 MiB on the wire. While the browser
posted straight to insightd there was no cap in the way at all; routing it through cored put `drainBody`'s cap
in front of it, and **`drainBody` REPLACES `r.Body` with what it read**. The symptom would have been a 422
from insightd on a body the operator's own file produced. Fixed with `maxDraftUploadBody` (16 MiB) and its
own `bodyCapFor` arm, asserted both ways — this is `body_cap_test.go`'s lesson arriving at a second route.

#### THE PREDICTIONS, WRITTEN BEFORE THE BUILD (§5.3), AND HOW THEY LANDED
- ✅ `test_healthz_DISCLOSES_that_there_is_no_authentication` had to move. It did, renamed, **with the answer
  its own message demanded** — what a token is checked against — in the docstring.
- ⛔ **I predicted the opposite of the driver on one point and was right.** The driver said
  `test_no_ledger_write_endpoint_exists` would go RED and to anticipate it in the commit. It stays **GREEN**,
  because the credential is a route `Depends` and not middleware: routing runs first, so a path insightd does
  not serve still answers 404. That test is the product's proof that insightd has no ledger-write route, and
  401-before-routing would have turned it into a statement about headers. It now drives the path **both**
  credentialed and anonymous, and says why.
- ✅ `test_get_draft_other_company_is_404` stays green, re-expressed: the harness stands in for the proxy and
  asserts the company header from the request PATH, so `OTHER-CO` still 404s — and the property is now about
  the STORE's scope, which is what it always meant.
- ✅ The back-dated cutoff reaches `POST /drafts` through the generic peek and is inert: **zero** `voucher_date`
  occurrences across the whole OCR corpus, and `CreateDraftRequest` has no such field.
- ⚠️ **Unpredicted:** the stored-date gate above, and `test_hitl_invariant`'s write-client grep firing on my own
  comments — they cited the test's name, which contains the forbidden token `ledger_write`. **The guard was
  left alone and the prose was rephrased**; widening an allowlist to silence a failure is what that file
  forbids, and the token is now avoided deliberately, with a note saying why.

#### THE DRIVE — `frontend/scripts/drive-l20-d49.mjs`, **52/52**
Two tenants it mints and reaps (the D91 pattern), plus a **read-only** browser arm on the demo tenant so it
cannot move `verify:demo`. Every refusal asserts the **payload** as well as the status — a 401 that still
shipped the body would be the same leak with a different number on it. The arms worth naming:
- six anonymous draft routes → 401, each with the supplier name **absent** from the body;
- `/benchmark/last` → 401 anonymous **and 200 with the credential** (without that second half the first
  proves nothing about the credential);
- the four auto-docs → 404;
- ⛔ **a caller who HOLDS the service credential but names a company cored did not authorize → 403.** Holding
  the credential is not holding a tenant, and this is the arm that retires *"caller-supplied company_id in
  the path"*;
- through cored: anonymous → 401, **B's owner lists B's drafts → 200 with the supplier name present** (the
  control, without which every refusal is satisfied by a bricked-up door), and **A's bearer naming B → 403
  with B's supplier name absent from a 41-byte body** — the string in an A-scoped response was always the
  defect, not the status code;
- ⛔ `disallow display purchase` **hides the inbox** while the same clerk still reads the Trial Balance, and
  `disallow create purchase` **leaves the inbox readable and refuses the claim, with nothing reserved.** That
  pair is what proves the token choice was understood rather than merely chosen: a mapping that lumped all six
  routes onto one action could never pass it;
- browser: the Bill Inbox opens, the LIST went to **`/v1/companies/{co}/drafts` 200**, **nothing** addressed
  insightd's own prefix, and the row **rendered** (`Pune Fabricators · 21240.0000 · ready · 95%`). A 200 on an
  empty array would have satisfied the arm above it;
- ⚠️ **the reap deletes `insightd_draft` too.** Its `company_id` is TEXT with no FK, so nothing in the schema
  would ever have reported the rows left behind — they would simply accumulate under a company that no longer
  exists, which is exactly how 61 stray `co-1` drafts were once found.

#### ⚠️ ONE MORE PROBE THE MOVE WOULD HAVE BROKEN — FOUND BY GREPPING, NOT BY RUNNING
`drive-l11-ai.mjs:119` listed drafts with a bare `fetch("http://localhost:8088/companies/…/drafts")` and **no
credential at all** — which worked precisely because insightd authenticated nobody. It now goes through cored
with the bearer it already mints. Left alone it would have thrown `cannot list insightd drafts: HTTP 401`, and
the next session would have read D16/D18/D19 as broken. §5.15 says re-run the whole set; **grep the set too.**

#### GATES
`pnpm vitest run` **1450 / 87 files** (+7: four client arms for the session transport — including the
ORDERING one a mutation bought — and the path pins in `draftAccept.test.ts` **inverted rather than
deleted**, §5.16) · `pnpm exec tsc --noEmit` clean · insightd pytest **161 passed** (was 154; **+7**, all
refusal arms) · `go test ./... -count=1` **35 ok / 0 FAIL / 0 SKIP, exit 0**, bracketed with a live `psql`
· `pnpm contract:cored` **14/14**.

**THE WHOLE DRIVE SET, ON A FRESH SEED, AFTER THE LANE:** `verify:demo` **34/34** → l13 **33/33** ·
l14 **17/17** · l14b **8/8** · l15-d5 **22/22** · l15c **7/7** · l15d **13/13** · l15f **8/8** ·
l16-d22 **34/34** · l11-ai **14/14** · l17-d95 **20/20** · **l20-d49 52/52** → `verify:demo` **34/34
again**. ✅ **The set is still gate-safe with the new probe in it** (§4/§5.15) — l20 mints and reaps its own
two tenants and its browser arm never presses Ctrl+A.
⚠️ **AND drive-l16-d22 STILL FINISHES WITHOUT EXITING** (§4's warning, re-confirmed): it printed 34/34 with
zero `Chrome for Testing` processes alive and then held the pipe. ⛔ **Running the set as
`node … | tail -4` therefore LOSES that summary** — `tail` emits nothing until EOF, so the sequence stalls
and killing the node process discards the very line you were waiting for. Write each drive's output to its
own FILE and judge it there.
⚠️ **AND A GATE HAZARD WORTH KNOWING:** a `pnpm vitest run` with **9 failing tests** was reported by the
harness as **exit code 0**. Judge vitest by its printed `Test Files` line, never by the exit status of the
wrapper that ran it.

---

### L21 — 2026-08-24 · D85 CLOSED: the redaction tag was a CONSTANT, so a supplier's SECOND bill was refused

**Position 1 of `HANDOFF-THE-REMAINING-SIX.md`**, unblocked by L20 landing the final transport. One row.
The transport warning in that driver's header turned out not to bind at all — see below.

#### ⛔⛔ THE ROW, MEASURED THROUGH THE REAL MAPPER BEFORE A LINE MOVED
```
invoice_no "ABCDE1234F" -> voucher_no '[PAN REDACTED]'
invoice_no "ZZZZZ9999Z" -> voucher_no '[PAN REDACTED]'   COLLAPSE: True
```
`assertNotAlreadyOnTheBooks` asks the DAY BOOK, and the day book stores nothing but `voucher_no`. Two
bills from one supplier on one date whose numbers were different PANs were one bill to that check.
✅ **AND THE COLLAPSE IS NARROWER THAN THE REGISTER SAYS, EXACTLY AS THE RECON WARNED.** `scrub()` replaces
only the MATCHED SPAN, so `INV/ABCDE1234F/77` and `INV/ZZZZZ9999Z/88` never collided. Total collapse needs
the whole invoice number to BE the entity — which is what the L11 fixture carries and is why this was found
by driving. ⛔ **A probe built on the wrapped form passes vacuously in both worlds.**

#### ⛔⛔ THE FIX IS SMALLER THAN THREE HANDOFFS PREDICTED, AND THE PREDICTED SHAPE COULD NOT HAVE WORKED
The driver and `HANDOFF-RECON-LAST-16.md` §3.11 both called for a hash carried as a **new draft-body
field**, travelling through insightd's `VoucherDraftBody` (`extra="forbid"`), cored's transparent proxy and
a mirrored `= None` default in `frontend/src/api/draft.ts`. ⛔ **The day book has no field to carry one**, so
a draft-side hash closes nothing; and `scrub()` already holds the raw `m.value` at the point it writes the
tag. So the whole change is `placeholder(entity_type, value)` returning
`[{TYPE} REDACTED {fingerprint(value)}]`. **No new field, no wire-shape change, no contract to mirror, and
nothing anywhere near cored's byte-for-byte passthrough of insightd's 409** — the header's ⛔⛔ warning was
about a change this row did not need to make.

#### ⛔ UNSALTED IS LOAD-BEARING, AND IT IS THE OPPOSITE CHOICE FROM `boundary.py`
`boundary.py` salts per REQUEST (`secrets.token_hex(8)`) so co-reference tags are unlinkable across
documents. The persistence path REQUIRES linkability: the same bill re-uploaded must redact to the same
`voucher_no` or layer 2 stops recognising the duplicate and the money defect it exists to stop comes back.
⛔ **AND THE WIDTH IS A PRIVACY CONTROL, NOT A COLLISION BUDGET.** Four hex characters leave ~10,700 issued
PANs per fingerprint; widening it helps a brute-force of the removed value back out of append-only books.
⚠️ **The residual is written into the module docstring rather than hidden:** for an entity whose REAL domain
is small — an IFSC, ~1.7e5 live branch codes — a fingerprint of any usable width narrows it to a couple of
candidates, so such a value is protected by the REMOVAL of the value and not by the fingerprint.

#### ⚠️⚠️ "IT MOVES PINNED NUMBERS" WAS FALSE, IN BOTH DIRECTIONS — A FOURTH §5.24 CASE
Four handoffs said D85 moves `test_pii_persistence.py`'s 4/6 redaction counts and the F9 panel text, each
derived from the last. **It moves neither.** The fingerprint changes the tag STRING, not the COUNT of
matches, and every pre-existing assertion on the tag is a substring or a prefix (`"PAN" in`,
`startswith("INV/")`, `endswith("/77")`, `"AADHAAR REDACTED" in`). The insightd suite was **161 passed
before and 161 passed after, with not one test moved** — and M1 confirmed the other direction too: with the
fingerprint removed again, the count assertions still did not move.
⛔ **SO THERE WAS NOTHING THAT WOULD HAVE NOTICED A REVERT, AND THE GATE HAD TO BE WRITTEN.** §5.17 says a
green gate means "check the binary was rebuilt before doubting the probe"; here there is no binary, the
structural reason is plain, and the honest conclusion is that the prediction was wrong. **Budget for writing
the gate, not for re-pinning one.**

#### ⛔⛔ THE MUTATION PROOFS CORRECTED THE TESTS, NOT THE CODE — TWICE, IN OPPOSITE DIRECTIONS (§5.3)
- **M1 (drop the fingerprint).** Predicted 1 failure; observed **2 of 166**. The arm I had labelled a
  REGRESSION GUARD — "the tag still says WHAT was removed in words" — is not one: its
  `startswith("INV/[PAN REDACTED ")` includes the SPACE, so it pins the fingerprint's presence too.
- **M2 (salt it per process — i.e. copy `boundary.py`).** Predicted the determinism arm would fail;
  observed **1 failure, and not that one.** ⛔⛔ **THE ARM WRITTEN FOR EXACTLY THIS MUTATION PASSED THROUGH
  IT.** Both `_draft()` calls ran in ONE pytest process, so a per-process salt agreed with itself. The only
  thing that caught M2 was `test_the_fingerprint_is_SHORT_and_unsalted`, which recomputes the digest **out of
  band** — §5.10 applied to a hash instead of a database column. **The property is cross-process, because the
  day book is asked on a LATER request than the one that redacted**, and the arm now spawns two fresh
  interpreters. Re-run under M2: the predicted 2 failures, with the discrimination oracle still GREEN — which
  is what makes a salted fingerprint the wrong fix that a weaker gate ships.
- **M3 (give the fingerprint sentence unconditionally).** 1 of 60 in `draftAccept.test.ts`, exactly as
  predicted: the ORDINARY-number arm. A blanket rewrite would tell an operator holding "INV-9001" about a
  fingerprint they do not have.

#### ⛔ THE ADVICE CHANGED WITH THE KEY — THE OTHER HALF OF THE ROW (§5.19)
Three refusal sentences advised remedies their own key cannot deliver:
- the **day-book** refusal said *"key its own supplier invoice number"* about a number that had been
  REDACTED — a dead end, since every number keyed landed on the same token. It now says the tag ends in a
  fingerprint of the removed number, so this is the SAME supplier number and a genuinely different bill would
  not have been stopped. ⚠️ **The ordinary-number sentence is UNCHANGED and an arm pins that.**
- the **session and durable memos** key on `draftAcceptKey` — the DRAFT id when the caller passes one, the
  body's CONTENT when it does not — and both gave the content remedy, i.e. the wrong one exactly when the
  caller is most precise. One `secondBillAdvice()` names the key it actually refused on rather than a third
  copy of the sentence. ⚠️ **Both branches are driven; the live F9 path passes no `draftId`.**

#### THE DRIVE — 16/16, WIRE AND BROWSER, ON A FRESH SEED
`frontend/scripts/drive-l21-d85.mjs`. Both halves, because half one alone is satisfied by deleting the guard:
two different bare-PAN numbers BOTH post (`[PAN REDACTED 6442]` / `[PAN REDACTED 0413]`, two purchases, each
with its own `C:Steel Suppliers` leg and its own `D:Input CGST`/`D:Input SGST` debits — sides and NAMES,
never totals); the SAME number is still refused, posts nothing, and a **second Ctrl+A** refuses again (§5.8);
and the refusal's ADVICE is asserted, not its presence.
⛔ **THE CONTROL BILL DIFFERS IN AMOUNT ON PURPOSE.** The five layers are ORDERED: a byte-identical
re-upload is refused by the CONTENT-keyed memo before the day book is ever asked. Same party, same date, same
tag, **different amount** is the only shape that reaches layer 2 — a probe that re-uploads an identical bill
measures layer 1 and reports on layer 2.
⛔ **FOOTED AGAINST A HANDLER THAT IS NOT THE DAY BOOK (§2.2):** Outstandings reads `bill_allocation` and
names each bill `Purchase/<fy>/<voucher_no>`. Both tags appear, every generated ref name is distinct, and the
count of refs carrying a `(xxxxxxxx)` discriminating suffix is **0**.

#### ⛔⛔ A SECOND DEFECT'S WORTH OF EVIDENCE, FOR D83 — FOUND BY DRIVING, NOT REGISTERED HERE
cored mints a generated bill reference as `Purchase/<fy>/<voucher_no>`, so while the tag was a CONSTANT
**every PII-numbered bill from one supplier competed for ONE ref-name namespace**, and
`resolveGeneratedBillRefNames` renamed the loser rather than failing. So the collapse never surfaced as an
error there — it surfaced as a supplier's bills wearing suffixes nobody typed. A pre-fix tenant carried
`Purchase/2026-27/[PAN REDACTED]` beside `… [PAN REDACTED 6442] (583827a8)`. ⛔ **That is D83's namespace and
D83 is still OPEN** — this row removes one large source of collisions in it and changes nothing about the
concurrent race.

#### ⚠️ THREE PROBE BUGS, AND ONE OF THEM IS §5.7 VERBATIM
- `pages: 1` — `_normalized_from_fixture` **iterates** `input.pages`, so this raised `'int' object is not
  iterable` inside insightd and cored answered **500**.
- `classify: {supplier: {ledger_id}}` — the fake classifier reads the FLAT `supplier_ledger_id`. Nested is
  silently NO MATCH: no supplier state, zero input-tax legs, and cored refuses with
  *"CGST posting 0.0000 != derived 90.0000"* — which reads exactly like **D18/D19 having regressed**.
- ⛔⛔ **`GET /drafts` CARRIES NO `body` AT ALL** — it returns summaries (`draft_id · party · grand_total ·
  status · confidence · accepted_voucher_no`). Reading `draft.body.voucher_no` off one gave `undefined`,
  defaulted to `""`, **and the determinism arm then compared `"" === ""` and reported PASS.** §5.7 verbatim: an
  absent projection COMPARED instead of THROWN on. `voucherNoOf()` goes through `GET /drafts/{id}` and THROWS.

#### ⚠️ AND THE ONE UNEXPLAINED 403 IS EXPLAINED AND ASSERTED, NOT FILTERED OUT
Every run logged one `POST /drafts 403` beside its 201s. Its body is `{"code":"STEPUP_REQUIRED"}`: the
session signs in with a PASSWORD and the first mutating write demands a WebAuthn step-up, which the client
performs and retries — the product working. ⛔ **An unexplained refusal on the route a lane is driving must
never be waved through as noise**, so the probe reads the body and pins that this is the ONLY refusal there.

#### GATES
insightd pytest **166 passed** (was 161; **+5**, one of them the only arm red on the previous commit) ·
`pnpm vitest run` **1454 / 87 files** (+4 in `draftAccept.test.ts`) · `pnpm exec tsc --noEmit` clean ·
`go test ./... -count=1` **exit 0, 0 FAIL, 0 SKIP**, bracketed with a live `psql` (this row touches no Go).

**THE WHOLE DRIVE SET, ON A FRESH SEED, AFTER THE LANE:** `verify:demo` **34/34** → l13 **33/33** ·
l14 **17/17** · l14b **8/8** · l15-d5 **22/22** · l15c **7/7** · l15d **13/13** · l15f **8/8** ·
l16-d22 **34/34** · l11-ai **14/14** · l20-d49 **52/52** · **l21-d85 16/16** · l17-d95 **20/20** ·
`contract:cored` **14/14** → `verify:demo` **34/34 again**.
✅ **The set is still gate-safe with the new probe in it** (§4/§5.15) — and l21 is the first probe in the set
that POSTS into the demo tenant besides l11-ai, so that was the check that mattered: two purchases per run,
each with its own bill reference via F9 and dated on or before today, leave BS Sundry Creditors and
Outstandings (payable) in agreement.
⚠️ **AND L20's WARNING ABOUT `| tail` WAS EARNED AGAIN, THE HARD WAY.** The first attempt at the set ran each
drive as `node … | tail -4`; it reached `drive-l16-d22`, which printed **34/34** and then held on with **zero**
`Chrome for Testing` processes alive, and the sequence stalled. ⛔ **Each drive to its own FILE is necessary
but NOT sufficient** — with the pipe gone the *process* still does not exit, so the runner blocks on it
anyway. The working shape is: redirect to a file, and **judge the printed summary in that file, then kill the
pid** (⛔ by PID — `pkill -f <script>` also matches the wrapper, §4). l11-ai, l20 and l21 all needed the same
treatment.
⚠️ **A MID-SEQUENCE KILL IS A RESEED.** `drive-l14-cheap` was killed part-way through the first attempt; the
tenant was re-seeded before the real run rather than trusting that a half-finished probe left nothing behind.

---

### L22 — 2026-08-24 · D8 CLOSED: closing stock replayed in LEXICAL number order, not entry order

**Position 1 of `HANDOFF-THE-REMAINING-SIX.md` after L21.** One row, ALONE, and the only row in the wave
that re-prices books an operator has already read.

#### ⛔⛔ THE DEFECT, AND WHY THE KEY WAS NEVER GOING TO BE A NUMBER
`inventory.less()` ordered movements by (voucher_date, **voucher_no**, version_created_at, line_no,
voucher_version_id). A `voucher_no` is TEXT from a counter that is **per voucher type** — and on a purchase
it is the *supplier's* own reference — so it carries no ordering relationship to a number from another
series. cored mints bare integers per type, so on one date a delivery note numbered `2` sorts before a
receipt numbered `4`; and within ONE series `10` sorts before `9`. Weighted average is order-dependent, so
an issue keyed AFTER the receipt that funds it was costed at the **pre-receipt** average.
`voucher_id` is a UUIDv7 minted at CREATE (`ledger.go`'s `CreateVoucher`), so it is entry order per company
across every series, and an ALTER keeps it. ⚠️ **BOTH OBVIOUS ALTERNATIVES ARE WRONG AND THE CODE NOW SAYS
SO:** `version_created_at` is the CURRENT version's stamp, so an ALTER moves it and batched posts inside one
transaction share `now()` and tie; `voucher_version_id` is already a UUIDv7 but is RE-MINTED by an ALTER.
Both stay, BELOW `voucher_id`, so the order remains total.

#### ✅ THE COST MODEL WAS CHECKED BEFORE PRODUCTION CODE MOVED, AND IT HELD
The driver's cheapest check, run exactly as written — add ONLY the new fixture:
`corpora/validate.py` → **3 failures, all on `07-same-date-cross-series`** (outwardValue 750 vs 500,
closingValue 2250 vs 2500, closingRate 150 vs 166.6667); `go test ./... -count=1` → **exactly one** failure,
`TestWeightedAverageCorpus/07-same-date-cross-series`, **0 SKIP**. Nothing else in the backend suite.
✅ **And the REASON was verified rather than assumed:** grouping every existing fixture's movements by
(date, item, godown, batch) shows **not one** of the six has two voucher numbers on one date on one
position, so none of their expected values could move.

#### ⛔⛔ M2 DID NOT REPRODUCE, AND THE NON-REPRODUCTION BOUGHT THE ONLY GATE THAT MATTERS (§5.4)
M2 deleted `r.movement.VoucherID = voucherID.String()` from the scan, leaving `less()` fixed. Predicted: Go
green, wire figure back to the defect. Observed: **Go green AND the wire figure still CORRECT.** Measured
reason: with every `VoucherID` empty the order falls through to `version_created_at`, and the seed posts each
voucher in its own transaction —
```
RN "4"  created_at 20:30:21.722064
DN "2"  created_at 20:30:21.725537
```
— so the tiebreak happens to AGREE with entry order.
⛔ **SO THE WIRE ORACLE ALONE DOES NOT PROVE `voucher_id` IS DOING THE WORK**, and nothing in the repo
proved `queryStockMovements` populated the ordering key at all. **That is D90's shape exactly**: an
application key shadowed by a tiebreak, invisible to every gate.
✅ **THE GATE:** `TestReplayOrdersByENTRY_NotByVoucherNumber` (`internal/reporting`, through the real SQL).
A same-day pair whose NUMBER order is the wrong order, then — the arm M2 fails — **RESTATE THE RECEIPT WITH
IDENTICAL LINES.** An ALTER keeps `voucher_id` and re-mints BOTH `created_at` and `voucher_version.id`, so
the receipt becomes the newest version while staying the earlier voucher; after that `voucher_id` is the ONLY
key that still orders it first, and restating a voucher without changing a line must not re-price anything.

#### ⚠️ AND THE MUTATION CORRECTED THE TEST — THE THIRD TIME IN TWO LANES (§5.3)
The first draft of that gate numbered the receipt **10** and the issue **2**. `"10" < "2"`, so voucher_no
order put the receipt FIRST — the CORRECT order — and **M1 passed straight through a test written to catch
it.** The issue must carry the number that sorts EARLIER: receipt 9, issue 10. ⛔ **A test whose fixture
accidentally agrees with the defect is worse than no test, and only running the mutation showed it.**
⚠️ It also needed `postNo`/`alterNo`: the existing helpers let the SERVER allocate the number, and on a
single voucher class the server allocates in ENTRY order — so through them the numbers *cannot* disagree
with entry order, which is the disagreement under test.

#### THE ORACLE — PREDICTED FIRST, THEN READ (WIRE AND BROWSER)
The seed's own movements were replayed independently in Python (a THIRD implementation) before the fix was
built; that replay reproduced the PRE-fix figures exactly, which is what licensed it as a prediction.
```
                        PRE-FIX      POST-FIX     predicted
MS Angle closing_value  8500.8152 -> 8419.9091    8419.9091
MS Angle outward_value  2359.1848 -> 2440.0909    2440.0909
MS Angle closing_rate     41.4674 ->   41.0727      41.0727
MS Angle closing_qty     205.0000 ->  205.0000    UNCHANGED
BS Closing Stock       75175.8152 -> 75094.9091   -80.9061
```
⛔ **THE QUANTITY IS UNCHANGED WHILE THE VALUE MOVED** — the D8 signature, and the drive asserts it FIRST.
Both binaries were verified fresh BY SYMBOL (the pre-fix one counted **0** occurrences of the new SQL, the
post-fix one **1**).
⛔ **FOOTED ACROSS HANDLERS:** `/balance-sheet`'s Closing Stock node == `/stock-summary`'s total, and
`balanced: true` still holds. **And the defect's own figures are asserted ABSENT by value**, so a revert
reads as the defect by name.

#### ⚠️ THE BROWSER ARMS FAILED FIRST, AND IT IS §5.7 A THIRD TIME
The routes answer fixed-4 (`8419.9091`); the screen renders Indian-grouped, 2 dp, **TRUNCATED**
(`8,419.90`, *not* `8,419.91`). Both the wanted AND the defect string were absent from the page text, so all
three browser arms went red while every wire arm was green — a probe comparing a projection the DOM does not
carry. The display strings are now MEASURED off the rendered page rather than re-derived in the probe (a
second copy of the formatter would drift and would agree with itself), and **both** plausible renderings of
the defect are asserted absent so the arm cannot go quiet if the rounding mode changes.

#### ⛔ A PRECONDITION ARM, BECAUSE THE WHOLE ORACLE IS OTHERWISE VACUOUS (§5.5)
Nothing in the seed put two differently-numbered vouchers on one position on one date, so the entire
replay-ordering rule shipped with **no demo figure able to notice it being wrong**. `seed-demo.mts` grows the
pair, and `drive-l22-d8.mjs`'s first arm reads it off the wire and asserts the two orders DISAGREE — on a
stale seed that arm goes red and says "reseed" instead of thirteen green arms measuring the only figure there
is.

#### ⛔ IT SHIPS WITH A RELEASE NOTE, AND THE REPO HAD NOWHERE TO PUT ONE
`docs/RELEASE-NOTES.md` is created, with a one-line rule for what belongs in it (a change that moves a
number an operator has already read — not merely a large change) and the D8 entry: which reports move, that
QUANTITIES do not, that nothing is migrated (so an older figure can no longer be reproduced from the data),
and that **T9 FIFO/LIFO must be built on this ordering** because FIFO is *more* order-sensitive.

#### GATES
`go test ./... -count=1` **exit 0, 0 FAIL, 0 SKIP** (bracketed with a live `psql`) ·
`python3 corpora/validate.py` **OK, inventory=7** · `pnpm vitest run` **1454 / 87 files** ·
`pnpm exec tsc --noEmit` clean.

**THE WHOLE DRIVE SET, ON A FRESH SEED, AFTER THE LANE:** `verify:demo` **34/34** → l13 **33/33** ·
l14 **17/17** · l14b **8/8** · l15-d5 **22/22** · l15c **7/7** · l15d **13/13** · l15f **8/8** ·
l16-d22 **34/34** · l11-ai **14/14** · l20-d49 **52/52** · l21-d85 **16/16** · **l22-d8 14/14** ·
l17-d95 **20/20** · `contract:cored` **14/14** → `verify:demo` **34/34 again**.
✅ **Gate-safe with the new probe in it, and that mattered here:** D8 moves money that `verify:demo` foots,
and the seed grew a voucher pair. l22 is READ-ONLY (no posts, no masters), so it cannot shift the tenant.

---

### L23 — 2026-08-24 · D86 CLOSED: the ageing bounded an UNASKED date at today; the Balance Sheet did not

**Position 1 of `HANDOFF-THE-REMAINING-SIX.md` after L22.** ALONE, and **before D94**, which is now the
only report-screen row left.

#### ⛔⛔ THE DEFECT WAS THE DEFAULT, NOT THE PREDICATE
Outstandings is the BILL-WISE BREAKUP of a ledger balance, and a ledger balance includes every posted
voucher whatever its date. `resolveAsOn` defaults an empty as-on to today — correct for AGEING, because an
ageing report with no reference date answers nothing — and that same default was feeding the INCLUSION
predicate. So the moment a bill was dated ahead the breakup explained LESS than the balance it broke up:
driven, Balance Sheet 109950.0000 against Outstandings 108770.0000, each footing, nothing to reconcile them.
✅ **DECIDED: OUTSTANDINGS MOVES, NOT THE BALANCE SHEET.** The sheet's figure is byte-identical.

#### ⛔⛔ THE CHEAPEST CHECK (§2.10) FOUND THE SHAPE OF THE LANE BEFORE ANY GATE WAS WRITTEN
With ONLY the report-side change made:
```
go test ./internal/reporting/  -> ENTIRELY GREEN
go test ./internal/query/      -> exactly 3 failures, all Outstandings parity
```
⛔ **SO `internal/reporting` HAD NO TEST ON THE UNBOUNDED DEFAULT AT ALL.** Every assertion in
`outstanding_as_on_test.go` was about a date that had been ASKED for; the answer nobody asked for was the
one that was wrong. That is why the row existed and why the gate had to be WRITTEN, not re-pinned.
✅ **AND ONE OF THE THREE FAILURES WAS A TRIPWIRE AN EARLIER LANE PLANTED FOR THIS EXACT DAY:**
*"the REPORT itself now includes INV-FUT — reporting/outstanding.go lost its as-on bound and this test is
measuring the wrong thing."* It fired on purpose. **Leave tripwires like that; they pay.**

#### THE THREE PLACES THAT MOVED TOGETHER — MISSING ANY ONE RE-OPENS D20 VERBATIM
1. **the report** — a `bounded` flag plus `Outstanding.Bounded` on the struct so no reader re-derives it.
   ⚠️ **The ARG LIST moves with the predicate:** leaving `$2` bound while dropping it from the text answers
   *"bind message supplies 2 parameters, but prepared statement requires 1"* — a 500, not a wrong number.
2. **the NLQ skeleton** — both predicates dropped together, including the opening branch's
   `books_beginning_from` guard, which asks "had the books begun by the as-on date?", a question with no
   meaning when there is no as-on date. **Decided, not dropped:** the opening breakup IS part of these books
   and the ledger balance always includes it. The old constant's TIME-ZONE reasoning is kept verbatim
   (unreferenced, renamed) for the day an `as_on` parameter reaches the DSL.
3. **the caption, in FOUR copies** — `QueryScreen`'s basis line (which said *"AS AT TODAY … Bills dated in
   the future are not outstanding yet and are excluded"*, true when written and made FALSE by this commit),
   the wire echo, the screen's scope line, and the printed subtitle.

#### ⛔⛔ M3 IS THE ONE TO REMEMBER: THE FALSE CAPTION HAD NO GATE ANYWHERE
Reverting the wire echo to the RESOLVED as-on left `go test ./... -count=1` at **exit 0, 0 FAIL**. Every
figure right, the sentence over them wrong, and nothing in the repo read the sentence. §5.19 in its purest
form, and §2.9 for the second lane running. It bought `TestTheAgeingReportsScopeOnTheWireIsHONEST`, which
checks the caption **against the rows it captions** (the unbounded total equals Sundry Debtors from the
Balance Sheet) rather than asserting a field in isolation.
⛔ **THE CONVENTION WAS COPIED, NOT INVENTED:** `as_on: ""` means UNBOUNDED, exactly as `/stock-summary`
already answers, and `outstandingsScopeLine` mirrors `stockSummaryScopeLine`. ⚠️ `aged_as_on` is always
populated, because the ageing day is a real and DIFFERENT fact — one field could not say both things, and
trying to was the defect.
⚠️ **AND THE OLD CLIENT CAPTION WOULD HAVE VANISHED, NOT LIED:** `data.as_on ? … : null` renders NOTHING on
`""`. Quieter, no more honest — an operator cannot tell an unbounded report from a caption that failed.

#### M1 AND M2, BOTH PREDICTED, BOTH REPRODUCING
- **M1** (the unconditional bound restored) → **4** failures, one more than predicted: the two inverted
  parity tests, the new reporting gate, and — unpredicted — `TestExecute_OutstandingsMatchesTheOutstandings
  Report`. ✅ **The D20 gate catches the REPORT diverging from the DSL as readily as the reverse**, which is
  more than it was credited with.
- **M2** (the whitelist predicate restored alone) → 3 failures, all in `internal/query`, `internal/reporting`
  green. Exactly the D20 family.

#### TWO GO TESTS INVERTED, NOT DELETED (§5.16)
`TestExecute_OutstandingsExcludesTheFutureDatedBill` → `…INCLUDES…`, and
`…DropsTheOpeningWhenBooksBeginLater` → `…KeepsTheOpeningWhenUnboundedAndDropsItAsAtAnEarlierDate`. Both
keep their original reasoning verbatim — it is still exactly right **for a report that was asked for a
date** — and both gained an EXPLICIT-`as_on` arm, which is what keeps the guards alive and stops the fix
reading as "the bound was deleted".

#### ⛔ THE MOCK WAS LOOSER THAN CORED IN BOTH DIRECTIONS (D84's class)
Its bill loop had NO date predicate at all — already unbounded — while it echoed today as `as_on`. Every
vitest was green either way. It now applies the bound when one is given and echoes `""` when one is not.

#### ✅ `BillAllocationPicker` IS A DECISION NOW, NOT AN OMISSION
It passes no `as_on`, so it lists future-dated open bills — and it should: a customer paying an advance or
proforma invoice early is ordinary, and excluding it makes an allocable bill unallocable and pushes the
operator onto ON ACCOUNT, the balance nobody can chase. Recorded in the code.

#### THE DRIVE — 16/16, AND THE END-TO-END ARM IS THE PROOF
`frontend/scripts/drive-l23-d86.mjs`. Two reports from two TABLES agreeing with no `as_on`
(111130.0000 == 111130.0000), grown by exactly the new bill; the future bill NAMED in the list; the
explicit-`as_on` control; both wire fields; the screen's caption and the NLQ basis line each checked for the
new claim AND the absence of the old.
⛔ **fresh seed → verify:demo 34/34 → the probe posts a future-dated bill → verify:demo 34/34 AGAIN.**
`verify:demo` asserts "BS Sundry Creditors == Outstandings (payable)" EXACTLY, and one future-dated bill
used to break it by its own amount. That is the row, proved by a gate that already existed.

#### ⚠️⚠️ TWO PROBE BUGS, AND BOTH ARE FINDINGS RATHER THAN SLIPS
- ⛔ **THE OUTSTANDINGS SCREEN CANNOT SHOW A PAYABLE AT ALL.** `OutstandingsScreen` sends no `kind`, so the
  only ageing reachable from the keyboard is the RECEIVABLE one. The first version of the browser arm
  asserted the payable's ref was on screen; it never can be. **That is D94, still OPEN**, and the probe now
  posts a future-dated SALES invoice too so the browser arm has a receivable to find.
- ⛔⛔ **THE FUTURE DATE IS LOAD-BEARING AND 2027-02-20 BROKE A GATE.** `verify-demo.mts` asserts a Profit &
  Loss over **2027-02-01..2027-02-28** is EMPTY; two future-dated vouchers landed inside it and the gate went
  33/34 with `Feb-2027 net_profit -1000.0000` — a failure with nothing to do with D86 that the next session
  reads as a regression in their own lane. **2027-03-10** is after today, inside the demo FY (so the same
  numbering counter), and outside every window verify-demo probes. ⛔ **Read `verify-demo.mts` before dating
  a probe that posts.**
- ⚠️ And the control arm was RED ON CORRECT CODE first: it asserted `unbounded - bounded === 7080`, true only
  on a tenant no earlier run had touched. §5.23's second question applied to a SHARED tenant. It now accounts
  for the gap BY NAME — the refs the bound removes, summed.

#### ⛔ IT SHIPS WITH A RELEASE NOTE
D86 moves a number an operator has already read, so by `docs/RELEASE-NOTES.md`'s own rule it owes one.

#### GATES
`go test ./... -count=1` **exit 0, 0 FAIL, 0 SKIP** · `python3 corpora/validate.py` OK ·
`pnpm vitest run` **1457 / 87 files** · `pnpm exec tsc --noEmit` clean — and tsc named all four response
fixtures needing `aged_as_on`.

**THE WHOLE DRIVE SET, ON A FRESH SEED, AFTER THE LANE:** `verify:demo` **34/34** → l5-nlq **41/41** ·
l13 **33/33** · l14 **17/17** · l14b **8/8** · l15-d5 **22/22** · l15c **7/7** · l15d **13/13** ·
l15f **8/8** · l16-d22 **34/34** · l11-ai **14/14** · l20-d49 **52/52** · l21-d85 **16/16** ·
l22-d8 **14/14** · **l23-d86 16/16** · l17-d95 **20/20** · `contract:cored` **14/14** →
`verify:demo` **34/34 again**.
✅ **`drive-l5-nlq` IS IN THE SET NOW AND WAS NOT BEFORE**, which matters for this row: it is the probe that
carries the NLQ basis-line pin D86 re-wrote, and running it was how that re-pin was proved rather than
assumed. ⛔ **Add it to the standard sequence** — 41 probes on the surface D20 lives on had been outside the
routine re-run.
✅ **The set is gate-safe with the two new probes in it**, and for D86 that is the row's own proof: l23 POSTS
two future-dated bills and `verify:demo` still foots "BS Sundry Creditors == Outstandings (payable)" exactly.

---

### L24 — 2026-08-24 · D94 CLOSED: the payables ageing had no door, and no rule could name it

**Position 1 after L23.** ⛔ **AND IT REGISTERED A NEW ROW, D97** — the first new row in seven lanes, found
by driving this row's own oracle.

#### ⛔⛔ THE ROW WAS NEVER "ADD A SCREEN"
cored has always served the creditor ageing as `GET /outstandings?kind=PAYABLE`; the client simply never
sent `kind`. But a `?kind=` toggle on the existing screen would have shipped a door and left the defect:
`routeAction` maps the **FLAT LITERAL PATH** to the PDP resource, so a query parameter never reaches the
decision, and *"this level may see what customers owe us but not what we owe suppliers"* was a rule nobody
could WRITE DOWN. Two names, never one report with a direction toggle — the reasoning `REPORT_NAMES` and
migration 0073 already carry for the two ORDER BOOKS.

#### ✅ ONE WRITER, TWO DOORS — ON ALL THREE SIDES
cored's `writeOutstandings`, the client's `OutstandingsLikeScreen`, the mock's `outstandingsOf`. The two
reports cannot drift (D20's failure) while the two RULES stay separate (D94's). ⛔ **The only thing that
differs is which route is called**, which is why the client passes a FETCH FUNCTION rather than a `kind`
flag — a flag would put both screens back on one route and one rule.
⚠️ **AND THE MOCK HAD TO STOP DELEGATING THROUGH `this`.** `getBillsPayable` calling
`this.getOutstandings` meant a test spying on the receivables method also observed — and could stub — the
payables one: a mock COUPLING two surfaces the server keeps independent, aimed at the very split this row
makes. Caught by the arm below, not by inspection.

#### ⛔⛔ THE ARM THAT IS THE ROW, AND THE ONE THAT CATCHES THE INVISIBLE WRONG BUILD
- **On the wire, driven:** a level with `disallow display report:bills-payable` gets **403 "disallowed by
  rule"** on `/bills-payable` and **200 with a party row** on `/outstandings`. Refused the payables book
  while allowed the receivables one. ⛔ **A `?kind=` toggle could never pass that**, which is the whole
  reason the row exists. ⚠️ The CONTROLS come first — member, reads the receivables ageing, and that report
  has an actual ROW in it — because a 403 on its own proves nothing.
- **In vitest, M1:** point `BillsPayableScreen` at `getOutstandings(co, {kind:"PAYABLE"})`. The screen
  renders **IDENTICALLY** — same parties, same bills, same totals — and exactly ONE test fails: *"calls
  getBillsPayable (its own ROUTE), never getOutstandings with a kind"*. ⛔ **The wrong build is invisible to
  every rendering assertion**, and that one arm is the entire difference between a door and a rule.

#### ⛔ THE ALIAS THAT LIED, AND IT HAD SHIPPED
`REPORT_ALIASES` mapped `"bills payable"` → **"Outstandings", the RECEIVABLE report**. An operator typing
the words for what the company OWES was shown what it is OWED, under a heading that did not say so — the
class of wrong answer L16 deliberately refused to CREATE for "cash flow statement" (§6.5), except this one
was already live. It could not be fixed before this row, because there was no payables report to point it
at. ⚠️ **M2 also tripped a test I had not touched:** the existing *"CONTROL: Go To still opens the reports
it always did"* iterates `REPORT_NAMES`, so adding the name automatically extended an existing structural
gate to cover the new report's self-resolution. The totality property paying for itself again.

#### ⛔⛔ NO COUNTERWEIGHT MIGRATION — 0077 STAYS FREE, AND THIS CONTRADICTS THE ROW'S OWN TEXT
The driver said D94 "owes an ACL counterweight of its own", by analogy with D95. **Measured on the wire
against a freshly seeded tenant, with a control proving membership, three things say otherwise:**
1. a seeded Data Entry clerk **ALREADY reads `GET /outstandings?kind=PAYABLE` → 200.** There is no
   `outstandings` rule at all; 0011's coarse `allow display report` reaches it, and 0038 already refuses the
   printed form. **The data is not new**, so nothing "arrives pre-granted" the way the forecast plane did.
2. ⛔ **denying it would BREAK PAYMENT ENTRY.** The bill-wise picker calls `kindForGroup(group)` →
   `"PAYABLE"` for a Sundry Creditor, so a clerk keying a Payment needs that read; refused, they lose the
   picker and the money lands ON ACCOUNT — the balance nobody can chase.
3. a seeded denial would be only PARTLY enforceable anyway, for the same reason.
**The user was asked with those measurements and chose to seed nothing.** The rule is WRITABLE at
specificity 2 for a tenant that wants the split; the probe writes it, which is how such a tenant would.
⛔ **This is §2.7's shape a second time: a row's written scope is a CLAIM.** D85's was too big, D94's ACL
half was wrong in kind. Measure before building AND before asking.

#### ⛔⛔ AND IT REGISTERED **D97**, FOUND BY DRIVING THIS ROW'S ORACLE
The denied clerk can still read the same figures as `GET /outstandings?kind=PAYABLE` → **200, same total**.
**The rule governs the DOOR, not the bytes**, and an administrator writing *"may not see what we owe
suppliers"* would not expect that. ⛔ **It is not simply a bug to patch:** that read is what the bill-wise
picker needs. Any fix must separate *"read the payables REPORT"* from *"list THIS party's open bills to
settle one"* — a design decision about what the picker is authorised as, not a route change.
✅ **`drive-l24-d94.mjs` asserts the residual as an ARM**, so it is impossible to read the drive output and
believe the refusal is airtight.

#### GATES
`go test ./... -count=1` **exit 0, 0 FAIL, 0 SKIP** · `pnpm vitest run` **1463 / 87 files** (+6: the alias
arms with their control, the route-not-kind arm with its control, and the `REPORT_DOORS` sweep picking the
new name up for free) · `pnpm exec tsc --noEmit` clean, and it named both `Record<ReportName,…>` tables.
**Driven 15/15**, own tenant, reaped.

**THE WHOLE DRIVE SET, ON A FRESH SEED, AFTER THE LANE:** `verify:demo` **34/34** → l5-nlq **41/41** ·
l13 **33/33** · l14 **17/17** · l14b **8/8** · l15-d5 **22/22** · l15c **7/7** · l15d **13/13** ·
l15f **8/8** · l16-d22 **34/34** · l11-ai **14/14** · l20-d49 **52/52** · l21-d85 **16/16** ·
l22-d8 **14/14** · l23-d86 **16/16** · **l24-d94 15/15** · l17-d95 **20/20** · `contract:cored` **14/14**
→ `verify:demo` **34/34 again**.

---

### L25 — 2026-08-24 · D83 CLOSED: two CONCURRENT posts of one bill number merged into ONE bill

**The last of the builds.** ⛔ **The register now has ONE open row, D97, and it is a DESIGN DECISION rather
than a build** — see §7.1 and L24.

#### ⛔⛔ MEASURED ON THE WIRE FIRST TRY, BEFORE A LINE MOVED
Two sales posts in flight together — same company, same party, the same hand-typed `voucher_no`, each
opening a NEW bill, neither naming its reference:
```
post A -> 201 "7"       post B -> 201 "7"
GET /outstandings -> ONE bill "Sales/2026-27/7", pending 3000.0000, due 2026-07-10
```
The second instalment's **2000.0000 and its own 2026-08-10 due date were GONE** from the ageing report, with
nothing anywhere to say so. Nobody gets an error; a creditor's later instalment stops being chased.

#### ⛔ THE REGISTER'S PROPOSED FIX CANNOT EXPRESS THE RULE, AND THE DRIVER WAS RIGHT
A partial unique index on `bill_allocation` fails twice over: duplicate ref names are **LEGAL** for AGAINST,
for ON_ACCOUNT and for openers a CALLER named (that is how Tally adds to an existing bill, and how
`internal/migration` hands through what the XML says), so an index cannot tell the names this server
INVENTED from the ones a caller supplied — and it would fire on every ALTER against the voucher's own prior
version. ⛔ **And there is no ROW to lock:** the collision is between two names NEITHER transaction has
written yet. `resolveGeneratedBillRefNames`' own comment had already reasoned its way to *"a lock on a row
that does not exist"* and stopped there.

#### ✅ SO IT IS A LOCK ON A **NAME** — THE FIRST `pg_advisory_*` IN THIS CODEBASE
`lockGeneratedBillRefStems` takes `pg_advisory_xact_lock(hashtextextended(...))` on each (company, ledger,
STEM) the generated names would occupy, immediately **BEFORE** the "has any other voucher ever called a bill
this?" check. The loser blocks until the winner COMMITS, its check then reads the winner's row under a fresh
READ COMMITTED snapshot, and it takes the RENAME branch it would have taken sequentially:
```
"Sales/2026-27/7"             1000.0000  due 2026-07-10
"Sales/2026-27/7 (7d292bdd)"  2000.0000  due 2026-08-10
```
⚠️ **Four things, all written into the code:** it is said plainly that this is the first advisory lock here,
because a primitive nobody has used is one nobody looks for (`db.go` takes no TxOptions, so every
transaction is READ COMMITTED and nothing else in `backend/` serialises on anything but a row); the key is
the STEM and not the whole ref name, because the rename branch renames BY LEDGER; **the keys are SORTED**,
since a voucher can open bills on several parties and two posters taking an overlapping set in opposite
orders would deadlock; and `_xact_` scope means it is still held while the `bill_allocation` rows are
written, which is *why* it works.

#### ⛔⛔ THE DRIVER PREDICTED THE MUTATION WOULD NOT REPRODUCE. IT DID — 10 RUNS OUT OF 10.
§3 row 6 called a non-reproduction "the LIKELY outcome", on the strength of D90, whose equivalent mutation
reported 14/14 with two of three indexes missing because a Go pre-check shadowed the index for SERIAL
creates. Removing the lock and running the new gate with `-count=10` failed **every time**.
✅ **THE DIFFERENCE IS THE BARRIER, AND IT GENERALISES.** D90's probe had no way to make two creates overlap,
so the pre-check answered first and the missing index was invisible. This gate holds both goroutines on a
`sync.WaitGroup` until both are inside `PostVoucher`, so the overlap is **constructed rather than hoped
for** — and a race you can construct is a race you can gate. ⛔ Without that barrier it is the sequential
case wearing a WaitGroup, and the sequential case has always passed. **The non-reproduction D90 taught was a
property of D90's PROBE, not of races. Build the barrier, then judge.**

#### ⚠️ AND THE MUTATION FAILED ON THE **NAMES** CHECK, NOT THE ROW COUNT
More precise than expected, and worth keeping: both `bill_allocation` ROWS exist, sharing one NAME. Nothing
was ever lost from the table. What is lost is that `GET /outstandings` groups by `(ledger, ref_name)`, so two
rows under one name become ONE bill of the summed amount aged at the earlier due date.

#### THE DRIVE — 9/9, OWN TENANT, REAPED
`frontend/scripts/drive-l25-d83.mjs`. Both posts via `Promise.all`; CONTROLS first (both still 201, both
carrying the same number — the row is about the books, not about refusing a write); two bills, each keeping
its own amount and its own due date; exactly ONE discriminated `(xxxxxxxx)` stem with the other name plain,
so the loser is shown to have taken the RENAME branch rather than the two differing by luck.
⛔ **AND THE TOTAL IS ASSERTED BECAUSE IT DOES NOT MOVE:** Outstandings 3000.0000 == Balance Sheet Sundry
Debtors 3000.0000, in BOTH worlds. **The defect never lost money — it lost a BILL** — so a probe built on
totals, or on status codes, is green straight through it.

#### GATES
`go test ./... -count=1` **exit 0, 0 FAIL, 0 SKIP**, bracketed with a live psql. Binary verified fresh BY
SYMBOL (`pg_advisory_xact_lock` = 1). ⚠️ **The first re-measure of the race read as UNCHANGED because cored
had not been rebuilt** — §5.17, exactly as written.

**THE WHOLE DRIVE SET, ON A FRESH SEED, AFTER THE LANE** — and, since D83 was the last of the builds, this
is the state the whole wave leaves behind. Binary verified fresh BY SYMBOL first:
`pg_advisory_xact_lock`=1 (D83) · `bills-payable`=2 (D94) · `aged_as_on`=1 (D86) ·
`COALESCE(vv.voucher_no, ''), vv.voucher_id`=1 (D8).
```
verify:demo 34/34
  l5-nlq 41/41 · l13 33/33 · l14 17/17 · l14b 8/8 · l15-d5 22/22 · l15c 7/7 · l15d 13/13 · l15f 8/8
  l16-d22 34/34 · l11-ai 14/14 · l20-d49 52/52 · l21-d85 16/16 · l22-d8 14/14 · l23-d86 16/16
  l25-d83 9/9 · l24-d94 15/15 · l17-d95 20/20 · contract:cored 14/14
verify:demo 34/34
```
**GATES AT THE END OF THE WAVE:** `go test ./... -count=1` **exit 0, 0 FAIL, 0 SKIP** ·
`python3 corpora/validate.py` **OK** (inventory=7) · insightd pytest **166 passed** ·
`pnpm vitest run` **1463 / 87 files** · `pnpm exec tsc --noEmit` **clean**.
✅ **Seventeen drives now, and the set is still gate-safe** — three of the five lanes added a probe that
POSTS (l21, l23, l25) and `verify:demo` foots the same 34 checks at both ends.

---

### L26 — 2026-08-24 · D97 CLOSED: the payables rule now governs the BYTES, not just the door

⛔⛔ **THE LAST ROW IN THE REGISTER, AND THE ONLY ONE THAT WAS NEVER A BUILD.** It was registered by L24
from L24's own oracle, and it was written down as a DESIGN DECISION rather than a defect to patch. **The
user was brought the options before a line moved, and took them.** 97 registered · **97 CLOSED** · 0 OPEN ·
0 PARTIAL.

#### ⛔ WHAT WAS MEASURED BEFORE ANYTHING WAS PROPOSED (§3: measure, then ask)
Four facts, and the third and fourth are the ones that decided it:

1. `routeAction` maps the FLAT LITERAL PATH to the PDP resource (`auth_middleware.go`), and the PDP is
   **middleware-only** — no handler in `internal/server` consults it and `Server` holds no evaluator. A
   query parameter cannot reach the decision, so `/outstandings?kind=PAYABLE` is decided as
   `report:outstandings` no matter what it serves. Both doors share one body (`writeOutstandings`).
2. ⛔ **THE PRINTED DOOR HAD THE IDENTICAL LEAK AND THE ROW DID NOT MENTION IT.**
   `/print/reports/outstandings?kind=PAYABLE` is decided as `print report:outstandings`. Closing only the
   JSON one would have been **§5.19 exactly**: a refusal fully present and fully wrong, with the same rows
   on a filed sheet.
3. ⛔ **THE PICKER WAS ALSO OVER-READING, WHICH THE ROW DID NOT SAY.** `BillAllocationPicker` called
   `getOutstandings(companyID, { kind })` — the WHOLE book — and filtered to one `ledger_id` **in the
   browser**. To render one supplier's list it received every party's bills, the slab totals and the grand
   total. There was no party-scoped read anywhere: `reporting.Outstandings`' fifth argument is `slabs`, not
   a ledger filter.
4. ⛔⛔ **THE SEEDED LEVELS DECIDED THE ANSWER.** `0011_auth_acl.sql` makes Data Entry `default_access =
   deny` with `allow display <class>` per class it may key. **A NEW resource token is invisible to every
   existing default-deny level until a migration grants it** — and a level that loses its picker on upgrade
   posts every settlement ON ACCOUNT, which is the exact failure the row exists to prevent.

#### ✅ THE DECISION THE USER TOOK
**(1) The picker is authorised as the VOUCHER CLASS being keyed.**
`GET /vouchers/{class}/open-bills/{ledgerID}?kind=` — eight literal routes, one handler.
- The **class** is a literal path segment because that is the PDP's only view of the request. It is also
  what makes fact 4 harmless: every seeded level that can key the voucher **already holds the rule**, so
  **no migration, and no upgrade can silently take the picker away**.
- ⛔ **AND THE INSEPARABILITY IS THE POINT, NOT A COMPROMISE.** A level that may post a Payment but may not
  pick the bill it settles can only post ON ACCOUNT — the balance nobody can chase. Splitting those two
  would sell expressiveness for the very failure the row is about.
- The **ledger** is a path segment so there is **NO unscoped form of the route**. A `?ledger_id=` guarded by
  a check inside the handler would be the same defect wearing a third name. **Structure, not validation.**
- It emits ONE party: no slab totals, no grand total, no other party. Fact 3, closed with it.

**(2) `?kind=PAYABLE` is RETIRED from BOTH doors — a 308, not a 400 and not a handler-side 403.**
Nothing needs the parameter once the picker has its own door. A **redirect** keeps `handleBillsPayable`'s
own recorded promise that a stale bookmark is an ANSWER, and the follow-up request is a NEW request over a
DIFFERENT path — so `routeAction` decides it as `report:bills-payable` and **the MIDDLEWARE refuses it**. A
403 written in the handler would have been the first in-handler authorization decision in this codebase;
the PDP living in exactly one place is what makes it auditable.

#### ⛔ TWO THINGS RULED OUT BY MEASUREMENT, NOT BY PREFERENCE
A `routeAction` change (the driver forbade it, and it cannot see `?kind=` anyway); and an in-handler PDP
re-check to narrow the bytes — measured as a second decision point this codebase has deliberately never
had.

#### ⛔⛔ THE MUTATIONS — PREDICTIONS WRITTEN BEFORE RUNNING (§5.3), AND ONE OF THEM CORRECTED ME
**M1 — delete the 4-segment `open-bills` arm from `routeAction`.** Predicted (a) the row's own test fails
"not mapped by the PDP at all"; (b) `TestEveryCompanyRouteIsMappedAndInVocabulary` fails **8 times**, naming
each route — the structural gate catching it independently; (c) the non-class guard still passes, so it is
a guard and not a proof. ✅ **All three observed, verbatim.**

**M2 — delete the 308 from `handleOutstandings`.** Predicted (a) the redirect test fails **by PANIC inside
`writeOutstandings` on the nil reporting service**, which is itself the observation that the request reached
the report BODY; (b) the PRINTED test **still passes**, because the paper door is a different function; (c)
the receivables control still passes. ✅ **All three observed** — and (b) is the proof fact 2 needed its own
arm: **the JSON fix does not close the paper door.**

**M3 — point the picker back at `getOutstandings`.** Predicted the two D97 arms fail and the "ordinary sale
asks nothing" control still passes. ✅ Observed — ⚠️ **AND IT CORRECTED THE PREDICTION: five MORE tests
failed** than I wrote down, because the whole picker file's stubs now pin the new door. The prediction was
incomplete, not wrong, and the experiment is what said so.

#### ✅⛔ THE DRIVE — AND THE ARM THAT REGISTERED THE ROW WAS **INVERTED**, NOT DELETED
`drive-l24-d94.mjs`'s residual arm demanded a 200 and got one when it registered D97. It now demands a
**403** and gets one. **It is the same experiment pointed at the same URL**, which is the strongest form
this file has: the probe that found the row measures its closure.

⛔⛔ **AND THE FIRST RUN FAILED ITS OWN CONTROL, WHICH IS WHY THE FIXTURE WAS WRONG AND NOT THE DESIGN.**
The probe's clerk was a *Sales Clerk* with `default_access: none` and **no Payment class at all**, so the
picker answered 403 — correctly. A level that cannot key a Payment has no business settling a supplier
bill. The fixture now grants `create|alter|display payment` and **deliberately withholds `sales`**, which
turns that absence into the negative control:
```
same token, same ledger, class=payment (granted) = 200   ·   class=sales (NOT granted) = 403
```
⛔ **Without that pair the 200 would read equally well as "the new route lets anybody in"** — a worse leak
than the one being closed. The only difference between those two requests is the class segment, which IS
the claim.

And the read is the refused data, not an empty 200: the picker lists `D94/PAY/<stamp>` — **the very bill
`/bills-payable` had just answered 403 for** — while carrying `keys = bills,found,group,kind,ledger,
ledger_id,pending`: no `total`, no `slab_totals`, no `parties`.

⚠️ **THE RESIDUAL OF THE FIX, STATED RATHER THAN HIDDEN.** A level with `display payment` can call the
route once per supplier ledger it can already see and rebuild a list by hand. That is inherent to ANY
party-scoped door — decision (2)'s alternative had it too — and it is the accepted cost of (1). What it can
no longer do is read the BOOK: no ageing, no totals, no party it did not name.

#### GATES
`go build ./...` + `go vet` clean · `go test ./... -count=1` **exit 0, 0 FAIL, 0 SKIP** ·
`pnpm vitest run` **1466 / 87 files** (+3: the two D97 arms and their absence-control) ·
`pnpm exec tsc --noEmit` clean, and it named all four picker call sites plus the mock — the closed
`Record<VoucherType, VoucherClass>` table is what keeps the class in the URL a compile-time fact.
Binary verified fresh BY SYMBOL: `handleOpenBills` = 1. ⚠️ **AND THE FIRST START SILENTLY DID NOT TAKE** —
a stale `go run` cored from 15:40 still held :8087 and the fresh binary logged `bind: address already in
use` while `/healthz` answered 200 from the OLD process. §5.17's trap, wearing a green health check.
Driven **20/20**, own tenant, reaped.

### L27 — 2026-08-27 · THE SEVEN ROWS THE PARITY AUDIT NEVER SAW, DRIVEN. **SIX CLEAN, ONE LYING.**

`HANDOFF-AUDIT-THE-UNDRIVEN.md` (now `git rm`'d, its work done here) identified the gap the audit's
own scope created: the audit drove **34 reachable rows of a 47-row `FeaturesScreen`**, and the screen
is **53 rows** now. Ten were added; two of the ten are a split and a rename of audited rows. **Seven
were genuinely undriven.** All seven are driven below.

⭐ **THE HANDOFF'S PREMISE WAS PARTLY WRONG AND SAYING SO IS THE FIRST FINDING.** It said none of the
seven "has ever been driven end to end". In fact **every one of them shipped with a browser drive
beside it**, and all of those still pass against today's build — `drive-t32-import-screen` (U1),
`drive-d-offline-queue` 27/27 (U2), `drive-d-reconciliation` (U3), `drive-t14-data` 25/25 (U4 and
U7), `drive-c-print-templates` 33/33 (U5), `drive-b2-email-document` 20/20 (U6). ⛔ **What had never
happened is an AUDIT** — reading each drive's assertions against the row's own words and driving what
the row claims and the drive does not reach. That is a different and smaller job than the handoff
described, and it is the one that found things.

| Row | Verdict |
|---|---|
| **U1** `Migrate from Tally (Alt+O)` | ✅ **TRUE AS OF 2026-08-27 — three of its four defects are FIXED.** D98 (HIGH, the refused import was not atomic and the recovery double-posted) and D101 (HIGH, vouchers Tally excludes were posted as live money) are closed with mutation proofs and browser re-drives; D100 (MEDIUM, the export recipe) is closed as copy. ⚠️ **D99 (LOW) REMAINS OPEN BY DECISION, NOT BY OMISSION** — its premise did not survive a real TallyPrime export and fixing it speculatively would make genuinely malformed numbers readable. See below |
| **U2** `Offline Outbox (Alt+Q)` | ✅ **CLEAN**, **23/23** on four claims the shipped drive could not reach |
| **U3** `Reconciliation (Alt+R)` | ✅ **CLEAN**, **32/32** with the books arm that could actually fail |
| **U4** `Backup / restore (Alt+Y)` | ✅ **CLEAN**, **10/10**, and the anti-rot claim proved against a table added the day before |
| **U5** Per-company print templates | ✅ **CLEAN**, **33/33**. Reached from the Gateway; **no key was invented for it** |
| **U6** `E-mail a document (Alt+M)` | ✅ **CLEAN**, **20/20**. The oracle is the `mailto:` URL, driven with `altMac` |
| **U7** Split company data | ✅ **CLEAN** — an honest `off`, and the Data screen states the reason (`data-split-absent`) |

#### U1 — THE COMMERCIAL BLOCKER, AND IT IS NOT ONE PROBLEM BUT THREE

⛔⛔ **NOTHING IN THIS REPOSITORY HAD EVER DRIVEN THE IMPORTER'S REFUSAL PATH**, because every fixture
in `corpora/migration/` parses. Five fixtures that do not parse are now committed beside them
(`08-refusal-*.xml`, `09-refusal-grouped-amount.xml`); `corpora/validate.py` never sees them, because
it discovers cases by globbing `*.json` and these carry none — the same shape as 06 and 07.

**D98 (HIGH) — a refused import is not atomic, says nothing about it, and the recovery double-posts.**
`drive-u1-import-partial.mjs`, four throwaway tenants, all reaped. A three-voucher export whose third
voucher carries `<AMOUNT>$ 1000.00</AMOUNT>` is refused with a good sentence naming the voucher
ordinal, the ledger and the value — **and vouchers 1 and 2 are in the books**, read back off the
operator's own **Day Book** in the same session. ⛔ **THE RECOVERY IS THE MONEY LOSS AND IT WAS
MEASURED BOTH WAYS**: correcting the file and re-importing gives **5 vouchers** on a GUID-less export
(two duplicated, no delete path anywhere in this product) and **3** on a GUID-bearing one, which is
what TallyPrime itself writes. The GUID arm is the control that keeps the blast radius honest.

**D100 (MEDIUM) — the product tells an operator to produce a file and does not tell them how.** Driven
against a **genuine TallyPrime export pair the user supplied** — a real trading company's 2019-20
books, `Master.xml` 84 MB UTF-16LE and `Transactions.xml` 121 vouchers. ⛔ **NEITHER FILE IS IN THIS
REPOSITORY AND NEITHER MAY BE**; `drive-u1-real-export.mjs` reads them from `TALLY_MASTERS` /
`TALLY_TXNS` and records a **SKIP as a failed probe** when they are absent, because 22 vacuous greens
against two missing files is the worst outcome available. The migration's first act is the
whole-import abort naming two remedies — and remedy (1), "import a Masters.xml carrying the items
with their opening balances FIRST", cannot be acted on with the file the screen told them to make:
its `TALLYMESSAGE` children count **LEDGER 3706 · GROUP 37 · CURRENCY 2 · COMPANY 1**, no
`STOCKITEM`, no `UNIT`, no `GODOWN`, while the transactions name 6 stock items, 1 godown and 75
batches — so those masters plainly exist in the source company.

⭐⭐ **SETTLED THE SAME DAY, AND THE ANSWER DOWNGRADED IT FROM HIGH TO MEDIUM.** TallyPrime's master
export carries a **Type of Master** dropdown (`Accounting Masters` / `Inventory Masters` /
**All Masters**) plus **Include dependent masters** and **Export closing balance as opening balance**
([TallyHelp](https://help.tallysolutions.com/tally-prime/data-management/export-data-in-tally/)). The
capability exists; the user's file is an **Accounting Masters** export. So this is a DOCUMENTATION
defect — nothing is silently wrong and the product refuses correctly and loudly; the cost is that it
sends every migrating customer down the worse of two doors for want of one sentence.

⚠️⚠️ **AND ONE PIECE OF EVIDENCE IN THE FIRST DRAFT WAS WRONG, RECORDED RATHER THAN QUIETLY DROPPED.**
It read *"their `Master.xml` IS the All Masters export — its own REPORTNAME says so"*. **`<REPORTNAME>`
is VESTIGIAL in a TallyPrime export: both files carry `<REPORTNAME>All Masters</REPORTNAME>`,
including `Transactions.xml`, which holds 121 VOUCHERs and ZERO ledgers.** It identifies nothing —
count the `TALLYMESSAGE` children instead, which is what the load-bearing measurement always was.

⭐ **AND THE REST OF THE MIGRATION IS GOOD, WHICH IS WHY THIS IS A BLOCKER RATHER THAN A DISASTER.**
**22/22 in a browser**: 19 real accounting warnings rendered in full, `opening_positions: 0` shown as
a WARNING and not a clean result, F11 reaching the negative-stock control from the keyboard, then 121
vouchers and 226 stock movements with **zero** warnings, a trial balance that **BALANCES at
25,986,501.8800 over 3,712 rows**, and the Stock Summary the report tells you to check showing the
real items. ⚠️ One more measured fact, recorded and not a defect: the transactions create **72 ledgers
the All Masters export did not contain**, so Tally's "all masters" is not all the masters its own
vouchers post to.

**D99 (LOW, lowered from MEDIUM the same day) — its premise did not survive contact with a real file.**
Every money and quantity field in the real export was **counted, not sampled**: 1,156 `AMOUNT`, 714
`OPENINGBALANCE`, 454 `BILLEDQTY`, 454 `ACTUALQTY`, 227 `RATE` — **zero commas**. So `SplitQtyUnit`'s
comment ("Commas are thousands separators in Tally's own output") is the only evidence for the claim
and the one real export we have contradicts it. ⚠️ A comma *does* appear, 51 times, inside a stock
item NAME (`TOORDAL & DALI,ATTA,ETC`) — a reader that stripped commas from a name would merge items.

**D101 (HIGH) — the importer posts vouchers Tally keeps OUT of the books.** Found by ENUMERATING the
real export element by element against every tag the parser binds: `<VOUCHER>` has **207 distinct
direct children in a real file and `TallyVoucher` binds 15**. Almost all of the other 192 are Tally
internals nobody should read; three are not — `ISCANCELLED`, `ISOPTIONAL`, `ISDELETED`. ⛔ **Driven,
not inferred** (`drive-u1-voided-vouchers.mjs` over `corpora/migration/10-voided-vouchers.xml`): a
file whose second voucher is cancelled and whose third is optional reports **"Vouchers posted: 3"**
and **"Warnings (0)"**, and both appear in the operator's **Day Book** beside the real one. A
`ISDELETED=Yes` LEDGER master is created too. ⭐ **The asymmetry is the tell**: `Deleted()` is honoured
on **five** master kinds and never reached the voucher or the ledger, the two oldest readers.
⚠️ **Latent in this export** — all 121 vouchers carry `No` — but the export is demonstrably
**unfiltered**: all five voucher-number series are contiguous (Sales 7594-7641 · Receipt 475-511 ·
Payment 403-414 · Journal 60-68 · Purchase 105-119, zero missing), and a cancelled voucher in Tally
retains its number, so a filtering export would leave gaps.

#### U2 — FOUR CLAIMS ABOUT A *QUEUE*, WHICH A ONE-VOUCHER DRIVE CANNOT REACH

`drive-u2-outbox-batch.mjs`, **23/23**. The shipped drive is thorough and queues exactly one voucher,
so "one write, **oldest first**", the **whole-batch refusal banner**, **discard**, and **cleared on
sign-out** had never run. All four hold. ⛔ **The batch refusal is produced by the REAL control** — the
back-dated cutoff, set through the admin route — and not by stubbing a 422, because a stub would prove
the banner renders and nothing about whether the cutoff judges a batch as a unit. It does: banner
shown, both rows still queued, **nothing written**. ⛔ **Oldest-first is proved by the SERVER'S OWN
NUMBERING**, not by row order on screen — a screen that listed them oldest-first and sent them
newest-first would look identical. Sign-out really does clear the queue and the abandoned voucher
reaches no books either: lost is lost, not silently posted.

⚠️ **AND A TRAP FOR THE NEXT PROBE THAT TOUCHES A SECURITY LEVEL: clearing the cutoff is the EMPTY
STRING, not `null`.** `levelReq.BackdatedCutoff` is a `*string` so ABSENT and PRESENT are
distinguishable, and `null` decodes to absent — "leave it alone". A `null` clear returns **200 with
the cutoff still set**, and the next commit is refused for a reason the probe is no longer watching
for. That cost one run and read as a product failure.

#### U3 — THE ONE ARM THAT COULD ACTUALLY FAIL WAS MISSING

The row says clearing an entry "RE-APPLIES NOTHING". The shipped drive checked that against the
SCREEN and against `resolved_at`; **neither can see the failure the clause is about** — a resolve that
also replayed the parked intent would stamp the column, render the same sentence, and leave the books
carrying an edit the sequencer refused. Two arms added to `drive-d-reconciliation.mjs` (**32/32**):
the voucher head is byte-for-byte where it was, and the keyed version is in **no** version at all.

⛔ **THAT FILE HAD NO `try`/`finally`, WHICH IS THIS REPOSITORY'S OWN DOCUMENTED RULE.** Adding the two
arms threw on a Postgres precedence trap — `a || b->>'k'` parses as `(a || b) ->> 'k'` — and the drive
died between `openApp` and the teardown **twice**, leaking a tenant each time. Both were swept by
hand; the guard is now in, deliberately without re-indenting the 290 lines it wraps.

#### U4 — THE ANTI-ROT CLAIM, TESTED AGAINST A TABLE ADDED THE DAY BEFORE

`drive-u4-backup-newest-table.mjs`, **10/10**. ⛔⛔ **The shipped drive's tenant-exactness probe is
structurally blind to this row's worst failure**: "the restored company matches the archived one on
every table" compares the RESTORE against the ARCHIVE, so a table missing from the **enumeration** is
missing from both and the comparison is green while the data is gone. The row's actual claim is that
the enumeration is catalog-derived, *"so a table added by tomorrow's migration is archived without
anyone remembering to add it"* — and the T8 lane landed **migration 0080** the previous day.

A `currency` and a `currency_rate` written through **T8's own routes** (this lane edits none of their
files) survive a backup and a restore with nobody having touched `internal/backup`, and the rate comes
back as `87.1234567890` — to the last decimal, because an archive that rounds is an archive that lies.
The general form is checked too, catalog-derived on the probe's side as well so it keeps working when
0081 lands: **11 tables held rows and every one came back, none short**, with `company_membership`
named as the single deliberate exception rather than skipped.

#### WHAT IS LEFT, AND IT IS NAMED RATHER THAN IMPLIED

⚠️ **ROWS 20 / 23 / 26 HAVE STALE DRIVES.** They were driven before **T9** (FIFO/LIFO, `0078`) and
**T10** (stock groups + compound units, `0079`) landed underneath them. The rows are old; their
evidence is not current. That is the next audit lane, and it is smaller than this one was.
