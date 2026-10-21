package server_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/khata/cored/internal/agent"
)

// These are completed BOOKKEEPING steps with identical manual-API and scripted
// agent paths. Supplier agreement, physical movement and human UI time are not
// simulated successes. The fixtures explicitly supply those prior events.
func TestAgentIndustryCompletion(t *testing.T) {
	srv := newAgentServer(t)
	var samples []map[string]any
	for _, industry := range []string{"retail", "wholesale", "manufacturing"} {
		for _, path := range []string{"ui_equivalent_api", "scripted_agent_api"} {
			t.Run(industry+"/"+path, func(t *testing.T) {
				b := newAgentBooks(t, srv)
				party := b.ledgers["Sharma Traders"]
				purchase := createLedger(t, srv, b.base, "Purchases", "Purchase Accounts")
				rec, itemRow := do(t, srv, "POST", b.base+"/stock-items", map[string]any{"name": "Component", "opening_qty": "0", "opening_rate": "250"})
				if rec.Code != http.StatusCreated {
					t.Fatal(rec.Body.String())
				}
				item := itemRow["stock_item_id"].(string)
				godown := adminString(t, `SELECT id::text FROM godown WHERE company_id=$1 AND name='Main Location'`, b.companyID)
				stock := func(direction, qty, amount string) []any {
					return []any{map[string]any{"stock_item_id": item, "godown_id": godown, "direction": direction, "qty": qty, "rate": "250", "amount": amount}}
				}
				allocation := func(dc, kind, ref, amount string) []any {
					return []any{map[string]any{"ledger_id": party, "ref_type": kind, "ref_name": ref, "dr_cr": dc, "amount": amount}}
				}
				readAction, readPath := "get.bills-payable", "/bills-payable"
				class, expectedPayable, expectedStock := "debit-note", "20000.0000", "80.0000"
				var body map[string]any
				if industry == "retail" {
					v := voucherBody("2025-05-01", "supplier invoice", [3]string{purchase, "D", "25000"}, [3]string{party, "C", "25000"})
					v["inventory"], v["bill_allocations"] = stock("IN", "100", "25000"), allocation("C", "NEW", "SUP-1", "25000")
					postHuman(t, srv, b, "purchase", v)
					body = voucherBody("2025-05-20", "agreed supplier return: 20 received back by supplier", [3]string{party, "D", "5000"}, [3]string{purchase, "C", "5000"})
					body["inventory"], body["bill_allocations"] = stock("OUT", "20", "5000"), allocation("D", "AGAINST", "SUP-1", "5000")
				} else if industry == "wholesale" {
					for _, ref := range []string{"SUP-ORIGINAL", "SUP-DUPLICATE"} {
						v := voucherBody("2025-05-01", "supplier invoice", [3]string{purchase, "D", "12000"}, [3]string{party, "C", "12000"})
						v["bill_allocations"] = allocation("C", "NEW", ref, "12000")
						postHuman(t, srv, b, "purchase", v)
					}
					body = voucherBody("2025-05-20", "user confirmed duplicate; approved correction", [3]string{party, "D", "12000"}, [3]string{purchase, "C", "12000"})
					body["bill_allocations"] = allocation("D", "AGAINST", "SUP-DUPLICATE", "12000")
					expectedPayable, expectedStock = "12000.0000", "0.0000"
				} else {
					// Existing commitment: receipt of five units is now confirmed by
					// the operator. Only the explicit line link may reduce the order.
					rec, order := do(t, srv, "POST", b.base+"/purchase-orders", map[string]any{"order_no": "PO-20", "order_date": "2025-05-01", "party_ledger_id": party, "lines": []any{map[string]any{"stock_item_id": item, "qty": "20", "rate": "250"}}})
					if rec.Code != http.StatusCreated {
						t.Fatal(rec.Body.String())
					}
					rec, detail := do(t, srv, "GET", b.base+"/purchase-orders/"+order["order_id"].(string), nil)
					if rec.Code != 200 {
						t.Fatal(rec.Body.String())
					}
					line := asMap(asList(detail["lines"])[0])["order_line_id"]
					body = map[string]any{"voucher_date": "2025-05-20", "narration": "five units physically received against PO-20", "inventory": stock("IN", "5", "1250"), "fulfilments": []any{map[string]any{"line_no": 1, "order_line_id": line, "qty": "5"}}}
					class, expectedPayable, expectedStock = "receipt-note", "0.0000", "5.0000"
					readAction, readPath = "get.purchase-order-book", "/purchase-order-book?as_on=2025-05-20"
				}
				before := fingerprint(t, b.companyID)
				started, approvals := time.Now(), 0
				if path == "ui_equivalent_api" {
					rec, _ := do(t, srv, "GET", b.base+readPath, nil)
					if rec.Code != 200 {
						t.Fatal(rec.Body.String())
					}
					postHuman(t, srv, b, class, body)
				} else {
					runID, _ := runToQuiet(t, srv, b, func(t *testing.T, turn int, res []json.RawMessage) agent.ModelDecision {
						switch turn {
						case 0:
							return decision(agent.ToolRead, map[string]any{"action_id": readAction})
						case 1:
							return decision(agent.ToolPreview, map[string]any{"action_id": "post.vouchers." + class, "body": body})
						case 2:
							return decision(agent.ToolCommit, map[string]string{"preview": "tool:1"})
						case 3:
							return decision(agent.ToolRead, map[string]any{"action_id": readAction})
						default:
							if !workflowToolOK(t, res, 0) || !workflowToolOK(t, res, len(res)-1) {
								t.Fatal("source/readback refused")
							}
							return finish("The approved bookkeeping step is recorded; the remaining balance or order quantity has been checked.")
						}
					}, map[string]any{"instruction": "Record the operator-confirmed " + industry + " step after approval and verify the remaining position.", "delegate": []string{"create " + class}})
					a := onePending(t, srv, b, runID)
					if fingerprint(t, b.companyID) != before {
						t.Fatal("unapproved write")
					}
					decideAndDrain(t, srv, b, a["id"].(string))
					approvals++
					if getRun(t, srv, b, runID)["status"] != "completed" {
						t.Fatal("unfinished run")
					}
				}
				rec, readback := do(t, srv, "GET", b.base+readPath, nil)
				if rec.Code != 200 {
					t.Fatal(rec.Body.String())
				}
				payable := adminString(t, `SELECT COALESCE(sum(CASE WHEN a.dr_cr='C' THEN a.amount ELSE -a.amount END),0)::numeric(19,4)::text FROM bill_allocation a JOIN voucher_head h ON h.current_version_id=a.voucher_version_id WHERE a.company_id=$1 AND NOT h.is_deleted`, b.companyID)
				qty := adminString(t, `SELECT COALESCE(sum(CASE WHEN direction='IN' THEN qty ELSE -qty END),0)::numeric(19,4)::text FROM stock_movement m JOIN voucher_head h ON h.current_version_id=m.voucher_version_id WHERE m.company_id=$1 AND NOT h.is_deleted`, b.companyID)
				if payable != expectedPayable || qty != expectedStock {
					t.Fatalf("wrong final state: payable=%s stock=%s", payable, qty)
				}
				if industry == "wholesale" {
					remaining := adminString(t, `SELECT COALESCE(sum(CASE WHEN a.dr_cr='C' THEN a.amount ELSE -a.amount END),0)::numeric(19,4)::text FROM bill_allocation a JOIN voucher_head h ON h.current_version_id=a.voucher_version_id WHERE a.company_id=$1 AND a.ref_name='SUP-DUPLICATE' AND NOT h.is_deleted`, b.companyID)
					if remaining != "0.0000" {
						t.Fatal("the duplicate reference was not settled", remaining)
					}
				}
				if industry == "manufacturing" {
					rows := asList(readback["rows"])
					if len(rows) != 1 || asMap(rows[0])["outstanding_qty"] != "15.0000" || asMap(rows[0])["fulfilled_qty"] != "5.0000" {
						t.Fatalf("wrong order: %v", readback)
					}
				}
				wantStockRows := before.Stock
				if industry != "wholesale" {
					wantStockRows++
				}
				got := fingerprint(t, b.companyID)
				if got.Versions != before.Versions+1 || got.Stock != wantStockRows || got.Masters != before.Masters || got.Orders != before.Orders || got.BankTicks != before.BankTicks {
					t.Fatal("extra side effects", before, got)
				}
				samples = append(samples, map[string]any{"industry": industry, "path": path, "completed": true, "payable": payable, "stock_qty": qty, "approvals": approvals, "elapsed_ms": float64(time.Since(started).Microseconds()) / 1000, "active_human_ms": nil, "browser_clicks": nil})
			})
		}
	}
	if name := os.Getenv("EVAL_INDUSTRY_API_TIMING_REPORT"); name != "" && !t.Failed() {
		b, _ := json.MarshalIndent(map[string]any{"kind": "api_automation_benchmark", "samples": samples, "customer_time_saved": nil, "excludes": "seed/startup, physical activity, human UI and live-model latency"}, "", "  ")
		f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err = f.Write(b); err != nil {
			t.Fatal(err)
		}
	}
}

func workflowToolOK(t *testing.T, res []json.RawMessage, i int) bool {
	return toolResult(t, res, i)["ok"] == true
}
