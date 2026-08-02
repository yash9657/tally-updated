package server

// THE TDS/TCS REGISTER — Item Zero step 4.
//
// Everything Item Zero built before this was invisible: correct, stored, and
// impossible to look at. These tests cover the report that ends that, the
// authority migration 0068 writes for it, and the ONE thing it still refuses to
// do — emit a return file.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/testdb"
)

// recordDeductorStatus writes migration 0049's row over an ADMIN connection.
//
// In-package twin of the one in voucher_tds_http_test.go (package server_test),
// which is not reachable from here. It goes in by SQL because no route records
// it for an arbitrary company without a token dance, and because these tests are
// about the register rather than about step 2's route — which has its own
// coverage.
func recordDeductorStatus(t *testing.T, companyID string, taxYearStart int, category string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx,
		`INSERT INTO company_deductor_status
		   (id, company_id, tax_year_start, entity_category, recorded_by)
		 VALUES ($1, $2, $3, $4, 'tester')
		 ON CONFLICT (company_id, tax_year_start) DO NOTHING`,
		uuid.New(), companyID, taxYearStart, category); err != nil {
		t.Fatalf("record deductor status: %v", err)
	}
}

// registerFixture is a company with a TDS voucher and a TCS voucher already
// posted, so the register has something to show.
type registerFixture struct {
	companyID string
	base      string
	tok       string
	buyer     string
	creditor  string
}

func newRegisterFixture(t *testing.T, s *Server) registerFixture {
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
	expense := newLedger("Professional Fees REG", "Indirect Expenses")
	tdsPay := newLedger("TDS Payable REG", "Duties & Taxes")
	creditor := newLedger("Consultant REG", "Sundry Creditors")
	sales := newLedger("Scrap Sales REG", "Sales Accounts")
	tcsPay := newLedger("TCS Payable REG", "Duties & Taxes")
	buyer := newLedger("Foundry REG", "Sundry Debtors")

	recordDeductorStatus(t, companyID, 2025, "COMPANY")

	// A 194J-P deduction: ₹2,00,000 at 10% = ₹20,000.
	if rec, _ := aclRequest(t, s, http.MethodPost, base+"/vouchers/purchase", ownerTok,
		map[string]any{
			"voucher_no": "REG-P1", "voucher_date": "2026-05-10",
			"party_ledger_id": creditor, "created_by": "owner",
			"postings": []any{
				map[string]any{"ledger_id": expense, "dr_cr": "D", "amount": "200000.0000"},
				map[string]any{"ledger_id": tdsPay, "dr_cr": "C", "amount": "20000.0000"},
				map[string]any{"ledger_id": creditor, "dr_cr": "C", "amount": "180000.0000"},
			},
			"tds": map[string]any{
				"section": "194J-P", "deductee_ledger_id": creditor,
				"deductee_type": "OTHER", "pan_available": true,
				"taxable_amount": "200000.0000", "tds_ledger_id": tdsPay,
			},
		}); rec.Code != http.StatusCreated {
		t.Fatalf("seed TDS voucher = %d (%s)", rec.Code, rec.Body.String())
	}

	// A scrap sale: ₹1,00,000 at 2% = ₹2,000.
	if rec, _ := aclRequest(t, s, http.MethodPost, base+"/vouchers/sales", ownerTok,
		map[string]any{
			"voucher_no": "REG-S1", "voucher_date": "2026-05-12",
			"party_ledger_id": buyer, "created_by": "owner",
			"postings": []any{
				map[string]any{"ledger_id": buyer, "dr_cr": "D", "amount": "102000.0000"},
				map[string]any{"ledger_id": sales, "dr_cr": "C", "amount": "100000.0000"},
				map[string]any{"ledger_id": tcsPay, "dr_cr": "C", "amount": "2000.0000"},
			},
			"tcs": map[string]any{
				"section": "206C(1)-SCRAP", "buyer_ledger_id": buyer,
				"pan_available": true, "taxable_amount": "100000.0000",
				"tcs_ledger_id": tcsPay,
			},
		}); rec.Code != http.StatusCreated {
		t.Fatalf("seed TCS voucher = %d (%s)", rec.Code, rec.Body.String())
	}

	return registerFixture{companyID: companyID, base: base, tok: ownerTok,
		buyer: buyer, creditor: creditor}
}

// TestTDSRegisterShowsBothSides is the deliverable: an operator can finally see
// what was deducted and what was collected, per party, for a period.
func TestTDSRegisterShowsBothSides(t *testing.T) {
	s := payrollServer(t)
	f := newRegisterFixture(t, s)

	rec, body := aclRequest(t, s, http.MethodGet,
		f.base+"/tds-register?from=2026-04-01&to=2026-06-30", f.tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("register = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	deds, _ := body["deductions"].([]any)
	if len(deds) != 1 {
		t.Fatalf("want 1 deduction, got %d", len(deds))
	}
	d, _ := deds[0].(map[string]any)
	if d["deductee"] != "Consultant REG" || d["tds"] != "20000.0000" || d["section"] != "194J-P" {
		t.Errorf("deduction row: %v", d)
	}
	// The Annexure 2 code, shown because it is the key the return files this row
	// under — an operator checking a return against the register needs it.
	if d["return_code"] != "1027" {
		t.Errorf("return_code = %v, want 1027", d["return_code"])
	}

	cols, _ := body["collections"].([]any)
	if len(cols) != 1 {
		t.Fatalf("want 1 collection, got %d", len(cols))
	}
	c, _ := cols[0].(map[string]any)
	if c["buyer"] != "Foundry REG" || c["tcs"] != "2000.0000" {
		t.Errorf("collection row: %v", c)
	}
	if c["declaration"] != "NONE" {
		t.Errorf("declaration = %v, want NONE", c["declaration"])
	}

	if body["total_tds"] != "20000.0000" || body["total_tcs"] != "2000.0000" {
		t.Errorf("totals: tds=%v tcs=%v", body["total_tds"], body["total_tcs"])
	}
	if body["tax_year_start"] != float64(2026) {
		t.Errorf("tax_year_start = %v, want 2026", body["tax_year_start"])
	}
}

// TestRegisterDisclosesWhatItCannotTellYou pins the caveats as DATA rather than
// screen decoration. The most important one is that none of these figures is
// tax PAID — and it is the deposit, not the deduction, that stops interest
// running under s.396.
func TestRegisterDisclosesWhatItCannotTellYou(t *testing.T) {
	s := payrollServer(t)
	f := newRegisterFixture(t, s)

	_, body := aclRequest(t, s, http.MethodGet,
		f.base+"/tds-register?from=2026-04-01&to=2026-06-30", f.tok, nil)
	disclosures, _ := body["disclosures"].([]any)
	joined := ""
	for _, d := range disclosures {
		joined += " " + d.(string)
	}
	if !strings.Contains(joined, "DEPOSIT IS NOT TRACKED") {
		t.Errorf("the register must say it does not track deposit; got %q", joined)
	}
	if !strings.Contains(joined, "NO RETURN CAN BE FILED") {
		t.Errorf("the register must say no return can be filed from it; got %q", joined)
	}
}

// TestRegisterRefusesToEmitAReturnFile is I-7, and it is the point at which this
// track stops rather than guesses.
//
// Enough is known from commentary to produce something file-shaped. What is NOT
// known is the payment-code list, and a wrong one files a contractor payment as
// professional fees under a real person's PAN. So the register carries a
// structured REFUSAL naming every blocker and where to get it — a machine-
// readable "no", not an absence a client could mistake for "not asked for".
func TestRegisterRefusesToEmitAReturnFile(t *testing.T) {
	s := payrollServer(t)
	f := newRegisterFixture(t, s)

	_, body := aclRequest(t, s, http.MethodGet,
		f.base+"/tds-register?from=2026-04-01&to=2026-06-30", f.tok, nil)
	rf, ok := body["return_file"].(map[string]any)
	if !ok {
		t.Fatal("the register must carry a return_file status; its ABSENCE is what a " +
			"client would misread as 'not requested'")
	}
	if rf["available"] != false {
		t.Fatal("available must be false until a layout is read from a primary source")
	}
	// I-7 in one field: an empty format_version is exactly what makes it
	// unavailable.
	if rf["format_version"] != "" {
		t.Errorf("format_version = %v, want empty — a pinned version means a READ "+
			"primary source, and there is none", rf["format_version"])
	}
	// ALWAYS false, and always present. The FVU needs a JRE that does not exist
	// here, so "generated" must never be allowed to read as "validated".
	if rf["validated"] != false {
		t.Error("validated must be false: the FVU cannot run in this environment")
	}
	blockers, _ := rf["blockers"].([]any)
	if len(blockers) < 3 {
		t.Fatalf("the refusal must NAME what is missing and where to get it; got %d blockers",
			len(blockers))
	}
	// The dangerous blocker specifically: the code list.
	all := ""
	for _, b := range blockers {
		m, _ := b.(map[string]any)
		all += " " + m["what"].(string) + " " + m["why"].(string)
	}
	if !strings.Contains(all, "PAYMENT CODE LIST") {
		t.Errorf("the payment-code list is the blocker that makes a guessed file "+
			"dangerous rather than merely wrong; it must be named: %q", all)
	}
}

// TestDataEntryIsRefusedTheTDSRegister is migration 0068, and it CREATES ITS
// COMPANY AT TEST TIME — so it exercises seed_company_auth_defaults rather than
// the backfill. That distinction is the whole reason 0046's header exists: a
// migration that backfills and forgets the seeder is correct for every company
// that existed and silently wrong for every company created afterwards.
func TestDataEntryIsRefusedTheTDSRegister(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)

	// ⚠️ THE CONTROL. Migration 0011 grants Data Entry a coarse
	// `allow display report`, which reaches EVERY report:NAME at specificity 1.
	// This asserts that grant is real and still working — without it, the refusal
	// below would pass for the wrong reason (a level that can read no report at
	// all proves nothing about this one).
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/trial-balance?as_on=2026-07-31", dataEntry, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: Data Entry must still read the Trial Balance = %d, want 200", rec.Code)
	}

	// And is refused THIS one, by 0068's specificity-2 disallow.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/tds-register?from=2026-04-01&to=2026-06-30", dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("Data Entry read the TDS register = %d, want 403 — it is every "+
			"supplier paid and every customer collected from, with amounts", rec.Code)
	}

	// Owner may.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/tds-register?from=2026-04-01&to=2026-06-30", ownerTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("Owner read the TDS register = %d, want 200", rec.Code)
	}
}

// TestTDSRegisterTenancy is the two-touch rule for the route.
func TestTDSRegisterTenancy(t *testing.T) {
	s := payrollServer(t)
	f := newRegisterFixture(t, s)
	otherTok := aclToken(t, s, "reg-outsider-"+f.companyID[:8])

	path := f.base + "/tds-register?from=2026-04-01&to=2026-06-30"
	if rec, _ := aclRequest(t, s, http.MethodGet, path, "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, path, otherTok, nil); rec.Code != http.StatusForbidden {
		t.Errorf("another tenant = %d, want 403", rec.Code)
	}
}

// TestRegisterRefusesAnUndatedPeriod pins the deliberate absence of a default.
// A correctly rendered register for the wrong quarter is worse than a refusal.
func TestRegisterRefusesAnUndatedPeriod(t *testing.T) {
	s := payrollServer(t)
	f := newRegisterFixture(t, s)

	rec, _ := aclRequest(t, s, http.MethodGet, f.base+"/tds-register", f.tok, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("register with no period = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "wrong") {
		t.Errorf("the refusal should say WHY there is no default: %s", rec.Body.String())
	}
}

// TestRegisterCountsAnAlteredVoucherOnce is I-9, and the sibling case the
// printed Day Book already gets wrong.
//
// voucher_version is immutable and hash-chained, so an altered voucher has TWO
// versions for ever. A register that read versions would show the payment twice
// and double the quarter's liability. This one reads tds_deduction, whose row
// was REPLACED rather than appended (ledger/tds.go re-derives on ALTER) — so the
// correctness is structural, and this test pins it against the table rather than
// against that reasoning.
func TestRegisterCountsAnAlteredVoucherOnce(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	newLedger := func(name, group string) string {
		_, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok,
			map[string]any{"name": name, "group_name": group, "created_by": "owner"})
		id, _ := body["ledger_id"].(string)
		return id
	}
	expense := newLedger("Fees ALT", "Indirect Expenses")
	tdsPay := newLedger("TDS Payable ALT", "Duties & Taxes")
	creditor := newLedger("Consultant ALT", "Sundry Creditors")
	recordDeductorStatus(t, companyID, 2025, "COMPANY")

	voucher := func(base_, tds, net string) map[string]any {
		return map[string]any{
			"voucher_no": "ALT-1", "voucher_date": "2026-05-10",
			"party_ledger_id": creditor, "created_by": "owner",
			"postings": []any{
				map[string]any{"ledger_id": expense, "dr_cr": "D", "amount": base_},
				map[string]any{"ledger_id": tdsPay, "dr_cr": "C", "amount": tds},
				map[string]any{"ledger_id": creditor, "dr_cr": "C", "amount": net},
			},
			"tds": map[string]any{
				"section": "194J-P", "deductee_ledger_id": creditor,
				"deductee_type": "OTHER", "pan_available": true,
				"taxable_amount": base_, "tds_ledger_id": tdsPay,
			},
		}
	}

	rec, body := aclRequest(t, s, http.MethodPost, base+"/vouchers/purchase", ownerTok,
		voucher("200000.0000", "20000.0000", "180000.0000"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("post = %d (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)

	// Correct it to ₹3,00,000 -> ₹30,000.
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/vouchers/purchase/"+voucherID, ownerTok,
		voucher("300000.0000", "30000.0000", "270000.0000")); rec.Code != http.StatusOK {
		t.Fatalf("alter = %d (%s)", rec.Code, rec.Body.String())
	}

	_, reg := aclRequest(t, s, http.MethodGet,
		base+"/tds-register?from=2026-04-01&to=2026-06-30", ownerTok, nil)
	deds, _ := reg["deductions"].([]any)
	if len(deds) != 1 {
		t.Fatalf("an altered voucher must contribute ONE row, not one per version; got %d", len(deds))
	}
	if reg["total_tds"] != "30000.0000" {
		t.Fatalf("total_tds = %v, want 30000.0000 — the RESTATED figure, counted once. "+
			"50000.0000 would mean both versions were summed", reg["total_tds"])
	}
}

// TestRegisterExcludesADeletedVoucher is I-6. A tombstoned voucher leaves
// ledger_balance, every GST report and every stock movement; it must leave the
// register too, or a quarter is filed on a payment the books say never happened.
func TestRegisterExcludesADeletedVoucher(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	newLedger := func(name, group string) string {
		_, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok,
			map[string]any{"name": name, "group_name": group, "created_by": "owner"})
		id, _ := body["ledger_id"].(string)
		return id
	}
	expense := newLedger("Fees DEL", "Indirect Expenses")
	tdsPay := newLedger("TDS Payable DEL", "Duties & Taxes")
	creditor := newLedger("Consultant DEL", "Sundry Creditors")
	recordDeductorStatus(t, companyID, 2025, "COMPANY")

	rec, body := aclRequest(t, s, http.MethodPost, base+"/vouchers/purchase", ownerTok,
		map[string]any{
			"voucher_no": "DEL-1", "voucher_date": "2026-05-10",
			"party_ledger_id": creditor, "created_by": "owner",
			"postings": []any{
				map[string]any{"ledger_id": expense, "dr_cr": "D", "amount": "200000.0000"},
				map[string]any{"ledger_id": tdsPay, "dr_cr": "C", "amount": "20000.0000"},
				map[string]any{"ledger_id": creditor, "dr_cr": "C", "amount": "180000.0000"},
			},
			"tds": map[string]any{
				"section": "194J-P", "deductee_ledger_id": creditor,
				"deductee_type": "OTHER", "pan_available": true,
				"taxable_amount": "200000.0000", "tds_ledger_id": tdsPay,
			},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post = %d (%s)", rec.Code, rec.Body.String())
	}
	voucherID, _ := body["voucher_id"].(string)

	// The control: it IS in the register before the delete. Without this arm the
	// test would pass against a register that showed nothing at all.
	_, reg := aclRequest(t, s, http.MethodGet,
		base+"/tds-register?from=2026-04-01&to=2026-06-30", ownerTok, nil)
	if reg["total_tds"] != "20000.0000" {
		t.Fatalf("CONTROL: before the delete the register must show it; got %v", reg["total_tds"])
	}

	if rec, _ := aclRequest(t, s, http.MethodDelete, base+"/vouchers/"+voucherID, ownerTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete = %d (%s)", rec.Code, rec.Body.String())
	}

	_, reg = aclRequest(t, s, http.MethodGet,
		base+"/tds-register?from=2026-04-01&to=2026-06-30", ownerTok, nil)
	deds, _ := reg["deductions"].([]any)
	if len(deds) != 0 || reg["total_tds"] != "0.0000" {
		t.Fatalf("a deleted voucher must contribute nothing; got %d rows totalling %v",
			len(deds), reg["total_tds"])
	}
}
