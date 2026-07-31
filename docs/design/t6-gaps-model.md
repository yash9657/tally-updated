# DESIGN MEMO — the §3.1 statutory under-deduction gaps

> **Track:** `t6-gaps` (Wave 1). **Migration block: 0049** — from `PLAN-TALLY-PARITY.md` §2.5,
> which reserves **0049–0050** for "T6 TDS/TCS persistence". This memo takes **0049**; the
> register/return work in `t6-model.md` takes **0050**. No number outside that block is used.
>
> **Authority for every rate, threshold and definition below:**
> `docs/statutory/ita-2025-as-amended-fa-2026.pdf` — the Income-tax Act 2025 **as amended by
> the Finance Act 2026**. Extracted with `pdftotext -layout` and read verbatim. Citations are
> given as *section + Table Sl. No.*, which is stable, rather than as page or line numbers.
>
> **Return codes** are from `docs/statutory/form-140-file-format.xlsx` (TDS) and
> `form-143-file-format.xlsx` (TCS), Annexure 2 of each, as tabulated in
> `docs/statutory/README.md`.
>
> Invariant numbers in §4 are **local to this memo**. `t6-model.md` and `t4-model.md` each
> carry their own `I-n` register and they already collide with one another — do not read an
> `I-n` across memos.

---

## 0. ⛔ FINDING ZERO — A SHIPPED RATE IS WRONG AND IS UNDER-COLLECTING TODAY

**This was not on the §3.1 gap list. It is not a gap; it is a live defect, and it is the most
urgent thing in this memo.**

`internal/compliance/tcs.go:97` ships:

```go
"206C(1)-SCRAP": {RatePct: pct("1"), Threshold: pct("0.0000"), Basis: BasisPerTxn},
```

The amended Act, **s.394(1) Table Sl. No. 4**, reads:

> | 4. | Sale of scrap. | Seller. | <sup>88</sup>[2%] |
>
> <sup>88.</sup> Substituted for "1%" by the Finance Act, 2026, **w.e.f. 1-4-2026**.

**Scrap TCS is 2%, with effect from 1 April 2026.** The ruleset is named `tcs-2026-04` — it
claims to be the table in force *from April 2026*, which is precisely the date the 2% took
effect. **Within its own declared scope the 1% was never correct.** As at today's books date
the engine collects **half** the tax due on every scrap sale.

### Why the existing verification missed it

`docs/design/t6-model.md` §9.1 marks this row "✅ **CORRECT**" and concludes "**Eight of eight
shipped rules are right**". That check was made against the Act **as enacted** (Act 30 of 2025,
via the ICAI mirror) — see its own §9 header. `docs/statutory/README.md` anticipated exactly
this and said the amended PDF is "**now the authority to verify §9 against** — because whatever
this text says IS current law". **That re-verification had not been done.** This memo does it.

- **7 of 8 shipped rules are right, not 8 of 8.** `t6-model.md` §9.1 must be corrected.
- The six **TDS** rates are **unaffected** — I checked each row for a Finance Act 2026 footnote
  marker and there is none on Sl. No. 1(ii), 2(ii), 6(i) or 6(iii). §9's TDS verdicts stand.
- `206C(1F)-MV` (Sl. No. 6, 1%, "exceeding ten lakh rupees") carries **no** footnote and is
  **correct**.

### Other FA-2026 TCS rate changes, recorded so nobody re-derives them

None of these are shipped, so none is a live defect — but they are the reason the scrap change
is credible rather than an extraction artefact. It is a **coherent block of rate rises in one
amendment**, each with its own footnote:

| Sl. No. | Receipt | Was | Now | Footnote |
|---|---|---|---|---|
| 1 | Alcoholic liquor for human consumption | 1% | **2%** | 86 |
| 2 | Tendu leaves | 5% | **2%** | 87 |
| **4** | **Sale of scrap** | **1%** | **2%** | **88** |
| 5 | Minerals — coal, lignite, iron ore | 1% | **2%** | 88a |
| 7 | LRS remittance, education/medical | 5% | **2%** | 88b |

### The fix

**Correct `tcs-2026-04` in place. Do not add a new ruleset version.** A version string names
*the table in force for a period*; two values for one period is the bug, not the fix. The
pre-April-2026 1% belongs to a ruleset that does not exist here and is not needed — this build
has no books before FY 2026-27.

Both scrap fixtures carry the arithmetic and must move together:
`corpora/tcs/206c1-scrap.json` (5,00,000 × 1% = 5,000 → **× 2% = 10,000**) and
`corpora/tcs/206c1-scrap-rounding.json` (12,345.6789 × 1% = 123.4568 →
**× 2% = 246.913578 → 246.9136** half-up at 4 dp).

⚠️ `corpora/validate.py:262` **re-declares the rate table**, so it is an independent
implementation of the *arithmetic only* — a wrong rate there and here agree, and the gate goes
green. This is open item §7.2 in the handoff. **Changing the rate in three places and getting a
green tells you nothing.** The mutation proof in §4 (V-1) is what actually pins it.

---

## 1. THE OUTPUT INVENTORY

This track produces no new document or screen. Its outputs are **an engine result, a refusal,
and stored state that the T6 register and return will read**. Enumerated per gap.

### Gap #1 — the contractor aggregate threshold

**s.393(1) Table Sl. No. 6(i)**, threshold column, verbatim:

> Threshold limit: [for (a) and (b)] (a) ` 30,000; for any such sum; **and** (b) ` 1,00,000 in
> case of aggregate of such sums.

⚠️ **The chapeau is wider than the §3.1 brief says.** s.393(1)(a) reads:

> on the **entire amount** of such income or sum, where the amount **or aggregate of amounts**
> exceeds the threshold limit specified in column D

Two consequences the handoff does not record:

1. **Aggregation is not contractor-specific.** It governs *every* row of the s.393 Table. Our
   `194J-P`, `194J-T` and `194H` rules carry `BasisAnnual` and are tested per-transaction —
   each of those is the *same* defect, not a separate one. Sl. No. 6(i) is merely the only row
   with **two** limits.
2. **"On the entire amount."** When the aggregate crosses, tax is due on the **whole**
   cumulative sum, not on the excess and not only prospectively. That makes a **catch-up
   deduction** on the crossing payment a required output, not an optional nicety.

| Output field | Required by | Effective from |
|---|---|---|
| `AggregateBase` — cumulative taxable base for (deductee, section, tax year) *before* this payment | s.393(1)(a) "aggregate of amounts" | 1-4-2026 |
| `AggregateCrossed` — whether this payment takes the aggregate past the limb-(b) limit | s.393(1) Sl. 6(i)(b) | 1-4-2026 |
| `CatchUpBase` — the earlier, under-deducted base now brought to tax | s.393(1)(a) "entire amount" | 1-4-2026 |
| `AggregateThreshold` — the limb-(b) limit tested (₹1,00,000 for 6(i); the single stated limit elsewhere) | s.393(1) col. D | 1-4-2026 |

### Gap #2 — directors' fees

**s.393(1) Table Sl. No. 6(iii)(c)**, verbatim:

> remuneration or fees or commission by whatever name called, **other than those on which tax
> is deductible under section 392**, to a director of a company

Rate: limb (b), "**10%** of such sum in cases other than (a)". Threshold: "**(ii) for (c) of
Col. B: Nil.**" Return code **1028** (Annexure 2), **not** `1027`.

| Output field | Required by | Effective from |
|---|---|---|
| new section key for directors' remuneration | s.393(1) Sl. 6(iii)(c) | 1-4-2026 |
| rate 10%, threshold **Nil** | ibid., col. D | 1-4-2026 |
| return code `1028` | Form 140 file format, Annexure 2 | TY 2026-27 |

⚠️ **"other than those on which tax is deductible under section 392"** — s.392 is **salary**. A
whole-time or managing director on payroll is a **salary** deduction (Form 138 / ex-24Q), which
`HANDOFF-PARITY-SPRINT.md` §3.2 puts **out of scope**. Only a *non-employee* director's sitting
fee or commission lands in 6(iii)(c). **This is a routing condition, and getting it wrong files
salary as professional fees.**

### Gap #3 — the TCS no-PAN ceiling

**s.397(2)(b)(ii)**, verbatim:

> tax shall be collected at the higher of the following rates, **not exceeding 20%**— (A) at
> twice the rate specified in the relevant provision of this Act; or (B) at the rate of 5%;

Current `TCSDerive` computes `max(2 × rate, 5%)` with **no upper bound**.

**Finding zero makes this less latent than the handoff assumed.** At the corrected 2% scrap
rate the no-PAN result is `max(4%, 5%) = 5%` — still under the cap, so still latent. But
s.394's own Table has a **20%** row (Sl. No. 7(b), LRS other than education/medical), where
doubling yields 40% against a statutory ceiling of 20%. The cap is a **two-line change with a
fixture**; add it now.

Two further clauses of s.397(2) that nothing in this repo models — recorded, not built here:

- **s.397(2)(e):** for rent under Sl. No. 2(i), a no-PAN deduction "shall **not exceed the
  amount of rent payable for the last month** of the tax year or the last month of the
  tenancy". A *second* ceiling, on the TDS side, on a section we ship (`194I-*` is Sl. No.
  2(ii) — 2(i) is the *non*-specified-person payer, which we do not model). **Out of scope,
  logged as an open item.**
- **s.397(2)(c)/(d):** the no-PAN rules do not apply to certain non-residents. Our engine is
  resident-only (s.393 Table is headed "FOR PAYMENTS TO RESIDENT"). **No action; recorded so a
  future non-resident track does not assume the floor is universal.**

### Gap #4 — the s.394(2) manufacturing declaration

**s.394(2)**, verbatim:

> Irrespective of anything contained in sub-section (1) (Table: Sl. Nos. **1 to 5**), the
> collection of tax shall not to be made in respect of receipts specified in sub-section (1)
> (Table: Sl. Nos. 1 to 5) in respect of the buyer, **who is a resident in India**, if he
> furnishes a **written declaration in duplicate** in such form and manner, as may be
> prescribed, … mentioning that such goods are to be utilised— (a) for the purposes of
> **manufacturing, processing or producing articles or things or for generating power**; and
> (b) **not for trading purposes**.

| Output field | Required by | Effective from |
|---|---|---|
| declaration applicability — **Sl. Nos. 1–5 only** | s.394(2) | 1-4-2026 |
| buyer residence — **resident in India** | s.394(2) | 1-4-2026 |
| both purposes (a) **and** (b), conjunctive | s.394(2) | 1-4-2026 |
| onward-filing due date | s.394(3) | 1-4-2026 |

Three things that fall straight out of the text:

1. **It covers scrap (Sl. No. 4) but NOT motor vehicles (Sl. No. 6).** Our two shipped TCS
   sections split across the boundary. A declaration presented against `206C(1F)-MV` is
   **invalid** and must be refused, not ignored.
2. **s.394(3) is a dated obligation we would be creating:** the collector "shall deliver … one
   copy of the declaration to the Principal Chief Commissioner or Chief Commissioner or
   Principal Commissioner or Commissioner, **on or before the seventh day of the month
   following the month of receipt** of that declaration." Storing a declaration without
   surfacing this due date builds a compliance trap. **Modelled as stored state + a T6 register
   column; the actual reminder is out of scope and logged.**
3. **⚠️ s.397(2)(f)(i) + (g) couple gap #4 to gap #3.** Verbatim: "if a person does not furnish
   his valid Permanent Account Number in— (i) any declaration under section 393(6) or
   **394(2)**, then such declaration **becomes invalid**"; and (g) "if any declaration becomes
   invalid …, then the deductor or collector shall deduct or collect tax as per the provisions
   of clause (b)(i) or (ii)". **A declaration from a buyer with no valid PAN does not merely
   lose the exemption — it triggers the penal no-PAN rate.** Exemption → 5%, not exemption →
   2%. Nothing in the §3.1 brief says this.

### Gap #5 — whether there is a duty to deduct at all

The payer column of the s.393 Table is **not "any person"** for the rows we ship:

| Our key | Table Sl. No. | Payer (col. C) |
|---|---|---|
| `194C` | 6(i) | **"Any designated person."** |
| `194J-P`, `194J-T` | 6(iii) | **"Specified person."** |
| `194H` | 1(ii) | **"Specified person."** |
| `194I-A`, `194I-B` | 2(ii) | **"Specified person."** |

**s.402(37) — "specified person"**, verbatim:

> (a) any person, **not being an individual or Hindu undivided family**; or (b) an individual or
> a Hindu undivided family, whose total sales, gross receipts or turnover from the business or
> profession carried on by him **exceed one crore rupees in case of business or fifty lakh
> rupees in case of profession** during the tax year **immediately preceding** the tax year in
> which such income or sum is credited or paid;

**s.402(11) — "designated person"** (for Sl. No. 6(i) only) is an explicit list — Central/State
Government, local authority, corporation under a Central/State/Provincial Act, **company**,
co-operative society, housing/planning authority, registered society, **trust**, university,
foreign government or enterprise, **firm** — plus:

> (l) any person, being an **individual or a Hindu undivided family or an association of persons
> or a body of individuals**, if such person,— (i) does not fall under any of the preceding
> sub-clauses; and (ii) has total sales, gross receipts or turnover … exceeding **one crore
> rupees in case of business or fifty lakh rupees in case of profession** during the tax year
> **immediately preceding** …

⚠️ **The two definitions are NOT the same test, and treating them as one is a defect.** For
**AOP / BOI**: `specified person` (a) catches them **unconditionally** (they are "not an
individual or HUF"), whereas `designated person` (l) subjects them to the **turnover test**. So
an AOP with ₹40 lakh turnover **must** deduct on professional fees (6(iii)) and **must not** on
contractor payments (6(i)). One boolean cannot express this.

| Output field | Required by | Effective from |
|---|---|---|
| `DeductorCategory` — the entity class of the paying company | s.402(11), s.402(37) | 1-4-2026 |
| `PriorYearTurnover` + `PriorYearActivity` (business \| profession) | s.402(11)(l)(ii), s.402(37)(b) | 1-4-2026 |
| per-section **liability verdict**, or an explicit **refusal** | s.393(1) col. C | 1-4-2026 |

**The engine must REFUSE, not return zero.** `HANDOFF-PARITY-SPRINT.md` §3.1 gap 5 is explicit
and it is right: a silent zero is indistinguishable from "correctly below threshold", and it is
the deductor who carries the liability plus interest.

---

## 2. THE PROVENANCE TABLE

**Every "stored where" below was checked against the live database on 2026-07-31** with
`information_schema.columns` on `postgres@localhost:5433/tally` (41 migrations applied, max
`0046_bank_recon_acl`). Not from memory, not from a summary.

| Field | Required by | Stored where | Verdict |
|---|---|---|---|
| deductee PAN | s.397(2), Form 140 | `ledger.pan` ✅ exists | **OK** |
| deductee is individual/HUF vs other | s.393(1) Sl. 6(i) rate split | **nowhere** — `ledger` has no deductee-type column; today it rides in on `TDSParams.DeducteeType` per call | **MIGRATION 0049** |
| deductee residence (resident in India) | s.394(2); s.393 Table heading | **nowhere** | **MIGRATION 0049** |
| cumulative taxable base per (deductee, section, tax year) | s.393(1)(a) "aggregate of amounts" | **nowhere** — no TDS table of any kind exists | **MIGRATION 0049** |
| the deduction facts themselves (section, base, rate, amount, date) | s.393(1); Form 140 annexure | **nowhere** — `voucher_version` has no TDS columns; `PostResult.TDS` is in-process only | **MIGRATION 0049** |
| paying company's entity category | s.402(11), s.402(37) | **nowhere** — `company` has no such column | **MIGRATION 0049** |
| paying company's prior-year turnover + activity | s.402(11)(l)(ii), s.402(37)(b) | **nowhere** | **MIGRATION 0049** |
| s.394(2) declaration: buyer, date, purposes, PAN validity | s.394(2), s.397(2)(f)(i) | **nowhere** | **MIGRATION 0049** |
| s.394(3) onward-filing due date | s.394(3) | **derivable** from declaration receipt date | **NEEDS DERIVATION — new leaf** |
| directors'-fees section + code `1028` | s.393(1) Sl. 6(iii)(c); Annexure 2 | **nowhere** — `tdsRegistry` has no such key, and no code column exists at all | **CODE + MIGRATION 0049** |
| TCS no-PAN 20% ceiling | s.397(2)(b)(ii) | `TCSRuleSet` has `NoPANMultiplier`, `NoPANFloorPct` — **no ceiling field** | **CODE ONLY — no migration** |
| scrap rate 2% | s.394(1) Sl. 4 fn. 88 | `tcs.go:97` — **holds 1%, wrong** | **CODE ONLY — no migration** |

**Tables verified absent:** there is no `tds_*`, `tcs_*`, `deductee`, `deduction` or
`declaration` table in the live schema. The full table list was read and none exists. Gap #1,
#4 and #5 are therefore **all** first-storage problems, which is why none of them is the
"small pure-function fix" the gap list's framing suggests.

### The `company` vs `ledger` decision for gap #5

Deductor eligibility is an attribute of **the books' owner**, not of a counterparty, so it
belongs on `company`. But it is **effective-dated** — the test reads "the tax year immediately
preceding" — and `company` has no history mechanism. Two options:

- **(a)** columns on `company` + a `company_id`-scoped history table.
- **(b)** a single `company_deductor_status` table keyed `(company_id, tax_year)`, with
  `company` untouched.

**Decision: (b).** The test is *per tax year* by construction, so `(company_id, tax_year)` is
the natural key and needs no separate history table. It also keeps `company` — which every
tenancy test, fixture and RLS policy touches — out of this change. A row's absence for a year
is then a **refusal**, which is exactly the required behaviour and comes free rather than
needing a nullable-column convention.

---

## 3. UNITS AND SCALE AT EVERY SEAM

The ledger stores money at **NUMERIC(19,4)**; the Form 140/143 file writes **amounts at 2 dp
and rate at 4 dp** (`docs/statutory/README.md`, both file-format specs). Rounding is
**half-up**, matching `compliance.round`.

| Identifier | Unit / scale | Seam |
|---|---|---|
| `AggregateBaseRupees` | rupees, 4 dp | `compliance` → `ledger` |
| `CatchUpBaseRupees` | rupees, 4 dp | `compliance` → `ledger` |
| `AggregateThresholdRupees` | rupees, 4 dp | ruleset → result |
| `PerTxnThresholdRupees` | rupees, 4 dp | **rename** of `sectionRule.PerTxnThreshold` |
| `NoPANCeilingPct` | percent, not a fraction — `20` means 20% | `TCSRuleSet` field |
| `PriorYearTurnoverRupees` | rupees, 4 dp | DB → `compliance` |
| `RatePct` | percent; **written to the return at 4 dp** (`2` → `2.0000`) | engine → return |

**The existing `PerTxnThreshold` is a unitless name crossing a package boundary** and §3 of the
memo shape calls that "a defect in review". It is pre-existing, and this track is the one that
adds a *second* threshold beside it — leaving both unnamed would make the pair genuinely
ambiguous. **Rename both as part of this change.**

⚠️ **`TDSResult.Threshold` becomes ambiguous the moment there are two limits.** It must not
silently keep meaning "the per-transaction one". Replace with `PerTxnThresholdRupees` and
`AggregateThresholdRupees`, both always populated. A single `Threshold` field that means
different things depending on which limb fired is the shape of a future bug.

---

## 4. INVARIANTS, EACH WITH THE TEST THAT PINS IT

Numbering is **local to this memo** (see the header note).

| # | Invariant | Enforcement |
|---|---|---|
| **V-1** | Scrap TCS is **2%** for `tcs-2026-04` | Fixture + **mutation proof**: set it back to 1%, the corpus gate **and** a Go test must both fail. If only one fails, `corpora/validate.py:262`'s duplicate table is the reason — fix that too. |
| **V-2** | No-PAN TCS rate **never exceeds 20%**, for any section and any rate | Property test over rates 0–100%: `TCSDerive` result ≤ 20 always. **Mutation: remove the clamp, the test must fail** — the shipped 2% row will NOT catch this, so the test must use a synthetic high-rate ruleset. |
| **V-3** | A deduction row's amount = `round(base × rate ÷ 100, 4)` for its stored ruleset version | Constraint trigger + Go re-derivation. Mutation: alter the stored rate by 0.01, trigger fires. |
| **V-4** | The aggregate for `(deductee, section, tax_year)` equals the **sum of its contributing deduction rows** — it is never a free-standing counter | Deferred constraint trigger on both tables. **A running total that can drift from its rows is the classic version of this bug**; the sum must be derivable and checked. |
| **V-5** | Once the aggregate limb is crossed, tax is charged on the **entire** cumulative base, including the previously under-deducted part | Test: twenty ₹20,000 contractor bills. Bills 1–5 deduct nothing; bill 6 crosses ₹1,00,000 and must deduct on the **whole ₹1,20,000**, not on ₹20,000. Named `TestContractorAggregateCatchUp`. |
| **V-6** | A payment **exactly equal** to either limb attracts **nothing** — both statutes say "exceeds"/"exceeding" | Boundary fixtures at exactly ₹30,000 and exactly ₹1,00,000. The existing `194c-at-threshold-exact.json` is the precedent. |
| **V-7** | Directors' fees derive code **`1028`**, never `1027`, though both sit in Sl. No. 6(iii).D(b) | Test asserting the code is stored/mapped from **nature of payment**, not derived from the section. **Mutation: derive from the section, the test must fail.** |
| **V-8** | A director on payroll (s.392 salary) is **refused** by the 6(iii)(c) path, not deducted at 10% | Refusal test named `TestDirectorOnPayrollIsSalaryNotProfessional`. |
| **V-9** | A s.394(2) declaration is valid **only** for Table Sl. Nos. 1–5 | Refusal test: a declaration against `206C(1F)-MV` (Sl. No. 6) is rejected. |
| **V-10** | A declaration from a buyer with **no valid PAN** yields the **no-PAN penal rate**, not the exemption and not the base rate | Test named for s.397(2)(f)(i)+(g). **This is the coupling that is easy to get half-right**: assert the result is 5%, and assert it is *not* 0 and *not* 2%. |
| **V-11** | A company with no `company_deductor_status` row for the relevant tax year causes the engine to **REFUSE** | Error-returning test. **Mutation: return zero instead, the test must fail.** A zero and a refusal must be distinguishable in the type, not only in a log line. |
| **V-12** | AOP/BOI is liable under 6(iii)/1(ii)/2(ii) **unconditionally** but under 6(i) **only** on the turnover test | Table test over the cross-product of entity category × section. **The one that catches a single shared boolean.** |
| **V-13** | Every new table: RLS `ENABLE` **and** `FORCE`, policy with `USING` **and** `WITH CHECK`, composite `(company_id, id)` FKs, a row in `testdb`'s `buildFixture` | `internal/security/isolation_test.go` is **catalogue-driven** over all company-scoped tables (handoff §7.8), so new tables are picked up automatically — **verify that, do not assume it.** |
| **V-14** | Migration `0049` is **re-runnable** | `psql -f` twice, not `make migrate` twice (§2.5: the file is `go:embed`'d and any concurrent test run may have applied it already — say which you got). |

---

## 5. THE EDGE-CASE LEDGER

| Case | Behaviour | Test |
|---|---|---|
| Payment exactly ₹30,000 to a contractor | No deduction (limb (a) is "exceeds") | `V-6` |
| Aggregate exactly ₹1,00,000 | No deduction (limb (b) is "exceeds") | `V-6` |
| Aggregate crosses mid-payment | Deduct on the **entire** cumulative base | `V-5` |
| Twenty ₹20,000 bills | Nothing until the crossing bill, then catch-up | `V-5` |
| A single ₹40,000 bill, aggregate ₹40,000 | Deducts — limb (a) fires alone | `V-6` |
| Tax-year boundary | The aggregate **resets**; a payment on 1 April starts a new cumulation | `TestAggregateResetsAtTaxYear` — the key includes `tax_year` |
| Voucher **altered** after the aggregate advanced | The register reads the **current version only**; the aggregate must not double-count | Mirrors `t6-model.md` I-9. **The printed Day Book already got the sibling case wrong** — handoff §7.6. |
| Voucher **deleted/cancelled** after crossing | Contributes to no aggregate; a later payment may fall back below the limb | `TestCancelledVoucherLeavesAggregate` |
| Back-dated voucher landing before earlier ones | Cumulation is by **date**, not insertion order; a back-dated bill can retro-trigger a crossing | **The nastiest case here.** `TestBackDatedPaymentTriggersAggregate` |
| Director who is also an employee | Refuse — s.392 salary path, out of scope | `V-8` |
| Director's fee of ₹1 | Deducts ₹0.10 — threshold is **Nil**, so any positive sum attracts | `TestDirectorNilThreshold` |
| Declaration against motor vehicles | Refused — Sl. No. 6 is outside s.394(2) | `V-9` |
| Declaration, buyer has no valid PAN | **No-PAN penal rate**, not exemption | `V-10` |
| Declaration for trading purposes | Refused — (a) and (b) are conjunctive | `TestDeclarationTradingRefused` |
| Non-resident buyer files a declaration | Refused — s.394(2) says "resident in India" | `TestDeclarationNonResidentRefused` |
| Company with no deductor-status row | **REFUSE**, never zero | `V-11` |
| Sole proprietor, ₹40 lakh business turnover | Not a specified/designated person → **no duty to deduct**; refuse to derive | `V-12` |
| AOP, ₹40 lakh turnover, professional fees | **Liable** — s.402(37)(a) is unconditional for non-individual/HUF | `V-12` |
| AOP, ₹40 lakh turnover, contractor payment | **Not liable** — s.402(11)(l) applies the turnover test | `V-12` |
| Turnover exactly ₹1 crore | **Not** liable — the text is "exceed one crore rupees" | `TestTurnoverBoundaryExact` |
| Profession vs business misclassified | Different limit (₹50 lakh vs ₹1 crore); the activity is stored, never inferred | `V-12` |
| No-PAN TCS on a hypothetical 20% section | Capped at 20%, not 40% | `V-2` |

---

## 6. WHAT THIS MEMO DOES **NOT** BUILD — logged, not silently dropped

| # | Item | Why deferred |
|---|---|---|
| 1 | **s.397(2)(e)** — the no-PAN rent deduction ceiling (last month's rent) | Applies to Sl. No. 2(i), the *non*-specified-person payer, which we do not model. Real, and unmodelled. |
| 2 | **s.394(3)** reminder — deliver the declaration to the CIT by the 7th of the following month | Storage + a register column here; the reminder itself belongs with T6's register. |
| 3 | Aggregate cumulation for `194J-*` / `194H` | The chapeau makes these the **same** defect as gap #1. Fixing 6(i) alone leaves three sections still under-deducting. **This should be one mechanism, not four** — but it is a scope call for the plan owner, so it is raised rather than assumed. |
| 4 | Section identifier migration to s.393/394 Sl. Nos. | `t6-model.md` / handoff open item §7.10 puts this in T6 (migration 0050), "in one reviewable step". Doing it here would split it. |
| 5 | Salary TDS (s.392, Form 138) | Out of scope per handoff §3.2 — but **gap #2 now has a live edge against it** (V-8). |

---

## 7. OPEN QUESTIONS FOR THE PLAN OWNER

1. **Item 6.3 above is the big one.** The statutory chapeau makes aggregation universal. Gap #1
   as briefed fixes one section of four that have the same defect. Fix all four now, or ship
   6(i) and log the rest?
2. **`t6-model.md` §9.1 must be corrected** — "eight of eight" is now false, and it is the
   sentence a future reader will trust. It should be amended in the same commit as the rate fix
   so the two never disagree.
