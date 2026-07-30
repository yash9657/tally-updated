# T6 — TDS / TCS persistence, register and returns: THE DATA MODEL

> **This memo is the §3.2 gate on T6. No implementation agent may be launched until it is
> adversarially reviewed and accepted.** It exists because T1 took three waves for one
> reason: the engine was right and the schema was missing. Everything below was verified
> against the live database with `\d` on 2026-07-30, not from memory and not from a model
> summary.
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
| `ledger.pan`, `employee.pan` | Both exist. Deductee PAN is available for both 26Q and 24Q. |
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
on a filed return. Per §3.4's second mechanism, the rate table must be re-verified against
primary text before T6 ships, or every rule must be marked `ConfUnverified` and **refused**
the way `gststate` refuses codes 96/99. **T6 must not be the thing that launders an
unverified rate into a statutory filing.**

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

### 1.1 Form 26Q — quarterly return, NON-SALARY deductions (the track's centrepiece)

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

### 1.2 Form 27EQ — quarterly TCS return
Same three blocks, collection-side; deductee → **collectee**, and the no-PAN rule is
**s.206CC**, not s.206AA. **Do not build this by copying 26Q's mapper.**

### 1.3 Form 16A — the deductee's certificate
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

### 2.1 Deduction-level facts (Form 26Q annexure)

| Field | Required by | Stored where | Verdict |
|---|---|---|---|
| Deductee PAN | 26Q annexure | `ledger.pan` ✓ | **OK** |
| Deductee name | 26Q annexure | `ledger.name` ✓ | **OK** |
| Amount paid/credited | 26Q annexure | `posting.amount` ✓ | **OK** — but which leg is the *taxable base* is not recorded; see below |
| Section / nature of payment | 26Q annexure | ***nowhere*** — `gst_inward_document.section` is a GST field | **MIGRATION** |
| Deductee code (01/02) | 26Q annexure | ***nowhere*** (`compliance.DeducteeIndHUF/Other` is a request-time input, discarded) | **MIGRATION** |
| Rate actually applied | 26Q annexure | ***nowhere*** (returned on the HTTP response, then dropped) | **MIGRATION** |
| TDS amount deducted | 26Q annexure | ***nowhere*** — only an unlabelled credit posting | **MIGRATION** |
| Taxable base the rate applied to | 26Q annexure | ***nowhere*** — `TDSParams.TaxableAmount` is request-time only | **MIGRATION** |
| Date of payment/credit | 26Q annexure | `voucher_version.voucher_date` ✓ | **OK** |
| **Date of deduction** | 26Q annexure | ***nowhere*** — **not the same date** as payment/credit when tax is deducted on credit and paid later | **MIGRATION** |
| Was a PAN on record **at deduction time** | s.206AA defence | ***nowhere*** — `PANAvailable` is a request input, discarded; `ledger.pan` is *current*, and a PAN added later would retroactively make a correct 20% deduction look wrong | **MIGRATION — store the flag, do not re-derive it** |
| Lower-deduction certificate no. (s.197) | 26Q annexure | ***nowhere*** | **MIGRATION** |
| Reason for non/lower deduction | 26Q annexure | ***nowhere*** | **MIGRATION** |
| Surcharge, cess | 26Q annexure | ***nowhere*** — the engine models neither | **MIGRATION + ENGINE GAP** (see §6.3) |
| Ruleset version used | audit / reproducibility | ***nowhere*** | **MIGRATION** — without it a rate correction silently rewrites history |

### 2.2 Challan-level facts

| Field | Required by | Stored where | Verdict |
|---|---|---|---|
| BSR code (7 digits) | 26Q challan block | ***nowhere*** | **MIGRATION** |
| Challan serial number | 26Q challan block | ***nowhere*** | **MIGRATION** |
| Date of deposit | 26Q challan block | ***nowhere*** | **MIGRATION** |
| Amount deposited, split TDS/surcharge/cess/interest/fee/penalty | 26Q challan block | ***nowhere*** | **MIGRATION** |
| Paid-by-book-entry flag | 26Q challan block | ***nowhere*** | **MIGRATION** |
| Which deductions a challan covers | 26Q (annexure ↔ challan link) | ***nowhere*** | **MIGRATION — a link table, and §4's I-2 lives on it** |

### 2.3 Deductor and deductee identity

| Field | Required by | Stored where | Verdict |
|---|---|---|---|
| Deductor **TAN** | 26Q deductor block | ***nowhere*** — `company` has `gstin`, no TAN | **MIGRATION. A return cannot be filed without it, and it is not the GSTIN.** |
| Deductor **PAN** | 26Q deductor block | ***nowhere*** — checked: `company` has no `pan` column (it has `gstin` only; `ledger.pan` and `employee.pan` are the *other* party's) | **MIGRATION** |
| Deductor name, address | 26Q deductor block | `company.name`, `company.address/city/pincode` ✓ (0015) | **OK** |
| Deductor category (Company/Non-company/Govt) | 26Q deductor block | ***nowhere*** | **MIGRATION** |
| Responsible person name/designation | 26Q deductor block | ***nowhere*** | **MIGRATION** |
| **Deductee address** | **Form 16A** | `ledger.address/city/pincode` ✓ **(0039, and readable over HTTP as of `b76a7e1`)** | **OK — T1 paid for this already** |

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

**Decision required at review, not at implementation:** is the deposited figure rounded
*per deduction* or *per challan aggregate*? The two differ by up to (n−1) rupees over n
deductions, and **whichever is chosen, §4's I-2 must reconcile against that same figure or
the challan will never tie.** Payroll already faced this and chose
`_round_rupee` per payslip (`validate.py:2514`) — **follow that precedent unless review
finds a reason not to, and record the reason either way.**

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

## 6. WHAT MUST BE SETTLED BEFORE THE BUILD STARTS

These are decisions, not tasks. **The build does not start until review rules on them.**

### 6.1 ⚠️ THE SECTION-CODE TAXONOMY — the biggest open question, and it is structural

The **Income-tax Act 2025 renumbers every TDS provision with effect from 1 April 2026** —
which is *exactly* the period this ruleset (`tds-2026-04`, FY 2026-27) covers. Corroborated
mapping:

| Old | New (Income-tax Act 2025) |
|---|---|
| 192 (salary) | **392(1)** |
| 194C (contractors) | **393(1)[6(i)]** |
| 194J (professional/technical) | **393(1)[6(iii)]** |
| 194H (commission/brokerage) | **393(1)[1(ii)]** |
| 194I (rent) | **393(1)[2(ii)]** |

**Whether Form 26Q for TY 2026-27 must quote the old 194-series or the new codes could not
be established** — the source consulted is explicit that it does not say.

This is a **data-model** question, not a cosmetic one: it decides what the `section` column
*contains*, and a return filed with the wrong code family is rejected wholesale. **Design for
it now:** store a stable internal key **plus** an effective-dated mapping to the code the
form wants, so the same stored deduction can render under either family. **Do not store the
form's display string as the primary key.** Resolve against the primary text before any file
is emitted; until then, mark the mapping `ConfUnverified` and refuse to emit (I-7).

### 6.2 The rate table must be re-verified against primary text
Per §0.2. Secondary sources reachable from here **corroborate** the six rows — including
194I's 50,000 **per month** (the code's own MEDIUM-confidence flag), and 194C's ₹1,00,000
**annual aggregate**, which the engine **defers** and therefore **under-deducts** on split
payments. Corroboration is not verification. **Never invent a rate, a threshold or a clause
letter to make a test pass.**

### 6.3 Surcharge and cess are not modelled at all
26Q reports TDS, surcharge and cess as **separate columns**. `TDSResult` has one amount.
For resident deductees these are usually nil, which is why it has not bitten — but the
**column must exist** and the engine must state that it always emits zero, rather than the
return quietly implying zero. **A field that cannot be stored is a refusal that must be
designed, not a blank that appears on paper.**

### 6.4 Scope decisions to ratify
1. Is TCS (`27EQ`) in T6 or a follow-up? The engine exists but **is not wired to the post
   path** — wiring it is not free.
2. Section code **per transaction, per ledger, or per voucher line?** Recommend
   **per voucher line**: one payment can span two natures of payment.
3. Is the deposited figure rounded per deduction or per challan? (§3.1)
4. 24Q is **out of scope** (§2.4) — ratify explicitly so it is not assumed delivered.

### 6.5 Sources consulted (all SECONDARY — none is the notified form or the FVU File Format)
- [Income-tax Act 2025: TDS Rates, Thresholds & Compliance Changes for TY 2026-27 — TaxGuru](https://taxguru.in/income-tax/income-tax-act-2025-tds-rates-thresholds-compliance-ty-2026-27.html) — the §6.1 renumbering table and the §6.2 rate/threshold corroboration
- [Form 26Q: TDS Return Filing for Non Salary Deductions — ClearTax](https://cleartax.in/s/tds-return-non-salary) and [Form 26Q — Tax2win](https://tax2win.in/guide/form-26q) — the §1.1 field enumeration
- [Form 26Q (PDF) — TDSMAN](https://www.tdsman.com/downloads/forms/Form-26Q.pdf) — **fetched but NOT readable** (compressed PDF streams); it did **not** contribute a single field below

**The GSTN portal, the FVU, the NIC e-Way Bill API and the EPFO/ESIC portals cannot be
reached from this environment. No return produced by T6 has been validated by the FVU, and
no such claim may be made.**

---

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
- [ ] §6.1 ruled on: what does `section` contain, and can it render both code families?
- [ ] §6.2 ruled on: re-verify the rates, or mark `ConfUnverified` and refuse
- [ ] §3.1 ruled on: rounding per deduction or per challan, and I-2 reconciles to it
- [ ] I-2 is a **deferred constraint trigger**, not a form check
- [ ] §4.1's pinned vector sets **all three** of Inventory, Inward and the new field
- [ ] Every new table in `tenantTables` **and** `buildFixture`
- [ ] No new numeric identifier crosses a package boundary without its unit in the name
- [ ] No claim anywhere that a return was FVU-validated
