# D120 — durable runs for W2: the spike, and why §7-2's question was the wrong one

Date: 2026-09-11. Status: **DECIDED — neither DBOS nor Restate. `IMPLEMENTATION_PLAN.md:25` §1.1 stands.**
Evidence: `backend/internal/statutory/park_resume_spike_test.go`, run against the live `tally_pg`
database. Every figure below was printed by a test, not reasoned to.

Supersedes the framing of `PLAN-AGENTIC.md` §7-2 ("Choose DBOS Transact Go vs Restate after a one-week
spike"). Resolves `HANDOFF-AGENTIC-AFTER-W0.md` §6.2.

---

## 1. The question, restated before it was answered

§7-2 asked which of two workflow engines to adopt. Both are new Go modules, and
`IMPLEMENTATION_PLAN.md:25` §1.1 is binding: **"NO new module dependencies."** W0 had just upheld that
rule at real cost — a 487-line OTLP exporter hand-written rather than the OTel SDK (D119 §1). A workflow
engine is not 487 lines, so §7-2 as written could only ever have resolved by amending §1.1.

There is a third option, and it was already in this repository, already in production:
`internal/statutory` — `Run` / `StepStore` / `PGStore`, migration 0013, driving the e-invoice IRN path
where exactly-once matters because the government issues an IRN once.

So the spike ran **the W2 gate itself** against that third option, rather than comparing two engines
neither of which we may take.

The gate clause, quoted from `PLAN-AGENTIC.md` §3 W2:

> approval = `Recv` on a per-run topic with a days-long timeout; a three-day approval survives a
> `cored` restart

---

## 2. ⭐ The claim, and why it is a claim about *absence*

Write the approval as a step that reads a decision from a table and returns a sentinel when none is
there. `Run` aborts **without recording that step**. Everything before it stays recorded.

**A parked run is then an unrecorded step plus a row in a table.** There is no channel, no timer, no
goroutine, no in-process registry — so "survives a restart" is not a feature that had to be bought. It
is the *absence* of durable in-process state. That is the whole argument, and it is why a step-replay
engine can do this at all.

---

## 3. What was measured

⛔ **THE RESTARTS ARE REAL PROCESSES.** The spike re-executes its own test binary once per epoch and
communicates with the child only through Postgres and a sentinel on stdout. **The four epochs run under
four distinct pids, and the test asserts that distinctness** — otherwise "restart" would be a word in a
comment, and a later refactor that quietly ran the epochs in-process would leave the spike green while
testing nothing it claims to test.

Exactly-once is measured by an append-only execution log with a **unique row name per execution**, not
by reading the run's own steps. Memoisation means a resumed step returns its output without running, so
counting the run's steps would report 1 whether the step executed once or five times.

| Epoch | Clock | Process | Result | `commit` executions |
|---|---|---|---|---|
| 1 | day 0, 09:00 | fresh | `PARKED` after `plan`, `preview` | 0 |
| 2 | +71 h, **after restart** | fresh | `PARKED`; `plan`/`preview` **did not re-run** | 0 |
| — | human approves in a different process | | decision row written | 0 |
| 3 | +72 h | fresh | `COMPLETED` | **1** |
| 4 | +73 h, poller fires again | fresh | `COMPLETED` | **1** |

**A 72-hour approval survived four process lifetimes and the money-moving step executed exactly once.**

The gate clause is satisfied — with one substitution: **park-by-polling instead of `Recv` on a topic.**
The observable requirement ("a three-day approval survives a restart") is met; the mechanism named in the
plan is not the mechanism used. That substitution is the point, not a shortfall: a topic subscription is
in-process state to lose, and the polled design has none.

### The spike is mutation-proved

A spike that cannot fail is not evidence. Two injections, both caught:

- **Destroy durability** (workflow id varies per epoch): `plan executed 2 time(s), want 1`, and the
  recorded step list came back empty.
- **Break exactly-once** (erase the recorded `commit` before the last epoch):
  `step "commit" executed 2 time(s), want 1`.

⚠️ The second injection also caught a defect in the spike's own reporting: the `VERDICT` line printed on
a **failed** run, so a run whose assertions said "commit executed 2 times" still ended with "commit
executed exactly once". The verdict is now withheld behind `t.Failed()`. A summary that can contradict
its own assertions is worse than no summary, because the summary is the line that gets quoted into a
document like this one.

---

## 4. ⛔⛔ F5 — the finding that decides the design, and it is not in the plan

`PGStore`'s header states that `Append` takes no row lock, then discharges the consequence with a
**deployment assumption**: *"Single-instance statutoryd remains the deployment shape
(`IMPLEMENTATION_PLAN.md` §7 deferral 7)."*

**That assumption does not carry over to W2.** `statutoryd` is one process by choice. The agent plane
runs inside **`cored`**, a horizontally-scaled HTTP server.

Measured, not inferred — `TestConcurrentPollersDoubleExecuteTheMoneyStep`:

> **two concurrent pollers executed the money step 2 times while the step store recorded 1**

⛔ **The durable record does not reveal the double execution.** Both processes pass the memoisation
check, both run `commit`, and the loser's `Append` is swallowed by `ON CONFLICT DO NOTHING`. The books
get two vouchers; the step store shows one. A double-write that is invisible in the ledger this engine
keeps is the worst shape the defect could have.

The test asserts the defect — it is **green when the money step runs twice** — so it will go red the day
someone fixes the underlying race, which is the correct time to delete it.

### The fix, also measured

`TestAdvisoryLockPreventsDoubleExecution`: each poller takes
`pg_try_advisory_lock(hashtext(workflow_id))` on one connection, held for the whole `Run`. Under
identical contention:

> **under contention ([COMPLETED LOCKED]) the money step executed exactly once**

The assertion is `commits == 1` **exactly**, and it separately requires that one poller reported
`LOCKED` — without that, a run whose two children simply failed to overlap would also show 1 and the
lock would be credited with a result it did not produce.

`pg_try_advisory_lock` is non-blocking (the loser leaves and returns on its next tick, which is correct
poller behaviour), needs **no new table**, **no migration** and **no new dependency**. §1.1 intact.

---

## 5. The decision

**Neither DBOS Transact Go nor Restate. Build W2's durable runs on `statutory.Run` + `PGStore` + a
poll-driven re-invoker, with a per-run advisory lock.**

`IMPLEMENTATION_PLAN.md:25` §1.1 **stands, unamended**. ⛔ Had the answer gone the other way, the
amendment would have had to land in `IMPLEMENTATION_PLAN.md` in the same commit as the dependency, or
every subsequent PR checklist would be a lie. It did not, so there is nothing to amend — but the
reasoning is recorded here so the next person to want a dependency has to beat a measurement rather than
an opinion.

W0 had already shape-matched this on purpose: `trajectory.StepResult` is field-for-field
`statutory.StepResult`, and `trajectory.FileStepStore` has exactly `statutory.StepStore`'s method set
(`cassette.go:571-589`). A cassette is "a `StepStore` backed by a file", so record/replay is a one-line
store swap.

---

## 6. ⛔ What W2 owes before it adopts this — five items, none optional

**F1 — `Run` conflates "parked" with "failed".** It returns the step's error verbatim, so a caller
cannot tell "re-invoke me later" from "page someone". The poller must re-invoke the first and must not
re-invoke the second. Needs an exported `ErrParked` beside `Run` in `runner.go`, with `errors.Is`
support. The IRN workflow has no parked state, which is why this never surfaced.

**F2 — expiry is not terminal, and a late approval still commits.** Measured: after the 72-hour window
closed, the run reported `EXPIRED` — then a decision written at +96 h was picked up at +97 h and the run
**completed**. The expired step recorded nothing, so nothing says the run is dead. This is the sharpest
edge of "a parked run is just an unrecorded step". **W2 must make expiry terminal by writing something**
— a rejected decision, or a terminal step — never by letting the poller stop caring.

**F3 — the per-run advisory lock is mandatory, not an optimisation.** See §4. Without it, two `cored`
instances double-post vouchers invisibly.

**F4 — plumb `ctx` through `StepStore` when W2 adopts it, not later.** `pgstore.go:25-33` records that
the methods predate context plumbing and that the fix is worth doing. Doing it at adoption costs one
refactor; doing it afterwards costs one refactor plus every call site W2 adds.

**F5 — the poller itself does not exist.** The spike's parent process stood in for it. W2 needs a real
re-invoker (`pg_cron`, the existing `notify` outbox pattern, or a ticker in `cored`), plus the decision
of how often a parked run is retried and how that interacts with the advisory lock.

Also carried forward from the spike's shape: **the approval deadline must be recorded by an earlier
step** (or a column on the approvals row), because the parked step records nothing and has nowhere to
put one. In W2 proper this is a column the inbox needs anyway to render "expires in 2 days".

---

## 7. What this spike did **not** test

Stated so nobody reads it as broader than it is:

- **No agent, no LLM, no tools.** The steps are stand-ins. This tests the *durability substrate*, not
  W2's loop.
- **No RLS, no tenancy.** `statutory_step` carries no RLS by design (0013's header). W2's runs are
  tenant data and the intent log (0092) is where that question gets answered.
- **No load.** Two concurrent pollers, not two hundred. The advisory lock is proven correct under
  contention, not characterised under it.
- **The scratch tables are `statutory_step` rows** under `spike:` / `spike-approval:` / `spike-exec:`
  workflow ids, so the spike consumes no migration number (0092 and 0093 stay reserved) and leaves zero
  residue — verified 0 rows after a clean run.
