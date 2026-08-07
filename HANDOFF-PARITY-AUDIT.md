# THE PARITY AUDIT — DRIVE ALL 47 ROWS, THEN BUILD THE 13

> ## ⛔ THIS IS A WORKSHEET, NOT A RIVAL HANDOFF. `HANDOFF.md` IS STILL THE LIVE ONE.
>
> §11 of `HANDOFF.md` records eleven SPENT `HANDOFF-*.md` files and the cost of having them around:
> *"a handoff nobody can trust costs a session to re-verify."* This file exists only because a
> 47-row checklist does not belong inside a 2,400-line narrative. It is owned by `HANDOFF.md` §1.11,
> which points here.
>
> **WHEN THE AUDIT IS DONE: fold the findings into `HANDOFF.md` §5.5's log and DELETE THIS FILE.**
> Do not let it become the twelfth.

---

## 0. THE ANSWER TO THE FIRST QUESTION: THIS IS NOT ONE SESSION

Stated up front so nobody plans a session that cannot happen. **The ask is "audit 34, then build 13,
in one session." The audit alone is 1–2 sessions and the 13 builds are roughly 6–10.** That is not
pessimism; it is this repository's own measured rate:

| Evidence | Source |
|---|---|
| "Plan for roughly one track per session — the last three delivered exactly one each and all were full" | `HANDOFF.md` §3.1 |
| "**A track needing a SCREEN is a session.**" | §1.12 |
| "the frontend half of a track is not smaller than the backend half" — T3's frontend was a full session | §3.1 |
| Driving **ONE** new screen end to end found **THREE** defects, two in code that session never touched | §5.5 |
| The item this file's ancestor called "small, certain, needs no architectural decision" **took a whole session** | §3.1 |

⛔ **AND THE AUDIT'S COST IS NOT THE DRIVING — IT IS WHAT THE DRIVING FINDS.** Every session that has
driven `on` rows has found at least one lying, and the fix is then real work with its own tests and
mutation proofs. Last session found four. The session before found three. **Budget for the fixes, or
the audit will stop at the first one.**

**So do it in this order, and stop wherever the session ends — every phase is independently valuable:**

1. **PHASE A — the audit** (§2–§4). Produces the *true* number. Highest value per minute in the repo.
2. **PHASE B — fix what A finds** (inline, as found). Do not defer: a defect found and not fixed is a
   defect that will be re-found from scratch.
3. **PHASE C — build the 13** (§5), in the stated order. Expect several sessions.
4. **PHASE D — the big three** (§6): payroll, offline sync, backup/restore. Explicitly a later session.

> ### ⚠️ IF YOU ONLY GET THROUGH PHASE A, THAT IS A GOOD SESSION.
> The number "34 reachable" is currently an **upper bound with no measurement behind it**. Converting
> it to a measured number, with the liars named, is worth more than two half-built features.

---

## 1. WHAT WE ARE MEASURING, AND WHY THE CURRENT NUMBER IS A GUESS

Re-measured **2026-08-07** with §1's `python3` pairing method (47 `state:` lines, 47 adjacent pairs,
0 unpaired — so nothing was missed):

| State | Count | Meaning |
|---|---|---|
| `on` | 32 | claims to be reachable from the keyboard |
| `always` | 2 | structural, cannot be switched off |
| `server` | 2 | built, but no key reaches it |
| `off` | 11 | not built |
| **TOTAL** | **47** | |

⛔ **`on` IS AN UPPER BOUND, NOT A MEASUREMENT — AND THIS IS THE WHOLE POINT OF THE EXERCISE.**
Rows caught lying, every one by driving and never by reading:

| Row | What `on` was hiding |
|---|---|
| Journal (F7) | the form had **ONE input**; a Journal could not be keyed at all |
| Day Book drill | opened a **Sales** frame for every class; saving re-classed the document |
| Receipt after a Sale | posted `DR <the sale's customer>` instead of a bank |
| ALTER of a GST invoice | **stripped the tax** — an 11,800 invoice became 10,000 and left GSTR-1 |
| `Alt+E` export | bound, live, and **losing paisa** (exported display strings) |
| **F9 Purchase, Credit Note, Debit Note** | **posted the money BACKWARDS**, with OUTPUT tax where INPUT belongs. Server answered **201** |

**Roughly 20 of the 32 `on` rows have still never been driven end to end.**

---

## 2. THE METHOD — AND THE ORACLE RULE IS THE PART THAT MATTERS

For each row:

1. **Name the exact keystroke sequence an operator uses**, from the Gateway.
2. **DO IT in a browser against real cored.** Not a test. The app.
3. **Read the result back from the place the money actually lands** — see the oracle column in §3.
4. **Write the row into §7's log** with what you saw, pass or fail.

> ### ⛔⛔ AN AUDIT INHERITS THE BLIND SPOT OF ITS ORACLE. THIS HAS COST THIS REPO THREE TIMES.
>
> - §4.3's read-back audit cleared `bill_allocation` **on half a check** — an ALTER was destroying
>   references.
> - The same audit declared `stock_movement` SAFE on a **quantity-only** oracle. Mutating the GET's
>   rate to `"0.0000"` left it green while **1,000 of stock value** was written off by a narration
>   edit — and closing stock feeds the **Balance Sheet**.
> - This session: a test asserting a purchase is "the MIRROR of a sale", which reasons explicitly
>   about orientation, still pinned **`Output` tax on a purchase**.
>
> **So: never assert a total. Totals prove nothing about direction — a reversed voucher balances to
> the same paisa. Assert SIDES and LEDGER NAMES, and read VALUE as well as quantity.**

### The three oracles, in order of trustworthiness

| Oracle | Use when |
|---|---|
| **`psql` on `posting` / `stock_movement`, joined to `ledger.name`** | anything that moves money or goods. This is the only oracle that cannot be flattered by the client |
| **A REPORT the row feeds** (Balance Sheet, Outstandings, GSTR-1, Stock Summary) | to prove the entry reaches where a business reads it |
| **The screen re-rendering** | read-only screens only — mostly self-evidencing |

**The money oracle, ready to paste:**

```sql
select vv.voucher_no, vv.payload->>'class' as class, l.name as ledger, p.dr_cr, p.amount
from posting p
join voucher_version vv on vv.id = p.voucher_version_id
join ledger l on l.id = p.ledger_id
where l.company_id = '<COMPANY>'
order by vv.created_at desc, p.amount desc limit 12;
```

**The goods oracle — ⛔ `closing_value`, NOT just `closing_qty`:**

```sql
select si.name, sm.direction, sm.qty, sm.rate, sm.amount
from stock_movement sm join stock_item si on si.id = sm.stock_item_id
where si.company_id = '<COMPANY>' order by sm.created_at desc limit 12;
```

### ⛔ Rules for the pass itself

- **ONE signed-in browser, ONE virtual authenticator** (§9.7's CDP recipe). The browser pass is the
  orchestrator's; read-only code recon can be fanned out to subagents, the driving cannot.
- **⛔ RE-SEED BEFORE AND AFTER.** `pnpm seed:demo` — the demo company **is** `verify:demo`'s fixture,
  and manually-keyed vouchers make that gate fail for reasons that have nothing to do with the code.
  This cost time on 2026-08-07 (a blank HSN produced an empty GSTR-1 `hsn_sc` row → 32/33).
- **Drive MORE THAN ONE TYPE when a screen is parameterised by type.** "The Day Book works" was
  established with a Sales voucher and was false for every other class. **The default is always right
  for exactly one of them.**
- **A row covering a CLASS is not proved by one member.** Row 26 covers *four* documents; row 1
  covers *six*.

---

## 3. ⛔ THE CHECKLIST — 34 REACHABLE ROWS

`STATUS` legend: `—` not started · `✅` driven clean · `⛔` defect found (log it) · `~` partly driven.

⚠️ **"PREV" MEANS DRIVEN IN AN EARLIER SESSION, NOT "SKIP IT".** Several rows on that list were later
found lying anyway (the Day Book was "known-good" for two sessions). Re-drive anything cheap; spend
the time on the `—` rows first.

### Accounting

| # | Row | Keystrokes | ⛔ ORACLE — what to read back | Status |
|---|---|---|---|---|
| 1 | Double-entry vouchers (6 classes) | `F4` `F5` `F6` `F7` `F8` `F9`, each → key → `Ctrl+A` | `posting`: sides + ledger NAMES, **per class**. Six documents, not one | PREV (F5/F6/F4 + F7) |
| 2 | Edit Log / audit trail `always` | Day Book → `Enter` → `Alt+L` | the version list shows v1 **and** v2 with who/when + a before/after diff | — |
| 3 | Bill-wise details (4 ref types) | `F8` sale to a bill-by-bill party → `F6` receipt → settle | Outstandings **reconciles to the Balance Sheet to the paisa**. Drive **all four** ref types | PREV (New + Agst) |
| 4 | Credit / Debit Notes | `Alt+F6` / `Alt+F5` → key → `Ctrl+A` | `posting` sides. **CN verified 2026-08-07; ⛔ DEBIT NOTE STILL NEVER DRIVEN** | ~ |
| 5 | Opening balances (with bill-wise breakup) | `Alt+C` create ledger with opening + bills | the opening bill appears in Outstandings and the Balance Sheet opens balanced | — |
| 6 | Single-entry mode (Payment/Receipt/Contra) | `F5`/`F6`/`F4`, `Ctrl+H` → Single Entry | `posting`: one primary leg + N counter-legs, correct sides | PREV |
| 7 | Voucher numbering series | key 2 of a class; cross a FY boundary | `voucher_number_counter`: per (class, FY), monotonic, **restarts next FY** | PREV |
| 8 | Cost centres + both reports | `Alt+G` → Cost Centres; both summaries | per-(posting, **CATEGORY**) sums — ⛔ **NOT per posting**; axes are parallel (§8.1) | PREV |
| 9 | Cost allocation during entry | `F5` → allocate across 2 axes | `posting_cost_allocation`: **both** axes complete and independent | PREV |
| 11 | Bank reconciliation | `Alt+G` → Bank Reconciliation | a marked line changes the reconciled balance and survives a reload | — |

### Statutory

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 14 | GST (derivation, GSTR-1, GSTR-3B) | `F8` intra **and** inter-state | CGST/SGST vs IGST split; the supply appears in **GSTR-1 b2b AND the table-12 HSN summary** | PREV (partly) |
| 15 | e-Invoice / IRN | `Alt+I` → pick → `Ctrl+A` | IRN in `voucher_external_reference`; re-post returns the SAME IRN; ALTER+DELETE both 409 | ✅ 2026-08-06 |
| 16 | e-Way Bill | `Alt+Z` → state the movement → `Ctrl+A` | `e_way_bill` row; disclosures rendered in full; ⛔ **check the step-up wipe (§4 lead 1)** | PREV |
| 17 | TDS / TCS | `Alt+G` → TDS Challans → record + allocate | `tds_deduction` / `tds_challan_allocation`; the challan's unallocated balance falls | — |

### Inventory

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 20 | Stock items + units (masters) | `Alt+C` → Stock Item | the master persists **with its unit** — a blank unit is what broke every e-invoice line | PREV |
| 21 | Item Invoice (`Ctrl+H`) | `F8`/`F9` → Ctrl+H → Item Invoice | `stock_movement` **qty AND value**; server-derived postings | PREV |
| 22 | Godowns / batches | key a line with a godown **and** a batch | movement lands on the **right position** — ⛔ see §4 lead 3 (the NULL-godown default) | — |
| 23 | Stock Summary (qty AND value) | `Alt+G` → Stock Summary | ⛔ **`closing_value`, not just `closing_qty`** — this exact oracle gap shipped once | PREV (qty only) |
| 24 | Negative stock `always` | sell more than you hold | refused, or allowed per `allow_negative_stock` — and the flag actually governs it | — |
| 26 | Inventory vouchers (**FOUR** classes) | `Alt+F7` `Alt+F8` `Alt+F9` `Alt+F10` | ⛔ **the two `Alt+F7`/`Alt+F10` leads in §4 are the highest-value unverified items in this file** | ~ (F8/F9 only) |
| 27 | Orders + BOM | `Ctrl+F8` `Ctrl+F9` `Alt+X` | order book Ordered/Delivered/Outstanding; ⛔ an order **moves no stock** | PREV |
| 29 | Closing stock on the Balance Sheet | Balance Sheet at 2 dates | closing stock **moves the sheet** and the sheet still balances | PREV |

### AI-native

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 30 | Bill Inbox (OCR → draft) | Gateway → Bill Inbox → accept a draft | the accepted draft becomes a real voucher with the OCR'd figures; PII stays redacted | — |
| 31 | Ask your books (NL query) | Gateway → Query | the answer matches the same figure read from a report; ⛔ a cross-tenant probe is refused | — |
| 32 | Cash-flow projection / anomalies | `Alt+G` → the forecast report | it discloses that it is a projection, and the basis is stated | — |

### Users and security

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 33 | Multiple users + security levels | `Alt+G` → Users & Roles → invite | membership appears; the invitee is PENDING until claimed | — |
| 34 | Custom levels (Disallow/Allow) | edit a level, save, act as it | ⛔ **sign in AS that level and be REFUSED.** Reading the rules back is not the test | — |
| 35 | Switch company (`F3`) | `F3` → pick | the whole session re-scopes; no row of the old company leaks | — |
| 36 | WebAuthn step-up for writes | any write | 403 `STEPUP_REQUIRED` → ceremony → retry succeeds. ⛔ **§4 lead 1 lives here** | PREV |
| 37 | Back-dated entry cutoff | key a voucher before the cutoff | refused **by name**, and a voucher after it still posts (control) | — |
| 38 | Password login + lockout | N bad passwords | locked out, and it **unlocks** as configured | — |

### Printing and output

| # | Row | Keystrokes | ⛔ ORACLE | Status |
|---|---|---|---|---|
| 40 | Printed documents (PDF) | `Ctrl+P` on each of the 7 document classes | a real PDF (`%PDF`…`%%EOF`) **naming itself** — drive more than one class | PREV (sales) |
| 42 | Printed statements (PDF) | `Ctrl+P` on each report | as above, per report | PREV (TB) |
| 43 | Export a report (`Alt+E`) | `Alt+E` on several reports | ⛔ **WIRE precision, not display strings** — this row lost paisa once. CSV-injection guarded | PREV |

---

## 4. ⛔⛔ THE LEADS — START HERE, THESE ARE PREDICTED DEFECTS WITH THE CODE PATH ALREADY TRACED

Read-only recon (2026-08-06) traced these to specific lines. **None is verified.** Each is a
concrete, falsifiable prediction — confirm or kill it, and log which.

### LEAD 1 — the step-up wipes the screen (⛔ CONFIRMED ON ONE SCREEN, UNFIXED ON ANOTHER)

`App.tsx`'s `resolvedClient` memo depends on `session`; a WebAuthn step-up **replaces the session
token**, so every screen effect keyed on `client` re-runs the instant the ceremony completes.

```
POST -> 403 STEPUP -> passkey -> POST -> 422 -> the load effect's reset WIPED the refusal
```

The operator pressed the key and **saw nothing happen**. Fixed on `EInvoiceScreen`;
⛔ **`EWayBillScreen.tsx` has the identical shape and is NOT fixed** — same unconditional reset, same
`client` dep. On the e-way bill the loss is a `bill`; on the e-invoice it was the **ack number**,
which the schema stores nowhere. **Reproduces on the FIRST write of a session — the only one most
operators ever do.** Check every screen that shows a result after a write.

### LEAD 2 — Stock Journal destroys stock VALUE on a blank Rate

`isKeyedStockLine` needs only `qty > 0`, so a row with an empty Rate is "keyed". `parseFixed4("")`
returns `0n`, so the wire carries `rate: "0.0000"` — a **stated** zero. Server-side
`rateUnstated := l.Rate == nil`, so the `RateUnstated` backstop (whose own comment warns about
exactly this) **does not fire**. The DB CHECK is `rate >= 0`.

**Predicted:** company-wide closing stock **value** drops by the full average cost of the transferred
goods, **quantity unchanged** — so a quantity-only audit passes. ⛔ **Closing stock feeds the Balance
Sheet.** Watch the wire for `rate: "0.0000"` on the IN leg, then re-read Closing Stock **value**.

### LEAD 3 — Physical Stock counts against the WRONG position

The Godown select defaults to `— not tracked —` (NULL) while all seeded stock sits at Main Location.
Counting "13" without touching it differences 13 against a book of **0** on the NULL position →
a **surplus Inward of 13**, inflating closing stock qty *and* value, leaving Main Location untouched.

⚠️ **The symptom is deceptive:** quantity rises by exactly what was counted, which reads as "it
worked."

### LEAD 4 — the Edit Log is real, but three things around it are not

The screen genuinely renders `voucher_version` with hash-chain verification (tri-state, honest).
But: `screenTitle` has **no `edit-log` case** → the chrome reads **"Tally"**; `hintsFor` has none
either → it advertises *"Enter: Open"* on a screen with nothing to open; and the `voucher` hints
**never mention `Alt+L`**, on the only screen where `Alt+L` exists — so it is undiscoverable.
`"edit-log"` is also **absent from `COMPANY_SCOPED_SCREENS`** (latent, not currently reachable).

⚠️ **And check `Alt+L` with a real keypress:** `comboFromEvent` keys off `ev.key`, and macOS
Option+L natively emits `¬` → `Alt+¬`, missing the binding. **Playwright will pass where a human
fails.** Same doubt applies to `Alt+F5`/`Alt+F6` and the F-keys (media keys).

### LEAD 5 — a blank HSN silently produces an empty GSTR-1 `hsn_sc` row

Found by accident 2026-08-07. An accounting-mode GST voucher keyed with HSN blank yields an
`hsn_sc` row with an **empty HSN code**, on a mandatory table. Nothing on the form requires it and
nothing refuses it. The item path is immune (HSN comes off the stock master).

### LEAD 6 — discoverability holes that make working features invisible

`VOUCHER_BAR` omits **`Alt+F5`/`Alt+F6`**, and `hintsFor`'s `voucher` case does not name them either.
Both keys work. By this repo's own stated rule — *"an operator reads the hint strip as the list of
what works here"* — they are unreachable in practice. `buttons.test.ts` is the guard that should
have caught it.

---

## 5. PHASE C — BUILDING THE 13, IN ORDER

Ordered by **how much of a real business's day is blocked**, which is this repo's standing criterion.

| # | Row | Size | The one thing not to get wrong |
|---|---|---|---|
| 1 | **Backup / restore / split company** (46) | 1–2 sessions | Must be **tenant-exact**. ⛔ Reuse `testdb`'s delete-plan census — it already knows every tenant table and is anti-rot by construction |
| 2 | **FIFO / LIFO / batch valuation** (25) | 1 session | ⛔ **SERIALISE with #3** — both touch the movement primitive. Closing stock feeds the **Balance Sheet**, so this MOVES THE SHEET: re-pin `verify:demo` deliberately and explain the change. A new arm in `ValuerFor` + fixtures, **not** a schema change |
| 3 | **Stock groups + compound units** (28) | 1 session | After #2, never beside it |
| 4 | **Multi-currency** (10) | 1–2 sessions | Rate at transaction date vs reporting date; realised/unrealised difference is the trap |
| 5 | **Budgets** (13) | 1 session | ⛔ **Must follow T2's AXIS model** (§8.1): a budget is a comparison dimension, not a posting |
| 6 | **Interest on overdue bills** (12) | 1 session | Rides bill-wise ageing, which already works |
| 7 | **Per-company print templates** (45) | 1 session | Pure output; low risk |
| 8 | **Export: e-mail + PDF** (44) | ~½ session | ⛔ Sending e-mail is **outward-facing** — needs an explicit decision before anything is sent |
| 9 | **GSTR-9 annual return** (19) | 1–2 sessions | Aggregates GSTR-1/3B; the reconciliation disclosures are the hard part |
| 10 | **SSO / OIDC** (39) | 1 session | Last, deliberately. Must not weaken step-up |
| — | **Rule 48(4) QR code** (41) | ⛔ **NEVER** | **PERMANENTLY UNSHIPPABLE.** The payload is signed with the Income Tax Department's private key. ⛔ **If any agent reports this done, REJECT IT** |

⛔ **EVERY ONE OF THESE NEEDS THE 15 REGISTRATIONS (§7.2) IF IT CARRIES A SCREEN**, including
`corpora/validate.py`'s `KNOWN_SCREENS` (outside `frontend/` entirely) and `printTarget.ts`'s
`REPORT_SLUGS`. ⚠️ **`make verify` does not run `tsc`** — they are separate gates.

⚠️ **A `report:NAME` frame is much cheaper than a `ScreenKind`** and is the right choice more often
than it looks: it inherits the report keymap, button bar, title, F2 and a place in Go To **for free**.
Pick a `ScreenKind` only for a screen carrying a create FORM. **Decide the resource SHAPE first** — it
decides whether you spend a migration number at all (§6.1).

---

## 6. PHASE D — THE FOLLOWING SESSION (the user's stated plan, recorded so it is not re-derived)

| Row | State | Note |
|---|---|---|
| **Payroll statutory** (18) | `server` | **The biggest module currently invisible.** Engine, statutory rates and file emitters all built, and `internal/payroll`/`payrolldb` are **already** in `internal/server`'s archtest allow-set — **no refactor, pure frontend**. Employees, pay heads, pay run, payslips. Budget **2 sessions** |
| **Offline entry with sync** (47) | `server` | Built. Needs a screen **plus a design decision**: how does a browser present queued intents? |
| Backup / restore / split (46) | `off` | Listed in §5 as #1 — pull it here instead if that ordering suits |

---

## 7. ⛔ THE LOG — FILL THIS IN AS YOU GO, ONE LINE PER ROW

**An audit whose findings live in one session's memory is an audit that will be run again from
scratch.** Append; do not start a new table. Fold into `HANDOFF.md` §5.5 at the end.

| Date | Row # | Keystrokes driven | Oracle used | Result |
|---|---|---|---|---|
| 2026-08-06 | 15 | `Alt+I` → pick → `Ctrl+A` | `voucher_external_reference` + re-post + ALTER/DELETE | ✅ IRN minted from a browser; re-post same IRN; both 409 |
| 2026-08-07 | 4 | `Alt+F6` Credit Note → `Ctrl+A` | `posting` sides + ledger names | ✅ after fix — `Cr party / Dr Sales / Dr Output tax` |
| 2026-08-07 | 1 (F9) | `F9` Purchase, accounting mode | `posting` sides + ledger names | ✅ after fix — `Cr supplier / Dr Purchases / Dr INPUT tax` |
| | | | | |

### The running count — update it, do not assume it

| | Rows |
|---|---|
| Claimed reachable (`on` + `always`) | **34** |
| Driven end to end and **confirmed** | _fill in_ |
| Driven and **found lying** | _fill in_ |
| **TRUE reachable number** | _the answer this file exists to produce_ |

---

## 8. RULES THAT MUST NOT BE BROKEN DURING THIS WORK

- ⛔ **One owner for the contended files** (§1.12): `VoucherScreen.tsx`, `AppShell.tsx`,
  `FeaturesScreen.tsx`, `corpora/validate.py`, `seed-demo.mts`, `HANDOFF.md`. Lanes **REPORT** the
  edit they need; they do not make it.
- ⛔ **Only ONE lane runs DB-backed Go tests.** One Postgres on :5433, shared across worktrees.
- ⛔ **The browser pass is the orchestrator's.** Read-only code recon fans out well (it produced §4);
  driving does not.
- ⛔ **A row flips to `on` on BROWSER evidence only.** Never on a green suite. That rule is the only
  control that has ever worked here.
- ⛔ **Every fix carries a MUTATION PROOF, and you must check that what fails is what you predicted
  would fail.** A proof asserted rather than executed is worth less than none, because it reads as
  evidence — this happened on 2026-08-06 and the claim was false.
- **Re-measure the gates; do not assume them.** Current: backend **2367 / 0 SKIP / 0 FAIL**,
  frontend **961 / 67**, `tsc` clean, `make verify` 0, `verify:demo` **33/33** freshly seeded.
  ⚠️ **A down database makes every DB test SKIP and `go test` still exits 0** — the `0 SKIP` is the
  load-bearing number, so bracket the run with a live `psql`.
- ⚠️ **`docker ps` can wedge while OrbStack reports Running.** It happened on 2026-08-07. `orb stop`
  then `orb start` clears it — but it bounces **every** container on the machine, and `tally_pg` does
  **not** come back on its own (`docker start tally_pg`).
