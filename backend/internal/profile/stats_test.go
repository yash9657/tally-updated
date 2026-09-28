package profile

import (
	"strings"
	"testing"
	"time"

	"github.com/khata/cored/internal/authz"
	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func day(s string) time.Time {
	t, err := time.Parse(isoDate, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestPercentileIsNearestRank(t *testing.T) {
	xs := []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	cases := map[int]int{50: 50, 90: 90, 100: 100, 1: 10, 91: 100}
	for p, want := range cases {
		if got := percentile(xs, p); got != want {
			t.Errorf("p%d = %d, want %d", p, got, want)
		}
	}
	if got := percentile([]int{7}, 90); got != 7 {
		t.Errorf("single-element p90 = %d", got)
	}
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("empty = %d", got)
	}
}

// A part-payment does not end the lag: 10% on day 3, the rest on day 90, is a
// 90-day payer. NON-VACUOUS: make settledLag return on the FIRST settlement
// and this reads 3.
func TestSettledLagWaitsForFullSettlement(t *testing.T) {
	b := bill{raisedOn: day("2025-04-01"), amount: d("1000"), settlements: []settlement{
		{on: day("2025-06-30"), amount: d("900"), voucherID: "late"},
		{on: day("2025-04-04"), amount: d("100"), voucherID: "early"}, // out of order on purpose
	}}
	days, vid, ok := settledLag(b)
	if !ok || days != 90 || vid != "late" {
		t.Fatalf("got %d days by %q ok=%v, want 90 by late", days, vid, ok)
	}
	b.settlements = b.settlements[1:]
	if _, _, ok := settledLag(b); ok {
		t.Fatal("a 10% part-paid bill is still open")
	}
}

func TestABCIsParetoOnCumulativeShareAndOrderIndependent(t *testing.T) {
	items := []abcItem{
		{"c", d("50")}, {"a", d("700")}, {"b", d("150")}, {"d", d("50")}, {"e", d("50")}, {"z", d("0")},
	}
	// total 1000: a before=0% A; b before=70% A (crosses 80); c before=85% B;
	// d before=90% B; e before=95% -> C; z zero -> C.
	want := map[string]string{"a": "A", "b": "A", "c": "B", "d": "B", "e": "C", "z": "C"}
	got := abcClasses(items)
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %s, want %s (all: %v)", k, got[k], w, got)
		}
	}
	rev := make([]abcItem, len(items))
	for i := range items {
		rev[i] = items[len(items)-1-i]
	}
	got2 := abcClasses(rev)
	for k := range want {
		if got[k] != got2[k] {
			t.Errorf("classification depends on input order at %s", k)
		}
	}
}

func TestCoefficientOfVariationAndXYZ(t *testing.T) {
	steady := []decimal.Decimal{d("20"), d("20"), d("20"), d("20")}
	cv, ok := coefficientOfVariation(steady)
	if !ok || !cv.IsZero() || xyzClass(cv) != "X" {
		t.Errorf("steady cv = %s ok=%v", cv, ok)
	}
	// One month of twelve: mean 1, stddev sqrt(11) = 3.3166 -> Z.
	one := make([]decimal.Decimal, 12)
	for i := range one {
		one[i] = decimal.Zero
	}
	one[3] = d("12")
	cv, _ = coefficientOfVariation(one)
	if !cv.Equal(d("3.3166")) || xyzClass(cv) != "Z" {
		t.Errorf("single-month cv = %s (%s), want 3.3166 Z", cv, xyzClass(cv))
	}
	// 10, 30: mean 20, sd 10 -> 0.5 -> X (boundary inclusive).
	cv, _ = coefficientOfVariation([]decimal.Decimal{d("10"), d("30")})
	if !cv.Equal(d("0.5")) || xyzClass(cv) != "X" {
		t.Errorf("boundary cv = %s (%s)", cv, xyzClass(cv))
	}
	if _, ok := coefficientOfVariation([]decimal.Decimal{decimal.Zero}); ok {
		t.Error("a zero mean has no CV")
	}
}

func occ(date, amt, id string) occurrence {
	return occurrence{on: day(date), amount: d(amt), voucherID: id}
}

// The plan's tolerances, exactly: ±3 days, ±5%, >= 3 occurrences.
func TestDetectStreamTolerances(t *testing.T) {
	monthly := []occurrence{
		occ("2025-04-05", "25000", "1"), occ("2025-05-07", "25000", "2"), // +32 days
		occ("2025-06-05", "25500", "3"), occ("2025-07-04", "24500", "4"), // +2% / -2%
		occ("2025-08-05", "25000", "5"),
	}
	s, ok := detectStream(monthly)
	if !ok || len(s.occurrences) != 5 || s.cadence != "monthly" || !s.total.Equal(d("125000")) || !s.medianAmount.Equal(d("25000")) {
		t.Fatalf("monthly stream: ok=%v %+v", ok, s)
	}

	// An amount 6% off breaks the run: the longest clean run is kept.
	broken := append([]occurrence(nil), monthly...)
	broken[4] = occ("2025-08-05", "26600", "5") // +6.4% vs median 25000
	s, ok = detectStream(broken)
	if !ok || len(s.occurrences) != 4 {
		t.Fatalf("a 6%% outlier must end the run at 4; got ok=%v n=%d", ok, len(s.occurrences))
	}

	// An interval 4 days off the median breaks it too.
	late := []occurrence{occ("2025-04-05", "100", "a"), occ("2025-05-05", "100", "b"), occ("2025-06-04", "100", "c"), occ("2025-07-08", "100", "d")}
	s, ok = detectStream(late)
	if !ok || len(s.occurrences) != 3 {
		t.Fatalf("34 vs median 30 days is outside ±3; want the first 3, got ok=%v n=%d", ok, len(s.occurrences))
	}

	// Two occurrences are not a stream.
	if _, ok := detectStream(monthly[:2]); ok {
		t.Error("two occurrences must not be a stream")
	}
	// A renegotiated rent is two streams; the longest wins.
	reneg := []occurrence{
		occ("2025-04-05", "20000", "1"), occ("2025-05-05", "20000", "2"), occ("2025-06-05", "20000", "3"),
		occ("2025-07-05", "30000", "4"), occ("2025-08-05", "30000", "5"), occ("2025-09-05", "30000", "6"), occ("2025-10-05", "30000", "7"),
	}
	s, ok = detectStream(reneg)
	if !ok || len(s.occurrences) != 4 || !s.medianAmount.Equal(d("30000")) {
		t.Fatalf("renegotiated: want the 4-long 30000 run, got ok=%v n=%d median=%s", ok, len(s.occurrences), s.medianAmount)
	}
	// Same-day duplicates are an anomaly, not a stream.
	dup := []occurrence{occ("2025-04-05", "100", "a"), occ("2025-04-05", "100", "b"), occ("2025-04-05", "100", "c")}
	if _, ok := detectStream(dup); ok {
		t.Error("three payments on one day are not a stream")
	}
}

// TestVocabularyIsClosedAndCheckable: every fact declares exactly one of a
// report it reconciles to or statistic=true; the report is a real report
// name; the unit is in migration 0096's CHECK set; keys are unique; every
// job writes something.
func TestVocabularyIsClosedAndCheckable(t *testing.T) {
	reports := map[string]bool{}
	for _, n := range authz.KnownReportNames {
		reports[n] = true
	}
	units := map[Unit]bool{UnitINR: true, UnitDays: true, UnitQty: true, UnitQtyPerDay: true, UnitPct: true, UnitCount: true, UnitRatio: true, UnitClass: true}
	seen := map[string]bool{}
	perJob := map[string]int{}
	for _, f := range Facts {
		if seen[f.Key] {
			t.Errorf("duplicate fact key %q", f.Key)
		}
		seen[f.Key] = true
		if (f.ReconcilesTo != "") == f.Statistic {
			t.Errorf("%s: exactly one of ReconcilesTo / Statistic must be set", f.Key)
		}
		if f.ReconcilesTo != "" && !reports[f.ReconcilesTo] {
			t.Errorf("%s reconciles to %q, which is not a report name", f.Key, f.ReconcilesTo)
		}
		if !units[f.Unit] {
			t.Errorf("%s: unit %q is not in migration 0096's CHECK set", f.Key, f.Unit)
		}
		if f.Label == "" || f.Job == "" {
			t.Errorf("%s: label and job are required", f.Key)
		}
		perJob[f.Job]++
	}
	for _, j := range []string{JobCustomer, JobSupplier, JobItem, JobStream, JobTenant} {
		if perJob[j] == 0 {
			t.Errorf("job %s declares no facts", j)
		}
	}
}

func TestEstimateTokensIsConservative(t *testing.T) {
	if got := EstimateTokens("abcdef"); got != 2 {
		t.Errorf("6 ascii = %d, want 2", got)
	}
	if got := EstimateTokens("abcdefg"); got != 3 {
		t.Errorf("7 ascii = %d, want 3 (rounded up)", got)
	}
	if got := EstimateTokens("बाकी"); got != 4 {
		t.Errorf("4 Devanagari runes = %d, want 4", got)
	}
	// A realistic English line is estimated at no LESS than len/4 (the usual
	// BPE average) — the direction that matters.
	s := strings.Repeat("Kaveri Traders: Rs 3200.00, DSO 45.6 days\n", 20)
	if EstimateTokens(s) < len(s)/4 {
		t.Errorf("estimate %d is below len/4 = %d", EstimateTokens(s), len(s)/4)
	}
}
