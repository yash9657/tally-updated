# HANDOFF — FEATURE LOCK WITH TALLY, THEN PAST IT

> ## THE GOAL, IN ONE SENTENCE
>
> **Reach feature parity with TallyPrime — every feature, working the way Tally works it,
> reachable by the keystroke Tally uses — and then build the layer Tally has never had: an
> ERP that tells a business owner what their numbers MEAN.**
>
> Nothing in this repository is research. Everything below is a known feature of a shipping
> product that has existed for thirty years. There is no invention required, only work.
>
> **This file supersedes `HANDOFF-PARITY-SPRINT.md`, `HANDOFF-CLOSING-THE-GAP.md` and
> `HANDOFF-T1-PRINTING.md` as the entry point.** They remain useful for the *reasoning* behind
> specific decisions; this file is the plan of record.
>
> **State at handoff:** migrations `0067` · backend **1929 pass / 0 SKIP / 0 FAIL** · frontend **443** ·
> `make verify` green · tree clean. **Item Zero steps 1–3 have landed — see §2. Step 4 (the
> register and the returns) is all that is left of the debt.**

---

## 1. THE BAR. This is the part that has been getting missed.

**A FEATURE IS NOT DONE WHEN THE ENGINE IS CORRECT. IT IS DONE WHEN A USER CAN REACH IT.**

Every track ships **all seven** of these or it is not finished. Not "engine + tests, wiring
next session". That is how this codebase accumulated correct code that nothing calls.

| # | Layer | Done means |
|---|---|---|
| 1 | **Schema** | Migration applied, RLS `ENABLE`+`FORCE`, policy with `USING` **and** `WITH CHECK`, composite `(company_id, id)` FKs, `testdb` `buildFixture` row |
| 2 | **Engine** | The pure logic, with the statutory or behavioural citation |
| 3 | **Wired into the write path** | `ledger.Post` / the service actually **calls** it and **persists** the result |
| 4 | **HTTP** | A route, mounted, with `routeAction()` + `authz.KnownResources` + an ACL decision |
| 5 | **Screen** | A screen or panel a human can see it on, in `defaultScreens` |
| 6 | **Keystroke** | The **Tally** key, bound, cited to `RESEARCH.md` by line — plus the other 7 registration points (§5.2) |
| 7 | **Honesty surface** | `FeaturesScreen.tsx` row flipped to `on`, in the **same commit** |

**A track that stops at layer 2 has delivered nothing to a user.** If a track cannot reach
layer 7 in one pass, it was scoped too big — split it by *user-visible slice*, never by
*architectural layer*.

### 1.1 The evidence bar (unchanged, and it is why the numbers above are trustworthy)

- `go test ./... -count=1 -v`, **0 SKIP**, **0 FAIL** — without `-v` no SKIP lines print.
- **Bracket long runs with a live `psql` query before and after.** DB down ⇒ every DB test
  skips ⇒ `go test` still exits 0.
- Every new invariant gets a **mutation proof**: break it, watch the *right* test fail with a
  message stating the consequence in rupees or in a user's words, restore byte-identically
  (`shasum -a 256 -c`), say you did it.
- Every differential test gets a **control**.
- **Never invent a rate, threshold, clause letter, code or keystroke.** Cite it or refuse.
  `RESEARCH.md` is citable by line number for every Tally keystroke.
- **Migrations: use the reserved block in `PLAN-TALLY-PARITY.md` §2.5. Never pick a number.**

### 1.2 One lesson that cost real money this month

`206C(1)-SCRAP` shipped at 1% when the Finance Act 2026 had made it **2% w.e.f. 1-4-2026**.
Half the tax on every scrap sale, for four months. A prior pass had verified it — against the
Act **as enacted**, while the amended edition sat unread in `docs/statutory/`.

> **"Verified against the Act" is not a durable claim. An Act is amended annually, so a
> verification is only as current as the EDITION it read. Record the edition; re-read on every
> Finance Act.**

---

## 2. ⛔ ITEM ZERO — STEPS 1–3 ARE DONE. Step 4 remains.

Migrations 0049/0050 and three compliance engines existed, were tested, and **nothing called
them**. Steps 1–3 below have now landed: both taxes derive, refuse, persist, and are
configurable from F11. Nothing can yet be REPORTED, which is step 4.

**✅ Step 1 — the TDS post path (done).** Inside the existing post transaction, in this order:
deductor status for the **preceding** tax year → `MustDeduct` → refuse when unrecorded
(`ErrDeductorStatusUnknown`) and refuse a leg posted with no duty (`ErrNoDutyToDeduct`) → sum
the aggregate for `(deductee, section, tax year)` → `TDSDeriveCumulative` → verify the posted
leg → **write the `tds_deduction` row in the same transaction**, including zero-amount rows.
ALTER re-derives and DELETE removes. `deductee_ledger_id` is now required on the HTTP `tds`
block. Five mutation proofs; backend **1902 pass / 0 SKIP / 0 FAIL**.

> **⚠️ A REAL UNDER-DEDUCTION WAS FOUND AND FIXED WHILE WIRING THIS, and it is the §1.2 lesson
> again.** `TDSDeriveCumulative` returned early whenever the per-payment limb fired, so the
> aggregate limb was never evaluated. Five ₹20,000 contractor bills (₹1,00,000, untaxed)
> followed by a ₹40,000 bill collected ₹800 instead of the ₹2,800 due on the entire ₹1,40,000 —
> and the first five bills stayed untaxed for ever. **The tell was non-monotonicity:** had that
> sixth bill been ₹20,000 it would have caught up ₹2,400, so a *larger* payment collected *less*
> tax. `TestPerPaymentLimbDoesNotSuppressTheCatchUp` pins both the amount and the monotonicity.
>
> Two of the tests written for this slice were **initially vacuous** and only caught by
> mutation: an alter of ₹80,000→₹90,000 gives the same answer with or without the
> self-cumulation bug (whenever the prior base is fully taxed, the two formulas agree), and two
> HTTP tests were green while sending an invalid `dr_cr` — every domain refusal on that route is
> a 422, so **asserting the status code alone proves nothing. Assert the reason.**

**✅ Step 2 — the deductor-status route and screen (done).** `GET /deductor-status` and
`PUT /deductor-status/{taxYear}`, mapped in `routeAction()`, `deductor-status` added to
`authz.KnownResources`, and a **TDS Deductor Details** panel on the existing **F11** screen —
which is where Tally keeps it (`RESEARCH.md:692`: F11 > Enable TDS > TDS Deductor Details).
An operator can now go from "refused" to "posted" without touching the database, which is what
`TestRecordingTheStatusUnblocksTDSPosting` asserts end to end over HTTP.

> **NO ACL MIGRATION, AND THAT IS A DECISION.** Data Entry's `default_access` is `none` and no
> coarse rule reaches this token — 0011's `allow display report` only reaches `report:NAME`,
> 0022's is `master` — so Data Entry is already refused both halves and Owner's `full` already
> allows them. That is the intended authority: recording the deductor status decides a year of
> withholding, so it belongs to whoever signs the return, not whoever keys the bills.
> `TestDeductorStatusAuthMatrix` pins all four answers on both halves. **No restatement of
> `seed_company_auth_defaults` was needed, which also avoids that function's serialization
> trap** — 0046's header explains why that matters.
>
> **⛔ TCS DECLARATIONS WERE DELIBERATELY NOT BUILT HERE**, though the original step 2 bundled
> them. `TCSInput.Declaration` has no caller, so a route and screen for
> `tcs_collection_declaration` would be a second helping of the exact "correct code nothing
> calls" this Item Zero exists to pay off. They belong with step 3, where wiring TCS into the
> post path makes them a complete slice.

**✅ Step 3 — TCS on the post path, with the s.394(2) declaration route and screen (done).**
`TCSDerive` is called on Sales, the collection is verified and **persisted** (migration
**0067** `tcs_collection`, the mirror of 0050), and `GET/POST /tcs-declarations` plus a second
F11 panel make the exemption reachable. `TestFilingADeclarationExemptsTheNextSale` proves the
pair meets: a scrap sale collects 2%, a declaration is filed, the same sale collects nothing.

> **THREE THINGS ARE DELIBERATELY NOT MIRRORED FROM THE TDS SIDE. Do not "make them
> consistent" — each asymmetry is the statute, and two of them are traps.**
>
> 1. **No aggregate and no catch-up.** Both shipped sections are per-transaction: Sl. No. 4
>    (scrap) states no threshold, Sl. No. 6 (motor vehicles) tests the single sale. Cumulating
>    either would collect tax the section does not charge.
> 2. **The declaration is read for the SALE's OWN tax year**, whereas the deductor status is
>    read for the year BEFORE the payment. Reusing `deductorStatusTaxYear` here silently
>    ignores every declaration ever filed — mutation-proved.
> 3. **A PAN-less declaration is NOT "no declaration".** s.397(2)(f)(i) invalidates it and (g)
>    then requires the **penal 5%**, not the base 2% and not the exemption. Collapsing NULL PAN
>    into "absent" in the lookup charges 2% — mutation-proved. `tcs_collection.declaration_outcome`
>    is three-valued for the same reason.
>
> **TWO DISCLOSED GAPS, both in the over-collecting (recoverable) direction:** no
> seller-liability test is modelled — s.394(1) charges "every person, being a seller" and that
> definition has not been read against primary text from this environment, so every company
> that keys a `tcs` block collects; and a **credit note reversing a collected sale is not
> handled** (it needs a negative collection, which 0067 forbids by CHECK, and a return that
> files a negative line is a Form 143 question).
>
> **No `return_code` column on 0067, unlike 0050 — deliberately.** 0050 needed one because
> 194J-P and 194J-D share a statutory limb at the same rate and differ only by code. The two
> TCS sections are distinct rows at distinct rates, so `section` determines the code uniquely
> and a later migration can backfill it safely. The Form 143 codes have not been sourced, and
> **inventing one would put a fabricated code in a filed return.**

**What remains:**

| # | Work | Notes |
|---|---|---|
| **4** | **T6's register and the Form 140 / Form 143 returns** | The only Item Zero step left, and now the blocker. Both `tds_deduction` and `tcs_collection` have rows to read. **Needs: the Form 143 payment codes read against primary text** (§7 human task), and the `.csi` challan refusal designed. Until a register exists, the TDS/TCS row on `FeaturesScreen` stays **off** — the numbers are correct, stored, and go nowhere. |

---

## 3. THE COMPLETE GAP INVENTORY

### 3.1 Group A — the 15 features the product itself says are OFF

From `FeaturesScreen.tsx`, which is the honesty surface: **25 on, 3 server-only, 2 structural,
15 off.** Migration blocks from `PLAN-TALLY-PARITY.md` §2.5.

| # | Track | Migrations | Who it stops | Data-model risk |
|---|---|---|---|---|
| **T2** | **Cost centres + cost categories** | 0040–0041 | Anyone tracking profitability by branch, project or salesperson | **HIGH.** Allocations live *inside the hashed payload*; the sum-to-posting invariant is a **constraint trigger**, not a form check. Parallel allocation per category is not one column. |
| **T3** | **Sales/Purchase Orders + BOM** | 0042–0044 | Every trader and manufacturer. The most-used missing feature. | **HIGHEST.** An order is a **COMMITTED QUANTITY, NOT A MOVEMENT.** Push it through `stock_movement` and closing stock — and the Balance Sheet — is corrupted. Partial fulfilment is the normal case. |
| **T5** | **e-Way Bill** | 0047–0048 | Anyone moving goods >₹50,000 | Medium. Reuse the IRN precedent in `internal/statutory`: NIC is unreachable, so ship the deterministic-fake + recorded-schema arrangement and **say so on the document's face**. |
| **T6** | **TDS/TCS register + Form 140/143 returns** | 0049–0050 ✅ consumed | Every business paying contractors or professionals | Engines + storage **done**; see §2. Remaining: register, return file (`^`-delimited, `FH`/`BH`/`CD`/`DD`, CRLF, amounts 2dp, **rate 4dp**, dates `ddmmyyyy`). **The `.csi` challan file is unreachable — design the refusal.** FVU cannot run here: **never claim a validated return.** |
| **T8** | **Multi-currency** | 0051–0052 | Any exporter or importer | **HIGH.** Forex gain/loss on realisation, unrealised revaluation at period end, and a rate table. Deep but isolated. |
| **T9** | **FIFO / LIFO / batch-wise valuation** | 0053 | Anyone with real inventory | **HIGH — it MOVES THE BALANCE SHEET.** Closing stock feeds it. **Re-pin `verify:demo`.** |
| **T10** | **Stock groups + compound units** ("Box of 12") | 0054–0055 | Nearly every trader | Medium. Touches inventory with T9 — **serialise or worktree-isolate those two.** |
| **T11** | **Budgets + budget-vs-actual** | 0056–0057 | Anyone managing to a plan | Medium. **Depends on T2.** |
| **T12** | **GSTR-9 annual return** | 0058 | Every registered business, annually | Medium. It is a **RECONCILIATION** of the year's GSTR-1/3B to the books, not a fresh derivation — it must state where the three disagree. |
| **T13** | **Interest on overdue bills** | 0059 | Anyone charging interest | Medium. Effective-dated rates, simple vs compound, per-party override. |
| **T14** | **Backup / restore / split company** | 0060–0061 | Everyone, eventually | **HIGHEST BLAST RADIUS — a bug loses somebody's books.** Must be **tenant-exact**: reuse `testdb`'s delete-plan census, which already knows every tenant-scoped table and is anti-rot by construction. Own wave slot. |
| **T15** | **Export — Excel / CSV / XML / e-mail** | 0065 | Everyone, daily | Low technically. **`authz.Export` has ZERO route mappings so it default-DENIES — that ACL decision must be made explicitly.** |
| **T16** | **Per-company print templates UI** (logo, address, footer, declarations) | 0066 | Everyone who sends an invoice | Low. The override table exists; nothing writes it. |
| **T17** | **SSO / OIDC login** | — | Any business with >5 staff | Low-medium. |
| **T18** | **Rule 48(4) QR on e-invoice** | — | Every e-invoicing business | **BLOCKED, correctly.** The QR encodes the IRP's **signed** payload; generating one from anything else forges a government signature. Ships only with IRP access. |

### 3.2 Group B — the surface NOBODY HAS ENUMERATED, and it is large

`PLAN-TALLY-PARITY.md` §7 admits Tier 2 is not exhaustive. **You cannot feature-lock against a
list you have not written.** This is that list. Numbers continue from Group A; migration blocks
continue from **0067**.

| # | Track | Who it stops |
|---|---|---|
| **T19** | **Voucher classes + optional / scenario / reversing / memorandum / post-dated vouchers** | Anyone doing forecasting or provisional entries. Tally users rely on these heavily. |
| **T20** | **Price levels, price lists, discount columns per party** | Every distributor and wholesaler |
| **T21** | **Reorder levels + stock ageing + negative-stock control** | Every business holding inventory |
| **T22** | **Multi-godown transfers with IN-TRANSIT stock** | Anyone with two locations. In-transit is a real third state, not a flag. |
| **T23** | **Manufacturing journals + job work (material in/out), rejections in/out** | Every manufacturer |
| **T24** | **Job costing / project costing** | Contractors, agencies, project businesses. Interacts with T2. |
| **T25** | **POS / retail billing** (POS invoice, multi-mode payment, cash drawer) | Every retail counter |
| **T26** | **Payroll attendance, leave, production types, gratuity, loans & advances, Form 16** | Every employer. Payroll ships without attendance today. |
| **T27** | **Cheque printing + cheque book management + payment advice** | Everyone who pays by cheque. `RESEARCH.md:881–885` has the full spec including per-bank formats. |
| **T28** | **Credit limits + credit-control blocking on sale** | Anyone extending credit |
| **T29** | **Bank statement import + auto-matching** (T4 slice 2) | Everyone who reconciles. **Memo written: `docs/design/t4-slice2-model.md`.** Migrations 0062–0064. |
| **T30** | **MIS: ratio analysis, cash flow, funds flow, comparative & multi-period columns** | Every owner and their accountant. Cash Flow is already a *named* report answering 501. |
| **T31** | **Multi-company / group company consolidation** | Anyone with two entities |
| **T32** | **Data import FROM Tally** (XML / ODBC) | **EVERY SINGLE PROSPECT.** Nobody switches without their history. |
| **T33** | **ODBC / Tally.NET-style remote access / data synchronisation** | Multi-branch businesses |
| **T34** | **Composition scheme, RCM, ISD, e-invoice cancellation windows** | The GST long tail |

> **T32 IS A COMMERCIAL BLOCKER DRESSED AS A FEATURE.** A perfect Tally clone that cannot read
> a Tally backup has no migration path and therefore no users. It belongs early, not last.

### 3.3 Group C — the papercuts that only a real day of use will find

`PLAN-TALLY-PARITY.md` §7 is right that nobody has driven this app as a shopkeeper would. Two
of the most-used buttons in the product were found missing that way, not by reading code.
**Budget one real business day before declaring parity** — see §7.

---

## 4. SEQUENCE

Ordered by **who it unblocks**, not by size. Each row is one working unit that must reach all
seven layers of §1.

| Order | Work | Why here |
|---|---|---|
| **0** | **§2 step 4 — the TDS/TCS register and the Form 140 / 143 returns** | Steps 1–3 are done: both taxes derive, refuse, and persist, and both are configurable from F11. **Nothing can be reported yet**, which is the last of the debt. |
| **1** | **T6** register + returns · **T29** bank import | Finishes Wave 1. Both have memos and storage already. |
| **2** | **T3** orders + BOM · **T2** cost centres | The two most-used missing features. Disjoint packages. T11/T24 depend on T2. |
| **3** | **T32 import from Tally** | Moved EARLY on purpose — see the note above. Without it there is no migration path. |
| **4** | **T9** FIFO/LIFO · **T10** stock groups/units · **T21** reorder/ageing · **T22** godowns | One inventory push. **T9 moves the Balance Sheet — re-pin `verify:demo`.** Serialise T9+T10. |
| **5** | **T15** export · **T16** print templates · **T27** cheque printing · **T30** MIS reports | The "daily annoyance" band. High satisfaction per unit of work. |
| **6** | **T19** voucher classes · **T20** price levels · **T28** credit limits | The entry-speed band that makes an operator willing to switch. |
| **7** | **T5** e-Way Bill · **T12** GSTR-9 · **T34** GST long tail | Statutory completeness. |
| **8** | **T8** multi-currency · **T13** interest · **T11** budgets · **T24** job costing | Depth for larger businesses. |
| **9** | **T23** manufacturing · **T25** POS · **T26** payroll attendance | Vertical-specific. Each unlocks a whole customer segment. |
| **10** | **T14** backup/restore/split · **T31** consolidation · **T33** ODBC/sync · **T17** SSO | Infrastructure. **T14 gets its own slot — a bug here loses books.** |
| **11** | **Group C usability pass** | The papercuts. Then, and only then, parity may be claimed. |

**Parallelism:** research, memos, reviews and audits fan out perfectly. **Code does not** unless
packages are disjoint. `server.go`, `auth_middleware.go`, `authz/vocabulary.go`, the six
`keystroke/` files, `defaultScreens.tsx`, `AppShell.tsx` and `FeaturesScreen.tsx` are touched by
**every** track — one owner per wave, serialised at the end. The machine is 8 cores; ~6 agents
run concurrently; **one workflow at a time.**

---

## 5. THE TRAPS THAT HAVE ALREADY BITTEN. Do not re-derive these.

### 5.1 Domain traps, each of which corrupted something real

- **An order is a COMMITTED QUANTITY, NOT A MOVEMENT** (T3). Through `stock_movement` it
  corrupts closing stock and the Balance Sheet.
- **Cost-centre allocations live INSIDE the hashed payload** (T2) and the sum invariant is a
  constraint trigger.
- **Closing stock feeds the Balance Sheet** (T9) — re-pin `verify:demo`.
- **`posting` is HARD IMMUTABLE and an ALTER re-creates every posting row.** Anything keyed on
  `posting.id` goes silently stale on the first correction. Key on `voucher_id`, and store what
  you matched so an alter is **detectable** (migration 0045 is the worked precedent).
- **The printed Day Book lists voucher VERSIONS**, so an altered voucher contributes twice.
  Still an open product call.
- **Money is `NUMERIC(19,4)`, direction is `dr_cr`, never a sign.** No `float64`, no
  `parseFloat`, anywhere on a money path.
- **Tolerances are how a real error hides for a year.** Reconcile to the paisa.

### 5.2 The twelve registration points — silent failures, guaranteed under fan-out

**A new RESOURCE needs 4** (miss one ⇒ default-DENY for *every* level including Owner):
`routeAction()` · `authz.KnownResources` · `authz.KnownReportNames` · `reportName()`.

**A new SCREEN needs 8:** `ScreenKind` · `corpora/validate.py` `KNOWN_SCREENS` (fails
`make verify` — the gate that catches you) · `defaultScreens` · `buttonBar` (else it falls
through to `GATEWAY_BAR` and **no test catches it**) · `AppShell` `COMPANY_SCOPED_SCREENS` (else
`useCompanyID()` throws) · `keymaps` `screenBindings` (**an unbound key is not inert — global
`F5` opens a Payment voucher and `F9` a Purchase**) · `buttons.test.ts` `SCREEN_PATHS` ·
`actionLabel()`.

**An ACL migration needs 2:** backfill existing companies **and** extend
`seed_company_auth_defaults`. Write the test that **creates its company at test time**.

### 5.3 What cannot be reached from this environment — never claim otherwise

The FVU (no JRE), the GSTN portal, the NIC e-Way Bill API, the IRP, the EPFO/ESIC portals, and
the TIN `.csi` challan file. **Ship the deterministic-fake + recorded-schema arrangement and say
so on the artefact's face**, as `internal/statutory` already does for IRN.

---

## 6. PHASE 2 — WHAT MAKES THIS WORTH BUILDING

Parity gets a user to switch. **This is why they stay**, and it is the part Tally has never
done. Tally shows you what happened. It does not tell you what it means, what is about to go
wrong, or what to do about it.

The foundations already exist here and are unusual: a **hash-chained immutable audit trail**,
**conversational NL query** over the books, **OCR bill capture**, and a **forecast** engine.

| # | Capability | The question a business owner actually asks |
|---|---|---|
| **P1** | **Cash-flow runway and forecast** | *"Will I make payroll next month?"* — from real receivables ageing, payables, recurring costs and seasonality. |
| **P2** | **Working-capital diagnostics** | *"Why am I profitable on paper and broke in the bank?"* — the single most common SMB confusion. Debtor days, creditor days, stock days, the cash conversion cycle, and which one moved. |
| **P3** | **Customer and product profitability** | *"Which customers actually make me money?"* — after discounts, credit period and collection cost. Many businesses' biggest customer is their least profitable. Needs T2. |
| **P4** | **Collections intelligence** | *"Who is about to not pay me?"* — payment-behaviour scoring from history, and a prioritised chase list. |
| **P5** | **Anomaly + fraud detection** | *"Is anything wrong in my books?"* — duplicate payments, round-number invoices, out-of-hours entries, unusual vendor patterns. **The audit trail makes this uniquely credible here.** |
| **P6** | **Tax optimisation and early warning** | *"Am I about to cross a threshold?"* — registration, composition, audit, TDS liability. Warn **before** the crossing, not in the return. |
| **P7** | **Purchase and inventory intelligence** | *"What is my money stuck in?"* — dead stock, reorder timing against real lead times, price-trend alerts. |
| **P8** | **Benchmarking** | *"Is 8% margin good in my trade?"* — anonymised, aggregated, opt-in. Impossible for on-premise Tally by construction. |
| **P9** | **Ask anything, in any language** | Extend the existing NL query to *why* questions, not only *what*. |
| **P10** | **A daily briefing** | Three sentences each morning: what changed, what needs a decision, what is about to break. |

**The sequencing rule:** every P-track reads from books that are already correct and complete.
**They are worth nothing before parity**, because their answers are only as good as the data —
which is the real reason §1's bar matters.

---

## 7. HUMAN TASKS — the session cannot do these

| # | Task | Value |
|---|---|---|
| **7.1** | **Run one real business day on it.** Key real vouchers, reconcile a real statement, print a real invoice, write down every annoyance. | **HIGHEST ITEM IN THIS FILE.** Everything else was found by reading code, which finds *missing tables* — not what is unpleasant. Two of the most-used buttons in the product were found exactly this way. |
| **7.2** | **A real Tally backup file** (any version) to build and test T32 against. | Blocks the commercial migration path. |
| **7.3** | **A JRE + RPU 1.1 zip** (Protean/TIN) so the FVU can actually validate a return. | Stops us saying "generated, not validated". |
| **7.4** | **EPF/EDLI administrative charges** — no standalone rate notification existed as at 2026-07-30. **Do not substitute `S.O. 2701(E)`** (inspection charges, different levy). | ~₹500/establishment/month accruing, correctly refusing. |
| **7.5** | **Sample Form 140 Q1–Q4 file** to diff our output against. | Optional. |

---

## 8. OPEN PRODUCT CALLS — decide these, they are blocking small things

1. **Printed Day Book lists voucher VERSIONS** — an altered voucher appears twice. Deliberate,
   differs from the JSON report, needs a ruling.
2. **Data Entry gets no report print at all** (migration 0038 seeds zero `allow print report:`).
   Now user-visible since Ctrl+P exists. Which reports may that level print?
3. **`authz.Export` has zero route mappings** — default-DENY. Who may export?
4. **A party's address cannot be ALTERED** — `POST /ledgers` sets it, nothing changes it.
   Needs `masters.SetLedgerAddress`, a `PUT`, and an ACL decision.
5. **Rule 46(e)** (≥₹50,000 supply to an unregistered party) may already work now `gststate`
   exists — **verify before building**.
6. **`handler_print_reports.go` duplicates `printingdb`'s unexported `loadSupplier` verbatim.**
   Export one, delete the copy, or they drift.
