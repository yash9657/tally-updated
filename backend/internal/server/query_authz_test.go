package server_test

// query_authz_test.go pins §6.10: POST /query is judged per ENTITY against the
// reports whose figures the entity reveals, not only by the route's `query`
// right. Each refusal below has a control in the same test — the same member,
// a query the level CAN read, answered 200 — so a 403 can only mean the entity
// check refused it, never that the member, token or fixture was broken.

import (
	"net/http"
	"testing"

	"github.com/khata/cored/internal/query"
)

// countQuery is the smallest valid query on any entity: count(*) is legal on
// every one (query.Catalog's universal metric).
func countQuery(entity query.Entity, filters ...map[string]any) map[string]any {
	dsl := map[string]any{
		"entity":  string(entity),
		"metrics": []map[string]any{{"func": "count", "column": "*", "as": "n"}},
	}
	if len(filters) > 0 {
		dsl["filters"] = filters
	}
	return map[string]any{"dsl": dsl}
}

func natureIs(v string) map[string]any {
	return map[string]any{"column": "nature", "op": "eq", "values": []string{v}}
}

func TestQueryEntityHonoursReportGrants(t *testing.T) {
	srv := newAgentServer(t)
	b := newAgentBooks(t, srv)
	ask := func(t *testing.T, tok string, body map[string]any) int {
		t.Helper()
		rec, _ := doRaw(t, srv, http.MethodPost, b.base+"/query", tok, body)
		return rec.Code
	}
	expect := func(t *testing.T, tok, what string, body map[string]any, want int) {
		t.Helper()
		if got := ask(t, tok, body); got != want {
			t.Errorf("%s: POST /query = %d, want %d", what, got, want)
		}
	}

	t.Run("full access reads every entity", func(t *testing.T) {
		tok := memberWithRules(t, srv, b, nil)
		for _, doc := range query.Catalog() {
			expect(t, tok, "entity "+doc.Entity, countQuery(query.Entity(doc.Entity)), http.StatusOK)
		}
	})

	t.Run("bills-payable denied: payables are refused, receivables still answer", func(t *testing.T) {
		tok := memberWithRules(t, srv, b, []map[string]string{
			{"effect": "disallow", "action": "display", "resource": "report:bills-payable"}})
		// The row §6.10 records: no nature filter returns supplier bills too.
		expect(t, tok, "outstandings, no filter", countQuery(query.EntityOutstandings), http.StatusForbidden)
		expect(t, tok, "outstandings, nature=LIABILITY", countQuery(query.EntityOutstandings, natureIs("LIABILITY")), http.StatusForbidden)
		// Control: the same member may read what customers owe.
		expect(t, tok, "outstandings, nature=ASSET", countQuery(query.EntityOutstandings, natureIs("ASSET")), http.StatusOK)
	})

	t.Run("day-book denied: vouchers and postings refused, balances still answer", func(t *testing.T) {
		tok := memberWithRules(t, srv, b, []map[string]string{
			{"effect": "disallow", "action": "display", "resource": "report:day-book"}})
		expect(t, tok, "vouchers", countQuery(query.EntityVouchers), http.StatusForbidden)
		expect(t, tok, "postings", countQuery(query.EntityPostings), http.StatusForbidden)
		expect(t, tok, "ledger_balances (control)", countQuery(query.EntityLedgerBalances), http.StatusOK)
	})

	t.Run("trial-balance denied: ledger balances refused, vouchers still answer", func(t *testing.T) {
		tok := memberWithRules(t, srv, b, []map[string]string{
			{"effect": "disallow", "action": "display", "resource": "report:trial-balance"}})
		expect(t, tok, "ledger_balances", countQuery(query.EntityLedgerBalances), http.StatusForbidden)
		expect(t, tok, "vouchers (control)", countQuery(query.EntityVouchers), http.StatusOK)
	})

	t.Run("stock and bank reports gate their entities", func(t *testing.T) {
		tok := memberWithRules(t, srv, b, []map[string]string{
			{"effect": "disallow", "action": "display", "resource": "report:stock-movement"},
			{"effect": "disallow", "action": "display", "resource": "report:bank-statement"}})
		expect(t, tok, "stock_movements", countQuery(query.EntityStockMovements), http.StatusForbidden)
		expect(t, tok, "bank_statement_lines", countQuery(query.EntityBankStatementLines), http.StatusForbidden)
		expect(t, tok, "vouchers (control)", countQuery(query.EntityVouchers), http.StatusOK)
	})

	t.Run("profile facts need every report a fact summarises", func(t *testing.T) {
		tok := memberWithRules(t, srv, b, []map[string]string{
			{"effect": "disallow", "action": "display", "resource": "report:stock-summary"}})
		expect(t, tok, "profile_facts", countQuery(query.EntityProfileFacts), http.StatusForbidden)
		expect(t, tok, "vouchers (control)", countQuery(query.EntityVouchers), http.StatusOK)
	})

	t.Run("a malformed query keeps its 422 for a restricted member", func(t *testing.T) {
		tok := memberWithRules(t, srv, b, []map[string]string{
			{"effect": "disallow", "action": "display", "resource": "report:day-book"}})
		expect(t, tok, "unknown entity", countQuery(query.Entity("not_an_entity")), http.StatusUnprocessableEntity)
	})
}
