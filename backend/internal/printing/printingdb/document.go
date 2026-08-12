package printingdb

// document.go — A STORED VOUCHER BECOMES A PRINTABLE DOCUMENT.
//
// This is the seam between the books and the paper. internal/printing is pure
// (a document value in, deterministic bytes out) and knows nothing about
// Postgres; internal/ledger holds an immutable, hash-chained voucher and knows
// nothing about paper. This file is the only place that reads one and produces
// the other.
//
// WHAT IT NEVER DOES: ARITHMETIC THAT COULD DISAGREE WITH THE LEDGER
// -----------------------------------------------------------------
// Every amount on the returned document is a value the books already hold:
//
//	Subtotal  = voucher_version.taxable_value        (the stored taxable base)
//	Taxes     = voucher_gst_line.cgst/sgst/igst      (the stored per-line tax)
//	Total     = the PARTY LEG of the postings        (what the customer owes)
//	RoundOff  = Total - Subtotal - sum(Taxes)        (the reconciling residual)
//
// RoundOff is the only computed figure, and it is computed as a RESIDUAL on
// purpose: whatever the books contain, Subtotal + Taxes + RoundOff == Total
// holds exactly, and the printed total is BY CONSTRUCTION the amount posted to
// the customer's ledger. A print pipeline that re-derived the total from the
// lines would eventually print a figure the receivable does not agree with, and
// the customer would be the one to notice. Everything is decimal.Decimal end to
// end; there is no float64 in this file and no rounding step anywhere.
//
// WHY THE REFUSALS ARE HERE AND NOT ONLY IN THE RENDERER
// -----------------------------------------------------
// internal/printing validates a document VALUE ("Recipient.GSTIN is empty").
// This file validates the BOOKS ("ledger \"Acme Corp\" is marked REGULAR under
// GST and carries no GSTIN"), which is the sentence an operator can act on, and
// it refuses BEFORE any bytes exist. The two are complementary, not duplicated:
// one names a struct field, the other names the master record somebody has to
// go and fix. A tax invoice with a blank mandatory particular is not a cosmetic
// defect — it is a document the recipient cannot claim credit against.
//
// STATUTORY SOURCES CITED IN THIS FILE
//
//	CGST Rules 2017, Rule 46 — particulars of a tax invoice. Notified by
//	  Notification No. 3/2017-Central Tax, effective 2017-07-01.
//	CGST Rules 2017, Rule 49 — bill of supply (a supply on which no tax is
//	  charged): the same supplier/recipient particulars as Rule 46, without the
//	  rate and amount of tax. Effective 2017-07-01.
//	CGST Rules 2017, Rule 48(4) — e-invoice / IRN. Effective 2020-10-01 for the
//	  first turnover band; the band itself has moved several times.
//	CGST Rules 2017, Rule 53(1A) — particulars of a credit or debit note,
//	  including the number and date of the document it adjusts. Inserted in 2019
//	  and in force as at 2026-07-29; the exact notification and its date could
//	  NOT be verified from this environment, and neither could the LETTER of the
//	  clause within 53(1A) — see internal/printing/voucherdoc.go, which spells
//	  out why no letter is stated anywhere in this track.
//
// THE NAME OF A STATE comes from internal/gststate, a pure leaf that maps a
// two-digit GST State code to the name the statute that named it uses. Nothing
// in this schema stores a state NAME, and Rule 46(n) and Rule 46(e) both require
// one, so before that package every inter-State tax invoice and every supply of
// fifty thousand rupees or more to an unregistered person was refused with no
// field an operator could fill in. A code that package will not name is REFUSED
// here (see UnknownStateCodeError) rather than printed as a code or as a guess.
//
// The sub-clause LETTERING of Rule 46 follows internal/printing/doc.go, which
// this track's contract already fixed (it cites 46(a) for the supplier block
// and 46(n)/46(o) for place of supply and address of delivery). Sub-clause
// letters are quoted here for continuity with that file; the PARTICULARS
// themselves — supplier name/address/GSTIN, serial number, date, recipient
// name/address/GSTIN if registered, place of supply, rate and amount of tax per
// head — are what the refusals below actually enforce.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/gststate"
	"github.com/khata/cored/internal/printing"
)

// ErrVoucherNotFound is returned when this company has no LIVE voucher with the
// given id.
//
// A TOMBSTONED VOUCHER IS NOT FOUND, DELIBERATELY. voucher_head keeps the head
// row of a deleted voucher (that is what makes the edit log readable), so a
// print path that read the current version without the is_deleted filter would
// happily hand somebody a fresh copy of a CANCELLED invoice — and the copy
// would look exactly like a live one. History is for internal/ledger's history
// route to show; paper is only ever produced for a voucher that still stands.
var ErrVoucherNotFound = errors.New("printingdb: no live voucher with that id in this company")

// ErrNoDocumentForClass is returned for a voucher class this track prints NO
// document for at all: Contra, Journal, Payroll, Purchase, Receipt Note, Stock
// Journal and Physical Stock.
//
// IT IS AN ERROR RATHER THAN AN EMPTY DOCUMENT because the alternative is a
// blank page with a company letterhead on it. A pay run printed as an
// almost-empty "voucher" would be handed to an employee; a Purchase printed as
// a tax invoice would be OUR company issuing a document only the SUPPLIER may
// issue. Refusing names the class in the message so the HTTP layer can turn it
// into a 400 that says what is wrong.
var ErrNoDocumentForClass = errors.New("printingdb: this voucher class has no printable document")

// ErrNotAnInvoice / ErrNotAVoucherDoc separate "there is no document" from "you
// asked the wrong loader for it". A Receipt HAS a document; it is simply not an
// Invoice, and a caller that got ErrNoDocumentForClass for it would go looking
// for a missing feature instead of calling LoadVoucherDoc.
var (
	ErrNotAnInvoice   = errors.New("printingdb: this voucher prints as a voucher document, not an invoice")
	ErrNotAVoucherDoc = errors.New("printingdb: this voucher prints as an invoice, not a voucher document")
)

// ErrMissingParticular is the sentinel behind every MissingParticularError, so
// a caller can branch on "the books do not contain what this document must
// state" with one errors.Is and still get the specific field from the typed
// value.
var ErrMissingParticular = errors.New("printingdb: a mandatory particular is missing from the books")

// MissingParticularError names ONE mandatory particular the books cannot
// supply, where it should have come from, and the rule that makes it mandatory.
//
// WHY IT NAMES THE SOURCE AND NOT JUST THE FIELD. "Recipient.GSTIN is empty" is
// a fact about a Go struct. "ledger \"Acme Corp\" is marked REGULAR under GST
// and carries no GSTIN" is a fact about a master record, and it tells the person
// reading the error exactly which screen to open. Only the second one gets the
// invoice printed.
type MissingParticularError struct {
	// Kind is the document that was being assembled.
	Kind printing.DocKind
	// Field is the particular, in the statute's vocabulary ("recipient GSTIN"),
	// not the struct field's name.
	Field string
	// Source is where the value should have come from: a table and column, or a
	// named master record.
	Source string
	// Rule cites what makes the particular mandatory.
	Rule string
	// NotModelled distinguishes the two fixes, which is the whole reason this
	// flag exists: false means SOMEBODY MUST TYPE IT IN (the column is there and
	// empty), true means THIS SCHEMA HAS NO COLUMN FOR IT and the fix is a
	// migration. Reporting both as one error class would have an operator hunting
	// through a data-entry screen for a field that does not exist.
	NotModelled bool
}

func (e *MissingParticularError) Error() string {
	fix := "it is not recorded"
	if e.NotModelled {
		fix = "this schema has no column for it"
	}
	return fmt.Sprintf(
		"printingdb: cannot print a %s: %s is mandatory (%s) and %s (source: %s)",
		e.Kind, e.Field, e.Rule, fix, e.Source)
}

// Unwrap makes errors.Is(err, ErrMissingParticular) true.
func (e *MissingParticularError) Unwrap() error { return ErrMissingParticular }

// ErrUnknownStateCode is the sentinel behind UnknownStateCodeError.
var ErrUnknownStateCode = errors.New(
	"printingdb: a state code on this voucher denotes no State this system will name")

// UnknownStateCodeError refuses a document whose State code cannot be turned
// into the NAME of a State.
//
// WHY THE REFUSAL IS THE RIGHT ANSWER AND A CODE IN THE NAME'S PLACE IS NOT.
// Rule 46(n) requires the place of supply "along with the name of the State" and
// Rule 46(e) the recipient's State by name; internal/gststate answers for the
// codes it can stand behind and refuses the rest — a retired code (25 after the
// 2020 merger, 28 after the Andhra Pradesh bifurcation), a code whose meaning
// could not be verified from this environment (96, 99), and anything that is not
// a code at all. For each of those, the three alternatives to refusing are worse:
// printing the CODE where the statute asks for a name reads as correct to every
// reader who does not know the difference; printing a blank issues a defective
// invoice; and printing a plausible name asserts a place of supply nobody can
// check, which is the one failure a GST officer reads directly off the page.
//
// The message NAMES THE CODE and carries gststate's own sentence about it, so the
// operator is told what to correct the master TO ("state code 25 is retired: it
// merged into 26") rather than merely that something is wrong.
type UnknownStateCodeError struct {
	// Kind is the document that was being assembled.
	Kind printing.DocKind
	// Field is the particular, in the statute's vocabulary: "the place of supply"
	// or "the recipient's State".
	Field string
	// Code is the value found in the books, verbatim — whitespace, wrong width and
	// all, because that is what somebody has to go and find.
	Code string
	// Source is the table and column, or the named master record, the code came
	// from.
	Source string
	// Rule cites what makes the NAME (and not merely the code) mandatory.
	Rule string
	// Explanation is internal/gststate's sentence about this code: retired, not a
	// code at all, or believed-but-unverified.
	Explanation string
}

func (e *UnknownStateCodeError) Error() string {
	return fmt.Sprintf(
		"printingdb: cannot print a %s: %s is recorded as state code %q, and %s — "+
			"%s requires the NAME of the State, and printing the code in its place would state a "+
			"State nobody can check (source: %s)",
		e.Kind, e.Field, e.Code, e.Explanation, e.Rule, e.Source)
}

// Unwrap reports BOTH sentinels, for the same reason ResidualNotARoundOffError
// does: ErrUnknownStateCode for a caller that wants this condition alone, and
// ErrMissingParticular so the HTTP layer answers 422 (the request is fine, a
// master record is not) instead of 500.
func (e *UnknownStateCodeError) Unwrap() []error {
	return []error{ErrUnknownStateCode, ErrMissingParticular}
}

// ErrResidualNotARoundOff is the sentinel behind ResidualNotARoundOffError.
var ErrResidualNotARoundOff = errors.New(
	"printingdb: the difference between the party's leg and the taxable value plus tax is too large to be a rounding")

// roundOffBound is the largest residual that can honestly be LABELLED a
// round-off: one rupee.
//
// A rounding adjusts a figure to the nearest rupee, so it lives in the paise and
// can reach one rupee but not exceed it. The bound is deliberately inclusive —
// exactly 1.00 prints — because a voucher that rounded 1,739.51 up to 1,740.51 is
// still doing arithmetic on paise, and refusing it would refuse ordinary books.
//
// THE NUMBER IS PINNED BY A TEST AND NOT ONLY BY THIS COMMENT.
// TestTheRoundOffBoundIsWhateverTheCodeHappensToSay drives a residual of exactly
// 1.00 (which must print) and one of 1.01 (which must be refused), so raising the
// bound to 100 — which for one wave broke no test at all — now fails with the
// consequence stated: a hundred rupees of unexplained value would go out on a tax
// invoice under the label "Round Off", the one label a reader checks by glancing
// at it instead of adding it up.
var roundOffBound = decimal.NewFromInt(1)

// ResidualNotARoundOffError refuses an invoice whose figures do not close, rather
// than printing the difference under a label that hides it.
//
// WHY THIS IS A REFUSAL AND NOT A ROUND-OFF LINE. inv.RoundOff is computed as
// Total - Subtotal - sum(taxes), which makes the document add up BY
// CONSTRUCTION — that is the right design and it is what stops the printed total
// from ever disagreeing with the receivable. But the renderer labels the residual
// "Round Off", and a label is a claim: Rs 500 of freight keyed as a party leg
// with no line behind it, or a mis-keyed taxable value, would print as a
// ROUNDING. That is the one label that stops a reader noticing, because a reader
// checks a round-off by glancing at it, not by adding it up. So a residual that
// cannot be a rounding is refused, and the refusal names ALL THREE figures the
// person fixing it has to reconcile: the taxable value, the tax charged, and what
// was actually posted to the party's account.
//
// Rule 46(j) requires the TOTAL VALUE of the supply and Rule 46(k) the TAXABLE
// VALUE; a document on which those two differ by an unexplained Rs 500 states one
// of them wrongly, and neither party can tell which.
type ResidualNotARoundOffError struct {
	// Kind and Number identify the document that was refused.
	Kind   printing.DocKind
	Number string
	// TaxableValue is what the books hold as the base (Rule 46(k)).
	TaxableValue decimal.Decimal
	// TaxTotal is the sum of the tax heads the document would state
	// (Rule 46(l)/(m)).
	TaxTotal decimal.Decimal
	// PartyLeg is what was posted to the party's account: the amount the customer
	// is actually being asked to pay (Rule 46(j)).
	PartyLeg decimal.Decimal
	// Residual is PartyLeg - TaxableValue - TaxTotal, signed, so the direction is
	// legible: positive means the party was charged MORE than the invoice explains.
	Residual decimal.Decimal
	// Bound is the largest residual that could have been a rounding.
	Bound decimal.Decimal
	// Rule cites what makes the two figures mandatory and therefore what makes
	// their disagreement a defect.
	Rule string
}

func (e *ResidualNotARoundOffError) Error() string {
	return fmt.Sprintf(
		"printingdb: cannot print %s %s: the taxable value is %s and the tax charged is %s, "+
			"which come to %s, but %s was posted to the party's account — a residual of %s. "+
			"A round-off cannot exceed %s, so this would print %s of unexplained value on the "+
			"face of the document under the label \"Round Off\" (%s)",
		e.Kind, e.Number,
		e.TaxableValue.StringFixed(2), e.TaxTotal.StringFixed(2),
		e.TaxableValue.Add(e.TaxTotal).StringFixed(2),
		e.PartyLeg.StringFixed(2), e.Residual.StringFixed(2),
		e.Bound.StringFixed(2), e.Residual.Abs().StringFixed(2), e.Rule)
}

// Unwrap reports BOTH sentinels.
//
// ErrResidualNotARoundOff is the specific one, for a caller that wants to branch
// on this condition alone. ErrMissingParticular is the general one, and it is
// claimed deliberately: the HTTP layer maps it to 422 ("the request was well
// formed and the BOOKS cannot supply what the paper must state"), which is
// exactly the right status here — the request is fine, the voucher is not, and an
// operator fixes it. Without it this refusal would reach a client as a 500, i.e.
// as our bug rather than as their voucher's.
func (e *ResidualNotARoundOffError) Unwrap() []error {
	return []error{ErrResidualNotARoundOff, ErrMissingParticular}
}

// UnmodelledInvoiceParticulars is the HONEST LIST of Rule 46 particulars that
// this schema has NO COLUMN FOR, as of migration 0039. It is exported, and it is
// a function rather than a comment, so that the gap is greppable, assertable and
// surfaceable in a UI rather than being rediscovered by a business that has
// already sent the invoice.
//
// THE NAME OF THE STATE IS NO LONGER ON THIS LIST EITHER, and it left the same
// way the address did. It was declared here for two waves on the grounds that
// "no table or map anywhere in this codebase turns a code into a name" — which
// was true, and which refused every inter-State tax invoice (Rule 46(n)) and
// every supply of fifty thousand rupees or more to an unregistered person (Rule
// 46(e)) with nothing an operator could type in, because the name was never data
// anybody keys: it is a property of the code. internal/gststate is that map, so
// the two particulars are now MODELLED, LoadInvoice fills them, and a code the
// table will not name is refused by UnknownStateCodeError with the code named —
// a master to correct, not a migration to wait for.
//
// WHAT IS DELIBERATELY NOT PUT BACK ON THIS LIST IN ITS PLACE. gststate refuses
// four kinds of code (retired, unverified, malformed, absent). None of those is a
// SCHEMA gap: the column exists and holds a value, and the fix is to correct the
// master (or, for codes 96/99, to verify them against a published list and extend
// gststate). Declaring them here would tell an operator to wait for a migration
// that would change nothing.
//
// THE RECIPIENT'S ADDRESS IS NO LONGER ON THIS LIST. It was the first entry for
// one wave, and it made the whole track undeliverable: the party master had no
// address column, so ValidateRule46 refused every tax invoice this system could
// assemble. Migration 0039 added ledger.address/city/pincode, LoadInvoice now
// reads them, and an EMPTY address is therefore a data-entry fault with a fix an
// operator can apply today — so it moved from this list (a migration is needed)
// to validateInvoice (somebody must type it in). That distinction is the whole
// point of MissingParticularError.NotModelled and moving an entry across it is
// what "closing a gap" looks like here.
//
// WHY THESE ARE NOT REFUSALS. Every entry below is missing for EVERY company and
// EVERY voucher, because there is nowhere to put it. Refusing on them would mean
// this system cannot print a single tax invoice to a registered customer until a
// migration lands — which is not a refusal, it is an outage. So LoadInvoice
// refuses only on particulars that ARE modelled and happen to be empty (those
// have a fix an operator can apply today), leaves these fields EMPTY rather than
// fabricating anything, and declares them here.
//
// The renderer is the right place to act on this list: it holds the layout and
// can print a document that is visibly incomplete rather than one that looks
// complete and is not. See the handoff in this slice's report.
func UnmodelledInvoiceParticulars() []*MissingParticularError {
	return []*MissingParticularError{
		{
			Kind:        printing.KindTaxInvoice,
			Field:       "address of delivery (ship-to), where it differs from the place of supply",
			Source:      "nothing in the schema records a per-voucher delivery address",
			Rule:        "CGST Rules 2017, Rule 46(o)",
			NotModelled: true,
		},
		{
			Kind:        printing.KindTaxInvoice,
			Field:       "e-invoice acknowledgement number",
			Source:      "voucher_external_reference stores the IRN (kind = 'IRN') and has no column for the NIC acknowledgement number or its timestamp",
			Rule:        "CGST Rules 2017, Rule 48(4)",
			NotModelled: true,
		},
		{
			Kind:        printing.KindTaxInvoice,
			Field:       "cess rate and amount",
			Source:      "voucher_gst_line holds cgst, sgst and igst; there is no cess column (gst_inward_document has one, but that is the SUPPLIER's document)",
			Rule:        "CGST Rules 2017, Rule 46(l) and 46(m) — rate and amount of tax per head, cess included",
			NotModelled: true,
		},
	}
}

// ---------------------------------------------------------------------------
// class -> document kind
// ---------------------------------------------------------------------------

// The ledger voucher classes, spelled as internal/ledger's Class constants
// spell them (and as the hashed payload's "class" key stores them). Written out
// here rather than imported so that printingdb does not depend on
// internal/ledger: the coupling would be one-way today and circular the moment
// the ledger wanted to know a document had been printed.
const (
	classSales         = "Sales"
	classPurchase      = "Purchase"
	classReceipt       = "Receipt"
	classPayment       = "Payment"
	classContra        = "Contra"
	classJournal       = "Journal"
	classCreditNote    = "Credit Note"
	classDebitNote     = "Debit Note"
	classPayroll       = "Payroll"
	classDeliveryNote  = "Delivery Note"
	classReceiptNote   = "Receipt Note"
	classStockJournal  = "Stock Journal"
	classPhysicalStock = "Physical Stock"
)

// docKindForClass maps a stored voucher class to the document it prints as, and
// says which loader produces it.
//
// The classes that print NOTHING are listed explicitly rather than falling into
// a default, because "no document" is a DECISION about each one and the reason
// differs:
//
//	Purchase       — the SUPPLIER issues that invoice. Printing our purchase
//	                 voucher as a tax invoice would be this company issuing a
//	                 document it has no authority to issue. (The document we DO
//	                 issue against a supplier is a Debit Note, which prints.)
//	Contra/Journal — internal transfers and adjustments; no party receives paper.
//	Payroll        — a pay run's document is a PAYSLIP, per employee, which is a
//	                 payroll-report concern and not one voucher's document.
//	Receipt Note   — the inward counterpart of a Delivery Note: the goods arrived
//	                 with the SUPPLIER's challan, so we receive paper, not issue it.
//	Stock Journal / Physical Stock — a transfer between our own godowns and a
//	                 stock count. Nothing leaves the business and nobody is handed
//	                 anything.
func docKindForClass(class string) (kind printing.DocKind, isInvoice bool, err error) {
	switch class {
	case classSales:
		// Refined to KindBillOfSupply below when the voucher charges no tax
		// (Rule 49 governs instead of Rule 46).
		return printing.KindTaxInvoice, true, nil
	case classCreditNote:
		return printing.KindCreditNote, false, nil
	case classDebitNote:
		return printing.KindDebitNote, false, nil
	case classReceipt:
		return printing.KindReceipt, false, nil
	case classPayment:
		return printing.KindPayment, false, nil
	case classDeliveryNote:
		return printing.KindDeliveryNote, false, nil
	case classPurchase, classContra, classJournal, classPayroll,
		classReceiptNote, classStockJournal, classPhysicalStock:
		return "", false, fmt.Errorf("%w: %q", ErrNoDocumentForClass, class)
	case "":
		return "", false, fmt.Errorf("%w: the stored version records no class", ErrNoDocumentForClass)
	default:
		return "", false, fmt.Errorf("%w: %q", ErrNoDocumentForClass, class)
	}
}

// ---------------------------------------------------------------------------
// VoucherClass
// ---------------------------------------------------------------------------

// VoucherClass returns the class recorded on the voucher's CURRENT version.
//
// WHY THE HTTP LAYER NEEDS THIS. The print route is
// /print/vouchers/{class}/{voucherID}, and {class} is the ACL resource token:
// authz resolves `print sales` from the path segment, never from the row. So a
// caller permitted to print `receipt` but disallowed `sales` could ask for
// .../print/vouchers/receipt/<a sales voucher's id> and, without this check, be
// handed the sales invoice its level was explicitly refused. The path class must
// be compared against the STORED class and the request refused on a mismatch —
// this function is the stored half of that comparison.
//
// It reads the class from the hashed payload (payload->>'class'), the same
// source ledger.GetVoucher projects, so what is authorised against is what was
// signed rather than a denormalised column that could have been written wrong.
func (s *Store) VoucherClass(ctx context.Context, companyID, voucherID uuid.UUID) (string, error) {
	var class string
	err := s.inTenantTx(ctx, companyID, true, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(vv.payload->>'class', '')
			   FROM voucher_head vh
			   JOIN voucher_version vv
			     ON vv.company_id = vh.company_id AND vv.id = vh.current_version_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2 AND vh.is_deleted = false`,
			companyID, voucherID).Scan(&class)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrVoucherNotFound, voucherID)
		}
		if err != nil {
			return fmt.Errorf("printingdb: read voucher class: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return class, nil
}

// ---------------------------------------------------------------------------
// the shared read
// ---------------------------------------------------------------------------

// headRow is the current version of one logical voucher, as stored.
type headRow struct {
	versionID     uuid.UUID
	class         string
	voucherNo     string
	date          *time.Time
	partyLedgerID uuid.UUID
	hasParty      bool
	partyName     string
	narration     string
	placeOfSupply string
	reverseCharge bool
	hasGSTTotals  bool
	taxable       decimal.Decimal
	cgst          decimal.Decimal
	sgst          decimal.Decimal
	igst          decimal.Decimal
	supplierDocNo string
	supplierDate  *time.Time
}

// loadHead reads the CURRENT version of a live voucher.
//
// Scoped by company_id on BOTH tables and joined on (company_id, id) rather than
// on id alone. RLS already pins every row to app_current_company_id(), so the
// predicate is belt AND braces — and the composite join is the braces: a version
// id from another tenant cannot be reached even by a query that forgot the
// tenant predicate on the child.
func loadHead(ctx context.Context, tx pgx.Tx, companyID, voucherID uuid.UUID) (headRow, error) {
	var (
		h       headRow
		partyID *uuid.UUID
	)
	err := tx.QueryRow(ctx,
		`SELECT vv.id,
		        COALESCE(vv.payload->>'class', ''),
		        COALESCE(vv.voucher_no, ''),
		        vv.voucher_date,
		        vv.party_ledger_id,
		        COALESCE(vv.payload->>'party', ''),
		        COALESCE(vv.payload->>'narration', ''),
		        COALESCE(vv.place_of_supply, ''),
		        vv.is_reverse_charge,
		        vv.taxable_value IS NOT NULL,
		        COALESCE(vv.taxable_value, 0),
		        COALESCE(vv.cgst, 0), COALESCE(vv.sgst, 0), COALESCE(vv.igst, 0),
		        COALESCE(vv.supplier_invoice_no, ''),
		        vv.supplier_invoice_date
		   FROM voucher_head vh
		   JOIN voucher_version vv
		     ON vv.company_id = vh.company_id AND vv.id = vh.current_version_id
		  WHERE vh.company_id = $1 AND vh.voucher_id = $2 AND vh.is_deleted = false`,
		companyID, voucherID).Scan(
		&h.versionID, &h.class, &h.voucherNo, &h.date, &partyID, &h.partyName,
		&h.narration, &h.placeOfSupply, &h.reverseCharge,
		&h.hasGSTTotals, &h.taxable, &h.cgst, &h.sgst, &h.igst,
		&h.supplierDocNo, &h.supplierDate)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return headRow{}, fmt.Errorf("%w: %s", ErrVoucherNotFound, voucherID)
	case err != nil:
		return headRow{}, fmt.Errorf("printingdb: read voucher head: %w", err)
	}
	if partyID != nil {
		h.partyLedgerID, h.hasParty = *partyID, true
	}
	return h, nil
}

// loadSupplier reads the company block: who is issuing the document.
//
// Name follows migration 0015's rule that mailing_name is what PRINTS and an
// empty mailing_name means "use name" — a trading name may legitimately differ
// from the registered one, and the invoice carries the trading name.
//
// State is the NAME of the company's home State, from internal/gststate, and it
// is filled BEST-EFFORT: a code gststate will not name leaves the letterhead's
// State line exactly as it was before that package existed, and nothing is
// refused. That asymmetry with the place of supply and the recipient's State is
// deliberate and it follows the statute — Rule 46(a) asks for the supplier's
// name, address and GSTIN and not for the name of their State, while 46(n) and
// 46(e) ask for a State BY NAME. Refusing a company's whole print run over a
// letterhead line the rule does not require would be this file inventing a
// mandatory particular.
//
// PAN, Phone and Email stay empty because the company table has none of them
// (ledger.pan is the PARTY's PAN, not ours).
func loadSupplier(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (printing.Company, error) {
	var (
		c                                                       printing.Company
		name, mailing, address, city, pincode, stateCode, gstin string
	)
	err := tx.QueryRow(ctx,
		`SELECT name, mailing_name, address, city, pincode, state_code, gstin
		   FROM company WHERE company_id = $1 AND id = $1`,
		companyID).Scan(&name, &mailing, &address, &city, &pincode, &stateCode, &gstin)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Under RLS this is what a wrong or absent tenant GUC looks like, so it
		// is reported as the tenancy failure it is rather than as an empty
		// letterhead.
		return printing.Company{}, fmt.Errorf(
			"printingdb: company %s is not visible in this transaction (tenant scope)", companyID)
	case err != nil:
		return printing.Company{}, fmt.Errorf("printingdb: read company: %w", err)
	}
	c.Name = name
	if mailing != "" {
		c.Name = mailing
	}
	c.Address1, c.Address2 = splitAddress(address)
	c.City, c.PIN, c.StateCode, c.GSTIN = city, pincode, stateCode, gstin
	c.State, _ = gststate.Name(stateCode)
	return c, nil
}

// splitAddress turns migration 0015's newline-separated address BLOCK into the
// two address lines printing.Company carries.
//
// Tally treats an address as multi-line free text and so does the column, so
// there is nothing to parse: line one is Address1 and everything after it is
// joined into Address2 with ", ". Blank lines are dropped (they are keystrokes,
// not content, and an empty printed line looks like a rendering fault).
func splitAddress(block string) (line1, line2 string) {
	var lines []string
	for _, l := range strings.Split(block, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			lines = append(lines, t)
		}
	}
	if len(lines) == 0 {
		return "", ""
	}
	return lines[0], strings.Join(lines[1:], ", ")
}

// partyRow is a party master as the print path needs it.
//
// address / city / pin are migration 0039's columns, added for exactly this
// purpose: before them the party master had NO address at all, so Rule 46(d)
// could not be satisfied and no tax invoice could be printed to anybody. The
// block is newline-separated free text, spelled the same way as the company
// block (migration 0015), which is why splitAddress serves both sides.
type partyRow struct {
	name             string
	gstin            string
	stateCode        string
	registrationType string
	address          string
	city             string
	pin              string
	// noMaster records that there is NO ledger row behind this party at all —
	// only the version's denormalised label. It changes what a refusal can
	// honestly tell an operator to go and fix: an empty column on a master is a
	// keystroke, and a party that was never made a master is a different job.
	noMaster bool
}

// registered reports whether the master SAYS this party is registered under GST,
// and therefore whether a GSTIN is mandatory on a document naming them
// (Rule 46(d): "name, address and GSTIN or UIN, IF REGISTERED, of the
// recipient").
//
// UNKNOWN counts as registered only when a GSTIN is actually present. That
// asymmetry is deliberate: most ledgers are created without anybody choosing a
// registration type, so treating UNKNOWN as registered would refuse to print for
// every company that has not curated its masters, while treating an UNKNOWN
// party that HAS a GSTIN as unregistered would drop a number that is right there
// in the books off a B2B invoice.
func (p partyRow) registered() bool {
	switch p.registrationType {
	case "REGULAR", "COMPOSITION", "SEZ":
		return true
	case "UNKNOWN":
		return p.gstin != ""
	default: // UNREGISTERED, CONSUMER
		return false
	}
}

func loadParty(ctx context.Context, tx pgx.Tx, companyID, ledgerID uuid.UUID) (partyRow, error) {
	var p partyRow
	err := tx.QueryRow(ctx,
		`SELECT name, COALESCE(gstin, ''), COALESCE(state_code, ''), registration_type::text,
		        COALESCE(address, ''), COALESCE(city, ''), COALESCE(pincode, '')
		   FROM ledger WHERE company_id = $1 AND id = $2`,
		companyID, ledgerID).Scan(&p.name, &p.gstin, &p.stateCode, &p.registrationType,
		&p.address, &p.city, &p.pin)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return partyRow{}, fmt.Errorf(
			"printingdb: party ledger %s is not visible in this company", ledgerID)
	case err != nil:
		return partyRow{}, fmt.Errorf("printingdb: read party ledger: %w", err)
	}
	return p, nil
}

// partyBlock turns a party master into the RECIPIENT block a document names.
//
// It is one function, used by both loaders, because a credit note and an invoice
// address the same party the same way — and because the recipient's address was
// missing from the invoice for a whole wave precisely BECAUSE the two loaders
// each built printing.Party by hand from three fields.
//
// State — the NAME of the State — is filled from ledger.state_code through
// internal/gststate. It was EMPTY for two waves, on the true grounds that nothing
// in this codebase mapped a code to a name, and the cost was concrete: Rule 46(e)
// requires an unregistered recipient's State by name on a supply of fifty
// thousand rupees or more, printing.ValidateRule46 enforces it, so no such
// invoice could be printed at all and the operator had no field to fill in.
//
// A CODE gststate WILL NOT NAME LEAVES THIS FIELD EMPTY RATHER THAN GUESSING, and
// on an invoice that is then REFUSED by name: see stateNameFault, which the
// invoice path calls for exactly this. The blank is never printed as a name and
// the code is never printed in the name's place.
func partyBlock(p partyRow) printing.Party {
	line1, line2 := splitAddress(p.address)
	state, _ := gststate.Name(p.stateCode)
	return printing.Party{
		Name:      p.name,
		Address1:  line1,
		Address2:  line2,
		City:      p.city,
		State:     state,
		PIN:       p.pin,
		StateCode: p.stateCode,
		GSTIN:     p.gstin,
	}
}

// stateNameFault returns the refusal for a state code that is PRESENT in the
// books and that internal/gststate will not name, and nil for a code it names or
// for no code at all.
//
// An EMPTY code is not this function's business: an absent place of supply is
// already refused by validateInvoice (Rule 46(n) names the code as much as the
// name), and an absent recipient state code is refused by
// printing.ValidateRule46 only on the high-value unregistered supply where Rule
// 46(e) makes it mandatory. What this function catches is the case neither of
// those can see — a code that is there, that looks like data, and that denotes no
// State: 25 since the 2020 merger, 28 since the Andhra Pradesh bifurcation, 96
// and 99 (which could not be verified from this environment), and anything
// mistyped.
func stateNameFault(kind printing.DocKind, field, code, source, rule string) error {
	if strings.TrimSpace(code) == "" {
		return nil
	}
	if _, ok := gststate.Name(code); ok {
		return nil
	}
	return &UnknownStateCodeError{
		Kind:        kind,
		Field:       field,
		Code:        code,
		Source:      source,
		Rule:        rule,
		Explanation: gststate.Explain(code),
	}
}

// gstLine is one row of voucher_gst_line: the per-line taxable base, its rate,
// and the tax derived on it.
type gstLine struct {
	lineNo  int
	hsn     string
	taxable decimal.Decimal
	rate    decimal.Decimal
	cgst    decimal.Decimal
	sgst    decimal.Decimal
	igst    decimal.Decimal
}

func loadGSTLines(ctx context.Context, tx pgx.Tx, companyID, versionID uuid.UUID) ([]gstLine, error) {
	rows, err := tx.Query(ctx,
		`SELECT line_no, hsn, taxable_value, gst_rate_pct, cgst, sgst, igst
		   FROM voucher_gst_line
		  WHERE company_id = $1 AND voucher_version_id = $2
		  ORDER BY line_no`,
		companyID, versionID)
	if err != nil {
		return nil, fmt.Errorf("printingdb: query gst lines: %w", err)
	}
	defer rows.Close()
	var out []gstLine
	for rows.Next() {
		var g gstLine
		if err := rows.Scan(&g.lineNo, &g.hsn, &g.taxable, &g.rate, &g.cgst, &g.sgst, &g.igst); err != nil {
			return nil, fmt.Errorf("printingdb: scan gst line: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("printingdb: iterate gst lines: %w", err)
	}
	return out, nil
}

// movementRow is one stock line of the version, with the masters resolved.
type movementRow struct {
	lineNo      int
	item        string
	hsn         string // the ITEM master's HSN; the voucher's own line wins when it has one
	unit        string
	qty         decimal.Decimal
	rate        decimal.Decimal
	discountPct decimal.Decimal
	amount      decimal.Decimal
}

func loadMovements(ctx context.Context, tx pgx.Tx, companyID, versionID uuid.UUID) ([]movementRow, error) {
	rows, err := tx.Query(ctx,
		`SELECT sm.line_no, COALESCE(si.name, ''), COALESCE(si.hsn, ''),
		        COALESCE(u.symbol, ''),
		        sm.qty, sm.rate, sm.discount_pct, sm.amount
		   FROM stock_movement sm
		   LEFT JOIN stock_item si
		          ON si.company_id = sm.company_id AND si.id = sm.stock_item_id
		   LEFT JOIN unit u
		          ON u.company_id = si.company_id AND u.id = si.unit_id
		  WHERE sm.company_id = $1 AND sm.voucher_version_id = $2
		  ORDER BY sm.line_no`,
		companyID, versionID)
	if err != nil {
		return nil, fmt.Errorf("printingdb: query stock movements: %w", err)
	}
	defer rows.Close()
	var out []movementRow
	for rows.Next() {
		var m movementRow
		if err := rows.Scan(&m.lineNo, &m.item, &m.hsn, &m.unit,
			&m.qty, &m.rate, &m.discountPct, &m.amount); err != nil {
			return nil, fmt.Errorf("printingdb: scan stock movement: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("printingdb: iterate stock movements: %w", err)
	}
	return out, nil
}

// legRow is one posting, with the ledger and its group resolved.
type legRow struct {
	ledgerID uuid.UUID
	ledger   string
	group    string
	isDebit  bool
	amount   decimal.Decimal
}

// dutiesAndTaxesGroup is the seeded group every tax ledger hangs under
// (migration 0005's seed_company_defaults). It is how this file tells a TAX leg
// from a REVENUE leg without being told which ledger ids were used as tax heads:
// GSTParams.CGSTLedgerID and friends are inputs to the post and are NOT
// persisted anywhere, so the group is the only stored evidence of what a leg is.
const dutiesAndTaxesGroup = "Duties & Taxes"

func loadLegs(ctx context.Context, tx pgx.Tx, companyID, versionID uuid.UUID) ([]legRow, error) {
	// ORDER BY dr_cr DESC ('D' before 'C'), then descending amount, then name.
	//
	// A posting carries NO line number — the table has none — so there is no
	// keyed order to recover. This is therefore a canonical PRESENTATION order,
	// chosen to be total (the name breaks every remaining tie) so that printing
	// the same voucher twice produces byte-identical output. It is the same
	// ordering ledger.GetVoucher projects, plus the tie-break.
	rows, err := tx.Query(ctx,
		`SELECT p.ledger_id, COALESCE(l.name, ''), COALESCE(g.name, ''),
		        p.dr_cr::text, p.amount
		   FROM posting p
		   LEFT JOIN ledger l        ON l.company_id = p.company_id AND l.id = p.ledger_id
		   LEFT JOIN account_group g ON g.company_id = l.company_id AND g.id = l.group_id
		  WHERE p.company_id = $1 AND p.voucher_version_id = $2
		  ORDER BY p.dr_cr DESC, p.amount DESC, COALESCE(l.name, '')`,
		companyID, versionID)
	if err != nil {
		return nil, fmt.Errorf("printingdb: query postings: %w", err)
	}
	defer rows.Close()
	var out []legRow
	for rows.Next() {
		var (
			l    legRow
			side string
		)
		if err := rows.Scan(&l.ledgerID, &l.ledger, &l.group, &side, &l.amount); err != nil {
			return nil, fmt.Errorf("printingdb: scan posting: %w", err)
		}
		l.isDebit = side == "D"
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("printingdb: iterate postings: %w", err)
	}
	return out, nil
}

// billRow is one bill-wise allocation: the bill a leg opens or settles.
type billRow struct {
	ledgerID uuid.UUID
	refName  string
	refType  string
	dueDate  *time.Time
}

func loadBills(ctx context.Context, tx pgx.Tx, companyID, versionID uuid.UUID) ([]billRow, error) {
	rows, err := tx.Query(ctx,
		`SELECT ledger_id, ref_name, ref_type::text, due_date
		   FROM bill_allocation
		  WHERE company_id = $1 AND voucher_version_id = $2
		  ORDER BY ref_name`,
		companyID, versionID)
	if err != nil {
		return nil, fmt.Errorf("printingdb: query bill allocations: %w", err)
	}
	defer rows.Close()
	var out []billRow
	for rows.Next() {
		var b billRow
		if err := rows.Scan(&b.ledgerID, &b.refName, &b.refType, &b.dueDate); err != nil {
			return nil, fmt.Errorf("printingdb: scan bill allocation: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("printingdb: iterate bill allocations: %w", err)
	}
	return out, nil
}

// openerRow is the IDENTITY of the voucher that OPENED a bill reference — the
// document a credit or debit note adjusts.
//
// It carries BOTH identities that voucher has, because the two notes need
// different ones and neither may be substituted for the other:
//
//   - number/date are OUR document: the voucher number this company issued and
//     the date it issued it. That is what a CREDIT note must name, because the
//     invoice it adjusts is ours and the recipient is holding a copy of it.
//   - supplierNo/supplierDate are the SUPPLIER'S document, recorded on the
//     purchase from their own invoice (migration 0032). That is what a DEBIT
//     note must name. ⛔ Our purchase number must NEVER be printed there: the
//     supplier has never seen it, so a note citing it names a document that does
//     not exist on the side that has to act on it.
type openerRow struct {
	number       string
	date         *time.Time
	supplierNo   string
	supplierDate *time.Time
}

// billOpener finds the voucher that OPENED a bill reference, given the reference
// a note is allocated AGAINST.
//
// WHY A LOOKUP AND NOT A DERIVATION. Rule 53(1A) requires a note to carry the
// number AND the date of the document it adjusts, and a serial number repeats
// every financial year — "INV/0001" names one invoice per year, not one invoice.
// The date is therefore load-bearing, and the only honest source for both is the
// voucher that ISSUED that bill. Taking the note's own date, or today's, would
// print a date on which no invoice was ever issued.
//
// ⛔ AND IT RETURNS THE NUMBER, NOT ONLY THE DATE — WHICH IS THE DEFECT THIS
// SIGNATURE EXISTS TO CLOSE. The callers used to print the bill REFERENCE as the
// invoice number. A reference is not a number: an operator may name a bill
// anything they like ("PO-4471/partial"), and since bill names were made unique
// an UNNAMED bill is opened as `Sales/2026-27/7` — an internal name, on the one
// line of the page where the recipient looks for the invoice they are holding.
// The reference is what LOCATES the document; the document says what it is
// called.
//
// TWO PLACES ARE TRIED, IN THIS ORDER, because a bill reference is not always the
// voucher number:
//
//  1. the bill_allocation row that OPENED the reference (ref_type = 'NEW') on the
//     same party ledger. That IS the bill the note is applied to, so its
//     voucher's identity is the right one even when the operator named the bill
//     something of their own.
//  2. failing that, a voucher whose NUMBER is the reference — which is what a
//     note names when the invoice was billed without bill-wise tracking on.
//
// Both are scoped by company_id and joined through voucher_head.current_version_id
// with is_deleted = false, so a superseded version cannot supply the date and a
// CANCELLED invoice cannot either: a note that adjusts a deleted invoice must show
// no date and be refused, not quietly cite a tombstone.
//
// nil, nil is a legitimate answer (the reference matches nothing on file). The
// caller then falls back to the reference itself with no date, and the refusal
// comes from printing.validateVoucherDoc, which names the particular.
func billOpener(ctx context.Context, tx pgx.Tx, companyID, ledgerID uuid.UUID, refName string) (*openerRow, error) {
	if strings.TrimSpace(refName) == "" {
		return nil, nil
	}

	var o openerRow
	if ledgerID != uuid.Nil {
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(vv.voucher_no, ''), vv.voucher_date,
			        COALESCE(vv.supplier_invoice_no, ''), vv.supplier_invoice_date
			   FROM bill_allocation ba
			   JOIN voucher_head vh
			     ON vh.company_id = ba.company_id AND vh.current_version_id = ba.voucher_version_id
			   JOIN voucher_version vv
			     ON vv.company_id = ba.company_id AND vv.id = ba.voucher_version_id
			  WHERE ba.company_id = $1 AND ba.ledger_id = $2 AND ba.ref_name = $3
			    AND ba.ref_type = 'NEW' AND vh.is_deleted = false
			  ORDER BY vv.voucher_date
			  LIMIT 1`,
			companyID, ledgerID, refName).Scan(
			&o.number, &o.date, &o.supplierNo, &o.supplierDate)
		switch {
		case err == nil:
			return &o, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("printingdb: read the bill %q: %w", refName, err)
		}
	}

	err := tx.QueryRow(ctx,
		`SELECT COALESCE(vv.voucher_no, ''), vv.voucher_date,
		        COALESCE(vv.supplier_invoice_no, ''), vv.supplier_invoice_date
		   FROM voucher_version vv
		   JOIN voucher_head vh
		     ON vh.company_id = vv.company_id AND vh.current_version_id = vv.id
		  WHERE vv.company_id = $1 AND vv.voucher_no = $2 AND vh.is_deleted = false
		  ORDER BY vv.voucher_date
		  LIMIT 1`,
		companyID, refName).Scan(
		&o.number, &o.date, &o.supplierNo, &o.supplierDate)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("printingdb: read the invoice numbered %q: %w", refName, err)
	}
	return &o, nil
}

// loadIRN returns the e-invoice reference registered against this voucher, or ""
// when the invoice was never registered (which is the ordinary case for a
// taxpayer below the Rule 48(4) turnover band).
//
// voucher_external_reference is keyed on (company_id, voucher_id, kind,
// ref_value) and is where statutoryd's freeze hook records the IRN. Only the
// LATEST is taken: the unique key permits more than one row per kind, and an
// invoice that was somehow registered twice must print ONE number, not two.
func loadIRN(ctx context.Context, tx pgx.Tx, companyID, voucherID uuid.UUID) (string, error) {
	var irn string
	err := tx.QueryRow(ctx,
		`SELECT ref_value FROM voucher_external_reference
		  WHERE company_id = $1 AND voucher_id = $2 AND kind = 'IRN'
		  ORDER BY created_at DESC, ref_value DESC
		  LIMIT 1`,
		companyID, voucherID).Scan(&irn)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("printingdb: read e-invoice reference: %w", err)
	}
	return irn, nil
}

// ---------------------------------------------------------------------------
// LoadInvoice
// ---------------------------------------------------------------------------

// LoadInvoice assembles the tax invoice (or bill of supply) for one stored Sales
// voucher.
//
// It refuses rather than returning a document with a blank where a mandatory
// particular belongs — see MissingParticularError, and see
// UnmodelledInvoiceParticulars for the particulars this schema cannot supply for
// ANY voucher (those are declared, not refused, because refusing on them would
// mean no company could print at all).
//
// The whole read runs in ONE read-only, tenant-scoped transaction, so every
// figure on the document comes from the SAME snapshot: a concurrent alter cannot
// land between the head read and the tax lines and produce an invoice whose
// total belongs to one version and whose lines belong to another.
func (s *Store) LoadInvoice(ctx context.Context, companyID, voucherID uuid.UUID) (printing.Invoice, error) {
	var (
		inv   printing.Invoice
		party partyRow
	)
	err := s.inTenantTx(ctx, companyID, true, func(ctx context.Context, tx pgx.Tx) error {
		h, err := loadHead(ctx, tx, companyID, voucherID)
		if err != nil {
			return err
		}
		kind, isInvoice, err := docKindForClass(h.class)
		if err != nil {
			return err
		}
		if !isInvoice {
			return fmt.Errorf("%w: %q prints as %s", ErrNotAnInvoice, h.class, kind)
		}

		gstLines, err := loadGSTLines(ctx, tx, companyID, h.versionID)
		if err != nil {
			return err
		}
		moves, err := loadMovements(ctx, tx, companyID, h.versionID)
		if err != nil {
			return err
		}
		legs, err := loadLegs(ctx, tx, companyID, h.versionID)
		if err != nil {
			return err
		}
		bills, err := loadBills(ctx, tx, companyID, h.versionID)
		if err != nil {
			return err
		}

		// A SALE THAT CHARGES NO TAX IS A BILL OF SUPPLY, NOT A TAX INVOICE.
		// Rule 49 governs it and the document may not state a rate or an amount
		// of tax — printing "CGST 0.00" on an exempt or composition supply
		// invites the recipient to claim credit that does not exist. The test is
		// the tax the books actually hold, per head, not a flag.
		taxCharged := !h.cgst.IsZero() || !h.sgst.IsZero() || !h.igst.IsZero()
		for _, g := range gstLines {
			if !g.cgst.IsZero() || !g.sgst.IsZero() || !g.igst.IsZero() {
				taxCharged = true
			}
		}
		if !taxCharged {
			kind = printing.KindBillOfSupply
		}

		inv.Kind = kind
		inv.Number = h.voucherNo
		if h.date != nil {
			inv.Date = *h.date
		}
		inv.ReverseCharge = h.reverseCharge
		inv.PlaceOfSupplyCode = h.placeOfSupply
		// PlaceOfSupply is the NAME of the State the code denotes (Rule 46(n):
		// the place of supply ALONG WITH THE NAME OF THE STATE). It comes from
		// internal/gststate, and it is what makes an inter-State invoice
		// printable at all: printing.ValidateRule46 refuses a supply carrying
		// IGST whose place of supply has no name, so before this line every IGST
		// sale in the product answered 4xx — including the one the demo seed
		// posts. A code gststate will not name leaves this EMPTY and is refused
		// below by name; the code is never printed where the name belongs.
		inv.PlaceOfSupply, _ = gststate.Name(h.placeOfSupply)

		if inv.Supplier, err = loadSupplier(ctx, tx, companyID); err != nil {
			return err
		}

		if h.hasParty {
			if party, err = loadParty(ctx, tx, companyID, h.partyLedgerID); err != nil {
				return err
			}
		} else {
			// The version's denormalised label is all there is. It is a name with
			// no master behind it, so there is no GSTIN and no address to state,
			// and the validation below will refuse a tax invoice for it.
			party = partyRow{name: h.partyName, registrationType: "UNKNOWN", noMaster: true}
		}
		inv.Recipient = partyBlock(party)
		// ShipTo stays nil: no delivery address is stored anywhere. Rule 46(o)
		// only requires it WHERE IT DIFFERS from the place of supply, and a
		// fabricated ship-to block would assert something the books do not know.

		inv.Items = invoiceItems(moves, gstLines, legs, h.partyLedgerID)

		if inv.Taxes, err = taxHeads(kind, gstLines); err != nil {
			return err
		}

		// Subtotal is the STORED taxable base. It falls back to the sum of the
		// line amounts only for a voucher that carries no GST derivation at all
		// (a bill of supply), where there is no taxable_value column value to
		// read and the lines ARE the base.
		if h.hasGSTTotals {
			inv.Subtotal = h.taxable
		} else {
			inv.Subtotal = sumLineAmounts(inv.Items)
		}

		// Total is what the customer OWES: the net of the party's own postings.
		// Not the sum of the lines plus tax — those two differ by any round-off
		// or expense leg the voucher carries, and the receivable is the figure
		// the customer will be chased for.
		partyTotal, hasPartyLeg := partyLegNet(legs, h.partyLedgerID, h.hasParty)
		switch {
		case hasPartyLeg:
			inv.Total = partyTotal
		default:
			inv.Total = inv.Subtotal.Add(sumTax(inv.Taxes))
		}
		inv.RoundOff = inv.Total.Sub(inv.Subtotal).Sub(sumTax(inv.Taxes))

		inv.DueDate = earliestDueDate(bills, h.partyLedgerID)

		if inv.IRN, err = loadIRN(ctx, tx, companyID, voucherID); err != nil {
			return err
		}
		// AckNo has no column. See UnmodelledInvoiceParticulars.

		return validateInvoice(&inv, party)
	})
	if err != nil {
		return printing.Invoice{}, err
	}

	// The overrides are read in their OWN transaction, after the amounts are
	// settled, and that is safe precisely because a template can only change the
	// letterhead, the declaration, the terms and the bank block — never a figure.
	// Nesting the read inside the document transaction would mean duplicating
	// LoadOverrides' query here, and two copies of a tenant-scoped SELECT is how
	// one of them ends up without the predicate.
	overrides, err := s.LoadOverrides(ctx, companyID, inv.Kind)
	if err != nil {
		return printing.Invoice{}, err
	}
	inv.Overrides = overrides
	return inv, nil
}

// invoiceItems builds the line block.
//
// THE THREE SHAPES OF INVOICE, AND WHY THE SOURCE DIFFERS:
//
//   - An ITEM INVOICE has stock movements. The GOODS are the lines: quantity,
//     unit and rate all exist, and migration 0027's deferred trigger guarantees
//     the movements' amounts sum to exactly the taxed base, so taking the line
//     amounts from the movements cannot make the document disagree with the tax.
//     The HSN comes from the voucher's own GST line where it has one and from the
//     item master otherwise — the voucher's line is what was FILED (it is the
//     source of the GSTR-1 table 12 summary), so the paper must agree with it.
//
//   - A TAXED SERVICE INVOICE has no movements but does have GST lines. Its lines
//     are those: a taxable value and a rate, with no quantity and no unit, which
//     is exactly what such an invoice states.
//
//   - AN INVOICE WITH NEITHER — a supply keyed as plain double entry, which is
//     every exempt sale and every bill of supply — has only its POSTINGS. Its
//     lines are therefore the revenue legs: the ledger the value was booked to
//     and the amount booked to it. Without this arm such an invoice would print a
//     subtotal of nothing against a real total, and the difference would silently
//     land in the round-off (which is exactly what
//     TestASaleThatChargedNoTaxPrintsAsATaxInvoice caught).
//
// The description of a GST line is the LEDGER the value was booked to
// ("Consultancy Income"), which is how Tally prints an accounting invoice — but
// only when the voucher has exactly ONE such leg. With two or more there is
// nothing in the schema tying a revenue leg to a GST line, and pairing them by
// position or by amount would be a guess that reads as fact on a customer's
// invoice. So the description is left empty rather than invented. (The
// postings-only arm has no such problem: there the leg IS the line.)
func invoiceItems(moves []movementRow, gstLines []gstLine, legs []legRow, partyLedgerID uuid.UUID) []printing.LineItem {
	if len(moves) > 0 {
		hsnByLine := make(map[int]string, len(gstLines))
		for _, g := range gstLines {
			hsnByLine[g.lineNo] = g.hsn
		}
		items := make([]printing.LineItem, 0, len(moves))
		for _, m := range moves {
			hsn := m.hsn
			if h, ok := hsnByLine[m.lineNo]; ok && h != "" {
				hsn = h
			}
			items = append(items, printing.LineItem{
				SerialNo:    m.lineNo,
				Description: m.item,
				HSN:         hsn,
				Quantity:    m.qty,
				Unit:        m.unit,
				Rate:        m.rate,
				// stock_movement.discount_pct is a PERCENTAGE (the column CHECKs
				// 0..100) and it crosses this seam into a field whose NAME says so.
				// It was called Discount for one wave and the renderer printed it
				// through FormatMoney: an invoice reading "Rate 100.00, Qty 10,
				// Less: Discount 2.50, Amount 975.00" — four figures that cannot
				// all be true, understating the discount by qty × rate ÷ 100.
				//
				// It is NOT converted to rupees here. The books hold the percentage
				// and the already-net Amount; qty × rate × pct ÷ 100 is a fifth
				// figure no posting holds, and deriving it in the print path is how
				// the paper starts disagreeing with the movement it came from.
				DiscountPct: m.discountPct,
				Amount:      m.amount,
			})
		}
		return items
	}

	if len(gstLines) > 0 {
		description := soleRevenueLedger(legs, partyLedgerID)
		items := make([]printing.LineItem, 0, len(gstLines))
		for _, g := range gstLines {
			items = append(items, printing.LineItem{
				SerialNo:    g.lineNo,
				Description: description,
				HSN:         g.hsn,
				Amount:      g.taxable,
			})
		}
		return items
	}

	var items []printing.LineItem
	for _, l := range legs {
		if l.ledgerID == partyLedgerID || l.group == dutiesAndTaxesGroup {
			continue
		}
		items = append(items, printing.LineItem{
			SerialNo:    len(items) + 1,
			Description: l.ledger,
			Amount:      l.amount,
		})
	}
	return items
}

// soleRevenueLedger returns the name of the one leg that is neither the party
// nor a tax head, or "" when there is not exactly one.
func soleRevenueLedger(legs []legRow, partyLedgerID uuid.UUID) string {
	var (
		found string
		count int
	)
	for _, l := range legs {
		if l.ledgerID == partyLedgerID || l.group == dutiesAndTaxesGroup {
			continue
		}
		found = l.ledger
		count++
	}
	if count == 1 {
		return found
	}
	return ""
}

// two is the divisor that splits a GST rate into its central and state halves.
var two = decimal.NewFromInt(2)

// taxHeads turns the stored per-line tax into the RATE-AND-AMOUNT-PER-HEAD block
// Rule 46(l) and 46(m) require.
//
// GROUPED BY RATE, because that is the grain the statute asks for and the grain a
// mixed-rate invoice actually has: one invoice may carry 18% steel articles and
// 12% kitchenware, and a single "CGST 2,700" line states neither rate correctly.
// Rates are emitted ascending so the same invoice prints byte-identically twice.
//
// CGST and SGST each carry HALF the line's rate (an 18% intra-state supply is
// CGST 9% + SGST 9%, section 9(1) CGST Act read with the rate notifications);
// IGST carries the whole rate. The halving is exact decimal division — the
// AMOUNTS are never derived here, only the rate label.
//
// A zero-amount head is omitted rather than printed as 0.00: an intra-state
// invoice has no IGST at all, and a line reading "IGST 0%  0.00" invites exactly
// the confusion about what was charged that the per-head requirement exists to
// prevent.
func taxHeads(kind printing.DocKind, lines []gstLine) ([]printing.TaxHead, error) {
	// Rule 49: a bill of supply states NO rate and NO amount of tax.
	if kind == printing.KindBillOfSupply {
		return nil, nil
	}

	if len(lines) == 0 {
		// The aggregate says tax was charged but no per-line detail was ever
		// recorded (a voucher posted before migration 0016 added
		// voucher_gst_line). The AMOUNTS are known and the RATES are not, and the
		// rate is a mandatory particular — so this refuses rather than printing a
		// tax amount at an unstated or back-solved rate.
		return nil, &MissingParticularError{
			Kind:  kind,
			Field: "rate of tax, per head",
			Source: "voucher_gst_line has no rows for this voucher version; " +
				"only the version's aggregate cgst/sgst/igst totals exist",
			Rule: "CGST Rules 2017, Rule 46(l) and 46(m)",
		}
	}

	type bucket struct {
		rate decimal.Decimal
		cgst decimal.Decimal
		sgst decimal.Decimal
		igst decimal.Decimal
	}
	var order []string
	buckets := map[string]*bucket{}
	for _, l := range lines {
		key := l.rate.String()
		b, ok := buckets[key]
		if !ok {
			b = &bucket{rate: l.rate, cgst: decimal.Zero, sgst: decimal.Zero, igst: decimal.Zero}
			buckets[key] = b
			order = append(order, key)
		}
		b.cgst = b.cgst.Add(l.cgst)
		b.sgst = b.sgst.Add(l.sgst)
		b.igst = b.igst.Add(l.igst)
	}
	sort.Slice(order, func(i, j int) bool {
		return buckets[order[i]].rate.LessThan(buckets[order[j]].rate)
	})

	var heads []printing.TaxHead
	for _, key := range order {
		b := buckets[key]
		half := b.rate.Div(two)
		if !b.cgst.IsZero() {
			heads = append(heads, printing.TaxHead{Name: "CGST", Rate: half, Amount: b.cgst})
		}
		if !b.sgst.IsZero() {
			heads = append(heads, printing.TaxHead{Name: "SGST", Rate: half, Amount: b.sgst})
		}
		if !b.igst.IsZero() {
			heads = append(heads, printing.TaxHead{Name: "IGST", Rate: b.rate, Amount: b.igst})
		}
	}
	// Cess is not stored per line (see UnmodelledInvoiceParticulars), so no cess
	// head is ever emitted. The head list therefore sums to the version's
	// cgst+sgst+igst aggregate exactly, which is what makes RoundOff a residual
	// rather than an absorber of missing tax.
	return heads, nil
}

func sumTax(heads []printing.TaxHead) decimal.Decimal {
	sum := decimal.Zero
	for _, h := range heads {
		sum = sum.Add(h.Amount)
	}
	return sum
}

func sumLineAmounts(items []printing.LineItem) decimal.Decimal {
	sum := decimal.Zero
	for _, i := range items {
		sum = sum.Add(i.Amount)
	}
	return sum
}

// partyLegNet returns the magnitude of the party's net posting on this voucher.
//
// Net of both sides, then taken as a MAGNITUDE: a document never prints a
// negative total, and the direction is carried by the document KIND (a credit
// note credits, an invoice charges). A voucher whose party leg nets to zero is
// treated as having no party leg, so the total falls back to base + tax rather
// than printing 0.00 on an invoice that plainly has value.
func partyLegNet(legs []legRow, partyLedgerID uuid.UUID, hasParty bool) (decimal.Decimal, bool) {
	if !hasParty {
		return decimal.Zero, false
	}
	net := decimal.Zero
	found := false
	for _, l := range legs {
		if l.ledgerID != partyLedgerID {
			continue
		}
		found = true
		if l.isDebit {
			net = net.Add(l.amount)
		} else {
			net = net.Sub(l.amount)
		}
	}
	if !found || net.IsZero() {
		return decimal.Zero, false
	}
	return net.Abs(), true
}

// earliestDueDate returns the earliest due date recorded against the party's
// bill-wise allocations, or nil when none carries one.
//
// It is read and never COMPUTED. ledger.credit_days is right there and deriving
// "date + credit days" from it would look identical on paper — but a due date
// the books do not hold is a date nobody agreed to, and it would be the date a
// customer is chased on.
func earliestDueDate(bills []billRow, partyLedgerID uuid.UUID) *time.Time {
	var earliest *time.Time
	for _, b := range bills {
		if b.ledgerID != partyLedgerID || b.dueDate == nil {
			continue
		}
		if earliest == nil || b.dueDate.Before(*earliest) {
			d := *b.dueDate
			earliest = &d
		}
	}
	return earliest
}

// validateInvoice refuses an invoice the books cannot state completely.
//
// ONE ORDER, MOST FUNDAMENTAL FIRST, and it stops at the first failure: an
// operator fixing a voucher wants the next thing to fix, not a wall of nine
// findings. The particulars checked here are exactly the ones this schema HAS a
// column for — the structural gaps are declared by
// UnmodelledInvoiceParticulars instead, because refusing on those would refuse
// every invoice this system could ever print.
func validateInvoice(inv *printing.Invoice, party partyRow) error {
	miss := func(field, source, rule string) error {
		return &MissingParticularError{Kind: inv.Kind, Field: field, Source: source, Rule: rule}
	}

	if inv.Number == "" {
		return miss("the invoice serial number",
			"voucher_version.voucher_no",
			"CGST Rules 2017, Rule 46(b) — a consecutive serial number")
	}
	if inv.Date.IsZero() {
		return miss("the date of issue",
			"voucher_version.voucher_date",
			"CGST Rules 2017, Rule 46(c)")
	}
	if inv.Supplier.Name == "" {
		return miss("the supplier's name",
			"company.mailing_name / company.name",
			"CGST Rules 2017, Rule 46(a)")
	}
	if inv.Supplier.Address1 == "" {
		return miss("the supplier's address",
			"company.address",
			"CGST Rules 2017, Rule 46(a)")
	}
	if inv.Supplier.GSTIN == "" {
		// Required on a bill of supply too: Rule 49 asks for the same supplier
		// block as Rule 46 and drops only the tax rate and amount.
		return miss("the supplier's GSTIN",
			"company.gstin",
			"CGST Rules 2017, Rule 46(a); Rule 49 for a bill of supply")
	}
	if inv.Recipient.Name == "" {
		return miss("the recipient's name",
			"the party ledger named by voucher_version.party_ledger_id",
			"CGST Rules 2017, Rule 46(d)")
	}
	if inv.Recipient.Address1 == "" {
		// MANDATORY WHETHER OR NOT THE RECIPIENT IS REGISTERED. Rule 46(d) asks
		// for the recipient's name, address and — if registered — GSTIN: only the
		// GSTIN is conditional. A walk-in counter sale therefore needs an address
		// on the party master too, which is why the demo/seed data carries one.
		//
		// This is a DATA-ENTRY fix and says so (NotModelled stays false): migration
		// 0039 added ledger.address/city/pincode, so the field exists and is empty.
		// Before 0039 it was in UnmodelledInvoiceParticulars and nothing could be
		// printed at all.
		source := fmt.Sprintf("ledger %q has no address recorded (ledger.address)", party.name)
		if party.noMaster {
			source = fmt.Sprintf(
				"%q is only a label on the voucher (voucher_version.payload->>'party'); "+
					"there is no party ledger behind it to carry an address", party.name)
		}
		return miss("the recipient's address", source,
			"CGST Rules 2017, Rule 46(d) — the name, ADDRESS and GSTIN (if registered) of the recipient")
	}
	if party.registered() && inv.Recipient.GSTIN == "" {
		return miss("the recipient's GSTIN",
			fmt.Sprintf("ledger %q is marked %s under GST and carries no gstin",
				party.name, party.registrationType),
			"CGST Rules 2017, Rule 46(d) — name, address and GSTIN of the recipient, if registered")
	}
	// THE RECIPIENT'S STATE, BY NAME. Checked here and not left to
	// printing.ValidateRule46 because only this file knows WHERE the code came
	// from: "ledger \"Coorg Hardware\" records state code 25" names the master to
	// open, and "recipient.state is required" does not. A code the table names
	// needs no check — partyBlock has already filled the name.
	if err := stateNameFault(inv.Kind, "the recipient's State",
		party.stateCode,
		fmt.Sprintf("ledger %q records state_code %q", party.name, party.stateCode),
		"CGST Rules 2017, Rule 46(e) — on a supply of fifty thousand rupees or more to an "+
			"unregistered person, the recipient's address and the NAME OF THE STATE and its code"); err != nil {
		return err
	}
	if inv.Kind == printing.KindTaxInvoice && inv.PlaceOfSupplyCode == "" {
		return miss("the place of supply",
			"voucher_version.place_of_supply",
			"CGST Rules 2017, Rule 46(n)")
	}
	// AND THE PLACE OF SUPPLY BY NAME. Only on a tax invoice: Rule 49 does not
	// make the place of supply a particular of a bill of supply at all, so a
	// composition or exempt supplier whose voucher happens to carry a stale code
	// is not stopped from billing over a field their document does not state.
	if inv.Kind == printing.KindTaxInvoice {
		if err := stateNameFault(inv.Kind, "the place of supply",
			inv.PlaceOfSupplyCode,
			"voucher_version.place_of_supply",
			"CGST Rules 2017, Rule 46(n) — the place of supply ALONG WITH THE NAME OF THE STATE, "+
				"on a supply involving inter-State trade or commerce"); err != nil {
			return err
		}
	}
	// LAST, because it is the only check about the figures rather than the
	// particulars: an operator fixing a voucher that is missing its place of
	// supply does not also want to be told the totals do not close.
	return validateRoundOff(inv)
}

// validateRoundOff refuses a document whose residual is too large to be a
// rounding. See ResidualNotARoundOffError for why a residual of any size is worse
// than a refusal: the renderer labels it "Round Off".
//
// Note what this is NOT: it is not a recomputation of the total. The total stays
// the party's posted leg, the taxable value stays the stored base, and the
// residual stays whatever the books make it. This only decides whether that
// residual may be printed under a label that says it is small.
func validateRoundOff(inv *printing.Invoice) error {
	if inv.RoundOff.Abs().LessThanOrEqual(roundOffBound) {
		return nil
	}
	return &ResidualNotARoundOffError{
		Kind:         inv.Kind,
		Number:       inv.Number,
		TaxableValue: inv.Subtotal,
		TaxTotal:     sumTax(inv.Taxes),
		PartyLeg:     inv.Total,
		Residual:     inv.RoundOff,
		Bound:        roundOffBound,
		Rule: "CGST Rules 2017, Rule 46(j) — the total value of the supply — read with " +
			"Rule 46(k) — the taxable value",
	}
}

// ---------------------------------------------------------------------------
// LoadVoucherDoc
// ---------------------------------------------------------------------------

// LoadVoucherDoc assembles the documents that are not invoices: the receipt and
// payment vouchers, the credit and debit notes, and the delivery note.
//
// It is a SEPARATE loader rather than a mode of LoadInvoice because the two
// documents are different objects: an invoice has a taxable base, a tax block and
// a total owed; a receipt has ACCOUNTING LEGS and the bills they settle, and
// which ledger was debited is the whole content of the document. Collapsing them
// would mean a receipt carrying an empty Items block and an invoice carrying an
// empty Lines block, and every renderer branching on which fields happen to be
// populated.
func (s *Store) LoadVoucherDoc(ctx context.Context, companyID, voucherID uuid.UUID) (printing.VoucherDoc, error) {
	var doc printing.VoucherDoc
	err := s.inTenantTx(ctx, companyID, true, func(ctx context.Context, tx pgx.Tx) error {
		h, err := loadHead(ctx, tx, companyID, voucherID)
		if err != nil {
			return err
		}
		kind, isInvoice, err := docKindForClass(h.class)
		if err != nil {
			return err
		}
		if isInvoice {
			return fmt.Errorf("%w: %q prints as %s", ErrNotAVoucherDoc, h.class, kind)
		}

		legs, err := loadLegs(ctx, tx, companyID, h.versionID)
		if err != nil {
			return err
		}
		bills, err := loadBills(ctx, tx, companyID, h.versionID)
		if err != nil {
			return err
		}
		moves, err := loadMovements(ctx, tx, companyID, h.versionID)
		if err != nil {
			return err
		}
		gstLines, err := loadGSTLines(ctx, tx, companyID, h.versionID)
		if err != nil {
			return err
		}

		doc.Kind = kind
		doc.Number = h.voucherNo
		if h.date != nil {
			doc.Date = *h.date
		}
		doc.Narration = h.narration
		if doc.Supplier, err = loadSupplier(ctx, tx, companyID); err != nil {
			return err
		}
		if h.hasParty {
			p, err := loadParty(ctx, tx, companyID, h.partyLedgerID)
			if err != nil {
				return err
			}
			// The State NAME is filled where the code can be named and left empty
			// where it cannot — and unlike the invoice path, an unnameable code is
			// NOT refused here. These documents (a receipt, a payment, a delivery
			// note, a credit or debit note) state the party's block; none of them
			// carries a place of supply, and this track cites no requirement that
			// they name the party's State. Withholding a customer's receipt over a
			// stale code on their master would be refusing on a particular the
			// document does not state — see stateNameFault, which the invoice path
			// calls because Rule 46(n) and Rule 46(e) do make the name mandatory
			// there.
			block := partyBlock(p)
			doc.Party = &block
		} else if h.partyName != "" {
			// A label with no master behind it. Printed as the name it is, with
			// no GSTIN invented for it.
			doc.Party = &printing.Party{Name: h.partyName}
		}

		// The bill a leg opens or settles, per ledger. Several allocations on one
		// ledger are joined rather than one being picked: a receipt that settles
		// three bills has to SAY so — a document naming one of the three is
		// evidence the customer will use to dispute the other two.
		refs := map[uuid.UUID][]string{}
		for _, b := range bills {
			refs[b.ledgerID] = append(refs[b.ledgerID], b.refName)
		}
		for _, l := range legs {
			doc.Lines = append(doc.Lines, printing.VoucherLine{
				Ledger:  l.ledger,
				IsDebit: l.isDebit,
				Amount:  l.amount,
				BillRef: strings.Join(refs[l.ledgerID], ", "),
				// A per-leg narration is not stored: the payload carries ONE
				// narration for the voucher, which is on doc.Narration.
			})
		}

		// A delivery note's quantities. The same movements are the Items block of
		// any voucher document that moves goods; for a note they are the goods
		// the adjustment relates to.
		for _, m := range moves {
			doc.Items = append(doc.Items, printing.LineItem{
				SerialNo:    m.lineNo,
				Description: m.item,
				HSN:         m.hsn,
				Quantity:    m.qty,
				Unit:        m.unit,
				Rate:        m.rate,
				// A PERCENTAGE, exactly as on the invoice path above.
				DiscountPct: m.discountPct,
				Amount:      m.amount,
			})
		}

		// A note's tax adjustment carries a rate and an amount per head exactly as
		// an invoice does (Rule 53(1A) lists the same tax particulars), so the
		// same builder serves it. A receipt or payment has no GST lines and gets
		// no tax block.
		if len(gstLines) > 0 && (kind == printing.KindCreditNote || kind == printing.KindDebitNote) {
			if doc.Taxes, err = taxHeads(kind, gstLines); err != nil {
				return err
			}
		}

		// WHAT THE NOTE ADJUSTS (Rule 53(1A) — NO CLAUSE LETTER, deliberately:
		// this comment named a clause LETTER within 53(1A) for one wave and
		// internal/printing named a DIFFERENT letter for the same requirement, at
		// most one could be right, and neither can be checked from here because
		// the gazette and the GSTN portal are unreachable. The SUBSTANCE — a note
		// must name the document it adjusts, by number and date — is not in doubt
		// on any reading, so it is the substance that is enforced), from the two
		// places the books record it:
		//
		//   a CREDIT note adjusts OUR invoice, and the bill-wise AGAINST
		//   allocation names it (that is the reference the outstanding is
		//   settled against);
		//   a DEBIT note is raised on a SUPPLIER, so what it adjusts is the
		//   SUPPLIER's document — voucher_version.supplier_invoice_no/date, the
		//   columns migration 0032 added for exactly this identity.
		switch kind {
		case printing.KindCreditNote:
			for _, b := range bills {
				if b.refType == "AGAINST" {
					// ⛔ THE INVOICE'S OWN NUMBER, NOT THE BILL REFERENCE, and the
					// difference is the whole line. The reference LOCATES the
					// bill in our books; the invoice the customer is holding says
					// what it is called. They coincide only when nobody named the
					// bill — and since bill names were made unique they no longer
					// coincide even then, because an unnamed bill is opened as
					// `Sales/2026-27/7`. Printing that under "Against Invoice No."
					// puts an internal name on the one line the recipient uses to
					// match the credit to a supply.
					//
					// AND ITS DATE, which the note is unprintable without: an
					// invoice serial number repeats every financial year, so
					// printing.validateVoucherDoc refuses a note that names a
					// number and no date. Both are READ from the voucher that
					// OPENED that bill — never derived from the note's own date,
					// which would put a date on the paper that no invoice was
					// issued on.
					//
					// The reference SURVIVES as the fallback: when nothing on file
					// opened it, naming what the operator keyed is more use to the
					// recipient than naming nothing, and the missing date is what
					// makes the renderer refuse rather than print a half-identity.
					doc.AgainstNumber = b.refName
					opener, err := billOpener(ctx, tx, companyID, b.ledgerID, b.refName)
					if err != nil {
						return err
					}
					if opener != nil {
						if opener.number != "" {
							doc.AgainstNumber = opener.number
						}
						doc.AgainstDate = opener.date
					}
					break
				}
			}
		case printing.KindDebitNote:
			// ⛔ THE COLUMN FIRST, THEN THE BILL-WISE ALLOCATION — AND WITHOUT THE
			// SECOND HALF NO DEBIT NOTE IN THIS PRODUCT COULD EVER BE PRINTED.
			//
			// supplier_invoice_no/date are the identity migration 0032 added for
			// exactly this, and they are the RIGHT source when they are populated:
			// they are the supplier's own document number, written from the same
			// `inward` block the hashed payload carries. But NOTHING WRITES THEM
			// OVER HTTP. internal/ledger fills them only from a purchase's inward
			// block, so on a debit note they are always empty — AgainstNumber came
			// out "", validateVoucherDoc refused a note that names no document, and
			// every debit note answered 422. A class the client offers to print and
			// the server can never render is worse than one it refuses.
			//
			// So the AGAINST allocation is read as well, exactly as a credit note
			// reads it. On a debit note it is the reference of the SUPPLIER'S bill:
			// the purchase opened that bill with the number the operator keyed off
			// the supplier's invoice, and the note settles against it. That IS the
			// document being adjusted, which is what Rule 53(1A) requires the note
			// to name — the column and the allocation are two records of one fact,
			// and the fallback order says which one is authoritative when both
			// exist.
			//
			// The DATE comes from the voucher that OPENED the bill (billOpener),
			// never from the note's own date — a note dated today does not tell you
			// when the invoice it adjusts was issued, and a serial number repeats
			// every financial year, which is why the renderer refuses a number with
			// no date beside it.
			//
			// ⛔⛔ AND THE MIRROR OF THE CREDIT NOTE'S RULE IS NOT THE SAME RULE.
			// The credit note above prints the OPENING VOUCHER'S NUMBER because
			// that invoice is OURS. Here the opening voucher is a PURCHASE, and its
			// number is ours too — which is exactly why it must not be printed:
			// the supplier has never seen our purchase number, so a debit note
			// citing it names a document that does not exist on the side that has
			// to act on it. What the supplier can match is their OWN invoice
			// number, which the purchase records in supplier_invoice_no, and
			// failing that the bill reference the operator keyed off that very
			// invoice. Hence three sources in falling order of authority and NOT
			// the opening voucher's number at any point.
			doc.AgainstNumber = h.supplierDocNo
			if h.supplierDate != nil {
				d := *h.supplierDate
				doc.AgainstDate = &d
			}
			if doc.AgainstNumber == "" {
				for _, b := range bills {
					if b.refType != "AGAINST" {
						continue
					}
					doc.AgainstNumber = b.refName
					opener, err := billOpener(ctx, tx, companyID, b.ledgerID, b.refName)
					if err != nil {
						return err
					}
					if opener != nil {
						if opener.supplierNo != "" {
							doc.AgainstNumber = opener.supplierNo
						}
						// The supplier's own invoice date where the purchase
						// recorded one; otherwise the date we booked the purchase,
						// which is the nearest fact the books hold about when that
						// document was issued.
						doc.AgainstDate = opener.date
						if opener.supplierDate != nil {
							doc.AgainstDate = opener.supplierDate
						}
					}
					break
				}
			}
		}

		// THE REASON THE NOTE WAS ISSUED, FROM THE NARRATION.
		//
		// There is no `reason` column, and for one wave that meant Reason was left
		// empty — which made every credit and debit note UNPRINTABLE, because the
		// renderer required one. The narration is where an operator types the
		// reason in this schema (it is the only free-text field a note carries), so
		// that is what the note states.
		//
		// The narration is then CLEARED for a note, so the same sentence does not
		// print twice: the renderer draws Reason in the against-invoice box and
		// Narration again in the footer, and a document that says
		// "Reason: sales return" and "Narration: sales return" reads like a
		// rendering fault. On every other document kind the narration stays where
		// it is. Nothing is invented and nothing is lost — the text moves into the
		// field the statute names.
		switch kind {
		case printing.KindCreditNote, printing.KindDebitNote:
			if r := strings.TrimSpace(doc.Narration); r != "" {
				doc.Reason, doc.Narration = r, ""
			}
		}

		// Total: what the party is credited or paid. The party leg where there is
		// one (the figure that hits their account), and the balanced debit total
		// otherwise — for a Contra-shaped document with no party, and for a
		// delivery note, which has no postings at all and whose value is its
		// goods.
		if t, ok := partyLegNet(legs, h.partyLedgerID, h.hasParty); ok {
			doc.Total = t
		} else if len(legs) > 0 {
			sum := decimal.Zero
			for _, l := range legs {
				if l.isDebit {
					sum = sum.Add(l.amount)
				}
			}
			doc.Total = sum
		} else {
			doc.Total = sumLineAmounts(doc.Items)
		}
		return nil
	})
	if err != nil {
		return printing.VoucherDoc{}, err
	}

	overrides, err := s.LoadOverrides(ctx, companyID, doc.Kind)
	if err != nil {
		return printing.VoucherDoc{}, err
	}
	doc.Overrides = overrides
	return doc, nil
}
