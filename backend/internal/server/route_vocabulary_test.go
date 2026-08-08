package server

// route_vocabulary_test.go closes the project's most-documented failure mode
// with a STRUCTURAL test instead of a comment.
//
// routeAction() default-DENIES any route it does not recognise. That is the
// right failure direction (forgetting a mapping makes a route inaccessible, not
// unprotected) but it has bitten this codebase repeatedly: every handler added
// in a wave shipped with a "!! WIRING DEPENDENCY !!" banner in its test file
// because a missing routeAction entry turns a working handler into a 403 that
// looks like an authorization bug.
//
// This test walks the REAL chi router and asserts, for every registered route
// under /v1/companies/{companyID}:
//
//  1. routeAction maps it (mapped == true) — no route is unreachable; and
//  2. the (action, resource) it emits is drawn from the CLOSED PDP vocabulary in
//     internal/authz, so an administrator can actually write an ACL rule that
//     matches it.
//
// (2) matters because the admin API validates rules against that vocabulary. A
// resource the router emits but the vocabulary rejects would be un-governable:
// no rule could ever name it, so only default_access could reach it.
//
// It is an IN-PACKAGE test (package server, not server_test) because
// routeAction is unexported — deliberately, since it is enforcement wiring and
// not API.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/authz"
)

// paramValue supplies a syntactically valid value for each URL parameter chi
// reports, so the synthesized path has the same SHAPE as a real request.
func paramValue(name, companyID string) string {
	if name == "companyID" {
		return companyID
	}
	// ⚠️ NOT EVERY PARAMETER IS A UUID, and a synthesised path that pretends
	// otherwise is a path the PDP cannot fully parse — so any check keyed on a
	// parsed parameter silently reads as "this route is not one of mine" in every
	// test built on this helper. {legNo} is an ordinal (bankReconPathParams calls
	// strconv.Atoi on it); handing it a uuid made the bank-reconciliation arm of
	// voucherDateFromRequest invisible to TestTheCutoffActuallyReadsEveryDateItClaimsToRead.
	if name == "legNo" {
		return "0"
	}
	return uuid.NewString()
}

// synthPath turns a chi route pattern into a concrete path plus the URL params
// routeAction reads (only companyID today, but the RouteContext is populated the
// same way chi would).
func synthPath(pattern, companyID string) (string, *chi.Context) {
	rctx := chi.NewRouteContext()
	segs := strings.Split(pattern, "/")
	for i, seg := range segs {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			name := strings.Trim(seg, "{}")
			v := paramValue(name, companyID)
			segs[i] = v
			rctx.URLParams.Add(name, v)
		}
	}
	return strings.Join(segs, "/"), rctx
}

// TestEveryCompanyRouteIsMappedAndInVocabulary is the closure gate. Adding a
// route under {companyID} without a routeAction entry FAILS HERE, naming the
// method and pattern, instead of surfacing later as a mysterious 403.
func TestEveryCompanyRouteIsMappedAndInVocabulary(t *testing.T) {
	srv := &Server{}
	srv.routes() // build the real router; no DB needed to enumerate routes.

	companyID := uuid.NewString()
	const prefix = "/v1/companies/{companyID}"

	seen := 0
	err := chi.Walk(srv.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, prefix) {
			return nil
		}
		seen++
		path, rctx := synthPath(route, companyID)
		req := httptest.NewRequest(method, path, nil).
			WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))

		act, res, resName, mapped := routeAction(req)
		if !mapped {
			t.Errorf("route %s %s is NOT mapped by routeAction: it default-denies, so it is "+
				"unreachable for EVERY security level (add a case to routeAction)", method, route)
			return nil
		}
		if !authz.ValidAction(act) {
			t.Errorf("route %s %s emits action %q, which is not in the PDP vocabulary "+
				"(authz.KnownActions) — no ACL rule could ever name it", method, route, act)
		}
		if !authz.ValidResource(res) {
			t.Errorf("route %s %s emits resource %q, which is not in the PDP vocabulary "+
				"(authz.KnownResources) — no ACL rule could ever name it", method, route, res)
		}
		if resName != "" && !authz.ValidResource(authz.Resource(string(res)+":"+resName)) {
			t.Errorf("route %s %s emits resource name %q, which is not in "+
				"authz.KnownReportNames — a rule naming it would be rejected by the admin API",
				method, route, resName)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}
	// Guard against the test silently passing because the walk found nothing.
	if seen < 30 {
		t.Fatalf("walked only %d company routes; the router or the prefix changed", seen)
	}
}

// TestReportVocabularyMatchesRouter is the CONVERSE direction: every report name
// an administrator may write into a rule ("allow display report:trial-balance")
// must be a name the router actually emits. A name in the vocabulary that no
// route produces is a rule that silently never matches.
func TestReportVocabularyMatchesRouter(t *testing.T) {
	companyID := uuid.NewString()
	for _, name := range authz.KnownReportNames {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("companyID", companyID)
		req := httptest.NewRequest(http.MethodGet, "/v1/companies/"+companyID+"/"+name, nil).
			WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))

		act, res, resName, mapped := routeAction(req)
		if !mapped || act != authz.Display || res != "report" || resName != name {
			t.Errorf("report %q: routeAction returned (%q,%q,%q,mapped=%v), want (display,report,%s,true) — "+
				"authz.KnownReportNames lists a report the router does not serve, so a rule naming it can never match",
				name, act, res, resName, mapped, name)
		}
	}
}
