package server

// PRINTING THE BANK RECONCILIATION STATEMENT — the §3.4 slice.
//
// The report answered 501 until the route shape was decided, for a reason worth
// keeping in mind while reading these tests: /print/reports/{name} carries no
// ledger id, and a BRS is always FOR ONE BANK ACCOUNT. The decision
// (HANDOFF-PARITY-SPRINT.md §3.4, option (c)) is ?ledger_id= for one account,
// ?all=true for every account, and a 400 naming both when neither is given.
//
// THE TEST THAT MATTERS MOST IS TestPrintBankReconRefusesWithoutAScope. Every
// other test here proves the document is right; that one proves the product
// still refuses to guess WHICH account, which is the whole reason the feature
// was deferred rather than shipped with a default.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// printBankFixture is a company with TWO bank accounts and an un-cleared payment
// out of the first.
//
// TWO accounts, not one, on purpose: with a single bank account every wrong
// implementation — "print the first one", "ignore the parameter" — produces the
// right document, and the suite would pass while the actual hazard went
// untested.
type printBankFixture struct {
	base      string
	ownerTok  string
	bankOneID string
	bankTwoID string
	bankOne   string
	bankTwo   string
}

func newPrintBankFixture(t *testing.T, s *Server) printBankFixture {
	t.Helper()
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	sub := "print-bank-" + uuid.NewString()[:8]

	mk := func(name, group string) string {
		t.Helper()
		rec, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok,
			map[string]any{"name": name, "group_name": group, "created_by": sub})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d, want 201 (%s)", name, rec.Code, rec.Body.String())
		}
		id, _ := body["ledger_id"].(string)
		return id
	}

	const bankOne, bankTwo = "HDFC Current", "ICICI Current"
	bankOneID := mk(bankOne, "Bank Accounts")
	bankTwoID := mk(bankTwo, "Bank Accounts")
	rentID := mk("Rent Paid", "Indirect Expenses")

	// One cheque out of bank ONE, left un-cleared. It is what distinguishes the
	// two statements: bank two must NOT show it.
	rec, _ := aclRequest(t, s, http.MethodPost, base+"/vouchers/payment", ownerTok, map[string]any{
		"voucher_date": "2026-07-10",
		"created_by":   sub,
		"narration":    "cheque 000123",
		"postings": []map[string]any{
			{"ledger_id": rentID, "dr_cr": "D", "amount": "5000.0000"},
			{"ledger_id": bankOneID, "dr_cr": "C", "amount": "5000.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post payment = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	return printBankFixture{
		base: base, ownerTok: ownerTok,
		bankOneID: bankOneID, bankTwoID: bankTwoID,
		bankOne: bankOne, bankTwo: bankTwo,
	}
}

// TestPrintBankReconRefusesWithoutAScope is the invariant the original 501 was
// protecting, kept alive in its proper form.
//
// A bare GET must NOT print. Defaulting to "the first bank account" would put a
// real, correctly rendered statement for the WRONG ACCOUNT on paper with nothing
// on the page saying so — which is strictly worse than refusing, because it is
// signable.
func TestPrintBankReconRefusesWithoutAScope(t *testing.T) {
	s := payrollServer(t)
	f := newPrintBankFixture(t, s)

	rec := printGet(t, s, f.base+"/print/reports/bank-reconciliation", f.ownerTok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bare GET on the BRS print route = %d, want 400: a company with two bank "+
			"accounts has not said which statement it wants, and picking one silently puts the "+
			"wrong account on paper (%s)", rec.Code, rec.Body.String())
	}
	// The refusal must name BOTH ways forward. A 400 that only reports the fault
	// leaves the operator to guess the parameter.
	body := rec.Body.String()
	for _, want := range []string{"ledger_id", "all=true"} {
		if !strings.Contains(body, want) {
			t.Errorf("the BRS refusal does not mention %q, so it does not tell the operator how "+
				"to get their document: %s", want, body)
		}
	}
	if got := rec.Header().Get("Content-Type"); strings.HasPrefix(got, "application/pdf") {
		t.Errorf("the refusal returned a PDF (Content-Type %q); a refusal must not produce a "+
			"document", got)
	}
}

// TestPrintBankReconRefusesBothScopes pins that the two parameters are mutually
// exclusive rather than silently ordered.
func TestPrintBankReconRefusesBothScopes(t *testing.T) {
	s := payrollServer(t)
	f := newPrintBankFixture(t, s)

	rec := printGet(t, s,
		f.base+"/print/reports/bank-reconciliation?ledger_id="+f.bankOneID+"&all=true", f.ownerTok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("BRS print with BOTH ledger_id and all=true = %d, want 400: they are two "+
			"different documents and a precedence rule would silently discard the operator's "+
			"other intent (%s)", rec.Code, rec.Body.String())
	}
}

// TestPrintBankReconSingleAccountPrintsThatAccount is the happy path, and its
// CONTROL is the second account.
//
// Asserting only that bank one's statement mentions bank one would pass for an
// implementation that ignores ledger_id entirely and always prints the first
// account. So the same assertion is made against bank TWO, and each document is
// required NOT to carry the other's name.
func TestPrintBankReconSingleAccountPrintsThatAccount(t *testing.T) {
	s := payrollServer(t)
	f := newPrintBankFixture(t, s)

	for _, tc := range []struct {
		name, id, want, notWant string
	}{
		{"bank one", f.bankOneID, f.bankOne, f.bankTwo},
		{"bank two", f.bankTwoID, f.bankTwo, f.bankOne},
	} {
		rec := printGet(t, s,
			f.base+"/print/reports/bank-reconciliation?ledger_id="+tc.id, f.ownerTok)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: BRS print = %d, want 200 (%s)", tc.name, rec.Code, rec.Body.String())
		}
		text := pdfText(t, rec, tc.name+" BRS")
		if !strings.Contains(text, tc.want) {
			t.Errorf("%s: the printed statement does not name the account it is for (%q)",
				tc.name, tc.want)
		}
		if strings.Contains(text, tc.notWant) {
			t.Errorf("%s: the printed statement for %q also carries %q — ledger_id is being "+
				"ignored and the document is not the one that was asked for",
				tc.name, tc.want, tc.notWant)
		}
		// The three lines that make it a reconciliation rather than a list.
		for _, want := range []string{
			"Balance as per company books", "Balance as per bank", "Difference",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the printed statement is missing %q; without it the page is a "+
					"list of vouchers, not a reconciliation", tc.name, want)
			}
		}
	}
}

// TestPrintBankReconShowsUnclearedItemsOnTheAccountThatHasThem pins that the
// un-cleared item lands on bank ONE and not on bank two.
//
// This is the arithmetic half of the previous test: a statement can name the
// right account and still reconcile the wrong postings.
func TestPrintBankReconShowsUnclearedItemsOnTheAccountThatHasThem(t *testing.T) {
	s := payrollServer(t)
	f := newPrintBankFixture(t, s)

	one := pdfText(t, printGet(t, s,
		f.base+"/print/reports/bank-reconciliation?ledger_id="+f.bankOneID, f.ownerTok), "bank one")
	if !strings.Contains(one, "cheque 000123") {
		t.Errorf("bank one's statement does not list the un-cleared cheque, so the item that "+
			"explains the gap between book and bank is missing from the page:\n%s", one)
	}
	if !strings.Contains(one, "Less: amounts not yet reflected in the bank") {
		t.Errorf("bank one's statement has un-cleared items but no heading explaining what they "+
			"are; a bare list of vouchers under a balance reads as the balance's contents")
	}

	two := pdfText(t, printGet(t, s,
		f.base+"/print/reports/bank-reconciliation?ledger_id="+f.bankTwoID, f.ownerTok), "bank two")
	if strings.Contains(two, "cheque 000123") {
		t.Errorf("bank TWO's statement lists a cheque drawn on bank ONE — the statement is not "+
			"scoped to its own ledger:\n%s", two)
	}
	// The empty case must SAY it is empty. A blank space under a balance is
	// indistinguishable from a rendering fault.
	if !strings.Contains(two, "No items are awaiting clearance.") {
		t.Errorf("bank two has nothing outstanding and does not say so; a blank region under the "+
			"book balance reads as a failure to render, not as a clean account:\n%s", two)
	}
}

// TestPrintBankReconAllPrintsEveryAccount pins ?all=true.
func TestPrintBankReconAllPrintsEveryAccount(t *testing.T) {
	s := payrollServer(t)
	f := newPrintBankFixture(t, s)

	rec := printGet(t, s, f.base+"/print/reports/bank-reconciliation?all=true", f.ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("BRS print all = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	text := pdfText(t, rec, "all accounts BRS")
	for _, want := range []string{f.bankOne, f.bankTwo} {
		if !strings.Contains(text, want) {
			t.Errorf("?all=true omitted the bank account %q; an operator who asked for every "+
				"account and silently got a subset cannot tell which is missing", want)
		}
	}
	if !strings.Contains(text, "2 bank accounts in this document") {
		t.Errorf("the all-accounts document does not state how many accounts it covers, so a "+
			"missing account is invisible:\n%s", text)
	}
	// NO GRAND TOTAL. Summing several banks' balances — one of which may be an
	// overdraft — produces a figure that is on no statement and ties to nothing.
	if !strings.Contains(text, "No combined total is printed") {
		t.Errorf("the all-accounts document does not explain the absence of a combined total; a "+
			"reader will otherwise look for one and assume it was dropped:\n%s", text)
	}
	// NOTHING ON THE PAGE MAY BE ELLIPSISED. layoutRows confines a cell to its
	// own column and truncates what does not fit, so a note placed in a narrow
	// column stays on the page and stops being readable. This catches that for
	// every row at once, which no assertion on a single sentence can.
	if strings.Contains(text, "...") {
		t.Errorf("the printed statement contains an ellipsis, so at least one cell was truncated "+
			"to fit its column. A note a reader cannot finish is worse than no note:\n%s", text)
	}
}

// TestPrintBankReconRefusesANonBankLedger pins that the diagnostic refusal in
// reporting.BankReconciliationStatement reaches the print route unaltered.
//
// The check is deliberately NOT duplicated in the handler: two implementations
// of "is this a bank ledger" is how the two come to disagree, and the operator
// would get a different sentence depending on which surface they used.
func TestPrintBankReconRefusesANonBankLedger(t *testing.T) {
	s := payrollServer(t)
	f := newPrintBankFixture(t, s)

	rec, body := aclRequest(t, s, http.MethodPost, f.base+"/ledgers", f.ownerTok,
		map[string]any{"name": "Not A Bank", "group_name": "Indirect Expenses",
			"created_by": "print-bank-test"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create non-bank ledger = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	notBank, _ := body["ledger_id"].(string)

	prec := printGet(t, s,
		f.base+"/print/reports/bank-reconciliation?ledger_id="+notBank, f.ownerTok)
	if prec.Code == http.StatusOK {
		t.Fatalf("printing a BRS for a NON-BANK ledger returned 200: a reconciliation of an "+
			"expense ledger is a document about nothing (%s)", prec.Body.String())
	}
	if prec.Code != http.StatusUnprocessableEntity {
		t.Errorf("BRS print for a non-bank ledger = %d, want 422 to match the JSON statement "+
			"endpoint for the same input (%s)", prec.Code, prec.Body.String())
	}
}

// TestPrintBankReconIsGovernedByItsOwnACLName pins that the new document did not
// arrive outside the ACL vocabulary.
//
// report:bank-reconciliation was already a KnownReportName (it was mapped so its
// 501 stayed governable), so the risk here is the opposite of the usual one: not
// an unmapped route, but a route that now RETURNS DATA under a rule an
// administrator wrote when it returned nothing.
func TestPrintBankReconIsGovernedByItsOwnACLName(t *testing.T) {
	s := payrollServer(t)
	f := newPrintBankFixture(t, s)

	act, res, resName, mapped := printRouteActionFor(t, "/print/reports/bank-reconciliation")
	if !mapped || res != "report" || resName != "bank-reconciliation" {
		t.Fatalf("routeAction on the BRS print route = (%q,%q,%q,%v), want (print,report,"+
			"bank-reconciliation,true): a report the router cannot name is one no ACL rule can "+
			"govern", act, res, resName, mapped)
	}

	// A Data Entry token must not print it. 0038 seeds no `allow print report:NAME`
	// for that level, so this is the default-deny working — and it is worth
	// pinning HERE because the route's status changed from "501 for everyone" to
	// "a real document for whoever is allowed".
	dataEntry := aclSeededDataEntryToken(t, s, f.base, f.ownerTok)
	rec := printGet(t, s,
		f.base+"/print/reports/bank-reconciliation?ledger_id="+f.bankOneID, dataEntry)
	if rec.Code == http.StatusOK {
		t.Errorf("a Data Entry token printed the Bank Reconciliation Statement: migration 0038 "+
			"seeds no `allow print report:...` for that level, so this route now hands out a "+
			"document the ACL was never asked about (%s)", rec.Body.String())
	}
}
