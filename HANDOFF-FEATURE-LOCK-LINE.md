# HANDOFF — WHAT IS LEFT BEFORE THIS CAN BE CALLED FEATURE-LOCKED AGAINST TALLY

**Written:** 2026-08-27, at `8ee2bdb` · **Branch:** `session/t32-and-close-7-3`
**Read time: 8 minutes.** ⭐ **Every number below was MEASURED today and the command is beside it.**

⛔ **THIS FILE ANSWERS ONE QUESTION AND NOTHING ELSE:** *what stands between here and a defensible
"feature complete vs Tally"?* It is not a plan for a track. It is the LIST, the evidence for the list,
and a recommended lock line with the argument for where to draw it.

⛔ **`git rm` THIS FILE when the lock line is agreed and the work it names is scheduled.** It is a
decision document, not a reference one.

---

## 0. ⛔ THE ONE THING TO READ FIRST: "FEATURE LOCK" IS A DECISION, NOT A MEASUREMENT

The measurement below is unambiguous — **6 real gaps, 4 open defects, 3 rows with stale evidence.**
What is NOT a measurement is which of those must close before the product may be called complete.
Two of the six are not Tally-parity items at all, and shipping them would be scope the competitor
never had. **§6 proposes a line and argues for it. Disagreeing with the line is a legitimate outcome
of reading this file; disagreeing with the list is not.**

---

## 1. ⭐ THE MEASUREMENT — 54 ROWS, PARSED FROM THE SCREEN ITSELF

```bash
# the roster, by state, from the source of truth an operator actually reads
python3 - <<'PY'
import re, collections
s = open('frontend/src/screens/FeaturesScreen.tsx').read()
rows = re.findall(r'name:\s*("(?:[^"\\]|\\.)*")\s*,\s*\n\s*state:\s*"([a-z]+)"', s)
print(len(rows), collections.Counter(st for _, st in rows))
PY
# -> 54 {'on': 45, 'off': 7, 'always': 1, 'server': 1}
```

| State | Count | Meaning |
|---|---|---|
| `on` | **45** | built, and 34 of them driven end to end by the parity audit, plus the seven of §7.5 L27 |
| `always` | 1 | Edit Log — on by construction, cannot be switched off |
| `server` | 1 | **Offline entry with sync** — half built (see §2.6) |
| `off` | **7** | the gaps. One is a deliberate non-goal, so **six are real** |

⛔ **THE ROWS ARE THE RIGHT INSTRUMENT AND THAT IS ITSELF AN ACHIEVEMENT.** This screen has been
audited row by row twice (34 rows, then the 7 added since — `HANDOFF-PARITY-AUDIT.md` §7.5 L27), and
its `off` rows argue their own case in prose. **Trust the roster. Do not re-derive the gap list from
`PLAN-TALLY-PARITY.md`'s tier tables — those are a plan, and the screen is the product.**

---

## 2. THE SIX REAL GAPS, EACH WITH WHAT IT COSTS AND WHAT IT TAKES

### 2.1 ⭐ T8 — MULTI-CURRENCY · **IN FLIGHT, DO NOT START IT AGAIN**

The other lane is working forward from **migration 0080**, which landed the schema, and `a7e9341`,
which landed the currency master and its rate. ⛔ **The row is deliberately still `off`**, and its
note is the reason: *"You cannot yet key a voucher in a foreign currency, settle a foreign bill, or
see an exchange gain or loss — those are the feature, and none of them exists."* The masters got a
row of their own that is wholly true. **What remains is the voucher path, bill settlement, and the
gain/loss computation.**

⚠️ **`FeaturesScreen.tsx` is SHARED with that lane.** Announce before editing; rows are one per line
so conflicts resolve trivially, but a silent double-edit does not.

### 2.2 T11 — BUDGETS AND BUDGET-VS-ACTUAL · **buildable today, nothing blocks it**

Row: *"A budget is a comparison dimension rather than a posting, so it changes no voucher and no
balance — which is exactly why it cannot be faked by keying journals: there is no second column for
any report to compare an actual against."* ⭐ **That sentence is also the reason it is cheap-ish**: it
posts nothing, so it inherits none of the immutability, hash-chain or cutoff machinery. Budget-vs-
actual on groups, ledgers and — since T2 shipped — cost centres.

### 2.3 T12 — GSTR-9 ANNUAL RETURN · **buildable today; its inputs already exist**

GSTR-1, GSTR-3B and the ITC engine are all built and driven. ⛔ **It is a RECONCILIATION, not a
derivation** — the row is explicit that it *"must be able to state where the three disagree instead of
smoothing it, which is why it is not a report we can generate by summing twelve months and calling it
done."* Treat "where they disagree" as the deliverable, not as an error path.

### 2.4 T13 — INTEREST ON OVERDUE BILLS · **buildable, but it needs citations first**

Row: *"Outstandings will tell you a bill is 90 days old; it will not tell you what that has cost."*
⛔ **The blocker is not code.** Simple vs compound and the day-count basis are configuration **with
statutory defaults**, and the row says plainly that *"inventing those defaults would be worse than the
gap."* So the first step is sourcing the defaults into `docs/statutory/`, the same way T5's Rule 138
material is treated. Until that exists, building the engine means choosing numbers nobody can cite.

### 2.5 ⛔ RULE 48(4) QR CODE ON A PRINTED e-INVOICE · **BLOCKED ON A HUMAN, NOT ON US**

⛔⛔ **THIS IS THE ONLY GAP ON THE BOARD THAT IS A COMPLIANCE GAP RATHER THAN A PARITY GAP, AND IT
CANNOT BE CLOSED BY WRITING CODE.** The QR that rule 48(4) requires encodes **the signed payload the
Invoice Registration Portal hands back**. No IRP call can be made from this build, and — the row's own
words — *"a QR generated from anything else would be a forgery of a government signature."*

⭐ **THE PRODUCT ALREADY HANDLES THIS AS WELL AS IT CAN BE HANDLED.** Any invoice carrying an IRN
prints, in bold, under it:

> *"QR code not printed - this copy is NOT the e-invoice to be issued under rule 48(4); obtain the
> signed QR from the IRP"*

…because the failure that costs money is the one where the paper LOOKS complete and the person at the
counter hands it over. ⚠️ The mandatory-particulars validator states the gap where the check would go
rather than pretending to check it, and **deliberately does not cite a clause letter** for the QR
particular (believed 46(r), unverifiable from here — a wrong clause in a refusal shown to an auditor
is a false citation).

⛔ **SO: THIS ROW CLOSES WHEN §5 ITEM 1 (IRP SANDBOX CREDENTIALS) IS SATISFIED AND NOT BEFORE.** Do not schedule engineering
against it. **It is the strongest argument for putting IRP sandbox credentials at the top of §5.**

### 2.6 OFFLINE ENTRY WITH SYNC (`server`) · **half built, and the built half is driven clean**

⭐ **What works, and it is not small:** a voucher whose post could not reach the server is queued on
this machine instead of lost — `Alt+Q`, IndexedDB, provisional numbers marked `PROV`, one batched
commit, whole-batch refusal as a banner, discard, cleared on sign-out. **Driven 23/23 + 27/27**
(§7.5 L27, U2). ⛔ **What is missing is the half that would let you key on a train:** there is no
service worker and no offline app shell, so **this application still needs the server to load at
all.** The row will not say `on` until that exists, and it is right not to.

⚠️ **THIS IS A PRODUCT BET, NOT A TALLY GAP** — Tally is a desktop binary and has no equivalent
"works without the app shell" problem to be at parity with. See §6.

### 2.7 ⛔ NOT A GAP: SPLIT COMPANY DATA · **a deliberate non-goal, and the row argues it**

Tally splits a company by financial year because it loads the whole company into memory and RAM is the
binding constraint (`RESEARCH.md:987`). **This product keeps unlimited multi-year history queryable at
once, which is one of the things it competes on.** Building split for parity would be building a
workaround for a limit we do not have. ⛔ **DO NOT BUILD IT, AND DO NOT COUNT IT AGAINST THE LOCK.**

⚠️ **There IS a real thing next door and it is not this one:** closing a financial year into a
READ-ONLY archive company plus a fresh one carrying opening balances, for statutory year-end and for
handing a closed year to an auditor. That is archive-and-carry-forward. It is not built either, and it
is **not on the roster as a row at all** — the only gap in this document that the FeaturesScreen does
not itself disclose. Worth a row whichever way it is decided.

### 2.8 ⛔ NOT A TALLY GAP: SINGLE SIGN-ON (SSO / OIDC)

**Tally has no SSO.** Counting this against Tally parity is a category error. It is on the roster
because the screen tells the truth about the whole product, not because Tally has it.

⚠️ **But the row discloses a real defect inside the half that DOES exist, and it should not be lost
when this is deferred:** the verify half accepts an OIDC provider's tokens (keys cached by kid,
fail-closed, asymmetric only, issuer pinned) — **and the audience claim is not checked.** That is a
security gap in shipped code, not a missing feature. ⛔ **It belongs in the defect register even if SSO
is deferred forever.** It is not there today; putting it there is a five-minute act and it is item 1
of §7.

---

## 3. ⛔ THE FOUR OPEN DEFECTS — ALL FOUR ON THE MIGRATION PATH

```bash
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md
# -> 101 REGISTERED · 93 CLOSED · 4 OPEN · 0 PARTIAL · 4 UNREADABLE
#    OPEN: D98, D99, D100, D101      ?: D49, D80, D82, D84 (all CLOSED, see the §7.1 header)
```

| # | Sev | One line | Blocked on |
|---|---|---|---|
| **D98** | HIGH | A refused import is **not atomic**, says nothing about the vouchers it already committed, and the operator's natural recovery **double-posts** them on a GUID-less export | a design decision: make it atomic, or keep it partial and disclose it |
| **D101** | HIGH | A voucher marked **ISCANCELLED or ISOPTIONAL** in Tally — money the source books exclude — is posted as a **live entry**, with no warning. A `ISDELETED` ledger master is created | a decision: skip them, or post and say so |
| **D100** | MEDIUM | The screen tells an operator to *"export the whole company"*; doing exactly that produces a file that cannot satisfy the importer's own stated remedy | **nothing — answered.** TallyPrime's *Type of Master* offers All/Inventory Masters. It is three sentences of copy |
| **D99** | LOW | The three money readers refuse comma-grouped numbers while the quantity reader strips them on purpose | nothing. Premise refuted by a real export; leave it |

⛔⛔ **A FEATURE LOCK WITH TWO OPEN HIGH DEFECTS ON THE MIGRATION PATH IS NOT A LOCK.** Both are on
`Alt+O`, which `HANDOFF.md` §10 item 1 calls **the commercial blocker**: a business cannot switch if it
cannot bring its books. **D98 and D101 change the same loop** (`migration.go:247`, the second pass), so
they are ONE piece of work and should be decided together.

⭐ **D100 is the cheapest item on this entire document** — three sentences of copy, no new code path,
and it should name TallyPrime's **"Export closing balance as opening balance"** option, because that is
what makes the remedy deliver what it promises: an export of a closed year carries CLOSING balances and
the new company needs them as OPENINGS.

---

## 4. ⚠️ THREE ROWS WHOSE EVIDENCE IS STALE — CHEAPER THAN IT SOUNDS

Rows **20** (stock items/units), **23** (Stock Summary) and **26** (inventory vouchers) were driven
**before** T9 (FIFO/LIFO, `0078`) and T10 (stock groups + compound units, `0079`) landed underneath
them. **The rows are old; their evidence is not current.** No defect is alleged — the drives simply no
longer attest to the code beneath them.

⭐ **The method is settled and the tooling is committed**: `frontend/scripts/audit-harness.mjs`, and
§7.5 L27 is the worked example of an audit lane. Budget one session.

---

## 5. ⛔ WHAT ONLY A HUMAN CAN DO — REORDERED BY WHAT IT NOW UNBLOCKS

`HANDOFF.md` §10 carries eight asks. **Item 1 is DONE** — a real TallyPrime export pair was supplied
2026-08-27 and drove U1 to 22/22, settling D99 and producing D100 and D101. The rest, re-ranked:

| # | Ask | Now unblocks |
|---|---|---|
| **1** | ⛔ **IRP / GSTN / e-Way Bill sandbox credentials** (needs a GSTIN) | **The Rule 48(4) QR row — §2.5 — which no amount of engineering can close.** Also T5 for real and T18's portal half. **This is now the single highest-value human ask on the board** |
| **2** | **One real business day on the app** | still the highest-value item that is unmeasurable any other way |
| **3** | **The day-count / simple-vs-compound statutory defaults for interest**, into `docs/statutory/` | **T13 — §2.4.** Without it the engine has to invent numbers nobody can cite |
| **4** | **The published text of CGST Rule 138 and 138(10)** into `docs/statutory/` | lets T5's threshold and validity table be cited rather than carried as secondary material |
| **5** | **A JRE + Protean's RPU zip** | lets the TDS return be called *validated*, not merely generated |
| **6** | **EPF/EDLI administrative-charge rate notification**, if issued since 2026-07-30 | ~₹500/establishment/month accruing unbooked. ⛔ Do NOT substitute `S.O. 2701(E)` — that is *inspection* charge |
| **7** | **Is a cost report something a data-entry clerk should see?** `0072` says no, deliberately | a one-line ACL decision, and a business call rather than a technical one |
| **8** | ⚠️ **A re-export with `Type of Master = All Masters`** (optional) | would let a complete migration be driven end to end and confirm D100's abort disappears. **Not blocking** — every finding stands without it |

---

## 6. ⭐⭐ THE PROPOSED LOCK LINE, AND THE ARGUMENT FOR IT

**Recommendation: lock on four features and two defects, and defer three items with the reason
written down.**

### MUST CLOSE BEFORE THE LOCK

| Item | Why it is inside the line |
|---|---|
| **T8 Multi-currency** | In flight. A half-landed schema with no voucher path is the worst state to lock in — the tables exist and nothing can reach them |
| **T11 Budgets** | A Tally feature every Tally user has. Nothing blocks it |
| **T12 GSTR-9** | A Tally feature, statutory, and its inputs are already built and driven |
| **T13 Interest on overdue bills** | A Tally feature. ⚠️ Gated on §5 item 3, so START THE SOURCING NOW even if the build is last |
| **D98 + D101** | Both HIGH, both on the migration path, both the same loop. **A product that cannot be migrated into cannot be locked** |
| **D100** | Three sentences. There is no version of "we are finished" that survives leaving this |

### DEFER, WITH THE REASON RECORDED ON THE ROW

| Item | Why it is outside the line |
|---|---|
| **Split company data** | ⛔ A deliberate non-goal — a workaround for a RAM limit this product does not have. The row already argues it. **Never build it** |
| **Single sign-on** | **Tally has no SSO.** Counting it against Tally parity is a category error. ⚠️ But register the unchecked-audience defect (§2.8) regardless |
| **Offline app shell / service worker** | A product bet, not a parity gap — Tally is a desktop binary with no equivalent. The queue half is built and driven clean; the row honestly says `server` |
| **Rule 48(4) QR** | ⛔ **Cannot be closed by engineering at all.** It closes when §5 item 1 arrives. The row discloses it in bold on the printed page, which is the correct behaviour in the meantime |

### ⚠️ AND ONE THING THAT IS NOT ON THE ROSTER AT ALL

**Archive-and-carry-forward** (§2.7) — closing a financial year into a read-only archive company plus a
fresh one carrying opening balances. It is the real need that sits next door to the split-company
non-goal, it is not built, and **it has no row**. Decide it either way, but give it a row: an
undisclosed gap is the one failure mode this screen exists to prevent.

**On that line, the lock is four tracks and one defect decision. It is a quarter of work, not a year.**

---

## 7. THE ORDER TO WORK IN

1. ⭐ **D100 + the SSO-audience defect registration.** An hour. Copy and a register row.
2. ⛔ **Decide D98/D101 together, then fix them.** One loop, two decisions, both proofs required —
   a mutation proof whose failure was **predicted then observed**, *and* a browser re-drive.
   `drive-u1-import-partial.mjs` and `drive-u1-voided-vouchers.mjs` are written so that **the correct
   behaviour is the PASS**: either fix turns them green, which is how you will know.
3. **Ask for §5 items 1 and 3 in the same message.** Item 1 unblocks a row nothing else can; item 3
   has to arrive before T13 can start honestly. Both have lead time; neither blocks steps 1–2, 4 or 5.
4. **T11 Budgets**, then **T12 GSTR-9**. Independent of each other and of T8.
5. **T13 Interest**, once item 3 has arrived.
6. **The stale-evidence audit** (§4) — rows 20 / 23 / 26. One session.
7. **T8 lands** on its own lane's schedule. Do not start it from here.

⛔ **Steps 4–5 are the only ones with a hard ordering constraint between them, and it is external
(the citations), not technical.** Everything else can be parallelised across disjoint files.

---

## 8. ⛔ MIGRATION NUMBERS — THE RESERVATION TABLE'S LOW BLOCKS ARE DEAD IN PRACTICE

`PLAN-TALLY-PARITY.md` §2.5 calls itself **"the only authority"**, and it still reserves **T8
0051–0052, T11 0056–0057** and neighbours as *free*. ⛔ **T8 did not take 0051. It took `0080`.**

```bash
ls backend/migrations/*.sql | tail -1     # -> 0080_multi_currency.sql
```

⚠️ **SO THE LOW RESERVATIONS ARE ABANDONED IN PRACTICE AND TAKING ONE TODAY IS A TRAP.** A migration
numbered 0056 runs **before** 0080 on a fresh database. For a plain `CREATE TABLE` that is harmless.
For anything that **restates `seed_company_auth_defaults`** it is exactly the defect `0071` exists to
repair: *a body installed lower is overwritten on every fresh database and the rules simply do not
exist.* **Take the next free number at the END, derive any seeder restatement from the
highest-numbered one, and update §2.5 in place when you do.**

---

## 9. RUN FIRST — and what was actually verified today

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
git log --oneline -3 && git status --porcelain
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # NEWER than the last backend commit?
```

⛔ **CHECK THE AGE OF `cored`.** `make run-cored` is `go run`, so a stale process is indistinguishable
from a fix that does not work. **Found stale five times now** — including at the start of the session
that wrote this file. ⛔ **A cored restart invalidates statutoryd's minted token**, so the two restart
together, cored first. ⛔ **VITE MUST BE ON `:5173`** — WebAuthn origin matching is exact, and a second
vite was found squatting on 5174 today. ⛔ **A TCP CONNECT IS NOT LIVENESS.**

**Verified at `8ee2bdb` on 2026-08-27:**

| Gate | Value | Run today? |
|---|---|---|
| `python3 corpora/validate.py` | **OK** (migration=6) | ✅ |
| `cd frontend && pnpm exec tsc --noEmit` | **clean** | ✅ |
| `cd frontend && pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT | ✅ |
| `python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md` | **101 · 93 · 4 · 0** (+4 unreadable, all closed) | ✅ |
| Six browser drives (U1–U7) | see §7.5 L27 | ✅ |
| `cd backend && go build ./... && go vet ./... && go test ./... -timeout 60m` | exit 0 at `20dd96f` | ⚠️ **carried forward, NOT re-run** |
| `make verify` | exit 0 at `20dd96f` | ⛔ **NOT run — it invokes `make migrate`, and the T8 lane owns the schema this week** |

⚠️ **`make verify` DOES NOT RUN `tsc`.** Each gate has caught a real error the other missed.

---

## 10. ⚠️ HOUSEKEEPING — THE HANDOFF SPRAWL IS NOW WORSE THAN THE ROW THAT TRACKS IT

```bash
ls HANDOFF-*.md | wc -l                                   # -> 27, this file included
sed -n '/^## 11/,+14p' HANDOFF.md | grep -o 'HANDOFF-[A-Z0-9-]*\.md' | sort -u | wc -l   # -> 11
```

`HANDOFF.md` §10 item 8 says *"delete the ELEVEN spent handoffs"* and §11 does name exactly eleven.
**There are 27 files on disk.** ⛔ **Sixteen are unlisted, and four of those sixteen are LIVE** —
`HANDOFF-PARITY-AUDIT.md`, this file, and (until their lanes land) `HANDOFF-T8-MULTI-CURRENCY.md` and
`HANDOFF-T32-REAL-FILE.md`. **So twelve unlisted files are candidates nobody has judged**, and the
drift is the finding each one recorded: every handoff re-derives context the last already held.

```bash
# the twelve to judge — on disk, not named in §11, not live
ls HANDOFF-*.md | grep -vxF -f <(sed -n '/^## 11/,+14p' HANDOFF.md | grep -o 'HANDOFF-[A-Z0-9-]*\.md' | sort -u) \
  | grep -vE 'PARITY-AUDIT|FEATURE-LOCK-LINE|T8-MULTI-CURRENCY|T32-REAL-FILE'
```

**Live and worth keeping:** `HANDOFF.md` (the fold-everything file), `HANDOFF-PARITY-AUDIT.md` (the
defect register and the drive ledger — this is the audit's actual product), `PLAN-TALLY-PARITY.md`,
`RESEARCH.md`, `docs/design/*`, `docs/statutory/*`, and **this file until §6's line is agreed.**

⛔ **Deleting the rest is a judgement about what documentation a team keeps and it belongs to whoever
owns the repo.** But it is now a real cost: a session that opens the wrong one loses an hour, and this
repository has already shipped two false regressions and one "69 open defects" report to a user that
way. **Re-derive the spent list before deleting — do not trust §11's eleven names.**
