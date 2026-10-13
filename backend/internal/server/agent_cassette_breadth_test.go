package server_test

// agent_cassette_breadth_test.go — the per-feature cassette replay for the
// wave-2 write families (tier0 counts these cassettes; its pins moved with
// them). Each case starts from the run's INPUT — the instruction and the books
// it names — and replays every recorded model decision by a key re-derived
// from the live conversation, through preview, park, approval and commit.
//
// SeedNote cassettes: the "model" is the deterministic in-repo script, NOT a
// live provider (there are no keys). Regenerate with the wave-1 command:
//
//	TRAJECTORY_RECORD=1 go test ./internal/server/ -run TestAgentCassette

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/khata/cored/internal/server"
)

type breadthCassette struct {
	caseID    string
	setup     func(t *testing.T, srv *server.Server, b agentBooks) (script, string)
	delegate  []string
	wantCalls int
	// check runs after the approved commit.
	check func(t *testing.T, srv *server.Server, b agentBooks, runID string)
}

var breadthCassettes = []breadthCassette{
	{caseID: "alter-rent-payment", delegate: []string{"alter payment"}, wantCalls: 4,
		setup: func(t *testing.T, srv *server.Server, b agentBooks) (script, string) {
			vid := postHuman(t, srv, b, "payment", rentPayment(b, "25000.00", "Office rent for May 2025"))
			return alterScript(vid, "26000.00"), "Voucher " + vid + " should have been Rs 26,000, not Rs 25,000. Correct it."
		},
		check: func(t *testing.T, srv *server.Server, b agentBooks, runID string) {
			if n := adminInt(t, `SELECT count(*) FROM voucher_version WHERE company_id = $1`, b.companyID); n != 2 {
				t.Fatalf("versions = %d, want 2 (the original and its compensating version)", n)
			}
		}},
	{caseID: "new-supplier-born-blocked", delegate: []string{"create master", "create payment"}, wantCalls: 5,
		setup: func(t *testing.T, srv *server.Server, b agentBooks) (script, string) {
			return newPartyScript, "Add our new supplier " + newPartyName + " (HDFC a/c 50100123456789, IFSC HDFC0001234) and pay them Rs 1,000."
		},
		check: func(t *testing.T, srv *server.Server, b agentBooks, runID string) {
			if row := ledgerRow(t, srv, b, newPartyName); row == nil || row["blocked"] != true {
				t.Fatalf("the agent-created supplier is not blocked: %v", row)
			}
			if n := countVouchers(t, b.companyID); n != 0 {
				t.Fatalf("%d voucher(s): the blocked supplier was paid", n)
			}
		}},
}

func TestAgentCassetteBreadthRunsEndToEnd(t *testing.T) {
	for _, tc := range breadthCassettes {
		t.Run(tc.caseID, func(t *testing.T) {
			srv := newAgentServer(t)
			b := newAgentBooks(t, srv)
			s, instruction := tc.setup(t, srv, b)
			clock := &fixedClock{t: day0}
			model := cassetteModel(t, tc.caseID, s)
			srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t), Now: clock.Now})
			runID := startRun(t, srv, b, map[string]any{"instruction": instruction, "delegate": tc.delegate})
			pollUntilQuiet(t, srv)
			if got := getRun(t, srv, b, runID)["status"]; got != "parked" {
				t.Fatalf("status after the first poll = %v, want parked", got)
			}
			a := onePending(t, srv, b, runID)
			if code, resp := decide(t, srv, b, a["id"].(string), map[string]any{"decision": "approve"}); code != http.StatusOK {
				t.Fatalf("approve: %d %v", code, resp)
			}
			clock.Set(day0.Add(10 * time.Minute))
			pollUntilQuiet(t, srv)
			if st := getRun(t, srv, b, runID)["status"]; st != "completed" {
				t.Fatalf("status after the approval = %v", st)
			}
			tc.check(t, srv, b, runID)
			if model.calls != tc.wantCalls {
				t.Fatalf("model calls = %d, want %d", model.calls, tc.wantCalls)
			}
			kinds := intentKinds(t, b.companyID, runID)
			for _, want := range []string{"run_started", "preview", "policy", "approval_requested", "approval_decided", "commit", "run_finished"} {
				if !strings.Contains(kinds, want) {
					t.Fatalf("intent log %s lacks %q", kinds, want)
				}
			}
			if model.cassette != nil && model.cassette.Replayed() != model.cassette.Len() {
				t.Fatalf("replayed %d of %d recorded interactions", model.cassette.Replayed(), model.cassette.Len())
			}
		})
	}
}
