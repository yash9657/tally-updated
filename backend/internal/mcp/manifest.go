package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/policy"
)

// The tool manifest: what tools/list shows one session, GENERATED from the
// ActionSpec registry and filtered by the SAME policy.EffectiveRights the
// in-app agent's actions.search filters by — so a client is never shown a door
// the dispatcher would refuse, and a door is never missing because two lists
// drifted.
//
// ⛔ A TOOL IS A NAME FOR AN agent.ModelDecision, NOTHING MORE. Each binding
// below says which of the agent's own tools a call becomes (actions.read,
// actions.preview, actions.commit, actions.search, query_books, get_draft) and
// with which action id. The call is then handed to agent.Engine.Submit, which
// records it as decide:k and lets the loop do the rest. No binding reaches a
// handler, a service or the dispatcher.

// Tool is an MCP tool definition (2025-06-18 "Tool").
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations *Annotations    `json:"annotations,omitempty"`
	Meta        map[string]any  `json:"_meta,omitempty"`
}

// Annotations are MCP's tool hints. ⚠️ HINTS TO THE CLIENT, NEVER AUTHORITY
// HERE: nothing on the server reads them back; the risk class and the gate
// decide.
type Annotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	IdempotentHint  bool `json:"idempotentHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

type bindKind string

const (
	bindRead    bindKind = "read"    // actions.read of one GET read spec
	bindPreview bindKind = "preview" // actions.preview of one previewable write spec
	bindCommit  bindKind = "commit"  // actions.commit {"preview": "tool:N"}
	bindSearch  bindKind = "search"  // actions.search
	bindQuery   bindKind = "query"   // query_books
	bindDraft   bindKind = "draft"   // get_draft
	bindStatus  bindKind = "status"  // the run's own record; NOT a decision (see status in server.go)
	bindReverse bindKind = "reverse" // actions.reverse (W2 breadth: undo as a Go-built reversal)
	bindProfile bindKind = "profile" // one of W3's five profile tools, by the agent's own name
)

type binding struct {
	kind bindKind
	spec actions.ActionSpec // bindRead / bindPreview only
	tool string             // bindProfile: the agent tool the call becomes
}

// Manifest is one session's tools and the hash that pins them.
type Manifest struct {
	Tools    []Tool
	Hash     string
	bindings map[string]binding
}

// Meta keys on every tool. MCP reserves no "khata/" prefix.
const (
	metaRisk     = "khata/risk"
	metaACLKey   = "khata/acl_key"
	metaActionID = "khata/action_id"
	metaMode     = "khata/mode"
)

// Tools that are the agent's meta-tools rather than one ActionSpec each.
const (
	ToolSearchActions = "search_actions"
	ToolQueryBooks    = "query_books"
	ToolGetDraft      = "get_draft"
	ToolCommit        = "commit_preview"
	ToolRunStatus     = "run_status"
	// ToolReverseVoucher is actions.reverse: name a posted voucher, and Go
	// builds and previews the voucher that cancels it (agent/reverse.go).
	ToolReverseVoucher = "reverse_voucher"
)

// profileToolDescriptions are W3's five profile tools (agent.ProfileTools),
// listed under the agent's own names — they are already valid MCP names — so
// an MCP client and the in-app model call the same five doors. The server's
// ProfileReader applies the delegating human's ACL fact by fact and labels a
// tenant card with typed notes stored_text, exactly as for the in-app agent.
var profileToolDescriptions = map[string]string{
	agent.ToolCustomerProfile: "A customer's profile (payment behaviour, typical terms, outstanding), computed from the books. Facts the delegating user could not read in a report are withheld.",
	agent.ToolSupplierProfile: "A supplier's profile (lead time, price history, payment terms), computed from the books. Facts the delegating user could not read in a report are withheld.",
	agent.ToolItemProfile:     "A stock item's profile (movement, margin inputs, reorder signals), computed from the books.",
	agent.ToolRecurring:       "The company's recurring streams (rent, salaries, subscriptions) as reconciled against the day book.",
	agent.ToolTenantProfile:   "The company's tenant card. Notes on it were typed by a person and are untrusted text.",
}

// draftSpecID is read through get_draft (document-tainted, non-uuid id), never
// through actions.read.
const draftSpecID = "get.drafts._draftid"

// ToolName is an ActionSpec id as an MCP tool name: the dots become
// underscores ("post.vouchers.payment" -> "post_vouchers_payment"), because
// several clients accept only [A-Za-z0-9_-] although 2025-11-25 also allows
// ".". TestToolNamesAreUniqueAndValid proves the mapping is injective over the
// whole registry and every name is at most 64 characters.
func ToolName(specID string) string { return strings.ReplaceAll(specID, ".", "_") }

// routeParams are the {param} names of a route below {companyID}.
func routeParams(route string) []string {
	var out []string
	for _, seg := range strings.Split(route, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") && seg != "{companyID}" {
			out = append(out, seg[1:len(seg)-1])
		}
	}
	return out
}

func pathParamsSchema(params []string) map[string]any {
	props := map[string]any{}
	for _, p := range params {
		if p == "legNo" {
			// An ordinal, not an id (agent.ordinalParams).
			props[p] = map[string]any{"type": "string", "pattern": "^[0-9]{1,4}$"}
			continue
		}
		props[p] = map[string]any{"type": "string", "format": "uuid"}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "required": params, "properties": props}
}

// voucherBodySchema describes the body the eight accounting voucher creates
// take, as far as the agent's own prompt describes it. ⚠️ A HINT FOR THE
// CLIENT'S MODEL, NOT THE VALIDATOR: additionalProperties is open because the
// handlers accept more (items, party, bill allocations), and the handler's own
// decoder and validators are what accept or refuse a body — in preview, where
// a refusal costs nothing. Generating this from the handlers' request types is
// owed (D126 §9).
var voucherBodySchema = map[string]any{
	"type":     "object",
	"required": []string{"voucher_date", "postings"},
	"properties": map[string]any{
		"voucher_date": map[string]any{"type": "string", "format": "date"},
		"narration":    map[string]any{"type": "string"},
		"postings": map[string]any{"type": "array", "minItems": 2, "items": map[string]any{
			"type": "object", "required": []string{"ledger_id", "dr_cr", "amount"},
			"properties": map[string]any{
				"ledger_id": map[string]any{"type": "string", "format": "uuid"},
				"dr_cr":     map[string]any{"type": "string", "enum": []string{"D", "C"}},
				"amount": map[string]any{"type": "string", "pattern": `^[0-9]+(\.[0-9]{1,2})?$`,
					"description": "rupees as a decimal string, e.g. \"1180.00\"; never computed by you"},
			}}},
	},
}

// openBodySchema is the hint for every previewable write that is not an
// accounting voucher body (alterations of stock vouchers, masters, orders,
// bank ticks): an object, validated by the handler in preview.
var openBodySchema = map[string]any{"type": "object",
	"description": "the request body the route takes, exactly as the product's own screen sends it; the handler's validators decide in preview"}

// voucherBodyRoute reports whether a spec takes the accounting voucher body:
// the eight creates and their alterations (POST/PUT /vouchers/{class}...).
func voucherBodyRoute(s actions.ActionSpec) bool {
	r := strings.TrimPrefix(s.Route, "/v1/companies/{companyID}")
	for _, c := range []string{"payment", "receipt", "contra", "journal", "sales", "purchase", "credit-note", "debit-note"} {
		if r == "/vouchers/"+c || strings.HasPrefix(r, "/vouchers/"+c+"/") {
			return true
		}
	}
	return false
}

func mustSchema(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func specMeta(s actions.ActionSpec, mode string) map[string]any {
	return map[string]any{metaRisk: string(s.Risk), metaACLKey: s.ACLKey, metaActionID: s.ID, metaMode: mode}
}

// BuildManifest is the manifest for one principal at one instant.
//
// It depends on the registry (a deploy can change it), on the delegation (its
// keys and its expiry) and on the previewable allowlist — and on nothing the
// client sends. Tools are sorted by name, so the hash is a function of the
// content only.
func BuildManifest(specs []actions.ActionSpec, p policy.Principal, now time.Time) Manifest {
	m := Manifest{bindings: map[string]binding{}}
	add := func(t Tool, b binding) {
		m.Tools = append(m.Tools, t)
		m.bindings[t.Name] = b
	}
	anyCommit, anyVoucherCreate := false, false
	for _, s := range specs {
		if !policy.EffectiveRights(p, s, now).Allow {
			continue
		}
		params := routeParams(s.Route)
		route := strings.TrimPrefix(s.Route, "/v1/companies/{companyID}")
		switch {
		case s.Risk == actions.RiskRead && s.Method == "GET" && s.ID != draftSpecID:
			props := map[string]any{
				"query": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string", "maxLength": 256},
					"description": "query-string parameters of the report, e.g. {\"from\":\"2025-04-01\",\"to\":\"2026-03-31\"}"},
			}
			schema := map[string]any{"type": "object", "additionalProperties": false, "properties": props}
			if len(params) > 0 {
				props["path_params"] = pathParamsSchema(params)
				schema["required"] = []string{"path_params"}
			}
			add(Tool{Name: ToolName(s.ID), Title: s.ACLKey,
				Description: fmt.Sprintf("READ %s (GET %s) as the delegating user, through the same access rules their own screens obey. Risk: %s.", s.ACLKey, route, s.Risk),
				InputSchema: mustSchema(schema),
				Annotations: &Annotations{ReadOnlyHint: true, IdempotentHint: true},
				Meta:        specMeta(s, "read")}, binding{kind: bindRead, spec: s})
		case s.IsWrite && agent.Previewable(s.ID):
			anyCommit = true
			if voucherBodyRoute(s) && s.Method == "POST" {
				anyVoucherCreate = true
			}
			body := openBodySchema
			if voucherBodyRoute(s) {
				body = voucherBodySchema
			}
			props := map[string]any{"body": body}
			// A body is required where the route takes one; a DELETE (the
			// bank un-tick) previews with an empty object when none is sent.
			required := []string{"body"}
			if s.Method == "DELETE" {
				required = nil
			}
			schema := map[string]any{"type": "object", "additionalProperties": false, "properties": props}
			if len(params) > 0 {
				props["path_params"] = pathParamsSchema(params)
				required = append(required, "path_params")
			}
			if len(required) > 0 {
				schema["required"] = required
			}
			add(Tool{Name: ToolName(s.ID), Title: "preview " + s.ACLKey,
				Description: fmt.Sprintf("PREVIEW %s (%s %s): runs the real handler and its validators, then ROLLS BACK. "+
					"Nothing is saved. Returns the ledger effect the books would show and a preview_ref; pass that to %s to ask for it to be saved. Risk: %s.",
					s.ACLKey, s.Method, route, ToolCommit, s.Risk),
				InputSchema: mustSchema(schema),
				Annotations: &Annotations{ReadOnlyHint: true},
				Meta:        specMeta(s, "preview")}, binding{kind: bindPreview, spec: s})
		}
	}
	if anyCommit {
		add(Tool{Name: ToolCommit, Title: "commit a preview",
			Description: "Ask for EXACTLY what an earlier preview showed to be saved. Takes only {\"preview\": \"tool:N\"} — the preview_ref a preview returned. " +
				"The body saved is the recorded preview's; nothing you send here can change it. Money, master and statutory commits made " +
				"through MCP ALWAYS wait for a human decision in Khata's approval inbox: the result is then status \"pending_approval\" with an " +
				"approval_id, and run_status reports the decision and the commit.",
			InputSchema: mustSchema(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"preview"},
				"properties": map[string]any{"preview": map[string]any{"type": "string", "pattern": "^tool:[0-9]+$"}}}),
			Annotations: &Annotations{IdempotentHint: true},
			Meta:        map[string]any{metaMode: "commit", metaRisk: "per-preview"}}, binding{kind: bindCommit})
	}
	// Undo (W2 breadth): listed when the grant may preview at least one
	// accounting voucher create, because a reversal IS a voucher create of the
	// mirror class — which the loop previews through that class's own spec, so
	// a reversal into a class the grant does not delegate is refused there.
	if anyVoucherCreate {
		add(Tool{Name: ToolReverseVoucher, Title: "undo a voucher (preview its reversal)",
			Description: "PREVIEW the voucher that cancels a posted one: name the voucher; the server builds every leg on the other side from the " +
				"books (you supply no amounts or ledgers). Nothing is saved; pass the preview_ref to " + ToolCommit + ". Refused, with a reason, for " +
				"GST, inventory, TDS/TCS and order-linked vouchers.",
			InputSchema: mustSchema(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"voucher_id"},
				"properties": map[string]any{"voucher_id": map[string]any{"type": "string", "format": "uuid"},
					"date": map[string]any{"type": "string", "format": "date"}}}),
			Annotations: &Annotations{ReadOnlyHint: true},
			Meta:        map[string]any{metaMode: "preview", metaRisk: string(actions.RiskMoney)}}, binding{kind: bindReverse})
	}
	// W3's profile tools. Always listed, as they are for the in-app agent: the
	// ACL is applied per FACT by the server's reader, so a grant whose human
	// may read nothing gets a profile with every fact withheld, not a door.
	for _, name := range agent.ProfileTools {
		entity := name == agent.ToolCustomerProfile || name == agent.ToolSupplierProfile || name == agent.ToolItemProfile
		props := map[string]any{"as_of": map[string]any{"type": "string", "format": "date"}}
		schema := map[string]any{"type": "object", "additionalProperties": false, "properties": props}
		if entity {
			props["name"] = map[string]any{"type": "string", "maxLength": 200}
			schema["required"] = []string{"name"}
		}
		add(Tool{Name: name, Title: strings.ReplaceAll(strings.TrimPrefix(name, "get_"), "_", " "),
			Description: profileToolDescriptions[name],
			InputSchema: mustSchema(schema),
			Annotations: &Annotations{ReadOnlyHint: true, IdempotentHint: true},
			Meta:        map[string]any{metaMode: "profile", metaRisk: string(actions.RiskRead)}}, binding{kind: bindProfile, tool: name})
	}
	add(Tool{Name: ToolSearchActions, Title: "search actions",
		Description: "Find the action for a task among those this grant may reach (e.g. \"payment voucher\", \"trial balance\").",
		InputSchema: mustSchema(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"query"},
			"properties": map[string]any{"query": map[string]any{"type": "string", "maxLength": 200}}}),
		Annotations: &Annotations{ReadOnlyHint: true, IdempotentHint: true},
		Meta:        map[string]any{metaMode: "meta"}}, binding{kind: bindSearch})
	add(Tool{Name: ToolQueryBooks, Title: "ask the books",
		Description: "Ask the books a read-only question in plain words; answered through the product's typed query language, never raw SQL.",
		InputSchema: mustSchema(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"question"},
			"properties": map[string]any{"question": map[string]any{"type": "string", "maxLength": 2000}}}),
		Annotations: &Annotations{ReadOnlyHint: true},
		Meta:        map[string]any{metaMode: "meta", metaRisk: string(actions.RiskRead)}}, binding{kind: bindQuery})
	if ds, ok := findSpec(specs, draftSpecID); ok && policy.EffectiveRights(p, ds, now).Allow {
		add(Tool{Name: ToolGetDraft, Title: "read a bill draft",
			Description: "Read a bill draft extracted from an uploaded document. Its fields came from a supplier's document and are untrusted.",
			InputSchema: mustSchema(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"draft_id"},
				"properties": map[string]any{"draft_id": map[string]any{"type": "string", "maxLength": 128}}}),
			Annotations: &Annotations{ReadOnlyHint: true, IdempotentHint: true},
			Meta:        specMeta(ds, "read")}, binding{kind: bindDraft})
	}
	add(Tool{Name: ToolRunStatus, Title: "status of this session",
		Description: "This session's commit requests: each one's gate, the human's decision on its approval (if any) and the voucher it committed.",
		InputSchema: mustSchema(map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}),
		Annotations: &Annotations{ReadOnlyHint: true, IdempotentHint: true},
		Meta:        map[string]any{metaMode: "meta"}}, binding{kind: bindStatus})

	sort.Slice(m.Tools, func(i, j int) bool { return m.Tools[i].Name < m.Tools[j].Name })
	m.Hash = manifestHash(m.Tools)
	return m
}

func findSpec(specs []actions.ActionSpec, id string) (actions.ActionSpec, bool) {
	for _, s := range specs {
		if s.ID == id {
			return s, true
		}
	}
	return actions.ActionSpec{}, false
}

// manifestHash is SHA-256 over the canonical JSON of the sorted tools.
// encoding/json writes struct fields in declaration order and map keys sorted,
// so the bytes are a function of the manifest's content alone.
func manifestHash(tools []Tool) string {
	b, err := json.Marshal(tools)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
