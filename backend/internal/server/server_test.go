// Package server_test is the HTTP smoke test for the Phase 1 REST surface. It
// drives the real chi router through httptest (no socket) against the live
// Postgres, exercising the end-to-end path a frontend uses: health check,
// company creation, master creation, a balanced Sales voucher post, and a
// report read. It proves the composition root wires the modules together and
// that every write still flows through the tenant-scoped GUC guard.
package server_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/server"
	"github.com/khata/cored/migrations"
)

// ADMIN_DATABASE_URL / DATABASE_URL override the shared dev database (the
// Makefile exports both with these defaults), so a lane on its own database
// never migrates the shared one.
var (
	adminDSN = envDSN("ADMIN_DATABASE_URL", "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable")
	appDSN   = envDSN("DATABASE_URL", "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable")
)

func envDSN(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// newServer applies migrations (idempotent) and returns a wired Server. It
// skips the test when Postgres is unavailable so the suite stays green offline.
// TestAuthFailsClosedByDefault is the regression guard for the critical
// token-minting-oracle hole: with NO auth env configured, server.New must
// REFUSE to start rather than silently booting the open dev issuer. It also
// pins the WS6 production (AUTH_JWKS_URL) boot matrix: a live JWKS boots without
// the dev signer; a missing AUTH_ISSUER, a dead endpoint, or a JWKS with no
// usable asymmetric key all fail closed — never a fall back to the dev issuer.
func TestAuthFailsClosedByDefault(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	ctx := context.Background()
	if c, err := pgx.Connect(ctx, appDSN); err != nil {
		t.Skipf("app postgres unavailable, skipping: %v", err)
	} else {
		_ = c.Close(ctx)
	}

	t.Run("no auth configured -> refuse to start", func(t *testing.T) {
		t.Setenv("AUTH_DEV_TOKEN_ENABLED", "")
		t.Setenv("CORED_ENV", "")
		t.Setenv("AUTH_JWKS_URL", "")
		srv, err := server.New(ctx, appDSN)
		if err == nil {
			srv.Close()
			t.Fatal("server.New must refuse to start with no auth configured (fail-open is a critical hole)")
		}
		if !strings.Contains(err.Error(), "no authentication configured") {
			t.Fatalf("expected fail-closed auth error, got: %v", err)
		}
	})

	// WS6: AUTH_JWKS_URL is now a REAL production mode (authn.JWKSVerifier). The
	// contract it must keep is the fail-closed half: it must never silently fall
	// back to the dev issuer, and a misconfigured/unreachable IdP must be a hard
	// BOOT failure (buildAuth fetches the JWKS once, eagerly), not a process that
	// starts and 401s every request.

	t.Run("AUTH_JWKS_URL without AUTH_ISSUER refuses to start", func(t *testing.T) {
		t.Setenv("AUTH_DEV_TOKEN_ENABLED", "")
		t.Setenv("CORED_ENV", "")
		t.Setenv("AUTH_ISSUER", "")
		t.Setenv("AUTH_JWKS_URL", "http://127.0.0.1:1/jwks")
		srv, err := server.New(ctx, appDSN)
		if err == nil {
			srv.Close()
			t.Fatal("server.New must refuse to start with AUTH_JWKS_URL and no AUTH_ISSUER (an unpinned issuer accepts any realm's tokens)")
		}
		if !strings.Contains(err.Error(), "AUTH_ISSUER") {
			t.Fatalf("expected an AUTH_ISSUER fail-closed error, got: %v", err)
		}
	})

	// D102: the audience pin. The issuer pin above scopes acceptance to a REALM,
	// and every client in a realm mints tokens carrying the same iss — so without
	// this second pin a token a user legitimately obtained for ANY OTHER
	// application in that realm authenticates here as that subject.
	t.Run("AUTH_JWKS_URL without AUTH_AUDIENCE refuses to start", func(t *testing.T) {
		t.Setenv("AUTH_DEV_TOKEN_ENABLED", "")
		t.Setenv("CORED_ENV", "")
		t.Setenv("AUTH_ISSUER", jwksTestIssuer)
		t.Setenv("AUTH_AUDIENCE", "")
		t.Setenv("AUTH_JWKS_URL", "http://127.0.0.1:1/jwks")
		srv, err := server.New(ctx, appDSN)
		if err == nil {
			srv.Close()
			t.Fatal("server.New must refuse to start with AUTH_JWKS_URL and no AUTH_AUDIENCE (any client in the pinned realm would be accepted)")
		}
		if !strings.Contains(err.Error(), "AUTH_AUDIENCE") {
			t.Fatalf("expected an AUTH_AUDIENCE fail-closed error, got: %v", err)
		}
		// ⭐ AND IT MUST REFUSE BEFORE IT DIALS. The URL above is a dead port: if
		// the audience guard ran AFTER the fetch this would be the unreachable
		// error instead, and an operator with a LIVE IdP and no audience would
		// boot wide open — the exact configuration this defect is about.
		if strings.Contains(err.Error(), "unreachable") {
			t.Fatalf("the audience guard ran after the JWKS fetch; it must run before: %v", err)
		}
	})

	t.Run("AUTH_JWKS_URL at a dead endpoint fails closed", func(t *testing.T) {
		t.Setenv("AUTH_DEV_TOKEN_ENABLED", "")
		t.Setenv("CORED_ENV", "")
		t.Setenv("AUTH_ISSUER", jwksTestIssuer)
		t.Setenv("AUTH_AUDIENCE", jwksTestAudience)
		// Port 1 on loopback: guaranteed-dead, no DNS, refuses immediately (so
		// this cannot hang on the 5s boot timeout in CI).
		t.Setenv("AUTH_JWKS_URL", "http://127.0.0.1:1/jwks")
		srv, err := server.New(ctx, appDSN)
		if err == nil {
			srv.Close()
			t.Fatal("server.New must not boot (nor fall back to the dev issuer) when the JWKS endpoint is unreachable")
		}
		// ⛔ "unreachable", not merely "JWKS". The audience refusal added by D102
		// also contains the substring "JWKS" (inside "AUTH_JWKS_URL"), so the
		// looser assertion this replaced would pass against the WRONG error.
		if !strings.Contains(err.Error(), "unreachable") {
			t.Fatalf("expected an UNREACHABLE-JWKS boot error, got: %v", err)
		}
	})

	t.Run("AUTH_JWKS_URL serving an unusable JWKS fails closed", func(t *testing.T) {
		// A document containing ONLY a symmetric key must not boot: symmetric
		// keys are never admitted as verification keys.
		js := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"keys":[{"kty":"oct","kid":"h","k":"c2VjcmV0"}]}`))
		}))
		defer js.Close()
		t.Setenv("AUTH_DEV_TOKEN_ENABLED", "")
		t.Setenv("CORED_ENV", "")
		t.Setenv("AUTH_ISSUER", jwksTestIssuer)
		t.Setenv("AUTH_AUDIENCE", jwksTestAudience)
		t.Setenv("AUTH_JWKS_URL", js.URL)
		srv, err := server.New(ctx, appDSN)
		if err == nil {
			srv.Close()
			t.Fatal("server.New must not boot against a JWKS with no usable asymmetric key")
		}
		// Same reasoning as the subtest above: name the actual failure. A JWKS
		// carrying only a symmetric key is SERVED fine — it is invalid, not
		// unreachable — so this must not match the audience refusal either.
		if !strings.Contains(err.Error(), "invalid JWKS") {
			t.Fatalf("expected an INVALID-JWKS boot error, got: %v", err)
		}
	})

	t.Run("AUTH_JWKS_URL at a live JWKS boots in production mode", func(t *testing.T) {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("gen key: %v", err)
		}
		doc, err := json.Marshal(map[string]any{"keys": []map[string]any{{
			"kty": "OKP", "crv": "Ed25519", "kid": "idp-1", "use": "sig",
			"x": base64.RawURLEncoding.EncodeToString(pub),
		}}})
		if err != nil {
			t.Fatalf("marshal jwks: %v", err)
		}
		js := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(doc)
		}))
		defer js.Close()

		t.Setenv("AUTH_DEV_TOKEN_ENABLED", "")
		t.Setenv("CORED_ENV", "")
		t.Setenv("AUTH_ISSUER", jwksTestIssuer)
		t.Setenv("AUTH_AUDIENCE", jwksTestAudience)
		t.Setenv("AUTH_JWKS_URL", js.URL)
		srv, err := server.New(ctx, appDSN)
		if err != nil {
			t.Fatalf("server.New with a reachable JWKS must boot, got: %v", err)
		}
		defer srv.Close()

		// Production shape: NO in-process signer, so the dev token-minting oracle
		// is NOT mounted (the endpoint that would otherwise mint a valid token for
		// any subject).
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/auth/token", strings.NewReader(`{"subject":"attacker"}`))
		req.Header.Set("Content-Type", "application/json")
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("/auth/token in JWKS mode = %d, want 404/405 (the dev minting oracle must not be mounted)", rec.Code)
		}
		// And an unsigned/garbage token is still 401 on a real route.
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/v1/companies/"+uuid.NewString()+"/reports/trial-balance", nil)
		req.Header.Set("Authorization", "Bearer not-a-token")
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("garbage bearer in JWKS mode = %d, want 401", rec.Code)
		}

		// ...and the booted server ACTUALLY VERIFIES against the JWKS, which the
		// two negative probes above cannot show (a verifier that rejected every
		// token would pass them). A token signed by the IdP's private key, under
		// the published kid and the pinned issuer, is AUTHENTICATED: it gets past
		// the 401 gate and is refused at the AUTHORIZATION gate instead (403 —
		// the subject has no app_user row; the 401-vs-403 contract pinned by
		// TestUnprovisionedUserTokenIs403).
		idpToken, err := authn.NewSigner(priv, "idp-1", jwksTestIssuer).Sign(authn.Claims{
			Subject:   "idp-user-" + uuid.NewString(),
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
			// D102: minted FOR this application. Without it the token is refused,
			// which is the whole point of the audience pin.
			Audience: authn.Audience{jwksTestAudience},
		})
		if err != nil {
			t.Fatalf("sign IdP token: %v", err)
		}
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/v1/companies/"+uuid.NewString()+"/reports/trial-balance", nil)
		req.Header.Set("Authorization", "Bearer "+idpToken)
		srv.Router().ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("a token signed by the published JWKS key was rejected as invalid (401) — the JWKS verifier is not actually verifying (body %s)", rec.Body.String())
		}
		if rec.Code != http.StatusForbidden {
			t.Fatalf("JWKS-signed token status = %d, want 403 (authenticated, then unauthorized) (body %s)", rec.Code, rec.Body.String())
		}

		// Control on the same booted server: the SAME claims signed by a DIFFERENT
		// key under the same kid must be 401, so the 403 above is attributable to
		// the signature check and not to the route 403-ing everything.
		_, wrongPriv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("gen key: %v", err)
		}
		forged, err := authn.NewSigner(wrongPriv, "idp-1", jwksTestIssuer).Sign(authn.Claims{
			Subject:   "idp-user-" + uuid.NewString(),
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		})
		if err != nil {
			t.Fatalf("sign forged token: %v", err)
		}
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/v1/companies/"+uuid.NewString()+"/reports/trial-balance", nil)
		req.Header.Set("Authorization", "Bearer "+forged)
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token forged under the published kid = %d, want 401", rec.Code)
		}

		// ⛔⛔ D102 END TO END, AND THIS IS THE ARM THAT ACTUALLY PROVES THE FIX.
		// A token signed by the REAL IdP key, under the published kid, carrying the
		// PINNED ISSUER, with a valid exp and a well-formed subject — everything
		// the pre-D102 verifier checked — but minted for a DIFFERENT application in
		// the same realm. This is precisely the token a user legitimately holds for
		// another internal app, and before the fix it authenticated here as that
		// subject and reached the authorization gate (403). It must now be 401.
		//
		// ⚠️ 401-vs-403 IS THE WHOLE ASSERTION. 403 would mean the token was
		// AUTHENTICATED and merely lacked permissions — i.e. the audience check did
		// not run. Do not loosen this to "not 200".
		otherApp, err := authn.NewSigner(priv, "idp-1", jwksTestIssuer).Sign(authn.Claims{
			Subject:   "idp-user-" + uuid.NewString(),
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
			Audience:  authn.Audience{"some-other-app-in-the-same-realm"},
		})
		if err != nil {
			t.Fatalf("sign other-audience token: %v", err)
		}
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/v1/companies/"+uuid.NewString()+"/reports/trial-balance", nil)
		req.Header.Set("Authorization", "Bearer "+otherApp)
		srv.Router().ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden {
			t.Fatalf("a token minted for ANOTHER application in the same realm was AUTHENTICATED (403 = it got past the 401 gate). The audience check is not running on the request path — this is D102 exactly")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("other-audience token = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
	})
}

// jwksTestIssuer is the iss the JWKS-mode boot tests pin via AUTH_ISSUER.
const jwksTestIssuer = "https://idp.test/realms/tally"

// jwksTestAudience is the OIDC client id cored's tokens are minted for (D102).
// ⛔ EVERY JWKS SUBTEST MUST SET IT, INCLUDING THE ONES THAT EXPECT A REFUSAL.
// buildAuth checks the audience BEFORE it fetches the JWKS, so a subtest that
// leaves it empty gets the audience refusal rather than the failure it is
// actually testing — and because that message contains the substring
// "AUTH_JWKS_URL", a Contains(err, "JWKS") assertion goes on PASSING while
// testing nothing at all.
const jwksTestAudience = "cored"

func newServer(t *testing.T) *server.Server {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping smoke test: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	// Auth is fail-closed by default; tests opt into the in-process dev issuer
	// (mounts /auth/token + a deterministic signer) so do()/mintToken work.
	t.Setenv("AUTH_DEV_TOKEN_ENABLED", "true")

	srv, err := server.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// ownerTokens caches a per-server Owner token so every do() request carries a
// valid Bearer for a consistent Owner subject. The first POST /v1/companies with
// that token (create-on-first-login) makes the subject an Owner of the company it
// creates; the same token then authorizes all subsequent {companyID} requests.
var ownerTokens sync.Map // *server.Server -> string

// ownerToken mints (once per server) a token for a unique Owner subject via the
// live POST /auth/token dev endpoint, with AMR ["webauthn"] so the Owner level's
// requires_webauthn step-up is satisfied on writes.
func ownerToken(t *testing.T, srv *server.Server) string {
	t.Helper()
	if v, ok := ownerTokens.Load(srv); ok {
		return v.(string)
	}
	subject := "owner-" + uuid.NewString()
	rec, body := doRaw(t, srv, http.MethodPost, "/auth/token", "", map[string]any{
		"subject": subject,
		"amr":     []string{"webauthn"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("mint owner token status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	tok, _ := body["token"].(string)
	if tok == "" {
		t.Fatalf("mint owner token returned no token (body %s)", rec.Body.String())
	}
	ownerTokens.Store(srv, tok)
	return tok
}

// mintToken mints a token for an arbitrary subject (and AMR) via /auth/token.
func mintToken(t *testing.T, srv *server.Server, subject string, amr ...string) string {
	t.Helper()
	rec, body := doRaw(t, srv, http.MethodPost, "/auth/token", "", map[string]any{
		"subject": subject,
		"amr":     amr,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("mint token %q status = %d, want 200 (body %s)", subject, rec.Code, rec.Body.String())
	}
	tok, _ := body["token"].(string)
	if tok == "" {
		t.Fatalf("mint token %q returned no token", subject)
	}
	return tok
}

// do issues an in-process request carrying the per-server Owner token. It is the
// drop-in used by every existing test, now with auth on.
func do(t *testing.T, srv *server.Server, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	token := ""
	// /auth/token and /healthz are unauthenticated; everything else needs a token.
	if path != "/auth/token" && path != "/healthz" {
		token = ownerToken(t, srv)
	}
	return doRaw(t, srv, method, path, token, body)
}

// doRaw issues an in-process request, attaching Authorization: Bearer <token>
// when token != "". It returns the recorder and the decoded JSON body.
func doRaw(t *testing.T, srv *server.Server, method, path, token string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	var decoded map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s: decode response %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec, decoded
}

// TestHealthz asserts the health endpoint reports ok against a live DB.
func TestHealthz(t *testing.T) {
	srv := newServer(t)
	rec, body := do(t, srv, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body["status"] != "ok" {
		t.Fatalf("healthz status field = %v, want ok", body["status"])
	}
}

// TestPostSaleEndToEnd creates a company and ledgers, posts a balanced Sales
// voucher through the REST endpoint, and reads the Trial Balance back — the
// full Phase 1 happy path over HTTP.
func TestPostSaleEndToEnd(t *testing.T) {
	srv := newServer(t)

	// Unique company per run so tenant-isolated tables never collide.
	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	base := "/v1/companies/" + companyID.String()

	// 1. Create the company (seeds the 28 groups, Cash + P&L ledgers, Sales VT).
	rec, body := do(t, srv, http.MethodPost, "/v1/companies", map[string]any{
		"id":            companyID.String(),
		"name":          "Smoke Co " + companyID.String()[:8],
		"fy_start_date": "2025-04-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if body["company_id"] != companyID.String() {
		t.Fatalf("create company returned %v, want %s", body["company_id"], companyID)
	}
	// This is the ONE company in this package minted inline rather than through
	// newCompany, so it needs its own registration. Every seeded company drags a
	// 28-node account_group tree behind it; 43,419 of them is how account_group
	// reached 603 MB and how a ledger test ran out of disk mid-seed.
	testdb.Cleanup(t, companyID)

	// 2. Create the debtor and a domestic-sales ledger.
	debtorID := createLedger(t, srv, base, "Acme Corp", "Sundry Debtors")
	salesID := createLedger(t, srv, base, "Domestic Sales", "Sales Accounts")

	// 3. Post a balanced Sales voucher: debtor Dr 1000 / sales Cr 1000.
	rec, body = do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
		"voucher_no":   "S-1",
		"voucher_date": "2025-05-10",
		"narration":    "Smoke sale",
		"party":        "Acme Corp",
		"created_by":   "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "1000.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "1000.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if body["voucher_id"] == nil || body["voucher_id"] == "" {
		t.Fatalf("post sale returned no voucher_id (body %s)", rec.Body.String())
	}
	if vn, ok := body["version_no"].(float64); !ok || vn != 1 {
		t.Fatalf("post sale version_no = %v, want 1", body["version_no"])
	}

	// 4. An unbalanced post must be rejected (422), proving validation is wired.
	rec, _ = do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
		"voucher_no":   "S-2",
		"voucher_date": "2025-05-11",
		"created_by":   "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "1000.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "999.0000"},
		},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unbalanced post status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}

	// 5. Read the Trial Balance back; it must balance and total 1000.
	rec, body = do(t, srv, http.MethodGet, base+"/trial-balance", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("trial balance status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body["balanced"] != true {
		t.Fatalf("trial balance not balanced (body %s)", rec.Body.String())
	}
	if got := body["total_debit"]; got != "1000.0000" {
		t.Fatalf("trial balance total_debit = %v, want 1000.0000", got)
	}
}

// newCompany creates a unique seeded company over HTTP and returns its id and
// the base path. It fails the test on any non-201.
//
// It also registers the teardown. The registration lives HERE rather than at
// the fifteen call sites for two reasons: one edit cannot be forgotten at a new
// call site, and because `t` is the CALLING test's `*testing.T`, a test that
// creates two companies gets two independent cleanups — which is exactly what
// the deliberate second-tenant isolation probes need and what a package-level
// or per-server teardown would silently get wrong.
func newCompany(t *testing.T, srv *server.Server) (string, string) {
	t.Helper()
	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	base := "/v1/companies/" + companyID.String()
	rec, body := do(t, srv, http.MethodPost, "/v1/companies", map[string]any{
		"id":            companyID.String(),
		"name":          "Edit Co " + companyID.String()[:8],
		"fy_start_date": "2025-04-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if body["company_id"] != companyID.String() {
		t.Fatalf("create company returned %v, want %s", body["company_id"], companyID)
	}
	testdb.Cleanup(t, companyID)
	return companyID.String(), base
}

// TestAlterVoucherRestatesTrialBalance proves the 400->650->650 invariant over
// HTTP: an ALTER restates the voucher rather than accumulating versions, so the
// trial balance reflects the NEW amount (650), not the sum (1050).
func TestAlterVoucherRestatesTrialBalance(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)

	debtorID := createLedger(t, srv, base, "Acme Corp", "Sundry Debtors")
	salesID := createLedger(t, srv, base, "Domestic Sales", "Sales Accounts")

	// Post sales 400.
	rec, body := do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
		"voucher_no": "INV-1", "voucher_date": "2025-04-10", "party": "Acme Corp", "created_by": "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "400.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "400.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)
	if voucherID == "" {
		t.Fatalf("post sale returned no voucher_id (body %s)", rec.Body.String())
	}

	// Baseline trial balance: 400.
	rec, body = do(t, srv, http.MethodGet, base+"/trial-balance", nil)
	if rec.Code != http.StatusOK || body["total_debit"] != "400.0000" {
		t.Fatalf("baseline trial balance total_debit = %v, want 400.0000 (body %s)", body["total_debit"], rec.Body.String())
	}

	// Alter to 650, resending the SAME voucher_no/date (numbering preservation).
	rec, body = do(t, srv, http.MethodPut, base+"/vouchers/sales/"+voucherID, map[string]any{
		"voucher_no": "INV-1", "voucher_date": "2025-04-10", "party": "Acme Corp", "created_by": "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "650.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "650.0000"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("alter status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body["voucher_id"] != voucherID {
		t.Fatalf("alter must preserve voucher_id: got %v, want %s", body["voucher_id"], voucherID)
	}
	if vn, ok := body["version_no"].(float64); !ok || vn != 2 {
		t.Fatalf("alter version_no = %v, want 2", body["version_no"])
	}

	// Trial balance must now read 650, NOT 1050.
	rec, body = do(t, srv, http.MethodGet, base+"/trial-balance", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("trial balance status = %d, want 200", rec.Code)
	}
	if body["balanced"] != true {
		t.Fatalf("trial balance not balanced after alter (body %s)", rec.Body.String())
	}
	if got := body["total_debit"]; got != "650.0000" {
		t.Fatalf("trial balance total_debit = %v, want 650.0000 (NOT 1050)", got)
	}
}

// TestDeleteVoucherDropsFromReports proves a DELETE removes the voucher from
// both the trial balance (net to zero) and the default Day Book.
func TestDeleteVoucherDropsFromReports(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)

	debtorID := createLedger(t, srv, base, "Acme Corp", "Sundry Debtors")
	salesID := createLedger(t, srv, base, "Domestic Sales", "Sales Accounts")

	rec, body := do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
		"voucher_no": "INV-1", "voucher_date": "2025-04-10", "party": "Acme Corp", "created_by": "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "400.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "400.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)

	rec, body = do(t, srv, http.MethodDelete, base+"/vouchers/"+voucherID+"?created_by=deleter", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body["op"] != "DELETE" {
		t.Fatalf("delete op = %v, want DELETE", body["op"])
	}
	if body["voucher_id"] != voucherID {
		t.Fatalf("delete must preserve voucher_id: got %v, want %s", body["voucher_id"], voucherID)
	}

	rec, body = do(t, srv, http.MethodGet, base+"/trial-balance", nil)
	if rec.Code != http.StatusOK || body["total_debit"] != "0.0000" {
		t.Fatalf("trial balance total_debit after delete = %v, want 0.0000 (body %s)", body["total_debit"], rec.Body.String())
	}

	rec, body = do(t, srv, http.MethodGet, base+"/day-book", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("day book status = %d, want 200", rec.Code)
	}
	entries, _ := body["entries"].([]any)
	for _, raw := range entries {
		e, _ := raw.(map[string]any)
		if e["voucher_id"] == voucherID {
			t.Fatalf("deleted voucher must be absent from the day book, but it appeared: %v", e)
		}
	}
}

// TestAlterUnknownVoucher404 proves the phantom-create guard on the alter path.
func TestAlterUnknownVoucher404(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)

	debtorID := createLedger(t, srv, base, "Acme Corp", "Sundry Debtors")
	salesID := createLedger(t, srv, base, "Domestic Sales", "Sales Accounts")

	unknown, _ := uuid.NewV7()
	rec, _ := do(t, srv, http.MethodPut, base+"/vouchers/sales/"+unknown.String(), map[string]any{
		"voucher_no": "INV-X", "voucher_date": "2025-04-10", "created_by": "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "100.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "100.0000"},
		},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("alter unknown voucher status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestDeleteUnknownVoucher404 proves the phantom-create guard on the delete path.
func TestDeleteUnknownVoucher404(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)

	unknown, _ := uuid.NewV7()
	rec, _ := do(t, srv, http.MethodDelete, base+"/vouchers/"+unknown.String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown voucher status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestGetVoucherReturnsRealLedgerIDs proves the GET single-voucher endpoint
// returns the CURRENT head version with postings carrying the REAL ledger ids
// (the uuids posted), not ledger names — the fix the alter path depends on. It
// also asserts the header fields echo, then deletes the voucher and confirms a
// subsequent GET is a 404.
func TestGetVoucherReturnsRealLedgerIDs(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)

	debtorID := createLedger(t, srv, base, "Acme Corp", "Sundry Debtors")
	salesID := createLedger(t, srv, base, "Domestic Sales", "Sales Accounts")

	rec, body := do(t, srv, http.MethodPost, base+"/vouchers/sales", map[string]any{
		"voucher_no": "INV/0001", "voucher_date": "2025-05-10", "party": "Acme Corp", "created_by": "smoke",
		"postings": []map[string]any{
			{"ledger_id": debtorID, "dr_cr": "D", "amount": "11800.0000"},
			{"ledger_id": salesID, "dr_cr": "C", "amount": "11800.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)
	if voucherID == "" {
		t.Fatalf("post sale returned no voucher_id (body %s)", rec.Body.String())
	}

	// GET the voucher: header fields echo, op=CREATE, version_no=1.
	rec, body = do(t, srv, http.MethodGet, base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get voucher status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body["voucher_no"] != "INV/0001" {
		t.Fatalf("get voucher voucher_no = %v, want INV/0001", body["voucher_no"])
	}
	if body["voucher_date"] != "2025-05-10" {
		t.Fatalf("get voucher voucher_date = %v, want 2025-05-10", body["voucher_date"])
	}
	if body["op"] != "CREATE" {
		t.Fatalf("get voucher op = %v, want CREATE", body["op"])
	}
	if vn, ok := body["version_no"].(float64); !ok || vn != 1 {
		t.Fatalf("get voucher version_no = %v, want 1", body["version_no"])
	}

	// Postings carry REAL ledger ids (parseable uuids) — assert the set equals
	// {debtorID, salesID} with matching amounts, proving names are NOT returned
	// where uuids belong.
	postings, _ := body["postings"].([]any)
	if len(postings) != 2 {
		t.Fatalf("get voucher postings len = %d, want 2 (body %s)", len(postings), rec.Body.String())
	}
	gotAmounts := map[string]string{}
	for _, raw := range postings {
		p, _ := raw.(map[string]any)
		idStr, _ := p["ledger_id"].(string)
		if _, err := uuid.Parse(idStr); err != nil {
			t.Fatalf("posting ledger_id %q is not a uuid (the name-as-uuid bug): %v", idStr, err)
		}
		amt, _ := p["amount"].(string)
		gotAmounts[idStr] = amt
	}
	if gotAmounts[debtorID] != "11800.0000" {
		t.Fatalf("debtor posting amount = %q, want 11800.0000 (got map %v)", gotAmounts[debtorID], gotAmounts)
	}
	if gotAmounts[salesID] != "11800.0000" {
		t.Fatalf("sales posting amount = %q, want 11800.0000 (got map %v)", gotAmounts[salesID], gotAmounts)
	}

	// DELETE then GET -> 404 (the head join excludes deleted vouchers).
	rec, _ = do(t, srv, http.MethodDelete, base+"/vouchers/"+voucherID+"?created_by=deleter", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	rec, _ = do(t, srv, http.MethodGet, base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted voucher status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestGetUnknownVoucher404 proves a GET for a random (never-created) voucher id
// is a 404.
func TestGetUnknownVoucher404(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)

	unknown, _ := uuid.NewV7()
	rec, _ := do(t, srv, http.MethodGet, base+"/vouchers/"+unknown.String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get unknown voucher status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestGetVoucherReturnsGSTAndSurvivesAlter posts an intra-state 18% GST sale
// over HTTP, GETs it and asserts the derived CGST/SGST (and zero IGST) plus the
// tax-ledger postings carry real uuids, then ALTERs the taxable base and GETs
// again asserting op=ALTER/version_no=2 with the re-derived tax. This is the
// durable coverage for the GET endpoint's GST attributes + the head-advance
// case (the reviewer flagged both as untested).
func TestGetVoucherReturnsGSTAndSurvivesAlter(t *testing.T) {
	srv := newServer(t)
	_, base := newCompany(t, srv)

	debtorID := createLedger(t, srv, base, "Acme Corp", "Sundry Debtors")
	salesID := createLedger(t, srv, base, "Domestic Sales", "Sales Accounts")
	cgstID := createLedger(t, srv, base, "Output CGST", "Duties & Taxes")
	sgstID := createLedger(t, srv, base, "Output SGST", "Duties & Taxes")
	igstID := createLedger(t, srv, base, "Output IGST", "Duties & Taxes")

	// Intra-state 18% on taxable 10000 -> CGST 900, SGST 900, invoice 11800.
	salePayload := func(taxable, half, total string) map[string]any {
		return map[string]any{
			"voucher_no": "INV/0001", "voucher_date": "2025-05-10", "party": "Acme Corp", "created_by": "smoke",
			"postings": []map[string]any{
				{"ledger_id": debtorID, "dr_cr": "D", "amount": total},
				{"ledger_id": salesID, "dr_cr": "C", "amount": taxable},
				{"ledger_id": cgstID, "dr_cr": "C", "amount": half},
				{"ledger_id": sgstID, "dr_cr": "C", "amount": half},
			},
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"lines":                      []map[string]any{{"taxable_value": taxable, "gst_rate_pct": "18"}},
				"cgst_ledger_id":             cgstID,
				"sgst_ledger_id":             sgstID,
				"igst_ledger_id":             igstID,
			},
		}
	}

	rec, body := do(t, srv, http.MethodPost, base+"/vouchers/sales", salePayload("10000.0000", "900.0000", "11800.0000"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("post GST sale status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)

	rec, body = do(t, srv, http.MethodGet, base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get GST voucher status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body["taxable_value"] != "10000.0000" || body["cgst"] != "900.0000" || body["sgst"] != "900.0000" || body["igst"] != "0.0000" {
		t.Fatalf("get GST voucher tax = taxable %v cgst %v sgst %v igst %v, want 10000/900/900/0",
			body["taxable_value"], body["cgst"], body["sgst"], body["igst"])
	}
	// Tax-ledger postings must carry real uuids (the name-as-uuid bug guard).
	for _, raw := range body["postings"].([]any) {
		p, _ := raw.(map[string]any)
		idStr, _ := p["ledger_id"].(string)
		if _, err := uuid.Parse(idStr); err != nil {
			t.Fatalf("GST posting ledger_id %q is not a uuid: %v", idStr, err)
		}
	}

	// ALTER the taxable base to 20000 (CGST 1800, SGST 1800, invoice 23600),
	// resending INV/0001 so numbering is preserved. GET must show version_no=2,
	// op=ALTER, and the re-derived tax.
	rec, _ = do(t, srv, http.MethodPut, base+"/vouchers/sales/"+voucherID, salePayload("20000.0000", "1800.0000", "23600.0000"))
	if rec.Code != http.StatusOK {
		t.Fatalf("alter GST sale status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	rec, body = do(t, srv, http.MethodGet, base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get altered voucher status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body["op"] != "ALTER" {
		t.Fatalf("get altered voucher op = %v, want ALTER", body["op"])
	}
	if vn, ok := body["version_no"].(float64); !ok || vn != 2 {
		t.Fatalf("get altered voucher version_no = %v, want 2", body["version_no"])
	}
	if body["voucher_no"] != "INV/0001" {
		t.Fatalf("alter must preserve number: voucher_no = %v, want INV/0001", body["voucher_no"])
	}
	if body["taxable_value"] != "20000.0000" || body["cgst"] != "1800.0000" || body["sgst"] != "1800.0000" {
		t.Fatalf("re-derived tax = taxable %v cgst %v sgst %v, want 20000/1800/1800",
			body["taxable_value"], body["cgst"], body["sgst"])
	}
}

// createLedger POSTs a ledger and returns its id, failing the test on a non-201.
func createLedger(t *testing.T, srv *server.Server, base, name, group string) string {
	t.Helper()
	rec, body := do(t, srv, http.MethodPost, base+"/ledgers", map[string]any{
		"name":       name,
		"group_name": group,
		"created_by": "smoke",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create ledger %q status = %d, want 201 (body %s)", name, rec.Code, rec.Body.String())
	}
	id, _ := body["ledger_id"].(string)
	if id == "" {
		t.Fatalf("create ledger %q returned no id (body %s)", name, rec.Body.String())
	}
	return id
}
