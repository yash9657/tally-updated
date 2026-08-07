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

// discountedSnapshot is the case NONE of the nine tests above ever posted: a
// line with a real discount on it. 10 units at 500.00 with 500.00 off is 4500.00
// taxable and 18% intra-State is 405.00 + 405.00.
func discountedSnapshot() VoucherSnapshot {
	s := conformantSnapshot()
	s.Lines[0] = SnapshotLine{
		Desc: "Widget", HSN: "84713010", Qty: "10", Unit: "NOS",
		UnitPrice: "500.00", Discount: "500.00", Taxable: "4500.00",
		GstRatePct: "18", CGST: "405.00", SGST: "405.00", IGST: "0.00",
		LineTotal: "5310.00",
	}
	s.Taxable, s.CGST, s.SGST, s.IGST = "4500.00", "405.00", "405.00", "0.00"
	s.InvoiceTotal = "5310.00"
	return s
}

// TestValidateCatchesALineThatDoesNotFootToItsOwnPrice is the arithmetic check
// this validator never had, driven by the exact payload that shipped.
//
// ===========================================================================
// ⛔⛔ WHY THIS INPUT AND NOT A REPRESENTATIVE ONE
// ===========================================================================
// The old builder set TotAmt = AssAmt = Taxable and emitted no Discount at all.
// On every line WITHOUT a discount that is correct — gross and net are the same
// number — so a validator gap and a builder gap were invisible together. A
// DISCOUNTED line is the one input where the two differ, and the difference is
// ₹500 on a document that had exactly two decimal places everywhere and passed
// every check this file previously ran.
func TestValidateCatchesALineThatDoesNotFootToItsOwnPrice(t *testing.T) {
	// ---- CONTROL FIRST: the discounted invoice, built correctly, is conformant.
	// Without it the violation below is consistent with the validator refusing
	// every discount, and the refusal would be for the wrong reason.
	if v := ValidateEInvoice(BuildEInvoice(discountedSnapshot())); len(v) != 0 {
		t.Fatalf("a correctly-built discounted invoice must be conformant, got: %v", v)
	}

	// ---- THE SHIPPED PAYLOAD: the same line with NO discount carried, which is
	// byte-for-byte what the assembler used to send.
	shipped := discountedSnapshot()
	shipped.Lines[0].Discount = ""
	p := BuildEInvoice(shipped)
	if p.ItemList[0].TotAmt != "4500.00" {
		t.Fatalf("setup: the shipped shape declares TotAmt %q, want 4500.00 — this test is not "+
			"reproducing the defect", p.ItemList[0].TotAmt)
	}
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "ItemList[0].TotAmt: must equal Qty x UnitPrice") {
		t.Fatalf("a line declaring a gross of 4500.00 for 10 units priced at 500.00 was accepted. "+
			"NIC requires TotAmt = Qty x UnitPrice and the IRP refuses the document; got: %v", vs)
	}
}

// TestValidateCatchesAnAssessableValueThatIgnoresTheDiscount pins the other half
// of the identity: AssAmt = TotAmt - Discount.
func TestValidateCatchesAnAssessableValueThatIgnoresTheDiscount(t *testing.T) {
	p := BuildEInvoice(discountedSnapshot())
	p.ItemList[0].AssAmt = "5000.00" // the gross, as if the discount were never taken
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "ItemList[0].AssAmt: must equal TotAmt - Discount") {
		t.Fatalf("an AssAmt that ignores its own Discount was accepted: %v", vs)
	}
}

// TestValidateRefusesANegativeDiscount pins the shape a rounding residue takes
// when an assembler quotes a rate to fewer places than it derived the taxable
// value from. NIC has no surcharge field.
func TestValidateRefusesANegativeDiscount(t *testing.T) {
	p := BuildEInvoice(discountedSnapshot())
	p.ItemList[0].Discount = "-500.00"
	p.ItemList[0].AssAmt = "5500.00" // keep the subtraction consistent
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "ItemList[0].Discount: must not be negative") {
		t.Fatalf("a negative discount was accepted: %v", vs)
	}
}

// TestValidateCatchesABlankUnit pins the field that shipped empty on every line
// cored ever built. NIC's Unit is mandatory for goods.
func TestValidateCatchesABlankUnit(t *testing.T) {
	p := BuildEInvoice(conformantSnapshot())
	p.ItemList[0].Unit = ""
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "ItemList[0].Unit: missing") {
		t.Fatalf("a blank unit of measure was accepted, so a document with no unit on its goods "+
			"would be minted an IRN and frozen: %v", vs)
	}
}

// TestValidateCatchesAnItemListThatDoesNotSumToTheInvoice pins the rule the REAL
// IRP applies and this validator did not: sum of AssAmt must equal AssVal.
func TestValidateCatchesAnItemListThatDoesNotSumToTheInvoice(t *testing.T) {
	p := BuildEInvoice(conformantSnapshot())
	// One paisa. That is the whole point: it is the amount nobody notices, and
	// the footing check (TotInvVal == AssVal + taxes) still passes because every
	// figure in it is an aggregate.
	p.ItemList[0].AssAmt = "1000.01"
	p.ItemList[0].TotAmt = "1000.01"
	p.ItemList[0].TotItemVal = "1180.01"
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "ValDtls.AssVal: the item list sums to") {
		t.Fatalf("an item list that does not add up to its own invoice was accepted: %v", vs)
	}
	// CONTROL: the invoice-level footing is UNTOUCHED and still passes, which is
	// why this needed its own check.
	if hasViolation(vs, "footing mismatch") {
		t.Fatalf("the footing check fired too, so this test is not isolating the item-list sum: %v", vs)
	}
}

// TestValidateCatchesADocTypeTheSchemaDoesNotRecord reads the doc_types list the
// recorded schema has always carried and nothing ever consulted. NIC keys the
// IRN on (SupplierGSTIN, DocTyp, DocNo, FY), so the type is part of the
// document's identity at the IRP rather than a label.
func TestValidateCatchesADocTypeTheSchemaDoesNotRecord(t *testing.T) {
	p := BuildEInvoice(conformantSnapshot())
	p.DocDtls.Typ = "PUR"
	vs := ValidateEInvoice(p)
	if !hasViolation(vs, "DocDtls.Typ") {
		t.Fatalf("an unrecorded document type was accepted: %v", vs)
	}
	// CONTROL: the two types this build does NOT emit are still recorded as
	// valid, so the check is reading the schema rather than hard-coding "INV".
	for _, typ := range []string{"CRN", "DBN"} {
		p.DocDtls.Typ = typ
		if vs := ValidateEInvoice(p); hasViolation(vs, "DocDtls.Typ") {
			t.Errorf("%s is in the recorded doc types and must not be refused: %v", typ, vs)
		}
	}
}

// TestBuildKeepsARateAtThePrecisionTheBooksHold is the paisa case one level
// down. The books hold NUMERIC(19,4); rounding the RATE to two places makes
// Qty x UnitPrice disagree with the taxable value derived from the un-rounded
// rate, and the only place to put the difference is a discount that was never
// given.
func TestBuildKeepsARateAtThePrecisionTheBooksHold(t *testing.T) {
	s := conformantSnapshot()
	// 3 units at 10.0050 is 30.0150, which rounds to 30.02.
	s.Lines[0] = SnapshotLine{
		Desc: "Rod", HSN: "7213", Qty: "3", Unit: "NOS",
		UnitPrice: "10.005", Discount: "0.00", Taxable: "30.02",
		GstRatePct: "18", CGST: "2.70", SGST: "2.70", IGST: "0.00",
		LineTotal: "35.42",
	}
	s.Taxable, s.CGST, s.SGST, s.IGST = "30.02", "2.70", "2.70", "0.00"
	s.InvoiceTotal = "35.42"

	p := BuildEInvoice(s)
	if p.ItemList[0].UnitPrice != "10.005" {
		t.Fatalf("UnitPrice = %q, want 10.005 — a rate rounded to the paisa here makes "+
			"Qty x UnitPrice disagree with the taxable value the books derived", p.ItemList[0].UnitPrice)
	}
	if v := ValidateEInvoice(p); len(v) != 0 {
		t.Fatalf("a four-decimal-book invoice must be conformant, got: %v", v)
	}

	// ---- AND A FOURTH PLACE IS REFUSED RATHER THAN EMITTED. NIC models
	// UnitPrice as decimal(12,3); the books can hold one more place than that,
	// and the IRP would reject the document rather than round it.
	p.ItemList[0].UnitPrice = "10.0055"
	if vs := ValidateEInvoice(p); !hasViolation(vs, "UnitPrice: at most 3 decimal places") {
		t.Fatalf("a four-decimal unit price was accepted; NIC's field is decimal(12,3): %v", vs)
	}
}
