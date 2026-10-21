package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/khata/cored/internal/agent"
)

func TestAgentPreservesNestedFinishAnswer(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	for _, tc := range []struct {
		name, top, nested, want string
	}{
		{"nested provider answer", "", "The bank balance is ₹2,53,000.00.", "The bank balance is ₹2,53,000.00."},
		{"canonical answer wins", "Canonical answer", "Other answer", "Canonical answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runID, _ := runToQuiet(t, srv, b, func(*testing.T, int, []json.RawMessage) agent.ModelDecision {
				args, _ := json.Marshal(map[string]string{"final": tc.nested})
				return agent.ModelDecision{Tool: agent.ToolFinish, Args: args, Final: tc.top}
			}, map[string]any{"instruction": "Summarize the result."})
			rec, body := do(t, srv, http.MethodGet, b.base+"/agent/runs/"+runID, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("read run: %d %s", rec.Code, rec.Body.String())
			}
			run := asMap(body["run"])
			if run["status"] != "completed" || asMap(run["outcome"])["final"] != tc.want {
				t.Fatalf("lost finish answer: %v", run)
			}
		})
	}
}

func TestAgentRepairsPresentationWithoutTruncatingDetails(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	runID, m := runToQuiet(t, srv, b, func(t *testing.T, turn int, _ []json.RawMessage) agent.ModelDecision {
		if turn == 0 {
			return finish(strings.Repeat("review ", 251))
		}
		d := finish("₹25,000 is last-rate stock value; recovery terms are unknown. Draft: Please confirm return or credit terms. No write or send occurred.")
		d.Details = strings.Repeat("Requested full explanation. ", 300)
		return d
	}, map[string]any{"instruction": "Explain in detail and include a supplier draft."})
	r := getRun(t, srv, b, runID)
	if r["status"] != "completed" || len(asMap(r["outcome"])["details"].(string)) < 1000 {
		t.Fatal(r)
	}
	if result := profileResult(t, m, 0); result["ok"] != false {
		t.Fatalf("no revision requested: %v", result)
	}
}

func TestAgentCannotCompleteSilently(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	runID, _ := runToQuiet(t, srv, b, func(*testing.T, int, []json.RawMessage) agent.ModelDecision {
		return agent.ModelDecision{Tool: agent.ToolFinish, Args: json.RawMessage(`{"final":" "}`)}
	}, map[string]any{"instruction": "Summarize the result."})
	if run := getRun(t, srv, b, runID); run["status"] != "failed" {
		t.Fatalf("empty answer was presented as success: %v", run)
	}
}
