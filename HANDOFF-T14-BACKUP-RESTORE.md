# HANDOFF — T14: BACKUP / RESTORE / SPLIT COMPANY

**Written:** 2026-08-25 · **Branch:** `session/t32-and-close-7-3` · **Tree:** clean at `6778fc1`
**Read time:** ~15 minutes. Everything below was MEASURED today; every claim has the command beside it.

> ## ⛔⛔ THE FOUR THINGS THAT CHANGE WHAT YOU BUILD. READ THE BOX, THEN §2.
>
> | # | Measured fact | What it decides |
> |---|---|---|
> | 1 | **`internal/archtest` FORBIDS production code importing `internal/platform/testdb`** (`boundaries_test.go:625`) | Every handoff says "reuse the testdb delete-plan census". **You cannot import it.** The census must be EXTRACTED into a production package first — that is slice 0 and it is not optional. |
> | 2 | **There is no `DELETE /companies` route anywhere**, and the `*_no_mutate` triggers block DELETE on the immutable tables **for every role** | "Restore over an existing company" is not a hard feature, it is an **impossible** one on today's schema. Restore CREATES A NEW COMPANY. Design for that and say so on the screen. |
> | 3 | **The hash chain does NOT include `company_id`** but DOES include `voucher_id`, `voucher_type_id`, every `ledger_id` and every `stock_item_id`/`godown_id`/`batch_id` (`internal/ledger/hash.go:192` `CanonicalPayload`) | A restore that PRESERVES primary keys preserves the audit chain. A restore that re-mints ids **silently invalidates every hash in the book**. Preserve the keys. |
> | 4 | Ten triggers fire on INSERT; **the six that could plausibly fail on a bulk insert are all DEFERRABLE INITIALLY DEFERRED** (the other four are per-row field checks a faithful archive satisfies by construction — full table in §2.3) | A restore in **ONE transaction** may insert in any order and is checked once at COMMIT. Insert order is a debuggability concern, not a correctness one — and one transaction is also what makes a half-restored company impossible. |
>
> ⚠️ **AND ONE PRODUCT QUESTION THAT IS NOT MINE TO DECIDE — §3.** Tally's *Split Company Data*
> exists because Tally loads a whole company into RAM (`RESEARCH.md:987`). This product does not.
> `RESEARCH.md:1010` says so in our own words: keeping unlimited multi-year history hot "directly
> attacks Tally's #1 pain … the need to 'Split Company Data' by year." **Building split for
> performance parity would be building a workaround for a limitation we do not have.** There is still
> a real reason to build it (§3) — but decide that deliberately, do not inherit it from a feature list.

---

## 0. RUN-FIRST — the stack, and the numbers to beat

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
lsof -nP -iTCP:8087 -sTCP:LISTEN -t   # and 5173, 5433
```

⛔ **SYMBOL-CHECK THE RUNNING `cored` BEFORE BELIEVING ANY DRIVE.** `make run-cored` is `go run`, so a
stale process is indistinguishable from a fix that does not work:

```bash
strings "$(ps -o command= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)" | awk '{print $1}')" | grep -c backup
```

⛔ **VITE MUST BE ON `:5173`.** WebAuthn origin matching is exact; a vite that took `:5174` signs in
fine and then fails every write with `PASSKEY STEP-UP FAILED`.

**The gate baseline, re-measured 2026-08-25 at `6778fc1` — these are the numbers to beat:**

| Gate | Command | Value now |
|---|---|---|
| backend build + vet | `cd backend && go build ./... && go vet ./...` | clean |
| backend tests | `cd backend && go test ./... -timeout 60m` | **exit 0** |
| typecheck | `cd frontend && pnpm exec tsc --noEmit` | clean |
| frontend tests | `cd frontend && pnpm vitest run` | **1565 pass / 93 files** |
| live demo | `cd frontend && pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT; exits 2 with a usage line without one |
| corpora | `python3 corpora/validate.py` | OK (inventory=11, migration=6) |
| everything | `make verify` | exit 0 |

⚠️ **`make verify` DOES NOT RUN `tsc`.** They are different gates and each has caught a real error the
other missed.

---

## 1. ⛔ SLICE 0 — EXTRACT THE CENSUS. NOTHING ELSE CAN START UNTIL THIS LANDS.

### 1.1 What exists, and why you cannot use it as-is

`backend/internal/platform/testdb/plan.go` (440 lines) is the best code in this repo for T14's hardest
problem. It derives the tenant table set **from the live schema at runtime** and classifies every
table in `public` as exactly one of three things:

| Class | How it is found | Registry |
|---|---|---|
| **scoped** | has a literal `company_id` column, enumerated by catalog query | `queryScopedTables`, `plan.go:227` |
| **specialTenancy** | tenancy encoded some other way; a WHERE PREDICATE is registered | `specialTenancy`, `plan.go:113` |
| **notTenantScoped** | genuinely not a company's data, with the reason written down | `notTenantScoped`, `plan.go:130` |

`TestEveryTableIsClassifiedForTeardown` makes the classification's **completeness** a test: a new
table that fits none of the three fails **by name**, on the day its migration lands.

⛔ **AND `internal/archtest/boundaries_test.go:625` — `TestNoProductionCodeImportsTestdb` — walks every
non-test `.go` file in the module and fails if any of them imports `testdb`.** Its reasoning is
correct and must not be relaxed: testdb carries a hardcoded **superuser DSN** and a routine that
disarms every immutability trigger and mass-deletes a tenant. That is a fine test harness and an
unacceptable thing to link into `cored`.

### 1.2 The extraction

Create **`backend/internal/platform/tenant`** holding ONLY the census — no DSN, no pool, no delete:

- `queryScopedTables`, `queryFKEdges`, `queryBaseTables`, `reverseTopo`, `quoteLiteral`;
- the three registries (`specialTenancy`, `notTenantScoped`, and the `scoped` / `specialStmt` types);
- a public `Census(ctx, conn) (Plan, error)` taking the existing narrow `connLike` interface, so it
  works against a pooled conn OR a `pgx.Tx` and imports nothing from `internal/`.

Then:

- `testdb` imports `tenant` and keeps ONLY the superuser pieces (`adminPool`, `Drop`, the
  `session_replication_role` teardown, the residue check).
- **Move `TestEveryTableIsClassifiedForTeardown` with the registries.** A completeness test that lives
  apart from the thing it makes complete is one refactor away from being vacuous.
- The backup service imports `tenant`.

⛔ **THE POINT OF THE EXTRACTION IS THAT THERE IS EXACTLY ONE ENUMERATION.** If the backup keeps its
own list — even a "temporary" one — then the day a migration adds a table, teardown cleans it and
backup silently omits it, and the omission is discovered by somebody restoring. **A second list is the
defect.** Say so in the package comment.

### 1.3 The two archtest entries (§7.1 of `HANDOFF.md`, and only the second fails in the full suite)

`backend/internal/archtest/boundaries_test.go` — a new `internal/` package needs **both**:

1. its own allowed-import set: `"internal/platform/tenant": {}` (it imports nothing from `internal/`);
2. an entry in the allowed set of every package that imports it — `internal/platform/testdb` gains
   `"internal/platform/tenant": true`, and so does whichever package owns the backup service.

⚠️ Only the first passes when you run the package alone. The second fails **only in the full suite**.

### 1.4 The proof for slice 0

- `go test ./internal/platform/testdb/ ./internal/platform/tenant/ ./internal/archtest/` green.
- ⛔ **MUTATION:** delete one entry from `notTenantScoped` and confirm the classification test names
  that exact table. Then add a scratch table with a `company_id` to a throwaway database and confirm
  it is picked up with no code change. **Both must be executed, not reasoned about.**

---

## 2. THE GROUND TRUTH — measured 2026-08-25, with the commands

Re-run every one of these before you trust them (§9 rule: **VERIFY, DO NOT INHERIT** — this file
included). `PG` below is `PGPASSWORD=postgres psql -h 127.0.0.1 -p 5433 -U postgres -d tally -tAc`.

### 2.1 The census, today

| Fact | Value | Command |
|---|---|---|
| base tables in `public` | **68** | `$PG "SELECT count(*) FROM pg_tables WHERE schemaname='public'"` |
| with a literal `company_id` | **61** | the `information_schema.columns` query at `plan.go:230` |
| specialTenancy | **1** (`statutory_step`) | `plan.go:113` |
| deliberately NOT tenant-scoped | **6** (`app_user`, `auth_login_attempt`, `schema_migrations`, `user_credential`, `webauthn_challenge`, `webauthn_credential`) | `plan.go:130` |

61 + 1 + 6 = 68. **The classification is currently exact, with nothing unaccounted for.** A backup
must cover **62** tables (61 scoped + `statutory_step`).

⚠️ **`insightd_draft.company_id` is TEXT, not UUID** — it is written by the Python insightd service.
`plan.go:224`'s comment is emphatic that filtering the census on `data_type='uuid'` is a silent
data-retention bug. The same applies to backup: **filter on type and you silently omit insightd
drafts.** Decide explicitly whether a backup includes another service's drafts (§4 decision D4).

### 2.2 RLS, and why restore needs no superuser

```
$PG "SELECT policyname, count(*) FROM pg_policies WHERE schemaname='public' GROUP BY 1"
 -> tenant_isolation | 65
```

**Every policy in the database is named `tenant_isolation`**, is `FOR ALL`, and carries
`WITH CHECK (company_id = app_current_company_id())`. So the ordinary `tally_app` role, inside a
transaction that has done `set_config('app.current_company_id', <new company>, true)` — which is
exactly what `db.RunInTx` does as its first statement (`internal/platform/db/db.go:153`) — **may INSERT
rows for that company.**

⛔ **AND THE IMMUTABILITY TRIGGERS DO NOT BLOCK IT.** Every `*_no_mutate` trigger is
`BEFORE UPDATE OR DELETE` (`migrations/0002_core_schema.sql:123`, `:150`, and six more). **INSERT is
allowed.** A restore that only ever INSERTs is a perfectly ordinary application write.

⚠️ **The converse is the constraint in box item 2:** those same triggers make DELETE impossible for
every role, which is why `testdb` needs a superuser and `session_replication_role = replica` just to
drop a test company. **Do not build "restore replaces the company".** There is no delete path, there
is no delete route, and adding one puts a tenant-wipe into the product.

### 2.3 The deferred triggers — why restore is ONE transaction

```
$PG "SELECT c.relname||'.'||t.tgname||' deferrable='||t.tgdeferrable
     FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace
     WHERE NOT t.tgisinternal AND n.nspname='public' AND (t.tgtype::int & 4)>0"
```

| Trigger | Deferrable |
|---|---|
| `posting.posting_balanced` | ✅ initially deferred |
| `stock_movement.stock_movement_not_negative` | ✅ initially deferred |
| `stock_movement.stock_movement_ties_to_gst` | ✅ initially deferred |
| `voucher_gst_line.voucher_gst_line_ties_to_stock` | ✅ initially deferred |
| `posting_cost_allocation.cost_allocation_sums_to_posting` | ✅ initially deferred |
| `order_fulfilment.order_fulfilment_within_commitment` | ✅ initially deferred |
| `bank_reconciliation`, `bank_statement_import`, `bom_component`, `company` | ❌ per-row |

The four per-row ones are all simple field validations that a faithful archive satisfies by
construction. **The six that could plausibly fail on a bulk insert are all deferred to COMMIT**, so
restore may insert in FK-topological order for debuggability without needing chronological order for
correctness.

⛔ **THIS IS ALSO THE ARGUMENT FOR ONE TRANSACTION AND AGAINST CHUNKING.** A restore that commits in
batches can leave a company that exists, is a member's, opens on screen, and is missing its second
half — with every deferred check having passed on each batch. **A half-restored book is worse than a
failed restore**, and the only thing that makes it impossible is a single transaction.

### 2.4 Identity and keys

- **Only one sequence in the whole schema**, `auth_login_attempt.id`, and it is not tenant data. **No
  `setval` problem.** (`information_schema.columns WHERE column_default LIKE 'nextval%'`)
- **Only one FK from a tenant table to a user table:** `company_membership -> app_user`. Everything
  else is self-contained. That single edge is decision **D3** in §4.
- `company.company_id = company.id` is a CHECK (`0002_core_schema.sql:26`) — the tenant root scopes
  itself. Restore's first INSERT must honour it.
- `company.parent_company_id` **exists in the schema and is referenced by ZERO lines of Go**
  (`grep -rn parent_company_id backend/internal/` is empty). It is dead schema from `0002`. ⚠️ Do not
  assume it means anything; if split uses it, that is a NEW decision to write down, not an inherited one.

### 2.5 Scale

The demo company is **528 rows across 28 of the 61 scoped tables**. A real SME year is bigger but not
by orders of magnitude — the real Tally export this importer was fixed against was 121 vouchers with
227 stock lines. **A single-transaction JSON archive is comfortably feasible**; do not design for
streaming before you have measured a company that needs it.

Regenerate the per-table census for any company:

```bash
$PG "SELECT string_agg(format('SELECT %L t, count(*) n FROM %I WHERE company_id::text = %L',
       table_name, table_name, '<COMPANY-ID>'), ' UNION ALL ')
     FROM information_schema.columns c JOIN information_schema.tables t USING (table_schema, table_name)
     WHERE c.table_schema='public' AND c.column_name='company_id' AND t.table_type='BASE TABLE';"
```

---

## 3. WHAT T14 ACTUALLY IS — and the honest split-company question

Tally's own surface (`RESEARCH.md:993`): **`Alt+Y (Data) > Backup / Restore`**, producing one archive
named `TBK<gen>_<companycode>.001`; and **Split Company Data**, by financial year.

⚠️ **`Alt+Y` IS COMPLETELY UNBOUND in this build** — `grep -rn 'Alt+Y' frontend/src/` returns nothing.
It is free, and it is the right key.

### The split-company question, stated so it can be argued with

`RESEARCH.md:987`: Tally splits because *"RAM is the binding constraint … the standard remedy is Split
Company Data (by financial year) so only current-year data is resident."* `RESEARCH.md:1010`, our own
positioning: keeping unlimited multi-year history hot *"directly attacks Tally's #1 pain … the need to
'Split Company Data' by year."*

**So the performance reason for split does not exist here.** Three honest options:

| Option | What it means | My recommendation |
|---|---|---|
| **A. Ship backup/restore, defer split with a stated reason** | The FeaturesScreen row flips to `on` for backup/restore and says plainly that split is deliberately absent because the limit it works around is Tally's, not ours | ⭐ **Recommended.** It is truthful, it is a quarter of the work, and "we don't need that" is a better answer than a worse copy of it |
| **B. Build split as ARCHIVE-AND-CARRY-FORWARD** | A real feature with a real reason: closing a financial year into a read-only archive company and a fresh one carrying opening balances — for statutory year-end and for handing a closed year to an auditor. NOT for performance | Build it later, as its own track, if a user asks |
| **C. Build split for parity** | A migration-from-Tally user looks for the menu item | Weakest. The menu item can say why it is absent |

⛔ **Whichever you choose, the FeaturesScreen row must state it.** The row today
(`frontend/src/screens/FeaturesScreen.tsx:502`) reads `off`, *"Not built, and absent is the honest
state to be in until it can be TENANT-EXACT."* If you ship backup/restore and defer split, that row
must say **exactly which half shipped** — a row that reads `on` for "Backup / restore, split company
data" while split does not exist is the failure mode this whole screen exists to prevent. **Split the
row in two if you defer split.**

⚠️ If you do build split (option B), the machinery for the second half's opening balances already
exists and must be reused, never re-implemented: `ledger.opening_balance` / `ledger.opening_dc`,
`ledger_opening_bill` (migration 0017), and `stock_opening` — plus `masters.SetStockOpening`, which §3
of the inventory lane already drives from the Tally importer. **And both halves must independently
balance** — that is the gate, not "it ran".

---

## 4. THE DECISIONS TO MAKE BEFORE WRITING CODE

Each of these changes what you build. Write the answer down in the commit message.

| # | Question | The constraint that forces it | Recommendation |
|---|---|---|---|
| **D1** | Archive format | Must round-trip 62 tables including JSONB payloads and BYTEA hashes | **JSON Lines, one object per row, with a header record** carrying schema version, `schema_migrations` max version, source company id, row counts per table. Gzip it. NOT `pg_dump` — the app role cannot run it, and a dump is not tenant-scoped |
| **D2** | Does restore preserve primary keys? | The hash chain contains `voucher_id`/`ledger_id`/`stock_item_id` but not `company_id` (`hash.go:192`) | ⭐ **PRESERVE THEM.** Then a restored book verifies against its original chain. Re-minting ids means `VerifyChain` passes on a chain you re-derived — which attests to nothing. ⚠️ Consequence: **restoring into the SAME database as the source collides on every PK**, so restore must refuse when the source company id already exists, and say why |
| **D3** | What happens to `company_membership`? | It is the only tenant→`app_user` FK. The archive names users who may not exist in the target database | **Do not restore memberships.** Restore seeds the RESTORING user as Owner (the same thing `handleCreateCompany` already does) and REPORTS the memberships it did not recreate, by name. Silently recreating access rights from a file is a privilege-escalation vector |
| **D4** | Does the archive include `insightd_draft`? | It is tenant data with a TEXT company_id, owned by another service | **Include it and say so**, or exclude it and say so — but the choice must be in the archive header, not implicit. An archive that silently drops a plane is the census defect wearing a different hat |
| **D5** | Does the archive include `statutory_step`? | specialTenancy, keyed `irn:<companyID>:<voucherID>`, no FK, no RLS | **Include it.** It is the IRN workflow log; without it a restored company can re-attempt an IRN that was already raised. Its key embeds the company id, so restore must rewrite the key — the ONE place a value is transformed, and it needs its own test |
| **D6** | Is a backup a read the ACL can refuse? | A backup is the most sensitive read in the product: every ledger, every party, every salary | **Yes, and it must be its own resource token** (§5.2). Do not fold it into `company` or `report` |

---

## 5. THE SLICES, IN ORDER

Each slice ends green and useful. **Do not start slice 2 until slice 1 is driven in a browser.**

### Slice 0 — the census extraction (§1)
No user-visible change. Gate: full `go test ./...` green, both mutations executed.

### Slice 1 — BACKUP: one company out, as a file
- `internal/backup` package (its own archtest set: needs `internal/platform/db` and
  `internal/platform/tenant`; add it to `internal/server`'s set too).
- `GET /v1/companies/{companyID}/backup` streaming the gzipped JSONL archive.
- Header record first, then tables in census order, then a trailing record with per-table counts.
- ⛔ **The trailer is the oracle for a truncated download.** A restore that reads a short file must
  fail loudly, not restore 80% of a company.

**Proof:** an HTTP test that backs up a company and asserts the archive contains a row from **every
non-empty table** the census reports for it — derived from the census at test time, never a literal
list. ⛔ **Mutation: drop one table from the backup loop and watch that test name it.**

### Slice 2 — RESTORE: a file back in, as a NEW company
- `POST /v1/companies/restore`, mounted **authn-only** beside `POST /v1/companies`
  (`server.go:580`) — the company does not exist yet, so the `{companyID}` subtree would 403 it.
  `handleCreateCompany` is the precedent for seeding the Owner level + membership afterwards.
- ONE transaction (§2.3). Preserve PKs (D2). Refuse if the source company id already exists.
- Report what it did NOT restore (memberships, D3) rather than restoring it silently.

**Proof — and this is the one that matters:**

⛔ **THE ORACLE IS A ROUND TRIP THAT COMPARES THE CENSUS, NOT A SPOT CHECK.** Back up a company, drop
it with `testdb`, restore it, and assert **every table's row count and every row's contents match**,
using the census to enumerate. Then assert `VerifyChain` passes on the restored vouchers — which is
the whole point of D2 and the thing no row count can see.

⛔ **AND ASSERT A REPORT, NOT ONLY THE ROWS.** Take the source company's Trial Balance, Stock Summary
and Balance Sheet before backup and after restore and compare them byte for byte. Rows can all be
present and a report still differ if a `current_version_id` head pointer was restored wrongly.
⚠️ **A balanced Trial Balance proves nothing on its own** — that is the inventory lane's rule 2, and it
applies here exactly: the statements balance for any self-consistent subset of the books.

### Slice 3 — the screen
`Alt+Y` (free, §3) opening a Data screen with Backup and Restore. Full registration surface in §6.

**Proof:** a real-shell keypress test (`AppShell` rendered, panel absent as a control, `fireEvent.keyDown`,
panel appears), **mutation-proved by deleting the `SCREEN_ACTIONS` entry** — and a browser drive.

### Slice 4 — split, or the honest refusal (§3)
Whichever option §3 lands on, the FeaturesScreen row states it precisely.

---

## 6. THE REGISTRATION SURFACE — miss one and it is 403 for the Owner too

`HANDOFF.md` §7 is the authority; this is what it means for T14 specifically.

### 6.1 The route (§7.1: a new RESOURCE needs 4)

- `routeAction()` in `backend/internal/server/auth_middleware.go:1243` — add the cases.
- `authz.KnownResources` in `backend/internal/authz/vocabulary.go:95` — add the token.
- The report entries do **not** apply: a backup is not a report and must not be modelled as one.
  ⚠️ A `report:NAME` is **GRANTED to Data Entry the moment it exists** by migration 0011's coarse
  `allow display report` — modelling a whole-company export as a report would hand every operator in
  every tenant the entire book.
- Gate: `go test ./internal/server/ -run TestEveryCompanyRouteIsMappedAndInVocabulary`.

⛔ **`POST /v1/companies/restore` IS OUTSIDE THE `{companyID}` SUBTREE**, so `routeAction` never sees
it — it is authn-only, like company creation. That is correct and it is also a hole to think about:
**the only thing gating restore is "you are a signed-in user"**, exactly as for creating a company.
Say so in the handler comment.

### 6.2 The ACL migration — probably none (§6.1)

The five actions are `create`/`alter`/`delete`/`display`/`print` (`internal/authz/authz.go:38`).

**A BARE RESOURCE TOKEN NEEDS NO MIGRATION.** The seeded `Data Entry` level has
`default_access = 'none'` and every rule names a specific token, so a token never mentioned is
**denied**. `tds-challan`, `cost-centre`, `sales-order`, `purchase-order` and `bom` all shipped with
**zero** ACL rows. A new `backup` token gets the same treatment for free.

⛔ **IF you nonetheless need to restate `seed_company_auth_defaults`, read `HANDOFF.md` §6 first, and
note that §6 IS STALE:** it says the highest number is 0073 with 13 PERFORMs. **Measured today: the
highest migration on disk is `0079_stock_groups_and_compound_units.sql`, and the highest-numbered
restatement of the seeder is `0076_forecast_report_acl.sql` with 20 PERFORMs.** Derive from **0076**,
and do all three verification steps in §6.2 of that file — the second (diff the restated body against
its base with comments stripped) is the one that catches the real defect.

### 6.3 The screen (§7.2: fifteen registrations)

The two whose paths lie: `ScreenKind` is in `frontend/src/keystroke/types.ts` (**not**
`screenRegistry.tsx`), and `actionLabel()` is in `keystroke/keymaps/tallyprime.ts` (**not**
`AppShell.tsx`). The fifteenth is outside `frontend/` entirely: **`corpora/validate.py`'s
`KNOWN_SCREENS`** (line 66) — a `ScreenKind` it does not list fails `make verify`. ⚠️ That file is
CO-OWNED: **ADD only, and re-read it immediately before editing.**

⛔ **`AppShell.tsx`'s `SCREEN_ACTIONS` is the one that ships the key COMPLETELY DEAD** — no error, no
status line. And an unbound key is NOT inert: F5 opens a Payment over your form, F9 a Purchase.

⚠️ Client methods go in **three** files: `api/client.ts` (declare it there and `rest.ts` + `mock.ts`
fail to compile until both implement it — **use that**), `api/rest.ts`, `api/mock.ts`.

⚠️ **The download path already exists**: `frontend/src/export/download.ts` for sheets, and
`frontend/src/screens/usePrint.ts:44` for a Blob + `a.download`. Reuse the pattern; do not invent a
third.

---

## 7. THE TRAPS — every one of these is measured, not imagined

1. ⛔ **The census is the feature.** A hand-written table list is the defect, not a shortcut to it.
   The delete-plan census caught `stock_group` in the T10 lane, by name, on the day it landed.
2. ⛔ **Do not filter the census on `data_type='uuid'`.** It compiles, it type-checks, every test
   stays green, and `insightd_draft` silently stops being covered (`plan.go:224` says this at length
   about teardown; it is identically true of backup).
3. ⛔ **Re-minting primary keys silently invalidates every hash in the book.** Nothing fails. The
   restored company looks perfect and `VerifyChain` reports tampering — or worse, passes against a
   chain you re-derived, which attests to nothing.
4. ⛔ **A balanced Trial Balance is not evidence.** The inventory lane's rule: *an oracle that cannot
   see the defect is not an oracle.* The statements balance for any self-consistent subset of the
   books, so a restore that dropped a whole table can still foot.
5. ⚠️ **`macOS composes Option+<letter>`.** Drive `Alt+Y` with the harness's `altMac("y")`, never
   `press("Alt+y")`. Every Alt binding in this product was once found dead on macOS while every
   browser test passed. The table is in `frontend/scripts/audit-harness.mjs` (`MAC_OPTION_CHAR`).
6. ⚠️ **Drive on a THROWAWAY company, never `Demo Traders`.** Copy
   `frontend/scripts/drive-stock-groups.mjs` (27/27) or `drive-valuation.mjs` (21/21) — both create
   their own tenant. Residue in the demo tenant drops `verify:demo` below 34/34.
7. ⚠️ **`statutory_step`'s tenancy is inside its TEXT primary key** (`irn:<companyID>:<voucherID>`,
   see `internal/statutory/irn_workflow.go:48`). It is the one row shape whose VALUE must be
   rewritten on restore. Give it its own test.
8. ⚠️ **`archtest` needs TWO entries for a new package** and only the second fails in the full suite.

---

## 8. DEFINITION OF DONE FOR THIS TRACK

`HANDOFF.md` §12 in full, plus these three that are specific to T14:

1. ⛔ **A ROUND TRIP THAT DROPS THE ORIGINAL.** Back up → `testdb.Drop` → restore → the census
   comparison AND `VerifyChain` AND three reports byte-identical. A restore tested against a company
   that still exists has proved nothing about restore.
2. ⛔ **BOTH PROOFS, as always.** An EXECUTED mutation proof whose failure was **predicted, then
   observed** — drop one table from the backup loop, and separately re-mint one id class on restore —
   *and* a drive in a real browser. A probe that has never failed proves nothing.
3. ⛔ **The FeaturesScreen row says exactly which half shipped** (§3), on BROWSER evidence. If split
   is deferred, the row is SPLIT IN TWO so the gap stays visible instead of being absorbed by a green
   badge. ⚠️ **`frontend/src/screens/VoucherScreen.stockVoucher.test.tsx` currently pins
   `/^Export: e-mail a document/` as its "still not built" row** — if you flip a Data-and-operations
   row, check whether that assertion needs re-pointing, and re-point it deliberately with a comment.

---

## 9. THE RULES THAT ARE NOT NEGOTIABLE

1. **Both proofs, for every fix.** An EXECUTED mutation proof whose failure was **predicted, then
   observed**, *and* a drive in a real browser.
2. ⛔ **Run every new probe against the BROKEN build first.** A probe that has never failed proves nothing.
3. **Assert a PROPERTY, not a list.** For T14 the property is *"every table the census reports"* — never
   a list of table names in a test, which is the same defect as a list in the product.
4. ⚠️ **Vary the axis the product actually varies on.** For backup that is **table coverage and row
   shape** (JSONB, BYTEA, TEXT company ids, NULLs), not archive size.
5. **Do not assert a specific non-terminal status name** in a probe.
6. ⛔ **VERIFY, DO NOT INHERIT — including this file.** Every claim in §2 has a command beside it. Run them.

---

## 10. WHEN THIS LANDS

- Fold it into `HANDOFF.md` **§1.17** (the inventory lane took §1.16), flip `HANDOFF.md` §1.11's row 4
  to done, and **`git rm` this file** — it is built to be retired, exactly as `HANDOFF-T10-FINISH.md`
  was.
- `HANDOFF-NEXT-BUILD.md` §4 becomes spent; its headline moves to **§5 (the offline entry screen)**.
- Add a `docs/RELEASE-NOTES.md` note. It leads with what an operator will act on: **a restore creates
  a NEW company and never replaces one**, and **what the archive does not carry** (memberships, and
  whatever D4/D5 decided).
