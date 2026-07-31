# docs/statutory — primary sources, downloaded by a human

**Why this directory exists:** `incometaxindia.gov.in` returns **403 to every automated
request** — with and without a browser user-agent, from more than one fetcher. A human with
a browser gets through instantly. So the primary statutory documents this project must not
guess at are downloaded by hand and committed here.

**What belongs here:** the notified form, the file-format specification, the gazette text —
the things a rate, a field or a code list must be traceable to. **Not** blog articles, and
not summaries.

**What is expected, and why** (see `HANDOFF-CLOSING-THE-GAP.md` §1):

| File | Status | Source | What it settles |
|---|---|---|---|
| `form-140.pdf` | ✅ **here** (2026-07-30) | `https://www.incometaxindia.gov.in/documents/d/guest/fn-140` | The return's statutory anchors and its annexure's field NAMES. **It does NOT contain the four-digit payment codes** — see below. |
| `form-140-faqs.pdf` | ✅ **here** (2026-07-30) | `https://www.incometaxindia.gov.in/documents/d/guest/form-140-faqs` | Nothing further; it carries no code list either (checked). |
| `ita-2025-as-amended-fa-2026.pdf` | ✅ **here** (2026-07-30) | `https://www.incometaxindia.gov.in/documents/d/guest/income_tax_act_2025_as_amended_by_fa_act_2026-pdf` | The s.393 rate table **as amended**. This is now the authority to verify §9 against — better than a yes/no on "did FA 2026 change it", because whatever this text says IS current law. |
| `fvu-file-format.pdf` | ❌ **STILL MISSING — THE ONE BLOCKER** | Protean/TIN, "**File Format for Form Number 140** Q1 to Q4 Version 1.0" (TY 2026-27 onwards) | Form 140's column order, **field widths** and its **Annexure 2 payment-code list**. **`docs/design/t6-model.md` invariant I-7 still refuses to emit a TDS return.** |
| `form-143-file-format.xlsx` | ✅ **here** (2026-07-31) | Protean/TIN, "File Format for Form Number 143 (27EQ) Q1 to Q4 Version 1.0" | The **TCS** return. Delivered more than expected — see the verified collection codes below. |

### ⚠️ What Form 140 does NOT contain — checked, not assumed

Both PDFs were text-extracted and searched. **Neither contains a four-digit payment code
list.** The codes circulating in secondary sources (1023 / 1024 / 1027 / 1026 / 1006 / 1008 /
1009) appear in **neither** document. `HANDOFF-CLOSING-THE-GAP.md` §1.1 already recorded that
list as untrusted because the same source got thresholds wrong; obtaining Form 140 has **not**
verified it, and it must not be treated as verified.

What Form 140 actually gives for the annexure is a list of field **names**, ending in
"**etc.**" — an explicitly open list, with no widths and no order:

> PAN, Name, Section Code, Amount paid or credited, Date of payment or credit, Tax Deducted,
> Tax Deposited, Date of Deduction, Rate, Reason for non/lower/higher Deduction, Certificate
> Number u/s 395, UIN of Form No. 121, **etc.**

Three consequences worth stating because each contradicts a plausible secondary claim:

1. **The field count is not fixed by this document.** Any specific count (e.g. "14 fields") is
   not supported by it.
2. **There are THREE deduction-variance cases, not two** — non, lower **and higher**. A
   two-valued or A–D code set does not obviously cover them, and no code list is given here.
3. Two identifiers are named that the engine has nowhere to put yet: the **s.395 certificate
   number** and the **UIN of Form No. 121**.

### ✅ What Form 140 DID settle (verified from its own text)

- Filed under **s.397(3)(b)**, **Rule 219 of the Income-tax Rules, 2026** (was s.200(3) /
  Rule 31A of the 1962 Rules). Tax payment timing is **Rule 218**.
- **A SINGLE annexure, filed for all four quarters**, capturing deductee-level TDS across
  multiple sections — not one annexure per quarter.
- Due dates: **Q1 31 July, Q2 31 October, Q3 31 January** of the tax year; **Q4 31 May** of
  the year immediately following.
- Part B carries the challan identifiers: BSR code, date of deposit, challan serial number and
  **minor head**.

### ✅ What the amended Act settled — including two §2.3 gaps, from primary text

Read from `ita-2025-as-amended-fa-2026.pdf`, s.393 rate table Sl. No. 6:

- **The contractor DUAL threshold is real and the conjunction is "and"**, verbatim:
  "Threshold limit: [for (a) and (b)] (a) `₹30,000; for any such sum; **and** (b) `₹1,00,000
  in case of aggregate of such sums." This confirms `HANDOFF-CLOSING-THE-GAP.md` §2.3 gap 1
  from the primary source: an engine modelling only the per-payment limit under-deducts.
- **Directors' fees are 10% at a NIL threshold**, confirming §2.3 gap 2. Sl. No. 6(iii)(c)
  covers "remuneration or fees or commission ... to a director of a company", it falls in rate
  limb (b) "10% ... in cases other than (a)", and the threshold is stated as
  "(ii) for (c) of Col. B: **Nil**."
- Contractor rates: **1%** individual/HUF, **2%** other. 6(ii) individual/HUF payer: **2%** at
  a **₹50 lakh** threshold. 6(iii): **2%** for technical services / cinematographic-film
  royalty / call-centre operators, **10%** otherwise, threshold **₹50,000**. Dividend **10%**,
  threshold **Nil**.
- Note under 6(i): where the work is s.402(47)(e), deduct on the invoice value **excluding
  material if separately specified**, else on the whole invoice value.

**A note on what is already verified.** The Income-tax Act 2025 as enacted HAS been read —
sections 392, 393, 394, 395, 397 and 516, verbatim from the Gazette text, via ICAI's mirror
(`resource.cdn.icai.org`). All eight shipped TDS/TCS rates match it. See
`docs/design/t6-model.md` §9. **The remaining gap is the FILE FORMAT, not the statute and no
longer the form.**

---

## Form 143 (ex-27EQ) — the TCS return, read from the primary spec

`form-143-file-format.xlsx` is the Income Tax Department's prescribed format, version 1.0,
TY 2026-27 onwards. It was obtained as the *optional* item and settled more than the blocker
was expected to.

### ✅ OUR TWO SHIPPED TCS SECTIONS NOW HAVE VERIFIED FOUR-DIGIT CODES

From **Annexure 2** ("Nature of Payment | Section | Section code to be used in the return"):

| Our internal section | Nature of payment (spec wording) | 2025-Act section | **Code** |
|---|---|---|---|
| `206C(1)-SCRAP` | Sale of Scrap | 394(1) [Table: Sl. No. 4] | **1073** |
| `206C(1F)-MV` | Sale consideration exceeding threshold limit in case of sale of motor vehicle | 394(1) [Table: Sl. No. 6.D(a)] | **1075** |

⚠️ **NOTE THE NAMING DRIFT.** Our identifiers still use the repealed 1961-Act nomenclature
(`206C(1)`, `206C(1F)`) while the return keys off **s.394(1) Table Sl. Nos.** This is the same
trap `HANDOFF-CLOSING-THE-GAP.md` §3.3 records for TDS — "the old 194-series does not exist
under the 2025 Act". The codes above are the bridge; the internal names should migrate with the
T6 work, not before, so the two are changed in one reviewable step.

Other codes in the annexure, recorded so nobody re-derives them: liquor 1068, tendu leaves
1069, timber under a forest lease 1070, timber otherwise 1071, other forest produce 1072,
minerals (coal/lignite/iron ore) 1074, wrist watch 1076, art 1077, collectibles 1078, yacht
etc. 1079, sunglasses 1080, bags 1081, shoes 1082, sportswear 1083, home theatre 1084, horses
1085, parking lot 1090, toll plaza 1091, mine or quarry 1092.

### ✅ THE FILE'S PHYSICAL FORMAT

Recorded because it is likely — but **NOT PROVEN** — to be shared with Form 140, which is the
same family at the same version and date. **Do not build Form 140 against this; get its own
spec.** The codes certainly differ (TCS occupies 1068–1092).

- **ASCII**, extension `.txt`. Every record including the last ends with a newline; line
  terminator is **CRLF** (hex `0D 0A`).
- **`^`-delimited, VARIABLE field width.** No leading zeros for numerics, no trailing spaces
  for character fields. The delimiter count in a record must be exactly *fields − 1*.
- Constant values (`Record Type`, `Upload Type`, …) in **BLOCK CAPITALS**.
- **Amounts: 2 decimals** (`1000.50`). **Rate of deduction: 4 decimals** (`2` is written
  `2.0000`) — which matches this codebase's fixed-4 money convention.
- **Dates `ddmmyyyy`**, and **no date may be in the future**.
- Record types: **`FH`** file header, **`BH`** batch header, **`CD`** challan detail,
  **`DD`** collectee/deductee detail. File type `TC1` for FN 143; uploader type `D`;
  upload type `R` for a regular statement.
- A **statement with zero collectee records will not validate** — the FVU rejects it.

### ⚠️ TWO THINGS THAT CONTRADICT EARLIER SECONDARY CLAIMS

1. **The lower/non-collection remark flags are `A`, `B`, `F`, `K`, `Y`, `Z`** — not "A through
   D". A nil challan or transfer voucher must carry a collectee record flagged with one of
   them. (Form 140's own set still needs its own spec; do not copy these across.)
2. **A `.csi` file download is MANDATORY** to validate challan details, and the TAN and TAN
   name in the statement must match the `.csi`. That is an **external dependency at validation
   time** which nothing in this build has modelled, and it is fetched from the TIN website
   (Challan Status Inquiry) — i.e. unreachable from here.

Also newly visible: `Certificate number under section 395(3)` (CHAR 15, conditional) and a
`s.394(5)` "is the collectee's payment liable to TDS" flag. The s.395 certificate matches the
identifier Form 140 also names, so it is needed by both returns.

---

## EPFO — not files, but findings (`HANDOFF-CLOSING-THE-GAP.md` §5 item 8)

No PDF could be obtained (`egazette.gov.in` presents a certificate chain automated fetchers
reject; a browser is fine). What follows was established from secondary reporting and is
recorded here because `internal/payroll/rates.go` cites it.

**The three scheme notifications, all dated 29 June 2026:**

| Scheme | Notification | Gazette No. | e-Gazette ID | Effective |
|---|---|---|---|---|
| EPF Scheme 2026 | G.S.R. 525(E) | 473, Pt II §3(i) | `CG-DL-E-01072026-273957` | 1 Jul 2026 |
| EDLI Scheme 2026 | G.S.R. 526(E) | 474, Pt II §3(i) | `CG-DL-E-30062026-273942` | 30 Jun 2026 |
| EPS 2026 | G.S.R. 527(E) | 475, Pt II §3(i) | — | 30 Jun 2026 |

**The RATE notifications are separate, and this is the trap.** The EDLI Scheme's own para
5(1)–(2) says the contribution rate is "to be notified separately ... **not fixed in this
Scheme's text**". Three rate notifications were issued **1 July 2026**:

| Notification | Scheme | Rate | Effective |
|---|---|---|---|
| **S.O. 3580(E)** | EPS 2026 | 8.33% | 29.06.2026 |
| **S.O. 3581(E)** | **EDLI 2026** | **0.5%** | **29.06.2026** |
| **S.O. 3582(E)** | EPF 2026 | 12% | deemed 21.11.2025 |

A sequential same-day block, one per scheme. 3580 and 3582 were confirmed independently of
3581, which brackets it; the EPS and EPF effective dates independently match what
`rates.go` already held. EDLI is ceiling-capped at ₹15,000/month → **₹75 per member per
month maximum**.

**Administrative charges: NO standalone rate notification exists under the 2026 Schemes as at
2026-07-30.** Two things must not be substituted for it:

- **S.O. 2701(E) of 29 May 2026 is INSPECTION charges**, under s.143(6), for **exempted
  establishments only** (EPF 0.35%, min ₹8,750; EDLI 0.005%, min ₹1,250). It is not an
  administrative charge and must not be read as one — this is the likeliest way a wrong number
  gets booked.
- The superseded 1952/1976 figures (0.50%, ₹500 minimum, EDLI admin waived) do not survive the
  schemes they were granted under.

**The charges are PENDING, not abolished**, and the 2026 EPF Scheme proves it: para 29(2)
introduces a ₹500/day late-return fee capped at "a maximum equal to the **administrative
charges payable by the employer for the relevant month**" — a cap that presupposes the charge
exists. So the zero in `rates.go` must stay a refusal and must never harden into a settled nil.

**The FVU cannot be run in this environment, and the GSTN portal, the NIC e-Way Bill API and
the EPFO/ESIC portals cannot be reached from it. Never claim a validation that was not
performed.**
