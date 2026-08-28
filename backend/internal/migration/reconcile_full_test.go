package migration_test

import (
	"path/filepath"
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
	id   string
	stem string
}

var fullCorpora = []fullCorpus{
	{id: "01-opening-trading", stem: "01-opening-trading"},
	{id: "02-multistate-gst", stem: "02-multistate-gst"},
	{id: "03-billwise-outstandings", stem: "03-billwise-outstandings"},
	{id: "04-edited-deleted", stem: "04-edited-deleted"},
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
			im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool), pool)
			if _, err := im.ImportFile(ctx, xmlPath, "fullrecon"); err != nil {
				t.Fatalf("import %s: %v", c.id, err)
			}

			rep := reporting.NewService(pool)
			gen := gstr.NewService(pool)
			ms := masters.NewService(pool)

			if err := migration.ReconcileFull(ctx, rep, gen, ms, companyID, exp); err != nil {
				t.Fatalf("corpus %s FULL reconciliation failed:\n%v", c.id, err)
			}
		})
	}
}

// TestImportRoutesAlterAndDelete asserts the importer dispatches the Tally ACTION
// explicitly: corpus 04 has 6 voucher messages — a duplicate Create (idempotent
// skip), a Create+Alter pair, and a Create+Delete pair — so the result must show
// 3 posted, 1 skipped, 1 altered, 1 deleted. This pins the GUID-keyed routing so
// a future regression where Alter silently falls through to a fresh Create (which
// would double-post) is caught independently of the reconciliation totals.
func TestImportRoutesAlterAndDelete(t *testing.T) {
	root := repoRoot(t)
	pool, ctx, _ := setupCompanyState(t, "")
	im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool), pool)
	res, err := im.ImportFile(ctx, filepath.Join(root, "corpora", "migration", "04-edited-deleted.xml"), "routing")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.VouchersPosted != 3 || res.Skipped != 1 || res.VouchersAltered != 1 || res.VouchersDeleted != 1 {
		t.Fatalf("ACTION routing: posted=%d skipped=%d altered=%d deleted=%d, want 3/1/1/1",
			res.VouchersPosted, res.Skipped, res.VouchersAltered, res.VouchersDeleted)
	}
}

