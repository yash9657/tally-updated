package rules

// snapshot.go — THE VOUCHER FACTS BOTH STATUTORY ARTEFACTS ARE BUILT FROM.
//
// This type used to live in internal/statutory/einvoice.go. It moved DOWN here
// when the pure Rule 138 core was split out (see the package comment in
// ewaybill.go), because it is the one thing the two halves genuinely share: an
// e-invoice payload and an e-way bill payload are different documents assembled
// from the SAME invoice.
//
// ⛔ IT IS RE-EXPORTED FROM internal/statutory BY TYPE ALIAS, not copied. A
// second struct with these twelve fields would drift from this one the first time
// a field was added to either, and the drift would be invisible: both would
// compile, both would look authoritative, and the two statutory documents raised
// against one invoice would disagree about what the invoice said.

import "github.com/shopspring/decimal"

// VoucherSnapshot is the input to BuildEInvoice and (via ConsignmentSnapshot) to
// BuildEWayBill: the amount fields come from cored's voucher GET (fixed-4
// strings, re-formatted to fixed-2 here), while the seller/buyer identity is
// supplied by the trigger/caller (cored's ledger does not carry GSTINs/legal
// names). InvoiceTotal may be supplied or derived.
type VoucherSnapshot struct {
	VoucherNo   string
	VoucherDate string

	SellerGstin     string
	SellerLegalName string
	SellerStateCode string

	BuyerGstin     string
	BuyerLegalName string
	BuyerStateCode string

	Lines []SnapshotLine

	Taxable      string
	CGST         string
	SGST         string
	IGST         string
	InvoiceTotal string
}

// SnapshotLine is one invoice line. Amounts are strings (any precision in, 2dp
// out of the payload builders).
type SnapshotLine struct {
	Desc       string
	HSN        string
	Qty        string
	Unit       string
	UnitPrice  string
	Taxable    string
	GstRatePct string
	CGST       string
	SGST       string
	IGST       string
	LineTotal  string
}

// Fixed2 re-formats a decimal string to exactly two decimal places. A blank or
// unparseable input yields "0.00" so the payload is always well-formed; the
// validators independently reject genuinely malformed inputs upstream.
//
// EXPORTED (it was `fixed2`) because both payload builders now live on opposite
// sides of a package boundary and NIC requires exactly two decimal places in
// both documents. There is one implementation, deliberately: two would be two
// rounding rules, and the first divergence would show up as a statutory
// document that does not foot.
//
// ⛔ decimal.NewFromString, never ParseFloat — ParseFloat accepts "NaN" and
// "Inf" and silently rounds what the decimal package would have preserved.
func Fixed2(s string) string {
	if s == "" {
		return "0.00"
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return s
	}
	return d.StringFixed(2)
}
