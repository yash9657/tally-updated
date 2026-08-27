# HANDOFF — OFFLINE ENTRY: PIECE ONE LANDED, PIECE TWO IS THE WHOLE REMAINDER

**Updated:** 2026-08-27 (later) · **Branch:** `session/t32-and-close-7-3`
**Read time:** ~3 minutes. `HANDOFF.md` **§1.23** is the authority on what shipped and why; this file
is only the plan for what has not.

---

> # ⭐ RESUMING COLD? THIS IS THE WHOLE STATE.
>
> **DONE — do not re-open any of it.** `T32`, `B1`, `C`, `B2`, the reconciliation queue (§1.18–§1.22)
> and now **the queue-or-refuse judgement plus the Offline Outbox** (§1.23):
>
> - a voucher whose post could not reach the server is QUEUED on this machine;
> - ⛔ a voucher the books REFUSED is not queued, and never will be — see §1.23.1 for why the two
>   directions are not symmetric;
> - `Alt+Q` / **Gateway of Tally > Offline Outbox** shows what is waiting, commits the whole queue as
>   ONE write, and can discard a row;
> - the queue survives a reload (real IndexedDB, proved in a browser — jsdom has none);
> - a commit really lands: the voucher reaches Postgres and the Day Book under a SERVER-allocated
>   number, and the screen keeps the `PROV-… → <real number>` mapping for the session.
>
> **LEFT — ONE THING.**
>
> ⛔⛔ **A SERVICE WORKER AND AN OFFLINE APP SHELL.** Until it exists this application STILL NEEDS THE
> SERVER TO LOAD. What shipped helps when a connection drops during a session you already have open;
> it does not let anybody start one on a train.
>
> ⛔ **THEREFORE THE `FeaturesScreen` ROW STAYS SPLIT — DO NOT FLIP IT.** "Offline entry with sync"
> still reads `server` and states which half is missing; "Offline Outbox … (Alt+Q)" reads `on`.
> Flipping the first would absorb a missing feature into a green badge, which is what that screen
> exists to prevent.

---

## 1. THE REMAINING TRACK: A SERVICE WORKER AND AN APP SHELL

**What it has to achieve:** the app BOOTS with the network off, on a machine that has run it before.
Everything downstream of booting already works.

**What already exists and must not be rebuilt:**

- the outbox, the HLC, node-partitioned UUIDv7s, provisional numbering, all three idempotency-key
  formats — `frontend/src/offline/`, a port of `internal/sync/node.go` proved against Go by a
  GENERATED golden (`TestOfflineNodeVectorsGolden` emits the vectors `port.golden.test.ts` asserts);
- the live node, scoped per (company, subject) — `offline/OfflineOutbox.tsx`;
- the judgement — `offline/queueDecision.ts`;
- the screens — `OfflineOutboxScreen` (Alt+Q) and `ReconciliationScreen` (Alt+R).

**⚠️ THE THINGS THAT WILL BITE, MEASURED:**

1. ⛔ **THERE IS NO SERVICE WORKER ANYWHERE TODAY.**
   `/usr/bin/grep -rln "serviceWorker\|workbox" frontend/src/ frontend/public/` → **EMPTY**. This is a
   from-scratch addition, not an extension.
2. ⛔ **THE APP SHELL IS NOT THE HARD PART — THE AUTH GATE IS.** `App.tsx` renders nothing until a
   session exists, `AuthProvider` hydrates from `sessionStorage` (a closed tab is a logged-out tab, on
   purpose), and `CompanyProvider` bootstraps by calling the server. An offline boot has to answer
   "who is signed in, and to which company" with no network, and the honest answers are constrained by
   decisions already made — including that the offline queue is **cleared on sign-out**.
3. ⛔ **DO NOT LET A CACHED SHELL SERVE STALE CODE AFTER A DEPLOY.** A service worker that caches
   `index.html` will happily run last week's bundle against this week's API. Version the cache and
   name the failure mode in the code.
4. ⚠️ **THE `server` BADGE FLIPS ONLY WHEN AN OPERATOR CAN BOOT OFFLINE**, and the row must be edited
   in the same change that makes it true.

## 2. ALSO NOT BUILT, STATED RATHER THAN IMPLIED

- **A queued DELETE.** `Alt+D` takes the ordinary online path; only `Ctrl+A` queues. `OfflineNode`
  supports a DELETE intent (`node.go:186`'s key format is ported), so this is wiring, not design — but
  it needs the loaded voucher's head version, exactly as the queued ALTER does.
- **A request TIMEOUT.** `/usr/bin/grep -rn "AbortController\|AbortSignal" frontend/src/` → **EMPTY**.
  So a STALLED connection hangs rather than failing, and nothing is queued because nothing ever
  rejects. ⚠️ **Adding one is a real decision, not a detail**: it trades the stall for the
  committed-then-abandoned ambiguity (§1.23.1) on a deadline of our choosing.

## 3. THE HAZARDS THIS LANE PAID FOR. `HANDOFF.md` §9 IS THE FULL LIST; THESE ARE THE LIVE ONES.

1. ⛔⛔ **VERIFYING THAT THE PIECES EXIST IS NOT VERIFYING THAT THEY COMPOSE**, and this track has the
   sharpest example in the repository: the whole queue-and-commit flow was green in vitest while
   `POST /sync/commit` sent `dr_cr` un-normalised, so every queued voucher was refused **422 at COMMIT
   TIME** (§1.23.2). The mock client is not an HTTP client, so no unit test could see it. **Try to
   perform the whole BUSINESS TASK end to end, in a browser, every time.**
2. ⛔ **A FIXED SLEEP IN FRONT OF AN ASSERTION IS A FLAKE, AND IT LIES IN THE DANGEROUS DIRECTION.**
   Two probes in `drive-d-offline-queue.mjs` were written that way and both reported a DATA-LOSS claim
   that was false: "the queue did not survive a reload" (the key was delivered before the shell was
   listening) and "the commit wrote nothing" (read 3s after the click; the very next query found the
   voucher). Both now poll. **A probe that says "your books are gone" must be certain.**
3. ⛔ **A DRIVE THAT THROWS BEFORE TEARDOWN LEAKS A TENANT**, and residue drops `verify:demo` below
   34/34. This file's drive died on a mistyped table name after 19 green probes and had to be swept by
   NAME. Every probe now runs inside one `try` so a throw still reaches the sweep. ⚠️ **And never run
   `make verify` concurrently with a browser drive** — they share the dev database; the residue check
   fails and `internal/sync` times out under the contention.
4. ⛔ **THE SCHEMA HAS NO `voucher` TABLE.** It is `voucher_version` (which carries `company_id`
   itself) plus `voucher_head`. Guessing cost a run.
5. ⛔ **`useState` IS THE WRONG GUARD FOR A ONE-SHOT EFFECT WHOSE DEPS INCLUDE A CONTEXT VALUE.** A
   test seeder written that way worked in the first test of a file and looped forever in every test
   after it — appending intents on each pass and, because the loop was driven by promise continuations
   rather than timers, **starving the event loop so vitest's own timeout could never fire.** The
   symptom is not a failing test: it is a worker pegged at 130% CPU and a run that never ends. Use a
   ref. Found by counting calls and printing a stack, not by reading.
6. ⛔ **jsdom HAS NO `indexedDB` AT ALL** and `fake-indexeddb` is not a dependency. The real store
   adapter runs **only** in the browser drive; every unit test exercises the memory fallback, and
   reports `durable: false` while doing so. Do not write an "IndexedDB test" here.
7. ⚠️ **macOS COMPOSES Option+<letter>.** Alt+Q is `œ`. Drive it with the harness's `altMac("q")` and
   keep the entry in `normalize.test.ts`'s `MAC_COMPOSED` table.
8. ⛔ **A GATEWAY LABEL THAT IS A SUBSTRING OF A SHIPPED ONE IS A SILENT COLLISION.** "Offline Outbox"
   was checked against `GatewayScreen.tsx`'s list AND `REPORT_NAMES` before use; its nearest
   neighbour is "Bill Inbox". Check both lists.
9. ⛔ **A NEW `ScreenKind` NEEDS 15 REGISTRATION POINTS AND `tsc` ENFORCES NONE OF THEM.** Adding
   `"offline-outbox"` to the union compiled cleanly with every one of them missing. `HANDOFF.md` §7.2
   is the list; the mutation set in §1.23 proves five of them individually.

## 4. THE RULES THAT ARE NOT NEGOTIABLE

1. **Both proofs, for every change.** An EXECUTED mutation proof whose failure was **predicted, then
   observed**, *and* a drive in a real browser. ⛔ **A mutation proof proves the TEST; only the browser
   proves the FIX** — and on this track the browser is what found the defect.
2. ⛔ **Run every new probe against the BROKEN build first.** This session: 20 mutations, 20
   predictions matched — after one MISMATCH that was the MUTATION's fault. It set
   `err.status < 500` on an `instanceof ApiError` guard, which drops a 500 into the client-fault
   branch, which also refuses to queue: the code changed, the tests stayed green, and it proved
   nothing. **A vacuous mutation is indistinguishable from a passing test.**
3. ⛔ **AN ORACLE THAT CANNOT SEE THE DEFECT IS NOT AN ORACLE.** This track's worked example: the
   "a 422 is not queued" probe asserts **the queue's CONTENTS**, never just the status line — a build
   that queued the voucher AND printed the error would satisfy a status-line assertion while being the
   worst of both outcomes.
4. ⛔ **DRIVE ON A THROWAWAY COMPANY, NEVER `Demo Traders`**, and tear it down with the
   catalog-derived sweep. ⛔ **Never `DELETE FROM company` alone** — it strands ACL rows and `/v1/me`
   then answers 500 for **every** session.
5. **Assert a PROPERTY, not a list.** This track's: *no button on the outbox offers to post or save a
   single queued voucher*, over every button on the page.
6. ⛔ **VERIFY, DO NOT INHERIT — including this file.** Every claim above has a command beside it.

## 5. STILL BLOCKED ON A HUMAN

- ⛔ **A REAL GST-REGISTERED TallyPrime XML EXPORT** (`HANDOFF.md` §10 item 1) — still the commercial
  blocker, still one e-mail. `classifyTaxLedger`, `inferRate` and the GSTR-1 B2B join have **never met
  real GST data**. **Do not touch those three until it arrives.** `Alt+O` turns receiving it from an
  afternoon into ten minutes.
- ⛔ **IRP / GSTN / e-Way Bill SANDBOX CREDENTIALS** (§10 item 4). If they arrive the IRN screen stops
  being groundwork and becomes a filing, with no change to either payload builder. Re-order
  immediately. ⚠️ The Rule 48(4) QR row is what `VoucherScreen.stockVoucher.test.tsx`'s pinned "Not
  built" assertion names — whoever makes it true must re-point that assertion in the same change.

## 6. THE BACKLOG AFTER THIS

`Multi-currency (T8)` → `Budgets (T11)` → `Interest on overdue bills (T13)` → `GSTR-9 (T12)` →
`SSO/OIDC last`. `HANDOFF.md` §5.4 states the one thing not to get wrong in each. ⚠️ **GSTR-9 is worth
deferring until real GST data has been through the importer.**

## 7. WHEN THE SERVICE WORKER LANDS

- Fold it into `HANDOFF.md` **§1.24**, flip §1.11 **row 11**, and edit the `FeaturesScreen` row **in
  the same change that makes it true** — a row flips when an operator gets a BEHAVIOUR, not a file.
- Add a `docs/RELEASE-NOTES.md` note leading with what an operator will act on.
- ⛔ **DELETE THIS FILE.** `HANDOFF.md` §11's rule is *update in place rather than write another*.
  This file was UPDATED rather than replaced for that reason, and there is nothing left in it once the
  app shell exists.
