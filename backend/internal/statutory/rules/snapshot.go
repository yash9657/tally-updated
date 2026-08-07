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
	Desc string
	HSN  string
	Qty  string
	Unit string
	// UnitPrice is the GROSS rate — what one unit costs BEFORE the line
	// discount. NIC's item block is an arithmetic statement about exactly that:
	// TotAmt = Qty x UnitPrice, and AssAmt = TotAmt - Discount.
	//
	// ⛔ IT IS NOT FORCED TO TWO DECIMALS, AND THAT IS THE FIX FOR A ₹500
	// DEFECT. It used to be, while Taxable carried the NET of the discount, so a
	// discounted line shipped Qty x UnitPrice = 5000.00 beside a declared
	// TotAmt/AssAmt of 4500.00 with no Discount anywhere — an e-invoice that
	// does not add up, minted and then frozen. The books hold NUMERIC(19,4), so
	// rounding the rate to the paisa HERE would make Qty x UnitPrice disagree
	// with the taxable value the books derived from the un-rounded rate, and the
	// residue would have to be dumped into Discount — sometimes as a NEGATIVE
	// discount, which NIC refuses. The rate therefore travels at the precision
	// the books hold it (see MoneyAtLeast2); NIC's own field is decimal(12,3)
	// and ValidateEInvoice refuses a fourth place rather than emitting one.
	UnitPrice string
	// Discount is the line discount in currency, NOT a percentage: exactly
	// TotAmt - Taxable. Blank is read as "0.00".
	//
	// The books record a discount PERCENT (stock_movement.discount_pct) and
	// derive the taxable value from it once, at four places
	// (ledger.InventoryLine.TaxableValue). Re-deriving a percentage here would
	// be a second discount engine and its first divergence would be an e-invoice
	// whose AssAmt is not the value the GSTR-1 was filed on — so the assembler
	// sends the DIFFERENCE it actually applied, and the identity closes exactly.
	Discount   string
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

// MoneyAtLeast2 formats a decimal string with AT LEAST two decimal places,
// keeping any further places the value actually carries. "500.0000" -> "500.00",
// "10.0050" -> "10.005", "" -> "0.00".
//
// ⛔ IT EXISTS FOR EXACTLY ONE FIELD: the item UnitPrice, which is a RATE and not
// a money total. Every total on an NIC document is fixed-2 and goes through
// Fixed2; a rate is not, because the document then asserts
// TotAmt = Qty x UnitPrice, and a rate rounded to the paisa makes that product
// disagree with the taxable value the books derived from the un-rounded rate.
// Truncating "10.005" to "10.01" and multiplying by 3 gives 30.03 against a book
// value of 30.02 — a paisa the document cannot account for, in a field where the
// only place to put it is a discount that was never given.
//
// It never strips a value BELOW two places, because NIC prints money to two and
// a bare "500" reads as a missing precision rather than a round number.
func MoneyAtLeast2(s string) string {
	if s == "" {
		return "0.00"
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return s
	}
	if d.Equal(d.Round(2)) {
		return d.StringFixed(2)
	}
	// String() drops trailing zeros, so a four-place book figure that only uses
	// three places prints as three.
	return d.String()
}
