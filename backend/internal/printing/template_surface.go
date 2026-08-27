package printing

// template_surface.go — WHAT A COMPANY MAY CUSTOMISE, AND WHAT IT GETS IF IT DOES NOT.
//
// TemplateOverrides has eight fields and DocKind has eight values, and the
// tempting reading — that the grid is full — is WRONG. RenderReport honours
// exactly three of the eight (Title, HeaderLines, FooterLines) and ignores the
// rest on purpose: printing "Authorised Signatory" under a Trial Balance would
// imply a certification nobody gave, and a Trial Balance has no declaration to
// make. handler_print_reports.go has said so in a comment since the report
// letterhead shipped; this file is that comment turned into something a caller
// can ASK, because a comment cannot be served to a user interface.
//
// ⛔ WHY THIS EXISTS AT ALL. An editing screen that offers a Declaration box for
// the `report` kind is a promise the renderer will never keep: the operator
// types a declaration, saves it, prints the report and it is not there. Nothing
// fails, nothing logs, and the product has lied. The screen therefore has to
// know which fields bite for which kind — and the only correct answer is the one
// the renderers actually implement.
//
// ⛔ AND WHY THE RENDERERS CALL INTO IT RATHER THAN THE OTHER WAY AROUND. The
// first draft of this file declared its own table of defaults beside the ones
// already living in invoice.go and voucherdoc.go. That is the drift bug this
// repository has paid for three times: two tables, both plausible, and the one
// the screen shows stops being the one the paper shows. invTitle, declaration()
// and the signature label now READ these functions, so there is one table and a
// wrong answer here is a wrong answer on the printed page too — which
// template_surface_golden_test.go measures by rendering.

// TemplateField names one overridable piece of a printed document.
//
// The strings are the print_template COLUMN names, not prose, so a caller can
// map a field straight onto the row it writes — with one deliberate exception
// noted on FieldBankDetails.
type TemplateField string

const (
	FieldTitle          TemplateField = "title"
	FieldHeaderLines    TemplateField = "header_lines"
	FieldFooterLines    TemplateField = "footer_lines"
	FieldDeclaration    TemplateField = "declaration"
	FieldTerms          TemplateField = "terms"
	FieldSignatureLabel TemplateField = "signature_label"

	// FieldBankDetails covers show_bank_details AND bank_lines, which are TWO
	// columns and ONE control.
	//
	// They are inseparable in the renderer: every consumer tests
	// `ShowBankDetails && len(BankLines) > 0`, so bank lines with the flag off
	// print nothing and the flag on with no lines prints nothing. Offering them
	// as two independent fields would let an operator fill in three lines of
	// account details, save, and print a document with no bank block — having
	// done everything the screen asked.
	FieldBankDetails TemplateField = "bank_details"
)

// AllDocKinds is every kind this package renders, in a STABLE order.
//
// Stable because it is serialised into a golden the frontend consumes: a map
// range would reorder the artefact on every run and make the diff meaningless.
// The order is the declaration order in doc.go — the two invoice kinds, the five
// voucher documents, then the statement.
func AllDocKinds() []DocKind {
	return []DocKind{
		KindTaxInvoice,
		KindBillOfSupply,
		KindDeliveryNote,
		KindReceipt,
		KindPayment,
		KindCreditNote,
		KindDebitNote,
		KindReport,
	}
}

// AllTemplateFields is every overridable field, in a stable order suitable for
// laying out a form top to bottom.
func AllTemplateFields() []TemplateField {
	return []TemplateField{
		FieldTitle,
		FieldHeaderLines,
		FieldFooterLines,
		FieldDeclaration,
		FieldTerms,
		FieldSignatureLabel,
		FieldBankDetails,
	}
}

// reportHonours is the THREE fields RenderReport reads. Named rather than
// inlined so the exception is findable from either direction.
var reportHonours = []TemplateField{FieldTitle, FieldHeaderLines, FieldFooterLines}

// HonouredFields returns the fields the renderer for kind actually reads, in
// AllTemplateFields order.
//
// An UNKNOWN kind returns nil — not everything. A caller that mistypes a kind
// gets a form with no fields, which is visibly broken, rather than a form
// offering all seven whose saved values would never print.
func HonouredFields(kind DocKind) []TemplateField {
	switch kind {
	case KindTaxInvoice, KindBillOfSupply,
		KindDeliveryNote, KindReceipt, KindPayment, KindCreditNote, KindDebitNote:
		// RenderInvoice and RenderVoucherDoc both read all seven. Measured, not
		// assumed: template_surface_golden_test.go renders each kind twice per
		// field and compares the bytes.
		return AllTemplateFields()
	case KindReport:
		return reportHonours
	}
	return nil
}

// Honours reports whether the renderer for kind reads field.
func Honours(kind DocKind, field TemplateField) bool {
	for _, f := range HonouredFields(kind) {
		if f == field {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The built-in defaults — what prints when a company has overridden nothing
// ---------------------------------------------------------------------------

// DefaultTitle is the heading a kind prints with no override.
//
// ⚠️ KindReport RETURNS THE EMPTY STRING, AND THAT IS NOT A GAP. A statement's
// default title is the statement's own name — "Trial Balance", "Day Book" — which
// arrives on ReportTable.Title from the report builder and is different for every
// one of the thirty reports. There is no single default to show, so an editing
// screen must say "the report's own name" rather than print a blank box.
func DefaultTitle(kind DocKind) string {
	switch kind {
	case KindTaxInvoice:
		return invDefaultTaxInvoiceTitle
	case KindBillOfSupply:
		return invDefaultBillOfSupplyTitle
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
	return ""
}

// DefaultDeclaration is the certification a kind prints with no override.
//
// ⚠️ THREE KINDS HAVE NO DEFAULT DECLARATION AND PRINT NO DECLARATION BLOCK AT
// ALL — payment, credit-note and debit-note. The empty string here means "this
// document says nothing unless the company gives it something to say", which is
// different from KindReport, where the field is not read at all. A screen must
// keep those apart: the first is an empty box worth filling, the second is a box
// that should not be on the form.
//
// ⛔ AND IT IS NOT THE STATUTORY FOOTNOTE. A credit or debit note prints a line
// naming the authority it is issued under, and voucherdoc.go draws that
// SEPARATELY and unconditionally, precisely so a company customising its
// declaration cannot delete it.
func DefaultDeclaration(kind DocKind) string {
	switch kind {
	case KindTaxInvoice:
		return invDefaultDeclaration
	case KindBillOfSupply:
		return invDefaultBillOfSupplyDeclaration
	case KindDeliveryNote:
		return "Received the goods described above in good order and condition."
	case KindReceipt:
		return "Received subject to realisation of the instrument."
	}
	return ""
}

// DefaultSignatureLabel is the label under the signature panel with no
// override. Rule 46(q) and Rule 49(h) want a signature; this is what the space
// for one is called.
func DefaultSignatureLabel(kind DocKind) string {
	if !Honours(kind, FieldSignatureLabel) {
		return ""
	}
	return invDefaultSignatureLabel
}
