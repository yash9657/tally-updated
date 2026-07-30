package printing

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// This file renders the FIVE documents that are not invoices and not reports:
// the delivery note, the receipt and payment vouchers, and the credit and debit
// notes. RenderVoucherDoc is the only entry point.
//
// WHY THESE FIVE SHARE ONE FILE AND ONE FRAME, AND WHERE THEY DIVERGE.
// All five carry the same masthead (who issued it, its number, its date), the
// same party block and the same signature block, so the frame is written once.
// What differs is the BODY, and the differences are not cosmetic — each one is
// the reason the document exists:
//
//   - DELIVERY NOTE: goods left the warehouse. Quantities and descriptions, the
//     delivery address, and NO TAX COLUMNS AND NO TAX-INVOICE TITLE. That is the
//     one hard rule of this document: a delivery note that looks like a tax
//     invoice gets handed to a customer as one, and the customer then claims
//     input tax credit against a piece of paper that never charged tax.
//     TestAVoucherDeliveryNoteIsNeverTitledOrPricedLikeATaxInvoice pins it.
//
//   - RECEIPT / PAYMENT: the money side, and the bills it settles. A receipt
//     that says "Rs. 50,000.00 received" without naming the invoices it is
//     against is the single most common cause of a disputed customer ledger:
//     six months later neither side can say which invoice is still open. So
//     VoucherLine.BillRef gets its OWN COLUMN, and a leg with no bill reference
//     prints "On Account" rather than an empty cell, because "unallocated" is a
//     fact about the receipt and not a missing field.
//
//   - CREDIT / DEBIT NOTE: it must NAME THE INVOICE IT ADJUSTS — number and date —
//     and it must show the tax adjustment PER HEAD, because the recipient reverses
//     input tax credit out of this document. A note with no original invoice
//     reference is refused outright. The REASON it was issued prints when the
//     voucher records one and is omitted when it does not; the two are treated
//     differently because one requirement is cited and the other could not be
//     verified from here — see vdAgainstInvoiceRequirement below.
//
// MONEY. Every amount is rendered with FormatMoney/FormatQty/FormatRate on the
// decimal.Decimal this package was HANDED. Nothing here adds, subtracts,
// re-derives or re-rounds an amount; v.Total is printed as given, and a negative
// note total prints with the leading minus that FormatMoney documents. There is
// no float64 in this file.

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// ErrUnsupportedVoucherKind is what an *UnsupportedVoucherKindError unwraps to,
// so a handler can branch with errors.Is without naming the concrete type.
var ErrUnsupportedVoucherKind = errors.New("printing: this document kind is not one RenderVoucherDoc serves")

// ErrVoucherParticularMissing is what a *VoucherParticularError unwraps to.
var ErrVoucherParticularMissing = errors.New("printing: a particular the document cannot be understood without is missing")

// UnsupportedVoucherKindError says RenderVoucherDoc was handed a kind it does not
// serve — an invoice, a report, or an empty DocKind from a zero value.
//
// WHY THIS IS AN ERROR AND NOT A BLANK PAGE. The obvious alternative, falling
// through to the shared frame and printing a masthead with no body, produces a
// plausible-looking one-page PDF with a party name on it and nothing else. That
// document gets filed, and the missing body is discovered when someone goes
// looking for a voucher that was never actually printed. Failing here puts the
// mistake in a log line at the moment it is made.
type UnsupportedVoucherKindError struct {
	Kind DocKind
}

func (e *UnsupportedVoucherKindError) Error() string {
	kind := string(e.Kind)
	if kind == "" {
		kind = "(no kind set)"
	}
	return fmt.Sprintf("printing: %s is not a document RenderVoucherDoc can print, so nothing was produced; it serves only the delivery note, receipt, payment, credit note and debit note, and an unserved kind must fail loudly rather than be filed as a blank sheet", kind)
}

func (e *UnsupportedVoucherKindError) Unwrap() error { return ErrUnsupportedVoucherKind }

// VoucherParticularError names a particular the document cannot be printed
// without, and states the CONSEQUENCE of printing it anyway. The consequence is
// part of the error on purpose: "field Reason is required" tells an operator
// nothing, and "a recipient cannot tell a rate correction from a sales return"
// tells them what to go and find out.
type VoucherParticularError struct {
	Kind        DocKind
	Particular  string
	Consequence string
}

func (e *VoucherParticularError) Error() string {
	return fmt.Sprintf("printing: a %s cannot be printed without %s: %s", string(e.Kind), e.Particular, e.Consequence)
}

func (e *VoucherParticularError) Unwrap() error { return ErrVoucherParticularMissing }

// vdAgainstInvoiceRequirement is the statutory basis for refusing to print a credit
// or debit note that does not name the invoice it adjusts.
//
// SOURCE: Central Goods and Services Tax Act 2017, section 34 (credit and debit
// notes), read with the Central Goods and Services Tax Rules 2017, rule 53(1A),
// which lists the particulars a credit or debit note issued under section 34 must
// contain — among them the serial number(s) and date(s) of the corresponding tax
// invoice(s) or bill(s) of supply. Effective from the insertion of rule 53(1A)
// (2019) and in force as at 2026-07-29.
//
// UNVERIFIED — THE CLAUSE LETTER, AND NOTHING ELSE. The requirement above sits at
// a lettered clause WITHIN rule 53(1A). This file once recorded the author's
// recollection as clause (g) while doc.go asserted (d); at most one could be right,
// neither could be checked from this environment (the GSTN portal is not
// reachable), and doc.go's letter has since been removed. So NOTHING PRINTED ON THE
// PAPER OR IN AN ERROR CITES A LETTER: the footnote on a note cites "rule 53(1A)"
// and stops there, which is true on either reading. Do not add a letter to the
// printed string without checking the gazette.
const vdAgainstInvoiceRequirement = "issued under section 34 of the CGST Act, 2017; the original invoice number and date shown above are required particulars of a credit or debit note (CGST Rules, 2017, rule 53(1A))"

// WHY A MISSING ORIGINAL-INVOICE REFERENCE REFUSES AND A MISSING REASON DOES NOT.
// The two are not equally certain, and this file used to treat them as if they
// were — with the result that NO credit or debit note printed at all, because the
// voucher schema has no reason column for printingdb to fill (it now passes the
// voucher narration through, which is often empty).
//
//   - CITED, AND ENFORCED: a credit or debit note must NAME THE ORIGINAL INVOICE,
//     number and date. Section 34 of the CGST Act 2017 read with rule 53(1A) of the
//     CGST Rules 2017 lists those particulars, and the substance of that is not in
//     doubt on any reading of the clause lettering. A note that names no invoice
//     cannot be applied to an invoice by anybody, so it is refused: see the two
//     checks in validateVoucherDoc.
//
//   - UNVERIFIED, AND THEREFORE NOT ENFORCED: that a free-text REASON is itself a
//     mandatory particular. That is the claim that could not be checked here, and
//     refusing on an unverified rule stops a business issuing a document the law
//     may well not require of it — a worse outcome than printing a note whose
//     reason is recorded elsewhere. So the reason PRINTS WHEN PRESENT and the line
//     is OMITTED WHEN ABSENT (drawAgainstBlock); there is no placeholder, because a
//     printed "Reason: -" asserts that no reason was given, which is a claim about
//     the transaction we are not in a position to make.
//
// If the clause lettering is ever verified against the gazette and a reason IS a
// required particular, the check belongs back in validateVoucherDoc together with
// the citation — not as a silent placeholder here.

// ---------------------------------------------------------------------------
// Geometry
// ---------------------------------------------------------------------------

// The page frame, in POINTS, origin bottom-left (see the Page doc comment — y
// increases UPWARD, so the masthead is at a large y and the signature at a small
// one). These are the only magic numbers in the file and they are all geometry:
// per the note in fmtNum, a float64 here can move a rule by a hundredth of a
// point and can never change a printed figure.
const (
	vdLeft      = 40.0
	vdRight     = 555.28 // 595.28 (A4 portrait width) - 40
	vdTop       = 806.0  // baseline of the document title
	vdFooterTop = 104.0  // nothing in the body is ever drawn below this

	vdTitleSize = 14.0
	vdNameSize  = 11.0
	vdBodySize  = 9.0
	vdSmallSize = 8.5

	vdLead      = 11.0 // baseline-to-baseline inside a table row
	vdSmallLead = 10.0
	vdCellPad   = 3.0
	vdNameLead  = 13.0 // baseline-to-baseline when the issuer's name wraps

	// vdNumberBlockWidth is the width the No./Dated block on the RIGHT of the
	// masthead claims for itself, measured back from vdRight.
	vdNumberBlockWidth = 150.0

	// vdBlockGutter is the clear space kept between the two masthead blocks. It is
	// the reason the left block is wrapped to less than the full sheet: a name that
	// merely reached the number block would still print through it.
	vdBlockGutter = 12.0

	// vdSignatureBlockWidth is the width of the signature block on the right.
	vdSignatureBlockWidth = 190.0
)

// vdLeftBlockWidth is how much room the issuer's block on the LEFT of the masthead
// actually has: everything up to the number block, less the gutter.
//
// THE ISSUER'S NAME IS WRAPPED TO IT, and that is not cosmetic. Supplier.Name was
// drawn with a bare Page.Text at vdLeft, so a long registered name ran straight
// through the No./Dated block on the right — overprinting the VOUCHER NUMBER,
// which is the reference both sides quote when a customer ledger is disputed. That
// is the same dispute the Against-Bill column at the head of this file exists to
// prevent, lost to a layout fault instead of a missing field.
const vdLeftBlockWidth = vdRight - vdNumberBlockWidth - vdBlockGutter - vdLeft

// vdCenterX is the horizontal centre of the usable width, where every centred
// heading lands.
const vdCenterX = (vdLeft + vdRight) / 2

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// RenderVoucherDoc renders a delivery note, receipt, payment, credit note or
// debit note to PDF bytes.
//
// It is PURE and DETERMINISTIC: the same VoucherDoc renders byte-identical
// output in this process and any other, which is what makes the golden tests in
// voucherdoc_test.go meaningful.
//
// It renders TWICE. The first pass exists only to count the pages, so that the
// footer of page one can say "Page 1 of 3" — a receipt settling two dozen bills
// runs onto a second sheet, and a page that does not say how many follow it is a
// page that gets filed on its own. Pagination itself does not depend on the
// count (the footer sits at a fixed y), so the second pass lays out identically.
func RenderVoucherDoc(v VoucherDoc) ([]byte, error) {
	if err := validateVoucherDoc(v); err != nil {
		return nil, err
	}
	counting := &voucherRenderer{v: v}
	counting.layout()
	final := &voucherRenderer{v: v, pageCount: counting.pageNo}
	final.layout()
	return final.doc.Bytes()
}

// validateVoucherDoc refuses every document that could not be understood by the
// person who receives it. Each check names the consequence, not the field.
func validateVoucherDoc(v VoucherDoc) error {
	switch v.Kind {
	case KindDeliveryNote, KindReceipt, KindPayment, KindCreditNote, KindDebitNote:
	default:
		return &UnsupportedVoucherKindError{Kind: v.Kind}
	}

	if strings.TrimSpace(v.Number) == "" {
		return &VoucherParticularError{
			Kind:        v.Kind,
			Particular:  "its voucher number",
			Consequence: "an unnumbered voucher cannot be found again in the books it was posted to, and cannot be quoted by anyone who queries it",
		}
	}
	if v.Date.IsZero() {
		return &VoucherParticularError{
			Kind:        v.Kind,
			Particular:  "its date",
			Consequence: "FormatDate prints a zero time as a blank, so the document would go out with an empty date cell and no way to place it in a financial year",
		}
	}
	if strings.TrimSpace(v.Supplier.Name) == "" {
		return &VoucherParticularError{
			Kind:        v.Kind,
			Particular:  "the name of the business issuing it",
			Consequence: "a document with no issuer is not evidence of anything",
		}
	}
	if v.Party == nil || strings.TrimSpace(v.Party.Name) == "" {
		return &VoucherParticularError{
			Kind:        v.Kind,
			Particular:  "the other party",
			Consequence: vdPartyConsequence(v.Kind),
		}
	}

	switch v.Kind {
	case KindDeliveryNote:
		if len(v.Items) == 0 {
			return &VoucherParticularError{
				Kind:        v.Kind,
				Particular:  "the goods it accompanies",
				Consequence: "a delivery note with no lines describes no movement, and the transporter carrying it has nothing to show at a check post",
			}
		}
	case KindReceipt, KindPayment:
		if len(v.Lines) == 0 {
			return &VoucherParticularError{
				Kind:        v.Kind,
				Particular:  "the ledger legs it posted",
				Consequence: "a money voucher with no legs states an amount against nothing, so neither side can say what was settled",
			}
		}
	case KindCreditNote, KindDebitNote:
		if strings.TrimSpace(v.AgainstNumber) == "" {
			return &VoucherParticularError{
				Kind:        v.Kind,
				Particular:  "the number of the original invoice it adjusts",
				Consequence: "a note nobody can match to an invoice cannot be applied to any invoice: the recipient has no way to know which supply the credit or debit belongs to, or how much input tax credit to reverse; " + vdAgainstInvoiceRequirement,
			}
		}
		if v.AgainstDate == nil || v.AgainstDate.IsZero() {
			return &VoucherParticularError{
				Kind:        v.Kind,
				Particular:  "the date of the original invoice it adjusts",
				Consequence: "an invoice serial number repeats every financial year, so without the invoice date the note still cannot be matched to one invoice; " + vdAgainstInvoiceRequirement,
			}
		}
		// v.Reason IS NOT CHECKED HERE, deliberately. See the note above
		// vdAgainstInvoiceRequirement: the original-invoice particulars are cited and
		// enforced, a mandatory free-text reason is not verifiable from here and is
		// therefore printed when present and omitted when absent.
		if len(v.Items) == 0 && len(v.Lines) == 0 {
			return &VoucherParticularError{
				Kind:        v.Kind,
				Particular:  "what it adjusts",
				Consequence: "a note with neither goods lines nor ledger legs shows a total that cannot be tied to anything on the original invoice",
			}
		}
	}
	return nil
}

func vdPartyConsequence(k DocKind) string {
	switch k {
	case KindDeliveryNote:
		return "a delivery note with no delivery address cannot be delivered, and the goods in transit have no consignee"
	case KindReceipt:
		return "money received from nobody cannot be credited to a customer's account"
	case KindPayment:
		return "money paid to nobody cannot be debited to a supplier's account"
	default:
		return "a note with no recipient adjusts nobody's books"
	}
}

// ---------------------------------------------------------------------------
// The renderer
// ---------------------------------------------------------------------------

// voucherRenderer holds the state of one render: the document under
// construction, the page being drawn, and the cursor.
type voucherRenderer struct {
	v    VoucherDoc
	doc  *Doc
	page *Page

	y      float64 // the TOP of the next thing to be drawn
	pageNo int

	// pageCount is 0 during the counting pass, when no page footer is drawn.
	pageCount int

	cols []vdColumn
}

// vdColumn is one column of the body table. Width is a RELATIVE WEIGHT, in the
// same spirit as printing.Column: the renderer divides the usable width in these
// proportions so no caller restates geometry.
type vdColumn struct {
	header string
	align  Align
	weight float64
	wrap   bool

	x0, x1 float64 // absolute edges, filled in by vdLayoutColumns
}

// vdRow is one laid-out body row: every cell already wrapped to its column.
type vdRow struct {
	cells [][]string
	bold  bool
	lines int
}

func (r *voucherRenderer) layout() {
	r.doc = NewDoc(A4Portrait)
	r.cols = r.columns()
	vdLayoutColumns(r.cols)
	rows := r.bodyRows()

	r.startPage()
	r.drawPartyBlock()
	r.drawAgainstBlock()
	r.drawTableHead()

	for _, row := range rows {
		h := vdRowHeight(row.lines)
		r.ensure(h)
		r.drawRow(row, h)
	}
	r.rule(0.7)

	r.drawClosing()
}

// ensure guarantees h points of body space, breaking to a new page (masthead and
// table head repeated) when there is not enough left.
func (r *voucherRenderer) ensure(h float64) {
	if r.y-h >= vdFooterTop {
		return
	}
	r.rule(0.7)
	r.startPage()
	r.drawTableHead()
}

func (r *voucherRenderer) startPage() {
	r.page = r.doc.NewPage()
	r.pageNo++
	r.drawMasthead()
	r.drawPageFooter()
}

// ---------------------------------------------------------------------------
// The masthead: title, issuer, number and date. Repeated on every page.
// ---------------------------------------------------------------------------

func (r *voucherRenderer) drawMasthead() {
	p := r.page
	p.TextCenter(vdCenterX, vdTop, HelveticaBold, vdTitleSize, r.title())

	y := vdTop - 13
	if sub := r.subtitle(); sub != "" {
		p.TextCenter(vdCenterX, y, HelveticaOblique, vdSmallSize, sub)
		y -= 11
	}
	if r.pageNo > 1 {
		p.TextCenter(vdCenterX, y, HelveticaOblique, vdSmallSize, "(continued)")
		y -= 11
	}

	p.Line(vdLeft, y-2, vdRight, y-2, 1)
	y -= 15

	// Left: the issuer. Every line of the block is fitted to vdLeftBlockWidth —
	// drawn as given when it fits and wrapped when it does not — so nothing in it
	// can reach the number block on the right. See the note on vdLeftBlockWidth for
	// what used to happen when the name did.
	ly := y
	for _, line := range vdFit(HelveticaBold, vdNameSize, r.v.Supplier.Name, vdLeftBlockWidth) {
		p.Text(vdLeft, ly, HelveticaBold, vdNameSize, line)
		ly -= vdNameLead
	}
	for _, line := range vdCompanyLines(r.v.Supplier) {
		for _, fitted := range vdFit(Helvetica, vdSmallSize, line, vdLeftBlockWidth) {
			p.Text(vdLeft, ly, Helvetica, vdSmallSize, fitted)
			ly -= vdSmallLead
		}
	}
	for _, line := range r.v.Overrides.HeaderLines {
		for _, fitted := range vdFit(Helvetica, vdSmallSize, line, vdLeftBlockWidth) {
			p.Text(vdLeft, ly, Helvetica, vdSmallSize, fitted)
			ly -= vdSmallLead
		}
	}

	// Right: the number and the date, which is how this document is quoted.
	ry := y
	labelX := vdRight - vdNumberBlockWidth
	for _, kv := range [][2]string{
		{"No.", r.v.Number},
		{"Dated", FormatDate(r.v.Date)},
	} {
		p.Text(labelX, ry, Helvetica, vdBodySize, kv[0])
		p.TextRight(vdRight, ry, HelveticaBold, vdBodySize, kv[1])
		ry -= 13
	}

	r.y = vdMin(ly, ry) - 8
}

// vdCompanyLines is the issuer's address block, empty fields skipped so a company
// that stored no second address line does not print a blank row.
func vdCompanyLines(c Company) []string {
	var out []string
	out = vdAppendNonEmpty(out, c.Address1, c.Address2)
	if loc := vdJoinNonEmpty(", ", c.City, c.State); loc != "" {
		if c.PIN != "" {
			loc += " - " + c.PIN
		}
		out = append(out, loc)
	} else if c.PIN != "" {
		out = append(out, c.PIN)
	}
	if c.GSTIN != "" {
		out = append(out, "GSTIN/UIN: "+c.GSTIN)
	}
	if c.PAN != "" {
		out = append(out, "PAN: "+c.PAN)
	}
	if contact := vdJoinNonEmpty("   ", vdPrefixed("Phone: ", c.Phone), vdPrefixed("E-Mail: ", c.Email)); contact != "" {
		out = append(out, contact)
	}
	return out
}

func vdPartyLines(p Party) []string {
	var out []string
	out = vdAppendNonEmpty(out, p.Address1, p.Address2)
	if loc := vdJoinNonEmpty(", ", p.City, p.State); loc != "" {
		if p.PIN != "" {
			loc += " - " + p.PIN
		}
		out = append(out, loc)
	} else if p.PIN != "" {
		out = append(out, p.PIN)
	}
	if p.GSTIN != "" {
		out = append(out, "GSTIN/UIN: "+p.GSTIN)
	} else {
		// Stated, not left blank: an unregistered party is perfectly legal and an
		// empty GSTIN row reads as data that failed to load.
		out = append(out, "GSTIN/UIN: Unregistered")
	}
	return out
}

// title is the words at the top of the sheet. An override replaces it, which is
// how a company prints "GOODS DESPATCH NOTE" instead — but the DEFAULT for a
// delivery note is never anything containing "TAX INVOICE".
func (r *voucherRenderer) title() string {
	if t := strings.TrimSpace(r.v.Overrides.Title); t != "" {
		return t
	}
	switch r.v.Kind {
	case KindDeliveryNote:
		return "DELIVERY NOTE"
	case KindReceipt:
		return "RECEIPT VOUCHER"
	case KindPayment:
		return "PAYMENT VOUCHER"
	case KindCreditNote:
		return "CREDIT NOTE"
	case KindDebitNote:
		return "DEBIT NOTE"
	}
	return string(r.v.Kind)
}

// subtitle is the line under the title. The delivery note's is the important
// one: it says on the face of the paper that this is not a tax invoice, so that
// nobody downstream treats it as one.
func (r *voucherRenderer) subtitle() string {
	switch r.v.Kind {
	case KindDeliveryNote:
		return "Not a tax invoice. Issued for the movement of goods only; no tax is charged by this document."
	case KindCreditNote, KindDebitNote:
		return "Issued under section 34 of the CGST Act, 2017"
	}
	return ""
}

func (r *voucherRenderer) partyLabel() string {
	switch r.v.Kind {
	case KindDeliveryNote:
		return "Delivery Address (Consignee)"
	case KindReceipt:
		return "Received From"
	case KindPayment:
		return "Paid To"
	default:
		return "To"
	}
}

func (r *voucherRenderer) drawPartyBlock() {
	p := r.page
	y := r.y
	p.Text(vdLeft, y, Helvetica, vdSmallSize, r.partyLabel())
	y -= 12
	// The party block has the whole width to itself — no number block beside it —
	// but it still wraps: a consignee's registered name is routinely longer than
	// the sheet, and a name that runs off the right margin loses the words a
	// transporter or a bank needs to identify who the document is for.
	for _, line := range vdFit(HelveticaBold, vdBodySize+1, r.v.Party.Name, vdRight-vdLeft) {
		p.Text(vdLeft, y, HelveticaBold, vdBodySize+1, line)
		y -= 12
	}
	for _, line := range vdPartyLines(*r.v.Party) {
		for _, fitted := range vdFit(Helvetica, vdSmallSize, line, vdRight-vdLeft) {
			p.Text(vdLeft, y, Helvetica, vdSmallSize, fitted)
			y -= vdSmallLead
		}
	}
	r.y = y - 6
}

// drawAgainstBlock puts the original invoice — and the reason, WHEN THERE IS ONE —
// in a BOX on a credit or debit note. The box is not decoration: the invoice number
// and date are what make the note applicable to anything, and a reader must not
// have to hunt for them.
//
// AN ABSENT REASON PRINTS NO REASON LINE AND THE BOX SHRINKS BY IT. Not a blank
// "Reason:" label, which reads as a field that failed to load, and not a dash,
// which asserts that no reason was given. Why the reason is optional at all is set
// out above vdAgainstInvoiceRequirement.
func (r *voucherRenderer) drawAgainstBlock() {
	if r.v.Kind != KindCreditNote && r.v.Kind != KindDebitNote {
		return
	}
	p := r.page

	var reasonLines []string
	if reason := strings.TrimSpace(r.v.Reason); reason != "" {
		reasonLines = Wrap(Helvetica, vdBodySize, "Reason: "+reason, vdRight-vdLeft-2*vdCellPad-4)
	}
	h := 16 + vdLead*float64(len(reasonLines)) + 6

	top := r.y
	p.Rect(vdLeft, top-h, vdRight-vdLeft, h, 0.8)

	y := top - 13
	p.Text(vdLeft+vdCellPad+2, y, HelveticaBold, vdBodySize, "Against Invoice No.: "+r.v.AgainstNumber+
		"   dated "+FormatDate(*r.v.AgainstDate))
	y -= vdLead + 3
	for _, line := range reasonLines {
		p.Text(vdLeft+vdCellPad+2, y, Helvetica, vdBodySize, line)
		y -= vdLead
	}
	r.y = top - h - 8
}

func (r *voucherRenderer) drawPageFooter() {
	if r.pageCount == 0 {
		return // the counting pass draws no footer; it changes no geometry
	}
	r.page.TextRight(vdRight, 40, Helvetica, vdSmallSize,
		fmt.Sprintf("Page %d of %d", r.pageNo, r.pageCount))
	r.page.Text(vdLeft, 40, Helvetica, vdSmallSize, r.title()+" "+r.v.Number)
}

// ---------------------------------------------------------------------------
// The body table
// ---------------------------------------------------------------------------

// columns is where the five documents actually differ.
//
// THE DELIVERY NOTE HAS NO TAX COLUMN in either variant, and its value columns
// appear only when the note carries values at all — a note despatched with
// quantities only prints quantities only, rather than a column of "0.00" that
// reads like a free supply.
func (r *voucherRenderer) columns() []vdColumn {
	switch r.v.Kind {
	case KindDeliveryNote:
		cols := []vdColumn{
			{header: "Sl", align: AlignCenter, weight: 0.5},
			{header: "Description of Goods", align: AlignLeft, weight: 5.0, wrap: true},
			{header: "HSN/SAC", align: AlignLeft, weight: 1.2},
			{header: "Quantity", align: AlignRight, weight: 1.3},
			{header: "Unit", align: AlignLeft, weight: 0.8},
		}
		if r.carriesValue() {
			cols[1].weight = 3.9
			cols = append(cols,
				vdColumn{header: "Rate", align: AlignRight, weight: 1.2},
				vdColumn{header: "Value", align: AlignRight, weight: 1.5})
		}
		return cols

	case KindReceipt, KindPayment:
		return []vdColumn{
			{header: "Sl", align: AlignCenter, weight: 0.5},
			{header: "Particulars", align: AlignLeft, weight: 3.4, wrap: true},
			{header: "Against Bill", align: AlignLeft, weight: 2.4, wrap: true},
			{header: "Dr/Cr", align: AlignCenter, weight: 0.7},
			{header: "Amount", align: AlignRight, weight: 1.6},
		}

	default: // credit and debit notes
		if len(r.v.Items) > 0 {
			return []vdColumn{
				{header: "Sl", align: AlignCenter, weight: 0.5},
				{header: "Description", align: AlignLeft, weight: 3.6, wrap: true},
				{header: "HSN/SAC", align: AlignLeft, weight: 1.1},
				{header: "Quantity", align: AlignRight, weight: 1.1},
				{header: "Unit", align: AlignLeft, weight: 0.7},
				{header: "Rate", align: AlignRight, weight: 1.1},
				{header: "Taxable Value", align: AlignRight, weight: 1.5},
			}
		}
		return []vdColumn{
			{header: "Sl", align: AlignCenter, weight: 0.5},
			{header: "Particulars", align: AlignLeft, weight: 4.4, wrap: true},
			{header: "Against Bill", align: AlignLeft, weight: 2.2, wrap: true},
			{header: "Dr/Cr", align: AlignCenter, weight: 0.7},
			{header: "Amount", align: AlignRight, weight: 1.6},
		}
	}
}

// carriesValue reports whether a delivery note was despatched with values on it.
// It is a presence test, never arithmetic: it looks at whether the caller GAVE us
// an amount, and never adds one up.
func (r *voucherRenderer) carriesValue() bool {
	if !r.v.Total.IsZero() {
		return true
	}
	for _, it := range r.v.Items {
		if !it.Amount.IsZero() {
			return true
		}
	}
	return false
}

func vdLayoutColumns(cols []vdColumn) {
	var total float64
	for _, c := range cols {
		total += c.weight
	}
	if total <= 0 {
		return
	}
	usable := vdRight - vdLeft
	x := vdLeft
	for i := range cols {
		w := usable * cols[i].weight / total
		cols[i].x0 = x
		cols[i].x1 = x + w
		x += w
	}
	// The last column ends exactly on the right margin: accumulating fractions
	// would otherwise leave the rule and the amount column a hair apart.
	cols[len(cols)-1].x1 = vdRight
}

// bodyRows turns the document into laid-out rows, including the total row.
func (r *voucherRenderer) bodyRows() []vdRow {
	switch r.v.Kind {
	case KindDeliveryNote:
		// The total row appears only when the caller GAVE us a total. Deriving one
		// by summing the lines is exactly the re-derivation this package refuses:
		// a note carrying per-line values but no agreed total would otherwise
		// print a figure the ledger never blessed.
		return r.itemRows(r.carriesValue(), !r.v.Total.IsZero())
	case KindReceipt, KindPayment:
		return r.lineRows(true)
	default:
		// A NOTE'S TOTAL IS NOT A COLUMN TOTAL, so it is not printed as one. The
		// body table of a note shows the TAXABLE value being adjusted; the note's
		// total is that value plus the tax heads, and putting it at the foot of a
		// column headed "Taxable Value" would state 14,632.00 as a taxable value
		// when the taxable value is 12,400.00. It is printed in the closing block
		// instead, below the tax heads, exactly where an invoice puts it.
		if len(r.v.Items) > 0 {
			return r.itemRows(true, false)
		}
		return r.lineRows(false)
	}
}

func (r *voucherRenderer) itemRows(withValue, withTotal bool) []vdRow {
	var rows []vdRow
	for i, it := range r.v.Items {
		serial := it.SerialNo
		if serial == 0 {
			serial = i + 1
		}
		cells := []string{
			vdItoa(serial),
			it.Description,
			it.HSN,
			FormatQty(it.Quantity),
			it.Unit,
		}
		if withValue {
			cells = append(cells, FormatMoney(it.Rate), FormatMoney(it.Amount))
		}
		rows = append(rows, r.laidRow(cells, false))
	}
	if withValue && withTotal {
		rows = append(rows, r.totalRow(len(r.cols)-1, "Total"))
	}
	return rows
}

func (r *voucherRenderer) lineRows(withTotal bool) []vdRow {
	var rows []vdRow
	for i, l := range r.v.Lines {
		particulars := l.Ledger
		if l.Narration != "" {
			// A hard newline, which Wrap treats as a break, so a leg's own note
			// sits under its ledger name instead of running into the column edge.
			particulars += "\n" + l.Narration
		}
		drcr := "Cr"
		if l.IsDebit {
			drcr = "Dr"
		}
		rows = append(rows, r.laidRow([]string{
			vdItoa(i + 1),
			particulars,
			billRefCell(l.BillRef),
			drcr,
			FormatMoney(l.Amount),
		}, false))
	}
	if withTotal {
		rows = append(rows, r.totalRow(len(r.cols)-1, "Total"))
	}
	return rows
}

// billRefCell is why a receipt is worth printing. An empty BillRef becomes "On
// Account" — Tally's own term for a leg not allocated to any invoice — because an
// EMPTY cell is read as a field that failed to load, and the reader then assumes a
// bill reference exists somewhere and goes looking for it. "On Account" says the
// opposite, which is the truth about that leg: no bill reference was recorded
// against it. A bank or cash leg legitimately shows it, and so does a genuinely
// unallocated advance.
func billRefCell(ref string) string {
	if strings.TrimSpace(ref) == "" {
		return "On Account"
	}
	return ref
}

// totalRow puts the label in the column before the amount column and the amount,
// as given, in it.
func (r *voucherRenderer) totalRow(amountCol int, label string) vdRow {
	cells := make([]string, len(r.cols))
	if amountCol > 0 {
		cells[amountCol-1] = label
	}
	cells[amountCol] = FormatMoney(r.v.Total)
	row := r.laidRow(cells, true)
	return row
}

func (r *voucherRenderer) laidRow(cells []string, bold bool) vdRow {
	out := vdRow{cells: make([][]string, len(r.cols)), bold: bold, lines: 1}
	f := Helvetica
	if bold {
		f = HelveticaBold
	}
	for i := range r.cols {
		text := ""
		if i < len(cells) {
			text = cells[i]
		}
		if r.cols[i].wrap {
			lines := Wrap(f, vdBodySize, text, r.cols[i].x1-r.cols[i].x0-2*vdCellPad)
			out.cells[i] = lines
			if len(lines) > out.lines {
				out.lines = len(lines)
			}
			continue
		}
		out.cells[i] = []string{text}
	}
	return out
}

func vdRowHeight(lines int) float64 { return float64(lines)*vdLead + 3 }

func (r *voucherRenderer) drawTableHead() {
	p := r.page
	r.rule(0.7)
	h := vdRowHeight(1)
	base := r.y - vdLead + 3
	for _, c := range r.cols {
		vdDrawCell(p, c, base, HelveticaBold, c.header)
	}
	r.y -= h
	r.rule(0.7)
}

func (r *voucherRenderer) drawRow(row vdRow, h float64) {
	p := r.page
	f := Helvetica
	if row.bold {
		f = HelveticaBold
		r.rule(0.5)
	}
	base := r.y - vdLead + 3
	for i, c := range r.cols {
		b := base
		for _, line := range row.cells[i] {
			vdDrawCell(p, c, b, f, line)
			b -= vdLead
		}
	}
	r.y -= h
}

func vdDrawCell(p *Page, c vdColumn, baseline float64, f Font, text string) {
	if text == "" {
		return
	}
	switch c.align {
	case AlignRight:
		p.TextRight(c.x1-vdCellPad, baseline, f, vdBodySize, text)
	case AlignCenter:
		p.TextCenter((c.x0+c.x1)/2, baseline, f, vdBodySize, text)
	default:
		p.Text(c.x0+vdCellPad, baseline, f, vdBodySize, text)
	}
}

func (r *voucherRenderer) rule(w float64) {
	r.page.Line(vdLeft, r.y, vdRight, r.y, w)
	r.y -= 2
}

// ---------------------------------------------------------------------------
// The closing block: taxes, words, narration, declaration, signature
// ---------------------------------------------------------------------------

func (r *voucherRenderer) drawClosing() {
	blocks := r.closingBlocks()
	var need float64
	for _, b := range blocks {
		need += b.height
	}
	need += r.signatureHeight()
	if r.y-need < 44 {
		r.startPage()
	}
	r.y -= 8
	for _, b := range blocks {
		b.draw(r)
	}
	r.drawSignature()
}

// vdBlock is one closing block: how tall it is, and how to draw it. Measuring
// before drawing is what lets the whole tail move to a fresh page together
// instead of a signature landing alone on page three.
type vdBlock struct {
	height float64
	draw   func(*voucherRenderer)
}

// vdSignatureHeight is the block's height when the issuer's name fits on ONE line,
// which is the ordinary case. signatureHeight() is what the layout actually uses.
const vdSignatureHeight = 74.0

// signatureNameLines is the "for <issuer>" line of the signature block, wrapped to
// the block's own width. It is a function and not a field so that the measuring
// pass and the drawing pass cannot be handed different line counts.
func (r *voucherRenderer) signatureNameLines() []string {
	return vdFit(Helvetica, vdSmallSize, "for "+strings.TrimSpace(r.v.Supplier.Name), vdSignatureBlockWidth)
}

// signatureHeight is the block's height for THIS document: the base height plus a
// line for each line the issuer's name wrapped onto.
//
// IT GROWS RATHER THAN LETTING THE NAME RUN OFF THE PAGE. The alternative — a
// fixed height with a wrapped name inside it — puts the second line of the name
// through the signature rule, so the person signing signs across their own
// company's name. The alternative to wrapping at all is what was here before: the
// name printed past vdRight and off the edge of the paper.
func (r *voucherRenderer) signatureHeight() float64 {
	return vdSignatureHeight + float64(len(r.signatureNameLines())-1)*vdSmallLead
}

func (r *voucherRenderer) closingBlocks() []vdBlock {
	var out []vdBlock

	// The tax adjustment, per head. A credit note is the document the recipient
	// reverses input tax credit out of, so CGST/SGST/IGST/Cess must each appear
	// at their own rate and amount, exactly as on an invoice.
	if len(r.v.Taxes) > 0 {
		taxes := r.v.Taxes
		out = append(out, vdBlock{
			height: 14 + vdLead*float64(len(taxes)),
			draw: func(rr *voucherRenderer) {
				p := rr.page
				y := rr.y - 11
				p.Text(vdLeft, y, HelveticaBold, vdBodySize, "Tax adjusted by this document")
				y -= vdLead
				for _, t := range taxes {
					label := t.Name
					if !t.Rate.IsZero() {
						label += " @ " + FormatRate(t.Rate) + "%"
					}
					p.Text(vdLeft+12, y, Helvetica, vdBodySize, label)
					p.TextRight(vdRight, y, Helvetica, vdBodySize, FormatMoney(t.Amount))
					y -= vdLead
				}
				rr.y = y - 3
			},
		})
	}

	// A note's total, under the tax heads: taxable value plus the tax adjusted, in
	// the same place an invoice puts its total, and printed as v.Total was GIVEN.
	if r.v.Kind == KindCreditNote || r.v.Kind == KindDebitNote {
		out = append(out, vdBlock{
			height: 24,
			draw: func(rr *voucherRenderer) {
				p := rr.page
				y := rr.y - 13
				p.Line(vdRight-210, y+9, vdRight, y+9, 0.7)
				p.Text(vdRight-210, y, HelveticaBold, vdBodySize, "Total")
				p.TextRight(vdRight, y, HelveticaBold, vdBodySize, FormatMoney(rr.v.Total))
				rr.y = y - 8
			},
		})
	}

	// The amount in words. On a receipt it is the operative amount, the same way
	// it is on a cheque, and it is the only place a transposed digit shows up.
	if r.wantsWords() {
		words := "Amount (in words): " + AmountInWords(r.v.Total)
		lines := Wrap(Helvetica, vdBodySize, words, vdRight-vdLeft)
		out = append(out, vdBlock{
			height: 6 + vdLead*float64(len(lines)),
			draw: func(rr *voucherRenderer) {
				y := rr.y - 11
				for i, line := range lines {
					f := HelveticaBold
					if i > 0 {
						f = Helvetica
					}
					rr.page.Text(vdLeft, y, f, vdBodySize, line)
					y -= vdLead
				}
				rr.y = y - 3
			},
		})
	}

	if n := strings.TrimSpace(r.v.Narration); n != "" {
		lines := Wrap(Helvetica, vdSmallSize, "Narration: "+n, vdRight-vdLeft)
		out = append(out, vdTextBlock(lines, Helvetica, vdSmallSize))
	}

	// The statutory footnote on a note is NOT the overridable Declaration: a
	// company that customises its declaration must not thereby delete the line
	// that says under what authority the note was issued.
	if r.v.Kind == KindCreditNote || r.v.Kind == KindDebitNote {
		lines := Wrap(HelveticaOblique, vdSmallSize, "This document is "+vdAgainstInvoiceRequirement+".", vdRight-vdLeft)
		out = append(out, vdTextBlock(lines, HelveticaOblique, vdSmallSize))
	}

	if d := strings.TrimSpace(r.declaration()); d != "" {
		lines := Wrap(Helvetica, vdSmallSize, "Declaration: "+d, vdRight-vdLeft)
		out = append(out, vdTextBlock(lines, Helvetica, vdSmallSize))
	}
	if t := strings.TrimSpace(r.v.Overrides.Terms); t != "" {
		lines := Wrap(Helvetica, vdSmallSize, "Terms: "+t, vdRight-vdLeft)
		out = append(out, vdTextBlock(lines, Helvetica, vdSmallSize))
	}
	if r.v.Overrides.ShowBankDetails && len(r.v.Overrides.BankLines) > 0 {
		lines := append([]string{"Bank Details:"}, r.v.Overrides.BankLines...)
		out = append(out, vdTextBlock(lines, Helvetica, vdSmallSize))
	}
	if len(r.v.Overrides.FooterLines) > 0 {
		out = append(out, vdTextBlock(r.v.Overrides.FooterLines, Helvetica, vdSmallSize))
	}
	return out
}

func vdTextBlock(lines []string, f Font, size float64) vdBlock {
	return vdBlock{
		height: 5 + vdSmallLead*float64(len(lines)),
		draw: func(rr *voucherRenderer) {
			y := rr.y - 10
			for _, line := range lines {
				rr.page.Text(vdLeft, y, f, size, line)
				y -= vdSmallLead
			}
			rr.y = y - 2
		},
	}
}

// wantsWords: the money documents and the notes state their amount in words. A
// delivery note does not — it is not an instrument, and words on it would invite
// someone to read it as a demand for payment.
func (r *voucherRenderer) wantsWords() bool {
	switch r.v.Kind {
	case KindReceipt, KindPayment, KindCreditNote, KindDebitNote:
		return true
	}
	return false
}

func (r *voucherRenderer) declaration() string {
	if d := r.v.Overrides.Declaration; d != "" {
		return d
	}
	switch r.v.Kind {
	case KindDeliveryNote:
		return "Received the goods described above in good order and condition."
	case KindReceipt:
		return "Received subject to realisation of the instrument."
	}
	return ""
}

// drawSignature prints the issuer's signature block on the right, and — on a
// delivery note only — the receiver's on the left, because a delivery note that
// nobody signed for proves no delivery.
func (r *voucherRenderer) drawSignature() {
	p := r.page
	h := r.signatureHeight()
	top := r.y - 10
	if top < 44+h {
		top = 44 + h
	}

	label := r.v.Overrides.SignatureLabel
	if label == "" {
		label = "Authorised Signatory"
	}

	nameLines := r.signatureNameLines()
	// extra is the space the wrapped name took beyond its first line; the rule and
	// the label move down by exactly that, which is what keeps the signing space
	// clear on a document issued by a company with a long registered name.
	extra := float64(len(nameLines)-1) * vdSmallLead
	ny := top
	for _, line := range nameLines {
		p.Text(vdRight-vdSignatureBlockWidth, ny, Helvetica, vdSmallSize, line)
		ny -= vdSmallLead
	}
	p.Line(vdRight-vdSignatureBlockWidth, top-46-extra, vdRight, top-46-extra, 0.5)
	p.TextRight(vdRight, top-58-extra, Helvetica, vdSmallSize, label)

	if r.v.Kind == KindDeliveryNote {
		p.Text(vdLeft, top, Helvetica, vdSmallSize, "Received by")
		p.Line(vdLeft, top-46-extra, vdLeft+vdSignatureBlockWidth, top-46-extra, 0.5)
		p.Text(vdLeft, top-58-extra, Helvetica, vdSmallSize, "Signature / Date")
	}
	r.y = top - h
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

// vdFit is Wrap for a line that has to stay inside a block: the line AS GIVEN when
// it already fits width, and wrapped when it does not. report.go uses it for the
// report letterhead as well; it is in this file because this is where the idiom
// below lives.
//
// WHY NOT ALWAYS Wrap. Wrap normalises whitespace — strings.Fields collapses every
// run of blanks — and this file has a line that depends on one: vdCompanyLines
// joins "Phone: ..." to "E-Mail: ..." with a deliberate three-space gap, which is
// what makes the two readable as two facts rather than one. Re-flowing a line that
// already fits would silently close that gap up, and the same goes for a letterhead
// line a company typed into its own template. A line that does NOT fit has to be
// re-flowed whatever its spacing, because the alternative is printing it through
// the voucher-number block or off the edge of the paper.
func vdFit(f Font, size float64, line string, width float64) []string {
	if Measure(f, size, line) <= width {
		return []string{line}
	}
	return Wrap(f, size, line, width)
}

func vdAppendNonEmpty(out []string, vals ...string) []string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func vdJoinNonEmpty(sep string, vals ...string) string {
	var keep []string
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			keep = append(keep, v)
		}
	}
	return strings.Join(keep, sep)
}

func vdPrefixed(prefix, v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return prefix + v
}

func vdMin(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// vdItoa renders a line's serial number. A serial number is a COUNT, not money:
// it goes nowhere near FormatMoney and gets no digit grouping, so line 1,234 of a
// long delivery note reads as a line number and not as an amount.
func vdItoa(n int) string { return strconv.Itoa(n) }
