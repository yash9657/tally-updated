package server

// D40 — WHAT AN UNROUTED PATH ANSWERS.
//
// The PDP used to refuse every request routeAction could not map with a flat 403
// "unmapped route: access denied". That made "this route was never built"
// indistinguishable from "your level is not permitted", and the cost was paid
// four separate times in the parity audit: `/reports/sales-order-book`,
// `/reports/outstanding`, `/outstanding` and `/reports/day-book` are all
// misspellings of real routes, every one answered 403, and every one was first
// diagnosed as a permission defect. It also destroyed the recon's literal proof
// that PATCH on a challan returns 405.
//
// ⛔ THE POINT OF THIS FILE IS THE BOUNDARY, NOT THE STATUS CODES. Asserting
// "404 for a nonsense path" alone would be satisfied by a middleware that had
// stopped enforcing anything, so the CONTROLS carry the weight:
//
//	(1) a real route still answers 200 for the same token, so the middleware is
//	    still in the path and the 404s are not a dead router;
//	(2) an UNAUTHENTICATED request to the same nonexistent path still answers
//	    401, so nothing here opened up route enumeration to a stranger;
//	(3) a NON-MEMBER with a valid token still answers 403 "not a member", so the
//	    membership gate still precedes the shape answer and a foreign caller
//	    learns nothing about this company's API surface.
//
// ⛔⛔ AND THE FAIL-CLOSED ARM IS PINNED SEPARATELY, in
// TestAMountedButUnmappedRouteStillFailsClosed. That arm is the security
// property: a route the router serves but the PDP cannot map must stay 403,
// because the alternative is serving a route no rule governs. In a healthy tree
// TestEveryCompanyRouteIsMappedAndInVocabulary makes it unreachable, which is
// exactly why it needs a test that reaches it deliberately — an unreachable
// branch nobody exercises is how it would rot into a 404.
//
// IN-PACKAGE (package server) for the aclRequest/aclToken/aclOwnerCompany
// harness in auth_payroll_itc_acl_test.go and for denyUnmappedRoute itself.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestANeverBuiltRouteAnswers404AndNot403(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	// CONTROL (1): the middleware is still enforcing, and this token still gets
	// served. Without this every assertion below is satisfied by a broken router.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/trial-balance?as_on=2026-07-31", ownerTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: Owner GET /trial-balance = %d, want 200 — the router or the middleware is "+
			"broken, so the 404s below would prove nothing (%s)", rec.Code, rec.Body.String())
	}

	// The four real misspellings from the audit, plus one path that is nonsense
	// by construction. Each is a path the router mounts NOTHING at.
	for _, path := range []string{
		"/reports/sales-order-book", // the real route is /sales-order-book
		"/reports/outstanding",      // the real route is /outstandings
		"/outstanding",              // ditto — singular
		"/reports/day-book",         // the real route is /day-book
		"/no-such-report-exists",
	} {
		rec, body := aclRequest(t, s, http.MethodGet, base+path, ownerTok, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("Owner GET %s = %d, want 404 — nothing is mounted here, and answering 403 makes "+
				"a typo indistinguishable from a permission refusal (%s)", path, rec.Code, rec.Body.String())
			continue
		}
		// ⛔ THE MESSAGE MATTERS AS MUCH AS THE CODE: "access denied" on a route
		// that does not exist is the half of D40 that misled the reader.
		if msg, _ := body["error"].(string); strings.Contains(msg, "access denied") {
			t.Errorf("Owner GET %s answered 404 but still says %q — a nonexistent route must not "+
				"describe itself as denied", path, msg)
		}
	}

	// CONTROL (2): no route enumeration for a stranger. An unauthenticated call
	// to the same nonexistent path must still be 401, not 404 — the shape answer
	// lives BEHIND authentication.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/no-such-report-exists", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("CONTROL: UNAUTHENTICATED GET a nonexistent route = %d, want 401 — the route shape "+
			"must not be readable without a token (%s)", rec.Code, rec.Body.String())
	}

	// CONTROL (3): no enumeration across tenants either. A valid token for a
	// DIFFERENT company is not a member here, and the membership gate must
	// answer before the shape does.
	otherTok := aclToken(t, s, "acl-outsider-"+companyID[:8])
	rec, _ := aclRequest(t, s, http.MethodGet, base+"/no-such-report-exists", otherTok, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("CONTROL: NON-MEMBER GET a nonexistent route = %d, want 403 — membership must be "+
			"decided before the route shape, or a foreign caller can map this company's API (%s)",
			rec.Code, rec.Body.String())
	}
}

func TestAWrongMethodOnARealRouteAnswers405WithAllow(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	// PATCH is mounted nowhere on the challan collection, but GET and POST are.
	// This is the exact proof D40 said the flat 403 had killed.
	rec, body := aclRequest(t, s, http.MethodPatch, base+"/tds-challans", ownerTok, map[string]any{})
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Owner PATCH /tds-challans = %d, want 405 — the path exists (GET and POST are "+
			"mounted), only the method does not (%s)", rec.Code, rec.Body.String())
	}
	allow := rec.Header().Get("Allow")
	// ⛔ ASSERT THE METHODS THAT ARE REALLY THERE, and assert the absent one is
	// ABSENT — an Allow header listing everything would pass a containment-only
	// check while telling the caller nothing.
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		if !strings.Contains(allow, m) {
			t.Errorf("405 Allow = %q, want it to name %s — the header is the disclosure, and a 405 "+
				"without it leaves the caller guessing exactly as the 403 did", allow, m)
		}
	}
	if strings.Contains(allow, http.MethodPatch) {
		t.Errorf("405 Allow = %q names PATCH, the method that was just refused", allow)
	}
	if msg, _ := body["error"].(string); strings.Contains(msg, "access denied") {
		t.Errorf("405 body says %q — a method that was never mounted is not a denial", msg)
	}
}

// TestAMountedButUnmappedRouteStillFailsClosed reaches the arm that
// TestEveryCompanyRouteIsMappedAndInVocabulary keeps unreachable.
//
// ⛔ IT CALLS denyUnmappedRoute DIRECTLY, and that is deliberate rather than
// lazy. The condition is "the router serves this exact method+path AND
// routeAction cannot map it", and the only honest ways to produce it over HTTP
// are to mount a junk route on the real server (which changes the tree the other
// tests measure and would make the vocabulary walker fail) or to delete a
// routeAction arm (a mutation, not a test). Calling the decision function with a
// request whose path the router really does serve tests the decision itself,
// which is where the property lives.
func TestAMountedButUnmappedRouteStillFailsClosed(t *testing.T) {
	s := payrollServer(t)
	companyID, _ := aclOwnerCompany(t, s)

	// A path the router genuinely mounts for GET. If this ever stops being true
	// the test says so rather than passing for the wrong reason.
	real := "/v1/companies/" + companyID + "/trial-balance"
	served, canAsk := s.routeMethods(httptest.NewRequest(http.MethodGet, real, nil))
	if !canAsk {
		t.Fatal("routeMethods could not consult the router — the fail-closed default is untestable")
	}
	if len(served) == 0 {
		t.Fatalf("PRECONDITION: the router mounts nothing at %s, so this test is not exercising the "+
			"mounted-but-unmapped arm at all", real)
	}

	rec := httptest.NewRecorder()
	s.denyUnmappedRoute(rec, httptest.NewRequest(http.MethodGet, real, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a MOUNTED route the PDP cannot map answered %d, want 403 — this arm must fail "+
			"CLOSED. Serving a route no rule governs is the one outcome D40's fix may not produce "+
			"(%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "access denied") {
		t.Errorf("the fail-closed arm answered 403 but not as a denial: %s", rec.Body.String())
	}
}

// TestRouteMethodsFailsClosedWithNoRouter pins the `canAsk` half: if the router
// cannot be consulted, the answer is 403 and NOT 404. An inability to tell the
// cases apart is precisely the situation the old flat 403 existed for.
func TestRouteMethodsFailsClosedWithNoRouter(t *testing.T) {
	var s Server // zero value: s.router is nil
	if _, canAsk := s.routeMethods(httptest.NewRequest(http.MethodGet, "/v1/companies/x/y", nil)); canAsk {
		t.Fatal("routeMethods claimed it could consult a nil router")
	}
	rec := httptest.NewRecorder()
	s.denyUnmappedRoute(rec, httptest.NewRequest(http.MethodGet, "/v1/companies/x/y", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("with no router to ask, denyUnmappedRoute answered %d, want 403 — not knowing "+
			"whether a route exists must never become 'it does not exist'", rec.Code)
	}
}
