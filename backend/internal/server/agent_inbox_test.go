package server_test

// agent_inbox_test.go — the approval inbox's four verbs (approve, edit-then-
// approve, reject, ask-back), the refusals on the decision endpoint, the
// side-panel event stream, and tenant isolation of runs and approvals.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/server"
)

// parkPayment runs the payment script (scripted, not a cassette) to its
// approval and returns the run and approval ids.
func parkPayment(t *testing.T, srv *server.Server, b agentBooks, after script) (string, string, *fakeModel) {
	t.Helper()
	s := func(t *testing.T, turn int, res []json.RawMessage) agent.ModelDecision {
		if turn >= 4 && after != nil {
			return after(t, turn, res)
		}
		return paymentScript(t, turn, res)
	}
	model := scriptedModel(t, s)
	srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t)})
	runID := startRun(t, srv, b, map[string]any{"instruction": cassetteCases[0].instruction, "delegate": []string{"create payment"}})
	pollUntilQuiet(t, srv)
	pend := pendingApprovals(t, srv, b, runID)
	if len(pend) != 1 {
		t.Fatalf("pending approvals = %d", len(pend))
	}
	return runID, pend[0]["id"].(string), model
}

func decide(t *testing.T, srv *server.Server, b agentBooks, approvalID string, body map[string]any) (int, map[string]any) {
	t.Helper()
	rec, resp := do(t, srv, http.MethodPost, b.base+"/agent/approvals/"+approvalID+"/decision", body)
	return rec.Code, resp
}

func TestAgentInboxVerbs(t *testing.T) {
	srv := newAgentServer(t)

	t.Run("reject commits nothing and hands the run back to the model", func(t *testing.T) {
		b := newAgentBooks(t, srv)
		runID, id, model := parkPayment(t, srv, b, nil)
		if code, _ := decide(t, srv, b, id, map[string]any{"decision": "reject", "note": "wrong month"}); code != 200 {
			t.Fatalf("reject: %d", code)
		}
		pollUntilQuiet(t, srv)
		if countVouchers(t, b.companyID) != 0 || getRun(t, srv, b, runID)["status"] != "completed" {
			t.Fatalf("after reject: vouchers %d, status %v", countVouchers(t, b.companyID), getRun(t, srv, b, runID)["status"])
		}
		last := model.requests[len(model.requests)-1].Messages
		if !strings.Contains(last[len(last)-1].Content, `"decision":"reject"`) {
			t.Fatalf("the model was not told of the rejection: %s", last[len(last)-1].Content)
		}
		if code, resp := decide(t, srv, b, id, map[string]any{"decision": "approve"}); code != http.StatusConflict || resp["code"] != "ALREADY_DECIDED" {
			t.Fatalf("second decision: %d %v, want 409 ALREADY_DECIDED", code, resp)
		}
	})

	t.Run("ask-back carries the question to the model", func(t *testing.T) {
		b := newAgentBooks(t, srv)
		_, id, model := parkPayment(t, srv, b, nil)
		if code, resp := decide(t, srv, b, id, map[string]any{"decision": "ask_back"}); code != http.StatusUnprocessableEntity {
			t.Fatalf("ask_back with no note: %d %v, want 422", code, resp)
		}
		if code, _ := decide(t, srv, b, id, map[string]any{"decision": "ask_back", "note": "Is this for May or June?"}); code != 200 {
			t.Fatalf("ask_back: %d", code)
		}
		pollUntilQuiet(t, srv)
		last := model.requests[len(model.requests)-1].Messages
		if !strings.Contains(last[len(last)-1].Content, "Is this for May or June?") {
			t.Fatalf("the approver's question did not reach the model: %s", last[len(last)-1].Content)
		}
		if countVouchers(t, b.companyID) != 0 {
			t.Fatal("an ask-back committed")
		}
	})

	t.Run("edit-then-approve commits the EDITED body through the same validators", func(t *testing.T) {
		b := newAgentBooks(t, srv)
		runID, id, _ := parkPayment(t, srv, b, nil)
		if code, _ := decide(t, srv, b, id, map[string]any{"decision": "reject", "edited_body": map[string]any{}}); code != http.StatusUnprocessableEntity {
			t.Fatalf("an edit with reject: %d, want 422", code)
		}
		if code, _ := decide(t, srv, b, id, map[string]any{"decision": "approve", "edited_body": []int{1}}); code != http.StatusUnprocessableEntity {
			t.Fatalf("a non-object edit: %d, want 422", code)
		}
		edited := voucherBody("2025-05-10", "Office rent for May 2025 (corrected)",
			[3]string{b.ledgers["Office Rent"], "D", "26000.00"}, [3]string{b.ledgers["HDFC Bank"], "C", "26000.00"})
		if code, resp := decide(t, srv, b, id, map[string]any{"decision": "approve", "edited_body": edited}); code != 200 {
			t.Fatalf("edit-then-approve: %d %v", code, resp)
		}
		pollUntilQuiet(t, srv)
		if countVouchers(t, b.companyID) != 1 {
			t.Fatalf("vouchers %d", countVouchers(t, b.companyID))
		}
		rec, tb := do(t, srv, http.MethodGet, b.base+"/trial-balance", nil)
		if rec.Code != 200 || tb["total_debit"] != "26000.0000" {
			t.Fatalf("the committed voucher is not the EDITED one: %v", tb["total_debit"])
		}
		_ = runID
	})

	// W2 breadth: an edited body is re-previewed at the decision, so a body
	// the validators refuse is refused THERE (422 EDIT_REFUSED, nothing
	// decided) — earlier than wave 1's commit_refused, and never smuggled.
	t.Run("an edit the validators refuse is refused at the decision, not smuggled", func(t *testing.T) {
		b := newAgentBooks(t, srv)
		runID, id, _ := parkPayment(t, srv, b, nil)
		bad := voucherBody("2025-05-10", "unbalanced",
			[3]string{b.ledgers["Office Rent"], "D", "26000.00"}, [3]string{b.ledgers["HDFC Bank"], "C", "1.00"})
		code, resp := decide(t, srv, b, id, map[string]any{"decision": "approve", "edited_body": bad})
		if code != http.StatusUnprocessableEntity || resp["code"] != "EDIT_REFUSED" {
			t.Fatalf("approve an unbalanced edit: %d %v, want 422 EDIT_REFUSED", code, resp)
		}
		pollUntilQuiet(t, srv)
		if countVouchers(t, b.companyID) != 0 {
			t.Fatal("an unbalanced edited body was committed")
		}
		if p := pendingApprovals(t, srv, b, runID); len(p) != 1 {
			t.Fatalf("the refused edit decided the approval (pending %d)", len(p))
		}
	})

	t.Run("the side panel's event stream", func(t *testing.T) {
		b := newAgentBooks(t, srv)
		runID, _, _ := parkPayment(t, srv, b, nil)
		rec, resp := do(t, srv, http.MethodGet, b.base+"/agent/runs/"+runID+"/events", nil)
		if rec.Code != 200 {
			t.Fatalf("events: %d", rec.Code)
		}
		events := resp["events"].([]any)
		if len(events) < 6 {
			t.Fatalf("events = %d", len(events))
		}
		first := events[0].(map[string]any)
		if first["type"] != "RUN_STARTED" || first["seq"].(float64) != 1 {
			t.Fatalf("first event %v", first)
		}
		var sawApproval bool
		for _, e := range events {
			m := e.(map[string]any)
			if m["type"] == "CUSTOM" && m["name"] == "approval_requested" {
				sawApproval = true
			}
		}
		if !sawApproval {
			t.Fatal("no approval_requested event for the panel")
		}
		after := int(resp["next_after"].(float64))
		rec, resp = do(t, srv, http.MethodGet, b.base+"/agent/runs/"+runID+"/events?after="+itoa(after), nil)
		if rec.Code != 200 || len(resp["events"].([]any)) != 0 || resp["status"] != "parked" {
			t.Fatalf("incremental poll: %d %v", rec.Code, resp)
		}
		// ⛔ No prompt or completion text in the panel's feed: the payload of a
		// decision step carries hashes, never the instruction.
		raw, _ := json.Marshal(events)
		if strings.Contains(string(raw), "office rent of Rs 25,000") || strings.Contains(string(raw), "Pay the May") {
			t.Fatal("the instruction text reached the event feed")
		}
	})

	t.Run("cancel ends the run and nothing commits after", func(t *testing.T) {
		b := newAgentBooks(t, srv)
		runID, id, _ := parkPayment(t, srv, b, nil)
		if rec, _ := do(t, srv, http.MethodPost, b.base+"/agent/runs/"+runID+"/cancel", nil); rec.Code != 200 {
			t.Fatalf("cancel: %d", rec.Code)
		}
		if code, _ := decide(t, srv, b, id, map[string]any{"decision": "approve"}); code != 200 {
			t.Fatalf("approve after cancel: %d", code)
		}
		pollUntilQuiet(t, srv)
		if getRun(t, srv, b, runID)["status"] != "cancelled" || countVouchers(t, b.companyID) != 0 {
			t.Fatalf("a cancelled run committed or resurrected: %v / %d", getRun(t, srv, b, runID)["status"], countVouchers(t, b.companyID))
		}
	})

	t.Run("runs and approvals are tenant-scoped", func(t *testing.T) {
		a := newAgentBooks(t, srv)
		runID, id, _ := parkPayment(t, srv, a, nil)
		other := newAgentBooks(t, srv)
		if rec, _ := do(t, srv, http.MethodGet, other.base+"/agent/runs/"+runID, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("company B read company A's run: %d", rec.Code)
		}
		if rec, _ := do(t, srv, http.MethodPost, other.base+"/agent/approvals/"+id+"/decision", map[string]any{"decision": "approve"}); rec.Code != http.StatusNotFound {
			t.Fatalf("company B decided company A's approval: %d", rec.Code)
		}
		if n := len(pendingApprovals(t, srv, other, runID)); n != 0 {
			t.Fatalf("company B lists %d of company A's approvals", n)
		}
	})

	t.Run("start refuses what it must", func(t *testing.T) {
		b := newAgentBooks(t, srv)
		for _, body := range []map[string]any{
			{"instruction": ""},
			{"instruction": "x", "delegate": []string{"delete voucher"}},
			{"instruction": "x", "delegate": []string{"no such key"}},
			{"instruction": "x", "inputs": []map[string]any{{"label": "l", "provenance": "user", "text": "launder me"}}},
			{"instruction": "x", "inputs": []map[string]any{{"label": "l", "provenance": "books", "text": "launder me"}}},
		} {
			if rec, _ := do(t, srv, http.MethodPost, b.base+"/agent/runs", body); rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("start %v = %d, want 422", body, rec.Code)
			}
		}
	})
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// TestAgentUnconfiguredIsHermetic: the shipped default has no model, and the
// agent plane says so with a 503 rather than pretending (HANDOFF §1.6).
func TestAgentUnconfiguredIsHermetic(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	rec, resp := do(t, srv, http.MethodPost, b.base+"/agent/runs", map[string]any{"instruction": "pay the rent"})
	if rec.Code != http.StatusServiceUnavailable || resp["code"] != "AGENT_UNCONFIGURED" {
		t.Fatalf("start with no model: %d %v, want 503 AGENT_UNCONFIGURED", rec.Code, resp)
	}
	if rec, _ := do(t, srv, http.MethodGet, b.base+"/agent/approvals", nil); rec.Code != 200 {
		t.Fatalf("the inbox must still read with no model: %d", rec.Code)
	}
}
