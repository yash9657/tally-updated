# NEXT SESSION — test-data hygiene, then closing stock, GSTR-2A/2B + IMS, and payroll

> ## ⚠️ READ THIS BEFORE YOU TOUCH ANYTHING
>
> **This is accounting software. Real companies will file real returns from its
> output and pay real people from its payroll.** A wrong number here is not a bug
> report, it is somebody's tax notice or somebody's short salary. Every wave in
> §5 therefore ends with a **mandatory adversarial review pass** (§4.3) that has
> the authority to send work back. Do not skip it because the suite is green — on
> 2026-07-27 an adversarial pass over eleven "done" commits found **seven** of
> nine claims wrong, almost always because a test could not fail.
>
> **Work in parallel wherever the work is genuinely independent.** Use dynamic
> workflows (§4.1). The three feature tracks in §5 touch disjoint packages by
> design and can run concurrently. What must NOT be parallelised is listed in
> §4.2 — get that wrong and you will spend an hour chasing a phantom regression,
> as this project has done twice.
>
> **Do §5 Track 0 first.** It is small, it unblocks everything else, and until it
> is done your gates will keep going flaky for reasons that have nothing to do
> with your code.

---

## 1. Where things stand

Branch `master`, tree clean, HEAD **`aea1936`**. Migrations run to **0031**.

`HANDOFF.md` is the authoritative project runbook — architecture, ADRs, the
honest-gaps list (§6), the worklist (§10). This file is the
verify-then-continue wrapper for one handover.

### 1.1 What landed last session

Phase-3 **inventory depth**, in five commits plus a test fix:

| Commit | What |
|---|---|
| `6e42a5a` | Foundation: `stock_movement` as an immutable versioned child of `voucher_version`; the pure `internal/inventory` valuation leaf; Stock Summary |
| `324b45b` | Item Invoice — stock lines DERIVE the accounting and GST lines; report depth; frontend item grid |
| `eacf62e` | Mounted four stock reports; **added the `acl_rule` indexes that had never existed** |
| `bc84519` | Non-accounting stock vouchers (delivery/receipt note, stock journal, physical stock); price lists |
| `8eafafa` | Stock voucher SCREENS; price lists applied in the voucher path; one closing-position query |
| `aea1936` | e2e stability: wait for the company picker instead of sleeping for it |

**The one architectural decision that constrains everything after it: STOCK VALUE
IS NEVER STORED.** Quantity is posted; value is derived at report time by
replaying the movement ledger through a pure valuer. Valuation is
order-dependent — a back-dated inward re-values every later outward — so a
materialised value is wrong the moment a voucher is back-dated or altered.

**Everything rests on ONE non-test `INSERT INTO stock_movement`**
(`internal/ledger/stock.go`, inside `ledger.post()`'s transaction). If a change
adds a second write path, every guard in the feature becomes advisory. §3.2
check 1 verifies this; run it before you trust anything else.

### 1.2 Already done — do not redo

The previous handoff listed these as next; they shipped in `8eafafa`:

- The optional `rate` on the HTTP voucher path **is wired** (`inventoryLineReq.Rate`
  is a `*string`, so absent ≠ `"0"`). Price lists ARE reachable from HTTP.
- The triplicated closing-position query **is hoisted** to `closingPosition()` in
  `internal/ledger/stock.go`. Two spellings remain overall — that Go function and
  migration 0025's trigger, which is the authority — and a test pins them.

---

## 2. What this session is for

Four tracks, in this order. **Track 0 first and alone; then 1, 2 and 3 may run
concurrently** — they touch disjoint packages.

| Track | Work | Primary package |
|---|---|---|
| **0** | Test-data hygiene + harness cleanup | `internal/*_test.go`, seed scripts |
| **1** | **Closing stock on the Balance Sheet and P&L** | `internal/reporting` |
| **2** | **GSTR-2A/2B + IMS reconciliation** | `internal/compliance/gstr` (new files) |
| **3** | **Payroll — Form 24Q, PF ECR, ESI** | `internal/payroll` (new package) |

Full specs in §5.

---

## 3. CONFIRM THE BASELINE (before building anything)

### 3.1 Prerequisites and the gate matrix

Postgres `tally_pg` on **:5433** · cored **:8087** · insightd **:8088** ·
statutoryd **:8091** · vite **:5173**.

```bash
# tally_pg does NOT auto-start after an OrbStack restart:
docker start tally_pg

# Rebuild cored from the CURRENT tree — a stale binary silently 404s new routes.
cd backend && go build -o /tmp/cored ./cmd/cored && go run ./cmd/cored migrate
AUTH_DEV_TOKEN_ENABLED=true PORT=8087 \
  APP_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" /tmp/cored serve &

# insightd MUST carry the DSN or verify:demo drops a check.
cd insightd && . .venv/bin/activate && \
  INSIGHTD_PII_BOUNDARY_STRICT=1 \
  INSIGHTD_DATABASE_URL="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" \
  python -m uvicorn insightd.api.app:app --port 8088 &
```

> The `?sslmode=disable` on that DSN is **load-bearing for the test suite**, not
> decoration. `insightd/tests/test_pg_store.py::_schemaless_dsn` derives an
> unmigrated database with `dsn.replace("/tally?", "/template1?")`; with no `?` in
> the DSN that rewrite is a no-op, `_schemaless_dsn` skips, and **two tests
> disappear silently** — including the one that proves a set-but-unmigrated DSN
> fails loud instead of degrading to the in-memory store.

| Gate | Expected at `aea1936` |
|---|---|
| `cd backend && go build ./... && go vet ./... && go test ./... -count=1` | exit 0, **27 packages** (21 ok + 6 no-test) — was 25 before Track 0 added `internal/platform/testdb` and `internal/testhygiene`. Measured by the integrator on a quiescent, freshly-reset database. |
| `make backend-test-residue` | the same suite, ONCE, + per-table residue all **0** (this is what `make verify` now runs instead of `backend-test`) |
| `cd backend && go test ./internal/testhygiene -count=1` | 3 PASS — the DB-free static half of the residue bound |
| `cd frontend && pnpm exec tsc --noEmit` | clean (covers `scripts/` too) |
| `cd frontend && pnpm exec vitest run` | **370 tests / 33 files** |
| `cd frontend && pnpm run build` | ok |
| `cd insightd && python -m pytest` | **107 passed, 0 skipped** (was 106; Track 0 added `test_the_harness_hides_the_environments_dsn_from_a_freshly_built_settings`) |
| `python -m insightd.benchmark.validate` / `.report` | 28 fixtures / ALL FLOORS PASSED |
| `python3 corpora/validate.py` | tax=14 tds=13 tcs=5 keystroke=32 isolation=13 migration=6 forecast=4 inventory=6 |

Live: `verify:demo` **24/24** · `verify:tenancy` **113/113** · `e2e:ui` **38/38
twice** · `e2e:edit`/`e2e:numbering`/`e2e:state-code`/`e2e:ocr` OK · statutory
freeze OK including the durable-restart phase.

> **If `insightd` reports skips instead of 106 passed, the database is down** —
> `test_pg_store` skips with "postgres unreachable". That is the environment, not
> your code.

### 3.2 Structural spot-checks — these do not depend on any test being honest

```bash
# 1. EXACTLY ONE non-test writer of stock_movement. The whole design rests on it.
grep -rn "INSERT INTO stock_movement" backend --include='*.go' | grep -v _test.go
#    expect exactly one: backend/internal/ledger/stock.go

# 2. NO stored stock value or valuation-rate column anywhere.
psql "$PG" -c "\d stock_movement"     # qty, rate, discount_pct, amount — all as ENTERED

# 3. Triggers: immutability IMMEDIATE, negative-stock DEFERRED, and the
#    negative-stock body joining voucher_head.current_version_id AND is_deleted
#    (without that an ALTERed voucher double-counts).
psql "$PG" -c "select tgname, tgdeferrable, tginitdeferred from pg_trigger t
  join pg_class c on c.oid=t.tgrelid where c.relname='stock_movement' and not tgisinternal;"

# 4. A voucher hashed BEFORE inventory must still verify its chain (the
#    CanonicalPayload one-way door). This used to say "GET
#    /vouchers/{id}/history on any pre-0025 voucher" — that check is RETIRED,
#    not skipped: `make reset-dev-db` destroys all 283,066 pre-0025
#    voucher_version rows and they cannot be regenerated, so on any fresh
#    database there is no such voucher left to point at.
#
#    The replacement reproduces a genuinely pre-inventory row instead of hunting
#    for one. It inserts, as ADMIN, a voucher_version carrying the PINNED
#    pre-inventory payload bytes and the PINNED BLAKE3 hash (copied verbatim
#    from internal/ledger/stock_hash_test.go: preInventoryJSON / preInventoryHash
#    = ec486146e6...) into a throwaway company, then reads it back over HTTP:
cd backend && go test ./internal/server -run TestPreInventoryVoucherStillVerifiesItsChain -count=1 -v
#    plus the DB-free wedge that pins the hash constant itself:
cd backend && go test ./internal/ledger -run TestExistingVoucherHashesAreUnchanged -count=1 -v
```

---

## 4. HOW TO WORK

### 4.1 Dynamic workflows, and what to parallelise

Use the `Workflow` tool. Keep the ORCHESTRATOR's context small: agents read
files and return conclusions; you integrate. The shape that worked:

```
phase('Understand')  → N parallel scouts, one per seam, returning structured notes
phase('Design')      → 1 architect that must MAKE the hard decisions, not list them
phase('Build')       → parallel slices on DISJOINT file sets
phase('Wiring')      → SERIAL, one owner for server.go / auth_middleware.go / vocabulary.go
phase('Review')      → adversarial, §4.3 — mandatory
```

**Rules that earned their place:**

- **Slices in one wave must own disjoint files.** Anything touching
  `server.go`, `auth_middleware.go` or `authz/vocabulary.go` goes in ONE serial
  wiring slice. Those three cannot be edited in parallel.
- **Mounting a report is a THREE-touch change** — `routes()` + `reportName()` +
  `authz.KnownReportNames` — and two tests pin closure in both directions. A
  slice that owns only one of the three cannot ship a route. Last session four
  report services were built that no route served, for exactly this reason.
- **Give each agent its acceptance criteria AND the mutation that must make each
  one fail.** Agents that were handed mutations found three of them aimed at the
  wrong site and built better ones. That is the process working.
- **An agent that stalls has often left work on disk.** Verify the TREE, not the
  summary. One stalled mid-mutation last session; the code was fine, the proofs
  were not finished.

### 4.2 What must NOT run concurrently — this has cost hours

- **Live browser gates (`e2e:ui`) run ALONE.** Nothing else may touch the
  database. A second run came back 33/38 and looked like a dirty-DB regression;
  it was a verification agent editing ACL rules on the live DB at that moment.
  Re-run alone: 38/38.
- **No agent may modify ACL rules, security levels or members of a PRE-EXISTING
  company.** Make a throwaway company instead.
- ~~**Never run `go test ./internal/server` unscoped**~~ — **THIS BAN IS LIFTED**
  (Track 0). It existed because two test harnesses ran `ALTER TABLE … DISABLE
  TRIGGER` around their cleanup, and that statement is **database-wide and
  persistent**, not session-scoped: while it was lifted, voucher immutability was
  off for every connection, so a concurrent test asserting "the append-only
  trigger rejects this UPDATE" passed while the trigger did nothing — and a
  `t.Fatalf` between the disable and the re-enable left the invariant disarmed in
  the shared database with nothing reporting it. Both sites are **deleted**
  (`internal/security/isolation_test.go`, `internal/server/voucher_history_test.go`).
  The only remaining mechanism is `internal/platform/testdb`'s **transaction-scoped
  `SET LOCAL session_replication_role = replica`**, which `SET LOCAL` guarantees
  cannot outlive its transaction, so no other connection ever observes it.
  `internal/security` carries a test that fails the build if the banned statement
  comes back. `make backend-test` (the unscoped `go test ./...`) is therefore
  trustworthy again — which is what let `verify` depend on `backend-test-residue`.
- **`go test ./...` under other load will produce false failures.**
  `internal/sync`'s `TestFuzzPartition` timed out at 600s and later died on
  `insert voucher_version: unexpected EOF`; it passes alone in 158s and the exact
  failing seed passes in 0.3s.

### 4.3 THE REVIEW PASS — mandatory, and it can send work back

Every wave ends here. This is the part the user cares most about.

Run reviewers **in parallel, one per lens**, each adversarial and each required
to produce evidence rather than an opinion:

1. **Correctness-by-mutation.** For every new invariant, apply a mutation that
   should break it and confirm a NAMED test fails with a message that states the
   consequence. **A mutation that does not bite is a FINDING**, not a formality.
   Restore byte-identical and prove it with `shasum -a 256`.
2. **Arithmetic re-derivation, independently.** For anything that computes money
   or tax, recompute the expected figure from first principles in a second
   implementation (a throwaway Python/Go oracle) and compare. Do NOT accept a
   fixture that was produced by the code under test. Last session a corpus oracle
   had been edited to agree with the mapper it was supposed to check, making the
   metric tautological.
2b. **Float check.** Grep the diff for `float64`, `parseFloat`, `Number(`. Money
   and quantity are `decimal.Decimal` on the server and scaled integers on the
   client. A briefed float-drift fixture last session did NOT bite because the
   chosen numbers happened to agree — pick fixtures that actually diverge and
   assert that at least one does.
3. **Edge cases, named explicitly.** Zero, negative, exactly-on-a-boundary,
   period boundaries (inclusive on BOTH ends — a bound flip survived the whole
   reporting package until a test placed a voucher ON the bound), empty sets,
   a single row, rounding at the paisa, back-dated entries, and ALTER/DELETE of
   anything already reported.
4. **Tenant isolation.** Every new table: `ENABLE` + `FORCE ROW LEVEL SECURITY`,
   a policy with USING **and** WITH CHECK, and composite `(company_id, id)` FKs
   so a cross-tenant reference is refused by the FK rather than resting on RLS.
5. **Authorization.** Every new route needs its `routeAction` mapping (unmapped
   paths default-DENY, so a handler without one is a 403 even for an Owner) and
   a deliberate decision about which levels may reach it. **Statutory outputs and
   payroll are more sensitive than the cost book** — salaries and PAN/Aadhaar are
   not "reports an operator may see by inheritance". Note that the seeded
   `Data Entry` level carries a COARSE `allow display report` from migration 0011
   that matches EVERY `report:NAME`, so each sensitive report needs an explicit
   `disallow` at specificity 2, as 0026/0028 do.
6. **Migration safety.** Re-runnable (a by-hand `psql -f` has no transaction
   envelope even though `cored migrate` applies each file atomically), set-based
   backfills — **never a per-company loop**; 0028's header explains why — and
   indexed for whatever the hot path probes.

**Verdicts must be CONFIRMED / REFUTED / PARTIAL with the command and observed
output.** "The tests pass" is not evidence unless you also showed they can fail.

### 4.4 Committing

One verified-green commit per increment, directly to `master`. Re-run every gate
YOURSELF as integrator; never trust an agent's "green". State in the message
which gates you ran, every deferral, and any correction to the previous record.
End with:
`Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`

---

## 5. THE WORK

### TRACK 0 — Test-data hygiene (do this FIRST, alone, ~1 wave)

**The problem, with evidence.** The dev database holds **43,419 companies** and
~262,000 vouchers, essentially all throwaway. Nothing cleans up. This has now
caused three failures that looked like code bugs:

1. `e2e:ui` went flaky because the shared `browser-dev-owner` account had
   accumulated **120 company memberships**, and the F3 picker renders all of them
   past a fixed sleep (fixed in `aea1936`, but the cause remains).
2. A migration backfill ran **1h57m** partly because `acl_rule` had no index and
   the table had grown with the fleet (index added in `eacf62e`).
3. A `go test ./internal/ledger` run aborted mid-seed with
   `No space left on device (SQLSTATE 53100)`.

`account_group` alone is **592 MB**, because every test company gets a full
chart-of-accounts tree.

**Do:**
- Add cleanup to the integration harness so a test company is removed at the end
  of the test that made it (`t.Cleanup`). Decide deliberately what to do about
  tests that assert on tombstones and history — those may need to keep their data
  and say so.
- Provide a `make reset-dev-db` (drop → `cored migrate` → seed) and document it.
- **The one-off wipe of the existing 43k companies is DESTRUCTIVE to the owner's
  dev database. ASK BEFORE DOING IT.** The owner has said they want to be asked.

**Acceptance:** after a full `go test ./...`, the company count grows by less than
some small bound (state the number); `make reset-dev-db` produces a database on
which the whole gate matrix passes.

---

### TRACK 1 — Closing stock on the Balance Sheet and P&L

**This is the largest accounting hole in the product right now.**
`internal/reporting` has NO closing-stock term: the Balance Sheet omits the
asset entirely and gross profit ignores the movement in stock. A company using
inventory today gets a Balance Sheet that is wrong in a way its accountant will
notice immediately.

The machinery exists — `stock_summary.go` already values a position at a date by
replay, per godown and per batch.

**The accounting, state it in the code:**
- Balance Sheet: Closing Stock is a **current asset** at the same `as_on` date the
  rest of the position uses.
- P&L: `Gross Profit = Sales − (Opening Stock + Purchases − Closing Stock)`.
  Opening stock at the period start, closing at the period end — so a P&L over
  [d0,d1] uses the position at d0−1 and at d1.
- **Both must move together.** Adding the asset without the P&L term, or vice
  versa, unbalances the Balance Sheet — the net-profit line ties them.

**Edge cases the review pass must see:** a date before the books began (no
opening stock leaks — the accounting reports already had this bug once); an
ALTERed or DELETEd stock voucher restating a period already reported; expired
batches (they are FLAGGED not excluded, precisely so this fold-in cannot make
physical stock vanish); a company with no inventory at all (the term must be
absent, not a zero row that changes the tree); and P&L **additivity** — two
disjoint halves must still sum to the whole once stock is in.

**Gate it with `verify:demo`,** which already asserts the Balance Sheet balances
at every month-end and that the P&L is additive. Those two assertions are what
will catch a wrong fold-in.

---

### TRACK 2 — GSTR-2A/2B + IMS reconciliation

**Why it matters:** `GET /gstr3b` currently reports ITC as **ALL-ELIGIBLE** — no
eligibility test, no rule 42/43 apportionment, no reversals, no s.17(5) block, no
matching against what suppliers actually filed. That is the single biggest
correctness gap in the compliance surface, and this track is what closes it.

**Scope:**
- Ingest a GSTR-2A/2B statement (the GSTN JSON shape) into a reconcilable form.
- Match supplier invoices against posted purchase vouchers. Matching keys:
  supplier GSTIN + invoice number + date + taxable value, with tolerance rules —
  and **invoice numbers in the wild are not clean**, so decide and document how
  near-matches are surfaced rather than silently paired.
- Classify each row: matched, mismatched (and in which field), missing in books,
  missing in 2B.
- **IMS** (Invoice Management System): accept / reject / pending, with the
  **deemed-accept on the 14th** timer. That date arithmetic is statutory — get the
  timezone and the boundary right, and test the boundary explicitly.
- Feed the result into GSTR-3B's ITC tables rather than leaving them all-eligible.

**Judgement calls to make deliberately, not by default:** what happens to ITC on
an invoice the supplier has not filed; how amendments and credit notes are paired
with the original; and whether reconciliation writes anything to the books at all
(it probably should NOT — it is a report over two sources, and a reconciliation
that silently posts is how books stop matching returns).

**Gate it with a golden corpus** in `corpora/`, following the existing families,
with the expected classification for every fixture re-derived independently.

---

### TRACK 3 — Payroll: Form 24Q, PF ECR, ESI

**The most safety-critical track. People get paid from this.**

New package `internal/payroll`. Follow `internal/compliance`'s shape: a pure
engine with a golden corpus, no DB imports in the calculation core.

**Scope:** employee masters; salary structure (earnings/deductions); a pay run
that posts to the ledger; and three statutory outputs — **Form 24Q** (TDS on
salary, quarterly, FVU-validatable `.txt`), **PF ECR**, and **ESI**.

**Where the errors will be, and what the review pass must check:**
- **Statutory rates and ceilings are effective-dated and they change.** PF at 12%
  on a ₹15,000 wage ceiling, ESI at its own rates on its own ceiling, and the
  income-tax slabs — every one needs an effective-from date and a corpus fixture
  citing its source. **Re-verify every rate against current law before release**;
  the TDS/TCS work already carries this warning and it applies double here.
- **Rounding is not uniform.** Income tax rounds to the nearest rupee; PF and ESI
  have their own rules. Do not apply one rounding everywhere.
- **Thresholds are periodic**, and the existing TDS engine already approximates
  annual thresholds per-transaction (disclosed, error direction: under-deduction).
  Payroll's are genuinely annual — do not inherit that approximation silently.
- **Mid-year joiners and leavers, arrears, and revisions** are where payroll
  engines are wrong. Test them explicitly.
- **PII.** Employee PAN, Aadhaar and bank details are the most sensitive data in
  the product. The redaction boundary in `insightd` exists for exactly this class
  of data — decide what applies here, and default to NOT exposing salary data to
  the `Data Entry` level (see §4.3 item 5 on the coarse `allow display report`).

**Gate it against the official validators' expectations** via a golden corpus, as
the TDS/TCS work did. Note the FVU itself cannot run in this environment — say so
rather than claiming validation you did not perform.

---

## 6. STANDING BACKLOG (carried forward, still true)

Ranked, from `HANDOFF.md` §6:

1. **Price-provenance is a one-way door — decide it early.** "Why did this invoice
   go out at the wrong price?" is unanswerable. Needs `price_list_id` +
   `rate_source` on `stock_movement` **and a field on the hashed payload**. The
   cost only rises with the number of hashed vouchers in the field.
2. **Inventory in the Tally-XML importer.** `internal/migration` mentions neither
   stock nor inventory, so a migrated company reconciles to the rupee **with zero
   stock** — and this compounds with Track 1 (a Tally BS showing Closing Stock
   will not tie to ours).
3. **FIFO/LIFO** behind the `Valuer` seam — a new switch arm plus fixtures plus
   widening 0025's `CHECK (valuation_method IN ('WEIGHTED_AVG'))`.
4. **A quantity-only stock report `Data Entry` may see.** All five stock reports
   are denied to that level because quantity and value share one report, so an
   operator cannot answer "do we have 10 to sell?".
5. **A stock entity in the NL-query whitelist** (`internal/query/dsl.go` still has
   four). Quantity-only is the honest first cut.
6. **Godown-create route + `CoredClient.createGodown`**; the item-invoice ALTER
   over HTTP is not a pure GET→PUT echo (it 422s loudly); `GET /stock-items` does
   not serve `hsn`/`gst_rate_pct` for the grid's tax preview.
7. Non-inventory: after `Alt+C` resolves an OCR draft's party the tax split is
   **not re-derived**; insightd's OCR runner is in-process `BackgroundTasks` and a
   crash leaves a draft `processing` forever; invite delivery has no retry backoff
   and `LogSender` only logs, so `notification_status = 'sent'` means "written to
   stdout"; **no first-password bootstrap for an invited user and no password
   reset** — the biggest remaining hole in the SaaS story.

**Deliberately absent, not oversights:** order processing and BoM (an order is a
COMMITMENT, not a movement — one that moved stock would let a quotation deplete
the warehouse), stock groups, compound units.

**Blocked in this environment:** Tauri desktop shell (no Rust toolchain); e-way
bill and full GSTR filing with ARN (needs GSP/NIC access); real-OCR accuracy on
genuine bills (every image ever tested was synthetic).

---

## 7. ENVIRONMENT

- **OrbStack can hang**, taking the database with it — `docker ps` stops
  responding and `:5433` refuses connections. Restarting OrbStack fixes it but
  **drops the owner's other containers, so ASK.** After a restart,
  **`docker start tally_pg`** — it does not come back on its own.
- **Disk was cleared on 2026-07-27**: 10.8 GB of unused Docker images removed,
  volume 93% → 57%. It is already back to **70%** after one session of gate runs —
  see Track 0.
- **`psql` can time out while the DB is fine**, and vice versa. Probe with a real
  `select 1`.
- **Restart insightd after changing its code** — a stale :8088 makes `e2e:ocr`
  green against code you did not write.
- **The `tally_app` role is RLS-restricted**, so ad-hoc queries as `tally_app`
  return 0 rows and look like missing data. Use the superuser connection to
  inspect.
- **statutoryd's service token dies when cored restarts** (fresh ephemeral signing
  key) — re-mint it, or the statutory e2e fails with a 401 that looks like a bug.
- **The invite POST requires a `webauthn` amr**; an invite is only claimed when
  the invitee calls `GET /v1/me`.
- Keys are delivered on the **document**, and e2e drives them with
  **`page.keyboard`, never `locator.press`** — two shipped bugs hid behind that.
