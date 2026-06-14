package statutory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRunExecutesStepsOnce(t *testing.T) {
	store := NewMemStore()
	var calls [2]int
	steps := []Step{
		{Name: "a", Run: func(_ context.Context, _ map[string]json.RawMessage) (json.RawMessage, error) {
			calls[0]++
			return json.RawMessage(`"A"`), nil
		}},
		{Name: "b", Run: func(_ context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
			calls[1]++
			if string(prev["a"]) != `"A"` {
				t.Errorf("step b did not see step a output: %s", prev["a"])
			}
			return json.RawMessage(`"B"`), nil
		}},
	}
	state, err := Run(context.Background(), store, "wf1", steps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(state["a"]) != `"A"` || string(state["b"]) != `"B"` {
		t.Fatalf("unexpected state: %v", state)
	}
	if calls[0] != 1 || calls[1] != 1 {
		t.Fatalf("expected each step once, got %v", calls)
	}
}

func TestRunCompletedWorkflowZeroExecutions(t *testing.T) {
	store := NewMemStore()
	var calls int
	steps := []Step{
		{Name: "a", Run: func(_ context.Context, _ map[string]json.RawMessage) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`1`), nil
		}},
	}
	if _, err := Run(context.Background(), store, "wf", steps); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("first run should execute once, got %d", calls)
	}
	// Re-run a fully-completed workflow: ZERO new executions.
	if _, err := Run(context.Background(), store, "wf", steps); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("re-run of completed workflow must do zero executions, got %d", calls)
	}
}

func TestRunStepErrorAbortsAndRetriesTail(t *testing.T) {
	store := NewMemStore()
	var aCalls, bCalls, cCalls int
	failB := true
	steps := func() []Step {
		return []Step{
			{Name: "a", Run: func(_ context.Context, _ map[string]json.RawMessage) (json.RawMessage, error) {
				aCalls++
				return json.RawMessage(`"a"`), nil
			}},
			{Name: "b", Run: func(_ context.Context, _ map[string]json.RawMessage) (json.RawMessage, error) {
				bCalls++
				if failB {
					return nil, errors.New("boom")
				}
				return json.RawMessage(`"b"`), nil
			}},
			{Name: "c", Run: func(_ context.Context, _ map[string]json.RawMessage) (json.RawMessage, error) {
				cCalls++
				return json.RawMessage(`"c"`), nil
			}},
		}
	}

	// First run: b fails, c never runs, b not recorded.
	if _, err := Run(context.Background(), store, "wf", steps()); err == nil {
		t.Fatal("expected error from failing step b")
	}
	if aCalls != 1 || bCalls != 1 || cCalls != 0 {
		t.Fatalf("after first run want a=1 b=1 c=0, got a=%d b=%d c=%d", aCalls, bCalls, cCalls)
	}

	// Resume: a is memoized (not re-run), b retried + c runs.
	failB = false
	if _, err := Run(context.Background(), store, "wf", steps()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if aCalls != 1 {
		t.Fatalf("step a must NOT re-run on resume, got %d", aCalls)
	}
	if bCalls != 2 {
		t.Fatalf("step b should retry, got %d", bCalls)
	}
	if cCalls != 1 {
		t.Fatalf("step c should run after b succeeds, got %d", cCalls)
	}
}
