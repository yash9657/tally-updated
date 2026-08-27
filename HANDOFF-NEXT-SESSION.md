# HANDOFF — WHAT TO DO NEXT, AND WHY IT IS NOT DEFECT WORK

**Written:** 2026-08-27 (late) · **Updated:** 2026-08-27 (later — offline entry piece one landed)
**Branch:** `session/t32-and-close-7-3`
**Read time: 5 minutes.** This file is the ORDER and the reasoning. It deliberately holds no analysis
that is not already at a code site or in the files it points at.

⛔ **IT WAS NOT DELETED, AND HERE IS WHY.** Its own §2 said to `git rm` it once the first item landed.
That item was "finish OFFLINE ENTRY", and only **piece one** of it has — the service worker is still
outstanding — so the order has CHANGED rather than completed, and a stale order file is exactly what
§0 warns about. ⛔ **`git rm` it once §2's first item lands in full.** `HANDOFF.md` §11 names eleven
superseded handoffs and what they cost; there are **25 `HANDOFF-*.md` files on disk today**.

---

## 0. ⛔⛔ READ THIS BEFORE YOU PLAN ANYTHING — IT CHANGES WHAT YOU WOULD DO

### The defect backlog is EMPTY. Verified by parsing, not by reading a header.

```
$ python3 - <<'PY'   # the snippet lives in HANDOFF-THE-REMAINING-SIX.md §0
… parses HANDOFF-PARITY-AUDIT.md §7.1's table, reading the STATUS CELL not the line …
PY
{'CLOSED': 97} total 97
OPEN: (none)   PARTIAL: (none)   ?: (none)
```

**All 34 keyboard-reachable feature rows were driven end to end in a real browser. 29 of 34 carried a
defect. All 97 registered defects are closed**, each with a mutation proof AND a browser re-drive.

⛔ **DO NOT START A "FIX THE DEFECTS" SESSION. THERE IS NOTHING IN THE REGISTER TO FIX.**

⚠️ **AND HERE IS THE MISTAKE THAT MAKES THIS SECTION NECESSARY.** On 2026-08-27 a session read
`HANDOFF-DEFECT-BURNDOWN.md`'s title — *"69 OPEN"*, written 2026-08-12, 61 code commits earlier — and
reported it to the user as live work. It was stale by fifteen days. That file is now retitled SPENT.
**A count written in prose is a COPY, and copies go stale**; §7.1's own heading has been wrong twice
for the same reason. **Parse the rows.**

### What the register CANNOT see, which is the standing risk

The register is organised **row by row**. The payroll defect was not: `SetSalaryComponent` and
`SetStatutoryElection` had no route at all, so the whole module could not pay anybody over HTTP — and
no register had a row for it **because nobody had tried to perform the task**.

> ⛔ **VERIFYING THAT THE PIECES EXIST IS NOT VERIFYING THAT THEY COMPOSE.** "Eight routes are
> mounted" measures nothing an operator can do. This class has now produced **eight** findings —
> the eighth is the sharpest yet and it is worth reading in full (`HANDOFF.md` §1.23.2): the offline
> queue's whole queue-and-commit flow was GREEN IN VITEST while `POST /sync/commit` sent `dr_cr`
> un-normalised, so **every queued voucher was refused 422 at COMMIT time.** The mock client is not an
> HTTP client, so no unit test could see it. It appeared on the first real commit from a browser.
> **Not one of the eight was found by reading code.**

**Carry this as a standing habit, not a track:** when you finish anything, try to perform a whole
BUSINESS TASK with it end to end, not just the row you built.

---

## 1. WHERE THE PRODUCT IS, IN ONE PARAGRAPH

Measured from `FeaturesScreen.tsx` today: **53 feature rows — 44 `on`, 1 `always`, 7 `off`, 1
`server`.** The one `server` row is offline entry, which is now **half built and honest about which
half** (`HANDOFF.md` §1.23): a dropped connection queues the voucher and `Alt+Q` commits it, but there
is no service worker, so the app still needs the server to LOAD. Of the 7 `off`, one is a **stated
refusal** (split company data — Tally splits because it loads everything into RAM; this product does
not have that limit) and one is **blocked on a human** (the Rule 48(4) QR code needs government
sandbox credentials). So the real remaining feature work is **five tracks plus the service worker.**

---

## 2. ⛔ THE ORDER, AND THE REASONING

### ⛔ FIRST — **T8 MULTI-CURRENCY**. `HANDOFF-T8-MULTI-CURRENCY.md` is the plan; read it, not this.

⚠️ **THE ORDER CHANGED ON 2026-08-27, AND HERE IS WHY — DO NOT SILENTLY REVERT IT.** This file used
to put the service worker first, on the grounds that offline entry was *"the only feature in a
half-built state — the engine, the client port and the queue UI all exist and nothing can reach
them."* ⭐ **That reason has been CONSUMED** (`HANDOFF.md` §1.23): a dropped connection now queues the
voucher, `Alt+Q` commits it and it lands, and the `FeaturesScreen` row honestly names the one half
still missing. **Nothing is unreachable any more**, so the argument that ranked infrastructure above
the largest accounting gap no longer holds.

**Why T8 now:** it is **the largest genuine gap for a real business — anyone who imports or exports is
blocked today**, and it is accounting work rather than plumbing.

⛔ **IT IS A SCHEMA TRACK, NOT A UI TRACK, AND THE FIRST DELIVERABLE IS A DESIGN DOCUMENT.** Measured:
there is **no** currency table, **no** rate table, **no** forex column, and **no** Go logic — every
`currency` hit in `backend/internal` is the word *conCURRENCy*. Three facts already constrain the
design (all in the plan, with commands): `posting.amount` must stay the BASE amount because a deferred
DB trigger sums it; `posting` rows are IMMUTABLE, so revaluation must be a new voucher; and an
exchange RATE is not fixed-4, which this product has no precedent for.

⛔⛔ **AND THE REAL RISK IS NOT CODE: `RESEARCH.md` DOES NOT PIN TALLY'S MULTI-CURRENCY BEHAVIOUR** —
one passing mention, about print formats. This repository cites RESEARCH.md for every keystroke and
behaviour; here there is nothing to cite. **Ask the user the §5 questions rather than inventing a
model.** Track D was run this way (`docs/design/d-offline-entry-model.md` before any code) and it
caught two gaps that would have made the screen unbuildable.

### THEN — in this order

| # | Track | Why here | ⛔ The one thing not to get wrong (`HANDOFF.md` §5.4) |
|---|---|---|---|
| 1 | **Offline entry, piece two — the service worker** (`HANDOFF-OFFLINE-ENTRY.md`) | Finishes the differentiator against Tally's LAN-only model. Deferred rather than dropped | ⛔ **The AUTH GATE is the hard part, not the cache.** `App.tsx` renders nothing without a session, `AuthProvider` hydrates from `sessionStorage`, `CompanyProvider` bootstraps over HTTP — so an offline boot must answer "who is signed in, to which company" with no network. And ⛔ **never let a cached shell serve stale code after a deploy.** ⛔ **KEEP THE `FeaturesScreen` ROW SPLIT** until an operator can BOOT offline |
| 2 | **Budgets (T11)** | Common in Tally shops, and it is comparison rather than new posting machinery | A budget is a **comparison dimension, not a posting**. It inherits T2's axis model — see §8.1 |
| 3 | **Interest on overdue bills (T13)** | Small, self-contained, immediately visible on Outstandings | Simple vs compound and the day-count basis are configuration with statutory defaults — **cite them** |
| 4 | **GSTR-9 annual return (T12)** | ⚠️ **Deliberately last.** It is an annual return that reads everything the tax paths produce, and those paths have **never met real GST data** — see §4 | It is a **reconciliation** of the year's returns to the books and must **state where it disagrees** rather than smoothing it |
| 5 | **SSO / OIDC login** | Lowest business value; an enterprise sales unblocker, not an accounting one | — |

⭐ **A CHEAP HONEST FIX FOUND WHILE SCOUTING T8, WORTH DOING EVEN IF T8 SLIPS.** The TallyPrime XML
importer **ignores currency entirely** (`/usr/bin/grep -rni currency backend/internal/migration/*.go`
minus *concurrency* → **EMPTY**), so a real Tally file with foreign-currency vouchers imports them as
if the amounts were rupees — silently. ⛔ **It is not in the defect register**, because that register is
row-by-row over keyboard-reachable features and nobody had tried to perform this task. Making the
importer DISCLOSE such a voucher rather than absorb it is small, and `TallyImportScreen` already has
the disclosure surface.

**Not on this list, on purpose:** split company data (a stated refusal, and the row says so) and the
Rule 48(4) QR code (§4 — it is one e-mail away, not one track).

---

## 3. RUN FIRST — ten minutes, and skipping it has burned sessions

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
git log --oneline -3 && git status --porcelain
lsof -nP -iTCP:8087 -sTCP:LISTEN -t
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # NEWER than the last backend commit?
```

⛔ **CHECK THE AGE OF `cored` BEFORE BELIEVING ANY DRIVE.** `make run-cored` is `go run`, so a stale
process is indistinguishable from a fix that does not work. **It has now been found stale four
times** — including at the start of this session — once costing a whole failed browser run that showed
404s for routes that existed.
⚠️ **Run `make run-cored` FROM THE REPO ROOT** — from `backend/` there is no such target and the
process simply never starts, which looks like a crash.

⛔ **VITE MUST BE ON `:5173`.** WebAuthn origin matching is exact; a vite that took `:5174` signs in
fine and then fails every write with `PASSKEY STEP-UP FAILED`.

⛔⛔ **NEVER RUN `make verify` CONCURRENTLY WITH A BROWSER DRIVE.** They share the dev database. Doing
it once this session produced two failures that were pure artefacts: the residue check reported
**+4 companies** and `internal/sync` hit its **600s timeout** under the contention. Both looked like
real regressions and neither was. Run the drives, then run `make verify` alone.

**The gate baseline to beat** (measured 2026-08-27, after offline entry piece one):

| Gate | Command | Value |
|---|---|---|
| backend | `cd backend && go build ./... && go vet ./... && go test ./... -timeout 60m` | clean · **exit 0**, 36 pkgs |
| typecheck | `cd frontend && pnpm exec tsc --noEmit` | clean |
| frontend tests | `cd frontend && pnpm vitest run` | **1784 pass / 109 files** |
| live demo | `cd frontend && pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT |
| corpora | `python3 corpora/validate.py` | OK (keystroke=33, migration=6, inventory=11) |
| everything | `make verify` | exit 0 |
| drives | `drive-d-offline-queue` · `drive-d-reconciliation` · `drive-t32` · `drive-b1` · `drive-c` · `drive-b2` | **27/27 · 29/29 · 25/25 · 15/15 · 33/33 · 20/20** |

⚠️ **`make verify` DOES NOT RUN `tsc`.** Different gates, and each has caught a real error the other
missed — including a suite that was green in vitest with 8 real `tsc` errors, and (this session) a
required field added to a store snapshot that vitest happily ignored and `tsc` refused.

---

## 4. ⛔ WHAT ONLY A HUMAN CAN DO — BOTH ARE ONE E-MAIL, BOTH GATE REAL WORK

1. ⛔⛔ **A REAL GST-REGISTERED TallyPrime XML EXPORT.** This is the **commercial blocker**.
   `classifyTaxLedger`, `inferRate` and the GSTR-1 B2B join have **never met real GST data** — the
   only real file this importer was ever fixed against is a **grain trader, GST-free and
   zero-rated**. ⛔ **Do not touch those three until it arrives**: speculative changes destroy the
   evidence the file would give you, and this is why GSTR-9 is ordered last in §2.
   The `Alt+O` import screen turns receiving it from an afternoon into ten minutes.
2. ⛔ **IRP / GSTN / e-Way Bill SANDBOX CREDENTIALS.** If they arrive, the IRN screen stops being
   groundwork and becomes a filing, and the e-way bill becomes real **with no change to either
   payload builder** — so **re-order immediately**, it jumps the queue in §2.
   ⚠️ The Rule 48(4) QR row is what `VoucherScreen.stockVoucher.test.tsx`'s pinned "Not built"
   assertion names — **whoever makes it true must re-point that assertion in the same change.**

---

## 5. THE FILE MAP — WHICH ONES ARE LIVE

| File | Status |
|---|---|
| **`HANDOFF.md`** | ⭐ **THE LIVE ONE.** §1.11 is the plan, **§1.23 is the newest track**, §5 is the parity ordering, §7.2 the 15-point registration surface, §9 the traps |
| **`HANDOFF-T8-MULTI-CURRENCY.md`** | ⭐ **LIVE — §2's FIRST ITEM**, in full: what exists (nothing, measured), the three schema facts that constrain the design, the six design questions, and what to ask the user |
| **`HANDOFF-OFFLINE-ENTRY.md`** | ⭐ **LIVE** — §2's item 1 after T8 (the service worker), plus the nine hazards this lane paid for |
| **`HANDOFF-NEXT-BUILD.md`** | ⭐ LIVE for §6's long tail; its §1–§4 are spent and say so |
| **`HANDOFF-PARITY-AUDIT.md`** | **DATA, not a driver.** §7.1 is the closed register — parse it, never quote a header |
| **`HANDOFF-DEFECT-BURNDOWN.md`** | ⛔ **SPENT** (retitled 2026-08-27). Its §1 five rules and §2 orchestration pattern are still worth reading for a parallel session |
| **`docs/design/d-offline-entry-model.md`** | ⭐ LIVE — the four offline questions, answered, with a command beside each claim |
| **`docs/RELEASE-NOTES.md`** | ⭐ LIVE — what an operator will notice, newest first |
| the other ~20 `HANDOFF-*.md` | spent; each says so in its first line |

---

## 6. THE RULES THAT ARE NOT NEGOTIABLE

1. **Both proofs, for every change.** A mutation proof whose failure was **predicted, then observed**,
   *and* a drive in a real browser. ⛔ **A mutation proof proves the TEST; only the browser proves the
   FIX** — and this session the browser is what found the only real defect (§0).
2. ⛔ **Run every new probe against the BROKEN build first.** A probe that has never failed proves
   nothing. This session: 20 mutations, 20 predictions matched — after **one MISMATCH** that was the
   MUTATION's fault: it set `err.status < 500` on an `instanceof ApiError` guard, which drops a 500
   into the client-fault branch, which also refuses to queue. The code changed, the tests stayed
   green, and it proved nothing.
3. ⛔ **A MUTATION THAT SILENTLY DOES NOTHING LOOKS EXACTLY LIKE A TEST THAT PASSED.** Assert the
   anchor occurs **exactly once** and print the byte delta.
4. ⛔ **AN ORACLE THAT CANNOT SEE THE DEFECT IS NOT AN ORACLE.** Worked example from this session: the
   "a 422 is not queued" probe asserts **the queue's CONTENTS**, never just the status line — a build
   that queued the voucher AND printed the error would satisfy a status-line assertion while being the
   worst of both outcomes.
5. ⛔⛔ **A FIXED SLEEP IN FRONT OF AN ASSERTION IS A FLAKE, AND IT LIES IN THE DANGEROUS DIRECTION.**
   Two probes written that way reported false DATA-LOSS claims this session ("the queue did not survive
   a reload"; "the commit wrote nothing") — both were the probe being early. **A probe that says
   "your books are gone" must be certain.** Poll.
6. ⚠️ **macOS COMPOSES Option+<letter>.** Drive every Alt binding with the harness's `altMac("x")`,
   and add the composed character to `normalize.test.ts`'s `MAC_COMPOSED` table (Alt+Q → `œ`).
7. ⚠️ **DRIVE ON A THROWAWAY COMPANY, NEVER `Demo Traders`**, and tear it down with the
   catalog-derived sweep. ⛔ **Never `DELETE FROM company` alone** — it strands ACL rows and `/v1/me`
   then answers 500 for **every** session. ⛔ **And wrap the whole drive in one `try` so a throw still
   reaches the teardown**: a drive that died after 19 green probes leaked a tenant this session.
8. **Assert a PROPERTY, not a list.**
9. ⛔ **USE `/usr/bin/grep` WHENEVER AN ABSENCE IS LOAD-BEARING.** The shell's `grep` is a `ugrep`
   shim that silently skips files it judges binary, and one NUL byte is enough — it once hid 8,672
   lines. This repository's handoffs use `grep … -> EMPTY` as evidence that something is unwired.
10. ⛔ **VERIFY, DO NOT INHERIT — including this file, and including any COUNT in it.** §0 exists
    because that rule was broken by the session that wrote the first version of this file.
