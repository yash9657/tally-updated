package compliance_test

// THE STATUTORY SECTION TABLES, EMITTED FOR THE FRONTEND (register row D3).
//
// ⛔ WHY A GOLDEN AND NOT A COMMENT SAYING "KEEP THESE IN SYNC". The entry form
// that closes D3 has to OFFER the sections, and its mock has no server to ask —
// so `frontend/src/api/mock.ts` carries the only copy of this table outside Go.
// A hand-kept copy of a table that changes with every Finance Act is the drift
// bug the TDS challan screen's rule (3) already records for three minor heads;
// this one has seven rows, two rate columns and a threshold each. So the copy is
// PINNED: this test writes what the ruleset actually holds, and the frontend
// asserts its tables against the same file. A section added to the registry and
// forgotten in the mock fails HERE, naming the diff, rather than shipping a
// picker that is missing a section.
//
// It needs no database and no server, deliberately: a golden guarded by a
// connection is a golden that silently stops being checked when the connection
// is down (a t.Skip exits 0). It reads the registry directly.
//
// ⚠️ IT IS AN EXTERNAL TEST PACKAGE (`compliance_test`) so it can import
// internal/ledger for the DEFAULT ruleset versions. internal/ledger imports
// compliance, so an in-package test importing it back is an import cycle — and
// naming the two versions as string literals here would be a third copy of the
// very fact this golden exists to pin.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/ledger"
)

// goldenPath reaches out of the Go tree into the frontend's, because the
// artefact's whole purpose is to be consumed there. One constant, so the
// coupling is visible.
const taxSectionsGoldenPath = "../../../frontend/src/api/goldens/tax-sections.json"

func TestTaxSectionsGolden(t *testing.T) {
	tds, err := compliance.LoadTDSRuleSet(ledger.DefaultTDSRulesetVersion)
	if err != nil {
		t.Fatalf("load tds ruleset %q: %v", ledger.DefaultTDSRulesetVersion, err)
	}
	tcs, err := compliance.LoadTCSRuleSet(ledger.DefaultTCSRulesetVersion)
	if err != nil {
		t.Fatalf("load tcs ruleset %q: %v", ledger.DefaultTCSRulesetVersion, err)
	}
	mul, floor, ceil := compliance.TCSNoPANPct(tcs)

	// EXACTLY the shape handleTDSSections serves, so the frontend asserts against
	// the wire contract and not against a second, tidier one invented here.
	golden := struct {
		TDSRulesetVersion  string                  `json:"tds_ruleset_version"`
		TCSRulesetVersion  string                  `json:"tcs_ruleset_version"`
		TDSSections        []compliance.SectionDoc `json:"tds_sections"`
		TCSSections        []compliance.SectionDoc `json:"tcs_sections"`
		TDSNoPANFloorPct   string                  `json:"tds_no_pan_floor_pct"`
		TCSNoPANMultiplier string                  `json:"tcs_no_pan_multiplier"`
		TCSNoPANFloorPct   string                  `json:"tcs_no_pan_floor_pct"`
		TCSNoPANCeilingPct string                  `json:"tcs_no_pan_ceiling_pct"`
		ThresholdApprox    bool                    `json:"threshold_basis_is_approximated"`
	}{
		TDSRulesetVersion:  tds.Version,
		TCSRulesetVersion:  tcs.Version,
		TDSSections:        compliance.TDSSectionCatalog(tds),
		TCSSections:        compliance.TCSSectionCatalog(tcs),
		TDSNoPANFloorPct:   compliance.TDSNoPANFloorPct(tds),
		TCSNoPANMultiplier: mul,
		TCSNoPANFloorPct:   floor,
		TCSNoPANCeilingPct: ceil,
		ThresholdApprox:    true,
	}

	// Sanity before anything is written. An EMPTY catalog would serialise
	// happily and teach the form that this build deducts under no section —
	// a statement about the law, not about the build.
	if len(golden.TDSSections) == 0 || len(golden.TCSSections) == 0 {
		t.Fatalf("the golden would be written with %d TDS and %d TCS sections",
			len(golden.TDSSections), len(golden.TCSSections))
	}

	got, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	if os.Getenv("UPDATE_GOLDENS") != "" {
		if err := os.MkdirAll(filepath.Dir(taxSectionsGoldenPath), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(taxSectionsGoldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("regenerated %s (%d bytes)", taxSectionsGoldenPath, len(got))
		return
	}

	want, err := os.ReadFile(taxSectionsGoldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v\n\nIf this is the first run, generate it:\n"+
			"  UPDATE_GOLDENS=1 go test ./internal/compliance/ -run TestTaxSectionsGolden",
			taxSectionsGoldenPath, err)
	}
	if string(got) != string(want) {
		t.Errorf("the statutory section table has changed, and frontend/src/api/mock.ts "+
			"carries the only copy of it outside Go.\n\n"+
			"THIS IS NOT A FAILURE TO ROUTE AROUND. A picker missing a section is a "+
			"deduction an operator cannot key, which is register row D3 coming back.\n\n"+
			"Read the diff, decide whether the change is what you meant, then:\n"+
			"  UPDATE_GOLDENS=1 go test ./internal/compliance/ -run TestTaxSectionsGolden\n"+
			"and update MOCK_TDS_SECTIONS / MOCK_TCS_SECTIONS to match; the frontend suite "+
			"asserts them against this file.\n\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
