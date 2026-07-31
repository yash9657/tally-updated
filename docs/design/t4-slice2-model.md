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
