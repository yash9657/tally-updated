# Release notes

Operator-facing consequences of a change. ⛔ **A change belongs here when it moves a
number an operator has already read, or removes/renames something they rely on** —
not merely because it was large. The register (`HANDOFF-PARITY-AUDIT.md` §7.1) records
what was wrong; this file records what a person will notice.

---

## Alt+E can now save a report as a PDF — and it is NOT the rows on screen

**Applies to:** ten of the thirty reports. **Feature:** B1.

### What changes

Press **F12** on a report and there is a new line: **Export (Alt+E) as PDF**. Turn it
on and **Alt+E** saves the report as a PDF file instead of a spreadsheet.

The document is the same one **Ctrl+P** has always produced — the server lays it out,
with your company's letterhead. The difference is the verb: Ctrl+P opens it in a tab
where your browser's print command lives, Alt+E saves it to your downloads.

### ⛔ The one thing to know: a PDF is the WHOLE statement

Alt+E's other two formats — CSV and Excel XML — write **exactly what is on your
screen**, after a Ctrl+F filter and any F12 settings have been applied. **The PDF does
not.** It comes from the print route, which returns the complete statement.

So if you filter the Trial Balance to one party and export it as a PDF, **the PDF
contains every party**. That is not a bug and it is not going to change — the printed
layout is the one used on your invoices, and re-implementing it in the browser would
produce a second document that disagrees with the first.

Because that is easy to get wrong in a way you would only notice after sending the file
to somebody, the product tells you twice:

- the F12 line itself reads *"the server's WHOLE statement, not the rows on screen"*;
- and the message after the export **names what was not applied** — for example,
  *"your Ctrl+F filter "ACME" was NOT applied to it"*. With nothing filtered it says so
  instead, so the line is worth reading rather than something to skip past.

### Which reports offer it

Ten reports have a printed layout: the **Trial Balance**, **Balance Sheet**, **Profit &
Loss**, **Day Book**, **Outstandings**, **Bills Payable**, **Stock Summary**, the
**Payroll Register**, a **Payslip**, and the **Bank Reconciliation Statement**.

On any other report the F12 line is simply not there — and the panel says which report
it is and that printing it answers "not implemented", rather than leaving you looking
for a control that never appears. CSV and Excel XML still work on every report.

⚠️ Printing is its own permission, separate from displaying, so a user allowed to READ a
statement may still be refused permission to export it as a PDF. If that happens the
message is the server's own, naming the report.

### Not built

**E-mailing a document is still not built**, and it is now its own line on the F11
Company Features screen rather than sharing one with PDF export. The reason is on that
line: the outbox that would carry it is real and good, but this deployment has no mail
transport at all, and the first question — whether the product sends mail itself or
hands your own mail client a pre-addressed draft with the PDF already downloaded — is a
product decision that has not been made. JPEG and HTML export are also still not built.

---

## Alt+O: your books can now be brought over from Tally, from the keyboard

**Applies to:** every company. **Feature:** T32.

### What changes

`Alt+O` opens **Import from Tally** — and so does `Gateway of Tally > Import`, because
that is where a Tally operator looks for it. Choose the XML file TallyPrime wrote (export
the whole company: Masters *and* Transactions) and press **Import this file**.

The importer has existed for a while. What did not exist was any way to reach it: bringing
a company over meant somebody hand-rolling an HTTP request with the right content type. It
is now a screen.

### ⛔ Read the stock figures, not just the voucher count

The report shows **two things side by side**: the accounting (vouchers posted, altered,
deleted, and skipped because they were already imported) and **the goods** — how many stock
movements were written, and how many opening positions.

⛔ **That second pair is the only thing that tells you your stock arrived.** The rupee value
of a sale lives in the ledger entries, so an import that read every stock line and dropped
it would give you books that balance perfectly and a **Stock Summary with nothing in it**.
`Stock movements written: 0` on a file full of invoices is the tell, and the screen says so
in those words.

The screen also flags a subtler case: **no opening positions alongside real stock movement**
is a warning, not a clean result. It means the export carried no opening stock, so every
item starts at nil and any sale of goods bought before the export window has nothing behind
it.

### ⛔ Every warning is shown in full, and an empty list is not a clean bill of health

The importer carries on past things it cannot represent exactly rather than refusing the
whole file — an opening bill-wise breakup that does not add up to its own balance, a ledger
whose Tally group was not in the export, a compound unit (`1 BOX = 12 BAG`) stored in its
own unit rather than flattened, Tally's location *tree* recorded as flat locations. Each of
those is listed on the page, in the importer's own words. **A total looks right either way.**

> **Never treat an empty warning list as the only thing to check, and never treat a
> balanced trial balance as proof the migration worked: check the Stock Summary.**

### Before you press the button

- **The import goes into the company that is open, and it cannot be undone.** The screen
  names that company above the file picker. Every voucher it writes goes through the same
  immutable ledger path as one you key by hand, so nothing can remove them afterwards.
- **An export with no stock-item masters is refused outright**, naming both ways forward.
  Such a file carries no opening stock, so the first sale of goods bought before the export
  window has nothing to issue — and the importer will neither invent a quantity nobody
  stated nor switch your negative-stock setting on your behalf.
- **Importing needs permission.** It is a write, so it asks for your passkey, it respects a
  closed-period cutoff, and no seeded level below Owner is allowed it. If you are refused,
  the screen shows the reason it was given rather than "import failed".
- Files up to **128 MB** are accepted. A whole-company export takes a while and the page
  will not show a percentage — there is nothing honest to show one from. It answers once,
  when the import has committed.

### One thing worth knowing if you are testing this

Real TallyPrime exports are UTF-16 encoded. The screen sends the file to the server as
bytes and lets the server work out the encoding, which is why a genuine export imports
correctly. If you have been round this loop before with a tool that mangled non-ASCII names
in a Tally export, that is the bug this avoids.

---

## Alt+Y: a company can now be backed up, and an archive can be restored — as a NEW company

**Applies to:** every company. **Feature:** T14.

### What changes

`Alt+Y` opens a **Data** screen with two things on it.

**Backup** downloads the whole company as one compressed file
(`<company>-<date>.tallybak.jsonl.gz`). It covers every table the database itself says
holds this company's data — the list is derived from the live schema and shared with the
test-data teardown, so a table added by a future migration is archived without anyone
having to remember it. Vouchers and every version of them, the chart of accounts, the
inventory and its movements, the statutory workflow log, and the unposted bills sitting in
the AI inbox are all in it.

⛔ **Treat the file as you would the books.** It contains every party's details and every
salary, and anyone holding it holds all of them. It is served under its own permission
(`backup`), which no seeded level has: an Owner can take one, and a Data Entry operator
cannot, without anybody granting or revoking anything.

### ⛔ The two things to know before you use Restore

**1. A RESTORE CREATES A NEW COMPANY. IT NEVER REPLACES ONE.** There is no way to
overwrite or delete a company in this product at all — the audit trail is immutable, and
the same rule that stops anyone editing a posted voucher stops anyone erasing a book. So
if the archive's company is still in the database, the restore is **refused** (409) rather
than merged. Restore into a different database, or restore a company that is genuinely
gone.

This is a consequence of a deliberate choice: the archive keeps every internal identifier
exactly as it was, which is what lets the restored books verify against their **original**
hash chain. An archive that renumbered everything would restore into a company that looked
perfect and whose audit trail attested only to itself.

**2. AN ARCHIVE DOES NOT GIVE ANYBODY BACK THEIR ACCESS.** Who could open the archived
company is recorded in the file but is never re-granted: silently restoring permissions
from a file somebody handed you is how access is escalated by accident. Whoever performs
the restore becomes the Owner, and **everyone else is listed by name on the screen** so
they can be re-granted deliberately.

### What else the restore refuses, and why

- **A download that was cut short.** A truncated archive is still a valid compressed file
  and still reads as far as it got, so the archive carries a per-table row count at the
  end and the restore checks it. An incomplete file is refused with a message saying so —
  download it again. Nothing is written; a restore is one transaction, so it is all or
  nothing.
- **An archive from a newer version of the product**, which would restore "successfully"
  while quietly dropping any field added since. Upgrade first.
- **A file that is not an archive at all.**

### Split Company Data is deliberately absent

Tally's Data menu has a third item, and this one does not. Tally splits a company by
financial year because it loads the whole company into memory and cannot hold the history;
this product keeps every year queryable at once, so splitting would be a workaround for a
limit it does not have. The Features screen (`F11`) carries this as its own row reading
**Not built**, with the reason, rather than folding it into the backup row.

⚠️ There **is** a real feature next door and it is not built either: closing a financial
year into a read-only archive company plus a fresh one carrying opening balances, for
year-end and for handing a closed year to an auditor. That is archive-and-carry-forward,
not a performance workaround.


## Stock groups subtotal the WHOLE BRANCH, and a compound quantity stays in its own unit

**Applies to:** the Stock Summary, the stock item and unit masters, and a Tally import.
**Feature:** T10.

### What changes

Stock items were flat and a unit was a bare symbol. Two things are now modelled, and both
of them show up on the Stock Summary:

- **Stock groups.** `Alt+G > Create Stock Group` builds an item hierarchy — a name and,
  optionally, a parent, nested as deep as you like — and an item is filed into one on its
  own master form (`Alt+G > Create Stock Item`, *Stock group*). The Stock Summary then
  prints a subtotal table above the item list: one line per group in tree order, indented
  by depth, with everything ungrouped in its own named bucket. **A company that has created
  no groups sees no table at all**, rather than a one-line box saying "(ungrouped)".
- **Compound units.** `Alt+G > Create Unit` takes a base unit and a conversion factor, so
  `1 BOX = 12 Nos`, and the Stock Summary carries a base-unit column beside the closing
  quantity. An item in a simple unit prints an em dash there, not a zero.

A Tally import brings both in: `STOCKGROUP` masters arrive with their hierarchy, and a unit
the export marks `ISSIMPLEUNIT=No` arrives with its base and its factor.

### ⛔ The group lines OVERLAP. Do not add them up.

A group's subtotal is **the whole branch** — "Provisions" includes "Provisions > Grains" —
which is the only thing that makes a branch total worth reading, and exactly what makes the
column impossible to total:

```
Stock group                    Items   Closing Value
Provisions                         3        2,450.00     <- 250 direct, plus the branch
  Grains                           2        2,200.00
(ungrouped)                        1           20.00
                                          ----------
your closing stock is                       2,470.00     <- NOT 4,670.00
```

**The company's closing stock is the Stock-in-Hand line, never the sum of that table.** The
table says so in its own footer, where a reader would otherwise reach for a calculator.

⚠️ **A stock group is not an accounting classification.** Every item in every group lands
in the same Stock-in-Hand asset, so a group has no *nature* and no group decides which
statement a figure appears on. It changes how the Stock Summary is subtotalled and nothing
else — no balance moves when you create one.

### ⛔ A quantity in a compound unit is STORED in that unit — `4 BOX`, not `48 Nos`

Four boxes of twelve are stored as **`4.0000 BOX`**. Your invoice says 4, the stock ledger
says 4, and `48.0000 Nos` is a **derived column** recomputed on every read.

That is the reason to key it this way, and it is worth knowing before you key it wrongly:
**if a conversion factor turns out to be wrong, correcting it changes no voucher, no
quantity and no balance** — only the restated column moves. A build that had multiplied on
the way *in* would have to rewrite every movement ever posted in that unit, and could not
tell which ones it had already done.

⚠️ The corollary: the Qty column on a compound item is **boxes**, so it is not comparable
with a Nos figure on the line above it. That is what the base-unit column is for.

### What is refused

- **A chain** — a base unit that is itself compound (Tally's *PALLET of 20 BOX of 12 Nos*).
  This build converts **one level only**, and a conversion that resolved one level of two
  would be wrong by the second factor and printed as confidently as any other number.
  Define the outer unit in terms of the simple unit at the bottom instead.
- **Half a conversion** — a base with no factor, or a factor with no base. The form refuses
  it before the request, naming the field; the service and a `CHECK` constraint refuse
  behind it.
- **An item naming a stock group that does not exist.** The group is never minted from the
  item, because a group created from a misspelling subtotals half a company's stock under a
  name nobody meant. Create the group first.

### What is not here

No rename, re-parent or delete of a stock group, and **no way to move an item to a
different group after it is created** — both are master alters and this build has none for
either master, so a mis-filed item is corrected by creating it again. And the group
subtotals are **on the screen only**: the exported and printed Stock Summary carries the
base-unit column but not the group table.

---

## A Tally import now brings in the GOODS, and it refuses a file with no opening stock

**Applies to:** `POST .../import/tally-xml` — migrating from TallyPrime. **Feature:** §3.

### What changes

The importer read every stock line in an export and threw it away. A migrated company
got the **accounting value** of its trading and no goods at all: no item, no quantity, no
batch, no location.

⛔ **And nothing disagreed.** The rupee value of a trade lives in the ledger entries, so
the Trial Balance footed, the Balance Sheet carried closing stock, and the Stock Summary
was empty. If you have migrated from Tally on an earlier build, **open the Stock Summary**:
if it is empty while the Balance Sheet shows Stock-in-Hand, that is this defect and the
fix is to re-import into a fresh company.

An import now brings in:

- **stock items, units, locations and batches** — created from the masters when the export
  declares them, and INFERRED from the vouchers when it does not (a Day Book export
  declares none; the unit is recovered from the quantity's own text, `100.00 BAG`);
- **opening positions** from `STOCKITEM` masters, per (location, batch), with the rate
  derived from `OPENINGVALUE` when the file states no rate;
- **quantities**, read from `BATCHALLOCATIONS.LIST` where a real export puts the location
  and the lot — a line split 60/40 across two batches lands as two positions, and the
  line's own total is not posted a second time;
- and a **negative quantity inside a purchase** (goods rejected back) as an OUTWARD
  movement, because a quantity is never signed in this product.

The import report now says how many stock movements and opening positions it wrote. **That
count is the only figure that distinguishes "imported the accounting" from "imported the
trade"** — check it.

### ⛔ The refusal you are most likely to meet, and why it is not a bug

An export with no `STOCKITEM` masters carries **no opening stock** — and a Day Book export
has none. So the first sale of goods you bought before the export window has nothing to
issue, and **the whole import is refused**, naming the item and the voucher.

Two ways forward, and the importer will not choose for you:

1. **Import a `Masters.xml` carrying the items with their opening balances first.** This is
   the correct fix, and it is also the only one that gets the opening VALUE right.
2. **Switch the company's `allow negative stock` on before importing.** The quantities come
   in with the shortfall visible as a negative position — which is how you find the missing
   purchases — but a negative position has no cost to consume, so the cost of goods sold is
   understated by the whole opening value until you correct it.

The importer will not invent an opening position (a quantity nobody stated is not derivable
from a voucher, however plausible) and will not change your negative-stock setting to make
its own job succeed.

### What is still not imported

Cost-centre allocations, price lists and budgets. Tally's location **tree** is flattened —
the stock is in the right place and the hierarchy is not recorded. Each of those is reported
in the import's warnings rather than silently flattened.

⛔ **This paragraph used to say a compound unit ("Box of 12 Nos") arrives as a simple
symbol. That is no longer true** — T10 gave units a base and a factor, and the importer
brings both in (see the note above). A compound unit whose export carries no usable base or
conversion still arrives as a simple symbol, and still says so in the warnings.

⚠️ **A clean import is not an empty warnings list, and a balanced Trial Balance proves
nothing about your stock.** Read the warnings, then read the Stock Summary.

---

## Stock items can be valued FIFO or LIFO, and switching one RESTATES its whole history

**Applies to:** every stock item, the Stock Summary, closing stock on the Balance Sheet
and the gross profit in the Profit & Loss. **Feature:** T9.

### What changes

Until now every item in every company was valued at **Avg. Cost** (weighted average),
which is Tally's default and remains this build's. Two more costing methods are now
available and are chosen **per item**:

- **FIFO** — the oldest receipt is issued first, so closing stock is carried at what the
  most recent purchases cost.
- **LIFO (Perpetual)** — the newest receipt in existence at the moment of each issue is
  consumed first, so closing stock is carried at what the oldest purchases cost.

Choose the method when you create the item (Gateway → Alt+G → Create Stock Item →
**Costing method**), or change it on an existing item with **Alt+V on the Stock Summary**.
The Stock Summary now has a **Basis** column, and the printed report names each item's
basis whenever a company holds more than one.

⛔ **NO EXISTING FIGURE MOVES UNTIL YOU CHANGE AN ITEM.** Every item keeps `WEIGHTED_AVG`
and every number you have already read is reproducible. Nothing was migrated and no
company's books were touched by the release itself.

### ⛔ What happens when you DO change one, and why it is not an edit

The same three movements — buy 10 @ 100.00, buy 10 @ 120.00, issue 5 — close at
**1,650.00** at Avg. Cost, **1,700.00** under FIFO and **1,600.00** under LIFO. On one
item, on one page, the method is worth 100.00.

No valuation figure is stored anywhere in this product: value is DERIVED at report time by
replaying the movement ledger. So changing an item's method does not change data going
forward — **it re-values that item's entire history, from its opening onwards, including
periods that have already been reported, printed and filed.** Your April Balance Sheet
will not read the same as the copy in the folder.

⚠️ **Switching back reproduces the earlier figures exactly**, to the paisa. That is the
one comfort available here, and it exists because nothing was stored: there is nothing to
lose and nothing to rebuild. If you change a method by mistake, change it back.

Each switch writes a `master_audit` row naming both methods — the only record of why a
period's closing stock moved — and it is an `alter master` write, so it obeys the
back-dated-entry cutoff and asks for your passkey like any other change to the books.

### What is deliberately NOT offered

TallyPrime ships eight costing methods; this build implements three, and an item naming
any of the other five is **refused when you save the item** rather than when a report is
read (a refusal at the report would take out the Stock Summary and the Balance Sheet for
the whole company, every other item included):

- **At Zero Cost** silently zeroes closing stock;
- **Last Purchase Cost** and **Standard Cost** need a price the movement ledger does not
  carry;
- **Monthly Avg. Cost** needs a period boundary a replay has no notion of;
- **LIFO Annual** would cost an April issue at a purchase made the following March — no
  figure could be stated until the year closed, and every interim Profit & Loss would be
  restated by the next purchase keyed. The LIFO offered here is the perpetual variant.

⚠️ **Batch-wise valuation is not a fourth method and never was.** A batch is a dimension
of the position (item, godown, batch), so all three methods already value each batch — and
each godown — as its own independent cost history.

⚠️ **What negative stock does to FIFO.** If `Allow negative stock` is on and an issue
exceeds everything received, the valuer consumes only what was actually there — never a
cost invented from the sale price — and the item's layer history is then gone, because
FIFO cannot say which receipt a unit that was never received came from. The next receipt
rebuilds a single blended layer. Under negative stock a FIFO figure is not a FIFO figure,
and that is a cost of the setting rather than of the method.

---

## A link with a broken company id no longer opens the wrong company

**Applies to:** opening the app with `?company=` in the address bar.
**Register:** §7.3 C.

### What changes

A `?company=` that is not a valid company id — a typo, a template that was never filled
in, an id copied out of a demo — used to be **thrown away without a word**, and the app
then opened whichever company this browser had open last. Real books, fully populated,
nothing on screen wrong. Every report and every voucher screen answered normally about a
company nobody had asked for.

It now refuses: no company is opened, none is created, and the notice names the value it
ignored and says explicitly that your stored company was **not** silently substituted.
One button drops the bad `?company=` and carries on.

⛔ **If you have ever keyed a voucher after following a `?company=` link, check which
company it landed in** — particularly if the link came from a chat message, a spreadsheet
or a template. The address bar showed the id you asked for the whole time.

⚠️ A `?company=` carrying **nothing at all** is unchanged: it means "no preference", and
the app opens your stored company as before.

---

## A refused company now tells you a reload will not help, and offers a way out

**Applies to:** opening the app with a `?company=` the server refuses (HTTP 403).
**Register:** §7.3 D.

### What changes

Two things were wrong with the old notice, and both mattered.

**It told you the wrong reason.** It said you are not a member of that company. The server
cannot actually tell you that: it checks membership before it checks existence — on
purpose, so a stranger cannot discover which companies exist — so **a company that does
not exist looks exactly like someone else's books.** A mistyped or deleted id therefore
read as "wait for your invite", and the invite was never coming.

**And there was no way out.** The id stays in the address bar, so every reload asked the
same dead question and got the same refusal, for ever. F3 (Select Company) does not help:
it opens a company you are already a member of, it does not remove the id that is
refusing.

The notice now states both possibilities, says plainly that reloading cannot change the
answer, and carries a control that drops `?company=` and continues — without a page
reload, and without Back walking you into the refusing URL.

⚠️ **The refusal itself is unchanged and is not being softened.** Nothing is opened and
nothing is created; you are never quietly made the Owner of a new empty company.

---

## A closed period now also stops a Tally import — including a real Tally file

**Applies to:** importing a Tally XML export into a company whose security level has
a back-dated cutoff set (Admin > Security Levels > "backdated cutoff").
**Register:** §7.2, door 7.

### What changes

Setting a back-dated cutoff is what an owner does the day a quarter's returns are
filed: from then on nobody may post into that period. It worked on every screen,
and on the Tally import — **but only for import files saved as UTF-8.**

Files that Gateway of Tally writes are **UTF-16**. On those, the check that decides
whether a body contains any vouchers at all could not read the bytes, concluded
there were none, and let the import through untouched. So a real, unmodified export
straight out of Tally could post vouchers dated inside a filed period, in bulk —
while typing a single voucher of the same date into the same company was refused.
The same blind spot meant editing or deleting a previously-imported voucher through
an import file did not ask for the passkey confirmation it should have.

Both now behave the same for every file, whatever it is saved as.

⛔ **If you have imported a Tally file into a company with a cutoff set, check what
landed in the closed period.** The import reported success and the vouchers are
ordinary vouchers — nothing about them looks unusual in the day book. The Day Book
for the closed dates, or a Trial Balance as at the cutoff compared against your
filed figures, is where a difference would show.

⚠️ **Imports are not otherwise restricted, and this is worth knowing before you set
a cutoff.** A file whose vouchers are all on or after the cutoff imports exactly as
before. Only vouchers dated into the closed period are refused — and the whole file
is refused together, because one import posts every voucher in it, so a file
containing even one back-dated voucher must be split or the cutoff moved.

---

## One supplier bill can now only ever become one voucher — from any machine

**Applies to:** accepting a supplier bill from the Bill Inbox (Ctrl+A on the F9 screen).
**Register:** §7.3 B — **now closed, on both halves.**

### What changes

Accepting a bill does three things in order: reserve the bill, post the voucher, then
record that it was posted. **The third step used to be sent and forgotten.** If it failed
after the voucher was already on the books — a dropped connection, a closed tab, a
restarted service — you were told "Saved" (correctly, the voucher exists) and the bill
stayed in the Bill Inbox looking un-posted, with nothing anywhere that would ever go back
and finish the job. Once the reservation expired the bill could be posted a second time.

That confirmation is now **written down before it is attempted** and retried on your next
accept and every time you open the Bill Inbox, until the server acknowledges it. A bill
that was posted stops being offered.

⛔ **AND THE SERVER NOW RECORDS IT TOO, WHICH IS WHAT ACTUALLY CLOSES THIS.** The note
above is kept in **this browser**, so only this browser can finish the job — which used to
mean that a colleague on another machine, inside that window, could post the same bill a
second time. Their only protection was a check against the day book, and that check cannot
find a bill whose invoice number was never read off the scan.

**The bill it came from is now recorded on the voucher itself, as part of the same save.**
So a second attempt — from any machine, at any time, with or without the note above — does
not post. It is refused, and the refusal **names the voucher your bill is already on**, so
you can go and look at it. Nothing is written by the refused attempt: no voucher, no
voucher number, no input tax credit.

⚠️ **A refused bill does not sit in the inbox for ever.** The machine that was refused
tells the inbox what happened, so the bill stops being offered there too.

⚠️ **This applies to bills accepted from the Bill Inbox and to nothing else.** A purchase
you key yourself is unaffected in every way — there is no bill draft behind it, so there is
nothing to record and nothing that can refuse it.

⚠️ **Deleting the voucher does not put the bill back in the inbox.** Accepting a bill is a
once-only act: if you delete a purchase that came from a scan and want it back, key it, or
upload the bill again so it gets its own draft. This is deliberate — a deleted voucher that
silently re-offered its bill is how one supplier invoice becomes two.

⚠️ **Nothing here can turn a successful accept into an error.** Everything above happens
after the voucher exists, and an operator told "Error" about a bill that posted retries
and posts it twice — which is the defect, arriving through the error path.

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
