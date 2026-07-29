package itc

import (
	"errors"
	"fmt"
)

// Statement sections, matching gst_inward_document.section's CHECK constraint.
// The vocabulary is CLOSED: a section GSTN adds later must be added here AND to
// the constraint, deliberately, rather than arriving as free text nobody
// classified.
const (
	SectionB2B     = "B2B"
	SectionB2BA    = "B2BA"
	SectionCDNR    = "CDNR"
	SectionCDNRA   = "CDNRA"
	SectionIMPG    = "IMPG"
	SectionIMPGSEZ = "IMPGSEZ"
	SectionISD     = "ISD"
	SectionISDA    = "ISDA"
	SectionECOM    = "ECOM"
	SectionECOMA   = "ECOMA"
)

// ErrIMSNotApplicable is returned when an IMS action is attempted on a record
// IMS does not cover.
//
// It is a REFUSAL, not a warning. Recording an ACCEPT against an import bill of
// entry would make the action log claim the filer confirmed something the portal
// never asked them about, and a later reader has no way to tell that entry from
// a real one.
var ErrIMSNotApplicable = errors.New("itc: IMS does not apply to this record")

// ErrPendingNotAllowed is returned when PENDING is attempted on a record whose
// category forbids it in that tax period. See PendingAllowed.
var ErrPendingNotAllowed = errors.New("itc: PENDING is not permitted on this record in this tax period")

// ErrReturnFiled is returned when an action is attempted after the GSTR-3B for
// the period has been filed. GSTN IMS FAQ 12 freezes the action set at filing;
// an action after it does not count, so accepting one into the log would make
// the log disagree with the portal.
var ErrReturnFiled = errors.New("itc: the GSTR-3B for this period is already filed; the IMS action set is frozen")

// ErrUnknownAction is returned for an action kind outside the closed vocabulary.
var ErrUnknownAction = errors.New("itc: unknown IMS action")

// IMSApplies reports whether the Invoice Management System covers a record.
//
// IMS actions apply ONLY to B2B, B2BA, CDNR, CDNRA, ECOM and ECOMA. Imports
// (IMPG, IMPGSEZ), ISD credit (ISD, ISDA) and ANY reverse-charge row bypass IMS
// entirely — GSTN IMS FAQ 8 and 18: "RCM invoices are not part of IMS but will
// continue to be part of GSTR-2B". [CITED-PRIMARY]
//
// The reverse-charge test is deliberately applied ON TOP OF the section test, so
// an RCM row that arrives in the B2B section (which it does — RCM supplies stay
// in 2B) is still excluded. Getting this backwards produces the worst available
// failure: a filer "rejects" an RCM invoice in the belief that it removes the
// credit, when in law they owe the tax and hold the credit regardless.
func IMSApplies(section string, reverseCharge bool) bool {
	if reverseCharge {
		return false
	}
	switch section {
	case SectionB2B, SectionB2BA, SectionCDNR, SectionCDNRA, SectionECOM, SectionECOMA:
		return true
	default:
		return false
	}
}

// ValidateAction is the PURE gate every IMS action must pass before it is
// appended to the log. It performs no I/O, so it can be called from a handler,
// from a batch importer, or from inside the insert transaction, and it will give
// the same answer.
//
// The four checks, in the order a human would apply them:
//
//  1. is the action kind in the vocabulary at all;
//  2. does IMS cover this record;
//  3. has the return already been filed (the freeze — FAQ 12);
//  4. is PENDING permitted on this category IN THIS TAX PERIOD (the
//     effective-dated rule — see PendingAllowed).
func ValidateAction(kind, section string, reverseCharge bool, p Period, c PendingCase, returnFiled bool) error {
	switch kind {
	case ActionAccept, ActionReject, ActionPending, ActionReset:
	default:
		return fmt.Errorf("%w: %q", ErrUnknownAction, kind)
	}
	if !IMSApplies(section, reverseCharge) {
		return fmt.Errorf("%w: section %s (reverse charge %v)", ErrIMSNotApplicable, section, reverseCharge)
	}
	if returnFiled {
		return fmt.Errorf("%w: period %s", ErrReturnFiled, p)
	}
	if kind == ActionPending {
		if allowed, _ := PendingAllowed(p, c); !allowed {
			return fmt.Errorf("%w: case %s, period %s", ErrPendingNotAllowed, c, p)
		}
	}
	return nil
}
