# T13 — interest on overdue bills: the model

**Written:** 2026-08-27, at `fc7b9d9`, before any T13 code — and, deliberately, before the
statutory documents §8 asks for have landed.
**Precedent:** `t8-multi-currency-model.md` — a design document first, because the questions are
answerable in writing and answering them wrongly costs a migration. §10 borrows
`t4-slice2-model.md` §7's "what this memo does not settle".

⭐ **Every claim below has the command that produced it beside it, and every command reproduces as
printed.** Absences were produced with `/usr/bin/grep`, per `HANDOFF-NEXT-SESSION.md:222` item 9
and `.claude/HANDOFF.md:346` item 20 — the shell's `grep` is a `ugrep` shim that returns **silently
empty** on files it judges binary, so an absence "proved" with bare `grep` proves nothing.
(`t8-multi-currency-model.md:8` cites the same rule as "`HANDOFF.md` §9"; the numbered item lives
at the two references above.)

⛔ **NOBODY HAS DECIDED TO BUILD T13.** This note exists so that the build is *startable on the
day the primary sources arrive*, and so that the wait is spent on design rather than on
discovering the design after the schema is poured.

---

## 0. THE HEADLINE, BEFORE ANYTHING ELSE

### 0.1 ⛔⛔ THE PLAN LINE IS WRONG, AND IT IS WRONG IN BOTH DIRECTIONS AT ONCE

```
$ /usr/bin/grep -n "T13" PLAN-TALLY-PARITY.md
  154:> T8 0050–0051, T9 0052, T10 0053–0054, T11 0055–0056, T12 0057, T13 0058, T14 0059). Those
  178:| T13 Interest | see §2.5 | free |
  477:| **T13** | **Interest calculation** | On overdue receivables/payables. Simple vs compound, and the day-count basis, are configuration with statutory defaults — cite them. | see §2.5 |
  497:9. **T11 Budgets** → **T12 GSTR-9** → **T13 Interest** → **T14 Backup/restore.**
```

`PLAN-TALLY-PARITY.md:477` — **"Simple vs compound, and the day-count basis, are configuration
with statutory defaults"** — reads as ONE global setting with a sensible pre-filled value. There
is no such setting, because **there is no single answer.** Interest on an overdue Indian
receivable follows one of two regimes, and which one applies is a property of **the
counterparty**, not of the company:

| | **(a) The supplier is a micro or small enterprise** | **(b) Everybody else** |
|---|---|---|
| Source of the rule | **MSMED Act 2006, ss.15–16** | The invoice / the contract |
| Rate | **Three times the RBI Bank Rate** — fixed by statute | Whatever was agreed |
| Rests | **Compound, MONTHLY rests** — fixed by statute | Whatever was agreed |
| Runs from | The **appointed day** (s.15) | Whatever was agreed |
| Can the operator change it? | ⛔ **NO.** ss.15–23 override any contrary contract term | ✅ Yes — it is the only input |
| Statutory default if nothing agreed? | Not applicable — the statute *is* the term | ⛔ **NONE EXISTS** |

⭐ **A single global toggle is therefore wrong in BOTH directions simultaneously:**

1. It is **overridable where the law forbids override.** A UI control that lets an operator set
   "simple interest, 12%" against an MSE supplier's overdue bill produces a number the MSMED Act
   says is not the number, and a contract clause to that effect is void anyway. Exposing the knob
   *implies the knob is legitimate*.
2. It is **defaulted where nothing authorises a default.** For a non-MSE party there is no
   statutory rate, no statutory day-count and no statutory compounding. Any pre-filled value is
   this build's invention presented as law. `FeaturesScreen.tsx:143` already says the quiet part
   out loud — *"inventing those defaults would be worse than the gap"* — and then the plan line
   asks for exactly that invention.

**So the one-line correction to `PLAN-TALLY-PARITY.md:477` is:**

> Simple-vs-compound and the day-count basis are **resolved per party, under one of two regimes**.
> For a micro/small-enterprise supplier the MSMED Act fixes them and they are **not configurable**.
> For everyone else they are **configuration with NO default** — an unconfigured party accrues
> nothing, and says so.

⚠️ **AND THE RBI BANK RATE IS NOT THE REPO RATE.** They are different instruments carrying
different numbers and are conflated constantly, including by calculator websites. §8 says where
the number must come from.

### 0.2 THE PROVENANCE OF THIS NOTE'S OWN STATUTORY CLAIMS — SECOND-HAND, AND MARKED

```
$ /usr/bin/grep -c -i "interest" RESEARCH.md
2
$ /usr/bin/grep -n -o -i ".\{0,28\}interest.\{0,24\}" RESEARCH.md
706: (exempt/nil/non-GST), plus interest/late fee. Both reports
1981:nil/non-GST inward; **5.1** interest & late fee. Offline uti
```

**Two hits, both GST late-fee, neither about a receivables-interest feature.** `RESEARCH.md` pins
**nothing** about how TallyPrime shapes this. ⛔ **Everything in this note about the SHAPE of the
feature is therefore a DESIGN DECISION taken here, labelled as such — not parity recalled from
Tally.** That is the same discipline `t8-multi-currency-model.md` §0.1 applied.

⛔⛔ **AND THE STATUTORY CONTENT OF §1 IS ITSELF SECOND-HAND.** It comes from
`HANDOFF-NEXT-UNBLOCKED.md:137` and the task brief, **not from primary text**:

```
$ ls -1 docs/statutory/
form-140-faqs.pdf
form-140-file-format.xlsx
form-140.pdf
form-143-file-format.xlsx
ita-2025-as-amended-fa-2026.pdf
README.md
```

**No MSMED file. No RBI notification.** Every statutory assertion in §1 is therefore marked
**PENDING PRIMARY VERIFICATION** and must be re-read against the gazette before a line of engine
code is written. A design note that warned against uncited numbers while resting on uncited
numbers would be the very thing it warns against — so it says so here, at the top.

### 0.3 ⛔ THE SAME WRONG SENTENCE EXISTS A SECOND TIME, IN THE SHIPPED UI

```
$ /usr/bin/grep -c "day-count basis" frontend/src/screens/FeaturesScreen.tsx
1
$ /usr/bin/grep -n -o "The day-count basis.\{0,86\}" frontend/src/screens/FeaturesScreen.tsx
143:The day-count basis and simple-vs-compound are configuration with statutory defaults, and inventing those
```

`frontend/src/screens/FeaturesScreen.tsx:141-143` carries the identical claim. **That file is
owned by another lane in this sequence and this note's author must not touch it** (it is shared
with T8 multi-currency), so the correction is recorded here as a named follow-up rather than left
as a silent inconsistency between the shipped UI and this design.

⭐ **The replacement sentence the owning lane should paste**, preserving the row's honesty while
removing the false premise:

> "…Outstandings will tell you a bill is 90 days old; it will not tell you what that has cost.
> **There is no single answer to paste in, either: a micro/small-enterprise supplier's interest is
> FIXED by the MSMED Act 2006 (compound, monthly rests, three times the RBI Bank Rate) and is not
> ours to configure, while for every other party no statutory rate exists at all and a default
> would be invented.** Building one global toggle would be wrong in both directions — see
> `docs/design/t13-interest-model.md`."

⚠️ Until that lands, the shipped UI and this note disagree. Recorded, not hidden.

---

## 1. REGIME (a) — THE MSMED ACT. WHAT IS FIXED, AND THEREFORE WHAT MUST NOT BE A UI CONTROL

⭐⭐ **VERIFIED 2026-08-29 AGAINST THE ACT'S OWN TEXT, FROM TWO INDEPENDENT COPIES.**
`docs/statutory/msmed-act-2006.pdf` is now **India Code's `A2006-27.pdf`** — text-native, downloaded
by a human with a browser (India Code 403s automated fetchers). It replaced an OCR'd Ministry scan
from `dcmsme.gov.in`, and **every quotation below was diffed word for word between the two before
the swap; all matched.** §0.2's "PENDING PRIMARY VERIFICATION" is **DISCHARGED for the mechanics
below.**

⚠️ It still stands for the two things the Act does not carry: the **RBI Bank Rate series** (§6) and
the **current micro/small thresholds** (§1.2). Neither is in this Act and neither ever will be.

| Element | What the statute fixes | Consequence for the build |
|---|---|---|
| **s.15 — when payment is due** | *"payment therefor on or before the date agreed upon between him and the supplier **in writing** or, where there is no agreement in this behalf, before the appointed day"*; proviso — the agreed period *"shall in no case exceed forty-five days from the day of acceptance or the day of deemed acceptance"*. | ⛔ **TWO CASES, NOT ONE.** A written agreement (capped at 45 days) or, failing that, the appointed day. |
| **s.2(b) — "appointed day"** ⭐ **THE 15 DAYS LIVES HERE, NOT IN s.15** | *"the day following immediately after the expiry of the period of **fifteen days** from the day of acceptance or the day of deemed acceptance"*. Its **Explanation** defines BOTH: *"the day of acceptance"* is actual delivery, **or** where the buyer objects in writing within fifteen days, *"the day on which such objection is removed by the supplier"*; *"the day of deemed acceptance"* is actual delivery where no such objection is made. | ⛔ **DEFAULTING AN UNAGREED BILL TO 45 DAYS STARTS THE CLOCK A MONTH LATE** on every one of them — understating in the debtor's favour, with every figure still footing. ⛔⛔ **AND THE ANCHOR IS NOT THE INVOICE DATE, NOR EVEN THE DELIVERY DATE**: a written objection moves it to the day the supplier resolves that objection — **an event this product records nowhere**. §5's anchor-date gap is therefore WIDER than §5 states, and closing it needs a new fact, not a new query. |
| **s.16 — the rate and the rests** | *"compound interest with monthly rests … from the appointed day or, as the case may be, from the date immediately following the date agreed upon, at three times of the bank rate notified by the Reserve Bank"*. | ⛔ Not a rate input, not a simple/compound toggle, not a day-count basis. **Months, not days.** ⭐ **AND TWO ANCHORS**: the appointed day when there was no written agreement, the day after the agreed date when there was. A single anchor is wrong for half of all bills. |
| **s.24 — the override** ⭐ **PINNED 2026-08-29; §8 no longer needs to** | *"The provisions of sections 15 to 23 shall have effect notwithstanding anything inconsistent therewith contained in any other law for the time being in force."* A clause reading "no interest on late payment" does not survive against an MSE supplier. | ⛔ **A per-party interest configuration must not be readable for a party flagged MSE.** The regime is selected, then the configuration is *ignored*, and the report must say it was ignored and why. |

⭐ **THE UI RULE THAT FOLLOWS.** For an MSE party the rate/rests/basis controls are not merely
pre-filled — they are **absent**, replaced by a statement of the regime and its citation. A
disabled input still reads as "this is the value, we just locked it"; an absent input reads as
"this is not a choice". The distinction matters because the number ends up on a demand letter.

⚠️ **"Three times the Bank Rate" is a MULTIPLE OF A MOVING NUMBER.** It is not a rate this build
gets to store. It is an arithmetic instruction over a dated series — which is §6.

### 1.2 ⛔⛔ WHO IS IN REGIME (a) — STILL OPEN, AND s.7's OWN NUMBERS ARE A TRAP

The Act reaches this note's mechanics but **not** its most basic precondition: which counterparties
regime (a) applies to at all.

**s.7(1) as enacted** classifies a micro manufacturing enterprise as one *"where the investment in
plant and machinery does not exceed twenty-five lakh rupees"*. ⛔ **THOSE FIGURES ARE THE 2006
ORIGINALS AND ARE SUPERSEDED** — replaced in 2020 by composite investment-**and**-turnover criteria
and revised since.

⛔ **AND THE FAILURE IS IN THE FLATTERING DIRECTION**, which is why it would survive review: judged
against 2006 thresholds almost every real enterprise falls *outside* micro/small, so regime (a)
would silently never fire, no MSMED interest would ever be computed, and **every screen would look
clean**. There is no error state to notice.

⭐ **WHAT IS STILL GOOD LAW IS s.7(1)'s MECHANISM**: the Central Government *"may, for the purposes
of this Act, **by notification** … classify"*. Classification is delegated — the same shape as the
EPF administrative charges blocker, where the Scheme fixes no rate and a separate notification does.

⭐⭐ **THE DESIGN CONSEQUENCE, AND IT SIMPLIFIES §3.2: DO NOT DERIVE THE CLASSIFICATION.** This
product holds no investment figure and no turnover figure for a counterparty, and must not invent
either. What an MSE supplier actually presents is a **Udyam Registration Number** and a declared
class. So the flag §3.2 says does not exist should store **a recorded declaration** — the Udyam
number, the class declared, and the date it was given — and the report should say *"treated as a
micro/small enterprise on the supplier's own Udyam declaration of <date>"*.

⛔ **A DECLARATION IS NOT A DERIVATION, AND THE REPORT MUST NOT DRESS IT AS ONE.** The buyer is
relying on what the supplier told them, which is exactly what s.22's annual-accounts disclosure also
rests on. Recording the source of the belief is what makes the figure defensible when a debtor asks.

---

### 1.1 ⭐ THREE THINGS ss.15–23 CARRY THAT THIS NOTE DID NOT KNOW ABOUT

**Added 2026-08-28** from a section-by-section summary supplied by the project owner.
⭐ **SUPERSEDED 2026-08-29 BY THE ACT ITSELF** (`docs/statutory/msmed-act-2006.pdf`); kept because it
is what prompted the s.15 correction, and because §1.1(a) records what the summary got wrong.
⚠️ Originally marked second-hand — see §0.2. It is a *second independent* second-hand source rather than a
primary one: it corroborates s.15's 45 days and s.16's compound/monthly-rests/three-times-Bank-Rate
against the brief this note was written from, which is worth something, and it is **not** the
gazette text §8 asks for. Everything here remains **PENDING PRIMARY VERIFICATION**.

**(a) ⛔ THE 15-DAY FALLBACK — a correction, not an addition.** This note had only "the day agreed
… not later than 45 days". ⭐ **This is the kind of error that would never have surfaced in
testing**: 45 days is a real number from the real section, so a fixture built on it passes, and the
only symptom is that interest on every unagreed bill starts a month late.

⚠️ **AND THE SUMMARY ATTRIBUTED IT TO THE WRONG SECTION**, which the Act text (obtained the next
day) settled: s.15 says *"before the appointed day"* and never mentions fifteen days. **The fifteen
days is the s.2(b) DEFINITION of "appointed day"**, and s.2(b)'s Explanation additionally moves the
anchor when the buyer objects in writing. Both are in §1's table above. A note that had cited s.15
for the 15 days would have cited a section that does not contain it — the softer version of the
mistake this whole file exists to prevent.

**(b) ⭐ s.22 — AN ANNUAL-ACCOUNTS DISCLOSURE, WHICH IS A REPORTING FEATURE NOBODY HAS SCOPED.**
Companies must disclose unpaid MSME dues **and the related interest** in their annual financial
statements. That is not part of an interest *calculator*; it is a note on the Balance Sheet, and it
needs the same MSME party flag §3.2 says does not exist yet. **It is out of scope for T13 and should
be tracked separately — but it shares T13's blocker**, so whoever adds the flag should know both
consumers exist before choosing where it lives.

**(c) ⛔⛔ s.23 — THE INTEREST IS NOT TAX-DEDUCTIBLE, AND THIS ONE CHANGES §7.**
Interest paid or payable on a delayed MSME payment **is disallowed as a deduction** to the buyer.

§7 decides that v1 **POSTS NOTHING** and only reports. ⭐ **s.23 makes that decision safer, not
less so** — an interest expense posted into the books without being marked disallowed would flow
into the tax computation as an ordinary deduction and understate the buyer's liability. So:

> ⛔ **IF A LATER WAVE EVER POSTS THIS INTEREST, THE POSTING MUST CARRY A "DISALLOWED UNDER s.23"
> MARKER FROM THE MOMENT IT IS WRITTEN**, not as a year-end adjustment somebody remembers. A
> deductible-looking expense that is not deductible is exactly the shape this repository's register
> is full of: it foots, it reconciles, and it is wrong in one direction only.

⚠️ **AND NOTE WHAT THE SUMMARY DOES *NOT* COVER**, so the §8 ask stays honest:

| Still missing | Why it matters |
|---|---|
| ⛔ **The overriding-effect provision** (s.24 or equivalent) | §1's table rests on it: it is what makes an MSE party's interest terms **unreadable** rather than merely pre-filled. The summary runs 15–23 and names no override |
| ⛔ **The definitions of micro / small** (s.7 and s.2, NOT 15–23) | They decide **who is in regime (a) at all**. Without them the flag in §3.2 has no rule behind it |
| ⛔ **The RBI Bank Rate dated series** | The other half of §8, and the harder one. "Three times the Bank Rate" is arithmetic over a series this build does not have |

---

## 2. REGIME (b) — CONTRACTUAL. THE KNOBS, AND THE FACT THAT THE DEFAULT IS NOTHING

For every counterparty outside regime (a), **no statute supplies a rate, a compounding rule or a
day-count convention.** The invoice does, or nothing does.

The knobs, per party (this is the DESIGN DECISION; TallyPrime's shape is the informal reference,
and per §0.2 `RESEARCH.md` does not pin it):

| Knob | Values | Default |
|---|---|---|
| **Rate** | An annual percentage | ⛔ **NONE — NULL** |
| **Compounding** | Simple · Compound | ⛔ **NONE — NULL** |
| **Rest period** (only if compound) | Monthly · Quarterly · Annually | ⛔ **NONE — NULL** |
| **Day-count basis** | **30-day month** (30/360) · **calendar month** (actual/actual) · **365-day year** (actual/365) | ⛔ **NONE — NULL** |
| **Grace / from-date rule** | From due date · from bill date · from due date + N days | ⛔ **NONE — NULL** |

⛔⛔ **"NO DEFAULT" IS A FEATURE AND IT MUST SURVIVE REVIEW.** The pressure to pick "simple, 18%,
365-day year, from the due date" will be real, because it makes every screen produce a number and
every demo produce a chart. It is exactly wrong. **An unconfigured party accrues ZERO interest and
the report states the reason: "no interest terms recorded for this party".** A zero that names
itself is information; an invented 18% is a claim the customer cannot defend when the debtor's
lawyer asks where it came from.

⭐ **The precedent for this tri-state is already in the schema and already pinned by a test** —
`ledger.credit_days` distinguishes NULL from 0:

```
$ /usr/bin/grep -rn "credit_days" backend/migrations/*.sql
backend/migrations/0002_core_schema.sql:63:    credit_days           INTEGER NULL,
backend/migrations/0039_party_address.sql:58:-- credit_days, external_id -- is a NULLABLE column, and masters.CreateLedger
```

`backend/internal/server/handler_ledger_party_fields_test.go:116-118` names all three cases:

```go
{"omitted is NO TERM (NULL)", map[string]any{}, nil},
{"explicit 0 is DUE IMMEDIATELY", map[string]any{"credit_days": 0}, intPtr(0)},
{"a real term survives", map[string]any{"credit_days": 30}, intPtr(30)},
```

**NULL rate = nothing agreed = no interest. Rate 0 = interest expressly waived = no interest, but
recorded as a decision.** They are different facts and the schema must keep them different, for
the same reason `credit_days` does.

---

## 3. HOW THE RULE IS RESOLVED PER PARTY — AND WHERE THE MSME FLAG LIVES (IT DOES NOT YET)

### 3.1 The resolution order

For each bill, in order, stopping at the first hit:

1. **Is the counterparty flagged a micro/small enterprise, as at the bill's date?** → **regime (a)**.
   Any per-party interest configuration is ignored, and the disclosure records that it was ignored.
2. **Does the party carry interest terms?** → **regime (b)**, using exactly those terms.
3. **Neither** → **no interest**, with the reason "no interest terms recorded".

⚠️ Step 1 says **"as at the bill's date"**, not "today". Udyam registration is acquired and can
lapse. A party that became an MSE in September does not retroactively convert last March's
invoice. So the flag is **dated**, like everything else in §6.

### 3.2 ⛔ THE FLAG DOES NOT EXIST ANYWHERE. MEASURED.

```
$ /usr/bin/grep -rn -iE "msme|udyam|MSMED" --include="*.go" --include="*.sql" --include="*.ts" --include="*.tsx" .
(no output)

$ /usr/bin/grep -rln -iE "msme|udyam|MSMED" --include="*.md" .
./HANDOFF-NEXT-UNBLOCKED.md
./RESEARCH.md
```

**Zero hits in code and zero in schema.** The only occurrences in the tree are prose.

**And the Tally importer cannot supply it either.** `TallyLedgerMaster`
(`backend/internal/integration/tallyxml.go:352-379`) reads NAME / PARENT / OPENINGBALANCE /
PARTYGSTIN / GSTIN / GSTREGISTRATIONTYPE / STATE (and its four aliases) /
`LEDGSTREGDETAILS.LIST` / `BILLALLOCATIONS.LIST` / ISDELETED — **and nothing resembling a Udyam
registration number.** So a migrated party arrives with no MSME status, and there is no source of
truth to import one from.

⭐ **THEREFORE THE FLAG CAN ONLY BE AN OPERATOR DECLARATION.** It is a fact about the
counterparty that this system learns from its user, per party, dated, and it should be presented
that way — with the Udyam number recorded alongside it, because a declaration with a registration
number behind it is defensible and a bare checkbox is not.

**Specified** (⛔ as a specification, not a migration — see §10):

- New NULLable columns on `ledger`, beside the party attributes already there —
  `backend/migrations/0002_core_schema.sql:53-68` has `gstin`, `pan`, `credit_days`; `0005` and
  `0039` add the rest:
  - `udyam_number TEXT NULL` — the registration as declared.
  - `msme_class TEXT NULL` — micro / small / medium. ⚠️ **MEDIUM ENTERPRISES ARE NOT IN
    REGIME (a).** ss.15–16 protect micro and small suppliers; storing the class rather than a
    boolean is what keeps a "medium" party from being silently swept in.
  - `msme_effective_from DATE NULL` — the dated part of §3.1 step 1.
- ⚠️ **NULL means "not declared", NOT "not an MSE".** The report must distinguish "this party is
  not an MSE" from "nobody has told us" — the second is a data-quality item the operator can act
  on, and rolling it into the first hides an exposure. Same discipline as `credit_days`.

---

## 4. WHAT THE ENGINE READS FROM — AND ⛔⛔ THE FINDING THAT RESHAPES THE BUILD

### 4.1 The real tables

| Source | Where | Columns that matter |
|---|---|---|
| `bill_allocation` | `backend/migrations/0005_phase1_masters_and_vouchers.sql:73` | `company_id`, `voucher_version_id`, `ledger_id`, `ref_name`, `ref_type` (`bill_ref_type`), `dr_cr`, `amount NUMERIC(19,4)`, `due_date DATE NULL`. **Immutable** — `bill_allocation_no_mutate`, same file :102-104. |
| `ledger_opening_bill` | `backend/migrations/0017_ledger_opening_bill.sql:36` | The same shape minus the voucher link, plus `UNIQUE (company_id, ledger_id, ref_name)` (:53). **Not immutable** — it is master data (:32-34). |

⛔ **BOTH, ALWAYS.** Outstandings nets a UNION of the two, and
`backend/migrations/0080_multi_currency.sql:174-185` already wrote down what happens to anything
that reads only the first — it heads that block *"WHY `ledger_opening_bill` IS IN THIS LIST, AND
WHY OMITTING IT WOULD BE A SILENT DATA DEFECT RATHER THAN A MISSING FEATURE"* and describes the
opening bill as *"the balance brought in at migration time, which for a new customer IS their
whole history"*.

⭐ **T13 inherits that hazard in a sharper form.** An interest engine reading only
`bill_allocation` computes **nothing at all** on the oldest and most overdue debt in the book —
which is precisely the debt interest exists for, and precisely the debt a migrating customer
brings with them on day one. T8's failure mode was a partial column beside a correct total; T13's
would be a confident ₹0.

Go side, all in `backend/internal/reporting/outstanding.go`:

| Type | Line |
|---|---|
| `AgeSlab` | `:28` |
| `DefaultAgeSlabs` | `:35` |
| `Bill` | `:44` |
| `PartyOutstanding` | `:58` |
| `Outstanding` | `:68` |
| `ageBill` | `:371` |

### 4.2 ⛔⛔ `reporting.Outstandings` IS THE WRONG INPUT, AND THIS IS NOT A SMALL FIX

The plan's framing ("we already have ageing") suggests T13 wraps the outstandings report. **It
cannot.** The report's query nets each `(ledger, ref_name)` to a single figure:

```
backend/internal/reporting/outstanding.go:189
  signExpr := "SUM(CASE WHEN ba.dr_cr='D' THEN ba.amount ELSE -ba.amount END)"

backend/internal/reporting/outstanding.go:266
  to_char(MIN(ba.due_date), 'YYYY-MM-DD') AS due_date
```

⭐ **Interest accrues on a DECLINING BALANCE THROUGH TIME. A netted row has thrown time away.**

Worked, on a ₹1,00,000 invoice due 2026-04-01, part-paid ₹40,000 on 2026-05-31 and ₹60,000 on
2026-07-30:

| What actually happened | Principal at risk | Days |
|---|---|---|
| 2026-04-01 → 2026-05-31 | ₹1,00,000 | 60 |
| 2026-05-31 → 2026-07-30 | ₹60,000 | 60 |
| From 2026-07-30 | ₹0 | — |

**Interest is owed on both segments.** What the outstandings query returns for this bill is one
row: `pending = 0`. An engine built on it computes **zero interest on a bill that ran 120 days
late and was settled in two instalments** — which is not an edge case, it is how bills get paid.

⛔ **So T13 needs its OWN query, returning ONE ROW PER ALLOCATION** — every `NEW` and every
`AGST` leg, with its amount, its side, and **the date of the voucher that made it** — ordered, and
walked as a running balance. It is a new read path in `internal/reporting` (or its own package),
not a wrapper.

⭐ **The join is already known-shaped, so this is work but not risk.** `forecast.loadDateAttrs`
(`backend/internal/forecast/service.go:143-152`) already joins `bill_allocation` to
`voucher_version` for exactly these dates, and its header (`:137-142`) already argues why a
date-only lookup deliberately scans **all** versions rather than current ones — *"a superset is
the safe direction: every bill the … amount query returns is guaranteed a matching date row, where
a narrower scan could leave one dateless"*. An interest walk has the same requirement and should
inherit the same reasoning.

---

## 5. ⛔ THE ANCHOR-DATE GAP — MEASURED, AND IT BLOCKS REGIME (a) FOR MIGRATED BOOKS

MSMED s.15's appointed day runs from **acceptance or deemed acceptance**, i.e. from the
**BILL's own date** — not from its due date. **Neither bill table stores a bill date** (§4.1: both
carry `due_date` only).

- **For a voucher-raised bill it is recoverable**: `voucher_version.voucher_date`, via the §4.2
  query. Fine.
- **For a MIGRATED opening bill it is NOT.** Two measurements:

```
$ /usr/bin/grep -rn "BillDate" --include="*.go" backend/
backend/internal/integration/tallyxml.go:401:	BillDate       string `xml:"BILLDATE"`
```

**One hit — the struct tag.** `TallyOpeningBill.BillDate` is parsed out of the Tally export and
**never read by anything.** The date is in the XML, it is decoded into memory, and it is dropped
on the floor because there is no column to put it in.

```
$ /usr/bin/grep -rn "BILLCREDITPERIOD\|billDueISO" --include="*.go" backend/
backend/internal/integration/tallyxml.go:402:	CreditPeriod   string `xml:"BILLCREDITPERIOD"`
backend/internal/migration/migration.go:805:			DueDate: billDueISO(b.CreditPeriod),
backend/internal/migration/migration.go:945:func billDueISO(s string) string {
backend/internal/migration/migration.go:1054:				DueDate:  billDueISO(ba.DueDate),
```

And `billDueISO` (`backend/internal/migration/migration.go:945-957`) interprets `BILLCREDITPERIOD`
as **a date and only a date** — ISO, or `TallyDateToISO`, else `""`. Tally's very common
`"45 Days"` form yields the empty string.

⛔⛔ **SO A MIGRATED MSE BILL CAN REACH THE ENGINE WITH NO DUE DATE *AND* NO BILL DATE.** Under
regime (a) that is not a degraded answer, it is **no answer**: there is no appointed day, so there
is no clock. The engine must **refuse and name the bill**, never assume a date. (Compare
`ErrTDSDateRequired`, `backend/internal/ledger/tds.go:50-53` — *"A defaulted date would file a
March payment in the wrong year"*. Same shape of harm, same answer.)

**What the fix needs — REPORTED, NOT TAKEN:**

- A `bill_date DATE NULL` column on **both** `bill_allocation` and `ledger_opening_bill`.
- `billDueISO` extended (or joined by a sibling) to parse the `"N Days"` credit-period form
  against the bill date, so a migrated bill gets a real due date instead of `""`.
- ⛔ **THE MIGRATION NUMBER IS DELIBERATELY NOT ALLOCATED IN THIS NOTE.** ⚠️ Note also that
  `PLAN-TALLY-PARITY.md:154` still pencils T13 in at `0058`; the tree is already at
  `0080_multi_currency.sql` (`$ ls -1 backend/migrations/*.sql | tail -1`), so that reservation
  is stale and the number must be taken fresh when the work is scheduled, not read off the plan.

---

## 6. ⚠️ THE DATED RATE TABLE — WHY A SCALAR "BANK RATE" SETTING IS A BUG

### 6.1 The worked example

A scalar setting — one row, one number, "the Bank Rate" — is not a simplification. It is a defect
with two distinct symptoms.

Take an MSE supplier's invoice: **₹10,00,000**, appointed day **2026-06-15**, interest computed to
**2026-12-31**. Regime (a): compound, monthly rests, at **3 × Bank Rate**. Suppose the Reserve
Bank's notified Bank Rate stood at **B₁** until 2026-09-30 and at **B₂** from 2026-10-01. (⛔ B₁
and B₂ are deliberately symbols. This note does not know their values and will not invent them —
see §8.)

| Rest | Period | Correct rate | What a scalar setting holding today's number uses |
|---|---|---|---|
| 1 | Jun 15 → Jul 15 | 3 × B₁ | 3 × B₂ |
| 2 | Jul 15 → Aug 15 | 3 × B₁ | 3 × B₂ |
| 3 | Aug 15 → Sep 15 | 3 × B₁ | 3 × B₂ |
| 4 | Sep 15 → Oct 15 | **spans the change** | 3 × B₂ |
| 5 | Oct 15 → Nov 15 | 3 × B₂ | 3 × B₂ |
| 6 | Nov 15 → Dec 15 | 3 × B₂ | 3 × B₂ |

**Symptom 1 — the number is simply wrong.** Three-and-a-bit of the six rests are priced off a rate
that was not in force when they accrued. Because the rests compound, the error is not a
proportional shave off the total; it is carried forward and multiplied by every later rest.

**Symptom 2, and it is the worse one — ⛔ THE REPORT STOPS BEING REPRODUCIBLE.** With a scalar, the
moment the RBI notifies a new Bank Rate and somebody updates the setting, **every past period's
interest silently changes.** The demand letter sent in November cannot be regenerated in January.
Two people running "interest as at 31 December" on different days get different answers and
neither is doing anything wrong. A report that cannot reproduce its own history is not evidence,
and interest is *only* ever wanted as evidence.

⭐ **The rate is not a setting. It is a DATED SERIES, and every accrual segment must be priced off
the rate in force ON THAT SEGMENT.** Rest 4 above spans the change and must be split at the
notification's effective date — which means the walk in §4.2 is segmented by **two** things: the
payment events, and the rate-change events.

### 6.3 ⚠️ A PROVISIONAL SERIES — SUPPLIED 2026-08-29, AND WHY IT IS NOT YET USABLE

The project owner supplied a Bank Rate table. It is recorded here **because its SHAPE is
informative even though its DATES are not usable**, and because §6.1 deliberately reasons with
symbols `B₁`/`B₂` — the engine can be built against this shape and take the numbers later.

⛔ **IT IS NOT COMMITTED TO `docs/statutory/` AND MUST NOT BE READ AS VERIFIED.** No source, no
notification reference. It is at best `ConfUnverified` in `rates.go`'s vocabulary.

⭐ **WHAT PASSED — a real consistency check, not a glance.** RBI's convention aligns the Bank Rate
with the MSF rate at the repo rate plus 25 basis points. **All twelve supplied rows satisfy
`bank = repo + 0.25` exactly**, computed rather than eyeballed. A table that had been assembled
carelessly would very likely have broken that somewhere.

⭐ **AND THE SHAPE: TWELVE ROWS ARE NINE CHANGES.** Three rows repeat the preceding rate unchanged
(Feb 2026 and Aug 2026 both restate Dec 2025; Dec 2024 restates Feb 2023). **They are observation
points, not notifications.** A series builder that loaded twelve rows as twelve change-events would
insert three spurious segment boundaries — harmless to the arithmetic, since the rate either side is
identical, but it would make the segmented walk of §4.2 emit rests that split for no stated reason,
and a reader asking "why does this rest end here?" would find no answer.

| Bank Rate from | Rate | s.16's `3 ×` |
|---|---|---|
| May 2020 | 4.25% | 12.75% |
| May 2022 | 4.65% | 13.95% |
| Aug 2022 | 5.65% | 16.95% |
| Sep 2022 | 6.15% | 18.45% |
| Dec 2022 | 6.50% | 19.50% |
| Feb 2023 | 6.75% | 20.25% |
| Feb 2025 | 6.50% | 19.50% |
| June 2025 | 5.75% | 17.25% |
| Dec 2025 | 5.50% | 16.50% |

⚠️ **Note what `3 ×` produces: 12.75% to 20.25% per annum, compounding monthly.** These are large
numbers and they are the statute's, not a setting anyone chose. It is worth seeing them written out
before the first demand letter is generated.

### ⛔⛔ WHY IT CANNOT BE USED YET: THE DATES ARE MONTHS, AND §6.1 NEEDS DAYS

§6.1's rest 4 *"spans the change and must be split at the notification's **effective date**"*.
**"Feb 2025" cannot split anything.** A bill whose appointed day falls in the same month as a rate
change has at least one rest priced off a boundary this table cannot locate, and the engine would
have to pick a day — the 1st, the last, the announcement — which is precisely the invention this
whole note exists to prevent.

⭐ **SO THE REMAINING ASK IS NARROW AND IT IS THE ONLY THING LEFT BLOCKING T13:** the **exact
effective date** of each of the nine changes above, from `rbi.org.in`. The Bank Rate moves with the
MSF rate, which moves with the MPC's policy decisions, so the per-decision press releases carry the
dates; RBI's own historical rate tables carry them in one place. **Nine dates and a source, and this
section becomes `docs/statutory/` material and the build starts.**

### 6.2 Where the series lives — three options, and a recommendation

**Precedent A — a keyed effective-date table.** `currency_rate`,
`backend/migrations/0080_multi_currency.sql:144-164`: `(company_id, currency_id, effective_date)`
with a unique index at `:164`. Simple; "the rate as at D" is a `MAX(effective_date) <= D` lookup.

**Precedent B — a half-open window with an exclusion constraint.**
`backend/migrations/0034_payroll_schema.sql:209-302`: `effective_from` / `effective_to` NULL-able
(`:225-226`), `CHECK (effective_to IS NULL OR effective_to > effective_from)` (`:271`), and a GiST
`EXCLUDE … daterange(effective_from, effective_to, '[)') WITH &&` (`:290-294`) that makes
overlapping windows **impossible rather than merely unlikely**. Stronger, and the comment at
`:209` explains the half-open convention.

⛔ **BUT BOTH PRECEDENTS ARE TENANT-SCOPED, AND THE BANK RATE IS NOT TENANT DATA.** It is a
national notification. A per-company copy means two tenants of the same instance can hold
**contradictory law**, and a tenant can quietly edit the statutory rate applied to its own MSE
suppliers — which is precisely the override §1 says the Act forbids.

⚠️ **The counter-pressure is real: every business table in this schema is `company_id`-scoped
under FORCE RLS.** The only non-tenant tables are the auth ones (`app_user`,
`webauthn_credential`, `webauthn_challenge`, `user_credential`, `auth_login_attempt`). **There is
no precedent for a shared reference table**, so introducing one is a decision with an owner, not a
default.

⭐ **RECOMMENDED — OPTION C: A VERSIONED, COMPILED-IN DATED RULESET.** Model it exactly on
`compliance.tdsRegistry` (`backend/internal/compliance/tds.go:246-247`, ruleset id `"tds-2026-04"`;
`backend/internal/ledger/tds.go:15-16` holds `DefaultTDSRulesetVersion`). The Bank Rate series
ships as data in the binary, keyed by a ruleset version, and every stored interest computation
records **which version produced it**.

Why this and not a table:

- The Bank Rate is law, not configuration. It belongs where the TDS rates belong.
- It gets a **RATE PROVENANCE header** — `backend/internal/compliance/tds.go:9-30` is the model,
  and it is a good one: it names the primary document, names the *edition* it was read against, and
  carries the warning *"A VERIFICATION IS ONLY AS CURRENT AS THE EDITION IT READ"* after that
  project got burned by exactly one stale rate (scrap TCS, 1% vs 2%).
- It is reviewable. A rate change arrives as a diff with a citation, in a commit, seen by a human.
- It cannot be edited by a tenant, which is the §1 override requirement made structural.

**The honest cost:** a new RBI notification then requires a release, and a customer on an old build
computes interest off a stale series. ⭐ **That is the right trade for a number that goes on a
demand letter.** A stale rate that is *versioned and disclosed* is recoverable — the report says
which ruleset it used and the figure can be reproduced or re-run. A tenant-editable rate that was
quietly wrong is not recoverable, because nothing recorded what it was at the time.

⚠️ **This recommendation cuts against the universal `company_id` + RLS convention and needs an
owner's sign-off. It is not settled here.** If the eventual builder follows the convention
reflexively instead of reading this section, the result is two tenants holding contradictory
statutory rates — flagged in §10.

---

## 7. DOES IT POST ANYTHING? ⭐ NO IN v1 — AND THE HOUSE ALREADY SETTLED THIS RULE

**Recommendation: T13 v1 is a REPORT. It posts nothing.**

**The rule is not new.** `backend/internal/ledger/tds.go:57-58` states it for tax:

> *"It NEVER injects postings of its own — the books stay exactly what the client sent, they are
> merely refused when they contradict the engine."*

And T8 settled the same question the same way for revaluation —
`t8-multi-currency-model.md:208`, *"Q4 — UNREALISED gain/loss ⭐ A REPORT THAT POSTS NOTHING, IN
v1"*, with the accompanying rule that the `FeaturesScreen` row must say exactly that.

⭐ **Interest on an overdue bill is an unrealised claim.** It is what the company *could* charge,
not what it *has* charged. It becomes a book entry when the company decides to bill it — which is
a commercial decision (often declined, to keep the customer), not an accounting consequence. A
report that computes it and a human who decides to invoice it are the correct division.

### 7.1 ⛔ PRICING THE ALTERNATIVE, SO THE DECISION IS INFORMED

If someone later wants interest to post, **"just raise a debit note" is not a small extra — it is
a GST question this tree has never asked.**

1. A debit note is GST-bearing. `backend/internal/ledger/ledger.go:123-129`:
   `gstBearing` returns true for `Sales, Purchase, CreditNote, DebitNote`. So the posting path
   immediately demands a rate, a place of supply and an HSN/SAC.
2. A debit note must **name the invoice it adjusts** — a required particular under CGST s.34 read
   with Rule 53(1A), and this build already refuses to print one without it
   (`backend/internal/printing/voucherdoc.go:100-118`, and note :110-117's careful refusal to cite
   a clause letter it could not verify).
3. ⛔ **And the substantive question: CGST s.15(2)(d) arguably makes interest for delayed payment
   part of the VALUE OF SUPPLY** — i.e. interest may itself attract GST, at the rate of the
   underlying supply. **Nothing in this tree has ever considered it:**

```
$ /usr/bin/grep -rn "15(2)" backend/internal/ RESEARCH.md
(no output)
$ /usr/bin/grep -rl "15(2)" docs/
docs/design/t13-interest-model.md          <-- this note, and nothing else
```

**A Journal carrying a bill allocation is the cheap posting path** if posting is ever wanted — it
sidesteps `gstBearing` entirely and keeps the bill-wise linkage. This note names it and does not
build it.

⚠️ **Choosing "report only" for v1 does NOT retire the s.15(2)(d) question — it defers it.** The
day anyone decides interest should post, that question becomes blocking and needs primary text of
its own. Said here so the deferral is visible rather than mistaken for a resolution.

---

## 8. ⛔⛔ WHAT IS STILL MISSING — THE PRIMARY DOCUMENTS, AND WHY THEY COME FIRST

`docs/statutory/README.md` states the convention this note is written against: *"the notified form,
the file-format specification, the gazette text — the things a rate, a field or a code list must be
traceable to. **Not** blog articles, and not summaries"*, downloaded **by a human with a browser**
(automated fetchers get 403s and certificate rejections), committed to `docs/statutory/`, and
listed in that README's table with a status, a source and a "what it settles" column.

**T13 needs two rows added to that table.** They are `HANDOFF-NEXT-UNBLOCKED.md:182`'s ask 2, which
that document rates ⭐ *"NOT blocked by anything — ordinary public documents, no registration, no
geo-fence. This is the ask worth chasing."*

| File | Status | Source | What it settles |
|---|---|---|---|
| MSMED Act 2006, **ss.15–23** — gazette text ⚠️ **plus s.24 (override) and s.7/s.2 (the micro/small definitions), which are OUTSIDE 15–23 and which §1.1 shows a 15–23 summary does not reach** | ⛔ **NOT HERE.** ⭐ Partially corroborated 2026-08-28 by a second second-hand source (§1.1), which also CORRECTED s.15 — the 15-day no-written-agreement fallback this note had missed | The Ministry of MSME / India Code gazette text of the Micro, Small and Medium Enterprises Development Act, 2006 | §1 in full: s.15's appointed day and its 45-day limit; s.16's *compound / monthly rests / three times the Bank Rate*; the s.24 (or equivalent) overriding effect; **and the definitions that decide who is in regime (a)** — micro vs small vs medium, which §3.2 stores a class for |
| **RBI Bank Rate**, notification history | ⛔ **NOT HERE** | **`rbi.org.in`** — the Reserve Bank's own notifications / policy-rate archive | The dated series of §6. ⛔ **NOT a calculator website. NOT a news summary. NOT the repo rate** |

### ⛔ WHY THE ORDER IS NOT NEGOTIABLE

**Do not build the engine first and retro-fit the citations.** The output of this feature is a
rupee figure that a business puts in front of a debtor, and sometimes in front of a court. If it
cannot be traced to a gazette and an RBI notification, it is a number nobody can defend — and the
first time it is challenged, the customer discovers that their accounting software made it up.

This project has already been burned by exactly the softer version of this mistake and wrote it
down: `docs/statutory/README.md` records that a shipped TCS rate was **1% when the Finance Act 2026
had moved it to 2%**, *"under-collected by half on every scrap sale from 1 April 2026"* — a rate
that had been "verified", against the wrong edition. **T13 would be starting from further back than
that: not the wrong edition, but no text at all.**

⭐ The corollary, which is the whole reason this note exists: **everything except the numbers can be
built now.** The two-regime split, the per-party resolution, the per-allocation walk, the segmented
accrual, the disclosure — none of it needs the documents. Only the rate series and the exact
wording of ss.15–16 do. So the wait is not idle time.

---

## 9. THE ORACLE, AND THE BUILD ORDER

### 9.1 The oracle — named before the first test, per `t8-multi-currency-model.md` §4

**A corpus of fixtures, each carrying its statutory citation**, exactly as the tax engine does:
`backend/internal/compliance/tds.go:24-25` — *"Each fixture in `corpora/tds/*.json` carries the
statutory citation in its `description` field."* A `corpora/interest/*.json` beside it, where every
case names what it is testing and under which authority, and where the regime-(a) cases stay
**RED until the §8 documents land** rather than being quietly filled in with plausible numbers.

**A pure `compliance.InterestDerive`**, beside `TDSDerive`, whose header says what
`backend/internal/compliance/tds.go:3-7` says of its neighbour — *"a PURE function … It performs no
I/O"*. Given a principal series, a date series, a regime and a versioned ruleset, it returns the
interest and the working. **Testable with no database**, which is what makes a corpus worth having.

⭐ **And every figure must disclose HOW it was produced** — which regime, which ruleset version,
which rate applied to which segment, and which dates were observed versus assumed. **The pattern
already exists in this build for precisely this problem**: `forecast.DateSource`
(`backend/internal/forecast/forecast.go:77-88`) distinguishes `due_date` (*"Observed, not
assumed"*) from `party_credit_days` (*"A tenant-configured assumption"*) from `default_credit_days`
(⛔ *"the arm that is a CONVENTION of this build and nothing else"*), and the API surfaces the
breakdown in money and in count — `backend/internal/server/handler_forecast.go:189-194`
(`from_due_date` / `from_party_credit_days` / `from_default_credit_days`).

⛔ **T13 has a stronger version of the same obligation, because its output is a demand and not a
forecast.** An interest figure with no provenance is not a smaller feature than one with
provenance — it is an unusable one.

### 9.2 The build order

1. **The §8 documents land** and the README table is updated. Nothing numeric starts before this.
2. **The per-allocation read path** (§4.2) — one row per allocation with its voucher date, both
   source tables, walked as a running balance. Its own tests, no rates involved.
3. **The schema**: bill date (§5) and the party MSME columns (§3.2), plus the regime-(b) interest
   terms. ⛔ **Migration number taken fresh at that point — §5.**
4. **`compliance.InterestDerive` + the corpus** (§9.1), pure, with the rate provenance header.
5. **Regime (a)**, refusing loudly on a missing appointed day.
6. **Regime (b)**, with NULL meaning nothing and saying so.
7. **The report + the screen.** A new report under the `report:` ACL family —
   `report:outstandings` is the sibling (`backend/internal/server/server.go:3604`;
   `backend/internal/server/outstandings_kind_retired_test.go:5-6` shows how a route and its print
   route are decided against one row). `backend/migrations/0038_print_acl.sql:117-124` documents
   the precedent for granting one: *"that is one `allow print report:outstandings` row in a
   successor migration — a decision, made once, on the record."* The screen goes beside
   `frontend/src/screens/reports/`. ⛔ **The ACL migration number is deliberately NOT allocated
   here either.**
8. **The `FeaturesScreen` row**, moving in the same commit as the feature — and §0.3's correction
   lands before any of this, independently, because the current sentence is wrong today.

---

## 10. WHAT THIS MEMO DOES NOT SETTLE

| # | Open | Who decides |
|---|---|---|
| 1 | ⛔ **The statutory content of §1 is second-hand** (`HANDOFF-NEXT-UNBLOCKED.md:137` + the task brief), not primary text. Every rate, rest and period in it is **PENDING PRIMARY VERIFICATION**. | §8's two documents |
| 2 | ⛔ **Shared vs tenant-scoped rate series (§6.2).** The recommendation — compiled-in, versioned — cuts against the universal `company_id` + FORCE RLS convention. Following the convention reflexively lets two tenants hold contradictory law. | Owner sign-off, before the schema |
| 3 | ⛔ **The bill-date column (§5)** and the MSME columns (§3.2) need a migration number this note does not take. `PLAN-TALLY-PARITY.md:154`'s `0058` reservation is stale — the tree is at `0080`. | Whoever schedules T13 |
| 4 | ⚠️ **GST on interest — CGST s.15(2)(d) (§7.1).** Unanswered anywhere in this tree before this note (§7.1 has the commands). Deferred by the report-only choice, **not retired** by it. | Blocking only if interest ever posts |
| 5 | ⚠️ **`FeaturesScreen.tsx:143` still asserts the wrong sentence** (§0.3). The file is another lane's; the replacement text is drafted above and needs that lane to paste it. | The `FeaturesScreen` owner |
| 6 | Whether interest is offered on **payables** as well as receivables. The engine is symmetric and `Outstandings` already takes a `Kind` — but a company computing what it *owes* an MSE supplier is a materially different product claim (an exposure report, arguably a disclosure obligation) and deserves its own decision rather than falling out of the code. | Product |
| 7 | Whether a **grace period** may be configured under regime (b). It is a legitimate commercial term; it is also the most likely place for a "sensible default" to sneak back in. | Product, with §2's no-default rule binding |
