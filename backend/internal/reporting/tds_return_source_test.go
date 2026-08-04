package reporting_test

// SOURCING A FORM 140 RETURN FROM REAL BOOKS.
//
// Every test here posts real vouchers through internal/ledger, records real
// challans through migration 0069, and asserts on what comes out the other end.
// Nothing is stubbed: the point of this file is that the emitter, which is
// proved against the spec in internal/compliance/form140, is being fed the
// right things by the database.
//
// Several are named for the REFUSAL rather than the behaviour, because a return
// that quietly omits a row is the failure mode that costs money — the deductee
// loses their credit and finds out months later.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance/form140"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

func d4(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func day(y int, m time.Month, dd int) time.Time {
	return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)
}

// returnFixture is a company that can actually file: a deductor profile, a
// deductor status, and the ledgers a TDS voucher needs.
type returnFixture struct {
	pool      *db.Pool
	ctx       context.Context
	companyID uuid.UUID
	led       *ledger.Service
	ids       *identity.Service

	expense, tdsPayable, consultant, landlord uuid.UUID
}

// adminExec runs a statement as the OWNER, bypassing RLS — used only to seed
// tables that have no write API yet (migrations 0049, 0069, 0070). The moment
// one of them gets a route, these helpers should call it instead.
func adminExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("admin exec: %v\nSQL: %s", err, sql)
	}
}

func newReturnFixture(t *testing.T) *returnFixture {
	t.Helper()
	pool, ctx, companyID, ids := setup(t)
	f := &returnFixture{
		pool: pool, ctx: ctx, companyID: companyID,
		led: ledger.NewService(pool), ids: ids,
	}

	mk := func(name, group string) uuid.UUID {
		id, err := ids.CreateLedger(ctx, name, group)
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	f.expense = mk("Professional Fees", "Indirect Expenses")
	f.tdsPayable = mk("TDS Payable", "Duties & Taxes")
	f.consultant = mk("Chitra Consulting", "Sundry Creditors")
	f.landlord = mk("Godown Landlord", "Sundry Creditors")

	// Migration 0049: without this row the post path REFUSES to derive at all.
	adminExec(t, `INSERT INTO company_deductor_status
		(id, company_id, tax_year_start, entity_category, recorded_by)
		VALUES (gen_random_uuid(), $1, 2025, 'COMPANY', 'test')`, companyID)
	f.seedDeductorProfile(t)
	return f
}

// seedDeductorProfile writes migration 0070's row. Kept separate so the
// no-profile test can build a fixture WITHOUT it.
func (f *returnFixture) seedDeductorProfile(t *testing.T) {
	t.Helper()
	adminExec(t, `INSERT INTO company_tds_profile (
		company_id, tan, pan, deductor_name, deductor_category,
		address1, address2, state_code, pincode, email, contact_number,
		responsible_name, responsible_designation, responsible_pan,
		responsible_address1, responsible_state_code, responsible_pincode,
		responsible_email, responsible_contact_number, gstin, recorded_by)
		VALUES ($1,'MUMK12345E','AAACK1234M','Demo Traders Private Limited','K',
		        '14 Marine Lines','Churchgate','19','400020',
		        'accounts@demotraders.example','2226301234',
		        'Radhika Venkatesan','Finance Director','AKQPV7821L',
		        '14 Marine Lines','19','400020',
		        'radhika@demotraders.example','2226301299',
		        '27AAACK1234M1Z5','test')`, f.companyID)
}

// setPAN puts a PAN on a deductee ledger, so the return can quote a real one.
func (f *returnFixture) setPAN(t *testing.T, ledgerID uuid.UUID, pan string) {
	t.Helper()
	adminExec(t, `UPDATE ledger SET pan = $2 WHERE id = $1`, ledgerID, pan)
}

// deduct posts a Purchase voucher carrying a TDS leg and returns its voucher id.
func (f *returnFixture) deduct(t *testing.T, no, date, section string,
	deductee uuid.UUID, base, tax string) uuid.UUID {
	t.Helper()
	purchaseVT, err := f.ids.VoucherTypeID(f.ctx, "Purchase")
	if err != nil {
		t.Fatalf("purchase vt: %v", err)
	}
	baseD, taxD := d4(base), d4(tax)
	postings := []ledger.PostingInput{
		{LedgerID: f.expense, DrCr: ledger.Debit, Amount: baseD},
		{LedgerID: deductee, DrCr: ledger.Credit, Amount: baseD.Sub(taxD)},
	}
	if !taxD.IsZero() {
		postings = append(postings, ledger.PostingInput{
			LedgerID: f.tdsPayable, DrCr: ledger.Credit, Amount: taxD,
		})
	}
	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: purchaseVT,
		VoucherNo: no, VoucherDate: date, CreatedBy: "test",
		PartyLedgerID: deductee, Postings: postings,
		TDS: &ledger.TDSParams{
			Section: section, DeducteeLedgerID: deductee,
			DeducteeType: "OTHER", PANAvailable: true,
			TaxableAmount: baseD, TDSLedgerID: f.tdsPayable,
		},
	})
	if err != nil {
		t.Fatalf("post %s: %v", no, err)
	}
	return res.VoucherID
}

// challan records a deposit and allocates it to one deduction.
func (f *returnFixture) challan(t *testing.T, bsr, date, no, tax string,
	voucherID uuid.UUID, section, allocated string) uuid.UUID {
	t.Helper()
	challanID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	adminExec(t, `INSERT INTO tds_challan
		(id, company_id, bsr_code, deposit_date, challan_no, tax_rupees,
		 minor_head, tax_year_start, recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,'200',2026,'test')`,
		challanID, f.companyID, bsr, date, no, d4(tax))
	f.allocate(t, challanID, voucherID, section, allocated)
	return challanID
}

func (f *returnFixture) allocate(t *testing.T, challanID, voucherID uuid.UUID,
	section, amount string) {
	t.Helper()
	adminExec(t, `INSERT INTO tds_challan_allocation
		(id, company_id, challan_id, voucher_id, section, leg_seq,
		 allocated_amount_rupees, recorded_by)
		VALUES (gen_random_uuid(),$1,$2,$3,$4,0,$5,'test')`,
		f.companyID, challanID, voucherID, section, d4(amount))
}

// scalarInt runs a one-column count against the tenant pool. The pool exposes
// only RunInTx (there is no bare QueryRow), which is deliberate: every read
// goes through a transaction that has set the RLS company GUC.
func (f *returnFixture) scalarInt(t *testing.T, sql string) int {
	t.Helper()
	var n int
	if err := f.pool.RunInTx(f.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, sql, f.companyID).Scan(&n)
	}); err != nil {
		t.Fatalf("query %s: %v", sql, err)
	}
	return n
}

func (f *returnFixture) onlyDeductionVoucher(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.RunInTx(f.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT voucher_id FROM tds_deduction WHERE company_id = $1`, f.companyID).Scan(&id)
	}); err != nil {
		t.Fatalf("read deduction voucher: %v", err)
	}
	return id
}

func (f *returnFixture) request() reporting.Form140Request {
	return reporting.Form140Request{
		CompanyID:        f.companyID,
		TaxYearStart:     2026,
		Quarter:          form140.Q1,
		FileCreationDate: day(2026, time.July, 20),
		FileSequenceNo:   1,
		RPUName:          "Khata cored",
	}
}

// ---------------------------------------------------------------------------
// The happy path — books in, filed file out
// ---------------------------------------------------------------------------

// TestAQuarterWithOneDepositedDeductionProducesAFile is the whole point of the
// two migrations: before them, this test could not be written at all.
func TestAQuarterWithOneDepositedDeductionProducesAFile(t *testing.T) {
	f := newReturnFixture(t)
	f.setPAN(t, f.consultant, "ABCPK9012Q")
	// ₹2,00,000 of professional fees at 10% = ₹20,000.
	v := f.deduct(t, "P1", "2026-05-10", "194J-P", f.consultant, "200000.0000", "20000.0000")
	f.challan(t, "0004329", "2026-06-07", "05122", "20000.0000", v, "194J-P", "20000.0000")

	svc := reporting.NewService(f.pool)
	out, err := svc.Form140File(f.ctx, f.request())
	if err != nil {
		t.Fatalf("Form140File: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(string(out), "\r\n"), "\r\n")
	if len(lines) != 4 {
		t.Fatalf("want FH, BH, CD, DD = 4 records; got %d:\n%s", len(lines), out)
	}
	cd := strings.Split(lines[2], "^")
	if cd[5] != "N" { // NIL challan indicator
		t.Errorf("challan is marked nil (%q); tax WAS deposited", cd[5])
	}
	if cd[14] != "0004329" { // BSR code, leading zero intact
		t.Errorf("BSR code is %q, want 0004329 — a leading zero was eaten", cd[14])
	}
	dd := strings.Split(lines[3], "^")
	if dd[7] != "ABCPK9012Q" {
		t.Errorf("deductee PAN is %q", dd[7])
	}
	if dd[14] != "1027" { // Annexure 2 code for professional services
		t.Errorf("section code is %q, want 1027", dd[14])
	}
	if dd[4] != "1" { // challan reference -> CD record 1
		t.Errorf("challan reference is %q, want 1", dd[4])
	}
	if dd[17] != "Y" { // has the tax been deposited
		t.Errorf("deposited flag is %q, want Y", dd[17])
	}
	if dd[23] != "20000.00" || dd[24] != "20000.00" {
		t.Errorf("tax deducted/deposited are %q/%q, want 20000.00/20000.00", dd[23], dd[24])
	}
	if dd[27] != "10.0000" {
		t.Errorf("rate is %q, want 10.0000", dd[27])
	}
	if dd[31] != "" { // no Annexure 6 reason: an ordinary deduction needs none
		t.Errorf("reason flag is %q, want empty for an ordinary deduction", dd[31])
	}
}

// TestASubThresholdPaymentIsReportedUnderASynthesisedNilChallan.
//
// The zero-tax row is a FACT, not an absence — migration 0050's header — and the
// return carries it with Annexure 6 flag Y. The nil challan it hangs from is
// SYNTHESISED and must not be in tds_challan, because it is not a deposit.
func TestASubThresholdPaymentIsReportedUnderASynthesisedNilChallan(t *testing.T) {
	f := newReturnFixture(t)
	f.setPAN(t, f.consultant, "ABCPK9012Q")
	// ₹20,000 of professional fees is under the ₹50,000 limit: no tax.
	f.deduct(t, "P1", "2026-05-10", "194J-P", f.consultant, "20000.0000", "0.0000")

	svc := reporting.NewService(f.pool)
	out, err := svc.Form140File(f.ctx, f.request())
	if err != nil {
		t.Fatalf("Form140File: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\r\n"), "\r\n")
	cd := strings.Split(lines[2], "^")
	if cd[5] != "Y" {
		t.Errorf("nil challan indicator is %q, want Y", cd[5])
	}
	if cd[14] != "" || cd[16] != "" {
		t.Errorf("a nil challan carries a BSR code (%q) or challan number (%q)", cd[14], cd[16])
	}
	if cd[18] != "30062026" { // pinned to the last day of Q1
		t.Errorf("nil challan date is %q, want 30062026", cd[18])
	}
	dd := strings.Split(lines[3], "^")
	if dd[31] != "Y" {
		t.Errorf("reason flag is %q, want Y (below threshold)", dd[31])
	}
	if dd[23] != "0.00" || dd[27] != "0.0000" {
		t.Errorf("zero-tax row has tax %q rate %q, want 0.00 / 0.0000", dd[23], dd[27])
	}
	if dd[26] != "" {
		t.Errorf("zero-tax row carries a date of deduction %q; the format forbids one", dd[26])
	}

	// And the fiction is not in the books.
	challans := f.scalarInt(t, `SELECT count(*) FROM tds_challan WHERE company_id = $1`)
	if challans != 0 {
		t.Errorf("%d challan row(s) exist; a nil challan is a return artefact and must not be "+
			"stored as a deposit — the first report to SUM that table would count it", challans)
	}
}

// ---------------------------------------------------------------------------
// The refusals, each naming what the operator must do
// ---------------------------------------------------------------------------

// TestNoDeductorProfileRefusesByName — migration 0070's absent row.
func TestNoDeductorProfileRefusesByName(t *testing.T) {
	pool, ctx, companyID, _ := setup(t)
	svc := reporting.NewService(pool)
	_, err := svc.Form140StatementFor(ctx, reporting.Form140Request{
		CompanyID: companyID, TaxYearStart: 2026, Quarter: form140.Q1,
		FileCreationDate: day(2026, time.July, 20), FileSequenceNo: 1, RPUName: "x",
	})
	if !errors.Is(err, reporting.ErrNoDeductorProfile) {
		t.Fatalf("want ErrNoDeductorProfile, got %v", err)
	}
	if !strings.Contains(err.Error(), "TAN") {
		t.Errorf("the refusal must name the TAN — it is the field nothing else can supply: %v", err)
	}
}

// TestTaxDeductedButNotDepositedIsRefused.
//
// ⚠️ THE TEMPTING ALTERNATIVE IS TO FILE IT WITH "deposited = N", AND THE
// FORMAT DOES NOT ALLOW IT: Deductee Detail field 5 is a challan reference, so
// a deduction with no challan has no row to occupy. Omitting it silently would
// under-report the quarter by exactly the tax somebody forgot to pay.
func TestTaxDeductedButNotDepositedIsRefused(t *testing.T) {
	f := newReturnFixture(t)
	f.setPAN(t, f.consultant, "ABCPK9012Q")
	f.deduct(t, "P1", "2026-05-10", "194J-P", f.consultant, "200000.0000", "20000.0000")
	// No challan recorded.

	svc := reporting.NewService(f.pool)
	_, err := svc.Form140StatementFor(f.ctx, f.request())
	if !errors.Is(err, reporting.ErrTaxNotDeposited) {
		t.Fatalf("want ErrTaxNotDeposited, got %v", err)
	}
	for _, want := range []string{"P1", "Chitra Consulting", "194J-P"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q so it is actionable: %v", want, err)
		}
	}
}

// TestAnAlteredVoucherBreaksItsChallanAllocation is the 0045 mechanism, ported.
//
// The allocation records WHAT WAS DEPOSITED and is keyed on the voucher
// identity, which survives an alter. Altering the voucher re-derives the
// deduction; if the tax moves, the allocation no longer matches the deduction
// it paid. That is a real discrepancy — money went to the Government against a
// figure that has since changed — and it must be SEEN, never used to build a
// return.
func TestAnAlteredVoucherBreaksItsChallanAllocation(t *testing.T) {
	f := newReturnFixture(t)
	f.setPAN(t, f.consultant, "ABCPK9012Q")
	v := f.deduct(t, "P1", "2026-05-10", "194J-P", f.consultant, "200000.0000", "20000.0000")
	f.challan(t, "0004329", "2026-06-07", "05122", "20000.0000", v, "194J-P", "20000.0000")

	svc := reporting.NewService(f.pool)
	if _, err := svc.Form140StatementFor(f.ctx, f.request()); err != nil {
		t.Fatalf("the return should build before the alter: %v", err)
	}

	// Correct the bill downwards: ₹1,00,000 at 10% = ₹10,000.
	purchaseVT, err := f.ids.VoucherTypeID(f.ctx, "Purchase")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.led.AlterVoucher(f.ctx, v, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: purchaseVT,
		VoucherNo: "P1", VoucherDate: "2026-05-10",
		CreatedBy: "test", PartyLedgerID: f.consultant,
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: d4("100000.0000")},
			{LedgerID: f.consultant, DrCr: ledger.Credit, Amount: d4("90000.0000")},
			{LedgerID: f.tdsPayable, DrCr: ledger.Credit, Amount: d4("10000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section: "194J-P", DeducteeLedgerID: f.consultant,
			DeducteeType: "OTHER", PANAvailable: true,
			TaxableAmount: d4("100000.0000"), TDSLedgerID: f.tdsPayable,
		},
	}); err != nil {
		t.Fatalf("alter: %v", err)
	}

	// The allocation SURVIVED the alter — a cascade would have destroyed the
	// record of a payment that really was made.
	allocations := f.scalarInt(t, `SELECT count(*) FROM tds_challan_allocation WHERE company_id = $1`)
	if allocations != 1 {
		t.Fatalf("%d allocation(s) survived the alter, want 1 — an ON DELETE CASCADE to "+
			"tds_deduction would have silently destroyed the record of a real payment", allocations)
	}

	// And it is now visibly broken rather than silently used.
	_, err = svc.Form140StatementFor(f.ctx, f.request())
	if !errors.Is(err, reporting.ErrAllocationBrokenByAlter) {
		t.Fatalf("want ErrAllocationBrokenByAlter, got %v", err)
	}
	if !strings.Contains(err.Error(), "20000") || !strings.Contains(err.Error(), "10000") {
		t.Errorf("the refusal must show BOTH figures — what was paid and what is now due: %v", err)
	}
}

// TestSubThresholdRentCannotBeFiled is the finding from the primary spec,
// arriving at the operator through real books.
//
// Rent has a ₹50,000-a-month threshold, so a sub-threshold rent payment is
// ordinary — and Annexure 6 lists NO below-threshold flag against codes 1008 or
// 1009. A guessed implementation would have written Y and been rejected at the
// portal. This one refuses, names the voucher and the landlord, and says which
// flags the annexure does allow.
func TestSubThresholdRentCannotBeFiled(t *testing.T) {
	f := newReturnFixture(t)
	f.setPAN(t, f.landlord, "AAJPT4455N")
	// ₹40,000 of godown rent: under the limit, so no tax.
	f.deduct(t, "P1", "2026-05-10", "194I-B", f.landlord, "40000.0000", "0.0000")

	svc := reporting.NewService(f.pool)
	_, err := svc.Form140StatementFor(f.ctx, f.request())
	if !errors.Is(err, form140.ErrNoValidReasonFlag) {
		t.Fatalf("want form140.ErrNoValidReasonFlag, got %v", err)
	}
	for _, want := range []string{"P1", "Godown Landlord", "1009"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q: %v", want, err)
		}
	}
	// Contrast: the SAME amount as professional fees files without complaint,
	// because Annexure 6 does list Y against 1027. The difference is the
	// annexure's, not this code's.
	f2 := newReturnFixture(t)
	f2.setPAN(t, f2.consultant, "ABCPK9012Q")
	f2.deduct(t, "P1", "2026-05-10", "194J-P", f2.consultant, "40000.0000", "0.0000")
	if _, err := reporting.NewService(f2.pool).Form140File(f2.ctx, f2.request()); err != nil {
		t.Errorf("the same sub-threshold amount as professional fees should file: %v", err)
	}
}

// TestADeducteeWithNoPANIsReportedWithTheHigherRateFlag — s.397(2). The engine
// applies the 20% floor; the return has to say why the rate is 20%, and flag C
// is that reason.
func TestADeducteeWithNoPANIsReportedWithTheHigherRateFlag(t *testing.T) {
	f := newReturnFixture(t)
	purchaseVT, err := f.ids.VoucherTypeID(f.ctx, "Purchase")
	if err != nil {
		t.Fatal(err)
	}
	// PANAvailable false: ₹2,00,000 at the 20% floor = ₹40,000.
	if _, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: purchaseVT,
		VoucherNo: "P1", VoucherDate: "2026-05-10", CreatedBy: "test",
		PartyLedgerID: f.consultant,
		Postings: []ledger.PostingInput{
			{LedgerID: f.expense, DrCr: ledger.Debit, Amount: d4("200000.0000")},
			{LedgerID: f.consultant, DrCr: ledger.Credit, Amount: d4("160000.0000")},
			{LedgerID: f.tdsPayable, DrCr: ledger.Credit, Amount: d4("40000.0000")},
		},
		TDS: &ledger.TDSParams{
			Section: "194J-P", DeducteeLedgerID: f.consultant,
			DeducteeType: "OTHER", PANAvailable: false,
			TaxableAmount: d4("200000.0000"), TDSLedgerID: f.tdsPayable,
		},
	}); err != nil {
		t.Fatalf("post: %v", err)
	}
	voucherID := f.onlyDeductionVoucher(t)
	f.challan(t, "0004329", "2026-06-07", "05122", "40000.0000", voucherID, "194J-P", "40000.0000")

	out, err := reporting.NewService(f.pool).Form140File(f.ctx, f.request())
	if err != nil {
		t.Fatalf("Form140File: %v", err)
	}
	dd := strings.Split(strings.Split(string(out), "\r\n")[3], "^")
	if dd[31] != "C" {
		t.Errorf("reason flag is %q, want C (higher rate for non-furnishing of PAN)", dd[31])
	}
	if dd[27] != "20.0000" {
		t.Errorf("rate is %q, want 20.0000", dd[27])
	}
	// The PAN placeholder that claims the least: no declaration was received.
	if dd[7] != "PANNOTAVBL" {
		t.Errorf("PAN is %q, want PANNOTAVBL — PANAPPLIED would assert a document exists", dd[7])
	}
}

// TestTwoDeductionsOnOneChallanShareItsRecord — the ordinary case, and the one
// that proves the grouping. One deposit, two deductees, one CD record.
func TestTwoDeductionsOnOneChallanShareItsRecord(t *testing.T) {
	f := newReturnFixture(t)
	f.setPAN(t, f.consultant, "ABCPK9012Q")
	f.setPAN(t, f.landlord, "AAJPT4455N")
	v1 := f.deduct(t, "P1", "2026-05-10", "194J-P", f.consultant, "200000.0000", "20000.0000")
	v2 := f.deduct(t, "P2", "2026-05-12", "194I-B", f.landlord, "60000.0000", "6000.0000")

	// ₹26,000 paid over on one challan, covering both.
	cid := f.challan(t, "0004329", "2026-06-07", "05122", "26000.0000", v1, "194J-P", "20000.0000")
	f.allocate(t, cid, v2, "194I-B", "6000.0000")

	out, err := reporting.NewService(f.pool).Form140File(f.ctx, f.request())
	if err != nil {
		t.Fatalf("Form140File: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\r\n"), "\r\n")
	if len(lines) != 5 { // FH, BH, CD, DD, DD
		t.Fatalf("want one challan record with two deductees; got %d records:\n%s", len(lines), out)
	}
	cd := strings.Split(lines[2], "^")
	if cd[4] != "2" {
		t.Errorf("challan reports %q deductee records, want 2", cd[4])
	}
	if cd[11] != "26000.00" {
		t.Errorf("challan deposit total is %q, want 26000.00", cd[11])
	}
	// CD 21/22 must tie to the annexure below them, and they are SUMMED from it.
	if cd[20] != "26000.00" || cd[21] != "26000.00" {
		t.Errorf("challan annexure totals are %q/%q, want 26000.00 each", cd[20], cd[21])
	}
	for i, want := range []string{"1027", "1009"} {
		dd := strings.Split(lines[3+i], "^")
		if dd[14] != want {
			t.Errorf("deductee %d section code is %q, want %s", i+1, dd[14], want)
		}
		if dd[4] != "1" {
			t.Errorf("deductee %d references challan %q, want 1", i+1, dd[4])
		}
	}
}

// TestADeductionOutsideTheQuarterIsNotInTheReturn — a return is filed per
// quarter, and a payment in Q2 has no business in the Q1 file.
func TestADeductionOutsideTheQuarterIsNotInTheReturn(t *testing.T) {
	f := newReturnFixture(t)
	f.setPAN(t, f.consultant, "ABCPK9012Q")
	v1 := f.deduct(t, "P1", "2026-05-10", "194J-P", f.consultant, "200000.0000", "20000.0000")
	f.challan(t, "0004329", "2026-06-07", "05122", "20000.0000", v1, "194J-P", "20000.0000")
	// A second bill in Q2, deliberately left undeposited: if the Q1 return
	// picked it up it would fail with ErrTaxNotDeposited, which is a louder
	// signal than a silent extra row.
	f.deduct(t, "P2", "2026-08-14", "194J-P", f.consultant, "300000.0000", "30000.0000")

	out, err := reporting.NewService(f.pool).Form140File(f.ctx, f.request())
	if err != nil {
		t.Fatalf("the Q1 return must not see the Q2 bill: %v", err)
	}
	if got := strings.Count(string(out), "^DD^"); got != 1 {
		t.Errorf("the Q1 return carries %d deductee records, want 1", got)
	}
}

// TestQuarterBoundsAgreeWithTheEmitter pins the one duplicated computation in
// this file against form140's own, which is unexported there.
//
// They must agree exactly, and Q4 is the case worth checking: it crosses the
// calendar year, so a shared-constant implementation gets it wrong.
func TestQuarterBoundsAgreeWithTheEmitter(t *testing.T) {
	f := newReturnFixture(t)
	f.setPAN(t, f.consultant, "ABCPK9012Q")
	// The last day of Q4 2026-27 is 31 March 2027.
	v := f.deduct(t, "P1", "2027-03-31", "194J-P", f.consultant, "200000.0000", "20000.0000")
	f.challan(t, "0004329", "2027-04-07", "05122", "20000.0000", v, "194J-P", "20000.0000")

	req := f.request()
	req.Quarter = form140.Q4
	req.FileCreationDate = day(2027, time.May, 20)
	out, err := reporting.NewService(f.pool).Form140File(f.ctx, req)
	if err != nil {
		t.Fatalf("a 31 March payment must fall in Q4 of the tax year beginning 2026: %v", err)
	}
	if got := strings.Count(string(out), "^DD^"); got != 1 {
		t.Errorf("Q4 carries %d deductee records, want 1", got)
	}
	// And Q3 must not claim it.
	req.Quarter = form140.Q3
	if _, err := reporting.NewService(f.pool).Form140File(f.ctx, req); err == nil {
		t.Error("Q3 produced a file from a 31 March payment; the quarter bounds are wrong")
	}
}
