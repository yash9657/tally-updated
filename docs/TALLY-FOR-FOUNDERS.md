# Tally for Founders — learning the product by walking its own books

**Who this is for:** someone who has never opened Tally, is building a Tally
competitor, and needs to understand what their product actually does at an
operational level.

**How to read it:** every example below is a real row in the demo company
`Demo Traders`. No figure here is invented — each one was read back from the
running system, and the whole document is gated by `pnpm verify:demo`, which
re-derives all 18 claims from the live stack and fails if any drifts.

```bash
make dev-db          # Postgres on :5433
make migrate         # apply schema
make run-cored       # API on :8087
make run-insightd    # AI plane on :8088, with the DURABLE draft store
make seed-demo       # build Demo Traders, print its URL
cd frontend && pnpm verify:demo <companyID>
```

---

## 1. The mental model: Groups and Ledgers

Everything in Tally hangs off two ideas.

A **ledger** is an account you post money to. `HDFC Bank`, `Acme Traders`,
`Rent` are all ledgers. A **group** is the bucket a ledger sits in.

The load-bearing point, and the one that catches people: **the group, not the
ledger, decides what a number means.** `Acme Traders` is money owed *to us*
because it sits under `Sundry Debtors`. Move that same ledger under `Sundry
Creditors` and the Balance Sheet silently reports the company as owing money it
is in fact owed. No error appears. This is why the Phase 1 audit checked every
ledger's parent group one by one.

Tally ships **28 predefined groups** — 15 primary and 13 sub-groups — and every
ledger must live under one of them (or a custom sub-group). Demo Traders has all
28, seeded at company creation.

Here is the company's actual chart of accounts, as at the last verified run:

| Ledger | Group | Closing |
|---|---|---:|
| HDFC Bank | Bank Accounts | 3,70,000 Dr |
| Cash | Cash-in-Hand | 41,400 Dr |
| Acme Traders | Sundry Debtors | 1,73,680 Dr |
| Mumbai Metals | Sundry Debtors | 80,240 Dr |
| Steel Suppliers | Sundry Creditors | 80,450 Cr |
| Delhi Parts | Sundry Creditors | 23,600 Cr |
| Pune Fabricators | Sundry Creditors | — (no transactions yet) |
| Capital A/c | Capital Account | 5,02,000 Cr |
| Domestic Sales | Sales Accounts | 1,44,500 Cr |
| Raw Material Purchases | Purchase Accounts | 47,500 Dr |
| Rent | Indirect Expenses | 45,000 Dr |
| Electricity | Indirect Expenses | 8,600 Dr |
| Output CGST / SGST / IGST | Duties & Taxes | 6,090 / 6,090 / 12,240 Cr |
| Input CGST / SGST / IGST | Duties & Taxes | 2,475 / 2,475 / 3,600 Dr |

Two conventions worth internalising:

- **Dr / Cr are not "plus / minus".** For an asset, Dr increases it. For a
  liability or income, Cr increases it. `Domestic Sales` sitting at 1,44,500 **Cr**
  means we sold that much.
- **Duties & Taxes holds both sides.** `Output` heads are tax you collected and
  owe the government (Cr). `Input` heads are tax you paid suppliers and can
  reclaim (Dr). The net of that group is what you actually remit.

---

## 2. The keyboard is the interface

Tally's moat is muscle memory. A Tally operator does not reach for a mouse, and
this product replicates the same grammar. These keys are all bound and working:

| Key | Does |
|---|---|
| `F4` `F5` `F6` `F7` `F8` `F9` | Contra · Payment · Receipt · Journal · Sales · Purchase |
| `Alt+F5` `Alt+F6` | Debit Note · Credit Note |
| `Enter` | next field / drill into the highlighted row |
| `Esc` | back one level |
| `Ctrl+A` | **Accept** — save the voucher |
| `Ctrl+Q` | quit the screen *without* saving |
| `F2` / `Alt+F2` | change date / change period |
| `Alt+G` / `Ctrl+G` | Go To (overlay) / Switch To (replaces the stack) |
| `Alt+C` | create a master inline, without leaving the voucher |
| `Ctrl+F` | filter rows · `Alt+E` export · `Ctrl+N` calculator |
| `F1` | help — the live key list for the current screen |
| `F11` | Company Features · `F12` configure the current screen |

**A design decision worth knowing.** Keys for features this edition does *not*
have are still bound — they answer with what is missing and why, instead of
doing nothing. `Alt+F7` (Stock Journal) says inventory vouchers are not built.
That is deliberate: a dead key is indistinguishable from a bug.

---

## 3. Creating a company, field by field

Demo Traders' real values, and why each field exists:

| Field | Value | Why it matters |
|---|---|---|
| Name | Demo Traders | — |
| Mailing name | Demo Traders | Printed on invoices. Tally keeps it separate so a trading name can differ from the registered one. |
| Address / City / Pincode | 14, 3rd Cross, Industrial Layout, Koramangala · Bengaluru · 560095 | Appears on invoices and statutory documents. |
| Country | India | — |
| **State code** | **29** (Karnataka) | **The single most consequential field.** It decides CGST+SGST vs IGST on every invoice. Get it wrong and every tax split in the books is wrong. |
| GSTIN | `29AACCD1234K1Z8` | Your own registration. It is the top-level `gstin` on every GSTR-1 you file. Without it the return cannot be filed. |
| Financial year begins | 2026-04-01 | Fixes the reporting year and restarts voucher numbering. |
| **Books beginning from** | 2026-04-01 | **A different date, and the subtle one.** See below. |
| Base currency | INR, symbol ₹, 2 decimals | Display only — money is stored at 4 decimal places regardless. |

### Why "books beginning from" is separate

They are equal for Demo Traders, which starts fresh at the top of its year. They
differ the moment a real business migrates mid-year.

A company switching systems on 1 October still has a financial year that began
1 April. Its year start is 2026-04-01; its books here begin 2026-10-01. **Opening
balances are stated as at *books beginning from*, not as at the year start** —
because the first six months already happened somewhere else.

Collapsing the two dates makes that migration unrepresentable and silently
mis-dates every opening balance. The database refuses books that begin *before*
the year opens (`CHECK books_beginning_from >= fy_start_date`).

---

## 4. Opening balances — how books begin

When a business moves onto a new system, it does not start from zero. It carries
in what it owns and owes. Demo Traders' opening position, as at 2026-04-01:

```
Cash                1,50,000 Dr
HDFC Bank           2,50,000 Dr
Acme Traders        1,20,000 Dr     (a customer who owed us)
Steel Suppliers        68,000 Cr    (a supplier we owed)
                    ─────────────
Capital A/c         4,52,000 Cr     ← the balancing figure
```

**Capital is derived, not decided.** You know your assets and liabilities; the
owners' equity is whatever reconciles them:
`1,50,000 + 2,50,000 + 1,20,000 − 68,000 = 4,52,000`.

### Bill-wise opening breakup

Here is the concept this demo could not teach before this work, because the data
model had nowhere to put it.

`Acme Traders` does not owe a lump of 1,20,000. It owes **specific invoices**:

| Reference | Amount | Due | Age slab today |
|---|---:|---|---|
| `OB/ACME/24-25/07` | 70,000 | 2026-04-20 | 90–180 days |
| `OB/ACME/24-25/11` | 50,000 | 2026-05-05 | 45–90 days |

Tally makes you enter that breakup and **refuses one that does not add up** to
the opening balance. So does this system. The reason is visible in the table: the
two bills fall in *different* ageing buckets. A lump-sum opening would make every
carried-forward rupee look equally old, and the ageing report would be fiction
from day one.

---

## 5. The voucher types, each with a real example

A **voucher** is one transaction. Its **type** decides which reports see it.
Every voucher in Demo Traders:

| Date | Type | No. | What it is |
|---|---|---|---|
| 2026-04-02 | Contra `F4` | 1 | Cash deposited to HDFC — 1,00,000 |
| 2026-04-12 | Sales `F8` | 1 | INV-001 to Acme Traders, 50,000 @18% |
| 2026-04-20 | Purchase `F9` | 1 | SUP-101 from Steel Suppliers, 30,000 @18% |
| 2026-05-08 | Sales `F8` | 2 | INV-002 to Mumbai Metals, 80,000 @18% |
| 2026-05-15 | Purchase `F9` | 2 | SUP-102 from Delhi Parts, 20,000 @18% |
| 2026-05-20 | Journal `F7` | 1 | Additional capital introduced — 50,000 |
| 2026-05-30 | Receipt `F6` | 1 | Part payment against INV-001 — 35,000 |
| 2026-06-05 | Payment `F5` | 1 | Payment against SUP-101 — 20,000 |
| 2026-06-19 | Sales `F8` | 3 | INV-003 to Acme, altered 24,000 → 26,500 @12% |
| 2026-06-30 | Payment `F5` | 2 | Office rent — 45,000 |
| 2026-07-01 | Journal `F7` | 2 | Electricity accrued for June — 8,600 |
| 2026-07-08 | **Credit Note** `Alt+F6` | 1 | Sales return against INV-002 — 12,000 |
| 2026-07-12 | **Debit Note** `Alt+F5` | 1 | Purchase return against SUP-101 — 2,500 |

### Contra (F4) — money between your own accounts

`Dr HDFC Bank 1,00,000 / Cr Cash 1,00,000`. No income, no expense, no party.
Tally gives it its own type precisely so it never pollutes the P&L.

### Sales (F8) — the shape of an invoice

INV-001, the intra-state case:

```
Dr  Acme Traders      59,000     ← what the customer owes, tax included
Cr  Domestic Sales    50,000     ← the income
Cr  Output CGST        4,500
Cr  Output SGST        4,500
```

Acme is in Karnataka (29), same as us, so the 18% splits into **CGST 9% + SGST 9%**.

INV-002 to Mumbai Metals (Maharashtra, 27) is the inter-state case: the same 18%
becomes a single **IGST 14,400**. Same rate, different heads, decided entirely by
comparing two state codes.

**The client never decides this split.** You may type the tax lines, but the
server re-derives them and *rejects the voucher* if they disagree. Tax cannot be
silently hand-fudged.

### Receipt (F6) and bill-wise settlement

The 35,000 received from Acme carries `AGAINST INV-001`. That is what makes
Outstandings show INV-001 with **24,000 still open** rather than the full 59,000.
Without the reference the payment would reduce Acme's balance but no bill would
close, and the ageing report would drift from the ledger.

### Journal (F7) — no cash moves

`Electricity accrued for June`: the expense belongs to June even though it will
be paid later. This is accrual accounting, and it is why P&L profit and bank
balance are different numbers.

### Credit Note (Alt+F6) — the right way to undo part of an invoice

On 8 July, 12,000 of the Mumbai Metals sale came back:

```
Dr  Domestic Sales    12,000     ← income reverses
Dr  Output IGST        2,160     ← the tax on it reverses
Cr  Mumbai Metals     14,160     ← they no longer owe it
```

**Why this instead of editing INV-002?** Three reasons, and they are the whole
argument for the feature:

1. **The return is a real event with its own date.** Goods went out in May and
   came back in July. Rewriting the May invoice erases that.
2. **May may already be filed.** Altering a filed invoice back-dates a change
   into a period you have already told the government about.
3. **GST has a dedicated section for it** — `cdnr`, table 9B — keyed to the
   original invoice. A return expressed as an alteration is invisible there.

It gets **its own number series**: this is Credit Note **1**, while Sales is at
3. A credit note never consumes an invoice number.

### Debit Note (Alt+F5) — and which way a note faces

The 12 July debit note is a *purchase* return — defective steel sent back:

```
Dr  Steel Suppliers    2,950     ← we owe them less
Cr  Raw Material Purchases 2,500
Cr  Input CGST / SGST    225 each ← the credit we claimed is reversed
```

Note what decides where this is reported. **It is not the voucher type — it is
who the party is.** A note against a customer (`Sundry Debtors`) adjusts outward
supply and goes in GSTR-1. A note against a supplier (`Sundry Creditors`) adjusts
input credit and goes in GSTR-3B. Both note types can face either way. This
system decides from the party's group, which is why the supplier note above
correctly stays *out* of GSTR-1.

---

## 6. The daily and monthly rhythm

**Daily — enter and check.** `F8`/`F9` for invoices as they arrive, `F6`/`F5` as
money moves, `Ctrl+A` to accept each. Then `Alt+G → Day Book` and `Alt+F2` to set
the period; `Enter` on any row drills into the voucher behind it.

**Weekly — chase money.** `Outstandings` is the collections list. Demo Traders'
receivables today:

| Party | Reference | Pending | Due | Age |
|---|---|---:|---|---|
| Acme Traders | `OB/ACME/24-25/07` | 70,000 | 2026-04-20 | 90–180 days |
| | `OB/ACME/24-25/11` | 50,000 | 2026-05-05 | 45–90 days |
| | INV-001 | 24,000 | 2026-05-12 | 45–90 days |
| | INV-003 | 29,680 | 2026-07-19 | 0–45 days |
| Mumbai Metals | INV-002 | 80,240 | 2026-06-22 | 0–45 days |
| | **Total** | **2,53,920** | | |

Two things to read here. INV-001 shows 24,000, not 59,000, because the receipt
settled against it. INV-002 shows 80,240, not 94,400, because the credit note
reduced it. Both are the bill-wise machinery working.

**Monthly — GST.** `GSTR-1` (what you sold) and `GSTR-3B` (the summary and what
you can reclaim). Check the return ties to the books before filing — see §7.

**Monthly — review.** Trial Balance must net to zero; Balance Sheet must balance.
If either does not, stop.

---

## 7. The reports, and what "correct" means

### Trial Balance — the integrity check

`Dr 7,74,970 / Cr 7,74,970`. Every voucher is balanced by construction (the
database refuses an unbalanced one), so this is a proof that nothing is corrupt.

### Profit & Loss — did we make money?

```
Income    1,44,500     (sales 1,56,500 less the 12,000 return)
Expenses  1,01,100     (purchases 47,500 + rent 45,000 + electricity 8,600)
          ─────────
Profit       43,400
```

### Balance Sheet — what we own and owe

```
ASSETS                          6,65,320
  Bank Accounts    3,70,000
  Cash-in-Hand        41,400
  Sundry Debtors   2,53,920

LIABILITIES                     6,65,320
  Capital Account  5,02,000     (4,52,000 opening + 50,000 introduced)
  Duties & Taxes      15,870    (output tax owed, net of input credit)
  Sundry Creditors 1,04,050
  P&L (current)       43,400    ← the profit above, carried in
```

Assets equal liabilities exactly. That is not a coincidence — it is the
definition of double-entry, and it is checked on every run.

### GSTR-1 — what you sold

```
gstin  29AACCD1234K1Z8
b2b    27AACCM9012M1Z7  INV-002  80,000  IGST 14,400
       29AACCA5678L1Z3  INV-001  50,000  CGST 4,500 SGST 4,500
                        INV-003  26,500  CGST 1,590 SGST 1,590
cdnr   27AACCM9012M1Z7  Credit Note 1   12,000  IGST 2,160
hsn    7323 @12%  26,500      7326 @18%  1,18,000
```

Read what each section means:

- **`b2b`** — sales to *registered* businesses, reported invoice by invoice
  against their GSTIN. This is how your customer gets their input credit.
- **`cdnr`** — the credit note, as its own document.
- **`hsn`** — the mandatory rate-wise summary. Note two rows: the 12% kitchenware
  line and the 18% steel line are genuinely different products.

### GSTR-3B — the summary, and your money

```
outward   txval 1,44,500   IGST 12,240  CGST 6,090  SGST 6,090
ITC       CGST 2,475  SGST 2,475  IGST 3,600   (= 8,550 reclaimable)
```

The ITC figure equals, to the paisa, the balances sitting in the Input CGST/SGST/
IGST ledgers. That equality is the point: **the return and the books are two
independent paths to the same number.**

### These reports used to be silently wrong — and why that matters

This is the part worth dwelling on, because it is an argument about
architecture, not about bugs.

Before this remediation, Demo Traders' Trial Balance balanced, its Balance Sheet
balanced, and its GST returns were **wrong in four separate ways**, with nothing
on any screen indicating a problem:

| What was wrong | What you would have filed |
|---|---|
| Party ledgers had no GSTIN | Every sale filed as **B2C**. Customers never receive input credit. |
| Purchase vouchers carried no GST block | ITC reported as **zero** while 9,000 sat in the Input ledgers. You overpay. |
| An **altered** invoice lost its voucher type | The corrected INV-003 **vanished** from GSTR-1 — sales of 1,56,500 in the P&L, 1,30,000 in the return. |
| HSN was discarded after derivation | `hsn_sc: ""` on every line. Not a filable return. |

And two reports over the same books disagreed:

| | Before | Now |
|---|---|---|
| BS Sundry Debtors vs Outstandings | 1,48,080 vs 1,45,280 | **2,53,920 == 2,53,920** |

The lesson for the product: **a report that balances is not a report that is
correct.** Internal consistency is cheap; agreement between independently
computed views is what actually catches errors. Every cross-check in §7 exists
because one of them caught a real defect.

---

## 8. Beyond the ledger: the AI plane

**Bill Inbox** (`Gateway → Bill Inbox`). A supplier bill is OCR'd into a *draft*
purchase voucher. The pending draft in Demo Traders:

```
Pune Fabricators   PF/2026-27/0117   2026-07-10
  Raw Material Purchases  D  18,000
  Input IGST              D   3,240      ← 27 → 29 is inter-state
  Pune Fabricators        C  21,240
  3 PII fields redacted before anything left the boundary
```

Two structural properties, not features:

- **The AI plane has no write path to the ledger.** It produces proposals. A
  human presses `Ctrl+A` in F9. That is enforced by the module boundary, not by
  policy — `insightd` holds no write client.
- **PII is redacted at the boundary**, fail-closed: the service refuses to start
  with the boundary disabled.

**Ask your books** (`Gateway → Query`) compiles plain English into a closed query
DSL, then into parameterized, read-only, tenant-scoped SQL. It cannot write, and
it cannot reach another company's data.

---

## 9. How this maps to our product

### What is real today

Everything in this document was executed against the running system and verified
by `pnpm verify:demo` (18/18 checks). Specifically:

- All six accounting voucher types plus Credit and Debit Notes, on their real
  Tally keys, with independent numbering series.
- GST derived and **re-verified server-side**; GSTR-1 (b2b / b2cl / b2cs / cdnr /
  HSN) and GSTR-3B, both tied back to the postings.
- Bill-wise tracking including opening breakups; Outstandings agreeing with the
  Balance Sheet to the paisa on both the receivable and payable sides.
- Opening balances stated as at *books beginning from*.
- An **immutable, hash-chained edit log**: an alter or delete appends a new
  version and never mutates the old one.
- e-Invoice / IRN generation, with a voucher freezing against edits once pinned.

### Deliberately not built

Stated plainly, because a demo that implies otherwise is worse than one that
admits it. `F11` in the product lists these with the same honesty:

| Missing | Consequence you can see |
|---|---|
| **Inventory** (stock vouchers, godowns, batches, BoM, compound units) | `Stock-in-Hand` is always empty; the Balance Sheet can never show closing stock. |
| **Cost centres** | No allocation dimension on postings. |
| **Multi-currency** | The base currency is the only one; its symbol and decimals are configurable, forex is not. |
| **TDS / TCS** | The derivation engines exist and are corpus-gated, but nothing is persisted or reported — treat as not built. |
| **e-Way Bill** | Not built. |
| **Single-entry mode** for Payment/Receipt/Contra | Tally defaults these to single-entry; every screen here is explicit Dr/Cr. The config already carries the flag, but nothing reads it. |
| **Party picker reads a fixed list** | The voucher screen's party dropdown is a hardcoded sample list, not this company's chart of accounts. The read API now exists; the screen has not been switched over. |

### What is genuinely different from legacy Tally

These are the claims that survive scrutiny:

- **Structural audit trail.** Vouchers are append-only and hash-chained. An
  "edit" is a new version chained to the previous one's hash. It cannot be
  switched off, so the MCA Rule 11(g) edit-log requirement is an architectural
  invariant rather than a setting someone can disable.
- **Server-authoritative tax.** The client cannot decide a CGST/SGST/IGST split.
  The server re-derives and refuses contradicting vouchers.
- **Real multi-tenancy.** Every table is row-level-security protected and
  default-deny; tenant isolation is enforced by the database, not by query
  discipline.
- **An API.** Everything this document did — creating the company, posting
  vouchers, pulling returns — went through the same REST surface the UI uses.
- **An AI plane that structurally cannot write** to the ledger.
- **Zero-retraining keystroke fidelity** as the adoption argument: the F-key
  grammar above is Tally's, not ours.

---

## Appendix — reproducing every figure

```bash
cd frontend
pnpm seed:demo -- --insightd     # rebuild Demo Traders, print its URL
pnpm verify:demo <companyID>     # 18 checks; exits non-zero on any drift
```

The verifier is deliberately non-vacuous: run it against a pre-remediation
company and it fails 12 of 16 checks, naming each defect.
