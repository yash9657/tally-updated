// THE POST PATH, END TO END — liability, cumulation, and the row that is left
// behind.
//
// Every test in this file exercises something that was UNREACHABLE before the
// engines were wired: compliance.MustDeduct, compliance.TDSDeriveCumulative and
// migration 0050's tds_deduction all existed, were tested as pure functions, and
// nothing called them. The unit tests beside them prove the arithmetic; these
// prove a user can actually get it.
//
// They map onto docs/design/t6-gaps-model.md §4:
//
//	V-5   TestContractorAggregateCatchUp
//	V-11  TestTDSRefusedWhenDeductorStatusUnrecorded
//	V-12  TestAOPIsSpecifiedButNotDesignated
package ledger_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
)

// recordNaturalPersonStatus writes a status row for a category that IS subject
// to the turnover test, which needs both operands (migration 0049's
// turnover_pair CHECK).
func recordNaturalPersonStatus(
	t *testing.T, pool *db.Pool, ctx context.Context,
	taxYearStart int, category, activity, turnoverRupees string,
) {
	t.Helper()
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		t.Fatal("no company in context")
	}
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO company_deductor_status
			   (id, company_id, tax_year_start, entity_category,
			    prior_year_turnover_rupees, prior_year_activity, recorded_by)
			 VALUES ($1, $2, $3, $4, $5, $6, 'tester')
			 ON CONFLICT (company_id, tax_year_start) DO UPDATE
			   SET entity_category            = EXCLUDED.entity_category,
			       prior_year_turnover_rupees = EXCLUDED.prior_year_turnover_rupees,
			       prior_year_activity        = EXCLUDED.prior_year_activity`,
			uuid.New(), companyID, taxYearStart, category, turnoverRupees, activity)
		return err
	})
	if err != nil {
		t.Fatalf("record natural-person deductor status: %v", err)
	}
}

// clearDeductorStatus removes the fixture's status row, so a test can post into
// a company whose liability is genuinely unrecorded.
func clearDeductorStatus(t *testing.T, pool *db.Pool, ctx context.Context) {
	t.Helper()
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM company_deductor_status`)
		return err
	})
	if err != nil {
		t.Fatalf("clear deductor status: %v", err)
	}
}

// TestTDSRefusedWhenDeductorStatusUnrecorded is V-11, and it is the test the
// whole of deductor.go exists for.
//
// ⚠️ THE ASSERTION THAT MATTERS IS THAT THIS IS AN ERROR, NOT A ZERO. A zero is
// indistinguishable from "correctly below the threshold", and it is the DEDUCTOR
// who carries the shortfall plus interest under s.396. Mutating the wiring to
// treat an unknown status as "no deduction" must fail here.
func TestTDSRefusedWhenDeductorStatusUnrecorded(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)
	clearDeductorStatus(t, f.pool, f.ctx)
	before := versionCount(t, f.pool, f.ctx)

	// A voucher that is correct in every other respect: balanced, and its TDS
	// leg is exactly what the engine would derive if it were allowed to.
	_, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-NOSTATUS",
		VoucherDate: "2026-05-10", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "200000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "20000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "180000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194J-P",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "200000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if err == nil {
		t.Fatal("a company with no recorded deductor status must be REFUSED, not " +
			"quietly allowed to deduct — whether it must deduct is unknown")
	}
	if !errors.Is(err, compliance.ErrDeductorStatusUnknown) {
		t.Fatalf("want ErrDeductorStatusUnknown, got %v", err)
	}
	if after := versionCount(t, f.pool, f.ctx); after != before {
		t.Fatalf("refused post wrote %d voucher_version rows; the refusal runs "+
			"inside the tx and must roll everything back", after-before)
	}
}

// TestDeductorStatusReadsThePrecedingTaxYear pins the off-by-one that
// s.402(11)(l)(ii) invites: the test reads "the tax year IMMEDIATELY PRECEDING
// the tax year in which such income or sum is credited or paid".
//
// A payment on 2026-05-10 falls in tax year 2026-27, so the row that governs it
// is 2025 — NOT 2026. A status recorded only for 2026 must therefore leave the
// post refused. Wiring the lookup to the payment's own year passes every other
// test in this package and fails this one.
func TestDeductorStatusReadsThePrecedingTaxYear(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)
	clearDeductorStatus(t, f.pool, f.ctx)
	// The year of the PAYMENT, which is the wrong one to record.
	recordDeductorStatus(t, f.pool, f.ctx, 2026, compliance.EntityCompany)

	post := func(no string) error {
		_, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
			Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: no,
			VoucherDate: "2026-05-10", PartyLedgerID: f.creditor, CreatedBy: "tester",
			Postings: []ledger.PostingInput{
				{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "200000.0000")},
				{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "20000.0000")},
				{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "180000.0000")},
			},
			TDS: &ledger.TDSParams{
				Section:          "194J-P",
				DeducteeLedgerID: f.creditor,
				DeducteeType:     compliance.DeducteeOther,
				PANAvailable:     true,
				TaxableAmount:    dec(t, "200000.0000"),
				TDSLedgerID:      f.tdsPay,
			},
		})
		return err
	}

	if err := post("TDS-YEAR-WRONG"); !errors.Is(err, compliance.ErrDeductorStatusUnknown) {
		t.Fatalf("a status recorded for 2026-27 does not govern a 2026-27 payment; "+
			"the turnover test reads the PRECEDING year. want ErrDeductorStatusUnknown, got %v", err)
	}

	// The control: record the preceding year as well and the identical voucher
	// posts. Without this arm the test would also pass if the lookup were broken
	// in some way that refused everything.
	recordDeductorStatus(t, f.pool, f.ctx, 2025, compliance.EntityCompany)
	if err := post("TDS-YEAR-RIGHT"); err != nil {
		t.Fatalf("with the preceding year recorded the same voucher must post: %v", err)
	}
}

// TestNoDutyToDeductRefusesAPostedLeg is the harm deductor.go's header names: a
// sole proprietor below the turnover limit who withholds 10% of a consultant's
// fee has taken money the consultant was entitled to receive.
//
// The voucher below BALANCES and its leg matches what TDSDerive would compute —
// so only the liability test can reject it.
func TestNoDutyToDeductRefusesAPostedLeg(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)
	clearDeductorStatus(t, f.pool, f.ctx)
	// An individual with ₹40 lakh of business turnover. s.402(37)(b) requires
	// turnover EXCEEDING one crore, so this proprietor is not a specified person
	// and owes no deduction under 194J-P.
	recordNaturalPersonStatus(t, f.pool, f.ctx, 2025,
		compliance.EntityIndividual, compliance.ActivityBusiness, "4000000")

	_, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-NODUTY",
		VoucherDate: "2026-05-10", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "200000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "20000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "180000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194J-P",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "200000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if !errors.Is(err, ledger.ErrNoDutyToDeduct) {
		t.Fatalf("a proprietor below the turnover limit must not withhold 20,000 "+
			"of a consultant's fee; want ErrNoDutyToDeduct, got %v", err)
	}

	// The SAME payment without a TDS leg posts, deducts nothing, and — this is
	// the part that is easy to get wrong — leaves NO deduction row. A zero row
	// would read in the register and in Annexure 6 as "below the threshold",
	// which is a different and false claim about a payment that simply carried
	// no obligation.
	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-NODUTY-OK",
		VoucherDate: "2026-05-10", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "200000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "200000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194J-P",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "200000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if err != nil {
		t.Fatalf("the same payment with no TDS leg must post: %v", err)
	}
	if rows := tdsDeductionRows(t, f.pool, f.ctx, res.VoucherID); len(rows) != 0 {
		t.Fatalf("a company with no duty to deduct must leave NO deduction row; got %d", len(rows))
	}
}

// TestAOPIsSpecifiedButNotDesignated is V-12, driven through the post path — the
// case that catches an implementation holding liability in a single boolean.
//
// An AOP with ₹40 lakh of turnover:
//   - s.402(37)(a) "any person, NOT BEING an individual or HUF" catches it
//     UNCONDITIONALLY, so it MUST deduct on professional fees (194J-P);
//   - s.402(11)(l) names "an association of persons" explicitly, so it takes the
//     turnover test and MUST NOT deduct on contractor payments (194C).
//
// One flag cannot express that, and any implementation that uses one will fail
// exactly one of the two arms below.
func TestAOPIsSpecifiedButNotDesignated(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)
	clearDeductorStatus(t, f.pool, f.ctx)
	recordNaturalPersonStatus(t, f.pool, f.ctx, 2025,
		compliance.EntityAOP, compliance.ActivityBusiness, "4000000")

	// 194J-P — SPECIFIED person, liable. 2,00,000 at 10% = 20,000.
	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "AOP-194J",
		VoucherDate: "2026-05-10", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "200000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "20000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "180000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194J-P",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "200000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if err != nil {
		t.Fatalf("an AOP is a SPECIFIED person unconditionally and must deduct "+
			"under 194J-P whatever its turnover: %v", err)
	}
	if !res.TDS.TDS.Equal(dec(t, "20000.0000")) {
		t.Fatalf("194J-P deduction: got %s, want 20000.0000", res.TDS.TDS)
	}

	// 194C — DESIGNATED person, and the turnover test fails, so nothing may be
	// withheld from the contractor.
	_, err = f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "AOP-194C",
		VoucherDate: "2026-05-11", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "200000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "4000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "196000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194C",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "200000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if !errors.Is(err, ledger.ErrNoDutyToDeduct) {
		t.Fatalf("an AOP under ₹1 crore is NOT a designated person and must not "+
			"deduct under 194C; want ErrNoDutyToDeduct, got %v", err)
	}
}

// TestTDSDeductionIsPersisted is the debt this whole slice pays: the engine used
// to derive a deduction, verify the leg and then DISCARD every fact about it, so
// a posted TDS voucher left behind an unexplained credit to a TDS ledger and no
// return could be filed from it.
//
// It asserts what is IN THE TABLE, not what came back in-process. The two
// disagreeing is precisely the failure migration 0050 exists to end.
func TestTDSDeductionIsPersisted(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-PERSIST",
		VoucherDate: "2026-05-10", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "200000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "20000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "180000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194J-P",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "200000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	rows := tdsDeductionRows(t, f.pool, f.ctx, res.VoucherID)
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 persisted deduction, got %d", len(rows))
	}
	r := rows[0]
	if r.Section != "194J-P" {
		t.Errorf("section: got %q, want 194J-P", r.Section)
	}
	// The PAYMENT's tax year, 2026-27 — not the 2025 the status test reads.
	if r.TaxYearStart != 2026 {
		t.Errorf("tax_year_start: got %d, want 2026 (the year of the PAYMENT; the "+
			"preceding year is what the deductor-status test reads, and confusing "+
			"the two files the quarter under the wrong year)", r.TaxYearStart)
	}
	if !r.Base.Equal(dec(t, "200000.0000")) {
		t.Errorf("taxable_base_rupees: got %s, want 200000.0000", r.Base)
	}
	if !r.Rate.Equal(dec(t, "10.0000")) {
		t.Errorf("rate_pct: got %s, want 10.0000", r.Rate)
	}
	if !r.Amount.Equal(dec(t, "20000.0000")) {
		t.Errorf("tds_amount_rupees: got %s, want 20000.0000", r.Amount)
	}
	// Annexure 2 code for 194J professional fees. 194J-D (directors' fees) shares
	// the same statutory limb at the same rate and carries 1028 — so a builder
	// re-deriving the code from `section` would file one as the other.
	if r.ReturnCode != "1027" {
		t.Errorf("return_code: got %q, want 1027", r.ReturnCode)
	}
	if r.Deductee != f.creditor {
		t.Errorf("deductee_ledger_id: got %s, want the creditor %s", r.Deductee, f.creditor)
	}
}

// TestSubThresholdPaymentIsStillRecorded pins the sentence migration 0050 opens
// with: A ZERO-AMOUNT ROW IS A FACT, NOT AN ABSENCE.
//
// Adding a `if res.Applied` guard around the write — which looks like an obvious
// tidy-up — makes the aggregate permanently zero and re-creates the bug the
// table exists to fix. It also files an incomplete quarter, because Annexure 6
// flag `Y` REPORTS a below-threshold payment rather than omitting it.
func TestSubThresholdPaymentIsStillRecorded(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	// ₹20,000 to a contractor: under limb (a)'s ₹30,000, so nothing is deducted.
	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-SUB",
		VoucherDate: "2026-05-10", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "20000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "20000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194C",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "20000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if err != nil {
		t.Fatalf("a sub-threshold contractor bill must post: %v", err)
	}
	if res.TDS.Applied {
		t.Fatal("₹20,000 is below limb (a)'s ₹30,000 and must not be deducted upon")
	}

	rows := tdsDeductionRows(t, f.pool, f.ctx, res.VoucherID)
	if len(rows) != 1 {
		t.Fatalf("a sub-threshold payment must STILL be recorded — it is what "+
			"carries the aggregate past the limit, and Annexure 6 flag Y reports "+
			"it. Want 1 row, got %d", len(rows))
	}
	if !rows[0].Amount.IsZero() {
		t.Fatalf("tds_amount_rupees: got %s, want 0", rows[0].Amount)
	}
	if !rows[0].Base.Equal(dec(t, "20000.0000")) {
		t.Fatalf("the row must carry the BASE even with no tax: got %s, want 20000.0000",
			rows[0].Base)
	}
}

// TestContractorAggregateCatchUp is V-5, end to end.
//
// Six ₹20,000 contractor bills. Each is below limb (a)'s ₹30,000, so bills 1–5
// deduct NOTHING and the aggregate reaches exactly ₹1,00,000 — which does not
// EXCEED limb (b)'s ₹1,00,000 either. Bill 6 takes the total to ₹1,20,000 and
// s.393(1)(a) then charges tax "on the ENTIRE amount": 2% of ₹1,20,000 = ₹2,400,
// all of it on this one ₹20,000 payment.
//
// ⚠️ THE ₹2,400 IS THE ASSERTION. An implementation that starts deducting at the
// right moment but only on the crossing payment (₹400) is the half-right version
// of this feature, and it leaves five bills untaxed for ever.
func TestContractorAggregateCatchUp(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	bill := func(no, date, tds, net string) (ledger.PostResult, error) {
		postings := []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "20000.0000")},
		}
		if tds != "" {
			postings = append(postings,
				ledger.PostingInput{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, tds)})
		}
		postings = append(postings,
			ledger.PostingInput{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, net)})
		return f.led.PostVoucher(f.ctx, ledger.VoucherInput{
			Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: no,
			VoucherDate: date, PartyLedgerID: f.creditor, CreatedBy: "tester",
			Postings: postings,
			TDS: &ledger.TDSParams{
				Section:          "194C",
				DeducteeLedgerID: f.creditor,
				DeducteeType:     compliance.DeducteeOther,
				PANAvailable:     true,
				TaxableAmount:    dec(t, "20000.0000"),
				TDSLedgerID:      f.tdsPay,
			},
		})
	}

	// Bills 1–5: nothing deducted, aggregate climbs to exactly ₹1,00,000.
	for i, date := range []string{"2026-04-01", "2026-04-08", "2026-04-15", "2026-04-22", "2026-04-29"} {
		res, err := bill(t.Name()+"-"+string(rune('A'+i)), date, "", "20000.0000")
		if err != nil {
			t.Fatalf("bill %d must post with no deduction: %v", i+1, err)
		}
		if res.TDS.Applied {
			t.Fatalf("bill %d: ₹20,000 is below ₹30,000 and the running total is "+
				"still at or below ₹1,00,000; nothing may be deducted", i+1)
		}
	}

	// Bill 6 crosses. 2% of the WHOLE ₹1,20,000.
	res, err := bill(t.Name()+"-F", "2026-05-06", "2400.0000", "17600.0000")
	if err != nil {
		t.Fatalf("the crossing bill must deduct ₹2,400 — 2%% of the entire "+
			"₹1,20,000 aggregate, not 2%% of this ₹20,000 payment: %v", err)
	}
	if !res.TDS.TDS.Equal(dec(t, "2400.0000")) {
		t.Fatalf("catch-up: got %s, want 2400.0000 (the sixth bill carries the tax "+
			"for all six)", res.TDS.TDS)
	}
	if !res.TDS.AggregateCrossed {
		t.Error("AggregateCrossed must be true; Annexure 6 flag Y turns on it")
	}
	if !res.TDS.CatchUpBaseRupees.Equal(dec(t, "100000.0000")) {
		t.Errorf("CatchUpBaseRupees: got %s, want 100000.0000 (the earlier base "+
			"this payment reached back to tax)", res.TDS.CatchUpBaseRupees)
	}

	// Bill 7: the aggregate has already been fully taxed, so this one pays only
	// its own 2% — ₹400. Getting ₹2,800 here would mean the prior tax was not
	// credited and the deductee is being charged twice.
	res, err = bill(t.Name()+"-G", "2026-05-13", "400.0000", "19600.0000")
	if err != nil {
		t.Fatalf("the bill after the crossing pays only its own 2%%: %v", err)
	}
	if !res.TDS.TDS.Equal(dec(t, "400.0000")) {
		t.Fatalf("post-crossing bill: got %s, want 400.0000", res.TDS.TDS)
	}
}

// TestAlterReDerivesWithoutCumulatingAgainstItself pins the trap in
// readTDSAggregate: an ALTER re-derives, and a re-derivation that counted the
// version it is REPLACING would cumulate the voucher against itself.
//
// ⚠️ THE AMOUNTS HERE ARE CHOSEN TO MAKE THE BUG VISIBLE, AND THE OBVIOUS ONES DO
// NOT. An earlier draft altered ₹80,000 to ₹90,000: with the self-exclusion
// removed that gives 2% of ₹1,70,000 − ₹1,600 already taken = ₹1,800, which is
// EXACTLY the right answer by coincidence — whenever the prior base has been
// taxed at the full rate, the catch-up formula and the per-payment one agree.
// The defect is only observable when the prior base is UNTAXED, so the aggregate
// below is built out of sub-threshold bills.
func TestAlterReDerivesWithoutCumulatingAgainstItself(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	bill := func(no, date, base, tds, net string) (ledger.PostResult, error) {
		postings := []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, base)},
		}
		if tds != "" {
			postings = append(postings,
				ledger.PostingInput{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, tds)})
		}
		postings = append(postings,
			ledger.PostingInput{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, net)})
		return f.led.PostVoucher(f.ctx, ledger.VoucherInput{
			Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: no,
			VoucherDate: date, PartyLedgerID: f.creditor, CreatedBy: "tester",
			Postings: postings,
			TDS: &ledger.TDSParams{
				Section:          "194C",
				DeducteeLedgerID: f.creditor,
				DeducteeType:     compliance.DeducteeOther,
				PANAvailable:     true,
				TaxableAmount:    dec(t, base),
				TDSLedgerID:      f.tdsPay,
			},
		})
	}

	// Four ₹20,000 bills: each below limb (a)'s ₹30,000, none deducted upon, so
	// ₹80,000 of UNTAXED aggregate accumulates.
	for i, date := range []string{"2026-04-01", "2026-04-08", "2026-04-15", "2026-04-22"} {
		if _, err := bill("ALT-BG-"+string(rune('1'+i)), date, "20000.0000", "", "20000.0000"); err != nil {
			t.Fatalf("background bill %d: %v", i+1, err)
		}
	}

	// A fifth ₹20,000 bill takes the aggregate to exactly ₹1,00,000, which does
	// not EXCEED limb (b). Still nothing deducted.
	res, err := bill("TDS-ALT", "2026-04-29", "20000.0000", "", "20000.0000")
	if err != nil {
		t.Fatalf("the fifth bill must post with no deduction: %v", err)
	}
	if res.TDS.Applied {
		t.Fatal("an aggregate of exactly ₹1,00,000 does not exceed limb (b)")
	}

	// Now CORRECT that fifth bill to ₹25,000. The true aggregate becomes
	// ₹80,000 + ₹25,000 = ₹1,05,000, which crosses — so 2% of the whole
	// ₹1,05,000 = ₹2,100 falls on this voucher.
	//
	// Were the alter to count the ₹20,000 row it is REPLACING, the base would be
	// ₹1,25,000 and it would demand ₹2,500. That ₹400 is the bug.
	altered, err := f.led.AlterVoucher(f.ctx, res.VoucherID, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-ALT",
		VoucherDate: "2026-04-29", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "25000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "2100.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "22900.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194C",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "25000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if err != nil {
		t.Fatalf("the alter must derive 2%% of ₹1,05,000 = ₹2,100; counting the "+
			"₹20,000 version it is replacing would demand ₹2,500: %v", err)
	}
	if !altered.TDS.TDS.Equal(dec(t, "2100.0000")) {
		t.Fatalf("altered deduction: got %s, want 2100.0000", altered.TDS.TDS)
	}

	// And exactly ONE row survives, restating the voucher as it now is. A stale
	// ₹20,000 row left beside the new one would go on inflating the aggregate for
	// every later payment to this contractor.
	rows := tdsDeductionRows(t, f.pool, f.ctx, res.VoucherID)
	if len(rows) != 1 {
		t.Fatalf("an alter re-states the deduction; want 1 row, got %d", len(rows))
	}
	if !rows[0].Base.Equal(dec(t, "25000.0000")) {
		t.Fatalf("persisted base after alter: got %s, want 25000.0000", rows[0].Base)
	}
	if !rows[0].Amount.Equal(dec(t, "2100.0000")) {
		t.Fatalf("persisted amount after alter: got %s, want 2100.0000", rows[0].Amount)
	}
}

// TestDeleteRemovesTheDeduction closes the same loop for a tombstone. A deleted
// voucher leaves ledger_balance, every GST report and every stock movement; its
// deduction must go too, or the aggregate goes on counting a payment that the
// books say never happened — and the quarter is filed with it.
func TestDeleteRemovesTheDeduction(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-DEL",
		VoucherDate: "2026-05-10", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "80000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "1600.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "78400.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194C",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "80000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if len(tdsDeductionRows(t, f.pool, f.ctx, res.VoucherID)) != 1 {
		t.Fatal("precondition: the post must have written a deduction row")
	}

	if _, err := f.led.DeleteVoucher(f.ctx, res.VoucherID, "tester"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rows := tdsDeductionRows(t, f.pool, f.ctx, res.VoucherID); len(rows) != 0 {
		t.Fatalf("a tombstoned voucher must carry no deduction; got %d row(s), "+
			"which would keep inflating this contractor's aggregate", len(rows))
	}
}

// TestTDSVoucherMustBeDated pins ErrTDSDateRequired. Every other class may be
// posted undated and take today's date for numbering; a TDS voucher may not,
// because the date picks the tax year and the tax year is what the aggregate
// resets on and what the quarter is filed for.
func TestTDSVoucherMustBeDated(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	_, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-UNDATED",
		PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "80000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "80000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194C",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "80000.0000"),
		},
	})
	if !errors.Is(err, ledger.ErrTDSDateRequired) {
		t.Fatalf("want ErrTDSDateRequired, got %v", err)
	}
}

// TestTDSWithoutDeducteeIsRefused pins ErrDeducteeUnknown. WHO was paid is half
// the key of the aggregate and the whole of the return's deductee annexure; a
// deduction without it cannot be cumulated and cannot be reported, which is the
// "derived and then discarded" defect all over again.
func TestTDSWithoutDeducteeIsRefused(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	_, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-NODEDUCTEE",
		VoucherDate: "2026-05-10", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "80000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "1600.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "78400.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:       "194C",
			DeducteeType:  compliance.DeducteeOther,
			PANAvailable:  true,
			TaxableAmount: dec(t, "80000.0000"),
			TDSLedgerID:   f.tdsPay,
		},
	})
	if !errors.Is(err, ledger.ErrDeducteeUnknown) {
		t.Fatalf("want ErrDeducteeUnknown, got %v", err)
	}
}

// TestAggregateIsPerDeducteeAndPerSection is the control for the cumulation
// tests: the aggregate is keyed on (deductee, section, tax year), so payments to
// a DIFFERENT contractor must not push this one over the limit.
//
// Without this arm, an implementation that summed every payment in the company
// would pass TestContractorAggregateCatchUp exactly as well.
func TestAggregateIsPerDeducteeAndPerSection(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)
	other, err := masters.NewService(f.pool).CreateLedger(f.ctx,
		masters.Ledger{Name: "Consultant B", GroupName: "Sundry Creditors"}, "tester")
	if err != nil {
		t.Fatalf("create second contractor: %v", err)
	}

	bill := func(no string, deductee uuid.UUID, section string) (ledger.PostResult, error) {
		return f.led.PostVoucher(f.ctx, ledger.VoucherInput{
			Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: no,
			VoucherDate: "2026-05-10", PartyLedgerID: deductee, CreatedBy: "tester",
			Postings: []ledger.PostingInput{
				{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "25000.0000")},
				{LedgerID: deductee, DrCr: ledger.Credit, Amount: dec(t, "25000.0000")},
			},
			TDS: &ledger.TDSParams{
				Section:          section,
				DeducteeLedgerID: deductee,
				DeducteeType:     compliance.DeducteeOther,
				PANAvailable:     true,
				TaxableAmount:    dec(t, "25000.0000"),
				TDSLedgerID:      f.tdsPay,
			},
		})
	}

	// Four ₹25,000 bills to contractor B — ₹1,00,000 of 194C base that belongs to
	// somebody else entirely.
	for _, no := range []string{"B-1", "B-2", "B-3", "B-4"} {
		if _, err := bill(no, other, "194C"); err != nil {
			t.Fatalf("bill %s to the other contractor: %v", no, err)
		}
	}
	// Two ₹25,000 bills to contractor A under a DIFFERENT section. 194J-T's
	// ₹50,000 limit is not exceeded by ₹50,000, so these deduct nothing either —
	// and they must not cumulate into 194C.
	for _, no := range []string{"A-J1", "A-J2"} {
		if _, err := bill(no, f.creditor, "194J-T"); err != nil {
			t.Fatalf("bill %s under 194J-T: %v", no, err)
		}
	}

	// Contractor A's FIRST 194C bill. ₹25,000 is below limb (a)'s ₹30,000 and
	// their 194C aggregate is ₹25,000 — nothing may be deducted. Were the sum
	// taken company-wide it would be ₹1,75,000, cross limb (b), and this
	// perfectly balanced voucher would be REFUSED for carrying no TDS leg.
	res, err := bill("A-C1", f.creditor, "194C")
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if res.TDS.Applied {
		t.Fatalf("another contractor's ₹1,25,000 and this contractor's 194J-T "+
			"payments must not push their 194C aggregate over the limb; "+
			"applied=%v tds=%s", res.TDS.Applied, res.TDS.TDS)
	}
}
