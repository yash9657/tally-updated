# SESSION HANDOFF — T29 slice 2a is shipped. Start at §4.

> **This is a SESSION handoff, not a plan.** The plan of record is
> `HANDOFF-FEATURE-LOCK.md`; read **§1 (the bar)**, **§2.6 (what just shipped)** and **§5 (the
> traps)** there for what to build next and why. This file records only what happened in the
> session of **2026-08-02 → 2026-08-03**, what is trustworthy, and the things that cost time.
> Delete it once you have read it and started work.
>
> It replaces `HANDOFF-SESSION-ITEM-ZERO.md`, which was deleted per its own instruction. Its two
> durable parts were folded into the plan: the under-deduction lesson is `HANDOFF-FEATURE-LOCK.md`
> §1.2/§2 step 1, and the gate list is §5.2.

---

## 1. STATE — verified, not assumed

| | |
|---|---|
| Branch | `master`, HEAD **`b850844`**, **tree clean** |
| Migrations | **0068** is the lexical max; **0062, 0063, 0064 also applied** (0062 dry-run under `BEGIN/ROLLBACK` before first application, then all three re-run with `psql -f` against the populated DB) |
| Backend | **1977 pass / 0 SKIP / 0 FAIL** (`go test ./... -count=1 -v`) |
| Frontend | **459 pass** |
| insightd | 107 pass |
| corpora | OK (tax=14 tds=15 tcs=5 keystroke=32 isolation=13 migration=6 forecast=4 inventory=6 gstr2b=17 ims=12 payroll=19) |
| `verify:demo` | **33/33** against a live stack (cored + insightd + Postgres) |
| `make verify` | **exit 0** |

**The long run was bracketed** with a live `psql` query before and after, per §1.1. 55 public
tables both times. The numbers are real, not a skip census.

### Three commits

```
b850844  docs(plan)  T29 slice 2a shipped — §2.6, and the trap waiting for 2b
262b6d2  test(t29)   prove I-11 against the seeded demo, not just against a column
633639e  feat(t29)   bank statement import — all seven layers, no invented bank layouts
```

---

## 2. WHAT SHIPPED

`Alt+O` on the Bank Reconciliation screen imports a bank's CSV. **All seven layers of §1** —
schema, engine, write path, HTTP, screen, keystroke (`RESEARCH.md:361`, :415, :1221), and the
`FeaturesScreen` note rewritten in the same commit.

**`docs/design/t4-slice2-model.md` §8 is new** and settles the two calls §7 of that memo left
open: **CSV only**, and **no built-in per-bank column layouts**. §2.6 of the plan has the full
reasoning; the one-line version is that a column layout is the same class of fact as a tax rate,
and a wrong one is *silently plausible* rather than merely wrong.

**Slice 2b (matching) is NOT built** and says so everywhere it could be mistaken for built. See
plan §2.6 for what 2b must do and the boundary edge already waiting for it.

---

## 3. THE FIVE THINGS THAT COST TIME. Do not re-derive these.

### 3.1 ⚠️ A DEFECT I WROTE AND MUTATION DID NOT CATCH — a TEST caught it

`ProposeMapping` emitted **two contradictory messages** for one field: "Date, Posting Date could
all be it, so none was chosen" *followed by* "no column looked like the value date". Both present,
one false.

**Neither is wrong in isolation, which is why no mutation would have found it** — the bug was in
the *pair*. It surfaced only because a test asserted the CONTENT of the message rather than its
presence, and read the last matching entry. An operator reading "no column looked like the value
date" under a list of two candidates has been told the tool is broken rather than that it needs an
answer. Fixed with an `ambiguous` set; `TestTwoPlausibleHeadersForOneFieldAreBothReported` pins it.

> **The lesson: mutation testing proves a test can fail. It does not prove the test asserts the
> right thing.** Assert the sentence, not the fact that a sentence exists.

### 3.2 THE DIRECTION INVERSION IS THE WHOLE TRACK, AND IT IS COUNTER-INTUITIVE

A **DEPOSIT is `'D'`** in our books. The bank's statement calls it a *credit*, because in the
BANK's books we are a liability. Normalised once, in `bankstatement.direction()`.

**Get it backwards and slice 2b produces ZERO matches, which reads on screen as "nothing to
reconcile" rather than as a bug.** Mutation-proved, and the differential test over all three
encodings carries a **control** (money in is `D`/5000, money out is `C`/1200) — without it, a
mutation that inverted *all three together* would have passed a consistency-only assertion.

The screen labels its columns **In / Out**, not Dr / Cr, for the same reason: a `D` under "Dr"
looks wrong to an operator holding the paper statement.

### 3.3 THE RE-IMPORT PROBLEM HAS TWO GRAINS AND THE OBVIOUS ONE IS THE LESS USEFUL

A per-**file** hash catches the double-click. It does **nothing** for the real case: 1–31 January,
then 15 January–15 February. That needs a per-**transaction** hash — and then a bare unique-on-hash
would silently DROP a second genuinely identical transaction (two identical ATM withdrawals), which
is an **under-import**: nothing on screen is wrong, there is simply less of it. Hence
`occurrence`, the ordinal among identical rows within a file.

**The running balance is deliberately NOT in the hash**, so the same rows re-exported with the
balance column switched off still deduplicate.

### 3.4 A CHECK THAT DID NOT RUN LOOKS EXACTLY LIKE A CHECK THAT PASSED

A file with no running-balance column cannot be balance-walked. `BalanceWalked` is therefore
three-valued in effect (`walked` / `did not run` / `refused`), the import **says** which, and the
screen renders "did not run" in a **different weight** — mutation-proved, because "did not run"
set in the same grey as "passed" is not a warning.

### 3.5 THE GATES THAT CAUGHT ME (they work — expect them)

- **`archtest.TestInternalImportBoundaries`** — a new package must declare its `allowedDeps`
  policy or the build fails by name. Three lines of failure for one missing entry.
- **The two print censuses** — the deferred-report count (15 → 16) and
  `printDisallowedBySeededMigrations` (→ 16 rows). **Both assert counts, so update the REASONING,
  not just the number.** While there I collapsed two stacked doc comments on that slice, the first
  of which claimed THIRTEEN rows while the slice held fifteen — stale before this session.
- **`buttons.test.ts`** — a bar button must actually ACT. `Alt+O` failed until
  `importBankStatement` was added to the side-effect list, because it deliberately does not move
  the nav stack.
- **`testdb`'s teardown census** — two new tables, two `buildFixture` rows. Note both reuse the
  fixture's existing bank ledger: 0062's trigger refuses a non-bank ledger exactly as 0045's does.
- **`verify:demo`** — I added the I-11 check to it and then RAN it against a live stack. A seed
  change that is only typechecked proves nothing.

---

## 4. WHERE TO START

`HANDOFF-FEATURE-LOCK.md` §4, **order 1**, which now holds two things:

1. **T29 slice 2b — the MATCHING.** Plan §2.6 has the migration rule, the boundary edge waiting
   for it, and §0's governing rule (**never auto-clear an ambiguous match**). The import side is
   done and tested, so 2b starts from a real statement in a real table.
2. **T6's challan register** — still the piece that needs no unread document, and still the
   biggest hole in that feature: nothing tracks what has been **deposited**, and it is the deposit
   that stops interest running under s.396.

---

## 5. HOUSEKEEPING

- **`PLAN-TALLY-PARITY.md` §2.3's "never `git add -A`" was followed** — every commit staged with
  explicit paths.
- **Two files were reverted rather than committed.** `gofmt -w internal/reporting` reformatted
  `closing_stock.go` and `closing_stock_statement_test.go`, which I never edited — pre-existing
  formatting drift, unrelated to this track, so it was reverted rather than smuggled into a feature
  commit. **`gofmt -l` still flags them, plus `handler_print_bankrecon_test.go`.** Nothing gates on
  it (`make verify` is green either way); if you want them clean, do it as its own commit.
- **`internal/api/mock.ts`'s `addMoney` uses `parseFloat`** — pre-existing, on a money path, which
  §5.1 forbids. Not touched this session; the helpers I added to that file use BigInt
  ten-thousandths instead. Worth a separate fix.
- **`ci-runner` IS STILL STOPPED** (inherited from the previous session). Start it before a push
  that expects CI: `orb start ci-runner`.
- **Migration blocks:** 0062–0064 consumed and recorded in `PLAN-TALLY-PARITY.md` §2.5. Group B's
  blocks still continue from **0069**; slice 2b's `bank_statement_match` takes a new number there.
- `docs/design/t6-model.md` §9.1's "eight of eight" claim is **still known-false and still not
  fixed** — inherited, and now two sessions old.

---

## 6. ONE THING I DID NOT DO THAT YOU MIGHT EXPECT

**The mock client does not reimplement the parser, and that is deliberate.** `mock.ts` reads a
confirmed mapping and copies the direction inversion exactly, but it does **not** walk balances,
does **not** dedup by transaction, does **not** number occurrences and does **not** check the
account number. All four gaps are named in the comment on its `bankStatements` state.

**So a screen that looks right against the mock is NOT evidence that any of those refusals work.**
The refusal paths are covered by `internal/bankstatement` and by the HTTP tests in
`handler_bank_statement_test.go`, which run against a real database.
