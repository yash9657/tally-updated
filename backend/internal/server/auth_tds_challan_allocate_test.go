package server

// auth_tds_challan_allocate_test.go — THE ALLOCATE-LATER ROUTE, ON THE WIRE.
//
//	POST .../tds-challans/{challanID}/allocations -> Create / tds-challan
//
// ===========================================================================
// WHAT THIS ROUTE IS FOR, AND WHY ITS ABSENCE WAS A TRAP RATHER THAN A GAP
// ===========================================================================
// A challan may legitimately be recorded naming FEWER deductions than it paid
// for: masters/tds_challan.go has never required the allocations to cover the
// tax, only that a challan carrying tax names at least one deduction. Until this
// route existed the remainder could then never be attached — the create is the
// only writer and the same CIN cannot be entered twice, so the second post is a
// 409 and never an upsert.
//
// ⛔ THE PARITY AUDIT DROVE IT (register row D1): seven doors in the browser and
// six HTTP methods against challan 05130, and its unallocated balance stayed at
// exactly 12000.0000, while reporting.Form140StatementFor refused the unattached
// deduction with ErrTaxNotDeposited. Tax that HAD been paid to the Government was
// permanently unfilable, and nothing in the product could say so.
//
// ===========================================================================
// ⛔ AND IT INHERITS THE BACK-DATED CUTOFF FROM A DATE IT DOES NOT SEND
// ===========================================================================
// The body carries NO date — the deposit is already recorded and an allocation
// adopts its date — so every body peek in auth_middleware.go is blind to this
// act. That is round 3's shape exactly (the voucher ALTER and DELETE), and the
// answer is round 3's answer: judge the date the books ALREADY HOLD, via
// storedChallanDepositDate. TestAllocatingIntoAClosedPeriodIsRefused below is the
// executed proof, with its control.

import (
	"net/http"
	"strings"
	"testing"
)

// allocateBody is the request: the lines to attach, and who is attaching them.
func allocateBody(voucherID, section string, legSeq int, amount string) map[string]any {
	return map[string]any{
		"allocations": []any{
			map[string]any{
				"voucher_id":    voucherID,
				"section":       section,
				"leg_seq":       legSeq,
				"amount_rupees": amount,
			},
		},
		"recorded_by": "owner",
	}
}

// challanWithRoom records a deposit for TWO deductions while naming only ONE —
// the state the audit found and could not leave. It returns the challan id and
// the second deduction that is waiting to be attached.
//
// ⛔ IT POSTS ITS OWN TWO DEDUCTIONS rather than reusing the fixture's, because a
// deduction can be paid by exactly ONE challan: a helper that reused the
// fixture's would 409 on its second call, which is the constraint working, not a
// bug — and a test that hit it would be testing the fixture.
func challanWithRoom(t *testing.T, s *Server, f tdsChallanFixture,
	depositDate, challanNo string) roomyChallan {
	t.Helper()
	firstVoucher, firstTax := f.postAnotherDeduction(t, s, "2026-05-10")
	secondVoucher, secondTax := f.postAnotherDeduction(t, s, "2026-05-10")

	body := f.challanBody()
	body["deposit_date"] = depositDate
	body["challan_no"] = challanNo
	body["allocations"] = []any{
		map[string]any{
			"voucher_id":    firstVoucher,
			"section":       f.section,
			"leg_seq":       f.legSeq,
			"amount_rupees": firstTax,
		},
	}
	// Twice the tax, one allocation. NOT a mistake by the operator: one deposit
	// covering two deductions is the ordinary way tax is paid over.
	body["tax_rupees"] = mulTax(t, firstTax)
	rec, out := aclRequest(t, s, http.MethodPost, f.base+"/tds-challans", f.ownerTok, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("record the challan = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	challanID, _ := out["id"].(string)
	if challanID == "" {
		t.Fatalf("the challan came back with no id: %v", out)
	}
	posted, _ := out["challan"].(map[string]any)
	if posted == nil || posted["unallocated_tax_rupees"] != firstTax {
		t.Fatalf("unallocated after the create = %v, want %s — the fixture is not the shape this "+
			"test is about", posted["unallocated_tax_rupees"], firstTax)
	}
	return roomyChallan{
		id:           challanID,
		namedVoucher: firstVoucher,
		namedTax:     firstTax,
		spareVoucher: secondVoucher,
		spareTax:     secondTax,
	}
}

// roomyChallan is one recorded deposit that paid for two deductions and names
// one of them. `spare*` is the one waiting to be attached — the whole subject of
// register row D1.
type roomyChallan struct {
	id           string
	namedVoucher string
	namedTax     string
	spareVoucher string
	spareTax     string
}

// mulTax doubles the engine's figure without restating a rate, using the same
// string→decimal parse the handler uses. ⚠️ It is deliberately NOT a hard-coded
// "40000.0000": the fixture reads its tax off the engine's own response so this
// file cannot become the place a wrong rate is invented.
func mulTax(t *testing.T, tax string) string {
	t.Helper()
	d, err := optionalDecimal(tax)
	if err != nil {
		t.Fatalf("the fixture's tax %q is not a decimal", tax)
	}
	return d.Add(d).StringFixed(4)
}

// TestAllocatingLaterOnTheWireMakesTheBalanceFALL is register row D1's own
// oracle, driven through the real router: the unallocated balance must FALL.
//
// ⛔ IT ASSERTS THE REGISTER, NOT THE STATUS CODE. A 200 that changed nothing is
// this repo's signature failure, and the audit's finding was precisely a series
// of well-formed requests that moved 12000.0000 not at all.
func TestAllocatingLaterOnTheWireMakesTheBalanceFALL(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "AL")
	ch := challanWithRoom(t, s, f, "2026-06-07", "05130")

	rec, out := aclRequest(t, s, http.MethodPost,
		f.base+"/tds-challans/"+ch.id+"/allocations", f.ownerTok,
		allocateBody(ch.spareVoucher, f.section, f.legSeq, ch.spareTax))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST .../allocations = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// THE READ-BACK the route sends, and then the REGISTER, because the two must
	// agree: a route that reported a fall its own list did not show would be the
	// same defect one layer up.
	posted, _ := out["challan"].(map[string]any)
	if posted == nil {
		t.Fatalf("the allocate did not read the challan back: %v", out)
	}
	if posted["unallocated_tax_rupees"] != "0.0000" {
		t.Errorf("unallocated_tax_rupees = %v, want 0.0000 — THIS IS THE ROW'S ORACLE. It stayed "+
			"at exactly 12000.0000 through seven browser doors and six HTTP methods because no "+
			"route existed to move it", posted["unallocated_tax_rupees"])
	}
	if n, _ := posted["allocation_count"].(float64); n != 2 {
		t.Errorf("allocation_count = %v, want 2", posted["allocation_count"])
	}

	rec, body := aclRequest(t, s, http.MethodGet,
		f.base+"/tds-challans?tax_year_start=2026", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET the register = %d (%s)", rec.Code, rec.Body.String())
	}
	challans, _ := body["challans"].([]any)
	if len(challans) != 1 {
		t.Fatalf("want 1 challan, got %d", len(challans))
	}
	c, _ := challans[0].(map[string]any)
	if c["unallocated_tax_rupees"] != "0.0000" {
		t.Errorf("the REGISTER still shows unallocated %v after a 200 — the route reported a "+
			"change the list does not have", c["unallocated_tax_rupees"])
	}
	allocs, _ := c["allocations"].([]any)
	if len(allocs) != 2 {
		t.Fatalf("the register shows %d allocation(s), want 2", len(allocs))
	}
	// ⛔ NAMES AND SIDES, never a total alone. Both deductions must be named, each
	// against the party the tax was deducted from, and neither may read as broken
	// by an alter the moment it was recorded.
	seen := map[string]bool{}
	for _, raw := range allocs {
		a, _ := raw.(map[string]any)
		seen[a["voucher_id"].(string)] = true
		if a["deductee_name"] == "" {
			t.Error("an allocation names no deductee; an operator recognises the party, not a UUID")
		}
		if broken, _ := a["broken_by_alter"].(bool); broken {
			t.Errorf("allocation on %v reads BROKEN BY ALTER the moment it was made", a["voucher_no"])
		}
		if a["kind"] != "TDS" {
			t.Errorf("allocation on %v reads kind %v, want TDS", a["voucher_no"], a["kind"])
		}
	}
	if !seen[ch.namedVoucher] || !seen[ch.spareVoucher] {
		t.Errorf("the deposit does not name both deductions it paid: %v", seen)
	}

	// And the picker now says the second deduction is deposited, so an operator
	// cannot tick it again and collect a 409.
	allocatable, _ := body["allocatable_deductions"].([]any)
	for _, raw := range allocatable {
		d, _ := raw.(map[string]any)
		if d["voucher_id"] == ch.spareVoucher {
			if paid, _ := d["already_paid"].(bool); !paid {
				t.Error("the picker still offers the deduction that was just deposited")
			}
		}
	}
}

// TestAllocatingLaterCannotOverrunTheDepositOnTheWire is register row D2 through
// the new route, with the sentinel mapped to 400.
func TestAllocatingLaterCannotOverrunTheDepositOnTheWire(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "OV")

	// A deposit of EXACTLY the fixture's tax, fully allocated at entry.
	rec, out := aclRequest(t, s, http.MethodPost, f.base+"/tds-challans", f.ownerTok, f.challanBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("record the challan = %d (%s)", rec.Code, rec.Body.String())
	}
	challanID, _ := out["id"].(string)
	second, secondTax := f.postAnotherDeduction(t, s, "2026-05-10")

	rec, _ = aclRequest(t, s, http.MethodPost,
		f.base+"/tds-challans/"+challanID+"/allocations", f.ownerTok,
		allocateBody(second, f.section, f.legSeq, secondTax))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("attaching a second ₹20,000 deduction to a ₹20,000 deposit = %d, want 400 (%s)\n"+
			"Nothing compared the allocations against the tax box, which is how ₹1 was accepted as "+
			"having paid ₹27,000 across two deductees — and both were then marked deposited, so "+
			"the real deposit could never be allocated to them", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "more than was paid") {
		t.Errorf("the refusal does not say what disagrees: %s", rec.Body.String())
	}

	// ⛔ AND THE BOOKS DID NOT MOVE. A refusal that still wrote is this repo's
	// signature failure.
	rec, body := aclRequest(t, s, http.MethodGet,
		f.base+"/tds-challans?tax_year_start=2026", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET the register = %d", rec.Code)
	}
	challans, _ := body["challans"].([]any)
	c, _ := challans[0].(map[string]any)
	if n, _ := c["allocation_count"].(float64); n != 1 {
		t.Errorf("allocation_count = %v after a 400, want 1", c["allocation_count"])
	}
	if c["unallocated_tax_rupees"] != "0.0000" {
		t.Errorf("unallocated = %v after a 400, want 0.0000 — a refused request wrote",
			c["unallocated_tax_rupees"])
	}
}

// TestAllocateLaterAuthMatrix pins the four-row answer for the new route.
//
// It shares the `tds-challan` token with the two routes beside it — one small
// object, one rule — so the answers must be the SAME four. ⚠️ THE SAME VALID
// BODY IN ALL FOUR CASES: if the refused cases sent junk, a 403 could be a 403
// for a reason that has nothing to do with the ACL. The three refusals run FIRST
// so nothing they send can consume the deduction the Owner case needs.
func TestAllocateLaterAuthMatrix(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "AM2")
	ch := challanWithRoom(t, s, f, "2026-06-07", "05130")
	dataEntry := aclSeededDataEntryToken(t, s, f.base, f.ownerTok)
	otherTok := aclToken(t, s, "acl-outsider2-"+f.companyID[:8])

	path := f.base + "/tds-challans/" + ch.id + "/allocations"
	body := allocateBody(ch.spareVoucher, f.section, f.legSeq, ch.spareTax)

	if rec, _ := aclRequest(t, s, http.MethodPost, path, "", body); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST allocations with no token = %d, want 401", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodPost, path, otherTok, body); rec.Code != http.StatusForbidden {
		t.Errorf("POST allocations as another tenant = %d, want 403", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodPost, path, dataEntry, body); rec.Code != http.StatusForbidden {
		t.Errorf("POST allocations as Data Entry = %d, want 403 — saying which deductions a "+
			"deposit discharged is the same authority as recording the deposit, and one token "+
			"governs both", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodPost, path, f.ownerTok, body); rec.Code != http.StatusOK {
		t.Fatalf("POST allocations as Owner = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// TestAllocatingIntoAClosedPeriodIsRefused — ⛔ THE CUTOFF, ON A REQUEST THAT
// CARRIES NO DATE AT ALL.
//
// The legs, and why each one is load-bearing:
//
//	CONTROL   POST /vouchers/journal dated 2026-05-31            -> 403
//	          Proves the cutoff is live on THIS company; without it every 403
//	          below could be any other denial.
//	ATTACK    allocate to a challan DEPOSITED 2026-06-07         -> MUST be 403
//	          The deposit is inside the lock, so what it paid for is fixed.
//	ORACLE    the register after the attack                      -> unmoved
//	          A 403 that still WROTE is this repo's signature failure.
//	CONTROL   allocate to a challan DEPOSITED 2026-07-15         -> MUST be 200
//	          Without this leg a "fix" that refused EVERY allocation would pass —
//	          and refusing every allocation breaks the whole feature. ⚠️ Its
//	          deduction is dated 2026-05-10, INSIDE the lock, which is the second
//	          thing this leg proves: the deduction's own date is deliberately NOT
//	          judged. Tax is always deducted before it is deposited, so judging it
//	          would refuse the feature at every company that has locked a filed
//	          month, while protecting nothing — no voucher is restated.
func TestAllocatingIntoAClosedPeriodIsRefused(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "cut2")

	// Everything is keyed BEFORE the lock goes on, which is the honest order: an
	// owner closes a period after the work in it is done.
	locked := challanWithRoom(t, s, f, "2026-06-07", "05130")
	unlocked := challanWithRoom(t, s, f, "2026-07-15", "05131")

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	const cutoff = "2026-07-01"
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, cutoff)

	// CONTROL: the cutoff is live.
	rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		map[string]any{"voucher_date": "2026-05-31", "postings": []any{}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CONTROL: POST /vouchers/journal dated 2026-05-31 under a %s cutoff = %d, want "+
			"403 — the cutoff is not live, so nothing below proves anything (%s)",
			cutoff, rec.Code, rec.Body.String())
	}

	// ATTACK.
	rec, _ = aclRequest(t, s, http.MethodPost,
		f.base+"/tds-challans/"+locked.id+"/allocations", f.ownerTok,
		allocateBody(locked.spareVoucher, f.section, f.legSeq, locked.spareTax))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("allocating to a challan deposited 2026-06-07 under a %s cutoff = %d, want 403 — "+
			"the body carries NO date, so every body peek is blind to this act and the PDP must "+
			"read the STORED deposit_date (storedChallanDepositDate). Attaching a deduction to a "+
			"deposit inside a filed quarter changes what that return says the deposit paid for (%s)",
			cutoff, rec.Code, rec.Body.String())
	}
	// The refusal must name the operator's own subject. They typed no date at
	// all, so "this voucher is dated…" would name a document they never touched.
	if !strings.Contains(rec.Body.String(), "deposit date") {
		t.Errorf("the refusal does not say WHICH date closed the period, so the operator cannot "+
			"act on it: %s", rec.Body.String())
	}

	// ORACLE: nothing was written.
	rec, body := aclRequest(t, s, http.MethodGet,
		f.base+"/tds-challans?tax_year_start=2026", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET the register = %d (%s)", rec.Code, rec.Body.String())
	}
	for _, raw := range body["challans"].([]any) {
		c, _ := raw.(map[string]any)
		if c["id"] != locked.id {
			continue
		}
		if n, _ := c["allocation_count"].(float64); n != 1 {
			t.Errorf("the refused challan carries %v allocation(s), want 1 — A 403 THAT STILL "+
				"WROTE is this repo's signature failure", c["allocation_count"])
		}
		if c["unallocated_tax_rupees"] != locked.spareTax {
			t.Errorf("the refused challan's unallocated moved to %v, want %s",
				c["unallocated_tax_rupees"], locked.spareTax)
		}
	}

	// CONTROL: a deposit AFTER the cutoff still takes allocations, even though
	// the deduction it pays is dated inside the lock.
	rec, _ = aclRequest(t, s, http.MethodPost,
		f.base+"/tds-challans/"+unlocked.id+"/allocations", f.ownerTok,
		allocateBody(unlocked.spareVoucher, f.section, f.legSeq, unlocked.spareTax))
	if rec.Code != http.StatusOK {
		t.Fatalf("allocating to a challan deposited 2026-07-15 (AFTER the %s cutoff) = %d, want "+
			"200 — a control that refuses everything is indistinguishable from a working refusal, "+
			"and tax is ALWAYS deducted before it is deposited, so judging the deduction's own "+
			"date would refuse this feature at every company that locks a filed month (%s)",
			cutoff, rec.Code, rec.Body.String())
	}
}

// TestAllocatingToAChallanThatIsNotThereIs404OnTheWire pins the two shapes of
// "wrong challan" apart. They are different answers on purpose: a malformed id
// is the caller's link, a well-formed unknown one is the books.
//
// ⚠️ ANOTHER TENANT'S CHALLAN IS ALSO A 404 HERE — RLS makes it indistinguishable
// from a missing one, which is the intended answer: the alternative discloses
// whether a CIN exists somewhere else. (It never gets that far anyway: the
// company scope is refused first.)
func TestAllocatingToAChallanThatIsNotThereIs404OnTheWire(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "NF")

	body := allocateBody(f.voucherID, f.section, f.legSeq, f.taxRupees)
	rec, _ := aclRequest(t, s, http.MethodPost,
		f.base+"/tds-challans/not-a-uuid/allocations", f.ownerTok, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a malformed challan id = %d, want 400: \"not found\" would tell the operator the "+
			"deposit is missing when what is wrong is the link they followed", rec.Code)
	}

	rec, _ = aclRequest(t, s, http.MethodPost,
		f.base+"/tds-challans/9f1b2c3d-4e5f-4a6b-8c9d-0e1f2a3b4c5d/allocations", f.ownerTok, body)
	if rec.Code != http.StatusNotFound {
		t.Errorf("a well-formed id for a challan this company does not have = %d, want 404 (%s)",
			rec.Code, rec.Body.String())
	}
}
