package query_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/query"
)

// query_security_test.go is the ADVERSARIAL GATE: it proves the query core is
// unbreakable against the threat model — SQL injection, cross-tenant leakage,
// and writes. Each test is NON-VACUOUS: a deliberately weakened compiler (one
// that concatenated a value, dropped the read-only tx, or skipped the company
// GUC) would flip at least one assertion. See the inline notes per test.

// injectionPayloads are the hostile strings sprayed into every DSL string field.
var injectionPayloads = []string{
	"1; DROP TABLE ledger",
	"x' OR '1'='1",
	") UNION SELECT id, name FROM app_user --",
	"'; DELETE FROM voucher_version; --",
	" bad-unicode\u2603 quote' /* x */",
	"`backtick`",
	"\\'; UPDATE ledger SET name='pwned'; --",
}

// rawCount counts rows in a table for ONE company using the admin pool, so the
// gate can prove the physical table is intact (existence + row count) after
// hostile input, independent of RLS. Scoping to the test's own company makes the
// assertion robust to concurrent writers from OTHER tenants (e.g. the server
// package's tests sharing this live Postgres when packages run in parallel) — an
// unscoped global count would flake and produce a false "injection mutated the
// DB!" failure.
func rawCount(t *testing.T, table string, companyID uuid.UUID) int64 {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer conn.Close(ctx)
	var n int64
	// Table name is a hard-coded literal from this test, never user input; the
	// company id is a bound param.
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE company_id = $1", companyID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestSecurity_InjectionInEveryStringField sprays injection payloads into Entity,
// Metric.Column, Filter.Column, Filter.Values, DateRange, and OrderBy. Each query
// must EITHER be rejected by the compiler (unknown identifier) OR execute with the
// payload as a harmless bound param returning zero rows — and the database must be
// byte-for-byte intact afterward.
//
// NON-VACUOUS: a compiler that concatenated Filter.Values would let
// "'; DELETE FROM voucher_version; --" through, and the post-state ledger/voucher
// counts would drop — flipping the intact-DB assertion.
func TestSecurity_InjectionInEveryStringField(t *testing.T) {
	pool, ctx, companyID, ids := setupCompany(t, "Sec Co")
	seedSales(t, pool, ctx, ids)

	ledgerBefore := rawCount(t, "ledger", companyID)
	voucherBefore := rawCount(t, "voucher_version", companyID)

	for _, p := range injectionPayloads {
		// (a) payload as Entity -> always rejected (unknown entity).
		if _, err := query.Execute(ctx, pool, query.Query{Entity: query.Entity(p), Dimensions: []string{"month"}}); err == nil {
			t.Errorf("entity payload %q was not rejected", p)
		}
		// (b) payload as Metric.Column -> rejected (unknown metric).
		if _, err := query.Execute(ctx, pool, query.Query{Entity: query.EntityVouchers,
			Metrics: []query.Metric{{Func: query.AggSum, Column: p}}}); err == nil {
			t.Errorf("metric column payload %q was not rejected", p)
		}
		// (c) payload as Filter.Column -> rejected (unknown filter column).
		if _, err := query.Execute(ctx, pool, query.Query{Entity: query.EntityVouchers,
			Dimensions: []string{"month"},
			Filters:    []query.Filter{{Column: p, Op: query.OpEq, Values: []string{"x"}}}}); err == nil {
			t.Errorf("filter column payload %q was not rejected", p)
		}
		// (d) payload as Filter.Values on a VALID column -> must execute and return
		// the payload as a bound param (zero matching rows), never as SQL.
		res, err := query.Execute(ctx, pool, query.Query{Entity: query.EntityVouchers,
			Dimensions: []string{"month"},
			Metrics:    []query.Metric{{Func: query.AggCount, Column: "*", As: "n"}},
			Filters:    []query.Filter{{Column: "voucher_type_name", Op: query.OpEq, Values: []string{p}}}})
		if err != nil {
			t.Errorf("filter value payload %q errored (should be a harmless param): %v", p, err)
		} else if len(res.Rows) != 0 {
			t.Errorf("filter value payload %q matched %d rows, expected 0", p, len(res.Rows))
		}
		// (e) payload as DateRange.Column -> rejected (unknown date column).
		if _, err := query.Execute(ctx, pool, query.Query{Entity: query.EntityVouchers,
			Dimensions: []string{"month"},
			DateRange:  &query.DateRange{Column: p, From: "2025-04-01"}}); err == nil {
			t.Errorf("date column payload %q was not rejected", p)
		}
		// (f) payload as OrderBy.Column -> rejected (not a selected dim/alias).
		if _, err := query.Execute(ctx, pool, query.Query{Entity: query.EntityVouchers,
			Dimensions: []string{"month"},
			OrderBy:    []query.OrderTerm{{Column: p}}}); err == nil {
			t.Errorf("order-by payload %q was not rejected", p)
		}
	}

	// The database must be physically intact: the tables still exist and the row
	// counts are unchanged.
	if got := rawCount(t, "ledger", companyID); got != ledgerBefore {
		t.Fatalf("ledger row count changed: before=%d after=%d (injection mutated the DB!)", ledgerBefore, got)
	}
	if got := rawCount(t, "voucher_version", companyID); got != voucherBefore {
		t.Fatalf("voucher_version row count changed: before=%d after=%d", voucherBefore, got)
	}
}

// TestSecurity_CrossTenantIsolation seeds two companies and proves a query run
// under company A returns ONLY A's rows — even when the DSL adds a filter that
// tries to widen to company B (RLS ANDs in company_id = app_current_company_id(),
// so the forged value is harmless). A's data and B's data are disjoint by name.
//
// NON-VACUOUS: a compiler/runner that dropped the company GUC (RunInReadOnlyTx's
// set_config) would let RLS default-deny return ZERO rows (or, if it bypassed RLS,
// return B's rows too) — either flips an assertion.
func TestSecurity_CrossTenantIsolation(t *testing.T) {
	poolA, ctxA, _, idsA := setupCompany(t, "Tenant A")
	seedSales(t, poolA, ctxA, idsA)

	// Company B in its own scope, with a uniquely named ledger.
	ctx := context.Background()
	companyB, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("company B id: %v", err)
	}
	ctxB := db.WithCompany(ctx, companyB)
	// Reuse the same pool object (RLS is per-tx GUC, not per-pool). Company B gets
	// only its default seeded ledgers — no user ledgers — so any of A's user
	// ledgers appearing under B would be a leak.
	idsB := identity.NewService(poolA)
	if _, err := idsB.CreateCompany(ctxB, identity.Company{
		ID:          companyB,
		Name:        "Tenant B " + companyB.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company B: %v", err)
	}

	// Query ledger_balances under A, with a (harmless) attempt to reference B by
	// adding a ledger_name filter for a name only B could have. Since we cannot
	// inject company_id directly (not a whitelisted filter), we assert positively:
	// A sees its own "Domestic Sales" ledger; running the SAME query under B sees
	// zero of A's ledgers.
	qAllLedgers := query.Query{
		Entity:     query.EntityLedgerBalances,
		Dimensions: []string{"ledger_name"},
		Metrics:    []query.Metric{{Func: query.AggCount, Column: "*", As: "n"}},
		Limit:      1000,
	}
	resA, err := query.Execute(ctxA, poolA, qAllLedgers)
	if err != nil {
		t.Fatalf("query A: %v", err)
	}
	if !rowsContainLedger(resA, "Domestic Sales") {
		t.Fatalf("company A should see its own 'Domestic Sales' ledger; rows=%+v", resA.Rows)
	}

	resB, err := query.Execute(ctxB, poolA, qAllLedgers)
	if err != nil {
		t.Fatalf("query B: %v", err)
	}
	if rowsContainLedger(resB, "Domestic Sales") {
		t.Fatalf("company B leaked company A's 'Domestic Sales' ledger; rows=%+v", resB.Rows)
	}
	// B only has the default seeded ledgers (Cash etc.), not A's user ledgers.
	if rowsContainLedger(resB, "Acme Corp") {
		t.Fatalf("company B leaked company A's 'Acme Corp' ledger; rows=%+v", resB.Rows)
	}
}

// TestSecurity_WriteAttemptRejected proves a write issued inside the query
// execution path (the read-only tx) is rejected by Postgres and mutates nothing.
//
// NON-VACUOUS: if RunInReadOnlyTx used a normal (read-write) Begin, the DELETE
// would succeed and the ledger count would drop.
func TestSecurity_WriteAttemptRejected(t *testing.T) {
	pool, ctx, companyID, ids := setupCompany(t, "Write Co")
	seedSales(t, pool, ctx, ids)

	before := rawCount(t, "ledger", companyID)

	// Attempt a DELETE within the read-only, company-scoped tx.
	err := pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "DELETE FROM ledger")
		return e
	})
	if err == nil {
		t.Fatal("DELETE inside read-only tx unexpectedly succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "read-only") {
		t.Logf("write rejected (message not asserted verbatim): %v", err)
	}

	if got := rawCount(t, "ledger", companyID); got != before {
		t.Fatalf("ledger count changed after write attempt: before=%d after=%d", before, got)
	}
}

// TestSecurity_UnknownEntityColumnOpRejected is the explicit closed-set gate.
func TestSecurity_UnknownEntityColumnOpRejected(t *testing.T) {
	pool, ctx, _, _ := setupCompany(t, "Closed Co")
	cases := []query.Query{
		{Entity: "app_user", Dimensions: []string{"name"}},
		{Entity: query.EntityVouchers, Metrics: []query.Metric{{Func: "subquery", Column: "taxable_value"}}},
		{Entity: query.EntityVouchers, Dimensions: []string{"month"},
			Filters: []query.Filter{{Column: "voucher_type_name", Op: "regexp", Values: []string{"x"}}}},
	}
	for i, q := range cases {
		if _, err := query.Execute(ctx, pool, q); err == nil || !query.IsCompileError(err) {
			t.Errorf("case %d: expected CompileError, got %v", i, err)
		}
	}
}

// --- helpers ---

func rowsContainLedger(res query.Result, name string) bool {
	for _, r := range res.Rows {
		if len(r) > 0 && r[0] == name {
			return true
		}
	}
	return false
}
