package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/server"
)

// recordDeductorStatusHTTP writes migration 0049's row for a company.
//
// It goes in over an ADMIN connection rather than through a route because no
// route records this yet — that is Item Zero step 2. Writing it here rather than
// waiting keeps the post-path tests unblocked, and the admin role bypasses RLS
// so no company context is needed.
func recordDeductorStatusHTTP(t *testing.T, companyID string, taxYearStart int, category string) {
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

// THE HTTP SEAM FOR TDS — the layer that had no test at all.
//
// voucher_tds.go's own header records why that matters: a stale comment there
// once claimed the path was dead, and "not a live path at all" is exactly the
// sentence that stops a reviewer looking. The path was in fact live and
// unverified. This file verifies it.
//
// What is actually at risk here is one line of field-copying in buildTDS. If
// `deductee_ledger_id` were parsed and then not assigned, EVERY TDS voucher
// posted over HTTP would be refused with ErrDeducteeUnknown — and no Go-level
// test would notice, because the ledger tests construct TDSParams directly.

// tdsHTTPFixture is a company with the three ledgers a TDS voucher needs and a
// recorded deductor status, all created through the public API where one exists.
type tdsHTTPFixture struct {
	base     string
	tok      string
	expense  string
	tdsPay   string
	creditor string
}

func newTDSHTTPFixture(t *testing.T, srv *server.Server) tdsHTTPFixture {
	t.Helper()
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	base := "/v1/companies/" + companyID
	f := tdsHTTPFixture{
		base:     base,
		tok:      ownerTok,
		expense:  createLedgerAs(t, srv, base, ownerTok, "Professional Fees HTTP", "Indirect Expenses"),
		tdsPay:   createLedgerAs(t, srv, base, ownerTok, "TDS Payable HTTP", "Duties & Taxes"),
		creditor: createLedgerAs(t, srv, base, ownerTok, "Consultant HTTP", "Sundry Creditors"),
	}
	// The voucher below is dated in 2026-27, so the status that governs it is the
	// PRECEDING year's. Written directly because no route records it yet — that
	// is Item Zero step 2, and this test is deliberately not blocked on it.
	recordDeductorStatusHTTP(t, companyID, 2025, "COMPANY")
	return f
}

// TestPostVoucherWithTDSOverHTTP is the end-to-end proof that the `tds` block
// reaches the engine and comes back with a derived deduction: ₹2,00,000 of
// professional fees under 194J-P at 10% -> ₹20,000.
func TestPostVoucherWithTDSOverHTTP(t *testing.T) {
	srv := newServer(t)
	f := newTDSHTTPFixture(t, srv)

	rec, body := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/purchase", f.tok,
		map[string]any{
			"voucher_date":    "2026-05-10",
			"party_ledger_id": f.creditor,
			"created_by":      "owner",
			"postings": []any{
				map[string]any{"ledger_id": f.expense, "dr_cr": "D", "amount": "200000.0000"},
				map[string]any{"ledger_id": f.tdsPay, "dr_cr": "C", "amount": "20000.0000"},
				map[string]any{"ledger_id": f.creditor, "dr_cr": "C", "amount": "180000.0000"},
			},
			"tds": map[string]any{
				"section":            "194J-P",
				"deductee_ledger_id": f.creditor,
				"deductee_type":      "OTHER",
				"pan_available":      true,
				"taxable_amount":     "200000.0000",
				"tds_ledger_id":      f.tdsPay,
			},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST purchase with a tds block = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	tds, ok := body["tds"].(map[string]any)
	if !ok {
		t.Fatalf("response carries no tds block: %v", body)
	}
	if got := tds["tds"]; got != "20000.0000" {
		t.Errorf("derived tds over HTTP = %v, want 20000.0000", got)
	}
}

// TestTDSOverHTTPWithoutDeducteeIs422 is the non-vacuity gate for the field
// above, and it is the one that catches buildTDS forgetting to assign it.
//
// The body is IDENTICAL to the passing case except that deductee_ledger_id is
// omitted. If the field were dropped on the floor, the happy-path test would
// fail instead — but if it were assigned from the wrong source (say, always the
// party) this test would still pass while the happy path did too, so both arms
// are needed.
func TestTDSOverHTTPWithoutDeducteeIs422(t *testing.T) {
	srv := newServer(t)
	f := newTDSHTTPFixture(t, srv)

	rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/purchase", f.tok,
		map[string]any{
			"voucher_date":    "2026-05-10",
			"party_ledger_id": f.creditor,
			"created_by":      "owner",
			"postings": []any{
				map[string]any{"ledger_id": f.expense, "dr_cr": "D", "amount": "200000.0000"},
				map[string]any{"ledger_id": f.tdsPay, "dr_cr": "C", "amount": "20000.0000"},
				map[string]any{"ledger_id": f.creditor, "dr_cr": "C", "amount": "180000.0000"},
			},
			"tds": map[string]any{
				"section":        "194J-P",
				"deductee_type":  "OTHER",
				"pan_available":  true,
				"taxable_amount": "200000.0000",
				"tds_ledger_id":  f.tdsPay,
			},
		})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a tds block with no deductee = %d, want 422 — the aggregate is "+
			"per deductee and the return is filed per deductee (%s)",
			rec.Code, rec.Body.String())
	}
	// ⚠️ THE STATUS CODE ALONE IS NOT ENOUGH. Every domain refusal on this route
	// is a 422, so a body that is malformed in some UNRELATED way passes a bare
	// code check — an earlier draft of this test was green while actually sending
	// an invalid dr_cr. Assert the reason.
	if !strings.Contains(rec.Body.String(), "deductee") {
		t.Fatalf("422 for the wrong reason: %s", rec.Body.String())
	}
}

// TestTDSOverHTTPWithoutDeductorStatusIs422 proves the s.402(11)/(37) refusal is
// reachable by a real client and does not surface as a 500. An operator who has
// not told the system what kind of entity they are gets a message saying so, not
// a silently untaxed payment.
func TestTDSOverHTTPWithoutDeductorStatusIs422(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	base := "/v1/companies/" + companyID
	expense := createLedgerAs(t, srv, base, ownerTok, "Professional Fees NS", "Indirect Expenses")
	tdsPay := createLedgerAs(t, srv, base, ownerTok, "TDS Payable NS", "Duties & Taxes")
	creditor := createLedgerAs(t, srv, base, ownerTok, "Consultant NS", "Sundry Creditors")
	// No RecordDeductorStatus call: this company's liability is unrecorded.

	rec, _ := doRaw(t, srv, http.MethodPost, base+"/vouchers/purchase", ownerTok,
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
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a TDS post by a company with no recorded deductor status = %d, "+
			"want 422 (%s)", rec.Code, rec.Body.String())
	}
	// Same trap as above: check WHICH refusal this is.
	if !strings.Contains(rec.Body.String(), "deductor status") {
		t.Fatalf("422 for the wrong reason: %s", rec.Body.String())
	}
}
