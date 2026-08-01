package masters_test

// ANTI-DRIFT for the deductor-status vocabulary.
//
// Three places name the s.402(11)(a)–(l) categories: the constants in
// internal/compliance, migration 0049's CHECK, and (via the API) the client's
// picker. The API serves the Go list so the client cannot drift, which leaves
// ONE pair that can: Go against the migration.
//
// These tests read the CHECK constraint out of the live catalogue rather than a
// copy of it, so a category added to one side and not the other fails here
// instead of becoming a picker option the database refuses — or, worse, a row
// the database accepts that no Go code can evaluate.

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/platform/testdb"
)

// checkClause returns the SQL text of one CHECK constraint.
func checkClause(t *testing.T, constraint string) string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var clause string
	if err := conn.QueryRow(ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1`,
		constraint).Scan(&clause); err != nil {
		t.Fatalf("read constraint %s: %v", constraint, err)
	}
	return clause
}

// TestKnownEntityCategoriesMatchTheMigration is the pair that can drift.
//
// It asserts BOTH directions, which is the part that matters: a category in Go
// but not in the CHECK is a picker option that fails on save, and a category in
// the CHECK but not in Go is a row compliance.MustDeduct answers
// ErrUnknownEntityCategory for — a company that recorded its status and still
// cannot post.
func TestKnownEntityCategoriesMatchTheMigration(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	clause := checkClause(t, "company_deductor_status_category_known")

	for _, c := range compliance.KnownEntityCategories {
		if !strings.Contains(clause, "'"+c+"'") {
			t.Errorf("compliance.KnownEntityCategories has %q but migration 0049's "+
				"CHECK does not — the picker would offer a category the database "+
				"refuses on save", c)
		}
	}
	// The other direction. Extract every quoted literal in the CHECK and require
	// Go to know it.
	for _, lit := range strings.Split(clause, "'") {
		// Split on quotes yields alternating outside/inside; a category is an
		// all-caps token with no spaces.
		if lit == "" || strings.ContainsAny(lit, " (),\n") {
			continue
		}
		if lit != strings.ToUpper(lit) {
			continue
		}
		if !compliance.IsKnownEntityCategory(lit) {
			t.Errorf("migration 0049's CHECK allows %q but compliance does not know "+
				"it — a company could record that status and then be refused every "+
				"TDS post with ErrUnknownEntityCategory", lit)
		}
	}

	if len(compliance.KnownEntityCategories) != 15 {
		t.Errorf("expected the 15 categories of s.402(11)(a)–(l), got %d",
			len(compliance.KnownEntityCategories))
	}
	// No duplicates: a repeated entry would render twice in the picker.
	seen := append([]string(nil), compliance.KnownEntityCategories...)
	sort.Strings(seen)
	for i := 1; i < len(seen); i++ {
		if seen[i] == seen[i-1] {
			t.Errorf("duplicate category %q", seen[i])
		}
	}
}

// TestNaturalPersonCategoriesMatchTheMigration pins the OTHER list that has to
// agree with 0049: which categories the turnover test can reach.
//
// masters.isNaturalPerson decides whether to demand a turnover figure, and
// migration 0049's turnover_required_for_natural_persons CHECK decides whether
// to accept the row. If they disagree, one direction gives a confusing constraint
// violation instead of the sentence masters wrote, and the other silently stores
// a status that cannot be evaluated.
func TestNaturalPersonCategoriesMatchTheMigration(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	clause := checkClause(t, "company_deductor_status_turnover_required_for_natural_persons")

	// The four the Go side treats as subject to the test.
	want := []string{
		compliance.EntityIndividual, compliance.EntityHUF,
		compliance.EntityAOP, compliance.EntityBOI,
	}
	for _, c := range want {
		if !strings.Contains(clause, "'"+c+"'") {
			t.Errorf("masters treats %q as subject to the turnover test but migration "+
				"0049's turnover_required CHECK does not name it", c)
		}
	}
	// And nothing else. COMPANY appearing here would mean the database demands a
	// turnover that masters refuses to supply — an unstorable status.
	for _, c := range compliance.KnownEntityCategories {
		named := strings.Contains(clause, "'"+c+"'")
		expected := false
		for _, w := range want {
			if w == c {
				expected = true
			}
		}
		if named != expected {
			t.Errorf("category %q: named in the turnover_required CHECK = %v, but "+
				"masters treats it as subject to the test = %v", c, named, expected)
		}
	}
}
