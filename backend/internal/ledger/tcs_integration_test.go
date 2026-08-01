// TCS ON THE POST PATH — Item Zero step 3.
//
// compliance.TCSDerive shipped corpus-gated and UNCALLED: a sale of scrap
// collected nothing, and the s.394(2) declaration table was read by nobody.
// These tests exercise the seam that ends both, including the three-valued
// declaration outcome that a boolean would collapse.
package ledger_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
)

type tcsFixture struct {
	led       *ledger.Service
	pool      *db.Pool
	ctx       context.Context
	salesType uuid.UUID
	sales     uuid.UUID
	tcsPay    uuid.UUID
	buyer     uuid.UUID
}

func newTCSFixture(t *testing.T) tcsFixture {
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
	vtID, err := m.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("sales voucher type: %v", err)
	}
	return tcsFixture{
		led:       ledger.NewService(pool),
		pool:      pool,
		ctx:       ctx,
		salesType: vtID,
		sales:     mk("Scrap Sales", "Sales Accounts"),
		tcsPay:    mk("TCS Payable", "Duties & Taxes"),
		buyer:     mk("Foundry Buyer", "Sundry Debtors"),
	}
}

// tcsCollectionRow is what migration 0067 actually stored.
type tcsCollectionRow struct {
	Section     string
	TaxYear     int
	Base        decimal.Decimal
	Rate        decimal.Decimal
	Amount      decimal.Decimal
	Declaration string
	Buyer       uuid.UUID
}

func tcsCollectionRows(t *testing.T, pool *db.Pool, ctx context.Context, voucherID uuid.UUID) []tcsCollectionRow {
	t.Helper()
	var out []tcsCollectionRow
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT section, tax_year_start, taxable_base_rupees, rate_pct,
			        tcs_amount_rupees, declaration_outcome, buyer_ledger_id
			   FROM tcs_collection WHERE voucher_id = $1 ORDER BY leg_seq`, voucherID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r tcsCollectionRow
			if err := rows.Scan(&r.Section, &r.TaxYear, &r.Base, &r.Rate,
				&r.Amount, &r.Declaration, &r.Buyer); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("read tcs_collection: %v", err)
	}
	return out
}

// sellScrap posts a scrap sale. tcs "" means no TCS leg at all.
func (f tcsFixture) sellScrap(t *testing.T, no, date, base, tcs, receivable string, panAvailable bool) (ledger.PostResult, error) {
	t.Helper()
	postings := []ledger.PostingInput{
		{LedgerID: f.buyer, DrCr: ledger.Debit, Amount: dec(t, receivable)},
		{LedgerID: f.sales, DrCr: ledger.Credit, Amount: dec(t, base)},
	}
	if tcs != "" {
		postings = append(postings,
			ledger.PostingInput{LedgerID: f.tcsPay, DrCr: ledger.Credit, Amount: dec(t, tcs)})
	}
	return f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: f.salesType, VoucherNo: no,
		VoucherDate: date, PartyLedgerID: f.buyer, CreatedBy: "tester",
		Postings: postings,
		TCS: &ledger.TCSParams{
			Section:       "206C(1)-SCRAP",
			BuyerLedgerID: f.buyer,
			PANAvailable:  panAvailable,
			TaxableAmount: dec(t, base),
			TCSLedgerID:   f.tcsPay,
		},
	})
}

// fileDeclaration writes a s.394(2) declaration. pan "" means none furnished,
// which is the s.397(2)(f)(i) invalid state and NOT the absence of a document.
func (f tcsFixture) fileDeclaration(t *testing.T, taxYear int, pan string) {
	t.Helper()
	m := masters.NewService(f.pool)
	if err := m.SetTCSDeclaration(f.ctx, masters.TCSDeclarationRecord{
		BuyerLedgerID:   f.buyer,
		Section:         "206C(1)-SCRAP",
		TaxYearStart:    taxYear,
		DeclarationDate: "2026-04-01",
		ReceivedDate:    "2026-04-03",
		BuyerPAN:        pan,
	}, "tester"); err != nil {
		t.Fatalf("file declaration: %v", err)
	}
}

// TestScrapSaleCollectsTwoPercent is the happy path and the rate regression
// guard in one. ₹1,00,000 of scrap at the Finance Act 2026 rate of 2% = ₹2,000.
//
// ⚠️ THE 2% IS THE ASSERTION. This row shipped at 1% and under-collected by half
// on every scrap sale for four months (docs/design/t6-gaps-model.md §0). It is
// now reachable from a real post, so a regression would move real money.
func TestScrapSaleCollectsTwoPercent(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTCSFixture(t)

	res, err := f.sellScrap(t, "TCS-1", "2026-05-10", "100000.0000", "2000.0000", "102000.0000", true)
	if err != nil {
		t.Fatalf("scrap sale must post: %v", err)
	}
	if !res.TCS.TCS.Equal(dec(t, "2000.0000")) {
		t.Fatalf("collected %s, want 2000.0000 (2%% of ₹1,00,000 — the Finance Act "+
			"2026 rate; 1000 would be the repealed 1%%)", res.TCS.TCS)
	}
	if !res.TCS.Applied {
		t.Error("scrap carries no threshold, so any positive sale attracts collection")
	}

	// The buyer pays the consideration PLUS the tax: that is what "collected at
	// source" means, and it is why the receivable is 102,000 not 100,000.
	dr, _ := ledgerBalance(t, f.pool, f.ctx, f.buyer)
	if !dr.Equal(dec(t, "102000.0000")) {
		t.Fatalf("buyer debit: got %s, want 102000.0000 (consideration + collection)", dr)
	}

	rows := tcsCollectionRows(t, f.pool, f.ctx, res.VoucherID)
	if len(rows) != 1 {
		t.Fatalf("want 1 persisted collection, got %d", len(rows))
	}
	if rows[0].Declaration != "NONE" {
		t.Errorf("declaration_outcome: got %q, want NONE", rows[0].Declaration)
	}
	if !rows[0].Amount.Equal(dec(t, "2000.0000")) || !rows[0].Rate.Equal(dec(t, "2.0000")) {
		t.Errorf("persisted %s at %s%%, want 2000.0000 at 2.0000", rows[0].Amount, rows[0].Rate)
	}
	if rows[0].TaxYear != 2026 {
		t.Errorf("tax_year_start: got %d, want 2026 (the SALE's year — unlike the "+
			"deductor-status test, which reads the preceding one)", rows[0].TaxYear)
	}
	if rows[0].Buyer != f.buyer {
		t.Errorf("buyer_ledger_id: got %s, want %s", rows[0].Buyer, f.buyer)
	}
}

// TestValidDeclarationExemptsTheSale is the whole reason migration 0049's
// declaration table exists, driven end to end for the first time.
func TestValidDeclarationExemptsTheSale(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTCSFixture(t)
	f.fileDeclaration(t, 2026, "AAAPZ1234C")

	// No TCS leg, because nothing is collected.
	res, err := f.sellScrap(t, "TCS-EX", "2026-05-10", "100000.0000", "", "100000.0000", true)
	if err != nil {
		t.Fatalf("an exempted sale must post with no collection: %v", err)
	}
	if res.TCS.Applied || !res.TCS.TCS.IsZero() {
		t.Fatalf("a valid s.394(2) declaration exempts the collection entirely; "+
			"got applied=%v tcs=%s", res.TCS.Applied, res.TCS.TCS)
	}

	// ⚠️ THE ROW IS STILL WRITTEN, AND IT SAYS *WHY* IT IS ZERO. An exempt sale
	// is the one that most needs recording: the exemption rests on a document
	// the department can demand under s.394(3).
	rows := tcsCollectionRows(t, f.pool, f.ctx, res.VoucherID)
	if len(rows) != 1 {
		t.Fatalf("an exempted sale must still be recorded; got %d rows", len(rows))
	}
	if rows[0].Declaration != "EXEMPT" {
		t.Fatalf("declaration_outcome: got %q, want EXEMPT — a zero with NONE would "+
			"say this sale was below a threshold, which is a different and false "+
			"claim about a sale that carried a filed declaration", rows[0].Declaration)
	}
}

// TestDeclarationWithoutPANTriggersThePenalRate is V-10, and it is the coupling
// the memo calls "easy to get half-right".
//
// s.397(2)(f)(i): a declaration without a valid PAN BECOMES INVALID.
// s.397(2)(g): an invalid declaration then requires collection at the no-PAN
// rate — higher of twice the rate (4%) or 5%, so 5%.
//
// THE THREE-WAY ASSERTION IS THE POINT: the answer must be 5%, and must be
// NEITHER the exemption (0) NOR the base rate (2%). Both wrong answers look
// entirely plausible.
func TestDeclarationWithoutPANTriggersThePenalRate(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTCSFixture(t)
	f.fileDeclaration(t, 2026, "") // furnished, no PAN

	// 5% of 1,00,000 = 5,000.
	res, err := f.sellScrap(t, "TCS-NOPAN", "2026-05-10", "100000.0000", "5000.0000", "105000.0000", true)
	if err != nil {
		t.Fatalf("a PAN-less declaration must collect at the PENAL rate, not exempt "+
			"and not the base rate: %v", err)
	}
	if !res.TCS.TCS.Equal(dec(t, "5000.0000")) {
		t.Fatalf("collected %s, want 5000.0000 (s.397(2)(b)(ii): higher of 2x2%% and "+
			"5%%). 0 would be the exemption wrongly applied; 2000 would be the base "+
			"rate wrongly applied", res.TCS.TCS)
	}
	if res.TCS.TCS.Equal(dec(t, "2000.0000")) || res.TCS.TCS.IsZero() {
		t.Fatal("unreachable given the check above; kept as the explicit statement of " +
			"the two wrong answers this test exists to exclude")
	}
	rows := tcsCollectionRows(t, f.pool, f.ctx, res.VoucherID)
	if len(rows) != 1 || rows[0].Declaration != "INVALID_NO_PAN" {
		t.Fatalf("declaration_outcome must record the INVALID state — it is neither "+
			"an exemption nor an absence; got %+v", rows)
	}
}

// TestDeclarationForAnotherYearDoesNotExempt pins the tax-year key on the
// declaration lookup, and the direction of the off-by-one.
//
// A declaration governs the year it was FURNISHED FOR — the sale's own year, NOT
// the preceding one the deductor-status test reads. Reusing that subtraction
// here would ignore every declaration ever filed, so this test fails loudly if
// somebody "makes the two lookups consistent".
func TestDeclarationForAnotherYearDoesNotExempt(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTCSFixture(t)
	// Filed for 2025-26; the sale is in 2026-27.
	f.fileDeclaration(t, 2025, "AAAPZ1234C")

	// It must NOT exempt: full 2% is due, so a voucher with no TCS leg is
	// refused.
	_, err := f.sellScrap(t, "TCS-YR", "2026-05-10", "100000.0000", "", "100000.0000", true)
	if !errors.Is(err, ledger.ErrTCSMismatch) {
		t.Fatalf("a declaration filed for 2025-26 must not exempt a 2026-27 sale; "+
			"want ErrTCSMismatch, got %v", err)
	}

	// The control: file it for the sale's own year and the identical voucher
	// posts. Without this arm the test would pass against a build that ignored
	// declarations entirely.
	f.fileDeclaration(t, 2026, "AAAPZ1234C")
	if _, err := f.sellScrap(t, "TCS-YR2", "2026-05-10", "100000.0000", "", "100000.0000", true); err != nil {
		t.Fatalf("with the declaration filed for the sale's own year it must exempt: %v", err)
	}
}

// TestTCSLegOnTheWrongSideRejected mirrors the TDS-side side check. The voucher
// balances and the magnitude is right, so only the side assertion can reject it.
func TestTCSLegOnTheWrongSideRejected(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTCSFixture(t)

	_, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: f.salesType, VoucherNo: "TCS-SIDE",
		VoucherDate: "2026-05-10", PartyLedgerID: f.buyer, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.buyer, DrCr: ledger.Debit, Amount: dec(t, "98000.0000")},
			{LedgerID: f.tcsPay, DrCr: ledger.Debit, Amount: dec(t, "2000.0000")},
			{LedgerID: f.sales, DrCr: ledger.Credit, Amount: dec(t, "100000.0000")},
		},
		TCS: &ledger.TCSParams{
			Section: "206C(1)-SCRAP", BuyerLedgerID: f.buyer, PANAvailable: true,
			TaxableAmount: dec(t, "100000.0000"), TCSLedgerID: f.tcsPay,
		},
	})
	if !errors.Is(err, ledger.ErrTCSMismatch) {
		t.Fatalf("a TCS leg on the DEBIT side must be rejected; got %v", err)
	}
}

// TestPurchaseIgnoresTCSParams pins the class gate from the other direction.
// Collection is made by the SELLER, so a `tcs` block on a Purchase is inert and
// must not reject an otherwise valid bill — the mirror of
// TestSalesClassIgnoresTDSParams.
func TestPurchaseIgnoresTCSParams(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTCSFixture(t)
	m := masters.NewService(f.pool)
	expense, err := m.CreateLedger(f.ctx, masters.Ledger{Name: "Scrap Purchases", GroupName: "Purchase Accounts"}, "tester")
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	supplier, err := m.CreateLedger(f.ctx, masters.Ledger{Name: "Scrap Supplier", GroupName: "Sundry Creditors"}, "tester")
	if err != nil {
		t.Fatalf("create supplier: %v", err)
	}
	purchType, err := m.VoucherTypeID(f.ctx, "Purchase")
	if err != nil {
		t.Fatalf("purchase type: %v", err)
	}

	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: purchType, VoucherNo: "TCS-PUR",
		VoucherDate: "2026-05-10", PartyLedgerID: supplier, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: expense, DrCr: ledger.Debit, Amount: dec(t, "100000.0000")},
			{LedgerID: supplier, DrCr: ledger.Credit, Amount: dec(t, "100000.0000")},
		},
		// Deliberately WRONG had it been honoured: no TCS leg is posted.
		TCS: &ledger.TCSParams{
			Section: "206C(1)-SCRAP", BuyerLedgerID: supplier, PANAvailable: true,
			TaxableAmount: dec(t, "100000.0000"), TCSLedgerID: f.tcsPay,
		},
	})
	if err != nil {
		t.Fatalf("a tcs block on a Purchase is inert and must not reject it: %v", err)
	}
	if rows := tcsCollectionRows(t, f.pool, f.ctx, res.VoucherID); len(rows) != 0 {
		t.Fatalf("a Purchase must record no collection; got %d rows", len(rows))
	}
}

// TestMotorVehicleBelowTheLimitCollectsNothingButIsRecorded covers the OTHER
// shipped section, and the threshold direction.
//
// s.394(1) Table Sl. No. 6: 1% "where the sale consideration EXCEEDS ten lakh
// rupees". Exactly ₹10,00,000 does not exceed it, so nothing is collected — and
// the sale is still recorded, because the return has to account for it.
func TestMotorVehicleBelowTheLimitCollectsNothingButIsRecorded(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTCSFixture(t)

	res, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: f.salesType, VoucherNo: "TCS-MV",
		VoucherDate: "2026-05-10", PartyLedgerID: f.buyer, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.buyer, DrCr: ledger.Debit, Amount: dec(t, "1000000.0000")},
			{LedgerID: f.sales, DrCr: ledger.Credit, Amount: dec(t, "1000000.0000")},
		},
		TCS: &ledger.TCSParams{
			Section: "206C(1F)-MV", BuyerLedgerID: f.buyer, PANAvailable: true,
			TaxableAmount: dec(t, "1000000.0000"), TCSLedgerID: f.tcsPay,
		},
	})
	if err != nil {
		t.Fatalf("a sale exactly AT the limit collects nothing and must post: %v", err)
	}
	if res.TCS.Applied {
		t.Fatal("Sl. No. 6 reads \"exceeds ten lakh rupees\"; exactly ₹10,00,000 does not")
	}
	rows := tcsCollectionRows(t, f.pool, f.ctx, res.VoucherID)
	if len(rows) != 1 || !rows[0].Amount.IsZero() || rows[0].Declaration != "NONE" {
		t.Fatalf("a below-threshold sale is still recorded, with NONE; got %+v", rows)
	}
}

// TestAlterAndDeleteRestateTheCollection closes the same loop TDS has: a
// correction re-derives, and a tombstone leaves nothing behind for the return to
// file.
func TestAlterAndDeleteRestateTheCollection(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTCSFixture(t)

	res, err := f.sellScrap(t, "TCS-ALT", "2026-05-10", "100000.0000", "2000.0000", "102000.0000", true)
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	// Correct the sale to ₹1,50,000: 2% = ₹3,000.
	if _, err := f.led.AlterVoucher(f.ctx, res.VoucherID, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: f.salesType, VoucherNo: "TCS-ALT",
		VoucherDate: "2026-05-10", PartyLedgerID: f.buyer, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.buyer, DrCr: ledger.Debit, Amount: dec(t, "153000.0000")},
			{LedgerID: f.sales, DrCr: ledger.Credit, Amount: dec(t, "150000.0000")},
			{LedgerID: f.tcsPay, DrCr: ledger.Credit, Amount: dec(t, "3000.0000")},
		},
		TCS: &ledger.TCSParams{
			Section: "206C(1)-SCRAP", BuyerLedgerID: f.buyer, PANAvailable: true,
			TaxableAmount: dec(t, "150000.0000"), TCSLedgerID: f.tcsPay,
		},
	}); err != nil {
		t.Fatalf("alter: %v", err)
	}
	rows := tcsCollectionRows(t, f.pool, f.ctx, res.VoucherID)
	if len(rows) != 1 {
		t.Fatalf("an alter re-states the collection; want 1 row, got %d — a stale row "+
			"beside the new one would be filed twice", len(rows))
	}
	if !rows[0].Amount.Equal(dec(t, "3000.0000")) {
		t.Fatalf("restated amount: got %s, want 3000.0000", rows[0].Amount)
	}

	if _, err := f.led.DeleteVoucher(f.ctx, res.VoucherID, "tester"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rows := tcsCollectionRows(t, f.pool, f.ctx, res.VoucherID); len(rows) != 0 {
		t.Fatalf("a tombstoned sale must carry no collection; got %d", len(rows))
	}
}

// TestTCSWithoutBuyerIsRefused pins ErrBuyerUnknown. Without the buyer the
// s.394(2) declaration cannot even be looked up, so an exempt buyer would be
// charged — which is why this is a refusal and not a nullable column.
func TestTCSWithoutBuyerIsRefused(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	f := newTCSFixture(t)

	_, err := f.led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: f.salesType, VoucherNo: "TCS-NOBUYER",
		VoucherDate: "2026-05-10", PartyLedgerID: f.buyer, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.buyer, DrCr: ledger.Debit, Amount: dec(t, "102000.0000")},
			{LedgerID: f.sales, DrCr: ledger.Credit, Amount: dec(t, "100000.0000")},
			{LedgerID: f.tcsPay, DrCr: ledger.Credit, Amount: dec(t, "2000.0000")},
		},
		TCS: &ledger.TCSParams{
			Section: "206C(1)-SCRAP", PANAvailable: true,
			TaxableAmount: dec(t, "100000.0000"), TCSLedgerID: f.tcsPay,
		},
	})
	if !errors.Is(err, ledger.ErrBuyerUnknown) {
		t.Fatalf("want ErrBuyerUnknown, got %v", err)
	}
}
