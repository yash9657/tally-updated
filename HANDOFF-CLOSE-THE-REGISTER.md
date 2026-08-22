# HANDOFF — CLOSING THE REGISTER: the last 21 defects

Written 2026-08-21, after L5 and L13 closed. **88 defects registered, 67 closed, 21 open.**

`HANDOFF-PARITY-AUDIT.md` §7.1 is the register and its header is the live count. This file is the
PLAN for the remaining rows: what each one actually is (verified against the code on 2026-08-21,
not copied from the register), what order to do them in, what the oracle is, and which four are
blocked on a decision only the user can make.

`.claude/HANDOFF.md` stays the SESSION handoff — the stack commands, the harness, the gotcha list,
and whatever is in flight. Read it first; this file is what to do next after it.

---

## 0. The bar, and the one rule that governs this whole plan

**Acceptance per row — BOTH proofs, no exceptions:**

1. an **executed** mutation proof whose failure was **predicted then observed**, and
2. a drive in a **real browser** (or on the wire, for a server-side defect) against a cored binary
   **verified fresh by symbol**, with the **books read back**.

Then: §7.1 row marked, §7.5 drive ledger appended, committed. **One lane per commit.** Commit the
fix/probe **BEFORE** mutation-proving it (a proof's `git checkout --` once wiped an uncommitted fix).

### ⛔⛔ READ THE CODE BEFORE PLANNING A FIX. THE REGISTER DESCRIBES THE SHIPPED CODE.

**Both lanes closed on 2026-08-21 were already built and had simply never been driven.** L5's four
fixes were all in `internal/query`; L13's three were all in `MasterCreateScreen` +
`MASTER_GOTO_ENTRIES`. In both lanes the real work was (a) the drive, and (b) the ONE thing driving
found that reading could not.

Every row below carries a **VERIFIED** line saying what was actually checked on 2026-08-21 and
what was taken on the register's word. Re-verify anyway before you plan: the cost of checking is a
grep, and the cost of not checking is a session spent re-fixing something that works.

### ⛔ The second rule: a probe that passes first try is the one to distrust

Gotcha #2 has now fired eighteen-plus times. L5's probe went 41/41 first run and L13's went 33/33
(after one self-inflicted failure) — so both were re-run against a **deliberately broken product**
(`/tmp/proofs-l5-e2e.py`, `/tmp/proofs-l13-e2e.py`: mutate → rebuild + restart cored → re-drive).
Both reproduced the register's own numbers. **Do that for every lane below.** A frontend mutation
needs only a beat for vite's HMR; a Go mutation needs a rebuild AND a restart, or the running
binary reports STILL GREEN and you conclude the probe is not an oracle.

### ⛔ The third rule: a probe that writes must clean up after itself

L13's first runs took `verify:demo` from 34/34 to **29/34** — a one-sided opening balance
unbalances the Trial Balance, and masters have no keyboard delete. `drive-l13-masters.mjs` now
removes exactly what it created, by name, after reading it back, and then asserts the Trial Balance
**reports itself `balanced`**. Copy that ending.

---

## 1. The 21, in the order to do them

| Wave | Rows | Why this order |
|---|---|---|
| **1 — L4, TDS/TCS** | D1, D2, D3, D4, D88 | **Already built this session.** Highest value per unit of work; four of the five converge on ONE design step, so splitting them costs more than doing them together |
| **2 — the cheap honest builds** | D5+D43, D22+D52, D80 | Each is small, each has a clean oracle, and each pairs a capability with a screen that currently lies about it. Do them as three commits, not five |
| **3 — the judgement rows** | D82, D85, D86, D87 | ✅ **ALL FOUR DECIDED 2026-08-21 — unblocked.** §4 records the decisions and what each one costs |
| **4 — the structural ones** | D84, D49, D81, D83 | Each needs a GATE or an architecture, not a patch. D84 first — it is a source of false green for every lane after it |
| **5 — the numbers-moving and the low-value** | D8, D77, D72 | D8 **moves the Balance Sheet** and must be serialised with T9. D77 and D72 are LOW and are honest last |

Plus **§7.3 B, C, D** — three traced-but-unfixed items outside the register (see §5).

---

## 2. WAVE 1 — L4: TDS/TCS. **BUILT THIS SESSION, NOT DRIVEN.**

⛔ **D1 and D3 are committed as `build(...)`, not `drive(...)`, and every L4 row is still marked
⛔ OPEN in §7.1. That is deliberate. They have unit tests and a wire walk-through, NO browser drive
and NO mutation proof. Do not mark the register on the strength of a commit.**

### The chain works end to end — walked on the wire 2026-08-21

```
PUT  /deductor-status/2025   {"entity_category":"COMPANY","prior_year_turnover_rupees":"",
                              "prior_year_activity":"","recorded_by":"…"}          -> 200
POST /vouchers/journal       Dr Fees 200000 / Cr Bank 180000 / Cr TDS Payable 20000
                             + tds{194J-P, deductee, 200000, tds_ledger_id}        -> 201
                             response: tds{applied:true, rate_pct 10, tds 20000.0000}
GET  /tds-challans?tax_year_start=2026   -> allocatable_deductions NO LONGER EMPTY
POST /tds-challans           25000 of tax paying the 20000 deduction               -> 201
                             unallocated 5000.0000        <- D1's trap state
POST /tds-challans/{id}/allocations   the second deduction, 5000.0000              -> 200
                             unallocated 0.0000           <- D1's ORACLE. IT FELL.
```

⛔ **THE PRECONDITION NOTHING ON SCREEN ASKS FOR: a DEDUCTOR STATUS for the tax year PRECEDING the
voucher's.** Without it the post is refused outright. `seed:demo` writes none. ⚠️ `COMPANY` is a
specified person WHATEVER its turnover, so a turnover figure against it is REFUSED — send `""` for
both turnover and activity.

### D1 — no route attached an allocation to an existing challan

**VERIFIED:** server route `POST /tds-challans/{id}/allocations` → `handleAllocateToTDSChallan`
exists and re-reads the challan into the response. The client half landed this session
(`b9cdd05`): `allocateToTDSChallan` on `client.ts` / `rest.ts` / `mock.ts`, and an **`Alt+A`**
panel on `TDSChallansScreen`.

**THE ORACLE IS THE FALLING BALANCE, NEVER THE STATUS CODE.** Testid
`tds-challan-unallocated:{challan_no}`. A screen reporting success off the 200 looks identical
whether it moved anything or not. The panel also prints the BEFORE figure
(`tds-allocate-before`) so an operator can see it fall.

**Drive:** Alt+A → pick the challan → tick a deduction → Ctrl+A → the register re-reads and the
figure falls. Plus: the empty-tick refusal, and the already-paid row carrying **no checkbox** (so
the 409 is unreachable from the keyboard — the 409 itself is pinned at the client level instead).

### D2 — nothing compared allocations against the challan's tax box

**VERIFIED:** `ErrChallanOverAllocated` exists in `internal/masters/tds_challan.go`, tolerance is
**one rupee PER ALLOCATION** (not an equality — a deposit is whole rupees and a deduction is 4-dp),
and it is a **separate sentinel** from `ErrTDSAllocationMismatch` on purpose. The mock mirrors it.

**Drive:** that attaching one deduction at a time cannot get past a cap the create applies in one
shot. ⚠️ The server takes `FOR UPDATE` on the challan so two CONCURRENT attaches cannot each pass a
cap their sum breaks — **there is no concurrency in the mock, so that guard is only drivable on the
wire.** The mock says so at the method.

### D3 — a deduction could not be keyed from the keyboard. **TDS HALF DONE; TCS HALF NOT.**

**VERIFIED / built this session (`bdc04a5`):**
- `GET /v1/companies/{co}/tds-sections` serves the section vocabulary with rate, threshold, basis
  and **both** no-PAN rules. ⛔ Mapped as a **master**, not a report: Data Entry keys the vouchers
  that carry a deduction and is denied `report:tds-register` by design, so a report mapping would
  deny the picker to the one level that needs it.
- A **golden** (`internal/compliance/tax_sections_golden_test.go` → `frontend/src/api/goldens/
  tax-sections.json`) pins the mock's copy — the only copy of that table outside Go.
  `UPDATE_GOLDENS=1 go test ./internal/compliance/ -run TestTaxSectionsGolden` regenerates it.
- `frontend/src/screens/vouchers/taxDeduction.ts` — the pure half: form state, every refusal, both
  request blocks. `TaxDeductionPanel.tsx` — the UI, **on the double-entry grid only**.

⛔ **THE LEG IS NAMED AND THE SERVER VERIFIES IT.** A `tds` block alone records the statutory
deduction and posts nothing — the books would say the whole sum was paid while the return says a
fifth was withheld. The panel asks WHICH LINE carries the withheld tax, offered from the grid the
operator just keyed, and sends it as `tds_ledger_id`. A named leg that is not on the grid is
REFUSED (an id matching no posting makes the verification sum zero). Leaving it empty is legal and
is DISCLOSED, not refused.

⚠️ **Caught by driving, not review:** the kind was first inferred from `primarySide`, which put a
tax-**COLLECTED** panel on the Journal — whose `primarySide: "DR"` has nothing to do with the
direction of money. It is now an explicit `statutoryTax` field per voucher class.

**WHAT REMAINS — and it is a design step, not a copy-paste:**

> A collection must be **CHARGED to the buyer**, so the sales invoice needs a TCS line. The GST
> invoice form **synthesises its postings** rather than taking a Dr/Cr grid, so there is no line
> for the operator to name. `statutoryTax` is `null` on sales for exactly that reason, with the
> reason at the field. Offering the block there without a leg would produce invoices that
> under-charge the buyer while the return says otherwise.
>
> **Two ways out, and the choice is the work:** (a) teach `buildGstVoucherRequest` to emit a TCS
> line from the derived collection, the way it already emits the three tax ledgers — which means
> the client computes an amount the server owns, and that is the thing this codebase refuses
> everywhere else; or (b) give the invoice form an explicit "additional charge" line the operator
> keys, which the buyer is then really charged. **(b) is consistent with how TDS was done.**

**D3 and D4 converge here. Do them together.**

### D4 — the TCS half of the challan mechanism

**VERIFIED:** the server half is DONE — `AllocationKindTCS`, `ErrMixedChallanKinds` ("one challan
cannot pay both"), and the allocatable read LEFT JOINs `tcs_collection`. **What is missing is a
collection to allocate**, which is D3's remaining half.

**Drive:** a collection keyed from the keyboard reaching the picker, and one challan refusing to
pay both a deduction and a collection.

### D88 — altering a voucher silently deletes its TDS deduction ⛔ NEW, registered this session

**VERIFIED:** `internal/ledger/tds.go` `deleteTDSDeductions` runs on EVERY alter and the row is
re-derived from the body — correct on its own terms ("a voucher altered to carry no TDS at all must
lose its deduction row rather than keep a stale one"). But `VoucherView` carries **no** `tds`/`tcs`,
so `formFromView` cannot re-state what it loaded: hydrate a voucher that carried a deduction, edit
its narration, Ctrl+A — and the deduction is gone, while the challan that paid it keeps an
allocation whose natural key now matches no deduction row (migration 0069 carries no FK by design).

**Fix:** serve the stored deduction on the voucher GET, then hydrate it in `formFromView`. The
field already says so in a comment. **Land it with D3's TCS half, not ahead of it** — it is only
reachable now that a deduction can be keyed at all.

**Drive:** key a deduction, alter the narration, re-read `tds_deduction` — the row survives — and
the challan allocation still reports `deduction_on_record: true`.

---

## 3. WAVE 2 — the cheap honest builds

Three commits, five rows. Each pairs a capability with a screen that currently overstates it.

### D5 + D43 — `allow_negative_stock` is honoured by both enforcers and writable by NOTHING

**VERIFIED 2026-08-21:** `internal/ledger/stock.go:358` reads
`SELECT allow_negative_stock FROM company WHERE id = $1` and honours it. `grep` across
`backend/internal/` finds **no writer at all** — no route, no request field, no setter. And
`POST /v1/companies` accepts the key and **discards it at 201** (D43), so the caller is told a
setting was applied that never was.

**The work:** add the field to `companyReq` and a `PUT`/`PATCH` (or fold it into the company
profile write that already exists), then a control on the Features/company screen. **Do the two
rows in ONE commit** — D43 is D5's symptom, and fixing the writer without fixing the silent
discard leaves a 201 that still lies about a body it ignored.

**Oracle:** flip the flag, then drive an issue that takes stock below zero and see it ACCEPTED;
flip it back and see the same issue REFUSED. Read the flag back from `company`, not from the
response.

### D22 + D52 — no cash-flow screen at all, while `FeaturesScreen` advertises it as `on`

**VERIFIED 2026-08-21:** `grep -rn "cash-flow" frontend/src/` finds **nothing** outside tests.
`FeaturesScreen.tsx:351` still advertises "Cash-flow projection and anomaly detection", and the two
AI-native rows immediately above it name their doors ("Gateway > Bill Inbox", "Gateway > Query")
while this one names none.

**The cheap route is a `report:NAME` frame, not a new `ScreenKind`.** The server route and the PDP
tokens already exist — `/v1/companies/{co}/cash-flow` was driven green for D23 on 2026-08-20 and
carries its own `estimate`/`method`/`basis` disclosure, which the screen must render (that
disclosure is what D23 was).

**Do D22 and D52 in ONE commit:** the screen and the sentence that describes it. A door built
without correcting the note leaves the note still naming no door; a note corrected without the door
is worse.

### D80 — `Alt+G > Vouchers` pushes the Unwired screen

**VERIFIED 2026-08-21:** `GOTO_ENTRIES` is `["Vouchers", ...MASTER_GOTO_ENTRIES, ...REPORT_NAMES]`.
`resolveGotoSelection` matches the master labels first, then falls through to
`canonicalReportName(selectedItem) ?? selectedItem` — and `canonicalReportName("Vouchers")` returns
**undefined** (its own comment says so), so the raw label becomes a `reportName` and the frame
renders `UnwiredReportScreen`. The one entry that has been in that picker since the beginning is
the one that does not work.

**The work:** an arm in `resolveGotoSelection` before the report fallback, exactly like the
master-kind arm above it — `"Vouchers"` should push the voucher frame (or the Day Book, if that is
what Tally does; check RESEARCH.md before choosing). **The comment above the master arm already
explains why order is load-bearing here; add to it rather than restating it.**

---

## 4. WAVE 3 — the judgement rows. ✅ **ALL FOUR DECIDED BY THE USER 2026-08-21. UNBLOCKED.**

The four were put to the user as options with a recommendation each; **all four came back as the
recommended option.** The decisions are recorded here because they are the AUTHORITY for what the
code should do — a later session that disagrees with one of these is disagreeing with the user, not
with a previous session's guess, and must ask again rather than re-decide.

### D82 — ✅ DECIDED: the HSN refusal on the ALTER path is WORKING AS INTENDED

An old sales invoice stored without an HSN cannot be re-saved without keying one. **Close the row
as correct**, and make the refusal say WHY: the invoice is unprintable without an HSN (Rule 46(g)),
so keying it is the fix rather than a hoop.

**The work is one sentence, not a behaviour change.** `HSN_REQUIRED_CLASSES` in
`VoucherScreen.tsx` stays exactly as it is. Mark §7.1 as working-as-intended with the user's
decision named, and pin the SENTENCE in a test so a later session cannot quietly relax the refusal
believing it was never wanted.

⚠️ It still needs a drive: the refusal has to REACH the operator on an alter, and be readable.

### D85 — ✅ DECIDED: key the duplicate-bill guard on a PRE-REDACTION HASH

`draftAccept.ts` `assertNotAlreadyOnTheBooks` keys on the **post-redaction** `voucher_no`, so every
PII-shaped invoice number collapses to `[PAN REDACTED]` and two genuinely different bills from one
supplier on one date read as the same bill.

**Hash the invoice number BEFORE redaction and key on the hash.** Genuinely different bills stay
distinct, and no PII is stored — a hash is not reversible to a PAN.

⛔ **THE HASH MUST BE TAKEN ON THE PII BOUNDARY'S OWN INPUT, NOT RE-DERIVED DOWNSTREAM.**
`insightd/insightd/pipeline.py` is where the raw OCR still exists (`self.pii.redact(ocr)`); anything
after it has only the redacted text. So the hash travels WITH the draft as a field, and the
frontend never sees the raw number. ⚠️ That makes this a change to the insightd wire shape, not a
one-line client edit — and `pii_boundary_strict` plus the M23 mutation proof
(`/tmp/proofs-l11.py`) both guard that boundary. Re-read them before touching it.

⚠️ And the refusal's advice must change with the key: "key its own supplier invoice number" is only
followable once the number is what is actually compared.

### D86 — ✅ DECIDED: OUTSTANDINGS stops bounding at today

Measured: BS **109950.0000** vs Outstandings **108770.0000**, short by exactly one 1180.0000 bill
dated 2026-08-27 with today at 2026-08-20.

**Include future-dated bills in Outstandings so the two agree.** ⛔ **This deliberately does NOT
move the Balance Sheet, so no `verify:demo` re-pin** — which is the whole reason it was preferred
over bounding the Balance Sheet (that carries D8/T9's constraint).

⛔ **THREE PLACES MOVE TOGETHER, and missing one re-opens D20:**
1. `internal/reporting`'s Outstandings — the `vv.voucher_date <= as-on` guard;
2. **`internal/query/whitelist.go`'s `EntityOutstandings` skeleton**, which was made to MIRROR the
   report in D20's fix and carries the same bound (`outstandingAsOnExpr`) plus the books-beginning
   guard on the opening side. **If the report changes and the DSL does not, the NL answer and the
   report disagree again — which IS D20.** `drive-l5-nlq.mjs` will catch it;
3. the caption. The ageing report now shows bills that are not due yet, and
   `QueryScreen`'s `measureBasis` currently states "Bills dated in the future are not outstanding
   yet and are excluded" — a sentence that becomes FALSE the moment this lands.

⚠️ `verify:demo`'s own check passes both reports NO `as_on` and asserts they agree exactly; it
should keep doing so, and it becomes a real gate for this fix rather than a coincidence of a seed
with no future dates.

### D87 — ✅ DECIDED: GSTIN + state code + registration type, and **state_code is MANDATORY under party groups**

**VERIFIED by driving 2026-08-21:** the form renders none of the three, so every party keyed from
the keyboard reads back `(null)|(null)|UNKNOWN` — the exact input that made D19 derive an
intra-state bill as IGST — and `verify:demo`'s party checks fail on it.

**Add the three fields to the ledger master form, and REFUSE a create under Sundry Debtors /
Sundry Creditors with no state code.** ~~The address block, PAN, credit days and bank details stay
out for now~~ — **AMENDED BY THE USER 2026-08-22 (L15): the form also carries PAN and CREDIT DAYS**,
both optional. PAN because it is statutory identity and sits beside GSTIN; credit days because it
drives Outstandings ageing, which is blind on every hand-keyed party without it. **The address
block, ITC eligibility, blocked clause and bank details STILL stay out** (`CreateLedgerRequest`
carries all of them; the form does not have to). So the form's scope is exactly FIVE new fields:
`gstin`, `state_code`, `registration_type`, `pan`, `credit_days` — one of them mandatory.

⚠️ **THIS REFUSES CREATIONS THAT SUCCEED TODAY, deliberately and on the user's instruction.** Two
consequences to handle rather than discover:
- the refusal must fire **client-side, while the figures are on screen** — the same rule the
  opening-breakup check follows, because `masters.CreateLedger` creates before it can be refused;
- ⛔ **`Alt+C` FROM A VOUCHER FIELD reaches this form**, so a party created inline now needs a state
  code too. That is the D19 path and is the point — but it means the inline create can now be
  refused where it never was, and D47's disclosure (the field names a master that does not exist)
  is what carries that. Drive both doors.

### D90 — ✅ DECIDED 2026-08-22 (L15): REFUSE a duplicate STOCK ITEM name **NOW**, naming the existing item

The user was asked whether to refuse now or defer into T10 (stock groups), given that Tally allows
same-named items under different stock GROUPS and this build has none. **They chose to refuse now.**

**The reasoning that was put to them and accepted:** with no stock groups, a duplicate name is a pure
data defect — two indistinguishable rows in every item picker, and one item's movements and valuation
split across two masters, each weighted-average costed on half the history. The relaxation T10 needs
later is `UNIQUE (company_id, name)` → `UNIQUE (company_id, group_id, name)`, which is a **widening**
migration: it admits rows the narrow constraint refused and can never fail on existing data. Widening
later is cheap; de-duplicating a year of split valuation history is not.

⚠️ **The mock does the OPPOSITE today** (create-or-select returns the existing id), and that is
recorded as a `deliberate_divergence` in `corpora/mock-vs-cored/masters.json`. When cored refuses,
**that entry must be DELETED and replaced by a real expectation row**, or the D84 gate keeps
certifying the behaviour this decision overturns.

## 5. WAVE 4 — the structural ones

### D84 — `api/mock.ts` is looser than cored and is a SOURCE OF FALSE GREEN ⛔ DO THIS FIRST

Three lanes have hit it. It accepts an empty ledger name and an unparseable `gst_rate_pct` that
`server.go` 400s on, reproduces D44 verbatim, skips D6's party-group and D69's due-date checks, and
does not mirror `createLedger`'s opening-breakup refusal.

**The register is explicit that this needs a STRUCTURAL GATE, not three patches.** Two shapes worth
considering, and the second is the one this codebase already reaches for:

- a **contract corpus**: a table of (request, expected status, expected sentence) run against BOTH
  the mock and a live cored, failing when they disagree. `corpora/validate.py` already exists and
  `make verify` already runs it;
- a **Go-emitted golden** per refusal, like `tax-sections.json` and `tds-register-prose.json` — the
  pattern used twice already, including once this session.

⚠️ Every later wave is measured through this mock. Fixing it first makes the rest trustworthy;
fixing it last means re-running everything.

### D49 — insightd has NO AUTH and scopes drafts by caller-supplied `company_id` alone

**VERIFIED 2026-08-21:** `GET :8088/healthz` still answers `"authentication":"none"`, and a test
pins that known-bad value. vite proxies it same-origin at `/companies/*`, so any `company_id` lists
and fully hydrates another tenant's drafts — supplier names, invoice numbers, dates, ledger names,
amounts, GST state codes.

**The recommendation on file is Option B: cored proxies.** insightd stays network-simple and every
request inherits the PDP and RLS that already work. ⚠️ The `/healthz` disclosure and its test move
WITH the fix — a health endpoint that still says `none` after the fix is a worse lie than before.

### D81 — a SERVER-side refusal still leaves masters behind on the voucher path

**VERIFIED:** there is **no `DELETE /ledgers`** (the only Deletes are voucher, bank-recon, invites,
members, security-levels), so a posting's ledger cannot be rolled back — a posting needs a real
uuid, and `resolveMasters` creates before the server can refuse.

The client-side gate closes the refusals the FORM can make (D39's lesson, applied on both grids and
again on the master form this session). What remains is the refusals only the SERVER can make:
cutoff, ACL, duplicate number, negative stock.

**The draft path already solves this the honest way** — `DraftAcceptFailedError` DISCLOSES its
orphans. The voucher path does not. **Cheapest correct fix: disclose, do not delete.** Name the
ledgers that were created for a voucher that was then refused, so the operator can go and remove
them. (A `DELETE /ledgers` is a much larger decision — a ledger with postings must never be
deletable.)

### D83 — concurrent duplicate manual bill numbers

Two SIMULTANEOUS posts with the same hand-typed bill number on one party can both pass the
uniqueness check. **⚠️ The sequential case — what an operator actually produces — is already
closed.** It needs an index `bill_allocation` cannot carry as-is, because duplicate names are legal
for AGAINST / ON_ACCOUNT / caller-named openers.

**Likely shape:** a partial unique index over `(company_id, ledger_id, ref_name)` **restricted to
`ref_type = 'NEW'`**, plus the typed error to name it. Verify the ref-type semantics in the
migration before writing it. LOW frequency, real money if it lands.

---

## 6. WAVE 5 — the numbers-moving and the low-value

### D8 — closing stock replayed in LEXICAL `voucher_no` order ⛔ T9 WORK. DO NOT TOUCH CASUALLY.

`inventory/valuation.go:155-170`: `if a.VoucherNo != b.VoucherNo { return a.VoucherNo < b.VoucherNo }`.
Voucher numbers are bare integers with an INDEPENDENT counter per type, so on a shared date
`"10" < "9"` and two series interleave arbitrarily — an issue keyed after a receipt on the same
date is costed at the **pre-receipt average**. Confirmed to the paisa in a browser: Closing Stock
25,909.3478 for MS Angle where entry order gives 25,307.8797, **601.4681 too high**.

⛔ **NOT FIXED ON PURPOSE, three sessions running, and the reasons still hold:** it changes the
valuation/movement primitive, which §5 requires be serialised with **T9 FIFO/LIFO**, and it **MOVES
THE BALANCE SHEET**, so it needs a deliberate `verify:demo` re-pin. **Do it as part of T9.**

### D77 — posting order has no `p.id` tiebreak

**VERIFIED 2026-08-21:** `internal/ledger/ledger.go:847` reads `ORDER BY p.dr_cr DESC, p.amount DESC`
with no `p.id`, so `posting_seq` is arbitrary for two same-side same-amount postings — while the
analogous ordinal elsewhere in the codebase uses `ORDER BY p.id`. LOW: no data loss was observed,
only nondeterminism.

**One line.** The oracle is a voucher with two identical same-side legs read back twice, and cost
allocations keyed against `posting_seq` still landing on the same leg.

### D72 — insightd's statistical forecast: unreachable, and money as JSON floats

`routes_forecast.py` types `weekly_net_cash: list[float]` and `daybook: list[tuple[str, float]]`.
Wiring it as-is would reintroduce paisa loss into a system that pins fixed-4 strings everywhere.
⚠️ It does at least declare `estimate: true` and its method — **the disclosure cored's own
projection lacked until D23**.

**Do it after D22's cash-flow screen exists** (there is nowhere to render it before that), and
**convert the wire to fixed-4 strings as part of the wiring, not after.**

---

## 7. §7.3 B, C, D — outside the register, still open

| | What | Shape of the fix |
|---|---|---|
| **B** | ⛔ **THE SEVENTH DOOR ON THE DRAFT ACCEPT — one supplier bill, two vouchers, REPRODUCED IN A BROWSER.** The CONFIRM (`POST /drafts/{id}/accepted`) is fire-and-forget and only `console.warn`s, so if it fails after the voucher lands, the only DURABLE record of the acceptance is lost and the claim's TTL becomes the duplicate's timer. Driven: browser A posted purchase 6, its confirm was aborted at the network layer, and the operator was told "Saved" | a durable pending-confirm retry. ⚠️ The race the fix was built for IS closed (two browsers 13ms apart → ONE voucher); this is the door that remains |
| **C** | A `?company=` that fails the client-side uuid regex is **silently discarded and somebody else's books are opened instead** — real, populated books, nothing on screen looks wrong | refuse legibly, mint nothing. Same class as two defects that WERE fixed |
| **D** | A well-formed but NONEXISTENT `?company=` uuid **wedges permanently** — the documented 400/404 recovery valve never fires because cored answers **403** for a company that does not exist (membership is checked before any handler can 404), so `isDeadCompanyId`'s 404 arm is dead code against this backend | either make the 403 recoverable client-side, or have cored distinguish "not a member" from "no such company" — ⚠️ the second discloses existence across tenants, so probably the first |

**C and D are the same class as D26/D27, which were fixed and hold.** They are cheap and they are
about work landing in the wrong company, which is the worst failure this product has.

---

## 8. What "done" means, and what comes after

**Done = §7.1 header reads `88 DEFECTS, 0 STILL OPEN`**, every row marked with both proofs, and
§7.5 carrying a drive-ledger section per lane.

Then the user's "once done" phase begins — **THE FEATURE GAP**, from `FeaturesScreen.tsx`, which is
the honest ledger:

- **11 rows read `off`:** T8 multi-currency, T9 FIFO/LIFO (⛔ **take D8 with it**), T10 stock groups
  + compound units, T11 budgets, T12 GSTR-9, T13 interest, T14 backup/restore/split, T16 per-company
  print templates, export-email/PDF, SSO, and ⛔ **T18 rule 48(4) QR, which is PERMANENTLY
  UNSHIPPABLE — the payload is signed with the Income Tax Department's private key. If a session
  reports T18 done, reject it.**
- **2 rows read `server`:** the payroll screens (pure frontend, no refactor needed —
  `internal/payroll` is already in the archtest allowed set) and offline sync.
- ⛔ **T32 Tally XML import is the COMMERCIAL BLOCKER and has never met a real Tally file. Ask the
  user for one EARLY** — nothing in the repo can substitute for it.

---

## 9. Where the working knowledge lives

| File | What it is |
|---|---|
| `.claude/HANDOFF.md` | **the session handoff** — stack commands, symbol checks, the harness, the 19-item gotcha list, the open questions. **Read it first.** |
| `HANDOFF-PARITY-AUDIT.md` §7.1 | **the register.** Its header is the live count |
| …§7.5 | **the drive ledger** — every closed lane's proofs, evidence and probe traps. Append one section per lane |
| …§7.3 | the four traced-but-unfixed items (A fixed; B, C, D above) |
| `HANDOFF-WAVE-A-RECOVERY.md` | what the wave landed; §4 the reachable-half queue, §5 the never-built list |
| `frontend/scripts/audit-harness.mjs` | `openApp()` — sign-in, virtual WebAuthn, `altMac()`, `net[]`. **Import it; never rebuild it** |
| `frontend/scripts/drive-l*.mjs` | 12 committed probes. **Copy the nearest one.** `drive-l5-nlq.mjs` for a read-only lane with report oracles; `drive-l13-masters.mjs` for a lane that WRITES (it tears itself down) |
| `/tmp/proofs-l5.py`, `/tmp/proofs-l13.py` | unit-level mutation runners — copy the `proof()` helper |
| `/tmp/proofs-l5-e2e.py`, `/tmp/proofs-l13-e2e.py` | ⛔ mutate → rebuild + restart cored → **re-run the browser probe**. The pattern that proves the PROBE is an oracle |
