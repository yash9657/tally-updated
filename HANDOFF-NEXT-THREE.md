# HANDOFF — the three things immediately left, and the whole parity gap in three tiers

> **Written 2026-08-03**, at HEAD `77a1317` + the plan correction. Companion to
> `HANDOFF-FEATURE-LOCK.md` (the plan of record) and `HANDOFF-SESSION-T29-IMPORT.md` (what the
> last session did). **Read §0 first — it changes what "immediately left" means.**

---

## 0. ⛔⛔ READ THIS FIRST: A DOCUMENTED BLOCKER WAS ALREADY DEAD FOR TWO DAYS

**The TDS/TCS return file is NOT blocked on a human, and the plan said it was for two days after
it stopped being true.**

`docs/statutory/form-140-file-format.xlsx` and `form-143-file-format.xlsx` — the Income Tax
Department's **prescribed file format, version 1.0, TY 2026-27** — have been committed in this
repository since **`cfb83ff`, 2026-07-31**. That is **nineteen commits before HEAD**, and it
predates `HANDOFF-FEATURE-LOCK.md` itself. `docs/statutory/README.md` documents them in detail.

Yet the Item Zero session (2026-08-02) shipped `reporting.ReturnFileRefusal()` and wrote *"the
Form 140 layout and the Protean file format **were never read**"*, and the plan carried that as
open work.

**How the mistake happened, because it will happen again otherwise.** The blocker was recorded
as *"`incometaxindia.gov.in` returns 403 to every automated request"*. **That is true and still
true — I re-confirmed the 403 on 2026-08-03.** But the file format was never on that site. It is
Protean/TIN, which moved to `tinpan.proteantech.in`, and that host serves it without complaint.
The refusal was correct when written and was overtaken the next day; nobody re-checked, because
the blocker was phrased as a fact about a *site* rather than a question about a *document*.

> **The lesson: a blocker is a claim with an expiry date. Re-check it before building around it,
> and write down the DOCUMENT you need, not the site that refused you.**

### What I verified on 2026-08-03, from the primary spec

- All **seven** shipped TDS payment codes, each against its statutory limb — `1023`/`1024`
  (contractor ind-HUF / other), `1026`/`1027` (technical / professional), `1006` (commission),
  `1008`/`1009` (rent machinery / other). **Every one matches `internal/compliance/tds.go`.**
- Including the subtle pair: **`1027` and `1028` share limb 393(1) [Sl. No. 6(iii).D(b)]** but
  are different codes. An engine deriving the code from the section alone would file directors'
  fees as professional fees. `tds.go` already keys on the pair. Correct.
- The physical format: ASCII `.txt`, **`^`-delimited variable width**, CRLF, delimiter count =
  fields − 1, constants in block capitals, **amounts 2dp, rate 4dp**, dates `ddmmyyyy` and never
  future, record types `FH`/`BH`/`CD`/`DD`, upload type `R`, uploader type `D`.
- A statement with **zero deductee records will not validate**.

### What is STILL blocked, and it is a different thing

**Validation, not generation.** The FVU needs a Java runtime plus Protean's RPU zip; the `.csi`
challan file needs a TIN portal login. So:

> **Generate the return with a `format_version` traced to the read primary source — which is
> exactly what invariant I-7 asks for — and NEVER describe it as validated.** That is the
> deterministic-fake-plus-recorded-schema arrangement `internal/statutory` already uses for IRN,
> and it must be said on the artefact's own face.

---

## 1. THE THREE THINGS IMMEDIATELY LEFT

Ordered by value. All three are unblocked.

### 1.1 T6 — the Form 140 / 143 return file (**newly unblocked; do this first**)

Everything upstream is done: both taxes derive, refuse, persist, and are visible in a register.
`reporting.ReturnFileRefusal()` is the thing to replace.

| | |
|---|---|
| **Read first** | `docs/statutory/README.md` (all of it), then `form-140-file-format.xlsx` sheet `File Format_R` |
| **Format version** | `1.0`, TY 2026-27 — put it in `format_version` and make the test assert it is non-empty |
| **Codes** | Annexure 2, already verified. `tds.go`'s map is correct — do not re-derive it |
| **Reason flags** | Annexure 6 is **thirteen flags** (A,B,C,D,E,N,O,P,Q,S,T,Y,Z), **not "A–D"**. It is a flag × section-code **validity matrix**, so it is a validation rule and not a lookup. `Y` (below threshold) is the one the engine will use most |
| **Do NOT** | claim validation; copy Form 143's flag set to Form 140 (they differ — 143's are A,B,F,K,Y,Z); or invent a field order the spec does not give |
| **Two identifiers with nowhere to live yet** | the **s.395 certificate number** and the **UIN of Form No. 121**. Both returns name them. Decide where they go before writing the emitter, not after |

**A naming drift to resolve in the same reviewable step:** our internal section identifiers are
still 1961-Act nomenclature (`206C(1)-SCRAP`, `194J-P`) while the return keys off **s.393/394
Table Sl. Nos.** The four-digit codes are the bridge. `README.md` asks that the rename travel
*with* the T6 work rather than before it.

### 1.2 T29 slice 2b — statement matching

The import shipped 2026-08-03. This is its other half, and it is the half that carries the rule.

> **§0 of `docs/design/t4-slice2-model.md`: NEVER AUTO-CLEAR AN AMBIGUOUS MATCH.** Two vouchers,
> same amount, same day is **ordinary** — two suppliers paid ₹25,000 on the 14th. A matcher that
> picks one is right half the time and **the half it gets wrong is invisible**: both vouchers
> exist, both amounts are correct, the statement reconciles either way, and it surfaces only when
> somebody chases a supplier for a payment the books say cleared. Ambiguity is a **first-class
> outcome** with its own status; it surfaces **both** candidates and waits.

- `bank_statement_match` takes **a new migration number at the end** (0062–0064 are consumed).
- **The edge already waiting:** `internal/bankstatement`'s archtest policy is `{platform/db}`.
  The tempting addition is `bankstatement → banking` so a matched row writes its own clearance.
  Memo §2.1 wants that clearance in **0045's `bank_reconciliation`** instead, because that table
  is what carries the BROKEN-BY-ALTER mechanism. Add the edge deliberately and write down why.
- ⚠️ **Direction is inverted relative to the bank's wording — a DEPOSIT is `'D'`.** Get it
  backwards and the matcher produces **zero matches**, which reads as "nothing to reconcile"
  rather than as a bug. This is the single thing 2b lives or dies on.
- Flip `F8`, `Alt+F8`, `Alt+S`, `F9` from refusals to real actions **in the same commit**, and
  revisit `Ctrl+J` (exception report) which is a matching concept.

### 1.3 T6 — the challan register

Still true, still the biggest hole in the feature: **no `tds_challan` table exists, so nothing
tracks what has been DEPOSITED** — and it is the deposit, not the deduction, that stops interest
running under s.396. `t6-model.md` §1.5. Needs no unread document. Note it interacts with 1.1:
the return's `CD` (challan detail) records need exactly this data, so **doing 1.1 first will tell
you the shape 1.3 must store.**

---

## 2. THE PARITY GAP, IN THREE TIERS

Counts verified against the code on 2026-08-03, not recalled.

### Tier 1 — the 15 the product itself admits are off

`FeaturesScreen.tsx` is the honesty surface and its own census is **25 `on`, 3 `server`, 2
`always`, 15 `off`**. The fifteen:

| Track | Feature | Note |
|---|---|---|
| T2 | Cost centres + cost categories | **HIGH risk.** Allocations live *inside the hashed payload*; the sum-to-posting invariant is a constraint trigger. T11 and T24 depend on this |
| T3 | Sales/Purchase Orders + BOM | **HIGHEST risk, and the most-used missing feature.** An order is a **COMMITTED QUANTITY, NOT A MOVEMENT** — through `stock_movement` it corrupts closing stock and the Balance Sheet |
| T5 | e-Way Bill | Reuse the IRN precedent: NIC unreachable, ship the recorded-schema arrangement and say so on the document |
| T6 | TDS / TCS | Engines, storage, register all done. Row stays `off` until a return can be filed — **see §1.1, now unblocked** |
| T8 | Multi-currency | HIGH. Realised + unrealised forex, rate table. Deep but isolated |
| T9 | FIFO / LIFO / batch valuation | **HIGH — it MOVES THE BALANCE SHEET.** Re-pin `verify:demo` |
| T10 | Stock groups + compound units | Serialise with T9 |
| T11 | Budgets + budget-vs-actual | Depends on T2 |
| T12 | GSTR-9 | It is a **RECONCILIATION** of the year's GSTR-1/3B to the books, not a fresh derivation |
| T13 | Interest on overdue bills | Effective-dated rates, simple vs compound |
| T14 | Backup / restore / split company | **HIGHEST BLAST RADIUS — a bug loses somebody's books.** Own wave slot. Reuse `testdb`'s delete-plan census |
| T15 | Export (Excel/CSV/XML/e-mail) | Low technically. **`authz.Export` has ZERO route mappings so it default-DENIES** — that ACL decision must be made explicitly |
| T16 | Per-company print templates | The override table exists; nothing writes it |
| T17 | SSO / OIDC | Low-medium |
| T18 | Rule 48(4) QR on e-invoice | **Structurally blocked — see §3** |

### Tier 2 — 16 tracks that are not on the honesty surface at all

This is the half that gets underestimated: **`FeaturesScreen` does not have a row for any of
these**, so the product does not even admit they are missing. T19–T34 in the plan's §3.2.

I probed the codebase for accidental partial builds on 2026-08-03. **Genuinely absent:** cheque
printing, price lists/levels, POS, ODBC/sync, BOM, cost centres, credit limits, reorder levels,
in-transit stock. The greps that hit were comments and unrelated words (`reorder` was
"reorders object keys"; `cheque` was the word in a narration).

| Track | Feature | State |
|---|---|---|
| T19 | Voucher classes, optional / scenario / reversing / memorandum / post-dated vouchers | absent |
| T20 | Price levels, price lists, per-party discount columns | absent |
| T21 | Reorder levels, stock ageing, negative-stock control | absent |
| T22 | Multi-godown transfers with **IN-TRANSIT** stock | godowns exist; in-transit is a real third state and is absent |
| T23 | Manufacturing journals, job work, rejections in/out | absent |
| T24 | Job / project costing | absent; interacts with T2 |
| T25 | POS / retail billing | absent |
| T26 | Payroll attendance, leave, gratuity, loans, Form 16 | payroll ships, but **attendance is a request parameter, not storage** — `payrolldb` calls it "facts the database does not hold" |
| T27 | Cheque printing + cheque book management + payment advice | absent. `RESEARCH.md:881–885` has the full spec |
| T28 | Credit limits + credit-control blocking on sale | absent |
| ~~T29~~ | ~~Bank statement import~~ | ✅ **import shipped**; matching is §1.2 |
| T30 | MIS: ratio analysis, cash flow, funds flow, multi-period columns | absent. Cash Flow is already a *named* report answering 501 |
| T31 | Multi-company / group consolidation | absent |
| T32 | **Data import FROM Tally** | ⚠️ **PARTLY BUILT — the plan overstates this gap.** `internal/migration` parses a Tally XML `ENVELOPE`, creates masters on demand, posts through the sole ledger write path, and reconciles the Trial Balance to the paisa. **But: six ACCOUNTING voucher classes only** (contra/payment/receipt/journal/sales/purchase — no inventory vouchers), **XML envelope only** (not the binary `.900` files), and **never tested against a real Tally export** |
| T33 | ODBC / Tally.NET-style remote access / sync | absent |
| T34 | Composition scheme, RCM, ISD, e-invoice cancellation windows | absent |

> **T32 is a commercial blocker dressed as a feature.** A perfect clone that cannot read a real
> Tally backup has no migration path and therefore no users. What exists is a real start; what it
> needs is **a real Tally export to test against**, which is a human task (§3.3).

### Tier 3 — the papercuts, and nobody has counted them

**Unenumerated by construction.** Nobody has driven this app through a real business day. Two of
the most-used buttons in the product were found missing exactly that way — by using it, not by
reading code.

So the honest total is: **Tier 1 + Tier 2 ≈ 31 known tracks, and Tier 3 is unmeasured.** Parity
cannot be claimed until Tier 3 has been measured, and it can only be measured by a human keying
real vouchers, reconciling a real statement, and printing a real invoice for a day.

---

## 3. THE TWO THINGS THAT WILL NEVER CLOSE BY WRITING CODE

Both are in the plan as blockers. Neither is a hard problem — they are **permissions problems**,
and no amount of work or research substitutes.

### 3.1 The Rule 48(4) QR code on an e-invoice (T18)

**What it is.** When you issue a GST e-invoice above the turnover threshold, you must upload it
to the government's Invoice Registration Portal. The IRP sends back an IRN and **a QR code that
the government has digitally signed**. That QR is the government's own signature saying "we
registered this invoice". You print *their* QR on your invoice.

**Why code cannot produce it.** The QR contains a cryptographic signature made with **the
government's private key**. We do not have that key and never will — that is the entire point of
a signature. Generating a QR that *looked* like theirs would be **forging a government
signature**: anyone scanning it would be told the invoice was registered when it was not.

**So it is not "hard", it is "not ours to make".** It closes the day we have real IRP access:
we call them, they sign, we print what they sent. Until then the only honest options are to omit
the QR or to print a clearly-marked non-government placeholder — which is what the codebase
already does for IRN.

### 3.2 The government systems this environment cannot reach

Six of them: the **FVU** (TDS return validator), the **GSTN portal**, the **NIC e-Way Bill
API**, the **IRP**, the **EPFO/ESIC portals**, and the **`.csi` challan file**. They are blocked
for **three different reasons**, and only the first is mine to fix:

| Kind of blocker | Which | Who can clear it |
|---|---|---|
| **A document behind a fetcher-hostile site** | Form 140/143 formats, gazette PDFs | ✅ **Already cleared** — and §0 is the lesson. `incometaxindia.gov.in` and `egazette.gov.in` refuse automated fetchers; a browser gets through in seconds |
| **A tool that must be downloaded and run** | FVU (needs a **Java runtime** + Protean's RPU zip) | **You**, with a browser and a JRE. Then we can say "validated" instead of "generated" |
| **Credentials tied to a real GSTIN / establishment** | IRP, GSTN portal, e-Way Bill API, EPFO/ESIC, `.csi` | **Only you**, by registering. There is no research path — the whole point of a credential is that it identifies a specific taxpayer |

**The rule the codebase already follows, and must keep following:** ship the deterministic fake
plus the recorded schema, and **say so on the artefact's own face**. Never claim a validation
that was not performed. A "generated" return that reads as "validated" is worse than no return.

---

## 4. WHAT I NEED FROM A HUMAN — the whole list, shortest first

| # | Ask | Unblocks | Effort |
|---|---|---|---|
| **1** | **A real Tally export** (XML from any version, or a backup) | T32, the commercial migration path. What exists has never met a real file | minutes to export |
| **2** | **A JRE + the Protean RPU zip** | Lets us say the TDS return is *validated*, not merely generated. **The FORMAT is no longer part of this ask** | ~15 min |
| **3** | **One real business day on the app** | **Tier 3, the highest-value item in the plan.** Key real vouchers, reconcile a real statement, print a real invoice, write down every annoyance | a day |
| **4** | **IRP / GSTN / e-Way Bill sandbox credentials** (needs a GSTIN) | T5 e-Way Bill, T18's QR, e-invoicing for real | registration |
| **5** | **EPF/EDLI administrative-charge rate notification**, if one has been issued since 2026-07-30 | ~₹500/establishment/month accruing, currently and correctly refusing. **Do NOT substitute `S.O. 2701(E)`** — that is *inspection* charges, a different levy | a search |

**Everything else I can get myself.** I re-confirmed on 2026-08-03 that I can reach
`tinpan.proteantech.in` (the file formats), ICAI's mirror of the Gazette Act text, and ordinary
web sources. What I cannot do is authenticate as a taxpayer, or run Java.

⚠️ **And the standing caution, from `HANDOFF-FEATURE-LOCK.md` §1.2:** secondary sources are not
a substitute for a primary one. The seven payment codes turned out to be right — but the same
secondary source that carried them **got thresholds wrong**, and a shipped rate was half the
legal rate for four months because a verification was measured against an unamended edition of
the Act. **Record the edition. Re-read on every Finance Act.**
