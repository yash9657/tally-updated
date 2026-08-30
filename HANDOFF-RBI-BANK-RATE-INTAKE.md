# HANDOFF — FIVE RBI PDFs TO INTAKE, THEN T13 IS UNBLOCKED

**Written:** 2026-08-30, working tree CLEAN at `c7f9d09` · **Branch:** `session/t32-and-close-7-3`
**Read time: 7 minutes.** ⭐ **Every claim below was MEASURED and the command is beside it.**

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** It is self-contained. §1 is a
mechanical intake task that takes about ten minutes; §4 onward is what to do afterwards.

---

## 0. ⭐ WHAT THE OWNER IS HANDING YOU, AND THE ONE-LINE REASON IT MATTERS

**Five PDFs of RBI "Change in Bank Rate" notifications.** They are the last missing input to
**T13 — interest on overdue bills**, which is otherwise fully designed and verified.

Why interest needs them: **MSMED Act s.16** prices interest on a late payment to a micro or small
enterprise at **three times the RBI Bank Rate, compound, with monthly rests**. That is not a rate
this build stores — it is arithmetic over a **dated series**, and an accrual period spanning a rate
change must be split at the change's effective date (`docs/design/t13-interest-model.md` §6.1).

⛔ **DO NOT SKIP §2's VERIFICATION.** The series this project was originally handed was a pasted
table that turned out to be **missing two rate changes**. Every step below exists because something
already went wrong once.

---

## 1. THE INTAKE — mechanical, do this first

### 1.1 What should be in the five files

| Effective | Bank Rate | Reference | RBI id |
|---|---|---|---|
| 4 May 2022 | 4.25 → **4.65%** | RBI/2022-23/45, `DOR.RET.REC.32/12.01.001/2022-23` | 12312 |
| 8 Jun 2022 | 4.65 → **5.15%** | RBI/2022-23/65, `DOR.RET.REC.44/12.01.001/2022-23` | 12333 |
| 30 Sep 2022 | 5.65 → **6.15%** | RBI/2022-23/123, `DOR.RET.REC.70/12.01.001/2022-23` | 12394 |
| 7 Dec 2022 | 6.15 → **6.50%** | RBI/2022-23/148, `DOR.RET.REC.88/12.01.001/2022-23` | 12420 |
| 7 Feb 2025 | 6.75 → **6.50%** | RBI/2024-25/111, `DoR.RET.REC.57/12.01.001/2024-25` | 12776 |

Each is at `https://www.rbi.org.in/Scripts/NotificationUser.aspx?Id=<id>&Mode=0` if one is missing
or wrong. ⭐ **`rbi.org.in` HTML pages ARE fetchable from this machine** — no geo-fence, no 403.

### 1.2 File them

```bash
# name them by EFFECTIVE DATE, matching the four already committed
cp <downloaded>.pdf docs/statutory/rbi-bank-rate-2022-05-04.pdf     # and 06-08, 09-30, 12-07, 2025-02-07
ls docs/statutory/rbi-bank-rate-*.pdf     # -> should be NINE files afterwards
```

⚠️ **A `rbi-bank-rate-2025-12-05.pdf` may also arrive** (the owner downloaded December 2025
separately). If so it is the tenth; treat it the same way.

### 1.3 ⛔ READ EACH ONE AND CHECK IT SAYS WHAT THE TABLE CLAIMS

```bash
for f in docs/statutory/rbi-bank-rate-*.pdf; do
  echo "== $(basename $f)"
  pdftotext -layout "$f" - | tr -s ' \n' ' \n' \
    | /usr/bin/grep -iE "RBI/20|RET\.REC|per cent to|revised" | head -4
done
```

⛔ **DO NOT FILE A DOCUMENT YOU HAVE NOT READ.** Two circulars issue on each policy day — see §2.2.

---

## 2. ⛔ THREE TRAPS, EACH OF WHICH ALREADY CAUGHT SOMEBODY

### 2.1 ⭐ THE CHAIN IS THE POINT — and it is what proves nothing is missing

**Every RBI Bank Rate circular opens by citing its IMMEDIATE PREDECESSOR**, with reference number
and exact date:

```bash
for f in docs/statutory/rbi-bank-rate-*.pdf; do
  printf "%-32s -> " "$(basename $f)"
  pdftotext -layout "$f" - | tr -s ' \n' ' \n' \
    | /usr/bin/grep -oE "(DOR|DoR)\.RET\.REC\.[0-9]+/12\.01\.001/[0-9-]+ dated [A-Z][a-z]+ [0-9]{2}, [0-9]{4}" | head -1
done
```

So the series is a **linked list**, and the check is that each circular's *"from X per cent"*
equals its predecessor's *"to"*:

```
4.25 -> 4.65 -> 5.15 -> 5.65 -> 6.15 -> 6.50 -> 6.75 -> 6.50 -> 6.25 -> 5.75 -> 5.50
```

⭐ **THAT IS A COMPLETENESS PROOF, NOT A TIDINESS CHECK.** A skipped change leaves a circular whose
named predecessor is absent, or whose "from" does not match. **It is how the two changes missing
from the original table were caught** (8 Jun 2022 and 9 Apr 2025), and neither was inferred — each
is named by the circular that follows it.

⛔ **IF ANY PREDECESSOR CITATION NAMES A DATE NOT IN §1.1's TABLE OR ALREADY COMMITTED, THERE IS
ANOTHER RATE CHANGE.** Fetch it before going further. Do not paper over it.

### 2.2 ⛔ TWO CIRCULARS ISSUE ON EACH POLICY DAY AND THE WRONG ONE LOOKS RIGHT

RBI publishes both, with **adjacent ids**:

| Circular | Department | Carries |
|---|---|---|
| *"Liquidity Adjustment Facility – Change in rates"* | FMOD | repo, SDF, **MSF** |
| ⭐ *"Change in Bank Rate"* | **DoR.RET.REC** | **the Bank Rate** |

**The MSF rate equals the Bank Rate**, so a series built from the LAF circular would show the right
numbers while citing the wrong instrument throughout. **Take the `DoR.RET.REC` one.** The ids in
§1.1 are already the correct ones.

⚠️ **THE TITLE HAS TWO SPELLINGS.** Older circulars are titled just **"Change in Bank Rate"**;
2025-26 ones are **"Penal Interest on shortfall in CRR and SLR requirements-Change in Bank Rate"**.
Searching for the long form alone misses February 2025 — that is exactly why it was reported as
"not findable".

### 2.3 ⚠️ THE "WITHDRAWN" WATERMARK IS INVISIBLE TO grep

Several of these circulars carry a diagonal **Withdrawn** stamp. `pdftotext` scatters it into
`W` / `it` / `hdr` / `aw` / `n` on separate lines, so **`grep -i withdrawn` over the text layer
returns nothing**. Use:

```bash
for f in docs/statutory/rbi-bank-rate-*.pdf; do
  printf "%-32s " "$(basename $f)"; strings "$f" | /usr/bin/grep -icm1 withdrawn; done
```

⭐ **AND IT DOES NOT INVALIDATE THEM.** RBI withdraws a circular when its substance folds into a
Master Direction (the CRR/SLR Master Direction 2021, Chapter VIII). Withdrawal ends its life as an
operative **instruction**; it does not unmake the historical fact that the rate changed on that
date, which is the only thing T13 needs. ⛔ **But never cite a withdrawn circular as CURRENT
authority** — "what is the rate today" and "what was the rate on 12 Sep 2022" are two questions.

⚠️ **A RELATED FALSE NEGATIVE, SAME CLASS:** both the MSMED Act PDF and these circulars wrap lines
mid-phrase, so `grep "three times"` returns **zero** against s.16 and `grep "twenty-five lakh"`
returns zero against s.7. **A zero is a line-wrap artefact, not an absence.** Search a distinctive
single word.

---

## 3. WHAT TO WRITE DOWN WHEN THE FIVE ARE IN

1. **`docs/statutory/README.md`** — add a row per file to the table, matching the four already
   there (`rbi-bank-rate-2022-08-05.pdf` etc.). The section *"RBI BANK RATE — THE NOTIFICATIONS
   FORM A CHAIN"* already carries the full picture; update its ✅/⛔ marks.
2. **`docs/design/t13-interest-model.md` §6.3** — its table marks rows `⚠️ secondary`. Promote the
   five to ✅ with their reference numbers.
3. ⛔ **§8's blocker table** — the RBI row can finally be struck **for the window 4 May 2022
   onwards**, and *only* that window. See §3.1.

### 3.1 ⛔ THE ONE THING THAT STAYS OPEN, AND IT NEEDS A DECISION NOT A DOWNLOAD

The 4 May 2022 circular revises *"from 4.25 per cent"* — so 4.25% was in force immediately before
it. ⛔ **When 4.25% BEGAN is unverified** (believed 22 May 2020, secondary sources only), and
nothing between May 2020 and May 2022 has been checked.

⚠️ **It matters only for a bill accruing interest before 4 May 2022** — a receivable more than four
years overdue.

⭐ **RECORD THE SCOPE DECISION RATHER THAN LEAVING THE GAP UNNAMED.** Either fetch the tail, or
state in §6.3 that the series begins 4 May 2022 and have the engine **refuse to compute** for an
anchor date before it, saying why. ⛔ **What it must not do is silently price a pre-May-2022 period
at the earliest rate it happens to hold** — that is the "missing change" failure one step removed,
and it foots perfectly.

---

## 4. ⭐ THE STATE OF T13 — what these files finish, and what was already settled

**Nobody has written a line of T13 engine code.** `docs/design/t13-interest-model.md` (~780 lines)
is the design, and as of 2026-08-29 its statutory half is **verified against primary text**.

### 4.1 What is SETTLED (verified against `docs/statutory/msmed-act-2006.pdf`, India Code)

| | |
|---|---|
| **s.2(b)** | "appointed day" = **fifteen days** after acceptance/deemed acceptance. ⛔ **The 15 days is HERE, not in s.15** |
| **s.15** | Pay by the date agreed **in writing**, else by the appointed day; agreed period capped at **45 days** |
| **s.16** | **Compound, monthly rests, 3 × Bank Rate**, from the appointed day **or** the day after the agreed date — ⭐ **two anchors, not one** |
| **s.23** | The interest is **not deductible** to the buyer under the Income-tax Act |
| **s.24** | ss.15–23 **override** anything inconsistent in any other law |

### 4.2 ⛔ WHAT IS STILL OPEN, AND NEITHER IS A DOWNLOAD

1. **WHO is a micro/small enterprise (§1.2).** s.7's thresholds in the Act are the **2006
   originals and are superseded**. ⛔ Judged against them almost every real business falls
   *outside* micro/small — so the regime would silently never fire and **every screen would look
   clean**. ⭐ The memo's recommendation: **do not derive the classification.** Record the
   supplier's **Udyam declaration** (number, class, date) and say so on the report.
2. **The anchor-date gap (§5, widened §1.1).** s.2(b)'s Explanation moves the start when the buyer
   **objects in writing within fifteen days** — to the day the supplier resolves the objection,
   **an event this product records nowhere**. Closing it needs a new fact captured at entry, not a
   new query.

### 4.3 ⚠️ AND A CORRECTION THE MEMO ALREADY CARRIES

`PLAN-TALLY-PARITY.md:477` and `FeaturesScreen.tsx`'s Interest row both say the day-count basis and
simple-vs-compound are *"configuration with statutory defaults"*. **That is wrong in both
directions** and §0.1/§0.3 explain why: for an MSE party the terms are fixed by statute and must not
be a UI control; for everyone else **no statutory default exists at all**. §0.3 has the replacement
sentence for the UI row. ⚠️ `FeaturesScreen.tsx` is SHARED with the T8 and T32 lanes — announce
before editing, cite rows by NAME never by number.

---

## 5. ⛔ WHAT IS *NOT* NEXT — the other blockers, so you do not re-derive them

| Item | Status |
|---|---|
| **EPF/EDLI administrative charges** | ⛔ **A RELEASE BLOCKER.** The EPF Scheme 2026 (G.S.R. 525(E), 29 Jun 2026) **fixes no rate** — para 29 delegates it to a separate notification. ~₹500/establishment/month is accruing **unbooked**. An email to the owner's CA is outstanding. ⛔ **Do not adopt the pre-2026 0.50%**; `pf.go` refuses it deliberately |
| **Portal verification** (GSTN/EPFO/ESIC) | ⛔ Needs a human in India with credentials. **No statutory output has ever been through a government portal** |
| **e-Invoice / IRP** | ⛔ Geo-fenced. ⚠️ See the project memory: the fence covers **NIC's 164.100.x generally**, not just the IRP — `samadhaan.msme.gov.in` refused too. It also constrains **where the product may be hosted** |
| **DPDP / privacy** | ⛔ Zero coverage, and a hard launch blocker. The immutable hash chain and "delete my data" are in real tension |
| **SSO / OIDC** | Waits on an IdP choice |
| **T8 multi-currency** | In flight on another lane; nothing has landed since `0080`. Worth asking whether it is still alive |

---

## 6. ⭐ AFTER THE INTAKE — the two things actually buildable today

Neither needs anything from the owner.

1. **The usability day.** `PLAN-TALLY-PARITY.md` §7 has asked for it since before T11: *"Nobody has
   yet driven the app for a day as a shopkeeper would."* Still true — every gate in the repo is a
   test or a scripted drive. ⭐ **It is the cheapest remaining item and probably the most
   valuable**, and bank matching (shipped 2026-08-28) was the last piece of the daily loop, so a
   full day is finally completable end to end.
2. **T4 slice 2c — the Tally matching gaps.** `HANDOFF-T4-SLICE-2C-MATCHING-GAPS.md` is written and
   sized: reference normalisation and party matching are ordinary work; an amount tolerance trades
   away half of invariant B-7; ⛔ **instrument-date matching is BLOCKED** on cheque-book management,
   a whole unbuilt Tally feature; and the opt-in auto-reconcile is marked highest-risk with five
   traps, four of them invisible once shipped.

⛔ **T13's ENGINE IS NOT ON THAT LIST, AND THAT IS DELIBERATE.** §8 of the memo: *"Do not build the
engine first and retro-fit the citations."* With the five PDFs in, the citations exist — but §4.2's
two open questions (the MSME flag, the anchor date) still shape the schema, and §9.2 has the build
order. **Read §9 before starting.**

---

## 7. THE GATES — all green at `c7f9d09`, so any red is yours

```bash
cd backend && go build ./... && go vet ./... && go test ./... -timeout 60m   # 0 FAIL
bash backend/scripts/test_residue.sh                                         # OK: no residue
cd frontend && pnpm exec tsc --noEmit                                        # clean
cd frontend && pnpm exec vitest run                                          # 1831 passed / 113 files
cd frontend && pnpm verify:demo "$(cat .demo-company-id)"                    # 34/34 ⚠️ id is an ARGUMENT
node frontend/scripts/drive-t4-slice2b-matching.mjs                          # 35/35, tenant reaped
node frontend/scripts/drive-t12-gstr9.mjs                                    # 33/33
node frontend/scripts/drive-t11-budgets.mjs                                  # 36/36
python3 corpora/validate.py                                                  # OK
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md                # 105 · 99 · 2 · 0
```

⚠️ **THE VITEST BASELINE IS 1831 / 113 FILES.** If you see 1812, you are on a tree without T4 slice 2b.

⛔ **CHECK THE AGE OF `cored` BEFORE ANY BROWSER DRIVE.** `make run-cored` is `go run`, so a stale
process is indistinguishable from a fix that does not work — found stale **eight** times now.
Restart both, **cored first** (a cored restart invalidates statutoryd's minted token):

```bash
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # newer than your last backend commit?
nohup make run-cored > /tmp/cored.log 2>&1 &                # ~20s to listen
nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 &
```

⚠️ **Vite must be on `:5173`** (`--strictPort`); WebAuthn origin matching is exact, and it binds
`::1` so use `http://localhost:5173/`, never `127.0.0.1`.

---

## 8. DEFINITION OF DONE FOR THE INTAKE

- [ ] Five (or six) PDFs in `docs/statutory/`, named by effective date.
- [ ] ⛔ **Each one READ**, and its `from → to` matching §1.1's table.
- [ ] ⛔ **The chain checked end to end** — every "from" equals its predecessor's "to". Any
      predecessor citation naming an unknown date means **another circular exists**; fetch it.
- [ ] The withdrawn-stamp check run with `strings`, not `grep`.
- [ ] `docs/statutory/README.md` rows added; `t13-interest-model.md` §6.3 rows promoted to ✅.
- [ ] ⛔ **§3.1's scope decision recorded** — either the pre-May-2022 tail is fetched, or the series
      is declared to begin 4 May 2022 and the engine refuses earlier anchors *by name*.
- [ ] Committed. ⚠️ These are binary PDFs; keep the commit message factual about what each settles.
