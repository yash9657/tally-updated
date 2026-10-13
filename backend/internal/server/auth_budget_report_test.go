package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/reporting"
)

// Migration 0086 — the budget report's ACL counterweight (T11).
//
// ⛔ THE DEFECT THIS GUARDS IS THAT THE GRANT IS THE DEFAULT. Migration 0011 seeds
// the Data Entry level a COARSE `allow display report` and internal/authz matches
// it against ANY `report:NAME`, so a report becomes readable by Data Entry the
// moment its name enters authz.KnownReportNames. The REFUSAL is the thing somebody
// has to remember to write. This is registered defect D95, and nine migrations now
// write that refusal for a comparable report.
//
// ⛔⛔ AND THIS REPORT IS A SHARPER DISCLOSURE THAN ANY OF THEM. Every other report
// in the vocabulary derives from vouchers a clerk can already see in some form.
// This one carries a figure NO VOUCHER CONTAINS — what the owner PLANNED — and sets
// it beside the actual, so one read yields the strategy, every line the business is
// failing against, and (via a budget on a payroll group) the payroll total that
// migration 0035 refuses at five separate names. A refusal that can be walked
// around through a second report is not a refusal.
//
// ⚠️ THE DENY IS AN ASSUMPTION, NOT AN ANSWERED QUESTION, and this file says so
// because 0086's header does. Unlike 0076 — where the owner was asked on
// 2026-08-23 — "should a data-entry clerk see a budget-variance report" is on the
// owner's ask list and unanswered. It proceeds on 0072's precedent (deny for the
// cost reports) because the failure directions are asymmetric: over-denying is a
// support ticket, under-denying is a clerk reading the company's margins. If the
// owner reverses it, THESE TESTS ARE THE THING TO CHANGE, deliberately — a silent
// policy flip should break a test.
//
// Like the forecast and TDS-register files, these tests CREATE THEIR COMPANY AT
// TEST TIME, so they exercise seed_company_auth_defaults rather than 0086's
// backfill. A migration that backfills and forgets the seeder is correct for every
// company that existed and silently wrong for every company created afterwards —
// which is exactly the 0063/0071 incident.

// TestDataEntryIsRefusedTheBudgetVariance is the row's real oracle: the DISPLAY
// refusal, which is the one row that actually changes behaviour.
func TestDataEntryIsRefusedTheBudgetVariance(t *testing.T) {
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

	// ⛔ THE ARM THAT GOES RED-TO-GREEN ON MIGRATION 0086.
	//
	// ⚠️ NOTE THE PATH CARRIES NO budget_id. It does not need one: the PDP runs
	// BEFORE the handler, so a refused clerk never reaches the parameter check. An
	// Owner on this same bare path gets 400 (see the arm below), and asserting the
	// two different refusals is what proves the 403 is the ACL and not a malformed
	// request — a test that sent a valid budget_id would still pass if the 403 came
	// from somewhere else entirely.
	path := base + "/budget-variance"
	if rec, _ := aclRequest(t, s, http.MethodGet, path, dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("Data Entry read /budget-variance = %d, want 403 — it is what the owner "+
			"PLANNED set beside the actual: the strategy, every line the business is failing "+
			"against, and the payroll total by inference", rec.Code)
	}

	// ⛔ THE OWNER'S ANSWER IS 400, NOT 200, AND ASSERTING 200 WOULD FAIL ON CORRECT
	// CODE. There is no default plan to compare against, so a bare request is
	// refused — and the refusal NAMES the budgets this company has. The point of
	// this arm is that the Owner is stopped by a MISSING PARAMETER and the clerk by
	// a POLICY DECISION: two different answers, and a test that blurred them would
	// hide the day one became the other.
	// ⚠️ BOTH REQUIRED PARAMETERS ARE ASSERTED, IN THE ORDER THE SERVICE CHECKS
	// THEM, rather than one of them being assumed. The dimension check is pure and
	// runs before the budget is resolved (which needs a query), so a bare request
	// names the DIMENSION — and a request that supplies a dimension then names the
	// BUDGET. Pinning both is what proves neither refusal has quietly become a
	// default: a report that silently picked a grain would render a page of correct
	// numbers about the wrong question, and one that picked a plan would compare
	// against a plan nobody chose.
	for _, tc := range []struct{ query, mustName, why string }{
		{"", "dimension",
			"the three grains answer different questions, so there is no default grain"},
		{"?dimension=LEDGER", "budget_id",
			"there is no default plan: an empty variance report reads as \"nothing was " +
				"planned and nothing was spent\", which is a real and reassuring claim " +
				"about a company and would be a lie"},
	} {
		rec, _ := aclRequest(t, s, http.MethodGet, path+tc.query, ownerTok, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("Owner read /budget-variance%s = %d, want 400 — 0086 must narrow Data "+
				"Entry, not the Owner, and the Owner's refusal must be about a missing "+
				"parameter rather than about permission", tc.query, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), tc.mustName) {
			t.Errorf("the Owner's 400 for /budget-variance%s does not name %q, the parameter "+
				"that is missing: %s — %s", tc.query, tc.mustName, rec.Body.String(), tc.why)
		}
	}

	// ⛔ AND THE CLERK IS STILL 403 WITH BOTH PARAMETERS PRESENT. Without this arm
	// the refusal above could be coming from anywhere: the PDP runs before the
	// handler, so a 403 on a malformed request proves the path is guarded but not
	// that a WELL-FORMED read is. This is the arm a reversed policy would flip.
	if rec, _ := aclRequest(t, s, http.MethodGet,
		path+"?dimension=LEDGER", dataEntry, nil); rec.Code != http.StatusForbidden {
		t.Errorf("Data Entry read a well-formed /budget-variance?dimension=LEDGER = %d, "+
			"want 403", rec.Code)
	}
}

// TestDataEntryIsRefusedTheBudgetMasterWithNoACLRowsAtAll is the CONTRAST that
// makes 0086's design rule legible, and it is the more instructive of the two.
//
// ⛔ THE SHAPE OF THE NAME DECIDES THE DEFAULT. `report:budget-variance` is a
// `report:NAME`, arrives GRANTED by 0011's coarse rule, and needs a migration to
// take it back. `budget` is a BARE token, so the level's default_access = 'none'
// refuses it with ZERO RULES WRITTEN — the same authority tds-challan, tds-profile,
// cost-centre, sales-order, purchase-order and bom all shipped under.
//
// ⚠️ SO A FAILURE HERE DOES NOT MEAN "ADD RULES FOR `budget`". It means the bare-
// token default has changed, which would silently un-refuse six other masters too.
func TestDataEntryIsRefusedTheBudgetMasterWithNoACLRowsAtAll(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/budgets"},
		{http.MethodPost, "/budgets"},
	} {
		if rec, _ := aclRequest(t, s, tc.method, base+tc.path, dataEntry,
			map[string]any{"name": "X", "from_date": "2026-04-01", "to_date": "2027-03-31",
				"created_by": "clerk"}); rec.Code != http.StatusForbidden {
			t.Errorf("Data Entry %s %s = %d, want 403. A budget states what the owner "+
				"INTENDS and setting one is not clerical work — and this refusal costs NO "+
				"acl_rule rows at all, because `budget` is a bare token and the level's "+
				"default_access is 'none'", tc.method, tc.path, rec.Code)
		}
	}

	// ⛔ AND THE PROOF THAT IT COSTS NOTHING: no rule on the level mentions the
	// master token. If a later change adds one, this fails — and it should, because
	// adding rows for a bare token is how a reader concludes bare tokens need them.
	_, body := aclRequest(t, s, http.MethodGet, base+"/security-levels", ownerTok, nil)
	levels, _ := body["security_levels"].([]any)
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
			if res, _ := rm["resource"].(string); res == "budget" {
				effect, _ := rm["effect"].(string)
				action, _ := rm["action"].(string)
				t.Errorf("the Data Entry level carries %q %q on the BARE `budget` token. It "+
					"needs none: default_access = 'none' already refuses every token no rule "+
					"names, and writing rows for a bare token teaches the next reader that "+
					"bare tokens require them — which is how six other masters would start "+
					"acquiring pointless migrations", effect, action)
			}
		}
	}
	if !found {
		t.Fatal("no Data Entry level in the listing — the seeder did not run")
	}
}

// TestTheBudgetPrintRefusalIsDurabilityAndNotABehaviourChange exists because the
// honest thing is to SAY SO IN A TEST.
//
// ⛔⛔ 0086'S `disallow print` ROW CHANGES NOTHING OBSERVABLE TODAY, AND A PROBE
// THAT COUNTED IT AS A WIN WOULD BE LYING. Data Entry is ALREADY refused the print
// form before that migration exists: 0038 seeds ZERO `allow print report:NAME`
// rows, 0011's coarse grant is action 'display' only, and the level's
// default_access is 'none'. The row is written for the reason 0038's own header
// gives — a report that cannot be displayed but CAN be printed is a leak with a
// paper trail — and so that the day anyone seeds a coarse print grant, this name is
// not swept up in it.
func TestTheBudgetPrintRefusalIsDurabilityAndNotABehaviourChange(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	dataEntry := aclSeededDataEntryToken(t, s, base, ownerTok)
	path := base + "/print/reports/budget-variance"

	// Data Entry: 403. ⚠️ TRUE BEFORE 0086 TOO — see the doc comment.
	if rec := printGet(t, s, path, dataEntry); rec.Code != http.StatusForbidden {
		t.Errorf("Data Entry print budget-variance = %d, want 403", rec.Code)
	}

	// ⛔ THE OWNER ARM IS 501, NOT 200. No printed layout is specified for a variance
	// statement — the screen legitimately suppresses a grand total when a plan mixes
	// bases, and paper has no affordance for that caveat.
	rec := printGet(t, s, path, ownerTok)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("Owner print budget-variance = %d, want 501 (no printed layout is "+
			"specified for it)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "budget-variance") {
		t.Errorf("the 501 must NAME the report so an operator knows which one has no "+
			"layout: %s", rec.Body.String())
	}
}

// TestTheBudgetRulesReachEveryExistingCompanyToo is 0086's BACKFILL half, and the
// out-of-band oracle for the whole file.
//
// ⛔ A 403 CAN BE PRODUCED BY A DOZEN THINGS — a stale membership, a wrong token, a
// company that does not exist — and only the RULES THEMSELVES prove which rule
// answered. GET /security-levels is the administrator's own view of the level, so
// this asserts what a human would actually be shown. It reads through the product's
// surface rather than the table because acl_rule is RLS-protected, and a test that
// reached around RLS would be asserting something no caller can.
func TestTheBudgetRulesReachEveryExistingCompanyToo(t *testing.T) {
	s := payrollServer(t)
	companyID, ownerTok := aclOwnerCompany(t, s)

	rec, body := aclRequest(t, s, http.MethodGet,
		"/v1/companies/"+companyID+"/security-levels", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list security levels = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// ⛔ THE KEY IS `security_levels`, NOT `levels`. An absent projection has a
	// falsy value that compares cleanly against anything, so the discipline is to
	// make the probe THROW — which is what the not-found Fatalf below does.
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
			if res != "report:budget-variance" {
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
		"disallow display report:budget-variance": false,
		"disallow print report:budget-variance":   false,
	}
	for _, g := range got {
		if _, ok := want[g]; !ok {
			// ⛔ AND NO ALLOW WAS SEEDED. 0086 seeds two disallows and no allow. An
			// `allow` would land here and fail loudly rather than being averaged into
			// a count — and note that an allow could not even WIN: it would tie at
			// specificity 3 and Disallow beats Allow at equal specificity, so reversing
			// this decision means DELETING the disallow, not adding an allow.
			t.Errorf("unexpected budget rule %q — 0086 seeds two disallows and no allow", g)
			continue
		}
		want[g] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing acl_rule %q (got %v) — every company created on a fresh "+
				"deployment would hand its Data Entry level the owner's own plan", k, got)
		}
	}
}

// ---------------------------------------------------------------------------
// VOCABULARY PINS
// ---------------------------------------------------------------------------

// TestTheBudgetMasterTokenIsNotAReportName is the one-line guard against the
// mistake that would make migration 0086 dead text and hand the master to every
// clerk.
//
// server.go loops over authz.KnownReportNames and auto-mounts
// GET /print/reports/<name>; a name with no reportName() case is a route
// routeAction cannot map, i.e. default-DENY for every level INCLUDING the Owner.
// That bug already shipped once for reconciliation-queue.
func TestTheBudgetMasterTokenIsNotAReportName(t *testing.T) {
	for _, n := range authz.KnownReportNames {
		if n == "budget" {
			t.Fatal("`budget` is in KnownReportNames. It is the MASTER token and must be a " +
				"bare resource: as a report name, migration 0011's coarse `allow display " +
				"report` would GRANT it to every seeded Data Entry level, and server.go would " +
				"auto-mount /print/reports/budget with no reportName() case — default-denying " +
				"even the Owner")
		}
	}
	if !authz.ValidResource("budget") {
		t.Error("`budget` is not a valid bare resource, so no administrator can write a rule " +
			"about the budget master at all — the token would be ungovernable")
	}
	if !authz.ValidResource("report:budget-variance") {
		t.Error("`report:budget-variance` is not a valid resource, so migration 0086's rows " +
			"name something the vocabulary rejects and the admin API could never restate them")
	}
}

// TestBudgetVocabularyMatchesMasters pins the two packages' copies of the
// dimension and basis strings together.
//
// ⛔ THEY ARE DELIBERATELY DUPLICATED AND MUST NOT BE DE-DUPLICATED BY IMPORT.
// internal/reporting depends only on the db interface and never on a sibling write
// module (its package header says so), so it cannot import internal/masters. Both
// spell the strings migration 0085's CHECK constraints store, and a drift between
// them is silent: masters would write "COST_CENTRE" while reporting queried for
// something else, and the report would return an empty budget column — which reads
// as "nothing was planned" rather than as a bug.
func TestBudgetVocabularyMatchesMasters(t *testing.T) {
	if len(reporting.KnownBudgetDimensions) != len(masters.KnownBudgetDimensions) {
		t.Fatalf("reporting knows %d budget dimensions and masters knows %d: a dimension "+
			"masters can write but reporting cannot query is a budget line that silently "+
			"never appears on any report",
			len(reporting.KnownBudgetDimensions), len(masters.KnownBudgetDimensions))
	}
	for i, d := range masters.KnownBudgetDimensions {
		if reporting.KnownBudgetDimensions[i] != d {
			t.Errorf("dimension %d: masters writes %q, reporting queries %q — the report's "+
				"budget column would come back EMPTY for that grain, which reads as \"nothing "+
				"was planned\"", i, d, reporting.KnownBudgetDimensions[i])
		}
	}
	if masters.BasisClosingBalance != reporting.BudgetBasisClosing {
		t.Errorf("the CLOSING_BALANCE basis is %q in masters and %q in reporting: every "+
			"closing-balance line would be measured as movement instead of as a position, "+
			"which is a real, correctly-footed, confidently wrong number",
			masters.BasisClosingBalance, reporting.BudgetBasisClosing)
	}
	if masters.BasisNettTransactions != reporting.BudgetBasisNett {
		t.Errorf("the NETT_TRANSACTIONS basis is %q in masters and %q in reporting",
			masters.BasisNettTransactions, reporting.BudgetBasisNett)
	}
}
