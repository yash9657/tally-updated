package server_test

// The party block on POST/GET .../ledgers — register row D87's server half.
//
// D87 is mostly a CLIENT row: the server has accepted `gstin`, `state_code`,
// `registration_type` and `pan` since they were written, and the form rendered
// none of them. But firing the wire at the server before writing the form (as
// §4.1 requires) turned up two things the recon did not have, and neither is a
// client problem:
//
//   1. `credit_days` was ACCEPTED AT 201 AND DISCARDED. masters.Ledger.CreditDays
//      existed and the INSERT bound it, but `ledgerReq` had no field to fill it
//      from, so it was nil on every create. That is D43's shape on a second
//      route, and a form field for it would have been a lie.
//   2. `pan` and `credit_days` were NOT READABLE BACK. GET .../ledgers omitted
//      both, so a party's PAN could be written and never seen again through
//      HTTP — which is also why D87 could report "(null)" for a field that was
//      in fact stored.
//
// ⛔ THE ORACLE IS THE COLUMN, NOT THE 201. A create answering 201 is exactly
// what the discarded `credit_days` did for the whole life of the route.

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// rawPartyBlock reads the four party columns out of band, over the admin DSN.
func rawPartyBlock(t *testing.T, conn *pgx.Conn, companyID, name string) (gstin, stateCode, regType, pan string, creditDays *int) {
	t.Helper()
	var g, sc, rt, p *string
	if err := conn.QueryRow(context.Background(),
		`SELECT gstin, state_code, registration_type::text, pan, credit_days
		   FROM ledger WHERE company_id = $1 AND name = $2`,
		companyID, name).Scan(&g, &sc, &rt, &p, &creditDays); err != nil {
		t.Fatalf("read party block for %q: %v", name, err)
	}
	deref := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	return deref(g), deref(sc), deref(rt), deref(p), creditDays
}

// TestCreateLedgerCarriesTheWholePartyBlock is the D43-shaped half: every key
// the route accepts must reach its column.
func TestCreateLedgerCarriesTheWholePartyBlock(t *testing.T) {
	srv := newServer(t)
	conn := adminConn(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")

	companyID := uuid.NewString()
	if code, _ := createCompanyBody(t, srv, owner, map[string]any{
		"id":            companyID,
		"name":          "Party Block Co",
		"fy_start_date": "2025-04-01",
		"state_code":    "29",
	}); code != http.StatusCreated {
		t.Fatalf("create company = %d, want 201", code)
	}

	rec, _ := doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/ledgers", owner,
		map[string]any{
			"name":              "Mumbai Metals",
			"group_name":        "Sundry Creditors",
			"gstin":             "27AACCM9012M1Z7",
			"state_code":        "27",
			"registration_type": "REGULAR",
			"pan":               "AACCM9012M",
			"credit_days":       45,
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create ledger = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	gstin, sc, rt, pan, credit := rawPartyBlock(t, conn, companyID, "Mumbai Metals")
	if gstin != "27AACCM9012M1Z7" || sc != "27" || rt != "REGULAR" || pan != "AACCM9012M" {
		t.Errorf("party block = %q|%q|%q|%q, want the four values sent", gstin, sc, rt, pan)
	}
	// ⛔ THE ONE THAT WAS BROKEN. The four above already worked; this key was
	// accepted and thrown away, and the 201 above said nothing about it.
	if credit == nil || *credit != 45 {
		t.Errorf("credit_days = %v, want 45 — the create answered 201 either way", credit)
	}
}

// TestCreateLedgerDistinguishesNoTermFromZeroDays pins the pointer.
//
// "No term agreed" and "due immediately" are different facts about a party and
// they age differently. A plain int would collapse them, and every party nobody
// stated a term for would read as a cash party in an ageing report.
func TestCreateLedgerDistinguishesNoTermFromZeroDays(t *testing.T) {
	srv := newServer(t)
	conn := adminConn(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")

	companyID := uuid.NewString()
	if code, _ := createCompanyBody(t, srv, owner, map[string]any{
		"id": companyID, "name": "Terms Co", "fy_start_date": "2025-04-01",
	}); code != http.StatusCreated {
		t.Fatal("create company failed")
	}
	base := "/v1/companies/" + companyID + "/ledgers"

	for _, tc := range []struct {
		name string
		body map[string]any
		want *int
	}{
		{"omitted is NO TERM (NULL)", map[string]any{}, nil},
		{"explicit 0 is DUE IMMEDIATELY", map[string]any{"credit_days": 0}, intPtr(0)},
		{"a real term survives", map[string]any{"credit_days": 30}, intPtr(30)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ledgerName := "Terms " + uuid.NewString()[:8]
			body := map[string]any{"name": ledgerName, "group_name": "Sundry Debtors"}
			for k, v := range tc.body {
				body[k] = v
			}
			rec, _ := doRaw(t, srv, http.MethodPost, base, owner, body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
			}
			_, _, _, _, got := rawPartyBlock(t, conn, companyID, ledgerName)
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("credit_days = %d, want NULL (no term agreed)", *got)
			case tc.want != nil && got == nil:
				t.Errorf("credit_days = NULL, want %d", *tc.want)
			case tc.want != nil && got != nil && *got != *tc.want:
				t.Errorf("credit_days = %d, want %d", *got, *tc.want)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

// TestListLedgersReadsBackTheWholePartyBlock closes the read side.
//
// The list item's own contract is "a client can read a ledger and re-send it",
// and it was false for `pan` and `credit_days`: both writable, neither
// readable. A form that writes fields nothing can show cannot be driven through
// the product, and a party's PAN could be stated and never seen again.
func TestListLedgersReadsBackTheWholePartyBlock(t *testing.T) {
	srv := newServer(t)
	owner := mintToken(t, srv, "owner-"+uuid.NewString(), "webauthn")

	companyID := uuid.NewString()
	if code, _ := createCompanyBody(t, srv, owner, map[string]any{
		"id": companyID, "name": "Readback Co", "fy_start_date": "2025-04-01",
	}); code != http.StatusCreated {
		t.Fatal("create company failed")
	}
	base := "/v1/companies/" + companyID + "/ledgers"

	if rec, _ := doRaw(t, srv, http.MethodPost, base, owner, map[string]any{
		"name": "Delhi Parts", "group_name": "Sundry Debtors",
		"gstin": "07AACCD7890P1Z5", "state_code": "07",
		"registration_type": "REGULAR", "pan": "AACCD7890P", "credit_days": 60,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create ledger = %d (%s)", rec.Code, rec.Body.String())
	}
	// A second party with NO term, so the null arm is exercised on the read too.
	if rec, _ := doRaw(t, srv, http.MethodPost, base, owner, map[string]any{
		"name": "No Term Co", "group_name": "Sundry Debtors",
	}); rec.Code != http.StatusCreated {
		t.Fatal("create second ledger failed")
	}

	rec, body := doRaw(t, srv, http.MethodGet, base, owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list ledgers = %d (%s)", rec.Code, rec.Body.String())
	}
	rows, _ := body["ledgers"].([]any)
	byName := map[string]map[string]any{}
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok {
			name, _ := m["name"].(string)
			byName[name] = m
		}
	}

	delhi, ok := byName["Delhi Parts"]
	if !ok {
		t.Fatal("Delhi Parts is not in the list")
	}
	if got, _ := delhi["pan"].(string); got != "AACCD7890P" {
		t.Errorf("pan = %v, want AACCD7890P (it was WRITABLE and not READABLE)", delhi["pan"])
	}
	if got, _ := delhi["credit_days"].(float64); got != 60 {
		t.Errorf("credit_days = %v, want 60", delhi["credit_days"])
	}

	// ⛔ NULL READS BACK AS null, NOT AS 0. The whole point of the pointer: a
	// JSON 0 here would tell every client that a party with no agreed term is
	// due immediately.
	noTerm, ok := byName["No Term Co"]
	if !ok {
		t.Fatal("No Term Co is not in the list")
	}
	if v, present := noTerm["credit_days"]; !present || v != nil {
		t.Errorf("credit_days for a party with no term = %v, want JSON null", v)
	}
}
