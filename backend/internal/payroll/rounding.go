package payroll

import "github.com/shopspring/decimal"

// This file holds the FOUR rounding functions of the payroll engine and nothing
// else. There is deliberately NO package-level round(), NO moneyScale constant
// and NO default rounding — unlike internal/compliance and internal/inventory,
// which each have exactly one rounding rule and can afford a single helper.
//
// Payroll has FOUR STATUTORY ROUNDINGS WITH THREE DIFFERENT UNITS, and applying
// one of them everywhere is the single most likely wrong-number bug in this
// track. The shape of the package is what forbids it:
//
//   - every rounding is a NAMED function whose name says which statute it obeys,
//     so a reader of a call site can see which rule was chosen;
//   - TestRoundingHelpersAreTheOnlyRounders parses every non-test file in the
//     package and FAILS if any file other than this one calls a method named
//     Round, RoundUp, RoundDown, RoundBank, RoundCash, RoundCeil, RoundFloor,
//     Ceil, Floor or Truncate. Six months from now, somebody adding "just a
//     .Round(2)" in payrun.go breaks the build instead of quietly changing
//     everybody's net pay.
//
// A NOTE ON shopspring's Round, which all four rest on: it rounds HALF AWAY FROM
// ZERO. For non-negative values that is identical to the statutory "half up".
// For negative values it is NOT — see RoundNearestRupee. Statutory amounts are
// non-negative by construction, and the callers that must never see a negative
// return ErrNegativeStatutoryAmount rather than rounding one under a rule that
// does not cover it.

// RoundPaise quantizes an ordinary (NON-statutory) money value to 4 decimal
// places, matching the NUMERIC(19,4) columns the value will be stored in and the
// 4-dp decimal strings the corpora carry.
//
// Use it for gross earnings, individual allowances, employer cost lines, net pay
// — every money figure whose scale is set by the database rather than by a
// statute. Do NOT use it for tax, TDS, EPF, EPS or ESI: those have their own
// statutory roundings below, and quantizing them to paise first and rounding
// again afterwards is a double rounding that moves the answer.
//
// Pinned by TestRoundPaise_MatchesNumeric194.
func RoundPaise(d decimal.Decimal) decimal.Decimal {
	return d.Round(4)
}

// RoundNearestRupee rounds to the nearest whole rupee.
//
// It implements s.288B of the Income-tax Act 1961 / s.516 of the Income-tax Act
// 2025 — "the amount of tax ... shall be rounded off to the nearest rupee" — and
// it is ALSO the rounding EPFO expects for EPF and EPS, which are reported per
// employee per component as whole-rupee integers in the ECR file.
//
// Applies to: income tax, TDS on salary, interest, EPF employee share, EPF
// employer share, EPS. Each component is rounded SEPARATELY, per employee, per
// month — never once on a total, because the ECR carries the components and the
// portal re-adds them.
//
// SIGN BEHAVIOUR, and this is the caveat that matters: shopspring's Round is
// HALF AWAY FROM ZERO, so -100.50 rounds to -101, whereas the statutory half-up
// rule would give -100. The two rules coincide only for non-negative values. No
// statutory amount here is ever negative, and rather than rely on that, the
// callers that must not see a negative return ErrNegativeStatutoryAmount. Do not
// "fix" this by switching to a half-up implementation: that would change every
// non-negative answer by nothing and give a false impression that negatives are
// supported.
//
// Pinned by TestRoundNearestRupee_StatutoryHalfUpAndSignBehaviour.
func RoundNearestRupee(d decimal.Decimal) decimal.Decimal {
	return d.Round(0)
}

// RoundUpRupee rounds UP to the next higher whole rupee (a ceiling).
//
// ESI ONLY. Rule 51 of the Employees' State Insurance (Central) Rules, 1950
// prescribes each contribution as "a sum (rounded to the next higher rupee)
// equal to [X] per cent. of the wages" — the rounding is written into the rate
// rule itself, not into a general rounding section.
//
// JUDGEMENT MADE, and it is a real fork in the road: the rule's own drafting
// applies the parenthesis to BOTH the employee's and the employer's
// contribution, so this engine rounds BOTH shares up. Several commercial payroll
// products round only the employee share and leave the employer share at two
// decimals. The rule text is the higher authority; the corpus fixture
// esi-rounding-both-shares-up cites it and asserts it. If a future reading of
// the primary text says otherwise, change it HERE and in the fixture, and expect
// the employer share of every ESI return ever filed from this software to move.
//
// This is the ONLY ceiling rounding anywhere in the codebase. Anything else
// reaching for it is almost certainly reaching for the wrong rule.
//
// Pinned by TestRoundUpRupee_BothESISharesCeil.
func RoundUpRupee(d decimal.Decimal) decimal.Decimal {
	return d.Ceil()
}

// RoundNearestTen rounds to the nearest multiple of ten rupees.
//
// s.288A of the Income-tax Act 1961 / s.516 of the Income-tax Act 2025: the
// TOTAL INCOME is rounded off to the nearest multiple of ten rupees, and if the
// last figure is five or more it goes UP to the next higher multiple of ten.
//
// TOTAL INCOME ONLY. There are TWO income-tax roundings with DIFFERENT UNITS and
// they apply at different points in the same computation — the brief's "income
// tax rounds to the nearest rupee" is only half the rule. The order of
// operations is fixed and pinned by a corpus fixture, not merely by a unit test:
//
//	RoundNearestTen(total income)
//	  -> slabs -> rebate -> rebate marginal relief
//	  -> surcharge -> surcharge marginal relief -> cess
//	  -> RoundNearestRupee(annual tax)
//	  -> divide over the months remaining
//	  -> RoundNearestRupee(this month's TDS)
//
// Rounding the income to the rupee instead of to ten changes the slab arithmetic
// for anyone whose income does not already end in a zero, and near the s.156
// (ex-87A) rebate cliff at 12,00,000 it changes whether the rebate applies at
// all. The corpus fixture it-income-rounds-to-nearest-ten carries a
// counterfactual that re-derives under the nearest-rupee rule and FAILS if the
// two agree.
//
// Implemented with Shift rather than a division: shifting the decimal point is
// EXACT, whereas dividing by ten would consult decimal.DivisionPrecision, which
// is a MUTABLE PACKAGE GLOBAL that any other package's init() could change
// (internal/inventory's rateScale comment explains why that matters).
//
// Half away from zero, as above: 12,34,565 -> 12,34,570.
//
// Pinned by TestRoundNearestTen_StatutoryExamples.
func RoundNearestTen(d decimal.Decimal) decimal.Decimal {
	return d.Shift(-1).Round(0).Shift(1)
}
