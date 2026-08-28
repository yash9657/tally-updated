# HANDOFF — T11 BUDGETS, BUILT FROM A CLEAN CONTEXT

**Written:** 2026-08-28, at `7e84627` · **Branch:** `session/t32-and-close-7-3`
**Read time: 12 minutes.** ⭐ **Every number and path below was MEASURED and the command is beside it.**

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** It is self-contained: the feature,
the house patterns to copy, the exact migration numbers, the traps, and the gates. **You should not
need to read another handoff to build T11.** (`HANDOFF-D103-AND-THE-SESSION-LEDGER.md` is the
previous session's record if you want context; `HANDOFF-PARITY-AUDIT.md` §7.1 is the defect register.)

⛔ **T11 IS UNBLOCKED. Nothing waits on a human, no credentials, no statutory documents, no
decisions outstanding.** That is rare on this board and is why it is next.

---

## 0. WHAT THIS PRODUCT IS, IN FIVE LINES

An AI-native replacement for **Tally**, India's dominant SMB accounting package. Go backend
(`cored`, port 8087) + React/TypeScript keyboard-first frontend (vite, port 5173) + Postgres (5433).
**Every voucher is immutable and hash-chained**; reports are DERIVED, never stored. The product is
multi-tenant with Postgres **RLS scoped by a company GUC**, and a Tally-style **Disallow/Allow ACL**
decides who may see what. `frontend/src/screens/FeaturesScreen.tsx` is the product's own
truth-telling surface — **54 rows, each stating honestly what is and is not built.**

---

## 1. ⭐ WHAT T11 IS, AND WHY IT IS CHEAP

The FeaturesScreen row (`FeaturesScreen.tsx:146-149`) is the specification and it argues its own case:

> **"Budgets and budget-vs-actual"** — `state: "off"`
> *"Not built. A budget is a comparison dimension rather than a posting, so it changes no voucher and
> no balance — which is exactly why it cannot be faked by keying journals: there is no second column
> for any report to compare an actual against."*

And the plan row (`PLAN-TALLY-PARITY.md:475`):

> **T11 · Budgets** — *"A budget is a comparison dimension, not a posting. Budget-vs-actual on groups,
> ledgers and (after T2) cost centres."* — **T2 has shipped, so cost centres are in scope.**

⭐⭐ **THAT FIRST SENTENCE IS ALSO THE ESTIMATE, AND IT IS THE MOST IMPORTANT THING IN THIS FILE.**
A budget **posts nothing**. It therefore inherits **none** of the machinery that makes every other
feature here expensive:

| Machinery every posting feature must satisfy | Does T11 touch it? |
|---|---|
| The immutable voucher path + audit hash chain | ❌ **No** |
| Double-entry validation trigger | ❌ **No** |
| The back-dated cutoff (which took SEVEN doors to close) | ❌ **No** |
| GST derivation / e-invoice / e-way bill | ❌ **No** |
| Inventory lot queues and valuation replay | ❌ **No** |
| RLS tenant scoping | ✅ **Yes** — like every table |
| The ACL (see §4 — **the one real trap**) | ✅ **Yes, and it is mandatory** |

**T11 is a master plus a report.** Nothing else.

⚠️ **THE RESEARCH IS THIN AND YOU SHOULD KNOW THAT UP FRONT.** `RESEARCH.md` mentions Tally budgets
only in passing — as an importable object type (`RESEARCH.md:1921`) and in the object model
(`RESEARCH.md:957`). **There is no section on Tally's budget semantics.**

```bash
grep -n -i "budget" RESEARCH.md    # -> 3 hits, none of them a feature description
```

⛔ **SO DO NOT CITE `RESEARCH.md` FOR BUDGET BEHAVIOUR — IT DOES NOT SUPPORT IT.** Tally's own
budget feature has three shapes worth knowing (verify against TallyHelp before claiming parity):
budgets on **Groups**, on **Ledgers**, and on **Cost Centres**; each set either **"on Closing
Balance"** or **"on Nett Transactions"**; and periods that may be **yearly or monthly**. ⚠️ **If you
implement only one of Closing Balance / Nett Transactions, SAY SO ON THE ROW.** Those two answer
different questions and a budget report silently using the wrong one is worse than an absent feature.

---

## 2. ⛔⛔ THE MIGRATION NUMBERS ARE ALREADY RESERVED. TAKE THEM. DO NOT DISCOVER THEM.

**`0085` = the tables. `0086` = the ACL.** Reserved 2026-08-27 in `PLAN-TALLY-PARITY.md` §2.5.

```bash
ls backend/migrations/*.sql | tail -1     # -> 0080_multi_currency.sql
```

⚠️ **0081–0084 ARE A DELIBERATE GAP — DO NOT USE THEM.** The T8 multi-currency lane is in flight and
working forward from 0080. **Leaving gaps rather than renumbering is §2.5's own rule.**

### 2.1 ⛔⛔ WHY A LOW NUMBER WOULD BE A SECURITY BUG, NOT A STYLE POINT

`migrations.Apply` runs files in **`sort.Strings` order**, and `seed_company_auth_defaults` is a
plpgsql function that can only be extended by **RESTATING ITS WHOLE BODY** — so **only the
highest-numbered restatement survives**. An ACL written at a low number is silently overwritten on
every fresh database and **the rule simply does not exist**.

⛔ **THIS IS NOT HYPOTHETICAL. IT SHIPPED.** `0063_bank_statement_acl.sql` was authored correctly,
derived correctly, and numbered **five lower** than `0068`. On every fresh deployment 0068's older
body won, `seed_data_entry_bank_statement_rules` vanished, and **every new company handed its Data
Entry level the imported bank statement.** Migration `0071` exists to repair it.

⭐ **THERE IS A STATIC GUARD AND IT WILL CATCH YOU** — `backend/migrations/seeder_census_test.go`.
It reads the FILES, needs no Postgres, and exists because a runtime test **cannot** see this: on any
incrementally-migrated dev machine the apply order is different and the bug is invisible.
⛔ **If it fails, do not route around it.**

### 2.2 DERIVE THE RESTATEMENT FROM THE HIGHEST-NUMBERED ONE — RE-MEASURE, DO NOT INHERIT

```bash
for f in backend/migrations/*.sql; do \
  c=$(awk '/CREATE OR REPLACE FUNCTION seed_company_auth_defaults/,/^\$\$ LANGUAGE plpgsql/' "$f" | grep -c PERFORM); \
  [ "$c" -gt 0 ] && echo "$(basename $f) -> $c"; done | tail -3
# -> 0072_cost_report_acl.sql -> 12 · 0073_order_book_acl.sql -> 13 · 0076_forecast_report_acl.sql -> 14
```

⛔ **DERIVE FROM `0076` (14 PERFORMs). Your 0086 body will have 15.**
⚠️ **COUNT THE FUNCTION BODY, NOT THE FILE.** `grep -c PERFORM` on the whole file gives a different
number (migrations also call the seeder for existing companies *outside* the body) and is a false
alarm generator on exactly this check.

⭐ **`backend/migrations/0072_cost_report_acl.sql` IS YOUR TEMPLATE.** Read it end to end before
writing 0086 — its header already explains every trap above, and its diff against 0071 is
*comment-stripped exactly one line*. Copy that discipline: derive verbatim, add one PERFORM.

---

## 3. ⭐ THE FILE SET TO COPY: T2 COST CENTRES

Cost centres are the closest analogue in the tree — **also a comparison dimension, also a master plus
a report, also carrying a mandatory ACL migration.** Read its shape before designing yours:

```bash
git show --stat cfcd20b | tail -18      # the T2 backend commit
```

| T2 file | Your T11 equivalent |
|---|---|
| `backend/migrations/0040_*.sql` | **`0085_budgets.sql`** — `budget` + `budget_line` |
| `backend/migrations/0072_cost_report_acl.sql` | **`0086_budget_report_acl.sql`** |
| `backend/internal/masters/cost_centre.go` | `backend/internal/masters/budget.go` |
| `backend/internal/reporting/cost_summary.go` | `backend/internal/reporting/budget_variance.go` |
| `backend/internal/server/handler_cost_centre.go` | `handler_budget.go` |
| `backend/internal/server/handler_cost_summary.go` | `handler_budget_variance.go` |
| `backend/internal/authz/vocabulary.go` | **add the report name — see §4** |
| `frontend/src/app/defaultScreens.tsx:300-302` | register the screens |
| `frontend/src/keystroke/types.ts:498` (`REPORT_NAMES`) | add the report so **Alt+G Go To** can reach it |

⚠️ **T2 is 8,261 lines across 17 files. T11 SHOULD BE MUCH SMALLER** — cost centres carry a
posting-side allocation engine with a deferred constraint trigger, and **a budget allocates nothing**.
Copy the *shape*, not the *scale*.

---

## 4. ⛔⛔ THE ONE REAL TRAP: A NEW `report:NAME` IS **GRANTED** BY DEFAULT

**If you add a budget report and write no ACL rule, every seeded Data Entry clerk can read the
company's margins the day you ship it, and nothing will tell you.**

Verified at `backend/migrations/0011_auth_acl.sql:249`:

```sql
VALUES (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'display', 'report');
```

That is a **BARE resource** — and `internal/authz` matches it against **any** `report:NAME` at
**specificity 1**. ⛔ **So the GRANT is the default and the REFUSAL is the thing that must be
written.** This is registered defect **D95**, found exactly this way.

⭐ **CONTRAST — and this contrast is the whole design rule:** `tds-challan` and `tds-profile` are
**bare tokens**, and the Data Entry level's `default_access = 'none'` denies them **with zero rules
written**. **The shape of the name decides the default.** `report:` is the dangerous shape.

### 4.1 What 0086 must do

1. Add `budget-variance` (and any sibling report name) to **`authz.KnownReportNames`** —
   `backend/internal/authz/vocabulary.go:517`. ⛔ A name in that list whose route does not resolve is
   a *build failure* by design (`handler_tds_register.go:9`), so add the route in the same change.
2. Write `disallow display` **AND** `disallow print` for that report, for Data Entry, at specificity
   2 — restating `seed_company_auth_defaults` derived from `0076`.
   ⛔ **BOTH FORMS.** *"A report that cannot be displayed but CAN be printed is a leak with a paper
   trail"* — 0072's own words.
3. ⚠️ **The MASTER takes no rules.** A `budget` master token is bare, so `default_access = 'none'`
   denies it already — the same authority T6 shipped `tds-challan` under. **Do not add rules for it;
   adding them is how a reader later concludes bare tokens need rules.**

### 4.2 ⚠️ THE POLICY QUESTION, AND THE ANSWER TO PROCEED ON

*Should a data-entry clerk see a budget-variance report?* This is on the owner's ask list and is
**unanswered**. ⛔ **PROCEED ON `0072`'s PRECEDENT — DENY IT** — that migration made exactly this call
for cost reports, deliberately. **State the assumption in the migration body** so it is one rule to
reverse if the owner disagrees. **The failure directions are asymmetric:** over-denying is a support
ticket; under-denying is a clerk reading the company's margins.

---

## 5. DESIGN NOTES WORTH TAKING SERIOUSLY

### 5.1 ⛔ A BUDGET IS NOT A POSTING — DO NOT GIVE IT ONE

No voucher, no `posting` row, no ledger balance effect, no hash chain. If you find yourself writing
into anything the ledger owns, **stop** — the row's own sentence is the specification, and a budget
that posts is a budget that shows up in a Trial Balance, which is wrong in a way no report can undo.

### 5.2 The two axes, and the trap in the second

**Budget-vs-actual on: groups · ledgers · cost centres.** The *actual* side already exists — that is
what the reporting package computes today. Your work is the *budget* column and the variance.

⚠️ **THE COMPARISON MUST STATE ITS BASIS.** Tally's *Closing Balance* vs *Nett Transactions*
distinction is not cosmetic: a budget of ₹10,00,000 against a **closing balance** and against
**nett transactions for the period** are different numbers, and the same variance figure means
different things. ⛔ **Whichever you build, name it on the report.**

### 5.3 ⭐ WHERE THE HOUSE STYLE WILL JUDGE YOU: THE VARIANCE MUST NOT LIE BY ROUNDING OR BY SILENCE

- **A budget with no actual is not zero variance** — it is *unspent*, which is a different fact.
- **An actual with no budget is not 100% over** — it is *unbudgeted*, and it is the row a finance
  person most wants to see.
- ⛔ Do not divide by a zero budget to produce a percentage. Say *"no budget set"*.

This repository's register is full of defects that were exactly this: a figure that looked complete
and was not. See D100 and D105 for the pattern.

### 5.4 Money type and scale

All money in this codebase is `NUMERIC(19,4)` server-side and fixed-4 strings on the wire, via
`github.com/shopspring/decimal`. ⛔ **Never use a float anywhere.** Copy the column definitions from
`0040` and the wire shapes from `handler_cost_summary.go`.

---

## 6. ⛔ RUN FIRST — TRAPS THAT HAVE EACH COST REAL TIME

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
git log --oneline -3 && git status --porcelain
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # NEWER than the last backend commit?
PGCONNECT_TIMEOUT=5 pg_isready -h localhost -p 5433          # must say "accepting connections"
```

1. ⛔ **CHECK THE AGE OF `cored`.** `make run-cored` is `go run`, so **a stale process is
   indistinguishable from a fix that does not work.** Found stale five times. ⛔ A cored restart
   **invalidates statutoryd's minted token**, so restart both, **cored first**.
2. ⛔ **A TCP CONNECT IS NOT LIVENESS — FOR THE DATABASE TOO.** On 2026-08-28 postgres was wedged
   behind OrbStack's forwarder: `nc -z` succeeded while `pg_isready` said *"no response"*. Recovery:
   ```bash
   orbctl stop && orbctl start && docker start tally_pg   # ⛔ NEVER `orbctl reset` — it deletes ALL data
   ```
   ⚠️ Other projects share that VM (`supabase_db_*`, `dentabill-*`) — start **only** `tally_pg`.
3. ⛔ **VITE MUST BE ON `:5173`** — WebAuthn origin matching is exact. Start it with **`--strictPort`**
   so it fails loudly instead of silently taking 5174 (which has happened). ⚠️ It binds `::1` — use
   `http://localhost:5173/`, **not** `127.0.0.1`.
4. ⚠️ **`FeaturesScreen.tsx` IS SHARED** with the T8 and T32 lanes. You must flip the budgets row to
   `on` — **announce before editing, and keep it to that one row.** Rows are one per line so
   conflicts resolve trivially; a silent double-edit does not.
5. ⭐ **THE FIRST WRITE OF EVERY BROWSER DRIVE 403s — THAT IS THE WebAuthn STEP-UP CEREMONY, NOT A
   BUG.** The wire log reads `POST /x 403` → `webauthn/register/begin` → `finish` → `assert/*` →
   `POST /x 201`. Do not go hunting for a permission defect.
6. ⚠️ **`make verify` DOES NOT RUN `tsc`**, and `tsc` does not run what `make verify` runs — each has
   caught a real error the other missed. ⛔ `make verify` invokes `make migrate`, and **the T8 lane
   owns the schema this week.**

---

## 7. THE GATES — ALL GREEN AT `7e84627`, SO ANY RED IS YOURS

```bash
cd backend && go build ./... && go vet ./... && go test ./... -timeout 60m   # 0 FAIL lines
cd frontend && pnpm exec tsc --noEmit                                        # clean
cd frontend && pnpm exec vitest run                                          # 1787 passed / 109 files
cd frontend && pnpm verify:demo "$(cat .demo-company-id)"                    # 34/34 ⚠️ id is an ARGUMENT
python3 corpora/validate.py                                                  # OK
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md                # 105 · 99 · 2 · 0 (+4 unreadable, all closed)
```

⛔ **AND THE ONE THIS REPOSITORY CARES ABOUT MOST — MUTATION-PROVE YOUR TESTS.**
The house standard is: **break the thing you fixed, watch the test fail, then restore.** Predict the
failure *before* you observe it. Two things this session found make the case:

- A security test kept **passing while testing nothing** — it matched `Contains(err, "JWKS")`, and a
  newly added error message happened to contain that substring.
- A conservation fix's rounding line had **no test at all**; deleting it left the whole suite green
  while leaking value onto the Balance Sheet.

**For T11 the minimum mutations are:** delete the 0086 ACL rule → the ACL test must fail; break the
variance arithmetic → the report test must fail with a message naming the *consequence*, not
`want X got Y`.

---

## 8. DEFINITION OF DONE

- [ ] `0085_budgets.sql` — tables, RLS-scoped like every other tenant table, `NUMERIC(19,4)` money.
- [ ] `0086_budget_report_acl.sql` — derived from **0076** (14 → 15 PERFORMs), `disallow display`
      **and** `disallow print`, with the assumption in §4.2 stated in the body.
- [ ] `seeder_census_test.go` passes. ⛔ **Do not route around it.**
- [ ] Report name in `authz.KnownReportNames` **and** a route that resolves.
- [ ] Budget master: create / alter / list. **No posting path.**
- [ ] Budget-vs-actual on **groups, ledgers and cost centres**, stating its basis (§5.2).
- [ ] Unbudgeted actuals and unspent budgets each shown as **what they are**, not as zero or ∞ (§5.3).
- [ ] Reachable **from the keyboard**: `REPORT_NAMES` in `frontend/src/keystroke/types.ts:498` and the
      screen registered in `frontend/src/app/defaultScreens.tsx`.
- [ ] A **browser drive** under `frontend/scripts/`, in the style of the existing ones: its own
      throwaway company, torn down in a `try/finally`, **assertions phrased so the CORRECT behaviour
      is the PASS**, and **control probes** that prove the oracle is not vacuous. Read
      `drive-l28-stock-restale.mjs` as the current best example.
- [ ] ⛔ **A Data Entry level is REFUSED the report** — driven, not assumed.
- [ ] `FeaturesScreen.tsx` budgets row flipped to `on` with a note that is **true**, including
      anything you did not build (§1). ⚠️ Announce first — shared file.
- [ ] `PLAN-TALLY-PARITY.md` §2.5 updated in place to mark 0085/0086 **consumed**.
- [ ] All gates in §7 green, plus the mutation proofs.

---

## 9. ⚠️ WHAT IS *NOT* T11 — DO NOT LET IT GROW

- **Cash-flow projection / forecasting** already exists (insightd) and is a different feature.
- **Split company data** is a deliberate **non-goal** — never build it.
- **Interest on overdue bills (T13)** is blocked on statutory documents; the design note is at
  `docs/design/t13-interest-model.md`.
- **GSTR-9 (T12)** is the next feature after this one and has **`0087`** reserved. Independent of T11.

⭐ **When you finish, the honest next step is T12 GSTR-9** — also unblocked, also nothing waiting on
a human.
