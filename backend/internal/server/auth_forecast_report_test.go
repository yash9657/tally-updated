package server

import (
	"net/http"
	"strings"
	"testing"
)

// Migration 0076 — the forecast plane's ACL counterweight. Register defect D95.
//
// ⛔ THE DEFECT THIS GUARDS IS THAT THE GRANT IS THE DEFAULT. Migration 0011 seeds
// the Data Entry level a COARSE `allow display report`, and internal/authz matches
// it against ANY `report:NAME`. So a report becomes readable by Data Entry the
// moment its name enters authz.KnownReportNames, and the REFUSAL is the thing
// somebody has to remember to write. `cash-flow` and `anomalies` were in that
// vocabulary and served by cored for a whole wave with no refusal written.
//
// ⛔ AND THE ANOMALY LIST IS NOT A REPORT ABOUT MONEY. It names, BY VOUCHER NUMBER,
// every voucher backdated more than 7 days, entered outside 07:00-22:00 IST, edited
// 3+ times inside 24h, or written for a round taxable value >= 10,000. The people it
// monitors ARE the Data Entry operators. A control surface readable by its own
// subject is not much of a control — which is why this file exists at all.
//
// Like TestDataEntryIsRefusedTheTDSRegister (0068) these tests CREATE THEIR COMPANY
// AT TEST TIME, so they exercise seed_company_auth_defaults rather than 0076's
// backfill. A migration that backfills and forgets the seeder is correct for every
// company that existed and silently wrong for every company created afterwards.

// TestDataEntryIsRefusedTheForecastPlane is the row's real oracle: the two DISPLAY
// refusals, which are the two rows that actually change behaviour.
func TestDataEntryIsRefusedTheForecastPlane(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)

	// ⚠️⚠️ THE CONTROL, AND IT CARRIES MORE WEIGHT HERE THAN THE REFUSALS BELOW.
	// It disproves TWO different ways of passing for the wrong reason:
	//
	//  1. a level that can read NO report proves nothing about these two; and
	//  2. ⛔ AN INVITE THAT WAS NEVER CLAIMED. `aclSeededDataEntryToken` invites and
	//     then claims via GET /v1/me, and if the claim step were ever dropped the
	//     token would be 403 "not a member" on EVERY company route — satisfying
	//     every assertion in this file while proving nothing whatsoever. That
	//     mistake has been made in this repo before (drive-l9-users.mjs records it).
	//
	// So: the coarse grant must be real and this token must really be a member.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/trial-balance?as_on=2026-07-31", dataEntry, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: Data Entry must still read the Trial Balance = %d, want 200 — "+
			"if this is 403 the token is not a member and every refusal below is vacuous", rec.Code)
	}

	// ⛔ THE TWO ARMS THAT GO RED-TO-GREEN ON THIS MIGRATION.
	for _, tc := range []struct {
		path, why string
	}{
		{"/cash-flow", "the forward cash position — what the business expects to collect and pay"},
		{"/anomalies", "the control surface naming, by voucher number, this very level's irregular entries"},
	} {
		if rec, _ := aclRequest(t, s, http.MethodGet, base+tc.path, dataEntry, nil); rec.Code != http.StatusForbidden {
			t.Errorf("Data Entry read %s = %d, want 403 — it is %s", tc.path, rec.Code, tc.why)
		}
		// Owner may. Both handlers answer a BARE GET: cash-flow defaults as_on to
		// today with a 90-day weekly horizon, anomalies treats an absent window as
		// unbounded — so no query string is needed to make this arm meaningful.
		if rec, _ := aclRequest(t, s, http.MethodGet, base+tc.path, ownerTok, nil); rec.Code != http.StatusOK {
			t.Errorf("Owner read %s = %d, want 200 — 0076 must narrow Data Entry, not the Owner",
				tc.path, rec.Code)
		}
	}
}

// TestTheForecastPrintRefusalsAreDurabilityAndNotABehaviourChange exists because
// the honest thing is to SAY SO IN A TEST.
//
// ⛔⛔ THE TWO `disallow print` ROWS IN 0076 CHANGE NOTHING OBSERVABLE TODAY, AND A
// PROBE THAT COUNTED THEM AS WINS WOULD BE LYING. Data Entry is ALREADY refused
// both print forms before that migration exists: 0038 seeds ZERO
// `allow print report:NAME` rows, 0011's coarse grant is action 'display' only (the
// authz matcher matches the action too), and the level's default_access is 'none' —
// so print of every report is already default-denied by falling through to the
// default. The rows are written for the reason 0038's own header gives: a report
// that cannot be displayed but CAN be printed is a leak with a paper trail, and the
// day someone seeds a coarse print grant these two must not be swept up in it.
//
// This test therefore asserts the CURRENT truth and labels it as pre-existing, so
// that a later reader does not mistake it for evidence that 0076 did something.
func TestTheForecastPrintRefusalsAreDurabilityAndNotABehaviourChange(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)

	for _, name := range []string{"cash-flow", "anomalies"} {
		path := base + "/print/reports/" + name

		// Data Entry: 403. ⚠️ TRUE BEFORE 0076 TOO — see the doc comment.
		if rec := printGet(t, s, path, dataEntry); rec.Code != http.StatusForbidden {
			t.Errorf("Data Entry print %s = %d, want 403", name, rec.Code)
		}

		// ⛔ THE OWNER ARM IS **501**, NOT 200, AND ASSERTING 200 WOULD FAIL ON
		// CORRECT CODE. Printing these two is NOT IMPLEMENTED: handlePrintReport's
		// default arm answers 501 naming the report, and
		// TestTheUnimplementedReportsAreStillGovernableAndSaySo actively requires
		// that. The point of the arm is that the Owner's refusal is a MISSING LAYOUT
		// and the clerk's is a POLICY DECISION — two different answers, and a test
		// that blurred them would hide the day one became the other.
		rec := printGet(t, s, path, ownerTok)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("Owner print %s = %d, want 501 (no printed layout is specified for it)",
				name, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), name) {
			t.Errorf("the 501 for %s must NAME the report, so an operator knows which "+
				"one has no layout: %s", name, rec.Body.String())
		}
	}
}

// TestTheForecastRulesReachEveryExistingCompanyToo is 0076's BACKFILL half.
//
// The seeder covers companies created from now on; the backfill covers the ones
// that already existed. ⛔ MISSING EITHER IS A REAL DEFECT AND THEY FAIL IN
// OPPOSITE DIRECTIONS — 0063 wrote a correct backfill and lost its seeder to apply
// order, which is why 0071 exists. This asserts the rules are present for a level
// that the seeder did NOT create in this test.
func TestTheForecastRulesReachEveryExistingCompanyToo(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)

	// ⛔ THIS IS THE OUT-OF-BAND ORACLE, AND IT IS DELIBERATELY NOT A STATUS CODE.
	// A 403 can be produced by a dozen things — a stale membership, a wrong token,
	// a company that does not exist — and only the RULES THEMSELVES prove which
	// rule answered. `GET /security-levels` is the administrator's own view of the
	// level, so this asserts what a human would actually be shown.
	//
	// ⚠️ It reads through the product's surface rather than the table because
	// acl_rule is RLS-protected: a bare pool query cannot see it without the
	// company GUC bound (see db.RunInAuthTx's own doc comment), and a test that
	// reached around RLS to read it would be asserting something no caller can.
	rec, body := aclRequest(t, s, http.MethodGet,
		"/v1/companies/"+companyID+"/security-levels", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list security levels = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// ⛔ THE KEY IS `security_levels`, NOT `levels`, AND GETTING THAT WRONG IS THE
	// §5.7 TRAP THIS REPO HAS BEEN BITTEN BY THREE TIMES (p.ledger_name for
	// p.ledger, stock_item_name for item, innerText for an input's value). The
	// first draft of this test read body["levels"], which is absent — and it
	// FAILED LOUDLY instead of passing vacuously for exactly one reason: the
	// not-found case below is a t.Fatalf, not a skipped assertion. An absent
	// projection has a falsy value that compares cleanly against anything, so the
	// discipline is to make the probe THROW rather than to read carefully.
	levels, _ := body["security_levels"].([]any)
	var got []string
	found := false
	for _, lv := range levels {
		m, _ := lv.(map[string]any)
		if m == nil || m["name"] != "Data Entry" {
			continue
		}
		found = true
		rules, _ := m["rules"].([]any)
		for _, r := range rules {
			rm, _ := r.(map[string]any)
			if rm == nil {
				continue
			}
			res, _ := rm["resource"].(string)
			if res != "report:cash-flow" && res != "report:anomalies" {
				continue
			}
			effect, _ := rm["effect"].(string)
			action, _ := rm["action"].(string)
			got = append(got, effect+" "+action+" "+res)
		}
	}
	if !found {
		t.Fatalf("no Data Entry level in the listing — the seeder did not run")
	}

	want := map[string]bool{
		"disallow display report:cash-flow": false,
		"disallow print report:cash-flow":   false,
		"disallow display report:anomalies": false,
		"disallow print report:anomalies":   false,
	}
	for _, g := range got {
		if _, ok := want[g]; !ok {
			// ⛔ AND NO ALLOW WAS SEEDED. The user's decision was DENY BOTH. An
			// `allow` on either token would land here and fail loudly rather than
			// being averaged into a count.
			t.Errorf("unexpected forecast rule %q — 0076 seeds four disallows and no allow", g)
			continue
		}
		want[g] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing acl_rule %q (got %v)", k, got)
		}
	}
}
