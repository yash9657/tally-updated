# HANDOFF — WHAT TO DO NEXT, AND WHY IT IS NOT DEFECT WORK

**Written:** 2026-08-27 (late) · **Branch:** `session/t32-and-close-7-3` · **Tree:** clean
**Read time: 5 minutes.** This file is the ORDER and the reasoning. It deliberately holds no analysis
that is not already at a code site or in the files it points at — ⛔ **`git rm` it once §2's first
item lands.** `HANDOFF.md` §11 names eleven superseded handoffs and what they cost — and that list is itself
stale: there are **25 `HANDOFF-*.md` files on disk today**. This one is built to be cheap to retire
rather than to become another.

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
> mounted" measures nothing an operator can do. This class has now produced **seven** findings —
> the seventh was this session's reconciliation queue, whose rows had been written since migration
> 0009 with no screen ever reading them — and **not one was found by reading code.**

**Carry this as a standing habit, not a track:** when you finish anything, try to perform a whole
BUSINESS TASK with it end to end, not just the row you built.

---

## 1. WHERE THE PRODUCT IS, IN ONE PARAGRAPH

Measured from `FeaturesScreen.tsx` today: **52 feature rows — 43 `on`, 1 `always`, 7 `off`, 1
`server`.** The one `server` row is offline entry, which is **half built** as of this session. Of the
7 `off`, one is a **stated refusal** (split company data — Tally splits because it loads everything
into RAM; this product does not have that limit) and one is **blocked on a human** (the Rule 48(4) QR
code needs government sandbox credentials). So the real remaining feature work is **five tracks plus
the other half of offline entry.**

---

## 2. ⛔ THE ORDER, AND THE REASONING

### FIRST — finish OFFLINE ENTRY. `HANDOFF-OFFLINE-ENTRY.md` is the plan; read it, not this.

**Why first:** it is the only feature in a half-built state, and half-built is the worst state a
feature can be in — the engine, the TypeScript client and the reconciliation UI all exist and nothing
can reach them. It is also the **differentiator against Tally's LAN-only model**, so it is the one
remaining track that is not parity but advantage.

⛔ **THE FIRST PIECE IS A JUDGEMENT CALL, NOT A SCREEN, AND IT IS ON THE PRODUCT'S MAIN WRITE PATH:**
when a voucher fails to post, was it the NETWORK (queue it) or did the BOOKS REJECT IT (do not queue —
say so)? A dropped connection must queue; **a 422 or a 403 must not.** Getting it backwards tells an
operator their invoice is safe when it was refused, and they walk away. The funnel is narrow —
`voucherConfig.ts`'s per-class `post`, reached from `editActions.ts:63` and
`StockVoucherScreen.tsx:238` — so the wiring is small and the judgement is the whole job.
**It needs its own browser drive, and the 422 half is the half that matters.**

The second piece — a service worker and an offline app shell — is a separate track and the honest one
to defer. ⛔ **Until it exists, keep the `FeaturesScreen` row SPLIT**: "Offline entry with sync" reads
`server` and names both missing pieces; the reconciliation row reads `on`. Flipping one row would
absorb a missing feature into a green badge, which is what that screen exists to prevent.

### THEN — the five remaining features, in this order

| # | Track | Why here | ⛔ The one thing not to get wrong (`HANDOFF.md` §5.4) |
|---|---|---|---|
| 1 | **Multi-currency (T8)** | The largest genuine gap for a real business; anyone importing or exporting is blocked | A foreign amount, a rate and the base amount are **three stored values**, not two plus arithmetic. Realised vs unrealised gain/loss are **different postings** |
| 2 | **Budgets (T11)** | Common in Tally shops, and it is comparison rather than new posting machinery | A budget is a **comparison dimension, not a posting**. It inherits T2's axis model — see §8.1 |
| 3 | **Interest on overdue bills (T13)** | Small, self-contained, immediately visible on Outstandings | Simple vs compound and the day-count basis are configuration with statutory defaults — **cite them** |
| 4 | **GSTR-9 annual return (T12)** | ⚠️ **Deliberately last of the four.** It is an annual return that reads everything the tax paths produce, and those paths have **never met real GST data** — see §4 | It is a **reconciliation** of the year's returns to the books and must **state where it disagrees** rather than smoothing it |
| 5 | **SSO / OIDC login** | Lowest business value; an enterprise sales unblocker, not an accounting one | — |

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
process is indistinguishable from a fix that does not work. **It has now been found stale three
times**, once costing a whole failed browser run that showed 404s for routes that existed.
⚠️ **Run `make run-cored` FROM THE REPO ROOT** — from `backend/` there is no such target and the
process simply never starts, which looks like a crash.

⛔ **VITE MUST BE ON `:5173`.** WebAuthn origin matching is exact; a vite that took `:5174` signs in
fine and then fails every write with `PASSKEY STEP-UP FAILED`.

**The gate baseline to beat** (measured 2026-08-27, after the reconciliation queue):

| Gate | Command | Value |
|---|---|---|
| backend | `cd backend && go build ./... && go vet ./... && go test ./... -timeout 60m` | clean · **exit 0**, 36 pkgs |
| typecheck | `cd frontend && pnpm exec tsc --noEmit` | clean |
| frontend tests | `cd frontend && pnpm vitest run` | **1729 pass / 102 files** |
| live demo | `cd frontend && pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT |
| corpora | `python3 corpora/validate.py` | OK (keystroke=33, migration=6, inventory=11) |
| everything | `make verify` | exit 0 |
| drives | `drive-t32` · `drive-b1` · `drive-c` · `drive-b2` · `drive-d-reconciliation` | **25/25 · 15/15 · 33/33 · 20/20 · 29/29** |

⚠️ **`make verify` DOES NOT RUN `tsc`.** Different gates, and each has caught a real error the other
missed — including a suite that was green in vitest with 8 real `tsc` errors, and (this session) a
`tsc`-only strict-index error in a file whose tests all passed.

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
| **`HANDOFF.md`** | ⭐ **THE LIVE ONE.** §1.11 is the plan, §1.22 is the newest track, §5 is the parity ordering, §7.2 the registration surface, §9 the traps |
| **`HANDOFF-OFFLINE-ENTRY.md`** | ⭐ **LIVE** — §2's first item, in full |
| **`HANDOFF-NEXT-BUILD.md`** | ⭐ LIVE for §6's long tail; its §1–§4 are spent and say so |
| **`HANDOFF-PARITY-AUDIT.md`** | **DATA, not a driver.** §7.1 is the closed register — parse it, never quote a header |
| **`HANDOFF-DEFECT-BURNDOWN.md`** | ⛔ **SPENT** (retitled 2026-08-27). Its §1 five rules and §2 orchestration pattern are still worth reading for a parallel session |
| **`docs/design/d-offline-entry-model.md`** | ⭐ LIVE — the four offline questions, answered, with a command beside each claim |
| the other ~20 `HANDOFF-*.md` | spent; each says so in its first line |

---

## 6. THE RULES THAT ARE NOT NEGOTIABLE

1. **Both proofs, for every change.** A mutation proof whose failure was **predicted, then observed**,
   *and* a drive in a real browser. ⛔ **A mutation proof proves the TEST; only the browser proves the
   FIX.**
2. ⛔ **Run every new probe against the BROKEN build first.** A probe that has never failed proves
   nothing. This session: 34 mutations, 34 predictions matched — after **one MISMATCH** exposed a test
   asserting `/IRN/` against a string **the server itself supplies**, so it was reading the wire data
   back and checking nothing the screen decided.
3. ⛔ **A MUTATION THAT SILENTLY DOES NOTHING LOOKS EXACTLY LIKE A TEST THAT PASSED.** Assert the
   anchor occurs **exactly once** and print the byte delta. The harness aborted twice this session on
   a two-match anchor — that is it working.
4. ⛔ **AN ORACLE THAT CANNOT SEE THE DEFECT IS NOT AN ORACLE.** Worked example: the resolve probe
   asserts the **queue listing** and **Postgres**, never the handler's return value — a resolve that
   stamped nothing still answers 200.
5. ⛔ **A REFUSAL FOR THE WRONG REASON IS A VACUOUS PROBE.** Three of this session's failures were in
   the probe, not the product: a live 401-vs-404 check that could not distinguish mounted from
   unmounted (auth runs before routing), a Gateway assertion a **different shipped screen** satisfied,
   and a `403 STEPUP_REQUIRED` passkey challenge counted as a failed request.
6. ⚠️ **macOS COMPOSES Option+<letter>.** Drive every Alt binding with the harness's `altMac("x")`,
   and add the composed character to `normalize.test.ts`'s `MAC_COMPOSED` table.
7. ⚠️ **DRIVE ON A THROWAWAY COMPANY, NEVER `Demo Traders`**, and tear it down with the
   catalog-derived sweep (`drive-d-reconciliation.mjs` is the copy-me). ⛔ **Never `DELETE FROM
   company` alone** — it strands ACL rows and `/v1/me` then answers 500 for **every** session.
8. **Assert a PROPERTY, not a list.**
9. ⛔ **USE `/usr/bin/grep` WHENEVER AN ABSENCE IS LOAD-BEARING.** The shell's `grep` is a `ugrep`
   shim that silently skips files it judges binary, and one NUL byte is enough — it once hid 8,672
   lines. This repository's handoffs use `grep … -> EMPTY` as evidence that something is unwired.
10. ⛔ **VERIFY, DO NOT INHERIT — including this file, and including any COUNT in it.** §0 exists
    because that rule was broken by the session that wrote this one.
