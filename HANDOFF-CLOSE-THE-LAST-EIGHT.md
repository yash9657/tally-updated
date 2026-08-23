# HANDOFF — CLOSE THE LAST EIGHT: the register's remaining rows, in order, with the oracle for each

Written 2026-08-23, after L15 closed **D5, D43, D87 and D90** from the driver and **found and closed
D91, D92 and D93** along the way. This **supersedes `HANDOFF-THE-LAST-TWELVE.md`**, whose positions
0–3 are done. It is the DRIVER: what to do next, in what order, what proves it, what will go wrong.
Execute it top to bottom.

## ⛔ THE COUNT, RE-DERIVED BY PARSING §7.1 — NOT COPIED FROM A HEADER

**93 registered · 81 CLOSED · 8 OPEN · 4 PARTIAL.** `81 + 4 + 8 = 93`.

```bash
python3 - <<'PY'
import io,re,collections
s=io.open("HANDOFF-PARITY-AUDIT.md",encoding="utf-8").read().split("\n")
a=[n for n,l in enumerate(s) if l.startswith("## 7.1")][0]
b=[n for n,l in enumerate(s) if n>a and l.startswith("### ⛔⛔ §7.2")][0]
c=collections.Counter(); rows={}
for l in s[a:b]:
    m=re.match(r'^\| \*?\*?(D\d+)\*?\*? \|', l)
    if m:
        st="CLOSED" if "✅" in l else "PARTIAL" if "PARTIAL" in l else "OPEN" if "OPEN" in l else "?"
        c[st]+=1; rows[m.group(1)]=st
print(c, "total", sum(c.values()))
print("OPEN   :", " · ".join(k for k,v in rows.items() if v=="OPEN"))
print("PARTIAL:", " · ".join(k for k,v in rows.items() if v=="PARTIAL"))
PY
```

⚠️ **CHECK `✅` BEFORE `OPEN`, IN THAT ORDER.** Several CLOSED rows contain the word "OPEN" in their
prose (D28 and D84 both do), so a naive `"OPEN" in line` test reports 10. The snippet above is
correct; a hand-rolled one usually is not.

**OPEN (8):** D8 · D22 · D49 · D52 · D81 · D83 · D85 · D86
**PARTIAL (4):** D13 · D15 · D17 · D48 — see §0, and do them FIRST this time.

⚠️ **THE OPEN COUNT FELL BY FOUR WHILE SEVEN ROWS CLOSED**, because three of the seven were rows
nobody had written down. **That is the normal shape of this phase.** Do not read "8 open" as "8 lanes
of work left" — read it as "8 known, plus whatever measuring turns up." L15 found one new row for
every row it was sent to fix.

---

## THE FILES, AND WHICH ONE WINS

| File | Role |
|---|---|
| **this file** | ⛔ **the order, the state, the oracle per row, the traps.** Start here. |
| **`HANDOFF-RECON-LAST-16.md` §3** | ⛔ **the `file:line` ANCHORS**, entries **8–14** cover all eight remaining rows. Deliberately NOT copied here — two copies drift. ⚠️ **Use it to find the CODE, never to fill in an EXPECTED VALUE** (§4.1), and ⚠️ **its expected values have now been wrong three times** (§4.11). |
| `HANDOFF-PARITY-AUDIT.md` §7.1 / §7.3 / §7.5 | the register (re-parse it), the three non-register defects B/C/D (§8), and the drive ledger (append one section per lane) |
| `HANDOFF-CLOSE-THE-REGISTER.md` §4 | ⛔ **the user's decisions. AUTHORITY, not suggestion.** A session that disagrees is disagreeing with the USER and must ask again, not re-decide. ⚠️ **Unless the decision's PREMISE is false — see §4.11 and D90.** |
| `HANDOFF-THE-LAST-TWELVE.md` | ⚠️ the PREVIOUS driver. Positions 0–3 are done. Its §4 trap list is carried forward here in full. |
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
been broken twice. **Commit, then prove.** (Held six times in L15 without incident.)

### ⛔ AND MARK THE ROW IN §7.1 IN THE SAME PASS
L15 shipped D87's fix, tests, drive and §7.5 entry with the register row still reading `⛔ OPEN`,
and the driver's own count line was correct about the TABLE and wrong about the PRODUCT. **Re-parse
§7.1 AFTER closing a row, not only before starting one.**

---

## 1. THE ORDER

Cost-ordered. **The PARTIAL audit is position 0 and it is genuinely first** — it is a read, not a
build, and **D48 scopes D81 (position 2)** so doing it later means doing D81 twice.

| # | Row(s) | Sev | Why here |
|---|---|---|---|
| **0** | **D13 · D15 · D17 · D48** | — | ⛔ **A READ, NOT A BUILD.** Settles what "finished" means, and **D48 decides how big D81 is.** ~40 minutes for all four. §2. |
| 1 | **D22 + D52** | HIGH | **ONE commit** — a door without the sentence, or the sentence without the door, is each worse than today |
| 2 | **D81** | MED | ⛔ **BIGGER THAN THE ROW SAYS — L15 added a second half to it.** Do after D48 (§2) says what remains. |
| 3 | **D49** | MED | largest scope of the wave. **Before D85**, so D85 lands on the final transport |
| 4 | **D85** | MED | **moves a pinned number.** ALONE |
| 5 | **D8** | HIGH | ⛔ **moves the BALANCE SHEET.** ALONE, with a release note |
| 6 | **D86** | MED | **moves Outstandings** and inverts a Go test by design. ALONE |
| 7 | **D83** | LOW | ⛔ the register's proposed index **WILL NOT WORK.** Last |

**Must be ONE commit:** D22+D52. **Must be ALONE:** D85, D8, D86, D83.
**Nothing is waiting on the user.** All four §6 questions were answered 2026-08-22.

---

## 2. POSITION 0 — THE FOUR `PARTIAL` ROWS. DO THESE FIRST.

They have been counted as CLOSED by every session since F6/F8 and **their status is genuinely
ambiguous**. Each is a ten-minute read.

| Row | Sev | What is definitely done | ⛔ THE QUESTION TO ANSWER |
|---|---|---|---|
| **D48** | MED | — | ⛔ **DO THIS ONE FIRST OF THE FOUR.** It is the orphan-master-on-failed-accept defect **on the DRAFT path**, and D81's row says the draft path already discloses its orphans via `DraftAcceptFailedError`. **If that is true, D48 is CLOSED and D81 is the surviving half — which is what position 2 needs to know before it starts.** |
| **D17** | HIGH | §7.4 records bill-wise references reaching the wire, driven | **This may simply be CLOSED and never re-marked.** Verify against `draftBodyToVoucherRequest` at HEAD, then close it or state what remains. |
| **D15** | HIGH | the claim→post→confirm reorder closed the race it was built for (two browsers 13ms apart → ONE voucher) | Is the residue exactly **§7.3 B** (the durable pending-confirm retry)? If so, say so in the row and stop calling it PARTIAL — the open work is already tracked in §8. |
| **D13** | HIGH | the per-bill receivables breakdown is on screen and exported, driven | The **PAYABLE side** is missing, and it is deliberately NOT a `?kind=` toggle: two directions must be two report NAMES, because **the PDP decides on the report name and a query parameter is invisible to it**. Needs a `Payables` report name + server resource + migration. ⛔ **THIS IS A BUILD. Register it as its own row and stop calling D13 partial** — otherwise "the register is finished" will be claimed with a feature missing. |

⚠️ **RESOLVING THESE MAY RAISE THE OPEN COUNT.** D13 becoming a row of its own is the likely outcome
and it is the correct one. A count that goes up because a hidden build was written down is a better
count.

---

## 3. PER ROW — state, the change, THE ORACLE

⛔ **Anchors are in `HANDOFF-RECON-LAST-16.md` §3, entries 8–14.** Search for the SYMBOL, never jump
to the line: HEAD moved ~16 times in L15 alone.
⛔ **And use `/usr/bin/grep`** — the shell's `grep` is aliased to ugrep and returns **silently empty**
on `frontend/src/api/mock.ts`. It has cost four searches across three sessions.

### 1 · D22 + D52 — no cash-flow screen, advertised as `on` · RECON §3.8 · ONE COMMIT
`/cash-flow` and `/anomalies` are live, ACL-mapped and carry their own `estimate`/`method`/`basis`
disclosure — **that disclosure IS register row D23, which is CLOSED, so the screen MUST render it.**
There is no type, no client method, no report name and no screen. `FeaturesScreen` advertises it as
`on` and names no door, while its two AI-native neighbours name theirs.
**The cheap route is a `report:NAME` frame, not a new `ScreenKind`.**
⚠️ **A report route's JSON shape is NOT guessable** — read the handler. Two attempts died on
`'str' has no attribute 'get'`.
✅ **D80 made `Alt+G` honest, so the picker is a usable oracle surface.**
**ORACLE:** a keyboard path reaches the projection in a browser AND the disclosure is on the page.
A door without the corrected sentence leaves the note naming no door; the sentence without the door
is worse. Also fold **D72's surviving clause** (insightd's forecast has nowhere to render) into D22.
⚠️ **L15 NOTE:** `FeaturesScreen.tsx` gained a fourth panel (`NegativeStockPanel`) and its Negative
stock row flipped `always` → `on`. Re-derive anchors; the `:340-354` line numbers in the register are
stale.

### 2 · D81 — a server refusal leaves masters behind · RECON §3.9 · ⛔ NOW TWO HALVES
⚠️ **The FORM-side half is closed** (L6/L12 drives prove Ctrl+A mints nothing when the form refuses).
This is the **SERVER-refusal** half: a cutoff/ACL/duplicate-number/negative-stock refusal still leaves
the ledgers behind, because a posting needs a real uuid and cored has no `DELETE /ledgers`. The DRAFT
path discloses its orphans via `DraftAcceptFailedError` — **that is the shape to copy.**
⛔ **AND L15 ADDED A SECOND HALF THAT BELONGS HERE.** D92 made a **client-side** refusal
non-destructive (the frame stays, the keying survives). A **server** refusal still pops the frame and
discards everything keyed — which is why D47 exists at all. **So the product now refuses in two
different ways, and it is the same seam:** a server refusal leaves the operator holding nothing, both
in the books (orphan masters) and on the screen (lost keying). Measured 2026-08-22 by
`drive-l15f-d90.mjs`, which asserts the pop as KNOWN and points here.
⛔ **Check D48 first (§2).** If the draft path is genuinely covered, this row is exactly the two
halves above and nothing else.
**ORACLE:** provoke a REAL server refusal (a back-dated cutoff is easiest; a duplicate master name is
now easier still — see D90) and the screen NAMES the masters it minted **and keeps the operator's
work**.
⚠️ Re-derive anchors at HEAD — `VoucherScreen.tsx` and `ScreenActions.tsx` both moved in L15.

### 3 · D49 — insightd authenticates nobody · RECON §3.10
⛔ **FAR LARGER THAN THE REGISTER IMPLIES.** insightd authenticates no one and scopes by a path
`company_id`: **7 unauthenticated routes**, and no cored proxy. Scope is 7 proxy routes + the insightd
bind + 2 client transports + the vite proxy + healthz + its test.
**ORACLE:** an unauthenticated call to each route is REFUSED, and a call for another tenant's
`company_id` cannot read it. On the wire, per route.
⚠️ **Before D85**, so D85's insightd wire-shape change lands on the final transport.

### 4 · D85 — the duplicate-bill guard keys on the POST-redaction number · RECON §3.11 · ALONE
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

### 5 · D8 — closing stock replayed in LEXICAL voucher_no order · RECON §3.12 · ALONE
An issue keyed after a receipt on the same date is costed at the pre-receipt average. Measured in a
browser: Closing Stock **25,909.3478** where entry order gives **25,307.8797** — 601.4681 too high.
The valuation half is correct; the ORDERING half is wrong, and **no existing fixture can fail on it.**
⛔ **THIS MOVES THE BALANCE SHEET** — closing stock feeds it — so it re-prices real books on the next
report read. Serialise with feature T9 (FIFO/LIFO).
⛔ **CHEAPEST CHECK, BEFORE CHANGING A LINE:** add ONLY the new failing fixture and the new
`valuation_test.go` subcase, and run the full backend + corpora suites. **Exactly one Go subcase and
the new fixture should fail.** If anything else fails, the cost model is wrong and the row needs
re-scoping before any code moves.
✅ **D77 is done, so posting order is stable** — this row's drive reads reports whose leg order no
longer wobbles.
**ORACLE:** the same books, replayed in ENTRY order, price the issue at the post-receipt average — and
the figure the browser showed moves to the one entry order gives.

### 6 · D86 — Outstandings bounds at today, the Balance Sheet does not · RECON §3.13 · ALONE
✅ **DECIDED: OUTSTANDINGS STOPS BOUNDING AT TODAY** — deliberately NOT the Balance Sheet, so
`verify:demo` needs no re-pin. One bill dated in the future gave BS **109950.0000** vs Outstandings
**108770.0000**, while `?as_on` in the future made them agree.
⛔⛔ **THREE PLACES MOVE TOGETHER, AND MISSING ONE RE-OPENS D20:** (1) the report; (2)
`internal/query/whitelist.go`'s `EntityOutstandings` skeleton, which D20's fix made MIRROR the report
— `drive-l5-nlq.mjs` will catch it; (3) `QueryScreen`'s `measureBasis` caption, which says
future-dated bills are excluded and becomes **FALSE**.
**ORACLE:** post a future-dated bill; the two reports AGREE with no `as_on`. ⚠️ It **inverts a Go test
by design** — say so in the commit rather than deleting the test.

### 7 · D83 — two concurrent posts share one hand-typed bill number · RECON §3.14 · LAST
⛔ **THE REGISTER'S PROPOSED FIX WILL NOT WORK.** `bill_allocation` cannot carry the index, because
duplicate names are legal for AGAINST / ON_ACCOUNT / caller-named openers. It needs
`pg_advisory_xact_lock` or a new table.
⚠️ **The SEQUENTIAL case — what an operator actually produces — is already closed.** This is the
concurrent one, under READ COMMITTED, and it is the only row in the wave needing a genuinely
concurrent probe.
✅ **L15 BUILT THE PATTERN YOU NEED HERE.** D90's `refuseIfNameTaken` is a pre-check that RACES with
a unique index behind it, and `migrations/master_name_index_census_test.go` exists precisely because
**no gate could see the index missing** — the pre-check shadowed it for serial creates. **Read that
test before designing this row:** it is the same shape (an application check that is not the
authority) and it names the trap.
**ORACLE:** two simultaneous posts of the same bill number on one party — one succeeds, one is
refused. On the wire; there is no mock equivalent for concurrency, and the mock says so at the method.

---

## 4. THE STACK, AND HOW TO KNOW IT IS REAL

```bash
cd /Users/yashbhalgat/code/tally-updated
docker compose up -d db
pg_isready -h localhost -p 5433 -t 10
PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"

# ⛔ MIGRATIONS ARE NOT APPLIED ON BOOT. `make run-cored` serves; it does not migrate.
#    L15 added 0075 and spent ten minutes wondering why the index was absent.
make migrate                                   # -> "migrations applied (59 files)"

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
until lsof -nP -iTCP:8087 -sTCP:LISTEN >/dev/null 2>&1; do sleep 1; done
PID=$(lsof -nP -iTCP:8087 -sTCP:LISTEN | tail -1 | awk '{print $2}')
BIN=$(ps -o command= -p "$PID" | awk '{print $1}')
strings -a "$BIN" | grep -c 'p.amount DESC, p.id'            # D77  (want 2)
strings -a "$BIN" | grep -c 'identity: set company features' # D5   (want 1)
strings -a "$BIN" | grep -c 'is not a GST registration type' # D93  (want 1)
strings -a "$BIN" | grep -c 'apart from capitalisation'      # D90  (want 1)

# SEED, and take the id from the LOG (never .demo-company-id)
cd frontend && pnpm seed:demo --insightd > /tmp/seed.log 2>&1
grep -oE "company id: [0-9a-f-]{36}" /tmp/seed.log | head -1 | awk '{print $3}' > /tmp/drive-company-id
pnpm run verify:demo "$(cat /tmp/drive-company-id)"     # -> ALL CHECKS PASSED (34/34)
```

### ✅ THE DRIVE SET IS GATE-SAFE — MEASURED 2026-08-23, END TO END
```
fresh seed -> verify:demo 34/34
   run all seven drives + contract:cored
verify:demo 34/34          <- the whole set leaves the tenant level
```
⛔ **THAT WAS NOT TRUE UNTIL THIS HANDOFF WAS WRITTEN, AND IT DIRECTLY CONTRADICTED §5.15.**
`drive-l14b-d77.mjs` posted an 11,800 invoice to `Acme Traders` — a bill-by-bill party — **with no
bill reference**, so it landed as an unallocated ON-ACCOUNT balance: the Balance Sheet counts it (a
ledger balance) and Outstandings does not (bill-wise). `verify:demo`'s **"BS Sundry Debtors ==
Outstandings (receivable)"** then failed by exactly that amount, on a tenant the probe does not tear
down. **A MONEY failure, which the next session reads as a regression in its own lane.** Fixed by
giving the fixture the bill reference a real invoice carries. **Re-run this end-to-end check after
adding any probe that POSTS.**

### THE DRIVE SET — all green at HEAD, 2026-08-23
```bash
# always from the REPO ROOT; the harness resolves against frontend/
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l13-masters.mjs  # 33/33
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l14-cheap.mjs    # 17/17 (D82/D89/D80)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l14b-d77.mjs     # 8/8   (D77)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15-d5.mjs       # 22/22 (D5/D43)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15c-d92.mjs     # 7/7   (D92)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15d-d87.mjs     # 13/13 (D87, both doors)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15f-d90.mjs     # 8/8   (D90)
cd frontend && pnpm contract:cored                     # 14/14 — ⛔ NO `CO`, it makes its own tenant (D91)
```

### Gates, measured at this handoff
`pnpm vitest run` **1440 / 87 files** · `pnpm exec tsc --noEmit` **clean** (a SEPARATE gate) ·
`go test ./... -count=1` **0 FAIL / 0 SKIP** (bracket with a live `psql` — ⚠️ a down database makes
every DB test SKIP and `go test` still exits 0, so the `0 SKIP` count is the load-bearing number) ·
`pnpm contract:cored` **14/14** · `pnpm verify:demo` **34/34 on a FRESH seed** ·
`make verify` **run ALONE** (beside anything else `internal/sync` hits its 600s timeout and
`EWayBillScreen.test.tsx` fails on a missing `role="radio"` while passing standalone).

### ⚠️ THE DEV DATABASE IS NOT PRISTINE
- **`CLUSTER posting` was run** (D77's oracle needs a heap rewrite). Harmless; the fix makes reads
  deterministic regardless — but it is why the table's physical order is not insertion order.
- ⚠️ **220 companies (2026-08-23) and nothing reaps them**, so `GET /v1/me` measures **0.5–3.5s** and the browser
  bootstrap is occasionally slower than any fixed wait — `openApp`'s tenant assertion polls to 40s for
  exactly that reason. Reaping needs a written script: `company` has **14 non-cascading FKs**.
  ⚠️ **81 of them are called "Demo Traders"** — see §5.13.
- ⚠️ **`"No Company"` in the chrome is a LOADING state, not a resolved one.**

### ⛔⛔ THE MACHINE — CHECK THIS BEFORE BLAMING THE APP
```bash
ps -Ao pcpu,pid,comm -r | head -12
pkill -9 -f "Google Chrome for Testing"       # orphaned probe browsers
cat ~/.claude/machine-health/ci-slots/*.owner # who actually holds the CI slots
```
⚠️ **`ci-slot` NAMES A STALE HOLDER, AND DEAD PIDS DO NOT MEAN A WEDGED QUEUE.** The semaphore is an
`flock`, which the OS releases on death — it **cannot** wedge on a dead holder.
⚠️ **`make run-cored` coming up in "1s" is NORMAL** — the build is cached. Judge the binary by its
symbols, never by how fast the port opened.
⚠️ **`timeout` is not installed on this machine.** Use `Bash(run_in_background)` or an `until` loop.

---

## 5. WHAT WILL GO WRONG

### 5.1 ⛔⛔ A RECON IS FOR FINDING THE CODE, NEVER FOR FILLING IN THE EXPECTED VALUE
D84 measured all eleven of its expectations on the wire instead of trusting the recon, and two were
wrong. **L15 did the same for D87 and two of five were wrong** — `credit_days` was accepted at 201 and
discarded, and `pan`/`credit_days` were not readable back — **on a row whose server side the recon
called done.** Three sessions running, ten minutes of `curl` has overturned a documented claim.
**Every remaining row's "the server answers X" claim is unverified until you fire it at the server.**

### 5.2 ⛔⛔ A MEASUREMENT IS EVIDENCE, NOT PROOF — THE COUNTERWEIGHT TO 5.1
§5.1 stands. **D90 supplies the other half.** D84 measured that cored ACCEPTS a duplicate stock item
name, contradicting the recon. That overturning was written into the corpus `$provenance`, repeated
across three handoffs, used to register **D90**, and used to frame a question **the user answered**.
It was false — `UNIQUE (company_id, name)` has been on `stock_item` since migration **0005**.
**The corrective is not to measure less** — re-measuring found the real defect underneath. **The
corrective is that "I measured it" is a claim like any other, and the measurements that feel like
DISCOVERIES are the ones to re-run.**

### 5.3 ⛔ THE MUTATION PROOF WILL SOMETIMES CORRECT **YOU**, NOT THE CODE
D77's commit asserted a cost-split misattribution that was plausible, traced, and **false**. L15's
M13 predicted cored would silently coerce a bad enum label; **it refuses** — and the refusal leaked
`SQLSTATE`, which became **D93**. **Write the claim down before the proof, so the proof can kill it.**

### 5.4 ⛔ A MUTATION THAT WILL NOT REPRODUCE IS A FINDING, NOT A FAILED STEP
D90's predicted mutation — drop the normalised indexes, watch the corpus go red — **did not
reproduce.** `contract:cored` reported **14/14 with two of three indexes missing**, because a Go
pre-check shadows the index for serial creates. The index is the only thing that refuses the second of
two **concurrent** creates, and **no gate could see its absence.** The non-reproduction bought
`migrations/master_name_index_census_test.go`. **When a mutation refuses to break something, find out
what is holding it up before writing a better mutation.**

### 5.5 ⛔ THE STATED ORACLE MAY NOT WORK — CHECK THE MECHANISM IS LIVE
D77's prescribed oracle (*"GET, `VACUUM ANALYZE`, GET again"*) does not move a non-total sort. It
takes a **heap rewrite**. **Fix:** if a probe's mechanism is probabilistic, add a **PRECONDITION arm**
that proves the mechanism fired for THIS fixture.

### 5.6 ⛔ A PROBE THAT PASSES FIRST TRY IS THE ONE TO DISTRUST
D82's suite was green THROUGH a duplicate-invoice defect. D80's probe passed asserting nothing about
the mode. D77's passed comparing side and amount only. **Print the field you are asserting on, not a
prefix of the page.**

### 5.7 ⚠️ A PROBE THAT READS THE WRONG FIELD NAME PASSES VACUOUSLY — **THREE TIMES IN ONE SESSION**
| Probe | Read | The wire/DOM actually has |
|---|---|---|
| D77 (L14b) | `p.ledger_name` | `p.ledger` |
| D5 (L15) | `stock_item_name` | `item` |
| D92 (L15c) | `body.innerText` for an input's value | **input VALUES are not in `innerText`** |
⛔ **All three are one shape: a projection that does not exist, COMPARED instead of THROWN on.** An
absent projection has a falsy value that compares cleanly against anything. **Make the probe THROW.**

### 5.8 ⛔ ASK WHAT A **SECOND** KEYPRESS DOES
Nothing in this repo had ever pressed the accept key twice on one frame, and that is where D89 lived.
**For every row whose oracle is a REFUSAL, drive the operator's next keystroke too. The refusal is not
the end of the story; the remedy is.**

### 5.9 ⛔ WHEN A ROW HAS A STRUCTURAL GUARD, READ WHAT THE GUARD **COMPARES**
D80 shipped a dead picker entry *past* a test written to prevent exactly that: two of its three
clauses checked the machine's real routing tables; the third certified the broken entry.
⛔ **D93 IS THE SAME LESSON FROM THE OTHER SIDE:** `drive-l13-masters.mjs` asserts `!/SQLSTATE/` on a
refusal from the ledger-create route, and **what it compared was one arm of four.** **Read what the
guard does NOT compare, and ask why those siblings are exempt.**
⛔ **AND D5's M4:** mapping a route to `authz.Display` instead of `authz.Alter` kept
`TestEveryCompanyRouteIsMappedAndInVocabulary` **GREEN** while handing a Data Entry clerk the
inventory escape hatch. **The structural gate catches an UNMAPPED route and is blind to a MIS-mapped
one.**

### 5.10 ⛔ ANSWER FROM THE ROW, NEVER FROM THE REQUEST
D5's **M2b** is the cleanest evidence in the repo: with the read-back replaced by an echo, the
RESPONSE assertion went **green** while the column stayed wrong. That is D43's exact mechanism, **one
line away at all times**, and **only an out-of-band oracle can see it.** Every remaining row that
writes a setting owes this check. **A renamed constraint has the same shape** — see D90.

### 5.11 ⚠️ FOR A CLIENT-SIDE MUTATION, VERIFY **VITE**, NOT THE BINARY
The §4 symbol check proves what cored is running and says nothing about a refusal that never leaves
the browser. Curl the module out of vite and count the mutated construct — and **calibrate against
`git show HEAD:<file> | grep -c`**, because a construct that also appears in COMMENTS never reaches
zero.

### 5.12 ⚠️ TWO CLIENT/AUTH TRAPS
- **`createRestClient`'s auth option is `headers`, not `getAuthHeaders`, and a wrong key is silently
  ignored.** Symptom: `401 missing bearer token` on every row.
- **`auth.mts`'s `authHeaders()` mints for a fresh RANDOM subject** — right for a script that creates
  its own company, wrong for one handed an existing tenant. Mint for `browser-dev-owner`, **once per
  run**. ⚠️ **`contract:cored` now does the opposite ON PURPOSE** (D91): it creates its own tenant, so
  it mints a random subject — and that gives it a second layer, because a caller who re-points it at
  someone's books with `CO` gets eleven loud `403`s instead of eleven quiet writes.

### 5.13 ⚠️ NEVER GROUP BY A DISPLAY NAME IN THIS DATABASE
A duplicate-hunting query grouped by `company.name` and reported **188 collisions**; the real number
was **6**. **81 of the 220 companies are called "Demo Traders"** — one per re-seed. **Group by
`company_id`.**

### 5.14 ⛔⛔ A PROBE THAT CREATES A MASTER IN THE DEMO TENANT MUST REAP IT
**D91 closed this for `contract:cored` and did NOT close the class** — the very next drive script
written in the same session re-introduced it, and `verify:demo` went 34/34 → 31/34 on one party left
behind. **The cost is misattribution, not the failing check:** the next session reads seven failures,
two of them money failures, as a regression in its own lane. The reap must **assert it worked** and
**refuse to run on a master that has postings**.
⚠️ **RE-SEED BEFORE JUDGING `verify:demo`.** Seven failures on a stale tenant is the NORMAL state.

### 5.15 ⛔⛔ RE-RUN THE **WHOLE** DRIVE SET AT THE END OF A LANE
D87 broke **two** probes hours after both were green:
- `getByText("Sundry Debtors")` began matching TWO elements, because D87's party block carries the
  prose *"a state code is REQUIRED under Sundry Debtors and Sundry Creditors"*. Playwright's strict
  mode throws. **Pick a group with `getByRole("option", …)`, never by text.**
- and `drive-l13-masters.mjs` — **the file that FOUND D87** — went comprehensively red, because its
  fixture party is created under Sundry Debtors with no state code, so the very first create was
  refused and every downstream arm failed. ⛔ **That does not read as a stale probe. It reads as D36
  and D47 having regressed.**
⚠️ **Every drive that creates a PARTY through the FORM now needs a state code.** Measured 2026-08-23:
only `drive-l13-masters` and `drive-l15c-d92` use the form path; the other five that mention party
groups go through the API, where the client-side refusal does not apply.
⛔⛔ **AND FOLLOWING THIS RULE IS WHAT EXPOSED THE NEXT ONE.** Re-running the set turned `verify:demo`
from 34/34 to 33/34 with a MONEY failure — `drive-l14b-d77` was posting to a bill-wise party with no
bill reference (see §4). **Advice to re-run everything is a trap unless the set is gate-safe**, so the
set was made gate-safe in the same pass and the end-to-end check is now in §4.

### 5.16 ⛔ WHEN A ROW'S OWN DRIVE ASSERTED THE DEFECT, INVERT IT — DO NOT DELETE IT
`drive-l13-masters.mjs` found D87 and asserted *"the ledger form offers NO GSTIN field"*. Those arms
are now **inverted**, not removed: **a deleted arm leaves nothing that would notice a REVERT**, an
inverted one fails the day the field disappears and still explains why it has to be there.

### 5.17 ⚠️ THE NUMBER-MOVING ROWS MAY MOVE MORE THAN PREDICTED — OR NOTHING
If D85/D8/D86's gate stays GREEN, assume the binary was not rebuilt and cored not restarted **before**
assuming the probe is not an oracle. **Predict the moved number out loud in the commit message.**

### 5.18 ⚠️ A FULL-SUITE FAILURE UNDER LOAD MAY BE A FLAKE — CHECK, DON'T ASSUME
**Structural argument first** (does the test even touch your change?), then the standalone run, then
the re-run. Known members: `internal/sync`, `EWayBillScreen`, `VoucherScreen.draft.test.tsx`.

---

## 6. THE USER'S DECISIONS — ✅ ALL ANSWERED. DO NOT RE-ASK.

1. **T32:** ✅ **No public corpus of genuine Tally exports exists.** What GitHub carries
   (`TallyConnector`, `nodejs-tally-integration`, `PHP-to-tally-integration`, `tally-prime-api-docs`)
   is **hand-authored REQUEST envelopes** — the import direction, written by integrators, never
   produced by the program whose output T32 must parse. ⛔ **Do not let one into the repo as a
   fixture.** ✅ **THE REAL PATH: TallyPrime Educational** — free, unlicensed, indefinite, and it
   exports XML. ⚠️ In Educational mode voucher DATES are restricted to the **1st, 2nd and 31st**, so
   build fixtures on it for **SHAPE**, never for date-ordering or period boundaries. ⛔ **STILL THE
   COMMERCIAL BLOCKER until a file is actually in the repo.**
2. **D87's scope:** ✅ the three + PAN + credit days. **DONE (L15d).**
3. **D90:** ✅ refuse duplicates. ⛔ **Its premise was false; the user was re-asked with the real
   finding and chose to normalise all three master kinds. DONE (L15f).**
4. **D50's fourth arm** stays answered-with-disclosure. Raise again only if the user asks for a
   refusal — the only honest way puts the caller's own party names in the prompt, reversing the
   current safety posture.

---

## 7. WHAT L15 CHANGED THAT THE REMAINING ROWS TOUCH

| Change | Who it affects |
|---|---|
| `PUT /v1/companies/{id}` + `(authz.Alter, "company")` | anything walking the router or the routeAction map |
| `allow_negative_stock` on create + read + the F11 panel | **D22** (FeaturesScreen anchors are stale) |
| `screenRefusal()` / `ScreenActionResult.refused` (D92) | **D81** — this is the mechanism its screen half should extend to the server path |
| the ledger party block + 6 client-side refusals (D87) | **any probe creating a party through the FORM** — §5.15 |
| `pan` + `credit_days` on `ledgerReq` and `GET /ledgers` | anything round-tripping a ledger |
| migration **0075** — normalised master-name uniqueness | **D83** (same pre-check-vs-authority shape) |
| `masters` refusals are sentences, not driver errors (D93) | anything asserting a refusal MESSAGE |
| `contract:cored` makes its own tenant and IGNORES `CO` (D91) | every session's gate script |

---

## 8. ALSO OPEN, OUTSIDE THE REGISTER — §7.3 B, C, D

Do not let these fall off the end. **B is a double-post and C is a tenancy leak.**

- **B** — the durable pending-confirm retry: one supplier bill, two vouchers, reproduced in a browser.
  The CONFIRM is fire-and-forget and only `console.warn`s, so if it fails after the voucher lands the
  only durable record of the acceptance is lost. Needs a durable retry or a transactional record —
  **a design decision, not a patch.** ⚠️ **Probably the same thing as D15's residue — §2 settles it.**
- **C** — a `?company=` that fails the client-side uuid regex is **silently discarded and somebody
  else's books are opened instead.** Real, populated books; nothing on screen looks wrong.
- **D** — a well-formed but NONEXISTENT `?company=` **wedges permanently**: cored answers 403 for a
  company that does not exist (membership is checked before any handler can 404), so
  `isDeadCompanyId`'s 404 arm is dead code against this backend. Needs an actionable notice.

---

## 9. AFTER THE REGISTER — the user's "once done" phase

`FeaturesScreen.tsx` is the honest ledger. **11 rows read `off`:** T8 multi-currency, T9 FIFO/LIFO,
T10 stock groups + compound units, T11 budgets, T12 GSTR-9, T13 interest, T14 backup/restore/split,
T16 per-company print templates, export-email/PDF, SSO, and ⛔ **T18 rule 48(4) QR, which is
PERMANENTLY unshippable — the payload is signed with the Income Tax Department's private key. If a
session reports T18 done, reject it.** **2 rows read `server`** (the payroll screens — pure frontend,
`internal/payroll` is already in the archtest allowed set — and offline sync).

⛔ **T32 Tally XML import is the COMMERCIAL BLOCKER** (§6.1 has the path).
⚠️ **T9 must be serialised with D8** (§3 row 5).
⚠️ **T10 widens D90's stock-item index** to `(company_id, group_id, lower(btrim(name)))` — a
**widening** migration that admits rows the current one refuses and can never fail on existing data.

---

## 10. ON PARALLELISM — asked, answered, re-examined 2026-08-22, unchanged

**The rule "reading fans out, driving does not" is TRUE but has been OVER-APPLIED.** The real
constraint is that there is ONE database, ONE cored, ONE vite and ONE tenant, and a mutation proof
**edits a file, rebuilds the binary and restarts it** — so two agents doing that concurrently
manufacture false green for each other.

⛔ **BUT EVERY ONE OF THOSE IS ALREADY AN OVERRIDABLE VARIABLE:** `CORED_PORT`, `INSIGHTD_PORT`,
`STATUTORYD_PORT`, `DATABASE_URL`, `ADMIN_DATABASE_URL`. Nobody has spent the hour to turn that into
`make stack-up SLOT=n`.

**What it would cost:** the Makefile part is easy; the work is that **30 drive/e2e/probe scripts
hardcode endpoints** (42 occurrences of `localhost:8087` / `:5173`, re-counted 2026-08-23 — the
previous driver said 24/44 and both had drifted), `seed:demo` writes the single global
`/tmp/drive-company-id`, and migrating into a fresh empty database is a path nothing exercises. Note
`internal/platform/testdb` is a **teardown** harness over the SHARED dev database, not per-test
isolation — which is why even `go test` cannot safely run twice at once.

✅ **D91 IS THE FIRST STEP OF THAT WORK AND IT IS ALREADY DONE.** `contract:cored` now provisions its
own tenant through the product's own route. **That is the pattern the rest would follow.**

**What it would buy:** ~3× on this register, not 8×. Genuinely concurrent once slots exist: **D49**
(insightd, a separate service), **D8** and **D86** (backend-only — and "ALONE" stops meaning "serial"
once each has its own stack), **D83** (backend-only). Rows 1 and 2 (D22+D52, D81) both churn
`VoucherScreen.tsx`/`FeaturesScreen.tsx` and must stay serial.
**The user's standing decision as of 2026-08-22 is to keep going serially.** Raise it again only if
they ask.
