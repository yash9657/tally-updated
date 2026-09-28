package server_test

// agent_approver_test.go — the checker's OWN authority is judged on the body
// that commits, and an edited body stays inside the run's delegated ceiling
// (the wave-1 review's second finding). Before the fix the approve endpoint
// asked the PDP about the approver with no date, and exec:k posted the
// approver's edited body under the run creator's identity and cutoff only.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/policy"
	"github.com/khata/cored/internal/server"
)

// memberWithCutoff invites a full-access member whose level carries a
// back-dated cutoff, and returns their (step-up) token.
func memberWithCutoff(t *testing.T, srv *server.Server, b agentBooks, cutoff string) string {
	tok, _ := memberAndLevel(t, srv, b, cutoff)
	return tok
}

// memberAndLevel is memberWithCutoff that also returns the level's id; an
// empty cutoff creates a level with none.
func memberAndLevel(t *testing.T, srv *server.Server, b agentBooks, cutoff string) (string, string) {
	t.Helper()
	owner := ownerToken(t, srv)
	name := "Accountant " + uuid.NewString()[:6]
	body := map[string]any{"name": name, "default_access": "full"}
	if cutoff != "" {
		body["backdated_cutoff"] = cutoff
	}
	rec, created := doRaw(t, srv, http.MethodPost, b.base+"/security-levels", owner, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create level = %d (%s)", rec.Code, rec.Body.String())
	}
	levelID, _ := created["security_level_id"].(string)
	subject := "approver-" + uuid.NewString()[:8]
	rec, _ = doRaw(t, srv, http.MethodPost, b.base+"/members/invites", owner,
		map[string]any{"subject": subject, "security_level_name": name})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d (%s)", rec.Code, rec.Body.String())
	}
	tok := mintToken(t, srv, subject, "webauthn")
	if rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("claim invite = %d", rec.Code)
	}
	return tok, levelID
}

func TestAgentApproverIsJudgedOnTheCommittedBody(t *testing.T) {
	srv := newAgentServer(t)

	t.Run("an approver whose cutoff forbids the voucher's date may not approve it", func(t *testing.T) {
		b := newAgentBooks(t, srv)
		// The payment is dated 2025-05-10; B's cutoff is after it.
		_, id, _ := parkPayment(t, srv, b, nil)
		tok := memberWithCutoff(t, srv, b, "2025-06-01")
		rec, _ := doRaw(t, srv, http.MethodPost, b.base+"/agent/approvals/"+id+"/decision", tok, map[string]any{"decision": "approve"})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("approve by a member whose cutoff forbids the date = %d (%s), want 403", rec.Code, rec.Body.String())
		}
		pollUntilQuiet(t, srv)
		if n := countVouchers(t, b.companyID); n != 0 {
			t.Fatalf("%d voucher(s) committed", n)
		}
	})

	t.Run("an approver may not back-date an edited body past their own cutoff", func(t *testing.T) {
		b := newAgentBooks(t, srv)
		runID, id, _ := parkPayment(t, srv, b, nil)
		tok := memberWithCutoff(t, srv, b, "2025-05-01") // the original date is fine for B
		edited := voucherBody("2025-04-15", "Office rent, back-dated",
			[3]string{b.ledgers["Office Rent"], "D", "25000.00"}, [3]string{b.ledgers["HDFC Bank"], "C", "25000.00"})
		rec, _ := doRaw(t, srv, http.MethodPost, b.base+"/agent/approvals/"+id+"/decision", tok,
			map[string]any{"decision": "approve", "edited_body": edited})
		code := rec.Code
		pollUntilQuiet(t, srv)
		if n := countVouchers(t, b.companyID); n != 0 {
			t.Fatalf("the approver's back-dated edit committed under the creator's cutoff (decision %d, %d voucher(s))", code, n)
		}
		if code == http.StatusOK && !strings.Contains(intentKinds(t, b.companyID, runID), "commit_refused") {
			t.Fatal("the approval was accepted but no refused commit is recorded")
		}
		if code != http.StatusForbidden {
			t.Fatalf("decision = %d, want 403 before anything is recorded", code)
		}
	})

	t.Run("the router re-judges the approver at commit, not only at the decision", func(t *testing.T) {
		b := newAgentBooks(t, srv)
		runID, id, _ := parkPayment(t, srv, b, nil)
		tok, levelID := memberAndLevel(t, srv, b, "")
		rec, _ := doRaw(t, srv, http.MethodPost, b.base+"/agent/approvals/"+id+"/decision", tok, map[string]any{"decision": "approve"})
		if rec.Code != http.StatusOK {
			t.Fatalf("approve = %d (%s)", rec.Code, rec.Body.String())
		}
		// Between the approval and the commit, the owner locks B's period.
		if rec, _ := doRaw(t, srv, http.MethodPut, b.base+"/security-levels/"+levelID, ownerToken(t, srv),
			map[string]any{"backdated_cutoff": "2025-06-01"}); rec.Code != http.StatusOK {
			t.Fatalf("set cutoff = %d (%s)", rec.Code, rec.Body.String())
		}
		pollUntilQuiet(t, srv)
		if n := countVouchers(t, b.companyID); n != 0 {
			t.Fatalf("the commit ran on the creator's authority alone (%d voucher(s))", n)
		}
		if !strings.Contains(intentKinds(t, b.companyID, runID), "commit_refused") {
			t.Fatal("the refused commit is not in the intent log")
		}
	})

	t.Run("an edited body above the run's delegated ceiling does not commit", func(t *testing.T) {
		b := newAgentBooks(t, srv)
		model := scriptedModel(t, paymentScript)
		srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t)})
		runID := startRun(t, srv, b, map[string]any{"instruction": cassetteCases[0].instruction,
			"delegate": []string{"create payment"}, "max_amount_paise": 3_000_000})
		pollUntilQuiet(t, srv)
		pend := pendingApprovals(t, srv, b, runID)
		if len(pend) != 1 {
			t.Fatalf("pending = %d", len(pend))
		}
		edited := voucherBody("2025-05-10", "Office rent, raised",
			[3]string{b.ledgers["Office Rent"], "D", "490000.00"}, [3]string{b.ledgers["HDFC Bank"], "C", "490000.00"})
		if code, resp := decide(t, srv, b, pend[0]["id"].(string), map[string]any{"decision": "approve", "edited_body": edited}); code != 200 {
			t.Fatalf("approve: %d %v", code, resp)
		}
		pollUntilQuiet(t, srv)
		if n := countVouchers(t, b.companyID); n != 0 {
			t.Fatalf("an edited body of Rs 4,90,000 committed past a Rs 30,000 delegated ceiling (%d voucher(s))", n)
		}
		if !strings.Contains(intentKinds(t, b.companyID, runID), "commit_refused") {
			t.Fatal("the over-ceiling commit is not recorded as refused")
		}
	})
}

// TestAgentCancelStopsARunMidResume: the user cancels while a decide:k model
// call is in flight (the fake model issues the cancel from inside its own
// round trip). The same Resume must not go on to gate and commit — before the
// fix it posted the voucher and the run page kept saying "cancelled".
func TestAgentCancelStopsARunMidResume(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	setAutonomy(t, b.companyID, "create payment", policy.L3)
	var runID string
	cancelled := 0
	s := func(t *testing.T, turn int, res []json.RawMessage) agent.ModelDecision {
		if turn == 3 && cancelled == 0 {
			rec, _ := do(t, srv, http.MethodPost, b.base+"/agent/runs/"+runID+"/cancel", nil)
			cancelled = rec.Code
		}
		return paymentScript(t, turn, res)
	}
	model := scriptedModel(t, s)
	srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t)})
	runID = startRun(t, srv, b, map[string]any{"instruction": cassetteCases[0].instruction, "delegate": []string{"create payment"}})
	pollUntilQuiet(t, srv)
	if cancelled != http.StatusOK {
		t.Fatalf("cancel from inside the model call = %d, want 200 (the scenario did not happen)", cancelled)
	}
	if n := countVouchers(t, b.companyID); n != 0 {
		t.Fatalf("a run cancelled mid-Resume committed %d voucher(s)", n)
	}
	if st := getRun(t, srv, b, runID)["status"]; st != "cancelled" {
		t.Fatalf("status %v", st)
	}
	// The step guard, not only the commit's row lock, stopped it: no policy
	// decision was even made after the cancel.
	kinds := intentKinds(t, b.companyID, runID)
	if strings.Contains(kinds, "policy") || strings.Contains(kinds, "commit") {
		t.Fatalf("the run kept executing after the cancel: %s", kinds)
	}
}
