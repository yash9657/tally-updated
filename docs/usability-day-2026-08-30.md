# THE USABILITY DAY — first pass, 2026-08-30

`PLAN-TALLY-PARITY.md` §7 has asked for this since before T11:

> *"Nobody has yet driven the app for a day as a shopkeeper would, which is how the smaller
> papercuts surface — the ones that do not show up as a missing table. **Budget a usability
> pass** before calling parity."*

This is that pass, or the first half of it. ⭐ **It produced one HIGH defect that no inventory of
routes, tables or code could have found** — both screens render, every figure is real, and two
reports over one set of books disagree while one of them promises they agree.

⛔ **READ §4 BEFORE TRUSTING THIS AS A FULL DAY.** The write half was driven a day later, once
there was a scratch company to drive it in; it found **D107**. Printing, export, bank matching,
stock, payroll and the GST returns are still undriven.

---

> ## ✅ D106 IS FIXED — both halves, same day
> The design was taken from **Tally's own documentation**, not invented: see
> `HANDOFF-PARITY-AUDIT.md` D106 for the full close. Outstandings now reports the
> unreferenced part of a party's balance as one un-aged **On Account** row, so it
> reconciles to Sundry Debtors *by construction*; and the entry path refuses a
> voucher that leaves a bill-by-bill ledger unreferenced, naming `ON_ACCOUNT` as
> the answer. §2.1's sort-order papercut went with it. **§2.2–§2.8 below are still
> open**, and so is §4.

## 1. ⛔⛔ THE FINDING — D106, and it fails in the direction that foots

**An on-account receipt is invisible to Outstandings.** Registered as **D106** in
`HANDOFF-PARITY-AUDIT.md`; the short version:

| Measured from the API, one set of books (`be33e0c6`) | |
|---|---|
| Outstandings total | **318584.0000** |
| Balance Sheet, Sundry Debtors | **311584.0000** |
| Difference | **7000.0000**, all of it on Acme Traders |

The cause is a Receipt dated 2026-08-07 (no. 2, ₹7,000, via HDFC Bank) posted with **no bill-wise
reference**. Outstandings' reference universe is `bill_allocation` UNION `ledger_opening_bill`
(`outstanding.go:190-235`) and there is no third source, so money received on account reaches the
ledger balance and nothing else.

⭐ **HOW A SHOPKEEPER MEETS IT.** They open Outstandings — the first thing anyone opens in the
morning — and ring Acme Traders about ₹2,02,944. Acme says they paid ₹7,000 three weeks ago. They
did. It is on the Balance Sheet.

⛔ **AND THE SCREEN PROMISES THIS CANNOT HAPPEN.** Its caption reads *"NO DATE LIMIT — every bill
on these books … so this agrees with Sundry Debtors/Creditors on the Balance Sheet."* **That
sentence is the only reason the defect was findable at all**, which is why D106 says not to
"fix" it by deleting the caption.

⚠️ **IT IS D86'S FAMILY, ONE STEP OVER.** D86 was the same disagreement caused by the *date* half
of coverage, and the comment its fix left behind states the invariant this breaks:

> *"a bill-wise breakup of that same balance must cover the same vouchers or it is a breakup of
> something else"*

D86 closed the date half. The allocation half was never asked.

---

## 2. ⚠️ THE PAPERCUTS — smaller, and all in the daily loop

### 2.1 ✅ FIXED — the chasing report buried the oldest debt

`outstanding.go:281` — `ORDER BY l.name, ba.ref_name`. Bills sort by **reference as a string**, so
Acme's list reads `1`, `4`, `7`, `8`, `INV-001`, `INV-003`, `OB/ACME/24-25/07`,
`OB/ACME/24-25/11`. The four leading rows are the ones with **no due date recorded** — the bills
that cannot be chased — and the **132-day-old ₹70,000** sits seventh of eight.

⭐ **The entire purpose of this screen is deciding who to ring first**, and the sort worked against
it. Tally's Bills Outstanding leads with age.

✅ **Fixed with D106** (same query, same screen): bills now sort by due date oldest-first, with the
un-aged On Account row last. Driven after: `132d → 117d → 110d → 42d → undated → On Account`.

### 2.2 The Gateway and Go To are two different catalogues, and neither contains the other

| Only on the Gateway | Only in Alt+G Go To |
|---|---|
| Bill Inbox, Query, Bank Reconciliation, TDS Challans, Import from Tally, Print Templates, Reconciliation Queue, Offline Outbox, Cost Centres, Cost Centre/Category Summary, Sales Orders, Purchase Orders, Bills of Materials, Employees, Pay Run, Payroll Register, Form 138, Users & Roles | **Outstandings**, **Trial Balance**, **Cash Flow**, **Bills Payable**, Anomalies, TDS/TCS Register, Sales/Purchase Order Book, and every `Create …` master |

⛔ **`Outstandings` is not on the Gateway.** The report a shopkeeper opens first is reachable only
by knowing to press Alt+G. Learning one list does not teach the other.

### 2.3 In Go To, a mouse click selects but does not open

Clicking `Outstandings` prints `Selected: Outstandings` and stays put; it needs a second keystroke.
The engine is keyboard-first by design and that is defensible — but a click that produces only a
line of text reads as a dead control.

### 2.4 Collapsed Balance Sheet shows Current Assets with one child out of four

Before `Alt+F1`, `Current Assets 7,98,526.35` lists only `Stock-in-Hand 72,742.35`. Bank Accounts
(3,79,000), Cash-in-Hand (35,200) and Sundry Debtors (3,11,584) — ₹7.25L of the ₹7.98L — appear
only in Detailed. ⚠️ Reported as **observed, not diagnosed**: it may be deliberate, but it reads
as a group that does not add up.

### 2.5 Current Assets sub-groups are in alphabetical order

`Bank Accounts, Cash-in-Hand, Stock-in-Hand, Sundry Debtors`. Tally uses a fixed conventional
sequence (Sundry Debtors, Cash-in-Hand, Bank Accounts, Stock-in-Hand). A Tally user's eye goes to
the wrong row.

### 2.6 The ledger drill-down's Particulars column names the ledger itself

In `Balance Sheet — Acme Traders`, seven of eight rows show `Acme Traders` in **Particulars** — the
ledger being viewed. Only the receipt shows a counterparty (`HDFC Bank`). The column is where a
Tally user looks to see what the entry was *against*, so it is carrying no information on most rows.

### 2.7 `favicon.ico` 404s on every page load

The only console error in the whole session. Cosmetic, but it is the first thing in the log.

### 2.8 The app opened a company that is not `.demo-company-id`

`frontend/.demo-company-id` names `45f69de4…`; the app signed in to `be33e0c6…`. **Two companies
are both called "Demo Traders"**, and nothing on screen distinguishes them. ⚠️ This cost real time
here — the first pass at diagnosing D106 queried the wrong books and found nothing, and the numbers
disagreeing with the screen looked briefly like a much larger bug.

---

## 3. ✅ WHAT IS GOOD, recorded because a defect list is not a verdict

- **The disclosure lines are excellent and they are why D106 exists.** Outstandings states its date
  basis, its ageing reference and its agreement claim in one sentence. A screen that says what it
  is doing can be caught being wrong.
- **`Profit & Loss A/c (cumulative to the latest date posted)`** — the label carries its own
  qualification rather than leaving "P&L" to be interpreted.
- **The receipt voucher's single-entry caption** explains the mode, why the account amount is not
  typed, and how to switch. That is a genuinely hard thing to word.
- The keyboard model held up throughout: `Alt+G`, `Enter` to drill, `Esc` to go back, `Alt+F1` to
  expand, all where a Tally user expects them.

---

## 4. ✅ THE WRITE HALF — driven 2026-08-31, and it found D107

The first pass could not drive voucher ENTRY because there was nowhere safe: the only
populated books were the demo company, which `verify:demo` and three drive scripts assert
against. **That blocker is gone.**

```bash
pnpm seed:demo -- --name "Scratch Traders"     # -> .scratch-company-id, gitignored
```

⭐ **THE NAME AND THE ID FILE MOVE TOGETHER**, deliberately. The seeder hard-coded
"Demo Traders" and always wrote `.demo-company-id`, so a second book was another
identically-named company that silently replaced the id every gate reads — which is how
nine of them accumulated and how §2.8 cost real time. A scratch book is named, writes its
own id file, and no gate reads it. `verify:demo` still passes 34/34 against the default.

### ✅ WHAT THE WRITE PATH GOT RIGHT

**D106's entry rule works end to end, in a browser, with the message it was written with.**
Keying a receipt on a bill-by-bill party with an empty bill-wise panel is refused:

> *"ledger: this ledger is maintained bill-by-bill and posts -5000.0000, but the voucher
> names no bill reference for it — record which bill it is against, or record it as
> ON_ACCOUNT if you do not yet know"*

⭐ **AND THE ESCAPE HATCH IS REACHABLE AND CORRECT.** `+ reference` offers Tally's four
types by Tally's names (New Ref / Agst Ref / Advance / On Account); choosing **On Account**
drops the bill-name field (an on-account reference names no bill) and the receipt saves,
stored as `ref_type = ON_ACCOUNT`, `ref_name = "On Account"` — the same bucket the report
derives into, so the two never split. Measured after, over the scratch books:
**Outstandings 248920.0000 == Balance Sheet Sundry Debtors 248920.0000**, and
`sum(slabs) + on_account_total == total`.

### ⛔⛔ WHAT IT GOT WRONG — D107, and it is on the write path

**The screen says `Saved` when Ctrl+A was merely pressed.** `VoucherScreen.tsx:2899` falls
back to the literal word when `status` is null and `snapshot.saved` is set — and that flag
means *"this voucher frame has been accepted at least once (Ctrl+A)"*, not "the server has
it".

Minimal repro, no network call possible:

```
F9 (empty Purchase) → Ctrl+A
  header: "Purchase Voucher" → "Saved" → "Error: Party A/c Name is required — …"
```

⛔ **THE STEP-UP PATH MAKES IT DURABLE.** A receipt needing WebAuthn leaves `status` null
while the passkey is pending, so the screen sits reading **`Saved`** next to *"Confirm the
passkey prompt"* — measured with the POST returning **403** and nothing written. An operator
keying a day of receipts who does not confirm each passkey is told every one of them saved.

⚠️ **I nearly reported this as "the 403 renders as Saved" and stopped there.** It reproduced
on an empty form with no request at all, which is what identified the real mechanism.
Registered as **D107**.

### ⚠️ STILL NOT COVERED

Printing, export, the bank-statement import and matching loop, stock vouchers, payroll and
the GST returns. Two smaller things noticed while driving and not yet registered: the
Account combobox **ignores a mouse click on its option** (§2.3's papercut again, now on the
entry path, where the keyboard is the documented route), and a Sales voucher to a
bill-by-bill party **shows no bill-wise panel at all** — it relies on the client adding an
unnamed NEW reference, which works, but means the operator cannot split an invoice into two
bills from that screen.

## 5. THE STATE THIS WAS DRIVEN AGAINST

```
cored        restarted 2026-08-30 13:09:28   (the running one was 79s OLDER than the last
                                              backend commit — the staleness trap, hit again)
statutoryd   restarted after cored, token re-minted
vite         :5173, http://localhost:5173/
verify:demo  34/34 on 45f69de4…
vitest       1831 passed / 113 files
register     106 · 100 closed · 2 open (D99, D106) · 4 unreadable
```

⚠️ **THE FOUR "UNREADABLE" ROWS ARE ALL CLOSED** — D49, D82 and D84 because their status cell
contains the word OPEN in prose after the verdict, D80 because a `|` inside a backtick code span
splits its row into eight cells. So the register's real count is **104 closed, 1 open** before
D106. ⛔ **`docs/parse-defect-register.py` was left alone deliberately**: a rewrite that reads the
first verdict word reconciles to 106 and looks right, but it classifies **D13** off a cell that is
not the status cell and is correct only by luck. **A gate that honestly says UNREADABLE beats one
that is confidently wrong**, so the parser needs its own pass rather than a quick fix.
