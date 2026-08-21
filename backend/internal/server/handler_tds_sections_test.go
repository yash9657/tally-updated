package server_test

import (
	"net/http"
	"testing"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/ledger"
)

// TestTDSSectionsServesTheStatutoryVocabulary is register row D3's server half:
// the form that lets an operator key a deduction must be able to OFFER the
// sections, and it must not carry a copy of the Finance Act to do it.
//
// ⛔ THE ORACLE IS internal/compliance, NOT THIS RESPONSE ECHOING ITSELF. The
// test asserts the served list equals the ruleset's own catalog — so a section
// added to the registry and forgotten here, or a hand-edited response, fails.
func TestTDSSectionsServesTheStatutoryVocabulary(t *testing.T) {
	srv := newServer(t)
	base := seedSalesCompany(t, srv)

	rec, body := do(t, srv, http.MethodGet, base+"/tds-sections", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tds-sections status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	tdsRules, err := compliance.LoadTDSRuleSet(ledger.DefaultTDSRulesetVersion)
	if err != nil {
		t.Fatalf("load tds ruleset: %v", err)
	}
	want := compliance.TDSSectionCatalog(tdsRules)
	got, _ := body["tds_sections"].([]any)
	if len(got) != len(want) {
		t.Fatalf("served %d tds sections, the ruleset has %d", len(got), len(want))
	}
	for i, w := range want {
		row, _ := got[i].(map[string]any)
		if row["section"] != w.Section {
			t.Errorf("section %d: served %v, ruleset %q", i, row["section"], w.Section)
		}
		// The RATE travels with the key: choosing between 194J-P and 194J-T is
		// choosing between 10% and 2%, and a bare key hides that.
		if row["rate_other_pct"] != w.RateOtherPct {
			t.Errorf("%s: served rate %v, ruleset %q", w.Section, row["rate_other_pct"], w.RateOtherPct)
		}
		if row["threshold_rupees"] != w.ThresholdRupees {
			t.Errorf("%s: served threshold %v, ruleset %q", w.Section, row["threshold_rupees"], w.ThresholdRupees)
		}
	}
	if body["tds_ruleset_version"] != tdsRules.Version {
		t.Errorf("served ruleset %v, want %q", body["tds_ruleset_version"], tdsRules.Version)
	}

	tcsRules, err := compliance.LoadTCSRuleSet(ledger.DefaultTCSRulesetVersion)
	if err != nil {
		t.Fatalf("load tcs ruleset: %v", err)
	}
	tcs, _ := body["tcs_sections"].([]any)
	if len(tcs) != len(compliance.TCSSectionCatalog(tcsRules)) {
		t.Fatalf("served %d tcs sections, the ruleset has %d",
			len(tcs), len(compliance.TCSSectionCatalog(tcsRules)))
	}

	// ⛔ THE TWO NO-PAN RULES ARE DIFFERENT SHAPES and a client that assumed
	// otherwise would print 20% where the answer is 5%. Both are served.
	if body["tds_no_pan_floor_pct"] != compliance.TDSNoPANFloorPct(tdsRules) {
		t.Errorf("tds no-PAN floor served as %v", body["tds_no_pan_floor_pct"])
	}
	mul, floor, ceil := compliance.TCSNoPANPct(tcsRules)
	if body["tcs_no_pan_multiplier"] != mul ||
		body["tcs_no_pan_floor_pct"] != floor ||
		body["tcs_no_pan_ceiling_pct"] != ceil {
		t.Errorf("tcs no-PAN rule served as %v/%v/%v, want %s/%s/%s",
			body["tcs_no_pan_multiplier"], body["tcs_no_pan_floor_pct"],
			body["tcs_no_pan_ceiling_pct"], mul, floor, ceil)
	}
	// The per-transaction approximation is DISCLOSED: the form will show a
	// threshold and an operator will read it as the law.
	if body["threshold_basis_is_approximated"] != true {
		t.Error("the served catalog does not disclose that v1 applies every basis per transaction")
	}
}
