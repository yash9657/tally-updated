# HANDOFF — THE REGISTER HAS ONE OPEN ROW LEFT, AND IT IS OPEN BY DECISION

**Written:** 2026-09-04, describing the L40 lane · **Branch:** `session/t32-and-close-7-3`
**Read time: 6 minutes.** ⭐ **Every claim in §1 and §3 was MEASURED at `HEAD`** and the command is
beside it.

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** For the environment, the traps and
the house standard, `HANDOFF-SIX-ROWS-LEFT-AND-A-CORRECTION.md` §1–§2 and §6 are still correct and
are not repeated here. **What has changed is §3 of that file: five of its six rows are closed.**

---

## 0. ⭐ THE ONE-LINE VERSION

**D109, D110, D111, D114 and D117 are CLOSED (L40); D118 — found while closing D109 — was registered
and then CLOSED too (L41).** The register reads **118 · 113 · 1 · 0 · 4**.

⛔ **THE ONE OPEN ROW IS D99, AND IT IS OPEN BY DECISION**, recorded in L27 and re-affirmed here: its
premise did not survive a real TallyPrime export (1,156 amounts, 714 opening balances, 454
quantities, not one comma), and "fixing" it would make `1,2,3.00` a readable number. **Leave it.**

⭐ **SIX REPORTS NOW DECLARE THEIR LEVELS IN THEIR EXPORTS** (Stock Summary, Outstandings, Bills
Payable, Balance Sheet, Profit & Loss, Trial Balance, Unadjusted Forex), and the two statements name
their VIEW in the file name. §2 is the one idea worth carrying forward; §4 is what is left.

---

## 1. THE STATE — verbatim, and what each number should be

```bash
cd /Users/yashbhalgat/code/tally-updated
git status --porcelain        # (empty, once the L40 lane is committed)
```

**Gate baselines at `HEAD`. Any red is yours.** ⚠️ Everything below is run from the REPO ROOT.

```bash
(cd backend && go build ./... && go vet ./... && go test ./... -count=1)  # all green
bash backend/scripts/test_residue.sh                                     # OK: no residue
(cd frontend && pnpm exec tsc --noEmit)                                  # clean
(cd frontend && pnpm exec vitest run)                                    # 1940 passed / 121 files
(cd frontend && pnpm verify:demo "$(cat .demo-company-id)")              # 37/37
node frontend/scripts/drive-t4-slice2b-matching.mjs                      # 35/35, tenant reaped
node frontend/scripts/drive-t12-gstr9.mjs                                # 33/33
node frontend/scripts/drive-t11-budgets.mjs                              # 36/36
node frontend/scripts/drive-payroll.mjs                                  # 38/38
CO="$(cat frontend/.demo-company-id)" \
  node frontend/scripts/browse-closed-rows.mjs                      # 32/32  ⭐ NEW THIS LANE
python3 corpora/validate.py                                              # OK
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md            # 118 · 113 · 1 · 0 · 4
```

⛔ **THE BACKEND SUITE AND EVERY DRIVE NEED THE SERVICES UP; `tsc` AND `vitest` DO NOT.** Ports:
cored `:8087`, insightd `:8088`, statutoryd `:8091`, vite `:5173`. Start **cored first** (a restart
mints a new signing key and invalidates statutoryd's service token; both failures present as a bare
`401`), and check the process START TIME against the clock rather than the startup duration.

⚠️ **`browse-closed-rows.mjs` IS THE GATE FOR THIS LANE AND IT IS NOT IN `verify:demo`.** Four
of the five rows are defects OF THE SURFACE that no wire gate can see. **A drive nobody runs is a
drive that rots** — that is L34's own lesson and it is why this line is in the list above.

⚠️ **THE REGISTER PRINTS `4 UNREADABLE` AND THAT IS NOT WORK.** D49, D80, D82 and D84 are all CLOSED;
the parser cannot read their status cells. ⛔ **Do not "fix" the parser casually** — a rewrite that
reconciles by counting looks right and classifies D13 off the wrong cell.

⛔ **AND THE PARSER READS THE STATUS CELL WITH `\bOPEN\b`, CASE-SENSITIVELY.** Writing the word
`OPEN` in capitals anywhere in a CLOSED row's status cell makes that row **UNREADABLE**. It happened
to D114 in this lane; the sentence was reworded. **Re-run the parser after every register edit.**

---

## 2. ⭐⭐ WHAT WAS BUILT, AND THE ONE IDEA WORTH CARRYING FORWARD

Full detail is in `HANDOFF-PARITY-AUDIT.md` **§7 L40 and L41** and in each row. The single most
transferable thing in the two lanes:

> ⛔⛔ **THE DUPLICATION WAS NOT THE DEFECT. THE ABSENCE OF A DECLARATION WAS.**

Stock Summary and Outstandings each show a parent and its children, and both are facts the screen
shows on purpose. The fix therefore had to leave the naive whole-column sum at **exactly 2×**, and
there is a test asserting it does:

```
✓ D109: ⚠️ CONTROL — the whole money column is still exactly 2x, as it should be
```

A "fix" that made that sum foot would have satisfied every other assertion and **silently deleted a
level of each report.** The same shape recurs in three other places in this lane: D110's replacement
sentence had to keep saying nothing is ticked automatically; D114's Esc had to keep leaving the
screen when no panel is open; D117's 500 had to leave the 422s and 400s exactly where they were.
**In each one the control is more work than the fix, and in each one the control is the thing that
would have caught the plausible wrong version.**

⭐ **AND THE SECOND IDEA, FROM L41: A FIX CAN ARRIVE WITH ITS OWN HAZARD, AND IT SHIPS WITH THE FIX.**
D118's `Parent` column made a per-group sum possible for the first time — and on the CONDENSED
Balance Sheet one of those sums is **75,094.9091 against a stated 746,314.9091**, because the
condensed view keeps a deep non-drillable row and its ancestors while dropping its siblings. Nobody
could have got that wrong number before the fix. So the two statements now name their view in the
file itself (`Balance-Sheet-condensed.csv`), **on both views, because "no suffix" is the silence that
makes a condensed file look complete.** ⚠️ **Ask of every fix: what can somebody now do that they
could not do before, and what is the first thing they will get wrong?**

---

## 3. WHAT IS OPEN

| Row | Sev | One line | ⚠️ Before you start |
|---|---|---|---|
| **D99** | LOW | Money readers reject `83,650.00`; the quantity reader strips commas | ⛔ **OPEN BY DECISION. Leave it.** L27, re-affirmed in L40 |

**That is the whole list.** ⛔ **A REGISTER WITH NOTHING OPEN IN IT IS NOT A STATEMENT THAT THE
PRODUCT HAS NO DEFECTS** — it is a statement that nobody has looked lately. §4 is where to look.

---

## 4. ⭐ THE RECOMMENDED WORK — GO AND FIND THE NEXT ONE, AND HERE IS WHERE

**D118 was found by asking D109's own question of the reports D109 did not name**, and it took about
twenty minutes: export each report, sum its money column, compare with the figure the file itself
states. **Two lanes' worth of defects came out of one question asked repeatedly.**

### 4.1 ⭐⭐ THE SAME QUESTION, NOT YET ASKED OF THE SEVEN REPORTS WITH THEIR OWN SHEET BUILDERS

Alt+E has **two** paths. `useReportView` covers seven screens and all of them have now been checked.
The other path is `useExportAction` with a screen-owned builder, and it covers the TDS/TCS register,
the two order books, the two cost summaries and the Stock Summary. **Only the Stock Summary has been
audited** (D109).

```bash
# The recipe, verbatim, on a book with data in it:
CO="$(cat frontend/.demo-company-id)" node frontend/scripts/browse-closed-rows.mjs   # the pattern
# then, per report: does Σ(money column) equal the figure the file itself states?
```

⚠️ **A COST SUMMARY IS A TREE** (category → centre) and both order books have totals. **Neither has
ever been summed.**

### 4.2 THE TWO SMALLER THINGS, BOTH REAL AND NEITHER A ROW

1. **`browse-closed-rows.mjs` is not in `verify:demo`.** Nothing asserts it runs, and it is now the
   only gate for six closed rows. ⛔ **A drive nobody runs is a drive that rots** — L34's lesson, and
   the reason it is in §1's list at all.
2. **The demo book still has no payroll data**, so `verify:demo` asserts nothing about payroll
   (carried from L34; still true).

### 4.3 ⚠️ AND READ THE TWO MEMOS BEFORE TOUCHING AN EXPORT

`docs/design/d109-two-level-export.md` (§3 is the options table) and
`docs/design/d118-statement-tree-export.md` (§2.1 is why there is no `Depth` column, §3 is the
hazard the fix created and what was done about it). **Between them they are the format decision for
every report in the product**, and the vocabulary is in D118 §6.

---

## 5. ⛔ THE HOUSE STANDARD YOUR WORK MUST MEET

Unchanged, and every item was exercised this lane:

- [ ] **A failing test written FIRST.**
- [ ] ⛔ **MUTATION-PROVED** — revert the fix and watch the test go red **with the number the real
      system produced.** L40 did this five times; two of them reproduced the original measurement
      *verbatim* (`stored ""`, and the 422 carrying `SQLSTATE 42703`).
- [ ] ⛔ **DRIVEN AGAINST THE REAL SYSTEM.** The oracle is `psql` or the wire, **never the screen**.
- [ ] ⚠️ **DRIVE THE CONTROL TOO.** See §2 — the control is usually the larger half.
- [ ] ⛔ **A FORMAT OR STATUS DECISION IS RECORDED BEFORE THE CODE.** D109's memo is the template.
- [ ] Gates in §1 re-run and **stated in the commit message, with every deferral named.**
- [ ] `HANDOFF-PARITY-AUDIT.md` row moved to CLOSED in the house format, plus an `### L<n>` lane
      section. **Then re-run the parser** (§1's last trap).
