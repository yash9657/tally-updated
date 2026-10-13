# DESIGN MEMO — T4 slice 2: bank statement import and matching

> **Track:** `t4-slice2` (Wave 1). **Migration block: 0062–0064** — from
> `PLAN-TALLY-PARITY.md` §2.5. **NOT 0047**, which is T5's e-Way Bill block;
> `HANDOFF-CLOSING-THE-GAP.md` §3.2 said 0047 and was corrected in `53d1d3c`.
>
> **Slice 1 shipped** (migrations 0045–0046): the manual bank-date loop, the statement, and
> the screen this hangs on. `docs/design/t4-model.md` is the authority for slice 1 and its
> §0.1 explains the key choice this memo inherits.
>
> **Every keystroke below is cited to `RESEARCH.md:879`**, which carries the reconciliation
> shortcuts verbatim. Nothing here is asserted from memory — `HANDOFF-PARITY-SPRINT.md` §0.2
> records that two keystrokes were invented from memory in the memo that fixed §3.1, and both
> were wrong.
>
> Invariant numbers are **local to this memo**.

---

## 0. THE ONE RULE THIS TRACK EXISTS TO HONOUR

**NEVER AUTO-CLEAR AN AMBIGUOUS MATCH.** `HANDOFF-PARITY-SPRINT.md` §3.3 states it and it is
the reason this is a design memo and not a parser.

The ambiguous case is ordinary, not exotic: two vouchers, same amount, same day — a company
paying two suppliers ₹25,000 each on the 14th, or two identical monthly standing orders. A
matcher that picks one is right half the time, and the half it gets wrong is **invisible**:
both vouchers exist, both amounts are correct, and the statement reconciles either way. The
error only surfaces when somebody chases a supplier for a payment the books say cleared.

So ambiguity is a **first-class outcome**, not a failure of the matcher. It has its own status,
it surfaces **both** candidates, and it waits for a human. `RESEARCH.md:879` supports this
directly: Tally's own tiers are **Exact, Potential, Partial, plus manual match**, and `Alt+S`
is *"potential matches"* — a list to choose from, not a decision already taken.

---

## 1. THE OUTPUT INVENTORY

This track produces **no statutory artefact**, which is why its field list comes from the
banking formats and from `RESEARCH.md`, not from a rule.

### 1.1 What an imported statement line must carry

There is no single statutory or national schema for a bank statement in India. The realistic
inputs are **CSV/XLSX exports** (per-bank layouts) and **MT940** (SWIFT). The fields below are
the intersection every real statement carries; anything richer is preserved verbatim rather
than parsed.

| Field | Source | Notes |
|---|---|---|
| value date | every format | The bank's own date. This is what `bank_date` becomes on a match. |
| transaction date | most formats | May differ from value date; stored, never conflated. |
| description / narration | every format | The only field that carries a cheque number in most CSVs. |
| amount | every format | **Magnitude only** — see §3. |
| direction (deposit / withdrawal) | every format | Two columns in most CSVs, a sign in some, `C`/`D` in MT940. |
| running balance | most formats | Stored **for verification only**, never used to derive anything. |
| bank reference / UTR | most formats | The strongest matching key when present. |
| cheque number | when present | Often only inside the description. |

⚠️ **The running balance is the one field worth importing that we must not compute from.** It
is the bank's own arithmetic and it is how a *dropped or duplicated row* is detected — if the
imported lines do not walk from opening to closing balance, the file was mis-parsed. That is a
**check**, not an input.

### 1.2 What matching produces

| Field | Why | Effective from |
|---|---|---|
| match tier — exact / potential / partial / manual | `RESEARCH.md:879` names all four | — |
| the candidate set (may be >1) | §0: ambiguity surfaces **both** | — |
| the voucher leg matched, if resolved | becomes a `bank_reconciliation` row | — |
| unmatched-in-books / unmatched-in-bank | `RESEARCH.md:873` "Books-only vs Bank-only unreconciled counts" | — |

### 1.3 The account identity — invariant I-11

`HANDOFF-PARITY-SPRINT.md` §3.3 requires bank account number and IFSC on `ledger`, **columns
AND data together**: a blank labelled field reads as "this bank has no IFSC".

They are not decoration here. **They are how an import knows which ledger a file belongs to.**
An operator importing three banks' statements in a morning must not have to remember which is
which, and a file imported against the wrong ledger reconciles nothing and corrupts two
accounts at once.

---

## 2. THE PROVENANCE TABLE

**Every "stored where" was checked against the live database on 2026-07-31** with
`information_schema.columns` on `postgres@localhost:5433/tally` (43 migrations applied, max
`0050_tds_deduction`). Not from memory.

| Field | Required by | Stored where | Verdict |
|---|---|---|---|
| bank account number | §1.3, I-11 | **nowhere** — `ledger` has `id, company_id, name, group_id, opening_balance, opening_dc, maintain_bill_by_bill, gstin, pan, credit_days, is_system, external_id, created_at, state_code, registration_type, itc_eligibility, blocked_clause, address, city, pincode` | **MIGRATION 0064** |
| IFSC | §1.3, I-11 | **nowhere** | **MIGRATION 0064** |
| the import batch (file, when, by whom, which ledger) | audit | **nowhere** | **MIGRATION 0062** |
| each statement line | §1.1 | **nowhere** | **MIGRATION 0062** |
| match tier and candidates | §1.2 | **nowhere** | **MIGRATION 0063** |
| the resolved clearance | slice 1 | `bank_reconciliation` ✅ exists (`id, company_id, voucher_id, ledger_id, leg_no, bank_date, cleared_amount, cleared_dr_cr, reconciled_by, reconciled_at`) | **OK — reuse, do not duplicate** |

### 2.1 The decision that shapes everything: matching does NOT replace `bank_reconciliation`

A resolved match **writes a `bank_reconciliation` row**, exactly as the manual loop does. It
does not introduce a second, parallel notion of "cleared".

Two reasons, and the second is the load-bearing one:

1. The Bank Reconciliation Statement, the BRS print (shipped this session) and
   `reporting.bankReconSQL` all read `bank_reconciliation`. A second source of clearance means
   two statements that can disagree, and the operator has no way to know which is authoritative.
2. **`bank_reconciliation` already carries the alter-detection mechanism.** It stores
   `cleared_amount` / `cleared_dr_cr` and the reporting layer compares them against the current
   version, marking an item **BROKEN BY ALTER** rather than silently valid
   (`0045_bank_reconciliation.sql` header). A matcher writing clearances anywhere else would
   lose that, and would lose it **silently** — which is the failure mode 0045 was built to
   prevent.

So the new tables record **the statement and the matching process**; the *outcome* lands in the
existing table. `bank_statement_line` carries a nullable FK to the `bank_reconciliation` row it
produced.

### 2.2 Migration allocation

| # | Contents |
|---|---|
| **0062** | `bank_statement_import` (batch) + `bank_statement_line` |
| **0063** | `bank_statement_match` (candidate links) + ACL for the new resource |
| **0064** | `ledger.bank_account_number`, `ledger.bank_ifsc` — **columns and a backfill path together** |

---

## 3. UNITS AND SCALE AT EVERY SEAM

| Identifier | Unit / scale | Seam |
|---|---|---|
| `AmountRupees` | rupees, `NUMERIC(19,4)` | parser → matcher → `bank_reconciliation` |
| `RunningBalanceRupees` | rupees, 4 dp, **verification only** | parser → import check |
| `ValueDate`, `TransactionDate` | calendar `DATE`, never `TIMESTAMPTZ` | a bank asserts a day, not an instant — same reasoning as `bank_reconciliation.bank_date` |
| `AmountToleranceRupees` | rupees, 4 dp | matcher config |

⚠️ **DIRECTION IS NEVER A SIGN.** Every amount in this codebase is a magnitude with a separate
`dr_cr`, and `bank_reconciliation.cleared_amount` has a `>= 0` CHECK. Statement formats vary —
two columns, a signed number, or `C`/`D` — and **the parser normalises at the boundary**. A
signed amount reaching the matcher would compare unequal to a positive voucher leg of the same
value and silently produce zero matches, which reads as "nothing to reconcile".

⚠️ **THE MATCH TOLERANCE IS ZERO BY DEFAULT AND MUST STAY EXPLICIT.** `bankrecon.go`'s header
is emphatic that the statement reconciles **to the paisa**, never to a tolerance, "and a
tolerance is how a real error hides for a year". If a partial-match tier ever needs one it is a
**named, configured, displayed** number — never a constant buried in the matcher.

---

## 4. INVARIANTS, EACH WITH THE TEST THAT PINS IT

| # | Invariant | Enforcement |
|---|---|---|
| **B-1** | An **ambiguous** match is never auto-cleared | Test: two vouchers, same amount, same day, one statement line ⇒ status AMBIGUOUS, **zero** `bank_reconciliation` rows written, both candidates returned. **Mutation: make the matcher take the first candidate; this test must fail.** |
| **B-2** | A resolved match writes a `bank_reconciliation` row and **nothing else** records clearance | Test asserting the BRS total moves by exactly the matched amount. |
| **B-3** | Re-importing the same file **does not duplicate** lines or matches | Content hash per line + unique constraint. Test: import twice, assert counts unchanged. **This is the one an operator will hit in week one**, having clicked twice. |
| **B-4** | Imported lines **walk from opening to closing balance** | Refusal test: a file with a dropped row is REFUSED, naming the row where the walk breaks — not imported with a gap. |
| **B-5** | A statement line for **another company's** ledger is refused **by the FK**, not by RLS alone | Composite `(company_id, ledger_id)` FK; cross-tenant test. |
| **B-6** | Amounts are magnitudes; a signed input is normalised at the parser | Table test over all three direction encodings, asserting identical stored rows. |
| **B-7** | Match tolerance is **zero** unless explicitly configured | Test that an off-by-one-paisa line does NOT exact-match. **Mutation: introduce a 1-paisa tolerance; this must fail.** |
| **B-8** | Unlinking (`Alt+F8`) removes the `bank_reconciliation` row **and** returns the line to unmatched | Round-trip test — the slice-1 clear/un-clear loop is the precedent. |
| **B-9** | A voucher **altered after matching** shows as BROKEN, exactly as the manual loop does | Reuses 0045's mechanism; test proves the matcher gained no exemption from it. |
| **B-10** | Every new table: RLS `ENABLE` **and** `FORCE`, policy with `USING` **and** `WITH CHECK`, composite FKs, a row in `testdb`'s `buildFixture` | `internal/security/isolation_test.go` is catalogue-driven — **verify by disarming FORCE and watching it fail by name**, as migration 0049 did this session. |
| **B-11** | Migrations 0062–0064 are **re-runnable** | `BEGIN/ROLLBACK` dry run **before first application** (the stronger proof), then `psql -f` against the populated DB. |
| **B-12** | `ledger.bank_account_number` / `bank_ifsc` arrive **with data**, not as blank labelled fields | I-11. The seed and `verify:demo` must populate them; a test asserts the demo company's bank ledger has both. |

---

## 5. THE EDGE-CASE LEDGER

| Case | Behaviour | Test |
|---|---|---|
| Two vouchers, same amount, same day | **AMBIGUOUS**; surface both, clear neither | `B-1` |
| Same file imported twice | Second import is a no-op | `B-3` |
| File with a dropped row | **Refused**, naming where the balance walk breaks | `B-4` |
| Statement line matching nothing | Stays **bank-only unreconciled** — a real category (`RESEARCH.md:873`), not an error |
| Voucher matching nothing | Stays **books-only unreconciled** | — |
| One voucher, two statement lines (part payment) | **PARTIAL** tier (`RESEARCH.md:879`); never auto-cleared |
| Two vouchers, one statement line (netted) | **PARTIAL**; never auto-cleared |
| Amount matches, date off by two days | **POTENTIAL**, not exact — cheques clear late; that is the normal case |
| Amount off by one paisa | **No exact match.** Zero tolerance | `B-7` |
| Voucher altered after matching | **BROKEN**, with what was cleared shown beside the current figure | `B-9` |
| Voucher deleted after matching | Contributes nothing; the line returns to unmatched | — |
| Statement imported for the wrong ledger | Refused if account number/IFSC disagree; otherwise it is the operator's call and is **stated on screen** | `B-12` |
| Import spanning a period already reconciled manually | Already-cleared legs are **not candidates**; no double-clearing | — |
| Empty file | Refused, naming the fact — an empty import that "succeeds" reads as "your bank had no transactions" |
| MT940 with a continuation line (`:86:`) | Description joins; the parser must not treat it as a new transaction |

---

## 6. THE KEYSTROKES — all currently bound to REFUSALS, all cited

These are already in `keymaps/tallyprime.ts` under `bank-reconciliation` as
`kind: "unavailable"` with a note. **`HANDOFF-PARITY-SPRINT.md` §3.3 requires flipping them to
real actions in the same change that builds the feature** — a shipped matcher behind keys that
still say "not built" is worse than either state alone.

| Key | Action (`RESEARCH.md:879`) | Today |
|---|---|---|
| `F8` | Modify match | `unavailable` |
| `Alt+F8` | Unlink a mistaken match | `unavailable` |
| `Alt+S` | Potential matches | `unavailable` — its note already promises the §0 rule |
| `F9` | Set status manually | `unavailable` |
| `F5` | Amount-view toggle (deposits/withdrawals) | `unavailable` — **not part of this slice**; it is a display toggle, not matching. Leave it refused and say so. |

Already real and unchanged: `F4` select bank ledger, `F2` period, `Alt+F5` detailed,
`Ctrl+H` change view.

⚠️ **`Ctrl+J` (exception report) and `Ctrl+B` (basis of values) are also at `RESEARCH.md:879`
and are bound to NEITHER an action nor a refusal on this screen.** Checked: neither combo
appears anywhere in `keymaps/tallyprime.ts`, globally or per-screen. So they are inert *today*
— which is a **latent** instance of §2.4's rule 6 rather than a live one: the moment either
acquires a global binding, this screen silently inherits it. **Bind both to refusals in this
track** while their absence is still harmless; `Ctrl+J` in particular is an *exception report*,
which is precisely a matching concept and will otherwise be reached for the week this ships.

---

## 7. WHAT THIS MEMO DOES NOT SETTLE

| # | Item | Why |
|---|---|---|
| 1 | **Which file formats ship first.** CSV is unavoidable; MT940 is well-specified and rarer; XLSX needs a reader this build does not have. | A product call. **CSV first is the recommendation** — it is what every Indian retail bank exports. |
| 2 | **Per-bank CSV layouts.** There is no standard: HDFC, ICICI and SBI all differ in column order and date format. | Needs either a mapping UI or a per-bank profile table. **This is the part most likely to make the track a three-wave one**, and it is a data-model risk, not a parser detail. |
| 3 | **Auto-create vouchers from bank entries.** `RESEARCH.md:879` records Tally offers it. | Deliberately OUT of this slice: creating vouchers from a bank file is a write to the books from an external source, and it needs its own memo. |
| 4 | **Cheque printing / cheque book management** (`RESEARCH.md:881–885`). | A separate feature that happens to live on the same screen. Not this track. |

> **§7.1 and §7.2 ARE NOW SETTLED — see §8. Items 3 and 4 remain out of scope.**

---

## 8. THE TWO PRODUCT CALLS, SETTLED — and the slice split that follows from them

Added 2026-08-02, when the track was picked up. §7 items 1 and 2 had to be decided before a
single column could be written, because both change the schema.

### 8.1 CSV ships first, and it is the only format in this track

MT940 is well-specified but rare in Indian retail banking; XLSX needs a reader this build does
not have and adding one to parse a *bank statement* is the wrong place to take that dependency.
CSV is what every bank in the country exports.

**The refusal matters as much as the choice.** A file that is not CSV is refused **by name**
("MT940 is not supported in this build"), never parsed on a best-effort basis. §1.1's field
list is the intersection of the formats; a half-understood MT940 would populate that intersection
with plausible values and reconcile to nothing.

### 8.2 ⛔ THERE ARE NO BUILT-IN PER-BANK PROFILES, AND THAT IS THE §1.1 RULE, NOT LAZINESS

`HANDOFF-FEATURE-LOCK.md` §1.1: **"Never invent a rate, threshold, clause letter, code or
keystroke. Cite it or refuse."** A per-bank column layout is the same class of fact as a rate,
and it fails in the same direction — worse, actually, because a wrong rate is at least a wrong
*number*, whereas a wrong column mapping is a **silently plausible** one.

**No real HDFC, ICICI or SBI export exists in this environment to check a profile against.** A
shipped `hdfc` profile would therefore be a layout recalled from memory. If its column order is
wrong by one, the parser reads the *running balance* column as the amount: every figure is a
real number off a real statement, nothing raises, the balance walk (B-4) still passes because
the balance column also walks — and the operator reconciles against amounts that were never
transactions. That is `HANDOFF-FEATURE-LOCK.md` §1.2 happening a third time.

So:

| | Decision |
|---|---|
| **The mapping is an INPUT to the import**, supplied per file: which column is the value date, the amount, the direction, and so on. | It is data the operator confirms, never a constant the build asserts. |
| **A mapping may be PROPOSED from the file's own header row** — matching header text against a list of known synonyms ("Value Date", "Txn Date", "Withdrawal Amt.", "Deposit Amt."). | A proposal derived **from the file in front of us** is evidence. A profile recalled from memory is not. The proposal is shown and must be confirmed. |
| **An unresolvable header is REFUSED, naming the columns it could not place.** | Never a positional guess. "Column 4 is probably the amount" is exactly the silent mis-parse above. |
| **A confirmed mapping is STORED ON THE IMPORT BATCH**, verbatim. | So a statement can be re-read months later and the question "how was this file interpreted?" has an answer. It is also what a per-bank profile table would be built from later — **out of real files an operator confirmed**, which is the only honest way to acquire one. |

**This is not a worse product than per-bank profiles; it is the same product with its evidence
visible.** Tally itself asks the user to map columns on first import of a new bank's format.

### 8.3 The slice split — 2a ships the import, 2b ships the matching

§1 of `HANDOFF-FEATURE-LOCK.md` requires all seven layers per pass and says a track that cannot
reach layer 7 was scoped too big: **split by user-visible slice, never by architectural layer.**
The seam that satisfies that is the one between *what the bank says* and *what the books say*:

| Slice | The user-visible sentence | Contents |
|---|---|---|
| **2a** | *"Import my bank's CSV and show me what the bank says."* | The parser, the batch and line tables, the ledger's account number and IFSC, the import route, the Alt+O key, the lines on screen. Every refusal in §5 that concerns the FILE — the balance walk, the duplicate import, the empty file, the wrong ledger. |
| **2b** | *"Tick it off against my books."* | The match table, the tier engine, `Alt+S` / `F8` / `Alt+F8` / `F9`, and the resolved match writing a `bank_reconciliation` row (§2.1). **§0's rule — an ambiguous match is never auto-cleared — is 2b's, and B-1 belongs there.** |

**2a is a complete, useful thing on its own**: `RESEARCH.md:873` names *bank-only unreconciled*
as a real category, and 2a is what makes it visible. It is also honest about what it is not —
the four matching keys keep answering "not built", with their notes updated from "T4 slice 2" to
"slice 2b", because a key that says the wrong thing about *why* is worse than one that says
nothing.

### 8.4 The migration block, re-allocated

§2.2 allocated 0062–0064 as `tables / match+ACL / ledger columns`. The 2a–2b split moves the ACL
forward, because 2a needs one and the match table is 2b's:

| # | Contents | Slice |
|---|---|---|
| **0062** | `bank_statement_import` (batch, incl. the stored mapping — §8.2) + `bank_statement_line` | 2a |
| **0063** | ACL for the `bank-statement` resource | 2a |
| **0064** | `ledger.bank_account_number`, `ledger.bank_ifsc` — columns **and** data (I-11) | 2a |
| **a new number at the end** | `bank_statement_match` | 2b |

**2b's table takes a fresh number rather than renumbering**, following the precedent T6 set when
it came back for `0067`: `PLAN-TALLY-PARITY.md` §2.5's rule is *renumber unstarted blocks, never
renumber a migration that exists*, and once 0062–0064 are applied the block is consumed.

## 9. SLICE 2b AS BUILT — 2026-08-28, migration 0088

Written when the track shipped. §§0–8 are the design as it stood BEFORE the build; this section
records what was actually made, and **the three places it departs from the sections above**, so a
reader is never left comparing a plan against a tree and guessing which is current.

### 9.1 The map

| File | What it is |
|---|---|
| `backend/migrations/0088_bank_statement_match.sql` | The `bank_statement_match` table. **No ACL rules** — see §9.3 |
| `backend/internal/bankstatement/match.go` | The tier engine. **PURE**: no clock, no database, no writes |
| `backend/internal/bankstatement/matchdata.go` | The reads that feed it, and the two writes (`SetAside`, `UndoSetAside`) |
| `backend/internal/banking/banking.go` | `SetBankDateFromStatementLine` — the resolve, on the EXISTING clearance write |
| `backend/internal/server/handler_bank_match.go` | Two routes; the third is the bank-reconciliation PUT |
| `frontend/src/screens/BankMatchPanel.tsx` | The Alt+S panel |
| `frontend/scripts/drive-t4-slice2b-matching.mjs` | The browser drive, **35/35** |

### 9.2 ⛔ DEPARTURE 1 — THE TABLE STORES RESOLUTIONS, NOT CANDIDATES

§2.2 and 0062's header both call `bank_statement_match` *"the candidate links"*. **It holds no
candidates.** Proposals are DERIVED on every read by `ProposeMatches`, exactly as every report in
this product is derived.

The reason is the one that governs the rest of the codebase: **a stored candidate goes stale
silently.** A voucher altered, deleted, cleared by the manual loop, or claimed by another line
between the proposal and the acceptance leaves the operator choosing from a list that no longer
describes the books — while still looking authoritative. In its worst form it offers a match to a
voucher that has already been matched.

So the table records only what a PERSON decided: `matched` (carrying the clearance it produced) or
`set-aside` (carrying none). ⛔ **`set-aside` is not in §1.2's field list and is new here.** It is
the operator recording that a bank row corresponds to nothing in the books — `RESEARCH.md:873`'s
*bank-only unreconciled* made assertable. It writes no clearance and moves no balance, and 0088's
`clearance_matches_status` CHECK is what makes that structural rather than a promise.

### 9.3 ⛔ DEPARTURE 2 — ACCEPTING A MATCH IS THE EXISTING BANK-DATE ROUTE

§8.3 lists *"the resolved match writing a `bank_reconciliation` row"* as 2b's work, and §2.1 says
why the clearance must land in 0045's table. Both are honoured — but **not by a route of its own.**
`PUT .../bank-reconciliation/{ledgerID}/{voucherID}/{legNo}` gained an optional
`statement_line_id`, and there is no other way to resolve a match.

⭐ **THIS IS THE SLICE'S LOAD-BEARING DECISION.** Matching must not become a SECOND WAY TO ACQUIRE
THE AUTHORITY TO ASSERT A CLEARANCE: a level refused `alter bank-reconciliation` but granted the
statement would otherwise reconcile by matching what it may not reconcile by ticking, and the rule
list would say the opposite of what the product did. Three further properties come free, and every
one of them would have had to be re-implemented (and could have been forgotten) on a new route:

* **The back-dated cutoff.** `peekBankDate` reads `bank_date` out of THAT body. A resolve carrying
  only a line id would have left the PDP with no date and `cutoffDeny` would have skipped —
  byte for byte the defect `auth_middleware.go`'s bank-reconciliation comment records shipping once.
* **The WebAuthn step-up**, which gates on `Action.IsWrite()`.
* **Invariant B-2**, which stops being a convention: there is nowhere else to write.

⛔ **AND THE REQUEST STILL CARRIES THE DATE, though the server could read it off the line.** The
operator is asserting a date and an assertion they never see is not one they made — and the cutoff
needs it on the wire. A date that disagrees with the line's own value date is **refused naming
both**, never resolved in either direction: preferring one silently would write a clearance the
operator never asserted, preferring the other would file a bank row under a date that is not on the
statement it came from, and neither is visible afterwards because every figure would be real.

**The consequence for the ACL is that slice 2b writes no rules at all**, and the seeder census
still reads 16 PERFORMs. 0088's footer states that as a decision, because "no ACL migration" is
exactly the shape of an oversight.

### 9.4 ⛔ DEPARTURE 3 — NOTHING AUTO-CLEARS, NOT EVEN AN UNAMBIGUOUS EXACT MATCH

§0 forbids auto-clearing an **ambiguous** match, which leaves room for an unambiguous one to tick
itself. It does not. `ProposeMatches` returns `AcceptableInBulk`, and the operator presses one key
to assert all of those at once — the typing is saved, the judgement is not.

The reason is the handoff's own design rule for this slice: a fuzzy match that ticks itself is a
reconciliation that attests to nothing, and it fails in the direction that **foots perfectly**.

⭐ **AND B-1 IS TWO-SIDED, WHICH §4 DOES NOT SAY.** §4's B-1 is *one line, two candidate legs*. The
mirror — **one leg, two candidate lines** — is the one that is easy to miss, because each line has
exactly one candidate and looks perfectly unambiguous on its own: two ₹25,000 bank rows against a
single ₹25,000 payment. Without `markContestedLegs` both would be offered for bulk acceptance, the
first would be asserted, and the second would collide with 0088's one-match-per-clearance index
**after** a wrong clearance had been written. `TestTheMirrorAmbiguityIsCaughtTooWhenTwoBankRowsWantOneVoucher`
pins it, and the pure B-1 test deliberately does not catch it.

### 9.5 THE INVARIANTS, AND WHERE EACH IS PINNED

| # | Where |
|---|---|
| B-1 | `match_test.go` (both directions) + `TestAnAmbiguousLineWritesZeroClearances` — the DB arm counts `bank_reconciliation` rows in SQL rather than trusting the response |
| B-2 | `TestAcceptingAMatchWritesOneClearanceAndMovesTheStatement` — the BRS's un-cleared net moves by exactly the matched amount |
| B-7 | `TestAPaisaOutIsNotAMatchAtAnyTier`; the drive's ODD AMOUNT row is the live control |
| B-8 | `TestUnTickingAMatchedLegRemovesTheMatchToo` — **structural**, via 0088's `ON DELETE CASCADE`, so there is no second write path to drift |
| B-9 | `TestAVoucherAlteredAfterMatchingIsStillReportedBroken` — the matcher gained no exemption because it stores no clearance of its own |
| B-3, B-4, B-5, B-6, B-10, B-11, B-12 | slice 2a's, unchanged |

### 9.6 ⚠️ WHAT IS NOT BUILT, AND THE SCREEN SAYS EACH OF IT

1. **Partial matching is PAIRS ONLY.** Subset-sum over a year's statement does not terminate
   usefully, so the search is bounded and the bound is **disclosed** — "none found by this search",
   never silence that reads as "none exists".
2. **No voucher is created from a bank row** (§7 item 3), still deliberately out of scope: it is a
   write to the books from an outside file and needs its own memo.
3. **No printed layout** for the matching screen.
4. **F5's amount-view toggle stays refused** — §6 says it is a display toggle and not matching, and
   flipping it would also re-open the hazard that the global F5 opens a Payment voucher.
5. **The date window is a SEARCH WIDTH, not a statutory rule** — stated on screen, adjustable, and
   safe to choose precisely because widening it cannot clear anything.

   ⛔ **CORRECTED 2026-08-28, HOURS AFTER THE TRACK SHIPPED: THE DEFAULT WAS 7 AND 7 WAS INVENTED.**
   The constant's own comment said *"no authority anywhere says how late a cheque may clear"* and
   then chose a number regardless, justifying it as *"a cheque written on a Friday and cleared after
   a weekend and a bank holiday"*. That is reasoning from nothing dressed as reasoning from
   something, and it survived review precisely because the sentence admitting the invention sat
   directly above it — the admission read as diligence.

   ⭐ **The default is now 30, which is TALLY'S OWN DOCUMENTED DEFAULT** for this exact parameter:
   TallyPrime's auto bank reconciliation exposes it as **"Find Potential Matches within"**, states
   *"the default number of days set is 30 days"*, and treats 0 as "the dates must be identical"
   (`help.tallysolutions.com/auto-bank-reconciliation/`, read 2026-08-28).

   ⚠️ **THAT IS A PARITY CITATION AND NOT A STATUTORY ONE.** A vendor help page is good evidence
   about what Tally does — which is what this product is measured against — and is worth nothing as
   evidence about what a law requires. `docs/statutory/README.md`'s rule ("the notified form, the
   gazette text… **Not** blog articles, and not summaries") governs rates and is untouched by this.
   The two kinds of source must never be filed in the same drawer.
