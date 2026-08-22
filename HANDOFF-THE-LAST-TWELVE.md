# HANDOFF — THE LAST TWELVE: the register's remaining rows, in order, with the oracle for each

Written 2026-08-22, after L14/b/c/d closed **D82, D77, D80 and D84**, found and fixed **D89**, and
found **D90**. This supersedes `HANDOFF-FINISH-THE-REGISTER.md`, whose positions 1–5 are struck
through. It is the DRIVER: what to do next, in what order, what proves it, what will go wrong.
Execute it top to bottom.

## ⛔ THE COUNT, RE-DERIVED BY PARSING §7.1 — NOT COPIED FROM A HEADER

**90 registered · 74 CLOSED · 12 OPEN · 4 PARTIAL.** `74 + 4 + 12 = 90`.

The previous header said "11 open" and was wrong twice: **D90 was registered and never counted**, and
**four `PARTIAL` rows have been counted as closed by every session since F6/F8**. Re-parse the table;
never restate this from memory. The one-liner that did it:

```bash
python3 - <<'PY'
import io,re
s=io.open("HANDOFF-PARITY-AUDIT.md",encoding="utf-8").read().split("\n")
a=[n for n,l in enumerate(s) if l.startswith("## 7.1")][0]
b=[n for n,l in enumerate(s) if n>a and l.startswith("### ⛔⛔ §7.2")][0]
for l in s[a:b]:
    m=re.match(r'^\| \*?\*?(D\d+)\*?\*? \|', l)
    if m: print(m.group(1), "CLOSED" if "✅" in l else "PARTIAL" if "PARTIAL" in l else "OPEN" if "OPEN" in l else "?")
PY
```

**OPEN (12):** D5 · D8 · D22 · D43 · D49 · D52 · D81 · D83 · D85 · D86 · D87 · D90
**PARTIAL (4), status genuinely ambiguous:** D13 · D15 · D17 · D48 — see §5.

---

## THE FILES, AND WHICH ONE WINS

| File | Role |
|---|---|
| **this file** | ⛔ **the order, the state, the oracle per row, the traps.** Start here. |
| **`HANDOFF-RECON-LAST-16.md` §3** | ⛔ **the `file:line` ANCHORS.** Deliberately NOT copied here — two copies drift and the drifted one wins for whoever reads it first. ⚠️ **Use it to find the CODE, never to fill in an EXPECTED VALUE** — see §4.1. |
| `HANDOFF-PARITY-AUDIT.md` §7.1 / §7.5 | the register (re-parse it; the header has been wrong) and the drive ledger (append one section per lane) |
| `HANDOFF-CLOSE-THE-REGISTER.md` §4 | ⛔ **the user's four decisions. AUTHORITY, not suggestion.** A session that disagrees is disagreeing with the USER and must ask again, not re-decide. |
| `HANDOFF-FINISH-THE-REGISTER.md` | ⚠️ the PREVIOUS driver. Positions 1–5 are done. Kept for its §2 per-row detail on rows still open. |
| `.claude/HANDOFF.md` | the SESSION handoff — stack commands, gate numbers, the full gotcha list |

---

## 0. THE BAR. Every row owes BOTH proofs.

1. an **executed** mutation proof whose failure was **predicted then observed**, and
2. a drive in a **real browser** (or on the wire, for a server-side defect) against a cored binary
   **verified fresh BY SYMBOL**, with the **books read back** — assert SIDES and LEDGER NAMES,
   never totals.

Then: §7.1 row marked, §7.5 appended, committed. **One lane per commit.**

### ⛔⛔ COMMIT THE FIX **BEFORE** MUTATION-PROVING IT

A proof's own `git checkout --` reverts the file it mutated. On 2026-08-21 that wiped an uncommitted
fix which was then committed in its reverted state. `f48e353` exists purely to obey this rule. It has
been broken twice. **Commit, then prove.** (Held four times in L14a–d without incident.)

### ⛔ A PROBE THAT PASSES FIRST TRY IS THE ONE TO DISTRUST

L14 is now the strongest evidence for this rule in the repo. In ONE session:

- **D82's suite was green THROUGH a duplicate-invoice defect** (D89) because all three of its pins
  press Ctrl+A exactly *once*.
- **D80's probe passed while asserting nothing about the mode** — its evidence string was sliced to 80
  chars, cutting off the only word that distinguished the two frames.
- **D77's probe passed while comparing side and amount only** — it read `p.ledger_name`, the wire field
  is `p.ledger`, so every leg projected `undefined` and the one thing a swap changes was dropped. 7/7,
  vacuous.
- **D84's first-run oracle worked and its headline claim was false** (see §4.2).

**Print the field you are asserting on, not a prefix of the page. Make the probe THROW on an
`undefined` projection rather than compare it.**

### ⛔ WHAT "AN ORACLE" MEANS HERE

Never a status code. Never a green suite. Never the screen re-reading its own claim. A real oracle is
**a number moving, or a row appearing (or NOT appearing), read back through a surface the change does
not own.** L14's best: the Day Book's entry count over one date holding still while `op` flips
`CREATE → ALTER` — a surface neither the form nor the reducer owns.

---

## 1. THE ORDER

Cost-ordered, continuing the previous driver's reasoning. **D5+D43 is next.**

| # | Row(s) | Sev | Why here |
|---|---|---|---|
| 1 | **D5 + D43** | HIGH | **ONE commit** — D43 is D5's symptom and disappears when the field is honoured. First because D84's gate now exists to measure the mock arm honestly. |
| 2 | **D87** | MED | ✅ scope DECIDED 2026-08-22 (§6.2): the three + PAN + credit days. Unblocked. |
| 3 | **D90** | MED | ✅ DECIDED 2026-08-22 (§6.3): refuse now. Cheapest row in the wave. |
| 4 | **D22 + D52** | HIGH | **ONE commit** — a door without the sentence, or the sentence without the door, is each worse than today |
| 5 | **D81** | MED | after the VoucherScreen churn from 1 and 4 settles |
| 6 | **D49** | MED | largest scope of the wave. **Before D85**, so D85 lands on the final transport |
| 7 | **D85** | MED | **moves a pinned number.** ALONE |
| 8 | **D8** | HIGH | ⛔ **moves the BALANCE SHEET.** ALONE, with a release note |
| 9 | **D86** | MED | **moves Outstandings** and inverts a Go test by design. ALONE |
| 10 | **D83** | LOW | ⛔ the register's proposed index **WILL NOT WORK.** Last |

**Must be ONE commit:** D5+D43, D22+D52. **Must be ALONE:** D85, D8, D86, D83.
~~**Must be ASKED before code:** D87, D90~~ — ✅ **both answered 2026-08-22, see §6. Nothing in the wave is
waiting on the user any more.**

---

## 2. PER ROW — state, the change, THE ORACLE

⛔ **Anchors are in `HANDOFF-RECON-LAST-16.md` §3, numbered to match its own list.** Search for the
SYMBOL, never jump to the line: HEAD moved five times in L14 alone, and rows 1/4/5 churn
`VoucherScreen.tsx` further.
⛔ **And use `/usr/bin/grep`** — the shell's `grep` is aliased to ugrep and returns **silently empty**
on `frontend/src/api/mock.ts`. It has cost four searches across three sessions.

### 1 · D5 + D43 — `allow_negative_stock` · RECON §3.6 · ONE COMMIT
Both stock enforcers read the flag and honour it. **Nothing writes it** — no field, no route, no
setter, no control. And `POST /v1/companies` **accepts the key and discards it at 201** (D43), so the
caller is told a setting was applied that never was.
**ORACLE:** flip the flag, drive an issue that takes stock below zero and see it **ACCEPTED**; flip it
back and see the same issue **REFUSED**. ⛔ Read the flag back from the `company` row, **not** from the
response — reading the response is the D43 defect wearing the fix's clothes.
**Traps:** a NEW route means the **two-touch rule** (router **and** `auth_middleware` routeAction).
✅ **D84's gate now covers the mock arm**: add the accept/refuse pair to
`corpora/mock-vs-cored/masters.json` and both runners will hold the mock to it — that arm no longer
needs hand-checking.

### 2 · D87 — a keyed party has no GSTIN, no state code, no registration type · RECON §3.7
✅ **DECIDED:** GSTIN + state code + registration type on the ledger form, with **`state_code`
MANDATORY under Sundry Debtors/Creditors**. The server has accepted all three since it was written;
the form renders none, so every keyed party lands `(null)|(null)|UNREGISTERED` — and `state_code` NULL
is the exact input that made D19 derive an intra-state bill as IGST.
✅ **ANSWERED (§6.2): the three + `pan` + `credit_days`. FIVE fields, one mandatory.** Address block,
ITC eligibility, blocked clause and bank details stay out.
⚠️ **This REFUSES creations that succeed today, on purpose**, and the refusal must fire
**CLIENT-SIDE** — the server creates before it can refuse.
⛔ **TWO DOORS:** the masters screen AND `Alt+C` from a voucher field. **Drive both.**
**ORACLE:** `verify:demo`'s own checks — "every party's GSTIN prefix == its state_code" and "every
party is registration_type REGULAR" — currently FAIL on a keyed party. They are how this was found and
they are the oracle.

### 3 · D90 — a duplicate STOCK ITEM name is accepted; a duplicate LEDGER name is refused · NEW
Found 2026-08-22 by D84's wire discovery **measuring a recon claim instead of trusting it.** cored
answers `201` and a NEW id for a second stock item with the same name, while a second ledger with the
same name is `422`. Consequence: two identical rows in every item picker with nothing to tell them
apart, and one item's movements and valuation split across two masters, each weighted-average costed
on half the history.
⚠️ **The mock does the OPPOSITE** (create-or-select returns the existing id), so no vitest can see
this. It is recorded as a **declared divergence** in `corpora/mock-vs-cored/masters.json` rather than
mirrored — mirroring cored here would spread the more suspicious of the two behaviours.
✅ **ANSWERED (§6.3): REFUSE NOW, naming the existing item.** The T10 relaxation
(`UNIQUE (company_id, name)` → `UNIQUE (company_id, group_id, name)`) is a WIDENING migration that
cannot fail on existing data — cheap later; un-splitting half-costed valuation history is not.
**ORACLE:** on the wire, two items named the same → one succeeds, one is refused naming the existing
item; and the item picker shows ONE row. ⚠️ Then the corpus's `deliberate_divergence` entry must be
**deleted and replaced by a real row**, or the gate still certifies the old behaviour.

### 4 · D22 + D52 — no cash-flow screen, advertised as `on` · RECON §3.8 · ONE COMMIT
`/cash-flow` and `/anomalies` are live, ACL-mapped and carry their own `estimate`/`method`/`basis`
disclosure — **that disclosure IS register row D23, which is closed, so the screen MUST render it.**
There is no type, no client method, no report name and no screen. `FeaturesScreen` advertises it as
`on` and names no door, while its two AI-native neighbours name theirs.
**The cheap route is a `report:NAME` frame, not a new `ScreenKind`.**
⚠️ **A report route's JSON shape is NOT guessable** — read the handler. Two attempts died on
`'str' has no attribute 'get'`.
✅ **D80 made `Alt+G` honest, so the picker is now a usable oracle surface for this row** — that was
the reason D80 was sequenced first, and it is done.
**ORACLE:** a keyboard path reaches the projection in a browser AND the disclosure is on the page.
A door without the corrected sentence leaves the note naming no door; the sentence without the door is
worse. Also fold D72's surviving clause (insightd's forecast has nowhere to render) into D22.

### 5 · D81 — a server refusal leaves masters behind · RECON §3.9
⚠️ **The FORM-side half is already closed** (L6/L12 drives prove Ctrl+A mints nothing when the form
refuses). This is the **SERVER-refusal** half: a cutoff/ACL/duplicate-number/negative-stock refusal
still leaves the ledgers behind, because a posting needs a real uuid and cored has no
`DELETE /ledgers`. The DRAFT path already discloses its orphans via `DraftAcceptFailedError` — **that
is the shape to copy.**
**ORACLE:** provoke a REAL server refusal (a back-dated cutoff is the easiest) and the screen NAMES
the masters it minted.
⚠️ Re-derive anchors at HEAD — the recon's skeptic overturned the reader here, and rows 1 and 4 churn
the same file. ⛔ **Check D48 first (§5): it may already be this row's draft-path twin, closed.**

### 6 · D49 — insightd authenticates nobody · RECON §3.10
⛔ **FAR LARGER THAN THE REGISTER IMPLIES.** insightd authenticates no one and scopes by a path
`company_id`: **7 unauthenticated routes**, and no cored proxy. Scope is 7 proxy routes + the insightd
bind + 2 client transports + the vite proxy + healthz + its test.
**ORACLE:** an unauthenticated call to each route is REFUSED, and a call for another tenant's
`company_id` cannot read it. On the wire, per route.

### 7 · D85 — the duplicate-bill guard keys on the POST-redaction number · RECON §3.11 · ALONE
✅ **DECIDED: key on a PRE-REDACTION HASH.** Every PII-shaped invoice number collapses to the constant
`[PAN REDACTED]`, so two different PAN-shaped numbers from one supplier on one date are the same bill
to the check, and the second accept is refused. Its own advice — *"key its own supplier invoice
number"* — **cannot work**, because any number that redacts lands on the same token.
⛔ **The hash must be taken on the PII boundary's OWN INPUT:** `insightd/insightd/pipeline.py` is the
last place the raw number exists, so the hash travels **with the draft as a field**. That is an
**insightd wire-shape change**, and `pii_boundary_strict` plus M23 guard that boundary. **The
refusal's advice must change with the key.**
**ORACLE:** two DIFFERENT PAN-shaped invoice numbers from one supplier on one date both post.
⚠️ It **moves pinned numbers** — `test_pii_persistence.py`'s redaction counts and the F9 panel text.
**Predict the moved number OUT LOUD in the commit message before running the gate**; if the gate stays
green, assume the binary was not rebuilt before assuming the probe is not an oracle.

### 8 · D8 — closing stock replayed in LEXICAL voucher_no order · RECON §3.12 · ALONE
An issue keyed after a receipt on the same date is costed at the pre-receipt average. Measured in a
browser: Closing Stock **25,909.3478** where entry order gives **25,307.8797** — 601.4681 too high.
The valuation half is correct; the ORDERING half is wrong, and **no existing fixture can fail on it.**
⛔ **THIS MOVES THE BALANCE SHEET** — closing stock feeds it — so it re-prices real books on the next
report read. Serialise with feature T9 (FIFO/LIFO).
⛔ **CHEAPEST CHECK, BEFORE CHANGING A LINE:** add ONLY the new failing fixture and the new
`valuation_test.go` subcase, and run the full backend + corpora suites. **Exactly one Go subcase and
the new fixture should fail.** If anything else fails, the cost model is wrong and the row needs
re-scoping before any code moves.
✅ **D77 is done, so posting order is now stable** — this row's drive reads reports whose leg order no
longer wobbles, which is why D77 was sequenced ahead of it.
**ORACLE:** the same books, replayed in ENTRY order, price the issue at the post-receipt average — and
the figure the browser showed moves to the one entry order gives.

### 9 · D86 — Outstandings bounds at today, the Balance Sheet does not · RECON §3.13 · ALONE
✅ **DECIDED: OUTSTANDINGS STOPS BOUNDING AT TODAY** — deliberately NOT the Balance Sheet, so
`verify:demo` needs no re-pin. One bill dated in the future gave BS **109950.0000** vs Outstandings
**108770.0000**, while `?as_on` in the future made them agree.
⛔⛔ **THREE PLACES MOVE TOGETHER, AND MISSING ONE RE-OPENS D20:** (1) the report; (2)
`internal/query/whitelist.go`'s `EntityOutstandings` skeleton, which D20's fix made MIRROR the report
— `drive-l5-nlq.mjs` will catch it; (3) `QueryScreen`'s `measureBasis` caption, which says
future-dated bills are excluded and becomes **FALSE**.
**ORACLE:** post a future-dated bill; the two reports AGREE with no `as_on`. ⚠️ It **inverts a Go test
by design** — say so in the commit rather than deleting the test.

### 10 · D83 — two concurrent posts share one hand-typed bill number · RECON §3.14 · LAST
⛔ **THE REGISTER'S PROPOSED FIX WILL NOT WORK.** `bill_allocation` cannot carry the index, because
duplicate names are legal for AGAINST / ON_ACCOUNT / caller-named openers. It needs
`pg_advisory_xact_lock` or a new table.
⚠️ **The SEQUENTIAL case — what an operator actually produces — is already closed.** This is the
concurrent one, under READ COMMITTED, and it is the only row in the wave needing a genuinely
concurrent probe.
**ORACLE:** two simultaneous posts of the same bill number on one party — one succeeds, one is
refused. On the wire; there is no mock equivalent for concurrency, and the mock says so at the method.

---

## 3. THE STACK, AND HOW TO KNOW IT IS REAL

```bash
cd /Users/yashbhalgat/code/tally-updated
docker compose up -d db
pg_isready -h localhost -p 5433 -t 10
PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"
# ⛔ kill by PORT OWNER — the compiled child's argv is a temp path, so a command
#    pattern misses it and the stale binary keeps the port while /healthz says 200.
#    Kill the go-run PARENT too, or it respawns nothing and you debug a ghost.
for p in 8087 8088 8091; do
  pid=$(lsof -nP -iTCP:$p -sTCP:LISTEN | tail -1 | awk '{print $2}')
  [ -n "$pid" ] && kill "$(ps -o ppid= -p $pid | tr -d ' ')" "$pid"
done
(nohup make run-cored      > /tmp/cored.log      2>&1 < /dev/null &)   # wait for :8087 FIRST
(nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 < /dev/null &)
(nohup make run-insightd   > /tmp/insightd.log   2>&1 < /dev/null &)
cd frontend && (nohup pnpm dev > /tmp/vite.log 2>&1 < /dev/null &)

# ⛔ VERIFY THE RUNNING BINARY BY SYMBOL. 0 = STALE = STOP. A TCP connect is not liveness.
# ⚠️ Poll for the PORT before resolving the pid: `ps -o command= -p ""` prints a USAGE
#    ERROR and the symbol count then reads 0, which looks exactly like a stale binary.
PID=$(lsof -nP -iTCP:8087 -sTCP:LISTEN | tail -1 | awk '{print $2}')
BIN=$(ps -o command= -p "$PID" | awk '{print $1}')
strings -a "$BIN" | grep -c 'query tds deduction'          # D88's symbol
strings -a "$BIN" | grep -c 'one challan cannot pay both'  # D4's symbol
strings -a "$BIN" | grep -c 'p.amount DESC, p.id'          # D77's symbol (want 2)

# SEED, and take the id from the LOG (never .demo-company-id)
cd frontend && pnpm seed:demo --insightd > /tmp/seed.log 2>&1
grep -oE "company id: [0-9a-f-]{36}" /tmp/seed.log | head -1 | awk '{print $3}' > /tmp/drive-company-id
pnpm run verify:demo "$(cat /tmp/drive-company-id)"     # -> ALL CHECKS PASSED

# DRIVE — always from the REPO ROOT; the harness resolves against frontend/
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l14-cheap.mjs    # 17/17 (D82/D89/D80)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l14b-d77.mjs     # 8/8   (D77)
cd frontend && CO="$(cat /tmp/drive-company-id)" pnpm contract:cored           # 11/11 (D84)
```

### Gates, measured at this handoff
`pnpm vitest run` **1418 / 86 files** · `pnpm exec tsc --noEmit` **clean** (a SEPARATE gate) ·
`go test ./... -count=1` **0 FAIL** (bracket with a live `psql` — ⚠️ a down database makes every DB
test SKIP and `go test` still exits 0, so the `0 SKIP` count is the load-bearing number) ·
`pnpm contract:cored` **11/11** (needs live cored + `CO`) ·
`make verify` **run ALONE** (beside anything else `internal/sync` hits its 600s timeout and
`EWayBillScreen.test.tsx` fails on a missing `role="radio"` while passing standalone).

### ⚠️ THE DEV DATABASE IS NOT PRISTINE — L14 CHANGED IT ON PURPOSE
- **`CLUSTER posting` was run** (D77's oracle needs a heap rewrite). Harmless, and the fix makes reads
  deterministic regardless — but it is why the table's physical order is not insertion order.
- **Probe fixtures were left behind**, including the **duplicate invoice pair D89 minted** (narration
  `diag legacy no HSN nk0zb`, twice, both `op=CREATE`). That is the evidence; reap when done with it.
- ⚠️ **112+ companies and nothing reaps them**, so `GET /v1/me` measures **0.5–3.5s** and the browser
  bootstrap is occasionally slower than any fixed wait — `openApp`'s tenant assertion polls to 40s for
  exactly that reason. Reaping needs a written script: `company` has **14 non-cascading FKs**.
- ⚠️ **`"No Company"` in the chrome is a LOADING state, not a resolved one.**

### ⛔⛔ THE MACHINE — CHECK THIS BEFORE BLAMING THE APP
Load average hit **111** on 2026-08-21 and **83** on 2026-08-22, both times from OTHER repos.

```bash
ps -Ao pcpu,pid,comm -r | head -12
pkill -9 -f "Google Chrome for Testing"       # orphaned probe browsers
cat ~/.claude/machine-health/ci-slots/*.owner # who actually holds the CI slots
```

⚠️ **`ci-slot` NAMES A STALE HOLDER, AND DEAD PIDS DO NOT MEAN A WEDGED QUEUE.** The "queuing behind
[pid=…]" line prints ONCE, so the named processes are usually gone by the time you read it. The
semaphore is an `flock`, which the OS releases on death — it **cannot** wedge on a dead holder.
⚠️ **`make run-cored` coming up in "1s" is NORMAL, not suspicious** — the build is cached. Judge the
binary by its build directory and its symbols, never by how fast the port opened.

---

## 4. WHAT WILL GO WRONG

### 4.1 ⛔⛔ A RECON IS FOR FINDING THE CODE, NEVER FOR FILLING IN THE EXPECTED VALUE
D84 measured all eleven of its expectations on the wire instead of trusting `HANDOFF-RECON-LAST-16.md`,
and **two were wrong**: an unknown group answers **404** (the recon said 422), and a duplicate stock
item name is **ACCEPTED** (the recon's premise was right, its conclusion wasn't — that is now D90).
Ten minutes of `curl` overturned 2 of 11. **Every remaining row's "the server answers X" claim is
unverified until you fire it at the server.**

### 4.2 ⛔ THE MUTATION PROOF WILL SOMETIMES CORRECT **YOU**, NOT THE CODE
D77's commit message asserted the defect misattributes a cost split — plausible, traced, and **false**:
`GetVoucher` joins allocations by `posting_id` and re-derives the seq per read, so with the fix
reverted and the array demonstrably reordered, that arm stayed **green**. The commit was amended and
the arm kept as a labelled CONTROL. **Write the claim down before the proof, so the proof can kill it.**

### 4.3 ⛔ THE STATED ORACLE MAY NOT WORK — CHECK THE MECHANISM IS LIVE
D77's prescribed oracle (*"GET, `VACUUM ANALYZE`, GET again"*) does not move a non-total sort. Neither
do four plan toggles or `work_mem=64kB`. It takes a **heap rewrite** (`CLUSTER`/`pg_repack`). A probe
built on the stated oracle would have gone green against a broken build.
**Fix:** if a probe's mechanism is probabilistic, add a **PRECONDITION arm** that proves the mechanism
fired for THIS fixture. D77's runs the old query itself, in both heap states, and requires them to
differ — it reported *"invoice untied: UNCHANGED · journal untied: CHANGED"* on its first run, which is
the arm earning its place.

### 4.4 ⛔ ASK WHAT A **SECOND** KEYPRESS DOES
Nothing in this repo had ever pressed the accept key twice on one frame, and that is where D89 lived —
a refused alter silently became a create, so the remedy the refusal itself prescribed POSTed a
duplicate invoice. **For every row whose oracle is a REFUSAL, drive the operator's next keystroke too.
The refusal is not the end of the story; the remedy is.**

### 4.5 ⛔ WHEN A ROW HAS A STRUCTURAL GUARD, READ WHAT THE GUARD **COMPARES**
D80 shipped a dead picker entry *past* a test called "GOTO_ENTRIES offers exactly what the machine can
route", written by an earlier row precisely to prevent that. It contained `entry === "Vouchers" ||` —
a string comparison against a label *believed* routable. Two of its three clauses checked the machine's
real routing tables; the third certified the one broken entry.

### 4.6 ⚠️ A PROBE THAT READS THE WRONG FIELD NAME PASSES VACUOUSLY
D77's probe read `p.ledger_name`; the wire field is `p.ledger`. Every leg projected `undefined`, so
"the array is element-identical" compared **side and amount only** — the two things equal by
construction in a tie. 7/7, asserting nothing. **Make the probe THROW on an `undefined` projection.**

### 4.7 ⚠️ FOR A CLIENT-SIDE MUTATION, VERIFY **VITE**, NOT THE BINARY
The §3 symbol check proves what cored is running and says nothing about a refusal that never leaves the
browser. Curl the module out of vite and count the mutated construct — and **calibrate against
`git show HEAD:<file> | grep -c`**, because a construct that also appears in COMMENTS never reaches
zero. This trap fired **twice in one session**: expected 0, actual 2 at HEAD-minus-fix and 3 at HEAD.

### 4.8 ⚠️ TWO CLIENT/AUTH TRAPS THAT COST REAL TIME IN L14d
- **`createRestClient`'s auth option is `headers`, not `getAuthHeaders`, and a wrong key is silently
  ignored** — `HttpClientOptions` merges a STATIC map, there is no per-request hook, and tsc cannot
  catch it (the options type is a union). Symptom: `401 missing bearer token` on every row.
- **`auth.mts`'s `authHeaders()` mints for a fresh RANDOM subject** (`e2e-owner-<uuid>`) — right for a
  script that creates its own company, wrong for one handed an existing tenant. Mint for
  `browser-dev-owner`, **once per run**.

### 4.9 ⚠️ THE NUMBER-MOVING ROWS MAY MOVE MORE THAN PREDICTED — OR NOTHING
If D85/D86/D8's gate stays GREEN, assume the binary was not rebuilt and cored not restarted **before**
assuming the probe is not an oracle. *Check:* predict the moved number out loud in the commit message.

### 4.10 ⚠️ A FULL-SUITE FAILURE UNDER LOAD MAY BE A FLAKE — CHECK, DON'T ASSUME
`VoucherScreen.draft.test.tsx` timed out on a `waitFor` at load 80+, passed standalone, and the suite
was green on a re-run. **Structural argument first** (does the test even touch your change?), then the
standalone run, then the re-run. Known members of this class: `internal/sync`, `EWayBillScreen`.

---

## 5. ⛔ THE FOUR `PARTIAL` ROWS — AUDIT BEFORE TRUSTING THE COUNT

These have been counted as CLOSED by every session since F6/F8 and **their status is genuinely
ambiguous**. Resolve them before claiming the register is finished; each is a ten-minute read, not a
build.

| Row | Sev | What is definitely done | ⛔ THE QUESTION TO ANSWER |
|---|---|---|---|
| **D13** | HIGH | the per-bill breakdown is on screen and exported, driven | The **PAYABLE side** is still missing, and it is deliberately NOT a `?kind=` toggle: two directions must be two report NAMES because the PDP decides on the report name and a query parameter is invisible to it. Needs a `Payables` report name + server resource + migration. **This is a BUILD and should probably be its own row, not a partial.** |
| **D15** | HIGH | the claim→post→confirm reorder closed the race it was built for (two browsers 13ms apart → ONE voucher) | Is the residue exactly **§7.3 B** (the durable pending-confirm retry)? If so, say so in the row and stop calling it PARTIAL — the open work is already tracked. |
| **D17** | HIGH | §7.4 records bill-wise references now reaching the wire, driven | **This may simply be CLOSED and never re-marked.** Verify against `draftBodyToVoucherRequest` at HEAD and close it or state what remains. |
| **D48** | MED | — | It is the orphan-master-on-failed-accept defect **on the draft path**, and D81's row says the draft path already discloses its orphans via `DraftAcceptFailedError`. **If that is true, D48 is closed and D81 is the surviving half.** Check before starting D81 (§2 row 5). |

---

## 6. QUESTIONS THAT NEEDED THE USER — ✅ ALL FOUR ANSWERED 2026-08-22 (L15). DO NOT RE-ASK.

1. **T32:** ✅ **ANSWERED — the user has no real Tally file on hand and asked whether one can be
   obtained.** Searched 2026-08-22. **There is no public corpus of genuine full exports from a live
   install.** What GitHub carries (`TallyConnector`, `nodejs-tally-integration`,
   `PHP-to-tally-integration`, `tally-prime-api-docs`) is **hand-authored REQUEST envelopes** — the
   import direction, written by integrators, not dumped by Tally. Those are worth reading for tag
   spelling and **worthless as a fidelity oracle**, because they were never produced by the program
   whose output T32 must parse. ⛔ **Do not let one into the repo as a fixture** — it would imply
   coverage that does not exist, which is the exact failure §0 exists to prevent.
   ✅ **THE REAL PATH: TallyPrime Educational is a free, unlicensed, indefinite download, and it
   exports XML.** Install it, key a handful of masters and vouchers, `Export > Masters` and
   `Export > Day Book` in XML — that is a genuine export from a live install, legally obtained,
   no licence. ⚠️ **Its one distortion to know before building fixtures on it:** in Educational mode
   voucher DATES are restricted to the **1st, 2nd and 31st** of a month. Masters are unaffected;
   a voucher corpus built from it will have a degenerate date distribution, so **do not pin
   date-ordering or period-boundary behaviour on it** — build those from cored's own fixtures and use
   the Tally file for SHAPE. Also: Educational cannot print, and stamps a watermark; irrelevant to XML.
   ⛔ **STILL THE COMMERCIAL BLOCKER until a file actually exists in the repo.** The user asked; this
   is the answer; **nobody has done the install yet.** Raise it again with this paragraph attached,
   not as an open question.
2. **D87's scope:** ✅ **ANSWERED: the three + PAN + CREDIT DAYS.** Five new fields on the ledger
   form, one of them (`state_code`) mandatory under party groups. Address block, ITC eligibility,
   blocked clause and bank details stay OUT. Recorded as an amendment in
   `HANDOFF-CLOSE-THE-REGISTER.md` §4 D87 — that section is the authority; this line is the pointer.
3. **D90:** ✅ **ANSWERED: REFUSE NOW**, naming the existing item; do NOT defer into T10. The T10
   relaxation is `UNIQUE (company_id, name)` → `UNIQUE (company_id, group_id, name)`, a **widening**
   migration that cannot fail on existing data — cheap later, whereas un-splitting a year of
   half-costed valuation history is not. Recorded in `HANDOFF-CLOSE-THE-REGISTER.md` §4 D90.
4. **D50's fourth arm** stays answered-with-disclosure. Raise again only if the user asks for a
   refusal — the only honest way puts the caller's own party names in the prompt, reversing the current
   safety posture.

---

## 7. ALSO OPEN, OUTSIDE THE REGISTER — §7.3 B, C, D

Do not let these fall off the end. **B is a double-post and C is a tenancy leak.**

- **B** — the durable pending-confirm retry: one supplier bill, two vouchers, reproduced in a browser.
  The CONFIRM is fire-and-forget and only `console.warn`s, so if it fails after the voucher lands the
  only durable record of the acceptance is lost. Needs a durable retry or a transactional record —
  **a design decision, not a patch.** ⚠️ Probably the same thing as D15's residue (§5).
- **C** — a `?company=` that fails the client-side uuid regex is **silently discarded and somebody
  else's books are opened instead.** Real, populated books; nothing on screen looks wrong.
- **D** — a well-formed but NONEXISTENT `?company=` **wedges permanently**: cored answers 403 for a
  company that does not exist (membership is checked before any handler can 404), so `isDeadCompanyId`'s
  404 arm is dead code against this backend. Needs an actionable notice, not a revert.

---

## 8. AFTER THE REGISTER — the user's "once done" phase

`FeaturesScreen.tsx` is the honest ledger. **11 rows read `off`:** T8 multi-currency, T9 FIFO/LIFO,
T10 stock groups + compound units, T11 budgets, T12 GSTR-9, T13 interest, T14 backup/restore/split,
T16 per-company print templates, export-email/PDF, SSO, and ⛔ **T18 rule 48(4) QR, which is
PERMANENTLY unshippable — the payload is signed with the Income Tax Department's private key. If a
session reports T18 done, reject it.** **2 rows read `server`** (the payroll screens — pure frontend,
`internal/payroll` is already in the archtest allowed set — and offline sync).

⛔ **T32 Tally XML import is the COMMERCIAL BLOCKER.** Ask for a real file early (§6.1).
⚠️ **T9 must be serialised with D8** (§2 row 8) and **T10 with D90** (§2 row 3).

---

## 9. ON PARALLELISM — asked, answered, and re-examined 2026-08-22

The user asked why this is not parallelised, twice. The honest answer:

**The rule "reading fans out, driving does not" is TRUE but has been OVER-APPLIED.** The real
constraint is not "driving" — it is that there is exactly ONE database, ONE cored, ONE vite and ONE
tenant, and a mutation proof **edits a file, rebuilds the binary and restarts it**, so two agents
doing that concurrently manufacture false green for each other.

⛔ **BUT EVERY ONE OF THOSE IS ALREADY AN OVERRIDABLE VARIABLE:** `CORED_PORT`, `INSIGHTD_PORT`,
`STATUTORYD_PORT`, `DATABASE_URL`, `ADMIN_DATABASE_URL`. Nobody has spent the hour to turn that into
`make stack-up SLOT=n` (a port block + its own database + its own seeded tenant + teardown). That —
**not a law of nature** — is why every session has been serial.

**What it would honestly cost:** the Makefile part is easy; the work is that **24 drive scripts
hardcode endpoints** (44 occurrences of `localhost:8087` / `:5173`, counted 2026-08-22), `seed:demo` writes the single
global `/tmp/drive-company-id`, and migrating into a fresh empty database is a path nothing currently
exercises. Also note `internal/platform/testdb` is a **teardown** harness over the SHARED dev database,
not per-test isolation — which is why even `go test` cannot safely run twice at once and why `ci-slot`
is pinned at 2.

**What it would buy:** ~3× on this register, not 13×. Rows 1, 4 and 5 (D5+D43, D22+D52, D81) all churn
`VoucherScreen.tsx` and must stay serial. Genuinely concurrent once slots exist: **D49** (insightd, a
separate service), **D8** and **D86** (backend-only — and "ALONE" stops meaning "serial" once each has
its own stack), **D83** (backend-only). The user's standing decision as of 2026-08-22 was to **keep
going serially** rather than pause to build the harness; raise it again only if they ask.
