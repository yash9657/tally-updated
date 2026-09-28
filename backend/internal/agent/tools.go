package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/zeebo/blake3"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/durable"
	"github.com/khata/cored/internal/intentlog"
	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/policy"
)

// hashText is BLAKE3 hex — the trajectory package's content-fingerprint
// primitive — for text that must be identifiable in the intent log without
// being stored there.
func hashText(s string) string {
	if s == "" {
		return ""
	}
	sum := blake3.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func inputDigests(in []Input) []map[string]string {
	out := make([]map[string]string, 0, len(in))
	for _, i := range in {
		out = append(out, map[string]string{"label_hash": hashText(i.Label), "provenance": string(i.Source), "text_hash": hashText(i.Text)})
	}
	return out
}

// RequestHash is the cassette key of one decide call: SHA-256 (the
// trajectory.PromptHash primitive, see its comment on why not BLAKE3) over the
// full rendered conversation — Static AND Dynamic, every role and content.
//
// ⛔ IT HASHES THE WHOLE CONVERSATION, NOT llm.StaticPromptHash. The static
// hash is identical for every call in the feature, so a cassette keyed on it
// would replay by ORDINAL — the property cassette.go's Entry.key forbids,
// because a broken agent that makes the same number of calls passes it.
func RequestHash(req *llm.Request) string {
	h := sha256.New()
	for _, m := range append(append([]llm.Message{}, req.Static...), req.Dynamic...) {
		fmt.Fprintf(h, "%s\x00%s\x00", m.Role, m.Content)
	}
	for _, t := range req.Tools {
		var buf bytes.Buffer
		_ = json.Compact(&buf, t.Parameters)
		fmt.Fprintf(h, "tool\x00%s\x00%s\x00", t.Name, buf.String())
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ---------------------------------------------------------------------------
// Specs the agent may reach
// ---------------------------------------------------------------------------

// previewable is the set of ActionSpecs whose handlers are known to do their
// whole write in ONE RunInTx — the property ModePreview's rollback depends on
// (platform/db TxHook) — and whose effect internal/server's readTxEffect can
// read back from that transaction (postings, versions, stock movements,
// master_audit rows, order heads, bank ticks).
//
// ⛔ AN ALLOWLIST, NOT A PROPERTY OF THE REGISTRY. "This handler writes only
// through one hooked transaction" is not derivable from a route; a handler that
// also wrote through Raw(), or called insightd, would commit or send during a
// preview. Each entry is a claim somebody checked, and the value says which
// handler it was checked against. Wave 1 had the eight accounting creates; wave
// 2 (D121 "Wave 2 breadth") adds each family below after reading its handler.
//
// NOT HERE, AND WHY (the owed list is D121's):
//   - a ledger ALTER: the product has no human path for one (only the opening
//     balance, whose PUT no screen calls), and policy.Structural refuses the
//     agent a door no person uses;
//   - delete of anything: structural;
//   - post.drafts._draftid.accepted and the other insightd proxies: they call
//     another service, which no rollback can take back;
//   - groups, units, godowns, price lists, BOMs, budgets, cost centres: each
//     is one more handler to read, and none is on PLAN-AGENTIC W2's list.
var previewable = map[string]bool{
	// The eight accounting voucher creates: postVoucher -> ledger.PostVoucher,
	// one post() transaction.
	"post.vouchers.sales":       true,
	"post.vouchers.purchase":    true,
	"post.vouchers.receipt":     true,
	"post.vouchers.payment":     true,
	"post.vouchers.contra":      true,
	"post.vouchers.journal":     true,
	"post.vouchers.credit-note": true,
	"post.vouchers.debit-note":  true,
	// Their ALTERATIONS: alterVoucher -> ledger.AlterVoucher -> the same post()
	// transaction with op ALTER — a new hash-chained version that supersedes
	// the head, never an edit of it. (A FrozenError's reconciliation-queue row
	// is written in a SEPARATE transaction only on refusal, when nothing
	// commits anyway; under a preview it is rolled back like the rest.)
	"put.vouchers.sales._voucherid":       true,
	"put.vouchers.purchase._voucherid":    true,
	"put.vouchers.receipt._voucherid":     true,
	"put.vouchers.payment._voucherid":     true,
	"put.vouchers.contra._voucherid":      true,
	"put.vouchers.journal._voucherid":     true,
	"put.vouchers.credit-note._voucherid": true,
	"put.vouchers.debit-note._voucherid":  true,
	// The four stock vouchers, create and alter: the same two handlers; the
	// effect is stock_movement rows, not postings.
	"post.vouchers.delivery-note":            true,
	"post.vouchers.receipt-note":             true,
	"post.vouchers.stock-journal":            true,
	"post.vouchers.physical-stock":           true,
	"put.vouchers.delivery-note._voucherid":  true,
	"put.vouchers.receipt-note._voucherid":   true,
	"put.vouchers.stock-journal._voucherid":  true,
	"put.vouchers.physical-stock._voucherid": true,
	// Masters: handleCreateLedger -> masters.CreateLedger (one RunInTx, a
	// master_audit CREATE row; an agent's ledger is BORN BLOCKED, 0095);
	// handleCreateStockItem -> masters.CreateStockItem (one RunInTx, audit
	// row); the valuation-method setter (one RunInTx, audit ALTER row).
	"post.ledgers":     true,
	"post.stock-items": true,
	"put.stock-items._stockitemid.valuation-method": true,
	// Orders: postOrder -> orders.Create and putOrderClose -> orders.Close,
	// one RunInTx each.
	"post.sales-orders":                  true,
	"post.purchase-orders":               true,
	"put.sales-orders._orderid.close":    true,
	"put.purchase-orders._orderid.close": true,
	// Bank reconciliation, tick and un-tick: banking.SetBankDate /
	// SetBankDateFromStatementLine / ClearBankDate, one RunInTx each. The
	// before-image is read ahead of the handler (the un-tick deletes it).
	"put.bank-reconciliation._ledgerid._voucherid._legno":    true,
	"delete.bank-reconciliation._ledgerid._voucherid._legno": true,
}

// Previewable reports whether spec may be previewed (and therefore committed)
// by the agent today.
func Previewable(id string) bool { return previewable[id] }

type searchArgs struct {
	Query string `json:"query"`
}

type searchHit struct {
	ActionID string `json:"action_id"`
	ACLKey   string `json:"acl_key"`
	Risk     string `json:"risk"`
	Method   string `json:"method"`
	Route    string `json:"route"`
	Commit   bool   `json:"committable"`
}

// search is actions.search: the registry's deterministic matcher, filtered to
// what THIS run may reach, with a few Tally words mapped onto route words.
func (l *loop) search(q string) []searchHit {
	now := l.e.now()
	p := l.run.Principal()
	var words []string
	for _, w := range strings.Fields(strings.ToLower(q)) {
		switch w {
		case "voucher", "vouchers", "entry", "a", "an", "the", "for", "to", "of":
			continue
		}
		words = append(words, w)
	}
	var hits []searchHit
	for _, s := range actions.Specs {
		if !policy.EffectiveRights(p, s, now).Allow {
			continue
		}
		hay := strings.ToLower(s.ID + " " + s.ACLKey + " " + s.Label)
		match := len(words) > 0
		for _, w := range words {
			if !strings.Contains(hay, w) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		hits = append(hits, searchHit{ActionID: s.ID, ACLKey: s.ACLKey, Risk: string(s.Risk),
			Method: s.Method, Route: strings.TrimPrefix(s.Route, "/v1/companies/{companyID}"), Commit: previewable[s.ID]})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Commit != hits[j].Commit {
			return hits[i].Commit
		}
		return hits[i].ActionID < hits[j].ActionID
	})
	if len(hits) > 10 {
		hits = hits[:10]
	}
	return hits
}

// ordinalParams are the path parameters that are an ordinal, not an id.
var ordinalParams = map[string]bool{"legNo": true}

// concretePath substitutes path params into a spec's route.
//
// ⛔ EVERY PARAM MUST BE A UUID (or, for ordinalParams, digits). A model-supplied "../members/x" or a value
// carrying "?" or "/" would otherwise re-route the request to a different
// door than the spec it claims — and although the router re-derives the ACL
// key and refuses a mismatch, the cheapest defence is to never build such a
// path. Every {param} in this registry is an id.
func concretePath(spec actions.ActionSpec, companyID uuid.UUID, params map[string]string) (string, error) {
	route := strings.Replace(spec.Route, "{companyID}", companyID.String(), 1)
	for {
		i := strings.IndexByte(route, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(route[i:], '}')
		if j < 0 {
			return "", fmt.Errorf("malformed route %q", spec.Route)
		}
		name := route[i+1 : i+j]
		v, ok := params[name]
		if !ok {
			return "", fmt.Errorf("path parameter %q is required for %s", name, spec.ID)
		}
		if ordinalParams[name] {
			// The one non-id parameter an agent-reachable route carries: a
			// bank-reconciliation leg's ordinal. Digits only, so it can no
			// more re-route a request than a uuid can.
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 9999 || strconv.Itoa(n) != v {
				return "", fmt.Errorf("path parameter %q must be a small non-negative integer", name)
			}
			route = route[:i] + v + route[i+j+1:]
			continue
		}
		id, err := uuid.Parse(v)
		if err != nil {
			return "", fmt.Errorf("path parameter %q must be a uuid", name)
		}
		route = route[:i] + url.PathEscape(id.String()) + route[i+j+1:]
	}
	return route, nil
}

func (l *loop) caller(webauthn bool) Caller {
	return Caller{Principal: l.run.Principal(), Subject: l.run.Subject, IssuedAt: l.run.CreatedAt, WebAuthn: webauthn}
}

func toolErr(tool, msg string) toolOut { return toolOut{Tool: tool, OK: false, Error: msg} }

// trimBody bounds what a read hands back to the model.
func trimBody(b json.RawMessage) json.RawMessage {
	const max = 16 << 10
	if len(b) <= max {
		return b
	}
	out, _ := json.Marshal(map[string]any{"truncated": true, "bytes": len(b), "head": string(b[:max])})
	return out
}

// ---------------------------------------------------------------------------
// tool:k
// ---------------------------------------------------------------------------

func (l *loop) toolStep(k int) durable.Step {
	name := stepName("tool", k)
	return durable.Step{Name: name, Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
		d, err := l.decision(prev, k)
		if err != nil {
			return nil, err
		}
		out, err := l.runTool(ctx, prev, d)
		if err != nil {
			return nil, err // transient: not recorded, retried
		}
		kind := intentlog.KindToolCall
		payload := map[string]any{"k": k, "tool": d.Tool, "ok": out.OK, "args_hash": hashText(string(d.Args))}
		action := ""
		if out.Preview != nil {
			kind, action = intentlog.KindPreview, out.Preview.ActionID
			payload["status"] = out.Preview.Status
			if out.Preview.Effect != nil {
				payload["debit_paise"] = out.Preview.Effect.DebitPaise
				payload["credit_paise"] = out.Preview.Effect.CreditPaise
				payload["postings"] = len(out.Preview.Effect.Postings)
			}
		}
		if out.VerifierError {
			kind = intentlog.KindVerifierError
		}
		spec, _ := actions.ByID(action)
		if err := l.e.appendIntentTx(ctx, l.run, intentlog.Event{Kind: kind, ActorKind: intentlog.ActorAgent,
			ActionID: action, ACLKey: spec.ACLKey, Payload: payload}); err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}}
}

func strictDecode(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// runTool executes one non-commit tool. A returned error is TRANSIENT (the
// step is retried); a tool-level failure is a recorded toolOut the model sees.
func (l *loop) runTool(ctx context.Context, prev map[string]json.RawMessage, d ModelDecision) (toolOut, error) {
	switch d.Tool {
	case ToolSearch:
		var a searchArgs
		if err := strictDecode(d.Args, &a); err != nil {
			return toolErr(d.Tool, "args: "+err.Error()), nil
		}
		res, _ := json.Marshal(map[string]any{"actions": l.search(a.Query)})
		return toolOut{Tool: d.Tool, OK: true, Result: res}, nil

	case ToolPreview:
		return l.preview(ctx, prev, d)

	case ToolReverse:
		return l.reverse(ctx, prev, d)

	case ToolCustomerProfile, ToolSupplierProfile, ToolItemProfile, ToolRecurring, ToolTenantProfile:
		return l.profile(ctx, d)

	case ToolRead:
		// actions.read: any RiskRead ActionSpec — a report, a master list —
		// through the router as the human. ⛔ It is the ONLY generic door the
		// agent has, and it is a read by construction: a spec whose Risk is
		// anything but read (a print, a governance read like backup) is
		// refused here before the router is asked.
		var a struct {
			ActionID   string            `json:"action_id"`
			PathParams map[string]string `json:"path_params,omitempty"`
			Query      map[string]string `json:"query,omitempty"`
		}
		if err := strictDecode(d.Args, &a); err != nil {
			return toolErr(d.Tool, "args: "+err.Error()), nil
		}
		spec, ok := actions.ByID(a.ActionID)
		if !ok {
			return toolErr(d.Tool, fmt.Sprintf("no action %q; use actions.search", a.ActionID)), nil
		}
		if (spec.Risk != actions.RiskRead && spec.Risk != actions.RiskExport) || spec.Method != "GET" {
			return toolErr(d.Tool, fmt.Sprintf("%s is not a read (risk %s)", spec.ID, spec.Risk)), nil
		}
		if r := policy.EffectiveRights(l.run.Principal(), spec, l.e.now()); !r.Allow {
			return toolErr(d.Tool, r.Reason), nil
		}
		path, err := concretePath(spec, l.run.CompanyID, a.PathParams)
		if err != nil {
			return toolErr(d.Tool, err.Error()), nil
		}
		q := url.Values{}
		for k, v := range a.Query {
			if len(k) > 64 || len(v) > 256 || strings.ContainsAny(k, "&=?#/") {
				return toolErr(d.Tool, "query parameters must be short plain values"), nil
			}
			q.Set(k, v)
		}
		res, err := l.e.Dispatch.Dispatch(ctx, l.caller(l.run.Delegation.StepUp), Request{Mode: ModeRead, Spec: spec, Path: path,
			Query: q.Encode(), RunID: l.run.ID})
		if err != nil {
			return toolOut{}, err
		}
		if spec.Risk == actions.RiskExport {
			// ⛔ AN EXPORT IS PRODUCED, NOT READ. A print is the document a
			// person would hand over — a voucher's narration, a party's
			// address — and its bytes are useless to the planner and a
			// channel for untrusted text into its context. So the model is
			// told that the document exists (status, type, size, digest) and
			// never what it says; with nothing of its content in the
			// context, the result is labelled books. Delivering the file to
			// a person (WhatsApp, email) is W6's channel, not this tool.
			return exportOut(d.Tool, res), nil
		}
		// ⛔ THE LABEL IS THE SPEC'S, NOT "books" (readsource.go): a bank
		// statement or a day book read is untrusted text reaching the model.
		return toolOut{Tool: d.Tool, OK: res.OK(), Result: trimBody(res.Body), Sources: []policy.Source{readSourceFor(spec)},
			Error: map[bool]string{false: fmt.Sprintf("status %d", res.Status)}[res.OK()]}, nil

	case ToolStatus, ToolListPending:
		approvals, err := l.e.ListApprovals(ctx, l.run.CompanyID, map[string]string{ToolStatus: "all", ToolListPending: "pending"}[d.Tool], l.run.ID)
		if err != nil {
			return toolOut{}, err
		}
		type view struct {
			ActionID string `json:"action_id"`
			State    string `json:"state"`
			Step     string `json:"step"`
			Deadline string `json:"deadline_at"`
		}
		var vs []view
		for _, a := range approvals {
			vs = append(vs, view{ActionID: a.ActionID, State: a.State, Step: a.StepKey, Deadline: a.DeadlineAt.Format(time.RFC3339)})
		}
		res, _ := json.Marshal(map[string]any{"approvals": vs})
		return toolOut{Tool: d.Tool, OK: true, Result: res}, nil

	case ToolQueryBooks:
		var a struct {
			Question string `json:"question"`
		}
		if err := strictDecode(d.Args, &a); err != nil || strings.TrimSpace(a.Question) == "" {
			return toolErr(d.Tool, "args: a non-empty question is required"), nil
		}
		spec, ok := actions.ByID("post.query")
		if !ok {
			return toolErr(d.Tool, "the query action is not in the registry"), nil
		}
		path, _ := concretePath(spec, l.run.CompanyID, nil)
		body, _ := json.Marshal(map[string]string{"nl": a.Question})
		res, err := l.e.Dispatch.Dispatch(ctx, l.caller(l.run.Delegation.StepUp), Request{Mode: ModeRead, Spec: spec, Path: path, Body: body, RunID: l.run.ID})
		if err != nil {
			return toolOut{}, err
		}
		// "books" is honest HERE and only here among the generic reads: the
		// query whitelist never PROJECTS a prose column — bank_lines.description
		// is filter-only (query/whitelist.go) — so what comes back is master
		// names and computed figures.
		return toolOut{Tool: d.Tool, OK: res.OK(), Result: trimBody(res.Body), Sources: []policy.Source{policy.SourceBooks},
			Error: map[bool]string{false: fmt.Sprintf("status %d", res.Status)}[res.OK()]}, nil

	case ToolGetDraft:
		var a struct {
			DraftID string `json:"draft_id"`
		}
		if err := strictDecode(d.Args, &a); err != nil {
			return toolErr(d.Tool, "args: "+err.Error()), nil
		}
		spec, ok := actions.ByID("get.drafts._draftid")
		if !ok {
			return toolErr(d.Tool, "the draft action is not in the registry"), nil
		}
		// Draft ids are insightd's; they are not uuids, so the uuid rule in
		// concretePath does not apply — but the same path-safety one does.
		if a.DraftID == "" || strings.ContainsAny(a.DraftID, "/?#%.\\") || len(a.DraftID) > 128 {
			return toolErr(d.Tool, "draft_id is not a valid id"), nil
		}
		path := strings.Replace(strings.Replace(spec.Route, "{companyID}", l.run.CompanyID.String(), 1), "{draftID}", url.PathEscape(a.DraftID), 1)
		res, err := l.e.Dispatch.Dispatch(ctx, l.caller(l.run.Delegation.StepUp), Request{Mode: ModeRead, Spec: spec, Path: path, RunID: l.run.ID})
		if err != nil {
			return toolOut{}, err
		}
		// ⛔ A DRAFT IS DOCUMENT-DERIVED: its fields were extracted from a
		// supplier's bill, which is untrusted text, whatever the OCR did to it.
		return toolOut{Tool: d.Tool, OK: res.OK(), Result: trimBody(res.Body), Sources: []policy.Source{policy.SourceDocument},
			Error: map[bool]string{false: fmt.Sprintf("status %d", res.Status)}[res.OK()]}, nil
	}
	return toolErr(d.Tool, "not a tool"), nil
}

type previewArgs struct {
	ActionID   string            `json:"action_id"`
	PathParams map[string]string `json:"path_params,omitempty"`
	Body       json.RawMessage   `json:"body"`
}

// preview is actions.preview: the real handler, every transaction rolled back.
func (l *loop) preview(ctx context.Context, prev map[string]json.RawMessage, d ModelDecision) (toolOut, error) {
	var a previewArgs
	if err := strictDecode(d.Args, &a); err != nil {
		return toolErr(d.Tool, "args: "+err.Error()), nil
	}
	spec, ok := actions.ByID(a.ActionID)
	if !ok {
		return toolErr(d.Tool, fmt.Sprintf("no action %q; use actions.search", a.ActionID)), nil
	}
	if r := policy.EffectiveRights(l.run.Principal(), spec, l.e.now()); !r.Allow {
		return toolErr(d.Tool, r.Reason), nil
	}
	if !previewable[spec.ID] {
		return toolErr(d.Tool, fmt.Sprintf("%s cannot be previewed by the agent yet", spec.ID)), nil
	}
	// ⛔ THE BOUNDED RETRY. A verifier refusal is fed back once; a second
	// refusal of the same action ends the run. Counted from the RECORDED
	// steps, so a restart cannot reset the budget.
	failures := 0
	for name, raw := range prev {
		if kind, _ := splitStep(name); kind != "tool" {
			continue
		}
		if t, err := decode[toolOut](raw); err == nil && t.VerifierError && t.Preview != nil && t.Preview.ActionID == spec.ID {
			failures++
		}
	}
	if failures >= 2 {
		out := toolErr(d.Tool, fmt.Sprintf("the validators refused %s twice; not retrying", spec.ID))
		out.Terminal = true
		return out, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(a.Body, &obj); err != nil {
		return toolErr(d.Tool, "body must be a JSON object"), nil
	}
	path, err := concretePath(spec, l.run.CompanyID, a.PathParams)
	if err != nil {
		return toolErr(d.Tool, err.Error()), nil
	}
	res, err := l.e.Dispatch.Dispatch(ctx, l.caller(l.run.Delegation.StepUp), Request{Mode: ModePreview, Spec: spec, Path: path,
		Body: a.Body, RunID: l.run.ID, Params: a.PathParams})
	if err != nil {
		return toolOut{}, err
	}
	rec := &previewRecord{ActionID: spec.ID, Params: a.PathParams, Body: a.Body, Status: res.Status, Effect: res.Effect}
	out := toolOut{Tool: d.Tool, OK: res.OK(), Preview: rec, Sources: []policy.Source{policy.SourceBooks}}
	if res.OK() {
		v, _ := json.Marshal(map[string]any{"status": res.Status, "effect": res.Effect, "response": trimBody(res.Body)})
		out.Result = v
		if res.Effect.Empty() {
			out.OK = false
			out.Error = "the preview produced no effect on the books"
		}
		return out, nil
	}
	// 4xx from the handler or the PDP. 422 is the validators speaking.
	out.VerifierError = res.Status == 422 || res.Status == 400
	out.Error = verifierMessage(res.Body)
	out.Result, _ = json.Marshal(map[string]any{"status": res.Status, "error": out.Error})
	return out, nil
}

func verifierMessage(body json.RawMessage) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return truncate(e.Error, 500)
	}
	return truncate(string(body), 500)
}

// ---------------------------------------------------------------------------
// gate:k — the policy decision for actions.commit
// ---------------------------------------------------------------------------

type commitArgs struct {
	Preview string `json:"preview"`
}

func (l *loop) gateStep(k int) durable.Step {
	name := stepName("gate", k)
	return durable.Step{Name: name, Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
		d, err := l.decision(prev, k)
		if err != nil {
			return nil, err
		}
		out, err := l.gate(ctx, prev, k, d)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}}
}

func refuse(out gateOut, reason string) gateOut {
	out.Gate = policy.Gate{Mode: policy.ModeRefuse, Reasons: []string{reason}}
	out.Error = reason
	return out
}

func (l *loop) gate(ctx context.Context, prev map[string]json.RawMessage, k int, d ModelDecision) (gateOut, error) {
	var a commitArgs
	out := gateOut{}
	if err := strictDecode(d.Args, &a); err != nil {
		// ⛔ A COMMIT THAT CARRIES ANYTHING BUT A PREVIEW REFERENCE — a body,
		// an "approved": true, an action id — is refused here, by the strict
		// decode. The model cannot hand the gate a value to trust.
		return refuse(out, "actions.commit takes exactly {\"preview\": \"tool:N\"}"), nil
	}
	out.PreviewRef = a.Preview
	kind, j := splitStep(a.Preview)
	if kind != "tool" || j < 0 || j >= k {
		return refuse(out, "preview must name an earlier step, e.g. tool:2"), nil
	}
	t, err := decode[toolOut](prev[a.Preview])
	if err != nil || t.Preview == nil {
		return refuse(out, a.Preview+" is not a preview"), nil
	}
	if !t.OK || t.Preview.Effect == nil {
		return refuse(out, a.Preview+" did not succeed; only a successful preview can be committed"), nil
	}
	spec, ok := actions.ByID(t.Preview.ActionID)
	if !ok {
		return refuse(out, "the previewed action is no longer in the registry"), nil
	}
	out.ActionID, out.ACLKey = spec.ID, spec.ACLKey
	// ⛔ ONE COMMIT PER PREVIEW. A second actions.commit naming the same
	// preview would post the same voucher twice under two idempotency keys.
	for name, raw := range prev {
		if kind, _ := splitStep(name); kind != "gate" {
			continue
		}
		if g, err := decode[gateOut](raw); err == nil && g.PreviewRef == a.Preview && g.Gate.Mode != policy.ModeRefuse {
			return refuse(out, a.Preview+" was already submitted for commit at "+name), nil
		}
	}

	// The amount is the handler's own, in paise: the preview's debit total,
	// or for a write that posts no legs (an order, a stock voucher) the value
	// Effect.ValuePaise reads from its lines. nil when neither measures it,
	// and policy.Decide then holds a run with a delegated ceiling for a human.
	var amount *int64
	if v, ok := t.Preview.Effect.ValuePaise(); ok {
		amount = &v
	}
	out.AmountPaise = amount
	now := l.e.now()
	prov := policy.Provenance{ContextSources: l.contextSources(prev), Fields: l.fieldSources(t.Preview.Body)}

	var gate policy.Gate
	var approvalID uuid.UUID
	var deadline time.Time
	cctx := db.WithCompany(ctx, l.run.CompanyID)
	err = l.e.Pool.RunInTx(cctx, func(ctx context.Context, tx pgx.Tx) error {
		lvl, err := policy.LookupLevel(ctx, tx, l.run.CompanyID, l.run.CreatedBy, spec.ACLKey, amount)
		if err != nil {
			return err
		}
		gate = policy.Decide(policy.GateInput{Principal: l.run.Principal(), Spec: spec, Level: lvl,
			Amount: amount, Provenance: prov, Now: now})
		if _, err := intentlog.Append(ctx, tx, l.run.CompanyID, intentlog.Event{
			RunID: l.run.ID, Kind: intentlog.KindPolicy, ActorKind: intentlog.ActorAgent, ActorID: l.run.AgentID,
			OnBehalfOf: l.run.CreatedBy, ActionID: spec.ID, ACLKey: spec.ACLKey,
			Payload: map[string]any{"k": k, "mode": gate.Mode, "level": gate.Level, "reasons": gate.Reasons,
				"amount_paise": amount, "context_sources": prov.ContextSources}}, now); err != nil {
			return err
		}
		if gate.Mode != policy.ModeApproval {
			return nil
		}
		// The approval row. Idempotent per (run, step_key): a re-run of this
		// step after a crash finds the row it already wrote.
		stepKey := stepName("gate", k)
		err = tx.QueryRow(ctx, `SELECT id, deadline_at FROM agent_approval WHERE run_id = $1 AND step_key = $2`,
			l.run.ID, stepKey).Scan(&approvalID, &deadline)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		approvalID, err = uuid.NewV7()
		if err != nil {
			return err
		}
		deadline = now.Add(l.e.window())
		args, _ := json.Marshal(map[string]any{"action_id": spec.ID, "path_params": t.Preview.Params, "body": t.Preview.Body})
		preview, _ := json.Marshal(map[string]any{"status": t.Preview.Status, "effect": t.Preview.Effect})
		reasons, _ := json.Marshal(gate.Reasons)
		provJSON, _ := json.Marshal(prov)
		if _, err := tx.Exec(ctx,
			`INSERT INTO agent_approval (id, company_id, run_id, step_key, action_id, acl_key, risk, args, preview,
			                             amount_paise, reasons, provenance, on_behalf_of, requested_at, deadline_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			approvalID, l.run.CompanyID, l.run.ID, stepKey, spec.ID, spec.ACLKey, string(spec.Risk), args, preview,
			amount, reasons, provJSON, l.run.CreatedBy, now, deadline); err != nil {
			return err
		}
		_, err = intentlog.Append(ctx, tx, l.run.CompanyID, intentlog.Event{
			RunID: l.run.ID, Kind: intentlog.KindApprovalRequested, ActorKind: intentlog.ActorAgent, ActorID: l.run.AgentID,
			OnBehalfOf: l.run.CreatedBy, ActionID: spec.ID, ACLKey: spec.ACLKey,
			Payload: map[string]any{"approval_id": approvalID, "deadline_at": deadline.Format(time.RFC3339),
				"body_hash": hashText(string(t.Preview.Body)), "amount_paise": amount}}, now)
		return err
	})
	if err != nil {
		return gateOut{}, err
	}
	out.Gate = gate
	if gate.Mode == policy.ModeApproval {
		out.ApprovalID = approvalID.String()
		out.DeadlineAt = &deadline
	}
	return out, nil
}

// fieldSources finds which string values of a body appear verbatim in an
// untrusted input. Informational only (the approver's diff view); the gate
// decides on context taint, not on this.
func (l *loop) fieldSources(body json.RawMessage) map[string]policy.Source {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return nil
	}
	out := map[string]policy.Source{}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				walk(strings.TrimPrefix(path+"."+k, "."), e)
			}
		case []any:
			for i, e := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), e)
			}
		case string:
			s := strings.ToLower(strings.TrimSpace(x))
			if len(s) < 4 {
				return
			}
			if strings.Contains(strings.ToLower(l.run.Instruction), s) {
				return // the user said it themselves
			}
			for _, in := range l.run.Inputs {
				if strings.Contains(strings.ToLower(in.Text), s) {
					out[path] = in.Source
					return
				}
			}
		}
	}
	walk("", v)
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// await:k — the parked step
// ---------------------------------------------------------------------------

func (l *loop) awaitStep(k int) durable.Step {
	name := stepName("await", k)
	return durable.Step{Name: name, Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
		g, err := decode[gateOut](prev[stepName("gate", k)])
		if err != nil {
			return nil, err
		}
		approvalID, err := uuid.Parse(g.ApprovalID)
		if err != nil {
			return nil, fmt.Errorf("agent: gate:%d recorded no approval id", k)
		}
		now := l.e.now()
		var a Approval
		cctx := db.WithCompany(ctx, l.run.CompanyID)
		err = l.e.Pool.RunInTx(cctx, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			a, err = loadApproval(ctx, tx, approvalID)
			if err != nil {
				return err
			}
			if a.Decision != nil || now.Before(a.DeadlineAt) {
				return nil
			}
			// ⛔ D120 F2 — EXPIRY IS WRITTEN, SO IT IS TERMINAL. The one-
			// decision UNIQUE then refuses any late approve, and the run's
			// next step (finish, expired) asks the model nothing.
			inserted, err := insertDecision(ctx, tx, l.run.CompanyID, approvalID, "expired", nil, "", nil, false, now, nil)
			if err != nil {
				return err
			}
			if inserted {
				if _, err := intentlog.Append(ctx, tx, l.run.CompanyID, intentlog.Event{
					RunID: l.run.ID, Kind: intentlog.KindApprovalDecided, ActorKind: intentlog.ActorSystem, ActorID: "poller",
					ActionID: a.ActionID, ACLKey: a.ACLKey,
					Payload: map[string]any{"approval_id": approvalID, "decision": "expired"}}, now); err != nil {
					return err
				}
			}
			a, err = loadApproval(ctx, tx, approvalID)
			return err
		})
		if err != nil {
			return nil, err
		}
		if a.Decision == nil {
			// Nothing recorded: the whole park mechanism (D120 §2).
			return nil, durable.Park(name, "awaiting a human decision on approval "+approvalID.String())
		}
		return json.Marshal(awaitOut{ApprovalID: approvalID.String(), Decision: a.Decision.Decision,
			Edited: len(a.Decision.EditedArgs) > 0, Note: a.Decision.Note})
	}}
}

// ---------------------------------------------------------------------------
// exec:k — the commit
// ---------------------------------------------------------------------------

// expectVersions is, per altered voucher, the head version an effect
// superseded — what a commit of that alteration must still find.
func expectVersions(e *Effect) map[uuid.UUID]int {
	if e == nil || len(e.Replaces) == 0 {
		return nil
	}
	m := make(map[uuid.UUID]int, len(e.Replaces))
	for _, r := range e.Replaces {
		m[r.VoucherID] = r.VersionNo
	}
	return m
}

// IdempotencyKey is the commit key for step k of a run. Derived, never
// random: a re-execution of exec:k after a crash must present the SAME key.
func IdempotencyKey(runID uuid.UUID, k int) string { return fmt.Sprintf("agent:%s:exec:%d", runID, k) }

func (l *loop) execStep(k int) durable.Step {
	name := stepName("exec", k)
	return durable.Step{Name: name, Run: func(ctx context.Context, prev map[string]json.RawMessage) (json.RawMessage, error) {
		g, err := decode[gateOut](prev[stepName("gate", k)])
		if err != nil {
			return nil, err
		}
		t, err := decode[toolOut](prev[g.PreviewRef])
		if err != nil || t.Preview == nil {
			return nil, fmt.Errorf("agent: exec:%d cannot find its preview %q", k, g.PreviewRef)
		}
		spec, ok := actions.ByID(t.Preview.ActionID)
		if !ok {
			return nil, fmt.Errorf("agent: exec:%d action %q vanished", k, t.Preview.ActionID)
		}
		body := t.Preview.Body
		// restated is the effect the committing human (or, autonomously, the
		// tenant's policy) was shown: the preview's, or the approver's
		// re-preview of their own edit. An ALTER commits only onto the
		// version that effect superseded.
		restated := t.Preview.Effect
		var approvalID uuid.UUID
		// An autonomous commit carries the delegating human's step-up; one
		// behind an approval carries the APPROVER's (set below), never the
		// starter's.
		webauthn := l.run.Delegation.StepUp
		var approver uuid.UUID
		if g.Gate.Mode == policy.ModeApproval {
			// ⛔ A COMMIT BEHIND AN APPROVAL RE-READS THE DECISION HERE, not
			// only in await:k. await recorded "approve"; this is the belt to
			// that brace — the commit refuses unless the database STILL says a
			// human approved this exact approval.
			approvalID, _ = uuid.Parse(g.ApprovalID)
			a, err := l.e.GetApproval(ctx, l.run.CompanyID, approvalID)
			if err != nil {
				return nil, err
			}
			if a.Decision == nil || a.Decision.Decision != "approve" || a.Decision.DecidedBy == nil {
				return json.Marshal(execOut{Status: 409, OK: false,
					Body: json.RawMessage(`{"error":"no human approval is recorded for this commit"}`)})
			}
			if len(a.Decision.EditedArgs) > 0 {
				body = a.Decision.EditedArgs
				if a.Decision.EditedPreview != nil && a.Decision.EditedPreview.Effect != nil {
					restated = a.Decision.EditedPreview.Effect
				}
			}
			webauthn = a.Decision.DecidedWebAuthn
			approver = *a.Decision.DecidedBy
		}
		path, err := concretePath(spec, l.run.CompanyID, t.Preview.Params)
		if err != nil {
			return nil, err
		}
		key := IdempotencyKey(l.run.ID, k)
		caller := l.caller(webauthn)
		caller.Approver = approver
		res, err := l.e.Dispatch.Dispatch(ctx, caller, Request{Mode: ModeCommit, Spec: spec, Path: path,
			Body: body, IdempotencyKey: key, ApprovalID: approvalID, RunID: l.run.ID,
			MaxDebitPaise: l.run.Delegation.MaxAmountPaise, Params: t.Preview.Params,
			ExpectVersions: expectVersions(restated)})
		if err != nil {
			return nil, err
		}
		out := execOut{Status: res.Status, OK: res.OK(), Replayed: res.Replayed, Body: trimBody(res.Body),
			ObjectKind: string(res.ObjectKind)}
		for _, id := range res.VoucherIDs {
			out.VoucherIDs = append(out.VoucherIDs, id.String())
		}
		for _, id := range res.ObjectIDs {
			out.ObjectIDs = append(out.ObjectIDs, id.String())
		}
		kind := intentlog.KindCommit
		if !res.OK() {
			kind = intentlog.KindCommitRefused
		}
		if err := l.e.appendIntentTx(ctx, l.run, intentlog.Event{Kind: kind, ActorKind: intentlog.ActorAgent,
			ActionID: spec.ID, ACLKey: spec.ACLKey, Payload: map[string]any{
				"k": k, "status": res.Status, "voucher_ids": out.VoucherIDs, "replayed": res.Replayed,
				"object_kind": out.ObjectKind, "object_ids": out.ObjectIDs,
				"idempotency_key": key, "approval_id": g.ApprovalID, "mode": g.Gate.Mode,
				"body_hash": hashText(string(body))}}); err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}}
}

// exportOut is what the model is told about a produced export.
func exportOut(tool string, res Result) toolOut {
	sum := sha256.Sum256(res.Body)
	meta := map[string]any{"status": res.Status, "bytes": len(res.Body), "sha256": hex.EncodeToString(sum[:])}
	if res.OK() {
		meta["produced"] = true
	}
	b, _ := json.Marshal(meta)
	out := toolOut{Tool: tool, OK: res.OK(), Result: b, Sources: []policy.Source{policy.SourceBooks}}
	if !res.OK() {
		out.Error = fmt.Sprintf("status %d", res.Status)
	}
	return out
}
