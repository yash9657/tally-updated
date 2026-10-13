package query

import (
	"strings"
	"testing"
)

// w3_compile_test.go is the HERMETIC gate for the three W3 entities
// (stock_movements, bank_statement_lines, profile_facts). The DB half —
// that the numbers equal the reports — is w3_execute_test.go; this half pins
// the decisions that no amount of result-set equality would catch because the
// seeded book simply never exercises them.

// TestW3_EntitiesCarryTheirGuards pins the constant WHERE guards. Each one is
// the difference between a right answer and a plausible one:
//
//   - stock_movements without the current-version join counts an altered
//     line twice (the tombstone of a DELETE carries no movement rows, so the
//     is_deleted guard is belt-and-braces — w3_execute_test.go measured it);
//   - profile_facts without BOTH temporal halves shows a corrected-away or a
//     historical value next to today's.
//
// NON-VACUOUS: blank either whereGuard in whitelist.go and this fails.
func TestW3_EntitiesCarryTheirGuards(t *testing.T) {
	cases := []struct {
		entity Entity
		want   []string
	}{
		{EntityStockMovements, []string{"vh.is_deleted = false", "vh.current_version_id = vv.id"}},
		{EntityProfileFacts, []string{"f.superseded_at IS NULL", "f.valid_to IS NULL"}},
		{EntityBankStatementLines, []string{"LEFT JOIN bank_statement_match bm", "bm.line_id = bl.id"}},
	}
	for _, tc := range cases {
		sql, _, err := Compile(Query{Entity: tc.entity, Metrics: []Metric{{Func: AggCount, Column: "*", As: "n"}}})
		if err != nil {
			t.Fatalf("%s: compile: %v", tc.entity, err)
		}
		for _, w := range tc.want {
			if !strings.Contains(sql, w) {
				t.Errorf("%s: compiled SQL lost %q:\n%s", tc.entity, w, sql)
			}
		}
	}
}

// TestW3_TheNarrationIsNeverADimension pins the injection decision on
// bank_statement_lines: third-party narration text can be SEARCHED (a bound $N
// value) but never RETURNED. NON-VACUOUS: add "description" to the entity's
// dimensions map and the first assertion fails.
func TestW3_TheNarrationIsNeverADimension(t *testing.T) {
	_, _, err := Compile(Query{Entity: EntityBankStatementLines, Dimensions: []string{"description"}})
	if err == nil || !IsCompileError(err) {
		t.Fatalf("description must NOT be a dimension (it would put third-party text in the result); err=%v", err)
	}
	// ...and it IS a filter, bound as a parameter.
	sql, args, err := Compile(Query{
		Entity:  EntityBankStatementLines,
		Metrics: []Metric{{Func: AggSum, Column: "withdrawals", As: "w"}},
		Filters: []Filter{{Column: "description", Op: OpLike, Values: []string{"%IGNORE PREVIOUS INSTRUCTIONS%"}}},
	})
	if err != nil {
		t.Fatalf("description filter must compile: %v", err)
	}
	if strings.Contains(sql, "IGNORE") {
		t.Fatalf("the search value leaked into SQL text:\n%s", sql)
	}
	if len(args) != 1 || args[0] != "%IGNORE PREVIOUS INSTRUCTIONS%" {
		t.Fatalf("the search value must be the single bound arg; args=%v", args)
	}
	// Nor may any other entity expose a free-text third-party column as a
	// dimension by accident: the dimension whitelist of every W3 entity is
	// pinned exactly here.
	want := map[Entity][]string{
		EntityBankStatementLines: {"bank_ledger_name", "dr_cr", "match_status", "month", "quarter", "year"},
	}
	for ent, dims := range want {
		got := map[string]bool{}
		for d := range entities[ent].dimensions {
			got[d] = true
		}
		if len(got) != len(dims) {
			t.Errorf("%s dimensions = %v, want exactly %v", ent, got, dims)
		}
		for _, d := range dims {
			if !got[d] {
				t.Errorf("%s: missing dimension %q", ent, d)
			}
		}
	}
}

// TestW3_NoValuedIssueMetric pins the stock decision: there is no metric that
// could be mistaken for the valued cost of goods issued. NON-VACUOUS: add an
// "outward_value" (or any *value*) metric to stock_movements and this fails.
func TestW3_NoValuedIssueMetric(t *testing.T) {
	for name := range entities[EntityStockMovements].metrics {
		if strings.Contains(name, "value") || strings.Contains(name, "cost") {
			t.Errorf("stock_movements metric %q reads as a VALUATION, which SQL cannot compute (the valuer replays movements); "+
				"name it for what it sums or route the question to the stock reports", name)
		}
	}
	// rate is min/max/avg only: a SUM of rates is not a number anyone can use.
	if _, _, err := Compile(Query{Entity: EntityStockMovements, Metrics: []Metric{{Func: AggSum, Column: "rate", As: "r"}}}); err == nil {
		t.Fatal("sum(rate) must be refused")
	}
	sql, _, err := Compile(Query{Entity: EntityStockMovements, Metrics: []Metric{{Func: AggMax, Column: "rate", As: "r"}}})
	if err != nil || !strings.Contains(sql, "MAX(sm.rate)") {
		t.Fatalf("max(rate) must compile to MAX(sm.rate); err=%v sql=%s", err, sql)
	}
}

// TestW3_ValueTypesRejectNonsense proves the three new closed value types
// reject a literal outside their enum — a direction of 'SIDEWAYS' would
// otherwise return zero rows and read as "no movement".
func TestW3_ValueTypesRejectNonsense(t *testing.T) {
	bad := []Query{
		{Entity: EntityStockMovements, Metrics: []Metric{{Func: AggCount, Column: "*"}},
			Filters: []Filter{{Column: "direction", Op: OpEq, Values: []string{"SIDEWAYS"}}}},
		{Entity: EntityBankStatementLines, Metrics: []Metric{{Func: AggCount, Column: "*"}},
			Filters: []Filter{{Column: "match_status", Op: OpEq, Values: []string{"reconciled"}}}},
		{Entity: EntityProfileFacts, Metrics: []Metric{{Func: AggCount, Column: "*"}},
			Filters: []Filter{{Column: "entity_kind", Op: OpEq, Values: []string{"company"}}}},
		{Entity: EntityBankStatementLines, Metrics: []Metric{{Func: AggCount, Column: "*"}},
			Filters: []Filter{{Column: "description", Op: OpIn, Values: []string{"a", "b"}}}},
	}
	for i, q := range bad {
		if _, _, err := Compile(q); err == nil || !IsCompileError(err) {
			t.Errorf("case %d must be a CompileError, got %v", i, err)
		}
	}
	good := []Query{
		{Entity: EntityStockMovements, Metrics: []Metric{{Func: AggCount, Column: "*"}},
			Filters: []Filter{{Column: "direction", Op: OpIn, Values: []string{"IN", "OUT"}}}},
		{Entity: EntityBankStatementLines, Metrics: []Metric{{Func: AggCount, Column: "*"}},
			Filters: []Filter{{Column: "match_status", Op: OpEq, Values: []string{"unresolved"}}}},
		{Entity: EntityProfileFacts, Metrics: []Metric{{Func: AggSum, Column: "value"}},
			Filters: []Filter{{Column: "entity_kind", Op: OpEq, Values: []string{"party"}}}},
	}
	for i, q := range good {
		if _, _, err := Compile(q); err != nil {
			t.Errorf("good case %d must compile: %v", i, err)
		}
	}
}

// TestW3_EveryFilterOfEveryEntityBindsItsValue is GENERIC over the whole
// catalogue, not just W3's three entities, so the next widening inherits it:
// for every filter column of every entity, a hostile literal must reach the
// args and never the SQL text.
//
// NON-VACUOUS: change compile()'s default filter branch to fmt.Sprintf the
// value in and every row of this fails.
func TestW3_EveryFilterOfEveryEntityBindsItsValue(t *testing.T) {
	const hostile = "x'); DROP TABLE ledger; --"
	checked, total := 0, 0
	for ent, def := range entities {
		for col, fc := range def.filters {
			total++
			var op Op
			for _, candidate := range []Op{OpEq, OpLike, OpIn} {
				if fc.ops[candidate] {
					op = candidate
					break
				}
			}
			if op == "" {
				continue
			}
			v := hostile
			switch fc.valType {
			case valueDate, valueNature, valueDrCr, valueDirection, valueMatchStatus, valueEntityKind:
				// A closed type rejects the hostile literal outright, which is
				// the stronger outcome; assert THAT instead.
				_, _, err := Compile(Query{Entity: ent, Metrics: []Metric{{Func: AggCount, Column: "*"}},
					Filters: []Filter{{Column: col, Op: op, Values: []string{v}}}})
				if err == nil {
					t.Errorf("%s.%s: a closed-type filter accepted %q", ent, col, v)
				}
				checked++
				continue
			}
			sql, args, err := Compile(Query{Entity: ent, Metrics: []Metric{{Func: AggCount, Column: "*"}},
				Filters: []Filter{{Column: col, Op: op, Values: []string{v}}}})
			if err != nil {
				t.Errorf("%s.%s: %v", ent, col, err)
				continue
			}
			if strings.Contains(sql, "DROP TABLE") {
				t.Errorf("%s.%s: the value reached the SQL text:\n%s", ent, col, sql)
			}
			found := false
			for _, a := range args {
				if a == v {
					found = true
				}
			}
			if !found {
				t.Errorf("%s.%s: the value is not among the bound args %v", ent, col, args)
			}
			checked++
		}
	}
	// Every filter column must have been exercised: one with no eq/like/in
	// operator would be skipped silently and this is where that shows.
	if checked != total || total == 0 {
		t.Fatalf("checked %d of %d filter columns — the walk skipped some or saw none", checked, total)
	}
	t.Logf("checked %d filter columns across %d entities", checked, len(entities))
}
