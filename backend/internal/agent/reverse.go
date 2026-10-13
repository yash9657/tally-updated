package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/policy"
)

// reverse.go is UNDO AS A GENERATED REVERSAL (PLAN-AGENTIC W2): actions.reverse
// reads a live voucher and previews the voucher that cancels it — every leg on
// the other side, the same ledgers, the same amounts — which actions.commit can
// then commit like any other preview, behind the same gate.
//
// ⛔ GO BUILDS THE REVERSAL, NOT THE MODEL. The model names a voucher; the body
// is derived here from the voucher as the books hold it (GET /vouchers/{id}),
// so "undo" cannot be a vehicle for a different amount or a different ledger.
// And the model never sees what was read: its result is the preview's effect
// (ledger names and paise), so the original's narration — stored text, whose
// author the server cannot vouch for — never reaches its context, and the tool
// is labelled books.
//
// ⛔ NOT A DELETE, AND NOT AN ALTER. A delete is structurally refused to an
// agent, and an alter would rewrite the voucher a person may already have
// printed, filed or paid against. A reversal leaves both vouchers in the books,
// each with its own number, which is how an accountant undoes a posted entry.
//
// WHAT IT REFUSES, AND WHY (D121 "Wave 2 breadth"; each is owed, not wrong):
//   - a voucher with GST lines: a GST invoice is reversed by a credit or debit
//     note that must reach GSTR-1's credit-note section with the original's
//     number and date — a document a person prepares, not a mirror of legs;
//   - a voucher with inventory, TDS, TCS or order fulfilments: each carries a
//     derivation (stock valuation, a statutory deduction, an order balance)
//     that swapping legs would contradict;
//   - a tombstoned voucher: the GET answers 404.

// reverseClass is the class whose voucher cancels one of class c.
//
// The four settlement classes reverse into themselves: a Payment with its legs
// swapped is still a payment-shaped voucher, and receipts, payments, contras
// and journals put the party on either side legitimately (ledger's
// validatePartyOrientation). The four invoice classes have an ORIENTATION —
// a Sales voucher that credits its party is refused — so each reverses into
// its mirror: Sales <-> Credit Note, Purchase <-> Debit Note.
func reverseClass(c string) (string, bool) {
	switch strings.ToLower(strings.ReplaceAll(c, " ", "-")) {
	case "payment":
		return "payment", true
	case "receipt":
		return "receipt", true
	case "contra":
		return "contra", true
	case "journal":
		return "journal", true
	case "sales":
		return "credit-note", true
	case "credit-note", "creditnote":
		return "sales", true
	case "purchase":
		return "debit-note", true
	case "debit-note", "debitnote":
		return "purchase", true
	}
	return "", false
}

// voucherView is the part of GET /vouchers/{id} a reversal is built from.
type voucherView struct {
	VoucherID       string            `json:"voucher_id"`
	VoucherNo       string            `json:"voucher_no"`
	VoucherDate     string            `json:"voucher_date"`
	Class           string            `json:"class"`
	Party           string            `json:"party"`
	PartyLedgerID   string            `json:"party_ledger_id"`
	Op              string            `json:"op"`
	Postings        []json.RawMessage `json:"postings"`
	BillAllocations []json.RawMessage `json:"bill_allocations"`
	Inventory       []json.RawMessage `json:"inventory"`
	CostAllocations []json.RawMessage `json:"cost_allocations"`
	Fulfilments     []json.RawMessage `json:"fulfilments"`
	GSTLines        []json.RawMessage `json:"gst_lines"`
	TDS             json.RawMessage   `json:"tds"`
	TCS             json.RawMessage   `json:"tcs"`
}

// ErrNotReversible is a voucher reversal.go will not mirror.
var ErrNotReversible = errors.New("agent: this voucher cannot be reversed by a generated voucher")

// flip swaps a row's dr_cr ("D" <-> "C"), keeping every other field verbatim
// (ledger, amount, and a foreign triple when the row carries one).
func flip(row json.RawMessage, drop ...string) (map[string]any, error) {
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(string(row)))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	switch m["dr_cr"] {
	case "D":
		m["dr_cr"] = "C"
	case "C":
		m["dr_cr"] = "D"
	default:
		return nil, fmt.Errorf("a row has dr_cr %v", m["dr_cr"])
	}
	for _, k := range drop {
		delete(m, k)
	}
	return m, nil
}

func emptyJSON(b json.RawMessage) bool {
	s := strings.TrimSpace(string(b))
	return s == "" || s == "null" || s == "{}"
}

// ReversalBody builds the voucher that cancels view, dated date (the
// original's date when empty). It is pure: the same view gives the same body.
func ReversalBody(raw json.RawMessage, date string) (class string, body json.RawMessage, err error) {
	var v voucherView
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", nil, fmt.Errorf("agent: read voucher: %w", err)
	}
	if strings.EqualFold(v.Op, "DELETE") {
		return "", nil, fmt.Errorf("%w: it is deleted", ErrNotReversible)
	}
	class, ok := reverseClass(v.Class)
	if !ok {
		return "", nil, fmt.Errorf("%w: a %s voucher posts no ledger legs to reverse", ErrNotReversible, v.Class)
	}
	switch {
	case len(v.GSTLines) > 0:
		return "", nil, fmt.Errorf("%w: it carries GST lines — a GST invoice is reversed by a credit or debit note a person prepares, so it reaches GSTR-1 with the original's number", ErrNotReversible)
	case len(v.Inventory) > 0:
		return "", nil, fmt.Errorf("%w: it moves stock — swapping its legs would not reverse its stock valuation", ErrNotReversible)
	case !emptyJSON(v.TDS) || !emptyJSON(v.TCS):
		return "", nil, fmt.Errorf("%w: it carries a statutory deduction or collection", ErrNotReversible)
	case len(v.Fulfilments) > 0:
		return "", nil, fmt.Errorf("%w: it fulfils an order", ErrNotReversible)
	case len(v.Postings) == 0:
		return "", nil, fmt.Errorf("%w: it has no postings", ErrNotReversible)
	}
	if date == "" {
		date = v.VoucherDate
	}
	var postings []map[string]any
	for _, p := range v.Postings {
		// "ledger" is the GET's display name; the request names ledger_id.
		m, err := flip(p, "ledger", "currency")
		if err != nil {
			return "", nil, fmt.Errorf("agent: reverse posting: %w", err)
		}
		postings = append(postings, m)
	}
	var bills []map[string]any
	for _, b := range v.BillAllocations {
		m, err := flip(b, "currency")
		if err != nil {
			return "", nil, fmt.Errorf("agent: reverse bill allocation: %w", err)
		}
		// A reference the original OPENED is closed by the reversal (AGAINST
		// the same name); one it settled is re-opened by settling the other
		// way (still AGAINST); on-account stays on account.
		switch m["ref_type"] {
		case "NEW", "ADVANCE", "AGAINST":
			m["ref_type"] = "AGAINST"
		}
		bills = append(bills, m)
	}
	var allocs []map[string]any
	for _, a := range v.CostAllocations {
		var m map[string]any
		dec := json.NewDecoder(strings.NewReader(string(a)))
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
			return "", nil, err
		}
		// posting_seq indexes the SAME postings in the same order; the split
		// of a leg is unsigned, so it is restated as it stood.
		allocs = append(allocs, map[string]any{"posting_seq": m["posting_seq"],
			"cost_category_id": m["cost_category_id"], "cost_centre_id": m["cost_centre_id"], "amount": m["amount"]})
	}
	out := map[string]any{
		"voucher_date": date,
		"narration":    fmt.Sprintf("Reversal of %s No. %s dated %s", v.Class, v.VoucherNo, v.VoucherDate),
		"postings":     postings,
	}
	if v.Party != "" {
		out["party"] = v.Party
	}
	if v.PartyLedgerID != "" {
		out["party_ledger_id"] = v.PartyLedgerID
	}
	if len(bills) > 0 {
		out["bill_allocations"] = bills
	}
	if len(allocs) > 0 {
		out["cost_allocations"] = allocs
	}
	b, err := json.Marshal(out)
	return class, b, err
}

type reverseArgs struct {
	VoucherID string `json:"voucher_id"`
	Date      string `json:"date,omitempty"`
}

// reverse is actions.reverse.
func (l *loop) reverse(ctx context.Context, prev map[string]json.RawMessage, d ModelDecision) (toolOut, error) {
	var a reverseArgs
	if err := strictDecode(d.Args, &a); err != nil {
		return toolErr(d.Tool, "args: "+err.Error()), nil
	}
	id, err := uuid.Parse(a.VoucherID)
	if err != nil {
		return toolErr(d.Tool, "voucher_id must be a uuid"), nil
	}
	if a.Date != "" && !isoDate(a.Date) {
		return toolErr(d.Tool, "date must be YYYY-MM-DD"), nil
	}
	read, ok := actions.ByID("get.vouchers._voucherid")
	if !ok {
		return toolErr(d.Tool, "the voucher read is not in the registry"), nil
	}
	if r := policy.EffectiveRights(l.run.Principal(), read, l.e.now()); !r.Allow {
		return toolErr(d.Tool, r.Reason), nil
	}
	path, err := concretePath(read, l.run.CompanyID, map[string]string{"voucherID": id.String()})
	if err != nil {
		return toolErr(d.Tool, err.Error()), nil
	}
	res, err := l.e.Dispatch.Dispatch(ctx, l.caller(l.run.Delegation.StepUp), Request{Mode: ModeRead, Spec: read, Path: path, RunID: l.run.ID})
	if err != nil {
		return toolOut{}, err
	}
	if !res.OK() {
		// ⛔ THE READ'S BODY IS NOT RELAYED: it could carry the narration.
		return toolErr(d.Tool, fmt.Sprintf("the voucher could not be read (status %d)", res.Status)), nil
	}
	class, body, err := ReversalBody(res.Body, a.Date)
	if err != nil {
		return toolErr(d.Tool, err.Error()), nil
	}
	pd, _ := json.Marshal(previewArgs{ActionID: "post.vouchers." + class, Body: body})
	out, err := l.preview(ctx, prev, ModelDecision{Tool: d.Tool, Args: pd})
	if err != nil {
		return toolOut{}, err
	}
	// The body is Go's; nothing the model sees came from the voucher's text.
	out.Sources = []policy.Source{policy.SourceBooks}
	if out.Preview != nil {
		out.Preview.ReversalOf = id.String()
	}
	return out, nil
}

func isoDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, c := range s {
		if i == 4 || i == 7 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
