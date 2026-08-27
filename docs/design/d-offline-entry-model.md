# Track D — the offline entry screen: the model

**Written:** 2026-08-27 · **Status:** DESIGN ANSWERED, no code written yet
**Prerequisite:** `HANDOFF-THE-LAST-ONE.md` §3. This file is that section's §3.2 deliverable —
*"answer the four questions in writing; the answers are the deliverable before the code is."*

Every claim below was MEASURED today against the tree at `9955e48`, with `/usr/bin/grep`
(§0.1 item 1) wherever an absence is load-bearing. The command is beside each one.

---

## 0. THE HEADLINE, BEFORE THE FOUR ANSWERS

### 0.1 ⭐ THE CLIENT IS A PORT, NOT A DESIGN. `internal/sync/node.go` IS THE SPEC.

The handoff says D is "thinking, not typing" and that is right about the *screens*. It is
**wrong about the intent-generation half**, and this is the single most useful thing found today:

`backend/internal/sync/node.go` is a **complete, working reference implementation of the offline
client** — the one the partition fuzzer drives. It pins, executably:

| What | Where | Value |
|---|---|---|
| provisional number | `node.go:137` | `INV/<fy>/PROV-<nodeID>-<seq>` |
| CREATE idempotency key |  `node.go:148` | `<nodeID>:CREATE:<clientVoucherID>` |
| ALTER key |  `node.go:169` | `<nodeID>:ALTER:<targetID>:<seq>` |
| DELETE key |  `node.go:186` | `<nodeID>:DELETE:<targetID>:<seq>` |
| `payload.VoucherNo` | `node.go:146`, `:167` | **forced empty** — never send the provisional as `voucher_no` |
| base-version sourcing | `node.go:114` `knownHeads` | voucher_id -> last observed head; `Learn()` seeds it |
| outbox | `node.go:115` | append-only; nothing mutates the server while offline |

⛔ **DO NOT INVENT THE OUTBOX. PORT IT.** Any divergence in an idempotency-key format silently
breaks exactly-once replay — the server's `DUPLICATE` path keys off that string. `fuzz_test.go`
(37KB) is the executable spec of correct behaviour; read it before writing the TS.

So the honest split of track D is:

- **a port** — outbox, HLC, node-partitioned UUIDv7, provisional numbers, key formats;
- **a design** — the three screens, the storage decision, and §0.2's two missing routes.

### 0.2 ⛔ TWO BACKEND GAPS MAKE Q2 UNBUILDABLE TODAY. BOTH ARE MEASURED ABSENCES.

**Gap 1 — the parked payload has NO HTTP route.** `parkConflict` preserves the losing intent's
full body in `reconciliation_queue.payload` (JSONB, migration 0010) and `Service.ConflictPayload`
reads it back — but that method has **no caller outside Go tests**:

```
$ /usr/bin/grep -rn "ConflictPayload" backend --include='*.go'
  internal/sync/fuzz_test.go:404,488,490,496   internal/sync/service.go:308,313,317   internal/sync/service_test.go:297
```

`handleReconciliationQueue` (`server.go:2337`) serialises `id · voucher_id · attempted_op · kind ·
ref_value · frozen_voucher_no · detail · created_at` — **no payload**. So a client can see THAT a
conflict happened and cannot see WHAT THE OPERATOR KEYED. §3.2 Q2 requires exactly that.

**Gap 2 — `resolved_at` has NO WRITER ANYWHERE.** The column exists (migration 0009) and every
read filters `resolved_at IS NULL` (`internal/ledger/freeze.go:167`), but nothing ever sets it:

```
$ /usr/bin/grep -rn "UPDATE reconciliation_queue" backend --include='*.go' --include='*.sql'   -> EMPTY
$ /usr/bin/grep -rni "func.*resolve" backend --include='*.go' | grep -i "recon\|freeze\|conflict\|queue"  -> EMPTY
```

⛔ **A reconciliation row is therefore IMMORTAL.** Every conflict an operator handles stays open
forever, and their "choose" has nowhere to record itself. A reconciliation screen built without
fixing this shows a queue that only ever grows.

**Gap 3 (free finding, already true today) — the queue is invisible to every operator.** Nothing
in the frontend calls the route, and the report of the same name answers **501**:

```
$ /usr/bin/grep -rn "reconciliation-queue\|reconciliationQueue" frontend/src/
  src/api/mock.ts:1930 "report:reconciliation-queue"      (a report NAME only)
  src/api/goldens/printed-report-layouts.json:32           (in answers_501, NOT prints_a_layout)
$ /usr/bin/grep -rn "Reconciliation Queue" frontend/src/   -> EMPTY   (no UI name)
```

So the **FROZEN rows the shipped freeze rule already raises today** — from IRN pinning, a live
feature — have never been shown to anybody. Track D's reconciliation screen fixes a present
defect, not only an offline one. ⭐ **That is the argument for shipping the queue UI first.**

---

## 1. THE FOUR ANSWERS

### Q1 — What does a queued voucher look like, and how is it visibly NOT a posted voucher?

**Answer: it lives ONLY on its own screen, and its number column is never headed like a voucher
number.**

1. **A queued intent never appears in the Day Book, a register, or any report.** It cannot — it is
   not on the server. This is a property to *assert*, not merely to honour.
2. **The Outbox is its own screen**, and the precedent to copy is `BillInboxScreen` — the app's
   existing surface for items that exist but are **not in the books** and later become vouchers.
   Same shape: a list, a per-row status, an action that promotes it.
3. **The number column is headed `Provisional`, never `No.`** The format already helps:
   `INV/2025-26/PROV-node-a-3` contains the literal `PROV` and the node id, so a number read down
   a phone line is self-evidently not a document number. ⛔ **Never render a provisional in a
   field a final number also appears in** — that is the failure §3.2 Q1 names.
4. **On `COMMITTED`, show BOTH for the life of the session:** `PROV-node-a-3 → INV/2026-27/42`.
   The operator may already have quoted the provisional to a customer; the mapping is the only
   thing that lets them correct it. Then the row leaves the outbox.

### Q2 — What happens to a `CONFLICT`?

**Answer: a two-pane compare screen with exactly two choices — and it needs §0.2's two backend
additions first.**

- **Left pane, "what the server holds":** `GET /v1/companies/{id}/vouchers/{voucherID}` — already
  exists, and its postings carry **real ledger ids, not names** (`handleGetVoucher`'s comment),
  so the pane can re-seed a form without smuggling a name into a uuid field.
- **Right pane, "what you keyed":** the parked intent. **Needs a new route.** Recommend
  `GET /v1/companies/{id}/reconciliation-queue/{reconID}/payload` exposing `ConflictPayload`
  — a per-row drill-down, so the list handler stays cheap and unchanged.
- **The choice is binary, and this is the part not to get wrong:**
  1. **Discard mine** — keep the server's version; resolve the row.
  2. **Re-key mine as a NEW voucher** — a fresh `CREATE` intent, ⛔ **never a retried `ALTER`**.
     The base is stale *permanently*; the head has moved and will not move back.

  ⛔ **There is no third option and the screen must not imply one.** "Force my version" would
  mean an ALTER on a base the sequencer has already refused; offering it is how the server's
  version gets lost.
- **Resolving requires Gap 2's writer.** Add `POST .../reconciliation-queue/{reconID}/resolve`
  setting `resolved_at = now()`. Without it, both choices above are cosmetic.

### Q3 — What happens to a `FROZEN`?

**Answer: explained, not retried — and it has an asymmetry the CONFLICT case does not.**

- **No retry affordance of any kind.** A frozen voucher can *never* be superseded
  (`sync/types.go:103`). The only actions are **Open the frozen voucher** and **Copy my keyed
  version to a new voucher**.
- **The row must name the pin, not just the state.** `kind` is `IRN` / `FILED_GSTR1` and
  `ref_value` carries the reference. "This is frozen" without "because Q1's GSTR-1 is filed" is
  unactionable; the operator's next step is a credit note, and only the *reason* tells them that.
- ⚠️ **A FROZEN row's `payload` is NULL.** `frozenOutcome` (`service.go:273`) deliberately does
  **not** insert a row — the ledger already raised it, and only `parkConflict` ever writes the
  payload column. `ConflictPayload` returns `ok=false` for these.
  ⛔ **So for FROZEN the right pane cannot come from the API at all** — the operator's keyed
  version survives only in the **local outbox**. The compare screen must source "what you keyed"
  from IndexedDB for FROZEN and from the new route for CONFLICT, and must degrade honestly when
  the local copy is gone (a different browser): *"your keyed version is not on this machine."*

### Q4 — Where does the queue live?

**Answer: IndexedDB, scoped per (company, subject), cleared on sign-out, and the screen SAYS it
is unencrypted.**

- **`localStorage` is wrong** — synchronous, ~5MB, string-only. It is already used for exactly one
  thing (the bootstrapped company id, `CompanyContext.tsx:57`) and books do not belong beside it.
- **IndexedDB it is**, and note: there is **no precedent in this app** —
  `/usr/bin/grep -rn "indexedDB" frontend/src/` (non-test) → **EMPTY**. Track D introduces the
  first client-side datastore. There is also **no service worker anywhere** (`grep -rln
  "serviceWorker\|workbox" src/ public/` → EMPTY).
- ⚠️ **THE HONEST SECURITY ANSWER, WRITTEN DOWN AS §3.2 DEMANDS: it is unencrypted books on a
  shared machine.** Encrypting client-side with a key the client also holds is theatre. So:
  1. store it **unencrypted**;
  2. **say so on the screen** — the operator on a shared back-office PC is entitled to know;
  3. **key the store by company id AND user subject**, so a second operator on the same browser
     does not inherit a queue;
  4. **clear it on sign-out** — mirroring the existing `COMPANY_STORAGE_KEY` clearing rule, which
     exists for precisely this reason (`CompanyContext.tsx:60`: *"without the clear the NEXT user
     to sign in…"*).

---

## 2. ⛔ THE FIFTH OUTCOME §3.2 DOES NOT LIST — A WHOLE-BATCH 403

The four statuses are **per-intent**. Two failures are **per-REQUEST**, return no outcomes at all,
and the screen must render them completely differently:

| Failure | Why | What the screen must do |
|---|---|---|
| **`403` back-dated cutoff** | ⭐ **"one bad date refuses the LOT — a batch is only as permitted as its most-refused member"** (`auth_backdated_key_binding_test.go`, cutoff round 2). ONE pre-cutoff intent refuses **every** intent in the outbox | Name the offending date, keep the outbox **untouched**, and let the operator fix or drop that one row and re-commit |
| **`403 STEPUP_REQUIRED`** | any write at a level with `RequiresWebAuthn` and no WebAuthn AMR (`authz.go:450`) | Run the passkey ceremony, then re-commit. ⭐ Because the commit is ONE batched write, the whole outbox costs **exactly one step-up** — an argument for draining as a batch, never row-by-row |

⛔ **Neither is a per-intent status, so neither may be rendered in the status column.** Treating a
whole-batch refusal as "these 12 vouchers failed" is wrong in the dangerous direction: the outbox
is intact and re-committable, and a screen that says otherwise invites a re-key.

---

## 3. SCOPE — WHAT TO SHIP, AND SAYING SO HONESTLY

⭐ **Ship the queue + reconciliation UI. Do NOT ship a service worker / offline app shell.**

The handoff calls this "legitimate — and probably right", and §0.2's Gap 3 settles it: the
reconciliation screen fixes a **present-tense** defect (today's FROZEN rows are invisible to
everyone), and the outbox is useful the moment a commit fails or a connection drops mid-session —
neither of which needs an app shell.

So `FeaturesScreen`'s row (`FeaturesScreen.tsx:602`) must be **edited, not flipped**: keep the
sentence that there is no offline-capable client and nothing can be keyed on a train, and add what
now exists — a queue that survives a reload, a real commit, and a reconciliation screen for the
losers. ⛔ **The row flips to "shipped" only when a service worker lets the app boot with the
network off.** That is a separate track.

### The build order that follows from all of the above

1. **Backend, first** — the payload read route + the `resolve` writer (§0.2). Q2 is unbuildable
   without them, and they are small.
2. **Port `node.go` to TS** — outbox, HLC, UUIDv7, key formats. Proof: a vitest suite that
   reproduces `fuzz_test.go`'s scenarios against the ported client.
3. **The three screens** — Outbox (Q1), Reconciliation compare (Q2/Q3), both registered through
   `HANDOFF-THE-LAST-ONE.md` §0.1.1's eleven-file surface. ⛔ **Item 7 (`COMPANY_SCOPED_SCREENS`)
   is seen by no test unless you write the company-less one** — §0.1 item 2 says D is the track
   that adds it.
4. **The drive** — must produce a **real** `CONFLICT` by committing a stale `base_version`, never
   a stubbed one, on a throwaway company, torn down with the catalog-derived sweep (rule 7).

