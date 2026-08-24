# Release notes

Operator-facing consequences of a change. ⛔ **A change belongs here when it moves a
number an operator has already read, or removes/renames something they rely on** —
not merely because it was large. The register (`HANDOFF-PARITY-AUDIT.md` §7.1) records
what was wrong; this file records what a person will notice.

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
