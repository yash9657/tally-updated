package statutory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// park_resume_spike_test.go is the W2 durable-runs spike
// (HANDOFF-AGENTIC-AFTER-W0.md §5, PLAN-AGENTIC.md §7-2).
//
// ---------------------------------------------------------------------------
// THE QUESTION THIS FILE ANSWERS
// ---------------------------------------------------------------------------
//
// PLAN-AGENTIC.md §7-2 asks "DBOS Transact Go vs Restate, after a one-week
// spike". Both are new Go modules and IMPLEMENTATION_PLAN.md:25 §1.1 is binding
// ("NO new module dependencies") — W0 upheld §1.1 by hand-writing a 487-line
// OTLP exporter rather than taking the OTel SDK, and a workflow engine is not
// 487 lines. So the question was reframed: **does statutory.Run + PGStore + a
// poll-driven re-invoker satisfy the W2 gate instead?**
//
// The W2 gate clause under test, quoted from PLAN-AGENTIC.md §3 W2:
//
//	"approval = Recv on a per-run topic with a days-long timeout; a three-day
//	 approval survives a `cored` restart"
//
// ⭐ THE CLAIM BEING TESTED is that a park-and-wake built on step replay has **no
// in-memory state to lose**: write the approval as a step that reads a decision
// from a table and returns a sentinel when it is absent, and the run aborts
// having recorded nothing for that step. A parked run is then an unrecorded step
// plus a row in a table, and "survives a restart" is not a feature you buy — it
// is the ABSENCE of durable in-process state.
//
// ⛔ THE RESTARTS HERE ARE REAL PROCESSES, NOT A SIMULATION. TestParkResume...
// re-executes this test binary once per epoch (the standard Go subprocess
// idiom) and communicates with it only through Postgres and a sentinel on
// stdout. Nothing but the run id crosses an epoch boundary in memory. A version
// of this spike that called Run twice inside one process would prove only that
// a map lookup works, which is the assertion a broken design also satisfies.
//
// ---------------------------------------------------------------------------
// WHAT THE SPIKE USES INSTEAD OF NEW TABLES, AND WHY THAT IS NOT A DODGE
// ---------------------------------------------------------------------------
//
// A spike must not consume a migration number — 0092 and 0093 are reserved for
// W2's intent log and autonomy table (HANDOFF §8). So the three things this
// design needs beyond the runner are all modelled inside statutory_step under
// SEPARATE workflow ids, which is residue-free and cleaned up by workflow id:
//
//	spike:<id>           the run itself (the real subject)
//	spike-approval:<id>  stands in for the approvals table the inbox writes
//	spike-exec:<id>      an append-only log of ACTUAL step executions
//
// ⛔ spike-exec IS WHAT MAKES EXACTLY-ONCE MEASURABLE RATHER THAN ASSUMED. Step
// memoisation means a resumed run returns a completed step's output without
// running it — so counting a side effect by reading the run's own steps would
// count 1 whether the step executed once or five times. Every execution appends
// a UNIQUELY-NAMED row here (Append is ON CONFLICT DO NOTHING on
// (workflow_id, step_name), so a fixed name would silently collapse repeats
// into one row and manufacture the result this spike is supposed to test).
// Counting rows counts executions.

const (
	spikeChildEnv    = "KHATA_SPIKE_CHILD"
	spikeRunEnv      = "KHATA_SPIKE_RUN_ID"
	spikeNowEnv      = "KHATA_SPIKE_NOW"
	spikeDelayEnv    = "KHATA_SPIKE_COMMIT_DELAY"
	spikeLockEnv     = "KHATA_SPIKE_ADVISORY_LOCK"
	spikeSentinelPfx = "SPIKE_RESULT="
	spikePIDPfx      = "SPIKE_PID="
)

// approvalWindow is the "days-long timeout" of the gate clause.
const approvalWindow = 72 * time.Hour

// errParked is returned by the approval step when no decision has been recorded.
//
// ⛔ FINDING (F1), AND IT IS THE ONE CONCRETE GAP THIS SPIKE FOUND IN THE RUNNER.
// statutory.Run returns the step's error verbatim, so its caller cannot tell
// "parked, re-invoke me later" from "failed, page someone". Today the IRN
// workflow has no parked state and never needed the distinction. W2 does: the
// poller must re-invoke a parked run and must NOT re-invoke a permanently failed
// one. The sentinel lives here in the spike; if W2 adopts this design it belongs
// in runner.go beside Run, as an exported ErrParked with errors.Is support.
var errParked = errors.New("spike: parked awaiting approval")

// errApprovalExpired is the timeout half of the gate clause.
var errApprovalExpired = errors.New("spike: approval window expired")

func spikeRunWorkflowID(id string) string      { return "spike:" + id }
func spikeApprovalWorkflowID(id string) string { return "spike-approval:" + id }
func spikeExecWorkflowID(id string) string     { return "spike-exec:" + id }

// previewOutput is what the preview step records.
//
// ⛔ THE DEADLINE IS RECORDED BY AN EARLIER STEP, AND IT HAS TO BE. The approval
// step records NOTHING while it is parked — that is the entire mechanism — so it
// has nowhere to put a deadline of its own. Persisting it in the last completed
// step before the park is the shape the design forces: a parked run is an
// unrecorded step plus previously-recorded state. (In W2 proper this is a column
// on the approvals row, which the inbox needs to render "expires in 2 days"
// anyway.)
type previewOutput struct {
	ApprovalDeadline time.Time `json:"approval_deadline"`
	Amount           string    `json:"amount"`
}

type decisionOutput struct {
	Decision string    `json:"decision"`
	By       string    `json:"by"`
	At       time.Time `json:"at"`
}

// spikeSteps builds the agent run under test: plan → preview → await_approval →
// commit → notify, where commit is the money-moving step that must execute
// exactly once across every restart.
func spikeSteps(store StepStore, runID string, now time.Time, recordExec func(step string) error) []Step {
	return []Step{
		{
			Name: "plan",
			Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
				if err := recordExec("plan"); err != nil {
					return nil, err
				}
				return json.Marshal(map[string]string{"intent": "sales invoice Acme 50000 +18% GST"})
			},
		},
		{
			Name: "preview",
			Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
				if err := recordExec("preview"); err != nil {
					return nil, err
				}
				return json.Marshal(previewOutput{
					ApprovalDeadline: now.Add(approvalWindow).UTC(),
					Amount:           "59000.00",
				})
			},
		},
		{
			Name: "await_approval",
			Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
				// ⛔ NO recordExec HERE, DELIBERATELY. This step is polled: it
				// runs on every re-invocation until a decision exists. Counting
				// it as an execution would make the exec log measure how often
				// the poller fired, not how often work happened — and the
				// commit assertion below would lose its meaning by association.

				// The approvals "table": whatever the inbox wrote.
				decided, err := store.Load(spikeApprovalWorkflowID(runID))
				if err != nil {
					return nil, fmt.Errorf("spike: read approvals: %w", err)
				}
				for _, d := range decided {
					if d.Name == "decision" {
						// Record the decision INTO the run, so the chain carries
						// who decided and when — PLAN-AGENTIC §3 W2, "the
						// decision is written to the chain".
						return d.Output, nil
					}
				}

				// No decision yet. Has the window closed?
				var pv previewOutput
				if raw, ok := prev["preview"]; ok {
					if err := json.Unmarshal(raw, &pv); err != nil {
						return nil, fmt.Errorf("spike: decode preview: %w", err)
					}
					if !pv.ApprovalDeadline.IsZero() && now.After(pv.ApprovalDeadline) {
						return nil, errApprovalExpired
					}
				}
				return nil, errParked
			},
		},
		{
			Name: "commit",
			Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
				// The money-moving step. Everything in this file exists to
				// assert that this line runs exactly once.
				var d decisionOutput
				if err := json.Unmarshal(prev["await_approval"], &d); err != nil {
					return nil, fmt.Errorf("spike: decode decision: %w", err)
				}
				if d.Decision != "approve" {
					return nil, fmt.Errorf("spike: refused: decision was %q", d.Decision)
				}
				// ⚠️ THE DELAY WIDENS A REAL WINDOW; IT DOES NOT INVENT ONE.
				// Run checks the store for a completed "commit" and only then
				// executes. Between that check and Append there is an interval
				// in which a second process makes the same check and sees the
				// same absence. That interval exists at every commit; it is
				// normally sub-millisecond, so an undelayed race reproduces only
				// occasionally and a flaky demonstration is not a measurement.
				// Set only by TestConcurrentPollers..., never by the gate tests.
				if d := os.Getenv(spikeDelayEnv); d != "" {
					if dur, err := time.ParseDuration(d); err == nil {
						time.Sleep(dur)
					}
				}
				if err := recordExec("commit"); err != nil {
					return nil, err
				}
				return json.Marshal(map[string]string{"voucher_id": "v-" + runID})
			},
		},
		{
			Name: "notify",
			Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
				if err := recordExec("notify"); err != nil {
					return nil, err
				}
				return json.Marshal(map[string]string{"sent": "true"})
			},
		},
	}
}

// ---------------------------------------------------------------------------
// The child: one epoch = one `cored` lifetime.
// ---------------------------------------------------------------------------

// TestSpikeChildEpoch is not a test. It is the body of a separate process that
// the parent re-executes once per epoch; `go test` reaches it only when the
// parent set KHATA_SPIKE_CHILD.
//
// ⚠️ IT SKIPS SILENTLY IN A NORMAL RUN AND THAT IS CORRECT HERE, unlike the
// t.Skip trap in HANDOFF §7.2: nothing about the gate is asserted in this
// function. The assertions are the parent's, and the parent FAILS if this
// process does not print its sentinel — so a child that silently did nothing
// cannot produce a green parent.
func TestSpikeChildEpoch(t *testing.T) {
	if os.Getenv(spikeChildEnv) == "" {
		t.Skip("not the spike child process (parent sets " + spikeChildEnv + ")")
	}
	runID := os.Getenv(spikeRunEnv)
	if runID == "" {
		t.Fatalf("%s is empty in the child", spikeRunEnv)
	}
	now, err := time.Parse(time.RFC3339, os.Getenv(spikeNowEnv))
	if err != nil {
		t.Fatalf("child clock %q: %v", os.Getenv(spikeNowEnv), err)
	}

	pool, err := spikeAppPool()
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	defer pool.Close()
	store := NewPGStore(pool)

	recordExec := func(step string) error {
		// A UNIQUE name per execution — see the file header on why a fixed name
		// would collapse repeats and fake the result.
		return store.Append(spikeExecWorkflowID(runID), StepResult{
			Name:   step + "@" + uuid.NewString(),
			Output: json.RawMessage(`{"at":"` + now.UTC().Format(time.RFC3339) + `"}`),
		})
	}

	// ⛔ THE PID IS THE PROOF THAT AN EPOCH IS A REAL RESTART. The parent asserts
	// all epochs ran under DISTINCT pids; without it "restart" is a claim in a
	// comment, and a refactor that quietly ran the epochs in-process would leave
	// this whole spike green while testing nothing it says it tests.
	fmt.Println(spikePIDPfx + fmt.Sprint(os.Getpid()))

	// The F5 fix under test: a per-run advisory lock, held for the whole Run on
	// ONE connection. pg_try_advisory_lock is non-blocking — a poller that does
	// not get the lock leaves the run to whoever holds it and comes back on its
	// next tick, which is the correct behaviour for a poller and requires no
	// new table and no new dependency (§1.1 intact).
	if os.Getenv(spikeLockEnv) != "" {
		conn, err := pool.Acquire(context.Background())
		if err != nil {
			t.Fatalf("child: acquire for advisory lock: %v", err)
		}
		defer conn.Release()
		var got bool
		if err := conn.QueryRow(context.Background(),
			`SELECT pg_try_advisory_lock(hashtext($1))`, spikeRunWorkflowID(runID)).Scan(&got); err != nil {
			t.Fatalf("child: advisory lock: %v", err)
		}
		if !got {
			fmt.Println(spikeSentinelPfx + "LOCKED")
			return
		}
		defer func() {
			_, _ = conn.Exec(context.Background(),
				`SELECT pg_advisory_unlock(hashtext($1))`, spikeRunWorkflowID(runID))
		}()
	}

	_, runErr := Run(context.Background(), store, spikeRunWorkflowID(runID),
		spikeSteps(store, runID, now, recordExec))

	switch {
	case runErr == nil:
		fmt.Println(spikeSentinelPfx + "COMPLETED")
	case errors.Is(runErr, errParked):
		fmt.Println(spikeSentinelPfx + "PARKED")
	case errors.Is(runErr, errApprovalExpired):
		fmt.Println(spikeSentinelPfx + "EXPIRED")
	default:
		fmt.Println(spikeSentinelPfx + "FAILED:" + runErr.Error())
	}
}

// ---------------------------------------------------------------------------
// The parent: the gate.
// ---------------------------------------------------------------------------

// TestDurableParkResumeSurvivesProcessRestart is the W2 gate clause, run.
func TestDurableParkResumeSurvivesProcessRestart(t *testing.T) {
	admin := spikeHarness(t)
	pids := &spikePIDs{}
	runID := uuid.NewString()
	t.Cleanup(func() { spikeCleanup(admin, runID) })

	day0 := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)

	// --- Epoch 1: a fresh `cored` starts the run. It parks. -----------------
	if got := spikeEpoch(t, pids, runID, day0); got != "PARKED" {
		t.Fatalf("epoch 1: got %q, want PARKED", got)
	}
	assertRecordedSteps(t, admin, runID, "plan", "preview")
	assertExecCounts(t, admin, runID, map[string]int{"plan": 1, "preview": 1, "commit": 0, "notify": 0})

	// --- Epoch 2: THREE DAYS LATER, after a restart. Still parked. ----------
	// ⛔ THE PROCESS FROM EPOCH 1 IS GONE. If park state lived in memory — a
	// channel, a timer, a goroutine blocked on Recv — this epoch is where it
	// would be lost. The assertion that plan and preview did NOT re-execute is
	// what proves the run resumed rather than restarted.
	day3 := day0.Add(71 * time.Hour) // inside the 72h window, only just
	if got := spikeEpoch(t, pids, runID, day3); got != "PARKED" {
		t.Fatalf("epoch 2 (71h later, after restart): got %q, want PARKED", got)
	}
	assertExecCounts(t, admin, runID, map[string]int{"plan": 1, "preview": 1, "commit": 0, "notify": 0})

	// --- The human approves, in a different process entirely. ---------------
	spikeRecordDecision(t, admin, runID, decisionOutput{
		Decision: "approve", By: "u-owner", At: day3.Add(30 * time.Minute),
	})

	// --- Epoch 3: the poller re-invokes. The run completes. -----------------
	if got := spikeEpoch(t, pids, runID, day3.Add(time.Hour)); got != "COMPLETED" {
		t.Fatalf("epoch 3: got %q, want COMPLETED", got)
	}
	assertRecordedSteps(t, admin, runID, "plan", "preview", "await_approval", "commit", "notify")
	assertExecCounts(t, admin, runID, map[string]int{"plan": 1, "preview": 1, "commit": 1, "notify": 1})

	// --- Epoch 4: the poller fires again on a completed run. ----------------
	// ⛔ THIS IS THE EXACTLY-ONCE ASSERTION AND IT IS NOT REDUNDANT. A poller
	// re-invokes; at-least-once delivery is the normal shape. commit must still
	// be 1.
	if got := spikeEpoch(t, pids, runID, day3.Add(2*time.Hour)); got != "COMPLETED" {
		t.Fatalf("epoch 4: got %q, want COMPLETED", got)
	}
	assertExecCounts(t, admin, runID, map[string]int{"plan": 1, "preview": 1, "commit": 1, "notify": 1})

	if len(pids.seen) != 4 {
		t.Fatalf("ran %d epochs, expected 4", len(pids.seen))
	}
	// ⛔ THE VERDICT LINE IS GUARDED BY t.Failed(). An earlier version printed
	// it unconditionally, so a run with "commit executed 2 time(s)" above it
	// still ended with "commit executed exactly once" — a spike whose summary
	// contradicts its own assertions is worse than one with no summary, because
	// the summary is the line that gets quoted into a decision document.
	// Measured: the mutation that broke exactly-once printed both.
	if t.Failed() {
		t.Fatal("VERDICT WITHHELD: assertions above failed; this design did NOT satisfy the gate in this run")
	}
	t.Logf("VERDICT: a %v approval survived 4 DISTINCT process lifetimes (pids %v); commit executed exactly once", approvalWindow, pids.seen)
}

// TestDurableParkExpiresAfterTheWindow is the "days-long timeout" half.
func TestDurableParkExpiresAfterTheWindow(t *testing.T) {
	admin := spikeHarness(t)
	pids := &spikePIDs{}
	runID := uuid.NewString()
	t.Cleanup(func() { spikeCleanup(admin, runID) })

	day0 := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	if got := spikeEpoch(t, pids, runID, day0); got != "PARKED" {
		t.Fatalf("epoch 1: got %q, want PARKED", got)
	}

	// One second past the 72h window, in a new process.
	if got := spikeEpoch(t, pids, runID, day0.Add(approvalWindow+time.Second)); got != "EXPIRED" {
		t.Fatalf("after the window: got %q, want EXPIRED", got)
	}
	// ⛔ AN EXPIRED RUN MUST NOT HAVE COMMITTED ANYTHING.
	assertExecCounts(t, admin, runID, map[string]int{"plan": 1, "preview": 1, "commit": 0, "notify": 0})

	// ⚠️ FINDING (F2): EXPIRY IS NOT TERMINAL IN THIS DESIGN. The expired step
	// recorded nothing, so the run is still resumable — approve it late and the
	// next poll commits. Proved here rather than asserted, because it is the
	// sharpest edge of "a parked run is just an unrecorded step": there is no
	// state that says "this run is dead". W2 must make expiry terminal by
	// WRITING something (a rejected decision, or a terminal step), not by
	// letting the poller stop caring.
	spikeRecordDecision(t, admin, runID, decisionOutput{
		Decision: "approve", By: "u-owner", At: day0.Add(96 * time.Hour),
	})
	if got := spikeEpoch(t, pids, runID, day0.Add(97*time.Hour)); got != "COMPLETED" {
		t.Fatalf("F2 probe: got %q, want COMPLETED (an expired run is still resumable)", got)
	}
	assertExecCounts(t, admin, runID, map[string]int{"commit": 1})
	t.Log("F2 CONFIRMED: expiry left no record, so a late approval still committed. W2 must write a terminal decision.")
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// spikeHarness follows the FIXED database idiom (HANDOFF §7.2,
// handler_itc_test.go:659): CORED_SKIP_DB is an intentional skip, but a database
// that is unreachable while it is unset is a FAILURE. A spike whose verdict came
// from a skipped run would be the worst possible outcome here — it decides
// whether this repo takes a workflow-engine dependency.
func spikeHarness(t *testing.T) *pgx.Conn {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, statAdminDSN)
	if err != nil {
		t.Fatalf("admin postgres unavailable and CORED_SKIP_DB is unset: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	if !statTablePresent(ctx, admin, "statutory_step") {
		t.Fatal("migration 0013 (statutory_step) is not applied; run `make migrate`")
	}
	return admin
}

// spikePIDs records every epoch pid seen by one test, so distinctness is
// checkable at the end.
type spikePIDs struct {
	seen []int
}

func (p *spikePIDs) add(t *testing.T, pid int) {
	t.Helper()
	for _, prev := range p.seen {
		if prev == pid {
			t.Fatalf("epoch reused pid %d: these are NOT separate processes, so nothing about surviving a restart has been tested", pid)
		}
	}
	p.seen = append(p.seen, pid)
}

// spikeEpoch runs ONE process lifetime and returns its sentinel.
func spikeEpoch(t *testing.T, pids *spikePIDs, runID string, now time.Time, extraEnv ...string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSpikeChildEpoch$", "-test.v")
	cmd.Env = append(os.Environ(),
		spikeChildEnv+"=1",
		spikeRunEnv+"="+runID,
		spikeNowEnv+"="+now.UTC().Format(time.RFC3339),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		t.Fatalf("epoch process failed: %v\n%s", err, text)
	}
	sentinel := ""
	pid := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, spikeSentinelPfx) {
			sentinel = strings.TrimPrefix(line, spikeSentinelPfx)
		}
		if strings.HasPrefix(line, spikePIDPfx) {
			pid, _ = strconv.Atoi(strings.TrimPrefix(line, spikePIDPfx))
		}
	}
	if pid == 0 {
		t.Fatalf("epoch process printed no %s line:\n%s", spikePIDPfx, text)
	}
	pids.add(t, pid)
	if sentinel != "" {
		return sentinel
	}
	// ⛔ NO SENTINEL = NO RESULT. Never treat a silent child as a pass; that is
	// how a spike concludes that a design works because its probe never ran.
	t.Fatalf("epoch process printed no %s line:\n%s", spikeSentinelPfx, text)
	return ""
}

func spikeRecordDecision(t *testing.T, admin *pgx.Conn, runID string, d decisionOutput) {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal decision: %v", err)
	}
	_, err = admin.Exec(context.Background(), `
		INSERT INTO statutory_step (workflow_id, step_name, output)
		VALUES ($1, 'decision', $2)
		ON CONFLICT (workflow_id, step_name) DO NOTHING`,
		spikeApprovalWorkflowID(runID), raw)
	if err != nil {
		t.Fatalf("record decision: %v", err)
	}
}

// assertRecordedSteps checks the run's recorded step names, in order, read
// straight from the table rather than through PGStore — an independent oracle,
// matching pgstore_test.go's convention.
func assertRecordedSteps(t *testing.T, admin *pgx.Conn, runID string, want ...string) {
	t.Helper()
	rows, err := admin.Query(context.Background(),
		`SELECT step_name FROM statutory_step WHERE workflow_id = $1 ORDER BY seq`,
		spikeRunWorkflowID(runID))
	if err != nil {
		t.Fatalf("read recorded steps: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan step: %v", err)
		}
		got = append(got, n)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("recorded steps = %v, want %v", got, want)
	}
}

// assertExecCounts checks how many times each step ACTUALLY executed.
func assertExecCounts(t *testing.T, admin *pgx.Conn, runID string, want map[string]int) {
	t.Helper()
	for step, n := range want {
		var got int
		err := admin.QueryRow(context.Background(),
			`SELECT count(*) FROM statutory_step WHERE workflow_id = $1 AND step_name LIKE $2`,
			spikeExecWorkflowID(runID), step+"@%").Scan(&got)
		if err != nil {
			t.Fatalf("count executions of %q: %v", step, err)
		}
		if got != n {
			t.Errorf("step %q executed %d time(s), want %d", step, got, n)
		}
	}
}

func spikeCleanup(admin *pgx.Conn, runID string) {
	ctx := context.Background()
	for _, wf := range []string{
		spikeRunWorkflowID(runID), spikeApprovalWorkflowID(runID), spikeExecWorkflowID(runID),
	} {
		_, _ = admin.Exec(ctx, `DELETE FROM statutory_step WHERE workflow_id = $1`, wf)
	}
}

// spikeAppPool is the child's connection: the APP role (NOBYPASSRLS), which is
// what cored itself would use. It returns an error rather than taking a *testing.T
// because the child reports through its sentinel, not through the parent's t.
func spikeAppPool() (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(context.Background(), statAppDSN)
	if err != nil {
		return nil, fmt.Errorf("spike: app pool: %w", err)
	}
	return pool, nil
}

// TestConcurrentPollersDoubleExecuteTheMoneyStep is FINDING F5, measured.
//
// ⛔⛔ THIS TEST ASSERTS A DEFECT, NOT A GUARANTEE. It is green when the money
// step runs TWICE, because that is what this design does today under two
// concurrent pollers — and W2 must not adopt it without the fix below.
//
// PGStore's own header says so in prose ("Append takes NO row lock ... two
// statutoryd instances sharing this DSN could both execute the `generate` step
// and call the real IRP twice") and then discharges it with a DEPLOYMENT
// assumption: "Single-instance statutoryd remains the deployment shape
// (IMPLEMENTATION_PLAN.md §7 deferral 7)".
//
// ⛔ THAT ASSUMPTION DOES NOT CARRY OVER TO W2, AND THAT IS THE WHOLE POINT OF
// THIS TEST. statutoryd is one process by choice. The agent plane runs inside
// **cored**, which is a horizontally-scaled HTTP server — the moment two cored
// instances poll the same parked run, both pass the memoisation check, both
// execute `commit`, and the loser's Append is silently swallowed by ON CONFLICT
// DO NOTHING. The books get two vouchers and the step store shows one. A defect
// that is invisible in the ledger this engine keeps is the worst shape it could
// have.
//
// THE FIX W2 OWES (do not adopt this design without it): take a per-run
// PostgreSQL advisory lock — pg_try_advisory_xact_lock(hashtext(workflow_id)) —
// or a SELECT ... FOR UPDATE lease row, around the whole Run, and skip the run
// when the lock is not granted. Advisory locks need no new table and no new
// dependency, which keeps §1.1 intact.
func TestConcurrentPollersDoubleExecuteTheMoneyStep(t *testing.T) {
	admin := spikeHarness(t)
	pids := &spikePIDs{}
	runID := uuid.NewString()
	t.Cleanup(func() { spikeCleanup(admin, runID) })

	day0 := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	if got := spikeEpoch(t, pids, runID, day0); got != "PARKED" {
		t.Fatalf("setup: got %q, want PARKED", got)
	}
	spikeRecordDecision(t, admin, runID, decisionOutput{Decision: "approve", By: "u-owner", At: day0})

	// Two pollers wake on the same approved run at the same instant.
	const delay = "400ms"
	results := make(chan string, 2)
	for i := 0; i < 2; i++ {
		go func() {
			// Each child is a separate process, so they cannot share a mutex
			// even in principle — exactly the two-cored-instances shape.
			cmd := exec.Command(os.Args[0], "-test.run=^TestSpikeChildEpoch$", "-test.v")
			cmd.Env = append(os.Environ(),
				spikeChildEnv+"=1",
				spikeRunEnv+"="+runID,
				spikeNowEnv+"="+day0.Add(time.Hour).UTC().Format(time.RFC3339),
				spikeDelayEnv+"="+delay,
			)
			out, _ := cmd.CombinedOutput()
			for _, line := range strings.Split(string(out), "\n") {
				if line = strings.TrimSpace(line); strings.HasPrefix(line, spikeSentinelPfx) {
					results <- strings.TrimPrefix(line, spikeSentinelPfx)
					return
				}
			}
			results <- "NO-SENTINEL"
		}()
	}
	for i := 0; i < 2; i++ {
		if got := <-results; got != "COMPLETED" {
			t.Fatalf("concurrent poller %d: got %q, want COMPLETED", i, got)
		}
	}

	var commits int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM statutory_step WHERE workflow_id = $1 AND step_name LIKE 'commit@%'`,
		spikeExecWorkflowID(runID)).Scan(&commits); err != nil {
		t.Fatalf("count commits: %v", err)
	}

	// The recorded run still shows ONE commit step — which is precisely why this
	// is dangerous: the durable record does not reveal the double execution.
	var recorded int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM statutory_step WHERE workflow_id = $1 AND step_name = 'commit'`,
		spikeRunWorkflowID(runID)).Scan(&recorded); err != nil {
		t.Fatalf("count recorded commit: %v", err)
	}
	if recorded != 1 {
		t.Errorf("recorded commit steps = %d, want 1", recorded)
	}

	switch commits {
	case 2:
		t.Logf("F5 CONFIRMED: two concurrent pollers executed the money step %d times while the step store recorded %d. W2 must take a per-run advisory lock before adopting this design.", commits, recorded)
	case 1:
		t.Logf("F5 NOT REPRODUCED in this run (commits=%d): the race did not open. This does NOT clear the design — the window is real and unserialized; see the header. Re-run or widen %s.", commits, spikeDelayEnv)
	default:
		t.Fatalf("unexpected commit execution count %d", commits)
	}
}

// TestAdvisoryLockPreventsDoubleExecution is the F5 FIX, measured under exactly
// the conditions that broke without it.
//
// Same two concurrent pollers, same widened commit window, same run — the only
// difference is that each child takes pg_try_advisory_lock(hashtext(workflow_id))
// before Run and releases it after. One poller does the work; the other is told
// LOCKED and leaves. ⛔ The assertion is commits == 1 EXACTLY: a fix that merely
// made the race rarer would pass an "is it usually 1?" test and fail in
// production at volume.
func TestAdvisoryLockPreventsDoubleExecution(t *testing.T) {
	admin := spikeHarness(t)
	pids := &spikePIDs{}
	runID := uuid.NewString()
	t.Cleanup(func() { spikeCleanup(admin, runID) })

	day0 := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	if got := spikeEpoch(t, pids, runID, day0, spikeLockEnv+"=1"); got != "PARKED" {
		t.Fatalf("setup: got %q, want PARKED", got)
	}
	spikeRecordDecision(t, admin, runID, decisionOutput{Decision: "approve", By: "u-owner", At: day0})

	results := make(chan string, 2)
	for i := 0; i < 2; i++ {
		go func() {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSpikeChildEpoch$", "-test.v")
			cmd.Env = append(os.Environ(),
				spikeChildEnv+"=1",
				spikeRunEnv+"="+runID,
				spikeNowEnv+"="+day0.Add(time.Hour).UTC().Format(time.RFC3339),
				spikeDelayEnv+"=400ms",
				spikeLockEnv+"=1",
			)
			out, _ := cmd.CombinedOutput()
			for _, line := range strings.Split(string(out), "\n") {
				if line = strings.TrimSpace(line); strings.HasPrefix(line, spikeSentinelPfx) {
					results <- strings.TrimPrefix(line, spikeSentinelPfx)
					return
				}
			}
			results <- "NO-SENTINEL"
		}()
	}
	var got []string
	for i := 0; i < 2; i++ {
		got = append(got, <-results)
	}

	var commits int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM statutory_step WHERE workflow_id = $1 AND step_name LIKE 'commit@%'`,
		spikeExecWorkflowID(runID)).Scan(&commits); err != nil {
		t.Fatalf("count commits: %v", err)
	}
	if commits != 1 {
		t.Fatalf("the money step executed %d time(s) under the advisory lock, want exactly 1 (pollers reported %v)", commits, got)
	}

	// ⚠️ ONE POLLER MUST HAVE BEEN TURNED AWAY. Without this, a run where the
	// two children simply did not overlap would also show commits == 1 and the
	// lock would be credited with a result it did not produce — the same
	// "green because nothing happened" failure the vacuity guards elsewhere in
	// this repo exist to catch.
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"COMPLETED", "LOCKED"}) {
		t.Fatalf("pollers reported %v, want one COMPLETED and one LOCKED; the two did not actually contend, so this run does not demonstrate the fix", got)
	}
	t.Logf("F5 FIX VERIFIED: under contention (%v) the money step executed exactly once", got)
}
