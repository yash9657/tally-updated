package statutory

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/shopspring/decimal"
)

//go:embed nic_einvoice_schema.json
var nicSchemaBytes []byte

// NICSchema is the parsed recorded NIC e-invoice golden subset. The validator
// reads its constants (lengths, item caps, GSTIN pattern, decimal places).
type NICSchema struct {
	Version        string `json:"version"`
	DocTypes       []string `json:"doc_types"`
	SupplyTypes    []string `json:"supply_types"`
	TaxSchemes     []string `json:"tax_schemes"`
	GSTIN          struct {
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

	// Per-item decimal-place checks on the value fields.
	for i, it := range p.ItemList {
		prefix := fmt.Sprintf("ItemList[%d]", i)
		for _, f := range []struct {
			name, val string
		}{
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
	}

	// ValDtls amounts: 2dp precision.
	assVal, assOK := parseDecimal(&v, "ValDtls.AssVal", p.ValDtls.AssVal)
	cgstVal, cgstOK := parseDecimal(&v, "ValDtls.CgstVal", p.ValDtls.CgstVal)
	sgstVal, sgstOK := parseDecimal(&v, "ValDtls.SgstVal", p.ValDtls.SgstVal)
	igstVal, igstOK := parseDecimal(&v, "ValDtls.IgstVal", p.ValDtls.IgstVal)
	totVal, totOK := parseDecimal(&v, "ValDtls.TotInvVal", p.ValDtls.TotInvVal)

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
