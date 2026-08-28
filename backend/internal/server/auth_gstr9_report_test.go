package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/khata/cored/internal/authz"
)

// Migration 0087 — the GSTR-9 annual return's ACL counterweight (T12).
//
// ⛔ THE DEFECT THIS GUARDS IS THAT THE GRANT IS THE DEFAULT. Migration 0011 seeds
// the Data Entry level a COARSE `allow display report` and internal/authz matches
// it against ANY `report:NAME`, so a report becomes readable by Data Entry the
// moment its name enters authz.KnownReportNames. The REFUSAL is the thing somebody
// has to remember to write. This is registered defect D95, and 0087 is the TWELFTH
// migration to write it.
//
// ⛔⛔ WHAT MAKES THIS ONE DIFFERENT FROM THE ELEVEN BEFORE IT. Every other refused
// report is a view of the company's own trading. This is a STATUTORY RETURN for a
// whole financial year, and — through its divergence lists — a named account of the
// months in which this company may have mis-filed. That second half has no
// precedent in the vocabulary: nothing else in this product tells anybody where the
// filed returns and the books part company.
//
// ⭐ AND UNLIKE 0086, THE DENY IS NOT RECORDED AS AN ASSUMPTION. 0086's header says
// in terms that "should a data-entry clerk see a budget-variance report" was on the
// owner's ask list and unanswered, and that it proceeded on 0072's precedent. This
// file does not inherit that hedge, deliberately: 0035 already refuses payroll
// totals to this level and 0072 already refuses departmental profitability, and a
// document strictly more disclosive than both — which additionally names the
// company's filing risk — does not raise a new question. ⚠️ A caveat copied along
// with the SQL it sat beside is how every migration in a family ends up equally
// uncertain.
//
// Like the budget, forecast and TDS-register files, these tests CREATE THEIR
// COMPANY AT TEST TIME, so they exercise seed_company_auth_defaults rather than
// 0087's backfill. A migration that backfills and forgets the seeder is correct for
// every company that existed and silently wrong for every company created
// afterwards — which is exactly the 0063/0071 incident.

// TestDataEntryIsRefusedTheAnnualReturn is the row's real oracle: the DISPLAY
// refusal, which is the one row that actually changes behaviour.
func TestDataEntryIsRefusedTheAnnualReturn(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)

	// ⚠️⚠️ THE CONTROL, AND IT CARRIES MORE WEIGHT THAN THE REFUSAL BELOW. It
	// disproves two ways of passing for the wrong reason: a level that can read NO
	// report proves nothing about this one, and an invite that was never CLAIMED
	// yields a token that is 403 on every company route — satisfying every
	// assertion in this file while proving nothing at all. That mistake has been
	// made in this repo before.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/trial-balance?as_on=2026-07-31", dataEntry, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: Data Entry must still read the Trial Balance = %d, want 200 — "+
			"if this is 403 the token is not a member and every refusal below is vacuous", rec.Code)
	}

	// ⛔ A SECOND CONTROL THAT THE OTHER ACL FILES DO NOT HAVE, AND IT IS THE ONE
	// THAT MAKES THIS FILE ABOUT 0087 RATHER THAN ABOUT GST IN GENERAL. The seeded
	// Data Entry level CAN read the monthly GSTR-1 — no migration ever refused it,
	// and 0011's coarse grant reaches `report:gstr1` exactly as it reaches
	// `report:gstr9`. Without this arm, a 403 on /gstr9 would be equally consistent
	// with "this level cannot read GST returns at all", and 0087 would be
	// unfalsifiable.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		base+"/gstr1?from=2026-07-01&to=2026-07-31", dataEntry, nil); rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: Data Entry reads the MONTHLY GSTR-1 = %d, want 200 — this is what "+
			"makes the /gstr9 refusal below attributable to 0087 rather than to GST reads "+
			"being closed off wholesale", rec.Code)
	}

	// ⛔ THE ARM THAT GOES RED-TO-GREEN ON MIGRATION 0087.
	//
	// ⚠️ NOTE THE PATH CARRIES NO PARAMETERS. It does not need any: the PDP runs
	// BEFORE the handler, so a refused clerk never reaches the parameter check. An
	// Owner on this same bare path gets 400 (see the arm below), and asserting the
	// two DIFFERENT refusals is what proves the 403 is the ACL and not a malformed
	// request.
	path := base + "/gstr9"
	if rec, _ := aclRequest(t, s, http.MethodGet, path, dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("Data Entry read /gstr9 = %d, want 403 — it is a whole financial year of "+
			"turnover and input credit in one document, plus the months in which this company "+
			"may have mis-filed", rec.Code)
	}

	// ⛔ THE OWNER'S ANSWER IS 400, NOT 200, AND ASSERTING 200 WOULD FAIL ON CORRECT
	// CODE. GSTR-9 refuses two requests its monthly neighbours would accept, and
	// each refusal must name the thing to change. The point of this arm is that the
	// Owner is stopped by a MALFORMED REQUEST and the clerk by a POLICY DECISION:
	// two different answers, and a test that blurred them would hide the day one
	// became the other.
	for _, tc := range []struct{ query, mustName, why string }{
		{"", "financial year",
			"there is no default period, and the message has to say the window must be a " +
				"financial year rather than merely that from/to are missing"},
		{"?from=2026-06-01&to=2026-08-31", "GSTR-3B",
			"a quarter is not a GSTR-9 window, and the refusal must say what to read " +
				"instead — a refusal with no alternative sends the caller looking for a " +
				"setting that does not exist"},
		{"?from=2026-04-01&to=2027-03-31", "cgst_ledger_id",
			"without the tax ledger ids the books column is ZERO for all twelve months and " +
				"every period appears to disagree with the returns by exactly the tax " +
				"charged — a wrong number that looks like a finding, which is worse than " +
				"an error"},
	} {
		rec, _ := aclRequest(t, s, http.MethodGet, path+tc.query, ownerTok, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("Owner read /gstr9%s = %d, want 400 — 0087 must narrow Data Entry, not "+
				"the Owner, and the Owner's refusal must be about the REQUEST rather than "+
				"about permission (%s)", tc.query, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(rec.Body.String(), tc.mustName) {
			t.Errorf("the Owner's 400 for /gstr9%s does not mention %q: %s — %s",
				tc.query, tc.mustName, rec.Body.String(), tc.why)
		}
	}

	// ⛔ AND THE CLERK IS STILL 403 WITH A WELL-FORMED YEAR. Without this arm the
	// refusal above could be coming from anywhere: the PDP runs before the handler,
	// so a 403 on a malformed request proves the path is guarded but not that a
	// well-formed READ is. This is the arm a reversed policy would flip.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		path+"?from=2026-04-01&to=2027-03-31", dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("Data Entry read a well-formed /gstr9 financial year = %d, want 403", rec.Code)
	}
}

// TestTheGSTR9PrintRefusalIsDurabilityAndNotABehaviourChange exists because the
// honest thing is to SAY SO IN A TEST.
//
// ⛔⛔ 0087'S `disallow print` ROW CHANGES NOTHING OBSERVABLE TODAY, AND A PROBE
// THAT COUNTED IT AS A WIN WOULD BE LYING. Data Entry is ALREADY refused the print
// form before that migration exists: 0038 seeds ZERO `allow print report:NAME`
// rows, 0011's coarse grant is action 'display' only, and the level's
// default_access is 'none'. The row is written for the reason 0038's own header
// gives — a report that cannot be displayed but CAN be printed is a leak with a
// paper trail — and so that the day anyone seeds a coarse print grant, this name is
// not swept up in it.
func TestTheGSTR9PrintRefusalIsDurabilityAndNotABehaviourChange(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)
	path := base + "/print/reports/gstr9"

	// Data Entry: 403. ⚠️ TRUE BEFORE 0087 TOO — see the doc comment.
	if rec := printGet(t, s, path, dataEntry); rec.Code != http.StatusForbidden {
		t.Errorf("Data Entry print gstr9 = %d, want 403", rec.Code)
	}

	// ⛔ THE OWNER ARM IS 501, NOT 200, AND THIS IS THE SHARPEST DEFERRAL IN THE
	// SET. On screen the annual return's ten unconditional disclosures — headed
	// `not-filing-ready` — sit beside the figures and cannot be missed. On paper,
	// tidy tables on company stationery with nowhere for ten caveats is a document
	// somebody hands to a tax officer. The printed form needs a layout in which the
	// disclosures come FIRST, and nobody has designed one.
	rec := printGet(t, s, path, ownerTok)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("Owner print gstr9 = %d, want 501 (no printed layout is specified for an "+
			"annual return, and a blank page that reads as a filed one is worse than a "+
			"refusal)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "gstr9") {
		t.Errorf("the 501 must NAME the report so an operator knows which one has no "+
			"layout: %s", rec.Body.String())
	}
}

// TestTheGSTR9RulesReachEveryExistingCompanyToo is 0087's BACKFILL half, and the
// out-of-band oracle for the whole file.
//
// ⛔ A 403 CAN BE PRODUCED BY A DOZEN THINGS — a stale membership, a wrong token, a
// company that does not exist — and only the RULES THEMSELVES prove which rule
// answered. GET /security-levels is the administrator's own view of the level, so
// this asserts what a human would actually be shown. It reads through the product's
// surface rather than the table because acl_rule is RLS-protected, and a test that
// reached around RLS would be asserting something no caller can.
func TestTheGSTR9RulesReachEveryExistingCompanyToo(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)

	rec, body := aclRequest(t, s, http.MethodGet,
		"/v1/companies/"+companyID+"/security-levels", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list security levels = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	levels, _ := body["security_levels"].([]any)
	var got []string
	// ⛔ THE CONTROL FOR THE 0071 INCIDENT, CARRIED FORWARD. 0087 restates
	// seed_company_auth_defaults, and the failure that migration 0071 exists to
	// repair is a restatement that drops an EARLIER migration's PERFORM: every
	// company created on a fresh deployment then silently loses a refusal nobody
	// looks at again. So this counts a rule from a much earlier migration as well,
	// and fails if it went missing.
	bankStatementRules := 0
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
			effect, _ := rm["effect"].(string)
			action, _ := rm["action"].(string)
			if res == "report:bank-statement" {
				bankStatementRules++
			}
			if res != "report:gstr9" {
				continue
			}
			got = append(got, effect+" "+action+" "+res)
		}
	}
	if !found {
		t.Fatalf("no Data Entry level in the listing — the seeder did not run")
	}
	if bankStatementRules != 2 {
		t.Errorf("CONTROL: report:bank-statement has %d rules, want 2. 0087 restates "+
			"seed_company_auth_defaults, and this is the line migration 0068 once dropped — "+
			"the 0071 incident, in which every company created on a fresh deployment handed "+
			"its Data Entry level the imported bank statement", bankStatementRules)
	}

	want := map[string]bool{
		"disallow display report:gstr9": false,
		"disallow print report:gstr9":   false,
	}
	for _, g := range got {
		if _, ok := want[g]; !ok {
			// ⛔ AND NO ALLOW WAS SEEDED. 0087 seeds two disallows and no allow. An
			// `allow` would land here and fail loudly rather than being averaged into a
			// count — and note that an allow could not even WIN: it would tie at
			// specificity 3 and Disallow beats Allow at equal specificity, so reversing
			// this decision means DELETING the disallow, not adding an allow.
			t.Errorf("unexpected gstr9 rule %q — 0087 seeds two disallows and no allow", g)
			continue
		}
		want[g] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing acl_rule %q (got %v) — every company created on a fresh "+
				"deployment would hand its Data Entry level the annual return", k, got)
		}
	}
}

// ---------------------------------------------------------------------------
// VOCABULARY PINS
// ---------------------------------------------------------------------------

// TestTheAnnualReturnIsSpelledLikeItsTwoSiblings is the pin on the one decision
// in T12 that departs from a written reservation.
//
// PLAN-TALLY-PARITY.md §2.5 reserved `report:gstr-9`. The token taken is
// `report:gstr9`, because the two names it sits beside are `gstr1` and `gstr3b`.
// ⛔ IF SOMEBODY "CORRECTS" THIS BACK, migration 0087's rows would govern a
// resource no route emits: 0011's coarse `allow display report` would reach the
// real name unopposed, and an administrator reading the rule list would believe
// the annual return was refused while it was being served. That failure is
// invisible from the rule list, which is why it is pinned here rather than left
// to the migration's header.
func TestTheAnnualReturnIsSpelledLikeItsTwoSiblings(t *testing.T) {
	names := map[string]bool{}
	for _, n := range authz.KnownReportNames {
		names[n] = true
	}
	if !names["gstr9"] {
		t.Fatal("`gstr9` is not in authz.KnownReportNames, so migration 0087's rows name a " +
			"resource the vocabulary does not know and the route is unmapped — default-DENY " +
			"for every level including the Owner")
	}
	if names["gstr-9"] {
		t.Error("BOTH `gstr9` and `gstr-9` are in KnownReportNames. Two tokens one hyphen " +
			"apart governing the same document is how an administrator writes a rule that " +
			"silently governs nothing — the hazard vocabulary.go already records for " +
			"tds-profile and deductor-status")
	}
	for _, sibling := range []string{"gstr1", "gstr3b"} {
		if !names[sibling] {
			t.Errorf("CONTROL: `%s` is missing from KnownReportNames, so the consistency "+
				"argument this test rests on no longer holds and the spelling should be "+
				"reconsidered rather than assumed", sibling)
		}
	}
	if !authz.ValidResource("report:gstr9") {
		t.Error("`report:gstr9` is not a valid resource, so migration 0087's rows name " +
			"something the vocabulary rejects and the admin API could never restate them")
	}
}

// TestTheAnnualReturnRouteResolvesForBothActions is the two-touch rule as a test.
//
// server.go loops over authz.KnownReportNames and auto-mounts
// GET /print/reports/<name>; a name with no reportName() case is a route
// routeAction cannot map, i.e. default-DENY for every level INCLUDING the Owner.
// That bug already shipped once for reconciliation-queue, and the display half
// fails the same way — an unmapped {companyID} sub-path is a 403 for everybody.
func TestTheAnnualReturnRouteResolvesForBothActions(t *testing.T) {
	act, res, resName, mapped := printRouteActionFor(t, "/print/reports/gstr9")
	if !mapped || act != authz.Print || res != "report" || resName != "gstr9" {
		t.Errorf("routeAction on /print/reports/gstr9 = (%q,%q,%q,%v), want (print,report,"+
			"gstr9,true): a report the router cannot name is a report no ACL rule can ever "+
			"govern, so 0087's `disallow print` row would match nothing", act, res, resName, mapped)
	}
	if got := reportName("/gstr9"); got != "gstr9" {
		t.Errorf("reportName(\"/gstr9\") = %q, want \"gstr9\". Without this case the DISPLAY "+
			"route is an unmapped {companyID} sub-path, which routeAction default-denies — "+
			"so the Owner gets a 403 on a report they own", got)
	}
}
