package statutory

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/shopspring/decimal"
)

//go:embed nic_einvoice_schema.json
var nicSchemaBytes []byte

// NICSchema is the parsed recorded NIC e-invoice golden subset. The validator
// reads its constants (lengths, item caps, GSTIN pattern, decimal places).
type NICSchema struct {
	Version     string   `json:"version"`
	DocTypes    []string `json:"doc_types"`
	SupplyTypes []string `json:"supply_types"`
	TaxSchemes  []string `json:"tax_schemes"`
	GSTIN       struct {
		Length  int    `json:"length"`
		Pattern string `json:"pattern"`
	} `json:"gstin"`
	DecimalPlaces int `json:"decimal_places"`
	ItemList      struct {
		Min int `json:"min"`
		Max int `json:"max"`
	} `json:"item_list"`
	MaxLengths map[string]int `json:"max_lengths"`
}

var (
	loadedSchema NICSchema
	gstinRe      *regexp.Regexp
)

func init() {
	if err := json.Unmarshal(nicSchemaBytes, &loadedSchema); err != nil {
		panic(fmt.Sprintf("statutory: cannot parse embedded NIC schema: %v", err))
	}
	gstinRe = regexp.MustCompile(loadedSchema.GSTIN.Pattern)
}

// Schema returns the loaded NIC golden schema subset.
func Schema() NICSchema { return loadedSchema }

// ValidateEInvoice runs the recorded NIC conformance checks against the payload
// and returns the list of VIOLATIONS (empty slice means conformant). It is a
// hand-written validator driven by the embedded schema constants — there is no
// external json-schema dependency.
func ValidateEInvoice(p EInvoicePayload) []string {
	var v []string

	// Version present.
	if p.Version == "" {
		v = append(v, "Version: missing")
	}

	// DocDtls mandatory fields.
	if p.DocDtls.Typ == "" {
		v = append(v, "DocDtls.Typ: missing")
	} else if !inSet(loadedSchema.DocTypes, p.DocDtls.Typ) {
		// The recorded schema has carried this list since it was written and
		// nothing read it. NIC keys the IRN on (SupplierGSTIN, DocTyp, DocNo, FY),
		// so the doc type is not decoration: it is part of the document's identity
		// at the IRP.
		v = append(v, fmt.Sprintf("DocDtls.Typ: %q is not one of the recorded doc types %v",
			p.DocDtls.Typ, loadedSchema.DocTypes))
	}
	if p.DocDtls.No == "" {
		v = append(v, "DocDtls.No: missing")
	} else if max := loadedSchema.MaxLengths["DocDtls.No"]; max > 0 && len(p.DocDtls.No) > max {
		v = append(v, fmt.Sprintf("DocDtls.No: exceeds max length %d", max))
	}
	if p.DocDtls.Dt == "" {
		v = append(v, "DocDtls.Dt: missing")
	}

	// Seller & buyer GSTINs: present + 15-char + pattern.
	v = append(v, validateGSTIN("SellerDtls.Gstin", p.SellerDtls.Gstin)...)
	v = append(v, validateGSTIN("BuyerDtls.Gstin", p.BuyerDtls.Gstin)...)

	if p.SellerDtls.LglNm == "" {
		v = append(v, "SellerDtls.LglNm: missing")
	}
	if p.BuyerDtls.LglNm == "" {
		v = append(v, "BuyerDtls.LglNm: missing")
	}
	if p.SellerDtls.Stcd == "" {
		v = append(v, "SellerDtls.Stcd: missing")
	}
	if p.BuyerDtls.Pos == "" {
		v = append(v, "BuyerDtls.Pos: missing")
	}

	// ItemList cardinality.
	n := len(p.ItemList)
	if n < loadedSchema.ItemList.Min {
		v = append(v, fmt.Sprintf("ItemList: %d items, minimum %d", n, loadedSchema.ItemList.Min))
	}
	if n > loadedSchema.ItemList.Max {
		v = append(v, fmt.Sprintf("ItemList: %d items, maximum %d", n, loadedSchema.ItemList.Max))
	}

	// ⛔⛔ PER-ITEM: PRECISION *AND* ARITHMETIC.
	//
	// This loop used to check precision only, and that omission is what let a
	// discounted line ship as Qty:10 UnitPrice:500.00 TotAmt:4500.00 with no
	// Discount — five hundred rupees of self-contradiction, every field of which
	// had exactly two decimal places. A validator that checks the SHAPE of the
	// numbers and never their RELATIONSHIP catches the paisa and misses the ₹500.
	//
	// The item list also has to add up to the invoice, which the REAL IRP checks
	// ("sum of AssAmt of all items must equal AssVal") and this validator did not.
	// It is accumulated here and compared once the ValDtls are parsed below.
	itemAssSum := decimal.Zero
	itemSumOK := true
	for i, it := range p.ItemList {
		prefix := fmt.Sprintf("ItemList[%d]", i)
		for _, f := range []struct {
			name, val string
		}{
			{"TotAmt", it.TotAmt},
			{"Discount", it.Discount},
			{"AssAmt", it.AssAmt},
			{"TotItemVal", it.TotItemVal},
			{"IgstAmt", it.IgstAmt},
			{"CgstAmt", it.CgstAmt},
			{"SgstAmt", it.SgstAmt},
		} {
			if msg := check2dp(prefix+"."+f.name, f.val); msg != "" {
				v = append(v, msg)
			}
		}

		// ⛔ Unit IS MANDATORY FOR GOODS AND WAS NEVER CHECKED, so cored minted a
		// blank one on every line it ever sent and froze the voucher against it.
		// A quantity with no unit is not a quantity: "10" of a thing sold by
		// weight and "10" of a thing sold by the piece are different consignments.
		if strings.TrimSpace(it.Unit) == "" {
			v = append(v, prefix+".Unit: missing (NIC requires a unit of measure on every goods line)")
		}

		qty, qtyOK := parseItemNumber(&v, prefix+".Qty", it.Qty)
		unitPrice, priceOK := parseItemNumber(&v, prefix+".UnitPrice", it.UnitPrice)
		// NIC models UnitPrice as decimal(12,3). The books hold NUMERIC(19,4), so
		// a fourth place is representable here and would be REFUSED BY THE IRP;
		// say so rather than emitting it and finding out against production
		// credentials.
		if priceOK && !unitPrice.Equal(unitPrice.Round(3)) {
			v = append(v, fmt.Sprintf("%s.UnitPrice: at most 3 decimal places (NIC decimal(12,3)), got %q",
				prefix, it.UnitPrice))
		}
		totAmt, totOK := parseItemNumber(&v, prefix+".TotAmt", it.TotAmt)
		// Discount is the one item amount NIC treats as optional; blank reads as
		// zero rather than as a violation.
		discount, discOK := decimal.Zero, true
		if strings.TrimSpace(it.Discount) != "" {
			discount, discOK = parseItemNumber(&v, prefix+".Discount", it.Discount)
		}
		assAmt, assOK := parseItemNumber(&v, prefix+".AssAmt", it.AssAmt)

		if qtyOK && priceOK && totOK {
			// Rounded to the paisa because Qty x UnitPrice is the ONE product on
			// the document that can carry more places than NIC prints (a 3dp rate
			// times a 3dp quantity is 6dp). The rounding is the document's own —
			// there is no tolerance here, the two sides must agree exactly once
			// both are expressed at the precision the document uses.
			if want := qty.Mul(unitPrice).Round(int32(loadedSchema.DecimalPlaces)); !want.Equal(totAmt) {
				v = append(v, fmt.Sprintf("%s.TotAmt: must equal Qty x UnitPrice = %s, got %s",
					prefix, want.StringFixed(2), totAmt.StringFixed(2)))
			}
		}
		if totOK && discOK && assOK {
			if want := totAmt.Sub(discount); !want.Equal(assAmt) {
				v = append(v, fmt.Sprintf("%s.AssAmt: must equal TotAmt - Discount = %s, got %s",
					prefix, want.StringFixed(2), assAmt.StringFixed(2)))
			}
		}
		if discOK && discount.IsNegative() {
			// A negative discount is a surcharge, and NIC has no such field. It is
			// also the shape a rounding residue takes when an assembler quotes a
			// rate to fewer places than it derived the taxable value from.
			v = append(v, fmt.Sprintf("%s.Discount: must not be negative, got %s",
				prefix, discount.StringFixed(2)))
		}

		igst, igstOK := parseItemNumber(&v, prefix+".IgstAmt", it.IgstAmt)
		cgst, cgstOK := parseItemNumber(&v, prefix+".CgstAmt", it.CgstAmt)
		sgst, sgstOK := parseItemNumber(&v, prefix+".SgstAmt", it.SgstAmt)
		itemTotal, itemTotalOK := parseItemNumber(&v, prefix+".TotItemVal", it.TotItemVal)
		if assOK && igstOK && cgstOK && sgstOK && itemTotalOK {
			if want := assAmt.Add(igst).Add(cgst).Add(sgst); !want.Equal(itemTotal) {
				v = append(v, fmt.Sprintf("%s.TotItemVal: must equal AssAmt + IgstAmt + CgstAmt + SgstAmt = %s, got %s",
					prefix, want.StringFixed(2), itemTotal.StringFixed(2)))
			}
		}

		if assOK {
			itemAssSum = itemAssSum.Add(assAmt)
		} else {
			itemSumOK = false
		}
	}

	// ValDtls amounts: 2dp precision.
	assVal, assOK := parseDecimal(&v, "ValDtls.AssVal", p.ValDtls.AssVal)
	cgstVal, cgstOK := parseDecimal(&v, "ValDtls.CgstVal", p.ValDtls.CgstVal)
	sgstVal, sgstOK := parseDecimal(&v, "ValDtls.SgstVal", p.ValDtls.SgstVal)
	igstVal, igstOK := parseDecimal(&v, "ValDtls.IgstVal", p.ValDtls.IgstVal)
	totVal, totOK := parseDecimal(&v, "ValDtls.TotInvVal", p.ValDtls.TotInvVal)

	// ⛔ THE ITEM LIST MUST ADD UP TO THE INVOICE. The REAL IRP checks this ("sum
	// of AssAmt of all items must equal AssVal") and nothing here did, so a
	// document whose items disagreed with their own total passed every check this
	// stack has and was rejected only against production credentials.
	if assOK && itemSumOK && len(p.ItemList) > 0 && !itemAssSum.Equal(assVal) {
		v = append(v, fmt.Sprintf("ValDtls.AssVal: the item list sums to %s but the invoice declares %s",
			itemAssSum.StringFixed(2), assVal.StringFixed(2)))
	}

	// Footing: TotInvVal == AssVal + CgstVal + SgstVal + IgstVal.
	if assOK && cgstOK && sgstOK && igstOK && totOK {
		want := assVal.Add(cgstVal).Add(sgstVal).Add(igstVal)
		if !want.Equal(totVal) {
			v = append(v, fmt.Sprintf("ValDtls.TotInvVal: footing mismatch, got %s want %s", totVal.StringFixed(2), want.StringFixed(2)))
		}
	}

	// Intra vs inter-state consistency. Seller state vs buyer POS decides.
	intra := p.SellerDtls.Stcd != "" && p.SellerDtls.Stcd == p.BuyerDtls.Pos
	if cgstOK && sgstOK && igstOK {
		if intra {
			// intra-state: CGST+SGST charged, IGST must be zero.
			if !igstVal.IsZero() {
				v = append(v, "ValDtls: intra-state supply but IgstVal is non-zero")
			}
			if cgstVal.IsZero() && sgstVal.IsZero() && !assVal.IsZero() {
				v = append(v, "ValDtls: intra-state supply but CgstVal/SgstVal both zero")
			}
		} else {
			// inter-state: IGST charged, CGST and SGST must be zero.
			if !cgstVal.IsZero() || !sgstVal.IsZero() {
				v = append(v, "ValDtls: inter-state supply but CgstVal/SgstVal is non-zero")
			}
			if igstVal.IsZero() && !assVal.IsZero() {
				v = append(v, "ValDtls: inter-state supply but IgstVal is zero")
			}
		}
	}

	return v
}

func validateGSTIN(field, g string) []string {
	if g == "" {
		return []string{field + ": missing"}
	}
	if len(g) != loadedSchema.GSTIN.Length {
		return []string{fmt.Sprintf("%s: must be %d chars, got %d", field, loadedSchema.GSTIN.Length, len(g))}
	}
	if !gstinRe.MatchString(g) {
		return []string{field + ": does not match GSTIN pattern"}
	}
	return nil
}

// inSet reports whether s appears in the recorded vocabulary.
func inSet(vocab []string, s string) bool {
	for _, allowed := range vocab {
		if allowed == s {
			return true
		}
	}
	return false
}

// parseItemNumber parses one item-level number, recording a violation when it is
// absent or unparseable.
//
// ⛔ IT DOES NOT CHECK PRECISION, deliberately: the 2dp rule is checked once, by
// check2dp, over the list of fields it applies to. Reporting the same field twice
// would put two violations on one operator-facing refusal and make the SECOND
// one — the arithmetic — look like a consequence of the first.
func parseItemNumber(v *[]string, field, s string) (decimal.Decimal, bool) {
	if strings.TrimSpace(s) == "" {
		*v = append(*v, field+": missing")
		return decimal.Decimal{}, false
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		*v = append(*v, field+": not a valid decimal")
		return decimal.Decimal{}, false
	}
	return d, true
}

// parseDecimal parses a money string and records a 2dp violation if malformed
// or not exactly two decimal places. Returns the parsed value and ok flag.
func parseDecimal(v *[]string, field, s string) (decimal.Decimal, bool) {
	if s == "" {
		*v = append(*v, field+": missing")
		return decimal.Decimal{}, false
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		*v = append(*v, field+": not a valid decimal")
		return decimal.Decimal{}, false
	}
	if msg := check2dp(field, s); msg != "" {
		*v = append(*v, msg)
		return d, true
	}
	return d, true
}

// check2dp returns a violation message if s is not exactly two decimal places
// (NIC requires fixed-2). Empty fields are skipped here (handled elsewhere).
func check2dp(field, s string) string {
	if s == "" {
		return ""
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return field + ": not a valid decimal"
	}
	if d.StringFixed(int32(loadedSchema.DecimalPlaces)) != s {
		return fmt.Sprintf("%s: must have exactly %d decimal places (got %q)", field, loadedSchema.DecimalPlaces, s)
	}
	return ""
}
