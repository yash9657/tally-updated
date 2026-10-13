package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// view is a GET /vouchers/{id} body with the fields ReversalBody reads.
func view(class string, extra map[string]any) json.RawMessage {
	v := map[string]any{
		"voucher_id": "01a0ea2d-0000-7000-8000-000000000001", "voucher_no": "7", "voucher_date": "2025-05-10",
		"class": class, "op": "CREATE", "party": "", "party_ledger_id": "",
		"postings": []any{
			map[string]any{"ledger_id": "L-RENT", "ledger": "Office Rent", "dr_cr": "D", "amount": "25000.0000"},
			map[string]any{"ledger_id": "L-HDFC", "ledger": "HDFC Bank", "dr_cr": "C", "amount": "25000.0000"},
		},
		"bill_allocations": []any{}, "inventory": []any{}, "cost_allocations": []any{}, "fulfilments": []any{},
		"gst_lines": []any{}, "tds": nil, "tcs": nil,
	}
	for k, x := range extra {
		v[k] = x
	}
	b, _ := json.Marshal(v)
	return b
}

func decodeBody(t *testing.T, b json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestReversalSwapsEveryLegAndKeepsTheLedgersAndAmounts(t *testing.T) {
	class, body, err := ReversalBody(view("Payment", nil), "")
	if err != nil {
		t.Fatal(err)
	}
	if class != "payment" {
		t.Fatalf("a payment reverses as %q", class)
	}
	m := decodeBody(t, body)
	ps := m["postings"].([]any)
	got := map[string]string{}
	for _, p := range ps {
		pm := p.(map[string]any)
		if _, ok := pm["ledger"]; ok {
			t.Fatal("the display name was sent back as a request field")
		}
		got[pm["ledger_id"].(string)] = pm["dr_cr"].(string) + " " + pm["amount"].(string)
	}
	if got["L-RENT"] != "C 25000.0000" || got["L-HDFC"] != "D 25000.0000" {
		t.Fatalf("legs = %v", got)
	}
	if m["voucher_date"] != "2025-05-10" || !strings.HasPrefix(m["narration"].(string), "Reversal of Payment No. 7") {
		t.Fatalf("date/narration = %v / %v", m["voucher_date"], m["narration"])
	}
	// A stated date wins.
	_, body, _ = ReversalBody(view("Payment", nil), "2025-06-01")
	if decodeBody(t, body)["voucher_date"] != "2025-06-01" {
		t.Fatal("the stated reversal date was ignored")
	}
}

func TestReversalOfAnInvoiceIsItsMirrorClass(t *testing.T) {
	for in, want := range map[string]string{"Sales": "credit-note", "Purchase": "debit-note",
		"Credit Note": "sales", "Debit Note": "purchase", "Receipt": "receipt", "Contra": "contra", "Journal": "journal"} {
		class, _, err := ReversalBody(view(in, nil), "")
		if err != nil || class != want {
			t.Errorf("%s reverses as %q (%v), want %q", in, class, err, want)
		}
	}
}

func TestReversalRefusesWhatSwappingLegsCannotUndo(t *testing.T) {
	cases := map[string]map[string]any{
		"GST lines": {"gst_lines": []any{map[string]any{"line_no": 1, "taxable_value": "100"}}},
		"inventory": {"inventory": []any{map[string]any{"item": "x"}}},
		"a TDS":     {"tds": map[string]any{"section": "194C"}},
		"a TCS":     {"tcs": map[string]any{"section": "206C(1H)"}},
		"an order":  {"fulfilments": []any{map[string]any{"line_no": 1}}},
		"a delete":  {"op": "DELETE"},
		"no legs":   {"postings": []any{}},
	}
	for name, extra := range cases {
		if _, _, err := ReversalBody(view("Sales", extra), ""); !errors.Is(err, ErrNotReversible) {
			t.Errorf("a voucher with %s: %v, want ErrNotReversible", name, err)
		}
	}
	if _, _, err := ReversalBody(view("Delivery Note", nil), ""); !errors.Is(err, ErrNotReversible) {
		t.Errorf("a stock voucher: %v, want ErrNotReversible", err)
	}
}

func TestReversalClosesTheBillsTheOriginalOpened(t *testing.T) {
	_, body, err := ReversalBody(view("Sales", map[string]any{"bill_allocations": []any{
		map[string]any{"ledger_id": "L-ACME", "ref_name": "S/7/25-26", "ref_type": "NEW", "dr_cr": "D", "amount": "25000.0000", "due_date": ""},
		map[string]any{"ledger_id": "L-ACME", "ref_name": "", "ref_type": "ON_ACCOUNT", "dr_cr": "D", "amount": "0.0000", "due_date": ""},
	}}), "")
	if err != nil {
		t.Fatal(err)
	}
	bs := decodeBody(t, body)["bill_allocations"].([]any)
	first, second := bs[0].(map[string]any), bs[1].(map[string]any)
	if first["ref_type"] != "AGAINST" || first["ref_name"] != "S/7/25-26" || first["dr_cr"] != "C" {
		t.Fatalf("the opened bill is not closed against itself: %v", first)
	}
	if second["ref_type"] != "ON_ACCOUNT" {
		t.Fatalf("on-account changed type: %v", second)
	}
}
