package authz

import (
	"strings"
	"testing"
)

// TestExportIsNotAWritableAction pins the DELETION of the `export` action token
// from the rule vocabulary, which is a security fix and not a tidy-up.
//
// THE DEFECT IT LOCKS OUT. `export` sat in KnownActions with nothing on any code
// path consulting it. Dormant, that is merely dead; once T15 shipped Alt+E — the
// report screens write a CSV/Excel file of the rows on screen, in the browser —
// it became a control that LIES. An owner could type `disallow export
// report:stock-summary` into Gateway > Users & Roles, have it VALIDATED by this
// very function, saved into acl_rule, listed back to them as an active rule, and
// the operator's Alt+E would still write the file. Nothing anywhere reads the
// rule. A refused-looking setting that does nothing is worse than an absent one,
// because it ends the owner's search.
//
// THE CONTROL IS THE SECOND HALF OF EVERY CASE BELOW: the identical rule with
// `display` (the action that DOES govern who may reach a report at all, and is
// enforced server-side on the route) must still validate. Without it this test
// would pass just as happily against a ValidateRule that rejected everything, or
// against a typo in the resource token — i.e. it would prove the refusal
// happened for a reason unrelated to `export`.
func TestExportIsNotAWritableAction(t *testing.T) {
	for _, res := range []Resource{"report:stock-summary", "report:day-book", "*"} {
		// THE FIX: an export rule is refused, and the message names the token so
		// the admin API's 422 tells the owner WHICH word it rejected.
		err := ValidateRule(Rule{Effect: Disallow, Action: "export", Resource: res})
		if err == nil {
			t.Errorf("ValidateRule(disallow export %s) = nil, want an error: "+
				"nothing enforces `export`, so a rule naming it is a security "+
				"setting that reports success and does nothing", res)
		} else if !strings.Contains(err.Error(), "export") {
			t.Errorf("ValidateRule(disallow export %s) error %q does not name the "+
				"offending token; the admin API surfaces it verbatim as a 422", res, err)
		}

		// THE CONTROL: the same rule shape with the action that IS enforced.
		if err := ValidateRule(Rule{Effect: Disallow, Action: Display, Resource: res}); err != nil {
			t.Errorf("ValidateRule(disallow display %s) = %v, want nil — the refusal "+
				"above must be about the ACTION and not about the resource or the effect", res, err)
		}
	}

	// ValidAction is what the served vocabulary (GET /security/vocabulary) and
	// the PDP's own token set are both built from, so this is the same fact
	// stated where a rule editor reads it.
	if ValidAction("export") {
		t.Error("ValidAction(\"export\") = true: the token is offered to rule editors again")
	}
	for _, a := range KnownActions {
		if string(a) == "export" {
			t.Error("KnownActions still carries `export`; GET /security/vocabulary serves this slice verbatim")
		}
	}

	// AND THE OTHER CONTROL: removing one token must not have removed the rest.
	// Print is the closest neighbour — it is the other read-shaped action, it was
	// added the same way, and it IS enforced (handler_print_*.go), so a change
	// that took `export` out by taking read actions out would fail here.
	for _, a := range []Action{Create, Alter, Delete, Display, Print} {
		if !ValidAction(a) {
			t.Errorf("ValidAction(%q) = false: an enforced action was dropped with `export`", a)
		}
	}
	if !ValidAction(Wildcard) {
		t.Error("the wildcard action stopped validating")
	}
}
