# HANDOFF — closing the gap to Tally

> **This file supersedes `HANDOFF-T1-PRINTING.md` as the entry point.** That file is still
> the authority on T1 (printing) and on the *procedures* in its §3 and §5 — read those, they
> are not repeated here in full. This file is the plan for everything that is left.
>
> **HEAD `81dd870` · migrations applied to `0046` · working tree clean · all gates green.**

---

## 0. READ THIS FIRST — THE THREE RULES THAT DECIDE EVERY TASK BELOW

If you read nothing else, read these. Every one of them was learned by getting it wrong.

**0.1 — DATA MODEL FIRST. A design memo before any code, per track, no exceptions.**
`HANDOFF-T1-PRINTING.md` §3.2 is the required shape (five sections). T1 took three waves
because the engine was right and the *schema* was missing. Every "stored where" claim must
be checked with `\d` against the live database — not from memory, not from a summary, not
from this file. `docs/design/t4-model.md` and `docs/design/t6-model.md` are worked examples;
the T4 one found, before a line was written, that the plan's stated design was impossible.

**0.2 — NEVER INVENT A RATE, A THRESHOLD, A CLAUSE LETTER OR A FIELD.** Refuse instead. The
project's single most important habit: `gststate` refuses state codes it cannot verify
rather than printing a plausible name. A confidently wrong number is a tax notice; a refusal
is a support call. **Corroboration from a tax blog is not verification** — §2.3 below records
a blog that gave thresholds the statute disproves.

**0.3 — REPORTED-COMPLETE MEANS NOTHING. Run the gates yourself.**
- Full backend suite with `-v`, and **census the skips**: `grep -c -- '--- SKIP'` must be 0.
  Without `-v` no SKIP lines print at all and the grep is vacuous — `make verify` alone
  cannot prove this.
- **Bracket long runs with a live `psql` query before and after.** When the DB is down every
  DB-backed test silently skips and `go test` still exits 0.
- `nc -z` is NOT a liveness check (`docker-proxy` holds the port open while Postgres answers
  nothing). Use `pg_isready` **and** a real query.
- **Age every running server against the commit** before believing a live result. Last
  session all three (`cored`, `insightd`, `statutoryd`) were hours-to-days stale and all
  three answered `200` on `/healthz`. See `HANDOFF-T1-PRINTING.md` §5.7.
- Every new invariant needs a **mutation proof**: break it, watch the test fail, restore
  **byte-identically** (`shasum -a 256`), say you did.
- Every differential test needs a **control**. "Data Entry gets 403" proves nothing unless
  the same token provably gets 200 somewhere it should.

---

## 1. ⚠️ TASKS FOR THE HUMAN — do these; the session cannot

**These are blocked on a browser and a human. Everything else in this file can proceed in
parallel while they are outstanding.** Save each file into `docs/statutory/` and tell the
session it is there.

### 1.1 — THE ONE THING BLOCKING TDS FILING (highest value, ~10 minutes)

`incometaxindia.gov.in` returns **403 to every automated request** — tried with and without
a browser user-agent, from two different fetchers. A human with a browser gets through
instantly.

**Download these two and save them into `docs/statutory/`:**

| What | URL | Save as |
|---|---|---|
| **Form 140** (the quarterly TDS return; replaced Form 26Q) | `https://www.incometaxindia.gov.in/documents/d/guest/fn-140` | `docs/statutory/form-140.pdf` |
| **Form 140 FAQ** | `https://www.incometaxindia.gov.in/documents/d/guest/form-140-faqs` | `docs/statutory/form-140-faqs.pdf` |

**Then one search that needs a human to judge the result.** Go to the Income Tax
Department's e-filing site (`incometax.gov.in`) or the Protean/TIN site and find the
**File Format / File Validation Utility (FVU) specification for the quarterly TDS statement,
tax year 2026-27**. It is usually a PDF or XLS titled something like "File Format for
Quarterly Return". Save it as `docs/statutory/fvu-file-format.pdf`.

**What the session needs out of these three, specifically:**
1. **The four-digit payment code for each nature of payment** — contractor (individual vs
   other), professional fees, technical services, commission/brokerage, rent of plant, rent
   of land/building. Secondary sources suggest 1023/1024/1027/1026/1006/1008/1009 but the
   same source got thresholds wrong, so it is not trusted.
2. **The exact annexure column list and order**, with field widths.
3. **The code list for "reason for non-deduction / lower deduction"**.

Until these exist, `docs/design/t6-model.md`'s invariant **I-7 refuses to emit any return
file**. Everything else in T6 — schema, engine fixes, register, challan tracking — is
unblocked and the session should build it regardless.

### 1.2 — CONFIRM ONE ACT VERSION (5 minutes, do it when convenient)

The statutory rates in `docs/design/t6-model.md` §9 were read from the **Income-tax Act 2025
as enacted** (via ICAI's mirror, because the department blocks automation). The department
also publishes a text **"as amended by Finance Act, 2026"** at:

`https://www.incometaxindia.gov.in/documents/d/guest/income_tax_act_2025_as_amended_by_fa_act_2026-pdf`

Download it as `docs/statutory/ita-2025-as-amended-fa-2026.pdf`. **Any Finance Act 2026
change to the Section 393 rate table is currently unchecked.** The session will re-verify §9
against it. This is not believed to be a large risk, but it is unverified and must not be
described as verified until it is done.

### 1.3 — A PRODUCT DECISION ONLY YOU CAN MAKE (§6.3)

**How should a Bank Reconciliation Statement print, when a company has three bank accounts?**
The print route is `/print/reports/{name}` and carries no ledger id. Options: a `?ledger_id=`
parameter (one statement per print), or one document containing every bank account in turn.
Say which; the session will build it. It currently answers `501` honestly rather than
guessing.

### 1.4 — THE THING THAT WILL FIND MORE THAN THIS WHOLE FILE

**Run one real business day on it.** Key a day's actual vouchers, reconcile an actual bank
statement, print an actual invoice, and write down every moment you were annoyed or stuck.
Every gap enumerated in this file was found by reading code and database tables, which finds
*missing tables* — it does not find what is unpleasant. **The two missing buttons in §3.1
were found that way and they are the single most embarrassing class of gap in the product.**

---

## 2. WHERE THE PRODUCT ACTUALLY IS

### 2.1 The honesty surface

`frontend/src/screens/FeaturesScreen.tsx` — **45 rows: 22 on, 2 structural ("always"), 5
server-only, 16 off.** It is accurate as of `81dd870` and it is the thing to update
**in the same commit** as any feature change. A feature marked `on` that is not reachable
from the app is a lie the product tells its own user.

### 2.2 The correctness core is done and is genuinely gated

Double-entry, the immutable hash-chained edit log, bill-wise details, credit/debit notes,
opening balances, numbering series, GST (GSTR-1, 2A/2B, 3B, IMS, ITC), statutory payroll,
inventory with closing stock feeding the Balance Sheet, e-invoice/IRN with freeze, auth +
Tally's Disallow/Allow ACL + WebAuthn, offline sync, printed documents, and now bank
reconciliation on the server. **1839 backend tests, 0 skipped, plus 11 independent corpora
and seven live end-to-end gates.** This half is not where the risk is.

### 2.3 ⚠️ FIVE PLACES THE TDS ENGINE UNDER-DEDUCTS — this is a correctness issue, not a gap

Read from the **Gazette text of the Income-tax Act 2025**; full detail and citations in
`docs/design/t6-model.md` §9.2. All eight *shipped* rates are **correct**. These are
obligations the engine does not model at all, and every one costs the **deductor** money
plus interest:

1. **Contractor payments have TWO thresholds joined by "and"** — ₹30,000 per payment **and
   ₹1,00,000 aggregate**. Only the first is modelled, so twenty ₹20,000 bills attract nothing.
2. **Directors' fees: 10% at a NIL threshold.** Not modelled at all. Small companies pay
   sitting fees constantly. **Most likely of the five to bite a real user.**
3. **TCS no-PAN is capped at 20%** by s.397(2)(b)(ii); the engine has no ceiling. Latent at
   today's 1% rates, wrong the moment a 20% row is added.
4. **A manufacturing declaration exempts scrap TCS** (s.394(2)) — one of the two shipped TCS
   sections. No concept of it.
5. **Whether you must deduct AT ALL** depends on the payer's own entity type and prior-year
   turnover (₹1 crore business / ₹50 lakh profession). The engine derives for anyone who
   asks, so a ₹40 lakh proprietor would deduct, post and file tax they never owed.

**Fix these as part of T6 (§4.1), not after.** #5 is a stored, effective-dated company
attribute, and the engine must **refuse** rather than silently return zero.

---

## 3. THE GAP, IN THE ORDER I WOULD CLOSE IT

Ordered by who it stops, not by size. Each track starts with its §3.2 memo (rule 0.1).

### 3.1 FIRST — THE TWO FEATURES WITH NO BUTTON. Small work, and it is embarrassing.

**This is the highest value-per-hour work in the whole file and it is not a new feature.**
Two things are fully built, server-side, tested, and **unreachable by a human**:

| Built | Reachable? | What is missing |
|---|---|---|
| **Printing** (T1) | ❌ HTTP only | No print key anywhere in `frontend/src`. An operator cannot print the invoice from the screen they keyed it on. |
| **Bank reconciliation** (T4) | ❌ HTTP only | No bank-reconciliation key. The whole statement and the tick-off loop exist and cannot be used. |

For a **keystroke-compatible** product this is a parity gap, not a nicety — keystroke
compatibility is the actual moat (32 corpus cases) and is what lets an operator switch
without retraining. **Do not build a third headless feature before fixing these two.**

Shape: a print key on the voucher screen and a reconciliation screen listing a bank ledger's
postings with an editable Bank Date column (exactly Tally's `F5` view). The server side is
done; both are UI work against existing routes.

### 3.2 T4 slice 2 — bank statement import + matching  (migration 0047)
Finish what `81dd870` started. `docs/design/t4-model.md` §1.4 scopes it. **The one rule that
matters: never auto-clear an ambiguous match** (same amount, same day, two vouchers) —
surface both and let a human choose. Also add bank account number / IFSC to `ledger`, which
§2 of that memo deliberately deferred.

### 3.3 T6 — TDS/TCS persistence, register, returns  (migrations 0049–0050)
**The memo is written and every decision is resolved against the statute:
`docs/design/t6-model.md`.** Review it adversarially, then build. Key points so you do not
re-derive them: the `section` column stores a **four-digit numeric payment code** (the old
194-series does not exist under the 2025 Act); rounding is **nearest rupee per deduction**
with the challan being the sum; the code attaches **per voucher line**; 21 fields are
MIGRATION including the deductor's own TAN and PAN, which `company` does not have. **Fix
§2.3's five gaps in the same track.** Form 26Q is now **Form 140**, 24Q is **138**, 27EQ is
**143**. Salary TDS (24Q/138) is explicitly out of scope. **The FVU cannot run here — never
claim a validated return.**

### 3.4 T2 — Cost centres and cost categories  (0040–0041)
Very widely used in Tally; there is no allocation dimension on postings at all. Budgets
(T11) depends on it. **The sum-to-posting invariant is a constraint trigger, not a form
check**; allocations must live inside the hashed payload; parallel allocation per category
is not one column on `posting`.

### 3.5 T3 — Order processing (Sales/Purchase Orders) + BOM  (0042–0044)
Most trading businesses run on them. **An order is a COMMITTED QUANTITY, NOT A MOVEMENT** —
do not push it through `stock_movement` or closing stock and therefore the Balance Sheet are
corrupted. Partial fulfilment is the normal case, not an edge case.

### 3.6 T5 — e-Way Bill  (0047–0048 → renumber, see §5)
Legally required to move goods over ₹50,000. **Reuse the IRN precedent in
`internal/statutory` — do not invent a second pattern.** The NIC API is unreachable from
here, so it ships behind the same deterministic-fake + recorded-schema arrangement as IRN,
and says so on its face.

### 3.7 Then, in this order
| Track | Why | Migrations |
|---|---|---|
| **T9 FIFO/LIFO/batch valuation** | v1 is weighted average. Closing stock feeds the Balance Sheet, so this **moves the sheet** — re-pin `verify:demo`. | 0053 |
| **T10 Stock groups + compound units** ("Box of 12") | Items are flat, units are bare symbols. Serialise with T9 — both touch inventory. | 0054–0055 |
| **T8 Multi-currency** | Only exporters, but for them it is the whole ledger. A foreign amount, a rate and the base amount are **three stored values**, not two plus arithmetic. | 0051–0052 |
| **T11 Budgets** | Needs T2's cost-centre dimension first. | 0056–0057 |
| **T12 GSTR-9 annual return** | Annual filers. | 0058 |
| **T13 Interest on overdue bills** | Anyone charging it. | 0059 |
| **T14 Backup / restore / split company** | Operationally important, absent. **Must be tenant-exact** — this is the one where a bug loses somebody's books. | 0060–0061 |
| **Export (Excel/CSV/XML/e-mail)** | Daily annoyance. `authz.Export` exists as an action with **zero route mappings**, so it is default-DENY — an ACL decision is needed, not just a handler. | — |
| **Per-company print templates (UI)** | 0037 built the table and the store; **nothing writes an override**, so every company prints the built-in layout. | — |
| **SSO / OIDC sign-in** | The VERIFY half exists (point it at a JWKS). There is no sign-in flow, no redirect, no provider button. **Disclosed gap in the verify half too: the audience claim is not checked.** | — |

### 3.8 CANNOT BE CLOSED HERE — say so, do not attempt
**Rule 48(4) QR code on a printed e-invoice.** The QR encodes an **IRP-signed** payload; no
IRP/NIC call is possible from this environment. The printed document already says this on
its face. Do not fabricate a QR.

### 3.9 NOT ENUMERATED ANYWHERE — and it is a lot
Tally has thirty years of surface this file does not list: **POS, job costing, ODBC,
data synchronisation, payroll attendance and leave, manufacturing journals, multi-godown
transfers with in-transit, price levels per party, reorder levels, and more.**
`PLAN-TALLY-PARITY.md` §7 says Tier 2 is explicitly not exhaustive, and it is not.
**"Full Tally parity" is not a near-term goal and probably should not be one** — see §6.

---

## 4. HOW TO WORK

### 4.1 The shape of a track
```
1. docs/design/<track>-model.md      §3.2's five sections, checked with \d
2. ONE adversarial review of the memo   no build until it is accepted
3. Build, partitioned by FILE OWNERSHIP
4. Review: 2–3 adversarial lenses that RE-DERIVE rather than re-read
5. Run the full scoped suite YOURSELF, then the live gates ALONE
6. Update FeaturesScreen.tsx IN THE SAME COMMIT
```

### 4.2 A new resource token has FOUR registration points, not one
Learned in T4. Missing any one produces a route that default-denies for **every** level
including an Owner:
1. `routeAction()` in `auth_middleware.go`
2. `authz.KnownResources`
3. `authz.KnownReportNames` (if it is a report)
4. `reportName()` — because `routes()` mounts `/print/reports/<name>` for **every** known
   report name

### 4.3 An ACL migration has TWO registration points
Also learned in T4, and the failure is silent and in the worst direction. You must **both**
backfill existing companies **and** extend `seed_company_auth_defaults` (every ACL migration
since 0011 re-declares that function wholesale — derive yours from the most recent one).
Getting only the backfill means every company created *afterwards* silently lacks the rules.
**Write the test that creates its company at test time**, or you will not catch it.

### 4.4 Migrations
- They apply themselves the moment the file exists (`//go:embed`, and every test's
  `newServer()` calls `Apply`).
- **Every migration must be re-runnable.** `applyOne` wraps each file in one transaction and
  stamps `schema_migrations` inside it, so a non-re-runnable file **walls off every later
  migration**. `ADD COLUMN IF NOT EXISTS` is fine; `ADD CONSTRAINT` and `CREATE POLICY` need
  a `DO $$ … IF NOT EXISTS … $$` guard.
- **Prove re-runnability by running the file by hand** (`psql -f`), not by re-running
  `make migrate` — that only proves the stamp works.
- Every new tenant table: RLS `ENABLE` **and** `FORCE`, a policy with `USING` **and**
  `WITH CHECK`, composite `(company_id, id)` FKs, and **a row in `testdb`'s `buildFixture`**
  (the teardown harness fails loudly if you add a table and seed no row).

### 4.5 The gate commands
```bash
# liveness FIRST — and again after
pg_isready -h localhost -p 5433 -t 5
PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"

make verify                                    # migrate, build, residue, frontend, corpora, insightd
cd backend && go test ./... -count=1 -v | grep -c -- '--- SKIP'   # must be 0

# live gates — rebuild the servers FIRST (§0.3) and run them ALONE
make run-cored          # + make run-insightd in another shell
make seed-all           # NOT `npm run seed:all` — no such script
cd frontend
pnpm verify:demo    "$(cat .demo-company-id)"                               # 31/31
pnpm verify:tenancy "$(cat .demo-company-id)" "$(cat .tenancy-company-id)"  # 113/113
pnpm e2e:ui                                                                 # 38/38, run TWICE
CORED_BASE_URL=http://localhost:8087 pnpm e2e:edit          # + numbering, state-code
CORED_BASE_URL=http://localhost:8087 INSIGHTD_BASE_URL=http://localhost:8088 pnpm e2e:ocr
# statutoryd freeze gate: recipe in HANDOFF.md §"statutoryd (IRN plane)"
```
Ports: cored **8087** · insightd **8088** · statutoryd **8091** · vite **5173** · pg **5433**.

---

## 5. OPEN ITEMS AND KNOWN INCONSISTENCIES

| # | Item |
|---|---|
| 1 | **Migration numbers past 0046 need renumbering.** `PLAN-TALLY-PARITY.md` §2.5 reserved 0045–0046 for T4 and T4 used them, but it also reserved **0047–0048 for T5 (e-Way Bill)** while T4's slice 2 (§3.2) will want the next free number. **§2.5 is the authoritative table — fix it once, in one place, before two tracks collide.** This exact class of drift already bit: all five per-track lines were stale by one and pointed T2 at a number T1 had consumed. |
| 2 | **`corpora/validate.py` re-declares the TDS/TCS rate table** (`:262`), so it is an independent implementation of the *arithmetic* only. A wrong rate produces two agreeing implementations and a green gate. Make the fixtures carry a **citation to a section and serial number** so a rate change breaks the citation. |
| 3 | **A party's address cannot be ALTERED.** `POST /ledgers` sets it; nothing changes it. An operator who mistypes must delete and recreate the party, which its posted vouchers forbid. Needs `masters.SetLedgerAddress` + a `PUT` route + an ACL decision. **Now unblocked** — the read side was fixed in `b76a7e1`. |
| 4 | **Rule 46(e): a ≥₹50,000 supply to an unregistered party.** May already work now that `gststate` exists — **verify before building**. |
| 5 | **`handler_print_reports.go` duplicates `printingdb`'s unexported `loadSupplier` verbatim.** Export one and delete the copy or they will drift. |
| 6 | **The printed Day Book lists voucher VERSIONS**, so an altered voucher contributes twice. It deliberately differs from the JSON one. **A judgement, not a spec — needs a product call.** |
| 7 | **Data Entry gets no report print at all.** 0038 seeds zero `allow print report:NAME`. Fail-closed and reversible in one row, but unspecified. |
| 8 | **EDLI premium and EPF/EDLI admin charges unbooked from wage month June 2026** (~₹75 per member per month; rates `ConfUnverified`). **Release blocker. Reading the primary G.S.R. is the fix** — same class of problem as §1.1, and a human with a browser can close it. |
| 9 | **`internal/security/isolation_test.go`'s RLS assertions are now catalogue-driven** and cover all 41 company-scoped tables. The 7-name `tenantTables` list remains for the narrow-role probe that depends on its exact membership — **do not "tidy" the two into one.** |

---

## 6. WHAT "PARITY" SHOULD MEAN — read before promising a date

**The correctness core is strong and roughly done; the daily-workflow surface is about
60–70% there.** Finishing §3.1 (the two buttons) and §3.2–3.3 (bank statement import, TDS)
gets a straightforward GST trading or services SMB to genuinely **usable**. Four or five
more tracks gets to **comfortable**.

**Feature-count parity with Tally is the wrong target.** Tally has thirty years of surface
(§3.9), and chasing all of it trades away the reason to switch. Where this product is
**ahead** — and where the effort belongs:

- **The hash-chained audit trail is STRUCTURAL, not a toggle.** Tally's Edit Log can be
  switched off. This cannot.
- **Conversational query over the books** and **OCR bill capture** have no Tally equivalent.
- **Keystroke compatibility** (32 corpus cases) is what lets an operator switch without
  retraining. It is the real moat — **protect it in every track**, and note that §3.1 is
  exactly a failure to protect it.

The remaining work is well understood and mostly not research. **But that is exactly what was
said about T1**, which took three waves because the data model kept turning out to be the
gap. §0.1 exists so that sentence stops being true, and it has now worked twice — T4's memo
found a plan-breaking flaw before any code, and T6's found three.
