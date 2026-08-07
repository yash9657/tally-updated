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
		// ⛔⛔ TotAmt IS DERIVED, NOT COPIED FROM Taxable. NIC's item block says
		// AssAmt = TotAmt - Discount, so the gross is the taxable value PLUS the
		// discount that was taken off it. Setting TotAmt = AssAmt (what this used
		// to do) is only correct when Discount is zero, and it silently asserted
		// that a 10%-discounted line had no discount and a gross of 4500.00 for
		// ten units priced at 500.00.
		//
		// Deriving it here rather than carrying a fourth amount on SnapshotLine
		// keeps ONE of the three figures free: the assembler sends the taxable
		// value (which must equal the books, because GSTR-1 is filed from the
		// same number) and the discount it applied, and the gross follows. The
		// OTHER half of the identity — TotAmt = Qty x UnitPrice — is not derivable
		// here and is what ValidateEInvoice independently checks.
		discount := rules.Fixed2(ln.Discount)
		assAmt := rules.Fixed2(ln.Taxable)
		items = append(items, EInvoiceItem{
			SlNo:      itoa(i + 1),
			PrdDesc:   ln.Desc,
			HsnCd:     ln.HSN,
			Qty:       ln.Qty,
			Unit:      ln.Unit,
			UnitPrice: rules.MoneyAtLeast2(ln.UnitPrice),
			TotAmt:    addMoney(assAmt, discount),
			Discount:  discount,
			AssAmt:    assAmt,

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
		// ⛔⛔ Typ IS HARDCODED "INV", AND THAT IS ONLY SAFE BECAUSE THE ASSEMBLER
		// REFUSES EVERY OTHER DOCUMENT.
		//
		// The real IRP derives IRN = hash(SupplierGSTIN, DocTyp, DocNo, FY). This
		// codebase numbers vouchers PER CLASS (voucher_number_counter is keyed on
		// (company, voucher_type, fin_year)), so Sales #1, Purchase #1 and Credit
		// Note #1 all exist at once — and all three would leave here as
		// INV/"1"/same FY, i.e. ONE NIC key. The second submission is rejected as a
		// DUPLICATE by NIC, and FakeProvider cannot show it because it hashes the
		// whole payload and therefore mints three distinct IRNs for three distinct
		// documents.
		//
		// The classes that would collide are refused at the source, by name:
		// internal/einvoice's loadInvoice accepts SALES and nothing else (see
		// ErrNotAnOutwardSalesInvoice). A credit note IS legitimately e-invoiceable
		// in India as CRN and a debit note as DBN — the recorded schema already
		// lists all three doc types — but emitting either means modelling the
		// original-invoice reference block NIC requires with them, which this build
		// does not. ⛔ IF YOU EVER LET A NON-SALES CLASS THROUGH loadInvoice, THIS
		// LINE IS THE SECOND HALF OF THE BUG: set the doc type from the class here,
		// or the document lies about what it is AND collides at the IRP.
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

// addMoney sums two fixed-2 money strings and prints the result at two places.
// decimal, never float64 — a float sum of "0.10" and "0.20" is how a paisa goes
// missing inside a document carrying a government reference number.
func addMoney(a, b string) string { return dec(a).Add(dec(b)).StringFixed(2) }

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
