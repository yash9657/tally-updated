package query

// translate_test.go is the hermetic, DETERMINISTIC test of the FakeTranslator:
// every time-relative question derives from an INJECTED clock (a fixed instant),
// never time.Now(), so the date ranges are reproducible. It also proves the
// canonical NL -> Query mappings and that gibberish returns ErrUntranslatable.
// It runs WITHOUT a database (the translator never touches one).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fixedNow builds a FakeTranslator whose clock is pinned to the given instant
// (UTC) and whose FY starts in April (the Indian default).
func fixedNow(y int, m time.Month, d int) FakeTranslator {
	return FakeTranslator{
		Now: func() time.Time { return time.Date(y, m, d, 12, 0, 0, 0, time.UTC) },
	}
}

func TestFakeTranslatorTrialBalance(t *testing.T) {
	q, err := fixedNow(2025, time.July, 15).ToQuery(context.Background(), uuid.New(), "trial balance")
	if err != nil {
		t.Fatalf("trial balance: unexpected error %v", err)
	}
	if q.Entity != EntityLedgerBalances {
		t.Fatalf("entity = %q, want ledger_balances", q.Entity)
	}
	if len(q.Dimensions) != 1 || q.Dimensions[0] != "group_name" {
		t.Fatalf("dimensions = %v, want [group_name]", q.Dimensions)
	}
	if len(q.Metrics) != 2 {
		t.Fatalf("metrics = %v, want sum(debit)+sum(credit)", q.Metrics)
	}
}

func TestFakeTranslatorBalancesByGroup(t *testing.T) {
	q, err := fixedNow(2025, time.July, 15).ToQuery(context.Background(), uuid.New(), "balances by group")
	if err != nil {
		t.Fatalf("balances by group: unexpected error %v", err)
	}
	if q.Entity != EntityLedgerBalances {
		t.Fatalf("entity = %q, want ledger_balances", q.Entity)
	}
}

// TestFakeTranslatorSalesThisMonth pins the clock to 2025-07-15 and asserts the
// derived month range is exactly 2025-07-01..2025-07-31.
func TestFakeTranslatorSalesThisMonth(t *testing.T) {
	q, err := fixedNow(2025, time.July, 15).ToQuery(context.Background(), uuid.New(), "sales this month")
	if err != nil {
		t.Fatalf("sales this month: unexpected error %v", err)
	}
	if q.Entity != EntityVouchers {
		t.Fatalf("entity = %q, want vouchers", q.Entity)
	}
	if q.DateRange == nil {
		t.Fatalf("date_range nil, want a month range")
	}
	if q.DateRange.From != "2025-07-01" || q.DateRange.To != "2025-07-31" {
		t.Fatalf("month range = %s..%s, want 2025-07-01..2025-07-31", q.DateRange.From, q.DateRange.To)
	}
	if q.DateRange.Column != "voucher_date" {
		t.Fatalf("date column = %q, want voucher_date", q.DateRange.Column)
	}
	// The Sales filter is present.
	if len(q.Filters) != 1 || q.Filters[0].Column != "voucher_type_name" || q.Filters[0].Values[0] != "Sales" {
		t.Fatalf("filters = %v, want voucher_type_name=Sales", q.Filters)
	}
}

// TestFakeTranslatorSalesThisQuarter pins 2025-05-10 (calendar Q2: Apr-Jun) and
// asserts 2025-04-01..2025-06-30.
func TestFakeTranslatorSalesThisQuarter(t *testing.T) {
	q, err := fixedNow(2025, time.May, 10).ToQuery(context.Background(), uuid.New(), "sales this quarter")
	if err != nil {
		t.Fatalf("sales this quarter: unexpected error %v", err)
	}
	if q.DateRange == nil || q.DateRange.From != "2025-04-01" || q.DateRange.To != "2025-06-30" {
		t.Fatalf("quarter range = %v, want 2025-04-01..2025-06-30", q.DateRange)
	}
}

// TestFakeTranslatorSalesThisYearFY pins 2025-05-10 and asserts the FINANCIAL
// year (April start) 2025-04-01..2026-03-31 — and that a date in Jan-Mar maps to
// the PRIOR FY start year.
func TestFakeTranslatorSalesThisYearFY(t *testing.T) {
	// In May 2025 -> FY 2025-26.
	q, err := fixedNow(2025, time.May, 10).ToQuery(context.Background(), uuid.New(), "sales this year")
	if err != nil {
		t.Fatalf("sales this year: unexpected error %v", err)
	}
	if q.DateRange == nil || q.DateRange.From != "2025-04-01" || q.DateRange.To != "2026-03-31" {
		t.Fatalf("FY range (May) = %v, want 2025-04-01..2026-03-31", q.DateRange)
	}

	// In Feb 2026 -> still FY 2025-26 (the FY began April 2025).
	q2, err := fixedNow(2026, time.February, 10).ToQuery(context.Background(), uuid.New(), "sales this year")
	if err != nil {
		t.Fatalf("sales this year (Feb): unexpected error %v", err)
	}
	if q2.DateRange == nil || q2.DateRange.From != "2025-04-01" || q2.DateRange.To != "2026-03-31" {
		t.Fatalf("FY range (Feb) = %v, want 2025-04-01..2026-03-31", q2.DateRange)
	}
}

// TestFakeTranslatorFYStartMonthOverride proves a non-April FY start is honored.
func TestFakeTranslatorFYStartMonthOverride(t *testing.T) {
	f := FakeTranslator{
		Now:          func() time.Time { return time.Date(2025, time.August, 1, 0, 0, 0, 0, time.UTC) },
		FYStartMonth: int(time.January), // calendar year
	}
	q, err := f.ToQuery(context.Background(), uuid.New(), "sales this year")
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if q.DateRange == nil || q.DateRange.From != "2025-01-01" || q.DateRange.To != "2025-12-31" {
		t.Fatalf("calendar-year range = %v, want 2025-01-01..2025-12-31", q.DateRange)
	}
}

func TestFakeTranslatorTotalPurchases(t *testing.T) {
	q, err := fixedNow(2025, time.July, 15).ToQuery(context.Background(), uuid.New(), "total purchases")
	if err != nil {
		t.Fatalf("total purchases: unexpected error %v", err)
	}
	if q.Entity != EntityVouchers {
		t.Fatalf("entity = %q, want vouchers", q.Entity)
	}
	if len(q.Filters) != 1 || q.Filters[0].Values[0] != "Purchase" {
		t.Fatalf("filters = %v, want voucher_type_name=Purchase", q.Filters)
	}
}

func TestFakeTranslatorTopDebtors(t *testing.T) {
	q, err := fixedNow(2025, time.July, 15).ToQuery(context.Background(), uuid.New(), "top debtors")
	if err != nil {
		t.Fatalf("top debtors: unexpected error %v", err)
	}
	if q.Entity != EntityOutstandings {
		t.Fatalf("entity = %q, want outstandings", q.Entity)
	}
	if len(q.Filters) != 1 || q.Filters[0].Column != "nature" || q.Filters[0].Values[0] != "ASSET" {
		t.Fatalf("filters = %v, want nature=ASSET", q.Filters)
	}
}

func TestFakeTranslatorPayables(t *testing.T) {
	q, err := fixedNow(2025, time.July, 15).ToQuery(context.Background(), uuid.New(), "payables")
	if err != nil {
		t.Fatalf("payables: unexpected error %v", err)
	}
	if q.Entity != EntityOutstandings {
		t.Fatalf("entity = %q, want outstandings", q.Entity)
	}
	if q.Filters[0].Values[0] != "LIABILITY" {
		t.Fatalf("nature = %q, want LIABILITY", q.Filters[0].Values[0])
	}
}

// TestFakeTranslatorUnknownIsUntranslatable proves gibberish (and empty input)
// returns the typed ErrUntranslatable the endpoint maps to a 422.
func TestFakeTranslatorUnknownIsUntranslatable(t *testing.T) {
	f := fixedNow(2025, time.July, 15)
	for _, nl := range []string{"", "   ", "what is the meaning of life", "delete all vouchers"} {
		_, err := f.ToQuery(context.Background(), uuid.New(), nl)
		if !errors.Is(err, ErrUntranslatable) {
			t.Fatalf("ToQuery(%q) err = %v, want ErrUntranslatable", nl, err)
		}
	}
}

// TestFakeTranslatorDeterminism proves two calls with the SAME injected clock
// produce IDENTICAL date ranges (no hidden time.Now()).
func TestFakeTranslatorDeterminism(t *testing.T) {
	f := fixedNow(2025, time.July, 15)
	a, _ := f.ToQuery(context.Background(), uuid.New(), "sales this month")
	b, _ := f.ToQuery(context.Background(), uuid.New(), "sales this month")
	if a.DateRange.From != b.DateRange.From || a.DateRange.To != b.DateRange.To {
		t.Fatalf("non-deterministic range: %v vs %v", a.DateRange, b.DateRange)
	}
}

// TestProducedQueriesCompile proves every canonical question yields a Query that
// the WS1 compiler ACCEPTS — i.e. the translator only ever emits whitelisted
// entities/columns/ops. This couples WS2 to WS1's whitelist so a future drift
// (a renamed column) fails here rather than at runtime.
func TestProducedQueriesCompile(t *testing.T) {
	f := fixedNow(2025, time.July, 15)
	for _, nl := range ExampleQuestions {
		q, err := f.ToQuery(context.Background(), uuid.New(), nl)
		if err != nil {
			t.Fatalf("example %q did not translate: %v", nl, err)
		}
		if _, _, err := Compile(q); err != nil {
			t.Fatalf("example %q produced an uncompilable Query: %v", nl, err)
		}
	}
}
