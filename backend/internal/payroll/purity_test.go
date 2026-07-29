package payroll

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// bannedRounders is the set of method names that ROUND, and which therefore may
// appear only in rounding.go.
//
// It is a name-based scan rather than a type-checked one on purpose: a
// type-checking pass would need go/types and the whole build graph, whereas this
// runs from a bare parse in milliseconds and can never be skipped for being
// slow. The cost is that it also fires on an unrelated method that happens to be
// called Round — which is the right trade, because the fix is to rename or to
// route through rounding.go, and both are improvements.
//
// Div is on the list and DivRound is NOT, which is deliberate:
// decimal.Decimal.Div consults decimal.DivisionPrecision, a MUTABLE PACKAGE
// GLOBAL that any other package's init() could change, so a division whose
// precision is set somewhere else entirely would silently re-price everybody's
// contributions. DivRound takes its precision as an argument and is therefore
// safe (internal/inventory makes exactly this distinction for the same reason).
var bannedRounders = map[string]string{
	"Round":      "the statutory rounding depends on WHICH statute; use RoundPaise, RoundNearestRupee, RoundUpRupee or RoundNearestTen",
	"RoundUp":    "use RoundUpRupee, which names the ESI rule it implements",
	"RoundDown":  "no payroll statute rounds down; if one does, add a named helper to rounding.go with its citation",
	"RoundBank":  "banker's rounding is not an Indian statutory rounding rule",
	"RoundCash":  "cash rounding is not an Indian statutory rounding rule",
	"RoundCeil":  "use RoundUpRupee",
	"RoundFloor": "no payroll statute rounds down",
	"Ceil":       "use RoundUpRupee, which is ESI-only and says so",
	"Floor":      "no payroll statute rounds down",
	"Truncate":   "truncation silently under-states every fractional amount",
	"Div":        "Div reads the mutable global decimal.DivisionPrecision; use DivRound with an explicit precision, or Shift for a power of ten",
}

// TestRoundingHelpersAreTheOnlyRounders is the mechanical guard that stops one
// rounding rule from spreading across the package.
//
// Payroll has FOUR statutory roundings with THREE different units — paise for
// ordinary money, whole rupees for tax and EPF/EPS, a CEILING to whole rupees
// for ESI, and nearest ten for total income. Applying one of them everywhere is
// the single most likely wrong-number bug in this track, and it is the kind of
// change that looks like a tidy-up in review: a `.Round(2)` added to payrun.go
// six months from now would silently change every employee's net pay, and no
// existing test would notice because the fixtures would be regenerated to match.
//
// So the rule is structural: only rounding.go may call a rounding method. Every
// other file must call one of the four NAMED functions, whose name states which
// statute it obeys.
func TestRoundingHelpersAreTheOnlyRounders(t *testing.T) {
	fset := token.NewFileSet()
	scanned, checked := 0, 0

	for _, name := range packageGoFiles(t, false) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		if name == "rounding.go" {
			continue // the one file allowed to round
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if why, banned := bannedRounders[sel.Sel.Name]; banned {
				t.Errorf("%s:%d calls .%s(). Only rounding.go may round: %s",
					name, fset.Position(sel.Sel.Pos()).Line, sel.Sel.Name, why)
			}
			return true
		})
	}

	if scanned < 4 {
		t.Fatalf("scanned %d non-test files; the scan is vacuous (expected payroll.go, rounding.go, rates.go, wages.go at least)", scanned)
	}
	if checked == 0 {
		t.Fatal("every scanned file was rounding.go; the scan checked nothing")
	}

	// And prove the scan can actually see a banned call: rounding.go itself is
	// full of them, so parsing it with the exemption removed must produce hits.
	// Without this, a bug in the AST walk would leave the test passing forever.
	f, err := parser.ParseFile(fset, "rounding.go", nil, 0)
	if err != nil {
		t.Fatalf("parse rounding.go: %v", err)
	}
	hits := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if _, banned := bannedRounders[sel.Sel.Name]; banned {
				hits++
			}
		}
		return true
	})
	if hits < 4 {
		t.Fatalf("the AST walk found only %d rounding calls inside rounding.go, which contains at least four. "+
			"The walk is broken and would not catch a rounding call elsewhere either", hits)
	}
	t.Logf("rounding confined to rounding.go across %d files (%d checked, %d rounding calls seen in the walk's self-test)",
		scanned, checked, hits)
}

// allowedImports is the complete non-test import set of internal/payroll.
//
// It is FIVE packages, and every one of them is there because the arithmetic
// needs it. What is absent is the point:
//
//   - no "context", no "database/sql", no platform/db: the engine cannot reach a
//     database, so a payslip is a function of its inputs and can be recomputed
//     years later, byte for byte;
//   - no "time.Now" caller and no clock package beyond the calendar types: a
//     recomputation must not depend on when it runs;
//   - no "log" or "log/slog" and no fmt.Print*: this leaf handles salary figures
//     and must never write one anywhere;
//   - no internal/compliance: see the package doc — its ThresholdBasis carries a
//     per-transaction approximation that would be a different computation for
//     salary, and the archtest boundary for internal/payroll is the empty map so
//     the edge cannot be added by accident.
var allowedImports = map[string]bool{
	"errors":                        true,
	"fmt":                           true,
	"sort":                          true,
	"time":                          true,
	"github.com/shopspring/decimal": true,
}

// TestPayrollImportsNothingButStdlibAndDecimal pins the purity of the leaf at
// the import line, which is the only place it can be broken.
//
// archtest's boundary map already forbids sibling internal packages; this test
// is stricter and local: it forbids the STANDARD LIBRARY packages that would
// make the engine impure — a clock, a socket, a file handle, a logger — none of
// which archtest looks at.
func TestPayrollImportsNothingButStdlibAndDecimal(t *testing.T) {
	fset := token.NewFileSet()
	scanned := 0
	for _, name := range packageGoFiles(t, false) {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !allowedImports[path] {
				t.Errorf("%s imports %q, which is outside the payroll engine's allowed set %v. "+
					"This leaf must stay a pure function of its inputs: no I/O, no clock, no logging, and no "+
					"sibling package — a payslip that depends on anything else cannot be recomputed",
					name, path, sortedKeys(allowedImports))
			}
		}
	}
	if scanned < 4 {
		t.Fatalf("scanned %d non-test files; the import scan is vacuous", scanned)
	}
	t.Logf("import purity checked across %d source files", scanned)
}

// TestPayrollDoesNotLog is the PII guard.
//
// Every number this package handles is somebody's salary, and the leaf is
// deliberately given no identifier at all (PANAvailable, never a PAN). A log
// line would be the one way a salary figure could leave the engine into a place
// with no access control — and application logs are read by people who are not
// allowed to see individual pay.
func TestPayrollDoesNotLog(t *testing.T) {
	fset := token.NewFileSet()
	scanned := 0
	for _, name := range packageGoFiles(t, false) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "log" || path == "log/slog" {
				t.Errorf("%s imports %q. Salary figures must never be written to a log: application logs have no "+
					"per-employee access control and are read by people who may not see individual pay", name, path)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "fmt" {
				return true
			}
			if strings.HasPrefix(sel.Sel.Name, "Print") || strings.HasPrefix(sel.Sel.Name, "Fprint") {
				t.Errorf("%s:%d calls fmt.%s. This leaf must not write anywhere: use fmt.Errorf or fmt.Sprintf "+
					"and return the string", name, fset.Position(sel.Sel.Pos()).Line, sel.Sel.Name)
			}
			return true
		})
	}
	if scanned < 4 {
		t.Fatalf("scanned %d non-test files; the logging scan is vacuous", scanned)
	}
	t.Logf("no logging across %d source files", scanned)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Insertion sort: this package's tests must not need a sibling import either,
	// and the slice is five long.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
