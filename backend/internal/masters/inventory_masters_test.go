package masters_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
)

// secondCompany creates another tenant on the SAME pool and returns a context
// scoped to it. Cross-tenant tests need two companies sharing one connection
// pool, because the point being proved is that the company GUC (and the RLS
// policies keyed off it) — not the connection — is what separates them.
func secondCompany(t *testing.T, pool *db.Pool) context.Context {
	t.Helper()
	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	ctx := db.WithCompany(context.Background(), companyID)
	if _, err := identity.NewService(pool).CreateCompany(ctx, identity.Company{
		ID: companyID, Name: "Inventory Co " + companyID.String()[:8], FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create second company: %v", err)
	}
	// secondCompany is a SECOND creation site in this package, separate from
	// setup()'s. Cleaning up only the primary harness would halve the leak and
	// look finished; the tenant B of every cross-tenant probe needs its own
	// registration, and this is that registration.
	testdb.Cleanup(t, companyID)
	return ctx
}

// TestCreateGodownAppendsMasterAudit pins the audit contract for locations AND
// pins it to the transaction: the second, FAILING create must leave no trace.
// An edit log that records creations which never happened is worse than no log
// — it cannot be used to answer "what does this company actually have", which
// is the only question an auditor asks of it.
func TestCreateGodownAppendsMasterAudit(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	id, err := m.CreateGodown(ctx, masters.Godown{Name: "Cold Store"}, "tester")
	if err != nil {
		t.Fatalf("create godown: %v", err)
	}

	var (
		rows       int
		op         string
		beforeNull bool
		afterName  string
	)
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*), max(op::text), bool_and(before_doc IS NULL),
			        max(after_doc->>'name')
			   FROM master_audit
			  WHERE entity_type = 'godown' AND entity_id = $1`, id).
			Scan(&rows, &op, &beforeNull, &afterName)
	}); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if rows != 1 {
		t.Fatalf("creating one godown must append exactly one audit row, got %d — "+
			"a missing row loses the change, a duplicate double-counts it", rows)
	}
	if op != "CREATE" {
		t.Fatalf("godown audit op is %q, want CREATE — the edit log would report the "+
			"wrong kind of change", op)
	}
	if !beforeNull {
		t.Fatalf("a CREATE has no before state, but before_doc was not NULL — the log " +
			"would claim the location existed beforehand")
	}
	if afterName != "Cold Store" {
		t.Fatalf("audit after_doc names %q, want %q — the log cannot be matched to the "+
			"row it describes", afterName, "Cold Store")
	}

	// A duplicate name is refused by UNIQUE (company_id, name). The audit
	// append lives inside the same transaction, so the rollback must take the
	// audit row with it.
	if _, err := m.CreateGodown(ctx, masters.Godown{Name: "Cold Store"}, "tester"); err == nil {
		t.Fatalf("a second godown named 'Cold Store' must be refused: two locations with " +
			"one name make every stock report ambiguous")
	}

	var godownAudits int
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM master_audit WHERE entity_type = 'godown'`).Scan(&godownAudits)
	}); err != nil {
		t.Fatalf("read audit after failed create: %v", err)
	}
	if godownAudits != 1 {
		t.Fatalf("after a REFUSED godown create the company has %d godown audit rows, want 1 — "+
			"the edit log is claiming a location that does not exist", godownAudits)
	}
}

// TestCreateBatchIsScopedToItsStockItem pins the uniqueness grain of a batch
// name: per ITEM, not per company. Two suppliers numbering their lots 'B-01'
// is ordinary; merging those two physical batches (with two different expiry
// dates) into one master row is how a recall goes wrong.
func TestCreateBatchIsScopedToItsStockItem(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	for _, name := range []string{"Amoxycillin 500", "Paracetamol 650"} {
		if _, err := m.CreateStockItem(ctx, masters.StockItem{
			Name: name, GSTRatePct: decimal.RequireFromString("12.00"),
		}, "tester"); err != nil {
			t.Fatalf("create stock item %q: %v", name, err)
		}
	}

	first, err := m.CreateStockBatch(ctx, masters.StockBatch{
		StockItemName: "Amoxycillin 500", Name: "B-01",
	}, "tester")
	if err != nil {
		t.Fatalf("create batch on first item: %v", err)
	}
	second, err := m.CreateStockBatch(ctx, masters.StockBatch{
		StockItemName: "Paracetamol 650", Name: "B-01",
	}, "tester")
	if err != nil {
		t.Fatalf("two DIFFERENT items must each be allowed a batch 'B-01' — batch names "+
			"belong to the item, not the company: %v", err)
	}
	if first == second {
		t.Fatalf("the two items' 'B-01' batches resolved to one row — two physical batches " +
			"with different expiry dates would share a single position")
	}

	if _, err := m.CreateStockBatch(ctx, masters.StockBatch{
		StockItemName: "Amoxycillin 500", Name: "B-01",
	}, "tester"); err == nil {
		t.Fatalf("the SAME item must not accept a second batch 'B-01': stock could then be " +
			"issued from either row and neither would show the true balance")
	}

	// Each name must resolve back to its own item's batch.
	gotFirst, err := m.StockBatchID(ctx, "Amoxycillin 500", "B-01")
	if err != nil {
		t.Fatalf("resolve first batch: %v", err)
	}
	gotSecond, err := m.StockBatchID(ctx, "Paracetamol 650", "B-01")
	if err != nil {
		t.Fatalf("resolve second batch: %v", err)
	}
	if gotFirst != first || gotSecond != second {
		t.Fatalf("batch name resolution crossed items: got %s/%s want %s/%s — a movement "+
			"would be booked against the wrong product's lot", gotFirst, gotSecond, first, second)
	}
}

// TestBatchExpiryIsOptionalAndRoundTrips pins that "no expiry" survives the
// round trip as ABSENT. If it came back as the zero time (0001-01-01) every
// undated batch would sort ahead of genuinely expired stock in an expiry
// report, and a shelf-life alert would fire on goods that are fine.
func TestBatchExpiryIsOptionalAndRoundTrips(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	if _, err := m.CreateStockItem(ctx, masters.StockItem{Name: "Vaccine Vial"}, "tester"); err != nil {
		t.Fatalf("create stock item: %v", err)
	}

	dated, err := m.CreateStockBatch(ctx, masters.StockBatch{
		StockItemName: "Vaccine Vial", Name: "LOT-DATED",
		MfgDate: "2026-04-01", ExpiryDate: "2027-03-31",
	}, "tester")
	if err != nil {
		t.Fatalf("create dated batch: %v", err)
	}
	undated, err := m.CreateStockBatch(ctx, masters.StockBatch{
		StockItemName: "Vaccine Vial", Name: "LOT-UNDATED",
	}, "tester")
	if err != nil {
		t.Fatalf("create undated batch: %v", err)
	}

	got, err := m.StockBatchInfo(ctx, dated)
	if err != nil {
		t.Fatalf("read dated batch: %v", err)
	}
	if got.ExpiryDate != "2027-03-31" {
		t.Fatalf("expiry read back as %q, want 2027-03-31 — a shifted or reformatted "+
			"expiry date makes the expiry report wrong by whatever the shift is", got.ExpiryDate)
	}
	if got.MfgDate != "2026-04-01" {
		t.Fatalf("mfg date read back as %q, want 2026-04-01", got.MfgDate)
	}
	if got.StockItemName != "Vaccine Vial" || got.Name != "LOT-DATED" {
		t.Fatalf("batch identity did not round trip: %+v", got)
	}

	got, err = m.StockBatchInfo(ctx, undated)
	if err != nil {
		t.Fatalf("read undated batch: %v", err)
	}
	if got.ExpiryDate != "" {
		t.Fatalf("a batch created without an expiry reads back %q, want \"\" — an invented "+
			"date makes never-expiring stock look like the oldest stock in the warehouse",
			got.ExpiryDate)
	}
	if got.MfgDate != "" {
		t.Fatalf("a batch created without a manufacturing date reads back %q, want \"\"",
			got.MfgDate)
	}
}

// TestCreateStockItemWritesStockOpeningNotLegacyColumns pins the repair 0025
// deliberately forced: an opening quantity is a POSITION (item, godown, batch)
// in stock_opening, never the retired dimensionless columns on the master.
func TestCreateStockItemWritesStockOpeningNotLegacyColumns(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	id, err := m.CreateStockItem(ctx, masters.StockItem{
		Name:        "Opening Widget",
		GSTRatePct:  decimal.RequireFromString("18.00"),
		OpeningQty:  decimal.RequireFromString("100"),
		OpeningRate: decimal.RequireFromString("12.50"),
	}, "tester")
	if err != nil {
		t.Fatalf("create stock item with opening: %v", err)
	}

	var (
		positions  int
		qty, rate  decimal.Decimal
		godownName string
		batchNull  bool
	)
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*), max(so.qty), max(so.rate), max(g.name),
			        bool_and(so.batch_id IS NULL)
			   FROM stock_opening so
			   JOIN godown g ON g.id = so.godown_id
			  WHERE so.stock_item_id = $1`, id).
			Scan(&positions, &qty, &rate, &godownName, &batchNull)
	}); err != nil {
		t.Fatalf("read stock_opening: %v", err)
	}
	if positions != 1 {
		t.Fatalf("an item with an opening quantity must have exactly one opening position, "+
			"got %d — zero loses the stock, two double it", positions)
	}
	if !qty.Equal(decimal.RequireFromString("100")) {
		t.Fatalf("opening qty stored as %s, want 100 — the closing balance of every later "+
			"report is out by the difference", qty.String())
	}
	if !rate.Equal(decimal.RequireFromString("12.50")) {
		t.Fatalf("opening rate stored as %s, want 12.50 — the valuer's initial state is "+
			"wrong, so every derived stock value is wrong", rate.String())
	}
	if godownName != masters.MainLocationName {
		t.Fatalf("opening position sits in godown %q, want %q — stock in an unnamed or wrong "+
			"location cannot be found by a location-wise report", godownName, masters.MainLocationName)
	}
	if !batchNull {
		t.Fatalf("the dimensionless opening was given a batch it never had")
	}

	// The retired columns must stay untouched. They exist only as the
	// provenance of the backfilled rows; a second writer would recreate
	// exactly the two-sources-of-truth problem 0025 removed.
	var legacyQty, legacyRate decimal.Decimal
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT legacy_opening_qty, legacy_opening_rate FROM stock_item WHERE id = $1`, id).
			Scan(&legacyQty, &legacyRate)
	}); err != nil {
		t.Fatalf("read legacy columns: %v", err)
	}
	if !legacyQty.IsZero() || !legacyRate.IsZero() {
		t.Fatalf("the retired columns were written (%s @ %s): the item now carries two "+
			"unreconciled openings", legacyQty.String(), legacyRate.String())
	}

	// An item with no opening must not invent a position at all.
	plain, err := m.CreateStockItem(ctx, masters.StockItem{Name: "Plain Widget"}, "tester")
	if err != nil {
		t.Fatalf("create stock item without opening: %v", err)
	}
	var plainPositions int
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM stock_opening WHERE stock_item_id = $1`, plain).Scan(&plainPositions)
	}); err != nil {
		t.Fatalf("read stock_opening for plain item: %v", err)
	}
	if plainPositions != 0 {
		t.Fatalf("an item with no opening quantity got %d opening positions — every item "+
			"would claim a place in Main Location it never occupied", plainPositions)
	}
}

// TestGodownAndBatchResolveByNameWithinTenantOnly pins that name resolution
// cannot reach across companies. Both tenants have a 'Main Location' (they are
// seeded with one), so a resolver that ignored the tenant scope would return a
// plausible-looking id belonging to somebody else's warehouse — and the FK
// would then be the only thing standing between that and stock booked into
// another company's books.
func TestGodownAndBatchResolveByNameWithinTenantOnly(t *testing.T) {
	pool, ctxA, _ := setup(t)
	ctxB := secondCompany(t, pool)
	m := masters.NewService(pool)

	mainA, err := m.GodownID(ctxA, masters.MainLocationName)
	if err != nil {
		t.Fatalf("resolve Main Location in company A: %v", err)
	}
	mainB, err := m.GodownID(ctxB, masters.MainLocationName)
	if err != nil {
		t.Fatalf("resolve Main Location in company B: %v", err)
	}
	if mainA == mainB {
		t.Fatalf("both companies resolved 'Main Location' to %s — one tenant's stock would "+
			"sit in the other tenant's warehouse", mainA)
	}

	// A location that exists ONLY in B must be unresolvable from A.
	if _, err := m.CreateGodown(ctxB, masters.Godown{Name: "B Only Store"}, "tester"); err != nil {
		t.Fatalf("create godown in company B: %v", err)
	}
	if got, err := m.GodownID(ctxA, "B Only Store"); !errors.Is(err, masters.ErrNotFound) {
		t.Fatalf("company A resolved company B's godown to %s (err %v), want ErrNotFound — "+
			"a cross-tenant location id is a leak of the other tenant's master data", got, err)
	}

	// Same for batches, which resolve through a join and so have two chances
	// to escape their tenant.
	if _, err := m.CreateStockItem(ctxB, masters.StockItem{Name: "Shared Name Item"}, "tester"); err != nil {
		t.Fatalf("create stock item in company B: %v", err)
	}
	batchB, err := m.CreateStockBatch(ctxB, masters.StockBatch{
		StockItemName: "Shared Name Item", Name: "B-01",
	}, "tester")
	if err != nil {
		t.Fatalf("create batch in company B: %v", err)
	}
	if got, err := m.StockBatchID(ctxA, "Shared Name Item", "B-01"); !errors.Is(err, masters.ErrNotFound) {
		t.Fatalf("company A resolved company B's batch to %s (err %v), want ErrNotFound", got, err)
	}
	if got, err := m.StockBatchIDForItem(ctxA, uuid.Nil, "B-01"); !errors.Is(err, masters.ErrNotFound) {
		t.Fatalf("company A resolved a batch by id-and-name to %s (err %v), want ErrNotFound", got, err)
	}

	// A's OWN masters of the same names still resolve, so the isolation above
	// is not just "nothing resolves".
	if _, err := m.CreateStockItem(ctxA, masters.StockItem{Name: "Shared Name Item"}, "tester"); err != nil {
		t.Fatalf("create stock item in company A: %v", err)
	}
	batchA, err := m.CreateStockBatch(ctxA, masters.StockBatch{
		StockItemName: "Shared Name Item", Name: "B-01",
	}, "tester")
	if err != nil {
		t.Fatalf("create batch in company A: %v", err)
	}
	gotA, err := m.StockBatchID(ctxA, "Shared Name Item", "B-01")
	if err != nil {
		t.Fatalf("resolve A's own batch: %v", err)
	}
	if gotA != batchA || gotA == batchB {
		t.Fatalf("company A resolved 'B-01' to %s, want its own %s (B's is %s)", gotA, batchA, batchB)
	}
}

// TestEveryCompanyIsSeededWithMainLocation pins the seed half of the contract:
// a company created today has a location for stock to sit in before anybody
// creates a master. Without it the first inventory voucher a new tenant enters
// fails on a foreign key, which is not a message anyone can act on.
func TestEveryCompanyIsSeededWithMainLocation(t *testing.T) {
	pool, _, _ := setup(t)
	ctx := secondCompany(t, pool)
	m := masters.NewService(pool)

	var (
		count    int
		name     string
		isSystem bool
	)
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*), max(name), bool_and(is_system) FROM godown`).
			Scan(&count, &name, &isSystem)
	}); err != nil {
		t.Fatalf("read godowns of a fresh company: %v", err)
	}
	if count != 1 {
		t.Fatalf("a fresh company has %d godowns, want exactly 1 — with none, no stock can "+
			"be located at all; with more, the default is ambiguous", count)
	}
	if name != masters.MainLocationName {
		t.Fatalf("the seeded location is named %q, want %q", name, masters.MainLocationName)
	}
	if !isSystem {
		t.Fatalf("the seeded 'Main Location' is not flagged is_system — a user could delete " +
			"the one location every default position depends on")
	}

	// And it is the one MainLocationID returns.
	id, err := m.MainLocationID(ctx)
	if err != nil {
		t.Fatalf("MainLocationID: %v", err)
	}
	byName, err := m.GodownID(ctx, masters.MainLocationName)
	if err != nil {
		t.Fatalf("GodownID(Main Location): %v", err)
	}
	if id != byName {
		t.Fatalf("MainLocationID returned %s but the name resolves to %s — the default "+
			"position and the named location would diverge", id, byName)
	}
}
