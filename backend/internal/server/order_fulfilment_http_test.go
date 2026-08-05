package server_test

// order_fulfilment_http_test.go — CAN A CLIENT ACTUALLY LINK A DELIVERY TO AN
// ORDER, AND SEE THE ORDER BOOK MOVE?
//
// ⛔⛔ THIS FILE EXISTS BECAUSE THE ANSWER WAS "NO" FOR T2 WHILE EVERY OTHER TEST
// SAID YES, AND NOBODY NOTICED FOR A WHOLE FEATURE.
//
// cost_allocation_http_test.go tells that story in full: T2's write path was
// built, reviewed and covered by unit tests AND live-Postgres integration tests,
// all of which drove ledger.PostVoucher DIRECTLY, while `voucherReq` had no
// cost_allocations field — so no HTTP body could carry one and the whole feature
// was reachable only from Go. Nothing failed. Both report routes answered 200 and
// were CORRECT: they accurately reported that nothing had been allocated, because
// nothing could be.
//
// It had already happened once before that, for `voucherReq.ItemLedgerID`. So this
// is the third occurrence of one shape, and the rule it produced is that a
// feature is not reachable until the field exists on EVERY wire between the
// operator and the table. This test is written at the same time as the wire field
// rather than after it, and it asserts through the OUTERMOST surface at BOTH ends:
// it POSTs a delivery note as JSON and reads the outstanding quantity back out of
// the ORDER BOOK, over HTTP. Nothing in between is stubbed.
//
// THE EXACT REQUEST IT PINS, stated because the definition of done asks for it by
// name:
//
//	POST /v1/companies/{id}/vouchers/delivery-note
//	  {"inventory":[{...,"qty":"5"}], "fulfilments":[{"line_no":1,"order_line_id":"...","qty":"5"}]}
//	GET  /v1/companies/{id}/sales-order-book?as_on=2026-07-31
//
// ===========================================================================
// ⛔ EVERY STEP IS A REAL HTTP REQUEST, INCLUDING CREATING THE ORDER
// ===========================================================================
// An earlier revision of this file seeded the order by SQL, because the
// order-creation route did not exist yet, and said so in this comment rather than
// implying otherwise. The route exists now and the caveat is GONE rather than
// weakened: the order, its line, the delivery note and the read-back are all
// requests a browser could make. The only SQL left is the opening stock, and that
// is a fixture concern (a Delivery Note needs something on the shelf) rather than
// part of the feature under test.
//
// That is the standard to hold: a test whose fixture reaches around the API is
// evidence about the layers under it, not about whether anyone can use the thing.

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/server"
)

// orderHTTPFixture is a company with stock on the shelf and one 20-unit sales
// order for it.
type orderHTTPFixture struct {
	base     string
	ownerTok string

	itemID      string
	godownID    string
	orderLineID string
}

// seedOrderSQL plants the order and its line directly. See the file header: this
// is a STAND-IN for a route that does not exist, not a shortcut around one that
// does.
func newOrderHTTPFixture(t *testing.T, srv *server.Server) orderHTTPFixture {
	t.Helper()
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	f := orderHTTPFixture{base: "/v1/companies/" + companyID, ownerTok: ownerTok}

	// The item and its opening stock go in over HTTP where routes exist, so as much
	// of the fixture as possible is built the way a client would.
	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/stock-items", ownerTok,
		map[string]any{"name": "Table " + uuid.NewString()[:8], "created_by": "owner"})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create stock item = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if f.itemID, _ = body["stock_item_id"].(string); f.itemID == "" {
		f.itemID, _ = body["id"].(string)
	}
	if f.itemID == "" {
		t.Fatalf("the stock item came back with no id: %s", rec.Body.String())
	}

	cid, err := uuid.Parse(companyID)
	if err != nil {
		t.Fatalf("parse company id: %v", err)
	}
	pool, err := db.New(context.Background(), appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx := db.WithCompany(context.Background(), cid)

	itemID, err := uuid.Parse(f.itemID)
	if err != nil {
		t.Fatalf("parse stock item id: %v", err)
	}
	openingID := uuid.New()
	var partyLedger, godown uuid.UUID
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT id FROM godown WHERE company_id = $1 AND name = 'Main Location'`,
			cid).Scan(&godown); err != nil {
			return err
		}
		// Any ledger will do as the counterparty: what is under test is the
		// fulfilment link, not the party.
		if err := tx.QueryRow(ctx,
			`SELECT id FROM ledger WHERE company_id = $1 ORDER BY name LIMIT 1`,
			cid).Scan(&partyLedger); err != nil {
			return err
		}
		// Opening stock, so the delivery does not trip stock_movement_not_negative
		// before it ever reaches the fulfilment path — that would be a refusal for
		// the wrong reason, which reads as coverage and is not. THE ONLY SQL LEFT IN
		// THIS FIXTURE, and it is a fixture concern rather than the feature.
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_opening (id, company_id, stock_item_id, godown_id, qty, rate)
			 VALUES ($1, $2, $3, $4, 1000, 100)`,
			openingID, cid, itemID, godown)
		return err
	}); err != nil {
		t.Fatalf("seed the opening stock: %v", err)
	}
	f.godownID = godown.String()

	// ⛔ THE ORDER ITSELF, OVER HTTP. This is the step that used to be SQL.
	orec, obody := doRaw(t, srv, http.MethodPost, f.base+"/sales-orders", ownerTok,
		map[string]any{
			"order_no":        "SO-" + uuid.NewString()[:8],
			"order_date":      "2026-06-01",
			"party_ledger_id": partyLedger.String(),
			"created_by":      "owner",
			"lines": []any{map[string]any{
				"stock_item_id": f.itemID,
				"qty":           "20",
				"rate":          "500",
				"due_date":      "2026-06-30",
			}},
		})
	if orec.Code != http.StatusCreated && orec.Code != http.StatusOK {
		t.Fatalf("POST a sales order = %d, want 201 (%s)", orec.Code, orec.Body.String())
	}
	orderID, _ := obody["order_id"].(string)
	if orderID == "" {
		t.Fatalf("the sales order came back with no order_id: %s", orec.Body.String())
	}

	// And read the LINE id back out, because a fulfilment names the line and only
	// the server knows its id. A client does exactly this.
	grec, gbody := doRaw(t, srv, http.MethodGet, f.base+"/sales-orders/"+orderID, ownerTok, nil)
	if grec.Code != http.StatusOK {
		t.Fatalf("GET the sales order back = %d, want 200 (%s)", grec.Code, grec.Body.String())
	}
	lines, _ := gbody["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("the order read back with %d line(s), want 1: %s", len(lines), grec.Body.String())
	}
	lm, _ := lines[0].(map[string]any)
	if f.orderLineID, _ = lm["order_line_id"].(string); f.orderLineID == "" {
		t.Fatalf("the order line came back with no order_line_id: %s", grec.Body.String())
	}
	// A fresh order must read back fully outstanding from its OWN route as well as
	// from the order book — two independent projections of one derived figure.
	if got, _ := lm["outstanding_qty"].(string); got != "20.0000" {
		t.Fatalf("a fresh 20-unit order line reads outstanding_qty=%q from the order route, want 20.0000", got)
	}
	return f
}

// salesOrderBook reads the outstanding position for our order line over HTTP and
// returns (outstanding, fulfilled, totalValue).
func salesOrderBook(t *testing.T, srv *server.Server, f orderHTTPFixture) (string, string, string) {
	t.Helper()
	rec, body := doRaw(t, srv, http.MethodGet,
		f.base+"/sales-order-book?as_on=2026-07-31", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET sales-order-book = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	total, _ := body["total_outstanding_value"].(string)
	rows, _ := body["rows"].([]any)
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if m == nil {
			continue
		}
		if id, _ := m["order_line_id"].(string); id != f.orderLineID {
			continue
		}
		out, _ := m["outstanding_qty"].(string)
		got, _ := m["fulfilled_qty"].(string)
		return out, got, total
	}
	// A line with nothing outstanding is EXCLUDED by design, so "absent" is a real
	// answer and the caller distinguishes it.
	return "", "", total
}

// deliveryBody is the exact JSON a client sends. Written out as a literal rather
// than built from a helper so the WIRE SHAPE is visible in the test: if
// `fulfilments` is renamed or dropped, this is the line that has to change and the
// assertion below is what notices.
func deliveryBody(f orderHTTPFixture, qty string, fulfilments []any) map[string]any {
	return map[string]any{
		"voucher_date": "2026-06-10",
		"narration":    "Part delivery against the order",
		"created_by":   "owner",
		"inventory": []any{map[string]any{
			"stock_item_id": f.itemID,
			"godown_id":     f.godownID,
			"direction":     "OUT",
			"qty":           qty,
			"rate":          "500.0000",
			"amount":        "0.0000",
		}},
		"fulfilments": fulfilments,
	}
}

// TestAClientCanFulfilAnOrderOverHTTPAndSeeTheOrderBookMove is the reachability
// pin. The assertion spans wire -> ledger -> hashed payload -> order_fulfilment ->
// order book -> wire.
func TestAClientCanFulfilAnOrderOverHTTPAndSeeTheOrderBookMove(t *testing.T) {
	srv := newServer(t)
	f := newOrderHTTPFixture(t, srv)

	// ---- CONTROL 1: the order is fully outstanding before anything is delivered.
	//
	// Without this leg, every figure below is consistent with the report being
	// broken in a way that returns the expected number, or with the fixture having
	// delivered something during setup.
	out, done, total := salesOrderBook(t, srv, f)
	if out != "20.0000" || done != "0.0000" {
		t.Fatalf("CONTROL: a fresh 20-unit order reads outstanding=%q fulfilled=%q, want 20/0 "+
			"(total value %q)", out, done, total)
	}
	if total != "10000.0000" {
		t.Errorf("CONTROL: total outstanding value = %q, want 10000.0000 (20 at 500)", total)
	}

	// ---- CONTROL 2: a delivery of the same goods carrying NO fulfilments moves
	// stock and must leave the order UNTOUCHED.
	//
	// ⛔ THIS IS THE LEG THAT MAKES THE PROBE MEAN SOMETHING. An order book that
	// inferred fulfilment from stock movements — matching on item and party, say —
	// would pass the probe below and be wrong in the one way that matters: it would
	// discharge commitments nobody keyed. An order is a committed quantity and a
	// movement is a movement, and only an explicit link joins them.
	rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/delivery-note", f.ownerTok,
		deliveryBody(f, "7", nil))
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: an unlinked delivery note = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if out, done, _ = salesOrderBook(t, srv, f); out != "20.0000" || done != "0.0000" {
		t.Fatalf("CONTROL: after delivering 7 units with NO fulfilment link, the order reads "+
			"outstanding=%q fulfilled=%q, want 20/0. Something is inferring fulfilment from stock "+
			"movement, which would discharge commitments nobody keyed", out, done)
	}

	// ---- THE PROBE: a delivery note that names the order line on the wire.
	rec, probeBody := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/delivery-note", f.ownerTok,
		deliveryBody(f, "5", []any{map[string]any{
			"line_no":       1, // ⚠️ 1-BASED, not an array index. See fulfilmentReq.
			"order_line_id": f.orderLineID,
			"qty":           "5",
		}}))
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST a delivery note carrying `fulfilments` = %d, want 201. If this is a 400 the "+
			"wire field is missing or misnamed; if the body was accepted but the report below is "+
			"unchanged, it was silently ignored (%s)", rec.Code, rec.Body.String())
	}

	out, done, total = salesOrderBook(t, srv, f)
	if done != "5.0000" {
		t.Errorf("after delivering 5 of 20 THROUGH THE API, the order book reports fulfilled=%q, "+
			"want 5.0000. A zero here is the T2 defect exactly: the request was accepted, every "+
			"layer worked, and the feature is reachable only from Go", done)
	}
	if out != "15.0000" {
		t.Errorf("outstanding = %q, want 15.0000", out)
	}
	if total != "7500.0000" {
		t.Errorf("total outstanding value = %q, want 7500.0000 (15 at 500)", total)
	}

	// ---- ⛔⛔ THE ALTER, THROUGH THE REAL REPORT. This is the leg that covers the
	// voucher_head.current_version_id join in reporting.OrderBookFor, and no other
	// test in the project does.
	//
	// Fulfilment rows are VERSIONED: correcting this delivery from 5 to 8 leaves the
	// superseded version's row in order_fulfilment forever. A report that summed
	// without the join would read 13 fulfilled and 7 outstanding — both plausible,
	// both wrong, and reconcilable against nothing. internal/ledger's DB test
	// asserts the same property but through a query of its OWN, so it cannot see a
	// defect in this package's SQL. This leg can.
	voucherID, _ := probeBody["voucher_id"].(string)
	if voucherID == "" {
		t.Fatalf("the delivery note came back with no voucher_id, so the alter leg cannot run: %s",
			rec.Body.String())
	}
	arec, _ := doRaw(t, srv, http.MethodPut,
		f.base+"/vouchers/delivery-note/"+voucherID, f.ownerTok,
		deliveryBody(f, "8", []any{map[string]any{
			"line_no":       1,
			"order_line_id": f.orderLineID,
			"qty":           "8",
		}}))
	if arec.Code != http.StatusOK && arec.Code != http.StatusCreated {
		t.Fatalf("altering the delivery from 5 to 8 = %d, want 200 (%s)", arec.Code, arec.Body.String())
	}
	out, done, total = salesOrderBook(t, srv, f)
	if done != "8.0000" {
		t.Errorf("after altering the delivery 5 -> 8, the order book reports fulfilled=%q, want "+
			"8.0000. If this says 13.0000, reporting.OrderBookFor is missing the "+
			"voucher_head.current_version_id join and EVERY corrected delivery in the product is "+
			"counted twice", done)
	}
	if out != "12.0000" {
		t.Errorf("after the alter, outstanding = %q, want 12.0000", out)
	}
	if total != "6000.0000" {
		t.Errorf("after the alter, total outstanding value = %q, want 6000.0000 (12 at 500)", total)
	}

	// ---- AND THE REFUSAL IS REACHABLE TOO. Over-fulfilment must be refused over
	// HTTP, not just from Go: 18 more against an order with 5 of 20 already gone.
	//
	// This leg matters because a wire field that reached the table but bypassed
	// validation would pass every assertion above.
	rec, _ = doRaw(t, srv, http.MethodPost, f.base+"/vouchers/delivery-note", f.ownerTok,
		deliveryBody(f, "18", []any{map[string]any{
			"line_no":       1,
			"order_line_id": f.orderLineID,
			"qty":           "18",
		}}))
	if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
		t.Errorf("delivering 18 more against an order with 8 of 20 already fulfilled was ACCEPTED "+
			"over HTTP (%d); the order book would report a negative outstanding balance", rec.Code)
	}
	// And the refusal changed nothing.
	if out, done, _ = salesOrderBook(t, srv, f); out != "12.0000" || done != "8.0000" {
		t.Errorf("after the refused delivery the order reads outstanding=%q fulfilled=%q, want "+
			"12/8 — the transaction did not roll back cleanly", out, done)
	}
}

// TestTheVoucherGETCarriesItsOrderLinksSoAnAlterCannotSilentlyReopenTheOrder is the
// OTHER half of reachability, and it is the half that was missing.
//
// ⛔⛔ THE DEFECT IT PINS. `order_fulfilment` keys on (voucher_version_id, line_no),
// so an ALTER RE-STATES the links: whatever the alter body carries becomes the
// current version's links, and whatever it omits is gone. Meanwhile the voucher GET
// did not return them at all — ledger.VoucherView had no Fulfilments field and
// voucherFullJSON emitted no `fulfilments` key.
//
// Put those two facts together and the ordinary round trip DESTROYS DATA. An
// operator opens a delivered delivery note, fixes a typo in the narration, saves —
// and the order silently re-opens, because the client rebuilt the body from a GET
// that never mentioned the links. Nothing refuses it: zero fulfilments is a legal
// state, so no trigger fires and no total stops adding up. The only trace is an
// order book that starts reporting a commitment the company already met, which reads
// as a warehouse problem rather than a software one.
//
// This is the FOURTH occurrence of one shape (postings, InventoryLines,
// CostAllocations, and now this), which is why the fix is a projection in the service
// layer AND a key in the HTTP body AND this test, written at the same time.
//
// ⚠️ THE CONTROL IS THE SECOND HALF OF THE TEST, and without it the first half is
// consistent with an alter that does not re-state at all — in which case the order
// would survive for the wrong reason and this test would be inert. So it alters a
// second time with the links DELIBERATELY OMITTED and asserts the order DOES re-open.
// That is the behaviour the first half depends on being dangerous.
func TestTheVoucherGETCarriesItsOrderLinksSoAnAlterCannotSilentlyReopenTheOrder(t *testing.T) {
	srv := newServer(t)
	f := newOrderHTTPFixture(t, srv)

	// Deliver 8 of the 20, linked.
	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/delivery-note", f.ownerTok,
		deliveryBody(f, "8", []any{map[string]any{
			"line_no":       1,
			"order_line_id": f.orderLineID,
			"qty":           "8",
		}}))
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST a linked delivery note = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)
	if voucherID == "" {
		t.Fatalf("the delivery note came back with no voucher_id: %s", rec.Body.String())
	}

	// ---- CONTROL: the link landed and the order book moved.
	if out, done, _ := salesOrderBook(t, srv, f); out != "12.0000" || done != "8.0000" {
		t.Fatalf("CONTROL: after a linked delivery of 8/20 the order reads outstanding=%q "+
			"fulfilled=%q, want 12/8", out, done)
	}

	// ---- ⛔ THE READ-BACK. This is the assertion that did not exist.
	grec, gbody := doRaw(t, srv, http.MethodGet, f.base+"/vouchers/"+voucherID, f.ownerTok, nil)
	if grec.Code != http.StatusOK {
		t.Fatalf("GET the delivery note back = %d, want 200 (%s)", grec.Code, grec.Body.String())
	}
	raw, present := gbody["fulfilments"]
	if !present {
		t.Fatalf("GET /vouchers/{id} returned NO `fulfilments` key. A client rebuilding an alter "+
			"body from this response cannot restate the order links, so the next save of an "+
			"unrelated field silently re-opens a delivered order. Body: %s", grec.Body.String())
	}
	links, _ := raw.([]any)
	if len(links) != 1 {
		t.Fatalf("the delivery note read back with %d order link(s), want 1: %s",
			len(links), grec.Body.String())
	}
	lm, _ := links[0].(map[string]any)
	// ⚠️ 1-BASED, and it indexes `inventory` at line_no-1. It is the STORED
	// order_fulfilment.line_no, not a re-derivation — and it is 1-based where
	// `cost_allocations[].posting_seq` on this same body is 0-based.
	if got, _ := lm["line_no"].(float64); got != 1 {
		t.Errorf("the link's line_no = %v, want 1 (1-based, indexing `inventory` at line_no-1)", lm["line_no"])
	}
	if got, _ := lm["order_line_id"].(string); got != f.orderLineID {
		t.Errorf("the link's order_line_id = %q, want %q", got, f.orderLineID)
	}
	// How much of the ORDER was consumed — not how much stock moved. Here they are
	// both 8, which is the ordinary case; the fields are distinct regardless.
	if got, _ := lm["qty"].(string); got != "8.0000" {
		t.Errorf("the link's qty = %q, want 8.0000", got)
	}
	// The DISPLAY fields, so a form can re-render the link without re-reading the
	// order. Without these a rebuilt form shows an operator a bare uuid.
	if got, _ := lm["order_no"].(string); got == "" {
		t.Errorf("the link came back with an empty order_no; a form rebuilt from this body can "+
			"only show the operator a uuid. Body: %s", grec.Body.String())
	}
	if got, _ := lm["stock_item"].(string); got == "" {
		t.Errorf("the link came back with an empty stock_item, so a mis-linked row cannot be seen")
	}

	// ---- THE ROUND TRIP: alter using ONLY what the GET gave us, changing one
	// unrelated field. This is the exact sequence "open it, fix the narration, save".
	restated := deliveryBody(f, "8", []any{map[string]any{
		"line_no":       int(lm["line_no"].(float64)),
		"order_line_id": lm["order_line_id"].(string),
		"qty":           lm["qty"].(string),
	}})
	restated["narration"] = "Part delivery against the order (typo fixed)"
	arec, _ := doRaw(t, srv, http.MethodPut,
		f.base+"/vouchers/delivery-note/"+voucherID, f.ownerTok, restated)
	if arec.Code != http.StatusOK && arec.Code != http.StatusCreated {
		t.Fatalf("altering the narration = %d, want 200 (%s)", arec.Code, arec.Body.String())
	}
	if out, done, _ := salesOrderBook(t, srv, f); out != "12.0000" || done != "8.0000" {
		t.Errorf("after altering ONLY the narration, the order reads outstanding=%q fulfilled=%q, "+
			"want 12/8. The links did not survive a round trip through the GET, so every "+
			"unrelated edit of a delivery note silently re-opens the order it delivered", out, done)
	}

	// ---- ⛔ THE CONTROL THAT MAKES THE LEG ABOVE MEAN SOMETHING.
	//
	// An alter that omitted the links MUST re-open the order. If it did not — if the
	// links were keyed on voucher_id and survived regardless — the assertion above
	// would pass no matter what the GET returned, and this whole test would be inert
	// while reading as a guard. §9.2's rule: a negative assertion needs a positive
	// one beside it.
	orec, _ := doRaw(t, srv, http.MethodPut,
		f.base+"/vouchers/delivery-note/"+voucherID, f.ownerTok,
		deliveryBody(f, "8", nil))
	if orec.Code != http.StatusOK && orec.Code != http.StatusCreated {
		t.Fatalf("CONTROL: altering with no links = %d, want 200 (%s)", orec.Code, orec.Body.String())
	}
	if out, done, _ := salesOrderBook(t, srv, f); out != "20.0000" || done != "0.0000" {
		t.Fatalf("CONTROL: an alter that OMITS the links left the order at outstanding=%q "+
			"fulfilled=%q, want 20/0. An alter is supposed to RE-STATE, so omitting the links must "+
			"clear them — if it does not, the round-trip assertion above proves nothing", out, done)
	}
}
