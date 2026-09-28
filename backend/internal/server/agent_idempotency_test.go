package server_test

// agent_idempotency_test.go — the commit idempotency key (PLAN-AGENTIC W2
// "Idempotency key on commit"; W5's Bill Inbox duplicate-post class).
//
// The durable engine makes exec:k run once per RECORDED step, but there is a
// window it cannot close by itself: the voucher commits, then the process dies
// before exec:k's output is appended, so the resumed run executes exec:k AGAIN.
// The dispatcher closes it twice over: a pre-check answers a known key from
// agent_commit without running the handler, and — for the race past the pre-
// check — the agent_commit row is inserted INSIDE the voucher's own
// transaction, so a second insert of the same key rolls its voucher back.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/policy"
	"github.com/khata/cored/internal/server"
)

func TestAgentCommitIsIdempotentByKey(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	// A real run, because agent_commit references agent_run.
	model := scriptedModel(t, func(*testing.T, int, []json.RawMessage) agent.ModelDecision { return finish("noop") })
	srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t)})
	runID := uuid.MustParse(startRun(t, srv, b, map[string]any{"instruction": "noop", "delegate": []string{"create payment"}}))
	cid := uuid.MustParse(b.companyID)
	caller := server.AgentCaller(cid, runID, uuid.New(), ownerSubject(t, srv), []string{"create payment"})
	spec, _ := actions.ByID("post.vouchers.payment")
	body, _ := json.Marshal(voucherBody("2025-05-10", "idempotent rent",
		[3]string{b.ledgers["Office Rent"], "D", "100.00"}, [3]string{b.ledgers["HDFC Bank"], "C", "100.00"}))
	req := func(key string) agent.Request {
		return agent.Request{Mode: agent.ModeCommit, Spec: spec, Path: b.base + "/vouchers/payment", Body: body,
			IdempotencyKey: key, RunID: runID}
	}

	// The re-execution after a crash: same key, twice, in sequence.
	first, err := server.AgentDispatch(context.Background(), srv, caller, req("k-seq"))
	if err != nil || first.Status != http.StatusCreated || len(first.VoucherIDs) != 1 {
		t.Fatalf("first commit: %+v %v", first, err)
	}
	second, err := server.AgentDispatch(context.Background(), srv, caller, req("k-seq"))
	if err != nil || !second.Replayed || len(second.VoucherIDs) != 1 || second.VoucherIDs[0] != first.VoucherIDs[0] {
		t.Fatalf("second commit with the same key: %+v %v — want a replay naming the first voucher", second, err)
	}
	if n := countVouchers(t, b.companyID); n != 1 {
		t.Fatalf("vouchers after a repeated key = %d, want 1", n)
	}

	// The race: eight concurrent dispatches of one new key.
	//
	// Mutation proofs (run for D121): make the hook's INSERT "ON CONFLICT DO
	// NOTHING" and this reports 9 vouchers — the pre-check alone cannot stop
	// concurrent callers that all read "absent"; it is the insert FAILING inside
	// the voucher's own transaction that rolls the duplicates back. Replace the
	// insert with a no-op and the SEQUENTIAL half above fails first.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = server.AgentDispatch(context.Background(), srv, caller, req("k-race"))
		}()
	}
	wg.Wait()
	if n := countVouchers(t, b.companyID); n != 2 {
		t.Fatalf("vouchers after 8 concurrent commits of one key = %d, want 2 (one per key)", n)
	}
	if n := agentCommits(t, b.companyID); n != 2 {
		t.Fatalf("agent_commit rows = %d, want 2", n)
	}
}

// TestAgentPreviewLeavesNoTrace: a preview runs the real handler and rolls it
// back — no voucher, no posting, and the NEXT real voucher takes the number
// the preview showed (the counter rolled back too; a gap in a GST invoice
// series is itself a compliance defect).
func TestAgentPreviewLeavesNoTrace(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	model := scriptedModel(t, func(*testing.T, int, []json.RawMessage) agent.ModelDecision { return finish("noop") })
	srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t)})
	runID := uuid.MustParse(startRun(t, srv, b, map[string]any{"instruction": "noop", "delegate": []string{"create payment"}}))
	cid := uuid.MustParse(b.companyID)
	caller := server.AgentCaller(cid, runID, uuid.New(), ownerSubject(t, srv), []string{"create payment"})
	spec, _ := actions.ByID("post.vouchers.payment")
	body, _ := json.Marshal(voucherBody("2025-05-10", "preview only",
		[3]string{b.ledgers["Office Rent"], "D", "1180.00"}, [3]string{b.ledgers["HDFC Bank"], "C", "1180.00"}))
	res, err := server.AgentDispatch(context.Background(), srv, caller, agent.Request{Mode: agent.ModePreview, Spec: spec,
		Path: b.base + "/vouchers/payment", Body: body, RunID: runID})
	if err != nil || res.Status != http.StatusCreated || res.Effect == nil {
		t.Fatalf("preview: %+v %v", res, err)
	}
	if res.Effect.DebitPaise != 118000 || res.Effect.CreditPaise != 118000 || len(res.Effect.Postings) != 2 {
		t.Fatalf("preview effect %+v, want Dr 118000 / Cr 118000 paise over 2 postings", res.Effect)
	}
	var shown struct {
		VoucherNo string `json:"voucher_no"`
	}
	_ = json.Unmarshal(res.Body, &shown)
	if n := countVouchers(t, b.companyID); n != 0 {
		t.Fatalf("a preview left %d voucher(s)", n)
	}
	rec, human := do(t, srv, http.MethodPost, b.base+"/vouchers/payment", voucherBody("2025-05-10", "real",
		[3]string{b.ledgers["Office Rent"], "D", "1180.00"}, [3]string{b.ledgers["HDFC Bank"], "C", "1180.00"}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("human post: %d %s", rec.Code, rec.Body.String())
	}
	if shown.VoucherNo == "" || human["voucher_no"] != shown.VoucherNo {
		t.Fatalf("the preview showed voucher_no %q but the next real voucher took %v: the counter did not roll back", shown.VoucherNo, human["voucher_no"])
	}
}

// TestAgentCommitBookkeepingFailureIsRetriedNotReplayed: the agent_commit
// INSERT fails for a reason that is NOT a duplicate key (a trigger raising
// serialization_failure stands in for a deadlock or a statement timeout). The
// voucher's transaction rolls back — and the run must NOT record a replayed
// success with no voucher behind it (the wave-1 review's finding against
// agent_dispatch.go's dupKey=true-on-any-error). The step is left unrecorded,
// and a retry once the fault clears commits exactly one voucher.
func TestAgentCommitBookkeepingFailureIsRetriedNotReplayed(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	setAutonomy(t, b.companyID, "create payment", policy.L3)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	fn := "agent_commit_fault_" + strings.ReplaceAll(b.companyID, "-", "")[:12]
	if _, err := admin.Exec(ctx, `CREATE FUNCTION `+fn+`() RETURNS trigger AS $$
		BEGIN
			IF NEW.company_id = '`+b.companyID+`'::uuid THEN
				RAISE EXCEPTION 'injected fault' USING ERRCODE = '40001';
			END IF;
			RETURN NEW;
		END $$ LANGUAGE plpgsql`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE TRIGGER `+fn+` BEFORE INSERT ON agent_commit FOR EACH ROW EXECUTE FUNCTION `+fn+`()`); err != nil {
		t.Fatal(err)
	}
	dropped := false
	drop := func() {
		if !dropped {
			_, _ = admin.Exec(ctx, `DROP TRIGGER IF EXISTS `+fn+` ON agent_commit`)
			_, _ = admin.Exec(ctx, `DROP FUNCTION IF EXISTS `+fn+`()`)
			dropped = true
		}
	}
	defer drop()

	model := scriptedModel(t, paymentScript)
	srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t)})
	runID := startRun(t, srv, b, map[string]any{"instruction": cassetteCases[0].instruction, "delegate": []string{"create payment"}})
	pollUntilQuiet(t, srv)
	if n := countVouchers(t, b.companyID); n != 0 {
		t.Fatalf("%d voucher(s) survived a failed commit record", n)
	}
	kinds := intentKinds(t, b.companyID, runID)
	if strings.Contains(kinds, ",commit") || getRun(t, srv, b, runID)["status"] == "completed" {
		t.Fatalf("a rolled-back commit was recorded as a success: status %v, intents %s", getRun(t, srv, b, runID)["status"], kinds)
	}

	// The fault clears; the retry (same idempotency key) commits exactly once.
	drop()
	if _, err := admin.Exec(ctx, `UPDATE agent_run SET next_poll_at = now() - interval '1 second' WHERE id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	pollUntilQuiet(t, srv)
	if n := countVouchers(t, b.companyID); n != 1 {
		t.Fatalf("after the fault cleared: %d voucher(s), want 1", n)
	}
	if st := getRun(t, srv, b, runID)["status"]; st != "completed" {
		t.Fatalf("status %v", st)
	}
}
