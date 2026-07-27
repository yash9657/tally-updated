package server_test

// auth_inventory_test.go — the WIRING claims of slice INV-F, asserted through
// the real router: what the inventory routes map to in the PDP, and that the
// item grid actually rides the voucher wire (including the offline one).
//
// WHY THESE ARE BLACK-BOX. routeAction is unexported and could be asserted
// directly, but a unit test of the mapping table proves only that the table
// says what it says. What has bitten this codebase is the mapping being RIGHT
// in the table and WRONG in effect — a route that resolves to a resource no
// rule can name, or a read that inherits a write's gates. So the oracle here is
// a security level with exactly one rule in it: if POST /godowns is reachable
// by a level whose only grant is `allow create master`, and unreachable by a
// level whose only grant is `allow display master`, then the route emits
// (create, master) and there is no table to misread.

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/server"
)

// levelWithRules creates a custom security level whose default_access is none
// (so ONLY the listed rules can grant anything) and returns a token for a fresh
// member holding it.
func levelWithRules(t *testing.T, srv *server.Server, base, ownerTok, name string, rules []map[string]string) string {
	t.Helper()
	rec, _ := doRaw(t, srv, http.MethodPost, base+"/security-levels", ownerTok, map[string]any{
		"name":           name,
		"default_access": "none",
		"rules":          rules,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create level %q = %d, want 201 (%s)", name, rec.Code, rec.Body.String())
	}
	return inventoryMemberToken(t, srv, base, ownerTok, name)
}

// TestInventoryMasterPostsMapToCreateMaster pins that godowns and batches are
// MASTERS to the PDP: created under (create, master) and listed under
// (display, master), exactly like ledgers.
//
// The two directions are both asserted because they are the failure that
// matters. If the create half slipped to some other resource, the one rule a
// supervisor writes to control master creation would stop covering stock
// masters and nobody would notice until an operator invented a warehouse. If
// the read half slipped to a write action, the picker would start demanding a
// step-up. And if either path is missing from isMasterPath, routeAction
// default-denies it and BOTH levels get a 403 before any handler runs — which
// is what the third block below catches, naming the route.
func TestInventoryMasterPostsMapToCreateMaster(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	base := "/v1/companies/" + companyID

	// The batch create needs an item to hang off; batch names are item-scoped.
	rec, _ := doRaw(t, srv, http.MethodPost, base+"/stock-items", ownerTok,
		map[string]any{"name": "Nut", "unit_symbol": "Nos", "created_by": "owner"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("owner creates stock item = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	creator := levelWithRules(t, srv, base, ownerTok, "StockCreator-"+uuid.NewString()[:6],
		[]map[string]string{{"effect": "allow", "action": "create", "resource": "master"}})
	reader := levelWithRules(t, srv, base, ownerTok, "StockReader-"+uuid.NewString()[:6],
		[]map[string]string{{"effect": "allow", "action": "display", "resource": "master"}})

	// A level whose ONLY grant is `create master` may create both, and may read
	// neither. That is only true if the POSTs emit exactly (create, master).
	for _, tc := range []struct {
		what, path string
		body       map[string]any
	}{
		{"godown", base + "/godowns", map[string]any{"name": "Bonded Warehouse", "created_by": "creator"}},
		{"batch", base + "/batches", map[string]any{"stock_item_name": "Nut", "name": "LOT-1", "created_by": "creator"}},
	} {
		rec, body := doRaw(t, srv, http.MethodPost, tc.path, creator, tc.body)
		if rec.Code != http.StatusCreated {
			t.Errorf("`allow create master` creating a %s (POST %s) = %d %v, want 201 — the route "+
				"does not emit (create, master), so the one rule that governs master creation "+
				"does not govern stock masters", tc.what, tc.path, rec.Code, body)
		}
		if rec, body := doRaw(t, srv, http.MethodGet, tc.path, creator, nil); rec.Code != http.StatusForbidden {
			t.Errorf("`allow create master` listing the %s collection (GET %s) = %d %v, want 403 — "+
				"a create grant must not carry a read", tc.what, tc.path, rec.Code, body)
		}
	}

	// The converse level: reads yes, writes no.
	for _, tc := range []struct {
		what, path string
		body       map[string]any
	}{
		{"godown", base + "/godowns", map[string]any{"name": "Rogue Store"}},
		{"batch", base + "/batches", map[string]any{"stock_item_name": "Nut", "name": "LOT-2"}},
	} {
		if rec, body := doRaw(t, srv, http.MethodGet, tc.path, reader, nil); rec.Code != http.StatusOK {
			t.Errorf("`allow display master` listing the %s collection (GET %s) = %d %v, want 200", tc.what, tc.path, rec.Code, body)
		}
		if rec, body := doRaw(t, srv, http.MethodPost, tc.path, reader, tc.body); rec.Code != http.StatusForbidden {
			t.Errorf("`allow display master` creating a %s (POST %s) = %d %v, want 403 — a read grant "+
				"must not carry a write", tc.what, tc.path, rec.Code, body)
		}
	}

	// And the default-deny half, stated explicitly: an Owner reaches every one
	// of these. An unmapped {companyID} route 403s even an Owner, so a 403 here
	// means the path never reached a handler at all.
	for _, path := range []string{
		base + "/godowns", base + "/batches", base + "/stock-items", base + "/units",
	} {
		if rec, body := doRaw(t, srv, http.MethodGet, path, ownerTok, nil); rec.Code != http.StatusOK {
			t.Errorf("Owner GET %s = %d %v, want 200 — an unmapped route default-denies before any "+
				"handler runs, so this is a routeAction gap, not an authorization one", path, rec.Code, body)
		}
	}
}

// TestVoucherRequestCarriesInventoryLines proves the item grid rides the
// ordinary voucher wire: an `inventory` array on the POST body becomes
// stock movements, and comes back on the GET in a form the alter path can
// re-send verbatim.
//
// It also pins the FAILURE mode of a malformed quantity. Every numeric field
// here is parsed with decimal.NewFromString, never strconv.ParseFloat: float
// parsing ACCEPTS "NaN" and "Inf", which the decimal package then rejects by
// PANICKING — and a data-entry operator who fat-fingers a quantity must get a
// 400 naming the line, not an opaque 500 from a recovered panic.
func TestVoucherRequestCarriesInventoryLines(t *testing.T) {
	srv := newServer(t)
	f := newStockFixture(t, srv)

	// A line with every optional field exercised: a batch-less position, a
	// discount, and an amount that is NOT qty * rate (Tally lets a user override
	// a line total, and the server stores what was keyed).
	rec, body := do(t, srv, http.MethodPost, f.base+"/vouchers/purchase", map[string]any{
		"voucher_no": "PUR-W", "voucher_date": "2025-04-02", "created_by": "inv",
		"postings": []map[string]any{
			{"ledger_id": f.purchaseID, "dr_cr": "D", "amount": "855"},
			{"ledger_id": f.creditorID, "dr_cr": "C", "amount": "855"},
		},
		"inventory": []map[string]any{
			{"stock_item_id": f.itemID, "godown_id": f.godownID, "batch_id": "",
				"direction": "IN", "qty": "9", "rate": "100", "discount_pct": "5", "amount": "855"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post purchase with inventory = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)

	rec, body = do(t, srv, http.MethodGet, f.base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get voucher = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	lines := list(t, body, "inventory")
	if len(lines) != 1 {
		t.Fatalf("fetched voucher carries %d inventory lines, want 1 — the grid did not survive "+
			"the wire (%s)", len(lines), rec.Body.String())
	}
	line := lines[0]
	for _, tc := range []struct {
		field string
		want  any
		why   string
	}{
		{"line_no", float64(1), "the 1-based input position, which is part of the replay order"},
		{"stock_item_id", f.itemID, "the id is load-bearing: re-resolving by name would drop a renamed item"},
		{"item", "Widget", "the name is what a grid renders"},
		{"godown_id", f.godownID, "the location the stock sits in"},
		{"batch_id", "", "an untracked batch is empty, never the all-zero uuid"},
		{"direction", "IN", "qty is always positive and the direction carries the sign"},
		{"qty", "9.0000", "a fixed-4 decimal string, never a JSON float"},
		{"rate", "100.0000", "the GROSS rate; the discount is a separate column"},
		{"discount_pct", "5.0000", "carried, not pre-netted into the rate"},
		{"amount", "855.0000", "AS ENTERED, never recomputed from qty * rate"},
	} {
		if line[tc.field] != tc.want {
			t.Errorf("fetched inventory line %s = %v, want %v (%s)", tc.field, line[tc.field], tc.want, tc.why)
		}
	}

	// A malformed line is the CALLER's fault and must say so. "NaN" and "Inf"
	// are the interesting ones: they are the values a float parser accepts and a
	// decimal one refuses, so they are the difference between a 400 and a
	// recovered panic served as a 500.
	for _, tc := range []struct{ what, qty string }{
		{"letters", "abc"},
		{"empty", ""},
		{"a float parser's NaN", "NaN"},
		{"a float parser's infinity", "Inf"},
		{"a thousands separator", "1,000"},
	} {
		rec, body := do(t, srv, http.MethodPost, f.base+"/vouchers/purchase", map[string]any{
			"voucher_no": "PUR-BAD", "voucher_date": "2025-04-03", "created_by": "inv",
			"postings": []map[string]any{
				{"ledger_id": f.purchaseID, "dr_cr": "D", "amount": "100"},
				{"ledger_id": f.creditorID, "dr_cr": "C", "amount": "100"},
			},
			"inventory": []map[string]any{
				{"stock_item_id": f.itemID, "godown_id": f.godownID,
					"direction": "IN", "qty": tc.qty, "rate": "10", "amount": "100"},
			},
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("a quantity of %s (%q) = %d %v, want 400 — a fat-fingered quantity is the "+
				"caller's mistake and must come back naming the field, not as a server error",
				tc.what, tc.qty, rec.Code, body)
		}
	}
}

// TestSyncCommitCarriesInventory proves the offline path carries stock FOR FREE
// because it reuses the same wire type: syncIntentReq.Payload IS a voucherReq
// and buildVoucherInput is the only translator.
//
// The oracle is the Stock Summary, not the commit response. A parallel
// inventory type on the sync struct would decode, commit, report COMMITTED with
// a final voucher number — and silently record no stock at all. Only a report
// that replays the movement ledger can tell the difference, which is exactly
// what an offline node's operator would discover weeks later.
func TestSyncCommitCarriesInventory(t *testing.T) {
	srv := newServer(t)
	f := newStockFixture(t, srv)

	clientVoucherID := uuid.NewString()
	rec, decoded := do(t, srv, http.MethodPost, f.base+"/sync/commit", map[string]any{
		"intents": []map[string]any{{
			"idempotency_key":   "inv-" + clientVoucherID,
			"client_voucher_id": clientVoucherID,
			"op":                "CREATE",
			"class":             "Purchase",
			"node_id":           "nodeA",
			"hlc":               map[string]any{"wall_millis": 100, "counter": 0, "node_id": "nodeA"},
			"base_version":      0,
			"payload": map[string]any{
				"voucher_date": "2025-05-02",
				"narration":    "offline goods receipt",
				"created_by":   "nodeA",
				"postings": []map[string]any{
					{"ledger_id": f.purchaseID, "dr_cr": "D", "amount": "700"},
					{"ledger_id": f.creditorID, "dr_cr": "C", "amount": "700"},
				},
				"inventory": []map[string]any{
					{"stock_item_id": f.itemID, "godown_id": f.godownID,
						"direction": "IN", "qty": "7", "rate": "100", "amount": "700"},
				},
			},
		}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("sync commit = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	outcomes := list(t, decoded, "outcomes")
	if len(outcomes) != 1 || outcomes[0]["status"] != "COMMITTED" {
		t.Fatalf("sync outcomes = %v, want one COMMITTED", outcomes)
	}

	rec, body := do(t, srv, http.MethodGet, f.base+"/stock-summary?as_on=2025-05-31", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-summary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	rows := list(t, body, "rows")
	if len(rows) != 1 {
		t.Fatalf("stock summary after the offline commit has %d rows, want 1 — the intent "+
			"committed but recorded no stock, so the sync wire dropped the inventory lines: %v",
			len(rows), rows)
	}
	if rows[0]["closing_qty"] != "7.0000" || rows[0]["closing_value"] != "700.0000" {
		t.Errorf("after the offline goods receipt, closing = %v @ %v, want 7.0000 worth 700.0000 — "+
			"an offline node's stock must land exactly as an online post's does",
			rows[0]["closing_qty"], rows[0]["closing_value"])
	}
}
