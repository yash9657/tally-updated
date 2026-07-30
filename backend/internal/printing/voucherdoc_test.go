package printing

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// This file is the golden-test suite for the five non-invoice documents.
//
// HOW A GOLDEN TEST HERE IS BUILT, AND WHY IT IS BUILT THAT WAY.
// Each kind is asserted TWICE over the same render:
//
//  1. THE BYTES, against a committed file in testdata/. This catches everything —
//     a moved column, a lost rule, a changed font — and it is only meaningful
//     because Doc.Bytes is deterministic (no timestamp, no /ID, no compression;
//     see the determinism contract on Doc).
//  2. THE EXTRACTED TEXT, naming the fields that MATTER on that document: the
//     bill references on a receipt, the original invoice on a note, the ABSENCE
//     of tax columns on a delivery note. A byte diff says "byte 4471 differs"; the
//     text assertion says which fact went missing from the paper, which is the
//     thing a reviewer actually needs to know.
//
// A screenshot assertion was never an option: it cannot tell a right total from a
// wrong one.
//
// REGENERATING THE GOLDEN FILES
//
//	cd backend && PRINTING_UPDATE_VOUCHER_GOLDEN=1 go test ./internal/printing/ -run TestAVoucher -count=1
//
// then READ THE DIFF (the streams are uncompressed, so `git diff` on the .pdf is
// legible) and commit it in the same change as the code that moved it. An
// environment variable rather than a -flag on purpose: every _test.go file in this
// package shares one flag set, and a second file registering the same flag name
// panics the whole binary before a single test runs.
const voucherGoldenUpdateEnv = "PRINTING_UPDATE_VOUCHER_GOLDEN"

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

func vdTestDate(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func vdMoney(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// vdTestSupplier is the issuing business on every fixture. The accented trading
// name is deliberate: it is representable in WinAnsi, so it must survive to the
// paper rather than becoming a substitution.
func vdTestSupplier() Company {
	return Company{
		Name:      "Mehta Trading Co.",
		Address1:  "14, Kalbadevi Road",
		Address2:  "Opp. Café Excelsior",
		City:      "Mumbai",
		State:     "Maharashtra",
		StateCode: "27",
		PIN:       "400002",
		GSTIN:     "27AAAPZ1234C1ZS",
		PAN:       "AAAPZ1234C",
		Phone:     "022 2201 4455",
		Email:     "accounts@mehtatrading.example",
	}
}

func vdTestCustomer() Party {
	return Party{
		Name:      "Kulkarni Hardware Stores",
		Address1:  "Shop 3, Laxmi Niwas",
		Address2:  "Tilak Road",
		City:      "Pune",
		State:     "Maharashtra",
		StateCode: "27",
		PIN:       "411030",
		GSTIN:     "27AACCK5678M1Z9",
	}
}

// vdDeliveryNoteFixture is the plain delivery note: quantities only, no value at
// all, which is how goods move against a later invoice.
func vdDeliveryNoteFixture() VoucherDoc {
	return VoucherDoc{
		Kind:     KindDeliveryNote,
		Number:   "DN/2026-27/0041",
		Date:     vdTestDate(2026, time.June, 12),
		Supplier: vdTestSupplier(),
		Party:    vdPtrParty(vdTestCustomer()),
		Items: []LineItem{
			{SerialNo: 1, Description: `Galvanised Iron Pipe 25mm (Class "B") \ ISI marked`, HSN: "73063090", Quantity: vdMoney("120"), Unit: "Nos"},
			{SerialNo: 2, Description: "Mild Steel Angle 50x50x6", HSN: "72161000", Quantity: vdMoney("18.5"), Unit: "MT"},
			{SerialNo: 3, Description: "Brass Ball Valve 15mm", HSN: "84818030", Quantity: vdMoney("400"), Unit: "Nos"},
		},
		Narration: "Despatched by Sharma Roadways, LR 88213, vehicle MH-12-AB-4477.",
	}
}

// vdReceiptFixture settles two named bills and carries one bank leg that is
// against nothing.
func vdReceiptFixture() VoucherDoc {
	return VoucherDoc{
		Kind:     KindReceipt,
		Number:   "RCT/2026-27/0117",
		Date:     vdTestDate(2026, time.June, 30),
		Supplier: vdTestSupplier(),
		Party:    vdPtrParty(vdTestCustomer()),
		Lines: []VoucherLine{
			{Ledger: "Bank of Baroda - Current A/c 0012", IsDebit: true, Amount: vdMoney("50000.00"), Narration: "NEFT ref BARBN26061800417"},
			{Ledger: "Kulkarni Hardware Stores", Amount: vdMoney("31450.00"), BillRef: "INV/2026-27/0308"},
			{Ledger: "Kulkarni Hardware Stores", Amount: vdMoney("18550.00"), BillRef: "INV/2026-27/0331"},
		},
		Total:     vdMoney("50000.00"),
		Narration: "Received against outstanding bills as allocated above.",
	}
}

func vdPaymentFixture() VoucherDoc {
	supplier := Party{
		Name:      "Deshpande Steel Traders",
		Address1:  "Plot 22, MIDC Bhosari",
		City:      "Pune",
		State:     "Maharashtra",
		StateCode: "27",
		PIN:       "411026",
		GSTIN:     "27AABCD9012F1ZK",
	}
	return VoucherDoc{
		Kind:     KindPayment,
		Number:   "PMT/2026-27/0064",
		Date:     vdTestDate(2026, time.July, 4),
		Supplier: vdTestSupplier(),
		Party:    vdPtrParty(supplier),
		Lines: []VoucherLine{
			{Ledger: "Deshpande Steel Traders", IsDebit: true, Amount: vdMoney("212500.00"), BillRef: "DST/PUR/2026/1195"},
			{Ledger: "Deshpande Steel Traders", IsDebit: true, Amount: vdMoney("62500.00"), BillRef: "DST/PUR/2026/1207"},
			{Ledger: "HDFC Bank - CC A/c 4409", Amount: vdMoney("275000.00"), Narration: "RTGS UTR HDFCR52026070400931"},
		},
		Total: vdMoney("275000.00"),
	}
}

// vdCreditNoteFixture is a sales return: it names the invoice, says why, and shows
// the tax it reverses head by head.
func vdCreditNoteFixture() VoucherDoc {
	against := vdTestDate(2026, time.May, 21)
	return VoucherDoc{
		Kind:     KindCreditNote,
		Number:   "CN/2026-27/0009",
		Date:     vdTestDate(2026, time.June, 18),
		Supplier: vdTestSupplier(),
		Party:    vdPtrParty(vdTestCustomer()),
		Items: []LineItem{
			{SerialNo: 1, Description: "Brass Ball Valve 15mm - returned, threads out of gauge", HSN: "84818030", Quantity: vdMoney("40"), Unit: "Nos", Rate: vdMoney("310.00"), Amount: vdMoney("12400.00")},
		},
		Taxes: []TaxHead{
			{Name: "CGST", Rate: vdMoney("9"), Amount: vdMoney("1116.00")},
			{Name: "SGST", Rate: vdMoney("9"), Amount: vdMoney("1116.00")},
		},
		AgainstNumber: "INV/2026-27/0308",
		AgainstDate:   &against,
		Reason:        "Sales return - 40 valves rejected at the customer's incoming inspection.",
		Total:         vdMoney("14632.00"),
		Narration:     "Credit passed to the party ledger; ITC to be reversed by the recipient.",
	}
}

// vdDebitNoteFixture raises a charge on a purchase, against a named supplier
// invoice, and is the case where the total is NEGATIVE on the face of the
// document.
func vdDebitNoteFixture() VoucherDoc {
	against := vdTestDate(2026, time.April, 9)
	party := Party{
		Name:      "Deshpande Steel Traders",
		Address1:  "Plot 22, MIDC Bhosari",
		City:      "Pune",
		State:     "Maharashtra",
		StateCode: "27",
		PIN:       "411026",
	}
	return VoucherDoc{
		Kind:     KindDebitNote,
		Number:   "DN-DR/2026-27/0003",
		Date:     vdTestDate(2026, time.June, 27),
		Supplier: vdTestSupplier(),
		Party:    vdPtrParty(party),
		Lines: []VoucherLine{
			{Ledger: "Deshpande Steel Traders", IsDebit: true, Amount: vdMoney("-9500.00"), BillRef: "DST/PUR/2026/1102", Narration: "Short weight on MS angle, 0.42 MT"},
		},
		Taxes: []TaxHead{
			{Name: "IGST", Rate: vdMoney("18"), Amount: vdMoney("-1710.00")},
		},
		AgainstNumber: "DST/PUR/2026/1102",
		AgainstDate:   &against,
		Reason:        "Short weight received against the supplier's invoice; value and tax reduced accordingly.",
		Total:         vdMoney("-11210.00"),
	}
}

// vdReceiptSettling25Bills is the pagination case: twenty-five allocations plus a
// bank leg will not fit on one sheet, and every one of the twenty-five bill
// references must survive the page break.
func vdReceiptSettling25Bills() VoucherDoc {
	v := VoucherDoc{
		Kind:     KindReceipt,
		Number:   "RCT/2026-27/0210",
		Date:     vdTestDate(2026, time.July, 15),
		Supplier: vdTestSupplier(),
		Party:    vdPtrParty(vdTestCustomer()),
		Lines: []VoucherLine{
			{Ledger: "Bank of Baroda - Current A/c 0012", IsDebit: true, Amount: vdMoney("50000.00"), Narration: "Cheque 442019, cleared 15-Jul-2026"},
		},
		Total:     vdMoney("50000.00"),
		Narration: "Single cheque allocated across twenty-five open bills.",
	}
	for i := 1; i <= 25; i++ {
		v.Lines = append(v.Lines, VoucherLine{
			Ledger: "Kulkarni Hardware Stores",
			Amount: vdMoney("2000.00"),
			// The due date rides along on the allocation, which is what makes each
			// row two lines tall and is why twenty-five of them do not fit on one
			// sheet. It is also what a customer actually needs on the receipt: the
			// bill it settles AND the date that bill was due.
			Narration: fmt.Sprintf("bill dated 12-Jun-2026, due %02d-Jul-2026", i),
			BillRef:   fmt.Sprintf("INV/2026-27/%04d", 300+i),
		})
	}
	return v
}

func vdBillRefsOf(v VoucherDoc) []string {
	var out []string
	for _, l := range v.Lines {
		if strings.TrimSpace(l.BillRef) != "" {
			out = append(out, l.BillRef)
		}
	}
	return out
}

func vdPtrParty(p Party) *Party { return &p }

// ---------------------------------------------------------------------------
// golden helpers
// ---------------------------------------------------------------------------

// vdAssertGolden renders v, compares the bytes to testdata/<name>, and returns the
// extracted text so the caller can assert on the words that are actually printed.
func vdAssertGolden(t *testing.T, name string, v VoucherDoc) []string {
	t.Helper()
	got, err := RenderVoucherDoc(v)
	if err != nil {
		t.Fatalf("a %s that carries every particular it needs must print, but rendering failed: %v", v.Kind, err)
	}

	path := filepath.Join("testdata", name)
	if os.Getenv(voucherGoldenUpdateEnv) == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("cannot create testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", path, err)
		}
		t.Logf("regenerated %s (%d bytes) - READ THE DIFF before committing it", path, len(got))
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the golden document %s is missing, so this render is unverified; regenerate with %s=1 go test ./internal/printing/ -run TestAVoucher: %v", path, voucherGoldenUpdateEnv, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the printed %s no longer matches %s: %d bytes now, %d before, first difference at byte %d.\nIf this change was intended, regenerate with %s=1 and review the diff; the streams are uncompressed so git diff on the pdf is legible.",
			v.Kind, path, len(got), len(want), vdFirstDiff(got, want), voucherGoldenUpdateEnv)
	}

	text, err := ExtractText(got)
	if err != nil {
		t.Fatalf("the printed %s is not a readable PDF, so nobody can open the document that was filed: %v", v.Kind, err)
	}

	// The words-on-the-paper sidecar. It is committed next to the PDF so that a
	// reviewer reading the diff of a layout change sees WHAT MOVED ON THE
	// DOCUMENT, in reading order, instead of a wall of changed content-stream
	// coordinates. The byte golden above is the assertion that catches everything;
	// this one is the assertion a human can check.
	txtPath := strings.TrimSuffix(path, ".pdf") + ".txt"
	gotText := strings.Join(text, "\n") + "\n"
	if os.Getenv(voucherGoldenUpdateEnv) == "1" {
		if err := os.WriteFile(txtPath, []byte(gotText), 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", txtPath, err)
		}
	}
	wantText, err := os.ReadFile(txtPath)
	if err != nil {
		t.Fatalf("the extracted-text golden %s is missing: %v", txtPath, err)
	}
	if gotText != string(wantText) {
		t.Errorf("the words printed on the %s changed; %s is the readable record of what is on the paper, so check the diff there first:\n--- committed ---\n%s\n--- now ---\n%s",
			v.Kind, txtPath, wantText, gotText)
	}
	return text
}

func vdFirstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// vdRequireText fails naming the CONSEQUENCE of the missing words, not the words.
func vdRequireText(t *testing.T, text []string, want, consequence string) {
	t.Helper()
	for _, s := range text {
		if strings.Contains(s, want) {
			return
		}
	}
	t.Errorf("%q does not appear anywhere on the printed document: %s", want, consequence)
}

func vdRefuseText(t *testing.T, text []string, unwanted, consequence string) {
	t.Helper()
	for _, s := range text {
		if strings.Contains(s, unwanted) {
			t.Errorf("%q appears on the printed document (in the string %q): %s", unwanted, s, consequence)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// delivery note
// ---------------------------------------------------------------------------

// TestAVoucherDeliveryNoteShowsWhatLeftTheWarehouseAndWhereItIsGoing is the
// delivery note's whole purpose: a transporter stopped at a check post has to be
// able to show what is on the vehicle and who it is for.
func TestAVoucherDeliveryNoteShowsWhatLeftTheWarehouseAndWhereItIsGoing(t *testing.T) {
	v := vdDeliveryNoteFixture()
	text := vdAssertGolden(t, "voucher_delivery_note.pdf", v)

	vdRequireText(t, text, "DELIVERY NOTE", "the document does not say what it is, and a delivery note that does not name itself is read as whatever the reader expects")
	vdRequireText(t, text, "DN/2026-27/0041", "the note cannot be quoted or found again in the books it was posted from")
	vdRequireText(t, text, "12-Jun-2026", "an undated delivery note cannot be tied to the despatch it accompanied")
	vdRequireText(t, text, "Kulkarni Hardware Stores", "the consignee is not named, so the goods in transit have nobody to be delivered to")
	vdRequireText(t, text, "Tilak Road", "the delivery address is missing, so the driver has nowhere to take the goods")

	for _, it := range v.Items {
		vdRequireText(t, text, it.HSN, "the HSN of a despatched line is missing, so the line cannot be matched to the invoice that follows it")
		vdRequireText(t, text, FormatQty(it.Quantity), "a despatched quantity is missing from the note, so nobody can check the vehicle against the paper")
	}
	vdRequireText(t, text, `Galvanised Iron Pipe 25mm (Class "B") \ ISI marked`, "a description containing parentheses and a backslash was mangled, which is exactly the case that breaks a PDF string literal")
	vdRequireText(t, text, "Sharma Roadways", "the narration naming the transporter and the lorry receipt did not print")
	vdRequireText(t, text, "Received by", "there is no place for the consignee to sign, and a delivery note nobody signed for proves no delivery")
}

// TestAVoucherDeliveryNoteIsNeverTitledOrPricedLikeATaxInvoice is the one that
// matters most on this document. A delivery note that carries tax columns and a
// tax-invoice heading WILL be handed to a customer as an invoice, and the customer
// will claim input tax credit against a document that charged no tax.
func TestAVoucherDeliveryNoteIsNeverTitledOrPricedLikeATaxInvoice(t *testing.T) {
	pdf, err := RenderVoucherDoc(vdDeliveryNoteFixture())
	if err != nil {
		t.Fatalf("rendering the delivery note failed: %v", err)
	}
	text, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("the printed delivery note is not a readable PDF: %v", err)
	}

	for _, banned := range []string{"TAX INVOICE", "CGST", "SGST", "IGST", "Cess", "Taxable Value", "Place of Supply", "Amount (in words)"} {
		vdRefuseText(t, text, banned,
			"a delivery note carrying this is indistinguishable from a tax invoice, and the customer it is handed to will claim input tax credit against a document that charged no tax")
	}
	vdRequireText(t, text, "Not a tax invoice",
		"the note does not say on its face that it is not a tax invoice, which is the sentence that stops it being treated as one")

	// A quantities-only note must not print a value column of zeroes either: a
	// column of "0.00" reads as a free supply.
	vdRefuseText(t, text, "0.00",
		"a delivery note despatched with no values printed a money column anyway, and a column of zeroes reads as a free supply")
}

// TestAVoucherDeliveryNoteCarryingValuesStillCarriesNoTax covers the e-way-bill
// case: the note declares a value for transport, and still charges no tax.
func TestAVoucherDeliveryNoteCarryingValuesStillCarriesNoTax(t *testing.T) {
	v := vdDeliveryNoteFixture()
	v.Number = "DN/2026-27/0042"
	v.Items[0].Rate = vdMoney("410.00")
	v.Items[0].Amount = vdMoney("49200.00")
	v.Items[1].Rate = vdMoney("62500.00")
	v.Items[1].Amount = vdMoney("1156250.00")
	v.Items[2].Rate = vdMoney("310.00")
	v.Items[2].Amount = vdMoney("124000.00")
	v.Total = vdMoney("1329450.00")

	text := vdAssertGolden(t, "voucher_delivery_note_with_value.pdf", v)

	vdRequireText(t, text, "11,56,250.00", "a declared line value is missing, or was printed with thousands grouping instead of the Indian lakh grouping an Indian document uses")
	vdRequireText(t, text, "13,29,450.00", "the total value declared for the movement is missing, so a check post cannot compare it to the e-way bill")
	for _, banned := range []string{"TAX INVOICE", "CGST", "SGST", "IGST"} {
		vdRefuseText(t, text, banned, "a delivery note that declares a value for transport still charges no tax, and printing a tax head on it makes it look like an invoice")
	}
}

// ---------------------------------------------------------------------------
// receipt and payment
// ---------------------------------------------------------------------------

// TestAVoucherReceiptNamesTheBillsItSettlesSoTheLedgerCannotBeDisputed is the
// reason the receipt renderer exists. "Rs. 50,000.00 received" with no bill
// references is the single most common cause of a disputed customer ledger: six
// months later neither side can say which invoice is still open.
func TestAVoucherReceiptNamesTheBillsItSettlesSoTheLedgerCannotBeDisputed(t *testing.T) {
	v := vdReceiptFixture()
	text := vdAssertGolden(t, "voucher_receipt.pdf", v)

	vdRequireText(t, text, "RECEIPT VOUCHER", "the document does not name itself as a receipt")
	vdRequireText(t, text, "Received From", "the receipt does not say who the money came from, so it cannot be credited to anyone's account")
	vdRequireText(t, text, "Kulkarni Hardware Stores", "the payer is not named on the receipt")

	refs := vdBillRefsOf(v)
	if len(refs) != 2 {
		t.Fatalf("the fixture is meant to settle two named bills, it settles %d", len(refs))
	}
	for _, ref := range refs {
		vdRequireText(t, text, ref,
			"the bill this receipt settles is not printed on it: a receipt nobody can match to an invoice leaves the customer ledger in dispute, because six months later neither side can say which invoice is still open")
	}
	vdRequireText(t, text, "Against Bill",
		"the receipt has no bill-reference column at all, so no allocation on it can be matched to an invoice and the customer ledger is disputable by construction")

	vdRequireText(t, text, "31,450.00", "the amount allocated to the first bill is missing, so the allocation cannot be checked")
	vdRequireText(t, text, "18,550.00", "the amount allocated to the second bill is missing, so the allocation cannot be checked")
	vdRequireText(t, text, "50,000.00", "the receipt total is missing from the face of the document")
	vdRequireText(t, text, "Rupees Fifty Thousand Only",
		"the amount in words is missing, and the words are the operative amount on a receipt exactly as on a cheque - a transposed digit in the figures shows up nowhere else")
	vdRequireText(t, text, "Authorised Signatory", "there is nobody signing for the money received")
}

// TestAVoucherReceiptLegWithNoBillReferencePrintsOnAccountNotAnEmptyCell: an
// empty cell is read as a field that failed to load, and the reader then goes
// looking for a bill reference that was never recorded.
func TestAVoucherReceiptLegWithNoBillReferencePrintsOnAccountNotAnEmptyCell(t *testing.T) {
	v := vdReceiptFixture()
	pdf, err := RenderVoucherDoc(v)
	if err != nil {
		t.Fatalf("rendering the receipt failed: %v", err)
	}
	text, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("the printed receipt is not a readable PDF: %v", err)
	}
	vdRequireText(t, text, "On Account",
		"the bank leg carries no bill reference and its cell was left blank; a blank cell reads as data that failed to load, and the reader then hunts for an allocation that was never recorded")
}

// TestAVoucherPaymentNamesTheSupplierBillsItDischarges is the mirror image: a
// payment with no bill references is how a supplier's statement and ours stop
// agreeing.
func TestAVoucherPaymentNamesTheSupplierBillsItDischarges(t *testing.T) {
	v := vdPaymentFixture()
	text := vdAssertGolden(t, "voucher_payment.pdf", v)

	vdRequireText(t, text, "PAYMENT VOUCHER", "the document does not name itself as a payment")
	vdRequireText(t, text, "Paid To", "the payment does not say who was paid, so it cannot be debited to anyone's account")
	for _, ref := range vdBillRefsOf(v) {
		vdRequireText(t, text, ref,
			"the supplier bill this payment discharges is not printed on it, so our ledger and the supplier's statement will disagree about which bills are open")
	}
	vdRequireText(t, text, "2,12,500.00", "an allocated amount is missing, or was grouped as 212,500.00 instead of the Indian 2,12,500.00")
	vdRequireText(t, text, "2,75,000.00", "the payment total is missing from the face of the document")
	vdRequireText(t, text, "Rupees Two Lakh Seventy Five Thousand Only", "the amount in words is missing from the payment voucher")
	vdRequireText(t, text, "HDFCR52026070400931", "the bank reference recorded on the leg did not print, so the payment cannot be traced in the bank statement")
}

// TestAVoucherReceiptSettlingTwentyFiveBillsKeepsEveryReferenceAcrossThePageBreak
// is the pagination case. Twenty-five allocations do not fit on one sheet, and a
// receipt that silently drops the allocations that ran off the bottom is worse
// than one that never listed them: it LOOKS complete.
func TestAVoucherReceiptSettlingTwentyFiveBillsKeepsEveryReferenceAcrossThePageBreak(t *testing.T) {
	v := vdReceiptSettling25Bills()
	text := vdAssertGolden(t, "voucher_receipt_25_bills.pdf", v)

	refs := vdBillRefsOf(v)
	if len(refs) != 25 {
		t.Fatalf("the fixture is meant to settle 25 bills, it settles %d", len(refs))
	}
	for _, ref := range refs {
		vdRequireText(t, text, ref,
			"an allocation ran off the bottom of the page and was not carried onto the next sheet, so the receipt looks complete while naming fewer bills than it settles")
	}

	pdf, err := RenderVoucherDoc(v)
	if err != nil {
		t.Fatalf("rendering the 25-bill receipt failed: %v", err)
	}
	pages := bytes.Count(pdf, []byte("/Type /Page "))
	if pages < 2 {
		t.Errorf("twenty-five allocations plus a bank leg were forced onto %d page(s); rows below the bottom margin are drawn off the paper and vanish when the sheet is printed", pages)
	}
	vdRequireText(t, text, fmt.Sprintf("Page 1 of %d", pages),
		"the pages do not say how many follow them, so a sheet that gets separated from the rest is filed as a complete receipt")
	vdRequireText(t, text, "(continued)",
		"the second sheet does not say it is a continuation, so it reads as a second, separate receipt for the same money")
	vdRequireText(t, text, "Against Bill",
		"the bill-reference column heading was not repeated on the continuation page, leaving a column of invoice numbers under no heading")
}

// ---------------------------------------------------------------------------
// credit and debit notes
// ---------------------------------------------------------------------------

// TestAVoucherCreditNoteNamesTheInvoiceItAdjustsAndTheTaxItReverses. The
// recipient of a credit note reverses input tax credit out of it, which they can
// only do if the note names the original invoice and states the tax per head.
func TestAVoucherCreditNoteNamesTheInvoiceItAdjustsAndTheTaxItReverses(t *testing.T) {
	v := vdCreditNoteFixture()
	text := vdAssertGolden(t, "voucher_credit_note.pdf", v)

	vdRequireText(t, text, "CREDIT NOTE", "the document does not name itself as a credit note")
	vdRequireText(t, text, "CN/2026-27/0009", "the note's own serial number is missing")
	vdRequireText(t, text, "Against Invoice No.: INV/2026-27/0308",
		"the note does not name the invoice it adjusts, so the recipient cannot tell which supply the credit belongs to or how much input tax credit to reverse")
	vdRequireText(t, text, "21-May-2026",
		"the original invoice's DATE is missing; an invoice serial repeats every financial year, so without the date the note still matches no single invoice")
	vdRequireText(t, text, "Sales return",
		"the reason is missing, and a recipient cannot tell a sales return from a rate correction from a post-sale discount - the three are treated differently in their books")

	vdRequireText(t, text, "CGST @ 9%", "the CGST head and rate are missing, so the recipient does not know how much central tax to reverse")
	vdRequireText(t, text, "SGST @ 9%", "the SGST head and rate are missing, so the recipient does not know how much state tax to reverse")
	vdRequireText(t, text, "1,116.00", "a tax amount is missing from the note that the recipient reverses credit against")
	vdRequireText(t, text, "12,400.00", "the taxable value being credited is missing")
	vdRequireText(t, text, "14,632.00", "the note's total is missing from the face of the document")
	vdRequireText(t, text, "rule 53(1A)",
		"the note does not cite the rule that makes the original-invoice particulars mandatory, so nobody reading it knows the reference is required rather than a courtesy")
	vdRequireText(t, text, "Rupees Fourteen Thousand Six Hundred Thirty Two Only", "the amount in words is missing from the credit note")
}

// TestAVoucherDebitNoteRendersItsNegativeTotalWithTheSignOnTheFaceOfIt pins the
// money convention to the ONE FormatMoney documents: a leading minus, never the
// accountant's parenthesis. A note whose sign the reader has to infer is a note
// that gets posted the wrong way round.
func TestAVoucherDebitNoteRendersItsNegativeTotalWithTheSignOnTheFaceOfIt(t *testing.T) {
	v := vdDebitNoteFixture()
	text := vdAssertGolden(t, "voucher_debit_note.pdf", v)

	vdRequireText(t, text, "DEBIT NOTE", "the document does not name itself as a debit note")
	vdRequireText(t, text, "Against Invoice No.: DST/PUR/2026/1102",
		"the debit note does not name the supplier invoice it adjusts, so the supplier cannot apply it to anything")
	vdRequireText(t, text, "09-Apr-2026", "the original invoice's date is missing from the debit note")
	vdRequireText(t, text, "Short weight", "the reason the note was raised is missing")

	vdRequireText(t, text, "-11,210.00",
		"the negative total did not print with the leading minus FormatMoney documents, so the sign is left to be inferred and the note can be posted the wrong way round")
	vdRefuseText(t, text, "(11,210.00)",
		"the accountant's parenthesis convention was used instead of the leading minus this codebase pinned in FormatMoney; two conventions on one set of books is how a sign gets misread")
	vdRequireText(t, text, "-1,710.00", "the negative IGST adjustment did not print with its sign")
	vdRequireText(t, text, "Minus Rupees Eleven Thousand Two Hundred Ten Only",
		"the words dropped the sign that the figures show, and the words are the operative amount")
	vdRequireText(t, text, "GSTIN/UIN: Unregistered",
		"the party has no GSTIN and the row was left blank, which reads as data that failed to load rather than as a legitimately unregistered party")
}

// TestAVoucherCreditNoteWithoutTheOriginalInvoiceIsRefusedNotPrinted is the loud
// failure the slice requires. A note that names no invoice cannot be applied to
// any invoice; printing it produces a document that will sit in a file for years
// and adjust nothing.
//
// THE MISSING REASON IS NOT IN THIS TABLE, AND THAT IS THE POINT. It was, for one
// wave, and the consequence was that no credit or debit note printed at all: the
// voucher schema has no reason column, so the field arrived empty from the DB half
// and every note was refused. The original-invoice particulars are cited (section
// 34 read with rule 53(1A)) and are enforced here; a mandatory free-text reason
// could not be verified from this environment and is therefore not enforced
// anywhere — see TestACreditNoteWithNoRecordedReasonStillPrints and the note above
// vdAgainstInvoiceRequirement.
func TestAVoucherCreditNoteWithoutTheOriginalInvoiceIsRefusedNotPrinted(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*VoucherDoc)
		wantHas string
	}{
		{
			name:    "no invoice number at all",
			mutate:  func(v *VoucherDoc) { v.AgainstNumber = "" },
			wantHas: "the number of the original invoice it adjusts",
		},
		{
			name:    "an invoice number of only whitespace",
			mutate:  func(v *VoucherDoc) { v.AgainstNumber = "   " },
			wantHas: "the number of the original invoice it adjusts",
		},
		{
			name:    "no invoice date",
			mutate:  func(v *VoucherDoc) { v.AgainstDate = nil },
			wantHas: "the date of the original invoice it adjusts",
		},
		{
			name:    "a zero invoice date",
			mutate:  func(v *VoucherDoc) { v.AgainstDate = &time.Time{} },
			wantHas: "the date of the original invoice it adjusts",
		},
	}
	for _, kind := range []DocKind{KindCreditNote, KindDebitNote} {
		for _, tc := range cases {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				v := vdCreditNoteFixture()
				v.Kind = kind
				tc.mutate(&v)

				pdf, err := RenderVoucherDoc(v)
				if err == nil {
					t.Fatalf("a %s missing %s WAS PRINTED (%d bytes): a note nobody can match to an invoice adjusts nothing, and it must fail loudly here rather than be filed as a document that looks complete", kind, tc.name, len(pdf))
				}
				if pdf != nil {
					t.Errorf("the refused %s returned %d bytes alongside its error; a caller that ignores the error would write that file out", kind, len(pdf))
				}
				if !errors.Is(err, ErrVoucherParticularMissing) {
					t.Errorf("the refusal does not unwrap to ErrVoucherParticularMissing, so a handler cannot classify it: %v", err)
				}
				var pe *VoucherParticularError
				if !errors.As(err, &pe) {
					t.Fatalf("the refusal is not a *VoucherParticularError, so no caller can see which particular was missing: %v", err)
				}
				if pe.Kind != kind {
					t.Errorf("the error names kind %q, but a %q was being printed", pe.Kind, kind)
				}
				if !strings.Contains(pe.Particular, tc.wantHas) {
					t.Errorf("the error blames %q, which does not name the missing particular %q", pe.Particular, tc.wantHas)
				}
				if !strings.Contains(err.Error(), "cannot") {
					t.Errorf("the error message does not say what cannot be done: %q", err.Error())
				}
			})
		}
	}
}

// TestAVoucherCreditNoteRefusalExplainsWhyTheInvoiceReferenceIsMandatory: the
// message an operator sees has to tell them what to go and find, and under what
// authority.
func TestAVoucherCreditNoteRefusalExplainsWhyTheInvoiceReferenceIsMandatory(t *testing.T) {
	v := vdCreditNoteFixture()
	v.AgainstNumber = ""
	_, err := RenderVoucherDoc(v)
	if err == nil {
		t.Fatal("a credit note with no original invoice reference was printed")
	}
	for _, want := range []string{
		"nobody can match",
		"input tax credit",
		"section 34",
		"rule 53(1A)",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so the operator is told a field is missing without being told why it is mandatory: %q", want, err.Error())
		}
	}
	// UNVERIFIED clause letter: nothing printed or reported may cite one. See the
	// comment on vdAgainstInvoiceRequirement.
	for _, letter := range []string{"53(1A)(a)", "53(1A)(d)", "53(1A)(g)"} {
		if strings.Contains(err.Error(), letter) {
			t.Errorf("the refusal cites %s; the clause LETTER within rule 53(1A) could not be verified from this environment, so no letter may be stated as fact", letter)
		}
	}
}

// TestACreditNoteWithNoRecordedReasonStillPrintsAndPrintsNoEmptyReasonLine is the
// counterpart of the refusal table above.
//
// WHAT THIS TEST IS ABOUT. That a credit or debit note must NAME THE ORIGINAL
// INVOICE is cited: section 34 of the CGST Act 2017 read with rule 53(1A) of the
// CGST Rules 2017. That a free-text REASON is itself a mandatory particular is the
// claim that could NOT be verified from this environment — the clause lettering
// within rule 53(1A) could not be checked against the gazette. Refusing on the
// unverified half stopped every credit and debit note in the product from printing,
// because the voucher schema records no reason. So the reason prints when the
// voucher has one and the line is omitted when it does not.
func TestACreditNoteWithNoRecordedReasonStillPrintsAndPrintsNoEmptyReasonLine(t *testing.T) {
	for _, kind := range []DocKind{KindCreditNote, KindDebitNote} {
		t.Run(string(kind), func(t *testing.T) {
			v := vdCreditNoteFixture()
			v.Kind = kind
			v.Reason = "" // exactly what printingdb supplies when the voucher has no narration

			pdf, err := RenderVoucherDoc(v)
			if err != nil {
				t.Fatalf("a %s that names the invoice it adjusts, its date, its lines and its tax was REFUSED for want of a free-text reason: %v.\nThe reason requirement is UNVERIFIED (the clause lettering within rule 53(1A) could not be checked here) and the voucher schema records no reason, so this refusal stops every credit and debit note in the product from printing at all.", kind, err)
			}
			text, err := ExtractText(pdf)
			if err != nil {
				t.Fatalf("the printed %s is not a readable PDF: %v", kind, err)
			}

			// What is cited is still on the paper.
			vdRequireText(t, text, "Against Invoice No.: INV/2026-27/0308",
				"the note does not name the invoice it adjusts, which is the particular that IS cited and IS mandatory")
			vdRequireText(t, text, "21-May-2026", "the original invoice's date is missing from the note")
			vdRequireText(t, text, "rule 53(1A)", "the note no longer cites the rule behind the original-invoice particulars")

			// And nothing is printed where the reason would have been. A bare "Reason:"
			// reads as a field that failed to load and sends the recipient looking for
			// a reason that was never recorded; a dash asserts that none was given.
			vdRefuseText(t, text, "Reason:",
				"the note printed a reason LABEL with nothing after it; an empty labelled field reads as data that failed to load, and the recipient goes looking for a reason the books never held")
			for _, placeholder := range []string{"Reason: -", "Reason: N/A", "Reason: Not specified"} {
				vdRefuseText(t, text, placeholder,
					"the renderer invented a placeholder reason; asserting that no reason was given is a claim about the transaction that this package is not in a position to make")
			}

			// The reason still prints when the voucher HAS one.
			v.Reason = "Sales return - 40 valves rejected at the customer's incoming inspection."
			withReason, err := RenderVoucherDoc(v)
			if err != nil {
				t.Fatalf("the same %s with a reason would not print: %v", kind, err)
			}
			withText, err := ExtractText(withReason)
			if err != nil {
				t.Fatalf("%v", err)
			}
			vdRequireText(t, withText, "Reason: Sales return",
				"the recorded reason did not reach the paper, so a recipient cannot tell a sales return from a rate correction even when the books say which it is")
		})
	}
}

// ---------------------------------------------------------------------------
// the two blocks of the masthead must not print through each other
// ---------------------------------------------------------------------------

// vdAssertNothingIsPrintedOffTheEdgeOfTheSheet walks every drawn string and checks
// it against the printable width. It reuses reportPagesText from report_test.go
// rather than parsing the content stream a second way: both renderers draw through
// the same Page.Text, so one reader over both is one thing to keep correct.
func vdAssertNothingIsPrintedOffTheEdgeOfTheSheet(t *testing.T, v VoucherDoc, pdf []byte) {
	t.Helper()
	const eps = 0.05
	for pi, ops := range reportPagesText(t, pdf) {
		for _, o := range ops {
			w := Measure(reportFontNamed(o.font), o.size, o.text)
			if o.x < vdLeft-eps {
				t.Errorf("page %d of the %s: %q starts at x=%s, left of the margin at x=%s, so %s points of it are off the edge of the paper",
					pi+1, v.Kind, o.text, fmtNum(o.x), fmtNum(vdLeft), fmtNum(vdLeft-o.x))
			}
			if o.x+w > vdRight+eps {
				t.Errorf("page %d of the %s: %q ends at x=%s, past the right margin at x=%s, so %s points of it are off the edge of the paper and the printer drops them",
					pi+1, v.Kind, o.text, fmtNum(o.x+w), fmtNum(vdRight), fmtNum(o.x+w-vdRight))
			}
		}
	}
}

// TestALongIssuerNameWrapsInsteadOfPrintingThroughTheVoucherNumber. The No./Dated
// block on the right of the masthead is how this document is quoted: it is the
// reference both sides name when a customer ledger is disputed, which is the same
// dispute the Against-Bill column exists to prevent. A registered name drawn
// straight across it destroys the one field that makes the voucher findable.
func TestALongIssuerNameWrapsInsteadOfPrintingThroughTheVoucherNumber(t *testing.T) {
	const issuer = "Shri Mahalaxmi Engineering & Fabrication Works Private Limited (Bhiwandi Unit)"
	const consignee = "Maharashtra State Electricity Distribution Company Limited, Bhiwandi Circle Office Stores & Purchase Department"

	if Measure(HelveticaBold, vdNameSize, issuer) <= vdLeftBlockWidth {
		t.Fatalf("this fixture no longer proves anything: the issuer's name measures %s points and the masthead's left block is %s points wide, so it never reached the number block",
			fmtNum(Measure(HelveticaBold, vdNameSize, issuer)), fmtNum(vdLeftBlockWidth))
	}
	if Measure(HelveticaBold, vdBodySize+1, consignee) <= vdRight-vdLeft {
		t.Fatalf("this fixture no longer proves anything: the consignee's name measures %s points and the usable width is %s",
			fmtNum(Measure(HelveticaBold, vdBodySize+1, consignee)), fmtNum(vdRight-vdLeft))
	}

	for _, kind := range []DocKind{KindDeliveryNote, KindReceipt, KindCreditNote} {
		t.Run(string(kind), func(t *testing.T) {
			v := vdDeliveryNoteFixture()
			switch kind {
			case KindReceipt:
				v = vdReceiptFixture()
			case KindCreditNote:
				v = vdCreditNoteFixture()
			}
			v.Supplier.Name = issuer
			v.Party.Name = consignee

			pdf, err := RenderVoucherDoc(v)
			if err != nil {
				t.Fatalf("a %s issued by a company with a long registered name would not print: %v", kind, err)
			}
			vdAssertNothingIsPrintedOffTheEdgeOfTheSheet(t, v, pdf)

			ops := reportPagesText(t, pdf)[0]
			if reportContains(ops, issuer) {
				t.Errorf("the issuer's registered name was drawn as ONE line %s points wide in a %s-point block, so it prints straight over the No./Dated block: the voucher number, the one reference both sides quote when the ledger is disputed, is buried under it",
					fmtNum(Measure(HelveticaBold, vdNameSize, issuer)), fmtNum(vdLeftBlockWidth))
			}

			// vdNameSize is used for exactly one thing on this document — the issuer's
			// name in the masthead — so the size identifies the block.
			numberBlockLeft := vdRight - vdNumberBlockWidth
			for _, o := range ops {
				if o.size != vdNameSize {
					continue
				}
				end := o.x + Measure(reportFontNamed(o.font), o.size, o.text)
				if end > numberBlockLeft {
					t.Errorf("a line of the issuer's name (%q) ends at x=%s and the No./Dated block starts at x=%s, so the voucher number and its date are overprinted by the name of the company that issued it",
						o.text, fmtNum(end), fmtNum(numberBlockLeft))
				}
			}

			// The name survived intact on both blocks: a masthead missing "Private
			// Limited" names a different legal person from the one issuing the document.
			joined := strings.Join(reportPageStrings(ops), " ")
			for _, word := range strings.Fields(issuer) {
				if !strings.Contains(joined, word) {
					t.Errorf("wrapping the issuer's name lost %q, so the document is issued in the name of a business that does not exist", word)
				}
			}
			for _, word := range strings.Fields(consignee) {
				if !strings.Contains(joined, word) {
					t.Errorf("wrapping the party's name lost %q, so the document names a party that is not the one in the books", word)
				}
			}
		})
	}
}

// TestEveryVoucherFixtureKeepsItsPhoneAndEmailApart pins the reason vdFit exists
// rather than a bare Wrap: the issuer's contact line separates the telephone number
// from the e-mail address with a deliberate gap, and normalising the whitespace of
// a line that already fits runs the two together into one unreadable string.
func TestEveryVoucherFixtureKeepsItsPhoneAndEmailApart(t *testing.T) {
	pdf, err := RenderVoucherDoc(vdReceiptFixture())
	if err != nil {
		t.Fatalf("%v", err)
	}
	text, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("%v", err)
	}
	vdRequireText(t, text, "Phone: 022 2201 4455   E-Mail: accounts@mehtatrading.example",
		"the gap between the telephone number and the e-mail address was closed up, so the two facts read as one string and neither can be dialled or copied")
}

// ---------------------------------------------------------------------------
// the kinds this renderer does not serve
// ---------------------------------------------------------------------------

// TestAVoucherKindThisRendererDoesNotServeIsRefusedNotPrintedBlank. Falling
// through would produce a one-page PDF with a masthead and no body, which gets
// filed and looks like a document.
func TestAVoucherKindThisRendererDoesNotServeIsRefusedNotPrintedBlank(t *testing.T) {
	for _, kind := range []DocKind{KindTaxInvoice, KindBillOfSupply, KindReport, DocKind(""), DocKind("delivery note")} {
		t.Run(string(kind), func(t *testing.T) {
			v := vdReceiptFixture()
			v.Kind = kind

			pdf, err := RenderVoucherDoc(v)
			if err == nil {
				t.Fatalf("RenderVoucherDoc printed %d bytes for kind %q, which it does not serve; a blank-bodied document that looks real is worse than a refusal", len(pdf), kind)
			}
			if pdf != nil {
				t.Errorf("kind %q was refused but %d bytes came back with the error", kind, len(pdf))
			}
			if !errors.Is(err, ErrUnsupportedVoucherKind) {
				t.Errorf("the refusal of kind %q does not unwrap to ErrUnsupportedVoucherKind: %v", kind, err)
			}
			var ue *UnsupportedVoucherKindError
			if !errors.As(err, &ue) {
				t.Fatalf("the refusal of kind %q is not an *UnsupportedVoucherKindError: %v", kind, err)
			}
			if ue.Kind != kind {
				t.Errorf("the error names kind %q but %q was asked for", ue.Kind, kind)
			}
		})
	}
}

// TestEveryVoucherKindDocGoDeclaresIsEitherServedOrRefusedByName walks the
// document kinds declared in doc.go so that a kind added there can never quietly
// fall into the default branch and start printing a blank body.
func TestEveryVoucherKindDocGoDeclaresIsEitherServedOrRefusedByName(t *testing.T) {
	served := map[DocKind]bool{
		KindDeliveryNote: true,
		KindReceipt:      true,
		KindPayment:      true,
		KindCreditNote:   true,
		KindDebitNote:    true,
	}
	all := []DocKind{
		KindTaxInvoice, KindBillOfSupply, KindDeliveryNote, KindReceipt,
		KindPayment, KindCreditNote, KindDebitNote, KindReport,
	}
	for _, kind := range all {
		v := vdDeliveryNoteFixture()
		v.Kind = kind
		if kind == KindReceipt || kind == KindPayment {
			v = vdReceiptFixture()
			v.Kind = kind
		}
		if kind == KindCreditNote || kind == KindDebitNote {
			v = vdCreditNoteFixture()
			v.Kind = kind
		}
		_, err := RenderVoucherDoc(v)
		if served[kind] && err != nil {
			t.Errorf("%q is a kind this renderer serves, but a complete document of that kind would not print: %v", kind, err)
		}
		if !served[kind] && !errors.Is(err, ErrUnsupportedVoucherKind) {
			t.Errorf("%q is not served by this renderer, so it must be refused as an unsupported kind rather than rendered; got %v", kind, err)
		}
	}
}

// ---------------------------------------------------------------------------
// the particulars every one of the five needs
// ---------------------------------------------------------------------------

func TestAVoucherWithoutTheParticularsItIsUselessWithoutIsRefused(t *testing.T) {
	cases := []struct {
		what   string
		mutate func(*VoucherDoc)
		blames string
	}{
		{"no voucher number", func(v *VoucherDoc) { v.Number = "" }, "its voucher number"},
		{"a whitespace voucher number", func(v *VoucherDoc) { v.Number = "\t " }, "its voucher number"},
		{"no date", func(v *VoucherDoc) { v.Date = time.Time{} }, "its date"},
		{"no issuing business", func(v *VoucherDoc) { v.Supplier.Name = "" }, "the name of the business issuing it"},
		{"no party at all", func(v *VoucherDoc) { v.Party = nil }, "the other party"},
		{"a party with no name", func(v *VoucherDoc) { v.Party = &Party{City: "Pune"} }, "the other party"},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			v := vdReceiptFixture()
			tc.mutate(&v)
			_, err := RenderVoucherDoc(v)
			var pe *VoucherParticularError
			if !errors.As(err, &pe) {
				t.Fatalf("a receipt with %s was not refused with a *VoucherParticularError: %v", tc.what, err)
			}
			if !strings.Contains(pe.Particular, tc.blames) {
				t.Errorf("the refusal blames %q rather than %q", pe.Particular, tc.blames)
			}
			if strings.TrimSpace(pe.Consequence) == "" {
				t.Errorf("the refusal states no consequence, so the operator is told a field name and nothing about what goes wrong")
			}
		})
	}
}

func TestADeliveryNoteWithNoGoodsLinesAndAMoneyVoucherWithNoLegsAreBothRefused(t *testing.T) {
	dn := vdDeliveryNoteFixture()
	dn.Items = nil
	if _, err := RenderVoucherDoc(dn); !errors.Is(err, ErrVoucherParticularMissing) {
		t.Errorf("a delivery note with no lines describes no movement and must be refused; got %v", err)
	}
	for _, kind := range []DocKind{KindReceipt, KindPayment} {
		v := vdReceiptFixture()
		v.Kind = kind
		v.Lines = nil
		if _, err := RenderVoucherDoc(v); !errors.Is(err, ErrVoucherParticularMissing) {
			t.Errorf("a %s with no ledger legs states an amount against nothing and must be refused; got %v", kind, err)
		}
	}
	note := vdCreditNoteFixture()
	note.Items = nil
	note.Lines = nil
	if _, err := RenderVoucherDoc(note); !errors.Is(err, ErrVoucherParticularMissing) {
		t.Errorf("a credit note with neither goods lines nor ledger legs shows a total tied to nothing and must be refused; got %v", err)
	}
}

// ---------------------------------------------------------------------------
// determinism and money discipline
// ---------------------------------------------------------------------------

// TestTheSameVoucherPrintsByteIdenticalOutputEveryTime: without this, every
// golden test above is meaningless, because a byte difference could not be told
// from a re-render.
func TestTheSameVoucherPrintsByteIdenticalOutputEveryTime(t *testing.T) {
	for _, v := range []VoucherDoc{
		vdDeliveryNoteFixture(), vdReceiptFixture(), vdPaymentFixture(),
		vdCreditNoteFixture(), vdDebitNoteFixture(), vdReceiptSettling25Bills(),
	} {
		first, err := RenderVoucherDoc(v)
		if err != nil {
			t.Fatalf("rendering the %s failed: %v", v.Kind, err)
		}
		second, err := RenderVoucherDoc(v)
		if err != nil {
			t.Fatalf("re-rendering the %s failed: %v", v.Kind, err)
		}
		if !bytes.Equal(first, second) {
			t.Errorf("two renders of the same %s differ (first difference at byte %d), so no golden test on it can tell a regression from a re-print", v.Kind, vdFirstDiff(first, second))
		}
	}
}

// TestAnUnprintableRuneInAPartyNameFailsRatherThanPrintingGarbage: a Devanagari
// party name has no WinAnsi code point, and printing the low byte would render a
// customer's own name as random accented Latin letters on their own receipt.
func TestAnUnprintableRuneInAPartyNameFailsRatherThanPrintingGarbage(t *testing.T) {
	v := vdReceiptFixture()
	v.Party.Name = "कुलकर्णी हार्डवेअर"
	pdf, err := RenderVoucherDoc(v)
	if err == nil {
		t.Fatalf("a party name with no WinAnsi encoding printed anyway (%d bytes); the customer's name would appear as garbage on their own receipt", len(pdf))
	}
	var ee *EncodingError
	if !errors.As(err, &ee) {
		t.Errorf("the failure is not an *EncodingError naming the offending rune, so nobody can tell which character to transliterate: %v", err)
	}
}

// TestTheVoucherRendererDoesNoFloatingPointMoneyArithmetic parses voucherdoc.go
// without comments and fails on any identifier that would take an amount through
// a float or through int64.
//
// float64 ITSELF IS NOT BANNED HERE, unlike in format.go: this file computes PAGE
// GEOMETRY in points, which is real-valued by definition (see fmtNum). What is
// banned is every route from a decimal to a float, and IntPart, which converts
// through int64 and silently wraps above 9.22e18.
func TestTheVoucherRendererDoesNoFloatingPointMoneyArithmetic(t *testing.T) {
	banned := map[string]string{
		"ParseFloat":     "parses money through a float",
		"FormatFloat":    "renders money through a float",
		"Float64":        "converts a decimal to a float",
		"InexactFloat64": "converts a decimal to a float, inexactly and by name",
		"IntPart":        "converts a decimal through int64 and SILENTLY WRAPS above 9.22e18",
		"NewFromFloat":   "builds a decimal from a float",
		"Add":            "re-derives an amount instead of printing the one the ledger agreed",
		"Sub":            "re-derives an amount instead of printing the one the ledger agreed",
		"Mul":            "re-derives an amount instead of printing the one the ledger agreed",
		"Div":            "re-derives an amount instead of printing the one the ledger agreed",
		"Round":          "re-rounds an amount the ledger already rounded",
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "voucherdoc.go", nil, 0 /* comments discarded */)
	if err != nil {
		t.Fatalf("cannot parse voucherdoc.go: %v", err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if why, bad := banned[sel.Sel.Name]; bad {
			t.Errorf("voucherdoc.go:%d calls .%s, which %s; every amount on these documents must be the decimal this package was handed, rendered by FormatMoney",
				fset.Position(sel.Pos()).Line, sel.Sel.Name, why)
		}
		return true
	})
	src, err := os.ReadFile("voucherdoc.go")
	if err != nil {
		t.Fatalf("cannot read voucherdoc.go: %v", err)
	}
	for _, bad := range []string{"float32", "strconv.ParseFloat", "%f"} {
		if bytes.Contains(src, []byte(bad)) {
			t.Errorf("voucherdoc.go contains %q; money never passes through a float in this package", bad)
		}
	}
}
