// Package durable is the step-replay engine that the e-invoice IRN workflow has
// run on since migration 0013, lifted out of internal/statutory so the agent
// plane can run on the SAME engine without importing the IRN plane.
//
// # WHY IT MOVED, AND WHY IT IS STILL "statutory.Run"
//
// D120 decided W2's durable runs: "statutory.Run + PGStore + a poll-driven
// re-invoker, with a per-run advisory lock" — neither DBOS nor Restate, because
// IMPLEMENTATION_PLAN.md:25 §1.1 forbids new modules. But archtest forbids the
// obvious edge. internal/server's allowed set says, in terms, that
// internal/statutory is NOT importable from cored ("the IRN workflow wraps an
// exactly-once external call in durable state, so it stays in statutoryd"), and
// the agent plane runs inside cored. An internal/agent -> internal/statutory
// edge would link the IRN provider, the cored callback client and the NIC schema
// into every cored binary, which is the thing that comment exists to stop.
//
// So the ENGINE moved and the WORKFLOW stayed. internal/statutory/runner.go now
// aliases every name here (StepResult, StepStore, Step, MemStore, Run,
// ErrParked, ErrLocked) so the IRN path compiles unchanged and IS this code —
// one implementation, two importers, and the import graph still says what it
// said before. This package's allowed set is the EMPTY map: it knows nothing
// about tenants, vouchers or statutory schemas, so both planes can depend on it
// without either dragging the other in.
//
// # THE FIVE THINGS D120 §6 SAID W2 OWED THIS ENGINE, AND WHERE EACH LANDED
//
//	F1 parked != failed     ErrParked + Park(): errors.Is-able, so a poller can
//	                        re-invoke a parked run and page on a failed one.
//	F2 expiry is terminal   NOT HERE, deliberately — see ErrParked. Expiry is a
//	                        decision the caller WRITES (internal/agent records an
//	                        'expired' decision row); an engine that could expire a
//	                        run on its own would be an engine with a clock and a
//	                        policy, and it has neither.
//	F3 the lock is mandatory  StepStore.TryLock, called by Run itself, before the
//	                        first Load. There is no Run that skips it.
//	F4 ctx on StepStore      Load/Append/TryLock all take ctx now.
//	F5 the poller           internal/agent's Poller; this package supplies the
//	                        AdvisoryLocker it holds across each Run.
package durable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// StepResult is a completed step's recorded output, keyed by step Name.
type StepResult struct {
	Name   string          `json:"name"`
	Output json.RawMessage `json:"output"`
}

// StepStore persists completed step results per workflow so a resumed Run skips
// already-completed steps. The workflowID is the idempotency key.
//
// ⛔ TryLock IS PART OF THE STORE CONTRACT, NOT AN OPTIONAL CAPABILITY, AND THAT
// IS D120 F3. The spike measured two concurrent pollers executing the money step
// TWICE while the store recorded it ONCE: both passed the memoisation check,
// both ran the step, and the loser's Append was swallowed by ON CONFLICT DO
// NOTHING. The books got two vouchers and the durable record showed one. A lock
// that callers "should" take is a lock the next caller forgets, and the
// forgetting is invisible in exactly the record one would check — so Run takes
// it itself, and a store that cannot provide mutual exclusion cannot be a store.
type StepStore interface {
	// Load returns the completed step results for a workflow, in append order.
	Load(ctx context.Context, workflowID string) ([]StepResult, error)
	// Append records one newly-completed step's result. Re-appending a
	// recorded name is a silent no-op.
	Append(ctx context.Context, workflowID string, r StepResult) error
	// TryLock takes the per-workflow exclusion lock WITHOUT BLOCKING. ok=false
	// means another holder has it: the caller leaves and comes back on its
	// next tick, which is correct poller behaviour. release must be called
	// exactly once when ok is true.
	TryLock(ctx context.Context, workflowID string) (release func(), ok bool, err error)
}

// Step is one unit of work in a workflow. Run receives the outputs of all
// previously-completed steps (by name) and returns this step's output, which is
// recorded on success.
type Step struct {
	Name string
	Run  func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error)
}

// ErrParked is what a step returns (wrapped, via Park) when it cannot proceed
// YET and must be re-invoked later — an approval with no decision recorded.
//
// ⛔ D120 F1: Run returns a step's error verbatim, so before this sentinel a
// caller could not tell "re-invoke me later" from "page someone". A poller that
// re-invoked every error would retry a permanently failed run forever; one that
// re-invoked none would strand every parked approval. errors.Is(err, ErrParked)
// is the only question a re-invoker needs to ask.
//
// ⚠️ A PARKED STEP RECORDS NOTHING, AND THAT IS THE MECHANISM, NOT A GAP. The
// run aborts before Append, so a parked run is an unrecorded step plus whatever
// earlier steps recorded — no channel, no timer, no goroutine, no in-process
// registry. Surviving a restart is the absence of state, not a feature.
// The corollary D120 F2 measured: nothing about a parked run says it is DEAD.
// A caller whose park has a deadline must make expiry terminal by WRITING
// something the step will read next time (internal/agent writes an 'expired'
// decision row), never by letting its poller stop caring.
var ErrParked = errors.New("durable: run parked; re-invoke later")

// ErrLocked is returned by Run when another holder has the workflow's lock.
// It is not a failure: the other holder is doing the work.
var ErrLocked = errors.New("durable: workflow is locked by another runner")

// ParkedError carries WHY a step parked. errors.Is(err, ErrParked) holds.
type ParkedError struct {
	Step   string
	Reason string
}

func (e *ParkedError) Error() string {
	return fmt.Sprintf("durable: step %q parked: %s", e.Step, e.Reason)
}

// Is makes errors.Is(e, ErrParked) true.
func (e *ParkedError) Is(target error) bool { return target == ErrParked }

// Park is the value a step returns to park the run.
func Park(step, reason string) error { return &ParkedError{Step: step, Reason: reason} }

// Run executes the steps in order with durable resume semantics.
//
// It first takes the workflow's lock (StepStore.TryLock) and holds it for the
// whole call; if another holder has it, Run returns ErrLocked having executed
// nothing. Then, before running a step, it checks the store for a completed
// result with that Name: if present it REUSES the stored output and does NOT
// re-run the step. This is what makes a memoised external call (GenerateIRN, a
// voucher commit) execute exactly once across retries, resumes and — with the
// lock — concurrent pollers. A step error aborts Run and returns the error
// WITHOUT recording that step, so a re-Run retries only the failed step and the
// tail. workflowID is the idempotency key; a fully-completed workflow re-Run
// performs zero step executions and returns the final state from the store.
func Run(ctx context.Context, store StepStore, workflowID string, steps []Step) (map[string]json.RawMessage, error) {
	release, ok, err := store.TryLock(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("durable: lock workflow %q: %w", workflowID, err)
	}
	if !ok {
		return nil, ErrLocked
	}
	defer release()

	// ⛔ THE LOAD HAPPENS UNDER THE LOCK. Loading first and locking second
	// would reopen exactly the window F3 closes: a runner that read "commit not
	// done", then waited for the lock while the holder committed, would run
	// commit again from its stale read.
	done, err := store.Load(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("durable: load workflow %q: %w", workflowID, err)
	}
	completed := make(map[string]json.RawMessage, len(done))
	for _, r := range done {
		completed[r.Name] = r.Output
	}

	for _, step := range steps {
		if _, ok := completed[step.Name]; ok {
			continue // completed in a prior run: reuse, do not re-execute
		}
		out, err := step.Run(ctx, completed)
		if err != nil {
			return nil, err
		}
		if err := store.Append(ctx, workflowID, StepResult{Name: step.Name, Output: out}); err != nil {
			return nil, fmt.Errorf("durable: persist step %q: %w", step.Name, err)
		}
		completed[step.Name] = out
	}
	return completed, nil
}

// Next decides the step after everything recorded so far. It is called with
// the recorded steps IN ORDER and returns the next step to execute, or
// ok=false when the workflow is finished.
//
// ⛔ IT MUST BE A PURE FUNCTION OF done. That is the entire replay contract: a
// re-invocation after a restart reloads the same recorded steps, so a pure
// Next derives the same next step, and nothing already recorded runs again.
// A Next that consulted the clock, the database or a model would make the
// resumed run a different run.
type Next func(done []StepResult) (step Step, ok bool, err error)

// RunDynamic is Run for a workflow whose shape is only known as it unfolds —
// an agent loop, where step k+1 depends on what the model decided at step k.
// Same lock, same memoisation, same park semantics as Run: it takes the
// workflow's lock, loads what is recorded, and asks next for each following
// step until next says done or a step errors (a parked step records nothing).
//
// It returns the recorded steps in order.
func RunDynamic(ctx context.Context, store StepStore, workflowID string, next Next) ([]StepResult, error) {
	release, ok, err := store.TryLock(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("durable: lock workflow %q: %w", workflowID, err)
	}
	if !ok {
		return nil, ErrLocked
	}
	defer release()

	done, err := store.Load(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("durable: load workflow %q: %w", workflowID, err)
	}
	seen := make(map[string]bool, len(done))
	completed := make(map[string]json.RawMessage, len(done))
	for _, r := range done {
		seen[r.Name] = true
		completed[r.Name] = r.Output
	}
	for {
		step, more, err := next(done)
		if err != nil {
			return done, err
		}
		if !more {
			return done, nil
		}
		// A Next that proposes a recorded name has a bug that would otherwise
		// loop forever (Append of a recorded name is a silent no-op, so the
		// same step would be proposed again and again).
		if seen[step.Name] {
			return done, fmt.Errorf("durable: next proposed %q, which is already recorded for %q", step.Name, workflowID)
		}
		out, err := step.Run(ctx, completed)
		if err != nil {
			return done, err
		}
		if err := store.Append(ctx, workflowID, StepResult{Name: step.Name, Output: out}); err != nil {
			return done, fmt.Errorf("durable: persist step %q: %w", step.Name, err)
		}
		seen[step.Name] = true
		completed[step.Name] = out
		done = append(done, StepResult{Name: step.Name, Output: out})
	}
}

// MemStore is the in-memory StepStore — the tested path and the one the local
// statutoryd binary uses. It is concurrency-safe, and its TryLock is a real
// per-workflow try-lock within the process (it cannot exclude another process,
// which is why every multi-instance deployment uses a Postgres store).
type MemStore struct {
	mu     sync.Mutex
	res    map[string][]StepResult
	locked map[string]bool
}

// NewMemStore builds an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{res: make(map[string][]StepResult), locked: make(map[string]bool)}
}

// Load returns a copy of the recorded results for the workflow.
func (m *MemStore) Load(_ context.Context, workflowID string) ([]StepResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.res[workflowID]
	out := make([]StepResult, len(src))
	copy(out, src)
	return out, nil
}

// Append records a completed step. Re-appending the same step name is ignored
// (a fully-completed workflow re-run never duplicates rows).
func (m *MemStore) Append(_ context.Context, workflowID string, r StepResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.res[workflowID] {
		if existing.Name == r.Name {
			return nil
		}
	}
	m.res[workflowID] = append(m.res[workflowID], r)
	return nil
}

// TryLock is a non-blocking per-workflow lock within this process.
func (m *MemStore) TryLock(_ context.Context, workflowID string) (func(), bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locked[workflowID] {
		return nil, false, nil
	}
	m.locked[workflowID] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.locked, workflowID)
			m.mu.Unlock()
		})
	}, true, nil
}

var _ StepStore = (*MemStore)(nil)
