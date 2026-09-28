package durable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestParkedIsDistinguishableFromFailed is D120 F1: a poller must be able to
// tell "re-invoke me later" from "page someone" with errors.Is alone, and a
// parked step must record NOTHING (the whole park mechanism).
func TestParkedIsDistinguishableFromFailed(t *testing.T) {
	store := NewMemStore()
	boom := errors.New("provider exploded")
	var parked bool
	steps := func() []Step {
		return []Step{
			{Name: "plan", Run: func(context.Context, map[string]json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`{"ok":true}`), nil
			}},
			{Name: "approve", Run: func(context.Context, map[string]json.RawMessage) (json.RawMessage, error) {
				if parked {
					return nil, Park("approve", "no decision")
				}
				return nil, boom
			}},
		}
	}

	parked = true
	_, err := Run(context.Background(), store, "wf", steps())
	if !errors.Is(err, ErrParked) {
		t.Fatalf("parked run: errors.Is(err, ErrParked) = false for %v", err)
	}
	var pe *ParkedError
	if !errors.As(err, &pe) || pe.Step != "approve" {
		t.Fatalf("parked run: want a *ParkedError naming the step, got %#v", err)
	}

	parked = false
	_, err = Run(context.Background(), store, "wf", steps())
	if errors.Is(err, ErrParked) {
		t.Fatalf("a FAILED step reported as parked: %v — a poller would retry it forever", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("failed run: want the step's own error, got %v", err)
	}

	got, _ := store.Load(context.Background(), "wf")
	if len(got) != 1 || got[0].Name != "plan" {
		t.Fatalf("recorded steps = %v, want only [plan]: a parked or failed step must record nothing", got)
	}
}

// TestRunRefusesWhileLocked proves Run takes the lock ITSELF (D120 F3): with
// the lock held elsewhere it returns ErrLocked and executes no step.
//
// Mutation proof: replace durable.Run's TryLock call with an always-granted
// stub and this test fails ("executed 1 step(s) while locked").
func TestRunRefusesWhileLocked(t *testing.T) {
	store := NewMemStore()
	release, ok, err := store.TryLock(context.Background(), "wf")
	if err != nil || !ok {
		t.Fatalf("first TryLock: ok=%v err=%v", ok, err)
	}
	var executed int32
	steps := []Step{{Name: "commit", Run: func(context.Context, map[string]json.RawMessage) (json.RawMessage, error) {
		atomic.AddInt32(&executed, 1)
		return json.RawMessage(`{}`), nil
	}}}
	if _, err := Run(context.Background(), store, "wf", steps); !errors.Is(err, ErrLocked) {
		t.Fatalf("Run under a held lock: got %v, want ErrLocked", err)
	}
	if n := atomic.LoadInt32(&executed); n != 0 {
		t.Fatalf("executed %d step(s) while locked, want 0", n)
	}
	release()
	release() // idempotent: a double release must not unlock someone else's hold
	if _, err := Run(context.Background(), store, "wf", steps); err != nil {
		t.Fatalf("Run after release: %v", err)
	}
	if n := atomic.LoadInt32(&executed); n != 1 {
		t.Fatalf("executed %d, want 1", n)
	}
}

// TestMemStoreConcurrentRunsExecuteOnce races 16 goroutines at one workflow.
// Exactly one executes; the rest see ErrLocked or a completed record.
func TestMemStoreConcurrentRunsExecuteOnce(t *testing.T) {
	store := NewMemStore()
	var executed int32
	start := make(chan struct{})
	steps := []Step{{Name: "commit", Run: func(context.Context, map[string]json.RawMessage) (json.RawMessage, error) {
		atomic.AddInt32(&executed, 1)
		time.Sleep(20 * time.Millisecond) // widen the window, as the spike did
		return json.RawMessage(`{}`), nil
	}}}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := Run(context.Background(), store, "wf", steps)
			if err != nil && !errors.Is(err, ErrLocked) {
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if n := atomic.LoadInt32(&executed); n != 1 {
		t.Fatalf("16 concurrent runs executed the step %d times, want exactly 1", n)
	}
}

// ---------------------------------------------------------------------------
// AdvisoryLocker against a real Postgres.
// ---------------------------------------------------------------------------

func testAppDSN() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
}

// pgPool follows handler_itc_test.go's shape: CORED_SKIP_DB is the only skip;
// an unreachable database with it unset is a FAILURE, not a green run.
func pgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	pool, err := pgxpool.New(context.Background(), testAppDSN())
	if err == nil {
		err = pool.Ping(context.Background())
	}
	if err != nil {
		t.Fatalf("postgres unavailable and CORED_SKIP_DB is unset: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestAdvisoryLockerExcludesAcrossSessions proves the lock excludes a second
// SESSION (two separate pools = two separate processes' worth of connections),
// that release really unlocks, and that the namespace separates lock spaces.
func TestAdvisoryLockerExcludesAcrossSessions(t *testing.T) {
	a := AdvisoryLocker{Pool: pgPool(t), Namespace: "durable-test"}
	b := AdvisoryLocker{Pool: pgPool(t), Namespace: "durable-test"}
	other := AdvisoryLocker{Pool: b.Pool, Namespace: "durable-test-other"}
	id := fmt.Sprintf("wf-%d", time.Now().UnixNano())
	ctx := context.Background()

	relA, ok, err := a.TryLock(ctx, id)
	if err != nil || !ok {
		t.Fatalf("A TryLock: ok=%v err=%v", ok, err)
	}
	if _, ok, err := b.TryLock(ctx, id); err != nil || ok {
		t.Fatalf("B TryLock while A holds it: ok=%v err=%v, want ok=false", ok, err)
	}
	relO, ok, err := other.TryLock(ctx, id)
	if err != nil || !ok {
		t.Fatalf("a different namespace must not share the lock: ok=%v err=%v", ok, err)
	}
	relO()
	relA()
	relB, ok, err := b.TryLock(ctx, id)
	if err != nil || !ok {
		t.Fatalf("B TryLock after A released: ok=%v err=%v — release did not unlock", ok, err)
	}
	relB()
}

// TestRunDynamicReplaysAPureNext drives a loop whose next step depends on the
// previous step's output, parks it half way, and resumes it: nothing recorded
// runs twice, and the resumed run continues where it stopped.
func TestRunDynamicReplaysAPureNext(t *testing.T) {
	store := NewMemStore()
	var executed []string
	gate := false
	next := func(done []StepResult) (Step, bool, error) {
		n := len(done)
		if n == 4 {
			return Step{}, false, nil
		}
		name := fmt.Sprintf("s%d", n)
		return Step{Name: name, Run: func(context.Context, map[string]json.RawMessage) (json.RawMessage, error) {
			if n == 2 && !gate {
				return nil, Park(name, "waiting")
			}
			executed = append(executed, name)
			return json.RawMessage(fmt.Sprintf(`{"n":%d}`, n)), nil
		}}, true, nil
	}
	done, err := RunDynamic(context.Background(), store, "wf", next)
	if !errors.Is(err, ErrParked) || len(done) != 2 {
		t.Fatalf("first run: done=%d err=%v, want 2 recorded and parked", len(done), err)
	}
	gate = true
	done, err = RunDynamic(context.Background(), store, "wf", next)
	if err != nil || len(done) != 4 {
		t.Fatalf("resume: done=%d err=%v", len(done), err)
	}
	if got := fmt.Sprint(executed); got != "[s0 s1 s2 s3]" {
		t.Fatalf("executions %s, want each step exactly once", got)
	}
	// A Next that re-proposes a recorded step is refused, not looped on.
	_, err = RunDynamic(context.Background(), store, "wf", func([]StepResult) (Step, bool, error) {
		return Step{Name: "s0", Run: func(context.Context, map[string]json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		}}, true, nil
	})
	if err == nil {
		t.Fatal("a Next re-proposing a recorded step was not refused")
	}
	// And the lock applies to RunDynamic too.
	rel, _, _ := store.TryLock(context.Background(), "wf")
	if _, err := RunDynamic(context.Background(), store, "wf", next); !errors.Is(err, ErrLocked) {
		t.Fatalf("RunDynamic under a held lock: %v, want ErrLocked", err)
	}
	rel()
}
