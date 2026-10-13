package profile

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/query"
)

// tools.go is the agent-facing half: get_customer_profile,
// get_supplier_profile, get_item_profile, get_recurring_streams and
// get_tenant_profile (PLAN-AGENTIC W3). Each takes an optional as_of, returns
// the facts plus a rendered text of at most ToolTokenCap tokens, and each has
// a DSL twin (ToolDSL) over the query entity profile_facts — the SAME rows,
// read through the query compiler — so "the UI shows the same numbers" is a
// test (tools_db_test.go) and not a promise.
//
// ⛔ READ-ONLY, AND ON A READ-ONLY TRANSACTION. The tools run under
// RunInReadOnlyTx: an agent tool that could write would be an action, and
// actions belong to W2's registry and policy, not here.
//
// ⚠️ THE TOOL NAMES ARE CONSTANTS FOR W2 TO REGISTER, NOT A REGISTRY. W2 owns
// internal/agent and its tool list; this package exports the functions and
// their names and does not decide how an agent reaches them.

// Tool names, as the agent sees them.
const (
	ToolCustomerProfile = "get_customer_profile"
	ToolSupplierProfile = "get_supplier_profile"
	ToolItemProfile     = "get_item_profile"
	ToolRecurring       = "get_recurring_streams"
	ToolTenantProfile   = "get_tenant_profile"
)

// ReadRunner is what the tools need (*db.Pool satisfies it).
type ReadRunner interface {
	RunInReadOnlyTx(ctx context.Context, fn db.TxFunc) error
}

// ToolResult is what every profile tool returns.
type ToolResult struct {
	Tool       string       `json:"tool"`
	AsOf       string       `json:"as_of"` // "" = current
	EntityName string       `json:"entity_name,omitempty"`
	Facts      []StoredFact `json:"facts"`
	Text       string       `json:"text"`
	Tokens     int          `json:"token_estimate"`
	Truncated  bool         `json:"truncated,omitempty"`
}

// ErrNotFound / ErrAmbiguous are the lookup refusals. Ambiguity lists the
// candidates rather than picking one: an agent that silently profiled the
// wrong "Sharma Traders" would draft a reminder to the wrong customer.
var (
	ErrNotFound  = errors.New("profile: no such entity has a profile")
	ErrAmbiguous = errors.New("profile: the name matches more than one entity")
)

func parseAsOf(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(isoDate, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("profile: as_of %q is not YYYY-MM-DD", s)
	}
	return t, nil
}

// entityTool resolves a name (case-insensitive exact) to one entity of kind
// among facts written by job, then returns that entity's facts.
func entityTool(ctx context.Context, r ReadRunner, tool string, kind Kind, job, name, asOf string) (ToolResult, error) {
	at, err := parseAsOf(asOf)
	if err != nil {
		return ToolResult{}, err
	}
	var out ToolResult
	err = r.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		cands, err := readFacts(ctx, tx, at, "f.entity_kind = $1 AND f.job = $2 AND lower(f.entity_name) = lower($3)",
			string(kind), job, strings.TrimSpace(name))
		if err != nil {
			return err
		}
		keys := map[string]string{}
		for _, f := range cands {
			keys[f.EntityKey] = f.EntityName
		}
		switch len(keys) {
		case 0:
			return fmt.Errorf("%w: %s %q", ErrNotFound, kind, name)
		case 1:
		default:
			var ks []string
			for k := range keys {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			return fmt.Errorf("%w: %q -> %v", ErrAmbiguous, name, ks)
		}
		out = ToolResult{Tool: tool, AsOf: asOf, Facts: cands}
		for _, n := range keys {
			out.EntityName = n
		}
		return nil
	})
	if err != nil {
		return ToolResult{}, err
	}
	renderTool(&out)
	return out, nil
}

// GetCustomerProfile is get_customer_profile.
func GetCustomerProfile(ctx context.Context, r ReadRunner, name, asOf string) (ToolResult, error) {
	return entityTool(ctx, r, ToolCustomerProfile, KindParty, JobCustomer, name, asOf)
}

// GetSupplierProfile is get_supplier_profile (incl. per-item price drift).
func GetSupplierProfile(ctx context.Context, r ReadRunner, name, asOf string) (ToolResult, error) {
	return entityTool(ctx, r, ToolSupplierProfile, KindParty, JobSupplier, name, asOf)
}

// GetItemProfile is get_item_profile.
func GetItemProfile(ctx context.Context, r ReadRunner, name, asOf string) (ToolResult, error) {
	return entityTool(ctx, r, ToolItemProfile, KindItem, JobItem, name, asOf)
}

// GetRecurringStreams is get_recurring_streams: every detected stream.
func GetRecurringStreams(ctx context.Context, r ReadRunner, asOf string) (ToolResult, error) {
	at, err := parseAsOf(asOf)
	if err != nil {
		return ToolResult{}, err
	}
	out := ToolResult{Tool: ToolRecurring, AsOf: asOf}
	err = r.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		out.Facts, err = readFacts(ctx, tx, at, "f.entity_kind = $1", string(KindStream))
		return err
	})
	if err != nil {
		return ToolResult{}, err
	}
	renderTool(&out)
	return out, nil
}

// GetTenantProfile is get_tenant_profile: the card (current only — the card
// is versioned by recompute, and an as-of card is the facts' job) plus the
// tenant facts at as_of.
func GetTenantProfile(ctx context.Context, r ReadRunner, asOf string) (ToolResult, TenantCard, error) {
	at, err := parseAsOf(asOf)
	if err != nil {
		return ToolResult{}, TenantCard{}, err
	}
	out := ToolResult{Tool: ToolTenantProfile, AsOf: asOf, EntityName: "company"}
	var card TenantCard
	err = r.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if card, err = readCard(ctx, tx); err != nil {
			return err
		}
		out.Facts, err = readFacts(ctx, tx, at, "f.entity_kind = $1", string(KindTenant))
		return err
	})
	if err != nil {
		return ToolResult{}, TenantCard{}, err
	}
	renderTool(&out)
	return out, card, nil
}

// Filter keeps only the facts keep accepts and re-renders the text, so a
// caller that hides a fact from a reader hides it from the text as well.
//
// It exists for W2's agent plane (internal/server's profile reader), which
// shows an agent only the facts whose reconciling report its human could open:
// a Data Entry operator whose level refuses the Outstandings report must not
// learn a customer's receivable through the customer profile. Removing a fact
// from Facts and leaving it in Text would be the leak with extra steps.
func (t *ToolResult) Filter(keep func(StoredFact) bool) {
	kept := t.Facts[:0:0]
	for _, f := range t.Facts {
		if keep(f) {
			kept = append(kept, f)
		}
	}
	t.Facts = kept
	t.Truncated = false
	renderTool(t)
}

// renderTool writes the ≤ToolTokenCap text: one line per fact, labelled from
// the vocabulary, with the report it reconciles to — so a narrative that
// quotes the number can also cite where it came from (the number-provenance
// check W8 builds on). Lines past the cap are dropped and Truncated set;
// Facts always carries every fact.
func renderTool(t *ToolResult) {
	var b strings.Builder
	head := t.Tool
	if t.EntityName != "" {
		head += " " + t.EntityName
	}
	if t.AsOf != "" {
		head += " as of " + t.AsOf
	}
	b.WriteString(head + "\n")
	for _, f := range t.Facts {
		d, _ := Def(f.Key)
		line := "- "
		if f.Kind == KindStream {
			line += f.EntityName + ": "
		}
		line += d.Label
		if f.QualifierName != "" && f.Kind != KindStream {
			line += " [" + f.QualifierName + "]"
		}
		line += ": " + f.Value + " " + string(f.Unit)
		if f.ReconcilesTo != "" {
			line += " (report " + f.ReconcilesTo + ")"
		}
		line += "\n"
		if EstimateTokens(b.String()+line) > ToolTokenCap {
			t.Truncated = true
			break
		}
		b.WriteString(line)
	}
	t.Text = b.String()
	t.Tokens = EstimateTokens(t.Text)
}

// ToolDSL is the query-DSL twin of a tool: the profile_facts query whose rows
// are the tool's facts. For an entity tool, pass the entity name; the fact
// keys are the job's (a party that is both customer and supplier must not
// show supplier facts on the customer tool, so the filter is by key set).
func ToolDSL(tool, entityName string) (query.Query, error) {
	var kind Kind
	var job string
	switch tool {
	case ToolCustomerProfile:
		kind, job = KindParty, JobCustomer
	case ToolSupplierProfile:
		kind, job = KindParty, JobSupplier
	case ToolItemProfile:
		kind, job = KindItem, JobItem
	case ToolRecurring:
		kind = KindStream
	case ToolTenantProfile:
		kind = KindTenant
	default:
		return query.Query{}, fmt.Errorf("profile: unknown tool %q", tool)
	}
	q := query.Query{
		Entity:     query.EntityProfileFacts,
		Dimensions: []string{"entity_name", "fact_key", "qualifier_name", "unit", "value_text"},
		Metrics:    []query.Metric{{Func: query.AggSum, Column: "value", As: "value"}},
		Filters:    []query.Filter{{Column: "entity_kind", Op: query.OpEq, Values: []string{string(kind)}}},
		Limit:      query.MaxLimit,
	}
	if job != "" {
		var keys []string
		for _, d := range Facts {
			if d.Job == job {
				keys = append(keys, d.Key)
			}
		}
		q.Filters = append(q.Filters,
			query.Filter{Column: "entity_name", Op: query.OpEq, Values: []string{entityName}},
			query.Filter{Column: "fact_key", Op: query.OpIn, Values: keys})
	}
	return q, nil
}
