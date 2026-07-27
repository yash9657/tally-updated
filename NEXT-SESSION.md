# NEXT SESSION — verify first, then continue

> ## ⚠️ READ THIS BEFORE YOU TOUCH ANYTHING
>
> **The previous session ran to ~82% of its context window and you should not
> trust its self-report — including this file's.** Long-context sessions drift:
> they misremember what they changed, mark things done that were only started,
> and describe tests that pass for the wrong reason. Eleven commits landed on
> `master` in one sitting.
>
> **Your first job is NOT to build. It is to CONFIRM.** Run §2's gate matrix
> yourself, then spot-check §3's per-item claims against the real code. Treat
> every "DONE" below as a claim to be refuted, not a fact to build on. If a claim
> does not survive, fix it and say so plainly — a corrected claim is worth far
> more than an inherited one.
>
> **One thing was genuinely NOT finished: the final consolidated live sweep.**
> See §2.3. Do that first.
>
> **Use dynamic workflows to keep YOUR context small** — §5. Fan verification out
> to subagents and keep only their conclusions. Do not read large files into the
> orchestrator when a subagent can read them and report back.

---

## 1. Where things stand

Branch `master`, tree clean, HEAD **`f95ef2c`**. The session before this one
ended at `cadf3af`, so everything in between is what needs confirming.

`HANDOFF.md` remains the authoritative project runbook — architecture, the ADRs,
the honest-gaps list (§6), the full worklist (§10). **This file is only the
verify-then-continue wrapper for one handover.** Read `HANDOFF.md` §3 (the
working method) and `memory/verification-lessons-item-a.md` before you start;
they are short and they are why this project's gates are worth anything.

**The §10 worklist is reported CLEAR.** Items A–I are claimed done, plus the two
non-blocked halves of J. What is left is either genuinely blocked in this
environment or belongs to the forward roadmap (§4).

### The eleven commits to confirm

| Commit | Claim |
|---|---|
| `1da7a6c` | **B** — admin surface finished in the browser; new `GET .../security-levels/vocabulary` serves the closed ACL vocabulary |
| `85546a0` | **D** — Edit Log viewer: `GET .../vouchers/{id}/history` + chain verification + `Alt+L` |
| `e5483cc` | **E** — period support on Balance Sheet / P&L / Trial Balance |
| `79e919b` | **F** — Data Entry may read company+masters (migration 0022); party picker reads the real chart of accounts |
| `959c021` | docs — HANDOFF updated for B/D/E/F |
| `58633c5` | **G** — insightd supplier-state guess removed; forecast over-count found already fixed |
| `60f3b9b` | **C** — invite delivery via a transactional outbox (migration 0023, `internal/notify`) |
| `e7b8095` | **H** — single-entry mode + a working Ctrl+H Change Mode |
| `fbbeb9c` | **I.1** — PaddleOCR 3.x adapter + structural review signals |
| `06d1572` | **I.2** — enqueue-and-poll + Bill Inbox file upload |
| `f95ef2c` | **J** — narrow DB role for statutoryd (migration 0024) |

---

## 2. CONFIRM THE BASELINE (do this before anything else)

### 2.1 Prerequisites

Postgres `tally_pg` on **:5433** · cored **:8087** · insightd **:8088** ·
statutoryd **:8091** · vite **:5173**.

```bash
# Rebuild cored from the CURRENT tree — a stale binary silently 404s new routes.
cd backend && go build -o /tmp/cored ./cmd/cored && go run ./cmd/cored migrate
AUTH_DEV_TOKEN_ENABLED=true PORT=8087 \
  APP_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" /tmp/cored serve &

# insightd MUST carry the DSN or verify:demo drops to 21/22 (see §6).
cd insightd && . .venv/bin/activate && \
  INSIGHTD_PII_BOUNDARY_STRICT=1 \
  INSIGHTD_DATABASE_URL="postgres://tally_app:tally_app@localhost:5433/tally" \
  python -m uvicorn insightd.api.app:app --port 8088 &
```

### 2.2 The hermetic matrix — expected numbers

| Gate | Expected |
|---|---|
| `cd backend && go build ./... && go vet ./... && go test ./... -count=1` | exit 0; **23 packages** (18 `ok` + 5 no-test) |
| `cd frontend && pnpm exec tsc --noEmit` | clean (now also covers `scripts/`) |
| `cd frontend && pnpm exec vitest run` | **317 tests / 31 files** |
| `cd frontend && pnpm run build` | ok |
| `cd insightd && python -m pytest` | **102 passed** |
| `cd insightd && python -m insightd.benchmark.validate` | 28 fixtures valid |
| `cd insightd && python -m insightd.benchmark.report` | ALL FLOORS PASSED |
| `python3 corpora/validate.py` | tax=14 tds=13 tcs=5 **keystroke=26** isolation=11 migration=6 forecast=4 |

Migrations now run to **0024**.

### 2.3 ⚠️ THE LIVE SWEEP THAT WAS NOT FINISHED

Every one of these passed **immediately before** `f95ef2c` was committed, and the
hermetic matrix above passed after it — but the final consolidated live re-run
was **interrupted mid-command and never completed**. Run it yourself and trust
only what you observe:

```bash
cd frontend
CORED_BASE_URL=http://localhost:8087 pnpm verify:demo <companyA>          # expect 24/24
CORED_BASE_URL=http://localhost:8087 pnpm verify:tenancy <A> <B>          # expect 113/113
CORED_BASE_URL=http://localhost:8087 pnpm e2e:ui                          # expect 38/38 — RUN TWICE
CORED_BASE_URL=http://localhost:8087 pnpm e2e:edit
CORED_BASE_URL=http://localhost:8087 pnpm e2e:numbering
CORED_BASE_URL=http://localhost:8087 pnpm e2e:state-code
CORED_BASE_URL=http://localhost:8087 INSIGHTD_BASE_URL=http://localhost:8088 pnpm e2e:ocr
# statutoryd on its NEW narrow role, including the durable-restart phase:
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

Seed first if you need fresh companies:
`pnpm seed:demo -- --insightd` then `pnpm seed:tenancy <A>`.
Sign-in for the browser: `browser-dev-owner` / `tally-demo-2026`.

> **RUN `e2e:ui` TWICE AND BELIEVE ONLY THE SECOND NUMBER.** A live gate that
> passes only on a virgin database is not a gate — that is how the multi-device
> passkey bug was found. And **never run two `e2e:ui` processes at once**: they
> both submit the deliberate wrong password and trip the shared account's
> lockout. (That happened this session.)

---

## 3. SPOT-CHECK THE CLAIMS (the part that matters)

A green suite proves the tests pass, not that they test anything. For each item,
the fastest way to catch a lie is listed. **Prefer mutation to reading**: break
the thing, confirm a named test fails, restore byte-identical (`shasum` before
and after).

- **B — admin surface.** `GET /v1/companies/{id}/security-levels/vocabulary` as
  an Owner should 200 with `report:trial-balance` present and
  `report:trail-balance` absent. Then: does Users & Roles actually change a
  level, revoke, and delete? Mutation: serve one fewer report name and confirm
  `TestAdminVocabularyIsExactlyWhatTheServerAccepts` names it.
- **D — Edit Log.** `GET .../vouchers/{id}/history` must return
  `verification.ok: true`. The real gate is
  `TestVoucherHistoryDetectsTampering` — it disables the append-only triggers as
  superuser, damages a voucher three ways and repairs it. **Confirm it leaves
  the DB intact**: `SELECT tgenabled FROM pg_trigger WHERE tgname IN
  ('voucher_version_no_mutate','posting_no_mutate')` must both be `O`.
- **E — periods.** Semantics differ per statement and that is the risk: TB/BS
  are positions (`as_on` only), P&L is a period (both bounds, opening excluded).
  Check a Balance Sheet still BALANCES at several dates, and that the P&L is
  additive across two disjoint halves. `verify:demo` covers both.
- **F — Data Entry + party picker.** Migration 0022 granted `display company` +
  `display master`. Confirm the grant is still NARROW: a Data Entry token must
  still get 403 on `PUT .../ledgers/{id}/opening` and on `.../members`.
- **G — insightd supplier state.** An unmatched supplier must NOT produce a
  CGST+SGST split. Three corpus fixtures AND the corpus oracle were corrected
  because both encoded the old wrong answer — **re-derive for yourself that the
  new expectation is right**, do not take it on trust.
- **C — invite delivery.** Invite someone, then poll `GET .../members` until
  `notification_status` reaches `sent`. The subtle claim is no double-send: the
  claim moves the row to a leased `sending` state. Mutation: drop `status =
  'sending'` from the claim UPDATE and confirm
  `TestConcurrentDispatchersSendEachMessageOnce` fails.
- **H — single entry.** F5 must open in single entry and post a BALANCED,
  correctly-ORIENTED voucher (payment CREDITS the bank). Mutation: make the
  many-side equal the primary side; both orientation tests must fail.
- **I — OCR.** paddle is **not installed**; the adapter is pinned by a fake
  `paddleocr` injected into `sys.modules`. **Real inference was never re-run
  this session** — only the spike (`960539b`) ever measured it, on synthetic
  images. Do not repeat any accuracy or latency claim as if it were tested here.
- **J — statutoryd role.** `has_table_privilege('tally_statutory','company','SELECT')`
  must be **false** and `...('statutory_step','INSERT')` **true**.

---

## 4. WHAT IS LEFT

### Blocked in this environment (do not attempt)
- **Tauri 2 desktop shell** — `cargo`/`rustc` are absent (confirmed).
- **e-way bill / full GSTR filing with ARN** — needs real GSP/NIC access.
- **Real-OCR accuracy on genuine photographed bills** — every image ever tested
  against this pipeline was synthetic. Needs real bills.

### Open follow-ups left by this session (all disclosed in `HANDOFF.md` §6)
- After Alt+C resolves an OCR draft's party, the tax **split is not re-derived**
  from the now-known supplier state; the draft keeps the no-conclusion IGST.
- insightd's background OCR runner is in-process `BackgroundTasks` — it does
  **not** survive a restart, unlike cored's outbox. A draft left `processing` by
  a crash stays there.
- Invite delivery has **no retry backoff** and the default Sender only LOGS.
- No **first-password bootstrap** for an invited user, and no password reset —
  the biggest remaining hole in the SaaS story, and it pairs naturally with the
  invite outbox that now exists.
- Entry mode lives on the frame, so it does not persist per voucher type as
  Tally's does.
- Tally's Trial Balance shows opening/transactions/closing columns for a period;
  this shows the closing position as at a date.

### The forward roadmap
`HANDOFF.md` §11 and `PLAN.md` Part C: Phase-3 depth (payroll 24Q/PF/ESI,
inventory batches/expiry, GSTR-2A/2B + IMS reconciliation, more query entities,
ClickHouse/Kafka at the CDC seam) then Phase 4 (FlowDef DSL + sandboxed
extensions, the `/tally` connector ingress, webhooks, group consolidation).

---

## 5. HOW TO WORK — keep the orchestrator's context small

The previous session did everything inline and burned 82% of its window. **Do
not repeat that.** Use dynamic workflows so the reading happens in subagents and
only conclusions reach you.

**Verification is embarrassingly parallel — fan it out first.** One agent per
claim in §3, each returning a structured verdict, is a handful of tool calls in
your context instead of thousands of lines of file content.

```js
export const meta = {
  name: 'confirm-then-continue',
  description: 'Independently confirm the previous session, then build what survives',
  phases: [{ title: 'Confirm' }, { title: 'Build' }, { title: 'Review' }],
}
const REPO = '/Users/yashbhalgat/code/tally-updated'
const VERDICT = {
  type: 'object',
  properties: {
    claim: { type: 'string' },
    verdict: { type: 'string', enum: ['CONFIRMED', 'REFUTED', 'UNVERIFIABLE'] },
    evidence: { type: 'string' },      // the COMMAND run and its OBSERVED output
    mustFix: { type: 'array', items: { type: 'string' } },
  },
  required: ['claim', 'verdict', 'evidence'],
}

phase('Confirm')
const CLAIMS = [
  'B: served ACL vocabulary cannot drift from the PDP; admin writes work in the browser',
  'D: the edit-log chain verifier detects tampering AND restores the DB (triggers back to O)',
  'E: BS/TB are positions, P&L is a period; a BS balances at every date',
  'F: Data Entry gained display company+master and NOTHING wider',
  'G: an unmatched OCR supplier no longer yields a CGST+SGST split',
  'C: concurrent dispatchers never hand one message to the sender twice',
  'H: single entry posts a balanced, correctly-oriented voucher',
  'I: the paddle 3.x seam is pinned; NO real inference was run',
  'J: tally_statutory reaches statutory_step and nothing else',
]
const verdicts = await parallel(CLAIMS.map((c) => () => agent(
  `Repo ${REPO}. ADVERSARIALLY verify this claim from the last session — try to REFUTE it.
   Read the real code, RUN the gate, and where you can, MUTATE the code, confirm a named
   test fails, then restore it byte-identical (shasum before/after). Report ONLY observed
   results; "the test passed" is not evidence unless you also show it can fail.
   CLAIM: ${c}`,
  { label: `confirm:${c.slice(0, 12)}`, phase: 'Confirm', schema: VERDICT })))

const refuted = verdicts.filter(Boolean).filter((v) => v.verdict !== 'CONFIRMED')
if (refuted.length) return { refuted }   // fix these BEFORE building anything new

phase('Build')
// ...only now, and on DISJOINT file sets so parallel agents cannot collide.
```

**Rules that earned their place** (`HANDOFF.md` §3, and this session):
- **You are the integrator.** Re-run every gate yourself before committing.
  Never trust a subagent's "green".
- **Verify a workflow's TREE, not its summary.** Agents have died mid-run with
  their files written and their report lost.
- **Never `git stash` at the repo root while parallel agents are writing.**
- **Disjoint file sets** make parallelism safe without worktrees.
- **A mutation that does NOT bite is a finding**, not a formality — twice this
  session it revealed a guard standing in for an invariant nothing tested.
- **The live browser e2e catches what hermetic tests cannot.** It fell 35/35 →
  25/35 on a change every unit test approved, because a brand-new company had an
  empty party picker and its first voucher was unkeyable.
- **Commit only verified-green work, directly to `master`**, with a message
  stating the gates you re-ran and every deferral. End with:
  `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`

---

## 6. ENVIRONMENT GOTCHAS (each cost real time this session)

- **OrbStack's Docker daemon hung**, taking `tally_pg` with it; `docker ps`
  stopped responding entirely. Fixed by quitting and relaunching OrbStack, then
  `docker start tally_pg`. **The user approved that restart** — ask before doing
  it again, it drops their other containers.
- **`psql` can time out while the DB is fine** (and vice versa). Probe with a
  real `select 1`, and give OrbStack a minute after a restart.
- **insightd without `INSIGHTD_DATABASE_URL`** silently falls back to an
  in-memory draft store and `verify:demo` drops to 21/22. Always start it with
  the DSN.
- **Restart insightd after changing its code** — a stale :8088 makes `e2e:ocr`
  green against code you did not write.
- **`frontend/scripts/` is now inside `tsconfig`'s `include`.** Those `.mts`
  verifiers used to be type-checked by nothing; a redeclared local reached a
  live run as an esbuild error. They check clean — keep them that way.
- **pillow and numpy were installed into `insightd/.venv`** so the OCR adapter
  test executes rather than skipping. Both are declared in
  `requirements-ocr.txt`; `requirements.txt` is untouched and paddle is still
  absent, so the core suite's dep-free property holds.
- The DB holds **~23 000 throwaway test companies**. Harmless.
- Key delivery is on the **document**, and e2e keys go through
  **`page.keyboard`, never `locator.press`** — two shipped bugs hid behind that.
  (Typing into a text field with `fill` is fine and the file does it.)
