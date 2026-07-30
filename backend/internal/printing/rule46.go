package printing

// rule46.go — the completeness gate that stands between a voucher and a printed
// tax invoice.
//
// WHY THIS FILE EXISTS AT ALL. A tax invoice that is missing a mandatory
// particular is not a slightly untidy invoice, it is an INVALID one: the
// recipient's input tax credit rests on it (section 16(2)(a) of the CGST Act
// requires the recipient to be in possession of a tax invoice), and an officer
// looking at a document with a blank place of supply is looking at a defective
// document. So the only two honest outcomes for a print request are a COMPLETE
// document or a REFUSAL. A blank on the page is the one outcome that must never
// happen, because the refusal is caught at the counter by the person who can fix
// it, and the blank is caught months later by somebody who cannot.
//
// AUTHORITY AND EFFECTIVE DATE
// ----------------------------
// Rule 46 of the Central Goods and Services Tax Rules, 2017 ("Tax invoice"), in
// force from 1 July 2017. The clause letters (a)…(q) below are the rule's own, and
// each constant quotes the words of its clause so that a reader can check the
// citation without leaving the file. Rule 46 opens "Subject to rule 54" — rule
// 54 carries the special invoices (ISD, banking, insurance, goods transport
// agency, passenger transport), which this renderer does not claim to produce.
//
// NO NOTIFICATION NUMBER IS CITED FOR THE INVOICE RULES, AND THAT IS DELIBERATE.
// An earlier draft of this file attributed rule 46 and rule 49 to "Notification
// No. 3/2017-Central Tax dated 19 June 2017". That notification is the one that
// brought the CGST Rules, 2017 into being, but the chapter carrying the tax-invoice
// rules is understood to have been inserted by a LATER amending notification, and
// WHICH ONE CANNOT BE CHECKED FROM THIS ENVIRONMENT — the GSTN portal is not
// reachable. A notification number in a refusal an operator or an auditor reads is
// a citation, and a citation that is probably attached to the wrong instrument is
// worse than no citation at all. So the number is UNVERIFIED and is not asserted:
// what is stated is the rule, its chapter's subject matter and the commencement
// date, none of which is in doubt. The same correction is owed by
// internal/gststate/gststate.go and internal/printing/printingdb/document.go,
// which still carry the old attribution.
//
// Rule 49 ("Bill of supply") governs a document where NO TAX IS CHARGED — a
// composition supplier or a wholly exempt supply. Its particulars are a SUBSET
// of Rule 46's with the tax particulars removed, and validating a bill of supply
// against the tax-invoice clauses would refuse a perfectly legal document for
// lacking a rate of tax it is forbidden to charge. That is why there are two
// lists here and not one.
//
// WHAT THIS FILE CANNOT CHECK, STATED PLAINLY
//   - UNIQUENESS of the serial number within the financial year (clause (b)).
//     One document cannot see the series it belongs to; that invariant belongs to
//     the numbering service and its unique index. What is checked here is the
//     SHAPE the clause specifies: sixteen characters, alphanumerics and - and /.
//   - The GSTIN CHECKSUM. Presence is checked; validity is a different concern
//     living with the party master, and a renderer that recomputed it would be a
//     second, drifting implementation of the same rule.
//   - Whether the SIGNATURE (clause (q)) was actually applied. See
//     clauseSignature.
//   - THE QR CODE of an e-invoice. Rule 46 gained a further particular for
//     invoices issued under rule 48(4) — a Quick Response code with the IRN
//     embedded in it — and this renderer does not generate one, so it is neither
//     validated nor printed. The IRN itself IS printed. The clause letter for the
//     QR-code particular is believed to be 46(r) but is marked UNVERIFIED here
//     and is deliberately not written as a constant: an unverified clause letter
//     in an error message is a false citation, and there is no constant to cite
//     because there is no check. A company under the e-invoicing mandate needs
//     the QR code on the face of the document, and that is a gap in this track,
//     not a decision.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// The mandatory particulars. ONE CONSTANT PER CLAUSE.
// ---------------------------------------------------------------------------

// The clause letters are the citation. A wrong letter is a FALSE CITATION — an
// operator or an auditor handed "Rule 46(k)" for a missing place of supply is
// being told something untrue about the law — so each constant carries the words
// of its own clause and nothing is abbreviated to save a line.
const (
	// Rule 46(a): "name, address and Goods and Services Tax Identification
	// Number of the supplier".
	clauseSupplierIdentity = "46(a)"

	// Rule 46(b): "a consecutive serial number not exceeding sixteen
	// characters, in one or multiple series, containing alphabets or numerals
	// or special characters hyphen or dash and slash symbolised as '-' and '/'
	// respectively, and any combination thereof, unique for a financial year".
	clauseSerialNumber = "46(b)"

	// Rule 46(c): "date of its issue".
	clauseDateOfIssue = "46(c)"

	// Rule 46(d): "name, address and Goods and Services Tax Identification
	// Number or Unique Identity Number, if registered, of the recipient". The
	// "if registered" attaches to the GSTIN/UIN alone — the name and address of
	// the recipient are required on every tax invoice.
	clauseRecipientIdentity = "46(d)"

	// Rule 46(e): "name and address of the recipient and the address of
	// delivery, along with the name of the State and its code, if such
	// recipient is un-registered and where the value of the taxable supply is
	// fifty thousand rupees or more".
	clauseUnregisteredHighValue = "46(e)"

	// Rule 46(g): "Harmonised System of Nomenclature code for goods or
	// services".
	//
	// WHAT IS CHECKED IS THAT A CODE IS PRESENT — not how many digits it has,
	// and not whether this particular supplier is one who may omit it. The words
	// of the clause are unconditional, but rule 46 carries a PROVISO empowering
	// the Government, on the recommendations of the Council, to specify by
	// notification the NUMBER OF DIGITS of the Harmonised System of Nomenclature
	// code that a class of registered persons shall be required to mention, and
	// the class of registered persons who shall not be required to mention it.
	// The requirement that actually binds a given invoice is therefore a function
	// of the supplier's AGGREGATE TURNOVER in the preceding financial year and of
	// whether the supply is to a registered person or to a consumer.
	//
	// UNVERIFIED, AND DELIBERATELY NOT IMPLEMENTED: the notification that carries
	// that table. Its number, its turnover thresholds, its digit counts and the
	// exact terms of the relief for a small taxpayer CANNOT BE CHECKED FROM THIS
	// ENVIRONMENT — the GSTN portal is not reachable — so no notification number
	// appears anywhere in this file, in a comment or in a message. The relaxation
	// is not applied either, and that asymmetry is the point: this gate REFUSES
	// only on what is cited (rule 46(g) and rule 49's own HSN particular, both
	// transcribed above and below from the rule text), and it will not RELAX a
	// statutory particular on the strength of a notification nobody here could
	// read. That is the same discipline vdAgainstInvoiceRequirement in
	// voucherdoc.go applies to the credit-note reason, pointed the other way.
	//
	// THE COST OF THAT CHOICE, STATED SO IT IS NOT REDISCOVERED AS A SURPRISE:
	// this gate may OVER-REFUSE a supplier whom the notification relieves of the
	// HSN altogether. That is the survivable direction — the refusal reaches the
	// operator at the counter, who can key a code, where a blank HSN reaches the
	// recipient's assessing officer months later — but it is a real
	// over-strictness and the fix is NOT a looser check. It is two facts the data
	// model does not hold: the company's aggregate turnover of the preceding
	// financial year (to evaluate the condition at all), and, on an accounting-only
	// or exempt sale, ANY per-line HSN or SAC to print — see the note above
	// clauseBoSHSN, which is where this bites hardest.
	clauseHSN = "46(g)"

	// Rule 46(h): "description of goods or services".
	clauseDescription = "46(h)"

	// Rule 46(i): "quantity in case of goods and unit or Unique Quantity Code
	// thereof". "in case of goods" is the reason this is conditional — see
	// invQuantityParticularsApply.
	clauseQuantityAndUnit = "46(i)"

	// Rule 46(j): "total value of supply of goods or services or both".
	clauseTotalValue = "46(j)"

	// Rule 46(k): "taxable value of the supply of goods or services or both
	// taking into account discount or abatement, if any".
	clauseTaxableValue = "46(k)"

	// Rule 46(l): "rate of tax (central tax, State tax, integrated tax, Union
	// territory tax or cess)". PER HEAD. A single "GST 18%" line does not
	// satisfy it for a 9 + 9 CGST/SGST supply, because neither of the two taxes
	// actually charged appears with its own rate.
	clauseRateOfTax = "46(l)"

	// Rule 46(m): "amount of tax charged in respect of taxable goods or
	// services (central tax, State tax, integrated tax, Union territory tax or
	// cess)". Per head, for the same reason as (l).
	clauseAmountOfTax = "46(m)"

	// Rule 46(n): "place of supply along with the name of the State, in the
	// case of a supply of goods or services or both involving inter-State trade
	// or commerce".
	clausePlaceOfSupply = "46(n)"

	// Rule 46(o): "address of delivery where the same is different from the
	// place of supply".
	clauseDeliveryAddress = "46(o)"

	// Rule 46(p): "whether the tax is payable on reverse charge basis".
	//
	// NOT VALIDATED, DISCHARGED BY THE RENDERER. The particular is a statement
	// the document must MAKE, and Invoice.ReverseCharge is a bool that always
	// has one of its two values — there is no "missing" state to detect. So
	// RenderInvoice prints the marker unconditionally, saying Yes or No, and the
	// clause is satisfied structurally rather than by a check that could never
	// fire. Pinned by
	// TestATaxInvoiceStatesWhetherTaxIsPayableOnReverseChargeEitherWay.
	clauseReverseCharge = "46(p)"

	// Rule 46(q): "signature or digital signature of the supplier or his
	// authorised representative".
	//
	// NOT VALIDATED, DISCHARGED BY THE RENDERER. What this package can put on
	// paper is the signature PANEL — "For <Company>" over a space and the
	// authorised-signatory label — which is then signed by hand, or, for an
	// e-invoice under rule 48(4), is accompanied by the IRN the portal digitally
	// signed. Claiming to validate that a human signed a PDF would be a
	// validation we did not perform. Pinned by
	// TestEveryPrintedInvoiceCarriesASignaturePanelForRule46q.
	clauseSignature = "46(q)"
)

// The Rule 49 particulars of a BILL OF SUPPLY. Rule 49 of the CGST Rules 2017,
// in force from 1 July 2017; no notification number is cited, for the reason given
// in the file header.
//
// WHICH CLAUSES ARE APPLIED TO A BILL OF SUPPLY, AND WHY. Rule 49 lists: the
// supplier's name, address and GSTIN; a consecutive serial number on the same
// sixteen-character terms as rule 46(b); the date of issue; the recipient's
// name, address and GSTIN or UIN if registered; the HSN code; the description;
// the value of supply taking into account discount or abatement; and the
// signature. It does NOT list a rate of tax, an amount of tax, a place of supply
// or a reverse-charge marker — a bill of supply is issued precisely because no
// tax is being charged, so validating it against rule 46(l), (m), (n) or (p)
// would refuse a legal document for lacking particulars it must not carry.
const (
	clauseBoSSupplierIdentity  = "49(a)"
	clauseBoSSerialNumber      = "49(b)"
	clauseBoSDateOfIssue       = "49(c)"
	clauseBoSRecipientIdentity = "49(d)"

	// THE HSN ON A BILL OF SUPPLY: THE REFUSAL STAYS, AND HERE IS THE REASONING,
	// because this one check is the reason no exempt and no accounting-only sale
	// in this product prints at all today.
	//
	// THE STATUTORY QUESTION, ANSWERED FIRST. Rule 49 lists the Harmonised System
	// of Nomenclature code for goods or services among the particulars a bill of
	// supply "shall contain" — it is in the rule's own list, alongside the
	// supplier's identity, the serial number, the date, the recipient's identity,
	// the description, the value and the signature. It is NOT an inference from
	// rule 46 and it is not a recollection of a notification: it is a listed
	// particular of the rule that governs this document, transcribed from the rule
	// text exactly as every other clause in this file is (CGST Rules 2017, rule
	// 49, in force from 1 July 2017; no notification number is cited and no portal
	// validation was performed here — see the file header).
	//
	// SO THE REFUSAL IS KEPT. The precedent this track set for an unverifiable
	// particular (the credit-note reason: keep what is cited, stop refusing on
	// what is not — see vdAgainstInvoiceRequirement) points THIS way here, not the
	// other. What is uncertain about the HSN is only the RELAXATION: rule 46's
	// proviso lets a notification excuse a class of registered persons from
	// mentioning the code, and whether and how that reaches a bill of supply, for
	// which turnover, and with how many digits, cannot be checked from this
	// environment. Relaxing on an unread notification would be weakening a
	// statutory gate on an unverified basis; refusing rests on the rule's own
	// list. Where the two differ, the cited one wins. Nothing here cites a
	// notification number, and nothing here invents a proviso number.
	//
	// WHAT THE REFUSAL COSTS TODAY, PLAINLY. printingdb's postings-only arm — the
	// arm that serves every sale keyed as plain double entry, which is every
	// exempt sale and every accounting-only sale — sets no HSN on its lines,
	// because an accounting-only sale has no stock movement and no GST line, and
	// there is nowhere in the books for an HSN to have been recorded. So this
	// check refuses EVERY bill of supply the product can currently assemble. That
	// is an honest refusal of an incomplete document, not a bug in this gate, and
	// the fix is a DATA-MODEL change: something on the sale — a SAC on the service
	// or sales ledger, or an HSN typed on the voucher line — has to carry the code
	// before the document can be printed. Until it does, an operator who needs a
	// bill of supply is refused with the field named, which is the outcome this
	// package chooses over a blank particular on paper.
	clauseBoSHSN = "49(e)"

	clauseBoSDescription   = "49(f)"
	clauseBoSValueOfSupply = "49(g)"
	clauseBoSSignature     = "49(h)" // discharged by the renderer, as 46(q) is
)

// invSerialNumberMaxLen is the "not exceeding sixteen characters" of Rule 46(b)
// and Rule 49(b).
const invSerialNumberMaxLen = 16

// invUnregisteredHighValueThreshold is the "fifty thousand rupees" of Rule
// 46(e). The clause reads "fifty thousand rupees OR MORE", so the comparison is
// >= and not > — an invoice for exactly Rs 50,000 to an unregistered person
// carries the extra particulars.
var invUnregisteredHighValueThreshold = decimal.NewFromInt(50000)

// ErrTaxOnBillOfSupply says a document declared as a bill of supply carries tax
// heads.
//
// THIS IS A REFUSAL AND NOT A MISSING FIELD. A composition supplier "shall not
// collect any tax from the recipient" (section 10(4) of the CGST Act), and an
// exempt supply has no tax to collect; a bill of supply with CGST on it is
// therefore not an incomplete document that somebody forgot to finish, it is a
// document that must not exist. Printing it would put a tax charge in a
// customer's hands that the customer cannot claim credit for and the supplier
// cannot lawfully collect, so the print is refused outright.
var ErrTaxOnBillOfSupply = errors.New("printing: a bill of supply cannot charge tax")

// ---------------------------------------------------------------------------
// The error
// ---------------------------------------------------------------------------

// MissingField names ONE mandatory particular that is absent, the clause that
// requires it, and the consequence of printing without it.
//
// Why is required and not decoration: the string ends up in front of an operator
// who has to fix the voucher, and "recipient.state is required" tells them
// nothing they did not already know from the 400.
type MissingField struct {
	Field  string // the document field, e.g. "recipient.stateCode"
	Clause string // the clause that requires it, e.g. "46(e)"
	Why    string // what goes wrong if the document is printed without it
}

// Rule46Error reports EVERY mandatory particular that is missing, not the first
// one.
//
// ALL OF THEM, IN ONE ERROR, AND THIS IS THE WHOLE DESIGN OF THE TYPE. A
// validator that stops at the first fault turns fixing an invoice into a
// conversation: the operator supplies the HSN, is told about the unit, supplies
// the unit, is told about the place of supply. Three round trips through a
// voucher screen is a bad afternoon and it is how an invoice ends up issued from
// a spreadsheet instead. Missing is therefore a slice, it is built in clause
// order so the message reads down the rule, and Error() names all of it.
//
// It is also used for the Rule 49 particulars of a bill of supply. The type
// keeps its name because Rule 46 is the exhaustive case; the CLAUSE on each
// MissingField is what says which rule was applied, so nothing in the message a
// caller sees cites the wrong rule.
type Rule46Error struct {
	Missing []MissingField
}

// Error lists every missing particular with its clause.
func (e *Rule46Error) Error() string {
	if e == nil || len(e.Missing) == 0 {
		// Unreachable through ValidateRule46, which returns nil rather than an
		// empty error. Present so that a future caller constructing the type by
		// hand cannot produce the empty string as an error message.
		return "printing: the document was refused but no missing particular was named; this is a bug in the completeness gate"
	}
	var b strings.Builder
	// THE VERB AGREES TOO, and this is not pedantry about grammar. This is the
	// most frequent refusal in the product and it is read by an operator at a
	// counter with a customer waiting; "1 mandatory particular are missing" reads
	// as a machine that does not know what it is talking about, which is exactly
	// the moment somebody decides to raise the invoice from a spreadsheet instead.
	// One missing particular is by far the commonest case, so the broken half of
	// the sentence was the half almost everybody saw.
	particulars, verb := "mandatory particulars", "are"
	if len(e.Missing) == 1 {
		particulars, verb = "mandatory particular", "is"
	}
	fmt.Fprintf(&b, "printing: this document cannot be printed: %d %s %s missing:",
		len(e.Missing), particulars, verb)
	for i, m := range e.Missing {
		if i > 0 {
			b.WriteByte(';')
		}
		fmt.Fprintf(&b, " %s is required by Rule %s (%s)", m.Field, m.Clause, m.Why)
	}
	return b.String()
}

// Fields lists the missing field names, in clause order. It exists so an HTTP
// layer can put the field list in a machine-readable body next to the message
// without re-parsing Error().
func (e *Rule46Error) Fields() []string {
	if e == nil {
		return nil
	}
	out := make([]string, 0, len(e.Missing))
	for _, m := range e.Missing {
		out = append(out, m.Field)
	}
	return out
}

// ---------------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------------

// ValidateRule46 reports whether inv carries every mandatory particular, and
// returns nil when it does.
//
// It validates the particulars OF THE DOCUMENT IT WAS HANDED: Rule 46 for a
// KindTaxInvoice, Rule 49 for a KindBillOfSupply. The dispatch is deliberate and
// is safer than the alternative in both directions — a bill of supply checked
// against Rule 46 would be refused for having no rate of tax, which it is
// forbidden to charge, and a tax invoice checked against Rule 49 would be
// printed with no place of supply and no reverse-charge marker at all.
//
// Any other DocKind is refused: this gate has no list of particulars for a
// delivery note or a receipt, and silently returning nil for one would be a gate
// that certifies documents it has never looked at.
func ValidateRule46(inv Invoice) error {
	switch inv.Kind {
	case KindTaxInvoice:
		if missing := rule46Particulars(inv); len(missing) > 0 {
			return &Rule46Error{Missing: missing}
		}
		return nil
	case KindBillOfSupply:
		// The tax check comes FIRST, before completeness. A bill of supply with
		// tax on it is not an unfinished document, it is the wrong document, and
		// telling the operator to fill in three more fields would send them
		// further down a road that ends in an unlawful charge.
		if len(inv.Taxes) > 0 {
			return fmt.Errorf("%w: this document is declared a bill of supply but carries %d tax head(s), the first being %q; a composition supplier may not collect tax (section 10(4) of the CGST Act) and an exempt supply has none to collect, so a tax invoice is the document that was meant",
				ErrTaxOnBillOfSupply, len(inv.Taxes), inv.Taxes[0].Name)
		}
		if missing := rule49Particulars(inv); len(missing) > 0 {
			return &Rule46Error{Missing: missing}
		}
		return nil
	default:
		return fmt.Errorf("printing: the completeness gate has no list of mandatory particulars for a %q, so it will not certify one; it validates %q against Rule 46 and %q against Rule 49",
			string(inv.Kind), string(KindTaxInvoice), string(KindBillOfSupply))
	}
}

// rule46Particulars collects every missing particular of a TAX INVOICE, in
// clause order.
func rule46Particulars(inv Invoice) []MissingField {
	var missing []MissingField
	add := func(field, clause, why string) {
		missing = append(missing, MissingField{Field: field, Clause: clause, Why: why})
	}

	// ---- (a) supplier name, address and GSTIN ----------------------------
	if invBlank(inv.Supplier.Name) {
		add("supplier.name", clauseSupplierIdentity,
			"an invoice that does not say who issued it identifies no supplier to the recipient or to an officer")
	}
	if invBlank(inv.Supplier.Address1) {
		add("supplier.address1", clauseSupplierIdentity,
			"the supplier's address is the registered place of business the invoice is issued from")
	}
	if invBlank(inv.Supplier.GSTIN) {
		add("supplier.gstin", clauseSupplierIdentity,
			"without the supplier's GSTIN the recipient cannot match this invoice to a return and cannot claim the input tax credit on it")
	}

	// ---- (b) consecutive serial number ----------------------------------
	missing = append(missing, invSerialNumberFaults(inv.Number, clauseSerialNumber)...)

	// ---- (c) date of issue ----------------------------------------------
	if inv.Date.IsZero() {
		add("date", clauseDateOfIssue,
			"the date of issue fixes the tax period the supply falls in, and a blank one puts the invoice in no return at all")
	}

	// ---- (d) recipient name, address, and GSTIN if registered -----------
	if invBlank(inv.Recipient.Name) {
		add("recipient.name", clauseRecipientIdentity,
			"an invoice with no recipient names nobody who owes the amount and nobody who may claim credit for the tax")
	}
	if invBlank(inv.Recipient.Address1) {
		add("recipient.address1", clauseRecipientIdentity,
			"the recipient's address is a mandatory particular whether or not the recipient is registered")
	}

	// ---- (e) unregistered recipient, value of fifty thousand or more ----
	if invHighValueToUnregistered(inv) {
		if invBlank(inv.Recipient.State) {
			add("recipient.state", clauseUnregisteredHighValue,
				"a supply of fifty thousand rupees or more to an unregistered person must name the recipient's State, because there is no GSTIN on the document to infer it from")
		}
		if invBlank(inv.Recipient.StateCode) {
			add("recipient.stateCode", clauseUnregisteredHighValue,
				"the clause requires the State code as well as its name on a high-value supply to an unregistered person")
		}
	}

	// ---- (g)(h)(i) the line particulars ---------------------------------
	if len(inv.Items) == 0 {
		add("items", clauseDescription,
			"a tax invoice with no line items describes no goods and no services, so there is nothing it can be an invoice for")
	}
	for i, it := range inv.Items {
		// PRESENCE, NOT DIGITS. Whether four digits or six are required of this
		// supplier, and whether they are required of them at all, is set by a
		// notification this environment cannot read — see clauseHSN for the whole
		// judgement and for why the check is neither relaxed nor tightened here.
		if invBlank(it.HSN) {
			add(fmt.Sprintf("items[%d].hsn", i), clauseHSN,
				"the HSN or SAC code of the line is what the rate charged on it is defended by, and GSTR-1 will not accept the supply without it")
		}
		if invBlank(it.Description) {
			add(fmt.Sprintf("items[%d].description", i), clauseDescription,
				"a line with no description tells the recipient nothing about what was supplied")
		}
		if invQuantityParticularsApply(it) && invBlank(it.Unit) {
			add(fmt.Sprintf("items[%d].unit", i), clauseQuantityAndUnit,
				"a quantity without its unit is not a quantity: 5 of a thing could be 5 kilograms or 5 boxes, and the two are different supplies")
		}
	}

	// ---- (j) total value of supply --------------------------------------
	if inv.Total.IsZero() {
		add("total", clauseTotalValue,
			"the total value of supply is the amount the recipient is being asked to pay, and a nil total states that nothing was supplied")
	}

	// ---- (k) taxable value ---------------------------------------------
	if inv.Subtotal.IsZero() {
		add("subtotal", clauseTaxableValue,
			"the taxable value is the base the tax was computed on, and without it neither party can check that the tax charged is the tax due")
	}

	// ---- (l)(m) rate and amount of tax, PER HEAD -----------------------
	if len(inv.Taxes) == 0 {
		add("taxes", clauseRateOfTax,
			"a tax invoice with no tax head shows neither a rate nor an amount of tax; a supply that genuinely bears no tax is billed on a bill of supply under Rule 49, and a zero-rated supply still carries its head at 0%")
	}
	for i, t := range inv.Taxes {
		if invBlank(t.Name) {
			add(fmt.Sprintf("taxes[%d].name", i), clauseAmountOfTax,
				"an unnamed tax head cannot be read as central tax, State tax, integrated tax or cess, and the recipient's credit differs for each")
		}
	}

	// ---- (n) place of supply on an inter-state supply -------------------
	if invIsInterState(inv) {
		if invBlank(inv.PlaceOfSupply) {
			add("placeOfSupply", clausePlaceOfSupply,
				"on an inter-State supply the place of supply along with the name of the State is what decides which State's revenue the tax belongs to; without it the invoice is defective")
		}
		if invBlank(inv.PlaceOfSupplyCode) {
			add("placeOfSupplyCode", clausePlaceOfSupply,
				"the State code of the place of supply is what GSTR-1 reports the inter-State supply under")
		}
	}

	// ---- (o) address of delivery where different ------------------------
	if inv.ShipTo != nil && invBlank(inv.ShipTo.Address1) {
		add("shipTo.address1", clauseDeliveryAddress,
			"this invoice says the goods go somewhere other than the billing address, and then does not say where")
	}

	// (p) and (q) are discharged by RenderInvoice, not here — see
	// clauseReverseCharge and clauseSignature.
	return missing
}

// rule49Particulars collects every missing particular of a BILL OF SUPPLY, in
// clause order. It is deliberately the Rule 46 list with the tax particulars —
// (l) rate, (m) amount, (n) place of supply, (p) reverse charge — and the
// high-value-unregistered clause (e) removed; see the Rule 49 constants above
// for why.
func rule49Particulars(inv Invoice) []MissingField {
	var missing []MissingField
	add := func(field, clause, why string) {
		missing = append(missing, MissingField{Field: field, Clause: clause, Why: why})
	}

	if invBlank(inv.Supplier.Name) {
		add("supplier.name", clauseBoSSupplierIdentity,
			"a bill of supply that does not say who issued it identifies no supplier")
	}
	if invBlank(inv.Supplier.Address1) {
		add("supplier.address1", clauseBoSSupplierIdentity,
			"the supplier's address is the registered place of business the document is issued from")
	}
	if invBlank(inv.Supplier.GSTIN) {
		add("supplier.gstin", clauseBoSSupplierIdentity,
			"a bill of supply is issued by a REGISTERED person who is not charging tax, so the GSTIN is still required")
	}

	missing = append(missing, invSerialNumberFaults(inv.Number, clauseBoSSerialNumber)...)

	if inv.Date.IsZero() {
		add("date", clauseBoSDateOfIssue,
			"the date of issue fixes the tax period the supply is reported in")
	}
	if invBlank(inv.Recipient.Name) {
		add("recipient.name", clauseBoSRecipientIdentity,
			"a bill of supply with no recipient names nobody who owes the amount")
	}
	if invBlank(inv.Recipient.Address1) {
		add("recipient.address1", clauseBoSRecipientIdentity,
			"the recipient's address is a mandatory particular of a bill of supply")
	}

	if len(inv.Items) == 0 {
		add("items", clauseBoSDescription,
			"a bill of supply with no line items describes no supply")
	}
	for i, it := range inv.Items {
		// KEPT, ON THE RULE'S OWN LIST OF PARTICULARS, AND IT IS THE ONE CHECK
		// THAT STOPS EVERY ACCOUNTING-ONLY SALE IN THIS PRODUCT FROM PRINTING.
		// The full statutory reasoning, and what would have to be modelled to make
		// such a sale printable, is written out above clauseBoSHSN. The message
		// below says as much to the operator, because "items[0].hsn is required" on
		// a voucher screen with no HSN field on it is a dead end.
		if invBlank(it.HSN) {
			add(fmt.Sprintf("items[%d].hsn", i), clauseBoSHSN,
				"the HSN or SAC code of the line is what the exemption or the composition rate claimed on it is defended by; a sale keyed as plain double entry, with no stock item and no GST line, records the code nowhere in the books, so it has to be supplied on the line before a bill of supply can be printed")
		}
		if invBlank(it.Description) {
			add(fmt.Sprintf("items[%d].description", i), clauseBoSDescription,
				"a line with no description tells the recipient nothing about what was supplied")
		}
	}

	if inv.Total.IsZero() {
		add("total", clauseBoSValueOfSupply,
			"the value of supply is the amount the recipient is being asked to pay, and a nil value states that nothing was supplied")
	}

	// 49(h), the signature, is discharged by the renderer exactly as 46(q) is.
	return missing
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// invBlank reports whether a mandatory string is effectively absent. A field
// holding a single space is ABSENT: it prints as nothing, so treating it as
// present would put a blank on the page, which is the exact outcome this gate
// exists to prevent.
func invBlank(s string) bool { return strings.TrimSpace(s) == "" }

// invSerialNumberFaults checks the SHAPE of the serial number that Rule 46(b)
// and Rule 49(b) specify, in the same words: not exceeding sixteen characters,
// containing alphabets or numerals or the special characters "-" and "/".
//
// Uniqueness within the financial year is NOT checked here and cannot be — see
// the file comment.
func invSerialNumberFaults(number, clause string) []MissingField {
	trimmed := strings.TrimSpace(number)
	if trimmed == "" {
		return []MissingField{{
			Field:  "number",
			Clause: clause,
			Why:    "an invoice with no serial number cannot be referred to, cannot be reported in a return, and cannot be shown to be one of a consecutive series",
		}}
	}
	var out []MissingField
	// Sixteen CHARACTERS. The rule counts characters, so the count is over runes
	// rather than bytes; a sixteen-character number is legal however many bytes
	// it takes.
	if n := len([]rune(trimmed)); n > invSerialNumberMaxLen {
		out = append(out, MissingField{
			Field:  "number",
			Clause: clause,
			Why:    fmt.Sprintf("the serial number is %d characters long, and the clause allows not more than %d", n, invSerialNumberMaxLen),
		})
	}
	for _, r := range trimmed {
		if invSerialNumberRuneAllowed(r) {
			continue
		}
		out = append(out, MissingField{
			Field:  "number",
			Clause: clause,
			Why:    fmt.Sprintf("the serial number contains %q, and the clause allows only alphabets, numerals, hyphen and slash", r),
		})
		break // one complaint per number; the operator can see the whole string
	}
	return out
}

// invSerialNumberRuneAllowed is the character set of Rule 46(b): alphabets,
// numerals, "-" and "/". Nothing else — a space, a "#" or an accented letter is
// outside the clause even though it would print perfectly well.
func invSerialNumberRuneAllowed(r rune) bool {
	switch {
	case r >= '0' && r <= '9':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= 'a' && r <= 'z':
		return true
	case r == '-' || r == '/':
		return true
	}
	return false
}

// invQuantityParticularsApply decides whether Rule 46(i) — "quantity in case of
// goods and unit or Unique Quantity Code thereof" — applies to a line.
//
// THE CLAUSE IS CONDITIONAL AND THE DOCUMENT MODEL DOES NOT SAY GOODS OR
// SERVICES. LineItem carries no goods/services flag (see doc.go), so the
// condition is inferred: a line with a NON-ZERO QUANTITY is treated as goods and
// must carry its unit; a line with a zero quantity is treated as a service,
// where a quantity and a unique quantity code are not particulars of the supply
// at all — a consultancy fee is not "1 nos".
//
// The inference has a known blind spot, and it is stated rather than hidden: a
// GOODS line keyed with a zero quantity and no unit passes this check. It is the
// safer direction of the two — the alternative refuses every legitimate service
// invoice in the country — and the fix is a goods/services flag on LineItem,
// which is a change to the shared document model.
func invQuantityParticularsApply(it LineItem) bool { return !it.Quantity.IsZero() }

// invHighValueToUnregistered decides whether Rule 46(e) applies: an unregistered
// recipient and a value of fifty thousand rupees or more.
//
// WHICH VALUE IS COMPARED, AND WHY IT IS THE INVOICE TOTAL. The clause says "the
// value of the taxable supply", which read strictly is the taxable value
// (Subtotal) rather than the tax-inclusive total. This uses Total, which is
// never smaller, so the extra particulars are demanded on a slightly WIDER set
// of invoices than the strict reading requires. That direction is deliberate:
// what clause (e) asks for is the recipient's State and its code, which a seller
// almost always has, and demanding it on a Rs 45,000 + tax counter sale is a
// smaller harm than omitting it from one the department reads as crossing the
// threshold. Stated here because it is a judgement call and not a fact.
func invHighValueToUnregistered(inv Invoice) bool {
	if !invBlank(inv.Recipient.GSTIN) {
		return false // a registered recipient: clause (d) applies, not (e)
	}
	return inv.Total.GreaterThanOrEqual(invUnregisteredHighValueThreshold)
}

// invIsInterState decides whether Rule 46(n) applies — whether the supply
// involves inter-State trade or commerce.
//
// TWO SIGNALS, EITHER OF WHICH IS ENOUGH:
//
//   - An IGST head. Integrated tax is charged on an inter-State supply and on
//     nothing else, so its presence settles the question even when the place of
//     supply is exactly the field that is missing. This is the signal that makes
//     the check useful: a document with IGST and a blank place of supply is the
//     defect being hunted, and a check that needed the place of supply in order
//     to decide whether the place of supply was required would never fire on it.
//   - A place-of-supply State code that differs from the supplier's. Present so
//     that a supply mis-keyed with CGST/SGST across State lines is still caught.
//
// A supply with CGST and SGST and no State codes at all is intra-State by both
// signals, and clause (n) does not apply to it — which is correct: the clause is
// conditional, and requiring the place of supply on every intra-State invoice
// would be refusing documents the rule does not require it on.
func invIsInterState(inv Invoice) bool {
	for _, t := range inv.Taxes {
		if strings.EqualFold(strings.TrimSpace(t.Name), "IGST") {
			return true
		}
	}
	supplierState := strings.TrimSpace(inv.Supplier.StateCode)
	posState := strings.TrimSpace(inv.PlaceOfSupplyCode)
	return supplierState != "" && posState != "" && supplierState != posState
}
