# T1+T4 UI REACHABILITY — THE DATA MODEL

> The §3.2 design memo for `HANDOFF-CLOSING-THE-GAP.md` §3.1: **the two features with no
> button.** Printing (T1) and bank reconciliation (T4) are built, tested and reachable over
> HTTP only. This memo is the model for making them reachable by a keyboard.
>
> Measured against HEAD `c66949d`, migrations applied to `0046`.

---

## 0. WHAT EXISTS TODAY — measured, not remembered

Both server halves are complete. The client half is **absent**, not partial:

```
$ grep -rn "print\|reconcil" frontend/src/api/*.ts | grep -v types.ts
(nothing — no client method for either feature)
```

`frontend/src/keystroke/keymaps/tallyprime.ts` binds **no print key at all** — not to a real
action, and not to `unavailable`. That is a violation of that file's own stated COVERAGE
RULE (its header: *"a combo that does NOTHING is worse than one that says 'not in this
edition', because the user cannot tell a missing feature from a broken app"*). It is the only
documented TallyPrime combo missing from the table.

### 0.1 ⚠️ TODAY, Ctrl+P PRINTS THE WRONG THING — and that is worse than nothing

`AppShell`'s keydown handler returns early on an unresolved combo (`if (!action) return`),
and `useNavMachine` only calls `preventDefault()` for combos that reach the machine.
Ctrl+P resolves to no action, so **the browser's own print dialog opens** and prints a
screenshot of the DOM: a dark-background terminal emulation, no Rule 46 particulars, no
GSTIN block, no tax summary.

So this is not a missing feature. It is a **feature that produces a wrong document today**,
and the fix is not additive — binding Ctrl+P suppresses the browser dialog as a side effect,
because any `Ctrl+` chord that reaches `useNavMachine` is `preventDefault`ed.

### 0.2 ⚠️ THE CLIENT CANNOT TELL WHICH LEDGERS ARE BANK LEDGERS — this decides the design

This is the finding that would have broken the build, and it is the reason this memo exists
before any code.

A reconciliation screen must ask the operator *which bank account*. The obvious source is
`GET /ledgers`, which returns `group_name`. **That is the ledger's IMMEDIATE group name.**
But bank-ness is defined by **ltree ancestry**, in `internal/reporting/bankrecon.go:180`:

```sql
EXISTS (SELECT 1 FROM account_group bg
         WHERE bg.name IN ('Bank Accounts', 'Bank OD A/c')
           AND g.path <@ bg.path)
```

Verified against the live database:

```
$ psql -tAc "\d account_group"    -- path | ltree | not null
                                  -- parent_id -> account_group(id)
$ psql -tAc "select name, path from account_group where name in ('Bank Accounts','Bank OD A/c')"
Bank Accounts|g985b0e2f….sa29a2167…
Bank OD A/c  |gc7507866….s837e454a…
```

And `masters.CreateGroup` (`internal/masters/masters.go:92`) accepts a `ParentName`, so a
user **can** create `Current Accounts` under `Bank Accounts`. A ledger in it has
`group_name = "Current Accounts"`.

**Therefore a client-side filter `group_name IN ('Bank Accounts','Bank OD A/c')` silently
omits real bank accounts.** Silently: the operator sees a shorter list, not an error, and
concludes the app does not know about the account they need to reconcile.

**Decision: `is_bank` is derived ON THE SERVER and added to the ledger list response**, using
the *same* `path <@ bg.path` predicate, factored into one exported SQL fragment so the list
endpoint and `bankrecon.go` cannot drift. No migration — the data is already there; this is
a **NEEDS DERIVATION — new leaf**, not a schema change.

---

## 1. THE OUTPUT INVENTORY

### 1.1 The print action (T1)

Not a screen — an **action on the active screen**, resolving to one of two existing routes,
or to a **refusal that names its reason**. Fields it must assemble:

| Field | Source | Notes |
|---|---|---|
| company id | `useCompanyID()` | already in context |
| voucher class | active frame's `voucherType` | must be one of the 6 printable |
| voucher id | `LoadedVoucher` (alter) or the create-accept response | §2 |
| report slug | active frame's `reportName`, mapped | §1.3 |
| filename | `Content-Disposition` | server already sends it |

**The six printable classes** (`printingdb.docKindForClass`, `document.go:424`): `sales`,
`credit-note`, `debit-note`, `receipt`, `payment`, `delivery-note`.

**The seven that print nothing**, each of which must produce a *specific* refusal rather than
a generic one: `purchase` (the supplier's document, not ours), `contra`, `journal`,
`payroll`, `receipt-note` (goods arrived on the supplier's challan), `stock-journal` and
`physical-stock` (nothing leaves the business; nobody is handed anything).

### 1.2 What the print action must NOT do

Print is a **GET returning `application/pdf`**. `api/http.ts` parses every response as text
then JSON (`http.ts:96`) — a PDF through that path is corrupted into a lossy string. A
`getBlob` leaf is required. It must also **not** be routed through the JSON error path
blindly: a 501/403/404 on a print route returns a JSON `{"error": …}` body, so the blob
path has to sniff the content type and raise `ApiError` with the server's own message.

### 1.3 The report-name mapping

The client's `REPORT_NAMES` are display strings; the routes use slugs. All six client
reports are printable server-side:

| Client `ReportName` | Server slug | Prints? |
|---|---|---|
| Balance Sheet | `balance-sheet` | yes |
| Profit & Loss A/c | `profit-and-loss` | yes |
| Trial Balance | `trial-balance` | yes |
| Day Book | `day-book` | yes |
| Outstandings | `outstandings` | yes |
| Stock Summary | `stock-summary` | yes |
| *(Bank Reconciliation — §1.5)* | `bank-reconciliation` | **501, by design** |

Two further printable slugs (`payroll-register`, `payroll-payslip`) have **no client screen
at all** — payroll is server-only — so they are unreachable from the UI and this track does
not change that. Recorded so the count is not mistaken for a gap this track introduced.

### 1.4 The Bank Reconciliation screen (T4)

Tally's `F5` view on a bank ledger. One `GET /bank-reconciliation/{ledgerID}` supplies
everything; the handler was shaped for exactly this screen. Columns, straight from
`bankReconLineView`:

| Column | Wire field | Editable |
|---|---|---|
| Date | `voucher_date` | no |
| Particulars | `party`, `narration` | no |
| Vch No | `voucher_no` | no |
| Amount | `amount` + `dr_cr` | no |
| **Bank Date** | `bank_date` | **YES — the only editable cell** |
| Age | `age_days` | no (server-derived) |

And the four balance lines, which are the point of the statement: `balance_as_per_books`,
`uncleared_net`, `balance_as_per_bank`, `difference` (+ `balanced`).

**`broken_count` / `broken_by_alter` is not a detail and must be surfaced prominently.**
Non-zero means somebody altered a voucher *after* reconciling it; the row carries
`cleared_amount` / `cleared_dr_cr` as the evidence of what changed. The server comment says
a client must surface this; a screen that renders it as an ordinary row would defeat the one
invariant T4 built.

### 1.5 What this track deliberately does NOT build

**Printing the BRS.** Blocked on `HANDOFF-CLOSING-THE-GAP.md` §1.3 — an unanswered product
question (one statement per bank account via `?ledger_id=`, or one document covering all
three). The route already answers **501 honestly**. The screen's print key must therefore
**surface that 501**, not guess a layout. Pressing Ctrl+P on the reconciliation screen says
so out loud.

**Statement import / auto-matching** is T4 slice 2 (§3.2 of the handoff), not this track.

---

## 2. THE PROVENANCE TABLE

One row per field from §1. Every "stored where" checked against the live database or the
named source file — not from memory.

| Field | Required by | Stored / available where | Verdict |
|---|---|---|---|
| voucher id, ALTER mode | print route path | `LoadedVoucher.entry.voucher_id` | **OK** |
| voucher id, CREATE mode just saved | print route path | `VoucherResponse.voucher_id` (`api/types.ts:428`) — **returned and currently discarded**; the screen keeps only `voucher_no` (`VoucherScreen.tsx:451 setSavedNo`) | **NEEDS DERIVATION — new leaf** (§2.1) |
| voucher class | print route path | active frame `voucherType` | **OK** |
| is this class printable | the refusal | `docKindForClass`, server-side only | **NEEDS DERIVATION — new leaf** (client-side table, §4 I-3) |
| report slug | print route path | nowhere — client has display names only | **NEEDS DERIVATION — new leaf** (§1.3) |
| PDF bytes | the document | no blob path in `api/http.ts` | **NEEDS DERIVATION — new leaf** (§1.2) |
| **`is_bank` per ledger** | picking a bank account | **NOT in the `/ledgers` response**; `group_name` is the IMMEDIATE group and bank-ness is ltree ancestry | **SERVER CHANGE — new derived field** (§0.2) |
| bank account no. / IFSC | display on the statement | *nowhere* — `t4-model.md` §2 deferred it | **DEFERRED to slice 2** — must NOT be shown as blank |
| every BRS line + balance field | the statement | `GET /bank-reconciliation/{ledgerID}` | **OK** |
| `broken_by_alter`, `cleared_amount` | the broken-reconciliation warning | same response | **OK** |
| bank ledger list | the picker | `GET /ledgers` + new `is_bank` | **OK once §0.2 lands** |

**No migration is required by this track.** That is a finding worth stating plainly: both
features are pure client work *plus one derived server field*, which is why §3.1 calls this
the highest value-per-hour work in the file.

### 2.1 The create-mode print slot

`§3.1`'s exact complaint is *"an operator cannot print the invoice from the screen they keyed
it on."* Requiring an alteration (the `editLog` precedent, `navMachine.ts:609`) would leave
that complaint standing.

The response already carries `voucher_id`. So: **on a successful create the screen records
the saved voucher's id and class**, and Ctrl+P prints it. The slot is cleared when the frame
is reset or a new voucher is started, so Ctrl+P can never print a stale document.

This keeps the machine pure — no voucher id travels through `navMachine`, exactly as
`editLog` established.

---

## 3. UNITS AND SCALE

Nothing in this track computes money. It **transports and displays** it, and the rule is
that it must not touch it:

| Value | Unit / scale | Rule here |
|---|---|---|
| `amount`, `cleared_amount`, all four balances | fixed-4 decimal **string** | **Rendered as received.** No `parseFloat`, no arithmetic. `screens/reports/money.ts` is the existing formatter and is reused. |
| `difference` | fixed-4 string | Must be **exactly** `"0.0000"` to read as balanced — and `balanced` is the server's own verdict, so the screen renders that boolean rather than comparing strings itself. |
| `age_days` | **integer days**, server-derived | Never recomputed client-side; a clock-skew disagreement between browser and server would show two different ages for one row. |
| `bank_date`, `voucher_date`, `as_on` | ISO `yyyy-mm-dd`, **date not instant** | Sent verbatim. `reconciled_at` is the instant; `bank_date` is not, and the two must never be conflated. |
| `leg_no` | **ordinal**, integer ≥ 0 | Path parameter; non-zero only when one voucher touches the same bank twice. |
| PDF | **bytes** | `Blob`, never a string (§1.2). |

The only *new* numeric identifier this track introduces is `AgeDays`, already named with its
unit on the wire, and it is carried through unchanged.

---

## 4. INVARIANTS, EACH WITH THE TEST THAT PINS IT

| # | Invariant | Enforcement |
|---|---|---|
| **I-1** | **Every documented TallyPrime combo resolves to an action** — a real one or an explicit `unavailable`. Ctrl+P and Alt+P included. | Extend the existing coverage test in `keystroke/buttons.test.ts`; assert `resolveAction(keymap, screen, "Ctrl+P")` is defined on **every** `ScreenKind`. **Mutation proof: delete the Ctrl+P binding, watch it fail.** |
| **I-2** | **Ctrl+P never opens the browser print dialog.** | Test asserts `preventDefault` was called for Ctrl+P (the shell's `!action` early return is what breaks this). Pins §0.1 — the regression that makes the app print a screenshot. |
| **I-3** | **The client's printable-class table equals the server's.** A class the client offers to print but the server refuses is a 404 the operator cannot explain. | A test enumerating the client table against the 6 names, plus a **Go-side test asserting `docKindForClass` accepts exactly those 6 and refuses the other 7** — two implementations that must agree, from both sides of the boundary. |
| **I-4** | **Print refuses with a class-specific reason**, never a generic failure. Purchase says *the supplier's document*; physical-stock says *nothing is handed to anybody*. | One test per non-printable class asserting the message names the class. |
| **I-5** | **A create-mode voucher with nothing saved cannot print**, and says why. | Test: fresh frame + Ctrl+P → refusal, and **no HTTP call is made** (a fetch spy with zero calls; a refusal that still hits the network is a 404 in the log). |
| **I-6** | **The stale-print guard**: after the frame resets or a new voucher starts, the saved-voucher slot is empty. | Test: save, reset, Ctrl+P → refusal, not the previous document. **Mutation proof: remove the clear, watch it print the stale voucher.** |
| **I-7** | **`broken_count > 0` is surfaced before the rows.** | Test with a broken line asserting the warning renders *and* that `cleared_amount` appears as evidence. |
| **I-8** | **A bank ledger under a user sub-group of `Bank Accounts` appears in the picker.** This is §0.2's failure, and it is the one that would have shipped. | Go test: create group `Current Accounts` with `ParentName: "Bank Accounts"`, a ledger in it, assert `is_bank = true` in the list response. **Then the mutation proof: change the predicate to match on `group_name` and watch this test fail.** |
| **I-9** | **`is_bank` uses the same predicate as `bankrecon.go`.** | One exported SQL fragment, referenced by both call sites. A test asserting that a ledger the list marks `is_bank` is one the BRS endpoint accepts (and vice versa) — pinned **from both sides**, so a drift in either breaks it. |
| **I-10** | **The BRS print key reports the server's 501; it never renders a guessed layout.** | Test asserting the 501 message reaches the status line. Pins §1.5 against a future well-meaning "just print something". |
| **I-11** | **Bank account number / IFSC are absent, not blank.** | The screen has no such column until slice 2 adds the columns *and* the data. A blank labelled field reads as "the bank has no IFSC". |
| **I-12** | **Alt+P discloses that the print OPTIONS menu is not built** (§6.1). Ctrl+P and Alt+P are different Tally actions; binding both to a silent direct print would let an operator think they chose options they never saw. | Test asserting Alt+P's status message names the missing options menu, and that Ctrl+P's does **not** (they must not converge on one message). |

---

## 5. THE EDGE-CASE LEDGER

Worked against `HANDOFF-T1-PRINTING.md` §3.3.

| Edge case | Behaviour | Pinned by |
|---|---|---|
| Print a voucher the level may not print | Server 403 → the server's message in the status line, not "print failed" | I-4's sibling test |
| Print a voucher id whose stored class differs from the path | Server answers **404 with a class-neutral body** (deliberate; `handler_print.go`'s escalation refusal). The client must **pass that message through unchanged** — a client that "helpfully" rewrote it to "wrong class" would leak exactly what the 404 protects | dedicated test |
| Data Entry prints a report | Seeds **zero** `allow print report:NAME` (handoff §5 item 7) — so *every* report print is refused for that level. Surfaced as the server's refusal; **this track does not change the ACL**, it only stops the refusal being invisible | control test: same token gets 200 on a permitted print |
| A deleted (tombstoned) voucher | 404, same body as a non-existent one — never a fresh copy of a cancelled invoice | existing server test; client passes through |
| Print from `report-detail` (a drill-down) | Prints the **parent report**, since that is what the route offers. Stated in the status line so the operator is not surprised | test |
| Non-bank ledger reaches the BRS route | Server 400 naming the ledger. The picker should make it unreachable, but the refusal stays as defence in depth | existing server test |
| Bank OD A/c (a **liability**) | The sign trap of `t4-model.md` §3.1. The screen renders the server's signed values and **derives no sign of its own** | I-3-style render test on an OD ledger |
| `bank_date` before the voucher date | Migration 0045's trigger refuses; the message names the field and reaches the status line | test |
| Clearing an already-cleared leg | Idempotent 200 by design — a double-click is not an error | existing server test |
| Two legs on the same bank ledger in one voucher | `leg_no` disambiguates; both rows show, independently tickable | test with a bank-charge-netted transfer |
| Empty statement (nothing to reconcile) | An explicit "nothing outstanding" state, **not** a blank grid | test |
| A company with no bank ledger at all | The picker says so plainly and does not open an empty screen | test |
| Print while offline | The offline plane is sync-only; a print is a live GET. Fails with a stated transport error rather than a silent nothing | test |
| Concurrent alter during reconciliation | Exactly what `broken_by_alter` reports — surfaced, never absorbed | I-7 |

---

## 6. THE SHAPE THAT FOLLOWS

New vocabulary (each an explicit, testable addition):

- `ScreenKind` += `"bank-reconciliation"` — and `COMPANY_SCOPED_SCREENS` in `AppShell.tsx`
  must gain it, or `useCompanyID()` throws on entry.
- `KeymapAction` += `{ kind: "print" }`, `{ kind: "bankReconciliation" }`.
- `ScreenActionKind` += `"print"` — print is a screen-owned side effect (it needs the client
  and the live frame), exactly like `export`.
- `actionLabel` cases for both, or the F1 Help screen renders an empty row.

### 6.1 The print combo — verified against RESEARCH.md, not assumed

`RESEARCH.md:2165` is the authoritative row (columns: *Feature | ERP 9 | TallyPrime |
status*):

```
| **Print** | Alt+P | Ctrl+P (voucher) / Alt+P (print menu) | partial |
```

Corroborated twice more for TallyPrime: `RESEARCH.md:361` and `:415` both record the
top-menu **Alt+P = Print**.

So in the profile we actually ship (`tallyprime`), **both combos are live bindings and they
are NOT the same action**:

| Combo | TallyPrime meaning | What we bind it to |
|---|---|---|
| **Ctrl+P** | print **this voucher** | `{ kind: "print" }` — the real thing |
| **Alt+P** | open the **print MENU** (options, copies, print-before-save rules) | `{ kind: "print" }`, but its status line must say the print **options menu** is not built and that it printed with defaults |

An earlier draft of this memo justified Alt+P as "ERP 9 muscle memory". **That was wrong** —
it is a documented TallyPrime binding in its own right, and describing it as legacy would
have led to binding it as a courtesy alias with no note, which in turn would have let an
operator believe they had reached a print-options screen and silently chosen defaults.
Recorded because rule 0.2 applies to keystrokes exactly as it applies to tax rates: this
product's moat is that the keys mean what Tally means, and "I remembered the combo" is not
verification.

Bindings: **Ctrl+P** and **Alt+P** → `print`, distinguished as tabled above.
Bank reconciliation is reached two ways, mirroring how every other screen is reachable:
a **Gateway entry** (`"Bank Reconciliation"`, checked *before* the `canonicalReportName`
fallback in `navMachine.ts:483` — the ordering trap Bill Inbox and Query both document), and
**F5 on a bank ledger's report**. Note F5 is already `ledgerWise` on report screens
(`tallyprime.ts:161`); the override is scoped so it does not break the Trial Balance toggle.

**Four registration points** (handoff §4.2) — this track adds **no new server resource**
(`bank-reconciliation` was registered by 0046 and print by T1), so §4.2 does not apply.
Confirmed by reading `authz.KnownReportNames:163` and the print routes at `server.go:772`.
**§4.3 does not apply either: no ACL migration.** Both stated so a reviewer can check the
claim rather than assume it.

---

## 7. ACCEPTANCE CHECKLIST

- [ ] Ctrl+P never opens the browser dialog (I-2) — the §0.1 regression
- [ ] Both mutation proofs run and restored byte-identically (`shasum -a 256`): I-1, I-6, I-8
- [ ] I-8's sub-group bank ledger test written **before** the `is_bank` field
- [ ] I-9 pinned from both sides
- [ ] Every refusal message names its reason (I-4) — no generic "failed"
- [ ] Ctrl+P and Alt+P are bound as **different** messages per §6.1 / I-12, and the combos are
      cited to `RESEARCH.md:2165` rather than recalled
- [ ] `FeaturesScreen.tsx` updated **in the same commit** (handoff §2.1): the three rows that
      currently say "NOT REACHABLE FROM THIS APP" / "NO PRINT BUTTON" / "BUILT, WITH NO KEY"
      become accurate, and the BRS-print 501 and the deferred IFSC stay disclosed
- [ ] Full backend suite `-v`, `grep -c -- '--- SKIP'` = 0, bracketed by a live `psql` query
- [ ] Live gates on **freshly rebuilt** servers, aged against the commit
