package server_test

// voucher_readback_audit_test.go — §4.3's READ-BACK AUDIT, RUN AGAINST THE
// CORRECTED QUESTION.
//
// ===========================================================================
// WHY THIS FILE EXISTS, AND WHY THE FIRST AUDIT DID NOT CLOSE IT
// ===========================================================================
// An ALTER RE-STATES. Every table keyed on `voucher_version_id` is rewritten
// from the request body, so whatever the body OMITS IS GONE — and nothing
// refuses it, because zero rows is a legal state: no trigger fires, no total
// stops adding up, and the only trace is a report that quietly changes its mind.
//
// §4.3's original audit asked "does `voucherFullJSON` EMIT it?" and ticked five
// tables off. That question is NECESSARY AND NOT SUFFICIENT, and it cleared a
// table that was actively losing data: `bill_allocation` was emitted by the GET
// all along, and NO FORM EVER SENT IT BACK, so editing the narration of any
// voucher carrying a bill reference DELETED it.
//
// THE QUESTION THIS FILE ASKS INSTEAD:
//
//	Does a body REBUILT FROM THAT GET RESPONSE send it back?
//
// Proved by POSTing over HTTP, GETting, rebuilding the PUT body FROM THAT
// RESPONSE, and reading the rows back out through the OUTERMOST report — WITH
// the inverse control (§9.3) that a PUT omitting them DESTROYS the rows. Without
// the control, "it survived" is equally consistent with the rows being immune to
// an alter, in which case the test proves nothing while reading as a guard.
//
// NO SQL ANYWHERE IN THIS FILE. Every fact enters and leaves through a request a
// browser could make.
//
// ===========================================================================
// ⛔⛔ WHAT THE AUDIT FOUND, so the next reader does not re-derive it
// ===========================================================================
// The brief named FOUR remaining tables. There were SIX in the class, and the
// sixth was the one losing data:
//
//	posting                  SAFE — an alter that omits it is REFUSED (422). It is
//	                         the one table in the class that CANNOT be silently
//	                         emptied, because a voucher with fewer than two legs is
//	                         not a voucher. Pinned by TestReadBackAudit_Postings...
//	stock_movement           SAFE on the wire — the GET carries the grid with real
//	                         uuids and an echo restates it. TestAlterViaHTTPPreserves
//	                         Stock covered the positive half and had NO INVERSE
//	                         CONTROL; the control is here, and it shows an alter that
//	                         echoes the postings but omits `inventory` is ACCEPTED and
//	                         writes the stock off to nothing.
//	posting_cost_allocation  SAFE on the wire; the client re-sends the splits from
//	                         two of its four builders and NOT from the
//	                         accounting-mode GST one — see the note on
//	                         TestReadBackAudit_CostAllocations...
//	order_fulfilment         SAFE on the wire, end to end, rebuilt entirely from the
//	                         GET (the existing test rebuilt only the LINKS from the
//	                         GET and the stock line from its fixture).
//	bill_allocation          already closed at §1.5.
//	voucher_gst_line         ⛔ WAS LOSING DATA, AND WAS NEVER ON §4.3's LIST AT ALL.
//	                         See TestReadBackAudit_GSTLines... below, which is the
//	                         whole point of this file.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/server"
)

// ---------------------------------------------------------------------------
// The fixture. One company, the ledgers every test below posts against, one
// cost axis with two centres, and one stock item on a shelf.
// ---------------------------------------------------------------------------

type readbackFixture struct {
	base     string
	ownerTok string
	subject  string

	debtor   string // Sundry Debtors, the party on the GST sale
	sales    string // Sales Accounts
	salaries string // Indirect Expenses — the leg a cost split lands on
	bank     string // Bank Accounts
	creditor string // Sundry Creditors, the party on the stock purchase
	purchase string // Purchase Accounts
	outCGST  string
	outSGST  string
	outIGST  string

	axis   string // cost category "Department"
	centre string // cost centre "Sales" on that axis

	itemID   string
	godownID string
}

func newReadbackFixture(t *testing.T, srv *server.Server) readbackFixture {
	t.Helper()
	// newAdminCompany creates the company over HTTP and registers
	// testdb.Cleanup(t, companyID) — the teardown census fails BY NAME if a
	// tenant table is left behind, so nothing in this file leaks.
	companyID, ownerTok, subject := newAdminCompany(t, srv)
	f := readbackFixture{base: "/v1/companies/" + companyID, ownerTok: ownerTok, subject: subject}

	// find-or-create, because migrations seed part of the chart per company and a
	// duplicate name is a raw unique violation rather than a no-op (§1.3). A
	// fixture that died on a 409 would be refusing for the wrong reason.
	mkLedger := func(name, group string) string {
		t.Helper()
		rec, body := doRaw(t, srv, http.MethodPost, f.base+"/ledgers", ownerTok,
			map[string]any{"name": name, "group_name": group, "created_by": subject})
		if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
			if id, _ := body["ledger_id"].(string); id != "" {
				return id
			}
		}
		lrec, lbody := doRaw(t, srv, http.MethodGet, f.base+"/ledgers", ownerTok, nil)
		if lrec.Code != http.StatusOK {
			t.Fatalf("list ledgers = %d (%s)", lrec.Code, lrec.Body.String())
		}
		rows, _ := lbody["ledgers"].([]any)
		for _, r := range rows {
			m, _ := r.(map[string]any)
			if m == nil {
				continue
			}
			if n, _ := m["name"].(string); n == name {
				if id, _ := m["ledger_id"].(string); id != "" {
					return id
				}
				if id, _ := m["id"].(string); id != "" {
					return id
				}
			}
		}
		t.Fatalf("could not create or find ledger %q (create said %d: %s)",
			name, rec.Code, rec.Body.String())
		return ""
	}

	f.debtor = mkLedger("Readback Buyer", "Sundry Debtors")
	f.sales = mkLedger("Domestic Sales", "Sales Accounts")
	f.salaries = mkLedger("Salaries & Wages", "Indirect Expenses")
	f.bank = mkLedger("HDFC Current A/c", "Bank Accounts")
	f.creditor = mkLedger("Readback Supplier", "Sundry Creditors")
	f.purchase = mkLedger("Purchases", "Purchase Accounts")
	f.outCGST = mkLedger("Output CGST", "Duties & Taxes")
	f.outSGST = mkLedger("Output SGST", "Duties & Taxes")
	f.outIGST = mkLedger("Output IGST", "Duties & Taxes")

	arec, abody := doRaw(t, srv, http.MethodPost, f.base+"/cost-categories", ownerTok,
		map[string]any{"name": "Department", "created_by": subject})
	if arec.Code != http.StatusCreated && arec.Code != http.StatusOK {
		t.Fatalf("create cost category = %d (%s)", arec.Code, arec.Body.String())
	}
	f.axis, _ = abody["id"].(string)
	crec, cbody := doRaw(t, srv, http.MethodPost, f.base+"/cost-centres", ownerTok,
		map[string]any{"cost_category_id": f.axis, "name": "Sales Dept", "created_by": subject})
	if crec.Code != http.StatusCreated && crec.Code != http.StatusOK {
		t.Fatalf("create cost centre = %d (%s)", crec.Code, crec.Body.String())
	}
	f.centre, _ = cbody["id"].(string)
	if f.axis == "" || f.centre == "" {
		t.Fatalf("the cost masters came back without ids (%s / %s)",
			arec.Body.String(), crec.Body.String())
	}

	irec, ibody := doRaw(t, srv, http.MethodPost, f.base+"/stock-items", ownerTok,
		map[string]any{
			"name": "Readback Bolt " + uuid.NewString()[:8], "unit_symbol": "Nos",
			"hsn": "7318", "gst_rate_pct": "18", "created_by": subject,
		})
	if irec.Code != http.StatusCreated && irec.Code != http.StatusOK {
		t.Fatalf("create stock item = %d (%s)", irec.Code, irec.Body.String())
	}
	if f.itemID, _ = ibody["stock_item_id"].(string); f.itemID == "" {
		f.itemID, _ = ibody["id"].(string)
	}
	if f.itemID == "" {
		t.Fatalf("the stock item came back with no id: %s", irec.Body.String())
	}
	// The godown is the one migration 0025 seeds per company; read it back the way
	// a client would rather than assuming an id.
	grec, gbody := doRaw(t, srv, http.MethodGet, f.base+"/godowns", ownerTok, nil)
	if grec.Code != http.StatusOK {
		t.Fatalf("list godowns = %d (%s)", grec.Code, grec.Body.String())
	}
	rows, _ := gbody["godowns"].([]any)
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if m == nil {
			continue
		}
		if id, _ := m["godown_id"].(string); id != "" {
			f.godownID = id
			break
		}
		if id, _ := m["id"].(string); id != "" {
			f.godownID = id
			break
		}
	}
	if f.godownID == "" {
		t.Fatalf("the company has no godown to move stock into: %s", grec.Body.String())
	}
	return f
}

// readbackGet fetches a voucher the way a form hydrating an alteration does.
func (f readbackFixture) get(t *testing.T, srv *server.Server, voucherID string) map[string]any {
	t.Helper()
	rec, body := doRaw(t, srv, http.MethodGet, f.base+"/vouchers/"+voucherID, f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /vouchers/%s = %d, want 200 (%s)", voucherID, rec.Code, rec.Body.String())
	}
	return body
}

// rebuild is THE OPERATION UNDER AUDIT: the PUT body a client assembles from
// nothing but the GET response.
//
// ⛔ IT COPIES ONLY THE NAMED KEYS, AND IT DOES NOT FAIL ON A MISSING ONE. That
// is the whole design: a read shape that silently omits a table produces a body a
// client would send perfectly happily, and the damage shows up in a report rather
// than here. A helper that t.Fatal'd on the missing key would report the defect as
// "the projection is wrong" instead of as what it costs.
func rebuild(view map[string]any, keys ...string) map[string]any {
	req := map[string]any{"created_by": "readback-audit"}
	for _, k := range keys {
		if v, ok := view[k]; ok {
			req[k] = v
		}
	}
	return req
}

// readbackPut alters through the ordinary route and returns the recorder.
func (f readbackFixture) put(t *testing.T, srv *server.Server, class, voucherID string, body map[string]any) (int, string) {
	t.Helper()
	rec, _ := doRaw(t, srv, http.MethodPut,
		f.base+"/vouchers/"+class+"/"+voucherID, f.ownerTok, body)
	return rec.Code, rec.Body.String()
}

// closing reads the item's closing QUANTITY AND CLOSING VALUE off the DERIVED
// stock summary, which replays the movement ledger. Nothing is stored for it to
// read stale, so a zero here means the movements are genuinely gone. ("", "") =
// no position at all.
//
// ⛔⛔ IT RETURNS BOTH FIGURES, AND THE VALUE IS THE ONE THAT WAS MISSING.
//
// A quantity-only oracle cannot see a VALUE-DESTROYING round trip, and this
// audit shipped with one — as did TestAlterViaHTTPPreservesStock
// (handler_voucher_inventory_test.go), which this file's own header criticises
// for a missing control while making the same mistake about the money. Mutate
// the GET projection at handler_voucher_inventory.go's `"rate"` key to
// "0.0000" — a grid handed out WITHOUT the price a form would send back — and
// the alter rebuilt from it restates ten units at no price:
//
//	BEFORE alter: closing_qty=10.0000 closing_value=1000.0000
//	AFTER  alter: closing_qty=10.0000 closing_value=0.0000
//
// The quantity never moves, so a qty-only assertion stays green while 1,000 of
// stock value is written off by a narration edit. CLOSING STOCK IS A BALANCE
// SHEET LINE (it is what `total_value` on this very response feeds), so this is
// a Balance Sheet defect wearing a warehouse costume, and `closing_value` is the
// only oracle in this file that can see it.
//
// The value is DERIVED (qty x rate less discount, replayed through
// internal/inventory's valuer) and is never read out of stock_movement.amount —
// which is why the rate, and not the amount, is the field a lossy projection
// costs money on.
func (f readbackFixture) closing(t *testing.T, srv *server.Server) (qty, value string) {
	t.Helper()
	rec, body := doRaw(t, srv, http.MethodGet, f.base+"/stock-summary", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-summary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	rows, _ := body["items"].([]any)
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if m == nil {
			continue
		}
		if id, _ := m["stock_item_id"].(string); id == f.itemID {
			q, _ := m["closing_qty"].(string)
			v, _ := m["closing_value"].(string)
			return q, v
		}
	}
	return "", ""
}

// centreOwn reads one cost centre's own debit off the Cost Centre Summary — a
// projection of posting_cost_allocation, and the report an operator would notice
// quietly stop adding up.
func (f readbackFixture) centreOwn(t *testing.T, srv *server.Server, name string) string {
	t.Helper()
	rec, body := doRaw(t, srv, http.MethodGet,
		f.base+"/cost-centre-summary?cost_category_id="+f.axis+"&from=2026-04-01&to=2027-03-31",
		f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /cost-centre-summary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var found string
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, n := range nodes {
			m, _ := n.(map[string]any)
			if m == nil {
				continue
			}
			if got, _ := m["name"].(string); got == name {
				found, _ = m["own_debit"].(string)
			}
			if kids, ok := m["children"].([]any); ok {
				walk(kids)
			}
		}
	}
	if centres, ok := body["centres"].([]any); ok {
		walk(centres)
	}
	return found
}

// hsnCodes lists the HSN codes the GSTR-1 table-12 summary reports for the
// period. It is built in SQL straight off `voucher_gst_line`, so it is the honest
// witness to whether those rows still exist — and it is a DIFFERENT query from the
// voucher GET, so the two agreeing is a cross-check rather than one read
// reformatted.
func (f readbackFixture) hsnCodes(t *testing.T, srv *server.Server) []string {
	t.Helper()
	rec, body := doRaw(t, srv, http.MethodGet,
		f.base+"/gstr1?from=2026-06-01&to=2026-06-30", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /gstr1 = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	hsn, _ := body["hsn"].(map[string]any)
	rows, _ := hsn["data"].([]any)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if m == nil {
			continue
		}
		code, _ := m["hsn_sc"].(string)
		out = append(out, code)
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. `posting` — VERDICT: SAFE, and safe for a structural reason rather than a
//    client-side one, which is why it is the only table here that needs no fix.
// ---------------------------------------------------------------------------

// TestReadBackAuditPostingsCannotBeSilentlyEmptiedByAnAlter asks §4.3's corrected
// question of `posting`, whose entry on the "already checked" list read "re-sent
// by the form (the alter tests cover them)". That claim is inherited and this test
// does not inherit it.
//
// The answer is better than "the client remembers": a voucher with fewer than two
// legs is REFUSED (validatePostings, ledger.go), so an alter that omits the
// postings cannot quietly succeed the way an alter omitting bill references,
// stock lines, cost splits or order links can. `posting` is the one table in the
// class that is safe by construction.
//
// ⛔ THE THREE LEGS, AND THE THIRD IS WHAT MAKES THE OTHER TWO MEAN ANYTHING:
//
//  1. the round trip: a body rebuilt from the GET leaves the ledger untouched;
//  2. the CONTROL that an alter genuinely RE-STATES — altering with DIFFERENT
//     postings moves the trial balance. Without this, leg 1 is consistent with
//     postings being immune to an alter and proves nothing (§9.3);
//  3. the refusal, WITH its own control that it refused for the RIGHT reason —
//     the message names the two-posting rule, and the ledger did not move.
func TestReadBackAuditPostingsCannotBeSilentlyEmptiedByAnAlter(t *testing.T) {
	srv := newServer(t)
	f := newReadbackFixture(t, srv)

	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-15", "narration": "salary provision",
			"created_by": f.subject,
			"postings": []any{
				map[string]any{"ledger_id": f.salaries, "dr_cr": "D", "amount": "50000.0000"},
				map[string]any{"ledger_id": f.bank, "dr_cr": "C", "amount": "50000.0000"},
			},
		})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST journal = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)

	view := f.get(t, srv, voucherID)
	postings, _ := view["postings"].([]any)
	if len(postings) != 2 {
		t.Fatalf("the GET returned %d posting(s), want 2: %v", len(postings), view["postings"])
	}

	// ---- 1. THE ROUND TRIP: everything from the GET, one unrelated field changed.
	req := rebuild(view, "voucher_no", "voucher_date", "party", "party_ledger_id",
		"narration", "postings")
	req["narration"] = "salary provision (typo fixed)"
	if code, msg := f.put(t, srv, "journal", voucherID, req); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("altering the narration = %d, want 200 (%s)", code, msg)
	}
	after := f.get(t, srv, voucherID)
	if got, _ := after["postings"].([]any); len(got) != 2 {
		t.Fatalf("after an alter rebuilt from the GET the voucher carries %d posting(s), want 2",
			len(got))
	}

	// ---- 2. THE CONTROL THAT AN ALTER RE-STATES. Halve the entry and the ledger
	// must follow. If it did not, leg 1 above would pass for any body at all.
	half := rebuild(view, "voucher_no", "voucher_date", "narration")
	half["postings"] = []any{
		map[string]any{"ledger_id": f.salaries, "dr_cr": "D", "amount": "25000.0000"},
		map[string]any{"ledger_id": f.bank, "dr_cr": "C", "amount": "25000.0000"},
	}
	if code, msg := f.put(t, srv, "journal", voucherID, half); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("CONTROL: altering to a different amount = %d, want 200 (%s)", code, msg)
	}
	restated := f.get(t, srv, voucherID)
	rows, _ := restated["postings"].([]any)
	if len(rows) != 2 {
		t.Fatalf("CONTROL: the restated voucher carries %d posting(s), want 2", len(rows))
	}
	first, _ := rows[0].(map[string]any)
	if amt, _ := first["amount"].(string); amt != "25000.0000" {
		t.Fatalf("CONTROL: after altering to 25,000 the first posting reads %q, want 25000.0000 — "+
			"if an alter did not re-state the postings, the round-trip leg above would pass "+
			"whatever body it sent", amt)
	}

	// ---- 3. THE REFUSAL. An alter with NO postings is a 422, not a silent
	// emptying — the one thing that distinguishes this table from the other five.
	code, msg := f.put(t, srv, "journal", voucherID,
		rebuild(view, "voucher_no", "voucher_date", "narration"))
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("an alter with NO postings = %d, want 422. If a voucher may be emptied of its "+
			"legs, `posting` is in the same silent-deletion class as bill_allocation and this "+
			"whole audit has a sixth entry (body: %s)", code, msg)
	}
	// ⛔ AND IT REFUSED FOR THE RIGHT REASON (§9.3). A 422 raised by, say, a
	// numbering clash would read as coverage and be none.
	if !strings.Contains(msg, "at least two postings") {
		t.Fatalf("the refusal does not name the double-entry rule, so it may have refused for an "+
			"unrelated reason: %s", msg)
	}
	// And it changed nothing: the refusal rolled back rather than half-applying.
	survived := f.get(t, srv, voucherID)
	srows, _ := survived["postings"].([]any)
	if len(srows) != 2 {
		t.Fatalf("after the REFUSED alter the voucher carries %d posting(s), want the 2 it had",
			len(srows))
	}
}

// ---------------------------------------------------------------------------
// 2. `stock_movement` — VERDICT: SAFE on the wire. The control that was missing.
// ---------------------------------------------------------------------------

// TestReadBackAuditStockMovementsSurviveAnAlterRebuiltFromTheGet asks the
// corrected question of `stock_movement`, whose §4.3 entry also read "re-sent by
// the form (the alter tests cover them)".
//
// THE TEST THAT COVERS IT IS TestAlterViaHTTPPreservesStock
// (handler_voucher_inventory_test.go:434). It is a real test and it does the
// positive half properly — it echoes the fetched body rather than a hand-written
// one. ⛔ BUT IT HAS NO INVERSE CONTROL. Its "control" is that the POSTINGS
// survived, which is a different proposition: on its own, "the stock was still
// there after a no-op alter" is consistent with movements being immune to an
// alter, and in that world the test would pass no matter what the projection
// returned (§9.3 — the same gap that let `bill_allocation` be ticked off).
//
// So the leg that matters here is the second one: an alter that echoes the
// postings and OMITS `inventory` is ACCEPTED — 200, a tidy version 3, the trial
// balance unchanged — and the goods are gone from the movement ledger. That is
// the danger the first leg depends on being real.
//
// ⛔⛔ AND EVERY LEG NOW ASSERTS THE VALUE AS WELL AS THE QUANTITY. The first
// version of this test declared `stock_movement` SAFE on a QUANTITY-ONLY oracle,
// which is exactly the criticism it levels at TestAlterViaHTTPPreservesStock —
// and it was wrong in the same way: a GET that hands out the grid WITHOUT the
// price (the reviewer's mutation: project `"rate"` as "0.0000") produces a
// perfectly acceptable PUT that leaves ten units on the shelf carried at NOTHING.
// Ten units and 1,000 of value went in; ten units and zero came out; a qty-only
// assertion says SAFE. See f.closing's comment for why that is a Balance Sheet
// defect and not a warehouse one.
func TestReadBackAuditStockMovementsSurviveAnAlterRebuiltFromTheGet(t *testing.T) {
	srv := newServer(t)
	f := newReadbackFixture(t, srv)

	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/purchase", f.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-15", "narration": "goods in",
			"party": "Readback Supplier", "party_ledger_id": f.creditor,
			"created_by": f.subject,
			"postings": []any{
				map[string]any{"ledger_id": f.purchase, "dr_cr": "D", "amount": "1000.0000"},
				map[string]any{"ledger_id": f.creditor, "dr_cr": "C", "amount": "1000.0000"},
			},
			"inventory": []any{map[string]any{
				"stock_item_id": f.itemID, "godown_id": f.godownID, "batch_id": "",
				"direction": "IN", "qty": "10.0000", "rate": "100.0000", "amount": "1000.0000",
			}},
		})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST purchase with stock = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)
	if q, v := f.closing(t, srv); q != "10.0000" || v != "1000.0000" {
		t.Fatalf("after the purchase the shelf reads qty=%q value=%q, want 10.0000 / 1000.0000", q, v)
	}

	view := f.get(t, srv, voucherID)
	if inv, ok := view["inventory"].([]any); !ok || len(inv) != 1 {
		t.Fatalf("GET /vouchers/{id} returned %v for `inventory`, want one line — a client "+
			"rebuilding an alter from this response could not restate the goods", view["inventory"])
	}

	// ---- THE ROUND TRIP.
	req := rebuild(view, "voucher_no", "voucher_date", "party", "party_ledger_id",
		"narration", "postings", "inventory")
	req["narration"] = "goods in (typo fixed)"
	if code, msg := f.put(t, srv, "purchase", voucherID, req); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("altering the narration = %d, want 200 (%s)", code, msg)
	}
	// ⛔ BOTH FIGURES, AND THE SECOND ONE IS THE POINT. The quantity surviving is
	// the cheap half: it survives even a projection that hands out the grid with
	// no PRICE on it, because the qty is re-stated either way. The VALUE is what a
	// lossy read shape costs, and 10 units carried at nothing is a Balance Sheet
	// that lost 1,000 to a narration edit.
	if q, v := f.closing(t, srv); q != "10.0000" || v != "1000.0000" {
		t.Fatalf("after an alter rebuilt from the GET the shelf reads qty=%q value=%q, want "+
			"10.0000 / 1000.0000 — the round trip wrote the stock off. A value of 0 against a "+
			"correct quantity means the GET handed out the grid WITHOUT the rate a form sends "+
			"back, and closing stock is a Balance Sheet line", q, v)
	}

	// ---- ⛔ THE INVERSE CONTROL THE EXISTING TEST DOES NOT HAVE.
	//
	// Same body, `inventory` deliberately dropped. It must be ACCEPTED and it must
	// empty the shelf; if it were refused, or if the movements survived, the leg
	// above would be inert.
	omitted := rebuild(view, "voucher_no", "voucher_date", "party", "party_ledger_id",
		"narration", "postings")
	if code, msg := f.put(t, srv, "purchase", voucherID, omitted); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("CONTROL: an alter omitting `inventory` = %d, want 200 (%s)", code, msg)
	}
	q, v := f.closing(t, srv)
	if q != "" && q != "0.0000" {
		t.Fatalf("CONTROL: after an alter that OMITTED `inventory` the closing qty is %q, want 0 "+
			"or no position at all. An alter is supposed to RE-STATE — if omitting the grid did "+
			"not clear the movements, the round-trip assertion above proves nothing", q)
	}
	if v != "" && v != "0.0000" {
		t.Fatalf("CONTROL: after an alter that OMITTED `inventory` the closing VALUE is %q, want "+
			"0 or no position at all", v)
	}

	// ---- AND THE STOCK COMES BACK, IN BOTH DIMENSIONS, when the grid is
	// restated — which is what shows the zeroes above were caused by the omission
	// and not by the voucher breaking.
	if code, msg := f.put(t, srv, "purchase", voucherID, req); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("restating the grid = %d, want 200 (%s)", code, msg)
	}
	if q, v := f.closing(t, srv); q != "10.0000" || v != "1000.0000" {
		t.Fatalf("after restating the grid the shelf reads qty=%q value=%q, want 10.0000 / 1000.0000", q, v)
	}
}

// ---------------------------------------------------------------------------
// 2b. THE QUESTION THE AUDIT ABOVE RAISES AND NOBODY HAD ASKED: the server
//     ACCEPTED `rate 0.0000` alongside `amount 1000.0000`. Is that a defect?
// ---------------------------------------------------------------------------

// TestAnInventoryLineWhoseAmountContradictsItsOwnRateIsRefused answers it: YES,
// and the answer is now enforced in ledger.validateInventory (stock.go).
//
// ===========================================================================
// WHY AN INCONSISTENT PAIR CANNOT BE LEGAL
// ===========================================================================
// `amount` is written, hashed and PRINTED, and it is read back for value by
// NOTHING. Every stock figure in this product — /stock-summary's closing_value,
// its total_value, the godown and batch grids, the P&L's closing-stock term — is
// REPLAYED from qty x rate less discount through internal/inventory's valuer,
// whose Movement.Value() comment already stated the invariant as fact:
// "stock_movement.amount MUST equal it". Nothing enforced it outside the
// item-invoice generator.
//
// So `qty 10, rate 0.0000, amount 1000.0000` is not an override, it is two
// contradictory statements of what the goods are worth: the supplier's account
// is credited 1,000, the document prints 1,000, and the Balance Sheet carries
// the goods at ZERO. A 1,000-rupee disagreement is not a rounding artefact, and
// there is no reading of the pair that is not a guess about which number the
// operator meant. Tally's own grid never stores the pair — typing over the
// Amount back-computes the Rate — so refusing costs no parity.
//
// ⚠️ AND THE REFUSAL IS THE NON-NAIVE ONE, which is the whole reason this test
// has four legs. A DISCOUNT MAKES `amount != qty x rate` ON PURPOSE
// (InventoryLine.TaxableValue = qty x rate x (1 - disc)), so a check written
// against qty x rate would reject every discounted invoice in the product. Leg 3
// is that invoice, and it must be ACCEPTED and VALUED AT THE DISCOUNTED FIGURE.
//
// ⛔ THE CONTROLS ARE WHAT MAKE THE REFUSAL MEAN WHAT IT SAYS (§9.3): leg 2 pins
// that it refused for the CONTRADICTION and not for a zero rate, and leg 5 posts
// a genuinely free inward — rate 0.0000, no amount — and requires it through.
func TestAnInventoryLineWhoseAmountContradictsItsOwnRateIsRefused(t *testing.T) {
	srv := newServer(t)
	f := newReadbackFixture(t, srv)

	purchase := func(line map[string]any, postingAmt string) (int, string) {
		t.Helper()
		rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/purchase", f.ownerTok,
			map[string]any{
				"voucher_date": "2026-06-15", "narration": "goods in",
				"party": "Readback Supplier", "party_ledger_id": f.creditor,
				"created_by": f.subject,
				"postings": []any{
					map[string]any{"ledger_id": f.purchase, "dr_cr": "D", "amount": postingAmt},
					map[string]any{"ledger_id": f.creditor, "dr_cr": "C", "amount": postingAmt},
				},
				"inventory": []any{line},
			})
		return rec.Code, rec.Body.String()
	}

	// ---- 1. THE REFUSAL. Exactly the body the mutated projection above produced.
	code, msg := purchase(map[string]any{
		"stock_item_id": f.itemID, "godown_id": f.godownID, "batch_id": "",
		"direction": "IN", "qty": "10.0000", "rate": "0.0000", "amount": "1000.0000",
	}, "1000.0000")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("a line stating 10 @ 0.0000 with an amount of 1,000 = %d, want 422. Accepting it "+
			"credits the supplier 1,000, prints 1,000 and carries the goods on the Balance Sheet "+
			"at nothing (%s)", code, msg)
	}

	// ---- 2. ⛔ AND IT REFUSED FOR THE RIGHT REASON. A 422 raised by, say, the
	// negative-stock guard or a numbering clash would read as coverage and be none.
	// The message must name the contradiction AND both numbers, because that is
	// what tells the operator which of the two figures to fix.
	for _, want := range []string{"contradicts qty x rate less discount", "1000.0000", "0.0000"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal does not mention %q, so it may have refused for an unrelated "+
				"reason: %s", want, msg)
		}
	}
	// And it wrote nothing: a refusal that half-applied would leave the movement
	// behind while telling the operator the voucher did not post.
	if q, v := f.closing(t, srv); q != "" && q != "0.0000" || v != "" && v != "0.0000" {
		t.Fatalf("after the REFUSED purchase the shelf reads qty=%q value=%q, want no position "+
			"at all", q, v)
	}

	// ---- 3. ⛔⛔ THE INPUT THAT DISTINGUISHES A CORRECT REFUSAL FROM A NAIVE ONE.
	//
	// A DISCOUNTED line: 10 @ 100.0000 less 10% is a line total of 900, and
	// `amount != qty x rate` is the CORRECT state of that line. A check written
	// against qty x rate refuses this — every discounted invoice in the product —
	// and this leg is the one that goes red if anyone rewrites the rule that way.
	if code, msg := purchase(map[string]any{
		"stock_item_id": f.itemID, "godown_id": f.godownID, "batch_id": "",
		"direction": "IN", "qty": "10.0000", "rate": "100.0000",
		"discount_pct": "10.0000", "amount": "900.0000",
	}, "900.0000"); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("a DISCOUNTED line (10 @ 100 less 10%% = 900) = %d, want 201. A discount makes "+
			"amount != qty x rate on purpose; refusing it would reject every discounted invoice "+
			"in the product (%s)", code, msg)
	}
	if q, v := f.closing(t, srv); q != "10.0000" || v != "900.0000" {
		t.Fatalf("after the discounted purchase the shelf reads qty=%q value=%q, want 10.0000 / "+
			"900.0000 — the discount belongs in the carried value, not only on the printout", q, v)
	}

	// ---- 4. AN OMITTED AMOUNT IS "DERIVE IT", not a contradiction of zero. This
	// is what the F8/F9 grid sends, and it must not have become a 422.
	if code, msg := purchase(map[string]any{
		"stock_item_id": f.itemID, "godown_id": f.godownID, "batch_id": "",
		"direction": "IN", "qty": "5.0000", "rate": "100.0000",
	}, "500.0000"); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("a line with NO amount = %d, want 201 — zero means \"you compute it\" (%s)", code, msg)
	}
	if q, v := f.closing(t, srv); q != "15.0000" || v != "1400.0000" {
		t.Fatalf("after the amount-less purchase the shelf reads qty=%q value=%q, want 15.0000 / "+
			"1400.0000", q, v)
	}

	// ---- 5. ⛔ THE OTHER CONTROL: A ZERO RATE IS NOT WHAT WAS REFUSED.
	//
	// A free sample really does come in at 0.0000 — a stated fact, not a missing
	// price (see InventoryLine.RateUnstated) — and it must still post. A Receipt
	// Note carries it because a stock-only class needs no money side to state.
	// Without this leg, leg 1 is equally consistent with "the server refuses a
	// zero rate", which would be a different and much worse rule.
	rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/receipt-note", f.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-16", "narration": "free samples from the supplier",
			"created_by": f.subject,
			"inventory": []any{map[string]any{
				"stock_item_id": f.itemID, "godown_id": f.godownID, "batch_id": "",
				"direction": "IN", "qty": "4.0000", "rate": "0.0000",
			}},
		})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: a genuinely free inward (4 @ 0.0000, no amount) = %d, want 201 — the "+
			"rule under test is about a CONTRADICTED total, not about a zero price (%s)",
			rec.Code, rec.Body.String())
	}
	if q, v := f.closing(t, srv); q != "19.0000" || v != "1400.0000" {
		t.Fatalf("after the free inward the shelf reads qty=%q value=%q, want 19.0000 / 1400.0000 "+
			"— four more units, not one rupee more value", q, v)
	}
}

// ---------------------------------------------------------------------------
// 3. `posting_cost_allocation` — VERDICT: SAFE ON THE WIRE, and the client half
//    is where the remaining hole is.
// ---------------------------------------------------------------------------

// TestReadBackAuditCostAllocationsSurviveAnAlterRebuiltFromTheGet asks the
// corrected question of `posting_cost_allocation`, whose §4.3 entry read
// "hydrated by allocationsFromView — PROBABLY fine". "Probably" is what
// `bill_allocation` had.
//
// THE WIRE HALF PASSES: `cost_allocations` is on the GET, its `posting_seq`
// indexes the response's own `postings` array, and a body that echoes BOTH
// restates the split exactly.
//
// ⛔ THE CLIENT HALF IS ONLY PARTLY CLOSED, AND THIS TEST CANNOT SEE WHICH PART —
// stated here because a green test whose scope is narrower than its name is how
// the first audit went wrong. frontend VoucherScreen.tsx has FOUR accept
// builders and only two of them re-send `cost_allocations`:
//
//	buildSingleEntryRequest   contra / payment / receipt          ✅ sends them
//	buildDoubleEntryRequest   journal (the class this test posts) ✅ sends them
//	buildGstRequest           accounting-mode sales / purchase /
//	                          credit-note / debit-note            ⛔ DOES NOT
//	buildItemRequest          the F8/F9 item grid                 n/a — the server
//	                          refuses a split on an item invoice by design
//
// So the CONTROL leg below is not a hypothetical for the GST classes: a split
// that reached one of them (over HTTP, or from an import) is destroyed by the
// next browser alter. The exact patch is in the audit's report.
func TestReadBackAuditCostAllocationsSurviveAnAlterRebuiltFromTheGet(t *testing.T) {
	srv := newServer(t)
	f := newReadbackFixture(t, srv)

	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-15", "narration": "salaries, split to a department",
			"created_by": f.subject,
			"postings": []any{
				map[string]any{"ledger_id": f.salaries, "dr_cr": "D", "amount": "50000.0000"},
				map[string]any{"ledger_id": f.bank, "dr_cr": "C", "amount": "50000.0000"},
			},
			"cost_allocations": []any{map[string]any{
				"posting_seq": 0, "cost_category_id": f.axis,
				"cost_centre_id": f.centre, "amount": "50000.0000",
			}},
		})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST journal with an allocation = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)
	if got := f.centreOwn(t, srv, "Sales Dept"); got != "50000.0000" {
		t.Fatalf("the Cost Centre Summary reads %q for the department, want 50000.0000", got)
	}

	view := f.get(t, srv, voucherID)
	allocs, ok := view["cost_allocations"].([]any)
	if !ok || len(allocs) != 1 {
		t.Fatalf("GET /vouchers/{id} returned %v for `cost_allocations`, want one split",
			view["cost_allocations"])
	}
	// ⛔⛔ THE DISTINGUISHING INPUT, AND IT IS SHARPER THAN IT LOOKS.
	//
	// The POST above stated `posting_seq: 0`, because Salaries is postings[0] in the
	// array the REQUEST sent. The GET returns `posting_seq: 1`. Both are correct and
	// they are different numbers: the projection re-derives the index against its own
	// slice, which is ordered `dr_cr DESC, amount DESC`, and `dr_cr` is the enum
	// ('D','C') — so DESC puts the CREDIT first and the allocated debit second.
	//
	// That asymmetry is what makes this test able to fail for the right reason. A
	// client that carried the sequence it SENT back onto the wire would allocate the
	// BANK leg: a departmental cost attributed to a cash account, on a voucher that
	// balances and a Cost Centre Summary that foots to the same total. Nothing
	// downstream can detect it. Echoing the GET's `postings` and its
	// `cost_allocations` TOGETHER is the only rebuild that stays consistent, and it
	// is the rebuild the leg below performs.
	am, _ := allocs[0].(map[string]any)
	if seq, _ := am["posting_seq"].(float64); seq != 1 {
		t.Fatalf("the split's posting_seq = %v, want 1 — the index of the DEBIT leg in THIS "+
			"response's postings array, which is ordered (dr_cr DESC, amount DESC) and so leads "+
			"with the credit. The request stated 0. If these two ever agree by construction, "+
			"this test has stopped being able to tell a re-derived index from an echoed one",
			am["posting_seq"])
	}

	// ---- THE ROUND TRIP.
	req := rebuild(view, "voucher_no", "voucher_date", "narration", "postings", "cost_allocations")
	req["narration"] = "salaries, split to a department (typo fixed)"
	if code, msg := f.put(t, srv, "journal", voucherID, req); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("altering the narration = %d, want 200 (%s)", code, msg)
	}
	if got := f.centreOwn(t, srv, "Sales Dept"); got != "50000.0000" {
		t.Fatalf("after an alter rebuilt from the GET the department reads %q, want 50000.0000 — "+
			"the split did not survive the round trip", got)
	}

	// ---- ⛔ THE INVERSE CONTROL, AND IT IS THE SHIPPED CLIENT'S BEHAVIOUR ON A
	// JOURNAL. Drop the splits and keep everything else.
	omitted := rebuild(view, "voucher_no", "voucher_date", "narration", "postings")
	if code, msg := f.put(t, srv, "journal", voucherID, omitted); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("CONTROL: an alter omitting `cost_allocations` = %d, want 200 (%s)", code, msg)
	}
	if got := f.centreOwn(t, srv, "Sales Dept"); got != "" && got != "0.0000" {
		t.Fatalf("CONTROL: after an alter that OMITTED the splits the department still reads %q, "+
			"want 0 or absent. If omitting them did not clear them, the round-trip leg proves "+
			"nothing", got)
	}
	// ⚠️ AND THE COST IS STILL IN THE PROFIT & LOSS. That is exactly what makes this
	// class of defect invisible: nothing is out of balance, the expense is untouched,
	// and only the report that attributes it has quietly changed its answer.
	stillThere := f.get(t, srv, voucherID)
	prows, _ := stillThere["postings"].([]any)
	if len(prows) != 2 {
		t.Fatalf("the expense itself vanished with the split (%d postings) — then the defect "+
			"would be loud, and it is not", len(prows))
	}
}

// ---------------------------------------------------------------------------
// 4. `order_fulfilment` — VERDICT: SAFE on the wire, now proved with the WHOLE
//    body rebuilt from the GET.
// ---------------------------------------------------------------------------

// TestReadBackAuditOrderLinksSurviveAnAlterRebuiltEntirelyFromTheGet asks the
// corrected question of `order_fulfilment`, whose §4.3 entry read "hydrated by
// the fulfilment picker — PROBABLY fine".
//
// ⚠️ WHAT IT ADDS OVER
// TestTheVoucherGETCarriesItsOrderLinksSoAnAlterCannotSilentlyReopenTheOrder,
// which already exists and already carries its control: that test rebuilds the
// LINKS from the GET and the STOCK LINE from `deliveryBody`, its own fixture
// helper. A form has no fixture. The link is keyed on
// (voucher_version_id, line_no) and line_no is the STOCK line's number, so "the
// links round-trip" and "the links round-trip against a grid that was also
// rebuilt from the response" are two different claims, and only the second is the
// one an operator relies on. This test sends nothing the GET did not give it.
//
// ⛔⛔ AND THE DELIVERY NOTE HAS TWO LINES, WITH THE LINK ON LINE 2. The first
// version of this test posted ONE inventory line, which made `line_no` trivially
// 1 however it was derived — a projection that hard-coded 1, re-derived the index
// from its own slice, or lost the column entirely would all have produced the
// same green. A single-line grid cannot distinguish a mapping that works from a
// mapping that cannot be wrong, and this test exists to make exactly that claim.
//
// The second line moves a DIFFERENT ITEM, which is what turns a mis-mapped
// line_no from an invisible relabelling into a refusal: migration 0042's
// order_fulfilment_within_commitment requires the linked movement to name the
// SAME stock item as the order line. Two lines of the same item would leave a
// link that slid from line 2 to line 1 perfectly acceptable and perfectly wrong.
// The last leg posts that slide deliberately and requires the 422.
func TestReadBackAuditOrderLinksSurviveAnAlterRebuiltEntirelyFromTheGet(t *testing.T) {
	srv := newServer(t)
	f := newOrderHTTPFixture(t, srv)

	// A SECOND ITEM, and the stock to ship it with. Both over HTTP: a Receipt Note
	// is the ordinary way goods arrive without an invoice, so nothing here needs
	// the fixture's SQL.
	irec, ibody := doRaw(t, srv, http.MethodPost, f.base+"/stock-items", f.ownerTok,
		map[string]any{"name": "Packing Crate " + uuid.NewString()[:8], "created_by": "owner"})
	if irec.Code != http.StatusCreated && irec.Code != http.StatusOK {
		t.Fatalf("create the second stock item = %d, want 201 (%s)", irec.Code, irec.Body.String())
	}
	crateID, _ := ibody["stock_item_id"].(string)
	if crateID == "" {
		crateID, _ = ibody["id"].(string)
	}
	if crateID == "" {
		t.Fatalf("the second stock item came back with no id: %s", irec.Body.String())
	}
	rnrec, _ := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/receipt-note", f.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-05", "narration": "Crates in", "created_by": "owner",
			"inventory": []any{map[string]any{
				"stock_item_id": crateID, "godown_id": f.godownID,
				"direction": "IN", "qty": "50.0000", "rate": "10.0000", "amount": "500.0000",
			}},
		})
	if rnrec.Code != http.StatusCreated && rnrec.Code != http.StatusOK {
		t.Fatalf("receipt note for the crates = %d, want 201 (%s)", rnrec.Code, rnrec.Body.String())
	}

	// ⛔ THE DISTINGUISHING GRID: crates on line 1, the ORDERED item on line 2, and
	// the link naming line 2. Both lines move 8 units, so a link that slid onto
	// line 1 would break exactly ONE invariant (the item) and the refusal it earns
	// can only mean the one thing.
	twoLineGrid := []any{
		map[string]any{
			"stock_item_id": crateID, "godown_id": f.godownID,
			"direction": "OUT", "qty": "8.0000", "rate": "10.0000", "amount": "80.0000",
		},
		map[string]any{
			"stock_item_id": f.itemID, "godown_id": f.godownID,
			"direction": "OUT", "qty": "8.0000", "rate": "500.0000", "amount": "4000.0000",
		},
	}
	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/delivery-note", f.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-10",
			"narration":    "Part delivery against the order",
			"created_by":   "owner",
			"inventory":    twoLineGrid,
			"fulfilments": []any{map[string]any{
				"line_no": 2, "order_line_id": f.orderLineID, "qty": "8",
			}},
		})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST a two-line delivery note linked on line 2 = %d, want 201 (%s)",
			rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)
	if out, done, _ := salesOrderBook(t, srv, f); out != "12.0000" || done != "8.0000" {
		t.Fatalf("after a linked delivery of 8/20 the order reads %q/%q, want 12/8", out, done)
	}

	grec, view := doRaw(t, srv, http.MethodGet, f.base+"/vouchers/"+voucherID, f.ownerTok, nil)
	if grec.Code != http.StatusOK {
		t.Fatalf("GET the delivery note = %d, want 200 (%s)", grec.Code, grec.Body.String())
	}
	// ⛔ THE READ-BACK THE SINGLE-LINE VERSION COULD NOT MAKE: the projection must
	// hand back the STORED line_no — 2 — and the grid it indexes, in the order it
	// indexes. A 1 here is the whole defect this test is for.
	gotInv, _ := view["inventory"].([]any)
	if len(gotInv) != 2 {
		t.Fatalf("the GET returned %d inventory line(s), want 2: %v", len(gotInv), view["inventory"])
	}
	second, _ := gotInv[1].(map[string]any)
	if got, _ := second["stock_item_id"].(string); got != f.itemID {
		t.Fatalf("inventory[1] moves item %v, want the ORDERED item %s — if the grid comes back "+
			"in a different order from the one it was keyed in, every line_no on the body points "+
			"at the wrong goods", second["stock_item_id"], f.itemID)
	}
	if got, _ := second["line_no"].(float64); got != 2 {
		t.Fatalf("inventory[1] line_no = %v, want 2", second["line_no"])
	}
	links, _ := view["fulfilments"].([]any)
	if len(links) != 1 {
		t.Fatalf("the GET returned %d fulfilment(s), want 1: %v", len(links), view["fulfilments"])
	}
	link, _ := links[0].(map[string]any)
	if got, _ := link["line_no"].(float64); got != 2 {
		t.Fatalf("the fulfilment's line_no = %v, want 2 — 1-based and STORED, indexing "+
			"`inventory` at line_no-1. On a one-line delivery note this assertion is true "+
			"however the number is produced, which is why the note has two lines", link["line_no"])
	}

	// ---- THE ROUND TRIP: `inventory` AND `fulfilments` both straight off the GET.
	req := rebuild(view, "voucher_no", "voucher_date", "party", "narration",
		"inventory", "fulfilments")
	req["narration"] = "Part delivery against the order (typo fixed)"
	arec, _ := doRaw(t, srv, http.MethodPut,
		f.base+"/vouchers/delivery-note/"+voucherID, f.ownerTok, req)
	if arec.Code != http.StatusOK && arec.Code != http.StatusCreated {
		t.Fatalf("altering the narration = %d, want 200 (%s)", arec.Code, arec.Body.String())
	}
	if out, done, _ := salesOrderBook(t, srv, f); out != "12.0000" || done != "8.0000" {
		t.Fatalf("after an alter rebuilt ENTIRELY from the GET the order reads %q/%q, want 12/8 — "+
			"a form that re-sends only what the response gave it re-opens the order", out, done)
	}

	// ---- ⛔ THE INVERSE CONTROL: the same body with the links dropped.
	omitted := rebuild(view, "voucher_no", "voucher_date", "party", "narration", "inventory")
	orec, _ := doRaw(t, srv, http.MethodPut,
		f.base+"/vouchers/delivery-note/"+voucherID, f.ownerTok, omitted)
	if orec.Code != http.StatusOK && orec.Code != http.StatusCreated {
		t.Fatalf("CONTROL: an alter omitting the links = %d, want 200 (%s)", orec.Code, orec.Body.String())
	}
	if out, done, _ := salesOrderBook(t, srv, f); out != "20.0000" || done != "0.0000" {
		t.Fatalf("CONTROL: an alter that OMITTED the links left the order at %q/%q, want 20/0. "+
			"If omitting them did not re-open the order, the leg above proves nothing", out, done)
	}

	// ---- ⛔⛔ AND THE CONTROL THAT MAKES `line_no` MEAN SOMETHING: SLIDE IT.
	//
	// Same grid, same link, `line_no` moved from 2 to 1 — the exact damage a
	// projection that hard-coded or re-derived the number would do to a form. Line
	// 1 moves CRATES, so the claim is "these eight crates discharge a commitment to
	// supply Tables", and migration 0042's trigger refuses it by name. Without this
	// leg, every assertion above is consistent with line_no being ignored entirely.
	slid := rebuild(view, "voucher_no", "voucher_date", "party", "narration", "inventory")
	slid["fulfilments"] = []any{map[string]any{
		"line_no": 1, "order_line_id": f.orderLineID, "qty": "8",
	}}
	srec, _ := doRaw(t, srv, http.MethodPut,
		f.base+"/vouchers/delivery-note/"+voucherID, f.ownerTok, slid)
	if srec.Code == http.StatusOK || srec.Code == http.StatusCreated {
		t.Fatalf("CONTROL: a link slid from line 2 to line 1 was ACCEPTED (%d). line_no would "+
			"then be decoration: delivering crates would discharge the order for the item the "+
			"customer actually bought", srec.Code)
	}
	if !strings.Contains(srec.Body.String(), "cannot discharge a commitment to supply a different one") {
		t.Fatalf("CONTROL: the slid link was refused with %d but not for the item mismatch, so "+
			"this leg may be passing for an unrelated reason: %s", srec.Code, srec.Body.String())
	}
	// And the refusal rolled back rather than half-applying: the order is still
	// re-opened by the PREVIOUS leg, not fulfilled by a link that was rejected.
	if out, done, _ := salesOrderBook(t, srv, f); out != "20.0000" || done != "0.0000" {
		t.Fatalf("CONTROL: after the REFUSED alter the order reads %q/%q, want the 20/0 the "+
			"previous leg left behind", out, done)
	}
}

// ---------------------------------------------------------------------------
// 5. ⛔⛔ `voucher_gst_line` — VERDICT: WAS LOSING DATA. It was never on §4.3's
//    list of five, and it is the worst of the class.
// ---------------------------------------------------------------------------

// TestReadBackAuditGSTLinesSurviveAnAlterRebuiltFromTheGet is the finding.
//
// ===========================================================================
// THE DEFECT, AND WHY THE ORIGINAL AUDIT COULD NOT HAVE SEEN IT
// ===========================================================================
// §4.3's audit says "grep the migrations for `voucher_version_id` and check each
// one off against voucherFullJSON", and it lists five tables. `voucher_gst_line`
// (migration 0016) is a SIXTH, keyed on voucher_version_id exactly like the
// others, written from `in.GST.Lines` inside post()'s transaction — and it was on
// neither the checked list nor the unchecked one. It was simply not seen.
//
// It is the worst member of the class because the missing field is an INPUT, not
// a record. `voucher_version` keeps taxable_value/cgst/sgst/igst — what the
// derivation PRODUCED. The RATE it was derived at, and the HSN it was stated
// under, lived only in these rows, and nothing projected them. So a form rebuilt
// from GET /vouchers/{id} had no rate to put back in its `gst` block:
//
//   - it sends no `gst` at all, and `gstActive` is false — the version's OWN
//     aggregate GST columns are written as zero, the tax postings survive as
//     ordinary credits to Output CGST/SGST, the invoice leaves the GSTR-1 b2b
//     section and the table-12 HSN summary, and nothing refuses it because a
//     supply with no tax is a legal state; or
//   - it sends `gst` with an EMPTY rate, which is what the shipped client does
//     (`formFromView` seeds `gstRatePct: ""`, gstDerive turns "" into 0) — the
//     server re-derives 0% tax, agrees with the client's zeroed tax postings, and
//     the PARTY LEG FALLS BY THE WHOLE TAX. An 11,800 invoice becomes 10,000.
//
// ⚠️ AN ITEM INVOICE IS NOT EXPOSED TO THIS and must not be used to test it: its
// lines are generated server-side from the stock-item master, so the rate is
// re-derived from something the round trip cannot lose. The voucher below is
// therefore an ACCOUNTING-MODE GST sale — taxable value plus a typed rate, no
// stock — which is the shape §1.5 keyed in a real browser.
//
// The fix is `gst_lines` on the GET (ledger.VoucherView.GSTLines + voucherFullJSON),
// and the leg-1 assertion below is what it buys.
func TestReadBackAuditGSTLinesSurviveAnAlterRebuiltFromTheGet(t *testing.T) {
	srv := newServer(t)
	f := newReadbackFixture(t, srv)

	// 10,000 at 18% intra-state (company state 29, place of supply 29) = 900 + 900.
	// The oracle is chosen so a wrong answer cannot look right: an inter-state
	// reading would post a single IGST 1,800, and a rate-less reading posts nothing
	// at all and bills 10,000.
	sale := func() map[string]any {
		return map[string]any{
			"voucher_date": "2026-06-15", "narration": "consultancy",
			"party": "Readback Buyer", "party_ledger_id": f.debtor,
			"created_by": f.subject,
			"postings": []any{
				map[string]any{"ledger_id": f.debtor, "dr_cr": "D", "amount": "11800.0000"},
				map[string]any{"ledger_id": f.sales, "dr_cr": "C", "amount": "10000.0000"},
				map[string]any{"ledger_id": f.outCGST, "dr_cr": "C", "amount": "900.0000"},
				map[string]any{"ledger_id": f.outSGST, "dr_cr": "C", "amount": "900.0000"},
			},
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"lines": []any{map[string]any{
					"taxable_value": "10000.0000", "gst_rate_pct": "18", "hsn": "998311",
				}},
				"cgst_ledger_id": f.outCGST,
				"sgst_ledger_id": f.outSGST,
				"igst_ledger_id": f.outIGST,
			},
		}
	}
	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/sales", f.ownerTok, sale())
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST an accounting-mode GST sale = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)
	if codes := f.hsnCodes(t, srv); len(codes) != 1 || codes[0] != "998311" {
		t.Fatalf("the GSTR-1 HSN summary reports %v, want exactly [998311]", codes)
	}

	// ---- ⛔ THE READ-BACK. This key did not exist.
	view := f.get(t, srv, voucherID)
	raw, present := view["gst_lines"]
	if !present {
		t.Fatalf("GET /vouchers/{id} returned NO `gst_lines` key. The RATE a voucher's tax was "+
			"derived at is stored only in voucher_gst_line — the aggregate columns record what "+
			"the derivation produced, and no rate is recoverable from them. Without this key a "+
			"form rebuilt from the response re-derives the tax at 0%%, and fixing a typo in the "+
			"narration takes an 11,800 invoice down to 10,000. Body: %v", view)
	}
	lines, _ := raw.([]any)
	if len(lines) != 1 {
		t.Fatalf("the sale read back with %d gst line(s), want 1: %v", len(lines), raw)
	}
	gl, _ := lines[0].(map[string]any)
	// ⛔ THE DISTINGUISHING INPUT. taxable_value alone is already on the body, so a
	// projection that carried only that would look like a fix and change nothing:
	// 10,000 at no rate is 10,000 at 0%%. The RATE is the field that tells a form
	// that re-states from one that quietly zeroes the tax.
	if got, _ := gl["gst_rate_pct"].(string); got != "18.0000" {
		t.Fatalf("the gst line's gst_rate_pct = %v, want \"18.0000\" — this is the ONE field "+
			"that distinguishes a body which can restate the tax from one which cannot", gl["gst_rate_pct"])
	}
	if got, _ := gl["hsn"].(string); got != "998311" {
		t.Fatalf("the gst line's hsn = %v, want \"998311\" — without it the restated invoice "+
			"leaves the GSTR-1 table-12 summary", gl["hsn"])
	}
	// ⚠️ FIELD NAMES MIRROR gstLineReq (taxable_value / gst_rate_pct / hsn) so the
	// objects can be lifted straight back into `gst.lines`. The extra keys the
	// projection adds are ignored by the decoder, which is what makes the echo below
	// a plain echo rather than a translation the test author had to remember.
	if got, _ := gl["taxable_value"].(string); got != "10000.0000" {
		t.Fatalf("the gst line's taxable_value = %v, want \"10000.0000\"", gl["taxable_value"])
	}

	// ---- THE ROUND TRIP: the `gst` block reassembled from nothing but the GET.
	req := rebuild(view, "voucher_no", "voucher_date", "party", "party_ledger_id",
		"narration", "postings")
	req["narration"] = "consultancy (typo fixed)"
	req["gst"] = map[string]any{
		"supplier_state_code":        "29",
		"place_of_supply_state_code": view["place_of_supply"],
		"reverse_charge":             view["reverse_charge"],
		"lines":                      raw,
		"cgst_ledger_id":             f.outCGST,
		"sgst_ledger_id":             f.outSGST,
		"igst_ledger_id":             f.outIGST,
	}
	if code, msg := f.put(t, srv, "sales", voucherID, req); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("altering the narration = %d, want 200 (%s). A 422 here means the server "+
			"re-derived a different tax from the rate it handed out, which would make the "+
			"projection a lie in a new way (%s)", code, msg, msg)
	}
	after := f.get(t, srv, voucherID)
	if got, _ := after["cgst"].(string); got != "900.0000" {
		t.Fatalf("after an alter rebuilt from the GET the voucher's CGST = %q, want 900.0000", got)
	}
	if got, _ := after["taxable_value"].(string); got != "10000.0000" {
		t.Fatalf("after an alter rebuilt from the GET the taxable value = %q, want 10000.0000", got)
	}
	if codes := f.hsnCodes(t, srv); len(codes) != 1 || codes[0] != "998311" {
		t.Fatalf("after an alter rebuilt from the GET the HSN summary reports %v, want [998311] — "+
			"the per-line detail did not survive the round trip", codes)
	}

	// ---- ⛔ THE INVERSE CONTROL, AND IT IS WHAT THE OLD BODY FORCED.
	//
	// The same alter WITHOUT a `gst` block — the only body a client could build from
	// a response that did not carry the rate. It must be ACCEPTED, and it must strip
	// the voucher of its tax. If it were refused, or if the tax survived, the leg
	// above would be inert and there would have been no defect to fix.
	omitted := rebuild(view, "voucher_no", "voucher_date", "party", "party_ledger_id",
		"narration", "postings")
	if code, msg := f.put(t, srv, "sales", voucherID, omitted); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("CONTROL: an alter omitting `gst` = %d, want 200 (%s)", code, msg)
	}
	stripped := f.get(t, srv, voucherID)
	if got, _ := stripped["cgst"].(string); got != "0.0000" {
		t.Fatalf("CONTROL: after an alter that OMITTED `gst` the voucher's CGST = %q, want "+
			"0.0000. If the tax survived an alter that did not state it, the round-trip leg "+
			"above proves nothing", got)
	}
	if codes := f.hsnCodes(t, srv); len(codes) != 0 {
		t.Fatalf("CONTROL: after an alter that OMITTED `gst` the HSN summary still reports %v, "+
			"want nothing — the per-line rows are supposed to be re-stated", codes)
	}
	// ⚠️ AND THE MONEY IS STILL ON THE BOOKS, WHICH IS THE WHOLE SHAPE OF THIS
	// DEFECT CLASS. The four postings are untouched: the debtor is still owed
	// 11,800, Output CGST still carries its 900. Nothing is out of balance, no
	// trigger fires, and a supply the company must report has silently left the
	// return.
	prows, _ := stripped["postings"].([]any)
	if len(prows) != 4 {
		t.Fatalf("the stripped voucher carries %d posting(s), want the 4 it had — if the money "+
			"moved too, the defect would be loud, and the point is that it is not", len(prows))
	}

	// ---- AND IT COMES BACK, so the zero above is attributable to the omission.
	if code, msg := f.put(t, srv, "sales", voucherID, req); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("restating the gst block = %d, want 200 (%s)", code, msg)
	}
	if codes := f.hsnCodes(t, srv); len(codes) != 1 || codes[0] != "998311" {
		t.Fatalf("after restating the gst block the HSN summary reports %v, want [998311]", codes)
	}
}
