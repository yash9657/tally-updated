package gstr

// errors_operator_facing_test.go pins ONE RULE over this package's REFUSALS:
//
//	an error that reaches an operator must not begin with a Go package name.
//
// ⭐ THIS IS NOT A STYLE TEST, AND THE EVIDENCE IS ON A SCREEN. Every sentinel
// below reaches an HTTP body (400 or 422) through handler_gstr9.go and
// handler_gstr3b.go, and `GSTR9Screen.tsx:482` renders it VERBATIM:
//
//	setError(e instanceof Error ? e.message : String(e));
//
// So the operator read `gstr: GSTR-9 covers an Indian financial year (1 April
// to 31 March), not 2025-01-01 to 2025-12-31`. The first word names a Go
// package. It tells them nothing about what they may do, and it is the first
// thing their eye lands on.
//
// ⛔⛔ AND THE MOCK ALREADY DISAGREED WITH THE SERVER. `frontend/src/api/mock.ts`
// raises these same two refusals for the same two conditions — deliberately, its
// comment says, because "a mock that refused in a different order lets a screen's
// error handling pass here and fail against the real server" — and it writes
// them WITHOUT the prefix. So the screens were built against the clean sentence
// and shown the prefixed one, and nothing anywhere compared the two.
//
// D93 closed exactly this class for masters.CreateLedger. D113's §4.4 named it
// again for the annual return's stacked prefixes. This is the same rule applied
// to the sentinels themselves rather than to one composition of them.

import (
	"regexp"
	"strings"
	"testing"
)

// packagePrefix matches a leading Go package qualifier — a lowercase
// identifier, a colon and a space: "gstr: ", "gstr3b: ", "itc: ".
//
// ⚠️ IT DELIBERATELY DOES NOT MATCH "GSTR-9 periods could not be generated:",
// because that colon follows a STATUTORY NAME in prose, not a package. The rule
// is about what the sentence OPENS with, not about colons.
var packagePrefix = regexp.MustCompile(`^[a-z][a-z0-9]*: `)

// operatorFacingSentinels is every error in this package that a user can be
// shown as a REFUSAL — one they are expected to read and act on.
//
// ⚠️ WHAT IS NOT HERE, AND WHY. The DB plumbing wraps ("gstr: query 3b
// versions: %w", "gstr: scan sales version: %w", "gstr: resolve ITC decisions:
// %w") keep their prefix. They are not refusals; they are diagnostics for
// whoever reads a log, and the package name is the useful part of them. That
// they ALSO reach the operator as a 422 today is a separate defect, registered
// rather than silently folded into this one — a dead database is not an
// "unprocessable entity" and its message is not for a filer.
func operatorFacingSentinels() []struct {
	name string
	err  error
} {
	return []struct {
		name string
		err  error
	}{
		{"ErrInvalid3B", ErrInvalid3B},
		{"ErrInvalid9", ErrInvalid9},
		{"ErrNotAFinancialYear", ErrNotAFinancialYear},
		{"ErrLedgersRequired", ErrLedgersRequired},
		{"ErrNoData", ErrNoData},
		{"ErrPeriodsFailed", ErrPeriodsFailed},
		{"errStaleITCDecision", errStaleITCDecision},
	}
}

// TestOperatorFacingSentinelsCarryNoPackagePrefix is the rule.
//
// ⛔ MUTATION PROOF: put `gstr: ` back on any one of them and this fails naming
// that sentinel and the exact prefix it grew.
func TestOperatorFacingSentinelsCarryNoPackagePrefix(t *testing.T) {
	sentinels := operatorFacingSentinels()
	if len(sentinels) < 7 {
		t.Fatalf("the list has shrunk to %d; a sentinel removed from it is a sentinel "+
			"nothing checks, which is how the prefix came back", len(sentinels))
	}
	for _, c := range sentinels {
		if p := packagePrefix.FindString(c.err.Error()); p != "" {
			t.Errorf("%s opens with the package prefix %q, which names a Go package and tells "+
				"the operator nothing about what they may do. GSTR9Screen renders this string "+
				"verbatim.\n  got: %s", c.name, p, c.err.Error())
		}
	}
}

// TestOperatorFacingSentinelsStillSayWhatBroke is the CONTROL, and it matters
// more than the rule above.
//
// Trimming a prefix is one keystroke away from trimming the sentence. A refusal
// that says nothing is worse than one that opens with a package name, so each
// sentinel is pinned to the word that makes it actionable.
func TestOperatorFacingSentinelsStillSayWhatBroke(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"ErrInvalid3B", ErrInvalid3B, "GSTR-3B"},
		{"ErrInvalid9", ErrInvalid9, "GSTR-9"},
		{"ErrNotAFinancialYear", ErrNotAFinancialYear, "financial year"},
		{"ErrLedgersRequired", ErrLedgersRequired, "tax ledger ids"},
		{"ErrNoData", ErrNoData, "sales"},
		{"ErrPeriodsFailed", ErrPeriodsFailed, "periods"},
		{"errStaleITCDecision", errStaleITCDecision, "input tax"},
	} {
		if !strings.Contains(c.err.Error(), c.want) {
			t.Errorf("%s no longer contains %q — the trim ate the message.\n  got: %s",
				c.name, c.want, c.err.Error())
		}
	}
}

// TestOperatorFacingSentinelsMatchTheFrontendMock pins the two sentences the
// mock also raises, VERBATIM as the mock spells them.
//
// ⛔ THE MOCK IS THE ONE THE SCREENS WERE BUILT AGAINST. Its own comment says it
// mirrors the server's refusals so a screen's error handling cannot pass against
// the mock and fail against the real thing. It spelled both of these without a
// prefix while the server spelled them with one, and no gate compared them —
// which is the whole reason this divergence survived.
//
// The strings below are copied from frontend/src/api/mock.ts (the GSTR-9
// branch). If a future change edits one side, this fails and names the other.
func TestOperatorFacingSentinelsMatchTheFrontendMock(t *testing.T) {
	const mockFinancialYear = "GSTR-9 covers an Indian financial year (1 April to 31 March)"
	const mockLedgers = "GSTR-9 is a reconciliation TO the books and needs the tax ledger ids"

	if got := ErrNotAFinancialYear.Error(); got != mockFinancialYear {
		t.Errorf("ErrNotAFinancialYear has drifted from frontend/src/api/mock.ts.\n"+
			" server: %s\n   mock: %s", got, mockFinancialYear)
	}
	if got := ErrLedgersRequired.Error(); got != mockLedgers {
		t.Errorf("ErrLedgersRequired has drifted from frontend/src/api/mock.ts.\n"+
			" server: %s\n   mock: %s", got, mockLedgers)
	}
}
