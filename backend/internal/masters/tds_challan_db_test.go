package masters_test

// THE DB-BACKED HALF OF THE CHALLAN WRITE PATH.
//
// Everything here needs Postgres, because everything here is about a rule that
// the pure validator CANNOT check: whether the deduction an allocation names
// actually exists, whether it still shows the amount that was deposited against
// it, and whether the whole write is atomic when one allocation of five is bad.
//
// ⚠️ THE NOTE THAT USED TO SIT HERE — "written, type-checked and NOT EXECUTED,
// because another worktree held the shared Postgres on :5433" — IS NO LONGER
// TRUE, and leaving it would tell the next reader to distrust evidence that now
// exists. Everything below has been executed against a live database, including
// the allocate-later and collection tests added for register rows D1, D2 and D4,
// each with a mutation proof whose observed failure was checked against a
// prediction. Treat a failure here as a real regression.
//
// They reuse setup(t) from masters_test.go, which creates the company and — this
// is the part internal/testhygiene fails the build over — registers
// testdb.Cleanup for it. Any company created in this file must do the same.
//
// ===========================================================================
// WHY THE VOUCHER AND THE DEDUCTION ARE SEEDED BY SQL AND NOT POSTED
// ===========================================================================
// The honest way to make a tds_deduction row is to post a voucher through
// internal/ledger, and internal/reporting's suite does exactly that. It cannot
// happen here: internal/masters may not import internal/ledger (the boundary in
// internal/archtest/boundaries_test.go is one-way, and a test that imports it
// would be a test proving nothing about a dependency the package cannot have).
//
// So the fixture writes voucher_version, voucher_head and tds_deduction directly,
// exactly as the census fixture in internal/platform/testdb/testdb_test.go does.
// voucher_version's immutability trigger is BEFORE UPDATE OR DELETE only, so a
// plain INSERT needs no suppression (internal/server/voucher_history_test.go
// records the same fact), and a voucher with no postings trips no deferred
// posting_balanced trigger because that trigger is on posting.
//
// WHAT THAT COSTS, STATED: these deductions were not derived by
// compliance.TDSDerive, so this file proves nothing about the ENGINE. It proves
// the write path's behaviour given deductions, which is its whole job. The
// engine-to-challan-to-return round trip is internal/reporting's suite.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
)

// dec is a money literal. Never a float: the whole-rupee rule lives at the
// fourth decimal place and 1000.50 is not representable as one.
func dec2(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// deductionFixture is one tds_deduction row to seed.
type deductionFixture struct {
	VoucherNo    string
	Section      string
	LegSeq       int
	TaxYearStart int
	// Date is the deduction date, yyyy-mm-dd.
	Date string
	Base decimal.Decimal
	Tax  decimal.Decimal
	// ReturnCode must be four digits (tds_deduction_return_code_shape).
	ReturnCode string
}

// seedDeductee creates the supplier the tax was deducted from.
func seedDeductee(t *testing.T, m *masters.Service, ctx context.Context, name string) uuid.UUID {
	t.Helper()
	id, err := m.CreateLedger(ctx, masters.Ledger{
		Name:      name,
		GroupName: "Sundry Creditors",
	}, "tester")
	if err != nil {
		t.Fatalf("create deductee %q: %v", name, err)
	}
	return id
}

// seedDeduction writes one voucher and one deduction on it, returning the
// VOUCHER id — which is the identity the allocation keys on, and the only one of
// the three ids that survives an ALTER.
func seedDeduction(t *testing.T, pool *db.Pool, ctx context.Context,
	deducteeID uuid.UUID, f deductionFixture) uuid.UUID {
	t.Helper()
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		t.Fatal("seedDeduction needs a company-scoped context")
	}
	if f.ReturnCode == "" {
		f.ReturnCode = "1024"
	}
	voucherID := uuid.New()
	versionID := uuid.New()

	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO voucher_version
			   (id, company_id, voucher_id, version_no, op, hash, payload,
			    created_by, voucher_no, voucher_date)
			 VALUES ($1, $2, $3, 1, 'CREATE', '\x00'::bytea, '{}'::jsonb,
			         'tester', $4, $5::date)`,
			versionID, companyID, voucherID, f.VoucherNo, f.Date); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO voucher_head
			   (company_id, voucher_id, current_version_id, version_no, op, voucher_date)
			 VALUES ($1, $2, $3, 1, 'CREATE', $4::date)`,
			companyID, voucherID, versionID, f.Date); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO tds_deduction
			   (id, company_id, voucher_id, leg_seq, deductee_ledger_id, section,
			    deductee_type, tax_year_start, deduction_date, taxable_base_rupees,
			    rate_pct, tds_amount_rupees, return_code, pan_available,
			    ruleset_version, recorded_by)
			 VALUES ($1, $2, $3, $4, $5, $6, 'OTHER', $7, $8::date, $9, 2, $10,
			         $11, true, 'tds-2026-04', 'tester')`,
			uuid.New(), companyID, voucherID, f.LegSeq, deducteeID, f.Section,
			f.TaxYearStart, f.Date, f.Base, f.Tax, f.ReturnCode)
		return err
	})
	if err != nil {
		t.Fatalf("seed deduction on voucher %s: %v", f.VoucherNo, err)
	}
	return voucherID
}

// oneThousandOn is the fixture used by most tests below: ₹50,000 paid to a
// contractor with ₹1,000 deducted under 194C in 2026-27.
func oneThousandOn(voucherNo string) deductionFixture {
	return deductionFixture{
		VoucherNo:    voucherNo,
		Section:      "194C",
		TaxYearStart: 2026,
		Date:         "2026-07-10",
		Base:         dec2("50000"),
		Tax:          dec2("1000"),
	}
}

// challanFor is a believable deposit of exactly that ₹1,000.
func challanFor(voucherID uuid.UUID, section string, legSeq int,
	amount decimal.Decimal) masters.TDSChallanInput {
	return masters.TDSChallanInput{
		BSRCode:      "0004329",
		DepositDate:  "2026-08-07",
		ChallanNo:    "05122",
		TaxRupees:    amount,
		MinorHead:    "200",
		TaxYearStart: 2026,
		Allocations: []masters.TDSChallanAllocationInput{{
			VoucherID:    voucherID,
			Section:      section,
			LegSeq:       legSeq,
			AmountRupees: amount,
		}},
	}
}

// countChallans is the atomicity probe: after a refused write it must be zero.
func countChallans(t *testing.T, pool *db.Pool, ctx context.Context) int {
	t.Helper()
	var n int
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM tds_challan`).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count challans: %v", err)
	}
	return n
}

func countAllocations(t *testing.T, pool *db.Pool, ctx context.Context) int {
	t.Helper()
	var n int
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM tds_challan_allocation`).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count allocations: %v", err)
	}
	return n
}

// TestRecordingAChallanStoresTheDepositAndWhatItPaidFor is the control and the
// shape of the register row.
func TestRecordingAChallanStoresTheDepositAndWhatItPaidFor(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	voucher := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))

	in := challanFor(voucher, "194C", 0, dec2("1000"))
	// Interest on a late deposit rides on the same challan and is NOT tax. It
	// must not end up in the allocated total.
	in.InterestRupees = dec2("37")
	challanID, err := m.RecordTDSChallan(ctx, in, "tester")
	if err != nil {
		t.Fatalf("record challan: %v", err)
	}
	if challanID == uuid.Nil {
		t.Fatal("RecordTDSChallan returned the nil UUID; tds_challan.id has no DEFAULT, so the " +
			"caller has no other way to name the row it just created")
	}

	rows, err := m.ListTDSChallans(ctx, 2026)
	if err != nil {
		t.Fatalf("list challans: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one challan for 2026-27, got %d", len(rows))
	}
	r := rows[0]
	if r.ID != challanID {
		t.Errorf("listed challan id %s, want %s", r.ID, challanID)
	}
	// The CIN parts, with their leading zeros intact. This is the assertion that
	// fails if anything on the path ever treats either identifier as a number.
	if r.BSRCode != "0004329" {
		t.Errorf("BSR code read back as %q — 0004329 and 4329 are different bank branches",
			r.BSRCode)
	}
	if r.ChallanNo != "05122" {
		t.Errorf("challan serial read back as %q — 05122 and 5122 are different payments",
			r.ChallanNo)
	}
	if r.DepositDate != "2026-08-07" {
		t.Errorf("deposit date %q", r.DepositDate)
	}
	if !r.TaxRupees.Equal(dec2("1000")) || !r.InterestRupees.Equal(dec2("37")) {
		t.Errorf("amounts read back as tax %s interest %s", r.TaxRupees, r.InterestRupees)
	}
	if !r.TotalDepositedRupees.Equal(dec2("1037")) {
		t.Errorf("total deposited %s, want 1037 — the four amounts are separate facts and the "+
			"total is derived from them", r.TotalDepositedRupees)
	}
	// The minor head is NOT defaulted anywhere, including on the way out.
	if r.MinorHead != "200" {
		t.Errorf("minor head read back as %q", r.MinorHead)
	}
	if r.MinorHeadDescription == "" {
		t.Error("the minor head came back with no description; the client is not supposed to hold " +
			"a fourth copy of that vocabulary")
	}
	// What it paid for.
	if r.AllocationCount != 1 || !r.AllocatedTotalRupees.Equal(dec2("1000")) {
		t.Errorf("allocations: count %d total %s, want 1 and 1000 — the interest is not tax and "+
			"must not be in here", r.AllocationCount, r.AllocatedTotalRupees)
	}
	if !r.UnallocatedTaxRupees.IsZero() {
		t.Errorf("unallocated tax %s, want 0", r.UnallocatedTaxRupees)
	}
	if len(r.Allocations) != 1 {
		t.Fatalf("expected one allocation line, got %d", len(r.Allocations))
	}
	a := r.Allocations[0]
	if a.VoucherNo != "P/1042" || a.DeducteeName != "Chitra Consulting" {
		t.Errorf("the register line reads %q / %q; an operator recognises the voucher number and "+
			"the supplier, not a UUID", a.VoucherNo, a.DeducteeName)
	}
	if a.Section != "194C" || a.LegSeq != 0 {
		t.Errorf("allocation key read back as (%s, %d)", a.Section, a.LegSeq)
	}
	if !a.DeductionOnRecord || a.BrokenByAlter {
		t.Errorf("a freshly recorded allocation must match its deduction: on_record=%v broken=%v",
			a.DeductionOnRecord, a.BrokenByAlter)
	}
}

// TestADuplicateCINIsRefusedAndNamesTheFirstEntry.
//
// The same CIN twice is the same real payment twice: it over-claims a deposit in
// the return's batch total, and the FVU fails it against the .csi file at the
// portal rather than here. It must NOT behave like tcs_declaration.go's upsert.
func TestADuplicateCINIsRefusedAndNamesTheFirstEntry(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	v1 := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))
	f2 := oneThousandOn("P/1043")
	v2 := seedDeduction(t, pool, ctx, deductee, f2)

	if _, err := m.RecordTDSChallan(ctx, challanFor(v1, "194C", 0, dec2("1000")), "tester"); err != nil {
		t.Fatalf("record first challan: %v", err)
	}
	// Same branch, same date, same serial — a DIFFERENT deduction underneath,
	// which is what makes this a plausible mistake rather than a silly one.
	_, err := m.RecordTDSChallan(ctx, challanFor(v2, "194C", 0, dec2("1000")), "tester")
	if err == nil {
		t.Fatal("the same CIN was accepted twice. That over-claims a deposit in the return's " +
			"batch total, and it must not be an upsert either: an upsert would silently re-point " +
			"the first challan's allocations")
	}
	if !errors.Is(err, masters.ErrDuplicateTDSChallanCIN) {
		t.Errorf("wrong sentinel, so the HTTP layer cannot answer 409/400 deliberately: %v", err)
	}
	for _, want := range []string{"05122", "2026-08-07", "0004329", "tester"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so the operator cannot find the challan that "+
				"is already there.\ngot: %v", want, err)
		}
	}
	if n := countChallans(t, pool, ctx); n != 1 {
		t.Errorf("%d challans stored, want 1", n)
	}
	if n := countAllocations(t, pool, ctx); n != 1 {
		t.Errorf("%d allocations stored, want 1 — the refused challan must leave nothing behind", n)
	}
}

// TestAnAllocationToNoDeductionIsRefusedAndTheWHOLEChallanRollsBack is the test
// for the constraint the database DOES NOT HAVE.
//
// Migration 0069 carries no foreign key to tds_deduction, deliberately (its rows
// are deleted and re-derived on every voucher ALTER). So nothing but this code
// stops an allocation naming a triple no deduction has — and if it were stored,
// it would be a deposit recorded against nothing.
//
// The second half is the atomicity: the challan is written BEFORE the allocations
// are verified, so a bad allocation must take the challan down with it. A stored
// challan with a missing allocation is the worst outcome available here, because
// there is no allocate-later route to repair it with.
func TestAnAllocationToNoDeductionIsRefusedAndTheWHOLEChallanRollsBack(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	good := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))

	in := challanFor(good, "194C", 0, dec2("1000"))
	in.TaxRupees = dec2("2000")
	// A second allocation naming a section this voucher has no deduction under.
	in.Allocations = append(in.Allocations, masters.TDSChallanAllocationInput{
		VoucherID:    good,
		Section:      "194J-P",
		LegSeq:       0,
		AmountRupees: dec2("1000"),
	})

	_, err := m.RecordTDSChallan(ctx, in, "tester")
	if err == nil {
		t.Fatal("an allocation naming a deduction that does not exist was STORED. Nothing in the " +
			"database would have caught it — 0069 has no FK to tds_deduction — so it would sit " +
			"there as a deposit against nothing, inflating the register and reconciling to no " +
			"deductee row in the return")
	}
	if !errors.Is(err, masters.ErrNoSuchDeduction) {
		t.Errorf("wrong sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "194J-P") {
		t.Errorf("the refusal does not name the section it could not find: %v", err)
	}
	if n := countChallans(t, pool, ctx); n != 0 {
		t.Errorf("%d challans stored after a refused write, want 0. The challan is inserted before "+
			"its allocations are verified, so it MUST roll back with them — and there is no "+
			"allocate-later route that could repair a half-written one", n)
	}
	if n := countAllocations(t, pool, ctx); n != 0 {
		t.Errorf("%d allocations stored, want 0", n)
	}
}

// TestAPartialAllocationIsRefusedAtEntry is the decision that CONTRADICTS
// migration 0069's own header, and it is here so that the contradiction is
// visible in a test name.
//
// 0069 lines 203-210 say a part payment is "an allocation smaller than the
// deduction". reporting/tds_return_source.go:351 raises
// ErrAllocationBrokenByAlter on ANY inequality, so a deliberately partial
// allocation would refuse the whole quarter at filing time with a message about
// a voucher alter that never happened. Refusing here, where the operator holds
// the challan, is the recoverable direction.
func TestAPartialAllocationIsRefusedAtEntry(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	voucher := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))

	// ₹600 deposited against a ₹1,000 deduction.
	in := challanFor(voucher, "194C", 0, dec2("1000"))
	in.Allocations[0].AmountRupees = dec2("600")

	_, err := m.RecordTDSChallan(ctx, in, "tester")
	if err == nil {
		t.Fatal("a partial allocation was stored. At filing time reporting would call it " +
			"ErrAllocationBrokenByAlter — 'the voucher has been altered since the tax was paid' — " +
			"about a voucher nobody touched, and refuse the entire quarter")
	}
	if !errors.Is(err, masters.ErrTDSAllocationMismatch) {
		t.Errorf("wrong sentinel: %v", err)
	}
	// BOTH numbers and the voucher, because "amounts do not match" sends the
	// operator to compare two figures they cannot see.
	for _, want := range []string{"1000", "600", "P/1042", "194C"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if n := countChallans(t, pool, ctx); n != 0 {
		t.Errorf("%d challans stored, want 0", n)
	}
}

// TestAnOverAllocationIsRefused is the other direction of the same equality: a
// deposit of MORE than was deducted claims tax that was never withheld from
// anybody.
func TestAnOverAllocationIsRefused(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	voucher := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))

	in := challanFor(voucher, "194C", 0, dec2("1500"))
	_, err := m.RecordTDSChallan(ctx, in, "tester")
	if err == nil {
		t.Fatal("an allocation LARGER than the deduction was stored; the return would then claim " +
			"a deposit of tax that was never deducted from the deductee")
	}
	if !errors.Is(err, masters.ErrTDSAllocationMismatch) {
		t.Errorf("wrong sentinel: %v", err)
	}
}

// TestADeductionAlreadyPaidByAnotherChallanIsRefusedByName.
//
// Deductee Detail field 5 is a SINGLE challan reference, so a deduction paid by
// two challans is not expressible in the return at all. The unique constraint
// tds_challan_allocation_one_per_deduction is the authority; the point of this
// test is that the operator is told WHICH challan already paid it.
func TestADeductionAlreadyPaidByAnotherChallanIsRefusedByName(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	voucher := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))

	if _, err := m.RecordTDSChallan(ctx, challanFor(voucher, "194C", 0, dec2("1000")),
		"tester"); err != nil {
		t.Fatalf("record first challan: %v", err)
	}
	// A different CIN — so this is NOT the duplicate-CIN refusal — paying the
	// same deduction a second time.
	second := challanFor(voucher, "194C", 0, dec2("1000"))
	second.ChallanNo = "05123"
	_, err := m.RecordTDSChallan(ctx, second, "tester")
	if err == nil {
		t.Fatal("one deduction was allocated to two challans; the return has exactly one field " +
			"for the challan reference, so the second is unfilable")
	}
	if !errors.Is(err, masters.ErrDeductionAlreadyPaid) {
		t.Errorf("wrong sentinel: %v", err)
	}
	for _, want := range []string{"05122", "2026-08-07", "P/1042"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name the challan that already paid it (%q): %v",
				want, err)
		}
	}
	if n := countChallans(t, pool, ctx); n != 1 {
		t.Errorf("%d challans stored, want 1", n)
	}
}

// TestAZeroTaxDeductionCannotBeAllocated.
//
// A sub-threshold payment is a FACT and is REPORTED — the s.393(1) aggregate is
// of amounts PAID and Annexure 6 flag Y reports a below-threshold payment — but
// no tax was deducted on it, so no deposit can have paid it. reporting routes
// every zero-tax row to the SYNTHESISED nil challan regardless of any
// allocation, so an allocation here would be a payment of nothing recorded
// against a real deposit slip.
func TestAZeroTaxDeductionCannotBeAllocated(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Small Contractor")
	f := oneThousandOn("P/1050")
	f.Base = dec2("20000")
	f.Tax = decimal.Zero
	voucher := seedDeduction(t, pool, ctx, deductee, f)

	in := challanFor(voucher, "194C", 0, dec2("1000"))
	in.Allocations[0].AmountRupees = decimal.Zero
	// The challan itself still deposited something, so this is not the
	// nil-challan refusal.
	_, err := m.RecordTDSChallan(ctx, in, "tester")
	if err == nil {
		t.Fatal("a deduction of zero tax was allocated to a real deposit")
	}
	if !errors.Is(err, masters.ErrTDSAllocationMismatch) {
		t.Errorf("wrong sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "nil challan") {
		t.Errorf("the refusal does not say where a sub-threshold payment IS reported, which is "+
			"the operator's next question: %v", err)
	}
}

// TestADeductionFromAnotherTaxYearCannotBeAllocated is the refusal with NO
// constraint behind it at all — not even a CHECK, because the two tax years sit
// on different tables and the link table carries neither.
//
// A challan belongs to ONE tax year, which is exactly why 0069 stores the year
// rather than deriving it from the deposit date.
func TestADeductionFromAnotherTaxYearCannotBeAllocated(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	f := oneThousandOn("P/0042")
	f.TaxYearStart = 2025
	f.Date = "2026-03-20"
	voucher := seedDeduction(t, pool, ctx, deductee, f)

	// A challan paid on 7 April, recorded against 2026-27, paying a March
	// deduction that belongs to 2025-26. The DATE is plausible — this is the
	// exact case 0069 lines 109-113 describe — and the YEAR on the challan is
	// the thing that is wrong.
	in := challanFor(voucher, "194C", 0, dec2("1000"))
	in.DepositDate = "2026-04-07"
	in.TaxYearStart = 2026

	_, err := m.RecordTDSChallan(ctx, in, "tester")
	if err == nil {
		t.Fatal("a 2025-26 deduction was paid by a challan recorded against 2026-27; the deductee " +
			"row would then be filed under a challan the department holds against another year")
	}
	if !errors.Is(err, masters.ErrTDSAllocationMismatch) {
		t.Errorf("wrong sentinel: %v", err)
	}
	for _, want := range []string{"2025", "2026"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name both years: %v", err)
		}
	}

	// The control: recorded against the year it actually belongs to, it is
	// accepted — INCLUDING the 7 April deposit date, which must not be used to
	// derive the year.
	in.TaxYearStart = 2025
	if _, err := m.RecordTDSChallan(ctx, in, "tester"); err != nil {
		t.Fatalf("a challan paid on 7 April 2026 for March 2025-26 is the ordinary case and must "+
			"be accepted: %v", err)
	}
	rows, err := m.ListTDSChallans(ctx, 2025)
	if err != nil {
		t.Fatalf("list 2025-26: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected the challan under 2025-26 (the year of the TAX), got %d rows", len(rows))
	}
	later, err := m.ListTDSChallans(ctx, 2026)
	if err != nil {
		t.Fatalf("list 2026-27: %v", err)
	}
	if len(later) != 0 {
		t.Errorf("the challan also appears under 2026-27, the year its DEPOSIT DATE falls in — the "+
			"list must read the stored year, not the date (got %d rows)", len(later))
	}
}

// TestAnAlteredVoucherMakesItsAllocationVisiblyBroken is 0045's mechanism read
// from the register instead of from the return.
//
// Two stages, because they are two different failures:
//
//  1. the deduction still exists and its amount MOVED — the allocation no longer
//     matches what was deposited against it;
//  2. the deduction is GONE (ledger/tds.go deletes every row for the voucher and
//     re-derives; if the alter removes the TDS leg, nothing comes back) — and
//     the allocation SURVIVES, because there is no foreign key. It must remain
//     visible: it records a payment really made to the Government.
//
// The list's flag is ADVISORY; reporting refuses the quarter on the same test.
func TestAnAlteredVoucherMakesItsAllocationVisiblyBroken(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	voucher := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))
	if _, err := m.RecordTDSChallan(ctx, challanFor(voucher, "194C", 0, dec2("1000")),
		"tester"); err != nil {
		t.Fatalf("record challan: %v", err)
	}

	// Stage 1: the amount moves under the recorded payment. (ledger/tds.go would
	// DELETE and re-INSERT; the observable end state is the same and this test is
	// about the observation, not the mechanism.)
	exec(t, pool, ctx, `UPDATE tds_deduction SET tds_amount_rupees = 800
	                     WHERE voucher_id = $1`, voucher)
	rows, err := m.ListTDSChallans(ctx, 2026)
	if err != nil {
		t.Fatalf("list challans: %v", err)
	}
	if len(rows) != 1 || len(rows[0].Allocations) != 1 {
		t.Fatalf("expected one challan with one allocation, got %d/%v", len(rows), rows)
	}
	a := rows[0].Allocations[0]
	if !a.DeductionOnRecord {
		t.Error("the deduction still exists; DeductionOnRecord must be true")
	}
	if !a.BrokenByAlter {
		t.Error("the deduction now shows 800 against a recorded deposit of 1000 and the register " +
			"calls the allocation intact. The return will refuse the whole quarter for this, so " +
			"the register is where it has to be visible first")
	}
	if !a.DeductionTaxRupees.Equal(dec2("800")) || !a.AllocatedRupees.Equal(dec2("1000")) {
		t.Errorf("the register shows deduction %s against allocated %s; both numbers have to be "+
			"there or the operator cannot see which way the discrepancy runs",
			a.DeductionTaxRupees, a.AllocatedRupees)
	}

	// Stage 2: the leg disappears entirely.
	exec(t, pool, ctx, `DELETE FROM tds_deduction WHERE voucher_id = $1`, voucher)
	rows, err = m.ListTDSChallans(ctx, 2026)
	if err != nil {
		t.Fatalf("list challans: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("the CHALLAN must survive the deduction being deleted: got %d rows", len(rows))
	}
	if len(rows[0].Allocations) != 1 {
		t.Fatalf("the allocation was dropped from the list when its deduction went. It is the "+
			"record of a payment really made to the Government, and 0069 has no foreign key "+
			"precisely so that it survives — got %d allocation lines", len(rows[0].Allocations))
	}
	a = rows[0].Allocations[0]
	if a.DeductionOnRecord {
		t.Error("DeductionOnRecord is true for a deduction that has been deleted")
	}
	if !a.BrokenByAlter {
		t.Error("an allocation whose deduction no longer exists is the worst kind of broken and " +
			"must be flagged")
	}
	if !a.AllocatedRupees.Equal(dec2("1000")) {
		t.Errorf("the amount that was deposited must still be readable: %s", a.AllocatedRupees)
	}
	// The allocated total still counts it: money left the bank.
	if !rows[0].AllocatedTotalRupees.Equal(dec2("1000")) {
		t.Errorf("allocated total %s, want 1000", rows[0].AllocatedTotalRupees)
	}
}

// TestTwoLegsOfOneVoucherArePaidSeparately. leg_seq exists because a
// consolidated contractor payment carries two deductions of the SAME section
// against the same deductee (migration 0050 line 63).
func TestTwoLegsOfOneVoucherArePaidSeparately(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	f := oneThousandOn("P/1042")
	voucher := seedDeduction(t, pool, ctx, deductee, f)
	// A second leg on the SAME voucher, same section, different amount.
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		companyID, _ := db.CompanyFromContext(ctx)
		_, err := tx.Exec(ctx,
			`INSERT INTO tds_deduction
			   (id, company_id, voucher_id, leg_seq, deductee_ledger_id, section,
			    deductee_type, tax_year_start, deduction_date, taxable_base_rupees,
			    rate_pct, tds_amount_rupees, return_code, pan_available,
			    ruleset_version, recorded_by)
			 VALUES ($1, $2, $3, 1, $4, '194C', 'OTHER', 2026, DATE '2026-07-10',
			         30000, 2, 600, '1024', true, 'tds-2026-04', 'tester')`,
			uuid.New(), companyID, voucher, deductee)
		return err
	})
	if err != nil {
		t.Fatalf("seed second leg: %v", err)
	}

	in := challanFor(voucher, "194C", 0, dec2("1600"))
	in.Allocations[0].AmountRupees = dec2("1000")
	in.Allocations = append(in.Allocations, masters.TDSChallanAllocationInput{
		VoucherID: voucher, Section: "194C", LegSeq: 1, AmountRupees: dec2("600"),
	})
	if _, err := m.RecordTDSChallan(ctx, in, "tester"); err != nil {
		t.Fatalf("one challan paying two legs of one voucher must be accepted — that is what "+
			"leg_seq is for: %v", err)
	}
	rows, err := m.ListTDSChallans(ctx, 2026)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].AllocationCount != 2 {
		t.Fatalf("expected one challan with two allocations, got %d challans", len(rows))
	}
	if !rows[0].AllocatedTotalRupees.Equal(dec2("1600")) {
		t.Errorf("allocated total %s, want 1600", rows[0].AllocatedTotalRupees)
	}
	if !rows[0].UnallocatedTaxRupees.IsZero() {
		t.Errorf("unallocated %s, want 0", rows[0].UnallocatedTaxRupees)
	}
}

// TestAChallanIsInvisibleToAnotherCompany. The company comes from the CONTEXT and
// RLS supplies the tenancy; nothing in this package writes a WHERE company_id for
// it. This is the test that fails if anybody ever "helpfully" adds one — or
// removes the GUC.
func TestAChallanIsInvisibleToAnotherCompany(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	voucher := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))
	if _, err := m.RecordTDSChallan(ctx, challanFor(voucher, "194C", 0, dec2("1000")),
		"tester"); err != nil {
		t.Fatalf("record challan: %v", err)
	}

	// A second tenant on the same pool. testdb.Cleanup is registered the moment
	// the company exists — internal/testhygiene fails the build otherwise.
	other := uuid.New()
	octx := db.WithCompany(context.Background(), other)
	if _, err := identity.NewService(pool).CreateCompany(octx, identity.Company{
		ID: other, Name: "Other Co " + other.String()[:8], FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create second company: %v", err)
	}
	testdb.Cleanup(t, other)

	rows, err := m.ListTDSChallans(octx, 2026)
	if err != nil {
		t.Fatalf("list for the other company: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("company %s can see %d of another company's tax deposits", other, len(rows))
	}

	// And with NO company in context at all, both halves refuse BEFORE any SQL
	// is issued.
	bare := context.Background()
	if _, err := m.RecordTDSChallan(bare, challanFor(voucher, "194C", 0, dec2("1000")),
		"tester"); !errors.Is(err, db.ErrNoCompany) {
		t.Errorf("RecordTDSChallan with no company in context: %v, want db.ErrNoCompany", err)
	}
	if _, err := m.ListTDSChallans(bare, 2026); !errors.Is(err, db.ErrNoCompany) {
		t.Errorf("ListTDSChallans with no company in context: %v, want db.ErrNoCompany", err)
	}
}

// TestRecordedByIsRequired. recorded_by is NOT NULL on both tables and it is the
// only audit trail a challan has: who keyed this deposit.
func TestRecordedByIsRequired(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	voucher := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))

	_, err := m.RecordTDSChallan(ctx, challanFor(voucher, "194C", 0, dec2("1000")), "")
	if !errors.Is(err, masters.ErrInvalidTDSChallan) {
		t.Errorf("a challan with no recorded_by: %v, want ErrInvalidTDSChallan", err)
	}
	if n := countChallans(t, pool, ctx); n != 0 {
		t.Errorf("%d challans stored, want 0", n)
	}
}

// TestTheListIsOrderedByDepositDate — the return walks challans in deposit order
// and the challan record numbers follow it, so a register that showed them in
// another order would not match the file that was filed.
func TestTheListIsOrderedByDepositDate(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	dates := []string{"2026-09-07", "2026-07-07", "2026-08-07"}
	for i, date := range dates {
		f := oneThousandOn("P/200" + string(rune('0'+i)))
		voucher := seedDeduction(t, pool, ctx, deductee, f)
		in := challanFor(voucher, "194C", 0, dec2("1000"))
		in.DepositDate = date
		in.ChallanNo = "0512" + string(rune('0'+i))
		if _, err := m.RecordTDSChallan(ctx, in, "tester"); err != nil {
			t.Fatalf("record challan for %s: %v", date, err)
		}
	}
	rows, err := m.ListTDSChallans(ctx, 2026)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected three challans, got %d", len(rows))
	}
	want := []string{"2026-07-07", "2026-08-07", "2026-09-07"}
	for i, w := range want {
		if rows[i].DepositDate != w {
			t.Errorf("challan %d is dated %s, want %s (earliest deposit first)",
				i, rows[i].DepositDate, w)
		}
	}
}

// exec runs one statement in the tenant-scoped transaction. Used only to
// SIMULATE what ledger/tds.go does to a deduction on ALTER.
func exec(t *testing.T, pool *db.Pool, ctx context.Context, sql string, args ...any) {
	t.Helper()
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, sql, args...)
		return err
	})
	if err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// ===========================================================================
// REGISTER ROW D1 — THE ALLOCATE-LATER
// ===========================================================================
// A challan may legitimately be recorded naming fewer deductions than it paid
// for: nothing here requires the allocations to COVER the tax. Before
// AllocateToRecordedChallan the remainder could then never be attached, because
// the create was the only writer and the CIN cannot be entered twice. The parity
// audit drove it — seven doors in the browser, six HTTP methods, and challan
// 05130's unallocated balance stayed at exactly 12000.0000 — while
// reporting.Form140StatementFor refused the unattached deduction with
// ErrTaxNotDeposited. Tax that HAD been paid was permanently unfilable.

// TestAllocatingLaterMakesTheUnallocatedBalanceFALL is the row's own oracle, and
// it is asserted on the BOOKS rather than on a return value: the allocation
// rows, the deductee each one names, and the balance that could never move.
func TestAllocatingLaterMakesTheUnallocatedBalanceFALL(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	// ONE deposit of ₹2,000 that really paid TWO deductions of ₹1,000.
	v1 := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))
	v2 := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1043"))

	in := challanFor(v1, "194C", 0, dec2("1000"))
	in.TaxRupees = dec2("2000")
	challanID, err := m.RecordTDSChallan(ctx, in, "tester")
	if err != nil {
		t.Fatalf("record challan: %v", err)
	}

	// The state the audit found and could not leave.
	before := oneChallan(t, m, ctx, challanID)
	if !before.UnallocatedTaxRupees.Equal(dec2("1000")) {
		t.Fatalf("unallocated before = %s, want 1000 — the fixture is not the shape this test is "+
			"about", before.UnallocatedTaxRupees)
	}

	taxYear, err := m.AllocateToRecordedChallan(ctx, challanID, []masters.TDSChallanAllocationInput{{
		VoucherID:    v2,
		Section:      "194C",
		LegSeq:       0,
		AmountRupees: dec2("1000"),
	}}, "tester")
	if err != nil {
		t.Fatalf("allocate later: %v", err)
	}
	if taxYear != 2026 {
		t.Errorf("allocate returned tax year %d, want 2026 — the caller reads the register back "+
			"with it, and the year is the challan's STORED one and not the caller's guess", taxYear)
	}

	// ⛔ READ THE BOOKS BACK. A status (or a nil error) that changed nothing is
	// this repo's signature failure, so assert the LEDGER NAMES and the sides:
	// which deductions this deposit now covers, and by how much.
	after := oneChallan(t, m, ctx, challanID)
	if after.AllocationCount != 2 {
		t.Fatalf("the challan carries %d allocation(s) after allocating later, want 2",
			after.AllocationCount)
	}
	if !after.AllocatedTotalRupees.Equal(dec2("2000")) {
		t.Errorf("allocated total = %s, want 2000", after.AllocatedTotalRupees)
	}
	if !after.UnallocatedTaxRupees.IsZero() {
		t.Errorf("unallocated after = %s, want 0 — THIS IS THE ROW'S ORACLE: the balance must "+
			"FALL. It stayed at 12000.0000 through seven browser doors and six HTTP methods "+
			"because no route existed to move it", after.UnallocatedTaxRupees)
	}
	seenVouchers := map[string]decimal.Decimal{}
	for _, a := range after.Allocations {
		seenVouchers[a.VoucherNo] = a.AllocatedRupees
		if a.DeducteeName != "Chitra Consulting" {
			t.Errorf("allocation on %s names %q; an operator recognises the supplier, not a UUID",
				a.VoucherNo, a.DeducteeName)
		}
		if a.BrokenByAlter {
			t.Errorf("allocation on %s reads BROKEN BY ALTER the moment it was made", a.VoucherNo)
		}
		if a.Kind != masters.AllocationKindTDS {
			t.Errorf("allocation on %s reads kind %q, want TDS", a.VoucherNo, a.Kind)
		}
	}
	for _, want := range []string{"P/1042", "P/1043"} {
		got, ok := seenVouchers[want]
		if !ok {
			t.Errorf("the challan does not name voucher %s; the deposit that paid its tax is what "+
				"Deductee Detail field 5 has to point at", want)
			continue
		}
		if !got.Equal(dec2("1000")) {
			t.Errorf("voucher %s is allocated %s, want 1000", want, got)
		}
	}
}

// TestAllocatingLaterCannotOverrunTheDeposit is register row D2 on the SECOND
// write path, and it is the reason the cap is checked inside the transaction
// rather than only in the pure validator: the sum that matters includes rows
// that are already stored.
func TestAllocatingLaterCannotOverrunTheDeposit(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	v1 := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))
	v2 := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1043"))

	// A deposit of exactly ₹1,000, fully allocated at entry.
	challanID, err := m.RecordTDSChallan(ctx, challanFor(v1, "194C", 0, dec2("1000")), "tester")
	if err != nil {
		t.Fatalf("record challan: %v", err)
	}

	_, err = m.AllocateToRecordedChallan(ctx, challanID, []masters.TDSChallanAllocationInput{{
		VoucherID:    v2,
		Section:      "194C",
		LegSeq:       0,
		AmountRupees: dec2("1000"),
	}}, "tester")
	if err == nil {
		t.Fatal("a ₹1,000 deposit was allowed to pay ₹2,000 of tax. Both deductions would then " +
			"read as already deposited, so the REAL second deposit could never be allocated to " +
			"the second one — which is the trap D2 recorded, reached through the new route")
	}
	if !errors.Is(err, masters.ErrChallanOverAllocated) {
		t.Errorf("wrong sentinel, so the HTTP layer cannot answer 400 deliberately: %v", err)
	}
	// ATOMICITY: the refused request left nothing behind.
	if n := countAllocations(t, pool, ctx); n != 1 {
		t.Errorf("%d allocations stored after a refused allocate-later, want 1", n)
	}
	after := oneChallan(t, m, ctx, challanID)
	if !after.UnallocatedTaxRupees.IsZero() || after.AllocationCount != 1 {
		t.Errorf("the challan moved: count %d unallocated %s, want 1 and 0",
			after.AllocationCount, after.UnallocatedTaxRupees)
	}
}

// TestAllocatingToAChallanThatIsNotThereIsANotFound. The request is well formed
// and names a deposit this company does not have, so it is a 404 and not a 400 —
// and ⚠️ another tenant's challan is indistinguishable from a missing one, which
// is the intended answer under RLS: the alternative leaks whether a CIN exists
// somewhere else.
func TestAllocatingToAChallanThatIsNotThereIsANotFound(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	v1 := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))

	_, err := m.AllocateToRecordedChallan(ctx, uuid.New(), []masters.TDSChallanAllocationInput{{
		VoucherID:    v1,
		Section:      "194C",
		LegSeq:       0,
		AmountRupees: dec2("1000"),
	}}, "tester")
	if !errors.Is(err, masters.ErrNotFound) {
		t.Fatalf("allocating to a challan that does not exist = %v, want masters.ErrNotFound so "+
			"the HTTP layer answers 404", err)
	}
	if n := countAllocations(t, pool, ctx); n != 0 {
		t.Errorf("%d allocations stored against a challan that does not exist", n)
	}
}

// TestAllocatingLaterRefusesADeductionAnotherChallanAlreadyPaid. The new route
// must not become a way around the one-challan-per-deduction rule — Deductee
// Detail field 5 is a SINGLE challan reference — and the refusal has to NAME the
// challan that already paid it, because the operator's next question is "then
// what is this second slip in my hand".
func TestAllocatingLaterRefusesADeductionAnotherChallanAlreadyPaid(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	v1 := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))
	v2 := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1043"))

	// Challan 05122 pays P/1042.
	if _, err := m.RecordTDSChallan(ctx, challanFor(v1, "194C", 0, dec2("1000")), "tester"); err != nil {
		t.Fatalf("record first challan: %v", err)
	}
	// A genuinely different deposit slip with ₹1,000 of room on it, paying P/1043.
	second := challanFor(v2, "194C", 0, dec2("1000"))
	second.ChallanNo = "05123"
	second.TaxRupees = dec2("2000")
	secondID, err := m.RecordTDSChallan(ctx, second, "tester")
	if err != nil {
		t.Fatalf("record the second challan: %v", err)
	}

	_, err = m.AllocateToRecordedChallan(ctx, secondID, []masters.TDSChallanAllocationInput{{
		VoucherID: v1, Section: "194C", LegSeq: 0, AmountRupees: dec2("1000"),
	}}, "tester")
	if !errors.Is(err, masters.ErrDeductionAlreadyPaid) {
		t.Fatalf("allocate-later paid a deduction a second time = %v, want ErrDeductionAlreadyPaid "+
			"so the HTTP layer answers 409: a deduction split across two challans is not "+
			"expressible in the return at all", err)
	}
	for _, want := range []string{"05122", "0004329", "P/1042"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so the operator cannot find the deposit that "+
				"already paid it.\ngot: %v", want, err)
		}
	}
	if n := countAllocations(t, pool, ctx); n != 2 {
		t.Errorf("%d allocations stored, want 2 — the refused request must leave nothing behind", n)
	}
}

// TestAllocatingToAnInterestOnlyChallanIsRefused. Only the TAX component
// discharges a deduction liability (migration 0069 lines 90-96), so an
// interest-, fee- or penalty-only deposit has nothing on it to allocate. The
// create path has always refused allocations on such a challan; the new route
// has to make the same call, or it becomes the door around it.
func TestAllocatingToAnInterestOnlyChallanIsRefused(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	v1 := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))

	interestOnly := challanFor(v1, "194C", 0, dec2("1000"))
	interestOnly.ChallanNo = "05130"
	interestOnly.TaxRupees = decimal.Zero
	interestOnly.InterestRupees = dec2("500")
	interestOnly.Allocations = nil // a no-tax challan may allocate to nothing
	challanID, err := m.RecordTDSChallan(ctx, interestOnly, "tester")
	if err != nil {
		t.Fatalf("record the interest-only challan: %v", err)
	}

	_, err = m.AllocateToRecordedChallan(ctx, challanID, []masters.TDSChallanAllocationInput{{
		VoucherID: v1, Section: "194C", LegSeq: 0, AmountRupees: dec2("1000"),
	}}, "tester")
	if !errors.Is(err, masters.ErrInvalidTDSChallan) {
		t.Fatalf("allocating to a challan that deposited NO TAX = %v, want ErrInvalidTDSChallan: "+
			"s.396 interest is paid on the same slip and is not tax, so it discharges nothing", err)
	}
	if n := countAllocations(t, pool, ctx); n != 0 {
		t.Errorf("%d allocations stored against a deposit that paid no tax", n)
	}
}

// oneChallan reads one challan back out of the register by id. It goes through
// ListTDSChallans deliberately: that is the read a screen makes, so a test that
// used a private read could pass while the register showed something else.
func oneChallan(t *testing.T, m *masters.Service, ctx context.Context,
	id uuid.UUID) masters.TDSChallanRecord {
	t.Helper()
	rows, err := m.ListTDSChallans(ctx, 2026)
	if err != nil {
		t.Fatalf("list challans: %v", err)
	}
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("challan %s is not in the 2026-27 register", id)
	return masters.TDSChallanRecord{}
}

// ===========================================================================
// REGISTER ROW D4 — A COLLECTOR CAN RECORD THAT IT DEPOSITED WHAT IT COLLECTED
// ===========================================================================
// tcs_collection (migration 0067) was not part of this mechanism at all. The
// audit drove it: a live 2000.0000 collection under 206C(1)-SCRAP appeared in NO
// allocation picker and could be attached to no challan, so the collection side
// of T6 could record what was collected and never record that it was paid over.

// seedCollection writes one voucher and one TCS collection on it, returning the
// VOUCHER id — the same identity an allocation keys on, for the same reason:
// internal/ledger re-derives collection rows with fresh ids on every ALTER.
//
// Seeded by SQL for the reason the whole file is (masters may not import
// internal/ledger). ⚠️ declaration_outcome is 'NONE' and the amount is positive,
// because 0067's CHECKs make an EXEMPT row collect nothing — and a zero
// collection is precisely what may NOT be deposited.
func seedCollection(t *testing.T, pool *db.Pool, ctx context.Context,
	buyerID uuid.UUID, voucherNo, section string, legSeq int, amount decimal.Decimal) uuid.UUID {
	t.Helper()
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		t.Fatal("seedCollection needs a company-scoped context")
	}
	voucherID := uuid.New()
	versionID := uuid.New()
	const date = "2026-07-12"

	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO voucher_version
			   (id, company_id, voucher_id, version_no, op, hash, payload,
			    created_by, voucher_no, voucher_date)
			 VALUES ($1, $2, $3, 1, 'CREATE', '\x00'::bytea, '{}'::jsonb,
			         'tester', $4, $5::date)`,
			versionID, companyID, voucherID, voucherNo, date); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO voucher_head
			   (company_id, voucher_id, current_version_id, version_no, op, voucher_date)
			 VALUES ($1, $2, $3, 1, 'CREATE', $4::date)`,
			companyID, voucherID, versionID, date); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO tcs_collection
			   (id, company_id, voucher_id, leg_seq, buyer_ledger_id, section,
			    tax_year_start, collection_date, taxable_base_rupees, rate_pct,
			    tcs_amount_rupees, pan_available, declaration_outcome,
			    ruleset_version, recorded_by)
			 VALUES ($1, $2, $3, $4, $5, $6, 2026, $7::date, $8, 2, $9, true, 'NONE',
			         'tcs-2026-04', 'tester')`,
			uuid.New(), companyID, voucherID, legSeq, buyerID, section, date,
			amount.Mul(dec2("50")), amount)
		return err
	})
	if err != nil {
		t.Fatalf("seed collection on voucher %s: %v", voucherNo, err)
	}
	return voucherID
}

// TestACollectionCanBeOfferedAndDeposited is the row's oracle end to end: the
// collection appears in what a challan could pay for, a deposit can name it, and
// the register reads it back as a COLLECTION rather than as a deduction broken
// by an alter.
func TestACollectionCanBeOfferedAndDeposited(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	buyer, err := m.CreateLedger(ctx, masters.Ledger{
		Name: "Scrap Buyer", GroupName: "Sundry Debtors",
	}, "tester")
	if err != nil {
		t.Fatalf("create buyer: %v", err)
	}
	v := seedCollection(t, pool, ctx, buyer, "S/2201", "206C(1)-SCRAP", 0, dec2("2000"))

	// 1. IT IS OFFERED. The audit's exact figure: a live 2000.0000 collection
	//    under 206C(1)-SCRAP that appeared in no picker.
	allocatable, err := m.ListAllocatableDeductions(ctx, 2026)
	if err != nil {
		t.Fatalf("list allocatable: %v", err)
	}
	var found *masters.AllocatableDeduction
	for i := range allocatable {
		if allocatable[i].VoucherNo == "S/2201" {
			found = &allocatable[i]
		}
	}
	if found == nil {
		t.Fatalf("the 2000.0000 collection under 206C(1)-SCRAP is not offered as allocatable, so "+
			"no deposit can name it and a collector can never record that it paid the tax over. "+
			"Got %d row(s)", len(allocatable))
	}
	if found.Kind != masters.AllocationKindTCS {
		t.Errorf("the collection is offered as kind %q, want TCS — a screen that captions it as a "+
			"deduction names the wrong party as the one taxed", found.Kind)
	}
	if found.DeducteeName != "Scrap Buyer" {
		t.Errorf("the collection names %q; on a collection the party is the BUYER it was collected "+
			"FROM", found.DeducteeName)
	}
	if !found.TaxRupees.Equal(dec2("2000")) {
		t.Errorf("the collection offers %s to allocate, want 2000", found.TaxRupees)
	}
	if found.AlreadyPaid {
		t.Error("a collection with no challan against it reads as already paid")
	}

	// 2. IT CAN BE DEPOSITED.
	in := challanFor(v, "206C(1)-SCRAP", 0, dec2("2000"))
	in.ChallanNo = "05140"
	in.Allocations[0].Kind = masters.AllocationKindTCS
	challanID, err := m.RecordTDSChallan(ctx, in, "tester")
	if err != nil {
		t.Fatalf("record the collection's deposit: %v", err)
	}

	// 3. AND IT READS BACK AS A COLLECTION. ⛔ Before the register learned about
	//    tcs_collection this line came back deduction_on_record=false, i.e.
	//    BROKEN BY ALTER — telling the operator a voucher change had invalidated
	//    a deposit that was made minutes ago and never touched since.
	c := oneChallan(t, m, ctx, challanID)
	if len(c.Allocations) != 1 {
		t.Fatalf("the deposit carries %d allocation(s), want 1", len(c.Allocations))
	}
	a := c.Allocations[0]
	if a.Kind != masters.AllocationKindTCS {
		t.Errorf("the stored allocation reads kind %q, want TCS", a.Kind)
	}
	if !a.DeductionOnRecord || a.BrokenByAlter {
		t.Errorf("a freshly recorded collection deposit reads on_record=%v broken=%v — the "+
			"register must not tell an operator an alter invalidated a deposit nobody touched",
			a.DeductionOnRecord, a.BrokenByAlter)
	}
	if a.DeducteeName != "Scrap Buyer" || a.VoucherNo != "S/2201" {
		t.Errorf("the register line reads %q / %q", a.VoucherNo, a.DeducteeName)
	}
	if !c.UnallocatedTaxRupees.IsZero() {
		t.Errorf("unallocated = %s, want 0", c.UnallocatedTaxRupees)
	}

	// 4. AND THE SAME COLLECTION CANNOT BE DEPOSITED TWICE.
	if !allocatableSaysPaid(t, m, ctx, "S/2201") {
		t.Error("the collection is not flagged already_paid after its deposit was recorded, so an " +
			"operator would tick it again and collect a 409")
	}
}

// TestAChallanCannotPayADeductionAndACollection is the mixing refusal against
// what is ALREADY STORED — the half validateAllocationInputs cannot see.
//
// It matters because reporting reaches a challan THROUGH tds_deduction and files
// the challan's WHOLE tax as the deduction return's challan total, so a mixed
// deposit over-states that total by the collection half and the FVU fails it at
// the portal.
func TestAChallanCannotPayADeductionAndACollection(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	deductee := seedDeductee(t, m, ctx, "Chitra Consulting")
	v1 := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1042"))
	buyer, err := m.CreateLedger(ctx, masters.Ledger{
		Name: "Scrap Buyer", GroupName: "Sundry Debtors",
	}, "tester")
	if err != nil {
		t.Fatalf("create buyer: %v", err)
	}
	v2 := seedCollection(t, pool, ctx, buyer, "S/2201", "206C(1)-SCRAP", 0, dec2("2000"))

	// A deposit of ₹3,000 that names the DEDUCTION, with room left on it.
	in := challanFor(v1, "194C", 0, dec2("1000"))
	in.TaxRupees = dec2("3000")
	challanID, err := m.RecordTDSChallan(ctx, in, "tester")
	if err != nil {
		t.Fatalf("record challan: %v", err)
	}

	_, err = m.AllocateToRecordedChallan(ctx, challanID, []masters.TDSChallanAllocationInput{{
		VoucherID:    v2,
		Section:      "206C(1)-SCRAP",
		LegSeq:       0,
		Kind:         masters.AllocationKindTCS,
		AmountRupees: dec2("2000"),
	}}, "tester")
	if !errors.Is(err, masters.ErrMixedChallanKinds) {
		t.Fatalf("a collection was attached to a deposit that pays deductions = %v, want "+
			"ErrMixedChallanKinds", err)
	}
	if n := countAllocations(t, pool, ctx); n != 1 {
		t.Errorf("%d allocations stored, want 1 — the refused request must leave nothing behind", n)
	}
	// The CONTROL: the same deposit still accepts another DEDUCTION, so the rule
	// refuses mixing rather than refusing everything.
	v3 := seedDeduction(t, pool, ctx, deductee, oneThousandOn("P/1044"))
	if _, err := m.AllocateToRecordedChallan(ctx, challanID, []masters.TDSChallanAllocationInput{{
		VoucherID: v3, Section: "194C", LegSeq: 0, AmountRupees: dec2("1000"),
	}}, "tester"); err != nil {
		t.Fatalf("the mixing rule ate the feature: a second DEDUCTION on the same deposit was "+
			"refused too: %v", err)
	}
}

// allocatableSaysPaid answers whether the picker flags one voucher's row as
// already deposited. SERVED rather than filtered, so an operator who cannot find
// a row learns WHICH reason applies.
func allocatableSaysPaid(t *testing.T, m *masters.Service, ctx context.Context,
	voucherNo string) bool {
	t.Helper()
	rows, err := m.ListAllocatableDeductions(ctx, 2026)
	if err != nil {
		t.Fatalf("list allocatable: %v", err)
	}
	for _, r := range rows {
		if r.VoucherNo == voucherNo {
			return r.AlreadyPaid
		}
	}
	t.Fatalf("voucher %s is not offered at all", voucherNo)
	return false
}
