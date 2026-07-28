# NEXT SESSION — inventory depth landed; verify it, then continue

> ## ⚠️ READ THIS FIRST
>
> The 2026-07-27 session added **inventory** — the largest feature since the OCR
> pipeline. Four commits (`6e42a5a`, `324b45b`, `eacf62e`, `bc84519`) plus the
> wave-4 slices in the commit that carries this file. Migrations **0025–0031**.
>
> **Your first job is not to build.** It is §2 — confirm the baseline — and §2.4,
> the structural spot-checks that do not depend on any test being honest. Then
> pick up §4.
>
> Two things this session had to correct in its own record are in §1.2. **A doc
> that hides its own errata teaches nothing**, and one of the two ("migrations are
> not atomic") would have made the next person design around a hazard that does
> not exist.

---

## 1. Where things stand

Branch `master`. `HANDOFF.md` remains the authoritative project runbook —
architecture, ADRs, the honest-gaps list (**§6, which now opens with a long
INVENTORY block**), the worklist (§10). This file is the verify-then-continue
wrapper for one handover.

### 1.1 What inventory actually is

- **Stock value is NEVER stored.** Quantity is posted; value is **derived at
  report time by replaying the movement ledger** through a pure valuer
  (`internal/inventory`). Valuation is order-dependent — a back-dated inward
  re-values every later outward — so a materialised value is wrong the moment a
  voucher is back-dated or altered. Deriving means ALTER/DELETE correctness comes
  free from the existing current-version join, with zero reversal code.
- **Everything rests on ONE non-test `INSERT INTO stock_movement`**
  (`backend/internal/ledger/stock.go`, inside `ledger.post()`'s transaction). If
  a future change adds a second write path, every guard in the feature becomes
  advisory. Check that first (§2.4).
- An **Item Invoice** derives its GST lines and party leg from its stock lines
  (0027 ties `SUM(movement.amount) = SUM(gst_line.taxable_value)` by a deferred
  trigger). **Delivery/Receipt Note, Stock Journal, Physical Stock** move goods
  and post zero accounting lines; whether a class is an accounting class is a
  **compile-time closed set in Go**, never a DB flag. **Price lists** are
  deterministic by an exclusion constraint, and now price a line that omits its
  rate — inside the posting transaction, before the taxable value is computed.

### 1.2 Two corrections to the committed record

1. **MIGRATIONS *ARE* ATOMIC.** `eacf62e`'s message and 0028's header claimed
   `cored migrate` does not wrap a migration in a transaction. It does:
   `backend/migrations/migrations.go` **`applyOne()` (line 103)** begins a tx,
   execs the body, inserts the `schema_migrations` row, commits. The unstamped
   partial rows that prompted the claim came from applying a file **by hand with
   `psql -f`** during development, which has no such envelope. Keep writing
   migrations re-runnable (the by-hand path is real); do not design around a
   partial `cored migrate`.
2. **`acl_rule` genuinely had no index beyond its primary key** — that part
   stands, and is now **measured** rather than asserted.
   `internal/authz/store.go`'s `Rules()` probes it by `security_level_id` on
   **every authenticated request**. `EXPLAIN (ANALYZE, BUFFERS)` on that probe:
   **3 buffer hits with `acl_rule_level`; a parallel Gather over two workers
   without it.** What was wrong in `eacf62e` was the supporting arithmetic — the
   "13% in 1h57m" figure divided 5,106 (the number of Data Entry *levels*) by
   38,748 companies, most of which have no Data Entry level. The backfill had
   completed. The index and 0028's set-based backfill are both still right.

---

## 2. CONFIRM THE BASELINE

### 2.1 Prerequisites

Postgres `tally_pg` on **:5433** · cored **:8087** · insightd **:8088** ·
statutoryd **:8091** · vite **:5173**.

```bash
# Rebuild cored from the CURRENT tree — a stale binary silently 404s new routes.
cd backend && go build -o /tmp/cored ./cmd/cored && go run ./cmd/cored migrate
AUTH_DEV_TOKEN_ENABLED=true PORT=8087 \
  APP_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" /tmp/cored serve &

# insightd MUST carry the DSN or verify:demo drops a check (see §5).
cd insightd && . .venv/bin/activate && \
  INSIGHTD_PII_BOUNDARY_STRICT=1 \
  INSIGHTD_DATABASE_URL="postgres://tally_app:tally_app@localhost:5433/tally" \
  python -m uvicorn insightd.api.app:app --port 8088 &
```

### 2.2 The hermetic matrix — expected numbers, and where each came from

| Gate | Expected | Provenance |
|---|---|---|
| `cd backend && go build ./... && go vet ./...` | exit 0 | build re-run 2026-07-27, clean |
| `go test ./... -count=1` | **25 packages**, exit 0 | `go list ./...` = 25 (measured); exit-0 from **`bc84519`'s gate block** — the doc session was not permitted to run `./internal/server` unscoped |
| `cd frontend && pnpm exec tsc --noEmit` | clean — and it **does** cover `frontend/scripts/` | `bc84519`; the `include` list was re-read 2026-07-27 and is now `["src","scripts","vite.config.ts","vitest.config.ts"]`, so `HANDOFF.md` §10.3's "scripts is NOT covered" note is **stale** |
| `cd frontend && pnpm exec vitest run` | **370 tests / 33 files** | **measured 2026-07-27** with wave 4 in the tree (`bc84519` committed 348/32) |
| `cd frontend && pnpm run build` | ok | `bc84519` |
| `cd insightd && python -m pytest` | **106 passed** | **measured 2026-07-27** |
| `python -m insightd.benchmark.validate` | 28 fixtures valid | `bc84519` |
| `python -m insightd.benchmark.report` | ALL FLOORS PASSED | `bc84519` |
| `python3 corpora/validate.py` | `tax=14 tds=13 tcs=5 keystroke=32 isolation=13 migration=6 forecast=4 inventory=6` | **measured 2026-07-27** (`keystroke` 28 → 32: the four `Alt+F7..F10` fixtures are wave 4) |

Migrations run to **0031** (`ls backend/migrations`, measured).

### 2.3 The live sweep

**These three are the ones most likely to have moved with wave 4** (new voucher
screens, new keymap bindings, new client methods) and are exactly the ones the
doc session was not permitted to run. Numbers below are from `bc84519`'s gate
block — **treat them as the previous reading, not as a guarantee.**

```bash
cd frontend
CORED_BASE_URL=http://localhost:8087 pnpm verify:demo <companyA>          # 24/24
CORED_BASE_URL=http://localhost:8087 pnpm verify:tenancy <A> <B>          # 113/113
CORED_BASE_URL=http://localhost:8087 pnpm e2e:ui                          # 38/38 — RUN TWICE
CORED_BASE_URL=http://localhost:8087 pnpm e2e:edit
CORED_BASE_URL=http://localhost:8087 pnpm e2e:numbering
CORED_BASE_URL=http://localhost:8087 pnpm e2e:state-code
CORED_BASE_URL=http://localhost:8087 INSIGHTD_BASE_URL=http://localhost:8088 pnpm e2e:ocr
# statutoryd on its narrow role, including the durable-restart phase:
cd ../backend && go build -o /tmp/statutoryd ./cmd/statutoryd
SVC=$(curl -s -XPOST localhost:8087/auth/token -H 'Content-Type: application/json' \
      -d '{"subject":"svc:statutoryd"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
CORED_BASE_URL=http://localhost:8087 CORED_SERVICE_TOKEN=$SVC PORT=8091 \
  STATUTORYD_DSN="postgres://tally_statutory:tally_statutory@localhost:5433/tally?sslmode=disable" \
  /tmp/statutoryd &
CORED_BASE_URL=http://localhost:8087 STATUTORYD_BASE_URL=http://localhost:8091 \
  STATUTORYD_BIN=/tmp/statutoryd \
  STATUTORYD_DSN="postgres://tally_statutory:tally_statutory@localhost:5433/tally?sslmode=disable" \
  python3 backend/scripts/e2e-statutory-freeze.py
```

Seed first: `pnpm seed:demo -- --insightd`, then `pnpm seed:tenancy <A>`.
Browser sign-in: `browser-dev-owner` / `tally-demo-2026`.

> **RUN `e2e:ui` TWICE AND BELIEVE ONLY THE SECOND NUMBER**, and **run it with
> NOTHING ELSE TOUCHING THE DB.** This cost real time on 2026-07-27: run 2 came
> back 33/38 and looked like a dirty-database regression. It was not — a
> verification agent was editing Data Entry ACL rules on the live DB at the same
> moment. Re-run alone: 38/38, twice. **A live gate run concurrently with
> anything else is not evidence — of a pass or of a failure.**

### 2.4 Structural spot-checks for the inventory claims

These do not depend on any test being honest. The previous integrator ran each
of them by hand; re-run them before building on the feature.

```bash
# 1. EXACTLY ONE non-test INSERT INTO stock_movement (the whole design rests on it):
grep -rn "INSERT INTO stock_movement" backend --include='*.go' --include='*.sql' | grep -v _test.go
# expect exactly one line: backend/internal/ledger/stock.go:462 (checked 2026-07-27).
# The only other writers in the whole tree are two test files.

# 2. NO stored stock value/rate column anywhere on a balance-like table:
grep -rn "closing_value\|stock_value\|valuation_rate" backend/migrations/*.sql

# 3. Both triggers present with the RIGHT SHAPE — immutability IMMEDIATE,
#    negative-stock DEFERRED — and the negative-stock body joining
#    voucher_head.current_version_id and is_deleted (without that an ALTERed
#    voucher double-counts):
grep -n "DEFERRABLE\|current_version_id\|is_deleted" backend/migrations/0025_inventory_schema.sql
# expect 2 DEFERRABLE occurrences and the closing-position query at ~line 380
# reading "AND vh.current_version_id = sm.voucher_version_id" (checked 2026-07-27).

# 4. The two closing-position spellings must agree: the Go one is
#    internal/ledger/stock.go:402 closingPosition(); the authority is 0025's
#    trigger. The test that pins them ALTERS its fixture voucher — on an
#    unaltered voucher the mutation is invisible. Do not "simplify" that fixture.

# 5. A voucher hashed BEFORE inventory must still verify its chain (the
#    CanonicalPayload one-way door): open any pre-0025 voucher's Alt+L Edit Log
#    in the browser, or GET /vouchers/{id}/history and check ok:true.
```

---

## 3. HOW TO WORK

Fan verification out to subagents and keep only their conclusions; do not read
large files into the orchestrator. One agent per claim, each returning a
structured verdict, is a handful of tool calls instead of thousands of lines.

**Rules that earned their place** (`HANDOFF.md` §3, plus this session):

- **You are the integrator. Verify the TREE, not the agent's summary.** Re-run
  every gate yourself before committing. Agents die mid-run with their files on
  disk and their report lost.
- **A mutation that does NOT bite is a FINDING, not a formality.** Three times
  this session a briefed mutation failed to bite and the reason mattered more
  than the mutation: the briefed float-drift fixture (`0.1 × 3 @ 33.33`) gives
  the same answer in exact decimal and in float, so the test would have passed
  the bug it existed to catch; the briefed "unbalanced Sales voucher" mutation is
  refused by a deferred trigger anyway, while the genuinely reachable hole (a
  Sales voucher with ZERO postings and inventory — `posting_balanced` is a
  trigger on the posting table and never fires) is worse; and a briefed
  Receipt-Note mutation aimed at a control the form deliberately does not render.
- **If a briefed mutation is aimed at the wrong site, say so and build the one
  that reaches.** Both wave-4 slices did this and both were right to.
- **If the briefed file list cannot satisfy the briefed acceptance criteria,
  report BLOCKED before proceeding** — or, if you proceed, enumerate every
  out-of-list edit explicitly. Wave 4's screen slice was briefed a test file
  without the screen or keymap it tests.
- **Give parallel agents DISJOINT file sets.** Two agents mutating one Go package
  invalidate each other's test runs.
- **Never let an agent touch shared live state** — a pre-existing company's ACL
  rules, security levels or members. Make it create its own throwaway tenant.
  This is what corrupted an `e2e:ui` run above.
- **Never run `go test ./internal/server` unscoped** while others work: it
  contains a test that disables DB triggers globally. Scope with `-run`.
- **A frontend mock is a FALSE ORACLE until it mirrors the server rule.** The
  Physical Stock acceptance assertion ("the closing position IS the count") would
  have passed against a client that sent the count as a movement, because the
  mock echoed request bodies. `mock.ts` now derives the correction the way
  `nonaccounting.go` does, and a mutation relaxing that differencing makes the
  books read 23 where a human counted 13.
- **Commit only verified-green work, directly to `master`**, stating the gates
  re-run and every deferral. End with:
  `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`

---

## 4. WHAT IS LEFT — priority-ordered

Every item below is disclosed in `HANDOFF.md` §6; the INVENTORY block there has
the full reasoning for 1–9.

**Do these next, in this order:**

1. **Wire the optional `rate` on the HTTP voucher path** (`internal/server`).
   The price-list resolver ships and is reachable only from Go: the handler must
   decode an inventory line's `rate` as nullable/optional and set
   `InventoryLine.RateUnstated` when absent. Until then **no HTTP client can ever
   produce a line that gets priced.** Smallest slice with the largest unlock.
   Mind the polarity — `RateUnstated`'s zero value means STATED, so no existing
   caller starts repricing its free samples.
2. **Fold CLOSING STOCK into the Balance Sheet and the P&L.** The biggest
   accounting hole in the feature: `internal/reporting` has no closing-stock term
   at all, so the BS omits the asset and gross profit ignores the movement in
   stock. The valuation machinery (replay to a date, per godown/batch) already
   exists. Expired batches are FLAGGED not excluded precisely so this fold-in
   cannot make physical stock vanish. Gate it with `verify:demo`.
3. **Decide the price-provenance one-way door NOW** — migration **0032**
   (`price_list_id`, `rate_source` on `stock_movement`) plus a field on
   `CanonicalStockLine`, i.e. a change to the **hashed payload** and `hash.go`.
   "Why did this invoice go out at the wrong price?" is unanswerable today. The
   cost of this door only rises with the number of hashed vouchers in the field.
4. **Inventory in the Tally-XML importer.** `internal/migration/*.go` mentions
   neither stock nor inventory, so a migrated company reconciles to the rupee
   **with zero stock** — a real hole in the migration thesis, and it compounds
   with #2 (a Tally BS showing Closing Stock will not tie to ours).
5. **Hoist the price lookup into a tx-scoped resolver in `internal/masters`** that
   both `masters.Service.ResolveRate` and `ledger/pricing.go` share.
   `TestTheLedgerPriceMatchesTheMastersResolver` currently pins the two spellings
   against each other; that is a guard, not a fix.
6. **A godown-create route + `CoredClient.createGodown`.** The mock seeds exactly
   one named godown, so the stock-journal transfer fixture moves goods out of the
   UNTRACKED position rather than between two named locations.
7. **FIFO / LIFO behind the `Valuer` seam** (`internal/inventory/valuation.go:84`)
   — a new switch arm, fixtures, and widening 0025's
   `CHECK (valuation_method IN ('WEIGHTED_AVG'))`. `ValuerFor`'s default arm
   already fails loudly, which is what makes the widening safe.
8. **A stock entity in the NL-query whitelist.** `internal/query/dsl.go` still
   exposes four entities. A **quantity-only** entity is the honest first cut —
   value is derived by replay and the compiler's fixed FROM/JOIN skeleton cannot
   express it.
9. **Two small item-invoice edges:** the ALTER over HTTP is not a pure GET→PUT
   echo (it 422s loudly; needs an item-derived marker on `VoucherView`), and
   `GET /stock-items` does not serve `hsn`/`gst_rate_pct` so the grid cannot
   preview the tax the server is about to derive.
10. **A quantity-only stock report Data Entry may see.** Today the level is
    denied all five stock reports (0026/0028) because quantity and value share
    one report, so an operator cannot answer "do we have 10 to sell?".
    Column-level ACL is not built.
11. **Housekeeping, before it bites:** the Postgres host volume was at **99%**
    (4.7 GiB free) and one `go test ./internal/ledger/` run aborted with
    `SQLSTATE 53100` mid-seed; the rerun passed. Every integration test there
    creates a company and none clean up. Reclaim the volume, or expect spurious
    failures that look like regressions.

**Larger, decide before starting:** stock groups/categories, compound and
alternate units, BoM + manufacturing journals, and order processing. Orders are
absent **deliberately** — an order is a commitment, not a movement, and one that
moved stock would let a quotation deplete the warehouse; it needs a
committed-quantity concept the movement ledger does not have.

### Carried forward, still true (non-inventory)

- After `Alt+C` resolves an OCR draft's party, the tax split is **not re-derived**
  from the now-known supplier state.
- insightd's background OCR runner is in-process `BackgroundTasks`: a draft left
  `processing` by a crash **stays there forever** — no reaper, timeout or requeue.
- **`InferredInvoice` has nowhere to put** the supplier's state or the invoice's
  own CGST/SGST-vs-IGST label, both printed in plain text on real bills and both
  surviving redaction. That, not the PII boundary, blocks the remaining honest
  fallback for the supplier-state derivation.
- Invite delivery has **no retry backoff**, and the default Sender only LOGS —
  `notification_status = 'sent'` means "written to cored's stdout".
- **No first-password bootstrap** for an invited user and **no password reset** —
  the biggest remaining hole in the SaaS story.
- **Grant changes live only in one-shot migrations.** `migrations.Apply` skips an
  applied version, so widening 0022 on an existing database is a no-op, invisible
  to every DB-backed test until someone provisions a fresh DB.
- **Data Entry holds a bare `allow display report`** (from 0011) which matches
  every `report:NAME`. Each valued stock report needed its own explicit
  `disallow` (0026, 0028) for exactly this reason. **The next report added will
  hit it again.** Product decision, not a bug.
- `status='sending' AND claimed_at IS NULL` is unclaimable forever in the outbox
  (`NULL < now()-interval` is NULL); a `CHECK` would make the lease invariant
  structural.
- Tally's Trial Balance shows opening/transactions/closing columns for a period;
  this shows the closing position as at a date.
- The PaddleOCR fake is pinned to **prose** (transcribed from the spike doc), not
  to bytes. Capture a serialised real 3.x `predict()` response when paddle is
  next available. `_is_inter` in `insightd/benchmark/validate.py` is dead code.

### Blocked in this environment (do not attempt)

- **Tauri 2 desktop shell** — `cargo`/`rustc` absent (confirmed).
- **e-way bill / full GSTR filing with ARN** — needs real GSP/NIC access.
- **Real-OCR accuracy on genuine photographed bills** — every image ever tested
  against this pipeline was synthetic.

---

## 5. ENVIRONMENT GOTCHAS

- **The Postgres host volume is nearly full.** `SQLSTATE 53100`
  (`No space left on device`) during company seeding is an environment failure,
  not a code regression — rerun before you believe it, then reclaim space.
- **OrbStack's Docker daemon can hang**, taking `tally_pg` with it. Fixed by
  quitting and relaunching OrbStack, then `docker start tally_pg`. **Ask the user
  before doing this** — it drops their other containers.
- **`psql` / `pg_isready` can time out while the DB is fine.** Probe with a real
  `select 1`.
- **insightd without `INSIGHTD_DATABASE_URL`** silently falls back to an
  in-memory draft store and `verify:demo` loses a check.
- **Restart insightd after changing its code** — a stale :8088 makes `e2e:ocr`
  green against code you did not write.
- **`frontend/scripts/` IS now covered by `pnpm exec tsc --noEmit`** — tsconfig's
  `include` is `["src","scripts","vite.config.ts","vitest.config.ts"]` (re-read
  2026-07-27). Keep the `.mts` scripts typechecking clean; older notes saying
  they are uncovered are stale.
- **The `tally_app` role is RLS-restricted**, so ad-hoc `psql` queries as
  `tally_app` return 0 rows and look like missing data. Use the superuser
  connection for inspection.
- **The invite POST requires a `webauthn` amr** — a plain dev token gets
  `403 STEPUP_REQUIRED`. An invite is only claimed when the invitee calls
  `GET /v1/me`.
- The DB holds tens of thousands of throwaway test companies. Harmless, but see
  the volume note above.
- Key delivery is on the **document**, and e2e keys go through **`page.keyboard`,
  never `locator.press`** — two shipped bugs hid behind that.
