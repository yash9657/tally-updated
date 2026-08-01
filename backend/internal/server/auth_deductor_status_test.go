package server

// THE DEDUCTOR-STATUS ROUTE — Item Zero step 2.
//
// This is an IN-PACKAGE test (package server, not server_test) because it uses
// the same aclRequest/aclOwnerCompany harness the payroll and ITC ACL matrices
// use, which reach unexported wiring.
//
// The route exists for one reason: after step 1, the TDS post path refuses every
// voucher for a company whose s.402(11)/(37) status is unrecorded, and nothing
// else writes that row. TestRecordingTheStatusUnblocksTDSPosting is therefore
// the test that matters most here — it is the whole point of the slice, and it
// would still fail if every other test in this file passed.

import (
	"net/http"
	"strings"
	"testing"
)

// TestDeductorStatusAuthMatrix pins the two-touch rule for both halves of the
// route, and the ACL decision recorded in authz.KnownResources: recording the
// deductor status belongs to whoever signs the return, NOT to whoever keys the
// bills.
//
// If the routeAction entry were missing, the Owner cases would be 403 ("unmapped
// route: access denied") and this test would say so.
func TestDeductorStatusAuthMatrix(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)
	otherTok := aclToken(t, s, "acl-outsider-"+companyID[:8])

	body := map[string]any{
		"entity_category": "COMPANY",
		"recorded_by":     "owner",
	}

	// --- The WRITE. -------------------------------------------------------
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/deductor-status/2025", "", body); rec.Code != http.StatusUnauthorized {
		t.Errorf("PUT deductor-status with no token = %d, want 401", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/deductor-status/2025", otherTok, body); rec.Code != http.StatusForbidden {
		t.Errorf("PUT deductor-status as another tenant = %d, want 403", rec.Code)
	}
	// ⚠️ THE DECISION. Data Entry's default_access is 'none' and no seeded rule
	// names `deductor-status`, so it is refused — and that is deliberate rather
	// than incidental. Flipping the category from INDIVIDUAL to COMPANY starts
	// withholding tax out of every supplier payment for the year.
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/deductor-status/2025", dataEntry, body); rec.Code != http.StatusForbidden {
		t.Errorf("PUT deductor-status as Data Entry = %d, want 403 — recording the "+
			"deductor status decides a year of withholding and belongs to whoever "+
			"signs the return", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/deductor-status/2025", ownerTok, body); rec.Code != http.StatusOK {
		t.Errorf("PUT deductor-status as Owner = %d, want 200", rec.Code)
	}

	// --- The READ. Same token, so the same three answers. ------------------
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/deductor-status", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET deductor-status with no token = %d, want 401", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/deductor-status", otherTok, nil); rec.Code != http.StatusForbidden {
		t.Errorf("GET deductor-status as another tenant = %d, want 403", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/deductor-status", dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("GET deductor-status as Data Entry = %d, want 403 — read and write "+
			"share one resource token, so they are granted or refused together", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/deductor-status", ownerTok, nil); rec.Code != http.StatusOK {
		t.Errorf("GET deductor-status as Owner = %d, want 200", rec.Code)
	}
}

// TestDeductorStatusRoundTrip covers the shape a screen depends on: what goes in
// comes back, the derived governing year is stated, and the category vocabulary
// is served rather than hard-coded in the client.
func TestDeductorStatusRoundTrip(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	rec, body := aclRequest(t, s, http.MethodPut, base+"/deductor-status/2025", ownerTok,
		map[string]any{"entity_category": "COMPANY", "recorded_by": "owner"})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	// The off-by-one, stated on the way in as well as the way out.
	if body["governs_payments_in_label"] != "2026-27" {
		t.Errorf("governs_payments_in_label = %v, want 2026-27 — the 2025-26 row "+
			"decides payments made in 2026-27", body["governs_payments_in_label"])
	}

	rec, body = aclRequest(t, s, http.MethodGet, base+"/deductor-status", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200", rec.Code)
	}
	rows, _ := body["deductor_statuses"].([]any)
	if len(rows) != 1 {
		t.Fatalf("want 1 recorded year, got %d", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	if row["tax_year_label"] != "2025-26" {
		t.Errorf("tax_year_label = %v, want 2025-26", row["tax_year_label"])
	}
	if row["entity_category"] != "COMPANY" {
		t.Errorf("entity_category = %v, want COMPANY", row["entity_category"])
	}
	// A COMPANY is liable whatever its turnover, so no figure may come back. An
	// empty string and not "0": zero would assert it turned over nothing.
	if row["prior_year_turnover_rupees"] != "" {
		t.Errorf("prior_year_turnover_rupees = %v, want empty — a COMPANY records "+
			"no turnover, and 0 would be a different and false claim",
			row["prior_year_turnover_rupees"])
	}
	// The picker vocabulary, served so the client cannot offer a category the
	// database would refuse.
	cats, _ := body["entity_categories"].([]any)
	if len(cats) != 15 {
		t.Errorf("entity_categories has %d entries, want the 15 of s.402(11)(a)–(l)", len(cats))
	}

	// PUT again for the same year CORRECTS it rather than adding a second row —
	// two rows would make "was this company required to deduct in 2026-27?"
	// unanswerable.
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/deductor-status/2025", ownerTok,
		map[string]any{
			"entity_category":            "INDIVIDUAL",
			"prior_year_turnover_rupees": "4000000",
			"prior_year_activity":        "BUSINESS",
			"recorded_by":                "owner",
		}); rec.Code != http.StatusOK {
		t.Fatalf("corrective PUT = %d, want 200", rec.Code)
	}
	_, body = aclRequest(t, s, http.MethodGet, base+"/deductor-status", ownerTok, nil)
	rows, _ = body["deductor_statuses"].([]any)
	if len(rows) != 1 {
		t.Fatalf("after correcting the same year: want 1 row, got %d", len(rows))
	}
	row, _ = rows[0].(map[string]any)
	if row["entity_category"] != "INDIVIDUAL" || row["prior_year_turnover_rupees"] != "4000000.0000" {
		t.Errorf("correction did not take: %v", row)
	}
}

// TestDeductorStatusRefusesAnUnanswerableRow pins the validation that turns a
// CHECK-constraint violation into a sentence an operator can act on. Each case
// is a row the table would refuse anyway — the point is WHICH message arrives.
func TestDeductorStatusRefusesAnUnanswerableRow(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			// The one that matters: without both operands neither test can be
			// evaluated, and the row would be a silent "unknown" masquerading as
			// a recorded status.
			name: "an individual with no turnover recorded",
			body: map[string]any{"entity_category": "INDIVIDUAL", "recorded_by": "owner"},
			want: "turnover test",
		},
		{
			// The mirror, and the less obvious half: a figure here would imply a
			// test that is never applied to a company.
			name: "a company with a turnover recorded",
			body: map[string]any{
				"entity_category": "COMPANY", "prior_year_turnover_rupees": "4000000",
				"prior_year_activity": "BUSINESS", "recorded_by": "owner",
			},
			want: "WHATEVER its turnover",
		},
		{
			name: "turnover without the activity that selects its limit",
			body: map[string]any{
				"entity_category": "INDIVIDUAL", "prior_year_turnover_rupees": "4000000",
				"recorded_by": "owner",
			},
			want: "DIFFERENT limits",
		},
		{
			name: "a category outside s.402(11)(a)-(l)",
			body: map[string]any{"entity_category": "PARTNERSHIP", "recorded_by": "owner"},
			want: "deductor categories",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, _ := aclRequest(t, s, http.MethodPut, base+"/deductor-status/2025", ownerTok, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("= %d, want 400 (%s)", rec.Code, rec.Body.String())
			}
			// A 400 alone proves nothing — every malformed body is one. Assert
			// the operator is told which fact is missing.
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("400 for the wrong reason: want a message mentioning %q, got %s",
					tc.want, rec.Body.String())
			}
		})
	}
}

// TestDeductorStatusRejectsAYearBeforeTheAct pins the lower bound. 2024-25
// predates the 2025 Act, so its entity categories cannot describe that year.
func TestDeductorStatusRejectsAYearBeforeTheAct(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	rec, _ := aclRequest(t, s, http.MethodPut, base+"/deductor-status/2024", ownerTok,
		map[string]any{"entity_category": "COMPANY", "recorded_by": "owner"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT for 2024-25 = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

// TestRecordingTheStatusUnblocksTDSPosting IS THE POINT OF THIS SLICE.
//
// Step 1 made the post path refuse every TDS voucher for a company whose
// liability is unrecorded. Nothing but this route writes that row. So the
// feature is delivered only if an operator can go from "refused" to "posted"
// WITHOUT touching the database — and that is exactly what this test does,
// entirely over HTTP.
//
// The two arms bracket the change: the identical voucher is refused before and
// accepted after. Without the first arm the test would pass against a build that
// never refused anything.
func TestRecordingTheStatusUnblocksTDSPosting(t *testing.T) {
	s := payrollServer(t)
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
	expense := newLedger("Professional Fees DS", "Indirect Expenses")
	tdsPay := newLedger("TDS Payable DS", "Duties & Taxes")
	creditor := newLedger("Consultant DS", "Sundry Creditors")

	voucher := map[string]any{
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
	}

	// BEFORE. Liability unrecorded, so the post is refused — and refused for
	// THAT reason, not some other 422.
	rec, _ := aclRequest(t, s, http.MethodPost, base+"/vouchers/purchase", ownerTok, voucher)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("before recording the status, the TDS post = %d, want 422 (%s)",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "deductor status") {
		t.Fatalf("refused for the wrong reason: %s", rec.Body.String())
	}

	// THE FIX, over the wire. A payment in 2026-27 is governed by the 2025-26
	// row — the preceding tax year, s.402(37)(b).
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/deductor-status/2025", ownerTok,
		map[string]any{"entity_category": "COMPANY", "recorded_by": "owner"}); rec.Code != http.StatusOK {
		t.Fatalf("record deductor status = %d (%s)", rec.Code, rec.Body.String())
	}

	// AFTER. The IDENTICAL voucher now posts and deducts.
	rec, body := aclRequest(t, s, http.MethodPost, base+"/vouchers/purchase", ownerTok, voucher)
	if rec.Code != http.StatusCreated {
		t.Fatalf("after recording the status, the same voucher = %d, want 201 — "+
			"recording the deductor status is the ONLY thing standing between an "+
			"operator and a TDS voucher (%s)", rec.Code, rec.Body.String())
	}
	tds, _ := body["tds"].(map[string]any)
	if tds["tds"] != "20000.0000" {
		t.Fatalf("derived tds = %v, want 20000.0000", tds["tds"])
	}
}

// TestRecordingTheWrongYearDoesNotUnblock is the control for the test above, and
// it pins the off-by-one at the seam a real operator will hit.
//
// Recording 2026-27 for a 2026-27 payment feels right and is wrong: the turnover
// test reads "the tax year IMMEDIATELY PRECEDING". If the route and the post
// path ever disagree about which year to write and read, this fails while
// everything else stays green.
func TestRecordingTheWrongYearDoesNotUnblock(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	newLedger := func(name, group string) string {
		_, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok,
			map[string]any{"name": name, "group_name": group, "created_by": "owner"})
		id, _ := body["ledger_id"].(string)
		return id
	}
	expense := newLedger("Professional Fees WY", "Indirect Expenses")
	tdsPay := newLedger("TDS Payable WY", "Duties & Taxes")
	creditor := newLedger("Consultant WY", "Sundry Creditors")

	// The year of the payment itself — the intuitive, wrong choice.
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/deductor-status/2026", ownerTok,
		map[string]any{"entity_category": "COMPANY", "recorded_by": "owner"}); rec.Code != http.StatusOK {
		t.Fatalf("record 2026-27 status = %d", rec.Code)
	}

	rec, _ := aclRequest(t, s, http.MethodPost, base+"/vouchers/purchase", ownerTok, map[string]any{
		"voucher_date":    "2026-05-10",
		"party_ledger_id": creditor,
		"created_by":      "owner",
		"postings": []any{
			map[string]any{"ledger_id": expense, "dr_cr": "D", "amount": "200000.0000"},
			map[string]any{"ledger_id": tdsPay, "dr_cr": "C", "amount": "20000.0000"},
			map[string]any{"ledger_id": creditor, "dr_cr": "C", "amount": "180000.0000"},
		},
		"tds": map[string]any{
			"section": "194J-P", "deductee_ledger_id": creditor, "deductee_type": "OTHER",
			"pan_available": true, "taxable_amount": "200000.0000", "tds_ledger_id": tdsPay,
		},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a status recorded for the PAYMENT's own year must not satisfy the "+
			"preceding-year test; = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}
