package ledger_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/shopspring/decimal"
)

// TestTenantIsolation reproduces the corpora/isolation contract through the app
// role: with company A in scope a session sees only A's voucher_versions and
// never B's; an unscoped or forged-company session sees zero rows.
func TestTenantIsolation(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	poolA, ctxA, companyA, idsA := setup(t)

	// Build a second company B sharing the same pool.
	companyB, _ := uuid.NewV7()
	ctxB := db.WithCompany(context.Background(), companyB)
	if _, err := idsA.CreateCompany(ctxB, identity.Company{
		ID: companyB, Name: "Company B", FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company B: %v", err)
	}
	// Company B is minted INLINE, not through setup(), and that is precisely the
	// kind of site a cleanup retrofit walks straight past: the harness looks tidy,
	// the package's own teardown test goes green, and half the companies still
	// leak. Every cross-tenant test in this repo has a second creation site like
	// this one; each needs its own registration.
	testdb.Cleanup(t, companyB)

	// Post one sale into A and two into B.
	post := func(ctx context.Context, ids *identity.Service, no string) {
		vt, _ := ids.VoucherTypeID(ctx, "Sales")
		d, _ := ids.CreateLedger(ctx, "Debtor "+no, "Sundry Debtors")
		s, _ := ids.CreateLedger(ctx, "Sales "+no, "Sales Accounts")
		led := ledger.NewService(poolA)
		amt := decimal.RequireFromString("100.0000")
		if _, err := led.PostSalesVoucher(ctx, ledger.SalesVoucherInput{
			VoucherTypeID: vt, VoucherNo: no, VoucherDate: "2025-05-01",
			Party: "P", CreatedBy: "t",
			Postings: []ledger.PostingInput{
				{LedgerID: d, DrCr: ledger.Debit, Amount: amt},
				{LedgerID: s, DrCr: ledger.Credit, Amount: amt},
			},
		}); err != nil {
			t.Fatalf("post %s: %v", no, err)
		}
	}
	idsB := identity.NewService(poolA)
	post(ctxA, idsA, "A1")
	post(ctxB, idsB, "B1")
	post(ctxB, idsB, "B2")

	countVouchers := func(ctx context.Context) (int, error) {
		var n int
		err := poolA.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT count(*) FROM voucher_version").Scan(&n)
		})
		return n, err
	}

	// Company A sees exactly its own voucher (1), never B's.
	if n, err := countVouchers(ctxA); err != nil || n != 1 {
		t.Fatalf("company A should see 1 voucher, got %d (err %v)", n, err)
	}
	// Company B sees exactly its own two.
	if n, err := countVouchers(ctxB); err != nil || n != 2 {
		t.Fatalf("company B should see 2 vouchers, got %d (err %v)", n, err)
	}

	// No company in scope: the guard refuses before hitting the DB.
	if _, err := countVouchers(context.Background()); err != db.ErrNoCompany {
		t.Fatalf("unscoped query should return ErrNoCompany, got %v", err)
	}

	// Forged / unknown company id: scoped but matches no rows => zero.
	forged := db.WithCompany(context.Background(), uuid.Nil)
	// uuid.Nil is treated as "no company" by the guard, so use a random unknown.
	_ = forged
	unknown := db.WithCompany(context.Background(), uuid.Must(uuid.NewV7()))
	if n, err := countVouchers(unknown); err != nil || n != 0 {
		t.Fatalf("unknown company should see 0 vouchers, got %d (err %v)", n, err)
	}
	_ = companyA
	_ = companyB
}
