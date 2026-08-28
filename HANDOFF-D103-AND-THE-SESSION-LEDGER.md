# HANDOFF — D103, AND EVERYTHING THIS SESSION DID

**Written:** 2026-08-27/28, from `c9b2ded` to `87eb9da` · **Branch:** `session/t32-and-close-7-3`
**Read time: 10 minutes.** ⭐ **Every number below was MEASURED and the command is beside it.**

⛔ **THIS FILE SUPERSEDES `HANDOFF-NEXT-UNBLOCKED.md`**, which was written earlier in this same
session and is already partly stale (it lists D102 as open and knows nothing of D103). Everything
still live in it has been folded in here. **`git rm HANDOFF-NEXT-UNBLOCKED.md`** — the handoff count
stays at 28 rather than growing to 29.

⛔ **THE ONE THING TO READ IF YOU READ NOTHING ELSE:** **§2 — D103.** A stock journal between the
company's own godowns **creates value on the Balance Sheet**, a fix is **IN FLIGHT** as of this
writing, and §2.6 tells you exactly how to check whether it landed.

---

## 1. ⭐ WHAT THIS SESSION DID — EIGHT COMMITS, `c9b2ded` → `87eb9da`

```bash
git log --oneline c9b2ded..HEAD          # 8 commits
git diff --stat c9b2ded..HEAD | tail -1  # -> 27 files changed, 3508 insertions(+), 129 deletions(-)
```

| Commit | What |
|---|---|
| `7085159` | **D100 FIXED** — the TallyPrime export recipe, in both places an operator meets it |
| `07df42e` | **D102 REGISTERED** — the OIDC audience gap, disclosed on a screen but on no register |
| `d67e396` | **D98's blocking question ANSWERED** — atomicity is available, and cheap |
| `9707ebe` | **D98 + D101 FIXED** — the migration path stops losing and stops inventing money |
| `fcc55c0` | The lock-line handoff marked up — its §7 steps 1 and 2 done |
| `c091af7` | `HANDOFF-NEXT-UNBLOCKED.md` (⛔ **now superseded by this file**) |
| `fc7b9d9` | **Migration numbers reserved** — 0085–0087, with a deliberate gap for the T8 lane |
| `87eb9da` | **D102 FIXED · T13 design note · L28 re-audit → D103/D104/D105 registered** |

**Defect register moved `102 · 100 · 2` → `105 · 101 · 4`.**

```bash
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md
# -> 105 REGISTERED · 97 CLOSED · 4 OPEN · 0 PARTIAL · 4 UNREADABLE
#    OPEN: D99, D103, D104, D105     (97 parsed + 4 unreadable-but-closed = 101 closed)
```

⭐⭐ **THE COMMERCIAL BLOCKER IS GONE.** `HANDOFF.md` §10 item 1 called the Tally import path the
thing that stops a business switching. D98, D100 and D101 are closed. **A business can now bring its
books in without losing money or inventing it.**

---

## 2. ⛔⛔ D103 — THE ACTIVE WORK. READ THIS BEFORE TOUCHING INVENTORY.

### 2.1 What it is

**A stock journal (Alt+F7) moving goods between the company's OWN godowns CREATES VALUE on a FIFO
item, and the Balance Sheet moves with it and still reports `balanced=true`.**

Measured 2026-08-27 by `frontend/scripts/drive-l28-stock-restale.mjs`:

```
Fifo Widget @ Main Location: two lots — 10 @ 120.00 and 5 @ 130.00 = 15.0000 / 1,850.00
Stock Summary position Rate: 123.33      (= closing_value / closing_qty)
Alt+F7: transfer 10 units Main Location -> Second Store, keying 123.33

item closing value   1,850.00 -> 1,883.30   (+33.30)
company closing stock 5,100.00 -> 5,133.30
GET /balance-sheet?as_on=2026-04-30 -> 5133.3000, balanced=true
```

Nothing entered or left the business.

### 2.2 ⭐ The mechanism, and why nothing caught it

The **OUT leg consumes the oldest layer** (10 @ 120.00 = 1,200.00). The **IN leg carries the goods at
the keyed blended 123.33** = 1,233.30. The difference is created from nothing.

⛔ **QUANTITY IS UNCHANGED, so no quantity check anywhere can see it.** And the screen's own footer
asserts the opposite: *"Each line posts as a PAIR (out of From, into To), so the company's total
holding is unchanged."*

⛔ **123.33 IS THE ONLY RATE THE PRODUCT DISPLAYS ANYWHERE.** `stockVoucher.ts` leaves the Stock
Journal's Rate box **blank**, so the operator reaches for the one number on screen — the Stock
Summary's position Rate — which is a *blend*, not a consumption rate.

### 2.3 ⭐⭐ Two controls from the same run locate it — this is why it is actionable

1. Identical keystrokes on a **WEIGHTED_AVG** item conserve **exactly** (1,650.00 → 1,650.00).
2. Identical keystrokes on a **single-layer FIFO** position conserve **exactly**.

**So it is neither "transfers" nor "FIFO".** It is the rate, which stops being the consumption rate
the moment a lot queue holds more than one layer.

⚠️ **DIRECTION IS NOT FIXED EITHER:** a queue whose oldest layer is *dearer* than the blend
**destroys** value on the same keystrokes.

### 2.4 ⛔ IT IS A REGRESSION BY PREMISE, NOT BY EDIT — THIS IS THE LESSON OF THE SESSION

A 2026-08-07 fix (the long comment at `backend/internal/ledger/nonaccounting.go` ~316) forced a
non-zero inward rate, and **it was completely correct for the world it was written in**: under
weighted average the displayed rate *is* the consumption rate, so keying it conserved exactly.

**Migration `0078` (FIFO/LIFO) made that premise false for two of three methods, and nothing
downstream was revisited.** Every comment in `nonaccounting.go` and `stockVoucher.ts` still reasons
in weighted-average terms.

⛔ **NO DIFF INTRODUCED THIS BUG. A FEATURE LANDED UNDERNEATH A CORRECT FIX AND FALSIFIED ITS
ASSUMPTION.** That is why the stale-evidence audit — which alleged no defect and existed only to
refresh evidence — is what found it.

### 2.5 The decision already taken

> **(a) DERIVE the outward leg's actual consumption cost and carry the inward leg at exactly that,
> so a same-company stock journal conserves BY CONSTRUCTION.**

Not (b) "keep the rate an operator input and refuse a voucher whose legs do not net to zero".
⭐ **Why:** a transfer between your own godowns **has no price**. Asking an operator to key one is
asking them to be the invariant — and they will key the only number on screen, which is exactly how
this defect happens.

⛔⛔ **THE TRAP THAT MAKES IT NOT A ONE-LINER** (`nonaccounting.go:110-117`): `StockJournal` is ONE
class serving TWO shapes — a **transfer** of one item between godowns, and a **manufacture** that
consumes components and produces a different finished good. **A manufacture MUST be able to change
per-item value; that is what manufacturing is.** A naive "inward rate == outward rate" rule refuses
every legitimate manufacture. The open question handed to the fix lane was whether **voucher-level**
conservation (total inward value == total outward consumption cost) is the right general rule.

⚠️ **AND A SECOND FORK:** value appears to be derived **at report time** (`reporting.replayPositions`
selects a valuer per item since 0078), not stored at post time — so the outward's true consumption
cost may not be knowable at post time at all, and the fix may belong in the **valuer**.

### 2.6 ⛔ STATUS: A FIX IS IN FLIGHT. HOW TO TELL WHETHER IT LANDED.

A 6-agent workflow was running when this file was written: three read-only investigation lenses (the
valuation path · scope-and-manufacture · an adversarial sweep for the same rotten premise elsewhere)
→ a design agent taking the two forks above → implementation → an **independent** proof agent.

```bash
git log --oneline -5                 # is there a commit naming D103 after 87eb9da?
node frontend/scripts/drive-l28-stock-restale.mjs
#   BEFORE the fix: 72/75, three reds
#   AFTER  the fix: the D103 probe and the D104 copy assertion should be GREEN
```

⭐ **THE DRIVE IS THE ORACLE AND IT IS ALREADY WRITTEN CORRECTLY** — its probes are phrased so that
**conservation is the PASS**, so it goes green on the run that fixes this, with no edit to the
script. If you find the script edited to make it pass, treat that as the defect.

⛔ **IF THE FIX DID NOT LAND, DO NOT START FROM SCRATCH.** The workflow's transcript and its per-agent
findings are at
`~/.claude-premium/projects/-Users-yashbhalgat-code-tally-updated/*/subagents/workflows/wf_b82d1f94-f0d/journal.jsonl`
(one `{"type":"result",...}` line per agent). The three investigation lenses alone are worth
recovering before re-doing the work.

### 2.7 D104 and D105 ride with it

- **D104 (LOW)** — `nonaccounting.go:446`, the physical-stock surplus refusal names *"the item's
  weighted-average value"* on FIFO and LIFO items too. The **rule is right on all three methods**;
  only its reason names one. Same file, same misconception — it was handed to the same lane.
- **D105 (LOW)** — `FeaturesScreen.tsx` row 23 still says value is replayed *"through a
  WEIGHTED-AVERAGE valuer"*. Since 0078 the valuer is **per item**, and the L28 drive measures three
  different valuers on one report in one company. ⛔ **NOT FIXED BY ANY LANE ON PURPOSE:
  `FeaturesScreen.tsx` is shared with the T8 and T32 lanes.** It needs whoever owns that file.

---

## 3. ⭐ THE FIXES THAT LANDED, AND THE PROOF EACH CARRIES

⛔ **EVERY FIX THIS SESSION WAS MUTATION-PROVEN: THE FAILURE WAS PREDICTED, THEN OBSERVED.** That is
the standard now; a test that passes with the fix disabled proves nothing.

### 3.1 D98 — a refused import was not atomic, and the recovery double-posted

The voucher pass now runs in **one transaction**. ⭐ **It needed no change to the `ledger` or
`masters` packages at all**: both are built over a one-method `TxRunner`, so a seven-line `txPin`
handing out the same `pgx.Tx` makes the pass one unit of work — the hash chain included, because it
is computed inside the transaction rather than against committed state.

⚠️ **SCOPE IS DELIBERATE: the voucher pass only.** The master passes still commit per-master and
still survive a refusal, because a master is a fact about the *file* and a voucher is a claim about
the *trade*.

| Mutation | Observed | The register's original figure |
|---|---|---|
| revert the enrolment | *"a REFUSED import left **2** voucher(s) in the books"* | **2** |
| ” | *"the books hold **5** vouchers, want 3"* | **5** |

⭐ **The atomicity question had been registered as a blocker for a week. Answering it took twenty
minutes of reading.** Check the seam before you scope the work.

### 3.2 D101 — the importer posted money Tally keeps out of the books

`ISCANCELLED` / `ISOPTIONAL` vouchers and `ISDELETED` ledger masters are now skipped, **checked
before the ACTION dispatch** so an ALTER cannot append a live version to a voided source. Counted in
its own bucket — ⛔ **not `vouchers_skipped`, which means "already imported under this GUID"** — and
rendered as *"Excluded as void in Tally: N"* with a note saying they are **not missing and not
duplicates**. Mutation: disable the skip → *"3 vouchers posted, want 1"*, and **3 is the pre-fix
number the drive recorded**.

### 3.3 D100 — the export recipe

Alt+E → Configure → **Type of Master = All Masters**, **Include dependent masters = Yes**, **Export
closing balance as opening balance = Yes**. In *both* copies (screen and abort text), **pinned
together by a test** that asserts the refusal contains `"All Masters"` so they cannot drift apart.

### 3.4 D102 — the unchecked OIDC audience

`authn.Audience` is a `[]string` with a custom `UnmarshalJSON` accepting **both** RFC 7519 forms.
⛔ **The array form is what Keycloak emits, and binding `aud` as a plain `string` would have made the
check pass vacuously for every token** — the fix would have *looked* done and verified nothing.
`buildAuth` refuses to boot when `AUTH_JWKS_URL` is set and `AUTH_AUDIENCE` is empty, and ⭐ **the
audience guard runs BEFORE the JWKS fetch**, so a live IdP with no audience cannot start — pinned by
a test that asserts the error is *not* the unreachable one.

Mutation: disable the comparison → a token signed by the real IdP key, under the published kid, with
the pinned issuer, but minted for **another application in the same realm**, returns **403 — it
AUTHENTICATED.** That is D102 reproduced exactly.

⚠️⚠️ **AND TWO EXISTING SECURITY TESTS WERE FOUND PASSING FOR THE WRONG REASON.** Both asserted
`Contains(err, "JWKS")` — and the new audience refusal contains that substring inside
`AUTH_JWKS_URL`. They had silently stopped testing unreachability and invalidity **while still going
green**. They now assert `unreachable` and `invalid JWKS`. ⛔ **When you add an error message, grep
for tests that match loosely on the old one.**

---

## 4. ⛔ WHAT IS BLOCKED ON THE REPO OWNER

| # | Ask | Unblocks | State |
|---|---|---|---|
| **1** | **MSMED Act ss.15–16 text + the RBI Bank Rate**, into `docs/statutory/` | **T13** | ⭐ **NOT blocked by anything** — ordinary public documents. **The one worth chasing** |
| **2** | **IRP / GSTN sandbox credentials** | The Rule 48(4) QR row — the only item no engineering can close | ⛔ **HARD-BLOCKED, see §4.1** |
| **3** | One real business day on the app | Unmeasurable any other way | open |
| **4** | Should a data-entry clerk see a cost/budget report? | A one-line ACL decision | ⚠️ **T11 proceeds on `0072`'s precedent (deny) unless told otherwise** |
| **5** | CGST Rule 138 text · a JRE + Protean's RPU · EPF/EDLI notification | T5 citations · a *validated* TDS return · ~₹500/establishment/month unbooked | open, `HANDOFF.md` §10 |

### 4.1 ⛔⛔ THE IRP IS GEO-FENCED. MEASURED, NOT GUESSED.

```
www.nic.in                                → 302   ✅ NIC itself is fine
einv-apisandbox.nic.in  (164.100.69.129)  → TIMEOUT on 443 AND 80
einvoice1.gst.gov.in    (164.100.69.11)   → TIMEOUT          ← same subnet
www.gst.gov.in          (103.83.78.25)    → 200   ✅
einvoice6.gst.gov.in    (18.65.5.167)     → 200   ✅ CloudFront (IRIS IRP)
```

Both failures are on NIC's **164.100.69.x** subnet and are **silent drops, not refusals**, while
every GST host on a commercial CDN answers. **That is a source-IP geography filter.**

⛔ **DO NOT RE-DIAGNOSE IT AS DOWNTIME OR BOT-BLOCKING.** `docs/statutory/README.md` documents a
*different* failure (`incometaxindia.gov.in` 403s automated requests; a human with a browser gets
through). **This one is not defeated by a browser.** Routes around: an India-exit VPN, someone in
India doing the one-time OTP registration, or a private IRP such as IRIS.

⚠️⚠️ **IT IS AN ARCHITECTURE CONSTRAINT, NOT A SIGNUP ANNOYANCE.** Production IRP access needs the
**calling server's outbound IP whitelisted**, so wherever this product runs must reach that fenced
network — Indian hosting, an Indian egress path, or a GSP.

### 4.2 ⭐ T13's PLAN LINE IS WRONG, AND THE DESIGN NOTE NOW SAYS SO

`docs/design/t13-interest-model.md` (621 lines, written this session) corrects
`PLAN-TALLY-PARITY.md:477`'s *"configuration with statutory defaults"*. **There is no single
statutory default — there are two regimes**, and one global setting is wrong in both directions at
once: overridable where MSMED ss.15–23 forbid override, and defaulted where nothing authorises one.

⛔ **THREE FINDINGS IN THAT NOTE RESHAPE THE BUILD ORDER — read them before starting T13:**
1. **`reporting.Outstandings` cannot be the engine's input.** It nets with `SUM(...)` and collapses
   dates to `MIN(due_date)`, so a bill part-paid on day 60 and settled on day 120 returns
   `pending = 0` and would accrue **zero** interest on 120 days of lateness.
2. **No MSME/Udyam field exists anywhere** in code or schema, and the Tally importer reads none.
   ⚠️ And it must store a **class, not a boolean** — MEDIUM enterprises are *not* protected by
   ss.15–16, so a boolean sweeps them into the statutory regime silently.
3. **No bill table stores a bill DATE**, so MSMED's appointed day is unrecoverable for migrated
   opening bills. `TallyOpeningBill.BillDate` is parsed and **never read**.

---

## 5. THE ORDER TO WORK IN

1. ⛔ **Land D103** (§2.6) — check whether the in-flight fix committed; if not, recover the
   workflow's investigation findings before redoing anything.
2. **D104** rides with it. **D105** needs the `FeaturesScreen.tsx` owner.
3. ⭐ **T11 Budgets** — nothing blocks it. **Migration `0085` (tables) + `0086` (ACL).**
4. **T12 GSTR-9** — **migration `0087`**. Independent of T11 and T8.
5. **T13** — once §4 ask 1 lands. The design note makes it a one-sitting build.
6. **T8** lands on its own lane's schedule. ⛔ Do not start it from here.

### 5.1 ⛔⛔ THE TWO TRAPS THAT WILL COST YOU AN HOUR EACH ON T11/T12

**A. A NEW `report:NAME` IS GRANTED TO DATA ENTRY BY DEFAULT.** Verified at
`backend/migrations/0011_auth_acl.sql:249`:

```sql
VALUES (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'display', 'report');
```

A **bare** resource, matched by `internal/authz` against **any** `report:NAME` at specificity 1. So
`report:budget-variance` is readable by a clerk **the day you ship it** unless you write the refusal
— in **both** `display` and `print` forms, because a report that cannot be displayed but can be
printed is a leak with a paper trail. This is **D95** repeating. Contrast bare tokens like
`tds-challan`, which are denied with no rule at all.

**B. THE MIGRATION NUMBERS ARE ALREADY RESERVED — TAKE THEM, DO NOT DISCOVER THEM.** `fc7b9d9`
reserved **0085–0086 (T11)**, **0087 (T12)**, and left **0081–0084 as a deliberate GAP** because the
T8 lane is in flight from 0080.

```bash
# derive any seeder restatement from the highest-NUMBERED one — RE-MEASURE, do not inherit:
for f in backend/migrations/*.sql; do c=$(awk '/CREATE OR REPLACE FUNCTION seed_company_auth_defaults/,/^\$\$ LANGUAGE plpgsql/' "$f" | grep -c PERFORM); [ "$c" -gt 0 ] && echo "$(basename $f) -> $c"; done | tail -3
# -> 0072 -> 12 · 0073 -> 13 · 0076 -> 14      DERIVE FROM 0076
```

⛔ **A low number is a SECURITY bug, not a style point:** `migrations.Apply` runs in `sort.Strings`
order and **only the highest-numbered restatement of `seed_company_auth_defaults` survives**. An ACL
at 0056 is overwritten on every fresh database and **the rule simply does not exist** — the defect
`0071` exists to repair, which shipped undetected once.

---

## 6. ⛔ RUN FIRST — TRAPS THAT HAVE EACH COST REAL TIME

```bash
git log --oneline -3 && git status --porcelain
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # NEWER than the last backend commit?
```

1. ⛔ **CHECK THE AGE OF `cored`.** `make run-cored` is `go run`, so a stale process is
   indistinguishable from a fix that does not work. **Found stale five times.** ⛔ A cored restart
   **invalidates statutoryd's minted token** — restart both, **cored first**.
   *(Both were restarted 2026-08-27 19:45 and are current as of `87eb9da`.)*
2. ⛔ **VITE MUST BE ON `:5173`** — WebAuthn origin matching is exact.
   ⭐ **FIXED THIS SESSION, AND THE FAILURE WAS INVERTED FROM WHAT YOU EXPECT:** 5173 was held by a
   **week-old** dev server while the process launched with `--port 5173` had silently fallen back to
   **5174**. Start it with **`--strictPort`** so it fails loudly. ⚠️ It binds `::1` — reachable as
   `localhost`, **not** `127.0.0.1`. The harness uses `http://localhost:5173/`; a script hard-coding
   `127.0.0.1` will not work.
3. ⚠️ **`FeaturesScreen.tsx` IS SHARED** with the T8/T32 lanes. Announce before editing. This is why
   **D105 is still open**.
4. ⚠️ **`make verify` DOES NOT RUN `tsc`**, and `tsc` does not run what `make verify` runs. Each has
   caught a real error the other missed. ⛔ `make verify` invokes `make migrate` — the T8 lane owns
   the schema this week.
5. ⭐ **THE FIRST WRITE OF EVERY BROWSER DRIVE 403s. THAT IS THE STEP-UP CEREMONY, NOT A BUG.** The
   wire log reads `POST /units 403` → `webauthn/register/begin` → `finish` → `assert/*` →
   `POST /units 201`. Do not go hunting for a permission defect.

### 6.1 GATES — ALL GREEN AT `87eb9da`, ALL RE-RUN THIS SESSION

| Gate | Result |
|---|---|
| `cd backend && go build ./... && go vet ./... && go test ./... -timeout 60m` | **exit 0, ZERO FAIL lines** |
| `cd frontend && pnpm exec tsc --noEmit` | clean |
| `cd frontend && pnpm exec vitest run` | **1787 passed / 109 files** |
| `pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT |
| `python3 corpora/validate.py` | OK (migration=6) |
| `python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md` | **105 · 97 · 4 · 0** (+4 unreadable, all closed) |
| `node frontend/scripts/drive-u1-import-partial.mjs` | **15/16** ⚠️ the 16th is D99's comma arm, red **by decision** |
| `node frontend/scripts/drive-u1-voided-vouchers.mjs` | **12/12** |
| `node frontend/scripts/drive-l28-stock-restale.mjs` | **72/75** ⛔ three reds = D103 + D104 + the row-23 copy |

---

## 7. ⚠️ LOOSE ENDS NOBODY HAS PICKED UP

1. ⛔ **`drive-l10-stock.mjs:34` IS STALE AND SHOULD BE FAILING TODAY.** It asserts
   `headers.length === 11` on the Stock Summary; the screen has **13** (T9 added *Basis*, T10 added
   *Closing Qty (base unit)*). It is row 23's standing evidence. **Deliberately not corrected** — the
   L28 lane's brief was to ADD a drive, and silently editing another lane's assertion would erase the
   record that the evidence went stale. Needs an owner.
2. ⚠️ **THE MIXED-BASIS COMPANY TOTAL CANNOT DISCRIMINATE — a trap for any future valuation probe.**
   1,650 + 1,700 + 1,600 = 4,950 = 3 × 1,650. Three items with identical movements on
   WEIGHTED_AVG / FIFO / LIFO total **exactly** the same as if all three were averaged. A build with
   a broken per-item valuer prints a Balance Sheet that foots and a Stock Summary footer that is
   right. ⛔ **On a mixed company the per-ITEM lines are the only oracle.**
3. ⚠️ **NO KEYBOARD ROUTE TO A GODOWN MASTER.** `MASTER_GOTO_ENTRIES` (`navMachine.ts:165`) lists
   ledger, group, stock item, stock group, unit and currency — not godown. FeaturesScreen row 22 says
   godowns *"can be created"*, which is true of the API and not of the keyboard.
4. ⚠️ **`classByVchType` STILL ABORTS A WHOLE IMPORT** on a `Memorandum` or `Reversing Journal`
   VCHTYPE — the third kind of voucher Tally keeps out of the books; D101 handled the other two.
   Safe for the money and no longer a partial commit now D98 is fixed. **No row yet.**
5. ⚠️ **A CITATION THAT DOES NOT RESOLVE:** `t8-multi-currency-model.md:8` attributes the `grep`
   discipline to *"HANDOFF.md §9"*; §9 is "TRAPS" and contains no such item. The rule actually lives
   at `HANDOFF-NEXT-SESSION.md:222` and `.claude/HANDOFF.md:346`.
6. ⚠️ **ARCHIVE-AND-CARRY-FORWARD HAS NO ROW AT ALL.** Closing a financial year into a **read-only
   archive company** plus a fresh one carrying opening balances — for statutory year-end and for
   handing a closed year to an auditor. Not built, and **the FeaturesScreen does not disclose it**.
   ⛔ **Give it a row whichever way it is decided: an undisclosed gap is the one failure mode that
   screen exists to prevent.**
7. **`HANDOFF-FEATURE-LOCK-LINE.md` is down to ONE open question** — §6, where to draw the lock line.
   Its §7 steps 1–2 are done and its §3 defects are closed. `git rm` it when the line is agreed.

---

## 8. ⭐⭐ THE THREE LESSONS WORTH CARRYING OUT OF THIS SESSION

1. ⛔ **A FEATURE LANDING UNDERNEATH A CORRECT FIX CAN FALSIFY IT, AND NO DIFF SHOWS THAT.** D103 was
   not introduced by any change to the code that broke. `0078` made a true comment false. **The only
   thing that catches this class is re-driving evidence that has gone stale** — which is exactly the
   lane that found it, and it had alleged no defect going in.
2. ⛔ **A TEST THAT KEEPS PASSING CAN STOP TESTING.** Two security tests matched `Contains(err,
   "JWKS")` and silently began matching a *different* error the moment one was added containing that
   substring. **Mutation-prove security tests, and grep for loose matches when you add an error.**
3. ⭐ **CHECK THE SEAM BEFORE SCOPING THE WORK.** D98's atomicity question sat registered as a
   blocker for a week; the answer was a one-method interface already in place and the fix was seven
   lines.
