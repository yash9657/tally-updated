package bankstatement

// Tests for the CSV parser. Design memo: docs/design/t4-slice2-model.md §8 and
// the invariant table in §4.
//
// EVERY REFUSAL TEST ASSERTS THE REASON, NOT JUST THAT AN ERROR HAPPENED.
// HANDOFF-SESSION-ITEM-ZERO.md §4.2 records two HTTP tests that were green
// while sending a malformed body, because every domain refusal on that route
// answered the same status code. The equivalent here would be errors.Is against
// nothing, or a bare `if err == nil { t.Fatal }` — both of which pass when the
// parser refuses for entirely the wrong reason.

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// twoColMapping is the layout most Indian retail banks export.
func twoColMapping() ColumnMapping {
	return ColumnMapping{
		Encoding:       TwoColumns,
		DateLayout:     DMYSlash,
		ValueDate:      "Value Date",
		Description:    "Narration",
		Deposit:        "Deposit Amt.",
		Withdrawal:     "Withdrawal Amt.",
		RunningBalance: "Closing Balance",
	}
}

const twoColCSV = `Value Date,Narration,Withdrawal Amt.,Deposit Amt.,Closing Balance
01/04/2026,OPENING TRANSFER,,"1,00,000.00","2,50,000.00"
05/04/2026,NEFT ACME TRADERS,"25,000.00",,"2,25,000.00"
09/04/2026,CHQ 004312 SUPPLIER,"12,500.50",,"2,12,499.50"
`

func TestATwoColumnFileParsesWithTheRightDirections(t *testing.T) {
	st, err := Parse(twoColCSV, twoColMapping())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(st.Lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(st.Lines))
	}

	// ⚠️ THE INVERSION. A DEPOSIT is a DEBIT in our books: the bank asset rose,
	// even though the bank's own statement calls it a credit. If this ever
	// flips, every deposit compares unequal to the receipt voucher that created
	// it and the whole statement matches nothing — which reads on screen as
	// "nothing to reconcile", not as a bug.
	if st.Lines[0].DrCr != "D" {
		t.Errorf("a DEPOSIT must be 'D' in our books (the bank asset rose); got %q", st.Lines[0].DrCr)
	}
	if st.Lines[1].DrCr != "C" {
		t.Errorf("a WITHDRAWAL must be 'C' in our books; got %q", st.Lines[1].DrCr)
	}

	// Lakh grouping, and paise preserved exactly.
	if got := st.Lines[0].Amount.String(); got != "100000" {
		t.Errorf("1,00,000.00 must read as 100000, got %s", got)
	}
	if got := st.Lines[2].Amount.StringFixed(2); got != "12500.50" {
		t.Errorf("paise must survive: want 12500.50, got %s", got)
	}

	// The opening balance is DERIVED from the first line, never taken on trust:
	// 2,50,000 closing less the 1,00,000 that moved in = 1,50,000.
	if got := st.OpeningBalance.StringFixed(2); got != "150000.00" {
		t.Errorf("opening balance want 150000.00, got %s", got)
	}
	if got := st.ClosingBalance.StringFixed(2); got != "212499.50" {
		t.Errorf("closing balance want 212499.50, got %s", got)
	}
	if !st.BalanceWalked {
		t.Error("a file with a running balance on every row must report the walk as having RUN")
	}
	if st.PeriodFrom.Format("2006-01-02") != "2026-04-01" || st.PeriodTo.Format("2006-01-02") != "2026-04-09" {
		t.Errorf("period want 2026-04-01..2026-04-09, got %s..%s",
			st.PeriodFrom.Format("2006-01-02"), st.PeriodTo.Format("2006-01-02"))
	}
}

// B-6: all three direction encodings must produce IDENTICAL stored rows. This
// is a differential test, so it carries its own control: the three inputs are
// the same two transactions written three ways.
func TestAllThreeDirectionEncodingsNormaliseIdentically(t *testing.T) {
	cases := []struct {
		name    string
		csv     string
		mapping ColumnMapping
	}{
		{
			name: "two columns",
			csv: "Date,Narration,Withdrawal,Deposit\n" +
				"01/04/2026,IN,,5000.00\n" +
				"02/04/2026,OUT,1200.00,\n",
			mapping: ColumnMapping{
				Encoding: TwoColumns, DateLayout: DMYSlash,
				ValueDate: "Date", Description: "Narration",
				Deposit: "Deposit", Withdrawal: "Withdrawal",
			},
		},
		{
			name: "signed amount",
			csv: "Date,Narration,Amount\n" +
				"01/04/2026,IN,5000.00\n" +
				"02/04/2026,OUT,-1200.00\n",
			mapping: ColumnMapping{
				Encoding: SignedAmount, DateLayout: DMYSlash,
				ValueDate: "Date", Description: "Narration", Amount: "Amount",
			},
		},
		{
			name: "amount plus C/D flag",
			csv: "Date,Narration,Amount,Dr/Cr\n" +
				"01/04/2026,IN,5000.00,C\n" +
				"02/04/2026,OUT,1200.00,D\n",
			mapping: ColumnMapping{
				Encoding: AmountAndFlag, DateLayout: DMYSlash,
				ValueDate: "Date", Description: "Narration",
				Amount: "Amount", Direction: "Dr/Cr",
			},
		},
	}

	type row struct {
		date, drCr, amount string
	}
	var first []row
	for _, tc := range cases {
		st, err := Parse(tc.csv, tc.mapping)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		got := make([]row, 0, len(st.Lines))
		for _, l := range st.Lines {
			got = append(got, row{l.ValueDate.Format("2006-01-02"), l.DrCr, l.Amount.StringFixed(4)})
		}
		// The control: the money in is a DEBIT and the money out a CREDIT, in
		// every encoding. Asserting only that the three AGREE would pass if all
		// three were inverted together.
		if got[0] != (row{"2026-04-01", "D", "5000.0000"}) {
			t.Errorf("%s: money IN must be D/5000, got %+v", tc.name, got[0])
		}
		if got[1] != (row{"2026-04-02", "C", "1200.0000"}) {
			t.Errorf("%s: money OUT must be C/1200, got %+v", tc.name, got[1])
		}
		if first == nil {
			first = got
			continue
		}
		for i := range got {
			if got[i] != first[i] {
				t.Errorf("%s row %d: %+v differs from the two-column reading %+v", tc.name, i, got[i], first[i])
			}
		}
	}
}

// B-4: a dropped row must be REFUSED, naming where the walk breaks — not
// imported with a gap. This is the only check that catches a mis-exported file.
func TestADroppedRowBreaksTheBalanceWalkAndIsRefused(t *testing.T) {
	// The 05/04 withdrawal of 25,000 is removed; the balances either side no
	// longer walk.
	broken := `Value Date,Narration,Withdrawal Amt.,Deposit Amt.,Closing Balance
01/04/2026,OPENING TRANSFER,,"1,00,000.00","2,50,000.00"
09/04/2026,CHQ 004312 SUPPLIER,"12,500.50",,"2,12,499.50"
`
	_, err := Parse(broken, twoColMapping())
	if !errors.Is(err, ErrBalanceWalk) {
		t.Fatalf("a file whose balances do not walk must be refused with ErrBalanceWalk, got %v", err)
	}
	// The MESSAGE is the deliverable. A refusal that does not name the row
	// leaves the operator with a spreadsheet and no idea where to look.
	for _, want := range []string{"row 3", "237499.50", "212499.50"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must quote %q so the operator can find it; got: %v", want, err)
		}
	}
	if !strings.Contains(err.Error(), "NOT imported") {
		t.Errorf("the refusal must say the statement was not imported; got: %v", err)
	}
}

// B-7: the tolerance is ZERO. One paisa out is out.
func TestOnePaisaOutBreaksTheWalk(t *testing.T) {
	offByOnePaisa := `Value Date,Narration,Withdrawal Amt.,Deposit Amt.,Closing Balance
01/04/2026,OPENING,,"1,00,000.00","2,50,000.00"
05/04/2026,NEFT,"25,000.00",,"2,25,000.01"
`
	_, err := Parse(offByOnePaisa, twoColMapping())
	if !errors.Is(err, ErrBalanceWalk) {
		t.Fatalf("ONE PAISA is a break. A tolerance is how a real error hides for a year "+
			"(reporting/bankrecon.go). Got %v", err)
	}
	if !strings.Contains(err.Error(), "0.01") {
		t.Errorf("the refusal must state the difference as 0.01; got %v", err)
	}
}

// A file with no running-balance column cannot be walked, and must SAY so —
// never report the same shape as a walk that ran and passed.
func TestAFileWithNoBalanceColumnReportsThatTheWalkDidNotRun(t *testing.T) {
	m := twoColMapping()
	m.RunningBalance = ""
	st, err := Parse(twoColCSV, m)
	if err != nil {
		t.Fatalf("a file without a balance column is importable: %v", err)
	}
	if st.BalanceWalked {
		t.Error("BalanceWalked must be FALSE when there is no balance to walk — " +
			"a check that did not run must never look like a check that passed")
	}
	if len(st.Lines) != 3 {
		t.Errorf("the lines still import; want 3, got %d", len(st.Lines))
	}
}

// An empty file is a refusal, not an import of nothing: "imported 0 lines"
// reads as "your bank had no transactions that month".
func TestAnEmptyFileIsRefusedByName(t *testing.T) {
	_, err := Parse("Value Date,Narration,Withdrawal Amt.,Deposit Amt.,Closing Balance\n", twoColMapping())
	if !errors.Is(err, ErrEmptyFile) {
		t.Fatalf("a header-only file must be refused with ErrEmptyFile, got %v", err)
	}
}

// A blank row in the MIDDLE is refused: it is what a mis-saved export looks
// like, and skipping it would silently remove a row the walk depends on.
func TestABlankRowInTheMiddleIsRefused(t *testing.T) {
	withGap := `Value Date,Narration,Withdrawal Amt.,Deposit Amt.,Closing Balance
01/04/2026,OPENING,,"1,00,000.00","2,50,000.00"
,,,,
05/04/2026,NEFT,"25,000.00",,"2,25,000.00"
`
	_, err := Parse(withGap, twoColMapping())
	if !errors.Is(err, ErrEmptyFile) {
		t.Fatalf("a blank row mid-statement must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "row 3") {
		t.Errorf("the refusal must name the row; got %v", err)
	}
}

// A mapping naming a column the file does not have is refused LOUDLY. Under a
// positional fallback it would read a neighbouring column and produce money
// that was never a transaction.
func TestAMappingForADifferentExportIsRefusedAndNamesTheColumns(t *testing.T) {
	m := twoColMapping()
	m.Withdrawal = "Debit Amount" // this file calls it "Withdrawal Amt."
	_, err := Parse(twoColCSV, m)
	if !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("a mapping naming an absent column must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "Debit Amount") || !strings.Contains(err.Error(), "Withdrawal Amt.") {
		t.Errorf("the refusal must name BOTH what was asked for and what the file has; got %v", err)
	}
}

// A required field with no column mapped is refused per-encoding: a two-column
// mapping missing its withdrawal column would otherwise import every payment as
// having no amount.
func TestEachEncodingRequiresAllOfItsOwnColumns(t *testing.T) {
	m := twoColMapping()
	m.Withdrawal = ""
	_, err := Parse(twoColCSV, m)
	if !errors.Is(err, ErrUnmappedColumn) {
		t.Fatalf("a two-column mapping without a withdrawal column must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "withdrawal") {
		t.Errorf("the refusal must name the missing field; got %v", err)
	}
}

// Both columns filled on one row is not summable and is not netted.
func TestARowWithBothADepositAndAWithdrawalIsRefused(t *testing.T) {
	both := `Value Date,Narration,Withdrawal Amt.,Deposit Amt.,Closing Balance
01/04/2026,ODD,"500.00","1,00,000.00","2,50,000.00"
`
	_, err := Parse(both, twoColMapping())
	if !errors.Is(err, ErrBothColumnsFilled) {
		t.Fatalf("want ErrBothColumnsFilled, got %v", err)
	}
	if !strings.Contains(err.Error(), "the wrong way round") {
		t.Errorf("the refusal should suggest the likely cause; got %v", err)
	}
}

// A date the declared layout cannot read is refused against THE LAYOUT, so the
// operator learns the mapping is wrong rather than that "the file is bad".
func TestADateThatDoesNotMatchTheDeclaredLayoutIsRefused(t *testing.T) {
	m := twoColMapping()
	m.DateLayout = YMDDash
	_, err := Parse(twoColCSV, m)
	if !errors.Is(err, ErrBadDate) {
		t.Fatalf("want ErrBadDate, got %v", err)
	}
	if !strings.Contains(err.Error(), "yyyy-mm-dd") || !strings.Contains(err.Error(), "01/04/2026") {
		t.Errorf("the refusal must show the value AND the declared layout; got %v", err)
	}
}

// Two transactions identical in every modelled field must BOTH survive, on
// distinct occurrence ordinals. Without that the second collides with the first
// on re-import and is silently dropped — an under-import, which shows nothing
// wrong on screen.
func TestTwoIdenticalTransactionsGetDistinctOccurrences(t *testing.T) {
	twice := `Value Date,Narration,Withdrawal Amt.,Deposit Amt.,Closing Balance
05/04/2026,ATM CASH,"5,000.00",,"2,45,000.00"
05/04/2026,ATM CASH,"5,000.00",,"2,40,000.00"
`
	st, err := Parse(twice, twoColMapping())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if st.Lines[0].Hash != st.Lines[1].Hash {
		t.Fatal("two identical transactions must share a hash — that is what makes them identical")
	}
	if st.Lines[0].Occurrence != 0 || st.Lines[1].Occurrence != 1 {
		t.Errorf("identical lines must be numbered 0 and 1, got %d and %d",
			st.Lines[0].Occurrence, st.Lines[1].Occurrence)
	}
}

// The hash must NOT include the running balance: the same rows re-exported with
// the balance column switched off must still deduplicate against the first
// import, or an overlapping download silently doubles the statement.
func TestTheHashIgnoresTheRunningBalance(t *testing.T) {
	withBalance, err := Parse(twoColCSV, twoColMapping())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	noBalanceMapping := twoColMapping()
	noBalanceMapping.RunningBalance = ""
	without, err := Parse(twoColCSV, noBalanceMapping)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for i := range withBalance.Lines {
		if withBalance.Lines[i].Hash != without.Lines[i].Hash {
			t.Errorf("line %d: the hash changed when the balance column was dropped; "+
				"an overlapping re-export would then double the statement", i)
		}
	}
}

// The hash separates its fields, so a description ending in a digit beside an
// amount cannot collide with a different split of the same characters.
func TestTheHashDoesNotCollideOnAdjacentFields(t *testing.T) {
	a := Line{Description: "PAYMENT 1", Amount: decimal.RequireFromString("200"), DrCr: "C"}
	b := Line{Description: "PAYMENT", Amount: decimal.RequireFromString("1200"), DrCr: "C"}
	if hashLine(a) == hashLine(b) {
		t.Error("'PAYMENT 1' + 200 must not hash the same as 'PAYMENT' + 1200")
	}
}

// Several banks export newest-first. A period read off the first and last rows
// would then run backwards, and migration 0062's CHECK would refuse the import
// for a reason that has nothing to do with what is wrong.
func TestThePeriodIsNotAssumedSorted(t *testing.T) {
	newestFirst := `Value Date,Narration,Withdrawal Amt.,Deposit Amt.
09/04/2026,LATEST,"100.00",
01/04/2026,EARLIEST,,"100.00"
`
	m := twoColMapping()
	m.RunningBalance = ""
	st, err := Parse(newestFirst, m)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if st.PeriodFrom.Format("2006-01-02") != "2026-04-01" {
		t.Errorf("period_from must be the EARLIEST date, got %s", st.PeriodFrom.Format("2006-01-02"))
	}
	if st.PeriodTo.Format("2006-01-02") != "2026-04-09" {
		t.Errorf("period_to must be the LATEST date, got %s", st.PeriodTo.Format("2006-01-02"))
	}
}

// The value date and the transaction date are STORED, NEVER CONFLATED: they are
// the only evidence that a cheque was presented one day and credited another.
func TestTheTwoDatesAreKeptApart(t *testing.T) {
	twoDates := "Txn Date,Value Date,Narration,Amount\n" +
		"05/04/2026,07/04/2026,CHEQUE 4312,-1200.00\n"
	st, err := Parse(twoDates, ColumnMapping{
		Encoding: SignedAmount, DateLayout: DMYSlash,
		ValueDate: "Value Date", TxnDate: "Txn Date",
		Description: "Narration", Amount: "Amount",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l := st.Lines[0]
	if l.ValueDate.Format("2006-01-02") != "2026-04-07" {
		t.Errorf("value date want 2026-04-07, got %s", l.ValueDate.Format("2006-01-02"))
	}
	if l.TxnDate.Format("2006-01-02") != "2026-04-05" {
		t.Errorf("transaction date want 2026-04-05, got %s", l.TxnDate.Format("2006-01-02"))
	}
}

// Everything the file carried is preserved, including columns we do not model.
func TestUnmodelledColumnsArePreservedVerbatim(t *testing.T) {
	extra := "Value Date,Narration,Amount,Branch Code\n" +
		"01/04/2026,IN,5000.00,MUM0421\n"
	st, err := Parse(extra, ColumnMapping{
		Encoding: SignedAmount, DateLayout: DMYSlash,
		ValueDate: "Value Date", Description: "Narration", Amount: "Amount",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := st.Lines[0].Raw["Branch Code"]; got != "MUM0421" {
		t.Errorf("an unmodelled column must survive verbatim so it is recoverable later; got %q", got)
	}
}

// Accounting negatives and Cr/Dr suffixes inside the amount cell.
func TestAmountCellVariants(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1,25,000.00", "125000.0000"},
		{"(1,200.00)", "-1200.0000"},
		{"1200.00Cr", "1200.0000"},
		{" 1200.00 ", "1200.0000"},
	} {
		got, err := parseAmount(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got.StringFixed(4) != tc.want {
			t.Errorf("%q: want %s, got %s", tc.in, tc.want, got.StringFixed(4))
		}
	}
	if _, err := parseAmount("N/A"); !errors.Is(err, ErrBadAmount) {
		t.Errorf("a non-numeric amount must be refused, got %v", err)
	}
}
