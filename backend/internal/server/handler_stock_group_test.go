package server_test

// handler_stock_group_test.go — FEATURE T10 THROUGH THE PUBLIC SURFACE: the item
// hierarchy and compound units, created over HTTP and read back off the report.
//
// ⛔ WHY THE ASSERTIONS ARE ON THE REPORT AND NOT ON THE MASTERS. A stock group
// that can be created and does not SUBTOTAL anything is a label; a compound unit
// that stores a base and a factor nobody multiplies is two columns. Both would
// pass a test that read the master back. So every figure below is read from
// GET /stock-summary, which is where an operator meets the feature.
//
// THE ORACLE IS THAT THE BRANCH TOTAL IS BIGGER THAN THE LEVEL TOTAL. A rollup
// that counted only a group's DIRECT items — the obvious implementation — would
// print the parent SMALLER than the child, and nothing on the page would
// explain it.

import (
	"net/http"
	"testing"

	"github.com/khata/cored/internal/server"
)

// stockGroup creates a group (optionally under a parent) and returns its id.
func stockGroup(t *testing.T, srv *server.Server, base, name, parent string) string {
	t.Helper()
	body := map[string]any{"name": name, "created_by": "t10"}
	if parent != "" {
		body["parent_name"] = parent
	}
	rec, resp := do(t, srv, http.MethodPost, base+"/stock-groups", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create stock group %q = %d, want 201 (%s)", name, rec.Code, rec.Body.String())
	}
	id, _ := resp["stock_group_id"].(string)
	if id == "" {
		t.Fatalf("create stock group %q returned no id: %v", name, resp)
	}
	return id
}

// TestStockGroupsSubtotalTheWholeBranch is T10's first half.
func TestStockGroupsSubtotalTheWholeBranch(t *testing.T) {
	srv := newServer(t)
	f := newStockFixture(t, srv)

	// A two-level tree, plus an item left OUTSIDE it. The ungrouped item is not
	// an edge case: every item that existed before T10 is ungrouped, and a report
	// that hid them would show a company holding less stock than it does.
	stockGroup(t, srv, f.base, "Provisions", "")
	stockGroup(t, srv, f.base, "Grains", "Provisions")

	rec, _ := do(t, srv, http.MethodPost, f.base+"/stock-items", map[string]any{
		"name": "Rice", "unit_symbol": "Nos", "stock_group_name": "Grains", "created_by": "t10",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create Rice = %d (%s)", rec.Code, rec.Body.String())
	}
	rec, _ = do(t, srv, http.MethodPost, f.base+"/stock-items", map[string]any{
		"name": "Salt", "unit_symbol": "Nos", "stock_group_name": "Provisions", "created_by": "t10",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create Salt = %d (%s)", rec.Code, rec.Body.String())
	}
	// "Widget" comes from the fixture and belongs to no group.

	// ⛔ THE GROUP MUST EXIST FIRST. Creating one implicitly from a misspelling is
	// how a company ends up with "Grains" and "grains" subtotalling half its
	// stock each, so an unknown group is a refusal and not a create.
	rec, _ = do(t, srv, http.MethodPost, f.base+"/stock-items", map[string]any{
		"name": "Ghost", "unit_symbol": "Nos", "stock_group_name": "Graims", "created_by": "t10",
	})
	if rec.Code == http.StatusCreated {
		t.Errorf("an item naming a stock group that does not exist was created (201) — a "+
			"mistyped group must not mint a second one beside the real one (%s)", rec.Body.String())
	}

	// Read the ids back and buy some of each. 100 @ 10 of Rice, 100 @ 3 of Salt,
	// 100 @ 1 of the ungrouped Widget: three distinct values so no assertion
	// below can be satisfied by the wrong item.
	items := map[string]string{}
	_, itemBody := do(t, srv, http.MethodGet, f.base+"/stock-items", nil)
	for _, it := range list(t, itemBody, "stock_items") {
		name, _ := it["name"].(string)
		id, _ := it["id"].(string)
		items[name] = id
	}
	for _, tc := range []struct{ item, qty, rate, amount string }{
		{"Rice", "100", "10", "1000"},
		{"Salt", "100", "3", "300"},
		{"Widget", "100", "1", "100"},
	} {
		id := items[tc.item]
		if id == "" {
			t.Fatalf("GET /stock-items does not list %q: %v", tc.item, items)
		}
		rec, _ := do(t, srv, http.MethodPost, f.base+"/vouchers/purchase", map[string]any{
			"voucher_no": "PUR-" + tc.item, "voucher_date": "2025-04-01", "created_by": "t10",
			"postings": []map[string]any{
				{"ledger_id": f.purchaseID, "dr_cr": "D", "amount": tc.amount},
				{"ledger_id": f.creditorID, "dr_cr": "C", "amount": tc.amount},
			},
			"inventory": []map[string]any{
				{"stock_item_id": id, "godown_id": f.godownID, "batch_id": "",
					"direction": "IN", "qty": tc.qty, "rate": tc.rate, "amount": tc.amount},
			},
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("purchase of %s = %d (%s)", tc.item, rec.Code, rec.Body.String())
		}
	}

	rec, body := do(t, srv, http.MethodGet, f.base+"/stock-summary?as_on=2025-04-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-summary = %d (%s)", rec.Code, rec.Body.String())
	}
	groups := list(t, body, "groups")
	if len(groups) != 3 {
		t.Fatalf("the Stock Summary carries %d group subtotals, want 3 (Provisions, Grains, and "+
			"the UNGROUPED bucket every pre-T10 item lives in): %v", len(groups), groups)
	}
	got := map[string]map[string]any{}
	for _, g := range groups {
		name, _ := g["group"].(string)
		got[name] = g
	}
	for _, tc := range []struct{ group, value, why string }{
		{"Provisions", "1300.0000",
			"THE WHOLE BRANCH: Salt's 300 directly, plus Grains' 1000 beneath it. A rollup that " +
				"counted only direct items would print 300 here — less than the child group " +
				"printed under it, with nothing on the page to explain the gap"},
		{"Grains", "1000.0000", "Rice only"},
		{"", "100.0000",
			"the UNGROUPED bucket: the fixture's Widget. Hiding it would show this company " +
				"holding 1,300 when it holds 1,400"},
	} {
		g, ok := got[tc.group]
		if !ok {
			t.Errorf("no subtotal for group %q — %s", tc.group, tc.why)
			continue
		}
		if g["closing_value"] != tc.value {
			t.Errorf("group %q closes at %v, want %s — %s", tc.group, g["closing_value"], tc.value, tc.why)
		}
	}
	// Tree order, and the depth a client indents by: the parent precedes its
	// child, and the ungrouped bucket is LAST.
	if groups[0]["group"] != "Provisions" || groups[1]["group"] != "Grains" || groups[2]["group"] != "" {
		t.Errorf("group order is %v, %v, %v; want Provisions, Grains, then the ungrouped bucket — "+
			"a client indents from `depth` and cannot re-sort without breaking the tree",
			groups[0]["group"], groups[1]["group"], groups[2]["group"])
	}
	if groups[0]["depth"] != float64(0) || groups[1]["depth"] != float64(1) {
		t.Errorf("depths are %v and %v, want 0 and 1", groups[0]["depth"], groups[1]["depth"])
	}

	// ⛔ AND THEY DO NOT SUM TO THE COMPANY TOTAL. 1300 + 1000 + 100 = 2400
	// against a company holding 1,400, because Rice is in two totals. That is
	// what makes a branch total readable and what makes adding them up wrong.
	if body["total_value"] != "1400.0000" {
		t.Errorf("company closing stock = %v, want 1400.0000 (1000 + 300 + 100). The GROUP rows "+
			"sum to 2400 and must not be used for this figure: an item counts in every ancestor",
			body["total_value"])
	}

	// The group is on the item read too, so a picker can show it.
	_, itemBody2 := do(t, srv, http.MethodGet, f.base+"/stock-items", nil)
	for _, it := range list(t, itemBody2, "stock_items") {
		if it["name"] == "Rice" && it["stock_group"] != "Grains" {
			t.Errorf("GET /stock-items reports Rice in group %v, want Grains", it["stock_group"])
		}
		if it["name"] == "Widget" && it["stock_group"] != "" {
			t.Errorf("the ungrouped Widget reports group %v, want \"\"", it["stock_group"])
		}
	}

	// And the hierarchy reads back in tree order with its item counts — the one
	// thing a delete control needs before it offers.
	rec, treeBody := do(t, srv, http.MethodGet, f.base+"/stock-groups", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-groups = %d (%s)", rec.Code, rec.Body.String())
	}
	nodes := list(t, treeBody, "stock_groups")
	if len(nodes) != 2 || nodes[0]["name"] != "Provisions" || nodes[1]["name"] != "Grains" {
		t.Fatalf("the group tree reads back as %v, want Provisions then Grains", nodes)
	}
	if nodes[1]["parent_name"] != "Provisions" || nodes[1]["depth"] != float64(1) {
		t.Errorf("Grains reads back as %v, want parent Provisions at depth 1", nodes[1])
	}
	if nodes[0]["item_count"] != float64(1) || nodes[1]["item_count"] != float64(1) {
		t.Errorf("item counts are %v and %v, want 1 each — the count is DIRECT items, so "+
			"Provisions holds Salt and not Rice", nodes[0]["item_count"], nodes[1]["item_count"])
	}
}

// TestACompoundUnitRestatesTheClosingQuantity is T10's second half.
//
// ⛔ THE STORED QUANTITY IS THE ONE THAT WAS KEYED. A movement of 5 BOX stores
// 5.0000; the 60 pieces are DERIVED on the report. Converting on the way in
// would make the invoice and the stock ledger disagree about what was keyed, and
// would make a corrected conversion factor unable to restate history.
func TestACompoundUnitRestatesTheClosingQuantity(t *testing.T) {
	srv := newServer(t)
	f := newStockFixture(t, srv)

	// 1 BOX = 12 Nos. "Nos" is seeded with every company.
	rec, _ := do(t, srv, http.MethodPost, f.base+"/units", map[string]any{
		"symbol": "BOX", "formal_name": "Boxes",
		"base_unit_symbol": "Nos", "conversion_factor": "12", "created_by": "t10",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create compound unit = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	// ⛔ HALF A COMPOUND UNIT IS REFUSED. A base with no factor cannot be
	// converted and a factor with no base converts to nothing; either half alone
	// is a unit that reads as compound and cannot be restated.
	for _, bad := range []map[string]any{
		{"symbol": "HALF-A", "base_unit_symbol": "Nos", "created_by": "t10"},
		{"symbol": "HALF-B", "conversion_factor": "12", "created_by": "t10"},
		{"symbol": "ZERO", "base_unit_symbol": "Nos", "conversion_factor": "0", "created_by": "t10"},
		{"symbol": "NEG", "base_unit_symbol": "Nos", "conversion_factor": "-12", "created_by": "t10"},
		{"symbol": "GHOST", "base_unit_symbol": "NOPE", "conversion_factor": "12", "created_by": "t10"},
	} {
		if rec, _ := do(t, srv, http.MethodPost, f.base+"/units", bad); rec.Code == http.StatusCreated {
			t.Errorf("unit %v was created — a half-stated conversion is a unit no report can "+
				"restate and every quantity in it would look convertible", bad["symbol"])
		}
	}
	// ⛔ AND A CHAIN IS REFUSED RATHER THAN HALF-RESOLVED. A PALLET of 20 BOX of
	// 12 Nos needs two multiplications and this build does one; resolving the
	// first level only would be wrong by a factor of twelve, reported as
	// confidently as any other number.
	if rec, _ := do(t, srv, http.MethodPost, f.base+"/units", map[string]any{
		"symbol": "PALLET", "base_unit_symbol": "BOX", "conversion_factor": "20", "created_by": "t10",
	}); rec.Code == http.StatusCreated {
		t.Error("a compound unit measured in another COMPOUND unit was accepted; this build " +
			"converts one level, so the second factor would silently vanish")
	}

	rec, resp := do(t, srv, http.MethodPost, f.base+"/stock-items", map[string]any{
		"name": "Boxed Widget", "unit_symbol": "BOX", "created_by": "t10",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item in BOX = %d (%s)", rec.Code, rec.Body.String())
	}
	itemID, _ := resp["stock_item_id"].(string)

	// Buy 5 BOX at 240 a box, sell 1 BOX. Closing 4 BOX = 48 Nos.
	rec, _ = do(t, srv, http.MethodPost, f.base+"/vouchers/purchase", map[string]any{
		"voucher_no": "PUR-BOX", "voucher_date": "2025-04-01", "created_by": "t10",
		"postings": []map[string]any{
			{"ledger_id": f.purchaseID, "dr_cr": "D", "amount": "1200"},
			{"ledger_id": f.creditorID, "dr_cr": "C", "amount": "1200"},
		},
		"inventory": []map[string]any{
			{"stock_item_id": itemID, "godown_id": f.godownID, "batch_id": "",
				"direction": "IN", "qty": "5", "rate": "240", "amount": "1200"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("purchase = %d (%s)", rec.Code, rec.Body.String())
	}
	rec, _ = do(t, srv, http.MethodPost, f.base+"/vouchers/sales", map[string]any{
		"voucher_no": "INV-BOX", "voucher_date": "2025-04-10", "created_by": "t10",
		"postings": []map[string]any{
			{"ledger_id": f.debtorID, "dr_cr": "D", "amount": "400"},
			{"ledger_id": f.salesID, "dr_cr": "C", "amount": "400"},
		},
		"inventory": []map[string]any{
			{"stock_item_id": itemID, "godown_id": f.godownID,
				"direction": "OUT", "qty": "1", "rate": "400", "amount": "400"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("sale = %d (%s)", rec.Code, rec.Body.String())
	}

	rec, body := do(t, srv, http.MethodGet, f.base+"/stock-summary?as_on=2025-04-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-summary = %d (%s)", rec.Code, rec.Body.String())
	}
	var boxed, plain map[string]any
	for _, it := range list(t, body, "items") {
		switch it["item"] {
		case "Boxed Widget":
			boxed = it
		case "Widget":
			plain = it
		}
	}
	if boxed == nil {
		t.Fatalf("Boxed Widget is not on the Stock Summary: %s", rec.Body.String())
	}
	for _, tc := range []struct{ field, want, why string }{
		{"closing_qty", "4.0000", "the STORED quantity, in the item's own unit — 5 bought less 1 sold"},
		{"unit", "BOX", "the item's own unit"},
		{"base_unit", "Nos", "what a BOX is measured in"},
		{"base_qty", "48.0000", "4 x 12, DERIVED at report time. A build that stored the " +
			"conversion instead would have put 48 in closing_qty and lost the 4 that was keyed"},
	} {
		if boxed[tc.field] != tc.want {
			t.Errorf("Boxed Widget %s = %v, want %s — %s", tc.field, boxed[tc.field], tc.want, tc.why)
		}
	}
	// ⛔ A SIMPLE UNIT RESTATES INTO NOTHING, AND SAYS SO WITH AN EMPTY STRING.
	// "0.0000" would read as "this stock is worth no pieces", which is a claim
	// about the goods rather than about the unit.
	if plain != nil {
		if plain["base_qty"] != "" || plain["base_unit"] != "" {
			t.Errorf("the simple-unit Widget reports base_qty %q in %q; a unit with no base must "+
				"restate into nothing rather than into zero",
				plain["base_qty"], plain["base_unit"])
		}
	}
}
