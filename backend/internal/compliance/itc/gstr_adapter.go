// gstr_adapter.go is the ONLY file in this package that imports
// compliance/gstr, and the edge is ONE-WAY BY CONSTRUCTION: itc imports gstr,
// gstr NEVER imports it. That is what keeps the dependency acyclic now that the
// ITC-source interface lives in gstr.
//
// It contains no arithmetic. Line.Decision() in effect.go is the projection and
// it is plain, tested Go; what lives here is the rename of its fields into
// gstr's vocabulary, plus the two structural facts gstr's checkITCDecision will
// refuse a decision for if this file gets them wrong.
package itc

import (
	"context"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/compliance/gstr"
)

// Decisions implements gstr.ITCSource.
//
// It is keyed by VOUCHER ID — the logical voucher, not the version — because
// that is the identity that survives an ALTER, and an altered purchase must
// carry ONE decision, not one per version. store.go's restatement guard
// (`vh.current_version_id = vv.id`) is what makes that true, and
// TestITCDecisionsAreVersionAware is the gate on it.
//
// TWO STRUCTURAL RULES gstr.checkITCDecision enforces, and which this adapter
// must therefore not break:
//
//  1. Available + Ineligible == the voucher's POSTED input tax, head by head.
//     itcEffect guarantees it by construction: a line either avails its full
//     posted tax or moves the whole of it to Ineligible, never a part of each
//     and never neither. That equality is ALSO gstr's staleness guard — a
//     decision resolved against a superseded version carries the superseded
//     version's tax and fails the check instead of silently reversing a figure
//     that no longer exists.
//  2. At most one of ReversedRules / ReversedOther is non-zero. itcEffect
//     returns early after setting RevRule (4(B)(1) wins).
//  3. ReversedOther (4(B)(2)) is NEVER NEGATIVE — reversal4B2 ends in
//     Tax.PositivePart(). ReversedRules (4(B)(1)) IS SIGNED and may be
//     negative: it mirrors the 4(A) figure of the same document rupee for
//     rupee, so a section 17(5) blocked purchase that is RETURNED carries a
//     negative availment AND a negative reversal, and nets to zero. Clamping
//     it destroys the whole tax on the return.
//
// What gstr does NOT check, and must not: an upper bound on a reversal against a
// NEGATIVE availment. A supplier credit note the supplier declared LARGER than
// we booked carries a positive 4(B)(2) correction on a negative 4(A) — books
// -300 against a statement -400 reverses 100 and nets to -400 — and the size of
// that correction comes from the statement, which only this package can see. It
// DOES check the LOWER bound, because that one is local: a negative 4(B)(1) can
// undo at most the whole of the note it sits on. The three cases (note absent
// from the statement, note declared larger, blocked purchase returned) are
// written out in full above itcEffect in effect.go and in rule (4) of gstr's
// checkITCDecision, in the same words on purpose.
//
// A voucher classified NOT_COMPARABLE still gets a decision. It has to: under
// model (b) its input tax is in 4(A), and rule (1) above is only satisfiable if
// EVERY posted rupee is accounted for. Omitting the vouchers this software
// cannot compare would make gstr refuse the whole return.
func (s *Service) Decisions(ctx context.Context, from, to string) (map[uuid.UUID]gstr.ITCDecision, error) {
	asOn, ok := asOnFromContext(ctx)
	if !ok {
		// NOT a fallback to time.Now(). See WithAsOn in service.go: a default
		// wall-clock read would make the no-wall-clock rule true only of the
		// code paths somebody remembered, which is the same as not having it.
		return nil, ErrNoAsOn
	}
	rep, err := s.Reconcile(ctx, from, to, Options{AsOn: asOn})
	if err != nil {
		return nil, err
	}
	return decisionsFromLines(rep.Lines), nil
}

// decisionsFromLines is the pure half of Decisions, split out so it can be
// tested without a database.
func decisionsFromLines(lines []Line) map[uuid.UUID]gstr.ITCDecision {
	out := make(map[uuid.UUID]gstr.ITCDecision, len(lines))
	for _, l := range lines {
		if l.Books == nil {
			// Statement-only lines move no figure under model (b): 4(A) is what
			// the BOOKS posted, and a document we never recorded posted nothing.
			// They are in the Report for the operator to chase; they are not
			// decisions ABOUT a voucher, because there is no voucher.
			continue
		}
		dec := l.Decision()
		out[l.Books.VoucherID] = gstr.ITCDecision{
			Bucket:        bucketFor(dec),
			Available:     toTaxTotals(dec.Avail),
			ReversedRules: toTaxTotals(dec.RevRule),
			ReversedOther: toTaxTotals(dec.RevOther),
			Ineligible:    toTaxTotals(dec.Inelig),
			Reason:        reasonFor(l),
		}
	}
	return out
}

// bucketFor picks the 4(A) row code.
//
// Only ISRC and OTH are ever produced. IMPG, IMPS and ISD exist in gstr's
// vocabulary but this software has no books-side marker that distinguishes an
// import or an ISD credit from an ordinary purchase — the ISD classification on
// a ledger is a per-supplier default, not a per-document fact from a statement —
// so claiming those buckets would be asserting something the books do not know.
// Stated here rather than guessed.
func bucketFor(d Decision) gstr.ITCBucket {
	if d.RCM {
		return gstr.BucketISRC
	}
	return gstr.BucketOTH
}

func toTaxTotals(t Tax) gstr.TaxTotals {
	return gstr.TaxTotals{CGST: t.CGST, SGST: t.SGST, IGST: t.IGST}
}

// reasonFor renders the verdict as the short human-readable string gstr carries
// for reporting. It never affects arithmetic — gstr's own comment says so — so
// it is safe for it to be prose.
func reasonFor(l Line) string {
	switch l.Status {
	case NotComparable:
		return string(l.Status) + ": " + string(l.NotComparable)
	case Mismatched:
		s := string(l.Status)
		for i, f := range l.Fields {
			if i == 0 {
				s += ": "
			} else {
				s += ","
			}
			s += string(f)
		}
		return s
	default:
		if l.IMSState != "" && !l.IMSState.AvailableForITC() {
			return string(l.Status) + "/IMS " + string(l.IMSState)
		}
		return string(l.Status)
	}
}
