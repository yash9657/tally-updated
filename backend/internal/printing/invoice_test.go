package printing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// goldenEnv regenerates the committed golden files.
//
// HOW TO REGENERATE, and this is the whole documentation of the procedure:
//
//	cd backend && UPDATE_GOLDEN=1 go test ./internal/printing/ -run TestPrintedInvoiceMatchesItsGoldenFile
//
// then READ THE DIFF of the .txt file before committing. The .pdf golden proves
// the bytes did not move; the .txt golden — one printed string per line, in the
// order the renderer drew them — is what tells a reviewer WHAT moved. A byte
// golden alone can only say "byte 4471 differs", which no reviewer can act on,
// and a regeneration nobody read is how a wrong total gets blessed.
const goldenEnv = "UPDATE_GOLDEN"

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

// goldenIntraStateInvoice is THE fixture of this file: three line items, CGST +
// SGST, a discount on one line, a round-off, an IRN and an acknowledgement
// number, a separate ship-to address, template overrides (extra letterhead line,
// terms, bank details, footer), and a description containing a PARENTHESIS, a
// BACKSLASH and a PERCENT SIGN — the three characters that break a PDF string
// literal or a naive format string.
//
// THE FIGURES ARE A CONSISTENT SET AND THEY ARE NOT ROUND:
//
//	line amounts   2,500.00 + 750.00 + 6,750.40 = 10,000.40 taxable value
//	CGST 9%                                          900.04
//	SGST 9%                                          900.04
//	                                              -----------
//	                                              11,800.48
//	round off                                        (0.48)
//	total                                         11,800.00
//
// The four-decimal-looking 900.036 rounding to 900.04 is deliberate: it is the
// case where a renderer that recomputed tax from the taxable value would print a
// different number than the ledger holds.
//
// THE DISCOUNT ON LINE 3 IS A PERCENTAGE AND THE THREE FIGURES AGREE: one unit at
// a listed rate of 8,438.00 less DiscountPct 20 is a taxable value of 6,750.40,
// which is what the voucher stored. The percentage is what stock_movement holds
// (discount_pct, CHECK 0..100); the rupee discount of 1,687.60 appears in no
// column, because no posting holds it.
func goldenIntraStateInvoice(t *testing.T) Invoice {
	t.Helper()
	due := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	return Invoice{
		Kind:    KindTaxInvoice,
		Number:  "INV/2026-27/0042",
		Date:    time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
		DueDate: &due,
		Supplier: Company{
			Name:      "Mehta Trading Co.",
			Address1:  "14 Laxmi Industrial Estate",
			Address2:  "Off Karve Road",
			City:      "Pune",
			State:     "Maharashtra",
			StateCode: "27",
			PIN:       "411004",
			GSTIN:     "27AAAPZ1234C1ZS",
			PAN:       "AAAPZ1234C",
			Phone:     "020 2567 8899",
			Email:     "accounts@mehtatrading.example",
		},
		Recipient: Party{
			Name:      "Kulkarni Hardware Stores",
			Address1:  "22 Shivaji Road",
			Address2:  "Near Panchavati Bus Stand",
			City:      "Nashik",
			State:     "Maharashtra",
			StateCode: "27",
			PIN:       "422001",
			GSTIN:     "27AACCK5678L1Z9",
		},
		ShipTo: &Party{
			Name:      "Kulkarni Hardware Stores - Satpur Godown",
			Address1:  "Plot D-14, Satpur MIDC",
			City:      "Nashik",
			State:     "Maharashtra",
			StateCode: "27",
			PIN:       "422007",
			GSTIN:     "27AACCK5678L1Z9",
		},
		PlaceOfSupply:     "Maharashtra",
		PlaceOfSupplyCode: "27",
		ReverseCharge:     false,
		Items: []LineItem{
			{
				SerialNo:    1,
				Description: `Widget (Large) \ 50% cotton blend`,
				HSN:         "6109",
				Quantity:    dec(t, "10"),
				Unit:        "NOS",
				Rate:        dec(t, "250.00"),
				Amount:      dec(t, "2500.00"),
			},
			{
				SerialNo:    2,
				Description: "Galvanised steel bracket 12mm",
				HSN:         "7326",
				Quantity:    dec(t, "4"),
				Unit:        "KGS",
				Rate:        dec(t, "187.50"),
				Amount:      dec(t, "750.00"),
			},
			{
				SerialNo:    3,
				Description: "Annual maintenance contract for the packaging line, including four quarterly preventive service visits and all consumables",
				HSN:         "998719",
				Quantity:    dec(t, "1"),
				Unit:        "NOS",
				Rate:        dec(t, "8438.00"),
				DiscountPct: dec(t, "20"),
				Amount:      dec(t, "6750.40"),
			},
		},
		Taxes: []TaxHead{
			{Name: "CGST", Rate: dec(t, "9"), Amount: dec(t, "900.04")},
			{Name: "SGST", Rate: dec(t, "9"), Amount: dec(t, "900.04")},
		},
		Subtotal: dec(t, "10000.40"),
		RoundOff: dec(t, "-0.48"),
		Total:    dec(t, "11800.00"),
		IRN:      "35b4f5c1d1b6b6f0f8a5c0d3e2f1a09876543210fedcba9876543210abcdef01",
		AckNo:    "112010021000123",
		Overrides: TemplateOverrides{
			HeaderLines:     []string{"Works: Plot 7, Chakan MIDC, Pune 410501"},
			Terms:           "Payment due within 30 days. Interest at 18% per annum on overdue amounts.",
			ShowBankDetails: true,
			BankLines: []string{
				"Bank: State Bank of India, Karve Road Branch",
				"A/c: 30012345678   IFSC: SBIN0001234",
			},
			FooterLines: []string{"Subject to Pune jurisdiction"},
		},
	}
}

// goldenInterStateInvoice is an inter-State supply: IGST only, a place of supply
// in another State, no ship-to, no IRN and no template overrides — so it also
// exercises every built-in default.
func goldenInterStateInvoice(t *testing.T) Invoice {
	t.Helper()
	return Invoice{
		Kind:   KindTaxInvoice,
		Number: "INV/2026-27/0043",
		Date:   time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
		Supplier: Company{
			Name:      "Mehta Trading Co.",
			Address1:  "14 Laxmi Industrial Estate",
			City:      "Pune",
			State:     "Maharashtra",
			StateCode: "27",
			PIN:       "411004",
			GSTIN:     "27AAAPZ1234C1ZS",
		},
		Recipient: Party{
			Name:      "Shreyas Enterprises",
			Address1:  "48 Jayanagar 4th Block",
			City:      "Bengaluru",
			State:     "Karnataka",
			StateCode: "29",
			PIN:       "560011",
			GSTIN:     "29AABCS9012M1ZK",
		},
		PlaceOfSupply:     "Karnataka",
		PlaceOfSupplyCode: "29",
		Items: []LineItem{
			{SerialNo: 1, Description: "Widget (Small)", HSN: "6109",
				Quantity: dec(t, "24"), Unit: "NOS", Rate: dec(t, "250.00"), Amount: dec(t, "6000.00")},
			{SerialNo: 2, Description: "Galvanised steel bracket 12mm", HSN: "7326",
				Quantity: dec(t, "20"), Unit: "KGS", Rate: dec(t, "200.00"), Amount: dec(t, "4000.00")},
		},
		Taxes:    []TaxHead{{Name: "IGST", Rate: dec(t, "18"), Amount: dec(t, "1800.00")}},
		Subtotal: dec(t, "10000.00"),
		Total:    dec(t, "11800.00"),
	}
}

// goldenBillOfSupply is a Rule 49 document: no tax of any kind.
func goldenBillOfSupply(t *testing.T) Invoice {
	t.Helper()
	inv := goldenInterStateInvoice(t)
	inv.Kind = KindBillOfSupply
	inv.Number = "BOS/2026-27/0009"
	inv.Taxes = nil
	inv.PlaceOfSupply = ""
	inv.PlaceOfSupplyCode = ""
	inv.Recipient.GSTIN = ""
	inv.Items = []LineItem{
		{SerialNo: 1, Description: "Handloom cotton dusters", HSN: "6307",
			Quantity: dec(t, "150"), Unit: "NOS", Rate: dec(t, "20.00"), Amount: dec(t, "3000.00")},
		{SerialNo: 2, Description: "Jute twine 3 ply", HSN: "5607",
			Quantity: dec(t, "30"), Unit: "KGS", Rate: dec(t, "50.00"), Amount: dec(t, "1500.00")},
	}
	inv.Subtotal = dec(t, "4500.00")
	inv.Total = dec(t, "4500.00")
	return inv
}

// goldenLongRegistrationNamesInvoice is the intra-State fixture with the names a
// GST registration actually carries.
//
// NONE OF THESE IS A PATHOLOGICAL STRING. company.name and ledger.name are TEXT
// NOT NULL and unbounded, "SHREE SIDDHIVINAYAK ENGINEERING AND FABRICATION WORKS
// PRIVATE LIMITED" is 69 characters of ordinary Maharashtra proprietary-company
// naming, and a State utility's circle office is longer still. The supplier name
// is drawn at 10pt bold on the SAME BASELINE as the Rule 46(b) invoice number in
// the right-hand meta block, and the recipient name sits directly left of the
// Shipped-to block, so this is the fixture that decides whether the header
// columns hold.
func goldenLongRegistrationNamesInvoice(t *testing.T) Invoice {
	t.Helper()
	inv := goldenIntraStateInvoice(t)
	inv.Supplier.Name = "SHREE SIDDHIVINAYAK ENGINEERING AND FABRICATION WORKS PRIVATE LIMITED"
	inv.Recipient.Name = "MAHARASHTRA STATE ELECTRICITY DISTRIBUTION COMPANY LIMITED, NASHIK URBAN CIRCLE"
	inv.ShipTo.Name = "MAHARASHTRA STATE ELECTRICITY DISTRIBUTION COMPANY LIMITED, SATPUR RECEIVING STATION STORES"
	return inv
}

// ---------------------------------------------------------------------------
// the golden tests
// ---------------------------------------------------------------------------

// TestPrintedInvoiceMatchesItsGoldenFile compares the GENERATED BYTES of three
// invoices to committed golden files, and their extracted text to a committed
// text golden.
//
// Regenerate with:
//
//	cd backend && UPDATE_GOLDEN=1 go test ./internal/printing/ -run TestPrintedInvoiceMatchesItsGoldenFile
//
// and read the .txt diff. See the goldenEnv comment for why both files exist.
func TestPrintedInvoiceMatchesItsGoldenFile(t *testing.T) {
	cases := []struct {
		golden string
		inv    Invoice
	}{
		{"invoice_taxinvoice_intrastate", goldenIntraStateInvoice(t)},
		{"invoice_taxinvoice_interstate_igst", goldenInterStateInvoice(t)},
		{"invoice_bill_of_supply", goldenBillOfSupply(t)},
		{"invoice_long_registration_names", goldenLongRegistrationNamesInvoice(t)},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			pdf, err := RenderInvoice(c.inv)
			if err != nil {
				t.Fatalf("the fixture invoice would not print: %v", err)
			}
			assertGolden(t, c.golden, pdf)
		})
	}
}

// assertGolden compares pdf to testdata/<name>.pdf and its extracted text to
// testdata/<name>.txt, or rewrites both when UPDATE_GOLDEN=1.
func assertGolden(t *testing.T, name string, pdf []byte) {
	t.Helper()

	texts, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("the invoice this package just produced cannot be read back, so it is not a valid PDF: %v", err)
	}
	for i, s := range texts {
		if strings.ContainsAny(s, "\r\n") {
			// The .txt golden is one printed string per line, so a printed string
			// containing a newline would silently split into two golden lines.
			t.Fatalf("printed string %d contains a line break (%q); the text golden format cannot represent it", i, s)
		}
	}
	got := strings.Join(texts, "\n") + "\n"

	pdfPath := filepath.Join("testdata", name+".pdf")
	txtPath := filepath.Join("testdata", name+".txt")

	if os.Getenv(goldenEnv) == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("cannot create testdata: %v", err)
		}
		if err := os.WriteFile(pdfPath, pdf, 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", pdfPath, err)
		}
		if err := os.WriteFile(txtPath, []byte(got), 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", txtPath, err)
		}
		t.Logf("regenerated %s and %s (%d bytes, %d printed strings) — READ THE .txt DIFF before committing",
			pdfPath, txtPath, len(pdf), len(texts))
		return
	}

	wantTxt, err := os.ReadFile(txtPath)
	if err != nil {
		t.Fatalf("golden %s is missing; regenerate with %s=1 go test ./internal/printing/ -run TestPrintedInvoiceMatchesItsGoldenFile (%v)", txtPath, goldenEnv, err)
	}
	if got != string(wantTxt) {
		t.Errorf("WHAT IS PRINTED ON THIS INVOICE CHANGED:\n%s", firstTextDifference(string(wantTxt), got))
	}

	wantPDF, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatalf("golden %s is missing; regenerate with %s=1 (%v)", pdfPath, goldenEnv, err)
	}
	if !bytes.Equal(pdf, wantPDF) {
		gotSum := sha256.Sum256(pdf)
		wantSum := sha256.Sum256(wantPDF)
		t.Errorf("the printed bytes of %s moved: %d bytes sha %s, golden %d bytes sha %s. If the .txt golden above is unchanged the difference is geometry only; either way, regenerate with %s=1 and review the diff",
			name, len(pdf), hex.EncodeToString(gotSum[:8]), len(wantPDF), hex.EncodeToString(wantSum[:8]), goldenEnv)
	}
}

// firstTextDifference reports the first line where two text goldens diverge, with
// a little context, because "the bytes differ" is not something a reviewer can
// act on.
func firstTextDifference(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w == g {
			continue
		}
		return fmt.Sprintf("printed string %d:\n  golden: %q\n  now:    %q\n(%d printed strings in the golden, %d now)",
			i+1, w, g, len(wantLines)-1, len(gotLines)-1)
	}
	return "the two texts are identical, so the difference is in the bytes only"
}

// ---------------------------------------------------------------------------
// what has to be on the page
// ---------------------------------------------------------------------------

// printedStrings renders inv and returns every string that ended up on the paper.
func printedStrings(t *testing.T, inv Invoice) []string {
	t.Helper()
	pdf, err := RenderInvoice(inv)
	if err != nil {
		t.Fatalf("the invoice would not print: %v", err)
	}
	texts, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("the invoice cannot be read back: %v", err)
	}
	return texts
}

// countContaining counts the printed strings that contain sub.
func countContaining(texts []string, sub string) int {
	n := 0
	for _, s := range texts {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

// requirePrinted fails unless some printed string contains sub.
func requirePrinted(t *testing.T, texts []string, sub, why string) {
	t.Helper()
	if countContaining(texts, sub) == 0 {
		t.Errorf("%q is not on the printed invoice: %s\nprinted: %q", sub, why, texts)
	}
}

// TestATaxInvoiceCarriesEveryRuleFortySixParticularOnTheFaceOfIt walks the
// clauses and looks for each particular in the text that was actually drawn.
// ValidateRule46 proves the DATA is present; this proves it reached the PAPER,
// which is a different claim — a renderer that validated a field and then forgot
// to draw it would pass every test in rule46_test.go.
func TestATaxInvoiceCarriesEveryRuleFortySixParticularOnTheFaceOfIt(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	texts := printedStrings(t, inv)

	requirePrinted(t, texts, "Mehta Trading Co.", "Rule 46(a): the name of the supplier")
	requirePrinted(t, texts, "14 Laxmi Industrial Estate", "Rule 46(a): the address of the supplier")
	requirePrinted(t, texts, "27AAAPZ1234C1ZS", "Rule 46(a): the GSTIN of the supplier")
	requirePrinted(t, texts, "INV/2026-27/0042", "Rule 46(b): the consecutive serial number")
	requirePrinted(t, texts, "15-Apr-2026", "Rule 46(c): the date of issue")
	requirePrinted(t, texts, "Kulkarni Hardware Stores", "Rule 46(d): the name of the recipient")
	requirePrinted(t, texts, "22 Shivaji Road", "Rule 46(d): the address of the recipient")
	requirePrinted(t, texts, "27AACCK5678L1Z9", "Rule 46(d): the GSTIN of the recipient")
	requirePrinted(t, texts, "6109", "Rule 46(g): the HSN code of a line")
	requirePrinted(t, texts, "998719", "Rule 46(g): the SAC code of the service line")
	requirePrinted(t, texts, "Galvanised steel bracket 12mm", "Rule 46(h): the description of a line")
	requirePrinted(t, texts, "KGS", "Rule 46(i): the unit of the quantity")
	requirePrinted(t, texts, "11,800.00", "Rule 46(j): the total value of supply")
	requirePrinted(t, texts, "10,000.40", "Rule 46(k): the taxable value")
	requirePrinted(t, texts, "CGST @ 9%", "Rule 46(l): the rate of central tax")
	requirePrinted(t, texts, "SGST @ 9%", "Rule 46(l): the rate of State tax")
	requirePrinted(t, texts, "Place of Supply: Maharashtra (27)", "Rule 46(n): the place of supply with the name of the State")
	requirePrinted(t, texts, "Plot D-14, Satpur MIDC", "Rule 46(o): the address of delivery, which differs from the billing address")
	requirePrinted(t, texts, "reverse charge", "Rule 46(p): whether tax is payable on reverse charge")
	requirePrinted(t, texts, invDefaultSignatureLabel, "Rule 46(q): the signature panel")

	// Rule 48(4): once the portal has registered the invoice, the IRN is part of
	// what a registered invoice IS, so it prints.
	requirePrinted(t, texts, "IRN: 35b4f5c1d1b6b6f0f8a5c0d3e2f1a09876543210fedcba9876543210abcdef01", "Rule 48(4): the Invoice Reference Number")
	requirePrinted(t, texts, "Ack No: 112010021000123", "the e-invoice acknowledgement number")
}

// TestEachTaxHeadPrintsItsOwnRateAndItsOwnAmount is clause (l) and clause (m)
// read strictly. A single "GST 18%: 1,800.08" line for a 9 + 9 CGST/SGST supply
// states neither of the two taxes actually charged: the recipient's credit is
// split between two ledgers, GSTR-1 reports the two separately, and a State
// officer looking for State tax finds none.
func TestEachTaxHeadPrintsItsOwnRateAndItsOwnAmount(t *testing.T) {
	texts := printedStrings(t, goldenIntraStateInvoice(t))

	if n := countContaining(texts, "CGST @ 9%"); n != 1 {
		t.Errorf("central tax at 9%% appears %d times on the invoice, want exactly once with its own rate (Rule 46(l))", n)
	}
	if n := countContaining(texts, "SGST @ 9%"); n != 1 {
		t.Errorf("State tax at 9%% appears %d times on the invoice, want exactly once with its own rate (Rule 46(l))", n)
	}
	// Both heads are 900.04, so the amount must appear TWICE: once against each
	// head (Rule 46(m)). One occurrence would mean the two were merged.
	if n := countContaining(texts, "900.04"); n != 2 {
		t.Errorf("the tax amount 900.04 appears %d times, want twice — once against central tax and once against State tax (Rule 46(m)); a merged 1,800.08 satisfies neither clause",
			n)
	}
	if n := countContaining(texts, "1,800.08"); n != 0 {
		t.Errorf("a merged GST amount of 1,800.08 is on the invoice %d times; the two heads must be printed separately", n)
	}
}

// TestATaxInvoiceStatesWhetherTaxIsPayableOnReverseChargeEitherWay is how clause
// (p) is discharged: not by validating a bool, but by ALWAYS printing the answer.
// An invoice with no marker leaves the recipient unable to tell whether it is
// they who owe the tax.
func TestATaxInvoiceStatesWhetherTaxIsPayableOnReverseChargeEitherWay(t *testing.T) {
	on := goldenIntraStateInvoice(t)
	on.ReverseCharge = true
	texts := printedStrings(t, on)
	requirePrinted(t, texts, "Tax payable on reverse charge: Yes",
		"Rule 46(p): the invoice says tax IS payable on reverse charge and the words must be visible on the page")
	if countContaining(texts, "reverse charge: No") != 0 {
		t.Error("an invoice marked reverse charge printed 'No'")
	}

	off := goldenIntraStateInvoice(t)
	off.ReverseCharge = false
	texts = printedStrings(t, off)
	requirePrinted(t, texts, "Tax payable on reverse charge: No",
		"Rule 46(p) requires the document to state WHETHER tax is payable on reverse charge, so silence is not one of the permitted answers")
}

// TestEveryPrintedInvoiceCarriesASignaturePanelForRule46q. What software can
// supply for clause (q) is the PLACE for a signature — "For <company>", a gap wide
// enough to sign in, and the authorised-signatory label — which is then signed by
// hand, or accompanied by the portal's digital signature travelling with the IRN.
func TestEveryPrintedInvoiceCarriesASignaturePanelForRule46q(t *testing.T) {
	for _, inv := range []Invoice{goldenIntraStateInvoice(t), goldenInterStateInvoice(t), goldenBillOfSupply(t)} {
		texts := printedStrings(t, inv)
		requirePrinted(t, texts, "For Mehta Trading Co.", "the signature panel names the supplier signing")
		requirePrinted(t, texts, "Authorised Signatory", "Rule 46(q) / Rule 49(h): the signature or digital signature of the supplier or his authorised representative")
	}
}

// TestACompanysOwnSignatureLabelReplacesTheBuiltInDefault. The default lives in
// code so a fresh company prints on day one; migration 0037's table holds only
// what a company changed.
func TestACompanysOwnSignatureLabelReplacesTheBuiltInDefault(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	inv.Overrides.SignatureLabel = "Proprietor"
	inv.Overrides.Title = "TAX INVOICE (ORIGINAL FOR RECIPIENT)"
	inv.Overrides.Declaration = "Goods once sold will not be taken back."

	texts := printedStrings(t, inv)
	requirePrinted(t, texts, "Proprietor", "the company's own signature label")
	requirePrinted(t, texts, "TAX INVOICE (ORIGINAL FOR RECIPIENT)", "the company's own document title")
	requirePrinted(t, texts, "Goods once sold will not be taken back.", "the company's own declaration")
	if countContaining(texts, invDefaultSignatureLabel) != 0 {
		t.Error("the built-in signature label printed alongside the company's own")
	}
	if countContaining(texts, "actual price of the goods") != 0 {
		t.Error("the built-in declaration printed alongside the company's own")
	}
}

// TestAFreshCompanyWithNoTemplateRowStillPrintsACompleteInvoice. Zero overrides is
// the ORDINARY case and will be for most companies forever, so it is the case that
// must not print a document with no title and no signature label.
func TestAFreshCompanyWithNoTemplateRowStillPrintsACompleteInvoice(t *testing.T) {
	inv := goldenInterStateInvoice(t)
	inv.Overrides = TemplateOverrides{}
	texts := printedStrings(t, inv)
	requirePrinted(t, texts, invDefaultTaxInvoiceTitle, "the built-in title")
	requirePrinted(t, texts, invDefaultSignatureLabel, "the built-in signature label")
	requirePrinted(t, texts, "actual price of the goods", "the built-in declaration")
}

// TestTheWordsAndTheFiguresOnOneInvoiceAgree. The words are the operative amount
// on a document that settles a debt, and both come from the same decimal through
// the same quantization; an invoice that says 11,800.00 in the total box and
// "Eleven Thousand Seven Hundred Ninety Nine" in the words is a dispute.
func TestTheWordsAndTheFiguresOnOneInvoiceAgree(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	texts := printedStrings(t, inv)

	figures := FormatMoney(inv.Total) // "11,800.00"
	words := AmountInWords(inv.Total) // "Rupees Eleven Thousand Eight Hundred Only"

	requirePrinted(t, texts, figures, "the total in figures")
	requirePrinted(t, texts, words, "the same total in words")

	// And the words on the page are the words for the figure on the page — not for
	// some other amount that happens to be nearby.
	if !strings.Contains(strings.Join(texts, "\n"), "Amount in Words: "+words) {
		t.Errorf("the amount in words on the invoice is not the words for %s: %q", figures, texts)
	}
}

// TestTheAmountInWordsFollowsTheLedgersFourthDecimalPlace uses a total whose
// fourth decimal place decides the second: 11800.4450 renders 11,800.45 in
// figures, so the words must say Forty Five Paise and not Forty Four.
func TestTheAmountInWordsFollowsTheLedgersFourthDecimalPlace(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	inv.Total = dec(t, "11800.4450")
	texts := printedStrings(t, inv)
	joined := strings.Join(texts, "\n")

	if !strings.Contains(joined, "11,800.45") {
		t.Errorf("the total 11800.4450 must print as 11,800.45: %q", texts)
	}
	if !strings.Contains(joined, "Forty Five Paise") {
		t.Errorf("the figures say 11,800.45 and the words must say Forty Five Paise: %q", texts)
	}
	if strings.Contains(joined, "Forty Four Paise") {
		t.Error("the figures round to 45 paise and the words truncated to 44; the words are the legally operative amount")
	}
}

// TestAnInvoiceWhoseFiguresDoNotAddUpPrintsThemAsGivenAndFixesNothing documents a
// DECISION, and the decision is: this renderer prints what the ledger holds and
// never re-derives it.
//
// The invoice below has three lines totalling 10,000.40 and a stored taxable value
// of 9,000.00 — they disagree, which means the voucher that posted it disagrees
// with itself. The renderer prints 9,000.00, because that is the number in the
// books: a renderer that "corrected" it to 10,000.40 would produce a document
// that no ledger, no return and no reconciliation could reproduce, and nobody
// would ever be told. THE MISMATCH IS A POSTING DEFECT and belongs to the layer
// that posted it; making it visible on the paper is what gets it found.
func TestAnInvoiceWhoseFiguresDoNotAddUpPrintsThemAsGivenAndFixesNothing(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	inv.Subtotal = dec(t, "9000.00") // the lines say 10,000.40

	texts := printedStrings(t, inv)
	requirePrinted(t, texts, "9,000.00", "the stored taxable value, printed as given")
	if countContaining(texts, "10,000.40") != 0 {
		t.Error("the renderer re-derived the taxable value from the lines; the printed invoice must show the number the ledger holds, even when the ledger is wrong")
	}
	// The line amounts are equally untouched. Line 3 is one unit at a listed rate
	// of 8,438.00 less 20 per cent, and the voucher stored 6,750.40: the rate
	// appears ONCE, in the rate column, and never a second time as the amount.
	requirePrinted(t, texts, "6,750.40", "the third line's stored amount")
	if countContaining(texts, "8,438.00") > 1 {
		t.Error("the line amount looks recomputed from quantity x rate; the voucher's amount is net of a discount the renderer must not apply itself")
	}
}

// TestADescriptionWithParenthesesABackslashAndAPercentSurvivesToThePage. A PDF
// string literal is delimited by parentheses: an unescaped ")" closes it early and
// the rest of the page is parsed as operators. Real descriptions contain
// "Widget (Large) \ 50%", so this is not theoretical.
func TestADescriptionWithParenthesesABackslashAndAPercentSurvivesToThePage(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	hostile := `Widget (Large) \ 50% cotton blend`

	texts := printedStrings(t, inv)
	found := false
	for _, s := range texts {
		if s == hostile {
			found = true
		}
	}
	if !found {
		t.Fatalf("the description %q did not come back off the page verbatim, so either it was mangled or it broke out of its string literal: %q", hostile, texts)
	}
	// And the page after it is intact: the totals still print.
	requirePrinted(t, texts, "11,800.00", "the total, which follows the hostile description on the page")
}

// ---------------------------------------------------------------------------
// the bill of supply
// ---------------------------------------------------------------------------

// TestABillOfSupplyCarriesNoTaxColumnAndSaysWhatItIs. Rule 49 governs a document
// where no tax is charged. A bill of supply with a tax row on it, or headed "Tax
// Invoice", tells the recipient they hold something they can claim credit
// against — and they cannot.
func TestABillOfSupplyCarriesNoTaxColumnAndSaysWhatItIs(t *testing.T) {
	texts := printedStrings(t, goldenBillOfSupply(t))
	joined := strings.Join(texts, "\n")

	requirePrinted(t, texts, "Bill of Supply", "Rule 49: the document must carry the Bill of Supply title")
	if strings.Contains(joined, invDefaultTaxInvoiceTitle) {
		t.Error("a bill of supply is headed 'Tax Invoice'; the recipient would look for an input tax credit that does not exist")
	}
	for _, head := range []string{"CGST", "SGST", "IGST", "Cess", "@ 9%", "@ 18%"} {
		if strings.Contains(joined, head) {
			t.Errorf("%q appears on a bill of supply, which charges no tax at all", head)
		}
	}
	if strings.Contains(joined, "reverse charge") {
		t.Error("a bill of supply carries a reverse-charge marker; Rule 49 has no such particular and there is no tax to be liable for")
	}
	if strings.Contains(joined, "Taxable Value") {
		t.Error("a bill of supply calls its value 'Taxable Value'; Rule 49(g) is the value of supply, and nothing on this document is taxable")
	}
	requirePrinted(t, texts, "Value of Supply", "Rule 49(g): the value of supply")
	requirePrinted(t, texts, "4,500.00", "the value of supply in figures")
	requirePrinted(t, texts, "BOS/2026-27/0009", "Rule 49(b): the serial number")
	requirePrinted(t, texts, "Authorised Signatory", "Rule 49(h): the signature")
}

// TestAnUnregisteredRecipientIsPrintedAsUnregisteredAndNotAsABlank. A B2C invoice
// is legal; a B2B invoice with the customer's GSTIN dropped is a lost input tax
// credit. The two must not look the same on paper.
func TestAnUnregisteredRecipientIsPrintedAsUnregisteredAndNotAsABlank(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	inv.Recipient.GSTIN = ""
	inv.ShipTo = nil
	texts := printedStrings(t, inv)
	requirePrinted(t, texts, "GSTIN: Unregistered", "an unregistered recipient is stated, not left blank")
}

// TestAnInvoiceWithNoSeparateDeliveryAddressSaysSoRatherThanLeavingABox. An empty
// box reads as a field somebody forgot to fill in, and Rule 46(o) only bites when
// the delivery address DIFFERS.
func TestAnInvoiceWithNoSeparateDeliveryAddressSaysSoRatherThanLeavingABox(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	inv.ShipTo = nil
	texts := printedStrings(t, inv)
	requirePrinted(t, texts, "Same as billed to", "the goods go to the billing address, and the document says so")
}

// ---------------------------------------------------------------------------
// the header block holds its columns
// ---------------------------------------------------------------------------

// invHeaderOps draws ONLY the header of inv, onto a page whose bytes are thrown
// away, and reads back every string that was placed together with the geometry it
// was placed at. Drawing the header alone is what makes the assertions below about
// THE HEADER and not about whatever the item table happened to contain.
//
// It reuses reportParseTextOps (report_test.go, same package), which parses the
// one shape Page.Text emits. The ParseFloat calls in there read a FONT SIZE and an
// (x,y) in POINTS; see the note above it.
func invHeaderOps(t *testing.T, inv Invoice) []reportTextOp {
	t.Helper()
	p := NewDoc(A4Portrait).NewPage()
	invDrawHeader(p, inv, invTitle(inv), inv.Kind == KindTaxInvoice)
	return reportParseTextOps(t, p.content(), 0)
}

// invOpFont resolves the /Fn resource name in a content stream back to the Font
// it was drawn with, so the string can be measured with the metrics it was set in.
func invOpFont(t *testing.T, resource string) Font {
	t.Helper()
	for _, f := range allFonts {
		if f.resourceName() == resource {
			return f
		}
	}
	t.Fatalf("the header was drawn with font resource %q, which is not one of the five base-14 fonts this package emits", resource)
	return Helvetica
}

// invOpWidth and invOpRight are the drawn string's width and its right-hand edge,
// in points. GEOMETRY, not money: no amount on any of these documents passes
// through a float64 (every figure is a decimal formatted by FormatMoney and
// compared as a string).
func invOpWidth(t *testing.T, op reportTextOp) float64 {
	t.Helper()
	return Measure(invOpFont(t, op.font), op.size, op.text)
}

func invOpRight(t *testing.T, op reportTextOp) float64 {
	t.Helper()
	return op.x + invOpWidth(t, op)
}

// invSameBaselineTolerance is how far apart two baselines may be and still be two
// strings a reader sees on ONE line of the page.
//
// It is 6 points, which is chosen between two numbers in the layout. The left-hand
// column advances in steps of invLineHeight (11) and the right-hand meta column in
// steps of invTightLine (9.5), so the two columns drift 1.5 points apart and stay
// there — at 8.5pt text those two strings unquestionably collide, so the tolerance
// must be ABOVE 1.5. Consecutive lines inside one column are 9.5 points apart and
// must NOT be treated as sharing a line, so it must be BELOW 9.5.
const invSameBaselineTolerance = 6.0

// invApart is |a-b| for two page coordinates.
func invApart(a, b float64) float64 {
	if a < b {
		return b - a
	}
	return a - b
}

// TestNothingInTheInvoiceHeaderIsDrawnOverItsNeighbourOrOffTheSheet is a PROPERTY
// OF THE BLOCK, not a golden diff, and that is deliberate: a golden only catches
// the string that is in the golden, and the next long name is a different string.
//
// THE BUG IT EXISTS FOR. The supplier, recipient and ship-to NAMES were the only
// strings in the header drawn without Wrap, while every address line beneath each
// of them was wrapped to invBlockWidth. A 69-character registration name — an
// ordinary one — therefore ran through the Rule 46(b) invoice number on the same
// baseline, through the Shipped-to block, and off the right edge of the paper,
// where the rest of it does not print at all. The invBlockWidth comment asserts
// that the blocks "cannot overlap"; until this test, nothing checked it.
//
// Three claims, each with its own failure message:
//
//	(a) nothing is drawn past the right edge of the printable area;
//	(b) no two strings on one line of the page overprint each other;
//	(c) inside the letterhead-and-party band, every string in a column starts at
//	    the column's x and ends within invBlockWidth of it.
func TestNothingInTheInvoiceHeaderIsDrawnOverItsNeighbourOrOffTheSheet(t *testing.T) {
	cases := []struct {
		what string
		inv  Invoice
	}{
		{"the intra-State fixture", goldenIntraStateInvoice(t)},
		{"the inter-State fixture, which has no ship-to block", goldenInterStateInvoice(t)},
		{"a bill of supply", goldenBillOfSupply(t)},
		{"registration names of the length the registrar actually issues", goldenLongRegistrationNamesInvoice(t)},
		{"a supplier with no ship-to and an unregistered recipient", func() Invoice {
			inv := goldenLongRegistrationNamesInvoice(t)
			inv.ShipTo = nil
			inv.Recipient.GSTIN = ""
			return inv
		}()},
	}

	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			ops := invHeaderOps(t, c.inv)
			if len(ops) == 0 {
				t.Fatal("the header drew nothing at all")
			}

			// (a) OFF THE SHEET. Text drawn past invMarginRight is not clipped by a
			// reader, it is simply outside the printable area of the paper: the
			// characters past the edge are gone and nobody can tell they were meant
			// to be there.
			for _, op := range ops {
				if right := invOpRight(t, op); right > invMarginRight+0.01 {
					t.Errorf("the header draws %q from x=%.1f to x=%.1f, past the right edge of the printable area at x=%.1f; the last %.1f points of it are off the sheet and do not print",
						op.text, op.x, right, invMarginRight, right-invMarginRight)
				}
			}

			// (b) OVERPRINTING. Two strings a reader sees on one line, one of which
			// reaches into the other, are ink on top of ink.
			for i := range ops {
				for j := range ops {
					left, right := ops[i], ops[j]
					if i == j || invApart(left.y, right.y) >= invSameBaselineTolerance {
						continue
					}
					if left.x > right.x || (left.x == right.x && i > j) {
						continue // each pair is considered once, leftmost first
					}
					if leftEnd := invOpRight(t, left); leftEnd > right.x+0.01 {
						t.Errorf("on one line of the header (baselines y=%.1f and y=%.1f) %q is drawn from x=%.1f to x=%.1f and %q starts at x=%.1f: the two overprint each other on the paper, and neither can be read",
							left.y, right.y, left.text, left.x, leftEnd, right.text, right.x)
					}
				}
			}

			// (c) THE COLUMN WIDTH ITSELF. The letterhead-and-party band is two
			// invBlockWidth columns side by side; a string that leaves its column has
			// left the geometry that keeps the three blocks apart, even if the
			// particular fixture happens to have nothing beside it to collide with.
			band := ops[:invHeaderBandEnd(ops)]
			for _, op := range band {
				var column float64
				switch {
				case invApart(op.x, invMarginLeft) < 0.01:
					column = invMarginLeft
				case invApart(op.x, invConsigneeLeft) < 0.01:
					column = invConsigneeLeft
				default:
					continue // right-aligned meta lines are not column-left strings
				}
				if w := invOpWidth(t, op); w > invBlockWidth+0.01 {
					t.Errorf("%q is drawn in the header column at x=%.0f and is %.1f points wide, over the %.0f-point invBlockWidth the column is wrapped to; that width is the only thing keeping the supplier block, the invoice-number block and the shipped-to block from overlapping",
						op.text, column, w, invBlockWidth)
				}
			}
		})
	}
}

// invHeaderBandEnd is the index one past the last string of the
// letterhead-and-party band: everything from the supplier name down to the last
// address line, which is the part of the header laid out in two invBlockWidth
// columns.
//
// It ends at the first string the renderer draws that belongs to a FULL-WIDTH row
// instead — the place-of-supply line, the reverse-charge marker, or the "Sl"
// column heading, whichever comes first. Those three are legitimately allowed to
// be wider than a block.
func invHeaderBandEnd(ops []reportTextOp) int {
	for i, op := range ops {
		if op.text == "Sl" ||
			strings.HasPrefix(op.text, "Place of Supply") ||
			strings.HasPrefix(op.text, "Tax payable on reverse charge") {
			return i
		}
	}
	return len(ops)
}

// TestALongRegistrationNameIsWrappedAndNotTruncated. Wrapping is the fix; SHORTENING
// would be a different bug with the same test result if the test only looked at
// geometry. A recipient's registered name is what the invoice must state (Rule
// 46(d)) and a name cut off at 40 characters names a different legal person.
func TestALongRegistrationNameIsWrappedAndNotTruncated(t *testing.T) {
	inv := goldenLongRegistrationNamesInvoice(t)
	texts := printedStrings(t, inv)

	// Wrap breaks on spaces and collapses runs of blanks, so the fragments of one
	// name, joined back with single spaces in draw order, are the name.
	joined := strings.Join(texts, " ")
	for _, name := range []string{inv.Supplier.Name, inv.Recipient.Name, inv.ShipTo.Name} {
		if !strings.Contains(joined, name) {
			t.Errorf("the name %q is not on the printed invoice in full; a truncated registered name identifies a different legal person than the one the supply was made to (Rule 46(a) and 46(d))", name)
		}
	}

	// And it really was broken into lines rather than drawn as one long string.
	for _, op := range invHeaderOps(t, inv) {
		if op.text == inv.Supplier.Name || op.text == inv.Recipient.Name || op.text == inv.ShipTo.Name {
			t.Errorf("%q was drawn as a single string; it is %.1f points wide against a %.0f-point column",
				op.text, invOpWidth(t, op), invBlockWidth)
		}
	}
}

// ---------------------------------------------------------------------------
// the discount is a percentage
// ---------------------------------------------------------------------------

// TestADiscountedLinePrintsThePercentageTheBooksHoldAndNotARupeeFigure.
//
// LineItem.DiscountPct carries stock_movement.discount_pct, which is a PERCENTAGE
// (the column CHECKs 0..100). Printed through FormatMoney it read "Less: Discount
// 2.50" on a line discounted by Rs 25.00 — four figures on the face of a tax
// invoice that cannot all be true, understating the discount by qty x rate / 100,
// and contradicting the Rule 46(k) taxable value printed on the same row.
//
// The fixture's third line is one unit at 8,438.00 less 20 per cent, and the
// voucher stored 6,750.40. The rupee discount of 1,687.60 is deliberately NOT
// printed: the books hold the percentage and the already-net amount, so a rupee
// figure derived here would be a number on the paper that no posting holds.
func TestADiscountedLinePrintsThePercentageTheBooksHoldAndNotARupeeFigure(t *testing.T) {
	discountLine := func(t *testing.T, pct string) string {
		t.Helper()
		inv := goldenIntraStateInvoice(t)
		inv.Items[2].DiscountPct = dec(t, pct)
		var found []string
		for _, s := range printedStrings(t, inv) {
			if strings.HasPrefix(s, "Less: Discount") {
				found = append(found, s)
			}
		}
		if len(found) != 1 {
			t.Fatalf("a line discounted by %s per cent produced %d discount lines on the invoice (%q); Rule 46(k) wants the recipient to see how the taxable value was arrived at", pct, len(found), found)
		}
		return found[0]
	}

	if got, want := discountLine(t, "20"), "Less: Discount 20%"; got != want {
		t.Errorf("the invoice states %q for a line discounted by 20 per cent, want %q; without the per-cent sign the figure reads as rupees, and Rs 20.00 off a listed 8,438.00 is not the discount the voucher stored", got, want)
	}

	// 12.5 is the discriminating case: FormatMoney renders it "12.50", which is
	// indistinguishable from twelve rupees fifty paise, and FormatRate renders it
	// "12.5", which can only be a rate.
	got := discountLine(t, "12.5")
	if got != "Less: Discount 12.5%" {
		t.Errorf("the invoice states %q for a line discounted by 12.5 per cent, want %q", got, "Less: Discount 12.5%")
	}
	if strings.Contains(got, "12.50") {
		t.Errorf("the discount is rendered with FormatMoney (%q): a rupee-shaped figure in the discount line contradicts the taxable value printed beside it", got)
	}

	// A line with no discount says nothing at all, rather than "Less: Discount 0%".
	inv := goldenIntraStateInvoice(t)
	inv.Items[2].DiscountPct = dec(t, "0")
	if n := countContaining(printedStrings(t, inv), "Less: Discount"); n != 0 {
		t.Errorf("an undiscounted invoice printed %d discount line(s); a discount of nothing is not a particular of Rule 46", n)
	}
}

// ---------------------------------------------------------------------------
// the rule 48(4) QR code, which this package cannot produce
// ---------------------------------------------------------------------------

// TestAnInvoiceWithAnIRNDisclosesOnItsFaceThatTheQRCodeIsNotPrinted.
//
// Rule 48(4) makes an e-invoice the copy prepared through the Invoice Registration
// Portal, and the copy issued to the recipient must carry the Quick Response code
// the portal returned. THIS PACKAGE CANNOT PRODUCE THAT CODE — the QR encodes the
// signed payload the IRP hands back, and no NIC/IRP call can be made from this
// deployment. Printing "IRN:" and nothing else made the sheet LOOK like a complete
// rule 48(4) copy, so the gap is now stated on the paper. THIS TEST PINS THE
// DISCLOSURE, NOT A FIX: the document is still not a valid e-invoice copy.
func TestAnInvoiceWithAnIRNDisclosesOnItsFaceThatTheQRCodeIsNotPrinted(t *testing.T) {
	registered := goldenIntraStateInvoice(t) // carries an IRN and an Ack No
	joined := strings.Join(printedStrings(t, registered), " ")
	if !strings.Contains(joined, invQRNotPrintedNotice) {
		t.Errorf("an invoice that carries an IRN prints no notice that the rule 48(4) QR code is missing; the sheet reads as a complete e-invoice copy and somebody hands it over.\nwant on the page: %q\nprinted: %s",
			invQRNotPrintedNotice, joined)
	}
	if !strings.Contains(joined, "48(4)") {
		t.Error("the notice does not name rule 48(4), so it does not tell the reader which obligation is unmet")
	}

	// Every page carries it, for the same reason every page carries the invoice
	// number: page two can be detached from page one.
	pdf, err := RenderInvoice(registered)
	if err != nil {
		t.Fatalf("the fixture would not print: %v", err)
	}
	pages := reportPagesText(t, pdf)
	for i, ops := range pages {
		if !strings.Contains(strings.Join(reportPageStrings(ops), " "), invQRNotPrintedNotice) {
			t.Errorf("page %d of %d carries an IRN header but not the missing-QR notice", i+1, len(pages))
		}
	}

	// An invoice that was never registered says nothing about a QR code: there is
	// no rule 48(4) obligation to disclose against, and a notice about a missing
	// code on a document that needs none is noise that trains operators to ignore
	// it.
	plain := goldenIntraStateInvoice(t)
	plain.IRN = ""
	plain.AckNo = ""
	if n := countContaining(printedStrings(t, plain), "QR code"); n != 0 {
		t.Errorf("an invoice with no IRN mentions a QR code %d time(s); rule 48(4) does not apply to it", n)
	}
}

// ---------------------------------------------------------------------------
// refusal, pagination, determinism
// ---------------------------------------------------------------------------

// TestARefusedInvoicePrintsNothingAtAll. A refusal is caught at the counter by
// somebody who can fix the voucher; a printed invoice with a blank place of supply
// is caught by an officer months later. So the bytes must not exist.
func TestARefusedInvoicePrintsNothingAtAll(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	inv.Supplier.GSTIN = ""
	inv.PlaceOfSupply = ""

	pdf, err := RenderInvoice(inv)
	if err == nil {
		t.Fatal("an invoice with no supplier GSTIN was printed; Rule 46(a) makes it invalid")
	}
	if pdf != nil {
		t.Fatalf("a refused invoice still produced %d bytes; a partially-drawn tax invoice must never reach a printer", len(pdf))
	}
	if !strings.Contains(err.Error(), "supplier.gstin") || !strings.Contains(err.Error(), "46(a)") {
		t.Errorf("the refusal does not tell the operator what to fix: %v", err)
	}
}

// TestARefusalCausedByTheLetterheadNamesTheFieldToShortenAndNeverReportsNegativeSpace.
//
// THE MESSAGE IS THE WHOLE FEATURE OF A REFUSAL. One combined check —
// "headerBottom - totalsHeight < invBodyBottom" — could not tell "the totals block
// is too tall" from "the header consumed the whole sheet", so it blamed the
// company's template overrides for both and reported a NEGATIVE number of
// available points. An operator handed that message deletes their bank details and
// their terms, prints again, and it still refuses, because what did not fit was one
// enormous address line typed into the ledger.
func TestARefusalCausedByTheLetterheadNamesTheFieldToShortenAndNeverReportsNegativeSpace(t *testing.T) {
	// A single monstrous address line, and NO template overrides at all, so a
	// message that blames the overrides is provably blaming the wrong thing.
	inv := goldenInterStateInvoice(t)
	inv.Overrides = TemplateOverrides{}
	inv.Supplier.Address2 = strings.TrimSpace(strings.Repeat("Gat No One Two Three, Village Kuruli, Taluka Khed, near the Chakan Talegaon Road, ", 60))

	pdf, err := RenderInvoice(inv)
	if err == nil {
		t.Fatal("an invoice whose letterhead fills the sheet was printed; the item rows and the totals would have been drawn off the bottom of the paper")
	}
	if pdf != nil {
		t.Fatalf("a refused invoice still produced %d bytes", len(pdf))
	}
	msg := err.Error()

	requireNoNegativeQuantity(t, msg)
	for _, want := range []string{"a supplier address or letterhead line", "Village Kuruli"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not tell the operator which field to shorten (looked for %q): %s", want, msg)
		}
	}
	if strings.Contains(msg, "template overrides") {
		t.Errorf("the refusal blames the company's template overrides on an invoice that has none, so the operator deletes settings for nothing: %s", msg)
	}
	// And it QUOTES A PREFIX of the offending field, not the whole of it: the field
	// that fills an A4 sheet is thousands of characters, and a refusal that repeats
	// all of them is a message nobody reads to the end.
	if len(msg) > 600 {
		t.Errorf("the refusal is %d characters long because it quotes the whole offending field back; it needs to be short enough to be read: %s...", len(msg), msg[:200])
	}
	if !strings.Contains(msg, "4919 characters") {
		t.Errorf("the refusal does not state how long the offending field is, which is the number that tells the operator it was a paste accident: %s", msg)
	}

	// The other half of the split: a totals block that does not fit in what the
	// header left. HERE the overrides ARE the cause, and the message says which.
	tall := goldenInterStateInvoice(t)
	tall.Overrides.Declaration = strings.TrimSpace(strings.Repeat("All disputes are subject to the exclusive jurisdiction of the courts at Pune, and interest accrues on any overdue amount. ", 40))

	pdf, err = RenderInvoice(tall)
	if err == nil || pdf != nil {
		t.Fatalf("an invoice whose declaration fills the sheet printed anyway (%d bytes, err %v)", len(pdf), err)
	}
	msg = err.Error()
	requireNoNegativeQuantity(t, msg)
	for _, want := range []string{"the totals block needs", "template overrides", "the declaration"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal for an over-long declaration does not say what to shorten (looked for %q): %s", want, msg)
		}
	}
}

// requireNoNegativeQuantity fails if the message reports a negative measurement.
// "only -212 points are available on a page" is not a quantity an operator can act
// on, and it is what "the header used the whole sheet" looked like when it was
// reported through the arithmetic for "the totals are too tall".
func requireNoNegativeQuantity(t *testing.T, msg string) {
	t.Helper()
	for i := 0; i+1 < len(msg); i++ {
		if msg[i] == '-' && msg[i+1] >= '0' && msg[i+1] <= '9' {
			t.Errorf("the refusal reports a negative measurement, which cannot be acted on: %s", msg)
			return
		}
	}
}

// TestRenderInvoiceRefusesADocumentThatIsNotAnInvoice. A delivery note has its own
// renderer and its own rule; printing one through the invoice template would put
// tax rows on a document that carries no tax and validate it against clauses that
// do not govern it.
func TestRenderInvoiceRefusesADocumentThatIsNotAnInvoice(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	inv.Kind = KindDeliveryNote
	pdf, err := RenderInvoice(inv)
	if err == nil || pdf != nil {
		t.Fatalf("a delivery note was printed as a tax invoice (%d bytes, err %v)", len(pdf), err)
	}
}

// TestASixtyLineInvoicePaginatesRepeatsItsHeadersAndTotalsOnlyOnce is the
// pagination contract, and the third clause is the one that matters: A TOTAL ON
// PAGE ONE OF A TWO-PAGE INVOICE IS THE TOTAL OF PAGE ONE'S LINES, and a customer
// who pays it pays the wrong amount.
func TestASixtyLineInvoicePaginatesRepeatsItsHeadersAndTotalsOnlyOnce(t *testing.T) {
	inv := goldenInterStateInvoice(t)
	inv.Items = nil
	for i := 1; i <= 60; i++ {
		inv.Items = append(inv.Items, LineItem{
			SerialNo:    i,
			Description: fmt.Sprintf("Widget assembly, part number WA-%03d", i),
			HSN:         "8479",
			Quantity:    dec(t, "2"),
			Unit:        "NOS",
			Rate:        dec(t, "50.00"),
			Amount:      dec(t, "100.00"),
		})
	}
	inv.Subtotal = dec(t, "6000.00")
	inv.Taxes = []TaxHead{{Name: "IGST", Rate: dec(t, "18"), Amount: dec(t, "1080.00")}}
	inv.Total = dec(t, "7080.00")

	pdf, err := RenderInvoice(inv)
	if err != nil {
		t.Fatalf("a sixty-line invoice would not print: %v", err)
	}
	texts, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("the sixty-line invoice cannot be read back: %v", err)
	}
	joined := strings.Join(texts, "\n")

	// More than one page: sixty lines do not fit on an A4 sheet under a full
	// letterhead, and a renderer that drew them anyway would have written the last
	// twenty off the bottom of the paper.
	pageCount := bytes.Count(pdf, []byte("/Type /Page /Parent"))
	if pageCount < 2 {
		t.Fatalf("a sixty-line invoice printed on %d page(s); the lines that did not fit would have been drawn off the sheet", pageCount)
	}
	if !bytes.Contains(pdf, []byte("/Type /Pages /Count "+fmt.Sprint(pageCount))) {
		t.Errorf("the page tree does not declare %d pages, so some readers will show a blank document", pageCount)
	}

	// Every line reached the paper.
	for _, want := range []string{"WA-001", "WA-030", "WA-060"} {
		if !strings.Contains(joined, want) {
			t.Errorf("line %s is not on the printed invoice; sixty lines went in", want)
		}
	}

	// The column headers repeat: a continuation sheet of bare figures is
	// unreadable.
	if n := countContaining(texts, "HSN/SAC"); n != pageCount {
		t.Errorf("the column headers appear on %d of %d pages; a page of unlabelled figures cannot be read", n, pageCount)
	}
	// So does the invoice number, because page two can be detached from page one.
	if n := countContaining(texts, "INV/2026-27/0043"); n != pageCount {
		t.Errorf("the invoice number appears on %d of %d pages; a detached continuation sheet could not be filed against anything", n, pageCount)
	}

	// THE TOTAL APPEARS EXACTLY ONCE, and on the LAST page.
	total := FormatMoney(inv.Total) // "7,080.00"
	if n := countContaining(texts, total); n != 1 {
		t.Fatalf("the total %s appears %d times on a %d-page invoice, want exactly once; a per-page total is the total of that page's lines and a customer who pays it pays the wrong amount",
			total, n, pageCount)
	}
	firstPageFooter := indexOf(texts, fmt.Sprintf("Page 1 of %d", pageCount))
	totalAt := indexOfContaining(texts, total)
	if firstPageFooter < 0 {
		t.Fatalf("no page footer says 'Page 1 of %d': %q", pageCount, texts)
	}
	if totalAt < firstPageFooter {
		t.Errorf("the total is drawn before the end of page 1, so it is not the total of the whole invoice")
	}
	if !strings.Contains(joined, fmt.Sprintf("Page %d of %d", pageCount, pageCount)) {
		t.Errorf("the last page is not numbered 'Page %d of %d', so a recipient cannot tell whether a sheet is missing", pageCount, pageCount)
	}
	if !strings.Contains(joined, "Continued ...") {
		t.Error("a page that is not the last carries no continuation marker, so a reader cannot tell the invoice goes on")
	}
	// And no page carries a running subtotal, which the renderer would have to
	// compute by adding up money it was handed.
	if strings.Contains(joined, "Carried Forward") || strings.Contains(joined, "Carried forward") {
		t.Error("a carried-forward subtotal appears; computing one would mean this renderer adding up money and then disagreeing with the voucher")
	}
}

// indexOf returns the position of the first printed string equal to s, or -1.
func indexOf(texts []string, s string) int {
	for i, got := range texts {
		if got == s {
			return i
		}
	}
	return -1
}

// indexOfContaining returns the position of the first printed string containing
// sub, or -1.
func indexOfContaining(texts []string, sub string) int {
	for i, got := range texts {
		if strings.Contains(got, sub) {
			return i
		}
	}
	return -1
}

// TestTheSameInvoicePrintsByteIdenticalOutputEveryTime. Without this, no golden
// test in this file means anything: a renderer whose output moved between two runs
// could not be told apart from a regression.
func TestTheSameInvoicePrintsByteIdenticalOutputEveryTime(t *testing.T) {
	first, err := RenderInvoice(goldenIntraStateInvoice(t))
	if err != nil {
		t.Fatalf("the fixture invoice would not print: %v", err)
	}
	second, err := RenderInvoice(goldenIntraStateInvoice(t))
	if err != nil {
		t.Fatalf("the fixture invoice would not print the second time: %v", err)
	}
	if !bytes.Equal(first, second) {
		a := sha256.Sum256(first)
		b := sha256.Sum256(second)
		t.Fatalf("two renders of the same invoice differ (sha %s vs %s); no golden test on printed output can mean anything",
			hex.EncodeToString(a[:8]), hex.EncodeToString(b[:8]))
	}
}

// TestAnInvoiceRefusesToPrintANameItCannotSpell. WinAnsi has no Devanagari, so a
// party name in Marathi would print as random accented Latin letters — a
// customer's name rendered as garbage on their own invoice. The document refuses
// instead, and the caller (which holds the transliteration) is told.
func TestAnInvoiceRefusesToPrintANameItCannotSpell(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	inv.Recipient.Name = "कुलकर्णी हार्डवेअर"

	pdf, err := RenderInvoice(inv)
	if err == nil {
		t.Fatal("an invoice with a Devanagari party name printed; the name would appear as garbage Latin letters")
	}
	if pdf != nil {
		t.Fatalf("a refused invoice produced %d bytes", len(pdf))
	}
}

// TestTemplateOverridesReachThePaper. The override store exists so a company can
// put its works address, terms and bank details on its own invoices; an override
// that is saved and never printed is a customisation that silently does not apply.
func TestTemplateOverridesReachThePaper(t *testing.T) {
	texts := printedStrings(t, goldenIntraStateInvoice(t))
	requirePrinted(t, texts, "Works: Plot 7, Chakan MIDC, Pune 410501", "an extra letterhead line")
	requirePrinted(t, texts, "Payment due within 30 days.", "the company's terms")
	requirePrinted(t, texts, "Bank: State Bank of India, Karve Road Branch", "the bank details the company asked to show")
	// One space, not the three the fixture stored: Wrap collapses runs of blanks
	// (see font.go), because a description or a bank line pasted out of a
	// spreadsheet is full of them and they must not become visible ragged gaps.
	requirePrinted(t, texts, "A/c: 30012345678 IFSC: SBIN0001234", "the account number and IFSC")
	requirePrinted(t, texts, "Subject to Pune jurisdiction", "the footer line")
}

// TestTheInvoiceRendererNeitherFloatsMoneyNorArithmeticsIt is a STATIC gate over
// invoice.go, and it enforces something stronger than "no floats": THE RENDERER
// DOES NO MONEY ARITHMETIC AT ALL.
//
// Both halves matter and they fail differently. A float64 total drifts by a paisa
// and produces an invoice a customer disputes. A perfectly exact decimal ADDITION
// is worse: it produces a document whose total is right by its own reckoning and
// disagrees with the voucher that posted it, and there is no way to tell from the
// paper which of the two is the books. So Add, Sub, Mul and Div are banned here
// alongside float64 conversions, and the only permitted float in the file is a
// PAGE COORDINATE.
//
// It parses without comments, so the prose above cannot trip it.
func TestTheInvoiceRendererNeitherFloatsMoneyNorArithmeticsIt(t *testing.T) {
	banned := map[string]string{
		"ParseFloat":     "parses money through a float",
		"FormatFloat":    "renders money through a float",
		"Float64":        "converts a decimal to a float",
		"InexactFloat64": "converts a decimal to a float, inexactly and by name",
		"NewFromFloat":   "builds a decimal from a float",
		"IntPart":        "converts a decimal through int64 and SILENTLY WRAPS above 9.22e18",
		"Add":            "adds money, and every total on this document was already computed by the voucher",
		"Sub":            "subtracts money, and a discount or a round-off the renderer applies is in no ledger",
		"Mul":            "multiplies money, which is how a line amount stops matching the posting it came from",
		"Div":            "divides money, and a rate this renderer derived is not the rate that was charged",
		"Sum":            "sums money, and a total this renderer computed cannot be reconciled to anything",
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "invoice.go", nil, 0 /* comments discarded */)
	if err != nil {
		t.Fatalf("cannot parse invoice.go: %v", err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if why, bad := banned[id.Name]; bad {
			t.Errorf("invoice.go:%d uses %s, which %s; every amount on the page must be the decimal the caller handed over, rendered through FormatMoney",
				fset.Position(id.Pos()).Line, id.Name, why)
		}
		return true
	})
}

// TestBankDetailsAreWithheldUntilTheCompanyAsksForThem. ShowBankDetails is a
// separate switch from the lines themselves: a company that has stored its account
// number but not asked for it on invoices has not asked for it on invoices.
func TestBankDetailsAreWithheldUntilTheCompanyAsksForThem(t *testing.T) {
	inv := goldenIntraStateInvoice(t)
	inv.Overrides.ShowBankDetails = false
	texts := printedStrings(t, inv)
	if countContaining(texts, "30012345678") != 0 {
		t.Error("the account number printed on an invoice where ShowBankDetails is false")
	}
}
