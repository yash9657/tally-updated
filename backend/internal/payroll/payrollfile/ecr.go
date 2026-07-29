// EPFO Electronic Challan-cum-Return (ECR) text file.
//
// ---------------------------------------------------------------------------
// FORMAT PROVENANCE — SECONDARY, effective ECR 2.0 (EPFO, from 2012, still the
// format in use as at the model's knowledge cutoff). NOT verified against the
// EPFO Unified Portal, which cannot be reached from this machine. See doc.go.
// ---------------------------------------------------------------------------
//
// One line per member. Fields are separated by the three-character delimiter
// `#~#`, there is NO header row, NO trailer row and NO comment syntax, and the
// file carries exactly these ELEVEN fields in this order:
//
//	 1  UAN                          12 digits, MANDATORY under ECR 2.0
//	 2  Member Name                  as per UAN
//	 3  Gross Wages                  whole rupees
//	 4  EPF Wages                    whole rupees
//	 5  EPS Wages                    whole rupees
//	 6  EDLI Wages                   whole rupees
//	 7  EPF Contribution Remitted    employee share, whole rupees
//	 8  EPS Contribution Remitted    whole rupees
//	 9  EPF & EPS Diff Remitted      whole rupees (see the invariant below)
//	10  NCP Days                     non-contributory period, whole days
//	11  Refund of Advances           whole rupees
//
// ADMIN AND INSPECTION CHARGES ARE CORRECTLY ABSENT. The Unified Portal derives
// them from the wages in this file when it generates the challan; a file that
// carried them would be asking the employer to pay them twice.
package payrollfile

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"
)

// ecrDelimiter is the ECR field separator. It is three characters, not one, and
// it is the reason member names must be rejected rather than escaped: the
// format has no escape syntax at all, so a name containing "#~#" would silently
// become two fields and shift every later field on the line by one.
const ecrDelimiter = "#~#"

// ecrFieldCount is the number of fields on every ECR line. A line with any
// other count is rejected by the portal, so the writer asserts it on the way
// out rather than trusting the code above it to have built eleven.
const ecrFieldCount = 11

var (
	// ErrBlankUAN is returned when a member row carries no UAN. Under ECR 2.0
	// the UAN is MANDATORY and there is no "blank means unknown" convention: a
	// blank field would be uploaded as a member with no identity, which either
	// rejects the whole file or credits the contribution to nobody. It is an
	// error, never a blank field.
	ErrBlankUAN = errors.New("payrollfile: ECR row has a blank UAN (mandatory under ECR 2.0)")
	// ErrInvalidUAN is returned for a UAN that is not exactly 12 digits.
	ErrInvalidUAN = errors.New("payrollfile: ECR UAN must be exactly 12 digits")
	// ErrNegativeAmount is returned for any negative money field in any of the
	// three files. A negative statutory contribution is not a correction
	// mechanism in any of these formats — corrections are separate returns — so
	// a negative here is always a computation bug upstream, and emitting it
	// would file it.
	ErrNegativeAmount = errors.New("payrollfile: negative amount in a statutory file")
	// ErrDelimiterInField is returned when a text field contains the format's
	// own delimiter (or a newline), which no format in this package can escape.
	ErrDelimiterInField = errors.New("payrollfile: text field contains the format delimiter or a line break")
	// ErrEPSExceedsEmployerEPF is returned when the rounded EPS contribution
	// exceeds the rounded employer EPF share, which would make the "EPF & EPS
	// Diff Remitted" column negative. EPS is carved OUT of the employer's 12%,
	// so this cannot happen arithmetically; if it does, the caller has swapped
	// the employee and employer shares.
	ErrEPSExceedsEmployerEPF = errors.New("payrollfile: EPS contribution exceeds the employer EPF share (EPF-EPS diff would be negative)")
	// ErrInvalidNCPDays is returned for an NCP day count outside 0..31.
	ErrInvalidNCPDays = errors.New("payrollfile: NCP days must be between 0 and 31")
)

// ECRRow is ONE member's line in the ECR. It is deliberately this package's own
// struct and not a payroll.Payslip: see the package doc on why the file layer
// must never dictate engine semantics.
//
// Money is decimal.Decimal at whatever precision the caller computed it, NEVER
// a float. This writer rounds each money field to whole rupees on the way out
// (the EPFO fields are integer-only, so there is no other option) using
// half-away-from-zero, which equals the statutory half-up for the non-negative
// values this file can contain.
//
// THE ROUNDING LIVES HERE, NOT IN THE CALLER, FOR ONE REASON: field 9 must be
// the SUBTRACTION OF TWO ALREADY-ROUNDED COMPONENTS. If the caller rounded and
// this writer merely printed, nothing in the codebase would guarantee that the
// three columns 7/8/9 are mutually consistent, and the off-by-one below would
// be reintroduced by the next caller. Rounding is idempotent, so a caller that
// has already rounded loses nothing.
type ECRRow struct {
	// UAN is the 12-digit Universal Account Number. Mandatory.
	UAN string
	// MemberName is the member's name as per UAN.
	MemberName string
	// GrossWages is total gross wages paid for the month.
	GrossWages decimal.Decimal
	// EPFWages, EPSWages and EDLIWages are THREE DIFFERENT NUMBERS and this
	// writer never collapses them or derives one from another. They diverge in
	// ordinary cases: a member above the statutory ceiling whose employer pays
	// PF on actual wages has EPF wages above the ceiling while EPS and EDLI
	// wages stay at the ceiling; a member excluded from EPS (a new member joining
	// above the ceiling) has EPS wages of zero while EPF and EDLI wages are
	// positive. Collapsing them mis-states the pension liability for the rest of
	// that member's working life.
	EPFWages  decimal.Decimal
	EPSWages  decimal.Decimal
	EDLIWages decimal.Decimal
	// EmployeeEPFContribution is field 7, "EPF Contribution Remitted" — the
	// EMPLOYEE's share.
	EmployeeEPFContribution decimal.Decimal
	// EmployerEPFContribution is the EMPLOYER's total EPF share. It has no
	// column of its own: field 9 is what remains of it after EPS is carved out.
	EmployerEPFContribution decimal.Decimal
	// EPSContribution is field 8.
	EPSContribution decimal.Decimal
	// NCPDays is the non-contributory period in whole days (0..31).
	NCPDays int
	// RefundOfAdvances is field 11, ordinarily zero.
	RefundOfAdvances decimal.Decimal
}

// WriteECR writes the ECR text file for a wage month.
//
// Lines are newline-terminated, including the last one. There is no header,
// trailer or comment: see doc.go for why the not-validator-verified disclosure
// travels in a sidecar instead.
func WriteECR(w io.Writer, rows []ECRRow) error {
	if len(rows) == 0 {
		return fmt.Errorf("payrollfile: ECR with no member rows would file a nil return for the month; refuse rather than emit an empty file")
	}
	var b strings.Builder
	for i, r := range rows {
		line, err := ecrLine(r)
		if err != nil {
			return fmt.Errorf("payrollfile: ECR row %d: %w", i+1, err)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("payrollfile: write ECR: %w", err)
	}
	return nil
}

// ecrLine renders one member row, or reports why it cannot be filed.
func ecrLine(r ECRRow) (string, error) {
	if strings.TrimSpace(r.UAN) == "" {
		return "", ErrBlankUAN
	}
	if !isDigits(r.UAN, 12) {
		return "", fmt.Errorf("%w: got %d characters", ErrInvalidUAN, len(r.UAN))
	}
	name := strings.TrimSpace(r.MemberName)
	if name == "" {
		// Deliberately identifies the row by position only. Error strings end up
		// in HTTP responses and in the operator's log; a UAN, a PAN or a name
		// interpolated into one is PII that has left the payroll boundary. The
		// caller knows which row index it handed us.
		return "", errors.New("payrollfile: ECR member name is blank")
	}
	if err := rejectDelimiters(name, ecrDelimiter); err != nil {
		return "", err
	}
	if r.NCPDays < 0 || r.NCPDays > 31 {
		return "", fmt.Errorf("%w: got %d", ErrInvalidNCPDays, r.NCPDays)
	}

	// Round each money component ONCE, here, to whole rupees.
	gross, err := rupees(r.GrossWages, "gross wages")
	if err != nil {
		return "", err
	}
	epfWages, err := rupees(r.EPFWages, "EPF wages")
	if err != nil {
		return "", err
	}
	epsWages, err := rupees(r.EPSWages, "EPS wages")
	if err != nil {
		return "", err
	}
	edliWages, err := rupees(r.EDLIWages, "EDLI wages")
	if err != nil {
		return "", err
	}
	employeeEPF, err := rupees(r.EmployeeEPFContribution, "employee EPF contribution")
	if err != nil {
		return "", err
	}
	employerEPF, err := rupees(r.EmployerEPFContribution, "employer EPF contribution")
	if err != nil {
		return "", err
	}
	eps, err := rupees(r.EPSContribution, "EPS contribution")
	if err != nil {
		return "", err
	}
	refund, err := rupees(r.RefundOfAdvances, "refund of advances")
	if err != nil {
		return "", err
	}

	// FIELD 9 — THE INVARIANT THIS WHOLE FILE TURNS ON.
	//
	// "EPF & EPS Diff Remitted" is the SUBTRACTION OF THE TWO ALREADY-ROUNDED
	// COMPONENTS, never the difference computed at full precision and rounded
	// afterwards. The two disagree by a rupee whenever the fractional parts
	// straddle the half — e.g. employer EPF 1000.40 and EPS 500.60 round to 1000
	// and 501, so the diff is 499, while rounding (1000.40 - 500.60) = 499.80
	// gives 500. Field 9 must reconcile against fields printed in the SAME file:
	// the portal recomputes employer share = field 8 + field 9 and rejects the
	// upload when that does not equal 12% of field 4 within its tolerance, so
	// the "more accurate" single rounding is the one that gets the file bounced.
	// TestECR_DiffIsRoundedComponentsSubtracted pins exactly this case.
	if eps.GreaterThan(employerEPF) {
		return "", fmt.Errorf("%w: EPS %s > employer EPF %s", ErrEPSExceedsEmployerEPF, eps, employerEPF)
	}
	diff := employerEPF.Sub(eps)

	fields := []string{
		r.UAN,
		name,
		gross.String(),
		epfWages.String(),
		epsWages.String(),
		edliWages.String(),
		employeeEPF.String(),
		eps.String(),
		diff.String(),
		fmt.Sprintf("%d", r.NCPDays),
		refund.String(),
	}
	if len(fields) != ecrFieldCount {
		return "", fmt.Errorf("payrollfile: ECR line has %d fields, want %d", len(fields), ecrFieldCount)
	}
	return strings.Join(fields, ecrDelimiter), nil
}
