# D118 — what a STATEMENT TREE means when it is written to a spreadsheet

**Written:** 2026-09-04, **before the code change**, for the reason
`docs/design/d109-two-level-export.md` was: a format decision is not a bug fix, and this one is a
*different* decision from D109's rather than the same one twice.

⛔ **IT CORRECTS TWO CLAIMS D118's OWN REGISTER ROW MADE A FEW HOURS EARLIER**, and both are the same
kind of mistake: a figure that read like a measurement and was never run. The shared layer is called
by **seven** screens, not "about twenty" (`grep -n "useReportView(" `), and the honest column is a
row KIND plus a **PARENT**, not a depth — §2.1 is why.

⭐ **Every figure below was measured by opening the file Alt+E writes** on `Demo Traders 45f69de4`,
not by reading the API.

---

## 1. THE DEFECT

```
Balance-Sheet.csv    Σ Amount = 3,135,449.4546  vs a section total of 746,314.9091   ratio 4.2013…
Profit-and-Loss.csv  Σ Amount =   837,168.7273  vs Income 224,594.9091 + Expenses 163,384.0000
Trial-Balance.csv    Σ Debit  = 1,708,730.2400  = 2 × its own Total row, which is unmarked
```

⛔ **IT IS WORSE THAN D109 IN THE ONE WAY THAT MATTERS.** D109's exact `2.000000` is at least
suspicious to a reader who knows the true total. **4.2 is not a number anybody recognises**, and the
Balance Sheet has **no total row of its own** — its section headers ARE the totals, unmarked — so
unlike D109's two reports the file carries no stated figure to contradict itself with.

⚠️ **AND `depth` IS ALREADY ON EVERY ROW.** `flattenNodes` sets it, `condense` reads it,
`exportRowFor` drops it on the floor. The file is missing something the client already knows.

---

## 2. ⛔ WHY D109's ANSWER DOES NOT FIT, AND WHAT THIS ONE ADDS

D109's reports are **two levels and exactly two**: item→location, party→bill. One `Level` column with
a three-word vocabulary describes them completely.

A statement is not that. It is a **section header, groups nested to arbitrary depth, `COMPUTED` rows
that are neither, and a re-parenting rule (`condense`) that changes which rows are in the file at
all.** Knowing a row is a `Group` does not tell you whose total it is part of — and on a three-deep
tree that is the only question a spreadsheet can act on.

**So this decision adds a second column, and the pair is the answer:**

| column | what it answers | why the other one cannot |
|---|---|---|
| **`Level`** | *what kind of row is this?* — `Section`, `Group`, `Ledger`, `Computed`, `Total` | tells you which rows to EXCLUDE (headers and totals), and nothing about nesting |
| **`Parent`** | *whose total is this row's amount part of?* — the label of the enclosing row | tells you which rows to SUM, at any depth, without knowing the tree |

**Together:** `SUMIF(Parent, "Liabilities", Amount)` = `502,000 + 183,104 + 61,210.9091` =
**746,314.9091**, which is the `Liabilities` row itself. That is the file doing its job.

### 2.1 ⛔ WHY NOT A `Depth` COLUMN

It was the obvious third column and it is deliberately absent. **It is derivable from `Parent`** (walk
the links), it answers no question `Parent` does not answer better — *"sum the rows at depth 2"* is
never what anybody means; *"sum the rows under Current Assets"* is — and it costs something real:
**an integer column sitting beside a money column in a file whose whole purpose is to be summed.**
This repository already refused a numeric column nobody should sum once, in the Stock Summary's own
words. It is not adding one here.

### 2.2 ⛔ WHY NOT AN INDENT ON THE LABEL

Same answer as D109 §3: an indent survives to the eye and not to `SUM()`, and it changes the label,
which is data a reader may be matching against another system.

---

## 3. ⛔⛔ THE HAZARD THIS CHANGE CREATES, AND WHAT IS DONE ABOUT IT

**A `Parent` column makes a per-group sum possible. On a CONDENSED export, one of those sums is
wrong.** Measured:

```
Balance-Sheet.csv (condensed, the DEFAULT view)
  rows with Parent = "Current Assets"  ->  Stock-in-Hand 75,094.9091
  the Current Assets row itself        ->              746,314.9091
```

⛔ **WHY, AND IT IS NOT A BUG IN `condense`.** The condensed view drops drillable rows deeper than
level 1 **but keeps every non-drillable row at any depth, plus its chain of ancestors** — that rule
exists because `Closing Stock` (a `COMPUTED` row three deep, and the largest asset a trading company
owns) was otherwise absent from the statement AND from its export. So `Stock-in-Hand` is in the file
only as `Closing Stock`'s ancestor, and its three siblings — `Bank Accounts`, `Cash-in-Hand`,
`Sundry Debtors` — are not. **The branch is partially expanded, and before this change nothing could
even attempt the sum that gets it wrong.**

⭐ **THE FIX IS TO MAKE THE FILE SAY WHICH VIEW PRODUCED IT, IN ITS OWN NAME:**

```
Balance-Sheet-condensed.csv     the default view: only the TOP level under each section is complete
Balance-Sheet-detailed.csv      Alt+F1: every level is complete and every Parent sum foots
```

**Precedent:** the Stock Summary already puts its scope in its title for exactly this reason — a file
called `Stock-Summary.csv` with no date on it is indistinguishable from one taken at a year end, so
its title says `(no date limit)` and its filename carries it.

⚠️ **AND THE SUFFIX IS ON BOTH VIEWS, NOT ONLY THE DANGEROUS ONE.** "No suffix" is precisely the
silence that makes a condensed file look complete, and a reader cannot tell a file that is complete
from a file whose author did not think about it.

⭐ **THE TOP LEVEL IS ALWAYS COMPLETE IN BOTH VIEWS**, because `condense` never drops a depth-1 row —
so `SUMIF(Parent, "<section>")` foots in a condensed file too. **That is the sum this column exists
for; the deeper ones are the ones the filename warns about.**

⛔ **THE SUFFIX GOES ON THE TWO STATEMENT SCREENS ONLY.** They are the only reports in the product
that nest past depth 1, so on any other report the word "condensed" would describe nothing and would
be noise on a file that has no such hazard.

---

## 4. WHICH REPORTS OPT IN — AND THE TWO THAT DO NOT, MEASURED RATHER THAN ASSUMED

Seven screens call `useReportView`. **`grep -n "__total__" ReportScreens.tsx ReportDetailScreen.tsx`
returns three hits**, and nesting exists on three screens:

| report | nests? | total row? | opted in |
|---|---|---|---|
| Balance Sheet | ⭐ to depth 3 | no (sections ARE the totals) | **yes** |
| Profit & Loss | ⭐ to depth 3 | no (`Net Profit` is `Computed`) | **yes** |
| Trial Balance | no | ⭐ yes, unmarked | **yes** — `Σ Debit = 2 ×` it |
| Outstandings / Bills Payable | to depth 1 | ⭐ yes | **yes** — migrated off D109's hand-rolled cell |
| Unadjusted Forex Gain/Loss | no | ⭐ yes, unmarked | **yes** — same shape as the Trial Balance |
| Day Book | no | **no** | no — flat, nothing to declare |
| Report detail (drill-down) | no | **no** | no — flat, nothing to declare |

⚠️ **THE DAY BOOK AND THE DRILL-DOWN ARE OUT ON EVIDENCE, NOT ON A HUNCH.** A flat report with no
total row has no row that is anybody's subtotal, so a `Level` column of one repeated word and a
`Parent` column of empties would be two columns saying nothing — the noise this document's §2.1
refuses for `Depth`. **If either ever grows a total row it must opt in**, and the vocabulary is here.

---

## 5. THE MECHANISM — ONCE, AT THE SHARED LAYER

`DrillRow` gains an optional `level`. `useReportView`'s meta gains `levels: true`. When set,
`reportSheet` **splices both columns in after the label** — the header pair and the cell pair
together, so a screen cannot supply one without the other and `toCsv`'s width refusal cannot be
tripped by half a change.

⛔ **`Parent` IS DERIVED AND NOT DECLARED**, from `depth` and row order over the rows that are IN THE
FILE (after Ctrl+F, F12 and Alt+F1 have been applied) — the nearest preceding row that is shallower.
Declaring it per screen would be a second copy of the tree walk `condense` already does, and the two
would drift the first time a screen re-parented anything.

⛔ **`Level` IS DECLARED AND NOT DERIVED.** It could be inferred from the `rowKey` prefix, and that
inference would be wrong the first time a screen chose a prefix for a navigation reason — which is
what those prefixes are for. It is one word at the site that already knows the answer.

⚠️ **THE SCREEN DOES NOT GAIN THE COLUMNS.** Same rule as D109: on screen the levels are in the
indentation and in the CSS, and they were never the defect.

---

## 6. THE VOCABULARY, IN EACH REPORT'S OWN WORDS

| report | `Level` values |
|---|---|
| Balance Sheet, Profit & Loss | `Section`, `Group`, `Ledger`, `Computed` |
| Trial Balance | `Ledger` or `Group` (whichever F5 is showing — it matches the column header), `Total` |
| Outstandings, Bills Payable | `Party`, `Bill`, `Total` |
| Unadjusted Forex Gain/Loss | `Bill`, `Total` |
| Stock Summary *(its own builder, D109)* | `Item`, `Location`, `Total` |

⛔ **ONE VOCABULARY PER REPORT AND NOT ONE FOR THE PRODUCT** (`parent`/`child`): an operator filtering
a Stock Summary is looking for locations, not for children. The words are the ones already printed at
the top of the screen they came from.
