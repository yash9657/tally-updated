// ESIC monthly contribution return.
//
// ---------------------------------------------------------------------------
// FORMAT PROVENANCE — SECONDARY, AND DELIBERATELY NOT THE PORTAL'S FORMAT
// ---------------------------------------------------------------------------
// The ESIC employer portal reportedly accepts an Excel 97-2003 (BIFF8) workbook
// and rejects .xlsx. Emitting BIFF8 from Go needs a new third-party dependency,
// which is a decision outside this track's mandate, so this package emits CSV
// carrying the portal's own column headers and the operator pastes it into the
// official template. WE DO NOT CLAIM PORTAL-ACCEPTABLE OUTPUT. The column
// headers, the date format and the leaving-reason vocabulary come from
// SECONDARY sources and have not been checked against the portal, which cannot
// be reached from this machine. See doc.go.
//
// ---------------------------------------------------------------------------
// THE COLUMNS
// ---------------------------------------------------------------------------
//
//	IP Number                            10 digits, the insured person's number
//	IP Name                              as registered with ESIC
//	No. of Days for which wages paid     a WHOLE number; fractions round UP
//	Total Monthly Wages                  whole rupees
//	Reason code for 0 wages              closed vocabulary, blank when none
//	Last Working Day                     DD/MM/YYYY, blank when there is none
package payrollfile

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// esiHeaders are the ESIC template's column headers, verbatim and in order. The
// portal matches on them, so they are data and not a caption: changing the
// wording here changes what the operator can paste.
var esiHeaders = []string{
	"IP Number",
	"IP Name",
	"No. of Days for which wages paid",
	"Total Monthly Wages",
	"Reason code for 0 wages",
	"Last Working Day",
}

// The CLOSED vocabulary of leaving/zero-wage reasons.
//
// It is closed for the same reason every other vocabulary in this codebase is:
// the portal has a dropdown, and a value outside it is not "a slightly unusual
// reason", it is a row the portal cannot accept. A free-text reason would be
// discovered at upload time by an operator who has no idea what to type instead.
// An unrecognised value is an ERROR — never silently blanked, because a blank
// reason against zero wages is itself a rejection and would look like our
// output was accepted-but-empty.
const (
	ESIReasonLeftService    = "Employee has left service"
	ESIReasonRetired        = "Retired"
	ESIReasonOutOfCoverage  = "Out of Coverage"
	ESIReasonExpired        = "Expired"
	ESIReasonNonImplemented = "Non-implemented Area"
	ESIReasonRetrenchment   = "Retrenchment"
)

// esiReasons is the membership test for the vocabulary above.
var esiReasons = map[string]bool{
	ESIReasonLeftService:    true,
	ESIReasonRetired:        true,
	ESIReasonOutOfCoverage:  true,
	ESIReasonExpired:        true,
	ESIReasonNonImplemented: true,
	ESIReasonRetrenchment:   true,
}

// esiReasonNeedsLastWorkingDay splits the vocabulary into the four reasons that
// are TERMINATIONS — each of which has a definite last working day the portal
// wants — and the two that are COVERAGE STATES, which do not.
//
// This is our judgement, not a transcribed portal rule, and it is here rather
// than absent because the alternative failure is silent: a leaver filed with no
// last working day stays on the employer's ESIC roll, and the contribution
// history that decides whether that person's family can claim sickness or
// dependants' benefit is wrong in the employee's disfavour.
var esiReasonNeedsLastWorkingDay = map[string]bool{
	ESIReasonLeftService:  true,
	ESIReasonRetired:      true,
	ESIReasonExpired:      true,
	ESIReasonRetrenchment: true,
}

var (
	// ErrUnknownESIReason is returned for a reason outside the closed
	// vocabulary.
	ErrUnknownESIReason = errors.New("payrollfile: ESI reason is not in the closed vocabulary")
	// ErrInvalidIPNumber is returned for an IP number that is not 10 digits.
	ErrInvalidIPNumber = errors.New("payrollfile: ESIC IP number must be exactly 10 digits")
	// ErrInvalidESIDays is returned for a day count outside 0..31.
	ErrInvalidESIDays = errors.New("payrollfile: ESI days for which wages paid must be between 0 and 31")
	// ErrZeroWagesNeedsReason is returned when a row reports zero paid days and
	// no reason. The column is literally called "Reason code for 0 wages": a
	// zero-day row without one is the case the column exists for.
	ErrZeroWagesNeedsReason = errors.New("payrollfile: zero paid days requires a reason from the closed vocabulary")
	// ErrLastWorkingDayWithoutReason is returned when a last working day is
	// supplied with no reason, which the portal cannot interpret.
	ErrLastWorkingDayWithoutReason = errors.New("payrollfile: last working day supplied with no leaving reason")
	// ErrMissingLastWorkingDay is returned when a termination reason carries no
	// last working day.
	ErrMissingLastWorkingDay = errors.New("payrollfile: termination reason requires a last working day")
	// ErrFractionalWages is returned when monthly wages are not whole rupees.
	//
	// THIS WRITER DELIBERATELY DOES NOT ROUND WAGES, and the asymmetry with the
	// ECR (which does round) is the point. The ECR rounds because its "EPF & EPS
	// Diff" column must be the subtraction of two ALREADY-ROUNDED components, so
	// the rounding and the subtraction cannot be split across two packages
	// without the invariant becoming advisory. Nothing here is coupled like
	// that: the ESI wage is a number the engine computed under Rule 51, and the
	// portal derives both contributions from it. If this writer rounded it, a
	// file emitter would be silently changing the base of somebody's statutory
	// contribution. The caller decides the rounding; we refuse to invent one.
	ErrFractionalWages = errors.New("payrollfile: ESI monthly wages must be whole rupees; the caller must decide the rounding, not the file writer")
)

// ESIRow is one insured person's row in the monthly contribution return. Like
// the other row structs it is this package's own and never a payroll.Payslip.
type ESIRow struct {
	// IPNumber is the 10-digit insured person number.
	IPNumber string
	// IPName is the name as registered with ESIC.
	IPName string
	// DaysWagesPaid is the number of days wages were paid for. It is a decimal
	// because a half-day exists in a real attendance register; it is emitted as
	// a WHOLE number ROUNDED UP, because a person who worked any part of a day
	// is insured for that day and rounding down would shorten their
	// contribution record. The rounding direction is in the employee's favour by
	// construction.
	DaysWagesPaid decimal.Decimal
	// TotalMonthlyWages must be whole rupees: see [ErrFractionalWages].
	TotalMonthlyWages decimal.Decimal
	// Reason is blank, or one of the ESIReason* constants.
	Reason string
	// LastWorkingDay is the zero time when there is none.
	LastWorkingDay time.Time
}

// WriteESIReturn writes the monthly contribution return as CSV.
func WriteESIReturn(w io.Writer, rows []ESIRow) error {
	if len(rows) == 0 {
		return errors.New("payrollfile: ESI return with no rows would file a nil contribution for the month; refuse rather than emit a header-only file")
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(esiHeaders); err != nil {
		return fmt.Errorf("payrollfile: write ESI headers: %w", err)
	}
	for i, r := range rows {
		rec, err := esiRecord(r)
		if err != nil {
			return fmt.Errorf("payrollfile: ESI row %d: %w", i+1, err)
		}
		if err := cw.Write(rec); err != nil {
			return fmt.Errorf("payrollfile: write ESI row %d: %w", i+1, err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("payrollfile: flush ESI return: %w", err)
	}
	return nil
}

// esiRecord validates and renders one row.
func esiRecord(r ESIRow) ([]string, error) {
	if !isDigits(r.IPNumber, 10) {
		return nil, fmt.Errorf("%w: got %d characters", ErrInvalidIPNumber, len(r.IPNumber))
	}
	name := strings.TrimSpace(r.IPName)
	if name == "" {
		return nil, errors.New("payrollfile: ESI IP name is blank")
	}
	// CSV quotes commas and quotes for us, so only line breaks are refused
	// (a quoted newline is legal CSV but breaks a paste into the template).
	if err := rejectDelimiters(name, ""); err != nil {
		return nil, err
	}

	if r.DaysWagesPaid.IsNegative() {
		return nil, fmt.Errorf("%w: got %s", ErrInvalidESIDays, r.DaysWagesPaid.String())
	}
	// FRACTIONS ROUND UP: Ceil, never Round. A half day worked is a day insured.
	days := r.DaysWagesPaid.Ceil()
	if days.GreaterThan(decimal.NewFromInt(31)) {
		return nil, fmt.Errorf("%w: got %s", ErrInvalidESIDays, days.String())
	}

	if r.TotalMonthlyWages.IsNegative() {
		return nil, fmt.Errorf("%w: monthly wages is %s", ErrNegativeAmount, r.TotalMonthlyWages.String())
	}
	if !r.TotalMonthlyWages.Equal(r.TotalMonthlyWages.Truncate(0)) {
		return nil, fmt.Errorf("%w: got %s", ErrFractionalWages, r.TotalMonthlyWages.String())
	}

	reason := strings.TrimSpace(r.Reason)
	if reason != "" && !esiReasons[reason] {
		return nil, fmt.Errorf("%w: got %q", ErrUnknownESIReason, reason)
	}
	if days.IsZero() && reason == "" {
		return nil, ErrZeroWagesNeedsReason
	}
	if !r.LastWorkingDay.IsZero() && reason == "" {
		return nil, ErrLastWorkingDayWithoutReason
	}
	if esiReasonNeedsLastWorkingDay[reason] && r.LastWorkingDay.IsZero() {
		return nil, fmt.Errorf("%w: %q", ErrMissingLastWorkingDay, reason)
	}

	lwd := ""
	if !r.LastWorkingDay.IsZero() {
		lwd = r.LastWorkingDay.Format("02/01/2006")
	}
	return []string{
		r.IPNumber,
		name,
		days.String(),
		r.TotalMonthlyWages.Truncate(0).String(),
		reason,
		lwd,
	}, nil
}
