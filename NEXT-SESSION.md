# NEXT SESSION — the last handover was VERIFIED; here is what it cost

> ## ⚠️ READ THIS FIRST
>
> The previous file asked you to refute eleven commits rather than build on them.
> **That was done, adversarially, on 2026-07-27.** Nine claims went to independent
> agents that read the real code, ran the real gates, and mutated the source to
> prove each test could fail. **Seven of the nine were wrong in some way that
> mattered**, and every one of those is fixed and pinned in the commit that
> carries this file.
>
> The lesson is not "the last session lied". Every claim's *behaviour* was
> essentially right. What was wrong, again and again, was the **evidence**: guards
> standing in for invariants nothing tested, assertions invariant under the very
> reversal they claimed to catch, an oracle edited to agree with the fixture it
> was supposed to check. **A green suite proves the tests pass. Mutation is the
> only thing that proves they test anything.**
>
> **Your first job is still not to build.** It is §2 — confirm the baseline —
> then pick up §4. But you are inheriting a much better-evidenced tree than the
> last session did.

---

## 1. Where things stand

Branch `master`, tree clean. `HANDOFF.md` remains the authoritative project
runbook — architecture, ADRs, the honest-gaps list (§6), the full worklist (§10).
This file is the verify-then-continue wrapper for one handover.

The §10 worklist A–J is **CLEAR and now independently verified**. What is left is
either blocked in this environment or forward roadmap (§4).

### What the verification pass found

| Claim | Verdict | The finding |
|---|---|---|
| **B** admin surface | ✅ + fixed | Vocabulary genuinely derived from the PDP's own vars — except `default_accesses`, a hardcoded literal the test also hardcoded. Widening `ValidDefaultAccess` left the suite green. Now derived from `authz.KnownDefaultAccesses` on both sides. |
| **D** edit log | ✅ | Tampering test detects all three damages AND leaves the DB intact — `voucher_version_no_mutate` / `posting_no_mutate` both `O` before and after. |
| **E** periods | ✅ + fixed | BS balances at 9 dates; P&L additive line-by-line. **But** flipping either bound `<=`→`<` passed the entire package: no fixture ever sat *on* a bound. `TestPeriodBoundsAreInclusiveOnBothEnds` added. |
| **F** Data Entry | ✅ + fixed | Grant is exactly `display company` + `display master`; 26 negative routes 403. **But** remapping `PUT .../opening` to `(Display, master)` passed the whole Go suite and returned **200** — a real escalation, pinned only by a live script. Go test added. |
| **G** supplier state | ⚠️ **over-corrected** | Removing the circular guess was right. Treating an **ambiguous** match as an **unknown state** was not — see below. |
| **C** invite outbox | ✅ + fixed | Atomic claim, real 5-min lease, same-transaction enqueue. **But** `settle` updated by id alone, so a dispatcher that stalled past its lease could push an already-`sent` row back to `pending` — a third send. Every settle now carries its lease token. |
| **H** single entry | ❌ **refuted** | Code correct, tests were not. Reversing Receipt so the bank is **credited** on money *in* passed **317/317**. Contra had no posting test at all. Assertions now name the ledger on each side. |
| **I** OCR seam | ✅ | paddle genuinely absent; the fake's `predict()`/`rec_texts` shape traces to real 3.x API errors recorded in the spike doc, not invented. Adapter test executes (0 skips). |
| **J** statutoryd role | ✅ | Stronger than claimed: of **31** public relations `tally_statutory` reaches exactly **one**, `statutory_step`. No superuser, no `bypassrls`, no CREATE on `public`. |

### G — the one to understand before you touch insightd

The previous session removed a circular fallback (`supplier_state = place_of_supply`,
i.e. assume intra to derive intra). Correct. It then treated *every* unmatched
supplier as having an unknown state. **Ambiguous is not unknown.** In the corpus
fixture `adv-ambiguous-supplier`, both candidate ledgers are state 27 and so is
the place of supply — the state was never in doubt, only *which account to
credit*. The fixture's own description asserted a premise false for its own data,
and the ORACLE had been edited to agree with it, making `gst_reconciliation == 1.0`
tautological for those fixtures.

Now: `SupplierMatch.candidate_ledger_ids` carries the shortlist; the mapper
derives the state from a **unanimous** candidate set (every candidate must be a
real ledger in that tenant and carry a state code, so a hallucinated id cannot
introduce one). `supplier` (who) and `supplier_state` (where) are separate
`unresolved` tokens — one token for two claims meant a split that HAD been
derived still read as unasserted. The oracle now reads state off the fixture's
own ledger rows.

**Two corrections to the previous session's narrative, both load-bearing:**
- It claimed the old code emitted no `"supplier"` unresolved tag. It did — via
  the header-confidence floor. That is why the tokens are now distinct.
- It claimed "there is no honest fallback available". There is at least one, now
  applied. A second (the plain-text `State: ... Code: NN` line real bills print,
  which is not PII and survives redaction) is still unbuilt: `InferredInvoice`
  has nowhere to put it.

---

## 2. CONFIRM THE BASELINE

### 2.1 Prerequisites

Postgres `tally_pg` on **:5433** · cored **:8087** · insightd **:8088** ·
statutoryd **:8091** · vite **:5173**.

```bash
# Rebuild cored from the CURRENT tree — a stale binary silently 404s new routes.
cd backend && go build -o /tmp/cored ./cmd/cored && go run ./cmd/cored migrate
AUTH_DEV_TOKEN_ENABLED=true PORT=8087 \
  APP_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" /tmp/cored serve &

# insightd MUST carry the DSN or verify:demo drops to 21/22 (see §5).
cd insightd && . .venv/bin/activate && \
  INSIGHTD_PII_BOUNDARY_STRICT=1 \
  INSIGHTD_DATABASE_URL="postgres://tally_app:tally_app@localhost:5433/tally" \
  python -m uvicorn insightd.api.app:app --port 8088 &
```

### 2.2 The hermetic matrix — expected numbers

| Gate | Expected |
|---|---|
| `cd backend && go build ./... && go vet ./... && go test ./... -count=1` | exit 0; **25 packages** (18 `ok` + 7 no-test) |
| `cd frontend && pnpm exec tsc --noEmit` | clean (covers `scripts/` too) |
| `cd frontend && pnpm exec vitest run` | **321 tests / 31 files** |
| `cd frontend && pnpm run build` | ok |
| `cd insightd && python -m pytest` | **106 passed** |
| `cd insightd && python -m insightd.benchmark.validate` | 28 fixtures valid |
| `cd insightd && python -m insightd.benchmark.report` | ALL FLOORS PASSED |
| `python3 corpora/validate.py` | tax=14 tds=13 tcs=5 **keystroke=26** isolation=11 migration=6 forecast=4 |

Migrations run to **0024**. (The previous file said 23 packages / 317 tests /
102 passed — those numbers were stale, not wrong-in-kind.)

### 2.3 The live sweep

```bash
cd frontend
CORED_BASE_URL=http://localhost:8087 pnpm verify:demo <companyA>          # 24/24
CORED_BASE_URL=http://localhost:8087 pnpm verify:tenancy <A> <B>          # 113/113
CORED_BASE_URL=http://localhost:8087 pnpm e2e:ui                          # 38/38 — RUN TWICE
CORED_BASE_URL=http://localhost:8087 pnpm e2e:edit
CORED_BASE_URL=http://localhost:8087 pnpm e2e:numbering
CORED_BASE_URL=http://localhost:8087 pnpm e2e:state-code
CORED_BASE_URL=http://localhost:8087 INSIGHTD_BASE_URL=http://localhost:8088 pnpm e2e:ocr
# statutoryd on its narrow role, including the durable-restart phase:
cd ../backend && go build -o /tmp/statutoryd ./cmd/statutoryd
SVC=$(curl -s -XPOST localhost:8087/auth/token -H 'Content-Type: application/json' \
      -d '{"subject":"svc:statutoryd"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
CORED_BASE_URL=http://localhost:8087 CORED_SERVICE_TOKEN=$SVC PORT=8091 \
  STATUTORYD_DSN="postgres://tally_statutory:tally_statutory@localhost:5433/tally?sslmode=disable" \
  /tmp/statutoryd &
CORED_BASE_URL=http://localhost:8087 STATUTORYD_BASE_URL=http://localhost:8091 \
  STATUTORYD_BIN=/tmp/statutoryd \
  STATUTORYD_DSN="postgres://tally_statutory:tally_statutory@localhost:5433/tally?sslmode=disable" \
  python3 backend/scripts/e2e-statutory-freeze.py
```

Seed first: `pnpm seed:demo -- --insightd`, then `pnpm seed:tenancy <A>`.
Browser sign-in: `browser-dev-owner` / `tally-demo-2026`.

> **RUN `e2e:ui` TWICE AND BELIEVE ONLY THE SECOND NUMBER**, and **run it with
> NOTHING ELSE TOUCHING THE DB.** This cost real time on 2026-07-27: run 2 came
> back 33/38 and looked like a dirty-database regression. It was not. A
> verification agent was editing Data Entry ACL rules on the live DB at the same
> moment. Re-run alone: 38/38, twice. **A live gate run concurrently with
> anything else is not evidence** — of a pass or a failure.

---

## 3. HOW TO WORK

Fan verification out to subagents and keep only their conclusions; do not read
large files into the orchestrator. One agent per claim, each returning a
structured verdict, is a handful of tool calls instead of thousands of lines.

**Rules that earned their place** (`HANDOFF.md` §3, plus this pass):
- **You are the integrator.** Re-run every gate yourself before committing.
  Never trust a subagent's "green".
- **Give parallel agents DISJOINT file sets**, and worktree isolation when they
  mutate. Two agents mutating one Go package invalidate each other's test runs.
- **Never let an agent touch shared live state** — a pre-existing company's ACL
  rules, security levels, or members. Make it create its own throwaway tenant.
  This is what corrupted `e2e:ui` above.
- **Never run `go test ./internal/server` unscoped while others work**: it
  contains a test that disables DB triggers globally. Scope with `-run`.
- **A mutation that does NOT bite is a finding**, not a formality. It was the
  single most productive technique in this pass — it caught the Receipt
  orientation, the opening-balance escalation, the report boundaries and the
  `default_accesses` drift, all of which had green tests over them.
- **Agents die mid-run.** One stalled with its report lost; its worktree was
  gone. Verify the TREE, not the summary, and re-run anything that did not
  return.
- **Commit only verified-green work, directly to `master`**, stating the gates
  re-run and every deferral. End with:
  `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`

---

## 4. WHAT IS LEFT

### Blocked in this environment (do not attempt)
- **Tauri 2 desktop shell** — `cargo`/`rustc` absent (confirmed).
- **e-way bill / full GSTR filing with ARN** — needs real GSP/NIC access.
- **Real-OCR accuracy on genuine photographed bills** — every image ever tested
  against this pipeline was synthetic. Needs real bills.

### Open follow-ups (all disclosed in `HANDOFF.md` §6)
Carried forward, still true:
- After Alt+C resolves an OCR draft's party, the tax split is **not re-derived**
  from the now-known supplier state.
- insightd's background OCR runner is in-process `BackgroundTasks`: it does not
  survive a restart, and a draft left `processing` by a crash **stays there
  forever** — there is no reaper, timeout or requeue anywhere in `insightd/`.
- Invite delivery has **no retry backoff**, and the default Sender only LOGS —
  `notification_status = 'sent'` means "written to cored's stdout", not
  "delivered". The admin UI should say so while `LogSender` is the only transport.
- No **first-password bootstrap** for an invited user, and no password reset —
  the biggest remaining hole in the SaaS story, and it pairs naturally with the
  invite outbox.
- Tally's Trial Balance shows opening/transactions/closing columns for a period;
  this shows the closing position as at a date.

New, raised by the verification pass:
- **`InferredInvoice` has nowhere to put the supplier's state or the invoice's
  own CGST/SGST-vs-IGST label**, both of which real bills print in plain text and
  both of which survive redaction. That, not the PII boundary, is what blocks the
  remaining honest fallback for G. `inferencer_opus.py` also explicitly forbids
  emitting the tax type.
- **Grant changes live only in one-shot migrations.** `migrations.Apply` skips an
  applied version, so widening 0022 on an existing database is a no-op and would
  be invisible to every DB-backed test until someone provisions a fresh DB.
- **Data Entry holds a bare `allow display report`** (from 0011, not 0022), which
  matches every `report:NAME` — balance sheet, P&L, both GST returns. Far wider
  than the two reads 0022 added. If "an operator sees only what they type" is the
  intent, scope it. Product decision, not a bug.
- **`status='sending' AND claimed_at IS NULL` is unclaimable forever** in the
  outbox (`NULL < now()-interval` is NULL). Only reachable by an external write
  today; a `CHECK (status <> 'sending' OR claimed_at IS NOT NULL)` would make the
  lease invariant structural.
- **`adv-non-purchase-doc` still carries a `derived_gst` block** for a delivery
  challan that must never post. It is retained only so the oracle has something
  to check; no `derived_gst` at all would be more honest.
- **`_is_inter` in `insightd/benchmark/validate.py` is dead code** — no callers.
- **The PaddleOCR fake is pinned to PROSE**, not bytes: its shape is transcribed
  from `docs/spikes/ws10-paddleocr-spike.md`, so a transcription error would
  propagate into both the fake and the adapter. Capture a serialised real 3.x
  `predict()` response as a golden fixture when paddle is next available.

### The forward roadmap
`HANDOFF.md` §11 and `PLAN.md` Part C: Phase-3 depth (payroll 24Q/PF/ESI,
inventory batches/expiry, GSTR-2A/2B + IMS reconciliation, more query entities,
ClickHouse/Kafka at the CDC seam) then Phase 4 (FlowDef DSL + sandboxed
extensions, the `/tally` connector ingress, webhooks, group consolidation).

---

## 5. ENVIRONMENT GOTCHAS

- **OrbStack's Docker daemon can hang**, taking `tally_pg` with it. Fixed by
  quitting and relaunching OrbStack, then `docker start tally_pg`. **Ask the user
  before doing this** — it drops their other containers.
- **`psql` can time out while the DB is fine** (and vice versa). Probe with a
  real `select 1`, and give OrbStack a minute after a restart.
- **insightd without `INSIGHTD_DATABASE_URL`** silently falls back to an
  in-memory draft store and `verify:demo` drops to 21/22.
- **Restart insightd after changing its code** — a stale :8088 makes `e2e:ocr`
  green against code you did not write.
- **`frontend/scripts/` is inside `tsconfig`'s `include`.** Keep it clean.
- **pillow and numpy are installed in `insightd/.venv`** so the OCR adapter test
  executes rather than skipping. Both are declared in `requirements-ocr.txt`;
  `requirements.txt` is untouched and paddle is still absent.
- **The `tally_app` role is RLS-restricted**, so ad-hoc `psql` queries as
  `tally_app` return 0 rows and look like missing data. Use the superuser
  connection for inspection.
- **The invite POST requires a `webauthn` amr** — a plain dev token gets
  `403 STEPUP_REQUIRED`. An invite is only claimed when the invitee calls
  `GET /v1/me`; before that it grants nothing.
- The DB holds tens of thousands of throwaway test companies. Harmless.
- Key delivery is on the **document**, and e2e keys go through
  **`page.keyboard`, never `locator.press`** — two shipped bugs hid behind that.
