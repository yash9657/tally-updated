package insights

import (
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// The migration's CHECK sets, restated here and pinned: a type that the
// table would refuse must fail in CI, not on a tenant's night.
var (
	typeShape   = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)
	entityKinds = map[string]bool{"party": true, "item": true, "voucher": true, "tenant": true, "user": true, "period": true}
)

func TestEveryTypeIsWellFormed(t *testing.T) {
	if len(Types) != 15 {
		t.Errorf("%d types registered; the ADR and the Features screen say 15", len(Types))
	}
	seen := map[string]bool{}
	for _, td := range Types {
		if seen[td.Code] {
			t.Errorf("type %s registered twice", td.Code)
		}
		seen[td.Code] = true
		if !typeShape.MatchString(td.Code) || !entityKinds[td.EntityKind] {
			t.Errorf("%s: code or entity kind %q outside the migration's CHECK", td.Code, td.EntityKind)
		}
		if td.Cadence != RealTime && td.Cadence != Nightly {
			t.Errorf("%s: cadence %q", td.Code, td.Cadence)
		}
		if !td.Actionability.IsPositive() || td.Actionability.GreaterThan(decimal.NewFromInt(1)) {
			t.Errorf("%s: actionability %s outside (0,1]", td.Code, td.Actionability)
		}
		if td.PriorReal <= 0 || td.PriorFalse <= 0 || td.WeeklyCap <= 0 {
			t.Errorf("%s: prior %v:%v cap %d", td.Code, td.PriorReal, td.PriorFalse, td.WeeklyCap)
		}
		if PReal(td, Verdicts{}) < DigestMinPReal {
			t.Errorf("%s: prior p_real %.2f is below the digest floor before any feedback", td.Code, PReal(td, Verdicts{}))
		}
		keys := map[string]bool{}
		evidenced := 0
		for _, f := range td.Fields {
			if keys[f.Key] {
				t.Errorf("%s: field %s twice", td.Code, f.Key)
			}
			keys[f.Key] = true
			if f.Unit.Evidenced() {
				evidenced++
			}
		}
		if evidenced == 0 {
			t.Errorf("%s declares no evidenced figure; the evidence gate would check nothing", td.Code)
		}
		for _, k := range Placeholders(td.Template) {
			if !keys[k] {
				t.Errorf("%s: template names undeclared %s", td.Code, k)
			}
		}
		if strings.TrimSpace(td.Action) == "" || strings.TrimSpace(td.Title) == "" {
			t.Errorf("%s: no title or action", td.Code)
		}
	}
}

func TestOwedTypesAreNotBuilt(t *testing.T) {
	for _, o := range Owed {
		if _, ok := ByCode(o.Type); ok {
			t.Errorf("%s is both built and owed", o.Type)
		}
		if strings.TrimSpace(o.Reason) == "" {
			t.Errorf("%s is owed without a reason", o.Type)
		}
	}
}

func TestRankerTerms(t *testing.T) {
	td, _ := ByCode(TypeCustomerReorderDue) // prior 7:3
	if p := PReal(td, Verdicts{}); math.Abs(p-0.7) > 1e-9 {
		t.Errorf("prior mean %v, want 0.7", p)
	}
	// "not useful" is still REAL: it must not lower p_real.
	if p := PReal(td, Verdicts{NotUseful: 5}); p <= 0.7 {
		t.Errorf("not-useful verdicts lowered p_real to %v", p)
	}
	if p := PReal(td, Verdicts{Wrong: 1}); math.Abs(p-7.0/11) > 1e-9 {
		t.Errorf("one wrong -> %v, want 7/11", p)
	}
	if a := Affinity(Verdicts{}); a != 1 {
		t.Errorf("no-data affinity %v, want 1", a)
	}
	if Affinity(Verdicts{Useful: 3}) <= 1 || Affinity(Verdicts{NotUseful: 3}) >= 1 {
		t.Error("affinity does not move with the recipient's verdicts")
	}
	if Novelty(0) != 1 || Novelty(2) != 0.25 {
		t.Error("novelty is not 0.5^k")
	}
	if s := Score(decimal.NewFromInt(1000), 0.5, 0.8, 0.5, 2); math.Abs(s-400) > 1e-9 {
		t.Errorf("score %v, want 400", s)
	}
}

func TestCanonical(t *testing.T) {
	for _, c := range []struct {
		u    Unit
		in   string
		want string
	}{
		{UnitINR, "12500", "12500.00"}, {UnitINR, "52.3456", "52.3456"}, {UnitINR, "7.1000", "7.10"}, {UnitQty, "20.5000", "20.5"},
		{UnitDays, "60.0000", "60"}, {UnitPct, "10", "10.00"},
	} {
		if got := Canonical(c.u, decimal.RequireFromString(c.in)); got != c.want {
			t.Errorf("Canonical(%s, %s) = %s, want %s", c.u, c.in, got, c.want)
		}
	}
}
