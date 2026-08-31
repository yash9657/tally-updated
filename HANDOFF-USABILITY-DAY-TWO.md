# HANDOFF — DRIVE THE SIX UNDRIVEN AREAS, AS A SHOPKEEPER WOULD

**Written:** 2026-08-31, working tree CLEAN at `19efecd` · **Branch:** `session/t32-and-close-7-3`
**Read time: 8 minutes.** ⭐ **Every claim below was MEASURED and the command is beside it.**

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** It is self-contained.
§1–§3 get you a running app you can safely break. §5 is the work. §6 is how to recognise a
finding when you see one.

---

## 0. ⭐ WHAT THIS IS, AND THE ONE-LINE REASON IT MATTERS

`PLAN-TALLY-PARITY.md` §7 has asked since before T11 for somebody to **drive the app for a
day as a shopkeeper would**, *"which is how the smaller papercuts surface — the ones that do
not show up as a missing table."*

Two passes have now run (`docs/usability-day-2026-08-30.md`). Between them they produced
**two defects that no test suite had caught and no inventory of routes or tables could have
found** — D106 and D107, both closed or registered, both described in §6 because **the
pattern is the point**. Six areas of the product have still never had that treatment:

> **printing · export · bank statement import & matching · stock vouchers · payroll · GST returns**

⚠️ **AND "UNDRIVEN" DOES NOT MEAN "UNTESTED" — READ §4 BEFORE YOU BELIEVE THE WORD.** Every
one of those six has a scripted probe already. That is exactly why they still need this.

---

## 1. THE STATE — verbatim, and what each number should be

```bash
cd /Users/yashbhalgat/code/tally-updated
git log --oneline -1          # 19efecd audit(D107): the write half is drivable at last…
git status --porcelain        # (empty)
```

**Gate baselines at `19efecd`. Any red is yours.**

⚠️ **EVERY LINE BELOW IS RUN FROM THE REPO ROOT** and each `cd` is contained in its own
subshell for that reason — `cd frontend && … "$(cat frontend/.demo-company-id)"` looks right
and cannot work, because after the `cd` that path no longer exists.

```bash
(cd backend && go build ./... && go vet ./... && go test ./... -count=1)  # all green
bash backend/scripts/test_residue.sh                                     # OK: no residue
(cd frontend && pnpm exec tsc --noEmit)                                  # clean
(cd frontend && pnpm exec vitest run)                                    # 1836 passed / 113 files
(cd frontend && pnpm verify:demo "$(cat .demo-company-id)")              # 34/34  ⚠️ id is an ARGUMENT
node frontend/scripts/drive-t4-slice2b-matching.mjs                      # 35/35, tenant reaped
node frontend/scripts/drive-t12-gstr9.mjs                                # 33/33
node frontend/scripts/drive-t11-budgets.mjs                              # 36/36
python3 corpora/validate.py                                              # OK
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md            # 107 · 101 · 2 · 0 · 4
```

⛔ **THE BACKEND SUITE AND THE DRIVES NEED THE SERVICES UP (§2.1). The vitest and tsc lines
do not.**

⚠️ **THE VITEST BASELINE IS 1836 / 113 FILES.** If you see 1831 you are on a tree without
D106's client tests.

⚠️ **THE REGISTER PRINTS `4 UNREADABLE` AND THAT IS NOT WORK WAITING FOR YOU.** D49, D80,
D82 and D84 are all CLOSED; the parser cannot read their status cells (three contain the
word OPEN in prose after the verdict, and D80 has a `|` inside a backtick code span that
splits its row into eight cells). ⛔ **Do not "fix" the parser casually** — a first-verdict-
word rewrite reconciles to 107 and looks right, but it classifies **D13** off a cell that is
not the status cell and is correct only by luck. It needs its own pass.

---

## 2. ⛔ FIVE TRAPS, EACH OF WHICH ALREADY CAUGHT SOMEBODY

### 2.1 ⛔⛔ A STALE `cored` IS INDISTINGUISHABLE FROM A BROKEN FIX — AND `pkill` MISSES IT

`make run-cored` is `go run`, so the listening process is a compiled child with a different
command line. **Found stale eight times before 2026-08-30 and twice more that day.** The
second time is the one to learn from:

```
$ pkill -f "cmd/cored"; sleep 2; nohup make run-cored &   # looked fine
$ curl -sf localhost:8087/healthz                          # answered in ONE second
$ ps -o lstart= -p $(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)  # …from the OLD process
```

⛔ **DO NOT USE STARTUP DURATION AS THE TEST.** It is tempting — the old process answers
instantly — but with a warm Go build cache a genuinely fresh `cored` also listens in about a
second, so "it came up too fast" is a heuristic that cries wolf and then misses the real
case. ⭐ **THE ONLY RELIABLE CHECK IS THE PROCESS START TIME AGAINST THE CLOCK**, and the
second reliable check is against your last backend commit:

```bash
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # is this NOW?
git log -1 --format='%ci' -- backend/                        # older than the process?
```

⚠️ **That second line is how the first staleness of 2026-08-30 was caught:** cored had
started at 14:16:07 and the last backend commit was 14:17:26 — the running binary was
**79 seconds older than the code**. Kill by PORT, always:

```bash
kill -9 $(lsof -nP -iTCP:8087 -sTCP:LISTEN -t) 2>/dev/null
kill -9 $(lsof -nP -iTCP:8091 -sTCP:LISTEN -t) 2>/dev/null
sleep 2
nohup make run-cored > /tmp/cored.log 2>&1 &
for i in $(seq 1 90); do curl -sf localhost:8087/healthz >/dev/null && break; sleep 1; done
nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 &   # AFTER cored — see below
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"; date
```

⛔ **CORED FIRST, ALWAYS.** A cored restart mints a new ephemeral signing key, which
invalidates the service token `statutoryd` minted from it. Both failures present as a bare
`401` from a call statutoryd makes, surfaced as
`{"error":"statutory: GET voucher: unexpected status 401"}` — which names neither the token
nor the expiry.

**Ports:** cored `:8087`, insightd `:8088`, statutoryd `:8091`, vite `:5173`.

### 2.2 ⚠️ VITE MUST BE ON `:5173`, AND IT IS `localhost`, NOT `127.0.0.1`

`--strictPort`; WebAuthn origin matching is exact and vite binds `::1`. Use
`http://localhost:5173/`.

### 2.3 ⛔⛔ AN EMPTY `?company=` MINTS A FRESH TENANT AND EVERY SCREEN LOOKS CLEAN

The app bootstraps an empty company called **"My Company"** when no company is named. **Two
audit rows were wrongly logged as passing that way on 2026-08-07.** Always pass
`?company=<uuid>`, and check the header bar says the company you meant.

⚠️ **THERE ARE 94 COMPANIES CALLED "Demo Traders"** and the header bar shows only the name.

```bash
psql "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable" -X -A -F' | ' \
  -c "SELECT name, count(*) FROM company GROUP BY name ORDER BY 2 DESC LIMIT 3;"
# Demo Traders | 94        <- the seeder ran 94 times, same name every time
# redteam-…    | 90
# My Company   | 24        <- ⛔ 24 tenants minted by an empty ?company=. The trap above
#                             is not hypothetical; it has fired two dozen times
```

On 2026-08-30 that cost real time: a browser drive on one "Demo Traders" was diagnosed
against the database rows of another, and the disagreement looked like a far larger bug than
the one actually present. **Read the id, not the name** — and `--name` your scratch book
(§3) so its header is unambiguous.

### 2.4 ⛔ `openApp()`'s TENANT CHECK DEFAULTS TO "Demo Traders"

`frontend/scripts/audit-harness.mjs` exports `EXPECTED_COMPANY = "Demo Traders"`. If you
drive a **scratch** company (§3) through the harness you must pass `expectCompany` or set
the env var, or the assertion that protects you from §2.3 will fire on a correct run — or
worse, be edited away.

### 2.5 ⚠️ THE FIRST WRITE OF A SESSION CARRIES A WEBAUTHN CEREMONY

The Owner level requires step-up. `openApp()` installs a **virtual authenticator over CDP
before the first navigation** so the ceremony completes by itself:

```js
await cdp.send("WebAuthn.addVirtualAuthenticator", { options: { protocol: "ctap2",
  transport: "internal", hasResidentKey: true, hasUserVerification: true,
  isUserVerified: true, automaticPresenceSimulation: true } });
```

⛔ **INSTALL IT BEFORE THE FIRST NAVIGATION AND DO NOT REUSE A WARM SESSION** — the harness
comment records a defect class that reproduces only on the first write of a session.

⚠️ **In a hand-driven browser (Playwright MCP) the first `POST /v1/…/vouchers/*` returns
`403` and the ceremony runs.** That 403 is not a bug and not your rule firing. Measured
2026-08-31. It is also the condition that makes **D107** (§7) visible, so expect to meet it.

---

## 3. ⭐ THE SCRATCH COMPANY — a book you may dirty and throw away

**This is new at `19efecd` and it is what makes an entry drive possible at all.** Before it,
the only populated books were the demo company that `verify:demo` and three drive scripts
assert against: entering a day's vouchers there breaks them, so the write half of the
product had never been driven by hand.

```bash
cd frontend
pnpm seed:demo -- --name "Scratch Traders"      # -> .scratch-traders-company-id (gitignored)
cat .scratch-traders-company-id
```

⭐ **THE NAME AND THE ID FILE MOVE TOGETHER, DELIBERATELY.** The seeder hard-coded
"Demo Traders" and always wrote `.demo-company-id`, so a second populated book was another
identically-named company that silently replaced the id every gate reads — which is how ten
accumulated. With **no** `--name` the behaviour is byte-for-byte what it always was, which
is what every existing gate depends on.

⛔ **DIRTY THE SCRATCH BOOK, NEVER THE DEMO ONE.** After any session, re-run
`pnpm verify:demo "$(cat frontend/.demo-company-id)"` and expect **34/34**. If it is not,
you drove the wrong company.

A scratch book already seeded and used on 2026-08-31 is
`5895d51d-86c5-408f-ba00-4b67f75a3b58` — it now carries one hand-keyed on-account receipt.
**Make a fresh one; do not inherit somebody else's mess.**

---

## 4. ⛔⛔ WHAT "UNDRIVEN" MEANS HERE — AND IT IS NOT "UNTESTED"

```bash
ls frontend/scripts/drive-*.mjs | wc -l      # 55
```

**All six areas below already have scripted probes**, several of them excellent:

| Area | Probes that already exist |
|---|---|
| printing | `drive-c-print-templates.mjs`, `drive-l7-print.mjs` |
| export | `drive-b1-pdf-export.mjs`, `drive-u1-real-export.mjs` |
| bank matching | `drive-d-reconciliation.mjs`, `drive-t4-slice2b-matching.mjs` (35/35) |
| stock | `drive-l10-stock.mjs`, `drive-l28-stock-restale.mjs`, `drive-stock-groups.mjs` |
| payroll | `drive-payroll.mjs` |
| GST returns | `drive-t12-gstr9.mjs` (33/33) |

⭐ **SO WHY DRIVE THEM AGAIN? BECAUSE A PROBE ASSERTS WHAT ITS AUTHOR ALREADY SUSPECTED.**
That is PLAN §7's whole point — *"every gate in the repo is a test or a scripted drive"* —
and it is exactly how both existing findings survived: D106 sat in the demo data while
`drive-t4-slice2b-matching.mjs` passed 35/35 beside it, and D107 flashes on **every**
`Ctrl+A` in a suite of 1836 passing tests.

⛔ **DO NOT WRITE A NEW PROBE FIRST.** Drive it as a shopkeeper: open the screen, do the
ordinary thing, read what it says, and ask *"would a person believe this?"* Write the probe
**after** you have something to pin.

---

## 5. THE SIX AREAS — and what a shopkeeper actually does in each

Take them in this order: it follows one working day, so state accumulates the way it really
would rather than being set up per area.

### 5.1 STOCK VOUCHERS — goods in and out
Receipt Note (Alt+F9), Delivery Note (Alt+F8), Stock Journal (Alt+F7), Physical Stock
(Alt+F10). ⭐ **The interesting question is valuation:** an item's method is per-item and may
be FIFO/LIFO/weighted-average since migration `0078`, so one Stock Summary can carry three
valuers and **two item totals on one page are not on a common basis**. The screen says so
now (D105) — check the report agrees with the screen, and that a physical-stock surplus
states the rate the extra units come in at.

### 5.2 PURCHASE & SALES with inventory — the daily loop
⚠️ **Known and NOT yet registered, found 2026-08-31:** a Sales voucher to a bill-by-bill
party **shows no bill-wise panel at all** — it works, because the client adds an unnamed NEW
reference, but an operator cannot split one invoice into two bills from that screen, which
Tally supports. Decide whether that is a defect and register it if so.

### 5.3 BANK STATEMENT IMPORT & MATCHING — Alt+O then Alt+S
Shipped 2026-08-28 and the last piece of the daily loop. ⭐ **The invariant to attack: it
PROPOSES and never TICKS.** Try to make it tick something by itself. Try an ambiguous row
(two suppliers paid the same amount the same day) and confirm it refuses to choose in
**both** directions. Try a file with no running-balance column and confirm it says the
balance check DID NOT RUN rather than staying silent.

### 5.4 PRINTING — Ctrl+P from every screen that offers it
Invoices, the reconciliation statement, reports. ⛔ **The known refusal to respect:** a print
request that does not name a bank account is refused rather than defaulted, because printing
"the first bank account" puts a real, correctly rendered statement for the WRONG bank on
paper with nothing on the page saying so. Look for the same class elsewhere: **a document
that is right in every particular and is about the wrong thing.**

### 5.5 EXPORT — Alt+E
⭐ **Check the headers against the rows.** The write-up already records one instance of the
shape to hunt (`ReportScreens.tsx`: *"a header reading 'Group' over a column that holds a due
date on every second row"*). Open the export in a spreadsheet, not just the API response.

### 5.6 PAYROLL & GST RETURNS — the month end
Pay Run, Payroll Register, Form 138; GSTR-9. ⛔ **NOTHING HERE HAS EVER BEEN THROUGH A
GOVERNMENT PORTAL** — the GSTN/EPFO/ESIC portals and the NIC e-Way Bill API cannot be
reached from this machine, and the FVU cannot run here. **Never claim a validation that was
not performed.** ⚠️ EPF/EDLI administrative charges are a known **release blocker**: the EPF
Scheme 2026 fixes no rate, `pf.go` refuses it deliberately, and ~₹500/establishment/month is
accruing unbooked. Do not "fix" that refusal.

---

## 6. ⭐⭐ HOW TO RECOGNISE A FINDING — the pattern both defects share

Neither D106 nor D107 was a wrong number. **Both were screens telling a confident lie while
every figure on them was real.** That is the shape to hunt.

**D106 — two reports over one book, disagreeing, while one promised they agreed.**
Outstandings said `318584.0000`; the Balance Sheet's Sundry Debtors said `311584.0000`. The
₹7,000 was a receipt posted with no bill-wise reference: it reached the ledger balance and
reached the ageing report nowhere. Both screens rendered, both footed, and the operator rings
a customer about money they already paid. ⭐ **It was findable ONLY because the report's
caption claimed the agreement out loud.**

**D107 — the word "Saved" for a voucher the server refused.** `VoucherScreen.tsx:2899` falls
back to the literal word when `status` is null and `snapshot.saved` is set, and that flag
means *"this frame has been accepted at least once (Ctrl+A)"*, not "the server has it".

⛔ **SO THE THREE QUESTIONS TO ASK AT EVERY SCREEN ARE:**

1. **Does it claim anything?** A caption asserting agreement, completeness or a basis is a
   claim you can test. Test it against a second surface that computes the same fact a
   different way — the Balance Sheet is the oracle for anything about balances.
2. **Would a person believe it?** Not "is the field correct" but "does the sentence mean
   what it appears to mean". "Saved" meant *a key was pressed*.
3. **What does it say when it has nothing to say?** An empty cell reading as "not overdue",
   a blank field reading as "this bank has no IFSC", a check that did not run looking like a
   check that passed. **A zero and an absence are different answers.**

⚠️ **AND WHEN SOMETHING LOOKS WRONG, REPRODUCE IT BEFORE YOU NAME IT.** D107 was first seen
as "a 403 renders as Saved" and that was the wrong diagnosis; it reproduced on an **empty
form with no request at all**, which is what found the real mechanism. Two intermediate
attempts failed to reproduce it, and the honest state at that point was *"observed once,
mechanism unknown"* — say that rather than filing it, and keep looking.

---

## 7. THE TWO OPEN DEFECTS — one is a fix waiting for you

| | |
|---|---|
| **D107** MEDIUM | The `Saved` lie above. ⭐ **The fix is small but needs a decision, not a delete.** The fallback branch presumably exists so a frame reset after a REAL save still reads "Saved" once `status` clears — so removing it may break that. The fact you actually want is already in the same header: `savedNo` / `savedVoucherID`, set only from a server response. ⛔ **Do not just delete the branch without establishing what it was for.** |
| **D99** LOW | The three money readers refuse a comma-grouped number while the quantity reader strips commas on purpose — so this codebase states that Tally emits them and then cannot read them. `<AMOUNT>83,650.00</AMOUNT>` is refused on import. `SplitQtyUnit` strips; `Magnitude()`, `OpeningSide()`, `OpeningMagnitude()` hand the raw string to `decimal.NewFromString`. |

---

## 8. ⛔ WHAT IS *NOT* NEXT — so you do not re-derive it

| Item | Status |
|---|---|
| **T13 interest engine** | Statutory half is DONE — MSMED Act + eleven RBI Bank Rate notifications, chain proved complete from 22 May 2020. ⛔ **The engine is still not next:** §4.2 found `reporting.Outstandings` is the wrong input, and two questions shape the schema — who is a micro/small enterprise (record the **Udyam declaration**, do not derive it) and the s.2(b) anchor date. Read `docs/design/t13-interest-model.md` §9.2 before starting |
| **EPF/EDLI admin charges** | ⛔ **RELEASE BLOCKER.** No rate notified under the 2026 Scheme. An email to the owner's CA is outstanding |
| **Portal verification, e-Invoice/IRP** | ⛔ Geo-fenced or needs a human in India with credentials. See the project memory: the fence covers NIC's `164.100.x` **and** RBI's `rbidocs` (a different range) — the pattern is Indian-hosted government document servers, and a CDN-fronted `www` host of the same body often serves the same content |
| **DPDP / privacy** | ⛔ Zero coverage, hard launch blocker. The immutable hash chain and "delete my data" are in real tension |
| **T4 slice 2c** | `HANDOFF-T4-SLICE-2C-MATCHING-GAPS.md` is written and sized. Instrument-date matching is BLOCKED on cheque-book management, a whole unbuilt Tally feature |
| **The remaining §2 papercuts** | `docs/usability-day-2026-08-30.md` §2.2–§2.8. The substantive one is the Gateway and Alt+G being **two different catalogues where neither contains the other** — `Outstandings`, `Trial Balance`, `Cash Flow` and `Bills Payable` are reachable only from Go To |

---

## 9. DEFINITION OF DONE

- [ ] A **fresh scratch company** seeded (§3) and driven — not the demo book.
- [ ] All six areas of §5 opened and used as a shopkeeper would, not probed.
- [ ] Every finding **reproduced** before it is named (§6), with the command or keystrokes
      that reproduce it written down.
- [ ] Findings registered in `HANDOFF-PARITY-AUDIT.md` in the house row format, and
      `docs/usability-day-2026-08-30.md` extended with a dated section — ⛔ **including what
      you did NOT cover**, which is the part that made this handoff possible.
- [ ] ⛔ From the repo root: `(cd frontend && pnpm verify:demo "$(cat .demo-company-id)")`
      still **34/34** — proof you dirtied the right book.
- [ ] Gates in §1 re-run and stated in the commit message, **with every deferral named**.
- [ ] ⚠️ If you fix D107, the commit says what the `snapshot.saved` fallback was for and why
      your replacement keeps it.
