package reporting

// SOURCING A FORM 140 RETURN FROM THE BOOKS.
//
// internal/compliance/form140 knows the prescribed layout and nothing about
// storage. This file is the other half: it reads migrations 0069 (the challan
// register), 0070 (the deductor return profile) and 0050 (the deductions), and
// assembles a form140.Statement — or REFUSES, naming the gap.
//
// ===========================================================================
// EVERY REFUSAL HERE IS ABOUT THIS COMPANY'S BOOKS, NOT ABOUT THE SOFTWARE
// ===========================================================================
// That is the whole change in kind from what this package used to say. The old
// refusal was "we have not read the layout". Every refusal below is instead
// "you have not recorded X", with X named and actionable. Each one is a
// sentinel error so a caller can tell them apart without matching on prose.
//
// ===========================================================================
// ⚠️ THE REASON FLAG IS DERIVED HERE, AND THAT IS A DISCLOSED APPROXIMATION
// ===========================================================================
// Annexure 6 has thirteen flags. tds_deduction stores NONE of them — it stores
// the facts a flag is derived from. This build can honestly derive exactly two:
//
//	Y  the payment was below the threshold          (tds_amount_rupees = 0)
//	C  deducted at the higher no-PAN rate, s.397(2) (pan_available = false)
//
// The other eleven all rest on a DOCUMENT nobody has recorded — a s.395
// certificate (A), a s.393(6) declaration (B), a Board circular (E), a
// transporter's declaration (T), and so on. Deriving one of those from the
// numbers would be asserting that a document exists, which is the one thing a
// tax return must never do on its own initiative. So they are not derived, and
// a deduction that needs one cannot be filed until the document is recorded —
// which is a schema gap this file names rather than papers over.
//
// See also form140.ErrNoValidReasonFlag: a sub-threshold RENT payment has no
// legal flag at all, because Annexure 6 does not list Y against 1008 or 1009.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance/form140"
)

// ErrNoDeductorProfile is migration 0070's absent row. A return names the
// deductor on every record; without a TAN there is nothing to file as.
var ErrNoDeductorProfile = errors.New(
	"reporting: this company has no TDS return profile — no TAN, PAN, deductor category or " +
		"responsible person is on file, and a return cannot be filed without them")

// ErrTaxNotDeposited is a deduction with tax on it and no challan behind it.
//
// It is a REFUSAL and not a warning. Deductee Detail field 5 is a challan
// reference, so a deduction with no challan has no row to occupy in the return;
// there is no "file it as unpaid" option in the layout. The remedy is to
// deposit the tax and record the challan, in that order.
var ErrTaxNotDeposited = errors.New(
	"reporting: tax was deducted but no challan is recorded against it, so it cannot be reported")

// ErrAllocationBrokenByAlter is the 0045 mechanism firing.
//
// A challan allocation records WHAT WAS DEPOSITED against a deduction, keyed on
// the voucher identity that survives an alter. When somebody corrects a voucher
// the deduction is re-derived and its amount can move — leaving an allocation
// that no longer matches the deduction it paid. That is not an error in either
// row; it is a real-world discrepancy (money was paid against a figure that has
// since changed) and it must be SEEN, never silently used to build a return.
var ErrAllocationBrokenByAlter = errors.New(
	"reporting: a recorded deposit no longer matches the deduction it was allocated to — the " +
		"voucher has been altered since the tax was paid")

// ErrNoReasonDocument is a zero-tax or reduced-rate deduction whose Annexure 6
// reason cannot be derived from the facts on file. See the header.
var ErrNoReasonDocument = errors.New(
	"reporting: this payment needs an Annexure 6 reason that cannot be derived from the books")

// Form140Request is one quarter of one company.
type Form140Request struct {
	CompanyID uuid.UUID
	// TaxYearStart is the calendar year the tax year begins in: 2026 = 2026-27.
	TaxYearStart int
	Quarter      form140.Quarter
	// FileCreationDate is stamped into the file header AND is the horizon for
	// the format's no-future-date rule. Passed in rather than read from the
	// clock so that the same books produce the same bytes.
	FileCreationDate time.Time
	// FileSequenceNo must be unique across every file this deductor sends.
	FileSequenceNo int
	// RPUName identifies the preparing software on the file header.
	RPUName string
	// PriorStatementFiled and PriorTokenNo answer Batch Header field 52: has a
	// regular Form 140 been filed for an earlier period, and under what token.
	// Facts about past filings, which this software does not track — so they
	// are supplied rather than inferred, and inferring "N" would be a false
	// statement for any company that has ever filed.
	PriorStatementFiled bool
	PriorTokenNo        string
}

// Form140StatementFor assembles the statement for one quarter, or refuses.
//
// It is READ-ONLY and runs in one transaction, so the deductions, the challans
// and the allocations are all read at the same instant — a return assembled
// from three separate reads could straddle a voucher alter and reconcile to
// nothing.
func (s *Service) Form140StatementFor(ctx context.Context, req Form140Request) (form140.Statement, error) {
	qStart, qEnd, ok := quarterBounds(req.Quarter, req.TaxYearStart)
	if !ok {
		return form140.Statement{}, fmt.Errorf("reporting: %q is not Q1, Q2, Q3 or Q4", req.Quarter)
	}

	st := form140.Statement{
		FileCreationDate:    req.FileCreationDate,
		FileSequenceNo:      req.FileSequenceNo,
		RPUName:             req.RPUName,
		TaxYearStart:        req.TaxYearStart,
		Period:              req.Quarter,
		PriorStatementFiled: req.PriorStatementFiled,
		PriorTokenNo:        req.PriorTokenNo,
	}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		d, err := readDeductorProfile(ctx, tx, req.CompanyID)
		if err != nil {
			return err
		}
		st.Deductor = d

		rows, err := readQuarterDeductions(ctx, tx, req.CompanyID, qStart, qEnd)
		if err != nil {
			return err
		}
		challans, err := groupByChallan(ctx, tx, req.CompanyID, rows, qEnd)
		if err != nil {
			return err
		}
		st.Challans = challans
		return nil
	})
	if err != nil {
		return form140.Statement{}, err
	}
	return st, nil
}

// Form140File assembles the statement and renders it.
//
// ⚠️ WHAT THIS RETURNS HAS NOT BEEN VALIDATED. The department's File Validation
// Utility cannot run in this environment; the bytes are built to a read
// specification and must be validated before filing. Every caller that shows
// this to a human says so — see ReturnFileRefusal's ValidationNote.
func (s *Service) Form140File(ctx context.Context, req Form140Request) ([]byte, error) {
	st, err := s.Form140StatementFor(ctx, req)
	if err != nil {
		return nil, err
	}
	return form140.Build(st)
}

// quarterBounds mirrors form140's own, which is unexported there because it is
// an implementation detail of validation. Duplicated deliberately and narrowly:
// exporting it would invite callers to compute quarter arithmetic against the
// emitter rather than against the statute, and the two copies are pinned
// against each other by TestQuarterBoundsAgreeWithTheEmitter.
func quarterBounds(q form140.Quarter, taxYearStart int) (time.Time, time.Time, bool) {
	d := func(y int, m time.Month, day int) time.Time {
		return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	}
	switch q {
	case form140.Q1:
		return d(taxYearStart, time.April, 1), d(taxYearStart, time.June, 30), true
	case form140.Q2:
		return d(taxYearStart, time.July, 1), d(taxYearStart, time.September, 30), true
	case form140.Q3:
		return d(taxYearStart, time.October, 1), d(taxYearStart, time.December, 31), true
	case form140.Q4:
		// Q4 crosses the calendar year: January to March of the following one.
		return d(taxYearStart+1, time.January, 1), d(taxYearStart+1, time.March, 31), true
	}
	return time.Time{}, time.Time{}, false
}

// ---------------------------------------------------------------------------
// The deductor profile
// ---------------------------------------------------------------------------

func readDeductorProfile(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (form140.Deductor, error) {
	var d form140.Deductor
	var r form140.ResponsiblePerson
	err := tx.QueryRow(ctx,
		`SELECT tan, pan, deductor_name, deductor_category,
		        address1, address2, address3, address4, address5, country,
		        state_code, pincode, email, contact_country_code, contact_number,
		        state_name_code, ministry_code, ministry_other, ain, gstin,
		        responsible_name, responsible_designation, responsible_pan,
		        responsible_address1, responsible_address2, responsible_address3,
		        responsible_address4, responsible_address5, responsible_country,
		        responsible_state_code, responsible_pincode, responsible_email,
		        responsible_contact_country_code, responsible_contact_number
		   FROM company_tds_profile
		  WHERE company_id = $1`, companyID).
		Scan(&d.TAN, &d.PAN, &d.Name, &d.Category,
			&d.Address1, &d.Address2, &d.Address3, &d.Address4, &d.Address5, &d.Country,
			&d.StateCode, &d.Pincode, &d.Email, &d.ContactCountryCode, &d.ContactNumber,
			&d.StateName, &d.Ministry, &d.MinistryOther, &d.AIN, &d.GSTIN,
			&r.Name, &r.Designation, &r.PAN,
			&r.Address1, &r.Address2, &r.Address3, &r.Address4, &r.Address5, &r.Country,
			&r.StateCode, &r.Pincode, &r.Email,
			&r.ContactCountryCode, &r.ContactNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		// The absence of a row IS the refusal — migration 0070's header.
		return form140.Deductor{}, ErrNoDeductorProfile
	}
	if err != nil {
		return form140.Deductor{}, fmt.Errorf("reporting: read deductor profile: %w", err)
	}
	d.Responsible = r
	return d, nil
}

// ---------------------------------------------------------------------------
// The deductions, and the challans behind them
// ---------------------------------------------------------------------------

// sourcedDeduction is one tds_deduction row joined to its deductee and to the
// challan (if any) that paid it.
type sourcedDeduction struct {
	voucherID   uuid.UUID
	voucherNo   string
	section     string
	legSeq      int
	returnCode  string
	deducteeNm  string
	deducteePAN string
	panOnRecord bool
	date        time.Time
	base        decimal.Decimal
	rate        decimal.Decimal
	tax         decimal.Decimal

	// From tds_challan_allocation + tds_challan, all NULL when undeposited.
	challanID   *uuid.UUID
	allocated   decimal.Decimal
	bsrCode     string
	challanNo   string
	byBookEntry bool
	depositDate time.Time
	minorHead   string
	chTax       decimal.Decimal
	chInterest  decimal.Decimal
	chFee       decimal.Decimal
	chPenalty   decimal.Decimal
}

func readQuarterDeductions(ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	qStart, qEnd time.Time) ([]sourcedDeduction, error) {
	// A LEFT JOIN, deliberately: an undeposited deduction must come back so it
	// can be refused BY NAME. An inner join would silently drop it and produce
	// a return that is short by exactly the rows somebody forgot to pay.
	rows, err := tx.Query(ctx,
		`SELECT d.voucher_id, COALESCE(vv.voucher_no, ''), d.section, d.leg_seq,
		        d.return_code, l.name, COALESCE(l.pan, ''), d.pan_available,
		        d.deduction_date, d.taxable_base_rupees, d.rate_pct, d.tds_amount_rupees,
		        c.id, COALESCE(a.allocated_amount_rupees, 0),
		        COALESCE(c.bsr_code, ''), COALESCE(c.challan_no, ''),
		        COALESCE(c.by_book_entry, FALSE), c.deposit_date,
		        COALESCE(c.minor_head, ''), COALESCE(c.tax_rupees, 0),
		        COALESCE(c.interest_rupees, 0), COALESCE(c.fee_rupees, 0),
		        COALESCE(c.penalty_rupees, 0)
		   FROM tds_deduction d
		   JOIN ledger l           ON l.company_id  = d.company_id AND l.id = d.deductee_ledger_id
		   JOIN voucher_head vh    ON vh.company_id = d.company_id AND vh.voucher_id = d.voucher_id
		   JOIN voucher_version vv ON vv.id = vh.current_version_id
		   LEFT JOIN tds_challan_allocation a
		          ON a.company_id = d.company_id
		         AND a.voucher_id = d.voucher_id
		         AND a.section    = d.section
		         AND a.leg_seq    = d.leg_seq
		   LEFT JOIN tds_challan c
		          ON c.company_id = a.company_id AND c.id = a.challan_id
		  WHERE d.company_id = $1
		    AND d.deduction_date BETWEEN $2 AND $3
		  ORDER BY c.deposit_date NULLS LAST, c.id NULLS LAST,
		           d.deduction_date, vv.voucher_no, d.leg_seq`,
		companyID, qStart, qEnd)
	if err != nil {
		return nil, fmt.Errorf("reporting: read quarter deductions: %w", err)
	}
	defer rows.Close()

	var out []sourcedDeduction
	for rows.Next() {
		var d sourcedDeduction
		var depositDate *time.Time
		if err := rows.Scan(&d.voucherID, &d.voucherNo, &d.section, &d.legSeq,
			&d.returnCode, &d.deducteeNm, &d.deducteePAN, &d.panOnRecord,
			&d.date, &d.base, &d.rate, &d.tax,
			&d.challanID, &d.allocated, &d.bsrCode, &d.challanNo,
			&d.byBookEntry, &depositDate, &d.minorHead,
			&d.chTax, &d.chInterest, &d.chFee, &d.chPenalty); err != nil {
			return nil, fmt.Errorf("reporting: scan deduction: %w", err)
		}
		if depositDate != nil {
			d.depositDate = *depositDate
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// groupByChallan turns the flat rows into the return's shape: one Challan per
// deposit, plus at most one synthesised nil challan for the zero-tax rows.
func groupByChallan(ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	rows []sourcedDeduction, qEnd time.Time) ([]form140.Challan, error) {
	// Insertion-ordered, because the challan record numbers follow this order
	// and a map's iteration order would make the file non-deterministic —
	// which would in turn make a golden test impossible and a re-emitted return
	// differ from the one that was filed.
	var order []uuid.UUID
	byChallan := map[uuid.UUID]*form140.Challan{}
	var nilDeductees []form140.Deductee

	for i := range rows {
		r := &rows[i]

		if r.tax.IsZero() {
			// A sub-threshold payment. It is REPORTED — the aggregate under
			// s.393(1) is of amounts PAID — and it belongs to the nil challan,
			// because no tax was deposited for it.
			dd, err := deducteeFor(r)
			if err != nil {
				return nil, err
			}
			nilDeductees = append(nilDeductees, dd)
			continue
		}

		if r.challanID == nil {
			return nil, fmt.Errorf("%w: voucher %s deducted %s under %s from %s, and no challan "+
				"is recorded against it. Deposit the tax and record the challan — the return has "+
				"no way to carry a deduction with no deposit behind it",
				ErrTaxNotDeposited, r.voucherNo, r.tax, r.section, r.deducteeNm)
		}
		// The 0045 mechanism. See ErrAllocationBrokenByAlter.
		if !r.allocated.Equal(r.tax) {
			return nil, fmt.Errorf("%w: voucher %s now shows %s of tax under %s, but %s was "+
				"deposited against it on challan %s of %s. Re-allocate the deposit, or correct "+
				"the voucher, before filing",
				ErrAllocationBrokenByAlter, r.voucherNo, r.tax, r.section, r.allocated,
				r.challanNo, r.depositDate.Format("2006-01-02"))
		}

		c, seen := byChallan[*r.challanID]
		if !seen {
			c = &form140.Challan{
				TaxDeductedRupees: r.chTax,
				InterestRupees:    r.chInterest,
				FeeRupees:         r.chFee,
				PenaltyRupees:     r.chPenalty,
				BSRCode:           r.bsrCode,
				ChallanNo:         r.challanNo,
				ByBookEntry:       r.byBookEntry,
				DepositDate:       r.depositDate,
				MinorHead:         r.minorHead,
			}
			byChallan[*r.challanID] = c
			order = append(order, *r.challanID)
		}
		dd, err := deducteeFor(r)
		if err != nil {
			return nil, err
		}
		c.Deductees = append(c.Deductees, dd)
	}

	out := make([]form140.Challan, 0, len(order)+1)
	for _, id := range order {
		out = append(out, *byChallan[id])
	}
	if len(nilDeductees) > 0 {
		// SYNTHESISED, never stored — migration 0069's header says why. Its
		// date is fixed by the format to the last day of the quarter.
		out = append(out, form140.Challan{
			Nil:         true,
			DepositDate: qEnd,
			Deductees:   nilDeductees,
		})
	}
	return out, nil
}

// deducteeFor maps one deduction to a DD record, deriving the Annexure 6 flag.
func deducteeFor(r *sourcedDeduction) (form140.Deductee, error) {
	pan := r.deducteePAN
	if pan == "" {
		// The format's three placeholders are NOT interchangeable: PANAPPLIED
		// means one was applied for, PANINVALID that one was quoted in a bad
		// format, PANNOTAVBL that the deductee gave no declaration at all.
		// Nothing in this schema records which, so the one that CLAIMS THE
		// LEAST is used. Recording the declaration is the fix; guessing
		// PANAPPLIED would assert a document exists.
		pan = "PANNOTAVBL"
	}

	flag, err := reasonFlagFor(r)
	if err != nil {
		return form140.Deductee{}, err
	}

	dd := form140.Deductee{
		PAN:                pan,
		Name:               r.deducteeNm,
		SectionCode:        r.returnCode,
		PaymentDate:        r.date,
		AmountPaidRupees:   r.base,
		TaxDeductedRupees:  r.tax,
		TaxDepositedRupees: r.allocated,
		ReasonFlag:         flag,
	}
	// ⚠️ THE STORED RATE AND THE REPORTED RATE DIFFER ON A SUB-THRESHOLD ROW,
	// and this is a seam rather than a bug in either place.
	//
	// compliance.TDSDerive returns RatePct — the rate IN FORCE for the section,
	// after any no-PAN override — whether or not the deduction applied, and
	// tds_deduction stores it. That is the right fact to keep: it records the
	// rate that was TESTED, so a deduction can be re-checked years later
	// against the law as it then stood.
	//
	// The return wants something else. Deductee Detail field 28: "If value in
	// field 'Total tax collected' is 0.00 then rate at which tax deducted should
	// be 0.0000." A ₹20,000 professional fee below the ₹50,000 limit is stored
	// with rate 10 and must be REPORTED with rate 0.0000, because no tax was
	// deducted at any rate. Writing the stored 10 there would assert a deduction
	// that did not happen.
	if !r.tax.IsZero() {
		dd.RatePct = r.rate
		// Date of deduction is mandatory when tax was deducted and forbidden
		// when it was not. tds_deduction carries one date — the date the sum
		// was credited or paid — and the deduction happens on it, so the two
		// are the same here. They diverge only where tax is deducted on credit
		// and paid later, which this build does not model.
		dd.DeductionDate = r.date
		dd.Deposited = r.allocated.Equal(r.tax)
	}
	return dd, nil
}

// reasonFlagFor derives the Annexure 6 flag, or refuses.
//
// Only Y and C are derivable from stored facts — see the file header. The
// ordering matters: a zero-tax row is Y whether or not a PAN is on record,
// because C asserts tax was deducted AT THE HIGHER RATE and no tax was.
func reasonFlagFor(r *sourcedDeduction) (string, error) {
	switch {
	case r.tax.IsZero():
		if !form140.ReasonFlagValid("Y", r.returnCode) {
			// The rent case. form140 refuses this too, but refusing here names
			// the voucher and the supplier, which is what an operator needs.
			legal := form140.ReasonFlagsFor(r.returnCode)
			return "", fmt.Errorf("%w: voucher %s pays %s to %s under section code %s with no "+
				"tax, but Annexure 6 does not permit the below-threshold flag Y against that "+
				"code. The flags it permits are %v, and every one of them rests on a document "+
				"this software does not record",
				form140.ErrNoValidReasonFlag, r.voucherNo, r.base, r.deducteeNm, r.returnCode, legal)
		}
		return "Y", nil
	case !r.panOnRecord:
		// s.397(2): deducted at the higher rate for non-furnishing of PAN.
		if !form140.ReasonFlagValid("C", r.returnCode) {
			return "", fmt.Errorf("%w: voucher %s was deducted at the higher no-PAN rate under "+
				"section code %s, and Annexure 6 does not permit flag C against it",
				ErrNoReasonDocument, r.voucherNo, r.returnCode)
		}
		return "C", nil
	default:
		// An ordinary deduction at the ordinary rate needs no reason.
		return "", nil
	}
}
