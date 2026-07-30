// Package printing turns what the books already hold into the paper a business
// hands over: a Rule 46 tax invoice, a delivery note, a receipt, and the
// statements an accountant reads.
//
// WHY THIS PACKAGE HAS NO DATABASE AND NO DEPENDENCIES
// ---------------------------------------------------
// Everything here is PURE: a document value in, deterministic bytes out. The
// DB-facing half lives in internal/printing/printingdb, which reads a voucher
// and assembles the values below. Two consequences that are the whole point:
//
//   - The bytes are REPRODUCIBLE, so a golden test can assert on them. A print
//     pipeline verified by looking at a screenshot is not verified.
//   - Money is never re-derived here. Every amount arrives as a
//     decimal.Decimal already agreed with the ledger, and rendering is
//     decimal.StringFixed. There is no float64 in this package and no
//     arithmetic that could disagree with the posting it came from — a printed
//     total that differs from the ledger by a paisa is an invoice a customer
//     disputes.
//
// The PDF writer is hand-rolled against the PDF 1.4 spec using only the
// base-14 fonts, so there is no new module dependency, no font file to ship,
// and no library version that could move the golden bytes underneath us.
package printing

import (
	"time"

	"github.com/shopspring/decimal"
)

// DocKind names every document this package can render. It is also the value
// printingdb stores against a template override row, so the strings are stable
// and are part of the on-disk contract.
type DocKind string

const (
	KindTaxInvoice   DocKind = "tax-invoice"
	KindBillOfSupply DocKind = "bill-of-supply"
	KindDeliveryNote DocKind = "delivery-note"
	KindReceipt      DocKind = "receipt"
	KindPayment      DocKind = "payment"
	KindCreditNote   DocKind = "credit-note"
	KindDebitNote    DocKind = "debit-note"
	KindReport       DocKind = "report"
)

// Company is the SUPPLIER block: who is issuing the document. Rule 46(a) of the
// CGST Rules 2017 requires the name, address and GSTIN of the supplier on a tax
// invoice, so these fields are not decoration — ValidateRule46 refuses to print
// without them.
type Company struct {
	Name      string
	Address1  string
	Address2  string
	City      string
	State     string
	StateCode string // the two-digit GST state code
	PIN       string
	GSTIN     string
	PAN       string
	Phone     string
	Email     string
}

// Party is the other side: a customer on a sale, a supplier on a purchase, or
// the ship-to address when it differs from the bill-to (Rule 46(o)).
type Party struct {
	Name      string
	Address1  string
	Address2  string
	City      string
	State     string
	StateCode string
	PIN       string
	GSTIN     string // empty for an unregistered party, which is legal
}

// LineItem is one row of goods or services. Amount is the TAXABLE VALUE of the
// line as the voucher stored it — it is never recomputed from Quantity × Rate
// here, because the voucher may carry a discount or a rounding the recomputation
// would not reproduce.
type LineItem struct {
	SerialNo    int
	Description string
	HSN         string
	Quantity    decimal.Decimal
	Unit        string
	Rate        decimal.Decimal

	// DiscountPct is a PERCENTAGE, 0..100 — never a rupee amount.
	//
	// THE NAME CARRIES THE UNIT BECAUSE THE UNITLESS NAME ALREADY CAUSED THE BUG.
	// This field was called Discount for one wave. stock_movement stores
	// discount_pct (a percentage, CHECK 0..100), the DB half assigned it
	// verbatim, and the renderer printed it through FormatMoney as
	// "Less: Discount 2.50" against a line whose discount was in fact Rs 25.00 —
	// a tax invoice stating four figures that cannot all be true, understating
	// the discount by qty × rate ÷ 100. Rule 46(k) requires the taxable value
	// to take the discount into account, so the face of the document
	// contradicted the taxable value printed beside it.
	//
	// Render it as a percentage (FormatRate + "%"). Do NOT convert it to rupees
	// in the print path: the books store the percentage and the already-net
	// Amount, and re-deriving the rupee discount here would put a number on the
	// paper that no posting holds.
	DiscountPct decimal.Decimal

	Amount decimal.Decimal
}

// TaxHead is one head of tax at one rate: CGST, SGST, IGST or Cess. Rule 46(l)
// and 46(m) require the RATE and the AMOUNT of tax per head to appear
// separately, which is why this is a slice on the document and not two totals.
type TaxHead struct {
	Name   string // "CGST", "SGST", "IGST", "Cess"
	Rate   decimal.Decimal
	Amount decimal.Decimal
}

// Invoice is a tax invoice (KindTaxInvoice) or a bill of supply
// (KindBillOfSupply — a composition dealer or an exempt supply, where no tax is
// charged and Rule 49 governs instead of Rule 46).
type Invoice struct {
	Kind      DocKind
	Number    string
	Date      time.Time
	DueDate   *time.Time
	Supplier  Company
	Recipient Party
	ShipTo    *Party

	PlaceOfSupply     string // the state whose tax applies
	PlaceOfSupplyCode string
	ReverseCharge     bool

	Items    []LineItem
	Taxes    []TaxHead
	Subtotal decimal.Decimal // sum of the line taxable values, as stored
	RoundOff decimal.Decimal
	Total    decimal.Decimal

	// IRN and AckNo are present once the e-invoice workflow has registered the
	// document. Rule 48(4) makes the IRN part of what a registered invoice is,
	// so when we have it, it prints.
	IRN   string
	AckNo string

	Overrides TemplateOverrides
}

// VoucherLine is one accounting leg on a receipt, payment or note.
type VoucherLine struct {
	Ledger    string
	IsDebit   bool
	Amount    decimal.Decimal
	BillRef   string // the bill this leg settles, when it names one
	Narration string
}

// VoucherDoc covers the documents that are not invoices: the delivery note
// (goods moved, quantities only unless the note carries value), the receipt and
// payment vouchers, and the credit and debit notes.
//
// A credit or debit note MUST name the invoice it adjusts (CGST Rules 2017,
// rule 53(1A)), and that is what AgainstNumber/AgainstDate carry. THE CLAUSE
// LETTER IS DELIBERATELY OMITTED: this comment said "53(1A)(d)" for one wave
// while voucherdoc.go recorded the author's recollection as clause (g) and
// marked it UNVERIFIED. At most one was right, neither could be checked from
// this environment, and a false citation in the file every other slice compiles
// against is worse than no citation — a future author lifts it into a
// user-visible message. The SUBSTANCE (the original invoice must be named) is
// not in doubt; the letter is, so the letter is not asserted.
type VoucherDoc struct {
	Kind     DocKind
	Number   string
	Date     time.Time
	Supplier Company
	Party    *Party

	Lines []VoucherLine
	Items []LineItem // a delivery note's quantities
	Taxes []TaxHead  // a note's tax adjustment

	AgainstNumber string
	AgainstDate   *time.Time
	Reason        string

	Total     decimal.Decimal
	Narration string

	Overrides TemplateOverrides
}

// Align is a column's horizontal alignment in a report table.
type Align int

const (
	AlignLeft Align = iota
	AlignRight
	AlignCenter
)

// Column describes one column of a report. Width is a RELATIVE WEIGHT, not
// points: the renderer divides the usable page width in these proportions, so a
// report renders the same on portrait and landscape without every caller
// restating the geometry.
type Column struct {
	Header string
	Align  Align
	Width  float64
}

// Row is one line of a report. Cells are ALREADY FORMATTED STRINGS.
//
// WHY STRINGS AND NOT decimal.Decimal: the caller — internal/server — owns the
// formatting decision, and it already holds the decimal. Handing this package a
// number would mean the renderer decides scale and grouping, which is how a
// report and a ledger start disagreeing about the same amount. The rule is:
// format with FormatMoney at the seam, never inside the renderer.
type Row struct {
	Cells     []string
	Indent    int  // indent level for the first cell (a group tree)
	Bold      bool // a total or a group heading
	RuleAbove bool
	RuleBelow bool
}

// ReportTable is any of the statements an accountant is handed. One shape
// serves all of them deliberately: a Trial Balance and a Payroll Register
// differ in their columns and their title, not in how they are put on paper.
type ReportTable struct {
	Title     string // "Trial Balance"
	Subtitle  string // the period or as-on date, already worded
	Company   Company
	Columns   []Column
	Rows      []Row
	Footer    []Row // totals, ruled off from the body
	Landscape bool
	Overrides TemplateOverrides
}

// TemplateOverrides is the per-company, per-document-kind customisation stored
// by migration 0037. Every field is OPTIONAL and an absent one means "use the
// built-in default", which is why a fresh company with zero template rows can
// still print on day one: the default lives in code, and this table holds only
// what a company chose to change.
type TemplateOverrides struct {
	Title           string   // replaces the document title
	HeaderLines     []string // extra letterhead lines under the company block
	FooterLines     []string
	Declaration     string // the certification line above the signature
	Terms           string
	SignatureLabel  string // "Authorised Signatory" by default
	ShowBankDetails bool
	BankLines       []string
}
