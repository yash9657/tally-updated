# HANDOFF — DRIVE THE SEVEN ROWS THE PARITY AUDIT NEVER SAW

**Written:** 2026-08-27, at `20dd96f` · **Branch:** `session/t32-and-close-7-3`
**Read time: 5 minutes.** ⭐ **Every number below was MEASURED today and the command is beside it.**

⛔ **THIS FILE IS ONE OF TWO LANES RUNNING AT ONCE.** The other lane is T8 multi-currency, working
forward from migration 0080. **§4 is the collision protocol and it is not optional** — the two lanes
share one Postgres and this repository has already produced two false regressions that way.

⛔ **`git rm` THIS FILE when the seven rows are driven and their defects registered.**

---

## 0. ⛔⛔ TWO CORRECTIONS FIRST. BOTH WOULD SEND YOU AT THE WRONG WORK.

### 0.1 THE DEFECT REGISTER IS **CLOSED**. THERE ARE NOT 69 OPEN DEFECTS.

`HANDOFF-DEFECT-BURNDOWN.md` is titled **SPENT**. A session once read its old header and reported 69
open defects to a user as live work. **Re-derived today by PARSING `HANDOFF-PARITY-AUDIT.md` §7.1's
rows** — which is that file's own stated rule, *never read the header* — every registered defect
carries a `✅ CLOSED` / `✅ FIXED` status. The rows that a naive `grep OPEN` flags are prose
(*"the PDP's date peek fails OPEN"*, *"Re-opening the Day Book"*), not statuses.

> **97 REGISTERED · 97 CLOSED · 0 OPEN**, as of 2026-08-24 and unchanged at `20dd96f`.

### 0.2 ⛔ `HANDOFF.md` §5.5 IS STALE, AND IT IS THE TRAP THIS FILE EXISTS TO STOP

§5.5 still reads *"15 of 34 ROWS DRIVEN, 10 WERE LYING"*. **That was true on 2026-08-07 and is not
true now.** `HANDOFF-PARITY-AUDIT.md`'s own headline:

> *"All 34 have now been driven end to end in a real browser against real cored — 16 in passes 1–2,
> and the final 18 in this pass."*

⭐ **SO DO NOT RE-DRIVE THE 34.** They are measured, their defects were registered, and the register
is closed. ⚠️ **§5.5 misled the session that wrote this file**, which recommended re-auditing them to
a user before parsing the register. Fix §5.5 in place when you touch `HANDOFF.md`.

---

## 1. ⭐ THE ACTUAL WORK: SEVEN ROWS, MEASURED FROM GIT, NEVER DRIVEN

The audit's roster was **34 reachable rows** of a 47-row `FeaturesScreen`. There are **53 rows** now.

```
$ git rev-list -1 --before=2026-08-08 HEAD                 -> 1d715fc9   (47 rows)
$ # ...diff that file's name:/state: pairs against today's (53 rows)
  -> 10 rows ADDED SINCE THE AUDIT
```

Two of those ten are **not** new features and must not be re-driven: `Cash-flow projection` and
`Anomaly detection` are audit **row 32** split in two (see D22 / D95 / D96), and `Export to PDF
(Alt+E)` is audit **row 43** renamed. ⚠️ **A renamed row looks new to a diff and is not.**

### THE SEVEN — none has ever been driven end to end

| # | Row | Key | Track | ⛔ Why it is worth driving |
|---|---|---|---|---|
| **U1** | **Migrate from Tally — import a TallyPrime XML export** | `Alt+O` | T32 | ⛔⛔ **DRIVE THIS FIRST.** `HANDOFF.md` §10 item 1 calls it **the commercial blocker**: a business cannot switch if it cannot bring its books, and this importer **has never met a real Tally file.** ⭐ AND A FINDING IS ALREADY WAITING FOR YOU — see §2 |
| **U2** | **Offline Outbox — vouchers this machine is holding** | `Alt+Q` | D1/D3 | Shipped **this week** (`1ed6e1f`, `0a3de00`). Newest code on the board and the least worn in |
| **U3** | **Reconciliation — the changes these books refused** | `Alt+R` | D2 | Shipped **this week** (`a9d3640`) |
| **U4** | **Backup / restore** | `Alt+Y` | T14 | Must be **tenant-exact**. A backup that silently omits a table is the worst defect class in this product — it is discovered only when a restore is needed |
| **U5** | **Per-company print templates** | *(no Alt key, deliberately)* | T16 | ⚠️ **DO NOT INVENT A KEY FOR IT.** `HANDOFF.md` §7.2: a screen with no documented key gets NO key. Reach it from the Gateway |
| **U6** | **E-mail a document** | `Alt+M` | B1 | Opens the mail client — so the oracle is the `mailto:` URL it builds, not a sent message |
| **U7** | **Split company data (by financial year)** | — | — | `off`. ⭐ **Drive it only to confirm the row tells the truth when the key is pressed** — an honest `off` is a pass |

⚠️ **AND THREE AUDITED ROWS NOW SIT ON CHANGED ENGINES.** Rows **20** (stock items/units), **23**
(Stock Summary) and **26** (inventory vouchers) were driven before T9 (FIFO/LIFO, `0078`) and T10
(stock groups + compound units, `0079`) landed underneath them. **Their drives are stale even though
the rows are old.** Do these after the seven, and only if the seven leave time.

---

## 2. ⭐ A FINDING ALREADY IN HAND FOR U1 — START FROM IT, DO NOT RE-DERIVE IT

`docs/design/t8-multi-currency-model.md` §6 records a probe run today against the **unchanged** build:

| `<AMOUNT>` in the XML | `TallyLedgerEntry.Magnitude()` today |
|---|---|
| `83650.00` | `83650` ✅ |
| `$ 1000.00` · `1000.00 USD` · `1000.00/USD` | ⛔ **hard ERROR** — `can't convert … to decimal` |
| `83,650.00` | ⛔ **hard ERROR — and this one is not about currency at all** |

Two things follow, and both are U1's job to settle **in a browser**:

1. ⛔ **A DECORATED AMOUNT REFUSES THE WHOLE FILE, WITH A MESSAGE THAT NAMES A SYMPTOM.** The operator
   sees `can't convert $ 1000.00 to decimal`. **Drive an import of a file containing one** and judge
   what `TallyImportScreen` actually renders — the disclosure surface is at `TallyImportScreen.tsx:173`.
2. ⚠️ **`Magnitude()` REFUSES COMMA-GROUPED NUMBERS WHILE `SplitQtyUnit` STRIPS THEM ON PURPOSE**, and
   `SplitQtyUnit`'s own comment says *"Commas are thousands separators in Tally's own output"*. So the
   codebase states Tally emits them, and the three money readers (`Magnitude`, `OpeningSide`,
   `OpeningMagnitude`) do not handle them while the quantity reader does. ⛔ **LATENT, NOT ACTIVE** —
   the real 121-voucher export imported clean, so `AMOUNT` carried no commas in *that* file.
   ⛔ **DO NOT "FIX" IT SPECULATIVELY**: accepting commas everywhere also makes a genuinely malformed
   value readable. Register it, and settle it when a second real export arrives.

⛔ **AND THE THING TO ASK THE USER FOR:** `HANDOFF.md` §10 item 1 — **a real Tally XML export, any
version.** U1 cannot be finished honestly without one. Ask early; do not discover the importer is
wrong after a customer tries it.

---

## 3. THE METHOD — THE TOOL IS COMMITTED, NOT DESCRIBED

`frontend/scripts/audit-harness.mjs` signs in, installs a virtual WebAuthn authenticator, and
exposes `altMac()` and the company assertion. `drive-d-offline-queue.mjs` is the copy-me.

```bash
CO="$(cat /Users/yashbhalgat/code/tally-updated/frontend/.demo-company-id)" \
  node frontend/scripts/<your-probe>.mjs
```

⛔ **THE ABSOLUTE PATH MATTERS.** A bare `cat .demo-company-id` breaks the moment the shell's cwd is
not `frontend/`, and **an EMPTY company id makes the app CREATE a company** — at which point a clean
result on an empty tenant looks exactly like a clean result on the demo. **Assert the company name.**

### The five traps, all live for you

1. ⛔⛔ **`Alt+G` IS NOT THE ONLY DOOR.** Two rows were once logged as unreachable that open fine from
   the **Gateway menu**. Check the other door before registering a defect.
2. ⛔ **macOS COMPOSES Option+letter.** Drive every Alt binding with the harness's `altMac("x")`.
   U2/U3/U4/U6 are `Alt+Q`/`Alt+R`/`Alt+Y`/`Alt+M` — **all four need it.**
3. ⛔⛔ **A FIXED SLEEP IN FRONT OF AN ASSERTION IS A FLAKE THAT LIES IN THE DANGEROUS DIRECTION.**
   Two probes written that way reported false DATA-LOSS claims. **Poll.**
4. ⚠️ **DRIVE ON A THROWAWAY COMPANY, NEVER `Demo Traders`.** Tear it down with the catalog-derived
   sweep, and ⛔ **wrap the whole drive in one `try`** so a throw still reaches the teardown — a drive
   that died after 19 green probes leaked a tenant. ⛔ **Never `DELETE FROM company` alone**: it
   strands ACL rows and `/v1/me` then answers **500 for every session**.
5. ⛔ **A GREEN UNIT SUITE CERTIFIES NOTHING HERE.** The audit's single biggest lesson: a traced
   diagnosis was real, its fix passed, its mutation proof failed exactly as predicted — **and the
   browser was still blank.** Only the browser closes a row.

---

## 4. ⛔⛔ THE COLLISION PROTOCOL — TWO LANES, ONE POSTGRES

The T8 lane is working on the **same dev database on :5433**. This has already produced **two false
regressions** (a residue check reporting **+4 companies**, and `internal/sync` hitting its **600s
timeout**) that were pure contention and looked exactly like real code failures.

| Rule | Why |
|---|---|
| ⛔ **Wrap heavy commands in `ci-slot`** — `CI_SLOTS=1 ci-slot -- make verify` | A machine-wide semaphore already installed at `~/.local/bin/ci-slot`. It makes the two lanes take turns instead of colliding |
| ⛔⛔ **NEVER run `make verify` while a browser drive is live**, and vice versa | The documented cause of both false regressions above |
| ⛔ **Throwaway companies only, named with your lane's prefix** (`audit-…`) | So a leaked tenant is attributable to a lane rather than a mystery |
| ⚠️ **Do not run `make migrate` or `reset-dev-db`** | The T8 lane owns the schema this week. If a census fails with *"the delete plan does NOT cover X"*, that is T8 landing a table — **say so and wait**, do not route around it |
| **Migration numbers 0081+ are RESERVED to the T8 lane** | Take none. This lane should need no migration at all |

**Files the T8 lane owns this week — do not edit them:**
`backend/migrations/0080*`, `backend/internal/ledger/forex_schema_test.go`,
`backend/internal/{identity,ledger,masters}/*` (forex paths),
`frontend/src/screens/vouchers/*`, `frontend/src/keystroke/types.ts` (the `MasterKind` union).

⭐ **`FeaturesScreen.tsx` is SHARED and both lanes will want it.** Announce before editing; its rows
are one-per-line so a conflict is trivial to resolve, but a silent double-edit is not.

---

## 5. DEFINITION OF DONE, PER ROW

A row is done when **one** of these is true, with browser evidence either way:

- ⭐ **CLEAN** — driven end to end, does what the row claims. Say so, and say what you drove.
- ⛔ **LYING** — a defect registered in `HANDOFF-PARITY-AUDIT.md` §7.1, at `file:line`, with a
  proposed fix and a severity. ⚠️ **Registering is done; FIXING is a separate decision** — the register
  is currently at 0 open and re-opening it is a deliberate act, not a side effect.

⛔ **Every FIX (if you fix one) carries BOTH proofs:** a mutation proof whose failure was **predicted,
then observed**, *and* a browser re-drive. A mutation proof proves the TEST; only the browser proves
the FIX.

⚠️ **AND RUN EVERY NEW PROBE AGAINST THE BROKEN BUILD FIRST.** A vacuous mutation is indistinguishable
from a passing test — one such mutation set `err.status < 500` on an `instanceof ApiError` guard,
dropped the value into a branch that behaved identically, and proved nothing while staying green.

---

## 6. RUN FIRST — ten minutes, and skipping it has burned sessions

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
git log --oneline -3 && git status --porcelain
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # NEWER than the last backend commit?
```

⛔ **CHECK THE AGE OF `cored`.** `make run-cored` is `go run`, so a stale process is indistinguishable
from a fix that does not work. **Found stale four times.** Run it **from the repo root**.
⛔ **VITE MUST BE ON `:5173`** — WebAuthn origin matching is exact.
⛔ **A TCP CONNECT IS NOT LIVENESS** — docker-proxy holds the port open past a dead Postgres, and a
watchdog built on `nc -z` once logged nothing through a ten-minute outage.

**Baseline at `20dd96f`** (measured today, T8's 0080 included):

| Gate | Value |
|---|---|
| `cd backend && go build ./... && go vet ./... && go test ./... -timeout 60m` | **exit 0** |
| `cd frontend && pnpm exec tsc --noEmit` | clean |
| `cd frontend && pnpm vitest run` | **1784 pass / 109 files** |
| `cd frontend && pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT |
| `python3 corpora/validate.py` | OK |
| `make verify` | exit 0 |

⚠️ **`make verify` DOES NOT RUN `tsc`.** Each gate has caught a real error the other missed.
