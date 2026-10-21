package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/policy"
	"github.com/khata/cored/internal/query"
)

// A complete inventory is deliberately not a workflow pass rate. UI commands
// without a verified conversational trace remain gaps, including governance
// handoffs and client-only actions. No refusal is counted as execution.
func TestAgentCoverageInventory(t *testing.T) {
	root := "../../.."
	raw, err := os.ReadFile(filepath.Join(root, "frontend/src/api/goldens/ui-commands.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ui struct {
		Commands []map[string]any `json:"commands"`
		Screens  []string         `json:"screen_kinds"`
		Calls    []struct {
			Method, Verb, Path string
			Used               bool
		} `json:"client_calls"`
	}
	if err = json.Unmarshal(raw, &ui); err != nil {
		t.Fatal(err)
	}
	var actionRows []map[string]any
	counts := map[string]int{}
	for _, s := range actions.Specs {
		status, tool, reason := "unsupported", "", "transaction/effect audit and completion test required"
		if e := policy.Structural(s); e != nil {
			reason = e.Error()
			status = "human_handoff_gap"
		} else if !policy.AgentAllows(s) {
			reason = "agent ACL denies this capability"
			status = "human_handoff_gap"
		} else if agent.Previewable(s.ID) {
			if len(agentActionSchema(s.ID)) == 0 && s.Method != "DELETE" {
				t.Fatalf("previewable action %s lacks body schema", s.ID)
			}
			status, tool, reason = "approval_execution", "actions.preview + actions.commit", "human ACL, delegation, validators and approval policy apply; backend execution coverage is not UI workflow completion"
		} else if s.ID == "post.query" {
			status, tool, reason = "read", "query_books", "typed compiler and report/field permissions"
		} else if s.Risk == actions.RiskRead && s.Method == "GET" {
			status, tool, reason = "read", "actions.read", "human read permissions and field projections apply"
		} else if s.Risk == actions.RiskExport {
			status, tool, reason = "delivery_gap", "actions.read", "returns metadata only; no conversational file delivery"
		}
		var clients, sites []string
		for _, c := range ui.Calls {
			if c.Verb == s.Method && c.Path == s.Route {
				clients = append(clients, c.Method)
				// Actual screen call sites supplement the generated client manifest;
				// this is a source locator, not evidence that a click completed a task.
				err := filepath.WalkDir(filepath.Join(root, "frontend/src"), func(path string, d os.DirEntry, e error) error {
					if e != nil {
						return e
					}
					if d.IsDir() || !strings.HasSuffix(path, ".tsx") || strings.Contains(path, ".test.") {
						return nil
					}
					b, e := os.ReadFile(path)
					if e != nil {
						return e
					}
					if bytes.Contains(b, []byte("."+c.Method+"(")) {
						rel, _ := filepath.Rel(root, path)
						sites = append(sites, filepath.ToSlash(rel))
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		sort.Strings(sites)
		counts[status]++
		actionRows = append(actionRows, map[string]any{"action_id": s.ID, "route": s.Route, "method": s.Method, "acl": s.ACLKey, "risk": s.Risk, "status": status, "agent_tool": tool, "reason": reason, "client_methods": clients, "screen_source_locations": sites, "body_schema": agentActionSchema(s.ID), "workflow_trace": "not established by this inventory"})
	}
	for _, c := range ui.Commands {
		c["conversation_status"] = "unverified_ui_workflow_gap"
		if c["dispatch"] == "unavailable" || c["dispatch"] == "dead" {
			c["conversation_status"] = "unavailable_in_product"
		}
	}
	// Concrete slices connect source screens to their dispatchable backend
	// effects and completion tests. Screen discovery alone never proves that
	// an operator completed the task through conversation.
	var slices []map[string]any
	for _, w := range []struct{ task, screen, action, test string }{
		{"Create account group", "frontend/src/screens/masters/MasterCreateScreen.tsx", "post.groups", "TestAgentMasterFamilyApprovalParity"},
		{"Create unit", "frontend/src/screens/masters/MasterCreateScreen.tsx", "post.units", "TestAgentMasterFamilyApprovalParity"},
		{"Create cost category", "frontend/src/screens/masters/CostCentresScreen.tsx", "post.cost-categories", "TestAgentMasterFamilyApprovalParity"},
		{"Create budget header (lines remain a gap)", "frontend/src/screens/masters/BudgetsScreen.tsx", "post.budgets", "TestAgentMasterFamilyApprovalParity"},
		{"Record agreed retail stock return and reconcile supplier balance", "frontend/src/screens/VoucherScreen.tsx", "post.vouchers.debit-note", "TestAgentIndustryCompletion/retail"},
		{"Record confirmed duplicate correction and reconcile supplier reference", "frontend/src/screens/VoucherScreen.tsx", "post.vouchers.debit-note", "TestAgentIndustryCompletion/wholesale"},
		{"Allocate confirmed service receipt and verify remaining invoice", "frontend/src/screens/VoucherScreen.tsx", "post.vouchers.receipt", "TestAgentCollectionWorkflowAndAPITiming"},
		{"Receive confirmed materials against purchase order and verify remaining quantity", "frontend/src/screens/VoucherScreen.tsx", "post.vouchers.receipt-note", "TestAgentIndustryCompletion/manufacturing"},
	} {
		if _, err := os.Stat(filepath.Join(root, w.screen)); err != nil {
			t.Fatal(err)
		}
		var matched map[string]any
		for _, row := range actionRows {
			if row["action_id"] == w.action {
				matched = row
				break
			}
		}
		if matched == nil || matched["status"] != "approval_execution" {
			t.Fatalf("workflow action missing: %s", w.action)
		}
		slices = append(slices, map[string]any{"task": w.task, "screen_source": w.screen, "action_id": w.action, "client_methods": matched["client_methods"], "permission": matched["acl"], "agent_tool": matched["agent_tool"], "backend_completion_test": w.test, "ui_completion": "gap: no browser/operator completion trace", "deliverable": "verified persisted state after required approval; no external sending or payment"})
	}
	out := map[string]any{"schema_version": 1, "note": "Generated by TestAgentCoverageInventory. Counts are capabilities/commands, never unique workflows or completion percentages. Existing registry and UI-manifest tests verify their own generators.", "counts": counts, "actions": actionRows, "ui_commands": ui.Commands, "screens": ui.Screens, "query_catalog": query.Catalog(), "query_semantics": "bill_allocations is current allocation movement excluding opening bills. Outstandings includes openings. No arbitrary SQL. limit/has_more disclose bounded results.", "required_gaps": []string{"UI navigation/company/date/filter controls", "file delivery/print/export", "bill import and review", "bulk reconciliation", "payroll execution", "tax filing and exports", "BOM and manufacturing completion", "cost-centre create readback under rollback", "data import/export/backup/sync", "company/features/members/settings human handoffs", "independent approval and step-up in conversational flow", "historical audit/attachment question coverage", "human time-savings baseline"}}
	out["workflow_slices"] = slices
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	path := filepath.Join(root, "docs/evals/agent-coverage-v9.json")
	if os.Getenv("UPDATE_AGENT_COVERAGE") == "1" {
		if err = os.WriteFile(path, b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(want, b) {
		t.Fatal("agent coverage inventory drift; inspect change then regenerate with UPDATE_AGENT_COVERAGE=1 go test ./internal/server -run '^TestAgentCoverageInventory$'")
	}
}
