// Package compliance implements the embedded, in-process tax-derivation engine.
//
// TaxDerive is a PURE function: given an invoice's lines and the supplier /
// place-of-supply state codes, plus a versioned RuleSet, it deterministically
// derives the CGST/SGST/IGST split, total tax, and invoice total. It performs
// no I/O and is safe to call inside the atomic voucher-post transaction.
//
// The algorithm reproduces the golden corpora in corpora/tax exactly:
//
//   - Intra-state (supplier state == place-of-supply state): each line's tax is
//     split CGST = SGST = round_half_up(taxable * (rate/2) / 100, 4dp). The two
//     halves are rounded INDEPENDENTLY per line then summed (so 30.00015 -> two
//     30.0002 components, totalTax 60.0004, not 60.0003).
//   - Inter-state: each line yields IGST = round_half_up(taxable * rate / 100, 4dp).
//   - Lines with a zero rate (including explicit exempt / nil-rated supplies)
//     contribute no tax.
//   - totalTax = cgst + sgst + igst.
//   - invoiceTotal = taxableTotal under reverse charge (the supplier does not
//     collect GST), otherwise taxableTotal + totalTax.
package compliance

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// moneyScale is the number of decimal places all money quantities carry. It
// matches the NUMERIC(19,4) database columns and the corpora's 4-dp strings.
const moneyScale int32 = 4

var (
	hundred = decimal.NewFromInt(100)
	two     = decimal.NewFromInt(2)
)

// ErrUnknownRuleset is returned when a RuleSet version is not registered.
var ErrUnknownRuleset = errors.New("compliance: unknown ruleset version")

// Line is a single invoice line item for tax derivation.
type Line struct {
	// TaxableValue is the pre-tax value of the line (NUMERIC(19,4)).
	TaxableValue decimal.Decimal
	// GSTRatePct is the combined GST rate for the line, e.g. 18 for 18%.
	GSTRatePct decimal.Decimal
	// HSN is the harmonized system code; carried for audit, not used in math.
	HSN string
}

// TaxInput is the complete, self-contained input to TaxDerive.
type TaxInput struct {
	// SupplierStateCode is the two-digit GST state code of the supplier.
	SupplierStateCode string
	// PlaceOfSupplyStateCode is the two-digit GST state code of the place of supply.
	PlaceOfSupplyStateCode string
	// ReverseCharge indicates the recipient pays the GST (RCM); the supplier's
	// invoice total then excludes the tax.
	ReverseCharge bool
	// Exempt marks an explicitly exempt / nil-rated supply. It is informational:
	// exempt lines carry a zero rate and so contribute no tax regardless.
	Exempt bool
	// Lines are the invoice line items; at least one is required.
	Lines []Line
}

// TaxResult is the derived tax breakdown. All fields are quantized to 4 dp.
type TaxResult struct {
	CGST         decimal.Decimal
	SGST         decimal.Decimal
	IGST         decimal.Decimal
	TotalTax     decimal.Decimal
	InvoiceTotal decimal.Decimal
}

// RuleSet captures the effective-dated GST behaviour for a version. For Phase 0
// the rate handling is uniform across versions; the struct exists so future
// versions (changed slabs, cess, special rates) plug in without changing the
// engine's call sites.
type RuleSet struct {
	// Version is the ruleset identifier, e.g. "gst-2024-09".
	Version string
}

// registry holds the known, effective-dated rulesets keyed by version.
var registry = map[string]RuleSet{
	"gst-2024-09": {Version: "gst-2024-09"},
}

// LoadRuleSet returns the registered RuleSet for the given version, or
// ErrUnknownRuleset.
func LoadRuleSet(version string) (RuleSet, error) {
	rs, ok := registry[version]
	if !ok {
		return RuleSet{}, fmt.Errorf("%w: %q", ErrUnknownRuleset, version)
	}
	return rs, nil
}

// round applies banker-free half-up rounding to the money scale, matching the
// corpora's decimal.ROUND_HALF_UP at 4 dp.
func round(d decimal.Decimal) decimal.Decimal {
	return d.Round(moneyScale)
}

// TaxDerive derives the GST breakdown for input under the supplied ruleset. It
// is pure and deterministic. It returns an error only for structurally invalid
// input (no lines); it never panics on bad numeric data.
func TaxDerive(input TaxInput, rs RuleSet) (TaxResult, error) {
	if rs.Version == "" {
		return TaxResult{}, fmt.Errorf("%w: empty ruleset", ErrUnknownRuleset)
	}
	if len(input.Lines) == 0 {
		return TaxResult{}, errors.New("compliance: at least one line is required")
	}

	intra := input.SupplierStateCode == input.PlaceOfSupplyStateCode

	cgst := decimal.Zero
	sgst := decimal.Zero
	igst := decimal.Zero
	taxableTotal := decimal.Zero

	for _, line := range input.Lines {
		taxableTotal = taxableTotal.Add(line.TaxableValue)
		if line.GSTRatePct.IsZero() {
			continue
		}
		if intra {
			// half = round_half_up(taxable * (rate/2) / 100), rounded per
			// component, per line.
			half := round(line.TaxableValue.
				Mul(line.GSTRatePct.Div(two)).
				Div(hundred))
			cgst = cgst.Add(half)
			sgst = sgst.Add(half)
		} else {
			full := round(line.TaxableValue.
				Mul(line.GSTRatePct).
				Div(hundred))
			igst = igst.Add(full)
		}
	}

	totalTax := cgst.Add(sgst).Add(igst)

	invoiceTotal := taxableTotal.Add(totalTax)
	if input.ReverseCharge {
		// Under RCM the supplier does not collect GST; the invoice total
		// charged to the recipient excludes the tax.
		invoiceTotal = taxableTotal
	}

	return TaxResult{
		CGST:         round(cgst),
		SGST:         round(sgst),
		IGST:         round(igst),
		TotalTax:     round(totalTax),
		InvoiceTotal: round(invoiceTotal),
	}, nil
}
