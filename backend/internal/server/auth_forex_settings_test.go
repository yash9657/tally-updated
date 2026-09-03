package server

// auth_forex_settings_test.go — the HTTP half of T8 step 5's setter.
//
// ⛔⛔ THE FIRST TEST HERE IS THE ONLY THING IN THE REPOSITORY THAT CAN CATCH A
// MISSING `routeAction` ARM, and the memo names that gap as registration fact 2:
// a new {companyID} route with no arm compiles, passes every Go test in
// internal/masters and internal/ledger — those call the SERVICE, not the route —
// and is 403 FOR THE OWNER, because an unmapped route default-denies. The Owner
// case below is that assertion, and it is why it is written first.

import (
	"net/http"
	"strings"
	"testing"
)

// aclForexLedger creates one ledger through the real route and returns its id, so
// the group NATURE this setter checks is the one the seeder actually assigned rather
// than one a test invented.
func aclForexLedger(t *testing.T, s *Server, base, ownerTok, name, group string) string {
	t.Helper()
	rec, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok, map[string]any{
		"name": name, "group_name": group, "created_by": "owner",
	})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create ledger %q: %d (%s)", name, rec.Code, rec.Body.String())
	}
	id, _ := body["ledger_id"].(string)
	if id == "" {
		t.Fatalf("create ledger %q returned no ledger_id (%v)", name, body)
	}
	return id
}

// TestForexSettingsAuthMatrix pins all four answers for both halves.
//
// ⭐ THE DATA ENTRY ANSWER IS THE DESIGN DECISION, AND IT IS FREE. A bare
// `forex-settings` resource token is default-DENIED for the seeded Data Entry levels
// (default_access 'none', and no seeded rule names it), so this authority needs NO
// ACL migration at all. Spelled `report:forex-settings` it would instead be GRANTED
// to every one of them by migration 0011's coarse `allow display report` at
// specificity 1 (defect D95) — a clerk could then read, and a second rule could
// easily be read as granting, the choice of where a company's realised gains land.
func TestForexSettingsAuthMatrix(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)
	otherTok := aclToken(t, s, "acl-outsider-"+companyID[:8])

	// Clearing the choice is a valid body and needs no ledger to exist, so the
	// matrix can be driven with it and asserts authorisation only.
	body := map[string]any{"gain_loss_ledger_id": ""}

	// --- The WRITE. -------------------------------------------------------
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/forex-settings", "", body); rec.Code != http.StatusUnauthorized {
		t.Errorf("PUT forex-settings with no token = %d, want 401", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/forex-settings", otherTok, body); rec.Code != http.StatusForbidden {
		t.Errorf("PUT forex-settings as another tenant = %d, want 403", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/forex-settings", dataEntry, body); rec.Code != http.StatusForbidden {
		t.Errorf("PUT forex-settings as Data Entry = %d, want 403 — this setting decides "+
			"which ledger a company's realised exchange gains and losses land in, which "+
			"is a Profit & Loss decision and not a clerical one", rec.Code)
	}
	if rec, out := aclRequest(t, s, http.MethodPut, base+"/forex-settings", ownerTok, body); rec.Code != http.StatusNoContent {
		t.Errorf("PUT forex-settings as Owner = %d, want 204 (%v) — ⛔ A 403 HERE IS THE "+
			"MISSING routeAction ARM, not an ACL rule: an unmapped {companyID} route "+
			"default-denies, and nothing else in this repository would tell you",
			rec.Code, out)
	}

	// --- The READ. One token, so the same four answers. --------------------
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/forex-settings", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET forex-settings with no token = %d, want 401", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/forex-settings", otherTok, nil); rec.Code != http.StatusForbidden {
		t.Errorf("GET forex-settings as another tenant = %d, want 403", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/forex-settings", dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("GET forex-settings as Data Entry = %d, want 403 — read and write share "+
			"one resource token, so they are granted or refused together", rec.Code)
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, base+"/forex-settings", ownerTok, nil); rec.Code != http.StatusOK {
		t.Errorf("GET forex-settings as Owner = %d, want 200 — ⛔ or the routeAction arm "+
			"is missing", rec.Code)
	}
}

// TestForexSettingsGETIsA200BeforeAnyChoiceIsMade covers the state every company in
// the field is in and every screen opens in: no company_forex_settings row at all.
//
// ⛔ A 404 WOULD BE WRONG, and not merely unhelpful. "No forex ledger chosen" is the
// correct and ordinary state of every company that never trades in a foreign
// currency, so reporting it the way a mistyped URL is reported would make the F11
// screen render an error for the common case — and a client would have no way to tell
// "you have not chosen one" from "this endpoint does not exist".
func TestForexSettingsGETIsA200BeforeAnyChoiceIsMade(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	rec, body := aclRequest(t, s, http.MethodGet, base+"/forex-settings", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET with no choice recorded = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	// ⛔ ABSENT, NEVER THE NIL UUID. A client that round-tripped
	// "00000000-0000-0000-0000-000000000000" back into the PUT would be sending a
	// well-formed uuid that names no ledger, and the setter would have to special-case
	// it. The empty/absent key is what the body documents as "cleared".
	if got, ok := body["gain_loss_ledger_id"]; ok && got != "" {
		t.Errorf("gain_loss_ledger_id is %v with nothing chosen, want absent or \"\" — the "+
			"nil uuid is a well-formed id that names no ledger", got)
	}
}

// TestForexSettingsRefusesALedgerThatCannotHoldADifference. ⛔ 0080's composite
// foreign key already refuses another company's ledger — that is the TENANCY
// guarantee. What it cannot refuse is a SUNDRY DEBTORS ledger, and choosing one is
// the mistake this screen invites: the realised difference would be credited to a
// customer's own balance, so a fully settled bill would show a permanent residue —
// which is exactly the silent wrong number Q3's refusal exists to prevent, arrived at
// through the setting instead of around it.
func TestForexSettingsRefusesALedgerThatCannotHoldADifference(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID

	debtor := aclForexLedger(t, s, base, ownerTok, "Globex LLC (USA)", "Sundry Debtors")
	rec, _ := aclRequest(t, s, http.MethodPut, base+"/forex-settings", ownerTok,
		map[string]any{"gain_loss_ledger_id": debtor})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("choosing a Sundry Debtors ledger = %d, want 422", rec.Code)
	}
	if b := rec.Body.String(); !strings.Contains(b, "revenue or expense") {
		t.Errorf("the refusal is %s — it must say what to choose INSTEAD, because "+
			"\"unsuitable\" leaves the operator with the same screen and no next action", b)
	}

	// ⭐ THE INVERSE CONTROL. Without it, "the debtor was refused" is equally
	// consistent with the setter refusing EVERY ledger — which is precisely what the
	// account_nature vocabulary being spelled "Income"/"Expenses" instead of
	// 'INCOME'/'EXPENSE' would do, silently, with no build or test failure anywhere.
	forexPL := aclForexLedger(t, s, base, ownerTok, "Forex Gain/Loss", "Indirect Expenses")
	rec, out := aclRequest(t, s, http.MethodPut, base+"/forex-settings", ownerTok,
		map[string]any{"gain_loss_ledger_id": forexPL})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("choosing an Indirect Expenses ledger = %d, want 204 (%v) — if EVERY "+
			"ledger is refused, the nature comparison matches nothing", rec.Code, out)
	}

	// And it reads back, by id AND by the operator's own spelling of it.
	rec, body := aclRequest(t, s, http.MethodGet, base+"/forex-settings", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET after choosing = %d, want 200", rec.Code)
	}
	if body["gain_loss_ledger_id"] != forexPL {
		t.Errorf("gain_loss_ledger_id read back as %v, want %s", body["gain_loss_ledger_id"], forexPL)
	}
	if body["gain_loss_ledger_name"] != "Forex Gain/Loss" {
		t.Errorf("gain_loss_ledger_name is %v, want \"Forex Gain/Loss\" — a screen that had "+
			"to resolve the uuid against the ledger list to show what is configured would "+
			"render a blank setting whenever that list was paged", body["gain_loss_ledger_name"])
	}

	// ⭐ AND THE CHOICE CAN BE UN-MADE. 0080 made the column nullable so that "not
	// chosen" stays expressible; an operator who picked the wrong ledger must be able
	// to clear it without a DELETE route that would also destroy the row.
	if rec, _ := aclRequest(t, s, http.MethodPut, base+"/forex-settings", ownerTok,
		map[string]any{"gain_loss_ledger_id": ""}); rec.Code != http.StatusNoContent {
		t.Fatalf("clearing the choice = %d, want 204", rec.Code)
	}
	rec, body = aclRequest(t, s, http.MethodGet, base+"/forex-settings", ownerTok, nil)
	if got, ok := body["gain_loss_ledger_id"]; ok && got != "" {
		t.Errorf("after clearing, gain_loss_ledger_id is %v, want absent or \"\"", got)
	}
}
