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
| `form-140-file-format.xlsx` | ✅ **here** (2026-07-31) | Protean/TIN, "File Format for Form Number 140 (26Q) Q1 to Q4 Version 1.0" | **THE BLOCKER, CLEARED.** Annexure 2's payment codes, the annexure field list, and Annexure 6's reason codes. See below. |
| `form-143-file-format.xlsx` | ✅ **here** (2026-07-31) | Protean/TIN, "File Format for Form Number 143 (27EQ) Q1 to Q4 Version 1.0" | The **TCS** return. Delivered more than expected — see the verified collection codes below. |
| `msmed-act-2006.pdf` | ✅ **here** (2026-08-29) — **India Code, text-native, downloaded by a human.** See "MSMED Act" below | `https://www.indiacode.nic.in/bitstream/123456789/2013/3/A2006-27.pdf` — India Code, the canonical portal | **T13's statutory half, except the numbers.** ss.15, 16, 23, 24 and the s.2(b) "appointed day" definition, all as enacted. ⛔ **It does NOT settle who is a micro/small enterprise** (s.7's thresholds are the 2006 originals and are superseded), and it says nothing about the RBI Bank Rate |

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
(`resource.cdn.icai.org`). See `docs/design/t6-model.md` §9. **The remaining gap is the FILE
FORMAT, not the statute and no longer the form.**

> ⛔ **CORRECTED 2026-07-31.** This paragraph used to read "All eight shipped TDS/TCS rates
> match it". **It was seven of eight**, and the sentence was measuring against the wrong
> edition. `ita-2025-as-amended-fa-2026.pdf` — the file this very table describes as "the
> authority to verify §9 against" — has now been read, and s.394(1) [Table: Sl. No. 4]
> carries footnote 88: *"Substituted for '1%' by the Finance Act, 2026, **w.e.f. 1-4-2026**"*.
> **Scrap TCS is 2%.** The engine shipped 1% and under-collected by half on every scrap sale
> from 1 April 2026. Corrected in `internal/compliance/tcs.go`; full working in
> `docs/design/t6-gaps-model.md` §0.
>
> The six TDS rates and `206C(1F)-MV` were re-checked against the amended text, carry no
> FA-2026 footnote, and stand. **A rate verified against "the Act" is only as current as the
> edition that was read — record the edition, and re-read on every Finance Act.**

---

## Form 140 (ex-26Q) — the TDS return. **THE BLOCKER IS CLEARED.**

`form-140-file-format.xlsx`, prescribed format version 1.0, TY 2026-27 onwards.

### ✅ THE SEVEN PAYMENT CODES — ALL CONFIRMED, from Annexure 2

`HANDOFF-CLOSING-THE-GAP.md` §1.1 recorded these as **untrusted** because the secondary source
carrying them got thresholds wrong. **The primary spec confirms all seven.**

| Nature of payment | 2025-Act section | **Code** |
|---|---|---|
| Contractor — individual/HUF | 393(1) [Table: Sl. No. 6(i).D(a)] | **1023** |
| Contractor — other than individual/HUF | 393(1) [Table: Sl. No. 6(i).D(b)] | **1024** |
| Technical services / cinematographic-film royalty / call-centre operator | 393(1) [Table: Sl. No. 6(iii).D(a)] | **1026** |
| Professional services, or a sum under s.26(2)(h) | 393(1) [Table: Sl. No. 6(iii).D(b)] | **1027** |
| Commission or brokerage — **others** | 393(1) [Table: Sl. No. 1(ii)] | **1006** |
| Rent — **machinery** etc., specified person | 393(1) [Table: Sl. No. 2(ii).D(a)] | **1008** |
| Rent — **other than machinery**, specified person | 393(1) [Table: Sl. No. 2(ii).D(b)] | **1009** |

**THE LIST BEING RIGHT DOES NOT MEAN THE REFUSAL WAS WASTED.** It means the process worked: the
codes could not be *known* to be right, and the same source demonstrably got thresholds wrong.
Had one code been off, an entire quarter's return would have been filed against the wrong
section. Do not read this outcome as licence to trust the next secondary list.

**Two codes worth having that were not on the ask list:**

- **Directors' fees = `1028`**, at 393(1) [Table: Sl. No. 6(iii).D(b)] — the §2.3 gap 2 row.
  **Note it shares its statutory limb with professional services (`1027`) but has its own
  return code.** Same Table Sl. No., different code. An engine that derived the code from the
  section alone would file directors' fees as professional fees.
- **Commission/brokerage — INSURANCE = `1005`**, at Sl. No. 1(i), distinct from others (`1006`).

Other codes recorded so nobody re-derives them: accumulated PF balance 1004, s.67(14) agreement
1011, compensation on compulsory acquisition 1012, mutual-fund/business-trust income 1013–1018,
interest on securities 1019, interest other than securities (senior citizen) 1020 / (other)
1021 / 1022, dividend 1029, life-insurance sum 1030, purchase of goods 1031, business
benefit/perquisite 1033 (in kind, tax pre-paid: 1034), e-commerce operator 1035, virtual digital
asset 1037/1038, lottery & game winnings 1059, online-game winnings 1060/1061, horse-race
winnings 1062, lottery-ticket commission 1063, cash withdrawals 1064/1065, s.80CCA(2)(a) 1066,
**payment to a partner of a firm 1067**.

### ✅ ANNEXURE 6 — the reason codes. **THIRTEEN FLAGS, NOT "A THROUGH D".**

Field 32 of the deductee-detail record, `CHAR 1`, optional: *"Reason for non-deduction / Lower
deduction / Higher deduction / Threshold / Transporter etc."* Annexure 6 is a **flag ×
section-code validity matrix** — a flag is legal only against the section codes listed for it,
which is a validation rule and not merely a lookup.

| Flag | Meaning |
|---|---|
| **A** | Lower or no deduction on a **s.395(1) certificate** |
| **B** | No deduction on a **s.393(6) declaration** |
| **C** | **Higher rate under s.397(2)** for non-furnishing of PAN |
| **D** | No/lower deduction per notification under s.194A(5) of the 1961 Act or s.400(1) of the 2025 Act |
| **E** | Payment to a person in Board Circulars 3/2002, 11/2002 or 18/2017 |
| **N** | Payment to a person in s.393(4) [Sl. No. 18], or the fifth proviso to s.194N of the 1961 Act |
| **O** | No deduction per s.393(4) [Sl. No. 5] |
| **P** | Dividend to a **business trust**, s.393(4) [Sl. No. 10(d)/(e)] |
| **Q** | Payment to an entity in s.393(4) [Sl. No. 7(c)(v)] |
| **S** | Tax deductible/collectible under another provision — s.393(1) [Sl. No. 8(ii)] Note 1(a) |
| **T** | Deductee is a **transporter**; **PAN is mandatory** — s.393(4) [Sl. No. 8] |
| **Y** | **Payment below the threshold limit** |
| **Z** | No/lower deduction per notification under **s.400(1)** |

**`Y` is the one the engine will use most** — it is how a sub-threshold payment is reported, and
it is why §2.3's dual contractor threshold matters for the RETURN and not only for the
deduction: a payment under ₹30,000 that crosses the ₹1,00,000 aggregate is no longer a `Y`.

⚠️ **Annexure 6's own header reads "Code in FN 139"**, which appears to be a clerical error in
the spec — this is the Form 140 file format and its Annexure 2 codes are the ones tabulated
against the flags. Recorded rather than silently corrected.

### The physical format

**Identical in kind to Form 143's** (see below) — ASCII `.txt`, `^`-delimited variable width,
CRLF, delimiter count = fields − 1, block-capital constants, amounts 2dp, **rate 4dp**, dates
`ddmmyyyy` and never future, record types `FH`/`BH`/`CD`/`DD`. Form 140 additionally uses
**Annexure 4** (deductor category), **Annexure 5** (state codes), **Annexure 3** (ministry) and
**Annexure 7** (challan **minor head**).

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


| `rbi-bank-rate-2022-08-05.pdf` | ✅ **here** (2026-08-30) | RBI/2022-23/103, `DOR.RET.REC.59/12.01.001/2022-23` | Bank Rate **5.15% → 5.65%** w.e.f. 5 Aug 2022 |
| `rbi-bank-rate-2023-02-08.pdf` | ✅ **here** (2026-08-30) | RBI/2022-23/174, `DOR.RET.REC.101/12.01.001/2022-23` | Bank Rate **6.50% → 6.75%** w.e.f. 8 Feb 2023 |
| `rbi-bank-rate-2025-04-09.pdf` | ✅ **here** (2026-08-30) | RBI/2025-26/23, `DoR.RET.REC.16/12.01.001/2025-26` | Bank Rate **6.50% → 6.25%** w.e.f. 9 Apr 2025 |
| `rbi-bank-rate-2025-06-06.pdf` | ✅ **here** (2026-08-30) | RBI/2025-26/45, `DoR.RET.REC.22/12.01.001/2025-26` | Bank Rate **6.25% → 5.75%** w.e.f. 6 Jun 2025 |

---

## ⭐⭐ RBI BANK RATE — THE NOTIFICATIONS FORM A CHAIN, AND THAT IS THE WHOLE POINT

**T13 (`docs/design/t13-interest-model.md` §6) needs a DATED SERIES**, because MSMED s.16 prices
interest at three times the Bank Rate and an accrual period spanning a change must be split at the
change's effective date. A table of rates is not enough; the series must also be **provably
complete**, because a MISSING change is wrong only for bills inside the gap and correct everywhere
else — it survives any spot-check that lands outside the window.

⭐ **RBI's notifications solve that, and not by accident.** Every one opens by citing its
IMMEDIATE PREDECESSOR, with both the reference number and the exact date:

```
$ for f in docs/statutory/rbi-bank-rate-*.pdf; do pdftotext -layout "$f" - \
    | tr -s ' \n' ' \n' | /usr/bin/grep -oE "(DOR|DoR)\.RET\.REC\.[0-9]+/12\.01\.001/[0-9-]+ dated [A-Z][a-z]+ [0-9]{2}, [0-9]{4}"; done

2022-08-05  ->  DOR.RET.REC.44/12.01.001/2022-23  dated June 08, 2022
2023-02-08  ->  DOR.RET.REC.88/12.01.001/2022-23  dated December 07, 2022
2025-04-09  ->  DoR.RET.REC.57/12.01.001/2024-25  dated February 07, 2025
2025-06-06  ->  DoR.RET.REC.16/12.01.001/2025-26  dated April 09, 2025
```

⛔ **SO THE SERIES IS A LINKED LIST AND A GAP BREAKS IT.** Walking the chain backwards from the
current rate does not merely *assemble* the series — it **proves there is nothing missing**, because
a change that had been skipped would leave a notification whose named predecessor is absent. No
table can offer that, and the table this project was originally given was in fact missing two
changes.

### ✅ WHAT THE FOUR COMMITTED FILES ALREADY ESTABLISH

**Four rate changes, quoted from RBI, plus THREE MORE dates and reference numbers** recovered from
the predecessor citations without needing the documents themselves:

| Effective | Reference | Bank Rate becomes | How known |
|---|---|---|---|
| **8 Jun 2022** | `DOR.RET.REC.44/…/2022-23` | 5.15% | ⭐ named by the 5 Aug 2022 notification, which revises *"from 5.15 per cent"* |
| 5 Aug 2022 | `DOR.RET.REC.59` | **5.65%** | ✅ committed |
| **7 Dec 2022** | `DOR.RET.REC.88/…/2022-23` | 6.50% | ⭐ named by the 8 Feb 2023 notification, which revises *"from 6.50 per cent"* |
| 8 Feb 2023 | `DOR.RET.REC.101` | **6.75%** | ✅ committed |
| **7 Feb 2025** | `DoR.RET.REC.57/…/2024-25` | 6.50% | ⭐ named by the 9 Apr 2025 notification, which revises *"from 6.50 per cent"* |
| 9 Apr 2025 | `DoR.RET.REC.16` | **6.25%** | ✅ committed |
| 6 Jun 2025 | `DoR.RET.REC.22` | **5.75%** | ✅ committed |

⭐ **NOTE HOW THE TWO ORIGINALLY-MISSING CHANGES WERE CAUGHT.** 8 June 2022 and 9 April 2025 were
both absent from the table this project was handed. Each is now proved twice over: by the *"from X
per cent"* wording of the notification that follows it, and by that notification naming it as the
predecessor circular. **Neither was inferred.**

### ⛔⛔ THREE OF THE FOUR CARRY A "WITHDRAWN" WATERMARK — AND IT DOES NOT MEAN WHAT IT LOOKS LIKE

```
$ for f in docs/statutory/rbi-bank-rate-*.pdf; do
    printf "%-30s " "$(basename $f)"; strings "$f" | /usr/bin/grep -icm1 withdrawn; done
rbi-bank-rate-2022-08-05.pdf   1        <- WITHDRAWN
rbi-bank-rate-2023-02-08.pdf   1        <- WITHDRAWN
rbi-bank-rate-2025-04-09.pdf   1        <- WITHDRAWN
rbi-bank-rate-2025-06-06.pdf   0
```

⚠️ **IT EXTRACTS AS SCATTERED LETTERS, NOT AS A WORD.** The stamp is a diagonal overlay, so
`pdftotext` yields `W` / `it` / `hdr` / `aw` / `n` on separate lines and a grep for "withdrawn"
finds **nothing**. It was noticed only because the letters interrupted a sentence mid-word
(*"…to 6.25 per cent with**n aw ithdr W**immediate effect"*). ⛔ **A `grep -i withdrawn` over the
text layer is a FALSE NEGATIVE here; use `strings` on the file.**

⭐ **WHAT IT MEANS, AND WHY THESE FILES ARE STILL THE RIGHT SOURCES.** RBI withdraws a circular when
its substance is folded into a Master Direction — the April 2025 circular points at exactly that:
*"Please refer to Chapter VIII of Master Direction – Reserve Bank of India [Cash Reserve Ratio (CRR)
and Statutory Liquidity Ratio (SLR)] Directions – 2021"*. **Withdrawal ends the circular's life as
an operative INSTRUCTION. It does not unmake the historical fact** that the Bank Rate changed on
that date to that figure, which is the only thing T13 needs from it.

⛔ **BUT DO NOT CITE A WITHDRAWN CIRCULAR AS CURRENT AUTHORITY.** For "what is the Bank Rate today",
the live instrument is the Master Direction (and the most recent, unwithdrawn circular). For "what
was the Bank Rate on 12 September 2022", these files are exactly right. The distinction has to be
written down because the two questions look identical and only one of them is T13's.

### ⭐ THE FEBRUARY 2025 CIRCULAR EXISTS — IT IS JUST TITLED DIFFERENTLY

It was reported as "not findable" when browsing by month. It is at
**`NotificationUser.aspx?Id=12776`** — RBI/2024-25/111, `DoR.RET.REC.57/12.01.001/2024-25`,
**7 February 2025, Bank Rate 6.75% → 6.50%**.

⚠️ **THE TITLE IS THE SHORT FORM, `"Change in Bank Rate"`** — not the longer *"Penal Interest on
shortfall in CRR and SLR requirements-Change in Bank Rate"* that the 2025-26 circulars use. **Both
spellings are in use and a search for the long one alone misses the short one.** Search for
`Bank Rate` and nothing more.

⭐⭐ **AND IT CLOSES THE LARGEST GAP IN THE SERIES.** It revises *"from 6.75 per cent"* — the figure
set by the 8 February 2023 circular. **So the Bank Rate did not move at all between 8 Feb 2023 and
7 Feb 2025**, and that two-year hold is now proved by the chain rather than assumed from silence.

### ✅ EVERY CIRCULAR LOCATED, AND THE CHAIN IS UNBROKEN

All ten Bank Rate changes from 4 May 2022 to date now have a notification id, found by fetching
`rbi.org.in` directly. ⭐ **AND THE CHAIN CLOSES: every circular's "from" equals its predecessor's
"to", with no gaps.**

```
4.25 -> 4.65 -> 5.15 -> 5.65 -> 6.15 -> 6.50 -> 6.75 -> 6.50 -> 6.25 -> 5.75 -> 5.50
```

| Effective | Bank Rate | Reference | `NotificationUser.aspx?Id=` |
|---|---|---|---|
| 4 May 2022 | 4.25 → **4.65%** | RBI/2022-23/45, `DOR.RET.REC.32` | **12312** |
| 8 Jun 2022 | 4.65 → **5.15%** | RBI/2022-23/65, `DOR.RET.REC.44` | **12333** |
| 5 Aug 2022 | 5.15 → **5.65%** | RBI/2022-23/103, `DOR.RET.REC.59` | **12372** ✅ committed |
| 30 Sep 2022 | 5.65 → **6.15%** | RBI/2022-23/123, `DOR.RET.REC.70` | **12394** |
| 7 Dec 2022 | 6.15 → **6.50%** | RBI/2022-23/148, `DOR.RET.REC.88` | **12420** |
| 8 Feb 2023 | 6.50 → **6.75%** | RBI/2022-23/174, `DOR.RET.REC.101` | **12450** ✅ committed |
| 7 Feb 2025 | 6.75 → **6.50%** | RBI/2024-25/111, `DoR.RET.REC.57` | **12776** |
| 9 Apr 2025 | 6.50 → **6.25%** | RBI/2025-26/23, `DoR.RET.REC.16` | **12832** ✅ committed |
| 6 Jun 2025 | 6.25 → **5.75%** | RBI/2025-26/45, `DoR.RET.REC.22` | **12857** ✅ committed |
| 5 Dec 2025 | 5.75 → **5.50%** | RBI/2025-26/… | *(browse Dec 2025)* |

⚠️ **TWO CIRCULARS ISSUE ON EACH POLICY DAY AND ONLY ONE IS THE RIGHT ONE.** RBI publishes
*"Liquidity Adjustment Facility – Change in rates"* (FMOD, repo/SDF/MSF) and *"Change in Bank
Rate"* (DoR.RET.REC, the Bank Rate) on the same date, with adjacent ids. **Take the DoR.RET.REC
one.** The LAF circular gives the MSF rate, which happens to equal the Bank Rate — but it is a
different instrument answering a different question, and a series assembled from it would be
citing the wrong document.

### ⭐ WHAT THE CHAIN NOW PROVES, AND WHAT IT STILL DOES NOT

✅ **PROVED COMPLETE from 4 May 2022 to today.** No change can be missing: a skipped one would
leave a circular whose "from" did not match its predecessor's "to", and none does.

⛔ **NOT YET ESTABLISHED: the boundary before 4 May 2022.** That circular revises *"from 4.25 per
cent"*, so 4.25% was in force immediately before it — but when 4.25% BEGAN is unverified. It is
believed to be 22 May 2020, from secondary sources only.

⚠️ **THAT MATTERS ONLY FOR A BILL WHOSE INTEREST ACCRUES BEFORE 4 MAY 2022** — i.e. a receivable
more than four years overdue. If T13 is scoped to bills inside the chain, **the series is done**.
If not, the May 2020 circular (and any between it and May 2022) is still needed. **Record the
scope decision rather than letting the gap sit unnamed.**

---

## ⚠️ MSMED Act 2006 — what it settles, what it does not, and how it was obtained

**Obtained 2026-08-29.** `docs/design/t13-interest-model.md` §8 asked for this and rated it
*"NOT blocked by anything — ordinary public documents"*. That turned out to be half true, and the
half that was false is worth recording because the next person will hit it:

| Host | Result |
|---|---|
| `indiacode.nic.in` (the canonical portal) | ⛔ **403 to automated fetchers.** A human with a browser gets through — the same behaviour this README's opening paragraph records for `incometaxindia.gov.in` |
| `samadhaan.msme.gov.in` (the Ministry's delayed-payment portal) | ⛔ **Connection REFUSED from `164.100.158.186`.** ⚠️ That is the **same `164.100.x` NIC range** as the geo-fenced e-invoice hosts. **The geo-fence is broader than the IRP** — it is a property of NIC-hosted government services generally, and it is now known to block a second, unrelated document source |
| `legislative.gov.in` | ⛔ 403 |
| `wb.gov.in` | ⚠️ Downloads, but it is an **FAQ / guidance document**, not the Act |
| ✅ `dcmsme.gov.in` | **Downloads, and it is the whole Act, ss.1–32** |

### ✅ THE COMMITTED COPY IS INDIA CODE'S, AND IT WAS VERIFIED AGAINST A SECOND SOURCE

**The file here is India Code's `A2006-27.pdf`**, downloaded **by a human with a browser** on
2026-08-29 — the same workaround this README's opening paragraph prescribes for
`incometaxindia.gov.in`, applied to `indiacode.nic.in`, which 403s automated fetchers identically.
It is **text-native** (produced from Word, not scanned), so quotations can be taken from the text
layer directly.

⭐ **IT REPLACED AN OCR'd MINISTRY SCAN, AND THE TWO WERE DIFFED BEFORE THE SWAP.** The first copy
obtained was `https://www.dcmsme.gov.in/MSMED2006.pdf` (Development Commissioner, MSME) — a scan
whose text layer carried artifacts (`d2Y` for "day", `(I)` for `(1)`, `offacilitating`). Every
section quoted below was re-read against the India Code text **word for word** and **all of them
matched**, including s.7's thresholds. The scan was right; it is simply a worse artefact to keep.

⚠️ **NOTE FOR ANYONE RE-VERIFYING WITH `grep`.** Both documents wrap lines mid-phrase, so
`grep "three times"` returns **zero** against s.16 and `grep "twenty-five lakh"` returns **zero**
against s.7 — in each case the phrase is split across a line break (`at three` / `times of the bank
rate`; `twenty-` / `five lakh rupees`). **A zero here is a line-wrap artefact and not an absence.**
Searching for a distinctive single word is the reliable form.

### ✅ WHAT IT SETTLES — read from its own text

- **s.2(b) — "appointed day"**: *"the day following immediately after the expiry of the period of
  fifteen days from the day of acceptance or the day of deemed acceptance"*.
  ⭐ **THE FIFTEEN DAYS LIVES HERE, NOT IN s.15.** A note citing s.15 for it cites the wrong section.
  ⚠️ And its **Explanation** moves the anchor. It defines **two** starting facts, not one:
  - *"the day of acceptance"* — the day of actual delivery **or**, where the buyer objects **in
    writing within fifteen days** of delivery, *"the day on which such objection is removed by the
    supplier"*;
  - *"the day of deemed acceptance"* — where **no** written objection is made within fifteen days,
    *"the day of the actual delivery of goods or the rendering of services"*.

  ⛔ **SO THE CLOCK'S START IS NOT SIMPLY THE INVOICE DATE, AND IT IS NOT EVEN FIXED AT DELIVERY.**
  A written objection resets it to an event that happens later and that this product does not
  record anywhere. §5's anchor-date gap is therefore wider than §5 states.
- **s.15**: payment on or before the date agreed **in writing**, or *"where there is no agreement in
  this behalf, before the appointed day"*; proviso — the agreed period *"shall in no case exceed
  forty-five days from the day of acceptance or the day of deemed acceptance"*.
- **s.16**: *"compound interest with monthly rests … from the appointed day or, as the case may be,
  from the date immediately following the date agreed upon, at three times of the bank rate notified
  by the Reserve Bank"*. ⭐ **TWO ANCHORS, not one** — which of them applies depends on whether there
  was a written agreement.
- **s.23**: the interest *"shall not, for the purposes of computation of income under the Income-tax
  Act, 1961, be allowed as deduction"*.
- **s.24 — the override, now PINNED**: *"The provisions of sections 15 to 23 shall have effect
  notwithstanding anything inconsistent therewith contained in any other law for the time being in
  force."* The T13 memo guessed "s.24 or equivalent"; it is s.24.

### ⛔ WHAT IT DOES *NOT* SETTLE — and the second one is a trap

1. **THE RBI BANK RATE.** Not in the Act and never will be. s.16 is an arithmetic instruction over a
   dated series that this repository still does not have. **T13 remains blocked on it.**
2. ⛔⛔ **WHO IS A MICRO OR SMALL ENTERPRISE. s.7's THRESHOLDS ARE THE 2006 ORIGINALS AND ARE
   SUPERSEDED.** The Act as enacted reads *"a micro enterprise, where the investment in plant and
   machinery does not exceed twenty-five lakh rupees"*. Those figures were replaced in 2020 by
   composite investment-**and**-turnover criteria and revised again since. **Using the Act's own
   numbers would misclassify almost every real enterprise**, and it would do so in the flattering
   direction — nearly everybody would fall outside micro/small, so the MSMED regime would silently
   never apply and no interest would ever be computed under it.

   ⭐ **s.7(1) IS THE PART THAT MATTERS, AND IT IS STILL GOOD LAW**: the Central Government *"may,
   for the purposes of this Act, **by notification** … classify"*. So classification is delegated,
   exactly as the EPF administrative charges are — the Act supplies the mechanism and a separate
   notification supplies the numbers.

   ⚠️ **AND THE PRODUCT PROBABLY SHOULD NOT COMPUTE THIS AT ALL.** It has no investment or turnover
   figure for a counterparty and must not invent one. What a supplier actually presents is a **Udyam
   Registration Number** and a declared class. **Record the declaration; do not derive the
   classification.** That is a design decision for `t13-interest-model.md` §3.2, which currently only
   records that the flag does not exist.
