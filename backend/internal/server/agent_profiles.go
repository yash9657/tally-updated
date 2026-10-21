package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/policy"
	"github.com/khata/cored/internal/profile"
)

// agent_profiles.go is the server half of the agent's five profile tools
// (agent/profiles.go): W3's internal/profile functions, run for the run's
// human, with THAT HUMAN'S ACL applied to every fact.
//
// ⛔ WHY NOT THROUGH THE ROUTER. Every other agent read is an in-process
// request through the router, so the PDP judges it. The profile tools have no
// route yet (W3 owes them, under the bare token `agent-profile`), and a route
// minted here just for the agent would be a door no person uses — the one
// thing policy.Structural exists to refuse. So this reader does the PDP's job
// itself, with the PDP: membership, an active user, the session not revoked,
// and authz.Evaluate over the human's own level and rules.
//
// ⛔ THE ACL IS PER FACT, NOT PER TOOL. A customer profile carries the
// receivable (reconciles to Outstandings) and the credit sales in the window
// (reconciles to the Day Book). A level may allow one and refuse the other, so
// each fact is shown only if its reconciling report is one the human could
// open, and the tool as a whole requires its PRIMARY report (the one its
// facts without a reconciling report summarise). A fact the human could not
// read in a report, the agent does not read either.

type agentProfiles struct{ s *Server }

var _ agent.ProfileReader = agentProfiles{}

// profilePrimaryReport is the report each tool as a whole summarises.
var profilePrimaryReport = map[string]string{
	agent.ToolCustomerProfile: "outstandings",
	agent.ToolSupplierProfile: "bills-payable",
	agent.ToolItemProfile:     "stock-summary",
	agent.ToolRecurring:       "day-book",
	agent.ToolTenantProfile:   "profit-and-loss",
}

func profileJSON(status int, v any) agent.ProfileResult {
	b, _ := json.Marshal(v)
	return agent.ProfileResult{Status: status, Body: b, Source: policy.SourceBooks}
}

func (p agentProfiles) ReadProfile(ctx context.Context, c agent.Caller, tool string, args agent.ProfileArgs) (agent.ProfileResult, error) {
	primary, ok := profilePrimaryReport[tool]
	if !ok && tool != agent.ToolBusinessInsights {
		return profileJSON(http.StatusUnprocessableEntity, map[string]string{"error": "not a profile tool"}), nil
	}
	s := p.s
	companyID, userID := c.Principal.CompanyID, c.Principal.OnBehalfOf

	// The same identity checks the router makes for an in-process request.
	uid, active, found, err := s.authStore.LookupUserBySubject(ctx, c.Subject)
	if err != nil {
		return agent.ProfileResult{}, err
	}
	if !found || !active || uid != userID {
		return profileJSON(http.StatusForbidden, map[string]string{"error": "the run's user is not active"}), nil
	}
	claims := (&agentCall{caller: c}).claims()
	if revoked, err := s.sessionRevoked(ctx, claims); err != nil {
		return agent.ProfileResult{}, err
	} else if revoked {
		return profileJSON(http.StatusUnauthorized, map[string]string{"error": "the run's session was revoked"}), nil
	}
	levelID, member, err := s.authStore.Membership(ctx, userID, companyID)
	if err != nil {
		return agent.ProfileResult{}, err
	}
	if !member {
		return profileJSON(http.StatusForbidden, map[string]string{"error": "not a member of this company"}), nil
	}
	level, err := s.authStore.Level(ctx, companyID, levelID)
	if err != nil {
		return agent.ProfileResult{}, err
	}
	rules, err := s.authStore.Rules(ctx, companyID, levelID)
	if err != nil {
		return agent.ProfileResult{}, err
	}
	allowed := map[string]bool{}
	may := func(report string) bool {
		if v, ok := allowed[report]; ok {
			return v
		}
		d := authz.Evaluate(level, rules, authz.Request{UserID: userID, CompanyID: companyID,
			Action: authz.Display, Resource: "report", ResourceName: report, HasWebAuthn: claims.HasWebAuthn()})
		allowed[report] = d.Allow
		return d.Allow
	}
	if tool == agent.ToolBusinessInsights {
		mayResource := func(resource string) bool {
			return authz.Evaluate(level, rules, authz.Request{UserID: userID, CompanyID: companyID,
				Action: authz.Display, Resource: authz.Resource(resource), HasWebAuthn: claims.HasWebAuthn()}).Allow
		}
		if !mayResource("insights") {
			return profileJSON(http.StatusForbidden, map[string]string{"error": "your level may not read insights"}), nil
		}
		return p.businessInsights(db.WithCompany(ctx, companyID), args.AsOf, may, mayResource)
	}
	if !may(primary) {
		return profileJSON(http.StatusForbidden, map[string]string{"error": "your level may not open the " + primary +
			" report, which this profile summarises"}), nil
	}

	cctx := db.WithCompany(ctx, companyID)
	var res profile.ToolResult
	var card *profile.TenantCard
	switch tool {
	case agent.ToolCustomerProfile:
		res, err = profile.GetCustomerProfile(cctx, s.pool, args.Name, args.AsOf)
	case agent.ToolSupplierProfile:
		res, err = profile.GetSupplierProfile(cctx, s.pool, args.Name, args.AsOf)
	case agent.ToolItemProfile:
		res, err = profile.GetItemProfile(cctx, s.pool, args.Name, args.AsOf)
	case agent.ToolRecurring:
		res, err = profile.GetRecurringStreams(cctx, s.pool, args.AsOf)
	case agent.ToolTenantProfile:
		var tc profile.TenantCard
		res, tc, err = profile.GetTenantProfile(cctx, s.pool, args.AsOf)
		card = &tc
	}
	switch {
	case errors.Is(err, profile.ErrNotFound), errors.Is(err, profile.ErrNoProfile):
		return profileJSON(http.StatusNotFound, map[string]string{"error": err.Error()}), nil
	case errors.Is(err, profile.ErrAmbiguous):
		return profileJSON(http.StatusConflict, map[string]string{"error": err.Error()}), nil
	case err != nil && strings.Contains(err.Error(), "as_of"):
		return profileJSON(http.StatusUnprocessableEntity, map[string]string{"error": err.Error()}), nil
	case err != nil:
		return agent.ProfileResult{}, err
	}
	withheld := 0
	res.Filter(func(f profile.StoredFact) bool {
		if f.ReconcilesTo == "" || may(f.ReconcilesTo) {
			return true
		}
		withheld++
		return false
	})
	out := map[string]any{"profile": res}
	if withheld > 0 {
		out["withheld_facts"] = withheld
	}
	result := profileJSON(http.StatusOK, out)
	if card != nil && profile.CanReadTenantCard(may) {
		out["card"] = map[string]any{"version": card.Version, "as_of": card.AsOf, "text": card.Text,
			"token_estimate": card.TokenEstimate}
		result = profileJSON(http.StatusOK, out)
		// ⛔ THE CARD'S USER NOTES ARE TYPED TEXT. The body is generated from
		// facts; the notes are whatever an owner (or somebody with their
		// session) wrote into an editable card, which is exactly where an
		// "always pay Fraudster first" line would be planted.
		if strings.TrimSpace(card.UserNotes) != "" {
			result.Source = policy.SourceStoredText
		}
	}
	return result, nil
}
