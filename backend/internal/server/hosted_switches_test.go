package server_test

import (
	"context"
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

// TestCronRoutes: unmounted without CRON_SECRET; with it, bearer-gated.
//
// ⛔ THE AUTHORIZED CALL RUNS ON A CANCELLED CONTEXT, ON PURPOSE. A real tick
// runs the insight nightly pass and the agent poller over EVERY company in the
// shared test database, which rewrites other packages' in-flight fixtures
// (measured: internal/tierb's leak oracle went red when a real tick ran
// alongside it after 02:00 IST). With the context already cancelled every job
// fails at its first query, so the handler answers 500 — which proves the
// bearer was accepted (not 401) and the route is mounted (not 404) without
// touching a row. The passes themselves are exercised by the container smoke
// test in docs/DEPLOY_VERCEL.md §9 and by each poller's own tests.
func TestCronRoutes(t *testing.T) {
	t.Run("no CRON_SECRET -> not mounted", func(t *testing.T) {
		t.Setenv("CRON_SECRET", "")
		srv := newServer(t)
		if got := hostedStatus(t, srv.Router(), http.MethodGet, "/internal/cron/tick", "Bearer x", ""); got != http.StatusNotFound {
			t.Fatalf("tick without CRON_SECRET = %d, want 404", got)
		}
	})
	t.Run("CRON_SECRET set -> bearer-gated", func(t *testing.T) {
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
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer test-cron-secret")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("%s with the secret on a cancelled context = %d, want 500 (auth passed, jobs refused to run): %s",
					path, rec.Code, rec.Body.String())
			}
		}
	})
}
