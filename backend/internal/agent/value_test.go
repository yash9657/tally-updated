package agent

import "testing"

// TestValuePaise pins what the delegated ceiling is judged on for a write
// that posts no ledger legs (wave-2 review, finding 1). Mutation: counting
// both directions of a stock journal (in + out) turns the journal case red;
// dropping the round-up turns the fraction case red.
func TestValuePaise(t *testing.T) {
	cases := []struct {
		name string
		e    *Effect
		want int64
		ok   bool
	}{
		{"nil effect", nil, 0, false},
		{"a posting's debit wins", &Effect{DebitPaise: 12345, Stock: []StockMove{{Direction: "OUT", Amount: "999999.00"}}}, 12345, true},
		{"an order is qty x rate over its lines", &Effect{Orders: []OrderChange{{Lines: []OrderLine{
			{Qty: "100000.0000", Rate: "1000.00"}, {Qty: "2", Rate: "0.50"}}}}}, 10_000_000_100, true},
		{"a stock journal is the larger side, not both", &Effect{Stock: []StockMove{
			{Direction: "OUT", Amount: "400.00"}, {Direction: "IN", Amount: "400.00"}}}, 40000, true},
		{"a physical-stock voucher counts its value", &Effect{Stock: []StockMove{{Direction: "IN", Amount: "-250.00"}}}, 25000, true},
		{"a fraction of a paisa rounds up", &Effect{Orders: []OrderChange{{Lines: []OrderLine{{Qty: "1", Rate: "0.001"}}}}}, 1, true},
		{"a master alter has nothing to measure", &Effect{Masters: []MasterChange{{Entity: "stock_item"}}}, 0, false},
		{"an order close has no lines", &Effect{Orders: []OrderChange{{Closed: true}}}, 0, false},
		{"an unparseable amount is not a zero", &Effect{Stock: []StockMove{{Direction: "IN", Amount: "lots"}}}, 0, false},
	}
	for _, c := range cases {
		got, ok := c.e.ValuePaise()
		if got != c.want || ok != c.ok {
			t.Errorf("%s: ValuePaise() = %d, %v; want %d, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
