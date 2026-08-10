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

## ⛔ THE THING THAT WILL BITE YOU FIRST

A killed lane left a **live mutation-proof sabotage** in `internal/masters/tds_challan.go` — a no-op loop
where the allocation write belongs, marked `MUTATION P1: the route answers 200 and writes nothing`. The
original code was never committed, **so git cannot restore it**. It has been replaced with an explicit
**fail-closed refusal** naming itself, because falling through would have re-shipped register row D1's own
defect (a success status over a write that never happened) by accident instead of by decision.

> ⛔ **BEFORE YOU BELIEVE ANY PART OF THIS TREE, RUN:**
> ```bash
> grep -rn "MUTATION\|SABOTAGE" backend/internal frontend/src insightd/insightd \
>   | grep -viE "mutation proof|mutation-proof|mutation_proof|a mutation|the mutation|mutation test"
> ```
> One hit is expected and is documented at `tds_challan.go` (the comment block, not live code). **Any other
> hit is live sabotage from a killed lane. Treat it as a P0.**

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
| backend `go test ./...` | ⛔ **FAIL** — `masters`, `reporting`, `server`, `testhygiene` |
| frontend `pnpm vitest run` | ⛔ **12 failed / 1211 passed**, 4 files |
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
| **L4** TDS | D1–D4 | the allocate-later route, its wire tests, `api/types.ts` changes. **1,257 lines in `masters` alone** | ⛔ **THE SABOTAGE IS HERE.** Step 4 of the allocation transaction is a fail-closed stub. **Write the loop that verifies each allocation against the live deduction/collection and writes it.** 4 `masters` + 4 `server` tests are red and they are L4's OWN tests — they describe what to build. `TestAllocatingLaterMakesTheUnallocatedBalanceFALL` is the row's own oracle |
| **L1r** bill-wise | D10, D11, D32 | a redesign of the unnamed-reference stamp in `ledger.go`, `bill_ref_naming_test.go`, `outstanding_ref_collision_test.go` | ⛔ **It broke two EXISTING tests it has not reconciled:** `TestUnnamedNewRefIsStampedWithTheAllocatedVoucherNo` and `TestReceiptAgainstTheStampedRefSettlesIt`, plus 3 in `server`. **Decide whether those tests pin the old defect (trap #7) or whether the redesign is wrong, and say which in the commit.** ⚠️ Read the blocker first: `BillAllocationPicker` has a name field for AGAINST **only**, so a NEW reference cannot be named from the keyboard — a "refuse the duplicate" fix is unreachable without that input |
| **L8** orders + BOM | D6, D7, D44, D68–D71 | party-group restriction at both ends, the fulfilment sort, overdue defaults | ⛔ **5 `server` order tests + 4 `OrderRegisterScreen` tests red.** The party restriction changed what the register offers and the round-trip tests have not caught up. Preserve the invariant **an order moves no stock** |
| **L9** users + security | D25, D55–D58, D73, D74 | `auth_revocation.go` (**WIRED** — `auth_middleware.go:98` calls `sessionRevoked`), migration **0074**, unlock route, `MembersScreen` lock state | ⛔ **4 `auth.test.tsx` + 1 `webauthn.test.tsx` red** — session revocation changed login/session behaviour and those tests describe the old contract. ⛔ **AND A HYGIENE FAILURE:** `auth_session_revocation_test.go:237` creates a company at line 262 and never tears it down (`testhygiene` is red). Add `testdb.Cleanup(t, companyID)`. ⚠️ **Lockout is keyed on SUBJECT: never submit a wrong password for `browser-dev-owner`** |
| **L6** voucher form | D39, D41, D59, D60 | the master-before-write gate, `VoucherScreen.masterGate.test.tsx` | ⛔ **3 `wiring.test.tsx` tests red** (`Ctrl+A … calls postSalesVoucher`, purchase, and the alter path) — the Ctrl+A path was mid-rewrite. **This is the single-owner file; nobody else may touch it** |
| **L11** AI-native | D16, D18, D19, D22, D23, D49, D72 | `insightd/redaction/persistence.py`, forecast disclosure, tenant context | ⛔ **`tsc` is red here** — `BillInboxScreen.test.tsx` builds a company without `base_currency` (required at HEAD, not a new contract). insightd's own suite is GREEN. ⛔ **D49 was framed as a DECISION, not a patch — see §5** |
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
5. ⚠️ **`api/mock.ts` and the real client can drift** — see §4 item 6. Worth a structural gate, like the two
   the cutoff now has.

---

## 6. ⛔ THE RULES THAT DID NOT CHANGE, AND ONE THAT WAS RE-PROVED

`HANDOFF-DEFECT-BURNDOWN.md` §1 is still the law. Two earned their keep again in this wave:

- **RULE 1** — a mutation proof proves the TEST; only the BROWSER proves the FIX. **Nothing in §2 has been
  browser-driven. Not one row may be marked closed until it is.**
- **§2.3, AGENTS DIE** — this whole file is that rule being cashed in. A killed lane left a live sabotage,
  another left a company-leaking test, a third left `tsc` red. **After any wave: `git status`, the sabotage
  grep above, `go build`, `tsc`, and the FULL suites — a green targeted run is not enough.**

> ### ⛔ AND THE LAST WORD
> Eleven lanes were launched and three came back. That is not an argument against fanning out — it is an
> argument for **collecting each lane's report before launching the next wave**, so an interruption costs
> one lane's work instead of eight. Fan out wide, but land the work in smaller batches than this one.
