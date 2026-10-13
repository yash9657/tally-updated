package payrolldb

// export.go — the payrolldb -> payrollfile ROW MAPPERS, and the only place in
// this codebase where a statutory payroll FILE is actually produced.
//
// ===========================================================================
// WHY THIS FILE EXISTS AT ALL
// ===========================================================================
// internal/payroll/payrollfile shipped three emitters — the EPFO ECR, the
// Form 138 (ex-24Q) Annexure I and the ESIC monthly contribution return — and
// until this file was written NOTHING CALLED ANY OF THEM. A library with no
// caller is not a feature: the product could not emit a single statutory
// payroll file. These mappers are the caller.
//
// They live HERE and not in internal/server for the reason every other read
// model does: the mapping is where the wrong-number bugs are (a UAN silently
// blank, an EPS wage collapsed into the EPF wage, a leaver filed with no last
// working day), and a mapping reachable only through an HTTP handler can only
// be tested through an HTTP handler.
//
// ===========================================================================
// THE RULE THIS FILE OBEYS: NEVER A SILENT BLANK
// ===========================================================================
// A field the mapper cannot populate is an ERROR or an EXPLICIT DISCLOSURE. It
// is never an empty string, and never a plausible substitute.
//
//   - A missing UAN is [ErrMissingStatutoryIdentifier], not a blank field. Under
//     ECR 2.0 the UAN is mandatory; a blank one credits the contribution to
//     nobody and the upload is rejected — but the employer only finds out at the
//     portal, after the challan window.
//   - A missing PAN is an ERROR and is NOT substituted with the reserved
//     PANNOTAVBL token, even though payrollfile accepts that token. The
//     substitution is not cosmetic: a deductee filed as PAN-not-available is
//     assessed at the s.206AA rate, so quietly writing it would change the tax
//     treatment of a real person to make our file emit.
//   - A leaver's ESI reason cannot be derived from the master, because the
//     master records a date of leaving and NOT why. "Retired", "Expired" and
//     "Employee has left service" are three different filings and guessing
//     between them mis-states somebody's benefit record, so the caller must
//     supply it: see [ESIOptions] and [ErrLeavingReasonRequired].
//   - The Form 138 DD record's tax / surcharge / cess SPLIT is not recoverable
//     from a payslip (the payslip stores one `tds` figure) and is therefore a
//     DISCLOSED approximation, not an error: see [DisclosureForm138TaxSplit].
//     The TOTAL — which is what TRACES credits to the employee and what must
//     reconcile against the challan — is exact.
//
// ===========================================================================
// ONLY A POSTED RUN IS FILEABLE
// ===========================================================================
// Every mapper here reads the POSTED run for the month and refuses a DRAFT with
// [ErrPayRunNotPosted]. The register and the payslip deliberately read any
// non-REVERSED run, because reviewing a draft before posting it is the point of
// a draft — but a statutory file is a FILING. Emitting one from figures that
// were never posted produces a return the books cannot support.
//
// ===========================================================================
// NOT VALIDATOR-VERIFIED
// ===========================================================================
// Nothing this file produces has been checked by the NSDL/Protean FVU, the EPFO
// Unified Portal or the ESIC employer portal: none of them can run in this
// environment. See payrollfile's package doc. [WriteSidecar] is what carries
// that disclosure — plus this file's own mapping disclosures — alongside every
// emitted file, and every emitter below is paired with it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/payroll/payrollfile"
)

// ---------------------------------------------------------------------------
// Sentinels. Each is a REFUSAL to file something wrong, and the HTTP layer maps
// the whole class to 422 with the message intact.
// ---------------------------------------------------------------------------

var (
	// ErrPayRunNotPosted is returned when the month's run exists but has not
	// been posted. A statutory file is a filing; a draft is a review.
	ErrPayRunNotPosted = errors.New("payrolldb: the pay run for this wage month is not POSTED; a statutory file may only be emitted from a posted run (a draft's figures are not in the books)")

	// ErrMissingStatutoryIdentifier is returned when an employee in the run has
	// no UAN (ECR), no PAN (Form 138) or no ESIC IP number (ESI return).
	//
	// It names the employee CODE and never the missing identifier's neighbours:
	// error strings reach HTTP responses and operator logs, and a PAN or a UAN
	// interpolated into one has left the payroll boundary.
	ErrMissingStatutoryIdentifier = errors.New("payrolldb: an employee in this run has no statutory identifier for the file being emitted")

	// ErrFractionalNCPDays is returned when an employee's loss-of-pay days are
	// not a whole number.
	//
	// The ECR's NCP column is whole days and this mapper deliberately does NOT
	// round it. Rounding up over-states the non-contributory period and shortens
	// the member's contribution record; rounding down under-states it and the
	// portal's challan will not agree with the wages. Either way the employee
	// carries the error for the rest of their service, so the caller records
	// whole LOP days or the file is refused.
	ErrFractionalNCPDays = errors.New("payrolldb: loss-of-pay days are fractional and the ECR's NCP column is whole days; this mapper will not round somebody's non-contributory period")

	// ErrLeavingReasonRequired is returned when an employee's date of leaving
	// falls in the wage month (or their paid days are zero) and the caller
	// supplied no reason from the ESIC closed vocabulary. See [ESIOptions].
	ErrLeavingReasonRequired = errors.New("payrolldb: this employee left during the wage month (or was paid for no days) and the ESI return needs a reason from the ESIC closed vocabulary; the employee master records a date of leaving but not why, and guessing between 'Retired', 'Expired' and 'Employee has left service' mis-states a benefit record")

	// ErrNoFileableRows is returned when a month (or a quarter) yields no row the
	// file can carry. It is distinct from "no pay run": a run that exists but
	// produced nothing fileable is a different fault in the caller.
	ErrNoFileableRows = errors.New("payrolldb: nothing to file for this period")

	// ErrForm138Input is returned for a Form 138 request whose deductor or
	// challan particulars are missing or inconsistent. None of them is in the
	// database — the company master carries no TAN and no challan — so they are
	// the caller's to supply, and an absent one is refused rather than blanked.
	ErrForm138Input = errors.New("payrolldb: Form 138 deductor/challan particulars are missing or inconsistent")
)

// ---------------------------------------------------------------------------
// The mapping disclosures. They travel with every file through [WriteSidecar].
// ---------------------------------------------------------------------------

const (
	// DisclosureECRRefund records that the ECR's "Refund of Advances" column is
	// STRUCTURALLY zero here, not unknown: this system records no provident-fund
	// advance and therefore no refund of one.
	DisclosureECRRefund = "PF ECR field 11 (Refund of Advances) is always 0. This system records no " +
		"provident-fund advances, so the zero is structural and not a missing value. If your " +
		"establishment refunded an advance this month, this file understates it."

	// DisclosureECRNCP records where the NCP days come from.
	DisclosureECRNCP = "PF ECR field 10 (NCP Days) is the payslip's recorded loss-of-pay days. " +
		"A fractional loss-of-pay day is REFUSED rather than rounded."

	// DisclosureESIWageRounding records the ONE rounding this file applies, and
	// its authority.
	DisclosureESIWageRounding = "ESI 'Total Monthly Wages' is rounded UP to whole rupees, which is the " +
		"direction the ESI rounding rule takes (internal/payroll/rounding.go, RoundUpRupee — the same " +
		"rule the engine already applies to the contributions). The ESIC column is integer-only, so " +
		"some rounding is unavoidable; rounding up is the direction that cannot shorten cover."

	// DisclosureESIReason records that the reason came from the caller.
	DisclosureESIReason = "ESI leaving reasons are supplied by the operator, not derived: the employee " +
		"master records a date of leaving and not why. A leaver with no supplied reason is refused."

	// DisclosureForm138TaxSplit is the most consequential disclosure in this
	// file. The payslip stores ONE tds figure; the statement has three columns.
	DisclosureForm138TaxSplit = "Form 138 Annexure I: the deductee's TAX / SURCHARGE / CESS split is NOT " +
		"recoverable from a payslip, which stores a single monthly deduction. The WHOLE monthly deduction " +
		"is reported in the TAX column and surcharge and cess are reported as 0. The TOTAL tax deducted " +
		"and deposited -- the figure TRACES credits to the employee and the figure that must reconcile " +
		"against the challan -- is EXACT. The split is not. Re-derive it before filing if any employee " +
		"is in a surcharge band."

	// DisclosureForm138Dates records where the payment/deduction dates come from.
	DisclosureForm138Dates = "Form 138 dates of payment and deduction are the LAST DAY OF THE WAGE MONTH, " +
		"which is the date the pay run posts its voucher on (internal/payroll/payrolldb/payrun.go). " +
		"If your establishment pays on a different day, these dates are wrong."

	// DisclosureForm138Deductor records that the deductor block is thin.
	DisclosureForm138Deductor = "Form 138: the deductor ADDRESS block and the RESPONSIBLE-PERSON block are " +
		"not emitted at all -- their field positions could not be verified. Records are NOT padded out " +
		"with invented values. This file is NOT claimed to load in the FVU."
)

// WriteSidecar writes the companion README for a generated statutory file: the
// payrollfile disclosure verbatim, then this package's MAPPING disclosures.
//
// It exists because none of the three formats permits an in-file comment (see
// payrollfile's package doc), and because payrollfile's own Disclosure cannot
// know what the MAPPER approximated. Every emitter in this file is paired with
// it; the HTTP layer ships the two together so a downloaded file can never
// arrive without the statement of what is not verified about it.
func WriteSidecar(w io.Writer, generatedFileName string, notes ...string) error {
	if err := payrollfile.WriteDisclosureSidecar(w, generatedFileName); err != nil {
		return err
	}
	if len(notes) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("\nMAPPING DISCLOSURES (how this system filled the file's fields):\n")
	for _, n := range notes {
		b.WriteString("  * ")
		b.WriteString(n)
		b.WriteString("\n")
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("payrolldb: write mapping disclosures: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// The one row shape every mapper reads.
// ---------------------------------------------------------------------------

// statutoryRow is one employee's posted payslip for one wage month, joined to
// the master identifiers the statutory FILES need.
//
// It is separate from RegisterRow on purpose. RegisterRow is what a SCREEN may
// show and deliberately carries no PAN, no UAN and no IP number; this struct
// carries all three because a file cannot be produced without them. Keeping
// them apart is what stops a report handler from accidentally rendering a
// screenful of PANs.
type statutoryRow struct {
	EmployeeID   uuid.UUID
	Code         string
	Name         string
	PAN          string
	UAN          string
	IPNumber     string
	DOL          string // ISO date; empty = still employed
	DaysPaid     decimal.Decimal
	LOPDays      decimal.Decimal
	Gross        decimal.Decimal
	EPFWages     decimal.Decimal
	EPSWages     decimal.Decimal
	EDLIWages    decimal.Decimal
	ESIWages     decimal.Decimal
	EPFEmployee  decimal.Decimal
	EPFEmployer  decimal.Decimal
	EPSEmployer  decimal.Decimal
	ESIEmployee  decimal.Decimal
	ESIEmployer  decimal.Decimal
	TDS          decimal.Decimal
	PayRunID     uuid.UUID
	PayRunStatus string
}

// statutoryRows reads the POSTED run for a wage month, ordered by employee code.
//
// Every read goes through RunInTx, so RLS confines it to the caller's company:
// there is no company parameter here and no way to ask for another tenant's
// payroll. A month with a run that is not POSTED is [ErrPayRunNotPosted]; a
// month with no live run at all is [ErrPayRunNotFound].
func (s *Service) statutoryRows(ctx context.Context, m payroll.WageMonth) ([]statutoryRow, error) {
	var out []statutoryRow
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var runID uuid.UUID
		var status string
		switch err := tx.QueryRow(ctx,
			`SELECT id, status FROM pay_run
			  WHERE wage_month = $1::date AND status <> $2`,
			m.First().Format(isoDate), StatusReversed).Scan(&runID, &status); {
		case err == pgx.ErrNoRows:
			return fmt.Errorf("%w for wage month %s — no payroll has been run for it yet", ErrPayRunNotFound, m)
		case err != nil:
			return err
		}
		if status != StatusPosted {
			return fmt.Errorf("%w: the %s run is %s", ErrPayRunNotPosted, m, status)
		}
		rows, err := tx.Query(ctx,
			`SELECT p.employee_id, e.code, e.name,
			        COALESCE(e.pan, ''), COALESCE(e.uan, ''), COALESCE(e.esic_ip_number, ''),
			        COALESCE(e.dol::text, ''),
			        p.days_paid, p.lop_days, p.gross_earnings,
			        p.epf_wages, p.eps_wages, p.edli_wages, p.esi_wages,
			        p.epf_employee_statutory + p.epf_employee_voluntary,
			        p.epf_employer_statutory + p.epf_employer_voluntary,
			        p.eps_employer, p.esi_employee, p.esi_employer, p.tds
			   FROM payslip p
			   JOIN employee e ON e.company_id = p.company_id AND e.id = p.employee_id
			  WHERE p.pay_run_id = $1
			  ORDER BY e.code`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r := statutoryRow{PayRunID: runID, PayRunStatus: status}
			if err := rows.Scan(&r.EmployeeID, &r.Code, &r.Name,
				&r.PAN, &r.UAN, &r.IPNumber, &r.DOL,
				&r.DaysPaid, &r.LOPDays, &r.Gross,
				&r.EPFWages, &r.EPSWages, &r.EDLIWages, &r.ESIWages,
				&r.EPFEmployee, &r.EPFEmployer, &r.EPSEmployer,
				&r.ESIEmployee, &r.ESIEmployer, &r.TDS); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		// Returned bare for the reason report.go gives at the same spot: the
		// sentinel already names the month and the missing run.
		if errors.Is(err, ErrPayRunNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("statutory rows for %s: %w", m, err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// PF ECR.
// ---------------------------------------------------------------------------

// ECRRows maps a wage month's posted payslips to EPFO ECR member rows.
//
// An employee with NO provident-fund figures at all (no EPF wages and no
// contribution on either side) is EXCLUDED rather than filed with zeros: they
// are not a member for the month, and a zero-wage member line is a different
// statement from an absent one. An employee who IS a member and has no UAN is
// [ErrMissingStatutoryIdentifier] — never a blank field.
func (s *Service) ECRRows(ctx context.Context, m payroll.WageMonth) ([]payrollfile.ECRRow, error) {
	rows, err := s.statutoryRows(ctx, m)
	if err != nil {
		return nil, err
	}
	out := make([]payrollfile.ECRRow, 0, len(rows))
	for _, r := range rows {
		if r.EPFWages.IsZero() && r.EPFEmployee.IsZero() && r.EPFEmployer.IsZero() && r.EPSEmployer.IsZero() {
			continue // not a provident-fund member this month
		}
		if strings.TrimSpace(r.UAN) == "" {
			return nil, fmt.Errorf("%w: employee %s has no UAN and the ECR requires one for every member (ECR 2.0); "+
				"record the UAN on the employee master, or exclude the employee from the run", ErrMissingStatutoryIdentifier, r.Code)
		}
		if !r.LOPDays.IsInteger() {
			return nil, fmt.Errorf("%w: employee %s has %s loss-of-pay days", ErrFractionalNCPDays, r.Code, r.LOPDays.String())
		}
		out = append(out, payrollfile.ECRRow{
			UAN:        r.UAN,
			MemberName: r.Name,
			GrossWages: r.Gross,
			// THREE DIFFERENT NUMBERS, carried across unchanged. Collapsing any
			// pair of them mis-states the pension liability for the rest of that
			// member's working life; see payrollfile/ecr.go.
			EPFWages:                r.EPFWages,
			EPSWages:                r.EPSWages,
			EDLIWages:               r.EDLIWages,
			EmployeeEPFContribution: r.EPFEmployee,
			// THE ONE ARITHMETIC STEP IN THIS MAPPER, AND THE EASIEST TO GET
			// BACKWARDS. The payslip stores epf_employer_* NET OF EPS — the
			// pension share is already carved out of the employer's 12% before it
			// is written (payrun.go; the fixture in compute_test.go derives
			// 1,680 - 1,166 = 514). payrollfile.ECRRow wants the employer's TOTAL
			// share, because it computes field 9 ("EPF & EPS Diff Remitted") as
			// total MINUS EPS and the portal recomputes the employer share as
			// field 8 + field 9.
			//
			// Passing the net figure straight through does NOT silently
			// under-report: it makes EPS exceed the employer share and
			// payrollfile refuses the file with ErrEPSExceedsEmployerEPF. Adding
			// EPS back is what makes field 9 equal the net share the books hold.
			EmployerEPFContribution: r.EPFEmployer.Add(r.EPSEmployer),
			EPSContribution:         r.EPSEmployer,
			NCPDays:                 int(r.LOPDays.IntPart()),
			// Structurally zero: see DisclosureECRRefund.
			RefundOfAdvances: decimal.Zero,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no employee in the %s run has any provident-fund figure, so there is no ECR to file", ErrNoFileableRows, m)
	}
	return out, nil
}

// WriteECR writes the wage month's ECR text file. The caller writes the sidecar
// with [WriteSidecar] and [ECRDisclosures].
func (s *Service) WriteECR(ctx context.Context, w io.Writer, m payroll.WageMonth) error {
	rows, err := s.ECRRows(ctx, m)
	if err != nil {
		return err
	}
	return payrollfile.WriteECR(w, rows)
}

// ECRDisclosures is what [WriteSidecar] must carry beside an ECR.
func ECRDisclosures() []string { return []string{DisclosureECRNCP, DisclosureECRRefund} }

// ECRFileName is the EPFO convention as far as it can be honoured here.
//
// The real convention embeds the ESTABLISHMENT CODE, which this schema does not
// store — the Unified Portal derives the establishment from the login, so the
// code is absent from the name rather than invented into it.
func ECRFileName(m payroll.WageMonth) string {
	return fmt.Sprintf("ECR_%s.txt", m.First().Format("012006"))
}

// ---------------------------------------------------------------------------
// ESI monthly contribution return.
// ---------------------------------------------------------------------------

// ESIOptions carries the ONE fact the database cannot supply: why an insured
// person's wages stopped.
//
// LeavingReasons is keyed by EMPLOYEE CODE (what the operator sees and types,
// and what every error in this file names) and its values must be one of the
// payrollfile.ESIReason* constants. An employee who left during the month, or
// who was paid for no days, with no entry here is [ErrLeavingReasonRequired].
type ESIOptions struct {
	LeavingReasons map[string]string
}

// ESIRows maps a wage month's posted payslips to ESIC contribution rows.
//
// An employee with no ESI figure at all is EXCLUDED: they are not an insured
// person for the month. An included employee with no IP number is
// [ErrMissingStatutoryIdentifier].
func (s *Service) ESIRows(ctx context.Context, m payroll.WageMonth, opts ESIOptions) ([]payrollfile.ESIRow, error) {
	rows, err := s.statutoryRows(ctx, m)
	if err != nil {
		return nil, err
	}
	out := make([]payrollfile.ESIRow, 0, len(rows))
	for _, r := range rows {
		if r.ESIWages.IsZero() && r.ESIEmployee.IsZero() && r.ESIEmployer.IsZero() {
			continue // not covered this month
		}
		if strings.TrimSpace(r.IPNumber) == "" {
			return nil, fmt.Errorf("%w: employee %s has no ESIC IP number and the return requires one for every insured person; "+
				"record it on the employee master, or exclude the employee from ESI cover", ErrMissingStatutoryIdentifier, r.Code)
		}

		// The last working day, and ONLY when it falls inside the wage month:
		// an employee who left in an earlier month is not on this return at all,
		// and one who leaves in a later month has not left yet.
		var lastWorkingDay time.Time
		if r.DOL != "" {
			d, perr := time.Parse(isoDate, r.DOL)
			if perr != nil {
				return nil, fmt.Errorf("payrolldb: employee %s has an unparseable date of leaving: %w", r.Code, perr)
			}
			if !d.Before(m.First()) && !d.After(m.Last()) {
				lastWorkingDay = d
			}
		}
		reason := strings.TrimSpace(opts.LeavingReasons[r.Code])
		needsReason := !lastWorkingDay.IsZero() || r.DaysPaid.IsZero()
		if needsReason && reason == "" {
			return nil, fmt.Errorf("%w: employee %s", ErrLeavingReasonRequired, r.Code)
		}
		if !needsReason && reason != "" {
			// Refused rather than ignored: a reason supplied against an employee
			// who did not leave is a keying error against the WRONG person, and
			// silently dropping it means the operator believes they filed a
			// leaver they did not.
			return nil, fmt.Errorf("%w: a leaving reason was supplied for employee %s, who neither left during %s nor was paid for zero days",
				ErrLeavingReasonRequired, r.Code, m)
		}

		out = append(out, payrollfile.ESIRow{
			IPNumber:      r.IPNumber,
			IPName:        r.Name,
			DaysWagesPaid: r.DaysPaid,
			// The ONE rounding this file applies, in the employee's favour and
			// under the ESI rule's own direction. See DisclosureESIWageRounding.
			TotalMonthlyWages: payroll.RoundUpRupee(r.ESIWages),
			Reason:            reason,
			LastWorkingDay:    lastWorkingDay,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no employee in the %s run is ESI-covered, so there is no contribution return to file", ErrNoFileableRows, m)
	}
	return out, nil
}

// WriteESIReturn writes the wage month's ESIC contribution return as CSV.
func (s *Service) WriteESIReturn(ctx context.Context, w io.Writer, m payroll.WageMonth, opts ESIOptions) error {
	rows, err := s.ESIRows(ctx, m, opts)
	if err != nil {
		return err
	}
	return payrollfile.WriteESIReturn(w, rows)
}

// ESIDisclosures is what [WriteSidecar] must carry beside an ESI return.
func ESIDisclosures() []string {
	return []string{DisclosureESIWageRounding, DisclosureESIReason}
}

// ESIFileName names the monthly contribution return.
func ESIFileName(m payroll.WageMonth) string {
	return fmt.Sprintf("ESI_Monthly_Contribution_%s.csv", m.First().Format("012006"))
}

// ---------------------------------------------------------------------------
// Form 138 (ex-24Q) Annexure I.
// ---------------------------------------------------------------------------

// Form138ChallanInput is ONE month's challan particulars.
//
// None of it is in the database. The company master carries no TAN, no BSR code
// and no challan serial, so every field here is the operator's to supply and an
// absent one is [ErrForm138Input] rather than a blank in a filed record.
type Form138ChallanInput struct {
	// Month is the wage month whose deduction this challan carries. It must lie
	// inside the requested quarter.
	Month payroll.WageMonth
	// BSRCode is the 7-digit bank branch code, as CHARACTERS (real codes begin
	// with 0 and stripping it makes the challan unmatchable at TRACES).
	BSRCode string
	// DateOfDeposit is the tender date.
	DateOfDeposit time.Time
	// ChallanSerialNumber is the bank's serial for the day.
	ChallanSerialNumber int
	// BookEntry is true for a government deductor's transfer voucher.
	BookEntry bool
	// TotalTDSDeposited is what was actually deposited. ZERO means "derive it
	// from the month's payslips" — which is what the books say was deducted. A
	// supplied figure that does not equal the deductee sum is refused by
	// payrollfile (ErrChallanTotalMismatch), which is the reconciliation the
	// whole statement exists to assert.
	TotalTDSDeposited decimal.Decimal
	// TotalInterest and TotalFee (s.234E) are deposited with the same challan
	// and are credited to no employee.
	TotalInterest decimal.Decimal
	TotalFee      decimal.Decimal
}

// Form138Input is a whole Annexure I request.
type Form138Input struct {
	TaxYear payroll.TaxYear
	// Quarter is "Q1", "Q2" or "Q3". Q4 is refused by payrollfile because the
	// Q4 / Annexure II record format is not published.
	Quarter string
	// The deductor block.
	TAN          string
	DeductorPAN  string
	DeductorName string
	// FormNumber is "138" or "24Q". There is deliberately no default: which one
	// the receiving system expects is exactly the kind of thing this track could
	// not verify, so the caller states it.
	FormNumber string
	// FileCreationDate stamps the file header; zero is refused rather than
	// silently filled with time.Now(), because a filing's creation date is a
	// fact about the filing and not about when a report was rendered.
	FileCreationDate   time.Time
	FileSequenceNumber int
	ProducerVersion    string
	// Challans is one per wage month that carries a deduction.
	Challans []Form138ChallanInput
}

// quarterMonths returns the three wage months of a quarter within a tax year.
//
// Q1 is April-June of the tax year's START year, and so on. Q4 exists in the
// table so an out-of-range quarter and an unbuilt one are different errors —
// payrollfile refuses Q4 with its own sentinel and says why.
func quarterMonths(ty payroll.TaxYear, quarter string) ([]payroll.WageMonth, error) {
	first := map[string]time.Month{
		"Q1": time.April, "Q2": time.July, "Q3": time.October, "Q4": time.January,
	}[strings.ToUpper(strings.TrimSpace(quarter))]
	if first == 0 {
		return nil, fmt.Errorf("%w: quarter %q is not Q1, Q2, Q3 or Q4", ErrForm138Input, quarter)
	}
	year := ty.StartYear
	if first == time.January {
		year = ty.StartYear + 1
	}
	out := make([]payroll.WageMonth, 0, 3)
	for i := 0; i < 3; i++ {
		mm := payroll.WageMonth{Year: year, Month: first + time.Month(i)}
		out = append(out, mm)
	}
	return out, nil
}

// Form138AnnexureI maps a quarter's posted payslips to a Form 138 Annexure I
// statement.
//
// Only employees with a POSITIVE deduction appear: Annexure I is the statement
// of tax DEDUCTED, and a deductee record for somebody from whom nothing was
// deducted is a record the department cannot reconcile. An included employee
// with no PAN is [ErrMissingStatutoryIdentifier] — the reserved PANNOTAVBL token
// is NOT substituted, because it changes the assessment.
func (s *Service) Form138AnnexureI(ctx context.Context, in Form138Input) (payrollfile.Form138File, error) {
	var zero payrollfile.Form138File
	months, err := quarterMonths(in.TaxYear, in.Quarter)
	if err != nil {
		return zero, err
	}
	inQuarter := map[string]bool{}
	for _, m := range months {
		inQuarter[m.String()] = true
	}
	if in.FileCreationDate.IsZero() {
		return zero, fmt.Errorf("%w: file creation date is required (it is a fact about the filing; it is not defaulted to now)", ErrForm138Input)
	}
	if strings.TrimSpace(in.FormNumber) == "" {
		return zero, fmt.Errorf("%w: form number is required and must be \"138\" or \"24Q\"; which one the receiving system expects could not be verified here, so it is not defaulted", ErrForm138Input)
	}
	if len(in.Challans) == 0 {
		return zero, fmt.Errorf("%w: at least one challan is required; a statement with no challan credits nobody", ErrForm138Input)
	}

	file := payrollfile.Form138File{
		Header: payrollfile.Form138Header{
			TAN:                strings.TrimSpace(in.TAN),
			DeductorPAN:        strings.TrimSpace(in.DeductorPAN),
			DeductorName:       strings.TrimSpace(in.DeductorName),
			FinancialYear:      fmt.Sprintf("%04d%02d", in.TaxYear.StartYear, (in.TaxYear.StartYear+1)%100),
			Quarter:            strings.ToUpper(strings.TrimSpace(in.Quarter)),
			FormNumber:         strings.TrimSpace(in.FormNumber),
			FileCreationDate:   in.FileCreationDate,
			FileSequenceNumber: in.FileSequenceNumber,
			ProducerVersion:    in.ProducerVersion,
		},
	}

	serial := 0
	for _, ci := range in.Challans {
		if !inQuarter[ci.Month.String()] {
			return zero, fmt.Errorf("%w: challan names wage month %s, which is not in %s of tax year %s",
				ErrForm138Input, ci.Month, strings.ToUpper(in.Quarter), in.TaxYear.Start().Format("2006"))
		}
		rows, rerr := s.statutoryRows(ctx, ci.Month)
		if rerr != nil {
			return zero, rerr
		}
		// The date of payment and of deduction. See DisclosureForm138Dates: the
		// pay run posts its voucher on the last day of the wage month, so that is
		// the date the books record — not a guess.
		paidOn := ci.Month.Last()

		ch := payrollfile.Form138Challan{
			BSRCode:             strings.TrimSpace(ci.BSRCode),
			DateOfDeposit:       ci.DateOfDeposit,
			ChallanSerialNumber: ci.ChallanSerialNumber,
			BookEntry:           ci.BookEntry,
			TotalInterest:       ci.TotalInterest,
			TotalFee:            ci.TotalFee,
		}
		deducted := decimal.Zero
		for _, r := range rows {
			if !r.TDS.IsPositive() {
				continue
			}
			if strings.TrimSpace(r.PAN) == "" {
				return zero, fmt.Errorf("%w: employee %s had tax deducted in %s but has no PAN. "+
					"The reserved PANNOTAVBL token is deliberately NOT substituted: a deductee filed as "+
					"PAN-not-available is assessed at the s.206AA rate, so writing it would change a real "+
					"person's tax treatment to make this file emit", ErrMissingStatutoryIdentifier, r.Code, ci.Month)
			}
			serial++
			deducted = deducted.Add(r.TDS)
			ch.Deductees = append(ch.Deductees, payrollfile.Form138Deductee{
				EmployeeSerialNumber: serial,
				EmployeeReference:    r.Code,
				PAN:                  r.PAN,
				Name:                 r.Name,
				DateOfPayment:        paidOn,
				DateOfDeduction:      paidOn,
				AmountPaid:           r.Gross,
				// THE DISCLOSED APPROXIMATION. The payslip stores ONE tds figure;
				// the statement has three columns. The TOTAL is exact, the split
				// is not. See DisclosureForm138TaxSplit.
				Tax:               r.TDS,
				Surcharge:         decimal.Zero,
				Cess:              decimal.Zero,
				TotalTaxDeducted:  r.TDS,
				TotalTaxDeposited: r.TDS,
			})
		}
		if len(ch.Deductees) == 0 {
			return zero, fmt.Errorf("%w: a challan was supplied for wage month %s but no employee had tax deducted that month; "+
				"a challan with no deductee is a statement the department cannot reconcile", ErrForm138Input, ci.Month)
		}
		ch.TotalTDSDeposited = ci.TotalTDSDeposited
		if ch.TotalTDSDeposited.IsZero() {
			ch.TotalTDSDeposited = deducted
		}
		file.Challans = append(file.Challans, ch)
	}
	return file, nil
}

// WriteForm138AnnexureI writes the quarter's Annexure I statement.
func (s *Service) WriteForm138AnnexureI(ctx context.Context, w io.Writer, in Form138Input) error {
	f, err := s.Form138AnnexureI(ctx, in)
	if err != nil {
		return err
	}
	return payrollfile.WriteForm138AnnexureI(w, f)
}

// Form138Disclosures is what [WriteSidecar] must carry beside a Form 138 file.
func Form138Disclosures() []string {
	return []string{DisclosureForm138TaxSplit, DisclosureForm138Dates, DisclosureForm138Deductor}
}

// Form138FileName follows the FVU naming convention as far as it is known:
// <TAN>_<form>_<quarter>_<FY>.txt.
func Form138FileName(in Form138Input) string {
	return fmt.Sprintf("%s_%s_%s_%04d%02d.txt",
		strings.TrimSpace(in.TAN), strings.TrimSpace(in.FormNumber),
		strings.ToUpper(strings.TrimSpace(in.Quarter)),
		in.TaxYear.StartYear, (in.TaxYear.StartYear+1)%100)
}
