package masters_test

// THE DB-BACKED HALF OF THE CHALLAN WRITE PATH.
//
// Everything here needs Postgres, because everything here is about a rule that
// the pure validator CANNOT check: whether the deduction an allocation names
// actually exists, whether it still shows the amount that was deposited against
// it, and whether the whole write is atomic when one allocation of five is bad.
//
// ⚠️ NOT RUN IN THE AUTHORING SESSION. Every worktree on this machine shares one
// Postgres on :5433 and another process held it, so these tests were WRITTEN and
// type-checked (go vet compiles test files) and NOT EXECUTED. They are part of
// the deliverable and the first session with the database to itself must run
// them; nothing below has been observed to pass. Treat a failure here as an
// unverified claim in tds_challan.go, not as flake.
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
