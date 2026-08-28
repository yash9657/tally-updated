# HANDOFF — T11 AND T12 ARE BOTH **COMMITTED**, AND T14 BACKUP/RESTORE IS NEXT

**Written:** 2026-08-28, working tree CLEAN at `92319a9` · **Branch:** `session/t32-and-close-7-3`
**Read time: 12 minutes.** ⭐ **Every number below was MEASURED and the command is beside it.**

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** It is self-contained: what
shipped, the state of the tree, the gates, the next feature, and the traps. You should not need to
open another handoff to act on it. (`HANDOFF-AFTER-T11.md` is the previous one and is now
superseded except for its §2, which is the T11 map. `HANDOFF-PARITY-AUDIT.md` §7.1 is the defect
register.)

---

## 0. ✅ THE TREE IS CLEAN — NOTHING IS AT RISK

```bash
git status --porcelain | wc -l    # -> 0
git log --oneline -3              # -> 92319a9 T12 · 5bd6a1f docs · 186f89d T11
```

Two features landed this session, in this order:

| Commit | What | Size |
|---|---|---|
| `186f89d` | **T11 Budgets** — built by the previous session, left uncommitted, committed here | 39 files, **10,610** insertions |
| `92319a9` | **T12 GSTR-9** — built here | 29 files, **6,204** insertions |

⚠️ **NEITHER HAS BEEN REVIEWED BY THE OWNER.** Green gates are not a review and a commit is not a
review. §2.4 lists T12's judgement calls; `HANDOFF-AFTER-T11.md` §2.6 lists T11's, and its ACL
question is **still unanswered**.

---

## 1. WHAT THIS PRODUCT IS, IN FIVE LINES

An AI-native replacement for **Tally**, India's dominant SMB accounting package. Go backend
(`cored`, port 8087) + React/TypeScript keyboard-first frontend (vite, 5173) + Postgres (5433).
**Every voucher is immutable and hash-chained**; reports are DERIVED, never stored. Multi-tenant
with Postgres **RLS scoped by a company GUC**, and a Tally-style **Disallow/Allow ACL** decides who
may see what. `frontend/src/screens/FeaturesScreen.tsx` is the product's own truth-telling surface.

```bash
python3 -c "import re,collections;s=open('frontend/src/screens/FeaturesScreen.tsx').read();\
print(collections.Counter(re.findall(r'state:\s*\"(\w+)\"',s)))"
# -> {'on': 47, 'off': 5, 'always': 1, 'server': 1}   (54 rows; T12 flipped one off->on)
```

---

## 2. WHAT T12 SHIPPED

**The GSTR-9 annual return, as a RECONCILIATION rather than a derivation.** GSTR-1 and GSTR-3B each
read the voucher log for a month and state what they find. GSTR-9 asks whether the year's FILED
RETURNS and the BOOKS tell the same story, and its content is the places where they do not.

### 2.1 The map

```bash
wc -l backend/migrations/0087_gstr9_acl.sql backend/internal/compliance/gstr/gstr9.go \
      backend/internal/server/handler_gstr9.go frontend/src/screens/reports/GSTR9Screen.tsx \
      frontend/scripts/drive-t12-gstr9.mjs      # -> 3272 total
```

| File | What it is |
|---|---|
| `backend/internal/compliance/gstr/gstr9.go` (1209) | The whole engine: the year, the four comparisons, the tables, the disclosures, `Validate9` |
| `backend/migrations/0087_gstr9_acl.sql` (380) | The ACL counterweight. **16 PERFORMs**, derived from `0086` |
| `frontend/src/screens/reports/GSTR9Screen.tsx` (845) | The screen |
| `frontend/scripts/drive-t12-gstr9.mjs` (661) | The browser drive, **33/33** |
| `backend/internal/server/handler_gstr9.go` (177) | The one route |
| + 4 test files | `gstr9_test.go` (pure), `gstr9_integration_test.go` (db), `auth_gstr9_report_test.go`, `GSTR9Screen.test.tsx` |

### 2.2 ⭐ THE ONE RULE THE FEATURE LIVES ON: **A NET DIFFERENCE IS A LIE**

An annual return is twelve months added up, and **addition CANCELS**. A company that over-declared
40,000 of IGST in May and under-declared 40,000 in September has an annual net of **ZERO** — every
figure foots, every total is correct, and the return is silently wrong about the one thing it
exists to say.

So **no difference is ever reported as a net alone.** Every comparison carries three things:

| | |
|---|---|
| `net` | sum over months of (A − B) — **it cancels** |
| `gross` | sum over months of \|A − B\| — **it cannot** |
| `months` | the FPs that disagree, so the reader can go and look |

and when they differ, `offsetting` fires, the `monthly-differences-offset` disclosure fires, and the
screen prints the months in place of the reassuring zero. **This is T11's `totals_are_addable`
discipline applied to a different shape of lie.**

⛔ **THE ABSOLUTE VALUE IS TAKEN HEAD BY HEAD, NOT ON THE THREE-HEAD TOTAL.** +100 CGST against
−100 SGST is two errors — a supply reported inter-state that was intra-state, or the reverse — and a
total-level absolute would score it clean.

⛔ **`Agrees` IS A PER-MONTH PROPERTY AND IS NEVER `Net.IsZero()`.** That is exactly the question a
net cannot answer. `Validate9` enforces it **at runtime**, not only in tests, because the failure is
invisible in the output.

### 2.3 FOUR INDEPENDENT PATHS, AND WHY THAT IS THE WHOLE VALUE

|   | Source | Reads |
|---|---|---|
| 1 | GSTR-1 | the section classifier over `voucher_version` GST aggregates |
| 2 | GSTR-3B | its **own** SQL over the same columns (its header says why it must not delegate) |
| 3 | The books | `posting` rows against the six named tax ledgers — a different table, a different key |
| 4 | GSTR-1 table 12 | **`voucher_gst_line`** — PER-LINE detail, the only path that does |

⭐ **THE FOURTH WAS FREE.** `Generate` already loads the HSN rollup, so it cost no extra read.
Migration 0016 *asserts* the lines sum to the aggregate; `HSNVsR1` is what **checks** that they do,
on real data, for twelve months. It is why table 17 is DERIVED rather than an excuse.

### 2.4 ⚠️ THE JUDGEMENT CALLS

1. **THE NAME IS `report:gstr9`, NOT the `report:gstr-9` PLAN §2.5 RESERVED.** The two names it
   sits beside are `gstr1` and `gstr3b`, served at `/gstr1` and `/gstr3b`. One member of that list
   spelled with a hyphen for no visible reason is how an administrator writes a rule for a resource
   that does not exist and silently governs nothing. §2.5 is updated in place;
   `TestTheAnnualReturnIsSpelledLikeItsTwoSiblings` pins **both** halves (gstr9 present, gstr-9
   absent). ⛔ **Do not "correct" it back** — 0011's coarse `allow display report` would then reach
   the real name unopposed while the rule list said "refused".
2. **THE DENY IS NOT RECORDED AS AN ASSUMPTION**, unlike 0086's — and 0087's header says why rather
   than inheriting the caveat by copying. 0035 already refuses payroll totals to this level and 0072
   departmental profitability; a document strictly more disclosive than both, which additionally
   names the months this company may have mis-filed, raises no new question.
3. **NO `statement_id`.** A 2A/2B statement belongs to ONE period and this covers twelve, so
   applying one would reconcile eleven months against a statement that has nothing to do with them.
   The consequence is disclosed, not hidden: table 7 is NOT_DERIVED and
   `itc-reversals-not-computed` is unconditional.
4. **THE BOOKS COLUMN IS REQUIRED**, where `/gstr1` and `/gstr3b` make the ledgers optional. Without
   it every month looks wrong by exactly the tax charged — twelve findings that are all artefacts of
   a missing parameter, which is worse than an error.

### 2.5 WHAT IS NOT BUILT, AND IT IS MOST OF THE FORM

**Eleven of the fourteen statutory tables are NOT_DERIVED**, each naming the input it is missing
rather than emitting a zero. ⛔ **Table 7 (ITC reversed) is the one to understand**: a zero there
asserts the taxpayer reversed NOTHING, when the truth is that nothing was checked. **Table 4's 4N
TOTAL IS SUPPRESSED** with the reason printed in its place — it totals rows five of which are not
derived, and a total over a set with known-missing members is an **understatement that foots**.

⛔ **IT IS NOT FILING-READY AND NEVER WILL BE WITHOUT PORTAL VERIFICATION.**

```bash
grep -c -i "gstr-9" RESEARCH.md    # -> 0
```

`RESEARCH.md` has **no** GSTR-9 material. The table numbering was written from the return's
published structure and the portal is geo-fenced from this machine, so it could not be checked. Ten
unconditional disclosures say so on every response, headed `not-filing-ready`.

---

## 3. THE GATES — ALL GREEN AS OF 2026-08-28 11:2x, SO ANY RED IS YOURS

```bash
cd backend && go build ./... && go vet ./... && go test ./... -timeout 60m   # 0 FAIL, 36 ok packages
bash backend/scripts/test_residue.sh                                         # OK: no residue (bound 0)
cd frontend && pnpm exec tsc --noEmit                                        # clean
cd frontend && pnpm exec vitest run                                          # 1812 passed / 112 files
cd frontend && pnpm verify:demo "$(cat .demo-company-id)"                    # 34/34 ⚠️ id is an ARGUMENT
python3 corpora/validate.py                                                  # OK
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md                # 105 · 99 · 2 · 0 (+4 unreadable)
node frontend/scripts/drive-t12-gstr9.mjs                                    # 33/33, tenant reaped
node frontend/scripts/drive-t11-budgets.mjs                                  # 36/36, tenant reaped
```

⚠️ **THE VITEST BASELINE MOVED TWICE TODAY: 1787/109 → 1801/111 (T11) → 1812/112 (T12).**
If you see 1801, you are on a tree without T12.

⚠️ **`make verify` DOES NOT RUN `tsc`**, and `tsc` does not run what `make verify` runs — each has
caught a real error the other missed. ⛔ `make verify` invokes `make migrate`, and **the T8 lane
owns the schema**.

### 3.1 ⭐ THREE ANTI-ROT GUARDS CAUGHT T12 AND WERE SATISFIED, NOT ROUTED AROUND

| Guard | What it demanded |
|---|---|
| `handler_print_test.go` deferred census | 21 → **22**; `handler_print_reports.go`'s header list needed the name AND its reason |
| `printDisallowedBySeededMigrations` census | 23 → **24** `disallow print` rows — and it wants the **disclosure sentence in code**, not a slug |
| `printed-report-layouts.json` golden | 31 → **32** report names; `gstr9` joins `answers_501`, so **Alt+E must not offer PDF** |

```bash
python3 -c "import json;d=json.load(open('frontend/src/api/goldens/printed-report-layouts.json'));\
print(d['total_report_names'], len(d['prints_a_layout']), len(d['answers_501']))"   # -> 32 10 22
```

⭐ **THE BACK-DATED CUTOFF REGISTRIES DID *NOT* FIRE, AND THAT IS CORRECT** — T12 adds no write
route and no date column. T11 had to answer them at length; do not assume you will not.

### 3.2 The migration derivation, and the census one-liner that lies

```bash
for f in backend/migrations/*.sql; do \
  c=$(awk '/^CREATE OR REPLACE FUNCTION seed_company_auth_defaults\(/,/^\$\$;$/' "$f" \
      | grep -c 'PERFORM seed_data_entry'); [ "$c" -gt 0 ] && echo "$(basename $f) -> $c"; done | tail -3
# -> 0076 -> 14 · 0086 -> 15 · 0087 -> 16

# the derivation check, which must print EXACTLY ONE added PERFORM line:
ex() { awk '/^CREATE OR REPLACE FUNCTION seed_company_auth_defaults\(/,/^\$\$;$/' "$1" \
      | grep -v '^\s*--' | sed '/^\s*$/d'; }
diff <(ex backend/migrations/0086_budget_report_acl.sql) <(ex backend/migrations/0087_gstr9_acl.sql)
```

⛔⛔ **ANCHOR ON `^CREATE OR REPLACE FUNCTION` AND `^\$\$;$`.** The one-liner quoted inside 0086's
own body is anchored on any line *containing* `seed_company_auth_defaults`, which matches the file's
COMMENTS — so the range opens early, never closes, and reports `0086 -> 22`. 0087's header carries
the corrected command.

⚠️ **`0081–0084` REMAIN A DELIBERATE GAP** for the in-flight T8 multi-currency lane, working
forward from `0080`. ⭐ **T12 TOOK NO TABLE NUMBER — `0088` IS FREE AND IS THE NEXT ONE.**

---

## 4. ⭐ WHAT TO PICK UP NEXT: **T14 BACKUP / RESTORE**

The plan order is T11 → T12 → **T13 Interest** → **T14 Backup/restore**
(`PLAN-TALLY-PARITY.md:497`). ⛔ **T13 IS BLOCKED ON STATUTORY DOCUMENTS** — the day-count basis and
simple-vs-compound are configuration with statutory defaults, and inventing them would be worse than
the gap. Design note: `docs/design/t13-interest-model.md`. **So T14 is the realistic next one.**

`PLAN-TALLY-PARITY.md:478` is the specification:

> **T14 · Backup / restore, split company data** — *"Operationally important and currently absent.
> Must be tenant-exact: a restore that leaks one company's rows into another is worse than no
> backup. Reuse the `testdb` delete-plan census — it already knows every tenant-scoped table and is
> anti-rot by construction."*

### 4.1 ⭐ THE CENSUS ALREADY EXISTS AND IS SCHEMA-DERIVED

```bash
ls backend/internal/platform/tenant/     # -> census.go, census_test.go
```

⭐ **IT IS ANTI-ROT BY CONSTRUCTION AND HAS ALREADY PROVEN IT TWICE**: T11's two new tables
(`budget`, `budget_line`) were picked up automatically, and T12 added none. A backup that enumerated
tables by hand would go stale the first time anyone shipped a migration.

⚠️ **`split company data` IS A DELIBERATE NON-GOAL** (§8) even though it shares T14's row. **Never
build it.** Back up and restore; do not split.

### 4.2 THE MIGRATION NUMBER

**`0060–0061` are reserved for T14** (`PLAN-TALLY-PARITY.md:179`). ⚠️ **CHECK THAT RESERVATION
BEFORE TAKING IT.** Numbers below the highest `seed_company_auth_defaults` restatement are the
0071-class trap — if T14 needs *any* ACL rows, those numbers **cannot carry them** and it must take
a fresh number at the end (**0088**). Tables alone are fine at 0060–0061; a seeder is not.

### 4.3 ⛔ THE DESIGN RULE T14 WILL LIVE OR DIE ON

**A restore that leaks one company's rows into another is worse than no backup.** The nearest
reusable discipline in the tree is T12's: **a figure the system cannot honestly state is SUPPRESSED
and the reason is printed in its place.** Applied here that means a restore must REFUSE a partial or
ambiguous archive rather than restoring what it can — a half-restored tenant that foots is exactly
the shape this repository's register is full of.

---

## 5. ⛔⛔ THE TRAP T12 DISCOVERED, AND IT WILL BITE THE NEXT MIGRATION TOO

**A MUTATION TO A MIGRATION IS INVISIBLE UNLESS YOU UN-STAMP `schema_migrations` FIRST.**

`migrations.go` skips a file already recorded in `schema_migrations`. So deleting the
`disallow display` rule from `0087` and re-running the ACL tests left **the whole suite GREEN** —
which reads exactly like a test that proves nothing. It does not: the mutation had never been
applied.

```bash
# the mutation harness that actually works:
PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally \
  -tAc "DELETE FROM schema_migrations WHERE version='0087_gstr9_acl';"
# ...now edit the file and run the tests; they re-apply it.
```

⭐ **WITH THE UN-STAMP, IT FAILED EXACTLY AS T11'S DID: the clerk got 400, NOT 403** — they passed
the PDP and reached the handler. That is the signature of a missing ACL rule and it is worth
recognising on sight.

### 5.1 AND THE STANDING TRAP: A NEW `report:NAME` IS **GRANTED** BY DEFAULT

Verified at `backend/migrations/0011_auth_acl.sql:249` — Data Entry is seeded a coarse
`allow display report` against a **BARE** resource, and `internal/authz` matches it against **any**
`report:NAME`. **So the GRANT is the default and the REFUSAL is what must be written.** Registered
defect **D95**. **Twelve migrations now write it**: 0026, 0028, 0033, 0035, 0046, 0063, 0068, 0072,
0073, 0076, 0086 and 0087.

⭐ **CONTRAST, AND IT IS THE DESIGN RULE:** `budget`, `cost-centre`, `tds-challan`, `sales-order` and
`bom` are **bare tokens**, and Data Entry's `default_access = 'none'` denies them with **zero rules
written**. **The shape of the name decides the default.** `report:` is the dangerous shape.

⚠️ **THE SPECIFICITY NUMBERS IN HEADERS 0026–0073 ARE OFF BY ONE.** `ruleSpecificity` sums **+1 for
an exact ACTION match** and **+0/+1/+2 for the RESOURCE**, so `allow display report` scores **2** and
`disallow display report:NAME` scores **3**. 0076, 0086 and 0087 carry the correction.

---

## 6. ⛔ RUN FIRST — TRAPS THAT HAVE EACH COST REAL TIME

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
git log --oneline -3 && git status --porcelain | wc -l      # expect 0 on a fresh pull
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # NEWER than the last backend commit?
PGCONNECT_TIMEOUT=5 pg_isready -h localhost -p 5433          # must say "accepting connections"
```

1. ⛔ **CHECK THE AGE OF `cored`.** `make run-cored` is `go run`, so **a stale process is
   indistinguishable from a fix that does not work.** Found stale **seven** times now, including
   this session — the running pair was from **09:06** and predated every line of T12.
   **Restarted 2026-08-28 11:06 (cored) and 11:06 (statutoryd, after it).**
   ⛔ A cored restart **invalidates statutoryd's minted token**, so restart both, **cored first**:
   ```bash
   # they run detached under `make`, parented to the go-run process — kill the whole tree
   ps -o pid=,ppid=,command= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"
   kill <the go-run parent AND the exe pid>
   nohup make run-cored > /tmp/cored.log 2>&1 &      # ~20s to listen
   nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 &
   ```
2. ⛔ **A TCP CONNECT IS NOT LIVENESS — FOR THE DATABASE TOO.** Postgres has been wedged behind
   OrbStack's forwarder with `nc -z` succeeding while `pg_isready` said *"no response"*. Recovery:
   ```bash
   orbctl stop && orbctl start && docker start tally_pg   # ⛔ NEVER `orbctl reset` — it deletes ALL data
   ```
   ⚠️ Other projects share that VM (`supabase_db_*`, `dentabill-*`) — start **only** `tally_pg`.
3. ⛔ **VITE MUST BE ON `:5173`** — WebAuthn origin matching is exact. Start it with `--strictPort`.
   ⚠️ It binds `::1` — use `http://localhost:5173/`, **not** `127.0.0.1`.
4. ⚠️ **`FeaturesScreen.tsx` IS SHARED** with the T8 and T32 lanes. **Announce before editing, and
   keep it to your own rows.** ⚠️ **CITE ROWS BY NAME, NEVER BY NUMBER** — the numbering drifts.
5. ⭐ **THE FIRST WRITE OF EVERY BROWSER DRIVE 403s — THAT IS THE WebAuthn STEP-UP CEREMONY, NOT A
   BUG.**
6. ⛔ **A DRIVE THAT CREATES A TENANT WITH BOOKS NEEDS `SET LOCAL session_replication_role = replica`
   TO REAP IT.** `posting_immutable` RAISES on DELETE. ⚠️ **AND `voucher_head` MUST BE DELETED
   BEFORE `voucher_version`** — nulling `current_version_id` first does not work (NOT NULL is a
   constraint, and `session_replication_role` disarms TRIGGERS only). ⚠️ **A GST tenant also holds
   `voucher_gst_line` rows** — T12's reap block has the complete list; copy it.
7. ⚠️ **BACKTICKS INSIDE A TEMPLATE LITERAL BREAK A DRIVE SCRIPT.** Always
   `node --check scripts/your-drive.mjs` before running it.
8. ⛔ **cored REFUSES A SALES VOUCHER WHOSE PARTY POSTING IS A CREDIT**, with a message saying why —
   it would turn a receivable into a payable and it would BALANCE. T12's fixture hit this; carry the
   direction on a non-party leg instead.

---

## 7. ⚠️ WHAT IS *NOT* NEXT — AND WHY EACH IS BLOCKED

| Item | Status |
|---|---|
| **T13 Interest on overdue bills** | ⛔ **Blocked on statutory documents.** Day-count basis and simple-vs-compound are configuration with statutory defaults. `docs/design/t13-interest-model.md` |
| **T8 Multi-currency** | **In flight on another lane**, forward from `0080`. Do not take `0081–0084`; do not edit the schema |
| **Single sign-on (SSO / OIDC)** | Needs an IdP and credentials — waits on a human |
| **Rule 48(4) QR on a printed e-invoice** | Needs the IRP, **geo-fenced from this machine** |
| **Split company data** | ⛔ A deliberate **non-goal**. Never build it |
| **A printed GSTR-9** | ⛔ Deliberately 501. On screen the ten disclosures sit beside the figures; on paper, a tidy annual return on company stationery with nowhere for ten caveats is a document somebody hands to a tax officer. It needs a layout where the disclosures come FIRST |
| **A statement-backed annual ITC** | Needs a statement PER MONTH — a different signature and a different decision. See §2.4(3) |

### 7.1 CHEAP PICKUPS

- **D105** (LOW, OPEN) — the `FeaturesScreen.tsx` **Stock Summary** row still says value is derived
  through a **weighted-average** valuer, but since `0078` `reporting.replayPositions` picks a valuer
  **per item**. One note string. ⚠️ Announce first — shared file.
  ```bash
  grep -n "weighted-average" frontend/src/screens/FeaturesScreen.tsx   # -> exactly 1 hit
  ```
- **D99** (LOW) is **OPEN BY DECISION, not omission** — fixing it speculatively would make genuinely
  malformed numbers readable. ⛔ **Leave it.** `drive-u1-import-partial.mjs` fails one arm against
  it deliberately; 15/16 is expected.
- **D49, D80, D82, D84** are all closed but unreadable to the parser. A tidy-up, not a defect.

---

## 8. DEFINITION OF DONE FOR THE NEXT SESSION

- [ ] Whatever T14 needs is **tenant-exact**, and it **reuses `internal/platform/tenant`'s
      schema-derived census** rather than listing tables by hand.
- [ ] ⛔ A restore **REFUSES a partial or ambiguous archive** rather than restoring what it can. A
      half-restored tenant that foots is the failure shape to design against.
- [ ] If it takes an ACL name, it takes a **fresh number at the end (0088)** and not 0060–0061 —
      see §4.2 — and it writes `disallow display` **AND** `disallow print`.
- [ ] `seeder_census_test.go` passes. ⛔ **Do not route around it.**
- [ ] A **browser drive** under `frontend/scripts/`: its own throwaway tenant, reaped in a
      `try/finally`, assertions phrased so the CORRECT behaviour is the PASS, and **control probes**
      that prove the oracle is not vacuous. ⭐ `drive-t12-gstr9.mjs` is the current best example —
      it has **four** controls, labels its pre-existing arm as pre-existing, and **found a real
      defect the unit tests did not** (a refused operator met the 403 only after filling in a form).
- [ ] `FeaturesScreen.tsx` row flipped with a note that is **true**, including everything not built.
      ⚠️ Announce first — shared file.
- [ ] `PLAN-TALLY-PARITY.md` §2.5 updated **in place**.
- [ ] All gates in §3 green — **including the new 1812/112 vitest baseline**.
- [ ] ⛔ **MUTATION-PROVE EVERY GUARD, AND CHECK THE TEST ACTUALLY EXECUTES THE MUTATED CODE.**
      T11's cautionary tale: one mutation left the suite green because the test reached the property
      by a path that bypassed the guard. **T12's addition: a migration mutation needs the
      `schema_migrations` un-stamp of §5, or it is never applied and the green means nothing.**
      Restore byte-identically and verify with `shasum -a 256`.
