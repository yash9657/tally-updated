package statutory

import (
	"context"
	"encoding/json"

	"github.com/khata/cored/internal/durable"
)

// The durable step engine that this plane has run the IRN workflow on since
// migration 0013 now lives in internal/durable, so that the agent plane (which
// runs inside cored, and may not import this package — see internal/server's
// allowedDeps) can run on the SAME engine. Every name below is an ALIAS, not a
// copy: statutory.Run IS durable.Run, so the IRN path and the agent path cannot
// drift into two engines with two sets of semantics. See durable's package doc
// for what moved, why, and the five items D120 §6 said the engine owed.

// StepResult is a completed step's recorded output, keyed by step Name.
type StepResult = durable.StepResult

// StepStore persists completed step results per workflow so a resumed Run skips
// already-completed steps. The workflowID is the idempotency key. Since W2 it
// takes a ctx on every method (D120 F4) and carries TryLock (D120 F3).
type StepStore = durable.StepStore

// Step is one unit of work in a workflow.
type Step = durable.Step

// MemStore is the in-memory StepStore — the tested path and the one the local
// statutoryd binary uses.
type MemStore = durable.MemStore

// ErrParked and ErrLocked are durable's sentinels, re-exported beside Run
// because D120 F1 said the parked sentinel belongs "beside Run in runner.go".
var (
	ErrParked = durable.ErrParked
	ErrLocked = durable.ErrLocked
)

// Park is the value a step returns to park the run (errors.Is(err, ErrParked)).
func Park(step, reason string) error { return durable.Park(step, reason) }

// NewMemStore builds an empty in-memory store.
func NewMemStore() *MemStore { return durable.NewMemStore() }

// Run executes the steps in order with durable resume semantics, under the
// workflow's lock. See durable.Run.
func Run(ctx context.Context, store StepStore, workflowID string, steps []Step) (map[string]json.RawMessage, error) {
	return durable.Run(ctx, store, workflowID, steps)
}
