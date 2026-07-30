package printing

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// completeTaxInvoice is a MINIMAL but COMPLETE Rule 46 tax invoice: every
// mandatory particular present and nothing decorative. The completeness table
// below removes one particular from a copy of it per case, so anything added here
// must be a particular some clause requires — an extra field would make a "field
// removed" case fail for two reasons at once and the failure would stop naming a
// clause.
//
// It is intra-State (CGST + SGST, supplier and place of supply both 27), so the
// conditional clauses (e) and (n) do NOT apply to it by default and the cases
// that test them switch it deliberately.
func completeTaxInvoice(t *testing.T) Invoice {
	t.Helper()
	return Invoice{
		Kind:   KindTaxInvoice,
		Number: "INV/2026-27/0001",
		Date:   time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
		Supplier: Company{
			Name:      "Mehta Trading Co.",
			Address1:  "14 Laxmi Industrial Estate",
			City:      "Pune",
			State:     "Maharashtra",
			StateCode: "27",
			PIN:       "411001",
			GSTIN:     "27AAAPZ1234C1ZS",
		},
		Recipient: Party{
			Name:      "Kulkarni Hardware Stores",
			Address1:  "22 Shivaji Road",
			City:      "Nashik",
			State:     "Maharashtra",
			StateCode: "27",
			PIN:       "422001",
			GSTIN:     "27AACCK5678L1Z9",
		},
		PlaceOfSupply:     "Maharashtra",
		PlaceOfSupplyCode: "27",
		Items: []LineItem{
			{SerialNo: 1, Description: "Galvanised steel bracket 12mm", HSN: "7326",
				Quantity: dec(t, "4"), Unit: "KGS", Rate: dec(t, "187.50"), Amount: dec(t, "750.00")},
			{SerialNo: 2, Description: "Cotton packing cloth", HSN: "6307",
				Quantity: dec(t, "10"), Unit: "MTR", Rate: dec(t, "25.00"), Amount: dec(t, "250.00")},
		},
		Taxes: []TaxHead{
			{Name: "CGST", Rate: dec(t, "9"), Amount: dec(t, "90.00")},
			{Name: "SGST", Rate: dec(t, "9"), Amount: dec(t, "90.00")},
		},
		Subtotal: dec(t, "1000.00"),
		Total:    dec(t, "1180.00"),
	}
}

// TestACompleteTaxInvoicePassesTheRuleFortySixGate is the control for every case
// below. Without it, a validator that rejected EVERYTHING would pass the whole
// completeness table and nothing would notice.
func TestACompleteTaxInvoicePassesTheRuleFortySixGate(t *testing.T) {
	if err := ValidateRule46(completeTaxInvoice(t)); err != nil {
		t.Fatalf("a tax invoice carrying every mandatory particular was refused: %v", err)
	}
}

// TestAnInvoiceMissingOneMandatoryParticularIsRefusedByNameAndClause removes ONE
// mandatory particular at a time and requires the refusal to name that field and
// cite the clause that requires it.
//
// ONE CASE PER CHECKABLE CLAUSE OF RULE 46. Clauses (p) — whether tax is payable
// on reverse charge — and (q) — the signature — are absent from this table on
// purpose and are NOT unvalidated by oversight: neither has a "missing" state a
// validator could detect (a bool always holds one of its two values, and a PDF
// cannot be asked whether a human signed it), so both are discharged
// structurally by RenderInvoice always printing them. Their tests are
// TestATaxInvoiceStatesWhetherTaxIsPayableOnReverseChargeEitherWay and
// TestEveryPrintedInvoiceCarriesASignaturePanelForRule46q.
func TestAnInvoiceMissingOneMandatoryParticularIsRefusedByNameAndClause(t *testing.T) {
	cases := []struct {
		name       string
		wantField  string
		wantClause string
		break_     func(inv *Invoice)
	}{
		{
			name: "the supplier is not named", wantField: "supplier.name", wantClause: "46(a)",
			break_: func(inv *Invoice) { inv.Supplier.Name = "" },
		},
		{
			name: "the supplier has no address", wantField: "supplier.address1", wantClause: "46(a)",
			break_: func(inv *Invoice) { inv.Supplier.Address1 = "" },
		},
		{
			name: "the supplier has no GSTIN", wantField: "supplier.gstin", wantClause: "46(a)",
			break_: func(inv *Invoice) { inv.Supplier.GSTIN = "" },
		},
		{
			name: "there is no serial number", wantField: "number", wantClause: "46(b)",
			break_: func(inv *Invoice) { inv.Number = "" },
		},
		{
			name: "the serial number is only whitespace", wantField: "number", wantClause: "46(b)",
			break_: func(inv *Invoice) { inv.Number = "   " },
		},
		{
			name: "the serial number is longer than sixteen characters", wantField: "number", wantClause: "46(b)",
			break_: func(inv *Invoice) { inv.Number = "INVOICE/2026-2027/000001" },
		},
		{
			name: "the serial number contains a character the clause does not allow", wantField: "number", wantClause: "46(b)",
			break_: func(inv *Invoice) { inv.Number = "INV#0001" },
		},
		{
			name: "there is no date of issue", wantField: "date", wantClause: "46(c)",
			break_: func(inv *Invoice) { inv.Date = time.Time{} },
		},
		{
			name: "the recipient is not named", wantField: "recipient.name", wantClause: "46(d)",
			break_: func(inv *Invoice) { inv.Recipient.Name = "" },
		},
		{
			name: "the recipient has no address", wantField: "recipient.address1", wantClause: "46(d)",
			break_: func(inv *Invoice) { inv.Recipient.Address1 = "" },
		},
		{
			name:      "a fifty thousand rupee supply to an unregistered person does not name the recipient's State",
			wantField: "recipient.state", wantClause: "46(e)",
			break_: func(inv *Invoice) {
				inv.Recipient.GSTIN = ""
				inv.Recipient.State = ""
				inv.Total = dec(t, "50000.00")
			},
		},
		{
			name:      "a fifty thousand rupee supply to an unregistered person does not carry the State code",
			wantField: "recipient.stateCode", wantClause: "46(e)",
			break_: func(inv *Invoice) {
				inv.Recipient.GSTIN = ""
				inv.Recipient.StateCode = ""
				inv.Total = dec(t, "50000.00")
			},
		},
		{
			name: "a line has no HSN code", wantField: "items[1].hsn", wantClause: "46(g)",
			break_: func(inv *Invoice) { inv.Items[1].HSN = "" },
		},
		{
			name: "a line has no description", wantField: "items[0].description", wantClause: "46(h)",
			break_: func(inv *Invoice) { inv.Items[0].Description = "" },
		},
		{
			name: "there are no lines at all", wantField: "items", wantClause: "46(h)",
			break_: func(inv *Invoice) { inv.Items = nil },
		},
		{
			name: "a quantity is given without its unit", wantField: "items[0].unit", wantClause: "46(i)",
			break_: func(inv *Invoice) { inv.Items[0].Unit = "" },
		},
		{
			name: "the total value of supply is nil", wantField: "total", wantClause: "46(j)",
			break_: func(inv *Invoice) { inv.Total = dec(t, "0") },
		},
		{
			name: "the taxable value is nil", wantField: "subtotal", wantClause: "46(k)",
			break_: func(inv *Invoice) { inv.Subtotal = dec(t, "0") },
		},
		{
			name: "no rate of tax appears anywhere on the invoice", wantField: "taxes", wantClause: "46(l)",
			break_: func(inv *Invoice) { inv.Taxes = nil },
		},
		{
			name: "a tax head is not named, so its amount cannot be read as central or State tax", wantField: "taxes[0].name", wantClause: "46(m)",
			break_: func(inv *Invoice) { inv.Taxes[0].Name = "" },
		},
		{
			name: "an inter-State supply does not name the place of supply", wantField: "placeOfSupply", wantClause: "46(n)",
			break_: func(inv *Invoice) {
				inv.Taxes = []TaxHead{{Name: "IGST", Rate: dec(t, "18"), Amount: dec(t, "180.00")}}
				inv.PlaceOfSupply = ""
				inv.PlaceOfSupplyCode = "29"
			},
		},
		{
			name: "an inter-State supply carries no place of supply State code", wantField: "placeOfSupplyCode", wantClause: "46(n)",
			break_: func(inv *Invoice) {
				inv.Taxes = []TaxHead{{Name: "IGST", Rate: dec(t, "18"), Amount: dec(t, "180.00")}}
				inv.PlaceOfSupply = "Karnataka"
				inv.PlaceOfSupplyCode = ""
			},
		},
		{
			name: "the goods go to a delivery address the invoice does not state", wantField: "shipTo.address1", wantClause: "46(o)",
			break_: func(inv *Invoice) {
				inv.ShipTo = &Party{Name: "Kulkarni Hardware - Godown", City: "Nashik"}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv := completeTaxInvoice(t)
			c.break_(&inv)

			err := ValidateRule46(inv)
			if err == nil {
				t.Fatalf("this invoice is invalid under Rule %s because %s is missing, and the completeness gate accepted it; it would have been printed with a blank where a mandatory particular belongs",
					c.wantClause, c.wantField)
			}
			var r46 *Rule46Error
			if !errors.As(err, &r46) {
				t.Fatalf("the refusal was %T (%v), not a *Rule46Error, so a caller cannot tell which particulars to fix", err, err)
			}
			found := false
			for _, m := range r46.Missing {
				if m.Field != c.wantField {
					continue
				}
				found = true
				if m.Clause != c.wantClause {
					t.Errorf("%s is reported against Rule %s but is required by Rule %s; a wrong clause letter is a false citation of the law",
						m.Field, m.Clause, c.wantClause)
				}
				if strings.TrimSpace(m.Why) == "" {
					t.Errorf("%s is reported with no reason, so the operator is told to fill in a field and not why", m.Field)
				}
			}
			if !found {
				t.Fatalf("this invoice is invalid under Rule %s because %s is missing, and the refusal never names it: %v",
					c.wantClause, c.wantField, err)
			}
			if !strings.Contains(err.Error(), c.wantClause) {
				t.Errorf("the message an operator reads does not cite Rule %s: %s", c.wantClause, err.Error())
			}
			if !strings.Contains(err.Error(), c.wantField) {
				t.Errorf("the message an operator reads does not name %s: %s", c.wantField, err.Error())
			}
		})
	}
}

// TestARefusalNamesEveryMissingParticularAndNotJustTheFirst is the reason
// Rule46Error carries a slice. An operator who is told about one missing field at
// a time has to make one round trip through the voucher screen per field, and
// four round trips is how an invoice ends up being typed into a spreadsheet
// instead.
func TestARefusalNamesEveryMissingParticularAndNotJustTheFirst(t *testing.T) {
	inv := completeTaxInvoice(t)
	inv.Supplier.GSTIN = ""    // 46(a)
	inv.Number = ""            // 46(b)
	inv.Date = time.Time{}     // 46(c)
	inv.Items[0].HSN = ""      // 46(g)
	inv.Items[1].Unit = ""     // 46(i)
	inv.Taxes[1].Name = ""     // 46(m)
	inv.Subtotal = dec(t, "0") // 46(k)

	err := ValidateRule46(inv)
	if err == nil {
		t.Fatal("an invoice missing seven mandatory particulars was accepted")
	}
	var r46 *Rule46Error
	if !errors.As(err, &r46) {
		t.Fatalf("the refusal was %T, not a *Rule46Error", err)
	}
	want := []string{"supplier.gstin", "number", "date", "items[0].hsn", "items[1].unit", "taxes[1].name", "subtotal"}
	for _, field := range want {
		found := false
		for _, m := range r46.Missing {
			if m.Field == field {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is missing from this invoice and the refusal does not mention it; the operator will fix what they were told about, print again, and be refused again: %v", field, err)
		}
	}
	if got := len(r46.Fields()); got != len(want) {
		t.Errorf("the refusal names %d particulars and %d are missing (%v); an operator fixing an invoice one 400 at a time is a bad afternoon",
			got, len(want), r46.Fields())
	}
}

// TestTheCommonestRefusalInTheProductIsAGrammaticalSentence pins the count and the
// verb of the refusal message against each other.
//
// WHY A GRAMMAR TEST EARNS ITS PLACE HERE. This is the single most frequently read
// error string in the product: it is what comes back when an operator presses print
// at a counter with a customer in front of them, and ONE missing particular is by
// far the commonest case, so the plural half of the sentence was the half almost
// nobody saw. "1 mandatory particular are missing" reads as a machine that does not
// know what it is talking about, and the operator who decides this software cannot
// be trusted with the invoice raises it from a spreadsheet instead — which is how a
// business ends up with an invoice series this product cannot account for. The
// figures on the document are only as credible as the sentence refusing it.
func TestTheCommonestRefusalInTheProductIsAGrammaticalSentence(t *testing.T) {
	// ONE missing particular: singular noun, singular verb.
	one := completeTaxInvoice(t)
	one.Items[0].HSN = ""
	err := ValidateRule46(one)
	if err == nil {
		t.Fatal("an invoice with no HSN on its first line was accepted")
	}
	var r46 *Rule46Error
	if !errors.As(err, &r46) {
		t.Fatalf("the refusal was %T, not a *Rule46Error", err)
	}
	if got := len(r46.Missing); got != 1 {
		t.Fatalf("this case is meant to leave exactly ONE particular missing and %d are missing (%v); the sentence being tested is the singular one",
			got, r46.Fields())
	}
	if want := "1 mandatory particular is missing"; !strings.Contains(err.Error(), want) {
		t.Errorf("the commonest refusal in the product does not read %q — the operator at the counter sees %q instead, and a message whose grammar contradicts its own count is read as a broken program rather than an incomplete invoice",
			want, err.Error())
	}

	// TWO missing particulars: plural noun, plural verb. Pinned in the same test so
	// that "fixing" one half of the sentence by hard-coding the other cannot pass.
	two := completeTaxInvoice(t)
	two.Items[0].HSN = ""
	two.Supplier.GSTIN = ""
	err = ValidateRule46(two)
	if err == nil {
		t.Fatal("an invoice with no HSN and no supplier GSTIN was accepted")
	}
	if !errors.As(err, &r46) {
		t.Fatalf("the refusal was %T, not a *Rule46Error", err)
	}
	if got := len(r46.Missing); got != 2 {
		t.Fatalf("this case is meant to leave exactly TWO particulars missing and %d are missing (%v)", got, r46.Fields())
	}
	if want := "2 mandatory particulars are missing"; !strings.Contains(err.Error(), want) {
		t.Errorf("the plural refusal does not read %q but %q; both halves of the sentence have to agree with the count, or one of the two cases an operator meets every day is ungrammatical",
			want, err.Error())
	}

	// A bill of supply takes the same sentence through the same Error(), so the
	// singular case is pinned on both documents rather than on the tax invoice only.
	bos := completeBillOfSupply(t)
	bos.Items[0].HSN = ""
	err = ValidateRule46(bos)
	if err == nil {
		t.Fatal("a bill of supply with no HSN on its first line was accepted")
	}
	if want := "1 mandatory particular is missing"; !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal of a bill of supply reads %q; the two documents share one Error() and must share its grammar", err.Error())
	}
}

// TestTheSerialNumberShapeRuleFortySixbSpellsOut pins the character set and the
// length the clause states in its own words. Uniqueness within the financial year
// is NOT checkable from one document and is not claimed here.
func TestTheSerialNumberShapeRuleFortySixbSpellsOut(t *testing.T) {
	cases := []struct {
		number string
		valid  bool
		why    string
	}{
		{"INV/2026-27/0001", true, "sixteen characters of alphanumerics, hyphen and slash is exactly what the clause allows"},
		{"1", true, "a one-character series is consecutive and within the length"},
		{"ABCDEFGHIJKLMNOP", true, "sixteen characters is 'not exceeding sixteen'"},
		{"ABCDEFGHIJKLMNOPQ", false, "seventeen characters exceeds the sixteen the clause allows"},
		{"INV 0001", false, "a space is not an alphabet, a numeral, a hyphen or a slash"},
		{"INV#0001", false, "a hash is not in the clause's character set"},
		{"INV_0001", false, "an underscore is not in the clause's character set, however ordinary it looks"},
		{"INV.0001", false, "a full stop is not in the clause's character set"},
	}
	for _, c := range cases {
		inv := completeTaxInvoice(t)
		inv.Number = c.number
		err := ValidateRule46(inv)
		if c.valid && err != nil {
			t.Errorf("the serial number %q is valid under Rule 46(b) (%s) and the invoice was refused: %v", c.number, c.why, err)
		}
		if !c.valid && err == nil {
			t.Errorf("the serial number %q is invalid under Rule 46(b) (%s) and the invoice was accepted", c.number, c.why)
		}
	}
}

// TestTheFiftyThousandRupeeThresholdOfClauseEIsInclusive pins the boundary. The
// clause reads "fifty thousand rupees or more", so an invoice for exactly Rs
// 50,000 to an unregistered person carries the extra particulars and one for Rs
// 49,999.99 does not.
func TestTheFiftyThousandRupeeThresholdOfClauseEIsInclusive(t *testing.T) {
	cases := []struct {
		total    string
		gstin    string
		wantFail bool
		why      string
	}{
		{"49999.99", "", false, "below fifty thousand, clause (e) does not apply"},
		{"50000.00", "", true, "'fifty thousand rupees or more' includes exactly fifty thousand"},
		{"50000.01", "", true, "above the threshold"},
		{"9999999.00", "27AACCK5678L1Z9", false, "a REGISTERED recipient is outside clause (e) however large the supply"},
	}
	for _, c := range cases {
		inv := completeTaxInvoice(t)
		inv.Recipient.GSTIN = c.gstin
		inv.Recipient.State = ""
		inv.Recipient.StateCode = ""
		inv.Total = dec(t, c.total)

		err := ValidateRule46(inv)
		named := err != nil && strings.Contains(err.Error(), "46(e)")
		if c.wantFail && !named {
			t.Errorf("a supply of Rs %s to an unregistered person must carry the recipient's State and code (%s), and the gate did not say so: %v", c.total, c.why, err)
		}
		if !c.wantFail && named {
			t.Errorf("clause (e) was applied to a supply of Rs %s where it does not apply (%s): %v", c.total, c.why, err)
		}
	}
}

// TestAnIntraStateInvoiceIsNotRefusedForHavingNoPlaceOfSupply guards the
// CONDITIONAL half of clause (n). The clause requires the place of supply "in the
// case of a supply involving inter-State trade or commerce"; a validator that
// demanded it on every invoice would refuse legal intra-State documents, which is
// the same class of harm as accepting an incomplete one.
func TestAnIntraStateInvoiceIsNotRefusedForHavingNoPlaceOfSupply(t *testing.T) {
	inv := completeTaxInvoice(t)
	inv.PlaceOfSupply = ""
	inv.PlaceOfSupplyCode = ""
	if err := ValidateRule46(inv); err != nil {
		t.Fatalf("an intra-State CGST/SGST invoice was refused for a particular Rule 46(n) requires only on an inter-State supply: %v", err)
	}
}

// TestAnIGSTHeadIsEnoughToMakeThePlaceOfSupplyMandatory is the reason the
// inter-State test does not rely on the place of supply itself: the invoice being
// hunted is the one where the place of supply is the missing field, and a check
// that needed it in order to decide whether it was required would never fire.
func TestAnIGSTHeadIsEnoughToMakeThePlaceOfSupplyMandatory(t *testing.T) {
	inv := completeTaxInvoice(t)
	inv.Taxes = []TaxHead{{Name: "igst", Rate: dec(t, "18"), Amount: dec(t, "180.00")}}
	inv.PlaceOfSupply = ""
	inv.PlaceOfSupplyCode = ""

	err := ValidateRule46(inv)
	if err == nil || !strings.Contains(err.Error(), "46(n)") {
		t.Fatalf("integrated tax is charged only on an inter-State supply, so this invoice must state its place of supply under Rule 46(n); got %v", err)
	}
}

// TestAMisKeyedInterStateSupplyIsStillCaughtByTheStateCodes covers the second
// signal: CGST/SGST charged where the place of supply is in another State.
func TestAMisKeyedInterStateSupplyIsStillCaughtByTheStateCodes(t *testing.T) {
	inv := completeTaxInvoice(t)
	inv.PlaceOfSupplyCode = "29" // Karnataka, while the supplier is 27
	inv.PlaceOfSupply = ""
	err := ValidateRule46(inv)
	if err == nil || !strings.Contains(err.Error(), "46(n)") {
		t.Fatalf("the place of supply is in another State than the supplier, so this is an inter-State supply and Rule 46(n) applies; got %v", err)
	}
}

// TestAServiceLineWithNoQuantityIsNotRefusedForHavingNoUnit pins the documented
// inference behind clause (i), which applies "in case of goods". A consultancy fee
// is not "1 nos", and refusing every service invoice in the country would be a
// worse failure than the blind spot this accepts (see
// invQuantityParticularsApply).
func TestAServiceLineWithNoQuantityIsNotRefusedForHavingNoUnit(t *testing.T) {
	inv := completeTaxInvoice(t)
	inv.Items = []LineItem{{
		SerialNo: 1, Description: "Statutory audit fee for FY 2025-26", HSN: "998221",
		Quantity: dec(t, "0"), Unit: "", Rate: dec(t, "0"), Amount: dec(t, "1000.00"),
	}}
	if err := ValidateRule46(inv); err != nil {
		t.Fatalf("a service line carries no quantity and no unique quantity code, and Rule 46(i) applies 'in case of goods'; the invoice was refused: %v", err)
	}
}

// ---------------------------------------------------------------------------
// the bill of supply — Rule 49, NOT Rule 46
// ---------------------------------------------------------------------------

// completeBillOfSupply is a Rule 49 document: no tax, no place of supply, no
// reverse-charge marker.
func completeBillOfSupply(t *testing.T) Invoice {
	t.Helper()
	inv := completeTaxInvoice(t)
	inv.Kind = KindBillOfSupply
	inv.Number = "BOS/2026-27/007"
	inv.Taxes = nil
	inv.PlaceOfSupply = ""
	inv.PlaceOfSupplyCode = ""
	inv.Total = dec(t, "1000.00")
	return inv
}

// TestABillOfSupplyIsNotRefusedForLackingTheTaxParticularsItMayNotCarry is the
// point of having two lists. A bill of supply is issued precisely because no tax
// is charged, so demanding a rate of tax (46(l)), an amount of tax (46(m)), a
// place of supply (46(n)) or a reverse-charge marker (46(p)) would refuse a
// document that is completely legal.
func TestABillOfSupplyIsNotRefusedForLackingTheTaxParticularsItMayNotCarry(t *testing.T) {
	if err := ValidateRule46(completeBillOfSupply(t)); err != nil {
		t.Fatalf("a complete Rule 49 bill of supply was refused, apparently against the tax-invoice clauses: %v", err)
	}
}

// TestABillOfSupplyThatChargesTaxIsRefusedOutright. A composition supplier "shall
// not collect any tax from the recipient" (section 10(4) of the CGST Act) and an
// exempt supply has none to collect. Printing this document would hand a customer
// a tax charge they cannot claim credit for and the supplier cannot lawfully
// collect.
func TestABillOfSupplyThatChargesTaxIsRefusedOutright(t *testing.T) {
	inv := completeBillOfSupply(t)
	inv.Taxes = []TaxHead{{Name: "CGST", Rate: dec(t, "9"), Amount: dec(t, "90.00")}}

	err := ValidateRule46(inv)
	if err == nil {
		t.Fatal("a bill of supply charging CGST was accepted; a composition supplier may not collect tax under section 10(4)")
	}
	if !errors.Is(err, ErrTaxOnBillOfSupply) {
		t.Fatalf("the refusal does not identify itself as tax on a bill of supply, so a caller cannot map it to the right message: %v", err)
	}
	if pdf, rerr := RenderInvoice(inv); pdf != nil || rerr == nil {
		t.Fatalf("a bill of supply charging tax was printed anyway (%d bytes, err %v)", len(pdf), rerr)
	}
}

// TestABillOfSupplyMissingARuleFortyNineParticularIsRefusedWithARuleFortyNineClause
// pins that the citation follows the rule that was actually applied. An operator
// told "Rule 46(a)" about a bill of supply has been cited a rule that does not
// govern their document.
func TestABillOfSupplyMissingARuleFortyNineParticularIsRefusedWithARuleFortyNineClause(t *testing.T) {
	cases := []struct {
		name       string
		wantField  string
		wantClause string
		break_     func(inv *Invoice)
	}{
		{"no supplier GSTIN", "supplier.gstin", "49(a)", func(inv *Invoice) { inv.Supplier.GSTIN = "" }},
		{"no serial number", "number", "49(b)", func(inv *Invoice) { inv.Number = "" }},
		{"no date of issue", "date", "49(c)", func(inv *Invoice) { inv.Date = time.Time{} }},
		{"no recipient name", "recipient.name", "49(d)", func(inv *Invoice) { inv.Recipient.Name = "" }},
		{"no HSN on a line", "items[0].hsn", "49(e)", func(inv *Invoice) { inv.Items[0].HSN = "" }},
		{"no description on a line", "items[1].description", "49(f)", func(inv *Invoice) { inv.Items[1].Description = "" }},
		{"no value of supply", "total", "49(g)", func(inv *Invoice) { inv.Total = dec(t, "0") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv := completeBillOfSupply(t)
			c.break_(&inv)
			err := ValidateRule46(inv)
			if err == nil {
				t.Fatalf("a bill of supply with no %s is invalid under Rule %s and was accepted", c.wantField, c.wantClause)
			}
			var r46 *Rule46Error
			if !errors.As(err, &r46) {
				t.Fatalf("the refusal was %T, not a *Rule46Error: %v", err, err)
			}
			for _, m := range r46.Missing {
				if m.Field == c.wantField {
					if m.Clause != c.wantClause {
						t.Fatalf("%s on a bill of supply is reported against Rule %s; Rule 49 governs this document, not Rule 46", m.Field, m.Clause)
					}
					return
				}
			}
			t.Fatalf("the refusal never names %s: %v", c.wantField, err)
		})
	}
}

// TestTheCompletenessGateRefusesToCertifyADocumentItHasNoRulesFor. Returning nil
// for a delivery note would be a gate that certifies documents it has never
// looked at, and the caller would read that nil as "this is a valid tax invoice".
func TestTheCompletenessGateRefusesToCertifyADocumentItHasNoRulesFor(t *testing.T) {
	for _, kind := range []DocKind{KindDeliveryNote, KindReceipt, KindPayment, KindCreditNote, KindDebitNote, KindReport, DocKind("")} {
		inv := completeTaxInvoice(t)
		inv.Kind = kind
		if err := ValidateRule46(inv); err == nil {
			t.Errorf("the gate certified a %q as complete, and it holds no list of mandatory particulars for one", string(kind))
		}
	}
}

// TestAParticularHoldingOnlyASpaceIsTreatedAsAbsent. A field containing " "
// prints as nothing, so accepting it as present would put a blank on the page —
// the exact outcome the gate exists to prevent.
func TestAParticularHoldingOnlyASpaceIsTreatedAsAbsent(t *testing.T) {
	inv := completeTaxInvoice(t)
	inv.Supplier.GSTIN = "   "
	inv.Items[0].HSN = "\t"
	err := ValidateRule46(inv)
	if err == nil {
		t.Fatal("a GSTIN of three spaces and an HSN of one tab were accepted as present; both print as a blank on the invoice")
	}
	for _, want := range []string{"supplier.gstin", "items[0].hsn"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s holds only whitespace and the refusal does not name it: %v", want, err)
		}
	}
}

// TestAnEmptyRuleFortySixErrorStillProducesAnErrorMessage. An error whose Error()
// returns "" is logged as an empty line and shown to a user as nothing at all.
func TestAnEmptyRuleFortySixErrorStillProducesAnErrorMessage(t *testing.T) {
	var e *Rule46Error
	if e.Error() == "" {
		t.Error("a nil *Rule46Error renders as an empty message, which reaches a user as a blank refusal")
	}
	if (&Rule46Error{}).Error() == "" {
		t.Error("an empty *Rule46Error renders as an empty message, which reaches a user as a blank refusal")
	}
}
