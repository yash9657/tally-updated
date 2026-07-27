# WALKTHROUGH — every feature, how to drive it, how to prove it

A guided tour of what this system does, in the order a Tally user would meet it.
Every "**Verified**" line below was run live against the local stack on
2026-07-25 and shows the actual output, not an expectation.

Two rules this document follows:

1. **Nothing is claimed that was not run.** Where a feature is missing, it says so.
2. **Every keystroke is real.** If a key is bound but its feature is not built, the
   app says that out loud in the status line — see §2.4.

---

## 1. Running it

```bash
make dev-db                       # Postgres 16 on :5433
cd backend && go run ./cmd/cored migrate
AUTH_DEV_TOKEN_ENABLED=true PORT=8087 APP_DSN=... go run ./cmd/cored serve   # cored  :8087
cd insightd && uvicorn insightd.api.app:create_app --factory --port 8088     # insightd :8088
cd frontend && pnpm dev                                                       # UI     :5173
# optional, for e-invoice / IRN:
SVC=$(curl -s -XPOST localhost:8087/auth/token -d '{"subject":"svc:statutoryd"}' | jq -r .token)
CORED_BASE_URL=http://localhost:8087 CORED_SERVICE_TOKEN=$SVC PORT=8091 \
  STATUTORYD_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" ./statutoryd
```

`frontend/.env.development.local` with `VITE_LIVE=1` points the UI at the real
backend (the dev server proxies `/v1`, `/auth` → cored and `/companies` →
insightd, and auto-mints a dev owner token). Delete that file to run the UI
against its in-memory mock instead.

### 1.1 The demo company — seed it, then open one link

There is **no pre-existing demo company**: the UI mints an empty `My Company` per
browser profile, and every other company in the database is throwaway test data.
Seed a real one:

```bash
cd frontend && pnpm seed:demo              # books only
cd frontend && pnpm seed:demo -- --insightd # …and a pending Bill Inbox draft
```

It prints `OPEN IT: http://localhost:5173/?company=<id>` — that URL selects the
company and persists it, so later plain reloads stay on it.

**Demo Traders** contains, deliberately, one of everything a screen can show:

| What | Why it is there |
|---|---|
| 2 customers + 2 suppliers, one in Karnataka and one out of state | both GST paths — CGST+SGST **and** IGST — in the same books |
| Opening capital ₹5,00,000, cash + bank | the Balance Sheet has an equity side and two cash accounts |
| 3 sales, 2 purchases across Apr–Jul 2026 | the F2 period picker, Day Book range and cash-flow buckets all have something to move |
| Bill-wise refs on every party voucher, **one part-paid** | Outstandings shows a fully open bill *and* a partly settled one, so ageing is non-trivial |
| Receipt, Payment, Contra, Journal | all six voucher types (F4–F9) have real entries behind them |
| 1 **altered** voucher (version 2) | the append-only edit trail exists in the data |
| 1 **deleted** voucher (tombstone) | it vanishes from reports while its versions remain |
| 1 pending OCR draft (`--insightd`) | the Bill Inbox → F9 prefill flow has a bill to accept |

**Verified after seeding:** Trial Balance Dr = Cr = 722080.0000 (balanced),
Balance Sheet assets = liabilities = 609480.0000, 2 receivable parties totalling
145280.0000; in the browser the title bar reads *Demo Traders*, the Day Book lists
the entries, and the Bill Inbox shows `Acme Components Pvt Ltd · 1180.0000 · ready · 95%`.

> **Seed under the right identity.** Company membership is per-subject, so the
> seeder authenticates as `browser-dev-owner` — the same subject the dev UI uses.
> Seeded under any other subject, cored answers 403 and the shell quietly opens a
> fresh empty company instead (it now logs a console warning saying exactly that).
> Override with `SEED_SUBJECT=…` if you point the UI at a different identity.

**One honest wrinkle in the data:** the altered invoice (INV-003) does not restate
its bill-wise reference, so Outstandings ages the original amount. That is the
documented over-count on altered vouchers, left visible rather than papered over.

**The gates**, all re-runnable:

| Gate | Command | Last result |
|---|---|---|
| Frontend unit + keystroke corpora | `cd frontend && pnpm test:run` | 210 passed (23 files) |
| Types | `cd frontend && pnpm exec tsc --noEmit` | clean |
| Live in-browser smoke (real keyboard) | `cd frontend && node scripts/e2e-ui-smoke.mjs` | 21/21 flows |
| Voucher edit loop / numbering / GST state | `pnpm e2e:edit`, `e2e:numbering`, `e2e:state-code` | OK |
| OCR draft → F9 prefill → accept | `pnpm e2e:ocr` | OK |
| IRN + freeze + durable restart | `python3 backend/scripts/e2e-statutory-freeze.py` | STATUTORY E2E OK |
| Backend | `make backend-test` | (unchanged this session) |

---

## 2. The keyboard — the part a Tally user judges you on

### 2.1 The grammar

| Key | What it does | Where |
|---|---|---|
| `Enter` | next field / drill down one level | everywhere |
| `Esc` | back one level — **and it cancels an open sub-screen first** | everywhere |
| `Ctrl+A` | accept (save) the screen | voucher, master |
| `Ctrl+Q` | quit the screen **without** saving | everywhere |
| `Tab` / `Shift+Tab` | next / previous field | voucher, master |
| `Alt+G` | Go To — opens anything, keeps your screen (Esc returns) | everywhere |
| `Ctrl+G` | Switch To — replaces your screen (no way back) | everywhere |
| `F1` | Help — the live keystroke reference | everywhere |
| `F11` | Company Features | everywhere |
| `F12` | Configure this screen | everywhere |
| `Ctrl+N` | calculator | everywhere |

### 2.2 Vouchers

`F4` Contra · `F5` Payment · `F6` Receipt · `F7` Journal · `F8` Sales · `F9` Purchase.
Inside a voucher: `F2` date, `Alt+C` create a ledger on the fly, `Ctrl+H` Change
Mode, `Ctrl+A` accept, `Alt+D` delete (alteration), `Alt+X` cancel the entry,
`Alt+2` duplicate, `PgUp`/`PgDn` browse saved vouchers.

### 2.3 Reports (the same grammar on every report)

`F2` period · `F5` ledger-wise ⇄ group-wise · `Alt+F1` detailed · `F12` configure ·
`Ctrl+H` change view · `Ctrl+F` filter rows · `Alt+E` export CSV · `Enter` drill ·
`Esc` back.

Note the context scoping, which is Tally's own: **`F5` opens a Payment voucher
from the Gateway but toggles ledger-wise inside a report**; `F2` is the voucher
date but the report period; `Ctrl+H` is Change Mode in a voucher and Change View
in a report.

### 2.4 No key is ever silently dead

Every documented TallyPrime combo is bound. The ones whose feature this edition
does not have answer in the status line instead of doing nothing:

```
Alt+F7 → "Stock Journal (Alt+F7) — Inventory vouchers are not built yet (no godown/stock movement plane)."
Ctrl+F4 → "Payroll voucher (Ctrl+F4) — The payroll module is not built yet."
F10    → "Other Vouchers (F10) — Order, delivery and stock vouchers are not built yet…"
```

A key that exists but does not apply to the screen you are on says that too
("`Alt+F1` — Detailed (expand all levels) does not apply on this screen."). Press
`F1` on any screen for the full generated list, split into "works here" and "bound,
but not built yet".

**Verified:** live smoke asserts the Alt+F7 message verbatim, and a 68-case unit
suite asserts (a) every button on every bar is bound, is not an unbuilt
placeholder, and does something on that screen, and (b) every documented combo
resolves to an action with a human label.

### 2.5 Mouse parity

The right-hand button bar is clickable and each button fires **the same combo**
the keyboard sends, so the two paths cannot drift. Clicking never steals focus
from the field you are typing in.

---

## 3. Feature by feature

### 3.1 Gateway navigation and type-ahead

Type any part of a menu entry to filter, then `Enter` once to open it.
`Display More Reports` opens the second-level report menu (Trial Balance,
Outstandings, …), exactly like Tally's own structure.

**Verified:** typing `Display` + Enter → the report menu; `Trial` + Enter → the
Trial Balance, on live data.

### 3.2 Voucher entry with server-derived GST

`F8`, pick the party, taxable value + rate, `Ctrl+A`.

- The voucher number is allocated server-side from a per company / voucher type /
  financial-year counter (gap-free under concurrency).
- **The tax split is derived and re-verified on the server.** The browser's
  preview never decides it; a mismatch is rejected with 422.

**Verified (UI):** Sales 10,000 @ 18 % intra-state → `No: 1`, CGST 900.0000,
SGST 900.0000. Purchase 4,000 @ 18 % → CGST 360.0000.
**Verified (API):** the same body posted directly returns
`{"cgst":"900.0000","sgst":"900.0000","igst":"0.0000","voucher_no":"1"}`.

Inter-state derivation is covered by `pnpm e2e:state-code` (an explicit `27`
place-of-supply and the company-default both produce IGST) — **OK**.

### 3.3 Alteration and deletion — the audit trail is structural

Drill into a voucher from any report and edit it. `Ctrl+A` writes a **new
version**; `Alt+D` writes a **delete tombstone**. Nothing is ever mutated or
erased, and the voucher number is carried forward, never re-allocated.

**Verified (API):** create → `version_no 1`; alter → `version_no 2`, same
`voucher_no "1"`; delete → `version_no 3, op DELETE`. After the delete the Day
Book no longer lists it and the Trial Balance is still balanced (0.00 / 0.00).

**The Edit Log — `Alt+L` on a drilled voucher.** Every version, oldest first,
with who made it and when, what changed from the version before, and — the part
that matters — the server's **verification of the BLAKE3 hash chain**. A
deleted voucher still has a log; the tombstone is an entry in it. The endpoint
is `GET .../vouchers/{id}/history`.

The verification is why this is evidence rather than a claim. It is proven by a
test that DEFEATS the append-only trigger as the postgres superuser, edits a
stored payload, and asserts the endpoint reports the chain broken at the right
version — then repairs the row and asserts it verifies again
(`TestVoucherHistoryDetectsTampering`, three tamper modes: an edited payload, an
edited amount, and a removed version).

### 3.4 Masters

`Alt+C` from inside a voucher field creates a ledger on the fly and returns you to
the field with it selected. Groups, voucher types, units and stock items exist as
API masters.

**Verified (API):** ledger creation under `Sundry Debtors` / `Sales Accounts` /
`Duties & Taxes` returns ids and the postings against them balance.

### 3.5 Reports, and the universal drill

Balance Sheet, Profit & Loss, Trial Balance, Day Book, Outstandings — all on live
data, money as fixed-4 strings with Indian digit grouping (never a float).

**"No report is a dead end":** `Enter` on any line drills, all the way to the
voucher, and `Esc` walks back up the same path.

**Verified (UI):** `Balance Sheet → Current Assets → Sundry Debtors → Acme
Traders → Sales Voucher (Alteration)` with the voucher loaded (number 1, taxable
4000.0000), then five `Esc`s back to the Gateway.

Report buttons, all verified live in the smoke test:

- `F5` — header flips `Ledger` → `Group` and the rows re-aggregate by group.
- `F12` — toggles apply (banner shows the active view) and `Esc` closes only the
  overlay, keeping the report.
- `Ctrl+F` — filters the rows (6 → 1 on a no-match string).
- `Alt+E` — downloads a real CSV (`Trial-Balance.csv`).
- `F2` — period picker (Day Book and Outstandings are period-aware server-side;
  the picker says so plainly for the reports that are as-of-latest in this build).

### 3.6 Bill Inbox — OCR bill → draft purchase voucher (AI-native)

Gateway → `Bill Inbox`. The AI plane (insightd) drafts a purchase voucher from a
bill; you open it into the `F9` screen prefilled, with per-field confidence and a
PII-redaction count, edit any field, and `Ctrl+A` to accept.

**The AI plane has no write path to the ledger by construction** — insightd's
relationship with cored is read-only (its own `/healthz` advertises
`"cored_relationship":"read-only"`, and a test fails the build if a write leaks in).

**Verified:** `pnpm e2e:ocr` — **OK** (draft → prefill → accept, ids remapped).

### 3.7 Ask your books — plain English over the ledger

Gateway → `Query`. Natural language compiles to a **closed query DSL**, then to
parameterized, read-only, tenant-scoped SQL.

**Verified (API):** `"sales this year"` → a DSL over `vouchers` summing
`taxable_value` by month; `"top debtors"` → `outstandings` ordered by pending.
An injection attempt — `"sales this year; DROP TABLE voucher_version; --"` —
compiles to the *same safe DSL*: the text never becomes SQL.

### 3.8 GST returns

**Verified (API):** `GET /gstr1` → `b2cs` line `{pos:"29", rt:"18", txval:"10000",
camt:"900", samt:"900"}` plus the HSN block; `GET /gstr3b` → `sup_details.osup_det`
with the same figures. Both derived from the posted vouchers, not re-entered.

### 3.9 e-Invoice (IRN) and freeze-on-external-reference

statutoryd mints an IRN through a durable, idempotent workflow, then calls cored's
freeze hook, which **pins the voucher number and hard-blocks any later edit**.

**Verified (live, two binaries):**
```
IRN minted: b3f24abba808184c… status=ACT pinned_no=1
freeze-hook idempotent re-register ok
ALTER frozen voucher -> 409: IRN
DELETE frozen voucher -> 409
recon queue after blocks: ['ALTER', 'DELETE']
voucher intact after blocked edits: version 1
```
…and, with the durable step store, statutoryd was **killed mid-workflow and
restarted**: the same IRN came back and the `generate` step's timestamp was
unchanged, proving the IRP was called exactly once.

### 3.10 Offline sync sequencer

An offline node posts an outbox of intents with its own provisional numbers; the
cloud sequencer assigns authoritative numbers and de-duplicates replays.

**Verified (API):** two nodes that both claimed provisional `OFF/1` were committed
as final numbers `2` and `3`; replaying the identical outbox returned
`status: DUPLICATE` with the same final numbers — no double-post.

### 3.11 Migration from Tally (the CA's veto)

`POST /import/tally-xml` parses a Tally export and posts every voucher through the
same ledger path as manual entry, so it inherits every invariant.

**Verified:** importing `corpora/migration/01-opening-trading.xml` into a fresh
company reproduced the corpus's **independently hand-computed** oracle exactly —
Trial Balance 132000.0000 / 132000.0000 balanced; Balance Sheet assets =
liabilities = 109000.0000; net profit 9000.0000.

### 3.12 Cash-flow projection, anomalies, forecast

- `GET /cash-flow` — deterministic weekly projection with opening/closing cash.
- `GET /anomalies` — rule-based findings. **Verified:** it flagged the voucher I
  had just posted as `AFTER_HOURS` ("entered at 22:39 IST") and `ROUND_AMOUNT`.
- `POST /companies/{id}/forecast/cashflow` (insightd) — Holt-Winters, optional
  dependency. **Verified:** 60 weeks of history → `method:"holt-winters-additive"`,
  a 4-week forecast that tracks the seasonality against a flat naive baseline.

### 3.13 Security

- Every company-scoped route is authn'd + policy-enforced; tenancy is enforced in
  the database by RLS, not only in the application.
- **Verified:** no token → **401**; another tenant's company → **403**; creating a
  master without a WebAuthn step-up → **403 `STEPUP_REQUIRED`**; the WebAuthn
  ceremony (`/auth/webauthn/register/begin`) returns a challenge; five bad logins
  → **423 `LOCKED`** with an `until` timestamp.

### 3.13a Users, roles and switching company

- **`F3` — Select Company.** Lists the companies *you* are a member of, with the
  security level you hold in each (the same person is often an Owner of their own
  books and a Data Entry operator on a client's). Picking one re-scopes the
  session and returns you to the Gateway — deliberately, because the screens you
  had open were showing another company's books.
- **Gateway > Users & Roles.** Who may open this company, the **pending invites**
  (an invite is not access until the invitee signs in and claims it), and the
  security levels with their Allow/Disallow rules. Inviting is done here; the
  default level offered is the least-privileged one, never Owner.
- A refusal is explained, not hidden: a Data Entry operator opening this screen is
  told their level does not permit it, and an Owner without a WebAuthn step-up is
  told the invite needs a second factor.
- **Verified:** `pnpm e2e:ui` drives both screens in a real headless browser
  (24/24 flows); `pnpm verify:tenancy` exercises 48 access-control paths against
  the live API, including creating a custom level, granting it, and letting the
  policy engine be the judge of what its holder can reach.

### 3.14 Help, Features, Calculator, status line

- `F1` Help is **generated from the live keymap**, so the documentation cannot
  drift from the behaviour.
- `F11` Company Features states what is on, what is structurally always-on (the
  audit trail), and what is not built — with reasons. No decorative toggles.
- `Ctrl+N` calculator evaluates `+ - * / ()` with a hand-written parser, never
  `eval`. **Verified:** `118000/1.18` → `100000.0000`.
- The status line under every screen reports what the last key did.

---

## 4. Honest gaps (what a Tally user will miss)

| Missing | Consequence | Keys that say so |
|---|---|---|
| Inventory: stock vouchers, godowns, batches, BoM, orders | no stock movement or valuation | `F10`, `Alt+F7`–`Alt+F9`, `Ctrl+F7`–`Ctrl+F9` |
| Payroll | — | `Ctrl+F4` |
| Cost centres / categories | no allocation dimension on postings | — |
| Multi-currency | INR only | — |
| Two companies open SIMULTANEOUSLY (and group consolidation) | you switch between companies (`F3`) rather than holding several open at once | `Alt+F3`, `Ctrl+F3` |
| Password / SSO login | the browser session authenticates as one fixed development subject, so every session is the same person | — |
| Admin WRITES beyond inviting | changing a member's level, revoking access and creating a custom level are API-only | — |
| Period filter on BS / P&L / Trial Balance | those three are as-of-latest (Day Book and Outstandings are period-aware) | the `F2` panel says so |
| Printing / invoice formats | no print or PDF path | `Alt+P` unbound |
| Banking: reconciliation, cheque printing | — | — |

**Environment caveat:** on macOS some `Ctrl+F<n>` chords are intercepted by the OS
before the browser sees them (Tally is Windows-first). The bindings are correct —
they simply may not reach the page on a Mac unless the OS shortcut is disabled.

---

## 5. A five-minute self-test

0. `cd frontend && pnpm seed:demo -- --insightd`, then open the URL it prints
   (§1.1) so every screen below has data.
1. `F8`, pick a party, taxable `10000`, rate `18`, `Ctrl+A` → a number and a
   server-derived CGST/SGST appear.
3. `Esc` → Gateway. (This is the bug that was fixed: before, `Esc` was dead once
   any screen had been opened.)
4. Type `Day` + `Enter` → the Day Book shows the voucher.
5. `Alt+E` → a CSV downloads. `Ctrl+F`, type nonsense → rows disappear; clear it.
6. `Enter` on the row → the voucher opens **loaded** in alteration mode.
7. `Esc`, then type `Balance` + `Enter`, `↓`, then `Enter` repeatedly → group →
   sub-group → ledger → the voucher. `Esc` back up.
8. `F1` → the whole keymap for the screen you were on. `Esc`.
9. `F11` → what this edition has and has not. `Esc`.
10. Press `Alt+F7` → it tells you inventory vouchers are not built yet, instead
    of doing nothing. (`Alt+F6` DOES open a Credit Note — that one is built.)
11. Press `F3` → the companies you may open, with the level you hold in each.
    `Esc` stays put; picking one re-scopes the session and returns you here.
12. Type `Users` + `Enter` → who may open this company, the pending invites, and
    the security levels with their rules.
