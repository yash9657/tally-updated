package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/intentlog"
	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/policy"
)

// agent_http.go is the HTTP surface of the agent plane (W2, D121): start and
// read runs, stream a run's events to the side panel, and the approval inbox —
// list, approve, edit-then-approve, reject, ask-back. The contract the W2
// frontend builds on is written out in docs/design/d121-w2-agent-plane.md
// under "Frontend contract"; change one and change the other.
//
// Every route is inside /v1/companies/{companyID}, so companyAuthMiddleware
// has run: the caller is an authenticated member whose ACL allowed the route's
// (action, resource) — `agent-run` or `agent-approval`, both bare tokens that
// every seeded non-Owner level is refused by default.

type stepUpKey struct{}

func withStepUp(ctx context.Context, v bool) context.Context {
	return context.WithValue(ctx, stepUpKey{}, v)
}

func stepUpFrom(ctx context.Context) bool {
	v, _ := ctx.Value(stepUpKey{}).(bool)
	return v
}

// AgentOptions configures the agent engine. Exported so a test can hand the
// engine a cassette-backed model and a driven clock; `cored serve` uses the
// defaults New() builds.
type AgentOptions struct {
	LLM            *llm.Client
	Now            func() time.Time
	ApprovalWindow time.Duration
	ParkedPoll     time.Duration
}

// ConfigureAgent replaces the agent engine's model and clock. The dispatcher
// and the pool are always the server's own — there is no option to point the
// agent at a different write path.
func (s *Server) ConfigureAgent(o AgentOptions) {
	s.agent.LLM = o.LLM
	if o.Now != nil {
		s.agent.Now = o.Now
	}
	if o.ApprovalWindow > 0 {
		s.agent.ApprovalWindow = o.ApprovalWindow
	}
	if o.ParkedPoll > 0 {
		s.agent.ParkedPoll = o.ParkedPoll
	}
}

// AgentPollOnce drives the agent poller once (tests, operators).
func (s *Server) AgentPollOnce(ctx context.Context) (int, error) { return s.agentPoller.RunOnce(ctx) }

// AgentAnchorOnce writes intent-log anchors once (tests, operators).
func (s *Server) AgentAnchorOnce(ctx context.Context) (int, error) {
	return s.agentPoller.AnchorOnce(ctx)
}

// RunAgentPoller polls until ctx ends: every `every`, immediately when a run
// is started or a decision is recorded, and anchors the intent log daily.
// Started by `cored serve`; never by tests, which drive AgentPollOnce.
func (s *Server) RunAgentPoller(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := s.agentPoller.AnchorOnce(ctx); err != nil {
					log.Printf("agent: anchor: %v", err)
				}
			}
		}
	}()
	s.agentPoller.Run(ctx, every, s.agentKick)
}

func (s *Server) kickAgent() {
	select {
	case s.agentKick <- struct{}{}:
	default: // a kick is already pending; one is enough
	}
}

func (s *Server) agentRoutes(r chi.Router) {
	r.Post("/agent/runs", s.handleAgentStartRun)
	r.Get("/agent/runs", s.handleAgentListRuns)
	r.Get("/agent/runs/{runID}", s.handleAgentGetRun)
	r.Get("/agent/runs/{runID}/events", s.handleAgentRunEvents)
	r.Post("/agent/runs/{runID}/cancel", s.handleAgentCancelRun)
	r.Get("/agent/approvals", s.handleAgentListApprovals)
	r.Get("/agent/approvals/{approvalID}", s.handleAgentGetApproval)
	r.Post("/agent/approvals/{approvalID}/decision", s.handleAgentDecide)
}

func (s *Server) agentScope(w http.ResponseWriter, r *http.Request) (context.Context, uuid.UUID, uuid.UUID, bool) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return nil, uuid.Nil, uuid.Nil, false
	}
	userID, ok := db.UserFromContext(ctx)
	if !ok {
		writeError(w, http.StatusForbidden, "agent routes need an authenticated user")
		return nil, uuid.Nil, uuid.Nil, false
	}
	return ctx, companyID, userID, true
}

func writeAgentErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agent.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, agent.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, agent.ErrNoModel):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "no agent model is configured on this server (set LLM_PROVIDER and friends)",
			"code":  "AGENT_UNCONFIGURED"})
	default:
		writeError(w, http.StatusInternalServerError, "agent: internal error")
		log.Printf("agent: %v", err)
	}
}

// ---------------------------------------------------------------------------
// runs
// ---------------------------------------------------------------------------

type agentStartReq struct {
	Instruction    string        `json:"instruction"`
	Channel        string        `json:"channel"`
	Inputs         []agent.Input `json:"inputs"`
	Delegate       []string      `json:"delegate"`
	MaxAmountPaise *int64        `json:"max_amount_paise"`
}

// runView is a run as the wire shows it.
type runView struct {
	agent.Run
	Approvals []agent.Approval `json:"approvals,omitempty"`
}

func (s *Server) handleAgentStartRun(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, userID, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	subject, _ := db.SubjectFromContext(ctx)
	var req agentStartReq
	if !decodeJSON(w, r, &req) {
		return
	}
	run, err := s.agent.Start(ctx, agent.StartRequest{
		CompanyID: companyID, UserID: userID, Subject: subject, Channel: agent.Channel(req.Channel),
		Instruction: req.Instruction, Inputs: req.Inputs, DelegatedKeys: req.Delegate, MaxAmountPaise: req.MaxAmountPaise, StepUp: stepUpFrom(ctx),
	})
	if err != nil {
		writeAgentErr(w, err)
		return
	}
	s.kickAgent()
	writeJSON(w, http.StatusAccepted, map[string]any{"run": run})
}

func (s *Server) handleAgentListRuns(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, _, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.agent.ListRuns(ctx, companyID, limit)
	if err != nil {
		writeAgentErr(w, err)
		return
	}
	if runs == nil {
		runs = []agent.Run{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleAgentGetRun(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, _, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	runID, ok := pathUUID(w, r, "runID", "run id")
	if !ok {
		return
	}
	run, err := s.agent.GetRun(ctx, companyID, runID)
	if err != nil {
		writeAgentErr(w, err)
		return
	}
	approvals, err := s.agent.ListApprovals(ctx, companyID, "all", runID)
	if err != nil {
		writeAgentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": runView{Run: run, Approvals: approvals}})
}

func (s *Server) handleAgentCancelRun(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, userID, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	runID, ok := pathUUID(w, r, "runID", "run id")
	if !ok {
		return
	}
	run, err := s.agent.Cancel(ctx, companyID, runID, userID)
	if err != nil {
		writeAgentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run})
}

// aguiType maps an intent kind onto the AG-UI event vocabulary the side panel
// speaks. The intent kind travels too, so nothing is lost in the mapping.
func aguiType(k intentlog.Kind) (string, string) {
	switch k {
	case intentlog.KindRunStarted:
		return "RUN_STARTED", ""
	case intentlog.KindRunFinished:
		return "RUN_FINISHED", ""
	case intentlog.KindRunFailed:
		return "RUN_ERROR", ""
	case intentlog.KindDecisionStep:
		return "STEP_FINISHED", "decide"
	case intentlog.KindToolCall, intentlog.KindPreview, intentlog.KindVerifierError:
		return "TOOL_CALL_RESULT", ""
	default:
		return "CUSTOM", string(k)
	}
}

type agentEvent struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"`
	Name       string          `json:"name,omitempty"`
	Kind       intentlog.Kind  `json:"kind"`
	At         time.Time       `json:"at"`
	ActorKind  string          `json:"actor_kind"`
	ActorID    string          `json:"actor_id"`
	OnBehalfOf *uuid.UUID      `json:"on_behalf_of,omitempty"`
	ActionID   string          `json:"action_id,omitempty"`
	ACLKey     string          `json:"acl_key,omitempty"`
	Data       json.RawMessage `json:"data"`
	Hash       string          `json:"hash"`
}

func (s *Server) handleAgentRunEvents(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, _, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	runID, ok := pathUUID(w, r, "runID", "run id")
	if !ok {
		return
	}
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	entries, err := s.agent.Events(ctx, companyID, runID, after)
	if err != nil {
		writeAgentErr(w, err)
		return
	}
	run, err := s.agent.GetRun(ctx, companyID, runID)
	if err != nil {
		writeAgentErr(w, err)
		return
	}
	events := make([]agentEvent, 0, len(entries))
	next := after
	for _, e := range entries {
		typ, name := aguiType(e.Kind)
		ev := agentEvent{Seq: e.Seq, Type: typ, Name: name, Kind: e.Kind, At: e.CreatedAt,
			ActorKind: string(e.ActorKind), ActorID: e.ActorID, ActionID: e.ActionID, ACLKey: e.ACLKey,
			Data: e.Payload, Hash: hexEncode(e.Hash)}
		if e.OnBehalfOf != uuid.Nil {
			obo := e.OnBehalfOf
			ev.OnBehalfOf = &obo
		}
		events = append(events, ev)
		next = e.Seq
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "status": run.Status,
		"events": events, "next_after": next, "finished": run.Status.Finished()})
}

// ---------------------------------------------------------------------------
// the approval inbox
// ---------------------------------------------------------------------------

func (s *Server) handleAgentListApprovals(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, _, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	state := r.URL.Query().Get("state")
	if state != "" && state != "pending" && state != "all" {
		writeError(w, http.StatusBadRequest, "state must be pending or all")
		return
	}
	var runID uuid.UUID
	if v := r.URL.Query().Get("run_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid run_id")
			return
		}
		runID = id
	}
	list, err := s.agent.ListApprovals(ctx, companyID, state, runID)
	if err != nil {
		writeAgentErr(w, err)
		return
	}
	if list == nil {
		list = []agent.Approval{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": list})
}

func (s *Server) handleAgentGetApproval(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, _, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "approvalID", "approval id")
	if !ok {
		return
	}
	a, err := s.agent.GetApproval(ctx, companyID, id)
	if err != nil {
		writeAgentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approval": a})
}

type agentDecisionReq struct {
	Decision   string          `json:"decision"`
	EditedBody json.RawMessage `json:"edited_body"`
	Note       string          `json:"note"`
}

// handleAgentDecide records approve / edit-then-approve / reject / ask-back.
func (s *Server) handleAgentDecide(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, userID, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	// ⛔ THE AGENT IS NEVER THE CHECKER. The route is already governance to an
	// agent (policy refuses `alter agent-approval` in the middleware); this is
	// the same rule said a second time where the decision is written.
	var p *policy.Principal
	if call := agentCallFrom(ctx); call != nil {
		p = &call.caller.Principal
	}
	if err := policy.CanDecide(p); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	id, ok := pathUUID(w, r, "approvalID", "approval id")
	if !ok {
		return
	}
	var req agentDecisionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if string(req.EditedBody) == "null" {
		req.EditedBody = nil
	}
	a, err := s.agent.GetApproval(ctx, companyID, id)
	if err != nil {
		writeAgentErr(w, err)
		return
	}
	// ⛔ THE CHECKER MUST HOLD THE AUTHORITY BEING EXERCISED. Approving an
	// agent's `create payment` is exercising `create payment`; a member whose
	// own ACL refuses that key may reject or ask back, never approve.
	if req.Decision == "approve" {
		if reason, allowed := s.userMayTake(ctx, companyID, userID, a.ActionID); !allowed {
			writeError(w, http.StatusForbidden, "you may not approve an action you may not take yourself: "+reason)
			return
		}
	}
	out, err := s.agent.Decide(ctx, companyID, id, agent.DecisionInput{Decision: req.Decision, EditedBody: req.EditedBody,
		Note: req.Note, DecidedBy: userID, WebAuthn: stepUpFrom(ctx)})
	switch {
	case errors.Is(err, agent.ErrAlreadyDecided):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "this approval has already been decided", "code": "ALREADY_DECIDED", "approval": out})
		return
	case errors.Is(err, agent.ErrApprovalExpired):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "the approval window closed before this decision; it has been recorded as expired", "code": "APPROVAL_EXPIRED", "approval": out})
		return
	case err != nil:
		writeAgentErr(w, err)
		return
	}
	s.kickAgent()
	writeJSON(w, http.StatusOK, map[string]any{"approval": out})
}

// userMayTake evaluates the user's OWN level and rules for an action, through
// the real PDP. Dates are not judged here — the commit itself runs through the
// router, where the cutoff and the year-end lock apply to the real body.
func (s *Server) userMayTake(ctx context.Context, companyID, userID uuid.UUID, actionID string) (string, bool) {
	spec, ok := actions.ByID(actionID)
	if !ok {
		return "unknown action", false
	}
	levelID, member, err := s.authStore.Membership(ctx, userID, companyID)
	if err != nil || !member {
		return "not a member", false
	}
	level, err := s.authStore.Level(ctx, companyID, levelID)
	if err != nil {
		return "level unavailable", false
	}
	rules, err := s.authStore.Rules(ctx, companyID, levelID)
	if err != nil {
		return "rules unavailable", false
	}
	d := authz.Evaluate(level, rules, authz.Request{UserID: userID, CompanyID: companyID, Action: spec.Action,
		Resource: spec.Resource, ResourceName: spec.ResourceName, HasWebAuthn: stepUpFrom(ctx)})
	return d.Reason, d.Allow
}

func hexEncode(b []byte) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2], out[i*2+1] = hexd[c>>4], hexd[c&0x0f]
	}
	return string(out)
}
