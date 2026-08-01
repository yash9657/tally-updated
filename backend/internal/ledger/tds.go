package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
)

// DefaultTDSRulesetVersion is used when TDSParams.RulesetVersion is empty.
const DefaultTDSRulesetVersion = "tds-2026-04"

// ErrTDSMismatch is returned when a posted TDS leg does not equal the amount
// the compliance engine derived (or sits on the wrong side of the entry). It is
// a sentinel so callers and tests can assert the specific failure rather than
// "some error happened".
var ErrTDSMismatch = errors.New("ledger: posted TDS leg does not match the derived deduction")

// ErrNoDutyToDeduct is returned when a voucher deducts tax that this company is
// not required to deduct — s.402(11)/(37) say the obligation is not universal.
//
// It is a REFUSAL and not a silent zero. A sole proprietor below the turnover
// limit who withholds 10% of a consultant's fee has taken money the consultant
// was entitled to receive, and the consultant can only get it back by filing a
// return. Refusing the post is the only place that harm can still be prevented.
var ErrNoDutyToDeduct = errors.New(
	"ledger: this company is not required to deduct under this section, so a TDS leg must not be posted")

// ErrDeducteeUnknown is returned when a TDS block names no deductee.
//
// It is required rather than optional because WHO was paid is half the key of
// the s.393(1) aggregate and the whole of the return's deductee annexure. A
// deduction with no deductee cannot be cumulated, cannot be reported, and would
// re-create the exact "derived and then discarded" defect migration 0050 exists
// to end.
var ErrDeducteeUnknown = errors.New(
	"ledger: a TDS deduction needs the deductee ledger it was deducted from")

// ErrTDSDateRequired is returned for a TDS voucher with no voucher date.
//
// Every other voucher may be posted undated and take today's date for numbering
// purposes. A TDS voucher may not: s.393(1)(c) dates the deduction to when the
// sum was "credited or paid, whichever is earlier", that date picks the tax
// year, and the tax year is what the aggregate resets on and what the quarter
// is filed for. A defaulted date would file a March payment in the wrong year.
var ErrTDSDateRequired = errors.New(
	"ledger: a voucher carrying TDS must be dated; the deduction date sets the tax year")

// TDSParams carries the inputs needed to derive and verify Tax Deducted at
// Source on a Purchase/Payment/Journal voucher. It mirrors GSTParams: when
// present the ledger derives the deduction and verifies the supplied TDS
// posting matches it, failing the post on mismatch. It NEVER injects postings
// of its own — the books stay exactly what the client sent, they are merely
// refused when they contradict the engine.
//
// WHAT THE POST PATH NOW DOES, in order, inside the posting transaction:
//
//  1. Reads this company's s.402(11)/(37) deductor status for the PRECEDING tax
//     year and refuses the post outright when it is unrecorded
//     (compliance.ErrDeductorStatusUnknown). "We do not know whether you must
//     deduct" is not "you need not deduct", and it is the deductor who carries
//     the liability plus interest.
//  2. Refuses a posted TDS leg when the company has no duty to deduct at all
//     (ErrNoDutyToDeduct).
//  3. Sums the s.393(1) aggregate for (deductee, section, tax year) out of
//     tds_deduction and derives through compliance.TDSDeriveCumulative, so a
//     stream of sub-threshold bills crosses and catches up.
//  4. Verifies the posted leg against that derivation.
//  5. Writes the tds_deduction row in the SAME transaction — including a
//     zero-amount row, which is a fact and not an absence (see migration 0050).
//
// The v1 header that used to sit here disclosed three limitations. The first
// two — nothing persisted, thresholds tested per transaction — are what this
// wiring removes. The third stands: TCS (compliance.TCSDerive) is still not
// wired into the post path.
type TDSParams struct {
	// RulesetVersion selects the effective-dated rate table; empty means
	// DefaultTDSRulesetVersion.
	RulesetVersion string
	// Section is the nature-of-payment key, e.g. "194C", "194J-P", "194I-B".
	Section string
	// DeducteeLedgerID is WHO was paid. Required: it is half the key of the
	// aggregate and the whole of the return's deductee annexure.
	DeducteeLedgerID uuid.UUID
	// DeducteeType is compliance.DeducteeIndHUF or compliance.DeducteeOther.
	DeducteeType string
	// PANAvailable reports whether the deductee's PAN is on record; false
	// triggers the s.206AA floor.
	PANAvailable bool
	// TaxableAmount is the amount the deduction applies to.
	TaxableAmount decimal.Decimal
	// TDSLedgerID, when set, identifies the TDS-payable ledger whose posted leg
	// is cross-checked against the derived deduction. When it is uuid.Nil the
	// derivation still runs (and is returned and persisted) but nothing is
	// verified.
	TDSLedgerID uuid.UUID
}

// readDeductorStatus loads this company's s.402(11)/(37) status for a tax year.
//
// ⚠️ THE YEAR ASKED FOR IS NOT THE YEAR OF THE PAYMENT. The turnover test reads
// "during the tax year IMMEDIATELY PRECEDING the tax year in which such income
// or sum is credited or paid", so a payment in 2027-28 is judged on the status
// recorded for 2026-27. Callers pass the preceding year; deductorStatusTaxYear
// is the one place that subtraction happens.
//
// An ABSENT ROW IS THE REFUSAL, by design — see migration 0049's header. It
// returns compliance.ErrDeductorStatusUnknown, never a zero status.
func readDeductorStatus(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, taxYearStart int) (compliance.DeductorStatus, error) {
	var (
		st       compliance.DeductorStatus
		turnover *decimal.Decimal
		activity *string
	)
	st.TaxYearStart = taxYearStart
	err := tx.QueryRow(ctx,
		`SELECT entity_category, prior_year_turnover_rupees, prior_year_activity
		   FROM company_deductor_status
		  WHERE company_id = $1 AND tax_year_start = $2`,
		companyID, taxYearStart).Scan(&st.EntityCategory, &turnover, &activity)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return compliance.DeductorStatus{}, fmt.Errorf(
			"%w: no status recorded for tax year %d-%02d, which is the year the turnover test reads",
			compliance.ErrDeductorStatusUnknown, taxYearStart, (taxYearStart+1)%100)
	case err != nil:
		return compliance.DeductorStatus{}, fmt.Errorf("ledger: read deductor status: %w", err)
	}
	// TurnoverKnown distinguishes "the test does not apply to this category"
	// from "this company turned over nothing" — two different claims that a
	// plain zero would collapse into one. Migration 0049's turnover_pair CHECK
	// keeps the two columns in step, so reading either one is sufficient.
	if turnover != nil {
		st.TurnoverKnown = true
		st.PriorYearTurnoverRupees = *turnover
	}
	if activity != nil {
		st.PriorYearActivity = *activity
	}
	return st, nil
}

// deductorStatusTaxYear maps the tax year a payment falls in to the tax year
// whose status governs it: the immediately preceding one, per s.402(11)(l)(ii)
// and s.402(37)(b).
func deductorStatusTaxYear(paymentTaxYear int) int { return paymentTaxYear - 1 }

// readTDSAggregate sums the s.393(1) running totals for one deductee under one
// section in one tax year.
//
// TWO THINGS ABOUT THIS QUERY ARE LOAD-BEARING.
//
// 1. IT EXCLUDES THIS VOUCHER'S OWN ROWS. An ALTER re-derives the deduction, and
// a re-derivation that counted the version it is replacing would cumulate the
// voucher against itself — every correction would inflate the aggregate and,
// past the limit, invent a catch-up out of nothing.
//
// 2. IT IS NOT BOUNDED BY DATE, and that is deliberate. The alternative — summing
// only rows dated on or before this payment — is the more literal reading of
// "prior", but it UNDER-COLLECTS on a back-dated entry: the bill inserted behind
// existing ones sees a smaller base, declines to cross, and the rows already
// written are never revisited. Summing the whole tax year instead keeps the
// invariant that after any post, the total tax deducted for the deductee equals
// the tax due on their whole cumulative base. THE DISCLOSED COST is attribution:
// where a back-dated bill triggers the crossing, the catch-up is charged to that
// bill rather than to whichever payment chronologically crossed first. The total
// is exact; which voucher carries it can differ from the paper trail.
func readTDSAggregate(
	ctx context.Context,
	tx pgx.Tx,
	companyID, deducteeID uuid.UUID,
	section string,
	taxYearStart int,
	excludeVoucherID uuid.UUID,
) (compliance.AggregateInput, error) {
	var agg compliance.AggregateInput
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(taxable_base_rupees), 0),
		        COALESCE(SUM(tds_amount_rupees), 0)
		   FROM tds_deduction
		  WHERE company_id = $1
		    AND deductee_ledger_id = $2
		    AND section = $3
		    AND tax_year_start = $4
		    AND voucher_id <> $5`,
		companyID, deducteeID, section, taxYearStart, excludeVoucherID,
	).Scan(&agg.PriorBaseRupees, &agg.PriorTDSRupees)
	if err != nil {
		return compliance.AggregateInput{}, fmt.Errorf("ledger: read tds aggregate: %w", err)
	}
	return agg, nil
}

// deriveAndVerifyTDS runs the cumulative compliance engine against the prior
// aggregate and, when a TDS ledger id is supplied, verifies the posted leg
// equals the derived deduction.
//
// It is DELIBERATELY STRICTER than deriveAndVerifyGST: that function sums
// postings on the tax ledger ignoring Dr/Cr, so a tax leg posted on the wrong
// side still passes. A TDS deduction is always a CREDIT (a liability to the
// government withheld out of what the deductee is paid), so this function
// asserts the side as well as the amount — a Debit on the TDS ledger is an
// error even when its magnitude matches.
func deriveAndVerifyTDS(in VoucherInput, agg compliance.AggregateInput) (compliance.TDSResult, error) {
	p := in.TDS
	version := p.RulesetVersion
	if version == "" {
		version = DefaultTDSRulesetVersion
	}
	rs, err := compliance.LoadTDSRuleSet(version)
	if err != nil {
		return compliance.TDSResult{}, fmt.Errorf("ledger: tds ruleset: %w", err)
	}
	deducteeType := p.DeducteeType
	if deducteeType == "" {
		deducteeType = compliance.DeducteeOther
	}
	res, err := compliance.TDSDeriveCumulative(compliance.TDSInput{
		Section:       p.Section,
		DeducteeType:  deducteeType,
		PANAvailable:  p.PANAvailable,
		TaxableAmount: p.TaxableAmount,
	}, agg, rs)
	if err != nil {
		return compliance.TDSResult{}, fmt.Errorf("ledger: derive tds: %w", err)
	}

	if p.TDSLedgerID == uuid.Nil {
		return res, nil
	}

	posted := decimal.Zero
	for _, pl := range in.Postings {
		if pl.LedgerID != p.TDSLedgerID {
			continue
		}
		if pl.DrCr != Credit {
			return compliance.TDSResult{}, fmt.Errorf(
				"%w: leg of %s is on the DEBIT side; a TDS deduction is always a credit",
				ErrTDSMismatch, pl.Amount.StringFixed(4))
		}
		posted = posted.Add(pl.Amount)
	}
	if !posted.Equal(res.TDS) {
		return compliance.TDSResult{}, fmt.Errorf(
			"%w: posted %s != derived %s (section %s, rate %s%%, threshold %s, applied=%v, "+
				"aggregate base %s against limit %s, crossed=%v)",
			ErrTDSMismatch, posted.StringFixed(4), res.TDS.StringFixed(4),
			p.Section, res.RatePct, res.Threshold.StringFixed(4), res.Applied,
			res.AggregateBaseRupees.StringFixed(4), res.AggregateThresholdRupees.StringFixed(4),
			res.AggregateCrossed)
	}
	return res, nil
}

// verifyNoTDSLegPosted enforces ErrNoDutyToDeduct: a company with no obligation
// under this section must not withhold anything.
//
// It checks the POSTINGS rather than trusting the client's declared amount,
// because the harm is the money that actually left the deductee's payment.
func verifyNoTDSLegPosted(in VoucherInput, section string) error {
	p := in.TDS
	if p.TDSLedgerID == uuid.Nil {
		return nil
	}
	posted := decimal.Zero
	for _, pl := range in.Postings {
		if pl.LedgerID == p.TDSLedgerID {
			posted = posted.Add(pl.Amount)
		}
	}
	if posted.IsZero() {
		return nil
	}
	return fmt.Errorf(
		"%w: %s was withheld under %s, but this company is not a person required to "+
			"deduct under it — that money belongs to the deductee",
		ErrNoDutyToDeduct, posted.StringFixed(4), section)
}

// deleteTDSDeductions removes every deduction row for a voucher.
//
// Called for ALTER and DELETE alike, and BEFORE any re-insert, because migration
// 0050 keys the row on voucher_id — the identity that survives a correction. An
// ALTER re-states what the voucher IS NOW, exactly as it re-states postings,
// GST lines and stock movements; a voucher altered to carry no TDS at all must
// therefore lose its deduction row rather than keep a stale one that the
// aggregate would go on counting.
func deleteTDSDeductions(ctx context.Context, tx pgx.Tx, companyID, voucherID uuid.UUID) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM tds_deduction WHERE company_id = $1 AND voucher_id = $2`,
		companyID, voucherID); err != nil {
		return fmt.Errorf("ledger: clear prior tds deductions: %w", err)
	}
	return nil
}

// writeTDSDeduction persists the derived deduction as migration 0050's row.
//
// ⚠️ IT WRITES A ROW EVEN WHEN NO TAX WAS DEDUCTED. A zero-amount row is a fact,
// not an absence: the s.393(1) aggregate is of amounts PAID, so the twenty
// sub-threshold bills are precisely what carries the total past the limit, and
// Annexure 6 flag `Y` reports a below-threshold payment rather than omitting it.
// Do not add a "only if applied" guard here.
//
// leg_seq is 0 because VoucherInput carries ONE TDS block, so one voucher
// currently produces at most one deduction per section. Migration 0050 models
// more (a consolidated contractor payment can carry two of the same nature);
// exercising that needs TDSParams to become a slice, which is T6 register work
// and is deferred, not overlooked.
func writeTDSDeduction(
	ctx context.Context,
	tx pgx.Tx,
	companyID, voucherID uuid.UUID,
	in VoucherInput,
	res compliance.TDSResult,
	taxYearStart int,
	deductionDate string,
	recordedBy string,
) error {
	p := in.TDS
	deducteeType := p.DeducteeType
	if deducteeType == "" {
		deducteeType = compliance.DeducteeOther
	}
	version := p.RulesetVersion
	if version == "" {
		version = DefaultTDSRulesetVersion
	}
	// The return code comes off the RESULT, decided at the same moment and from
	// the same inputs as the amount. Re-deriving it later from the stored section
	// would file directors' fees (1028) as professional fees (1027) — the two
	// share statutory limb 6(iii).D(b) at the same rate and differ only here.
	code := res.ReturnCode
	if code == "" {
		var err error
		if code, err = compliance.TDSReturnCode(p.Section, deducteeType); err != nil {
			return fmt.Errorf("ledger: tds return code: %w", err)
		}
	}
	rowID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("ledger: new tds deduction id: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO tds_deduction
		   (id, company_id, voucher_id, leg_seq, deductee_ledger_id, section,
		    deductee_type, tax_year_start, deduction_date, taxable_base_rupees,
		    rate_pct, tds_amount_rupees, return_code, pan_available,
		    ruleset_version, recorded_by)
		 VALUES ($1, $2, $3, 0, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		rowID, companyID, voucherID, p.DeducteeLedgerID, p.Section,
		deducteeType, taxYearStart, deductionDate, p.TaxableAmount,
		res.RatePct, res.TDS, code, p.PANAvailable,
		version, recordedBy,
	); err != nil {
		return fmt.Errorf("ledger: insert tds_deduction: %w", err)
	}
	return nil
}
