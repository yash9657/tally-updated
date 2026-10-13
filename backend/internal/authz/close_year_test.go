package authz

// close_year_test.go pins the COMPANY-WIDE year-end lock and, just as
// importantly, pins that it composes with the per-level cutoff rather than
// replacing it.
//
// ⛔ THE TWO CONTROLS ARE DIFFERENT STATEMENTS AND MUST STAY DIFFERENT.
//
//	security_level.backdated_cutoff  "this ROLE may not back-date past X"
//	company.books_closed_through     "the COMPANY's books are closed through Y"
//
// The first is a role limit an administrator tunes; the second is a statutory
// fact true for everyone, the Owner included. Spelling the second as N copies
// of the first is the design this rejected — see migration 0089's header for
// the two reasons, the second of which is a hole that opens by itself when a
// level is added after the year is closed.

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// writeOn is a create of a sales voucher dated d — the ordinary shape the
// cutoff judges.
func writeOn(d string) Request {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		panic(err)
	}
	return Request{
		UserID: uuid.New(), CompanyID: uuid.New(),
		Action: Create, Resource: "sales", VoucherDate: &t,
	}
}

// TestClosedYearRefusesEveryoneIncludingTheOwner is the point of the feature.
//
// ⛔ MUTATION PROOF: remove BooksClosedThrough from effectiveCutoff and the
// owner arm goes green while the year is closed — which is the whole defect.
func TestClosedYearRefusesEveryoneIncludingTheOwner(t *testing.T) {
	// FY 2026-27 closed. No level cutoff anywhere: the lock is the company's.
	closed := date("2027-03-31")

	owner := Level{IsOwner: true, DefaultAccess: "full", BooksClosedThrough: closed}
	clerk := Level{DefaultAccess: "full", BooksClosedThrough: closed}

	for _, c := range []struct {
		who   string
		level Level
	}{{"owner", owner}, {"a full-access non-owner", clerk}} {
		// Inside the closed year: refused.
		if d := Evaluate(c.level, nil, writeOn("2027-03-31")); d.Allow {
			t.Errorf("%s wrote on the LAST day of a closed year and was allowed — "+
				"`through` is INCLUSIVE", c.who)
		}
		if d := Evaluate(c.level, nil, writeOn("2026-06-15")); d.Allow {
			t.Errorf("%s wrote inside a closed year and was allowed", c.who)
		}
		// The day after: allowed. A year-end that also froze the NEW year would
		// stop the business trading.
		if d := Evaluate(c.level, nil, writeOn("2027-04-01")); !d.Allow {
			t.Errorf("%s could not write on the first day of the OPEN year: %s",
				c.who, d.Reason)
		}
	}
}

// TestAClosedYearSaysSoRatherThanSayingItIsARoleLimit is the D93/D112 rule
// applied to a refusal an operator will meet often.
//
// "Denied" is useless here: the operator has to know whether to ask an
// administrator to widen their role, or to accept that a filed year is filed.
// Those are different actions and the reason has to tell them apart.
func TestAClosedYearSaysSoRatherThanSayingItIsARoleLimit(t *testing.T) {
	closedOnly := Level{IsOwner: true, DefaultAccess: "full",
		BooksClosedThrough: date("2027-03-31")}
	roleOnly := Level{IsOwner: true, DefaultAccess: "full",
		BackdatedCutoff: date("2026-04-01")}

	d := Evaluate(closedOnly, nil, writeOn("2026-06-15"))
	if d.Allow {
		t.Fatal("a closed year must refuse")
	}
	if !contains(d.Reason, "closed") {
		t.Errorf("a year-end refusal must say the books are CLOSED, so the operator "+
			"does not go asking for a wider role: %q", d.Reason)
	}
	if !contains(d.Reason, "2027-03-31") {
		t.Errorf("a year-end refusal must name the date the books are closed through, "+
			"which is the one fact that tells the operator what IS writeable: %q", d.Reason)
	}

	// The CONTROL: a role limit must NOT claim the books are closed.
	r := Evaluate(roleOnly, nil, writeOn("2025-06-15"))
	if r.Allow {
		t.Fatal("a role cutoff must still refuse")
	}
	if contains(r.Reason, "closed") {
		t.Errorf("a per-level cutoff told the operator the BOOKS are closed; it is a "+
			"role limit and an administrator can change it: %q", r.Reason)
	}
}

// TestTheTwoCutoffsComposeAndTheLaterOneWins is the property that lets the two
// controls coexist. Neither may weaken the other.
func TestTheTwoCutoffsComposeAndTheLaterOneWins(t *testing.T) {
	// A clerk limited to this FY, at a company that has closed the PREVIOUS one.
	// The role limit is the tighter of the two here.
	lv := Level{DefaultAccess: "full",
		BackdatedCutoff:    date("2027-04-01"), // role: nothing before FY27-28
		BooksClosedThrough: date("2026-03-31"), // company: FY25-26 closed
	}
	if d := Evaluate(lv, nil, writeOn("2026-09-01")); d.Allow {
		t.Error("the company had not closed 2026-09, but the ROLE cutoff should still " +
			"refuse it — a company lock must never WIDEN what a role may do")
	}

	// And the mirror: the company lock is the tighter one.
	lv2 := Level{DefaultAccess: "full",
		BackdatedCutoff:    date("2020-01-01"), // role: almost anything
		BooksClosedThrough: date("2027-03-31"), // company: FY26-27 closed
	}
	if d := Evaluate(lv2, nil, writeOn("2026-09-01")); d.Allow {
		t.Error("a permissive ROLE cutoff let a write into a CLOSED year — the company " +
			"lock must never be weakened by a role")
	}
	if d := Evaluate(lv2, nil, writeOn("2027-06-01")); !d.Allow {
		t.Errorf("both controls permit 2027-06-01 and it was still refused: %s", d.Reason)
	}
}

// TestNoClosedYearChangesNothing is the CONTROL FOR EVERY EXISTING COMPANY.
// The column is NULL everywhere until somebody closes a year, and a company
// that has never closed one must behave exactly as it did before this existed.
func TestNoClosedYearChangesNothing(t *testing.T) {
	owner := Level{IsOwner: true, DefaultAccess: "full"}
	if d := Evaluate(owner, nil, writeOn("1999-01-01")); !d.Allow {
		t.Errorf("a company with no closed year refused an old write: %s", d.Reason)
	}
	// And a role cutoff alone still behaves exactly as it always did.
	roleOnly := Level{IsOwner: true, DefaultAccess: "full", BackdatedCutoff: date("2025-04-01")}
	if d := Evaluate(roleOnly, nil, writeOn("2025-03-31")); d.Allow {
		t.Error("the per-level cutoff stopped working")
	}
	if d := Evaluate(roleOnly, nil, writeOn("2025-04-01")); !d.Allow {
		t.Errorf("the per-level cutoff over-reached by a day: %s", d.Reason)
	}
}

// TestAClosedYearDoesNotBlockReads is the other half of "closed", and it is the
// reason the whole feature is safe to ship: a closed year must remain fully
// READABLE. An auditor reads a closed year; that is what it is for.
func TestAClosedYearDoesNotBlockReads(t *testing.T) {
	lv := Level{IsOwner: true, DefaultAccess: "full", BooksClosedThrough: date("2027-03-31")}
	for _, act := range []Action{Display, Print} {
		req := writeOn("2026-06-15")
		req.Action = act
		req.Resource = "report"
		if d := Evaluate(lv, nil, req); !d.Allow {
			t.Errorf("%s on a closed period was refused — a closed year must stay "+
				"readable, which is the entire point of closing it: %s", act, d.Reason)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
