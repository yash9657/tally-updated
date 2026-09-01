// gstr3b.go generates the GSTR-3B monthly summary return from the same posted,
// immutable voucher log GSTR-1 reads. It is a pure read path: it re-aggregates
// the denormalised GST columns the ledger stamps on Sales AND Purchase
// voucher_versions (`gstActive := in.GST != nil && (in.Class == Sales ||
// in.Class == Purchase)` — ledger.go:583; the columns are assigned at
// ledger.go:820-827 and INSERTed at ledger.go:830-840), so the outward half
// comes from `vt.parent_class = 'SALES'` rows and the ITC half from
// `vt.parent_class = 'PURCHASE'` rows.
//
// Generate3B deliberately issues its OWN SQL rather than delegating to
// Service.Generate: the "3.1(a) == GSTR-1 tax totals" consistency gate is only
// an independent oracle if the two paths are independently computed. Do not
// "simplify" this by re-summing a GSTR1 value.
//
// Tables produced (RESEARCH.md sec 19.1d enumerates the real 3B tables):
//
//   - 3.1(a) osup_det   — outward taxable supplies, from SALES rows.
//   - 3.1(b) osup_zero  — zero-rated (export/SEZ): ALWAYS ZERO in v1; the books
//     carry no export/SEZ marker to classify on.
//   - 3.1(c) osup_nil_exmp — nil-rated/exempt: ALWAYS ZERO in v1 (same reason).
//   - 3.1(d) isup_rev   — inward supplies liable to reverse charge, from
//     PURCHASE rows with is_reverse_charge = true.
//   - 3.1(e) osup_nongst — non-GST outward: ALWAYS ZERO in v1 (no marker).
//   - 3.2 inter_sup     — inter-state supplies to UNREGISTERED recipients, by
//     place of supply. GSTN validation requires 3.2 <= 3.1(a); Generate3B
//     enforces that structurally (see Validate).
//   - 4 itc_elg         — ITC. 4(A) "ITC Available" is re-summed from the
//     PURCHASE-class GST columns and routed into the GSTN row codes
//     (IMPG/IMPS/ISRC/ISD/OTH); 4(B) "ITC Reversed" (RUL = rule 42/43 and
//     section 17(5), OTH = everything else) and 4(D)(2) "ineligible" come from
//     the optional ITCSource. 4(C) net = sum(4A) - sum(4B).
//
// # THE ITC MODEL — 4(A) IS WHAT THE BOOKS POSTED, THE HAIRCUT LANDS IN 4(B)
//
// There are two defensible ways to report ITC when the supplier has not filed.
// GSTN's own auto-population (Circular 170/02/2022) puts the GSTR-2B figure in
// 4(A) and expects the filer to reconcile. WE DO NOT. 4(A) here is what the
// BOOKS posted to the input-tax ledgers, and every shortfall is a RETURN-SIDE
// reversal in 4(B)(2). The reason is that this preserves ONE exact, always-true
// invariant, and an exact invariant is what makes a wrong number DETECTABLE:
//
//	(I1)  sum(4A) + sum(4D)  ==  the input-tax POSTINGS for the period,
//	      head by head. Enforced by Reconciliation3B.BalancedBooks().
//	(I2)  sum(4A) - sum(4B)  ==  4C, head by head. Enforced by Validate().
//
// (I1) is why time-barred / place-of-supply-restricted credit is EXCLUDED FROM
// 4(A) ENTIRELY and reported in 4(D)(2) instead of being reversed: every rupee
// of posted input tax must land in EXACTLY ONE of 4(A) or 4(D). GSTN takes the
// same view — it marks such a document itcavl="N" and keeps it out of the "ITC
// Available" section rather than putting it in and reversing it.
//
// A FILER COMPARING AGAINST THE PORTAL'S PRE-FILLED 4(A) WILL SEE A DIFFERENT
// 4(A) WITH THE SAME 4(C). That divergence is deliberate and is disclosed here
// rather than hidden.
//
// 4(D) IS NOT SUBTRACTED FROM 4(C). Since Notification 14/2022 rewrote the
// table, 4(D) is "Other Details" and is INFORMATIONAL; section 17(5) is
// reversed through 4(B)(1). Subtracting 4(D) as well double-counts the haircut
// and understates claimable ITC.
//
// RECONCILIATION NEVER WRITES TO THE BOOKS. The 4(B) reversal is a RETURN-SIDE
// line only; the corresponding book entry (Dr Expense / Cr Input tax) is NOT
// posted by this software and the filer must pass it. Auto-posting it would
// make the books a FUNCTION of the supplier's filing — and then a wrong
// statement is undetectable — and it would destroy (I1) permanently. Emitted as
// the disclosure token `reversals-not-posted-to-books`.
//
// # DISCLOSED v1 LIMITATIONS (do not remove without replacing the behaviour)
//
//  1. ITC ELIGIBILITY IS ONLY AS GOOD AS THE SUPPLIED ITCSource. With
//     opts.ITC == nil every posted input-tax rupee is reported as available and
//     nothing is reversed — the pre-2A/2B behaviour, preserved BYTE-FOR-BYTE so
//     that turning the seam on is the only thing that can change a number. What
//     is NEVER done, at any setting: table 4(D)(1) "ITC reclaimed which was
//     reversed in an earlier period" is not emitted (there is no
//     earlier-period reversal store, and a carry-in is not a function of this
//     period's postings, so emitting it would break (I1)); rule 42/43 inputs
//     are never DERIVED — E (exempt supply value) and F (turnover in the
//     State) must be supplied by the caller, because an engine fed E = 0
//     returns a confidently wrong ZERO reversal for every taxpayer; and
//     section 17(5) is a CLASSIFICATION INPUT, never inferred from an HSN —
//     every 17(5) clause turns on USE and on the buyer's own business, so the
//     identical car is blocked for us and fully creditable for a taxi
//     operator. Matching is at INVOICE grain, not per-HSN/per-rate: a
//     multi-rate invoice whose per-rate split is wrong still matches. Every one
//     of these is reported in the `disclosures` field rather than left to a
//     reader of this comment.
//  2. FIELD NAMES ARE NOT VERIFIED AGAINST THE GSTN 3B OFFLINE-UTILITY SCHEMA.
//     No copy of that schema exists in this repo (internal/statutory carries
//     only the NIC e-INVOICE schema; RESEARCH.md sec 19.1d documents the TABLES,
//     not the JSON keys). Known likely divergences: `fp` vs the utility's
//     `ret_period`, and `inter_sup` being an ARRAY here versus an OBJECT
//     (`{unreg_details:[], comp_details:[], uin_details:[]}`) in the utility.
//     This output is a books-derived summary; PORTAL-UPLOAD COMPATIBILITY IS
//     NOT CLAIMED.
//  3. CESS IS NOT MODELLED anywhere in the books, so no `csamt` field is
//     emitted.
//  4. VOUCHERS POSTED WITHOUT GST PARAMS have taxable_value NULL and are
//     EXCLUDED (the `taxable_value IS NOT NULL` filter, inherited from GSTR-1).
//     A company that posts non-GST sales will therefore see 3.1(a) below its
//     P&L turnover.
//  5. RCM OUTWARD SUPPLIES: compliance.TaxDerive still returns non-zero
//     CGST/SGST/IGST under ReverseCharge (tax.go:109-160; only InvoiceTotal is
//     reduced), and those amounts are posted to the output-tax ledgers and
//     included by GSTR1.TaxTotals(). 3.1(a) therefore includes supplier-side RCM
//     tax, which is arguably wrong under GST law (the recipient discharges it).
//     It is included ON PURPOSE so 3.1(a) stays equal to the GSTR-1 rollup and
//     to the posted output tax; fixing it requires changing the outward side of
//     GSTR-1 too.
//  6. INTER-STATE IS DERIVED AS `igst > 0`, exactly as GSTR-1 does
//     (invoice.intra, gstr1.go:296-298), NOT from a supplier state code. A
//     0%-rated INTER-state supply has igst = 0 and is therefore classified
//     intra and omitted from 3.2. This is pre-existing GSTR-1 behaviour, not
//     introduced here; sharing it is what keeps 3.2 consistent with the B2CL
//     classification.
//  7. A SUPPLIER CREDIT NOTE THAT IS IN THE STATEMENT AND NOT IN THE BOOKS IS
//     NOT NETTED OUT, so table 4(C) is OVERSTATED by the tax on it. Under model
//     (b) 4(A) is what the books POSTED, and an unrecorded document posted
//     nothing; the reconciliation classifies the row MISSING_IN_BOOKS and it
//     moves no figure. The asymmetry is the danger: an unmatched BOOKS INVOICE
//     is fully reversed in 4(B)(2), so the error that REDUCES the claim is
//     caught and the one that INFLATES it is not. Under IMS an un-actioned
//     credit note deem-accepts and is already netted out of the recipient's
//     GSTR-2B, so the department's own auto-populated 4(C) will be LOWER than
//     this one.
//     THE FIX IS NOT AN ITCDecision AND MUST NOT BE MADE ONE: there is no
//     voucher to hang it on, so giving the row a 4(B)(2) reversal breaks
//     checkITCDecision rule (1) (Available + Ineligible == posted input tax).
//     It has to ride as a PERIOD-LEVEL adjustment in build3B alongside the
//     rule 42/43 component, and that is NOT BUILT. What exists today is the
//     disclosure: itc raises FindingStatementCreditNoteNotInBooks per row and
//     the `statement-credit-note-not-in-books` token per reconciliation.
//  8. A NEGATIVE 4(B)(1) "RUL" ROW IS EMITTED when a section 17(5) blocked
//     purchase is RETURNED in a period with no other 17(5) reversal to absorb
//     it. The figure is CORRECT — it is what makes the blocked document net to
//     zero rather than destroying credit the taxpayer never took — but whether
//     the GSTN portal ACCEPTS a negative value in that field is UNVERIFIED (no
//     copy of the offline-utility schema exists in this repo; see limitation
//     2). Covered by the standing `portal-schema-unverified` disclosure.
//
// NONE OF THE ABOVE IS FIXED BY THIS FILE. The output is NOT FILING-READY, and
// every generated return says so in its machine-readable `disclosures` field
// (`not-filing-ready`) rather than relying on a reader of this comment.
package gstr

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// Options3B configures GSTR-3B generation. It embeds the GSTR-1 Options (GSTIN
// plus the three OUTPUT-tax ledger ids used for the outward posting
// reconciliation) and adds the three INPUT-tax ledger ids needed for the ITC
// side, which have no analogue on Options.
type Options3B struct {
	Options
	// InputCGSTLedgerID, InputSGSTLedgerID, InputIGSTLedgerID identify the
	// INPUT-tax (ITC) ledgers. They drive the table-4 posting reconciliation in
	// Reconcile3B and may be left nil when reconciliation is not required.
	InputCGSTLedgerID uuid.UUID
	InputSGSTLedgerID uuid.UUID
	InputIGSTLedgerID uuid.UUID

	// ITC supplies the per-voucher ITC decisions (2A/2B match + IMS + section
	// 17(5) classification). A NIL ITC is the supported default and reproduces
	// the pre-reconciliation output exactly: every posted input-tax rupee
	// available, nothing reversed. It is an INTERFACE rather than a
	// pre-resolved map because the implementing package (compliance/itc)
	// imports THIS package for TaxTotals; a map-typed field would need gstr to
	// import itc and close the cycle.
	ITC ITCSource
	// ITCPolicy carries the period-level inputs that are not per-voucher: the
	// caller-supplied rule 42/43 figures and any disclosure tokens the
	// statement-ingestion layer needs to surface. Nil is supported.
	ITCPolicy *ITCPolicy
}

// ITCBucket is a GSTN table-4(A) row code.
//
//	IMPG — import of goods              IMPS — import of services
//	ISRC — inward supplies liable to reverse charge
//	ISD  — inward supplies from an Input Service Distributor
//	OTH  — all other ITC
type ITCBucket string

// The five 4(A) row codes, in the order GSTN prints them.
const (
	BucketIMPG ITCBucket = "IMPG"
	BucketIMPS ITCBucket = "IMPS"
	BucketISRC ITCBucket = "ISRC"
	BucketISD  ITCBucket = "ISD"
	BucketOTH  ITCBucket = "OTH"
)

// itc4ARowOrder is the emit order of table 4(A). Adding a row code is one entry
// here plus one constant above — which is the whole point of keying the
// accumulators rather than carrying a boolean per bucket.
var itc4ARowOrder = []ITCBucket{BucketIMPG, BucketIMPS, BucketISRC, BucketISD, BucketOTH}

// ITCDecision is what the reconciliation concluded about ONE voucher's input
// tax. It is a PARTITION of that voucher's posted input tax, not an opinion
// about it:
//
//	Available + Ineligible  ==  the voucher's posted input tax, head by head.
//
// build3B ENFORCES that equality and refuses to generate a return from a
// decision that violates it (see errStaleITCDecision). The check is what makes
// (I1) structurally exact instead of merely hoped for, and it is also the
// version guard: a decision resolved against a SUPERSEDED voucher version
// carries the superseded version's tax, so an ALTER that moves the taxable
// value makes the stale decision fail this equality instead of silently
// reversing the wrong amount.
//
// Reversals are a SUBSET of Available (you cannot reverse credit you never
// took), at most one of the two kinds per voucher, and NEVER negative — a
// document whose posted input tax is negative (a supplier credit note) is never
// reversed for being absent from the statement, because reversing a negative
// would ADD credit.
type ITCDecision struct {
	// Bucket is the 4(A) row this voucher's available credit belongs in.
	// The empty string means OTH.
	Bucket ITCBucket
	// Available is the part of the posted input tax that enters 4(A).
	Available TaxTotals
	// ReversedRules is the 4(B)(1) reversal: rule 42/43 apportionment and
	// section 17(5) blocked credit.
	ReversedRules TaxTotals
	// ReversedOther is the 4(B)(2) reversal: unmatched, mismatched-in-excess,
	// IMS-rejected/pending — everything that is not a rule reversal.
	ReversedOther TaxTotals
	// Ineligible is the 4(D)(2) figure: credit that was NEVER available
	// (section 16(4) time bar, the place-of-supply restriction). It is
	// EXCLUDED from 4(A) rather than reversed out of it.
	Ineligible TaxTotals
	// Reason is a short human-readable explanation, carried for reporting only.
	// It never affects arithmetic.
	Reason string
}

// ITCSource resolves the per-voucher ITC decisions for a period. The map is
// keyed by voucher_id (NOT version id): there is exactly one current version
// per voucher, and staleness is caught by the Available+Ineligible identity
// above rather than by the key.
type ITCSource interface {
	Decisions(ctx context.Context, from, to string) (map[uuid.UUID]ITCDecision, error)
}

// Rule42Inputs are the CALLER-SUPPLIED inputs to the rule 42 apportionment of
// common credit. They are never derived from our own return, and that is a
// decision, not a gap: gstr3b.go hard-zeroes 3.1(b)/(c)/(e) because "the books
// carry no export/SEZ marker to classify on", so E is exactly the number this
// software does not have, and F (total turnover IN THE STATE) is not modelled
// at all. An engine that quietly substituted E = 0 would return D1 = 0 for
// EVERY taxpayer and look like it worked.
type Rule42Inputs struct {
	// C2 is the common credit remaining after C1 - T4 (rule 42(1)(f)).
	C2 TaxTotals
	// E is the aggregate value of exempt supplies during the tax period.
	E decimal.Decimal
	// F is the total turnover in the State during the tax period.
	F decimal.Decimal
}

// Rule43Inputs are the CALLER-SUPPLIED inputs to the rule 43 apportionment of
// capital-goods credit. Same input boundary as Rule42Inputs.
type Rule43Inputs struct {
	// Tr is the common credit on capital goods for the tax period (rule 43(1)(c),
	// Tm/Tr aggregated by the caller).
	Tr TaxTotals
	// E is the aggregate value of exempt supplies during the tax period.
	E decimal.Decimal
	// F is the total turnover in the State during the tax period.
	F decimal.Decimal
}

// ITCPolicy carries the period-level ITC inputs. Every field is optional; a nil
// or empty policy produces no rule-42/43 component and the disclosure token
// `rule-42-43-not-computed`.
type ITCPolicy struct {
	Rule42 *Rule42Inputs
	Rule43 *Rule43Inputs
	// SourceDisclosures are tokens the statement-ingestion layer contributes
	// because gstr cannot know them (e.g. `statement-partially-parsed`). They
	// MUST come from the closed vocabulary in KnownDisclosures; an unknown
	// token is an error, because a disclosure a frontend does not recognise is
	// a disclosure that will not be shown.
	SourceDisclosures []string
}

// GSTR3B is the generated 3B summary for one tax period.
//
// The JSON field names below follow the plan's pinned names and GSTN naming
// conventions but are NOT verified against the GSTN 3B offline-utility schema —
// see limitation (2) in the file doc comment. Treat this as a books-derived
// summary, not a validated upload artifact. ITC is reported ALL-ELIGIBLE
// (limitation (1)).
type GSTR3B struct {
	// GSTIN of the supplier (filer).
	GSTIN string `json:"gstin,omitempty"`
	// FP is the filing period in "MMYYYY" form, e.g. "052025" for May 2025.
	FP string `json:"fp"`
	// T31 is table 3.1 (outward supplies + inward RCM), rows a..e.
	T31 Table31 `json:"sup_details"`
	// T32 is table 3.2: inter-state supplies to unregistered persons by place of
	// supply. v1 emits the unregistered bucket only (no composition/UIN split).
	T32 []Table32Row `json:"inter_sup"`
	// T4 is table 4: ITC available, reversed, net and ineligible.
	T4 Table4ITC `json:"itc_elg"`
	// Disclosures names every way this return is NOT filing-ready, over the
	// closed vocabulary in KnownDisclosures, sorted and deduplicated.
	//
	// It is a FIELD rather than prose in a doc comment for one reason: a
	// frontend cannot read a doc comment, so a limitation that lives only in
	// this file cannot stop the UI presenting the return as final. It is
	// declared LAST so that the rest of the document's bytes are unchanged
	// from the pre-ITC-seam shape (see TestBuild3B_NilITCSourceIsByteIdentical).
	Disclosures []string `json:"disclosures,omitempty"`
	// Carried names every value the no-negative rule (see clampNegatives)
	// removed from tables 3.1 and 3.2, as a POSITIVE magnitude: the amount the
	// filer must adjust against a subsequent period's outward supply.
	//
	// IT IS THE HALF OF THE CLAMP THAT MAKES THE CLAMP HONEST. Zeroing a
	// negative on its own silently destroys taxable value and tax — the filer
	// loses the credit note and has nothing on the return telling them so. It
	// carries `omitempty` and is declared after Disclosures for the same reason
	// that field is declared last: a book that never goes negative marshals to
	// exactly the bytes it did before this field existed.
	Carried []CarriedForward `json:"carried_forward,omitempty"`
}

// CarriedForward is one value the no-negative rule removed from a table.
//
// Amounts are POSITIVE MAGNITUDES — "12000 of taxable value to adjust later" —
// not the negatives that were clamped. A reader who sees a negative here would
// reasonably add it to the table it names and get the unfilable figure back.
type CarriedForward struct {
	// Table is the GSTN table the value was removed from: "3.1(a)", "3.1(d)",
	// or "3.2:27" for a table-3.2 place-of-supply bucket.
	Table string `json:"table"`
	// Txval, Iamt, Camt and Samt are the magnitudes clamped out of each of the
	// row's four values. A value that was not negative is zero here.
	Txval decimal.Decimal `json:"txval"`
	Iamt  decimal.Decimal `json:"iamt"`
	Camt  decimal.Decimal `json:"camt"`
	Samt  decimal.Decimal `json:"samt"`
}

// The closed disclosure vocabulary. A token that is not in this list is a
// programming error, not a message: the frontend switches on these strings, so
// an unrecognised one is silently invisible — which is the exact failure this
// field exists to prevent.
const (
	// DiscNoInwardStatement — no GSTR-2A/2B was supplied, so nothing in table 4
	// has been matched against what the suppliers actually filed.
	DiscNoInwardStatement = "no-inward-statement"
	// DiscRule4243NotComputed — rule 42/43 apportionment inputs were not
	// supplied, so no apportionment component is in 4(B)(1).
	DiscRule4243NotComputed = "rule-42-43-not-computed"
	// DiscITCEligibilityNotClassified — at least one purchase carried no ITC
	// decision at all and was reported fully available by default.
	DiscITCEligibilityNotClassified = "itc-eligibility-not-classified"
	// Disc4D1ReclaimNotModelled — table 4(D)(1) is never emitted; there is no
	// earlier-period reversal store to reclaim from.
	Disc4D1ReclaimNotModelled = "4d1-reclaim-not-modelled"
	// DiscReversalsNotPostedToBooks — the 4(B) reversal is return-side only;
	// the filer must pass the corresponding book entry.
	DiscReversalsNotPostedToBooks = "reversals-not-posted-to-books"
	// DiscStatementPartiallyParsed — the ingestion layer could not parse part
	// of the supplied statement. Contributed via ITCPolicy.SourceDisclosures.
	DiscStatementPartiallyParsed = "statement-partially-parsed"
	// DiscCessNotModelled — compensation cess is not modelled anywhere in the
	// books, so no csamt is emitted (limitation 3).
	DiscCessNotModelled = "cess-not-modelled"
	// DiscPortalSchemaUnverified — the JSON key names are not verified against
	// the GSTN offline-utility schema (limitation 2).
	DiscPortalSchemaUnverified = "portal-schema-unverified"
	// DiscStatementCreditNoteNotInBooks — the supplied statement carries at
	// least one supplier CREDIT NOTE that the books do not, so table 4(C) here
	// is HIGHER than the department's own auto-populated figure by the tax on
	// that note. It is not corrected — see limitation 7. Contributed via
	// ITCPolicy.SourceDisclosures by the reconciliation.
	DiscStatementCreditNoteNotInBooks = "statement-credit-note-not-in-books"
	// DiscSec16_6NotModelled — s.16(6) credit, for the window during which a
	// registration stood CANCELLED and the cancellation was later REVOKED, is
	// not computed. Every date it turns on (the cancellation order, its
	// effective date, the revocation order) is registration-status data no table
	// in this schema holds, and guessing would revive credit on registrations
	// that were never cancelled. The gap is UNCONDITIONAL, so compliance/itc
	// emits the token on every reconciliation and the handler forwards it here
	// verbatim.
	//
	// ITS DIRECTION IS AN UNDER-CLAIM, WHICH IS WHY IT HAS TO BE VISIBLE: a
	// filer whose registration was cancelled and restored pays tax they do not
	// owe unless they check the affected periods by hand, and an under-claim
	// nobody can see is indistinguishable from a correct return. It lives in the
	// closed vocabulary — rather than only on the reconciliation report — because
	// the 3B is the document the filer signs. See the s.16(6) block at the foot
	// of compliance/itc/clock.go and DiscSec16_6NotModelled in
	// compliance/itc/effect.go, which mirrors this constant.
	DiscSec16_6NotModelled = "sec-16-6-revocation-not-modelled"
	// DiscNegativeValuesCarriedForward — at least one value in table 3.1 or 3.2
	// netted NEGATIVE for the period (the period's credit notes exceed its
	// supplies) and was clamped to zero, because GSTR-3B admits no negative
	// entry. The magnitudes are in GSTR3B.Carried and the filer must adjust
	// them against a subsequent period. See clampNegatives for the authority.
	DiscNegativeValuesCarriedForward = "negative-values-carried-forward"
	// DiscNotFilingReady is emitted whenever ANY other token is present. It is
	// the single flag a frontend needs to refuse to present the return as final.
	DiscNotFilingReady = "not-filing-ready"
)

// KnownDisclosures is the closed vocabulary, for validation and for a caller
// that wants to enumerate it.
var KnownDisclosures = []string{
	Disc4D1ReclaimNotModelled,
	DiscCessNotModelled,
	DiscITCEligibilityNotClassified,
	DiscNegativeValuesCarriedForward,
	DiscNoInwardStatement,
	DiscNotFilingReady,
	DiscPortalSchemaUnverified,
	DiscReversalsNotPostedToBooks,
	DiscRule4243NotComputed,
	DiscSec16_6NotModelled,
	DiscStatementCreditNoteNotInBooks,
	DiscStatementPartiallyParsed,
}

// Table31 is GSTR-3B table 3.1, rows (a)..(e).
type Table31 struct {
	// OsupDet is 3.1(a): outward taxable supplies other than zero-rated, nil-rated
	// and exempted.
	OsupDet Table31Row `json:"osup_det"`
	// OsupZero is 3.1(b): outward taxable supplies (zero-rated). Always zero in v1.
	OsupZero Table31Row `json:"osup_zero"`
	// OsupNilExmp is 3.1(c): other outward supplies (nil-rated, exempted). Always
	// zero in v1.
	OsupNilExmp Table31Row `json:"osup_nil_exmp"`
	// IsupRev is 3.1(d): inward supplies liable to reverse charge.
	IsupRev Table31Row `json:"isup_rev"`
	// OsupNongst is 3.1(e): non-GST outward supplies. Always zero in v1.
	OsupNongst Table31Row `json:"osup_nongst"`
}

// Table31Row is one 3.1 row: taxable value plus the three tax heads.
type Table31Row struct {
	Txval decimal.Decimal `json:"txval"`
	Iamt  decimal.Decimal `json:"iamt"`
	Camt  decimal.Decimal `json:"camt"`
	Samt  decimal.Decimal `json:"samt"`
}

// Tax returns the row's three tax heads as a TaxTotals.
func (r Table31Row) Tax() TaxTotals {
	return TaxTotals{CGST: r.Camt, SGST: r.Samt, IGST: r.Iamt}
}

// zeroRow is an all-zero 3.1 row (decimal.Decimal's zero value is 0, but being
// explicit keeps the JSON stable and the arithmetic total-order safe).
func zeroRow() Table31Row {
	return Table31Row{Txval: decimal.Zero, Iamt: decimal.Zero, Camt: decimal.Zero, Samt: decimal.Zero}
}

// Table32Row is one place-of-supply row of table 3.2 (inter-state supplies to
// unregistered persons). Only IGST can arise on an inter-state supply, so no
// CGST/SGST fields are emitted.
type Table32Row struct {
	Pos   string          `json:"pos"`
	Txval decimal.Decimal `json:"txval"`
	Iamt  decimal.Decimal `json:"iamt"`
}

// Table4ITC is GSTR-3B table 4. With no ITCSource it degenerates to the
// pre-reconciliation shape: every input-tax rupee available, ItcRev and
// ItcInelg absent, ItcNet == sum(ItcAvl).
//
// ItcRev and ItcInelg were added ADDITIVELY: itc_avl and itc_net keep their
// names, their types and their meanings, and both new keys carry omitempty so
// a return generated without an ITCSource marshals to exactly the bytes it
// marshalled to before this field existed.
type Table4ITC struct {
	// ItcAvl is 4(A): ITC available, by GSTN row code, in the order
	// IMPG, IMPS, ISRC, ISD, OTH. A row appears only when that bucket received
	// a voucher, so a period with no purchases yields an empty itc_avl rather
	// than five zero rows.
	ItcAvl []Table4Row `json:"itc_avl"`
	// ItcRev is 4(B): ITC reversed. Ty is "RUL" (4(B)(1) — rule 42/43 and
	// section 17(5)) or "OTH" (4(B)(2) — everything else), in that order.
	ItcRev []Table4Row `json:"itc_rev,omitempty"`
	// ItcNet is 4(C): net ITC available == sum(ItcAvl) - sum(ItcRev), head by
	// head. It may legally be NEGATIVE when reversals exceed availment.
	ItcNet Table4Row `json:"itc_net"`
	// ItcInelg is 4(D): "Other Details". Ty is "OTH" ONLY — 4(D)(1) "ITC
	// reclaimed which was reversed in an earlier period" is deliberately never
	// emitted, so this slice carries at most one row, which is what keeps (I1)
	// a clean two-way split.
	//
	// IT IS NOT SUBTRACTED FROM 4(C). See the file header.
	ItcInelg []Table4Row `json:"itc_inelg,omitempty"`
}

// Table4Row is one ITC row. Ty is the GSTN row code ("ISRC", "OTH", "NET",
// "RUL", ...).
type Table4Row struct {
	Ty   string          `json:"ty"`
	Iamt decimal.Decimal `json:"iamt"`
	Camt decimal.Decimal `json:"camt"`
	Samt decimal.Decimal `json:"samt"`
}

// Tax returns the row's three tax heads as a TaxTotals.
func (r Table4Row) Tax() TaxTotals {
	return TaxTotals{CGST: r.Camt, SGST: r.Samt, IGST: r.Iamt}
}

// OutwardTax returns the 3.1(a) tax heads. It is the value the GSTR-1
// consistency gate compares against GSTR1.TaxTotals().
func (g GSTR3B) OutwardTax() TaxTotals { return g.T31.OsupDet.Tax() }

// ITCTax returns the net ITC (table 4C) tax heads.
func (g GSTR3B) ITCTax() TaxTotals { return g.T4.ItcNet.Tax() }

// ErrInvalid3B reports a generated 3B that violates a GSTN validation rule.
var ErrInvalid3B = fmt.Errorf("generated GSTR-3B violates a validation rule")

// checkNoNegative3B reports the first negative among a table's values, naming
// the table, the value and the reason it cannot stand. Camt/Samt are passed as
// zero for table 3.2, which has no CGST/SGST by construction.
func checkNoNegative3B(table string, txval, iamt, camt, samt decimal.Decimal) error {
	for _, v := range []struct {
		name string
		val  decimal.Decimal
	}{
		{"taxable value", txval}, {"IGST", iamt}, {"CGST", camt}, {"SGST", samt},
	} {
		if v.val.IsNegative() {
			return fmt.Errorf("%w: table %s %s is %s. GSTR-3B admits no negative entry "+
				"(Circular 26/26/2017-GST para 4); a period whose credit notes exceed its "+
				"supplies reports zero and carries the excess to a later period, which is "+
				"what GSTR3B.Carried records",
				ErrInvalid3B, table, v.name, v.val.StringFixed(4))
		}
	}
	return nil
}

// Validate enforces the GSTN cross-table rule that table 3.2 (inter-state
// supplies to unregistered persons) can never exceed table 3.1(a), on both the
// taxable value and the IGST. Generate3B calls it, so an internally
// inconsistent 3B is an error rather than a plausible-looking wrong return.
func (g GSTR3B) Validate() error {
	// (I0) NO NEGATIVE ENTRY IN 3.1 OR 3.2. THIS RULE IS FIRST ON PURPOSE.
	//
	// build3B clamps these values (clampNegatives), so in a return this package
	// produced this check can never fire — it is a MUTATION GUARD, and its whole
	// value is its SENTENCE. Before it existed, a negative 3.1(a) was caught
	// further down by the 3.2 comparison, which reported
	//
	//	table 3.2 taxable 0.0000 exceeds 3.1(a) taxable -12000.0000
	//
	// about a table 3.2 that was EMPTY and could not exceed anything. That
	// sentence sent a reader to the wrong table, and one such month aborted the
	// whole twelve-month annual return. Checking the real rule first means the
	// first thing that fails is the thing that is actually wrong.
	for _, r := range []struct {
		no  string
		row Table31Row
	}{
		{"3.1(a)", g.T31.OsupDet}, {"3.1(b)", g.T31.OsupZero}, {"3.1(c)", g.T31.OsupNilExmp},
		{"3.1(d)", g.T31.IsupRev}, {"3.1(e)", g.T31.OsupNongst},
	} {
		if err := checkNoNegative3B(r.no, r.row.Txval, r.row.Iamt, r.row.Camt, r.row.Samt); err != nil {
			return err
		}
	}
	for _, r := range g.T32 {
		if err := checkNoNegative3B("3.2 (place of supply "+r.Pos+")",
			r.Txval, r.Iamt, decimal.Zero, decimal.Zero); err != nil {
			return err
		}
	}

	txval := decimal.Zero
	iamt := decimal.Zero
	for _, r := range g.T32 {
		txval = txval.Add(r.Txval)
		iamt = iamt.Add(r.Iamt)
	}
	if txval.GreaterThan(g.T31.OsupDet.Txval) {
		return fmt.Errorf("%w: table 3.2 taxable %s exceeds 3.1(a) taxable %s",
			ErrInvalid3B, txval.StringFixed(4), g.T31.OsupDet.Txval.StringFixed(4))
	}
	if iamt.GreaterThan(g.T31.OsupDet.Iamt) {
		return fmt.Errorf("%w: table 3.2 IGST %s exceeds 3.1(a) IGST %s",
			ErrInvalid3B, iamt.StringFixed(4), g.T31.OsupDet.Iamt.StringFixed(4))
	}

	// (I2) 4C == sum(4A) - sum(4B), head by head.
	//
	// This is the identity a reader of the return checks by eye, so it is the
	// one a bug hides behind most comfortably: an itc_net computed by any route
	// other than the subtraction (say, left as the sum of 4A after reversals
	// were added) still LOOKS like a plausible return. A NEGATIVE result is
	// LEGAL and must pass — reversals can exceed availment in a period where a
	// supplier's earlier invoices are being reversed en masse — so this is an
	// equality check and never a sign check.
	//
	// 4(D) IS NOT PART OF IT. Since Notification 14/2022, 4(D) is "Other
	// Details" and informational; section 17(5) is reversed through 4(B)(1).
	// Subtracting 4(D) here as well would double-count the haircut and
	// understate the credit the filer may claim.
	avl := sumTable4(g.T4.ItcAvl)
	rev := sumTable4(g.T4.ItcRev)
	want := subTax(avl, rev)
	if !want.Equal(g.T4.ItcNet.Tax()) {
		return fmt.Errorf("%w: table 4C net ITC %s/%s/%s (c/s/i) does not equal 4A %s/%s/%s minus 4B %s/%s/%s",
			ErrInvalid3B,
			g.T4.ItcNet.Camt.StringFixed(4), g.T4.ItcNet.Samt.StringFixed(4), g.T4.ItcNet.Iamt.StringFixed(4),
			avl.CGST.StringFixed(4), avl.SGST.StringFixed(4), avl.IGST.StringFixed(4),
			rev.CGST.StringFixed(4), rev.SGST.StringFixed(4), rev.IGST.StringFixed(4))
	}

	// A NEGATIVE 4(B)(2) "OTH" REVERSAL IS REJECTED, NOT CLAMPED — AND 4(B)(1)
	// "RUL" IS EXEMPT, BECAUSE THERE A NEGATIVE IS THE CORRECT ANSWER.
	//
	// 4(B)(2) is a HAIRCUT for credit the supplier did not support. It is
	// bounded below by zero by construction (itc.reversal4B2 ends in
	// PositivePart), and a negative one ADDS credit out of nothing. The way it
	// arises is a supplier CREDIT NOTE (negative posted input tax) treated as
	// "missing from the statement" and reversed like any other unmatched
	// document — which would hand the filer credit for a purchase return the
	// supplier never reported. Clamping it to zero would produce a
	// correct-looking return from a broken matcher; rejecting reports the bug.
	//
	// 4(B)(1) "RUL" IS DIFFERENT IN KIND. It mirrors, rupee for rupee, a figure
	// that is ALREADY IN 4(A): a section 17(5) blocked document is availed and
	// reversed by the same signed amount and nets to zero. When a blocked
	// purchase is RETURNED, 4(A) carries the credit note's negative tax and
	// 4(B)(1) must carry the same negative tax, or the filer loses credit they
	// never took — Rs 1,80,000 in cash on a returned car, in the fixture
	// TestBlockedPurchaseReturnedNetsToZeroAgreesWithITC pins. A blanket ban
	// here made that return ungeneratable, which is why the rule now names the
	// row it applies to.
	//
	// THE REAL BOUND ON A NEGATIVE REVERSAL IS PER-VOUCHER AND IS ENFORCED IN
	// checkITCDecision RULE (4) — reversal >= availment when the availment is
	// negative. It cannot be restated at period level: a period may mix a
	// positive availment with a negative one, so no relationship between the
	// 4(A) and 4(B) TOTALS is implied by it. Weakening this loop to a total
	// comparison would be a check that looks like it holds and does not.
	//
	// A negative 4(B)(1) is also the one figure here whose acceptance by the
	// GSTN portal is UNVERIFIED — see disclosed limitation 7 in the file header.
	for _, r := range g.T4.ItcRev {
		if r.Ty == "RUL" {
			continue
		}
		if r.Camt.IsNegative() || r.Samt.IsNegative() || r.Iamt.IsNegative() {
			return fmt.Errorf("%w: table 4B row %q is negative (c=%s s=%s i=%s) — "+
				"a negative reversal ADDS credit; a document with negative posted input tax "+
				"(a supplier credit note) must never be reversed for being unmatched",
				ErrInvalid3B, r.Ty,
				r.Camt.StringFixed(4), r.Samt.StringFixed(4), r.Iamt.StringFixed(4))
		}
	}
	return nil
}

// sumTable4 totals a slice of table-4 rows head by head.
func sumTable4(rows []Table4Row) TaxTotals {
	t := TaxTotals{CGST: decimal.Zero, SGST: decimal.Zero, IGST: decimal.Zero}
	for _, r := range rows {
		t = addTax(t, r.Tax())
	}
	return t
}

// addTax and subTax are head-by-head arithmetic on TaxTotals. They live here
// rather than beside the type in gstr1.go because table 4 is the only place in
// the package that needs them.
func addTax(a, b TaxTotals) TaxTotals {
	return TaxTotals{CGST: a.CGST.Add(b.CGST), SGST: a.SGST.Add(b.SGST), IGST: a.IGST.Add(b.IGST)}
}

func subTax(a, b TaxTotals) TaxTotals {
	return TaxTotals{CGST: a.CGST.Sub(b.CGST), SGST: a.SGST.Sub(b.SGST), IGST: a.IGST.Sub(b.IGST)}
}

// isZeroTax reports whether every head is zero.
func isZeroTax(t TaxTotals) bool {
	return t.CGST.IsZero() && t.SGST.IsZero() && t.IGST.IsZero()
}

// row3b is one resolved voucher_version row feeding the 3B aggregation.
//
// It carries both the FOLDED class (credit/debit notes mapped onto the side
// they face, which is what the table aggregation switches on) and the RAW
// parent_class, because the ITC seam has to be able to tell a purchase from a
// supplier credit note: the two are the same side of the return but they are
// separate documents in a GSTR-2B, with their own IMS action, and folding them
// together is exactly how a credit note gets paired with the invoice it
// adjusts. It also carries the party's GSTIN rather than only the
// isRegistered() verdict derived from it, because a match key needs the GSTIN
// itself.
type row3b struct {
	voucherID  uuid.UUID
	voucherNo  string
	date       time.Time
	class      string // FOLDED: 'SALES' or 'PURCHASE'
	rawClass   string // RAW vt.parent_class, before the note fold
	pos        string
	reverse    bool
	taxable    decimal.Decimal
	cgst       decimal.Decimal
	sgst       decimal.Decimal
	igst       decimal.Decimal
	partyGSTIN string
	registered bool
}

// tax returns the row's three posted tax heads.
func (r row3b) tax() TaxTotals {
	return TaxTotals{CGST: r.cgst, SGST: r.sgst, IGST: r.igst}
}

// interState reports whether the row is an inter-state supply: IGST charged.
//
// ⛔ IT IS SIGN-AGNOSTIC, AND THAT IS THE DIFFERENCE FROM GSTR-1's invoice.intra
// (gstr1.go:383-385), WHICH READS `igst > 0`. The two row sets are not the same
// shape: GSTR-1 classifies INVOICES, whose tax is positive, and reports credit
// notes as their own documents in CDNR/CDNUR. The 3B row set is FOLDED — a
// credit note arrives here as a SALES row with NEGATIVE tax (see the CROSS JOIN
// LATERAL sgn in Generate3B) — so `igst > 0` reads every inter-state credit note
// as intra-state.
//
// The consequence was a refusal on correct books: the note reduced 3.1(a) but
// never reduced the 3.2 bucket it belonged to, so an inter-state sale to an
// unregistered party plus a partial return of it drove 3.2 above 3.1(a) and
// Validate rejected the month. See
// TestBuild3B_AnInterStateCreditNoteReducesTable32.
func (r row3b) interState() bool { return !r.igst.IsZero() }

// Generate3B produces the GSTR-3B summary for the [from, to] inclusive date
// range (ISO "YYYY-MM-DD"). The period FP is taken from the `from` month.
//
// The guard order mirrors Generate verbatim: company scope, then date parsing,
// then range ordering — all before any DB round-trip.
func (s *Service) Generate3B(ctx context.Context, from, to string, opts Options3B) (GSTR3B, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return GSTR3B{}, db.ErrNoCompany
	}
	fromT, err := time.Parse("2006-01-02", from)
	if err != nil {
		return GSTR3B{}, fmt.Errorf("gstr: invalid from date %q: %w", from, err)
	}
	toT, err := time.Parse("2006-01-02", to)
	if err != nil {
		return GSTR3B{}, fmt.Errorf("gstr: invalid to date %q: %w", to, err)
	}
	if toT.Before(fromT) {
		return GSTR3B{}, fmt.Errorf("gstr: period end %s precedes start %s", to, from)
	}

	var rows []row3b
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Same head-version + is_deleted guard as the GSTR-1 query
		// (gstr1.go:322-338), applied to BOTH classes. The guard is load-bearing:
		// without the voucher_head join an ALTERed voucher would be counted twice
		// (every version carries the GST columns) and a DELETEd one would survive.
		q, err := tx.Query(ctx,
			// CREDIT/DEBIT NOTES are folded into the SIDE THEY FACE, with a sign.
			// The side is decided by the party's group nature, NOT by the voucher
			// class: a note against a customer (ASSET / Sundry Debtors) adjusts
			// outward supply; a note against a supplier (LIABILITY / Sundry
			// Creditors) adjusts input credit. Both classes can face either way,
			// so classifying on class alone puts purchase returns in the outward
			// tables. Mapping to 'SALES'/'PURCHASE' here means every downstream
			// aggregate nets automatically instead of each needing note handling.
			`SELECT CASE
			          WHEN vt.parent_class IN ('CREDIT_NOTE', 'DEBIT_NOTE')
			          THEN CASE WHEN pg.nature = 'ASSET' THEN 'SALES' ELSE 'PURCHASE' END
			          ELSE vt.parent_class
			        END AS parent_class,
			        vt.parent_class AS raw_class,
			        vv.voucher_id, vv.voucher_no, vv.voucher_date,
			        vv.place_of_supply, vv.is_reverse_charge,
			        vv.taxable_value * s.sgn, vv.cgst * s.sgn,
			        vv.sgst * s.sgn, vv.igst * s.sgn,
			        pl.gstin, pl.registration_type::text
			   FROM voucher_version vv
			   JOIN voucher_head    vh
			     ON vh.company_id = vv.company_id
			    AND vh.voucher_id = vv.voucher_id
			    AND vh.current_version_id = vv.id
			   JOIN voucher_type    vt ON vt.id = vv.voucher_type_id
			   LEFT JOIN ledger        pl ON pl.id = vv.party_ledger_id
			   LEFT JOIN account_group pg ON pg.id = pl.group_id
			   CROSS JOIN LATERAL (
			     -- A CREDIT note reduces outward supply; a DEBIT note raised on a
			     -- SUPPLIER (a purchase return) reduces input credit. Both are
			     -- negative adjustments to the side they face.
			     SELECT CASE
			              WHEN vt.parent_class = 'CREDIT_NOTE' THEN -1
			              WHEN vt.parent_class = 'DEBIT_NOTE'
			                   AND pg.nature = 'LIABILITY' THEN -1
			              ELSE 1
			            END
			   ) AS s(sgn)
			  WHERE vh.is_deleted = false
			    AND vt.parent_class IN ('SALES', 'PURCHASE', 'CREDIT_NOTE', 'DEBIT_NOTE')
			    AND vv.voucher_date BETWEEN $1 AND $2
			    AND vv.taxable_value IS NOT NULL
			  ORDER BY vt.parent_class, vv.voucher_date, vv.voucher_no, vv.version_no`,
			from, to)
		if err != nil {
			return fmt.Errorf("gstr: query 3b versions: %w", err)
		}
		defer q.Close()

		for q.Next() {
			var (
				class     string
				rawClass  string
				voucherID uuid.UUID
				voucherNo *string
				date      *time.Time
				pos       *string
				reverse   bool
				taxable   decimal.Decimal
				cgst      *decimal.Decimal
				sgst      *decimal.Decimal
				igst      *decimal.Decimal
				gstin     *string
				regType   *string
			)
			if err := q.Scan(&class, &rawClass, &voucherID, &voucherNo, &date,
				&pos, &reverse, &taxable,
				&cgst, &sgst, &igst, &gstin, &regType); err != nil {
				return fmt.Errorf("gstr: scan 3b version: %w", err)
			}
			r := row3b{
				voucherID:  voucherID,
				voucherNo:  derefStr(voucherNo),
				class:      class,
				rawClass:   rawClass,
				pos:        derefStr(pos),
				reverse:    reverse,
				taxable:    taxable,
				cgst:       deref(cgst),
				sgst:       deref(sgst),
				igst:       deref(igst),
				partyGSTIN: derefStr(gstin),
				registered: isRegistered(derefStr(regType), derefStr(gstin)),
			}
			if date != nil {
				r.date = *date
			}
			rows = append(rows, r)
		}
		return q.Err()
	})
	if err != nil {
		return GSTR3B{}, err
	}

	// The ITC decisions are resolved AFTER the row transaction and OUTSIDE it:
	// the source issues its own reads against its own tables (statements, IMS
	// actions), it must not be able to hold the books' transaction open, and
	// build3B has to stay pure so the migration-corpus oracle and the hermetic
	// tests can call it directly.
	//
	// A NIL source is the supported default and yields a nil map, which
	// reproduces the pre-reconciliation output byte for byte. It is checked
	// here rather than being a required field because Options3B's ZERO VALUE is
	// passed by callers that only want the outward half.
	var decisions map[uuid.UUID]ITCDecision
	if opts.ITC != nil {
		decisions, err = opts.ITC.Decisions(ctx, from, to)
		if err != nil {
			return GSTR3B{}, fmt.Errorf("gstr: resolve ITC decisions: %w", err)
		}
	}

	out, err := build3B(fromT, rows, decisions, opts)
	if err != nil {
		return GSTR3B{}, err
	}
	if err := out.Validate(); err != nil {
		return GSTR3B{}, err
	}
	return out, nil
}

// errStaleITCDecision reports an ITC decision that does not partition the
// voucher's posted input tax. It is deliberately its OWN error rather than a
// silent fallback to "everything available": the commonest way to produce one
// is to resolve a decision against a voucher version that has since been
// ALTERED, and falling back would quietly drop a reversal the reconciliation
// had concluded was due — an over-claim, which is the audit-dangerous
// direction. See ITCDecision's doc comment.
var errStaleITCDecision = fmt.Errorf("ITC decision does not account for the voucher's posted input tax")

// checkITCDecision enforces every structural rule an ITCDecision must obey
// before its numbers are allowed anywhere near table 4. None of these is a
// style rule; each one is an identity the return rests on.
func checkITCDecision(r row3b, d ITCDecision) error {
	label := r.voucherNo
	if label == "" {
		label = r.voucherID.String()
	}

	// (1) THE PARTITION. Available + Ineligible == the posted input tax, head
	// by head. This is what makes (I1) exact by construction rather than by
	// hope, AND it is the version guard: an ALTER moves taxable_value, which
	// moves the tax, which makes a decision resolved against the superseded
	// version fail here instead of reversing a figure that no longer exists.
	if got := addTax(d.Available, d.Ineligible); !got.Equal(r.tax()) {
		return fmt.Errorf("%w: voucher %s posted c=%s s=%s i=%s but the decision "+
			"accounts for c=%s s=%s i=%s (available + ineligible) — a decision resolved "+
			"against a superseded voucher version must not be applied",
			errStaleITCDecision, label,
			r.cgst.StringFixed(4), r.sgst.StringFixed(4), r.igst.StringFixed(4),
			got.CGST.StringFixed(4), got.SGST.StringFixed(4), got.IGST.StringFixed(4))
	}

	// (2) A KNOWN 4(A) ROW CODE. An unknown bucket would be silently dropped by
	// the ordered emit below and its credit would vanish from 4(A) while still
	// counting in the posting total — i.e. it would break (I1) invisibly.
	if d.Bucket != "" {
		known := false
		for _, b := range itc4ARowOrder {
			if b == d.Bucket {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("%w: voucher %s carries unknown ITC bucket %q", ErrInvalid3B, label, d.Bucket)
		}
	}

	// (3) AT MOST ONE 4(B) REVERSAL PER VOUCHER, AND 4(B)(1) WINS. A blocked
	// credit that is ALSO unmatched is reversed ONCE; reversing it under both
	// heads takes the credit away twice.
	if !isZeroTax(d.ReversedRules) && !isZeroTax(d.ReversedOther) {
		return fmt.Errorf("%w: voucher %s is reversed under BOTH 4(B)(1) and 4(B)(2); "+
			"precedence is 4(B)(1) wins and the credit is reversed once", ErrInvalid3B, label)
	}

	// (4) A REVERSAL IS NEVER NEGATIVE, AND — WHERE CREDIT WAS ACTUALLY
	// AVAILED — IT IS A SUBSET OF WHAT WAS AVAILED.
	//
	// ---------------------------------------------------------------------
	// THE TWO SUPPLIER-CREDIT-NOTE CASES. THEY LOOK ALIKE AND PULL OPPOSITE
	// WAYS, WHICH IS WHY THEY ARE WRITTEN OUT.
	// ---------------------------------------------------------------------
	// This block and the "NEGATIVE-TAX RULE" block in
	// internal/compliance/itc/effect.go state the SAME decision. They are the
	// two halves of one seam and they disagreed once, silently. If you change
	// one, change the other; the paired regressions
	// TestCreditNoteDeclaredLargerAgreesWithITC (this package) and
	// TestCreditNoteDeclaredLargerAgreesWithGSTR (itc) assert the same rupee
	// figure on both sides so the drift cannot recur unnoticed.
	//
	//	(a) THE NOTE IS ABSENT FROM THE STATEMENT. We booked a supplier credit
	//	    note of -300 and the supplier has not filed it. 4(A) carries the
	//	    books' -300 and NOTHING is reversed: we have already given that
	//	    credit up, and the supplier's silence is no reason to hand it back.
	//	    A "reversal" here would have to be NEGATIVE — books -300 less a
	//	    statement side of zero — and a negative reversal ADDS credit. The
	//	    sign check below is what refuses that, and it is enforced for every
	//	    voucher, positive or negative.
	//
	//	(b) THE SUPPLIER DECLARED A LARGER NOTE THAN WE BOOKED. Books -300
	//	    against a statement -400. GSTR-2B says the credit for this document
	//	    is 100 LOWER than our books claim, so 100 must come out: a POSITIVE
	//	    reversal sitting on a NEGATIVE availment, netting -300 - 100 = -400,
	//	    which is exactly the statement's figure. This is NOT case (a) and it
	//	    must not be capped to zero — capping it leaves 100 of credit in the
	//	    return that the supplier has cancelled, which is an over-claim the
	//	    department can see in its own data.
	//
	// SO THE OLD BOUND, max(Available, 0), WAS WRONG. It read case (b) as case
	// (a) and refused the decision outright, so a company with ONE under-booked
	// supplier credit note could not generate a GSTR-3B at all. Where Available
	// is negative there is NO local upper bound to enforce: the size of the
	// correction is set by the supplier's declared figure, which this package
	// cannot see and must not restate — the formula lives once, in
	// itc.reversal4B2, because a formula stated twice is a formula that drifts.
	// Non-negativity of the 4(B)(2) head, plus the availment as a LOWER bound,
	// is the whole of the check for a credit note — see case (c) below.
	//
	// AND THE CORRECTION RIDES IN 4(B)(2), NOT IN 4(A). That is forced, not
	// chosen: rule (1) above requires Available + Ineligible to equal the
	// POSTED input tax, so 4(A) is -300 and cannot be made -400 without
	// breaking identity (I1) — the tie between the return and the books.
	// 4(C) = sum(4A) - sum(4B) subtracts the 100 exactly once (see build3B),
	// and 4(D) is not part of that identity, so nothing is double-counted.
	//
	//	(c) THE BLOCKED PURCHASE IS RETURNED. A section 17(5) blocked purchase
	//	    is availed in 4(A) and reversed in 4(B)(1) by the SAME figure, so
	//	    its net contribution to 4(C) is ZERO — the taxpayer never gets the
	//	    credit. When the purchase is RETURNED the supplier's credit note
	//	    posts NEGATIVE input tax: 4(A) carries -90,000 per head and the
	//	    17(5) reversal must carry -90,000 per head with it, or the pair no
	//	    longer nets to zero. THIS IS A LEGITIMATE NEGATIVE REVERSAL and
	//	    refusing it destroys the credit: a blanket ban leaves the earlier
	//	    period's +90,000 reversal standing against this period's -90,000
	//	    availment, and the filer pays Rs 1,80,000 in cash on a car they
	//	    returned. See TestBlockedPurchaseReturnedNetsToZeroAgreesWithITC
	//	    and, in itc, "THE BLOCKED PURCHASE THAT IS RETURNED".
	//
	// SO THE RULE IS NOT "A REVERSAL IS NEVER NEGATIVE". IT IS TWO RULES, AND
	// THE HEAD IT APPLIES TO IS WHAT SEPARATES THEM.
	//
	//	4(B)(2) "Others" IS NEVER NEGATIVE, whatever the availment. It is a
	//	HAIRCUT for credit the supplier did not support; there is no figure
	//	elsewhere in the return it mirrors, so a negative one manufactures
	//	credit out of nothing. That is case (a), and it is refused for every
	//	voucher, positive or negative. itc.reversal4B2 ends in PositivePart
	//	for the same reason.
	//
	//	4(B)(1) "RUL" MAY BE NEGATIVE, bounded by the availment. It MIRRORS a
	//	figure that is already in 4(A) rupee for rupee, so it must carry 4(A)'s
	//	sign — that is case (c).
	//
	// The combined bound, which is what the loop enforces, is that A REVERSAL
	// NEVER FLIPS A DOCUMENT'S NET CONTRIBUTION TO 4(C) TO THE OPPOSITE SIGN OF
	// WHAT WAS POSTED:
	//
	//	available >= 0 (an invoice):    0 <= reversal <= available.
	//	                                A negative reversal ADDS credit out of
	//	                                nothing; a reversal larger than the
	//	                                availment takes credit that was never
	//	                                taken.
	//	available <  0 (a credit note): reversal >= available, and NO UPPER
	//	                                BOUND. The lower bound is case (c): the
	//	                                undoing of a 17(5) reversal can be at
	//	                                most the whole of the note. The absent
	//	                                upper bound is case (b): the size of a
	//	                                statement-driven correction is set by
	//	                                the supplier's declared figure, which
	//	                                this package cannot see and must not
	//	                                restate — the formula lives once, in
	//	                                itc.reversal4B2, because a formula
	//	                                stated twice is a formula that drifts.
	//
	// Equivalently: avail - rev lies in [0, avail] when avail is positive, and
	// is <= 0 when avail is negative.
	rev := addTax(d.ReversedRules, d.ReversedOther)
	for _, h := range []struct {
		name              string
		rev, other, avail decimal.Decimal
	}{
		{"CGST", rev.CGST, d.ReversedOther.CGST, d.Available.CGST},
		{"SGST", rev.SGST, d.ReversedOther.SGST, d.Available.SGST},
		{"IGST", rev.IGST, d.ReversedOther.IGST, d.Available.IGST},
	} {
		// The 4(B)(2) haircut, first and unconditionally. Case (a).
		if h.other.IsNegative() {
			return fmt.Errorf("%w: voucher %s has a NEGATIVE %s 4(B)(2) reversal %s — "+
				"reversing a negative ADDS credit, and unlike the 4(B)(1) section 17(5) "+
				"reversal there is no figure in 4(A) for it to mirror",
				ErrInvalid3B, label, h.name, h.other.StringFixed(4))
		}
		if h.avail.IsNegative() {
			// Case (c) is the lower bound; case (b) leaves no upper one.
			if h.rev.LessThan(h.avail) {
				return fmt.Errorf("%w: voucher %s reverses %s %s against an availment of "+
					"only %s — a negative 4(B)(1) may undo the section 17(5) reversal of the "+
					"purchase this note returns, and no more; going past it ADDS credit the "+
					"document never carried",
					ErrInvalid3B, label, h.name, h.rev.StringFixed(4), h.avail.StringFixed(4))
			}
			continue
		}
		if h.rev.IsNegative() {
			return fmt.Errorf("%w: voucher %s has a NEGATIVE %s reversal %s against a "+
				"NON-NEGATIVE availment of %s — reversing a negative ADDS credit",
				ErrInvalid3B, label, h.name, h.rev.StringFixed(4), h.avail.StringFixed(4))
		}
		if h.rev.GreaterThan(h.avail) {
			return fmt.Errorf("%w: voucher %s reverses %s %s but only %s was available "+
				"(a reversal is a subset of the credit that was taken)",
				ErrInvalid3B, label, h.name, h.rev.StringFixed(4), h.avail.StringFixed(4))
		}
	}
	return nil
}

// zeroTax is an explicitly-zeroed TaxTotals.
func zeroTax() TaxTotals {
	return TaxTotals{CGST: decimal.Zero, SGST: decimal.Zero, IGST: decimal.Zero}
}

// apportionment applies rule 42 and rule 43 over CALLER-SUPPLIED inputs and
// returns the amount that belongs in 4(B)(1). Nil inputs contribute nothing.
//
//	rule 42:  D1 = (E / F) x C2      D2 = 5% x C2      reversal = D1 + D2
//	rule 43:  Te = (E / F) x Tr      reversal = Te
//
// E and F are NEVER derived from our own return — see Rule42Inputs. F <= 0 or
// E outside [0, F] is refused rather than treated as a zero reversal, because a
// zero reversal is indistinguishable from a correct one at a glance and this
// engine has no other way to say "these inputs are not usable".
//
// Arithmetic is carried at 4dp in decimal. GSTN's own utility rounds table 4 to
// whole rupees at declaration time; that is out of scope here and disclosed.
func apportionment(p *ITCPolicy) (TaxTotals, bool, error) {
	total := zeroTax()
	if p == nil {
		return total, false, nil
	}
	ratio := func(e, f decimal.Decimal, rule string) (decimal.Decimal, error) {
		if !f.IsPositive() {
			return decimal.Zero, fmt.Errorf("%w: rule %s total turnover in the State (F) is %s; "+
				"it must be positive, and it must be SUPPLIED — deriving it from this return would "+
				"make E/F zero for every taxpayer", ErrInvalid3B, rule, f.StringFixed(4))
		}
		if e.IsNegative() || e.GreaterThan(f) {
			return decimal.Zero, fmt.Errorf("%w: rule %s exempt supply value (E) %s is outside [0, F=%s]",
				ErrInvalid3B, rule, e.StringFixed(4), f.StringFixed(4))
		}
		return e.Div(f), nil
	}
	scale := func(t TaxTotals, k decimal.Decimal) TaxTotals {
		return TaxTotals{
			CGST: t.CGST.Mul(k).Round(4),
			SGST: t.SGST.Mul(k).Round(4),
			IGST: t.IGST.Mul(k).Round(4),
		}
	}
	any := false
	if r := p.Rule42; r != nil {
		k, err := ratio(r.E, r.F, "42")
		if err != nil {
			return zeroTax(), false, err
		}
		d1 := scale(r.C2, k)
		d2 := scale(r.C2, decimal.RequireFromString("0.05"))
		total = addTax(total, addTax(d1, d2))
		any = true
	}
	if r := p.Rule43; r != nil {
		k, err := ratio(r.E, r.F, "43")
		if err != nil {
			return zeroTax(), false, err
		}
		total = addTax(total, scale(r.Tr, k))
		any = true
	}
	return total, any, nil
}

// build3B assembles the GSTR3B structure from the resolved rows and the
// per-voucher ITC decisions. It is PURE and deterministic (stable ordering) —
// no context, no transaction — because the migration-corpus oracle and the
// hermetic table-4 tests both call it directly. A NIL decisions map is the
// supported default and reproduces the pre-reconciliation output exactly.
func build3B(period time.Time, rows []row3b, decisions map[uuid.UUID]ITCDecision, opts Options3B) (GSTR3B, error) {
	out := GSTR3B{
		GSTIN: opts.GSTIN,
		FP:    fmt.Sprintf("%02d%04d", int(period.Month()), period.Year()),
		T31: Table31{
			OsupDet:     zeroRow(),
			OsupZero:    zeroRow(),
			OsupNilExmp: zeroRow(),
			IsupRev:     zeroRow(),
			OsupNongst:  zeroRow(),
		},
	}

	// 3.2 buckets keyed by place of supply.
	interUnreg := map[string]*Table32Row{}

	// TABLE 4 ACCUMULATORS, KEYED RATHER THAN NAMED. The pre-2B code carried
	// two TaxTotals and two booleans, one pair per row code, so adding IMPG
	// meant touching six places. Keyed accumulators plus the itc4ARowOrder
	// slice make a new GSTN row code exactly one constant and one slice entry.
	avl := map[ITCBucket]*TaxTotals{}
	avlUsed := map[ITCBucket]bool{}
	addAvl := func(b ITCBucket, t TaxTotals) {
		cur, ok := avl[b]
		if !ok {
			z := zeroTax()
			cur = &z
			avl[b] = cur
		}
		*cur = addTax(*cur, t)
		// Marked used on ROUTING, not on amount: a purchase whose tax happens
		// to be zero still produced a 4(A) row before the ITC seam existed, and
		// changing that would change the bytes of a return nobody asked to
		// change.
		avlUsed[b] = true
	}
	revRules, revOther, inelg := zeroTax(), zeroTax(), zeroTax()
	haveRevRules, haveRevOther, haveInelg := false, false, false
	// unclassified records that at least one purchase reached table 4 with no
	// ITC decision at all. It drives a disclosure, not a number.
	unclassified := false

	addRow := func(dst *Table31Row, r row3b) {
		dst.Txval = dst.Txval.Add(r.taxable)
		dst.Camt = dst.Camt.Add(r.cgst)
		dst.Samt = dst.Samt.Add(r.sgst)
		dst.Iamt = dst.Iamt.Add(r.igst)
	}

	for _, r := range rows {
		switch r.class {
		case "SALES":
			// 3.1(a). See limitation (5): RCM outward tax is included here on
			// purpose so 3.1(a) ties to GSTR-1 and to the posted output tax.
			addRow(&out.T31.OsupDet, r)

			// 3.2: inter-state supplies to UNREGISTERED recipients, by POS.
			if r.interState() && !r.registered {
				b := interUnreg[r.pos]
				if b == nil {
					b = &Table32Row{Pos: r.pos, Txval: decimal.Zero, Iamt: decimal.Zero}
					interUnreg[r.pos] = b
				}
				b.Txval = b.Txval.Add(r.taxable)
				b.Iamt = b.Iamt.Add(r.igst)
			}

		case "PURCHASE":
			// 3.1(d) inward supplies liable to reverse charge. Under RCM the
			// ledger stamps taxable_value = InvoiceTotal (ledger.go:1004-1009)
			// AND non-zero tax heads, so both txval and tax are reportable.
			if r.reverse {
				addRow(&out.T31.IsupRev, r)

				// THE RCM CARVE-OUT. This early return is load-bearing and the
				// `continue` is the whole of it: reverse-charge ITC goes
				// straight into ISRC and is NEVER routed through the 2A/2B
				// decision lookup.
				//
				// WHY: IMS excludes reverse-charge supplies outright (GSTN IMS
				// FAQ 8 and 18 — "RCM invoices are not part of IMS but will
				// continue to be part of GSTR-2B"), and RCM credit is
				// SELF-ASSESSED: the recipient raises the liability and takes
				// the credit, so there is no supplier document in the statement
				// to match against. An ISRC row can therefore never match, and
				// a matcher that treats "no statement row" as "reverse it"
				// would reverse ONE HUNDRED PER CENT of every taxpayer's RCM
				// ITC. Delete this `continue` and that is exactly what happens.
				addAvl(BucketISRC, r.tax())
				continue
			}

			// Everything else used to fall through to a single unconditional
			// "all of it is eligible OTH" accumulation. THAT was the
			// all-eligible bug. It is now a decision lookup — with the SAME
			// behaviour preserved for a MISSING key, so a company with no
			// statement ingested reads exactly as it did before.
			d, ok := decisions[r.voucherID]
			if !ok {
				addAvl(BucketOTH, r.tax())
				unclassified = true
				continue
			}
			if err := checkITCDecision(r, d); err != nil {
				return GSTR3B{}, err
			}
			bucket := d.Bucket
			if bucket == "" {
				bucket = BucketOTH
			}
			addAvl(bucket, d.Available)
			if !isZeroTax(d.ReversedRules) {
				revRules = addTax(revRules, d.ReversedRules)
				haveRevRules = true
			}
			if !isZeroTax(d.ReversedOther) {
				revOther = addTax(revOther, d.ReversedOther)
				haveRevOther = true
			}
			if !isZeroTax(d.Ineligible) {
				inelg = addTax(inelg, d.Ineligible)
				haveInelg = true
			}
		}
	}

	// Rule 42/43 apportionment is a PERIOD-level figure, not a per-voucher one,
	// so it is added after the row loop and lands in 4(B)(1) alongside the
	// section 17(5) reversals.
	ap, haveAp, err := apportionment(opts.ITCPolicy)
	if err != nil {
		return GSTR3B{}, err
	}
	if haveAp {
		revRules = addTax(revRules, ap)
		haveRevRules = true
	}

	// 3.2 rows, stable by POS.
	if len(interUnreg) > 0 {
		poss := make([]string, 0, len(interUnreg))
		for p := range interUnreg {
			poss = append(poss, p)
		}
		sort.Strings(poss)
		out.T32 = make([]Table32Row, 0, len(poss))
		for _, p := range poss {
			out.T32 = append(out.T32, *interUnreg[p])
		}
	}

	// Table 4. Rows are emitted only when the bucket received a voucher, so a
	// books-with-no-purchases period yields an empty itc_avl rather than noise.
	//
	// 4(A), in the GSTN row order.
	for _, b := range itc4ARowOrder {
		if !avlUsed[b] {
			continue
		}
		t := *avl[b]
		out.T4.ItcAvl = append(out.T4.ItcAvl, Table4Row{
			Ty: string(b), Camt: t.CGST, Samt: t.SGST, Iamt: t.IGST})
	}
	// 4(B), RUL before OTH.
	if haveRevRules {
		out.T4.ItcRev = append(out.T4.ItcRev, Table4Row{
			Ty: "RUL", Camt: revRules.CGST, Samt: revRules.SGST, Iamt: revRules.IGST})
	}
	if haveRevOther {
		out.T4.ItcRev = append(out.T4.ItcRev, Table4Row{
			Ty: "OTH", Camt: revOther.CGST, Samt: revOther.SGST, Iamt: revOther.IGST})
	}
	// 4(C) = sum(4A) - sum(4B), head by head. NOT minus 4(D): since
	// Notification 14/2022, 4(D) is informational "Other Details" and section
	// 17(5) is reversed through 4(B)(1), so subtracting 4(D) as well would
	// DOUBLE-COUNT the haircut and understate the credit the filer may claim.
	//
	// The Ty stays "NET" even though it is not a GSTN row code, because nothing
	// reads it and a gratuitous wire change during a wave that already moves
	// real numbers is how you end up with two red gates and cannot attribute
	// either.
	net := subTax(sumTable4(out.T4.ItcAvl), sumTable4(out.T4.ItcRev))
	out.T4.ItcNet = Table4Row{Ty: "NET", Camt: net.CGST, Samt: net.SGST, Iamt: net.IGST}
	// 4(D)(2). 4(D)(1) "ITC reclaimed which was reversed in an earlier period"
	// is deliberately never emitted: it is a carry-in, not a function of this
	// period's postings, and emitting it would break (I1).
	if haveInelg {
		out.T4.ItcInelg = append(out.T4.ItcInelg, Table4Row{
			Ty: "OTH", Camt: inelg.CGST, Samt: inelg.SGST, Iamt: inelg.IGST})
	}

	// THE NO-NEGATIVE RULE, APPLIED LAST — after every table 3.1 and 3.2 figure
	// is final and before anything validates or discloses them.
	out.Carried = clampNegatives(&out)

	disc, err := disclosures3B(opts, out, unclassified)
	if err != nil {
		return GSTR3B{}, err
	}
	out.Disclosures = disc
	return out, nil
}

// clampNegatives applies the GSTN "no negative entries" rule to tables 3.1 and
// 3.2 and returns what it removed, as positive magnitudes.
//
// ⭐ THE AUTHORITY, because this function DESTROYS A FIGURE THE BOOKS PRODUCED
// and no reader should have to take that on trust:
//
//   - GST portal, FAQ > Form GSTR-3B: "Negative values, if any, shall not be
//     considered in the table and system will provide the value as zero.
//     Negative values may arise in these tables due to credit notes and
//     downward amendment of outward supplies."
//   - Circular No. 26/26/2017-GST dated 29-12-2017, para 4: "While making
//     adjustment in the output tax liability or input tax credit, there can be
//     no negative entries in the FORM GSTR-3B." and "The amount remaining for
//     adjustment, if any, may be adjusted in the return(s) in FORM GSTR-3B of
//     subsequent month(s) and, in cases where such adjustment is not feasible,
//     refund may be claimed."
//
// ⛔ THE CLAMP IS NOT THE FIX ON ITS OWN — THE CARRY IS. A month whose credit
// notes exceed its sales is an ordinary month, and the filer is OBLIGED to file
// it; refusing to produce the return would be a worse defect than the one this
// replaces. But zeroing the negative and saying nothing would destroy the
// filer's credit note, so every clamped magnitude is returned, disclosed
// through DiscNegativeValuesCarriedForward, and thereby made an action the
// operator can see and take next period.
//
// ⚠️ WHAT IT DOES NOT DO: it does not APPLY the carry to the next period. There
// is no cross-period store here and inventing one would silently change a
// figure in a return the filer may already have filed. The carry is reported,
// not consumed — which is why the return also declares itself not filing-ready.
//
// ⛔ TABLE 4 IS DELIBERATELY EXCLUDED. A negative 4(C) is LEGAL and must pass —
// Validate says so at length, and the ITC identity is an equality check and
// never a sign check. Clamping there would manufacture credit out of nothing.
//
// Clamping is PER VALUE, not per row, because that is what the portal FAQ
// describes ("negative values ... shall not be considered").
func clampNegatives(g *GSTR3B) []CarriedForward {
	var carried []CarriedForward

	clampRow := func(label string, row *Table31Row) {
		c := CarriedForward{Table: label, Txval: decimal.Zero, Iamt: decimal.Zero,
			Camt: decimal.Zero, Samt: decimal.Zero}
		any := false
		for _, f := range []struct {
			val  *decimal.Decimal
			into *decimal.Decimal
		}{
			{&row.Txval, &c.Txval}, {&row.Iamt, &c.Iamt},
			{&row.Camt, &c.Camt}, {&row.Samt, &c.Samt},
		} {
			if f.val.IsNegative() {
				*f.into = f.val.Neg()
				*f.val = decimal.Zero
				any = true
			}
		}
		if any {
			carried = append(carried, c)
		}
	}

	// 3.1, in GSTN row order, so the carried list reads down the return.
	clampRow("3.1(a)", &g.T31.OsupDet)
	clampRow("3.1(b)", &g.T31.OsupZero)
	clampRow("3.1(c)", &g.T31.OsupNilExmp)
	clampRow("3.1(d)", &g.T31.IsupRev)
	clampRow("3.1(e)", &g.T31.OsupNongst)

	// 3.2, per place-of-supply bucket. Already sorted by POS.
	for i := range g.T32 {
		r := &g.T32[i]
		c := CarriedForward{Table: "3.2:" + r.Pos, Txval: decimal.Zero, Iamt: decimal.Zero,
			Camt: decimal.Zero, Samt: decimal.Zero}
		any := false
		if r.Txval.IsNegative() {
			c.Txval, r.Txval, any = r.Txval.Neg(), decimal.Zero, true
		}
		if r.Iamt.IsNegative() {
			c.Iamt, r.Iamt, any = r.Iamt.Neg(), decimal.Zero, true
		}
		if any {
			carried = append(carried, c)
		}
	}
	return carried
}

// disclosures3B computes the closed-vocabulary disclosure set for a generated
// return: sorted, deduplicated, deterministic.
//
// `not-filing-ready` is emitted whenever ANY other token is, and at least three
// tokens are unconditional today (the JSON key names are unverified against the
// GSTN utility schema, cess is not modelled anywhere in the books, and 4(D)(1)
// is never emitted). So this software's GSTR-3B always declares itself
// not-filing-ready — which is the honest answer, and saying it in a field a
// frontend can switch on is the entire point.
func disclosures3B(opts Options3B, g GSTR3B, unclassified bool) ([]string, error) {
	set := map[string]bool{
		// Limitation (2): key names unverified against the GSTN offline utility.
		DiscPortalSchemaUnverified: true,
		// Limitation (3): no cess anywhere in the books.
		DiscCessNotModelled: true,
		// 4(D)(1) is structurally absent — there is no earlier-period store.
		Disc4D1ReclaimNotModelled: true,
	}
	if opts.ITC == nil {
		set[DiscNoInwardStatement] = true
	}
	if unclassified {
		set[DiscITCEligibilityNotClassified] = true
	}
	if opts.ITCPolicy == nil || opts.ITCPolicy.Rule42 == nil || opts.ITCPolicy.Rule43 == nil {
		set[DiscRule4243NotComputed] = true
	}
	if len(g.Carried) > 0 {
		// A figure the books produced was removed from the return and is the
		// filer's to adjust next period. Emitted only when something was
		// actually clamped, so it names a real outstanding action.
		set[DiscNegativeValuesCarriedForward] = true
	}
	if len(g.T4.ItcRev) > 0 {
		// The reversal is RETURN-SIDE ONLY; the filer must pass the
		// Dr Expense / Cr Input tax entry themselves. Emitted only when a
		// reversal actually exists, so it names a real outstanding action.
		set[DiscReversalsNotPostedToBooks] = true
	}
	if opts.ITCPolicy != nil {
		for _, t := range opts.ITCPolicy.SourceDisclosures {
			known := false
			for _, k := range KnownDisclosures {
				if k == t {
					known = true
					break
				}
			}
			if !known {
				return nil, fmt.Errorf("%w: disclosure %q is not in the closed vocabulary; "+
					"a token the frontend does not recognise is a disclosure that will never be shown",
					ErrInvalid3B, t)
			}
			set[t] = true
		}
	}
	if len(set) > 0 {
		set[DiscNotFilingReady] = true
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// ITCPostingTotals sums the INPUT-tax POSTINGS for the period: the amounts
// posted to the input-tax ledgers named in opts on PURCHASE voucher versions
// whose voucher_date falls in [from, to]. It is the books-side ITC total that
// Reconcile3B ties table 4 against. A nil ledger id contributes zero.
//
// It is the PURCHASE-class twin of PostingTaxTotals (gstr1.go:389-436) and
// carries the identical head-version + is_deleted guard.
func (s *Service) ITCPostingTotals(ctx context.Context, from, to string, opts Options3B) (TaxTotals, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return TaxTotals{}, db.ErrNoCompany
	}
	sum := func(ctx context.Context, tx pgx.Tx, ledgerID uuid.UUID) (decimal.Decimal, error) {
		if ledgerID == uuid.Nil {
			return decimal.Zero, nil
		}
		var total decimal.Decimal
		err := tx.QueryRow(ctx,
			// NET by dr_cr: a purchase DEBITS the input-tax ledger; a debit note
			// raised on a supplier CREDITS it back. A raw SUM would make a
			// purchase return INCREASE claimable ITC.
			`SELECT COALESCE(SUM(CASE WHEN p.dr_cr = 'D' THEN p.amount
			                          ELSE -p.amount END), 0)
			   FROM posting p
			   JOIN voucher_version vv ON vv.id = p.voucher_version_id
			   JOIN voucher_head    vh
			     ON vh.company_id = vv.company_id
			    AND vh.voucher_id = vv.voucher_id
			    AND vh.current_version_id = vv.id
			   JOIN voucher_type    vt ON vt.id = vv.voucher_type_id
			  WHERE p.ledger_id = $1
			    AND vh.is_deleted = false
			    AND (
			         vt.parent_class = 'PURCHASE'
			      OR (vt.parent_class IN ('CREDIT_NOTE', 'DEBIT_NOTE')
			          AND EXISTS (SELECT 1 FROM ledger pl
			                        JOIN account_group pg ON pg.id = pl.group_id
			                       WHERE pl.id = vv.party_ledger_id
			                         AND pg.nature = 'LIABILITY'))
			    )
			    AND vv.voucher_date BETWEEN $2 AND $3`,
			ledgerID, from, to).Scan(&total)
		if err != nil {
			return decimal.Zero, fmt.Errorf("gstr: sum input-tax postings: %w", err)
		}
		return total, nil
	}

	out := TaxTotals{CGST: decimal.Zero, SGST: decimal.Zero, IGST: decimal.Zero}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if out.CGST, err = sum(ctx, tx, opts.InputCGSTLedgerID); err != nil {
			return err
		}
		if out.SGST, err = sum(ctx, tx, opts.InputSGSTLedgerID); err != nil {
			return err
		}
		if out.IGST, err = sum(ctx, tx, opts.InputIGSTLedgerID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return TaxTotals{}, err
	}
	return out, nil
}

// Reconciliation3B is the result of tying a generated GSTR-3B against the
// postings for the same period: 3.1(a) against the output-tax postings on Sales
// vouchers, and table 4C against the input-tax postings on Purchase vouchers.
type Reconciliation3B struct {
	// Outward is the 3.1(a) tax total from the return.
	Outward TaxTotals
	// OutwardPostings is the books side: output-tax ledger postings on SALES.
	OutwardPostings TaxTotals
	// ITC is the table-4C NET ITC from the return. Name and meaning unchanged.
	ITC TaxTotals
	// ITCPostings is the books side: input-tax ledger postings on PURCHASE.
	ITCPostings TaxTotals
	// ITCAvailable is sum(4A), ITCReversed is sum(4B) and ITCIneligible is
	// sum(4D). They exist because ITC alone can no longer be compared with
	// ITCPostings: once a reversal is possible, 4C is deliberately LESS than
	// what the books posted, and that difference is the correct answer rather
	// than a break.
	ITCAvailable  TaxTotals
	ITCReversed   TaxTotals
	ITCIneligible TaxTotals
}

// BalancedBooks is invariant (I1): every rupee of posted input tax lands in
// EXACTLY ONE of 4(A) and 4(D).
//
// This is the tie to the books, and it is the reason 4(A) reports what the
// BOOKS posted rather than what the supplier filed. It holds head by head and
// to the paisa, in every period, whatever the reconciliation concluded —
// reversals move credit out of 4(C), never out of 4(A). If this is false, the
// return and the trial balance disagree about what was paid.
func (r Reconciliation3B) BalancedBooks() bool {
	return r.ITCPostings.Equal(addTax(r.ITCAvailable, r.ITCIneligible))
}

// BalancedInternal is invariant (I2): 4(C) == sum(4A) - sum(4B), head by head.
// Validate() enforces the same identity on the return itself; checking it here
// as well means a Reconciliation3B assembled by hand (or by a future caller
// that does not go through Generate3B) cannot report "balanced" without it.
func (r Reconciliation3B) BalancedInternal() bool {
	return subTax(r.ITCAvailable, r.ITCReversed).Equal(r.ITC)
}

// Balanced reports whether the return and the books agree — the meaning it has
// always had, restated for a table 4 that can now reverse.
//
// It is deliberately NOT "4C == input-tax postings" any more. That comparison
// was correct only while nothing could be reversed; keeping it would turn a
// CORRECT reversal into a reconciliation failure, and the natural way to make
// that failure go away is to stop reversing. The restated pair — the books tie
// on (I1) and the arithmetic tie on (I2) — is exactly as strict, and both
// degenerate to the old check when there are no reversals and nothing
// ineligible.
func (r Reconciliation3B) Balanced() bool {
	return r.Outward.Equal(r.OutwardPostings) && r.BalancedBooks() && r.BalancedInternal()
}

// Reconcile3B generates the GSTR-3B for the period, sums the output-tax and
// input-tax postings, and returns both sides so the caller can assert they tie
// out. It is the integrity check that the return reflects the books.
func (s *Service) Reconcile3B(ctx context.Context, from, to string, opts Options3B) (GSTR3B, Reconciliation3B, error) {
	g, err := s.Generate3B(ctx, from, to, opts)
	if err != nil {
		return GSTR3B{}, Reconciliation3B{}, err
	}
	outPosted, err := s.PostingTaxTotals(ctx, from, to, opts.Options)
	if err != nil {
		return GSTR3B{}, Reconciliation3B{}, err
	}
	itcPosted, err := s.ITCPostingTotals(ctx, from, to, opts)
	if err != nil {
		return GSTR3B{}, Reconciliation3B{}, err
	}
	return g, Reconciliation3B{
		Outward:         g.OutwardTax(),
		OutwardPostings: outPosted,
		ITC:             g.ITCTax(),
		ITCPostings:     itcPosted,
		ITCAvailable:    sumTable4(g.T4.ItcAvl),
		ITCReversed:     sumTable4(g.T4.ItcRev),
		ITCIneligible:   sumTable4(g.T4.ItcInelg),
	}, nil
}
