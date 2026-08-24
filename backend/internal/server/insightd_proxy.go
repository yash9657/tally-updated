package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/khata/cored/internal/platform/db"
)

// insightd_proxy.go makes CORED THE ONLY DOOR to insightd (register row D49).
//
// ⛔⛔ THE DEFECT THIS CLOSES, MEASURED ON THE WIRE 2026-08-24 BEFORE A LINE WAS
// WRITTEN. insightd authenticated nobody and scoped every draft route by the
// company_id in the PATH:
//
//	curl :8088/companies/<any-uuid>/drafts   ->  200, 15 drafts, in full
//	                                             ("Pune Fabricators | 21240.0000")
//	curl :8087/v1/companies/<same-uuid>      ->  401
//
// Supplier names, invoice numbers, dates, posting ledger names, amounts and GST
// state codes, for any tenant whose uuid the caller could name, from a process
// vite proxied SAME-ORIGIN at /companies/*. The Postgres RLS underneath was never
// a second layer: pg_store set the tenant GUC from that same path parameter, so it
// faithfully enforced whichever scope the caller chose.
//
// ⛔ WHY A PROXY AND NOT A TOKEN IN insightd (the recommendation on file, and it
// was re-verified at HEAD rather than trusted):
//
//   - cored's dev issuer generates an EPHEMERAL Ed25519 key (devIssuer, no
//     AUTH_DEV_SIGNING_KEY in the Makefile) and cored publishes NO JWKS route, so
//     there is nothing for a second verifier to verify against in dev at all;
//   - insightd's core dependency set is pydantic/fastapi/httpx/uvicorn/pypdf —
//     no crypto library, by a rule its own import-lint enforces;
//   - and the part that is not a dependency problem: A SIGNATURE PROVES *WHO*,
//     NEVER *WHICH COMPANY*. Membership and the per-(action, resource) rules live
//     in this database behind this PDP, so a token validated in insightd would
//     need a SECOND COPY of cored's PDP in another language to decide anything.
//
// So the browser authenticates to cored, with the bearer it already holds, and
// this file forwards the request after the SAME middleware that guards every other
// company route has finished with it: verified JWT, live (non-revoked) session,
// provisioned and active user, membership in {companyID}, and the PDP's verdict on
// (Display|Create, "purchase").
//
// ⛔ THE TWO HEADERS, AND WHAT EACH ONE IS FOR:
//
//	X-Insightd-Service-Token  a credential held by exactly two server processes.
//	                          It says "this request came through the proxy" and
//	                          nothing else. It is NOT the shared secret insightd's
//	                          app.py refuses: that refusal is about a secret the
//	                          BROWSER holds, which anything that can read the page
//	                          can read, and which still would not name a company.
//	X-Insightd-Company        the company THIS PDP JUST AUTHORIZED, taken from the
//	                          context the middleware injected — never from the path
//	                          param. insightd refuses any request where it and the
//	                          path disagree, which is what retires "caller-supplied
//	                          company_id in the path".
//
// ⛔⛔ THE RESPONSE IS PASSED THROUGH BYTE FOR BYTE, AND THAT IS LOAD-BEARING
// RATHER THAN LAZY. The client's duplicate-bill guard reads
// `err.body.detail.code` and ONLY for status 409 (draftAccept.ts's conflictCode);
// the three codes are minted in insightd's routes_drafts. A proxy that reshaped
// the body into cored's own {"error": ...} form, or normalised the status, would
// turn three specific refusals into the guard's fail-open branch — and every one
// of its four insightd-backed layers fails OPEN on a non-409, silently, with a
// console.warn as the only evidence. Re-opening a money defect by tidying a
// response is exactly the kind of thing that goes unnoticed for three sessions.

// insightdDefaultBaseURL is the Makefile's dev port. ⛔ Like statutoryd's, the
// location IS configuration: insightd is a second process that is routinely not
// running, and a compiled-in default that disagreed with a customised
// INSIGHTD_PORT would present as "insightd is not answering" while insightd was
// plainly running on the port the Makefile printed.
const insightdDefaultBaseURL = "http://localhost:8088"

// insightdDefaultTimeout bounds a forwarded draft call.
//
// ⚠️ IT IS LONGER THAN A REPORT AND SHORTER THAN AN OCR RUN, DELIBERATELY. With
// the real recogniser insightd does NOT do the slow work inside the request — it
// stores a "processing" placeholder and runs the pipeline in a background task,
// precisely because a 12 MP phone photo measured ~45s and a client that timed out
// would retry and OCR the same bill twice. With the deterministic fakes the
// pipeline finishes inline. So the request this proxy waits on is short in both
// modes, and a long timeout here would only ever be waiting on something wedged.
const insightdDefaultTimeout = 30 * time.Second

// The two headers. Named here and in insightd's api/authn.py, and nowhere else.
const (
	insightdServiceTokenHeader = "X-Insightd-Service-Token"
	insightdCompanyHeader      = "X-Insightd-Company"
)

// errInsightdNotConfigured is returned when no service token is set. ⛔ THE
// PROXY REFUSES RATHER THAN FORWARDING WITHOUT ONE: forwarding a credential-less
// request would work perfectly against an insightd that had also been left
// unconfigured, which is the one outcome this row exists to make impossible.
var errInsightdNotConfigured = errors.New("insightd service token not configured")

// draftProxy forwards one already-authorized request to insightd.
type draftProxy struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// newDraftProxy builds the proxy from the environment.
//
// ⚠️ AN EMPTY TOKEN IS KEPT AS EMPTY rather than defaulted to anything. There is
// no safe default for a credential, and a compiled-in one would be a credential
// published in a public repository.
func newDraftProxy() *draftProxy {
	timeout := insightdDefaultTimeout
	if raw := os.Getenv("INSIGHTD_TIMEOUT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			timeout = d
		} else {
			// Same rule as STATUTORYD_TIMEOUT: fall back rather than refusing to
			// boot the whole server over one optional knob for one side plane —
			// but never SILENTLY, or a deployment that meant 5m sits at 30s
			// forever with nothing anywhere saying why.
			log.Printf("cored: INSIGHTD_TIMEOUT=%q is not a positive Go duration (e.g. \"45s\"); using %s", raw, insightdDefaultTimeout)
		}
	}
	return &draftProxy{
		BaseURL: strings.TrimRight(envOr("INSIGHTD_BASE_URL", insightdDefaultBaseURL), "/"),
		Token:   strings.TrimSpace(os.Getenv("INSIGHTD_SERVICE_TOKEN")),
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// maxDraftResponse caps how much of insightd's answer is read back. A hydrated
// draft with its OCR blocks and redaction report is tens of kilobytes; anything
// past this is a misconfigured URL answering with something else entirely, and
// that must not become an unbounded read into cored's memory.
const maxDraftResponse = 4 << 20

// forward sends one request to insightd and returns its status, content type and
// body verbatim.
func (p *draftProxy) forward(ctx context.Context, method, path, rawQuery string, body io.Reader, companyID string) (int, string, []byte, error) {
	if p.Token == "" {
		return 0, "", nil, errInsightdNotConfigured
	}
	u := p.BaseURL + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	if _, err := url.Parse(u); err != nil {
		return 0, "", nil, fmt.Errorf("insightd: build url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return 0, "", nil, fmt.Errorf("insightd: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(insightdServiceTokenHeader, p.Token)
	req.Header.Set(insightdCompanyHeader, companyID)

	resp, err := p.HTTP.Do(req)
	if err != nil {
		return 0, "", nil, fmt.Errorf("insightd: %w", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, maxDraftResponse))
	if err != nil {
		return 0, "", nil, fmt.Errorf("insightd: read response: %w", err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), out, nil
}

// handleDraftProxy serves all six /drafts routes.
//
// ⚠️ ONE HANDLER FOR SIX ROUTES, AND THE ROUTER IS WHAT ENUMERATES THEM. The
// upstream path is derived from routeSuffix, so this cannot forward a path chi did
// not route and routeAction did not map — the six r.Get/r.Post lines in routes()
// are the whole surface, and an unmounted seventh is a 404 rather than something
// this function invents. (routeAction default-denies unmapped {companyID}
// sub-paths, so a route mounted without a mapping is refused, not served
// unauthorized; TestEveryCompanyRouteIsMappedAndInVocabulary walks the router and
// turns the suite red if the mapping is missing.)
func (s *Server) handleDraftProxy(w http.ResponseWriter, r *http.Request) {
	// The AUTHORIZED company, from the context companyAuthMiddleware injected —
	// never chi.URLParam. scopeCtxID cross-checks the two and 400s on a mismatch,
	// which is the same disagreement insightd refuses on its side.
	_, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	if _, hasAuthorized := db.CompanyFromContext(r.Context()); !hasAuthorized {
		// Defence in depth against a future mounting of this handler OUTSIDE the
		// authed subtree. Without the middleware there is no authorized company,
		// and forwarding the path param would be the defect this file closes.
		writeError(w, http.StatusInternalServerError, "draft proxy reached without an authorized company")
		return
	}

	upstream := "/companies/" + companyID.String() + routeSuffix(r)

	var body io.Reader
	if r.Body != nil {
		// ⛔ maxDraftUploadBody, NOT maxJSONBody. The create carries a base64 scan
		// and bodyCapFor declares the same number for this route — the two caps
		// must agree or the middleware hands this handler a truncated body.
		body = io.LimitReader(r.Body, maxDraftUploadBody)
	}
	status, contentType, out, err := s.drafts.forward(
		r.Context(), r.Method, upstream, r.URL.RawQuery, body, companyID.String(),
	)
	if err != nil {
		if errors.Is(err, errInsightdNotConfigured) {
			writeError(w, http.StatusServiceUnavailable,
				"the AI bill inbox is not configured: cored has no INSIGHTD_SERVICE_TOKEN, so it will not forward a request to insightd without one")
			return
		}
		// 503 and not 502: insightd not answering is transient and the operator
		// should try again. ⚠️ It is also what the client's draft layers already
		// treat as "insightd unreachable" — the window D15 documented — so this
		// path is the status quo rather than a new failure mode.
		writeError(w, http.StatusServiceUnavailable, "insightd is not answering: "+err.Error())
		return
	}

	// ⛔ VERBATIM. Status, content type and bytes. See the header comment: the 409
	// body carries the three refusal codes the duplicate-bill guard reads, and
	// every reshaping of it is a silent fail-open.
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(out)
}
