package server_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestAnOmittedRateAsksToBePricedAndAStatedZeroDoesNot is the HTTP expression of
// the price-list feature, and without it the feature is reachable only from Go.
//
// The distinction the whole thing rests on is ABSENT vs "0". An omitted rate
// means "price this from the effective list"; a rate of "0" is a stated fact — a
// free sample — and repricing it would put a price on goods the business
// deliberately gave away. encoding/json collapses both to "" on a plain string
// field, which is why inventoryLineReq.Rate is a *string: a wire shape that
// cannot express the difference makes the ledger's RateUnstated flag dead code,
// and the price list unreachable from any client.
func TestAnOmittedRateAsksToBePricedAndAStatedZeroDoesNot(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	base := "/v1/companies/" + companyID
	sub := "price-wire-" + uuid.NewString()[:8]

	doRaw(t, srv, http.MethodPost, base+"/units", ownerTok,
		map[string]any{"symbol": "Nos", "name": "Numbers", "created_by": sub})
	rec, itemBody := doRaw(t, srv, http.MethodPost, base+"/stock-items", ownerTok,
		map[string]any{"name": "Widget", "unit_symbol": "Nos", "hsn": "8471",
			"gst_rate_pct": "18.0000", "created_by": sub})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create stock item = %d (%s)", rec.Code, rec.Body.String())
	}
	itemID, _ := itemBody["stock_item_id"].(string)

	rec, _ = doRaw(t, srv, http.MethodPost, base+"/price-lists", ownerTok, map[string]any{
		"name": "Standard " + uuid.NewString()[:6], "effective_from": "2026-04-01",
		"created_by": sub,
		"entries": []map[string]any{{
			"stock_item_name": "Widget", "qty_from": "0", "qty_to_unbounded": true,
			"rate": "250.0000",
		}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create price list = %d (%s)", rec.Code, rec.Body.String())
	}

	_, gdBody := doRaw(t, srv, http.MethodGet, base+"/godowns", ownerTok, nil)
	godowns := list(t, gdBody, "godowns")
	if len(godowns) == 0 {
		t.Fatalf("company has no seeded godown")
	}
	godownID, _ := godowns[0]["id"].(string)

	// It must be an ITEM INVOICE: the ledger deliberately refuses to price a
	// Receipt Note or a Delivery Note ("it records goods moving, not a sale"),
	// which is itself the right call — those documents carry goods at a stated
	// cost, not at a selling price.
	mk := func(name, group string) string {
		t.Helper()
		body := map[string]any{"name": name, "group_name": group, "created_by": sub}
		if group == "Sundry Debtors" {
			body["state_code"] = "27"
		}
		rec, b := doRaw(t, srv, http.MethodPost, base+"/ledgers", ownerTok, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d (%s)", name, rec.Code, rec.Body.String())
		}
		id, _ := b["ledger_id"].(string)
		return id
	}
	party := mk("Acme Buyer", "Sundry Debtors")
	sales := mk("Sales A/c", "Sales Accounts")
	cgst := mk("Output CGST", "Duties & Taxes")
	sgst := mk("Output SGST", "Duties & Taxes")

	// Goods must exist before they can be sold: negative stock is refused, so an
	// empty warehouse would fail every case below for the wrong reason.
	rec, _ = doRaw(t, srv, http.MethodPost, base+"/vouchers/receipt-note", ownerTok,
		map[string]any{"voucher_date": "2026-04-01", "created_by": sub,
			"inventory": []map[string]any{{"stock_item_id": itemID, "godown_id": godownID,
				"direction": "IN", "qty": "100.0000", "rate": "120.0000"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("opening receipt note = %d (%s)", rec.Code, rec.Body.String())
	}

	rateOf := func(t *testing.T, line map[string]any) string {
		t.Helper()
		rec, body := doRaw(t, srv, http.MethodPost, base+"/vouchers/sales", ownerTok,
			map[string]any{"voucher_date": "2026-04-05", "created_by": sub,
				"party_ledger_id": party, "item_ledger_id": sales,
				"gst": map[string]any{"supplier_state_code": "27",
					"place_of_supply_state_code": "27",
					"cgst_ledger_id":             cgst, "sgst_ledger_id": sgst},
				"inventory": []map[string]any{line}})
		if rec.Code != http.StatusCreated {
			t.Fatalf("post sales item invoice = %d (%s)", rec.Code, rec.Body.String())
		}
		id, _ := body["voucher_id"].(string)
		_, got := doRaw(t, srv, http.MethodGet, base+"/vouchers/"+id, ownerTok, nil)
		lines := list(t, got, "inventory")
		if len(lines) != 1 {
			t.Fatalf("fetched voucher carries %d inventory lines, want 1", len(lines))
		}
		rate, _ := lines[0]["rate"].(string)
		return rate
	}

	line := func(extra map[string]any) map[string]any {
		l := map[string]any{"stock_item_id": itemID, "godown_id": godownID,
			"direction": "OUT", "qty": "4.0000"}
		for k, v := range extra {
			l[k] = v
		}
		return l
	}

	// NO rate key at all -> priced from the list.
	if got := rateOf(t, line(nil)); got != "250.0000" {
		t.Errorf("a line that OMITS its rate came back at %q, want 250.0000 from the "+
			"effective price list — an omitted rate is a request to be priced, and if the "+
			"wire cannot express it no client can ever use a price list", got)
	}

	// An explicit null is the same request, spelt by a client that always emits
	// the key.
	if got := rateOf(t, line(map[string]any{"rate": nil})); got != "250.0000" {
		t.Errorf("a line whose rate is explicitly null came back at %q, want 250.0000 — "+
			"null and absent must mean the same thing, or a client that always emits its "+
			"keys silently loses the feature", got)
	}

	// A STATED zero is a free sample and must survive untouched.
	if got := rateOf(t, line(map[string]any{"rate": "0"})); got != "0.0000" {
		t.Errorf("a line stating rate 0 came back at %q, want 0.0000 — a free sample that "+
			"silently acquires the list price is goods the business gave away being "+
			"invoiced for", got)
	}

	// And a stated rate that disagrees with the list is the operator's call.
	if got := rateOf(t, line(map[string]any{"rate": "99.0000"})); got != "99.0000" {
		t.Errorf("a stated rate came back at %q, want 99.0000 — the price list must not "+
			"override a rate the operator typed", got)
	}
}
