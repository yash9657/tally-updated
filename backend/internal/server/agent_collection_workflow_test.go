package server_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/khata/cored/internal/agent"
)

// A complete bookkeeping collection step: inspect an invoice, allocate the
// received money after approval, and verify the remaining outstanding. It is
// not an email send, bank transfer, browser benchmark or human timing study.
func TestAgentCollectionWorkflowAndAPITiming(t *testing.T) {
	srv := newAgentServer(t)
	var samples []map[string]any
	for _, path := range []string{"ui_equivalent_api", "scripted_agent_api"} {
		t.Run(path, func(t *testing.T) {
			b := newAgentBooks(t, srv)
			party := b.ledgers["Acme Retail"]
			alloc := func(dc, kind, amount string) []any {
				return []any{map[string]any{"ledger_id": party, "ref_type": kind, "ref_name": "SVC-OPEN", "dr_cr": dc, "amount": amount}}
			}
			invoice := voucherBody("2025-05-01", "service invoice", [3]string{party, "D", "10000"}, [3]string{b.ledgers["Domestic Sales"], "C", "10000"})
			invoice["bill_allocations"] = alloc("D", "NEW", "10000")
			invoice["voucher_no"] = "SVC-OPEN"
			postHuman(t, srv, b, "sales", invoice)
			receipt := voucherBody("2025-05-20", "partial receipt", [3]string{b.ledgers["HDFC Bank"], "D", "6000"}, [3]string{party, "C", "6000"})
			receipt["bill_allocations"] = alloc("C", "AGAINST", "6000")
			receipt["voucher_no"] = "SVC-RCPT"
			before := fingerprint(t, b.companyID)
			started := time.Now()
			approvals := 0
			if path == "ui_equivalent_api" {
				rec, _ := do(t, srv, http.MethodGet, b.base+"/outstandings", nil)
				if rec.Code != http.StatusOK {
					t.Fatal(rec.Body.String())
				}
				postHuman(t, srv, b, "receipt", receipt)
			} else {
				runID, _ := runToQuiet(t, srv, b, func(t *testing.T, turn int, _ []json.RawMessage) agent.ModelDecision {
					switch turn {
					case 0:
						return decision(agent.ToolRead, map[string]any{"action_id": "get.outstandings"})
					case 1:
						return decision(agent.ToolPreview, map[string]any{"action_id": "post.vouchers.receipt", "body": receipt})
					case 2:
						return decision(agent.ToolCommit, map[string]string{"preview": "tool:1"})
					case 3:
						return decision(agent.ToolRead, map[string]any{"action_id": "get.outstandings"})
					default:
						return finish("₹6,000 received and allocated to SVC-OPEN after approval; ₹4,000 remains. No message was sent.")
					}
				}, map[string]any{"instruction": "₹6,000 has arrived in HDFC. Allocate it against SVC-OPEN after approval and verify what remains.", "delegate": []string{"create receipt"}})
				a := onePending(t, srv, b, runID)
				if fingerprint(t, b.companyID) != before {
					t.Fatal("unapproved preview changed books")
				}
				decideAndDrain(t, srv, b, a["id"].(string))
				approvals++
				if getRun(t, srv, b, runID)["status"] != "completed" {
					t.Fatal("workflow unfinished")
				}
			}
			q := map[string]any{"dsl": map[string]any{"entity": "bill_allocations", "dimensions": []string{"ref_name"}, "metrics": []any{map[string]any{"func": "sum", "column": "debit_minus_credit", "as": "remaining"}}, "filters": []any{map[string]any{"column": "ref_name", "op": "eq", "values": []string{"SVC-OPEN"}}}}}
			rec, result := do(t, srv, http.MethodPost, b.base+"/query", q)
			if rec.Code != 200 || len(asList(result["rows"])) != 1 || asList(asList(result["rows"])[0])[1] != "4000.0000" {
				t.Fatalf("wrong final allocation: %d %v", rec.Code, result)
			}
			got := fingerprint(t, b.companyID)
			if got.Versions != before.Versions+1 || got.Stock != before.Stock || got.Masters != before.Masters || got.Orders != before.Orders || got.BankTicks != before.BankTicks {
				t.Fatal("unexpected side effects", before, got)
			}
			samples = append(samples, map[string]any{"path": path, "elapsed_ms": float64(time.Since(started).Microseconds()) / 1000, "approvals": approvals, "completed": true, "verified_remaining": "4000.0000", "active_human_ms": nil, "browser_clicks": nil})
		})
	}
	if path := os.Getenv("EVAL_WORKFLOW_TIMING_REPORT"); path != "" {
		data, _ := json.MarshalIndent(map[string]any{"kind": "api_automation_benchmark", "industry": "services", "task": "partial_receipt_allocation", "source_revision": os.Getenv("EVAL_SOURCE_REVISION"), "excludes": "seed/startup; human thinking, typing and approval time; browser rendering; live-model latency", "customer_time_saved": nil, "samples": samples}, "", "  ")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err = f.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}
