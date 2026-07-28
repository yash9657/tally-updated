// Inventory red team for migration 0025.
//
// The inventory plane introduces four new tenant tables (godown, stock_batch,
// stock_movement, stock_opening) and three below-the-Go-layer guarantees that
// the application is not permitted to be the only thing enforcing:
//
//  1. stock_movement is IMMUTABLE. A correction appends a new voucher version
//     and re-states its lines; it never edits a movement in place. If an UPDATE
//     were possible, the movement ledger would stop being replayable and every
//     derived stock value — which is derived, never stored — would become a
//     function of when you happened to ask.
//  2. Tenant isolation is the same default-deny FORCE-RLS posture as every
//     other tenant table, AND every dimension FK is COMPOSITE on
//     (company_id, id) so a cross-tenant godown/batch/item reference is refused
//     by the DATABASE rather than by a policy that depends on the session GUC
//     being set correctly.
//  3. Negative stock is refused AT COMMIT, not at INSERT. The distinction is
//     the whole design: one voucher may legitimately consume and produce the
//     same item (a manufacturing journal, a godown transfer), so the check must
//     see the finished voucher. Checked per-statement it would reject balanced
//     vouchers depending on the order their lines were written.
//
// These probes run against the live Postgres, as the superuser for structural
// facts (so RLS cannot mask a missing constraint) and as tally_app for the
// isolation contract.
package security_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/testdb"
)

// inventoryTables are the four tenant tables migration 0025 adds. They must be
// held to exactly the posture the original seven are.
var inventoryTables = []string{
	"godown", "stock_batch", "stock_movement", "stock_opening",
}

// invFixture is a minimal but REAL inventory position in each of the red team's
// two companies: a location, a batch, an item, an opening, and a current voucher
// version carrying one inward movement. Everything is planted with superuser
// privileges so the cross-tenant data genuinely exists and RLS is what must hide
// it — the same discipline setupHarness uses for vouchers.
type invFixture struct {
	h *harness

	// Company A.
	godownA uuid.UUID
	batchA  uuid.UUID
	itemA   uuid.UUID
	// An item with NO opening and NO movements, so an outward against it is
	// unambiguously negative.
	bareItemA uuid.UUID
	// The current version carrying A's inward movement.
	versionInA uuid.UUID
	moveInA    uuid.UUID
	// A second current version, reserved for outward probes.
	versionOutA uuid.UUID

	// Company B (the tenant that must stay invisible).
	godownB    uuid.UUID
	itemB      uuid.UUID
	versionInB uuid.UUID
	moveInB    uuid.UUID
}

// setupInventory plants the fixture and registers a cleanup that runs BEFORE
// setupHarness's own (t.Cleanup is LIFO and the harness registered first), so
// the movement rows are gone by the time the harness deletes the voucher
// versions they reference.
func setupInventory(t *testing.T, h *harness) *invFixture {
	t.Helper()
	ctx := context.Background()

	f := &invFixture{
		h:           h,
		godownA:     mustV7(t),
		batchA:      mustV7(t),
		itemA:       mustV7(t),
		bareItemA:   mustV7(t),
		versionInA:  seedVoucher(t, h.admin, h.companyA, 900),
		versionOutA: seedVoucher(t, h.admin, h.companyA, 901),
		moveInA:     mustV7(t),
		godownB:     mustV7(t),
		itemB:       mustV7(t),
		versionInB:  seedVoucher(t, h.admin, h.companyB, 900),
		moveInB:     mustV7(t),
	}

	// Cleanup first, so a failure part-way through still tidies up.
	t.Cleanup(func() { f.cleanup(t) })

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := h.admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed inventory (%s): %v", strings.SplitN(strings.TrimSpace(sql), "\n", 2)[0], err)
		}
	}

	exec(`INSERT INTO godown (id, company_id, name, is_system) VALUES ($1, $2, 'Main Location', true)`,
		f.godownA, h.companyA)
	exec(`INSERT INTO godown (id, company_id, name, is_system) VALUES ($1, $2, 'Main Location', true)`,
		f.godownB, h.companyB)

	exec(`INSERT INTO stock_item (id, company_id, name) VALUES ($1, $2, $3)`,
		f.itemA, h.companyA, "redteam-item-A-"+f.itemA.String()[:8])
	exec(`INSERT INTO stock_item (id, company_id, name) VALUES ($1, $2, $3)`,
		f.bareItemA, h.companyA, "redteam-bare-A-"+f.bareItemA.String()[:8])
	exec(`INSERT INTO stock_item (id, company_id, name) VALUES ($1, $2, $3)`,
		f.itemB, h.companyB, "redteam-item-B-"+f.itemB.String()[:8])

	exec(`INSERT INTO stock_batch (id, company_id, stock_item_id, name) VALUES ($1, $2, $3, 'B-01')`,
		f.batchA, h.companyA, f.itemA)

	exec(`INSERT INTO stock_opening (id, company_id, stock_item_id, godown_id, batch_id, qty, rate)
	      VALUES ($1, $2, $3, $4, NULL, 25, 10)`,
		mustV7(t), h.companyA, f.itemA, f.godownA)
	exec(`INSERT INTO stock_opening (id, company_id, stock_item_id, godown_id, batch_id, qty, rate)
	      VALUES ($1, $2, $3, $4, NULL, 7, 3)`,
		mustV7(t), h.companyB, f.itemB, f.godownB)

	// Point the mutable head at each version: the negative-stock check counts
	// only CURRENT versions, exactly as the report-time replay must.
	for _, v := range []struct {
		company uuid.UUID
		version uuid.UUID
	}{
		{h.companyA, f.versionInA},
		{h.companyA, f.versionOutA},
		{h.companyB, f.versionInB},
	} {
		exec(`INSERT INTO voucher_head
		        (company_id, voucher_id, current_version_id, version_no, op, is_deleted)
		      SELECT $1, vv.voucher_id, vv.id, vv.version_no, vv.op, false
		        FROM voucher_version vv WHERE vv.id = $2`, v.company, v.version)
	}

	exec(`INSERT INTO stock_movement
	        (id, company_id, voucher_version_id, line_no, stock_item_id, godown_id,
	         direction, qty, rate, amount)
	      VALUES ($1, $2, $3, 1, $4, $5, 'IN', 100, 10, 1000)`,
		f.moveInA, h.companyA, f.versionInA, f.itemA, f.godownA)
	exec(`INSERT INTO stock_movement
	        (id, company_id, voucher_version_id, line_no, stock_item_id, godown_id,
	         direction, qty, rate, amount)
	      VALUES ($1, $2, $3, 1, $4, $5, 'IN', 42, 5, 210)`,
		f.moveInB, h.companyB, f.versionInB, f.itemB, f.godownB)

	return f
}

// cleanup removes the planted inventory rows. stock_movement is immutable, so
// the delete needs the trigger out of the way; we do that with
// session_replication_role, which is scoped to THIS admin connection, rather
// than ALTER TABLE ... DISABLE TRIGGER, which is a persistent, database-wide
// change that would silently switch immutability off for every other connection
// for as long as it lasted.
func (f *invFixture) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	companies := []uuid.UUID{f.h.companyA, f.h.companyB}

	_, _ = f.h.admin.Exec(ctx, `SET session_replication_role = 'replica'`)
	for _, tbl := range []string{
		"stock_movement", "stock_opening", "stock_batch",
		"stock_item", "godown", "voucher_head",
	} {
		_, _ = f.h.admin.Exec(ctx, `DELETE FROM `+tbl+` WHERE company_id = ANY($1)`, companies)
	}
	_, _ = f.h.admin.Exec(ctx, `SET session_replication_role = 'origin'`)
}

// ---------------------------------------------------------------------------
// 1. Immutability.
// ---------------------------------------------------------------------------

// TestStockMovementIsImmutable proves that neither an UPDATE nor a DELETE of a
// movement line is permitted, even for the superuser.
//
// WHY BOTH ARMS. The movement ledger is replayed from scratch to derive stock
// value; that is only sound if the rows never change after the fact. An UPDATE
// that slipped through would silently re-value a closed period, and a DELETE
// would make a shipped invoice's goods vanish while its accounting entry stayed.
func TestStockMovementIsImmutable(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	f := setupInventory(t, h)
	ctx := context.Background()

	_, err := h.admin.Exec(ctx, `UPDATE stock_movement SET qty = 9999 WHERE id = $1`, f.moveInA)
	if err == nil {
		t.Fatal("a stock_movement row was UPDATEd: the movement ledger is no longer replayable, " +
			"and every derived stock value depends on when you ask")
	}
	if !strings.Contains(err.Error(), "stock_movement is immutable") {
		t.Fatalf("UPDATE was refused, but not by the immutability trigger: %v", err)
	}

	_, err = h.admin.Exec(ctx, `DELETE FROM stock_movement WHERE id = $1`, f.moveInA)
	if err == nil {
		t.Fatal("a stock_movement row was DELETEd: goods can now disappear from the ledger " +
			"while the voucher that shipped them remains")
	}
	if !strings.Contains(err.Error(), "stock_movement is immutable") {
		t.Fatalf("DELETE was refused, but not by the immutability trigger: %v", err)
	}

	// And the row is genuinely untouched.
	var qty string
	if err := h.admin.QueryRow(ctx, `SELECT qty::text FROM stock_movement WHERE id = $1`, f.moveInA).
		Scan(&qty); err != nil {
		t.Fatalf("re-read movement: %v", err)
	}
	if qty != "100.0000" {
		t.Fatalf("movement quantity is now %s, want 100.0000 — the mutation attempts left a mark", qty)
	}
}

// ---------------------------------------------------------------------------
// 2. Tenant isolation.
// ---------------------------------------------------------------------------

// TestInventoryTablesForceRLS pins the structural posture: ENABLE + FORCE row
// level security and an explicit WITH CHECK on all four new tables. Asserted via
// the catalog because a policy that is merely ENABLEd (not FORCEd) still lets an
// owner-context query read every tenant, and a policy with no WITH CHECK still
// lets a scoped session WRITE into another tenant.
func TestInventoryTablesForceRLS(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	ctx := context.Background()
	for _, tbl := range inventoryTables {
		var enabled, forced bool
		if err := h.admin.QueryRow(ctx,
			`SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname = $1`, tbl).
			Scan(&enabled, &forced); err != nil {
			t.Fatalf("pg_class for %s: %v", tbl, err)
		}
		if !enabled {
			t.Errorf("SECURITY HOLE: %s does not have ROW LEVEL SECURITY enabled", tbl)
		}
		if !forced {
			t.Errorf("SECURITY HOLE: %s lacks FORCE ROW LEVEL SECURITY; owner-context queries bypass isolation", tbl)
		}
		var usingExpr, withCheck string
		if err := h.admin.QueryRow(ctx,
			`SELECT pg_get_expr(polqual, polrelid), COALESCE(pg_get_expr(polwithcheck, polrelid), '')
			   FROM pg_policy WHERE polrelid = $1::regclass AND polname = 'tenant_isolation'`, tbl).
			Scan(&usingExpr, &withCheck); err != nil {
			t.Fatalf("SECURITY HOLE: no tenant_isolation policy on %s: %v", tbl, err)
		}
		if !strings.Contains(usingExpr, "company_id") {
			t.Errorf("%s USING expr does not reference company_id: %q", tbl, usingExpr)
		}
		if withCheck == "" {
			t.Errorf("SECURITY HOLE: %s has no explicit WITH CHECK; a scoped session could write into another tenant", tbl)
		}
	}
}

// TestStockMovementTenantIsolation is the read AND write half of the contract:
// under company A's GUC the four inventory tables show exactly A's rows, and an
// INSERT that hard-codes company B's id is refused by the WITH CHECK policy.
func TestStockMovementTenantIsolation(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	f := setupInventory(t, h)
	ctx := context.Background()

	// Ground truth via the superuser: B really does have inventory to leak.
	var bMoves int
	if err := h.admin.QueryRow(ctx,
		`SELECT count(*) FROM stock_movement WHERE company_id = $1`, h.companyB).Scan(&bMoves); err != nil {
		t.Fatalf("ground truth for B: %v", err)
	}
	if bMoves != 1 {
		t.Fatalf("fixture is wrong: company B has %d movements, want 1", bMoves)
	}

	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		for _, c := range []struct {
			table string
			want  int
		}{
			{"stock_movement", 1},
			{"godown", 1},
			{"stock_batch", 1},
			{"stock_opening", 1},
		} {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+c.table).Scan(&n); err != nil {
				t.Fatalf("count %s under A's scope: %v", c.table, err)
			}
			if n != c.want {
				t.Errorf("SECURITY HOLE: company A saw %d rows of %s, want exactly %d — company B's inventory leaked",
					n, c.table, c.want)
			}
		}

		// Knowing B's movement id by heart still yields nothing.
		var n int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM stock_movement WHERE id = $1`, f.moveInB).Scan(&n); err != nil {
			t.Fatalf("by-id probe: %v", err)
		}
		if n != 0 {
			t.Errorf("SECURITY HOLE: company A read company B's movement %s by primary key", f.moveInB)
		}
	})

	// Write side: an INSERT naming B's company_id must be rejected outright.
	//
	// Every OTHER column is a genuinely valid company-B reference (B's current
	// voucher version, B's item, B's godown), on purpose: if the row were only
	// refusable because it was internally inconsistent, a weakened WITH CHECK
	// would still be caught by a foreign key and this probe would report a
	// healthy policy that no longer exists. Wired this way, RLS is the ONLY
	// thing standing between company A and a write into B's books.
	h.asCompany(t, h.companyA.String(), func(tx pgx.Tx) {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_movement
			   (id, company_id, voucher_version_id, line_no, stock_item_id, godown_id,
			    direction, qty, rate, amount)
			 VALUES ($1, $2, $3, 2, $4, $5, 'IN', 1, 1, 1)`,
			mustV7(t), h.companyB, f.versionInB, f.itemB, f.godownB)
		if err == nil {
			t.Fatal("SECURITY HOLE: company A inserted a stock movement into company B's books")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "row-level security") {
			t.Fatalf("the cross-tenant insert was refused, but not by the RLS WITH CHECK policy: %v", err)
		}
	})
}

// TestCompositeTenantFKsRejectCrossCompanyDimensions proves the isolation that
// does NOT depend on the session GUC: a movement in company A that names company
// B's godown is refused by the composite foreign key itself.
//
// WHY THIS IS A SEPARATE GUARANTEE FROM RLS. RLS hides B's godown from A's
// SELECTs, but nothing stops a request body from carrying B's godown UUID (a
// stale client, a replayed offline batch, an attacker who learned the id
// elsewhere). With a single-column REFERENCES godown (id) that row inserts
// happily and then belongs to neither tenant's reports while holding real stock.
// The composite key makes the reference structurally impossible.
func TestCompositeTenantFKsRejectCrossCompanyDimensions(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	f := setupInventory(t, h)
	ctx := context.Background()

	// Superuser, so RLS is not what refuses this — only the FK can be.
	_, err := h.admin.Exec(ctx,
		`INSERT INTO stock_movement
		   (id, company_id, voucher_version_id, line_no, stock_item_id, godown_id,
		    direction, qty, rate, amount)
		 VALUES ($1, $2, $3, 5, $4, $5, 'IN', 1, 1, 1)`,
		mustV7(t), h.companyA, f.versionOutA, f.itemA, f.godownB)
	if err == nil {
		t.Fatal("a movement in company A was allowed to name company B's godown: the row now holds " +
			"stock that appears in neither tenant's stock summary")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("the cross-tenant godown was refused, but not by the composite FK: %v", err)
	}

	// The same shape for the item dimension.
	_, err = h.admin.Exec(ctx,
		`INSERT INTO stock_movement
		   (id, company_id, voucher_version_id, line_no, stock_item_id, godown_id,
		    direction, qty, rate, amount)
		 VALUES ($1, $2, $3, 6, $4, $5, 'IN', 1, 1, 1)`,
		mustV7(t), h.companyA, f.versionOutA, f.itemB, f.godownA)
	if err == nil {
		t.Fatal("a movement in company A was allowed to name company B's stock item")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("the cross-tenant stock item was refused, but not by the composite FK: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 3. Negative stock.
// ---------------------------------------------------------------------------

// TestNegativeStockIsRefusedAtCommit proves the timing as well as the outcome:
// the outward INSERT itself must SUCCEED, and the refusal must arrive at COMMIT.
//
// The timing is the design, not an implementation detail. A single voucher may
// consume and produce the same item — a manufacturing journal, a godown
// transfer — so a per-statement check would reject a perfectly balanced voucher
// purely because its outward line happened to be written before its inward one.
// This is the same argument that makes posting_balanced deferred.
func TestNegativeStockIsRefusedAtCommit(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	f := setupInventory(t, h)
	ctx := context.Background()

	outward := func(t *testing.T) error {
		t.Helper()
		tx, err := h.admin.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_movement
			   (id, company_id, voucher_version_id, line_no, stock_item_id, godown_id,
			    direction, qty, rate, amount)
			 VALUES ($1, $2, $3, 1, $4, $5, 'OUT', 10, 0, 0)`,
			mustV7(t), h.companyA, f.versionOutA, f.bareItemA, f.godownA); err != nil {
			_ = tx.Rollback(ctx)
			// A failure HERE is the bug this test exists to catch: the check ran
			// at statement time instead of at commit.
			t.Fatalf("the outward INSERT was refused at statement time (%v) — the negative-stock "+
				"check is not deferred, so a voucher that consumes and produces the same item "+
				"would be rejected on line order alone", err)
		}
		return tx.Commit(ctx)
	}

	err := outward(t)
	if err == nil {
		t.Fatal("an outward of 10 against an item with no opening and no inward COMMITTED: " +
			"stock is now negative with allow_negative_stock = false")
	}
	if !strings.Contains(err.Error(), "negative stock refused") {
		t.Fatalf("the commit failed, but not with the negative-stock refusal: %v", err)
	}

	// The opt-in makes it legal — the guard is a company POLICY, not a hard rule,
	// because some trades really do sell before the supplier's invoice arrives.
	if _, err := h.admin.Exec(ctx,
		`UPDATE company SET allow_negative_stock = true WHERE id = $1`, h.companyA); err != nil {
		t.Fatalf("enable allow_negative_stock: %v", err)
	}
	if err := outward(t); err != nil {
		t.Fatalf("with allow_negative_stock = true the same voucher was still refused: %v", err)
	}
}

// TestNegativeStockCountsOnlyCurrentVersions guards the subtlety that makes the
// trigger correct under an ALTER: movements are versioned children, so a
// superseded version's rows are still in the table. Counting them would invent
// stock that was never there — here, the superseded inward of 100 must NOT keep
// an outward of 60 alive once the head has moved to a version with no lines.
func TestNegativeStockCountsOnlyCurrentVersions(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	f := setupInventory(t, h)
	ctx := context.Background()

	// Supersede the version that carried the inward of 100: the head now points
	// at another version, so those 100 units are no longer part of the position.
	if _, err := h.admin.Exec(ctx,
		`UPDATE voucher_head SET current_version_id = $1
		  WHERE company_id = $2 AND current_version_id = $3`,
		f.versionOutA, h.companyA, f.versionInA); err != nil {
		t.Fatalf("move head: %v", err)
	}

	tx, err := h.admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`INSERT INTO stock_movement
		   (id, company_id, voucher_version_id, line_no, stock_item_id, godown_id,
		    direction, qty, rate, amount)
		 VALUES ($1, $2, $3, 2, $4, $5, 'OUT', 60, 0, 0)`,
		mustV7(t), h.companyA, f.versionOutA, f.itemA, f.godownA); err != nil {
		t.Fatalf("insert outward: %v", err)
	}
	// Opening is 25; the superseded inward of 100 must not count, so 25 - 60 < 0.
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("an outward of 60 committed against an opening of 25: the negative-stock check " +
			"is counting SUPERSEDED voucher versions, which invents stock that an ALTER removed")
	} else if !strings.Contains(err.Error(), "negative stock refused") {
		t.Fatalf("commit failed, but not with the negative-stock refusal: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 4. The opening-stock migration: rename + backfill.
// ---------------------------------------------------------------------------

// TestLegacyOpeningColumnsRenamed pins the DELIBERATE breakage. Leaving
// stock_item.opening_qty in place alongside stock_opening would leave two
// sources of truth for the same fact, and the easier one to write to is the
// wrong one. The rename makes any surviving writer fail loudly instead.
func TestLegacyOpeningColumnsRenamed(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	ctx := context.Background()

	for _, col := range []string{"opening_qty", "opening_rate"} {
		var n int
		err := h.admin.QueryRow(ctx, `SELECT count(`+col+`) FROM stock_item`).Scan(&n)
		if err == nil {
			t.Errorf("stock_item.%s still exists: openings now have TWO sources of truth, "+
				"and the dimensionless one silently wins for anything that writes it", col)
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			t.Errorf("selecting %s failed for the wrong reason: %v", col, err)
		}
	}

	// And the data was preserved under the new names, not dropped.
	for _, col := range []string{"legacy_opening_qty", "legacy_opening_rate"} {
		var n int
		if err := h.admin.QueryRow(ctx, `SELECT count(`+col+`) FROM stock_item`).Scan(&n); err != nil {
			t.Errorf("stock_item.%s is missing: the pre-0025 opening data was dropped rather "+
				"than carried across: %v", col, err)
		}
	}
}

// TestOpeningStockBackfilled proves the migration's backfill actually moves a
// legacy dimensionless opening into a real stock_opening position, at the
// company's 'Main Location' and with no batch.
//
// It does so by executing the SHIPPED backfill block itself, extracted from
// 0025 between its marker comments, against a freshly planted legacy item. That
// is deliberate: a re-implementation of the backfill inside the test would pass
// even if the migration's own block were deleted, which is precisely the
// regression worth catching. The block carries a NOT EXISTS guard, so running it
// a second time is a no-op for everything already migrated.
func TestOpeningStockBackfilled(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	ctx := context.Background()

	block := extractBackfillBlock(t)

	// A company with a Main Location (as every company has after 0025) and a
	// stock item carrying a legacy, dimensionless opening.
	if _, err := h.admin.Exec(ctx, `SELECT seed_inventory_defaults($1)`, h.companyA); err != nil {
		t.Fatalf("seed inventory defaults: %v", err)
	}
	item := mustV7(t)
	if _, err := h.admin.Exec(ctx,
		`INSERT INTO stock_item (id, company_id, name, legacy_opening_qty, legacy_opening_rate)
		 VALUES ($1, $2, $3, 40, 12.5)`,
		item, h.companyA, "legacy-open-"+item.String()[:8]); err != nil {
		t.Fatalf("seed legacy stock item: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.admin.Exec(ctx, `DELETE FROM stock_opening WHERE company_id = $1`, h.companyA)
		_, _ = h.admin.Exec(ctx, `DELETE FROM stock_item WHERE company_id = $1`, h.companyA)
		_, _ = h.admin.Exec(ctx, `DELETE FROM godown WHERE company_id = $1`, h.companyA)
	})

	if _, err := h.admin.Exec(ctx, block); err != nil {
		t.Fatalf("the shipped backfill block failed to run: %v", err)
	}

	var (
		n        int
		qty      string
		rate     string
		godownID *uuid.UUID
		batchID  *uuid.UUID
	)
	if err := h.admin.QueryRow(ctx,
		`SELECT count(*) FROM stock_opening WHERE company_id = $1 AND stock_item_id = $2`,
		h.companyA, item).Scan(&n); err != nil {
		t.Fatalf("count openings: %v", err)
	}
	if n != 1 {
		t.Fatalf("the legacy opening of 40 units produced %d stock_opening rows, want exactly 1 — "+
			"a pre-0025 tenant's opening stock has %s", n,
			map[bool]string{true: "vanished", false: "been double-counted"}[n == 0])
	}

	if err := h.admin.QueryRow(ctx,
		`SELECT qty::text, rate::text, godown_id, batch_id FROM stock_opening
		  WHERE company_id = $1 AND stock_item_id = $2`,
		h.companyA, item).Scan(&qty, &rate, &godownID, &batchID); err != nil {
		t.Fatalf("read opening: %v", err)
	}
	if qty != "40.0000" || rate != "12.5000" {
		t.Errorf("backfilled opening is %s @ %s, want 40.0000 @ 12.5000 — the carried position is wrong", qty, rate)
	}
	if batchID != nil {
		t.Errorf("backfilled opening was attached to batch %s; a dimensionless legacy opening names no batch", batchID)
	}
	if godownID == nil {
		t.Fatal("backfilled opening has no godown: the carried position is nowhere, so a " +
			"location-scoped stock summary will not show it")
	}
	var godownName string
	if err := h.admin.QueryRow(ctx, `SELECT name FROM godown WHERE id = $1`, *godownID).Scan(&godownName); err != nil {
		t.Fatalf("read godown: %v", err)
	}
	if godownName != "Main Location" {
		t.Errorf("backfilled opening sits in %q, want 'Main Location'", godownName)
	}

	// Idempotent: running it again must not duplicate the position.
	if _, err := h.admin.Exec(ctx, block); err != nil {
		t.Fatalf("re-running the backfill block failed: %v", err)
	}
	if err := h.admin.QueryRow(ctx,
		`SELECT count(*) FROM stock_opening WHERE company_id = $1 AND stock_item_id = $2`,
		h.companyA, item).Scan(&n); err != nil {
		t.Fatalf("recount openings: %v", err)
	}
	if n != 1 {
		t.Fatalf("re-running the backfill produced %d openings for one item — it is not idempotent, "+
			"so a re-applied migration would double a tenant's opening stock", n)
	}
}

const (
	backfillBegin = "-- BEGIN BACKFILL: legacy stock_item openings -> stock_opening"
	backfillEnd   = "-- END BACKFILL: legacy stock_item openings -> stock_opening"
)

// extractBackfillBlock returns the shipped backfill SQL from migration 0025.
func extractBackfillBlock(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Clean(filepath.Join(wd, "..", "..", "migrations", "0025_inventory_schema.sql"))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 0025: %v", err)
	}
	src := string(raw)
	i := strings.Index(src, backfillBegin)
	j := strings.Index(src, backfillEnd)
	if i < 0 || j < 0 || j <= i {
		t.Fatalf("migration 0025 no longer contains the opening-stock backfill block: every "+
			"pre-0025 tenant's opening stock would be left behind (looked for %q .. %q in %s)",
			backfillBegin, backfillEnd, path)
	}
	return src[i:j]
}

// TestSeedCompanyDefaultsCreatesMainLocation proves NEW companies get the
// system location too — without it, only pre-0025 tenants (which the migration
// backfilled) could enter an inventory voucher, and the difference would surface
// much later as an unexplainable failure on a brand-new company.
func TestSeedCompanyDefaultsCreatesMainLocation(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	ctx := context.Background()

	company := mustV7(t)
	seedCompany(t, h.admin, company)
	// This test seeds a THIRD company (beyond the harness's A and B) and then runs
	// seed_company_defaults over it, so it plants a full master tree. The
	// hand-written five-table delete this replaces was the exact failure mode the
	// hygiene work exists to kill: it named the tables it happened to remember, so
	// the day seed_company_defaults started creating a sixth kind of row — which is
	// literally what migration 0025 did when it added the system godown — the extra
	// rows were orphaned silently. DropNow enumerates the tables from the live
	// catalogue instead, so it cannot fall behind the schema.
	t.Cleanup(func() { testdb.DropNow(t, company) })
	if _, err := h.admin.Exec(ctx, `SELECT seed_company_defaults($1)`, company); err != nil {
		t.Fatalf("seed_company_defaults: %v", err)
	}

	var n int
	if err := h.admin.QueryRow(ctx,
		`SELECT count(*) FROM godown WHERE company_id = $1 AND name = 'Main Location' AND is_system`,
		company).Scan(&n); err != nil {
		t.Fatalf("count godowns: %v", err)
	}
	if n != 1 {
		t.Fatalf("a newly created company has %d system 'Main Location' godowns, want exactly 1 — "+
			"it cannot record an inventory voucher until somebody creates one by hand", n)
	}
}

// TestValuationMethodIsWeightedAverageOnly pins the CHECK. v1 ships weighted
// average only, and the schema says so out loud rather than accepting 'FIFO' and
// then valuing it as weighted average anyway — which would be a wrong closing
// value that nothing anywhere reports as an error.
func TestValuationMethodIsWeightedAverageOnly(t *testing.T) {
	skipIfNoDB(t)
	h := setupHarness(t, 0, 0)
	ctx := context.Background()

	item := mustV7(t)
	t.Cleanup(func() {
		_, _ = h.admin.Exec(ctx, `DELETE FROM stock_item WHERE id = $1`, item)
	})
	if _, err := h.admin.Exec(ctx,
		`INSERT INTO stock_item (id, company_id, name) VALUES ($1, $2, $3)`,
		item, h.companyA, "valuation-"+item.String()[:8]); err != nil {
		t.Fatalf("seed stock item: %v", err)
	}

	var method string
	if err := h.admin.QueryRow(ctx, `SELECT valuation_method FROM stock_item WHERE id = $1`, item).
		Scan(&method); err != nil {
		t.Fatalf("read valuation_method: %v", err)
	}
	if method != "WEIGHTED_AVG" {
		t.Errorf("a new stock item defaults to valuation_method %q, want WEIGHTED_AVG", method)
	}

	_, err := h.admin.Exec(ctx, `UPDATE stock_item SET valuation_method = 'FIFO' WHERE id = $1`, item)
	if err == nil {
		t.Fatal("an item was set to FIFO, which no valuer implements: its stock would be valued " +
			"as weighted average while the master claims otherwise")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "check constraint") {
		t.Fatalf("FIFO was refused, but not by the valuation_method CHECK: %v", err)
	}
}
