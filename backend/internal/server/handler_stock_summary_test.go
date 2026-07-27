package server_test

// handler_stock_summary_test.go — live-Postgres HTTP tests for
// GET /v1/companies/{companyID}/stock-summary (wave-1 INV-F).
//
// The oracle is deliberately NOT "the handler returned some JSON". Stock value
// is never stored, so the only way to know the endpoint is wired to the REPLAY
// and not to something that looks like it is to post movements whose correct
// answer differs from the plausible-but-wrong one:
//
//   - buy 10 @ 100 and 10 @ 120, then sell 5 at a SALE price of 200.
//   - the right answer prices the issue at the weighted-average COST (110), so
//     outward_value is 550 and closing is 15 @ 110 = 1650.
//   - a handler wired to a SUM(qty*rate) over the movement rows would report an
//     outward value of 1000 (the sale price) and a closing value of 1200 —
//     confident, plausible, and it zeroes the gross profit.
//
// Every assertion below is chosen so that the two answers differ.

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/server"
)

// stockCompany creates a company with the ledgers, unit and stock item the
// inventory tests need, and returns the base path plus the ids a voucher body
// has to carry.
type stockFixture struct {
	base       string
	creditorID string
	purchaseID string
	debtorID   string
	salesID    string
	itemID     string
	godownID   string
}

func newStockFixture(t *testing.T, srv *server.Server) stockFixture {
	t.Helper()
	_, base := newCompany(t, srv)
	f := stockFixture{
		base:       base,
		creditorID: createLedger(t, srv, base, "Stock Supplier", "Sundry Creditors"),
		purchaseID: createLedger(t, srv, base, "Stock Purchases", "Purchase Accounts"),
		debtorID:   createLedger(t, srv, base, "Stock Customer", "Sundry Debtors"),
		salesID:    createLedger(t, srv, base, "Stock Sales", "Sales Accounts"),
	}

	// "Nos" is seeded with every company (seed_phase1_voucher_types), and GET
	// /units is the only way a client can discover that. Reading it here is not
	// incidental: it is the same hole GET /ledgers closed for the chart of
	// accounts — a caller that cannot enumerate a seeded master invents a
	// duplicate of it.
	rec, body := do(t, srv, http.MethodGet, base+"/units", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /units = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	sawNos := false
	for _, u := range list(t, body, "units") {
		if u["symbol"] == "Nos" {
			sawNos = true
		}
	}
	if !sawNos {
		t.Fatalf("GET /units did not list the seeded 'Nos' unit: %v — an item grid "+
			"cannot offer a unit picker against a collection it cannot enumerate", body)
	}

	rec, body = do(t, srv, http.MethodPost, base+"/stock-items", map[string]any{
		"name": "Widget", "unit_symbol": "Nos", "hsn": "8471", "created_by": "inv",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create stock item = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	f.itemID, _ = body["stock_item_id"].(string)

	// Every company is seeded with the system godown 'Main Location'
	// (seed_inventory_defaults). Resolving it through the API is the point of
	// GET /godowns: without it a client would have to mint a second location
	// that means the same thing.
	rec, body = do(t, srv, http.MethodGet, base+"/godowns", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /godowns = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	for _, g := range list(t, body, "godowns") {
		if g["name"] == "Main Location" {
			f.godownID, _ = g["id"].(string)
			if g["is_system"] != true {
				t.Fatalf("'Main Location' came back with is_system=%v, want true — a client "+
					"cannot tell the seeded location from a user's own without it", g["is_system"])
			}
		}
	}
	if f.godownID == "" {
		t.Fatalf("GET /godowns did not list the seeded 'Main Location': %v", body)
	}
	return f
}

// purchase posts an inward of qty at rate, debiting Purchases and crediting the
// supplier for the same amount, so the books stay balanced while the stock moves.
func (f stockFixture) purchase(t *testing.T, srv *server.Server, no, date, qty, rate, amount string) {
	t.Helper()
	rec, _ := do(t, srv, http.MethodPost, f.base+"/vouchers/purchase", map[string]any{
		"voucher_no": no, "voucher_date": date, "created_by": "inv",
		"postings": []map[string]any{
			{"ledger_id": f.purchaseID, "dr_cr": "D", "amount": amount},
			{"ledger_id": f.creditorID, "dr_cr": "C", "amount": amount},
		},
		"inventory": []map[string]any{
			{"stock_item_id": f.itemID, "godown_id": f.godownID, "batch_id": "",
				"direction": "IN", "qty": qty, "rate": rate, "amount": amount},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("purchase %s = %d, want 201 (%s)", no, rec.Code, rec.Body.String())
	}
}

// TestStockSummaryValuesTheIssueAtCostNotAtTheSalePrice is the flagship: the
// endpoint must report cost of goods sold, not revenue, in its outward column.
func TestStockSummaryValuesTheIssueAtCostNotAtTheSalePrice(t *testing.T) {
	srv := newServer(t)
	f := newStockFixture(t, srv)

	f.purchase(t, srv, "PUR-1", "2025-04-01", "10", "100", "1000")
	f.purchase(t, srv, "PUR-2", "2025-04-05", "10", "120", "1200")

	// Sell 5 at 200 each. The SALE price is 200; the COST of those five is the
	// running weighted average, (1000 + 1200) / 20 = 110.
	rec, _ := do(t, srv, http.MethodPost, f.base+"/vouchers/sales", map[string]any{
		"voucher_no": "INV-1", "voucher_date": "2025-04-10", "created_by": "inv",
		"postings": []map[string]any{
			{"ledger_id": f.debtorID, "dr_cr": "D", "amount": "1000"},
			{"ledger_id": f.salesID, "dr_cr": "C", "amount": "1000"},
		},
		"inventory": []map[string]any{
			{"stock_item_id": f.itemID, "godown_id": f.godownID,
				"direction": "OUT", "qty": "5", "rate": "200", "amount": "1000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("sale = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	rec, body := do(t, srv, http.MethodGet, f.base+"/stock-summary?as_on=2025-04-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-summary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["as_on"] != "2025-04-30" {
		t.Errorf("stock summary as_on = %v, want the date asked for", body["as_on"])
	}
	if body["total_value"] != "1650.0000" {
		t.Errorf("total inventory value = %v, want 1650.0000 — this is what a Stock-in-Hand "+
			"line on the Balance Sheet is worth; 1200.0000 means the issue was priced at the "+
			"sale price instead of at cost", body["total_value"])
	}

	rows := list(t, body, "rows")
	if len(rows) != 1 {
		t.Fatalf("stock summary has %d position rows, want exactly 1 (item x Main Location): %v", len(rows), rows)
	}
	row := rows[0]
	for _, tc := range []struct{ field, want, why string }{
		{"closing_qty", "15.0000", "20 bought less 5 issued"},
		{"closing_value", "1650.0000", "15 units carried at the weighted-average cost of 110"},
		{"closing_rate", "110.0000", "(1000 + 1200) / 20 units"},
		{"inward_value", "2200.0000", "what the goods cost to buy"},
		{"outward_value", "550.0000", "COST of the 5 issued (5 x 110), NOT the 1000 they sold for — " +
			"booking revenue as cost is what makes gross profit come out as zero"},
		{"outward_qty", "5.0000", "quantity is order-independent and comes straight from the movements"},
		{"valuation_method", "WEIGHTED_AVG", "a report must be able to state its own basis"},
		{"godown", "Main Location", "stock always sits in a named place"},
		{"batch", "", "an untracked dimension is empty, not a phantom position"},
	} {
		if row[tc.field] != tc.want {
			t.Errorf("stock summary row %s = %v, want %s (%s)", tc.field, row[tc.field], tc.want, tc.why)
		}
	}

	// The item rollup folds from the same rows and deliberately carries NO
	// closing_rate: a blended rate across locations belongs to none of them.
	items := list(t, body, "items")
	if len(items) != 1 {
		t.Fatalf("stock summary has %d item rollups, want 1: %v", len(items), items)
	}
	if items[0]["closing_value"] != "1650.0000" || items[0]["item"] != "Widget" {
		t.Errorf("item rollup = %v, want Widget closing 1650.0000", items[0])
	}
	if _, present := items[0]["closing_rate"]; present {
		t.Errorf("item rollup carries a closing_rate: an average of two locations' averages is "+
			"not an average, and no user can act on it (%v)", items[0])
	}

	// A malformed as_on must be refused rather than silently answering a
	// different question than the one on screen.
	if rec, _ := do(t, srv, http.MethodGet, f.base+"/stock-summary?as_on=30-04-2025", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("GET /stock-summary?as_on=30-04-2025 = %d, want 400", rec.Code)
	}
}

// TestStockSummaryRouteAuthMatrix pins the endpoint's place in the 401/403/200
// contract, INCLUDING the half that is easiest to get wrong: it is a DISPLAY,
// so it must not inherit the Owner level's WebAuthn step-up. Mapping it to a
// write action would compile, serve, and demand a second factor to look at a
// warehouse.
func TestStockSummaryRouteAuthMatrix(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, ownerSubj := newAdminCompany(t, srv)
	base := "/v1/companies/" + companyID

	// No token at all: 401, RESERVED for missing/invalid credentials.
	if rec, _ := doRaw(t, srv, http.MethodGet, base+"/stock-summary", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /stock-summary with no token = %d, want 401", rec.Code)
	}

	// A perfectly valid token belonging to ANOTHER tenant's Owner: 403, not 401
	// — the caller is authenticated, they are simply not a member here.
	otherCompanyID, otherTok, _ := newAdminCompany(t, srv)
	if otherCompanyID == companyID {
		t.Fatal("the two fixture companies collided")
	}
	if rec, _ := doRaw(t, srv, http.MethodGet, base+"/stock-summary", otherTok, nil); rec.Code != http.StatusForbidden {
		t.Errorf("GET /stock-summary as another tenant's Owner = %d, want 403", rec.Code)
	}

	// The Owner of this company: 200.
	if rec, _ := doRaw(t, srv, http.MethodGet, base+"/stock-summary", ownerTok, nil); rec.Code != http.StatusOK {
		t.Errorf("GET /stock-summary as Owner = %d, want 200", rec.Code)
	}

	// THE LOAD-BEARING CASE. Same Owner, same rights, token WITHOUT a WebAuthn
	// AMR. The Owner level has requires_webauthn, and the step-up gates writes
	// only (authz.Action.IsWrite). A read that answered STEPUP_REQUIRED here
	// would mean the route was mapped to a write action — the endpoint would
	// still "work" for anyone who happened to have stepped up, so this is the
	// only assertion that catches it.
	weak := mintToken(t, srv, ownerSubj, "pwd")
	rec, body := doRaw(t, srv, http.MethodGet, base+"/stock-summary", weak, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-summary without a WebAuthn AMR = %d %v, want 200 — reading the stock "+
			"position is a Display and must not inherit the write step-up", rec.Code, body)
	}
	if body["code"] == "STEPUP_REQUIRED" {
		t.Fatalf("GET /stock-summary demanded a WebAuthn step-up: the route is mapped to a WRITE action")
	}
}

// TestDataEntryCanReadStockSummaryButNotCreateGodowns pins the WIDTH of what an
// operator gets in the inventory world: they may look at what is on hand and
// pick from the locations and batches a supervisor defined, and they may not
// mint new ones.
//
// Both halves matter. A grant test that only checks the 403s passes just as
// well when the feature is broken and the picker is empty; a test that only
// checks the 200s passes when every operator can restructure the warehouse.
func TestDataEntryCanReadStockSummaryButNotCreateGodowns(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	base := "/v1/companies/" + companyID

	// The Owner defines the masters an operator will pick from.
	rec, _ := doRaw(t, srv, http.MethodPost, base+"/stock-items", ownerTok,
		map[string]any{"name": "Bolt", "unit_symbol": "Nos", "created_by": "owner"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("owner creates stock item = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	rec, _ = doRaw(t, srv, http.MethodPost, base+"/batches", ownerTok,
		map[string]any{"stock_item_name": "Bolt", "name": "LOT-A", "expiry_date": "2027-01-31", "created_by": "owner"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("owner creates batch = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	de := inventoryMemberToken(t, srv, base, ownerTok, "Data Entry")

	// THE STOCK SUMMARY IS DENIED, and it is the only inventory read that is.
	//
	// It prints the company's own COST prices — the closing valuation rate is
	// what the business PAID — which is commercially sensitive in a way the
	// chart of accounts is not. Migration 0026 seeds an explicit
	// `disallow display report:stock-summary` on the Data Entry level, which
	// beats the coarse `allow display report` it has carried since 0011 because
	// report:NAME scores specificity 2 against a bare resource's 1.
	//
	// Without that rule this feature would have handed every operator in every
	// tenant the cost book, by inheritance, with nobody deciding to — the same
	// open-ended grant will do it again for the next report added.
	if rec, b := doRaw(t, srv, http.MethodGet, base+"/stock-summary", de, nil); rec.Code != http.StatusForbidden {
		t.Errorf("as Data Entry, GET /stock-summary = %d %v, want 403 — the closing "+
			"valuation rate is the company's own cost price and an operator keying "+
			"invoices has no need of it", rec.Code, b)
	}

	// POSITIVE — without these the item grid has no pickers and the operator
	// cannot see whether the goods they are invoicing exist. Note what this
	// costs: an operator can no longer answer "do we have 10 to sell?", because
	// quantity and value live on one report. A quantity-only variant they CAN
	// see is the tracked follow-up; column-level ACL is not built.
	for _, tc := range []struct{ what, path string }{
		{"pick a location", base + "/godowns"},
		{"pick a batch", base + "/batches"},
		{"pick an item", base + "/stock-items"},
		{"pick a unit", base + "/units"},
	} {
		if rec, b := doRaw(t, srv, http.MethodGet, tc.path, de, nil); rec.Code != http.StatusOK {
			t.Errorf("as Data Entry, %s (GET %s) = %d %v, want 200", tc.what, tc.path, rec.Code, b)
		}
	}

	// The batch read is real, not an empty array that happens to be a 200.
	_, body := doRaw(t, srv, http.MethodGet, base+"/batches", de, nil)
	batches := list(t, body, "batches")
	if len(batches) != 1 || batches[0]["name"] != "LOT-A" ||
		batches[0]["stock_item_name"] != "Bolt" || batches[0]["expiry_date"] != "2027-01-31" {
		t.Errorf("Data Entry's batch picker = %v, want one LOT-A of Bolt expiring 2027-01-31", batches)
	}

	// NEGATIVE — reading a master is not the same as defining one. An operator
	// who could mint a godown or a batch could quietly move stock somewhere
	// nobody reconciles.
	for _, tc := range []struct {
		what, path string
		body       map[string]any
	}{
		{"create a godown", base + "/godowns", map[string]any{"name": "Rogue Store"}},
		{"create a batch", base + "/batches", map[string]any{"stock_item_name": "Bolt", "name": "LOT-B"}},
		{"create a stock item", base + "/stock-items", map[string]any{"name": "Rogue Item", "unit_symbol": "Nos"}},
		{"create a unit", base + "/units", map[string]any{"symbol": "Box"}},
	} {
		if rec, b := doRaw(t, srv, http.MethodPost, tc.path, de, tc.body); rec.Code != http.StatusForbidden {
			t.Errorf("as Data Entry, %s (POST %s) = %d %v, want 403 — an operator picks from the "+
				"masters a supervisor defined and cannot mint one", tc.what, tc.path, rec.Code, b)
		}
	}
}

// inventoryMemberToken invites a fresh subject at the named security level,
// claims the invite (GET /v1/me is what claims it), and returns their token.
func inventoryMemberToken(t *testing.T, srv *server.Server, base, ownerTok, levelName string) string {
	t.Helper()
	subject := "inv-member-" + uuid.NewString()[:8]
	rec, _ := doRaw(t, srv, http.MethodPost, base+"/members/invites", ownerTok,
		map[string]any{"subject": subject, "security_level_name": levelName})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite at %q = %d, want 201 (%s)", levelName, rec.Code, rec.Body.String())
	}
	tok := mintToken(t, srv, subject, "webauthn")
	rec, body := doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil)
	if rec.Code != http.StatusOK || len(list(t, body, "claimed_invites")) != 1 {
		t.Fatalf("claiming the %q invite = %d (%s)", levelName, rec.Code, rec.Body.String())
	}
	return tok
}
