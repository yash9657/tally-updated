# ⛔ WAVE A WAS KILLED MID-FLIGHT. THIS FILE IS THE RECOVERY, AND IT IS TEMPORARY.

> **Written 2026-08-10.** A parallel edit wave of 11 lanes was launched against `HANDOFF-DEFECT-BURNDOWN.md`
> §3. **Three finished and reported. Eight were stopped mid-work by the operator.** Their edits are
> COMMITTED (this file is committed with them) but they are **unverified, unproved and in four cases
> RED**. Nothing here has been driven in a browser.
>
> | File | What it is | Do |
> |---|---|---|
> | **THIS FILE** | the state of one interrupted wave, per lane, with the exact next action | work it, then **DELETE it** |
> | `HANDOFF-DEFECT-BURNDOWN.md` | still the execution plan: lanes, ownership, the five rules, the traps | read §1 and §4 before touching anything |
> | `HANDOFF-PARITY-AUDIT.md` §7.1 | still the DATA: every defect at file:line | your work list |
>
> ⛔ **DO NOT TREAT THE COMMITTED WORK AS DONE.** It is committed so it is not lost, not because it is
> right. **Every killed lane below must be finished or reverted, and its mutation proof re-executed.**
> ✅ **WHEN EVERY LANE BELOW IS RESOLVED: fold the outcome into the burndown's §7.1 rows and DELETE THIS FILE.**

---

# ⛔⛔ COLD START? READ THIS SECTION, THEN §1, THEN GO.

## THE ONE-PARAGRAPH STATE

`master` at the commit carrying this file **does not pass its gates**. Backend: **4 packages RED**
(`masters`, `reporting`, `server`, `testhygiene`). Frontend: **12 tests RED in 4 files** (1211 pass).
`tsc`: **2 errors**. insightd: **green**. The backend **builds**. This is not a mystery to debug — §2 maps
every failure to the lane that caused it, and each one is *unfinished work*, not a regression in shipped
code. ⛔ **Do not start by "fixing the red". Start by picking a lane in §2 and finishing or reverting it.**

## ⛔ THE THING THAT WILL BITE YOU FIRST — ✅ NOW RESOLVED, BUT READ IT ANYWAY

A killed lane left a **live mutation-proof sabotage** in `internal/masters/tds_challan.go` — a no-op loop
where the allocation write belongs, marked `MUTATION P1: the route answers 200 and writes nothing`. The
original code was never committed, so git could not restore it; it was first replaced with a fail-closed
refusal, and the **L4 recovery has now written the real loop** (§2). `internal/masters` is green and the
grep below is clean.

⚠️ **It is left documented here because the LESSON is the durable part, not the incident:** an interrupted
lane can leave code that reports success over a write it never made, and that is indistinguishable from a
working route until somebody reads the books back.

> ⛔⛔ **AND THE GREP IS NOT ENOUGH — THIS WAS LEARNED THE HARD WAY ON 2026-08-10.** A lane disabled a
> live control with `true ? ledgers : ledgers.filter(…)` and **no marker at all**. The grep below was
> clean while the defect was fully live and four tests were red. **Sweep for tautologies too:**
> ```bash
> grep -rnE "\btrue \?|if \(true\)|\|\| true\b|&& false\b" backend/internal frontend/src insightd/insightd
> ```
>
> ⛔ **BEFORE YOU BELIEVE ANY PART OF THIS TREE, RUN:**
> ```bash
> grep -rn "MUTATION\|SABOTAGE" backend/internal frontend/src insightd/insightd \
>   | grep -viE "mutation proof|mutation-proof|mutation_proof|a mutation|the mutation|mutation test"
> ```
> As of the L4 recovery the only hit is `handler_stock_reports_test.go:562` — pre-existing prose describing
> a mutation to perform, not live code. **Any other hit is live sabotage from a killed lane. Treat it as a
> P0.**

## WHAT WAS ACTUALLY FINISHED, AND IS SAFE

Two things landed EARLIER in the same session, each with executed mutation proofs, a wire/browser re-drive
and green gates at the time. **They are not in question:**

- **§7.3 A — the bank-date cutoff** (commit `c1b54ee`). Plus the structural gate
  `TestEveryDateShapedWireFieldIsAccountedForByTheCutoff`, which fails if any new date-shaped json field on
  a wire type in package `server` is left unclassified. ⚠️ **If you add a dated request type, you must add a
  registry entry** — see §4.
- **D12, D65 and half of D13 — the ageing report** (commit `d95d031`).

---

## 1. RUN FIRST — and the numbers to expect

The stack, the seed and the gates are in `HANDOFF-DEFECT-BURNDOWN.md` §0. Everything there still applies,
including the two that have each cost this project a session:

1. ⛔ **VERIFY THE RUNNING BINARY BY SYMBOL** (§0.1). This fired again on 2026-08-08: a browser re-drive
   reported a working fix as broken because cored was 20 minutes stale. `strings -a "$BIN" | grep -c
   '<a symbol your fix introduced>'` — `0` means STALE, stop.
2. ⛔ **`psql` as `tally_app` returns zero rows under RLS.** Use the `postgres` superuser for every oracle.

**Measured on the commit that carries this file** — this is your baseline, and it is RED on purpose:

| Gate | State |
|---|---|
| `go build ./...` | ✅ passes |
| backend `go test ./...` | ⛔ **`server` only — 5 tests, ALL L8 orders.** `masters` ✅ L4 · `testhygiene` ✅ L9 · `reporting` ✅ L1r |
| frontend `pnpm vitest run` | ⛔ **7 failed / 1225 passed** — 3 `wiring.test.tsx` (**L6**), 4 `OrderRegisterScreen` (**L8**). Was 12/1211 |
| `pnpm exec tsc --noEmit` | ⛔ **2 errors**, both in `BillInboxScreen.test.tsx` |
| insightd `pytest` | ✅ **green** |
| `make verify` / `verify:demo` | not run — meaningless until the above is resolved |

**The last GREEN commit is `1d715fc`.** Everything after it is this wave.

---

## 2. ⛔ THE LANES — THREE DONE, EIGHT UNFINISHED

Ownership was disjoint by design, so **the lanes below can be resumed independently and in parallel**, with
one exception noted in §3.

### ✅ DONE — reported, mutation-proved, NOT browser-driven

| Lane | Rows | State |
|---|---|---|
| **L12** cost allocation | D38, D66, D67 | ✅ complete. 4 proofs executed. ⛔ **Its D38 fix is GLOBAL:** the `preventDefault` on Tab/Shift+Tab in `useNavMachine` was swallowing Tab on **every screen**, not just the picker — `nextField` returns `{}` on reports, the Gateway, Users & Roles and the Bill Inbox. The whole app got Tab back. **Re-drive accordingly: this touches every screen, not one.** |
| **L5** NL query | D20, D21, D50, D51 | ✅ complete. 12 proofs executed, including a live-Postgres parity suite that compares the NL answer to `reporting.Outstandings` / `ProfitAndLoss` rather than to a constant. ⛔ **See §4 item 1 — its fix is NOT on the production path yet.** |
| **L13** masters | D36, D47, row 20 | ✅ complete. 7 proofs executed. All five `MasterKind`s now have a keystroke (`Alt+G > Create …`). ⚠️ **D36 is only PARTLY closed** — see §5. |

⛔ **All three still owe RULE 1's second half: a browser re-drive.** A mutation proof proves the TEST. Only
the browser proves the FIX. Do not mark any §7.1 row closed on the strength of the suites.

### ⛔ UNFINISHED — killed mid-work. Each row is a decision: FINISH or REVERT.

| Lane | Rows | What is in the tree | ⛔ The exact next action |
|---|---|---|---|
| ~~**L4** TDS~~ | D1–D4 | ✅ **SERVER SIDE DONE (2026-08-10).** The missing allocation loop is written and routes through `allocateToDeduction` — the SAME function the create path calls, so both write paths share one verifier and one cap. `grep INSERT|UPDATE|DELETE` on `tds_challan_allocation` returns exactly one non-test hit. **`internal/masters` is fully green** (was 4 red) and the 6 TDS wire tests pass. The sabotage is GONE. 3 proofs executed, incl. one that reproduced D2 verbatim on the new path (a ₹1,000 deposit paying ₹2,000 of tax, `unallocated_tax_rupees:"-20000.0000"` behind a 200) | ⛔⛔ **D1 IS STILL UNREACHABLE FROM THE BROWSER — DO NOT MARK IT CLOSED.** Nothing in the client can call the route: `allocateToChallan` exists in neither `client.ts`, `rest.ts` nor `mock.ts`, and `TDSChallansScreen` renders no affordance. See §4 items 8–11. **D3 is likewise unbuilt**: `VoucherRequest.tds/.tcs` are expressible but NO screen constructs either block, so a deduction still cannot be keyed and the picker stays empty on real books |
| ~~**L1r** bill-wise~~ | D10, D11, D32 | ✅ **DONE (2026-08-10).** The redesign was kept and FINISHED — and its own doc comment was wrong: it claimed `Sales/2026-27/7` is *"unique by construction"*, which it is not. The counter's key is (company, **voucher type**, FY) while the name carries the **class**, and two paths collide on it — ⛔ **one of them a KEYSTROKE, not a theory:** `numbering.go` honours a client-supplied `voucher_no` verbatim without bumping the counter, and `VoucherScreen.tsx:1595` sends that field, so an operator typing `1` into the No. box merged 16,800 into one bill. Also closed: a generated name landing on `ledger_opening_bill` (18,800 merged into the carried-in balance), and the unnumbered fallback — `voucherID.String()[:8]` on a **uuid v7** is the top 32 bits of a millisecond clock, so the escape hatch against merging *was* a merge for every voucher minted in the same ~65s. ⛔ **All five red tests PINNED the defect**; their arcs were kept and the assertions made STRONGER (the receipt now reads the bill name back off `Outstandings`, which is where the settle picker gets it). 7 proofs executed | ⛔ Not browser-driven. See §4 item 13 |
| ~~**L8** orders + BOM~~ | D6, D7, D44, D68–D71 | ✅ **DONE (2026-08-10).** ⛔⛔ **IT FOUND AN UNMARKED LIVE SABOTAGE THE GREP CANNOT SEE:** `OrderRegisterScreen.tsx:291` read `true ? ledgers : ledgers.filter(…)` — D6's client filter disabled by a **tautology**, with no `MUTATION` marker. The prescribed grep was clean while the defect was fully live. See §6. ⛔ **And NONE of the nine red tests pinned a defect** — five were **ROTTED FIXTURES** (`ordersFixture` had one debtor and handed it to both directions, so every purchase order it posted *was* D6; another picked `ORDER BY name LIMIT 1` → "Cash" under a comment saying "any ledger will do"), and four were correct tests against sabotaged source. It also asserted the **no-stock invariant**, which four comments stated and nothing checked. 11 proofs, each restored to a verified sha256 | ⛔ `api/mock.ts` needs three mirrors — §4 item 14 |
| ~~**L9** users + security~~ | D25, D55–D58, D73, D74 | ✅ **DONE (2026-08-10).** The killed lane's work was re-read and RE-PROVED rather than assumed. D56 is asserted as a PROPERTY at `Server.authenticate` — the one funnel all three call sites go through — so a route mounted tomorrow inherits it. ⛔ **The door that would have re-opened everything:** `/auth/webauthn/assert/*` MINTS a write-capable token, so a revoked session must not be able to trade up; closed, with the register/`auth/password`/`POST /v1/companies` doors, each with a **DB read-back** rather than a status code. `internal/authn` + `internal/testhygiene` green; 9 proofs executed | ⛔ **D55's SECOND CLAUSE IS STILL OPEN — see §4 item 12.** `POST /auth/password` has a client function and NO CALLER, so a user still cannot change their own password |
| ~~**L6** voucher form~~ | D39, D41, D59, D60 | ✅ **DONE (2026-08-10).** All three red tests **pinned the defect**, and the reason is worth keeping: they passed only because **`api/mock.ts` is looser than cored** — it accepts an empty ledger name and an unparseable `gst_rate_pct` that the real handler 400s on (`server.go:2552`), so the suite certified bodies cored refuses. D39+D41 are closed as ONE sentence — *the client writes masters before the server has agreed to the document* — with a **structural walk over `VoucherScreen.tsx` itself** asserting the refusal precedes the first `ensure*` in every builder and that no `ensure*` stands outside a builder at all, so a fifth builder or a pasted `ensureLedger` fails naming file and line. Field highlight now follows real focus. 7 proofs | ⚠️ **A server-only refusal (cutoff, ACL, duplicate number) STILL leaves masters** — there is no `DELETE /ledgers`. Stated in the gate's header so it is not mistaken for closed |
| ~~**L11** AI-native~~ | D16, D18, D19, D22, D23, D49, D72 | ✅ **DONE (2026-08-10)** for D16/D18/D19/D23/D72; the `tsc` errors are fixed. D16 is closed as a PROPERTY: `persistence.py` classifies **every string-bearing field of the draft body** and a field it cannot classify is a hard runtime error, so a field added to `cored_map.py` tomorrow turns the suite red instead of shipping an unguarded master name. ⚠️ **Its other half matters as much:** money/dates/uuids are classified STRUCTURED and never scrubbed, because `AADHAAR_RE` is `\b\d{12}\b` and a fixed-4 amount like `180000000000.0000` contains a 12-digit run — scrubbing everything would one day eat money. D18+D19 fixed at BOTH ends. 8 proofs; insightd **160 passed** | ⛔ **D22 IS A BUILD, NOT BUILT — see §5 item 6 for the cheap route.** D49 is a RECOMMENDATION only — see §5 item 7 |
| **L7** printing | D29–D35, D61–D63, D75, D76 | `printTarget`, print handlers, `printingdb`, `DrillRows` | ⚠️ **No red tests attributable to it**, which means only that its own tests pass — it was killed before reporting, so **nothing about it is proved**. Re-run its packages, read its diff, finish the rows it did not reach. Oracle: a real PDF (`%PDF`…`%%EOF`) that NAMES ITSELF |
| **L10** stock + statements | D9, D45, D46, D53, D64 | `reportView` depth fix, as-on resolution, the 403 text | ⚠️ Same as L7 — no attributable red, nothing proved. ⛔ **DO NOT TOUCH `inventory/valuation.go`** (§4.1 of the burndown). ⚠️ If a change moves a demo figure, `verify:demo` needs a deliberate re-pin |
| **L14** negative stock | D5, D43 | **NOTHING — never launched** | Held back because it needs `server.go`, which L4 held. **Launch it once L4 settles** |

---

## 3. ⛔ THE ONE ORDERING CONSTRAINT

**L4 and L14 both need `backend/internal/server/server.go`.** Everything else in §2 is file-disjoint and can
run in parallel. Resolve ownership BEFORE launching a wave, never during it — two lanes editing one file is
how a fix gets silently lost.

---

## 4. ⛔ CROSS-LANE EDITS — THE QUEUE. TWO ARE APPLIED; FOUR ARE NOT.

Lanes were told to REPORT edits in files they did not own rather than make them. These are those reports.

✅ **APPLIED already (verify, do not redo):**
- `app/AppShell.tsx` — `nextField`/`prevField` added to `ALWAYS_MEANINGFUL`. Without it the status line said
  *"Tab — Next field does not apply on this screen"* while the browser **did** move focus. A status line
  that contradicts the screen teaches the operator to stop believing it.
- `keystroke/keymaps/tallyprime.ts` — `master-create` now holds back the voucher keys. **F5 opened a Payment
  straight over a half-keyed master**, and L13's Go To route turns that form into one an operator lingers on.

⛔ **STILL OPEN — apply these, they are the reachable halves of finished work:**

1. ⛔⛔ **`backend/internal/nlq/schema.go` — L5's FIX IS NOT ON THE PRODUCTION PATH WITHOUT THIS.** When
   `ANTHROPIC_API_KEY` is set, `ClaudeTranslator` **replaces** `FakeTranslator`, so D21/D50/D51 are fixed
   only on the fake path. Worse: the system prompt's example at **`schema.go:127` literally teaches the
   defective D51 shape** (`entity: vouchers`, `sum(taxable_value)`, filter on `voucher_type_name`).
   **Needed:** (a) rewrite that example to `postings` / `natural_net` / `pl_group`; (b) add rules that
   outstandings is as-at-today, that a period must bind to an explicit this/last qualifier, and that a
   question naming another company must emit entity `""` (the schema's documented refusal channel).
2. `backend/internal/server/server.go` — `handleQuery` needs an `errors.Is(err, query.ErrCrossTenantScope)`
   arm, or a cross-tenant refusal surfaces as the generic *"could not understand the question"*.
3. `screens/VoucherScreen.tsx` (L6) — the 5-string field HIGHLIGHT does not follow real focus now that Tab
   works, so it saturates on `narration` while focus walks into the grid. Nothing is unreachable; the
   decoration lags. Fix with `onFocusCapture` deriving from `document.activeElement`, or a real
   `:focus-visible` ring. **Also `:2584` says "Alt+C from Gateway > Masters" — `Gateway > Masters` DOES NOT
   EXIST. It is now `Alt+G > Create Stock Item`.**
4. `screens/vouchers/StockVoucherScreen.tsx` (L7) — `:310` has the same dead signpost.
5. `screens/vouchers/CostAllocationPicker.tsx` — `:339-341`, when `r.unassigned !== "0.0000"` render
   `${r.allocated} of ${line.amount} will be sent — ${r.unassigned} has no centre`.
6. `api/mock.ts` — `createLedger` does not mirror `masters.CreateLedger`'s opening-breakup sum refusal
   (only `setLedgerOpening` does). **The mock must refuse what the server refuses.**
7. `internal/ledger/ledger.go` comments at ~1170 and ~1509 still claim the refusal *"names the axis"*. It
   names it by its SHARES now. Prose only.

⛔⛔ **8–11 ARE ONE JOB: THE REACHABLE HALF OF D1.** The server side is finished and correct, and until
these land it is dead code — the exact shape RULE 3 forbids and the exact shape that left an insightd
mechanism unwired earlier in this project.

8. `frontend/src/api/client.ts` — add `allocateToChallan(companyID, challanID, req): Promise<...>`. **Both
   types already exist** at `types.ts:2118/2138`.
9. `frontend/src/api/rest.ts` — `POST ${company(id)}/tds-challans/${challanID}/allocations`. ⚠️ **403 is a
   NORMAL answer on this route** (bare `tds-challan` token; Data Entry denied by design) and so is a
   **cutoff 403 on a body carrying no date** — that refusal names the *deposit date* and must be surfaced,
   not flattened into an ACL message. This is register row D29's mistake on another screen.
10. `frontend/src/api/mock.ts` — the mock must refuse what the server refuses: over-allocation, mixed
    TDS/TCS kinds, already-paid, interest-only, empty array. Same drift as item 6.
11. `frontend/src/screens/TDSChallansScreen.tsx` — an "attach to this deposit" affordance on any register
    row whose `unallocated_tax_rupees != "0.0000"`, reusing the existing `allocatable_deductions` picker.
    ⛔ **It must render `unallocated_tax_rupees` from the response.** The falling balance is row 17's entire
    oracle, and a screen that reports success from the status code alone looks identical whether the
    balance moved or not — which is how D1 survived seven in-browser doors.

12. ⛔ **`frontend/src/auth/AuthContext.tsx` — the reachable half of D55's second clause.** `POST
    /auth/password` has a client function (`api/auth.setPassword`, exported and re-exported) and **no
    caller anywhere but tests**, so a user cannot change their own password. It was left unbuilt rather
    than half-built: the affordance needs the bearer and base URL, which live on `AuthContextValue`.
    **Add `changePassword(current, next)` to the context** — better than exposing `baseUrl`, so no screen
    handles a raw token. With that, the reachable half fits on `MembersScreen`'s `is_self` row: Gateway →
    Users & Roles, no new `ScreenKind`, no `navMachine`/`AppShell`/`validate.py` edit.

13. **`backend/internal/printing/printingdb/document.go` (L7's file).** A Credit Note prints *"Against
    Invoice No.: `<ref_name>`"*, which now reads `Sales/2026-27/7` instead of `7`. `againstInvoiceDate`
    already looks up the voucher that opened the bill — it should return the **number** beside the date and
    print that. Pre-existing for any hand-named bill (`PO-4471/partial`); L1r's fix makes it common.
    ⛔ **THIS IS NOW FIRING FOR REAL:** `printingdb`'s `TestACreditNoteCannotBePrintedAtAll` went red the
    moment L1r landed.

14. **`frontend/src/api/mock.ts` needs three mirrors, and one of them REPRODUCES A DEFECT THE SERVER NO
    LONGER HAS.** (a) `:1832` is D44 verbatim — `is_overdue: Boolean(l.due_date && asOn && l.due_date <
    asOn)` — so in dev mode and in every screen test the default no-as-at order book **still reports zero
    late deliveries**. (b) `mockCreateOrder` does not check the party GROUP, so against the mock an
    operator can tick the escape hatch and pick HDFC Bank and it succeeds, while cored 422s — D6's "a
    client filter alone is a suggestion" half, live in dev. (c) it does not refuse a due date before the
    order date (D69). **The mock must refuse what the server refuses.**

---

## 5. ⛔ NEW FINDINGS FROM THIS WAVE — NOT IN §7.1 YET. ADD THEM.

1. **`Alt+G > Vouchers` is broken and always has been.** `canonicalReportName("Vouchers")` returns
   undefined, the `??` falls back to the raw label, and it pushes `{screen:"report", reportName:"Vouchers"}`
   — the Unwired screen. Pre-existing, found by L13, out of its rows.
2. **D36 is only PARTLY closed.** An opening balance can be keyed on a ledger you CREATE. A **seeded** ledger
   (Cash, Profit & Loss A/c) still cannot be given one: `setLedgerOpening` is called by nothing. That needs
   an **Alter Master** screen (`Alt+G > Alter Master`, also RESEARCH.md:568) with a served ledger picker —
   a new `ScreenKind`/`MasterKind` plus `corpora/validate.py`'s `KNOWN_SCREENS`. **Left unbuilt rather than
   half-built, deliberately.**
3. **D49 (insightd has no auth) is still a DESIGN DECISION and must not be patched over.** insightd scopes
   drafts by the caller-supplied `company_id` and vite proxies it same-origin, so any company_id lists
   another tenant's supplier names, invoice numbers and amounts. The two candidate shapes: insightd
   validates a cored-minted bearer and derives the company from it, **or** insightd is never reachable from
   the browser and cored proxies it. ⛔ **Do not bolt a token on.**
4. **D40 was deliberately NOT given to a lane.** Non-existent challan sub-routes answer 403 rather than
   404/405, which makes "never built" indistinguishable from "not permitted". Changing it is a **default-deny
   decision in the PDP**, not a lane's call.
6. ⛔ **D22 — the cash-flow screen is a BUILD, and there is a cheap route that avoids most of it.** Make it
   a **`report:NAME` frame, NOT a `ScreenKind`.** It then inherits Alt+G Go To, Display More Reports, the
   report keymap, the button bar and F2 **for free**, and needs no Gateway entry at all. Files:
   `keystroke/types.ts` (`REPORT_NAMES`), `screens/printTarget.ts` (`REPORT_SLUGS` — omit it and `tsc`
   breaks; map it even though the print route 501s), `FrameOverlays.tsx` (`AS_AT_ONLY_REPORTS` — a
   projection is a position as at a date), `api/{client,rest,mock}.ts`, and a new
   `screens/reports/CashFlowScreen.tsx`. ✅ **NOTHING IS NEEDED SERVER-SIDE:** the route, `reportName()`
   and the PDP tokens `report:cash-flow` / `report:anomalies` **already exist** (`authz/vocabulary.go:457`).
   ⛔ Do NOT ship a Gateway entry alone — with no screen behind it, `canonicalReportName` falls through to
   the raw label and pushes the Unwired screen, which is the `Alt+G > Vouchers` defect built on purpose.
7. ⛔ **D49 — the recommendation is OPTION B: cored proxies insightd.** Reasoning, from the lane that
   traced it: insightd sets the RLS GUC **from the same path parameter it is scoping by**, so RLS is the
   first layer restated, not a second one. **Option A (insightd verifies a cored token) is bigger than it
   looks** — cored mints an ephemeral Ed25519 dev key and **publishes no JWKS** (`AUTH_JWKS_URL` is for an
   IdP cored *consumes*), and verifying a signature proves *who*, not *which company*, so insightd would
   need a SECOND COPY of cored's PDP decision in another language. This repo has been burned by exactly
   that shape. Option B costs plumbing: `/v1/companies/{id}/drafts…` routes, `routeAction`/`reportName`
   entries (default-deny makes this mandatory), a forwarding client, a base-URL change in
   `api/insightd.ts`, dropping the `/companies` proxy from `vite.config.ts`. **It touches `server.go` and
   `auth_middleware.go`, so it is A LANE OF ITS OWN.**
8. **`FeaturesScreen.tsx` (permanently single-owner) has TWO falsehoods, and fixing only the first leaves
   the second standing 70 lines below it.** (a) `:351-354` claims cash-flow/anomaly detection is state
   `"on"` with a note naming no door — the file's own vocabulary already has the right word, `"server"`,
   meaning *built with no key*. (b) `:422` says Cash Flow and the anomaly list "are all readable on
   screen" — **they are not readable at all; there is no screen.** The full replacement note is in the
   L11 report.

5. ⛔⛔ **`api/mock.ts` IS LOOSER THAN CORED, AND THREE SEPARATE LANES HIT IT.** This is no longer a tidiness
   item — it is a source of FALSE GREEN. L6's three red tests passed only because the mock accepts an empty
   ledger name and an unparseable `gst_rate_pct` that `server.go:2552` 400s on; L8 found the mock reproduces
   **D44 verbatim** and skips D6's party-group check and D69's due-date check; L13 found `createLedger` does
   not mirror the opening-breakup sum refusal. **A structural mock-vs-server gate is worth more than the
   individual fixes** — the same shape as the two gates the cutoff now has.

9. ⛔ **NEW DEFECT, FOUND BY L6, NOT IN §7.1 — REGISTER IT. An accounting-mode ALTER RE-POINTS THE BASE
   LEG.** `buildGstRequest` always `ensureLedger`s the orientation's canonical base ("Domestic Sales" /
   "Purchases"), so altering an invoice booked to **any other** income/expense head **silently moves it** —
   and mints that master on the way. The previous lane's own D41 control documents the behaviour as
   expected (`['Input CGST','Input IGST','Input SGST','Purchases']` on a voucher whose expense leg was
   "Consumables"), which is how it survived. **This is data loss on alter, not an orphan master.** The fix
   is to hydrate the base ledger id from the view — a change to what an alter POSTs, so it needs its own
   drive.

---

## 6. ⛔ THE RULES THAT DID NOT CHANGE, AND ONE THAT WAS RE-PROVED

`HANDOFF-DEFECT-BURNDOWN.md` §1 is still the law. Two earned their keep again in this wave:

- **RULE 1** — a mutation proof proves the TEST; only the BROWSER proves the FIX. **Nothing in §2 has been
  browser-driven. Not one row may be marked closed until it is.**
- **§2.3, AGENTS DIE** — this whole file is that rule being cashed in. A killed lane left a live sabotage,
  another left a company-leaking test, a third left `tsc` red. **After any wave: `git status`, the sabotage
  grep above, `go build`, `tsc`, and the FULL suites — a green targeted run is not enough.**

⚠️ **AND A PROCESS FINDING FROM THE RECOVERY, WORTH MORE THAN IT LOOKS.** Disjoint FILE ownership is not
disjoint BUILD ownership: an L4 recovery run was blocked mid-session by `go build` failing in
`internal/ledger/ledger.go` — another lane's in-flight edit, in a package L4 merely depends on. Go lanes
can stall each other even when no file is shared. Expect it, wait, re-run; do not diagnose it as your own.

> ### ⛔ AND THE LAST WORD
> Eleven lanes were launched and three came back. That is not an argument against fanning out — it is an
> argument for **collecting each lane's report before launching the next wave**, so an interruption costs
> one lane's work instead of eight. Fan out wide, but land the work in smaller batches than this one.
