package query

import (
	"strings"
	"testing"
)

// These tests are HERMETIC (no database): they exercise the deterministic
// compiler directly. They prove the two core safety properties at the SQL-text
// level — (1) every identifier is whitelist-mapped, (2) every user value becomes
// a $N arg and never appears literally in the SQL — plus the validation rejects.

func TestCompile_BasicAggregate(t *testing.T) {
	q := Query{
		Entity:     EntityVouchers,
		Metrics:    []Metric{{Func: AggSum, Column: "taxable_value", As: "total_sales"}},
		Dimensions: []string{"month"},
		Filters:    []Filter{{Column: "voucher_type_name", Op: OpEq, Values: []string{"Sales"}}},
		DateRange:  &DateRange{Column: "voucher_date", From: "2025-04-01", To: "2026-03-31"},
		OrderBy:    []OrderTerm{{Column: "month", Desc: false}},
		Limit:      100,
	}
	sql, args, err := Compile(q)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// The three user values are bound as $1,$2,$3 in order.
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(args), args)
	}
	for _, want := range []string{"$1", "$2", "$3", "GROUP BY", "LIMIT 100", "to_char(vv.voucher_date,'YYYY-MM')"} {
		if !strings.Contains(sql, want) {
			t.Errorf("sql missing %q:\n%s", want, sql)
		}
	}
	// The baked current-version + not-deleted guard must be present.
	if !strings.Contains(sql, "vh.is_deleted = false") || !strings.Contains(sql, "current_version_id = vv.id") {
		t.Errorf("missing current-version/not-deleted guard:\n%s", sql)
	}
}

// TestCompile_NoLiteralValueLeak is the load-bearing safety assertion: no user
// value (filter value, date) appears as literal text in the compiled SQL; they
// are only ever $N args. A weakened compiler that concatenated a value flips this.
func TestCompile_NoLiteralValueLeak(t *testing.T) {
	q := Query{
		Entity:     EntityVouchers,
		Metrics:    []Metric{{Func: AggSum, Column: "taxable_value", As: "t"}},
		Dimensions: []string{"month"},
		Filters: []Filter{
			{Column: "voucher_type_name", Op: OpEq, Values: []string{"Sales' OR '1'='1"}},
			{Column: "party", Op: OpIn, Values: []string{"Acme", "Globex"}},
		},
		DateRange: &DateRange{Column: "voucher_date", From: "2025-04-01", To: ""},
	}
	sql, args, err := Compile(q)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, v := range []string{"Sales' OR '1'='1", "Acme", "Globex", "2025-04-01"} {
		if strings.Contains(sql, v) {
			t.Fatalf("user value %q leaked into SQL:\n%s", v, sql)
		}
	}
	// All four values must be present as args, parameterized.
	if len(args) != 4 {
		t.Fatalf("expected 4 args (3 strings + 1 date), got %d: %v", len(args), args)
	}
	if !strings.Contains(sql, "IN ($2,$3)") {
		t.Errorf("IN list not parameterized as ($2,$3):\n%s", sql)
	}
}

func TestCompile_RejectsUnknownEntity(t *testing.T) {
	_, _, err := Compile(Query{Entity: "secret_table", Dimensions: []string{"x"}})
	if err == nil || !IsCompileError(err) {
		t.Fatalf("expected CompileError for unknown entity, got %v", err)
	}
}

func TestCompile_RejectsUnknownColumns(t *testing.T) {
	cases := []struct {
		name string
		q    Query
	}{
		{"dimension", Query{Entity: EntityVouchers, Dimensions: []string{"payload"}}},
		{"metric", Query{Entity: EntityVouchers, Metrics: []Metric{{Func: AggSum, Column: "hash"}}}},
		{"filter", Query{Entity: EntityVouchers, Dimensions: []string{"month"}, Filters: []Filter{{Column: "node_id", Op: OpEq, Values: []string{"x"}}}}},
		{"date", Query{Entity: EntityVouchers, Dimensions: []string{"month"}, DateRange: &DateRange{Column: "created_at", From: "2025-01-01"}}},
		{"order", Query{Entity: EntityVouchers, Dimensions: []string{"month"}, OrderBy: []OrderTerm{{Column: "cgst"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Compile(tc.q); err == nil || !IsCompileError(err) {
				t.Fatalf("expected CompileError, got %v", err)
			}
		})
	}
}

func TestCompile_RejectsUnknownOpAndAgg(t *testing.T) {
	if _, _, err := Compile(Query{Entity: EntityVouchers, Dimensions: []string{"month"},
		Filters: []Filter{{Column: "voucher_type_name", Op: "; DROP", Values: []string{"x"}}}}); err == nil {
		t.Fatal("expected reject for unknown op")
	}
	if _, _, err := Compile(Query{Entity: EntityVouchers,
		Metrics: []Metric{{Func: "exec", Column: "taxable_value"}}}); err == nil {
		t.Fatal("expected reject for unknown agg func")
	}
}

func TestCompile_OrderByMustBeSelected(t *testing.T) {
	// Ordering by a metric ALIAS that was selected is fine.
	q := Query{Entity: EntityVouchers, Dimensions: []string{"month"},
		Metrics: []Metric{{Func: AggSum, Column: "taxable_value", As: "tot"}},
		OrderBy: []OrderTerm{{Column: "tot", Desc: true}}}
	if _, _, err := Compile(q); err != nil {
		t.Fatalf("order by selected alias should compile: %v", err)
	}
	// Ordering by a column that exists in the whitelist but was NOT selected is rejected.
	q2 := Query{Entity: EntityVouchers, Dimensions: []string{"month"},
		OrderBy: []OrderTerm{{Column: "party_name"}}}
	if _, _, err := Compile(q2); err == nil {
		t.Fatal("expected reject: order by un-selected column")
	}
}

func TestCompile_EmptyInRejected(t *testing.T) {
	q := Query{Entity: EntityVouchers, Dimensions: []string{"month"},
		Filters: []Filter{{Column: "party", Op: OpIn, Values: nil}}}
	if _, _, err := Compile(q); err == nil {
		t.Fatal("expected reject: empty IN list")
	}
}

func TestCompile_EnumValueTypeEnforced(t *testing.T) {
	// dr_cr only accepts D/C.
	if _, _, err := Compile(Query{Entity: EntityPostings, Dimensions: []string{"ledger_name"},
		Filters: []Filter{{Column: "dr_cr", Op: OpEq, Values: []string{"X"}}}}); err == nil {
		t.Fatal("expected reject: bad dr_cr value")
	}
	// nature only accepts the four enum values.
	if _, _, err := Compile(Query{Entity: EntityPostings, Dimensions: []string{"ledger_name"},
		Filters: []Filter{{Column: "nature", Op: OpEq, Values: []string{"ROBOT"}}}}); err == nil {
		t.Fatal("expected reject: bad nature value")
	}
}

func TestCompile_LimitClamp(t *testing.T) {
	base := func(l int) Query {
		return Query{Entity: EntityVouchers, Dimensions: []string{"month"}, Limit: l}
	}
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, "LIMIT 100"},
		{50, "LIMIT 50"},
		{5000, "LIMIT 1000"},
		{-3, "LIMIT 100"},
	} {
		sql, _, err := Compile(base(tc.in))
		if err != nil {
			t.Fatalf("compile limit %d: %v", tc.in, err)
		}
		if !strings.HasSuffix(sql, tc.want) {
			t.Errorf("limit %d -> want suffix %q, got:\n%s", tc.in, tc.want, sql)
		}
	}
}

func TestCompile_OutstandingSignOrientation(t *testing.T) {
	// Receivable (ASSET/default): debit - credit.
	rec := Query{Entity: EntityOutstandings, Dimensions: []string{"ledger_name"},
		Metrics: []Metric{{Func: AggSum, Column: "pending", As: "p"}},
		Filters: []Filter{{Column: "nature", Op: OpEq, Values: []string{"ASSET"}}}}
	sql, _, err := Compile(rec)
	if err != nil {
		t.Fatalf("rec: %v", err)
	}
	if !strings.Contains(sql, "WHEN ba.dr_cr='D' THEN ba.amount ELSE -ba.amount") {
		t.Errorf("receivable orientation wrong:\n%s", sql)
	}
	// Payable (LIABILITY): credit - debit.
	pay := rec
	pay.Filters = []Filter{{Column: "nature", Op: OpEq, Values: []string{"LIABILITY"}}}
	sql, _, err = Compile(pay)
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if !strings.Contains(sql, "WHEN ba.dr_cr='C' THEN ba.amount ELSE -ba.amount") {
		t.Errorf("payable orientation wrong:\n%s", sql)
	}
}

func TestSQLPreview_PlaceholdersOnly(t *testing.T) {
	q := Query{Entity: EntityVouchers, Dimensions: []string{"month"},
		Filters: []Filter{{Column: "voucher_type_name", Op: OpEq, Values: []string{"secretvalue123"}}}}
	prev := SQLPreview(q)
	if strings.Contains(prev, "secretvalue123") {
		t.Fatalf("preview leaked value:\n%s", prev)
	}
	if !strings.Contains(prev, "$1") {
		t.Fatalf("preview missing placeholder:\n%s", prev)
	}
}

func TestSanitizeAlias(t *testing.T) {
	// A malicious alias is reduced to [a-z0-9_] and quoted, so it cannot inject.
	q := Query{Entity: EntityVouchers, Dimensions: []string{"month"},
		Metrics: []Metric{{Func: AggSum, Column: "taxable_value", As: `x"); DROP TABLE ledger;--`}}}
	sql, _, err := Compile(q)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if strings.Contains(sql, "DROP TABLE") {
		t.Fatalf("alias injection leaked:\n%s", sql)
	}
}
