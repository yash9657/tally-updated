package server_test

// auth_stock_vouchers_test.go — the WIRING claims of slice INV-M, asserted
// through the real router: that the four non-accounting stock voucher classes
// and the price-list masters are REACHABLE, that each one maps to its OWN PDP
// resource token, and that the seeded 'Data Entry' level gets exactly the
// authority migration 0031 decided it should get.
//
// WHY THIS IS A BLACK-BOX TEST OF THE LEVEL, NOT A UNIT TEST OF THE TABLE.
// routeAction is unexported and could be asserted directly, but a unit test of
// a mapping table proves only that the table says what it says. What has bitten
// this codebase repeatedly is the mapping being RIGHT in the table and WRONG in
// effect: routeAction DEFAULT-DENIES any unmapped {companyID} path, so a route
// mounted without its mapping comes back 403 — for an OWNER — and reads exactly
// like an authorization bug rather than the wiring gap it is. So the oracle
// here is a real security level with real rules: if POST /vouchers/receipt-note
// is reachable by a level whose grant names 'receipt-note' and refused for one
// that does not, then the route emits (create, receipt-note) and there is no
// table to misread.
//
// THE 403s MATTER AS MUCH AS THE 201s. A grant test that only checks the
// happy path passes just as well when EVERY class was granted to everybody;
// a test that only checks the refusals passes when the feature is entirely
// unreachable. Migration 0031 draws a line (an operator moves goods; an operator
// does not restate the count), and only both halves together pin it.

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/server"
)

// stockVoucherFixture is a company owned by a FRESH subject (so the Owner token
// is ours to hand around and to invite members with), carrying the masters a
// stock voucher needs and some stock on the shelf for an outward to consume.
type stockVoucherFixture struct {
	base     string
	ownerTok string
	itemID   string
	// mainGodown is the seeded 'Main Location'; sideGodown is a second location,
	// which is what makes a Stock Journal a TRANSFER rather than a write-off.
	mainGodown string
	sideGodown string
}

func newStockVoucherFixture(t *testing.T, srv *server.Server) stockVoucherFixture {
	t.Helper()
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	f := stockVoucherFixture{base: "/v1/companies/" + companyID, ownerTok: ownerTok}

	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/stock-items", ownerTok,
		map[string]any{"name": "Widget", "unit_symbol": "Nos", "created_by": "owner"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("owner creates stock item = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	f.itemID, _ = body["stock_item_id"].(string)

	rec, body = doRaw(t, srv, http.MethodGet, f.base+"/godowns", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /godowns = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	for _, g := range list(t, body, "godowns") {
		if g["name"] == "Main Location" {
			f.mainGodown, _ = g["id"].(string)
		}
	}
	if f.mainGodown == "" {
		t.Fatalf("GET /godowns did not list the seeded 'Main Location': %v", body)
	}

	rec, body = doRaw(t, srv, http.MethodPost, f.base+"/godowns", ownerTok,
		map[string]any{"name": "Bonded Store", "created_by": "owner"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("owner creates second godown = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	f.sideGodown, _ = body["godown_id"].(string)

	// Put stock on the shelf with a RECEIPT NOTE, which is also the first proof
	// that the class posts at all. 0025's negative-stock trigger is DEFERRED and
	// fires per (item, godown, batch), so without this every outward below would
	// be refused for a reason that has nothing to do with authorization.
	f.postAsOwner(t, srv, "receipt-note", "RN-SEED", map[string]any{
		"voucher_date": "2026-04-02", "created_by": "owner",
		"inventory": []map[string]any{
			{"stock_item_id": f.itemID, "godown_id": f.mainGodown,
				"direction": "IN", "qty": "100", "rate": "10", "amount": "1000"},
		},
	})
	return f
}

// postAsOwner posts one stock voucher as the fixture's Owner and fails loudly if
// it is refused — an Owner has default_access 'full', so anything but a 201 here
// is a wiring or a ledger problem and never an ACL one.
func (f stockVoucherFixture) postAsOwner(t *testing.T, srv *server.Server, class, no string, body map[string]any) string {
	t.Helper()
	body["voucher_no"] = no
	rec, decoded := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/"+class, f.ownerTok, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Owner POST /vouchers/%s = %d, want 201 (%s)", class, rec.Code, rec.Body.String())
	}
	id, _ := decoded["voucher_id"].(string)
	return id
}

// stockVoucherBody returns a MINIMAL, VALID body for each class, with a distinct
// voucher number so the four can be posted into one company.
//
// Physical Stock sends `physical_count`, NOT `inventory` — the count is an
// absolute figure and the server differences it against the books inside the
// post transaction. Sending `inventory` on that class is refused, which is why
// the wire needs both fields and why voucherReq carries both.
func stockVoucherBody(f stockVoucherFixture, class, no string) map[string]any {
	switch class {
	case "delivery-note":
		return map[string]any{
			"voucher_no": no, "voucher_date": "2026-04-05", "created_by": "t",
			"inventory": []map[string]any{
				{"stock_item_id": f.itemID, "godown_id": f.mainGodown,
					"direction": "OUT", "qty": "1", "rate": "10", "amount": "10"},
			},
		}
	case "receipt-note":
		return map[string]any{
			"voucher_no": no, "voucher_date": "2026-04-05", "created_by": "t",
			"inventory": []map[string]any{
				{"stock_item_id": f.itemID, "godown_id": f.mainGodown,
					"direction": "IN", "qty": "1", "rate": "10", "amount": "10"},
			},
		}
	case "stock-journal":
		// A TRANSFER: out of one location and into another on the same voucher.
		// This is the class that made 0025's trigger DEFERRABLE INITIALLY
		// DEFERRED — checked per statement, the OUT would raise before the IN
		// was written and a balanced transfer would be accepted or refused
		// depending on the order its lines happened to be inserted in.
		return map[string]any{
			"voucher_no": no, "voucher_date": "2026-04-05", "created_by": "t",
			"inventory": []map[string]any{
				{"stock_item_id": f.itemID, "godown_id": f.mainGodown,
					"direction": "OUT", "qty": "1", "rate": "10", "amount": "10"},
				{"stock_item_id": f.itemID, "godown_id": f.sideGodown,
					"direction": "IN", "qty": "1", "rate": "10", "amount": "10"},
			},
		}
	case "physical-stock":
		return map[string]any{
			"voucher_no": no, "voucher_date": "2026-04-05", "created_by": "t",
			"physical_count": []map[string]any{
				{"stock_item_id": f.itemID, "godown_id": f.mainGodown,
					"qty": "90", "rate": "10"},
			},
		}
	}
	panic("unknown stock voucher class " + class)
}

// stockVoucherClasses is the set this slice mounted, in the order the migration
// header discusses them.
var stockVoucherClasses = []string{"delivery-note", "receipt-note", "stock-journal", "physical-stock"}

// TestStockVoucherRouteAuthMatrix walks the three answers every route under
// {companyID} owes, for every class this slice mounted:
//
//	no token          -> 401 (authentication, not authorization)
//	another tenant    -> 403 (membership is the tenant gate)
//	this company's Owner -> 201
//
// The Owner row is the one that catches the wiring gap. routeAction
// default-denies an unmapped path BEFORE any handler runs, so a class mounted
// without its isVoucherClass entry answers 403 to an Owner who has
// default_access 'full' — an authorization answer to what is really a missing
// line in a mapping table.
//
// The 401/403 split is a contract in its own right: 401 is RESERVED for
// missing/invalid/expired tokens so a client can tell "log in again" apart from
// "you may not do this", and a route that answered 403 to an anonymous caller
// would send every logged-out operator to the wrong remedy.
func TestStockVoucherRouteAuthMatrix(t *testing.T) {
	srv := newServer(t)
	f := newStockVoucherFixture(t, srv)

	// Another tenant's Owner: full rights, in a company that is not this one.
	_, otherTok, _ := newAdminCompany(t, srv)

	for _, class := range stockVoucherClasses {
		path := f.base + "/vouchers/" + class
		body := stockVoucherBody(f, class, "MATRIX-"+class)

		if rec, _ := doRaw(t, srv, http.MethodPost, path, "", body); rec.Code != http.StatusUnauthorized {
			t.Errorf("POST %s with NO token = %d, want 401 — 401 is reserved for missing/invalid "+
				"tokens so a client can tell 'log in again' from 'you may not do this'", path, rec.Code)
		}
		if rec, b := doRaw(t, srv, http.MethodPost, path, otherTok, body); rec.Code != http.StatusForbidden {
			t.Errorf("POST %s as ANOTHER tenant's Owner = %d %v, want 403 — membership in the "+
				"targeted company is the tenant gate, and being an Owner somewhere else is not it",
				path, rec.Code, b)
		}
		rec, b := doRaw(t, srv, http.MethodPost, path, f.ownerTok, body)
		if rec.Code != http.StatusCreated {
			t.Errorf("POST %s as this company's Owner = %d %v, want 201 — an unmapped {companyID} "+
				"route default-denies before any handler runs, so a 403 here is a routeAction gap "+
				"(isVoucherClass) and not an authorization one", path, rec.Code, b)
			continue
		}

		// ALTER travels the same seam. It is a separate chi pattern
		// (/vouchers/{class}/{voucherID}) and a separate routeAction arm, so a
		// class whose POST is mapped and whose PUT is not would ship a voucher
		// nobody can correct.
		//
		// A Physical Stock ALTER restates by RESENDING `physical_count`, never
		// the movements GetVoucher projects: the delta is recomputed against a
		// book that excludes this voucher's superseded version, so a re-count
		// restates instead of compounding.
		voucherID, _ := b["voucher_id"].(string)
		alter := stockVoucherBody(f, class, "MATRIX-"+class)
		if rec, ab := doRaw(t, srv, http.MethodPut, path+"/"+voucherID, f.ownerTok, alter); rec.Code != http.StatusOK {
			t.Errorf("PUT %s/%s as Owner = %d %v, want 200 — the alter route is a separate "+
				"routeAction arm from the post route", path, voucherID, rec.Code, ab)
		}
	}
}

// TestPhysicalCountRidesTheVoucherWire proves the `physical_count` block this
// slice added to voucherReq actually reaches the ledger with its VALUES intact.
//
// WHY A 201 IS NOT ENOUGH TO KNOW THAT. validatePhysicalCount refuses a Physical
// Stock voucher with no counted lines, so DROPPING the block entirely is caught
// by any post test. What it cannot catch is the wire carrying the block with a
// wrong quantity — a count of 0 is LEGAL ("the shelf is empty" is the entry an
// auditor most wants recorded), so a buildPhysicalCountLine that lost the
// quantity would post cleanly, return 201, and write the entire position off
// the books. Only the resulting POSITION distinguishes the two.
//
// The oracle is chosen so the right and wrong answers differ: the books stand at
// 100, the count says 90, and the closing position afterwards must be exactly
// the count (the server posts the DELTA, never the absolute).
func TestPhysicalCountRidesTheVoucherWire(t *testing.T) {
	srv := newServer(t)
	f := newStockVoucherFixture(t, srv)

	f.postAsOwner(t, srv, "physical-stock", "PS-WIRE",
		stockVoucherBody(f, "physical-stock", "PS-WIRE"))

	rec, body := doRaw(t, srv, http.MethodGet, f.base+"/stock-summary?as_on=2026-04-30", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-summary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	rows := list(t, body, "rows")
	if len(rows) != 1 {
		t.Fatalf("stock summary has %d rows, want 1: %v", len(rows), rows)
	}
	if rows[0]["closing_qty"] != "90.0000" {
		t.Errorf("after counting 90 against a book of 100, closing = %v, want 90.0000 — the "+
			"closing position immediately after a stock-take IS the count, by construction; a "+
			"quantity lost on the wire posts a legal-looking count of zero and writes the "+
			"position off", rows[0]["closing_qty"])
	}
}

// TestDataEntryCanKeyTheStockVouchersItIsMeantTo pins migration 0031's judgement
// through the live PDP: the seeded operator level may record goods MOVING and
// may not restate what is on the shelf.
//
// WHY THE PHYSICAL STOCK REFUSAL IS THE POINT OF THIS TEST. A Physical Stock
// voucher is the only document in the system that makes the books agree with a
// number the operator types, with no counter-party and no counter-location —
// the closing position after it IS the count, by construction. Whoever keyed the
// deliveries could then make the shelf agree with the books, and the shrinkage
// would never appear in any report, because there is no report that shows you a
// discrepancy somebody already reconciled away. If this test starts passing
// with a 201, that control is gone and nothing else in the suite notices.
func TestDataEntryCanKeyTheStockVouchersItIsMeantTo(t *testing.T) {
	srv := newServer(t)
	f := newStockVoucherFixture(t, srv)
	de := inventoryMemberToken(t, srv, f.base, f.ownerTok, "Data Entry")

	// GRANTED: the three movement documents. A warehouse hand keys these all
	// day; each one is checkable against a party or a second location.
	for _, class := range []string{"delivery-note", "receipt-note", "stock-journal"} {
		path := f.base + "/vouchers/" + class
		body := stockVoucherBody(f, class, "DE-"+class)
		rec, b := doRaw(t, srv, http.MethodPost, path, de, body)
		if rec.Code != http.StatusCreated {
			t.Errorf("as Data Entry, POST %s = %d %v, want 201 — migration 0031 grants this class, "+
				"and an operator who cannot record goods leaving the warehouse either invents an "+
				"invoice or keys nothing at all", path, rec.Code, b)
			continue
		}
		voucherID, _ := b["voucher_id"].(string)
		alter := stockVoucherBody(f, class, "DE-"+class)
		if rec, ab := doRaw(t, srv, http.MethodPut, path+"/"+voucherID, de, alter); rec.Code != http.StatusOK {
			t.Errorf("as Data Entry, PUT %s/%s = %d %v, want 200 — 0031 grants alter alongside "+
				"create, because a note keyed with the wrong quantity has to be correctable by the "+
				"person who keyed it", path, voucherID, rec.Code, ab)
		}
	}

	// WITHHELD: the stock-take. Migration 0031 seeds an EXPLICIT
	// `disallow create physical-stock` rather than relying on the level's
	// default_access 'none', so the refusal survives an administrator who later
	// broadens the level with a wildcard allow.
	ps := f.base + "/vouchers/physical-stock"
	if rec, b := doRaw(t, srv, http.MethodPost, ps, de, stockVoucherBody(f, "physical-stock", "DE-PS")); rec.Code != http.StatusForbidden {
		t.Errorf("as Data Entry, POST %s = %d %v, want 403 — a stock-take is the one document that "+
			"makes the books agree with a figure the operator types, so the person who keyed the "+
			"deliveries must not also be able to reconcile away their own shortages",
			ps, rec.Code, b)
	}
	// The alter half, separately: a class refused on create and permitted on
	// alter would let an operator restate an Owner's stock-take, which is the
	// same authority by another route.
	ownerPS := f.postAsOwner(t, srv, "physical-stock", "OWNER-PS",
		stockVoucherBody(f, "physical-stock", "OWNER-PS"))
	if rec, b := doRaw(t, srv, http.MethodPut, ps+"/"+ownerPS, de,
		stockVoucherBody(f, "physical-stock", "OWNER-PS")); rec.Code != http.StatusForbidden {
		t.Errorf("as Data Entry, PUT %s/%s = %d %v, want 403 — restating somebody else's count is "+
			"the same authority as keying one", ps, ownerPS, rec.Code, b)
	}
}

// TestPriceListRoutesAreOwnTokenAndDataEntryMayReadNotDefine pins the OTHER
// half of this slice's ACL decision.
//
// Migrations 0026 and 0028 took five reports away from the operator level
// because each prints the company's COST book. That reasoning deliberately does
// NOT transfer to a price list: a rate card is a SELL price, it is printed in
// full on every invoice the operator raises, and an operator who cannot read it
// simply gets emailed the whole card instead — which discloses strictly more and
// leaves no trail. So the rate is readable and NOT definable, and the two are
// separable only because 'price-list' is its own resource token rather than the
// shared 'master' one (there is no `master:NAME` narrowing form).
func TestPriceListRoutesAreOwnTokenAndDataEntryMayReadNotDefine(t *testing.T) {
	srv := newServer(t)
	f := newStockVoucherFixture(t, srv)
	de := inventoryMemberToken(t, srv, f.base, f.ownerTok, "Data Entry")

	// The Owner defines the rate card: two slabs, so the quote below can only be
	// right if the resolver was actually consulted.
	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/price-lists", f.ownerTok, map[string]any{
		"name": "Retail 2026", "effective_from": "2026-04-01", "created_by": "owner",
		"entries": []map[string]any{
			{"stock_item_name": "Widget", "qty_from": "0", "qty_to": "10", "rate": "100"},
			{"stock_item_name": "Widget", "qty_from": "10", "qty_to_unbounded": true,
				"rate": "100", "discount_pct": "5"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("Owner POST /price-lists = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	listID, _ := body["price_list_id"].(string)

	// An operator may READ the card back and may QUOTE from it.
	if rec, b := doRaw(t, srv, http.MethodGet, f.base+"/price-lists/"+listID, de, nil); rec.Code != http.StatusOK {
		t.Errorf("as Data Entry, GET /price-lists/{id} = %d %v, want 200", rec.Code, b)
	}
	// THE SLAB IS THE ORACLE. A quantity of 25 falls in the SECOND slab, so the
	// right answer is 100 less 5% = 95.0000. A handler wired to "the first entry
	// for this item" would return 100.0000 — confident, plausible, and it
	// overcharges the customer who had earned the volume discount.
	quote := f.base + "/price-lists/quote?stock_item_id=" + f.itemID + "&on=2026-04-05&qty=25"
	rec, b := doRaw(t, srv, http.MethodGet, quote, de, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("as Data Entry, GET /price-lists/quote = %d %v, want 200 — the rate an operator "+
			"must charge is not the cost the company paid", rec.Code, b)
	}
	if b["net_rate"] != "95.0000" || b["rate"] != "100.0000" || b["discount_pct"] != "5.0000" {
		t.Errorf("quote at qty 25 = rate %v less %v = %v, want 100.0000 less 5.0000 = 95.0000 — "+
			"the gross and the discount are carried SEPARATELY because the invoice has to print "+
			"'1,000.00 less 5%%', and the net is rounded once so the quote and the posting agree",
			b["rate"], b["discount_pct"], b["net_rate"])
	}
	// The quote names WHICH list answered. That is not decoration: it is the
	// only thing that makes "why was this customer charged that" answerable
	// later without re-deriving the price lists as they stood on the day.
	if b["price_list_id"] != listID || b["party_scoped"] != false {
		t.Errorf("quote answered from price list %v (party_scoped %v), want %s scoped=false",
			b["price_list_id"], b["party_scoped"], listID)
	}

	// An operator may NOT define one. Rates are set by whoever owns the margin.
	if rec, b := doRaw(t, srv, http.MethodPost, f.base+"/price-lists", de, map[string]any{
		"name": "Mate's Rates " + uuid.NewString()[:6], "effective_from": "2026-04-01",
		"entries": []map[string]any{{"stock_item_name": "Widget", "qty_from": "0",
			"qty_to_unbounded": true, "rate": "1"}},
	}); rec.Code != http.StatusForbidden {
		t.Errorf("as Data Entry, POST /price-lists = %d %v, want 403 — a read grant must not carry "+
			"a write, and an operator who can mint a rate card can price the company's goods at 1",
			rec.Code, b)
	}

	// A miss is NOT a zero rate. Zero is a legal price (free samples are real),
	// so a caller that could not tell "no rate applies" from "the rate is zero"
	// would invoice the goods for nothing and still balance.
	miss := f.base + "/price-lists/quote?stock_item_id=" + f.itemID + "&on=2026-03-01&qty=1"
	rec, b = doRaw(t, srv, http.MethodGet, miss, de, nil)
	if rec.Code != http.StatusUnprocessableEntity || b["code"] != "NO_RATE" {
		t.Errorf("quote OUTSIDE every effective window = %d %v, want 422 with code NO_RATE — a "+
			"date the card does not cover must never resolve to a rate of zero", rec.Code, b)
	}

	// qty is REQUIRED, and this is the assertion that keeps it required. Slabs
	// price BY quantity and the first slab starts at zero, so an omitted
	// quantity silently defaulting to 0 would answer with the SMALLEST slab —
	// normally the dearest rate — for an order that had earned a better one.
	noQty := f.base + "/price-lists/quote?stock_item_id=" + f.itemID + "&on=2026-04-05"
	if rec, b := doRaw(t, srv, http.MethodGet, noQty, de, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("quote with NO qty = %d %v, want 400 — an assumed quantity quotes the dearest "+
			"slab and nothing downstream could detect it", rec.Code, b)
	}
}
