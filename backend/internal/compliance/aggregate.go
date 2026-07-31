// CUMULATION — the s.393(1) aggregate threshold and the catch-up deduction.
//
// §3.1 gap 1. Design memo: docs/design/t6-gaps-model.md gap 1. Migration 0050
// stores the facts this reads.
//
// ---------------------------------------------------------------------------
// WHAT THE STATUTE ACTUALLY SAYS, AND WHY IT IS WIDER THAN THE GAP LIST
// ---------------------------------------------------------------------------
// s.393(1)(a) charges tax
//
//	"on the ENTIRE amount of such income or sum, where the amount OR AGGREGATE
//	 OF AMOUNTS exceeds the threshold limit specified in column D"
//
// Three things follow, and only the first is in the §3.1 brief:
//
//  1. Sl. No. 6(i) (contractors) states TWO limits — "(a) ₹30,000; for any such
//     sum; AND (b) ₹1,00,000 in case of aggregate of such sums". Twenty ₹20,000
//     bills attracted nothing.
//
//  2. AGGREGATION IS NOT CONTRACTOR-SPECIFIC. The chapeau governs every row of
//     the Table. 194J-P, 194J-T and 194H carry BasisAnnual and were tested per
//     transaction, which is the identical defect. Sl. No. 6(i) is merely the
//     only row with a second, separate limit.
//
//  3. "ON THE ENTIRE AMOUNT." When the aggregate crosses, tax is due on the
//     WHOLE cumulative sum — not on the excess, and not only prospectively. The
//     payment that crosses must CATCH UP everything under-deducted before it.
//     This is the part an implementation is most likely to get half-right: it
//     starts deducting at the right moment and leaves the earlier bills
//     untaxed.
//
// ---------------------------------------------------------------------------
// MONTHLY BASIS IS DELIBERATELY NOT CUMULATED HERE
// ---------------------------------------------------------------------------
// 194I-A/B are BasisMonthly — "₹50,000 for a month or part of a month". Their
// aggregate is per calendar month, not per tax year, so feeding them a tax-year
// total would OVER-deduct. AggregateApplies reports false for them and they keep
// the existing per-transaction behaviour, which under-deducts on a split within
// one month. That is a NARROWER and disclosed gap, not a silent one.
package compliance

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// AggregateInput is the cumulative state a deduction is derived against.
//
// PriorBaseRupees is supplied by the caller — a SUM over migration 0050's
// tds_deduction rows for the same (deductee, section, tax year) with an earlier
// or equal date. It is passed in rather than read here so this stays a pure
// function beside TDSDerive.
type AggregateInput struct {
	// PriorBaseRupees is the total already paid or credited to this deductee
	// under this section in this tax year, BEFORE the current payment.
	//
	// It sums amounts PAID, not amounts deducted upon — sub-threshold payments
	// are exactly what carries the aggregate past the limit.
	PriorBaseRupees decimal.Decimal
	// PriorTDSRupees is the tax already deducted on that prior base. It is what
	// makes the catch-up exact rather than double-charging: the crossing payment
	// owes tax on the entire aggregate LESS what has already been taken.
	PriorTDSRupees decimal.Decimal
}

// AggregateThresholds returns the two limits for a section: the per-payment
// limit and the aggregate limit.
//
// The aggregate limit for a row stating only ONE threshold is that same
// threshold — per the chapeau, a single stated limit is tested against "the
// amount or aggregate of amounts". Sl. No. 6(i) is the only row where the two
// differ.
func AggregateThresholds(section string, rule sectionRule) (perTxn, aggregate decimal.Decimal) {
	if section == "194C" {
		// s.393(1) [Table: Sl. No. 6(i)] col. D, verbatim: "[for (a) and (b)]
		// (a) ₹30,000; for any such sum; and (b) ₹1,00,000 in case of aggregate
		// of such sums."
		return rule.PerTxnThreshold, contractorAggregateLimitRupees
	}
	return rule.PerTxnThreshold, rule.PerTxnThreshold
}

// contractorAggregateLimitRupees is limb (b) of Sl. No. 6(i).
var contractorAggregateLimitRupees = decimal.RequireFromString("100000")

// AggregateApplies reports whether a section cumulates over the TAX YEAR.
//
// False for BasisMonthly (194I): its limit is per month, so a tax-year total
// would over-deduct. False for BasisNil (194J-D): there is no threshold, so
// every payment is deducted upon and no aggregate arises.
func AggregateApplies(rule sectionRule) bool {
	switch rule.Basis {
	case BasisPerTxn, BasisAnnual:
		return true
	default:
		return false
	}
}

// TDSDeriveCumulative derives a deduction taking the running aggregate into
// account. It is pure; the caller supplies the prior state.
//
// The rules, in order:
//
//  1. Derive the per-payment result exactly as TDSDerive does. If the single
//     payment exceeds its own limit, that governs and no catch-up arises — the
//     earlier bills were already below the aggregate limit or already taxed.
//
//  2. Otherwise test the AGGREGATE: prior + this payment against the aggregate
//     limit.
//
//  3. If the aggregate crosses, tax is due "on the ENTIRE amount" — the whole
//     cumulative base — less whatever was already deducted. That difference is
//     the catch-up, and it is charged on this payment.
//
// The result's TDS can therefore EXCEED this payment's own base × rate, which is
// correct and is the whole point: the sixth ₹20,000 bill carries the tax for all
// six.
func TDSDeriveCumulative(in TDSInput, agg AggregateInput, rs TDSRuleSet) (TDSResult, error) {
	res, err := TDSDerive(in, rs)
	if err != nil {
		return TDSResult{}, err
	}
	rule, ok := rs.Sections[in.Section]
	if !ok {
		// Unreachable: TDSDerive already returned ErrUnknownSection.
		return TDSResult{}, fmt.Errorf("%w: %q", ErrUnknownSection, in.Section)
	}
	if !AggregateApplies(rule) {
		return res, nil
	}
	if agg.PriorBaseRupees.IsNegative() || agg.PriorTDSRupees.IsNegative() {
		return TDSResult{}, fmt.Errorf(
			"compliance: aggregate state must be non-negative (base %s, tds %s)",
			agg.PriorBaseRupees, agg.PriorTDSRupees)
	}

	// The per-payment limb already fired: this payment is taxed on its own, and
	// anything earlier was either below the aggregate limit or already taxed.
	if res.Applied {
		return res, nil
	}

	_, aggregateLimit := AggregateThresholds(in.Section, rule)
	cumulative := agg.PriorBaseRupees.Add(in.TaxableAmount)
	res.AggregateBaseRupees = cumulative
	res.AggregateThresholdRupees = aggregateLimit

	// "EXCEEDS the threshold limit" — the same strict comparison as everywhere
	// else in this package. A cumulative total exactly at the limit attracts
	// nothing.
	if !cumulative.GreaterThan(aggregateLimit) {
		return res, nil
	}

	// s.393(1)(a): "on the ENTIRE amount". Tax the whole cumulative base, then
	// credit what has already been taken. The remainder falls on this payment.
	dueOnWhole := round(cumulative.Mul(res.RatePct).Div(hundred))
	catchUp := dueOnWhole.Sub(agg.PriorTDSRupees)
	if catchUp.IsNegative() {
		// More has already been deducted than the whole aggregate attracts. Do
		// NOT deduct a negative: this function withholds tax, it does not refund
		// it, and a negative would post as a debit on the TDS ledger — which
		// ledger.deriveAndVerifyTDS refuses by design.
		catchUp = decimal.Zero
	}

	res.Applied = catchUp.IsPositive()
	res.TDS = catchUp
	res.AggregateCrossed = true
	// The EARLIER base this payment reaches back to tax — the cumulative total
	// less this payment's own share, which is exactly the prior base.
	//
	// Defined this way and not as "the previously untaxed portion" because that
	// would need base = tax ÷ rate × 100, and a division there reintroduces
	// rounding into a figure the return prints. Where some tax has already been
	// taken, the credit is applied through PriorTDSRupees above, not by
	// shrinking this number.
	res.CatchUpBaseRupees = agg.PriorBaseRupees
	return res, nil
}
