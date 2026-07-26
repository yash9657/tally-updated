package server_test

// HTTP contract tests for the migration-0015 company profile: the mailing
// block, country, BOOKS BEGINNING FROM, and the base-currency presentation
// fields.
//
// The one that carries real meaning is books_beginning_from. It is a SEPARATE
// date from fy_start_date -- the financial year begins 1 April, but a business
// migrating in mid-year has books that begin later, and its opening balances
// are stated as at THAT date. Three properties are proven here:
//
//   1. round-trip: what POST /v1/companies accepts, GET returns.
//   2. default: omitting books_beginning_from lands it on fy_start_date, via
//      the schema trigger (not just the Go constructor), so a raw INSERT is
//      covered too.
//   3. invariant: books may not begin BEFORE the year opens.

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
)

// createCompanyBody POSTs an arbitrary company body and returns the response.
func createCompanyBody(t *testing.T, srv *server.Server, token string, body map[string]any) (int, map[string]any) {
	t.Helper()
	rec, resp := doRaw(t, srv, http.MethodPost, "/v1/companies", token, body)
	return rec.Code, resp
}

// TestCompanyProfileRoundTrip proves every 0015 field survives create -> read.
func TestCompanyProfileRoundTrip(t *testing.T) {
	srv := newServer(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")
	companyID := uuid.NewString()

	// A MID-YEAR MIGRATION: the financial year opens 1 April, but these books
	// begin 1 October. This is the case the two dates exist to distinguish.
	code, _ := createCompanyBody(t, srv, owner, map[string]any{
		"id":                   companyID,
		"name":                 "Profile Co",
		"fy_start_date":        "2025-04-01",
		"books_beginning_from": "2025-10-01",
		"base_currency":        "INR",
		"state_code":           "29",
		"gstin":                "29AACCD1234K1Z8",
		"mailing_name":         "Profile Co Exports",
		"address":              "14, 3rd Cross\nIndustrial Layout",
		"city":                 "Bengaluru",
		"pincode":              "560095",
		"country":              "India",
		"currency_symbol":      "₹",
		"currency_formal_name": "Indian Rupees",
		"decimal_places":       2,
	})
	if code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201", code)
	}

	rec, body := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID, owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET company status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct{ field, want string }{
		{"fy_start_date", "2025-04-01"},
		{"books_beginning_from", "2025-10-01"},
		{"mailing_name", "Profile Co Exports"},
		{"address", "14, 3rd Cross\nIndustrial Layout"},
		{"city", "Bengaluru"},
		{"pincode", "560095"},
		{"country", "India"},
		{"currency_symbol", "₹"},
		{"currency_formal_name", "Indian Rupees"},
	} {
		if got, _ := body[tc.field].(string); got != tc.want {
			t.Errorf("GET company %s = %q, want %q", tc.field, got, tc.want)
		}
	}
	if dp, _ := body["decimal_places"].(float64); dp != 2 {
		t.Errorf("decimal_places = %v, want 2", body["decimal_places"])
	}
	// The whole point: the two dates are genuinely independent, not aliases.
	if body["books_beginning_from"] == body["fy_start_date"] {
		t.Error("books_beginning_from tracked fy_start_date despite being set apart from it")
	}
}

// TestCompanyProfileDefaults proves an omitted profile lands on the documented
// defaults, and specifically that books_beginning_from falls back to
// fy_start_date rather than to a hardcoded date or NULL.
func TestCompanyProfileDefaults(t *testing.T) {
	srv := newServer(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")
	companyID := uuid.NewString()

	code, _ := createCompanyBody(t, srv, owner, map[string]any{
		"id":            companyID,
		"name":          "Bare Co",
		"fy_start_date": "2024-04-01",
	})
	if code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201", code)
	}

	_, body := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID, owner, nil)
	for _, tc := range []struct{ field, want string }{
		{"books_beginning_from", "2024-04-01"}, // == fy_start_date
		{"country", "India"},
		{"currency_symbol", "₹"},
		{"currency_formal_name", "Indian Rupees"},
		{"mailing_name", ""},
		{"address", ""},
	} {
		if got, _ := body[tc.field].(string); got != tc.want {
			t.Errorf("default %s = %q, want %q", tc.field, got, tc.want)
		}
	}
	if dp, _ := body["decimal_places"].(float64); dp != 2 {
		t.Errorf("default decimal_places = %v, want 2", body["decimal_places"])
	}
}

// TestBooksBeginningDefaultIsSchemaLevel proves the fallback lives in the
// SCHEMA, not only in identity.CreateCompany: a RAW insert that names neither
// books_beginning_from nor any profile column still lands on a correct,
// non-NULL row. This is what keeps existing fixtures, imports and the sync path
// working without every call site learning about the new column.
func TestBooksBeginningDefaultIsSchemaLevel(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	companyID := uuid.New()
	if _, err := conn.Exec(ctx,
		`INSERT INTO company (id, company_id, name, fy_start_date)
		 VALUES ($1, $1, $2, $3)`,
		companyID, "Raw Insert Co", "2023-04-01"); err != nil {
		t.Fatalf("raw insert must succeed without naming books_beginning_from: %v", err)
	}

	var books, fy string
	if err := conn.QueryRow(ctx,
		`SELECT to_char(books_beginning_from, 'YYYY-MM-DD'),
		        to_char(fy_start_date, 'YYYY-MM-DD')
		   FROM company WHERE id = $1`, companyID).Scan(&books, &fy); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if books != fy {
		t.Fatalf("raw-insert books_beginning_from = %s, want fy_start_date %s", books, fy)
	}
}

// TestBooksCannotBeginBeforeFinancialYear proves the invariant is enforced by
// the database, so no code path can record books that begin before the year
// they belong to.
func TestBooksCannotBeginBeforeFinancialYear(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	companyID := uuid.New()
	_, err = conn.Exec(ctx,
		`INSERT INTO company (id, company_id, name, fy_start_date, books_beginning_from)
		 VALUES ($1, $1, $2, $3, $4)`,
		companyID, "Backdated Co", "2025-04-01", "2025-03-01")
	if err == nil {
		t.Fatal("books_beginning_from BEFORE fy_start_date was accepted; " +
			"the company_books_begin_within_fy CHECK is missing or wrong")
	}
}
