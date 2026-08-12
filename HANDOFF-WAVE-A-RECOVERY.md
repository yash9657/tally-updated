# ✅ THE WAVE LANDED AND EVERY GATE IS GREEN. WHAT REMAINS IS THE DRIVE, NOT THE CODE.

> **Rewritten 2026-08-12, at the end of the session that ran it.** An 11-lane parallel edit wave was
> launched against `HANDOFF-DEFECT-BURNDOWN.md` §3, **stopped mid-flight by the operator**, committed as a
> red WIP, and then **recovered lane by lane**. All eleven are resolved. **Every gate is green, measured
> alone.**
>
> ⛔⛔ **AND NOT ONE ROW HAS BEEN DRIVEN IN A BROWSER.** RULE 1's second half is unpaid on **every single
> defect below**. A mutation proof proves the TEST; only the BROWSER proves the FIX — and this repository
> has already had a fix that was traced, tested, mutation-proved and **still broken in the browser**.
> **DO NOT MARK ANY `§7.1` ROW CLOSED ON THE STRENGTH OF THESE SUITES.**
>
> | File | What it is | Do |
> |---|---|---|
> | **THIS FILE** | what landed, what is unverified, what is queued | work §2 → §3 → §4 → §5, then **DELETE it** |
> | `HANDOFF-DEFECT-BURNDOWN.md` | the execution plan: lanes, ownership, the five rules, the traps | §1 and §4 still law |
> | `HANDOFF-PARITY-AUDIT.md` §7.1 | the DATA: every defect at file:line | mark rows **after** the drive |

---

# ⛔ COLD START? THIS SECTION, THEN §1, THEN GO.

## THE GATES, MEASURED 2026-08-12 ON THE FINAL TREE

| Gate | Result |
|---|---|
| backend `go test ./... -count=1 -v` | ✅ **2607 PASS / 0 SKIP / 0 FAIL** (bracketed by a live `psql` before AND after) |
| frontend `pnpm vitest run` | ✅ **1274 passed / 82 files** |
| insightd `pytest` | ✅ green (160) |
| `pnpm exec tsc --noEmit` | ✅ clean |
| `make verify` (run ALONE) | ✅ **exit 0** |
| `verify:demo` on a fresh seed | ✅ **33/33** |
| sabotage keyword sweep | ✅ clean |
| tautology sweep | ✅ clean |

**Baseline before the wave: backend 2483, frontend 1076.** The last green commit before the wave was
`1d715fc`.

## ⛔⛔ THE THREE THINGS THAT WILL COST YOU A SESSION IF YOU SKIP THEM

1. **NOTHING IS BROWSER-DRIVEN.** See the box above. §3 is the drive plan.
2. ⛔ **ONE LANE WAS NEVER RECOVERED — L10 (stock + statements).** Its killed predecessor's work is
   COMMITTED and the suites are green over it, but **no agent ever reported on it, no mutation proof was
   executed for it, and nobody has read its diff.** Green suites over unreviewed code is exactly the
   flattering oracle this project keeps getting caught by. **Treat D9, D45, D46, D53, D64 as UNVERIFIED.**
   See §2.
3. ⛔ **THE SABOTAGE GREP IS NOT SUFFICIENT.** A lane disabled a live control with
   `true ? ledgers : ledgers.filter(…)` and **no marker**. The keyword grep was clean while the defect was
   fully live and four tests were red. **Always run both:**
   ```bash
   grep -rn "MUTATION\|SABOTAGE\|TEMPORARY-PROOF" backend/internal frontend/src insightd/insightd \
     | grep -viE "mutation proof|mutation-proof|a mutation|the mutation|MUTATION-PROVED|MUTATION THAT MUST"
   grep -rnE "\btrue \?|if \(true\)|\|\| true\b|&& false\b" backend/internal frontend/src insightd/insightd \
     | grep -vE "_test\.(go|ts|tsx)|test_.*\.py"
   ```
   Both are clean as of 2026-08-12.

---

## 1. THE COMMITS, IN ORDER

| Commit | Lane | Rows |
|---|---|---|
| `1d715fc` | — | **last green commit before the wave** |
| `9374b09` | the interrupted wave, committed RED on purpose so nothing was lost | — |
| `4f5d523` | **L4** TDS | D1 (server half), D2, D3, D4 |
| `41d7456` | **L9** users + security | D25, D55 (part), D56, D57, D58, D73, D74 |
| `0daf869` | **L1r** bill-wise | D10, D11, D32 |
| `780504d` | **L11** AI-native | D16, D18, D19, D23, D72 |
| `90b0536` | **L8** orders + BOM | D6, D7, D44, D68–D71 |
| `def0c8b` | **L6** voucher form | D39, D41, D59, D60 |
| *(final commit)* | **L7** printing | D29, D30, D31, D33, D34, D35, D61, D62 (part), D63, D75, D76 |

Earlier the same session, each with an executed mutation proof **and** a wire/browser drive — **the only
rows in this whole file that are genuinely finished**: **§7.3 A** (bank-date cutoff, `c1b54ee`) and
**D12 / D65 / half of D13** (`d95d031`).

Lanes that finished before the interruption and were never re-run: **L12** (D38, D66, D67), **L5** (D20,
D21, D50, D51), **L13** (D36 part, D47, row 20). Mutation-proved, not driven.

---

## 2. ⛔ L10 IS THE HOLE. START HERE IF YOU START ANYWHERE.

**Rows: D9, D45, D46, D53, D64.** Files: `screens/reports/StockSummaryScreen.tsx`, `reports/reportView.ts`,
`internal/reporting/{reporting,stock_summary,stock_summary_godown,stock_batch_summary}.go`.

Its predecessor was killed before reporting. Everything it wrote is committed and the suites pass over it.
**That means nothing** — L7's predecessor was in the same state and had shipped a fix whose own comment
claimed *"backed by a test that extracts the text of a rendered PDF"* when **that test did not exist**, and
L8's predecessor left a control disabled by a tautology with four tests red.

**Do this:** read its diff, re-run `./internal/reporting/` and its screen tests, and **execute a mutation
proof per row**. Then decide, per row, whether it is done.

⛔ **DO NOT TOUCH `inventory/valuation.go`.** Its lexical `voucher_no` replay (D8) is deferred ON PURPOSE:
it changes the valuation primitive, must be serialised with T9 FIFO/LIFO, and **moves the Balance Sheet**,
so it needs a deliberate `verify:demo` re-pin.
⚠️ If any L10 change moves a demo figure, `verify:demo` needs that re-pin — it is 33/33 today.

---

## 3. ⛔ THE DRIVE WAVE — THE ONLY THING BETWEEN HERE AND CLOSING ROWS

`HANDOFF-DEFECT-BURNDOWN.md` §0 has the stack, §2 has the pattern. **Driving parallelises fine** (10 agents
drove 14 rows concurrently in an earlier pass); editing and driving must not overlap.

**Per §2.1: every agent seeds its OWN tenant** and takes the id from its own seed log —
`pnpm seed:demo --insightd > /tmp/seed-<lane>.log`, then grep it. ⛔ **Never read
`frontend/.demo-company-id`** — every seed rewrites it, so with N agents it points at somebody else's books.

⛔ **BEFORE ANY DRIVE: rebuild, restart, and VERIFY THE BINARY BY SYMBOL** (§0.1). This fired twice in this
session — once against 20-minute-old cored, reporting a working fix as broken.
`strings -a "$BIN" | grep -c '<a symbol the fix introduced>'` — `0` means STALE, stop. Kill by **port
owner**, never by command pattern.

⛔ **`psql` as `tally_app` returns zero rows under RLS.** Use the `postgres` superuser for every oracle.

**Drive notes that are specific and load-bearing:**
- **L12's D38 is GLOBAL.** Tab was being swallowed on **every screen**; the fix gave it back app-wide.
  Drive reports, the Gateway, Users & Roles and the Bill Inbox — not just the cost picker.
- **L7's oracle is a PDF that NAMES ITSELF** — `%PDF`…`%%EOF`, extract the text, assert the document's own
  number and period. Drive more than one class.
- **L9's lockout drives run ALONE on throwaway subjects.** Lockout is keyed on SUBJECT: a wrong password
  for `browser-dev-owner` locks the account every other agent signs in as.
- **RULE 4 everywhere:** a 403 that still WROTE is this repo's signature failure. Read the books back, and
  assert **sides and ledger names**, not totals.

---

## 4. ⛔ THE QUEUE — REACHABLE HALVES AND CROSS-LANE EDITS, STILL OPEN

**Applied already:** `AppShell.tsx` (Tab/Shift+Tab in `ALWAYS_MEANINGFUL`) · `keymaps/tallyprime.ts`
(`master-create` holds back the voucher keys — F5 opened a Payment over a half-keyed master) ·
`printingdb/document.go` + the `StockVoucherScreen` signpost (L7 took both).

| # | File | What, and why it matters |
|---|---|---|
| 1 | ⛔⛔ `internal/nlq/schema.go` | **L5'S FIX IS NOT ON THE PRODUCTION PATH WITHOUT THIS.** With `ANTHROPIC_API_KEY` set, `ClaudeTranslator` **replaces** `FakeTranslator` — and the prompt's example at **`:127` literally teaches the defective D51 shape** (`entity: vouchers`, `sum(taxable_value)`, filter on `voucher_type_name`). Rewrite it to `postings`/`natural_net`/`pl_group`, and add rules: outstandings is as-at-today; a period must bind to an explicit this/last qualifier; a question naming another company emits entity `""` |
| 2 | `server.go` `handleQuery` | needs an `errors.Is(err, query.ErrCrossTenantScope)` arm, else a cross-tenant refusal reads as the generic "could not understand the question" |
| 3 | ⛔ **D1's REACHABLE HALF — 4 files** | `client.ts` + `rest.ts` + `mock.ts` + `TDSChallansScreen.tsx`. The allocate-later route is **finished server-side and callable by nothing**. ⛔ The screen **must render `unallocated_tax_rupees` from the response** — the falling balance is row 17's whole oracle, and a screen reporting success from a status code looks identical whether it moved or not, which is how D1 survived seven in-browser doors |
| 4 | ⛔ `auth/AuthContext.tsx` | **D55's second clause.** `POST /auth/password` has a client function and **no caller** — nobody can change their own password. Add `changePassword(current, next)` to the context (better than exposing `baseUrl`, so no screen handles a raw token); the affordance then fits on `MembersScreen`'s `is_self` row |
| 5 | ⛔⛔ `api/mock.ts` | **THE MOCK IS LOOSER THAN CORED AND IT IS A SOURCE OF FALSE GREEN — three lanes hit it.** L6's three red tests passed only because the mock accepts an empty ledger name and an unparseable `gst_rate_pct` that `server.go:2552` 400s on. L8: the mock **reproduces D44 verbatim** (`:1832`) and skips D6's party-group and D69's due-date checks — **both still live in dev mode and in every screen test**. L13: `createLedger` does not mirror the opening-breakup sum refusal. ⛔ **Build a structural mock-vs-server gate**, not three patches |
| 6 | `defaultScreens.tsx` | `UnwiredReportScreen` registers **nothing**, so REPORT_BAR sells Ctrl+P **and Alt+E** and both answer "does not apply on this screen" — on every unwired name, including `Alt+G > Vouchers`. Fix = one `usePrintAction()` + an export refusal |
| 7 | `keystroke/buttonBar.ts` | VOUCHER_BAR sells **"Alt+D Delete"** over the four stock screens with no handler. The omission is deliberate in the screens; the **bar** was never told |
| 8 | `FeaturesScreen.tsx` (single-owner) | **TWO falsehoods, and fixing one leaves the other 70 lines below.** `:351-354` claims cash-flow/anomaly detection is state `"on"` with a note naming no door — the file's own vocabulary already has the right word, `"server"` (*built with no key*). `:422` says Cash Flow and the anomaly list "are all readable on screen" — **there is no screen** |
| 9 | `internal/ledger/ledger.go` | comments at ~1170/~1509 still claim the refusal "names the axis"; it names it by its SHARES. Prose only |
| 10 | `CostAllocationPicker.tsx` | `:339-341`, when `r.unassigned !== "0.0000"` render `${r.allocated} of ${line.amount} will be sent — ${r.unassigned} has no centre` |

---

## 5. ⛔ NOT BUILT, AND EACH IS A LANE OF ITS OWN

1. **L14 — negative stock (D5, D43). NEVER LAUNCHED.** Held back because it needed `server.go` while L4
   had it; **L4 has landed, so it is unblocked now.** `allow_negative_stock` is read and honoured by both
   enforcers and can be written by NOTHING — no route, no request field, no setter, no UI — and
   `POST /v1/companies` accepts the key and silently discards it, answering 201.
2. **D22 — the cash-flow screen.** ⛔ **The cheap route: make it a `report:NAME` frame, NOT a
   `ScreenKind`.** It then inherits Alt+G Go To, Display More Reports, the report keymap, the button bar
   and F2 **for free**, and needs no Gateway entry. Files: `keystroke/types.ts` (`REPORT_NAMES`),
   `printTarget.ts` (`REPORT_SLUGS` — omit it and `tsc` breaks), `FrameOverlays.tsx`
   (`AS_AT_ONLY_REPORTS`), `api/{client,rest,mock}.ts`, and a new `CashFlowScreen.tsx`. ✅ **Nothing is
   needed server-side** — the route, `reportName()` and the PDP tokens `report:cash-flow` /
   `report:anomalies` already exist (`authz/vocabulary.go:457`). ⛔ Do **not** ship a Gateway entry alone:
   with no screen behind it, `canonicalReportName` falls through to the raw label and pushes the Unwired
   screen — which is defect #2 in §6, built on purpose.
3. **D49 — insightd has no authentication. RECOMMENDATION: Option B, cored proxies insightd.** insightd
   sets the RLS GUC **from the same path parameter it scopes by**, so RLS is the first layer restated, not
   a second one. ⛔ **Option A is bigger than it looks:** cored mints an ephemeral Ed25519 dev key and
   **publishes no JWKS**, and a signature proves *who*, not *which company* — so insightd would need a
   SECOND COPY of cored's PDP in another language, the exact shape this repo has been burned by. Option B
   costs plumbing only. **Do not bolt a token on.** `/healthz` now discloses the exposure, with a test
   pinning that known-bad value so whoever closes this finds it red and has to argue the change.
4. **D36's second half** — an opening balance can be keyed on a ledger you CREATE; a **seeded** ledger
   (Cash, P&L A/c) still cannot get one, because `setLedgerOpening` is called by nothing. Needs an **Alter
   Master** screen (`Alt+G > Alter Master`, RESEARCH.md:568) with a served ledger picker.
5. **D31's root cause** — nothing in `internal/server` ever constructs a `ledger.InwardDocument`, so
   `supplier_invoice_no` is **unsettable over HTTP on any class**. The debit note now prints via the bill
   reference; the wire gap is real.
6. **D40** — deliberately not given to a lane: unmapped challan sub-routes answer 403 rather than 404/405,
   which makes "never built" indistinguishable from "not permitted". That is a **default-deny decision in
   the PDP**, not a lane's call.
7. **D8** — see §2. T9 work, not a patch.

---

## 6. ⛔ NEW DEFECTS FOUND BY THE WAVE — ADD THESE TO `§7.1`

1. ⛔ **An accounting-mode ALTER RE-POINTS THE BASE LEG — data loss, not an orphan master.**
   `buildGstRequest` always `ensureLedger`s the orientation's canonical base ("Domestic Sales" /
   "Purchases"), so altering an invoice booked to **any other** income/expense head **silently moves it**.
   It survived because the previous lane's own D41 control documented the behaviour as expected. Fix =
   hydrate the base ledger id from the view, which changes what an alter POSTs, so it needs its own drive.
2. **`Alt+G > Vouchers` is broken and always has been** — `canonicalReportName("Vouchers")` returns
   undefined, the `??` falls back to the raw label, and it pushes the Unwired screen.
3. **A server-only refusal still leaves masters behind** (cutoff, ACL, duplicate number, negative stock):
   a posting needs a real uuid and cored has **no `DELETE /ledgers`**. The draft path *discloses* its
   orphans via `DraftAcceptFailedError`; the voucher path does not. Porting that disclosure is real work.
4. **The HSN refusal bites the ALTER path** — an old sales invoice stored without an HSN cannot be re-saved
   without keying one. Arguably correct (it cannot be printed either way), but it is a new refusal on
   existing data.
5. **Concurrent duplicate manual bill numbers** — two simultaneous posts with the same hand-typed number on
   one party can both pass the uniqueness check. Needs an index `bill_allocation` cannot carry, because
   duplicate names are legal for AGAINST/ON_ACCOUNT/caller-named openers. **The sequential case — what an
   operator actually produces — is closed.**
6. **`api/mock.ts` drift** — §4 item 5. Register it as a defect in its own right, not a chore.

---

## 7. WHAT THIS WAVE PROVED ABOUT THE METHOD

- ⛔ **Land work in SMALL BATCHES.** Eleven lanes were launched; three reported before the interruption and
  eight had to be recovered. Batches of three or four, each collected before the next goes out, cost one
  lane on an interruption instead of eight.
- ⛔ **A test that pins a defect is not rare — it is the DEFAULT.** Four of the five recovery lanes found
  them: a test requiring the raw UTC instant that D58 files against; five tests asserting the bare voucher
  number that D11 is about; a test whose title asserted the opposite of its own assertion.
- ⚠️ **AND THE OTHER PATTERN IS ROTTED FIXTURES.** L8's five red tests pinned nothing — `ordersFixture`
  held one debtor and handed it to both directions, so every purchase order it posted **was** the defect,
  and another picked `ORDER BY name LIMIT 1` → "Cash" under a comment reading *"any ledger will do"*.
  **Read the fixture before you read the assertion.**
- ⛔ **Disjoint FILE ownership is not disjoint BUILD ownership.** A Go lane was blocked mid-run by another
  lane's in-flight edit in a package it merely depends on. Expect it, wait, re-run.
- ⚠️ **A quiet suite proves nothing about an interrupted lane.** L7's predecessor left a comment claiming a
  PDF-text test existed; it did not. L8's left a tautology. **Read the diff.**
- ⚠️ **A weak oracle hides inside a passing mutation proof.** L7's M3 passed on `Contains` because `"1"` is
  a substring of `"Sales/2026-27/1"`; it changed the assertion to equality and re-ran. **Prefer equality.**
