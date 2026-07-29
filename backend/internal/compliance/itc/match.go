package itc

import (
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Money
// ---------------------------------------------------------------------------

// Tax is the three GST heads. It exists so this package never depends on
// compliance/gstr for a value type — gstr_adapter.go is the ONLY file here that
// imports gstr, and that one-way edge is what keeps the dependency acyclic once
// the ITC source interface lives in gstr.
//
// Cess is deliberately absent: the books do not model it (gstr3b.go limitation
// 3). The statement's cess IS parsed so it is not lost, but it is never compared
// and never emitted. Disclosure token `cess-not-modelled`.
type Tax struct {
	CGST decimal.Decimal
	SGST decimal.Decimal
	IGST decimal.Decimal
}

// ZeroTax is the additive identity. decimal.Decimal's zero value is already 0,
// but constructing explicitly keeps every accumulator total-order safe and every
// JSON rendering stable.
func ZeroTax() Tax { return Tax{CGST: decimal.Zero, SGST: decimal.Zero, IGST: decimal.Zero} }

// Add returns t + o, head by head.
func (t Tax) Add(o Tax) Tax {
	return Tax{CGST: t.CGST.Add(o.CGST), SGST: t.SGST.Add(o.SGST), IGST: t.IGST.Add(o.IGST)}
}

// Sub returns t - o, head by head.
func (t Tax) Sub(o Tax) Tax {
	return Tax{CGST: t.CGST.Sub(o.CGST), SGST: t.SGST.Sub(o.SGST), IGST: t.IGST.Sub(o.IGST)}
}

// Neg returns -t.
func (t Tax) Neg() Tax {
	return Tax{CGST: t.CGST.Neg(), SGST: t.SGST.Neg(), IGST: t.IGST.Neg()}
}

// PositivePart clamps each head at zero independently. It is used for the
// OVERCLAIM computation (see itcEffect) and nowhere else — a clamp anywhere in
// the availment path would hide a sign bug rather than report it.
func (t Tax) PositivePart() Tax {
	pos := func(d decimal.Decimal) decimal.Decimal {
		if d.IsNegative() {
			return decimal.Zero
		}
		return d
	}
	return Tax{CGST: pos(t.CGST), SGST: pos(t.SGST), IGST: pos(t.IGST)}
}

// Total is CGST + SGST + IGST. Used only for the negative-tax test, never for
// reporting: the three heads are never fungible on a return.
func (t Tax) Total() decimal.Decimal { return t.CGST.Add(t.SGST).Add(t.IGST) }

// IsZero reports whether all three heads are exactly zero.
func (t Tax) IsZero() bool { return t.CGST.IsZero() && t.SGST.IsZero() && t.IGST.IsZero() }

// Equal reports exact per-head equality.
func (t Tax) Equal(o Tax) bool {
	return t.CGST.Equal(o.CGST) && t.SGST.Equal(o.SGST) && t.IGST.Equal(o.IGST)
}

// Abs returns the per-head magnitudes.
func (t Tax) Abs() Tax {
	return Tax{CGST: t.CGST.Abs(), SGST: t.SGST.Abs(), IGST: t.IGST.Abs()}
}

// Scale multiplies every head by n. Used only to flip a statement's reported
// magnitudes into the direction the books recorded them (n = -1 on a note).
func (t Tax) Scale(n int64) Tax {
	m := decimal.NewFromInt(n)
	return Tax{CGST: t.CGST.Mul(m), SGST: t.SGST.Mul(m), IGST: t.IGST.Mul(m)}
}

// RoundingToleranceRupees is the per-head absolute tolerance within which a tax
// difference is MATCHED_ROUNDING rather than MISMATCHED. It applies ONLY when
// the taxable value is already exact to the paisa.
//
// DERIVATION (this is why it is one rupee and not a percentage):
// compliance.TaxDerive rounds PER LINE, at four decimal places. A supplier whose
// system rounds at INVOICE level instead differs from ours by at most half a
// paisa per line per head, so the worst-case divergence is line-count-scaled:
// roughly 0.005 x lines x heads. One rupee covers two hundred lines on a single
// head, which is beyond any realistic tax invoice.
//
// REJECTED: a PERCENTAGE tolerance. A percentage band grows with invoice size,
// which is precisely where a real error hides — a 0.5% band on a one-crore
// invoice silently accepts a fifty-thousand-rupee difference.
//
// REJECTED: any tolerance at all on TAXABLE VALUE. Taxable value is EXACT to
// the paisa for a match — see pairLine, where a non-zero taxable delta names
// TAXABLE_VALUE and forecloses MATCHED_ROUNDING.
var RoundingToleranceRupees = decimal.RequireFromString("1.00")

// ---------------------------------------------------------------------------
// Document identity
// ---------------------------------------------------------------------------

// DocType is the supplier's document type, in GSTN's single-character
// vocabulary: "I" invoice, "C" credit note, "D" debit note.
type DocType string

const (
	DocInvoice    DocType = "I"
	DocCreditNote DocType = "C"
	DocDebitNote  DocType = "D"
)

// Sense collapses the document type to the DIRECTION OF THE ADJUSTMENT it makes
// to input credit: "+" for a document that adds credit (an invoice, or a
// supplier's DEBIT note) and "-" for one that removes it (a supplier's CREDIT
// note). The sense — not the raw type — is what goes into the match key.
//
// WHY THE COARSER KEY IS THE RIGHT ONE HERE, and it is not a shortcut:
//
//	Our books record OUR document, not the supplier's. A supplier's credit note
//	is keyed into these books as a DEBIT note raised on the supplier (a purchase
//	return), which is why store.go's sign rule is "DEBIT_NOTE AND
//	pg.nature='LIABILITY' -> -1". If the raw letter went into the key, every
//	supplier credit note in the country would report as MISSING_IN_BOOKS on one
//	side and MISSING_IN_2B on the other, and the second of those REVERSES ITC.
//	The direction of the adjustment is the one thing both sides record
//	unambiguously, so the direction is what is keyed.
//
// WHAT THIS PRESERVES, which is the whole reason doc type is in the key at all:
// a CDNR credit note is NEVER paired with the invoice it adjusts, even when the
// supplier numbers them identically — "-" can never equal "+". In GSTR-2B they
// are separate documents with their own IMS action, while our own SQL folds
// supplier notes into PURCHASE with sgn = -1, so without this the two would
// collapse into one pair with a plausible-looking zero difference. See
// TestMatch_CreditNoteIsNotPairedToItsInvoice and corpus case 09.
//
// WHAT IT COSTS: an invoice and a supplier DEBIT note with the same number and
// value are not distinguished by the key. They are still reported: when the
// senses agree but the raw letters differ, DOC_TYPE is named in Line.Fields on a
// MATCHED-shaped pair, so a human sees it. A books row cannot currently BE a
// supplier debit note at all (see the disclosed gap in doc.go).
func (d DocType) Sense() string {
	if d == DocCreditNote {
		return "-"
	}
	return "+"
}

// ---------------------------------------------------------------------------
// The two sides
// ---------------------------------------------------------------------------

// BooksRow is one purchase-side voucher as this software recorded it.
//
// EVERY MONEY FIELD IS SIGNED, with store.go's sign convention already applied:
// a supplier note carries NEGATIVE taxable value and negative tax. That is what
// makes the arithmetic in effect.go a single subtraction instead of a case
// analysis, and it is what the 3B aggregation already assumes.
type BooksRow struct {
	VoucherID   uuid.UUID
	VoucherNo   string
	VoucherDate time.Time
	// ParentClass is the RAW voucher_type.parent_class, before any note fold:
	// 'PURCHASE', 'CREDIT_NOTE' or 'DEBIT_NOTE'. DocType is derived from it plus
	// the sign, in store.go, and it is carried here so a report can say which
	// voucher class the operator actually used.
	ParentClass string
	DocType     DocType
	// PartyGSTIN is read from the CURRENT ledger master, not as of the invoice
	// date. See disclosed gap 2 in doc.go.
	PartyGSTIN string
	// SupplierDocNo / SupplierDocDate are the SUPPLIER's own document identity,
	// from voucher_version.supplier_invoice_no / supplier_invoice_date. Empty
	// and zero on every voucher posted before migration 0032 — which is why
	// NOT_COMPARABLE exists and is not the same word as MISSING_IN_2B.
	SupplierDocNo   string
	SupplierDocDate time.Time
	Taxable         decimal.Decimal
	Tax             Tax
	ReverseCharge   bool
	// ITCEligibility is the voucher-level override COALESCEd over the supplier
	// ledger's default: ELIGIBLE, BLOCKED_17_5, CAPITAL_GOODS or ISD.
	ITCEligibility string
	BlockedClause  string
	// HasGSTParams is false when the voucher was posted with no GST params at
	// all (taxable_value IS NULL). Such a voucher is NOT_COMPARABLE, not absent.
	HasGSTParams bool
	// BillRef is bill_allocation.ref_name where ref_type='AGAINST' — the
	// EXISTING, already-populated mechanism that names the original a note was
	// raised against. It is INFORMATIONAL ONLY: it lets the report print "CN-3
	// against INV-17" and it changes no arithmetic whatsoever. A second pairing
	// mechanism is not invented here.
	BillRef string
}

// ITC eligibility classifications. These mirror the CHECK constraint on
// ledger.itc_eligibility and voucher_version.itc_eligibility. The s.17(5) CLAUSE
// LETTER is deliberately NOT in this vocabulary — it is free text, so that
// re-verifying the clause list against the bare Act is a documentation change
// rather than a migration. See doc.go.
const (
	EligibilityEligible = "ELIGIBLE"
	EligibilityBlocked  = "BLOCKED_17_5"
	EligibilityCapital  = "CAPITAL_GOODS"
	EligibilityISD      = "ISD"
)

// StatementRow is one document read out of an ingested GSTR-2A/2B file.
//
// EVERY MONEY FIELD IS THE MAGNITUDE THE FILE REPORTS, always non-negative: 2B
// states a credit note's value positively and carries the direction in the
// document type. Match compares magnitudes; effect.go flips the statement into
// the books' direction before subtracting.
type StatementRow struct {
	ID          uuid.UUID
	StatementID uuid.UUID
	Section     string
	// Rejected is true when the row came from docRejdata. Such rows are
	// VIEW-ONLY (GSTN IMS FAQ 24) and MUST NEVER feed ITC. Summing them is a
	// filing-grade overclaim. See TestRejectedDocumentsNeverEnterITC.
	Rejected             bool
	SupplierGSTIN        string
	SupplierTradeName    string
	SupplierReturnPeriod string
	SupplierFiledDate    time.Time
	DocNo                string
	DocDate              time.Time
	DocType              DocType
	DocValue             decimal.Decimal
	POS                  string
	ReverseCharge        bool
	// ITCAvailable is GSTN's OWN verdict (itcavl). When it is false, GSTN's
	// determination overrides ours — it is the one the department will use — and
	// the credit is excluded from 4(A) entirely and reported in 4(D)(2). A
	// DISAGREEMENT between GSTN's verdict and our own s.16(4) computation is
	// recorded as a FINDING, never silently resolved.
	ITCAvailable         bool
	ITCUnavailableReason string
	Taxable              decimal.Decimal
	Tax                  Tax
	Cess                 decimal.Decimal
	OrigDocNo            string
	OrigDocDate          time.Time
	IMSStatusInFile      string
	IRN                  string
}

// IsAmendment reports whether the row is an amendment section (B2BA, CDNRA,
// ISDA, ECOMA). An amendment is paired to its original on the amendment
// record's OWN (ctin, orig_doc_no, orig_doc_date) — GSTN gives an explicit
// precedence rule and no fuzzy one is invented here.
func (r StatementRow) IsAmendment() bool {
	switch r.Section {
	case SectionB2BA, SectionCDNRA, SectionISDA, SectionECOMA:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Classification vocabulary
// ---------------------------------------------------------------------------

// MatchStatus is the reconciliation verdict on one line.
type MatchStatus string

const (
	// Matched: identity, date and taxable value all exact.
	Matched MatchStatus = "MATCHED"
	// MatchedRounding: as Matched, but a tax head differs by no more than
	// RoundingToleranceRupees. It is a DISTINCT reported status carrying the
	// delta, NOT a silent Matched — the difference is real and somebody may need
	// to explain it, even though it does not change the claim.
	MatchedRounding MatchStatus = "MATCHED_ROUNDING"
	// Mismatched: identity exact (GSTIN + strict key + sense + date), but a
	// compared field differs beyond tolerance. Fields names which.
	Mismatched MatchStatus = "MISMATCHED"
	// NearMatch: a LOOSE-key candidate exists but the strict key or the date
	// differs. IT IS NOT A PAIRING. See the note on Match.
	NearMatch MatchStatus = "NEAR_MATCH"
	// Ambiguous: more than one candidate on one side or the other. NOTHING is
	// paired. See the note on Match.
	Ambiguous MatchStatus = "AMBIGUOUS"
	// MissingInBooks: the statement has the document, the books do not.
	MissingInBooks MatchStatus = "MISSING_IN_BOOKS"
	// MissingIn2B: the books have the voucher, the statement does not — the
	// supplier has not filed it (or filed it in a period we have not ingested).
	MissingIn2B MatchStatus = "MISSING_IN_2B"
	// Superseded: an amendment in the same statement replaces this record, so it
	// contributes nothing.
	Superseded MatchStatus = "SUPERSEDED"
	// NotComparable: the BOOKS row cannot enter matching at all. THIS IS NOT
	// "MISSING". It produces ZERO reversal and is reported as a data-completeness
	// finding. See doc.go.
	NotComparable MatchStatus = "NOT_COMPARABLE"
)

// MismatchField names a field that differed on a MISMATCHED line.
type MismatchField string

const (
	FieldTaxableValue  MismatchField = "TAXABLE_VALUE"
	FieldCGST          MismatchField = "CGST"
	FieldSGST          MismatchField = "SGST"
	FieldIGST          MismatchField = "IGST"
	FieldDocDate       MismatchField = "DOC_DATE"
	FieldDocType       MismatchField = "DOC_TYPE"
	FieldPOS           MismatchField = "POS"
	FieldReverseCharge MismatchField = "REVERSE_CHARGE"
	FieldDocNo         MismatchField = "DOC_NO"
)

// NotComparableReason says WHY a books row could not enter matching. Each of
// these is a data-completeness finding with a different remedy, which is why
// they are distinct constants and not one boolean.
type NotComparableReason string

const (
	// NoSupplierDocNo: the voucher carries no supplier document number. Every
	// purchase posted before migration 0032 is in this state, as is every
	// OCR-prefilled purchase until insightd's mapper is fixed.
	NoSupplierDocNo NotComparableReason = "NO_SUPPLIER_DOC_NO"
	// NoPartyGSTIN: the supplier ledger has no GSTIN, so there is nothing to key
	// against a filer.
	NoPartyGSTIN NotComparableReason = "NO_PARTY_GSTIN"
	// NoGSTParams: the voucher was posted with no GST params at all.
	NoGSTParams NotComparableReason = "NO_GST_PARAMS"
	// ReverseChargeReason: an RCM purchase. IMS excludes RCM supplies (FAQ 8/18)
	// and RCM credit is SELF-ASSESSED, not supplier-filed, so it can never
	// appear in a 2B match and MUST NOT be reversed for being unmatched.
	ReverseChargeReason NotComparableReason = "RCM"
	// ImportOrISD: an import or ISD credit, which does not arrive through the
	// ordinary supplier-filed path.
	ImportOrISD NotComparableReason = "IMPORT_OR_ISD"
)

// FindingCode names a reported observation that is not itself a match verdict.
//
// The vocabulary is CLOSED and SMALL, deliberately. A finding is something a
// human must look at; a list that grows to hold every interesting fact becomes
// a list nobody reads, and then the one finding that mattered is invisible.
// Facts that already change a number — a time bar, a duplicate document number —
// are carried by the STATUS and the EFFECT, which is where a reader will look
// for them, and are not restated here.
type FindingCode string

const (
	// FindingDocNoInvalid: the STRICT key exceeds rule 46(b)'s sixteen
	// characters. It is REPORTED, never a match failure — refusing to reconcile
	// a real invoice because the supplier numbered it badly helps nobody. It is
	// raised for whichever SIDE carries the over-long number, and for both when
	// both do.
	FindingDocNoInvalid FindingCode = "DOC_NO_INVALID"
	// FindingGSTNVerdictDisagrees: GSTN marked the document itcavl="N" while our
	// own s.16(4) computation found nothing wrong, or the reverse. GSTN's
	// verdict is APPLIED — it is the one the department will use — and the
	// DISAGREEMENT is reported here. It is never silently resolved in either
	// direction, because whichever of the two is wrong, somebody needs to know.
	FindingGSTNVerdictDisagrees FindingCode = "GSTN_VERDICT_DISAGREES"
)

// Side names which half of a line a finding is about.
const (
	SideBooks     = "BOOKS"
	SideStatement = "STATEMENT"
)

// Finding is one reported observation, carrying enough evidence to act on
// without going back to the source rows.
type Finding struct {
	Code FindingCode
	// Side is SideBooks or SideStatement, or "" for a finding about the pair as
	// a whole (GSTN_VERDICT_DISAGREES is one).
	Side string
	// DocNo is the document number as recorded, VERBATIM.
	DocNo string
	// StrictKey is what NormalizeDocNo made of it — the thing the length rule
	// is actually applied to, so a reader can see why the finding fired.
	StrictKey string
}

// Candidate is a near-miss pointer carried on a NEAR_MATCH or AMBIGUOUS line so
// a human can confirm or reject it. It never participates in arithmetic.
type Candidate struct {
	// StatementRowID is set when the candidate is on the statement side.
	StatementRowID uuid.UUID
	// VoucherID is set when the candidate is on the books side.
	VoucherID uuid.UUID
	DocNo     string
	DocDate   time.Time
	Taxable   decimal.Decimal
	// Why names the reason it is only a candidate: LOOSE_KEY, DOC_DATE, or
	// DUPLICATE_KEY.
	Why string
}

// TaxDelta is books minus statement, per head, in the BOOKS' direction. It is
// zero on an unpaired line.
type TaxDelta struct {
	Taxable decimal.Decimal
	Tax     Tax
}

// Line is one reconciliation result. Exactly one of the two sides may be nil.
type Line struct {
	Status        MatchStatus
	Fields        []MismatchField
	NotComparable NotComparableReason
	Books         *BooksRow
	Statement     *StatementRow
	Candidates    []Candidate
	Delta         TaxDelta
	Findings      []Finding
	// IMSState is the effective IMS state of the paired statement row, as at
	// Policy.AsOn. It is the empty string when there is no statement row or when
	// IMS does not apply to the section (imports, ISD, RCM).
	IMSState State
	Effect   ITCEffect
}

// Policy carries everything the classification needs that is not on the two
// rows themselves. It is a VALUE: Match is pure and takes the clock as data.
//
// ---------------------------------------------------------------------------
// THERE ARE TWO CLOCKS HERE AND THEY DECIDE DIFFERENT THINGS
// ---------------------------------------------------------------------------
// AsOn is "when is this report being LOOKED AT". ClaimOn is "when was the
// credit CLAIMED". They are different instants and conflating them is a
// catastrophe in one direction only, which is why they are separate fields:
//
//   - AsOn decides the IMS PROJECTION and NOTHING ELSE — which actions have
//     happened yet, and whether an un-actioned record has deem-accepted
//     (EffectiveAction). That genuinely is a function of the moment you ask:
//     a draft 2B looked at on the 10th and on the 20th legitimately differs.
//
//   - ClaimOn decides the s.16(4) TIME-BAR, and only that. s.16(4) asks when
//     the credit was TAKEN, not when a report was printed. Evaluating it
//     against AsOn means that regenerating an already-filed period after the
//     30 November following that document's financial year moves 100% of that
//     period's ITC out of table 4(A) into 4(D)(2) and collapses 4(C) to zero —
//     the same books producing a different, catastrophic answer purely because
//     time passed, on a return the department has already received.
//     TestFiledPeriodReportIsStableWhenRegeneratedYearsLater bites that.
//
// ClaimOn is REQUIRED in the same sense AsOn is: it is never defaulted to
// time.Now() (this package has no clock) and never falls back to AsOn. When a
// caller leaves it zero it is DERIVED from facts already on the Policy — see
// ClaimInstant.
type Policy struct {
	// AsOn is the instant the reconciliation is evaluated at — the moment the
	// report is generated. It is threaded from the handler boundary; nothing in
	// the pure core calls time.Now(). It decides ONLY the IMS projection.
	AsOn time.Time
	// ClaimOn is the instant the credit is treated as CLAIMED, and is the one
	// and only input to the s.16(4) time-bar. It is NOT the report instant. Set
	// it explicitly, or leave it zero and let ClaimInstant derive it from
	// Freeze/Period/Resolution.
	ClaimOn time.Time
	// Period is the tax period being reconciled. It is what an unset ClaimOn is
	// derived from for an UNFILED period (the later of the period's own GSTR-3B
	// due date and AsOn), so a caller that knows its period should always set it.
	Period Period
	// Resolution is the instant NO_ACTION resolves to DEEMED_ACCEPTED for the
	// period being reconciled. Use ResolutionInstant to compute it.
	Resolution time.Time
	// Freeze is gst_return_filing.filed_at for (GSTR3B, period), or nil when the
	// return has not been filed. After it, no IMS action counts — and it is also
	// the instant the credit was claimed, because filing the 3B IS the claim.
	Freeze *time.Time
	// Actions is the append-only IMS action log, keyed by statement document id
	// and ordered by (acted_at, id) within each key.
	Actions map[uuid.UUID][]Action
	// AnnualReturnFiledOn, when set, brings the s.16(4) expiry forward.
	AnnualReturnFiledOn *time.Time
	// RoundingTolerance overrides RoundingToleranceRupees when non-zero.
	RoundingTolerance decimal.Decimal
	// Rule4243Supplied reports whether the CALLER supplied rule 42/43
	// apportionment inputs to the RETURN (gstr.Options3B.ITCPolicy). This
	// package does not hold those inputs and does not compute the formula — see
	// the note in effect.go — but it must know whether they exist, because
	// their absence is the `rule-42-43-not-computed` disclosure, and that
	// disclosure is what stops a frontend presenting the return as final.
	Rule4243Supplied bool
}

// ClaimInstant is the instant the s.16(4) time-bar is evaluated at: WHEN THE
// CREDIT WAS OR COULD FIRST BE TAKEN, never merely when the report was printed.
//
// THE RULE, ONCE, IN FULL. ITC is "taken" when the GSTR-3B for the period is
// FURNISHED — s.16(4) bars a credit taken after the 30 November following the
// document's financial year, and furnishing the return is the taking. So the
// two cases are genuinely different and must behave differently:
//
//   - FILED (Freeze != nil). The claim already happened, on a date that is now
//     history, and the department holds that return. The claim instant is that
//     filing instant and NOTHING may move it. A filed period's 4(A)/4(C) must
//     therefore be byte-stable no matter how many years later the report is
//     regenerated — regenerating it after the bar must not sweep 100% of the
//     period's ITC into 4(D)(2) on a return already received.
//     TestFiledPeriodReportIsStableWhenRegeneratedYearsLater bites that.
//
//   - NOT FILED (Freeze == nil). Nothing has been taken yet. The credit cannot
//     have been claimed earlier than TODAY, so the earliest instant it could now
//     be taken is the later of the period's own 3B due date and the report
//     instant AsOn. An unfiled period's answer SHOULD move as the bar passes:
//     that is the statute operating, not the instability the freeze case fixes.
//     Pinning an unfiled period to its own due date instead would mean the bar
//     could NEVER fire — the due date is ~one month after the period and thus
//     always inside the window — and a filer catching up years late would be
//     told extinguished credit is claimable. That is a straight over-claim:
//     recovery, s.50(3) interest and a s.73/74 notice.
//
// The order, from most to least specific:
//
//  1. An explicit ClaimOn. The caller knows best; it wins over everything.
//  2. Freeze — gst_return_filing.filed_at for this period's GSTR-3B.
//  3. max(GSTR3BDueDate(period), AsOn) for an unfiled period. Period is
//     preferred; when it is unset the period is recovered from Resolution,
//     which is always the 2B instant for the period being reconciled (its
//     month, less one).
//
// If none of those is available the ZERO time is returned, and callers treat
// that as "s.16(4) not evaluated" — the same fail-safe the zero AsOn had before
// this instant existed. Note that AsOn is used ONLY in the unfiled branch and
// only as a FLOOR: it can never pull a filed period's claim instant forward,
// which is the bug this field was introduced to kill.
func (p Policy) ClaimInstant() time.Time {
	if !p.ClaimOn.IsZero() {
		return p.ClaimOn
	}
	if p.Freeze != nil && !p.Freeze.IsZero() {
		return *p.Freeze
	}
	if per, ok := p.claimPeriod(); ok {
		due := GSTR3BDueDate(per)
		if p.AsOn.After(due) {
			return p.AsOn
		}
		return due
	}
	return time.Time{}
}

// claimPeriod recovers the tax period the reconciliation is for.
func (p Policy) claimPeriod() (Period, bool) {
	if p.Period.Year > 0 && p.Period.Month >= time.January && p.Period.Month <= time.December {
		return p.Period, true
	}
	// Resolution is ResolutionInstant(period, genDate): either the computed 14th
	// of the month AFTER the period, or the statement's own generation date,
	// which lands in that same following month. One calendar month back is the
	// period. This is a RECOVERY, not the primary source — a 2B generated more
	// than a month late would recover the wrong month — which is why callers
	// should set Period.
	if !p.Resolution.IsZero() {
		return PeriodOf(p.Resolution).AddMonths(-1), true
	}
	return Period{}, false
}

// tolerance returns the per-head rounding tolerance in force.
func (p Policy) tolerance() decimal.Decimal {
	if p.RoundingTolerance.IsZero() {
		return RoundingToleranceRupees
	}
	return p.RoundingTolerance
}

// ---------------------------------------------------------------------------
// The matcher
// ---------------------------------------------------------------------------

// matchKey is the exact-pairing key: supplier GSTIN, STRICT normalised document
// number, adjustment sense, and document date.
//
// The taxable value is compared but is NOT part of the key: two documents that
// agree on identity and disagree on value are a MISMATCH to report, not two
// unrelated documents.
//
// DATE TOLERANCE IS ZERO DAYS, and the common "+/- 7 days" fuzz is deliberately
// REJECTED. The invoice date is statutory: it fixes the financial year and
// therefore the s.16(4) expiry of the credit. A seven-day band pairs a
// 31-March invoice with a 1-April one — a DIFFERENT FINANCIAL YEAR and a
// different expiry, one year apart. A date-only difference is MISMATCHED with
// field DOC_DATE and both dates printed, so the human decides.
type matchKey struct {
	gstin  string
	strict string
	sense  string
	date   string // ISO date in IST; "" when unknown
}

// looseKey is the CANDIDACY key: the same, without the date and with leading
// zeros stripped from the document number.
type looseKey struct {
	gstin string
	loose string
	sense string
}

func isoDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(IST).Format("2006-01-02")
}

// booksEntry / stmtEntry are the matcher's internal working rows: the public row
// plus its derived keys and a consumed flag. A row consumed by an earlier pass
// LEAVES THE POOL — the three passes are over DISJOINT pools, so no row can be
// paired twice and no later pass can second-guess an earlier one.
type booksEntry struct {
	row      BooksRow
	strict   string
	loose    string
	sense    string
	date     string
	consumed bool
	idx      int
}

type stmtEntry struct {
	row      StatementRow
	strict   string
	loose    string
	sense    string
	date     string
	consumed bool
	idx      int
}

// Match reconciles the books against the statement and returns one Line per
// reconciliation result, deterministically ordered.
//
// THREE PASSES OVER DISJOINT POOLS. Within each pass, candidates on both sides
// are sorted by (doc_date, strict key, id) BEFORE pairing, so the output does
// not depend on the order Postgres happened to return rows in. A reconciliation
// that changes when you re-run it cannot be signed off.
//
//	Pass 1 EXACT           gstin + strictKey + sense + docDate + taxable all
//	                       equal -> MATCHED (or MATCHED_ROUNDING when a tax head
//	                       differs within tolerance).
//	Pass 2 IDENTITY-EXACT  gstin + strictKey + sense + docDate equal, value
//	                       differs -> MISMATCHED, differing fields NAMED and both
//	                       figures carried.
//	Pass 3 CANDIDATE       gstin + looseKey + sense equal, but the strict key or
//	                       the date differs -> NEAR_MATCH.
//
// THE NON-NEGOTIABLE RULE: A NEAR-MATCH IS NEVER A PAIRING. A NEAR_MATCH leaves
// the books row MISSING_IN_2B and the statement row MISSING_IN_BOOKS in
// substance — each line carries a pointer to the candidate for a human to
// confirm — and it contributes NOTHING to the ITC arithmetic on either side.
// A reconciliation that silently pairs two invoices that are not the same
// invoice is WORSE than one that reports them unmatched: the first is a wrong
// number nobody can see, the second is a task on somebody's list.
//
// AMBIGUITY IS AN ERROR-GRADE FINDING, NOT A TIE-BREAK. If pass 1 or pass 2
// finds MORE THAN ONE candidate on either side, NOTHING is paired and every
// participant becomes AMBIGUOUS. Deterministic-but-arbitrary picking — lowest
// id, earliest date, whatever — is how you claim ITC against the wrong invoice
// and never find out.
//
// THE 2B WINDOW IS ON THE SUPPLIER'S FILING DATE, NOT THE DOCUMENT DATE. Match
// therefore NEVER requires a statement row's doc_date to fall inside the
// statement's return period, and never classifies a prior-financial-year invoice
// appearing in a current 2B as a date mismatch. It reports it, as
// FindingPriorPeriodDocument, and matches it normally.
func Match(books []BooksRow, stmt []StatementRow, policy Policy) []Line {
	var lines []Line

	// ------------------------------------------------------------------
	// Step 0. Books rows that CANNOT ENTER MATCHING AT ALL leave first.
	//
	// This is the step whose absence would tell a filer their supplier did not
	// file, when in truth this software never recorded which document it was.
	// ------------------------------------------------------------------
	var bpool []*booksEntry
	for i := range books {
		b := books[i]
		if reason, ok := notComparable(b); ok {
			lines = append(lines, Line{
				Status:        NotComparable,
				NotComparable: reason,
				Books:         cloneBooks(b),
				Delta:         zeroDelta(),
			})
			continue
		}
		strict, loose := NormalizeDocNo(b.SupplierDocNo)
		bpool = append(bpool, &booksEntry{
			row:    b,
			strict: strict,
			loose:  loose,
			sense:  b.DocType.Sense(),
			date:   isoDay(b.SupplierDocDate),
			idx:    i,
		})
	}

	// ------------------------------------------------------------------
	// Step 0b. Statement rows that must never feed ITC leave next.
	//
	// docRejdata rows are VIEW-ONLY (GSTN IMS FAQ 24). They produce NO LINE AT
	// ALL: a line is a reconciliation verdict, and there is no verdict to reach
	// about a record the system refused. They are not in the matching pool
	// either, so they cannot pair with a books row and cannot contribute a rupee
	// to any table — summing them would be a filing-grade overclaim.
	//
	// They are NOT discarded: RejectedRows() lists them and Report carries them,
	// because the operator does need to know a supplier reported something
	// against their GSTIN. Reporting them and scoring them are different things.
	// ------------------------------------------------------------------
	var spool []*stmtEntry
	for i := range stmt {
		s := stmt[i]
		if s.Rejected {
			continue
		}
		strict, loose := NormalizeDocNo(s.DocNo)
		spool = append(spool, &stmtEntry{
			row:    s,
			strict: strict,
			loose:  loose,
			sense:  s.DocType.Sense(),
			date:   isoDay(s.DocDate),
			idx:    i,
		})
	}

	// ------------------------------------------------------------------
	// Step 0c. AMENDMENTS SUPERSEDE THEIR ORIGINALS WITHIN THE STATEMENT.
	//
	// GSTN gives an explicit precedence rule; no fuzzy one is invented. Pairing
	// is on the amendment record's OWN (ctin, orig_doc_no, orig_doc_date). In
	// the SAME statement period only the AMENDED record counts for ITC (IMS
	// advisory kp7 / FAQ 13); for CREDIT NOTES the amendment always prevails
	// (FAQ 14).
	// ------------------------------------------------------------------
	supersededLines := supersedeAmendedOriginals(spool)
	lines = append(lines, supersededLines...)

	// Stable ordering BEFORE any pairing. Everything downstream depends on it.
	sortBooks(bpool)
	sortStmt(spool)

	// ------------------------------------------------------------------
	// Passes 1 and 2 share one grouping by the exact key, because they differ
	// only in what they do once the two sides are known to be the same
	// document. Grouping once also makes the ambiguity test a single count.
	// ------------------------------------------------------------------
	// A ROW CONSUMED BY AN EARLIER STEP LEAVES THE POOL, and the `consumed`
	// test here is what enforces it. Without it a SUPERSEDED original is still
	// in its key's bucket, so the bucket holds two statement rows — the original
	// and the amendment that replaced it — and the whole key is declared
	// AMBIGUOUS. The supplier's correction would then reverse the very credit it
	// was filed to confirm. corpora/gstr2b/13 caught exactly that.
	bByExact := map[matchKey][]*booksEntry{}
	for _, e := range bpool {
		if e.consumed {
			continue
		}
		bByExact[matchKey{e.row.PartyGSTIN, e.strict, e.sense, e.date}] = append(
			bByExact[matchKey{e.row.PartyGSTIN, e.strict, e.sense, e.date}], e)
	}
	sByExact := map[matchKey][]*stmtEntry{}
	for _, e := range spool {
		if e.consumed {
			continue
		}
		sByExact[matchKey{e.row.SupplierGSTIN, e.strict, e.sense, e.date}] = append(
			sByExact[matchKey{e.row.SupplierGSTIN, e.strict, e.sense, e.date}], e)
	}

	keys := make([]matchKey, 0, len(bByExact))
	for k := range bByExact {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return lessKey(keys[i], keys[j]) })

	for _, k := range keys {
		bs := bByExact[k]
		ss := sByExact[k]
		if len(ss) == 0 {
			continue // handled by the leftovers sweep
		}
		if len(bs) > 1 || len(ss) > 1 {
			// AMBIGUOUS: nothing is paired, every participant is reported.
			for _, b := range bs {
				b.consumed = true
				lines = append(lines, ambiguousBooksLine(b, ss))
			}
			for _, s := range ss {
				s.consumed = true
				lines = append(lines, ambiguousStmtLine(s, bs))
			}
			continue
		}
		b, s := bs[0], ss[0]
		b.consumed, s.consumed = true, true
		lines = append(lines, pairLine(b, s, policy))
	}

	// ------------------------------------------------------------------
	// Pass 3 CANDIDATE. Loose-key agreement only. NOTHING IS PAIRED HERE.
	// ------------------------------------------------------------------
	sByLoose := map[looseKey][]*stmtEntry{}
	for _, e := range spool {
		if e.consumed {
			continue
		}
		sByLoose[looseKey{e.row.SupplierGSTIN, e.loose, e.sense}] = append(
			sByLoose[looseKey{e.row.SupplierGSTIN, e.loose, e.sense}], e)
	}
	bByLoose := map[looseKey][]*booksEntry{}
	for _, e := range bpool {
		if e.consumed {
			continue
		}
		bByLoose[looseKey{e.row.PartyGSTIN, e.loose, e.sense}] = append(
			bByLoose[looseKey{e.row.PartyGSTIN, e.loose, e.sense}], e)
	}

	nearBooks := map[*booksEntry][]Candidate{}
	nearStmt := map[*stmtEntry][]Candidate{}
	for lk, bs := range bByLoose {
		ss := sByLoose[lk]
		if len(ss) == 0 {
			continue
		}
		for _, b := range bs {
			for _, s := range ss {
				nearBooks[b] = append(nearBooks[b], Candidate{
					StatementRowID: s.row.ID,
					DocNo:          s.row.DocNo,
					DocDate:        s.row.DocDate,
					Taxable:        s.row.Taxable,
					Why:            candidateWhy(b, s),
				})
				nearStmt[s] = append(nearStmt[s], Candidate{
					VoucherID: b.row.VoucherID,
					DocNo:     b.row.SupplierDocNo,
					DocDate:   b.row.SupplierDocDate,
					Taxable:   b.row.Taxable,
					Why:       candidateWhy(b, s),
				})
			}
		}
	}

	// ------------------------------------------------------------------
	// Leftovers. Everything not consumed by pass 1 or 2 is unpaired: a
	// NEAR_MATCH if it has a candidate, otherwise plainly missing.
	// ------------------------------------------------------------------
	for _, b := range bpool {
		if b.consumed {
			continue
		}
		l := Line{Books: cloneBooks(b.row), Delta: zeroDelta()}
		if c := nearBooks[b]; len(c) > 0 {
			l.Status = NearMatch
			l.Candidates = sortedCandidates(c)
		} else {
			l.Status = MissingIn2B
		}
		l.Findings = booksFindings(b)
		lines = append(lines, l)
	}
	for _, s := range spool {
		if s.consumed {
			continue
		}
		l := Line{Statement: cloneStmt(s.row), Delta: zeroDelta()}
		if c := nearStmt[s]; len(c) > 0 {
			l.Status = NearMatch
			l.Candidates = sortedCandidates(c)
		} else {
			l.Status = MissingInBooks
		}
		l.Findings = stmtFindings(s)
		lines = append(lines, l)
	}

	// ------------------------------------------------------------------
	// Effects, then a stable total order over the whole result.
	// ------------------------------------------------------------------
	for i := range lines {
		lines[i].IMSState = imsState(lines[i].Statement, policy)
		lines[i].Effect = lines[i].itcEffect(policy)
	}
	sortLines(lines)
	return lines
}

// notComparable decides whether a books row can enter matching at all, and why
// not. ORDER MATTERS and is deliberate: the reasons are tested most-specific
// first so the reported reason is the one a human can act on.
func notComparable(b BooksRow) (NotComparableReason, bool) {
	if !b.HasGSTParams {
		return NoGSTParams, true
	}
	if b.ReverseCharge {
		// IMS excludes RCM supplies (FAQ 8/18) and RCM credit is self-assessed,
		// not supplier-filed. An RCM purchase can NEVER appear in a 2B match, so
		// letting it into the pool would reverse 100% of RCM ITC for being
		// unmatched. See TestRCMIsNeverReversedForBeingUnmatched.
		return ReverseChargeReason, true
	}
	if b.ITCEligibility == EligibilityISD {
		return ImportOrISD, true
	}
	if b.PartyGSTIN == "" {
		return NoPartyGSTIN, true
	}
	if b.SupplierDocNo == "" {
		return NoSupplierDocNo, true
	}
	return "", false
}

// supersedeAmendedOriginals consumes any statement row that a LATER amendment in
// the SAME statement replaces, and returns a SUPERSEDED line for it.
func supersedeAmendedOriginals(spool []*stmtEntry) []Line {
	type origKey struct{ gstin, strict, date string }
	amended := map[origKey]*stmtEntry{}
	for _, e := range spool {
		if !e.row.IsAmendment() || e.row.OrigDocNo == "" {
			continue
		}
		strict, _ := NormalizeDocNo(e.row.OrigDocNo)
		amended[origKey{e.row.SupplierGSTIN, strict, isoDay(e.row.OrigDocDate)}] = e
	}
	if len(amended) == 0 {
		return nil
	}
	var out []Line
	for _, e := range spool {
		if e.consumed || e.row.IsAmendment() {
			continue
		}
		k := origKey{e.row.SupplierGSTIN, e.strict, e.date}
		if _, ok := amended[k]; !ok {
			continue
		}
		e.consumed = true
		out = append(out, Line{
			Status:    Superseded,
			Statement: cloneStmt(e.row),
			Delta:     zeroDelta(),
		})
	}
	return out
}

// pairLine builds the line for a pass-1/pass-2 pair: the two rows are the SAME
// document, and the only question is whether the figures agree.
func pairLine(b *booksEntry, s *stmtEntry, policy Policy) Line {
	// The statement reports magnitudes; flip it into the direction the books
	// recorded so a single subtraction answers "did we claim more than the
	// supplier declared?" for invoices and notes alike.
	sgn := int64(1)
	if b.sense == "-" {
		sgn = -1
	}
	stmtTaxable := s.row.Taxable.Abs().Mul(decimal.NewFromInt(sgn))
	stmtTax := s.row.Tax.Abs().Scale(sgn)

	delta := TaxDelta{
		Taxable: b.row.Taxable.Sub(stmtTaxable),
		Tax:     b.row.Tax.Sub(stmtTax),
	}

	l := Line{
		Books:     cloneBooks(b.row),
		Statement: cloneStmt(s.row),
		Delta:     delta,
	}

	var fields []MismatchField
	// TAXABLE VALUE IS EXACT TO THE PAISA FOR A MATCH. NO BAND. A percentage
	// band grows with invoice size, which is precisely where a real error hides.
	if !delta.Taxable.IsZero() {
		fields = append(fields, FieldTaxableValue)
	}
	tol := policy.tolerance()
	overTol := false
	if !delta.Tax.CGST.IsZero() {
		fields = append(fields, FieldCGST)
		overTol = overTol || delta.Tax.CGST.Abs().GreaterThan(tol)
	}
	if !delta.Tax.SGST.IsZero() {
		fields = append(fields, FieldSGST)
		overTol = overTol || delta.Tax.SGST.Abs().GreaterThan(tol)
	}
	if !delta.Tax.IGST.IsZero() {
		fields = append(fields, FieldIGST)
		overTol = overTol || delta.Tax.IGST.Abs().GreaterThan(tol)
	}
	// The senses agree (they are in the key) but the raw letters may not — an
	// invoice against a supplier DEBIT note. Named so a human sees it.
	if b.row.DocType != s.row.DocType {
		fields = append(fields, FieldDocType)
	}
	if b.row.ReverseCharge != s.row.ReverseCharge {
		fields = append(fields, FieldReverseCharge)
	}

	switch {
	case len(fields) == 0:
		l.Status = Matched
	case delta.Taxable.IsZero() && !overTol && onlyTaxHeads(fields):
		// MATCHED_ROUNDING: taxable exact, every tax head within the absolute
		// per-head tolerance. It is a DISTINCT status carrying the delta, not a
		// silent MATCHED, and ITC is taken at the BOOKS figure.
		l.Status = MatchedRounding
		l.Fields = sortedFields(fields)
	default:
		l.Status = Mismatched
		l.Fields = sortedFields(fields)
	}

	l.Findings = pairFindings(b, s, policy)
	return l
}

// onlyTaxHeads reports whether every named field is one of the three tax heads —
// the precondition for MATCHED_ROUNDING. A DOC_TYPE or REVERSE_CHARGE difference
// is never "rounding".
func onlyTaxHeads(fs []MismatchField) bool {
	for _, f := range fs {
		if f != FieldCGST && f != FieldSGST && f != FieldIGST {
			return false
		}
	}
	return true
}

func ambiguousBooksLine(b *booksEntry, ss []*stmtEntry) Line {
	var cands []Candidate
	for _, s := range ss {
		cands = append(cands, Candidate{
			StatementRowID: s.row.ID, DocNo: s.row.DocNo, DocDate: s.row.DocDate,
			Taxable: s.row.Taxable, Why: "DUPLICATE_KEY",
		})
	}
	return Line{Status: Ambiguous, Books: cloneBooks(b.row),
		Candidates: sortedCandidates(cands), Delta: zeroDelta(), Findings: booksFindings(b)}
}

func ambiguousStmtLine(s *stmtEntry, bs []*booksEntry) Line {
	var cands []Candidate
	for _, b := range bs {
		cands = append(cands, Candidate{
			VoucherID: b.row.VoucherID, DocNo: b.row.SupplierDocNo,
			DocDate: b.row.SupplierDocDate, Taxable: b.row.Taxable, Why: "DUPLICATE_KEY",
		})
	}
	return Line{Status: Ambiguous, Statement: cloneStmt(s.row),
		Candidates: sortedCandidates(cands), Delta: zeroDelta(), Findings: stmtFindings(s)}
}

// candidateWhy names why a loose-key agreement is only a candidate.
func candidateWhy(b *booksEntry, s *stmtEntry) string {
	if b.strict != s.strict {
		return "LOOSE_KEY"
	}
	return "DOC_DATE"
}

// docNoFinding raises FindingDocNoInvalid when a strict key exceeds rule
// 46(b)'s sixteen characters, for one side.
func docNoFinding(side, docNo, strict string) []Finding {
	if len(strict) <= MaxInvoiceNoLen {
		return nil
	}
	return []Finding{{Code: FindingDocNoInvalid, Side: side, DocNo: docNo, StrictKey: strict}}
}

// booksFindings collects the observations about an unpaired books row.
func booksFindings(b *booksEntry) []Finding {
	return docNoFinding(SideBooks, b.row.SupplierDocNo, b.strict)
}

// stmtFindings collects the observations about an unpaired statement row.
func stmtFindings(s *stmtEntry) []Finding {
	return docNoFinding(SideStatement, s.row.DocNo, s.strict)
}

// pairFindings collects observations about a paired line, including the one
// disagreement that must never be silently resolved.
func pairFindings(b *booksEntry, s *stmtEntry, policy Policy) []Finding {
	out := append(docNoFinding(SideStatement, s.row.DocNo, s.strict),
		docNoFinding(SideBooks, b.row.SupplierDocNo, b.strict)...)

	// GSTN's verdict is the one the department will use, so it is APPLIED
	// (effect.go). A DISAGREEMENT with our own s.16(4) computation is REPORTED
	// here and never silently resolved in either direction: whichever of the two
	// is wrong, the filer needs to know before they sign the return.
	//
	// The condition reads oddly and is exact: ITCAvailable == ourBar is true in
	// precisely the two disagreeing cases — itcavl="Y" while we say time-barred
	// (true == true) and itcavl="N" while we say the credit is fine
	// (false == false).
	//
	// THE BAR IS EVALUATED AT ClaimInstant, NOT AT AsOn. Against AsOn this
	// finding fires on every paired row of an already-filed period the moment
	// the report is regenerated past 30 November — telling the filer GSTN and
	// this software disagree about a return that was filed years ago, when
	// nothing disagrees and nothing has changed but the calendar.
	claimOn := policy.ClaimInstant()
	ourBar := !b.row.SupplierDocDate.IsZero() && !claimOn.IsZero() &&
		Sec16_4Barred(b.row.SupplierDocDate, claimOn, policy.AnnualReturnFiledOn)
	if s.row.ITCAvailable == ourBar {
		out = append(out, Finding{Code: FindingGSTNVerdictDisagrees})
	}
	return out
}

// RejectedRows returns the statement rows that came from docRejdata. They are
// VIEW-ONLY (GSTN IMS FAQ 24): they never enter matching and never contribute a
// rupee to any table, but the operator must be able to SEE that a supplier
// reported something against their GSTIN.
func RejectedRows(stmt []StatementRow) []StatementRow {
	var out []StatementRow
	for _, r := range stmt {
		if r.Rejected {
			out = append(out, r)
		}
	}
	return out
}

// imsState projects the IMS action log for a statement row, honouring the
// sections IMS does not cover.
func imsState(s *StatementRow, policy Policy) State {
	if s == nil || s.Rejected {
		return ""
	}
	if !IMSApplies(s.Section, s.ReverseCharge) {
		return ""
	}
	return EffectiveAction(policy.Actions[s.ID], policy.Resolution, policy.Freeze, policy.AsOn)
}

// ---------------------------------------------------------------------------
// Determinism helpers. Every one of these exists so that two runs over the same
// data produce byte-identical output.
// ---------------------------------------------------------------------------

func sortBooks(p []*booksEntry) {
	sort.SliceStable(p, func(i, j int) bool {
		if p[i].date != p[j].date {
			return p[i].date < p[j].date
		}
		if p[i].strict != p[j].strict {
			return p[i].strict < p[j].strict
		}
		return p[i].row.VoucherID.String() < p[j].row.VoucherID.String()
	})
}

func sortStmt(p []*stmtEntry) {
	sort.SliceStable(p, func(i, j int) bool {
		if p[i].date != p[j].date {
			return p[i].date < p[j].date
		}
		if p[i].strict != p[j].strict {
			return p[i].strict < p[j].strict
		}
		return p[i].row.ID.String() < p[j].row.ID.String()
	})
}

func lessKey(a, b matchKey) bool {
	if a.gstin != b.gstin {
		return a.gstin < b.gstin
	}
	if a.date != b.date {
		return a.date < b.date
	}
	if a.strict != b.strict {
		return a.strict < b.strict
	}
	return a.sense < b.sense
}

func sortedFields(fs []MismatchField) []MismatchField {
	out := append([]MismatchField(nil), fs...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedCandidates(cs []Candidate) []Candidate {
	out := append([]Candidate(nil), cs...)
	sort.Slice(out, func(i, j int) bool {
		if !out[i].DocDate.Equal(out[j].DocDate) {
			return out[i].DocDate.Before(out[j].DocDate)
		}
		if out[i].DocNo != out[j].DocNo {
			return out[i].DocNo < out[j].DocNo
		}
		return out[i].StatementRowID.String()+out[i].VoucherID.String() <
			out[j].StatementRowID.String()+out[j].VoucherID.String()
	})
	return out
}

// sortLines gives the whole result a total order that does not depend on the
// order the passes happened to append in.
func sortLines(ls []Line) {
	key := func(l Line) (string, string, string) {
		var date, docno, id string
		if l.Books != nil {
			date = isoDay(l.Books.SupplierDocDate)
			if date == "" {
				date = isoDay(l.Books.VoucherDate)
			}
			docno = l.Books.SupplierDocNo
			if docno == "" {
				docno = l.Books.VoucherNo
			}
			id = l.Books.VoucherID.String()
		} else if l.Statement != nil {
			date = isoDay(l.Statement.DocDate)
			docno = l.Statement.DocNo
			id = l.Statement.ID.String()
		}
		return date, docno, id
	}
	sort.SliceStable(ls, func(i, j int) bool {
		di, ni, ii := key(ls[i])
		dj, nj, ij := key(ls[j])
		if di != dj {
			return di < dj
		}
		if ni != nj {
			return ni < nj
		}
		return ii < ij
	})
}

func cloneBooks(b BooksRow) *BooksRow        { c := b; return &c }
func cloneStmt(s StatementRow) *StatementRow { c := s; return &c }
func zeroDelta() TaxDelta {
	return TaxDelta{Taxable: decimal.Zero, Tax: ZeroTax()}
}
