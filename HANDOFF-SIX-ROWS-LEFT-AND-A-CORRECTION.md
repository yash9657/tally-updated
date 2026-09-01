# HANDOFF — SIX ROWS LEFT, NONE HIGH. AND A CORRECTION THIS FILE OWES YOU

**Written:** 2026-08-31, working tree CLEAN at `HEAD` · **Branch:** `session/t32-and-close-7-3`
**Read time: 9 minutes.** ⭐ **Every claim in §1, §3 and §4 was MEASURED AT `HEAD`** and the
command is beside it. §5 is carried from the register and says so.

⛔ **§4.1 CORRECTS A FALSE CLAIM AN EARLIER VERSION OF THIS FILE MADE.** Read it before you plan
anything: it is the clearest worked example in the repo of a real measurement supporting a wrong
conclusion.

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** It is self-contained.
§1–§2 get you a running app you can safely break. §3 is what is open. **§4 is the recommended
work.** §6 is the house standard your work must meet.

---

## 0. ⭐ THE ONE-LINE VERSION

**D112, D113, D115 and D116 are CLOSED** (`9177473`, `badea6f`, and this lane). The demo book produces all twelve monthly GSTR-3Bs and its
annual GSTR-9. **There is no HIGH row open. Six rows remain, all MEDIUM or LOW.**

⛔⛔ **THIS FILE'S ORIGINAL RECOMMENDATION WAS WRONG AND IS CORRECTED HERE.** It said payroll was
*"a whole plane the product that no gate, no probe and no audit has ever run against real data."*
**Not true** — `frontend/scripts/drive-payroll.mjs` is a full browser drive (now **38/38**) covering
employee → salary → election → pay run → register → payslip → ECR/ESI/Form 138 with **Postgres as
the oracle**, and the engine carries 72 + 36 test functions and all 19 golden corpora. The
measurement behind the claim (`employees = 0` on the demo book) was right; the inference from it was
not — **the drive builds its own tenant on purpose.** See **L34** for the full correction.

⭐ **WHAT IS ACTUALLY LEFT IS SMALL AND NAMED (§3). D109 is the best row to take** (§5).

⚠️ **TWO NARROW PAYROLL GAPS DO SURVIVE THE CORRECTION**, and neither is a plane: `drive-payroll.mjs`
was **not in the §1 gate list** (it is now), and the demo book still has no payroll data, so
`verify:demo` asserts nothing about it.

---

## 1. THE STATE — verbatim, and what each number should be

```bash
cd /Users/yashbhalgat/code/tally-updated
git log --oneline -1          # fix(D112): one server fact, two correct presentations
git status --porcelain        # (empty)
```

**Gate baselines at `HEAD`, ALL RE-RUN AT THAT COMMIT. Any red is yours.**

⚠️ **EVERY LINE BELOW IS RUN FROM THE REPO ROOT** and each `cd` is contained in its own subshell for
that reason — `cd frontend && … "$(cat frontend/.demo-company-id)"` looks right and cannot work,
because after the `cd` that path no longer exists. ⛔ **This bit me this session in a different way:
`cd X && nohup … &` runs the `cd` in the BACKGROUND SUBSHELL, so the parent shell never moves** and
the next command in the same block reads the wrong directory. Use absolute paths in those blocks.

```bash
(cd backend && go build ./... && go vet ./... && go test ./... -count=1)  # all green
bash backend/scripts/test_residue.sh                                     # OK: no residue
(cd frontend && pnpm exec tsc --noEmit)                                  # clean
(cd frontend && pnpm exec vitest run)                                    # 1847 passed / 114 files
(cd frontend && pnpm verify:demo "$(cat .demo-company-id)")              # 37/37  ⚠️ id is an ARGUMENT
node frontend/scripts/drive-t4-slice2b-matching.mjs                      # 35/35, tenant reaped
node frontend/scripts/drive-t12-gstr9.mjs                                # 33/33
node frontend/scripts/drive-t11-budgets.mjs                              # 36/36
node frontend/scripts/drive-payroll.mjs                                  # 38/38  ⚠️ needs vite too
python3 corpora/validate.py                                              # OK
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md            # 117 · 107 · 6 · 0 · 4
```

⛔ **THE BACKEND SUITE AND THE DRIVES NEED THE SERVICES UP (§2.1). The vitest and tsc lines do not.**

⚠️ **`verify:demo` IS 37/37 NOW, NOT 34/34.** Two checks were added closing D113 and one closing
D116 — see §4.3, which is the single most transferable thing in this file.

⚠️ **THE REGISTER PRINTS `4 UNREADABLE` AND THAT IS NOT WORK WAITING FOR YOU.** D49, D80, D82 and D84
are all CLOSED; the parser cannot read their status cells. ⛔ **Do not "fix" the parser casually** —
a rewrite that reconciles by counting looks right and classifies D13 off the wrong cell. It needs
its own pass.

---

## 2. ⛔ THE TRAPS THAT WILL COST YOU AN HOUR

*(All five are durable. All five have bitten this repo more than once. §2.1 and §2.5 both bit again
this session.)*

### 2.1 ⛔⛔ A STALE `cored` IS INDISTINGUISHABLE FROM A BROKEN FIX — AND `pkill` MISSES IT

`make run-cored` is `go run`, so the listening process is a compiled child with a different command
line. ⛔ **DO NOT USE STARTUP DURATION AS THE TEST** — with a warm build cache a genuinely fresh
`cored` also listens in about a second. ⭐ **CHECK THE PROCESS START TIME AGAINST THE CLOCK:**

```bash
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # is this NOW?
git log -1 --format='%ci' -- backend/                        # older than the process?
```

Kill by PORT, always, and start **cored first**:

```bash
kill -9 $(lsof -nP -iTCP:8087 -sTCP:LISTEN -t) 2>/dev/null
kill -9 $(lsof -nP -iTCP:8091 -sTCP:LISTEN -t) 2>/dev/null
sleep 2
nohup make run-cored > /tmp/cored.log 2>&1 &
for i in $(seq 1 90); do curl -sf localhost:8087/healthz >/dev/null && break; sleep 1; done
nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 &   # AFTER cored
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"; date
```

⛔ **CORED FIRST.** A cored restart mints a new ephemeral signing key, invalidating the service token
`statutoryd` minted from it. Both failures present as a bare `401`.

**Ports:** cored `:8087`, insightd `:8088`, statutoryd `:8091`, vite `:5173`.

⭐ **AND YOU MUST RESTART IT TO MUTATION-PROVE ANYTHING THAT RUNS OVER THE WIRE.** Closing D113
meant rebuilding cored with the fix removed to prove `verify:demo` actually catches it. A mutation
proof against a stale binary proves nothing and looks identical.

### 2.2 ⚠️ VITE IS `localhost`, NOT `127.0.0.1`

`--strictPort`; WebAuthn origin matching is exact and vite binds `::1`. Use `http://localhost:5173/`.
⚠️ **Restart vite if it has been running for days.**

### 2.3 ⛔⛔ AN EMPTY `?company=` MINTS A FRESH TENANT AND EVERY SCREEN LOOKS CLEAN

The app bootstraps an empty company called **"My Company"** when no company is named. **Two audit
rows were wrongly logged as passing that way.** Always pass `?company=<uuid>`, and read the **id**,
not the name — there are ~95 companies called "Demo Traders" among **394 companies** in the database.

### 2.4 ⛔ DIRTY A SCRATCH BOOK, NEVER THE DEMO ONE

```bash
(cd frontend && pnpm seed:demo -- --name "Your Scratch")   # -> .your-scratch-company-id
```

⭐ **The name and the id file move together.** With **no** `--name` the seeder writes
`.demo-company-id`, which every gate reads — that is how ten identically-named books accumulated.
After any session re-run `pnpm verify:demo "$(cat frontend/.demo-company-id)"` and expect **37/37**.

⚠️ **TWO SCRATCH BOOKS ALREADY CARRY DRIVE DATA — MAKE A FRESH ONE:**
`34aee254-9027-4641-ae2a-76d79420772b` (`D108 Fix Scratch`, 28 vouchers) and
`0f3ab0c1-0e4a-49b1-8fe0-27751dcdecf2` (`D113 Carry Scratch`, a clean seeder output kept as proof
the D113 fix holds on a freshly seeded book).

### 2.5 ⚠️ THE FIRST WRITE OF A SESSION CARRIES A WEBAUTHN CEREMONY

The Owner level requires step-up. In a hand-driven browser the first `POST /v1/…/vouchers/*` returns
**403** and the ceremony runs; that 403 is not a bug. Install a virtual authenticator over CDP
**before the first navigation**:

```js
const cdp = await page.context().newCDPSession(page);
await cdp.send("WebAuthn.enable", { enableUI: false });
await cdp.send("WebAuthn.addVirtualAuthenticator", { options: { protocol: "ctap2",
  transport: "internal", hasResidentKey: true, hasUserVerification: true,
  isUserVerified: true, automaticPresenceSimulation: true } });
```

⚠️ **THIS TRAP IS HOW D108 WAS FOUND.** Copy it verbatim. **A payroll session (§4) needs it** — a pay
run is a write.

---

## 3. ⛔ WHAT IS OPEN — measured 2026-08-31 at `HEAD`

```bash
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md
# 117 REGISTERED · 107 CLOSED · 6 OPEN · 0 PARTIAL · 4 UNREADABLE
#   OPEN: D99, D109, D110, D111, D114, D117
```

**⭐ NOTHING OPEN IS HIGH.** D113 was the last one. **D112, D115 and D116 were found or closed across
this session's three lanes; D117 was found in the second and is OPEN because its fix is a status
decision, not a message edit.**

| Row | Sev | One line | Shape of the work |
|---|---|---|---|
| **D109** | MEDIUM | A two-level report **exports flat**, so summing the money column returns exactly **2.000000×** the file's own total | A format decision (a Level column?) then a small change |
| **D110** | MEDIUM | The Bank Reconciliation screen says *"matching is not built yet"* and **Alt+S on that same screen opens it** | One sentence, but keep the half it gets right |
| **D111** | LOW | Every **pasted** bank statement is stored with `file_name = ''`, and the disclosure that names the unchecked file renders an empty string | One line, but **decide the name** |
| **D114** | LOW | The import panel says *"Esc goes back to the reconciliation statement"* and Esc goes to the **Gateway** | Pop one frame, or change the sentence |
| **D117** | LOW | A DB/plumbing failure reaches the filer as a **422** carrying a SQL-shaped sentence — *"a dead database is not an unprocessable entity"* | ⚠️ **A status decision (422 vs 500), not a message edit. Registered from a CODE READING and never driven — reproduce it first** |
| **D99** | LOW | ⛔ **OPEN BY DECISION, NOT BY OMISSION.** Its premise did not survive a real TallyPrime export and fixing it speculatively would make genuinely malformed numbers readable. **Leave it** | |

---

## 4. ⭐⭐ THE RECOMMENDED WORK — D109, AND WHY NOT PAYROLL

### 4.1 ⛔⛔ FIRST, THE CORRECTION THIS FILE OWES YOU

**An earlier version of this handoff recommended payroll, on the claim that no gate had ever run it.
That claim was false and it is worth understanding how it was reached, because the mistake is a
common one.**

The measurement was real: `employees = 0, pay_runs = 0` on the demo book, re-measured twice. The
INFERENCE was wrong — an empty demo book was read as an unexercised module. Measured properly:

```bash
grep -h "^func Test" backend/internal/payroll/*_test.go | wc -l              # 72
grep -h "^func Test" backend/internal/payroll/payrolldb/*_test.go | wc -l    # 36
ls corpora/payroll/*.json | wc -l                                           # 19, all run by
                                     # TestPayrollDerive_GoldenCorpus AND by corpora/validate.py
node frontend/scripts/drive-payroll.mjs                                      # 38/38 in a browser
```

`drive-payroll.mjs` drives **create employee → salary → statutory election → post a pay run → read
the register → drill a payslip → download the ECR, the ESI return and Form 138**, with **Postgres as
the oracle**. It builds its OWN tenant on purpose, which is exactly why the demo book is empty.

⭐ **THE LESSON, WHICH IS THE SAME ONE L32 AND L33 KEEP TEACHING: an absence of DATA is not an
absence of COVERAGE.** Check for the probe before concluding there isn't one — `ls
frontend/scripts/` would have taken four seconds.

⚠️ **TWO NARROW PAYROLL GAPS DO SURVIVE, and neither is a plane:**

1. `drive-payroll.mjs` **was not in the §1 gate list.** It is now. A drive nobody runs is a drive
   that rots, and its absence is precisely what let this file claim payroll was untested.
2. The demo book has no payroll data, so **`verify:demo` asserts nothing about payroll.** Seeding a
   couple of employees and one pay run would let it — a real, contained piece of work, but a small
   one, and NOT a plane of the product.

### 4.2 ⭐ SO THE RECOMMENDATION IS D109 — the flat export

**MEDIUM, the highest severity open.** A two-level report exports flat, so summing the money column
in the exported file returns exactly **2.000000×** the file's own stated total, because every parent
is duplicated by exactly one child.

⛔ **IT IS A FORMAT DECISION BEFORE IT IS A BUG FIX, and §6.1's rule applies:** decide what the
export MEANS (a Level column? parents omitted? an indent convention?) and record the decision with
whatever you relied on, before touching code. Its row names both affected reports — **Stock Summary
and Outstandings** — and warns that the exact `2.000000` ratio is a property of the current data,
not a law.

⚠️ **D110, D111 and D114 are each an hour** and all three are "a sentence promises something the
code does not do". **D117 needs reproducing before it needs fixing** — its row says plainly it was
registered from a code reading and never driven.

### 4.3 ⛔⛔ READ THIS BEFORE YOU WRITE A SINGLE PROBE — IT IS WHY D113 SURVIVED

**`verify:demo` reported 34/34 against a book that could not produce a single one of its twelve
monthly GST returns for July, nor its annual return at all.** Not because it skipped GST — it has two
careful table-4 identity checks. Because of **one line**:

```
/v1/companies/${co}/gstr3b?from=${FY.from}&to=${FY.to}      // the WHOLE YEAR, in one call
```

Over a year, July's credit note nets against April's invoice and every figure is positive. **A
full-year window cannot observe a month that fails on its own.** A filer files **twelve** 3Bs and
**one** GSTR-9. **Nobody has ever filed the window that gate was asking for.**

⛔ **AN AGGREGATE QUERY IS NOT A SAMPLE OF THE QUERIES A USER MAKES.** It is the query most likely to
be green, because summing is exactly what hides a bad summand.

⚠️ **AND IT HAS A TWIN:** `drive-t12-gstr9.mjs` passes 33/33 and mints its **own** company and its
own vouchers, so it never meets the shape the seeder ships. **Two green gates, two different ways of
never asking the real question.**

---

## 5. THE OTHER ROWS — carried from the register, not re-measured this session

Each has a full row in `HANDOFF-PARITY-AUDIT.md` §7.1 with its measurement, file anchors and the trap
in its fix. **Read the row before starting; each names something you would otherwise get wrong.**

- **D109** (MEDIUM) — the flat export. ⚠️ Both Stock Summary and Outstandings have the shape; the
  ratio is exactly **2.000000** because every parent is duplicated by exactly one child. **A Level
  column is a format decision**, not a bug fix — decide it. ⭐ **Best of the six if you want a row.**
- **D110** (MEDIUM) — the stale sentence on Bank Reconciliation. ⚠️ **Keep the half it gets right:**
  the paragraph exists to stop an operator believing the Bank Date ticks are automatic, and the
  matcher does not tick anything either. Its row records the matcher itself is excellent and all
  three of its invariants hold — *a defect row is not a verdict*.
- **D111** (LOW) — the nameless pasted statement. ⚠️ **Do not copy the preview's fallback**
  (`pasted.csv`): two pasted statements would be indistinguishable again, the same defect one step
  over.
- **D114** (LOW) — Esc unwinds to the Gateway. ⚠️ Check the Alt+S match panel at the same time; its
  footer makes the same promise.
- **D99** (LOW) — ⛔ **leave it.** Open by explicit decision, recorded in §7's L27.

---

## 6. ⛔⛔ THE HOUSE STANDARD YOUR WORK MUST MEET

This is not style. Rows have been re-opened in this repo for failing each of these.

- [ ] **A failing test written FIRST**, asserting the thing no existing test covers.
- [ ] ⛔ **MUTATION-PROVED.** Revert the fix and watch the new test go red **with the number the real
      system produced.** A test that passes before the fix is not a test. ⭐ **L32's standard:** three
      mutations, one of which reproduced the original wire sentence *verbatim*.
- [ ] ⛔ **DRIVEN AGAINST THE REAL SYSTEM**, on a **fresh scratch book** (§2.4), against a cored
      verified fresh (§2.1). **The oracle is `psql` or the wire, never the screen.**
- [ ] ⚠️ **DRIVE THE CONTROL TOO.** The thing most likely to be broken by a fix is the behaviour the
      fix is adjacent to. L32's control asserted that a month which never went negative emits **no
      new JSON key at all** — absent, not present-and-empty.
- [ ] ⛔ **AND MUTATION-PROVE THE GATE, NOT ONLY THE CODE.** If you add a check to `verify:demo`,
      rebuild cored with the fix removed, restart it, and confirm the gate goes red **and that your
      new checks are the only reds.** L32 scored 34/36 doing this — the same 34 it scored while
      broken. **That is the proof a gate is real.**
- [ ] Gates in §1 re-run and **stated in the commit message, with every deferral named.**
- [ ] `HANDOFF-PARITY-AUDIT.md` row moved to CLOSED **with the measurement, the mutation proof and
      the drive recorded in the house format**, plus a `### L33 — …` lane section at the end of §7.
      (**`L32` is the template; copy its shape.**)

### 6.1 ⭐ THE LESSON FROM `L32` THAT OUTLIVES THE FIX

**A one-line fix was available, obvious, and would have produced a document the GST portal refuses.**
Widening the failing comparison stops the abort in ten seconds and leaves −12,000.00 in a table that
admits no negative — converting a loud failure into a silent one.

⭐ **The fix was a DECISION, and the decision needed a SOURCE.** The rule came from the GST portal's
own FAQ and Circular 26/26/2017-GST, **both quoted at the code** so the next reader does not take a
destroyed figure on trust. ⛔ **And the clamp alone would have been the silent version of the same
defect** — zeroing the negative destroys the filer's credit note — so the *carry* is the fix and the
clamp is only its first half.

**When a validator refuses something, ask whether it is right that something is wrong before you
change what it compares.** Here it was right, and its sentence was about the wrong table.

---

## 7. ⚠️ STILL BLOCKED, AND WHY — do not pick these up expecting to finish

**T13 interest engine · EPF/EDLI admin charges · portal verification · DPDP · T4 slice 2c.**
All five are blocked for reasons recorded in `HANDOFF-USABILITY-DAY-TWO.md` §8. Read that before
assuming any is available. ⚠️ **"Portal verification" is now load-bearing for GST:** L32 read the
no-negative rule from the portal FAQ and a CBIC circular, and **still nothing here has been submitted
to a portal.** Any row that claims portal conformance is claiming more than this repo can support.

---

## 8. THE READING LIST, in order

1. **This file.**
2. `HANDOFF-PARITY-AUDIT.md` §7's **`### L32`** — the house format you must copy, and §4.3's lesson
   in full. Then **`### L34`**, for the correction in §4.1 and for a fix that needed three layers.
3. `backend/internal/compliance/gstr/gstr3b.go` — `clampNegatives` and its doc comment, for how a
   decision with an external source is recorded at the code.
4. `frontend/scripts/verify-demo.mts` §8b and §8c — the checks that close the aggregate-query hole
   and the package-prefix leak, with the comments explaining why each gate was green while broken.
   ⭐ **Both were mutation-proved by rebuilding cored broken and watching them fail (§6).**
5. `frontend/scripts/drive-payroll.mjs` — the browser drive §4.1 is about, and its §6b (D112).
   ⭐ **Read its header**: it explains why it builds its own tenant, which is the fact an earlier
   version of this handoff misread as "payroll is untested".
6. `docs/usability-day-2026-08-30.md` §6.6 — what the 2026-08-31 pass did **not** cover.
