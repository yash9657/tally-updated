package server_test

// handler_stock_valuation_method_test.go — feature T9 THROUGH THE PUBLIC
// SURFACE: FIFO and LIFO reached by HTTP only.
//
// ⛔⛔ WHY EVERY LINE BELOW GOES OVER THE WIRE, INCLUDING THE PARTS THAT WOULD BE
// EASIER IN GO. The payroll lane (HANDOFF §1.15) shipped an engine, eight
// mounted routes, a full test suite and a module that could not pay anybody:
// the two writers a pay run needs had NO ROUTE, and every test set them up by
// calling the Go service, so a green suite said nothing about whether an
// operator could do the task. inventory.ValuerFor could be given a FIFO arm,
// the CHECK could be widened, the corpus could pass in both languages — and if
// nothing can WRITE stock_item.valuation_method, no user has FIFO. So:
//
//   - the item's method is set by POST /stock-items and by
//     PUT /stock-items/{id}/valuation-method, never by a masters.Service call;
//   - it is read back from GET /stock-items, never from the POST's own echo
//     (register row D43: a body key that is accepted and discarded answers 201
//     and changes nothing);
//   - the numbers are read from GET /stock-summary and GET /balance-sheet.
//
// THE ORACLE IS THAT THE THREE METHODS DISAGREE. The same three movements —
// buy 10 @ 100, buy 10 @ 120, issue 5 — close at 1650.0000 under weighted
// average, 1700.0000 under FIFO and 1600.0000 under LIFO. Any test whose
// expected figure is shared by two methods proves only that the endpoint
// returned JSON.

import (
	"net/http"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/server"
)

// stockItemWithMethod creates an item over HTTP, asks for `method`, and returns
// its id — having first confirmed through a READ that the column actually holds
// what was asked for. An empty method asks for nothing and must land on the
// default.
func stockItemWithMethod(t *testing.T, srv *server.Server, base, name, method string) string {
	t.Helper()
	body := map[string]any{"name": name, "unit_symbol": "Nos", "created_by": "inv"}
	if method != "" {
		body["valuation_method"] = method
	}
	rec, resp := do(t, srv, http.MethodPost, base+"/stock-items", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %s (%s) = %d, want 201 (%s)", name, method, rec.Code, rec.Body.String())
	}
	id, _ := resp["stock_item_id"].(string)
	if id == "" {
		t.Fatalf("create %s returned no stock_item_id: %v", name, resp)
	}

	want := method
	if want == "" {
		want = "WEIGHTED_AVG"
	}
	if got := storedValuationMethod(t, srv, base, id); got != want {
		t.Fatalf("POST /stock-items asked for valuation_method %q and the STORED row says %q — "+
			"a body key that is accepted and discarded answers 201 while the item is valued on "+
			"a basis nobody chose (register row D43 is this exact shape)", method, got)
	}
	return id
}

// storedValuationMethod reads one item's method back out of GET /stock-items.
func storedValuationMethod(t *testing.T, srv *server.Server, base, itemID string) string {
	t.Helper()
	rec, body := do(t, srv, http.MethodGet, base+"/stock-items", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-items = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	for _, it := range list(t, body, "stock_items") {
		if it["id"] == itemID {
			m, _ := it["valuation_method"].(string)
			return m
		}
	}
	t.Fatalf("GET /stock-items does not list %s — a client cannot address a master it cannot enumerate", itemID)
	return ""
}

// buyAndSellOneItem posts the three movements every case here shares against
// one item: 10 @ 100, 10 @ 120, then an issue of 5 at a SALE price of 200.
//
// The sale price is deliberately far from every cost: a valuer that consumed at
// the sale rate would report an outward value of 1000.0000 under all three
// methods and would be caught by any of them.
func buyAndSellOneItem(t *testing.T, srv *server.Server, f stockFixture, itemID, tag string) {
	t.Helper()
	for _, p := range []struct{ no, date, qty, rate, amount string }{
		{"PUR-1-" + tag, "2025-04-01", "10", "100", "1000"},
		{"PUR-2-" + tag, "2025-04-05", "10", "120", "1200"},
	} {
		rec, _ := do(t, srv, http.MethodPost, f.base+"/vouchers/purchase", map[string]any{
			"voucher_no": p.no, "voucher_date": p.date, "created_by": "inv",
			"postings": []map[string]any{
				{"ledger_id": f.purchaseID, "dr_cr": "D", "amount": p.amount},
				{"ledger_id": f.creditorID, "dr_cr": "C", "amount": p.amount},
			},
			"inventory": []map[string]any{
				{"stock_item_id": itemID, "godown_id": f.godownID, "batch_id": "",
					"direction": "IN", "qty": p.qty, "rate": p.rate, "amount": p.amount},
			},
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("purchase %s = %d, want 201 (%s)", p.no, rec.Code, rec.Body.String())
		}
	}
	rec, _ := do(t, srv, http.MethodPost, f.base+"/vouchers/sales", map[string]any{
		"voucher_no": "INV-1-" + tag, "voucher_date": "2025-04-10", "created_by": "inv",
		"postings": []map[string]any{
			{"ledger_id": f.debtorID, "dr_cr": "D", "amount": "1000"},
			{"ledger_id": f.salesID, "dr_cr": "C", "amount": "1000"},
		},
		"inventory": []map[string]any{
			{"stock_item_id": itemID, "godown_id": f.godownID,
				"direction": "OUT", "qty": "5", "rate": "200", "amount": "1000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("sale = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

// stockRowFor returns the one stock-summary position row for an item name.
func stockRowFor(t *testing.T, srv *server.Server, base, itemName string) map[string]any {
	t.Helper()
	rec, body := do(t, srv, http.MethodGet, base+"/stock-summary?as_on=2025-04-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-summary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	for _, row := range list(t, body, "rows") {
		if row["item"] == itemName {
			return row
		}
	}
	t.Fatalf("no stock-summary row for %q: %v", itemName, body)
	return nil
}

// assertPosition checks the three figures a costing method decides, and says
// what the other methods would have answered — so a failure names the method
// that actually ran instead of just a number.
func assertValuedAs(t *testing.T, row map[string]any, method, outward, closing, rate string) {
	t.Helper()
	for _, tc := range []struct{ field, want string }{
		{"valuation_method", method},
		{"outward_value", outward},
		{"closing_value", closing},
		{"closing_rate", rate},
		{"closing_qty", "15.0000"}, // quantity is method-INdependent: a plain sum.
	} {
		if row[tc.field] != tc.want {
			t.Errorf("under %s the position reports %s = %v, want %s. "+
				"(For these movements: WEIGHTED_AVG closes at 1650.0000 consuming 550.0000, "+
				"FIFO at 1700.0000 consuming 500.0000, LIFO at 1600.0000 consuming 600.0000 — "+
				"so this figure names which valuer actually ran.)",
				method, tc.field, row[tc.field], tc.want)
		}
	}
}

// TestAnItemCreatedAsFIFOOverHTTPIsValuedAsFIFO is the whole feature in one
// test: an operator asks for FIFO on the master form, keys two purchases and a
// sale, and the Stock Summary comes back on the FIFO basis.
//
// ⛔ IT FAILS ON THE BUILD THAT SHIPPED THE VALUERS AND NOTHING ELSE, with
// closing_value 1650.0000 — because POST /stock-items had no valuation_method
// field, so the item was created WEIGHTED_AVG and the FIFO arm was unreachable
// from every screen and every client. Measured: predicted 1650.0000, observed
// 1650.0000.
func TestAnItemCreatedAsFIFOOverHTTPIsValuedAsFIFO(t *testing.T) {
	srv := newServer(t)
	f := newStockFixture(t, srv)

	fifoID := stockItemWithMethod(t, srv, f.base, "Widget FIFO", "FIFO")
	buyAndSellOneItem(t, srv, f, fifoID, "F")

	assertValuedAs(t, stockRowFor(t, srv, f.base, "Widget FIFO"),
		"FIFO", "500.0000", "1700.0000", "113.3333")
}

// TestAnItemCreatedAsLIFOOverHTTPIsValuedAsLIFO is the other end of the queue.
// It is a separate test rather than a table row because LIFO's failure mode is
// specific: a lot queue walked from the wrong end still balances, still
// conserves value, and reports the FIFO answer.
func TestAnItemCreatedAsLIFOOverHTTPIsValuedAsLIFO(t *testing.T) {
	srv := newServer(t)
	f := newStockFixture(t, srv)

	lifoID := stockItemWithMethod(t, srv, f.base, "Widget LIFO", "LIFO")
	buyAndSellOneItem(t, srv, f, lifoID, "L")

	assertValuedAs(t, stockRowFor(t, srv, f.base, "Widget LIFO"),
		"LIFO", "600.0000", "1600.0000", "106.6667")
}

// TestThreeMethodsInOneCompanyAreValuedSeparately is the defect a shared valuer
// would cause, at the HTTP grain: reporting.StockSummary resolves a valuer PER
// ITEM and replays each item's positions with it, so three items bought and
// sold identically must come back on three different bases in ONE response.
//
// A build that resolved one valuer for the whole report — the obvious
// optimisation — would answer three identical rows, all of them plausible, all
// of them balanced, and two of them wrong.
func TestThreeMethodsInOneCompanyAreValuedSeparately(t *testing.T) {
	srv := newServer(t)
	f := newStockFixture(t, srv)

	cases := []struct{ name, method, outward, closing, rate string }{
		{"Widget Avg", "", "550.0000", "1650.0000", "110.0000"},
		{"Widget FIFO", "FIFO", "500.0000", "1700.0000", "113.3333"},
		{"Widget LIFO", "LIFO", "600.0000", "1600.0000", "106.6667"},
	}
	for i, c := range cases {
		id := stockItemWithMethod(t, srv, f.base, c.name, c.method)
		buyAndSellOneItem(t, srv, f, id, string(rune('a'+i)))
	}
	for _, c := range cases {
		method := c.method
		if method == "" {
			method = "WEIGHTED_AVG"
		}
		assertValuedAs(t, stockRowFor(t, srv, f.base, c.name), method, c.outward, c.closing, c.rate)
	}

	// And the company's total is the sum of three DIFFERENT bases, not three
	// copies of one. 1650 + 1700 + 1600.
	rec, body := do(t, srv, http.MethodGet, f.base+"/stock-summary?as_on=2025-04-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-summary = %d (%s)", rec.Code, rec.Body.String())
	}
	if body["total_value"] != "4950.0000" {
		t.Errorf("company closing stock = %v, want 4950.0000 (1650 + 1700 + 1600). "+
			"4950 is only reachable if each item used its OWN method; 4950 is not 3 x any single basis",
			body["total_value"])
	}
}

// TestSwitchingTheMethodOverHTTPRestatesTheBalanceSheet is the ALTER half, and
// it asserts the consequence rather than the field: switching an item that is
// already trading moves the Balance Sheet, on periods that have already been
// reported.
func TestSwitchingTheMethodOverHTTPRestatesTheBalanceSheet(t *testing.T) {
	srv := newServer(t)
	f := newStockFixture(t, srv)

	itemID := stockItemWithMethod(t, srv, f.base, "Widget Switch", "")
	buyAndSellOneItem(t, srv, f, itemID, "S")

	// BEFORE: the default basis, and the sheet that goes with it.
	assertValuedAs(t, stockRowFor(t, srv, f.base, "Widget Switch"),
		"WEIGHTED_AVG", "550.0000", "1650.0000", "110.0000")
	assetsBefore := assetsTotal(t, srv, f.base)

	rec, body := do(t, srv, http.MethodPut,
		f.base+"/stock-items/"+itemID+"/valuation-method",
		map[string]any{"valuation_method": "FIFO", "created_by": "inv"})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT valuation-method = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	// ⛔ THE ANSWER IS THE STORED ROW, NOT THE REQUEST. An echo cannot tell a
	// caller whether the column moved (register row D43), and here the caller
	// would go on to read a report on the old basis and believe it.
	if body["valuation_method"] != "FIFO" || body["name"] != "Widget Switch" {
		t.Errorf("PUT answered %v; it must answer with the row as STORED", body)
	}
	if body["restates"] == nil {
		t.Error("the answer does not disclose that the item's whole history has been " +
			"re-valued — a client showing only 'saved' lets an operator walk away not " +
			"knowing that a reported period has changed")
	}
	if got := storedValuationMethod(t, srv, f.base, itemID); got != "FIFO" {
		t.Fatalf("after the PUT the stored method is %q — the route answered 200 and changed nothing", got)
	}

	// AFTER: the same untouched movements, valued as FIFO.
	assertValuedAs(t, stockRowFor(t, srv, f.base, "Widget Switch"),
		"FIFO", "500.0000", "1700.0000", "113.3333")

	assetsAfter := assetsTotal(t, srv, f.base)
	if delta := assetsAfter.Sub(assetsBefore); delta.String() != "50" {
		t.Errorf("total assets moved by %s when the costing method changed, want 50 "+
			"(1700.0000 - 1650.0000). A method that reaches the Stock Summary and NOT the "+
			"Balance Sheet leaves the two statements stating different closing stock, with "+
			"nothing out of balance to reveal which is right",
			delta.StringFixed(4))
	}

	// The same method twice is a 409, not a silent success: an audit trail of
	// restatements must hold only real ones.
	if rec, _ := do(t, srv, http.MethodPut, f.base+"/stock-items/"+itemID+"/valuation-method",
		map[string]any{"valuation_method": "FIFO", "created_by": "inv"}); rec.Code != http.StatusConflict {
		t.Errorf("re-asserting the SAME method = %d, want 409", rec.Code)
	}

	// And switching back reproduces the earlier figures EXACTLY — which is the
	// claim the route's own answer makes, and the reason the switch is safe to
	// offer at all. Nothing is stored, so nothing was lost.
	rec, _ = do(t, srv, http.MethodPut, f.base+"/stock-items/"+itemID+"/valuation-method",
		map[string]any{"valuation_method": "WEIGHTED_AVG", "created_by": "inv"})
	if rec.Code != http.StatusOK {
		t.Fatalf("switch back = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	assertValuedAs(t, stockRowFor(t, srv, f.base, "Widget Switch"),
		"WEIGHTED_AVG", "550.0000", "1650.0000", "110.0000")
	if back := assetsTotal(t, srv, f.base); !back.Equal(assetsBefore) {
		t.Errorf("switching back put total assets at %s, not the original %s — the switch "+
			"is not reversible, so one of the two directions is losing information",
			back.StringFixed(4), assetsBefore.StringFixed(4))
	}
}

// TestAMethodWithNoValuerIsRefusedAtTheMASTER pins where the refusal has to
// happen. inventory.ValuerFor erroring is the LAST line of defence, and by then
// the damage is done at the wrong grain: the item exists, and the Stock Summary
// and the Balance Sheet fail for the WHOLE COMPANY — every other item included
// — until somebody finds the row. So the write is refused instead.
func TestAMethodWithNoValuerIsRefusedAtTheMaster(t *testing.T) {
	srv := newServer(t)
	f := newStockFixture(t, srv)

	// Every one of these is a REAL TallyPrime costing method that this build
	// deliberately does not implement (see migration 0078's header), plus the
	// case-variant traps. The assertion is the PROPERTY — "no valuer, no row" —
	// and not this list.
	for _, method := range []string{
		"LAST_PURCHASE_COST", "STANDARD_COST", "AT_ZERO_COST", "MONTHLY_AVG",
		"LIFO_ANNUAL", "fifo", "Fifo", "WEIGHTED AVG", "'FIFO'", "",
	} {
		body := map[string]any{"name": "Refused " + method, "unit_symbol": "Nos", "created_by": "inv"}
		if method != "" {
			body["valuation_method"] = method
		} else {
			// The empty string is the one case that is NOT refused: it means
			// "the default", so every caller written before T9 keeps working.
			body["valuation_method"] = ""
		}
		rec, _ := do(t, srv, http.MethodPost, f.base+"/stock-items", body)
		if method == "" {
			if rec.Code != http.StatusCreated {
				t.Errorf("an ABSENT valuation_method = %d, want 201 with the default — "+
					"every caller that predates T9 sends no method at all", rec.Code)
			}
			continue
		}
		if rec.Code == http.StatusCreated {
			t.Errorf("POST /stock-items accepted valuation_method %q with 201 — no valuer "+
				"implements it, so the Stock Summary and the Balance Sheet would now fail "+
				"for this whole company", method)
		}
	}

	// The ALTER route must refuse the same set. It is a separate seam and has
	// its own validation path; a route that validated on create and not on
	// alter would leave the hole exactly where an operator experiments.
	itemID := stockItemWithMethod(t, srv, f.base, "Widget Guarded", "")
	for _, method := range []string{"STANDARD_COST", "lifo", "WEIGHTED_AVG "} {
		rec, _ := do(t, srv, http.MethodPut, f.base+"/stock-items/"+itemID+"/valuation-method",
			map[string]any{"valuation_method": method, "created_by": "inv"})
		if rec.Code == http.StatusOK {
			t.Errorf("PUT valuation-method accepted %q with 200", method)
		}
	}
	if got := storedValuationMethod(t, srv, f.base, itemID); got != "WEIGHTED_AVG" {
		t.Errorf("after every refused switch the item reads %q, want WEIGHTED_AVG — a refused "+
			"write moved the column", got)
	}
	// A missing body field is a 400 and names where the vocabulary lives.
	if rec, _ := do(t, srv, http.MethodPut, f.base+"/stock-items/"+itemID+"/valuation-method",
		map[string]any{"created_by": "inv"}); rec.Code != http.StatusBadRequest {
		t.Errorf("PUT with no valuation_method = %d, want 400", rec.Code)
	}
}

// TestTheCostingVocabularyIsSERVED closes the loop a client-side list would
// leave open: a picker built from a hardcoded list can offer a method the server
// refuses, and the operator meets that refusal after keying the whole master.
func TestTheCostingVocabularyIsServed(t *testing.T) {
	srv := newServer(t)
	f := newStockFixture(t, srv)

	rec, body := do(t, srv, http.MethodGet, f.base+"/inventory-vocabulary", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /inventory-vocabulary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["default"] != "WEIGHTED_AVG" {
		t.Errorf("vocabulary default = %v, want WEIGHTED_AVG (Tally's default, and every "+
			"item created before T9 carries it)", body["default"])
	}
	methods := list(t, body, "valuation_methods")
	if len(methods) < 3 {
		t.Fatalf("vocabulary offers %d methods, want at least the three this build values "+
			"stock with: %v", len(methods), methods)
	}
	// EVERY offered method must be acceptable to the write path. That is the
	// only property worth asserting here: a vocabulary the create route refuses
	// is worse than no vocabulary at all.
	for i, m := range methods {
		name, _ := m["method"].(string)
		if name == "" {
			t.Errorf("vocabulary entry %d has no method: %v", i, m)
			continue
		}
		if m["label"] == nil || m["basis"] == nil {
			t.Errorf("method %s is offered with no label/basis — a picker showing "+
				"WEIGHTED_AVG/FIFO/LIFO teaches an operator nothing about which to choose", name)
		}
		stockItemWithMethod(t, srv, f.base, "Offered "+name, name)
	}
}

// assetsTotal is the Balance Sheet's asset side as at the test period's end.
// Reading the TOTAL rather than hunting for the Closing Stock node is
// deliberate: it is the figure a reader of the statement acts on, and it is the
// one that must move when the closing stock does.
func assetsTotal(t *testing.T, srv *server.Server, base string) decimal.Decimal {
	t.Helper()
	rec, body := do(t, srv, http.MethodGet, base+"/balance-sheet?as_on=2025-04-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /balance-sheet = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	assets, ok := body["assets"].(map[string]any)
	if !ok {
		t.Fatalf("balance sheet has no assets section: %v", body)
	}
	total, ok := assets["total"].(string)
	if !ok {
		t.Fatalf("assets section has no total: %v", assets)
	}
	d, err := decimal.NewFromString(total)
	if err != nil {
		t.Fatalf("assets total %q is not a decimal: %v", total, err)
	}
	if body["balanced"] != true {
		t.Fatalf("the balance sheet does not balance (%v) — no figure on it means anything", body)
	}
	return d
}
