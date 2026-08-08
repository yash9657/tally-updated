package authz

// cutoff_reason_test.go — WHAT A BACK-DATED REFUSAL TELLS THE OPERATOR, and the
// zero-date sentinel the callers use to mean "the date could not be read".
//
// ===========================================================================
// WHY THE MESSAGE IS TESTED AT ALL
// ===========================================================================
// Decision.Reason is not an internal label: the middleware writes it to the
// client verbatim (writeError puts it in `{"error": ...}`), so it IS the sentence
// the person at the keyboard reads. "voucher date is before the back-dated
// cutoff" told them a period was locked and not WHERE the lock began, so the only
// way to find the boundary was to resubmit with earlier dates until one stuck.
// The date is now in the sentence, rendered exactly as GET /security-levels
// renders it, so the refusal and the level's own JSON cannot disagree.
//
// ===========================================================================
// AND WHY THE ZERO DATE GETS A DIFFERENT SENTENCE
// ===========================================================================
// cutoffDeny skips entirely on a nil VoucherDate, so a caller that cannot read a
// write's date must NOT pass nil — it passes the zero time (see
// server.beforeEveryCutoff and peekPayrollVoucherDate), which is before every
// settable cutoff and therefore refuses. Refusing is right, but saying "your
// voucher date is before the cutoff" about it would be a LIE: the date the caller
// sent may well be AFTER the cutoff and merely spelled in a way the PDP cannot
// parse. That sentence sends them hunting a period lock instead of fixing a
// format, so the two cases say different things.

import (
	"strings"
	"testing"
	"time"
)

// TestCutoffDenyNamesTheCutoffDate pins the boundary into the refusal.
func TestCutoffDenyNamesTheCutoffDate(t *testing.T) {
	level := Level{IsOwner: true, DefaultAccess: "full", BackdatedCutoff: date("2026-07-01")}
	d := Evaluate(level, nil, Request{
		Action: Create, Resource: "sales", VoucherDate: date("2026-05-31"),
	})
	if d.Allow {
		t.Fatalf("a 2026-05-31 write under a 2026-07-01 cutoff was ALLOWED: %+v", d)
	}
	if !strings.Contains(d.Reason, "2026-07-01") {
		t.Errorf("refusal reason = %q, want it to name the cutoff date 2026-07-01 — Decision.Reason is "+
			"written to the client verbatim, and a refusal that does not name the boundary leaves the "+
			"operator to find it by resubmitting with earlier dates", d.Reason)
	}
	if !strings.Contains(d.Reason, "cutoff") {
		t.Errorf("refusal reason = %q, want it to say WHICH control refused — a bare date does not tell "+
			"the operator that a period lock is what stopped them", d.Reason)
	}
	if d.HTTPStatus != 403 {
		t.Errorf("cutoff deny HTTPStatus = %d, want 403", d.HTTPStatus)
	}
	// It must not be mistaken for the step-up contract: the middleware keys its
	// distinguishable STEPUP_REQUIRED body off Reason == StepUpReason.
	if d.Reason == StepUpReason {
		t.Errorf("the cutoff refusal now reads as the step-up reason (%q), so the client would be told to "+
			"present a second factor for a period lock no second factor can clear", d.Reason)
	}
}

// TestCutoffDenyRefusesTheZeroDateSentinelAndSaysWhy is the fail-closed half.
//
// ⛔ THE FIRST ASSERTION IS THE SECURITY ONE. If the zero time were allowed, then
// every caller that hands it over — server.beforeEveryCutoff, used by
// peekVoucherDate for a date it cannot read, peekPayrollVoucherDate for a wage
// month it cannot read — would be signalling "refuse this" into a PDP that
// permits it, and the back-dated cutoff would be bypassable from the wire.
func TestCutoffDenyRefusesTheZeroDateSentinelAndSaysWhy(t *testing.T) {
	level := Level{IsOwner: true, DefaultAccess: "full", BackdatedCutoff: date("2026-07-01")}
	var zero time.Time // exactly what server.beforeEveryCutoff() returns
	d := Evaluate(level, nil, Request{
		Action: Create, Resource: "sales", VoucherDate: &zero,
	})
	if d.Allow {
		t.Fatalf("the zero-date sentinel was ALLOWED under a 2026-07-01 cutoff: %+v — callers pass the "+
			"zero time to mean 'a date was required and could not be read', so allowing it makes an "+
			"unreadable date a way to walk straight through a closed period", d)
	}
	// The sentence must not claim something about the caller's date that the PDP
	// never managed to read.
	if strings.Contains(d.Reason, "0001-01-01") {
		t.Errorf("refusal reason = %q — it prints the SENTINEL as if it were the date the caller sent. "+
			"Nobody posted a voucher dated 1 January of year 1; the honest statement is that the date "+
			"could not be read", d.Reason)
	}
	if !strings.Contains(d.Reason, "could not be read") {
		t.Errorf("refusal reason = %q, want it to say the date could not be READ — as written the "+
			"operator is told their date is before the cutoff, which may be false (a 2026-12-31 write "+
			"spelled loosely lands here too) and sends them hunting a period lock instead of a format",
			d.Reason)
	}
	if !strings.Contains(d.Reason, "2026-07-01") {
		t.Errorf("refusal reason = %q, want the cutoff date named here too — this refusal only happens "+
			"BECAUSE a cutoff is set, so the operator still needs to know where it is", d.Reason)
	}

	// THE CONTROL, and it is the one that is easy to leave out: the sentinel must
	// be inert where NO cutoff is set. beforeEveryCutoff() is a cutoff signal, not
	// a date validator — a company that has locked nothing must not start being
	// refused writes because one of its dates was oddly spelled.
	noCutoff := Level{IsOwner: true, DefaultAccess: "full"}
	if d := Evaluate(noCutoff, nil, Request{
		Action: Create, Resource: "sales", VoucherDate: &zero,
	}); !d.Allow {
		t.Errorf("with NO cutoff set, the zero-date sentinel was refused (%+v) — the fail-closed peek has "+
			"become a global date validator and every company without a period lock now 403s on a "+
			"loosely spelled date instead of getting the handler's own 400", d)
	}

	// THE CORNER THE `Before()` COMPARISON ALONE WOULD MISS. parseCutoff puts no
	// lower bound on a cutoff, so a level can be set to 0001-01-01 — and at that
	// one value `zero.Before(cutoff)` is FALSE, so a cutoff test that relied only
	// on the comparison would let every unreadable date through at exactly the
	// company that locked the most. The sentinel is matched by identity for this.
	yearOne := Level{IsOwner: true, DefaultAccess: "full", BackdatedCutoff: date("0001-01-01")}
	if d := Evaluate(yearOne, nil, Request{
		Action: Create, Resource: "sales", VoucherDate: &zero,
	}); d.Allow {
		t.Errorf("with a cutoff of 0001-01-01 the zero-date sentinel was ALLOWED (%+v) — the fail-closed "+
			"peek is defeated by the one cutoff value where zero.Before(cutoff) is false", d)
	}

	// And a readable date after the cutoff still posts, so the sentinel branch has
	// not swallowed the ordinary case.
	if d := Evaluate(level, nil, Request{
		Action: Create, Resource: "sales", VoucherDate: date("2026-08-15"),
	}); !d.Allow {
		t.Errorf("a 2026-08-15 write under a 2026-07-01 cutoff was refused (%+v) — the cutoff now refuses "+
			"dates AFTER it and nobody can enter today's vouchers", d)
	}
}
