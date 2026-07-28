package server_test

// handler_stock_reports_test.go — live-Postgres HTTP tests for the four
// inventory reports mounted alongside GET /stock-summary:
//
//	GET /stock-movement       GET /godown-summary
//	GET /batch-summary        GET /stock-item-vouchers
//
// WHAT THESE TESTS ARE FOR. All four report services already existed and
// compiled; what was missing was the THREE-touch wiring (route, routeAction
// mapping, authz vocabulary), and a report that is wired badly does not crash —
// it 403s an Owner, or it serves a tidy empty array. So the oracles here are
// chosen so that neither failure can pass:
//
//   - every report is asserted to return REAL ROWS with EXACT figures for a
//     seeded scenario. An empty 200 is indistinguishable from a broken query,
//     which is precisely why "it returned 200" is not an assertion.
//   - the figures are chosen so the RIGHT answer differs from the
//     plausible-but-wrong one: the issue is priced at the weighted-average COST
//     (100), never at the sale price (200), so a report wired to a SUM(qty*rate)
//     over the movement rows reports different numbers rather than the same ones.
//   - the untracked location and the batch-less position are seeded on purpose.
//     A `WHERE godown_id IS NOT NULL` (or an inner join for a name) makes that
//     stock vanish from the breakdown while remaining in the item total, so the
//     columns stop adding up to the row they are supposed to explain.
//   - the EXPIRED batch is seeded on purpose. Expiry is a classification, never
//     a filter: expired stock is still in the warehouse and still on the Balance
//     Sheet at cost, and a report that filtered it out would understate the
//     assets of the business.

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/server"
)

// stockDepthScenario is the seeded books every figure below is asserted
// against, plus the ids a request needs.
//
// THE POSITIONS IT PRODUCES, as at 2025-04-30:
//
//	Widget @ Main Location / LOT-OLD (expired)   5 @ 100 =  500
//	Widget @ Cold Store    / LOT-NEW            10 @ 120 = 1200
//	Widget @ (no location) / (no batch)          4 @  50 =  200
//	                                           total       1900
type stockDepthScenario struct {
	stockFixture
	coldStoreID string
	// lotOld expires the day BEFORE the as-on date every assertion here uses;
	// lotNew is eight months out. Batches are addressed by id on the wire (a
	// batch name is item-scoped, not company-scoped, so it is not an address).
	lotOldID string
	lotNewID string
}

// newStockDepthScenario posts the movements above. It deliberately uses THREE
// different (godown, batch) buckets of ONE item, because every fold in these
// reports — by location, by batch, by item — is only tested by a company whose
// stock sits in more than one place.
func newStockDepthScenario(t *testing.T, srv *server.Server) stockDepthScenario {
	t.Helper()
	sc := stockDepthScenario{stockFixture: newStockFixture(t, srv)}

	rec, body := do(t, srv, http.MethodPost, sc.base+"/godowns",
		map[string]any{"name": "Cold Store", "created_by": "inv"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create godown = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	sc.coldStoreID, _ = body["godown_id"].(string)

	// LOT-OLD expired the day before the as-on date used throughout; LOT-NEW is
	// eight months out. One of each is what makes the ageing bands assertable.
	for _, b := range []struct {
		name, expiry string
		into         *string
	}{
		{"LOT-OLD", "2025-03-31", &sc.lotOldID},
		{"LOT-NEW", "2025-12-31", &sc.lotNewID},
	} {
		rec, body := do(t, srv, http.MethodPost, sc.base+"/batches", map[string]any{
			"stock_item_name": "Widget", "name": b.name,
			"expiry_date": b.expiry, "created_by": "inv",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create batch %s = %d, want 201 (%s)", b.name, rec.Code, rec.Body.String())
		}
		*b.into, _ = body["batch_id"].(string)
	}

	sc.purchaseInto(t, srv, "PUR-OLD", "2025-04-01", sc.godownID, sc.lotOldID, "10", "100", "1000")
	sc.purchaseInto(t, srv, "PUR-NEW", "2025-04-05", sc.coldStoreID, sc.lotNewID, "10", "120", "1200")
	// The UNTRACKED bucket: no location, no batch. A company that never enables
	// multi-location posts every line this way, so this is an ordinary position
	// and not an edge case.
	sc.purchaseInto(t, srv, "PUR-ANY", "2025-04-06", "", "", "4", "50", "200")

	// Sell 5 of LOT-OLD at a SALE price of 200. Those five COST 100 each — that
	// bucket holds only the 100 lot — so the cost consumed is 500 and not 1000.
	rec, _ = do(t, srv, http.MethodPost, sc.base+"/vouchers/sales", map[string]any{
		"voucher_no": "INV-1", "voucher_date": "2025-04-10", "created_by": "inv",
		"narration": "five widgets out of the old lot",
		"postings": []map[string]any{
			{"ledger_id": sc.debtorID, "dr_cr": "D", "amount": "1000"},
			{"ledger_id": sc.salesID, "dr_cr": "C", "amount": "1000"},
		},
		"inventory": []map[string]any{
			{"stock_item_id": sc.itemID, "godown_id": sc.godownID,
				"batch_id": sc.lotOldID, "direction": "OUT",
				"qty": "5", "rate": "200", "amount": "1000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("sale = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	return sc
}

// purchaseInto posts an inward into one (godown, batch) bucket, debiting
// Purchases and crediting the supplier so the books stay balanced while the
// stock moves. An empty godown/batch means the untracked dimension.
func (sc stockDepthScenario) purchaseInto(t *testing.T, srv *server.Server, no, date, godownID, batchID, qty, rate, amount string) {
	t.Helper()
	// Both dimensions go out as "" when untracked, which is what the wire means
	// by "this line names no location / no batch".
	line := map[string]any{
		"stock_item_id": sc.itemID,
		"godown_id":     godownID,
		"batch_id":      batchID,
		"direction":     "IN",
		"qty":           qty, "rate": rate, "amount": amount,
	}
	rec, _ := do(t, srv, http.MethodPost, sc.base+"/vouchers/purchase", map[string]any{
		"voucher_no": no, "voucher_date": date, "created_by": "inv",
		"postings": []map[string]any{
			{"ledger_id": sc.purchaseID, "dr_cr": "D", "amount": amount},
			{"ledger_id": sc.creditorID, "dr_cr": "C", "amount": amount},
		},
		"inventory": []map[string]any{line},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("purchase %s = %d, want 201 (%s)", no, rec.Code, rec.Body.String())
	}
}

// findRow returns the first row whose field equals want, or fails naming what
// was actually there — an assertion on rows[0] would otherwise turn a changed
// sort order into a confusing figure mismatch.
func findRow(t *testing.T, rows []map[string]any, field, want, what string) map[string]any {
	t.Helper()
	for _, r := range rows {
		if r[field] == want {
			return r
		}
	}
	t.Fatalf("%s: no row with %s = %q in %v", what, field, want, rows)
	return nil
}

// checkFields asserts a set of (field, want, why) triples on one row.
func checkFields(t *testing.T, row map[string]any, what string, want []struct{ field, want, why string }) {
	t.Helper()
	for _, tc := range want {
		if row[tc.field] != tc.want {
			t.Errorf("%s: %s = %v, want %s (%s)", what, tc.field, row[tc.field], tc.want, tc.why)
		}
	}
}

// TestGodownSummaryServesValuedColumnsOverHTTP is the location breakdown, end
// to end. The two things it exists to catch: the columns must FOOT to the item
// row (which they only do if the untracked location is a column like any
// other), and each location must be valued at ITS OWN average cost.
func TestGodownSummaryServesValuedColumnsOverHTTP(t *testing.T) {
	srv := newServer(t)
	sc := newStockDepthScenario(t, srv)

	rec, body := do(t, srv, http.MethodGet, sc.base+"/godown-summary?as_on=2025-04-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /godown-summary = %d, want 200 (%s) — a 403 here is a missing routeAction "+
			"mapping, not an authorization decision", rec.Code, rec.Body.String())
	}
	if body["as_on"] != "2025-04-30" {
		t.Errorf("godown summary as_on = %v, want the date asked for", body["as_on"])
	}
	if body["total_value"] != "1900.0000" {
		t.Errorf("godown summary total_value = %v, want 1900.0000 — it must equal the Stock "+
			"Summary's total for the same date, or two screens disagree about how much stock "+
			"the company owns", body["total_value"])
	}

	items := list(t, body, "items")
	if len(items) != 1 {
		t.Fatalf("godown summary has %d item rows, want 1 (Widget): %v — an empty or short list "+
			"is what a broken query looks like through a 200", len(items), items)
	}
	checkFields(t, items[0], "Widget row", []struct{ field, want, why string }{
		{"item", "Widget", "the row is the item"},
		{"closing_qty", "19.0000", "10 + 10 + 4 bought less 5 issued, ACROSS all three locations"},
		{"closing_value", "1900.0000", "500 + 1200 + 200: each bucket at its own average cost"},
		{"inward_value", "2400.0000", "what the goods cost to buy"},
		{"outward_value", "500.0000", "COST of the 5 issued (5 x 100), NOT the 1000 they sold for"},
	})
	if _, present := items[0]["closing_rate"]; present {
		t.Errorf("the item row carries a closing_rate: a row spans locations and the average of "+
			"their averages is not an average (%v)", items[0])
	}

	cols := list(t, items[0], "columns")
	if len(cols) != 3 {
		t.Fatalf("Widget has %d godown columns, want 3 (Cold Store, Main Location, untracked): %v — "+
			"a missing third column means the no-location stock was filtered out of the "+
			"breakdown while still counting in the row total", len(cols), cols)
	}
	// The untracked column prints LAST: it is the only column with no header,
	// and a nameless FIRST column reads as a rendering fault.
	if cols[0]["godown"] != "Cold Store" || cols[1]["godown"] != "Main Location" || cols[2]["untracked"] != true {
		t.Errorf("column order = %v/%v/untracked=%v, want Cold Store, Main Location, then the "+
			"untracked column last", cols[0]["godown"], cols[1]["godown"], cols[2]["untracked"])
	}
	checkFields(t, cols[0], "Cold Store column", []struct{ field, want, why string }{
		{"closing_qty", "10.0000", "nothing was issued from Cold Store"},
		{"closing_value", "1200.0000", "10 at the 120 Cold Store itself paid — never a blend with Main Location's 100"},
	})
	checkFields(t, cols[1], "Main Location column", []struct{ field, want, why string }{
		{"closing_qty", "5.0000", "10 in, 5 out"},
		{"closing_value", "500.0000", "5 at Main Location's own average of 100"},
		{"outward_value", "500.0000", "cost consumed, not the 1000 of revenue"},
	})
	checkFields(t, cols[2], "untracked column", []struct{ field, want, why string }{
		{"godown_id", "", "an untracked dimension is empty, never the all-zero uuid"},
		{"closing_qty", "4.0000", "stock posted with no location is still stock"},
		{"closing_value", "200.0000", "and it is still worth what was paid for it"},
	})

	// The footers are the other half of the same statement: what each location
	// is worth, value only (a quantity across items would add kilograms to litres).
	footers := list(t, body, "godowns")
	if len(footers) != 3 {
		t.Fatalf("godown summary has %d column footers, want 3: %v", len(footers), footers)
	}
	cold := findRow(t, footers, "godown", "Cold Store", "column footers")
	if cold["closing_value"] != "1200.0000" {
		t.Errorf("Cold Store footer closing_value = %v, want 1200.0000", cold["closing_value"])
	}
	if _, present := cold["closing_qty"]; present {
		t.Errorf("a column footer carries a quantity (%v): summing kilograms of one item and "+
			"litres of another produces a number with no unit", cold)
	}
}

// TestBatchSummaryBandsExpiredStockWithoutHidingIt is the ageing report end to
// end. The load-bearing claim is the one a `WHERE expiry_date >= as_on`
// destroys in a single line: the EXPIRED batch is still counted in every total
// and is merely flagged.
func TestBatchSummaryBandsExpiredStockWithoutHidingIt(t *testing.T) {
	srv := newServer(t)
	sc := newStockDepthScenario(t, srv)

	rec, body := do(t, srv, http.MethodGet, sc.base+"/batch-summary?as_on=2025-04-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /batch-summary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["total_value"] != "1900.0000" {
		t.Errorf("batch summary total_value = %v, want 1900.0000 — the expired batch is INSIDE "+
			"this identity: it is still in the warehouse and still on the Balance Sheet at cost "+
			"until somebody writes it off with a voucher", body["total_value"])
	}
	if body["expired_value"] != "500.0000" {
		t.Errorf("expired_value = %v, want 500.0000 — the money sitting in stock that can no "+
			"longer be sold is the headline this report exists to print", body["expired_value"])
	}

	rows := list(t, body, "rows")
	if len(rows) != 3 {
		t.Fatalf("batch summary has %d rows, want 3 (LOT-OLD, LOT-NEW, the batch-less position): "+
			"%v", len(rows), rows)
	}
	// Soonest deadline first, so expired stock sits at the TOP of the report —
	// the only place it can do its job.
	checkFields(t, rows[0], "the first row", []struct{ field, want, why string }{
		{"batch", "LOT-OLD", "expired stock prints first"},
		{"bucket", "EXPIRED", "2025-03-31 is behind the as-on date"},
		{"expiry_date", "2025-03-31", "the date the band was derived from"},
		{"closing_qty", "5.0000", "the expired quantity is REPORTED, not netted off"},
		{"closing_value", "500.0000", "and it is still carried at cost"},
		{"closing_rate", "100.0000", "a batch row is a single position, so a rate IS meaningful here"},
	})
	if rows[0]["expired"] != true {
		t.Errorf("the LOT-OLD row's expired flag = %v, want true — the flag is what a UI colours "+
			"red, and it is the whole point of the report", rows[0]["expired"])
	}
	if days, _ := rows[0]["days_to_expiry"].(float64); days != -30 {
		t.Errorf("LOT-OLD days_to_expiry = %v, want -30 (signed: negative once the date has "+
			"passed)", rows[0]["days_to_expiry"])
	}
	noBatch := findRow(t, rows, "bucket", "NO_EXPIRY", "batch rows")
	if noBatch["batch_id"] != "" || noBatch["closing_value"] != "200.0000" {
		t.Errorf("the batch-less position = %v, want an empty batch_id carrying 200.0000 — most "+
			"stock simply does not perish, and it still has to foot to the total", noBatch)
	}

	// ALL FIVE bands, empty ones included: a band that vanished when empty would
	// make "no expired stock" indistinguishable from "the expired band was
	// dropped somewhere".
	bands := list(t, body, "bands")
	wantBands := []struct {
		bucket, value string
		count         float64
	}{
		{"EXPIRED", "500.0000", 1},
		{"DUE_IN_30_DAYS", "0.0000", 0},
		{"DUE_IN_90_DAYS", "0.0000", 0},
		{"BEYOND_90_DAYS", "1200.0000", 1},
		{"NO_EXPIRY", "200.0000", 1},
	}
	if len(bands) != len(wantBands) {
		t.Fatalf("batch summary has %d bands, want 5 in nearest-deadline-first order: %v", len(bands), bands)
	}
	for i, wb := range wantBands {
		if bands[i]["bucket"] != wb.bucket || bands[i]["closing_value"] != wb.value ||
			bands[i]["row_count"] != wb.count {
			t.Errorf("band %d = %v, want %s worth %s over %v rows — the bands must sum to "+
				"total_value with every position in exactly one of them",
				i, bands[i], wb.bucket, wb.value, wb.count)
		}
	}

	// An ageing report with no as-on date is a 400: a band is defined RELATIVE
	// to a date, and defaulting to the server's clock would make the same books
	// print differently on two days with no column saying why.
	if rec, b := do(t, srv, http.MethodGet, sc.base+"/batch-summary", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("GET /batch-summary with no as_on = %d %v, want 400", rec.Code, b)
	}
}

// TestStockMovementReportsThePeriodNotThePosition pins the report's ARITY. It
// takes two dates and answers what MOVED; wiring it to the position report
// would serve confident, plausible, wrong numbers, so the assertion that
// matters is the narrowed window — which contains the sale and neither purchase
// that preceded it.
func TestStockMovementReportsThePeriodNotThePosition(t *testing.T) {
	srv := newServer(t)
	sc := newStockDepthScenario(t, srv)

	rec, body := do(t, srv, http.MethodGet, sc.base+"/stock-movement?from=2025-04-01&to=2025-04-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-movement = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["from"] != "2025-04-01" || body["to"] != "2025-04-30" {
		t.Errorf("stock movement echoed from/to = %v/%v, want the window asked for — a client "+
			"must not be able to render a period it did not get", body["from"], body["to"])
	}
	rows := list(t, body, "rows")
	if len(rows) != 3 {
		t.Fatalf("stock movement over the whole month has %d rows, want 3 (one per bucket that "+
			"moved): %v", len(rows), rows)
	}
	items := list(t, body, "items")
	if len(items) != 1 {
		t.Fatalf("stock movement has %d item rollups, want 1: %v", len(items), items)
	}
	checkFields(t, items[0], "Widget movement", []struct{ field, want, why string }{
		{"inward_qty", "24.0000", "10 + 10 + 4 received in the window"},
		{"inward_value", "2400.0000", "what they cost"},
		{"outward_qty", "5.0000", "the one issue"},
		{"outward_value", "500.0000", "COST consumed (5 x 100), not the 1000 of revenue — booking " +
			"the sale price as the cost is what makes gross profit come out as zero"},
		{"net_value", "1900.0000", "the change in carried value, which reconciles two Stock " +
			"Summaries taken at the two ends of the window"},
	})
	if _, present := items[0]["closing_qty"]; present {
		t.Errorf("the movement register carries a closing column (%v): a period reports what "+
			"MOVED, and reporting a position here would ignore every unit bought before `from`",
			items[0])
	}

	// THE ARITY ASSERTION. A window opening after both purchases contains only
	// the issue. A handler wired to the position report would answer with the
	// same 24 inward as above.
	rec, body = do(t, srv, http.MethodGet, sc.base+"/stock-movement?from=2025-04-07&to=2025-04-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-movement (narrow window) = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	rows = list(t, body, "rows")
	if len(rows) != 1 {
		t.Fatalf("stock movement for 07-30 April has %d rows, want exactly 1 (only the sale "+
			"happened): %v — more rows means the window was ignored", len(rows), rows)
	}
	checkFields(t, rows[0], "the sale-only window", []struct{ field, want, why string }{
		{"batch", "LOT-OLD", "the bucket the goods left"},
		{"inward_qty", "0.0000", "nothing was received after the 6th"},
		{"outward_qty", "5.0000", "the issue is inside the window"},
		{"outward_value", "500.0000", "priced off the running average set by purchases OUTSIDE " +
			"the window — an issue of goods bought last quarter must not be consumed at zero"},
		{"net_value", "-500.0000", "value left the position"},
	})
}

// TestStockItemVouchersDrillsToTheLinesBehindTheTotals is the drill: no report
// is a dead end. It lists the CURRENT version of every voucher that moved the
// item, which is what makes the register reconcile to the summary that links
// into it.
func TestStockItemVouchersDrillsToTheLinesBehindTheTotals(t *testing.T) {
	srv := newServer(t)
	sc := newStockDepthScenario(t, srv)

	rec, body := do(t, srv, http.MethodGet,
		sc.base+"/stock-item-vouchers?stock_item_id="+sc.itemID+"&from=2025-04-01&to=2025-04-30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-item-vouchers = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["item"] != "Widget" || body["unit"] != "Nos" {
		t.Errorf("drill header = %v/%v, want Widget in Nos", body["item"], body["unit"])
	}
	if body["inward_qty"] != "24.0000" || body["outward_qty"] != "5.0000" {
		t.Errorf("drill totals = %v in / %v out, want 24.0000 / 5.0000 — the register's own "+
			"totals must agree with the movement report over the same window",
			body["inward_qty"], body["outward_qty"])
	}

	lines := list(t, body, "lines")
	if len(lines) != 4 {
		t.Fatalf("the drill lists %d lines, want 4 (three purchases and the sale): %v — an empty "+
			"or short list through a 200 is what a broken current-version join looks like",
			len(lines), lines)
	}
	// Ordered by voucher date, so the register reads as a chronology.
	if lines[0]["voucher_no"] != "PUR-OLD" || lines[3]["voucher_no"] != "INV-1" {
		t.Errorf("drill order = %v ... %v, want PUR-OLD first and INV-1 last",
			lines[0]["voucher_no"], lines[3]["voucher_no"])
	}
	checkFields(t, lines[3], "the sale line", []struct{ field, want, why string }{
		{"voucher_type", "Sales", "the register names the document"},
		{"date", "2025-04-10", "the voucher date, which is what the period filtered on"},
		{"direction", "OUT", "qty is always positive and the direction carries the sign"},
		{"qty", "5.0000", "as entered"},
		{"rate", "200.0000", "THE SALE PRICE AS ENTERED — this column is an input to valuation, " +
			"never a valuation: the COST of these five (500) exists only at the position level"},
		{"amount", "1000.0000", "as entered, never recomputed"},
		{"batch", "LOT-OLD", "the lot the goods left"},
		{"godown", "Main Location", "the place they left from"},
		{"narration", "five widgets out of the old lot", "a register is read by a person"},
	})
	if _, present := body["outward_value"]; present {
		t.Errorf("the drill carries a value total (%v): totalling an as-entered amount column "+
			"and calling it cost of goods sold is the bug this report must not invite", body)
	}

	// The untracked line came in with no location and no batch, and it is listed
	// like any other — dropping it would leave the drill short of the totals
	// above it.
	untracked := findRow(t, lines, "voucher_no", "PUR-ANY", "drill lines")
	if untracked["godown_id"] != "" || untracked["batch_id"] != "" {
		t.Errorf("the untracked line = %v, want empty godown_id and batch_id", untracked)
	}

	// A drill with no subject is a malformed request, not an empty register:
	// answering it with every movement in the company would be a tidy, plausible
	// answer to a different question.
	if rec, b := do(t, srv, http.MethodGet, sc.base+"/stock-item-vouchers", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("GET /stock-item-vouchers with no stock_item_id = %d %v, want 400", rec.Code, b)
	}
	if rec, b := do(t, srv, http.MethodGet, sc.base+"/stock-item-vouchers?stock_item_id=not-a-uuid", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("GET /stock-item-vouchers with a malformed id = %d %v, want 400", rec.Code, b)
	}
	// An item that is not in THESE books is an error, not an empty register: RLS
	// makes another tenant's item invisible, so the two must not be conflated or
	// a caller could probe another tenant's ids by the shape of the reply.
	if rec, b := do(t, srv, http.MethodGet,
		sc.base+"/stock-item-vouchers?stock_item_id="+uuid.NewString(), nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("GET /stock-item-vouchers for an unknown item = %d %v, want 422", rec.Code, b)
	}
}

// newStockReportRequests is the four mounted reports with a request that is
// well-formed for each (batch-summary REQUIRES as_on; the drill REQUIRES an
// item), so an auth assertion cannot be satisfied by a 400.
func newStockReportRequests(itemID string) []struct{ name, query string } {
	return []struct{ name, query string }{
		{"stock-movement", "?from=2025-04-01&to=2025-04-30"},
		{"godown-summary", "?as_on=2025-04-30"},
		{"batch-summary", "?as_on=2025-04-30"},
		{"stock-item-vouchers", "?stock_item_id=" + itemID},
	}
}

// TestStockReportsRouteAuthMatrix pins all four endpoints in the 401/403/200
// contract, including the half that is easiest to get wrong: they are DISPLAYs,
// so they must not inherit the Owner level's WebAuthn step-up. Mapping one to a
// write action would compile, serve, and demand a second factor to look at a
// warehouse.
func TestStockReportsRouteAuthMatrix(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, ownerSubj := newAdminCompany(t, srv)
	base := "/v1/companies/" + companyID

	rec, itemBody := doRaw(t, srv, http.MethodPost, base+"/stock-items", ownerTok,
		map[string]any{"name": "Bolt", "unit_symbol": "Nos", "created_by": "owner"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("owner creates stock item = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	itemID, _ := itemBody["stock_item_id"].(string)

	otherCompanyID, otherTok, _ := newAdminCompany(t, srv)
	if otherCompanyID == companyID {
		t.Fatal("the two fixture companies collided")
	}
	// Same Owner, same rights, token WITHOUT a WebAuthn AMR.
	weak := mintToken(t, srv, ownerSubj, "pwd")

	for _, rep := range newStockReportRequests(itemID) {
		path := base + "/" + rep.name + rep.query

		// No token at all: 401, RESERVED for missing/invalid credentials.
		if rec, _ := doRaw(t, srv, http.MethodGet, path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET /%s with no token = %d, want 401", rep.name, rec.Code)
		}
		// A perfectly valid token belonging to ANOTHER tenant's Owner: 403, not
		// 401 — the caller is authenticated, they are simply not a member here.
		if rec, _ := doRaw(t, srv, http.MethodGet, path, otherTok, nil); rec.Code != http.StatusForbidden {
			t.Errorf("GET /%s as another tenant's Owner = %d, want 403 — this report would be "+
				"serving one company's cost book to another", rep.name, rec.Code)
		}
		// The Owner of this company: 200. A 403 HERE is a missing routeAction
		// mapping (unmapped {companyID} routes default-deny before any handler
		// runs), not an authorization decision.
		if rec, b := doRaw(t, srv, http.MethodGet, path, ownerTok, nil); rec.Code != http.StatusOK {
			t.Errorf("GET /%s as Owner = %d %v, want 200 — an unmapped route default-denies, so "+
				"this is a routeAction gap rather than an authorization one", rep.name, rec.Code, b)
		}
		// THE LOAD-BEARING CASE. A read that answered STEPUP_REQUIRED here would
		// mean the route was mapped to a write action — and it would still
		// "work" for anyone who happened to have stepped up, so nothing else
		// catches it.
		rec, body := doRaw(t, srv, http.MethodGet, path, weak, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET /%s without a WebAuthn AMR = %d %v, want 200 — reading a stock report "+
				"is a Display and must not inherit the write step-up", rep.name, rec.Code, body)
		}
		if body["code"] == "STEPUP_REQUIRED" {
			t.Errorf("GET /%s demanded a WebAuthn step-up: the route is mapped to a WRITE action", rep.name)
		}
	}
}

// TestDataEntryIsDeniedEveryValuedStockReport is the authz decision of this
// slice, asserted through the real PDP.
//
// Each of these four reports prints COST: a per-location valuation, a per-batch
// one (with a rate per lot), cost of goods sold for a period, and — on the
// drill — the rate each purchase was made at, beside the supplier's name. The
// Data Entry level carries the COARSE grant `allow display report` from
// migration 0011, and the PDP matches a bare `report` rule against any
// report:NAME — so mounting these reports handed the cost book to every
// operator in every tenant BY INHERITANCE, with nobody deciding to. Migration
// 0028 seeds one `disallow display report:NAME` per report; report:NAME scores
// specificity 2 against the bare rule's 1, so the disallow wins.
//
// MUTATION THAT MUST MAKE THIS FAIL: remove a name from the ARRAY[...] in
// migration 0028's seed function (and re-apply it). The 403 for that report
// becomes a 200 — which is what proves the DISALLOW is what denies it here, and
// not membership, a missing route, or a broken handler.
func TestDataEntryIsDeniedEveryValuedStockReport(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	base := "/v1/companies/" + companyID

	rec, itemBody := doRaw(t, srv, http.MethodPost, base+"/stock-items", ownerTok,
		map[string]any{"name": "Bolt", "unit_symbol": "Nos", "created_by": "owner"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("owner creates stock item = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	itemID, _ := itemBody["stock_item_id"].(string)

	de := inventoryMemberToken(t, srv, base, ownerTok, "Data Entry")

	for _, rep := range newStockReportRequests(itemID) {
		path := base + "/" + rep.name + rep.query
		// The SAME request as an Owner is a 200 (TestStockReportsRouteAuthMatrix
		// pins that), so a 403 here can only be the level's own rule.
		if rec, b := doRaw(t, srv, http.MethodGet, path, de, nil); rec.Code != http.StatusForbidden {
			t.Errorf("as Data Entry, GET /%s = %d %v, want 403 — this report prints what the "+
				"business PAID for its goods, and an operator keying invoices has no need of "+
				"it (migration 0028 seeds `disallow display report:%s`)",
				rep.name, rec.Code, b, rep.name)
		}
	}

	// The width of what an operator DOES keep, restated here because a denial
	// test that only checks 403s passes just as well when the level has been
	// broken outright and the operator can no longer open the product.
	for _, tc := range []struct{ what, path string }{
		{"pick an item", base + "/stock-items"},
		{"pick a location", base + "/godowns"},
		{"read the Day Book they keyed", base + "/day-book"},
	} {
		if rec, b := doRaw(t, srv, http.MethodGet, tc.path, de, nil); rec.Code != http.StatusOK {
			t.Errorf("as Data Entry, %s (GET %s) = %d %v, want 200 — the disallows must be "+
				"NARROW: named per report, never a blanket denial of the reporting surface",
				tc.what, tc.path, rec.Code, b)
		}
	}
}
