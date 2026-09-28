package server_test

// agent_restart_test.go — the W2 durability gate, run for real: "a three-day
// approval survives a cored restart" (PLAN-AGENTIC §3 W2).
//
// ⛔ THE RESTARTS ARE REAL PROCESSES, as in D120's spike, whose technique this
// reuses: the test re-executes its own binary once per epoch, and the child
// shares NOTHING with the parent or with earlier children except Postgres and
// the cassette file on disk. Each child builds a whole cored Server (router,
// PDP, engine, poller), polls once under a driven clock, prints its pid and a
// sentinel, and exits. The pids are asserted distinct, so a refactor that ran
// the epochs in one process could not leave this green.
//
// Unlike the spike, the steps here are the REAL agent: the model is the
// cassette-replayed payment conversation, the preview and the commit go
// through the real router and handler, the park is the real await step on the
// real agent_decision table, and exactly-once is measured on voucher_head and
// agent_commit — the books, not the step store.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
)

const (
	agentChildEnv      = "KHATA_AGENT_CHILD"
	agentChildNowEnv   = "KHATA_AGENT_NOW"
	agentChildRunEnv   = "KHATA_AGENT_RUN"
	agentChildSentinel = "AGENT_RESULT="
	agentChildPIDPfx   = "AGENT_PID="
)

// TestAgentRestartChild is not a test: it is one cored lifetime, run only when
// the parent sets KHATA_AGENT_CHILD. The parent FAILS if no sentinel is
// printed, so a child that silently did nothing cannot produce a green parent.
func TestAgentRestartChild(t *testing.T) {
	if os.Getenv(agentChildEnv) == "" {
		t.Skip("not an agent restart child (the parent sets " + agentChildEnv + ")")
	}
	now, err := time.Parse(time.RFC3339, os.Getenv(agentChildNowEnv))
	if err != nil {
		t.Fatalf("child clock: %v", err)
	}
	fmt.Println(agentChildPIDPfx + strconv.Itoa(os.Getpid()))
	srv := newServer(t)
	model := cassetteModel(t, "payment-rent-hdfc", paymentScript)
	srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t), Now: func() time.Time { return now }})
	if _, err := srv.AgentPollOnce(context.Background()); err != nil {
		t.Fatalf("child poll: %v", err)
	}
	status := runStatusFromDB(t, os.Getenv(agentChildRunEnv))
	fmt.Printf("%s%s calls=%d\n", agentChildSentinel, status, model.calls)
}

func runStatusFromDB(t *testing.T, runID string) string {
	t.Helper()
	c, err := pgx.Connect(context.Background(), adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	var st string
	if err := c.QueryRow(context.Background(), `SELECT status FROM agent_run WHERE id = $1`, runID).Scan(&st); err != nil {
		t.Fatalf("read run status: %v", err)
	}
	return st
}

type epochResult struct {
	status string
	calls  int
	pid    int
}

// agentEpoch runs one child process at `now` and returns what it reported.
func agentEpoch(t *testing.T, runID string, now time.Time, pids map[int]bool) epochResult {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAgentRestartChild$", "-test.v", "-test.count=1")
	env := []string{}
	for _, kv := range os.Environ() {
		// A child never records: it replays the checked-in cassette.
		if strings.HasPrefix(kv, "TRAJECTORY_RECORD=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, agentChildEnv+"=1", agentChildRunEnv+"="+runID,
		agentChildNowEnv+"="+now.UTC().Format(time.RFC3339))
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		t.Fatalf("epoch process failed: %v\n%s", err, text)
	}
	var r epochResult
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, agentChildPIDPfx) {
			r.pid, _ = strconv.Atoi(strings.TrimPrefix(line, agentChildPIDPfx))
		}
		if strings.HasPrefix(line, agentChildSentinel) {
			f := strings.Fields(strings.TrimPrefix(line, agentChildSentinel))
			r.status = f[0]
			if len(f) > 1 {
				r.calls, _ = strconv.Atoi(strings.TrimPrefix(f[1], "calls="))
			}
		}
	}
	if r.pid == 0 || r.status == "" {
		t.Fatalf("epoch process printed no pid/sentinel:\n%s", text)
	}
	if pids[r.pid] || r.pid == os.Getpid() {
		t.Fatalf("epoch reused pid %d: these are NOT separate processes, so nothing about surviving a restart was tested", r.pid)
	}
	pids[r.pid] = true
	return r
}

// TestAgentThreeDayApprovalSurvivesRestarts is the gate.
func TestAgentThreeDayApprovalSurvivesRestarts(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	clock := &fixedClock{t: day0}
	model := cassetteModel(t, "payment-rent-hdfc", paymentScript)
	srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t), Now: clock.Now})
	runID := startRun(t, srv, b, map[string]any{
		"instruction": cassetteCases[0].instruction, "delegate": cassetteCases[0].delegate})
	pids := map[int]bool{}

	// Epoch 1 — day 0: a fresh cored runs the agent to the approval and parks.
	e := agentEpoch(t, runID, day0, pids)
	if e.status != "parked" || e.calls != 4 {
		t.Fatalf("epoch 1: %+v, want parked after 4 model calls (search, read, preview, commit)", e)
	}
	if n := countVouchers(t, b.companyID); n != 0 {
		t.Fatalf("epoch 1: %d vouchers before approval", n)
	}

	// Epoch 2 — 71 hours later, after a restart: still parked, and the model
	// was asked NOTHING — every recorded step was memoised, not re-run.
	e = agentEpoch(t, runID, day0.Add(71*time.Hour), pids)
	if e.status != "parked" || e.calls != 0 {
		t.Fatalf("epoch 2 (+71h, new process): %+v, want parked with 0 model calls", e)
	}

	// The human approves, in yet another process (this one), at +71h30m.
	clock.Set(day0.Add(71*time.Hour + 30*time.Minute))
	pend := pendingApprovals(t, srv, b, runID)
	if len(pend) != 1 {
		t.Fatalf("pending approvals = %d", len(pend))
	}
	rec, _ := do(t, srv, http.MethodPost, b.base+"/agent/approvals/"+pend[0]["id"].(string)+"/decision",
		map[string]any{"decision": "approve"})
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}

	// Epoch 3 — +72h, a fresh process: the commit runs, once; one model call
	// (the finish).
	e = agentEpoch(t, runID, day0.Add(72*time.Hour), pids)
	if e.status != "completed" || e.calls != 1 {
		t.Fatalf("epoch 3: %+v, want completed after exactly 1 model call", e)
	}

	// Epoch 4 — +73h: the poller fires again. Nothing is due; nothing runs.
	e = agentEpoch(t, runID, day0.Add(73*time.Hour), pids)
	if e.status != "completed" || e.calls != 0 {
		t.Fatalf("epoch 4: %+v, want completed with 0 calls", e)
	}

	if n := countVouchers(t, b.companyID); n != 1 {
		t.Fatalf("vouchers after four process lifetimes = %d, want EXACTLY 1", n)
	}
	if n := agentCommits(t, b.companyID); n != 1 {
		t.Fatalf("agent_commit rows = %d, want 1", n)
	}
	if len(pids) != 4 {
		t.Fatalf("ran %d distinct processes, want 4", len(pids))
	}
	_ = intentKinds(t, b.companyID, runID)
	if t.Failed() {
		t.Fatal("VERDICT WITHHELD: an assertion above failed")
	}
	t.Logf("VERDICT: a 72h approval survived 4 distinct cored processes %v; the payment voucher was committed exactly once", keys(pids))
}

// TestAgentExpiredApprovalIsTerminal is D120 F2 closed: past the window the
// poller WRITES an 'expired' decision, the run ends 'expired' without asking the
// model anything, and a late approve is refused by the one-decision UNIQUE — in
// the spike, the same late approve committed.
func TestAgentExpiredApprovalIsTerminal(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	clock := &fixedClock{t: day0}
	model := cassetteModel(t, "payment-rent-hdfc", paymentScript)
	srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t), Now: clock.Now})
	runID := startRun(t, srv, b, map[string]any{
		"instruction": cassetteCases[0].instruction, "delegate": cassetteCases[0].delegate})
	pids := map[int]bool{}

	if e := agentEpoch(t, runID, day0, pids); e.status != "parked" {
		t.Fatalf("epoch 1: %+v", e)
	}
	// One second past the 72-hour window, in a new process.
	if e := agentEpoch(t, runID, day0.Add(72*time.Hour+time.Second), pids); e.status != "expired" || e.calls != 0 {
		t.Fatalf("after the window: %+v, want expired with 0 model calls", e)
	}
	// A late approve: refused, because a decision ('expired') already exists.
	clock.Set(day0.Add(96 * time.Hour))
	rec, body := do(t, srv, http.MethodGet, b.base+"/agent/approvals?state=all&run_id="+runID, nil)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	all := body["approvals"].([]any)
	if len(all) != 1 || all[0].(map[string]any)["state"] != "expired" {
		t.Fatalf("approval state after expiry: %v", all)
	}
	id := all[0].(map[string]any)["id"].(string)
	rec, _ = do(t, srv, http.MethodPost, b.base+"/agent/approvals/"+id+"/decision", map[string]any{"decision": "approve"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "ALREADY_DECIDED") {
		t.Fatalf("late approve: %d %s, want 409 ALREADY_DECIDED", rec.Code, rec.Body.String())
	}
	if e := agentEpoch(t, runID, day0.Add(97*time.Hour), pids); e.status != "expired" || e.calls != 0 {
		t.Fatalf("after the late approve: %+v", e)
	}
	if n := countVouchers(t, b.companyID); n != 0 {
		t.Fatalf("an EXPIRED approval committed %d voucher(s) — D120 F2 is open again", n)
	}
}

// TestAgentLateApprovalIsRecordedAsExpired covers the other door into F2: the
// approve arrives after the deadline but BEFORE any poll noticed. The API
// records 'expired' (not the approve) and says so.
func TestAgentLateApprovalIsRecordedAsExpired(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	clock := &fixedClock{t: day0}
	model := cassetteModel(t, "payment-rent-hdfc", paymentScript)
	srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t), Now: clock.Now})
	runID := startRun(t, srv, b, map[string]any{
		"instruction": cassetteCases[0].instruction, "delegate": cassetteCases[0].delegate})
	pollUntilQuiet(t, srv)
	pend := pendingApprovals(t, srv, b, runID)
	if len(pend) != 1 {
		t.Fatalf("pending = %d", len(pend))
	}
	clock.Set(day0.Add(73 * time.Hour))
	rec, body := do(t, srv, http.MethodPost, b.base+"/agent/approvals/"+pend[0]["id"].(string)+"/decision",
		map[string]any{"decision": "approve"})
	if rec.Code != http.StatusConflict || body["code"] != "APPROVAL_EXPIRED" {
		t.Fatalf("late approve: %d %s, want 409 APPROVAL_EXPIRED", rec.Code, rec.Body.String())
	}
	pollUntilQuiet(t, srv)
	if st := getRun(t, srv, b, runID)["status"]; st != "expired" {
		t.Fatalf("run status %v, want expired", st)
	}
	if n := countVouchers(t, b.companyID); n != 0 {
		t.Fatalf("%d vouchers", n)
	}
	raw, _ := json.Marshal(body["approval"])
	if !strings.Contains(string(raw), `"decision":"expired"`) {
		t.Fatalf("the recorded decision is not 'expired': %s", raw)
	}
}

func keys(m map[int]bool) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	return out
}
