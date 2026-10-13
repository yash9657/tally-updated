package server_test

// bill_allocation_http_test.go — CAN A CLIENT ACTUALLY RAISE A BILL, SETTLE IT,
// AND SEE IT AGE?
//
// ⛔⛔ THIS IS THE REACHABILITY PROOF (§12 item 4) FOR A FEATURE THAT WAS
// UNREACHABLE FOR ITS ENTIRE LIFE. `ledger.go` has always written
// `bill_allocation` from `in.BillAllocations` — the CLIENT supplies them on every
// path — and the only thing in the repository that ever built one was
// `frontend/scripts/seed-demo.mts`. So every receivable keyed in a browser sat in
// the Balance Sheet's Sundry Debtors and was ABSENT from the Outstandings report:
// money that never ages, never reaches a statement, and that nobody can chase.
// Every gate stayed green throughout, because the demo's references were
// hand-written by the seed script — the exact "reasoning from what you see on
// screen" hazard.
//
// It follows order_fulfilment_http_test.go and ewaybill_http_test.go: every step
// is a real HTTP request, the assertions read the figures back out through the
// OUTERMOST surface, and NO SQL IN THE FIXTURE REACHES AROUND THE API.
//
// THE EXACT REQUESTS IT PINS, named because §12 item 4 asks for them:
//
//	POST /v1/companies/{id}/vouchers/sales    (bill_allocations, ref_name "")
//	POST /v1/companies/{id}/vouchers/receipt  (bill_allocations, ref_type AGAINST)
//	GET  /v1/companies/{id}/outstandings?kind=RECEIVABLE
//	GET  /v1/companies/{id}/balance-sheet
//	GET  /v1/companies/{id}/vouchers/{voucherID}
//	PUT  /v1/companies/{id}/vouchers/sales/{voucherID}
//
// ===========================================================================
// §12 ITEM 4a — THE READ-BACK CHECK, AND HERE IT IS THE WHOLE POINT
// ===========================================================================
// `bill_allocation` IS keyed on voucher_version_id, so it is squarely in the
// class that four other tables were caught in: AN ALTER RE-STATES, and whatever
// the body omits is GONE. `voucherFullJSON` has always RETURNED these rows — the
// read-back audit checked exactly that and ticked them off — but until now no
// client sent them back, so the ordinary round trip (open an invoice, fix the
// narration, save) DELETED the bill reference and dropped the invoice out of
// Outstandings while leaving it in Sundry Debtors.
//
// TestAnAlterRebuiltFromTheGetKeepsTheBill below is the item-4a pairing, WITH the
// control that an alter omitting the references destroys them (§9.3).
//
// ===========================================================================
// ⛔⛔ THESE THREE TESTS ONCE ASSERTED `ref_name == voucher_no`, AND THAT WAS D11
// ===========================================================================
// A voucher number is allocated per (company, VOUCHER TYPE, financial year), so
// the bare "1" names one voucher per type per year and not one voucher: a Receipt
// 1's on-account amount netted against Sales invoice 1 on the same party, and both
// figures were wrong while the report still footed. The server now names an
// unnamed opener "<class>/<financial year>/<number>" — see ledger.go's
// stampBillRefNames — so an assertion that the report shows the BARE number was
// pinning the defect rather than the feature.
//
// The arc these tests describe is untouched, because the arc was never wrong: a
// body a browser can build reaches the ageing report, a receipt settles it, and a
// form rebuilt from the GET keeps it. What changed is that the NAME IS READ BACK
// — off the report, or off the GET — instead of being reconstructed from the
// voucher number, which is exactly what a real client has to do and what
// billAllocation.ts's header now tells clients to do.

import (
	"github.com/khata/cored/internal/reporting"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/server"
)

type billFixture struct {
	base     string
	ownerTok string
	subject  string

	partyID string // maintains bill-by-bill
	cashID  string // a debtor that does NOT — the control
	salesID string
	bankID  string
}

func newBillFixture(t *testing.T, srv *server.Server) billFixture {
	t.Helper()
	subject := "bill-owner-" + uuid.NewString()[:8]
	ownerTok := mintToken(t, srv, subject, "webauthn")
	cid := uuid.New()
	companyID := cid.String()

	rec, _ := doRaw(t, srv, http.MethodPost, "/v1/companies", ownerTok, map[string]any{
		"id": companyID, "name": "Bill Co " + companyID[:8],
		"fy_start_date": "2026-04-01", "base_currency": "INR", "state_code": "29",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	testdb.Cleanup(t, cid)

	f := billFixture{base: "/v1/companies/" + companyID, ownerTok: ownerTok, subject: subject}
	mk := func(name, group string, billwise bool) string {
		t.Helper()
		lrec, lb := doRaw(t, srv, http.MethodPost, f.base+"/ledgers", ownerTok, map[string]any{
			"name": name, "group_name": group, "created_by": subject,
			"maintain_bill_by_bill": billwise,
		})
		if lrec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d (%s)", name, lrec.Code, lrec.Body.String())
		}
		id, _ := lb["ledger_id"].(string)
		return id
	}
	f.partyID = mk("Acme Buyer", "Sundry Debtors", true)
	f.cashID = mk("Walk-in Counter", "Sundry Debtors", false)
	f.salesID = mk("Sales A/c", "Sales Accounts", false)
	f.bankID = mk("HDFC Bank", "Bank Accounts", false)
	return f
}

// postSale raises a credit sale of `amount` to `party`, opening a bill reference
// the CLIENT LEAVES UNNAMED — which is the only thing a browser can construct,
// because the voucher number is minted inside the post transaction.
func (f billFixture) postSale(t *testing.T, srv *server.Server, party, amount, due string) (string, string) {
	t.Helper()
	body := map[string]any{
		"voucher_date": "2026-06-19", "party": "buyer", "party_ledger_id": party,
		"created_by": f.subject,
		"postings": []any{
			map[string]any{"ledger_id": party, "dr_cr": "D", "amount": amount},
			map[string]any{"ledger_id": f.salesID, "dr_cr": "C", "amount": amount},
		},
		"bill_allocations": []any{
			map[string]any{
				"ledger_id": party, "ref_name": "", "ref_type": "NEW",
				"dr_cr": "D", "amount": amount, "due_date": due,
			},
		},
	}
	rec, b := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/sales", f.ownerTok, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	id, _ := b["voucher_id"].(string)
	no, _ := b["voucher_no"].(string)
	return id, no
}

// postSaleOn is postSale with the VOUCHER DATE stated.
//
// ⚠️ A separate helper rather than a fifth parameter on `postSale`: that one is used
// by three tests that do not care about the date, and widening a shared harness to
// serve one caller is how it accumulates knobs nobody reads (the same reason
// `setupCompanyBooksFrom` exists beside `setupCompany` in internal/query).
func (f billFixture) postSaleOn(t *testing.T, srv *server.Server, party, amount, due, on string) (string, string) {
	t.Helper()
	body := map[string]any{
		"voucher_date": on, "party": "buyer", "party_ledger_id": party,
		"created_by": f.subject,
		"postings": []any{
			map[string]any{"ledger_id": party, "dr_cr": "D", "amount": amount},
			map[string]any{"ledger_id": f.salesID, "dr_cr": "C", "amount": amount},
		},
		"bill_allocations": []any{
			map[string]any{
				"ledger_id": party, "ref_name": "", "ref_type": "NEW",
				"dr_cr": "D", "amount": amount, "due_date": due,
			},
		},
	}
	rec, b := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/sales", f.ownerTok, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("post sale dated %s = %d, want 201 (%s)", on, rec.Code, rec.Body.String())
	}
	id, _ := b["voucher_id"].(string)
	no, _ := b["voucher_no"].(string)
	return id, no
}

// outstandingBills returns the bills the ageing report shows for one party.
func (f billFixture) outstandingBills(t *testing.T, srv *server.Server, ledgerID string) []map[string]any {
	t.Helper()
	rec, b := doRaw(t, srv, http.MethodGet,
		f.base+"/outstandings?kind=RECEIVABLE", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("outstandings = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	parties, _ := b["parties"].([]any)
	for _, p := range parties {
		pm, _ := p.(map[string]any)
		if id, _ := pm["ledger_id"].(string); id != ledgerID {
			continue
		}
		raw, _ := pm["bills"].([]any)
		out := make([]map[string]any, 0, len(raw))
		for _, r := range raw {
			bm, _ := r.(map[string]any)
			out = append(out, bm)
		}
		return out
	}
	return nil
}

// sundryDebtors is the INDEPENDENT ORACLE: the Balance Sheet figure comes from
// ledger_balance (built from postings), not from bill_allocation, so the two
// agreeing is a genuine cross-check rather than one query reformatted.
func (f billFixture) sundryDebtors(t *testing.T, srv *server.Server) string {
	t.Helper()
	rec, b := doRaw(t, srv, http.MethodGet, f.base+"/balance-sheet", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("balance sheet = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var walk func(nodes []any) string
	walk = func(nodes []any) string {
		for _, n := range nodes {
			nm, _ := n.(map[string]any)
			if name, _ := nm["name"].(string); name == "Sundry Debtors" {
				amt, _ := nm["amount"].(string)
				return amt
			}
			kids, _ := nm["children"].([]any)
			if v := walk(kids); v != "" {
				return v
			}
		}
		return ""
	}
	assets, _ := b["assets"].(map[string]any)
	nodes, _ := assets["nodes"].([]any)
	return walk(nodes)
}

// TestAnUnnamedBillReferenceReachesTheAgeingReport is the reachability proof: a
// body a BROWSER can build reaches Outstandings under a name that ties back to the
// voucher the operator keyed, and the ageing report ties to the Balance Sheet.
func TestAnUnnamedBillReferenceReachesTheAgeingReport(t *testing.T) {
	srv := newServer(t)
	f := newBillFixture(t, srv)

	_, no := f.postSale(t, srv, f.partyID, "94400.0000", "2026-06-30")

	bills := f.outstandingBills(t, srv, f.partyID)
	if len(bills) != 1 {
		t.Fatalf("want exactly one open bill, got %+v", bills)
	}
	// ⛔ THE PROPERTY, NOT THE STRING — see the header. The name must exist, must
	// carry the number printed on the document, and must NOT be that number alone,
	// because a bare number names one voucher per type per year.
	got, _ := bills[0]["ref_name"].(string)
	if got == "" {
		t.Fatal("the bill reached the report nameless; nothing can ever settle it")
	}
	if !strings.Contains(got, no) {
		t.Fatalf("bill ref_name %q does not carry the voucher number %q — the "+
			"operator cannot tie the aged bill to the invoice in their hand", got, no)
	}
	if got == no {
		t.Fatalf("the bill is named %q, the bare voucher number: a Receipt %q or "+
			"next year's Sales %q would be the same bill on this party", got, no, no)
	}
	if !strings.HasPrefix(got, "Sales/") {
		t.Fatalf("bill ref_name %q does not name the class whose counter numbered it", got)
	}
	if got, _ := bills[0]["pending"].(string); got != "94400.0000" {
		t.Fatalf("pending = %q, want 94400.0000", got)
	}
	if debtors := f.sundryDebtors(t, srv); debtors != "94400.0000" {
		t.Fatalf("Sundry Debtors %q != the 94400.0000 that is ageing — "+
			"two reports over the same books disagreeing", debtors)
	}

	// ⛔ THE CONTROL. The same sale to a party that does NOT maintain bill-by-bill
	// carries no reference at all, so it reaches Sundry Debtors and NOT the
	// ageing report. Without this the assertions above are consistent with a
	// server that invents a bill for every voucher.
	rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/sales", f.ownerTok, map[string]any{
		"voucher_date": "2026-06-19", "party": "walk-in", "party_ledger_id": f.cashID,
		"created_by": f.subject,
		"postings": []any{
			map[string]any{"ledger_id": f.cashID, "dr_cr": "D", "amount": "1000.0000"},
			map[string]any{"ledger_id": f.salesID, "dr_cr": "C", "amount": "1000.0000"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("control sale = %d (%s)", rec.Code, rec.Body.String())
	}
	// ⛔ IT SHOWS AS `On Account`, NOT AS NOTHING, AND NOT AS A BILL (D106).
	// This assertion used to read `len(got) != 0` — "a party with no references has
	// nothing in the ageing report" — and that was the defect, not the control: the
	// 1,000 reached Sundry Debtors below while the report that is supposed to
	// explain Sundry Debtors showed it nowhere. What the control has to prove is
	// that the server does not INVENT A NAMED BILL, and this proves it better:
	// exactly one row, named the on-account bucket, flagged, and UN-AGED.
	if got := f.outstandingBills(t, srv, f.cashID); len(got) != 1 {
		t.Fatalf("a party with no bill references should show exactly one On Account "+
			"row, got %+v", got)
	} else {
		row := got[0]
		if name, _ := row["ref_name"].(string); name != reporting.OnAccountRefName {
			t.Fatalf("the unreferenced 1,000 is reported as a bill named %q — the "+
				"server invented a bill for a voucher that named none", name)
		}
		if on, _ := row["on_account"].(bool); !on {
			t.Fatalf("the On Account row is not flagged on_account: %+v", row)
		}
		if slab, _ := row["slab"].(string); slab != "" {
			t.Fatalf("the On Account row was aged into slab %q; money with no bill has "+
				"no due date to be overdue against", slab)
		}
		if pend, _ := row["pending"].(string); pend != "1000.0000" {
			t.Fatalf("On Account pending = %q, want 1000.0000", pend)
		}
	}
	if debtors := f.sundryDebtors(t, srv); debtors != "95400.0000" {
		t.Fatalf("Sundry Debtors %q, want 95400.0000 — the control sale is missing "+
			"from the sheet, so it proves nothing", debtors)
	}
}

// TestAReceiptSettlesTheBillItNames drives the whole arc a business does, over
// HTTP: raise 94,400, receive 50,000 against it, watch 44,400 age.
//
// ⛔ THE NAME COMES OFF THE AGEING REPORT, which is where the settle picker's
// open-bill list comes from too. Guessing it from the voucher number is what this
// test used to do and is what no client may do — see the header.
func TestAReceiptSettlesTheBillItNames(t *testing.T) {
	srv := newServer(t)
	f := newBillFixture(t, srv)

	_, no := f.postSale(t, srv, f.partyID, "94400.0000", "2026-06-30")
	open := f.outstandingBills(t, srv, f.partyID)
	if len(open) != 1 {
		t.Fatalf("want one open bill to settle, got %+v", open)
	}
	ref, _ := open[0]["ref_name"].(string)

	receipt := func(ref, amount string) (int, string) {
		rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/receipt", f.ownerTok,
			map[string]any{
				"voucher_date": "2026-07-02", "party": "buyer", "party_ledger_id": f.partyID,
				"created_by": f.subject,
				"postings": []any{
					map[string]any{"ledger_id": f.bankID, "dr_cr": "D", "amount": amount},
					map[string]any{"ledger_id": f.partyID, "dr_cr": "C", "amount": amount},
				},
				"bill_allocations": []any{
					map[string]any{
						"ledger_id": f.partyID, "ref_name": ref, "ref_type": "AGAINST",
						"dr_cr": "C", "amount": amount,
					},
				},
			})
		return rec.Code, rec.Body.String()
	}

	if code, body := receipt(ref, "50000.0000"); code != http.StatusCreated {
		t.Fatalf("receipt = %d, want 201 (%s)", code, body)
	}

	bills := f.outstandingBills(t, srv, f.partyID)
	if len(bills) != 1 {
		t.Fatalf("want ONE netted bill, got %+v — two rows means the receipt opened "+
			"its own reference instead of settling the invoice", bills)
	}
	if got, _ := bills[0]["pending"].(string); got != "44400.0000" {
		t.Fatalf("pending after part settlement = %q, want 44400.0000", got)
	}
	if debtors := f.sundryDebtors(t, srv); debtors != "44400.0000" {
		t.Fatalf("Sundry Debtors %q != the 44400.0000 that is ageing", debtors)
	}

	// ⛔ AND THE CONTROL THAT NAMES THE COST OF THE FIX. A receipt naming the BARE
	// voucher number settles NOTHING — it opens its own reference beside the
	// invoice. That is deliberate (the bare number is not an identity) and it is
	// why the picker offers names off this report and warns about a hand-typed
	// AGAINST that matches none of them.
	if code, body := receipt(no, "500.0000"); code != http.StatusCreated {
		t.Fatalf("bare-number receipt = %d, want 201 (%s)", code, body)
	}
	after := f.outstandingBills(t, srv, f.partyID)
	if len(after) != 2 {
		t.Fatalf("want the invoice AND an unapplied credit, got %+v", after)
	}
	for _, b := range after {
		if n, _ := b["ref_name"].(string); n != ref {
			continue
		}
		if p, _ := b["pending"].(string); p != "44400.0000" {
			t.Fatalf("a receipt naming the bare number %q moved the invoice to %q; "+
				"the bill is called %q and nothing else may net against it", no, p, ref)
		}
	}

	// ⛔ AND THE REFUSAL THAT MAKES THE NAME MEAN SOMETHING. An unnamed AGAINST is
	// rejected rather than defaulted, because a defaulted one would open a second
	// bill under this voucher's own number and leave the invoice fully
	// outstanding beside a negative row — both wrong, and the report still foots.
	code, body := receipt("", "1000.0000")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("an unnamed AGAINST = %d, want 422 (%s)", code, body)
	}
	if !strings.Contains(body, "must name the bill it settles") {
		t.Fatalf("the refusal does not explain itself: %s", body)
	}
}

// TestAnAlterRebuiltFromTheGetKeepsTheBill is §12 item 4a: a form rebuilt from
// GET /vouchers/{id} must re-send the references, WITH the control that an alter
// omitting them is REFUSED.
//
// ⭐ THE CONTROL GOT STRONGER AT D106, NOT WEAKER. It used to prove that an alter
// dropping the references DESTROYED the bill — which made re-sending them
// load-bearing, but only because the alternative was silent data loss: the
// receivable stayed on the Balance Sheet while the report that ages it went
// blank. Since the bill-by-bill coverage rule the same alter cannot be made at
// all, so the form is not merely well-advised to re-send them, it is unable to
// proceed without them. The old comment about the money "vanishing" is gone
// because the vanishing is gone.
func TestAnAlterRebuiltFromTheGetKeepsTheBill(t *testing.T) {
	srv := newServer(t)
	f := newBillFixture(t, srv)

	vID, _ := f.postSale(t, srv, f.partyID, "94400.0000", "2026-06-30")

	// Read the voucher back the way a form does.
	grec, got := doRaw(t, srv, http.MethodGet, f.base+"/vouchers/"+vID, f.ownerTok, nil)
	if grec.Code != http.StatusOK {
		t.Fatalf("get voucher = %d (%s)", grec.Code, grec.Body.String())
	}
	bills, _ := got["bill_allocations"].([]any)
	if len(bills) != 1 {
		t.Fatalf("the GET does not return the bill references: %s", grec.Body.String())
	}
	// ⛔ THE NAME THE FORM WILL RE-SEND, TAKEN FROM THE GET. This is the whole
	// point of item 4a: the client does not know what the server called the bill
	// and must not try to work it out — it echoes back what it was given.
	bm, _ := bills[0].(map[string]any)
	no, _ := bm["ref_name"].(string)
	if no == "" {
		t.Fatalf("the GET returns a nameless bill reference: %s", grec.Body.String())
	}
	postings, _ := got["postings"].([]any)

	// ⛔ THE CONTROL FIRST: an alter that OMITS them is REFUSED. Without this half,
	// "the alter kept it" is equally consistent with references being immune to an
	// alter — in which case re-sending them proves nothing.
	arec, _ := doRaw(t, srv, http.MethodPut, f.base+"/vouchers/sales/"+vID, f.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-19", "narration": "typo fixed",
			"party": "buyer", "party_ledger_id": f.partyID, "created_by": f.subject,
			"postings": postings,
		})
	if arec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an alter that dropped the references = %d, want 422 — the form can "+
			"silently lose a bill by rebuilding itself badly (%s)",
			arec.Code, arec.Body.String())
	}
	// ⛔ AND THE REFUSAL SAYS WHAT TO DO INSTEAD. An operator who genuinely does not
	// know which bill the money is against has an answer — ON_ACCOUNT — and a
	// refusal that withheld it would just teach them to find another door.
	if body := arec.Body.String(); !strings.Contains(body, "ON_ACCOUNT") {
		t.Fatalf("the refusal does not offer the on-account answer: %s", body)
	}
	// ⛔ A REFUSED ALTER MUST LEAVE THE BOOKS EXACTLY AS THEY WERE. A 422 that had
	// already written half of itself would be worse than the data loss it replaced,
	// so the bill is still here, still named, still whole — and so is the sheet.
	{
		still := f.outstandingBills(t, srv, f.partyID)
		if len(still) != 1 {
			t.Fatalf("after the REFUSED alter the party has %+v — the refusal was not "+
				"atomic", still)
		}
		if name, _ := still[0]["ref_name"].(string); name != no {
			t.Fatalf("the refused alter renamed the bill to %q, want %q", name, no)
		}
		if on, _ := still[0]["on_account"].(bool); on {
			t.Fatalf("the refused alter turned the bill into an on-account row: %+v",
				still[0])
		}
	}
	if debtors := f.sundryDebtors(t, srv); debtors != "94400.0000" {
		t.Fatalf("Sundry Debtors %q, want 94400.0000 — the refused alter moved money",
			debtors)
	}

	// Now the round trip a fixed form performs: re-send what the GET returned.
	rrec, _ := doRaw(t, srv, http.MethodPut, f.base+"/vouchers/sales/"+vID, f.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-19", "narration": "typo fixed properly",
			"party": "buyer", "party_ledger_id": f.partyID, "created_by": f.subject,
			"postings":         postings,
			"bill_allocations": bills,
		})
	if rrec.Code != http.StatusOK && rrec.Code != http.StatusCreated {
		t.Fatalf("restating alter = %d (%s)", rrec.Code, rrec.Body.String())
	}
	after := f.outstandingBills(t, srv, f.partyID)
	if len(after) != 1 {
		t.Fatalf("want the bill back, got %+v", after)
	}
	// ⛔ UNDER THE SAME NAME. A re-blanked name would let the server re-stamp,
	// which is a no-op only by luck.
	if name, _ := after[0]["ref_name"].(string); name != no {
		t.Fatalf("the restated bill is named %q, want %q", name, no)
	}
	if p, _ := after[0]["pending"].(string); p != "94400.0000" {
		t.Fatalf("pending after the restating alter = %q, want 94400.0000", p)
	}
}

// TestTheAgeingReportsScopeOnTheWireIsHONEST is D86's caption, on the wire.
//
// ⛔⛔ IT EXISTS BECAUSE A MUTATION PROVED NOTHING ELSE WOULD CATCH IT. Reverting the
// echo to the RESOLVED as-on — the false caption the row is half about — left the
// ENTIRE Go suite green: `go test ./... -count=1` exit 0, 0 FAIL. The rows were right
// and the sentence over them was wrong, and no test read the sentence. That is the
// §5.19 class in its purest form: every figure agrees while the caption lies, and it
// is the half a wrong number can never reveal.
//
// ⛔ THE TWO FIELDS SAY TWO DIFFERENT THINGS AND ONE COULD NOT SAY BOTH:
//   - `as_on` is what the ROWS are limited to, and `""` means NOT LIMITED. That is the
//     convention `/stock-summary` already uses, and the client's
//     `outstandingsScopeLine` turns the empty string into "NO DATE LIMIT — …".
//   - `aged_as_on` is the day the SLABS are measured against, and is always a date:
//     an unbounded report still buckets every bill by days overdue, and dropping that
//     would trade a false caption for a missing one.
func TestTheAgeingReportsScopeOnTheWireIsHONEST(t *testing.T) {
	srv := newServer(t)
	f := newBillFixture(t, srv)

	// Two invoices: one ordinary, one dated well AHEAD. Both are POSTED, so the
	// ledger balance carries both and a bill-wise breakup of it must too.
	//
	// ⚠️ THE FUTURE ONE NEEDS A FUTURE **VOUCHER DATE**, AND THE FIRST DRAFT OF THIS
	// TEST GOT THAT WRONG. `postSale`'s last argument is the bill's DUE date and its
	// voucher_date is hardcoded, so passing "2027-01-15" there dated the bill's
	// payment terms into the future and left the voucher on 2026-06-19 — both
	// invoices inside every bound, and the explicit-as_on control read 14000.0000
	// and failed. The inclusion predicate keys on `vv.voucher_date`; nothing else.
	f.postSale(t, srv, f.partyID, "10000.0000", "2026-06-30")
	f.postSaleOn(t, srv, f.partyID, "4000.0000", "2027-02-15", "2027-01-15")

	// ── NO as_on: the rows are unbounded and the echo must SAY SO ───────────────
	rec, b := doRaw(t, srv, http.MethodGet, f.base+"/outstandings?kind=RECEIVABLE", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("outstandings = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got, _ := b["as_on"].(string); got != "" {
		t.Errorf("as_on = %q for a request that named NO date; the rows are not limited "+
			"to it, so every caption downstream claims a boundary this report does not "+
			"have — which is exactly the defect, with all the numbers right", got)
	}
	aged, _ := b["aged_as_on"].(string)
	if aged == "" {
		t.Errorf("aged_as_on is empty: the slab labels in this response mean nothing " +
			"without the day they were measured against")
	}
	// ⛔ AND THE ROWS THEMSELVES, so this is a caption over the answer it describes
	// rather than a field asserted in isolation.
	if total, _ := b["total"].(string); total != "14000.0000" {
		t.Errorf("unbounded total = %q, want 14000.0000 (both invoices)", total)
	}
	if debtors := f.sundryDebtors(t, srv); debtors != "14000.0000" {
		t.Errorf("Sundry Debtors %q != the unbounded ageing total — the bill-wise breakup "+
			"explains less than the balance it breaks up", debtors)
	}

	// ── AN EXPLICIT as_on: bounded, and the echo names the date ─────────────────
	// ⛔ THE CONTROL. "Always send an empty as_on" passes every arm above.
	rec2, b2 := doRaw(t, srv, http.MethodGet,
		f.base+"/outstandings?kind=RECEIVABLE&as_on=2026-12-31", f.ownerTok, nil)
	if rec2.Code != http.StatusOK {
		t.Fatalf("outstandings as_on = %d, want 200 (%s)", rec2.Code, rec2.Body.String())
	}
	if got, _ := b2["as_on"].(string); got != "2026-12-31" {
		t.Errorf("as_on = %q for an explicit as_on=2026-12-31", got)
	}
	if got, _ := b2["aged_as_on"].(string); got != "2026-12-31" {
		t.Errorf("aged_as_on = %q for an explicit as_on=2026-12-31", got)
	}
	if total, _ := b2["total"].(string); total != "10000.0000" {
		t.Errorf("as at 2026-12-31 total = %q, want 10000.0000 — the bound was REMOVED "+
			"rather than made conditional, and the report can no longer answer "+
			"'as at the year end'", total)
	}
}
