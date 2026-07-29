package payrollfile

import (
	"bytes"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// sampleStatement is a two-challan Q1 statement with three deductees, so the
// counts in the batch header are not all 1 and a swapped count is visible.
func sampleStatement(t *testing.T) Form138File {
	t.Helper()
	ded := func(serial int, pan, name, tax string) Form138Deductee {
		amt := dec(t, tax)
		return Form138Deductee{
			EmployeeSerialNumber: serial,
			EmployeeReference:    "",
			PAN:                  pan,
			Name:                 name,
			DateOfPayment:        day(2026, time.April, 30),
			DateOfDeduction:      day(2026, time.April, 30),
			AmountPaid:           dec(t, "84317.50"),
			Tax:                  amt,
			Surcharge:            decimal.Zero,
			Cess:                 decimal.Zero,
			TotalTaxDeducted:     amt,
			TotalTaxDeposited:    amt,
		}
	}
	return Form138File{
		Header: Form138Header{
			TAN:                "BLRK12345F",
			DeductorPAN:        "AAACD1234E",
			DeductorName:       "DEMO TRADERS PRIVATE LIMITED",
			FinancialYear:      "202627",
			Quarter:            "Q1",
			FormNumber:         "138",
			FileCreationDate:   day(2026, time.July, 5),
			FileSequenceNumber: 1,
			ProducerVersion:    "khata-payroll-1.0",
		},
		Challans: []Form138Challan{
			{
				BSRCode:             "0221826", // real BSR codes begin with 0
				DateOfDeposit:       day(2026, time.May, 7),
				ChallanSerialNumber: 4321,
				TotalTDSDeposited:   dec(t, "17000"),
				TotalInterest:       decimal.Zero,
				TotalFee:            decimal.Zero,
				Deductees: []Form138Deductee{
					ded(1, "ABCPK1234F", "RAVI KUMAR", "9000"),
					ded(2, "PANNOTAVBL", "SUNITA DEVI", "8000"),
				},
			},
			{
				BSRCode:             "0510308",
				DateOfDeposit:       day(2026, time.June, 6),
				ChallanSerialNumber: 88,
				TotalTDSDeposited:   dec(t, "2500.50"),
				TotalInterest:       dec(t, "12.00"),
				TotalFee:            decimal.Zero,
				Deductees: []Form138Deductee{
					ded(3, "XYZPM9876Q", "ARUN NAIR", "2500.50"),
				},
			},
		},
	}
}

// TestForm138_RecordOrderAndBatchCounts pins the file's skeleton: exactly one
// FH then exactly one BH, then every CD immediately followed by ITS OWN
// deductees, and the counts in the BH equal to the records actually written.
//
// The counts matter more than they look. The FVU reads the BH's counts and then
// reads that many records; a count that is one low means the last deductee's
// tax is simply not filed — the employee's Form 130 is short by that amount and
// they get the demand, not us. Because the writer DERIVES the counts from the
// same slices it renders, the caller cannot cause that; this test is what pins
// the derivation.
func TestForm138_RecordOrderAndBatchCounts(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteForm138AnnexureI(&buf, sampleStatement(t)); err != nil {
		t.Fatalf("WriteForm138AnnexureI: %v", err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatal("file does not end with a newline; the last record would be truncated")
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2+2+3 {
		t.Fatalf("got %d records, want 7 (FH + BH + 2 CD + 3 DD)\n%s", len(lines), out)
	}

	types := make([]string, len(lines))
	recs := make([][]string, len(lines))
	for i, ln := range lines {
		f := strings.Split(ln, form138Delimiter)
		if len(f) < 2 {
			t.Fatalf("record %d has no record-type field: %q", i+1, ln)
		}
		recs[i] = f
		types[i] = f[1]
		// Every record carries its own sequential line number in field 1.
		if got, want := f[0], strconv.Itoa(i+1); got != want {
			t.Errorf("record %d: line number field = %q, want %q", i+1, got, want)
		}
	}
	wantOrder := []string{"FH", "BH", "CD", "DD", "DD", "CD", "DD"}
	for i := range wantOrder {
		if types[i] != wantOrder[i] {
			t.Fatalf("record order is %v, want %v — the FVU reads record type positionally and a reordered file is unreadable", types, wantOrder)
		}
	}

	// BH counts must equal what actually follows.
	countCD, countDD := 0, 0
	for _, ty := range types {
		switch ty {
		case "CD":
			countCD++
		case "DD":
			countDD++
		}
	}
	bh := recs[1]
	const (
		bhChallanCountIdx  = 3
		bhSalaryCountIdx   = 6
		bhDeducteeCountIdx = 7
	)
	if got, want := bh[bhChallanCountIdx], strconv.Itoa(countCD); got != want {
		t.Errorf("BH challan count = %q but %s CD records were written; the FVU would stop reading early and the missing challan is a deposit we claimed but never filed", got, want)
	}
	if got, want := bh[bhDeducteeCountIdx], strconv.Itoa(countDD); got != want {
		t.Errorf("BH deductee count = %q but %s DD records were written; a low count files less tax than was deducted and the employee's Form 130 is short", got, want)
	}
	if got := bh[bhSalaryCountIdx]; got != "0" {
		t.Errorf("BH salary-detail (SD) count = %q, want %q: Annexure II is a Q4 filing and this package does not build Q4, so the zero must be explicit", got, "0")
	}

	// Each CD's own deductee count must equal the DD records that follow it.
	const cdDeducteeCountIdx = 4
	for i, ty := range types {
		if ty != "CD" {
			continue
		}
		n := 0
		for j := i + 1; j < len(types) && types[j] == "DD"; j++ {
			n++
		}
		if got, want := recs[i][cdDeducteeCountIdx], strconv.Itoa(n); got != want {
			t.Errorf("CD at record %d claims %s deductees but %s follow it", i+1, got, want)
		}
	}
}

// TestForm138_CaretDelimitedNoLeadingZerosNoTrailingSpaces pins the lexical
// rules of the format.
//
// The rule is NOT "no field starts with a zero": a date is 01072026 and a BSR
// code is 0221826, and stripping those zeros corrupts them — an unmatchable BSR
// code means the challan is never traced and every employee under it loses the
// credit. The rule applies to NUMERIC and AMOUNT fields only, which is why the
// writer classifies each field by kind. This test walks the typed records and
// the rendered bytes together, so it fails both if a numeric field gains a
// padded zero and if the renderer stops agreeing with the record builder.
func TestForm138_CaretDelimitedNoLeadingZerosNoTrailingSpaces(t *testing.T) {
	f := sampleStatement(t)
	recs, err := buildForm138Records(f)
	if err != nil {
		t.Fatalf("buildForm138Records: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteForm138AnnexureI(&buf, f); err != nil {
		t.Fatalf("WriteForm138AnnexureI: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != len(recs) {
		t.Fatalf("renderer wrote %d lines for %d records", len(lines), len(recs))
	}

	leadingZero := regexp.MustCompile(`^-?0[0-9]`)
	for i, rec := range recs {
		got := strings.Split(lines[i], form138Delimiter)
		if len(got) != len(rec) {
			t.Fatalf("record %d: rendered %d fields, built %d", i+1, len(got), len(rec))
		}
		for j, fl := range rec {
			if got[j] != fl.val {
				t.Fatalf("record %d field %d (%s): rendered %q, built %q", i+1, j+1, fl.name, got[j], fl.val)
			}
			v := got[j]
			if v != strings.TrimRight(v, " \t") {
				t.Errorf("record %d field %d (%s) = %q has trailing whitespace; the format is variable width and the padding is filed as part of the value", i+1, j+1, fl.name, v)
			}
			if strings.Contains(v, form138Delimiter) {
				t.Errorf("record %d field %d (%s) contains the caret delimiter", i+1, j+1, fl.name)
			}
			switch fl.kind {
			case kindNum, kindAmount:
				if leadingZero.MatchString(v) {
					t.Errorf("record %d field %d (%s) = %q has a leading zero on a numeric field", i+1, j+1, fl.name, v)
				}
			case kindDate:
				if len(v) != 8 || !isDigits(v, 8) {
					t.Errorf("record %d field %d (%s) = %q is not DDMMYYYY", i+1, j+1, fl.name, v)
				}
			}
		}
	}

	// The BSR code really is present with its leading zero — otherwise the
	// kind-based rule above would be vacuously satisfied by a corpus with no
	// zero-leading identifier in it.
	if !strings.Contains(buf.String(), "^0221826^") {
		t.Fatalf("the leading-zero BSR code was not emitted verbatim; this fixture no longer demonstrates that character identifiers keep their zeros:\n%s", buf.String())
	}
}

// TestForm138_Q4IsRefusedNotGuessed is the refusal this package exists to make.
//
// Q4 carries Annexure II — every employee's whole-year taxable salary and the
// tax computed on it — and the Q4 record structure is NOT PUBLISHED (Protean
// lists Q1-Q3 only; Q4 is "expected soon"). Emitting it against the legacy 24Q
// v7.2 layout would be guessing at the layout of the numbers TRACES reconciles
// against every employee's Form 130. A wrong Annexure II is a tax notice
// addressed to the employee. Refusing is the only honest answer, and it must be
// a NAMED error so the caller can tell "not built" from "you passed garbage".
func TestForm138_Q4IsRefusedNotGuessed(t *testing.T) {
	f := sampleStatement(t)
	f.Header.Quarter = "Q4"
	var buf bytes.Buffer
	err := WriteForm138AnnexureI(&buf, f)
	if !errors.Is(err, ErrQ4FormatNotPublished) {
		t.Fatalf("Q4 was not refused with ErrQ4FormatNotPublished (got %v). A guessed Annexure II mis-files every employee's annual salary detail and the demand lands on the employee, not on us.", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("%d bytes of a Q4 statement were written before the refusal; a partial file is still a file somebody uploads", buf.Len())
	}

	// And an unknown quarter is a DIFFERENT error: "we have not built that yet"
	// and "that is not a quarter" are different bugs in the caller.
	for _, q := range []string{"", "Q5", "q1", "1"} {
		f.Header.Quarter = q
		if err := WriteForm138AnnexureI(&buf, f); !errors.Is(err, ErrUnknownQuarter) {
			t.Errorf("quarter %q: got %v, want ErrUnknownQuarter", q, err)
		}
	}
}

// TestForm138_ChallanReconciliationRefusesAMismatch pins the reconciliation the
// statement exists to assert: money deposited with the government must equal
// money credited to employees. A file that fails it either short-credits an
// employee (they pay the tax twice) or claims a deposit that was never made.
func TestForm138_ChallanReconciliationRefusesAMismatch(t *testing.T) {
	f := sampleStatement(t)
	f.Challans[0].TotalTDSDeposited = dec(t, "17001") // one rupee more than the deductees
	var buf bytes.Buffer
	if err := WriteForm138AnnexureI(&buf, f); !errors.Is(err, ErrChallanTotalMismatch) {
		t.Fatalf("got %v, want ErrChallanTotalMismatch: the challan claims a deposit the deductee rows do not account for", err)
	}

	f = sampleStatement(t)
	f.Challans[0].Deductees[0].TotalTaxDeducted = dec(t, "9001") // != tax+surcharge+cess
	if err := WriteForm138AnnexureI(&buf, f); !errors.Is(err, ErrDeducteeTaxMismatch) {
		t.Fatalf("got %v, want ErrDeducteeTaxMismatch: a total that does not equal its parts credits the employee with an amount the challan never carried", err)
	}
}

// TestForm138_RefusesMalformedIdentifiers: a bad TAN or PAN is not a cosmetic
// problem. A deduction filed against an invalid PAN is not credited to anybody
// at TRACES, and the employee discovers it when their refund does not arrive.
func TestForm138_RefusesMalformedIdentifiers(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Form138File)
		wantErr error
	}{
		{"short TAN", func(f *Form138File) { f.Header.TAN = "BLRK1234F" }, ErrInvalidTAN},
		{"numeric TAN prefix", func(f *Form138File) { f.Header.TAN = "1LRK12345F" }, ErrInvalidTAN},
		{"deductor PAN", func(f *Form138File) { f.Header.DeductorPAN = "AAACD1234" }, ErrInvalidPAN},
		{"blank employee PAN", func(f *Form138File) { f.Challans[0].Deductees[0].PAN = "" }, ErrInvalidPAN},
		{"lowercase employee PAN", func(f *Form138File) { f.Challans[0].Deductees[0].PAN = "abcpk1234f" }, ErrInvalidPAN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := sampleStatement(t)
			tc.mutate(&f)
			var buf bytes.Buffer
			if err := WriteForm138AnnexureI(&buf, f); !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}

	// The reserved "no PAN" tokens ARE accepted: an employee without a PAN is
	// filed under one of them, never under a blank.
	for _, tok := range []string{"PANNOTAVBL", "PANAPPLIED", "PANINVALID"} {
		f := sampleStatement(t)
		f.Challans[0].Deductees[0].PAN = tok
		var buf bytes.Buffer
		if err := WriteForm138AnnexureI(&buf, f); err != nil {
			t.Errorf("reserved token %q was refused: %v", tok, err)
		}
	}
}

// TestForm138_EmptyStatementIsRefused: a challan with no deductees is counted
// by the BH and reconciles against nothing.
func TestForm138_EmptyStatementIsRefused(t *testing.T) {
	var buf bytes.Buffer
	f := sampleStatement(t)
	f.Challans = nil
	if err := WriteForm138AnnexureI(&buf, f); !errors.Is(err, ErrEmptyStatement) {
		t.Errorf("no challans: got %v, want ErrEmptyStatement", err)
	}
	f = sampleStatement(t)
	f.Challans[1].Deductees = nil
	f.Challans[1].TotalTDSDeposited = decimal.Zero
	if err := WriteForm138AnnexureI(&buf, f); !errors.Is(err, ErrEmptyStatement) {
		t.Errorf("challan with no deductees: got %v, want ErrEmptyStatement", err)
	}
}
