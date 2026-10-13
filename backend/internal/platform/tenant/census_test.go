package tenant_test

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/khata/cored/internal/platform/tenant"
	"github.com/khata/cored/internal/platform/testdb"
)

// This test lives HERE, beside the registries it makes complete, and not in
// internal/platform/testdb where it was born. A completeness test that lives
// apart from the thing it makes complete is one refactor away from being
// vacuous: the registries could move again, the test could keep passing against
// a copy, and nobody would notice until a table leaked.
//
// It is an EXTERNAL test package (tenant_test) so it may import testdb for the
// admin DSN without the production package acquiring an edge to it — the
// archtest boundary walker skips _test.go files precisely so that this is
// possible, and TestNoProductionCodeImportsTestdb still forbids the import from
// any non-test file.

// conn opens an admin connection, skipping when the database is unreachable —
// the same contract testdb's own tests use, so a laptop with no Postgres can
// still run `go build ./... && go vet ./...` and the pure packages' tests.
func conn(t *testing.T) (*pgxpool.Pool, tenant.ConnLike) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("CORED_SKIP_DB set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("tenant: admin database unavailable (%v)", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("tenant: admin database unavailable (%v)", err)
	}
	t.Cleanup(pool.Close)
	return pool, pool
}

// ---------------------------------------------------------------------------
// 1. Classification — the anti-rot test
// ---------------------------------------------------------------------------

// TestEveryTableIsClassified is the test that makes the two hand-maintained
// registries in census.go trustworthy.
//
// Every base table in `public` must be classified as exactly one of: enumerated
// by its company_id column, listed in SpecialTenancy, or listed in
// NotTenantScoped. A migration that adds a tenant table with a company_id is
// picked up automatically and this test stays green. A migration that adds a
// tenant table whose tenancy is encoded some other way — as statutory_step's
// "irn:<companyID>:<voucherID>" primary key is — makes this test RED, by name,
// on the day it lands. That is the only moment at which somebody still knows how
// that table's tenancy works.
//
// A hardcoded table list cannot do this: it goes stale silently, and silence is
// the whole problem. TWO features now rest on it — teardown deletes what the
// census names and backup serialises what the census names — so an
// unclassified table is simultaneously a data leak in the test suite and a
// silently incomplete archive in the product.
func TestEveryTableIsClassified(t *testing.T) {
	_, c := conn(t)
	ctx := context.Background()

	census, err := tenant.BaseTables(ctx, c)
	if err != nil {
		t.Fatalf("census: %v", err)
	}
	if len(census) == 0 {
		t.Fatal("census returned no tables — the check would be vacuous; is this the right database?")
	}
	scopedTables, err := tenant.ScopedTables(ctx, c)
	if err != nil {
		t.Fatalf("scoped tables: %v", err)
	}

	inCensus := map[string]bool{}
	for _, tbl := range census {
		inCensus[tbl] = true
	}

	for _, tbl := range census {
		var classes []string
		if _, ok := scopedTables[tbl]; ok {
			classes = append(classes, "company_id column")
		}
		if _, ok := tenant.SpecialTenancy[tbl]; ok {
			classes = append(classes, "SpecialTenancy")
		}
		if _, ok := tenant.NotTenantScoped[tbl]; ok {
			classes = append(classes, "NotTenantScoped")
		}
		switch len(classes) {
		case 1:
			// classified exactly once: good.
		case 0:
			t.Errorf("table %q is UNCLASSIFIED: it has no company_id column and appears in "+
				"neither SpecialTenancy nor NotTenantScoped in internal/platform/tenant/census.go. "+
				"Until it is classified, its rows leak on every test run AND are silently missing "+
				"from every backup. Add it to one of the two registries, with the reason.", tbl)
		default:
			t.Errorf("table %q is classified %d ways (%s); exactly one classification is required, "+
				"or the delete plan, the residue check and the backup will disagree about it",
				tbl, len(classes), strings.Join(classes, " + "))
		}
	}

	// A registry entry naming a table that no longer exists is rot in the other
	// direction: it silently narrows the census check.
	for tbl := range tenant.SpecialTenancy {
		if !inCensus[tbl] {
			t.Errorf("SpecialTenancy names %q, which is not a base table in public — remove the stale entry", tbl)
		}
	}
	for tbl := range tenant.NotTenantScoped {
		if !inCensus[tbl] {
			t.Errorf("NotTenantScoped names %q, which is not a base table in public — remove the stale entry", tbl)
		}
	}

	t.Logf("classified %d base tables: %d by company_id, %d SpecialTenancy, %d NotTenantScoped",
		len(census), len(scopedTables), len(tenant.SpecialTenancy), len(tenant.NotTenantScoped))
}

// TestCensusCoversEveryClassifiedTenantTable asserts the PROPERTY that the whole
// extraction exists to guarantee: the plan Census returns names every scoped
// table and every special table, and nothing else.
//
// It is deliberately derived from the same catalog queries rather than from a
// literal count: an assertion that the plan covers "62 tables" would need
// editing on the day a migration lands, and the editing is where a real omission
// gets waved through.
func TestCensusCoversEveryClassifiedTenantTable(t *testing.T) {
	_, c := conn(t)
	ctx := context.Background()

	scopedTables, err := tenant.ScopedTables(ctx, c)
	if err != nil {
		t.Fatalf("scoped tables: %v", err)
	}
	pl, err := tenant.Census(ctx, c)
	if err != nil {
		t.Fatalf("census: %v", err)
	}

	want := map[string]bool{}
	for tbl := range scopedTables {
		want[tbl] = true
	}
	for tbl := range tenant.SpecialTenancy {
		want[tbl] = true
	}

	got := map[string]bool{}
	for _, tbl := range pl.Tables() {
		if got[tbl] {
			t.Errorf("table %q appears twice in the census plan", tbl)
		}
		got[tbl] = true
	}

	var missing, extra []string
	for tbl := range want {
		if !got[tbl] {
			missing = append(missing, tbl)
		}
	}
	for tbl := range got {
		if !want[tbl] {
			extra = append(extra, tbl)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("the census plan OMITS classified tenant tables: %s — "+
			"teardown will leak them and backup will silently skip them", strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("the census plan covers unclassified tables: %s", strings.Join(extra, ", "))
	}
	t.Logf("census plan covers %d tables (%d scoped + %d special)",
		len(pl.Tables()), len(pl.Order), len(pl.Special))
}

// TestTenantRootIsFirstInInsertOrder pins the ordering property a RESTORE rests
// on, stated in the direction restore reads it. Order is delete order (children
// first, company last), so reversed it is insert order — and the company row
// must be inserted before anything that references it, or every RLS WITH CHECK
// on the new tenant fails.
func TestTenantRootIsFirstInInsertOrder(t *testing.T) {
	_, c := conn(t)
	pl, err := tenant.Census(context.Background(), c)
	if err != nil {
		t.Fatalf("census: %v", err)
	}
	if len(pl.Order) == 0 {
		t.Fatal("empty plan")
	}
	if last := pl.Order[len(pl.Order)-1].Table; last != tenant.TenantRoot {
		t.Errorf("the tenant root %q must be LAST in delete order (= first in insert order), got %q",
			tenant.TenantRoot, last)
	}
}

// TestReverseTopoReportsACycle proves the cycle branch is real code and not a
// comment. A silently-chosen order under a cycle would fail intermittently
// rather than always, which is the worst possible failure mode — for a teardown
// harness and equally for a restore, which reads this order backwards.
//
// It moved here from internal/platform/testdb with the function it tests: a
// pure-logic test left behind in the package that used to own the logic is a
// test nobody thinks to run when they change the logic.
func TestReverseTopoReportsACycle(t *testing.T) {
	target := map[string]bool{"a": true, "b": true, "c": true}
	edges := []tenant.FKEdge{{Child: "a", Parent: "b"}, {Child: "b", Parent: "c"}, {Child: "c", Parent: "a"}}
	_, err := tenant.ReverseTopo(target, edges)
	if err == nil {
		t.Fatal("ReverseTopo accepted a cycle; it must refuse rather than pick an arbitrary order")
	}
	for _, name := range []string{"a", "b", "c"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("cycle error must name every table in the cycle; %q missing from: %v", name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. The one value a restore has to rewrite
// ---------------------------------------------------------------------------

// TestRewriteSpecialKey covers the ONE place in a restore where a value — not
// just a column — carries tenancy and must be transformed. statutory_step's
// primary key is "irn:<companyID>:<voucherID>"; restoring under a new company id
// without re-spelling it leaves rows that still name the SOURCE company, which
// means the restored company can re-attempt an IRN that was already raised.
func TestRewriteSpecialKey(t *testing.T) {
	const oldID = "11111111-1111-1111-1111-111111111111"
	const newID = "22222222-2222-2222-2222-222222222222"

	t.Run("rewrites the company segment and nothing else", func(t *testing.T) {
		got, handled, err := tenant.RewriteSpecialKey("statutory_step",
			"irn:"+oldID+":33333333-3333-3333-3333-333333333333", oldID, newID)
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if !handled {
			t.Fatal("statutory_step must be handled; it is the registered special-key table")
		}
		want := "irn:" + newID + ":33333333-3333-3333-3333-333333333333"
		if got != want {
			t.Errorf("rewrote to %q, want %q", got, want)
		}
	})

	t.Run("a voucher id equal to the company id is not also rewritten", func(t *testing.T) {
		// The anchoring is the point. A blind strings.Replace would rewrite BOTH
		// segments here and produce a workflow_id that names a voucher that does
		// not exist — silently, since nothing in the schema ties the two.
		got, _, err := tenant.RewriteSpecialKey("statutory_step",
			"irn:"+oldID+":"+oldID, oldID, newID)
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if want := "irn:" + newID + ":" + oldID; got != want {
			t.Errorf("rewrote to %q, want %q — the rewrite must be anchored on the FIRST segment only", got, want)
		}
	})

	t.Run("a row naming another company is refused, not restored", func(t *testing.T) {
		_, _, err := tenant.RewriteSpecialKey("statutory_step",
			"irn:99999999-9999-9999-9999-999999999999:x", oldID, newID)
		if err == nil {
			t.Fatal("a workflow_id naming a DIFFERENT company must be refused: the archive mixes tenants")
		}
	})

	t.Run("an unrecognised shape is refused, not half-rewritten", func(t *testing.T) {
		if _, _, err := tenant.RewriteSpecialKey("statutory_step", "gstr:"+oldID+":x", oldID, newID); err == nil {
			t.Fatal("a value not matching the registered prefix must be refused")
		}
		if _, _, err := tenant.RewriteSpecialKey("statutory_step", "irn:"+oldID, oldID, newID); err == nil {
			t.Fatal("a value with no segment separator must be refused")
		}
	})

	t.Run("an ordinary table is reported as unhandled, not silently altered", func(t *testing.T) {
		got, handled, err := tenant.RewriteSpecialKey("voucher", "anything", oldID, newID)
		if err != nil || handled || got != "anything" {
			t.Errorf("got (%q, %v, %v); an unregistered table must pass through untouched and unhandled",
				got, handled, err)
		}
	})
}

// TestEverySpecialKeyRewriteHasAPredicate keeps the two registries in step. A
// table whose VALUE encodes tenancy but whose ROWS cannot be found is a table
// the backup skips and the restore would rewrite nothing of; the reverse is a
// table that is archived and then restored still naming the source company.
func TestEverySpecialKeyRewriteHasAPredicate(t *testing.T) {
	for tbl := range tenant.SpecialTenancy {
		if tenant.SpecialKeyColumn(tbl) == "" {
			t.Errorf("SpecialTenancy names %q but no special-key column is registered for it: "+
				"a restore under a new company id would leave its rows naming the SOURCE company. "+
				"Register the column, or state why its tenancy needs no rewrite.", tbl)
		}
	}
}
