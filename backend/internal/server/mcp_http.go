package server

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/mcp"
	"github.com/khata/cored/internal/platform/db"
)

// mcp_http.go mounts the MCP server (W6, D126) inside /v1/companies/{companyID},
// so companyAuthMiddleware has run on every request: the caller is an
// authenticated member of the company in the path, and their level allowed
// `agent-mcp` (a bare token every seeded non-Owner level is refused).
//
//	POST   /agent/mcp/grants                 create agent-mcp   a human makes a grant
//	GET    /agent/mcp/grants                 display agent-mcp  list them
//	POST   /agent/mcp/grants/{grantID}/revoke alter agent-mcp   revoke: closes sessions, cancels runs
//	POST   /agent/mcp/{grantID}              display agent-mcp  the transport (JSON-RPC)
//	DELETE /agent/mcp/{grantID}              display agent-mcp  end a session
//
// The transport URL an MCP client is configured with is therefore
// https://<host>/v1/companies/<company>/agent/mcp/<grant>, with the grant's
// TRANSPORT TOKEN as its bearer (0106) — returned once when the grant is made,
// accepted on that URL and nowhere else. ⛔ Not the human's JWT: the transport
// refuses it, because a client holding the person's token could decide its
// own approvals (the wave-2 review).

func (s *Server) mcpRoutes(r chi.Router) {
	r.Post("/agent/mcp/grants", s.handleMCPCreateGrant)
	r.Get("/agent/mcp/grants", s.handleMCPListGrants)
	r.Post("/agent/mcp/grants/{grantID}/revoke", s.handleMCPRevokeGrant)
	r.Post("/agent/mcp/{grantID}", s.handleMCPTransport)
	r.Delete("/agent/mcp/{grantID}", s.handleMCPTransport)
}

// mcpAllowedOrigins reads MCP_ALLOWED_ORIGINS (comma-separated). Unset is the
// safe default: any request carrying an Origin is refused.
func mcpAllowedOrigins() map[string]bool {
	out := map[string]bool{}
	for _, o := range strings.Split(os.Getenv("MCP_ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			out[o] = true
		}
	}
	return out
}

// MCPOptions are test seams on the MCP server. There is deliberately no seam
// for its engine or pool: the MCP server drives the agent plane's own.
type MCPOptions struct {
	// Specs replaces the registry the manifest is generated from (the
	// manifest-change test). Nil restores actions.Specs.
	Specs func() []actions.ActionSpec
}

// ConfigureMCP applies test seams.
func (s *Server) ConfigureMCP(o MCPOptions) { s.mcp.Specs = o.Specs }

func writeMCPErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mcp.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, mcp.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		log.Printf("mcp: %v", err)
		writeError(w, http.StatusInternalServerError, "mcp: internal error")
	}
}

type mcpGrantReq struct {
	ClientName     string     `json:"client_name"`
	Delegate       []string   `json:"delegate"`
	MaxAmountPaise *int64     `json:"max_amount_paise"`
	ExpiresAt      *time.Time `json:"expires_at"`
}

func (s *Server) mcpPath(companyID, grantID uuid.UUID) string {
	return "/v1/companies/" + companyID.String() + "/agent/mcp/" + grantID.String()
}

func (s *Server) handleMCPCreateGrant(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, userID, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	var req mcpGrantReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ExpiresAt == nil {
		writeError(w, http.StatusUnprocessableEntity, "expires_at is required: a standing grant must say when it ends")
		return
	}
	sub, _ := db.SubjectFromContext(ctx)
	g, err := s.mcp.CreateGrant(ctx, mcp.NewGrant{CompanyID: companyID, UserID: userID, ClientName: req.ClientName,
		ACLKeys: req.Delegate, MaxAmountPaise: req.MaxAmountPaise, ExpiresAt: *req.ExpiresAt,
		// The grant carries the step-up of the request that MADE it, as a
		// run's delegation carries its starter's (policy.Delegation.StepUp).
		StepUp: stepUpFrom(ctx), Subject: sub})
	if err != nil {
		writeMCPErr(w, err)
		return
	}
	// ⛔ THE TRANSPORT TOKEN IS SHOWN HERE ONCE, and is what the MCP client is
	// configured with — never the person's own sign-in token, which the
	// transport refuses (0106).
	writeJSON(w, http.StatusCreated, map[string]any{"grant": g, "mcp_path": s.mcpPath(companyID, g.ID),
		"transport_token": g.TransportToken})
}

func (s *Server) handleMCPListGrants(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, _, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	gs, err := s.mcp.ListGrants(ctx, companyID)
	if err != nil {
		writeMCPErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": gs})
}

func (s *Server) handleMCPRevokeGrant(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, userID, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	grantID, ok := pathUUID(w, r, "grantID", "grant id")
	if !ok {
		return
	}
	g, err := s.mcp.RevokeGrant(ctx, companyID, grantID, userID)
	if err != nil {
		writeMCPErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grant": g})
}

func (s *Server) handleMCPTransport(w http.ResponseWriter, r *http.Request) {
	ctx, _, _, ok := s.agentScope(w, r)
	if !ok {
		return
	}
	grantID, err := uuid.Parse(chi.URLParam(r, "grantID"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no such MCP grant")
		return
	}
	s.mcp.Serve(w, r.WithContext(ctx), grantID)
}
