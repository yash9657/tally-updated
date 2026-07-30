package printing

// invoice.go — the tax invoice (Rule 46) and the bill of supply (Rule 49) on
// paper.
//
// THE ONE RULE THIS FILE OBEYS ABOVE EVERY OTHER: THE PAGE SHOWS THE NUMBER THE
// LEDGER HOLDS. Every amount printed here is the decimal the caller handed over,
// rendered through FormatMoney. Nothing is re-derived — the line amount is not
// Quantity × Rate, the taxable value is not the sum of the lines, the total is
// not Subtotal + tax + round-off, and no page carries a "carried forward"
// subtotal, because computing one would mean this renderer adding up money and
// then disagreeing with the voucher that posted it. If the values handed over do
// not add up, THE INVOICE PRINTS THEM AS GIVEN and the discrepancy is visible on
// the paper. That is deliberate and it is the safer of the two failures: a
// renderer that quietly "fixed" a total would produce a document that disagrees
// with the ledger it was printed from, and nobody would ever find out.
// Pinned by TestAnInvoiceWhoseFiguresDoNotAddUpPrintsThemAsGivenAndFixesNothing.
//
// GEOMETRY. Origin bottom-left, y decreasing down the page (see page.go). Every
// coordinate here is a float64 because a PDF coordinate is real-valued; no money
// is computed in this file, and grep for "float" will find only geometry.

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// The built-in default template. IT LIVES IN CODE, ON PURPOSE.
//
// Migration 0037's print_template table holds only per-company OVERRIDES, so a
// company created five minutes ago, with no template row of its own, prints a
// complete and correct invoice. If the default lived in the database, a fresh
// company would print a document with no title and no signature label until
// somebody remembered to seed it.
// ---------------------------------------------------------------------------

const (
	// invDefaultTaxInvoiceTitle is the heading of a Rule 46 document.
	invDefaultTaxInvoiceTitle = "Tax Invoice"

	// invDefaultBillOfSupplyTitle is the heading of a Rule 49 document. The
	// words matter: a document that charges no tax and is headed "Tax Invoice"
	// misrepresents itself to the recipient, who would look for a credit that is
	// not there.
	invDefaultBillOfSupplyTitle = "Bill of Supply"

	// invDefaultSignatureLabel is the label under the signature panel that Rule
	// 46(q) / Rule 49(h) is discharged by.
	invDefaultSignatureLabel = "Authorised Signatory"

	// invDefaultDeclaration is the certification every Indian invoice has
	// carried since long before GST. It is not required by Rule 46 — it is
	// commercial convention, which is why a company may override it away.
	invDefaultDeclaration = "We declare that this invoice shows the actual price of the goods described and that all particulars are true and correct."

	// invQRNotPrintedNotice is printed on any invoice that carries an IRN, and it
	// is a STATUTORY DISCLOSURE, not a cosmetic note.
	//
	// Rule 48(4) of the CGST Rules 2017 makes an e-invoice the one prepared by
	// uploading the particulars to the Invoice Registration Portal, and the copy
	// issued to the recipient must carry the Quick Response code the portal
	// returned. THIS PACKAGE CANNOT PRODUCE THAT QR CODE: what it encodes is the
	// signed payload the IRP hands back, no NIC/IRP call can be made from this
	// deployment (the statutory workflow ships against a fake provider), and a
	// QR generated from anything else would be a forgery of a government
	// signature. Drawing nothing and saying nothing was the worse option: the
	// sheet then LOOKS like a complete rule 48(4) copy, and the person at the
	// counter hands it over.
	//
	// So the gap is printed. The wording says what is missing and what the
	// consequence is, in the words the operator needs.
	invQRNotPrintedNotice = "QR code not printed - this copy is NOT the e-invoice to be issued under rule 48(4); obtain the signed QR from the IRP"

	// invDefaultBillOfSupplyDeclaration says the same thing about the document it
	// is actually printed on. A bill of supply that declares "this invoice" is
	// describing itself as something it is not, on the one document whose whole
	// point is that it is not a tax invoice.
	invDefaultBillOfSupplyDeclaration = "We declare that this bill of supply shows the actual price of the goods described and that all particulars are true and correct."
)

// Page geometry, in points, for A4 portrait.
const (
	invMarginLeft  = 36.0
	invMarginRight = 559.28 // 595.28 - 36
	invTitleY      = 806.0
	invFooterY     = 26.0

	// invBodyBottom is the lowest baseline the body or the totals may use. Below
	// it lives the page-number footer, and a row drawn under it would collide
	// with "Page 1 of 2".
	invBodyBottom = 46.0

	invLineHeight = 11.0
	invTightLine  = 9.5

	invFontTitle = 13.0
	invFontHead  = 9.0
	invFontBody  = 8.5
	invFontSmall = 7.5

	// invFontSupplierName is the size the supplier's own name is set at, larger
	// than the address beneath it because it is the one line a recipient reads
	// first. IT IS A NAMED CONSTANT BECAUSE IT IS USED TWICE: once to WRAP the
	// name and once to DRAW it. A wrap measured at one size and drawn at another
	// overflows by exactly the difference, which is the quiet version of the bug
	// the wrap was added to fix.
	invFontSupplierName = 10.0
)

// Column geometry of the item table. Left edges for text that reads
// left-to-right, right edges for every figure — an amount column is
// right-aligned so two amounts can be compared at a glance.
const (
	invColSlRight   = 56.0
	invColDescLeft  = 62.0
	invColDescRight = 292.0
	invColHSNLeft   = 296.0
	invColQtyRight  = 396.0
	invColUnitLeft  = 400.0
	invColRateRight = 486.0
	invColAmtRight  = 557.0

	// invColDescWidth is the wrap width of the description column.
	invColDescWidth = invColDescRight - invColDescLeft

	// invBlockWidth is the wrap width of a header block (supplier, party): a
	// little under half the page, so the two blocks cannot overlap.
	invBlockWidth = 240.0

	// invConsigneeLeft is where the shipped-to block starts.
	invConsigneeLeft = 306.0
)

// invCenterX is the horizontal centre of the printable area, where titles go.
const invCenterX = (invMarginLeft + invMarginRight) / 2

// RenderInvoice puts a tax invoice or a bill of supply on paper and returns the
// PDF bytes.
//
// IT VALIDATES FIRST AND PRINTS NOTHING ON FAILURE. A document missing a
// mandatory particular comes back as a *Rule46Error naming every missing field
// and its clause, and no bytes are produced. See the head of rule46.go for why a
// refusal beats a blank.
//
// Pagination: the item table flows onto as many pages as it needs, the column
// headers and the whole letterhead repeat on every page, and the TOTALS APPEAR
// ONLY ONCE, on the last page. A total on page one of a three-page invoice is a
// total of the first page's lines and nothing else, which is how a reader ends up
// paying the wrong amount.
func RenderInvoice(inv Invoice) ([]byte, error) {
	switch inv.Kind {
	case KindTaxInvoice, KindBillOfSupply:
	default:
		return nil, fmt.Errorf("printing: RenderInvoice prints %q and %q, not %q; the other documents are rendered by their own function so that each one is validated against the rule that governs it",
			string(KindTaxInvoice), string(KindBillOfSupply), string(inv.Kind))
	}
	if err := ValidateRule46(inv); err != nil {
		return nil, err
	}

	// showTax is the single switch between the two documents: a bill of supply
	// charges no tax, so no tax row may appear on it (Rule 49 lists no rate and
	// no amount of tax) and no reverse-charge marker either.
	showTax := inv.Kind == KindTaxInvoice
	title := invTitle(inv)

	// MEASURE BEFORE PAGINATING, using the very functions that draw. The header
	// and the totals block are laid out onto pages of a THROWAWAY document whose
	// bytes are never serialised, purely to learn their heights. Measuring with
	// the real drawing code is what stops the pagination arithmetic from drifting
	// away from the layout — a second, "predicted" height would be wrong the
	// first time somebody added a line to the letterhead.
	headerBottom := invDrawHeader(NewDoc(A4Portrait).NewPage(), inv, title, showTax)

	// TWO DIFFERENT FAILURES, TWO DIFFERENT MESSAGES, BECAUSE THEY HAVE DIFFERENT
	// REMEDIES. A single "headerBottom - totalsHeight < invBodyBottom" test cannot
	// tell "the totals block is too tall" from "the header consumed the whole
	// sheet": it reported a NEGATIVE number of available points and blamed the
	// company's template overrides, which on the second failure are frequently
	// empty. An operator handed that message deletes their bank details and their
	// terms, prints again, and the document still refuses — because what did not
	// fit was a 400-character letterhead line.
	if headerBottom <= invBodyBottom {
		label, value, lines := invLongestHeaderField(inv)
		runes := []rune(value)
		return nil, fmt.Errorf("printing: the letterhead and the party blocks fill the whole sheet on their own, leaving %.0f points for the items and the totals; the longest field in the header is %s, which wraps to %d lines (%d characters, beginning %q); shorten it",
			max(headerBottom-invBodyBottom, 0), label, lines, len(runes), invHeadOfField(runes))
	}

	totalsHeight := headerBottom - invDrawTotals(NewDoc(A4Portrait).NewPage(), inv, headerBottom, showTax)
	if headerBottom-totalsHeight < invBodyBottom {
		// The totals block does not fit in what the header left, even with no items
		// at all. Refuse rather than draw off the bottom of the sheet, where the
		// text is silently lost.
		return nil, fmt.Errorf("printing: the totals block needs %.0f points and only %.0f are left below the header; %s",
			totalsHeight, max(headerBottom-invBodyBottom, 0), invTotalsBlockBlame(inv))
	}

	pages := invPaginate(inv, headerBottom, totalsHeight)

	d := NewDoc(A4Portrait)
	for pi, group := range pages {
		p := d.NewPage()
		y := invDrawHeader(p, inv, title, showTax)
		for _, idx := range group {
			y = invDrawItem(p, inv.Items[idx], idx, y)
		}
		last := pi == len(pages)-1
		if last {
			invDrawTotals(p, inv, y, showTax)
		} else {
			p.TextRight(invColAmtRight, invFooterY, HelveticaOblique, invFontSmall, "Continued ...")
		}
		p.TextCenter(invCenterX, invFooterY, Helvetica, invFontSmall,
			fmt.Sprintf("Page %d of %d", pi+1, len(pages)))
	}

	return d.Bytes()
}

// invTitle is the document heading: the company's override if it has one, else
// the built-in default for the kind.
func invTitle(inv Invoice) string {
	if !invBlank(inv.Overrides.Title) {
		return inv.Overrides.Title
	}
	if inv.Kind == KindBillOfSupply {
		return invDefaultBillOfSupplyTitle
	}
	return invDefaultTaxInvoiceTitle
}

// invPaginate distributes the line items over pages, returning one slice of item
// indices per page.
//
// The last page must also hold the totals block: if the items reach the bottom of
// the page, a FURTHER page is added carrying the totals alone. That is the
// alternative to squeezing the totals into space that is not there, which prints
// them over the last two lines of the table.
func invPaginate(inv Invoice, headerBottom, totalsHeight float64) [][]int {
	var pages [][]int
	var cur []int
	y := headerBottom

	for i := range inv.Items {
		h := invItemHeight(inv.Items[i])
		if len(cur) > 0 && y-h < invBodyBottom {
			pages = append(pages, cur)
			cur = nil
			y = headerBottom
		}
		cur = append(cur, i)
		y -= h
	}
	pages = append(pages, cur) // the last (or only) page, possibly with no items

	if y-totalsHeight < invBodyBottom {
		pages = append(pages, nil) // a page for the totals alone
	}
	return pages
}

// ---------------------------------------------------------------------------
// the header, repeated on every page
// ---------------------------------------------------------------------------

// invDrawHeader draws the title, the e-invoice particulars, the supplier block,
// the document meta block, the party blocks, the place-of-supply and
// reverse-charge lines, and the column headers. It returns the baseline the item
// rows start from.
//
// IT REPEATS ON EVERY PAGE and that is a decision, not an accident: page two of
// an invoice can be detached from page one, and a sheet of line items that does
// not say whose invoice it is or what its number is cannot be filed against
// anything.
func invDrawHeader(p *Page, inv Invoice, title string, showTax bool) float64 {
	y := invTitleY
	p.TextCenter(invCenterX, y, HelveticaBold, invFontTitle, title)
	y -= 15

	// Rule 48(4) e-invoice particulars. Printed in Courier because an IRN is a
	// 64-character hex string somebody occasionally has to read out or compare
	// character by character, and a monospaced face is what makes that possible.
	if !invBlank(inv.IRN) {
		p.TextCenter(invCenterX, y, Courier, invFontSmall, "IRN: "+strings.TrimSpace(inv.IRN))
		y -= invTightLine
	}
	if !invBlank(inv.AckNo) {
		p.TextCenter(invCenterX, y, Courier, invFontSmall, "Ack No: "+strings.TrimSpace(inv.AckNo))
		y -= invTightLine
	}
	// The Quick Response code of rule 48(4) cannot be drawn here — see
	// invQRNotPrintedNotice. An invoice that HAS an IRN says so on its face, in
	// bold, rather than presenting itself as a complete e-invoice copy. The
	// condition is the presence of the IRN alone because there is no code path in
	// this package that draws a QR at all; when one exists, this becomes
	// "IRN present AND no QR drawn".
	if !invBlank(inv.IRN) {
		for _, line := range Wrap(HelveticaBold, invFontSmall, invQRNotPrintedNotice, invMarginRight-invMarginLeft) {
			p.TextCenter(invCenterX, y, HelveticaBold, invFontSmall, line)
			y -= invTightLine
		}
	}

	y -= 4
	p.Line(invMarginLeft, y, invMarginRight, y, 0.7)
	y -= 13

	// Supplier on the left (Rule 46(a) / 49(a)), document meta on the right.
	//
	// THE NAME IS WRAPPED TO invBlockWidth, exactly like every address line under
	// it. company.name is TEXT NOT NULL and unbounded, and "SHREE SIDDHIVINAYAK
	// ENGINEERING AND FABRICATION WORKS PRIVATE LIMITED" is an ordinary GST
	// registration name: drawn unwrapped at 10pt bold it is 70 characters that run
	// straight through the Rule 46(b) invoice number — which sits on the SAME
	// BASELINE in the right-hand meta block — and then off the right edge of the
	// sheet, where the rest of the name is simply gone. Nothing but this wrap
	// enforces the "the two blocks cannot overlap" claim the invBlockWidth comment
	// makes. Pinned by
	// TestNothingInTheInvoiceHeaderIsDrawnOverItsNeighbourOrOffTheSheet.
	leftY := y
	for _, wrapped := range Wrap(HelveticaBold, invFontSupplierName, strings.TrimSpace(inv.Supplier.Name), invBlockWidth) {
		p.Text(invMarginLeft, leftY, HelveticaBold, invFontSupplierName, wrapped)
		leftY -= invLineHeight
	}
	for _, line := range invSupplierLines(inv) {
		for _, wrapped := range Wrap(Helvetica, invFontBody, line, invBlockWidth) {
			p.Text(invMarginLeft, leftY, Helvetica, invFontBody, wrapped)
			leftY -= invTightLine
		}
	}

	rightY := y
	for _, line := range invMetaLines(inv) {
		p.TextRight(invMarginRight, rightY, Helvetica, invFontBody, line)
		rightY -= invTightLine
	}

	y = min(leftY, rightY) - 4
	p.Line(invMarginLeft, y, invMarginRight, y, 0.5)
	y -= 12

	// The party blocks. "Billed to" is the recipient of Rule 46(d); "Shipped to"
	// is the address of delivery of Rule 46(o), and when there is no separate one
	// the document says so rather than leaving the box empty, because an empty box
	// reads as a field somebody forgot.
	billY := y
	p.Text(invMarginLeft, billY, HelveticaBold, invFontSmall, "Billed to")
	billY -= invLineHeight
	// Wrapped for the same reason the supplier's name is: ledger.name is
	// unbounded, and an unwrapped recipient name overprints the Shipped-to block
	// that starts at invConsigneeLeft.
	for _, wrapped := range Wrap(HelveticaBold, invFontBody, strings.TrimSpace(inv.Recipient.Name), invBlockWidth) {
		p.Text(invMarginLeft, billY, HelveticaBold, invFontBody, wrapped)
		billY -= invTightLine
	}
	for _, line := range invPartyLines(inv.Recipient) {
		for _, wrapped := range Wrap(Helvetica, invFontBody, line, invBlockWidth) {
			p.Text(invMarginLeft, billY, Helvetica, invFontBody, wrapped)
			billY -= invTightLine
		}
	}

	shipY := y
	p.Text(invConsigneeLeft, shipY, HelveticaBold, invFontSmall, "Shipped to")
	shipY -= invLineHeight
	if inv.ShipTo == nil {
		p.Text(invConsigneeLeft, shipY, Helvetica, invFontBody, "Same as billed to")
		shipY -= invTightLine
	} else {
		// Wrapped: the ship-to block ends at invConsigneeLeft + invBlockWidth, which
		// is 13 points shy of the right margin, so an unwrapped godown name runs off
		// the paper altogether.
		for _, wrapped := range Wrap(HelveticaBold, invFontBody, strings.TrimSpace(inv.ShipTo.Name), invBlockWidth) {
			p.Text(invConsigneeLeft, shipY, HelveticaBold, invFontBody, wrapped)
			shipY -= invTightLine
		}
		for _, line := range invPartyLines(*inv.ShipTo) {
			for _, wrapped := range Wrap(Helvetica, invFontBody, line, invBlockWidth) {
				p.Text(invConsigneeLeft, shipY, Helvetica, invFontBody, wrapped)
				shipY -= invTightLine
			}
		}
	}

	y = min(billY, shipY) - 3
	p.Line(invMarginLeft, y, invMarginRight, y, 0.5)
	y -= 12

	// Place of supply (Rule 46(n)) on the left; the reverse-charge marker (Rule
	// 46(p)) on the right.
	if pos := invPlaceOfSupplyLine(inv); pos != "" {
		p.Text(invMarginLeft, y, Helvetica, invFontBody, pos)
	}
	if showTax {
		// ALWAYS PRINTED, Yes OR No. The clause requires the document to state
		// WHETHER tax is payable on reverse charge, so silence is not one of the
		// permitted answers: an invoice with no marker leaves the recipient
		// unable to tell whether it is they who owe the tax.
		p.TextRight(invMarginRight, y, HelveticaBold, invFontBody,
			"Tax payable on reverse charge: "+invYesNo(inv.ReverseCharge))
	}
	y -= 6
	p.Line(invMarginLeft, y, invMarginRight, y, 0.7)
	y -= 11

	// Column headers. These repeat on every page — a continuation sheet of bare
	// figures is unreadable.
	p.TextRight(invColSlRight, y, HelveticaBold, invFontSmall, "Sl")
	p.Text(invColDescLeft, y, HelveticaBold, invFontSmall, "Description of Goods or Services")
	p.Text(invColHSNLeft, y, HelveticaBold, invFontSmall, "HSN/SAC")
	p.TextRight(invColQtyRight, y, HelveticaBold, invFontSmall, "Qty")
	p.Text(invColUnitLeft, y, HelveticaBold, invFontSmall, "Unit")
	p.TextRight(invColRateRight, y, HelveticaBold, invFontSmall, "Rate")
	p.TextRight(invColAmtRight, y, HelveticaBold, invFontSmall, "Amount")
	y -= 4
	p.Line(invMarginLeft, y, invMarginRight, y, 0.7)

	return y - invLineHeight
}

// invSupplierLines is the supplier's address and identifiers, in the order they
// are read. The GSTIN is never omitted: Rule 46(a) requires it and
// ValidateRule46 has already refused the document if it is absent.
func invSupplierLines(inv Invoice) []string {
	c := inv.Supplier
	var out []string
	add := func(s string) {
		if !invBlank(s) {
			out = append(out, strings.TrimSpace(s))
		}
	}
	add(c.Address1)
	add(c.Address2)
	add(invCityStateLine(c.City, c.State, c.PIN))
	add("GSTIN: " + strings.TrimSpace(c.GSTIN))
	if !invBlank(c.PAN) {
		add("PAN: " + strings.TrimSpace(c.PAN))
	}
	add(invContactLine(c.Phone, c.Email))
	for _, l := range inv.Overrides.HeaderLines {
		add(l)
	}
	return out
}

// invPartyLines is a party's address block: the address, the city/State/PIN line,
// the State and its code, and the GSTIN.
//
// AN UNREGISTERED PARTY IS SAID TO BE UNREGISTERED rather than left blank. "GSTIN:
// Unregistered" is a statement; an empty GSTIN line is a missing field, and the
// two must not look the same on paper — a B2C invoice is legal, and a B2B invoice
// with the customer's GSTIN dropped is a lost input tax credit.
func invPartyLines(pt Party) []string {
	var out []string
	add := func(s string) {
		if !invBlank(s) {
			out = append(out, strings.TrimSpace(s))
		}
	}
	add(pt.Address1)
	add(pt.Address2)
	add(invCityStateLine(pt.City, pt.State, pt.PIN))
	if !invBlank(pt.State) {
		add("State: " + invStateAndCode(pt.State, pt.StateCode))
	}
	if invBlank(pt.GSTIN) {
		add("GSTIN: Unregistered")
	} else {
		add("GSTIN: " + strings.TrimSpace(pt.GSTIN))
	}
	return out
}

// invMetaLines is the document's own particulars: its number (Rule 46(b)), its
// date of issue (Rule 46(c)) and its due date when it has one.
func invMetaLines(inv Invoice) []string {
	numberLabel := "Invoice No.: "
	if inv.Kind == KindBillOfSupply {
		numberLabel = "Bill of Supply No.: "
	}
	out := []string{
		numberLabel + strings.TrimSpace(inv.Number),
		"Dated: " + FormatDate(inv.Date),
	}
	if inv.DueDate != nil && !inv.DueDate.IsZero() {
		out = append(out, "Due Date: "+FormatDate(*inv.DueDate))
	}
	return out
}

// invLongestHeaderField names the field that occupies the most vertical space in
// the header, together with its value and the number of lines it wraps to.
//
// IT EXISTS SO THAT A REFUSAL NAMES THE STRING TO SHORTEN. The refusal it feeds
// used to say "the company's template overrides ... are too long to print" for
// every kind of overflow, including the one where the company has no overrides at
// all and the offender is a 400-character address line typed into the ledger. An
// operator handed that message deletes their bank details for nothing and the
// document still will not print.
//
// The widths are measured with the SAME font and size each field is drawn at
// (see invDrawHeader), so the count reported is the count of lines that would
// actually have been put on the paper.
func invLongestHeaderField(inv Invoice) (label, value string, lines int) {
	consider := func(l, v string, f Font, size float64) {
		v = strings.TrimSpace(v)
		if invBlank(v) {
			return
		}
		n := len(Wrap(f, size, v, invBlockWidth))
		if n > lines || (n == lines && len([]rune(v)) > len([]rune(value))) {
			label, value, lines = l, v, n
		}
	}

	consider("the supplier name", inv.Supplier.Name, HelveticaBold, invFontSupplierName)
	for _, l := range invSupplierLines(inv) {
		consider("a supplier address or letterhead line", l, Helvetica, invFontBody)
	}
	consider("the recipient name", inv.Recipient.Name, HelveticaBold, invFontBody)
	for _, l := range invPartyLines(inv.Recipient) {
		consider("a recipient address line", l, Helvetica, invFontBody)
	}
	if inv.ShipTo != nil {
		consider("the ship-to name", inv.ShipTo.Name, HelveticaBold, invFontBody)
		for _, l := range invPartyLines(*inv.ShipTo) {
			consider("a ship-to address line", l, Helvetica, invFontBody)
		}
	}
	if lines == 0 {
		// Unreachable through RenderInvoice, which validates the supplier's name and
		// GSTIN first, but a refusal message must never be the thing that panics.
		return "no header field at all", "", 0
	}
	return label, value, lines
}

// invHeadOfField is the first invFieldQuoteRunes of a field, for quoting inside a
// refusal. The field that overflows a sheet is by definition enormous — five
// thousand characters of address pasted into one column is what the refusal is
// about — and quoting it whole produces a message no log, no terminal and no
// operator can read, which is how a message that names the problem still fails to
// communicate it. The length is reported as a number beside this.
func invHeadOfField(runes []rune) string {
	if len(runes) <= invFieldQuoteRunes {
		return string(runes)
	}
	return string(runes[:invFieldQuoteRunes]) + "..."
}

// invFieldQuoteRunes is how much of an offending field a refusal quotes: enough
// for an operator to recognise which field it is, and no more.
const invFieldQuoteRunes = 120

// invTotalsBlockBlame words the second half of the overflow refusal: WHICH
// overrides to shorten, and — when the company has none — that there is nothing
// for the operator to shorten at all.
//
// The distinction is the whole point. "The overrides are too long" told to a
// company with zero override rows sends somebody hunting for a setting that does
// not exist, when what has happened is that the built-in block no longer fits the
// page geometry, which is a defect in this package.
func invTotalsBlockBlame(inv Invoice) string {
	o := inv.Overrides
	var have []string
	if !invBlank(o.Declaration) {
		have = append(have, "the declaration")
	}
	if !invBlank(o.Terms) {
		have = append(have, "the terms")
	}
	if o.ShowBankDetails && len(o.BankLines) > 0 {
		have = append(have, fmt.Sprintf("%d bank line(s)", len(o.BankLines)))
	}
	if len(o.FooterLines) > 0 {
		have = append(have, fmt.Sprintf("%d footer line(s)", len(o.FooterLines)))
	}
	if len(have) == 0 {
		return "this company has no template overrides, so what does not fit is the built-in totals block itself; there is nothing an operator can shorten and the page geometry in invoice.go is wrong"
	}
	return "shorten the company's template overrides: " + strings.Join(have, ", ")
}

// invCityStateLine joins the city, the State and the PIN the way an Indian
// address is written: "Pune, Maharashtra - 411001". Any of the three may be
// absent and the punctuation adjusts, so a half-filled address never prints a
// stray comma or a dangling hyphen.
func invCityStateLine(city, state, pin string) string {
	var parts []string
	if !invBlank(city) {
		parts = append(parts, strings.TrimSpace(city))
	}
	if !invBlank(state) {
		parts = append(parts, strings.TrimSpace(state))
	}
	line := strings.Join(parts, ", ")
	if !invBlank(pin) {
		if line == "" {
			return strings.TrimSpace(pin)
		}
		return line + " - " + strings.TrimSpace(pin)
	}
	return line
}

// invContactLine joins the phone and the email, either of which may be absent.
func invContactLine(phone, email string) string {
	var parts []string
	if !invBlank(phone) {
		parts = append(parts, "Ph: "+strings.TrimSpace(phone))
	}
	if !invBlank(email) {
		parts = append(parts, strings.TrimSpace(email))
	}
	return strings.Join(parts, "  ")
}

// invStateAndCode renders a State with its GST code in parentheses:
// "Maharashtra (27)". The code is what a return is filed against, so it is
// printed next to the name rather than instead of it.
func invStateAndCode(state, code string) string {
	state = strings.TrimSpace(state)
	if invBlank(code) {
		return state
	}
	return state + " (" + strings.TrimSpace(code) + ")"
}

// invPlaceOfSupplyLine renders the place-of-supply particular of Rule 46(n), or
// "" when the document carries none.
//
// It prints whenever the value is present, INCLUDING on an intra-State supply
// where the clause does not require it. Printing a particular the rule does not
// demand is harmless; the validator, not the renderer, is where the conditional
// lives.
func invPlaceOfSupplyLine(inv Invoice) string {
	if invBlank(inv.PlaceOfSupply) && invBlank(inv.PlaceOfSupplyCode) {
		return ""
	}
	if invBlank(inv.PlaceOfSupply) {
		return "Place of Supply: (" + strings.TrimSpace(inv.PlaceOfSupplyCode) + ")"
	}
	return "Place of Supply: " + invStateAndCode(inv.PlaceOfSupply, inv.PlaceOfSupplyCode)
}

// invYesNo words a statutory boolean. "Yes"/"No" and never a tick or a blank: a
// tick that failed to render is indistinguishable from an unticked box.
func invYesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// ---------------------------------------------------------------------------
// the item rows
// ---------------------------------------------------------------------------

// invItemDescriptionLines is the description column of one row, wrapped, plus the
// discount line when the line carries one.
//
// IT IS THE SINGLE SOURCE OF THE ROW'S HEIGHT. invItemHeight counts these lines
// and invDrawItem draws them, so the pagination arithmetic and the layout cannot
// disagree — which they would the moment one of them forgot about the discount
// line and an item was drawn over the page footer.
func invItemDescriptionLines(it LineItem) []string {
	lines := Wrap(Helvetica, invFontBody, it.Description, invColDescWidth)
	if !it.DiscountPct.IsZero() {
		// Rule 46(k) requires the taxable value "taking into account discount or
		// abatement, if any", and Amount already IS net of it. The discount is
		// shown so the recipient can see how the net was arrived at; it is NOT
		// subtracted here, because the subtraction has already happened in the
		// voucher.
		//
		// IT PRINTS AS A PERCENTAGE, WITH THE SIGN, BECAUSE THAT IS THE UNIT THE
		// BOOKS HOLD. stock_movement stores discount_pct (CHECK 0..100) and this
		// field carries it verbatim. Printed through FormatMoney it read
		// "Less: Discount 2.50" against a line discounted by Rs 25.00 — a rupee
		// figure on the face of a tax invoice that contradicted the taxable value
		// beside it and understated the discount by qty x rate / 100. And it is NOT
		// converted to rupees here: the voucher stores the percentage and the
		// already-net Amount, so a rupee discount computed in the renderer would be
		// a number on the paper that no posting holds.
		lines = append(lines, "Less: Discount "+FormatRate(it.DiscountPct)+"%")
	}
	return lines
}

// invItemHeight is the vertical space one item row occupies.
func invItemHeight(it LineItem) float64 {
	n := len(invItemDescriptionLines(it))
	if n < 1 {
		n = 1
	}
	return float64(n) * invTightLine
}

// invDrawItem draws one item row and returns the next baseline.
//
// EVERY FIGURE IS THE ONE HANDED OVER. FormatQty of the quantity, FormatMoney of
// the rate, FormatMoney of the amount — no multiplication anywhere. A line whose
// Amount does not equal Quantity × Rate prints exactly what the voucher stored,
// because the voucher may hold a discount, an abatement or a rounding this
// renderer knows nothing about, and "correcting" it would put a number on the
// invoice that is in no ledger.
func invDrawItem(p *Page, it LineItem, index int, y float64) float64 {
	lines := invItemDescriptionLines(it)

	serial := it.SerialNo
	if serial <= 0 {
		// A voucher that did not number its lines still prints a numbered table:
		// the serial is presentational, and a column of blanks makes a
		// forty-line invoice impossible to talk about over the phone.
		serial = index + 1
	}

	p.TextRight(invColSlRight, y, Helvetica, invFontBody, fmt.Sprintf("%d", serial))
	if len(lines) > 0 {
		p.Text(invColDescLeft, y, Helvetica, invFontBody, lines[0])
	}
	p.Text(invColHSNLeft, y, Helvetica, invFontBody, strings.TrimSpace(it.HSN))
	if !it.Quantity.IsZero() {
		p.TextRight(invColQtyRight, y, Helvetica, invFontBody, FormatQty(it.Quantity))
		p.Text(invColUnitLeft, y, Helvetica, invFontBody, strings.TrimSpace(it.Unit))
	}
	if !it.Rate.IsZero() {
		p.TextRight(invColRateRight, y, Helvetica, invFontBody, FormatMoney(it.Rate))
	}
	p.TextRight(invColAmtRight, y, Helvetica, invFontBody, FormatMoney(it.Amount))

	for _, extra := range lines[1:] {
		y -= invTightLine
		p.Text(invColDescLeft, y, Helvetica, invFontBody, extra)
	}
	return y - invTightLine
}

// ---------------------------------------------------------------------------
// the totals block — LAST PAGE ONLY
// ---------------------------------------------------------------------------

// invDrawTotals draws the taxable value, one row per tax head, the round-off, the
// total, the amount in words, the declaration and terms, the bank details and the
// signature panel. It returns the baseline it finished at, which is how
// RenderInvoice measures the block's height before paginating.
//
// ONE ROW PER TAX HEAD, EACH WITH ITS OWN RATE AND ITS OWN AMOUNT. Rule 46(l) and
// 46(m) require the rate and the amount of tax per head — central, State,
// integrated, Union territory, cess — so a 9 + 9 supply prints
//
//	CGST @ 9%    900.00
//	SGST @ 9%    900.00
//
// and never a single "GST 18% 1,800.00", which states neither of the two taxes
// actually charged and cannot be reported in a return.
func invDrawTotals(p *Page, inv Invoice, y float64, showTax bool) float64 {
	y -= 2
	p.Line(invMarginLeft, y, invMarginRight, y, 0.7)
	y -= 12

	row := func(label, value string, f Font) {
		p.TextRight(invColRateRight, y, f, invFontBody, label)
		p.TextRight(invColAmtRight, y, f, invFontBody, value)
		y -= invLineHeight
	}

	// "Taxable Value" on a tax invoice (Rule 46(k)); "Value of Supply" on a bill
	// of supply, where Rule 49(g) uses those words and where there is no taxable
	// value at all — calling it taxable on a document that charges no tax invites
	// the recipient to look for a credit that does not exist.
	valueLabel := "Taxable Value"
	if !showTax {
		valueLabel = "Value of Supply"
	}
	row(valueLabel, FormatMoney(inv.Subtotal), Helvetica)
	if showTax {
		for _, t := range inv.Taxes {
			row(strings.TrimSpace(t.Name)+" @ "+FormatRate(t.Rate)+"%", FormatMoney(t.Amount), Helvetica)
		}
	}
	if !inv.RoundOff.IsZero() {
		row("Round Off", FormatMoney(inv.RoundOff), Helvetica)
	}

	p.Line(invColRateRight-90, y+4, invColAmtRight, y+4, 0.5)
	y -= 2
	p.TextRight(invColRateRight, y, HelveticaBold, invFontHead, "Total")
	p.TextRight(invColAmtRight, y, HelveticaBold, invFontHead, FormatMoney(inv.Total))
	y -= 6
	p.Line(invMarginLeft, y, invMarginRight, y, 0.7)
	y -= 12

	// The amount in words, from THE SAME decimal the Total row printed. Both go
	// through the same quantization (see words.go), which is what keeps the words
	// and the figures on one invoice from disagreeing — and on a document that
	// settles a debt, the words are the operative amount.
	words := "Amount in Words: " + AmountInWords(inv.Total)
	for _, line := range Wrap(HelveticaBold, invFontBody, words, invMarginRight-invMarginLeft) {
		p.Text(invMarginLeft, y, HelveticaBold, invFontBody, line)
		y -= invLineHeight
	}
	y -= 4

	// Declaration and terms on the left, signature panel on the right, sharing a
	// band so the two cannot be pushed apart by a long declaration.
	leftY := y
	declaration := inv.Overrides.Declaration
	if invBlank(declaration) {
		declaration = invDefaultDeclaration
		if !showTax {
			declaration = invDefaultBillOfSupplyDeclaration
		}
	}
	p.Text(invMarginLeft, leftY, HelveticaBold, invFontSmall, "Declaration")
	leftY -= invTightLine
	for _, line := range Wrap(Helvetica, invFontSmall, declaration, 300) {
		p.Text(invMarginLeft, leftY, Helvetica, invFontSmall, line)
		leftY -= invTightLine
	}
	if !invBlank(inv.Overrides.Terms) {
		leftY -= 3
		p.Text(invMarginLeft, leftY, HelveticaBold, invFontSmall, "Terms")
		leftY -= invTightLine
		for _, line := range Wrap(Helvetica, invFontSmall, inv.Overrides.Terms, 300) {
			p.Text(invMarginLeft, leftY, Helvetica, invFontSmall, line)
			leftY -= invTightLine
		}
	}
	if inv.Overrides.ShowBankDetails && len(inv.Overrides.BankLines) > 0 {
		leftY -= 3
		p.Text(invMarginLeft, leftY, HelveticaBold, invFontSmall, "Bank Details")
		leftY -= invTightLine
		for _, bank := range inv.Overrides.BankLines {
			for _, line := range Wrap(Helvetica, invFontSmall, bank, 300) {
				p.Text(invMarginLeft, leftY, Helvetica, invFontSmall, line)
				leftY -= invTightLine
			}
		}
	}

	// THE SIGNATURE PANEL — Rule 46(q) / Rule 49(h). "For <company>", a gap wide
	// enough to sign in, and the authorised-signatory label. The gap is the point:
	// the rule wants a signature, and what software can supply is the place for
	// one. An e-invoice's digital signature travels with the IRN printed at the
	// top of the page.
	rightY := y
	p.TextRight(invMarginRight, rightY, Helvetica, invFontBody, "For "+strings.TrimSpace(inv.Supplier.Name))
	rightY -= 30
	signature := inv.Overrides.SignatureLabel
	if invBlank(signature) {
		signature = invDefaultSignatureLabel
	}
	p.TextRight(invMarginRight, rightY, Helvetica, invFontBody, signature)
	rightY -= invLineHeight

	y = min(leftY, rightY)

	for _, line := range inv.Overrides.FooterLines {
		for _, wrapped := range Wrap(Helvetica, invFontSmall, line, invMarginRight-invMarginLeft) {
			p.TextCenter(invCenterX, y, Helvetica, invFontSmall, wrapped)
			y -= invTightLine
		}
	}
	return y
}
