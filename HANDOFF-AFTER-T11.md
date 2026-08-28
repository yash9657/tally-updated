# HANDOFF — T11 IS BUILT AND **COMMITTED** · ⛔ **SUPERSEDED BY `HANDOFF-AFTER-T12.md`**

> ⛔ **READ `HANDOFF-AFTER-T12.md` INSTEAD.** T12 GSTR-9 — which §4 below calls "next" — SHIPPED on
> 2026-08-28 at `92319a9`, so §4, §5 and §9 of this file are about work that is done. What is still
> worth reading here is **§2, the T11 map**, and **§2.6, T11's two open judgement calls** — the ACL
> deny remains an ASSUMPTION and the owner still has not ruled.

**Written:** 2026-08-28, working tree at `19fec0c` + T11's 39 uncommitted files · **Updated:** 2026-08-28, T11 committed at `186f89d` · **Branch:** `session/t32-and-close-7-3`
**Read time: 14 minutes.** ⭐ **Every number below was MEASURED and the command is beside it.**

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** It is self-contained: what just
shipped, what state the tree is in, the gates, the next feature, and the traps. You should not need
to read another handoff to act on it. (`HANDOFF-T11-BUDGETS.md` is the brief T11 was built from —
useful only if you want to audit T11 against its own spec. `HANDOFF-PARITY-AUDIT.md` §7.1 is the
defect register.)

---

## 0. ✅ T11 IS COMMITTED — THIS SECTION USED TO SAY THE OPPOSITE

**T11 Budgets is COMPLETE, all gates are GREEN, and it is now COMMITTED at `186f89d`.** The commit
is **39 files — 16 new, 23 modified, 10,610 insertions**, exactly the shape this section predicted
while it was still loose in the working tree.

```bash
git show --stat --oneline 186f89d | tail -1    # -> 39 files changed, 10610 insertions(+), 9 deletions(-)
git log --oneline -1 -- backend/internal/reporting/budget_variance.go   # -> 186f89d feat(T11): budgets...
```

⚠️ **THE ORIGINAL WARNING IS PRESERVED HERE BECAUSE IT WAS RIGHT AT THE TIME:** for one session T11
was 39 uncommitted files and a stray `git checkout .`, `git stash`, `git reset --hard` or branch
switch would have destroyed a full feature. It no longer can. If you are reading this and
`git status --porcelain` is large again, it is **your** work, not T11's.

⚠️ **THE OWNER HAS NOT REVIEWED T11.** Green gates are not a review, and a commit is not a review
either. §2.6 lists the two judgement calls most worth a second opinion — both still stand, and the
ACL deny in particular is an ASSUMPTION recorded in `0086`'s body, not an answered question.

---

## 1. WHAT THIS PRODUCT IS, IN FIVE LINES

An AI-native replacement for **Tally**, India's dominant SMB accounting package. Go backend
(`cored`, port 8087) + React/TypeScript keyboard-first frontend (vite, port 5173) + Postgres (5433).
**Every voucher is immutable and hash-chained**; reports are DERIVED, never stored. The product is
multi-tenant with Postgres **RLS scoped by a company GUC**, and a Tally-style **Disallow/Allow ACL**
decides who may see what. `frontend/src/screens/FeaturesScreen.tsx` is the product's own
truth-telling surface — **54 rows, each stating honestly what is and is not built.**

```bash
python3 -c "import re;s=open('frontend/src/screens/FeaturesScreen.tsx').read();\
import collections;print(collections.Counter(re.findall(r'state:\s*\"(\w+)\"',s)))"
# -> {'on': 46, 'off': 6, 'always': 1, 'server': 1}   (54 rows; T11 flipped one off->on)
```

---

## 2. WHAT T11 SHIPPED — THE MAP, SO YOU CAN AUDIT IT WITHOUT REDISCOVERING IT

**Budgets, and budget-vs-actual on groups, ledgers and cost centres.** A budget POSTS NOTHING: no
voucher, no posting, no balance, no hash chain. It is the second column a report compares an actual
against, which is why the feature cannot be faked by keying journals.

### 2.1 The sixteen new files

```bash
wc -l backend/migrations/0085_budgets.sql backend/migrations/0086_budget_report_acl.sql \
      backend/internal/masters/budget.go backend/internal/reporting/budget_variance.go \
      backend/internal/server/handler_budget.go backend/internal/server/handler_budget_variance.go \
      backend/internal/server/handler_groups_list.go \
      frontend/src/screens/masters/BudgetsScreen.tsx \
      frontend/src/screens/reports/BudgetVarianceScreen.tsx \
      frontend/scripts/drive-t11-budgets.mjs      # -> 6075 total
```

| File | What it is |
|---|---|
| `backend/migrations/0085_budgets.sql` (364) | `budget` (name + period) and `budget_line`. RLS ENABLE **and** FORCE, policy with USING **and** WITH CHECK |
| `backend/migrations/0086_budget_report_acl.sql` (353) | The ACL counterweight. **15 PERFORMs**, derived from `0076` |
| `backend/internal/masters/budget.go` (986) | Create/alter/delete plans and lines; the side derivation; the cost-centre basis refusal |
| `backend/internal/reporting/budget_variance.go` (1114) | The report. Seven statuses, per-basis subtotals, the unbudgeted sweep |
| `backend/internal/server/handler_budget.go` (458) | The eight master routes |
| `backend/internal/server/handler_budget_variance.go` (269) | The report route |
| `backend/internal/server/handler_groups_list.go` (114) | **`GET /groups`** — see §2.5, this is the one addition outside T11's brief |
| `frontend/src/screens/masters/BudgetsScreen.tsx` (817) | The master screen |
| `frontend/src/screens/reports/BudgetVarianceScreen.tsx` (846) | The report screen |
| `frontend/scripts/drive-t11-budgets.mjs` (754) | The browser drive, **36/36** |
| + 5 test files | `budget_test.go`, `budget_variance_test.go`, `budget_variance_db_test.go`, `auth_budget_report_test.go`, and two `.test.tsx` |

### 2.2 ⭐ THE MIGRATION NUMBERS, AND THE CENSUS ONE-LINER THAT LIES

`0085` and `0086` are **consumed**. `PLAN-TALLY-PARITY.md` §2.5 is updated in place to say so.

```bash
for f in backend/migrations/*.sql; do \
  c=$(awk '/^CREATE OR REPLACE FUNCTION seed_company_auth_defaults\(/,/^\$\$;$/' "$f" \
      | grep -c 'PERFORM seed_data_entry'); [ "$c" -gt 0 ] && echo "$(basename $f) -> $c"; done | tail -3
# -> 0073 -> 13 · 0076 -> 14 · 0086 -> 15
```

⛔⛔ **THE CENSUS COMMAND IN THE OLD T11 HANDOFF REPORTS `0086 -> 22`, AND IT IS WRONG.** Its awk
range is anchored on any line CONTAINING `seed_company_auth_defaults`, which matches the file's own
COMMENTS, so the range opens early and never closes. **Anchor on `^CREATE OR REPLACE FUNCTION` and
`^\$\$;$` as above.** The authoritative check is the static guard, which passes:

```bash
cd backend && go test ./migrations/     # -> ok
```

⚠️ **`0081–0084` REMAIN A DELIBERATE GAP** for the in-flight T8 multi-currency lane, which is
working forward from `0080`. Do not take them. **`0087` is reserved for T12.** The next genuinely
free number is **`0088`**.

### 2.3 THE TWO BASES ARE THE FEATURE, AND THEY ARE PROVEN APART

Every budget line is measured on **CLOSING_BALANCE** (a POSITION as at the plan's end date, opening
balance folded in) or **NETT_TRANSACTIONS** (MOVEMENT inside the period, opening balance excluded).
These are Tally's two, and the same ₹10,00,000 means different things under each.

⭐ **THE ACTUAL SIDE WAS NOT REIMPLEMENTED.** `internal/reporting` already had exactly those two
loaders — `loadClosingBalances(asOn)` and `loadPeriodMovement(from, to)` — for exactly this reason.

⛔ **THE ORACLE THAT PROVES `basis` IS HONOURED** is a fixture with a posting INSIDE the financial
year and OUTSIDE the budget period, so the two bases must differ by exactly that amount (₹40,000).
It is asserted in three places: `TestTheTwoBasesGiveDifferentActualsForTheSameLedger`
(`budget_variance_db_test.go`) and two arms of the browser drive. **If that gap is ever ZERO,
`basis` is being ignored** — and both numbers are real, correctly footed and confidently wrong.

### 2.4 THE HONESTY RULES, AND THE ONE THAT WAS NOT IN THE BRIEF

Seven statuses, none a rounding of another: `ON_TARGET`, `OVER`, `UNDER`, `UNSPENT`,
`NETTED_TO_ZERO`, `CONTRA`, `UNBUDGETED`. `variance_percent` is a **pointer** and stays `nil`
against a zero budget. Unbudgeted actuals live in their **own array**, outside the plan's totals.

⭐ **ONE RULE WAS ADDED BEYOND THE BRIEF AND IT IS THE ONE MOST WORTH KNOWING ABOUT: A PLAN THAT
MIXES THE TWO BASES IS REFUSED A GRAND TOTAL.** A position plus a flow is not a quantity. The
server sends `totals_are_addable: false` with **three zeros** and per-basis subtotals; the screen
draws no footer and prints the reason in its place. This is the same call `CostCategorySummary`
makes about cross-axis totals.

### 2.5 ⚠️ TWO DEVIATIONS FROM THE T11 BRIEF — BOTH DELIBERATE, BOTH ARGUED

**(1) `budget-variance` is NOT in `REPORT_NAMES`, though §8 of the brief asked for it.** That list
pushes a `report` FRAME, which renders `UnwiredReportScreen` — *"not available in this build"* —
over a report that IS built and IS ACL-gated. The screen also takes a PLAN and a GRAIN (and an AXIS
at the cost-centre grain) that no reportName frame can carry. Instead a new
`SCREEN_GOTO_ENTRIES` map in `navMachine.ts` routes it in `resolveGotoSelection` **before** the
report fallback. **The keyboard reachability the brief wanted is real and driven** — the browser arm
reaches it with Alt+G and asserts `h1="Budget Variance"`.

⭐ **THE SAME MECHANISM IS NOW AVAILABLE TO THE THREE T2 COST SCREENS**, which are Gateway-only and
have the identical gap. They were deliberately NOT added: that is a change to T2's reachability and
belongs to whoever owns it, not to a budgets lane quietly widening it.

**(2) `GET /groups` was added** (`handler_groups_list.go`). `POST /groups` has existed since Phase 1
with no GET beside it, so a GROUP budget line — which stores an `account_group_id` — could not pick
its target from a served list, and **no master form in this product may ask a human for a uuid.**
⛔ **IT COSTS NO ACL CHANGE**: `/groups` was already in `isMasterPath`, which maps a GET to
`(Display, "master")` — the token `/ledgers` and `/units` read under. It grants nothing new; a group
name is already on every ledger row.

### 2.6 ⚠️ THE TWO JUDGEMENT CALLS THAT DESERVE A SECOND OPINION

1. ⛔ **THE ACL DENY IS AN ASSUMPTION, NOT AN ANSWERED QUESTION.** "Should a data-entry clerk see a
   budget-variance report" is on the owner's ask list and is **unanswered**. 0086 proceeds on
   `0072`'s precedent (deny, for the cost reports) because the failure directions are asymmetric:
   over-denying is a support ticket, under-denying is a clerk reading the company's margins and
   strategy. It is stated as an assumption in 0086's body and in
   `auth_budget_report_test.go`'s header.
   ⛔ **REVERSING IT MEANS DELETING THE DISALLOW, NOT ADDING AN ALLOW** — they tie at specificity 3
   and Disallow beats Allow at equal specificity, so an allow would silently lose.
2. **NO PRO-RATA.** The budget column is always the plan's WHOLE period. Comparing it against a
   shorter window needs a spread rule (straight-line, monthly, seasonal) that Tally makes the
   operator choose and these books give no basis to invent; apportioning by day count would be
   confidently wrong every October for a festival-weighted business. **If the owner wants
   part-period comparison, that is the first T11 follow-up** — and it is a spread-rule decision, not
   a code change.

**Also NOT built, and each named on the FeaturesScreen row:** no revision history (altering a figure
overwrites it), no grand total across mixed bases, no printed layout.

### 2.7 THE MUTATION PROOFS, AND THE ONE THAT FOUND A REAL GAP

Six mutations were made, observed to fail with consequence-named messages, and restored: collapsing
`UNSPENT` into its neighbour, dividing by a zero budget, declaring mixed bases addable, ignoring
`basis`, deleting the 0086 ACL rule, and drawing the footer unconditionally.

⭐ **THE ACL MUTATION IS WORTH QUOTING:** with the rule gone the clerk got **400, not 403** — they
passed the PDP and reached the handler.

⛔⛔ **AND ONE MUTATION LEFT THE SUITE GREEN, WHICH IS THE FINDING.** The zero-budget guard's first
test drove `unbudgetedRow`, which sets `VariancePercent` nil by construction and **never calls the
mutated `scoreBudgetRow`**. The property was asserted on a path that could not execute the protected
code — the same shape as the JWKS security test that passed while testing nothing.
`TestScoreBudgetRowItselfNeverDividesByZero` now drives it directly and fails on the mutation, and
its doc comment says all of this so nobody deletes it as a duplicate.

⚠️ **THE LESSON, GENERALISED, BECAUSE IT WILL RECUR IN T12:** a defence-in-depth guard on an
unreachable branch needs its OWN test. Asserting the property through the path that makes the branch
unreachable proves nothing.

---

## 3. THE GATES — ALL GREEN AS OF 2026-08-28 09:2x, SO ANY RED IS YOURS

```bash
cd backend && go build ./... && go vet ./... && go test ./... -timeout 60m   # 0 FAIL lines, 36 ok packages
bash backend/scripts/test_residue.sh                                         # OK: no residue (bound 0)
cd frontend && pnpm exec tsc --noEmit                                        # clean
cd frontend && pnpm exec vitest run                                          # 1801 passed / 111 files
cd frontend && pnpm verify:demo "$(cat .demo-company-id)"                    # 34/34 ⚠️ id is an ARGUMENT
python3 corpora/validate.py                                                  # OK
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md                # 105 · 99 · 2 · 0 (+4 unreadable)
node frontend/scripts/drive-t11-budgets.mjs                                  # 36/36, tenant reaped
```

⚠️ **THE VITEST BASELINE MOVED: 1787/109 → 1801/111.** T11 added two screen test files (9 + 5). If
you see 1787, you are on a tree without T11.

⚠️ **`make verify` DOES NOT RUN `tsc`**, and `tsc` does not run what `make verify` runs — each has
caught a real error the other missed. ⛔ `make verify` invokes `make migrate`, and **the T8 lane owns
the schema this week.**

### 3.1 ⭐ FOUR ANTI-ROT GUARDS CAUGHT T11 AND WERE SATISFIED, NOT ROUTED AROUND

Expect these to catch you too. Each one is doing its job:

| Guard | What it demanded |
|---|---|
| `handler_print_test.go` deferred census | The count moved 20 → **21**; `handler_print_reports.go`'s header list needed the new name AND its reason |
| `testdb_test.go` teardown fixture | A row in `budget` and `budget_line`, or the teardown of those tables is never exercised |
| `wireDatesInPackageServer` + `writeRoutesNamingADocumentButNotMovingIt` | The back-dated-cutoff decision recorded **in code**, not in a comment — four date fields and five write routes, each with its reason |
| `corpora/validate.py` `KNOWN_SCREENS` | The two new ScreenKinds listed |

⛔ **THE CUTOFF REGISTRIES ARE THE ONES TO TAKE SERIOUSLY.** Their failure text says the shape has
"shipped OPEN three times", and **twice it shipped with a COMMENT asserting the control was in
place**. T11's answer — a budget's period is a planning window, not the date an entry was made, and
nothing in the ledger reads either table — is written into both registries at length, including the
one condition that would falsify it (*if a later track ever lets a budget post*).

### 3.2 The golden that regenerated

```bash
git --no-pager diff frontend/src/api/goldens/printed-report-layouts.json
# total_report_names 30 -> 31; "budget-variance" joins answers_501 (NOT prints_a_layout)
python3 -c "import json;d=json.load(open('frontend/src/api/goldens/printed-report-layouts.json'));\
print(d['total_report_names'], len(d['prints_a_layout']), len(d['answers_501']))"   # -> 31 10 21
```

That is correct: there is no printed budget-variance layout, so **Alt+E must not offer PDF for it.**
The frontend suite asserts against this file.

---

## 4. ⭐ WHAT TO PICK UP NEXT: **T12 GSTR-9**

⛔ **T12 IS UNBLOCKED. Nothing waits on a human, no credentials, no statutory documents, no
decisions outstanding.** That is rare on this board and is why it is next.

`PLAN-TALLY-PARITY.md:476` is the specification:

> **T12 · GSTR-9 annual return** — *"Builds on GSTR-1 + 3B + the new ITC engine. It is a
> RECONCILIATION of the year's returns to the books; it must state where it disagrees rather than
> smoothing it. Not filing-ready without portal-schema verification — keep the disclosure
> discipline."*

And its FeaturesScreen row — `state: "off"` — argues its own case:

> *"Not built. It is a RECONCILIATION of the year's GSTR-1 and GSTR-3B to the books rather than a
> fresh derivation, so it must be able to state where the three disagree instead of smoothing it —
> which is why it is not a report we can generate by summing twelve months and calling it done."*

### 4.1 ⛔⛔ THE RESEARCH IS **ZERO**, AND YOU SHOULD KNOW THAT BEFORE YOU START

```bash
grep -c -i "gstr-9" RESEARCH.md          # -> 0
```

**`RESEARCH.md` does not mention GSTR-9 at all** — not once, not in passing. This is worse than
T11's situation (budgets got three passing mentions). ⛔ **SO DO NOT CITE `RESEARCH.md` FOR GSTR-9
BEHAVIOUR — THERE IS NOTHING THERE.** The table structure (Tables 4–19: outward supplies, ITC
availed/reversed, tax paid, prior-year transactions, demands and refunds, HSN summary) must be
verified against the CBIC notification or the portal's own schema before any claim of parity, and
the PLAN row already says it is **not filing-ready without portal-schema verification**.

⚠️ **THE IRP/PORTAL IS GEO-FENCED FROM THIS MACHINE** (NIC's `164.100.69.x` e-invoice hosts time out
from here), which is a standing constraint recorded in this project's memory. Do not plan on
reaching a live portal to verify a schema.

### 4.2 WHAT ALREADY EXISTS FOR IT TO BUILD ON

```bash
ls backend/internal/compliance/gstr/          # gstr1.go, gstr3b.go + their integration tests
wc -l backend/internal/compliance/gstr/*.go | tail -1     # -> 5029 total
grep -rln "gstr1\|gstr3b" backend/internal | grep -v _test
# -> internal/compliance/gstr/{gstr1,gstr3b}.go, internal/compliance/itc/*, internal/server/handler_gstr3b.go,
#    internal/server/handler_itc.go, internal/authz/vocabulary.go, internal/server/server.go
```

So the three inputs the reconciliation needs — **GSTR-1**, **GSTR-3B** and the **ITC engine** — are
all live and served. **T12 is a third report over three existing planes**, not a new data plane.

### 4.3 THE MIGRATION NUMBER, AND WHETHER YOU NEED TABLES AT ALL

`PLAN-TALLY-PARITY.md` §2.5 reserves **`0087`** for T12 and says:

> *"ACL only unless the reconciliation turns out to need storage — GSTR-9 reads GSTR-1/3B/ITC and
> the books, and a report is not a table. `report:gstr-9` is a `report:NAME` and takes the same
> mandatory refusal as 0086. If tables ARE needed, take **0088** and record it here."*

⛔ **THE ACL HALF IS MANDATORY AND IS THE SAME TRAP T11 JUST WALKED THROUGH — see §5.**

### 4.4 ⭐ COPY T11'S SHAPE, IT IS THE CLOSEST ANALOGUE IN THE TREE

T11 is the most recent report-plus-ACL feature and every file is fresh:

| T11 file | Your T12 equivalent |
|---|---|
| `backend/migrations/0086_budget_report_acl.sql` | **`0087_gstr9_acl.sql`** — derive from **`0086`** (15 → **16 PERFORMs**) |
| `backend/internal/reporting/budget_variance.go` | the reconciliation service |
| `backend/internal/server/handler_budget_variance.go` | `handler_gstr9.go` |
| `backend/internal/authz/vocabulary.go` | add `gstr-9` to `KnownReportNames` (**31 → 32**) |
| `backend/internal/server/auth_middleware.go` | a `reportName()` case; **no routeAction arm needed** if the path is a flat literal |
| `backend/internal/server/auth_budget_report_test.go` | the ACL matrix, with its CONTROL arms |
| `frontend/scripts/drive-t11-budgets.mjs` | the browser drive |

⚠️ **T11 is ~10,600 lines. T12 SHOULD BE SMALLER** — it adds no master, no write path and (probably)
no tables. Copy the *shape*, not the *scale*.

### 4.5 ⛔ THE DESIGN RULE T12 WILL LIVE OR DIE ON

The PLAN and the FeaturesScreen row say the same thing twice, so take it as the specification:
**IT MUST STATE WHERE THE THREE DISAGREE RATHER THAN SMOOTHING IT.**

T11 has a directly reusable pattern for exactly this, and it is the part worth copying line for
line: **a figure the report cannot honestly state is SUPPRESSED and the reason is printed in its
place**, never left as a plausible zero (`totals_are_addable`, and the `nil` variance percentage).
A GSTR-9 that reconciled by picking one source and calling it the answer would be a real, correctly
footed, confidently wrong statutory return — which is the worst artefact this product could emit.

---

## 5. ⛔⛔ THE ONE TRAP THAT WILL BITE T12: A NEW `report:NAME` IS **GRANTED** BY DEFAULT

**If you add `report:gstr-9` and write no ACL rule, every seeded Data Entry clerk can read the
company's annual return the day you ship it, and nothing will tell you.**

Verified at `backend/migrations/0011_auth_acl.sql:249`:

```sql
VALUES (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'display', 'report');
```

That is a **BARE resource**, and `internal/authz` matches it against **any** `report:NAME`. ⛔ **So
the GRANT is the default and the REFUSAL is the thing that must be written.** This is registered
defect **D95**. **Eleven migrations now write that refusal** — 0026, 0028, 0033, 0035, 0046, 0063,
0068, 0072, 0073, 0076, and now 0086.

⭐ **CONTRAST, AND IT IS THE WHOLE DESIGN RULE:** `budget`, `cost-centre`, `tds-challan`,
`sales-order` and `bom` are **bare tokens**, and Data Entry's `default_access = 'none'` denies them
with **zero rules written**. **The shape of the name decides the default.** `report:` is the
dangerous shape.

### 5.1 What `0087` must do

1. Add `gstr-9` to **`authz.KnownReportNames`**. ⛔ A name in that list whose route does not resolve
   is a *build failure* by design, so add the `reportName()` case in the same change.
2. Write `disallow display` **AND** `disallow print` for it, restating `seed_company_auth_defaults`
   derived from **`0086`** — **16 PERFORMs**, and the comment-stripped body diff against 0086 must
   be **exactly one line**.
3. ⛔ **DO NOT EDIT 0086 IN PLACE.** It has been applied.

```bash
# the derivation check T11 used, and it should print exactly one added PERFORM line:
ex() { awk '/^CREATE OR REPLACE FUNCTION seed_company_auth_defaults\(/,/^\$\$;$/' "$1" \
      | grep -v '^\s*--' | sed '/^\s*$/d'; }
diff <(ex backend/migrations/0086_budget_report_acl.sql) <(ex backend/migrations/0087_gstr9_acl.sql)
```

### 5.2 ⚠️ THE SPECIFICITY NUMBERS IN THE OLDER HEADERS ARE OFF BY ONE

Migrations 0026–0073 say the coarse rule matches "at specificity 1". `ruleSpecificity` in
`internal/authz/authz.go` does not say that: it is the SUM of **+1 for an exact ACTION match** and
**+0/+1/+2 for the RESOURCE** (wildcard / `report` / `report:NAME`). So `allow display report`
scores **2** and `disallow display report:gstr-9` scores **3**. The conclusion those headers draw is
right; the number is wrong in eight files. **0076 and 0086 carry the correction — do not propagate
the old number into an eleventh.**

---

## 6. ⭐ CHEAP PICKUPS, IF YOU WANT A WARM-UP BEFORE T12

### 6.1 **D105 — one sentence, and it is now UNBLOCKED**

The register says D105 is *"OPEN — blocked only on file ownership"*: the `FeaturesScreen.tsx`
**Stock Summary** row still says value is derived through a **weighted-average** valuer, but since `0078`
`reporting.replayPositions` picks a valuer **per item** and it may be FIFO or LIFO —
`drive-l28-stock-restale.mjs` measured **three different valuers on one report in one company**.

⭐ **THE BLOCK WAS FILE OWNERSHIP AND T11 HAS JUST EDITED THAT FILE SAFELY.** It is one note string.
⚠️ Announce before editing — the file is shared with the T8 and T32 lanes — and keep it to that row.

```bash
grep -n "weighted-average" frontend/src/screens/FeaturesScreen.tsx    # -> exactly 1 hit
```

⚠️ **CITE FEATURESSCREEN ROWS BY NAME, NEVER BY NUMBER.** The register calls this one "row 23" and
a fresh count makes it 24 — the numbering drifts every time a row is added, and T11 added none but
reordered nothing either. `grep` for the sentence; do not count.

### 6.2 The other open row

**D99** (LOW) is **OPEN BY DECISION, not omission** — its premise did not survive a real TallyPrime
export, and fixing it speculatively would make genuinely malformed numbers readable. ⛔ **Leave it.**
`drive-u1-import-partial.mjs` deliberately fails one arm against it; that 15/16 is expected.

### 6.3 Four register rows are UNREADABLE to the parser

`D49, D80, D82, D84` — all four are closed, but the parser cannot read their status. A tidy-up, not
a defect.

---

## 7. ⛔ RUN FIRST — TRAPS THAT HAVE EACH COST REAL TIME

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
git log --oneline -3 && git status --porcelain | wc -l      # ⛔ HEAD should be at/after 186f89d (T11) — see §0
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # NEWER than the last backend commit?
PGCONNECT_TIMEOUT=5 pg_isready -h localhost -p 5433          # must say "accepting connections"
```

1. ⛔ **CHECK THE AGE OF `cored`.** `make run-cored` is `go run`, so **a stale process is
   indistinguishable from a fix that does not work.** Found stale six times, including this session
   — the running cored was 30 minutes older than the last backend commit.
   **It was restarted 2026-08-28 09:06 and statutoryd at 09:06 after it.**
   ⛔ A cored restart **invalidates statutoryd's minted token**, so restart both, **cored first**:
   ```bash
   # they run detached under `make`, parented to PID 1 — kill the whole tree, do not kill the child
   ps -o pid=,ppid=,command= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"
   ```
2. ⛔ **A TCP CONNECT IS NOT LIVENESS — FOR THE DATABASE TOO.** On 2026-08-28 postgres was wedged
   behind OrbStack's forwarder: `nc -z` succeeded while `pg_isready` said *"no response"*. Recovery:
   ```bash
   orbctl stop && orbctl start && docker start tally_pg   # ⛔ NEVER `orbctl reset` — it deletes ALL data
   ```
   ⚠️ Other projects share that VM (`supabase_db_*`, `dentabill-*`) — start **only** `tally_pg`.
3. ⛔ **VITE MUST BE ON `:5173`** — WebAuthn origin matching is exact. Start it with **`--strictPort`**
   so it fails loudly instead of silently taking 5174. ⚠️ It binds `::1` — use
   `http://localhost:5173/`, **not** `127.0.0.1`.
4. ⚠️ **`FeaturesScreen.tsx` IS SHARED** with the T8 and T32 lanes. **Announce before editing, and
   keep it to your own rows.** Rows are one per line so conflicts resolve trivially; a silent
   double-edit does not.
5. ⭐ **THE FIRST WRITE OF EVERY BROWSER DRIVE 403s — THAT IS THE WebAuthn STEP-UP CEREMONY, NOT A
   BUG.** The wire log reads `POST /x 403` → `webauthn/register/begin` → `finish` → `assert/*` →
   `POST /x 201`.
6. ⛔ **A DRIVE THAT CREATES A TENANT WITH BOOKS NEEDS `session_replication_role = replica` TO REAP
   IT**, and it must be `SET LOCAL`. `posting_immutable` RAISES on DELETE, so the reap fails without
   it — `drive-t11-budgets.mjs` hit exactly this and leaked a tenant on its first run.
   ⚠️ **AND `voucher_head` MUST BE DELETED BEFORE `voucher_version`.** The obvious
   "null out `current_version_id` first" does not work: the column is NOT NULL, and
   `session_replication_role` disarms TRIGGERS, not CHECK/NOT NULL constraints.
   See the reap block at the bottom of `drive-t11-budgets.mjs` — copy it.
7. ⚠️ **BACKTICKS INSIDE A TEMPLATE LITERAL BREAK A DRIVE SCRIPT.** Two of T11's probe messages
   quoted an identifier in backticks inside a `` ` `` string and the file would not parse. Always
   `node --check scripts/your-drive.mjs` before running it.

---

## 8. ⚠️ WHAT IS *NOT* NEXT — AND WHY EACH IS BLOCKED

| Item | Status |
|---|---|
| **T13 Interest on overdue bills** | ⛔ **Blocked on statutory documents.** The day-count basis and simple-vs-compound are configuration with statutory defaults, and inventing them would be worse than the gap. Design note: `docs/design/t13-interest-model.md` |
| **T8 Multi-currency** | **In flight on another lane**, working forward from `0080`. Do not take `0081–0084`; do not edit the schema |
| **Single sign-on (SSO / OIDC)** | Needs an IdP and credentials — waits on a human |
| **Rule 48(4) QR on a printed e-invoice** | Needs the IRP, which is **geo-fenced from this machine** |
| **Split company data** | ⛔ A deliberate **non-goal**. Never build it |
| **T14 Backup / restore** | Unblocked but AFTER T12/T13 in the plan order. ⭐ It should reuse the `internal/platform/tenant` census, which is schema-derived and anti-rot by construction — T11's two tables were picked up by it automatically |

⭐ **After T12 GSTR-9, the plan order is T13 Interest → T14 Backup/restore**
(`PLAN-TALLY-PARITY.md:497`). Since T13 is document-blocked, **T14 is the realistic one after T12.**

---

## 9. DEFINITION OF DONE FOR THE NEXT SESSION

- [x] ⛔ **COMMIT T11** — done, `186f89d`, 39 files / 10,610 insertions.
- [ ] `0087_gstr9_acl.sql` derived from **0086** (15 → 16 PERFORMs), `disallow display` **and**
      `disallow print`, body diff exactly one line.
- [ ] `seeder_census_test.go` passes. ⛔ **Do not route around it.**
- [ ] `gstr-9` in `authz.KnownReportNames` **and** a `reportName()` case that resolves.
- [ ] The reconciliation **states where GSTR-1, GSTR-3B and the books disagree** — no smoothing, no
      plausible zeros, and any figure it cannot honestly state is suppressed WITH its reason in
      place (copy `totals_are_addable`).
- [ ] Reachable from the keyboard, and registered in `defaultScreens.tsx`. ⚠️ If it needs a plan/
      grain-like selector, use `SCREEN_GOTO_ENTRIES` — **not** `REPORT_NAMES` (§2.5).
- [ ] A **browser drive** under `frontend/scripts/`: its own throwaway tenant, reaped in a
      `try/finally`, assertions phrased so the CORRECT behaviour is the PASS, and **control probes**
      that prove the oracle is not vacuous. ⭐ `drive-t11-budgets.mjs` is the current best example —
      it has three controls and labels its pre-existing arm as pre-existing.
- [ ] ⛔ **A Data Entry level is REFUSED the report** — driven, not assumed, with a control proving
      the token is a real member.
- [ ] `FeaturesScreen.tsx` GSTR-9 row flipped to `on` with a note that is **true**, including
      everything not built (filing-readiness above all). ⚠️ Announce first — shared file.
- [ ] `PLAN-TALLY-PARITY.md` §2.5 updated in place to mark `0087` **consumed**.
- [ ] All gates in §3 green — **including the new 1801/111 vitest baseline** — plus mutation proofs.
- [ ] ⛔ **MUTATION-PROVE EVERY GUARD, AND CHECK THE TEST ACTUALLY EXECUTES THE MUTATED CODE.**
      §2.7 is the cautionary tale: one mutation left the suite green because the test reached the
      property by a path that bypassed the guard.
