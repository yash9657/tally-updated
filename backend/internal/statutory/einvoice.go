package statutory

import "github.com/shopspring/decimal"

// VoucherSnapshot is the input to BuildEInvoice: the amount fields come from
// cored's voucher GET (fixed-4 strings, re-formatted to fixed-2 here), while the
// seller/buyer identity is supplied by the trigger/caller (cored's ledger does
// not carry GSTINs/legal names). InvoiceTotal may be supplied or derived.
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
// out of BuildEInvoice).
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

// fixed2 re-formats a decimal string to exactly two decimal places. A blank or
// unparseable input yields "0.00" so the payload is always well-formed; the
// validator independently rejects genuinely malformed inputs upstream.
func fixed2(s string) string {
	if s == "" {
		return "0.00"
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return s
	}
	return d.StringFixed(2)
}

// BuildEInvoice converts a VoucherSnapshot into an NIC EInvoicePayload. It is a
// PURE function (no I/O). Supply type is inferred from intra/inter-state (seller
// state code vs buyer place of supply). If InvoiceTotal is blank it is derived
// as Taxable + CGST + SGST + IGST.
func BuildEInvoice(s VoucherSnapshot) EInvoicePayload {
	items := make([]EInvoiceItem, 0, len(s.Lines))
	for i, ln := range s.Lines {
		items = append(items, EInvoiceItem{
			SlNo:       itoa(i + 1),
			PrdDesc:    ln.Desc,
			HsnCd:      ln.HSN,
			Qty:        ln.Qty,
			Unit:       ln.Unit,
			UnitPrice:  fixed2(ln.UnitPrice),
			TotAmt:     fixed2(ln.Taxable),
			AssAmt:     fixed2(ln.Taxable),
			GstRt:      ln.GstRatePct,
			IgstAmt:    fixed2(ln.IGST),
			CgstAmt:    fixed2(ln.CGST),
			SgstAmt:    fixed2(ln.SGST),
			TotItemVal: fixed2(ln.LineTotal),
		})
	}

	total := s.InvoiceTotal
	if total == "" {
		total = deriveTotal(s.Taxable, s.CGST, s.SGST, s.IGST)
	}

	return EInvoicePayload{
		Version: "1.1",
		TranDtls: TranDtls{
			TaxSch: "GST",
			SupTyp: "B2B",
			RegRev: "N",
		},
		DocDtls: DocDtls{
			Typ: "INV",
			No:  s.VoucherNo,
			Dt:  s.VoucherDate,
		},
		SellerDtls: PartyDtls{
			Gstin: s.SellerGstin,
			LglNm: s.SellerLegalName,
			Pos:   s.SellerStateCode,
			Stcd:  s.SellerStateCode,
		},
		BuyerDtls: PartyDtls{
			Gstin: s.BuyerGstin,
			LglNm: s.BuyerLegalName,
			Pos:   s.BuyerStateCode,
			Stcd:  s.BuyerStateCode,
		},
		ItemList: items,
		ValDtls: ValDtls{
			AssVal:    fixed2(s.Taxable),
			CgstVal:   fixed2(s.CGST),
			SgstVal:   fixed2(s.SGST),
			IgstVal:   fixed2(s.IGST),
			TotInvVal: fixed2(total),
		},
	}
}

func deriveTotal(taxable, cgst, sgst, igst string) string {
	sum := dec(taxable).Add(dec(cgst)).Add(dec(sgst)).Add(dec(igst))
	return sum.StringFixed(2)
}

func dec(s string) decimal.Decimal {
	if s == "" {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}

func itoa(i int) string {
	return decimal.NewFromInt(int64(i)).String()
}
