# D109 — what a two-level report MEANS when it is written to a spreadsheet

**Written:** 2026-09-04, at `30d3577`, **before the code change**, because §6.1 of
`HANDOFF-SIX-ROWS-LEFT-AND-A-CORRECTION.md` says so: *"decide what the export MEANS (a Level
column? parents omitted? an indent convention?) and record the decision with whatever you relied
on, before touching code."*

⭐ **Every figure below was measured on the demo book at `HEAD`**, by opening the file Alt+E writes
rather than by reading the API. The commands are beside them.

---

## 1. THE DEFECT, IN ONE LINE

**Stock Summary and Outstandings each render a PARENT row followed by its CHILDREN, and the exported
file says nothing about which is which — so summing the money column returns exactly twice the
file's own stated total.**

```
$ CO="$(cat frontend/.demo-company-id)" node frontend/scripts/browse-closed-rows.mjs
  Outstandings.csv                  Total=259820.0000 · Σ Party=2598200000 · Σ Bill=2598200000
                                    Σ everything=5196400000 = 2 × 2598200000
  Stock-Summary-no-date-limit.csv   Stock-in-Hand=75094.9091 · Σ Item=750949091 · Σ Location=750949091
```

⚠️ **THE SUMS ARE PRINTED IN INTEGER PAISE** (fixed-4 units, `BigInt`) and the stated totals in
rupees, because a probe about a money column that adds with `+` is not a probe about money:
`2598200000` units is `259820.0000`.

⛔ **THE SCREEN DOES NOT HAVE THIS PROBLEM, AND THE REASON IS THE WHOLE DEFECT.** The two levels are
distinct **in CSS and only in CSS**: the Stock Summary's item `td` is `font-bold text-tally-title`
and its location `td` is `pl-4`; the Outstandings bill row's label cell is `pl-4` and its figures are
`text-tally-field`. Every cue that says *"this is a child, already counted above"* is a class name,
and **CSS does not survive Alt+E**.

⚠️ **AND THE FILE'S PURPOSE IS THE THING IT GOT WRONG.** The exporter states it out loud, in the
comment that gave base-unit quantity its own column: *"a numeric column with prose in it is a column
nobody can sum, and this file's WHOLE PURPOSE IS TO BE SUMMED."*

---

## 2. ⛔ THE DUPLICATION IS NOT THE BUG

An item total and its godown positions are **both facts**, and the screen shows both deliberately —
that is what makes it a two-level report rather than two reports. A party's balance is the figure a
Balance Sheet already gives; the bills under it are the ageing, which is the only place in the
product that says WHICH invoice is open.

**So the fix must leave the file summing to 2× when you sum the whole column.** What was missing is
not a row — it is a **declaration**. A test asserts this explicitly
(`export/twoLevelExport.test.tsx`, the `CONTROL: the whole column is still 2x` arms), because a
"fix" that made the naive sum foot would have silently deleted one of the two levels.

---

## 3. THE THREE OPTIONS THE REGISTER NAMED, AND WHY (a)

| | option | summable by SUM()? | survives to the eye? | cost |
|---|---|---|---|---|
| **(a)** | **a `Level` column naming the row kind** | ⭐ **yes** — filter, then sum | yes, it is a column | one column wider |
| (b) | indent or prefix the label | ⛔ **no** | yes | changes the label, which is data |
| (c) | blank the numeric cells on parent rows | yes, trivially | yes | ⛔ **loses the item total the screen deliberately shows** |

**(a) is the only one that makes the file summable, which is its stated purpose.** (c) makes it
summable by deleting half the report. (b) is what a person can see and a spreadsheet cannot.

⚠️ **THE WEAK DISCRIMINATOR THAT ALREADY EXISTED IS NOT AN ARGUMENT AGAINST (a).** `Basis` is filled
on a Stock Summary item row and empty on a location row; Outstandings' second column holds a group
name on a party row and a due date on a bill row. **Neither is declared anywhere in the file, neither
is in the money column, and no SUM respects either — and the Outstandings one FAILS OUTRIGHT on a
bill with no due date**, which the demo book has: two invoices exported as `Sales/2026-27/4`, an
empty cell, `11800`. That case is a fixture in the test suite for exactly this reason.

---

## 4. THE DECISIONS, STATED SO THEY CAN BE ARGUED WITH

1. **The column is called `Level`.** It extends the convention the Outstandings header already
   adopted when it was widened to *"Party / Bill ref"* — the header names both row kinds, and now
   the rows say which one they are.
2. **Its vocabulary is per report, and it is the report's own words:** `Item` / `Location` / `Total`
   on the Stock Summary; `Party` / `Bill` / `Total` on Outstandings and Bills Payable. A shared
   vocabulary (`parent`/`child`) was rejected: an operator filtering a Stock Summary is looking for
   locations, not for children.
3. ⛔ **`Location` and not `Godown`/`Batch`.** The label cell already carries both (a batch is
   appended to its godown with a `·`), and a level whose vocabulary depends on the row's contents is
   not something a spreadsheet filter can be written against once.
4. ⛔ **THE TOTAL ROW DECLARES ITS LEVEL TOO.** A total swept into a sum of item rows is the same
   doubling one row smaller, and it is the mistake somebody makes on their first try.
5. **It is the SECOND column**, beside the label it qualifies, and not the first: every export in
   this product opens with the thing the row is about.
6. ⛔ **IT IS NOT ADDED TO THE SCREEN.** The screen never had the defect — the CSS is still there
   and still says it — so `STOCK_SUMMARY_SCREEN_HEADERS` is derived from the export list minus this
   one column. **That asymmetry is deliberate and it is the only one**: Alt+E's identity is "write
   what is displayed", and this is the one thing displayed that a file cannot carry any other way.

---

## 5. ⛔ WHAT THIS DOCUMENT DOES **NOT** DECIDE — and it is now a register row

**The statement trees have the same disease and a worse version of it**, measured the same way on
the same book:

```
Balance-Sheet.csv   Σ Amount = 3,135,449.4546   against a stated section total of 746,314.9091
                    ratio 4.2013…, i.e. NOT a constant a reader could spot
Profit-and-Loss.csv Σ Amount =   837,168.7273   against Income 224,594.9091 + Expenses 163,384.0000
Trial-Balance.csv   Σ Debit  = 1,708,730.2400   = 2 × the file's own Total row, which is unmarked
```

⛔ **IT IS DELIBERATELY OUT OF SCOPE HERE AND WAS REGISTERED AS D118 INSTEAD**, for the reason D117
was registered rather than folded into D116: **the fix is a different decision, not the same one
twice.** A statement tree is not two levels — it is a section header, groups nested to arbitrary
depth, `COMPUTED` rows, and a re-parenting rule (`condense`) that changes which rows are present at
all, and a `Level` column with a two-word vocabulary cannot describe it.

⭐ **D118 WAS TAKEN IMMEDIATELY AFTER AND IS CLOSED** — `docs/design/d118-statement-tree-export.md`.
Its answer is a row KIND plus a **`Parent`** column, at the shared `DrillRow` → `reportSheet` layer,
**and this report was migrated onto that mechanism with it**: the `Level` cell below is now declared
once per row instead of being written by hand into `exportCells` and into the headers, and the
Stock Summary gained the `Parent` column too. ⚠️ **THE "DEPTH" THIS SECTION PREDICTED WAS THE WRONG
ANSWER** and D118 §2.1 records why.
