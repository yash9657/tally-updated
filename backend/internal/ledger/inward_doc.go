package ledger

// inward_doc.go — THE SUPPLIER'S OWN DOCUMENT, as an input to the voucher path.
//
// Matching a purchase against what the supplier actually filed in their GSTR-1
// (and therefore against our GSTR-2B) needs ONE fact this database did not hold
// before migration 0032: the number the supplier printed on their invoice.
// Three columns looked like they held it and none did —
//
//   - bill_allocation.ref_name is a Tally BILL reference. It exists only when
//     the party ledger maintains bill-by-bill AND the operator typed something,
//     one reference legitimately spans several invoices, and several references
//     legitimately split one invoice into instalments.
//   - voucher_version.voucher_no is OUR numbering, with no UNIQUE constraint and
//     no relation to anything the supplier printed. It currently carries two
//     meanings at once: insightd's OCR mapper writes the SCANNED number into it
//     while a hand-keyed purchase gets an auto serial.
//   - voucher_external_reference (0009) is CHECK-constrained to IRN/FILED_GSTR1
//     and registering a row HARD-FREEZES the voucher against alter and delete.
//     A purchase must stay alterable.
//
// So the supplier's document is its own typed input, written on the version row
// AND into the hashed canonical payload from ONE source inside ONE transaction
// (see ledger.post). The column and the signature cannot disagree, because
// nothing can write one without the other.

import (
	"fmt"
	"time"
)

// ITC eligibility vocabulary. CLOSED, and identical to the CHECK constraints on
// ledger.itc_eligibility and voucher_version.itc_eligibility in migration 0032
// — the Go list and the SQL list are two statements of one rule, and
// TestInwardDocValidationMatchesTheSchemaVocabulary is what keeps them in step.
//
// Note what these values are NOT: they are not derivable from the HSN code, the
// ledger group, or anything else in the voucher. Every section 17(5) clause
// turns on the USE of the supply and on the buyer's own business — the
// identical car is blocked credit for a manufacturer and fully creditable for a
// taxi operator. An engine that guesses either denies real credit or claims
// blocked credit, and both of those are notices. So this is an INPUT.
const (
	// ITCEligible is the ordinary case: the input tax is creditable.
	ITCEligible = "ELIGIBLE"
	// ITCBlocked17_5 is credit blocked by section 17(5). The clause itself goes
	// in ITCBlockedClause as free text.
	ITCBlocked17_5 = "BLOCKED_17_5"
	// ITCCapitalGoods marks credit on capital goods, which rule 43 apportions on
	// its own schedule rather than rule 42's.
	ITCCapitalGoods = "CAPITAL_GOODS"
	// ITCISD marks credit received through an Input Service Distributor, which
	// arrives on its own document type and never through a supplier's B2B row.
	ITCISD = "ISD"
)

// itcEligibilityValues is the vocabulary in declaration order, exported through
// ValidITCEligibility so callers (and migration-vocabulary tests) have exactly
// one list to read.
var itcEligibilityValues = []string{
	ITCEligible, ITCBlocked17_5, ITCCapitalGoods, ITCISD,
}

// ITCEligibilityValues returns the closed eligibility vocabulary.
func ITCEligibilityValues() []string {
	out := make([]string, len(itcEligibilityValues))
	copy(out, itcEligibilityValues)
	return out
}

// InwardDocument is the supplier's document identity and the ITC classification
// for one voucher.
//
// Every field is a plain string, matching CanonicalInward exactly, because the
// hashed payload is built from it verbatim and the JSONB round trip VerifyChain
// performs is only exact for strings. A time.Time here would re-marshal under a
// different layout and declare the book tampered with.
//
// The ZERO VALUE MEANS "no inward detail", and post() writes NOTHING — no
// columns, no payload object — when all four fields are empty. That is what
// keeps a sales voucher's hash byte-identical to what it was before this type
// existed.
type InwardDocument struct {
	// SupplierInvoiceNo is the number the supplier printed, VERBATIM as keyed.
	// It is deliberately NOT normalised on the way in: normalisation lives in
	// exactly one place (itc.NormalizeDocNo, at match time), because a
	// normalisation stated twice drifts, and one baked into the hash could never
	// be corrected afterwards.
	SupplierInvoiceNo string
	// SupplierInvoiceDate is an ISO date (YYYY-MM-DD) or "". It is the
	// SUPPLIER'S date, not ours, and it is statutory: it fixes the financial
	// year for section 16(4) and therefore the date this credit expires. The
	// matcher tolerates zero days of drift on it for precisely that reason — a
	// 31-March invoice and a 1-April one expire a whole year apart.
	SupplierInvoiceDate string
	// ITCEligibility overrides the expense/party ledger's default for this one
	// voucher. "" means "the ledger's default applies", which is NOT the same as
	// ITCEligible: the ledger may say BLOCKED_17_5, and an empty override must
	// not silently overrule it.
	ITCEligibility string
	// ITCBlockedClause is free text, e.g. "17(5)(a)". Unconstrained here and in
	// the schema on purpose: the clause list in circulation is secondary
	// material pending re-verification against the bare Act, and that
	// re-verification must be a documentation change rather than a migration.
	ITCBlockedClause string
}

// IsZero reports whether the document carries nothing at all.
//
// This is the predicate post() uses to decide whether the payload gets an
// "inward" object. It must stay total over the four fields: if a fifth field
// were added and left out of here, a voucher carrying only that field would
// write a column and no signature, and the two would disagree forever.
func (d InwardDocument) IsZero() bool {
	return d.SupplierInvoiceNo == "" &&
		d.SupplierInvoiceDate == "" &&
		d.ITCEligibility == "" &&
		d.ITCBlockedClause == ""
}

// Canonical returns the hashed view of the document.
func (d InwardDocument) Canonical() CanonicalInward {
	return CanonicalInward{
		SupplierInvoiceNo:   d.SupplierInvoiceNo,
		SupplierInvoiceDate: d.SupplierInvoiceDate,
		ITCEligibility:      d.ITCEligibility,
		ITCBlockedClause:    d.ITCBlockedClause,
	}
}

// Validate rejects an inward document the schema would reject anyway, but with
// a message that names the problem.
//
// The point is NOT to duplicate the CHECK constraint for its own sake. It is
// that the CHECK fires inside post()'s transaction, at INSERT time, after the
// numbering counter has been advanced and the postings written — so its error
// surfaces as a rolled-back transaction with a constraint name in it, which
// tells an operator nothing. This fires before any of that.
func (d InwardDocument) Validate() error {
	if d.ITCEligibility != "" {
		ok := false
		for _, v := range itcEligibilityValues {
			if d.ITCEligibility == v {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf(
				"ledger: itc eligibility %q is not one of %v (empty means the ledger's own default applies)",
				d.ITCEligibility, itcEligibilityValues)
		}
	}
	if d.SupplierInvoiceDate != "" {
		// time.DateOnly, parsed strictly. An unparseable date would otherwise
		// reach Postgres as text and be coerced by whatever DateStyle the session
		// happens to carry — and "01-02-2026" coerced under the wrong DateStyle
		// is a REAL date in the WRONG MONTH, which is a section 16(4) expiry a
		// year out with nothing to show that it went wrong.
		if _, err := time.Parse(time.DateOnly, d.SupplierInvoiceDate); err != nil {
			return fmt.Errorf(
				"ledger: supplier invoice date %q must be an ISO date (YYYY-MM-DD): %w",
				d.SupplierInvoiceDate, err)
		}
	}
	// A blocked clause with no BLOCKED_17_5 classification is not rejected: the
	// ledger master may carry the classification while the voucher carries only
	// the clause that applies to this particular bill. Rejecting it here would
	// force the operator to restate a fact the master already holds.
	return nil
}
