package server

// THE s.394(2) DECLARATION ROUTE — Item Zero step 3.
//
// The route and the post-path wiring landed together on purpose: a declaration
// that exempts nothing is a document store, and a post path that reads a table
// nobody can write is unreachable. TestFilingADeclarationExemptsTheNextSale is
// the test that proves the pair actually meets.

import (
	"net/http"
	"strings"
	"testing"
)

// tcsHTTPFixture is a company with the ledgers a scrap sale needs.
type tcsHTTPFixture struct {
	base   string
	tok    string
	sales  string
	tcsPay string
	buyer  string
}

func newTCSHTTPFixture(t *testing.T, s *Server) tcsHTTPFixture {
	t.Helper()
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	newLedger := func(name, group string) string {
		rec, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok,
			map[string]any{"name": name, "group_name": group, "created_by": "owner"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d (%s)", name, rec.Code, rec.Body.String())
		}
		id, _ := body["ledger_id"].(string)
		return id
	}
	return tcsHTTPFixture{
		base:   base,
		tok:    ownerTok,
		sales:  newLedger("Scrap Sales HTTP", "Sales Accounts"),
		tcsPay: newLedger("TCS Payable HTTP", "Duties & Taxes"),
		buyer:  newLedger("Foundry Buyer HTTP", "Sundry Debtors"),
	}
}

// scrapSale builds a scrap-sale body. tcs "" posts no TCS leg.
func (f tcsHTTPFixture) scrapSale(no, base, tcs, receivable string) map[string]any {
	postings := []any{
		map[string]any{"ledger_id": f.buyer, "dr_cr": "D", "amount": receivable},
		map[string]any{"ledger_id": f.sales, "dr_cr": "C", "amount": base},
	}
	if tcs != "" {
		postings = append(postings,
			map[string]any{"ledger_id": f.tcsPay, "dr_cr": "C", "amount": tcs})
	}
	return map[string]any{
		"voucher_no":      no,
		"voucher_date":    "2026-05-10",
		"party_ledger_id": f.buyer,
		"created_by":      "owner",
		"postings":        postings,
		"tcs": map[string]any{
			"section":         "206C(1)-SCRAP",
			"buyer_ledger_id": f.buyer,
			"pan_available":   true,
			"taxable_amount":  base,
			"tcs_ledger_id":   f.tcsPay,
		},
	}
}

// TestTCSDeclarationAuthMatrix pins the two-touch rule and the ACL decision in
// authz.KnownResources: a declaration zeroes tax on every future sale to a named
// buyer, so it is not clerical by default.
func TestTCSDeclarationAuthMatrix(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)
	otherTok := aclToken(t, s, "acl-outsider-"+companyID[:8])

	rec, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok,
		map[string]any{"name": "Buyer ACL", "group_name": "Sundry Debtors", "created_by": "owner"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create buyer = %d", rec.Code)
	}
	buyer, _ := body["ledger_id"].(string)

	decl := map[string]any{
		"buyer_ledger_id":  buyer,
		"section":          "206C(1)-SCRAP",
		"tax_year_start":   2026,
		"declaration_date": "2026-04-01",
		"received_date":    "2026-04-03",
		"buyer_pan":        "AAAPZ1234C",
		"recorded_by":      "owner",
	}

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, base + "/tcs-declarations", decl},
		{http.MethodGet, base + "/tcs-declarations", nil},
	} {
		if rec, _ := aclRequest(t, s, tc.method, tc.path, "", tc.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with no token = %d, want 401", tc.method, rec.Code)
		}
		if rec, _ := aclRequest(t, s, tc.method, tc.path, otherTok, tc.body); rec.Code != http.StatusForbidden {
			t.Errorf("%s as another tenant = %d, want 403", tc.method, rec.Code)
		}
		// A declaration ZEROES tax on every future sale to that buyer, and its
		// absence is not visible on the invoice. Default-denied for Data Entry.
		if rec, _ := aclRequest(t, s, tc.method, tc.path, dataEntry, tc.body); rec.Code != http.StatusForbidden {
			t.Errorf("%s as Data Entry = %d, want 403 — filing a declaration zeroes "+
				"tax on a named customer's sales", tc.method, rec.Code)
		}
	}

	// Owner: 201 on the write, 200 on the read. A missing routeAction entry
	// would make BOTH of these 403.
	if rec, _ := aclRequest(t, s, http.MethodPost, base+"/tcs-declarations", ownerTok, decl); rec.Code != http.StatusCreated {
		t.Errorf("POST as Owner = %d, want 201", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/tcs-declarations", ownerTok, nil); rec.Code != http.StatusOK {
		t.Errorf("GET as Owner = %d, want 200", rec.Code)
	}
}

// TestFilingADeclarationExemptsTheNextSale IS THE POINT OF STEP 3. Three arms,
// entirely over HTTP:
//
//  1. a scrap sale collects 2%;
//  2. after a declaration is filed, the SAME sale collects nothing;
//  3. and it is recorded as EXEMPT, not as an absence.
//
// Arm 1 is what makes the rest non-vacuous: without it the test would pass
// against a build that never collected anything.
func TestFilingADeclarationExemptsTheNextSale(t *testing.T) {
	s := payrollServer(t)
	f := newTCSHTTPFixture(t, s)

	// 1. No declaration: 2% of ₹1,00,000.
	rec, body := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/sales", f.tok,
		f.scrapSale("SCRAP-1", "100000.0000", "2000.0000", "102000.0000"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("scrap sale = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	tcs, _ := body["tcs"].(map[string]any)
	if tcs["tcs"] != "2000.0000" {
		t.Fatalf("collected %v, want 2000.0000 (2%%)", tcs["tcs"])
	}
	if tcs["declaration"] != "NONE" {
		t.Errorf("declaration = %v, want NONE", tcs["declaration"])
	}

	// 2. File the declaration.
	if rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/tcs-declarations", f.tok,
		map[string]any{
			"buyer_ledger_id":  f.buyer,
			"section":          "206C(1)-SCRAP",
			"tax_year_start":   2026,
			"declaration_date": "2026-04-01",
			"received_date":    "2026-04-03",
			"buyer_pan":        "AAAPZ1234C",
			"recorded_by":      "owner",
		}); rec.Code != http.StatusCreated {
		t.Fatalf("file declaration = %d (%s)", rec.Code, rec.Body.String())
	}

	// 3. The same sale, now exempt — and a voucher that still posted a TCS leg
	// would be REFUSED, which is what makes this an assertion rather than a
	// coincidence.
	rec, body = aclRequest(t, s, http.MethodPost, f.base+"/vouchers/sales", f.tok,
		f.scrapSale("SCRAP-2", "100000.0000", "", "100000.0000"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("after the declaration the sale must collect nothing: %d (%s)",
			rec.Code, rec.Body.String())
	}
	tcs, _ = body["tcs"].(map[string]any)
	if tcs["tcs"] != "0.0000" {
		t.Fatalf("collected %v, want 0.0000", tcs["tcs"])
	}
	// The fact a zero cannot carry on its own.
	if tcs["declaration"] != "EXEMPT" {
		t.Fatalf("declaration = %v, want EXEMPT — a zero with NONE would claim this "+
			"sale was below a threshold rather than covered by a filed document",
			tcs["declaration"])
	}
}

// TestDeclarationAgainstMotorVehiclesIsRefused is V-9. s.394(2) reaches Table
// Sl. Nos. 1 to 5; motor vehicles are Sl. No. 6. An operator who files one
// believes their buyer is exempt, so this is a refusal rather than a row that
// quietly never matches.
func TestDeclarationAgainstMotorVehiclesIsRefused(t *testing.T) {
	s := payrollServer(t)
	f := newTCSHTTPFixture(t, s)

	rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/tcs-declarations", f.tok,
		map[string]any{
			"buyer_ledger_id":  f.buyer,
			"section":          "206C(1F)-MV",
			"tax_year_start":   2026,
			"declaration_date": "2026-04-01",
			"received_date":    "2026-04-03",
			"buyer_pan":        "AAAPZ1234C",
			"recorded_by":      "owner",
		})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a declaration against Sl. No. 6 = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Sl. Nos. 1 to 5") {
		t.Fatalf("400 for the wrong reason: %s", rec.Body.String())
	}
}

// TestDeclarationListDerivesTheSection394_3Deadline pins the obligation the
// document creates. s.394(3): a copy must reach the Commissioner "on or before
// the seventh day of the month following the month of receipt".
//
// Storing declarations WITHOUT surfacing that date would build a compliance
// trap — the operator files the document and acquires a deadline nobody shows
// them.
func TestDeclarationListDerivesTheSection394_3Deadline(t *testing.T) {
	s := payrollServer(t)
	f := newTCSHTTPFixture(t, s)

	if rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/tcs-declarations", f.tok,
		map[string]any{
			"buyer_ledger_id":  f.buyer,
			"section":          "206C(1)-SCRAP",
			"tax_year_start":   2026,
			"declaration_date": "2026-04-01",
			"received_date":    "2026-04-28",
			"buyer_pan":        "AAAPZ1234C",
			"recorded_by":      "owner",
		}); rec.Code != http.StatusCreated {
		t.Fatalf("file declaration = %d", rec.Code)
	}

	_, body := aclRequest(t, s, http.MethodGet, f.base+"/tcs-declarations", f.tok, nil)
	rows, _ := body["tcs_declarations"].([]any)
	if len(rows) != 1 {
		t.Fatalf("want 1 declaration, got %d", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	// Received 28 April -> due the 7th of MAY.
	if row["forward_due_date"] != "2026-05-07" {
		t.Errorf("forward_due_date = %v, want 2026-05-07 (the 7th of the month "+
			"FOLLOWING receipt)", row["forward_due_date"])
	}
	if row["pan_valid"] != true {
		t.Errorf("pan_valid = %v, want true", row["pan_valid"])
	}
	// The buyer's name is resolved so the screen shows who, not a UUID.
	if row["buyer_name"] != "Foundry Buyer HTTP" {
		t.Errorf("buyer_name = %v", row["buyer_name"])
	}
}

// TestDeclarationWithNoPANIsSTOREDNotRefused is the counterweight to the
// refusals above, and the one most likely to be "tidied" into a rejection.
//
// A PAN-less declaration is a real document in an INVALID state: s.397(2)(f)(i)
// invalidates it and (g) then requires the PENAL rate. Refusing it here would
// erase the fact that makes 5% correct, and the buyer would be charged 2%.
func TestDeclarationWithNoPANIsSTOREDNotRefused(t *testing.T) {
	s := payrollServer(t)
	f := newTCSHTTPFixture(t, s)

	if rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/tcs-declarations", f.tok,
		map[string]any{
			"buyer_ledger_id":  f.buyer,
			"section":          "206C(1)-SCRAP",
			"tax_year_start":   2026,
			"declaration_date": "2026-04-01",
			"received_date":    "2026-04-03",
			"buyer_pan":        "",
			"recorded_by":      "owner",
		}); rec.Code != http.StatusCreated {
		t.Fatalf("a declaration with no PAN must be STORED (it is what makes the "+
			"penal rate correct), got %d (%s)", rec.Code, rec.Body.String())
	}

	_, body := aclRequest(t, s, http.MethodGet, f.base+"/tcs-declarations", f.tok, nil)
	rows, _ := body["tcs_declarations"].([]any)
	row, _ := rows[0].(map[string]any)
	if row["pan_valid"] != false {
		t.Errorf("pan_valid = %v, want false", row["pan_valid"])
	}

	// And the sale collects the PENAL 5%, not the base 2% and not zero.
	rec, vbody := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/sales", f.tok,
		f.scrapSale("SCRAP-NOPAN", "100000.0000", "5000.0000", "105000.0000"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("penal-rate sale = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	tcs, _ := vbody["tcs"].(map[string]any)
	if tcs["tcs"] != "5000.0000" || tcs["declaration"] != "INVALID_NO_PAN" {
		t.Fatalf("got %v — want 5000.0000 / INVALID_NO_PAN", tcs)
	}
}
