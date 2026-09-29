package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/insights"
	"github.com/khata/cored/internal/platform/db"
)

// insights_http.go — W4's routes (d125): the Insights inbox, drill-down to
// the evidence, feedback, suppressions, the morning digest, and an operator's
// "run tonight's detectors now".
//
// ONE BARE TOKEN, `insights` (authz/vocabulary.go). Reads are Display; a
// verdict, a digest composition and a run are Create; suppressions are Alter.
// It is default-DENIED for every seeded non-Owner level on the day it ships
// (the 0091 row: Data Entry's default_access is 'none'), so no ACL migration.
//
// ⛔ AND IT IS GOVERNANCE TO AN AGENT, read and write (actions/risk.go): the
// inbox is the owner's view of the business, the digest is model-written
// text, and an agent giving verdicts on insights would be training its own
// ranker. Exposing insight tools to the agent is W2-breadth's decision (d125
// §OWED), not something this lane may do by classifying the route "read".

func (s *Server) insightRoutes(r chi.Router) {
	r.Get("/insights", s.handleInsightsList)
	r.Get("/insights/digest", s.handleInsightsGetDigest)
	r.Post("/insights/digest", s.handleInsightsComposeDigest)
	r.Get("/insights/suppressions", s.handleInsightsListSuppressions)
	r.Post("/insights/suppressions", s.handleInsightsSuppress)
	r.Delete("/insights/suppressions/{suppressionID}", s.handleInsightsUnsuppress)
	r.Post("/insights/run", s.handleInsightsRunNow)
	r.Get("/insights/{insightID}", s.handleInsightsGet)
	r.Get("/insights/{insightID}/evidence", s.handleInsightsEvidence)
	r.Post("/insights/{insightID}/feedback", s.handleInsightsFeedback)
}

// insightActor is who a verdict or a suppression is attributed to.
func insightActor(ctx context.Context) string {
	if s, ok := db.SubjectFromContext(ctx); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if u, ok := db.UserFromContext(ctx); ok {
		return u.String()
	}
	return "unauthenticated"
}

func writeInsightsErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, insights.ErrNotFound), errors.Is(err, insights.ErrNoDigest):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, insights.ErrBadVerdict):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, insights.ErrNightlyNotRun):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "NIGHTLY_NOT_RUN",
			"error": "that day's nightly run has not happened yet; run it now (R) or wait for the 02:00 IST run, and the digest follows"})
	default:
		writeError(w, http.StatusInternalServerError, "insights: internal error")
		log.Printf("insights: %v", err)
	}
}

// insightsListView carries the registry beside the rows, so the screen can say
// which types this build detects and which it does NOT (the Owed list) from
// the server's own record rather than a copy.
type insightsListView struct {
	Insights []insights.Insight `json:"insights"`
	Types    []typeView         `json:"types"`
	Owed     []owedView         `json:"owed"`
}

type typeView struct {
	Code    string `json:"code"`
	Title   string `json:"title"`
	Cadence string `json:"cadence"`
}

type owedView struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

func (s *Server) handleInsightsList(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	switch state {
	case "", "open", "dismissed", "superseded", "expired":
	default:
		writeError(w, http.StatusBadRequest, "state must be open, dismissed, superseded or expired")
		return
	}
	list, err := insights.List(ctx, s.pool, insights.ListFilter{State: state, Type: q.Get("type")})
	if err != nil {
		writeInsightsErr(w, err)
		return
	}
	v := insightsListView{Insights: list}
	if v.Insights == nil {
		v.Insights = []insights.Insight{}
	}
	for _, td := range insights.Types {
		v.Types = append(v.Types, typeView{td.Code, td.Title, string(td.Cadence)})
	}
	for _, o := range insights.Owed {
		v.Owed = append(v.Owed, owedView{o.Type, o.Reason})
	}
	writeJSON(w, http.StatusOK, v)
}

func insightID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "insightID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid insight id")
		return uuid.Nil, false
	}
	return id, true
}

func (s *Server) handleInsightsGet(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	id, ok := insightID(w, r)
	if !ok {
		return
	}
	in, err := insights.Get(ctx, s.pool, id)
	if err != nil {
		writeInsightsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

// handleInsightsEvidence re-runs the stored evidence NOW, read-only, and says
// whether the books still reproduce the insight. "stale" is not "wrong": a
// voucher posted since detection moves a balance, and the honest answer is
// that the figure described the books as they were.
func (s *Server) handleInsightsEvidence(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	id, ok := insightID(w, r)
	if !ok {
		return
	}
	err := insights.VerifyStored(ctx, s.pool, id)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"reproduces": true})
	case errors.Is(err, insights.ErrEvidenceMismatch):
		writeJSON(w, http.StatusOK, map[string]any{"reproduces": false, "detail": err.Error()})
	default:
		writeInsightsErr(w, err)
	}
}

type insightFeedbackReq struct {
	Verdict string `json:"verdict"`
	Note    string `json:"note"`
}

func (s *Server) handleInsightsFeedback(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	id, ok := insightID(w, r)
	if !ok {
		return
	}
	var req insightFeedbackReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := insights.Feedback(ctx, s.pool, companyID, id, req.Verdict, req.Note, insightActor(ctx)); err != nil {
		writeInsightsErr(w, err)
		return
	}
	in, err := insights.Get(ctx, s.pool, id)
	if err != nil {
		writeInsightsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleInsightsListSuppressions(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	list, err := insights.Suppressions(ctx, s.pool, time.Now())
	if err != nil {
		writeInsightsErr(w, err)
		return
	}
	if list == nil {
		list = []insights.Suppression{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"suppressions": list})
}

type insightSuppressReq struct {
	Type      string `json:"type"`
	EntityKey string `json:"entity_key"`
	Days      int    `json:"days"` // 0 = until lifted
	Reason    string `json:"reason"`
}

func (s *Server) handleInsightsSuppress(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	var req insightSuppressReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, known := insights.ByCode(req.Type); !known || req.Days < 0 || strings.TrimSpace(req.Reason) == "" {
		writeError(w, http.StatusBadRequest, "suppress needs a known type, days >= 0 and a reason")
		return
	}
	var until *time.Time
	if req.Days > 0 {
		u := time.Now().Add(time.Duration(req.Days) * 24 * time.Hour)
		until = &u
	}
	id, err := insights.Suppress(ctx, s.pool, companyID, req.Type, req.EntityKey, until, req.Reason, insightActor(ctx))
	if err != nil {
		writeInsightsErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"suppression_id": id.String()})
}

func (s *Server) handleInsightsUnsuppress(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "suppressionID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid suppression id")
		return
	}
	if err := insights.Unsuppress(ctx, s.pool, id); err != nil {
		writeInsightsErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func digestDate(r *http.Request) (time.Time, bool) {
	if d := r.URL.Query().Get("date"); d != "" {
		t, err := time.Parse("2006-01-02", d)
		return t, err == nil
	}
	return insights.BusinessDate(time.Now()), true
}

func (s *Server) handleInsightsGetDigest(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	on, ok := digestDate(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	d, err := insights.GetDigest(ctx, s.pool, on.Format("2006-01-02"), "")
	if err != nil {
		writeInsightsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// handleInsightsComposeDigest composes (or returns the already-stored) digest
// for the date. Idempotent: the (company, date, recipient) UNIQUE keeps the
// first.
func (s *Server) handleInsightsComposeDigest(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	on, ok := digestDate(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	d, err := insights.ComposeDigest(ctx, s.pool, companyID, on, "", s.insightNarrator)
	if err != nil {
		writeInsightsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// handleInsightsRunNow drains this company's real-time queue and runs
// tonight's detectors for today's business date (once: a second call the same
// day reports already_ran).
func (s *Server) handleInsightsRunNow(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	now := time.Now()
	ev, err := insights.ProcessEvents(ctx, s.pool, companyID, now, 500)
	if err != nil {
		writeInsightsErr(w, err)
		return
	}
	st, err := insights.RunNightly(ctx, s.pool, companyID, insights.BusinessDate(now))
	if err != nil {
		writeInsightsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": ev, "nightly": st})
}

// RunInsightPoller runs the real-time queue and the nightly scheduler until
// ctx ends (cored serve starts it).
func (s *Server) RunInsightPoller(ctx context.Context, every time.Duration) {
	p := &insights.Poller{Pool: s.pool, Narrator: s.insightNarrator}
	p.Run(ctx, every, nil)
}

// InsightPoller exposes the poller for tests and operators.
func (s *Server) InsightPoller() *insights.Poller {
	return &insights.Poller{Pool: s.pool, Narrator: s.insightNarrator}
}
