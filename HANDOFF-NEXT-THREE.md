# HANDOFF — the three things immediately left, and the whole parity gap in three tiers

> **Written 2026-08-03**, at HEAD **`d840bef`**. Companion to `HANDOFF-FEATURE-LOCK.md` (the plan
> of record) and `HANDOFF-SESSION-T29-IMPORT.md` (what the last session did).
>
> **State:** migrations **`0070`** · backend **2014 pass / 0 SKIP / 0 FAIL** ·
> frontend **464** · `verify:demo` **33/33** · `make verify` exit 0.
>
> **Read in this order:** §0 (a blocker that was already dead — it changes what "left" means) →
> §5 (how to RUN it; do this before touching code) → §4.1 (**the trap that will bite slice 2b
> four times over**) → then §1.
>
> ---
>
> ### ✅ UPDATE 2026-08-04 — §1.1's FORMAT WORK IS DONE, AND IT REORDERED §1.3
>
> `internal/compliance/form140` now encodes the prescribed file format from the workbook: all
> four record layouts (**FH 18 · BH 72 · CD 30 · DD 45** fields), every annexure, and a pure
> emitter with a reviewed golden file. `reporting.ReturnFileRefusal()` no longer claims the
> layout is unread — `format_version` is `"1.0"` with the document cited on the register's own
> face. **27 new backend tests, 3 new frontend tests.**
>
> **⛔ THE ORDERING IN §1.1 vs §1.3 WAS WRONG, AND THE SPEC SAYS SO.** §1.3 called the challan
> register "the third thing" that merely *interacts* with the return. It is a **HARD
> PREREQUISITE**: DD field 5 is a **challan reference**, so **no deductee record can exist
> outside a challan** — not even under a nil one. See §1.1′ and §1.3′ below.
>
> **Migrations `0069`/`0070` then shipped in the same session**, and a Form 140 file now
> assembles from real books (§1.3′). **What is left to make it usable is a WRITE PATH:** there
> is no route, no screen and no ACL for entering a challan or a deductor profile, so both are
> seeded by SQL in tests today. **That is the next slice — see §1.4′.**

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

### 1.1′ ✅ DONE 2026-08-04 — the Form 140 format, read and implemented

`internal/compliance/form140`: `doc.go` (provenance) · `layout.go` (the four record tables) ·
`annexure.go` (Annexures 1–7) · `row.go` (the serialiser) · `build.go` (rules + emitter) ·
`testdata/q2-2026-27.txt` (the golden file, read line-by-line against the workbook).

**What the primary spec settled that would otherwise have been guessed wrong:**

| Finding | Why it matters |
|---|---|
| **Annexure 6 is a flag × code VALIDITY MATRIX, and `Y` is not universal.** It is **not valid against `1008`/`1009` (rent)** or **`1028` (directors' fees)** | Rent *has* a ₹50,000/month threshold, so sub-threshold rent payments are ordinary — and there is **no below-threshold flag available for them**. `1009` admits only A, B, C, E. `Build` returns `ErrNoValidReasonFlag` rather than picking between a blank flag and a forbidden one |
| **`1028` having no `Y` agrees with the statute independently.** `compliance`'s `194J-D` carries `BasisNil` because s.393(1) Sl. No. 6(iii) states the threshold as "Nil" | Two documents read at different times by different means agree a directors' fee cannot be below a threshold. `TestDirectorsFeesHaveNoBelowThresholdFlagAndTheStatuteAgrees` pins the pair |
| **Flags A, C and E are valid against ALL 40 Annexure 2 codes** — checked row by row | Lets the transcription express them as the whole domain instead of three 40-line copies that drift |
| **The spec contradicts itself on the File Header.** General Note 10's prose says "16 fields, 15 delimiters"; the FH table lists **18 rows** | We follow the **table** (the two extras are trailing "not applicable" hash fields). `TestFileHeaderHasEighteenFields` makes changing that a loud edit, and it is **the first thing to try if the FVU rejects the header** |
| Annexure 6 lists `1045`/`1046` against flag `Z`, and neither is in Annexure 2 | Its header reads "Code in FN 139" — the matrix is shared across the form family. Recorded, not silently dropped |
| Annexure 4's deductor category is **NOT** `compliance`'s s.402(11) `EntityCategory` | Different vocabularies answering different questions (Annexure 4 splits Govt into Central/State and has "Branch/Division of Company"; the statute has neither). Mapping them is a **decision**, and it belongs to whatever stores the deductor profile |

**Deliberate design points, so nobody "fixes" them:**

- **The archtest policy for `form140` is `{}`** — the strictest in the map, with no `platform/db`
  edge. A return file that queries is one whose output depends on when it ran, and its golden
  test would need a database to assert a byte sequence transcribed from a spec.
- **General Note 10 is structural, not checked.** A record is a fixed ordered table; values are
  set BY FIELD NUMBER; serialisation joins the whole slice. So delimiters = fields−1 on every
  branch that will ever exist. **Never add an `append`-style writer here.**
- **Every total is computed, never accepted** — batch total, challan deposit total, the two
  annexure totals, all record counts. A supplied total that disagrees with its rows is how a
  register reconciles to nothing.
- `row.render()` checks unconditional mandatory fields; `build.go` owns the **conditional**
  ones. `TestTheConditionalFieldsAreTheDocumentedSet` fails if a `cond` field is added without a
  rule — a `cond` field with no rule is **a mandatory field nothing checks**.

**Both refusals were mutation-proved:** widening `Y` to 1008/1009 fails 2 tests; dropping FH
field 18 fails 4.

### 1.1 T6 — the Form 140 / 143 return file (**superseded by §1.1′ — kept for the spec detail**)

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

> ### ⛔ AND THIS IS WHERE §4.1 BITES YOU FOUR TIMES
>
> Those four keys become **screen actions**, and every one of them needs an entry in
> **`AppShell.tsx`'s `SCREEN_ACTIONS` map** or it will be **silently dead** — no error, no status
> line, nothing. That map is a registration point that `HANDOFF-FEATURE-LOCK.md` §5.2 does not
> list, and `Alt+O` shipped broken through it on 2026-08-03 with 459 tests green.
>
> **Read §4.1 before you write the first one.** Then add all four to the map, and add a routing
> test per key in `AppShell.test.tsx` — the pattern is already there.

### 1.3′ ✅ DONE 2026-08-04 — both migrations shipped, and the return sources from the books

**Migrations `0069` (`tds_challan` + `tds_challan_allocation`) and `0070` (`company_tds_profile`)
are applied**, and `reporting.Form140StatementFor` / `Form140File` assemble a real return from
real vouchers. **10 new integration tests**, all posting through `internal/ledger` and recording
real challans; three safety mechanisms mutation-proved.

**The two mechanisms that carry the risk:**

1. **`tds_challan_allocation` has NO foreign key to `tds_deduction`, deliberately.**
   `ledger/tds.go` does `DELETE FROM tds_deduction WHERE company_id=$1 AND voucher_id=$2` and
   re-derives on every ALTER. So `ON DELETE CASCADE` would **silently destroy the record of a
   payment really made to the Government**, and `RESTRICT` would make a voucher unalterable
   once its tax was deposited. Instead it keys on `(voucher_id, section, leg_seq)` — the
   identity that survives — and **records what was allocated**, so an alter that moves the
   amount makes it `ErrAllocationBrokenByAlter` and **visible**. This is 0045's mechanism, and
   `TestAnAlteredVoucherBreaksItsChallanAllocation` asserts both halves: the allocation
   survives, and the return then refuses.
2. **A nil challan is NEVER stored.** It is a return artefact — derivable from the zero-tax
   deductions, dated by the format to the quarter end, corresponding to no event. `tds_challan`
   has `CHECK (tax+interest+fee+penalty > 0)` so one cannot be inserted, and the emitter
   synthesises it. The test asserts `tds_challan` is empty after a sub-threshold-only quarter:
   the first report to `SUM` that table would otherwise count a fiction.

**A seam worth knowing about, found by a failing test:** `tds_deduction.rate_pct` stores the
rate **that was tested** (10% on a sub-threshold professional fee) because a deduction must be
re-checkable against the law as it then stood. **The return must write `0.0000`** — DD field 28,
"if 'Total tax collected' is 0.00 then rate should be 0.0000". Reporting the stored rate would
assert a deduction that did not happen. The zeroing is in `deducteeFor` and mutation-proved.

**Four refusals, each naming what the operator must do:** `ErrNoDeductorProfile` ·
`ErrTaxNotDeposited` (there is no "file it unpaid" option — DD field 5 is a challan reference) ·
`ErrAllocationBrokenByAlter` · `form140.ErrNoValidReasonFlag`.

**Only two Annexure 6 flags are derived, and that is a disclosed limit.** `Y` (below threshold)
and `C` (higher no-PAN rate) come from stored facts. **The other eleven all rest on a DOCUMENT
nobody records** — a s.395 certificate, a s.393(6) declaration, a Board circular, a
transporter's declaration. Deriving one would be asserting a document exists. **That is the next
schema gap**, and it is what blocks sub-threshold RENT (§1.1′) from ever being filable.

### 1.3″ — the original gap analysis, kept because it is the migration spec

Both gaps were fully specified by `form140.Statement` — read that type alongside 0069/0070.

1. **`tds_challan` does not exist.** `form140.Challan` is the shape: nil indicator, the four-way
   split (tax / interest / fee / penalty, **whole rupees — the spec forbids paise in these**),
   BSR code, challan number, book-entry flag, deposit date, Annexure 7 minor head, and the
   deductions it covers. **The link is not optional**: DD field 5 is a challan reference.
2. **No deductor return profile.** `company` has `gstin`, `address`, `city`, `pincode`,
   `country` — and **no TAN, no PAN, no Annexure 4 category, no e-mail, no contact number, and
   nothing about the person responsible for deduction** (name, designation, address, state, pin,
   e-mail, contact, and their *own* PAN). That is BH fields 13, 15, 28–30, 32–45 and 59. **The
   TAN is not the GSTIN and is not derivable from it.**

Two more things now have somewhere to go, answering the "two identifiers with nowhere to live"
question in §1.1: the **s.395 certificate number** is `Deductee.CertificateNo395` (15 chars,
mandatory iff flag `A`) and the **UIN of Form No. 121** is `Deductee.UINForm121` (26 chars,
optional). Both are DD fields; both belong on `tds_deduction`.

### 1.3 T6 — the challan register (original text)

Still true, still the biggest hole in the feature: **no `tds_challan` table exists, so nothing
tracks what has been DEPOSITED** — and it is the deposit, not the deduction, that stops interest
running under s.396. `t6-model.md` §1.5. Needs no unread document. Note it interacts with 1.1:
the return's `CD` (challan detail) records need exactly this data, so **doing 1.1 first will tell
you the shape 1.3 must store.**

---

### 1.4′ ▶ NEXT — the write path, and it is what makes T6 usable rather than merely correct

The engine derives, the schema stores, the emitter emits, and **nothing in the product can enter
a challan.** Both new tables are seeded by SQL in tests. To close it:

- **Routes + ACL.** A new table needs a `routeAction` mapping or it default-DENIES; a new report
  name must be a **flat literal** matching `authz.KnownReportNames` (§2.4 of
  `PLAN-TALLY-PARITY.md`). Expect an ACL migration — **it takes `0071`.** Note `company_tds_profile`
  carries a named individual's PAN, so the Data Entry level should be **disallowed**, exactly as
  0068 does for the register.
- **Screens.** Challan entry, and the deductor profile under **F11 Company Features**. Both are
  new screen actions — **read §4.1 first, and register every key in `AppShell.tsx`'s
  `SCREEN_ACTIONS`**, plus §4.1b: classify any new report in the F2 period lists.
- **Make the register's `return_file` status COMPANY-AWARE.** It is currently the static
  build-level refusal for every company. The sourcing layer already produces the four specific
  refusals; surfacing them would turn "a challan register is missing" into "voucher P1 deducted
  ₹20,000 from Chitra Consulting and no challan is recorded against it".
- **Then run it (§5)** and drive a real quarter end-to-end. Three of the last two sessions'
  findings came from running the app; none came from reading it.

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

---

## 4. THE TRAPS THIS SESSION PAID FOR. Do not re-derive these.

### 4.1 ⛔⛔ THE THIRTEENTH REGISTRATION POINT — `AppShell.tsx`'s `SCREEN_ACTIONS`

**`Alt+O` shipped completely dead, in a commit whose 459 tests were all green.** It is the single
most important mechanical lesson on this track, because **slice 2b adds four more screen actions**
(§1.2) and will hit it four times.

**What was present and correct — all of it:**

| Registration point | Status when Alt+O was broken |
|---|---|
| the keymap binding, screen-scoped, cited to `RESEARCH.md:361` | ✅ present |
| `ScreenActionKind` in `ScreenActions.tsx` | ✅ present |
| the `navMachine` case (returns `{}` — importing does not navigate) | ✅ present |
| the button-bar item in `buttonBar.ts` | ✅ present |
| the screen's own `useRegisterScreenAction` handler | ✅ present |
| **`SCREEN_ACTIONS` in `AppShell.tsx`** | ❌ **MISSING** |

Without that last entry the shell resolves the combo, finds no screen action, and forwards the key
to a machine that deliberately does nothing. **No error. No status line. Nothing.**

**⚠️ WHY EVERY EXISTING TEST MISSED IT — read this before trusting a green suite on a keystroke:**

- **The panel's own 11 tests call the screen-action dispatcher DIRECTLY**, so they bypass the map
  entirely. They proved the panel works; they proved nothing about anything *reaching* it.
- **`buttons.test.ts` asserts the combo RESOLVES** to an action and that it is not an
  `unavailable` placeholder. Both were true.
- **Nothing rendered the real shell and pressed the real key.**

It was found by driving the app in a browser (§5). The fix ships with the two tests that would
have caught it, in `AppShell.test.tsx` — they go through the real `AppShell`, each carries a
control asserting the panel is absent first, and both were mutation-proved by deleting the map
entry. **Copy that pattern for every new screen action.**

> **The general lesson: a test that calls the dispatcher directly is testing the handler, not the
> keystroke. If a key is the feature, the test must press the key on the real shell.**

### 4.1a ⛔⛔ A `cored` FROM A PREVIOUS SESSION WAS STILL ON :8087, SERVING DAY-OLD CODE

**This cost half an hour and nearly produced a false "the fix does not work".** After changing
`reporting`, the browser kept showing the OLD refusal text. Everything looked healthy:
`curl /healthz` returned 200, `docker ps` was fine, and the page loaded.

`make run-cored` had exited immediately with **`listen tcp :8087: bind: address already in
use`** — into a log nobody was reading — while a **16-hour-old `cored` from the previous
session** kept answering. §5.2's advice to launch detached (`nohup … & disown`) is what makes
this possible: a detached process outlives the session that started it.

> **`ps -eo etime` IS A DURATION, NOT A CLOCK TIME.** `16:51:35` read like "started at 16:51"
> and actually meant **"running for 16 h 51 m"**. That misreading is what made the stale process
> look like the one just launched.

**Before trusting anything you see in the browser:**

```bash
lsof -nP -iTCP:8087 -sTCP:LISTEN          # who actually holds the port
tail -3 /tmp/cored.log                    # did YOUR process bind, or die?
ps -eo pid,etime,command | grep cored     # etime is AGE. 16:51:35 = 16 hours.
```

And when in doubt, confirm the running binary has your change in it:

```bash
strings "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -Fn | ...)" | grep 'YOUR NEW STRING'
```

**Kill the old stack before starting a new one.** The failure mode is silent, and it makes
correct code look broken — which is the direction that wastes the most time, because you go
looking for a bug that is not there.

### 4.1b ⛔ THE F2 PERIOD DIALOG TOLD TWO REPORTS THEIR DATES DID NOTHING

Found by driving the app (§5), not by reading it, and **not by any of the 461 tests** — the same
way `Alt+O` was found. Worth reading beside §4.1 because it is the same *class* of defect
arriving through a different mechanism.

`PeriodOverlay` in `FrameOverlays.tsx` classified reports with two hardcoded allowlists and
**defaulted anything in neither to a plausible sentence**: *"…has no date dimension in this
build, so a period is not applied to it."* The **TDS / TCS Register** and the **Stock Summary**
were in neither list, and both read the period and act on it.

On the register the contradiction was on screen **at the same moment**: the page behind the
dialog reads *"Press F2 to set the period. Both dates are needed"*, and the dialog on top of it
said the period would be ignored.

> **THE GENERAL LESSON: a default branch that produces a PLAUSIBLE sentence is invisible to
> tests, because tests assert the sentences they expect and never the one nobody meant to
> render.** §4.1's lesson is "test the keystroke, not the handler"; this one is **"remove the
> default, and make every case an explicit decision"**.

Fixed by making the classification exhaustive — `FULL_PERIOD_REPORTS`, `AS_AT_ONLY_REPORTS`,
`NO_DATE_DIMENSION_REPORTS` — with `periodClassification.test.ts` asserting **every report
registered in `defaultScreens.tsx` is in exactly one**, and deriving the right answer from what
each screen actually reads (`periodFrom` + `periodTo` = a period; `periodTo` alone = a
position). Mutation-proved. **Add a report screen, and you must classify it.**

### 4.2 A screen action fires before its data has loaded, and that is CORRECT

Pressing `Alt+O` before the ledger list resolves answers *"select a bank account first"* rather
than opening a panel with no account to import into. That is the intended refusal, not a bug — but
it means **a routing test must `await` the load** (`await screen.findByText(/Bank Reconciliation —
HDFC Bank/)`) or it silently tests the refusal path instead of the route.

### 4.3 `gofmt -w <dir>` reformats files you did not touch

`gofmt -w internal/reporting` quietly reformatted two `closing_stock*` files unrelated to this
work. They were reverted rather than smuggled into a feature commit. **`gofmt -l` still flags
those two plus `handler_print_bankrecon_test.go`** — pre-existing drift, nothing gates on it. If
you want them clean, do it as its own commit. **Format named files, not directories.**

### 4.4 `mock.ts`'s `addMoney` uses `parseFloat` — on a money path

Pre-existing, and `HANDOFF-FEATURE-LOCK.md` §5.1 forbids exactly this. Not touched this session;
the helpers added to that file use BigInt ten-thousandths instead. Worth a separate fix.

---

## 5. RUN IT — the exact recipe, because rediscovering it costs half an hour

**Do this before touching code on any of §1.** Three of this session's findings came from running
the app, and none of them from reading it.

### 5.1 The four processes

Postgres runs in Docker and is usually already up. The rest are foreground processes.

```bash
# 0. Postgres on :5433 — check first, it is normally already running
docker ps --format '{{.Names}}\t{{.Status}}' | grep tally_pg   # or: make dev-db

# 1. cored (Go API) on :8087
cd backend && PORT=8087 AUTH_DEV_TOKEN_ENABLED=true \
  APP_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" \
  ADMIN_DSN="postgres://postgres:postgres@localhost:5433/tally?sslmode=disable" \
  go run ./cmd/cored serve
# or simply: make run-cored

# 2. insightd (FastAPI) on :8088
cd insightd && INSIGHTD_DATABASE_URL="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" \
  .venv/bin/python -m uvicorn insightd.api.app:app --port 8088
# or simply: make run-insightd

# 3. the frontend on :5173
cd frontend && pnpm dev
```

### 5.2 ⚠️ THE FOUR THINGS THAT WILL WASTE YOUR TIME IF YOU GUESS

| Gotcha | What happens if you miss it |
|---|---|
| **`PORT=8087`** | cored's own default is **8080** (`backend/cmd/cored/main.go`, `defaultPort`). The frontend and the seed script both expect 8087, so on the default port everything fails and nothing says why. `make run-cored` sets it from `CORED_PORT ?= 8087` — **prefer the make targets over hand-rolled commands for exactly this reason** |
| **`AUTH_DEV_TOKEN_ENABLED=true`** | cored **refuses to start** rather than run fail-open: *"no authentication configured … Refusing to start fail-open"*. This is correct behaviour, not a bug |
| **`INSIGHTD_DATABASE_URL`** | insightd silently falls back to an **in-memory** draft store and every Bill Inbox draft dies with the process — the inbox goes empty on restart with nothing to explain why |
| **Backgrounding via the agent's task runner** | Harness-tracked background tasks **get reaped**, and the stack dies mid-session. Launch detached instead: `(nohup <cmd> > log 2>&1 & disown)`. That pattern survived a whole session; the tracked one did not |

### 5.3 Seed and open

```bash
cd frontend && pnpm seed:demo -- --insightd     # needs cored + insightd already up
pnpm verify:demo "$(cat .demo-company-id)"      # 33/33 expected
open "http://localhost:5173/?company=$(cat .demo-company-id)"
```

**Sign in:** user `browser-dev-owner`, password `tally-demo-2026`.

The password is **`DEMO_PASSWORD`-overridable** (`frontend/scripts/auth.mts`, defaulting to
`tally-demo-2026`; the verifiers and the browser e2e read the same env var so they stay in
agreement). ⚠️ **If the account already carries a DIFFERENT password the seed answers 401 and says
so loudly — and there is NO admin reset, by design.** Clear it by hand:

```sql
DELETE FROM user_credential
 WHERE user_id = (SELECT id FROM app_user WHERE subject = 'browser-dev-owner');
```

The seeded company is **Demo Traders**: Trial Balance nets to zero (Dr = Cr = 774,970), Balance
Sheet balances, one bank ledger (**HDFC Bank**, a/c `50200012345678`, IFSC `HDFC0004321`) with
four imported statement rows (₹1,40,000 in / ₹25,590 out).

### 5.4 Driving it — it is a KEYSTROKE app, clicks are not enough

A click on a Gateway list item **selects without opening**; you need `Enter` after it. To reach
the work in §1.2:

```
Gateway → (click or arrow to) "Bank Reconciliation" → Enter → Alt+O
```

`Alt+O` should open **"Import Bank Statement — HDFC Bank"** with the account number and IFSC
under the heading, and the status line should read *"Import a bank statement (CSV)."* **If the key
does nothing, you have hit §4.1.**

Playwright MCP works against this app. Two notes: `Alt+g` etc. deliver `key: "g"` through
Playwright (no macOS dead-key transformation, so combos behave), and screenshots must be written
**inside the repo** — `.playwright-mcp/` is an allowed root, `/tmp` is not.

### 5.5 Known-harmless noise

A `404` for `/favicon.ico` in the browser console. That is the only console error on a clean load;
do not go hunting for it.

### 5.6 ⚠️ CAPTURE THIS AS A PROJECT SKILL

There is **no `.claude/skills/` directory** in this repo, so every session rediscovers §5.1–5.4.
The `/run` skill explicitly says a project skill is the repo's verified path and to prefer it.
**Run `/run-skill-generator` and commit the result** — it turns the above into something a session
follows verbatim instead of re-deriving, and it is maybe ten minutes of work that pays back on
every future session.
