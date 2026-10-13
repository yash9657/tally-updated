package catalogue

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/query"
)

// TestEveryVocabularyReportHasACatalogueEntry is THE gate the W3 plan names:
// "every report in the authz vocabulary registered with agent metadata ...
// with a completeness test that fails when a report has no entry".
//
// It fails in BOTH directions: a vocabulary report with no entry (the agent
// cannot find it), and an entry for a name the vocabulary does not know (a
// report the agent would try to call and the PDP would refuse).
//
// NON-VACUOUS: delete any Entry from entries.go -> "no catalogue entry";
// rename one Report -> both messages. Measured in the ADR.
func TestEveryVocabularyReportHasACatalogueEntry(t *testing.T) {
	known := map[string]bool{}
	for _, n := range authz.KnownReportNames {
		known[n] = true
	}
	seen := map[string]bool{}
	for _, e := range reportEntries {
		if seen[e.Report] {
			t.Errorf("duplicate catalogue entry for %q (the second silently overwrites the first in the lookup map)", e.Report)
		}
		seen[e.Report] = true
		if !known[e.Report] {
			t.Errorf("catalogue entry %q names no report in authz.KnownReportNames", e.Report)
		}
	}
	for _, n := range authz.KnownReportNames {
		if !seen[n] {
			t.Errorf("report %q is in authz.KnownReportNames but has no catalogue entry — the agent cannot find it", n)
		}
	}
	if len(Reports()) != len(authz.KnownReportNames) {
		t.Errorf("Reports() returned %d entries for %d vocabulary reports", len(Reports()), len(authz.KnownReportNames))
	}
	t.Logf("%d reports, %d catalogue entries", len(authz.KnownReportNames), len(reportEntries))
}

// TestEveryEntryIsComplete pins the MINIMUM metadata an entry must carry to be
// useful to a model, and that every example DSL still compiles.
func TestEveryEntryIsComplete(t *testing.T) {
	withDSL, exact := 0, 0
	for _, e := range reportEntries {
		if strings.TrimSpace(e.Title) == "" || len(e.Description) < 40 {
			t.Errorf("%s: title and a real description (>= 40 chars) are required", e.Report)
		}
		if len(e.Synonyms) < 2 {
			t.Errorf("%s: at least 2 English synonyms, got %d", e.Report, len(e.Synonyms))
		}
		if len(e.Hinglish) < 1 {
			t.Errorf("%s: at least 1 Hinglish phrasing is required", e.Report)
		}
		if len(e.Questions) < 2 {
			t.Errorf("%s: at least 2 typical questions, got %d", e.Report, len(e.Questions))
		}
		if len(e.DSL) == 0 {
			if strings.TrimSpace(e.NoDSLReason) == "" {
				t.Errorf("%s: no example DSL and no NoDSLReason — say why the DSL cannot answer it", e.Report)
			}
			if e.DSLFidelity != "" || e.ParityTest != "" {
				t.Errorf("%s: fidelity/parity claimed with no DSL", e.Report)
			}
			continue
		}
		withDSL++
		if e.NoDSLReason != "" {
			t.Errorf("%s: has DSL AND a NoDSLReason; pick one", e.Report)
		}
		switch e.DSLFidelity {
		case "exact":
			exact++
			if e.ParityTest == "" {
				t.Errorf("%s: an \"exact\" example must name the parity test that pins it", e.Report)
			}
		case "partial":
			if e.ParityTest != "" {
				t.Errorf("%s: a partial example cannot cite a parity test", e.Report)
			}
		default:
			t.Errorf("%s: DSLFidelity must be exact or partial, got %q", e.Report, e.DSLFidelity)
		}
		for i, q := range e.DSL {
			if _, _, err := query.Compile(q); err != nil {
				t.Errorf("%s: example DSL %d does not compile: %v", e.Report, i, err)
			}
		}
	}
	t.Logf("%d entries with DSL (%d exact), %d without", withDSL, exact, len(reportEntries)-withDSL)
}

// TestEveryExactClaimHasItsParityTest finds each named ParityTest as a real
// `func Name(t *testing.T)` in the backend tree. A label a model trusts must
// not outlive the test that earned it.
//
// NON-VACUOUS: rename TestExecute_StockMovementsMatchTheStockMovementReport
// and this fails naming it.
func TestEveryExactClaimHasItsParityTest(t *testing.T) {
	want := map[string]bool{}
	for _, e := range reportEntries {
		if e.ParityTest != "" {
			want[e.ParityTest] = false
		}
	}
	root := filepath.Join("..", "..")
	re := regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`)
	scanned := 0
	err := filepath.Walk(filepath.Join(root, "internal"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		scanned++
		for _, mm := range re.FindAllStringSubmatch(string(b), -1) {
			if _, ok := want[mm[1]]; ok {
				want[mm[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if scanned < 50 {
		t.Fatalf("scanned %d test files — the walk is vacuous", scanned)
	}
	for name, found := range want {
		if !found {
			t.Errorf("ParityTest %q is cited by the catalogue but no such test exists", name)
		}
	}
}

// TestEveryDSLEntityHasAnEntityEntry — the second hop is complete too.
func TestEveryDSLEntityHasAnEntityEntry(t *testing.T) {
	seen := map[query.Entity]bool{}
	for _, e := range entityEntries {
		seen[e.Entity] = true
		if len(e.Description) < 20 || len(e.Synonyms) == 0 || len(e.Hinglish) == 0 {
			t.Errorf("entity %s: description, synonyms and Hinglish are all required", e.Entity)
		}
	}
	for _, d := range query.Catalog() {
		if !seen[query.Entity(d.Entity)] {
			t.Errorf("DSL entity %q has no catalogue entity entry", d.Entity)
		}
	}
	if len(entityEntries) != len(query.Catalog()) {
		t.Errorf("%d entity entries for %d DSL entities", len(entityEntries), len(query.Catalog()))
	}
}

// TestEveryReportHasADisplayAction binds the catalogue to the GENERATED
// action registry: every report the agent can find, it can also call. A
// report in the vocabulary with no display route would be one the catalogue
// advertises and the agent cannot read.
func TestEveryReportHasADisplayAction(t *testing.T) {
	for _, e := range Reports() {
		acts := e.DisplayActions()
		if len(acts) == 0 {
			t.Errorf("%s: no display ActionSpec in the generated registry", e.Report)
			continue
		}
		for _, a := range acts {
			if a.IsWrite {
				t.Errorf("%s: display action %s is a write?", e.Report, a.ID)
			}
		}
	}
}
