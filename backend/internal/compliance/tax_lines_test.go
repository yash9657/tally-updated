package compliance_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// TestTaxLinesSumToAggregate pins the invariant the persistence layer relies on:
// the per-line breakdown is 1:1 with the input lines and sums EXACTLY to the
// aggregate. If these ever diverge, a GSTR-1 HSN summary built from lines would
// silently disagree with the b2b/b2cs sections built from the aggregate.
func TestTaxLinesSumToAggregate(t *testing.T) {
	for _, tc := range []struct {
		name           string
		supplier, pos  string
		lines          []compliance.Line
	}{
		{"intra multi-rate", "29", "29", []compliance.Line{
			{TaxableValue: d("50000"), GSTRatePct: d("18"), HSN: "7326"},
			{TaxableValue: d("26500"), GSTRatePct: d("12"), HSN: "7323"},
			{TaxableValue: d("1000"), GSTRatePct: d("0"), HSN: "9999"},
		}},
		{"inter multi-rate", "29", "27", []compliance.Line{
			{TaxableValue: d("80000"), GSTRatePct: d("18"), HSN: "7326"},
			{TaxableValue: d("333.33"), GSTRatePct: d("5"), HSN: "7208"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs, err := compliance.LoadRuleSet("gst-2024-09")
			if err != nil {
				t.Fatalf("ruleset: %v", err)
			}
			res, err := compliance.TaxDerive(compliance.TaxInput{
				SupplierStateCode:      tc.supplier,
				PlaceOfSupplyStateCode: tc.pos,
				Lines:                  tc.lines,
			}, rs)
			if err != nil {
				t.Fatalf("derive: %v", err)
			}
			if len(res.Lines) != len(tc.lines) {
				t.Fatalf("Lines len = %d, want %d (must be 1:1 with input)", len(res.Lines), len(tc.lines))
			}
			c, s, i := decimal.Zero, decimal.Zero, decimal.Zero
			for n, lt := range res.Lines {
				if lt.HSN != tc.lines[n].HSN {
					t.Errorf("line %d HSN = %q, want %q", n, lt.HSN, tc.lines[n].HSN)
				}
				if !lt.TaxableValue.Equal(tc.lines[n].TaxableValue) {
					t.Errorf("line %d taxable = %s, want %s", n, lt.TaxableValue, tc.lines[n].TaxableValue)
				}
				c, s, i = c.Add(lt.CGST), s.Add(lt.SGST), i.Add(lt.IGST)
			}
			if !c.Equal(res.CGST) || !s.Equal(res.SGST) || !i.Equal(res.IGST) {
				t.Fatalf("line sums (c=%s s=%s i=%s) != aggregate (c=%s s=%s i=%s)",
					c, s, i, res.CGST, res.SGST, res.IGST)
			}
		})
	}
}
