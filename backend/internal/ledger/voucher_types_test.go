package ledger_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
)

func dec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	return decimal.RequireFromString(s)
}

// ledgerBalance reads a ledger's (debit, credit) from ledger_balance in scope.
func ledgerBalance(t *testing.T, pool *db.Pool, ctx context.Context, id uuid.UUID) (decimal.Decimal, decimal.Decimal) {
	t.Helper()
	var d, c decimal.Decimal
	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT debit, credit FROM ledger_balance WHERE ledger_id = $1`, id).Scan(&d, &c)
	})
	if err != nil {
		t.Fatalf("read ledger_balance %s: %v", id, err)
	}
	return d, c
}

// TestSixVoucherTypes posts one voucher of each Phase 1 class and asserts the
// postings balance and the ledger_balance reflects each leg.
func TestSixVoucherTypes(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, _, _ := setup(t)
	m := masters.NewService(pool)
	led := ledger.NewService(pool)

	// Resolve seeded voucher types and create the working ledgers.
	vt := func(name string) uuid.UUID {
		id, err := m.VoucherTypeID(ctx, name)
		if err != nil {
			t.Fatalf("voucher type %s: %v", name, err)
		}
		return id
	}
	cashID, err := m.LedgerID(ctx, "Cash")
	if err != nil {
		t.Fatalf("cash: %v", err)
	}
	bank, err := m.CreateLedger(ctx, masters.Ledger{Name: "HDFC Bank", GroupName: "Bank Accounts"}, "tester")
	if err != nil {
		t.Fatalf("bank: %v", err)
	}
	creditor, err := m.CreateLedger(ctx, masters.Ledger{Name: "Supplier A", GroupName: "Sundry Creditors"}, "tester")
	if err != nil {
		t.Fatalf("creditor: %v", err)
	}
	rent, err := m.CreateLedger(ctx, masters.Ledger{Name: "Rent", GroupName: "Indirect Expenses"}, "tester")
	if err != nil {
		t.Fatalf("rent: %v", err)
	}
	debtor, err := m.CreateLedger(ctx, masters.Ledger{Name: "Customer B", GroupName: "Sundry Debtors"}, "tester")
	if err != nil {
		t.Fatalf("debtor: %v", err)
	}
	purch, err := m.CreateLedger(ctx, masters.Ledger{Name: "Local Purchase", GroupName: "Purchase Accounts"}, "tester")
	if err != nil {
		t.Fatalf("purch: %v", err)
	}

	post := func(in ledger.VoucherInput) ledger.PostResult {
		t.Helper()
		res, err := led.PostVoucher(ctx, in)
		if err != nil {
			t.Fatalf("post %s: %v", in.Class, err)
		}
		if res.VersionNo != 1 {
			t.Fatalf("%s: expected version 1, got %d", in.Class, res.VersionNo)
		}
		return res
	}

	// Contra (F4): Cash -> Bank deposit of 5000. Bank Dr, Cash Cr.
	post(ledger.VoucherInput{
		Class: ledger.Contra, VoucherTypeID: vt("Contra"), VoucherNo: "C1",
		VoucherDate: "2025-05-01", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: bank, DrCr: ledger.Debit, Amount: dec(t, "5000.0000")},
			{LedgerID: cashID, DrCr: ledger.Credit, Amount: dec(t, "5000.0000")},
		},
	})

	// Payment (F5): pay creditor 2000 from bank. Creditor Dr, Bank Cr.
	post(ledger.VoucherInput{
		Class: ledger.Payment, VoucherTypeID: vt("Payment"), VoucherNo: "P1",
		VoucherDate: "2025-05-02", PartyLedgerID: creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: creditor, DrCr: ledger.Debit, Amount: dec(t, "2000.0000")},
			{LedgerID: bank, DrCr: ledger.Credit, Amount: dec(t, "2000.0000")},
		},
	})

	// Receipt (F6): receive 3000 from debtor into cash. Cash Dr, Debtor Cr.
	post(ledger.VoucherInput{
		Class: ledger.Receipt, VoucherTypeID: vt("Receipt"), VoucherNo: "R1",
		VoucherDate: "2025-05-03", PartyLedgerID: debtor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: cashID, DrCr: ledger.Debit, Amount: dec(t, "3000.0000")},
			{LedgerID: debtor, DrCr: ledger.Credit, Amount: dec(t, "3000.0000")},
		},
	})

	// Journal (F7): book rent payable 1500. Rent Dr, Creditor Cr.
	post(ledger.VoucherInput{
		Class: ledger.Journal, VoucherTypeID: vt("Journal"), VoucherNo: "J1",
		VoucherDate: "2025-05-04", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: rent, DrCr: ledger.Debit, Amount: dec(t, "1500.0000")},
			{LedgerID: creditor, DrCr: ledger.Credit, Amount: dec(t, "1500.0000")},
		},
	})

	// Purchase (F9): buy goods 1000 + intra GST 18% from creditor.
	cgstIn, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Input CGST", GroupName: "Duties & Taxes"}, "tester")
	sgstIn, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Input SGST", GroupName: "Duties & Taxes"}, "tester")
	post(ledger.VoucherInput{
		Class: ledger.Purchase, VoucherTypeID: vt("Purchase"), VoucherNo: "PU1",
		VoucherDate: "2025-05-05", PartyLedgerID: creditor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: purch, DrCr: ledger.Debit, Amount: dec(t, "1000.0000")},
			{LedgerID: cgstIn, DrCr: ledger.Debit, Amount: dec(t, "90.0000")},
			{LedgerID: sgstIn, DrCr: ledger.Debit, Amount: dec(t, "90.0000")},
			{LedgerID: creditor, DrCr: ledger.Credit, Amount: dec(t, "1180.0000")},
		},
		GST: &ledger.GSTParams{
			RulesetVersion:         "gst-2024-09",
			SupplierStateCode:      "27",
			PlaceOfSupplyStateCode: "27",
			Lines:                  []compliance.Line{{TaxableValue: dec(t, "1000.0000"), GSTRatePct: dec(t, "18.00"), HSN: "8471"}},
			CGSTLedgerID:           cgstIn,
			SGSTLedgerID:           sgstIn,
		},
	})

	// Sales (F8): sell 2000 + intra GST 18% to debtor.
	salesAcct, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Domestic Sales", GroupName: "Sales Accounts"}, "tester")
	cgstOut, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Output CGST", GroupName: "Duties & Taxes"}, "tester")
	sgstOut, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Output SGST", GroupName: "Duties & Taxes"}, "tester")
	post(ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: vt("Sales"), VoucherNo: "S1",
		VoucherDate: "2025-05-06", PartyLedgerID: debtor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: debtor, DrCr: ledger.Debit, Amount: dec(t, "2360.0000")},
			{LedgerID: salesAcct, DrCr: ledger.Credit, Amount: dec(t, "2000.0000")},
			{LedgerID: cgstOut, DrCr: ledger.Credit, Amount: dec(t, "180.0000")},
			{LedgerID: sgstOut, DrCr: ledger.Credit, Amount: dec(t, "180.0000")},
		},
		GST: &ledger.GSTParams{
			RulesetVersion:         "gst-2024-09",
			SupplierStateCode:      "27",
			PlaceOfSupplyStateCode: "27",
			Lines:                  []compliance.Line{{TaxableValue: dec(t, "2000.0000"), GSTRatePct: dec(t, "18.00"), HSN: "8471"}},
			CGSTLedgerID:           cgstOut,
			SGSTLedgerID:           sgstOut,
		},
	})

	// Assert a few ledger balances.
	// Cash: Contra Cr 5000, Receipt Dr 3000 -> debit 3000, credit 5000.
	d, c := ledgerBalance(t, pool, ctx, cashID)
	if !d.Equal(dec(t, "3000.0000")) || !c.Equal(dec(t, "5000.0000")) {
		t.Fatalf("cash balance: dr=%s cr=%s", d, c)
	}
	// Bank: Contra Dr 5000, Payment Cr 2000.
	d, c = ledgerBalance(t, pool, ctx, bank)
	if !d.Equal(dec(t, "5000.0000")) || !c.Equal(dec(t, "2000.0000")) {
		t.Fatalf("bank balance: dr=%s cr=%s", d, c)
	}
	// Debtor: Receipt Cr 3000, Sales Dr 2360.
	d, c = ledgerBalance(t, pool, ctx, debtor)
	if !d.Equal(dec(t, "2360.0000")) || !c.Equal(dec(t, "3000.0000")) {
		t.Fatalf("debtor balance: dr=%s cr=%s", d, c)
	}
}

// TestSalesDerivesGSTMatchingCorpus posts a Sales voucher whose GST params come
// from the intra-state-b2b-18 corpus case and asserts the derived split matches
// the corpus expectation.
func TestSalesDerivesGSTMatchingCorpus(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, _, _ := setup(t)
	m := masters.NewService(pool)
	led := ledger.NewService(pool)

	c := loadCorpus(t, "intra-state-b2b-18")

	vtID, _ := m.VoucherTypeID(ctx, "Sales")
	debtor, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Corpus Debtor", GroupName: "Sundry Debtors"}, "tester")
	salesAcct, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Corpus Sales", GroupName: "Sales Accounts"}, "tester")
	cgst, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Corpus Output CGST", GroupName: "Duties & Taxes"}, "tester")
	sgst, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Corpus Output SGST", GroupName: "Duties & Taxes"}, "tester")

	taxable := dec(t, c.Input.Lines[0].TaxableValue)
	wantCGST := dec(t, c.Expected.CGST)
	wantSGST := dec(t, c.Expected.SGST)
	total := dec(t, c.Expected.InvoiceTotal)

	res, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: vtID, VoucherNo: "GS1",
		VoucherDate: "2025-05-10", PartyLedgerID: debtor, Party: "Corpus Debtor", CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: debtor, DrCr: ledger.Debit, Amount: total},
			{LedgerID: salesAcct, DrCr: ledger.Credit, Amount: taxable},
			{LedgerID: cgst, DrCr: ledger.Credit, Amount: wantCGST},
			{LedgerID: sgst, DrCr: ledger.Credit, Amount: wantSGST},
		},
		GST: &ledger.GSTParams{
			RulesetVersion:         c.RulesetVersion,
			SupplierStateCode:      c.Input.SupplierStateCode,
			PlaceOfSupplyStateCode: c.Input.PlaceOfSupplyStateCode,
			ReverseCharge:          c.Input.ReverseCharge,
			Lines:                  []compliance.Line{{TaxableValue: taxable, GSTRatePct: dec(t, c.Input.Lines[0].GSTRatePct)}},
			CGSTLedgerID:           cgst,
			SGSTLedgerID:           sgst,
		},
	})
	if err != nil {
		t.Fatalf("post sale: %v", err)
	}
	if !res.Tax.CGST.Equal(wantCGST) {
		t.Fatalf("derived CGST %s != corpus %s", res.Tax.CGST, wantCGST)
	}
	if !res.Tax.SGST.Equal(wantSGST) {
		t.Fatalf("derived SGST %s != corpus %s", res.Tax.SGST, wantSGST)
	}
	if !res.Tax.IGST.Equal(dec(t, c.Expected.IGST)) {
		t.Fatalf("derived IGST %s != corpus %s", res.Tax.IGST, c.Expected.IGST)
	}
	if !res.Tax.InvoiceTotal.Equal(total) {
		t.Fatalf("derived invoice total %s != corpus %s", res.Tax.InvoiceTotal, total)
	}

	// The derived tax must also be persisted on the version row for GSTR.
	var dbCGST, dbSGST decimal.Decimal
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT cgst, sgst FROM voucher_version WHERE id = $1`, res.VoucherVersionID).Scan(&dbCGST, &dbSGST)
	})
	if err != nil {
		t.Fatalf("read version gst: %v", err)
	}
	if !dbCGST.Equal(wantCGST) || !dbSGST.Equal(wantSGST) {
		t.Fatalf("persisted gst cgst=%s sgst=%s, want %s/%s", dbCGST, dbSGST, wantCGST, wantSGST)
	}
}

// TestSalesGSTMismatchFailsPost asserts that a tax posting inconsistent with the
// derived split is rejected (the engine guards the books).
func TestSalesGSTMismatchFailsPost(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, _, _ := setup(t)
	m := masters.NewService(pool)
	led := ledger.NewService(pool)

	vtID, _ := m.VoucherTypeID(ctx, "Sales")
	debtor, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Mismatch Debtor", GroupName: "Sundry Debtors"}, "tester")
	salesAcct, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Mismatch Sales", GroupName: "Sales Accounts"}, "tester")
	cgst, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Mismatch CGST", GroupName: "Duties & Taxes"}, "tester")
	sgst, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Mismatch SGST", GroupName: "Duties & Taxes"}, "tester")

	// Post wrong CGST (80 instead of 90) but keep the voucher balanced.
	_, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: vtID, VoucherNo: "MM1",
		VoucherDate: "2025-05-11", PartyLedgerID: debtor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: debtor, DrCr: ledger.Debit, Amount: dec(t, "1170.0000")},
			{LedgerID: salesAcct, DrCr: ledger.Credit, Amount: dec(t, "1000.0000")},
			{LedgerID: cgst, DrCr: ledger.Credit, Amount: dec(t, "80.0000")},
			{LedgerID: sgst, DrCr: ledger.Credit, Amount: dec(t, "90.0000")},
		},
		GST: &ledger.GSTParams{
			SupplierStateCode: "27", PlaceOfSupplyStateCode: "27",
			Lines:        []compliance.Line{{TaxableValue: dec(t, "1000.0000"), GSTRatePct: dec(t, "18.00")}},
			CGSTLedgerID: cgst, SGSTLedgerID: sgst,
		},
	})
	if err == nil {
		t.Fatal("expected GST mismatch to fail the post")
	}
}

// TestBillAllocationCreatesOutstanding posts a Sales voucher with a New Ref
// bill-wise allocation and asserts the bill_allocation row is created.
func TestBillAllocationCreatesOutstanding(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, _, _ := setup(t)
	m := masters.NewService(pool)
	led := ledger.NewService(pool)

	vtID, _ := m.VoucherTypeID(ctx, "Sales")
	debtor, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Bill Debtor", GroupName: "Sundry Debtors", MaintainBillByBill: true}, "tester")
	salesAcct, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Bill Sales", GroupName: "Sales Accounts"}, "tester")

	res, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class: ledger.Sales, VoucherTypeID: vtID, VoucherNo: "B1",
		VoucherDate: "2025-05-12", PartyLedgerID: debtor, CreatedBy: "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: debtor, DrCr: ledger.Debit, Amount: dec(t, "1000.0000")},
			{LedgerID: salesAcct, DrCr: ledger.Credit, Amount: dec(t, "1000.0000")},
		},
		BillAllocations: []ledger.BillAllocationInput{
			{LedgerID: debtor, RefName: "INV-B1", RefType: ledger.RefNew, DrCr: ledger.Debit,
				Amount: dec(t, "1000.0000"), DueDate: "2025-06-11"},
		},
	})
	if err != nil {
		t.Fatalf("post sale with bill: %v", err)
	}

	var refName string
	var amount decimal.Decimal
	var dueDate *string
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT ref_name, amount, due_date::text FROM bill_allocation
			   WHERE voucher_version_id = $1 AND ledger_id = $2`,
			res.VoucherVersionID, debtor).Scan(&refName, &amount, &dueDate)
	})
	if err != nil {
		t.Fatalf("read bill_allocation: %v", err)
	}
	if refName != "INV-B1" || !amount.Equal(dec(t, "1000.0000")) {
		t.Fatalf("unexpected bill allocation ref=%s amount=%s", refName, amount)
	}
	if dueDate == nil || *dueDate != "2025-06-11" {
		t.Fatalf("unexpected due date %v", dueDate)
	}
}

// TestAlterVoucherPreservesImmutability posts a Payment then alters it, and
// asserts both versions coexist with a valid hash chain (immutability).
func TestAlterVoucherPreservesImmutability(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, _, _ := setup(t)
	m := masters.NewService(pool)
	led := ledger.NewService(pool)

	vtID, _ := m.VoucherTypeID(ctx, "Payment")
	cashID, _ := m.LedgerID(ctx, "Cash")
	creditor, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Alter Creditor", GroupName: "Sundry Creditors"}, "tester")

	mk := func(amount string) ledger.VoucherInput {
		a := dec(t, amount)
		return ledger.VoucherInput{
			Class: ledger.Payment, VoucherTypeID: vtID, VoucherNo: "AP1",
			VoucherDate: "2025-05-20", PartyLedgerID: creditor, CreatedBy: "tester",
			Postings: []ledger.PostingInput{
				{LedgerID: creditor, DrCr: ledger.Debit, Amount: a},
				{LedgerID: cashID, DrCr: ledger.Credit, Amount: a},
			},
		}
	}

	v1, err := led.PostVoucher(ctx, mk("400.0000"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	v2, err := led.AlterVoucher(ctx, v1.VoucherID, mk("650.0000"))
	if err != nil {
		t.Fatalf("alter: %v", err)
	}
	if v2.VersionNo != 2 || v2.VoucherID != v1.VoucherID {
		t.Fatalf("alter must reuse voucher id and bump version: %+v", v2)
	}
	if v2.VoucherVersionID == v1.VoucherVersionID {
		t.Fatal("alter must create a new immutable version row")
	}

	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM voucher_version WHERE voucher_id=$1`, v1.VoucherID).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Fatalf("expected 2 coexisting versions, got %d", count)
		}
		var v1Hash, v2Prev []byte
		if err := tx.QueryRow(ctx,
			`SELECT hash FROM voucher_version WHERE voucher_id=$1 AND version_no=1`, v1.VoucherID).Scan(&v1Hash); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`SELECT prev_hash FROM voucher_version WHERE voucher_id=$1 AND version_no=2`, v1.VoucherID).Scan(&v2Prev); err != nil {
			return err
		}
		if string(v1Hash) != string(v2Prev) || len(v2Prev) == 0 {
			t.Fatal("hash chain broken across alter")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify chain: %v", err)
	}

	// Attempting to UPDATE the immutable version directly must be rejected.
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE voucher_version SET narration='hack' WHERE id=$1`, v1.VoucherVersionID)
		return e
	})
	if err == nil {
		t.Fatal("expected immutability trigger to reject UPDATE on voucher_version")
	}
}

// TestAlterVoucherCorrectsBalance is the regression guard for the 0006 fix: an
// ALTER must restate ledger_balance to reflect ONLY the new version, not the
// sum of all versions. Before the fix, editing a 400 payment to 650 left the
// cash credit at 1050 (400+650) instead of 650.
func TestAlterVoucherCorrectsBalance(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	pool, ctx, _, _ := setup(t)
	m := masters.NewService(pool)
	led := ledger.NewService(pool)

	vtID, _ := m.VoucherTypeID(ctx, "Payment")
	cashID, _ := m.LedgerID(ctx, "Cash")
	creditor, _ := m.CreateLedger(ctx, masters.Ledger{Name: "Balance Creditor", GroupName: "Sundry Creditors"}, "tester")

	mk := func(amount string) ledger.VoucherInput {
		a := dec(t, amount)
		return ledger.VoucherInput{
			Class: ledger.Payment, VoucherTypeID: vtID, VoucherNo: "BP1",
			VoucherDate: "2025-06-01", PartyLedgerID: creditor, CreatedBy: "tester",
			Postings: []ledger.PostingInput{
				{LedgerID: creditor, DrCr: ledger.Debit, Amount: a},
				{LedgerID: cashID, DrCr: ledger.Credit, Amount: a},
			},
		}
	}

	v1, err := led.PostVoucher(ctx, mk("400.0000"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	// After the original post: cash credited 400, creditor debited 400.
	if d, c := ledgerBalance(t, pool, ctx, cashID); !c.Equal(dec(t, "400.0000")) || !d.IsZero() {
		t.Fatalf("after post: cash want credit 400 debit 0, got debit=%s credit=%s", d, c)
	}

	if _, err := led.AlterVoucher(ctx, v1.VoucherID, mk("650.0000")); err != nil {
		t.Fatalf("alter: %v", err)
	}
	// After altering to 650, the balance must reflect ONLY 650 — not 1050.
	if d, c := ledgerBalance(t, pool, ctx, cashID); !c.Equal(dec(t, "650.0000")) || !d.IsZero() {
		t.Fatalf("after alter: cash want credit 650 debit 0 (not double-counted), got debit=%s credit=%s", d, c)
	}
	if d, c := ledgerBalance(t, pool, ctx, creditor); !d.Equal(dec(t, "650.0000")) || !c.IsZero() {
		t.Fatalf("after alter: creditor want debit 650 credit 0, got debit=%s credit=%s", d, c)
	}

	// voucher_head must point at version 2 for this voucher.
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var versionNo int
		var deleted bool
		if err := tx.QueryRow(ctx,
			`SELECT version_no, is_deleted FROM voucher_head WHERE voucher_id=$1`,
			v1.VoucherID).Scan(&versionNo, &deleted); err != nil {
			return err
		}
		if versionNo != 2 || deleted {
			t.Fatalf("voucher_head should point at version 2, not deleted; got v%d deleted=%v", versionNo, deleted)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify head: %v", err)
	}
}

// --- corpus loader (test-local) ---

type taxCorpus struct {
	ID             string `json:"id"`
	RulesetVersion string `json:"rulesetVersion"`
	Input          struct {
		SupplierStateCode      string `json:"supplierStateCode"`
		PlaceOfSupplyStateCode string `json:"placeOfSupplyStateCode"`
		ReverseCharge          bool   `json:"reverseCharge"`
		Lines                  []struct {
			TaxableValue string `json:"taxableValue"`
			GSTRatePct   string `json:"gstRatePct"`
			HSN          string `json:"hsn"`
		} `json:"lines"`
	} `json:"input"`
	Expected struct {
		CGST         string `json:"cgst"`
		SGST         string `json:"sgst"`
		IGST         string `json:"igst"`
		TotalTax     string `json:"totalTax"`
		InvoiceTotal string `json:"invoiceTotal"`
	} `json:"expected"`
}

func loadCorpus(t *testing.T, id string) taxCorpus {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		path := filepath.Join(dir, "corpora", "tax", id+".json")
		if raw, err := os.ReadFile(path); err == nil {
			var c taxCorpus
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatalf("unmarshal %s: %v", path, err)
			}
			return c
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate corpora/tax/%s.json", id)
	return taxCorpus{}
}
