package itc

import "sort"

// ITCEffect is what ONE reconciliation line does to GSTR-3B table 4. It is the
// only thing that crosses from this package into the return.
//
// Every rupee of posted input tax must land in EXACTLY ONE of Avail or Inelig,
// because that is what makes identity (I1) — sum(4A) + sum(4D) == input-tax
// postings — hold exactly. Reversals (RevRule, RevOther) are SUBTRACTED FROM
// Avail on the return, they are not an alternative destination for it.
type ITCEffect struct {
	// Avail is 4(A): ITC available. Under model (b) this is the BOOKS figure.
	Avail Tax
	// RCM is true when Avail belongs in 4(A)(3) "Inward supplies liable to
	// reverse charge" — the "ISRC" row — rather than 4(A)(5) "All other ITC".
	RCM bool
	// RevRule is 4(B)(1): reversal under rules 38, 42 and 43 and under s.17(5).
	RevRule Tax
	// RevOther is 4(B)(2): "Others" — the haircut for credit the supplier has
	// not filed, that the filer rejected or deferred in IMS, or that the books
	// claim in excess of what the supplier declared.
	RevOther Tax
	// Inelig is 4(D)(2): "Ineligible ITC under section 16(4) and ITC restricted
	// due to PoS provisions". Credit that NEVER WAS AVAILABLE. It is excluded
	// from Avail entirely, and it is NOT subtracted from 4(C) — 4(D) is "Other
	// Details" and informational in the post-Notification-14/2022 format.
	Inelig Tax
}

// ZeroEffect is the identity: a line that does nothing to the return.
func ZeroEffect() ITCEffect {
	return ITCEffect{Avail: ZeroTax(), RevRule: ZeroTax(), RevOther: ZeroTax(), Inelig: ZeroTax()}
}

// Add accumulates one effect into another, head by head. RCM is NOT combined —
// it is a per-line routing flag, and the aggregator splits on it.
func (e ITCEffect) Add(o ITCEffect) ITCEffect {
	return ITCEffect{
		Avail:    e.Avail.Add(o.Avail),
		RevRule:  e.RevRule.Add(o.RevRule),
		RevOther: e.RevOther.Add(o.RevOther),
		Inelig:   e.Inelig.Add(o.Inelig),
		RCM:      e.RCM,
	}
}

// itcEffect projects one reconciliation Line onto table 4. It is PURE and it is
// the heart of this package: everything else exists to give it correct inputs.
//
// ---------------------------------------------------------------------------
// THE EFFECT TABLE (each row has its own test)
// ---------------------------------------------------------------------------
//
//	| line                                    | 4(A)   | 4(B)(1) | 4(B)(2)  | 4(D)(2) |
//	|-----------------------------------------|--------|---------|----------|---------|
//	| MATCHED, eligible                       | full   |    -    |    -     |    -    |
//	| MATCHED_ROUNDING                        | full*  |    -    |    -     |    -    |
//	| MISMATCHED, books > statement           | full   |    -    | excess   |    -    |
//	| MISMATCHED, books < statement           | full   |    -    |    -     |    -    |
//	| MISSING_IN_2B, posted tax POSITIVE      | full   |    -    | full     |    -    |
//	| MISSING_IN_2B, posted tax NEGATIVE      | full   |    -    | ZERO     |    -    |
//	| NEAR_MATCH / AMBIGUOUS                  | full   |    -    | full     |    -    |
//	| IMS REJECTED or PENDING                 | full   |    -    | full     |    -    |
//	| statement itcavl="N" (s.16(4) or PoS)   | ZERO   |    -    |    -     | full    |
//	| our own s.16(4) time-bar, no statement  | ZERO   |    -    |    -     | full    |
//	| ledger/voucher BLOCKED_17_5             | full   | full*** |    -     |    -    |
//	| NOT_COMPARABLE / RCM                    | full** |    -    |    -     |    -    |
//	| NOT_COMPARABLE / other reasons          | full   |    -    |    -     |    -    |
//	| MISSING_IN_BOOKS                        | ZERO   |    -    |    -     |    -    |
//	| SUPERSEDED                              | ZERO   |    -    |    -     |    -    |
//
//	*   the BOOKS figure, not the statement's. The difference is a rounding
//	    artefact of who rounded where; the books figure is what was posted, and
//	    posting is what identity (I1) ties against.
//	**  into the ISRC row, not OTH.
//	*** SIGNED, not clamped — the same signed figure that went into 4(A). See
//	    "THE BLOCKED PURCHASE THAT IS RETURNED" below.
//
// ---------------------------------------------------------------------------
// PRECEDENCE, WHEN SEVERAL APPLY
// ---------------------------------------------------------------------------
//  1. 4(D) FIRST. Never-available beats everything and never enters 4(A) — that
//     is what keeps (I1) exact.
//  2. THEN 4(B)(1).
//  3. THEN 4(B)(2).
//
// AT MOST ONE 4(B) REVERSAL PER VOUCHER, AND 4(B)(1) WINS. A blocked credit that
// is ALSO unmatched is reversed ONCE. Reversing it twice halves the filer's
// working capital on a line nobody can trace back.
//
// ---------------------------------------------------------------------------
// THE STATEMENT'S OWN VERDICT OVERRIDES OURS
// ---------------------------------------------------------------------------
// When GSTN marks a document itcavl="N", GSTN's determination is the one the
// department will use, so it is applied. A DISAGREEMENT between GSTN's verdict
// and our own s.16(4) computation is recorded as FindingGSTNVerdictDisagrees and
// is NEVER silently resolved in either direction.
//
// ---------------------------------------------------------------------------
// THE NEGATIVE-TAX RULE, stated because it is not obvious
// ---------------------------------------------------------------------------
// A document whose posted input tax is NEGATIVE — a supplier credit note, a
// purchase return — IS NEVER REVERSED FOR BEING ABSENT FROM THE STATEMENT.
// Reversing a negative ADDS credit. The conservative direction for a missing
// credit note is to KEEP the reduction: we have already reduced our claim, and
// the supplier not having filed the note yet is no reason to give the credit
// back.
//
// The rule is not a special case in the code. It falls out of the single
// unifying identity below, which is the reason that identity is written this
// way:
//
//	reversal(4B2) = positivePart( booksTax - statementTaxInBooksDirection )
//
// with the statement side taken as ZERO when the line is unpaired. On a positive
// invoice that yields the full books tax; on a negative note it yields zero,
// because a negative minus zero is negative and the positive part of a negative
// is zero.
//
// ---------------------------------------------------------------------------
// THE TWO SUPPLIER-CREDIT-NOTE CASES. THEY LOOK ALIKE AND PULL OPPOSITE WAYS.
// ---------------------------------------------------------------------------
// This block and rule (4) of checkITCDecision in
// internal/compliance/gstr/gstr3b.go state the SAME decision. They are the two
// halves of one seam and they disagreed once, silently — this package computed
// the case-(b) correction and gstr refused it, so a company with one
// under-booked supplier credit note could not generate a GSTR-3B at all. If you
// change one, change the other; the paired regressions
// TestCreditNoteDeclaredLargerAgreesWithGSTR (this package) and
// TestCreditNoteDeclaredLargerAgreesWithITC (gstr) assert the same rupee figure
// on both sides so the drift cannot recur unnoticed.
//
//	(a) THE NOTE IS ABSENT FROM THE STATEMENT (MISSING_IN_2B). Books -300, no
//	    statement row. 4(A) carries -300 and the reversal is ZERO: the identity
//	    above gives positivePart(-300 - 0) = 0. Reversing anything here would
//	    hand back credit we had correctly given up.
//
//	(b) THE SUPPLIER DECLARED A LARGER NOTE THAN WE BOOKED (MISMATCHED). Books
//	    -300 against a statement -400. The identity gives
//	    positivePart(-300 - (-400)) = 100: a POSITIVE 4(B)(2) reversal sitting
//	    on a NEGATIVE 4(A), netting -300 - 100 = -400, which is exactly the
//	    statement's figure. Per GSTR-2B the credit for this document is 100
//	    lower than our books claim, and that 100 must come out; capping it to
//	    zero would leave credit in the return that the supplier has cancelled.
//	    (The mirror case, books -500 against a statement -400, is not an
//	    overclaim at all and yields zero.)
//
// THE CORRECTION RIDES IN 4(B)(2), NOT IN 4(A), and that is forced rather than
// chosen: 4(A) is the BOOKS figure under model (b), and gstr's partition rule
// requires Avail + Inelig to equal the posted input tax, so 4(A) is -300 and
// cannot be made -400 without breaking identity (I1). 4(C) = sum(4A) - sum(4B)
// then subtracts the 100 exactly once, so the netting is not double-counted.
//
// ---------------------------------------------------------------------------
// THE BLOCKED PURCHASE THAT IS RETURNED. 4(B)(1) IS SIGNED, NOT CLAMPED.
// ---------------------------------------------------------------------------
// This block and rule (4) of checkITCDecision in
// internal/compliance/gstr/gstr3b.go state the SAME decision, and the paired
// regressions TestBlockedPurchaseReturnedNetsToZero (this package) and
// TestBlockedPurchaseReturnedNetsToZeroAgreesWithITC (gstr) assert the same
// rupee figures on the same fixture. CHANGING ONE WITHOUT THE OTHER MAKES
// GSTR-3B GENERATION FAIL OUTRIGHT for any company with a returned blocked
// purchase, which is the same silent seam break the books-300/statement-400
// comment above records as having happened once already.
//
// A section 17(5) blocked purchase is availed in 4(A) and taken straight back
// out in 4(B)(1). Its NET effect on 4(C) is ZERO: the taxpayer never gets the
// credit. Both halves are the SAME figure, so both halves must carry the SAME
// SIGN.
//
//	(c) THE BLOCKED PURCHASE IS RETURNED. A blocked motor vehicle of
//	    10,00,000 at 18% avails +90,000 per head and reverses +90,000 per
//	    head; net zero. The supplier's credit note posts -90,000 per head, so
//	    4(A) falls by 90,000 and 4(B)(1) must fall by 90,000 with it —
//	    a NEGATIVE 4(B)(1) of -90,000 — for the pair to net to zero again.
//
//	    CLAMPING 4(B)(1) AT ZERO here (positivePart) leaves the +90,000
//	    reversal standing against a -90,000 availment. 4(C) becomes -90,000
//	    per head: Rs 1,80,000 of REAL credit destroyed, paid out of the
//	    electronic cash ledger, on a car the taxpayer returned and never took
//	    credit on. It is invisible to every gate in the file, because the
//	    partition identity (I1) still holds exactly — the identity that breaks
//	    is "a blocked document nets to zero", which nothing else asserts.
//
//	    It is equally wrong when the invoice and the note fall in DIFFERENT
//	    periods: the invoice's period nets zero, and the note's period must
//	    also net zero rather than refund a reversal that has nothing left to
//	    reverse.
//
// SO 4(B)(1) IS NOT THE POSITIVE PART OF ANYTHING. It is the signed books tax,
// exactly as 4(A) is. The general rule the two halves share, and which gstr's
// rule (4) enforces per voucher: A REVERSAL NEVER FLIPS A DOCUMENT'S NET
// CONTRIBUTION TO 4(C) TO THE OPPOSITE SIGN OF WHAT WAS POSTED. On a positive
// availment that bounds the reversal into [0, avail]; on a negative one it
// bounds it below by avail and leaves it UNBOUNDED ABOVE, which is what makes
// case (b)'s statement-driven positive correction legal on a negative 4(A).
//
// The 4(B)(2) "Others" haircut is a different animal and stays CLAMPED
// (reversal4B2 ends in PositivePart): it is a haircut for credit the supplier
// did not support, and a negative one would ADD credit out of nothing. Only
// 4(B)(1), which mirrors a figure that is already in 4(A) rupee for rupee, may
// go negative.
//
// ---------------------------------------------------------------------------
// THE RCM CARVE-OUT
// ---------------------------------------------------------------------------
// IMS excludes RCM supplies and RCM credit is SELF-ASSESSED, not supplier-filed,
// so an ISRC row can NEVER appear in a 2B match and MUST NOT be reversed for
// being unmatched. gstr3b.go's existing `if r.reverse { ...; continue }` early
// return IS this carve-out on the aggregation side; here it is
// notComparable()'s ReverseChargeReason. Without it the naive rule reverses 100%
// of RCM ITC. RCM input tax is ALSO posted to the input-tax ledgers (the
// existing 3B test's 540 == ISRC 360 + OTH 180 proves it), so it sits on both
// sides of identity (I1) and must stay in 4(A).
func (l Line) itcEffect(policy Policy) ITCEffect {
	e := ZeroEffect()

	// A statement-only line contributes NOTHING under model (b). 4(A) is what
	// the BOOKS posted; a document we never recorded posted nothing. It is
	// reported (MISSING_IN_BOOKS) so the filer chases it, but it moves no
	// figure. Superseded rows are statement-side too.
	//
	// THE DIRECTION OF THE ERROR IS OPPOSITE FOR A CREDIT NOTE, AND THAT IS A
	// DISCLOSED GAP RATHER THAN A SILENT ONE. An unbooked statement INVOICE
	// understates the claim — conservative, the filer's own problem. An unbooked
	// statement CREDIT NOTE OVERSTATES it: under IMS an un-actioned credit note
	// deem-accepts and is already netted out of the recipient's GSTR-2B, so the
	// department's auto-populated 4(C) is LOWER than this return's by the whole
	// of that note. Nothing here can fix it — there is no voucher to hang a
	// reversal on, and inventing one breaks gstr's partition rule (1) — so the
	// line raises FindingStatementCreditNoteNotInBooks and the reconciliation
	// raises DiscStmtCreditNoteNotInBooks. See EffectFindings below.
	if l.Books == nil {
		return e
	}
	b := *l.Books
	booksTax := b.Tax

	// -----------------------------------------------------------------
	// 1. 4(D)(2): credit that NEVER WAS AVAILABLE. Excluded from 4(A).
	// -----------------------------------------------------------------
	if neverAvailable(l, policy) {
		e.Inelig = booksTax
		return e
	}

	// -----------------------------------------------------------------
	// 2. 4(A): the books figure, always. Model (b).
	// -----------------------------------------------------------------
	e.Avail = booksTax
	e.RCM = b.ReverseCharge

	// -----------------------------------------------------------------
	// 3. AT MOST ONE 4(B) REVERSAL, AND 4(B)(1) WINS.
	// -----------------------------------------------------------------
	if b.ITCEligibility == EligibilityBlocked {
		// SIGNED, deliberately — the same figure that just went into 4(A), so a
		// blocked document nets to exactly zero whether it is a purchase or the
		// credit note that returns one. PositivePart() here destroys the whole
		// tax on any returned blocked purchase. See "THE BLOCKED PURCHASE THAT
		// IS RETURNED" above and rule (4) of gstr's checkITCDecision.
		e.RevRule = booksTax
		return e
	}

	e.RevOther = reversal4B2(l, policy)
	return e
}

// neverAvailable decides the 4(D)(2) branch: credit that was never available at
// all, as opposed to credit that is available and then reversed.
func neverAvailable(l Line, policy Policy) bool {
	// GSTN's own verdict, when we have one, is decisive. It is set for exactly
	// two reasons — s.16(4) and the place-of-supply rule — and GSTN expressly
	// disclaims the rest, which is why it is read as "never available" and not
	// as a general eligibility opinion.
	if l.Statement != nil && !l.Statement.Rejected && !l.Statement.ITCAvailable {
		return true
	}
	// Our own s.16(4) computation, used when no statement row says otherwise.
	// It runs on the SUPPLIER'S DOCUMENT DATE where we have one — the statutory
	// date — and falls back to the voucher date where we do not.
	if l.Books == nil {
		return false
	}
	d := l.Books.SupplierDocDate
	if d.IsZero() {
		d = l.Books.VoucherDate
	}
	// THE SECOND DATE IS ClaimInstant, NOT Policy.AsOn — when the credit was
	// CLAIMED, not when this report is being generated. s.16(4) bars a credit
	// TAKEN after 30 November following the document's financial year; it says
	// nothing about when somebody prints the return. With AsOn here, asking for
	// a past period's GSTR-3B after that 30 November moves ONE HUNDRED PERCENT
	// of the period's ITC out of 4(A) into 4(D)(2) and collapses 4(C) to zero,
	// including for periods already filed with the department.
	// TestFiledPeriodReportIsStableWhenRegeneratedYearsLater bites that.
	claimOn := policy.ClaimInstant()
	if d.IsZero() || claimOn.IsZero() {
		return false
	}
	return Sec16_4Barred(d, claimOn, policy.AnnualReturnFiledOn)
}

// reversal4B2 is the 4(B)(2) "Others" haircut. See the unifying identity in the
// itcEffect comment.
func reversal4B2(l Line, policy Policy) Tax {
	b := *l.Books

	// The RCM carve-out and the data-completeness carve-out. Neither is a
	// failure of the supplier to file, so neither reverses anything.
	if b.ReverseCharge {
		return ZeroTax()
	}
	if l.Status == NotComparable {
		return ZeroTax()
	}

	// IMS FIRST, BEFORE THE MATCH VERDICT. A record the filer REJECTED or kept
	// PENDING yields no credit in this period whatever the match said, so the
	// statement side of the subtraction collapses to zero and the whole books
	// figure is reversed.
	//
	// THE ORDER HERE IS LOAD-BEARING. Written the other way round — the match
	// verdict first, with MATCHED short-circuiting to zero — a REJECTED document
	// that matches the books perfectly (which is the ORDINARY case: you reject a
	// document precisely because you can see it, and the one you can see is the
	// one that matched) would be reversed by NOTHING. That is the whole IMS
	// feature silently not working, on exactly the documents it exists for.
	// TestEffectTable/ims_rejected_or_pending_reverses_in_full caught this.
	if l.IMSState != "" && !l.IMSState.AvailableForITC() {
		return b.Tax.PositivePart()
	}

	// The statement side of the subtraction, in the BOOKS' direction. Zero on an
	// unpaired line.
	stmtSide := ZeroTax()
	switch l.Status {
	case Matched, MatchedRounding:
		// Exact, or exact but for rounding. Nothing is reversed — and
		// MATCHED_ROUNDING short-circuits deliberately, because the unifying
		// subtraction would otherwise reverse the sub-rupee delta and produce a
		// 4(B)(2) line of eighty paise that nobody can explain.
		return ZeroTax()
	case Mismatched:
		// THIS IS CASE (b) OF THE TWO CREDIT-NOTE CASES ABOVE. The statement
		// reports magnitudes, so it is flipped into the BOOKS' direction first:
		// a supplier credit note of 400 becomes -400, and books -300 less -400
		// is a +100 reversal on a -300 availment. gstr's checkITCDecision rule
		// (4) deliberately applies NO upper bound when the availment is
		// negative, precisely so this correction survives into the return.
		sgn := int64(1)
		if b.DocType.Sense() == "-" {
			sgn = -1
		}
		stmtSide = l.Statement.Tax.Abs().Scale(sgn)
	case NearMatch, Ambiguous, MissingIn2B:
		// NEAR-MATCH AND AMBIGUOUS ARE TREATED AS UNMATCHED, conservatively.
		// A candidate is not a pairing (see Match); until a human confirms it,
		// the credit is unsupported.
		stmtSide = ZeroTax()
	default:
		stmtSide = ZeroTax()
	}

	return b.Tax.Sub(stmtSide).PositivePart()
}

// ---------------------------------------------------------------------------
// RULE 42 / 43 IS NOT IMPLEMENTED HERE. THAT IS DELIBERATE.
// ---------------------------------------------------------------------------
// The apportionment formula lives in compliance/gstr (gstr3b.go), which owns
// table 4(B)(1) and receives the caller-supplied inputs on Options3B.ITCPolicy.
// It is NOT restated in this package, for exactly the reason NormalizeDocNo is
// the only statement of the document-number rule: A FORMULA STATED TWICE IS A
// FORMULA THAT DRIFTS, and the drift here would be two different reversal
// figures for one taxpayer with nothing to say which is right.
//
// Rule 42/43 is also PERIOD-level, not per-voucher: D1 = (E/F) x C2 is a
// function of the whole period's exempt supply and turnover, so it cannot be an
// ITCDecision about a voucher even in principle. Policy.Rule4243Supplied
// therefore carries only what THIS package needs to know — whether the caller
// supplied the inputs — so that the `rule-42-43-not-computed` disclosure is
// emitted when they did not.
//
// What is NEVER done, on either side of that boundary: E (exempt supply value)
// and F (total turnover in the State) are never DERIVED from our own return.
// gstr3b.go hard-zeroes 3.1(b)/(c)/(e) because the books carry no export/SEZ
// marker, so E is exactly the number this software does not have and F is not
// modelled at all. An engine fed E = 0 returns D1 = 0 for EVERY taxpayer and
// looks like it works.

// ---------------------------------------------------------------------------
// Aggregation and the disclosure vocabulary
// ---------------------------------------------------------------------------

// Totals is the whole reconciliation rolled up into the four table-4 buckets,
// with 4(A) split into the ISRC (reverse charge) and OTH rows GSTR-3B wants.
type Totals struct {
	AvailISRC Tax
	AvailOTH  Tax
	RevRule   Tax
	RevOther  Tax
	Inelig    Tax
}

// Totalise rolls the per-line effects up into the four table-4 buckets.
//
// It does NOT add a rule 42/43 component: that is period-level, it is not a
// function of any line, and its formula lives in compliance/gstr.
func Totalise(lines []Line, policy Policy) Totals {
	t := Totals{AvailISRC: ZeroTax(), AvailOTH: ZeroTax(), RevRule: ZeroTax(), RevOther: ZeroTax(), Inelig: ZeroTax()}
	for _, l := range lines {
		if l.Effect.RCM {
			t.AvailISRC = t.AvailISRC.Add(l.Effect.Avail)
		} else {
			t.AvailOTH = t.AvailOTH.Add(l.Effect.Avail)
		}
		t.RevRule = t.RevRule.Add(l.Effect.RevRule)
		t.RevOther = t.RevOther.Add(l.Effect.RevOther)
		t.Inelig = t.Inelig.Add(l.Effect.Inelig)
	}
	// NO rule 42/43 component is added here. It is period-level and it belongs
	// to compliance/gstr, which owns the formula. See the note above Totals.
	return t
}

// Net is 4(C): sum(4A) - sum(4B), head by head. A NEGATIVE 4(C) IS LEGAL —
// reversals may exceed availment in a period — and is returned as-is, never
// clamped.
func (t Totals) Net() Tax {
	return t.AvailISRC.Add(t.AvailOTH).Sub(t.RevRule).Sub(t.RevOther)
}

// PostedTotal is sum(4A) + sum(4D), the left-hand side of identity (I1). It must
// equal the input-tax POSTINGS for the period, head by head, exactly.
func (t Totals) PostedTotal() Tax {
	return t.AvailISRC.Add(t.AvailOTH).Add(t.Inelig)
}

// The CLOSED disclosure vocabulary. "Not filing-ready" is a FIELD, not prose in
// a header nobody reads, so that a frontend can REFUSE to present the return as
// final rather than hoping the operator noticed a paragraph.
const (
	DiscNoInwardStatement   = "no-inward-statement"
	DiscRule4243NotComputed = "rule-42-43-not-computed"
	DiscEligibilityNotSet   = "itc-eligibility-not-classified"
	Disc4D1ReclaimNotModel  = "4d1-reclaim-not-modelled"
	DiscReversalsNotPosted  = "reversals-not-posted-to-books"
	DiscStatementPartial    = "statement-partially-parsed"
	DiscCessNotModelled     = "cess-not-modelled"
	DiscPortalSchemaUnverif = "portal-schema-unverified"
	// DiscStmtCreditNoteNotInBooks — the statement carries at least one supplier
	// CREDIT NOTE the books do not, so 4(C) is HIGHER than the department's own
	// auto-populated figure by the whole of that note. See EffectFindings.
	DiscStmtCreditNoteNotInBooks = "statement-credit-note-not-in-books"
	DiscNotFilingReady           = "not-filing-ready"
)

// DiscSec16_6NotModelled is MIRRORED IN gstr.KnownDisclosures, exactly like the
// ten tokens above. It is declared here, apart from that block, only because it
// arrived later; the two spellings are tied together by
// TestDisclosureVocabularyMatchesGSTR, which fails in both directions.
//
// s.16(6) — credit for the period during which a registration stood cancelled,
// where the cancellation was later revoked — turns entirely on registration-
// status data this system does not hold (see the s.16(6) block at the foot of
// clock.go). It is not implemented, and under this package's rule that whatever
// is NOT implemented must produce a token rather than silence, it is disclosed
// unconditionally.
//
// IT REACHES THE RETURN THE FILER SIGNS, AND THAT IS THE WHOLE POINT. A caller
// forwards Report.Disclosures verbatim into gstr.Options3B.ITCPolicy.
// SourceDisclosures — see handleGSTR3B in internal/server/handler_gstr3b.go —
// and gstr now accepts this token and reproduces it in GSTR3B.Disclosures. It
// was previously declared HERE ONLY, and the production handler responded by
// FILTERING it out of the forwarded list, so the one 3B a filer actually signs
// was the one document that could not show the gap. That filter is gone: gstr
// still REJECTS a token it does not know, with ErrInvalid3B naming the token, so
// a FUTURE itc-local token is a loud 422 rather than a silent omission — which
// is the trade this package always makes against a silently wrong number.
//
// The gap's direction is an UNDER-claim, not an over-claim, and that is not a
// reason to keep it quiet: a filer whose registration was cancelled and restored
// pays tax they do not owe unless they check those periods by hand, and they
// cannot know to do that from a return that says nothing.
const DiscSec16_6NotModelled = "sec-16-6-revocation-not-modelled"

// FindingStatementCreditNoteNotInBooks: GSTR-2B carries a supplier CREDIT NOTE
// that the books do not. Under model (b) it moves NO figure — 4(A) is what the
// books posted — so the return claims credit the supplier has already
// cancelled.
//
// It is a FindingCode rather than a MatchStatus because MISSING_IN_BOOKS is
// already the correct verdict; what this adds is that THIS PARTICULAR
// missing-in-books row inflates the claim rather than deflating it.
const FindingStatementCreditNoteNotInBooks FindingCode = "STATEMENT_CREDIT_NOTE_NOT_IN_BOOKS"

// EffectFindings returns the findings that are a property of a line's EFFECT ON
// THE RETURN rather than of the match itself.
//
// ---------------------------------------------------------------------------
// WHY THIS IS A SEPARATE ACCESSOR AND NOT SIMPLY Line.Findings
// ---------------------------------------------------------------------------
// Line.Findings is assembled in match.go from what the MATCH saw. This finding
// is not a fact about the match — MISSING_IN_BOOKS is the right verdict and the
// match is behaving correctly — it is a fact about what model (b) then does with
// that verdict, which is decided here. Appending it to Line.Findings inside
// Match is a ONE-LINE WIRING CHANGE in a file this change does not own; it is
// recorded as a handoff. Until then the fact still reaches the operator, because
// Disclosures (below) raises DiscStmtCreditNoteNotInBooks from it and that token
// is on Report.Disclosures, which is the field a frontend must read before
// presenting a return as final.
//
// ---------------------------------------------------------------------------
// WHAT THIS DOES NOT DO
// ---------------------------------------------------------------------------
// IT DOES NOT CORRECT THE NUMBER. The honest correction is a PERIOD-LEVEL
// adjustment in gstr's build3B, alongside the rule 42/43 component: there is no
// voucher to hang an ITCDecision on, and giving the row a 4(B)(2) reversal
// breaks gstr's partition rule (1) — Available + Ineligible == posted input tax
// — because nothing was posted. That gap is recorded in gstr3b.go's disclosed
// limitations and in this package's handoffs. 4(C) REMAINS OVERSTATED by the tax
// on every such note; this is the disclosure that says so.
//
// NEAR_MATCH statement-only rows are included alongside MISSING_IN_BOOKS: a
// candidate is not a pairing, so such a note is equally absent from the books
// and inflates the claim identically. SUPERSEDED rows are excluded — they are
// replaced by another row in the same statement, and reporting both would name
// the same note twice.
func (l Line) EffectFindings() []Finding {
	if l.Books != nil || l.Statement == nil {
		return nil
	}
	if l.Status != MissingInBooks && l.Status != NearMatch {
		return nil
	}
	if l.Statement.DocType.Sense() != "-" {
		return nil
	}
	strict, _ := NormalizeDocNo(l.Statement.DocNo)
	return []Finding{{
		Code:      FindingStatementCreditNoteNotInBooks,
		Side:      SideStatement,
		DocNo:     l.Statement.DocNo,
		StrictKey: strict,
	}}
}

// Disclosures builds the disclosure list for a reconciliation, sorted,
// deduplicated and deterministic. DiscNotFilingReady is emitted whenever ANY
// other token is present — which, in this wave, is always: DiscReversalsNotPosted
// and Disc4D1ReclaimNotModel are unconditional properties of the design, not
// symptoms of missing data.
func Disclosures(lines []Line, haveStatement bool, policy Policy, extra []string) []string {
	set := map[string]bool{
		// Unconditional, because they are design decisions rather than data gaps:
		DiscReversalsNotPosted:  true,
		Disc4D1ReclaimNotModel:  true,
		DiscCessNotModelled:     true,
		DiscPortalSchemaUnverif: true,
		// Unconditional because the gap is unconditional: no reconciliation this
		// software produces has ever considered s.16(6), and the registration
		// history it would need is not in the schema at all. s.16(5) IS
		// implemented (Sec16_4Expiry); 16(6) is not, and the difference must be
		// visible rather than inferred from its absence.
		DiscSec16_6NotModelled: true,
	}
	if !haveStatement {
		set[DiscNoInwardStatement] = true
	}
	if !policy.Rule4243Supplied {
		set[DiscRule4243NotComputed] = true
	}
	for _, l := range lines {
		if l.Status == NotComparable &&
			(l.NotComparable == NoSupplierDocNo || l.NotComparable == NoPartyGSTIN) {
			set[DiscEligibilityNotSet] = true
		}
		// A supplier credit note the books never recorded leaves 4(C) HIGHER
		// than the department's own auto-populated figure. It is not corrected
		// (see EffectFindings) and it must therefore not be silent.
		for _, f := range l.EffectFindings() {
			if f.Code == FindingStatementCreditNoteNotInBooks {
				set[DiscStmtCreditNoteNotInBooks] = true
			}
		}
	}
	for _, e := range extra {
		if e != "" {
			set[e] = true
		}
	}
	if len(set) > 0 {
		set[DiscNotFilingReady] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// The per-voucher decision handed to the return
// ---------------------------------------------------------------------------

// Decision is what ONE voucher does to GSTR-3B table 4, plus the verdict that
// explains why. It is the value gstr_adapter.go copies field-for-field into
// gstr.ITCDecision.
//
// It exists as a package-local type — rather than this package simply returning
// gstr's type from Match — so that the PROJECTION LOGIC is testable without the
// gstr dependency, and so the calculation core stays a leaf. Line.Decision() is
// plain Go with no build tag; only the twenty lines that rename its fields into
// gstr's vocabulary are tagged.
type Decision struct {
	// Status is the reconciliation verdict, carried through so the return can
	// explain a reversal rather than merely making one.
	Status MatchStatus
	// RCM routes Avail to 4(A)(3) "ISRC" rather than 4(A)(5) "OTH".
	RCM      bool
	Avail    Tax
	RevRule  Tax
	RevOther Tax
	Inelig   Tax
}

// Decision projects a Line onto the per-voucher decision.
//
// It is a straight copy of the already-computed Effect plus the Status. It is a
// named method rather than an inline literal in the adapter for one reason: the
// adapter is build-tagged until slice 3 lands, and a projection that only exists
// inside a file nobody compiles is a projection nobody has tested.
func (l Line) Decision() Decision {
	return Decision{
		Status:   l.Status,
		RCM:      l.Effect.RCM,
		Avail:    l.Effect.Avail,
		RevRule:  l.Effect.RevRule,
		RevOther: l.Effect.RevOther,
		Inelig:   l.Effect.Inelig,
	}
}
