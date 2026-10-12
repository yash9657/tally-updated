package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hostedStatus issues an in-process request and returns only the status code
// (chi's own 404 is plain text, which doRaw would refuse to decode).
func hostedStatus(t *testing.T, h http.Handler, method, path, auth, body string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// TestDevTokenRouteDisabled: AUTH_DEV_TOKEN_ROUTE_DISABLED=true removes the
// any-subject token mint but keeps password login (docs/DEPLOY_VERCEL.md).
func TestDevTokenRouteDisabled(t *testing.T) {
	t.Setenv("AUTH_DEV_TOKEN_ROUTE_DISABLED", "true")
	srv := newServer(t)
	h := srv.Router()

	if got := hostedStatus(t, h, http.MethodPost, "/auth/token", "", `{"subject":"svc:statutoryd"}`); got != http.StatusNotFound {
		t.Fatalf("POST /auth/token with the route disabled = %d, want 404", got)
	}
	got := hostedStatus(t, h, http.MethodPost, "/auth/login", "", `{"subject":"nobody-`+t.Name()+`","password":"wrong-password"}`)
	if got == http.StatusNotFound || got == http.StatusMethodNotAllowed {
		t.Fatalf("POST /auth/login must stay mounted with only the token route disabled, got %d", got)
	}
}

// TestCronRoutes: unmounted without CRON_SECRET; with it, bearer-gated and a
// pass of every poller succeeds against a migrated database.
func TestCronRoutes(t *testing.T) {
	t.Run("no CRON_SECRET -> not mounted", func(t *testing.T) {
		t.Setenv("CRON_SECRET", "")
		srv := newServer(t)
		if got := hostedStatus(t, srv.Router(), http.MethodGet, "/internal/cron/tick", "Bearer x", ""); got != http.StatusNotFound {
			t.Fatalf("tick without CRON_SECRET = %d, want 404", got)
		}
	})
	t.Run("CRON_SECRET set -> gated and working", func(t *testing.T) {
		t.Setenv("CRON_SECRET", "test-cron-secret")
		srv := newServer(t)
		h := srv.Router()
		for _, path := range []string{"/internal/cron/tick", "/internal/cron/anchor"} {
			if got := hostedStatus(t, h, http.MethodGet, path, "", ""); got != http.StatusUnauthorized {
				t.Fatalf("%s without bearer = %d, want 401", path, got)
			}
			if got := hostedStatus(t, h, http.MethodGet, path, "Bearer wrong", ""); got != http.StatusUnauthorized {
				t.Fatalf("%s with wrong bearer = %d, want 401", path, got)
			}
			if got := hostedStatus(t, h, http.MethodGet, path, "Bearer test-cron-secret", ""); got != http.StatusOK {
				t.Fatalf("%s with the secret = %d, want 200", path, got)
			}
		}
	})
}
