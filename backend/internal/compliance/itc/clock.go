package itc

import (
	"fmt"
	"strconv"
	"time"
)

// IST is the zone every GST date boundary in this package is evaluated in.
//
// INFERRED, NOT CITED. No GSTN or CBIC document read for this work states a
// timezone anywhere — not the 2B advisory, not the IMS FAQ, not the returns
// rules. Indian statute plus an IST-operated portal make it the only defensible
// reading, and it is recorded here as an assumption rather than presented as a
// citation.
//
// time.FixedZone, NOT time.LoadLocation("Asia/Kolkata"): a scratch container
// built without tzdata makes LoadLocation return an error, and the natural
// fallback (ignore the error, use time.UTC or time.Local) SILENTLY MOVES THE
// BOUNDARY by five and a half hours. India has run a fixed +05:30 with no
// daylight saving since 1945, so a fixed zone is exact for every date this
// software will ever be asked about, and it cannot fail to load.
//
// NEVER time.Local: a UTC-configured server computes the boundary 5h30m LATE
// and deem-accepts records into the WRONG TAX PERIOD — a filer who acted on the
// 14th sees their action ignored and a supplier's invoice silently accepted
// into a return they already filed. NEVER time.UTC, for the same reason.
// TestDeemedAcceptBoundary/utc_server_sees_the_same_instant is the subtest that
// bites both substitutions.
var IST = time.FixedZone("IST", 5*3600+30*60)

// Period is a GST tax period: a calendar month, the grain everything on the
// return side is keyed by. It is a (year, month) pair and NOT a time.Time,
// because every operation this package performs on a period is CALENDAR-MONTH
// arithmetic — "the month after December 2025" — and doing that with
// time.AddDate on a day-of-month is how February produces a 3 March.
type Period struct {
	Year  int
	Month time.Month
}

// ParsePeriod reads GSTN's "MMYYYY" return-period form, e.g. "012026" for
// January 2026. It is strict: six digits, a month in 1..12. A period is the key
// a whole return hangs off, so a malformed one must be an error and never a
// best-effort guess.
func ParsePeriod(s string) (Period, error) {
	if len(s) != 6 {
		return Period{}, fmt.Errorf("itc: return period %q is not MMYYYY", s)
	}
	for i := 0; i < 6; i++ {
		if s[i] < '0' || s[i] > '9' {
			return Period{}, fmt.Errorf("itc: return period %q is not MMYYYY", s)
		}
	}
	mm, _ := strconv.Atoi(s[0:2])
	yyyy, _ := strconv.Atoi(s[2:6])
	if mm < 1 || mm > 12 {
		return Period{}, fmt.Errorf("itc: return period %q has month %02d", s, mm)
	}
	return Period{Year: yyyy, Month: time.Month(mm)}, nil
}

// PeriodOf returns the tax period a date falls in, evaluated in IST.
func PeriodOf(t time.Time) Period {
	t = t.In(IST)
	return Period{Year: t.Year(), Month: t.Month()}
}

// String renders the period in GSTN's "MMYYYY" form.
func (p Period) String() string { return fmt.Sprintf("%02d%04d", int(p.Month), p.Year) }

// AddMonths advances the period by n calendar months (n may be negative). This
// is the ONLY way this package moves between periods. Day arithmetic is banned
// here by construction: there is no day to get wrong.
func (p Period) AddMonths(n int) Period {
	total := p.Year*12 + int(p.Month) - 1 + n
	return Period{Year: total / 12, Month: time.Month(total%12 + 1)}
}

// Day returns midnight IST on the given day-of-month of this period.
//
// It is used ONLY with days that exist in every month (14 and 20), so it never
// needs clamping — and that is a deliberate constraint on the callers, not an
// accident. February is the standing trap in date code, and the way this package
// avoids it is by never anchoring on a day past 28.
func (p Period) Day(d int) time.Time {
	return time.Date(p.Year, p.Month, d, 0, 0, 0, 0, IST)
}

// Start is midnight IST on the first day of the period.
func (p Period) Start() time.Time { return p.Day(1) }

// DeemedAcceptCutoff is the DEFAULT resolution instant for a monthly filer: the
// instant at which, absent any IMS action, a record is treated as ACCEPTED.
//
// GSTN's GSTR-2B Advisory (12-10-2021) Q.2 says 2B generation starts after the
// IFF / GSTR-5 / GSTR-6 due date ends at "13th midnight" of the month FOLLOWING
// the tax period. That is encoded here as the HALF-OPEN boundary
// [ .. , 14th 00:00:00 IST ) so there is no ambiguous 59-second gap at
// 23:59:59 and no half-day fudge about "the afternoon of the 14th": an action at
// 13th 23:59:59.999999999 IST counts, an action at 14th 00:00:00.000000000 IST
// does not.
//
// Calendar-month addition, NEVER day arithmetic: Dec-2025 -> 14-Jan-2026. No
// clamping is ever required — the 14th exists in every month, February included.
//
// THIS IS A DEFAULT, NOT A STATUTE. Use ResolutionInstant, which prefers the
// ingested statement's own generation date, wherever a statement exists.
func DeemedAcceptCutoff(p Period) time.Time { return p.AddMonths(1).Day(14) }

// GSTR3BDueDate is midnight IST on the 20th of the month following the tax
// period — the ordinary monthly GSTR-3B due date. It anchors the one-tax-period
// PENDING expiry introduced from October 2025 (see PendingAllowed), which is
// stated in the advisory as "the due date of GSTR-3B of the next period" and
// must therefore be computed by CALENDAR-MONTH addition on the 20th, not by
// adding thirty days to something.
//
// QRMP quarterly filers and the state-wise staggered due dates (22nd/24th) are
// NOT modelled. Every taxpayer is treated as a monthly 20th filer. Disclosed.
func GSTR3BDueDate(p Period) time.Time { return p.AddMonths(1).Day(20) }

// ResolutionInstant is the instant at which NO_ACTION resolves to
// DEEMED_ACCEPTED for a given tax period's draft 2B.
//
// It PREFERS the ingested statement's own gen_date over the computed 14th,
// because THE 14th IS NOT STATUTORY AND HAS ALREADY SLIPPED. GSTR-2B for
// December 2024 was generated on 16 January 2025, because the GSTR-1 due date
// for that period had been extended to the 13th. Software that hard-codes 14
// deem-accepts two days early and puts a record in a period the portal did not.
//
// The statement's date is taken at midnight IST on the day it names: the
// statement records a DATE (gendt), not an instant, and the whole day it names
// is on the far side of the boundary.
func ResolutionInstant(p Period, statementGenDate *time.Time) time.Time {
	if statementGenDate != nil {
		d := statementGenDate.In(IST)
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, IST)
	}
	return DeemedAcceptCutoff(p)
}

// State is the effective IMS state of one supplier document. There are FOUR,
// not three, and the fourth is the point: NO_ACTION is distinct from ACCEPTED
// until 2B generation RESOLVES it. Collapsing them means a filer who has not yet
// looked at their IMS dashboard is told their credit is confirmed.
type State string

const (
	// NoAction: nothing has been done and the resolution instant has not passed.
	NoAction State = "NO_ACTION"
	// Accepted: an explicit ACCEPT is the latest counted action.
	Accepted State = "ACCEPTED"
	// Rejected: an explicit REJECT is the latest counted action. Rejected credit
	// is not available and is reversed.
	Rejected State = "REJECTED"
	// Pending: explicitly deferred to a later period. The credit is not
	// available in THIS period.
	Pending State = "PENDING"
	// DeemedAccepted: no action was taken and the resolution instant has passed.
	// GSTN treats this identically to ACCEPTED for ITC; it is reported
	// separately because "the filer accepted this" and "the filer never looked"
	// are different facts about the same number.
	DeemedAccepted State = "DEEMED_ACCEPTED"
)

// Action kinds, matching gst_ims_action.action's CHECK constraint.
const (
	ActionAccept  = "ACCEPT"
	ActionReject  = "REJECT"
	ActionPending = "PENDING"
	// ActionReset withdraws whatever was done, returning the record to
	// no-action — after which deemed acceptance applies again if the resolution
	// instant has passed. It is NOT "accept": a reset before the 14th leaves the
	// record genuinely un-actioned.
	ActionReset = "RESET"
)

// Action is one row of the append-only gst_ims_action log.
//
// The log is APPEND-ONLY and is NEVER UPDATEd. The effective action is a
// last-write-wins PROJECTION over it (GSTN IMS FAQ 12: "the latest action will
// overwrite the previous action"). Storing the effective action instead would
// destroy the only evidence of WHEN the filer acted, and "when" is exactly what
// the freeze rule turns on.
type Action struct {
	Kind    string
	ActedAt time.Time
	Reason  string
	ActedBy string
}

// EffectiveAction projects the action log onto the four states, as at `asOn`.
//
// PURITY IS THE POINT: asOn is EXPLICIT and there is no time.Now() in this
// package. A boundary that reads the wall clock cannot be tested at the
// boundary, and every boundary here decides which tax period a credit lands in.
//
// TWO INSTANTS, NOT ONE:
//
//   - resolution — when the 2B is generated. Absent any action, the record
//     resolves to DEEMED_ACCEPTED at that instant, FOR THAT DRAFT. Before it,
//     the record is genuinely un-actioned. A single "deemed accept on the 14th"
//     boolean produces a wrong 3B for everyone who acts on the 15th, which is
//     most filers.
//   - freeze — gst_return_filing.filed_at for (GSTR3B, period). After the return
//     is filed no further action counts (GSTN IMS FAQ 12). Between generation
//     and filing the 2B is recomputable without limit (FAQ 27) and recomputation
//     is MANDATORY once any action has changed (IMS advisory, key point 2).
//
// Ties on ActedAt are broken by ORDER IN THE SLICE, and callers must load the
// log ordered by (acted_at, id) — two actions with the same timestamp are a real
// possibility on a bulk accept, and "whichever the planner returned first" is
// not an answer.
func EffectiveAction(actions []Action, resolution time.Time, freeze *time.Time, asOn time.Time) State {
	limit := asOn
	if freeze != nil && freeze.Before(limit) {
		limit = *freeze
	}

	var latest *Action
	for i := range actions {
		a := actions[i]
		if a.ActedAt.After(limit) {
			continue
		}
		if latest == nil || !a.ActedAt.Before(latest.ActedAt) {
			latest = &actions[i]
		}
	}

	if latest != nil {
		switch latest.Kind {
		case ActionAccept:
			return Accepted
		case ActionReject:
			return Rejected
		case ActionPending:
			return Pending
		case ActionReset:
			// Fall through to the un-actioned branch: a RESET does not accept,
			// it un-does. If the resolution instant has since passed the record
			// deem-accepts, exactly as one that was never touched would.
		default:
			// An unknown action kind is treated as no action rather than as an
			// acceptance. The CHECK constraint on gst_ims_action makes this
			// unreachable through the service; it is here so a future action
			// kind added to the schema and not to this switch fails SAFE
			// (unclaimed credit, reported) rather than fails OPEN (credit
			// claimed on a state nobody has implemented).
		}
	}

	if !asOn.Before(resolution) {
		return DeemedAccepted
	}
	return NoAction
}

// AvailableForITC reports whether a state permits the credit to be claimed in
// THIS period. ACCEPTED and DEEMED_ACCEPTED do; REJECTED and PENDING do not.
//
// NO_ACTION does: before 2B generation the credit is not yet withheld by
// anything, and under model (b) the books figure stands until something reverses
// it. Treating NO_ACTION as unavailable would reverse every purchase in a
// mid-month draft return.
func (s State) AvailableForITC() bool {
	switch s {
	case Rejected, Pending:
		return false
	default:
		return true
	}
}

// ---------------------------------------------------------------------------
// s.16(4): when the credit expires
// ---------------------------------------------------------------------------

// FYEnd returns 31 March of the financial year the given date falls in,
// evaluated in IST. The Indian financial year runs 1 April to 31 March, so a
// date in January 2026 belongs to FY 2025-26 and ends on 31 March 2026, while a
// date in April 2026 belongs to FY 2026-27 and ends on 31 March 2027.
func FYEnd(d time.Time) time.Time {
	d = d.In(IST)
	y := d.Year()
	if d.Month() >= time.April {
		y++
	}
	return time.Date(y, time.March, 31, 0, 0, 0, 0, IST)
}

// sec16_5AmnestyLastDate is the thirtieth day of November 2021 — the single
// date s.16(5) names for all four amnesty years. See Sec16_5Applies.
var sec16_5AmnestyLastDate = time.Date(2021, time.November, 30, 0, 0, 0, 0, IST)

// The four financial years s.16(5) covers, expressed as the YEAR THE FINANCIAL
// YEAR ENDS IN, because that is what FYEnd returns and re-deriving "FY 2017-18"
// from a start year is one off-by-one away from destroying a year of credit.
//
//	FY 2017-18 ends 31-Mar-2018 ... FY 2020-21 ends 31-Mar-2021.
const (
	sec16_5FirstFYEndYear = 2018
	sec16_5LastFYEndYear  = 2021
)

// Sec16_5Applies reports whether the section 16(5) amnesty covers a document
// dated docDate — that is, whether the document pertains to one of the FOUR
// financial years 2017-18, 2018-19, 2019-20 or 2020-21.
//
// [CITED-PRIMARY] s.16(5) CGST Act 2017, inserted by the Finance (No. 2) Act
// 2024. EFFECTIVE-FROM 27-09-2024 (the commencement notified for the amending
// section), WITH RETROSPECTIVE EFFECT FROM 01-07-2017:
//
//	"Notwithstanding anything contained in sub-section (4), in respect of an
//	invoice or debit note ... pertaining to the Financial Years 2017-18,
//	2018-19, 2019-20 and 2020-21, the registered person shall be entitled to
//	take input tax credit in any return under section 39 which is filed upto
//	the thirtieth day of November, 2021."
//
// THE EFFECTIVE-FROM IS RETROSPECTIVE, SO THERE IS NOTHING TO EFFECTIVE-DATE
// AGAINST. Unlike pendingRelaxationFrom, this rule does not switch on the tax
// period being reconciled: Parliament wrote it back to 01-07-2017, so there is
// no period in which the OLD answer is the right one. It is therefore a function
// of the DOCUMENT'S financial year alone, and takes no `asOn`.
//
// It uses the DOCUMENT'S OWN DATE, via FYEnd, for the same reason Sec16_4Expiry
// does: a debit note has been delinked from its underlying invoice since
// 01-01-2021, and "pertaining to" a financial year means the note's own year.
func Sec16_5Applies(docDate time.Time) bool {
	y := FYEnd(docDate).Year()
	return y >= sec16_5FirstFYEndYear && y <= sec16_5LastFYEndYear
}

// Sec16_4Expiry returns the LAST DAY (inclusive, midnight IST) on which the
// credit on a document dated docDate may still be claimed: the earlier of
//
//	30 November following the end of the document's financial year, and
//	the date the annual return for that year was actually filed,
//
// EXCEPT for the four financial years the s.16(5) amnesty revived, for which it
// is the LATER of that ordinary date and 30 November 2021.
//
// THE "30 NOVEMBER" IS ITSELF EFFECTIVE-DATED, AND THE EARLIER TEST IS NOT
// MODELLED. [CITED-PRIMARY] s.16(4) CGST Act 2017 as amended by the Finance Act
// 2022, s.100, whose commencement was notified w.e.f. 01-10-2022 (Notification
// 18/2022-Central Tax). EFFECTIVE-FROM 01-10-2022. BEFORE that commencement
// s.16(4) read "...the due date of furnishing of the return under section 39 for
// the month of September following the end of financial year...", i.e. the
// GSTR-3B due date for September (ordinarily 20 October), not 30 November. This
// function applies the POST-2022 wording to every year. That is DELIBERATE and
// it is safe only because of s.16(5): every financial year for which the older,
// earlier test could still bite — FY 2017-18 through FY 2020-21 — is inside the
// amnesty window below and gets 30 November 2021 regardless, which is later than
// either reading. FY 2021-22 is the first year outside the amnesty, and the
// amended wording was in force for the whole of the period in which its credit
// had to be taken. So there is no reachable date on which the un-modelled older
// wording gives a different answer. If a future change narrows the amnesty
// window, THIS PARAGRAPH BECOMES WRONG and the September due date must be
// modelled.
//
// A DEBIT NOTE'S OWN DATE FIXES THE FINANCIAL YEAR. Debit notes were DELINKED
// from the underlying invoice with effect from 01-01-2021, so a debit note dated
// 10 April 2026 raised against a January 2026 invoice expires on 30 November
// 2027, not 30 November 2026. Getting this backwards silently destroys a year of
// credit on every late debit note, and the filer has no way to see why.
//
// annualReturnFiledOn may be nil (the ordinary case: not yet filed).
func Sec16_4Expiry(docDate time.Time, annualReturnFiledOn *time.Time) time.Time {
	fyEnd := FYEnd(docDate)
	expiry := time.Date(fyEnd.Year(), time.November, 30, 0, 0, 0, 0, IST)
	if annualReturnFiledOn != nil {
		f := annualReturnFiledOn.In(IST)
		filed := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, IST)
		if filed.Before(expiry) {
			expiry = filed
		}
	}
	// s.16(5): THE LATER OF, not the earlier of. The section opens
	// "Notwithstanding anything contained in sub-section (4)", so it displaces
	// the WHOLE of s.16(4) for these four years — including the annual-return
	// cut-off, which is why the amnesty date is taken against `expiry` AFTER the
	// annual return has already been applied rather than before it. An FY 2018-19
	// invoice whose GSTR-9 was furnished in December 2019 is still claimable up
	// to 30 November 2021; reading s.16(5) as merely another "whichever is
	// earlier" limb would extinguish it on the annual-return date.
	//
	// Beyond the four years this is a no-op, and inside them it can only ever
	// move the expiry LATER, so it cannot bar a credit that was previously
	// allowed.
	if Sec16_5Applies(docDate) && expiry.Before(sec16_5AmnestyLastDate) {
		return sec16_5AmnestyLastDate
	}
	return expiry
}

// Sec16_4Barred reports whether a credit on docDate is time-barred when claimed
// on claimOn. The expiry day is INCLUSIVE: a claim ON 30 November is allowed, a
// claim on 1 December is barred.
func Sec16_4Barred(docDate, claimOn time.Time, annualReturnFiledOn *time.Time) bool {
	expiry := Sec16_4Expiry(docDate, annualReturnFiledOn)
	c := claimOn.In(IST)
	day := time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, IST)
	return day.After(expiry)
}

// ---------------------------------------------------------------------------
// s.16(6) IS NOT MODELLED, AND THAT IS DISCLOSED RATHER THAN GUESSED
// ---------------------------------------------------------------------------
// [CITED-PRIMARY] s.16(6) CGST Act 2017, inserted by the Finance (No. 2) Act
// 2024 alongside s.16(5), same commencement (27-09-2024, retrospective to
// 01-07-2017). Where a registration is cancelled under s.29 and the cancellation
// is later REVOKED — under s.30 or by an appellate order — the person may take
// the credit for the period between cancellation and revocation, in a return
// filed within thirty days of the revocation order, or by the ordinary 30
// November limb, WHICHEVER IS LATER.
//
// Every input that rule turns on — the date of the cancellation order, its
// effective date, the date of the revocation order — is REGISTRATION-STATUS DATA
// THIS SYSTEM DOES NOT HOLD. There is no cancellation table, no revocation
// order, nothing on the company master that records a suspended period. There is
// therefore nothing to compute it from, and a guess would either revive credit
// on a registration that was never cancelled (an over-claim, the direction that
// draws a notice) or invent a thirty-day window off a date we do not have.
//
// So it is NOT implemented and it is NOT silent. Disclosures() emits
// DiscSec16_6NotModelled ("sec-16-6-revocation-not-modelled") unconditionally,
// because the gap is unconditional: no reconciliation this software produces has
// ever considered s.16(6). A filer whose registration was cancelled and restored
// must check the affected periods by hand.
//
// The DIRECTION of the gap is the safe one — credit is under-claimed, never
// over-claimed — but an under-claim a filer cannot see is still a filer paying
// tax they do not owe, which is why it is a disclosure and not a comment.

// ---------------------------------------------------------------------------
// PENDING: forbidden on some records, and the rule is EFFECTIVE-DATED
// ---------------------------------------------------------------------------

// PendingCase names the categories on which IMS restricts the PENDING action.
// It is an explicit input rather than something inferred from the row, because
// three of the four turn on facts about the ORIGINAL record (was it rejected?
// was its 3B filed?) that live outside any single statement row.
type PendingCase string

const (
	// PendingOrdinary is every record the restriction does not name.
	PendingOrdinary PendingCase = "ORDINARY"
	// PendingOriginalCreditNote is an original credit note.
	PendingOriginalCreditNote PendingCase = "ORIGINAL_CREDIT_NOTE"
	// PendingUpwardAmendmentOfCN is an upward amendment of a credit note.
	PendingUpwardAmendmentOfCN PendingCase = "UPWARD_AMENDMENT_OF_CN"
	// PendingDownwardAmendmentOfRejectedCN is a downward amendment of a credit
	// note whose ORIGINAL was rejected.
	PendingDownwardAmendmentOfRejectedCN PendingCase = "DOWNWARD_AMENDMENT_OF_REJECTED_CN"
	// PendingDownwardAmendmentOfAcceptedDoc is a downward amendment of an
	// Invoice or Debit Note whose original was ACCEPTED and whose GSTR-3B has
	// been FILED.
	PendingDownwardAmendmentOfAcceptedDoc PendingCase = "DOWNWARD_AMENDMENT_OF_ACCEPTED_DOC_FILED"
)

// pendingRelaxationFrom is the first tax period from which PENDING became
// available on the four restricted categories: OCTOBER 2025.
//
// [CITED-SECONDARY] GSTN Advisory 631 of 08-10-2025, read via professional-press
// renderings (A2Z Taxcorp, TaxO) rather than out of the GSTN portal itself.
// ***VERIFY BEFORE RELEASE.***
//
// It is EFFECTIVE-DATED for the same reason the TDS and TCS rate tables are: a
// hard-coded "PENDING is never allowed on a credit note" is WRONG for every
// period this software ships into, and a hard-coded "always allowed" is wrong
// for every period before October 2025 — including periods still being amended.
var pendingRelaxationFrom = Period{Year: 2025, Month: time.October}

// PendingAllowed reports whether the PENDING action may be taken on a record of
// the given case in the given TAX PERIOD, and — when the relaxation applies —
// the instant the deferral expires.
//
// Before the October-2025 tax period the four restricted categories cannot be
// kept pending at all [CITED-PRIMARY: GSTN IMS advisory Flow(3) / FAQ 11].
//
// From the October-2025 tax period they may be kept pending for ONE TAX PERIOD,
// expiring at the GSTR-3B due date of period+1 [CITED-SECONDARY, see above]. The
// expiry is anchored on the 20th by CALENDAR-MONTH addition — writing it as
// "one month later" in day arithmetic is the clamping bug this package is
// otherwise free of.
//
// The zero time is returned as the expiry when the deferral is open-ended (the
// ordinary case: an unrestricted record may be kept pending indefinitely, and
// pretending otherwise would expire credit the law does not).
func PendingAllowed(p Period, c PendingCase) (allowed bool, expiresAt time.Time) {
	if c == PendingOrdinary {
		return true, time.Time{}
	}
	if p.Year*12+int(p.Month) < pendingRelaxationFrom.Year*12+int(pendingRelaxationFrom.Month) {
		return false, time.Time{}
	}
	return true, GSTR3BDueDate(p.AddMonths(1))
}
