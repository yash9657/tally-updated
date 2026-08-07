# THE PARITY AUDIT — FINISH THE REMAINING 18 ROWS, END TO END

> ## ⛔ THIS IS A WORKSHEET, NOT A RIVAL HANDOFF. `HANDOFF.md` IS STILL THE LIVE ONE.
>
> §11 of `HANDOFF.md` records eleven SPENT `HANDOFF-*.md` files and the cost of having them around:
> *"a handoff nobody can trust costs a session to re-verify."* This file exists only because a
> 47-row checklist does not belong inside a 2,400-line narrative. It is owned by `HANDOFF.md` §1.11.
>
> **WHEN THE AUDIT IS DONE: fold the findings into `HANDOFF.md` §5.5's log and DELETE THIS FILE.**
> Do not let it become the twelfth.

---

# ⛔⛔ COLD START? THIS SECTION IS THE WHOLE BRIEF. READ IT, THEN §2, THEN GO.

**Written 2026-08-07 at the end of the second audit pass, for a session with an empty context window.**
Everything needed to finish the job is in THIS file plus `PARITY-RECON-2026-08-07.md`. You should not
need to read `HANDOFF.md` to start — only to write findings into §5.5 at the end.

## THE MISSION, IN ONE PARAGRAPH

34 rows of `FeaturesScreen` claim to be reachable from the keyboard. **16 have been driven; 18 never
have.** Of the 16 driven, **11 were lying** — the claim was false and only pressing the key found it.
Your job is the remaining **18: drive each one end to end in a real browser against real cored, read
the result back from the place the money actually lands, fix what you find, and log it in §7.**

| | |
|---|---|
| **Driven and confirmed** | 16 — rows 1, 2, 4, 6, 7, 8, 11, 14, 15, 16, 20, 21, 22, 23, 26, 36 |
| ⛔ **YOUR WORK LIST — 18 undriven** | **3, 5, 9, 17, 24, 27, 29, 30, 31, 32, 33, 34, 35, 37, 38, 40, 42, 43** |
| **Lie rate so far** | **11 in 16.** Budget for FIXING, not for driving |

## ⛔ TWO DEFECTS ARE ALREADY FOUND AND STILL OPEN. THEY ARE YOURS TOO.

| # | What | Where |
|---|---|---|
| **Row 20** | ⛔ **CONFIRMED LYING, NOT FIXED.** `Alt+C` renders **"Create ledger"** from the Gateway AND from the item-invoice Item cell. No keystroke, Gateway entry or Go To entry reaches `masterKind` `"stock-item"` or `"unit"` — **a company can never be given a stock item or a unit from the keyboard**, and the item-invoice empty state points at `Gateway > Masters`, which does not exist. It is a FEATURE TO BUILD | §7's row-20 box has the traced route (RESEARCH.md:568 documents it) and the reason the obvious keymap fix cannot work |
| **LEAD 1b** | `MembersScreen` has the same shape as the step-up defect fixed on both e-* screens. **Traced to line numbers, never driven, deliberately not patched** | §4, LEAD 1b |

## THE ORDER TO WORK IN

1. **§0.1's RUN-FIRST CHECKLIST** (immediately below) — ten minutes, and skipping it has burned sessions.
2. **Drive rows, cheapest first.** Suggested order, grouped so one browser session covers several:
   - **Masters / bill-wise:** 5, 3, 9 (all voucher-form work, and row 5 feeds row 3)
   - **Inventory:** 24, 27, 29
   - **Statutory:** 17
   - **AI-native:** 30, 31, 32
   - **Users & security:** 33, 34, 35, 37, 38 — ⛔ **34 and 38 need a SECOND user**; read their recon entries before starting, they may not be drivable as written
   - **Output:** 40, 42, 43
3. **Fix what you find, as you find it.** §0 explains why deferring loses the finding entirely.
4. **Log every row in §7, pass or fail**, and update the running count.

⚠️ **DO NOT PLAN TO FINISH ALL 18 IN ONE SESSION.** The last pass drove FOUR rows and filled a
session, because three of them were lying and the fixes were real work with tests and mutation
proofs. **Every row you drive and log is permanent progress; a row half-driven and unlogged is not.**

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

## 0.2 THE 18 UNDRIVEN ROWS — where each one's trace lives

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
| 3 | Bill-wise details (4 ref types) | `F8` sale to a bill-by-bill party → `F6` receipt → settle | Outstandings **reconciles to the Balance Sheet to the paisa**. Drive **all four** ref types | PREV (New + Agst) |
| 4 | Credit / Debit Notes | `Alt+F6` / `Alt+F5` → key → `Ctrl+A` | `posting` sides. **CN verified 2026-08-07; ⛔ DEBIT NOTE STILL NEVER DRIVEN** | ~ |
| 5 | Opening balances (with bill-wise breakup) | `Alt+C` create ledger with opening + bills | the opening bill appears in Outstandings and the Balance Sheet opens balanced | — |
| 6 | Single-entry mode (Payment/Receipt/Contra) | `F5`/`F6`/`F4`, `Ctrl+H` → Single Entry | `posting`: one primary leg + N counter-legs, correct sides | PREV |
| 7 | Voucher numbering series | key 2 of a class; cross a FY boundary | `voucher_number_counter`: per (class, FY), monotonic, **restarts next FY** | PREV |
| 8 | Cost centres + both reports | ⚠️ **NOT `Alt+G`** — Gateway → Cost Centres; both summaries | per-(posting, **CATEGORY**) sums — ⛔ **NOT per posting**; axes are parallel (§8.1) | ✅ 2026-08-07 (reachable from the GATEWAY; the empty period prompt is deliberate and says why) |
| 9 | Cost allocation during entry | `F5` → allocate across 2 axes | `posting_cost_allocation`: **both** axes complete and independent | PREV |
| 11 | Bank reconciliation | ⚠️ **NOT `Alt+G`** — Gateway → Bank Reconciliation | a marked line changes the reconciled balance and survives a reload | ⛔→✅ 2026-08-07 (**four defects, all fixed**: silent out-of-window tick, `voucher_no` collides 4 ways, blank `As at`, refused date left on screen) |

### Statutory

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 14 | GST (derivation, GSTR-1, GSTR-3B) | `F8` intra **and** inter-state | CGST/SGST vs IGST split; the supply appears in **GSTR-1 b2b AND the table-12 HSN summary** | PREV (partly) |
| 15 | e-Invoice / IRN | `Alt+I` → pick → `Ctrl+A` | IRN in `voucher_external_reference`; re-post returns the SAME IRN; ALTER+DELETE both 409 | ✅ 2026-08-06 |
| 16 | e-Way Bill | `Alt+Z` → state the movement → `Ctrl+A` | `e_way_bill` row; disclosures rendered in full; ⛔ **check the step-up wipe (§4 lead 1)** | ⛔→✅ 2026-08-07 (**LEAD 1 confirmed with a control; TWO defects, both fixed, on this screen AND `EInvoiceScreen`**) |
| 17 | TDS / TCS | `Alt+G` → TDS Challans → record + allocate | `tds_deduction` / `tds_challan_allocation`; the challan's unallocated balance falls | — |

### Inventory

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 20 | Stock items + units (masters) | `Alt+C` → Stock Item | the master persists **with its unit** — a blank unit is what broke every e-invoice line | ⛔ 2026-08-07 — **LIES. `Alt+C` renders `Create ledger` everywhere; NO key reaches a stock-item or unit form. NOT FIXED — §7's box** |
| 21 | Item Invoice (`Ctrl+H`) | `F8`/`F9` → Ctrl+H → Item Invoice | `stock_movement` **qty AND value**; server-derived postings | PREV |
| 22 | Godowns / batches | key a line with a godown **and** a batch | movement lands on the **right position** — ⛔ see §4 lead 3 (the NULL-godown default) | — |
| 23 | Stock Summary (qty AND value) | `Alt+G` → Stock Summary | ⛔ **`closing_value`, not just `closing_qty`** — this exact oracle gap shipped once | PREV (qty only) |
| 24 | Negative stock `always` | sell more than you hold | refused, or allowed per `allow_negative_stock` — and the flag actually governs it | — |
| 26 | Inventory vouchers (**FOUR** classes) | `Alt+F7` `Alt+F8` `Alt+F9` `Alt+F10` | ⛔ **the two `Alt+F7`/`Alt+F10` leads in §4 are the highest-value unverified items in this file** | ~ (F8/F9 only) |
| 27 | Orders + BOM | `Ctrl+F8` `Ctrl+F9` `Alt+X` | order book Ordered/Delivered/Outstanding; ⛔ an order **moves no stock** | PREV |
| 29 | Closing stock on the Balance Sheet | Balance Sheet at 2 dates | closing stock **moves the sheet** and the sheet still balances | PREV |

### AI-native

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 30 | Bill Inbox (OCR → draft) | Gateway → Bill Inbox → accept a draft | the accepted draft becomes a real voucher with the OCR'd figures; PII stays redacted | — |
| 31 | Ask your books (NL query) | Gateway → Query | the answer matches the same figure read from a report; ⛔ a cross-tenant probe is refused | — |
| 32 | Cash-flow projection / anomalies | `Alt+G` → the forecast report | it discloses that it is a projection, and the basis is stated | — |

### Users and security

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 33 | Multiple users + security levels | `Alt+G` → Users & Roles → invite | membership appears; the invitee is PENDING until claimed | — |
| 34 | Custom levels (Disallow/Allow) | edit a level, save, act as it | ⛔ **sign in AS that level and be REFUSED.** Reading the rules back is not the test | — |
| 35 | Switch company (`F3`) | `F3` → pick | the whole session re-scopes; no row of the old company leaks | — |
| 36 | WebAuthn step-up for writes | any write | 403 `STEPUP_REQUIRED` → ceremony → retry succeeds. ⛔ **§4 lead 1 lives here** | PREV |
| 37 | Back-dated entry cutoff | key a voucher before the cutoff | refused **by name**, and a voucher after it still posts (control) | — |
| 38 | Password login + lockout | N bad passwords | locked out, and it **unlocks** as configured | — |

### Printing and output

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 40 | Printed documents (PDF) | `Ctrl+P` on each of the 7 document classes | a real PDF (`%PDF`…`%%EOF`) **naming itself** — drive more than one class | PREV (sales) |
| 42 | Printed statements (PDF) | `Ctrl+P` on each report | as above, per report | PREV (TB) |
| 43 | Export a report (`Alt+E`) | `Alt+E` on several reports | ⛔ **WIRE precision, not display strings** — this row lost paisa once. CSV-injection guarded | PREV |

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


### The running count — update it, do not assume it

Measured 2026-08-07, end of the SECOND pass. **This is still a PARTIAL audit — 16 of 34 rows are driven, and 18 have never been driven at all.**

| | Rows |
|---|---|
| Claimed reachable (`on` + `always`) | **34** |
| **Driven end to end and CONFIRMED** | **16** — rows 1, 2, 4, 6, 7, 8, 11, 14, 15, 16, 20, 21, 22, 23, 26, 36 |
| **Driven and found LYING** | **11** — rows 2, 11, 14, 16, 20, 21, 22, 26 (`Alt+F7`), 26 (`Alt+F10`), row 1's unkeyable Date/Narration, and **every `Alt`+letter on macOS** |
| **Fixed and re-driven green** | **10 of 11.** ⛔ **Row 20 is the one found and NOT fixed** — it is a build, not a patch; see the box below |
| **Still undriven** | **18** — rows 3, 5, 9, 17, 24, 27, 29, 30, 31, 32, 33, 34, 35, 37, 38, 40, 42, 43 |
| **TRUE reachable number** | ⛔ **STILL UNKNOWN.** 16 of 34 measured. The lie rate across everything ever driven here is now **11 in 16** — do not budget for driving, budget for FIXING |

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
- **Re-measure the gates; do not assume them.** Current (2026-08-07, end of the second pass): backend
  **2372 / 0 SKIP / 0 FAIL** (verbose census), frontend **996 / 68 files**, `tsc` clean,
  `make verify` **0**, `verify:demo` **33/33** freshly seeded.
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
