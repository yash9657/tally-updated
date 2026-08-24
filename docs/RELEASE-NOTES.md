# Release notes

Operator-facing consequences of a change. ⛔ **A change belongs here when it moves a
number an operator has already read, or removes/renames something they rely on** —
not merely because it was large. The register (`HANDOFF-PARITY-AUDIT.md` §7.1) records
what was wrong; this file records what a person will notice.

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
