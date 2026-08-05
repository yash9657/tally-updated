# HANDOFF — T6 IS ON. THIRTEEN ROWS LEFT, AND FOUR FINDINGS THAT OUTLIVE IT

> **Written 2026-08-05.** Successor to `HANDOFF-TIER-ONE.md`, which stays the plan of record for
> the remaining lanes. `PLAN-TALLY-PARITY.md` §2.5 is still the **only** authority on migration
> numbers.
>
> **Read §1 first.** It is a live security repair and a rule that constrains every future migration.

---

## 1. ⛔ THE FINDING THAT MATTERS MOST, AND THE RULE THAT FOLLOWS

**On any FRESH database, every company created handed its Data Entry level the imported bank
statement** — the bank's own file of every counterparty, balance and narration. Fixed in
`0071_seeder_order_repair.sql`.

`0063_bank_statement_acl` was authored 2026-08-03, correctly derived its `seed_company_auth_defaults`
body from 0068 (its header says so), and was numbered **five lower**. `migrations.go:71` applies in
`sort.Strings` order, so 0063's correct 11-PERFORM body installs first and 0068's older 10-PERFORM
body then `CREATE OR REPLACE`s it, dropping `seed_data_entry_bank_statement_rules` — the only
specificity-2 counterweight to 0011's coarse `allow display report`.

**Measured, not reasoned about:** a scratch database migrated from empty, one company seeded, gave
29 Data Entry report rules — the coarse allow plus disallows for fourteen names — and **no
`report:bank-statement` row in either action**.

### The rule, because six headers state one that is necessary and not sufficient

Every restatement header says "derive from the immediately preceding one". Both files obeyed.
`CREATE OR REPLACE` order is decided by the **filename**, not by authorship, so deriving correctly
and numbering below is indistinguishable from deriving from a stale base.

> **A seeder restatement may never take a number below an existing restatement, reserved block or
> not. Leave the gap unused and take a number at the end.**

`migrations/seeder_census_test.go` now guards this **statically, with no database**, because the
defect lives in apply order and is invisible on any incrementally-migrated DB — it was green on
every developer machine and would have been red on the first fresh deploy. Mutation-proved.

---

## 2. THREE MORE FINDINGS, ALL UNFIXED, ALL SMALL

### 2.1 The back-dated cutoff is INERT on two more routes, and both claim otherwise

`voucherDateFromRequest` peeks the body for a date. It knows `voucher_date`, `wage_month` (the pay
run) and now `deposit_date` (the challan — fixed this session, `peekDepositDate`). It does **not**
know:

| Route | Carries | Comment claims |
|---|---|---|
| `POST .../bank-reconciliation` writes | `bank_date` | "a bank date asserted into a closed period is…" |
| `PUT .../deductor-status/{taxYear}` | no date at all | "correctly inherits the back-dated cutoff" |

`cutoffDeny` returns early on a nil date, so on both routes the cutoff **never evaluates** while the
prose says it does. A comment asserting a control that never runs is worse than a missing control,
because the next reader stops looking. Each needs its own product call (is a bank date a back-dated
act? is recording a deductor status for an old year?), which is why they are handed back rather than
guessed. The pattern to copy is `peekDepositDate`, and the test to copy is
`TestAChallanDepositedIntoAClosedPeriodIsRefused` — it carries a CONTROL leg (an ordinary journal
must also be refused, or the 403 proves nothing) and an ALLOWED leg (a write after the cutoff must
still work, or a fix that refuses everything passes).

### 2.2 `make verify` does not run `tsc`

I introduced a type error, ran `make verify`, got exit 0, and shipped it into a commit. An agent
found it later. **`make verify` is not a typecheck** — `pnpm exec tsc --noEmit` is a separate gate
and the handoff lists it separately for this reason. Consider folding it in; until then, run it by
hand and do not read "verify passed" as "types are fine".

### 2.3 Migration 0069's header contradicts the code, and a human must pick a side

0069 lines ~203-210 say "a part payment is modelled as an allocation smaller than the deduction".
`challanRefusalFor` tests **inequality in either direction**, so a deliberately partial allocation
is indistinguishable from one broken by a voucher ALTER — and the whole quarter then refuses with a
message about a change nobody made. This slice refuses a partial allocation **at entry** instead,
which is the better of the two available answers but not a third one. Either correct 0069's header,
or model part payment properly (a distinguishable third state — a schema change, not a Go change).

---

## 3. WHAT T6 SHIPPED, SO NOBODY REBUILDS IT

`FeaturesScreen`'s TDS/TCS row is **`on`**, flipped on browser evidence, with the file gap stated in
the note.

| Layer | Where |
|---|---|
| challan write | `masters/tds_challan.go` — one atomic create, no edit/delete/re-allocate |
| profile write | `masters/tds_profile.go` — whole-row upsert on `company_id` |
| routes | `GET/POST .../tds-challans`, `GET/PUT .../tds-profile` — **bare tokens**, no ACL migration |
| picker read | `ListAllocatableDeductions`, served on the challan GET |
| filing status | `reporting.ReturnFileStatusFor` — per company, per period, names your vouchers |
| screens | `TDSChallansScreen.tsx` (13 registrations) + a profile panel on F11 |

**`available` is still false and that is deliberate.** Nothing emits the file: no route serves the
bytes, and the header needs four facts in neither the books nor this software (file sequence number,
RPU name, whether a prior statement was filed, its token). Inferring any of them puts a false
statement on a tax return. The last blocker says the gap is **ours, not the operator's** — and when
the books are clean the panel says so rather than showing a generic refusal.

### The defect worth carrying forward as a pattern

The first version of the entry form **could not be completed by a human**, and every test was green.
It asked for a voucher UUID and a 0-based leg ordinal — identifiers **no screen in this product
displays** — and passed because the tests keyed mock fixture ids the real server rejects as
non-UUIDs. Ask of any new form: *can a human obtain every value it demands, from this product?*

---

## 4. THE PARALLEL-WORKFLOW LESSONS, MEASURED

Three workflows ran (5 scouts, 4 backend, 4 frontend, 1 fix). What actually happened:

1. **A lane's "green" is weak evidence, exactly as §3.4 predicted.** 22 challan and profile tests had
   **never been executed** — the agents were held off the shared Postgres by design. All 22 passed on
   first run, but that was luck, not proof.
2. **The manifest came back duplicated 2×** — twelve entries for six registrations, one naming a
   handler that did not exist. Deduplicating was hand work.
3. **The manifest asserted a guardrail that is false:** it claimed chi PANICS on a duplicate
   method+pattern. It does not — `setEndpoint` overwrites silently. Applying all twelve would have
   compiled, passed every test, and left four registration points with two contradictory comment
   blocks each.
4. **The adversarial reviewer earned its cost twice**, finding the unusable-form blocker and the
   inert cutoff. Both were invisible to the suites. Budget for it.
5. **Barriers dominate wall clock.** The frontend workflow took 58 minutes for what was ~20 minutes
   of parallel work, because the API layer had to land before the screens and the review after them.
   If you need speed, review inline rather than as a phase.

---

## 5. ⛔ THE OPERATIONAL TRAPS I RE-DISCOVERED THE HARD WAY

- **`/healthz` IS NOT A VERSION CHECK.** A cored 1h03m old answered **200** while my new process died
  with `bind: address already in use` into a log nobody reads. Poll for a **field only the new build
  serves**. And my own probe hid it: `d.get('key', [])` made a *missing* key print as "count: 0".
  Never default a probe you are using to detect staleness.
- **`pkill -f "cmd/cored serve"` misses the compiled child.** `go run` spawns
  `…/go-build/…/cored serve`. Kill by PID from `lsof -nP -iTCP:8087 -sTCP:LISTEN`.
- **Launch detached and RETURN.** `(nohup … &)` inside a command that then blocks dies with your own
  timeout — mine SIGTERM'd the process group and took the server with it.
- **Every cored restart mints a new ephemeral signing key**, so every saved token dies with it. That
  presents as an unrelated 401.
- **A browser WRITE needs a passkey, and you CAN drive it — do not disable step-up.** The seeded
  Owner level has `requires_webauthn`, so a write answers 403 `STEPUP_REQUIRED` and the shell raises
  "Confirm the passkey prompt". Headless Chrome has no authenticator by default, so attach one over
  CDP:

  ```js
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('WebAuthn.enable', { enableUI: false });
  await cdp.send('WebAuthn.addVirtualAuthenticator', { options: {
    protocol: 'ctap2', transport: 'usb', hasResidentKey: true,
    hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
  ```

  Then the ceremony completes on its own, because `auth/webauthn.ts`'s `stepUp()` **enrols this
  device** when it holds none of the credentials the server lists — the branch written for "the user
  has passkeys but not on THIS device". Two gotchas: Chrome allows only ONE `internal` authenticator
  per environment (use `transport: 'usb'` for a second), and the authenticator lives on the CDP
  session, so add it and drive in the SAME session.

  I refused to disable `requires_webauthn` to get around this and the permission classifier refused
  it too, correctly — it is a security control, and turning it off would have proved the feature
  works in a configuration no real tenant runs.

---

## 6. STATE OF THE TREE

| Gate | Value |
|---|---|
| backend `go test ./... -count=1 -v` | **2126 pass / 0 SKIP / 0 FAIL** (was 2026) |
| frontend `pnpm run test:run` | **507 pass** (was 464) |
| `pnpm exec tsc --noEmit` | clean |
| `make verify` | exit 0 |
| `verify:demo` | **33/33** |
| `gofmt -l` | clean on every file touched (pre-existing drift in `closing_stock*.go` is NOT mine) |

**Migrations:** 0071 consumed by the security repair. T6 needed **no** ACL migration. Next free is
**0072**, which `HANDOFF-TIER-ONE.md` §3.5 allocates to T17 SSO — still fine, it sorts above 0071.

**The browser drive, in full, because this is the gate that finds what tests do not:**
Gateway lists `TDS Challans` → Enter opens it with its OWN button bar → **Alt+C opens the form on the
real shell** (the 13th registration point) → the picker shows already-deposited rows with no checkbox
and one selectable → **Ctrl+A** → 403 `STEPUP_REQUIRED` → passkey ceremony → **the challan is
recorded**: 05123, BSR 0004329, 2026-07-05, ₹9,000, against the deduction actually ticked. The
register then lists it. Then Alt+G → `TDS / TCS Register` → **F2 offers BOTH dates** (the
period-classification fix, on the screen that used to be told its period did nothing) → and the
filing panel reads **"YOUR BOOKS ARE READY: all 3 deductions in this period have a challan behind
them"** — because of the deposit just recorded through the UI. That is the whole loop in one browser.

**Two things I left in the dev database, deliberately:**
- A company named **`T6 Drive Co`** (`9466b85c-…`) with three TDS deductions, two challans and a
  deductor profile — one of those challans recorded through the UI. It exists so the browser drive
  did not pollute the demo. Drop it when you like.
- **The demo company has no TDS data at all** — the seeder creates no TDS vouchers — so both new
  screens render empty against `?company=$(cat .demo-company-id)`. Seeding one deduction and one
  challan into `seed:demo` would make T6 demonstrable; it is a small, real piece of work nobody has
  done.

---

## 7. WHAT IS LEFT OF TIER 1

Thirteen rows, unchanged from `HANDOFF-TIER-ONE.md` §2 — its lane architecture still holds, minus
Lane 5, which is now done:

| Lane | Domain | Sequence |
|---|---|---|
| 1 | ledger payload / money | **T2 → T11 → T8** |
| 2 | inventory (critical path) | **T9 → T10 → T3** |
| 3 | reports | **T12 → T15** |
| 4 | printing | **T16** |
| 5 | auth | ~~T17 → T6~~ → **T17 only** |
| 6 | statutory | **T5 → T13** |
| 7 | alone, last | **T14** |

**T18 (the Rule 48(4) QR) is still permanently unshippable from here** — the payload is signed with
the Income Tax Department's private key, so anything we generate tells a scanner the invoice was
registered when it was not. `HANDOFF-TIER-ONE.md` §0 is unchanged and still governs.

**And the standing rule that governed every hour of this session:** never invent a rate, a code, a
field order or a schema. Cite it or refuse. Four times in this one feature a claim outlived its own
truth — the layout that "had not been read", the challan table that "did not exist", the company
that "has no TAN on file", and a comment asserting a cutoff that never ran. Each was written
correctly and rotted. **Phrase a refusal as a question about the books, not as a fact about the
software:** the first is re-answered on every request and cannot rot; the second goes stale silently.
