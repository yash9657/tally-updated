# HANDOFF — OFFLINE ENTRY, THE HALF THAT IS LEFT

**Updated:** 2026-08-27 (late) · **Branch:** `session/t32-and-close-7-3` · **Tree:** clean
**Renamed from `HANDOFF-THE-LAST-ONE.md`** per that file's own §5 rule, because the track it tracked
is now half landed and only one thing is left.
**Read time:** ~4 minutes. `HANDOFF.md` **§1.22** is the authority on what shipped; this file is only
the plan for what has not.

---

> # ⭐ RESUMING COLD? THIS IS THE WHOLE STATE.
>
> ```
> HEAD     feat(D1/D2): the reconciliation queue      (this session)
> HEAD~    feat(D1):    payload + resolve routes
> HEAD~~   docs:        track D's model
> ```
>
> **DONE — do not re-open any of it.** `T32`, `B1`, `C`, `B2` (`HANDOFF.md` §1.18–§1.21) and now
> **the reconciliation queue** (§1.22): `Alt+R` / Gateway > **Reconciliation Queue**, two new backend
> routes, the first writer of `resolved_at` that has ever existed, and a TypeScript port of the
> offline node proved against Go by a generated golden.
>
> **LEFT — one track, two pieces, and the first is a DECISION rather than a screen.**
>
> ⛔⛔ **THE OFFLINE NODE IS ALREADY PORTED AND PROVED. DO NOT RE-DESIGN IT, AND DO NOT REWRITE IT.**
> `frontend/src/offline/` holds the outbox, the HLC, node-partitioned UUIDv7s, provisional numbering
> and all three idempotency-key formats. `internal/sync`'s `TestOfflineNodeVectorsGolden` GENERATES
> the vectors `port.golden.test.ts` asserts against, so the two cannot drift silently — and that
> matters because the server's exactly-once replay matches on the **key string**: a port that spelled
> one differently would make a re-commit **apply twice** instead of answering `DUPLICATE`.
>
> ⛔ **NOTHING PUTS A VOUCHER INTO THAT OUTBOX. THAT IS THE TRACK.**
>
> ### The first ten minutes, in order
>
> | # | Do this | Why |
> |---|---|---|
> | 1 | `git log --oneline -3` · `git status --porcelain` | Clean tree at the commits above |
> | 2 | Check the ports **and the AGE of `cored`** | ⛔ Stale `cored` has now been found **three** times, once costing a whole failed browser run. `ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"` must be NEWER than the last backend commit. ⚠️ **Run `make run-cored` from the REPO ROOT** — from `backend/` there is no such target and the process simply never starts |
> | 3 | Re-measure `HANDOFF.md` §1.22.8's gate table | It is the baseline you will be judged against |
> | 4 | Read **§1** below, then `HANDOFF.md` §1.22.6 | The decision is the work |
>
> ⛔ **VITE MUST BE ON `:5173`.** WebAuthn origin matching is exact; a vite on `:5174` signs in fine
> and then fails every write with `PASSKEY STEP-UP FAILED`.

---

## 1. THE TRACK: WHAT IS LEFT, IN ORDER

### 1.1 ⛔⛔ PIECE ONE IS A JUDGEMENT CALL ON THE PRODUCT'S MAIN WRITE PATH

**When does a failed post become a queued intent?**

- a **dropped connection** (a `fetch` TypeError, a timeout) **must queue**;
- ⛔ **a `422` the books refused must NOT queue**, and neither must a `403`. Confusing the two tells an
  operator their voucher is safe when it was **rejected** — which is strictly worse than the error
  they would otherwise have seen, because they walk away.

The funnel is narrow, which is the good news: `screens/vouchers/voucherConfig.ts` carries a per-class
`post`, reached from **`editActions.ts:63`** and **`StockVoucherScreen.tsx:238`**. So the wiring is
small and the judgement is the whole job.

⛔ **DO NOT BOLT THIS ON WITHOUT ITS OWN DRIVE.** It is the path every voucher in the product takes.
A drive must prove, in a browser, that a genuine network failure queues **and** that a 422 does not —
and the 422 half is the one that matters, because it is the direction that loses money.

⚠️ **THE OUTBOX SCREEN COMES WITH IT, NOT BEFORE IT.** A queue screen that nothing can enqueue into
is a screen advertising a hole; that is why this session did not ship one. When it lands it needs the
eleven-file registration surface (`HANDOFF.md` §7.2) — `Alt+Q` is free, and so are B, F, H, J, K, N,
T, U.

### 1.2 PIECE TWO: THE SERVICE WORKER AND THE APP SHELL

A separate track, and the honest one to defer. Until it exists this application needs the server and
nothing can be keyed on a train — which is what the `FeaturesScreen` row says.

⛔ **THE ROW WAS SPLIT, NOT FLIPPED.** "Offline entry with sync" still reads `server` and states both
missing pieces; "Reconciliation — the changes these books refused (Alt+R)" reads `on`. Flipping one
row would have absorbed a missing feature into a green badge, which is what that screen exists to
prevent. **Keep the split.**

### 1.3 WHAT IS ALREADY DECIDED — DO NOT RE-LITIGATE

`docs/design/d-offline-entry-model.md` answers §3.2's four questions with a command beside every
claim. In one line each:

| Question | Answer |
|---|---|
| what a queued voucher looks like | its own screen, never the Day Book; the column is headed **Provisional**, never "No."; on COMMITTED show `PROV-… → INV/2026-27/42` so a quoted number can be corrected |
| a `CONFLICT` | two panes, and exactly two choices — **discard mine** / **re-key as a NEW voucher**. ⛔ never "force mine". Shipped: `ReconciliationScreen` |
| a `FROZEN` | explained, not retried; the row **names the pin**; the way forward is a credit note. Shipped |
| where the queue lives | **IndexedDB**, scoped per (company, subject), cleared on sign-out, **unencrypted and the screen says so**. Shipped: `frontend/src/offline/store.ts` |

⛔ **AND THE FIFTH OUTCOME §3.2 DID NOT LIST, WHICH PIECE ONE WILL MEET:** a **whole-batch `403`**.
The back-dated cutoff refuses the LOT if one intent is pre-cutoff ("a batch is only as permitted as
its most-refused member" — cutoff round 2), and `STEPUP_REQUIRED` is the same shape. Neither is a
per-intent status; **the outbox is intact and must be left that way**, and rendering either as "these
12 vouchers failed" invites a re-key of work that is still queued. Because the commit is ONE write,
the whole outbox costs exactly one step-up — the argument for draining as a batch.

---

## 2. THE HAZARDS THIS LANE PAID FOR. `HANDOFF.md` §9 IS THE FULL LIST; THESE ARE THE LIVE ONES.

1. ⛔⛔ **THE SHELL'S `grep` IS A `ugrep` SHIM THAT SILENTLY SKIPS "BINARY" FILES**, and one NUL byte
   is enough (`mock.ts` hid 8,672 lines this way). **This repository's handoffs use `grep … -> EMPTY`
   as evidence that something is unwired.** Use **`/usr/bin/grep` whenever an absence is
   load-bearing** — both of track D's backend gaps were established that way.
2. ⛔ **A REGISTRATION POINT NO TEST CAN SEE.** `COMPANY_SCOPED_SCREENS` is consulted only when
   `company === null`, and every test passes an `initialCompany`. ✅ **Track D added the missing
   test** — `ReconciliationScreen.test.tsx` has the company-less case, and a mutation proved it bites.
   ⚠️ **The other fourteen screens in that set are still uncovered.**
3. ⛔ **A GATE OVER A SERVER DECISION IS A GENERATED GOLDEN.** Copy `TestOfflineNodeVectorsGolden` or
   track C's `template_surface_golden_test.go` — **both need no Postgres**, so neither can hide behind
   a skip. B1's can, and a skip is indistinguishable from a pass in `go test`'s exit code.
4. ⛔ **`export/download.ts`'s `downloadBlob` IS THE ONLY DOWNLOAD ANCHOR.** Do not write a fourth.
5. ⛔ **A GATEWAY LABEL THAT IS A SUBSTRING OF A SHIPPED ONE IS A COLLISION, AND IT IS SILENT.** The
   door was called "Reconciliation"; **"Bank Reconciliation"** contains the word, the Gateway is
   driven by typing into its search box, and Enter opened the BANK screen every time — arriving at
   "this company has no bank accounts", which reads as an empty queue rather than a wrong turn.
   ⚠️ **The probe could not see it either**: it asserted `/Reconciliation/i`, which the bank screen
   satisfies. **Check a new label against `GatewayScreen.tsx`'s existing list AND against
   `REPORT_NAMES` before using it.**
6. ⛔ **CLICKING A GATEWAY LABEL IN A DRIVE DOES NOT OPEN IT** — the `SelectionList` keeps its default
   selection. Use the harness's `openFromGateway(label)`, **with the FULL label** (a short one cost
   two runs this session), and remember `Alt+G` is hard-wired to `["Vouchers", ...REPORT_NAMES]`.
7. ⛔ **NEVER `DELETE FROM company` ALONE** — it leaves ACL and membership rows behind and `/v1/me`
   answers 500 for **every** session. Copy the catalog-derived sweep from
   `drive-d-reconciliation.mjs`. ⚠️ **A drive that throws before teardown leaks a tenant** — that
   happened twice this session; sweep by name before re-running.
8. ⛔ **A 403 ON A WRITE IS USUALLY `STEPUP_REQUIRED`, NOT A DENIAL.** Assert the response **body**,
   never a count of non-2xx: the app answers the passkey ceremony and retries, and the retry is the
   one that lands. A probe that treats the challenge as a failure reports a defect that is not one.
9. ⛔ **jsdom LACKS MORE THAN YOU THINK.** `File.text()`, `File.arrayBuffer()`, `File.stream()`,
   `Blob.arrayBuffer()` — **and `indexedDB` entirely** (measured; `fake-indexeddb` is not a
   dependency). ⛔ **Do not write an "IndexedDB test" here**: it exercises the memory fallback while
   reading as coverage. The store's decisions are unit-tested in a pure layer; the adapter's proof is
   the browser drive.
10. ⛔ **`buttons.test.ts` CANNOT SEE A DECORATIVE *GLOBAL* KEY ON A BAR** (Alt+M, Ctrl+P, and now
    Alt+R). If a track advertises a global on a bar, assert its placement **directly as a property**.

---

## 3. THE RULES THAT ARE NOT NEGOTIABLE

1. **Both proofs, for every fix.** An EXECUTED mutation proof whose failure was **predicted, then
   observed**, *and* a drive in a real browser.
2. ⛔ **Run every new probe against the BROKEN build first.** A probe that has never failed proves
   nothing. This session: 31 mutations, 31 predictions matched — after one MISMATCH exposed a test
   asserting `/IRN/` against a string **the server itself supplies**, which was reading the wire data
   back and checking nothing the screen decides.
3. ⛔ **A MUTATION THAT SILENTLY DOES NOTHING LOOKS EXACTLY LIKE A TEST THAT PASSED.** Assert the
   anchor occurs **exactly once** before replacing it and print the byte delta. The harness aborted
   twice this session on a two-match anchor, which is it working.
4. ⛔ **AN ORACLE THAT CANNOT SEE THE DEFECT IS NOT AN ORACLE.** Track D's worked example: the resolve
   probe asserts the **queue listing** and **Postgres**, never the handler's own return value — a
   resolve that stamped nothing still answers 200.
5. ⛔ **A REFUSAL FOR THE WRONG REASON IS A VACUOUS PROBE.** Three of this session's failures were in
   the probe: a live 401-vs-404 check that could not distinguish mounted from unmounted (auth runs
   before routing), a Gateway assertion a different screen satisfied, and a step-up challenge counted
   as a failed request.
6. ⚠️ **macOS COMPOSES Option+<letter>.** Drive every Alt binding with the harness's `altMac("x")`,
   and add the composed character to `normalize.test.ts`'s `MAC_COMPOSED` table (Alt+R → `®`).
7. ⚠️ **DRIVE ON A THROWAWAY COMPANY, NEVER `Demo Traders`**, and tear it down. Residue drops
   `verify:demo` below 34/34.
8. **Assert a PROPERTY, not a list.** Track D's: *no button anywhere offers to force a stale version
   through*, over every button on the page — so a helpfully-worded addition later cannot slip past.
9. ⛔ **VERIFY, DO NOT INHERIT — including this file.** Every claim has a command beside it.

### 3.1 The mutation harness — worth rebuilding, ~120 lines

A python script (session scratchpad, **not committed**) holding a table of
`(name, file, anchor, replacement, tests, predicted-RED)`. Per entry: assert the anchor occurs exactly
once; write it and assert it applied, printing the byte delta; run the named tests and report failures
by name; revert and assert byte-identical; compare predicted against observed and print
`PROVED` / `MISMATCH`.

---

## 4. STILL BLOCKED ON A HUMAN

- ⛔ **A REAL GST-REGISTERED TallyPrime XML EXPORT** (`HANDOFF.md` §10 item 1) — still the commercial
  blocker, still one e-mail. `classifyTaxLedger`, `inferRate` and the GSTR-1 B2B join have **never met
  real GST data**; the only real file the importer was fixed against is a grain trader, GST-free and
  zero-rated. **Do not touch those three until it arrives** — speculative changes destroy the evidence
  the file would give you. `Alt+O` turns receiving it from an afternoon into ten minutes.
- ⛔ **IRP / GSTN / e-Way Bill SANDBOX CREDENTIALS** (§10 item 4). If they arrive the IRN screen stops
  being groundwork and becomes a filing, and the e-way bill becomes real **with no change to either
  payload builder**. Re-order immediately. ⚠️ The Rule 48(4) QR row is what
  `VoucherScreen.stockVoucher.test.tsx`'s pinned "Not built" assertion names — **whoever makes it true
  must re-point that assertion in the same change**.

## 5. THE BACKLOG AFTER THIS

`Multi-currency (T8)` → `Budgets (T11)` → `Interest on overdue bills (T13)` → `GSTR-9 (T12)` →
`SSO/OIDC last`. `HANDOFF.md` §5.4 states the one thing not to get wrong in each. ⚠️ **GSTR-9 is worth
deferring until real GST data has been through the importer** — it is an annual return and it reads
everything the untested tax paths produce.

## 6. WHEN THE LAST PIECE LANDS

- Fold it into `HANDOFF.md` **§1.23**, flip §1.11 **row 10**, and edit the `FeaturesScreen` row **in
  the same change that makes it true** — a row flips when an operator gets a SCREEN, not a route.
- Add a `docs/RELEASE-NOTES.md` note leading with what an operator will act on.
- ⛔ **DELETE THIS FILE.** `HANDOFF.md` §11's rule is *update in place rather than write another*, and
  its evidence is twelve superseded files that each re-derived context the last already held. This is
  the thirteenth unless it is deleted rather than renamed again.
