package statutory

import (
	"context"
	"encoding/json"
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
type StepStore interface {
	// Load returns the completed step results for a workflow, in append order.
	Load(workflowID string) ([]StepResult, error)
	// Append records one newly-completed step's result.
	Append(workflowID string, r StepResult) error
}

// MemStore is the in-memory StepStore — the tested path and the one the local
// statutoryd binary uses. It is concurrency-safe.
type MemStore struct {
	mu  sync.Mutex
	res map[string][]StepResult
}

// NewMemStore builds an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{res: make(map[string][]StepResult)}
}

// Load returns a copy of the recorded results for the workflow.
func (m *MemStore) Load(workflowID string) ([]StepResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.res[workflowID]
	out := make([]StepResult, len(src))
	copy(out, src)
	return out, nil
}

// Append records a completed step. Re-appending the same step name is ignored
// (a fully-completed workflow re-run never duplicates rows).
func (m *MemStore) Append(workflowID string, r StepResult) error {
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

// Step is one unit of work in a workflow. Run receives the outputs of all
// previously-completed steps (by name) and returns this step's output, which is
// recorded on success.
type Step struct {
	Name string
	Run  func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error)
}

// Run executes the steps in order with durable resume semantics.
//
// Before running a step it checks the store for a completed result with that
// Name: if present it REUSES the stored output and does NOT re-run the step.
// This is what makes a memoized external call (e.g. GenerateIRN) execute exactly
// once across retries and resumes. A step error aborts Run and returns the error
// WITHOUT recording that step, so a re-Run retries only the failed step and the
// tail. Completed steps are Append-ed to the store. workflowID is the
// idempotency key; a fully-completed workflow re-Run performs zero step
// executions and returns the final state from the store.
func Run(ctx context.Context, store StepStore, workflowID string, steps []Step) (map[string]json.RawMessage, error) {
	done, err := store.Load(workflowID)
	if err != nil {
		return nil, fmt.Errorf("statutory: load workflow %q: %w", workflowID, err)
	}
	completed := make(map[string]json.RawMessage, len(done))
	for _, r := range done {
		completed[r.Name] = r.Output
	}

	for _, step := range steps {
		if out, ok := completed[step.Name]; ok {
			// Already completed in a prior run: reuse, do not re-execute.
			completed[step.Name] = out
			continue
		}
		out, err := step.Run(ctx, completed)
		if err != nil {
			return nil, err
		}
		if err := store.Append(workflowID, StepResult{Name: step.Name, Output: out}); err != nil {
			return nil, fmt.Errorf("statutory: persist step %q: %w", step.Name, err)
		}
		completed[step.Name] = out
	}
	return completed, nil
}
