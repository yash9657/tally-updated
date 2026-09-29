package server

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/profile"
	"github.com/khata/cored/internal/query"
)

// query_authz.go is the per-ENTITY half of POST /query's authorisation
// (HANDOFF-AGENTIC-WAVE-2 §6.10).
//
// ⛔ THE ROUTE RIGHT GOVERNS THE DOOR, NOT THE BYTES. routeAction maps every
// POST /query to `display query`, and the PDP cannot see the body, so before
// this file a level holding `query` but carrying `disallow display
// report:bills-payable` read every payable through the outstandings entity —
// D97's row again (the payables book behind the receivables report's door),
// reached through the conversational screen instead of a query parameter. The
// fix cannot be a routeAction arm for the same reason D97's could not: the
// entity is in the BODY.
//
// ⛔ SO EACH ENTITY NAMES THE REPORTS WHOSE FIGURES IT CAN REVEAL, AND THE
// CALLER MUST BE ABLE TO OPEN EVERY ONE OF THEM. The question is "could this
// person read these numbers on a report screen?", asked of the same PDP with
// the same level, rules and step-up. An entity missing from the table is
// REFUSED, not waved through: a new entity must declare its reports before it
// answers anyone (TestEveryQueryEntityDeclaresItsReports fails first).
//
// The route right still applies — a level that disallows `query` never gets
// here. This check only narrows it.

// queryEntityReports is the closed table: entity -> reports it can reveal.
var queryEntityReports = map[query.Entity][]string{
	// Every ledger's balance is the Trial Balance, line for line.
	query.EntityLedgerBalances: {"trial-balance"},
	// Voucher heads and their posting lines are what the Day Book lists.
	query.EntityVouchers: {"day-book"},
	query.EntityPostings: {"day-book"},
	// Outstandings is resolved per query below: receivables only when the
	// query pins nature = ASSET, otherwise both sides.
	query.EntityOutstandings:       {"outstandings", "bills-payable"},
	query.EntityStockMovements:     {"stock-movement"},
	query.EntityBankStatementLines: {"bank-statement"},
	// Resolved from internal/profile's closed vocabulary below.
	query.EntityProfileFacts: nil,
}

// profileFactReports is every report a current fact can summarise: each
// fact's ReconcilesTo, plus the primary report of every profile tool, because
// a STATISTIC fact (DSO, lead time, drift) has no ReconcilesTo of its own and
// is derived from the tool's primary report. The agent's profile tools gate
// fact by fact (agent_profiles.go); the query entity projects all facts in one
// table and cannot know which rows it will return before it runs, so it
// requires them all. Stricter than the tools, never looser.
func profileFactReports() []string {
	set := map[string]bool{}
	for _, d := range profile.Facts {
		if d.ReconcilesTo != "" {
			set[d.ReconcilesTo] = true
		}
	}
	for _, r := range profilePrimaryReport {
		set[r] = true
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// reportsForQuery returns the reports q can reveal, or ok=false for an entity
// that has not declared any (refused).
func reportsForQuery(q query.Query) ([]string, bool) {
	reps, ok := queryEntityReports[q.Entity]
	if !ok {
		return nil, false
	}
	switch q.Entity {
	case query.EntityProfileFacts:
		return profileFactReports(), true
	case query.EntityOutstandings:
		if outstandingsPinnedToReceivables(q) {
			return []string{"outstandings"}, true
		}
	}
	return reps, true
}

// outstandingsPinnedToReceivables is true only when a filter restricts the
// rows to nature = ASSET exactly. Anything else — no filter, IN (both), a
// neq, a group-by on nature — can return supplier bills and needs the
// payables report too. The query compiler's own orientation rule
// (outstandingIsPayable) answers a DIFFERENT question — which sign to sum —
// and must not be reused here: it treats "no filter" as receivable.
func outstandingsPinnedToReceivables(q query.Query) bool {
	for _, f := range q.Filters {
		if f.Column == "nature" && f.Op == query.OpEq && len(f.Values) == 1 && f.Values[0] == "ASSET" {
			return true
		}
	}
	return false
}

// queryReportDenial returns "" when the caller may read every report q can
// reveal, or the refusal to show them. A system error is returned as err.
func (s *Server) queryReportDenial(ctx context.Context, companyID uuid.UUID, q query.Query) (string, error) {
	reps, ok := reportsForQuery(q)
	if !ok {
		return fmt.Sprintf("the %q entity has not declared which reports it reveals, so it answers no one", q.Entity), nil
	}
	userID, ok := db.UserFromContext(ctx)
	if !ok {
		// companyAuthMiddleware always injects the user; its absence means the
		// route was reached without it, which must not read anything.
		return "no authenticated user on the request", nil
	}
	levelID, member, err := s.authStore.Membership(ctx, userID, companyID)
	if err != nil {
		return "", err
	}
	if !member {
		return "not a member of this company", nil
	}
	level, err := s.authStore.Level(ctx, companyID, levelID)
	if err != nil {
		return "", err
	}
	rules, err := s.authStore.Rules(ctx, companyID, levelID)
	if err != nil {
		return "", err
	}
	for _, rep := range reps {
		d := authz.Evaluate(level, rules, authz.Request{UserID: userID, CompanyID: companyID,
			Action: authz.Display, Resource: "report", ResourceName: rep, HasWebAuthn: stepUpFrom(ctx)})
		if !d.Allow {
			return fmt.Sprintf("your level may not open the %s report, whose figures the %s entity reveals", rep, q.Entity), nil
		}
	}
	return "", nil
}
