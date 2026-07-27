package server_test

// handler_admin_test.go — live-Postgres HTTP tests for the SaaS admin surface
// (members, invites, custom security levels, /v1/me).
//
// The oracle for "did the grant work?" is deliberately NOT the admin API
// reading its own writes. Every membership test ends by having the INVITED USER
// call a real business route with their own token and asserting the PDP's
// answer: a level created through POST /security-levels must actually change
// what its holder can do. That is the claim; anything less is the system
// echoing itself.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/server"
)

// newAdminCompany creates a company owned by a fresh subject and returns the
// company id, the Owner's token (WebAuthn AMR, so writes pass step-up) and the
// Owner's subject.
func newAdminCompany(t *testing.T, srv *server.Server) (companyID, ownerToken, ownerSubject string) {
	t.Helper()
	ownerSubject = "admin-owner-" + uuid.NewString()[:8]
	ownerToken = mintToken(t, srv, ownerSubject, "webauthn")
	companyID = uuid.NewString()
	rec, _ := doRaw(t, srv, http.MethodPost, "/v1/companies", ownerToken, map[string]any{
		"id":            companyID,
		"name":          "Admin Co " + companyID[:8],
		"fy_start_date": "2026-04-01",
		"base_currency": "INR",
		"state_code":    "29",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	return companyID, ownerToken, ownerSubject
}

// list pulls a []any of objects out of a decoded response body.
func list(t *testing.T, body map[string]any, key string) []map[string]any {
	t.Helper()
	raw, ok := body[key].([]any)
	if !ok {
		t.Fatalf("response has no %q array: %v", key, body)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		m, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("%s element is not an object: %v", key, e)
		}
		out = append(out, m)
	}
	return out
}

// ---------------------------------------------------------------------------
// The flagship: invite -> claim -> the PDP grants exactly what was granted.
// ---------------------------------------------------------------------------

// TestAdminInviteAcceptGrantsAccess is the whole reason this surface exists:
// a colleague gets access to a company WITHOUT anyone touching SQL, and the
// access they get is the level they were invited at — proven by having them
// exercise the business routes their level allows and one it does not.
func TestAdminInviteAcceptGrantsAccess(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, ownerSubj := newAdminCompany(t, srv)

	// The company starts with exactly one member: its creator, an Owner.
	rec, body := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/members", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list members = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	members := list(t, body, "members")
	if len(members) != 1 {
		t.Fatalf("fresh company has %d members, want 1: %v", len(members), members)
	}
	if members[0]["subject"] != ownerSubj || members[0]["is_owner"] != true || members[0]["is_self"] != true {
		t.Fatalf("sole member = %v, want the creating owner (%s) flagged is_owner+is_self", members[0], ownerSubj)
	}

	// Invite a colleague at the seeded Data Entry level, BY NAME.
	colleague := "colleague-" + uuid.NewString()[:8]
	rec, body = doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/members/invites", ownerTok,
		map[string]any{
			"subject":             colleague,
			"email":               colleague + "@example.test",
			"security_level_name": "Data Entry",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if body["status"] != "pending" {
		t.Fatalf("invite status = %v, want pending", body["status"])
	}

	// The invite is visible to the administrator, and is NOT yet a membership.
	_, body = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/members", ownerTok, nil)
	if got := len(list(t, body, "members")); got != 1 {
		t.Fatalf("members after invite = %d, want 1 (an invite is not a membership)", got)
	}
	invites := list(t, body, "invites")
	if len(invites) != 1 || invites[0]["subject"] != colleague ||
		invites[0]["security_level_name"] != "Data Entry" {
		t.Fatalf("pending invites = %v, want one for %s at Data Entry", invites, colleague)
	}

	// BEFORE claiming, the colleague is authenticated but not a member: 403.
	colleagueTok := mintToken(t, srv, colleague, "webauthn")
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/trial-balance", colleagueTok, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("pre-claim trial balance = %d, want 403 (an unclaimed invite grants nothing)", rec.Code)
	}

	// GET /v1/me claims the invite and reports the company they may now open.
	rec, body = doRaw(t, srv, http.MethodGet, "/v1/me", colleagueTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := len(list(t, body, "claimed_invites")); got != 1 {
		t.Fatalf("claimed_invites = %d, want 1 (%s)", got, rec.Body.String())
	}
	companies := list(t, body, "companies")
	if len(companies) != 1 || companies[0]["company_id"] != companyID ||
		companies[0]["security_level_name"] != "Data Entry" || companies[0]["is_owner"] != false {
		t.Fatalf("/v1/me companies = %v, want [%s @ Data Entry]", companies, companyID)
	}

	// THE ORACLE: the PDP now answers as Data Entry for this user.
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/trial-balance", colleagueTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("post-claim trial balance = %d, want 200 (Data Entry allows display report)", rec.Code)
	}
	rec, _ = doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/ledgers", colleagueTok,
		map[string]any{"name": "Nope", "group_name": "Sundry Debtors"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("data-entry create master = %d, want 403 (no master rule)", rec.Code)
	}
	// ...and administering users is itself refused for that level.
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/members", colleagueTok, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("data-entry list members = %d, want 403 (no `security` rule, default_access none)", rec.Code)
	}

	// Claiming is exactly-once: a second /v1/me claims nothing new.
	_, body = doRaw(t, srv, http.MethodGet, "/v1/me", colleagueTok, nil)
	if got := len(list(t, body, "claimed_invites")); got != 0 {
		t.Fatalf("second /v1/me claimed %d invites, want 0 (accept must be exactly-once)", got)
	}

	// The administrator now sees two members and no pending invites.
	_, body = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/members", ownerTok, nil)
	if got := len(list(t, body, "members")); got != 2 {
		t.Fatalf("members after claim = %d, want 2", got)
	}
	if got := len(list(t, body, "invites")); got != 0 {
		t.Fatalf("pending invites after claim = %d, want 0", got)
	}

	// Re-inviting an existing member is a 409 pointing at the level-change route.
	rec, body = doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/members/invites", ownerTok,
		map[string]any{"subject": colleague, "security_level_name": "Data Entry"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("re-invite an existing member = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if body["user_id"] == nil {
		t.Fatalf("409 body should name the existing user_id: %s", rec.Body.String())
	}
}

// TestAdminRevokedInviteGrantsNothing proves a revoked invite is inert: the
// invitee's /v1/me claims nothing and they stay locked out.
func TestAdminRevokedInviteGrantsNothing(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)

	subject := "revoked-" + uuid.NewString()[:8]
	_, body := doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/members/invites", ownerTok,
		map[string]any{"subject": subject, "security_level_name": "Data Entry"})
	inviteID, _ := body["invite_id"].(string)
	if inviteID == "" {
		t.Fatalf("no invite_id in %v", body)
	}

	rec, _ := doRaw(t, srv, http.MethodDelete,
		"/v1/companies/"+companyID+"/members/invites/"+inviteID, ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke invite = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	tok := mintToken(t, srv, subject, "webauthn")
	rec, body = doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/v1/me = %d, want 200", rec.Code)
	}
	if got := len(list(t, body, "claimed_invites")); got != 0 {
		t.Fatalf("claimed %d revoked invites, want 0", got)
	}
	if got := len(list(t, body, "companies")); got != 0 {
		t.Fatalf("/v1/me lists %d companies, want 0", got)
	}
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/trial-balance", tok, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("revoked invitee trial balance = %d, want 403", rec.Code)
	}
	// Revoking twice is an honest 404, not a silent success.
	rec, _ = doRaw(t, srv, http.MethodDelete,
		"/v1/companies/"+companyID+"/members/invites/"+inviteID, ownerTok, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second revoke = %d, want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Custom security levels
// ---------------------------------------------------------------------------

// TestAdminCustomLevelGovernsAccess creates a THIRD security level entirely
// through the API — the thing the PDP always supported and nothing could do —
// and proves its rules decide what its holder can reach: the one report it was
// granted, and nothing else.
func TestAdminCustomLevelGovernsAccess(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)

	rec, body := doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/security-levels", ownerTok,
		map[string]any{
			"name":           "Auditor",
			"default_access": "none",
			"rules": []map[string]string{
				{"effect": "allow", "action": "display", "resource": "report:trial-balance"},
				{"effect": "allow", "action": "display", "resource": "company"},
			},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create level = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	levelID, _ := body["security_level_id"].(string)
	if levelID == "" {
		t.Fatalf("no security_level_id in %v", body)
	}

	// It reads back with its rules and a zero member count.
	_, body = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/security-levels", ownerTok, nil)
	var auditor map[string]any
	for _, lv := range list(t, body, "security_levels") {
		if lv["name"] == "Auditor" {
			auditor = lv
		}
	}
	if auditor == nil {
		t.Fatalf("Auditor missing from %v", body)
	}
	if auditor["is_owner"] != false || auditor["default_access"] != "none" {
		t.Fatalf("Auditor = %v, want is_owner=false default_access=none", auditor)
	}
	if got := len(list(t, auditor, "rules")); got != 2 {
		t.Fatalf("Auditor has %d rules, want 2", got)
	}
	if auditor["member_count"] != float64(0) {
		t.Fatalf("Auditor member_count = %v, want 0", auditor["member_count"])
	}

	// Give it to somebody and let the PDP be the oracle.
	subject := "auditor-" + uuid.NewString()[:8]
	rec, _ = doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/members/invites", ownerTok,
		map[string]any{"subject": subject, "security_level_id": levelID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite auditor = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	tok := mintToken(t, srv, subject, "webauthn")
	if rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("/v1/me = %d, want 200", rec.Code)
	}

	for _, tc := range []struct {
		what   string
		method string
		path   string
		body   any
		want   int
	}{
		{"the granted report", http.MethodGet, "/trial-balance", nil, http.StatusOK},
		{"the company master (granted)", http.MethodGet, "", nil, http.StatusOK},
		{"a report NOT granted", http.MethodGet, "/balance-sheet", nil, http.StatusForbidden},
		{"a voucher write", http.MethodPost, "/vouchers/journal",
			map[string]any{"voucher_date": "2026-05-01", "postings": []any{}}, http.StatusForbidden},
		{"administering users", http.MethodGet, "/members", nil, http.StatusForbidden},
	} {
		rec, _ := doRaw(t, srv, tc.method, "/v1/companies/"+companyID+tc.path, tok, tc.body)
		if rec.Code != tc.want {
			t.Errorf("Auditor %s: %s %s = %d, want %d (%s)",
				tc.what, tc.method, tc.path, rec.Code, tc.want, rec.Body.String())
		}
	}

	// A level in use cannot be deleted (the members would point at nothing).
	rec, _ = doRaw(t, srv, http.MethodDelete,
		"/v1/companies/"+companyID+"/security-levels/"+levelID, ownerTok, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete in-use level = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}

	// PUT replaces the rule set wholesale — and the PDP follows immediately.
	rec, _ = doRaw(t, srv, http.MethodPut,
		"/v1/companies/"+companyID+"/security-levels/"+levelID, ownerTok,
		map[string]any{"rules": []map[string]string{
			{"effect": "allow", "action": "display", "resource": "report:balance-sheet"},
		}})
	if rec.Code != http.StatusOK {
		t.Fatalf("update level rules = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/balance-sheet", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("after rule swap, balance sheet = %d, want 200", rec.Code)
	}
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/trial-balance", tok, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("after rule swap, trial balance = %d, want 403 (the old rule was replaced)", rec.Code)
	}
}

// TestAdminRuleVocabularyRejectsTypos proves a misspelled rule is refused at
// write time rather than stored as a rule that can never match. The control
// case (the correctly-spelled rule) is accepted by the SAME request shape, so
// the rejection is attributable to the token and not to the endpoint.
func TestAdminRuleVocabularyRejectsTypos(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)

	post := func(name string, rule map[string]string) int {
		rec, _ := doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/security-levels", ownerTok,
			map[string]any{"name": name, "default_access": "none", "rules": []map[string]string{rule}})
		return rec.Code
	}

	for _, tc := range []struct {
		what string
		rule map[string]string
	}{
		{"a misspelled report name", map[string]string{"effect": "allow", "action": "display", "resource": "report:trail-balance"}},
		{"a report name used as a resource", map[string]string{"effect": "allow", "action": "display", "resource": "trial-balance"}},
		{"a misspelled voucher class", map[string]string{"effect": "allow", "action": "create", "resource": "sale"}},
		{"an unknown action", map[string]string{"effect": "allow", "action": "view", "resource": "report"}},
		{"an unknown effect", map[string]string{"effect": "permit", "action": "display", "resource": "report"}},
		{"a non-report qualified resource", map[string]string{"effect": "allow", "action": "create", "resource": "sales:urgent"}},
	} {
		if got := post("Bad "+uuid.NewString()[:8], tc.rule); got != http.StatusUnprocessableEntity {
			t.Errorf("%s (%v) = %d, want 422", tc.what, tc.rule, got)
		}
	}

	// CONTROL: the corrected spellings are accepted.
	for _, rule := range []map[string]string{
		{"effect": "allow", "action": "display", "resource": "report:trial-balance"},
		{"effect": "disallow", "action": "create", "resource": "sales"},
		{"effect": "allow", "action": "*", "resource": "*"},
	} {
		if got := post("Good "+uuid.NewString()[:8], rule); got != http.StatusCreated {
			t.Errorf("control rule %v = %d, want 201", rule, got)
		}
	}
}

// TestAdminOwnerLevelIsImmutableExceptCutoff pins the one level the API refuses
// to rewrite. Turning off the Owner level's WebAuthn requirement, or flipping
// its default_access, would silently rewrite the security posture every other
// check in this suite assumes. Its back-dated cutoff IS settable — Tally's own
// behaviour, and the cutoff binds Owners too.
func TestAdminOwnerLevelIsImmutableExceptCutoff(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)

	_, body := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/security-levels", ownerTok, nil)
	var ownerLevelID string
	for _, lv := range list(t, body, "security_levels") {
		if lv["is_owner"] == true {
			ownerLevelID, _ = lv["id"].(string)
		}
	}
	if ownerLevelID == "" {
		t.Fatalf("no owner level in %v", body)
	}
	path := "/v1/companies/" + companyID + "/security-levels/" + ownerLevelID

	for _, patch := range []map[string]any{
		{"requires_webauthn": false},
		{"default_access": "none"},
		{"name": "Overlord"},
		{"rules": []map[string]string{{"effect": "disallow", "action": "*", "resource": "*"}}},
	} {
		rec, _ := doRaw(t, srv, http.MethodPut, path, ownerTok, patch)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("PUT owner level %v = %d, want 422", patch, rec.Code)
		}
	}
	rec, _ := doRaw(t, srv, http.MethodDelete, path, ownerTok, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("DELETE owner level = %d, want 422", rec.Code)
	}

	// The cutoff IS settable, and it BINDS the owner who set it.
	rec, _ = doRaw(t, srv, http.MethodPut, path, ownerTok, map[string]any{"backdated_cutoff": "2026-07-01"})
	if rec.Code != http.StatusOK {
		t.Fatalf("set owner cutoff = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	rec, _ = doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/vouchers/journal", ownerTok,
		map[string]any{"voucher_date": "2026-06-15", "postings": []any{}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("back-dated write after cutoff = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	// Clearing it restores the previous behaviour (the write now fails on its
	// own merits — an empty voucher — NOT on authorization).
	rec, _ = doRaw(t, srv, http.MethodPut, path, ownerTok, map[string]any{"backdated_cutoff": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear owner cutoff = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	rec, _ = doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/vouchers/journal", ownerTok,
		map[string]any{"voucher_date": "2026-06-15", "postings": []any{}})
	if rec.Code == http.StatusForbidden {
		t.Fatalf("after clearing the cutoff the same write is still 403 (%s)", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Guards
// ---------------------------------------------------------------------------

// TestAdminLastOwnerGuard proves a company cannot be left with nobody able to
// administer it — and, because the guard is about OWNERS and not about SELF,
// that it lifts as soon as a second Owner exists.
func TestAdminLastOwnerGuard(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)

	_, body := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/members", ownerTok, nil)
	ownerUserID, _ := list(t, body, "members")[0]["user_id"].(string)

	// Demoting the last Owner is refused...
	rec, _ := doRaw(t, srv, http.MethodPut, "/v1/companies/"+companyID+"/members/"+ownerUserID, ownerTok,
		map[string]any{"security_level_name": "Data Entry"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("demote last owner = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	// ...as is removing them.
	rec, _ = doRaw(t, srv, http.MethodDelete, "/v1/companies/"+companyID+"/members/"+ownerUserID, ownerTok, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("remove last owner = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}

	// Add a second Owner; now the first may step down.
	second := "coowner-" + uuid.NewString()[:8]
	rec, _ = doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/members/invites", ownerTok,
		map[string]any{"subject": second, "security_level_name": "Owner"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite second owner = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	secondTok := mintToken(t, srv, second, "webauthn")
	if rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", secondTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("second owner /v1/me = %d, want 200", rec.Code)
	}

	rec, _ = doRaw(t, srv, http.MethodPut, "/v1/companies/"+companyID+"/members/"+ownerUserID, ownerTok,
		map[string]any{"security_level_name": "Data Entry"})
	if rec.Code != http.StatusOK {
		t.Fatalf("demote with a co-owner present = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	// The demotion is real: the ex-Owner can no longer administer.
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/members", ownerTok, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("demoted owner listing members = %d, want 403", rec.Code)
	}
	// The remaining Owner can revoke them entirely, and that too is real.
	rec, _ = doRaw(t, srv, http.MethodDelete, "/v1/companies/"+companyID+"/members/"+ownerUserID, secondTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove demoted member = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/trial-balance", ownerTok, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("removed member reading a report = %d, want 403", rec.Code)
	}
}

// TestAdminWritesRequireStepUp proves the admin surface inherits the Owner
// level's WebAuthn step-up: reading the member list without a WebAuthn AMR is
// fine, changing it is not. Same user, same rights, different AMR.
func TestAdminWritesRequireStepUp(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, ownerSubj := newAdminCompany(t, srv)
	_ = ownerTok

	weak := mintToken(t, srv, ownerSubj, "pwd") // no webauthn

	rec, _ := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyID+"/members", weak, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read members without step-up = %d, want 200 (step-up gates writes only)", rec.Code)
	}

	rec, body := doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/members/invites", weak,
		map[string]any{"subject": "x-" + uuid.NewString()[:8], "security_level_name": "Data Entry"})
	if rec.Code != http.StatusForbidden || body["code"] != "STEPUP_REQUIRED" {
		t.Fatalf("invite without step-up = %d %v, want 403 STEPUP_REQUIRED", rec.Code, body)
	}
	rec, body = doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/security-levels", weak,
		map[string]any{"name": "NoStepUp", "default_access": "none"})
	if rec.Code != http.StatusForbidden || body["code"] != "STEPUP_REQUIRED" {
		t.Fatalf("create level without step-up = %d %v, want 403 STEPUP_REQUIRED", rec.Code, body)
	}
}

// TestAdminCrossTenantAndServicePrincipalRefused covers the two callers who
// must never reach this surface: an Owner of a DIFFERENT company, and a service
// principal (which bypasses the PDP by design and would otherwise be able to
// grant itself Owner).
func TestAdminCrossTenantAndServicePrincipalRefused(t *testing.T) {
	srv := newServer(t)
	companyA, _, _ := newAdminCompany(t, srv)
	_, ownerBTok, _ := newAdminCompany(t, srv)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/members"},
		{http.MethodGet, "/security-levels"},
		{http.MethodPost, "/security-levels"},
	} {
		rec, body := doRaw(t, srv, tc.method, "/v1/companies/"+companyA+tc.path, ownerBTok,
			map[string]any{"name": "Sneaky"})
		if rec.Code != http.StatusForbidden {
			t.Errorf("owner of B on A%s = %d, want 403", tc.path, rec.Code)
		}
		if got, _ := body["error"].(string); got != "not a member of this company" {
			t.Errorf("owner of B on A%s error = %q, want the non-member message", tc.path, got)
		}
	}

	svc := mintToken(t, srv, "svc:statutoryd")
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/members", nil},
		{http.MethodPost, "/members/invites",
			map[string]any{"subject": "attacker", "security_level_name": "Owner"}},
		{http.MethodGet, "/security-levels", nil},
	} {
		rec, body := doRaw(t, srv, tc.method, "/v1/companies/"+companyA+tc.path, svc, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("service principal %s %s = %d, want 403 (%s)", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if got, _ := body["error"].(string); got != "service principals cannot administer users" {
			t.Errorf("service principal %s: error = %q, want the svc refusal", tc.path, got)
		}
	}

	// ...and a service subject cannot be made a member either.
	rec, _ := doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyA+"/members/invites",
		mintToken(t, srv, "does-not-matter"), map[string]any{"subject": "svc:evil"})
	if rec.Code == http.StatusCreated {
		t.Fatalf("inviting a svc: subject succeeded; it must never be a member")
	}
}

// TestMeIsTheCompanySwitchList proves /v1/me is a usable basis for company
// switching: a user with memberships in two companies sees BOTH, with the level
// they hold in each — and a user with none sees an empty list rather than an
// error.
func TestMeIsTheCompanySwitchList(t *testing.T) {
	srv := newServer(t)
	companyA, ownerA, _ := newAdminCompany(t, srv)
	companyB, ownerB, _ := newAdminCompany(t, srv)

	subject := "multi-" + uuid.NewString()[:8]
	for _, inv := range []struct {
		company, token, level string
	}{
		{companyA, ownerA, "Data Entry"},
		{companyB, ownerB, "Owner"},
	} {
		rec, _ := doRaw(t, srv, http.MethodPost, "/v1/companies/"+inv.company+"/members/invites", inv.token,
			map[string]any{"subject": subject, "security_level_name": inv.level})
		if rec.Code != http.StatusCreated {
			t.Fatalf("invite to %s = %d (%s)", inv.company, rec.Code, rec.Body.String())
		}
	}

	tok := mintToken(t, srv, subject, "webauthn")
	rec, body := doRaw(t, srv, http.MethodGet, "/v1/me", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/v1/me = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := map[string]string{}
	for _, c := range list(t, body, "companies") {
		id, _ := c["company_id"].(string)
		lvl, _ := c["security_level_name"].(string)
		got[id] = lvl
		if name, _ := c["name"].(string); name == "" {
			t.Errorf("company %s has no name in /v1/me (a switcher needs it)", id)
		}
	}
	if got[companyA] != "Data Entry" || got[companyB] != "Owner" {
		t.Fatalf("/v1/me companies = %v, want %s@Data Entry + %s@Owner", got, companyA, companyB)
	}

	// Both memberships are live, and they differ.
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyB+"/members", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("as Owner of B, list members = %d, want 200", rec.Code)
	}
	rec, _ = doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyA+"/members", tok, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("as Data Entry on A, list members = %d, want 403", rec.Code)
	}

	// A brand-new subject with no invites gets an empty list, not an error.
	fresh := mintToken(t, srv, "nobody-"+uuid.NewString()[:8], "webauthn")
	rec, body = doRaw(t, srv, http.MethodGet, "/v1/me", fresh, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/v1/me for a member of nothing = %d, want 200", rec.Code)
	}
	if got := len(list(t, body, "companies")); got != 0 {
		t.Fatalf("/v1/me for a member of nothing lists %d companies, want 0", got)
	}
}

// TestMeRequiresAuthentication keeps /v1/me on the right side of the 401/403
// contract: it is authn-only, so a missing/invalid token is 401 (not 403).
func TestMeRequiresAuthentication(t *testing.T) {
	srv := newServer(t)
	for _, tc := range []struct{ what, token string }{
		{"no token", ""},
		{"garbage token", "not-a-jwt"},
	} {
		rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", tc.token, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("/v1/me with %s = %d, want 401", tc.what, rec.Code)
		}
	}
	// A service principal is refused at the same boundary as company creation.
	rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", mintToken(t, srv, "svc:statutoryd"), nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("/v1/me as a service principal = %d, want 403", rec.Code)
	}
}

// TestAdminMemberListDoesNotLeakTheDirectory is the RLS half of the story: the
// widened app_user policy (migration 0020) must expose ONLY co-members of the
// company in scope. Company B's owner exists in app_user; company A's owner must
// never see them.
func TestAdminMemberListDoesNotLeakTheDirectory(t *testing.T) {
	srv := newServer(t)
	companyA, ownerATok, _ := newAdminCompany(t, srv)
	_, ownerBTok, ownerBSubj := newAdminCompany(t, srv)
	// Make sure B's owner row really exists (creating the company did it).
	if rec, _ := doRaw(t, srv, http.MethodGet, "/v1/me", ownerBTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("owner B /v1/me = %d", rec.Code)
	}

	_, body := doRaw(t, srv, http.MethodGet, "/v1/companies/"+companyA+"/members", ownerATok, nil)
	for _, m := range list(t, body, "members") {
		if m["subject"] == ownerBSubj {
			t.Fatalf("company A's member list leaked %s, a user of another tenant: %v", ownerBSubj, m)
		}
	}
	if got := len(list(t, body, "members")); got != 1 {
		t.Fatalf("company A has %d members, want 1 (%s)", got, rec500(body))
	}
}

func rec500(body map[string]any) string { return fmt.Sprintf("%v", body) }

// ---------------------------------------------------------------------------
// The served rule vocabulary
// ---------------------------------------------------------------------------

// TestAdminVocabularyIsExactlyWhatTheServerAccepts guards the reason
// GET /security-levels/vocabulary exists at all.
//
// A rule editor in the browser must offer exactly the tokens POST/PUT
// /security-levels will accept. Get that wrong in either direction and the
// failure is silent-ish: offer a token the server 422s and the administrator
// cannot express a rule they are entitled to; omit one and a resource becomes
// un-nameable, so only default_access can reach it. A hardcoded TypeScript copy
// of internal/authz's Known* sets would drift the moment either side changed.
//
// The test closes BOTH directions:
//
//	SUPERSET — every action and every resource the endpoint serves is carried
//	into a REAL POST /security-levels. Validation is per-token, so one level
//	carrying (each action x a fixed resource) + (a fixed action x each resource)
//	exercises every token served; a single stranger 422s the whole request and
//	the body names it.
//
//	SUBSET — the served lists must CONTAIN every token internal/authz declares
//	writable. Serving a subset would pass the superset check vacuously.
//
// Plus a control: the near-miss spellings TestAdminRuleVocabularyRejectsTypos
// proves are 422 must be absent from what is served.
func TestAdminVocabularyIsExactlyWhatTheServerAccepts(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)

	rec, body := doRaw(t, srv, http.MethodGet,
		"/v1/companies/"+companyID+"/security-levels/vocabulary", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET vocabulary = %d, want 200 (%s)", rec.Code, rec500(body))
	}
	strs := func(key string) []string {
		raw, ok := body[key].([]any)
		if !ok {
			t.Fatalf("vocabulary has no %q array: %v", key, body)
		}
		out := make([]string, 0, len(raw))
		for _, e := range raw {
			s, ok := e.(string)
			if !ok {
				t.Fatalf("%s element is not a string: %v", key, e)
			}
			out = append(out, s)
		}
		return out
	}
	effects, actions := strs("effects"), strs("actions")
	resources, defaults := strs("resources"), strs("default_accesses")

	// -- SUPERSET: every served token survives a real write ------------------
	// "report" and "display" are the fixed partners; both are asserted present
	// by the subset check below, so this cannot pass by pairing with a stranger.
	rules := make([]map[string]string, 0, len(actions)+len(resources))
	for _, a := range actions {
		rules = append(rules, map[string]string{"effect": "allow", "action": a, "resource": "report"})
	}
	for _, res := range resources {
		rules = append(rules, map[string]string{"effect": "allow", "action": "display", "resource": res})
	}
	for _, e := range effects {
		rules = append(rules, map[string]string{"effect": e, "action": "display", "resource": "report"})
	}
	rec, body = doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/security-levels", ownerTok,
		map[string]any{
			"name": "Vocab Probe " + uuid.NewString()[:8], "default_access": "none", "rules": rules,
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("a level carrying EVERY served token = %d, want 201 — the endpoint offers a token the "+
			"server rejects: %s", rec.Code, rec500(body))
	}

	// Both default_access values must likewise be writable.
	for _, da := range defaults {
		rec, body = doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/security-levels", ownerTok,
			map[string]any{"name": "Vocab DA " + uuid.NewString()[:8], "default_access": da, "rules": []any{}})
		if rec.Code != http.StatusCreated {
			t.Errorf("served default_access %q = %d, want 201 (%s)", da, rec.Code, rec500(body))
		}
	}

	// -- SUBSET: nothing writable is missing from what is served -------------
	has := func(hay []string, needle string) bool {
		for _, h := range hay {
			if h == needle {
				return true
			}
		}
		return false
	}
	for _, a := range authz.KnownActions {
		if !has(actions, string(a)) {
			t.Errorf("action %q is writable but not served — no rule editor could name it", a)
		}
	}
	if !has(actions, authz.Wildcard) {
		t.Errorf("the wildcard action is writable but not served")
	}
	for _, res := range authz.KnownResources {
		if !has(resources, string(res)) {
			t.Errorf("resource %q is writable but not served", res)
		}
	}
	for _, n := range authz.KnownReportNames {
		if !has(resources, "report:"+n) {
			t.Errorf("report %q is writable but report:%s is not served", n, n)
		}
	}
	if !has(resources, authz.Wildcard) {
		t.Errorf("the wildcard resource is writable but not served")
	}
	for _, e := range []string{"allow", "disallow"} {
		if !has(effects, e) {
			t.Errorf("effect %q is writable but not served", e)
		}
	}
	for _, da := range []string{"full", "none"} {
		if !has(defaults, da) {
			t.Errorf("default_access %q is writable but not served", da)
		}
	}

	// -- CONTROL: the proven-422 near-misses are not on offer ----------------
	for _, typo := range []string{"report:trail-balance", "trial-balance", "sale", "sales:urgent"} {
		if has(resources, typo) {
			t.Errorf("the vocabulary offers %q, which POST /security-levels answers 422", typo)
		}
	}
	for _, typo := range []string{"view", "read"} {
		if has(actions, typo) {
			t.Errorf("the vocabulary offers action %q, which POST /security-levels answers 422", typo)
		}
	}
}

// TestAdminVocabularyIsPDPGated pins that reading the vocabulary is governed by
// the same `security` resource as the rest of the surface: an operator who may
// not administer users may not enumerate the tokens either, and a service
// principal is refused outright.
func TestAdminVocabularyIsPDPGated(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)

	// A Data-Entry-shaped level: default-deny with no `security` rule.
	rec, body := doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/security-levels", ownerTok,
		map[string]any{
			"name": "Vocab Operator " + uuid.NewString()[:8], "default_access": "none",
			"rules": []map[string]string{{"effect": "allow", "action": "create", "resource": "sales"}},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create operator level = %d (%s)", rec.Code, rec500(body))
	}
	levelID, _ := body["security_level_id"].(string)

	operator := "vocab-operator-" + uuid.NewString()[:8]
	if rec, body = doRaw(t, srv, http.MethodPost, "/v1/companies/"+companyID+"/members/invites", ownerTok,
		map[string]any{"subject": operator, "security_level_id": levelID}); rec.Code != http.StatusCreated {
		t.Fatalf("invite operator = %d (%s)", rec.Code, rec500(body))
	}
	operatorTok := mintToken(t, srv, operator, "webauthn")
	if rec, _ = doRaw(t, srv, http.MethodGet, "/v1/me", operatorTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("operator /v1/me = %d", rec.Code)
	}

	if rec, _ = doRaw(t, srv, http.MethodGet,
		"/v1/companies/"+companyID+"/security-levels/vocabulary", operatorTok, nil); rec.Code != http.StatusForbidden {
		t.Errorf("operator -> vocabulary = %d, want 403", rec.Code)
	}

	svcTok := mintToken(t, srv, "svc:statutoryd", "webauthn")
	if rec, _ = doRaw(t, srv, http.MethodGet,
		"/v1/companies/"+companyID+"/security-levels/vocabulary", svcTok, nil); rec.Code != http.StatusForbidden {
		t.Errorf("service principal -> vocabulary = %d, want 403", rec.Code)
	}
}
