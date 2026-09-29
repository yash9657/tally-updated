package server

import (
	"testing"

	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/query"
)

// TestEveryQueryEntityDeclaresItsReports is the fail-first half of §6.10: a
// new DSL entity answers no one until it names the reports it reveals, and
// every name it gives must be one a rule can actually write, or the grant it
// claims to honour could never be expressed.
func TestEveryQueryEntityDeclaresItsReports(t *testing.T) {
	known := map[string]bool{}
	for _, n := range authz.KnownReportNames {
		known[n] = true
	}
	for _, doc := range query.Catalog() {
		reps, ok := reportsForQuery(query.Query{Entity: query.Entity(doc.Entity)})
		if !ok {
			t.Errorf("entity %q is in the query catalogue but not in queryEntityReports: it is refused to everyone", doc.Entity)
			continue
		}
		if len(reps) == 0 {
			t.Errorf("entity %q declares no reports, so no report grant governs it", doc.Entity)
		}
		for _, r := range reps {
			if !known[r] {
				t.Errorf("entity %q names report %q, which is not in authz.KnownReportNames", doc.Entity, r)
			}
		}
	}
}

// TestOutstandingsNeedsPayablesUnlessPinnedToAsset pins the direction: only an
// exact nature = ASSET filter drops the payables report.
func TestOutstandingsNeedsPayablesUnlessPinnedToAsset(t *testing.T) {
	cases := []struct {
		name    string
		filters []query.Filter
		want    int
	}{
		{"no filter", nil, 2},
		{"nature eq ASSET", []query.Filter{{Column: "nature", Op: query.OpEq, Values: []string{"ASSET"}}}, 1},
		{"nature eq LIABILITY", []query.Filter{{Column: "nature", Op: query.OpEq, Values: []string{"LIABILITY"}}}, 2},
		{"nature in both", []query.Filter{{Column: "nature", Op: query.OpIn, Values: []string{"ASSET", "LIABILITY"}}}, 2},
		{"nature ne LIABILITY", []query.Filter{{Column: "nature", Op: query.OpNe, Values: []string{"LIABILITY"}}}, 2},
	}
	for _, c := range cases {
		reps, _ := reportsForQuery(query.Query{Entity: query.EntityOutstandings, Filters: c.filters})
		if len(reps) != c.want {
			t.Errorf("%s: requires %v, want %d report(s)", c.name, reps, c.want)
		}
	}
}
