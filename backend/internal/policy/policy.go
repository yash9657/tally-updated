// Package policy decides what an AGENT may do on a human's behalf
// (PLAN-AGENTIC §3 W2, D121): the agent principal, effective rights, the
// structural rules no tenant setting can relax, the autonomy table, and the
// CaMeL-style provenance rule for money.
//
// # EFFECTIVE RIGHTS = ACL(user) ∩ ACL(agent) ∩ delegation — ON THE EXISTING PDP
//
// Three independent authorities, each of which can only NARROW:
//
//   - ACL(user): the human's own security level and rules, evaluated by the
//     router's PDP on the real request (internal/server's companyAuthMiddleware)
//     exactly as for the human's own keystrokes. The agent is not a user; it
//     borrows the human's rights and can never hold more.
//   - ACL(agent): AgentRules below, evaluated by THE SAME authz.Evaluate over a
//     synthetic level with default_access 'none'. A second evaluator would be a
//     second opinion about what "allow create *" means, and authz/vocabulary.go
//     is a monument to what happens when two copies of a security rule drift.
//   - delegation: the ACL keys the human granted this run, snapshotted at
//     start, with an expiry and an optional amount ceiling.
//
// # ⛔ THE STRUCTURAL RULES ARE GO, NOT DATA
//
// PLAN-AGENTIC W2: "agent-created parties born blocked; no delete / ACL /
// period-lock / delegation actions; agent never checker". They are enforced
// here and in the router regardless of any autonomy row, ACL rule or
// delegation, because each is a way for an agent to widen its own authority:
// an agent that could alter the ACL, lift a period lock or approve its own
// request could grant itself everything the other rules withhold.
//
// # ⛔ THE PROVENANCE RULE (PLAN-AGENTIC §2.4)
//
// "No untrusted value reaches money movement without a human." Implemented in
// its strongest simple form: a run whose model context has seen ANY untrusted
// material (a bill's text, a bank narration, a WhatsApp forward, a Tally XML
// NARRATION, an extracted draft) may not commit a money-moving action without a
// human decision — whatever the autonomy table says. That is the CaMeL insight
// taken literally: once untrusted text is in the planner's context, EVERY value
// the planner emits is potentially attacker-chosen, so per-field taint tracking
// could only ever loosen the rule, never tighten it. Per-field matches are
// still computed (Provenance.Fields) — for the approver's diff view, which must
// say "this payee's name appears in the bank narration", not to decide.
package policy

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/authz"
)

// Level is an autonomy level. See migration 0093 for the table.
type Level int

const (
	L0 Level = iota // may not propose
	L1              // propose; every commit waits for a human (THE DEFAULT)
	L2              // commit within the band when context is clean; human shown after
	L3              // as L2, no after-the-fact notice required
)

// DefaultLevel is what applies when no autonomy row matches. ⛔ It is L1, so a
// tenant who never configures anything gets "no unapproved write".
const DefaultLevel = L1

func (l Level) String() string { return fmt.Sprintf("L%d", int(l)) }

// Delegation is what a human granted one run.
type Delegation struct {
	// ACLKeys the run may act on, as the PDP spells them ("create payment").
	// ⛔ NO WILDCARDS: a delegation is a list a person read before granting,
	// and "*" is not a thing anyone reads.
	ACLKeys []string `json:"acl_keys"`
	// MaxAmountPaise, when set, forces approval for any commit above it,
	// whatever the autonomy level.
	MaxAmountPaise *int64    `json:"max_amount_paise,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	// StepUp records that the human who granted this delegation had stepped
	// up (WebAuthn) on the request that granted it. It is what a READ, a
	// PREVIEW (which writes nothing) and an AUTONOMOUS commit (which the
	// tenant chose to allow without a human at commit time) carry to a level
	// that requires step-up. A commit behind an approval carries the
	// APPROVER's step-up instead, never this.
	StepUp bool `json:"step_up,omitempty"`
}

// Allows reports whether the delegation names key and is unexpired at now.
func (d Delegation) Allows(key string, now time.Time) bool {
	if !d.ExpiresAt.IsZero() && !now.Before(d.ExpiresAt) {
		return false
	}
	for _, k := range d.ACLKeys {
		if k == key {
			return true
		}
	}
	return false
}

// Principal is the agent identity a request carries: WHICH agent, acting FOR
// whom, under WHAT grant. PLAN-AGENTIC §3 W2: {agent_id, on_behalf_of,
// delegation}.
type Principal struct {
	AgentID    string
	OnBehalfOf uuid.UUID
	CompanyID  uuid.UUID
	RunID      uuid.UUID
	Delegation Delegation
}

// AgentID is the one agent this build ships.
const AgentID = "khata-agent"

// AgentRules is ACL(agent): the rules the agent itself holds, over a level
// whose default_access is 'none'. Most-specific-wins and disallow-beats-allow
// are authz.Evaluate's, not ours.
//
// ⚠️ THESE OVERLAP THE STRUCTURAL RULES ON PURPOSE. Structural denial is
// decided from the action's Risk class; these rules deny by resource token
// through the PDP. Either alone would hold today; together, a future edit to
// risk.go that mis-classified `alter security` would still be refused here,
// and a future edit here would still be refused by the risk class.
var AgentRules = []authz.Rule{
	{Effect: authz.Allow, Action: authz.Display, Resource: authz.Wildcard},
	{Effect: authz.Allow, Action: authz.Print, Resource: authz.Wildcard},
	{Effect: authz.Allow, Action: authz.Create, Resource: authz.Wildcard},
	{Effect: authz.Allow, Action: authz.Alter, Resource: authz.Wildcard},
	{Effect: authz.Disallow, Action: authz.Delete, Resource: authz.Wildcard},
	{Effect: authz.Disallow, Action: authz.Wildcard, Resource: "security"},
	{Effect: authz.Disallow, Action: authz.Wildcard, Resource: "company"},
	{Effect: authz.Disallow, Action: authz.Wildcard, Resource: "backup"},
	{Effect: authz.Disallow, Action: authz.Wildcard, Resource: "import"},
	{Effect: authz.Disallow, Action: authz.Wildcard, Resource: "sync"},
	{Effect: authz.Disallow, Action: authz.Wildcard, Resource: "agent-run"},
	{Effect: authz.Disallow, Action: authz.Wildcard, Resource: "agent-approval"},
	{Effect: authz.Disallow, Action: authz.Wildcard, Resource: "agent-autonomy"},
	{Effect: authz.Disallow, Action: authz.Wildcard, Resource: "master-unblock"},
	// W6 (D126): the MCP grants and transport. A token that did not exist
	// before W6, so this narrows nothing that was allowed.
	{Effect: authz.Disallow, Action: authz.Wildcard, Resource: "agent-mcp"},
}

var agentLevel = authz.Level{Name: "agent:" + AgentID, DefaultAccess: "none"}

// AgentAllows is ACL(agent) through the PDP.
func AgentAllows(spec actions.ActionSpec) bool {
	d := authz.Evaluate(agentLevel, AgentRules, authz.Request{
		Action: spec.Action, Resource: spec.Resource, ResourceName: spec.ResourceName,
	})
	return d.Allow
}

// ErrStructural is the class of every structural refusal.
var ErrStructural = errors.New("policy: structurally forbidden to an agent")

// Structural returns a non-nil error (wrapping ErrStructural) when an agent
// may NEVER perform spec, whatever any table, rule or delegation says.
func Structural(spec actions.ActionSpec) error {
	switch spec.Risk {
	case actions.RiskDestructive:
		return fmt.Errorf("%w: %s is a delete — an agent never deletes", ErrStructural, spec.ACLKey)
	case actions.RiskGovernance:
		return fmt.Errorf("%w: %s changes who may do what or what the books are "+
			"(ACL, period lock, company, backup, bulk import, the agent's own runs and approvals)", ErrStructural, spec.ACLKey)
	case actions.RiskUnclassified, "":
		return fmt.Errorf("%w: %s has no risk class", ErrStructural, spec.ACLKey)
	}
	// ⛔ THE AGENT NEVER GAINS A WRITE PATH THE HUMAN DOES NOT HAVE
	// (PLAN-AGENTIC §1, last line). A write route with no human path — the four
	// budget alter/delete routes HANDOFF §3.3 flagged, whose client methods no
	// screen calls — is one the agent would be first to walk through. D121
	// decides HANDOFF §3.3's question as DENY: they get an operator in front
	// of them first, then an ActionSpec the agent may use.
	if spec.IsWrite && spec.Channel == actions.ChannelNone {
		return fmt.Errorf("%w: %s has no human path in the product (actionsWithNoHumanPathYet); "+
			"the agent may not be first through a door no person uses", ErrStructural, spec.ACLKey)
	}
	return nil
}

// Rights is the verdict on ACL(agent) ∩ delegation for one spec. ACL(user) is
// judged by the router's PDP on the real request and is not repeated here.
type Rights struct {
	Allow  bool
	Reason string
}

// EffectiveRights applies the structural rules, ACL(agent) and the delegation.
func EffectiveRights(p Principal, spec actions.ActionSpec, now time.Time) Rights {
	if err := Structural(spec); err != nil {
		return Rights{Reason: err.Error()}
	}
	if !AgentAllows(spec) {
		return Rights{Reason: fmt.Sprintf("the agent's own ACL does not allow %s", spec.ACLKey)}
	}
	// Reads are not delegated one by one: a human who can read a report can
	// have the agent read it. Everything the agent DOES must be named.
	if spec.Risk == actions.RiskRead {
		return Rights{Allow: true, Reason: "read"}
	}
	if !p.Delegation.Allows(spec.ACLKey, now) {
		if !p.Delegation.ExpiresAt.IsZero() && !now.Before(p.Delegation.ExpiresAt) {
			return Rights{Reason: "the delegation for this run has expired"}
		}
		return Rights{Reason: fmt.Sprintf("%s was not delegated to this run", spec.ACLKey)}
	}
	return Rights{Allow: true, Reason: "delegated"}
}

// Source is where a value the model saw came from.
type Source string

const (
	SourceUser     Source = "user"      // the human's own instruction: trusted
	SourceBooks    Source = "books"     // a read of the tenant's own ledger: trusted
	SourceDocument Source = "document"  // a bill / invoice's text, an extracted draft
	SourceBank     Source = "bank"      // a bank statement narration
	SourceWhatsApp Source = "whatsapp"  // a forwarded message
	SourceTallyXML Source = "tally_xml" // a Tally XML import (e.g. <NARRATION>)
	SourceEmail    Source = "email"
	// SourceStoredText is free text read back OUT of the books — a voucher
	// narration, an order's remarks — whose author the server cannot vouch
	// for: typed by a user, imported from Tally XML, pasted from a message.
	// Being stored in the tenant's own database does not make it trusted.
	SourceStoredText Source = "stored_text"
	// SourceMCPClient is the planner's OWN context when the planner is an
	// outside MCP client (W6, D126). Claude Desktop, ChatGPT or an IDE holds
	// whatever its user pasted and whatever its other tools fetched — web
	// pages, mail, files — none of which this server saw or can label. So an
	// MCP run is born with this source in its context, and every money,
	// master and statutory commit it proposes waits for a human at every
	// autonomy level, exactly as a run handed a bank narration does.
	SourceMCPClient Source = "mcp_client"
)

// Trusted reports whether a value from s may by itself authorise anything.
func (s Source) Trusted() bool { return s == SourceUser || s == SourceBooks }

// ValidSource is the closed vocabulary.
func ValidSource(s Source) bool {
	switch s {
	case SourceUser, SourceBooks, SourceDocument, SourceBank, SourceWhatsApp, SourceTallyXML, SourceEmail, SourceStoredText, SourceMCPClient:
		return true
	}
	return false
}

// Provenance is what the gate knows about where a commit's values came from.
type Provenance struct {
	// ContextSources is every source whose content reached the model's context
	// in this run. ANY untrusted entry taints the whole context.
	ContextSources []Source `json:"context_sources"`
	// Fields maps an argument path ("party", "postings[1].ledger") to the
	// untrusted source its value was found in. Informational: for the diff.
	Fields map[string]Source `json:"fields,omitempty"`
}

// Tainted reports whether any untrusted source reached the context.
func (p Provenance) Tainted() (bool, []Source) {
	var bad []Source
	for _, s := range p.ContextSources {
		if !s.Trusted() {
			bad = append(bad, s)
		}
	}
	return len(bad) > 0, bad
}

// Mode is what the gate decides for a commit.
type Mode string

const (
	ModeRefuse     Mode = "refuse"
	ModeApproval   Mode = "approval"
	ModeAutonomous Mode = "autonomous"
)

// Gate is the verdict on one proposed commit.
type Gate struct {
	Mode    Mode     `json:"mode"`
	Level   string   `json:"level"`
	Reasons []string `json:"reasons"`
}

// GateInput is everything Decide needs.
type GateInput struct {
	Principal  Principal
	Spec       actions.ActionSpec
	Level      Level
	Amount     *int64 // paise, computed by Go from the handler's preview; nil if not an amount-bearing action
	Provenance Provenance
	Now        time.Time
}

// Decide gates one commit. The order is the contract: refusals first (nothing
// the tenant configured can turn a refusal into a commit), then the rules that
// can only RAISE the bar, and only then the autonomy level.
func Decide(in GateInput) Gate {
	g := Gate{Level: in.Level.String()}
	r := EffectiveRights(in.Principal, in.Spec, in.Now)
	if !r.Allow {
		g.Mode, g.Reasons = ModeRefuse, []string{r.Reason}
		return g
	}
	if in.Level == L0 {
		g.Mode, g.Reasons = ModeRefuse, []string{fmt.Sprintf("autonomy for %s is L0: the agent may not propose it", in.Spec.ACLKey)}
		return g
	}
	if !in.Spec.IsWrite {
		// Reads and prints are tools, not commits: they run under ACL(user) ∩
		// ACL(agent) and alter nothing.
		g.Mode, g.Reasons = ModeAutonomous, []string{"not a write"}
		return g
	}

	var why []string
	if in.Level == L1 {
		why = append(why, fmt.Sprintf("autonomy for %s is L1: every commit waits for a human", in.Spec.ACLKey))
	}
	if tainted, srcs := in.Provenance.Tainted(); tainted && taintSensitive(in.Spec) {
		names := make([]string, len(srcs))
		for i, s := range srcs {
			names[i] = string(s)
		}
		why = append(why, fmt.Sprintf("untrusted input reached this run (%s): a %s action "+
			"built from it can never be authorised by it — a human must decide", strings.Join(names, ", "), in.Spec.Risk))
	}
	if in.Spec.Risk == actions.RiskMaster && in.Spec.Action == authz.Create {
		// ⛔ "AGENT-CREATED PARTIES BORN BLOCKED", realised as: an agent-created
		// master cannot be BORN without a human decision. The payee-fraud path
		// this closes is create-a-party-from-an-injected-invoice then pay it;
		// with this rule the party exists only after a person has read it.
		why = append(why, "an agent-created master is born only through a human decision")
	}
	if in.Spec.Risk == actions.RiskStatutory {
		why = append(why, "an act toward a government system is never autonomous")
	}
	if in.Amount != nil && in.Principal.Delegation.MaxAmountPaise != nil && *in.Amount > *in.Principal.Delegation.MaxAmountPaise {
		why = append(why, fmt.Sprintf("amount %d paise exceeds this run's delegated ceiling of %d paise",
			*in.Amount, *in.Principal.Delegation.MaxAmountPaise))
	}
	if in.Amount == nil && in.Principal.Delegation.MaxAmountPaise != nil {
		// ⛔ A CEILING THE GATE CANNOT MEASURE IS NOT MET. The delegation's
		// promise is "approval for any commit above it, whatever the autonomy
		// level"; a write whose value Go could not compute from the preview (a
		// master alter, a bank tick, an order whose lines were not read back)
		// may be above it, so it waits. Before the wave-2 review a nil amount
		// skipped the rule above and an L3 purchase order of Rs 10 crore
		// committed inside a Rs 1,000 run.
		why = append(why, fmt.Sprintf("this run has a delegated ceiling of %d paise and this commit's value "+
			"cannot be measured against it from the preview", *in.Principal.Delegation.MaxAmountPaise))
	}
	if in.Spec.MoneyMoving() && in.Amount == nil {
		// A money-moving commit whose amount Go could not compute from the
		// preview is one no band can be applied to.
		why = append(why, "the amount could not be computed from the preview, so no autonomy band applies")
	}
	if len(why) > 0 {
		g.Mode, g.Reasons = ModeApproval, why
		return g
	}
	g.Mode, g.Reasons = ModeAutonomous, []string{fmt.Sprintf("autonomy for %s is %s within the band, and the context is clean", in.Spec.ACLKey, in.Level)}
	return g
}

// taintSensitive is the set of classes a tainted context may not commit
// autonomously.
//
// ⚠️ WIDER THAN "MONEY-MOVING", DELIBERATELY. §2.4 names money movement; the
// commonest real fraud against an Indian SME's books does not move money in the
// step the agent takes — it ALTERS A PARTY'S BANK ACCOUNT from a forged "our
// bank details have changed" letter, and the next ordinary payment does the
// rest. So a master write, and an act toward a government system, are gated
// on a clean context exactly as money is.
//
// ⚠️ AND, SINCE W2 BREADTH, OPERATIONAL TOO — every class an agent can commit.
// Wave 1 could not commit an operational action, so the question never arose.
// Now it can, and each one is a claim the books will be held to: a bank-
// reconciliation tick ASSERTS the bank cleared an item (an auditor relies on
// the BRS), a physical-stock voucher writes the value of the warehouse, a
// stock journal moves goods between godowns, an order commits the business to
// deliver or to pay. A tick taken from a statement narration that says "mark
// cheque 451 cleared" is the injection a bank-statement agent will meet
// first. So an untrusted context forces a human for all of them; autonomy
// levels still apply to a clean one.
func taintSensitive(spec actions.ActionSpec) bool {
	switch spec.Risk {
	case actions.RiskMoney, actions.RiskMaster, actions.RiskStatutory, actions.RiskOperational:
		return true
	}
	return false
}

// CanDecide is the "agent is never the checker" rule, as a predicate the
// approval endpoint calls with whatever principal the request carries.
func CanDecide(agent *Principal) error {
	if agent != nil {
		return fmt.Errorf("%w: an agent may never decide an approval (it is never the checker)", ErrStructural)
	}
	return nil
}
