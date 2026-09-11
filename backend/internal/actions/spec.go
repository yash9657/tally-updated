package actions

import (
	"sort"
	"strings"

	"github.com/khata/cored/internal/authz"
)

// Channel names how a HUMAN reaches this action today.
//
// It exists so the coverage gate can ask the question W0 actually cares about:
// is there anything in the product a person can press that lands on this ACL
// key? An action with no human path is not automatically wrong — a few routes
// are machine-to-machine — but it must be DECLARED as such with a reason, never
// discovered later by an agent that finds a door nobody knew was open.
type Channel string

const (
	// ChannelKeystroke: a Tally-compatible key or chord reaches it (the F-keys,
	// Alt+chords, the global keymap). This is the channel that matters most for
	// parity: it means the action is part of the keystroke surface an operator
	// already knows.
	ChannelKeystroke Channel = "keystroke"
	// ChannelScreen: a screen action or an in-screen control reaches it, but no
	// dedicated key does.
	ChannelScreen Channel = "screen"
	// ChannelClient: a method on the shipped REST client calls this route, and
	// the app's own source outside src/api references that method — so a person
	// driving the running product reaches it.
	//
	// ⚠️ IT IS A WEAKER CLAIM THAN THE TWO ABOVE AND THE NAME SAYS SO. It does
	// NOT establish which key or which control fires the call: the frontend
	// manifest can enumerate what each client method requests (it drives the
	// real client against a recording transport) but not which handler invokes
	// that method, because answering THAT would mean mounting every screen and
	// firing every control — a census of what happens to be mounted, which is
	// the trap the manifest is built to avoid. Upgrading these rows to
	// keystroke/screen is the remaining half of W0 lane E: the generator already
	// reads `commands[].acl` and prefers it, so the day a command names the
	// calls it makes, these rows sharpen without any change here.
	//
	// ⛔ AND `used` IS PART OF THE CLAIM, NOT A DETAIL. Ten of the 159 client
	// methods are referenced nowhere in the app; those routes stay ChannelNone
	// and must be declared, because a method nothing calls puts nobody in front
	// of a route.
	ChannelClient Channel = "client"
	// ChannelNone: NOTHING in the UI reaches it. Every spec carrying this must
	// appear in registry_test.go's actionsWithNoHumanPathYet with a written
	// reason, or the coverage gate fails.
	ChannelNone Channel = "none"
)

// ActionSpec is one governable operation: a (method, route) the router mounts,
// and the ACL key the PDP emits for it.
//
// ⛔ EVERY FIELD IS DERIVED BY THE GENERATOR. Do not hand-edit registry_gen.go;
// see package doc.go for why the derivation IS the safety property, and the
// generated file's header for the command that rewrites it.
type ActionSpec struct {
	// ID is stable and derived from Method+Route: the lowercased method, then
	// the route's segments below /v1/companies/{companyID}, dot-joined, with a
	// path parameter {voucherID} rendered as `_voucherid`.
	//
	// ⚠️ PARAMETER SEGMENTS ARE KEPT, NOT DROPPED, and the underscore is what
	// keeps them distinguishable from a literal segment of the same name. The
	// obvious shortening — drop `{...}` segments so PUT /vouchers/sales/{id}
	// reads "put.vouchers.sales" — collides GET /vouchers with
	// GET /vouchers/{voucherID}, which are a list and a fetch of one document.
	// The generator refuses to write a registry containing a duplicate ID, so a
	// future shortening cannot land silently.
	ID     string `json:"id"`
	Method string `json:"method"`
	// Route is the chi PATTERN, never a concrete path: the generator gets it
	// from chi.Walk / chi's own Find, so it is the router's spelling and not a
	// transcription of it.
	Route string `json:"route"`

	// Action, Resource and ResourceName are VERBATIM what routeAction returned
	// for a synthesised request against this route. They are the ACL key.
	Action       authz.Action   `json:"action"`
	Resource     authz.Resource `json:"resource"`
	ResourceName string         `json:"resource_name,omitempty"` // only when Resource == "report"
	// ACLKey is rendered FROM the three above by RenderACLKey, so the string an
	// operator would type into Users & Roles and the tuple the PDP compares are
	// produced by one function with one spelling.
	ACLKey string `json:"acl_key"`

	// IsWrite is authz.Action.IsWrite(): create|alter|delete ONLY.
	//
	// ⛔ IT MEANS "THIS ALTERS THE BOOKS", NOT "THIS TOUCHES NO TABLE", AND A
	// POLICY LAYER MUST NOT READ IT AS THE SECOND. The metering plane writes on
	// every action including every read: with LLM_BUDGET_TOKENS_PER_MONTH set,
	// `display query` (POST /v1/companies/{companyID}/query, is_write=false
	// here) runs llm.PGBudget.Reserve/Settle, which INSERT and UPDATE
	// llm_token_budget in a read-write transaction on behalf of a principal
	// holding only display rights. That is bounded, tenant-scoped, RLS-forced
	// counter data and not ledger data — see internal/server's buildBudget for
	// the full statement — but a W2 autonomy check that inferred "no database
	// writes" from is_write=false would be inferring something this field does
	// not say.
	//
	// ⚠️ `print` IS NOT A WRITE, and that is the PDP's answer, not a convenience
	// here. Printing takes neither the back-dated cutoff nor the WebAuthn
	// step-up. An agent-autonomy table that treated print as a write would be
	// stricter than the product, which sounds safe and is merely wrong: it would
	// diverge from what the tenant's own ACL rules do.
	IsWrite bool `json:"is_write"`
	// StepUpEligible is true when a level with RequiresWebAuthn would refuse
	// this action with STEPUP_REQUIRED. It is obtained by calling authz.Evaluate
	// against a synthetic maximal level, not by re-deriving webauthnDeny's
	// condition — see the generator's stepUpEligible for why a second copy of a
	// security rule is the defect being avoided.
	//
	// ⚠️ IT IS CURRENTLY INDISTINGUISHABLE FROM IsWrite, ON EVERY ROW, AND A
	// READER MUST NOT TAKE IT FOR A SECOND, INDEPENDENT CONTROL. Step-up has
	// exactly one trigger today (the action is a write), so replacing the
	// authz.Evaluate call with `return act.IsWrite()` would change no byte of
	// either artefact. It is still recorded per row for a reason CutoffJudged
	// could not claim and was therefore deleted (see the block below): the value
	// here is what the PDP answers for the REAL request, because Evaluate's whole
	// input is (action, resource, resourceName) and those three are what a live
	// request carries. Nothing is synthesised except the level, which is the
	// question — "could a step-up stand here at all" — rather than an assumption
	// about the caller. So the day step-up grows a second trigger (an amount band,
	// a resource class) this column starts disagreeing with IsWrite by itself, and
	// the W2 autonomy table that reads it sharpens without a change here.
	StepUpEligible bool `json:"step_up_eligible"`

	// ⛔ THERE IS NO CutoffJudged FIELD, AND ITS ABSENCE IS THE HONEST ANSWER.
	// One shipped here, documented as "measured by running the real date
	// oracles", and it carried no information: the generator sent every route a
	// probe body holding all four date spellings the cutoff seam knows
	// (voucher_date, wage_month, deposit_date, bank_date), and
	// voucherDateFromRequest falls through to a route-agnostic peekVoucherDate
	// walk for every write that is not a pay run, a TDS challan or a
	// bank-reconciliation leg. So every write necessarily answered true and the
	// column was IsWrite spelled a second way — 230 of 230 rows.
	//
	// ⛔ AND IT WAS WORSE THAN REDUNDANT: IT WAS COUNTERFACTUAL. `delete
	// members/{userID}` (removing a company member) and `delete books-closure`
	// both read as governed by the back-dated cutoff, because the probe handed
	// them a voucher_date. Their real requests carry no body at all, so
	// voucherDateFromRequest returns nil and cutoffDeny never runs. W2 builds an
	// autonomy table on these fields; "the back-dated period lock governs
	// deleting a user" is a control that would be read and believed, and it is
	// not there.
	//
	// The two answers that ARE real are kept: IsWrite (the cutoff runs on
	// create|alter|delete and on nothing else — that is cutoffDeny's own
	// condition, not a per-route fact) and DateSubject below, which is derived
	// from the method and the path alone and so says something no other column
	// does. A per-route cutoff column can come back the day it can be measured
	// from a request the product actually sends.

	// DateSubject is what a cutoff refusal on this route would NAME, straight
	// from internal/server's cutoffDateSubject: "bank date", "deposit date", or
	// empty for the voucher's own date (which is every other write).
	DateSubject string `json:"date_subject,omitempty"`

	// Channel / UICommand record the human path, joined from the frontend's
	// generated ui-commands.json by (verb, path) -> the router -> this route.
	// UICommand is the command id on the frontend side, so a reviewer can grep
	// one string across both trees.
	Channel   Channel `json:"channel"`
	UICommand string  `json:"ui_command,omitempty"`

	// Label is the row rendered for a human reader: the action, the resource with
	// the word `voucher` or `report` added where the resource is one, then the
	// (method, route) in brackets —
	//
	//	alter sales voucher (PUT /v1/companies/{companyID}/vouchers/sales/{voucherID})
	//	delete security (DELETE /v1/companies/{companyID}/members/{userID})
	//
	// ⛔ IT IS CALLED Label AND NOT Description BECAUSE IT DESCRIBES NOTHING THE
	// OTHER COLUMNS DO NOT ALREADY SAY. It shipped as `Description`, documented
	// as "what a tool-search will embed later", and it is a mechanical join of
	// Action, Resource, Method and Route — so an embedding built from it cannot
	// separate `delete security (DELETE .../members/{userID})` from `delete security
	// (DELETE .../members/invites/{inviteID})`: removing a member from revoking
	// an invite, which is exactly the discrimination a retrieval field exists to
	// provide. This repo's own rule about a comment that restates the code,
	// applied to a data column: the name now claims what the string is worth.
	//
	// ⚠️ SO W3'S RETRIEVAL MUST NOT BE BUILT ON THIS COLUMN. When there is a real
	// source for a sentence — a doc line on the handler, or a hand-curated map
	// with a vacuity floor of the shape actionsWithNoHumanPathYet already uses —
	// it lands as its own field beside this one and this stays what it is: the
	// string a reviewer reads in a diff.
	Label string `json:"label"`
}

// RuleResource is the resource token AS AN ACL RULE WOULD SPELL IT: the bare
// resource, or `report:NAME` when the arm named a report.
//
// ⛔ THIS IS THE JOIN POINT WITH internal/authz, AND IT IS WHY THE FIELD IS NOT
// PRE-JOINED IN THE STRUCT. authz.Rule carries ONE resource string and
// ValidateRule splits it on ":" itself, while authz.Request carries the base and
// the name in two fields. Keeping both shapes reachable from one spec is what
// lets registry_test.go feed a spec to ValidateRule and prove an administrator
// could actually write a rule naming it — the check that makes "the ACL key
// equals the arm" falsifiable rather than asserted.
func (s ActionSpec) RuleResource() authz.Resource {
	if s.ResourceName == "" {
		return s.Resource
	}
	return authz.Resource(string(s.Resource) + ":" + s.ResourceName)
}

// RenderACLKey spells the key the way Users & Roles spells a rule: "<action>
// <resource>", with the report name appended after a colon.
//
// ⚠️ ONE RENDERER, CALLED FROM BOTH SIDES. The generator in internal/server
// calls this function rather than formatting the key itself, so the string
// checked into registry_gen.go and the string this package reconstructs cannot
// drift by a space or a colon. A second spelling here would be the "second
// table" doc.go forbids, in miniature.
func RenderACLKey(action authz.Action, resource authz.Resource, resourceName string) string {
	res := string(resource)
	if resourceName != "" {
		res += ":" + resourceName
	}
	return string(action) + " " + res
}

// ByID returns the spec with this id.
func ByID(id string) (ActionSpec, bool) {
	for _, s := range Specs {
		if s.ID == id {
			return s, true
		}
	}
	return ActionSpec{}, false
}

// ByACLKey returns every spec that evaluates to this ACL key.
//
// ⚠️ IT RETURNS A SLICE BECAUSE THE RELATION IS GENUINELY MANY-TO-ONE, and a
// signature that pretended otherwise would hide the fact that matters most
// here. Seven master collections all map to `create master`: one rule reading
// `disallow create master` governs all of them at once. An agent-side check
// that resolved a key to "the" route would silently pick one and leave the
// other six ungoverned by whatever it built on top.
func ByACLKey(key string) []ActionSpec {
	var out []ActionSpec
	for _, s := range Specs {
		if s.ACLKey == key {
			out = append(out, s)
		}
	}
	return out
}

// Search is the deterministic substring matcher behind the future
// `actions.search` meta-tool. It is case-insensitive over the id, the ACL key,
// the label and the UI command; an empty query returns everything.
//
// ⚠️ THE LABEL IT MATCHES CARRIES NO WORDS THE ID AND THE KEY DO NOT. See
// ActionSpec.Label: matching it widens nothing today, and is kept only so that
// the day a real sentence lands beside it this function is already reading the
// human-facing column.
//
// ⚠️ IT IS DELIBERATELY NOT RANKED AND NOT FUZZY. W3 replaces the body of this
// function with embedding retrieval over the same rows. Until then a lexical
// match that is stable across runs is worth more than a clever one, because the
// agent evals in W1 have to be reproducible: a scorer whose ordering shifts
// makes a regression indistinguishable from noise.
func Search(q string) []ActionSpec {
	q = strings.ToLower(strings.TrimSpace(q))
	var out []ActionSpec
	for _, s := range Specs {
		if q == "" ||
			strings.Contains(strings.ToLower(s.ID), q) ||
			strings.Contains(strings.ToLower(s.ACLKey), q) ||
			strings.Contains(strings.ToLower(s.Label), q) ||
			(s.UICommand != "" && strings.Contains(strings.ToLower(s.UICommand), q)) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Provenance records WHICH SOURCES actually contributed to the generated
// registry. It is written by the generator into registry_gen.go as Source.
//
// ⛔ IT EXISTS BECAUSE A HALF-BUILT REGISTRY LOOKS EXACTLY LIKE A FULL ONE.
// The coverage gate's headline assertion is a count, and a count is satisfied
// by 169 route arms and zero UI commands just as well as by both — at which
// point the gate is green while proving half of nothing, and the half it stops
// proving is the one W0 was commissioned for ("any UI command without an action
// or any action without a UI path"). So the generator records what it actually
// had, and registry_test.go refuses a registry that was built from an
// incomplete set of sources.
//
// This is also the shape house rule 3 requires of a missing input: not a quiet
// default, but a stated degradation that a later reader trips over. Degradation
// is non-empty exactly when something the generator wanted was not there, and
// it says what.
type Provenance struct {
	// RouteArms is how many (method, route) pairs the router walk produced.
	RouteArms int `json:"route_arms"`
	// UICommandsJoined is how many of the frontend manifest's commands resolved
	// to a route in this registry.
	UICommandsJoined int `json:"ui_commands_joined"`
	// ClientCallsJoined is how many of the manifest's client_calls resolved to a
	// route here, and ClientCallsUnused how many of those are calls to a method
	// nothing in the app references — reachable by the CLIENT, not by a person,
	// and therefore not counted as a human path.
	//
	// ⚠️ THE TWO NUMBERS ARE SEPARATE BECAUSE THEY ANSWER DIFFERENT QUESTIONS.
	// A drop in the first means the join broke; a rise in the second means the
	// product grew a client method no screen calls, which is a finding about the
	// product.
	ClientCallsJoined int `json:"client_calls_joined"`
	ClientCallsUnused int `json:"client_calls_unused"`
	// UIManifestPresent is whether the frontend manifest existed at generation
	// time at UIManifestPath.
	UIManifestPresent bool   `json:"ui_manifest_present"`
	UIManifestPath    string `json:"ui_manifest_path"`
	// Degradation is empty on a complete build, and otherwise names in prose
	// what was missing and what the registry consequently does not prove.
	Degradation string `json:"degradation,omitempty"`
}
