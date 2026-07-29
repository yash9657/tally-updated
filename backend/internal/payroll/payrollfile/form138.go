// Form 138 (ex-Form 24Q) — the quarterly statement of tax deducted from salary,
// ANNEXURE I only, quarters 1 to 3 only.
//
// ---------------------------------------------------------------------------
// FORMAT PROVENANCE — SECONDARY, AND WEAKER THAN THE ECR'S
// ---------------------------------------------------------------------------
// The field-level lengths, the mandatory/optional flags and the FVU validations
// come from SECONDARY sources. The official ITD "File Format for Form 24Q" data
// structure returned HTTP 403 from this machine and the legacy v7.2 .xls could
// not be opened here, so what follows is the record ORDER, the DELIMITER and the
// field SEQUENCE as best they are known — not a transcription of the primary
// document. The real statement carries more fields per record than we can name
// with confidence (the deductor address block and the responsible-person block
// in particular), and we do NOT pad the records out with invented values: a
// blank in the right position and a guess in the wrong position are both
// rejections, but only one of them is honest. This file is NOT claimed to load
// in the FVU. See doc.go.
//
// ---------------------------------------------------------------------------
// FORMAT
// ---------------------------------------------------------------------------
// Clean ASCII text, one record per line, newline-terminated. Fields are
// separated by a caret `^` and are VARIABLE WIDTH: no leading zeros on numeric
// fields, no trailing spaces on character fields, no padding of any kind. An
// empty optional field is an empty field between two carets.
//
// Record types, in order, exactly once per file for FH and BH:
//
//	FH  File Header   — one per file
//	BH  Batch Header  — one per file (one batch per statement), carrying the
//	                    COUNTS of the records that follow it
//	CD  Challan Detail — one per challan
//	DD  Deductee Detail — one per employee per challan, following its CD
//
// THE SALARY-DETAIL (SD) COUNT IN THE BATCH HEADER IS ZERO, and that zero is a
// statement, not a placeholder: Annexure II — the whole-year salary detail — is
// filed with Q4 only, and this package does not build Q4 (see
// [ErrQ4FormatNotPublished]). A reader of the file can therefore tell that no SD
// record was omitted by accident.
package payrollfile

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// form138Delimiter is the caret. Like the ECR's `#~#` it has no escape syntax,
// so a character field containing it is refused rather than escaped.
const form138Delimiter = "^"

var (
	// ErrQ4FormatNotPublished is returned when a Q4 file is requested.
	//
	// This is the single most important refusal in this package. Q4 carries
	// Annexure II — every employee's whole-year taxable salary, deductions and
	// the tax computed on them — and the Q4 record structure is NOT PUBLISHED:
	// Protean lists the Q1-Q3 structure only and describes Q4's as "expected
	// soon". Emitting Q4 against the legacy 24Q v7.2 layout would be a guess at
	// the layout of the numbers that TRACES reconciles against every employee's
	// Form 130, and a wrong Annexure II is a tax notice addressed to the
	// employee, not to us. We refuse and say why.
	ErrQ4FormatNotPublished = errors.New("payrollfile: Form 138 Q4 / Annexure II is not built: the Q4 record format is not published and guessing it would mis-file every employee's annual salary detail")
	// ErrUnknownQuarter is returned for a quarter outside Q1..Q4. It is
	// deliberately distinct from ErrQ4FormatNotPublished: "we cannot do that
	// yet" and "that is not a quarter" are different bugs in the caller.
	ErrUnknownQuarter = errors.New("payrollfile: unknown quarter (want Q1, Q2 or Q3)")
	// ErrInvalidTAN is returned for a TAN that is not AAAA99999A.
	ErrInvalidTAN = errors.New("payrollfile: TAN must be 4 letters, 5 digits, 1 letter")
	// ErrInvalidPAN is returned for a PAN that is neither AAAAA9999A nor one of
	// the reserved "PAN not available" tokens.
	ErrInvalidPAN = errors.New("payrollfile: PAN must be 5 letters, 4 digits, 1 letter (or a reserved PANNOTAVBL/PANAPPLIED/PANINVALID token)")
	// ErrDeducteeTaxMismatch is returned when a deductee's total tax deducted is
	// not the sum of its tax, surcharge and cess. The FVU recomputes this; more
	// importantly, the three columns are what the employee's Form 130 is built
	// from, so a total that does not equal its parts credits the employee with
	// an amount the challan never carried.
	ErrDeducteeTaxMismatch = errors.New("payrollfile: deductee total tax deducted != tax + surcharge + cess")
	// ErrChallanTotalMismatch is returned when the deductee rows attached to a
	// challan do not sum to the tax deposited by that challan. This is THE
	// reconciliation the whole statement exists to assert: money paid to the
	// government must equal money credited to employees. A file that fails it
	// either short-credits an employee or claims a deposit that was never made.
	ErrChallanTotalMismatch = errors.New("payrollfile: deductee tax deposited does not sum to the challan total")
	// ErrEmptyStatement is returned for a file with no challan, or a challan
	// with no deductee. A CD the BH counts but nothing reconciles against is a
	// silently wrong statement.
	ErrEmptyStatement = errors.New("payrollfile: Form 138 statement has no challan, or a challan with no deductee")
)

// Form138Header is the statement-level information. The writer derives BOTH the
// FH and the BH record from it — in particular it computes the record counts
// itself, so the counts in the BH cannot disagree with the records that follow
// (which is a class of FVU rejection the caller cannot cause here).
type Form138Header struct {
	// TAN of the deductor, AAAA99999A.
	TAN string
	// DeductorPAN, AAAAA9999A.
	DeductorPAN string
	// DeductorName as registered against the TAN.
	DeductorName string
	// FinancialYear as six digits, e.g. "202627" for FY 2026-27.
	FinancialYear string
	// Quarter is "Q1", "Q2" or "Q3". "Q4" is refused: see
	// [ErrQ4FormatNotPublished].
	Quarter string
	// FormNumber is "138" (the Income-tax Act 2025 name) or "24Q" (the name the
	// utilities still use). The caller chooses; we do not translate one into the
	// other, because which one the receiving system expects is exactly the kind
	// of thing this package has not been able to verify.
	FormNumber string
	// FileCreationDate stamps the FH.
	FileCreationDate time.Time
	// FileSequenceNumber distinguishes files created on the same day; >= 1.
	FileSequenceNumber int
	// ProducerVersion identifies the software that wrote the file.
	ProducerVersion string
}

// Form138Challan is one challan (or one book-entry transfer voucher) and the
// deductees whose tax it carries.
type Form138Challan struct {
	// BSRCode is the 7-digit bank branch code. It is a CHARACTER field, not a
	// numeric one: real BSR codes begin with 0 and stripping that zero makes the
	// challan unmatchable at TRACES.
	BSRCode string
	// DateOfDeposit is the challan tender date.
	DateOfDeposit time.Time
	// ChallanSerialNumber is the bank's serial for the day.
	ChallanSerialNumber int
	// BookEntry is true when a government deductor transferred by book entry
	// rather than paying a bank challan; the BSR code and serial are then the
	// transfer voucher's.
	BookEntry bool
	// NilChallan is true for a nil statement's challan; the totals must then be
	// zero.
	NilChallan bool
	// TotalTDSDeposited must equal the sum of the deductees' TotalTaxDeposited.
	TotalTDSDeposited decimal.Decimal
	// TotalInterest and TotalFee (s.234E) are deposited with the same challan
	// and are NOT credited to any employee, which is why they are excluded from
	// the deductee reconciliation above.
	TotalInterest decimal.Decimal
	TotalFee      decimal.Decimal
	// Deductees are the employees whose deduction this challan carries.
	Deductees []Form138Deductee
}

// Form138Deductee is one employee's DD record under a challan.
type Form138Deductee struct {
	// EmployeeSerialNumber is the employee's serial in the statement.
	EmployeeSerialNumber int
	// EmployeeReference is the employer's own reference; may be blank.
	EmployeeReference string
	// PAN is the employee's PAN, or one of the reserved tokens.
	PAN string
	// Name is the employee's name as per PAN.
	Name string
	// DateOfPayment is the date the salary was paid or credited; DateOfDeduction
	// is the date tax was deducted. They are ordinarily the same day and are
	// separate fields because they are separate fields in the statement.
	DateOfPayment   time.Time
	DateOfDeduction time.Time
	// AmountPaid is the salary paid or credited.
	AmountPaid decimal.Decimal
	// Tax, Surcharge and Cess must sum to TotalTaxDeducted.
	Tax       decimal.Decimal
	Surcharge decimal.Decimal
	Cess      decimal.Decimal
	// TotalTaxDeducted is what was deducted; TotalTaxDeposited is what this
	// challan deposited on the employee's behalf. They differ only in a
	// correction statement, which this package does not produce.
	TotalTaxDeducted  decimal.Decimal
	TotalTaxDeposited decimal.Decimal
	// Remarks carries the reason for a lower or nil deduction; blank otherwise.
	Remarks string
}

// Form138File is a whole Annexure I statement: one header, one or more challans.
type Form138File struct {
	Header   Form138Header
	Challans []Form138Challan
}

// fieldKind classifies a rendered field so the format rules can be asserted
// mechanically rather than by eye.
//
// It exists because "no leading zeros on numerics" is NOT a rule about every
// field: a date is 01072026 and a BSR code is 0221826, and stripping their
// leading zeros would corrupt them. The kind is what lets
// TestForm138_CaretDelimitedNoLeadingZerosNoTrailingSpaces apply the rule to
// exactly the fields it applies to.
type fieldKind int

const (
	kindChar   fieldKind = iota // free text or a fixed-width identifier
	kindNum                     // an integer: no leading zeros
	kindAmount                  // money with two decimals: no leading zeros
	kindDate                    // DDMMYYYY: leading zeros are part of the value
)

// f138Field is one rendered field with the kind it was rendered as.
type f138Field struct {
	kind fieldKind
	name string
	val  string
}

// f138Record is one line of the file.
type f138Record []f138Field

// recordType returns the record type code (field 2 of every record).
func (r f138Record) recordType() string {
	if len(r) < 2 {
		return ""
	}
	return r[1].val
}

// WriteForm138AnnexureI writes the Q1-Q3 Annexure I statement.
//
// It REFUSES a Q4 file with [ErrQ4FormatNotPublished] rather than emitting a
// guess.
func WriteForm138AnnexureI(w io.Writer, f Form138File) error {
	recs, err := buildForm138Records(f)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, rec := range recs {
		vals := make([]string, len(rec))
		for i, fl := range rec {
			if strings.Contains(fl.val, form138Delimiter) {
				return fmt.Errorf("%w: field %q of record %s contains a caret", ErrDelimiterInField, fl.name, rec.recordType())
			}
			if strings.ContainsAny(fl.val, "\r\n") {
				return fmt.Errorf("%w: field %q of record %s contains a line break", ErrDelimiterInField, fl.name, rec.recordType())
			}
			vals[i] = fl.val
		}
		b.WriteString(strings.Join(vals, form138Delimiter))
		b.WriteString("\n")
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("payrollfile: write Form 138: %w", err)
	}
	return nil
}

// buildForm138Records renders the whole statement into typed records. It is
// separate from the writer so a test can inspect field KINDS, which a rendered
// line no longer carries.
func buildForm138Records(f Form138File) ([]f138Record, error) {
	h := f.Header
	switch h.Quarter {
	case "Q1", "Q2", "Q3":
	case "Q4":
		return nil, ErrQ4FormatNotPublished
	default:
		return nil, fmt.Errorf("%w: got %q", ErrUnknownQuarter, h.Quarter)
	}
	if !isTAN(h.TAN) {
		return nil, ErrInvalidTAN
	}
	if !isPAN(h.DeductorPAN) {
		return nil, fmt.Errorf("%w: deductor PAN", ErrInvalidPAN)
	}
	if !isDigits(h.FinancialYear, 6) {
		return nil, fmt.Errorf("payrollfile: financial year must be six digits (e.g. 202627), got %q", h.FinancialYear)
	}
	if h.FormNumber != "138" && h.FormNumber != "24Q" {
		return nil, fmt.Errorf("payrollfile: form number must be \"138\" or \"24Q\", got %q", h.FormNumber)
	}
	if strings.TrimSpace(h.DeductorName) == "" {
		return nil, errors.New("payrollfile: deductor name is blank")
	}
	if h.FileSequenceNumber < 1 {
		return nil, fmt.Errorf("payrollfile: file sequence number must be >= 1, got %d", h.FileSequenceNumber)
	}
	if h.FileCreationDate.IsZero() {
		return nil, errors.New("payrollfile: file creation date is zero")
	}
	if len(f.Challans) == 0 {
		return nil, ErrEmptyStatement
	}

	// COUNTS ARE DERIVED, NEVER TAKEN FROM THE CALLER. The BH's counts and the
	// records that follow it cannot disagree, because they have one source.
	challanCount := len(f.Challans)
	deducteeCount := 0
	for _, c := range f.Challans {
		if len(c.Deductees) == 0 {
			return nil, ErrEmptyStatement
		}
		deducteeCount += len(c.Deductees)
	}

	line := 0
	next := func() string { line++; return fmt.Sprintf("%d", line) }
	recs := make([]f138Record, 0, 2+challanCount+deducteeCount)

	// ---- FH: File Header -------------------------------------------------
	recs = append(recs, f138Record{
		{kindNum, "line number", next()},
		{kindChar, "record type", "FH"},
		// "NS1" = a regular (non-correction) statement upload. Corrections are
		// a different file type and are not built.
		{kindChar, "file type", "NS1"},
		{kindChar, "upload type", "R"},
		{kindDate, "file creation date", ddmmyyyy(h.FileCreationDate)},
		{kindNum, "file sequence number", fmt.Sprintf("%d", h.FileSequenceNumber)},
		{kindChar, "uploader type", "D"},
		{kindChar, "TAN", h.TAN},
		{kindNum, "number of batches", "1"},
		{kindChar, "producer version", h.ProducerVersion},
	})

	// ---- BH: Batch Header ------------------------------------------------
	recs = append(recs, f138Record{
		{kindNum, "line number", next()},
		{kindChar, "record type", "BH"},
		{kindNum, "batch number", "1"},
		{kindNum, "count of challan (CD) records", fmt.Sprintf("%d", challanCount)},
		{kindChar, "form number", h.FormNumber},
		{kindChar, "transaction type", "Regular"},
		// Zero because Annexure II is a Q4 filing and Q4 is refused above. It is
		// not a placeholder for a record we forgot.
		{kindNum, "count of salary detail (SD) records", "0"},
		{kindNum, "count of deductee (DD) records", fmt.Sprintf("%d", deducteeCount)},
		{kindChar, "TAN", h.TAN},
		{kindChar, "deductor PAN", h.DeductorPAN},
		{kindChar, "financial year", h.FinancialYear},
		{kindChar, "quarter", h.Quarter},
		{kindChar, "deductor name", strings.TrimSpace(h.DeductorName)},
	})

	// ---- CD / DD ---------------------------------------------------------
	for ci, c := range f.Challans {
		cd, err := challanRecord(c, ci+1, next())
		if err != nil {
			return nil, fmt.Errorf("payrollfile: challan %d: %w", ci+1, err)
		}
		recs = append(recs, cd)
		for di, d := range c.Deductees {
			dd, err := deducteeRecord(d, ci+1, di+1, next())
			if err != nil {
				return nil, fmt.Errorf("payrollfile: challan %d deductee %d: %w", ci+1, di+1, err)
			}
			recs = append(recs, dd)
		}
	}
	return recs, nil
}

// challanRecord renders one CD and asserts the deductee reconciliation.
func challanRecord(c Form138Challan, challanNo int, lineNo string) (f138Record, error) {
	if !isDigits(c.BSRCode, 7) {
		return nil, fmt.Errorf("payrollfile: BSR code must be exactly 7 digits, got %q", c.BSRCode)
	}
	if c.DateOfDeposit.IsZero() {
		return nil, errors.New("payrollfile: challan date of deposit is zero")
	}
	if c.ChallanSerialNumber < 0 {
		return nil, fmt.Errorf("payrollfile: challan serial number must be >= 0, got %d", c.ChallanSerialNumber)
	}

	// The reconciliation: money deposited == money credited to employees.
	sum := decimal.Zero
	for _, d := range c.Deductees {
		sum = sum.Add(d.TotalTaxDeposited)
	}
	if !sum.Equal(c.TotalTDSDeposited) {
		return nil, fmt.Errorf("%w: deductees sum to %s, challan says %s", ErrChallanTotalMismatch, sum.StringFixed(2), c.TotalTDSDeposited.StringFixed(2))
	}
	if c.NilChallan && !c.TotalTDSDeposited.IsZero() {
		return nil, fmt.Errorf("payrollfile: challan %d is flagged NIL but deposits %s", challanNo, c.TotalTDSDeposited.StringFixed(2))
	}

	tds, err := amount2(c.TotalTDSDeposited, "challan total TDS")
	if err != nil {
		return nil, err
	}
	interest, err := amount2(c.TotalInterest, "challan interest")
	if err != nil {
		return nil, err
	}
	fee, err := amount2(c.TotalFee, "challan fee u/s 234E")
	if err != nil {
		return nil, err
	}
	return f138Record{
		{kindNum, "line number", lineNo},
		{kindChar, "record type", "CD"},
		{kindNum, "batch number", "1"},
		{kindNum, "challan record number", fmt.Sprintf("%d", challanNo)},
		{kindNum, "count of deductee records for this challan", fmt.Sprintf("%d", len(c.Deductees))},
		{kindChar, "NIL challan indicator", yn(c.NilChallan)},
		{kindChar, "BSR code", c.BSRCode},
		{kindDate, "date of deposit", ddmmyyyy(c.DateOfDeposit)},
		{kindNum, "challan serial number", fmt.Sprintf("%d", c.ChallanSerialNumber)},
		{kindChar, "deposited by book entry", yn(c.BookEntry)},
		{kindAmount, "total TDS deposited", tds},
		{kindAmount, "total interest", interest},
		{kindAmount, "total fee u/s 234E", fee},
	}, nil
}

// deducteeRecord renders one DD and asserts that its total equals its parts.
func deducteeRecord(d Form138Deductee, challanNo, deducteeNo int, lineNo string) (f138Record, error) {
	if !isPAN(d.PAN) {
		return nil, fmt.Errorf("%w: deductee PAN", ErrInvalidPAN)
	}
	name := strings.TrimSpace(d.Name)
	if name == "" {
		return nil, errors.New("payrollfile: deductee name is blank")
	}
	if err := rejectDelimiters(name, form138Delimiter); err != nil {
		return nil, err
	}
	if err := rejectDelimiters(strings.TrimSpace(d.EmployeeReference), form138Delimiter); err != nil {
		return nil, err
	}
	if err := rejectDelimiters(strings.TrimSpace(d.Remarks), form138Delimiter); err != nil {
		return nil, err
	}
	if d.EmployeeSerialNumber < 1 {
		return nil, fmt.Errorf("payrollfile: employee serial number must be >= 1, got %d", d.EmployeeSerialNumber)
	}
	if d.DateOfPayment.IsZero() || d.DateOfDeduction.IsZero() {
		return nil, errors.New("payrollfile: deductee date of payment or deduction is zero")
	}
	if want := d.Tax.Add(d.Surcharge).Add(d.Cess); !want.Equal(d.TotalTaxDeducted) {
		return nil, fmt.Errorf("%w: parts sum to %s, total says %s", ErrDeducteeTaxMismatch, want.StringFixed(2), d.TotalTaxDeducted.StringFixed(2))
	}

	paid, err := amount2(d.AmountPaid, "amount paid or credited")
	if err != nil {
		return nil, err
	}
	tax, err := amount2(d.Tax, "tax")
	if err != nil {
		return nil, err
	}
	sur, err := amount2(d.Surcharge, "surcharge")
	if err != nil {
		return nil, err
	}
	cess, err := amount2(d.Cess, "cess")
	if err != nil {
		return nil, err
	}
	totDed, err := amount2(d.TotalTaxDeducted, "total tax deducted")
	if err != nil {
		return nil, err
	}
	totDep, err := amount2(d.TotalTaxDeposited, "total tax deposited")
	if err != nil {
		return nil, err
	}
	return f138Record{
		{kindNum, "line number", lineNo},
		{kindChar, "record type", "DD"},
		{kindNum, "batch number", "1"},
		{kindNum, "challan record number", fmt.Sprintf("%d", challanNo)},
		{kindNum, "deductee record number", fmt.Sprintf("%d", deducteeNo)},
		{kindChar, "mode", "O"},
		{kindNum, "employee serial number", fmt.Sprintf("%d", d.EmployeeSerialNumber)},
		{kindChar, "employee reference number", strings.TrimSpace(d.EmployeeReference)},
		{kindChar, "PAN of employee", d.PAN},
		{kindChar, "name of employee", name},
		{kindDate, "date of payment or credit", ddmmyyyy(d.DateOfPayment)},
		{kindAmount, "amount paid or credited", paid},
		{kindAmount, "tax", tax},
		{kindAmount, "surcharge", sur},
		{kindAmount, "cess", cess},
		{kindAmount, "total tax deducted", totDed},
		{kindAmount, "total tax deposited", totDep},
		{kindDate, "date of deduction", ddmmyyyy(d.DateOfDeduction)},
		{kindChar, "remarks", strings.TrimSpace(d.Remarks)},
	}, nil
}

// ddmmyyyy renders a date the way every ITD text format wants it. The leading
// zeros are part of the value, which is why the field is kindDate and not
// kindNum.
func ddmmyyyy(t time.Time) string { return t.Format("02012006") }

// yn renders a Y/N indicator.
func yn(b bool) string {
	if b {
		return "Y"
	}
	return "N"
}

// isTAN reports whether s is AAAA99999A.
func isTAN(s string) bool {
	if len(s) != 10 {
		return false
	}
	return allUpper(s[0:4]) && isDigits(s[4:9], 5) && allUpper(s[9:10])
}

// panNotAvailableTokens are the reserved values the statement accepts in place
// of a PAN. They are a CLOSED set: an employee with no PAN is filed under one of
// these, never under a blank (a blank PAN silently becomes an unmatched
// deduction at TRACES and the employee never gets the credit).
var panNotAvailableTokens = map[string]bool{
	"PANNOTAVBL": true,
	"PANAPPLIED": true,
	"PANINVALID": true,
}

// isPAN reports whether s is AAAAA9999A or a reserved token.
func isPAN(s string) bool {
	if panNotAvailableTokens[s] {
		return true
	}
	if len(s) != 10 {
		return false
	}
	return allUpper(s[0:5]) && isDigits(s[5:9], 4) && allUpper(s[9:10])
}

// allUpper reports whether s is entirely A-Z.
func allUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return len(s) > 0
}
