package query

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// widen_compile_test.go is the HERMETIC (no database) gate for the WS2 DSL
// widening: min/max/avg over raw columns, the quarter/year time buckets, and the
// opening-balance-folded ledger_balances metrics. It asserts at the SQL-TEXT
// level, so a compiler that stopped emitting a whitelist fragment, dropped the
// load-bearing inner COALESCE, or let a disallowed aggregate through flips an
// assertion here without needing Postgres.

// TestCompile_SumSQLUnchangedByWidening pins the refactor's compatibility
// contract: after metricCol grew rawExpr+aggs, the SUM path must still emit the
// BYTE-IDENTICAL text the pre-WS2 compiler emitted. If this drifts, every
// currently-shipping query's SQL silently changed.
//
// NON-VACUOUS: it fails if the compiler wraps rawExpr differently (e.g. emits
// SUM(COALESCE(lb.debit,0)) or drops the outer COALESCE).
func TestCompile_SumSQLUnchangedByWidening(t *testing.T) {
	cases := []struct {
		name string
		q    Query
		want string
	}{
		{"ledger_balances debit", Query{Entity: EntityLedgerBalances,
			Metrics: []Metric{{Func: AggSum, Column: "debit", As: "d"}}},
			"COALESCE(SUM(lb.debit),0) AS \"d\""},
		{"ledger_balances credit", Query{Entity: EntityLedgerBalances,
			Metrics: []Metric{{Func: AggSum, Column: "credit", As: "c"}}},
			"COALESCE(SUM(lb.credit),0) AS \"c\""},
		{"ledger_balances net (pre-aggregated, emitted verbatim)", Query{Entity: EntityLedgerBalances,
			Metrics: []Metric{{Func: AggSum, Column: "net", As: "n"}}},
			"COALESCE(SUM(lb.debit),0) - COALESCE(SUM(lb.credit),0) AS \"n\""},
		{"vouchers taxable_value", Query{Entity: EntityVouchers,
			Metrics: []Metric{{Func: AggSum, Column: "taxable_value", As: "t"}}},
			"COALESCE(SUM(vv.taxable_value),0) AS \"t\""},
		{"postings amount", Query{Entity: EntityPostings,
			Metrics: []Metric{{Func: AggSum, Column: "amount", As: "a"}}},
			"COALESCE(SUM(p.amount),0) AS \"a\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql, _, err := Compile(tc.q)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if !strings.Contains(sql, tc.want) {
				t.Fatalf("sum SQL drifted; want substring %q in:\n%s", tc.want, sql)
			}
		})
	}
}

// TestCompile_MinMaxAvgEmitWhitelistFragments proves min/max/avg now compile on
// ROW-LEVEL numeric columns and emit ONLY the whitelist's raw fragment wrapped
// in a literal aggregate name.
//
// NON-VACUOUS: before WS2 every one of these returned a CompileError, so the
// whole test fails against the old compiler.
func TestCompile_MinMaxAvgEmitWhitelistFragments(t *testing.T) {
	cases := []struct {
		entity Entity
		column string
		fn     AggFunc
		want   string
	}{
		{EntityPostings, "amount", AggMin, "MIN(p.amount)"},
		{EntityPostings, "amount", AggMax, "MAX(p.amount)"},
		{EntityPostings, "amount", AggAvg, "AVG(p.amount)"},
		{EntityVouchers, "taxable_value", AggMin, "MIN(vv.taxable_value)"},
		{EntityVouchers, "cgst", AggMax, "MAX(vv.cgst)"},
		{EntityVouchers, "igst", AggAvg, "AVG(vv.igst)"},
		{EntityLedgerBalances, "debit", AggAvg, "AVG(lb.debit)"},
		{EntityLedgerBalances, "credit", AggMin, "MIN(lb.credit)"},
	}
	for _, tc := range cases {
		t.Run(string(tc.fn)+"_"+string(tc.entity)+"_"+tc.column, func(t *testing.T) {
			sql, args, err := Compile(Query{Entity: tc.entity,
				Metrics: []Metric{{Func: tc.fn, Column: tc.column, As: "m"}}})
			if err != nil {
				t.Fatalf("compile %s(%s): %v", tc.fn, tc.column, err)
			}
			if !strings.Contains(sql, tc.want) {
				t.Fatalf("missing %q in:\n%s", tc.want, sql)
			}
			if len(args) != 0 {
				t.Fatalf("metric-only query bound %d args, want 0: %v", len(args), args)
			}
			// MIN/MAX/AVG are deliberately NOT COALESCE'd (see whitelist.go's
			// NULL SEMANTICS note) — assert the pin so a silent change is caught.
			if strings.Contains(sql, "COALESCE("+tc.want) {
				t.Fatalf("min/max/avg unexpectedly COALESCE'd, contradicting the pinned NULL contract:\n%s", sql)
			}
		})
	}
}

// TestCompile_MinMaxAvgRejectedOnComposites proves the closed set still bites:
// composite/pre-aggregated metrics declare aggs:{sum} only, so min/max/avg on
// them keep the original rejection message.
//
// NON-VACUOUS: a compiler that wrapped `net`'s pre-aggregated expr in MIN()
// would emit nested-aggregate SQL and this test would stop seeing an error.
func TestCompile_MinMaxAvgRejectedOnComposites(t *testing.T) {
	for _, fn := range []AggFunc{AggMin, AggMax, AggAvg} {
		// ledger_balances "net" — pre-aggregated composite.
		_, _, err := Compile(Query{Entity: EntityLedgerBalances,
			Metrics: []Metric{{Func: fn, Column: "net", As: "n"}}})
		if err == nil || !IsCompileError(err) {
			t.Fatalf("%s(net): expected CompileError, got %v", fn, err)
		}
		if !strings.Contains(err.Error(), "not supported on column \"net\"") {
			t.Errorf("%s(net): unexpected message %q", fn, err.Error())
		}

		// outstandings "pending" — sign-oriented, special-cased before the
		// whitelist lookup; its own sum-only guard must survive.
		_, _, err = Compile(Query{Entity: EntityOutstandings,
			Dimensions: []string{"ledger_name"},
			Metrics:    []Metric{{Func: fn, Column: "pending", As: "p"}}})
		if err == nil || !IsCompileError(err) {
			t.Fatalf("%s(pending): expected CompileError, got %v", fn, err)
		}
		if !strings.Contains(err.Error(), "only supports sum") {
			t.Errorf("%s(pending): unexpected message %q", fn, err.Error())
		}
	}
}

// TestCompile_MinMaxAvgStillWhitelistGated proves the widening did NOT open a
// hole: an unknown metric column is still rejected under the new aggregates.
func TestCompile_MinMaxAvgStillWhitelistGated(t *testing.T) {
	for _, fn := range []AggFunc{AggMin, AggMax, AggAvg, AggSum} {
		for _, col := range []string{"hash", "payload", "l.opening_balance) FROM ledger --", "*"} {
			if _, _, err := Compile(Query{Entity: EntityVouchers,
				Metrics: []Metric{{Func: fn, Column: col, As: "m"}}}); err == nil || !IsCompileError(err) {
				t.Errorf("%s(%q): expected CompileError, got %v", fn, col, err)
			}
		}
	}
}

// TestCompile_QuarterYearDimensions pins the new time buckets on BOTH entities
// that carry vv.voucher_date. The exact to_char format strings are asserted
// because the bucket LABELS ("2025-Q2", "2025") are part of the wire contract.
func TestCompile_QuarterYearDimensions(t *testing.T) {
	for _, ent := range []Entity{EntityVouchers, EntityPostings} {
		for dim, want := range map[string]string{
			"quarter": `to_char(vv.voucher_date,'YYYY-"Q"Q')`,
			"year":    `to_char(vv.voucher_date,'YYYY')`,
		} {
			sql, _, err := Compile(Query{Entity: ent,
				Dimensions: []string{dim},
				Metrics:    []Metric{{Func: AggCount, Column: "*", As: "n"}},
				OrderBy:    []OrderTerm{{Column: dim}}})
			if err != nil {
				t.Fatalf("%s/%s: compile: %v", ent, dim, err)
			}
			if !strings.Contains(sql, want+` AS "`+dim+`"`) {
				t.Errorf("%s/%s: missing select fragment %q:\n%s", ent, dim, want, sql)
			}
			if !strings.Contains(sql, "GROUP BY "+want) {
				t.Errorf("%s/%s: missing GROUP BY on the bucket expr:\n%s", ent, dim, sql)
			}
		}
	}
	// ledger_balances has no voucher_date, so the buckets must NOT leak there.
	for _, dim := range []string{"quarter", "year"} {
		if _, _, err := Compile(Query{Entity: EntityLedgerBalances, Dimensions: []string{dim}}); err == nil {
			t.Errorf("ledger_balances accepted the %q dimension (no voucher_date in its skeleton)", dim)
		}
	}
}

// TestCompile_OpeningAndClosingNetExprs is the SQL-text guard on the WS2 trap:
// the INNER COALESCEs in closing_net. Without them, NULL from the LEFT JOIN
// propagates and SUM skips every ledger with no posted movement, silently
// dropping its opening balance.
//
// NON-VACUOUS: deleting either COALESCE from closingNetExpr fails this test.
func TestCompile_OpeningAndClosingNetExprs(t *testing.T) {
	sql, _, err := Compile(Query{Entity: EntityLedgerBalances,
		Dimensions: []string{"ledger_name"},
		Metrics: []Metric{
			{Func: AggSum, Column: "opening", As: "op"},
			{Func: AggSum, Column: "closing_net", As: "cl"},
		}})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	wantOpening := `COALESCE(SUM(CASE WHEN l.opening_dc='D' THEN l.opening_balance ELSE -l.opening_balance END),0) AS "op"`
	if !strings.Contains(sql, wantOpening) {
		t.Errorf("opening expr wrong; want %q in:\n%s", wantOpening, sql)
	}
	wantClosing := `COALESCE(SUM((CASE WHEN l.opening_dc='D' THEN l.opening_balance ELSE -l.opening_balance END) + COALESCE(lb.debit,0) - COALESCE(lb.credit,0)),0) AS "cl"`
	if !strings.Contains(sql, wantClosing) {
		t.Errorf("closing_net expr wrong; want %q in:\n%s", wantClosing, sql)
	}
	// Belt and braces: the two inner COALESCEs must be present verbatim.
	for _, frag := range []string{"COALESCE(lb.debit,0)", "COALESCE(lb.credit,0)"} {
		if !strings.Contains(sql, frag) {
			t.Errorf("load-bearing inner %s missing (openings of unposted ledgers would vanish):\n%s", frag, sql)
		}
	}
}

// TestSQLPreview_PlaceholdersOnlyOnWidenedSurface re-runs the placeholder-only
// property over the NEW surface (min/avg metrics + quarter/year dims + a hostile
// filter value), proving the widening did not create a literal-interpolation path.
func TestSQLPreview_PlaceholdersOnlyOnWidenedSurface(t *testing.T) {
	q := Query{
		Entity:     EntityPostings,
		Dimensions: []string{"quarter", "year"},
		Metrics: []Metric{
			{Func: AggMin, Column: "amount", As: "lo"},
			{Func: AggAvg, Column: "amount", As: "mean"},
		},
		Filters: []Filter{
			{Column: "ledger_name", Op: OpEq, Values: []string{"secretvalue123' OR '1'='1"}},
			{Column: "dr_cr", Op: OpEq, Values: []string{"D"}},
		},
		DateRange: &DateRange{Column: "voucher_date", From: "2025-04-01", To: "2026-03-31"},
	}
	prev := SQLPreview(q)
	for _, leak := range []string{"secretvalue123", "2025-04-01", "2026-03-31"} {
		if strings.Contains(prev, leak) {
			t.Fatalf("value %q leaked into the preview:\n%s", leak, prev)
		}
	}
	for _, want := range []string{"$1", "$2", "$3", "$4", "MIN(p.amount)", "AVG(p.amount)"} {
		if !strings.Contains(prev, want) {
			t.Fatalf("preview missing %q:\n%s", want, prev)
		}
	}
}

// TestFakeTranslator_ClosingBalances pins the new NL mapping and proves it is
// matched BEFORE the trial-balance case.
func TestFakeTranslator_ClosingBalances(t *testing.T) {
	f := fixedNow(2025, time.July, 15)
	for _, nl := range []string{"closing balances", "Closing Balances", "trial balance closing"} {
		q, err := f.ToQuery(context.Background(), uuid.New(), nl)
		if err != nil {
			t.Fatalf("%q did not translate: %v", nl, err)
		}
		if q.Entity != EntityLedgerBalances {
			t.Fatalf("%q entity = %q, want ledger_balances", nl, q.Entity)
		}
		if len(q.Metrics) != 1 || q.Metrics[0].Column != "closing_net" || q.Metrics[0].Func != AggSum {
			t.Fatalf("%q metrics = %+v, want one sum(closing_net)", nl, q.Metrics)
		}
		if _, _, err := Compile(q); err != nil {
			t.Fatalf("%q produced an uncompilable Query: %v", nl, err)
		}
	}
	// "trial balance" alone must still map to the movement-only debit/credit form.
	tb, err := f.ToQuery(context.Background(), uuid.New(), "trial balance")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if len(tb.Metrics) != 2 || tb.Metrics[0].Column != "debit" {
		t.Fatalf("trial balance mapping regressed: %+v", tb.Metrics)
	}
}
