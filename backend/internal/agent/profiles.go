package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/khata/cored/internal/policy"
)

// profiles.go registers W3's five profile tools (internal/profile/tools.go,
// PLAN-AGENTIC W3) in the agent's tool list: get_customer_profile,
// get_supplier_profile, get_item_profile, get_recurring_streams and
// get_tenant_profile.
//
// ⛔ THROUGH A SEAM, NOT AN IMPORT. internal/agent may not import
// internal/profile (archtest: the agent reads the books only through the
// Dispatcher and query_books), and there is no HTTP route to dispatch to yet
// (W3 owes the routes, under the bare token agent-profile). So internal/server
// implements ProfileReader, and that implementation — not this package — is
// where ACL(user) is applied: a fact is returned only when the human could
// open the report it reconciles to (see server/agent_profiles.go). The agent
// sees what its human could have read, and nothing a report would have hidden.
//
// ⛔ AND THE LABEL IS THE READER'S, PER RESULT. A profile is Go-computed
// figures over master names — "books" — EXCEPT the tenant card's user notes,
// which are free text an owner typed into an editable card and are therefore
// stored_text. The reader says which (ProfileResult.Source), so the CaMeL gate
// sees a card with notes as untrusted and one without as clean.

// Profile tool names, as the model sees them. They are internal/profile's
// constants, restated here because this package cannot import that one;
// TestProfileToolNamesMatchW3 pins the two lists together.
const (
	ToolCustomerProfile  = "get_customer_profile"
	ToolSupplierProfile  = "get_supplier_profile"
	ToolItemProfile      = "get_item_profile"
	ToolRecurring        = "get_recurring_streams"
	ToolTenantProfile    = "get_tenant_profile"
	ToolBusinessInsights = "get_business_insights"
)

// ProfileTools is the five, in the order the prompt lists them.
var ProfileTools = []string{ToolCustomerProfile, ToolSupplierProfile, ToolItemProfile, ToolRecurring, ToolTenantProfile}

func isProfileTool(t string) bool {
	if t == ToolBusinessInsights {
		return true
	}
	for _, p := range ProfileTools {
		if p == t {
			return true
		}
	}
	return false
}

// ProfileArgs is a profile tool's arguments: an entity name (the three entity
// tools) and an optional as_of date (every tool).
type ProfileArgs struct {
	Name string `json:"name,omitempty"`
	AsOf string `json:"as_of,omitempty"`
}

// ProfileResult is what a profile read produced.
type ProfileResult struct {
	// Status is HTTP-shaped: 200, 403 (the human may not read the report the
	// profile summarises), 404 (no such entity / no profile yet), 409
	// (ambiguous name), 422 (bad args).
	Status int
	Body   json.RawMessage
	// Source is the provenance of what Body puts in the model's context.
	Source policy.Source
}

// ProfileReader is the seam internal/server implements.
type ProfileReader interface {
	ReadProfile(ctx context.Context, c Caller, tool string, args ProfileArgs) (ProfileResult, error)
}

// profile runs one profile tool. A returned error is transient.
func (l *loop) profile(ctx context.Context, d ModelDecision) (toolOut, error) {
	var a ProfileArgs
	if err := strictDecode(d.Args, &a); err != nil {
		return toolErr(d.Tool, "args: "+err.Error()), nil
	}
	entity := d.Tool == ToolCustomerProfile || d.Tool == ToolSupplierProfile || d.Tool == ToolItemProfile
	switch {
	case entity && strings.TrimSpace(a.Name) == "":
		return toolErr(d.Tool, "args: name is required"), nil
	case !entity && a.Name != "":
		return toolErr(d.Tool, "args: this tool takes no name"), nil
	case len(a.Name) > 200:
		return toolErr(d.Tool, "args: name is too long"), nil
	case a.AsOf != "" && !isoDate(a.AsOf):
		return toolErr(d.Tool, "args: as_of must be YYYY-MM-DD"), nil
	}
	if l.e.Profiles == nil {
		return toolErr(d.Tool, "profiles are not available on this server"), nil
	}
	res, err := l.e.Profiles.ReadProfile(ctx, l.caller(l.run.Delegation.StepUp), d.Tool, a)
	if err != nil {
		return toolOut{}, err
	}
	src := res.Source
	if !policy.ValidSource(src) {
		// A reader that forgot to label its result is untrusted by default.
		src = policy.SourceStoredText
	}
	ok := res.Status >= 200 && res.Status < 300
	out := toolOut{Tool: d.Tool, OK: ok, Result: trimBody(res.Body), Sources: []policy.Source{src}}
	if !ok {
		out.Error = fmt.Sprintf("status %d: %s", res.Status, verifierMessage(res.Body))
	}
	return out, nil
}
