# HANDOFF — FINISH THE REGISTER: the last 15 defects, in order, with the oracle for each

Written 2026-08-22, after L4 (TDS/TCS) closed the whole lane and a 13-agent read-only fan-out
re-verified every remaining row against the code as it is.

**90 defects registered. 78 closed. 11 open.** (2026-08-22: D82, D77, D80, D84 closed; D89 found+fixed; D90 found, open.) This file is the DRIVER: what to do next, in what
order, what proves it, and what will go wrong. It is meant to be executed top to bottom.

## THE THREE FILES, AND WHICH ONE WINS

| File | Role |
|---|---|
| **this file** | ⛔ **the order, the state, the oracle per row, the traps.** Start here. |
| **`HANDOFF-RECON-LAST-16.md` §3** | ⛔ **the `file:line` ANCHORS.** Deliberately not copied here — two copies drift, and the drifted one always wins for whoever reads it first. |
| `HANDOFF-PARITY-AUDIT.md` §7.1 / §7.5 | the register (its header is the live count) and the drive ledger (append one section per lane) |
| `HANDOFF-CLOSE-THE-REGISTER.md` §4 | ⛔ **the user's four decisions. AUTHORITY, not suggestion.** A session that disagrees is disagreeing with the USER and must ask again, not re-decide. |
| `.claude/HANDOFF.md` | the SESSION handoff — stack commands, gate numbers, the full gotcha list |

---

## 0. THE BAR. Every row owes BOTH proofs.

1. an **executed** mutation proof whose failure was **predicted then observed**, and
2. a drive in a **real browser** (or on the wire, for a server-side defect) against a cored binary
   **verified fresh BY SYMBOL**, with the **books read back** — assert SIDES and LEDGER NAMES,
   never totals.

Then: §7.1 row marked, §7.5 appended, committed. **One lane per commit.**

### ⛔⛔ COMMIT THE FIX **BEFORE** MUTATION-PROVING IT

A proof's own `git checkout --` reverts the file it mutated. On 2026-08-21 that **wiped an
uncommitted fix** which was then committed in its reverted state, and `tsc` failed two commits later
on a prop the file no longer declared. `f48e353` exists purely to obey this rule. It has now been
broken twice. **Commit, then prove.**

### ⛔ A PROBE THAT PASSES FIRST TRY IS THE ONE TO DISTRUST

L4's probe went 38/38 and then 67/67 on its first runs — and was re-run against a deliberately
broken product **35 times**. Four of those runs found live product defects the green suite had
missed. The pattern to copy is `/tmp/proofs-l4-e2e.py`:

```
mutate → (Go: go build + RESTART cored | frontend: sleep 8 for vite HMR) → re-drive → git checkout --
```

⚠️ **3 seconds of HMR was NOT enough** and produced a false *"RED, but not where predicted"*.
⚠️ **A Go edit is invisible to a running binary.** Without the restart the proof reports STILL GREEN
and you conclude the probe is not an oracle.

### ⛔ WHAT "AN ORACLE" MEANS HERE

Never a status code. Never a green suite. Never the screen re-reading its own claim. A real oracle
is **a number moving, or a row appearing, read back through a surface the change does not own.**
L4's was the challan's unallocated balance falling `5000.0000 → 0.0000` on the register the screen
re-read for itself — and mutation E1 proved that oracle load-bearing by removing the re-read while
leaving the 200, the success notice and the stored rows intact.

---

## 1. THE ORDER

Cost-ordered, with a reason per position. **Positions 1–5 are done; D5+D43 (position 6) is next, as ONE commit.**

| # | Row(s) | State | Why here |
|---|---|---|---|
| ~~1~~ | ~~D72~~ | ✅ **CLOSED** | was stale — the money half was already fixed and gated |
| ~~2~~ | ~~D82~~ | ✅ **CLOSED 2026-08-22, 12/12** | working-as-intended per the user. ⛔ **its drive found D89 — a refused alter minted a DUPLICATE invoice — now fixed** |
| ~~3~~ | ~~D77~~ | ✅ **CLOSED 2026-08-22, 8/8** | all three sites carry `, p.id`. ⛔ **the recon's proposed oracle (VACUUM ANALYZE) DOES NOT WORK — it takes a HEAP REWRITE**; and the mutation proof KILLED the cost-split claim |
| ~~4~~ | ~~D80~~ | ✅ **CLOSED 2026-08-22, 17/17** | one arm, mirroring the gateway's. ⛔ **the guard written to make reachability structural is what let it ship** — a string comparison, now a drive |
| ~~5~~ | ~~D84~~ | ✅ **CLOSED 2026-08-22** | the instrument is landed (`pnpm contract:cored` + a vitest gate). ⛔ **the split criterion said >10 files; measured ZERO from the first arm and ONE from all eight.** It found 5 tests that could never have run against cored, and a NEW row (D90) |
| 6 | **D5 + D43** | open | **ONE commit** — D43 is D5's symptom and disappears when the field is honoured |
| 7 | **D87** | open | ⛔ needs a **scope decision from the user** before code is written |
| 8 | **D22 + D52** | open | **ONE commit** — a door without the sentence, or the sentence without the door, is each worse than today |
| 9 | **D81** | open | after the VoucherScreen churn from 6–8 settles |
| 10 | **D49** | open | largest scope of the wave. **Before D85**, so D85 lands on the final transport |
| 11 | **D85** | open | **moves a pinned number.** ALONE |
| 12 | **D8** | open | ⛔ **moves the BALANCE SHEET.** ALONE, with a release note |
| 13 | **D86** | open | **moves Outstandings** and inverts a Go test by design. ALONE |
| 14 | **D83** | open | ⛔ the register's proposed index **WILL NOT WORK.** Last |

**Must be ONE commit:** D5+D43, D22+D52. **Must be ALONE:** D84, D85, D8, D86, D83.

---

## 2. PER ROW — state, the change, THE ORACLE

⛔ **Anchors are in `HANDOFF-RECON-LAST-16.md` §3, numbered to match.** Search for the SYMBOL, never
jump to the line: HEAD moved three times during that recon and rows 6–9 will churn
`VoucherScreen.tsx` further.

### ~~2 · D82 — the HSN refusal on the ALTER path~~ · ✅ CLOSED 2026-08-22 · RECON §3.2
**✅ WORKING AS INTENDED — the user's decision, 2026-08-21.** `HSN_REQUIRED_CLASSES` stays exactly
as it is. **Done:** three tests pin the SENTENCE (`VoucherScreen.gstAlter.test.tsx`), mutation-proved
— disabling the condition turns this pin AND `masterGate.test.tsx`'s D60 arm red.
**✅ DONE 2026-08-22:** `drive-l14-cheap.mjs` runs **12/12**, §7.1 is marked working-as-intended
naming the user's decision, and §7.5 carries the lane.
⛔⛔ **AND THE DRIVE FOUND D89, A HIGH-SEVERITY DUPLICATE-INVOICE DEFECT — NOW FIXED.** Pressing
Ctrl+A **twice** — refused, then again after keying the code the refusal asked for — POSTed a
**second invoice** (201, "Saved") and left the original's `hsn` empty. `navMachine`'s `accept`
reducer replaced any voucher frame with a fresh `create` frame, so a refused alter silently became a
create. Fixed: an alter frame stays put. ⛔ **THE LESSON FOR EVERY ROW BELOW: no test in this repo
had ever pressed the accept key twice on one frame.** All three D82 pins press it once. Ask what a
second press does.
**ORACLE:** on the alter the status line carries *"Rule 46(g)"* **and** *"cannot be printed"* while
the HSN field sits empty and keyable **on the same screen**; key the code, Ctrl+A, it goes through;
`alterSalesVoucher` was never called on the refused attempt.
**Traps:** two `role="status"` spans exist — a bare `getByRole("status")` throws. The create path
refuses a blank-HSN sale (that is D60), so the legacy invoice must be **seeded over the wire**.

### ~~3 · D77 — non-total posting order at three sites~~ · ✅ CLOSED 2026-08-22 · RECON §3.3
`ORDER BY p.dr_cr DESC, p.amount DESC` is not a total order, so two legs of equal amount on the same
side come back in an arbitrary order. **Determinism, not data loss.**
**✅ DONE 2026-08-22, 8/8** — `frontend/scripts/drive-l14b-d77.mjs`, one arm per site, on the wire.
⛔⛔ **TWO THINGS HERE THAT THE NEXT ROWS SHOULD INHERIT:**
1. **The stated oracle does not work.** "GET, `VACUUM ANALYZE`, GET again" leaves the untied order
   untouched, as do all four plan toggles and `work_mem=64kB` — the read is an index scan, so ties come
   back in physical heap order and the sort preserves it. It takes a **HEAP REWRITE** (`CLUSTER` /
   `pg_repack`). A probe built on the stated oracle would have gone green on a broken build.
2. **The mutation proof killed the row's headline claim.** `posting_seq` looked positional, so a tied
   swap "should" misattribute a cost split. It does not: `GetVoucher` joins allocations by POSTING_ID
   and re-derives the seq per read. D77 is a PRESENTATION-order defect. `document.go` is an honest
   non-fix (its name key already made a full tie indistinguishable in every printed column).
**Traps that still apply:** D8's drive reads reports whose posting order should now be stable — it is.

### ~~4 · D80 — `Alt+G > Vouchers` pushes the Unwired screen~~ · ✅ CLOSED 2026-08-22 · RECON §3.4
`canonicalReportName("Vouchers")` returns undefined, the `??` falls back to the raw label, and the
Go To pushes a report frame named "Vouchers". **The one entry that has been in that picker since the
beginning is the one that does not work.** Add an arm to `resolveGotoSelection` before the report
fallback, like the master-kind arm above it.
⛔ **DECIDE FIRST, FROM `RESEARCH.md`:** does "Vouchers" push the VOUCHER frame or the DAY BOOK?
⚠️ **The recon's skeptic overturned the reader on the `replaced` flag** — marking a *voucher* frame
`replaced` makes the projection lie while Esc still returns. Do NOT set it; record that as a
deliberate decision.
**✅ DONE 2026-08-22, 17/17** in a browser. `RESEARCH.md:903` answered the question — *"Alt+G >
**Create Voucher** > Ctrl+F4"* — so it pushes the VOUCHER frame; the user did not need asking. `replaced`
is NOT set, pinned by driving Ctrl+G > Vouchers > Alt+C (on the depth-2 stack the flag is unobservable,
so an assertion there would have been vacuous — the recon's skeptic was right).
⛔⛔ **THE LESSON THAT APPLIES TO EVERY ROW LEFT: the guard written to make reachability STRUCTURAL is
what let this ship.** `masterGoto.test.ts` asserted `entry === "Vouchers" || …` — a string comparison
against labels *believed* routable — so the single dead entry in the product was certified reachable by
the test whose entire purpose was to prevent that. **When a row has a structural guard, read what the
guard actually compares.**

### ~~5 · D84 — the mock is looser than cored~~ · ✅ CLOSED 2026-08-22 · RECON §3.5
**This is the false-green root.** `frontend/src/api/mock.ts` accepts bodies cored refuses, and 61
test files build masters through it — `App.tsx`'s dev-client switch means the dev browser does too.
**Eleven confirmed divergences**, listed with both sides in RECON §3.5.

⚠️ **L4 ALREADY CLOSED PART OF THIS SURFACE, so re-derive the list before scoping.** The mock now
serves the allocation `kind`, carries a TCS fixture, and mirrors two refusals it previously did not
(the wrong/absent kind, and `ErrMixedChallanKinds`, which it had been **accepting**). It also carries
the wave's ONE deliberate divergence, declared at the type: `TDSDeductionView`'s derived figures are
omitted because deriving them would put a second copy of `internal/compliance` in the browser bundle.

**The shape:** a `corpora/mock-vs-cored/*.json` corpus of **refusal-shaped** rows (never
value-shaped) plus an explicit `deliberate_divergence` list, and TWO runners over it — vitest against
`createMockClient()`, and a script against live cored.
**ORACLE:** run both runners **BEFORE any fix** and watch the divergences print side by side. The one
that matters is not a status code: an order book with **no** as-on over a passed due date gives mock
`is_overdue:false` / cored `true`. Then fix, watch them agree, then re-loosen ONE mock check and
watch the gate go red **naming exactly that row**.
⛔ **CHEAPEST CHECK BEFORE COMMITTING TO THE FULL ROW:** land only the corpus + the two runners, then
tighten ONE check (`createLedger` empty name → 422) and count the failures. **If that one arm breaks
more than ~10 files, split the row**: "instrument now, tighten per row as each row needs it", and say
so in the register. The instrument is what later rows depend on; the tightening is not.
**Traps:** tightening turns green tests red — **that is the deliverable.** `masters.EnsureStockItem`,
which the mock cites to justify create-or-select, **does not exist**. Encode **422** (not 400) for the
empty ledger name.

### 6 · D5 + D43 — `allow_negative_stock` · RECON §3.6 · ONE COMMIT
Both stock enforcers read the flag and honour it. **Nothing writes it** — no field, no route, no
setter, no control. And `POST /v1/companies` **accepts the key and discards it at 201** (D43), so the
caller is told a setting was applied that never was.
**ORACLE:** flip the flag, drive an issue that takes stock below zero and see it **ACCEPTED**; flip it
back and see the same issue **REFUSED**. ⛔ Read the flag back from the `company` row, **not** from
the response — reading the response is the D43 defect wearing the fix's clothes.
**Traps:** a NEW route means the two-touch rule (router **and** `auth_middleware` routeAction). Check
whether the mock also discards it — that is D84's shape, so do this after D84.

### 7 · D87 — a keyed party has no GSTIN, no state code, no registration type · RECON §3.7
✅ **DECIDED:** GSTIN + state code + registration type on the ledger form, with **`state_code`
MANDATORY under Sundry Debtors/Creditors**. The server has accepted all three since it was written;
the form renders none, so every keyed party lands `(null)|(null)|UNREGISTERED` — and `state_code`
NULL is the exact input that made D19 derive an intra-state bill as IGST.
⛔ **ASK THE USER FIRST:** how many of the party's ten-odd other optional columns (address block, PAN,
credit days, bank details) the form should expose alongside these three. That is a scope question, not
a judgement the code implies.
⚠️ **This REFUSES creations that succeed today, on purpose**, and the refusal must fire **CLIENT-SIDE**
— the server creates before it can refuse.
⛔ **TWO DOORS:** the masters screen AND `Alt+C` from a voucher field. **Drive both.**
**ORACLE:** `verify:demo`'s own checks — "every party's GSTIN prefix == its state_code" and "every
party is registration_type REGULAR" — currently FAIL on a keyed party. They are how this was found and
they are the oracle.

### 8 · D22 + D52 — no cash-flow screen, advertised as `on` · RECON §3.8 · ONE COMMIT
`/cash-flow` and `/anomalies` are live, ACL-mapped and carry their own
`estimate`/`method`/`basis` disclosure — **that disclosure IS register row D23, which is closed, so
the screen MUST render it.** There is no type, no client method, no report name and no screen.
`FeaturesScreen` advertises it as `on` and names no door, while its two AI-native neighbours name
theirs.
**The cheap route is a `report:NAME` frame, not a new `ScreenKind`.**
⚠️ **A report route's JSON shape is NOT guessable** — read the handler. Two attempts died on
`'str' has no attribute 'get'`.
**ORACLE:** a keyboard path reaches the projection in a browser AND the disclosure is on the page.
A door without the corrected sentence leaves the note naming no door; the sentence without the door is
worse. Also fold D72's surviving clause (insightd's forecast has nowhere to render) into D22.

### 9 · D81 — a server refusal leaves masters behind · RECON §3.9
⚠️ **The FORM-side half is already closed** (L6/L12 drives prove Ctrl+A mints nothing when the form
refuses). This is the **SERVER-refusal** half: a cutoff/ACL/duplicate-number/negative-stock refusal
still leaves the ledgers behind, because a posting needs a real uuid and cored has no
`DELETE /ledgers`. The DRAFT path already discloses its orphans via `DraftAcceptFailedError` — **that
is the shape to copy.**
**ORACLE:** provoke a REAL server refusal (a back-dated cutoff is the easiest) and the screen NAMES
the masters it minted. ⚠️ Re-derive anchors at HEAD — the recon's skeptic overturned the reader here,
and rows 6–8 churn the same file.

### 10 · D49 — insightd authenticates nobody · RECON §3.10
⛔ **FAR LARGER THAN THE REGISTER IMPLIES.** insightd authenticates no one and scopes by a path
`company_id`: **7 unauthenticated routes**, and no cored proxy. Scope is 7 proxy routes + the insightd
bind + 2 client transports + the vite proxy + healthz + its test.
**ORACLE:** an unauthenticated call to each route is REFUSED, and a call for another tenant's
`company_id` cannot read it. On the wire, per route.

### 11 · D85 — the duplicate-bill guard keys on the POST-redaction number · RECON §3.11 · ALONE
✅ **DECIDED: key on a PRE-REDACTION HASH.** Every PII-shaped invoice number collapses to the constant
`[PAN REDACTED]`, so two different PAN-shaped numbers from one supplier on one date are the same bill
to the check, and the second accept is refused. Its own advice — *"key its own supplier invoice
number"* — **cannot work**, because any number that redacts lands on the same token.
⛔ **The hash must be taken on the PII boundary's OWN INPUT:** `insightd/insightd/pipeline.py` is the
last place the raw number exists, so the hash travels **with the draft as a field**. That is an
**insightd wire-shape change**, and `pii_boundary_strict` plus M23 guard that boundary. **The refusal's
advice must change with the key.**
**ORACLE:** two DIFFERENT PAN-shaped invoice numbers from one supplier on one date both post. ⚠️ It
**moves pinned numbers** — `test_pii_persistence.py`'s redaction counts and the F9 panel text.
**Predict the moved number OUT LOUD in the commit message before running the gate**; if the gate stays
green, assume the binary was not rebuilt before assuming the probe is not an oracle.

### 12 · D8 — closing stock replayed in LEXICAL voucher_no order · RECON §3.12 · ALONE
An issue keyed after a receipt on the same date is costed at the pre-receipt average. Measured in a
browser: Closing Stock **25,909.3478** where entry order gives **25,307.8797** — 601.4681 too high.
The valuation half is correct; the ORDERING half is wrong, and **no existing fixture can fail on it.**
⛔ **THIS MOVES THE BALANCE SHEET** — closing stock feeds it — so it re-prices real books on the next
report read. Serialise with feature T9 (FIFO/LIFO).
⛔ **CHEAPEST CHECK, BEFORE CHANGING A LINE:** add ONLY the new failing fixture and the new
`valuation_test.go` subcase, and run the full backend + corpora suites. **Exactly one Go subcase and
the new fixture should fail.** If anything else fails, the cost model is wrong and the row needs
re-scoping before any code moves — the whole "afternoon, not a wave" estimate rests on the claim that
no fixture, golden or demo figure holds two differently-numbered vouchers on one date.
**ORACLE:** the same books, replayed in ENTRY order, price the issue at the post-receipt average — and
the figure the browser showed moves to the one entry order gives.

### 13 · D86 — Outstandings bounds at today, the Balance Sheet does not · RECON §3.13 · ALONE
✅ **DECIDED: OUTSTANDINGS STOPS BOUNDING AT TODAY** — deliberately NOT the Balance Sheet, so
`verify:demo` needs no re-pin. One bill dated in the future gave BS **109950.0000** vs Outstandings
**108770.0000**, while `?as_on` in the future made them agree.
⛔⛔ **THREE PLACES MOVE TOGETHER, AND MISSING ONE RE-OPENS D20:** (1) the report; (2)
`internal/query/whitelist.go`'s `EntityOutstandings` skeleton, which D20's fix made MIRROR the report
— `drive-l5-nlq.mjs` will catch it; (3) `QueryScreen`'s `measureBasis` caption, which says
future-dated bills are excluded and becomes **FALSE**.
**ORACLE:** post a future-dated bill; the two reports AGREE with no `as_on`. ⚠️ It **inverts a Go test
by design** — say so in the commit rather than deleting the test.

### 14 · D83 — two concurrent posts share one hand-typed bill number · RECON §3.14 · LAST
⛔ **THE REGISTER'S PROPOSED FIX WILL NOT WORK.** `bill_allocation` cannot carry the index, because
duplicate names are legal for AGAINST / ON_ACCOUNT / caller-named openers. It needs
`pg_advisory_xact_lock` or a new table.
⚠️ **The SEQUENTIAL case — what an operator actually produces — is already closed.** This is the
concurrent one, under READ COMMITTED, and it is the only row in the wave needing a genuinely
concurrent probe.
**ORACLE:** two simultaneous posts of the same bill number on one party — one succeeds, one is
refused. On the wire; there is no mock equivalent for concurrency, and the mock says so at the method.

### ALSO OPEN, OUTSIDE THE REGISTER — §7.3 B, C, D
The durable pending-confirm retry (one supplier bill, two vouchers, reproduced in a browser), and two
`?company=` doors: a **malformed uuid silently opens somebody else's books**, and a well-formed
nonexistent one **wedges permanently**. Do not let these fall off the end — B is a double-post and C
is a tenancy leak.

---

## 3. THE STACK, AND HOW TO KNOW IT IS REAL

```bash
cd /Users/yashbhalgat/code/tally-updated
docker compose up -d db
pg_isready -h localhost -p 5433 -t 10
PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"
# ⛔ kill by PORT OWNER — the compiled child's argv is a temp path, so a command
#    pattern misses it and the stale binary keeps the port while /healthz says 200.
for p in 8087 8088 8091; do pid=$(lsof -nP -iTCP:$p -sTCP:LISTEN | tail -1 | awk '{print $2}'); [ -n "$pid" ] && kill $pid; done
(nohup make run-cored      > /tmp/cored.log      2>&1 < /dev/null &)   # wait for :8087 FIRST
(nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 < /dev/null &)
(nohup make run-insightd   > /tmp/insightd.log   2>&1 < /dev/null &)
cd frontend && (nohup pnpm dev > /tmp/vite.log 2>&1 < /dev/null &)

# ⛔ VERIFY THE RUNNING BINARY BY SYMBOL. 0 = STALE = STOP. A TCP connect is not liveness.
PID=$(lsof -nP -iTCP:8087 -sTCP:LISTEN | tail -1 | awk '{print $2}')
BIN=$(ps -o command= -p $PID | awk '{print $1}')
strings -a "$BIN" | grep -c 'query tds deduction'          # D88's symbol
strings -a "$BIN" | grep -c 'one challan cannot pay both'  # D4's symbol
strings -a "$BIN" | grep -c 'no such route'                # D40's symbol

# SEED, and take the id from the LOG (never .demo-company-id)
cd frontend && pnpm seed:demo --insightd > /tmp/seed.log 2>&1
grep -oE "company id: [0-9a-f-]{36}" /tmp/seed.log | head -1 | awk '{print $3}' > /tmp/drive-company-id
pnpm run verify:demo "$(cat /tmp/drive-company-id)"     # -> ALL CHECKS PASSED (34/34)

# DRIVE — always from the REPO ROOT; the harness resolves against frontend/
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l4-tds.mjs      # 67/67
```

### Gates, measured at this handoff
`pnpm vitest run` **1418 / 86 files** · `pnpm exec tsc --noEmit` **clean** (a SEPARATE gate) ·
`go test ./... -count=1` **0 FAIL** (bracket with a live `psql` — ⚠️ a down database makes every DB
test SKIP and `go test` still exits 0, so the `0 SKIP` count is the load-bearing number) ·
`make verify` **run ALONE** (beside anything else `internal/sync` hits its 600s timeout and
`EWayBillScreen.test.tsx` fails on a missing `role="radio"` while passing standalone).

### ⛔⛔ THE MACHINE — CHECK THIS BEFORE BLAMING THE APP
Load average hit **111** on 2026-08-21 and a browser probe hung for ten minutes. It was **not** the
app: a timed-out Bash call killed the shell and left the probe's `node` + "Google Chrome for Testing"
tree detached and spinning. Reaping it took load **111 → 6.7 in five seconds.**

```bash
ps -Ao pcpu,pid,comm -r | head -12
pkill -9 -f "Google Chrome for Testing"       # orphaned probe browsers
```

⚠️ **The dev database holds 112+ companies.** `seed:demo` mints a NEW one every run and nothing reaps
them, so `GET /v1/me` measures **0.5–3.5s** and the browser bootstrap is occasionally slower than any
fixed wait — `openApp`'s tenant assertion polls to 40s for exactly that reason.
⚠️ **`"No Company"` in the chrome is a LOADING state, not a resolved one** (`AppShell` renders
`company?.name ?? "No Company"` and paints the Gateway underneath). Reaping those tenants is worth
doing but needs a written script: `company` has **14 non-cascading FKs**.

---

## 4. WHAT WILL GO WRONG

**1. The anchors rot faster than the plan is executed.** HEAD moved three times during the recon.
*Check:* `/usr/bin/grep -n "<symbol>" <file>` for the anchor's symbol rather than opening a line.
⛔ **And use `/usr/bin/grep` on `frontend/src/api/mock.ts`** — the shell's `grep` is aliased to ugrep
and returns **silently empty** on that file. It cost four searches across two sessions.

**2. D84 may cost more than it saves, or land too late to help.** If tightening breaks far more than
61 files' worth of triage, positions 6–8 stall behind it and the temptation is to skip it — leaving
D5's, D87's and D22's mock arms all measured through an engine that enforces nothing.
*Check:* the one-arm count above. Split rather than skip.

**3. The number-moving rows may move more than predicted — or nothing at all.** If D85/D86's gate
stays GREEN, assume the binary was not rebuilt and cored not restarted **before** assuming the probe
is not an oracle. *Check:* predict the moved number out loud in the commit message first.

**4. The probe is wrong more often than the screen.** Suspect it first when one assertion fails while
its neighbours pass. From L4: the challan allocation picker **renders only inside the Alt+C form**, not
on the bare register — a probe asserting it one line early reported a live collection as missing. And
⛔ **the Day Book shows the PARTY, not the narration**, when the voucher has one, so a probe grepping
a sales invoice's narration finds nothing; match `"<date> | <no> | <party>"`. **Print what you saw.**

**5. A green suite is not coverage.** L4's four drive-found defects were all shipped behind green
tests, because every test used the one fixture shape that could not expose them (a party without
bill-by-bill; a mock fixture that was always a deduction). When a row's tests pass first try, ask what
shape they all share.

**6. ⛔⛔ NOTHING IN THIS REPO HAD EVER PRESSED THE ACCEPT KEY TWICE ON ONE FRAME.** That is how D89 —
a refused alter POSTing a duplicate invoice — survived a green suite and eleven closed rows on the
same screen. Every D82 pin presses Ctrl+A once; the defect lived in the seam between the refusal and
the retry. *Check:* for every row below whose oracle is a REFUSAL, drive the operator's next
keystroke too. The refusal is not the end of the story — the remedy is.

**7. ⛔ FOR A CLIENT-SIDE MUTATION, VERIFY **VITE**, NOT THE BINARY.** §3's symbol check proves what
cored is running and says nothing about a refusal that never leaves the browser. Curl the module out
of vite and count the mutated construct — and ⚠️ **calibrate the count against `git show HEAD:`**,
because a construct that also appears elsewhere in the same file never reaches zero and the wait
loop then blames HMR for a grep that was wrong.

---

## 5. AFTER THE REGISTER — the user's "once done" phase

`FeaturesScreen.tsx` is the honest ledger. **11 rows read `off`:** T8 multi-currency, T9 FIFO/LIFO,
T10 stock groups + compound units, T11 budgets, T12 GSTR-9, T13 interest, T14 backup/restore/split,
T16 per-company print templates, export-email/PDF, SSO, and ⛔ **T18 rule 48(4) QR, which is
PERMANENTLY unshippable — the payload is signed with the Income Tax Department's private key. If a
session reports T18 done, reject it.** **2 rows read `server`** (the payroll screens — pure frontend,
`internal/payroll` is already in the archtest allowed set — and offline sync).

⛔ **T32 Tally XML import is the COMMERCIAL BLOCKER and has never met a real Tally file. Ask the user
for one early.** Nothing in the repo substitutes for it.

## 6. QUESTIONS THAT NEED THE USER

1. **T32:** a real Tally XML export from a live install. The commercial blocker.
2. **D87's scope:** how many of the party's optional columns the ledger form should carry alongside
   the three that the decision makes near-mandatory.
3. **D80:** does `Alt+G > Vouchers` push the voucher frame or the Day Book? Check `RESEARCH.md` first
   and only ask if it does not say.
4. **D50's fourth arm** stays answered-with-disclosure. Raise again only if the user asks for a
   refusal — the only honest way puts the caller's own party names in the prompt, reversing the
   current safety posture.

## 7. ON PARALLELISM — asked and answered

The user asked why this is not parallelised. **Reading fans out; driving does not.**
`HANDOFF-RECON-LAST-16.md` was produced by 13 agents in ~32 minutes and is why every row above has an
anchor and an oracle. But the drives and mutation proofs share ONE Postgres, ONE cored, ONE vite and
ONE tenant, and a proof **edits a file, rebuilds the binary and restarts it** — two agents doing that
concurrently manufacture false green for each other. Fan out for recon and for the feature phase.
Never for a drive.
