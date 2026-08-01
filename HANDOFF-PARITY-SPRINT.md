# HANDOFF — THE PARITY SPRINT

> # ⛔ SUPERSEDED — START AT `HANDOFF-FEATURE-LOCK.md`
>
> That file is the plan of record: the goal, the complete gap inventory (including the ~16
> tracks nobody had ever enumerated), the sequence, and the definition of done that this
> file's process was not enforcing — **a feature is done when a USER CAN REACH IT**, not when
> the engine is correct.
>
> Keep reading here only for the *reasoning* behind specific decisions (§2.1 machine limits,
> §2.4 the twelve registration points, §5 the workflow scripts). Its Wave-1 status table is
> accurate as of `09b37c7`.

> **~~This file is the entry point.~~** It supersedes `HANDOFF-CLOSING-THE-GAP.md`, whose §3.1 is
> now done. That file remains the authority on the *gap inventory* (its §3.4–3.9) and on the
> lessons in its §6 — read those; they are not repeated here in full.
> `HANDOFF-T1-PRINTING.md` §3.2 and §5 remain the authority on the memo shape and the gate
> procedures. `PLAN-TALLY-PARITY.md` §2 is the authority on machine constraints and §2.5 is
> the **only** authority on migration numbers.
>
> **HEAD `8bfa23c` · migrations applied to `0050` · working tree clean · all gates green.**
> **Backend 1885 pass / 0 SKIP / 0 FAIL · frontend 434 · `make verify` green.**
>
> **GOAL: close the Tally feature gap as fast as correctness allows.** This file is organised
> around that: §2 is how to go fast, §4 is the wave plan, §5 is the workflow scripts.
>
> ### 🆕 WAVE 1 PROGRESS — §3.1 and §3.4 are DONE; §3.2 and §3.3 are NOT
>
> | Item | State |
> |---|---|
> | §3.4 BRS print | ✅ **shipped** (`ba00847`). `?ledger_id=` / `?all=true`, 400 with neither. |
> | §3.1 gap 1 — contractor aggregate | ✅ engine + migration 0050 (`8bfa23c`) |
> | §3.1 gap 2 — directors' fees | ✅ shipped (`eed561e`) |
> | §3.1 gap 3 — TCS no-PAN 20% cap | ✅ shipped (`47e5afb`) |
> | §3.1 gap 4 — s.394(2) declaration | ✅ engine + migration 0049 (`48b0633`) |
> | §3.1 gap 5 — duty to deduct at all | ✅ engine + migration 0049 (`088157a`) |
> | §3.2 T6 — register and Form 140 return | ❌ **not started.** Persistence exists; nothing reads it. |
> | §3.3 T4 slice 2 | ❌ **not started.** Memo written: `docs/design/t4-slice2-model.md`. |
>
> ### ⛔ AND ONE THING THAT WAS NOT ON ANY LIST
>
> **`206C(1)-SCRAP` shipped at 1% and should have been 2% since 1 April 2026** — s.394(1)
> [Table: Sl. No. 4], footnote 88, *"Substituted for '1%' by the Finance Act, 2026, w.e.f.
> 1-4-2026"*. The engine under-collected by half on every scrap sale. Fixed in `47e5afb`.
>
> `t6-model.md` §9 had marked that row correct and concluded **"eight of eight shipped rules
> are right"** — it had checked against the Act **as enacted**, and the amended edition was
> already in `docs/statutory/`, named by that directory's own README as *"the authority to
> verify §9 against"*. **The re-read had not been done.** It was seven of eight.
>
> > **The transferable lesson, now recorded in three places:** "verified against the Act" is
> > not a durable claim. An Act is amended annually, so a verification is only as current as
> > the *edition* it read. **Record the edition, and re-read on every Finance Act.**
>
> ### ⚠️ THE ENGINES ARE NOT WIRED INTO THE POST PATH
>
> Read this before believing the ✅s above. Gaps 1, 4 and 5 are **pure functions plus storage,
> both proven, and nothing calls them yet**: `ledger.Post` still calls `TDSDerive`, not
> `TDSDeriveCumulative`; it does not consult `MustDeduct`; and no code writes a `tds_deduction`
> row. That is the same shape as the deferral `internal/ledger/tds.go` already discloses, and
> **it must not be reported as a closed gap in production terms.** Wiring it is the first task
> of the next session and is the precondition for T6's register.

---

## 0. THE FIVE RULES. Everything below assumes them.

The first three are inherited and non-negotiable. Rules 0.4 and 0.5 are new and exist because
**this sprint runs work in parallel**, which breaks things the serial process never could.

**0.1 — DATA MODEL FIRST. A memo before any code, per track.** `HANDOFF-T1-PRINTING.md` §3.2
is the required five-section shape. **This is a SPEED rule, not a quality rule.** T1 took
three waves because the engine was right and the schema was missing; T4's memo found a
plan-breaking flaw before a line was written, T6's found three, and T1+T4's found two. Rework
is the single largest time cost in this repo's history. A memo costs one agent-hour and has
never once failed to pay for itself.

**0.2 — NEVER INVENT A RATE, A THRESHOLD, A CLAUSE LETTER, A CODE OR A KEYSTROKE.** Refuse
instead, with a citation. This now has a worked precedent in **both** directions:
- The seven Form 140 payment codes were refused for weeks as "untrusted secondary" and the
  primary spec then **confirmed all seven**. That does not mean the refusal was wasted — it
  means the process worked. The same source got *thresholds* wrong.
- Two keystrokes were asserted from memory in a design memo and **both were wrong**
  (`RESEARCH.md:2165`, `:873`, `:879`). Keystroke fidelity is the moat. `RESEARCH.md` has the
  answers and is citable by line number — cite it.

**0.3 — REPORTED-COMPLETE MEANS NOTHING. Run the gates yourself.** Full procedure in
`HANDOFF-T1-PRINTING.md` §5 and `PLAN-TALLY-PARITY.md` §2.2. The short form:
- `go test ./... -count=1 -v | grep -c -- '--- SKIP'` must be **0**. Without `-v` no SKIP
  lines print and the grep is vacuous.
- **Bracket long runs with a live `psql` query before and after.** DB down ⇒ every DB test
  skips ⇒ `go test` still exits 0. `nc -z` is not a liveness check.
- **Age every running server against the commit.** Last two sessions found all three stale
  and all three answering `200` on `/healthz`.
- Every new invariant needs a **mutation proof**: break it, watch the *right* test fail,
  restore byte-identically (`shasum -a 256 -c`), say you did.
- Every differential test needs a **control**.

**0.4 — 🆕 PARALLEL WORK IS PARTITIONED BY FILE OWNERSHIP, NOT BY TOPIC.** Two agents editing
one file is how a fix gets clobbered. §2.3 lists the files that are shared by *every* track and
must therefore be owned by exactly one agent per wave. Topic-partitioning feels natural and is
wrong: "you do TDS, you do orders" still has both agents editing `server.go`.

**0.5 — 🆕 A WAVE IS NOT DONE UNTIL THE REGISTRATION AUDIT PASSES.** A new resource has **4**
server registration points and a new screen has **8** client ones (§2.4). Missing one produces
a route that default-denies for every level including an Owner, or a screen whose keys open the
wrong voucher. These failures are silent and they are *guaranteed* to happen under fan-out
because no single agent sees the whole surface. §5.3 is a workflow that checks all twelve.

---

## 1. EXACT STATE

| | |
|---|---|
| HEAD | `8bfa23c` |
| Migrations applied | `0050` (next free blocks: §2.5 of `PLAN-TALLY-PARITY.md`) |
| Backend tests | **1885 pass / 0 SKIP / 0 FAIL** under `-v`, psql-bracketed |
| Frontend tests | **434** |
| Corpora | 11 independent corpora; `payroll=19`, `keystroke=32` |
| `e2e:ui` | **45 flows**, green twice consecutively |
| Live gates | `verify:demo` 31/31 · `verify:tenancy` 113/113 · `e2e:edit`/`numbering`/`state-code`/`ocr` OK · statutoryd freeze gate incl. durable-restart |
| `FeaturesScreen` | 45 rows: **25 on**, 2 always, 3 server-only, 15 off |

### 1.1 What landed in the last session

- **§3.1 is closed** — printing (Ctrl+P / Alt+P) and bank reconciliation are reachable from
  the keyboard. Memo: `docs/design/t1t4-ui-model.md`. No migration was needed.
- **`ledger.is_bank`** derived server-side from `reporting.BankLedgerPredicate` — bank-ness is
  **ltree ancestry**, not the immediate group name.
- **The EDLI premium is booked** — `S.O. 3581(E)`, 0.50% w.e.f. 29 June 2026, `ConfMedium`,
  ≤₹75/member/month. That was a release blocker.
- **Four primary statutory sources are now in `docs/statutory/`** and every claim in that
  directory's README was read out of them, not summarised. **Read it before any T6 work.**

### 1.2 The three unblocked-by-evidence facts that shape Wave 1

All from primary text, all in `docs/statutory/README.md`:

1. **Contractor dual threshold is real**: `₹30,000 per payment` **and** `₹1,00,000 aggregate`
   (amended Act s.393 Table Sl. No. 6, conjunction verbatim). We model only the first.
2. **Directors' fees: 10% at a NIL threshold**, return code **`1028`** — which *shares its
   statutory limb* with professional services (`1027`). An engine deriving the code from the
   section alone files directors' fees as professional fees.
3. **Annexure 6 is thirteen reason flags** (`A B C D E N O P Q S T Y Z`) and is a **flag ×
   section-code validity matrix**, not a lookup. Flag **`Y`** = below threshold, which couples
   fact 1 to the *return*: a sub-₹30,000 payment that crosses the ₹1,00,000 aggregate stops
   being a `Y`.

---

## 2. HOW TO GO FAST WITHOUT BREAKING IT

### 2.1 ⚠️ WHERE THE TIME ACTUALLY GOES — read this before designing any workflow

The instinct is to spawn agents. **On this machine that is not where the speed is**, and
believing otherwise will make the sprint slower, not faster.

`PLAN-TALLY-PARITY.md` §2.1 measures the box: **8 cores / 16 GB**, already running several
Claude worktrees plus local CI. Consequences that are not negotiable:

- **A workflow caps at `min(16, cores-2)` ≈ 6 concurrent agents.** Passing 40 items to
  `parallel()` does not run 40 agents; it queues them.
- **RUN ONE WORKFLOW AT A TIME.** Three concurrent workflows means up to 18 agents each
  spawning a Go compiler. That, not the work, turns a track into an afternoon.
- **Heavy commands are auto-queued through `ci-slot` (2 slots) by a PreToolUse hook.** Do not
  wrap anything yourself. **A queued test looks exactly like a hung test** — last session
  three runs appeared to hang for 10+ minutes and were simply behind each other in the queue,
  with two dead runs orphaning both slots. If a test seems hung, check `ci-slot` before
  diagnosing the machine. (`internal/payroll/payrolldb` genuinely takes ~360s.)

**So the real speed levers, in order of measured impact:**

| # | Lever | Why it dominates |
|---|---|---|
| 1 | **Rule 0.1 — memo first** | Rework is the biggest historical cost. T1 = 3 waves. Four memos have now caught six plan-breaking flaws pre-code. |
| 2 | **Parallelise the READ-ONLY phases** | Memos, reviews, audits and research have no write conflicts, so they fan out perfectly. This is where `parallel()` earns its keep. |
| 3 | **Disjoint file ownership on writes** (§2.3) | Conflict-free fan-out on *code* requires it. Otherwise you serialise anyway, badly. |
| 4 | **Worktree isolation** for tracks that must touch overlapping files | `isolation: 'worktree'` — costs ~200–500 ms + disk per agent, worth it only for genuine write conflicts. |
| 5 | Fan-out width | **Last.** It is capped at 6 and it is not the bottleneck. |

### 2.2 What parallelises cleanly vs what must not

| Phase | Parallel? | Notes |
|---|---|---|
| **Research / read primary sources** | ✅ perfectly | Read-only. Fan out one agent per source. |
| **Design memos** (one per track) | ✅ perfectly | Each writes its own `docs/design/<track>-model.md`. No overlap. |
| **Adversarial memo review** | ✅ perfectly | Read-only. Use `pipeline()` so each memo is reviewed the moment it exists. |
| **Migrations** | ⚠️ **only with §2.5 blocks** | Pre-allocated blocks make this safe. Without them it is the classic collision. |
| **Engine/domain code** in `internal/<track>/` | ✅ if one agent per package | Disjoint packages, disjoint files. |
| **Corpus fixtures** | ⚠️ ADD only | `corpora/validate.py` is CO-OWNED — see §2.3. |
| **The shared wiring files** | ❌ **never** | §2.3. One owner per wave, serialised at the end. |
| **Live gates** (`e2e:ui`, `verify:demo`, `verify:tenancy`) | ❌ **run ALONE** | They drive one server and one database. |
| **`FeaturesScreen.tsx`** | ❌ one owner | Every track touches it. Serialise. |

### 2.3 🔒 THE SERIALISATION LIST — one owner per wave, no exceptions

Every track wants to edit these. Assign **one** agent (the "wire owner") to apply *all* tracks'
changes to them at the end of a wave, after the per-track work has landed.

**Server:**
- `backend/internal/server/server.go` — route mounting
- `backend/internal/server/auth_middleware.go` — `routeAction()`, `reportName()`
- `backend/internal/authz/vocabulary.go` — `KnownResources`, `KnownReportNames`, `KnownVoucherClasses`
- `backend/migrations/*.sql` — **use your §2.5 block**; never pick a number

**Client:**
- `frontend/src/keystroke/types.ts` — `ScreenKind`, `KeymapAction`, `VoucherType`
- `frontend/src/keystroke/keymaps/tallyprime.ts` — bindings + `actionLabel`
- `frontend/src/keystroke/navMachine.ts` — the reducer
- `frontend/src/keystroke/buttonBar.ts` — the bars
- `frontend/src/app/defaultScreens.tsx` — the registry
- `frontend/src/app/AppShell.tsx` — `SCREEN_ACTIONS`, `COMPANY_SCOPED_SCREENS`
- `frontend/src/screens/FeaturesScreen.tsx` — **the honesty surface**

**Co-owned, ADD-only, re-read immediately before editing:**
- `corpora/validate.py`
- `frontend/scripts/verify-demo.mts`, `seed-demo.mts`
- `frontend/scripts/e2e-ui-smoke.mjs`

### 2.4 THE TWELVE REGISTRATION POINTS

Silent failures, guaranteed under fan-out. §5.3 audits them.

**A new server RESOURCE — 4 points** (miss one ⇒ default-DENY for *every* level, Owner included):
1. `routeAction()` in `auth_middleware.go`
2. `authz.KnownResources`
3. `authz.KnownReportNames` (if a report)
4. `reportName()` — `routes()` mounts `/print/reports/<name>` for every known report name

**A new client SCREEN — 8 points:**
1. `keystroke/types.ts` `ScreenKind`
2. `corpora/validate.py` `KNOWN_SCREENS` — **fails `make verify`**, the gate that catches you
3. `app/defaultScreens.tsx` registry
4. `keystroke/buttonBar.ts` — else it falls through `default:` to `GATEWAY_BAR` and silently
   advertises Go To / Switch To / Calculator. **No test catches this.**
5. `AppShell.tsx` `COMPANY_SCOPED_SCREENS` — else `useCompanyID()` **throws**
6. `keymaps/tallyprime.ts` `screenBindings` — **the one that bites.** An unbound key is not
   inert: global `F5` opens a Payment voucher and `F9` a Purchase, so a new screen gets a
   voucher on top of itself.
7. `buttons.test.ts` `SCREEN_PATHS` — else the bar is never asserted
8. `actionLabel()` for any new `KeymapAction` — else F1 Help renders a blank row

**An ACL migration — 2 points:** backfill existing companies **and** extend
`seed_company_auth_defaults` (every ACL migration since 0011 re-declares it wholesale — derive
yours from the most recent). Write the test that **creates its company at test time**, or you
will not catch it.

### 2.5 A note on the target, stated once

`HANDOFF-CLOSING-THE-GAP.md` §7 argues that feature-count parity with Tally is the wrong
target and that the moat is the hash-chained audit trail, conversational query, OCR capture and
keystroke fidelity. **The decision to sprint for parity has been taken; this file executes it.**
The one thing that argument still buys you is a *sequencing* constraint worth honouring:

> **Protect keystroke fidelity in every track.** It is 32 corpus cases and it is what lets an
> operator switch without retraining. §3.1 was a failure to protect it, and two keystrokes were
> invented from memory in the very memo that fixed it. Cite `RESEARCH.md` by line, always.

---

## 3. WAVE 1 — THE IMMEDIATE QUEUE

Fully specified; no research left. **Run as one workflow** (§5.1 then §5.2).

### 3.1 🥇 §2.3 statutory gaps #1 and #2 — smallest, highest value, fully cited

Both currently **under-deduct**, which costs the deductor money plus interest. Both now have
primary citations for rate **and** return code.

**Gap #1 — the contractor aggregate threshold.** Model `₹30,000 per payment` **and**
`₹1,00,000 aggregate`. Today twenty ₹20,000 bills attract nothing.
- The aggregate is **per deductee per tax year**, so it needs stored state — this is *not* a
  pure-function fix. **It therefore needs a memo** (rule 0.1) even though it looks small.
- It couples to the return: flag `Y` (below threshold) becomes wrong once the aggregate is
  crossed. Design both together.

**Gap #2 — directors' fees.** 10% at a **NIL** threshold, code **`1028`**.
- ⚠️ Shares its statutory limb (`393(1) Sl. No. 6(iii).D(b)`) with professional services
  (`1027`). **The code cannot be derived from the section.** Store the code, or map from
  nature-of-payment, never from the section alone.

**Also fix in the same track** (from `HANDOFF-CLOSING-THE-GAP.md` §2.3, unchanged):
- #3 TCS no-PAN cap of 20% (s.397(2)(b)(ii)) — latent today, wrong the moment a 20% row lands.
- #4 A manufacturing declaration exempts scrap TCS (s.394(2)) — scrap is one of our two
  shipped TCS sections (code `1073`).
- #5 **Whether you must deduct at all** depends on the payer's own entity type and prior-year
  turnover (₹1 crore business / ₹50 lakh profession). A stored, effective-dated company
  attribute. The engine must **refuse**, not silently return zero.

### 3.2 T6 — TDS/TCS persistence, register, returns (migrations 0049–0050)

**`docs/design/t6-model.md` must be REVISED against the specs now in `docs/statutory/`
before building.** It was written when the codes were unverified. What changed:
- All seven payment codes confirmed; `1005`/`1028` added; **I-7 can be lifted** — the return
  file is no longer blocked.
- The file format is known: ASCII `.txt`, **`^`-delimited variable width**, CRLF, delimiter
  count = fields − 1, block-capital constants, amounts 2dp, **rate 4dp**, dates `ddmmyyyy`
  never future, record types `FH`/`BH`/`CD`/`DD`.
- Thirteen reason flags as a **validity matrix**.
- **New external dependency nothing has modelled: a `.csi` file** from TIN is mandatory to
  validate challans, and the statement's TAN + TAN name must match it. Unreachable from here —
  design the refusal.
- **Migrate the section identifiers.** We still use repealed 1961-Act names (`206C(1)`,
  `206C(1F)`, and the 194-series for TDS). The return keys off s.393/394 Table Sl. Nos. The
  codes are the bridge; move names and codes in one reviewable step.
- **Still true: the FVU cannot be run here** — `/usr/bin/java` is Apple's stub, no JRE. Never
  claim a validated return. If a JRE + RPU 1.1 arrive, this changes and I-7's successor should
  say so.
- Salary TDS (Form 138 / ex-24Q) remains **out of scope**.

### 3.3 T4 slice 2 — bank statement import + matching (migrations **0062–0064**, not 0047)

`docs/design/t4-model.md` §1.4 scopes it. The screen already exists to hang it on.
- **Never auto-clear an ambiguous match** (same amount, same day, two vouchers). Surface both.
- The matching keys are **already bound to explicit refusals** (`F8` modify, `Alt+F8` unlink,
  `Alt+S` potential matches, `F9` set status, `F5` amount view — all `RESEARCH.md:879`). Flip
  them to real actions in the same change that builds them.
- Add bank account no. / IFSC to `ledger` — **columns AND data together** (invariant I-11: a
  blank labelled field reads as "this bank has no IFSC").

### 3.4 BRS print — the product decision is made: **(c)**

Default single-account via `?ledger_id=`, plus a flag for all accounts. Invariant I-10 and
`printTarget.ts`'s `bank-reconciliation` branch currently refuse; replace the refusal with the
real thing and keep the reason string for the no-ledger-id case.

---

## 4. THE WAVE PLAN — parity, in dependency order

Each wave is **one workflow**. Do not overlap waves. Tracks within a wave are chosen to have
**disjoint packages**; the wire owner serialises §2.3 at the end of each.

| Wave | Tracks | Why together | Migrations |
|---|---|---|---|
| **1** | §2.3 gaps · T6 · T4 slice 2 · BRS print | All statutory/banking; disjoint packages (`compliance`, `banking`, `printing`). Wave 1 is fully specified. | 0049–0050, 0062–0064 |
| **2** | **T2** cost centres · **T3** orders + BOM | The two most-used missing Tally features. Disjoint (`ledger` allocation dimension vs a new order plane). T11 depends on T2. | 0040–0041, 0042–0044 |
| **3** | **T5** e-Way Bill · **T9** FIFO/LIFO · **T10** stock groups + compound units | T9+T10 both touch inventory — **serialise those two inside the wave** or worktree-isolate. T5 is independent (`statutory`). | 0047–0048, 0053, 0054–0055 |
| **4** | **T8** multi-currency · **T11** budgets · **T13** interest | T11 needs T2 (wave 2). T8 is deep but isolated. | 0051–0052, 0056–0057, 0059 |
| **5** | **T12** GSTR-9 · **T14** backup/restore/split | T14 is the one where a bug loses somebody's books — **tenant-exact, own wave slot**. | 0058, 0060–0061 |
| **6** | **Export** (Excel/CSV/XML/email) · **per-company print templates UI** · **SSO/OIDC** | The three "daily annoyance" gaps. Export needs an **ACL decision**: `authz.Export` has zero route mappings so it is default-DENY. | 0065, 0066 |

**Per-track hazards are in `HANDOFF-CLOSING-THE-GAP.md` §3.4–3.7 and `PLAN-TALLY-PARITY.md`
§7. Read the track's entry before its memo.** The load-bearing ones:
- **T3: an order is a COMMITTED QUANTITY, NOT A MOVEMENT.** Push it through `stock_movement`
  and closing stock — and therefore the Balance Sheet — is corrupted. Partial fulfilment is
  the normal case.
- **T2: the sum-to-posting invariant is a constraint trigger, not a form check.** Allocations
  live inside the hashed payload. Parallel allocation per category is not one column.
- **T9 moves the Balance Sheet** (closing stock feeds it) — **re-pin `verify:demo`**.
- **T5: reuse the IRN precedent in `internal/statutory`.** NIC is unreachable; ship behind the
  same deterministic-fake + recorded-schema arrangement, and say so on its face.
- **T14 must be tenant-exact.** Reuse `testdb`'s delete-plan census — it already knows every
  tenant-scoped table and is anti-rot by construction.

### 4.1 Not enumerated anywhere, and it is a lot

POS, job costing, ODBC, data synchronisation, payroll attendance/leave, manufacturing
journals, multi-godown transfers with in-transit, price levels per party, reorder levels.
`PLAN-TALLY-PARITY.md` §7 says Tier 2 is explicitly not exhaustive. **If the goal is feature
lock, someone must enumerate these into tracks — that is itself a Wave-0 research workflow**
(§5.4), and it is the honest first step toward "feature locked" rather than "the list we
happen to have".

---

## 5. THE WORKFLOWS

Real scripts. Copy, adjust the track list, run **one at a time**.

> **Size guideline:** this session ships a *medium* cap (~15 agents/workflow). For a wave that
> genuinely needs more, the user can raise it via **`/config` → "Dynamic workflow size"**.
> Remember the real ceiling is ~6 *concurrent* agents (§2.1) — a higher cap buys more total
> agents, not more parallelism.

### 5.1 Wave memo workflow — fan out memos, review each as it lands

```javascript
export const meta = {
  name: 'parity-wave-memos',
  description: 'One design memo per track, each adversarially reviewed as soon as it exists',
  phases: [
    { title: 'Memo', detail: 'one agent per track writes docs/design/<track>-model.md' },
    { title: 'Review', detail: 'adversarial review that RE-DERIVES rather than re-reads' },
  ],
}

const TRACKS = [
  { key: 't6-return',   brief: 'REVISE docs/design/t6-model.md against docs/statutory/. Lift I-7.' },
  { key: 't6-gaps',     brief: 'The five s.2.3 under-deduction gaps. Aggregate threshold needs STORED state.' },
  { key: 't4-slice2',   brief: 'Bank statement import + matching. Migrations 0062-0064.' },
]

const MEMO = {
  type: 'object',
  required: ['track', 'path', 'migrations', 'findings', 'blockers'],
  properties: {
    track: { type: 'string' },
    path: { type: 'string' },
    migrations: { type: 'array', items: { type: 'string' } },
    // The whole point of rule 0.1: what did the memo find BEFORE code was written?
    findings: { type: 'array', items: { type: 'string' } },
    blockers: { type: 'array', items: { type: 'string' } },
  },
}

const VERDICT = {
  type: 'object',
  required: ['accepted', 'mustFix'],
  properties: {
    accepted: { type: 'boolean' },
    mustFix: { type: 'array', items: { type: 'string' } },
    unverifiedClaims: { type: 'array', items: { type: 'string' } },
  },
}

phase('Memo')
const results = await pipeline(
  TRACKS,
  (t) => agent(
    `Write the design memo for track ${t.key} at docs/design/${t.key}-model.md.\n` +
    `BRIEF: ${t.brief}\n\n` +
    `REQUIRED SHAPE: HANDOFF-T1-PRINTING.md section 3.2, all five sections.\n` +
    `RULE 0.1: verify EVERY "stored where" claim with \\d against the live database ` +
    `(psql -h localhost -p 5433 -U postgres -d tally). Not from memory, not from a summary.\n` +
    `RULE 0.2: cite every rate/threshold/code/keystroke. docs/statutory/README.md has the ` +
    `primary TDS/TCS facts; RESEARCH.md is citable BY LINE NUMBER for keystrokes. If you ` +
    `cannot verify something, design the REFUSAL instead of guessing.\n` +
    `Migration numbers: take them ONLY from PLAN-TALLY-PARITY.md section 2.5. Never pick your own.\n` +
    `Write NO implementation code. Return the schema.`,
    { label: `memo:${t.key}`, phase: 'Memo', schema: MEMO },
  ),
  (memo, t) => memo && agent(
    `Adversarially review ${memo.path} for track ${t.key}.\n` +
    `RE-DERIVE, do not re-read: independently check each "stored where" against the live DB, ` +
    `and independently look up each cited rate/code/keystroke at its stated source.\n` +
    `Hunt specifically for: (a) a claim sourced from memory rather than a citation; ` +
    `(b) a migration number outside this track's section-2.5 block; ` +
    `(c) an invariant stated as prose with no enforcing CHECK, trigger or test; ` +
    `(d) a numeric field crossing a package boundary with no unit in its NAME.\n` +
    `Reject on any single one. Return the schema.`,
    { label: `review:${t.key}`, phase: 'Review', schema: VERDICT, effort: 'high' },
  ),
)

// pipeline() returns the LAST stage's result per item, so these are VERDICTS.
// A non-null verdict means that track's memo exists and was reviewed.
const verdicts = results.filter(Boolean)
const accepted = verdicts.filter((v) => v.accepted)
log(`${verdicts.length}/${TRACKS.length} memos reviewed; ${accepted.length} accepted`)
// Rule 0.1: no build starts until a memo is ACCEPTED. Return the rejections loudly.
return { accepted, rejected: verdicts.filter((v) => !v.accepted) }
```

**6 agents** (3 memos + 3 reviews).

### 5.2 Wave build workflow — worktree-isolated builds, then ONE serialised wire step

```javascript
export const meta = {
  name: 'parity-wave-build',
  description: 'Build each track in its own worktree, then serialise the shared wiring files',
  phases: [
    { title: 'Build', detail: 'one worktree-isolated agent per track' },
    { title: 'Wire', detail: 'ONE owner applies every track to the shared files' },
    { title: 'Verify', detail: 'gates, run by a single agent' },
  ],
}

const TRACKS = ['t6-return', 't6-gaps', 't4-slice2']

const BUILT = {
  type: 'object',
  required: ['track', 'filesChanged', 'migrations', 'newResources', 'newScreens', 'testsAdded'],
  properties: {
    track: { type: 'string' },
    filesChanged: { type: 'array', items: { type: 'string' } },
    migrations: { type: 'array', items: { type: 'string' } },
    // These two feed the Wire phase — the track must DECLARE what needs registering
    // rather than registering it itself, which is what keeps the shared files serial.
    newResources: { type: 'array', items: { type: 'string' } },
    newScreens: { type: 'array', items: { type: 'string' } },
    testsAdded: { type: 'array', items: { type: 'string' } },
    mutationProofs: { type: 'array', items: { type: 'string' } },
  },
}

phase('Build')
// isolation:'worktree' because these tracks touch overlapping test helpers and
// migrations dirs. It costs ~200-500ms + disk per agent; it is worth it here and
// NOT worth it for read-only phases.
const built = (await parallel(TRACKS.map((t) => () => agent(
  `Implement track ${t} from its accepted memo docs/design/${t}-model.md.\n\n` +
  `DO NOT EDIT ANY OF THESE — they are serialised to one owner in the next phase. ` +
  `Instead, DECLARE what you need in newResources/newScreens and STOP:\n` +
  `  backend/internal/server/server.go, auth_middleware.go, authz/vocabulary.go,\n` +
  `  frontend keystroke/{types.ts,keymaps/tallyprime.ts,navMachine.ts,buttonBar.ts},\n` +
  `  frontend app/{defaultScreens.tsx,AppShell.tsx}, screens/FeaturesScreen.tsx\n\n` +
  `corpora/validate.py is CO-OWNED: ADD only, and re-read it immediately before editing.\n` +
  `Migrations: ONLY your section-2.5 block. Every new tenant table needs RLS ENABLE+FORCE, ` +
  `a policy with USING AND WITH CHECK, composite (company_id,id) FKs, and a row in ` +
  `testdb's buildFixture. Every migration must be RE-RUNNABLE — prove it with psql -f, ` +
  `not by re-running make migrate.\n` +
  `Every new invariant needs a MUTATION PROOF: break it, watch the RIGHT test fail, restore ` +
  `byte-identically (shasum -a 256 -c), and report it in mutationProofs.\n` +
  `If the database is down, STOP and return BLOCKED. Do not try to start it.\n` +
  `Scope tests to your own packages; do NOT run go test ./... .`,
  { label: `build:${t}`, phase: 'Build', schema: BUILT, isolation: 'worktree' },
)))).filter(Boolean)

// A genuine BARRIER. The wire owner must see EVERY track's declarations at once,
// because it is making one coherent edit per shared file. This is the exception
// that justifies parallel() over pipeline().
phase('Wire')
const wired = await agent(
  `You are the WIRE OWNER for this wave. You are the ONLY agent permitted to edit the ` +
  `shared files. Apply every track's declarations:\n\n` +
  JSON.stringify(built, null, 1) + '\n\n' +
  `For each new RESOURCE, all FOUR points: routeAction(), authz.KnownResources, ` +
  `authz.KnownReportNames (if a report), reportName().\n` +
  `For each new SCREEN, all EIGHT: ScreenKind, corpora/validate.py KNOWN_SCREENS, ` +
  `defaultScreens registry, buttonBar (else it silently falls through to GATEWAY_BAR), ` +
  `AppShell COMPANY_SCOPED_SCREENS (else useCompanyID throws), keymap screenBindings ` +
  `(AN UNBOUND KEY IS NOT INERT - global F5 opens a Payment voucher and F9 a Purchase), ` +
  `buttons.test.ts SCREEN_PATHS, and actionLabel().\n` +
  `Only BUILT keys go on a button bar - buttons.test.ts fails the build if a bar ` +
  `advertises an 'unavailable' action.\n` +
  `Update FeaturesScreen.tsx for every track IN THIS CHANGE, keeping each row's remaining ` +
  `gaps disclosed. Then run make verify and report.`,
  { label: 'wire-owner', phase: 'Wire', effort: 'high' },
)

phase('Verify')
const verdict = await agent(
  `Run the gates for this wave YOURSELF and report exactly what you observed.\n` +
  `1. pg_isready -t 5 AND psql -tAc "select 1" BEFORE and AFTER the long run.\n` +
  `2. cd backend && go test ./... -count=1 -v, then grep -c -- '--- SKIP' (must be 0) ` +
  `and grep -c -- '--- FAIL' (must be 0). Report both counts.\n` +
  `3. make verify.\n` +
  `Do NOT run the live gates here - they must run ALONE, outside a workflow.\n` +
  `Report the NUMBERS you saw, not "passed". A skip census taken while the DB was down ` +
  `is worthless, which is what the psql bracket is for.`,
  { label: 'gates', phase: 'Verify' },
)

return { built, wired, verdict }
```

**5 agents** (3 worktree-isolated builds + 1 wire owner + 1 gate runner).

### 5.3 The registration audit — rule 0.5, cheap, run after every wave

```javascript
export const meta = {
  name: 'registration-audit',
  description: 'Check all twelve registration points; these failures are silent',
  phases: [{ title: 'Audit' }],
}

const POINTS = [
  { key: 'routeAction',      ask: 'Every authz.KnownResources entry has a routeAction() arm, and vice versa.' },
  { key: 'reportName',       ask: 'Every authz.KnownReportNames entry has a reportName() arm. A name known to the vocabulary but not to reportName is served by some arms and default-denied by others.' },
  { key: 'printRoutes',      ask: 'routes() mounts /print/reports/<name> for every KnownReportNames entry, and each either renders or answers 501 deliberately.' },
  { key: 'aclSeeding',       ask: 'Every ACL migration since 0011 BOTH backfills existing companies AND extends seed_company_auth_defaults. Find any that only backfills.' },
  { key: 'screenKinds',      ask: 'Every ScreenKind appears in corpora/validate.py KNOWN_SCREENS, defaultScreens registry, buttonBar (not via default:), and buttons.test.ts SCREEN_PATHS.' },
  { key: 'companyScoped',    ask: 'Every screen that calls useCompanyID() is in AppShell COMPANY_SCOPED_SCREENS.' },
  { key: 'keyCoverage',      ask: 'On every ScreenKind, no key resolves to a DIFFERENT screen\'s action by falling through to the global table. Check F4-F12 and Alt/Ctrl variants specifically: global F5 is a Payment voucher and F9 a Purchase.' },
  { key: 'actionLabels',     ask: 'Every KeymapAction kind has an actionLabel() case, or F1 Help renders a blank row.' },
  { key: 'barHonesty',       ask: 'No button bar advertises an action that resolves to kind "unavailable".' },
  { key: 'rlsAndFixture',    ask: 'Every company-scoped table has RLS ENABLE AND FORCE, a policy with USING AND WITH CHECK, composite (company_id,id) FKs, and a row in testdb buildFixture.' },
  { key: 'migrationBlocks',  ask: 'Every migration file number falls inside its track\'s PLAN-TALLY-PARITY.md section 2.5 block, and no number is used twice.' },
  { key: 'moneyTypes',       ask: 'No float64/ParseFloat/parseFloat/Number() on any money path, server or client.' },
]

const FINDING = {
  type: 'object',
  required: ['point', 'ok', 'gaps'],
  properties: {
    point: { type: 'string' },
    ok: { type: 'boolean' },
    gaps: { type: 'array', items: { type: 'string' } },
    // Silent-vs-loud is the triage key: a gap make verify catches can wait a
    // commit; one no gate catches cannot.
    caughtByAGate: { type: 'boolean' },
  },
}

phase('Audit')
const findings = (await parallel(POINTS.map((p) => () => agent(
  `Audit this registration point across the repo and report precisely.\n` +
  `POINT: ${p.key}\nCHECK: ${p.ask}\n\n` +
  `Read the code; do not infer from documentation. For each gap, give file:line. ` +
  `Also state whether an EXISTING gate catches it - if none does, say so, because that ` +
  `is the difference between a bug and a trap.`,
  { label: `audit:${p.key}`, phase: 'Audit', schema: FINDING, effort: 'low' },
)))).filter(Boolean)

const broken = findings.filter((f) => !f.ok)
const silent = broken.filter((f) => !f.caughtByAGate)
log(`${broken.length}/${findings.length} points have gaps; ${silent.length} are caught by NO gate`)
return { broken, silent }
```

**12 agents**, all `effort: 'low'` — mechanical checks. Cheapest high-value workflow here; run it after every wave.

### 5.4 Wave 0 (optional but recommended for "feature lock") — enumerate the unknown

§4.1's point: you cannot feature-lock against a list you have not written.

```javascript
export const meta = {
  name: 'tally-surface-enumeration',
  description: 'Enumerate the Tally surface this repo has never listed, into candidate tracks',
  phases: [{ title: 'Sweep' }, { title: 'Synthesise' }],
}

// Multi-modal sweep: each agent looks at a DIFFERENT slice, because one search
// angle will not find everything and they are blind to each other.
const AREAS = [
  'POS / retail billing',
  'Job costing and job work',
  'Manufacturing journals and bill of materials beyond T3',
  'Payroll attendance, leave and gratuity',
  'Multi-godown transfers with in-transit stock',
  'Price levels, price lists and discounts per party',
  'Reorder levels and stock ageing alerts',
  'ODBC / data synchronisation / Tally.NET remote access',
]

const CANDIDATES = {
  type: 'object',
  required: ['area', 'features'],
  properties: {
    area: { type: 'string' },
    features: {
      type: 'array',
      items: {
        type: 'object',
        required: ['name', 'whoItStops', 'dataModelRisk'],
        properties: {
          name: { type: 'string' },
          whoItStops: { type: 'string' },
          dataModelRisk: { type: 'string' },
          keystrokes: { type: 'string' },
        },
      },
    },
  },
}

phase('Sweep')
const swept = (await parallel(AREAS.map((a) => () => agent(
  `Enumerate what TallyPrime does in this area, and what THIS repo does not: ${a}\n\n` +
  `Use RESEARCH.md first - it is this project's own researched source and is citable by ` +
  `line number - then the codebase to establish what exists here. Cite RESEARCH.md lines ` +
  `for any keystroke. Do NOT invent a keystroke or a Tally behaviour you cannot cite.\n` +
  `For each feature give: who it stops from switching, and the DATA MODEL risk (the thing ` +
  `that would make it a three-wave track rather than a one-wave one).`,
  { label: `sweep:${a.slice(0, 18)}`, phase: 'Sweep', schema: CANDIDATES },
)))).filter(Boolean)

// Barrier justified: the synthesiser must dedupe and rank across ALL areas at once.
phase('Synthesise')
const plan = await agent(
  `Turn these into candidate TRACKS appended to PLAN-TALLY-PARITY.md section 7, ranked by ` +
  `WHO THEY STOP rather than by size, and grouped so tracks in one wave touch DISJOINT ` +
  `packages.\n\n` + JSON.stringify(swept, null, 1) + '\n\n' +
  `Assign each a migration block CONTINUING from the highest reserved number in ` +
  `section 2.5, and do not renumber anything. Flag any track whose data-model risk means ` +
  `it needs its memo before it can even be sized. Write the file; do not write code.`,
  { label: 'synthesise', phase: 'Synthesise', effort: 'high' },
)
return { plan }
```

**9 agents** (8 sweeps + 1 synthesiser).

---

## 6. ⚠️ HUMAN TASKS — the session cannot do these

Everything in §3 and §4 proceeds without them.

| # | Task | Status |
|---|---|---|
| **6.1** | **EPF/EDLI administrative charges.** No standalone rate notification existed as at 2026-07-30. Watch for one; when it lands, promote the three rows together (they are `ConfUnverified` and REFUSING, which is correct). **Do not substitute `S.O. 2701(E)`** — inspection charges, exempted establishments, different levy. | open, ~₹500/establishment/month accruing |
| **6.2** | **A JRE + the RPU 1.1 zip** (`e-TDS/TCS RPU.zip` from Protean/TIN — the `.exe` is Windows-only, the zip ships the FVU jar). `/usr/bin/java` here is Apple's stub. Would let us actually run the FVU and stop saying "generated, not validated". | optional, high value |
| **6.3** | **Sample file for Form Number 140 Q1 to Q4** (Protean/TIN) — a known-good file to diff our output against. | optional |
| **6.4** | **Run one real business day on it.** Key real vouchers, reconcile a real statement, print a real invoice, write down every annoyance. Everything in these files was found by reading code, which finds *missing tables* — not what is unpleasant. **The two missing buttons in the old §3.1 were found this way.** | **the highest-value item in this table** |

---

## 7. OPEN ITEMS

| # | Item |
|---|---|
| 1 | ~~Migration numbers past 0046 need renumbering~~ — **FIXED in `53d1d3c`.** `PLAN-TALLY-PARITY.md` §2.5 is now the sole authority; §7's duplicate column reads "see §2.5". T4 slice 2 = **0062–0064**. |
| 2 | **`corpora/validate.py` re-declares the TDS/TCS rate table** (`:262`) so it is an independent implementation of the *arithmetic* only. A wrong rate gives two agreeing implementations and a green gate. **Now actionable:** make the fixtures carry a citation to a section + Sl. No. + return code, so a rate change breaks the citation. |
| 3 | **A party's address cannot be ALTERED.** `POST /ledgers` sets it; nothing changes it. Needs `masters.SetLedgerAddress`, a `PUT` route and an ACL decision. Unblocked. |
| 4 | **Rule 46(e): ≥₹50,000 supply to an unregistered party.** May already work now `gststate` exists — **verify before building**. |
| 5 | **`handler_print_reports.go` duplicates `printingdb`'s unexported `loadSupplier` verbatim.** Export one, delete the copy, or they drift. |
| 6 | **The printed Day Book lists voucher VERSIONS**, so an altered voucher contributes twice. Deliberately differs from the JSON one. **Needs a product call.** |
| 7 | **Data Entry gets no report print at all** (0038 seeds zero `allow print report:NAME`). Now user-visible since Ctrl+P exists — the server's 403 is passed through verbatim. Needs a product call on which reports that level may print. |
| 8 | **`internal/security/isolation_test.go`'s RLS assertions are catalogue-driven** over all 41 company-scoped tables. The 7-name `tenantTables` list remains for the narrow-role probe that depends on its exact membership — **do not "tidy" the two into one.** |
| 9 | **`buttonBar.ts` has no test that a bar exists for every `ScreenKind`** — it falls through `default:` to `GATEWAY_BAR`, silently advertising the wrong keys. §5.3 `barHonesty`/`screenKinds` audit it; a real test would be better. |
| 10 | **Section identifiers still use the repealed 1961 Act** (`206C(1)`, `206C(1F)`, the 194-series). The returns key off s.393/394 Table Sl. Nos. Migrate names + codes in one reviewable step during T6. |
| 11 | **The `.csi` challan file is an unmodelled external dependency** of return validation, and it is unreachable from this environment. Design the refusal in T6. |
