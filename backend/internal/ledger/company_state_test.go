package ledger_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/migrations"
)

// decFromStr is a brief decimal constructor for this test file.
func decFromStr(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// setupWithState migrates the live DB and creates a seeded company carrying the
// given home state_code, returning an app-scoped, company-bound context.
func setupWithState(t *testing.T, stateCode string) (*db.Pool, context.Context, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(ctx, companyID)

	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "State Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
		StateCode:   stateCode,
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	return pool, cctx, companyID
}

// TestCompanyStateDefaultsSupplierStateInsidePostTx is the WS3.3 guard: a Sales
// post that leaves GST.SupplierStateCode EMPTY must have it filled, inside the
// atomic post transaction, from the company's home state_code. With company
// state '27' and a place of supply '07' (inter-state), the engine must derive an
// IGST-only split — proving the default fired before deriveAndVerifyGST ran.
func TestCompanyStateDefaultsSupplierStateInsidePostTx(t *testing.T) {
	pool, ctx, _ := setupWithState(t, "27")

	m := masters.NewService(pool)
	led := ledger.NewService(pool)

	vtID, err := m.VoucherTypeID(ctx, "Sales")
	if err != nil {
		t.Fatalf("voucher type: %v", err)
	}
	party, err := m.CreateLedger(ctx, masters.Ledger{Name: "Inter Debtor", GroupName: "Sundry Debtors"}, "tester")
	if err != nil {
		t.Fatalf("party: %v", err)
	}
	sales, err := m.CreateLedger(ctx, masters.Ledger{Name: "Sales Income", GroupName: "Sales Accounts"}, "tester")
	if err != nil {
		t.Fatalf("sales: %v", err)
	}
	igst, err := m.CreateLedger(ctx, masters.Ledger{Name: "IGST Output", GroupName: "Duties & Taxes"}, "tester")
	if err != nil {
		t.Fatalf("igst: %v", err)
	}

	taxable := decFromStr("1000.0000")
	full := decFromStr("180.0000") // 18% IGST inter-state
	total := taxable.Add(full)

	res, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		VoucherTypeID: vtID,
		PartyLedgerID: party,
		VoucherNo:     "S1",
		VoucherDate:   "2025-05-10",
		Party:         "Inter Debtor",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Debit, Amount: total},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: taxable},
			{LedgerID: igst, DrCr: ledger.Credit, Amount: full},
		},
		// SupplierStateCode deliberately EMPTY — must be defaulted to '27' inside
		// the tx so the '07' place of supply classifies inter-state (IGST).
		GST: &ledger.GSTParams{
			PlaceOfSupplyStateCode: "07",
			Lines:                  []compliance.Line{{TaxableValue: taxable, GSTRatePct: decFromStr("18"), HSN: "8471"}},
			CGSTLedgerID:           uuid.Nil,
			SGSTLedgerID:           uuid.Nil,
			IGSTLedgerID:           igst,
		},
	})
	if err != nil {
		t.Fatalf("post sale: %v", err)
	}

	// Inter-state => IGST carries all the tax; CGST/SGST are zero. If the company
	// state had NOT defaulted, the derivation would have seen supplier '' != POS
	// '07' and still produced IGST; to be a real proof we also assert it is NOT an
	// intra split (which would have required supplier == POS == '07').
	if !res.Tax.IGST.Equal(full) {
		t.Fatalf("expected IGST %s, got %s (CGST %s SGST %s)",
			full, res.Tax.IGST, res.Tax.CGST, res.Tax.SGST)
	}
	if !res.Tax.CGST.IsZero() || !res.Tax.SGST.IsZero() {
		t.Fatalf("expected zero CGST/SGST for inter-state, got CGST %s SGST %s", res.Tax.CGST, res.Tax.SGST)
	}

	// Stronger proof of the default: post an INTRA-state sale where POS == company
	// state '27'. With an empty SupplierStateCode this can only become CGST/SGST if
	// the company-state default fired (supplier '27' == POS '27').
	cgst, err := m.CreateLedger(ctx, masters.Ledger{Name: "CGST Output", GroupName: "Duties & Taxes"}, "tester")
	if err != nil {
		t.Fatalf("cgst: %v", err)
	}
	sgst, err := m.CreateLedger(ctx, masters.Ledger{Name: "SGST Output", GroupName: "Duties & Taxes"}, "tester")
	if err != nil {
		t.Fatalf("sgst: %v", err)
	}
	half := decFromStr("90.0000")
	intraTotal := taxable.Add(half).Add(half)
	res2, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		VoucherTypeID: vtID,
		PartyLedgerID: party,
		VoucherNo:     "S2",
		VoucherDate:   "2025-05-11",
		Party:         "Inter Debtor",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: party, DrCr: ledger.Debit, Amount: intraTotal},
			{LedgerID: sales, DrCr: ledger.Credit, Amount: taxable},
			{LedgerID: cgst, DrCr: ledger.Credit, Amount: half},
			{LedgerID: sgst, DrCr: ledger.Credit, Amount: half},
		},
		GST: &ledger.GSTParams{
			PlaceOfSupplyStateCode: "27", // == company home state
			Lines:                  []compliance.Line{{TaxableValue: taxable, GSTRatePct: decFromStr("18"), HSN: "8471"}},
			CGSTLedgerID:           cgst,
			SGSTLedgerID:           sgst,
		},
	})
	if err != nil {
		t.Fatalf("post intra sale: %v", err)
	}
	if !res2.Tax.CGST.Equal(half) || !res2.Tax.SGST.Equal(half) || !res2.Tax.IGST.IsZero() {
		t.Fatalf("intra split should be CGST/SGST %s each, IGST 0; got CGST %s SGST %s IGST %s",
			half, res2.Tax.CGST, res2.Tax.SGST, res2.Tax.IGST)
	}
}
