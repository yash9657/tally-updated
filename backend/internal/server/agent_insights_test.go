package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/insights"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/server"
)

func TestAgentBusinessInsightsEvidenceAndACL(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	rec, response := do(t, srv, http.MethodPost, b.base+"/ledgers", map[string]any{
		"name": "Credit Customer", "group_name": "Sundry Debtors", "credit_limit": "10000", "created_by": "owner"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	customer := response["ledger_id"].(string)
	body := voucherBody("2025-06-30", "credit sale", [3]string{customer, "D", "30000"}, [3]string{b.ledgers["Domestic Sales"], "C", "30000"})
	body["party_ledger_id"] = customer
	postHuman(t, srv, b, "sales", body)
	pool, err := db.New(context.Background(), appDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	cid := mustUUID(t, b.companyID)
	ctx := db.WithCompany(context.Background(), cid)
	if _, err := insights.ProcessEvents(ctx, pool, cid, time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC), 500); err != nil {
		t.Fatal(err)
	}
	read := func(t *testing.T, token, asOf string) string {
		script := func(t *testing.T, turn int, _ []json.RawMessage) agent.ModelDecision {
			if turn == 0 {
				return decision(agent.ToolBusinessInsights, map[string]any{"as_of": asOf})
			}
			return finish("Read verified findings.")
		}
		model := scriptedModel(t, script)
		srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t)})
		if token == "" {
			startRun(t, srv, b, map[string]any{"instruction": "Find business priorities"})
		} else {
			rec, _ := doRaw(t, srv, http.MethodPost, b.base+"/agent/runs", token, map[string]any{"instruction": "Find business priorities"})
			if rec.Code != http.StatusAccepted {
				t.Fatal(rec.Body.String())
			}
		}
		pollUntilQuiet(t, srv)
		raw, _ := json.Marshal(profileResult(t, model, 0))
		return string(raw)
	}
	owner := read(t, "", "2025-06-30")
	if !strings.Contains(owner, "credit_limit_breach") || !strings.Contains(owner, "30000.00") || !strings.Contains(owner, "stored_text") {
		t.Fatalf("owner lost verified finding: %s", owner)
	}
	if strings.Contains(owner, "evidence_sql") || strings.Contains(owner, "evidence_args") {
		t.Fatal("raw SQL exposed")
	}
	if !strings.Contains(owner, `"voucher_no":"1"`) || !strings.Contains(owner, `"date":"2025-06-30"`) {
		t.Fatalf("missing usable source voucher number/date: %s", owner)
	}
	for _, resource := range []string{"report:outstandings", "report:day-book", "master", "insights"} {
		tok := memberWithRules(t, srv, b, []map[string]string{{"effect": "disallow", "action": "display", "resource": resource}})
		if got := read(t, tok, "2025-06-30"); strings.Contains(got, "credit_limit_breach") || strings.Contains(got, "30000") {
			t.Fatalf("%s denial leaked finding: %s", resource, got)
		}
	}
	if got := read(t, "", "2025-07-10"); strings.Contains(got, "credit_limit_breach") {
		t.Fatal("old finding presented as current")
	}
	// A payment invalidates the cached credit exposure. Verification must refuse
	// the old insight even though its row is still open and recent.
	postHuman(t, srv, b, "receipt", voucherBody("2025-06-30", "paid", [3]string{b.ledgers["HDFC Bank"], "D", "30000"}, [3]string{customer, "C", "30000"}))
	if got := read(t, "", "2025-06-30"); strings.Contains(got, "credit_limit_breach") {
		t.Fatalf("stale exposure reached model: %s", got)
	}
}

func TestAgentCompanyReadUsesHumanPermission(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	script := func(t *testing.T, turn int, _ []json.RawMessage) agent.ModelDecision {
		if turn == 0 {
			return decision(agent.ToolRead, map[string]any{"action_id": "get._self"})
		}
		return finish("Read company tax context.")
	}
	_, m := runToQuiet(t, srv, b, script, map[string]any{"instruction": "Read company tax context"})
	if r := profileResult(t, m, 0); r["ok"] != true {
		t.Fatalf("company read refused owner: %v", r)
	}
	fields := asMap(profileResult(t, m, 0)["result"])
	if _, ok := fields["state_code"]; !ok {
		t.Fatal("tax state missing")
	}
	for _, key := range []string{"id", "name", "address", "mailing_name", "city"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("unneeded company field %s reached model", key)
		}
	}
	tok := memberWithRules(t, srv, b, []map[string]string{{"effect": "disallow", "action": "display", "resource": "company"}})
	m = scriptedModel(t, script)
	srv.ConfigureAgent(server.AgentOptions{LLM: m.client(t)})
	rec, _ := doRaw(t, srv, http.MethodPost, b.base+"/agent/runs", tok, map[string]any{"instruction": "Read company tax context"})
	if rec.Code != http.StatusAccepted {
		t.Fatal(rec.Body.String())
	}
	pollUntilQuiet(t, srv)
	if r := profileResult(t, m, 0); r["ok"] != false {
		t.Fatalf("company read bypassed human denial: %v", r)
	}
}
