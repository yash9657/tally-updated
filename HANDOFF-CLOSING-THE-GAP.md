# HANDOFF — closing the gap to Tally

> **This file supersedes `HANDOFF-T1-PRINTING.md` as the entry point.** That file is still
> the authority on T1 (printing) and on the *procedures* in its §3 and §5 — read those, they
> are not repeated here in full. This file is the plan for everything that is left.
>
> **HEAD `90c9f11` · migrations applied to `0046` · working tree clean · all gates green.**
>
> **§3.1 IS DONE — printing and bank reconciliation are now reachable from the keyboard.**
> `docs/design/t1t4-ui-model.md` is the memo. No migration was needed. See §3.1 below for
> what shipped, and §7 for the three things the work discovered.

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

### 1.3 — A PRODUCT DECISION ONLY YOU CAN MAKE (see also §3.1)

**How should a Bank Reconciliation Statement print, when a company has three bank accounts?**
The print route is `/print/reports/{name}` and carries no ledger id. Options: a `?ledger_id=`
parameter (one statement per print), or one document containing every bank account in turn.
Say which; the session will build it. It currently answers `501` honestly rather than
guessing.

**Still open, and now more visible than it was.** The screen exists, so an operator will
press Ctrl+P on it. It refuses with the reason in full — "the print route carries no ledger
id, so a company with three bank accounts has no way to say which one to print" — rather
than guessing a layout. `printTarget.ts`'s `bank-reconciliation` branch and invariant I-10
are what a future well-meaning change has to argue with.

### 1.4 — THE THING THAT WILL FIND MORE THAN THIS WHOLE FILE

**Run one real business day on it.** Key a day's actual vouchers, reconcile an actual bank
statement, print an actual invoice, and write down every moment you were annoyed or stuck.
Every gap enumerated in this file was found by reading code and database tables, which finds
*missing tables* — it does not find what is unpleasant. **The two missing buttons in §3.1
were found that way and they are the single most embarrassing class of gap in the product.**

---

## 2. WHERE THE PRODUCT ACTUALLY IS

### 2.1 The honesty surface

`frontend/src/screens/FeaturesScreen.tsx` — **45 rows: 25 on, 2 structural ("always"), 3
server-only, 15 off.** Counted at `90c9f11` (the previous count in this file was one out on
two of the four buckets — count them, do not copy the number). It is the thing to update
**in the same commit** as any feature change. A feature marked `on` that is not reachable
from the app is a lie the product tells its own user.

The three rows that moved off `server`-only in `6de36ae` are printing (voucher), printing
(reports) and bank reconciliation. **Each kept its remaining gaps disclosed** — that is the
pattern to copy: a row goes `on` when the key exists, and the note says what the key still
cannot do.

### 2.2 The correctness core is done and is genuinely gated

Double-entry, the immutable hash-chained edit log, bill-wise details, credit/debit notes,
opening balances, numbering series, GST (GSTR-1, 2A/2B, 3B, IMS, ITC), statutory payroll,
inventory with closing stock feeding the Balance Sheet, e-invoice/IRN with freeze, auth +
Tally's Disallow/Allow ACL + WebAuthn, offline sync, printed documents, and bank
reconciliation — the last two now with keys that reach them. **1841 backend tests, 0
skipped, 431 frontend tests, plus 11 independent corpora and seven live end-to-end gates
(`e2e:ui` is now 45 flows, up from 38).** This half is not where the risk is.

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

### 3.1 ~~FIRST — THE TWO FEATURES WITH NO BUTTON~~ — ✅ DONE (`0ce54c6`…`90c9f11`)

Both are reachable from the keyboard. Memo: `docs/design/t1t4-ui-model.md`.

| Was | Now |
|---|---|
| **Printing** (T1) — HTTP only | **Ctrl+P** on a voucher or report, **Alt+P** too. Prints a voucher just saved with Ctrl+A — the case §3.1 was actually about. Six classes print; the other seven refuse with their own reason. |
| **Bank reconciliation** (T4) — HTTP only | **Gateway > Bank Reconciliation**. **F4** picks the account, **F2** dates the statement, the Bank Date cell ticks an item off. |

**Ctrl+P was not a missing key — it was a WRONG one.** `AppShell` returns early on an
unresolved combo, so Ctrl+P reached the *browser* and printed a screenshot of the DOM: no
Rule 46 particulars, no GSTIN, no tax summary. Binding it fixed that as a side effect. It
was also the only documented TallyPrime combo absent from a keymap whose own header says a
dead key is worse than an honest refusal.

**No migration.** The whole track was client work plus **one derived server field**
(`ledger.is_bank`) — which is why it was the best value-per-hour work in the file.

**Still open, and disclosed on `FeaturesScreen`:** the BRS cannot be printed (§1.3 below is
still unanswered — the route carries no ledger id); statement import and matching are §3.2;
and there is no print **options** menu, which is why Alt+P says it printed with defaults.

### 3.2 T4 slice 2 — bank statement import + matching  (migration 0047 — **but see §5 item 1**)
**This is now the next track.** Finish what `81dd870` started; `docs/design/t4-model.md` §1.4
scopes it. **The one rule that matters: never auto-clear an ambiguous match** (same amount,
same day, two vouchers) — surface both and let a human choose. Also add bank account number /
IFSC to `ledger`, which §2 of that memo deliberately deferred.

**The screen is already there to hang it on**, and three things about it are load-bearing:

- The matching keys **are already bound** and answer "not built" (`F8` modify match, `Alt+F8`
  unlink, `Alt+S` potential matches, `F9` set status — all from `RESEARCH.md:879`). Flip
  them to real actions in the same change that builds them, exactly as `Alt+F7`–`Alt+F10`
  were flipped when the stock vouchers got a screen.
- **`F5` is the amount-view toggle** (deposits/withdrawals as two columns) and is also bound
  as unavailable. It belongs to this slice.
- Adding the account number / IFSC means adding the **columns AND the data** together —
  invariant I-11 of `docs/design/t1t4-ui-model.md` exists because a blank labelled field
  reads as "this bank has no IFSC".

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
**"Full Tally parity" is not a near-term goal and probably should not be one** — see §7.

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

### 4.2b A new CLIENT SCREEN has its own registration points — learned in §3.1
A new `ScreenKind` is not one edit either. Missing any of these is either a build failure
(good) or a silent wrong behaviour (bad):
1. `keystroke/types.ts` — the `ScreenKind` union
2. `corpora/validate.py`'s `KNOWN_SCREENS` — **fails `make verify`**, which is the gate that
   catches you; §4.2's list does not mention it
3. `app/defaultScreens.tsx` — the registry, or the screen resolves to nothing
4. `keystroke/buttonBar.ts` — otherwise it falls through `default:` to `GATEWAY_BAR` and
   silently advertises Go To / Switch To / Calculator (**no test catches this**, see §5 item 11)
5. `AppShell.tsx`'s `COMPANY_SCOPED_SCREENS` — if it reads company data, or `useCompanyID()`
   **throws** instead of rendering the "select a company" state
6. `keymaps/tallyprime.ts` `screenBindings` — **the one that bites.** An unbound key is not
   inert: F5 globally opens a Payment voucher and F9 a Purchase, so the new screen gets a
   voucher on top of it. See §6.2.
7. `buttons.test.ts`'s `SCREEN_PATHS` — or the bar is never asserted at all
8. `actionLabel()` for any new `KeymapAction`, or F1 Help renders a blank row

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
| 7 | **Data Entry gets no report print at all.** 0038 seeds zero `allow print report:NAME`. Fail-closed and reversible in one row, but unspecified. **Now user-visible**: Ctrl+P exists, so that level sees the server's 403 on every report. The refusal is passed through verbatim rather than reworded. Still needs a product call on which reports that level may print. |
| 8 | **EDLI premium and EPF/EDLI admin charges unbooked from wage month June 2026** (~₹75 per member per month; rates `ConfUnverified`). **Release blocker. Reading the primary G.S.R. is the fix** — same class of problem as §1.1, and a human with a browser can close it. |
| 9 | **`internal/security/isolation_test.go`'s RLS assertions are now catalogue-driven** and cover all 41 company-scoped tables. The 7-name `tenantTables` list remains for the narrow-role probe that depends on its exact membership — **do not "tidy" the two into one.** |
| 10 | **A FIFTH REGISTRATION POINT exists for a new SCREEN, and §4.2 does not mention it: `corpora/validate.py`'s `KNOWN_SCREENS`.** Adding a `ScreenKind` without it fails `make verify` (the cross-language vocabulary check), which is the good outcome — but budget for it. Screens also need `COMPANY_SCOPED_SCREENS` in `AppShell.tsx` if they read company data, or `useCompanyID()` throws instead of rendering the "select a company" state. |
| 11 | **`buttonBar.ts` has no test that a bar exists for every `ScreenKind`** — it falls back to `GATEWAY_BAR` via `default:`, so a new screen silently advertises Go To / Switch To / Calculator. The decorative-button test only checks the screens listed in its own `SCREEN_PATHS`, so add the new screen there too. Caught by hand this time, not by a gate. |

---

## 6. WHAT §3.1 DISCOVERED — three lessons that generalise

Recorded because each one cost real time and each will recur.

**6.1 — RULE 0.2 APPLIES TO KEYSTROKES, NOT JUST TO TAX RATES.** The §3.1 memo asserted
two combos from memory and **both were wrong in a plausible direction**:

- It called Alt+P "ERP 9 muscle memory". `RESEARCH.md:2165` disproves it — in TallyPrime
  Ctrl+P prints the voucher and **Alt+P opens the print MENU**. Different actions, so
  binding both to one silent print would let an operator believe they had chosen options
  they never saw.
- It said reconciliation is reached by **F5 on a bank ledger's report**. That is the
  **ERP 9** gesture; `RESEARCH.md:873` says Prime uses Alt+G > Banking Activities, and
  `:879` says F5 *inside* reconciliation is the amount-view toggle. The build shipped the
  wrong binding and a test caught it: **pressing F5 twice opened a Payment voucher**,
  because the second press fell through to the global binding.

**`RESEARCH.md` is the citable source for key bindings and it has the answers.** Cite a line
number in the memo, as `docs/design/t1t4-ui-model.md` §6.1 and §6.2 now do. Keystroke
fidelity is the moat; "I remembered the combo" is not verification.

**6.2 — AN UNBOUND KEY ON A NEW SCREEN DOES NOT DO NOTHING. IT DOES SOMETHING WRONG.**
The global table binds F5 to Payment and F9 to Purchase, so any new screen that does not
override them opens a voucher on top of itself. **Enumerate the keys the real screen has
(`RESEARCH.md` usually lists them) and bind every one** — to the real action, or to an
explicit `unavailable`. Only **built** keys may go on a button bar; `buttons.test.ts` fails
the build otherwise, and rightly, because an operator reaches for a bar button by position
from then on.

**6.3 — A DECISION WITH NO DELIVERY MECHANISM IS NOT A REFUSAL, IT IS A DEAD KEY.**
`printTarget.ts` had a fully-reasoned refusal for the BRS from the first commit, and no
screen registered the print action, so the shell fell through to a generic "does not apply
on this screen". **430 unit tests missed it** — they all tested the resolver directly. The
**browser gate caught it**, which is the point of §1.4: over-HTTP gates are exactly how both
these features became unreachable in the first place. When a track adds a key, add a flow to
`e2e:ui` that presses it, and assert on the **network** where a status line could not tell a
real fetch from a browser-printed screenshot.

---

## 7. WHAT "PARITY" SHOULD MEAN — read before promising a date

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
