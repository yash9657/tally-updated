package migration_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/migration"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
	migs "github.com/khata/cored/migrations"
)

// setupCompanyState migrates the DB (idempotent) and creates a seeded company
// with the given home state, returning the app pool, company-scoped ctx and id.
func setupCompanyState(t *testing.T, stateCode string) (*db.Pool, context.Context, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
	}
	if err := migs.Apply(ctx, adminConn); err != nil {
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
		Name:        "RF Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
		StateCode:   stateCode,
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	return pool, cctx, companyID
}

// gstSalesXML is an inline Tally export: a registered counter-party LEDGER master
// (GSTIN, state 29) plus one intra-state Sales voucher (taxable 1000 @ 18%,
// CGST/SGST 90 each). The supplier state is supplied by the company default
// (WS3), so the GST params on import leave it empty. This drives the full GST
// import path (deriveAndVerifyGST) and produces a GSTR-1 with an HSN row.
const gstSalesXML = `<ENVELOPE>
  <HEADER><TALLYREQUEST>Import</TALLYREQUEST></HEADER>
  <BODY><DESC><STATICVARIABLES><SVCURRENTCOMPANY>RF Traders</SVCURRENTCOMPANY></STATICVARIABLES></DESC>
    <DATA>
      <TALLYMESSAGE>
        <LEDGER NAME="Acme Pvt Ltd">
          <PARENT>Sundry Debtors</PARENT>
          <PARTYGSTIN>29ABCDE1234F1Z5</PARTYGSTIN>
          <GSTREGISTRATIONTYPE>Regular</GSTREGISTRATIONTYPE>
          <STATE>29</STATE>
        </LEDGER>
      </TALLYMESSAGE>
      <TALLYMESSAGE>
        <VOUCHER VCHTYPE="Sales" ACTION="Create">
          <GUID>rf-sale-0001</GUID>
          <DATE>20250510</DATE>
          <VOUCHERNUMBER>S-1</VOUCHERNUMBER>
          <PARTYLEDGERNAME>Acme Pvt Ltd</PARTYLEDGERNAME>
          <PLACEOFSUPPLY>29</PLACEOFSUPPLY>
          <LEDGERENTRIES.LIST>
            <LEDGERNAME>Acme Pvt Ltd</LEDGERNAME>
            <ISDEEMEDPOSITIVE>Yes</ISDEEMEDPOSITIVE>
            <AMOUNT>-1180.00</AMOUNT>
          </LEDGERENTRIES.LIST>
          <LEDGERENTRIES.LIST>
            <LEDGERNAME>Sales Local</LEDGERNAME>
            <ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>
            <AMOUNT>1000.00</AMOUNT>
            <HSNCODE>8471</HSNCODE>
          </LEDGERENTRIES.LIST>
          <LEDGERENTRIES.LIST>
            <LEDGERNAME>CGST Output</LEDGERNAME>
            <ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>
            <AMOUNT>90.00</AMOUNT>
          </LEDGERENTRIES.LIST>
          <LEDGERENTRIES.LIST>
            <LEDGERNAME>SGST Output</LEDGERNAME>
            <ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>
            <AMOUNT>90.00</AMOUNT>
          </LEDGERENTRIES.LIST>
        </VOUCHER>
      </TALLYMESSAGE>
    </DATA>
  </BODY>
</ENVELOPE>`

// TestReconcileFull_FailsLoudly imports a CORRECT GST sales export, then runs
// ReconcileFull against a DOCTORED oracle (wrong Balance-Sheet assetsTotal and a
// wrong HSN IGST). The error must name BOTH offending statements so a CA sees the
// exact divergence — proving the four-statement gate is real, not a tautology.
func TestReconcileFull_FailsLoudly(t *testing.T) {
	pool, ctx, companyID := setupCompanyState(t, "29")

	im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool))
	res, err := im.ImportReader(ctx, strings.NewReader(gstSalesXML), "tester")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.VouchersPosted != 1 {
		t.Fatalf("expected 1 voucher posted, got %d (skipped %d)", res.VouchersPosted, res.Skipped)
	}

	rep := reporting.NewService(pool)
	gstrSvc := gstr.NewService(pool)
	ms := masters.NewService(pool)

	// A DOCTORED oracle. The TB block is left minimal-but-correct enough that the
	// failures we assert come from the Balance-Sheet and GSTR-1 blocks, not the TB
	// gate. assetsTotal is wrong (debtor is 1180, we claim 9999); HSN@18 IGST is
	// wrong (intra-state has IGST 0, we claim 999).
	exp := migration.ExpectedReconciliation{
		ID:        "doctored",
		GSTIN:     "29AAAAA0000A1Z5",
		StateCode: "29",
		Period:    &migration.Period{From: "2025-05-01", To: "2025-05-31"},
		TaxLedgers: &migration.TaxLedgerNames{
			CGST: "CGST Output", SGST: "SGST Output", IGST: "",
		},
		TrialBalance: migration.ExpectedTrialBalanceDoc{
			// Intentionally empty rows: the TB gate would flag the unexpected ledgers,
			// but we only assert that BALANCE SHEET and GSTR-1 HSN problems appear.
			Totals: migration.ExpectedTotals{Debit: "1180.0000", Credit: "1180.0000", Balanced: true},
		},
		BalanceSheet: &migration.ExpectedBalanceSheet{
			AssetsTotal:      "9999.0000", // WRONG (actual 1180)
			LiabilitiesTotal: "1180.0000",
			NetProfit:        "1000.0000",
			Balanced:         true,
		},
		GSTR1: &migration.ExpectedGSTR1{
			FP:        "052025",
			TaxTotals: migration.ExpectedTaxTotals{Taxable: "1000.0000", CGST: "90.0000", SGST: "90.0000", IGST: "0.0000"},
			HSN: []migration.ExpectedHSNRow{
				{HSN: "", Rate: "18", Taxable: "1000.0000", CGST: "90.0000", SGST: "90.0000", IGST: "999.0000"}, // WRONG IGST
			},
			Balanced: true,
		},
	}

	err = migration.ReconcileFull(ctx, rep, gstrSvc, ms, companyID, exp)
	if err == nil {
		t.Fatal("expected ReconcileFull to FAIL on a doctored oracle, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "BALANCE SHEET") {
		t.Fatalf("error should name BALANCE SHEET, got:\n%v", msg)
	}
	if !strings.Contains(msg, "GSTR-1 HSN") {
		t.Fatalf("error should name GSTR-1 HSN, got:\n%v", msg)
	}
}

// TestImportIdempotentOnGUID proves re-importing the same export is a no-op at
// the (company, guid) grain: the second run skips the voucher and the books are
// unchanged.
func TestImportIdempotentOnGUID(t *testing.T) {
	pool, ctx, companyID := setupCompanyState(t, "29")

	im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool))
	first, err := im.ImportReader(ctx, strings.NewReader(gstSalesXML), "tester")
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.VouchersPosted != 1 || first.Skipped != 0 {
		t.Fatalf("first import: posted=%d skipped=%d, want 1/0", first.VouchersPosted, first.Skipped)
	}

	rep := reporting.NewService(pool)
	tb1, err := rep.TrialBalance(ctx, companyID)
	if err != nil {
		t.Fatalf("tb1: %v", err)
	}

	second, err := im.ImportReader(ctx, strings.NewReader(gstSalesXML), "tester")
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.VouchersPosted != 0 || second.Skipped != 1 {
		t.Fatalf("second import: posted=%d skipped=%d, want 0/1 (skipped guids %v)",
			second.VouchersPosted, second.Skipped, second.SkippedGUIDs)
	}
	if len(second.SkippedGUIDs) != 1 || second.SkippedGUIDs[0] != "rf-sale-0001" {
		t.Fatalf("expected skipped guid rf-sale-0001, got %v", second.SkippedGUIDs)
	}

	tb2, err := rep.TrialBalance(ctx, companyID)
	if err != nil {
		t.Fatalf("tb2: %v", err)
	}
	if !tb1.TotalDebit.Equal(tb2.TotalDebit) || !tb1.TotalCredit.Equal(tb2.TotalCredit) {
		t.Fatalf("re-import changed balances: before Dr %s/Cr %s, after Dr %s/Cr %s",
			tb1.TotalDebit, tb1.TotalCredit, tb2.TotalDebit, tb2.TotalCredit)
	}
}

// TestImportOpeningBalancesFold proves a LEDGER master OPENINGBALANCE lands as
// ledger.opening_balance/opening_dc (folded into the TB by reporting) with the
// correct Tally sign convention: a NEGATIVE OPENINGBALANCE is debit-natured, a
// POSITIVE one credit-natured.
func TestImportOpeningBalancesFold(t *testing.T) {
	pool, ctx, companyID := setupCompanyState(t, "29")

	const openingXML = `<ENVELOPE><BODY><DESC><STATICVARIABLES><SVCURRENTCOMPANY>OB Co</SVCURRENTCOMPANY></STATICVARIABLES></DESC>
	  <DATA>
	    <TALLYMESSAGE><LEDGER NAME="Owner Capital"><PARENT>Capital Account</PARENT><OPENINGBALANCE>100000.00</OPENINGBALANCE></LEDGER></TALLYMESSAGE>
	    <TALLYMESSAGE><LEDGER NAME="Opening Cash"><PARENT>Cash-in-Hand</PARENT><OPENINGBALANCE>-100000.00</OPENINGBALANCE></LEDGER></TALLYMESSAGE>
	  </DATA></BODY></ENVELOPE>`

	im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool))
	if _, err := im.ImportReader(ctx, strings.NewReader(openingXML), "tester"); err != nil {
		t.Fatalf("import openings: %v", err)
	}

	rep := reporting.NewService(pool)
	tb, err := rep.TrialBalance(ctx, companyID)
	if err != nil {
		t.Fatalf("tb: %v", err)
	}
	if !tb.IsBalanced() {
		t.Fatalf("opening-only TB not balanced: Dr %s Cr %s", tb.TotalDebit, tb.TotalCredit)
	}
	want := decimal.RequireFromString("100000.0000")
	var sawCapital, sawCash bool
	for _, r := range tb.Rows {
		switch r.LedgerName {
		case "Owner Capital":
			sawCapital = true
			if !r.Credit.Equal(want) || !r.Debit.IsZero() {
				t.Fatalf("Owner Capital expected Cr %s/Dr 0, got Dr %s/Cr %s", want, r.Debit, r.Credit)
			}
		case "Opening Cash":
			sawCash = true
			if !r.Debit.Equal(want) || !r.Credit.IsZero() {
				t.Fatalf("Opening Cash expected Dr %s/Cr 0, got Dr %s/Cr %s", want, r.Debit, r.Credit)
			}
		}
	}
	if !sawCapital || !sawCash {
		t.Fatalf("opening ledgers missing from TB (capital=%v cash=%v)", sawCapital, sawCash)
	}
}
