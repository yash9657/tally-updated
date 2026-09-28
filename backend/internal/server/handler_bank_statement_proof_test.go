package server

// W5 — the mandatory balance proof on the WIRE. docs/design/d123-w5-documents-bank.md §2.3.
//
// Every refusal on the import route is a 400, so each case asserts the REASON
// (HANDOFF-SESSION-ITEM-ZERO.md §4.2), never the status alone.

import (
	"net/http"
	"strings"
	"testing"
)

// The same three movements as bankCSV, WITHOUT the running balance column.
const bankCSVNoBalance = `Value Date,Narration,Chq/Ref No.,Withdrawal Amt.,Deposit Amt.
15/04/2026,NEFT FROM ACME TRADERS,N0421,,"1,00,000.00"
18/04/2026,CHQ PAID SUPPLIER,004312,"25,000.00",
22/04/2026,BANK CHARGES,,"590.00",
`

func noBalanceMapping() map[string]any {
	m := hdfcMapping()
	delete(m, "running_balance")
	return m
}

func TestTheImportRouteRefusesAStatementItCannotProveAndAcceptsOneKeyedIn(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)
	url := f.base + "/bank-statement/" + f.bank + "/import"

	// 1. Nothing to prove it against: refused, and the refusal says what to key.
	rec, _ := aclRequest(t, s, http.MethodPost, url, f.tok, importBody(map[string]any{
		"content": bankCSVNoBalance, "mapping": noBalanceMapping(),
	}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "cannot be balance-proved") {
		t.Fatalf("an unprovable statement must be refused by reason; got %d %s", rec.Code, rec.Body.String())
	}

	// 2. A keyed closing that does not foot: refused by the proof, naming the gap.
	rec, _ = aclRequest(t, s, http.MethodPost, url, f.tok, importBody(map[string]any{
		"content": bankCSVNoBalance, "mapping": noBalanceMapping(),
		"declared_opening_balance": "2,50,000.00", "declared_closing_balance": "324410.01",
	}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "balance proof failed") {
		t.Fatalf("a closing one paisa out must be refused by the proof; got %d %s", rec.Code, rec.Body.String())
	}

	// 3. A keyed balance that is not a number is refused BEFORE the engine.
	rec, _ = aclRequest(t, s, http.MethodPost, url, f.tok, importBody(map[string]any{
		"content": bankCSVNoBalance, "mapping": noBalanceMapping(),
		"declared_opening_balance": "two lakh", "declared_closing_balance": "324410",
	}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "declared_opening_balance") {
		t.Fatalf("a non-numeric keyed balance must be refused by field; got %d %s", rec.Code, rec.Body.String())
	}

	// 4. Footing: imported, and the response says HOW it was proved.
	rec, body := aclRequest(t, s, http.MethodPost, url, f.tok, importBody(map[string]any{
		"content": bankCSVNoBalance, "mapping": noBalanceMapping(),
		"declared_opening_balance": "2,50,000.00", "declared_closing_balance": "324410.00",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("a footing statement must import: %d %s", rec.Code, rec.Body.String())
	}
	if body["balance_proof"] != "declared" || body["balance_declared_by"] != "operator" || body["balance_walked"] != false {
		t.Errorf("proof reported as %v by %v walked %v; want declared / operator / false",
			body["balance_proof"], body["balance_declared_by"], body["balance_walked"])
	}
	checks, _ := body["proof_checks"].([]any)
	if len(checks) == 0 {
		t.Error("the response must list what the proof checked")
	}

	// 5. The walked file reports the stronger proof.
	rec, body = aclRequest(t, s, http.MethodPost, url, f.tok, importBody(map[string]any{
		"file_name": "walked.csv",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("the walked file must import: %d %s", rec.Code, rec.Body.String())
	}
	if body["balance_proof"] != "running" || body["row_order"] != "oldest-first" {
		t.Errorf("walked file reported proof %v order %v", body["balance_proof"], body["row_order"])
	}
}
