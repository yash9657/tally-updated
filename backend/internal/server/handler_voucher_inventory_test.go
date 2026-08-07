package server_test

// handler_voucher_inventory_test.go — live-Postgres HTTP tests for STOCK ON THE
// VOUCHER WIRE (wave-2 INV-J).
//
// Three questions, in the order a client hits them:
//
//  1. can an F8/F9 item grid actually POST an item invoice? (it could not before
//     this slice: `item_ledger_id` had no wire field, so every item invoice the
//     grid could build came back 422);
//  2. does GET /vouchers/{id} project the stock lines in the shape the request
//     side reads — real uuids, fixed-4 STRINGS; and
//  3. does an ALTER that echoes that projection back PRESERVE the movements.
//
// (3) is the one that matters most, and it is the reason this file exists at
// all. A voucher is RESTATED in full on every alter — the new version is built
// only from what the request carried — so any field the read shape forgets is a
// field the next alter deletes. For stock that loss is silent: the accounting
// legs come back untouched, the trial balance is unchanged, and the goods have
// simply left the movement ledger with no report anywhere out of balance. So the
// test PUTs the FETCHED BODY back, not a hand-written one; a hand-written body
// would test the author's memory of the shape rather than the shape itself.

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/server"
)

// itemWireEnv is this file's own fixture. It deliberately does NOT reuse
// stockFixture from handler_stock_summary_test.go: that file belongs to another
// slice, and a shared helper edited under one test's assumptions silently
// changes what the other one proves.
type itemWireEnv struct {
	base string
	// Accounting ledgers. A Sales-side and a Purchase-side pair, because the two
	// item-invoice orientations bill opposite parties.
	debtorID   string
	salesID    string
	creditorID string
	purchaseID string
	cgstID     string
	sgstID     string
	igstID     string
	// Stock masters.
	itemID   string
	godownID string
	batchID  string
}

func newItemWireEnv(t *testing.T, srv *server.Server) itemWireEnv {
	t.Helper()
	_, base := newCompany(t, srv)
	e := itemWireEnv{
		base:       base,
		debtorID:   createLedger(t, srv, base, "Wire Customer", "Sundry Debtors"),
		salesID:    createLedger(t, srv, base, "Domestic Sales", "Sales Accounts"),
		creditorID: createLedger(t, srv, base, "Wire Supplier", "Sundry Creditors"),
		purchaseID: createLedger(t, srv, base, "Domestic Purchases", "Purchase Accounts"),
		cgstID:     createLedger(t, srv, base, "Output CGST", "Duties & Taxes"),
		sgstID:     createLedger(t, srv, base, "Output SGST", "Duties & Taxes"),
		igstID:     createLedger(t, srv, base, "Output IGST", "Duties & Taxes"),
	}

	// HSN and rate live on the MASTER, never on the voucher: they are the tax
	// identity of the goods. An item invoice reads them from here, which is what
	// stops two invoices for the same item filing under two different HSNs.
	rec, body := do(t, srv, http.MethodPost, base+"/stock-items", map[string]any{
		"name": "Hex Bolt", "unit_symbol": "Nos", "hsn": "7318",
		"gst_rate_pct": "18", "created_by": "inv-j",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create stock item = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	e.itemID, _ = body["stock_item_id"].(string)

	// The seeded system godown. Resolving it through GET /godowns rather than
	// creating one is the point: a client that cannot enumerate a seeded master
	// invents a duplicate of it.
	rec, body = do(t, srv, http.MethodGet, base+"/godowns", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /godowns = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	for _, g := range list(t, body, "godowns") {
		if g["name"] == "Main Location" {
			e.godownID, _ = g["id"].(string)
		}
	}
	if e.godownID == "" {
		t.Fatalf("GET /godowns did not list the seeded 'Main Location': %v", body)
	}

	rec, body = do(t, srv, http.MethodPost, base+"/batches", map[string]any{
		"stock_item_name": "Hex Bolt", "name": "LOT-2025-05",
		"expiry_date": "2026-05-31", "created_by": "inv-j",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create batch = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	e.batchID, _ = body["batch_id"].(string)
	return e
}

// stockPurchase posts an ACCOUNTING voucher that also moves stock: explicit
// postings, no gst block. That shape (not the item invoice) is what the alter
// echo test needs, because GET projects the GENERATED postings of an item
// invoice and re-sending those is refused by design — see the blocker note.
func (e itemWireEnv) stockPurchase(t *testing.T, srv *server.Server) string {
	t.Helper()
	rec, body := do(t, srv, http.MethodPost, e.base+"/vouchers/purchase", map[string]any{
		"voucher_no": "PUR/0001", "voucher_date": "2025-05-10",
		"party": "Wire Supplier", "party_ledger_id": e.creditorID, "created_by": "inv-j",
		"postings": []map[string]any{
			{"ledger_id": e.purchaseID, "dr_cr": "D", "amount": "1000.0000"},
			{"ledger_id": e.creditorID, "dr_cr": "C", "amount": "1000.0000"},
		},
		"inventory": []map[string]any{
			// Line 1 carries BOTH optional dimensions, line 2 carries neither:
			// the projection has to render a tracked dimension as a uuid and an
			// untracked one as "" (never the all-zero uuid, which the next PUT
			// would reject as a bad id).
			{"stock_item_id": e.itemID, "godown_id": e.godownID, "batch_id": e.batchID,
				"direction": "IN", "qty": "6.0000", "rate": "100.0000", "amount": "600.0000"},
			{"stock_item_id": e.itemID, "godown_id": "", "batch_id": "",
				"direction": "IN", "qty": "4.0000", "rate": "100.0000", "amount": "400.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post purchase with stock = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	id, _ := body["voucher_id"].(string)
	if id == "" {
		t.Fatalf("post purchase returned no voucher_id: %s", rec.Body.String())
	}
	return id
}

// receiveStock brings qty of the item INTO one (godown, batch) position with an
// ordinary accounting purchase, so a later outward has something to consume.
// Selling stock the company does not hold is refused by the deferred
// negative-stock trigger (migration 0025) — correctly, and it is why an item
// invoice test cannot start with a sale.
func (e itemWireEnv) receiveStock(t *testing.T, srv *server.Server, no, qty, rate, amount, godownID, batchID string) {
	t.Helper()
	rec, _ := do(t, srv, http.MethodPost, e.base+"/vouchers/purchase", map[string]any{
		"voucher_no": no, "voucher_date": "2025-05-01",
		"party_ledger_id": e.creditorID, "created_by": "inv-j",
		"postings": []map[string]any{
			{"ledger_id": e.purchaseID, "dr_cr": "D", "amount": amount},
			{"ledger_id": e.creditorID, "dr_cr": "C", "amount": amount},
		},
		"inventory": []map[string]any{
			{"stock_item_id": e.itemID, "godown_id": godownID, "batch_id": batchID,
				"direction": "IN", "qty": qty, "rate": rate, "amount": amount},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("receive stock %s = %d, want 201 (%s)", no, rec.Code, rec.Body.String())
	}
}

// fixed4 is the wire form every quantity and every money value on this surface
// must take: a STRING with exactly four decimals.
var fixed4 = regexp.MustCompile(`^-?\d+\.\d{4}$`)

// ---------------------------------------------------------------------------
// 1. The wiring this slice exists for: item_ledger_id on the voucher body.
// ---------------------------------------------------------------------------

// TestItemInvoiceOverHTTPDerivesAccountingAndTax posts the EXACT body an F8 item
// grid sends — party + item ledger, stock lines, gst parameters, and NO postings
// and NO gst.lines — and asserts the server generated the whole accounting and
// tax side from the goods.
//
// The oracle is chosen so a mis-wired server cannot pass by accident: 10 @ 100
// less 10% is a taxable 900, and 18% intra-state is CGST 81 + SGST 81, so the
// party is billed 1062. A server that ignored the discount would bill 1180; one
// that treated the invoice as inter-state would post a single IGST 162 leg.
//
// It is ALSO the regression test for the field this slice added: without
// item_ledger_id on the wire the ledger cannot name the account the goods are
// booked to, and every item invoice is a 422.
func TestItemInvoiceOverHTTPDerivesAccountingAndTax(t *testing.T) {
	srv := newServer(t)
	e := newItemWireEnv(t, srv)
	// Buy the goods before selling them: the sale is an OUTWARD movement and
	// the company has to hold the stock it is invoicing.
	e.receiveStock(t, srv, "PUR/0001", "10.0000", "60.0000", "600.0000", e.godownID, "")

	rec, body := do(t, srv, http.MethodPost, e.base+"/vouchers/sales", map[string]any{
		"voucher_date": "2025-05-10", "voucher_no": "",
		"party": "Wire Customer", "party_ledger_id": e.debtorID,
		"item_ledger_id": e.salesID, "created_by": "inv-j",
		"inventory": []map[string]any{
			{"stock_item_id": e.itemID, "godown_id": e.godownID, "batch_id": "",
				"direction": "OUT", "qty": "10.0000", "rate": "100.0000", "discount_pct": "10.0000"},
		},
		"gst": map[string]any{
			"supplier_state_code": "29", "place_of_supply_state_code": "29",
			"cgst_ledger_id": e.cgstID, "sgst_ledger_id": e.sgstID, "igst_ledger_id": e.igstID,
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post item invoice = %d, want 201 (%s)\n"+
			"an F8 item grid cannot post at all if this fails: item_ledger_id is the field "+
			"ledger.validateItemInvoice requires and the wire had no place for", rec.Code, rec.Body.String())
	}
	// The tax on the RESPONSE is the server-derived tax, which is what an
	// invoice screen must print rather than recomputing it in the browser.
	for _, c := range []struct{ key, want string }{
		{"cgst", "81.0000"}, {"sgst", "81.0000"}, {"igst", "0.0000"},
	} {
		if body[c.key] != c.want {
			t.Fatalf("item invoice response %s = %v, want %s — the tax the screen prints "+
				"comes from here, so a wrong one is a wrong invoice", c.key, body[c.key], c.want)
		}
	}
	voucherID, _ := body["voucher_id"].(string)

	rec, body = do(t, srv, http.MethodGet, e.base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get item invoice = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	// The four GENERATED legs, by ledger id. Asserting by id (not by position)
	// is what makes a reversed orientation — Cr the customer, Dr the sales
	// account — a failure rather than a reordering.
	wantLegs := map[string][2]string{
		e.debtorID: {"D", "1062.0000"},
		e.salesID:  {"C", "900.0000"},
		e.cgstID:   {"C", "81.0000"},
		e.sgstID:   {"C", "81.0000"},
	}
	got := map[string][2]string{}
	for _, p := range list(t, body, "postings") {
		id, _ := p["ledger_id"].(string)
		drcr, _ := p["dr_cr"].(string)
		amt, _ := p["amount"].(string)
		got[id] = [2]string{drcr, amt}
	}
	if len(got) != len(wantLegs) {
		t.Fatalf("item invoice generated %d postings, want %d (%v) — a zero tax component "+
			"must get NO posting at all, and every non-zero one exactly one", len(got), len(wantLegs), got)
	}
	for id, want := range wantLegs {
		if got[id] != want {
			t.Errorf("generated posting on ledger %s = %v, want %v (Dr/Cr, amount)", id, got[id], want)
		}
	}
	// And the goods themselves are on the voucher, at the SERVER-derived line
	// total (900 = 10 x 100 less 10%), not at the undiscounted 1000.
	inv := list(t, body, "inventory")
	if len(inv) != 1 || inv[0]["amount"] != "900.0000" || inv[0]["direction"] != "OUT" {
		t.Fatalf("item invoice inventory = %v, want one OUT line of 900.0000 — the movement "+
			"and the taxable base are tied by migration 0027 and must be the same 900", inv)
	}
}

// ---------------------------------------------------------------------------
// 2. The read shape.
// ---------------------------------------------------------------------------

// TestGetVoucherJSONCarriesInventoryLines pins the projection field by field:
// ids as uuid STRINGS, every quantity and money value as a fixed-4 STRING, and
// an untracked dimension as "" rather than the all-zero uuid.
//
// Numbers-as-strings is not a style preference. A JSON number is a float64 in
// every client that parses it, and a float quantity makes a closing stock
// balance depend on the order the lines were entered — so the value the report
// derives stops matching the value the invoice printed, with nothing anywhere
// out of balance to reveal it.
func TestGetVoucherJSONCarriesInventoryLines(t *testing.T) {
	srv := newServer(t)
	e := newItemWireEnv(t, srv)
	voucherID := e.stockPurchase(t, srv)

	rec, body := do(t, srv, http.MethodGet, e.base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get voucher = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	inv := list(t, body, "inventory")
	if len(inv) != 2 {
		t.Fatalf("GET projected %d inventory lines, want 2 — a client that cannot read the "+
			"stock lines back cannot restate them on an alter, and the next PUT deletes them", len(inv))
	}

	// The two lines, in the order they were keyed: line 1 fully dimensioned,
	// line 2 untracked on both optional dimensions.
	cases := []struct {
		lineNo   int
		godown   string
		batch    string
		qty      string
		rate     string
		discount string
		amount   string
	}{
		{lineNo: 1, godown: e.godownID, batch: e.batchID, qty: "6.0000", rate: "100.0000", discount: "0.0000", amount: "600.0000"},
		{lineNo: 2, godown: "", batch: "", qty: "4.0000", rate: "100.0000", discount: "0.0000", amount: "400.0000"},
	}
	for i, c := range cases {
		line := inv[i]

		// Ids: strings, and REAL uuids. Re-resolving a line by name on the way
		// back in would drop any line whose item was renamed between the fetch
		// and the save — an edit of the narration becoming a write-off.
		itemID, ok := line["stock_item_id"].(string)
		if !ok || itemID != e.itemID {
			t.Errorf("line %d stock_item_id = %v (%T), want the posted uuid %s as a string",
				c.lineNo, line["stock_item_id"], line["stock_item_id"], e.itemID)
		}
		for _, dim := range []struct{ key, want string }{
			{"godown_id", c.godown}, {"batch_id", c.batch},
		} {
			gotDim, ok := line[dim.key].(string)
			if !ok || gotDim != dim.want {
				t.Errorf("line %d %s = %v (%T), want %q — an untracked dimension must render "+
					"as \"\" (the token the request side reads as NULL), never as the zero uuid",
					c.lineNo, dim.key, line[dim.key], line[dim.key], dim.want)
			}
			if gotDim != "" {
				if _, err := uuid.Parse(gotDim); err != nil {
					t.Errorf("line %d %s = %q is not a uuid: %v", c.lineNo, dim.key, gotDim, err)
				}
			}
		}

		// Quantities and money: STRINGS, fixed to four decimals.
		for _, f := range []struct{ key, want string }{
			{"qty", c.qty}, {"rate", c.rate},
			{"discount_pct", c.discount}, {"amount", c.amount},
		} {
			s, ok := line[f.key].(string)
			if !ok {
				t.Errorf("line %d %s = %v serialised as %T, want a fixed-4 STRING: a JSON number "+
					"is a float64 in the client, and a float quantity makes the closing stock "+
					"balance depend on data-entry order", c.lineNo, f.key, line[f.key], line[f.key])
				continue
			}
			if !fixed4.MatchString(s) {
				t.Errorf("line %d %s = %q is not fixed-4 (a printed invoice and a derived report "+
					"must agree to the paisa)", c.lineNo, f.key, s)
			}
			if s != f.want {
				t.Errorf("line %d %s = %q, want %q", c.lineNo, f.key, s, f.want)
			}
		}

		if line["direction"] != "IN" {
			t.Errorf("line %d direction = %v, want IN — the direction carries the sign of the "+
				"movement, so a flipped one turns a receipt into an issue", c.lineNo, line["direction"])
		}
	}
}

// ---------------------------------------------------------------------------
// 3. The flagship: the alter round trip over HTTP.
// ---------------------------------------------------------------------------

// echoRequest turns a fetched voucher into the PUT body a client sends back when
// it alters one field. It copies ONLY what the request shape understands, and it
// copies it VERBATIM — including `inventory`, or NOT including it when the
// projection did not carry it, which is exactly the failure this test hunts.
//
// It must not t.Fatal on a missing key: the whole point is that a projection
// which silently omits the stock lines produces a body a client would send
// happily, and the damage shows up in the movement ledger rather than here.
func echoRequest(view map[string]any) map[string]any {
	req := map[string]any{"created_by": "inv-j-alter"}
	for _, k := range []string{
		"voucher_no", "voucher_date", "party", "party_ledger_id", "narration",
		"postings", "bill_allocations", "inventory",
	} {
		if v, ok := view[k]; ok {
			req[k] = v
		}
	}
	return req
}

// inventoryOf reads the projected stock lines WITHOUT failing when the key is
// missing, deliberately unlike list(): a projection that omits `inventory`
// entirely is precisely the bug TestAlterViaHTTPPreservesStock is hunting, and a
// helper that stopped the test there would report it as "the read shape is
// wrong" instead of as what it actually costs — the stock silently written off
// by the next save.
func inventoryOf(view map[string]any) []map[string]any {
	raw, ok := view["inventory"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		m, _ := e.(map[string]any)
		out = append(out, m)
	}
	return out
}

// closingPosition reads the closing QUANTITY AND VALUE of one item off
// GET /stock-summary. The stock summary is derived by replaying the movement
// ledger, so it is the honest witness to whether the movements still exist:
// nothing is stored for it to read stale.
//
// ⛔⛔ IT RETURNS THE VALUE BECAUSE A QUANTITY-ONLY ORACLE IS BLIND TO THE
// EXPENSIVE HALF OF THIS DEFECT CLASS. It used to return the quantity alone, and
// a projection that handed the grid back WITHOUT THE PRICE — `"rate"` and
// `"amount"` projected as "0.0000" in inventoryLinesJSON — left
// TestAlterViaHTTPPreservesStock GREEN while the round trip restated ten units
// at no cost and wrote 1,000 of stock value off the Balance Sheet. Measured, not
// reasoned about: that mutation was run, this test passed, and the audit test in
// voucher_readback_audit_test.go was the only thing that failed.
//
// The value is DERIVED from qty x rate less discount by internal/inventory's
// valuer and never read out of stock_movement.amount, which is why the RATE is
// the field a lossy projection costs real money on.
func closingPosition(t *testing.T, srv *server.Server, base, itemID string) (qty, value string) {
	t.Helper()
	rec, body := do(t, srv, http.MethodGet, base+"/stock-summary", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-summary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	for _, it := range list(t, body, "items") {
		if it["stock_item_id"] == itemID {
			q, _ := it["closing_qty"].(string)
			v, _ := it["closing_value"].(string)
			return q, v
		}
	}
	return "", "" // no position at all: every movement is gone.
}

// TestAlterViaHTTPPreservesStock is the HTTP expression of the invariant the
// whole inventory feature rests on: GET a voucher that moved stock, PUT the same
// body back unchanged, and the movements must be identical.
//
// A voucher is RESTATED on an alter, so the projection and the request shape
// have to agree completely. If the projection drops the stock lines, this
// round trip — the most ordinary thing a client does, "load it, change the
// narration, save it" — writes a version with NO movements. The accounting legs
// survive, the trial balance is unchanged, the edit log shows a tidy version 2,
// and ten bolts have left the warehouse with nothing anywhere out of balance to
// say so. That is why the closing-quantity assertion below is the point of the
// test and the postings assertion is only its control.
func TestAlterViaHTTPPreservesStock(t *testing.T) {
	srv := newServer(t)
	e := newItemWireEnv(t, srv)
	voucherID := e.stockPurchase(t, srv)

	if q, v := closingPosition(t, srv, e.base, e.itemID); q != "10.0000" || v != "1000.0000" {
		t.Fatalf("after the purchase the shelf reads qty=%q value=%q, want 10.0000 / 1000.0000", q, v)
	}

	rec, view := do(t, srv, http.MethodGet, e.base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get voucher = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	before := inventoryOf(view)

	// The alter: the fetched body, sent back unchanged apart from the narration
	// a user would have typed. Nothing about the stock is re-keyed.
	req := echoRequest(view)
	req["narration"] = "corrected the narration, nothing else"
	rec, body := do(t, srv, http.MethodPut, e.base+"/vouchers/purchase/"+voucherID, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("alter voucher = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if v, _ := body["version_no"].(float64); v != 2 {
		t.Fatalf("alter returned version_no = %v, want 2 (the alter must APPEND a version, "+
			"not rewrite version 1)", body["version_no"])
	}

	// THE ASSERTION, IN BOTH DIMENSIONS. Derived by replaying the movement ledger,
	// so a zero here means the movements are actually gone, not merely
	// mis-reported.
	//
	// ⛔ THE VALUE IS NOT DECORATION ON THE QUANTITY. A projection can hand back a
	// grid that restates every unit and no PRICE, and then the quantity survives
	// while the goods land in closing stock — a BALANCE SHEET line — at nothing.
	// This test was green through exactly that mutation until the value was
	// asserted here; see closingPosition.
	if q, v := closingPosition(t, srv, e.base, e.itemID); q != "10.0000" || v != "1000.0000" {
		t.Fatalf("after a NO-OP alter the shelf reads qty=%q value=%q, want 10.0000 / 1000.0000 — "+
			"the round trip GET -> PUT wrote the stock off. Every field the projection omits (or "+
			"zeroes) is a field the next alter deletes, and nothing in the books goes out of "+
			"balance to show it", q, v)
	}

	rec, after := do(t, srv, http.MethodGet, e.base+"/vouchers/"+voucherID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-get voucher = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := inventoryOf(after)
	if len(before) != 2 || len(got) != len(before) {
		t.Fatalf("stock lines on the voucher: %d before the alter, %d after, want 2 and 2 — "+
			"the projection and the request shape have to carry the same lines for a fetch-edit-save "+
			"cycle to be safe", len(before), len(got))
	}
	// Field-for-field: the dimensions and the numbers must be the ones the
	// projection handed out, or the "echo" was not an echo.
	for i := range before {
		for _, k := range []string{
			"stock_item_id", "godown_id", "batch_id", "direction",
			"qty", "rate", "discount_pct", "amount",
		} {
			if got[i][k] != before[i][k] {
				t.Errorf("line %d %s changed across a no-op alter: %v -> %v",
					i+1, k, before[i][k], got[i][k])
			}
		}
	}
	// The control: the accounting legs DID survive. Without this, a projection
	// bug that broke the whole alter would look like a passing stock test.
	if n := len(list(t, after, "postings")); n != 2 {
		t.Fatalf("after the alter the voucher carries %d postings, want 2 — the accounting "+
			"half of the round trip must be intact for the stock half to mean anything", n)
	}
}

// ---------------------------------------------------------------------------
// 4. The OTHER door into the same translator: POST /sync/commit.
// ---------------------------------------------------------------------------

// TestSyncCommitFromAnOfflineNodeCarriesStock proves the claim voucherReq's own
// comment makes and nothing asserted: an offline node's stock lines survive the
// cloud sequencer.
//
// The claim is structural — syncIntentReq.Payload IS a voucherReq and
// buildVoucherInput is the SINGLE translator, so /sync/commit carries stock "for
// free". Structural claims are exactly the ones that rot: the cheap way to add a
// field to the sync wire is a second, sync-specific payload struct, and such a
// struct compiles, decodes, returns 200 COMMITTED with a final voucher number,
// and drops every stock line the node ever recorded. A desk that keyed a week of
// delivery notes offline would find its accounting complete to the rupee and its
// warehouse empty.
//
// So the assertion is the CLOSING QUANTITY, read off the derived stock summary,
// not the shape of the intent: it is the only thing that distinguishes "the
// lines went through the translator" from "the request was well-formed".
func TestSyncCommitFromAnOfflineNodeCarriesStock(t *testing.T) {
	srv := newServer(t)
	e := newItemWireEnv(t, srv)

	// One offline CREATE of a purchase that receives goods: postings AND the
	// item grid, exactly as a disconnected desktop would have recorded it. The
	// provisional number is the node's own guess; the sequencer replaces it,
	// which is why the outcome's final_voucher_no is asserted separately from
	// the stock.
	clientVoucherID := uuid.NewString()
	rec, body := do(t, srv, http.MethodPost, e.base+"/sync/commit", map[string]any{
		"intents": []map[string]any{{
			"idempotency_key":   "inv-j-sync-1",
			"client_voucher_id": clientVoucherID,
			"op":                "CREATE",
			"class":             "Purchase",
			"provisional_no":    "OFFLINE/1",
			"node_id":           "desk-1",
			"hlc":               map[string]any{"wall_millis": 1700000000000, "counter": 0, "node_id": "desk-1"},
			"base_version":      0,
			"payload": map[string]any{
				"voucher_date": "2025-05-12", "party": "Wire Supplier",
				"party_ledger_id": e.creditorID, "created_by": "desk-1",
				"postings": []map[string]any{
					{"ledger_id": e.purchaseID, "dr_cr": "D", "amount": "700.0000"},
					{"ledger_id": e.creditorID, "dr_cr": "C", "amount": "700.0000"},
				},
				"inventory": []map[string]any{
					{"stock_item_id": e.itemID, "godown_id": e.godownID, "batch_id": e.batchID,
						"direction": "IN", "qty": "7.0000", "rate": "100.0000", "amount": "700.0000"},
				},
			},
		}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /sync/commit = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	outcomes := list(t, body, "outcomes")
	if len(outcomes) != 1 || outcomes[0]["status"] != "COMMITTED" {
		t.Fatalf("sync outcomes = %v, want one COMMITTED", outcomes)
	}
	if no, _ := outcomes[0]["final_voucher_no"].(string); no == "" {
		t.Fatalf("committed offline CREATE got no final_voucher_no: %v", outcomes[0])
	}

	// THE ASSERTION: the goods are in the warehouse, derived by replaying the
	// movement ledger. A sync path that decoded the intent but dropped the item
	// grid returns exactly the same 200 COMMITTED as this one.
	if q, v := closingPosition(t, srv, e.base, e.itemID); q != "7.0000" || v != "700.0000" {
		t.Fatalf("after an offline commit the shelf reads qty=%q value=%q, want 7.0000 / "+
			"700.0000 — the sequencer committed the accounting and threw the stock (or its "+
			"price) away. Every voucher door must go through buildVoucherInput, or a field only "+
			"the HTTP door knows about is a field an offline desk silently loses", q, v)
	}
}
