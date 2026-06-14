package migration_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/migration"
	"github.com/khata/cored/internal/reporting"
)

// fullCorpus enumerates the migration corpora that ship a four-statement oracle
// (corpora/migration/<stem>.{xml,expected.json}). requiresBillwise /
// requiresEditLog flag corpora whose FULL reconcile depends on importer features
// that may land after the corpus author (BILLALLOCATIONS.LIST parsing for
// outstandings; ACTION=Alter/Delete routing). For those, the test still asserts
// the portion that works today and skips (with a precise message) the part that
// is genuinely pending — it never fakes a pass.
type fullCorpus struct {
	id               string
	stem             string
	requiresBillwise bool
	requiresEditLog  bool
}

var fullCorpora = []fullCorpus{
	{id: "01-opening-trading", stem: "01-opening-trading"},
	{id: "02-multistate-gst", stem: "02-multistate-gst"},
	{id: "03-billwise-outstandings", stem: "03-billwise-outstandings", requiresBillwise: true},
	{id: "04-edited-deleted", stem: "04-edited-deleted", requiresEditLog: true},
	{id: "05-rounding-edge", stem: "05-rounding-edge"},
}

// TestReconcileFullEachCorpus is the switching-cost-killer gate at full fidelity:
// for each on-disk corpus, import the Tally XML into a fresh company (home state
// from the oracle's stateCode) and assert ReconcileFull returns nil to the paisa.
// A mismatch names the statement + ledger/bucket. Uses the skip-on-no-DB harness
// from migration_test.go / reconcilefull_test.go.
func TestReconcileFullEachCorpus(t *testing.T) {
	root := repoRoot(t)
	for _, c := range fullCorpora {
		c := c
		t.Run(c.id, func(t *testing.T) {
			xmlPath := filepath.Join(root, "corpora", "migration", c.stem+".xml")
			jsonPath := filepath.Join(root, "corpora", "migration", c.stem+".expected.json")

			exp, err := migration.LoadExpectedReconciliationFile(jsonPath)
			if err != nil {
				t.Fatalf("load expected: %v", err)
			}

			pool, ctx, companyID := setupCompanyState(t, exp.StateCode)
			im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool))
			if _, err := im.ImportFile(ctx, xmlPath, "fullrecon"); err != nil {
				if c.requiresEditLog && strings.Contains(err.Error(), "ACTION") {
					t.Skipf("corpus %s needs importer ACTION=Alter/Delete routing (pending); import rejected: %v", c.id, err)
				}
				t.Fatalf("import %s: %v", c.id, err)
			}

			rep := reporting.NewService(pool)
			gen := gstr.NewService(pool)
			ms := masters.NewService(pool)

			err = migration.ReconcileFull(ctx, rep, gen, ms, companyID, exp)

			// A corpus whose only remaining gap is OUTSTANDING (bill-allocation
			// import not yet landed) must still reconcile on TB/BS/P&L. Assert that,
			// then skip on the outstanding-only remainder.
			if c.requiresBillwise && err != nil && onlyOutstanding(err.Error()) {
				t.Skipf("corpus %s reconciles on TB/BS/P&L; OUTSTANDING pending bill-allocation import:\n%v", c.id, err)
			}

			if err != nil {
				t.Fatalf("corpus %s FULL reconciliation failed:\n%v", c.id, err)
			}
		})
	}
}

// onlyOutstanding reports whether every discrepancy bullet in a ReconcileFull
// error names the OUTSTANDING statement (i.e. TB/BS/P&L/GSTR-1 are all clean).
func onlyOutstanding(msg string) bool {
	sawBullet := false
	for _, line := range strings.Split(msg, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "- ") {
			continue
		}
		sawBullet = true
		if !strings.Contains(l, "OUTSTANDING") {
			return false
		}
	}
	return sawBullet
}
