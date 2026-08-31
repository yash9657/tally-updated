# THE USABILITY DAY — first pass, 2026-08-30

`PLAN-TALLY-PARITY.md` §7 has asked for this since before T11:

> *"Nobody has yet driven the app for a day as a shopkeeper would, which is how the smaller
> papercuts surface — the ones that do not show up as a missing table. **Budget a usability
> pass** before calling parity."*

This is that pass, or the first half of it. ⭐ **It produced one HIGH defect that no inventory of
routes, tables or code could have found** — both screens render, every figure is real, and two
reports over one set of books disagree while one of them promises they agree.

⛔ **READ §4 BEFORE TRUSTING THIS AS A FULL DAY.** The write half was driven a day later, once
there was a scratch company to drive it in; it found **D107**. ✅ **The six areas §4 lists as
uncovered were then driven on 2026-08-31 — see §6**, which found seven more rows (D108–D114),
two of them HIGH. ⛔ **§6.6 states what §6 did NOT cover**, and payroll is still effectively
undriven for want of a seeded employee.

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
the GST returns. ✅ **All six were driven on 2026-08-31 — see §6**, and §6.6 records how far
each actually got. Two smaller things noticed while driving and not yet registered: the
Account combobox **ignores a mouse click on its option** (§2.3's papercut again, now on the
entry path, where the keyboard is the documented route) — **still open, and still not
registered**; and a Sales voucher to a bill-by-bill party **shows no bill-wise panel at
all**. ✅ **That second one was MEASURED on 2026-08-31 and does NOT reproduce** — the panel
appears as soon as the party is named and `+ reference` splits an invoice into two bills.
See **§6.4**; the observation was made on the empty form, before a party was chosen.

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

---

# 6. ⭐ DAY TWO — 2026-08-31, the six undriven areas

`HANDOFF-USABILITY-DAY-TWO.md` named six areas that had never been driven by hand:
**printing · export · bank statement import and matching · stock vouchers · payroll · GST
returns.** All six were opened and used. This section records what came out, ⛔ **including
§6.6, what was NOT covered**, which is the part that made the previous handoff possible.

Driven on a **fresh scratch book** — `pnpm seed:demo -- --name "Scratch Day Two"`,
company `7c3ab7e9-de38-45d7-b887-f348d91f93df` — on a cored verified fresh (started
22:17:09 against a clock reading 22:17:12; the running one had been **288 seconds older
than the last backend commit**, so §2.1's staleness trap fired again and was caught by the
commit-time check, not by startup duration). `verify:demo` re-run after: **34/34**.

## 6.1 ⛔⛔ SEVEN NEW ROWS, AND THE TWO THAT MATTER ARE BOTH ABOUT MONEY

Registered as **D108–D114** in `HANDOFF-PARITY-AUDIT.md`. Two are HIGH:

**D108 — a second Ctrl+A silently posts an identical second voucher.** Nothing re-keyed
between the presses. One sale keyed, **two tax invoices on the books**: Sales No. 4 and
No. 5, each 10,000.00 + 900.00 CGST + 900.00 SGST, with the GST output liability doubled.
Reproduced on **three classes** — Sales, Contra (the same 5,000.00 moved twice) and Receipt
Note (100 units of stock arriving three times). ⭐ **It is D107's mechanism, and they are one
defect with two symptoms.** `navMachine.ts:1815` replaces the create frame and its own
comment states the intent — *"re-opens a fresh form for bulk entry"*, which is Tally's
behaviour — but the form is React component state and replacing the frame does not remount
it. **The frame is fresh; the form is not.** D107 is the `saved` flag surviving onto that
frame; D108 is the data surviving onto it.

**D113 — the annual return cannot be produced for the shipped demo book.** GSTR-9 on
`Demo Traders 45f69de4`, the book `verify:demo` asserts 34/34 against, answers
*"table 3.2 taxable 0.0000 exceeds 3.1(a) taxable -12000.0000"*. The seeder gives July 2026
a Credit Note and no sales, so 3.1(a) nets negative and an **empty** table 3.2 "exceeds" it.
One month aborts all twelve.

The other five: **D109** exports flatten a two-level report so SUM returns exactly twice the
file's own total; **D110** the Bank Reconciliation screen says matching *"is not built yet"*
while Alt+S on that same screen opens it; **D111** every pasted statement is stored nameless
and the disclosure renders `""`; **D112** the payroll register answers an empty month with a
raw internal error naming its own package twice; **D114** the import panel's Esc goes to the
Gateway, not to the screen its footer names.

## 6.2 ⭐ THE PATTERN HELD, AND SO DID THE WAY IN

Every one of the seven is §6's shape from the previous handoff — **a screen that is right in
every particular and is saying something untrue** — and four were caught by the same trick
that caught D106: **the code or the screen states its own purpose out loud, and the statement
can be tested.**

- **D109** was found because the exporter says *"this file's WHOLE PURPOSE IS TO BE SUMMED"*.
  Summing it returns 2.000000× the total printed in the same file.
- **D110** was found because two screens in one feature say opposite things in writing, and
  the wrong one is on the screen you land on first.
- **D113** was found because the validator names a table (3.2) that is EMPTY, so the sentence
  could not be about what it claimed to be about.
- **D108** was found by disbelieving the word `Saved` — D107's lesson, one step on.

⚠️ **AND ONE WAS FOUND BY MY OWN MISTAKE, WHICH IS WORTH RECORDING.** D108 surfaced because
I pressed Ctrl+A a second time while a WebAuthn ceremony was pending and could not tell
whether the first press had landed. That is exactly the operator's position — the screen
after a save is identical to the screen before it — so the accident and the defect are the
same thing.

## 6.3 ⭐ WHAT IS GOOD — recorded because seven rows is not a verdict

- ⭐⭐ **The bank matching screen (Alt+S) is the best screen in the product**, and all three
  invariants `HANDOFF-USABILITY-DAY-TWO.md` §5.3 asked about **HOLD**. It could not be made
  to tick anything by itself: *"NOTHING ON THIS SCREEN HAS BEEN RECONCILED. Every row below
  is a PROPOSAL."* An ambiguous row is refused **in both directions** and not chosen for you —
  proved with the two duplicate Contras D108 had just created, which the matcher correctly
  reported as `AMBIGUOUS — not accepted, and not chosen for you`, listing both and excluding
  the row from the bulk *"Accept 1 unambiguous exact match"*. And a file with no
  running-balance column says so loudly: **"Balance check DID NOT RUN"**.
- ⭐ **It discloses the limits of its own search**, which is the rarest thing on this list:
  *"Part payments and netted rows were sought over PAIRS ONLY … 'No match found' here means
  'none found by this search', not 'none exists'."* That is §6's third question answered
  before anybody asked it.
- ⭐ The **statement import mapping screen** derives its column mapping from the file's own
  header row and says why it does not remember a layout; it mapped a five-column HDFC-shaped
  CSV correctly with no help.
- ⭐ The **Receipt Note** screen does NOT have D107 — it showed *"Confirm the passkey prompt"*
  where the accounting screen would have said `Saved`.
- ⭐ The **payroll wage-month refusal** and the **GSTR-9 required-ledgers** caption are both
  excellent, and GSTR-9's claim to pre-select the six ledgers by name is TRUE — all six were
  pre-selected, verified by reading the select values rather than the rendered text.

## 6.4 ✅ §5.2 RESOLVED — the Sales bill-wise panel is NOT a defect

`HANDOFF-USABILITY-DAY-TWO.md` §5.2 carried a "known and not yet registered" item: a Sales
voucher to a bill-by-bill party *"shows no bill-wise panel at all … an operator cannot split
one invoice into two bills from that screen"*. **Measured, and it does not reproduce:**

| state | panel |
|---|---|
| empty form, no party named | absent — correct, there is no party to reference bills for |
| after choosing Acme Traders | **present** |
| after keying the amount | present, `referenced in full (8260.0000)` |
| after clicking `+ reference` | **two** reference rows, each with type, amount and due date |

So one invoice **can** be split into two bills from the Sales screen. The earlier observation
was almost certainly made on the empty form before a party was named. ⛔ **No row was
written**, deliberately — recording it here so the next session does not re-derive it.

## 6.5 ⚠️ §4'S THESIS, DEMONSTRATED — why 33/33 sat beside D113

`HANDOFF-USABILITY-DAY-TWO.md` §4 argued that *"a probe asserts what its author already
suspected"*. **D113 is the cleanest instance yet.** `drive-t12-gstr9.mjs` passes **33/33**,
and its own header says it *"does NOT touch the demo tenant"* — it mints its own company and
its own vouchers. So the probe never meets the data the seeder actually ships, and the annual
return has been broken on the demo book underneath a green gate.

⛔ **The lesson is not "write a better probe".** It is that a probe over data the probe built
cannot find a defect in the data the product ships. D113's re-drive should run against a
**seeded** book.

## 6.6 ⛔⛔ WHAT WAS *NOT* COVERED — read this before trusting §6 as a full day

- **Payroll was driven only to its empty state.** The scratch book has **zero employees and
  zero pay runs** (the seeder creates neither), so Pay Run, the payslip, Form 138 and the
  EPF/ESI computations were **never exercised on real data**. Only the register's month
  validation and its empty-month answer (D112) were driven. ⛔ **This is the largest gap in
  this pass** — payroll is the area with the most statutory surface and it remains
  effectively undriven. Seeding an employee is the first thing the next session should do.
- **Printing was driven on ONE document** — a sales invoice, Ctrl+P from the voucher screen,
  which correctly printed the voucher it named (`No: 5`). The §5.4 question — a document
  right in every particular and about the wrong thing — was **not** pursued across the
  reconciliation statement or the reports, and the known bank-account refusal was not
  re-driven.
- **GSTR-9 never got past `Reconcile the year`**, because D113 blocks it on every seeded book.
  Nothing downstream of that button has been seen: no annual figures, no GSTR-1/3B
  comparison, no export, no print.
- **Stock valuation was not driven across methods.** Both seeded items are `WEIGHTED_AVG`;
  Alt+V was never pressed, so the mixed-basis case §5.1 calls the interesting question — one
  Stock Summary carrying three valuers — was **not** tested. **Physical Stock (Alt+F10) was
  not driven at all**, so §5.1's sharpest question — *what rate does a physical-stock surplus
  come in at* — is still unanswered.
- **Stock Journal (Alt+F7) and Delivery Note (Alt+F8) were not driven**; only Receipt Note.
- **The matcher was never driven to ACCEPT.** Its proposals were read and its refusals
  measured, but no match was accepted, so `bank_statement_match` was never written and the
  accept path, Ctrl+J, Ctrl+B, F9 and Alt+F8 are all still unexercised.
- ⛔ **NOTHING WENT NEAR A GOVERNMENT PORTAL.** No GSTN, EPFO, ESIC or NIC validation was
  performed and none is claimed. D113's rule is read from the code and from the return's own
  arithmetic, not from a portal.
- **The `4 UNREADABLE` register rows were left alone**, as instructed.

## 6.7 THE STATE THIS WAS DRIVEN AGAINST

```
cored        restarted 2026-08-30 22:17:09 (the running one was 288s OLDER than the
                                            last backend commit — §2.1 again)
statutoryd   restarted after cored, token re-minted
vite         :5173, http://localhost:5173/
scratch book 7c3ab7e9-de38-45d7-b887-f348d91f93df  "Scratch Day Two"
verify:demo  34/34 on 45f69de4… BEFORE and AFTER — only the scratch book was dirtied
vitest       1836 passed / 113 files
backend      go build + go vet + go test ./... all green; test_residue OK
tsc          clean · corpora OK
register     114 · 101 closed · 9 open (D99, D107, D108–D114) · 4 unreadable
```
