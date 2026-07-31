# T6 — TDS / TCS persistence, register and returns: THE DATA MODEL

> **This memo is the §3.2 gate on T6. No implementation agent may be launched until it is
> adversarially reviewed and accepted.** It exists because T1 took three waves for one
> reason: the engine was right and the schema was missing. Everything below was verified
> against the live database with `\d` on 2026-07-30, not from memory and not from a model
> summary.
>
> **STATUTORY SOURCING — UPGRADED 2026-07-30.** An earlier draft of this memo could cite
> only secondary sources. **The primary text has since been obtained and read**: the
> **Income-tax Act, 2025 (Act 30 of 2025)**, as published in *The Gazette of India
> Extraordinary*, downloaded from ICAI's mirror
> (`resource.cdn.icai.org/87647dtc-aps2139-inceome-tax-act-2025.pdf`, 6.4 MB) because
> `incometaxindia.gov.in` hard-blocks automated fetches (403 on every path tried, with and
> without a browser user-agent). **Section 393's Table, Section 394's Table, the no-PAN
> provision and Section 516 were read verbatim from that text** and are quoted below.
> **§9 is the verified rate table.** What remains unread is the **Form 140 layout and the
> FVU File Format** — see §1 and §6.5.
>
> **This changes T6's risk profile.** The engine's six TDS rows and two TCS rows are now
> **CONFIRMED correct against the statute**. The remaining risk is no longer "are the rates
> wrong" but **"which obligations does the engine not model at all"** — and §9.2 lists five.
>
> Migration numbers reserved: **0049 (schema), 0050 (ACL)** — per `PLAN-TALLY-PARITY.md`
> §2.5, the authoritative table.
>
> **A hazard found while writing this and fixed in the same commit:** all five per-track
> `**Migrations:**` lines in that plan were stale by one — they were never updated when T1
> consumed 0037–0039 and shifted every unstarted block. The T2 line said **0039**, which T1
> has already used, so a T2 build following the prose would have written a migration on top
> of `0039_party_address`. All five now agree with §2.5. **This is the §3 failure mode in
> miniature: the number was decided in one place and read from another.**

---

## 0. WHAT ALREADY EXISTS — measured, not assumed

`HANDOFF-T1-PRINTING.md` §3.5 sketched this from `tds.go`'s own comment. The measurement
differs from the sketch in three ways that change the design, so start here.

| Thing | Status, measured 2026-07-30 |
|---|---|
| `compliance.TDSDerive` | Pure engine, 6 sections, s.206AA floor. Corpus-gated (13 fixtures). |
| `compliance.TCSDerive` | Pure engine, s.206CC (twice-the-rate-or-5%, **not** 206AA's flat 20%). 5 fixtures. |
| `ledger.deriveAndVerifyTDS` | Derives and **verifies** a posted TDS leg; stricter than GST — asserts the **credit side**, not just the magnitude. |
| **The `tds` request block** | **LIVE over HTTP.** ⚠️ see §0.1 |
| **TCS on the post path** | **NOT wired.** Engine only. |
| **Salary TDS (s.192)** | `payroll/incometax.go` — annualised base, s.192(2)/Form 12B prior-employer credit, YTD credit, round-to-nearest-rupee. **`payslip.tds` PERSISTS it.** |
| **Any TDS/TCS/challan/deduction table** | **NONE.** Confirmed by a full `information_schema` sweep. |
| `ledger.pan`, `employee.pan` | Both exist. Deductee PAN is available for both Form 140 and Form 138. |
| `gst_inward_document.section` | A **GST** field. Not TDS. Do not reuse it. |
| `gst_return_filing` | The **precedent** for a return-filing table: `(company_id, form, return_period)` unique, FORCE RLS, `filed_at`/`filed_by`. Follow its shape. |

### 0.1 `voucher_tds.go`'s doc comment is now FALSE — fix it in the first slice

It says the `tds` block is "DELIBERATELY DEAD CODE AT COMMIT TIME … unreachable until the
integrator applies the two-hunk wiring diff". **That diff landed.** `server.go:1302` carries
`TDS *tdsReq`, `server.go:1725` calls `buildTDS`. Proven live at `005edb7` against the demo
company — a Journal with a `tds` block returned:

```json
"tds": {"applied": true, "basis": "ANNUAL", "rate_pct": "10.0000",
        "tds": "10000.0000", "threshold": "50000.0000"}
```

**Then the hashed payload of that very voucher was read back, and it contained:**
`op, class, party, postings[], narration, version_no, voucher_id, voucher_no,
voucher_date, voucher_type_id`. **No section. No deductee type. No PAN flag. No rate. No
threshold. No derived amount.**

So the engine derived ₹10,000, verified the leg against it, reported it to the caller —
and then discarded every fact needed to file the return. What survives in the books is an
unexplained ₹10,000 credit to a TDS ledger. *That* is T6's problem statement, and it is
now a measurement rather than a claim. (The probe voucher was deleted; the demo trial
balance is back to its pinned `774970.0000`.)

### 0.2 THE CORPUS GATE DOES NOT VERIFY THE RATES — it verifies the arithmetic

This must be understood before anyone cites "corpus-gated" as assurance for T6.

`tds.go`'s own header says the rate table "was curated OFFLINE from model knowledge with a
training cutoff of January 2026. It was NOT read out of a statutory text, a gazette, or a
live source". `corpora/validate.py:262-267` then **hardcodes the same six rows**
(`194C: 1/2/30000`, `194J-P: 10/10/50000`, …).

`validate.py` is a genuine second implementation of the **formula** — it would catch a typo
in `TDSDerive`'s threshold comparison or its rounding. It is **not** an independent oracle
of the **rates**: both sides carry identical numbers from the same unverified source, so a
wrong rate produces two agreeing implementations and a green gate.

**Consequence for T6:** persisting a derived amount makes a wrong rate *durable* and puts it
on a filed return. **That re-verification has now been done — see §9.** All eight rules
match the Gazette text. The structural point stands regardless and must be fixed:
**`validate.py` must not re-declare the rate table.** It should assert the *arithmetic*
against fixtures whose rates carry a **citation to a section and serial number**, so that a
future rate change breaks the citation rather than silently agreeing with itself.

---

## 1. THE OUTPUT INVENTORY

Six artefacts. Enumerated per §3.2 from the form where possible.

> **SOURCING DISCLOSURE, and it is load-bearing.** The field lists below were corroborated
> from *secondary* sources reachable from this machine (see §6). The **primary** sources are
> the notified form (Income-tax Rules, Appendix II) and the **Protean/TIN FVU File Format**
> for the quarterly e-TDS statement, which fixes column order, widths and code lists.
> **Neither was read.** Every field below is therefore `CORROBORATED`, not `VERIFIED`, and
> §4's invariant I-7 refuses to emit a file until a `fileFormatVersion` is pinned to a read
> primary. **The FVU cannot run here — never claim a validated return.**

### 1.1 Form 140 (formerly Form 26Q) — quarterly return, NON-SALARY deductions

**Deductor block (once per return):** TAN · PAN of deductor · name · address (flat/premises,
road, area, town, state, pincode, e-mail, telephone) · deductor category (Company /
Non-company / Government) · financial year · assessment year · **quarter** · whether a
revised/correction statement, with the original provisional receipt number · responsible
person's name, designation and address.

**Challan block (one row per deposit):** **BSR code of the collecting branch (7 digits)** ·
**date of deposit** · **challan serial number (5 digits)** · total amount deposited, split
into **TDS · surcharge · education cess · interest · fee (s.234E) · penalty/other** ·
**"paid by book entry" flag** (Government deductors pay by transfer voucher, not challan —
the flag changes which identifiers are mandatory) · section code under which deposited.

**Annexure — deductee-wise breakup (one row per deduction):** deductee reference/serial ·
**deductee code (01 Company / 02 Non-company)** · **PAN of the deductee** · name · **section
code / nature of payment** · **date of payment or credit** · **amount paid or credited** ·
**date of deduction** · **TDS · surcharge · cess** · **rate at which deducted** · **date of
deposit** · **reason for non-deduction / lower deduction / higher deduction code** (e.g. a
s.197 certificate, a s.206AA no-PAN case, a 15G/15H declaration) · **certificate number**
where a lower-deduction certificate applies.

### 1.2 Form 143 (formerly Form 27EQ) — quarterly TCS return
Same three blocks, collection-side; deductee → **collectee**, and the no-PAN rule is
**s.397(2)(b)(ii)** (successor to s.206CC), not the TDS floor — and it carries a **20% ceiling**
the engine does not model (§9.2.3). **Do not build this by copying the TDS mapper.**

### 1.3 Form 131 (reportedly, formerly Form 16A) — the deductee's certificate
Per deductee per quarter: deductor TAN/PAN/name/address, deductee PAN/name/address,
certificate number, period, and a per-challan table of amount paid, tax deducted, tax
deposited, BSR code, deposit date, challan serial. **This is the first output that needs the
deductee's ADDRESS** — see §2, verdict MIGRATION.

### 1.4 The TDS/TCS register (screen + print)
Per deduction: date, voucher number, party, section, amount paid, rate, TDS, challan
reference, deposit status. Renders through T1's generic `report.go`. **Needs an explicit
`disallow display report:` and `disallow print report:` at specificity 2** (§3.3,
Authorization): it carries **PAN per deductee** and Data Entry's seeded coarse
`allow display report` matches every `report:NAME` at specificity 1.

### 1.5 The challan register
Per challan: BSR code, deposit date, serial, section, amounts, and **the deductions it
covers** — with the unallocated balance visible. This is the object T1's §3.5 note called
out and it is the piece a register-first build discovers at review.

### 1.6 The FVU-format `.txt` statement
**Deferred in this memo and stated as deferred**, because its column order and code lists
come from the File Format document (unread) and the FVU cannot validate it here. Ship
1.1–1.5 first; emit the file only behind a pinned `fileFormatVersion` and a
`WriteDisclosureSidecar` (T5's precedent, `PLAN-TALLY-PARITY.md:374`).

---

## 2. THE PROVENANCE TABLE

**Every "stored where" below was checked with `\d` against the live database.** This is the
section that would have prevented all four of T1's blockers.

### 2.1 Deduction-level facts (Form 140 annexure)

| Field | Required by | Stored where | Verdict |
|---|---|---|---|
| Deductee PAN | Form 140 annexure | `ledger.pan` ✓ | **OK** |
| Deductee name | Form 140 annexure | `ledger.name` ✓ | **OK** |
| Amount paid/credited | Form 140 annexure | `posting.amount` ✓ | **OK** — but which leg is the *taxable base* is not recorded; see below |
| Section / nature of payment | Form 140 annexure | ***nowhere*** — `gst_inward_document.section` is a GST field | **MIGRATION** |
| Deductee code (01/02) | Form 140 annexure | ***nowhere*** (`compliance.DeducteeIndHUF/Other` is a request-time input, discarded) | **MIGRATION** |
| Rate actually applied | Form 140 annexure | ***nowhere*** (returned on the HTTP response, then dropped) | **MIGRATION** |
| TDS amount deducted | Form 140 annexure | ***nowhere*** — only an unlabelled credit posting | **MIGRATION** |
| Taxable base the rate applied to | Form 140 annexure | ***nowhere*** — `TDSParams.TaxableAmount` is request-time only | **MIGRATION** |
| Date of payment/credit | Form 140 annexure | `voucher_version.voucher_date` ✓ | **OK** |
| **Date of deduction** | Form 140 annexure | ***nowhere*** — **not the same date** as payment/credit when tax is deducted on credit and paid later | **MIGRATION** |
| Was a PAN on record **at deduction time** | s.206AA defence | ***nowhere*** — `PANAvailable` is a request input, discarded; `ledger.pan` is *current*, and a PAN added later would retroactively make a correct 20% deduction look wrong | **MIGRATION — store the flag, do not re-derive it** |
| Lower-deduction certificate no. (s.395) | Form 140 annexure | ***nowhere*** | **MIGRATION** |
| Reason for non/lower deduction | Form 140 annexure | ***nowhere*** | **MIGRATION** |
| Surcharge, cess | Form 140 annexure | ***nowhere*** — the engine models neither | **MIGRATION + ENGINE GAP** (see §6.3) |
| Ruleset version used | audit / reproducibility | ***nowhere*** | **MIGRATION** — without it a rate correction silently rewrites history |

### 2.2 Challan-level facts

| Field | Required by | Stored where | Verdict |
|---|---|---|---|
| BSR code (7 digits) | Form 140 challan block | ***nowhere*** | **MIGRATION** |
| Challan serial number | Form 140 challan block | ***nowhere*** | **MIGRATION** |
| Date of deposit | Form 140 challan block | ***nowhere*** | **MIGRATION** |
| Amount deposited, split TDS/surcharge/cess/interest/fee/penalty | Form 140 challan block | ***nowhere*** | **MIGRATION** |
| Paid-by-book-entry flag | Form 140 challan block | ***nowhere*** | **MIGRATION** |
| Which deductions a challan covers | Form 140 (annexure ↔ challan link) | ***nowhere*** | **MIGRATION — a link table, and §4's I-2 lives on it** |

### 2.3 Deductor and deductee identity

| Field | Required by | Stored where | Verdict |
|---|---|---|---|
| Deductor **TAN** | Form 140 deductor block | ***nowhere*** — `company` has `gstin`, no TAN | **MIGRATION. A return cannot be filed without it, and it is not the GSTIN.** |
| Deductor **PAN** | Form 140 deductor block | ***nowhere*** — checked: `company` has no `pan` column (it has `gstin` only; `ledger.pan` and `employee.pan` are the *other* party's) | **MIGRATION** |
| Deductor name, address | Form 140 deductor block | `company.name`, `company.address/city/pincode` ✓ (0015) | **OK** |
| Deductor category (Company/Non-company/Govt) | Form 140 deductor block | ***nowhere*** | **MIGRATION** |
| Responsible person name/designation | Form 140 deductor block | ***nowhere*** | **MIGRATION** |
| **Deductee address** | **Form 131 (certificate)** | `ledger.address/city/pincode` ✓ **(0039, and readable over HTTP as of `b76a7e1`)** | **OK — T1 paid for this already** |

### 2.4 Salary TDS (Form 24Q) — closer than 26Q, and NOT in this track

`payslip.tds` persists a derived salary deduction and `employee.pan` exists, so 24Q has a
partial model where 26Q has none. **24Q is explicitly OUT OF SCOPE for T6** — it needs
Annexure II (the full salary breakup, chapter VI-A deductions, regime election) which is a
track of its own. Stated here so nobody assumes T6 delivered it. **The challan table T6
builds is shared infrastructure 24Q will reuse — design it without a 26Q-only assumption.**

### 2.5 The verdict, in one line

**Twenty-one fields are MIGRATION. Nothing about a deduction is stored today.** T6 is
overwhelmingly a schema track with a reporting layer on top, and the engines — the part that
looks like the hard bit — are already done and are not where the risk is.

---

## 3. UNITS AND SCALE, AT EVERY SEAM

Per §3.2(3): the unit lives in the **identifier**, not in a comment.

| Value | Type / scale | Naming rule |
|---|---|---|
| Money in the ledger | `NUMERIC(19,4)`, `decimal.Decimal` | plain `Amount` **only** where 4-dp rupees is the invariant; never `float64` |
| Rate | **percent, not fraction** | **`RatePct`** — `TDSResult.RatePct` is already correct. A field named `Rate` holding `10` is exactly T1's `Discount`/`DiscountPct` bug. |
| Threshold | 4-dp rupees | `ThresholdRupees4dp` at any new package boundary |
| **TDS deposited** | **WHOLE RUPEES** | **`TDSDepositedRupees`** — this is the dangerous seam. §3.4 |
| Quarter | `Q1..Q4` **of the financial year** | `FYQuarter` — never a calendar quarter. Q1 = Apr–Jun. |
| Return period | 6-char, like `gst_return_filing.return_period` | reuse that shape and its `CHECK` |
| BSR code | **7-char string with leading zeros** | `BSRCode string` — **never an integer.** A numeric type eats the leading zero and the return is rejected. Same for the 5-digit challan serial. |

### 3.1 The rounding seam — say it once, enforce it in both directions

The engine computes `round_half_up(amount × rate ÷ 100, 4dp)` (`tds.go:245`). The books
store 4 dp. **Tax is deposited in whole rupees.** So there are three representations and two
conversions, and the memo must fix both *before* code:

1. **derived (4 dp)** → what the voucher's TDS leg carries. Already pinned by
   `deriveAndVerifyTDS`, which refuses a leg that differs by a paisa.
2. **deposited (whole rupees)** → what goes on the challan and the return.

**✅ DECIDED — nearest RUPEE, per deduction; the challan is the SUM of the rounded rows.**

The statutory rounding rule is **s.516** of the Income-tax Act 2025 (successor to s.288A/
288B), read verbatim:

> "The amount of total income computed or any amount payable or refundable under this Act,
> shall be **rounded off to the nearest multiple of ₹10** ignoring any part of a rupee
> consisting of paise and thereafter if such amount is not a multiple of ten, then—
> (a) such amount shall be increased to the next higher amount which is a multiple of ten,
> if the last figure in that amount is five or more; or (b) … reduced to the next lower
> amount … if the last figure is less than five"

**s.516 does NOT govern the per-deduction figure**, and applying it there would be wrong. It
speaks to "total income computed" and "amount payable or refundable" — the assessee's
liability on assessment. A deduction is neither. Applying ₹10 rounding per deductee row
would also make the annexure fail to tie to the challan in ₹10 steps, which is the one thing
the return cannot tolerate.

**So:**
1. **Store the derived amount at full 4 dp.** Never lose it; `deriveAndVerifyTDS` already
   refuses a posted leg that differs by a paisa, and that check must keep working.
2. **Round to the nearest rupee at the deduction boundary** — the figure actually withheld
   and reported on the annexure row. This matches settled practice (individual TDS amounts
   are carried in whole rupees to avoid portal mismatches) **and** the precedent already in
   this codebase: payroll's `_round_rupee` per payslip (`validate.py:2514`).
3. **The challan amount is the SUM of the rounded rows**, never an independently rounded
   total. This makes I-2 an exact equality rather than a tolerance, which is the only form
   of that invariant worth having.

**Record s.516 in the catalogue anyway** — it is the rule for anything that ever computes an
*assessed liability*, and a future reader will otherwise re-litigate this.

**Do not re-derive a stored figure** (§3.3, Money): once a deduction is persisted, the
register and the return read the stored amount. A report that recomputes from
`rate × base` will disagree with the books the first time a rate is corrected.

---

## 4. INVARIANTS, EACH WITH THE TEST THAT PINS IT

"By the form" is not enforcement. Each of these is a `CHECK`, a constraint trigger, or a
test that fails from **both** sides.

| # | Invariant | Enforcement |
|---|---|---|
| **I-1** | A deduction row's `tds_amount` equals `round(taxable_base × rate_pct ÷ 100, 4)` for its stored `ruleset_version` | Constraint trigger + a Go test that re-derives via `TDSDerive`; **mutation: change the stored rate by 0.01 and the trigger must fire** |
| **I-2** | **Σ(deductions allocated to a challan) = the challan's TDS amount, TO THE PAISA** | **DEFERRED constraint trigger on BOTH tables** — deferral makes write order irrelevant *within* a transaction (0027's item-invoice tie is the precedent). Not a form check. |
| **I-3** | A challan is never over-allocated; the unallocated balance is ≥ 0 | same trigger, tested from both sides |
| **I-4** | Every deduction references a **section in the ruleset** | FK to a section catalogue table, **not** a free-text column — `ErrUnknownSection` must be unreachable from stored data |
| **I-5** | A deduction with **no PAN** carries either the s.206AA higher rate **or** an explicit exemption reason. **A blank PAN is a rejected return, not a partial one.** | `CHECK` + a test asserting the return **refuses** to emit |
| **I-6** | A **deleted or cancelled** voucher contributes to no register, no challan allocation and no return | Test named for the breach. §3.3 calls this out and **the printed Day Book already got the sibling case wrong.** |
| **I-7** | No FVU file is emitted without a pinned `fileFormatVersion` traced to a **read primary source** | Refusal test — the `gststate` habit: refuse rather than guess |
| **I-8** | A filed period is **immutable**: a deduction cannot be added to, altered in, or removed from a period with a `tds_return_filing` row | Follow `gst_return_filing`; test the back-dated write |
| **I-9** | The register reads the **CURRENT voucher version only** | `voucher_version` is immutable and hash-chained; an altered voucher must contribute **once**. Test: alter a TDS voucher, assert the register total is unchanged. |
| **I-10** | Every new table: RLS `ENABLE` **and** `FORCE`, policy with `USING` **and** `WITH CHECK`, composite `(company_id, id)` FKs, cross-tenant refusal proven **by the FK** | §3.3 Tenancy; add rows to `buildFixture`. **Also add the new tables to `internal/security/isolation_test.go`'s `tenantTables`** — measured: it is a hardcoded **7-table** list (`company, account_group, ledger, voucher_type, voucher_version, posting, ledger_balance`) while **41 tables carry `company_id`**. It is not a near-miss, it covers 17%. Five more unchecked tables is the wrong direction; make it catalogue-driven (open item §7.2). |

### 4.1 The hash-chain rule — get this wrong once and it is unfixable

The per-deduction facts (§2.1) **must live inside the hashed canonical payload**, or an
alter silently drops them (§3.3). `hash.go:110-164` states the three rules and they are not
stylistic:

* the new field is **LAST**, after `Inward` — `encoding/json` emits keys in
  struct-declaration order, so any other position changes the bytes of **every voucher ever
  written**;
* the **outer pointer is `omitempty`** — every voucher without TDS must marshal
  byte-identically to what it marshalled to before the field existed, or `VerifyChain`
  reports every book in the field as tampered with, which is indistinguishable from an
  actual attack;
* the **inner fields are plain strings with NO `omitempty`** — once present the shape is
  fixed, so the same facts cannot hash two ways depending on their history.

**And note what `inward_doc_test.go:83` learned the hard way:** only a pinned vector that
sets **both** `Inventory` and `Inward` can observe the ordering at all — the existing
vectors set neither, so swapping the declaration order leaves them byte-identical and
`TestExistingVoucherHashesAreUnchanged` **passes**. T6's vector must set **all three** or
the ordering rule is unpinned and the test is vacuous.

---

## 5. THE EDGE-CASE LEDGER

Worked against §3.3. An entry marked N/A says why.

**Money and arithmetic**
- **Zero deduction below threshold.** `Applied=false`. Decision: is a below-threshold
  payment a **row** in the register? **Yes** — 26Q reports amounts paid on which tax was
  *not* deducted, with a reason code. A register that only lists deductions cannot produce
  that. *Test: `TestBelowThresholdPaymentAppearsWithAReasonCode`.*
- **Exactly at the threshold.** Statute says "does not exceed" → **no deduction**. Already
  pinned (`194c-at-threshold-exact.json`); persistence must not re-introduce an off-by-one.
- **Negative / reversal.** A credit note against a payment already deducted and **already
  deposited**. The deposit cannot be un-made. Behaviour: an adjustment row, never a mutation
  of the deposited figure. *Test: `TestAReversalNeverAltersADepositedChallan`.*
- **0.01.** Smallest deduction; whole-rupee deposit rounds it to 0 or 1 — I-2 must still tie.
- **Crores.** `NUMERIC(19,4)` is ample; the FVU field width is **not** — deferred with §1.6.
- **A value that does not reconcile:** refuse, or print as given and make the discrepancy
  visible. **Never silently adjust.**

**Time**
- **FY boundary.** A payment credited 31-Mar and deducted 02-Apr falls in different years for
  "date of credit" and "date of deduction" — §2.1 records both **because of this case**.
- **Quarter boundary.** Q1 = Apr–Jun (financial, never calendar).
- **Back-dated entry into a filed quarter.** Refused by I-8.
- **Deposit after the due date** → interest u/s 201(1A). **v1 does not compute it; the
  challan carries an `interest` amount as an INPUT.** Stated, not silently absent.
- **Effective-from on rates:** the ruleset is `tds-2026-04` and there is **exactly one**. A
  voucher dated before it has no applicable ruleset — the engine must **refuse**, not
  fall back to the nearest. *Test: `TestAVoucherBeforeTheEarliestRulesetIsRefused`.*

**Voucher lifecycle**
- Alter → new version; register reads current only (I-9).
- Cancelled/deleted → contributes nothing (I-6).
- New data inside the hashed payload (§4.1).

**Tenancy** — I-10, every new table, plus the `tenantTables` fix.

**Authorization**
- `routeAction` mapping for **every** new route; unmapped `{companyID}` sub-paths
  default-DENY (and now do so for service principals too, verified live at `005edb7`).
- **Explicit `disallow` at specificity 2** for the register and the challan register, both
  `display` and `print` — they carry PAN per deductee.
- `authz.Export` exists in the vocabulary (`authz.go:43`, `KnownActions`) and has
  **zero `routeAction` mappings** — verified by grep, so any export route is default-DENY
  until mapped. A "download the return" route needs an ACL **decision**, not an accident.
- Is any resource token derived from the path spoofable? T1's answer — re-read the stored
  record and refuse a mismatch with **404, not 403** — applies to a challan id too.

**Absent and partial data**
- **No PAN** → I-5. **No TAN** → the return cannot be produced at all; refuse at the report
  boundary naming the field (T1's Rule 46 refusal is the model, and its diagnostic JSON body
  is why the demo failure was a one-line fix rather than a debugging session).
- **Partial allocation is the NORMAL case**: one challan covers many deductions and one
  quarter has many challans. Model it as a **link table with an allocated amount**, not a
  nullable `challan_id` on the deduction row. Deciding this here is the whole point of §3.2.
- A deduction **not yet deposited** is normal mid-quarter — it is a state, not an error.

---

## 6. THE DECISIONS — ALL RESOLVED AGAINST THE PRIMARY TEXT

An earlier draft left these open for review to rule on. **The Act has since been read and
they are now decided.** Each records what was decided, on what authority, and what is still
genuinely unknown. Review's job is to attack the reasoning, not to supply the answer.

### 6.1 ✅ RESOLVED — the `section` column stores a NUMERIC PAYMENT CODE

The Income-tax Act 2025 (in force **1 April 2026**, i.e. from the very first day of tax year
2026-27) consolidates every non-salary TDS provision into **section 393** and every TCS
provision into **section 394**. Salary TDS moves to **section 392**. `s.393(1)` reads:

> "Where any income or sum of the nature specified in **column B of the Table below**, is
> credited or paid or distributed by the person specified in column C during the tax year,
> to a resident, the person responsible for paying such income or sum shall deduct
> income-tax…"

So the old 194-series numbers **do not exist** in the new Act. Payments are identified by
**Table serial number**, and on the return by a **four-digit numeric payment code**.
Form 140's annexure carries a **"Section Code (1004–1067)"** field — numeric codes replacing
section references outright.

**DECISION.** `tds_deduction.payment_code` stores the **four-digit numeric code**, and the
section catalogue maps it to (a) the Act's Table serial number, (b) the legacy 194-series
number for display and for reading historical books, and (c) an **effective-from / effective-to**
window. The internal engine key (`"194C"`, `"194J-P"`) becomes a *legacy alias* on the
catalogue row, **not** the stored value.

**Why not just store `"194C"`:** a return filed with a code family the portal does not
accept is rejected wholesale, and the codes are the only identifier Form 140 has. Storing
the display string as the key is precisely T1's `place_of_supply` bug — the codebase worked
in one representation and the document needed another, and nothing mapped between them.

**STILL UNKNOWN and gated by I-7:** the exact code for each of our six payments. Secondary
sources offer a mapping (1023/1024 contractor, 1027 professional, 1026 technical, 1006
commission, 1008/1009 rent) but **one of those same sources carried pre-Finance-Act-2025
thresholds** (₹30,000 for professional fees, ₹2,40,000 for rent) that the Act text
disproves, so it is not trustworthy on codes either. **The code list must come from the
Form 140 layout or the FVU File Format.** Until it does, the catalogue rows carry the
mapping as `ConfUnverified` and **no file is emitted**.

### 6.2 ✅ RESOLVED — the rates are VERIFIED. See §9.

All six TDS rules and both TCS rules match the Gazette text exactly, including the two the
code itself flagged MEDIUM and asked to be spot-checked first. **§9.1 is the verified
table with the statutory wording.** §9.2 is what the engine does *not* model — which is now
the real risk.

### 6.3 ✅ RESOLVED — surcharge and cess: columns exist, engine emits zero

Unchanged in substance from the earlier draft: for **resident** deductees under s.393 the
Table prescribes a flat rate with no surcharge or cess loading, so zero is correct today.
The columns must still exist, because the return reports them separately and a
non-resident/s.393(2) extension would need them. **The engine states zero; it does not
imply it by omission.**

### 6.4 ✅ RESOLVED — scope

1. **TCS (Form 143, formerly 27EQ) is OUT of T6.** The engine exists but **is not wired to
   the post path at all**, and s.394(2)'s manufacturing declaration (§9.2.4) is a whole
   sub-feature. T6 ships TDS end-to-end; TCS is T6b.
2. **The payment code attaches PER VOUCHER LINE.** Decided by the return's own shape: the
   annexure is one row per *deductee × payment code × challan*, so a single voucher paying
   one party for two natures of payment must produce two annexure rows. A voucher-level or
   ledger-level column cannot express that. **Per-line, and the line is what carries it into
   the hashed payload.**
3. **Salary TDS (Form 138, formerly 24Q) is OUT of T6** — §2.4. It needs the full salary
   annexure. `payslip.tds` already persists the amount, so 24Q is *closer*, but "closer" is
   not "in scope".
4. **Rounding: nearest RUPEE per deduction** — §3.1, now decided on authority.

### 6.5 THE ONE THING STILL GENUINELY UNREAD — and it gates file emission

The **Form 140 layout** and the **FVU File Format** were not obtained.
`incometaxindia.gov.in` returns **403 to every automated request** (both `WebFetch` and
`curl`, with and without a browser user-agent), including the direct document URLs for
`fn-140` and its FAQ. The field enumeration in §1.1 is therefore **corroborated from
secondary sources, not read from the form**, and the payment-code list is unknown (§6.1).

**This does not block T6's schema, engine, register or challan work** — those are settled by
the Act, which *was* read. **It blocks exactly one thing: emitting a return file.** I-7
enforces that. **A human with browser access can close this in ten minutes**, and it is the
single highest-value manual task on this track:

- `https://www.incometaxindia.gov.in/documents/d/guest/fn-140` — the form
- `https://www.incometaxindia.gov.in/documents/d/guest/form-140-faqs` — its FAQ
- Protean/TIN e-TDS **File Format** for the quarterly statement — column order and widths

**The FVU cannot run here. No return produced by T6 has been validated, and no such claim
may be made.**

### 6.6 Sources

**PRIMARY — read verbatim, and the authority for §9:**
- **Income-tax Act, 2025 (Act 30 of 2025)**, *The Gazette of India Extraordinary*, assented
  21 Aug 2025, in force 1 Apr 2026. Sections **392, 393, 394, 397(3)(b), 516**, and the
  definitions of *designated person* and *specified person*. Obtained via
  [ICAI's mirror](https://resource.cdn.icai.org/87647dtc-aps2139-inceome-tax-act-2025.pdf).
  **Caveat: this is the Act as enacted.** The department publishes a text "as amended by
  Finance Act, 2026" which is **403-blocked here**; any Finance Act 2026 amendment to the
  s.393 Table is therefore **unchecked**, and §9's rows should be re-confirmed against it.

**SECONDARY — used only where marked, never for a rate:**
- [ClearTax — Form 140](https://cleartax.in/s/form-140-income-tax) (s.397(3)(b), Rule 219 of
  the Income-tax Rules 2026, the "Section Code (1004–1067)" annexure field, due dates)
- [CAclubindia — Form 140 filing guide](https://www.caclubindia.com/articles/tds-quarterly-return-on-non-salary-2026-know-how-to-file-with-new-form-140-55753.asp) (deductor/challan/annexure blocks)
- [TaxGuru — Income-tax Act 2025 TDS for TY 2026-27](https://taxguru.in/income-tax/income-tax-act-2025-tds-rates-thresholds-compliance-ty-2026-27.html) (old→new section mapping)
- [Terra Insight — s.393 payment codes](https://www.terra-insight.com/resources/section-393-payment-code-reference/) — **cited as UNRELIABLE**: its threshold column
  contradicts the Act text (see §6.1). Recorded so nobody re-uses it.
## 7. THE SHAPE THAT FOLLOWS (for review to attack, not to implement)

Five tables, migration **0049**, all FORCE RLS with composite `(company_id, id)` FKs:

```
tds_section_catalogue   internal key -> effective-dated form code + rate provenance   (§6.1, I-4)
tds_deduction           one row per deduction: base, rate_pct, amount, section,
                        deductee_type, pan_on_record_at_deduction, date_of_deduction,
                        ruleset_version, certificate_no, reason_code                  (§2.1)
tds_challan             BSR code, serial, deposit date, the six-way amount split,
                        book_entry flag                                               (§2.2)
tds_challan_allocation  challan <-> deduction, WITH an allocated amount               (I-2, I-3)
tds_return_filing       mirrors gst_return_filing exactly                             (I-8)
```

Plus: TAN + deductor category + responsible person on `company` (§2.3), and the per-deduction
facts inside the **hashed canonical payload** under §4.1's three rules.

Migration **0050** is the ACL: explicit `disallow display report:` **and**
`disallow print report:` at specificity 2 for both registers, and a decision on
`authz.Export`.

---

## 8. ACCEPTANCE CHECKLIST FOR THE REVIEWER

- [ ] §2's "stored where" re-verified with `\d` independently — **not** taken from this memo
- [ ] **§9.1 spot-checked against the Act text yourself.** Do not take the ✅s on trust —
      that is precisely the habit this memo exists to break. The text is at
      `resource.cdn.icai.org/87647dtc-aps2139-inceome-tax-act-2025.pdf`; s.393's Table
      begins at the heading "FOR PAYMENTS TO RESIDENT".
- [ ] **§9.2's five unmodelled obligations triaged** — which land in T6, which are deferred
      *with a disclosure*. **9.2.5 (who must deduct at all) and 9.2.2 (director's fees, nil
      threshold) are the two that will bite a real small business first.**
- [ ] §6.1's payment-code list obtained from the form/FVU, or I-7 still refuses emission
- [ ] §3.1 rounding accepted: nearest rupee per deduction, challan = sum of rounded rows
- [ ] I-2 is a **deferred constraint trigger**, not a form check
- [ ] §4.1's pinned vector sets **all three** of Inventory, Inward and the new field
- [ ] Every new table in `tenantTables` **and** `buildFixture`
- [ ] No new numeric identifier crosses a package boundary without its unit in the name
- [ ] `validate.py` no longer re-declares the rate table (§0.2)
- [ ] No claim anywhere that a return was FVU-validated
- [ ] §9's rows re-confirmed against the "as amended by Finance Act, 2026" text once
      someone with browser access can reach it (§6.6 caveat)

---

## 9. THE VERIFIED RATE TABLE — read from the Gazette text

**Authority: Income-tax Act, 2025 (Act 30 of 2025), section 393(1) Table "FOR PAYMENTS TO
RESIDENT" and section 394 Table "TAX COLLECTION AT SOURCE". In force 1 April 2026.**
Column D of each table carries the rate and the threshold. Quotations are verbatim.

### 9.0 ⛔ CORRECTION, 2026-07-31 — THIS SECTION READ THE WRONG VERSION OF THE ACT

**§9 below was checked against the Act AS ENACTED** (Act 30 of 2025, via the ICAI mirror).
`docs/statutory/ita-2025-as-amended-fa-2026.pdf` — the Act **as amended by the Finance Act
2026** — has since been read, and `docs/statutory/README.md` had explicitly flagged it as
"now the authority to verify §9 against … because whatever this text says IS current law".

**That re-verification changed one shipped rate.** s.394(1) [Table: Sl. No. 4] "Sale of
scrap" carries footnote 88, *"Substituted for '1%' by the Finance Act, 2026, **w.e.f.
1-4-2026**"* — the rate is **2%**, not 1%. The engine shipped 1% and **under-collected by
half on every scrap sale from 1 April 2026** until it was corrected. It is one of a coherent
block of five FA-2026 TCS rate changes (liquor, tendu leaves, scrap, minerals, LRS).

**So the claim below that "eight of eight shipped rules are right" was true of the enacted
text and false of current law: it was seven of eight.** The six TDS rates were re-checked
against the amended text and carry no FA-2026 footnote — those verdicts stand unchanged, as
does `206C(1F)-MV`.

Corrections applied to the table below in place. Full working:
**`docs/design/t6-gaps-model.md` §0.**

> **The transferable lesson:** "verified against the Act" is not a durable claim — an Act is
> amended annually. A verification is only as current as the *edition* it read, so record the
> edition, and re-read on every Finance Act.

### 9.1 Every engine rule, checked against the statute

| Engine key | Act reference | Rate — statutory wording | Threshold — statutory wording | Code | Verdict |
|---|---|---|---|---|---|
| `194C` | s.393(1) **Sl. 6(i)** | "(a) 1%, if contractor is individual or Hindu undivided family; (b) 2%, if contractor is a person other than the person mentioned in (a)." | "(a) ₹ 30000; for any such sum; **and** (b) ₹ 100000 in case of aggregate of such sums." | 1/2%, 30000 | ✅ rate & per-sum limit **CORRECT**; ⚠️ the **₹1,00,000 aggregate limb is not modelled** — §9.2.1 |
| `194J-P` | s.393(1) **Sl. 6(iii)(a)** | "(b) 10% of such sum in cases other than (a)." | "(i) for (a), (b), (d) and (e) of Col. B: **₹ 50,000**." | 10%, 50000 | ✅ **CORRECT** — the code flagged this threshold MEDIUM; it is right |
| `194J-T` | s.393(1) **Sl. 6(iii)** | "(a) 2% of such sum in case of— (i) fees for technical services (not being a professional services); or (ii) royalty … cinematographic films; or (iii) payee, engaged only in the business of operation of call centre" | "₹ 50,000" | 2%, 50000 | ✅ **CORRECT** |
| `194H` | s.393(1) **Sl. 1(ii)** | "Rate: 2%" | "Threshold limit: ₹ 20,000." | 2%, 20000 | ✅ **CORRECT** — the code called this "the FIRST thing to spot-check". It is right. |
| `194I-A` | s.393(1) **Sl. 2(ii)(a)** | "(a) 2%, for the use of any machinery or plant or equipment" | "Threshold limit [for (a) and (b)]: **₹ 50,000 for a month or part of a month**." | 2%, 50000, MONTHLY | ✅ **CORRECT, including the monthly basis** |
| `194I-B` | s.393(1) **Sl. 2(ii)(b)** | "(b) 10%, for the use of any land, or building (including factory building), or land appurtenant to a building …, or furniture, or fittings." | as above — **₹ 50,000 for a month or part of a month** | 10%, 50000, MONTHLY | ✅ **CORRECT** |
| no-PAN floor (TDS) | s.397(2)(b)(i) | "tax shall be deducted at the higher of the following rates:— (A) at the rate specified in the relevant provision …; or (B) at the rate or rates in force; or (C) at the rate of 5% where tax is required to be deducted under section 393(1) [Table: Sl. No. 8(ii) or 8(v)]; **or 20% in any other case**" | — | 20% | ✅ **CORRECT**, and correctly modelled as a **floor**, not a replacement |
| `206C(1)-SCRAP` | s.394(1) **Sl. 4** | as enacted: "Sale of scrap. Seller. 1%" — **as amended by FA 2026: `2%`**, fn. 88 "Substituted for '1%' … w.e.f. 1-4-2026" | no threshold column in the s.394 Table | ~~1%~~ **2%**, 0 | ❌ **WAS WRONG — CORRECTED 2026-07-31.** See §9.0 below. |
| `206C(1F)-MV` | s.394(1) **Sl. 6** | "Sale consideration **exceeding ten lakh rupees** in case of— (a) motor vehicle … **1%**" | ₹10,00,000, in the description | 1%, 1000000 | ✅ **CORRECT** |
| no-PAN (TCS) | s.397(2)(b)(ii) | "tax shall be collected at the higher of the following rates, **not exceeding 20%**— (A) at **twice the rate** specified …; or (B) at the rate of **5%**" | — | min(max(2×rate, 5%), 20%) | ✅ **CORRECT since 2026-07-31** — the ceiling was missing and is now modelled as `NoPANCeilingPct`; ~~§9.2.3~~ |
| **206C(1H) omitted** | s.394 Table | **There is no entry for sale of goods above ₹50 lakh anywhere in the s.394 Table.** | — | absent | ✅ **the deliberate omission is CORRECT** — this was the code's "single most important item for the release spot-check" |

**~~Eight of eight shipped rules are right.~~ SEVEN of eight — see §9.0.** The two the code least trusted — 194H's 2%/20,000
and 194I's monthly 50,000 — are both confirmed, and the hardest judgement call in the whole
engine (dropping 206C(1H)) is confirmed too.

### 9.2 What the engine does NOT model — the real risk, and it is under-deduction

Every item here causes the software to deduct **too little**, which is the deductor's
liability plus interest, not the deductee's problem.

**9.2.1 — s.393(1) Sl. 6(i)'s ₹1,00,000 aggregate limb.** The statute sets **two**
thresholds joined by "**and**": ₹30,000 for any single sum, **and** ₹1,00,000 for the
aggregate. s.393(1)(a) charges tax "on the entire amount … where the amount **or aggregate
of amounts** exceeds the threshold limit". The engine tests only the per-sum limb, so twenty
₹20,000 contractor bills attract **nothing** where the statute demands deduction on the whole
₹4,00,000 once the aggregate passes ₹1,00,000. `ThresholdBasis` records the approximation;
**T6 persists the data that finally makes cumulation implementable, so build it in T6 rather
than deferring again.**

**9.2.2 — director's remuneration has a NIL threshold.** s.393(1) Sl. 6(iii)(c) covers
"remuneration or fees or commission by whatever name called, other than those on which tax
is deductible under section 392, to a director of a company", and the threshold is
explicitly "**(ii) for (c) of Col. B: Nil**" at **10%**. The engine has no such rule, so a
₹10,000 director's sitting fee is deducted at nothing and should be ₹1,000. **Small
companies pay directors' fees constantly. This is a likely real-world miss.**

**9.2.3 — the TCS no-PAN ceiling. ✅ CLOSED 2026-07-31.** `TCSDerive` computed
`max(2 × rate, 5%)` with **no upper bound**, but s.397(2)(b)(ii) caps it at "**not exceeding
20%**". Now modelled as `TCSRuleSet.NoPANCeilingPct`, applied after the floor so it bounds
the result rather than either limb.

It **cannot** be pinned by a corpus fixture — every shipped section is ≤2%, so doubling never
reaches the ceiling and the shipped table can never exercise it. That is precisely why it was
missing. `TestTCSNoPANCeiling` uses a synthetic 20% ruleset, as s.394(1) [Sl. No. 7(b)]
genuinely is, where doubling would yield 40% against a statutory maximum of 20%. A ruleset
that declares no ceiling keeps the uncapped behaviour rather than collapsing to zero, and
that fallback is asserted too.

**9.2.4 — s.394(2)'s manufacturing declaration.** For Table Sl. Nos. 1–5 — **which includes
scrap, one of the two shipped sections** — no tax is collected if the resident buyer
furnishes a written declaration that the goods are "for the purposes of manufacturing,
processing or producing articles or things or for generating power" and "not for trading
purposes". The engine has no concept of it, so it will collect TCS on scrap sold to a
manufacturer who has filed the declaration. **This needs a stored declaration per buyer with
its own validity, not a boolean on the transaction.**

**9.2.5 — WHO MUST DEDUCT AT ALL. The biggest product finding on this track.**

The obligation is not universal. For contractors, the payer must be a "**designated
person**"; for professional/technical fees, commission and rent, a "**specified person**".
Both definitions turn on the same test (s.402(11)(l) and s.402(37)):

> "(b) an individual or a Hindu undivided family, whose total sales, gross receipts or
> turnover from the business or profession carried on by him **exceed one crore rupees in
> case of business or fifty lakh rupees in case of profession** during the tax year
> **immediately preceding** the tax year in which such income or sum is credited or paid"

Every other entity — company, firm, co-operative society, trust, society, local authority —
is in scope **unconditionally**.

**The engine derives a deduction for anyone who asks.** A sole proprietor with ₹40 lakh
turnover is **not required to deduct** under these entries, and this software would compute
one, post it, and file it. That is a wrong deduction from a supplier's payment.

**DECISION:** deductor eligibility is a **company-level, effective-dated attribute** —
entity type plus preceding-year turnover — **not** a per-transaction input, because it is a
property of the books, changes at most once a year, and must be auditable. The engine must
**refuse** (not silently zero) when a company that is not a designated/specified person
requests a deduction under an entry that requires one. **Refusing beats guessing** — §3.4's
second mechanism.

### 9.3 Everything else the primary text settled

| Question | Answer | Authority |
|---|---|---|
| Rounding | **Nearest multiple of ₹10** for "the amount of total income computed or any amount payable or refundable under this Act" | **s.516** (successor to s.288A/288B), quoted §3.1 |
| Form 26Q | **now Form 140** | s.397(3)(b); Rule 219, Income-tax Rules 2026 |
| Form 24Q | **now Form 138** | secondary |
| Form 27EQ | **now Form 143** | secondary |
| Form 16A | **now Form 131** (reported) | secondary — **verify** |
| Return due dates | Q1 **31 Jul** · Q2 **31 Oct** · Q3 **31 Jan** · Q4 **31 May** | secondary, consistent across sources |
| Deposit due date | "in such time as **may be prescribed**" — it is in the Rules, **not** the Act | s.397(3)(a) |
| Lower-deduction certificate | **s.395(1)/(3)** (successor to s.197); Form 140 carries the certificate number, or the UIN of Form 121 | Act + secondary |
| Charge basis | tax on the **ENTIRE** amount once the threshold is crossed, not on the excess | s.393(1)(a) — matches the engine ✅ |
| Timing of deduction | at **credit or payment, whichever is EARLIER** | s.393(1)(c) — hence §2.1 stores *date of credit* and *date of deduction* separately |
| Contractor material value | deduct on invoice value **excluding** material if stated separately, else on the whole | s.393(1) Sl. 6 Note — **not modelled**; add to the edge-case ledger |

