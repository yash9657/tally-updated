package server_test

// ewaybill_http_test.go — CAN A CLIENT ACTUALLY RAISE AN e-WAY BILL, AND READ
// THE DISCLOSURES BACK?
//
// ⛔⛔ THIS FILE IS WRITTEN AT THE SAME TIME AS THE WIRE FIELDS, NOT AFTER THEM,
// because T5's whole problem was the one HANDOFF §4.1 has now recorded four
// times: the rules-and-payload core was BUILT, TESTED AND GOOD while no screen
// and no route existed, so nothing could reach it and nothing failed.
//
// It follows order_fulfilment_http_test.go, which is the pattern §4.1 nominates:
// every step is a real HTTP request, the assertion reads the figures back out
// through the OUTERMOST surface, and NO SQL IN THE FIXTURE REACHES AROUND THE
// API. Nothing in between is stubbed.
//
// THE EXACT REQUESTS IT PINS, stated because the definition of done (§12 item 4)
// asks for them by name:
//
//	POST /v1/companies/{id}/vouchers/sales      (an item invoice over the threshold)
//	POST /v1/companies/{id}/e-way-bills         {"voucher_id":"…","distance_km":120,…}
//	GET  /v1/companies/{id}/e-way-bills/{voucherID}
//
// ===========================================================================
// WHAT §12 ITEM 4a (THE READ-BACK CHECK) COMES TO HERE, AND WHY IT IS DIFFERENT
// ===========================================================================
// Item 4a exists because four tables keyed on voucher_version_id were re-stated
// by an ALTER and silently emptied by a round trip. e_way_bill is NOT in that
// class: it keys on the voucher HEAD and the voucher path never writes it, so an
// ALTER cannot re-state it and cannot drop it.
//
// ⛔ BUT THE ALTER STILL MATTERS, AND THE NAIVE VERSION OF THIS FILE WOULD MISS
// IT. If the invoice changes after the bill is raised, the stored payload no
// longer describes the document it names — and nothing refuses that, because
// nothing connects them. So TestAnAlterMarksTheBillStale below is this feature's
// answer to item 4a: it proves the staleness flag is derived from the CURRENT
// version on every read, with the control that an unaltered bill is not stale.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/server"
)

// ewbFixture is a company that can actually move goods: a GSTIN and a pincode on
// both sides, an item with an HSN and a rate, and stock on the shelf.
type ewbFixture struct {
	base     string
	ownerTok string

	itemID   string
	godownID string
	partyID  string
	salesID  string
	cgstID   string
	sgstID   string
}

// newEWBFixture builds the whole thing over HTTP.
//
// ⛔ THE COMPANY AND THE PARTY BOTH CARRY A GSTIN, A STATE CODE AND A PINCODE,
// and that is not fixture decoration. ValidateEWayBill checks all three against
// the recorded NIC schema, so a fixture without them would fail for a SCHEMA
// reason before ever reaching the Rule 138 assessment under test — a refusal for
// the wrong reason, which reads as coverage and is not (HANDOFF §9.3).
func newEWBFixture(t *testing.T, srv *server.Server) ewbFixture {
	t.Helper()
	ownerSubject := "ewb-owner-" + uuid.NewString()[:8]
	ownerTok := mintToken(t, srv, ownerSubject, "webauthn")
	cid := uuid.New()
	companyID := cid.String()

	rec, _ := doRaw(t, srv, http.MethodPost, "/v1/companies", ownerTok, map[string]any{
		"id":            companyID,
		"name":          "EWB Co " + companyID[:8],
		"fy_start_date": "2026-04-01",
		"base_currency": "INR",
		"state_code":    "29",
		"gstin":         "29AACCD1234K1Z8",
		"address":       "14, 3rd Cross",
		"city":          "Bengaluru",
		"pincode":       "560095",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	testdb.Cleanup(t, cid)

	f := ewbFixture{base: "/v1/companies/" + companyID, ownerTok: ownerTok}

	mkLedger := func(name, group string, extra map[string]any) string {
		t.Helper()
		body := map[string]any{"name": name, "group_name": group, "created_by": ownerSubject}
		for k, v := range extra {
			body[k] = v
		}
		lrec, lb := doRaw(t, srv, http.MethodPost, f.base+"/ledgers", ownerTok, body)
		if lrec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d (%s)", name, lrec.Code, lrec.Body.String())
		}
		id, _ := lb["ledger_id"].(string)
		return id
	}
	// An INTRA-STATE buyer (29 -> 29): the movement is within Karnataka, so the
	// Part B relaxation is in play and the assessment has something to decide.
	f.partyID = mkLedger("Acme Buyer", "Sundry Debtors", map[string]any{
		"gstin":      "29AACCB4321K1Z2",
		"state_code": "29",
		"address":    "9, Hosur Road",
		"city":       "Bengaluru",
		"pincode":    "560100",
	})
	f.salesID = mkLedger("Sales A/c", "Sales Accounts", nil)
	f.cgstID = mkLedger("Output CGST", "Duties & Taxes", nil)
	f.sgstID = mkLedger("Output SGST", "Duties & Taxes", nil)

	// The item carries an HSN and a GST rate, because an item invoice takes BOTH
	// from the master and never from the request (ledger/item_invoice.go).
	//
	// ⛔ AND IT CARRIES A UNIT. It did not, and that made this fixture the case
	// that exhibited a blank NIC ItemList[].Unit — a MANDATORY field for goods —
	// on every e-invoice the suite ever built. stock_item.unit_id is nullable and
	// the e-invoice assembler now REFUSES a line whose master names no unit
	// (einvoice.ErrNoUnitOfMeasure) rather than minting a blank onto a document
	// that then freezes the voucher. Every other inventory fixture in this package
	// already sets unit_symbol; this one was the outlier.
	irec, ib := doRaw(t, srv, http.MethodPost, f.base+"/stock-items", ownerTok, map[string]any{
		"name":         "MS Angle " + uuid.NewString()[:8],
		"hsn":          "7216",
		"gst_rate_pct": "18",
		"unit_symbol":  "Nos",
		"created_by":   ownerSubject,
	})
	if irec.Code != http.StatusCreated && irec.Code != http.StatusOK {
		t.Fatalf("create stock item = %d, want 201 (%s)", irec.Code, irec.Body.String())
	}
	if f.itemID, _ = ib["stock_item_id"].(string); f.itemID == "" {
		f.itemID, _ = ib["id"].(string)
	}
	if f.itemID == "" {
		t.Fatalf("the stock item came back with no id: %s", irec.Body.String())
	}

	// The godown id is the ONE thing with no create route in play here, so it is
	// read (not written) out of the seeded default. A read is not a write that
	// reaches around the API.
	pool, err := db.New(context.Background(), appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)
	var godown uuid.UUID
	if err := pool.RunInTx(db.WithCompany(context.Background(), cid),
		func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT id FROM godown WHERE company_id = $1 AND name = 'Main Location'`,
				cid).Scan(&godown)
		}); err != nil {
		t.Fatalf("read the default godown: %v", err)
	}
	f.godownID = godown.String()

	// Goods on the shelf, over HTTP: a sale out of an empty warehouse is refused
	// by stock_movement_not_negative, which would be a refusal for the wrong
	// reason.
	rrec, _ := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/receipt-note", ownerTok,
		map[string]any{"voucher_date": "2026-04-01", "created_by": ownerSubject,
			"inventory": []any{map[string]any{"stock_item_id": f.itemID, "godown_id": f.godownID,
				"direction": "IN", "qty": "500.0000", "rate": "500.0000"}}})
	if rrec.Code != http.StatusCreated {
		t.Fatalf("opening receipt note = %d (%s)", rrec.Code, rrec.Body.String())
	}
	return f
}

// postInvoice raises an item invoice for qty units at 500 and returns its id.
//
// 200 units at 500 is 100,000 taxable plus 18% = 118,000, which is comfortably
// over the consignment threshold. ⛔ THE THRESHOLD IS NOT RESTATED HERE — the
// test asserts the ASSESSMENT's conclusion, not a number it copied out of
// rule138.go. A test that hard-codes 50000 is a second home for the figure.
func postInvoice(t *testing.T, srv *server.Server, f ewbFixture, qty string) string {
	t.Helper()
	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/sales", f.ownerTok,
		map[string]any{
			"voucher_date":    "2026-06-10",
			"created_by":      "owner",
			"party_ledger_id": f.partyID,
			"item_ledger_id":  f.salesID,
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"cgst_ledger_id":             f.cgstID,
				"sgst_ledger_id":             f.sgstID,
			},
			"inventory": []any{map[string]any{
				"stock_item_id": f.itemID, "godown_id": f.godownID,
				"direction": "OUT", "qty": qty, "rate": "500.0000",
			}},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post the sales item invoice = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	id, _ := body["voucher_id"].(string)
	if id == "" {
		t.Fatalf("the invoice came back with no voucher_id: %s", rec.Body.String())
	}
	return id
}

// raiseBody is the exact JSON a client sends. Written as a literal rather than
// built by a helper so the WIRE SHAPE is visible: if `distance_km` is renamed or
// dropped, this is the line that has to change and the assertions notice.
func raiseBody(voucherID string, overrides map[string]any) map[string]any {
	b := map[string]any{
		"voucher_id":       voucherID,
		"transporter_id":   "29AACCT9999K1Z1",
		"transporter_name": "Speedy Roadways",
		"vehicle_number":   "KA01AB1234",
		"transport_mode":   "ROAD",
		"distance_km":      120,
		"created_by":       "owner",
	}
	for k, v := range overrides {
		b[k] = v
	}
	return b
}

// TestAClientCanRaiseAnEWayBillOverHTTPAndReadItBack is the reachability pin.
// The assertion spans wire -> ledger -> consignment assembly -> Rule 138 ->
// e_way_bill -> wire.
func TestAClientCanRaiseAnEWayBillOverHTTPAndReadItBack(t *testing.T) {
	srv := newServer(t)
	f := newEWBFixture(t, srv)
	voucherID := postInvoice(t, srv, f, "200.0000")

	// ---- CONTROL: nothing has been raised yet, so the GET is a 404.
	//
	// Without this leg every assertion below is consistent with the GET returning
	// a row the fixture created, or with the route answering 200 unconditionally.
	crec, _ := doRaw(t, srv, http.MethodGet, f.base+"/e-way-bills/"+voucherID, f.ownerTok, nil)
	if crec.Code != http.StatusNotFound {
		t.Fatalf("CONTROL: GET before any bill is raised = %d, want 404 (%s)",
			crec.Code, crec.Body.String())
	}

	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/e-way-bills", f.ownerTok,
		raiseBody(voucherID, nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /e-way-bills = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	// ---- THE IDENTIFIER IS A LOCAL REFERENCE AND IS NOT SHAPED LIKE AN EBN.
	ref, _ := body["local_reference"].(string)
	if ref == "" {
		t.Fatalf("the bill came back with no local_reference: %s", rec.Body.String())
	}
	if !strings.HasPrefix(ref, "LOCAL-EWB-") {
		t.Errorf("local_reference = %q, want a LOCAL-EWB- prefix: a value that could pass "+
			"for a 12-digit NIC EBN on a printed sheet is the forgery this design avoids", ref)
	}
	// ⛔ AND THE RESPONSE MUST NOT CARRY AN EBN-SHAPED KEY AT ALL. A screen author
	// reaching for `body.ebn` must find nothing rather than find the local
	// reference under a name that licenses printing it as a government number.
	for _, forbidden := range []string{"ebn", "e_way_bill_no", "ewb_no", "number"} {
		if _, present := body[forbidden]; present {
			t.Errorf("the response carries a %q key. The identifier is a LOCAL reference, "+
				"never an EBN, and a key by that name is how it ends up on paper as one", forbidden)
		}
	}

	// ---- THE ASSESSMENT REACHED THE WIRE.
	if req, _ := body["assessed_required"].(bool); !req {
		t.Errorf("assessed_required = false for a 118,000 consignment, want true")
	}
	// 120 km within one State exceeds the short-hop relaxation, so Part B is
	// required — and the vehicle was supplied, so there is no blocker.
	if pb, _ := body["part_b_required"].(bool); !pb {
		t.Errorf("part_b_required = false for a 120 km movement, want true")
	}
	blockers, _ := body["blockers"].([]any)
	if len(blockers) != 0 {
		t.Errorf("a fully-stated consignment reports %d blocker(s), want none: %v",
			len(blockers), blockers)
	}
	// 120 km of ordinary cargo is one day, rounded up.
	if days, _ := body["validity_days"].(float64); days != 1 {
		t.Errorf("validity_days = %v for 120 km of ordinary cargo, want 1", days)
	}
	if vu, _ := body["valid_until"].(string); vu == "" {
		t.Errorf("valid_until is empty; a stored bill must always carry a computed expiry")
	}

	// ---- ⛔ THE DISCLOSURE BLOCK REACHED THE WIRE. This is the whole point of
	// the artefact: it is the only thing standing between a printed sheet and a
	// driver believing the consignment is covered.
	disclosures, _ := body["disclosures"].([]any)
	if len(disclosures) == 0 {
		t.Fatalf("the bill came back with NO disclosures: %s", rec.Body.String())
	}
	joined := ""
	for _, d := range disclosures {
		s, _ := d.(string)
		joined += s + "\n"
	}
	if !strings.Contains(joined, "NOT A GOVERNMENT-ISSUED") {
		t.Errorf("the disclosures do not state that this is not a government-issued e-way "+
			"bill. That sentence is the artefact's whole honesty:\n%s", joined)
	}
	if !strings.Contains(joined, "SECONDARY") {
		t.Errorf("the disclosures do not state that the Rule 138 figures are unverified "+
			"secondary material:\n%s", joined)
	}

	// ---- THE PAYLOAD IS THE REAL PAYLOAD, and it describes OUR goods.
	payload, _ := body["payload"].(map[string]any)
	if payload == nil {
		t.Fatalf("the bill came back with no payload: %s", rec.Body.String())
	}
	items, _ := payload["itemList"].([]any)
	if len(items) != 1 {
		t.Fatalf("the payload carries %d item(s), want 1: %v", len(items), payload["itemList"])
	}
	item, _ := items[0].(map[string]any)
	if hsn, _ := item["hsnCode"].(string); hsn != "7216" {
		t.Errorf("the payload's hsnCode = %q, want 7216 — the HSN the item master carries. "+
			"A wrong HSN here is a consignment described as different goods", hsn)
	}
	// 18% intra-State is 9 + 9. The e-way bill schema takes RATES where the
	// e-invoice takes amounts.
	if got, _ := item["cgstRate"].(string); got != "9" {
		t.Errorf("cgstRate = %q, want 9 (half of the line's 18%%)", got)
	}
	if got, _ := item["igstRate"].(string); got != "0" {
		t.Errorf("igstRate = %q, want 0 on an intra-State movement", got)
	}
	if got, _ := payload["totInvValue"].(string); got != "118000.00" {
		t.Errorf("totInvValue = %q, want 118000.00 (100,000 + 18%%)", got)
	}

	// ---- ⛔ AND NOW READ IT BACK OUT THROUGH THE OUTERMOST SURFACE. Everything
	// above could be a handler echoing its own input; only the GET proves it was
	// stored.
	grec, got := doRaw(t, srv, http.MethodGet, f.base+"/e-way-bills/"+voucherID, f.ownerTok, nil)
	if grec.Code != http.StatusOK {
		t.Fatalf("GET the bill back = %d, want 200 (%s)", grec.Code, grec.Body.String())
	}
	if back, _ := got["local_reference"].(string); back != ref {
		t.Errorf("the stored local_reference is %q but the raise returned %q", back, ref)
	}
	if backD, _ := got["disclosures"].([]any); len(backD) != len(disclosures) {
		t.Errorf("the raise returned %d disclosures and the read-back has %d. The block is "+
			"STORED, not re-derived, precisely so the two can never differ",
			len(disclosures), len(backD))
	}
	if backP, _ := got["payload"].(map[string]any); backP == nil || backP["totInvValue"] != "118000.00" {
		t.Errorf("the stored payload did not survive the round trip: %v", got["payload"])
	}
	if stale, _ := got["stale"].(bool); stale {
		t.Errorf("a bill read back with no intervening alter reports stale=true")
	}
	if gc, _ := got["generation_count"].(float64); gc != 1 {
		t.Errorf("generation_count = %v on a first raise, want 1", gc)
	}
}

// TestAnUnstatedDistanceIsRefusedWithItsReasoning pins the one blocker that must
// STOP the bill being raised.
//
// ⛔ AND THE CONTROL IS THE INVERSE, which is what makes it mean anything: the
// SAME consignment with a distance succeeds. Without that leg the refusal is
// consistent with the route being broken for every input.
func TestAnUnstatedDistanceIsRefusedWithItsReasoning(t *testing.T) {
	srv := newServer(t)
	f := newEWBFixture(t, srv)
	voucherID := postInvoice(t, srv, f, "200.0000")

	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/e-way-bills", f.ownerTok,
		raiseBody(voucherID, map[string]any{"distance_km": 0}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST with no distance = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	blockers, _ := body["blockers"].([]any)
	if len(blockers) == 0 {
		t.Fatalf("the refusal carries no blockers. Assess's reasoning is the point of the "+
			"refusal — an operator told only \"invalid\" cannot act: %s", rec.Body.String())
	}
	joined := ""
	for _, b := range blockers {
		s, _ := b.(string)
		joined += s + "\n"
	}
	if !strings.Contains(joined, "validity") {
		t.Errorf("the blockers do not explain that the VALIDITY could not be computed:\n%s", joined)
	}

	// ⛔ AND NOTHING WAS STORED. A 422 that half-wrote a row would leave a bill
	// with a guessed expiry, which is the exact outcome the refusal exists to
	// prevent.
	grec, _ := doRaw(t, srv, http.MethodGet, f.base+"/e-way-bills/"+voucherID, f.ownerTok, nil)
	if grec.Code != http.StatusNotFound {
		t.Errorf("after a refused raise, GET = %d, want 404 — the refusal must store nothing",
			grec.Code)
	}

	// ---- CONTROL: the same consignment WITH a distance is accepted.
	crec, _ := doRaw(t, srv, http.MethodPost, f.base+"/e-way-bills", f.ownerTok,
		raiseBody(voucherID, nil))
	if crec.Code != http.StatusCreated {
		t.Fatalf("CONTROL: the same consignment WITH a distance = %d, want 201. The refusal "+
			"above must be about the distance and not about the fixture (%s)",
			crec.Code, crec.Body.String())
	}
}

// TestAMissingVehicleDoesNotBlockThePartASlip is the counterweight to the test
// above, and it is the one a naive implementation gets wrong.
//
// Assess returns BOTH "no distance" and "no vehicle on a Part B movement" as
// Blockers. Refusing on any blocker would be the obvious reading and would be
// WRONG: raising Part A while the lorry is still being arranged is an ordinary
// thing an operator does. The blocker must travel to the ARTEFACT instead.
func TestAMissingVehicleDoesNotBlockThePartASlip(t *testing.T) {
	srv := newServer(t)
	f := newEWBFixture(t, srv)
	voucherID := postInvoice(t, srv, f, "200.0000")

	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/e-way-bills", f.ownerTok,
		raiseBody(voucherID, map[string]any{"vehicle_number": ""}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("a Part-A-only bill = %d, want 201: an operator may raise Part A while the "+
			"vehicle is still being arranged (%s)", rec.Code, rec.Body.String())
	}
	blockers, _ := body["blockers"].([]any)
	if len(blockers) != 1 {
		t.Fatalf("a Part-A-only bill reports %d blocker(s), want exactly 1 (the missing "+
			"vehicle): %v", len(blockers), blockers)
	}

	// ⛔ THE BLOCKER MUST REACH THE DISCLOSURES, which is what reaches paper. A
	// blocker that lives only in a JSON field a screen may not render is a
	// consignment that moves on a bill it may not move on.
	joined := ""
	for _, d := range body["disclosures"].([]any) {
		s, _ := d.(string)
		joined += s + "\n"
	}
	if !strings.Contains(joined, "BLOCKER:") || !strings.Contains(joined, "may not move") {
		t.Errorf("the Part B blocker did not reach the disclosure block, so it would not "+
			"reach the printed sheet:\n%s", joined)
	}
}

// TestAnAlterMarksTheBillStale is this feature's answer to §12 item 4a.
//
// e_way_bill is not re-stated by an ALTER (it keys on the voucher head), so the
// classic read-back defect cannot happen here. The defect that CAN happen is
// quieter: the invoice changes and the stored payload silently stops describing
// it. `stale` is derived on every read from the current version.
//
// ⛔ THE CONTROL IS THE FIRST HALF: the bill reads back NOT stale before the
// alter. Without it, a `stale` that was hard-coded true would pass.
func TestAnAlterMarksTheBillStale(t *testing.T) {
	srv := newServer(t)
	f := newEWBFixture(t, srv)
	voucherID := postInvoice(t, srv, f, "200.0000")

	rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/e-way-bills", f.ownerTok,
		raiseBody(voucherID, nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("raise = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	// ---- CONTROL: not stale before the alter.
	_, before := doRaw(t, srv, http.MethodGet, f.base+"/e-way-bills/"+voucherID, f.ownerTok, nil)
	if stale, _ := before["stale"].(bool); stale {
		t.Fatalf("CONTROL: the bill is stale before any alter, so the flag proves nothing")
	}

	// Alter the invoice — fewer goods than the bill describes.
	arec, _ := doRaw(t, srv, http.MethodPut, f.base+"/vouchers/sales/"+voucherID, f.ownerTok,
		map[string]any{
			"voucher_date":    "2026-06-10",
			"created_by":      "owner",
			"party_ledger_id": f.partyID,
			"item_ledger_id":  f.salesID,
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"cgst_ledger_id":             f.cgstID,
				"sgst_ledger_id":             f.sgstID,
			},
			"inventory": []any{map[string]any{
				"stock_item_id": f.itemID, "godown_id": f.godownID,
				"direction": "OUT", "qty": "150.0000", "rate": "500.0000",
			}},
		})
	if arec.Code != http.StatusOK && arec.Code != http.StatusCreated {
		t.Fatalf("alter the invoice = %d, want 200 (%s)", arec.Code, arec.Body.String())
	}

	grec, after := doRaw(t, srv, http.MethodGet, f.base+"/e-way-bills/"+voucherID, f.ownerTok, nil)
	if grec.Code != http.StatusOK {
		t.Fatalf("GET after the alter = %d, want 200 — the bill must SURVIVE an alter, not "+
			"vanish with it (%s)", grec.Code, grec.Body.String())
	}
	if stale, _ := after["stale"].(bool); !stale {
		t.Errorf("the invoice was altered and the bill still reads stale=false. The stored " +
			"payload now describes 200 units where the invoice says 150, and nothing else " +
			"in the system would ever say so")
	}
	// And the payload is UNCHANGED — stale means "no longer describes it", not
	// "was quietly rewritten".
	p, _ := after["payload"].(map[string]any)
	if got, _ := p["totInvValue"].(string); got != "118000.00" {
		t.Errorf("the stored payload changed to %q after an alter. A raised bill is a record "+
			"of what was raised; silently restating it would destroy the only evidence that "+
			"the two disagree", got)
	}
}

// TestARegenerationIsCountedRatherThanSilent pins migration 0047's
// one-bill-per-voucher decision and the upsert that goes with it.
func TestARegenerationIsCountedRatherThanSilent(t *testing.T) {
	srv := newServer(t)
	f := newEWBFixture(t, srv)
	voucherID := postInvoice(t, srv, f, "200.0000")

	for i, dist := range []int{120, 450} {
		rec, body := doRaw(t, srv, http.MethodPost, f.base+"/e-way-bills", f.ownerTok,
			raiseBody(voucherID, map[string]any{"distance_km": dist}))
		if rec.Code != http.StatusCreated {
			t.Fatalf("raise %d = %d, want 201: correcting a typo'd distance must not be "+
				"refused (%s)", i+1, rec.Code, rec.Body.String())
		}
		if gc, _ := body["generation_count"].(float64); int(gc) != i+1 {
			t.Errorf("raise %d reports generation_count=%v, want %d", i+1, gc, i+1)
		}
	}

	// The second generation's figures are the ones that survive: 450 km of
	// ordinary cargo is three days, not one.
	_, got := doRaw(t, srv, http.MethodGet, f.base+"/e-way-bills/"+voucherID, f.ownerTok, nil)
	if days, _ := got["validity_days"].(float64); days != 3 {
		t.Errorf("after regenerating at 450 km the stored validity_days = %v, want 3", days)
	}
	if km, _ := got["distance_km"].(float64); km != 450 {
		t.Errorf("the stored distance_km = %v, want 450 — the regeneration must replace the "+
			"consignment, not merge with it", km)
	}
}

// TestADeliveryNoteIsRefusedByNAME, not as a malformed voucher.
//
// ⛔ THE POINT IS THE SENTENCE, NOT THE STATUS CODE. A delivery note moves goods
// and carries no tax, so it lands on the same count mismatch a genuinely broken
// voucher would — and the first draft reported both as "your tax lines and stock
// lines do not correspond", which describes a perfectly well-formed document as
// malformed and sends an operator looking for a defect that is not there.
//
// This asserts that the refusal NAMES the real gap and says what to do instead.
// It is the kind of test HANDOFF §9.1 is about: the plausible wrong message is
// the one that gets written, and nothing but an assertion on the words stops it.
func TestADeliveryNoteIsRefusedByName(t *testing.T) {
	srv := newServer(t)
	f := newEWBFixture(t, srv)

	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/delivery-note", f.ownerTok,
		map[string]any{"voucher_date": "2026-06-12", "created_by": "owner",
			"inventory": []any{map[string]any{"stock_item_id": f.itemID, "godown_id": f.godownID,
				"direction": "OUT", "qty": "10.0000", "rate": "500.0000"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post a delivery note = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)

	erec, ebody := doRaw(t, srv, http.MethodPost, f.base+"/e-way-bills", f.ownerTok,
		raiseBody(voucherID, nil))
	if erec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("raising against a delivery note = %d, want 422 (%s)", erec.Code, erec.Body.String())
	}
	msg, _ := ebody["error"].(string)
	if !strings.Contains(msg, "delivery note") {
		t.Errorf("the refusal does not NAME the case. A delivery note is well formed and "+
			"simply carries no tax; telling the operator its lines do not correspond sends "+
			"them hunting a defect that is not there. Got: %q", msg)
	}
	if !strings.Contains(msg, "TAX INVOICE") {
		t.Errorf("the refusal does not say what to do instead (raise it against the tax "+
			"invoice for the same goods). Got: %q", msg)
	}
}

// TestAVoucherThatMovesNoGoodsIsRefused pins the domain rule.
func TestAVoucherThatMovesNoGoodsIsRefused(t *testing.T) {
	srv := newServer(t)
	f := newEWBFixture(t, srv)

	// A receipt: money, no goods.
	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/receipt", f.ownerTok,
		map[string]any{
			"voucher_date":    "2026-06-11",
			"created_by":      "owner",
			"party_ledger_id": f.partyID,
			"postings": []any{
				map[string]any{"ledger_id": f.salesID, "dr_cr": "D", "amount": "1000.0000"},
				map[string]any{"ledger_id": f.partyID, "dr_cr": "C", "amount": "1000.0000"},
			},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post a receipt = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)

	erec, _ := doRaw(t, srv, http.MethodPost, f.base+"/e-way-bills", f.ownerTok,
		raiseBody(voucherID, nil))
	if erec.Code != http.StatusUnprocessableEntity {
		t.Errorf("raising an e-way bill against a voucher that moves no goods = %d, want 422. "+
			"An e-way bill covers a MOVEMENT OF GOODS (%s)", erec.Code, erec.Body.String())
	}
}
