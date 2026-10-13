package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/policy"
)

// ProbeAgentRouterGate sends ONE agent-call request straight into the router,
// SKIPPING agentDispatcher's own policy pre-check, so a test can prove the
// router's gate (companyAuthMiddleware -> agentGate) refuses by itself.
// Compiled into test binaries only (export_*_test.go).
func ProbeAgentRouterGate(ctx context.Context, s *Server, companyID, subject, actionID string, params map[string]string) (int, string) {
	spec, ok := actions.ByID(actionID)
	if !ok {
		return 0, "no such action"
	}
	cid := uuid.MustParse(companyID)
	path := strings.Replace(spec.Route, "{companyID}", companyID, 1)
	for k, v := range params {
		path = strings.Replace(path, "{"+k+"}", v, 1)
	}
	call := &agentCall{
		caller: agent.Caller{
			Principal: policy.Principal{AgentID: policy.AgentID, OnBehalfOf: uuid.New(), CompanyID: cid, RunID: uuid.New(),
				// Everything delegated, unexpired: only the structural rules
				// and ACL(agent) can refuse.
				Delegation: policy.Delegation{ACLKeys: []string{spec.ACLKey}, ExpiresAt: time.Now().Add(time.Hour)}},
			Subject: subject, IssuedAt: time.Now(), WebAuthn: true,
		},
		spec: spec,
		at:   time.Now(),
	}
	r := httptest.NewRequest(spec.Method, path, strings.NewReader("{}")).WithContext(withAgentCall(ctx, call))
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

var _ = http.MethodGet

// AgentDispatch exposes the server's agent dispatcher to tests, so the commit
// idempotency can be driven directly (a crash between the voucher commit and
// the durable step's record is otherwise hard to reproduce on demand).
func AgentDispatch(ctx context.Context, s *Server, c agent.Caller, req agent.Request) (agent.Result, error) {
	return s.agent.Dispatch.Dispatch(ctx, c, req)
}

// AgentCaller builds the Caller a run would dispatch as.
func AgentCaller(companyID, runID, userID uuid.UUID, subject string, keys []string) agent.Caller {
	return agent.Caller{
		Principal: policy.Principal{AgentID: policy.AgentID, OnBehalfOf: userID, CompanyID: companyID, RunID: runID,
			Delegation: policy.Delegation{ACLKeys: keys, ExpiresAt: time.Now().Add(time.Hour)}},
		Subject: subject, IssuedAt: time.Now(), WebAuthn: true,
	}
}
