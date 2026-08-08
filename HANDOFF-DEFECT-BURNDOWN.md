# ⛔ THE DEFECT BURNDOWN — 72 OPEN, ALL TRACED. FIX THEM IN PARALLEL.

> ## THIS FILE REPLACES `HANDOFF-PARITY-AUDIT.md` AS THE LIVE DRIVER. IT IS NOT A TWELFTH HANDOFF.
>
> §11 of `HANDOFF.md` records eleven SPENT `HANDOFF-*.md` files and the cost of keeping them:
> *"a handoff nobody can trust costs a session to re-verify."* So read the division of labour and do not
> duplicate anything across it:
>
> | File | What it is | Do |
> |---|---|---|
> | **THIS FILE** | the **execution plan**: how to parallelise, who owns which files, what proof each fix needs | follow it |
> | `HANDOFF-PARITY-AUDIT.md` **§7.1** | the **DATA**: all 78 driven defects, each at file:line with a proposed fix | your work list — read your lane's rows |
> | `HANDOFF-PARITY-AUDIT.md` §7.2–§7.4 | the six-door cutoff lesson, what is still open, and what was already fixed and how it was proved | read §7.2 before writing any authorization code |
> | `HANDOFF.md` | still the project's live narrative | fold results into §5.5 at the end |
> | `PARITY-RECON-2026-08-07.md` | superseded — every row it traced is now driven | history; open only for a row's reasoning |
>
> ⛔ **NEVER copy a defect's description into this file.** Two copies drift, and the drifted one gets
> believed. Point at `§7.1 D<n>`.
>
> ✅ **WHEN THE BACKLOG IS EMPTY: fold the outcome into `HANDOFF.md` §5.5 and DELETE THIS FILE *and*
> `HANDOFF-PARITY-AUDIT.md` *and* `PARITY-RECON-2026-08-07.md`.** Three files become none.

---

# ⛔⛔ COLD START? READ THIS SECTION, THEN §2, THEN GO.

**Written 2026-08-08 at the end of the third audit pass, for a session with an empty context window.**

## WHERE THINGS STAND, IN ONE PARAGRAPH

The parity audit is **DONE**. All **34** rows that claim to be reachable from the keyboard have been driven
end to end in a real browser against real cored. ⛔ **Of the 18 driven in the last pass, NOT ONE was
clean** — 17 were defective and row 32 cannot be reached from the keyboard at all. Driving produced **78
defects**; fixing them produced **16 more** (the same control kept having another door). **22 are fixed,
each with an executed mutation proof AND a browser re-drive. 72 REMAIN, every one traced to file:line with
a proposed fix.** Your job is to burn them down, in parallel, without breaking the 5 green gates.

| | |
|---|---|
| Rows driven | **34 of 34** — the measurement is closed, do not re-drive to "check" |
| Register defects (`§7.1`) | **78** · closed **13** · ⛔ **open 65** (30 high, 25 medium, 10 low) · 1 PARTIAL (D13) |
| Found while fixing | **16** · closed **12** · ⛔ **open 4** (`§7.3` **B, C, D**) |
| ⛔ **YOUR WORK LIST** | **69 open defects.** §3 groups them into lanes by FILE OWNERSHIP |
| Gates, measured 2026-08-08 | backend **2483 / 0 SKIP / 0 FAIL**, frontend **1076 / 69 files**, insightd **137**, `tsc` clean, `make verify` **exit 0**, `verify:demo` **33/33** |

> ⛔ **L3 IS DONE (§7.3 A, the bank-date cutoff). Do not re-open it.** Its lesson generalises and the next
> lane should read it: `peekBankDate` could NOT be the "mirror `peekDepositDate`" this file prescribed,
> because the two stores disagree about what a date is — `masters.validateTDSChallan` parses strictly and
> 400s, while `banking.SetBankDate` hands the string to Postgres as `$6::date`, which **accepts `2026-5-20`**.
> Copying the sibling verbatim would have shipped the round-1 loose-spelling door on a new route.
> **The peek's fail-open/fail-closed choice belongs to the STORE behind it, not to the sibling beside it.**

## ⛔ SPEED IS A REQUIREMENT, AND PARALLELISM IS HOW YOU GET IT — BUT ONLY ONE SHAPE WORKS

The last pass drove 18 rows and fixed 22 defects in one session using ~30 agents. **That is the proven
rate, and it came from a specific structure. Copy the structure.** §2 is that structure. The two things
that made it work:

1. **Every agent gets its OWN freshly seeded tenant**, so parallel writes cannot pollute anybody's oracle.
2. **Editing and driving are SEPARATE WAVES.** Six agents editing `frontend/src` at once means vite is
   HMR-reloading constantly, and any browser result taken during an edit wave is worthless.

⚠️ **AND ONE PIECE OF FOLKLORE IS NOW DEAD.** Earlier handoffs said *"the browser pass is the
orchestrator's; driving does not fan out."* **That was wrong and the last pass disproved it:** 10 agents
drove 14 rows concurrently, each on its own tenant with its own Chromium and its own virtual
authenticator. **Driving parallelises fine.** What does NOT parallelise is *editing the same file* and
*restarting a shared service*.

---

## 0. ⛔ RUN FIRST — the stack, the seed, the gates. Ten minutes, and skipping it has burned sessions.

```bash
cd /Users/yashbhalgat/code/tally-updated
git status --porcelain          # commit or stash BEFORE anything

# 1. POSTGRES. ⛔ A TCP CONNECT IS NOT LIVENESS — docker-proxy holds the port open
#    past a dead backend, and a watchdog built on `nc -z` logged NOTHING through a
#    ten-minute outage.
docker ps | grep tally_pg
pg_isready -h localhost -p 5433 -t 5 && \
  PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"

# 2. THE FOUR SERVICES
for p in 8087 8088 8091 5173; do printf "%s: " $p; \
  curl -s -o /dev/null -w "%{http_code}\n" --max-time 3 http://localhost:$p/healthz; done
```

⛔ **START cored / statutoryd / insightd DETACHED, or they die with your own tool call.** `setsid` does not
exist on macOS; the subshell-orphan form is what works:

```bash
(nohup make run-cored      > /tmp/cored.log      2>&1 < /dev/null &)
# wait for :8087, THEN statutoryd — it mints its token FROM cored, and every cored
# restart invalidates the previous one (a stale token presents as a bare 401 naming
# neither process, token nor expiry, and cost an hour once).
(nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 < /dev/null &)
# ⛔ USE `make run-insightd`, NOT a bare uvicorn line: the make target sets
# INSIGHTD_DATABASE_URL, i.e. the DURABLE Postgres draft store. A bare uvicorn gives
# you the in-memory store, and the draft accept-once guarantee is then only as
# durable as one process.
(nohup make run-insightd   > /tmp/insightd.log   2>&1 < /dev/null &)
cd frontend && pnpm dev    # :5173, if not already up
```

### ⛔⛔ 0.1 — VERIFY WHAT IS ACTUALLY RUNNING, BY SYMBOL. THIS IS NOT OPTIONAL.

**A restart in the last pass silently failed and a whole re-drive ran against 17-hour-old code, reporting
six already-closed doors as wide open.** The kill pattern matched `cmd/cored`, but the COMPILED CHILD's
command line is `/var/folders/.../exe/cored serve`, so the old process survived, kept port 8087, and
`/healthz` answered **200 from the stale binary**.

```bash
# kill by PORT OWNER, never by command pattern
lsof -nP -iTCP:8087 -sTCP:LISTEN | tail -1 | awk '{print $2}' | xargs -r kill
# ...restart, then PROVE the new binary contains your change:
BIN=$(ps -o command= -p $(lsof -nP -iTCP:8087 -sTCP:LISTEN | tail -1 | awk '{print $2}') | awk '{print $1}')
ps -o lstart= -p $(lsof -nP -iTCP:8087 -sTCP:LISTEN | tail -1 | awk '{print $2}')
strings -a "$BIN" | grep -c '<a symbol your fix introduced>'      # 0 = STALE. STOP.
```

⚠️ **`go tool nm` is useless here** — it exposes ~103 C externals for this binary. Use `strings -a`.
⛔ **A stale binary makes every fixed door look open again**, which is the most expensive false result this
project can produce: it sends a session chasing bugs that are already dead.

```bash
# 3. SEED. ⛔ NOTE THE FLAG — without it verify:demo reports 30/31 with one FAIL that
#    reads exactly like a code regression.
cd frontend && pnpm seed:demo --insightd
pnpm run verify:demo "$(cat .demo-company-id)"      # -> ALL CHECKS PASSED (33/33)

# 4. THE GATES. ⛔ RUN `make verify` ALONE (see §5).
make verify                                        # -> exit 0
cd frontend && pnpm exec tsc --noEmit              # a SEPARATE gate from make verify
```

⚠️ **A DOWN DATABASE MAKES EVERY DB TEST SKIP AND `go test` STILL EXITS 0.** The **`0 SKIP`** is the
load-bearing number — bracket any census with a live `psql` **before and after**.

### Signing in, and the DB oracle

`http://localhost:5173/?company=$(cat frontend/.demo-company-id)`, user `browser-dev-owner`, password
`tally-demo-2026`. **It is a keystroke app: a click SELECTS, `Enter` OPENS.** Drive it with
`frontend/scripts/audit-harness.mjs` — ⛔ **import it, never rewrite it**; it carries the traps below.

⛔ **`psql` AS `tally_app` RETURNS ZERO ROWS FOR EVERY TENANT TABLE.** RLS needs a session GUC a bare
`psql` does not set, and the symptom is `count(*) = 0` — which reads exactly like "the write did not
happen" and nearly produced a false "the browser did not save it" conclusion. **Use the `postgres`
superuser for every DB oracle.**

---

## 1. ⛔⛔ THE FIVE RULES. BREAKING ANY ONE OF THEM WASTES A WHOLE SESSION.

### RULE 1 — A mutation proof proves the TEST. Only the BROWSER proves the FIX.

The second pass traced a defect to a line, fixed it there, wrote a regression test that **PASSED**, and ran
a mutation proof that **FAILED EXACTLY AS PREDICTED** — **and the browser was still broken**, because the
traced cause was not the live one.

> **Every fix carries BOTH: an EXECUTED mutation proof whose failure you checked against your prediction,
> AND a re-drive in a real browser (or on the wire, for a server-side defect).**

A proof asserted rather than executed is worth **less** than none, because it reads as evidence.

### RULE 2 — Fixing the reported call site is not fixing the CLASS.

The back-dated cutoff was declared fixed **four times**. Each time a re-drive found another door: loose
date *spelling* → *nested* body → *non-JSON* body → the *stored* date → the JSON key's *case* → *trailing
bytes*. **Every one had the same root cause: an authorization peek re-implementing a decoding rule that
lives in `encoding/json`.** Read `HANDOFF-PARITY-AUDIT.md` §7.2 before you touch authorization code.

> **Before you fix: enumerate every door. Then say, in your report, which doors you checked — including
> the ones you found SAFE and why.** Rounds 1–4 each asserted a *list* of hostile inputs and the next
> bypass was always off-list; what finally worked was asserting the **property** (a peek may never be
> looser than the handler) and a **router walk** that fails if a new route goes unaccounted for.

### RULE 3 — ⛔ NEVER SHIP AN UNREACHABLE FIX. It is worse than shipping none.

An agent built an entire server-side claim/release mechanism in insightd and was killed before wiring the
client. The routes were **dead code**, the race was **100% open**, and the invariant suite was **red** —
while the work *looked* done. **If your fix needs an edit in a file you do not own, STOP and report it;
never leave the reachable half unbuilt.** Every lane must ask: *what keystroke reaches this code?*

### RULE 4 — The oracle rule. Never assert a total.

This repo has been burned three times by a flattering oracle: a quantity-only stock check stayed green
while 1,000 of stock VALUE was written off; a `bill_allocation` check passed on half a read while an ALTER
destroyed references; a test reasoning explicitly about orientation still pinned OUTPUT tax on a purchase.

> **A reversed voucher balances to the same paisa. Assert SIDES and LEDGER NAMES, and read VALUE as well
> as quantity.** ⛔ **And a 403 that still WROTE is this repo's signature failure — never let a status code
> be the whole assertion. Read the books back.**

### RULE 5 — One owner per file. Lanes REPORT the edit they need; they do not make it.

⛔ Permanently single-owner: `VoucherScreen.tsx`, `AppShell.tsx`, `FeaturesScreen.tsx`,
`corpora/validate.py`, `seed-demo.mts`, and every `HANDOFF*.md` / `PARITY-*.md`.
⛔ **Only ONE lane runs DB-backed Go tests at a time** where they contend; one Postgres on :5433 is shared.

---

## 2. ⛔ THE ORCHESTRATION PATTERN — COPY THIS, IT IS MEASURED

Use **dynamic workflows** (the `Workflow` tool) for every wave. The cycle is four steps and the order is
load-bearing:

```
   WAVE A: EDIT (parallel, N lanes, disjoint files)      no browsers, no restarts, no `make verify`
      |    each lane: read §7.1 rows -> fix -> unit test -> EXECUTED mutation proof
      v
   REBUILD + RESTART (orchestrator ONLY, serialised)     then VERIFY BY SYMBOL (§0.1)
      |
      v
   WAVE B: RE-DRIVE (parallel, N lanes, own tenants)     no edits at all
      |    each lane: drive the browser/wire -> read the DB oracle -> verdict + control
      v
   GATES (alone) -> mark §7.1 rows -> commit
```

⛔ **Why the waves must not overlap:** an edit wave HMR-reloads vite constantly (any browser result then is
worthless), and a cored restart rotates the signing key (every in-flight agent 401s mid-probe and its
evidence is destroyed).

### 2.1 Concurrency, and what it costs

- The workflow cap is `min(16, cores-2)` — **6 concurrent on this 8-core machine**. Pass 12 lanes and 6
  run while 6 queue; that is fine and is the intended use.
- **Each agent must seed its OWN tenant** and take the id from **its own seed log**:
  ```bash
  cd frontend && pnpm seed:demo --insightd > /tmp/seed-<lane>.log 2>&1
  CO=$(grep -o 'company id: [0-9a-f-]*' /tmp/seed-<lane>.log | head -1 | awk '{print $3}')
  ```
  ⛔ **NEVER let an agent read `frontend/.demo-company-id`** — every seed rewrites it, so with N agents it
  points at somebody else's tenant. Seeding is ~1s; isolation is free.
- ⚠️ An **EMPTY `?company=`** bootstraps a fresh empty "My Company" on which every screen looks clean. Two
  rows were once wrongly logged as passing that way. `openApp` refuses an empty id and asserts the chrome
  names "Demo Traders" — **do not remove those guards.**

### 2.2 ⛔ TWO PIECES OF GLOBAL STATE THAT WILL BREAK YOUR WHOLE FLEET

1. **WebAuthn credentials are per-SUBJECT and every fresh browser context enrols one more.** The cap is
   now 64 offered / 128 retained, so this is fixed — but if writes across the fleet start failing with
   **"PASSKEY STEP-UP FAILED"**, check before diagnosing anything else:
   ```sql
   select count(*) from webauthn_credential c join app_user u on u.id=c.user_id
    where u.subject='browser-dev-owner';
   ```
2. **Account lockout is keyed on SUBJECT, not company** (`auth_login.go`, `app_user.locked_until`). ⛔ **No
   agent may ever submit a wrong password for `browser-dev-owner`** — it would lock the subject every
   other agent signs in as. Lockout work runs **ALONE**, on throwaway subjects, and restores what it locks.

### 2.3 ⛔ AGENTS DIE. PLAN FOR IT.

A session limit killed three lanes mid-edit last pass, leaving a half-built mechanism in the tree.
**After any wave, before believing anything:**

```bash
git status --porcelain                 # did a dead lane leave files you did not expect?
grep -rn "MUTATION" <files touched>    # a mutation marker left behind = a live sabotage
cd backend && go build ./... && go test ./internal/<touched>/... -count=1
cd frontend && pnpm exec tsc --noEmit && pnpm vitest run <touched>
cd insightd && .venv/bin/python -m pytest tests/ -q
```

⛔ **A green targeted run is not enough — a lane that ran only its own test file left the full frontend
suite red last pass** (a module-level accept-once memo made a second test in another file fail). Run the
full gate before you believe a wave.

### 2.4 The prompt skeleton that worked

Give every lane, verbatim: its **file ownership list** and the instruction to stop rather than edit
outside it · **no browsers / no restarts / no `make verify`** in an edit wave · the **§7.1 row numbers**
it owns · RULES 1–4 above · the **traps** in §4 · and a **structured output schema** demanding
`what_changed`, `why_this_closes_the_CLASS`, `tests_added`, `mutation_proof` (with predicted-vs-observed),
and `deliberately_not_closed`. That last field is what stops a lane silently half-fixing something.

---

## 3. ⛔ THE LANES — 72 DEFECTS, GROUPED SO FILE OWNERSHIP IS DISJOINT

Ordered by **how much of a real business's day is blocked**, this repo's standing criterion. Row numbers
are the audit rows; `D<n>` are `HANDOFF-PARITY-AUDIT.md` §7.1 register rows. **Read your lane's register
rows first — they carry the file:line and a proposed fix.**

### WAVE 1 — money and trust (run these six first)

| Lane | Owns | Rows | ~n | The one thing not to get wrong |
|---|---|---|---|---|
| **L1 bill-wise** ⚠️ **PART DONE** | `internal/ledger/ledger.go`, `screens/reports/ReportScreens.tsx`, `screens/vouchers/BillAllocationPicker.tsx` — ⛔ **`internal/reporting/outstanding.go` IS DONE, do not re-open it** | 3, 5 | 4 | ✅ **D12, D65 and half of D13 are closed (§7.4 F8).** ⛔ **WHAT IS LEFT IS THE STAMP (D10/D11), AND THE CLIENT HALF IS THE HARD PART:** `BillAllocationPicker` offers a name field for AGAINST **only**, so a NEW reference CANNOT BE NAMED FROM THE KEYBOARD AT ALL — the server's stamp is the only namer. Any fix that refuses a duplicate is therefore UNREACHABLE (RULE 3) until that input exists. ⛔ **And D13's payable half is a BUILD:** `REPORT_NAMES`' own Order Book note forbids a `?kind=` toggle, because the PDP decides on the report NAME. Original note follows | ⛔ **THREE of row 3's four highs collapse into ONE place**: `ledger.go:1754-1756` stamps the voucher number onto every unnamed reference with no uniqueness check, so two NEW refs merge into one bill AND an on-account payment nets against an unrelated invoice whose number happens to match. Fix the stamp, not the symptoms. Then `outstanding.go` honours `as_on` for AGEING but not for INCLUSION |
| **L2 wrong tenant + draft residual** | `app/CompanyContext.tsx`, `screens/vouchers/draftAccept.ts` | 33, 30 | 4 | §7.3 **C/D**: a malformed `?company=` silently opens somebody else's books; a ghost uuid wedges forever (cored 403s where the code expects 404). §7.3 **B**: the draft CONFIRM is fire-and-forget, so a network failure after a successful post loses the only durable acceptance record — **one bill, two vouchers, reproduced in a browser** |
| ~~**L3 cutoff residual**~~ | — | 11 | 0 | ✅ **CLOSED — §7.3 A is fixed and driven on the wire (§7.4 F7).** Both ends judged, the refusal names the operator's own field, and the class now has a structural gate: every date-shaped json key on every wire type in package `server` must be classified, and every "judged" claim is EXECUTED. Read the box at the top of this file before writing another peek |
| **L4 TDS** | `internal/masters/tds_challan.go`, `internal/server/server.go` (challan routes only), `api/types.ts` | 17 | 6 | ⛔ **Establish the missing allocate-later ROUTE first — it decides everything else in the lane.** Then: nothing compares a challan's allocations against the tax actually deposited, so ₹1 was accepted as having paid ₹27,000; and a TDS/TCS deduction cannot be keyed from the keyboard at all |
| **L5 NL query** | `internal/query/translate.go`, `internal/query/whitelist.go` | 31 | 4 | Three of the four are in one keyword switch: "last month/quarter/year" silently answer for the CURRENT period; "receivables" misses `ledger_opening_bill` so the answer does not match the report; "total sales" excludes GST and credit notes with no disclosure. ⛔ A cross-tenant question is not refused — it returns the caller's own data silently |
| **L6 voucher form** | `screens/VoucherScreen.tsx` **(single-owner — this lane owns it alone)** | 9, 17, 40 | 4 | `Ctrl+A` on a refused voucher **creates every ledger the operator named before the server refuses**, leaving orphan masters; printed Receipt/Payment name the BANK as counterparty; a GST invoice saves but cannot print because HSN is never required |

### WAVE 2 — output, inventory, security

| Lane | Owns | Rows | ~n | The one thing not to get wrong |
|---|---|---|---|---|
| **L7 printing** | `screens/printTarget.ts`, `internal/server/handler_print_reports.go`, `internal/server/handler_print.go`, `internal/printing/printingdb/document.go`, `screens/reports/TDSRegisterScreen.tsx`, `screens/vouchers/StockVoucherScreen.tsx` | 40, 42 | 14 | Mostly cheap and independent: `Ctrl+P` ignores the F2 period on EVERY report; two screens advertise `Ctrl+P` on the button bar with the key **dead**; a Debit Note can never print at all. ⛔ Oracle: a real PDF (`%PDF`…`%%EOF`) that **names itself** — extract the text, and drive **more than one class** |
| **L8 orders + BOM** | `screens/OrderRegisterScreen.tsx`, `screens/vouchers/FulfilmentPicker.tsx`, `screens/reports/OrderBookScreens.tsx`, `internal/reporting/orderbook.go`, `internal/orders/{order,bom}.go` | 27 | 7 | Independent and small. Headline: an order's party comes from the ENTIRE chart of accounts (a sales order can be committed to a bank ledger); the fulfilment picker's "oldest first" comment sorts by string so it offers the NEWEST. ⛔ Preserve the invariant **an order moves no stock** |
| **L9 users + security** | `internal/server/auth_login.go`, `internal/authn/lockout*.go`, `internal/server/handler_admin.go`, `auth/LoginScreen.tsx`, `screens/MembersScreen.tsx` — ⛔ **NOT `StockSummaryScreen.tsx`, see the note below** | 33, 34, 38 | 8 | ⛔ **Locking an account does not revoke the sessions it already holds** — a locked subject's held token posted a write and got 201. There is **no unlock affordance anywhere** in the product. Lockout N/window/duration are hardcoded while the plan sells them as configurable. ⛔ **This lane runs its lockout drives ALONE on throwaway subjects** (§2.2) |
| **L10 stock + statements** | `screens/reports/StockSummaryScreen.tsx`, `screens/reports/reportView.ts`, `internal/reporting/{reporting,stock_summary,stock_summary_godown,stock_batch_summary}.go` | 23, 24, 29 | 8 | Closing Stock is invisible in the Balance Sheet's default condensed view and absent from its export; the Balance Sheet's P&L line ignores F2's From date while the P&L honours it; three reports echo the caller's raw `as_on` instead of the date they resolved. ⛔ **DO NOT TOUCH `inventory/valuation.go`'s replay ordering — see §4.1** |
| **L11 AI-native** | `insightd/**`, `screens/BillInboxScreen.tsx`, `internal/server/handler_forecast.go`, `internal/forecast/forecast.go` | 30, 32 | 8 | ⛔ **insightd has NO AUTHENTICATION and scopes drafts by the caller-supplied `company_id`, and vite proxies it same-origin** — any company_id lists another tenant's supplier names and invoice numbers. That needs a design decision about how insightd is reached; raise it, do not bolt on a token. Row 32: the cash-flow projection has **no door from the keyboard** and discloses neither that it is a projection nor its basis |
| **L12 cost allocation** | `keystroke/useNavMachine.ts`, `screens/vouchers/costAllocation.ts`, `internal/ledger/cost_allocation.go` | 9 | 5 | ⛔ **The headline is the audit's whole question:** the cost-allocation picker cannot be reached from the keyboard at all — Tab, Shift+Tab and Enter move nothing, so it is MOUSE-ONLY while the hint strip advertises otherwise. In a keystroke product that is the defect |

### WAVE 3 — the builds (each is a feature, not a patch)

| Lane | Owns | Rows | The one thing not to get wrong |
|---|---|---|---|
| **L13 masters** | `keystroke/navMachine.ts`, `screens/masters/MasterCreateScreen.tsx` | **20**, 5 | ⛔ **`Alt+C` renders "Create ledger" everywhere; NO keystroke reaches `masterKind` "stock-item" or "unit", so a company can never be given a stock item or a unit from the keyboard**, and the item-invoice empty state points at `Gateway > Masters`, which does not exist. **The route is documented, not invented** (`RESEARCH.md:568`): add **Go To** entries branching in `resolveGotoSelection` — no new `ScreenKind` is needed, `master-create` is already registered. ⛔ **Do not start with the keymap:** `resolveAction` is keyed on `ScreenKind` only and has no notion of the item grid. Same lane fixes row 5: there is **no keyboard path to an opening balance** on any ledger, and nothing anywhere calls `setLedgerOpening` |
| **L14 negative stock** | `internal/server/server.go` (company routes), `screens/FeaturesScreen.tsx` **(single-owner)** | 24 | `allow_negative_stock` is read and honoured by both enforcers and can be written by **nothing** — no route, no request field, no setter, no UI — so the refusal is absolute. `POST /v1/companies` even accepts the key and silently discards it, answering 201. Build the writer; F11's claim was already made honest |

⚠️ **Every lane carrying a SCREEN needs the 15 registrations (`HANDOFF.md` §7.2)**, including
`corpora/validate.py`'s `KNOWN_SCREENS` (outside `frontend/` entirely) and `printTarget.ts`'s
`REPORT_SLUGS`. ⛔ **`make verify` does not run `tsc`** — they are separate gates.

### 3.1 ⛔ THE FILE LISTS ABOVE ARE **PRIMARY** OWNERSHIP, NOT EXHAUSTIVE — READ THIS BEFORE WAVE 1

The 72 open defects name **80 distinct files** between them, because a register row usually cites its
primary file **plus** a contrast (`X does this, while Y over here does the opposite`). The table above names
each lane's primary files only. **`§7.1`'s register row is authoritative for a defect's real file list.**

⛔ **THE ORCHESTRATOR MUST RESOLVE OWNERSHIP BEFORE LAUNCHING A WAVE, NOT DURING IT.** Two lanes editing one
file is how a fix gets silently lost, and a lane that "just quickly" edits outside its list destroys the
other lane's work with no error anywhere. Procedure, and it takes five minutes:

1. Extract every file named by the register rows in the wave you are about to launch.
2. Any file claimed by two lanes → **give it to exactly one**, and tell the other lane to **REPORT the
   edit it needs** in its `notes`. The orchestrator applies those afterwards, serially.
3. Hand each lane its resolved list verbatim, with "STOP and report rather than edit outside this list".

⚠️ **ONE COLLISION IS ALREADY KNOWN AND RESOLVED, AS THE WORKED EXAMPLE:**
`screens/reports/StockSummaryScreen.tsx` carries defects from **two** lanes — L10's report defects (the
"As at today" label over an unbounded report, the export blind to inward VALUE) and L9's security defect
(the 403 refusal **hardcodes "the Data Entry level"**, so a holder of any other level is told a level they
do not hold is the one denying them, and is pointed at the wrong level's rules).
⛔ **`StockSummaryScreen.tsx` belongs to L10.** L9 must report the 403-text change rather than make it.

**Notable files the lane table does not name, and where they belong** — assign them the same way:

| File | Lane | Its defect |
|---|---|---|
| `screens/reports/DrillRows.tsx` | **L7** | re-opening the Day Book makes `Enter` open a DIFFERENT voucher than the highlighted row (the drill target freezes), so `Ctrl+P` prints or refuses the wrong document |
| `screens/vouchers/BillAllocationPicker.tsx`, `screens/vouchers/billAllocation.ts` | **L1** | the AGAINST row's options only load in settle mode, which is why a Credit Note keyed in this app can never print |
| `screens/QueryScreen.tsx` | **L5** | the NL answer's disclosure surface |
| `screens/BankReconciliationScreen.tsx` | **L7** | `printTarget`'s "select a bank account first" refusal is unreachable — the screen auto-selects one |
| `screens/reports/CostCentreSummaryScreen.tsx` | **L7** | `Ctrl+P` claims `Alt+E` "is not built here either", which is false — `Alt+E` writes a CSV immediately |
| `screens/GatewayScreen.tsx` | **L11** | no Gateway entry reaches the cash-flow projection (row 32 opens from nowhere) |
| `app/App.tsx` | **L2** | `getMe()` is called from exactly one place (the F3 screen), so signing in never claims a pending invite |
| `internal/reporting/daybook.go` | **L7** | the printed Day Book's footer is clipped mid-sentence |
| `insightd/**` (`boundary.py`, `fakes.py`, `pipeline.py`, …) | **L11** | PII reaches the voucher; no auth on the draft API |
| `api/{client,admin,insightd,draft}.ts` | **by report** | shared API surface — whichever lane needs a field claims it **through the orchestrator**, never unilaterally |

---

## 4. ⛔ THE TRAPS. ALL OF THESE HAVE COST REAL TIME.

1. ⛔ **`press("Alt+g")` IS A LIE ON macOS.** Option COMPOSES: a real Mac sends `key:"©"`, never `"g"`.
   **Every Alt+letter binding in the product was once found DEAD on macOS while every browser test
   passed**, because the tests pressed keys a Mac cannot send. Use the harness's `altMac("g")`.
2. ⛔ **`Alt+G` IS NOT THE ONLY DOOR.** The Go To picker is hard-wired to `["Vouchers", ...REPORT_NAMES]`,
   so Cost Centres, both cost summaries, Bank Reconciliation, TDS Challans, both order registers, the BOM
   screen and Users & Roles are **absent from it and present on the GATEWAY MENU**. Use
   `openFromGateway(name)`. **A screen that will not open is not thereby unbuilt.**
3. ⛔ **Verify the running binary by symbol, not by uptime** (§0.1).
4. ⛔ **`psql` as `tally_app` returns zero rows under RLS** — use the `postgres` superuser.
5. ⚠️ **`verify:demo` needs `pnpm seed:demo --insightd`.** Without the flag no Bill Inbox draft is queued
   and the gate reports **30/31 with one FAIL**, which reads exactly like a regression.
6. ⚠️ **`docker ps` can wedge while OrbStack reports Running.** `orb stop && orb start` clears it — but it
   bounces **every** container and `tally_pg` does **not** come back on its own (`docker start tally_pg`).
7. ⚠️ **A test that pins a defect is part of the defect.** Two were found: one asserted a no-email invite
   ends at `status='sent'` (the 41-row lie written down as an expectation), and `buttons.test.ts` could
   only ever check the converse. **If a test blocks your fix, read it — it may be the bug.**

### 4.1 ⛔ DO NOT FIX THESE THREE. THE REASON IS SCOPE, NOT OVERSIGHT.

| Item | Why it is deferred |
|---|---|
| **Closing stock replayed in LEXICAL `voucher_no` order** (`inventory/valuation.go`'s `less`) — voucher numbers are bare integers with an INDEPENDENT counter per type, so on a shared date `"10" < "9"` and two series interleave; an issue keyed after a receipt is costed at the pre-receipt average | It changes the **valuation/movement primitive**, which §5 requires be serialised with **T9 FIFO/LIFO**, and it **MOVES THE BALANCE SHEET**, so it needs a deliberate `verify:demo` re-pin. ⛔ **Do it as part of T9** |
| **A value-neutral stock transfer** still moves value when the operator keys any wrong rate | Same primitive; the real fix is for the server to derive the IN rate from the OUT leg's consumption cost. ⛔ **T9** |
| **Rule 48(4) QR code** | ⛔ **PERMANENTLY UNSHIPPABLE** — the payload is signed with the Income Tax Department's private key. **If any agent reports this done, REJECT IT** |

---

## 5. GATES, AND HOW TO NOT BE FOOLED BY THEM

```bash
make verify                                                   # ALONE. -> exit 0
cd frontend && pnpm exec tsc --noEmit                         # SEPARATE gate
cd backend && go test ./... -count=1 -v > /tmp/census.log 2>&1
grep -c -- '--- PASS' /tmp/census.log; grep -c -- '--- SKIP' /tmp/census.log   # SKIP MUST BE 0
cd frontend && pnpm seed:demo --insightd && pnpm run verify:demo "$(cat .demo-company-id)"
```

⛔ **RUN `make verify` ALONE.** Beside other load it fails two ways that are **NOT** code: `internal/sync`
hits its **600s timeout** (and the timed-out package then trips the residue census), and
`EWayBillScreen.test.tsx` fails on a missing `role="radio"` while passing 3/3 standalone. Both were seen on
2026-08-07, and the timeout was reproduced on a **stashed, unmodified tree** — which is the only reason it
was not mistaken for a regression. **Bisect a red `make verify` against a clean tree before believing it.**

**Baseline to beat (2026-08-08):** backend **2463 / 0 SKIP / 0 FAIL** · frontend **1074 / 69 files** ·
insightd **137** · `tsc` clean · `make verify` **0** · `verify:demo` **33/33**.

⚠️ **Residue:** the audit left **~57 scratch tenants** in the dev DB (every agent seeds its own, and
nothing reaps them). Harmless, pre-existing, and worth a reaper someday — but **do not "clean" it mid-run**,
another lane's oracle may live there.

---

## 6. DEFINITION OF DONE, PER DEFECT

- The fix is written **in the voice of the surrounding code** — this codebase carries heavy comments
  stating WHY and what breaks otherwise. **Where you close a bypass, say that it was DRIVEN**, so the next
  reader knows the door was real and not hypothetical.
- **An EXECUTED mutation proof**, and you state the predicted failure and the observed one and whether
  they matched.
- **A browser (or wire) re-drive**, with **its control** — the case that must still work. A path that
  refuses everything looks identical to a working refusal.
- **`§7.1`'s register row is marked**, and `deliberately_not_closed` says what the fix does NOT cover.
- **The full gates are green**, run alone.
- ⛔ **Nothing half-wired.** If it cannot be reached from a keystroke, it is not done (RULE 3).

> ### ⛔ AND THE LAST WORD ON SPEED
> The fastest session this project has had ran ~30 agents and fixed 22 defects — and it spent that speed
> on **six rounds of the same control**, because each round fixed what it was shown instead of enumerating
> the class. **Parallelism buys you throughput; only RULE 2 buys you finishing.** Fan out wide, but make
> every lane enumerate its doors before it writes a line.
