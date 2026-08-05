package server_test

// orders_http_test.go — the order and BOM routes over HTTP (T3, migrations 0042
// and 0043).
//
// order_fulfilment_http_test.go pins the FULFILMENT path end to end. This file
// pins the MASTERS surface, and its most important test is not a happy path: it is
// that GET /sales-orders/{id-of-a-purchase-order} is a 404.
//
// ⛔ WHY THAT ONE MATTERS MOST. The schema is ONE order_head with a direction
// column (0042: two head tables mean two copies of every constraint), while the
// AUTHORITIES are two — `sales-order` and `purchase-order` — because a salesperson
// and a purchase clerk are different jobs. routeAction reads the PATH, so a caller
// holding only `sales-order` is admitted to /sales-orders/{any-id}. Without the
// handler's own direction check, that caller reads purchase commitments: the PDP
// said yes, and the row is the other object. There is no ACL rule that could
// prevent it, because the rule is about the path and the leak is about the row.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/server"
)

// ordersFixture is a company with two stock items and a party ledger, all created
// over HTTP where a route exists.
type ordersFixture struct {
	base     string
	ownerTok string
	table    string
	leg      string
	party    string
}

func newOrdersFixture(t *testing.T, srv *server.Server) ordersFixture {
	t.Helper()
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	f := ordersFixture{base: "/v1/companies/" + companyID, ownerTok: ownerTok}

	mkItem := func(label string) string {
		rec, body := doRaw(t, srv, http.MethodPost, f.base+"/stock-items", ownerTok,
			map[string]any{"name": label + " " + uuid.NewString()[:8], "created_by": "owner"})
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("create stock item %s = %d (%s)", label, rec.Code, rec.Body.String())
		}
		id, _ := body["stock_item_id"].(string)
		if id == "" {
			id, _ = body["id"].(string)
		}
		if id == "" {
			t.Fatalf("stock item %s came back with no id: %s", label, rec.Body.String())
		}
		return id
	}
	f.table = mkItem("Table")
	f.leg = mkItem("Leg")

	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/ledgers", ownerTok,
		map[string]any{"name": "Acme Retail " + uuid.NewString()[:8],
			"group_name": "Sundry Debtors", "created_by": "owner"})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create party ledger = %d (%s)", rec.Code, rec.Body.String())
	}
	if f.party, _ = body["ledger_id"].(string); f.party == "" {
		f.party, _ = body["id"].(string)
	}
	if f.party == "" {
		t.Fatalf("the party ledger came back with no id: %s", rec.Body.String())
	}
	return f
}

func (f ordersFixture) orderBody(no, item, qty string) map[string]any {
	return map[string]any{
		"order_no":        no,
		"order_date":      "2026-06-01",
		"party_ledger_id": f.party,
		"created_by":      "owner",
		"lines": []any{map[string]any{
			"stock_item_id": item, "qty": qty, "rate": "500", "due_date": "2026-06-30",
		}},
	}
}

// postOrderOK creates an order on the given path and returns its id.
func postOrderOK(t *testing.T, srv *server.Server, f ordersFixture, path string, body map[string]any) string {
	t.Helper()
	rec, out := doRaw(t, srv, http.MethodPost, f.base+path, f.ownerTok, body)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST %s = %d, want 201 (%s)", path, rec.Code, rec.Body.String())
	}
	id, _ := out["order_id"].(string)
	if id == "" {
		t.Fatalf("POST %s came back with no order_id: %s", path, rec.Body.String())
	}
	return id
}

// ---------------------------------------------------------------------------
// ⛔ THE ONE THAT MATTERS: the direction guard
// ---------------------------------------------------------------------------

func TestASalesOrderRouteWillNotServeAPurchaseOrder(t *testing.T) {
	srv := newServer(t)
	f := newOrdersFixture(t, srv)

	salesID := postOrderOK(t, srv, f, "/sales-orders", f.orderBody("SO-1", f.table, "20"))
	purchID := postOrderOK(t, srv, f, "/purchase-orders", f.orderBody("PO-1", f.leg, "50"))

	// THE CONTROLS: each id is readable from its OWN route. Without these, the 404s
	// below are consistent with the routes being broken for everything.
	if rec, _ := doRaw(t, srv, http.MethodGet, f.base+"/sales-orders/"+salesID, f.ownerTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: a sales order on the sales route = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if rec, _ := doRaw(t, srv, http.MethodGet, f.base+"/purchase-orders/"+purchID, f.ownerTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: a purchase order on the purchase route = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// THE PROBE, both ways. A 404 and not a 403: the resource does not exist AT THIS
	// PATH, and "forbidden" would confirm the id names something real.
	if rec, _ := doRaw(t, srv, http.MethodGet, f.base+"/sales-orders/"+purchID, f.ownerTok, nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET a PURCHASE order id on the /sales-orders route = %d, want 404. Both families "+
			"read one order_head table and routeAction decides on the PATH, so without this check a "+
			"caller holding only `sales-order` reads the company's purchase commitments — the PDP "+
			"said yes and the row is the other object (%s)", rec.Code, rec.Body.String())
	}
	if rec, _ := doRaw(t, srv, http.MethodGet, f.base+"/purchase-orders/"+salesID, f.ownerTok, nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET a SALES order id on the /purchase-orders route = %d, want 404 (%s)",
			rec.Code, rec.Body.String())
	}

	// And the same guard on the write. Closing an order through the wrong family
	// would be worse than reading it: a salesperson could abandon a purchase
	// commitment.
	rec, _ := doRaw(t, srv, http.MethodPut, f.base+"/sales-orders/"+purchID+"/close", f.ownerTok,
		map[string]any{"is_closed": true, "reason": "wrong family"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("PUT close on a PURCHASE order via the /sales-orders route = %d, want 404 — "+
			"otherwise a salesperson can abandon a purchase commitment (%s)", rec.Code, rec.Body.String())
	}

	// The list routes must not cross either.
	_, sbody := doRaw(t, srv, http.MethodGet, f.base+"/sales-orders", f.ownerTok, nil)
	srows, _ := sbody["orders"].([]any)
	if len(srows) != 1 {
		t.Errorf("the sales-order list returned %d order(s), want 1 — it must not include the "+
			"purchase order", len(srows))
	}
}

// ---------------------------------------------------------------------------
// The order lifecycle
// ---------------------------------------------------------------------------

func TestAnOrderRoundTripsAndItsNumberIsUniquePerDirection(t *testing.T) {
	srv := newServer(t)
	f := newOrdersFixture(t, srv)

	id := postOrderOK(t, srv, f, "/sales-orders", f.orderBody("SO-7", f.table, "20"))

	rec, body := doRaw(t, srv, http.MethodGet, f.base+"/sales-orders/"+id, f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET the order = %d (%s)", rec.Code, rec.Body.String())
	}
	if got, _ := body["order_no"].(string); got != "SO-7" {
		t.Errorf("order_no = %q, want SO-7", got)
	}
	if got, _ := body["direction"].(string); got != "SALES" {
		t.Errorf("direction = %q, want SALES", got)
	}
	if closed, _ := body["is_closed"].(bool); closed {
		t.Error("a new order reads is_closed = true")
	}
	lines, _ := body["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("the order read back with %d line(s), want 1", len(lines))
	}
	lm, _ := lines[0].(map[string]any)
	// Money and quantity as FIXED-4 STRINGS, never JSON numbers.
	for field, want := range map[string]string{
		"qty": "20.0000", "rate": "500.0000",
		"fulfilled_qty": "0.0000", "outstanding_qty": "20.0000",
	} {
		if got, _ := lm[field].(string); got != want {
			t.Errorf("line %s = %q, want %q (a fixed-4 STRING, never a float)", field, got, want)
		}
	}
	if got, _ := lm["due_date"].(string); got != "2026-06-30" {
		t.Errorf("due_date = %q, want 2026-06-30", got)
	}

	// ⛔ THE NUMBER IS UNIQUE PER DIRECTION, CASE-INSENSITIVELY. "SO-7" and "so-7"
	// are one order to an operator, and two of them is how half a commitment goes
	// missing while both halves look complete.
	rec, _ = doRaw(t, srv, http.MethodPost, f.base+"/sales-orders", f.ownerTok,
		f.orderBody("so-7", f.table, "5"))
	if rec.Code != http.StatusConflict {
		t.Errorf("a second sales order numbered \"so-7\" = %d, want 409 — order numbers are matched "+
			"case-insensitively (%s)", rec.Code, rec.Body.String())
	}

	// BUT a PURCHASE order MAY share the number: a business numbers its own sales
	// orders and receives supplier numbers it does not control.
	rec, _ = doRaw(t, srv, http.MethodPost, f.base+"/purchase-orders", f.ownerTok,
		f.orderBody("SO-7", f.leg, "5"))
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Errorf("a PURCHASE order numbered SO-7 = %d, want 201 — uniqueness is per direction, and "+
			"SO-1 colliding with PO-1 is not a collision an operator would accept being told about (%s)",
			rec.Code, rec.Body.String())
	}
}

// ⛔ CLOSING IS ABANDONMENT AND NEEDS A REASON. It is NOT "mark fulfilled":
// fulfilment is derived and can never be asserted.
func TestClosingAnOrderNeedsAReasonAndIsReversible(t *testing.T) {
	srv := newServer(t)
	f := newOrdersFixture(t, srv)
	id := postOrderOK(t, srv, f, "/sales-orders", f.orderBody("SO-9", f.table, "20"))

	// A closure with no reason is refused: an abandoned commitment with no stated
	// cause is the row an auditor asks about, and it cannot be told apart from a
	// mistake.
	rec, _ := doRaw(t, srv, http.MethodPut, f.base+"/sales-orders/"+id+"/close", f.ownerTok,
		map[string]any{"is_closed": true})
	if rec.Code == http.StatusOK {
		t.Error("closing an order with NO reason was accepted")
	}

	rec, _ = doRaw(t, srv, http.MethodPut, f.base+"/sales-orders/"+id+"/close", f.ownerTok,
		map[string]any{"is_closed": true, "reason": "the customer cancelled"})
	if rec.Code != http.StatusOK {
		t.Fatalf("closing with a reason = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	_, body := doRaw(t, srv, http.MethodGet, f.base+"/sales-orders/"+id, f.ownerTok, nil)
	if closed, _ := body["is_closed"].(bool); !closed {
		t.Error("the order does not read back closed")
	}
	if got, _ := body["closed_reason"].(string); got != "the customer cancelled" {
		t.Errorf("closed_reason = %q, want the reason given", got)
	}

	// A closed order leaves the ORDER BOOK — an abandoned commitment is not
	// outstanding, however little was delivered.
	_, ob := doRaw(t, srv, http.MethodGet, f.base+"/sales-order-book?as_on=2026-07-31", f.ownerTok, nil)
	if rows, _ := ob["rows"].([]any); len(rows) != 0 {
		t.Errorf("the order book still lists %d row(s) after the order was closed", len(rows))
	}

	// AND IT IS REVERSIBLE, which is the only reading under which "I closed the
	// wrong order" is correctable. The reason is cleared WITH the flag, because
	// 0042 refuses a reason on an open order — a reason attached to something that
	// has not happened is a claim about nothing.
	rec, _ = doRaw(t, srv, http.MethodPut, f.base+"/sales-orders/"+id+"/close", f.ownerTok,
		map[string]any{"is_closed": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("re-opening the order = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	_, body = doRaw(t, srv, http.MethodGet, f.base+"/sales-orders/"+id, f.ownerTok, nil)
	if closed, _ := body["is_closed"].(bool); closed {
		t.Error("the order did not re-open")
	}
	if got, _ := body["closed_reason"].(string); got != "" {
		t.Errorf("closed_reason = %q after re-opening, want empty — a reason on an open order is a "+
			"claim about something that has not happened, and 0042 CHECKs it", got)
	}
	_, ob = doRaw(t, srv, http.MethodGet, f.base+"/sales-order-book?as_on=2026-07-31", f.ownerTok, nil)
	if rows, _ := ob["rows"].([]any); len(rows) != 1 {
		t.Errorf("the order book lists %d row(s) after re-opening, want 1", len(rows))
	}
}

// `?open_only=true` is what a fulfilment picker asks for.
func TestTheOpenOnlyFilterExcludesClosedOrders(t *testing.T) {
	srv := newServer(t)
	f := newOrdersFixture(t, srv)
	open := postOrderOK(t, srv, f, "/sales-orders", f.orderBody("SO-OPEN", f.table, "20"))
	shut := postOrderOK(t, srv, f, "/sales-orders", f.orderBody("SO-SHUT", f.table, "20"))
	if rec, _ := doRaw(t, srv, http.MethodPut, f.base+"/sales-orders/"+shut+"/close", f.ownerTok,
		map[string]any{"is_closed": true, "reason": "cancelled"}); rec.Code != http.StatusOK {
		t.Fatalf("close the second order = %d (%s)", rec.Code, rec.Body.String())
	}

	// CONTROL: unfiltered, both are listed. Without it, the filtered count below is
	// consistent with the list route being broken.
	_, all := doRaw(t, srv, http.MethodGet, f.base+"/sales-orders", f.ownerTok, nil)
	if rows, _ := all["orders"].([]any); len(rows) != 2 {
		t.Fatalf("CONTROL: the unfiltered list has %d order(s), want 2", len(rows))
	}
	_, only := doRaw(t, srv, http.MethodGet, f.base+"/sales-orders?open_only=true", f.ownerTok, nil)
	rows, _ := only["orders"].([]any)
	if len(rows) != 1 {
		t.Fatalf("open_only=true returned %d order(s), want 1", len(rows))
	}
	m, _ := rows[0].(map[string]any)
	if got, _ := m["order_id"].(string); got != open {
		t.Errorf("open_only returned the wrong order: got %s, want the open one %s", got, open)
	}
	// Echoed, so a screen cannot render a filtered list under an unfiltered heading:
	// "no orders" and "no OPEN orders" are different answers.
	if flagged, _ := only["open_only"].(bool); !flagged {
		t.Error("the response does not echo open_only")
	}
}

// ---------------------------------------------------------------------------
// The bill of materials
// ---------------------------------------------------------------------------

func TestABomRoundTripsAndExplodesWithoutShortingTheBatch(t *testing.T) {
	srv := newServer(t)
	f := newOrdersFixture(t, srv)

	// The worked example from migration 0043's header: 10 of the component yields 3
	// of the finished good. Chosen because the ratio 1/3 is NOT representable at
	// four decimal places, which is the case that catches a pre-divided recipe.
	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/boms", f.ownerTok,
		map[string]any{
			"stock_item_id": f.table,
			"name":          "Standard",
			"output_qty":    "3",
			"is_default":    true,
			"created_by":    "owner",
			"components": []any{map[string]any{
				"stock_item_id": f.leg, "qty": "10",
			}},
		})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST a bom = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	bomID, _ := body["bom_id"].(string)
	if bomID == "" {
		t.Fatalf("the bom came back with no bom_id: %s", rec.Body.String())
	}

	rec, body = doRaw(t, srv, http.MethodGet, f.base+"/boms/"+bomID, f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET the bom = %d (%s)", rec.Code, rec.Body.String())
	}
	if got, _ := body["output_qty"].(string); got != "3.0000" {
		t.Errorf("output_qty = %q, want 3.0000 — the YIELD, stored as written", got)
	}
	comps, _ := body["components"].([]any)
	if len(comps) != 1 {
		t.Fatalf("the bom read back with %d component(s), want 1", len(comps))
	}
	cm, _ := comps[0].(map[string]any)
	if got, _ := cm["qty"].(string); got != "10.0000" {
		t.Errorf("component qty = %q, want 10.0000 — stored AGAINST the yield of 3 and never "+
			"pre-divided to 3.3333 per unit", got)
	}

	// ⛔ THE EXPLOSION. Make ONE, where the ratio is 1/3 and a pre-divided recipe
	// would give 3.3330 instead of 3.3333.
	rec, body = doRaw(t, srv, http.MethodGet, f.base+"/boms/"+bomID+"/explode?qty=1", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("explode for 1 = %d (%s)", rec.Code, rec.Body.String())
	}
	comps, _ = body["components"].([]any)
	cm, _ = comps[0].(map[string]any)
	if got, _ := cm["qty"].(string); got != "3.3333" {
		t.Errorf("exploding a 10-makes-3 recipe for ONE gives %q, want 3.3333. 3.3330 means the "+
			"recipe was divided per unit before scaling, and every batch is short by that much "+
			"forever", got)
	}
	// And the whole-multiple case, which is exact under any implementation and is
	// therefore the CONTROL rather than the probe.
	rec, body = doRaw(t, srv, http.MethodGet, f.base+"/boms/"+bomID+"/explode?qty=3", f.ownerTok, nil)
	comps, _ = body["components"].([]any)
	cm, _ = comps[0].(map[string]any)
	if got, _ := cm["qty"].(string); got != "10.0000" {
		t.Errorf("CONTROL: exploding for the recipe's own yield of 3 gives %q, want exactly 10.0000", got)
	}
	// The notes must SAY the explosion is single-level and that nothing has moved —
	// a screen that implied otherwise would have an operator issuing components
	// they do not hold.
	notes, _ := body["notes"].([]any)
	joined := ""
	for _, n := range notes {
		sn, _ := n.(string)
		joined += " " + sn
	}
	if !strings.Contains(joined, "SINGLE-LEVEL") {
		t.Error("the explosion does not state that it is single-level")
	}
	if !strings.Contains(strings.ToLower(joined), "nothing has moved") {
		t.Error("the explosion does not state that nothing has moved yet")
	}

	// A missing qty is a 400, not a silent default of 1: guessing would answer a
	// question about a different batch size.
	if rec, _ := doRaw(t, srv, http.MethodGet, f.base+"/boms/"+bomID+"/explode", f.ownerTok, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("explode with no qty = %d, want 400", rec.Code)
	}

	// A SECOND default for the same item is refused: two defaults is a silent coin
	// flip over which recipe a manufacture used, and they consume different
	// materials.
	rec, _ = doRaw(t, srv, http.MethodPost, f.base+"/boms", f.ownerTok,
		map[string]any{
			"stock_item_id": f.table, "name": "Substitute", "output_qty": "3",
			"is_default": true, "created_by": "owner",
			"components": []any{map[string]any{"stock_item_id": f.leg, "qty": "12"}},
		})
	if rec.Code != http.StatusConflict {
		t.Errorf("a second DEFAULT recipe for one item = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	// But a non-default alternative is fine: an item may have several recipes.
	rec, _ = doRaw(t, srv, http.MethodPost, f.base+"/boms", f.ownerTok,
		map[string]any{
			"stock_item_id": f.table, "name": "Substitute", "output_qty": "3",
			"created_by": "owner",
			"components": []any{map[string]any{"stock_item_id": f.leg, "qty": "12"}},
		})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Errorf("a NON-default alternative recipe = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

// ⛔ THE ALL-ZEROS UUID PARSES (HANDOFF §7.3), and uuid.Nil is this filter's
// "no filter" sentinel — so an explicit zero must be refused, or it silently
// returns EVERY recipe in the company with a 200 and a truthful-looking body.
// That exact defect shipped in T2's cost-centre filter.
func TestAnExplicitAllZerosStockItemFilterIsRefusedNotTreatedAsAbsent(t *testing.T) {
	srv := newServer(t)
	f := newOrdersFixture(t, srv)
	if rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/boms", f.ownerTok,
		map[string]any{
			"stock_item_id": f.table, "name": "Standard", "output_qty": "1", "created_by": "owner",
			"components": []any{map[string]any{"stock_item_id": f.leg, "qty": "4"}},
		}); rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("seed a bom = %d (%s)", rec.Code, rec.Body.String())
	}

	// CONTROL: omitting the filter lists it.
	_, all := doRaw(t, srv, http.MethodGet, f.base+"/boms", f.ownerTok, nil)
	if rows, _ := all["boms"].([]any); len(rows) != 1 {
		t.Fatalf("CONTROL: the unfiltered bom list has %d row(s), want 1", len(rows))
	}

	rec, _ := doRaw(t, srv, http.MethodGet,
		f.base+"/boms?stock_item_id=00000000-0000-0000-0000-000000000000", f.ownerTok, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an all-zeros stock_item_id filter = %d, want 400. uuid.Parse SUCCEEDS on the "+
			"all-zeros uuid and uuid.Nil is this filter's absent-sentinel, so treating it as "+
			"\"no filter\" returns every recipe in the company under a heading that says otherwise "+
			"(%s)", rec.Code, rec.Body.String())
	}
}

// A recipe whose finished good is also a component is refused — exploding it would
// consume the very thing it produces. Refused by the domain AND by migration
// 0043's trigger; this asserts an operator sees a sentence either way.
func TestAnItemCannotBeAComponentOfItsOwnRecipe(t *testing.T) {
	srv := newServer(t)
	f := newOrdersFixture(t, srv)
	rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/boms", f.ownerTok,
		map[string]any{
			"stock_item_id": f.table, "name": "Circular", "output_qty": "1", "created_by": "owner",
			"components": []any{
				map[string]any{"stock_item_id": f.leg, "qty": "4"},
				map[string]any{"stock_item_id": f.table, "qty": "1"},
			},
		})
	if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
		t.Fatalf("a recipe listing its own finished good as a component was ACCEPTED (%d)", rec.Code)
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "own recipe") {
		t.Errorf("the refusal does not explain what is circular: %s", rec.Body.String())
	}
}
