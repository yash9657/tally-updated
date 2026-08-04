package server

// THE TDS CHALLAN ROUTES — T6, migration 0069.
//
//	GET  .../tds-challans?tax_year_start=  -> Display / tds-challan
//	POST .../tds-challans                 -> Create  / tds-challan
//
// ⚠️ WRITTEN BUT NOT EXECUTED IN THE SESSION THAT ADDED THEM: all worktrees in
// that session shared one Postgres on :5433 and another process held it. The
// first run against a free database is the evidence. A SKIP IS INDISTINGUISHABLE
// FROM A PASS IN THE EXIT CODE, so census the run:
//
//	go test ./internal/server/ -count=1 -run TDSChallan -v | grep -c -- '--- SKIP'
//
// must be 0.
//
// THESE TESTS FAIL, BY DESIGN, UNTIL THE THREE SHARED-FILE REGISTRATIONS LAND.
// The store and the handlers are in this branch; the chi routes (server.go), the
// routeAction arm (auth_middleware.go) and the resource token
// (authz.KnownResources) are on the integrator's manifest because those three
// files are shared. Until all three exist the Owner cases below answer 403
// ("unmapped route: access denied") rather than 200/201 —
// companyAuthMiddleware refuses an unmapped route BEFORE it loads the level, so
// even default_access 'full' never applies. That is precisely the signal
// TestEveryCompanyRouteIsMappedAndInVocabulary was built to give.
//
// ---------------------------------------------------------------------------
// IN-PACKAGE (package server, not server_test) because it uses the
// aclRequest/aclOwnerCompany/aclSeededDataEntryToken harness in
// auth_payroll_itc_acl_test.go, which reaches unexported wiring. Everything
// ASSERTED on still goes through s.Router() with a real Bearer token, so the
// middleware, the PDP and the SEEDED security levels are all in the path — the
// seeded level is the oracle, never a hand-written one.
//
// ⚠️ aclToken MINTS WITH amr=webauthn, and the POST cases depend on it: the
// seeded Owner level has requires_webauthn true and Action.IsWrite() triggers
// step-up, so a write test whose token was minted any other way gets a refusal
// that reads exactly like an ACL bug. Reads do not inherit it.

import (
	"net/http"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The fixture: a company that has actually DEDUCTED something.
//
// A challan cannot be recorded in the abstract. Every allocation is verified
// against a live tds_deduction row inside the store's transaction (migration
// 0069 deliberately carries no foreign key to it), and the allocated amount must
// EQUAL the tax that was deducted — so the fixture has to post a real TDS
// voucher through the real post path. THAT IS A FEATURE OF THIS FILE RATHER THAN
// A COST: it is the only place in the tree where engine -> deduction -> challan
// is exercised over HTTP end to end. internal/masters' own DB test seeds
// tds_deduction with SQL, because masters may not import internal/ledger.
//
// The voucher is the one TestRecordingTheStatusUnblocksTDSPosting uses:
// ₹2,00,000 of professional fees under 194J-P, which that test asserts the
// engine derives ₹20,000 of tax from. NO RATE IS RESTATED HERE — the figure is
// read back off the response and carried into the allocation, so this file
// cannot drift from the engine and cannot be the place a wrong rate is invented.
// ---------------------------------------------------------------------------

type tdsChallanFixture struct {
	companyID string
	base      string
	ownerTok  string
	// voucherID, section and legSeq are tds_deduction's NATURAL key. legSeq is 0
	// because VoucherInput carries one TDS block (internal/ledger/tds.go says so
	// at the INSERT).
	voucherID string
	section   string
	legSeq    int
	// taxRupees is what the ENGINE derived, as it came back over the wire. The
	// allocation must equal it exactly.
	taxRupees string
	// taxYearStart is the year the deduction belongs to. The voucher is dated
	// 2026-05-10, so it is 2026-27, and the challan must name the SAME year — the
	// store refuses a challan whose year differs from its deductions', which is
	// what tds_challan's stored tax_year_start exists for.
	taxYearStart int
}

func newTDSChallanFixture(t *testing.T, s *Server, suffix string) tdsChallanFixture {
	t.Helper()
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	newLedger := func(name, group string) string {
		rec, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok,
			map[string]any{"name": name, "group_name": group, "created_by": "owner"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d (%s)", name, rec.Code, rec.Body.String())
		}
		id, _ := body["ledger_id"].(string)
		return id
	}
	expense := newLedger("Professional Fees "+suffix, "Indirect Expenses")
	tdsPay := newLedger("TDS Payable "+suffix, "Duties & Taxes")
	creditor := newLedger("Consultant "+suffix, "Sundry Creditors")

	// A payment in 2026-27 is governed by the PRECEDING year's status —
	// s.402(37)(b), and the off-by-one auth_deductor_status_test.go pins.
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/deductor-status/2025", ownerTok,
		map[string]any{"entity_category": "COMPANY", "recorded_by": "owner"}); rec.Code != http.StatusOK {
		t.Fatalf("record deductor status = %d", rec.Code)
	}

	rec, body := aclRequest(t, s, http.MethodPost, base+"/vouchers/purchase", ownerTok,
		map[string]any{
			"voucher_date":    "2026-05-10",
			"party_ledger_id": creditor,
			"created_by":      "owner",
			"postings": []any{
				map[string]any{"ledger_id": expense, "dr_cr": "D", "amount": "200000.0000"},
				map[string]any{"ledger_id": tdsPay, "dr_cr": "C", "amount": "20000.0000"},
				map[string]any{"ledger_id": creditor, "dr_cr": "C", "amount": "180000.0000"},
			},
			"tds": map[string]any{
				"section":            "194J-P",
				"deductee_ledger_id": creditor,
				"deductee_type":      "OTHER",
				"pan_available":      true,
				"taxable_amount":     "200000.0000",
				"tds_ledger_id":      tdsPay,
			},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post the TDS voucher = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)
	tds, _ := body["tds"].(map[string]any)
	tax, _ := tds["tds"].(string)
	if voucherID == "" || tax == "" {
		t.Fatalf("the fixture voucher returned no id or no derived tax: %v", body)
	}
	return tdsChallanFixture{
		companyID:    companyID,
		base:         base,
		ownerTok:     ownerTok,
		voucherID:    voucherID,
		section:      "194J-P",
		legSeq:       0,
		taxRupees:    tax,
		taxYearStart: 2026,
	}
}

// challanBody is a full, valid deposit of exactly the tax the fixture deducted.
//
// The BSR code and the challan serial are the file format's odd pair: both are
// TEXT and both carry significant LEADING ZEROS (0004329 is a bank branch and
// 4329 is a different one; 05122 is not 5122). They are written as strings here
// for that reason and not for tidiness.
func (f tdsChallanFixture) challanBody() map[string]any {
	return map[string]any{
		"bsr_code":     "0004329",
		"deposit_date": "2026-06-07",
		"challan_no":   "05122",
		"tax_rupees":   f.taxRupees,
		// 200 = paid over of the deductor's own motion. STATED, because there is
		// no default anywhere on this path: defaulting would assert on the
		// company's behalf that the deposit was voluntary rather than demanded.
		"minor_head":     "200",
		"tax_year_start": f.taxYearStart,
		"allocations": []any{
			map[string]any{
				"voucher_id":    f.voucherID,
				"section":       f.section,
				"leg_seq":       f.legSeq,
				"amount_rupees": f.taxRupees,
			},
		},
		"recorded_by": "owner",
	}
}

// ---------------------------------------------------------------------------
// The matrix.
// ---------------------------------------------------------------------------

// TestTDSChallanAuthMatrix pins the four-row answer for both halves of the route
// and, with it, the ACL decision behind the resource token.
//
// ⚠️ THE DECISION, and it needs no ACL migration, which is the whole point:
// `tds-challan` is a BARE resource token. The seeded Data Entry level has
// default_access 'none' and every rule it carries names a specific token, so a
// token never mentioned is refused — measured on a freshly-migrated database. Had
// this been modelled as `report:tds-challan`, migration 0011's coarse
// `allow display report` would REACH it at specificity 1 and hand every seeded
// Data Entry operator the company's tax deposits, and taking that back would need
// a 0068-style counterweight migration.
//
// The consequence is stated rather than left implicit: DATA ENTRY IS REFUSED BOTH
// HALVES WITH NO RULE WRITTEN. An administrator who wants a clerk recording
// challans writes an explicit `allow create tds-challan` through the admin API.
// That is a tenant's decision, taken deliberately.
func TestTDSChallanAuthMatrix(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "AM")
	dataEntry := aclSeededDataEntryToken(t, s, f.base, f.ownerTok)
	otherTok := aclToken(t, s, "acl-outsider-"+f.companyID[:8])

	path := f.base + "/tds-challans"

	// --- The WRITE. --------------------------------------------------------
	// THE SAME VALID BODY IN ALL FOUR CASES. If the refused cases sent junk, a
	// 403 could be a 403 for a reason that has nothing to do with the ACL, and
	// the Owner case would be the only one actually testing anything. The three
	// refusals run FIRST, so nothing they send can consume the one deduction the
	// Owner case needs (a deduction can be paid by exactly one challan).
	if rec, _ := aclRequest(t, s, http.MethodPost, path, "", f.challanBody()); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST tds-challans with no token = %d, want 401", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodPost, path, otherTok, f.challanBody()); rec.Code != http.StatusForbidden {
		t.Errorf("POST tds-challans as another tenant = %d, want 403", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodPost, path, dataEntry, f.challanBody()); rec.Code != http.StatusForbidden {
		t.Errorf("POST tds-challans as Data Entry = %d, want 403 — recording a deposit "+
			"asserts to the department that money reached the Government, and it is the "+
			"act that makes a deduction filable; it belongs to whoever signs the return "+
			"unless a tenant explicitly delegates it", rec.Code)
	}
	if rec, out := aclRequest(t, s, http.MethodPost, path, f.ownerTok, f.challanBody()); rec.Code != http.StatusCreated {
		t.Fatalf("POST tds-challans as Owner = %d, want 201 (%v)", rec.Code, out)
	}

	// --- The READ. Same token, so the same four answers. -------------------
	list := path + "?tax_year_start=2026"
	if rec, _ := aclRequest(t, s, http.MethodGet, list, "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET tds-challans with no token = %d, want 401", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, list, otherTok, nil); rec.Code != http.StatusForbidden {
		t.Errorf("GET tds-challans as another tenant = %d, want 403", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, list, dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("GET tds-challans as Data Entry = %d, want 403 — the register lists "+
			"every deductee paid, with amounts, and read and write share one resource "+
			"token so they are granted or refused together", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, list, f.ownerTok, nil); rec.Code != http.StatusOK {
		t.Errorf("GET tds-challans as Owner = %d, want 200", rec.Code)
	}
}

// TestTDSChallanRoundTripsThroughTheWire is the shape a screen depends on: the
// deposit goes in, and what comes back names the deductee it paid, the minor
// head in words, and the unallocated remainder.
func TestTDSChallanRoundTripsThroughTheWire(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "RT")
	path := f.base + "/tds-challans"

	rec, body := aclRequest(t, s, http.MethodPost, path, f.ownerTok, f.challanBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if id, _ := body["id"].(string); id == "" {
		t.Error("the response carries no challan id; migration 0069 gives tds_challan.id " +
			"NO default, so the id is generated in Go and must come back")
	}
	// The POST reads the row back, and the reason is this one field: the deposit
	// is whole rupees and the deduction is a 4-dp figure, so the difference is
	// what the operator must see AT ENTRY rather than on filing day.
	posted, _ := body["challan"].(map[string]any)
	if posted == nil {
		t.Fatalf("the POST did not read the challan back: %v", body)
	}
	if posted["unallocated_tax_rupees"] != "0.0000" {
		t.Errorf("unallocated_tax_rupees = %v, want 0.0000 — this deposit is exactly the "+
			"tax that was deducted. ⚠️ A NON-ZERO VALUE HERE IS NOT NECESSARILY A BUG in "+
			"general (see the handler header: whole-rupee deposits against 4-dp deductions "+
			"differ by paise), but it is one for THIS voucher, whose tax is a round number",
			posted["unallocated_tax_rupees"])
	}

	rec, body = aclRequest(t, s, http.MethodGet, path+"?tax_year_start=2026", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["tax_year_label"] != "2026-27" {
		t.Errorf("tax_year_label = %v, want 2026-27", body["tax_year_label"])
	}
	// The Annexure 7 vocabulary is SERVED, never hard-coded in the client: a copy
	// in TypeScript is a copy no test can pin, and it could offer a code migration
	// 0069's CHECK refuses on save.
	heads, _ := body["minor_heads"].([]any)
	if len(heads) != 3 {
		t.Errorf("minor_heads has %d entries, want 3 (200, 400, 100)", len(heads))
	}
	challans, _ := body["challans"].([]any)
	if len(challans) != 1 {
		t.Fatalf("want 1 challan for 2026-27, got %d", len(challans))
	}
	c, _ := challans[0].(map[string]any)
	// The CIN parts survive as TEXT with their leading zeros. If any layer ever
	// puts them through a number type this is the assertion that fails.
	if c["bsr_code"] != "0004329" {
		t.Errorf("bsr_code = %v, want 0004329 — the leading zeros are part of the "+
			"branch code, and 4329 is a different branch", c["bsr_code"])
	}
	if c["challan_no"] != "05122" {
		t.Errorf("challan_no = %v, want 05122 — 05122 is not 5122", c["challan_no"])
	}
	if c["tax_rupees"] != "20000.0000" || c["total_deposited_rupees"] != "20000.0000" {
		t.Errorf("tax/total = %v/%v, want 20000.0000 both (nothing but tax was paid)",
			c["tax_rupees"], c["total_deposited_rupees"])
	}
	if c["minor_head"] != "200" || !strings.Contains(fmtAny(c["minor_head_description"]), "motion") {
		t.Errorf("minor head = %v (%v); the description travels with the code so a list "+
			"row can be read without a client-side copy of the vocabulary",
			c["minor_head"], c["minor_head_description"])
	}
	if c["allocation_count"] != float64(1) {
		t.Errorf("allocation_count = %v, want 1", c["allocation_count"])
	}
	allocs, _ := c["allocations"].([]any)
	if len(allocs) != 1 {
		t.Fatalf("want 1 allocation, got %d", len(allocs))
	}
	a, _ := allocs[0].(map[string]any)
	if a["voucher_id"] != f.voucherID || a["section"] != "194J-P" {
		t.Errorf("allocation names %v/%v, want %s/194J-P", a["voucher_id"], a["section"], f.voucherID)
	}
	// WHO was paid, not just which voucher. This is what turns "a challan
	// register is missing" into a sentence naming a supplier.
	if !strings.Contains(fmtAny(a["deductee_name"]), "Consultant") {
		t.Errorf("deductee_name = %v, want the deductee ledger's name — the register's "+
			"whole question is which deductee a deposit paid for", a["deductee_name"])
	}
	if a["deduction_on_record"] != true || a["broken_by_alter"] != false {
		t.Errorf("deduction_on_record/broken_by_alter = %v/%v, want true/false for a "+
			"voucher nobody has altered", a["deduction_on_record"], a["broken_by_alter"])
	}
	if a["allocated_rupees"] != "20000.0000" || a["deduction_tax_rupees"] != "20000.0000" {
		t.Errorf("allocated/deducted = %v/%v, want 20000.0000 both — the two must be "+
			"EQUAL, and reporting treats any inequality as an allocation broken by an alter",
			a["allocated_rupees"], a["deduction_tax_rupees"])
	}
	if c["recorded_by"] != "owner" || c["recorded_at"] == nil || c["recorded_at"] == "" {
		t.Errorf("recorded_by/at = %v/%v; the list is read from the row, not echoed",
			c["recorded_by"], c["recorded_at"])
	}
}

// TestTheChallanRefusalNamesTheMissingFact — THE TEST THIS FILE EXISTS FOR
// besides the matrix.
//
// Migration 0069 carries NO foreign key to tds_deduction, deliberately:
// internal/ledger deletes and re-derives every deduction row with fresh ids on
// each voucher ALTER, so a cascade would destroy the record of a real payment and
// a restrict would make a voucher unalterable once its tax was deposited. THE
// PRICE IS THAT NOTHING IN THE DATABASE WOULD STOP AN ORPHAN ALLOCATION — a
// deposit recorded against no deduction, which inflates the register and
// reconciles to no deductee. The store verifies the triple inside its own
// transaction instead, and the refusal must NAME the voucher, the section and the
// leg, because "invalid allocation" leaves an operator with three fields and no
// way to tell which one is wrong.
//
// Each case also asserts the STATUS, because these three sentinels split across
// two HTTP families on purpose: a wrong request is 400 and a conflict with what
// the books already say is 409.
func TestTheChallanRefusalNamesTheMissingFact(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "MF")
	path := f.base + "/tds-challans"

	for _, tc := range []struct {
		name   string
		mut    func(map[string]any)
		status int
		wants  []string
	}{
		{
			// The orphan the schema cannot refuse. Leg 3 of a voucher that has
			// one leg: nothing was deducted there.
			name: "an allocation to a leg that carries no deduction",
			mut: func(b map[string]any) {
				b["allocations"].([]any)[0].(map[string]any)["leg_seq"] = 3
			},
			status: http.StatusBadRequest,
			wants:  []string{"nothing was deducted on voucher", "leg 3", "194J-P"},
		},
		{
			// The equality. A part payment is refused AT ENTRY rather than
			// refusing the whole quarter at filing time with a message about a
			// voucher change nobody made.
			name: "a deposit smaller than the tax it claims to pay",
			mut: func(b map[string]any) {
				b["tax_rupees"] = "15000"
				b["allocations"].([]any)[0].(map[string]any)["amount_rupees"] = "15000"
			},
			status: http.StatusBadRequest,
			wants:  []string{"20000", "15000", "must be EQUAL"},
		},
		{
			// My own rule, flagged in the store's notes so a reviewer can veto
			// it: there is no allocate-later route in this slice, so a challan
			// stored without its allocations is permanently stranded and every
			// deduction it paid permanently unfilable.
			name: "a challan that deposits tax and names no deduction",
			mut: func(b map[string]any) {
				b["allocations"] = []any{}
			},
			status: http.StatusBadRequest,
			wants:  []string{"names no deduction", "field 5"},
		},
		{
			// The minor head has NO DEFAULT, and the refusal has to say what the
			// three codes MEAN or the operator picks the first one.
			name: "no minor head",
			mut: func(b map[string]any) {
				b["minor_head"] = ""
			},
			status: http.StatusBadRequest,
			wants:  []string{"NO DEFAULT", "200", "400", "100"},
		},
		{
			// The BSR code is seven digits of TEXT. The operator's instinct on
			// seeing 0004329 refused is to drop the zeros, so the sentence has
			// to forbid that explicitly.
			name: "a BSR code with its leading zeros dropped",
			mut: func(b map[string]any) {
				b["bsr_code"] = "4329"
			},
			status: http.StatusBadRequest,
			wants:  []string{"SEVEN DIGITS", "LEADING ZEROS"},
		},
		{
			// leg_seq is 0-BASED, so an omitted field cannot be defaulted to 0 —
			// it would silently allocate to the first leg and nothing downstream
			// would catch it when that leg's amount happens to match. Refused by
			// name in the handler, which is the only layer that can tell an
			// absent field from a zero one.
			name: "an allocation with no leg_seq at all",
			mut: func(b map[string]any) {
				delete(b["allocations"].([]any)[0].(map[string]any), "leg_seq")
			},
			status: http.StatusBadRequest,
			wants:  []string{"leg_seq is required", "0-based"},
		},
	} {
		body := f.challanBody()
		tc.mut(body)
		rec, _ := aclRequest(t, s, http.MethodPost, path, f.ownerTok, body)
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.status, rec.Body.String())
			continue
		}
		for _, want := range tc.wants {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s: the refusal must say %q, got %s", tc.name, want, rec.Body.String())
			}
		}
	}
}

// TestTheSameDepositCannotBeRecordedTwice — the CIN is the department's identity
// for a payment, and a second post of one CIN is either a double entry or a
// second real deposit whose serial was mistyped. NEITHER may be an upsert:
// overwriting the first loses a real payment.
//
// 409 AND NOT 400, deliberately. Nothing in the body is wrong; changing a field
// would not help and RETRYING IS THE MISTAKE. The register is asserted afterwards
// because a status code alone would not catch an upsert that answered 409 and
// still overwrote the row.
func TestTheSameDepositCannotBeRecordedTwice(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "CIN")
	path := f.base + "/tds-challans"

	if rec, _ := aclRequest(t, s, http.MethodPost, path, f.ownerTok, f.challanBody()); rec.Code != http.StatusCreated {
		t.Fatalf("first POST = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	rec, _ := aclRequest(t, s, http.MethodPost, path, f.ownerTok, f.challanBody())
	if rec.Code != http.StatusConflict {
		t.Fatalf("re-posting the same CIN = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "already recorded") {
		t.Errorf("the refusal must say the challan is already recorded: %s", rec.Body.String())
	}

	_, body := aclRequest(t, s, http.MethodGet, path+"?tax_year_start=2026", f.ownerTok, nil)
	challans, _ := body["challans"].([]any)
	if len(challans) != 1 {
		t.Fatalf("after a refused duplicate the register has %d challans, want 1 — a "+
			"duplicate CIN must be a REFUSAL and never an upsert", len(challans))
	}
}

// TestADeductionCannotBePaidByTwoChallans is the other 409, and the one that
// stops a double payment: a DIFFERENT deposit slip allocated to a deduction some
// other challan has already paid.
//
// tds_challan_allocation_one_per_deduction is the authority; the store pre-reads
// so the sentence can name the challan that already paid it, and matches the
// constraint by NAME for the racing case rather than a bare 23505 (two
// constraints on that table mean two different sentences).
func TestADeductionCannotBePaidByTwoChallans(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "2X")
	path := f.base + "/tds-challans"

	if rec, _ := aclRequest(t, s, http.MethodPost, path, f.ownerTok, f.challanBody()); rec.Code != http.StatusCreated {
		t.Fatalf("first POST = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	// A genuinely different deposit — different serial, different date, same
	// deduction. This is what an operator who forgot they had already paid does.
	second := f.challanBody()
	second["challan_no"] = "05199"
	second["deposit_date"] = "2026-06-08"
	rec, _ := aclRequest(t, s, http.MethodPost, path, f.ownerTok, second)
	if rec.Code != http.StatusConflict {
		t.Fatalf("paying the same deduction twice = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "already paid") {
		t.Errorf("the refusal must say the deduction is already paid, and by which "+
			"challan: %s", rec.Body.String())
	}
}

// TestTheChallanRegisterRefusesToGuessTheTaxYear. There is no "this year"
// default, for the reason migration 0069 stores the year on the challan at all:
// a deposit made on 7 April normally belongs to the tax year that ended a week
// earlier, so a defaulted year would answer a question the operator did not ask
// while looking exactly like the one they did.
func TestTheChallanRegisterRefusesToGuessTheTaxYear(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	path := "/v1/companies/" + companyID + "/tds-challans"

	rec, _ := aclRequest(t, s, http.MethodGet, path, ownerTok, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET with no tax_year_start = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tax_year_start") {
		t.Errorf("the refusal must name the missing parameter: %s", rec.Body.String())
	}
	// And a company that has recorded nothing gets an EMPTY LIST with the
	// vocabulary, not a 404: a screen opens in this state and still has to draw
	// the minor-head picker.
	rec, body := aclRequest(t, s, http.MethodGet, path+"?tax_year_start=2026", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET for a company with no challans = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if challans, _ := body["challans"].([]any); len(challans) != 0 {
		t.Errorf("challans = %v, want an empty list", body["challans"])
	}
	if heads, _ := body["minor_heads"].([]any); len(heads) != 3 {
		t.Errorf("minor_heads = %v, want the three Annexure 7 codes even when there is "+
			"nothing recorded — a screen opens in exactly this state", body["minor_heads"])
	}
}

// fmtAny renders a decoded JSON value for a substring assertion without caring
// whether it arrived as a string or as null.
func fmtAny(v any) string {
	s, _ := v.(string)
	return s
}
