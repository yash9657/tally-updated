package masters_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/migrations"
)

const (
	adminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	appDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
)

func setup(t *testing.T) (*db.Pool, context.Context, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
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

	companyID, _ := uuid.NewV7()
	cctx := db.WithCompany(ctx, companyID)
	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID: companyID, Name: "Masters Co " + companyID.String()[:8], FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	return pool, cctx, companyID
}

func TestCreateLedgerWithGSTAndAudit(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	id, err := m.CreateLedger(ctx, masters.Ledger{
		Name:             "GST Customer",
		GroupName:        "Sundry Debtors",
		GSTIN:            "27ABCDE1234F1Z5",
		StateCode:        "27",
		RegistrationType: masters.GSTRegular,
	}, "tester")
	if err != nil {
		t.Fatalf("create ledger: %v", err)
	}

	gst, err := m.LedgerGSTInfo(ctx, id)
	if err != nil {
		t.Fatalf("gst info: %v", err)
	}
	if gst.GSTIN != "27ABCDE1234F1Z5" || gst.StateCode != "27" || gst.RegistrationType != masters.GSTRegular {
		t.Fatalf("unexpected gst info: %+v", gst)
	}

	// Audit row must exist for the create.
	var auditCount int
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM master_audit WHERE entity_type='ledger' AND entity_id=$1 AND op='CREATE'`,
			id).Scan(&auditCount)
	})
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("expected 1 audit row, got %d", auditCount)
	}
}

func TestEnsureLedgerIsIdempotent(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	id1, err := m.EnsureLedger(ctx, "Repeat Ledger", "Indirect Expenses", "tester")
	if err != nil {
		t.Fatalf("ensure 1: %v", err)
	}
	id2, err := m.EnsureLedger(ctx, "Repeat Ledger", "Indirect Expenses", "tester")
	if err != nil {
		t.Fatalf("ensure 2: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("EnsureLedger not idempotent: %s != %s", id1, id2)
	}
}

func TestCreateGroupSubgroupInheritsNature(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	// Primary group with explicit nature.
	if _, err := m.CreateGroup(ctx, masters.Group{
		Name: "My Primary", Nature: masters.Liability, IsPL: false,
	}, "tester"); err != nil {
		t.Fatalf("create primary: %v", err)
	}
	// Sub-group under it inherits nature/is_pl.
	subID, err := m.CreateGroup(ctx, masters.Group{
		Name: "My Sub", ParentName: "My Primary",
	}, "tester")
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}

	var nature string
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT nature::text FROM account_group WHERE id=$1`, subID).Scan(&nature)
	})
	if err != nil {
		t.Fatalf("read sub nature: %v", err)
	}
	if nature != "LIABILITY" {
		t.Fatalf("sub-group should inherit LIABILITY, got %s", nature)
	}
}

func TestCreateStockItemAndUnit(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	// "Nos" is seeded; create a custom unit too.
	if _, err := m.CreateUnit(ctx, masters.Unit{Symbol: "Kg", FormalName: "Kilogram", DecimalPlaces: 3}, "tester"); err != nil {
		t.Fatalf("create unit: %v", err)
	}
	id, err := m.CreateStockItem(ctx, masters.StockItem{
		Name: "Widget", UnitSymbol: "Kg", HSN: "8471",
		GSTRatePct: decimal.RequireFromString("18.00"),
	}, "tester")
	if err != nil {
		t.Fatalf("create stock item: %v", err)
	}
	got, err := m.StockItemID(ctx, "Widget")
	if err != nil {
		t.Fatalf("resolve stock item: %v", err)
	}
	if got != id {
		t.Fatalf("resolved id mismatch")
	}
}

func TestResolveUnknownReturnsNotFound(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)
	_, err := m.LedgerID(ctx, "Does Not Exist")
	if !errors.Is(err, masters.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMastersRequiresCompanyScope(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Skipf("pool unavailable: %v", err)
	}
	defer pool.Close()
	m := masters.NewService(pool)
	// No company in context -> must refuse before touching the DB.
	if _, err := m.CreateLedger(context.Background(), masters.Ledger{Name: "X", GroupName: "Y"}, "t"); !errors.Is(err, db.ErrNoCompany) {
		t.Fatalf("expected ErrNoCompany, got %v", err)
	}
}
