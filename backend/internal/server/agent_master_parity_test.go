package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/server"
)

func TestAgentMasterFamilyApprovalParity(t *testing.T) {
	for _, tc := range []struct {
		action, table, key, name, resource string
		body                               map[string]any
	}{
		{"post.groups", "account_group", "name", "Wholesale Region", "master", map[string]any{"name": "Wholesale Region", "parent_name": "Sundry Debtors"}},
		{"post.units", "unit", "symbol", "BOX12", "master", map[string]any{"symbol": "BOX12", "formal_name": "Box of twelve", "decimal_places": 0}},
		{"post.cost-categories", "cost_category", "name", "Branch", "cost-centre", map[string]any{"name": "Branch"}},
		{"post.budgets", "budget", "name", "Annual Operating Plan", "budget", map[string]any{"name": "Annual Operating Plan", "from_date": "2025-04-01", "to_date": "2026-03-31"}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			srv := newAgentServer(t)
			b := newAgentBooks(t, srv)
			tc.body["created_by"] = "forged attribution"
			runID, _ := runToQuiet(t, srv, b, func(t *testing.T, turn int, _ []json.RawMessage) agent.ModelDecision {
				switch turn {
				case 0:
					return decision(agent.ToolPreview, map[string]any{"action_id": tc.action, "body": tc.body})
				case 1:
					return decision(agent.ToolCommit, map[string]string{"preview": "tool:0"})
				default:
					return finish("Created the requested master after approval.")
				}
			}, map[string]any{"instruction": "Create the requested master", "delegate": []string{"create " + tc.resource}})
			a := onePending(t, srv, b, runID)
			count := func() int {
				return adminInt(t, "SELECT count(*) FROM "+tc.table+" WHERE company_id=$1 AND "+tc.key+"=$2", b.companyID, tc.name)
			}
			if count() != 0 {
				t.Fatal("preview wrote a master")
			}
			ms := asList(effectOf(t, a)["masters"])
			if len(ms) != 1 || asMap(ms[0])["op"] != "CREATE" {
				t.Fatalf("missing audited effect: %v", ms)
			}
			decideAndDrain(t, srv, b, a["id"].(string))
			pollUntilQuiet(t, srv)
			if count() != 1 || getRun(t, srv, b, runID)["status"] != "completed" {
				t.Fatal("approved create did not complete exactly once")
			}
			got := adminString(t, "SELECT created_by FROM master_audit WHERE company_id=$1 AND after_doc::text LIKE $2", b.companyID, "%"+tc.name+"%")
			if !strings.Contains(got, "agent:"+runID) || strings.Contains(got, "forged") {
				t.Fatal("incorrect audit attribution", got)
			}
			// A denied human cannot preview or smuggle a write through this capability.
			token := memberWithRules(t, srv, b, []map[string]string{{"effect": "disallow", "action": "create", "resource": tc.resource}})
			m := scriptedModel(t, func(t *testing.T, turn int, _ []json.RawMessage) agent.ModelDecision {
				if turn == 0 {
					return decision(agent.ToolPreview, map[string]any{"action_id": tc.action, "body": tc.body})
				}
				return finish("Permission denied; no master created.")
			})
			srv.ConfigureAgent(server.AgentOptions{LLM: m.client(t)})
			rec, _ := doRaw(t, srv, http.MethodPost, b.base+"/agent/runs", token, map[string]any{"instruction": "Create master", "delegate": []string{"create " + tc.resource}})
			if rec.Code != http.StatusAccepted {
				t.Fatalf("run did not start: %d %s", rec.Code, rec.Body.String())
			}
			pollUntilQuiet(t, srv)
			if result := profileResult(t, m, 0); result["ok"] != false || !strings.Contains(strings.ToLower(fmtResult(result)), "403") {
				t.Fatalf("preview exceeded human rights: %v", result)
			}
			if count() != 1 {
				t.Fatal("denied caller changed master count")
			}
		})
	}
}

func fmtResult(v any) string { b, _ := json.Marshal(v); return string(b) }
