package gstr_test

// REGRESSION: altering a voucher must NOT erase its voucher_type_id.
//
// The bug: ledger.post() resolved the default voucher type ONLY on OpCreate.
// On OpAlter, in.VoucherTypeID stayed uuid.Nil unless the caller happened to
// supply one, so the new version row landed with voucher_type_id = NULL --
// even though the code comment claimed "ALTER/DELETE keep whatever the prior
// version carried". (DeleteVoucher genuinely does carry it forward; ALTER had
// no such assignment anywhere.)
//
// Why that is not cosmetic: gstr1.go and gstr3b.go both reach the voucher
// class through an INNER JOIN on voucher_type_id. A NULL there eliminates the
// row, so the CURRENT version of any altered sales invoice silently vanishes
// from GSTR-1 and GSTR-3B while remaining fully present in the Trial Balance
// and P&L. A user who corrects an invoice under-reports it on their return
// with no error anywhere.
//
// NOTE ON THE ORACLE: gstr.Reconcile is NOT a valid oracle for this bug. Its
// "posted tax" side (gstr1.go:399) joins voucher_type the same way, so a
// dropped voucher disappears from BOTH sides and the reconciliation still
// reports balanced while both numbers are wrong. Every assertion below is
// therefore against ABSOLUTE expected amounts, independently computed here.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
)

// gstSaleAt posts one Sales voucher at an arbitrary GST rate and returns the
// logical voucher id, so a later ALTER can target it. Deliberately independent
// of gstSale (which is pinned to 18%) so this file's arithmetic is its own.
func gstSaleAt(t *testing.T, led *ledger.Service, ctx context.Context,
	vtID, party, sales, cgst, sgst, igst uuid.UUID,
	no, date, pos, taxable, ratePct string, intra bool,
) uuid.UUID {
	t.Helper()
	res, err := led.PostVoucher(ctx, buildSale(vtID, party, sales, cgst, sgst, igst,
		no, date, pos, taxable, ratePct, intra))
	if err != nil {
		t.Fatalf("post sale %s: %v", no, err)
	}
	return res.VoucherID
}

// buildSale assembles a balanced, GST-bearing Sales voucher input. Shared by
// the post and the alter so the alter is a true restatement of the same shape.
func buildSale(vtID, party, sales, cgst, sgst, igst uuid.UUID,
	no, date, pos, taxable, ratePct string, intra bool,
) ledger.VoucherInput {
	tx := dec(taxable)
	rate := dec(ratePct)
	tax := tx.Mul(rate).Div(dec("100")).Round(4)

	postings := []ledger.PostingInput{
		{LedgerID: sales, DrCr: ledger.Credit, Amount: tx},
	}
	var total decimal.Decimal
	if intra {
		half := tax.Div(dec("2")).Round(4)
		postings = append(postings,
			ledger.PostingInput{LedgerID: cgst, DrCr: ledger.Credit, Amount: half},
			ledger.PostingInput{LedgerID: sgst, DrCr: ledger.Credit, Amount: half},
		)
		total = tx.Add(half).Add(half)
	} else {
		postings = append(postings,
			ledger.PostingInput{LedgerID: igst, DrCr: ledger.Credit, Amount: tax},
		)
		total = tx.Add(tax)
	}
	postings = append([]ledger.PostingInput{
		{LedgerID: party, DrCr: ledger.Debit, Amount: total},
	}, postings...)

	return ledger.VoucherInput{
		Class: ledger.Sales,
		// VoucherTypeID is supplied on the CREATE only. The ALTER deliberately
		// leaves it zero -- that is exactly the caller shape (the UI, the seed
		// script, alterSalesVoucher) that triggered the bug.
		VoucherTypeID: vtID,
		PartyLedgerID: party,
		VoucherNo:     no,
		VoucherDate:   date,
		Party:         "party",
		CreatedBy:     "tester",
		Postings:      postings,
		GST: &ledger.GSTParams{
			SupplierStateCode:      supplierState,
			PlaceOfSupplyStateCode: pos,
			Lines: []compliance.Line{
				{TaxableValue: tx, GSTRatePct: rate, HSN: "8471"},
			},
			CGSTLedgerID: cgst,
			SGSTLedgerID: sgst,
			IGSTLedgerID: igst,
		},
	}
}

// currentVersionTypeID reads the voucher_type_id of the voucher's CURRENT
// version straight from the table -- the exact column the GSTR joins key on.
func currentVersionTypeID(t *testing.T, pool *db.Pool, ctx context.Context, voucherID uuid.UUID) (uuid.UUID, bool) {
	t.Helper()
	var (
		vtID    *uuid.UUID
		version int
	)
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT vv.voucher_type_id, vv.version_no
			   FROM voucher_head vh
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			  WHERE vh.voucher_id = $1`, voucherID).Scan(&vtID, &version)
	}); err != nil {
		t.Fatalf("read current version type: %v", err)
	}
	if version < 2 {
		t.Fatalf("expected the altered version to be current, got version %d", version)
	}
	if vtID == nil {
		return uuid.Nil, false
	}
	return *vtID, true
}

// salesFixture creates the ledgers every test below shares.
type salesFixture struct {
	vtID                    uuid.UUID
	cgst, sgst, igst, sales uuid.UUID
	acme, mumbai            uuid.UUID
}

func newSalesFixture(t *testing.T, pool *db.Pool, ctx context.Context) salesFixture {
	t.Helper()
	ms := masters.NewService(pool)

	must := func(id uuid.UUID, err error) uuid.UUID {
		t.Helper()
		if err != nil {
			t.Fatalf("master: %v", err)
		}
		return id
	}

	f := salesFixture{}
	f.vtID = must(ms.VoucherTypeID(ctx, "Sales"))
	f.cgst = must(ms.EnsureLedger(ctx, "Output CGST", "Duties & Taxes", "tester"))
	f.sgst = must(ms.EnsureLedger(ctx, "Output SGST", "Duties & Taxes", "tester"))
	f.igst = must(ms.EnsureLedger(ctx, "Output IGST", "Duties & Taxes", "tester"))
	f.sales = must(ms.EnsureLedger(ctx, "Domestic Sales", "Sales Accounts", "tester"))

	f.acme = must(ms.CreateLedger(ctx, masters.Ledger{
		Name: "Acme Traders", GroupName: "Sundry Debtors",
		GSTIN: "29AACCA1234F1Z5", StateCode: "29",
		RegistrationType: masters.GSTRegular,
	}, "tester"))
	f.mumbai = must(ms.CreateLedger(ctx, masters.Ledger{
		Name: "Mumbai Metals", GroupName: "Sundry Debtors",
		GSTIN: "27AACCM5678G1Z2", StateCode: "27",
		RegistrationType: masters.GSTRegular,
	}, "tester"))
	return f
}

// ---------------------------------------------------------------------------
// 1 + 2 + 3: an ALTER keeps the voucher type of the version it supersedes.
// ---------------------------------------------------------------------------

func TestAlterCarriesVoucherTypeForward(t *testing.T) {
	pool, ctx, _ := setup(t)
	led := ledger.NewService(pool)
	f := newSalesFixture(t, pool, ctx)

	vID := gstSaleAt(t, led, ctx, f.vtID, f.acme, f.sales, f.cgst, f.sgst, f.igst,
		"ALT-1", "2025-06-19", "29", "24000.0000", "12", true)

	// Alter the amount. VoucherTypeID is intentionally left zero: the caller
	// restates the body, not the type -- exactly what the UI and seed do.
	altered := buildSale(uuid.Nil, f.acme, f.sales, f.cgst, f.sgst, f.igst,
		"ALT-1", "2025-06-19", "29", "26500.0000", "12", true)
	if _, err := led.AlterVoucher(ctx, vID, altered); err != nil {
		t.Fatalf("alter: %v", err)
	}

	gotType, ok := currentVersionTypeID(t, pool, ctx, vID)
	if !ok {
		t.Fatal("REGRESSION: altered version has voucher_type_id = NULL; " +
			"it will be dropped by the INNER JOIN in gstr1.go/gstr3b.go")
	}
	if gotType != f.vtID {
		t.Fatalf("altered version voucher_type_id = %s, want the original %s", gotType, f.vtID)
	}
}

// ---------------------------------------------------------------------------
// SELF-HEALING FALLBACK: altering a voucher whose current version ALREADY has
// voucher_type_id = NULL (a row written before the fix landed) must RESOLVE the
// class default rather than propagate the NULL forward.
//
// Without this branch the fix would only protect vouchers created after it
// shipped: every already-corrupted voucher in a live database would stay
// invisible to GSTR-1 no matter how many times it was corrected.
//
// The corrupt state is created DIRECTLY rather than by re-introducing the bug,
// so the test still means something once the bug can no longer be triggered.
// voucher_version is append-only (a BEFORE UPDATE OR DELETE trigger), so the
// UPDATE runs on a superuser connection under
// `SET LOCAL session_replication_role = replica`, which suppresses user
// triggers for THAT SESSION ONLY. Deliberately not `ALTER TABLE ... DISABLE
// TRIGGER`: that is table-wide and would race with
// internal/ledger/voucher_types_test.go, which asserts the very same trigger
// rejects an UPDATE, whenever the two packages run concurrently.
// ---------------------------------------------------------------------------

func TestAlterRepairsNullVoucherTypeFromPreFixRow(t *testing.T) {
	pool, ctx, _ := setup(t)
	led := ledger.NewService(pool)
	f := newSalesFixture(t, pool, ctx)

	vID := gstSaleAt(t, led, ctx, f.vtID, f.acme, f.sales, f.cgst, f.sgst, f.igst,
		"HEAL-1", "2025-07-10", "29", "10000.0000", "18", true)

	// --- simulate the pre-fix corrupted row -------------------------------
	var currentVersionID uuid.UUID
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT current_version_id FROM voucher_head WHERE voucher_id = $1`,
			vID).Scan(&currentVersionID)
	}); err != nil {
		t.Fatalf("read head: %v", err)
	}

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	atx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("admin begin: %v", err)
	}
	if _, err := atx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		_ = atx.Rollback(ctx)
		t.Fatalf("suppress triggers: %v", err)
	}
	tag, err := atx.Exec(ctx,
		`UPDATE voucher_version SET voucher_type_id = NULL WHERE id = $1`, currentVersionID)
	if err != nil {
		_ = atx.Rollback(ctx)
		t.Fatalf("corrupt row: %v", err)
	}
	if tag.RowsAffected() != 1 {
		_ = atx.Rollback(ctx)
		t.Fatalf("corrupt row: affected %d rows, want 1", tag.RowsAffected())
	}
	if err := atx.Commit(ctx); err != nil {
		t.Fatalf("admin commit: %v", err)
	}

	// Guard: the simulated corruption must actually be in place, otherwise the
	// assertion below would pass for the wrong reason.
	if _, ok := currentVersionTypeIDAt(t, pool, ctx, currentVersionID); ok {
		t.Fatal("setup failed: voucher_type_id is still set; the test would be vacuous")
	}

	// --- alter it: the fallback must resolve the class default ------------
	if _, err := led.AlterVoucher(ctx, vID, buildSale(uuid.Nil,
		f.acme, f.sales, f.cgst, f.sgst, f.igst,
		"HEAL-1", "2025-07-10", "29", "12000.0000", "18", true)); err != nil {
		t.Fatalf("alter: %v", err)
	}

	gotType, ok := currentVersionTypeID(t, pool, ctx, vID)
	if !ok {
		t.Fatal("altered version STILL has voucher_type_id = NULL; " +
			"the pre-fix-row fallback did not fire")
	}
	if gotType != f.vtID {
		t.Fatalf("repaired voucher_type_id = %s, want the Sales default %s", gotType, f.vtID)
	}

	// And the repair is observable where it matters: the voucher is back in
	// GSTR-1 with its restated amount.
	g, _, err := gstr.NewService(pool).Reconcile(ctx, "2025-07-01", "2025-07-31", gstr.Options{
		GSTIN:        "29ZZZZZ0000Z1Z5",
		CGSTLedgerID: f.cgst, SGSTLedgerID: f.sgst, IGSTLedgerID: f.igst,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(g.B2B) != 1 || len(g.B2B[0].Inv) != 1 {
		t.Fatalf("repaired invoice missing from GSTR-1 b2b: %+v", g.B2B)
	}
	if d := g.B2B[0].Inv[0].Itms[0].ItmDet; !d.Txval.Equal(dec("12000")) {
		t.Fatalf("b2b txval: got %s, want the altered 12000", d.Txval)
	}
}

// currentVersionTypeIDAt reads voucher_type_id of ONE named version row (not
// whichever is current), for asserting the simulated pre-fix state.
func currentVersionTypeIDAt(t *testing.T, pool *db.Pool, ctx context.Context, versionID uuid.UUID) (uuid.UUID, bool) {
	t.Helper()
	var vtID *uuid.UUID
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT voucher_type_id FROM voucher_version WHERE id = $1`, versionID).Scan(&vtID)
	}); err != nil {
		t.Fatalf("read version type: %v", err)
	}
	if vtID == nil {
		return uuid.Nil, false
	}
	return *vtID, true
}

// ---------------------------------------------------------------------------
// 4: the altered voucher is still visible to the GSTR-1 query for its period.
// ---------------------------------------------------------------------------

func TestAlteredSaleStillAppearsInGSTR1(t *testing.T) {
	pool, ctx, _ := setup(t)
	led := ledger.NewService(pool)
	f := newSalesFixture(t, pool, ctx)

	vID := gstSaleAt(t, led, ctx, f.vtID, f.acme, f.sales, f.cgst, f.sgst, f.igst,
		"ALT-2", "2025-06-19", "29", "24000.0000", "12", true)
	altered := buildSale(uuid.Nil, f.acme, f.sales, f.cgst, f.sgst, f.igst,
		"ALT-2", "2025-06-19", "29", "26500.0000", "12", true)
	if _, err := led.AlterVoucher(ctx, vID, altered); err != nil {
		t.Fatalf("alter: %v", err)
	}

	g, _, err := gstr.NewService(pool).Reconcile(ctx, "2025-06-01", "2025-06-30", gstr.Options{
		GSTIN:        "29ZZZZZ0000Z1Z5",
		CGSTLedgerID: f.cgst, SGSTLedgerID: f.sgst, IGSTLedgerID: f.igst,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Acme is a registered party, so the invoice belongs in b2b.
	if len(g.B2B) != 1 || len(g.B2B[0].Inv) != 1 {
		t.Fatalf("REGRESSION: altered invoice missing from GSTR-1 b2b: %+v", g.B2B)
	}
	inv := g.B2B[0].Inv[0]
	if inv.Inum != "ALT-2" {
		t.Fatalf("b2b inum: got %s, want ALT-2", inv.Inum)
	}
	// The RESTATED amount must be reported, not the superseded one.
	d := inv.Itms[0].ItmDet
	if !d.Txval.Equal(dec("26500")) {
		t.Fatalf("b2b txval: got %s, want the altered 26500 (not the original 24000)", d.Txval)
	}
	if !d.Camt.Equal(dec("1590")) || !d.Samt.Equal(dec("1590")) {
		t.Fatalf("b2b tax: c=%s s=%s, want 1590/1590", d.Camt, d.Samt)
	}
}

// ---------------------------------------------------------------------------
// 5: the exact INV-003 scenario from the Demo Traders audit.
//
// Three sales; the third is altered 24,000 -> 26,500 @12%. Before the fix the
// GSTR-1 taxable total was 1,30,000 (the altered invoice silently gone) while
// the P&L showed 1,56,500 of sales. After the fix both are 1,56,500.
// ---------------------------------------------------------------------------

func TestGSTR1TotalIncludesAlteredInvoice_INV003Scenario(t *testing.T) {
	pool, ctx, _ := setup(t)
	led := ledger.NewService(pool)
	f := newSalesFixture(t, pool, ctx)

	// INV-001: intra-state 50,000 @18% -> CGST 4500 / SGST 4500.
	gstSaleAt(t, led, ctx, f.vtID, f.acme, f.sales, f.cgst, f.sgst, f.igst,
		"INV-001", "2026-04-12", "29", "50000.0000", "18", true)
	// INV-002: inter-state 80,000 @18% -> IGST 14400.
	gstSaleAt(t, led, ctx, f.vtID, f.mumbai, f.sales, f.cgst, f.sgst, f.igst,
		"INV-002", "2026-05-08", "27", "80000.0000", "18", false)
	// INV-003: intra-state 24,000 @12%, later corrected to 26,500.
	inv3 := gstSaleAt(t, led, ctx, f.vtID, f.acme, f.sales, f.cgst, f.sgst, f.igst,
		"INV-003", "2026-06-19", "29", "24000.0000", "12", true)
	if _, err := led.AlterVoucher(ctx, inv3, buildSale(uuid.Nil,
		f.acme, f.sales, f.cgst, f.sgst, f.igst,
		"INV-003", "2026-06-19", "29", "26500.0000", "12", true)); err != nil {
		t.Fatalf("alter INV-003: %v", err)
	}

	g, recon, err := gstr.NewService(pool).Reconcile(ctx, "2026-04-01", "2027-03-31", gstr.Options{
		GSTIN:        "29ZZZZZ0000Z1Z5",
		CGSTLedgerID: f.cgst, SGSTLedgerID: f.sgst, IGSTLedgerID: f.igst,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Total taxable value across the whole return, summed from the HSN summary
	// (which aggregates every reported invoice regardless of section).
	total := decimal.Zero
	for _, h := range g.HSN.Data {
		total = total.Add(h.Txval)
	}
	want := dec("156500") // 50000 + 80000 + 26500
	if !total.Equal(want) {
		t.Fatalf("GSTR-1 total taxable value = %s, want %s "+
			"(1,30,000 means the altered INV-003 was dropped by the voucher_type INNER JOIN)",
			total, want)
	}

	// Absolute tax totals, computed independently of the system under test:
	//   CGST 4500 + 1590 = 6090, SGST likewise, IGST 14400.
	if !recon.Return.CGST.Equal(dec("6090")) ||
		!recon.Return.SGST.Equal(dec("6090")) ||
		!recon.Return.IGST.Equal(dec("14400")) {
		t.Fatalf("GSTR-1 tax totals: c=%s s=%s i=%s, want 6090/6090/14400",
			recon.Return.CGST, recon.Return.SGST, recon.Return.IGST)
	}
	// The posted-tax side joins voucher_type identically, so it is NOT an
	// independent oracle -- assert it against the same absolute numbers.
	if !recon.Postings.CGST.Equal(dec("6090")) ||
		!recon.Postings.SGST.Equal(dec("6090")) ||
		!recon.Postings.IGST.Equal(dec("14400")) {
		t.Fatalf("posted output tax: c=%s s=%s i=%s, want 6090/6090/14400",
			recon.Postings.CGST, recon.Postings.SGST, recon.Postings.IGST)
	}
	if !recon.Balanced() {
		t.Fatalf("reconciliation not balanced: %+v vs %+v", recon.Return, recon.Postings)
	}
}
