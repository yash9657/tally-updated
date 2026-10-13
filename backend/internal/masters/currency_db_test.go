package masters_test

// DB-BACKED tests for the CURRENCY MASTER and its rates (T8, migration 0080).
//
// ⛔⛔ WHAT THESE ARE FOR THAT A PURE TEST CANNOT BE. Three of the four claims
// below are properties of the SCHEMA, not of the Go, and a pure test would be
// asserting the mock rather than the product:
//
//   - the base-currency refusal is a plpgsql TRIGGER, and the Go only translates
//     its message. A test that stubbed the database would prove the translation
//     and not the refusal;
//   - the rate really survives ten decimals, which is a column type;
//   - and the rate lookup really is "the most recent rate on or before the date"
//     rather than "the most recent rate", which is one ORDER BY away from being
//     silently wrong in a way every figure derived from it would hide.

import (
	"errors"
	"strings"
	"testing"

	"github.com/khata/cored/internal/masters"
)

func TestCreateCurrencyRefusesTheBaseCurrency(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	// CONTROL FIRST: a currency that is not the base must be accepted, so a
	// blanket refusal cannot masquerade as this rule working.
	if _, err := m.CreateCurrency(ctx, masters.Currency{
		Code: "USD", Symbol: "$", FormalName: "US Dollars", DecimalPlaces: 2,
	}, "tester"); err != nil {
		t.Fatalf("control: USD should be accepted: %v", err)
	}

	for _, code := range []string{"INR", "inr", "  InR  "} {
		_, err := m.CreateCurrency(ctx, masters.Currency{
			Code: code, Symbol: "₹", FormalName: "Indian Rupees", DecimalPlaces: 2,
		}, "tester")
		if err == nil {
			t.Fatalf("currency %q was accepted, but it IS the company base currency", code)
		}
		// ⛔ THE TYPED ERROR, NOT THE DATABASE'S SENTENCE. The operator's next
		// action here is unique — there is nothing to rename — so the HTTP layer
		// must be able to tell this refusal from a name clash.
		if !errors.Is(err, masters.ErrCurrencyIsBase) {
			t.Errorf("currency %q: refused, but not as ErrCurrencyIsBase: %v", code, err)
		}
	}
}

func TestCreateCurrencyRefusesMoreDecimalsThanTheWireCarries(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	// 0 is REAL (yen, won) and must be accepted — the control that stops this
	// from passing because everything is refused.
	if _, err := m.CreateCurrency(ctx, masters.Currency{
		Code: "JPY", Symbol: "¥", FormalName: "Japanese Yen", DecimalPlaces: 0,
	}, "tester"); err != nil {
		t.Fatalf("control: a 0-decimal currency is real and must be accepted: %v", err)
	}
	for _, dp := range []int{5, 6, -1} {
		if _, err := m.CreateCurrency(ctx, masters.Currency{
			Code: "XX", Symbol: "x", FormalName: "Test", DecimalPlaces: dp,
		}, "tester"); err == nil {
			t.Errorf("decimal_places %d was accepted; money is fixed-4 on the wire", dp)
		}
	}
}

func TestExchangeRateSurvivesTenDecimalsAndRefusesNonsense(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	if _, err := m.CreateCurrency(ctx, masters.Currency{
		Code: "IDR", Symbol: "Rp", FormalName: "Indonesian Rupiah", DecimalPlaces: 2,
	}, "tester"); err != nil {
		t.Fatalf("create IDR: %v", err)
	}
	const wanted = "0.0033333333"
	if err := m.SetExchangeRate(ctx, "IDR", "2026-08-27", wanted, "tester"); err != nil {
		t.Fatalf("set rate: %v", err)
	}
	got, ok, err := m.RateAsOn(ctx, "IDR", "2026-08-27")
	if err != nil || !ok {
		t.Fatalf("rate as on: ok=%v err=%v", ok, err)
	}
	if got.String() != wanted {
		t.Errorf("rate round trip: got %q want %q — the tenth decimal is the point", got.String(), wanted)
	}

	// ⛔ A rate of zero values the whole holding at nothing, silently.
	for _, bad := range []string{"0", "-83.65", "", "abc", "NaN", "Inf"} {
		if err := m.SetExchangeRate(ctx, "IDR", "2026-09-01", bad, "tester"); err == nil {
			t.Errorf("rate %q was accepted", bad)
		}
	}
}

// TestRateAsOnTakesTheRateInForceNotTheLatest is the one that would go silently
// wrong. Every figure derived from a rate is a plausible number, so a lookup
// that returned the NEWEST rate rather than the one in force on the date would
// produce a revaluation report that is wrong with nothing on it disagreeing.
func TestRateAsOnTakesTheRateInForceNotTheLatest(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	if _, err := m.CreateCurrency(ctx, masters.Currency{
		Code: "USD", Symbol: "$", FormalName: "US Dollars", DecimalPlaces: 2,
	}, "tester"); err != nil {
		t.Fatalf("create USD: %v", err)
	}
	for _, r := range []struct{ date, rate string }{
		{"2026-04-01", "80.0000000000"},
		{"2026-06-01", "83.6500000000"},
		{"2026-08-01", "88.0000000000"},
	} {
		if err := m.SetExchangeRate(ctx, "USD", r.date, r.rate, "tester"); err != nil {
			t.Fatalf("set rate %s: %v", r.date, err)
		}
	}
	for _, c := range []struct{ asOn, want string }{
		{"2026-04-01", "80"},    // exactly on the first effective date
		{"2026-05-31", "80"},    // before the second takes effect
		{"2026-06-01", "83.65"}, // exactly on it
		{"2026-07-15", "83.65"}, // ⛔ between two rates: the EARLIER one rules
		{"2026-12-31", "88"},    // after the last
	} {
		got, ok, err := m.RateAsOn(ctx, "USD", c.asOn)
		if err != nil || !ok {
			t.Fatalf("as on %s: ok=%v err=%v", c.asOn, ok, err)
		}
		if got.String() != c.want {
			t.Errorf("as on %s: got %s, want %s — a rate lookup that ignores the "+
				"date silently restates history", c.asOn, got.String(), c.want)
		}
	}

	// ⛔ BEFORE ANY RATE EXISTS THERE IS NO RATE, AND THAT IS NOT ZERO.
	_, ok, err := m.RateAsOn(ctx, "USD", "2026-03-31")
	if err != nil {
		t.Fatalf("as on before the first rate: %v", err)
	}
	if ok {
		t.Error("a date before every rate reported a rate; a zero here would value " +
			"the whole holding at nothing and every derived figure would stay plausible")
	}
}

func TestListCurrenciesShowsAnAbsentRateAsAbsent(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	if _, err := m.CreateCurrency(ctx, masters.Currency{
		Code: "AED", Symbol: "د.إ", FormalName: "UAE Dirham", DecimalPlaces: 2,
	}, "tester"); err != nil {
		t.Fatalf("create AED: %v", err)
	}
	list, err := m.ListCurrencies(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, c := range list {
		if c.Code != "AED" {
			continue
		}
		found = true
		// ⛔ EMPTY, NOT "0". A currency with no rate cannot key a voucher, and a
		// zero would offer the choice anyway and value the invoice at nothing.
		if c.LatestRate != "" || c.LatestRateDate != "" {
			t.Errorf("a currency with no rate reported rate=%q date=%q, want both empty",
				c.LatestRate, c.LatestRateDate)
		}
	}
	if !found {
		t.Error("a currency with no rate was omitted from the list entirely; " +
			"'I created AED and it vanished' is the operator's experience")
	}
	if err := m.SetExchangeRate(ctx, "AED", "2026-08-27", "22.7700000000", "tester"); err != nil {
		t.Fatalf("set rate: %v", err)
	}
	list, _ = m.ListCurrencies(ctx)
	for _, c := range list {
		if c.Code == "AED" && !strings.HasPrefix(c.LatestRate, "22.77") {
			t.Errorf("after setting a rate the list still shows %q", c.LatestRate)
		}
	}
}
