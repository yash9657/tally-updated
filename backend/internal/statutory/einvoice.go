package statutory

import (
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/statutory/rules"
)

// The voucher-fact vocabulary lives ONE package down, in internal/statutory/rules,
// and is re-exported here BY ALIAS so this package's API — and therefore
// cmd/statutoryd and the CoredClient interface — is unchanged by the split.
//
// ⛔ THESE ARE ALIASES (`=`), NOT NEW DEFINED TYPES. A defined type would be a
// SECOND type with the same fields: rules.BuildEWayBill would refuse a
// statutory.VoucherSnapshot, and the compiler would push whoever hit that into
// writing a converter — which is a copy, which drifts. An alias is the same type
// under two names, so a snapshot assembled anywhere satisfies both builders.
//
// Why the vocabulary moved down rather than the rules moving up: see the split
// note at the top of rules/ewaybill.go. In one line — the e-way bill core is
// PURE (no ctx, no error, no clock, no socket, no store), so it never needed the
// separate deployable that the exactly-once IRN workflow genuinely does need.
type (
	// VoucherSnapshot is the input to BuildEInvoice. See rules.VoucherSnapshot.
	VoucherSnapshot = rules.VoucherSnapshot
	// SnapshotLine is one invoice line. See rules.SnapshotLine.
	SnapshotLine = rules.SnapshotLine
)

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
			UnitPrice:  rules.Fixed2(ln.UnitPrice),
			TotAmt:     rules.Fixed2(ln.Taxable),
			AssAmt:     rules.Fixed2(ln.Taxable),
			GstRt:      ln.GstRatePct,
			IgstAmt:    rules.Fixed2(ln.IGST),
			CgstAmt:    rules.Fixed2(ln.CGST),
			SgstAmt:    rules.Fixed2(ln.SGST),
			TotItemVal: rules.Fixed2(ln.LineTotal),
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
			AssVal:    rules.Fixed2(s.Taxable),
			CgstVal:   rules.Fixed2(s.CGST),
			SgstVal:   rules.Fixed2(s.SGST),
			IgstVal:   rules.Fixed2(s.IGST),
			TotInvVal: rules.Fixed2(total),
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
