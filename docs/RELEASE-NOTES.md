# Release notes

Operator-facing consequences of a change. ⛔ **A change belongs here when it moves a
number an operator has already read, or removes/renames something they rely on** —
not merely because it was large. The register (`HANDOFF-PARITY-AUDIT.md` §7.1) records
what was wrong; this file records what a person will notice.

---

## Tally XML import now reads a file TallyPrime actually wrote

**Applies to:** migrating an existing company in — Gateway of Tally > Export > Masters
and > Day Book, XML — and the `POST .../import/tally-xml` endpoint behind it.
**Register:** T32.

### What changes

The importer had never been given a genuine export. Everything it had been built and
tested against was hand-authored — the shape an integrator SENDS Tally, not the shape
Tally EMITS — and the two differ enough that a real file did not import at all.

**A real export now imports.** The one it was fixed against carried 3,706 ledgers, 37
account groups and 121 vouchers; it goes in and the Trial Balance comes back balanced to
the paisa against the same figures recomputed straight from the XML.

⛔ **If you tried to import a Tally file before and were told it worked, check what
landed.** The most serious of the defects was silent: an export in TallyPrime's own
envelope parsed without error, produced **zero** vouchers, and reported success. An
import that appeared to do nothing did in fact do nothing, and one that appeared to
succeed may also have done nothing. Re-run it.

The rest were loud but total — the file was rejected before a single voucher was read:

- Tally writes **UTF-16**, which was read as UTF-8 and failed on the first two bytes.
- Tally puts **U+0004 inside ordinary field values** ("&#4; Not Applicable"), which XML
  forbids and the parser refused. The two files tested against held 32,464 of them.
- A masters export of a nine-year-old company is **84 MB**; the endpoint capped bodies at
  32 MB and answered 413. The cap is now 128 MB.

And three were quiet and would have produced WRONG BOOKS rather than no books:

- On an **item invoice**, the sales and purchase amounts are recorded inside the stock
  lines, not beside the party. Reading only the top level left 63 of those 121 vouchers
  missing their entire revenue side.
- A **round-off line** states one side in its flag and the other in its sign. The sign is
  the truth; reading the flag put 25 lines on the wrong side.
- A ledger the company **already has** — Cash is seeded into every company — kept its
  zero opening while the export's real opening was dropped. That was ₹14,14,596.01 of
  cash in the file tested against.

### What to check after importing

**Read the warnings.** The response now carries a `warnings` list, and an import can
finish with correct totals and still not be a perfect copy of your books. Two things a
real file does that cored cannot hold exactly:

- an opening balance whose **bill-by-bill breakup does not add up** to it, or which mixes
  amounts you owe with amounts owed to you on one party. The **total is kept** and the
  breakup is dropped — so that party's balance is right and its ageing needs re-entering.
  In the file tested against this was 19 of 3,706 ledgers.
- a ledger whose Tally **group is not in the file** (an export of ledgers without their
  groups). It is filed by inference and the warning names it.

Your own account groups come across: the nine user-defined groups in the file tested
against were created, and Tally's built-in ones were matched to the ones cored already
seeds rather than duplicated — including "Cash-in-hand", which Tally spells with a small
h and cored with a capital.

⚠️ **Import into a FRESH company and reconcile before going live.** Each voucher is its
own transaction, so a run that stops part-way leaves what it already posted.

⚠️ **GST is not exercised by this evidence.** The company the fixes were proved against
trades GST-free and zero-rated goods, so not one voucher in it carried a CGST/SGST/IGST
line. Tax-bearing invoices still import through the path the existing corpora cover, but
they have not met a real file yet.

---

## "Bills Payable" is a report of its own, and the words for it now open it

**Applies to:** Go To / Switch To, the report list, printing, and access rules.
**Register:** D94.

### What changes

The ageing of what you OWE has always been computed — the server has served it since the
report existed — but nothing in the product could ask for it. There was one screen,
"Outstandings", and it showed what customers owe YOU.

**There is now a second report, "Bills Payable".** Press **Alt+G** and type `bills
payable`, `payables`, `accounts payable` or `creditors ageing` and you land on it: your
suppliers, bill by bill, with due dates and ageing slabs, and Ctrl+P prints it.

⛔ **`bills payable` used to open the RECEIVABLES report.** If you have typed those words
before, you were shown what customers owe you, under a heading that did not say
otherwise. That is fixed. `bills receivable`, `bills outstanding` and `outstandings`
still open "Outstandings", which is unchanged.

### Why it is a separate report and not a toggle

Access rules are written against a report's NAME. While the payables ageing was only
reachable as a setting on the receivables report, *"this person may see what customers
owe us but not what we owe suppliers"* was a rule you could not write. Now you can:

    disallow  display  report:bills-payable

on any security level, and that level keeps its access to "Outstandings".

⚠️ **Nothing is denied by default, and nobody loses access.** Every level that could read
the ageing before still can, including the seeded Data Entry level — which needs it: the
bill-by-bill picker on a Payment lists a supplier's open bills from the same data, and a
clerk who cannot see them has to post the payment on account.

⚠️ **The rule governs the report, not the underlying figures.** A level denied
`report:bills-payable` is refused the report and its printed copy, and can still reach
the same amounts through the bill picker while keying a payment — which is exactly what
that picker is for. If you need the stricter separation, say so; it is a change to what
the picker is authorised as, and it is recorded as open work.

---

## The Outstandings ageing now covers every bill, not just those dated up to today

**Applies to:** Outstandings (receivables and payables), its printed copy, and the
conversational query surface. **Register:** D86.

### What changes

Outstandings is the bill-by-bill breakup of what a party owes you or you owe them —
the same figure the Balance Sheet shows as Sundry Debtors / Sundry Creditors. A ledger
balance includes every voucher that has been posted, whatever date it carries.

Until this release, asking for the ageing **without naming a date** silently limited it
to bills dated up to today. So the moment a bill was dated ahead — an advance invoice, a
proforma, a scheduled billing run — the bill-wise breakup explained LESS than the
balance it was a breakup of, and the two reports disagreed by exactly those bills with
nothing on either to reconcile them.

⛔ **The ageing report moved; the Balance Sheet did not.** With no date named, the ageing
now lists every bill these books carry, so the two agree again.

### What you will see

* **Outstandings may show more bills and a higher total than before**, if your books
  carry any future-dated ones. It now equals Sundry Debtors / Sundry Creditors exactly.
* The caption over the table used to read *"As on <today>"*. It now reads **"NO DATE
  LIMIT — every bill on these books, including any dated AFTER today…"**, and names the
  ageing day separately, because the two are different facts.
* **Asking as at a date is unchanged.** Press **F2** and set a To date and you get
  exactly what you got before: bills up to that date, opening breakup included only if
  the books had begun. That is the right way to read the ageing at a quarter or year end,
  and it is now the *only* thing that claims to be one.
* The bill-wise picker on a Receipt or Payment will offer future-dated open bills. That
  is deliberate: a customer paying an advance invoice early is ordinary, and the
  alternative pushes the receipt onto ON ACCOUNT, which nobody can chase later.
* Days overdue and the ageing slabs are unchanged — they are still measured against
  today unless you name a date.

### What does NOT change

Nothing is migrated and no voucher is rewritten; only the default scope of one read.
The Balance Sheet, the Trial Balance, and every report you asked for a date are
byte-identical.

---

## Closing stock is re-valued: two vouchers on one date now replay in ENTRY order

**Applies to:** Stock Summary, Stock Movement (period), the Closing Stock statement,
and — through the closing-stock term — the **Balance Sheet** and the **Profit & Loss**.
**Register:** D8.

### What changes

Stock value is never stored: every stock figure is derived by REPLAYING the movement
ledger on each read. Weighted average is order-dependent, so the order of that replay
decides the cost of every issue.

Until this release the replay ordered movements on one date by **voucher number**. A
voucher number is text, and it comes from a counter that is **per voucher type** — and
on a purchase it is the *supplier's* own reference. So two vouchers dated the same day
were replayed in the order their numbers happened to sort as strings, which is not the
order they were entered in:

* a delivery note numbered `2` sorted before a receipt note numbered `4`;
* and within one series, `10` sorts before `9`.

The consequence: **an issue keyed after the receipt that funds it could be costed at
the average that applied BEFORE the receipt.** The goods were right, the quantities
were right, and the value was wrong — on a voucher nobody had edited.

The replay now orders by the voucher's own identifier, which is allocated when the
voucher is created and is therefore entry order, per company, across every series.

### What you will see

⛔ **Closing stock, and any report that carries it, may change for periods you have
already reported on.** Only positions that have **two or more differently-numbered
vouchers on one date** are affected; every other figure is unchanged. Quantities do
not change — this corrects a *valuation*, not a movement.

In the shipped demo the corrected figures move as follows:

| Figure | Before | After |
|---|---|---|
| MS Angle 50x50 — cost of goods issued | 2,359.18 | **2,440.09** |
| MS Angle 50x50 — closing value | 8,500.82 | **8,419.90** |
| MS Angle 50x50 — closing quantity | 205.0000 | 205.0000 (unchanged) |
| Balance Sheet — Closing Stock | 75,175.82 | **75,094.90** |

**If you have filed or circulated a stock or balance-sheet figure that includes such a
date, re-read the report and compare.** The new figure is the correct one; the
difference is the mis-costed issue, and it is visible per item on the Stock Summary.

### What does NOT change

* No voucher, posting or stock movement is rewritten. Nothing is migrated. The change
  is in how existing rows are read, which is why re-reading a report is all that is
  needed — and why an older figure cannot be reproduced from the data any more.
* Quantities, opening stock, and every position with at most one voucher per date.
* An ALTER still lands after the version it replaced, and lines within one voucher
  still replay in the order they were keyed.

### If you use FIFO or LIFO

They are not shipped yet (`FeaturesScreen` reads `off`). ⚠️ They are **more**
order-sensitive than weighted average, so they must be built on this ordering and not
the old one.
