package compliance

import (
	"testing"

	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// TestContractorAggregateCatchUp is THE test for §3.1 gap 1, and it walks the
// scenario the handoff names: twenty ₹20,000 contractor bills.
//
// Each is below the ₹30,000 per-payment limit, so the old engine deducted
// NOTHING on any of them. s.393(1) Sl. No. 6(i)(b) sets a ₹1,00,000 aggregate,
// and s.393(1)(a) charges tax "on the ENTIRE amount ... where the amount OR
// AGGREGATE OF AMOUNTS exceeds the threshold limit".
//
// So the sixth bill — the one that takes the running total to ₹1,20,000 — must
// deduct on the WHOLE ₹1,20,000, not on its own ₹20,000 and not on the ₹20,000
// excess. At 2% that is ₹2,400 on a ₹20,000 bill.
func TestContractorAggregateCatchUp(t *testing.T) {
	rs, err := LoadTDSRuleSet("tds-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	var (
		priorBase = decimal.Zero
		priorTDS  = decimal.Zero
		bill      = dec("20000")
		totalTDS  = decimal.Zero
	)
	for i := 1; i <= 20; i++ {
		res, err := TDSDeriveCumulative(
			TDSInput{
				Section: "194C", DeducteeType: DeducteeOther,
				PANAvailable: true, TaxableAmount: bill,
			},
			AggregateInput{PriorBaseRupees: priorBase, PriorTDSRupees: priorTDS},
			rs,
		)
		if err != nil {
			t.Fatalf("bill %d: %v", i, err)
		}

		switch {
		case i <= 5:
			// Cumulative reaches ₹1,00,000 exactly at bill 5. "EXCEEDS" is
			// strict, so bill 5 still deducts nothing.
			if res.Applied {
				t.Errorf("bill %d (cumulative %s) deducted %s; the aggregate limit is ₹1,00,000 "+
					"and is not exceeded until bill 6", i, res.AggregateBaseRupees, res.TDS)
			}
		case i == 6:
			// THE CROSSING. 6 x 20,000 = 1,20,000; 2% of the WHOLE = 2,400.
			if !res.AggregateCrossed {
				t.Errorf("bill 6 took the total to ₹1,20,000 and AggregateCrossed is false")
			}
			if !res.TDS.Equal(dec("2400")) {
				t.Errorf("bill 6 deducted %s, want 2400. s.393(1)(a) charges tax on the ENTIRE "+
					"amount: 2%% of the whole ₹1,20,000, NOT 2%% of this bill's ₹20,000 (400) "+
					"and NOT 2%% of the ₹20,000 excess over the limit (400)", res.TDS)
			}
			if !res.CatchUpBaseRupees.Equal(dec("100000")) {
				t.Errorf("bill 6 reached back over %s of earlier base, want 100000",
					res.CatchUpBaseRupees)
			}
		default:
			// Past the crossing every further bill is taxed on its own, because
			// the earlier base has been settled by the credit for prior TDS.
			if !res.TDS.Equal(dec("400")) {
				t.Errorf("bill %d deducted %s, want 400 (2%% of its own ₹20,000). A larger "+
					"figure means the catch-up is being charged again on every later bill",
					i, res.TDS)
			}
		}

		priorBase = priorBase.Add(bill)
		priorTDS = priorTDS.Add(res.TDS)
		totalTDS = totalTDS.Add(res.TDS)
	}

	// THE INVARIANT THAT TIES IT TOGETHER: after twenty bills the total deducted
	// must equal 2% of the whole ₹4,00,000. Per-bill assertions can each pass
	// while the running total drifts; this cannot.
	if !totalTDS.Equal(dec("8000")) {
		t.Errorf("total deducted over twenty ₹20,000 bills = %s, want 8000 (2%% of ₹4,00,000). "+
			"The old engine deducted ZERO on all twenty", totalTDS)
	}
}

// TestAggregateExactlyAtTheLimitAttractsNothing pins the strict comparison on
// the aggregate limb, matching the per-payment one.
func TestAggregateExactlyAtTheLimitAttractsNothing(t *testing.T) {
	rs, _ := LoadTDSRuleSet("tds-2026-04")
	res, err := TDSDeriveCumulative(
		TDSInput{Section: "194C", DeducteeType: DeducteeOther, PANAvailable: true,
			TaxableAmount: dec("20000")},
		AggregateInput{PriorBaseRupees: dec("80000"), PriorTDSRupees: decimal.Zero},
		rs,
	)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if res.Applied || res.AggregateCrossed {
		t.Errorf("a cumulative total of EXACTLY ₹1,00,000 deducted %s; col. D says the limit is "+
			"₹1,00,000 'in case of aggregate of such sums' and s.393(1)(a) says 'exceeds', so "+
			"the boundary attracts nothing", res.TDS)
	}
	// A single paisa more crosses it.
	over, err := TDSDeriveCumulative(
		TDSInput{Section: "194C", DeducteeType: DeducteeOther, PANAvailable: true,
			TaxableAmount: dec("20000.0001")},
		AggregateInput{PriorBaseRupees: dec("80000"), PriorTDSRupees: decimal.Zero},
		rs,
	)
	if err != nil {
		t.Fatalf("derive over: %v", err)
	}
	if !over.AggregateCrossed {
		t.Error("a cumulative total one paisa over ₹1,00,000 did not cross the aggregate limit")
	}
}

// TestPerPaymentLimbStillGovernsOnItsOwn pins that a single large bill is taxed
// on itself and does not acquire a spurious catch-up.
func TestPerPaymentLimbStillGovernsOnItsOwn(t *testing.T) {
	rs, _ := LoadTDSRuleSet("tds-2026-04")
	res, err := TDSDeriveCumulative(
		TDSInput{Section: "194C", DeducteeType: DeducteeOther, PANAvailable: true,
			TaxableAmount: dec("40000")},
		AggregateInput{PriorBaseRupees: decimal.Zero, PriorTDSRupees: decimal.Zero},
		rs,
	)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if !res.Applied || !res.TDS.Equal(dec("800")) {
		t.Errorf("a single ₹40,000 bill deducted %s, want 800 (2%% of its own base). Limb (a)'s "+
			"₹30,000 fires by itself", res.TDS)
	}
	if res.AggregateCrossed {
		t.Error("a first bill that fires on the per-payment limb must not report an aggregate " +
			"crossing; there is no earlier base to catch up")
	}
}

// TestAggregationAppliesToTheAnnualSectionsToo is point 2 of aggregate.go's
// header, and the part the §3.1 brief does not record.
//
// The s.393(1)(a) chapeau applies "the amount or aggregate of amounts" to EVERY
// row, so 194J-P's ₹50,000 is an aggregate limit and not a per-bill one. Six
// ₹10,000 professional-fee bills used to attract nothing.
func TestAggregationAppliesToTheAnnualSectionsToo(t *testing.T) {
	rs, _ := LoadTDSRuleSet("tds-2026-04")

	// Five ₹10,000 bills: cumulative ₹50,000, exactly at the limit, nothing due.
	at, err := TDSDeriveCumulative(
		TDSInput{Section: "194J-P", DeducteeType: DeducteeOther, PANAvailable: true,
			TaxableAmount: dec("10000")},
		AggregateInput{PriorBaseRupees: dec("40000"), PriorTDSRupees: decimal.Zero},
		rs,
	)
	if err != nil {
		t.Fatalf("derive at limit: %v", err)
	}
	if at.Applied {
		t.Errorf("professional fees cumulating to exactly ₹50,000 deducted %s, want nothing", at.TDS)
	}

	// The sixth crosses: 10% of the whole ₹60,000 = ₹6,000, on a ₹10,000 bill.
	over, err := TDSDeriveCumulative(
		TDSInput{Section: "194J-P", DeducteeType: DeducteeOther, PANAvailable: true,
			TaxableAmount: dec("10000")},
		AggregateInput{PriorBaseRupees: dec("50000"), PriorTDSRupees: decimal.Zero},
		rs,
	)
	if err != nil {
		t.Fatalf("derive over: %v", err)
	}
	if !over.TDS.Equal(dec("6000")) {
		t.Errorf("professional fees crossing ₹50,000 deducted %s, want 6000 (10%% of the entire "+
			"₹60,000). Fixing only the contractor row leaves 194J-P, 194J-T and 194H with the "+
			"identical defect", over.TDS)
	}
}

// TestMonthlyBasisIsNotCumulatedOverTheYear pins the deliberate exclusion.
//
// 194I's limit is "₹50,000 for a month or part of a month". Feeding it a
// tax-year total would deduct on a landlord paid ₹10,000 a month, whose monthly
// figure never approaches the limit. Over-deducting is not the safe direction —
// it withholds money that was never due.
func TestMonthlyBasisIsNotCumulatedOverTheYear(t *testing.T) {
	rs, _ := LoadTDSRuleSet("tds-2026-04")
	res, err := TDSDeriveCumulative(
		TDSInput{Section: "194I-B", DeducteeType: DeducteeOther, PANAvailable: true,
			TaxableAmount: dec("10000")},
		// Eleven months of ₹10,000 already paid.
		AggregateInput{PriorBaseRupees: dec("110000"), PriorTDSRupees: decimal.Zero},
		rs,
	)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if res.Applied || res.AggregateCrossed {
		t.Errorf("monthly rent of ₹10,000 deducted %s once the YEAR's total passed ₹50,000. "+
			"s.393(1) Sl. No. 2(ii) col. D reads '₹50,000 for a month or part of a month', so "+
			"the year total is the wrong measure and this over-deducts", res.TDS)
	}
}

// TestNilThresholdSectionsDoNotCumulate: 194J-D has no threshold, so every
// payment is already taxed and no aggregate question arises.
func TestNilThresholdSectionsDoNotCumulate(t *testing.T) {
	rs, _ := LoadTDSRuleSet("tds-2026-04")
	res, err := TDSDeriveCumulative(
		TDSInput{Section: "194J-D", DeducteeType: DeducteeOther, PANAvailable: true,
			TaxableAmount: dec("5000")},
		AggregateInput{PriorBaseRupees: dec("95000"), PriorTDSRupees: dec("9500")},
		rs,
	)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if !res.TDS.Equal(dec("500")) {
		t.Errorf("a ₹5,000 director's fee deducted %s, want 500 (10%% of itself). A Nil-threshold "+
			"row is taxed payment by payment and must not acquire a catch-up", res.TDS)
	}
	if res.AggregateCrossed {
		t.Error("a Nil-threshold section reported an aggregate crossing; there is no threshold " +
			"to cross")
	}
}

// TestCatchUpNeverGoesNegative pins the clamp.
//
// A negative deduction would post as a DEBIT on the TDS ledger, which
// ledger.deriveAndVerifyTDS refuses outright — so an unclamped catch-up would
// turn an over-deduction into a failed post rather than a visible correction.
func TestCatchUpNeverGoesNegative(t *testing.T) {
	rs, _ := LoadTDSRuleSet("tds-2026-04")
	res, err := TDSDeriveCumulative(
		TDSInput{Section: "194C", DeducteeType: DeducteeOther, PANAvailable: true,
			TaxableAmount: dec("20000")},
		// More already deducted than the whole aggregate attracts.
		AggregateInput{PriorBaseRupees: dec("100000"), PriorTDSRupees: dec("9999")},
		rs,
	)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if res.TDS.IsNegative() {
		t.Errorf("catch-up came out negative (%s). This function withholds tax, it does not "+
			"refund it, and a negative posts as a debit that the ledger refuses", res.TDS)
	}
	if res.Applied {
		t.Errorf("nothing further is due, so Applied must be false; got TDS %s", res.TDS)
	}
}

// TestCumulativeRejectsNegativeState guards the caller's SUM.
func TestCumulativeRejectsNegativeState(t *testing.T) {
	rs, _ := LoadTDSRuleSet("tds-2026-04")
	_, err := TDSDeriveCumulative(
		TDSInput{Section: "194C", DeducteeType: DeducteeOther, PANAvailable: true,
			TaxableAmount: dec("20000")},
		AggregateInput{PriorBaseRupees: dec("-1"), PriorTDSRupees: decimal.Zero},
		rs,
	)
	if err == nil {
		t.Error("a negative prior base was accepted; it can only come from a broken aggregate " +
			"query and must not silently produce a deduction")
	}
}
