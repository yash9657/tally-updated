package printingdb_test

// document_notes_test.go — WHAT A CREDIT OR DEBIT NOTE SAYS IT ADJUSTS.
//
// Rule 53(1A) makes one thing mandatory on a note that nothing else on the page
// can stand in for: the NUMBER AND DATE of the document being adjusted. Both
// register rows below were failures of exactly that line, in opposite
// directions:
//
//	D31 — a DEBIT NOTE could never be printed at all. The against-number was read
//	      only from voucher_version.supplier_invoice_no on the NOTE, a column no
//	      HTTP request populates, so the field came out empty and the renderer
//	      refused every debit note in the product with a 422.
//	§4.13 — a CREDIT NOTE printed the bill REFERENCE where the invoice number
//	      belongs. The reference is what LOCATES the bill in our books; since bill
//	      names were made unique an unnamed bill is opened as "Sales/2026-27/7",
//	      so the one line the customer uses to match a credit to a supply carried
//	      an internal name that appears on no invoice they hold.
//
// ⛔ THE ORACLE IS THE PAPER, NOT THE STRUCT. Every case below renders a real PDF
// and EXTRACTS ITS TEXT, then asserts the document's own number and the
// against-invoice line are on it. A struct comparison would have passed
// throughout the period both defects were live: the assembly was producing a
// value, it was producing the wrong one, and the renderer's refusal was the only
// thing that noticed.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/printing"
)

const (
	docNameSupplier = "Kolhapur Forgings"
	docAddrSupplier = "Gat 221, Shiroli MIDC\nKolhapur"
	docNamePurchase = "Raw Material Purchases"
)

// newSupplier plants a party under Sundry Creditors with a full address — the
// only party a purchase, and therefore a debit note, can be raised on.
func (e *docEnv) newSupplier(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := masters.NewService(e.pool).CreateLedger(e.ctx, masters.Ledger{
		Name: docNameSupplier, GroupName: "Sundry Creditors",
		GSTIN: docAwayGSTIN, StateCode: docStateAway,
		RegistrationType: masters.GSTRegular, MaintainBillByBill: true,
		Address: docAddrSupplier, City: "Kolhapur", PIN: "416122",
	}, "fixture")
	if err != nil {
		t.Fatalf("create the supplier a debit note is raised on: %v", err)
	}
	return id
}

// newPurchaseLedger plants the expense head a purchase posts to.
func (e *docEnv) newPurchaseLedger(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := masters.NewService(e.pool).CreateLedger(e.ctx,
		masters.Ledger{Name: docNamePurchase, GroupName: "Purchase Accounts"}, "fixture")
	if err != nil {
		t.Fatalf("create the purchase ledger: %v", err)
	}
	return id
}

// storedVoucherNo reads a voucher's OWN number back with an independent query.
//
// Read back rather than asserted against a literal, deliberately: the number is
// assigned by the server's counter inside the post, so a literal here would be
// this test agreeing with itself about what the counter did.
func (e *docEnv) storedVoucherNo(t *testing.T, voucherID uuid.UUID) string {
	t.Helper()
	var no string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(vv.voucher_no, '')
			   FROM voucher_head vh
			   JOIN voucher_version vv
			     ON vv.company_id = vh.company_id AND vv.id = vh.current_version_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2`,
			e.company, voucherID).Scan(&no)
	}); err != nil {
		t.Fatalf("read the voucher's own number back: %v", err)
	}
	if no == "" {
		t.Fatal("the voucher was posted without a number, so there is nothing for a note to cite")
	}
	return no
}

// storedBillRef reads the name the server GAVE the bill this voucher opened.
//
// Also read back: the whole point of the credit-note case is that this name is
// NOT the voucher number and is invented by the server, so a fixture that
// spelled it would be asserting the defect's own assumption.
func (e *docEnv) storedBillRef(t *testing.T, voucherID, ledgerID uuid.UUID) string {
	t.Helper()
	var ref string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT ba.ref_name
			   FROM bill_allocation ba
			   JOIN voucher_head vh
			     ON vh.company_id = ba.company_id AND vh.current_version_id = ba.voucher_version_id
			  WHERE ba.company_id = $1 AND vh.voucher_id = $2 AND ba.ledger_id = $3
			    AND ba.ref_type = 'NEW'`,
			e.company, voucherID, ledgerID).Scan(&ref)
	}); err != nil {
		t.Fatalf("read the generated bill reference back: %v", err)
	}
	return ref
}

// againstLine returns the "Against Invoice No.: … dated …" line off the rendered
// page, which is the ONE line either register row is about.
//
// It is located rather than searched for as a substring anywhere on the page,
// because the bill reference legitimately appears elsewhere — the ledger-leg
// table has an "Against Bill" column — and a whole-page `Contains` would let the
// defect pass on the strength of the right string in the wrong place.
func againstLine(t *testing.T, pdf []byte, kind printing.DocKind) string {
	t.Helper()
	if !strings.HasPrefix(string(pdf), "%PDF") ||
		!strings.HasSuffix(strings.TrimRight(string(pdf), "\r\n"), "%%EOF") {
		t.Fatalf("the rendered %s is not a PDF file: it does not begin %%PDF and end %%%%EOF", kind)
	}
	text, err := printing.ExtractText(pdf)
	if err != nil {
		t.Fatalf("the printed %s is not a readable PDF, so nobody can open the document that "+
			"was issued: %v", kind, err)
	}
	for _, line := range text {
		if strings.HasPrefix(line, "Against Invoice No.:") {
			return line
		}
	}
	t.Fatalf("the printed %s carries no \"Against Invoice No.\" line at all. The whole page:\n%s",
		kind, strings.Join(text, "\n"))
	return ""
}

// pageText is the whole document's words, for the assertions that are about the
// note naming ITSELF.
func pageText(t *testing.T, pdf []byte) []string {
	t.Helper()
	text, err := printing.ExtractText(pdf)
	if err != nil {
		t.Fatalf("the printed note is not a readable PDF: %v", err)
	}
	return text
}

func containsLine(text []string, want string) bool {
	for _, l := range text {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// §4.13 — the CREDIT note cites an internal bill name as an invoice number
// ---------------------------------------------------------------------------

// TestACreditNoteCitesOurInternalBillNameAsTheCustomersInvoiceNumber is named
// for the breach.
//
// The invoice below opens its bill WITHOUT naming it, which is what the F8 form
// sends for virtually every invoice — so the server invents the reference, and
// since bill names were made unique it invents "Sales/2026-27/<n>". That string
// is on no document the customer holds. What they hold is an invoice whose face
// carries the voucher number, and that is what a note adjusting it has to say.
func TestACreditNoteCitesOurInternalBillNameAsTheCustomersInvoiceNumber(t *testing.T) {
	e := newDocEnv(t)
	const invoiceDate = "2026-05-24"

	// An ITEM invoice, not a plain double entry, because this case renders BOTH
	// papers and compares them: a sale keyed as bare postings records no HSN
	// anywhere, and Rule 49(e) makes the invoice unprintable — so the note would
	// have nothing to be checked against. Same fixture shape as
	// postMixedRateItemInvoice: 10 bolts @ 18% + 5 greases @ 12% = 1,740.00.
	invoice, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Sales,
		PartyLedgerID: e.customer,
		ItemLedgerID:  e.sales,
		VoucherDate:   invoiceDate,
		Party:         docNameCustomer,
		CreatedBy:     "fixture",
		Inventory: []ledger.InventoryLine{
			e.itemLine(e.bolt, "10", "100", "0"),
			e.itemLine(e.grease, "5", "100", "0"),
		},
		GST: &ledger.GSTParams{
			SupplierStateCode:      docStateHome,
			PlaceOfSupplyStateCode: docStateHome,
			CGSTLedgerID:           e.ocgst,
			SGSTLedgerID:           e.osgst,
			IGSTLedgerID:           e.oigst,
		},
		// UNNAMED, on purpose: this is the shape the client sends, and the shape
		// that makes the server invent "Sales/<fy>/<n>".
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: e.customer, RefType: ledger.RefNew,
			DrCr: ledger.Debit, Amount: decimal.RequireFromString("1740"),
		}},
	})
	if err != nil {
		t.Fatalf("post the invoice the note adjusts: %v", err)
	}

	invoiceNo := e.storedVoucherNo(t, invoice.VoucherID)
	billRef := e.storedBillRef(t, invoice.VoucherID, e.customer)
	// The premise of the whole row. If the server ever goes back to naming a
	// generated bill after the voucher number, this test still passes but proves
	// nothing — so say so rather than let it rot into a tautology.
	if billRef == invoiceNo {
		t.Fatalf("the generated bill reference is %q, the same as the voucher number: this case "+
			"no longer distinguishes the reference from the number and cannot detect the defect",
			billRef)
	}

	note, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.CreditNote,
		PartyLedgerID: e.customer,
		VoucherDate:   "2026-06-02",
		Party:         docNameCustomer,
		Narration:     "Sales return: 4 bolts rejected at the customer's incoming inspection.",
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: e.sales, DrCr: ledger.Debit, Amount: decimal.RequireFromString("400")},
			{LedgerID: e.customer, DrCr: ledger.Credit, Amount: decimal.RequireFromString("400")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: e.customer, RefName: billRef, RefType: ledger.RefAgainst,
			DrCr: ledger.Credit, Amount: decimal.RequireFromString("400"),
		}},
	})
	if err != nil {
		t.Fatalf("post the credit note: %v", err)
	}

	doc, err := e.store.LoadVoucherDoc(context.Background(), e.company, note.VoucherID)
	if err != nil {
		t.Fatalf("assemble the credit note: %v", err)
	}
	if doc.AgainstNumber != invoiceNo {
		t.Errorf("the note says it adjusts invoice %q; the invoice it is allocated against is "+
			"numbered %q and is named %q in our own books. The customer is holding a document "+
			"with %q printed on its face and has no way to know %q refers to it",
			doc.AgainstNumber, invoiceNo, billRef, invoiceNo, doc.AgainstNumber)
	}
	if doc.AgainstDate == nil || doc.AgainstDate.Format("2006-01-02") != invoiceDate {
		t.Fatalf("the note dates the original invoice %v, but that bill was opened by the "+
			"voucher dated %s", doc.AgainstDate, invoiceDate)
	}

	// ---- THE PAPER.
	pdf, err := printing.RenderVoucherDoc(doc)
	if err != nil {
		t.Fatalf("the assembled credit note was REFUSED by the renderer, so no credit note can "+
			"be handed to a customer at all: %v", err)
	}
	line := againstLine(t, pdf, doc.Kind)
	if !strings.Contains(line, invoiceNo) {
		t.Errorf("the printed line reads %q and does not carry the invoice number %q", line, invoiceNo)
	}
	if strings.Contains(line, billRef) {
		t.Errorf("the printed line reads %q: it names our internal bill reference %q where the "+
			"customer looks for the invoice number they hold", line, billRef)
	}
	// AND THE NOTE NAMES ITSELF. A document that cites another one correctly and
	// does not say what IT is cannot be filed against anything.
	text := pageText(t, pdf)
	noteNo := e.storedVoucherNo(t, note.VoucherID)
	if !containsLine(text, noteNo) {
		t.Errorf("the printed credit note does not carry its OWN number %q anywhere on the page. "+
			"The whole page:\n%s", noteNo, strings.Join(text, "\n"))
	}

	// ⛔⛔ THE ORACLE THAT CANNOT BE SATISFIED BY AGREEING WITH ITSELF: the two
	// PAPERS are compared. Everything above compares the note to a value read out
	// of the database, which is the same place the assembly read it from — so a
	// wrong column consistently used would pass. This renders the INVOICE as well
	// and requires that the string after "Against Invoice No.:" on the note is the
	// string after "Invoice No.:" on the invoice. That is the property the
	// customer actually needs, and it holds no matter what either number is.
	inv, err := e.store.LoadInvoice(context.Background(), e.company, invoice.VoucherID)
	if err != nil {
		t.Fatalf("assemble the invoice the note adjusts: %v", err)
	}
	invPDF, err := printing.RenderInvoice(inv)
	if err != nil {
		t.Fatalf("render the invoice the note adjusts: %v", err)
	}
	var invoiceFace string
	for _, l := range pageText(t, invPDF) {
		if strings.HasPrefix(l, "Invoice No.:") {
			invoiceFace = strings.TrimSpace(strings.TrimPrefix(l, "Invoice No.:"))
			break
		}
	}
	if invoiceFace == "" {
		t.Fatal("the printed invoice carries no \"Invoice No.\" line, so there is nothing for the note to agree with")
	}
	// ⚠️ EQUALITY, NEVER `Contains`, AND A MUTATION PROOF IS WHY. Voucher numbers
	// in this build are bare integers, so "1" is a substring of "Sales/2026-27/1"
	// — a Contains check on the whole line stayed GREEN under a deliberate
	// reversion of the fix, i.e. it could not see the very defect it was written
	// for. The number is cut out of the line and compared whole.
	noteCites := strings.TrimSpace(strings.SplitN(
		strings.TrimPrefix(line, "Against Invoice No.:"), "   dated ", 2)[0])
	if noteCites != invoiceFace {
		t.Errorf("the invoice's own face reads %q and the note cites %q: the two documents name "+
			"the same supply by two different strings, and the recipient has to reconcile them "+
			"by hand", invoiceFace, noteCites)
	}
}

// ---------------------------------------------------------------------------
// D31 — no debit note in this product could be printed at all
// ---------------------------------------------------------------------------

// TestNoDebitNoteInThisProductCanBePrinted is named for the breach.
//
// A debit note is raised ON A SUPPLIER, so what it adjusts is the SUPPLIER'S
// document. The columns migration 0032 added for that identity
// (supplier_invoice_no/date) are populated by internal/ledger ONLY from a
// purchase's `inward` block, and NOTHING ON THE WIRE SETS THAT BLOCK — no
// handler in internal/server ever constructs a ledger.InwardDocument. So on
// every debit note the product can create, the column is empty; the
// against-number came out ""; and printing.validateVoucherDoc refused the
// document. Every debit note answered 422.
//
// ⛔ AND THE FALLBACK MUST NOT BE THE OPENING VOUCHER'S NUMBER. That voucher is
// our PURCHASE, and our purchase number is a string the supplier has never seen —
// a note citing it names a document that does not exist on the side that has to
// act on it. What the supplier can match is the reference the operator keyed off
// their invoice.
func TestNoDebitNoteInThisProductCanBePrinted(t *testing.T) {
	e := newDocEnv(t)
	supplier := e.newSupplier(t)
	purchases := e.newPurchaseLedger(t)
	const (
		supplierBill = "SUP-101"
		purchaseDate = "2026-05-18"
	)

	// The purchase, exactly as an HTTP request produces one: a bill named off the
	// supplier's own invoice, and NO inward block, because nothing can send one.
	purchase, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Purchase,
		PartyLedgerID: supplier,
		VoucherDate:   purchaseDate,
		Party:         docNameSupplier,
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: purchases, DrCr: ledger.Debit, Amount: decimal.RequireFromString("2400")},
			{LedgerID: supplier, DrCr: ledger.Credit, Amount: decimal.RequireFromString("2400")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: supplier, RefName: supplierBill, RefType: ledger.RefNew,
			DrCr: ledger.Credit, Amount: decimal.RequireFromString("2400"),
		}},
	})
	if err != nil {
		t.Fatalf("post the purchase the note adjusts: %v", err)
	}
	purchaseNo := e.storedVoucherNo(t, purchase.VoucherID)

	note, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.DebitNote,
		PartyLedgerID: supplier,
		VoucherDate:   "2026-06-05",
		Party:         docNameSupplier,
		Narration:     "Purchase return: 6 forgings outside the drawing tolerance.",
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: supplier, DrCr: ledger.Debit, Amount: decimal.RequireFromString("600")},
			{LedgerID: purchases, DrCr: ledger.Credit, Amount: decimal.RequireFromString("600")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: supplier, RefName: supplierBill, RefType: ledger.RefAgainst,
			DrCr: ledger.Debit, Amount: decimal.RequireFromString("600"),
		}},
	})
	if err != nil {
		t.Fatalf("post the debit note: %v", err)
	}

	doc, err := e.store.LoadVoucherDoc(context.Background(), e.company, note.VoucherID)
	if err != nil {
		t.Fatalf("assemble the debit note: %v", err)
	}
	if doc.Kind != printing.KindDebitNote {
		t.Fatalf("a Debit Note voucher was assembled as %q", doc.Kind)
	}
	if doc.AgainstNumber != supplierBill {
		t.Errorf("the note says it adjusts %q; the supplier's bill it settles is %q", doc.AgainstNumber, supplierBill)
	}
	if doc.AgainstNumber == purchaseNo {
		t.Errorf("the note cites OUR purchase number %q as the supplier's document: the supplier "+
			"has never seen that string and cannot match the debit to any invoice they issued", purchaseNo)
	}
	if doc.AgainstDate == nil || doc.AgainstDate.Format("2006-01-02") != purchaseDate {
		t.Fatalf("the note dates the supplier's bill %v, but that bill was opened by the purchase "+
			"dated %s", doc.AgainstDate, purchaseDate)
	}

	// ---- THE PAPER. This is the assertion the register row is actually about:
	// the whole class answered 422 here.
	pdf, err := printing.RenderVoucherDoc(doc)
	if err != nil {
		t.Fatalf("the assembled debit note was REFUSED by the renderer, so NO debit note in this "+
			"product can be printed at all: %v", err)
	}
	line := againstLine(t, pdf, doc.Kind)
	if !strings.Contains(line, supplierBill) {
		t.Errorf("the printed line reads %q and does not name the supplier's bill %q", line, supplierBill)
	}
	// FormatDate's rendering of the purchase date, so the assertion is about what
	// a reader sees and not about an ISO string nobody prints.
	if want := printing.FormatDate(*doc.AgainstDate); !strings.Contains(line, want) {
		t.Errorf("the printed line reads %q and does not carry the date %q; a serial number "+
			"repeats every financial year, so the number alone identifies nothing", line, want)
	}
	text := pageText(t, pdf)
	noteNo := e.storedVoucherNo(t, note.VoucherID)
	if !containsLine(text, noteNo) {
		t.Errorf("the printed debit note does not carry its OWN number %q anywhere on the page. "+
			"The whole page:\n%s", noteNo, strings.Join(text, "\n"))
	}
	if !containsLine(text, docNameSupplier) {
		t.Errorf("the printed debit note does not name the supplier it is raised on. The whole "+
			"page:\n%s", strings.Join(text, "\n"))
	}
}

// TestADebitNotePrefersTheSuppliersOwnDocumentNumberWhenThePurchaseRecordedOne
// is the OTHER half of the fallback order, and it is not hypothetical: the
// inward block exists in internal/ledger, is hashed into the canonical payload
// and is written to voucher_version — it is only the WIRE that cannot send one
// yet. The day it can, the note must cite the supplier's own invoice number in
// preference to the reference an operator typed, because that is the string on
// the document the supplier will look for.
func TestADebitNotePrefersTheSuppliersOwnDocumentNumberWhenThePurchaseRecordedOne(t *testing.T) {
	e := newDocEnv(t)
	supplier := e.newSupplier(t)
	purchases := e.newPurchaseLedger(t)
	const (
		keyedRef       = "KF/partial-2"
		supplierDocNo  = "KF/2026-27/0184"
		supplierDocDay = "2026-05-02"
		purchaseDate   = "2026-05-18"
	)

	purchase, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.Purchase,
		PartyLedgerID: supplier,
		VoucherDate:   purchaseDate,
		Party:         docNameSupplier,
		CreatedBy:     "fixture",
		Inward: &ledger.InwardDocument{
			SupplierInvoiceNo:   supplierDocNo,
			SupplierInvoiceDate: supplierDocDay,
		},
		Postings: []ledger.PostingInput{
			{LedgerID: purchases, DrCr: ledger.Debit, Amount: decimal.RequireFromString("2400")},
			{LedgerID: supplier, DrCr: ledger.Credit, Amount: decimal.RequireFromString("2400")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: supplier, RefName: keyedRef, RefType: ledger.RefNew,
			DrCr: ledger.Credit, Amount: decimal.RequireFromString("2400"),
		}},
	})
	if err != nil {
		t.Fatalf("post the purchase carrying the supplier's document identity: %v", err)
	}
	_ = purchase

	note, err := e.led.PostVoucher(e.ctx, ledger.VoucherInput{
		Class:         ledger.DebitNote,
		PartyLedgerID: supplier,
		VoucherDate:   "2026-06-05",
		Party:         docNameSupplier,
		Narration:     "Rate correction agreed with the supplier.",
		CreatedBy:     "fixture",
		Postings: []ledger.PostingInput{
			{LedgerID: supplier, DrCr: ledger.Debit, Amount: decimal.RequireFromString("600")},
			{LedgerID: purchases, DrCr: ledger.Credit, Amount: decimal.RequireFromString("600")},
		},
		BillAllocations: []ledger.BillAllocationInput{{
			LedgerID: supplier, RefName: keyedRef, RefType: ledger.RefAgainst,
			DrCr: ledger.Debit, Amount: decimal.RequireFromString("600"),
		}},
	})
	if err != nil {
		t.Fatalf("post the debit note: %v", err)
	}

	doc, err := e.store.LoadVoucherDoc(context.Background(), e.company, note.VoucherID)
	if err != nil {
		t.Fatalf("assemble the debit note: %v", err)
	}
	if doc.AgainstNumber != supplierDocNo {
		t.Errorf("the note cites %q; the purchase records the supplier's own invoice as %q, and "+
			"that is the number printed on the document the supplier will look for", doc.AgainstNumber, supplierDocNo)
	}
	if doc.AgainstDate == nil || doc.AgainstDate.Format("2006-01-02") != supplierDocDay {
		t.Errorf("the note dates the supplier's invoice %v, but the purchase records it as issued "+
			"on %s; dating it from OUR booking date would put a date on the paper the supplier's "+
			"invoice does not carry", doc.AgainstDate, supplierDocDay)
	}

	pdf, err := printing.RenderVoucherDoc(doc)
	if err != nil {
		t.Fatalf("the debit note was refused by the renderer: %v", err)
	}
	line := againstLine(t, pdf, doc.Kind)
	if !strings.Contains(line, supplierDocNo) {
		t.Errorf("the printed line reads %q and does not carry the supplier's invoice number %q", line, supplierDocNo)
	}
	if strings.Contains(line, keyedRef) {
		t.Errorf("the printed line reads %q: it names our own bill reference %q where the "+
			"supplier's invoice number belongs", line, keyedRef)
	}
}
