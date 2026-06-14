package statutory

import (
	"strings"
	"testing"
)

// conformantSnapshot returns a clean intra-state B2B snapshot with 2dp amounts.
// Seller and buyer are in state 27 (Maharashtra) so CGST+SGST apply, IGST zero.
func conformantSnapshot() VoucherSnapshot {
	return VoucherSnapshot{
		VoucherNo:       "INV-001",
		VoucherDate:     "13/06/2026",
		SellerGstin:     "27AAAAA0000A1Z5",
		SellerLegalName: "Seller Pvt Ltd",
		SellerStateCode: "27",
		BuyerGstin:      "27BBBBB0000B1Z5",
		BuyerLegalName:  "Buyer Pvt Ltd",
		BuyerStateCode:  "27",
		Lines: []SnapshotLine{
			{
				Desc:       "Widget",
				HSN:        "84713010",
				Qty:        "10",
				Unit:       "NOS",
				UnitPrice:  "100.00",
				Taxable:    "1000.00",
				GstRatePct: "18",
				CGST:       "90.00",
				SGST:       "90.00",
				IGST:       "0.00",
				LineTotal:  "1180.00",
			},
		},
		Taxable:      "1000.00",
		CGST:         "90.00",
		SGST:         "90.00",
		IGST:         "0.00",
		InvoiceTotal: "1180.00",
	}
}

// interStateSnapshot is a clean inter-state snapshot (seller 27, buyer 29) so
// IGST applies and CGST/SGST are zero.
func interStateSnapshot() VoucherSnapshot {
	s := conformantSnapshot()
	s.BuyerStateCode = "29"
	s.BuyerGstin = "29BBBBB0000B1Z5"
	s.CGST, s.SGST, s.IGST = "0.00", "0.00", "180.00"
	s.Lines[0].CGST, s.Lines[0].SGST, s.Lines[0].IGST = "0.00", "0.00", "180.00"
	return s
}

func TestValidateConformant(t *testing.T) {
	if v := ValidateEInvoice(BuildEInvoice(conformantSnapshot())); len(v) != 0 {
		t.Fatalf("expected no violations, got: %v", v)
	}
	if v := ValidateEInvoice(BuildEInvoice(interStateSnapshot())); len(v) != 0 {
		t.Fatalf("inter-state: expected no violations, got: %v", v)
	}
}

func hasViolation(vs []string, substr string) bool {
	for _, v := range vs {
		if strings.Contains(v, substr) {
			return true
		}
	}
	return false
}

func TestValidateMissingBuyerGSTIN(t *testing.T) {
	p := BuildEInvoice(conformantSnapshot())
	p.BuyerDtls.Gstin = ""
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "BuyerDtls.Gstin: missing") {
		t.Fatalf("expected missing buyer gstin violation, got: %v", vs)
	}
}

func TestValidateBadGSTINPattern(t *testing.T) {
	p := BuildEInvoice(conformantSnapshot())
	p.SellerDtls.Gstin = "27ZZZZZ000ZZ1Z5" // 15 chars but wrong pattern
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "SellerDtls.Gstin") {
		t.Fatalf("expected seller gstin pattern violation, got: %v", vs)
	}
}

func TestValidate3dpAmount(t *testing.T) {
	p := BuildEInvoice(conformantSnapshot())
	p.ValDtls.AssVal = "1000.000" // 3dp
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "ValDtls.AssVal: must have exactly 2 decimal places") {
		t.Fatalf("expected 3dp violation, got: %v", vs)
	}
}

func TestValidateFootingMismatch(t *testing.T) {
	p := BuildEInvoice(conformantSnapshot())
	p.ValDtls.TotInvVal = "9999.00"
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "footing mismatch") {
		t.Fatalf("expected footing mismatch, got: %v", vs)
	}
}

func TestValidateZeroLines(t *testing.T) {
	p := BuildEInvoice(conformantSnapshot())
	p.ItemList = nil
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "minimum 1") {
		t.Fatalf("expected zero-lines violation, got: %v", vs)
	}
}

func TestValidateTooManyLines(t *testing.T) {
	p := BuildEInvoice(conformantSnapshot())
	one := p.ItemList[0]
	items := make([]EInvoiceItem, 1001)
	for i := range items {
		items[i] = one
	}
	p.ItemList = items
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "maximum 1000") {
		t.Fatalf("expected too-many-lines violation, got: %v", vs)
	}
}

func TestValidateIntraButIGSTSet(t *testing.T) {
	p := BuildEInvoice(conformantSnapshot()) // intra-state
	p.ValDtls.IgstVal = "50.00"
	// keep footing consistent so the IGST-set check is what fires
	p.ValDtls.TotInvVal = "1230.00"
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "intra-state supply but IgstVal is non-zero") {
		t.Fatalf("expected intra-but-IGST violation, got: %v", vs)
	}
}

func TestValidateInterButCGSTSet(t *testing.T) {
	p := BuildEInvoice(interStateSnapshot())
	p.ValDtls.CgstVal = "90.00"
	p.ValDtls.SgstVal = "90.00"
	p.ValDtls.IgstVal = "0.00"
	p.ValDtls.TotInvVal = "1180.00"
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "inter-state supply but CgstVal/SgstVal is non-zero") {
		t.Fatalf("expected inter-but-CGST violation, got: %v", vs)
	}
}

// The cored 4dp strings must be rejected by the 2dp check when they leak in.
func TestValidateRejects4dp(t *testing.T) {
	p := BuildEInvoice(conformantSnapshot())
	p.ValDtls.CgstVal = "90.0000"
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "ValDtls.CgstVal: must have exactly 2 decimal places") {
		t.Fatalf("expected 4dp rejection, got: %v", vs)
	}
}

// BuildEInvoice must re-format cored's 4dp strings down to 2dp.
func TestBuildReformats4dpTo2dp(t *testing.T) {
	s := conformantSnapshot()
	s.Taxable = "1000.0000"
	s.CGST = "90.0000"
	s.SGST = "90.0000"
	s.IGST = "0.0000"
	s.InvoiceTotal = ""
	p := BuildEInvoice(s)
	if p.ValDtls.AssVal != "1000.00" {
		t.Fatalf("AssVal not reformatted: %q", p.ValDtls.AssVal)
	}
	if p.ValDtls.TotInvVal != "1180.00" {
		t.Fatalf("derived total wrong: %q", p.ValDtls.TotInvVal)
	}
	if v := ValidateEInvoice(p); len(v) != 0 {
		t.Fatalf("reformatted payload should be conformant, got: %v", v)
	}
}
