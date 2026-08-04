package form140

import (
	"time"

	"github.com/shopspring/decimal"
)

// Statement is everything a Form 140 file needs, fully specified.
//
// ===========================================================================
// THIS TYPE IS ALSO THE ANSWER TO "WHAT MUST THE SCHEMA STORE"
// ===========================================================================
// docs/design/t6-model.md §2.2 and §2.3 list twenty-one fields as MIGRATION.
// This struct is that list, made concrete by the spec rather than by a memo, and
// it is deliberately the shape a caller must FILL rather than one it may
// partially fill: a Statement with a zero-valued Deductor does not build.
//
// TWO SCHEMA GAPS BLOCK SOURCING THIS FROM THE BOOKS TODAY, and naming them
// precisely is more useful than calling the return "not done":
//
//  1. **THE CHALLAN REGISTER DOES NOT EXIST.** There is no tds_challan table, so
//     nothing records what was DEPOSITED — BSR code, deposit date, challan
//     serial, the split across tax/interest/fee/penalty, or which deductions a
//     challan covers. Challan below is the shape that table must store, and
//     note the structural consequence: EVERY DD RECORD CARRIES A CHALLAN
//     REFERENCE (field 5). A deductee record cannot exist outside a challan, so
//     this is not an ordering preference — the challan register is a hard
//     prerequisite for any non-nil return. (HANDOFF-NEXT-THREE.md §1.3 ranks it
//     third of three; the spec makes it a prerequisite for the first.)
//
//  2. **THE DEDUCTOR RETURN PROFILE DOES NOT EXIST.** `company` carries gstin,
//     address, city, pincode and country — and no TAN, no PAN, no Annexure 4
//     category, no e-mail, no contact number, and nothing at all about the
//     person responsible for deduction. That is BH fields 13, 15, 28-30, 32-45
//     and 59: a return cannot be filed without any of them.
//
// Neither gap is a research problem. Both are migrations, and both are now
// specified rather than estimated.
type Statement struct {
	// FileCreationDate is FH field 5, and it is ALSO the horizon for General
	// Note 8 ("No date should be a future date"). Passing it in rather than
	// reading the clock is what keeps this package pure and its golden test
	// deterministic — and it is the honest model besides, since the question is
	// "was this date in the future when the file was made", not "is it in the
	// future now".
	FileCreationDate time.Time
	// FileSequenceNo is FH field 6: "the running sequence number for the file
	// (should be unique across all the files)". Uniqueness is the caller's to
	// guarantee; this package only refuses a non-positive value.
	FileSequenceNo int
	// RPUName is FH field 10 — "Name of the software used for preparing the
	// Quarterly e-TDS/TCS statement". Mandatory, 75 chars.
	RPUName string

	Deductor Deductor

	// TaxYearStart is the calendar year the tax year begins in: 2026 = 2026-27.
	// Same convention as migrations 0049 and 0050. BH fields 16 and 17 are both
	// derived from it — the assessment year is the tax year plus one, and the
	// workbook requires 202728 for TY 2026-27.
	TaxYearStart int
	// Period is Q1, Q2, Q3 or Q4 (BH field 18).
	Period Quarter

	// PriorStatementFiled is BH field 52: "Y if regular statement for Form 140
	// has been filed for earlier period, else N". When true, PriorTokenNo
	// becomes mandatory (BH field 9).
	PriorStatementFiled bool
	PriorTokenNo        string

	// Challans is one entry per deposit, plus at most the nil-challan case.
	// Order is preserved and becomes the Challan-Detail Record Number, which
	// every deductee record references.
	Challans []Challan
}

// Deductor is the BH deductor and responsible-person block — the return
// profile that no table holds today.
type Deductor struct {
	// TAN is the ten-character Tax Deduction Account Number, ALL CAPITALS. It
	// is NOT the GSTIN and is not derivable from it.
	TAN string
	// PAN of the deductor. "If deductor is not required to have a PAN mention
	// PANNOTREQD" — the workbook's own placeholder, so the field is never blank.
	PAN string
	// Name is BH field 19. The workbook forbids a value that is only blanks,
	// only special characters or only dots.
	Name string
	// Address lines 1-5 and the country/region that the workbook numbers 6.
	// Only line 1 and the country are mandatory.
	Address1, Address2, Address3, Address4, Address5 string
	Country                                          string
	// StateCode is an Annexure 1 two-digit code, not a name.
	StateCode string
	Pincode   string
	// Email may be empty IF Responsible.Email is present, and vice versa. The
	// workbook prints both as M and then makes each conditional on the other.
	Email string
	// ContactCountryCode is the calling code, e.g. "91" — a separate field from
	// the number since this version of the format (BH 29 and 44).
	ContactCountryCode string
	ContactNumber      string
	// Category is an Annexure 4 code (BH field 32) — "K" for a company, "Q" for
	// an individual or HUF, and so on. See DeductorCategories, and the warning
	// there that this is NOT compliance's s.402(11) EntityCategory.
	Category string
	// StateName is BH field 54, an Annexure 5 code. Mandatory for the four
	// State-Government categories and FORBIDDEN for every other.
	StateName string
	// Ministry is BH field 57, an Annexure 3 code. Mandatory for the three
	// Central-Government categories, optional for four more, forbidden
	// otherwise. MinistryOther is required when Ministry is "99".
	Ministry      string
	MinistryOther string
	// AIN is BH field 68, required only for Government deductors paying by book
	// entry.
	AIN string
	// GSTIN is BH field 69 — optional, and the one field of this block the
	// company table already holds.
	GSTIN string

	Responsible ResponsiblePerson
}

// ResponsiblePerson is the human who signs the return. Every field is BH
// mandatory except the four optional address lines.
type ResponsiblePerson struct {
	Name        string
	Designation string
	// PAN is BH field 59 — the responsible person's own PAN, distinct from the
	// deductor's.
	PAN                                              string
	Address1, Address2, Address3, Address4, Address5 string
	Country                                          string
	StateCode                                        string
	Pincode                                          string
	Email                                            string
	ContactCountryCode                               string
	ContactNumber                                    string
}

// Challan is one deposit — a CD record and the deductee records under it.
//
// THIS IS THE SHAPE tds_challan MUST STORE. Every field below is a Form 140
// challan-block field with nowhere to live in the current schema
// (docs/design/t6-model.md §2.2), and the Deductees slice is §2.2's last row —
// "which deductions a challan covers" — which is a link, not a column.
type Challan struct {
	// Nil marks a challan under which nothing was deposited (CD field 6 = "Y").
	// A nil challan carries no BSR code, no challan number and no mode, its
	// DepositDate must be the last day of the quarter, and General Note 11
	// requires every deductee record under it to carry a reason flag.
	Nil bool

	// The four components of the deposit, CD fields 8-11. Each is a whole
	// rupee amount written at 2 decimals with paise "00" — the workbook is
	// explicit that "1000.50 will not be allowed" in these four and in their
	// total.
	TaxDeductedRupees decimal.Decimal
	InterestRupees    decimal.Decimal
	FeeRupees         decimal.Decimal
	PenaltyRupees     decimal.Decimal

	// BSRCode is the seven-digit code of the receiving bank branch, or for a
	// Government deductor paying by book entry the seven-digit Form 24G receipt
	// number from the AO. Empty for a nil challan.
	BSRCode string
	// ChallanNo is the bank's challan number when paying by challan, or the DDO
	// serial number when paying by book adjustment. Empty for a nil challan.
	ChallanNo string
	// ByBookEntry selects CD field 13: "B" when the deposit was a transfer
	// voucher / book adjustment (Government deductors), "C" when it was a bank
	// challan. It is a bool rather than the letter because the two are
	// exhaustive and a third letter is not a thing the format admits.
	ByBookEntry bool
	// DepositDate is CD field 19 — the date tax was paid to the Government.
	DepositDate time.Time
	// MinorHead is an Annexure 7 code; "200" is the ordinary case. Optional in
	// the workbook, so an empty value is emitted as empty rather than defaulted:
	// choosing 200 on the operator's behalf would be asserting that a deposit
	// was voluntary rather than demanded, which is not this package's to say.
	MinorHead string

	Deductees []Deductee
}

// Deductee is one row of the annexure — one payment, and the tax on it.
type Deductee struct {
	// PAN is mandatory. Where none is available the workbook prescribes its own
	// placeholders — "PANAPPLIED", "PANINVALID", "PANNOTAVBL" — which carry
	// different meanings and are not interchangeable, so this package passes
	// through whatever it is given and validates only the shape.
	PAN  string
	Name string
	// SectionCode is an Annexure 2 four-digit code, decided by
	// compliance.TDSReturnCode at derivation time and stored on the deduction.
	// It is NOT re-derived here.
	SectionCode string
	// Deposited is DD field 18 — whether the tax in TaxDeductedRupees has been
	// paid over. "Y" or "N".
	Deposited bool
	// PaymentDate is DD field 19, the date the sum was paid or credited. It must
	// fall within the quarter the statement covers.
	PaymentDate time.Time
	// AmountPaidRupees is DD field 20, and the workbook requires it to be
	// "always greater than 0.00" — a zero-amount deductee record is not a thing.
	AmountPaidRupees decimal.Decimal
	// TaxDeductedRupees and TaxDepositedRupees are DD fields 24 and 25. Zero is
	// legitimate for both: a sub-threshold payment is REPORTED, with a reason
	// flag, not omitted.
	TaxDeductedRupees  decimal.Decimal
	TaxDepositedRupees decimal.Decimal
	// DeductionDate is DD field 27 — mandatory when tax was deducted, and it
	// must carry NO VALUE when the tax is zero. It is not the same date as
	// PaymentDate when tax is deducted on credit and paid later.
	DeductionDate time.Time
	// RatePct is DD field 28, written at four decimals. Must be zero when the
	// tax is zero.
	RatePct decimal.Decimal
	// ReasonFlag is an Annexure 6 flag, or empty. It must be valid AGAINST
	// SectionCode — see reasonFlagValidity, and read the warning there before
	// assuming "Y" is always available for a sub-threshold payment.
	ReasonFlag string
	// CertificateNo395 is the fifteen-character s.395 lower-deduction
	// certificate, mandatory when ReasonFlag is "A". It is one of the two
	// identifiers docs/statutory/README.md flagged as having nowhere to live.
	CertificateNo395 string
	// UINForm121 is the other one: the Unique Identification Number of the Form
	// No. 121 the deductee submitted. Optional, 26 characters.
	UINForm121 string
	// CashWithdrawalOver1Cr and Over3Cr are DD fields 22 and 23, each mandatory
	// for exactly one section code (1065 and 1064) and forbidden otherwise.
	// Present for completeness of the layout; this engine derives neither.
	CashWithdrawalOver1Cr decimal.Decimal
	CashWithdrawalOver3Cr decimal.Decimal
}

// Quarter is BH field 18.
type Quarter string

const (
	Q1 Quarter = "Q1"
	Q2 Quarter = "Q2"
	Q3 Quarter = "Q3"
	Q4 Quarter = "Q4"
)

// bounds returns the first and last day of the quarter within a tax year that
// begins in April of taxYearStart.
//
// Q4 CROSSES THE CALENDAR YEAR — January to March of taxYearStart+1 — and that
// is the whole reason this is a function and not four constants.
func (q Quarter) bounds(taxYearStart int) (time.Time, time.Time, bool) {
	d := func(y int, m time.Month, day int) time.Time {
		return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	}
	switch q {
	case Q1:
		return d(taxYearStart, time.April, 1), d(taxYearStart, time.June, 30), true
	case Q2:
		return d(taxYearStart, time.July, 1), d(taxYearStart, time.September, 30), true
	case Q3:
		return d(taxYearStart, time.October, 1), d(taxYearStart, time.December, 31), true
	case Q4:
		return d(taxYearStart+1, time.January, 1), d(taxYearStart+1, time.March, 31), true
	}
	return time.Time{}, time.Time{}, false
}
