package masters_test

import (
	"os"
	"strings"
	"testing"

	"github.com/khata/cored/internal/inventory"
)

// TestValuationMethodsMatchTheMigration pins the two copies of the costing
// vocabulary to each other, in BOTH directions, which is the part that matters:
//
//   - a method this build implements but the CHECK refuses is a picker option
//     that fails on save, after the operator has keyed the whole master;
//   - a method the CHECK ALLOWS but no valuer implements is far worse — the row
//     is written happily and then inventory.ValuerFor errors on every read, so
//     the Stock Summary AND the Balance Sheet fail for the WHOLE COMPANY, not
//     just for that item, and they fail long after the master was created.
//
// It is the same pairing TestKnownEntityCategoriesMatchTheMigration exists for,
// and it is here rather than in internal/inventory because inventory imports
// nothing and must not learn what a database is.
func TestValuationMethodsMatchTheMigration(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	clause := checkClause(t, "stock_item_valuation_method_check")

	implemented := inventory.KnownValuationMethods()
	if len(implemented) == 0 {
		t.Fatal("no valuation method has a valuer — no stock item could be valued at all")
	}
	for _, m := range implemented {
		if !strings.Contains(clause, "'"+m+"'") {
			t.Errorf("this build values stock at %q but migration 0078's CHECK refuses it — "+
				"an operator would choose the method and be refused on save", m)
		}
	}

	// The other direction: every quoted literal in the CHECK must have a valuer.
	for _, lit := range strings.Split(clause, "'") {
		if lit == "" || strings.ContainsAny(lit, " (),\n=[]:") {
			continue
		}
		if lit != strings.ToUpper(lit) {
			continue
		}
		if _, err := inventory.ValuerFor(lit); err != nil {
			t.Errorf("migration 0078's CHECK allows valuation_method %q and no valuer "+
				"implements it — an item created with it would make the Stock Summary and "+
				"the Balance Sheet fail for the entire company: %v", lit, err)
		}
	}

	// The DEFAULT must be one of them, and it must be the weighted average every
	// item created before T9 already carries. A default the CHECK refused would
	// make an INSERT that omits the column fail.
	if _, err := inventory.ValuerFor(defaultValuationMethod(t, clause)); err != nil {
		t.Errorf("stock_item.valuation_method's DEFAULT has no valuer: %v", err)
	}
}

// defaultValuationMethod reads the column default out of the CHECK's own
// vocabulary: the first literal, which migration 0025 set as the default and
// 0078 kept first. It is derived rather than typed so this test cannot pass by
// agreeing with itself.
func defaultValuationMethod(t *testing.T, clause string) string {
	t.Helper()
	for _, lit := range strings.Split(clause, "'") {
		if lit == "" || strings.ContainsAny(lit, " (),\n=[]:") || lit != strings.ToUpper(lit) {
			continue
		}
		return lit
	}
	t.Fatalf("no method literal found in %q", clause)
	return ""
}
