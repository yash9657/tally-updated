package server_test

// agent_cassette_test.go — W2's cassette-replayed agent runs, end to end: a
// Payment voucher and a Sales voucher, from an instruction typed by a human to
// a voucher in the books, through the real router, the real PDP, the real
// validators, a durable park on a human approval, and the commit.
//
// ⛔ THE PER-FEATURE REPLAY HALF OF THE TIER-0 GATE (tier0_test.go's header:
// "a new cassette feature owes a per-feature replay test that starts from the
// input"). These tests start from the instruction, let the agent build every
// prompt, and re-derive each cassette key from the request the llm client
// actually put on the wire. A prompt change that moved a key fails HERE, with
// the regeneration command; Tier 0 alone could not see it.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/intentlog"
	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/server"
	"github.com/khata/cored/internal/trajectory"

	"github.com/jackc/pgx/v5"
)

var day0 = time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)

// paymentScript: pay the May rent from HDFC Bank.
func paymentScript(t *testing.T, turn int, res []json.RawMessage) agent.ModelDecision {
	switch turn {
	case 0:
		d := decision(agent.ToolSearch, map[string]string{"query": "payment"})
		d.Plan = []string{"find the payment action", "look up the rent and bank ledgers", "preview", "commit"}
		return d
	case 1:
		return decision(agent.ToolRead, map[string]any{"action_id": "get.ledgers"})
	case 2:
		return decision(agent.ToolPreview, map[string]any{"action_id": "post.vouchers.payment",
			"body": voucherBody("2025-05-10", "Office rent for May 2025",
				[3]string{ledgerIDFromResults(t, res, "Office Rent"), "D", "25000.00"},
				[3]string{ledgerIDFromResults(t, res, "HDFC Bank"), "C", "25000.00"})})
	case 3:
		return decision(agent.ToolCommit, map[string]string{"preview": "tool:2"})
	default:
		return finish("The May rent payment of Rs 25,000 from HDFC Bank has been recorded.")
	}
}

// salesScript: a sales invoice whose FIRST preview the validators refuse
// (unbalanced), corrected once — the bounded retry.
func salesScript(t *testing.T, turn int, res []json.RawMessage) agent.ModelDecision {
	switch turn {
	case 0:
		return decision(agent.ToolSearch, map[string]string{"query": "sales"})
	case 1:
		return decision(agent.ToolRead, map[string]any{"action_id": "get.ledgers"})
	case 2:
		return decision(agent.ToolPreview, map[string]any{"action_id": "post.vouchers.sales",
			"body": withParty(voucherBody("2025-05-12", "Sale to Acme Retail",
				[3]string{ledgerIDFromResults(t, res, "Acme Retail"), "D", "11800.00"},
				[3]string{ledgerIDFromResults(t, res, "Domestic Sales"), "C", "10000.00"}), "Acme Retail")})
	case 3:
		return decision(agent.ToolPreview, map[string]any{"action_id": "post.vouchers.sales",
			"body": withParty(voucherBody("2025-05-12", "Sale to Acme Retail",
				[3]string{ledgerIDFromResults(t, res, "Acme Retail"), "D", "10000.00"},
				[3]string{ledgerIDFromResults(t, res, "Domestic Sales"), "C", "10000.00"}), "Acme Retail")})
	case 4:
		return decision(agent.ToolCommit, map[string]string{"preview": "tool:3"})
	default:
		return finish("The sales invoice to Acme Retail for Rs 10,000 has been recorded.")
	}
}

func withParty(body map[string]any, party string) map[string]any {
	body["party"] = party
	return body
}

type cassetteCase struct {
	caseID      string
	script      script
	instruction string
	delegate    []string
	wantCalls   int // model calls across the whole run
	wantDebit   int64
	verifierErr bool
}

var cassetteCases = []cassetteCase{
	{caseID: "payment-rent-hdfc", script: paymentScript,
		instruction: "Pay the May office rent of Rs 25,000 from HDFC Bank, dated 10 May 2025.",
		delegate:    []string{"create payment"}, wantCalls: 5, wantDebit: 2500000},
	{caseID: "sales-acme-retry", script: salesScript,
		instruction: "Raise a sales invoice to Acme Retail for Rs 10,000, dated 12 May 2025.",
		delegate:    []string{"create sales"}, wantCalls: 6, wantDebit: 1000000, verifierErr: true},
}

// TestAgentCassetteRunsEndToEnd is the gate for both cases.
func TestAgentCassetteRunsEndToEnd(t *testing.T) {
	for _, tc := range cassetteCases {
		t.Run(tc.caseID, func(t *testing.T) {
			srv := newAgentServer(t)
			b := newAgentBooks(t, srv)
			clock := &fixedClock{t: day0}
			model := cassetteModel(t, tc.caseID, tc.script)
			srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t), Now: clock.Now})

			runID := startRun(t, srv, b, map[string]any{"instruction": tc.instruction, "delegate": tc.delegate})
			pollUntilQuiet(t, srv)

			// PARKED on a human, with the handler's own preview — and NOTHING
			// in the books yet. The preview ran the real handler and rolled it
			// back; a voucher here would mean the dry run committed.
			if got := getRun(t, srv, b, runID)["status"]; got != "parked" {
				t.Fatalf("run status after first poll = %v, want parked", got)
			}
			if n := countVouchers(t, b.companyID); n != 0 {
				t.Fatalf("%d voucher(s) exist before any approval — the preview committed", n)
			}
			pend := pendingApprovals(t, srv, b, runID)
			if len(pend) != 1 {
				t.Fatalf("pending approvals = %d, want 1", len(pend))
			}
			a := pend[0]
			if amt, _ := a["amount_paise"].(float64); int64(amt) != tc.wantDebit {
				t.Fatalf("approval amount_paise = %v, want %d (computed by Go from the handler's postings)", a["amount_paise"], tc.wantDebit)
			}
			reasons, _ := json.Marshal(a["reasons"])
			if !strings.Contains(string(reasons), "L1") {
				t.Fatalf("approval reasons %s do not name the default L1", reasons)
			}
			preview, _ := json.Marshal(a["preview"])
			if !strings.Contains(string(preview), `"postings"`) {
				t.Fatalf("approval carries no ledger-effect preview: %s", preview)
			}

			// A human approves.
			rec, _ := do(t, srv, http.MethodPost, b.base+"/agent/approvals/"+a["id"].(string)+"/decision",
				map[string]any{"decision": "approve"})
			if rec.Code != http.StatusOK {
				t.Fatalf("approve status %d: %s", rec.Code, rec.Body.String())
			}
			clock.Set(day0.Add(10 * time.Minute))
			pollUntilQuiet(t, srv)

			run := getRun(t, srv, b, runID)
			if run["status"] != "completed" {
				t.Fatalf("run status after approval = %v (%v), want completed", run["status"], run["last_error"])
			}
			if n := countVouchers(t, b.companyID); n != 1 {
				t.Fatalf("vouchers after the approved commit = %d, want exactly 1", n)
			}
			if os.Getenv("AGENT_TEST_DUMP") != "" && len(model.requests) > 0 {
				for _, m := range model.requests[len(model.requests)-1].Messages {
					t.Logf("[%s] %s", m.Role, m.Content)
				}
			}
			if model.calls != tc.wantCalls {
				t.Fatalf("model calls = %d, want %d", model.calls, tc.wantCalls)
			}

			// Polling a finished run asks the model nothing and posts nothing.
			clock.Set(day0.Add(time.Hour))
			pollUntilQuiet(t, srv)
			if model.calls != tc.wantCalls || countVouchers(t, b.companyID) != 1 {
				t.Fatalf("a finished run did more work on a later poll (calls %d, vouchers %d)", model.calls, countVouchers(t, b.companyID))
			}

			// The intent chain recomputes, and it recorded what happened.
			kinds := intentKinds(t, b.companyID, runID)
			for _, want := range []string{"run_started", "decision_step", "preview", "policy",
				"approval_requested", "approval_decided", "commit", "run_finished"} {
				if !strings.Contains(kinds, want) {
					t.Fatalf("intent log %s lacks %q", kinds, want)
				}
			}
			if tc.verifierErr && !strings.Contains(kinds, "verifier_error") {
				t.Fatalf("the first sales preview was refused by the validators but no verifier_error was logged: %s", kinds)
			}

			// ⛔ THE REPLAY HALF: every recorded interaction was asked for, by a
			// key re-derived from the live conversation.
			if model.cassette != nil && model.cassette.Replayed() != model.cassette.Len() {
				t.Fatalf("replayed %d of %d recorded interactions", model.cassette.Replayed(), model.cassette.Len())
			}
		})
	}
}

// intentKinds verifies the run's chain and returns its kinds, comma-joined.
func intentKinds(t *testing.T, companyID, runID string) string {
	t.Helper()
	pool, err := db.New(context.Background(), appDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	cid, rid := uuid.MustParse(companyID), uuid.MustParse(runID)
	var kinds []string
	var verr error
	var n int
	_ = pool.RunInReadOnlyTx(db.WithCompany(context.Background(), cid), func(ctx context.Context, tx pgx.Tx) error {
		n, verr = intentlog.Verify(ctx, tx, cid, rid)
		es, err := intentlog.List(ctx, tx, cid, rid, 0)
		if err != nil {
			verr = err
		}
		for _, e := range es {
			kinds = append(kinds, string(e.Kind))
		}
		return nil
	})
	if verr != nil || n == 0 {
		t.Fatalf("intent chain for run %s: verified %d links, err %v", runID, n, verr)
	}
	return strings.Join(kinds, ",")
}

// TestAgentCassetteKeyIsTheAgentsOwnRequestHash pins the two key derivations
// to each other: the harness hashes the WIRE body the llm client encoded, the
// agent hashes the llm.Request it built (agent.RequestHash, recorded on every
// decide step). For a conversation with no ids to normalise they must agree —
// otherwise the cassette would be keyed on something the agent never computed.
func TestAgentCassetteKeyIsTheAgentsOwnRequestHash(t *testing.T) {
	f := scriptedModel(t, func(*testing.T, int, []json.RawMessage) agent.ModelDecision { return finish("done") })
	c := f.client(t)
	req := &llm.Request{
		TenantID: uuid.New(), Feature: agent.Feature, PromptVersion: agent.PromptVersion,
		Static:  []llm.Message{{Role: llm.RoleSystem, Content: "static"}},
		Dynamic: []llm.Message{{Role: llm.RoleUser, Content: "Pay the rent."}, {Role: llm.RoleAssistant, Content: `{"tool":"x"}`}},
		Tools: []llm.Tool{{Name: agent.DecideTool, Description: "d",
			Parameters: json.RawMessage(`{"type": "object"}`)}},
		ToolChoice: llm.ToolChoice{Mode: llm.ToolChoiceNamed, Name: agent.DecideTool},
	}
	var md agent.ModelDecision
	if _, err := c.DecodeInto(context.Background(), req, agent.DecideTool, &md); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(f.keys) != 1 {
		t.Fatalf("model calls = %d", len(f.keys))
	}
	if got, want := f.keys[0], agent.RequestHash(req); got != want {
		t.Fatalf("wire-derived key %s != agent.RequestHash %s: the cassette and the agent disagree on what a conversation is", got, want)
	}
	_ = trajectory.PromptHash // the primitive both use
}
