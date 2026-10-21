package server_test

import (
	"encoding/json"
	"net/http"
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
