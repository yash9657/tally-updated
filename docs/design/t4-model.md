# T4 — BANK RECONCILIATION: THE DATA MODEL

> **This memo is the §3.2 gate on T4.** Everything below was verified against the live
> database with `\d` and against the code on 2026-07-30. Migration numbers reserved:
> **0045 (schema), 0046 (ACL)** — `PLAN-TALLY-PARITY.md` §2.5.
>
> **Why this track:** §6.2 of the handoff calls bank reconciliation the gap that blocks
> "every business, every month" and says it **alone means nobody can switch off Tally
> today**. There is zero code for it.
>
> **The headline finding is in §0.1, and it contradicts the plan.** `PLAN-TALLY-PARITY.md`
> says reconciliation "is metadata on the posting". **It cannot be.** `posting` is
> hard-immutable and its rows are re-created with fresh UUIDs on every alter.

---

## 0. WHAT EXISTS TODAY — measured

| Thing | Status, measured 2026-07-30 |
|---|---|
| Any bank/reconciliation/statement table | **NONE.** Full `information_schema` sweep. |
| `posting` columns | `id, company_id, voucher_version_id, ledger_id, dr_cr, amount, created_at` — **no date, no bank date, no state** |
| `posting` mutability | **HARD IMMUTABLE.** Trigger `posting_no_mutate` BEFORE **UPDATE OR DELETE** → `RAISE EXCEPTION 'posting is immutable; % is not permitted'` |
| Posting date | **There isn't one.** The date lives on `voucher_version.voucher_date`; every report joins to get it. |
| Current-version filter | `voucher_head.current_version_id = vv.id AND vh.is_deleted = false` — `movementSQL`, `reporting.go:121` |
| Bank groups | `Bank Accounts` (nature **ASSET**, parent Current Assets) and `Bank OD A/c` (nature **LIABILITY**, parent Loans (Liability)). Both `is_system = true`. |
| `ledger_balance` | Materialised running total, **no date dimension** — reports derive from postings instead and a gate asserts the two agree |

### 0.1 ⚠️ RECONCILIATION CANNOT BE A COLUMN ON `posting` — and this is the whole design

Two measured facts make the obvious design impossible:

1. **`posting` is hard-immutable.** Marking a posting cleared would be an `UPDATE`, which
   the trigger refuses outright. There is no "except this column" escape and there must not
   be one — the immutability is what makes the ledger trustworthy.
2. **An alter re-creates every posting row.** `ledger.go:1327-1339` inserts a **fresh
   `uuid.NewV7()` per posting per version**. `posting.voucher_version_id` is an FK to the
   version, and versions are append-only. So after an alter, the posting the operator
   reconciled **is no longer the current version's posting**, and the current version's bank
   leg is a row that has never been seen before.

A reconciliation keyed on `posting.id` would therefore go **silently stale on the first
alter**: the cleared row drops out of `movementSQL` (it is not the current version), the new
row is unreconciled, and the Bank Reconciliation Statement quietly changes without anyone
touching the bank. **That is the §3.3 voucher-lifecycle trap, and the printed Day Book
already fell into its sibling.**

**DECISION — the reconciliation key is `(company_id, voucher_id, ledger_id, leg_no)`,
never `posting.id`.**

- `voucher_id` (not `voucher_version_id`) is the stable identity across alters — it is what
  `voucher_head` is keyed on.
- `leg_no` disambiguates a voucher with **two legs on the same bank ledger** (a bank charge
  netted against a transfer). It is the ordinal of the leg **within the current version's
  postings for that ledger**, ordered by `posting.id` (v7 UUIDs are monotonic by creation, so
  this is the insertion order of `in.Postings` and is stable within a version).
- **`leg_no` is the sharp edge.** If an alter reorders or removes legs, the ordinal can
  re-point. §4's I-4 is what makes that safe: the reconciliation stores the amount and side
  it was cleared against, and any disagreement with the current version **breaks the
  reconciliation loudly** rather than silently re-pointing.

---

## 1. THE OUTPUT INVENTORY

### 1.1 The Bank Reconciliation Statement (the artefact)

Tally's shape, and the one an accountant expects:

| Field | Meaning |
|---|---|
| Balance as per **company books** as at the date | the ledger's closing balance from `movementSQL` |
| **Less/Add:** amounts not reflected in the bank | each un-cleared item: voucher date, voucher no, particulars, amount, Dr/Cr |
| **Less/Add:** amounts not reflected in the books | statement lines with no matching voucher (slice 2) |
| Balance as per **bank** as at the date | derived: books balance ± un-cleared |
| **Difference** | **must be 0.0000 — to the paisa** |

Every un-cleared line must carry enough to *find* the voucher: date, number, party/narration,
amount, side, and its age in days (an item un-cleared for 90+ days is a stale cheque and is
the single most useful thing this report surfaces).

### 1.2 The bank-date entry surface
A screen listing a bank ledger's postings for a period with an editable **Bank Date**
column — exactly Tally's `F5` reconciliation view. Setting a bank date **is** clearing the
item; clearing it un-reconciles.

### 1.3 Printed BRS
Renders through T1's generic `report.go`. **Needs an explicit `disallow display report:` and
`disallow print report:` at specificity 2** — a bank statement is sensitive and the seeded
Data Entry level's coarse `allow display report` matches every `report:NAME` at specificity 1.

### 1.4 Statement import + matching — **SLICE 2, explicitly deferred**
The plan already scopes CSV import as the second slice. Slice 1 delivers the manual bank-date
loop, which is what makes the feature usable at all. **Deferred with a stated reason, not
silently absent.**

---

## 2. THE PROVENANCE TABLE

| Field | Required by | Stored where | Verdict |
|---|---|---|---|
| Which ledger is a bank account | BRS scope | `account_group` `Bank Accounts` / `Bank OD A/c`, both `is_system` — reachable via `ledger.group_id` | **OK — derive, do not add a flag** |
| Voucher date | BRS line | `voucher_version.voucher_date` ✓ | **OK** |
| Voucher number | BRS line | `voucher_version.voucher_no` ✓ | **OK** |
| Amount and side of the bank leg | BRS line | `posting.amount` / `posting.dr_cr` ✓ | **OK** |
| Books balance as at a date | BRS header | derivable via `movementSQL` ✓ | **OK** |
| **Bank date (date the bank cleared it)** | the entire feature | ***nowhere*** | **MIGRATION** |
| **Reconciliation state** | the entire feature | ***nowhere*** | **MIGRATION** |
| **Amount cleared against** | I-4 (alter safety) | ***nowhere*** | **MIGRATION — and it is not redundant, see §4** |
| **Side cleared against** | I-4 | ***nowhere*** | **MIGRATION** |
| Who reconciled it, and when | audit | ***nowhere*** | **MIGRATION** |
| Bank statement lines | slice 2 | ***nowhere*** | **MIGRATION (slice 2)** |
| Bank account number / IFSC | statement matching, BRS header | ***nowhere*** on `ledger` | **DEFERRED — slice 2.** Not needed to reconcile manually. Stated so it is a decision, not an oversight. |

**Ten fields, seven of them MIGRATION.** Like T6, T4 is mostly a schema track.

---

## 3. UNITS AND SCALE

| Value | Type / scale | Naming |
|---|---|---|
| Every money value | `NUMERIC(19,4)`, `decimal.Decimal` | never `float64`, never `ParseFloat` |
| Bank date | `DATE`, **no time zone** | `BankDate` — it is a calendar date on a statement, not an instant |
| Reconciled-at | `TIMESTAMPTZ` | `ReconciledAt` — this one *is* an instant (audit) |
| Difference | `NUMERIC(19,4)` | **must compare `.Equal(decimal.Zero)`**, never `< 0.005` |
| Age of an un-cleared item | whole days | `AgeDays` |

**The difference is an exact equality, not a tolerance.** `migration.ReconcileFull` sets the
precedent — it reconciles to the paisa. A BRS that "balances within a rupee" is a BRS that
does not balance, and a tolerance is how a real error hides for a year.

### 3.1 THE SIGN TRAP — Bank OD is a LIABILITY

`Bank Accounts` is ASSET nature; `Bank OD A/c` is LIABILITY nature. An overdraft's book
balance is a **credit**. The BRS must present "balance as per books" with the sign
convention the *account's nature* implies, and the un-cleared adjustments flip with it.

**Do not write `if debit > credit`.** Use the existing `ledgerBalance.signed()`
(`reporting.go:86`) which already encodes the nature convention for every other report, so
the BRS agrees with the Trial Balance by construction rather than by coincidence.
*Test: `TestAnOverdraftReconcilesWithTheSameArithmeticAsACurrentAccount`.*

---

## 4. INVARIANTS, EACH WITH THE TEST THAT PINS IT

| # | Invariant | Enforcement |
|---|---|---|
| **I-1** | **books balance ± un-cleared items = bank balance, EXACTLY (0.0000)** | The report computes bank balance *by* that arithmetic, so the real test asserts it against an **independently summed** figure — sum the cleared postings directly and compare. A report that derives X and then asserts X proves nothing (§3.4). |
| **I-2** | Reconciliation **never mutates a voucher or a posting** | The `posting_no_mutate` trigger already guarantees it; the test asserts the voucher's `hash` and `current_version_id` are **byte-identical** before and after reconciling. |
| **I-3** | Only a posting on a **bank-group** ledger may be reconciled | FK + `CHECK` via the group nature; a test tries to reconcile a Sales posting and asserts refusal |
| **I-4** | **A reconciled item whose current version disagrees with what was cleared is BROKEN, not silently valid** | The row stores `cleared_amount` and `cleared_dr_cr`. If the current version's leg differs, the BRS lists it as **BROKEN BY ALTER** and the difference does **not** silently absorb it. *Test: reconcile, alter the amount, assert the item is flagged and the BRS refuses to report a clean zero.* **This is the invariant that makes §0.1's `leg_no` key safe.** |
| **I-5** | A **deleted** voucher's reconciliation contributes nothing | `vh.is_deleted` guard, same as `movementSQL`. *Test named for the breach.* |
| **I-6** | The BRS reads **current versions only** — an altered voucher appears **once** | Join `voucher_head.current_version_id`, exactly as `movementSQL` does. The printed Day Book got this wrong; do not repeat it. |
| **I-7** | Bank date **may not precede** the voucher date | `CHECK`. A cheque cannot clear before it is written. *Mutation: remove the CHECK, assert the test bites.* |
| **I-8** | A bank date in a **filed/frozen** period follows the existing back-dated-cutoff rules | Reconciling is a **write** — see §5 Authorization |
| **I-9** | RLS `ENABLE` **and** `FORCE`; policy with `USING` **and** `WITH CHECK`; composite `(company_id, id)` FKs; cross-tenant refusal proven **by the FK** | Plus a row in `buildFixture` **and** adding the new table to `internal/security/isolation_test.go`'s `tenantTables` — measured at **7 of 41** company-scoped tables today |

---

## 5. THE EDGE-CASE LEDGER

**Money**
- **Zero-amount posting**: legal (a contra with no cash effect); it may be reconciled and
  contributes 0 to the difference.
- **Negative**: impossible — `posting_amount_check` forbids it. Direction is `dr_cr`.
- **Partial clearing is NOT modelled**: a bank clears a cheque in full or not at all. A
  part-cleared item is a *different voucher*, not a fraction of this one. **Stated once here
  so it is a decision, not a discovery.**
- **Same amount, same day, two vouchers** — the classic ambiguity. Manual clearing keys on
  the voucher, so it is unambiguous by construction. Slice 2's matcher **must never
  auto-clear it**; surface both.

**Time**
- Bank date **after** the BRS `as_on` date → the item is **un-cleared as at that date**, even
  though it is reconciled today. **The BRS is a point-in-time statement**, so it must filter
  on `bank_date <= as_on`, not on `bank_date IS NOT NULL`. *This is the single easiest bug to
  write in this whole track.* *Test: `TestAnItemClearedAfterTheAsOnDateIsUnclearedInThatStatement`.*
- Bank date before the voucher date → I-7 refuses.
- FY boundary / a cheque issued in March clearing in April → normal, and exactly what the
  un-cleared list is for.
- Books-beginning-from: a BRS dated before the books began is **empty, not an error** —
  matching how the Trial Balance and Balance Sheet already behave (`verify:demo` pins both).

**Voucher lifecycle**
- Alter → I-4 (broken, loudly). Delete → I-5. Reconciliation lives **outside** the hash
  chain: it is not a fact about the voucher, it is a fact about the **bank**, asserted later
  by a human. **It must NOT go inside the canonical payload** — unlike T6's TDS facts, which
  must. *Say this explicitly in the code comment or someone will "fix" it.*

**Tenancy** — I-9.

**Authorization**
- **Reconciling is a WRITE.** It needs `routeAction` mappings, the back-dated cutoff, and a
  decision on WebAuthn step-up. It does **not** alter the books, so step-up is arguably not
  required — **decide it once, here**: recommend **no step-up** (it changes no balance) but
  **yes to the cutoff** (it is a dated assertion about a closed period).
- Explicit `disallow` at specificity 2 for the BRS report, `display` **and** `print`.
- Data Entry: recommend **may set a bank date** (it is clerical), **may not** see the BRS
  report if the report is judged sensitive. Ratify at review.

**Absent/partial data**
- A bank ledger with **no postings** → an empty BRS with books balance = opening, difference
  0. Not an error.
- A company with **no bank ledger at all** → the report refuses naming the reason, T1's
  Rule 46 diagnostic-refusal pattern.

---

## 6. THE SHAPE THAT FOLLOWS

Migration **0045**, FORCE RLS, composite `(company_id, id)` FK:

```
bank_reconciliation
  id, company_id
  voucher_id        -- STABLE across alters (NOT voucher_version_id, NOT posting_id)
  ledger_id         -- the bank ledger
  leg_no            -- ordinal of the leg within that voucher x ledger
  bank_date         -- DATE; the date the bank cleared it
  cleared_amount    -- NUMERIC(19,4)  } what was cleared, so an alter that
  cleared_dr_cr     -- dr_cr          } disagrees is BROKEN and not silent (I-4)
  reconciled_by, reconciled_at
  UNIQUE (company_id, voucher_id, ledger_id, leg_no)
  CHECK bank_date >= (the voucher's date)          -- I-7, via trigger (cross-table)
```

Migration **0046** is the ACL. Slice 2 adds `bank_statement_line` + the matcher.

---

## 7. ACCEPTANCE CHECKLIST

- [ ] §0.1 attacked: is `(voucher_id, ledger_id, leg_no)` genuinely stable, and does I-4
      make `leg_no` safe under an alter that **reorders** legs?
- [ ] I-1's test sums cleared postings **independently** rather than re-deriving the report
- [ ] The `bank_date <= as_on` filter is present (§5 Time) — the easiest bug here
- [ ] `signed()` reused so an overdraft agrees with the Trial Balance by construction
- [ ] Reconciliation is **outside** the hash chain, and the comment says why
- [ ] New table in `tenantTables` **and** `buildFixture`
- [ ] Step-up / cutoff / Data-Entry decisions ratified
- [ ] Difference asserted with `.Equal(decimal.Zero)` — no tolerance anywhere

---

## 8. WHAT THE BUILD FOUND — slice 1, 2026-07-30

Three things the memo did not predict. Recorded here because §3.2's value is only
realised if the memo is corrected by what the build learns.

**8.1 The per-company ACL seeder is a second, separate registration — and missing it
fails SILENTLY in the worst direction.** Migration 0046 backfills every existing company
*and* must extend `seed_company_auth_defaults`, which every ACL migration since 0011
re-declares wholesale. Getting only the backfill means every company created *afterwards*
silently lacks both rules — so a clerk cannot reconcile, and the seeded Data Entry level
**can read the bank statement**, in exactly the tenants nobody tested.
`TestDataEntryMayTickOffButMayNotReadTheStatement` caught it because it creates its
company at test time and therefore exercises the seeder rather than the backfill. **Any
future ACL migration must assert both paths.**

**8.2 A new resource token has FOUR registration points, not one.** `routeAction` alone is
not enough. `authz.KnownResources` and `authz.KnownReportNames` are enforced by
`TestEveryCompanyRouteIsMappedAndInVocabulary`, and `reportName()` must know any name in
`KnownReportNames` because `routes()` mounts `/print/reports/<name>` for **every** one of
them — so a name in the vocabulary but not in `reportName()` leaves a mounted route that
default-denies for every level including an Owner. The guardrails caught all four.

**8.3 THE PRINTED BRS NEEDS A ROUTE-SHAPE DECISION, and it is a real gap.** §1.3 assumed
it would render through T1's generic `report.go` like any other report. It cannot, yet:
the print route is `/print/reports/{name}` and carries **no ledger id**, while a Bank
Reconciliation Statement is always *for one bank account*. There is no honest way to print
"the" statement for a company with three banks. It is therefore mapped and answers **501**
— the codebase's existing "authorized, and honestly refused" path — rather than guessing.
**Closing it means deciding the route shape first**: a `?ledger_id=` parameter, or one
statement per bank in a single document. That is a product call, not an implementation
detail.

### 8.4 Slice 1 status

Delivered: migration **0045** (schema, RLS, the two cross-table rules as a trigger),
migration **0046** (ACL, both halves), `internal/banking` (the write side, archtest-bounded
to `platform/db`), `reporting.BankReconciliationStatement` (the read side), the three HTTP
routes, and **13 tests** — 9 engine, 4 wire — with the as-at filter and the alter-detection
each **mutation-proven to bite** and restored byte-identically.

Verified live against the demo company: books **370,000.0000**, un-cleared **120,000.0000**
across 5 items, balance per bank **250,000.0000**, difference **0.0000**. The bank balance
was then checked against an independently known figure — the ledger's opening balance is
`250000.0000 D`, which is exactly what the bank has seen when nothing is reconciled.

**Not delivered, and stated rather than implied:** statement import + matching (§1.4, always
slice 2), the bank-date entry SCREEN (`frontend/src` has no bank-reconciliation key — the
same "pipeline with no button" gap T1 shipped, and it should not be repeated twice), and
the printed BRS (§8.3).
