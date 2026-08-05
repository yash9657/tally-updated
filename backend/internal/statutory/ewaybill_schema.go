package statutory

// ewaybill_schema.go — the recorded NIC e-way bill schema subset and its
// validator, in the same shape as schema.go's for the e-invoice.
//
// ⛔ THE SCHEMA IS SECONDARY MATERIAL, exactly as rule138.go's figures are, and
// the JSON says so in its own `provenance` field. It is derived from published API
// documentation summaries and NOT from a schema document held in this repository,
// because no NIC call can be made from this deployment. Recording it anyway is the
// point: a payload validated against a recorded golden is a payload whose shape
// somebody has thought about, and the day real credentials exist the golden is what
// the first live rejection gets diffed against.
//
// ⚠️ SO A CLEAN ValidateEWayBill IS *NOT* A CLAIM THAT NIC WOULD ACCEPT THE
// PAYLOAD. It is a claim that the payload conforms to what this repository has
// RECORDED about NIC. PLAN-TALLY-PARITY.md §2.3 forbids claiming a validation that
// was not performed, and the distinction is carried in the function's own doc
// comment so a caller cannot quote it as conformance.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//go:embed nic_ewaybill_schema.json
var ewayBillSchemaBytes []byte

// EWayBillSchema is the recorded subset. Field-for-field with the JSON.
type EWayBillSchema struct {
	Schema      string `json:"schema"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Provenance  string `json:"provenance"`

	SupplyTypes    []string `json:"supply_types"`
	SubSupplyTypes []string `json:"sub_supply_types"`
	DocTypes       []string `json:"doc_types"`
	TransportModes []string `json:"transport_modes"`

	GSTIN struct {
		Length  int    `json:"length"`
		Pattern string `json:"pattern"`
	} `json:"gstin"`
	Pincode struct {
		Length  int    `json:"length"`
		Pattern string `json:"pattern"`
	} `json:"pincode"`
	VehicleNo struct {
		Min     int    `json:"min"`
		Max     int    `json:"max"`
		Pattern string `json:"pattern"`
	} `json:"vehicle_no"`
	TransporterID struct {
		Length int `json:"length"`
	} `json:"transporter_id"`

	DecimalPlaces int `json:"decimal_places"`
	ItemList      struct {
		Min int `json:"min"`
		Max int `json:"max"`
	} `json:"item_list"`
	MaxLengths    map[string]int `json:"max_lengths"`
	TransDistance struct {
		Min int `json:"min"`
		Max int `json:"max"`
	} `json:"trans_distance"`
}

var (
	loadedEWaySchema EWayBillSchema
	ewayPincodeRe    *regexp.Regexp
	ewayVehicleRe    *regexp.Regexp
	ewayGstinRe      *regexp.Regexp
)

// ewayBillSchemaVersion is what BuildEWayBill stamps on every payload. Read from
// the embedded schema rather than written as a literal, so the payload and the
// golden can never disagree about which version was targeted.
var ewayBillSchemaVersion string

func init() {
	if err := json.Unmarshal(ewayBillSchemaBytes, &loadedEWaySchema); err != nil {
		panic(fmt.Sprintf("statutory: cannot parse embedded NIC e-way bill schema: %v", err))
	}
	ewayPincodeRe = regexp.MustCompile(loadedEWaySchema.Pincode.Pattern)
	ewayVehicleRe = regexp.MustCompile(loadedEWaySchema.VehicleNo.Pattern)
	ewayGstinRe = regexp.MustCompile(loadedEWaySchema.GSTIN.Pattern)
	ewayBillSchemaVersion = loadedEWaySchema.Version
}

// EWayBillSchemaSubset returns the loaded golden.
func EWayBillSchemaSubset() EWayBillSchema { return loadedEWaySchema }

// ValidateEWayBill returns every way the payload departs from the RECORDED
// schema — an empty slice means conformant WITH THE RECORDING, which is not the
// same as conformant with NIC. See the file header.
//
// It returns ALL violations rather than the first, deliberately and for the reason
// ValidateEInvoice does: an operator fixing a consignment wants the whole list, and
// a validator that stops at the first turns one round of corrections into five.
func ValidateEWayBill(p EWayBillPayload) []string {
	var v []string
	s := loadedEWaySchema

	if p.Version == "" {
		v = append(v, "version: missing")
	}
	if !inSet(s.SupplyTypes, p.SupplyType) {
		v = append(v, fmt.Sprintf("supplyType: %q is not one of %v", p.SupplyType, s.SupplyTypes))
	}
	if !inSet(s.SubSupplyTypes, p.SubSupplyType) {
		v = append(v, fmt.Sprintf("subSupplyType: %q is not one of %v", p.SubSupplyType, s.SubSupplyTypes))
	}
	if !inSet(s.DocTypes, p.DocType) {
		v = append(v, fmt.Sprintf("docType: %q is not one of %v", p.DocType, s.DocTypes))
	}
	// The mode may legitimately be absent on a Part-A-only bill, so an empty string
	// is not a violation — but a NON-EMPTY value that is not a known code is, and
	// that distinction is the whole reason this is not a bare set membership check.
	if p.TransMode != "" && !inSet(s.TransportModes, p.TransMode) {
		v = append(v, fmt.Sprintf("transMode: %q is not one of %v", p.TransMode, s.TransportModes))
	}

	if p.DocNo == "" {
		v = append(v, "docNo: missing")
	} else if m := s.MaxLengths["docNo"]; m > 0 && len(p.DocNo) > m {
		v = append(v, fmt.Sprintf("docNo: %d characters exceeds the maximum of %d", len(p.DocNo), m))
	}
	// DD/MM/YYYY, which is NIC's format and not the ISO one cored uses. nicDate
	// returns an unparseable input UNCHANGED precisely so this check can see it —
	// substituting a placeholder there would have put a plausible wrong date on a
	// statutory document with nothing to complain about.
	if !isNICDate(p.DocDate) {
		v = append(v, fmt.Sprintf("docDate: %q is not DD/MM/YYYY", p.DocDate))
	}

	v = append(v, validateEWayGSTIN("fromGstin", p.FromGstin)...)
	v = append(v, validateEWayGSTIN("toGstin", p.ToGstin)...)
	v = append(v, validateEWayPincode("fromPincode", p.FromPincode)...)
	v = append(v, validateEWayPincode("toPincode", p.ToPincode)...)

	for field, val := range map[string]string{
		"fromTrdName":     p.FromTrdName,
		"toTrdName":       p.ToTrdName,
		"transporterName": p.TransporterName,
	} {
		if m := s.MaxLengths[field]; m > 0 && len(val) > m {
			v = append(v, fmt.Sprintf("%s: %d characters exceeds the maximum of %d", field, len(val), m))
		}
	}

	// The transporter id is 15 characters — a GSTIN or an enrolment id. Empty is
	// legal (the consignor may transport their own goods), so only a WRONG length
	// is a violation.
	if p.TransporterID != "" && len(p.TransporterID) != s.TransporterID.Length {
		v = append(v, fmt.Sprintf("transporterId: %d characters, want %d (a GSTIN or a transporter enrolment id)",
			len(p.TransporterID), s.TransporterID.Length))
	}

	// ⚠️ THE VEHICLE NUMBER IS OPTIONAL HERE AND MANDATORY IN Assess, and that is
	// not a contradiction worth resolving. This function validates SHAPE against a
	// recorded schema; Assess decides whether the RULE requires the field at all
	// (Part B). A schema validator that also enforced the statutory requirement
	// would be a second copy of the Part B logic, and the drifted copy always wins
	// for whichever caller reaches it first.
	if p.VehicleNo != "" {
		switch {
		case len(p.VehicleNo) < s.VehicleNo.Min || len(p.VehicleNo) > s.VehicleNo.Max:
			v = append(v, fmt.Sprintf("vehicleNo: %d characters, want %d-%d",
				len(p.VehicleNo), s.VehicleNo.Min, s.VehicleNo.Max))
		case !ewayVehicleRe.MatchString(p.VehicleNo):
			v = append(v, fmt.Sprintf("vehicleNo: %q does not match the registration pattern %s",
				p.VehicleNo, s.VehicleNo.Pattern))
		}
	}

	if len(p.ItemList) < s.ItemList.Min {
		v = append(v, fmt.Sprintf("itemList: %d items, minimum %d — a consignment with no goods is not a movement",
			len(p.ItemList), s.ItemList.Min))
	}
	if len(p.ItemList) > s.ItemList.Max {
		v = append(v, fmt.Sprintf("itemList: %d items exceeds the maximum of %d", len(p.ItemList), s.ItemList.Max))
	}
	for i, it := range p.ItemList {
		where := fmt.Sprintf("itemList[%d]", i)
		if it.ProductName == "" {
			v = append(v, where+".productName: missing")
		}
		if m := s.MaxLengths["hsnCode"]; m > 0 && len(it.HSNCode) > m {
			v = append(v, fmt.Sprintf("%s.hsnCode: %d characters exceeds the maximum of %d", where, len(it.HSNCode), m))
		}
		if msg := check2dp(where+".taxableAmount", it.TaxableAmount); msg != "" {
			v = append(v, msg)
		}
	}

	// Every money field at exactly two places, matching the e-invoice's rule and
	// for the same reason: NIC rejects trailing-zero ambiguity.
	for field, val := range map[string]string{
		"totalValue":  p.TotalValue,
		"cgstValue":   p.CgstValue,
		"sgstValue":   p.SgstValue,
		"igstValue":   p.IgstValue,
		"totInvValue": p.TotInvValue,
	} {
		if msg := check2dp(field, val); msg != "" {
			v = append(v, msg)
		}
	}

	// The distance is a STRING in the payload (NIC's shape) and a bounded integer
	// in the schema. "0" is out of range on purpose: a zero distance is the
	// unstated case, and Assess already refuses to compute a validity from it —
	// this is the second line of defence, so a payload that reached NIC could not
	// carry an unstated distance silently.
	if n, ok := atoiStrict(p.TransDistance); !ok {
		v = append(v, fmt.Sprintf("transDistance: %q is not a whole number of kilometres", p.TransDistance))
	} else if n < s.TransDistance.Min || n > s.TransDistance.Max {
		v = append(v, fmt.Sprintf(
			"transDistance: %d km is outside %d-%d. A zero means the distance was never stated, and the "+
				"validity period cannot be computed from it",
			n, s.TransDistance.Min, s.TransDistance.Max))
	}
	return v
}

func inSet(set []string, want string) bool {
	for _, s := range set {
		if s == want {
			return true
		}
	}
	return false
}

func validateEWayGSTIN(field, g string) []string {
	if g == "" {
		return []string{field + ": missing"}
	}
	if len(g) != loadedEWaySchema.GSTIN.Length {
		return []string{fmt.Sprintf("%s: %d characters, want %d", field, len(g), loadedEWaySchema.GSTIN.Length)}
	}
	if !ewayGstinRe.MatchString(g) {
		return []string{fmt.Sprintf("%s: %q does not match the GSTIN pattern", field, g)}
	}
	return nil
}

func validateEWayPincode(field, p string) []string {
	if p == "" {
		return []string{field + ": missing — NIC keys the route on pincodes, not on the party addresses"}
	}
	if !ewayPincodeRe.MatchString(p) {
		return []string{fmt.Sprintf("%s: %q is not a six-digit Indian pincode", field, p)}
	}
	return nil
}

func isNICDate(s string) bool {
	if len(s) != 10 || s[2] != '/' || s[5] != '/' {
		return false
	}
	for i, r := range s {
		if i == 2 || i == 5 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// atoiStrict parses a non-negative whole number, rejecting anything with a sign,
// a decimal point or padding. strconv.Atoi would accept "+5" and " 5" in some
// forms; a distance that parsed loosely is a validity period computed from a value
// NIC would have rejected.
func atoiStrict(s string) (int, bool) {
	if s == "" || strings.TrimSpace(s) != s {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// FakeEWayBillProvider stands in for NIC, which is unreachable. It mirrors
// FakeProvider (fake.go) and is deliberately named so no caller can mistake it.
type FakeEWayBillProvider struct{}

// Name is what reaches the artefact's disclosure block, so it says FAKE out loud.
func (FakeEWayBillProvider) Name() string { return "fake-nic-ewaybill" }

// GenerateEWayBill returns a DETERMINISTIC local reference — never an EBN.
//
// Deterministic so the golden test is a byte comparison, and PREFIXED so the value
// cannot be mistaken for the real thing at a glance: a real EBN is 12 digits, and
// this is visibly not 12 digits.
func (f FakeEWayBillProvider) GenerateEWayBill(p EWayBillPayload, a Assessment) EWayBillResult {
	return EWayBillResult{
		// ⛔ "LOCAL-" AND NOT A 12-DIGIT NUMBER. A real EBN is 12 digits; anything
		// shaped like one would be indistinguishable from a government identifier on
		// a printed sheet, which is the forgery this whole design avoids.
		LocalReference: "LOCAL-EWB-" + nonEmpty(p.DocNo, "NODOC") + "-" + nonEmpty(p.FromGstin[:min(len(p.FromGstin), 6)], "NOGST"),
		ValidUntil:     a.ValidUntil,
		Provider:       f.Name(),
		Disclosures:    EWayBillDisclosures(a, f.Name()),
	}
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
