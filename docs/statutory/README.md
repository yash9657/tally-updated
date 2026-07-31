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
| `fvu-file-format.pdf` | ❌ **STILL MISSING** | Protean/TIN or `incometax.gov.in` — "File Format for Quarterly Return", tax year 2026-27 | Column order, **field widths**, the **four-digit payment codes**, and the non/lower/higher-deduction reason codes. **`docs/design/t6-model.md` invariant I-7 still refuses to emit any return file.** |

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
