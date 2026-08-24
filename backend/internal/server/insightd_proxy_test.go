package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/platform/db"
)

// insightd_proxy_test.go covers the four properties of cored's draft proxy that a
// wire drive cannot see, and the one it can see but would not notice going wrong.

// TestTheSixDraftRoutesMapOntoThePurchaseToken pins the token choice (D49).
//
// ⛔ WHY THIS IS ASSERTED AND NOT LEFT TO THE STRUCTURAL GATE.
// TestEveryCompanyRouteIsMappedAndInVocabulary catches an UNMAPPED route and is
// blind to a MIS-mapped one — D5's M4 kept it green while a route mapped to
// Display instead of Alter handed a Data Entry clerk an inventory escape hatch.
// The (action, resource) pair IS the authorization here: mapping the claim to
// Display would let a level that may not book purchases reserve other people's
// bills, and nothing structural would say so.
func TestTheSixDraftRoutesMapOntoThePurchaseToken(t *testing.T) {
	const co = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		method, path string
		wantAct      authz.Action
	}{
		{http.MethodGet, "/drafts", authz.Display},
		{http.MethodGet, "/drafts/D-1", authz.Display},
		{http.MethodPost, "/drafts", authz.Create},
		{http.MethodPost, "/drafts/D-1/claim", authz.Create},
		{http.MethodPost, "/drafts/D-1/claim/release", authz.Create},
		{http.MethodPost, "/drafts/D-1/accepted", authz.Create},
	}
	for _, tc := range cases {
		// ⚠️ THE CHI PARAM IS NOT OPTIONAL HERE. routeAction asks routeSuffix for
		// the path AFTER /v1/companies/{companyID}, and routeSuffix strips that
		// prefix by asking chi for the URL param — so a synthesised request without
		// one is judged on the WHOLE path and every arm misses. Same plumbing note
		// as withRouteCtx in auth_backdated_wire_date_test.go.
		req := withChiParam(httptest.NewRequest(tc.method, "/v1/companies/"+co+tc.path, nil), "companyID", co)
		act, res, resName, mapped := routeAction(req)
		if !mapped {
			t.Errorf("%s %s is UNMAPPED — an unmapped {companyID} sub-path default-denies, so the route is dead", tc.method, tc.path)
			continue
		}
		if act != tc.wantAct {
			t.Errorf("%s %s -> action %q, want %q", tc.method, tc.path, act, tc.wantAct)
		}
		if res != "purchase" {
			t.Errorf("%s %s -> resource %q, want \"purchase\" (a draft IS a proposed purchase voucher; migration 0011 already grants Data Entry create|display on it, which is why no migration was needed)", tc.method, tc.path, res)
		}
		if resName != "" {
			t.Errorf("%s %s -> resource NAME %q, want empty (purchase is a class token, not report:NAME)", tc.method, tc.path, resName)
		}
	}
}

// TestAnUnknownDraftSubPathDefaultDenies is the other half of the token test: the
// mapping must be a fixed list of SHAPES, not a /drafts prefix.
//
// ⛔ A prefix arm would authorize every future /drafts/... sub-path the moment
// somebody mounted one — born permitted by a rule nobody wrote for it, which is
// the exact opposite of what routeAction's default-deny is for.
func TestAnUnknownDraftSubPathDefaultDenies(t *testing.T) {
	const co = "11111111-1111-1111-1111-111111111111"
	for _, path := range []string{
		"/drafts/D-1/post",            // a future route
		"/drafts/D-1/claim/steal",     // a plausible sibling of claim/release
		"/drafts/D-1/accepted/undo",   // deeper than any real shape
		"/drafts/D-1/claim/release/x", // one segment too many
	} {
		req := withChiParam(httptest.NewRequest(http.MethodPost, "/v1/companies/"+co+path, nil), "companyID", co)
		if _, _, _, mapped := routeAction(req); mapped {
			t.Errorf("POST %s is MAPPED — isDraftPath must match the six shapes and nothing else, or a route mounted later is born authorized", path)
		}
	}
}

// TestTheDraftUploadKeepsItsOwnBodyCap is the silent-truncation trap.
//
// ⛔ WHY IT MATTERS THAT THIS IS NOT maxJSONBody. drainBody REPLACES r.Body with
// what it read, so a cap smaller than the route's own cap feeds the handler a
// truncated body. `POST .../drafts` carries a base64 SCAN — the Bill Inbox has a
// file input and does btoa(binary) — and base64 inflates by 4/3, so an ordinary
// ~800 KB photo of a bill exceeds 1 MiB on the wire. While the browser posted
// straight to insightd there was no cap in the way at all; moving the route behind
// cored put one there, and the symptom would be a 422 from insightd on a body the
// operator's own file produced.
func TestTheDraftUploadKeepsItsOwnBodyCap(t *testing.T) {
	const co = "11111111-1111-1111-1111-111111111111"
	create := httptest.NewRequest(http.MethodPost, "/v1/companies/"+co+"/drafts", nil)
	if got := bodyCapFor(create); got != maxDraftUploadBody {
		t.Errorf("bodyCapFor(POST /drafts) = %d, want maxDraftUploadBody %d — handleDraftProxy declares that number, and if this is smaller the proxy forwards a TRUNCATED scan", got, maxDraftUploadBody)
	}
	// The other five carry a claim token or a voucher id: ordinary JSON.
	for _, path := range []string{"/drafts/D-1/claim", "/drafts/D-1/claim/release", "/drafts/D-1/accepted"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/companies/"+co+path, nil)
		if got := bodyCapFor(req); got != maxJSONBody {
			t.Errorf("bodyCapFor(POST %s) = %d, want maxJSONBody %d — only the CREATE carries a scan, and a 16 MiB buffer on the authorization path for a claim token is a memory sink for nothing", path, got, maxJSONBody)
		}
	}
	// And a body over 1 MiB actually survives the peek on the create.
	big := strings.Repeat("x", 2<<20)
	body := `{"source":"upload","content_b64":"` + big + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/companies/"+co+"/drafts", strings.NewReader(body))
	drained := drainBody(req)
	restored, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if len(restored) != len(body) {
		t.Errorf("the create's body was restored as %d bytes of %d — TRUNCATED, so insightd parses a cut-off scan", len(restored), len(body))
	}
	if !bytes.Equal(drained, restored) {
		t.Error("the peek and the handler saw different bodies")
	}
}

// fakeInsightd stands in for the service. It records what the proxy sent and
// answers with whatever the test told it to.
type fakeInsightd struct {
	gotMethod  string
	gotPath    string
	gotQuery   string
	gotToken   string
	gotCompany string
	gotBody    string
	status     int
	body       string
	ctype      string
}

func (f *fakeInsightd) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.gotMethod = r.Method
		f.gotPath = r.URL.Path
		f.gotQuery = r.URL.RawQuery
		f.gotToken = r.Header.Get(insightdServiceTokenHeader)
		f.gotCompany = r.Header.Get(insightdCompanyHeader)
		b, _ := io.ReadAll(r.Body)
		f.gotBody = string(b)
		ct := f.ctype
		if ct == "" {
			ct = "application/json"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
	}))
}

// authorizedRequest builds a request as companyAuthMiddleware would have left it:
// the AUTHORIZED company injected into the context, and the chi route param set.
func authorizedRequest(t *testing.T, method, suffix string, companyID uuid.UUID, body string) *http.Request {
	t.Helper()
	var r *http.Request
	target := "/v1/companies/" + companyID.String() + suffix
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	r = withChiParam(r, "companyID", companyID.String())
	return r.WithContext(db.WithCompany(r.Context(), companyID))
}

// TestTheProxyPassesInsightdsRefusalThroughBYTEFORBYTE is the arm that protects a
// money defect.
//
// ⛔⛔ THE CLIENT'S DUPLICATE-BILL GUARD READS `err.body.detail.code`, AND ONLY FOR
// STATUS 409. insightd mints three such codes. All FOUR insightd-backed layers of
// that guard fail OPEN on any non-409 — claimDraftForPost warns and returns null,
// draftAlreadyAcceptedInsightd returns null, releaseClaimAfterFailure never
// throws, recordAccepted is fire-and-forget — so a proxy that reshaped the 409
// into cored's own {"error": ...} form, or normalised its status, would turn three
// specific refusals into the fail-open branch. The accept would still SUCCEED and
// the only evidence would be a console.warn. Tidying a response is how a duplicate
// supplier bill gets back into the books.
func TestTheProxyPassesInsightdsRefusalThroughBYTEFORBYTE(t *testing.T) {
	const conflict = `{"detail":{"code":"DRAFT_ALREADY_ACCEPTED","draft_id":"D-1","voucher_no":"PUR-7"}}`
	fake := &fakeInsightd{status: http.StatusConflict, body: conflict}
	srv := fake.server()
	defer srv.Close()

	s := &Server{drafts: &draftProxy{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}}
	co := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	r := authorizedRequest(t, http.MethodPost, "/drafts/D-1/accepted", co, `{"voucher_id":"V","voucher_no":"1"}`)
	w := httptest.NewRecorder()
	s.handleDraftProxy(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409 — a normalised status is the fail-open branch", w.Code)
	}
	if got := w.Body.String(); got != conflict {
		t.Fatalf("body was reshaped.\n got: %s\nwant: %s\nThe guard reads err.body.detail.code and nothing else.", got, conflict)
	}
	// And the code survives an actual decode, not just a string compare.
	var decoded struct {
		Detail struct{ Code string } `json:"detail"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil || decoded.Detail.Code != "DRAFT_ALREADY_ACCEPTED" {
		t.Fatalf("detail.code did not survive: %v / %q", err, decoded.Detail.Code)
	}
}

// TestTheProxySendsTheAUTHORIZEDCompanyAndNotThePathParam is the whole fix in one
// assertion.
//
// ⛔ The company insightd is told is the one THIS PDP authorized, taken from the
// context the middleware injected. Sending the path param instead would forward
// exactly the value the caller typed, which is the defect: insightd would then be
// scoping by caller-supplied input again, one process further along.
func TestTheProxySendsTheAUTHORIZEDCompanyAndNotThePathParam(t *testing.T) {
	fake := &fakeInsightd{status: http.StatusOK, body: `[]`}
	srv := fake.server()
	defer srv.Close()

	s := &Server{drafts: &draftProxy{BaseURL: srv.URL, Token: "sekrit", HTTP: srv.Client()}}
	co := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	r := authorizedRequest(t, http.MethodGet, "/drafts", co, "")
	r.URL.RawQuery = "limit=5"
	w := httptest.NewRecorder()
	s.handleDraftProxy(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if fake.gotCompany != co.String() {
		t.Errorf("X-Insightd-Company = %q, want the authorized %q", fake.gotCompany, co)
	}
	if fake.gotToken != "sekrit" {
		t.Errorf("X-Insightd-Service-Token = %q, want the configured credential", fake.gotToken)
	}
	if fake.gotPath != "/companies/"+co.String()+"/drafts" {
		t.Errorf("upstream path = %q, want insightd's own /companies/{id}/drafts shape", fake.gotPath)
	}
	if fake.gotQuery != "limit=5" {
		t.Errorf("query = %q, want it forwarded verbatim", fake.gotQuery)
	}
}

// TestAnUnconfiguredProxyREFUSESRatherThanForwarding.
//
// ⛔ FORWARDING WITHOUT A CREDENTIAL WOULD WORK PERFECTLY against an insightd that
// had also been left unconfigured — which is the one outcome this row exists to
// make impossible. Refusing loudly on both sides is the point: cored says it has
// no token, insightd says it has none either, and neither serves the drafts.
func TestAnUnconfiguredProxyREFUSESRatherThanForwarding(t *testing.T) {
	fake := &fakeInsightd{status: http.StatusOK, body: `["should never be reached"]`}
	srv := fake.server()
	defer srv.Close()

	s := &Server{drafts: &draftProxy{BaseURL: srv.URL, Token: "", HTTP: srv.Client()}}
	co := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	w := httptest.NewRecorder()
	s.handleDraftProxy(w, authorizedRequest(t, http.MethodGet, "/drafts", co, ""))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "INSIGHTD_SERVICE_TOKEN") {
		t.Errorf("the refusal must NAME the variable that is missing; got %s", w.Body.String())
	}
	if fake.gotMethod != "" {
		t.Error("the proxy contacted insightd anyway — an unconfigured proxy must not forward at all")
	}
}

// TestTheProxyRefusesWithoutAnAuthorizedCompanyInContext is defence in depth
// against a future mounting of this handler outside the authed subtree.
func TestTheProxyRefusesWithoutAnAuthorizedCompanyInContext(t *testing.T) {
	fake := &fakeInsightd{status: http.StatusOK, body: `[]`}
	srv := fake.server()
	defer srv.Close()

	s := &Server{drafts: &draftProxy{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}}
	co := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	// No db.WithCompany in the context: the middleware did not run.
	r := withChiParam(httptest.NewRequest(http.MethodGet, "/v1/companies/"+co.String()+"/drafts", nil), "companyID", co.String())
	w := httptest.NewRecorder()
	s.handleDraftProxy(w, r)

	if w.Code == http.StatusOK {
		t.Fatalf("served 200 with no authorized company in context — the path param must never be the scope again")
	}
	if fake.gotMethod != "" {
		t.Error("the proxy forwarded a request nobody authorized")
	}
}

// TestTheProxyReportsAnUnreachableInsightdAs503 — insightd is a separate
// deployable that is routinely not running, and that must not look like a books
// problem. 503 is also what the client's draft layers already treat as "insightd
// unreachable" (D15's window), so this path is the status quo.
func TestTheProxyReportsAnUnreachableInsightdAs503(t *testing.T) {
	// A closed port on loopback.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead := srv.URL
	srv.Close()

	s := &Server{drafts: &draftProxy{BaseURL: dead, Token: "tok", HTTP: &http.Client{}}}
	co := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	w := httptest.NewRecorder()
	s.handleDraftProxy(w, authorizedRequest(t, http.MethodGet, "/drafts", co, ""))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "insightd") {
		t.Errorf("the refusal must name the service that is not answering; got %s", w.Body.String())
	}
}

// TestTheProxyForwardsTheCreateBodyWhole — the companion to the cap test, one
// layer up: whatever the middleware restored must reach insightd unchanged.
func TestTheProxyForwardsTheCreateBodyWhole(t *testing.T) {
	fake := &fakeInsightd{status: http.StatusCreated, body: `{"draft_id":"D-9"}`}
	srv := fake.server()
	defer srv.Close()

	s := &Server{drafts: &draftProxy{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}}
	co := uuid.MustParse("77777777-7777-7777-7777-777777777777")
	scan := strings.Repeat("A", 3<<20) // 3 MiB of base64, over maxJSONBody
	body := `{"source":"upload","content_b64":"` + scan + `"}`
	r := authorizedRequest(t, http.MethodPost, "/drafts", co, body)
	w := httptest.NewRecorder()
	s.handleDraftProxy(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if len(fake.gotBody) != len(body) {
		t.Errorf("insightd received %d bytes of a %d-byte scan — TRUNCATED", len(fake.gotBody), len(body))
	}
}

// withChiParam attaches a chi URL param to a request, so routeSuffix and
// chi.URLParam behave as they do inside the mounted router. (withRouteCtx in
// auth_backdated_wire_date_test.go carries the same note: a synthesised request
// without it never sees the route the middleware would see.)
func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
