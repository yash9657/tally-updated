package server_test

// HTTP contract tests for company.allow_negative_stock — register rows D5 and
// D43, closed together because they are one defect seen from two ends.
//
// WHAT WAS WRONG. Migration 0025 added the column and TWO enforcers that read
// it: ledger.checkNegativeStock inside the post transaction, and the deferred
// check_stock_not_negative() constraint trigger at commit. Both honoured it
// correctly. NOTHING COULD WRITE IT — no field on the create body, no route, no
// service setter, no control — so it was false for every tenant that has ever
// existed and the refusal it gates was absolute. That is D5.
//
// D43 is the same absence seen from the caller's side, and it is the worse
// half: POST /v1/companies ACCEPTED `allow_negative_stock` and discarded it,
// answering 201. decodeJSON does not set DisallowUnknownFields (it is shared by
// ~40 handlers), so the key was dropped silently and the caller was told a
// setting had been applied that never was.
//
// ⛔ EVERY ASSERTION BELOW READS THE COLUMN, NOT THE RESPONSE. A 201, a 200, or
// a resolved call is exactly what D43 handed callers for the whole life of that
// route — the response was already "successful" while the column never moved,
// so a test written against the response would have passed against the defect.
// The oracle is `SELECT allow_negative_stock FROM company` over the ADMIN
// connection: out-of-band, and not a surface either handler owns.

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
)

// rawAllowNegativeStock reads the column out of band, over the admin DSN.
//
// THE ADMIN CONNECTION IS THE POINT. It is not subject to the RLS the handlers
// run under and it is not the code path being tested, so it cannot agree with a
// broken write for the same reason the write is broken.
func rawAllowNegativeStock(t *testing.T, conn *pgx.Conn, companyID string) bool {
	t.Helper()
	var allow bool
	if err := conn.QueryRow(context.Background(),
		`SELECT allow_negative_stock FROM company WHERE id = $1`, companyID).
		Scan(&allow); err != nil {
		t.Fatalf("read allow_negative_stock for %s: %v", companyID, err)
	}
	return allow
}

// TestCreateCompanyHonoursAllowNegativeStock is D43's half: the key the create
// route accepts must reach the column.
func TestCreateCompanyHonoursAllowNegativeStock(t *testing.T) {
	srv := newServer(t)
	conn := adminConn(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")

	for _, tc := range []struct {
		name string
		body map[string]any
		want bool
	}{
		{
			name: "explicit true reaches the column",
			body: map[string]any{"allow_negative_stock": true},
			want: true,
		},
		{
			// The arm that keeps the fix from being "always write true".
			name: "explicit false reaches the column",
			body: map[string]any{"allow_negative_stock": false},
			want: false,
		},
		{
			// AN OMITTED KEY TAKES THE RESTRICTIVE DEFAULT, and it must be the
			// restrictive one: an escape hatch nobody asked for must be shut.
			name: "omitted takes the column default, which is refuse",
			body: map[string]any{},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			companyID := uuid.NewString()
			body := map[string]any{
				"id":            companyID,
				"name":          "Negative Stock Co",
				"fy_start_date": "2025-04-01",
			}
			for k, v := range tc.body {
				body[k] = v
			}
			code, _ := createCompanyBody(t, srv, owner, body)
			if code != http.StatusCreated {
				t.Fatalf("create company status = %d, want 201", code)
			}

			// ⛔ THE ORACLE. Not the 201 above — that was already 201 while the
			// key was being thrown on the floor.
			if got := rawAllowNegativeStock(t, conn, companyID); got != tc.want {
				t.Errorf("company.allow_negative_stock = %v, want %v (D43: the "+
					"create answered 201 either way)", got, tc.want)
			}

			// And the READ must agree with the row, so a client can learn the
			// value of a setting it is already subject to.
			_, view := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID, owner, nil)
			if got, _ := view["allow_negative_stock"].(bool); got != tc.want {
				t.Errorf("GET allow_negative_stock = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSetCompanyFeaturesWritesAllowNegativeStock is D5's half: the write side
// that did not exist.
func TestSetCompanyFeaturesWritesAllowNegativeStock(t *testing.T) {
	srv := newServer(t)
	conn := adminConn(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")

	companyID := uuid.NewString()
	code, _ := createCompanyBody(t, srv, owner, map[string]any{
		"id":            companyID,
		"name":          "Switchable Co",
		"fy_start_date": "2025-04-01",
	})
	if code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201", code)
	}
	if rawAllowNegativeStock(t, conn, companyID) {
		t.Fatal("a fresh company must start with allow_negative_stock = false")
	}

	// ON.
	rec, body := doRaw(t, srv, http.MethodPut, "/v1/companies/"+companyID, owner,
		map[string]any{"allow_negative_stock": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got, _ := body["allow_negative_stock"].(bool); !got {
		t.Errorf("PUT answered allow_negative_stock = %v, want true", body["allow_negative_stock"])
	}
	if !rawAllowNegativeStock(t, conn, companyID) {
		t.Error("PUT answered 200 and the column did not move — the D43 shape, " +
			"now on the write route")
	}

	// OFF AGAIN. A switch that only goes one way is not a switch, and the
	// pointer-vs-bool distinction in the request type is what makes this arm
	// possible at all: with a plain bool, `false` and `omitted` are the same
	// wire value and this could not be told from the arm below.
	_, body = doRaw(t, srv, http.MethodPut, "/v1/companies/"+companyID, owner,
		map[string]any{"allow_negative_stock": false})
	if got, _ := body["allow_negative_stock"].(bool); got {
		t.Errorf("PUT answered allow_negative_stock = %v, want false", body["allow_negative_stock"])
	}
	if rawAllowNegativeStock(t, conn, companyID) {
		t.Error("turning the setting back OFF did not move the column")
	}
}

// TestSetCompanyFeaturesLeavesOmittedSettingsAlone pins the shape choice that
// keeps this route safe to extend: an ABSENT field is not a reset.
//
// It matters more than it looks. The moment a second setting lands here, a PUT
// carrying only the first would silently revert the second if omission meant
// "false" — a settings screen that quietly undoes settings it was not asked
// about is a worse defect than the one this route closes.
func TestSetCompanyFeaturesLeavesOmittedSettingsAlone(t *testing.T) {
	srv := newServer(t)
	conn := adminConn(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")

	companyID := uuid.NewString()
	if code, _ := createCompanyBody(t, srv, owner, map[string]any{
		"id":                   companyID,
		"name":                 "Sticky Co",
		"fy_start_date":        "2025-04-01",
		"allow_negative_stock": true,
	}); code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201", code)
	}

	// An EMPTY features body. Under a plain-bool request type this would decode
	// to false and switch the setting off.
	rec, body := doRaw(t, srv, http.MethodPut, "/v1/companies/"+companyID, owner,
		map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT {} status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got, _ := body["allow_negative_stock"].(bool); !got {
		t.Errorf("PUT {} answered allow_negative_stock = %v, want the unchanged true", body["allow_negative_stock"])
	}
	if !rawAllowNegativeStock(t, conn, companyID) {
		t.Error("a PUT that mentioned no setting reset one anyway")
	}
}

// TestSetCompanyFeaturesIsDeniedToDataEntry pins the ACL reading that let this
// route ship WITHOUT a migration.
//
// PUT / maps to (alter, company). Migration 0022 grants a seeded Data Entry
// level `allow display company` and nothing wider, and that level's
// default_access is 'none' — so the write is default-DENIED while the read
// still works. If a later change re-tokenises this route (say to something
// reached by 0011's coarse `allow display report`), this test is what fails
// instead of the inventory escape hatch quietly becoming operator-writable.
func TestSetCompanyFeaturesIsDeniedToDataEntry(t *testing.T) {
	srv := newServer(t)
	conn := adminConn(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")

	companyID := uuid.NewString()
	if code, _ := createCompanyBody(t, srv, owner, map[string]any{
		"id":            companyID,
		"name":          "Levels Co",
		"fy_start_date": "2025-04-01",
	}); code != http.StatusCreated {
		t.Fatalf("create company status = %d, want 201", code)
	}

	clerk := addDataEntryMember(t, srv, owner, companyID)

	// The READ is granted...
	rec, view := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID, clerk, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Data Entry GET company = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got, ok := view["allow_negative_stock"].(bool); !ok || got {
		t.Errorf("Data Entry read allow_negative_stock = %v, want false", view["allow_negative_stock"])
	}

	// ...and the WRITE is not.
	rec, _ = doRaw(t, srv, http.MethodPut, "/v1/companies/"+companyID, clerk,
		map[string]any{"allow_negative_stock": true})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Data Entry PUT company = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	// ⛔ AND THE COLUMN DID NOT MOVE. A 403 that arrived after the write would
	// be the same class of lie as D43's 201.
	if rawAllowNegativeStock(t, conn, companyID) {
		t.Error("the PUT was refused with 403 and the column moved anyway")
	}
}

// addDataEntryMember invites a fresh subject at the SEEDED 'Data Entry' level
// and claims the invite, returning that member's token.
//
// THE SEEDED LEVEL, NEVER A HAND-WRITTEN ONE. The whole claim being tested is
// about what migration 0022 grants the level every tenant ships with; a level
// written here would test this test's own opinion of it.
//
// ⛔ AND THE TOKEN CARRIES amr:["webauthn"] ON PURPOSE, even though the seeded
// Data Entry level does not require step-up. It removes the only other reason a
// write could 403 — so when the PUT is refused, the ACL is the sole remaining
// explanation rather than the most likely one.
func addDataEntryMember(t *testing.T, srv *server.Server, ownerTok, companyID string) string {
	t.Helper()
	subject := "d5-de-" + uuid.NewString()[:8]
	rec, _ := doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/members/invites",
		ownerTok, map[string]any{"subject": subject, "security_level_name": "Data Entry"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite at Data Entry = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	tok := mintToken(t, srv, subject, "webauthn")
	rec, body := doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim invite = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if claimed, _ := body["claimed_invites"].([]any); len(claimed) != 1 {
		t.Fatalf("claimed_invites = %v, want exactly one (%s)", claimed, rec.Body.String())
	}
	return tok
}
