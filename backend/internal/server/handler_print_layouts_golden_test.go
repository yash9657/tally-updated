package server

// WHICH REPORTS ACTUALLY PRINT — DERIVED FROM THE HANDLER, NOT DECLARED.
//
// ⛔ WHY A GOLDEN AND NOT A LIST IN TYPESCRIPT. Alt+E can now export a report as
// PDF, and it does that by asking the SAME print route Ctrl+P uses. But only ten
// of the thirty names in authz.KnownReportNames have a printed layout; the other
// twenty answer an honest 501. Offering "export as PDF" on one of those twenty is
// a menu item that fails AFTER the operator chooses it, so the frontend has to
// know which is which — and the moment it knows by holding its own copy of the
// list, the copy starts drifting from the switch in handler_print_reports.go.
//
// This repository has been here before, twice, and the answer it settled on is
// the tds-register-prose golden: GENERATE the artefact from production code and
// let the frontend consume it. `internal/reporting`'s golden test carries the
// argument in full. This is the same mechanism for the same reason.
//
// ⛔ AND THE LIST IS MEASURED, NOT TYPED. Every name is requested and the answer
// is read: 501 means no layout, anything else means there is one. A hand-written
// map — which TestTheUnimplementedReportsAreStillGovernableAndSaySo does keep, on
// purpose, as an independent statement of intent — can be wrong in the direction
// that matters (claiming a layout exists) and stay green. This cannot: the golden
// is whatever the router actually does.
//
// ⚠️ IT SKIPS WITHOUT POSTGRES, AND A SKIP IS INDISTINGUISHABLE FROM A PASS IN THE
// EXIT CODE. That is the hazard `internal/reporting`'s golden avoided by building
// its fixture synthetically, and it cannot be avoided here — deciding whether a
// report prints means running the handler, which means a database. The
// counterweight is on the frontend side: printTarget.test.ts asserts the golden
// has exactly ten entries and names several of them, so a golden that goes empty
// or garbage fails in a suite that always runs.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/khata/cored/internal/authz"
)

// It reaches out of the Go tree into the frontend's, because the artefact's whole
// purpose is to be consumed there. One constant, so the coupling is visible.
const printedLayoutsGoldenPath = "../../../frontend/src/api/goldens/printed-report-layouts.json"

// TestPrintedReportLayoutsGolden pins which report slugs produce a document.
//
// The shape is deliberately a sorted list of the ones that DO print, plus the
// ones that do not, plus the total. Recording BOTH sides is what makes the golden
// readable as evidence: a diff that moves a name from one list to the other says
// what changed, whereas a single list would only say a name appeared.
func TestPrintedReportLayoutsGolden(t *testing.T) {
	s := payrollServer(t)
	f := newPrintFixture(t, s)

	var prints, deferred []string
	for _, name := range authz.KnownReportNames {
		rec := printGet(t, s, f.base+"/print/reports/"+name, f.ownerTok)
		switch rec.Code {
		case http.StatusNotImplemented:
			// The honest gap: the route is mounted and governable, and no layout
			// has been specified for it.
			deferred = append(deferred, name)
		case http.StatusForbidden, http.StatusUnauthorized:
			// ⛔ AN OWNER MUST NEVER BE REFUSED HERE. If this fires, the golden
			// would silently record a printable report as deferred, and Alt+E would
			// stop offering PDF on a report that prints perfectly well.
			t.Errorf("print report %q answered %d for an OWNER: the golden cannot tell a "+
				"missing layout from a missing ACL rule, so this must be fixed rather than "+
				"recorded (%s)", name, rec.Code, rec.Body.String())
		default:
			// ⚠️ 200 AND 400 BOTH MEAN "THERE IS A LAYOUT". bank-reconciliation is
			// the one implemented report that answers 400 to a bare GET, because it
			// needs ?ledger_id= or ?all= and refuses to pick a bank account for you.
			// Treating a 400 as "does not print" would drop it from the golden and
			// take PDF export off the one report whose whole point is being handed
			// to a bank.
			prints = append(prints, name)
		}
	}
	sort.Strings(prints)
	sort.Strings(deferred)

	// Invariants that must hold in every golden this test could produce, checked
	// BEFORE anything is written — a generator that can emit a nonsense artefact is
	// a generator that will.
	if len(prints) == 0 {
		t.Fatal("no report printed at all: the golden would tell the frontend to offer PDF " +
			"nowhere, which looks exactly like the feature being switched off")
	}
	if len(prints)+len(deferred) != len(authz.KnownReportNames) {
		t.Fatalf("accounted for %d of %d report names — a name in neither list is a name the "+
			"frontend cannot reason about", len(prints)+len(deferred), len(authz.KnownReportNames))
	}

	golden := struct {
		Note     string   `json:"note"`
		Total    int      `json:"total_report_names"`
		Prints   []string `json:"prints_a_layout"`
		Deferred []string `json:"answers_501"`
	}{
		Note: "GENERATED by internal/server's TestPrintedReportLayoutsGolden by requesting " +
			"every name in authz.KnownReportNames and reading the answer. Do not edit by hand. " +
			"A report in answers_501 has a mounted, governable route and no printed layout, so " +
			"Alt+E must not offer PDF for it.",
		Total:    len(authz.KnownReportNames),
		Prints:   prints,
		Deferred: deferred,
	}

	got, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	got = append(got, '\n')

	if os.Getenv("UPDATE_GOLDENS") != "" {
		if err := os.MkdirAll(filepath.Dir(printedLayoutsGoldenPath), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(printedLayoutsGoldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("regenerated %s (%d bytes): %d print, %d deferred",
			printedLayoutsGoldenPath, len(got), len(prints), len(deferred))
		return
	}

	want, err := os.ReadFile(printedLayoutsGoldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v\n\nIf this is the first run, generate it:\n"+
			"  UPDATE_GOLDENS=1 go test ./internal/server/ -run TestPrintedReportLayoutsGolden",
			printedLayoutsGoldenPath, err)
	}
	if string(got) != string(want) {
		t.Errorf("which reports print has CHANGED, and the frontend reads this file to decide "+
			"whether Alt+E offers PDF.\n\n"+
			"THIS IS NOT A FAILURE TO ROUTE AROUND. If a name moved into prints_a_layout, the "+
			"export option should start appearing for it; if one moved out, an operator is "+
			"about to be offered a PDF that 501s after they choose it.\n\n"+
			"Read the diff, decide whether it is what you meant, then:\n"+
			"  UPDATE_GOLDENS=1 go test ./internal/server/ -run TestPrintedReportLayoutsGolden\n"+
			"and re-run the frontend suite, which asserts against it.\n\n"+
			"--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
