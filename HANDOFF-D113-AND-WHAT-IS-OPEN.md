# HANDOFF — D113: ONE MONTH'S CREDIT NOTE ABORTS THE ANNUAL RETURN

**Written:** 2026-08-31, working tree CLEAN at `74b8619` · **Branch:** `session/t32-and-close-7-3`
**Read time: 8 minutes.** ⭐ **Every claim in §1, §3 and §4 was MEASURED AT `74b8619` and the
command is beside it.** §5–§6 are carried from the register and say so.

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** It is self-contained.
§1–§2 get you a running app you can safely break. §3 is what is open. **§4 is the recommended
row and the decision it needs before any code.** §7 is the house standard your work must meet.

---

## 0. ⭐ THE ONE-LINE VERSION

**D108 and D107 are CLOSED** (`74b8619`) — a second Ctrl+A no longer keys a second invoice, and
the word `Saved` is no longer a fact about a keypress. **Seven rows remain open. One is HIGH.**

⭐ **THE RECOMMENDED NEXT ROW IS D113**, and it is the only HIGH: **the GST annual return cannot be
produced for the demo book this repo ships and `verify:demo` asserts 34/34 against.** One ordinary
July credit note aborts all twelve months.

⛔⛔ **DO NOT START BY WRITING CODE. D113 IS A DECISION FIRST** (§4.4). A one-line fix to the
comparison is available, obvious, and produces a return the GST portal would refuse.

---

## 1. THE STATE — verbatim, and what each number should be

```bash
cd /Users/yashbhalgat/code/tally-updated
git log --oneline -1          # 74b8619 fix(D108, D107): the frame was fresh, the form was not…
git status --porcelain        # (empty)
```

**Gate baselines at `74b8619`, ALL RE-RUN AT THAT COMMIT. Any red is yours.**

⚠️ **EVERY LINE BELOW IS RUN FROM THE REPO ROOT** and each `cd` is contained in its own subshell
for that reason — `cd frontend && … "$(cat frontend/.demo-company-id)"` looks right and cannot
work, because after the `cd` that path no longer exists.

```bash
(cd backend && go build ./... && go vet ./... && go test ./... -count=1)  # all green
bash backend/scripts/test_residue.sh                                     # OK: no residue
(cd frontend && pnpm exec tsc --noEmit)                                  # clean
(cd frontend && pnpm exec vitest run)                                    # 1845 passed / 114 files
(cd frontend && pnpm verify:demo "$(cat .demo-company-id)")              # 34/34  ⚠️ id is an ARGUMENT
node frontend/scripts/drive-t4-slice2b-matching.mjs                      # 35/35, tenant reaped
node frontend/scripts/drive-t12-gstr9.mjs                                # 33/33
node frontend/scripts/drive-t11-budgets.mjs                              # 36/36
python3 corpora/validate.py                                              # OK
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md            # 114 · 103 · 7 · 0 · 4
```

⛔ **THE BACKEND SUITE AND THE DRIVES NEED THE SERVICES UP (§2.1). The vitest and tsc lines do not.**

⚠️ **THE REGISTER PRINTS `4 UNREADABLE` AND THAT IS NOT WORK WAITING FOR YOU.** D49, D80, D82 and
D84 are all CLOSED; the parser cannot read their status cells. ⛔ **Do not "fix" the parser
casually** — a rewrite that reconciles by counting looks right and classifies D13 off the wrong
cell. It needs its own pass.

⚠️ **`7 OPEN` is correct and is not rot.** D99 is open **by decision**; D109–D114 were registered
by the 2026-08-31 audit and none has been started.

---

## 2. ⛔ THE TRAPS THAT WILL COST YOU AN HOUR

*(All five are durable and all five have bitten this repo more than once. They are unchanged by
the D108 work.)*

### 2.1 ⛔⛔ A STALE `cored` IS INDISTINGUISHABLE FROM A BROKEN FIX — AND `pkill` MISSES IT

`make run-cored` is `go run`, so the listening process is a compiled child with a different command
line. **Found stale ten times before this file's predecessor was written.**

⛔ **DO NOT USE STARTUP DURATION AS THE TEST.** With a warm Go build cache a genuinely fresh `cored`
also listens in about a second. ⭐ **CHECK THE PROCESS START TIME AGAINST THE CLOCK, AND AGAINST
YOUR LAST BACKEND COMMIT:**

```bash
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # is this NOW?
git log -1 --format='%ci' -- backend/                        # older than the process?
```

Kill by PORT, always, and start **cored first**:

```bash
kill -9 $(lsof -nP -iTCP:8087 -sTCP:LISTEN -t) 2>/dev/null
kill -9 $(lsof -nP -iTCP:8091 -sTCP:LISTEN -t) 2>/dev/null
sleep 2
nohup make run-cored > /tmp/cored.log 2>&1 &
for i in $(seq 1 90); do curl -sf localhost:8087/healthz >/dev/null && break; sleep 1; done
nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 &   # AFTER cored
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"; date
```

⛔ **CORED FIRST.** A cored restart mints a new ephemeral signing key, invalidating the service
token `statutoryd` minted from it. Both failures present as a bare `401`.

**Ports:** cored `:8087`, insightd `:8088`, statutoryd `:8091`, vite `:5173`.

⚠️ **D113 IS ENTIRELY SERVER-SIDE and needs only cored + psql.** You do not need vite for it.

### 2.2 ⚠️ VITE IS `localhost`, NOT `127.0.0.1`

`--strictPort`; WebAuthn origin matching is exact and vite binds `::1`. Use `http://localhost:5173/`.
⚠️ **Restart vite if it has been running for days** — this session found a three-day-old dev server.

### 2.3 ⛔⛔ AN EMPTY `?company=` MINTS A FRESH TENANT AND EVERY SCREEN LOOKS CLEAN

The app bootstraps an empty company called **"My Company"** when no company is named. **Two audit
rows were wrongly logged as passing that way.** Always pass `?company=<uuid>`, and read the **id**,
not the name — there are ~95 companies called "Demo Traders".

### 2.4 ⛔ DIRTY A SCRATCH BOOK, NEVER THE DEMO ONE

```bash
(cd frontend && pnpm seed:demo -- --name "D113 Scratch")   # -> .d113-scratch-company-id
```

⭐ **The name and the id file move together.** With **no** `--name` the seeder writes
`.demo-company-id`, which every gate reads — that is how ten identically-named books accumulated.
After any session re-run `pnpm verify:demo "$(cat frontend/.demo-company-id)"` and expect **34/34**.

⚠️ **`34aee254-9027-4641-ae2a-76d79420772b` (`D108 Fix Scratch`) already carries D108's drive
vouchers — 28 of them. Make a fresh one.**

⛔⛔ **BUT D113 IS DIFFERENT AND YOU MUST READ THIS:** the defect is **on the demo book itself**,
and it reproduces on any freshly seeded book because the seeder builds the same July. So you may
DIAGNOSE against `.demo-company-id` **read-only** (the two commands in §4.2 are GETs and write
nothing), and you must DRIVE any fix against a scratch book.

### 2.5 ⚠️ THE FIRST WRITE OF A SESSION CARRIES A WEBAUTHN CEREMONY

The Owner level requires step-up. In a hand-driven browser the first `POST /v1/…/vouchers/*`
returns **403** and the ceremony runs; that 403 is not a bug. Install a virtual authenticator over
CDP **before the first navigation**:

```js
const cdp = await page.context().newCDPSession(page);
await cdp.send("WebAuthn.enable", { enableUI: false });
await cdp.send("WebAuthn.addVirtualAuthenticator", { options: { protocol: "ctap2",
  transport: "internal", hasResidentKey: true, hasUserVerification: true,
  isUserVerified: true, automaticPresenceSimulation: true } });
```

⚠️ **THIS TRAP IS HOW D108 WAS FOUND** — a pending ceremony made it impossible to tell whether the
first Ctrl+A had landed, so a second was pressed. It worked exactly as written in this session's
re-drive; copy it verbatim.

---

## 3. ⛔ WHAT IS OPEN — measured 2026-08-31 at `74b8619`

```bash
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md
# 114 REGISTERED · 103 CLOSED · 7 OPEN · 0 PARTIAL · 4 UNREADABLE
#   OPEN: D99, D109, D110, D111, D112, D113, D114
```

| Row | Sev | One line | Shape of the work |
|---|---|---|---|
| ⭐ **D113** | **HIGH** | The annual return **cannot be produced for the shipped demo book** — one July credit note aborts all twelve months | **A DECISION, then a small change.** §4 |
| **D109** | MEDIUM | A two-level report **exports flat**, so summing the money column returns exactly **2.000000×** the file's own total | A format decision (a Level column?) then a small change |
| **D110** | MEDIUM | The Bank Reconciliation screen says *"matching is not built yet"* and **Alt+S on that same screen opens it** | One sentence, but keep the half it gets right |
| **D111** | LOW | Every **pasted** bank statement is stored with `file_name = ''`, and the disclosure that names the unchecked file renders an empty string | One line, but **decide the name** |
| **D112** | LOW | The Payroll Register answers an ordinary empty month with a raw internal error naming its own package **twice** | Distinguish the sentinel from a real failure |
| **D114** | LOW | The import panel says *"Esc goes back to the reconciliation statement"* and Esc goes to the **Gateway** | Pop one frame, or change the sentence |
| **D99** | LOW | ⛔ **OPEN BY DECISION, NOT BY OMISSION.** Its premise did not survive a real TallyPrime export and fixing it speculatively would make genuinely malformed numbers readable. **Leave it** |  |

### 3.1 ⚠️ AND THE LARGEST GAP IS NOT A REGISTER ROW AT ALL — PAYROLL

**Measured at `74b8619` on the demo book:**

```bash
CO=$(cat frontend/.demo-company-id)
psql "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable" -X -A -F' | ' -c "
SELECT (SELECT count(*) FROM employee WHERE company_id='$CO') AS employees,
       (SELECT count(*) FROM pay_run WHERE company_id='$CO') AS pay_runs;"
# employees | pay_runs
# 0 | 0
```

**The seeder creates no employees and no pay runs**, so Pay Run, payslips, Form 138 and every
EPF/ESI computation are **still undriven end to end** — a whole plane of the product that no gate
and no audit has ever exercised against real data. ⭐ **It is its own session** and it is a
plausible alternative to §4 if you would rather build than decide. ⚠️ **D112 lives inside it** —
the Payroll Register's empty-month sentence — so a payroll session absorbs that row for free.

---

## 4. ⭐⭐ D113 — THE RECOMMENDED ROW, MEASURED

### 4.1 What it is

**The GST annual return (GSTR-9) cannot be produced for `Demo Traders` — the very book
`verify:demo` asserts 34/34 against — and cannot be produced for any freshly seeded book either,
because the seeder builds the same July.** It is the shipped demo failing its own flagship
compliance report.

### 4.2 ⭐ REPRODUCE IT IN TWO COMMANDS (read-only GETs, safe on the demo book)

Needs cored up (§2.1). Save this as `/tmp/d113.mjs`:

```js
const B = "http://localhost:8087";
const j = async (m, p, { token } = {}) => {
  const r = await fetch(B + p, { headers: token ? { Authorization: `Bearer ${token}` } : {} });
  return { status: r.status, text: await r.text() };
};
const tok = (await (await fetch(B + "/auth/token", { method: "POST",
  headers: { "content-type": "application/json" },
  body: JSON.stringify({ subject: "browser-dev-owner", amr: ["webauthn"] }) })).json()).token;
const CO = process.argv[2], base = `/v1/companies/${CO}`;
for (const [label, q] of [
  ["3B July", `${base}/gstr3b?from=2026-07-01&to=2026-07-31`],
  ["3B June", `${base}/gstr3b?from=2026-06-01&to=2026-06-30`],
]) { const r = await j("GET", q, { token: tok }); console.log(label, r.status, r.text.slice(0, 200)); }
```

```bash
node /tmp/d113.mjs "$(cat frontend/.demo-company-id)"
```

**Measured at `74b8619`:**

```
3B July 422 {"error":"gstr: generated GSTR-3B violates a validation rule:
             table 3.2 taxable 0.0000 exceeds 3.1(a) taxable -12000.0000"}
3B June 200 {"fp":"062026","sup_details":{"osup_det":{"txval":"26500", …
```

⭐ **THAT IS A SMALLER REPRO THAN THE REGISTER ROW HAS.** The row reaches it through GSTR-9;
**one month's GSTR-3B fails on its own**, which is a far tighter loop to work in. The annual
return then fails with the month's error wrapped in two more package prefixes:

```
GSTR-9 FY2026-27: 422
{"error":"gstr: GSTR-9 2026-27, period 072026: gstr3b: gstr: generated GSTR-3B
          violates a validation rule: table 3.2 taxable 0.0000 exceeds 3.1(a) taxable -12000.0000"}
```

*(The GSTR-9 route additionally requires six tax-ledger id query params — `cgst_ledger_id`,
`sgst_ledger_id`, `igst_ledger_id`, `input_cgst_ledger_id`, `input_sgst_ledger_id`,
`input_igst_ledger_id`. Its 400 without them is a good refusal, not a bug; it says so at length.
Read them off `GET {base}/ledgers` by name — `Output CGST`, `Input CGST`, and so on.)*

### 4.3 The mechanism, verified line by line

**`backend/internal/compliance/gstr/gstr3b.go`, `Validate()` (~`:503-518`):**

```go
if txval.GreaterThan(g.T31.OsupDet.Txval) {
    return fmt.Errorf("%w: table 3.2 taxable %s exceeds 3.1(a) taxable %s", …)
}
```

**The July data that produces it** (`psql`, demo book, and it is an ordinary business month):

| type | no | date | taxable | cgst | sgst | igst |
|---|---|---|---|---|---|---|
| **Credit Note** | 1 | 2026-07-08 | 12000.0000 | 0 | 0 | **2160.0000** |
| Debit Note | 1 | 2026-07-12 | 2500.0000 | 225 | 225 | 0 |
| *(and four non-GST vouchers — journals, receipt/delivery notes)* |

**July has a credit note and NO SALES**, so outward taxable nets to **−12,000.00**. `Validate()`
then evaluates `0 > -12000` as **true** and rejects the month — **table 3.2 is EMPTY and cannot
meaningfully exceed anything.**

⭐ **THE SEEDER'S CREDIT NOTE IS DELIBERATE AND ITS COMMENT IS RIGHT** (`seed-demo.mts:939-949`):
it is a partial sales return against April's inter-state INV-002, written specifically to
demonstrate *"how you undo part of an invoice CORRECTLY"* rather than by altering a filed invoice.
**Nothing about the data is wrong.** A return in a month with no fresh sales is an ordinary Tuesday.

⭐ **AND THE AUTHORS ALREADY REASON ABOUT LEGAL NEGATIVES TWELVE LINES BELOW**, on the ITC
identity: *"A NEGATIVE result is LEGAL and must pass — reversals can exceed availment in a period
where a …"*. That care reached table 4C and not 3.1(a).

### 4.4 ⛔⛔ THE DECISION — MAKE THIS BEFORE WRITING ANY CODE

**There is a one-line fix and it is a trap.** Widening the comparison to `max(3.1(a), 0)`, or
skipping it when 3.2 is empty, stops the abort in ten seconds — **and leaves a return carrying
−12,000.00 in table 3.1(a), which the GST portal will not accept.** You would have converted a
loud failure into a silent one, which is the worse of the two.

**The question that must be answered first:**

> **(a) May table 3.1(a) go negative at all?**
> The portal's position is that it may not — an excess credit note is **carried forward** to the
> next period. If that is right, the validator is correct that something is wrong (even though its
> *sentence* is about the wrong table), and the fix belongs in how the period's outward supply is
> **computed and carried**, not in the comparison.
>
> **(b) Only if (a) says negatives may stand** does the comparison itself need widening.

⚠️ **NOTHING IN THIS REPO HAS BEEN NEAR A GOVERNMENT PORTAL** and no row claims portal validation.
Read the rule from the return's own arithmetic and from CBIC material; **say in the register what
you relied on**, the way D82's row records that it is a decision and not a measurement.

⚠️ **AND SEPARATELY, REGARDLESS OF (a)/(b) — TWO THINGS ARE WRONG NO MATTER WHAT:**

1. **One bad month must not abort the annual return with no per-month breakdown.** A twelve-month
   report that dies on month four and names only month four leaves the operator with no way to
   see the other eleven.
2. **The sentence stacks three package prefixes** in front of the operator:
   `gstr: … : gstr3b: gstr: generated GSTR-3B violates …`. **D93** is exactly this class, closed
   for `masters.CreateLedger` on the grounds that a refusal naming an internal type tells the
   operator nothing about what they may do. **D112 is the same class again.**

### 4.5 ⛔ WHY NO GATE CAUGHT IT — read this before trusting any probe you write

`drive-t12-gstr9.mjs` passes **33/33** and its own header states it *"takes NO CO — it makes one"*:
it mints its own company and its own vouchers, so **the probe never meets the shape the seeder
ships**. ⭐ **A probe asserts what its author already suspected.** Whatever you write for D113 must
run against **a seeded book**, not only against a hand-built fixture, or it will be green for the
same reason this one is.

---

## 5. THE OTHER ROWS — carried from the register, not re-measured this session

Each has a full row in `HANDOFF-PARITY-AUDIT.md` §7.1 with its measurement, its file anchors and
the trap in its fix. **Read the row before starting; each one names something you would otherwise
get wrong.**

- **D109** (MEDIUM) — the flat export. ⚠️ Its own row notes both Stock Summary and Outstandings
  have the shape, and that the ratio is exactly **2.000000** because every parent is duplicated by
  exactly one child. **A Level column is a format decision**, not a bug fix — decide it.
- **D110** (MEDIUM) — the stale sentence on Bank Reconciliation. ⚠️ **Keep the half it gets right:**
  the paragraph exists to stop an operator believing the Bank Date ticks are automatic, and the
  matcher does not tick anything either. Its row also records that the matcher itself is excellent
  and all three of its invariants hold — *a defect row is not a verdict*.
- **D111** (LOW) — the nameless pasted statement. ⚠️ **Do not copy the preview's fallback**
  (`pasted.csv`): two pasted statements would be indistinguishable again, which is the same defect
  one step over.
- **D112** (LOW) — the payroll register's raw error. ⚠️ **Distinguish the sentinel from a real
  failure** rather than rewording one string; `ErrPayRunNotFound` is an EMPTY STATE on the screen
  and a genuine refusal on the export route. **Absorbed for free by a payroll session (§3.1).**
- **D114** (LOW) — Esc unwinds to the Gateway. ⚠️ Check the Alt+S match panel at the same time; its
  footer makes the same promise.
- **D99** (LOW) — ⛔ **leave it.** Open by an explicit decision recorded in §7's L27.

---

## 6. ⚠️ STILL BLOCKED, AND WHY — do not pick these up expecting to finish

**T13 interest engine · EPF/EDLI admin charges · portal verification · DPDP · T4 slice 2c.**
All five are blocked for reasons recorded in `HANDOFF-USABILITY-DAY-TWO.md` §8. Read that before
assuming any of them is available.

---

## 7. ⛔⛔ THE HOUSE STANDARD YOUR WORK MUST MEET

This is not style. Rows have been re-opened in this repo for failing each of these.

- [ ] **A failing test written FIRST**, asserting the thing no existing test covers.
- [ ] ⛔ **MUTATION-PROVED.** Revert the fix and watch the new test go red **with the number the
      real system produced**. A test that passes before the fix is not a test. See D98, D101 and
      now D108 for the standard — D108's proof reads `expected 1, got 2` on all three doors,
      which is exactly what `psql` had counted in the browser.
- [ ] ⛔ **DRIVEN AGAINST THE REAL SYSTEM, not only in a unit test**, on a **fresh scratch book**
      (§2.4), against a cored verified fresh (§2.1). **The oracle is `psql` or the wire, never the
      screen** — the screen is what lied in D107.
- [ ] ⚠️ **DRIVE THE CONTROL TOO.** The thing most likely to be broken by a fix is the behaviour
      the fix is adjacent to, and it is the thing a unit test is least likely to catch. D108's fix
      could trivially have broken bulk entry; the browser drive keyed a second invoice to prove it
      had not.
- [ ] Gates in §1 re-run and **stated in the commit message, with every deferral named.**
- [ ] `HANDOFF-PARITY-AUDIT.md` row moved to CLOSED **with the measurement, the mutation proof and
      the drive recorded in the house format**, plus a `### L32 — …` lane section at the end of §7.
      (This session's `L31` is the template; copy its shape.)

### 7.1 ⭐ THE LESSON FROM `L31` THAT IS WORTH MORE THAN THE FIX — read it

Deleting D107's dishonest `Saved` **found a green test that was proving nothing.**
`CostAllocationPicker.test.tsx`'s *"SAVES the split and the server's own refusal appears"* opens
with *"⛔ THE REACHABILITY ASSERTION: the split must reach the wire."* It keyed **no Account**, so
every Ctrl+A in it was refused client-side and **not one byte ever left the browser**. It passed
for years because the header rendered `Saved` off the machine's keypress flag — the word appeared
synchronously on the keydown, before the refusal, and `waitFor` returned on its first poll.

⛔ **A test written to prove something reaches the wire was reading a fact about a keystroke.**

**When you assert that a thing HAPPENED, assert it on the wire or in the database — never on a word
the screen chose to render.** If you find yourself waiting for text to confirm a write, ask what
sets that text and whether anything else could.

---

## 8. THE READING LIST, in order

1. **This file.**
2. `HANDOFF-PARITY-AUDIT.md` — row **D113** first, then **D93** (why a stacked package prefix is a
   defect in its own right), then §7's **`### L31`** for the house format you must copy.
3. `backend/internal/compliance/gstr/gstr3b.go:503-540` — `Validate()`, and the ITC-identity
   comment twelve lines below it that already reasons about legal negatives.
4. `frontend/scripts/seed-demo.mts:939-985` — the July credit note and the comment explaining why
   it is the *correct* way to undo part of an invoice. **The data is not the bug.**
5. `frontend/scripts/drive-t12-gstr9.mjs` — its header, for §4.5: why 33/33 never met this shape.
6. `docs/usability-day-2026-08-30.md` §6.6 — what the 2026-08-31 pass did **not** cover.
