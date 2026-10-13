package server

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
)

// CRON ROUTES — the background pollers, driven by a scheduler instead of a
// long-lived process.
//
// `cored serve` starts three goroutines (notification outbox, agent poller,
// insight poller). On a scale-to-zero host such as a Vercel container function
// those goroutines only run while an instance happens to be warm, so a parked
// agent run, a queued voucher event or the 02:00 IST nightly pass can wait
// indefinitely when nobody is using the app. These routes run ONE pass of each
// job, so an external scheduler (Vercel Cron) can guarantee progress. Every
// job is already safe to run concurrently with the goroutines and with other
// replicas (advisory locks / SKIP LOCKED / UNIQUE keys — see cmd/cored), so a
// cron pass that overlaps a warm instance's tick is harmless.
//
// ⛔ MOUNTED ONLY WHEN CRON_SECRET IS SET, and every call must present
// `Authorization: Bearer <CRON_SECRET>` (what Vercel Cron sends when the
// project has a CRON_SECRET env var). No secret configured means no route:
// a 404, never an open trigger.

// cronRoutes mounts the /internal/cron/* routes when a secret is configured.
func (s *Server) cronRoutes() {
	if s.cronSecret == "" {
		return
	}
	s.router.Get("/internal/cron/tick", s.handleCronTick)
	s.router.Get("/internal/cron/anchor", s.handleCronAnchor)
}

// cronAuthorized reports whether r carries `Authorization: Bearer <secret>`.
// Constant-time on the secret; an empty secret never authorizes.
func cronAuthorized(r *http.Request, secret string) bool {
	if secret == "" {
		return false
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
}

// handleCronTick runs one pass of the notification outbox, the agent poller
// and the insight poller (real-time events + the nightly pass, which itself
// no-ops before 02:00 IST and once per company per business day).
func (s *Server) handleCronTick(w http.ResponseWriter, r *http.Request) {
	if !cronAuthorized(r, s.cronSecret) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx := r.Context()
	out := map[string]any{}
	failed := false
	note := func(job string, n int, err error) {
		if err != nil {
			failed = true
			log.Printf("cron: %s: %v", job, err)
			out[job] = map[string]any{"error": err.Error()}
			return
		}
		out[job] = n
	}

	n, err := s.DispatchNotifications(ctx)
	note("notifications", n, err)
	n, err = s.AgentPollOnce(ctx)
	note("agent_runs", n, err)
	p := s.InsightPoller()
	n, err = p.EventsOnce(ctx)
	note("insight_events", n, err)
	n, err = p.NightlyOnce(ctx)
	note("insight_nightly", n, err)

	status := http.StatusOK
	if failed {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, out)
}

// handleCronAnchor writes the daily intent-log anchors (what RunAgentPoller's
// 24h ticker does on a long-lived process).
func (s *Server) handleCronAnchor(w http.ResponseWriter, r *http.Request) {
	if !cronAuthorized(r, s.cronSecret) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	n, err := s.AgentAnchorOnce(r.Context())
	if err != nil {
		log.Printf("cron: anchor: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"anchors": map[string]any{"error": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"anchors": n})
}
