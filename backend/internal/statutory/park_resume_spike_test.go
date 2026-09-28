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
	spikeSentinelPfx = "SPIKE_RESULT="
	spikePIDPfx      = "SPIKE_PID="
)

// approvalWindow is the "days-long timeout" of the gate clause.
const approvalWindow = 72 * time.Hour

// F1 LANDED: the approval step parks with the engine's exported sentinel.
//
// This spike used to declare its own errParked, with a note that statutory.Run
// returned a step's error verbatim so its caller could not tell "parked,
// re-invoke me later" from "failed, page someone", and that the sentinel
// belonged beside Run as an exported ErrParked with errors.Is support. It now
// is (internal/durable, re-exported in runner.go), and the spike uses it, so the
// gate below is measured against the sentinel the agent plane's poller reads.

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
				decided, err := store.Load(context.Background(), spikeApprovalWorkflowID(runID))
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
				return nil, Park("await_approval", "no decision recorded")
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
		return store.Append(context.Background(), spikeExecWorkflowID(runID), StepResult{
			Name:   step + "@" + uuid.NewString(),
			Output: json.RawMessage(`{"at":"` + now.UTC().Format(time.RFC3339) + `"}`),
		})
	}

	// ⛔ THE PID IS THE PROOF THAT AN EPOCH IS A REAL RESTART. The parent asserts
	// all epochs ran under DISTINCT pids; without it "restart" is a claim in a
	// comment, and a refactor that quietly ran the epochs in-process would leave
	// this whole spike green while testing nothing it says it tests.
	fmt.Println(spikePIDPfx + fmt.Sprint(os.Getpid()))

	// F3 LANDED: THE CHILD TAKES NO LOCK OF ITS OWN ANY MORE. It used to
	// (behind KHATA_SPIKE_ADVISORY_LOCK), because the lock was a fix the caller
	// had to remember. Run now takes a per-workflow pg_try_advisory_lock itself
	// (StepStore.TryLock -> durable.AdvisoryLocker) and returns ErrLocked to the
	// loser, so there is no longer a way to call Run without it — which is the
	// only shape of "mandatory" that survives the next caller.
	_, runErr := Run(context.Background(), store, spikeRunWorkflowID(runID),
		spikeSteps(store, runID, now, recordExec))

	switch {
	case runErr == nil:
		fmt.Println(spikeSentinelPfx + "COMPLETED")
	case errors.Is(runErr, ErrLocked):
		fmt.Println(spikeSentinelPfx + "LOCKED")
	case errors.Is(runErr, ErrParked):
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
	// ⚠️ STILL TRUE OF THE RAW ENGINE, AND DELIBERATELY SO. An engine with no
	// clock and no policy cannot decide a run is dead; the caller that owns the
	// deadline must WRITE the terminal fact. internal/agent does — an expired
	// approval inserts an 'expired' agent_decision row that the approval step
	// reads, and the one-decision-per-approval UNIQUE refuses the late approve.
	// See internal/agent's TestExpiredApprovalIsTerminal.
	t.Log("F2 CONFIRMED at the engine: expiry left no record, so a late approval still committed. internal/agent makes it terminal by writing a decision.")
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

// TestConcurrentPollersExecuteTheMoneyStepExactlyOnce is FINDING F5 CLOSED.
//
// ⛔ IT REPLACES TestConcurrentPollersDoubleExecuteTheMoneyStep, AS THAT TEST'S
// OWN HEADER SAID IT SHOULD. That test ASSERTED THE DEFECT — it was green when
// two concurrent pollers ran the money step twice while statutory_step recorded
// it once — and said it would "go red the day someone fixes the underlying race,
// which is the correct time to delete it". The race is fixed in the engine
// (Run takes the per-workflow advisory lock before its first Load, D120 F3), so
// it went red (the second poller now answers LOCKED, not COMPLETED) and was
// deleted in the same change that made it red.
//
// Same two concurrent child PROCESSES, same run, same 400ms widening of the
// window between Run's memoisation check and its Append — and NO lock taken by
// the child. ⛔ The assertion is commits == 1 EXACTLY, and it separately requires
// that one poller was turned away with LOCKED: without that, two children that
// merely failed to overlap would also show 1 and the lock would be credited with
// a result it did not produce.
//
// Mutation proof (run for D121): delete the TryLock call from durable.Run and
// this test fails with "the money step executed 2 time(s)".
func TestConcurrentPollersExecuteTheMoneyStepExactlyOnce(t *testing.T) {
	admin := spikeHarness(t)
	pids := &spikePIDs{}
	runID := uuid.NewString()
	t.Cleanup(func() { spikeCleanup(admin, runID) })

	day0 := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	if got := spikeEpoch(t, pids, runID, day0); got != "PARKED" {
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
		t.Fatalf("the money step executed %d time(s) under two concurrent pollers, want exactly 1 (pollers reported %v)", commits, got)
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
	t.Logf("F5 CLOSED: under contention (%v), with no caller-side lock, the money step executed exactly once", got)
}
