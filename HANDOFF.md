# HANDOFF — THE LIVE ONE. Start here, from cold.

> **Written 2026-08-05, updated in place at the bill-reference commit (2026-08-06).** This file replaces the ELEVEN `HANDOFF-*.md`
> files that preceded it (§11 lists them; `ls` confirms eleven). **Every other `HANDOFF-*.md` in this
> repository is SPENT** — see §11 for the list and for the one thing to do about them.
>
> ## ⛔⛔ COLD START? READ §1, RUN §2, THEN DO §1.4.
>
> **§1.4 is the concrete plan for the next session** — what to pick up, in what order, what decision
> has to be made before any code is written, and what already exists so you do not go looking for it.
> It is a fork: one small certain fix, and one mandatory track that starts with an architecture
> decision rather than typing.
>
> ⚠️ **THE READING ORDER HAS CHANGED NOW THAT TIER 1 IS CLOSED.** §4 used to be the work; it is now a
> defect audit plus loose ends. **§5 is the PARITY ROADMAP and is where TRACKS come from** — ordered
> by how much of a real business's day each item unblocks, with the reasoning stated so it can be
> argued with. §3 is the machine. Everything after §6 is reference you will need exactly when you hit
> it, not reading for now.

---

## 1. THE ANSWER TO THE QUESTION YOU ARE ABOUT TO ASK

**Is Tier 1 done? YES. T5 was the last row and it is `on`, on BROWSER evidence.**

The `FeaturesScreen` (`frontend/src/screens/FeaturesScreen.tsx`) is this project's parity ledger, and
it is the authority, not this file. Measured at `2777980`:
**30 rows `on`, 11 `off`, 3 `server`, 2 `always`** — 46 rows in total.

⚠️ **COUNT IT WITH `python3` PAIRING EACH `name:` WITH ITS FOLLOWING `state:`.** A previous edit of
this file used `grep -c 'state: "always"\|state: "server"'`, read the COMBINED count as one of them,
and wrote down that no `server` row was left. Three are. A claim measured by a command you did not
check is a guess.

| Tier 1 track | Row | State |
|---|---|---|
| T1 Printing | several | ✅ `on` |
| T2 Cost centres | the masters and both reports | ✅ `on` |
| T2 (cont.) | Cost centre allocation during voucher entry | ✅ `on` — browser-verified, §1.2 |
| T3 Orders + BOM | Order processing and Bill of Materials | ✅ `on` — browser-verified, §1.1 |
| T4 Bank reconciliation | | ✅ `on` |
| **T5 e-Way Bill** | | ✅ **`on` — browser-verified, §1.3. THE LAST ONE.** |
| T6 TDS/TCS | | ✅ `on` |
| T7 Features screen | — | ✅ done |

**So Tier 1 is closed and §5 is now the whole map.** ⛔ **The concrete plan for what to do next is
§1.4** — read it after §2. In one line: three finished engines still have no screen (§5.2), one of
them is statutorily mandatory, and they remain the cheapest real parity this product can buy.

> ### ⛔ SINCE THEN: BILL-WISE REFERENCES SHIPPED (§1.5), AND THE BROWSER FOUND A NEW DEFECT.
> Money keyed in a browser now reaches the Outstandings report and reconciles with the Balance Sheet
> to the paisa — it never did before, on any path. **Two things came out of it that outrank most of
> §5.3:** an ALTER was silently destroying bill references (§4.3's audit had cleared that table on
> half a check), and **drilling into any non-Sales voucher from the Day Book re-classes it as a Sales
> voucher (§4.8, unfixed).**

> ### ⛔ WHAT TIER 1 COST, AND THE ONE SENTENCE WORTH CARRYING FORWARD
> Five of the seven tracks shipped a complete, tested, reviewed BACKEND before anyone noticed no
> human could reach it. Every one was caught the same way — by driving the app — and never by a
> suite. **§12's browser rule is not ceremony; it is the only control that has ever worked.**

### 1.1 What the session at `8db0ffd..7e565a3` closed, and how it is known

T3 went from `off` to `on` across three commits, and the row flipped on **BROWSER EVIDENCE** rather
than a green suite — which §12 requires and which is the only thing that catches the defect class
this repo keeps re-learning. Driven in a real browser against real `cored`, with a virtual WebAuthn
authenticator over CDP so the step-up completed (§9.7's recipe, **verified working again**):

| Keystroke | What actually happened |
|---|---|
| `Ctrl+F8` | the Sales Orders register opened |
| `Alt+C`, `Ctrl+A` | **SO-BROWSER-1 recorded through the passkey**, read back with the server-assigned line 1 |
| `Alt+F9`, `Ctrl+A` | a Receipt Note — and the picker **offered no orders**, because the only open order is a SALES order and a receipt discharges PURCHASE orders |
| `Alt+F8` + link 8 | a Delivery Note linked to the order; the qty field was **empty after picking** |
| Sales Order Book | **Ordered 20 / Delivered 8 / Outstanding 12 / ₹6,000** |
| `Alt+C`, `Alt+X` | a recipe (10 yields 3) exploded for 9 gave **exactly 30.0000** |

⛔ **AND THE BROWSER FOUND A BUG NOTHING ELSE COULD.** The order book heading rendered `As at ·` with
nothing after it: the server does **not** substitute its own today when no `as_on` is given — it
returns `as_on: ""` and reports the position from every order ever placed. That is now the third
defect of this exact shape found by driving the app rather than reading it (the F2 period dialog,
Alt+O's dead binding, this). **A default branch that produces a plausible-looking string is invisible
to every test that only asserts the sentences it expects.** Worse, and worth naming: **the test I had
written myself certified the bug** — it asserted `As at ${book.as_on}` and passed on the empty string.

### 1.2 The T2 allocation row, and the live bug it walked into

Closed at `a86283b`, browser-verified the same way: `F5` → HDFC Bank → Electricity
10,000 → **allocate** → Department/Sales 6,000 showed *"6000.0000 of 10000 — 4000.0000
left"* plus the short-axis refusal; a second split to Admin via the **rest** button
filled 4000.0000, the refusal cleared, and `Ctrl+A` saved through the passkey. The Cost
Centre Summary then read **Admin 44,000 / Sales 66,000** and reconciled.

⛔ **THE OFFSET IS THE HAZARD, AND IT IS PROVED IN THE DATABASE, NOT IN A TEST.** In
single entry the postings array is `[the account paid from, ...the particulars]`, so the
particular at row 0 is posting **1**. The allocations landed on **Electricity (Dr
10,000)** — not on HDFC Bank. With the offset at 0 they would have attached to the bank
credit leg, on a voucher that balances perfectly and a Cost Centre Summary that foots.
Nothing downstream can detect that. `buildCostAllocations` therefore takes
`primaryLegCount` as a REQUIRED parameter with no default.

> ### ⛔⛔ AND THE BUG IT EXPOSED, WHICH WAS LIVE AND WHICH NO TEST COULD SEE
>
> `ensureLedger` (`screens/vouchers/gstVoucher.ts`) called `createLedger`
> **unconditionally**, on an assumption written in its own doc comment: *"createLedger
> is idempotent server-side (by name within a company)"*. **That was false.**
> `INSERT INTO ledger` has no `ON CONFLICT` and `ledger_company_id_name_key` is a plain
> UNIQUE index on `(company_id, name)`, so creating an existing ledger is a raw
> unique-violation.
>
> **On a fresh page load the memo is empty, so keying a Payment / Receipt / Contra
> against any ledger the company ALREADY HAD — the normal case, and every case after the
> first day — failed with `masters: insert ledger: ERROR: duplicate key value violates
> unique constraint`.** It only ever appeared to work when the memo was warm from an
> earlier post in the same page session.
>
> **NOT ONE OF THE 705 TESTS COULD CATCH IT**, because they all run against the mock,
> whose `createLedger` mints a fresh id per call. That is the lesson to carry: *a mock
> that is more permissive than the server hides a whole class of defect no amount of
> coverage reaches.* Fixed to resolve-then-create, pinned by four tests against a client
> that refuses duplicates the way cored does, and mutation-proved.

### 1.3 T5 — THE LAST TIER-1 ROW, AND THE THREE BUGS THE BROWSER FOUND CLOSING IT

Closed at `2299fe7..2777980`. Driven in a real browser against real `cored` with a virtual WebAuthn
authenticator over CDP (§9.7's recipe, **verified working a third time**):

| Keystroke | What actually happened |
|---|---|
| `F8`, `Ctrl+H` → Item Invoice | the item grid; 40 MS Angle @ 2000 |
| `Ctrl+A` | **Sales 4 saved through the passkey** — 80,000 + 7,200 + 7,200 = 94,400 |
| `Alt+Z` | the e-Way Bill screen, standing notice already up |
| pick Sales 4, `Ctrl+A` | **`LOCAL-EWB-4-29AACC`**, valid to 2026-08-07 (1 day) |
| the payload | HSN 7326, CGST 9 / SGST 9, IGST 0, `totInvValue 94400.00` — **foots to the paisa** |
| `Esc`, `Alt+Z`, re-pick | the STORED bill loaded and the form pre-filled |
| alter to 30 units, `Alt+Z` | ⛔ **the STALE banner, above the identifier** |
| pick a Delivery Note | refused **by name**, pointing at the tax invoice |

⛔ **AND THE ARCHITECTURE DECISION §4.2 WAS BLOCKED ON — IT IS NEITHER OF THE TWO OPTIONS.**
Reading the code settled it: `statutoryd` exists because the IRN workflow wraps an **exactly-once
external IRP call** in durable, resumable state, and a crash between "generate" and "freeze" mints a
second IRN. **None of that is true of the e-way bill.** `Assess`, `BuildEWayBill`,
`ValidateEWayBill`, `EWayBillDisclosures` and even `FakeEWayBillProvider.GenerateEWayBill` take no
`context.Context`, return no `error`, and touch no clock, socket or store — they are pure functions.
Routing four pure calls through a second process would have bought nothing and would have put an
operator-facing WRITE **outside cored's PDP, RLS, step-up and company scoping**, none of which
statutoryd has. So the pure Rule-138 core moved DOWN into `internal/statutory/rules`, a leaf with the
EMPTY allowed-import set that `internal/server` may import. The workflow plane is untouched.

> ⚠️ **AND A CORRECTION TO THIS FILE'S OWN §5.2, WHICH CLAIMED THE SPLIT "UNBLOCKS TWO ROWS FOR THE
> PRICE OF ONE". IT DOES NOT.** The IRN genuinely needs the durable workflow, so its screen still
> needs cored to **proxy** to statutoryd — a different problem, and nobody has solved it. What the
> IRN screen inherits is the shared snapshot vocabulary in a package cored can name, and the
> precedent for where the line falls. The claim is corrected at the top of `rules/ewaybill.go` too,
> where the next person will actually read it.

#### The three bugs, and only one of them was T5's

1. ⛔⛔ **`ensureOutputTaxLedgers` called `createLedger` UNCONDITIONALLY** — the *identical* bug to
   the one fixed at `a86283b`, **forty lines below it in the same file**. So keying an ITEM INVOICE
   on any company that had already posted one GST voucher failed on every fresh page load with
   `duplicate key value violates unique constraint "ledger_company_id_name_key"` — i.e. every
   company after its first day. **Not one of the 726 tests could see it**, because the mock's
   `createLedger` mints a fresh id per call (§9.4's third kind).
   > **THE LESSON IS NOT "THERE WAS ANOTHER ONE."** It is that fixing the REPORTED call site is not
   > fixing the CLASS. When a defect is found in an `ensure*` / `upsert*` / `getOrCreate*` helper,
   > **grep the file for its neighbours before closing it.** Fixed by routing through the corrected
   > `ensureLedger` rather than re-implementing its dance — a second copy is exactly how this
   > happened. Mutation-proved against a `strictClient` that refuses the way cored does.
2. **A GET that answers AFTER a raise wiped the raised bill off the screen.** `let live = true` does
   not cover it: nothing unmounted, no dependency changed, so the stale 404's catch cleared state
   the newer POST had just set. The operator sees `Ctrl+A` do nothing, with no error anywhere, and a
   reload shows the bill — so it reads as a fluke. Found by the real-shell test, not by review;
   fixed with a generation ref the raise claims.
3. ⚠️ **PRE-EXISTING AND NOT T5's — see §4.7 item 11.** An ITEM invoice against a bill-by-bill party
   creates **no `bill_allocation`**, so the receivable is on the Balance Sheet and absent from
   Outstandings. `verify:demo` went 32/33 on exactly that invoice.

⚠️ **AND A FIFTEENTH REGISTRATION POINT, WHICH §7.2 DOCUMENTS AS FOURTEEN:**
`corpora/validate.py`'s `KNOWN_SCREENS`. `make verify` caught it by name. It is now fifteen.

---

### 1.4 ⛔⛔ START HERE — WHAT THE NEXT SESSION ACTUALLY DOES

**Run §2 first (ten minutes, and it has burned every session that skipped it). Then this.**

> ## ✅ TRACK A IS DONE. BILL REFERENCES ARE REAL, END TO END, ON BROWSER EVIDENCE.
>
> The section below is kept for its reasoning; **the work it describes is shipped.** §1.5 records
> what it actually cost and the three findings it produced. **The next track is §1.4 B (the IRN
> screen), and its architecture decision is still unmade.**
>
> ⛔ **AND A IS NOT WHAT THIS SECTION ESTIMATED.** It called A "small, certain, needs no
> architectural decision". Two of those were wrong:
> 1. **It needed a server change.** `allocateVoucherNo` mints the voucher number INSIDE the post
>    transaction, so a browser cannot name the bill it is opening — the name does not exist when the
>    request is assembled. "The shape of the work is CLIENT-side, not server-side" was false.
> 2. **It was two halves, not one.** `Outstandings` nets `NEW` against `AGAINST` by ref_name, so
>    raising bills without a way to settle them produces a report that lists every invoice as
>    pending forever. Shipping only the raise half would have replaced an invisible wrong number
>    with a visible one.

### THE RECOMMENDATION, AND IT IS A FORK

> **If you have a full session: do B (the IRN screen), and do A first as the warm-up — A is small
> enough that it will not eat B.**
> **If you have a short one: do A, and stop. It is a complete, shippable, real fix.**
>
> ⚠️ **THIS FORK WAS WRONG ABOUT A, and the correction is the reusable part: A took a full session
> on its own.** The estimate came from a diagnosis (§4.7 item 11) that had already been corrected
> once and was still incomplete — the corrected version knew no screen built a reference, but not
> that no screen COULD, nor that settlement was a second feature. **A diagnosis that has been wrong
> once is not thereby right; it has been wrong once.**

### A. ✅ DONE — `bill_allocation` on EVERY voucher path (was: "the item-invoice path")

§4.7 item 11. A voucher keyed against a bill-by-bill party creates **no bill reference**, so the
receivable sits in the Balance Sheet's Sundry Debtors and is **absent from Outstandings**: money that
never ages and never reaches a statement. Found by keying an item invoice in a browser (§1.3).

> ⛔⛔ **AND THE FIRST DIAGNOSIS WAS WRONG — READ THIS BEFORE YOU START, IT CHANGES THE JOB.**
> The obvious reading, and the one this file carried for an hour, is "the accounting path writes a
> `bill_allocation` and the item path does not". **That is false.** `internal/ledger/ledger.go`
> writes them from `in.BillAllocations` — i.e. **the CLIENT supplies them, on every path** — and
> `grep -rn "bill_allocations:" frontend/src` finds them constructed in exactly one place:
> **`frontend/scripts/seed-demo.mts`.** No screen builds one. Not the item invoice, not the
> accounting-mode GST voucher, not any of them.
>
> **So the gap is not an item-invoice gap. NO VOUCHER KEYED IN A BROWSER, ON ANY PATH, HAS EVER
> CREATED A BILL REFERENCE.** The demo only looks correct because the seed script hand-writes them,
> which is precisely the "reasoning from what you see on screen" hazard §11 warns about — and it is
> why every gate stayed green for the whole life of the feature.
>
> ⚠️ This is also a fresh instance of §9.1's rule, caught while writing this section: the plausible
> claim was inferred from DATA (seeded invoices have allocations, mine did not) and was never checked
> against CODE. Ten minutes of grep changed the size of the job.

Why this first: it is small, it is certain, it needs no architectural decision, and it closes a hole
behind rows that already read `on` — the exact class §5.5 says nobody has audited.

**The one thing to decide before writing code: what the bill REFERENCE is.** The voucher number is
the obvious answer and is Tally's own default (`New Ref`). Write down why you chose it.

The shape of the work is CLIENT-side, not server-side: the voucher forms must build
`bill_allocations` when the party maintains bill-by-bill. ⚠️ `ledger.maintain_bill_by_bill` is on the
ledger master, so the form can read it from the chart it already loads. Prove it the way §4.1
requires — POST over HTTP to a bill-by-bill party, then read the figure back out of the
**Outstandings report** and reconcile it against Sundry Debtors on the Balance Sheet. ⛔ Carry a
CONTROL: the same voucher to a party that does NOT maintain bill-by-bill must produce no allocation,
or the test passes by always writing one.

### 1.5 ⛔ WHAT TRACK A SHIPPED, AND THE THREE THINGS IT FOUND

Closed 2026-08-06. Driven in a real browser against real `cored` with a virtual WebAuthn
authenticator over CDP (§9.7's recipe, **verified working a fourth time**):

| Keystroke | What actually happened |
|---|---|
| `F8`, pick **Acme Traders** (bill-by-bill), 10,000 @ 18% | the bill-wise panel appeared **by itself**, tracking the gross to **11,800.0000 DR** |
| set due 2026-07-15, `Ctrl+A` | **Sales 5 saved through the passkey** |
| the database | `ref_name = "5"` — **the SERVER stamped the number it allocated** |
| `Alt+G` Outstandings | Acme **1,85,480.00**, and drilling showed the new invoice |
| `F6`, HDFC Bank, particular **Acme Traders** 5,000 | the settle picker appeared **on the ROW**, listing the party's five real open bills with ageing — `5 (11800.0000, 22d overdue)` |
| pick bill 5, `Ctrl+A` | **Receipt 2 saved** |
| Outstandings | Acme **1,80,480.00** — bill 5 now **6,800** pending |
| Balance Sheet | Sundry Debtors **2,60,720.00** = Outstandings total **2,60,720.00** — ⛔ **reconciles to the paisa** |

**The shape of the fix, because the next person will look for it in the wrong place:**

- **The server stamps the reference name.** `validateBillAllocations` now permits an empty `ref_name`
  for every type EXCEPT `AGAINST`, and `post()` substitutes `in.VoucherNo` after allocation, from a
  single-sourced `billAllocations` variable with the delete guard its four siblings already had.
- ⛔ **`AGAINST` MUST NAME ITS BILL, and that asymmetry is the whole design.**
  `reporting.Outstandings` groups by `(ledger_id, ref_name)` and **ignores `ref_type` entirely** — so
  a defaulted `AGAINST` would not settle anything, it would open a SECOND bill under its own number,
  leaving the invoice fully outstanding beside a negative row. Both figures wrong, report still foots.
- **A new server invariant: references on a ledger must total that ledger's posting**, signed and
  exact. `BillAllocationInput`'s doc comment had declared this since the beginning and *nothing had
  ever checked it* ("the engine records what it is given" was true, and was the bug). It is checked
  INSIDE the transaction against the FINAL postings, because an item invoice's postings do not exist
  when the request arrives.
- **The bill-bearing leg is stated by the caller, never inferred** — same discipline as
  `buildCostAllocations`'s `primaryLegCount`, and for a sharper reason: on a Sales invoice the leg is
  `party_ledger_id`, but **in SINGLE ENTRY the party field is the BANK** and the customer being
  settled is a PARTICULAR row.

#### The three findings, and two of them are not Track A's

1. ⛔⛔ **AN ALTER DESTROYED BILL REFERENCES, AND IT WAS LIVE.** `bill_allocation` is keyed on
   `voucher_version_id`, so an ALTER re-states. Editing the narration of any voucher carrying a
   reference **deleted it** — the invoice stayed in Sundry Debtors and vanished from Outstandings.
   > **AND §4.3's AUDIT HAD ALREADY "CHECKED" THIS TABLE.** It verified `voucherFullJSON` EMITS the
   > rows, ticked them off, and stopped. **Emitting is necessary and NOT SUFFICIENT** — the round
   > trip closes only when a form rebuilt from that response sends them back, and no form did.
   > **The audit was half an audit, and the other half is unrun for the four tables it cleared.**
   > See §4.3, which is corrected.
2. ⛔⛔ **DRILLING INTO ANY NON-SALES VOUCHER FROM THE DAY BOOK OPENS A *SALES* FRAME — AND SAVING
   RE-CLASSES THE VOUCHER.** `navMachine.ts` pushes `newVoucherFrame(DEFAULT_LAST_VOUCHER, "alter")`
   for every voucher-bearing drill, and `acceptVoucher` routes the PUT by that screen type. **Proved:
   drill a Payment, press `Ctrl+A`, and `alterSalesVoucher` is called** — not `alterPaymentVoucher`.
   NOT Track A's, NOT fixed, and it is §5.1's third kind (a row reading `on` with a hole behind it)
   found exactly the way §5.5 says to find them. **It is now §4.8 and it is the cheapest real defect
   on the board.**
3. **The settle pre-fill over-allocated every partial payment.** Choosing a bill filled its whole
   pending, so a 5,000 receipt against an 11,800 invoice greeted the operator with a refusal about
   money they had not typed. Capped at the line's remainder. Found by driving it, not by review.

⚠️ **AND A TEST THAT COULD NOT FAIL, CAUGHT BY MUTATION AND NOT BY READING.** The first version of
the alter round-trip test called `alterSalesVoucher` directly rather than driving the form; deleting
the hydration from `formFromView` left the whole suite green. It now drives the shell. **§9.2's rule
applies to tests you wrote ten minutes ago.**

⚠️ **THE SINGLE-ENTRY ALTER HYDRATION IS WRITTEN BUT UNVERIFIED**, because finding 2 makes it
unreachable: you cannot drill into a Receipt. `VoucherScreen.billRefs.test.tsx` says so at the point
where the test would go, with the exact test to add once the drill is fixed.

⚠️ **NO RESIDUE LEFT, DELIBERATELY** (§11's practice, copied from T5). The browser-keyed Sales 5 and
Receipt 2 were `Alt+D`-tombstoned through the app: the sale carried **no HSN** (the accounting-mode
GST form's HSN field was left blank) and took `verify:demo` to 32/33 on the GSTR-1 HSN check.
⚠️ **That is worth knowing on its own: any accounting-mode GST sale keyed without an HSN breaks that
gate**, which is a brittleness in the check rather than a product defect — but it will catch the next
person the same way.

### B. THEN THE e-INVOICE / IRN SCREEN — but the FIRST hour is a DECISION, not typing

§5.2. **Statutorily mandatory**, and the highest-value row left. ⛔ **It does NOT inherit a finished
path from T5** — read §5.2's correction before estimating anything.

> #### ⛔ THE DECISION TO MAKE BEFORE WRITING A LINE, exactly as T5's was
>
> The IRN workflow is NOT pure — it wraps an exactly-once external IRP call in durable, resumable
> state — so it **must stay in `statutoryd`**, and the T5 split does not help. The open question is
> the one nobody has answered: **how does a browser trigger it?**
>
> | Shape | What it costs | What it buys |
> |---|---|---|
> | **cored PROXIES to statutoryd** | cored gains a hard runtime dependency on a second process, and a new failure mode when it is down. ⚠️ **There is no `make run-statutoryd` target and nothing runs it in dev** — that is part of the work | the screen keeps cored's PDP, RLS, step-up and company scoping, and the client contract looks exactly like T5's |
> | **the browser reaches statutoryd directly** | statutoryd has **no PDP, no RLS, no step-up and no company scoping** — this is only acceptable behind something that adds them, which is most of a second server | no proxy hop |
>
> **The first shape is almost certainly right** for the same reason T5's decision went the way it
> did — an operator-facing write belongs inside the authority stack — but say WHY in the commit,
> because the next person will ask.
>
> ⛔ **AND WRITE THE DECISION DOWN HERE WHEN YOU MAKE IT**, in this section, the way §4.2 now
> records T5's. A decision that lives in one session's memory is a decision that gets re-litigated.

**What already exists, so you do not go looking:**

- `cmd/statutoryd/main.go` — `handleEInvoice` at
  `POST /companies/{companyID}/vouchers/{voucherID}/e-invoice`, returning `{irn, ack_no, status,
  pinned_voucher_no}`. Env: `CORED_BASE_URL`, `CORED_SERVICE_TOKEN`, `PORT` (8091), `STATUTORYD_DSN`.
- The durable workflow, the retry classification (`IsPermanent` → 422, transient → 503), and the
  freeze hook `POST /vouchers/{id}/external-reference`. ⚠️ **`voucher_external_reference.kind` is
  CHECK-constrained to `IRN` / `FILED_GSTR1`** — the IRN needs no new kind, unlike T5, which is why
  T5 got its own table instead.
- `statutory_step` is migration **0013**; statutoryd runs no migrations, cored owns all schema.

**What does NOT exist, and is the actual work:**

- ⛔ **NOTHING ON THE CLIENT AT ALL.** Unlike T5 — which found `EWayBillRequest`/`EWayBillResponse`
  already written speculatively in `api/types.ts` — there are **no IRN types and no client methods**.
  Checked. Budget for the full fifteen registrations (§7.2) plus three client files.
- No `make run-statutoryd`, and no dev flow for minting `CORED_SERVICE_TOKEN`. **You cannot
  browser-verify this row without both**, and §12 item 1 will not accept anything less.

> #### ⚠️ ONE THING T5 LEARNED THAT THIS ROW INHERITS FOR FREE, AND IT IS WORTH REAL TIME
> `eInvoiceRequest` makes the CALLER supply seller/buyer GSTIN, legal name and state code, because
> statutoryd cannot read cored's masters. **cored CAN.** `internal/ewaybill`'s `loadConsignment`
> already resolves exactly those fields from the `company` row and the party `ledger` row, and it is
> proven against the demo company. A cored-side proxy should fill them the same way rather than
> making a screen ask an operator to retype their own GSTIN — copy that query.

### ⛔ WHAT NOT TO DO

- **Do not start T18** (the Rule 48(4) QR code). §5.3's box: the payload is signed with the Income
  Tax Department's private key and it is permanently unshippable from here. If a workflow reports it
  done, reject it.
- **Do not treat the IRN as "T5 again".** T5 was pure functions and a table. This is a second
  deployable, a service token, and a process nothing currently runs.
- **Do not skip the browser pass to save time.** T5's found three defects, two in code that session
  never touched (§1.3), and §3.1's fourth observation exists because of it.

---

---

## 2. RUN-FIRST CHECKLIST — ten minutes, and it has burned every session that skipped it

```bash
# 1. Tree clean? Commit or stash BEFORE doing anything.
git status --porcelain

# 2. Postgres. ⛔ A TCP CONNECT IS NOT LIVENESS — docker-proxy holds the port open past a
#    dead backend, and a watchdog built on `nc -z` logged NOTHING through a ten-minute outage.
docker ps | grep tally_pg
pg_isready -h localhost -p 5433 -t 5 && \
  PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"

# 3. Kill stale servers BY PID. `pkill -f "cmd/cored serve"` misses the compiled child.
lsof -nP -iTCP:8087 -sTCP:LISTEN     # -> kill -9 that PID
ps -eo pid,etime,command | grep "cored serve" | grep -v grep   # etime is AGE; a 28h process predates you
lsof -nP -iTCP:5173 -sTCP:LISTEN     # a stale vite serves day-old code just as happily
lsof -nP -iTCP:8088 -sTCP:LISTEN     # insightd (uvicorn). verify:demo needs it UP.

# 4. Baseline, and WRITE IT DOWN. Without -v the SKIP census is a lie.
cd backend && go test ./... -count=1 -v 2>&1 | tee /tmp/base.log | tail -3
grep -c -- '--- PASS' /tmp/base.log; grep -c -- '--- SKIP' /tmp/base.log   # SKIP MUST BE 0
cd ../frontend && pnpm exec tsc --noEmit && pnpm run test:run
cd .. && make verify
```

### 2.1 The gate baseline at the bill-reference commit — re-measure, do not assume

| Gate | Value |
|---|---|
| backend `go test ./... -count=1 -v` | **2307 pass / 0 SKIP / 0 FAIL** |
| frontend `pnpm run test:run` | **761 pass / 56 files** |
| `pnpm exec tsc --noEmit` | clean — ⛔ **a SEPARATE gate from `make verify`** |
| `make verify` | exit 0 |
| `verify:demo` | **33/33**, run **ALONE**, needs cored **and** insightd up |
| `gofmt -l` on every file you touch | clean |

⚠️ **`verify:demo` TAKES THE COMPANY ID AS AN ARGUMENT** and prints a usage line without it:
`pnpm run verify:demo "$(cat frontend/.demo-company-id)"`. Bare `pnpm run verify:demo` exits 2, which
is easy to misread as a failing gate.

⚠️ **A DOWN DATABASE MAKES EVERY DB TEST SKIP AND `go test` STILL EXITS 0.** A skip is
indistinguishable from a pass in the exit code. This has invalidated a whole session's evidence
before. Bracket every suite run with a live `psql` before **and** after.

⚠️ **`make verify` DOES NOT RUN `tsc`.** They are different gates. Folding `tsc` into `make verify` is
a ~10-minute job and is still the cheapest real improvement available.

### 2.2 Driving the app

```bash
make run-cored      # :8087
make run-insightd   # :8088
cd frontend && pnpm dev   # :5173
open "http://localhost:5173/?company=$(cat frontend/.demo-company-id)"
```

Sign in `browser-dev-owner` / `tally-demo-2026`. **It is a keystroke app: a click SELECTS, `Enter`
OPENS.**

⛔ **`/healthz` IS NOT A VERSION CHECK.** Poll for something only the new build serves, and read the
STATUS: `401` means the route exists and needs auth; `404` means you are talking to the old binary.

---

## 3. THE MACHINE, AND HOW TO NOT MAKE IT SLOW

| Constraint | Value | Consequence |
|---|---|---|
| Cores / RAM | 8 / 16 GB, shared with other worktrees | |
| Concurrent agents per workflow | `min(16, cores-2)` = **6** | asking for 20 queues 14 |
| Workflows at once | **ONE** | 3 × 6 agents each spawning a compiler is what turns a track into an afternoon |
| Postgres | **ONE instance on :5433, shared by every worktree** | ⛔ parallel lanes may NOT run DB-backed tests; concurrent `migrations.Apply` races and the residue check counts rows globally |
| Heavy commands | auto-queued through `ci-slot` (2 slots) by a PreToolUse hook | **do not wrap anything yourself**; a command that pauses is WAITING, not hung |

**Live gates (`verify:demo`, `verify:tenancy`, `e2e:ui`) run ALONE.** Slow? `/machine-health`.

### 3.1 The measured rate, which should shape what you promise

| Session | Delivered | Workflows | Subagent tokens |
|---|---|---|---|
| n−3 | T6 | 4, with 3 sequential barriers | ~1.7M |
| n−2 | T2 + the shared T3 contract | 2, zero barriers | ~1.3M |
| n−1 | T3 backend end-to-end + T5 core | 0 — solo | 0 |
| n−1 | T3 frontend end-to-end (5 screens) AND T2's allocation UI — two rows, both browser-verified | 0 — solo | 0 |
| n−1 | **T5 end-to-end — the architecture decision, the rules split, migration 0047, the store/route/screen, and the row flipped on browser evidence. TIER 1 CLOSED.** | 0 — solo | 0 |
| **n (this one)** | **Bill-wise references end to end — the server stamp, the sum-to-posting invariant, the pure module, the picker, three build paths, the mock's ageing report, and raise→settle→reconcile on browser evidence. Plus one live data-loss fix and one new defect found (§4.8).** | **0 — solo** | **0** |

**Five observations, and the fifth is new.** First: plan for roughly one track per session — the
last three delivered exactly one each and all were full. Second: **the last THREE sessions each
delivered a whole track with no subagents at all**, because each was a long serial chain of decisions
in contended files; **fan out for genuinely disjoint files, go solo down a dependency chain.** Third:
**the frontend half of a track is not smaller than the backend half** — T3's frontend was a full
session on its own. Fourth, and it is the one to budget for: **the BROWSER PASS IS NOT A VICTORY LAP
AND IT IS NOT FIVE MINUTES.** T5's found three defects (§1.3), two of them in code this session never
touched, and fixing them was real work with its own tests and mutation proofs. Budget for the pass
AND for what it finds — a track is not done when the screen renders.

**Fifth, and it is about ESTIMATES rather than throughput: the item this file called "small,
certain, needs no architectural decision" took a whole session** (§1.5). It was sized from a
diagnosis that had already been corrected once and was still incomplete. **Re-derive the size from
the CODE at the start of the track, not from the handoff's own summary of it** — ten minutes of grep
changed the size of this job twice, and the second time was after it had supposedly been settled.

---

## 4. ⛔⛔ THE WORK, IN ORDER. THIS IS THE PART YOU ACT ON.

**Tier 1 is CLOSED (§1).** What is left here is a defect audit and the loose ends around it.
⛔ **The next TRACK comes from §5, not from this section** — and §5.2 is where to start.

⚠️ **THE ORDER IS NOT ARBITRARY.** Item 1 is a data-loss audit: cheap, and unbounded in cost if
skipped. Item 2 is a real money-visibility defect found by driving the app.

### 4.1 THE BAR, restated because it is the whole point

> **A feature is not shipped when its tests pass. It is shipped when a human can reach it.**
>
> This has been learned FIVE times now, at increasing cost:
> 1. `voucherReq.ItemLedgerID` — every item invoice the F8/F9 grid could send came back 422; the
>    generator was reachable only from Go tests.
> 2. **T2's cost allocations** — the validation, the hashed payload entry and the row insert were
>    built, reviewed, and covered by unit AND live-Postgres tests **while no HTTP body could carry an
>    allocation.** Nothing failed. Both report routes answered 200 and were *correct*: they accurately
>    reported that nothing had been allocated, because nothing could be.
> 3. **T2 again, one layer up** — the Go wire was fixed and `VoucherRequest` in
>    `frontend/src/api/types.ts` was not. So a browser still could not construct one, and all 597
>    frontend tests passed.
> 4. **T3, and this one ran a whole release** — the order tables, the fulfilment links, the
>    over-fulfilment trigger, both order books, the ACL migration and all fourteen client methods
>    shipped and were tested while **no screen existed**.
> 5. **T5, and this one ran a release too** — `Assess`, `BuildEWayBill`, `ValidateEWayBill`, the
>    recorded NIC schema and the disclosure block were built, reviewed and GOOD, with no store, no
>    route and no screen. The F11 row said `off`, honestly, for the whole time.
>
> **THE RULE: a feature is not reachable until the field exists on EVERY wire between the operator and
> the table — Go struct, TS type, and the form that fills it.**
>
> **THE PATTERN THAT CATCHES IT:** `backend/internal/server/order_fulfilment_http_test.go` and now
> `ewaybill_http_test.go`. Both POST as JSON and read the figures back out over HTTP, with nothing
> stubbed and **no SQL in the fixture that reaches around the API**. Copy this shape.
>
> ⚠️ **AND ITS TWIN: THE READ-BACK.** A feature can be perfectly reachable on the way IN and destroy
> data on the way OUT — see §4.3.

### 4.2 ✅ T5 — DONE at `2777980`. What the decision was, and what it did NOT buy.

The row is `on` (§1.3). The architecture question this section used to pose is **settled**: the pure
Rule-138 core lives in `internal/statutory/rules`, a leaf with the EMPTY allowed-import set that
`internal/server` may import; `internal/statutory` (the IRN workflow plane) is untouched and is NOT
in server's allowed set. The reasoning is at the top of `rules/ewaybill.go`.

⛔ **WHAT IT DID NOT BUY: the IRN screen.** §5.2 used to claim this split "unblocks TWO rows for the
price of one". It does not — the IRN needs the durable workflow, so a browser triggering one still
needs **cored to proxy to statutoryd**, and that is unbuilt and undesigned. Do not start the IRN
screen believing the path exists.

Two gaps T5 states on its own face rather than hiding:

- **A bill cannot be raised against a DELIVERY NOTE.** It moves goods and carries no tax, so the
  consignment value would have to be derived from the movement rather than read from the invoice —
  and Assess compares that value against the threshold. Guessing it in the permissive direction
  means a consignment that needed a bill did not get one. `ErrNoTaxLines` refuses by name and points
  at the tax invoice. NIC supports this (docType `CHL`); it is real work, not an oversight.
- **It does not PRINT, deliberately.** A printed sheet is exactly what a driver hands to a
  checkpost, and what this build produces is a payload plus a local reference no government issued.
  `printTarget.ts` refuses with that reason rather than being left unmapped.

### 4.3 ⛔ ITEM 1 — THE READ-BACK AUDIT. Cheap, and it is a DATA-LOSS class.

**T3 was about to ship a defect that silently un-delivers an order, and the shape generalises.**
`order_fulfilment` keys on `(voucher_version_id, line_no)`, so **an ALTER RE-STATES**: whatever the
body omits is gone. Meanwhile `GET /vouchers/{id}` did not return the links at all. Open a delivered
delivery note, fix a typo in the narration, save → **the order silently re-opens.** Nothing refuses it,
because zero links is a legal state: no trigger fires, no total stops adding up, and the only trace is
an order book reporting a commitment the company already met.

Fixed for fulfilments AND for `cost_allocations` (which answers the old §4.7 item 8 — and the answer
was worse than feared: `ledger.VoucherView` had carried them since T2 **with a doc comment explaining
at length why omitting them destroys a departmental split**, and `voucherFullJSON` dropped them).

> ### ⛔ THE AUDIT TO RUN, because this is now FOUR occurrences (postings, inventory, cost allocations, fulfilments):
> **For every table keyed on `voucher_version_id`, ask: does `voucherFullJSON` emit it?** If an ALTER
> re-states a table and the GET does not return it, the ordinary round trip DELETES it. Grep the
> migrations for `voucher_version_id` and check each one off against
> `internal/server/server.go`'s `voucherFullJSON`. **Verify the rest, and write down which ones you
> checked.**
>
> ✅ **CHECKED SO FAR, so nobody re-derives it:** `posting`, `stock_movement`, `bill_allocation`,
> `posting_cost_allocation` and `order_fulfilment` are all emitted.
>
> ### ⛔⛔ AND THAT TICK WAS HALF A CHECK. IT CLEARED A TABLE THAT WAS LOSING DATA.
>
> `bill_allocation` is on the list above because `voucherFullJSON` EMITS it — which it does, and
> always did. **No form ever sent them back.** So the ordinary round trip (open an invoice, fix the
> narration, save) DELETED the bill reference: the receivable stayed in Sundry Debtors on the Balance
> Sheet and vanished from Outstandings, with nothing refusing it because zero references is a legal
> state. Fixed 2026-08-06 (§1.5), pinned by `TestAnAlterRebuiltFromTheGetKeepsTheBill` WITH the
> control that an alter omitting them destroys the bill while Sundry Debtors does not move.
>
> **THE AUDIT QUESTION WAS WRONG, AND HERE IS THE RIGHT ONE.** "Does `voucherFullJSON` emit it?" is
> necessary and NOT sufficient. Ask instead:
>
> > **Does a form REBUILT FROM THAT RESPONSE send it back?** Prove it by ALTERING through the screen
> > and reading the table, with the control that an alter omitting it destroys the row.
>
> ⛔ **THE OTHER FOUR HAVE NOT BEEN ASKED THE RIGHT QUESTION.** `posting` and `stock_movement` are
> re-sent by the form (the alter tests cover them). `posting_cost_allocation` and `order_fulfilment`
> are hydrated by `allocationsFromView` / the fulfilment picker and are *probably* fine — **but
> "probably" is what this table had too.** Re-run all four against the corrected question and write
> the result down here.
>
> ⛔ **`e_way_bill` (0047) IS NOT IN THIS CLASS AT ALL, and the reason is the useful part.** It keys
> on the voucher HEAD, not on `voucher_version_id`, and the voucher path never writes it — so an
> ALTER can neither re-state nor drop it. **But an ALTER still matters there, in a quieter way**: the
> stored payload silently stops describing the invoice it names, and nothing connects the two.
> `generated_from_version_id` records what it was built from and `stale` is DERIVED on every read,
> never stored. **That is the second shape to look for**: a table that is *about* a voucher without
> being *part* of one is safe from the deletion hazard and exposed to the staleness one.

### 4.4 ✅ T2's ALLOCATION UI — DONE at `a86283b`. What is left around it.

The row is `on` (§1.2). Two gaps remain, and both are worth knowing before you touch a
voucher form:

- ⛔ **A JOURNAL CANNOT BE KEYED FROM SCRATCH IN THIS BUILD AT ALL.** `journal` is
  `entry: "double"`, and VoucherScreen's `double` branch renders **no posting grid** —
  the form falls through to `null` after Date / No. / Party / Narration.
  `buildPlainRequest` forwards `f.postings`, which is only ever populated from a LOADED
  voucher (alter) or an OCR draft. So F7 opens a form that cannot produce a voucher.
  This is pre-existing, it is not what T2 was blocked on, and it is now stated on the
  cost masters screen rather than left to be discovered. **It is probably the single
  biggest remaining hole in accounting parity** — a Journal is how every adjustment,
  provision and reclass is keyed.
- Allocation on an ITEM INVOICE is refused outright **and always will be** — its
  postings are generated inside the server's transaction and index 0 is the party leg.
  That is a decision, not a gap; the screen says so.

### 4.5 ⛔ ITEM 2 — A DOUBLE-ENTRY POSTING GRID, SO A JOURNAL CAN BE KEYED AT ALL

**F7 opens a form that cannot produce a voucher.** `journal` is `entry: "double"` and
VoucherScreen's `double` branch renders no posting grid — after Date / No. / Party /
Narration the panel chain falls through to `null`. `buildPlainRequest` forwards
`f.postings`, which is only ever populated by hydrating a LOADED voucher or an OCR
draft. Nothing on the screen can add a posting.

This was found while looking for somewhere to put the allocation control (§1.2) and it
is pre-existing, not a regression. It matters more than its size suggests:

- **A Journal is how every adjustment, provision, depreciation and reclass is keyed.**
  It is the voucher an accountant uses most at period end, and this build cannot key one.
- The F11 row "Double-entry vouchers (Contra, Payment, Receipt, Journal, Sales,
  Purchase)" reads `on`, which is **overstated for Journal specifically**. Correct that
  row in the same change (§9.1) — do not flip it to `off`, because five of the six are
  genuinely reachable; say which one is not.
- It also unblocks allocation on a journal, which currently has to arrive over HTTP.

⚠️ **The single-entry panel is NOT the model to copy.** It infers the primary leg and
puts the SUM on it; a double-entry grid states both sides and must refuse an unbalanced
body in the form (the server refuses it too, but a 422 does not survive the round trip
in a shape the operator can act on). ⛔ And `posting_seq` for an allocation on such a
grid is **0-based with NO offset** — `buildCostAllocations(rows, 0)` — because the rows
map one-for-one from index 0. That parameter exists for exactly this second call site.

### 4.6 ITEM 3 — SEED T3, T2 AND T5 INTO `seed:demo`

`frontend/scripts/seed-demo.mts`. Nothing seeds cost data, TDS data or orders, so **four screens render
empty against a fresh demo company** and cannot be demonstrated. One axis, two centres, one allocated
voucher, one order and one partial delivery would fix all of it. Same unclaimed work T2, T3 and T6 all
left behind.

⚠️ **THE DEV DATABASE NOW HAS SOME OF THIS AND THE SEED SCRIPT STILL DOES NOT** — see §11's residue
list. That is the worst of both worlds for anyone who reasons from what they see on screen, so this
item is more urgent than it was.

⚠️ `seed-demo.mts`, `verify-demo.mts` and `corpora/validate.py` are **CO-OWNED**. ADD only, and
re-read immediately before each edit.

### 4.8 ⛔⛔ NEW AND UNFIXED — DRILLING INTO A NON-SALES VOUCHER RE-CLASSES IT

**Found 2026-08-06 while browser-verifying Track A. It is not Track A's, it is pre-existing, and it
is the cheapest serious defect currently known.**

`navMachine.ts` (~line 546) pushes `newVoucherFrame(DEFAULT_LAST_VOUCHER, "alter")` for **every**
voucher-bearing drill, and `DEFAULT_LAST_VOUCHER` is `"sales"`. The machine has only the row key (a
voucher id) at that point, so it cannot know the class. Then `editActions.acceptVoucher` routes the
PUT by **`ctx.type`** — the SCREEN's type — not by the loaded voucher's class.

**PROVED against the mock, by driving the shell:** post a Payment, drill it from the Day Book, press
`Ctrl+A` → **`alterSalesVoucher` is called, `alterPaymentVoucher` is not.** The frame even titles
itself *"Sales Voucher (Alteration)"* over a voucher numbered `RCPT/1`.

Three consequences, and the first is the one that matters:

- **A Receipt / Payment / Contra / Journal opened from the Day Book and saved is re-classed as a
  Sales voucher.** That is a corrupted document, not a cosmetic error.
- The Sales GST form renders over a single-entry voucher, so the particulars are not editable.
- ⛔ **It makes the single-entry bill-reference hydration unreachable and therefore unverified**
  (§1.5) — you cannot drill into a Receipt to test it.

> **THE FIX IS A DECISION, NOT A ONE-LINER.** Either the machine learns the class (the drill would
> have to carry it from the report row, which knows it), or the screen corrects the frame after
> `getVoucher` resolves, or `acceptVoucher` routes by the LOADED voucher's class and the title
> follows it. The third is smallest and the first is most honest. ⚠️ Whichever you pick, **the
> browser pass is the acceptance test**: drill each of Payment / Receipt / Contra and save.

⚠️ **AND WHY NOBODY CAUGHT IT:** §5.5 lists the Day Book as "known-good, driven end to end". It was
— **with a Sales voucher**, the one class for which the default is accidentally right.

### 4.7 SMALLER, REAL, AND EACH WORTH DOING

| # | What | Why |
|---|---|---|
| 5 | `ReviseLineQty` on the wire | The service method and 0042's trigger exist; no route calls it, so a committed quantity cannot be corrected from a client. The order register SAYS SO on its face rather than hiding it |
| 6 | `VersionView` does not project cost allocations | The edit log SIGNS them but does not SHOW them: an auditor opening an altered pay run sees two versions that render identically. ⚠️ It does not project `fulfilments` either, and that is now the same gap twice |
| 7 | The pre-flight allocation refusal names the axis by raw UUID | Strictly *less* usable than the DB trigger message it exists to pre-empt. Resolve names at the in-transaction call site, which already holds the tx |
| 8 | The two cost reports disagree about an off-axis allocation | The category summary excludes it and reports it separately; the centre summary silently includes it. Neither says so |
| 9 | Fold `tsc` into `make verify` | ~10 minutes, and it closes a whole class of "green but broken". **Still not done, and it caught a real error this session** — `printTarget.ts`'s exhaustive `Record<ReportName, string>` failed only under `tsc`, never under `make verify` |
| 11 | ✅ **DONE 2026-08-06 — bill references are built, settled and reconciled** | §1.5. Was: "no screen builds `bill_allocations` on any voucher path", i.e. money nobody can chase. ⚠️ **AND THE DIAGNOSIS IN THIS ROW WAS *STILL* INCOMPLETE after being corrected once** — it said "the fix is client-side", which was false: the voucher number is minted inside the post transaction, so a browser CANNOT name the bill it opens and the server had to stamp it. It also missed that settlement is a second feature. **A claim that has been corrected once is not thereby right.** Left here rather than deleted, per §9.1 |
| 12 | An e-way bill cannot be raised against a DELIVERY NOTE | §4.2. Real, ordinary, and NIC supports it (docType `CHL`). Needs a decided rule for deriving the consignment value from the movement; `ErrNoTaxLines` refuses by name rather than guessing at a figure the threshold test depends on |
| 10 | No tracking numbers | RESEARCH.md:622 — Tally stitches Delivery Note ↔ Sales invoice with a **Tracking No.**, and creates "bill pending" entries when goods and bill arrive separately. T3 links a voucher to an ORDER but not to another VOUCHER. The order register states this gap |

## 5. ⛔⛔ THE PARITY GAP WITH TALLY, ORDERED. READ THIS BEFORE PICKING A TRACK.

`PLAN-TALLY-PARITY.md` §4 is still the authority on WHAT each track is. **This section is the
ordering, and it deliberately differs from that file's** — the plan was written before Tier 1 existed
and before the audits below were run, so its sequence (T9 → T10 → T8 → T11 → T12 → T13 → T14) is a
technical dependency order, not a parity order. What follows is by **how much of a real business's day
is blocked**, with the reason stated so it can be argued with.

### 5.1 THE GAP IS NOT WHAT THE `off` ROWS SAY IT IS

Three distinct kinds of gap, and only the first is on the FeaturesScreen as `off`:

| Kind | Count | Where it shows |
|---|---|---|
| **`off`** — not built, honestly stated | 11 | F11, and the keymap answers when the key is pressed |
| **`server`** — built, tested, reachable over HTTP, **NO SCREEN** | **3** | F11, and this is the kind that gets re-bought by an accountant who already owns it |
| ⛔ **HIDDEN — a row that reads `on` with a hole behind it** | **≥1 known** | nowhere. Found only by driving the app |

**The third kind is the dangerous one and it is why §12's browser-evidence rule exists.** One is known
(§4.5, the Journal), and it was found by accident while looking for somewhere to put a control. **There
is no audit that has been run for the others.** See §5.5.

### 5.2 ⛔ THE THREE `server` ROWS — BUILT AND UNREACHABLE. Highest value per unit of work.

These are not missing features. They are **finished engines with no door**, which is the state this
project has now shipped four times and which §4.1 exists to stop.

| Row | Why it matters | What is actually missing |
|---|---|---|
| **e-Invoice / IRN** | ⛔ **Statutorily MANDATORY** in India above a turnover threshold. An accountant who cannot raise an e-invoice cannot use this product as their books at all | A screen, and — the real blocker — **a decided way for a browser to trigger generation at all**, which T5 did NOT settle (see below). The workflow, the freeze-on-pin, the retry classification and `POST /vouchers/{id}/external-reference` all exist |
| **Payroll statutory** (EPF/EPS/EDLI, ESI, salary TDS, pay run) | Any business with employees. The whole engine, the statutory rates and the file emitters are built | Screens only. ⚠️ **NO refactor needed** — `internal/payroll` and `payrolldb` are already in `internal/server`'s archtest allowed set, so this is pure frontend |
| **Offline entry with sync** | The differentiator against Tally's LAN-only model, and it is built | A screen, and a decision about how a browser presents queued intents |

> ### ⛔⛔ A CORRECTION: THIS SECTION CLAIMED T5 AND THE IRN SHARE ONE BLOCKER. THEY DO NOT.
>
> The claim was that the `internal/statutory/rules` split would "unblock TWO rows for the price of
> one". **T5 shipped that split at `2299fe7` and it did not unblock the IRN.** The claim is left here
> rather than deleted because it is the plausible one and will be re-invented (§9.1).
>
> Why it was wrong: the split works for the e-way bill because that core is **pure** — no context, no
> error, no clock, no socket, no store — so cored can simply hold it. **The IRN is not pure.** It
> wraps an exactly-once external IRP call in a durable, resumable, memoized workflow, and a crash
> between "generate" and "freeze" mints a second IRN against a real government API. That workflow
> must stay in statutoryd.
>
> ⛔ **SO THE IRN SCREEN'S REAL BLOCKER IS UNSOLVED AND UNDESIGNED: how does a browser trigger it?**
> Two shapes, and both are work: **cored PROXIES** to statutoryd (cored gains a hard runtime
> dependency on a second process that nothing currently runs — there is no `make run-statutoryd`), or
> **the browser reaches statutoryd directly** (which has no PDP, no RLS, no step-up and no company
> scoping, so this is only acceptable behind something that adds them). **Decide that before
> estimating the IRN screen.** What it DOES inherit from T5 is the shared snapshot vocabulary in
> `internal/statutory/rules`, which cored can name — a head start, not a path.

### 5.3 THE ORDERED LIST, WITH REASONS

1. **The e-Invoice / IRN screen** — §5.2, and **§1.4 B has the concrete plan**. **Statutorily
   MANDATORY**, and the highest-value item on this list. ⚠️ It does NOT inherit a finished path from
   T5 — read the correction above before estimating it, because the browser-to-statutoryd question
   is still open and is the first hour's work.
2. **A double-entry posting grid** — §4.5. A Journal cannot be keyed AT ALL. This is accounting parity,
   not a feature: it is how every adjustment, provision, depreciation and reclass is entered. ⚠️ It is
   also the only known HIDDEN row (§5.1), so it is the one gap whose size is proven rather than
   guessed.
3. ✅ **`bill_allocation` on every voucher path — DONE 2026-08-06** (§1.5). It was billed here as
   "the cheapest thing on this list, needs no architectural decision" and **it was neither**: it
   needed a server change (the voucher number is minted inside the post transaction, so a browser
   cannot name the bill it opens) and it was two features, because a raise without a settle makes
   Outstandings overstate forever. It took a full session.
   > ⛔ **REPLACED AT THIS SLOT BY §4.8 — the Day Book drill re-classes any non-Sales voucher it
   > opens.** Found while browser-verifying this row. It is genuinely small, it is a corrupted
   > document rather than a cosmetic fault, and it blocks verification of work already written.
4. **T15 Export (Excel / CSV / XML)** — ⚠️ **the most underrated item on this list.** Tally users live
   in Excel; "export this report" is a dozen times a day, and its absence makes every report a dead end.
   `Alt+E` is already bound and already dispatches a screen action, so the plumbing exists.
5. **Payroll screens** — §5.2. Pure frontend, no refactor, a whole module currently invisible — and
   the ONLY one of the three `server` rows with no architectural question in front of it.
6. **T14 Backup / restore, split company** — operationally table-stakes; Tally users expect a company
   to be a file they can carry. Must be tenant-exact: reuse the `testdb` delete-plan census.
7. **T9 FIFO/LIFO** → **T10 stock groups + compound units** — serialise, both touch inventory.
8. **T16 Per-company print templates** — a logo and an address block is what makes an invoice *theirs*.
9. **T8 Multi-currency** → **T11 Budgets** → **T13 Interest** → **T12 GSTR-9**.
10. **SSO / OIDC** — `off` and in no numbered tier. Enterprise-only; leave it last.

⚠️ **T32 (Tally XML import) IS NOT ON THIS LIST AND IS THE COMMERCIAL BLOCKER.** §10 item 1: a business
cannot switch if it cannot bring its books, and what exists **has never met a real Tally file.** It is
not sequenced here because it needs a human to supply an export first — but it outranks everything
above it commercially, so **ask for the file early** rather than discovering the importer is wrong after
a customer tries it.

> ### ⛔⛔ T18 (the Rule 48(4) QR code on a printed e-invoice) IS PERMANENTLY UNSHIPPABLE FROM HERE, AND ITS ROW MUST STAY `off`.
> The payload is signed with the **Income Tax Department's private key**. Anything we generate tells a
> scanner the invoice was registered when it was not. **If a workflow or a session reports T18 done,
> reject it** — there is no amount of engineering here that closes it, and a plausible-looking QR code is
> a forgery rather than a feature.
>
> ⚠️ **THIS IS NOT TRUE OF T5 OR OF THE IRN ITSELF, and the three look alike.** An EBN is just an
> identifier NIC allocates and an IRN is one the IRP allocates, so both become real the day credentials
> exist **with no change to the payload builder**. T18's QR is a SIGNATURE, and we do not have the key.
> One of these three gaps is permanent and two are waiting on §10 item 4.

### 5.4 THE ONE THING NOT TO GET WRONG IN EACH TRACK

| # | Track | The one thing |
|---|---|---|
| **T8** | Multi-currency | A foreign amount, a rate, and the base amount are **three stored values**, not two plus arithmetic. Realised vs unrealised gain/loss are different postings |
| **T9** | FIFO / LIFO valuation | Weighted average's replay state is a strict SUBSET of what FIFO needs, so FIFO is a new arm in `ValuerFor` plus fixtures — **not** a schema change. ⚠️ Closing stock feeds the Balance Sheet, so a valuation change MOVES THE SHEET: re-pin `verify:demo` deliberately |
| **T10** | Stock groups + compound units | A conversion factor makes every quantity in the system *unit-qualified*. Touch the movement primitive with care and **never store a converted quantity you can re-derive** |
| **T11** | Budgets | A budget is a comparison dimension, not a posting. ⛔ It inherits T2's axis model — see §8.1, and the per-(posting, AXIS) discipline the allocation UI already implements |
| **T12** | GSTR-9 annual return | It is a *reconciliation* of the year's returns to the books and must **state where it disagrees** rather than smoothing it |
| **T13** | Interest on overdue bills | Simple vs compound and the day-count basis are configuration with statutory defaults — **cite them** |
| **T14** | Backup / restore, split company | Must be tenant-exact. Reuse the `testdb` delete-plan census: it already knows every tenant table and is anti-rot by construction |
| **T15** | Export | ⚠️ Money is fixed-4 on the wire and Excel will happily eat it as a float. Emit text, not numbers, or a CSV round trip loses paisa |
| **T16** | Per-company print templates | 0065 / 0066 reserved |

### 5.5 ⛔ THE AUDIT NOBODY HAS RUN: WHICH `on` ROWS ARE LYING?

29 rows say `on`. **One has already been caught with a hole behind it** (the Journal, §4.5), found by
accident. The method that finds the rest is cheap and nobody has done it:

> **For each `on` row, name the exact keystroke sequence an operator uses, then DO IT in a browser
> against real cored.** Not a test — the app. The Journal gap survived a full suite, a full
> `make verify`, and two sessions of browser driving on OTHER screens, because nothing ever pressed F7
> and tried to save.

Start with the rows whose feature has a WRITE path, because a read-only screen that renders is mostly
self-evidencing and a write path can be unreachable in a dozen invisible ways. Known-good as of
`2777980` — driven end to end, so do not re-audit these first: F5/F6/F4 single entry **with cost
allocation**, F8/F9 item invoice **including Ctrl+H item mode**, Alt+F8/F9 delivery & receipt notes
**with order links**, Ctrl+F8/F9 order registers, the BOM screen, both order books, F11, the two cost
reports, the Day Book, Alt+D delete, and **Alt+Z e-way bill**.

⚠️ **AND NOTE WHAT THE MOST RECENT PASS COST, because it is the argument for doing the rest.** Driving
ONE new screen end to end found THREE defects (§1.3): a live duplicate-key failure on every item
invoice after a company's first day, a race that silently discarded a successful write, and a
money-visibility hole in a row that reads `on`. **None was findable by any suite**, and two were in
code nobody had changed this session.

⚠️ **AND WRITE THE RESULT DOWN HERE**, per row, as it is checked. An audit whose findings live in one
session's memory is an audit that will be run again from scratch.

#### THE LOG. Append to it; do not start a new one.

| Date | Row / keystroke driven | Result |
|---|---|---|
| 2026-08-06 | **F8 GST sale to a bill-by-bill party → Outstandings → Balance Sheet** | ✅ after the fix. Bill-wise references now raise, settle and reconcile to the paisa (§1.5). Before the fix this row read `on` and **no browser-keyed receivable had ever reached Outstandings** |
| 2026-08-06 | **F6 Receipt settling an open bill** | ✅ after the fix. The picker lists real open bills with ageing |
| 2026-08-06 | **ALTER of a voucher carrying a bill reference** | ⛔ **WAS DESTROYING DATA** — fixed (§1.5 finding 1). §4.3's audit had cleared this table on half a check |
| 2026-08-06 | **Day Book drill → any non-Sales voucher** | ⛔⛔ **BROKEN, UNFIXED — §4.8.** Opens a Sales frame; saving re-classes the voucher. The Day Book was on the known-good list because it had only ever been driven with a Sales voucher |

⛔ **THE METHOD NOTE THAT CAME OUT OF THIS, and it is worth more than the rows:** driving ONE row
end to end is not enough when the row covers a CLASS. "The Day Book works" was established with a
Sales voucher and was false for every other class. **When a screen is parameterised by type, drive
more than one type — the default is always right for exactly one of them.**

---

## 6. ⛔⛔ MIGRATIONS — READ BEFORE TAKING A NUMBER

Highest number in the tree is **0073**. **`0073_order_book_acl.sql` is the highest-numbered
restatement of `seed_company_auth_defaults` (13 PERFORMs) — DERIVE FROM IT.**

> ### ⛔ 0041 AND 0044 ARE PERMANENTLY UNUSABLE AND MUST BE LEFT EMPTY.
> Both were reserved for a track's ACL rules and neither can carry them.
> `seed_company_auth_defaults` is extended only by **restating its whole plpgsql body**,
> `migrations.Apply` runs files in `sort.Strings` order, and **the highest-numbered restatement is the
> only body that survives**. Rules written at 0041 or 0044 are overwritten on every fresh database and
> simply do not exist.
>
> **TWO TRACKS HAVE NOW HIT THIS WALL** (T2 → 0072, T3 → 0073). Stop treating a reserved ACL number as
> a number to try:
>
> ## RESERVE BLOCKS FOR **TABLES**. TAKE THE SEEDER NUMBER AT THE END. EVERY TIME.

Still free **for tables only**: **0048**, Tier 2's **0051–0061**, 0065, 0066.

⚠️ **0047 IS SPENT** — `0047_e_way_bill.sql` (T5). ⛔ **AND T5 CONSUMED NO SEEDER NUMBER AT ALL**,
which is the rule §6.1 states working as intended: `e-way-bill` is a BARE RESOURCE TOKEN, so Data
Entry's `default_access = 'none'` denies it without a single `acl_rule` row. Deciding the resource
SHAPE first is what saved the number.

`backend/migrations/seeder_census_test.go` guards this **statically, with no database**, because the
defect lives in apply order and is invisible on any incrementally-migrated developer machine. **If it
fails, do not route around it.**

### 6.1 Does your track even need an ACL migration? Usually not.

- **A BARE RESOURCE TOKEN NEEDS NONE.** The seeded `Data Entry` level has `default_access = 'none'`
  and every rule names a specific token, so a token never mentioned is **denied**. `tds-challan`,
  `tds-profile`, `cost-centre`, `sales-order`, `purchase-order` and `bom` all shipped this way with
  **zero** ACL rows.
- **A `report:NAME` DOES, MANDATORILY.** Migration 0011 seeds a coarse `allow display report` which
  `internal/authz` matches against *any* `report:NAME` at specificity 1 — so **a new report is GRANTED
  to Data Entry the moment it exists.** The refusal is the thing that must be written, and it must
  write **both** `disallow display` **and** `disallow print`: a report that cannot be displayed but
  *can* be printed is a leak with a paper trail.

**Decide the resource SHAPE first.** It decides whether you spend a migration number at all.

⛔ **AND AN ACL MIGRATION SHIPS IN THE SAME COMMIT AS ITS REPORT.** You cannot front-load it: an
`acl_rule` row naming a `report:NAME` the vocabulary does not know breaks
`printDisallowedBySeededMigrations`' count assertion **and makes the admin API 422 an owner who reads
their own level's rules and saves them back**, while a name in `KnownReportNames` needs a mapped
route. This was learned by trying it.

### 6.2 How to verify an ACL migration, since a green suite proves nothing here

Do all three. **The second catches the real defect.**

1. `go test ./migrations/` — the static census.
2. **Diff the restated body against its base with comments stripped.** For 0073 that was exactly one
   line and a PERFORM count of 12 → 13. Anything else means a predecessor's rules were dropped.
3. **Migrate a scratch database from empty, seed one company through the real
   `seed_company_auth_defaults`, and COUNT** — including a **CONTROL** row from an earlier migration to
   prove your restatement dropped nothing. 0073's controls were `report:bank-statement` (the line 0068
   once dropped: still 2 rows) and 0072's four `report:cost-%` rows.

---

## 7. THE REGISTRATION SURFACE — miss one and it is 403 FOR THE OWNER TOO

### 7.1 A new RESOURCE needs 4

`routeAction()` · `authz.KnownResources` · (`authz.KnownReportNames` + `reportName()` **only if it is
a report**). Gate: `go test ./internal/server/ -run TestEveryCompanyRouteIsMappedAndInVocabulary`.

⚠️ Adding a name to `KnownReportNames` **AUTO-MOUNTS** `GET /print/reports/<name>`; a name with no
`reportName()` case is **default-DENY for everyone including the Owner**. That bug shipped once for
`reconciliation-queue`.

⚠️ A report's route path must be a **FLAT LITERAL spelling its own name**. Need a selector? **Query
parameter** — unless the selector deserves a different *authority*, in which case it needs a second
NAME (that is why there are two order books).

⚠️ **Two count assertions move whenever a report name is added**, and both are deliberate tripwires:
`handler_print_reports.go`'s deferred-set header plus its count in `handler_print_test.go`, and
`printDisallowedBySeededMigrations`. **Update the prose, not just the number.**

⚠️ **A NEW `internal/` PACKAGE NEEDS *TWO* ARCHTEST ENTRIES** — its own allowed-import set, AND an
entry in `internal/server`'s. Only the first passes when run against the package alone; the second
fails only in the full suite.

### 7.2 A new SCREEN needs 15 — documented as 13, then 14, and the 15th was found by `make verify`

The two whose file paths lie: `ScreenKind` is in `frontend/src/keystroke/types.ts` (**not**
`screenRegistry.tsx`), and `actionLabel()` is in `keystroke/keymaps/tallyprime.ts` (**not**
`AppShell.tsx`).

⛔ **THE FIFTEENTH, AND IT IS OUTSIDE `frontend/` ENTIRELY: `corpora/validate.py`'s
`KNOWN_SCREENS`.** A `ScreenKind` this set does not list fails `make verify` with
*"ScreenKind has ['x'] which this file does not list — a fixture naming one would be rejected even
though the screen exists"*. It is a good tripwire and it caught T5 by name. ⚠️ `corpora/validate.py`
is **CO-OWNED** (§4.6): ADD only, and re-read it immediately before editing.

⛔ **THE FOURTEENTH: `frontend/src/screens/printTarget.ts` holds `REPORT_SLUGS`, a
`Record<ReportName, string>`** mapping every report name to its server print slug. Adding a name to
`REPORT_NAMES` makes that record non-exhaustive and **`tsc` refuses to compile** — which is how it was
found, and it is a good tripwire rather than a nuisance. ⚠️ **`make verify` DOES NOT RUN `tsc`**
(§2.1), so this error is invisible to the gate most people run. Map the slug even when the server
DEFERS the report and answers 501: the TDS register and both order books are mapped-and-deferred on
purpose, so `Ctrl+P` says the layout does not exist instead of doing nothing. An absent slug is
indistinguishable from a key that quietly failed.

⚠️ **A `report:NAME` FRAME IS MUCH CHEAPER THAN A `ScreenKind`, and it is the right choice more often
than the list above suggests.** The two order books are registered as `report:Sales Order Book` /
`report:Purchase Order Book` and inherit the report keymap, the report button bar, the title, F2 and a
place in both Go To and Display More Reports **for free**. Pick a ScreenKind only when the screen
carries a create FORM (which needs its own keymap table to hold the voucher keys back) or renders a
shape no report screen does. `cost-centres`, `tds-challans`, the two order registers and `boms` are
ScreenKinds for that reason; the four order/cost REPORTS split on it.

⛔ **`AppShell.tsx`'s `SCREEN_ACTIONS` is the one that ships the key COMPLETELY DEAD** — no error, no
status line. ⛔ **An unbound key is NOT inert**: F5 opens a Payment voucher over your form, F9 a
Purchase, F8 Sales.

Plus, for a **report** screen only: a classification in `FrameOverlays.tsx`'s `FULL_PERIOD_REPORTS` /
`AS_AT_ONLY_REPORTS` / `NO_DATE_DIMENSION_REPORTS`. **A report in none of the three is told its period
does nothing** — that shipped twice.

And **client methods in three files**: `api/client.ts` (declaring it there makes `rest.ts` and
`mock.ts` fail to compile until both implement it — *use that*), `api/rest.ts`, `api/mock.ts`.

> ### ⛔ IF A KEY IS THE FEATURE, THE TEST MUST PRESS THE KEY ON THE REAL SHELL.
> Render `<AppShell/>`, assert the panel is **ABSENT** as a control, `fireEvent.keyDown`, assert it
> appears. Then mutation-prove it by deleting the `SCREEN_ACTIONS` entry. The pattern is in
> `TDSChallansScreen.test.tsx`'s `describe("a screen-owned key is actually ROUTED by the shell")`, and
> `OrderRegisterScreen.test.tsx` takes it further: **every test in that file** drives the shell and
> presses a real combo, because there is no other kind worth writing for a feature whose whole failure
> mode was being unreachable. Deleting `createOrder`/`closeOrder` from `SCREEN_ACTIONS` fails 12 of
> them **and nothing else in the repo** — that is the mutation proof, and it was run.

### 7.3 Two things about the keymap that save ~120 lines a screen

- **`voucherKeysHeldBack(screen, what)`** in `keymaps/tallyprime.ts` generates the twelve
  voucher-key refusals a form-carrying screen needs. It exists because the block was ~120 lines of
  near-identical prose per screen and **the fifth copy is where copies start to drift** — a paste that
  keeps the previous screen's noun tells the operator the keys are held back on a screen they are not
  on. `tds-challans` and `cost-centres` are still hand-written (they predate it) and that asymmetry is
  documented at the helper; new screens use the helper.
- ⚠️ **CHECK WHETHER THE KEY YOU WANT IS ALREADY BOUND TO `unavailable` WITH A STALE NOTE.** `Ctrl+F8`
  and `Ctrl+F9` said "Order processing is not built yet" — they are Tally's real Sales/Purchase Order
  keys (RESEARCH.md:400, :614, :616, :2145–2146). `buttons.test.ts`'s "features this build lacks" loop
  is what forces the note to be corrected instead of rotting: its example has now been re-pointed
  **twice** by features shipping (Alt+F7, then Ctrl+F8). Also correct `F10`'s note — it lists what the
  "Other Vouchers" menu would contain.

### 7.4 ⛔ THE MOCK IS A REAL DELIVERABLE AND MUST REFUSE WHAT THE SERVER REFUSES

*(§9.4 names the two KINDS of mock-fidelity defect and the control that catches the second.)*

`AppShell.test.tsx` renders with `createMockClient()`, so a lenient mock means a green suite on top of
a create path that cannot work in a browser. That happened twice in one session — once for a missing
`created_by` (server 400, mock 201) and once for a response **SHAPE** (the server nests a view under
`cost_centre` and may send it null; the mock returned it flat and `rest.ts` typed the envelope AS the
flat view — a lie the compiler cannot catch, so every field read `undefined` at runtime).

**When you add a mock method, diff its refusals AND its shape against the handler**, not against the
type you wish existed. `frontend/src/api/orders.test.ts` is the pattern: it asserts the REFUSALS and
the arithmetic, with status codes, and it caught three real defects on its first run.

Two of those are worth stating as rules:

- ⛔ **A mock method that THROWS SYNCHRONOUSLY from a `Promise`-typed signature does not reject.** The
  exception escapes past `.catch()` and past `expect(...).rejects`, so a screen sees a crash where the
  server would have handed it a handled 422. **Every mock helper must be `async`.**
- ⛔ **A `View` type that `extends` an `Input` type inherits its optionality, and that is a type that
  LIES ABOUT THE RESPONSE.** `rate?` on the way in is always sent back; `line_no` is *absent* on the
  way in (the server assigns it from position) and *required* on the way back.

---

## 8. THE DOMAIN MODELS YOU WILL INHERIT — get these wrong and it costs three tracks

### 8.1 ⛔ A COST CATEGORY IS AN AXIS. A COST CENTRE IS A POSITION ON IT.

Several axes allocate the **same money in parallel**: one ₹1,00,000 posting is allocated in full across
Departments *and again in full* across Branches. Both are complete and both are correct.

**Therefore the sum-to-posting invariant is per (posting, CATEGORY), never per posting**, and
**anything that totals across axes double-counts** — for two axes it is 2× the cost borne. **T11
(budgets) and T24 inherit this shape.**

### 8.2 ⛔ AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT.

The stock ledger has exactly one primitive — a positive quantity plus a direction — and an order is
neither. Push it through `stock_movement` and closing stock is corrupted, **and closing stock now feeds
the Balance Sheet**. The error compounds with every open order and reconciles against nothing.

`internal/archtest` enforces this: `internal/orders` may import **nothing but `internal/platform/db`**.
⚠️ The dependency runs the OTHER way — `internal/ledger` owns `order_fulfilment`, because a fulfilment
is written inside the voucher's transaction. **The order table is the shared vocabulary, not a call.**

- **Partial fulfilment is the normal case**, and the outstanding balance is the entire point.
- ⛔ **NO `fulfilled_qty` AND NO `status` COLUMN, EVER.** Both are derived by summing
  `order_fulfilment` across **CURRENT, non-deleted** voucher versions. A stored figure goes stale
  undetectably the first time a fulfilling voucher is ALTERED, because an ALTER writes a NEW version
  and re-states its links without visiting the order. `is_closed` IS stored, because "abandoned"
  cannot be computed — an undelivered order and a cancelled one have identical fulfilment histories.
- ⛔ **EVERY READER MUST JOIN `voucher_head.current_version_id`.** Without it a delivery corrected from
  5 to 8 counts as 13. `stock_movement_not_negative` (0025) is the reference implementation.
- **A fulfilment qty is a LINK, not a movement.** That is why refusing over-fulfilment costs nothing.

### 8.3 ⛔ A SCHEMA MAY UNIFY WHERE AN AUTHORITY MUST SPLIT.

Orders are ONE table with a direction column (two head tables mean two copies of every constraint).
The order books are TWO report names (**a query parameter is invisible to the PDP**, so one name makes
"the salesperson may see our pipeline but not our purchase commitments" an unwritable rule).

**Neither should be "made consistent" with the other.** Both files say so at their top.

### 8.4 ⛔ `posting` IS HARD IMMUTABLE AND AN ALTER RE-CREATES EVERY POSTING ROW.

Anything keyed on `posting.id` from *outside* the voucher path goes silently stale.
`posting_cost_allocation` may key on it because `post()` writes it in the same transaction; 0045 and
0069 may not, and store what they matched instead. **Know which case you are in.**

---

## 9. TRAPS — every one of these cost real time

### 9.1 Prose rots. Correct it; do not delete it.

The false version is the plausible one and **will be re-invented**. Claims that died:

- *"a voucher number allocated and then rolled back is a gap in a statutory series"* — **false.** There
  is no sequence in this schema; `voucher_number_counter` is a table row advanced on the same
  transaction, so a rollback gives the number back. It survived because it is what a Postgres SEQUENCE
  *would* do.
- *"what the edit log SHOWS must be what the edit log SIGNS"* — true of postings and stock lines, **not**
  of cost allocations (§4.6 item 6). Borrowing a true sentence into a place where it is false is the
  same defect as writing a false one.
- *"cost_allocations is reachable from a client"* — true of Go, false of TypeScript, for a whole
  feature.
- *"Order processing is not built, and not modelled at all rather than modelled wrong"* — the F11 note,
  **false for a whole release** while the tables, both books and fourteen client methods shipped. It
  read as a considered decision, which is what made it survive.
- *"Order vouchers are not built"* — `F10`'s keymap note, and *"Order processing is not built yet"* —
  `Ctrl+F8`/`Ctrl+F9`'s. All three were corrected by one assertion in `buttons.test.ts` that forces
  the claim to be re-pointed when a feature ships.
- *"NO SCREEN IN THIS BUILD ALLOCATES A POSTING"* — `CostCentresScreen.tsx`'s closing paragraph.
  **Corrected at `a86283b`**, in the change that made it false. It now says where allocation happens
  AND where it still is not reachable (a Journal, and an item invoice by design).
- ⛔ *"createLedger is idempotent server-side (by name within a company)"* — `ensureLedger`'s own doc
  comment, and **the whole helper was built on it.** It was never true. This is the most expensive
  false claim found so far, because it was a claim about ANOTHER LAYER's behaviour: nothing in the file
  that stated it could contradict it, and the mock agreed with it. **When a comment asserts what a
  different service does, that assertion needs a test against that service — or it is a guess with
  authority.**
- ⛔ *"the accounting voucher path writes a `bill_allocation` and the item path does not"* — inferred
  from DATA (seeded invoices had allocations, a browser-keyed one did not) and **false**. The client
  supplies them on every path and **no screen builds one at all**; only `seed-demo.mts` does. Caught
  by a ten-minute grep while writing §1.4, before it shipped as a work plan. **A claim inferred from
  what the database contains is a claim about the SEED SCRIPT until you have read the code.**
- ⛔ *"createLedger is idempotent server-side"*, **THE SEQUEL.** `ensureLedger`'s doc comment was
  corrected at `a86283b` and `ensureOutputTaxLedgers` — **forty lines below it in the same file** —
  went on calling `createLedger` unconditionally for another release. **Fixing the reported call site
  is not fixing the class.** When an `ensure*` / `upsert*` / `getOrCreate*` helper is found wrong,
  grep the file for its neighbours before closing it.
- *"the `internal/statutory/rules` split unblocks TWO rows for the price of one"* — this file's own
  §5.2, and **false**. T5 shipped the split and the IRN screen is no closer, because the IRN's core
  is not pure and its workflow must stay in statutoryd. Corrected in place at §5.2 rather than
  deleted, because it is the plausible claim and will be re-invented.
- *"a new screen needs 14 registrations"* — §7.2, and it is **fifteen**: `corpora/validate.py`'s
  `KNOWN_SCREENS` is outside `frontend/` and was missed by a list that only looked there.
- *"Double-entry vouchers (Contra, Payment, Receipt, Journal, Sales, Purchase)"* reads `on` on F11, and
  is **overstated for Journal**: F7 renders no posting grid, so a journal cannot be keyed from scratch
  (§4.5). Still uncorrected — correct it when you build the grid.

- ⛔ *"the fix is client-side and covers every form"* — §4.7 item 11's own correction, and **false**.
  `allocateVoucherNo` mints the voucher number INSIDE the post transaction, so a client physically
  cannot name the bill it is opening: the name does not exist when the request is built. The server
  had to learn to stamp it. **This claim had ALREADY been corrected once** (from the even more wrong
  "the accounting path writes one and the item path does not") and was still wrong — which is the
  lesson worth keeping: *a diagnosis that has been corrected once is not thereby right; it has been
  wrong once.*
- ⛔ *"`bill_allocation` is emitted by `voucherFullJSON`, so the read-back audit can tick it off"* —
  §4.3's audit, and **true but not sufficient**. Emitting is half the round trip; no form ever sent
  them back, so an ALTER destroyed them for the whole life of the feature. The audit question is
  corrected in place at §4.3, and **the other four tables have not been asked the corrected one.**
- ⛔ *"the Day Book is known-good, driven end to end"* — §5.5's list, and **true only for Sales**.
  Drilling into any other class opens a Sales frame and saving re-classes the voucher (§4.8). An
  audit that exercised one member of a class is an audit of that member.

**And prefer a refusal that takes a company and a period over one that takes no arguments** — the
latter can only make claims about the software, and those go stale silently.

⚠️ **A TEST CAN CERTIFY A BUG, and mine did.** The order book's heading test asserted
`As at ${book.as_on}` and passed on the empty string, so it locked in a heading reading "As at ·". A
test written from the same misunderstanding as the code agrees with it. **The only thing that caught it
was a screenshot.**

### 9.2 A test that CANNOT FAIL is worse than no test

A guard forbidding a money field asked whether a type satisfied `interface{ Total() any }` — a
signature nothing would ever declare — so the branch was unreachable and the test passed
unconditionally **while reading as a guard**. It was found by *adding the very field it forbade* and
watching it stay green.

**Mutation-prove every guard, especially the ones that assert a negative.** A negative assertion has
no natural failing case, so nothing else will ever tell you it is inert. Give each one a POSITIVE
assertion alongside so it cannot pass by finding nothing.

### 9.3 A differential test needs a CONTROL

A refusal that fires **for the wrong reason** reads as coverage and is not. Two probes once "passed" by
tripping a uniqueness constraint before ever reaching the invariant under test. Every refusal test in
`fulfilment_db_test.go` and `orders_http_test.go` carries the control that proves what it refused.

**The newest example is worth copying because the control is the INVERSE of the assertion.** The
round-trip test asserts that altering a narration leaves the order's fulfilment intact. On its own that
is consistent with links being immune to an alter — in which case the test proves nothing. So it alters
a second time with the links **deliberately omitted** and asserts the order DOES re-open. The control
establishes that the behaviour the first half depends on is genuinely dangerous.

### 9.4 ⛔ THE MOCK IS WHERE FIDELITY DEFECTS HIDE, AND THERE ARE THREE KINDS

`api/orders.test.ts` exists because the mock disagreed with the server twice in one session. Three more
have been found since. The second kind is the one no TYPE can catch; the third is the one no TEST can:

1. **A REFUSAL THE MOCK DID NOT MAKE.** The mock ignored `fulfilments` entirely — it did not reject
   them, it **dropped them silently**, so a screen keyed a link, got a 201, and the order book never
   moved. A developer driving the mock would conclude their own code was broken.
2. **A SHAPE THE MOCK GOT WRONG.** `party` and `stock_item` returned **the id that was sent in**, where
   the server LEFT JOINs the master and returns `COALESCE(name, '')`. Both fields are `string`, so
   `tsc` is clean and every quantity assertion stays green **while the operator is shown a UUID**.

**When you add a mock method, diff its refusals AND its shape against the handler**, not against the
type you wish existed. And include the control the fix needs: an id that resolves to NOTHING must come
back `""`, never as the id — a fallback to the id would put the UUID back on screen in exactly the case
an operator can least explain, while making the happy-path assertion pass.

> ### ⛔⛔ AND THE THIRD KIND, WHICH IS THE WORST: A MOCK MORE PERMISSIVE THAN THE SERVER
>
> `ensureLedger` called `createLedger` unconditionally for two releases. `INSERT INTO
> ledger` has no `ON CONFLICT`, so against cored that is a unique-violation — and
> against the MOCK, whose `createLedger` mints a fresh id per call, it is a success.
> **Every one of the 705 tests passed while Payment / Receipt / Contra could not save a
> voucher in a browser** against any ledger the company already had.
>
> This is not the "shape" defect or the "missing refusal" defect. Those are about a
> mock that answers differently. This is about a mock that answers *more generously*,
> and it is invisible by construction: no test written against it can fail.
>
> **The technique that catches it, and it is cheap:** for any client helper whose
> correctness depends on a server REFUSAL, write the test against a hand-rolled client
> that makes that refusal — see `gstVoucher.test.ts`'s `strictClient`. Four tests, and
> they would have caught a bug that shipped and survived a full session's browser
> driving on other screens.
>
> **The smell to grep for:** a helper named `ensure*` / `upsert*` / `getOrCreate*` that
> only ever calls a CREATE route. If it never reads, it is not an ensure.

### 9.5 The all-zeros UUID parses, and `uuid.Nil` is everyone's sentinel

`uuid.Parse("00000000-...-0")` **succeeds**. Two handlers treated it as "absent", so an explicit
all-zeros `parent_id` **lifted an existing child out of its hierarchy** (with a 200 and a truthful
read-back) and an all-zeros filter silently returned **every** row.

**Any handler whose "absent" sentinel is `uuid.Nil` must refuse an explicit zero.** Grep for `uuid.Nil`
comparisons after a `uuid.Parse`.

### 9.6 Never claim a validation that was not performed

Unreachable from here: the **FVU** (no JRE), the **GSTN portal**, the **NIC e-Way Bill API**, the
**IRP**, the **EPFO/ESIC portals**, the **TIN `.csi`** file, and **CBIC's own rule repository** (TLS
chain verification failure, 2026-08-05). Ship the deterministic fake plus the recorded schema and
**say so on the artefact's face**. `internal/statutory/rule138.go` is the worked example of how:
citation, grade, sources, caveat, all travelling with the value.

### 9.7 Operational

- **A browser WRITE needs a passkey, and you CAN drive it. Do not disable step-up.** Any write answers
  403 `STEPUP_REQUIRED` — including from `curl` with a valid dev token. Attach a virtual authenticator
  over CDP and the ceremony completes on its own, because `stepUp()` **enrols this device** when it
  holds none of the listed credentials:
  ```js
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('WebAuthn.enable', { enableUI: false });
  await cdp.send('WebAuthn.addVirtualAuthenticator', { options: {
    protocol:'ctap2', transport:'usb', hasResidentKey:true,
    hasUserVerification:true, isUserVerified:true, automaticPresenceSimulation:true } });
  ```
  Chrome allows only ONE `internal` authenticator per environment (use `usb` for a second), and the
  authenticator lives on the **CDP session** — add it and drive in the same one. **Verified working.**
- **Launch servers detached and RETURN from the command.** `(nohup … &)` inside a command that then
  blocks dies with your own timeout, taking the server with it.
- **Every `cored` restart mints a new ephemeral signing key**, so every saved token dies with it. It
  presents as an unrelated 401.
- **`gofmt -l` EXITS 0 WHILE PRINTING A FILENAME**, so `gofmt -l f.go && echo CLEAN` prints CLEAN on a
  dirty file. **Read the output, not the status.**
- **`gofmt -w <dir>` reformats files you did not touch. Format NAMED FILES.** Pre-existing drift in
  `internal/statutory/{provider,schema,fake}.go` and `closing_stock*.go` is **not yours**.
- **NEVER `git add -A`.** Stage explicit paths and grep the staged list for `zz_`, `scratch`, `probe`,
  `.bak`, `.orig`. A reviewer's `zz_adversarial_authz_probe_test.go` was caught only at staging.
- **`loadPlan` in `internal/platform/testdb` reads the LIVE database**, so a new tenant table fails the
  census with "the fixture seeds X, which the delete plan does NOT cover" until you `make migrate`. The
  message points at `plan.go`; the actual fix is a migration run.
- **The `testdb` fixture may hold only ONE row per table** (`seedCompany` fatals on a second).
- **Money is `decimal.Decimal` server-side, fixed-4 strings on the wire, scaled integers client-side.**
  **Never float64 / ParseFloat / Number() / parseFloat.** Grep every diff. `decimal.NewFromString`
  accepts what `ParseFloat` would silently round — and `ParseFloat` accepts `"NaN"` and `"Inf"`, which
  panic inside the decimal package as an opaque 500.
- **Tolerances are how a real error hides for a year. Reconcile to the paisa.**
- Any Go test that creates a company MUST call `testdb.Cleanup(t, companyID)` or
  `internal/testhygiene` **fails the build**.

---

## 10. WHAT ONLY A HUMAN CAN DO

| # | Ask | Unblocks |
|---|---|---|
| 1 | **A real Tally export** (XML, any version) | **T32 — the commercial blocker.** A business cannot switch if it cannot bring its books. What exists has never met a real file |
| 2 | **A JRE + Protean's RPU zip** | lets us say the TDS return is *validated*, not merely generated |
| 3 | **One real business day on the app** | the highest-value item on this list, and unmeasurable any other way |
| 4 | **IRP / GSTN / e-Way Bill sandbox credentials** (needs a GSTIN) | **T5 for real** — the payload is already correct — and T18's portal half |
| 5 | **EPF/EDLI administrative-charge rate notification**, if issued since 2026-07-30 | ~₹500/establishment/month accruing unbooked. **Do NOT substitute `S.O. 2701(E)`** — that is *inspection* charges, a different levy |
| 6 | **The published text of CGST Rule 138 and 138(10)** into `docs/statutory/` | lets T5's threshold and validity table be **cited** rather than carried as secondary material. CBIC fails TLS verification from here |
| 7 | **Is a cost report something a data-entry clerk should see?** 0072 says no, deliberately | a one-line ACL decision either way, and a business call rather than a technical one |
| 8 | **Delete the ELEVEN spent handoffs** (§11 — `ls HANDOFF-*.md` says eleven, and this row said twelve) | a handoff nobody can trust costs a session to re-verify |

---

## 11. THE SPENT HANDOFFS — and the one thing to do about them

**Live:** **this file**, `PLAN-TALLY-PARITY.md` (§2.5 migration numbers, §2 working rules, §4 Tier 2),
`RESEARCH.md` (Tally keystrokes, citable by line), `docs/design/*` (per-track models),
`docs/statutory/*` (primary sources — **Form 140 only**).

**SPENT — kept only for their reasoning, and safe to delete:**
`HANDOFF-TRACKS-2-3.md` · `HANDOFF-T1-PRINTING.md` · `HANDOFF-PARITY-SPRINT.md` ·
`HANDOFF-CLOSING-THE-GAP.md` · `HANDOFF-SESSION-T29-IMPORT.md` · `HANDOFF-FEATURE-LOCK.md` ·
`HANDOFF-NEXT-THREE.md` · `HANDOFF-TIER-ONE.md` · `HANDOFF-AFTER-T6.md` ·
`HANDOFF-CLEAR-A-TIER.md` · `HANDOFF-NEXT-T3-T5-PARALLEL.md`

That is **eleven superseded files**, and the sprawl is itself the finding each one recorded: every one
re-derived context the last already held. This file is deliberately ONE file that folds all of it, and
the way to keep it that way is to **update this file in place** rather than write a twelfth.

⚠️ **THEY ARE STILL ON DISK, DELIBERATELY.** Deleting them is a judgement about what documentation a
team keeps, and it belongs to whoever owns the repo. **`git rm` them in one commit when you agree** —
the list above is exactly what to remove.

### Left in the dev database on purpose

- **`T6 Drive Co`** (`9466b85c-…`) — three TDS deductions, two challans, a deductor profile.
- **`tally_freshproof`** — a scratch *database* predating all of this. Harmless; drop it.
- ⚠️ **THE DEMO COMPANY NOW HAS T3 DATA, PUT THERE BY A BROWSER AND NOT BY THE SEED SCRIPT.** Measured
  at `7e565a3` in `71fe4e15-…`: one sales order **`SO-BROWSER-1`** (20 MS Angle @ 500, 8 delivered, 12
  outstanding), one BOM **`Standard`** (10 MS Angle yields 3 SS Sheet), one `order_fulfilment` link, and
  a Receipt Note / Delivery Note pair that moved **+50 / −8 MS Angle** — so this residue IS IN CLOSING
  STOCK, which feeds the Balance Sheet. `verify:demo` still passes **33/33** because its assertions are
  differential rather than pinned constants; if you ever pin an absolute figure, this residue is baked
  into it. **The honest fix is §4.5 (seed it properly), not deleting it** — a fresh database still has
  none of this, which is the worst of both worlds for anyone reasoning from what is on screen.
- ⚠️ **T5 LEFT NO RESIDUE, DELIBERATELY, AND THAT IS A CHANGE OF PRACTICE WORTH COPYING.** The
  browser verification keyed a real item invoice (Sales 4, 94,400) and raised a real e-way bill
  against it. Both were then REMOVED — the voucher by `Alt+D` in the app, the `e_way_bill` row by
  SQL — because that invoice **broke `verify:demo`** (32/33, §4.7 item 11). A red gate handed over
  with a note is worse than residue: the next session cannot tell your known failure from their new
  one. **Leave residue only when it costs nothing; never leave a failing gate.**
- The demo company **does** now have cost masters (a `Department` axis with `Sales` and `Admin`) and
  allocated vouchers — including one keyed in a browser at `a86283b` (Electricity 10,000 split
  6,000/4,000), on top of pre-existing HTTP-posted allocations. The Cost Centre Summary reads Admin
  44,000 / Sales 66,000 over FY 2026-27 and reconciles. **Still none of it is in the seed script**
  (§4.6). No TDS data is seeded either.

---

## 12. DEFINITION OF DONE — for every feature from here on

**Tier 1 is closed (§1), so item 1 is no longer about T5.** It generalises, and it is the one that
has caught something every single time:

1. **The feature's FeaturesScreen row is `on`, on BROWSER evidence, not a green suite.** Name the
   exact keystroke sequence an operator uses and DO IT, against real `cored`, with the virtual
   authenticator (§9.7). Every Tier-1 track that skipped this shipped an unreachable backend, and
   the one pass that did it found three defects in one screen (§1.3).
2. `make verify` exit 0 **and** `pnpm exec tsc --noEmit` clean (**different gates** — and each has
   caught a real error the other missed: `tsc` found `printTarget.ts`'s non-exhaustive record,
   `make verify` found `corpora/validate.py`'s `KNOWN_SCREENS`).
3. backend **≥2307 pass, 0 SKIP, 0 FAIL** (verbose census); frontend **≥761 / 56 files**;
   `verify:demo` **33/33 run ALONE** with insightd up **and the company id as an argument** (§2.1),
   re-pinned deliberately if a figure moves — and **the change explained**.
4. ⛔ **THE REACHABILITY CHECK, for every new feature: name the exact HTTP request that exercises it
   end to end, and assert it in a test that reads the result back out through the OUTERMOST surface,
   with no SQL in the fixture that reaches around the API.** T2 passed every other bar on this list
   while being unreachable from every client. **This item exists because nothing else on the list
   catches that.**
4a. ⛔ **THE READ-BACK CHECK, and it is NEW because item 4 does not catch it.** For anything keyed on
   `voucher_version_id`: assert that `GET /vouchers/{id}` returns it, and that an ALTER rebuilt from
   that response preserves it — **with the control that an alter omitting it DESTROYS it** (§4.3, §9.3).
   A feature can pass item 4 perfectly on the way in and silently delete data on the way out. T3 was
   one commit from shipping exactly that, and it would have presented as a warehouse problem.
5. Every new tenant table in `buildFixture` (`internal/platform/testdb/testdb_test.go`) or the teardown
   census fails **by name**.
6. Every new screen carrying **all 14** registrations including `SCREEN_ACTIONS` and
   `printTarget.ts`'s `REPORT_SLUGS` (§7.2), with a **real-shell keypress test** that is
   **mutation-proved** against the `SCREEN_ACTIONS` entry.
7. `PLAN-TALLY-PARITY.md` §2.5 updated for every consumed block, **and §6 of this file obeyed** for
   anything touching the ACL seeder.
8. Every safety-critical invariant **mutation-proved**, and said so in the commit.
8a. ⛔ **ANY CLIENT HELPER WHOSE CORRECTNESS DEPENDS ON A SERVER REFUSAL is tested against a client
   that MAKES that refusal**, not against the mock (§9.4's third kind). A mock more permissive than the
   server cannot be caught by any test written against it.
9. **This file updated in place** — not a thirteenth handoff.
10. ⛔ **THE TREE IS HANDED OVER WITH EVERY GATE GREEN.** If your own verification leaves data that
   fails one, remove the data or fix the defect — do not hand over a red gate with an explanation
   (§11). A known failure and a new one are indistinguishable to the next session.
