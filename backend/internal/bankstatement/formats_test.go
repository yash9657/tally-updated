package bankstatement

// W5 — five bank export layouts, end to end: the header is FOUND, the bank is
// RECOGNISED by its header names, the mapping is PROPOSED, and the statement is
// PARSED and PROVED. Design memo: docs/design/d123-w5-documents-bank.md §2.
//
// ⚠️ THESE FIXTURES ARE SYNTHETIC. No real HDFC/ICICI/SBI/Axis/Kotak export
// exists in this environment (profiles.go says so, and every profile ships
// Verified=false). Each fixture is built to exercise one hard shape a real
// download has — a preamble, a separator row, a footer summary, tab separation
// under an ".xls" name, newest-first order, a repeated header, an overdraft —
// and every figure in it foots, so the proof has something true to check.
//
// EVERY TEST RUNS THE FILE THROUGH ProposeMapping AND THEN ParseStatement WITH
// THE PROPOSED MAPPING. Testing the parser with a hand-written mapping would
// prove the parser and not the proposal, and it is the proposal an operator
// confirms.

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

const hdfcExport = `HDFC BANK Ltd.,,,,,,
Account Branch : PUNE CAMP,,,,,,
Account No : 50100012345678,,,,,,
Statement From : 01/04/26 To : 30/04/26,,,,,,
,,,,,,
Date,Narration,Chq./Ref.No.,Value Dt,Withdrawal Amt.,Deposit Amt.,Closing Balance
********,********,********,********,********,********,********
01/04/26,UPI-ACME TRADERS-acme.traders@okhdfcbank-HDFC0001234-412345678901-INV 42,0000412345678901,01/04/26,,"25,000.00","1,75,000.00"
03/04/26,NEFT CR-ICIC0000104-BHARAT STEEL LTD-HDFCN52026040312345678,HDFCN52026040312345678,03/04/26,,"1,18,000.00","2,93,000.00"
05/04/26,CHQ PAID-MICR CTS-PUNE-STEEL SUPPLIERS,000123,05/04/26,"50,000.00",,"2,43,000.00"
10/04/26,IMPS-412398765432-RAVI KUMAR-SBIN-XXXXXXXX4321-SALARY,0000412398765432,10/04/26,"12,000.00",,"2,31,000.00"
,,,,,,
STATEMENT SUMMARY :-,,,,,,
Opening Balance,Dr Count,Cr Count,Debits,Credits,Closing Bal,
"1,50,000.00",2,2,"62,000.00","1,43,000.00","2,31,000.00",
`

const iciciExport = `DETAILED STATEMENT,,,,,,,
Transactions List - ACME INDUSTRIES (INR) - 000405001234,,,,,,,
,,,,,,,
S No.,Value Date,Transaction Date,Cheque Number,Transaction Remarks,Withdrawal Amount (INR ),Deposit Amount (INR ),Balance (INR )
1,02/04/2026,02/04/2026,,UPI/412311112222/Payment for goods/ravi.k@okaxis/AXIS BANK/,,5000.00,105000.00
2,15/04/2026,15/04/2026,,NEFT-HDFCN52026041500001111-PUNE FABRICATORS-INV 7,21240.00,,83760.00
3,20/04/2026,20/04/2026,000456,CLG/SHARMA AND SONS/000456,10000.00,,73760.00
`

// SBI's ".xls" is TAB-separated text, and its amounts carry lakh commas that
// are NOT quoted — so a comma reading of this file is garbage, and the header
// is only findable under the tab delimiter.
const sbiExport = "Account Name\t:\tACME INDUSTRIES\n" +
	"Account Number\t:\t00000012345678901\n" +
	"Balance as on 1 Apr 2026\t:\t50,000.00\n" +
	"\t\n" +
	"Txn Date\tValue Date\tDescription\tRef No./Cheque No.\tDebit\tCredit\tBalance\n" +
	"01 Apr 2026\t01 Apr 2026\tBY TRANSFER-UPI/CR/412355556666/MEHTA ST/HDFC/mehta.store@okhdfc/Payment--\tTRANSFER FROM 4897691162095\t\t2,000.00\t52,000.00\n" +
	"04 Apr 2026\t04 Apr 2026\tTO TRANSFER-NEFT*HDFC0000001*SBINN52026040400123*PUNE FABRICATORS--\tTRANSFER TO 4897691162095\t30,000.00\t\t22,000.00\n" +
	"07 Apr 2026\t07 Apr 2026\tBY TRANSFER-IMPS/412377778888/SHARMA TRADERS/ICIC/XXXXXXXX9876--\tTRANSFER FROM 4897691162095\t\t8,500.00\t30,500.00\n"

// Axis, NEWEST FIRST. In file order the balances do not walk; read bottom-up
// they do, and that is the only evidence of the order this package uses.
const axisExport = `Tran Date,CHQNO,PARTICULARS,DR,CR,BAL,SOL
28-04-2026,,UPI/P2A/412399990000/NISHA RAO/Rent April/HDFC BANK,15000.00,,40000.00,1234
20-04-2026,,NEFT/UTIBN52026042000009999/GREEN FARMS PVT LTD/ICIC,,25000.00,55000.00,1234
12-04-2026,000789,CHEQUE DEPOSIT/000789/CLG,,10000.00,30000.00,1234
`

// Kotak: ONE amount column with its own Dr/Cr, a SECOND "Dr / Cr" carrying the
// balance's sign, and an account that starts OVERDRAWN — so the balance flag is
// load-bearing and the account crosses zero inside the statement.
const kotakExport = `Sl. No.,Date,Description,Chq / Ref number,Amount,Dr / Cr,Balance,Dr / Cr
1,02-04-2026,UPI/SURESH MOTORS/412344445555/fuel,,"3,000.00",DR,"1,000.00",DR
2,05-04-2026,IMPS/412366667777/ANAND ENTERPRISES/receipt,,"6,000.00",CR,"5,000.00",CR
3,18-04-2026,NACH/ACH DR/HDFC LIFE INSURANCE/UMRN0012345,,"2,500.00",DR,"2,500.00",CR
`

type formatCase struct {
	name      string
	content   string
	bank      string
	delimiter string
	layout    DateLayout
	operator  Declared
	lines     int
	order     Order
	method    ProofMethod
	opening   string
	closing   string
	// firstLineNo is the row number the operator sees for the first
	// transaction: preamble + header + separator rows included.
	firstLineNo int
}

func dec(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}

func formatCases() []formatCase {
	return []formatCase{
		{name: "HDFC — preamble, asterisk separator, footer summary", content: hdfcExport,
			bank: "HDFC Bank", delimiter: "comma", layout: DMYSlash2, lines: 4, order: OldestFirst,
			method: ProofRunningDeclared, opening: "150000.00", closing: "231000.00", firstLineNo: 8},
		{name: "ICICI — preamble, INR-suffixed headers", content: iciciExport,
			bank: "ICICI Bank", delimiter: "comma", layout: DMYSlash, lines: 3, order: OldestFirst,
			method: ProofRunning, opening: "100000.00", closing: "73760.00", firstLineNo: 5},
		{name: "SBI — tab-separated .xls, dd Mon yyyy, operator-declared balances", content: sbiExport,
			bank: "State Bank of India", delimiter: "tab", layout: DMonYSpace,
			operator: Declared{Opening: dec("50000"), Closing: dec("30500")},
			lines: 3, order: OldestFirst, method: ProofRunningDeclared, opening: "50000.00", closing: "30500.00",
			firstLineNo: 6},
		{name: "Axis — newest first", content: axisExport,
			bank: "Axis Bank", delimiter: "comma", layout: DMYDash, lines: 3, order: NewestFirst,
			method: ProofRunning, opening: "20000.00", closing: "40000.00", firstLineNo: 2},
		{name: "Kotak — repeated Dr / Cr header, overdraft crossing zero", content: kotakExport,
			bank: "Kotak Mahindra Bank", delimiter: "comma", layout: DMYDash, lines: 3, order: OldestFirst,
			method: ProofRunning, opening: "2000.00", closing: "2500.00", firstLineNo: 2},
	}
}

func TestFiveBankLayoutsAreRecognisedProposedParsedAndProved(t *testing.T) {
	for _, tc := range formatCases() {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ProposeMapping(tc.content)
			if err != nil {
				t.Fatalf("propose: %v", err)
			}
			if p.Bank != tc.bank {
				t.Fatalf("recognised as %q, want %q (header %v)", p.Bank, tc.bank, p.Header)
			}
			if p.BankVerified {
				t.Errorf("no profile has been checked against a real export; Verified must be false")
			}
			if p.Delimiter != tc.delimiter {
				t.Errorf("delimiter %q, want %q", p.Delimiter, tc.delimiter)
			}
			if p.Mapping.DateLayout != tc.layout {
				t.Errorf("date layout %q, want %q (unresolved: %v)", p.Mapping.DateLayout, tc.layout, p.Unresolved)
			}
			if len(p.Unresolved) != 0 {
				t.Errorf("a recognised layout with self-settling dates must leave nothing unresolved, got %v", p.Unresolved)
			}
			var said bool
			for _, n := range p.Notes {
				if strings.Contains(n, "NOT been checked against a real") {
					said = true
				}
			}
			if !said {
				t.Errorf("the proposal must SAY the profile is unverified; notes %v", p.Notes)
			}

			st, err := ParseStatement(tc.content, p.Mapping, tc.operator)
			if err != nil {
				t.Fatalf("parse with the PROPOSED mapping: %v", err)
			}
			if len(st.Lines) != tc.lines {
				t.Fatalf("lines %d, want %d", len(st.Lines), tc.lines)
			}
			if st.Order != tc.order {
				t.Errorf("order %q, want %q", st.Order, tc.order)
			}
			if st.Proof.Method != tc.method {
				t.Errorf("proof method %q, want %q", st.Proof.Method, tc.method)
			}
			if got := st.Proof.Opening.StringFixed(2); got != tc.opening {
				t.Errorf("opening %s, want %s", got, tc.opening)
			}
			if got := st.Proof.Closing.StringFixed(2); got != tc.closing {
				t.Errorf("closing %s, want %s", got, tc.closing)
			}
			if st.Lines[0].LineNo != tc.firstLineNo {
				t.Errorf("first transaction reported as row %d, want %d — refusals quote this number",
					st.Lines[0].LineNo, tc.firstLineNo)
			}
		})
	}
}

// The HDFC footer is read by LABEL, and every figure in it is checked, not
// merely read.
func TestTheHDFCFooterSummaryIsReadAndEveryFigureChecked(t *testing.T) {
	p, err := ProposeMapping(hdfcExport)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	st, err := ParseStatement(hdfcExport, p.Mapping, Declared{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := st.Summary
	if s.Opening == nil || s.Opening.StringFixed(2) != "150000.00" {
		t.Errorf("declared opening not read: %v", s.Opening)
	}
	if s.Closing == nil || s.Closing.StringFixed(2) != "231000.00" {
		t.Errorf("declared closing not read: %v", s.Closing)
	}
	if s.DebitCount == nil || *s.DebitCount != 2 || s.CreditCount == nil || *s.CreditCount != 2 {
		t.Errorf("counts not read: %v / %v", s.DebitCount, s.CreditCount)
	}
	if st.Proof.DeclaredBy != "file" {
		t.Errorf("declared figures came from the file; DeclaredBy = %q", st.Proof.DeclaredBy)
	}

	// Each footer figure, falsified in turn, must refuse the file. A footer
	// that is read but not compared is a comment.
	for _, tc := range []struct{ from, to, why string }{
		{`"1,50,000.00",2,2`, `"1,40,000.00",2,2`, "opening"},
		{`"2,31,000.00",`, `"2,31,000.01",`, "closing"},
		{`,2,2,"62,000.00"`, `,3,2,"62,000.00"`, "debit count"},
		{`"62,000.00","1,43,000.00"`, `"61,000.00","1,43,000.00"`, "debit total"},
	} {
		bad := strings.Replace(hdfcExport, tc.from, tc.to, 1)
		if bad == hdfcExport {
			t.Fatalf("%s: the falsification did not apply", tc.why)
		}
		if _, err := ParseStatement(bad, p.Mapping, Declared{}); !errors.Is(err, ErrBalanceProof) {
			t.Errorf("a falsified %s must refuse the file with ErrBalanceProof, got %v", tc.why, err)
		}
	}
}

// ⛔ THE EDGE ROW. The running balances walk perfectly with the FIRST
// transaction removed — nothing is left to disagree with it — and only the
// declared opening balance can see that it is gone.
func TestAMissingFirstRowWalksButFailsAgainstTheDeclaredOpening(t *testing.T) {
	p, err := ProposeMapping(hdfcExport)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	firstRow := "01/04/26,UPI-ACME TRADERS-acme.traders@okhdfcbank-HDFC0001234-412345678901-INV 42,0000412345678901,01/04/26,,\"25,000.00\",\"1,75,000.00\"\n"
	cut := strings.Replace(hdfcExport, firstRow, "", 1)
	if cut == hdfcExport {
		t.Fatal("fixture drift: the first row was not removed")
	}
	// Control: the walk alone is satisfied.
	if st, err := Parse(cut, p.Mapping); err != nil || !st.BalanceWalked {
		t.Fatalf("control: without the first row the running balances still walk (err %v)", err)
	}
	_, err = ParseStatement(cut, p.Mapping, Declared{})
	if !errors.Is(err, ErrBalanceProof) {
		t.Fatalf("a missing first transaction must be refused by the declared-opening check, got %v", err)
	}
}

// A file with no running balance and nothing declared has no proof, and is
// REFUSED — slice 2a imported it with a disclosure.
func TestAStatementWithNoBalancesAndNothingDeclaredIsRefused(t *testing.T) {
	m := twoColMapping()
	m.RunningBalance = ""
	_, err := ParseStatement(twoColCSV, m, Declared{})
	if !errors.Is(err, ErrNoBalanceProof) {
		t.Fatalf("want ErrNoBalanceProof, got %v", err)
	}
	if !strings.Contains(err.Error(), "opening AND a closing") {
		t.Errorf("the refusal must say what to supply; got %v", err)
	}
	// Half a declaration is still no proof, and the message says which half.
	_, err = ParseStatement(twoColCSV, m, Declared{Opening: dec("150000")})
	if !errors.Is(err, ErrNoBalanceProof) || !strings.Contains(err.Error(), "only the opening") {
		t.Fatalf("an opening without a closing proves nothing; got %v", err)
	}
}

// With the balances DECLARED, a statement with no running balance imports —
// and a single paisa out refuses it.
func TestDeclaredBalancesProveAStatementWithNoRunningBalance(t *testing.T) {
	m := twoColMapping()
	m.RunningBalance = ""
	// 1,50,000 + 1,00,000 − 25,000 − 12,500.50 = 2,12,499.50
	st, err := ParseStatement(twoColCSV, m, Declared{Opening: dec("150000"), Closing: dec("212499.50")})
	if err != nil {
		t.Fatalf("a correctly declared statement must prove: %v", err)
	}
	if st.Proof.Method != ProofDeclared || st.Proof.DeclaredBy != "operator" {
		t.Errorf("method %q by %q, want declared by operator", st.Proof.Method, st.Proof.DeclaredBy)
	}
	if st.BalanceWalked {
		t.Error("the walk did not run and must not be reported as having run")
	}
	_, err = ParseStatement(twoColCSV, m, Declared{Opening: dec("150000"), Closing: dec("212499.51")})
	if !errors.Is(err, ErrBalanceProof) {
		t.Fatalf("one paisa out must refuse, got %v", err)
	}
	if !strings.Contains(err.Error(), "-0.01") && !strings.Contains(err.Error(), "0.01") {
		t.Errorf("the refusal must state the difference; got %v", err)
	}
}

// The file says one thing and the operator keyed another: refused, because one
// of them is not this statement's.
func TestAnOperatorFigureThatDisagreesWithTheFileIsRefused(t *testing.T) {
	p, err := ProposeMapping(hdfcExport)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	_, err = ParseStatement(hdfcExport, p.Mapping, Declared{Opening: dec("150000"), Closing: dec("231500")})
	if !errors.Is(err, ErrDeclaredConflict) {
		t.Fatalf("want ErrDeclaredConflict, got %v", err)
	}
	// Agreement is recorded as both.
	st, err := ParseStatement(hdfcExport, p.Mapping, Declared{Opening: dec("150000"), Closing: dec("231000")})
	if err != nil {
		t.Fatalf("agreeing figures must prove: %v", err)
	}
	if st.Proof.DeclaredBy != "file+operator" {
		t.Errorf("DeclaredBy %q, want file+operator", st.Proof.DeclaredBy)
	}
}

// ⛔ A newest-first file is walked bottom-up — and a genuinely broken file is
// still refused with the FORWARD walk's message, as slice 2a reported it.
func TestNewestFirstIsDiscoveredFromTheBalancesAndABrokenFileStillRefuses(t *testing.T) {
	p, err := ProposeMapping(axisExport)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	broken := strings.Replace(axisExport, ",,25000.00,55000.00,", ",,25000.00,55000.01,", 1)
	_, err = ParseStatement(broken, p.Mapping, Declared{})
	if !errors.Is(err, ErrBalanceWalk) {
		t.Fatalf("a newest-first file that walks in NEITHER direction must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "row 3") {
		t.Errorf("the refusal is the forward walk's, naming its row; got %v", err)
	}
}

// ⛔ THE BALANCE FLAG IS A SIGN. Read as unsigned, Kotak's overdrawn opening
// makes a correct file fail its walk.
func TestTheKotakBalanceFlagIsReadAsASign(t *testing.T) {
	p, err := ProposeMapping(kotakExport)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if p.Mapping.Direction != "Dr / Cr" || p.Mapping.BalanceSign != "Dr / Cr (2)" {
		t.Fatalf("the two Dr / Cr columns must be told apart: direction %q, balance sign %q",
			p.Mapping.Direction, p.Mapping.BalanceSign)
	}
	st, err := ParseStatement(kotakExport, p.Mapping, Declared{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := st.Lines[0].RunningBalance.StringFixed(2); got != "-1000.00" {
		t.Errorf("an overdrawn balance must be negative; got %s", got)
	}
	m := p.Mapping
	m.BalanceSign = ""
	if _, err := ParseStatement(kotakExport, m, Declared{}); !errors.Is(err, ErrBalanceWalk) {
		t.Errorf("control: without the balance flag the overdraft must break the walk, got %v", err)
	}
}

// A balance cell carrying its own Dr suffix is negative too.
func TestABalanceCellWithADrSuffixIsNegative(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"5,000.00 Dr", "-5000.0000"},
		{"5,000.00Dr.", "-5000.0000"},
		{"5,000.00 Cr", "5000.0000"},
		{"5,000.00", "5000.0000"},
	} {
		got, err := parseBalance(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got.StringFixed(4) != tc.want {
			t.Errorf("%q: want %s, got %s", tc.in, tc.want, got.StringFixed(4))
		}
	}
}

// ⛔ A LAST ROW WITH A MALFORMED DATE IS REFUSED, NOT SWEPT INTO THE FOOTER.
// Swept, it is a dropped last transaction — the one row the walk cannot see.
func TestAMalformedLastRowIsRefusedRatherThanTreatedAsFooter(t *testing.T) {
	bad := twoColCSV + "31/13/2026,LAST ROW,\"1.00\",,\"2,12,498.50\"\n"
	_, err := Parse(bad, twoColMapping())
	if !errors.Is(err, ErrBadDate) {
		t.Fatalf("want ErrBadDate for the last row, got %v", err)
	}
}

// A binary spreadsheet, a PDF and an HTML page are refused BY NAME.
func TestNonTextFormatsAreRefusedByName(t *testing.T) {
	for _, tc := range []struct{ content, word string }{
		{"\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1rest", ".xls"},
		{"PK\x03\x04rest", ".xlsx"},
		{"%PDF-1.7 rest", "PDF"},
		{"<html><table><tr><td>Date</td></tr></table></html>", "HTML"},
		{":20:STMT\n:25:123\n:60F:C260401INR100,00\n", "MT940"},
	} {
		_, err := Parse(tc.content, twoColMapping())
		if !errors.Is(err, ErrUnsupportedFormat) || !strings.Contains(err.Error(), tc.word) {
			t.Errorf("%s: want ErrUnsupportedFormat naming it, got %v", tc.word, err)
		}
	}
}

// A byte-order mark in front of the header must not hide the first column.
func TestAByteOrderMarkDoesNotHideTheFirstHeader(t *testing.T) {
	st, err := Parse("\ufeff"+twoColCSV, twoColMapping())
	if err != nil {
		t.Fatalf("a BOM-prefixed file must parse: %v", err)
	}
	if len(st.Lines) != 3 {
		t.Errorf("want 3 lines, got %d", len(st.Lines))
	}
}

// Two profiles matching one header would be a signature too loose to trust.
func TestNoTwoProfilesMatchTheSameHeader(t *testing.T) {
	for _, tc := range formatCases() {
		p, err := ProposeMapping(tc.content)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		n := 0
		have := map[string]bool{}
		for _, h := range p.Header {
			have[normaliseHeader(h)] = true
		}
		for _, prof := range bankProfiles {
			all := true
			for _, s := range prof.Signature {
				if !have[s] {
					all = false
				}
			}
			if all {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: %d profiles match its header, want exactly 1", tc.name, n)
		}
	}
}

// Two different "Opening Balance" figures in one file refuse it.
func TestTwoDifferentDeclaredOpeningsRefuse(t *testing.T) {
	bad := "Opening Balance : 100.00\n" + strings.Replace(hdfcExport, "HDFC BANK Ltd.", "HDFC BANK Ltd.", 1)
	p, err := ProposeMapping(hdfcExport)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := ParseStatement(bad, p.Mapping, Declared{}); !errors.Is(err, ErrDeclaredConflict) {
		t.Fatalf("want ErrDeclaredConflict, got %v", err)
	}
}
