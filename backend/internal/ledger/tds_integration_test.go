package ledger_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
)

// tdsFixture is the working set every TDS integration case needs: the ledger
// service, a professional-fees expense ledger, a TDS-payable ledger and a
// creditor.
type tdsFixture struct {
	led       *ledger.Service
	pool      *db.Pool
	ctx       context.Context
	purchType uuid.UUID
	journType uuid.UUID
	expense   uuid.UUID
	tdsPay    uuid.UUID
	creditor  uuid.UUID
}

func newTDSFixture(t *testing.T) tdsFixture {
	t.Helper()
	pool, ctx, _, _ := setup(t)
	m := masters.NewService(pool)
	mk := func(name, group string) uuid.UUID {
		id, err := m.CreateLedger(ctx, masters.Ledger{Name: name, GroupName: group}, "tester")
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	vt := func(name string) uuid.UUID {
		id, err := m.VoucherTypeID(ctx, name)
		if err != nil {
			t.Fatalf("voucher type %s: %v", name, err)
		}
		return id
	}
	f := tdsFixture{
		led:       ledger.NewService(pool),
		pool:      pool,
		ctx:       ctx,
		purchType: vt("Purchase"),
		journType: vt("Journal"),
		expense:   mk("Professional Fees", "Indirect Expenses"),
		tdsPay:    mk("TDS Payable 194J", "Duties & Taxes"),
		creditor:  mk("Consultant A", "Sundry Creditors"),
	}
	// EVERY voucher in this file is dated in 2026-27, and s.402(11)/(37) tests
	// the tax year IMMEDIATELY PRECEDING — so the status that governs them is
	// the one recorded for 2025. Recording it here rather than in each test is
	// safe precisely because its ABSENCE is separately pinned by
	// TestTDSRefusedWhenDeductorStatusUnrecorded, which builds its own fixture.
	//
	// COMPANY is chosen deliberately: s.402(11)(d) and s.402(37)(a) both catch it
	// unconditionally, so these arithmetic tests are not also silently testing
	// the turnover limb. The limb has its own tests.
	recordDeductorStatus(t, pool, ctx, 2025, compliance.EntityCompany)
	return f
}

// recordDeductorStatus writes the migration 0049 row that makes a company liable
// to deduct. Written by raw SQL rather than through a service because at the
// time this was needed no service existed — see the route added alongside it.
func recordDeductorStatus(t *testing.T, pool *db.Pool, ctx context.Context, taxYearStart int, category string) {
	t.Helper()
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		t.Fatal("no company in context")
	}
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO company_deductor_status
			   (id, company_id, tax_year_start, entity_category, recorded_by)
			 VALUES ($1, $2, $3, $4, 'tester')
			 ON CONFLICT (company_id, tax_year_start) DO NOTHING`,
			uuid.New(), companyID, taxYearStart, category)
		return err
	})
	if err != nil {
		t.Fatalf("record deductor status: %v", err)
	}
}

// tdsDeductionRows reads back migration 0050's rows for one voucher, so a test
// can assert what was PERSISTED rather than only what was returned in-process.
// The two disagreeing is the whole failure mode this table exists to end.
type tdsDeductionRow struct {
	Section      string
	TaxYearStart int
	Base         decimal.Decimal
	Rate         decimal.Decimal
	Amount       decimal.Decimal
	ReturnCode   string
	Deductee     uuid.UUID
}

func tdsDeductionRows(t *testing.T, pool *db.Pool, ctx context.Context, voucherID uuid.UUID) []tdsDeductionRow {
	t.Helper()
	var out []tdsDeductionRow
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT section, tax_year_start, taxable_base_rupees, rate_pct,
			        tds_amount_rupees, return_code, deductee_ledger_id
			   FROM tds_deduction WHERE voucher_id = $1 ORDER BY leg_seq`, voucherID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r tdsDeductionRow
			if err := rows.Scan(&r.Section, &r.TaxYearStart, &r.Base, &r.Rate,
				&r.Amount, &r.ReturnCode, &r.Deductee); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("read tds_deduction: %v", err)
	}
	return out
}

// versionCount reports how many voucher_version rows exist in the current
// company scope. Used to prove a rejected post wrote NOTHING (the derivation
// runs inside the tx, so the rollback must be total).
func versionCount(t *testing.T, pool *db.Pool, ctx context.Context) int {
	t.Helper()
	var n int
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM voucher_version`).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count voucher_version: %v", err)
	}
	return n
}

// TestPostPurchaseWithTDSLegVerified is the happy path: 2,00,000 of professional
// fees under s.194J-P at 10% -> 20,000 deducted. Expense Dr 200000, TDS payable
// Cr 20000, consultant Cr 180000 (the net actually payable).
func TestPostPurchaseWithTDSLegVerified(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-1",
		VoucherDate: "2026-05-10", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "200000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "20000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "180000.0000")},
		},
		TDS: &ledger.TDSParams{
			RulesetVersion:   "tds-2026-04",
			Section:          "194J-P",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "200000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if err != nil {
		t.Fatalf("post with matching TDS leg must succeed: %v", err)
	}
	if !res.TDS.Applied {
		t.Error("TDS should have applied above the 50,000 threshold")
	}
	if !res.TDS.TDS.Equal(dec(t, "20000.0000")) {
		t.Errorf("derived TDS: got %s, want 20000.0000", res.TDS.TDS)
	}
	if !res.TDS.RatePct.Equal(dec(t, "10")) {
		t.Errorf("derived rate: got %s, want 10", res.TDS.RatePct)
	}
	if res.TDS.Basis != compliance.BasisAnnual {
		t.Errorf("basis: got %q, want ANNUAL (v1 applies it per transaction — disclosed)", res.TDS.Basis)
	}

	// The books must actually carry the deduction.
	d, c := ledgerBalance(t, f.pool, f.ctx, f.tdsPay)
	if !d.IsZero() || !c.Equal(dec(t, "20000.0000")) {
		t.Fatalf("TDS payable balance: dr=%s cr=%s, want dr=0 cr=20000", d, c)
	}
	_, credCr := ledgerBalance(t, f.pool, f.ctx, f.creditor)
	if !credCr.Equal(dec(t, "180000.0000")) {
		t.Fatalf("creditor credit: got %s, want 180000.0000 (net of TDS)", credCr)
	}
}

// TestPostWithMismatchedTDSLegRejected is the core non-vacuity gate. The
// voucher stays PERFECTLY BALANCED (200000 Dr = 19000 + 181000 Cr) so it sails
// past validatePostings and the deferred DB balance trigger; only
// deriveAndVerifyTDS can reject it. If that function were deleted, this post
// would succeed and the test would fail.
func TestPostWithMismatchedTDSLegRejected(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)
	before := versionCount(t, f.pool, f.ctx)

	_, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-BAD",
		VoucherDate: "2026-05-11", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "200000.0000")},
			// 19,000 instead of the derived 20,000; the discrepancy is absorbed by
			// the creditor leg so the voucher still balances.
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "19000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "181000.0000")},
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
		t.Fatal("a TDS leg of 19000 against a derived 20000 must be REJECTED")
	}
	if !errors.Is(err, ledger.ErrTDSMismatch) {
		t.Fatalf("want ErrTDSMismatch, got %v", err)
	}
	// The whole post must have rolled back: the derivation runs inside the tx.
	if after := versionCount(t, f.pool, f.ctx); after != before {
		t.Fatalf("rejected post wrote %d voucher_version rows", after-before)
	}
}

// TestPostWithTDSLegOnWrongSideRejected pins the deliberate divergence from
// deriveAndVerifyGST, which sums tax legs ignoring Dr/Cr. A TDS deduction is a
// liability and is always a CREDIT; the voucher below balances and its TDS leg
// has the right MAGNITUDE, so only the side check can reject it.
func TestPostWithTDSLegOnWrongSideRejected(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)
	before := versionCount(t, f.pool, f.ctx)

	_, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-SIDE",
		VoucherDate: "2026-05-12", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "200000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Debit, Amount: dec(t, "20000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "220000.0000")},
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
		t.Fatal("a TDS leg posted on the DEBIT side must be REJECTED")
	}
	if !errors.Is(err, ledger.ErrTDSMismatch) {
		t.Fatalf("want ErrTDSMismatch, got %v", err)
	}
	if after := versionCount(t, f.pool, f.ctx); after != before {
		t.Fatalf("rejected post wrote %d voucher_version rows", after-before)
	}
}

// TestPostBelowThresholdDerivesZeroTDS proves the below-threshold path end to
// end: a 25,000 professional fee is under the 50,000 limit, so NOTHING may be
// deducted — a voucher that nonetheless posts a TDS leg is rejected, and the
// same voucher without one is accepted with Applied=false.
func TestPostBelowThresholdDerivesZeroTDS(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	params := func() *ledger.TDSParams {
		return &ledger.TDSParams{
			Section:          "194J-P",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "25000.0000"),
			TDSLedgerID:      f.tdsPay,
		}
	}

	_, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-LOW-BAD",
		VoucherDate: "2026-05-13", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "25000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "2500.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "22500.0000")},
		},
		TDS: params(),
	})
	if !errors.Is(err, ledger.ErrTDSMismatch) {
		t.Fatalf("deducting below the threshold must be rejected; got %v", err)
	}

	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-LOW-OK",
		VoucherDate: "2026-05-13", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "25000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "25000.0000")},
		},
		TDS: params(),
	})
	if err != nil {
		t.Fatalf("below-threshold voucher with no TDS leg must post: %v", err)
	}
	if res.TDS.Applied || !res.TDS.TDS.IsZero() {
		t.Fatalf("below threshold: applied=%v tds=%s, want false/0", res.TDS.Applied, res.TDS.TDS)
	}
	if !res.TDS.Threshold.Equal(dec(t, "50000.0000")) {
		t.Errorf("threshold echoed: got %s, want 50000.0000", res.TDS.Threshold)
	}
}

// TestJournalTDSNoPANAppliesSection206AA covers the no-PAN path on the Journal
// class (TDS rides Purchase/Payment/Journal). 194C 'other' is 2%, but with no
// PAN s.206AA lifts it to 20% -> 20,000 on 1,00,000.
func TestJournalTDSNoPANAppliesSection206AA(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Journal, VoucherTypeID: f.journType, VoucherNo: "TDS-J1",
		VoucherDate: "2026-05-14", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "100000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "20000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "80000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194C",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     false,
			TaxableAmount:    dec(t, "100000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if err != nil {
		t.Fatalf("no-PAN journal post: %v", err)
	}
	if !res.TDS.RatePct.Equal(dec(t, "20")) {
		t.Fatalf("s.206AA rate: got %s, want 20", res.TDS.RatePct)
	}
	if !res.TDS.TDS.Equal(dec(t, "20000.0000")) {
		t.Fatalf("no-PAN TDS: got %s, want 20000.0000", res.TDS.TDS)
	}
}

// TestTDSDefaultsPinned pins the two silent fallbacks in deriveAndVerifyTDS that
// no other test exercises: an empty RulesetVersion means DefaultTDSRulesetVersion,
// and an empty DeducteeType means OTHER (the CONSERVATIVE choice — 194C is 2%
// for "other" against 1% for individuals/HUF, so an unspecified deductee never
// under-deducts). Both were previously unpinned: flipping the deductee default
// to IND_HUF passed every gate in this package, which would have silently halved
// the 194C deduction for any client that omits the field.
func TestTDSDefaultsPinned(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	// 194C, deductee type OMITTED, ruleset version OMITTED. The default must be
	// OTHER at 2% -> 2,000 on 1,00,000. Under the IND_HUF default it would be
	// 1,000 and this perfectly balanced voucher would be REJECTED.
	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-DEF",
		VoucherDate: "2026-05-16", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "100000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "2000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "98000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194C",
			DeducteeLedgerID: f.creditor,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "100000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if err != nil {
		t.Fatalf("empty DeducteeType must default to OTHER (2%%): %v", err)
	}
	if !res.TDS.RatePct.Equal(dec(t, "2")) {
		t.Fatalf("defaulted rate: got %s, want 2 (OTHER)", res.TDS.RatePct)
	}
	if !res.TDS.TDS.Equal(dec(t, "2000.0000")) {
		t.Fatalf("defaulted TDS: got %s, want 2000.0000", res.TDS.TDS)
	}

	// An UNKNOWN ruleset version must be an error, not a silent fallback to the
	// default — otherwise a typo'd version would derive under the wrong rates.
	_, err = f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-BADVER",
		VoucherDate: "2026-05-16", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "100000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "100000.0000")},
		},
		TDS: &ledger.TDSParams{
			RulesetVersion:   "tds-1999-04",
			Section:          "194C",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "100000.0000"),
		},
	})
	if !errors.Is(err, compliance.ErrUnknownRuleset) {
		t.Fatalf("unknown TDS ruleset version must be rejected; got %v", err)
	}
}

// TestTDSDerivesWithoutLedgerIDButDoesNotVerify pins the documented seam in
// TDSParams: with TDSLedgerID unset the deduction is still DERIVED and returned,
// but NO posted leg is cross-checked. Without this test the "when it is uuid.Nil
// nothing is verified" branch is only a doc comment.
func TestTDSDerivesWithoutLedgerIDButDoesNotVerify(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)

	// A deliberately WRONG TDS leg (5,000 against a derived 2,000). It is not
	// verified because no TDSLedgerID was supplied, so the post succeeds.
	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: f.purchType, VoucherNo: "TDS-NOVERIFY",
		VoucherDate: "2026-05-17", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: dec(t, "100000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "5000.0000")},
			{LedgerID: f.creditor, DrCr: ledger.Credit, Amount: dec(t, "95000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194C",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "100000.0000"),
		},
	})
	if err != nil {
		t.Fatalf("without TDSLedgerID nothing is verified, so the post must succeed: %v", err)
	}
	if !res.TDS.TDS.Equal(dec(t, "2000.0000")) {
		t.Fatalf("derivation still runs: got %s, want 2000.0000", res.TDS.TDS)
	}
}

// TestPaymentClassIsTDSActive pins the third class in the gate. Purchase and
// Journal are covered above; Payment was asserted only by the tdsActive
// expression itself.
func TestPaymentClassIsTDSActive(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)
	m := masters.NewService(f.pool)
	bank, err := m.CreateLedger(f.ctx, masters.Ledger{Name: "TDS Bank", GroupName: "Bank Accounts"}, "tester")
	if err != nil {
		t.Fatalf("create bank: %v", err)
	}
	vtPay, err := m.VoucherTypeID(f.ctx, "Payment")
	if err != nil {
		t.Fatalf("payment voucher type: %v", err)
	}
	before := versionCount(t, f.pool, f.ctx)

	// Balanced but wrong: 1,000 deducted where the engine derives 2,000.
	_, err = f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Payment, VoucherTypeID: vtPay, VoucherNo: "TDS-P1",
		VoucherDate: "2026-05-18", PartyLedgerID: f.creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.creditor, DrCr: ledger.Debit, Amount: dec(t, "100000.0000")},
			{LedgerID: f.tdsPay, DrCr: ledger.Credit, Amount: dec(t, "1000.0000")},
			{LedgerID: bank, DrCr: ledger.Credit, Amount: dec(t, "99000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section:          "194C",
			DeducteeLedgerID: f.creditor,
			DeducteeType:     compliance.DeducteeOther,
			PANAvailable:     true,
			TaxableAmount:    dec(t, "100000.0000"),
			TDSLedgerID:      f.tdsPay,
		},
	})
	if !errors.Is(err, ledger.ErrTDSMismatch) {
		t.Fatalf("Payment class must be TDS-active; want ErrTDSMismatch, got %v", err)
	}
	if after := versionCount(t, f.pool, f.ctx); after != before {
		t.Fatalf("rejected payment wrote %d voucher_version rows", after-before)
	}
}

// TestSalesClassIgnoresTDSParams pins the class gate: a deduction is made by the
// payer, so TDS params on a Sales voucher are inert (they must not reject an
// otherwise valid sale). TCS, the collection-side mirror, is a pure engine in
// v1 and is NOT wired into the post path.
func TestSalesClassIgnoresTDSParams(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTDSFixture(t)
	m := masters.NewService(f.pool)
	salesAcct, err := m.CreateLedger(f.ctx, masters.Ledger{Name: "Domestic Sales TDS", GroupName: "Sales Accounts"}, "tester")
	if err != nil {
		t.Fatalf("create sales ledger: %v", err)
	}
	debtor, err := m.CreateLedger(f.ctx, masters.Ledger{Name: "Customer TDS", GroupName: "Sundry Debtors"}, "tester")
	if err != nil {
		t.Fatalf("create debtor: %v", err)
	}
	vtSales, err := m.VoucherTypeID(f.ctx, "Sales")
	if err != nil {
		t.Fatalf("sales voucher type: %v", err)
	}

	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: vtSales, VoucherNo: "TDS-S1",
		VoucherDate: "2026-05-15", PartyLedgerID: debtor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: debtor, DrCr: ledger.Debit, Amount: dec(t, "200000.0000")},
			{LedgerID: salesAcct, DrCr: ledger.Credit, Amount: dec(t, "200000.0000")},
		},
		// Deliberately contradictory: no TDS leg exists, so if the Sales class
		// were TDS-active this post would be rejected.
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
		t.Fatalf("TDS params must be inert on a Sales voucher: %v", err)
	}
	if res.TDS.Applied || !res.TDS.TDS.IsZero() {
		t.Fatalf("no TDS should have been derived on Sales: %+v", res.TDS)
	}
}
