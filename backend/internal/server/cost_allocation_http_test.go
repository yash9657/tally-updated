package server_test

// cost_allocation_http_test.go — CAN A CLIENT ACTUALLY ALLOCATE A COST?
//
// ⛔⛔ THE ANSWER WAS NO, AND EVERY OTHER TEST IN THE PROJECT SAID YES.
//
// T2's write path — the per-(posting, category) validation, the hashed payload
// entry, the posting_cost_allocation insert — was built, reviewed and covered by
// unit tests AND by live-Postgres integration tests, all of which drove
// ledger.PostVoucher DIRECTLY. `voucherReq` had no cost_allocations field, so no
// HTTP body could carry one. The whole feature was reachable only from Go.
//
// Nothing failed. The masters routes worked, both report routes worked and
// answered 200, and the reports were correct — they reported, correctly, that
// nothing had been allocated, because nothing could be. A screen built against
// that API would have been complete, green and useless.
//
// This is the same defect voucherReq.ItemLedgerID records one paragraph above
// itself in server.go: "until this field existed on the wire EVERY item invoice
// the F8/F9 grid could send came back 422 — the generator was reachable only from
// Go tests." It happened again, one feature later, and the reason it is worth its
// own file is that NEITHER TIME did any existing test notice.
//
// So this test exists to pin REACHABILITY, and it deliberately asserts through the
// OUTERMOST surface at both ends: it POSTs a voucher as JSON and then reads the
// figures back out of the REPORT, over HTTP. Nothing in between is stubbed. If the
// wire field is dropped, renamed, or silently ignored, the report comes back zero
// and this test fails naming the consequence.
//
// ⚠️ THE CONTROL IS THE POINT. An unallocated voucher of the same amount is posted
// first and the axis is asserted EMPTY. Without it, a report that returned the
// right number for the wrong reason — or a fixture that allocated something in
// setup — would pass.

import (
	"net/http"
	"testing"

	"github.com/khata/cored/internal/server"
)

// costHTTPFixture is a company with two axes, two centres on the first, and the
// two ledgers a pay run posts between.
type costHTTPFixture struct {
	base     string
	ownerTok string

	department string
	branch     string
	sales      string
	admin      string

	salaries string
	bank     string
}

func newCostHTTPFixture(t *testing.T, srv *server.Server) costHTTPFixture {
	t.Helper()
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	f := costHTTPFixture{base: "/v1/companies/" + companyID, ownerTok: ownerTok}

	mkAxis := func(name string) string {
		rec, body := doRaw(t, srv, http.MethodPost, f.base+"/cost-categories", ownerTok,
			map[string]any{"name": name, "created_by": "owner"})
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("owner creates the %s axis = %d, want 201 (%s)", name, rec.Code, rec.Body.String())
		}
		id, _ := body["id"].(string)
		if id == "" {
			t.Fatalf("the %s axis came back with no id: %s", name, rec.Body.String())
		}
		return id
	}
	mkCentre := func(axis, name string) string {
		rec, body := doRaw(t, srv, http.MethodPost, f.base+"/cost-centres", ownerTok,
			map[string]any{"cost_category_id": axis, "name": name, "created_by": "owner"})
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("owner creates cost centre %s = %d, want 201 (%s)", name, rec.Code, rec.Body.String())
		}
		id, _ := body["id"].(string)
		if id == "" {
			t.Fatalf("cost centre %s came back with no id: %s", name, rec.Body.String())
		}
		return id
	}
	mkLedger := func(name, group string) string {
		rec, body := doRaw(t, srv, http.MethodPost, f.base+"/ledgers", ownerTok,
			map[string]any{"name": name, "group_name": group, "created_by": "owner"})
		// The payroll ledger set is seeded per company by migration 0034, so
		// "Salaries & Wages" may already exist — a 409 is an ordinary answer here and
		// the id still has to be found. Same find-or-create reasoning as
		// internal/ledger/cost_allocation_db_test.go's ensureLedger.
		if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
			if id, _ := body["ledger_id"].(string); id != "" {
				return id
			}
			if id, _ := body["id"].(string); id != "" {
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
		t.Fatalf("could not create or find the ledger %q (create said %d: %s)", name, rec.Code, rec.Body.String())
		return ""
	}

	f.department = mkAxis("Department")
	f.branch = mkAxis("Branch")
	f.sales = mkCentre(f.department, "Sales")
	f.admin = mkCentre(f.department, "Admin")
	f.salaries = mkLedger("Salaries & Wages", "Indirect Expenses")
	f.bank = mkLedger("HDFC Current A/c", "Bank Accounts")
	return f
}

// axisTotal reads the Cost Centre Summary for one axis over the period and returns
// its axis-scoped total plus the per-centre own figures, keyed by centre name.
func axisTotal(t *testing.T, srv *server.Server, f costHTTPFixture, axis string) (string, map[string]string) {
	t.Helper()
	rec, body := doRaw(t, srv, http.MethodGet,
		f.base+"/cost-centre-summary?cost_category_id="+axis+"&from=2026-04-01&to=2027-03-31",
		f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET cost-centre-summary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	total, _ := body["total_debit"].(string)
	own := map[string]string{}
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, n := range nodes {
			m, _ := n.(map[string]any)
			if m == nil {
				continue
			}
			name, _ := m["name"].(string)
			d, _ := m["own_debit"].(string)
			own[name] = d
			if kids, ok := m["children"].([]any); ok {
				walk(kids)
			}
		}
	}
	if centres, ok := body["centres"].([]any); ok {
		walk(centres)
	}
	return total, own
}

// TestAClientCanAllocateACostOverHTTPAndSeeItInTheReport is the reachability pin.
//
// It posts the parallel-allocation worked example from migration 0040's header
// through the JSON API and reads it back out of the report, so the assertion spans
// wire -> ledger -> hashed payload -> posting_cost_allocation -> report -> wire.
func TestAClientCanAllocateACostOverHTTPAndSeeItInTheReport(t *testing.T) {
	srv := newServer(t)
	f := newCostHTTPFixture(t, srv)

	// ---- CONTROL: the same voucher with NO allocations. The axis must be empty.
	//
	// Without this leg, a report that reported the right figure for the wrong
	// reason — or a fixture that allocated something during setup — would pass the
	// probe below and prove nothing.
	rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-30",
			"narration":    "June salaries, unallocated (control)",
			"created_by":   "owner",
			"postings": []any{
				map[string]any{"ledger_id": f.salaries, "dr_cr": "D", "amount": "100000.0000"},
				map[string]any{"ledger_id": f.bank, "dr_cr": "C", "amount": "100000.0000"},
			},
		})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: posting an unallocated journal = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if total, _ := axisTotal(t, srv, f, f.department); total != "0.0000" {
		t.Fatalf("CONTROL: the Department axis reports %s allocated after a voucher that allocated NOTHING. "+
			"The probe below cannot prove the wire field works if something else is already writing allocations", total)
	}

	// ---- THE PROBE: the same shape, allocated in full on TWO axes at once.
	//
	// Departments Sales 60,000 / Admin 40,000 — posting_seq 0, the SALARIES DEBIT.
	// Both legs are 1,00,000, so attaching the split to the wrong posting would
	// satisfy the per-category sum perfectly and only the per-centre figures can
	// see it. That is why the centre-level assertions below matter.
	rec, _ = doRaw(t, srv, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		map[string]any{
			"voucher_date": "2026-07-31",
			"narration":    "July salaries, allocated",
			"created_by":   "owner",
			"postings": []any{
				map[string]any{"ledger_id": f.salaries, "dr_cr": "D", "amount": "100000.0000"},
				map[string]any{"ledger_id": f.bank, "dr_cr": "C", "amount": "100000.0000"},
			},
			"cost_allocations": []any{
				map[string]any{"posting_seq": 0, "cost_category_id": f.department, "cost_centre_id": f.sales, "amount": "60000.0000"},
				map[string]any{"posting_seq": 0, "cost_category_id": f.department, "cost_centre_id": f.admin, "amount": "40000.0000"},
			},
		})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("posting an ALLOCATED journal over HTTP = %d, want 201 (%s).\n"+
			"If this is a 422 about an unattributed amount, voucherReq.cost_allocations decoded to nothing and "+
			"the ledger saw an allocation-free voucher; if it is a 400, the ids or the amount did not survive the wire",
			rec.Code, rec.Body.String())
	}

	total, own := axisTotal(t, srv, f, f.department)
	if total != "100000.0000" {
		t.Fatalf("after allocating 60,000 + 40,000 of a 1,00,000 posting over HTTP, the Department axis reports "+
			"%s. A client cannot allocate a cost through this API, so both cost reports can only ever render "+
			"empty however correct they are", total)
	}
	if own["Sales"] != "60000.0000" || own["Admin"] != "40000.0000" {
		t.Fatalf("the Department axis totals correctly but the split landed wrong: Sales=%q Admin=%q, want "+
			"60000.0000 / 40000.0000. A department is being charged for cost it did not bear",
			own["Sales"], own["Admin"])
	}

	// ---- The OTHER axis must still be empty, and that is not a formality.
	//
	// It is the double-count guard at the API level: Branch was never allocated, so
	// if it reports anything then allocations are leaking across axes — and because
	// every axis allocates the same money, a leak here is indistinguishable from
	// correct data by inspection.
	if bTotal, _ := axisTotal(t, srv, f, f.branch); bTotal != "0.0000" {
		t.Fatalf("the Branch axis reports %s allocated, but nothing was ever allocated on it. "+
			"Allocations are leaking across axes, which double-counts every cost report", bTotal)
	}
}

// TestAStatedPostingVoucherAcceptsAnAllocationOverHTTP is the CONTROL LEG ONLY,
// and its name says so because an earlier name did not.
//
// ⚠️ IT WAS CALLED TestAllocatingAnItemInvoiceOverHTTPIsRefusedRatherThanMisattributed
// AND IT DID NOT TEST THAT. The body only posts a Journal with a stated posting and
// asserts the allocation is ACCEPTED — it never builds an item invoice, so it could
// not observe the refusal its name claimed. A test whose name overclaims is the same
// defect as a guard that cannot fail: it reads as coverage and is not.
//
// WHAT IS STILL UNTESTED AT THIS LAYER, stated plainly: that an ITEM INVOICE
// carrying cost_allocations is refused. ledger.post() does refuse it (see the
// itemInvoice guard in ledger.go, which explains why posting_seq would be a lie on
// that class — index 0 is the PARTY leg and the indices shift with the tax profile),
// and that refusal has no HTTP-level test. Building one needs a stock item, a unit
// and an item ledger; newStockVoucherFixture in auth_stock_vouchers_test.go already
// assembles exactly that and is the right thing to borrow.
//
// The control still carries its own weight: without it, a future refusal test could
// be refusing every allocation for any reason and would look correct.
func TestAStatedPostingVoucherAcceptsAnAllocationOverHTTP(t *testing.T) {
	srv := newServer(t)
	f := newCostHTTPFixture(t, srv)

	rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		map[string]any{
			"voucher_date": "2026-07-31",
			"narration":    "a stated-posting voucher accepts allocations",
			"created_by":   "owner",
			"postings": []any{
				map[string]any{"ledger_id": f.salaries, "dr_cr": "D", "amount": "50000.0000"},
				map[string]any{"ledger_id": f.bank, "dr_cr": "C", "amount": "50000.0000"},
			},
			"cost_allocations": []any{
				map[string]any{"posting_seq": 0, "cost_category_id": f.department, "cost_centre_id": f.sales, "amount": "50000.0000"},
			},
		})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("a Journal with a stated posting must ACCEPT an allocation, got %d (%s)",
			rec.Code, rec.Body.String())
	}
}
