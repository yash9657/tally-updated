package server

// BANK STATEMENT IMPORT — T4 slice 2a, end to end over HTTP.
//
// Design memo: docs/design/t4-slice2-model.md §8.
//
// ⚠️ EVERY REFUSAL ASSERTED HERE IS ASSERTED BY ITS REASON, NOT ITS STATUS.
// Every domain refusal on the import route answers 400, so a test that checks
// only the code passes when the parser refused for an entirely different cause
// — which is exactly how two HTTP tests in the previous session stayed green
// while sending an invalid dr_cr (HANDOFF-SESSION-ITEM-ZERO.md §4.2).

import (
	"net/http"
	"strings"
	"testing"
)

// The file every test below imports, unless it needs a broken one. Three
// movements with a running balance, so the balance walk actually runs.
const bankCSV = `Value Date,Narration,Chq/Ref No.,Withdrawal Amt.,Deposit Amt.,Closing Balance
15/04/2026,NEFT FROM ACME TRADERS,N0421,,"1,00,000.00","3,50,000.00"
18/04/2026,CHQ PAID SUPPLIER,004312,"25,000.00",,"3,25,000.00"
22/04/2026,BANK CHARGES,,"590.00",,"3,24,410.00"
`

// hdfcMapping is the mapping the operator confirms for the file above. It is
// spelled out in the test rather than taken from the preview on purpose: these
// tests must fail if the ENGINE's reading changes, not merely stay consistent
// with whatever the proposal happens to say.
func hdfcMapping() map[string]any {
	return map[string]any{
		"encoding":        "two-columns",
		"date_layout":     "dd/mm/yyyy",
		"value_date":      "Value Date",
		"description":     "Narration",
		"deposit":         "Deposit Amt.",
		"withdrawal":      "Withdrawal Amt.",
		"running_balance": "Closing Balance",
		"cheque_number":   "Chq/Ref No.",
	}
}

type stmtFixture struct {
	companyID string
	base      string
	tok       string
	bank      string // the bank ledger, with an account number recorded
}

// newStmtFixture makes a company with a BANK ledger that carries an account
// number and IFSC — migration 0064's columns WITH DATA, which is invariant I-11.
func newStmtFixture(t *testing.T, s *Server) stmtFixture {
	t.Helper()
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	rec, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok, map[string]any{
		"name": "HDFC Current STMT", "group_name": "Bank Accounts",
		"bank_account_number": "50200012345678",
		"bank_ifsc":           "HDFC0004321",
		"created_by":          "owner",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bank ledger = %d (%s)", rec.Code, rec.Body.String())
	}
	bank, _ := body["ledger_id"].(string)
	return stmtFixture{companyID: companyID, base: base, tok: ownerTok, bank: bank}
}

func importBody(extra map[string]any) map[string]any {
	b := map[string]any{
		"file_name":  "hdfc-apr-2026.csv",
		"content":    bankCSV,
		"mapping":    hdfcMapping(),
		"created_by": "owner",
	}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

// TestImportingAStatementStoresWhatTheBankSaid is the deliverable of slice 2a:
// an operator points at their bank's CSV and the rows are in the product.
func TestImportingAStatementStoresWhatTheBankSaid(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)

	rec, body := aclRequest(t, s, http.MethodPost,
		f.base+"/bank-statement/"+f.bank+"/import", f.tok,
		importBody(map[string]any{"statement_account_number": "50200012345678"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["lines_in_file"] != float64(3) || body["new_lines"] != float64(3) {
		t.Errorf("want 3 lines in file and 3 new; got %v and %v",
			body["lines_in_file"], body["new_lines"])
	}
	if body["balance_walked"] != true {
		t.Error("this file carries a running balance on every row, so the walk MUST have run")
	}
	// Opening is DERIVED from the first row, never taken on trust: 3,50,000
	// closing less the 1,00,000 that came in.
	if body["opening_balance"] != "250000.0000" {
		t.Errorf("opening_balance = %v, want 250000.0000", body["opening_balance"])
	}
	if body["closing_balance"] != "324410.0000" {
		t.Errorf("closing_balance = %v, want 324410.0000", body["closing_balance"])
	}
	// Both account numbers were present and agreed, so there is nothing this
	// import could not check.
	if d, _ := body["disclosures"].([]any); len(d) != 0 {
		t.Errorf("everything checkable was checked here; want no disclosures, got %v", d)
	}

	// And it reads back.
	rec, got := aclRequest(t, s, http.MethodGet, f.base+"/bank-statement/"+f.bank, f.tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read back = %d (%s)", rec.Code, rec.Body.String())
	}
	lines, _ := got["lines"].([]any)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines back, got %d", len(lines))
	}

	// ⚠️ THE INVERSION, END TO END. A DEPOSIT reaches the client as 'D',
	// because in OUR books the bank asset rose — even though the bank's own
	// statement calls it a credit. If this flips, every deposit compares
	// unequal to the receipt that created it and slice 2b matches nothing,
	// which reads on screen as "nothing to reconcile" rather than as a bug.
	first, _ := lines[0].(map[string]any)
	if first["dr_cr"] != "D" || first["amount"] != "100000.0000" {
		t.Errorf("the deposit must arrive as D/100000.0000; got %v/%v", first["dr_cr"], first["amount"])
	}
	if first["value_date"] != "2026-04-15" {
		t.Errorf("value_date = %v, want 2026-04-15 (dd/mm/yyyy read as declared)", first["value_date"])
	}
	second, _ := lines[1].(map[string]any)
	if second["dr_cr"] != "C" {
		t.Errorf("the cheque paid out must arrive as C; got %v", second["dr_cr"])
	}
	if second["cheque_number"] != "004312" {
		t.Errorf("cheque_number = %v, want 004312", second["cheque_number"])
	}
	if got["total_in"] != "100000.0000" || got["total_out"] != "25590.0000" {
		t.Errorf("totals: in=%v out=%v, want 100000.0000 / 25590.0000", got["total_in"], got["total_out"])
	}

	// I-11: the account identity is on the report, and it is REAL data rather
	// than a blank labelled field.
	if got["bank_account_number"] != "50200012345678" || got["bank_ifsc"] != "HDFC0004321" {
		t.Errorf("the account identity must be carried: %v / %v",
			got["bank_account_number"], got["bank_ifsc"])
	}
}

// THE REPORT MUST SAY THAT MATCHING IS NOT BUILT. Rows of bank movements with
// no status column otherwise read as "none of these matched", which is a
// different and false statement.
func TestTheImportedStatementSaysMatchingIsNotBuiltYet(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)
	if rec, _ := aclRequest(t, s, http.MethodPost,
		f.base+"/bank-statement/"+f.bank+"/import", f.tok, importBody(nil)); rec.Code != http.StatusOK {
		t.Fatalf("import = %d (%s)", rec.Code, rec.Body.String())
	}

	_, got := aclRequest(t, s, http.MethodGet, f.base+"/bank-statement/"+f.bank, f.tok, nil)
	joined := ""
	for _, d := range got["disclosures"].([]any) {
		joined += " " + d.(string)
	}
	if !strings.Contains(joined, "MATCHING IS NOT BUILT YET") {
		t.Errorf("the report must state that nothing here is matched or cleared; got %q", joined)
	}
	// It must also point at where reconciling IS done, or the disclosure is a
	// dead end.
	if !strings.Contains(joined, "Bank Reconciliation screen") {
		t.Errorf("the disclosure must say where reconciling is actually done; got %q", joined)
	}
}

// B-3, the one an operator hits in week one having clicked twice. The same file
// again is a SUCCESS and a no-op, not an error and not a doubled statement.
func TestReImportingTheSameFileIsANoOp(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)
	path := f.base + "/bank-statement/" + f.bank + "/import"

	_, first := aclRequest(t, s, http.MethodPost, path, f.tok, importBody(nil))
	if first["new_lines"] != float64(3) {
		t.Fatalf("CONTROL: the first import must add 3 lines; got %v", first["new_lines"])
	}

	rec, second := aclRequest(t, s, http.MethodPost, path, f.tok, importBody(nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("a repeat import = %d, want 200: the operator's intent is satisfied either way (%s)",
			rec.Code, rec.Body.String())
	}
	if second["already_imported"] != true {
		t.Error("a repeat of the same file must be flagged already_imported, not silently re-done")
	}
	if second["new_lines"] != float64(0) {
		t.Errorf("a repeat must add NOTHING; it added %v", second["new_lines"])
	}
	if second["import_id"] != first["import_id"] {
		t.Errorf("a repeat must point at the ORIGINAL import (%v), not a new empty one (%v)",
			first["import_id"], second["import_id"])
	}

	// And the statement itself did not double.
	_, got := aclRequest(t, s, http.MethodGet, f.base+"/bank-statement/"+f.bank, f.tok, nil)
	if lines, _ := got["lines"].([]any); len(lines) != 3 {
		t.Fatalf("after two imports of one file the account must hold 3 lines, not %d", len(lines))
	}
}

// THE REAL-WORLD CASE, which a per-file hash alone would miss entirely: an
// operator downloads 15–30 April after already importing 15–22 April. The
// overlapping rows must not double.
func TestOverlappingStatementsDoNotDoubleTheOverlap(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)
	path := f.base + "/bank-statement/" + f.bank + "/import"

	if rec, _ := aclRequest(t, s, http.MethodPost, path, f.tok, importBody(nil)); rec.Code != http.StatusOK {
		t.Fatalf("first import = %d (%s)", rec.Code, rec.Body.String())
	}

	// A DIFFERENT file (so the file hash differs) whose first two rows are the
	// same two transactions, plus one new one.
	overlapping := `Value Date,Narration,Chq/Ref No.,Withdrawal Amt.,Deposit Amt.,Closing Balance
18/04/2026,CHQ PAID SUPPLIER,004312,"25,000.00",,"3,25,000.00"
22/04/2026,BANK CHARGES,,"590.00",,"3,24,410.00"
28/04/2026,NEFT FROM BETA LTD,N0428,,"40,000.00","3,64,410.00"
`
	rec, body := aclRequest(t, s, http.MethodPost, path, f.tok, importBody(map[string]any{
		"file_name": "hdfc-apr-2026-part2.csv", "content": overlapping,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("overlapping import = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["already_imported"] != false {
		t.Error("a DIFFERENT file is not an already-imported file, even when its rows overlap")
	}
	if body["new_lines"] != float64(1) || body["duplicate_lines"] != float64(2) {
		t.Errorf("want 1 new and 2 duplicate rows; got %v new, %v duplicate",
			body["new_lines"], body["duplicate_lines"])
	}
	// The operator is TOLD, rather than left to wonder why 3 rows became 1.
	joined := ""
	for _, d := range body["disclosures"].([]any) {
		joined += " " + d.(string)
	}
	if !strings.Contains(joined, "already on this account") {
		t.Errorf("the skipped rows must be disclosed; got %q", joined)
	}

	_, got := aclRequest(t, s, http.MethodGet, f.base+"/bank-statement/"+f.bank, f.tok, nil)
	if lines, _ := got["lines"].([]any); len(lines) != 4 {
		t.Fatalf("three rows plus one new one is FOUR on the account, not %d", len(lines))
	}
}

// B-4 over HTTP. The refusal must name the row, or the operator has a
// spreadsheet and nowhere to look.
func TestAStatementWhoseBalancesDoNotWalkIsRefusedByReason(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)

	// The 18/04 cheque is missing; the balances either side no longer walk.
	dropped := `Value Date,Narration,Chq/Ref No.,Withdrawal Amt.,Deposit Amt.,Closing Balance
15/04/2026,NEFT FROM ACME TRADERS,N0421,,"1,00,000.00","3,50,000.00"
22/04/2026,BANK CHARGES,,"590.00",,"3,24,410.00"
`
	rec, _ := aclRequest(t, s, http.MethodPost,
		f.base+"/bank-statement/"+f.bank+"/import", f.tok,
		importBody(map[string]any{"content": dropped}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a file that does not walk = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	// ⚠️ THE REASON, NOT THE CODE. Every refusal on this route is a 400.
	msg := rec.Body.String()
	for _, want := range []string{"do not walk", "row 3", "NOT imported"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must contain %q so it is actionable; got %s", want, msg)
		}
	}

	// AND NOTHING WAS STORED. A refusal that half-imported would be worse than
	// no check at all.
	_, got := aclRequest(t, s, http.MethodGet, f.base+"/bank-statement/"+f.bank, f.tok, nil)
	if lines, _ := got["lines"].([]any); len(lines) != 0 {
		t.Fatalf("a refused import must store nothing; %d lines landed", len(lines))
	}
}

// The refusal that stops a morning's work going into the wrong account.
func TestAStatementForAnotherAccountIsRefused(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)

	rec, _ := aclRequest(t, s, http.MethodPost,
		f.base+"/bank-statement/"+f.bank+"/import", f.tok,
		importBody(map[string]any{"statement_account_number": "99999999999999"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a file for another account = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	msg := rec.Body.String()
	if !strings.Contains(msg, "99999999999999") || !strings.Contains(msg, "50200012345678") {
		t.Errorf("the refusal must show BOTH numbers, or the operator cannot tell which is wrong: %s", msg)
	}
	if !strings.Contains(msg, "nothing has been imported") {
		t.Errorf("the refusal must state that nothing landed: %s", msg)
	}
}

// The same number written the way a bank prints it must still MATCH. A safety
// check that refuses correct imports gets turned off.
func TestAccountNumberPunctuationDoesNotCauseAFalseRefusal(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)

	rec, _ := aclRequest(t, s, http.MethodPost,
		f.base+"/bank-statement/"+f.bank+"/import", f.tok,
		importBody(map[string]any{"statement_account_number": "50200 0123 45678"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("the same account with spaces = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// When the check CANNOT run, the import says so. Silence would read as "the
// account was verified".
func TestAnUncheckableAccountIsDisclosedRatherThanIgnored(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)

	// The ledger has a number; the request supplies none.
	_, body := aclRequest(t, s, http.MethodPost,
		f.base+"/bank-statement/"+f.bank+"/import", f.tok, importBody(nil))
	joined := ""
	for _, d := range body["disclosures"].([]any) {
		joined += " " + d.(string)
	}
	if !strings.Contains(joined, "was not checked") {
		t.Errorf("an unperformed check must be stated, never implied by silence; got %q", joined)
	}
}

// A statement can only be imported against a BANK account, and the refusal
// names the ledger rather than handing back an id.
func TestImportingAgainstANonBankLedgerIsRefusedByName(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)

	_, body := aclRequest(t, s, http.MethodPost, f.base+"/ledgers", f.tok, map[string]any{
		"name": "Scrap Sales STMT", "group_name": "Sales Accounts", "created_by": "owner",
	})
	sales, _ := body["ledger_id"].(string)

	rec, _ := aclRequest(t, s, http.MethodPost,
		f.base+"/bank-statement/"+sales+"/import", f.tok, importBody(nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("importing against a Sales ledger = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Scrap Sales STMT") {
		t.Errorf("the refusal must NAME the ledger: %s", rec.Body.String())
	}
}

// The preview is the whole reason there are no built-in per-bank layouts: it
// proposes from the file's own header and REFUSES to settle what the file
// cannot.
func TestThePreviewProposesFromTheFileAndFlagsWhatItWillNotGuess(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)

	rec, body := aclRequest(t, s, http.MethodPost,
		f.base+"/bank-statement/"+f.bank+"/preview", f.tok,
		map[string]any{"file_name": "hdfc.csv", "content": bankCSV})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	m, _ := body["mapping"].(map[string]any)
	if m["encoding"] != "two-columns" || m["deposit"] != "Deposit Amt." {
		t.Errorf("the proposal must read this header: %v", m)
	}
	// 15/04 and 22/04 rule out mm/dd, so the layout IS determined here.
	if m["date_layout"] != "dd/mm/yyyy" {
		t.Errorf("date_layout = %v; the file's own dates settle it", m["date_layout"])
	}
	if u, _ := body["unresolved"].([]any); len(u) != 0 {
		t.Errorf("this header is fully determined; want nothing unresolved, got %v", u)
	}

	// A file whose every date fits BOTH readings must leave the layout blank and
	// say why: choosing wrongly moves the whole statement by up to eleven months
	// while every amount stays plausible.
	ambiguous := "Value Date,Narration,Amount\n03/04/2026,ONE,-100.00\n05/06/2026,TWO,-200.00\n"
	_, body = aclRequest(t, s, http.MethodPost,
		f.base+"/bank-statement/"+f.bank+"/preview", f.tok,
		map[string]any{"file_name": "ambiguous.csv", "content": ambiguous})
	m, _ = body["mapping"].(map[string]any)
	if m["date_layout"] != "" {
		t.Errorf("an ambiguous date column must NOT be guessed; got %v", m["date_layout"])
	}
	joined := ""
	for _, u := range body["unresolved"].([]any) {
		joined += " " + u.(string)
	}
	if !strings.Contains(joined, "date_layout") || !strings.Contains(joined, "eleven months") {
		t.Errorf("the ambiguity must be reported with its consequence; got %q", joined)
	}
}

// TestDataEntryIsRefusedTheBankStatement is migration 0063, and it CREATES ITS
// COMPANY AT TEST TIME — so it exercises seed_company_auth_defaults rather than
// the backfill. §5.2 of HANDOFF-FEATURE-LOCK.md: an ACL migration that
// backfills and forgets the seeder is correct for every company that existed
// and silently wrong for every company created afterwards.
func TestDataEntryIsRefusedTheBankStatement(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)

	_, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok, map[string]any{
		"name": "HDFC ACL", "group_name": "Bank Accounts", "created_by": "owner",
	})
	bank, _ := body["ledger_id"].(string)

	// ⚠️ THE CONTROL. Migration 0011 grants Data Entry a coarse
	// `allow display report`, which reaches EVERY report:NAME at specificity 1.
	// Without asserting that grant still works, the refusal below would pass for
	// the wrong reason — a level that can read no report at all proves nothing
	// about this one.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/trial-balance?as_on=2026-07-31", dataEntry, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: Data Entry must still read the Trial Balance = %d, want 200", rec.Code)
	}

	// THE READ: refused by 0063's specificity-2 disallow.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/bank-statement/"+bank, dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("Data Entry read the imported statement = %d, want 403 — it is the bank's own "+
			"file of every counterparty, including rows no voucher exists for", rec.Code)
	}

	// THE WRITE: refused by default_access = 'none' and no coarse rule reaching
	// a bare `bank-statement`. 0063 writes no allow, unlike 0046 — see its
	// header for why importing is not the clerical act that ticking off is.
	if rec, _ := aclRequest(t, s, http.MethodPost,
		base+"/bank-statement/"+bank+"/import", dataEntry, importBody(nil)); rec.Code != http.StatusForbidden {
		t.Errorf("Data Entry imported a statement = %d, want 403", rec.Code)
	}

	// PREVIEW IS THE WRITE'S AUTHORITY, NOT THE READ'S. It echoes the uploaded
	// file back, so a level that may not import must not be able to rehearse
	// one — and being a Create rather than a Display is what keeps 0011's coarse
	// report grant away from it.
	if rec, _ := aclRequest(t, s, http.MethodPost,
		base+"/bank-statement/"+bank+"/preview", dataEntry,
		map[string]any{"file_name": "x.csv", "content": bankCSV}); rec.Code != http.StatusForbidden {
		t.Errorf("Data Entry previewed an import = %d, want 403", rec.Code)
	}

	// And an Owner may do all three.
	if rec, _ := aclRequest(t, s, http.MethodPost,
		base+"/bank-statement/"+bank+"/import", ownerTok, importBody(nil)); rec.Code != http.StatusOK {
		t.Errorf("Owner imported a statement = %d, want 200", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/bank-statement/"+bank, ownerTok, nil); rec.Code != http.StatusOK {
		t.Errorf("Owner read the imported statement = %d, want 200", rec.Code)
	}
}

// The two-touch tenancy rule for all three routes.
func TestBankStatementTenancy(t *testing.T) {
	s := payrollServer(t)
	f := newStmtFixture(t, s)
	otherTok := aclToken(t, s, "stmt-outsider-"+f.companyID[:8])

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"read", http.MethodGet, f.base + "/bank-statement/" + f.bank, nil},
		{"import", http.MethodPost, f.base + "/bank-statement/" + f.bank + "/import", importBody(nil)},
		{"preview", http.MethodPost, f.base + "/bank-statement/" + f.bank + "/preview",
			map[string]any{"content": bankCSV}},
	} {
		if rec, _ := aclRequest(t, s, tc.method, tc.path, "", tc.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with no token = %d, want 401", tc.name, rec.Code)
		}
		if rec, _ := aclRequest(t, s, tc.method, tc.path, otherTok, tc.body); rec.Code != http.StatusForbidden {
			t.Errorf("%s as another tenant = %d, want 403", tc.name, rec.Code)
		}
	}
}

// A ledger with no account number recorded is a LEGAL state, and the report
// says NOT RECORDED rather than showing a blank labelled field (I-11).
func TestALedgerWithNoAccountNumberSaysSoRatherThanShowingABlank(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	_, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok, map[string]any{
		"name": "HDFC Bare", "group_name": "Bank Accounts", "created_by": "owner",
	})
	bank, _ := body["ledger_id"].(string)

	_, got := aclRequest(t, s, http.MethodGet, base+"/bank-statement/"+bank, ownerTok, nil)
	if got["bank_account_number"] != "" || got["bank_ifsc"] != "" {
		t.Fatalf("an unrecorded account must be empty on the wire, not invented: %v / %v",
			got["bank_account_number"], got["bank_ifsc"])
	}
	joined := ""
	for _, d := range got["disclosures"].([]any) {
		joined += " " + d.(string)
	}
	if !strings.Contains(joined, "no account number or IFSC recorded") {
		t.Errorf("the report must SAY the identity is unrecorded, so the client renders words "+
			"rather than an empty box; got %q", joined)
	}
}

// A malformed IFSC is refused by the database's shape CHECK, because this
// column exists to be COMPARED against a bank's file: a typo that merely fails
// to match produces a refusal nobody can debug.
func TestAMalformedIFSCIsRefused(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	rec, _ := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok, map[string]any{
		"name": "HDFC Typo", "group_name": "Bank Accounts",
		"bank_ifsc": "HDFC1234", "created_by": "owner",
	})
	if rec.Code == http.StatusCreated {
		t.Fatal("HDFC1234 is not an IFSC (four letters, '0', six alphanumerics) and must be refused")
	}
}
